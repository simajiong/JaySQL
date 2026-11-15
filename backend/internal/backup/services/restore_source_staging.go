package services

// 恢复源暂存（staging）相关实现。
//
// 背景与改造目标：
//   旧实现在服务端本机把备份集解压到 <logDir>/restore-src，再用本机客户端导入，
//   解压结果（PG 场景可达十几 GB）长期残留在服务端系统盘上（/opt/app/goinsight/media/backup/restore）。
//
// 新实现：
//   1. 备份文件先传输到「待恢复的目标机器」（DB 实例所在主机）；
//   2. 在目标机本地用 mktemp -d 建独立临时目录并解压，不再本机解压后传输解压结果；
//   3. 解压过程用一个带 trap 的 shell 脚本执行，任何异常退出都会清理临时目录；
//   4. 无论恢复成功还是失败，Go 侧 defer 的 cleanup 都会删除本次临时目录；
//   5. 删除前严格校验路径确为本次创建的临时目录，绝不误删机器上的原有数据。
//
// 目标机判定与凭据：
//   - DB 实例与 JaySQL 服务端同机（或 127.0.0.1/localhost）→ 本地模式，直接在本地建临时目录；
//   - 否则按实例 hostname 去 insight_backup_nodes 匹配已注册节点，复用其 ssh_user/ssh_port/ssh_private_key；
//   - 匹配不到节点时不阻断恢复：回退本地模式并打 WARN 日志（行为与改造前一致）。

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
)

// restoreStageMode 恢复源暂存模式
type restoreStageMode string

const (
	restoreStageLocal  restoreStageMode = "local"
	restoreStageRemote restoreStageMode = "remote"

	// restoreTempDirPrefix 临时目录名前缀，清理时用它做校验，确保只删本次创建的目录。
	restoreTempDirPrefix = "goinsight-restore-"
)

// restoreTargetAccess 目标机访问信息
type restoreTargetAccess struct {
	Mode     restoreStageMode
	Host     string // 目标机（远程模式）
	User     string
	Port     int
	KeyPath  string // SSH 私钥临时文件（进程内生成，用完删除）
	ToolsDir string // 节点工具目录，用于探测客户端
	Reason   string // 模式选择原因，写日志用
}

// restoreSourceStage 本次恢复的暂存现场
type restoreSourceStage struct {
	Access            *restoreTargetAccess
	Dir               string // 临时目录（mktemp -d 生成）
	Artifact          string // 备份文件在目标机上的路径（zip 场景）
	LocalDownloadPath string // 对象存储备份集下载到服务端本地的临时文件路径（执行结束后清理）
	NeedsUnzip        bool   // 是否需要解压
	Staged            bool   // 是否已完成传输/解压
}

func (a *restoreTargetAccess) IsRemote() bool {
	return a != nil && a.Mode == restoreStageRemote
}

// resolveRestoreTargetAccess 判定目标机并准备访问凭据。
func resolveRestoreTargetAccess(targetInstance *commonModels.InsightInstances, logWriter io.Writer) *restoreTargetAccess {
	host := ""
	if targetInstance != nil {
		host = strings.TrimSpace(targetInstance.Hostname)
	}
	if isLocalDatabaseHost(host) {
		access := &restoreTargetAccess{Mode: restoreStageLocal, Reason: "目标实例位于服务端本机，使用本地临时目录解压"}
		stageLog(logWriter, "[stage] 目标机判定=本机(%s)：%s", displayOrDefault(host, "127.0.0.1"), access.Reason)
		return access
	}
	node, err := findBackupNodeByHost(host)
	if err != nil || node == nil {
		access := &restoreTargetAccess{
			Mode:   restoreStageLocal,
			Reason: fmt.Sprintf("未找到目标机 %s 对应的备份节点，回退本地解压（如需在目标机本地解压，请先将该主机注册为备份节点）", host),
		}
		stageLog(logWriter, "[stage] 目标机判定=本机回退：%s", access.Reason)
		return access
	}
	// 节点私钥在库里是密文，写临时密钥文件前必须解密（历史明文原样返回）
	keyPath, err := writeTempSSHKey(decryptCredential(node.PrivateKey))
	if err != nil {
		access := &restoreTargetAccess{
			Mode:   restoreStageLocal,
			Reason: fmt.Sprintf("准备目标机 %s 的 SSH 私钥失败，回退本地解压：%v", host, err),
		}
		stageLog(logWriter, "[stage] 目标机判定=本机回退：%s", access.Reason)
		return access
	}
	access := &restoreTargetAccess{
		Mode:     restoreStageRemote,
		Host:     node.Host,
		User:     defaultBackupNodeSSHUser(node.SSHUser),
		Port:     node.Port,
		KeyPath:  keyPath,
		ToolsDir: strings.TrimSpace(node.ToolsDir),
		Reason:   fmt.Sprintf("目标机 %s 已注册为备份节点，复用节点 SSH 凭据，在目标机本地解压", host),
	}
	stageLog(logWriter, "[stage] 目标机判定=远程(%s@%s:%d)：%s", access.User, access.Host, access.Port, access.Reason)
	return access
}

// stageRestoreSource 完成「传输 + 解压」，成功返回可用于导入的源根目录。
func stageRestoreSource(ctx context.Context, stage *restoreSourceStage, backupFile string, needsUnzip bool, logWriter io.Writer) (string, error) {
	if stage == nil || stage.Access == nil {
		return "", fmt.Errorf("恢复暂存上下文未初始化")
	}
	stage.NeedsUnzip = needsUnzip
	backupFile = strings.TrimSpace(backupFile)
	if backupFile == "" {
		return "", fmt.Errorf("备份文件路径为空")
	}

	// 对象存储引用（st:<id>:///path）需要先从云端下载到服务端本地临时文件，
	// 再按本地/远程模式继续 staging。下载文件在 cleanupRestoreStage 中统一清理。
	localFile := backupFile
	if strings.HasPrefix(backupFile, "st:") {
		if !isObjectStorageRef(backupFile, 0) {
			return "", fmt.Errorf("备份文件引用未解析到本地可访问文件: %s", backupFile)
		}
		tmp, err := downloadObjectStorageArtifact(ctx, backupFile, logWriter)
		if err != nil {
			return "", fmt.Errorf("从对象存储下载备份文件失败: %w", err)
		}
		stage.LocalDownloadPath = tmp
		localFile = tmp
	}

	if !stage.Access.IsRemote() {
		return stageRestoreSourceLocal(ctx, stage, localFile, logWriter)
	}
	return stageRestoreSourceRemote(ctx, stage, localFile, logWriter)
}

// ---------- 本地模式 ----------

func stageRestoreSourceLocal(ctx context.Context, stage *restoreSourceStage, backupFile string, logWriter io.Writer) (string, error) {
	// 单文件备份（.sql / .sql.gz / .dump 等）无需解压，直接引用原文件，不产生临时目录。
	if !stage.NeedsUnzip {
		stage.Staged = true
		stageLog(logWriter, "[stage] 备份文件无需解压，直接引用: %s", backupFile)
		return "", nil
	}
	dir, err := runLocalCommandCapture(ctx, "", "/bin/sh", []string{"-lc", makeTempDirShell()}, nil, logWriter)
	if err != nil || !isManagedRestoreTempDir(dir) {
		return "", fmt.Errorf("创建本地临时目录失败: %v (dir=%q)", err, dir)
	}
	stage.Dir = dir
	stageLog(logWriter, "[stage] 本地临时目录已创建: %s (mktemp -d)", dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		stageLog(logWriter, "[stage] 设置临时目录权限失败（忽略）: %v", err)
	}
	if !stage.NeedsUnzip {
		stage.Staged = true
		stageLog(logWriter, "[stage] 备份文件无需解压，直接引用原文件: %s", backupFile)
		return "", nil
	}
	stageLog(logWriter, "[stage] 开始本地解压: %s → %s", backupFile, dir)
	// 本地模式沿用 Go 原生解压（不依赖目标机 unzip 命令），仅把落点从 logDir/restore-src 换成临时目录。
	if _, err := unzipFile(backupFile, dir); err != nil {
		// 解压失败：等价 trap 的异常路径，立即清理。
		cleanupRestoreStage(stage, logWriter)
		return "", fmt.Errorf("本地解压备份文件失败: %w", err)
	}
	stage.Staged = true
	stageLog(logWriter, "[stage] 本地解压完成，源根目录: %s", dir)
	return dir, nil
}

// ---------- 远程模式 ----------

func stageRestoreSourceRemote(ctx context.Context, stage *restoreSourceStage, backupFile string, logWriter io.Writer) (string, error) {
	a := stage.Access
	dirOut, err := runRemoteShellCapture(ctx, a, makeTempDirShell(), nil, logWriter)
	if err != nil || !isManagedRestoreTempDir(dirOut) {
		return "", fmt.Errorf("在目标机 %s 创建临时目录失败: %v (dir=%q)", a.Host, err, dirOut)
	}
	stage.Dir = dirOut
	stageLog(logWriter, "[stage] 目标机 %s 临时目录已创建: %s (mktemp -d)", a.Host, dirOut)

	remoteArtifact := dirOut + "/" + filepath.Base(backupFile)
	stage.Artifact = remoteArtifact
	if err := runRemoteShell(ctx, a, fmt.Sprintf("chmod 700 %s", shellQuote(dirOut)), nil, logWriter); err != nil {
		stageLog(logWriter, "[stage] 设置目标机临时目录权限失败（忽略）: %v", err)
	}

	// —— 阶段一：传输备份文件到目标机（只传备份文件本身，不传任何解压结果）——
	sizeBytes := int64(0)
	if stat, statErr := os.Stat(backupFile); statErr == nil {
		sizeBytes = stat.Size()
	}
	stageLog(logWriter, "[stage] 开始传输备份文件到目标机 %s: %s → %s (%s)", a.Host, backupFile, remoteArtifact, formatBytesValue(sizeBytes))
	if err := runSCPUpload(ctx, a, backupFile, remoteArtifact, logWriter); err != nil {
		cleanupRestoreStage(stage, logWriter)
		return "", fmt.Errorf("传输备份文件到目标机 %s 失败: %w", a.Host, err)
	}
	stageLog(logWriter, "[stage] 备份文件传输完成: %s", remoteArtifact)

	if !stage.NeedsUnzip {
		stage.Staged = true
		stageLog(logWriter, "[stage] 备份文件无需解压，目标机直接引用: %s", remoteArtifact)
		return "", nil
	}

	// —— 阶段二：目标机本地解压 ——
	// 脚本自带 trap：仅在解压异常（非 0 退出）时清理临时目录；成功退出保留文件供导入阶段使用。
	script := fmt.Sprintf(`set -u
TMPDIR=%s
cleanup_on_error() {
  rc=$?
  if [ "$rc" -ne 0 ]; then
    case "$TMPDIR" in
      *%s*) rm -rf "$TMPDIR" && echo "[stage] 解压失败，已清理目标机临时目录: $TMPDIR" ;;
      *) echo "[stage] 解压失败，但临时目录路径不符合清理规则，跳过删除: $TMPDIR" ;;
    esac
  fi
}
trap cleanup_on_error EXIT INT TERM
echo "[stage] 目标机开始解压: %s → %s"
unzip -q -o %s -d %s
rc=$?
if [ "$rc" -ne 0 ]; then exit "$rc"; fi
echo "[stage] 目标机解压完成，源根目录: %s"
`, shellQuote(dirOut), restoreTempDirPrefix,
		shellQuote(remoteArtifact), shellQuote(dirOut),
		shellQuote(remoteArtifact), shellQuote(dirOut),
		shellQuote(dirOut))
	stageLog(logWriter, "[stage] 目标机 %s 开始本地解压", a.Host)
	if err := runRemoteShell(ctx, a, script, nil, logWriter); err != nil {
		// 解压脚本内部已 trap 清理；这里再兜底一次，避免 trap 未生效时残留。
		cleanupRestoreStage(stage, logWriter)
		return "", fmt.Errorf("目标机 %s 解压备份文件失败: %w", a.Host, err)
	}
	stage.Staged = true
	return dirOut, nil
}

// ---------- 清理 ----------

// cleanupRestoreStage 删除本次暂存产生的临时目录。
// 无论恢复成功还是失败（含中途报错返回）都由调用方 defer 触发。
func cleanupRestoreStage(stage *restoreSourceStage, logWriter io.Writer) {
	if stage == nil {
		return
	}
	// 清理对象存储下载到服务端本地的临时文件（优先，与 stage.Dir 无关）。
	if p := strings.TrimSpace(stage.LocalDownloadPath); p != "" {
		if err := os.Remove(p); err == nil {
			stageLog(logWriter, "[stage] 已清理本地下载的临时文件: %s", p)
		} else if !os.IsNotExist(err) {
			stageLog(logWriter, "[stage] 清理本地下载临时文件失败: %s (%v)", p, err)
		}
		stage.LocalDownloadPath = ""
	}
	if strings.TrimSpace(stage.Dir) == "" {
		if stage.Access != nil && stage.Access.KeyPath != "" {
			_ = os.Remove(stage.Access.KeyPath)
			stage.Access.KeyPath = ""
		}
		return
	}
	dir := strings.TrimSpace(stage.Dir)
	if !isManagedRestoreTempDir(dir) {
		stageLog(logWriter, "[stage] 拒绝清理：路径不是本次恢复创建的临时目录: %s", dir)
		return
	}
	defer func() {
		if stage.Access != nil && stage.Access.KeyPath != "" {
			_ = os.Remove(stage.Access.KeyPath)
			stage.Access.KeyPath = ""
		}
	}()
	if !stage.Access.IsRemote() {
		if err := os.RemoveAll(dir); err != nil {
			stageLog(logWriter, "[stage] 清理本地临时目录失败: %s (%v)", dir, err)
			return
		}
		stageLog(logWriter, "[stage] 已清理本地临时目录: %s", dir)
		stage.Dir = ""
		return
	}
	// 远程清理同样先校验：只删符合本次临时目录规则、且确为目录的路径。
	script := fmt.Sprintf(`set -u
D=%s
case "$D" in
  *%s*) ;;
  *) echo "[stage] 拒绝清理：路径不符合本次临时目录规则: $D"; exit 0 ;;
esac
if [ -z "$D" ] || [ "$D" = "/" ] || [ "$D" = "/tmp" ] || [ "$D" = "/var/tmp" ]; then
  echo "[stage] 拒绝清理：路径过于危险: $D"; exit 0
fi
if [ ! -d "$D" ]; then
  echo "[stage] 目标机临时目录已不存在，无需清理: $D"; exit 0
fi
rm -rf "$D" && echo "[stage] 已清理目标机 %s 临时目录: $D"
`, shellQuote(dir), restoreTempDirPrefix, stage.Access.Host)
	if err := runRemoteShell(context.Background(), stage.Access, script, nil, logWriter); err != nil {
		stageLog(logWriter, "[stage] 清理目标机临时目录失败: %s (%v)", dir, err)
		return
	}
	stage.Dir = ""
}

// downloadObjectStorageArtifact 把 st:<id>:///path 指向的对象存储备份文件下载到服务端本地临时文件。
// 返回本地临时文件路径（调用方负责在恢复结束后清理）。
func downloadObjectStorageArtifact(ctx context.Context, backupFile string, logWriter io.Writer) (string, error) {
	rel, targetID, err := ParseStorageArtifactPath(backupFile)
	if err != nil {
		return "", fmt.Errorf("解析对象存储引用失败 %q: %w", backupFile, err)
	}
	var target backupModels.InsightBackupStorageTargets
	if err := global.App.DB.Where("id = ? AND deleted_at IS NULL", targetID).Take(&target).Error; err != nil {
		return "", fmt.Errorf("加载存储目标 %d 失败: %w", targetID, err)
	}
	_, _, s3AK, s3SK, err := decryptStorageTargetCredentials(&target)
	if err != nil {
		return "", fmt.Errorf("解密存储目标 %d 凭据失败: %w", targetID, err)
	}
	if strings.TrimSpace(s3AK) == "" || strings.TrimSpace(s3SK) == "" {
		return "", fmt.Errorf("存储目标 %d 未配置 AK/SK，无法下载对象", targetID)
	}

	client, err := NewS3Client(S3ClientOptions{
		Endpoint:       strings.TrimSpace(target.S3Endpoint),
		Region:         strings.TrimSpace(target.S3Region),
		Bucket:         strings.TrimSpace(target.S3Bucket),
		AccessKey:      s3AK,
		SecretKey:      s3SK,
		ForcePathStyle: target.S3ForcePathStyle,
		HTTPTimeout:    30 * time.Minute,
	})
	if err != nil {
		return "", fmt.Errorf("创建 S3 客户端失败: %w", err)
	}

	objectKey := strings.TrimPrefix(rel, "/")
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("goinsight-restore-download-%d-%s", time.Now().UnixNano(), sanitizeRestoreFileName(filepath.Base(objectKey))))

	stageLog(logWriter, "[stage] 开始从对象存储下载备份文件: %s", backupFile)
	n, err := client.DownloadObject(ctx, objectKey, tmpFile)
	if err != nil {
		_ = os.Remove(tmpFile)
		return "", fmt.Errorf("下载对象 %s 失败: %w", objectKey, err)
	}
	stageLog(logWriter, "[stage] 对象存储下载完成: %s (%s)", tmpFile, formatBytesValue(n))
	return tmpFile, nil
}

// sanitizeRestoreFileName 把对象键中的路径分隔符等危险字符替换成下划线，避免本地落盘时穿越目录。
func sanitizeRestoreFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "artifact"
	}
	name = filepath.Base(filepath.ToSlash(name))
	name = strings.ReplaceAll(name, "..", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	if name == "" {
		return "artifact"
	}
	return name
}

// isManagedRestoreTempDir 校验路径确为「本次恢复创建的临时目录」。
// 要求：绝对路径 + 末段以 goinsight-restore- 开头 + 位于系统临时目录下 + 不是临时目录本身。
func isManagedRestoreTempDir(path string) bool {
	p := strings.TrimSpace(path)
	if p == "" || !filepath.IsAbs(p) {
		return false
	}
	cleaned := filepath.Clean(p)
	if cleaned != p {
		return false
	}
	parent := filepath.Dir(cleaned)
	tempRoot := filepath.Clean(os.TempDir())
	if parent != "/tmp" && parent != "/var/tmp" && parent != tempRoot {
		return false
	}
	base := filepath.Base(cleaned)
	if !strings.HasPrefix(base, restoreTempDirPrefix) {
		return false
	}
	return base != "." && base != ".." && base != string(os.PathSeparator)
}

// ---------- 远程执行 / 传输 ----------

func (a *restoreTargetAccess) sshBaseArgs() []string {
	port := a.Port
	if port <= 0 {
		port = 22
	}
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=15",
		"-p", fmt.Sprintf("%d", port),
	}
	if strings.TrimSpace(a.KeyPath) != "" {
		args = append(args, "-i", a.KeyPath)
	}
	return args
}

func (a *restoreTargetAccess) target() string {
	return fmt.Sprintf("%s@%s", a.User, a.Host)
}

// buildRemoteScript 把环境变量导出 + 业务脚本拼成一段可通过 stdin 交给 bash -s 的脚本。
func buildRemoteScript(env map[string]string, script string) string {
	var b strings.Builder
	for _, k := range sortedKeys(env) {
		v := env[k]
		if strings.TrimSpace(k) == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("export %s=%s\n", strings.TrimSpace(k), shellQuote(v)))
	}
	b.WriteString(script)
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func runRemoteShell(ctx context.Context, a *restoreTargetAccess, script string, env map[string]string, logWriter io.Writer) error {
	return runRemoteShellWithStdin(ctx, a, script, env, logWriter, nil)
}

func runRemoteShellCapture(ctx context.Context, a *restoreTargetAccess, script string, env map[string]string, logWriter io.Writer) (string, error) {
	var buf strings.Builder
	var out io.Writer = &buf
	if logWriter != nil {
		out = io.MultiWriter(logWriter, &buf)
	}
	err := runRemoteShellWithStdin(ctx, a, script, env, nil, out)
	return strings.TrimSpace(buf.String()), err
}

func runRemoteShellWithStdin(ctx context.Context, a *restoreTargetAccess, script string, env map[string]string, logWriter io.Writer, stdoutOverride io.Writer) error {
	if a == nil || !a.IsRemote() {
		return fmt.Errorf("SSH 目标机信息不可用")
	}
	args := append(a.sshBaseArgs(), a.target(), "bash -s")
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = strings.NewReader(buildRemoteScript(env, script))
	if stdoutOverride != nil {
		cmd.Stdout = stdoutOverride
	} else if logWriter != nil {
		cmd.Stdout = logWriter
	}
	if logWriter != nil {
		cmd.Stderr = logWriter
	} else {
		cmd.Stderr = io.Discard
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	waitErr := cmd.Wait()
	if waitErr != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return waitErr
}

// runSCPUpload 把本地文件传输到目标机。
func runSCPUpload(ctx context.Context, a *restoreTargetAccess, localPath string, remotePath string, logWriter io.Writer) error {
	args := append(a.sshBaseArgs(), localPath, fmt.Sprintf("%s:%s", a.target(), remotePath))
	cmd := exec.CommandContext(ctx, "scp", args...)
	if logWriter != nil {
		cmd.Stdout = logWriter
		cmd.Stderr = logWriter
	} else {
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	waitErr := cmd.Wait()
	if waitErr != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return waitErr
}

// resolveRemoteToolPath 探测目标机上的客户端工具绝对路径；找不到就明确报错（不静默降级）。
func resolveRemoteToolPath(ctx context.Context, a *restoreTargetAccess, toolName string, logWriter io.Writer) (string, error) {
	if a == nil || !a.IsRemote() {
		return "", fmt.Errorf("SSH 目标机信息不可用")
	}
	candidates := []string{fmt.Sprintf("command -v %s", toolName)}
	if dir := strings.TrimSpace(a.ToolsDir); dir != "" {
		candidates = append([]string{fmt.Sprintf("[ -x %s ] && echo %s", shellQuote(dir+"/"+toolName), shellQuote(dir+"/"+toolName))}, candidates...)
	}
	// 金仓客户端装机目录不在默认 PATH 里（sys_restore / ksql），补一轮固定路径探测，
	// 否则「目标机有客户端但没配 PATH」会被误判成未安装。
	for _, dir := range []string{
		"/opt/Kingbase/ES/V8/Server/bin",
		"/opt/Kingbase/ES/V8/bin",
		"/opt/Kingbase/Server/bin",
	} {
		full := dir + "/" + toolName
		candidates = append(candidates, fmt.Sprintf("[ -x %s ] && echo %s", shellQuote(full), shellQuote(full)))
	}
	script := strings.Join(candidates, "\n") + "\n"
	out, err := runRemoteShellCapture(ctx, a, script, nil, nil)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("目标机 %s 上未找到 %s 客户端，请先在该主机安装 %s 后再执行恢复", a.Host, toolName, toolName)
	}
	path := strings.TrimSpace(strings.Split(out, "\n")[0])
	stageLog(logWriter, "[stage] 目标机 %s 客户端 %s 路径: %s", a.Host, toolName, path)
	return path, nil
}

// ---------- 辅助 ----------

func makeTempDirShell() string {
	// Linux 支持 /tmp/prefixXXXXXXXX 模板；BSD/macOS 需要 -t 形式，这里两种都覆盖。
	return fmt.Sprintf(`D=$(mktemp -d /tmp/%sXXXXXXXX 2>/dev/null || mktemp -d -t %s); echo "$D"`, restoreTempDirPrefix, restoreTempDirPrefix)
}

func stageLog(logWriter io.Writer, format string, args ...any) {
	if logWriter == nil {
		return
	}
	_, _ = fmt.Fprintf(logWriter, format+"\n", args...)
}

func isLocalDatabaseHost(host string) bool {
	h := strings.TrimSpace(strings.ToLower(host))
	if h == "" {
		return true
	}
	switch h {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	if hn, err := os.Hostname(); err == nil && strings.EqualFold(strings.TrimSpace(hn), h) {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if ipNet.IP.String() == h {
			return true
		}
	}
	return false
}

type backupNodeSSHInfo struct {
	ID         uint64
	Host       string
	Port       int
	SSHUser    string
	PrivateKey string
	ToolsDir   string
}

// findBackupNodeByHost 按主机名匹配已注册的备份节点，复用其 SSH 凭据。
func findBackupNodeByHost(host string) (*backupNodeSSHInfo, error) {
	h := strings.TrimSpace(host)
	if h == "" {
		return nil, nil
	}
	var row backupNodeSSHInfo
	err := global.App.DB.Table("insight_backup_nodes").
		Select("id, host, port, ssh_user, ssh_private_key, tools_dir").
		Where("host = ?", h).
		Order("id desc").
		Limit(1).
		Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(row.Host) == "" {
		return nil, nil
	}
	if strings.TrimSpace(row.PrivateKey) == "" {
		// 无私钥：仍返回节点信息，由调用方决定（密钥登录不可用时 SSH 会失败并回退）。
		return &row, nil
	}
	return &row, nil
}

func writeTempSSHKey(privateKey string) (string, error) {
	key := strings.TrimSpace(privateKey)
	if key == "" {
		return "", fmt.Errorf("节点未配置 SSH 私钥")
	}
	if !strings.HasSuffix(key, "\n") {
		key += "\n"
	}
	file, err := os.CreateTemp("", "gi-restore-key-*.pem")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.WriteString(key); err != nil {
		file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// formatBytesValue 仅用于日志展示，避免依赖其它包的大小格式化实现。
func formatBytesValue(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	idx := -1
	for value >= unit && idx < len(units)-1 {
		value /= unit
		idx++
	}
	if idx < 0 {
		return fmt.Sprintf("%d B", size)
	}
	return fmt.Sprintf("%.1f %s", value, units[idx])
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
