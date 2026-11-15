package services

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/datatypes"
)

type BackupExecutionInstance struct {
	Hostname    string
	Port        int
	User        string
	Password    string
	DBType      string
	ExtraConfig datatypes.JSON
}

// BackupExecutionStorageTarget 是执行链路使用的存储目标快照。
//
// 两组字段互斥生效，由 Type 决定走哪条链路：
//   - LOCAL_SERVER：Host/Port/SSH* 一组，通过 SSH 读写文件系统；
//   - OSS_ALIYUN/COS_TENCENT/S3_MINIO/OBS_HUAWEI：S3* 一组，通过 S3 兼容接口 LIST+GET 读写对象。
//
// 对象存储字段为空时（历史快照、或下发方未填充）对应的读取链路会直接报「配置不完整」，
// 不会静默退化成 SSH —— 两类仓库的寻址方式不同，退化只会得到误导性错误。
type BackupExecutionStorageTarget struct {
	ID            uint64
	Type          string
	Host          string
	Port          int
	BasePath      string
	SSHUser       string
	SSHPassword   string
	SSHPrivateKey string

	S3Endpoint       string
	S3Region         string
	S3Bucket         string
	S3AccessKey      string
	S3SecretKey      string
	S3ForcePathStyle bool
}

// storageTargetTypeOf 取存储目标类型，nil 安全。
func storageTargetTypeOf(target *BackupExecutionStorageTarget) string {
	if target == nil {
		return ""
	}
	return strings.TrimSpace(target.Type)
}

type BackupExecutionInput struct {
	RunID            uint64
	TaskID           string
	SourceInstanceID uint64
	InstanceBizID    string
	RunSeq           uint64
	DBType           string
	DriverType       string
	DriverConfig     datatypes.JSON
	SourceDBName     string
	BackupType       string
	ObjectMode       string
	ObjectSpec       datatypes.JSON
	ObjectSummary    string
	OutputBaseDir    string
	CreatedBy        string
	Instance         BackupExecutionInstance
	StorageTarget    *BackupExecutionStorageTarget
	DirectStream     bool
	StreamSink       BackupStreamSink
	ExtraLogWriter   io.Writer
}

// RunDirSpec 汇总批次目录所需信息，保证各处路径构造完全同源
func (i BackupExecutionInput) RunDirSpec() BackupRunDirSpec {
	return BackupRunDirSpec{
		SourceInstanceID: i.SourceInstanceID,
		InstanceBizID:    i.InstanceBizID,
		TaskID:           i.TaskID,
		RunID:            i.RunID,
		RunSeq:           i.RunSeq,
	}
}

type BackupStreamSink interface {
	io.WriteCloser
	RemoteTarget() string
}

type BackupExecutionResult struct {
	ArtifactName        string
	ArtifactPath        string
	StorageArtifactPath string
	StorageTargetID     uint64
	SizeBytes           int64
	Sha256              string
	ServerVersion       string
	Tool                string
	ToolImage           string
	LogPath             string
	LogDir              string
	OutputDir           string
	ObjectMode          string
	ObjectSpec          datatypes.JSON
	ObjectSummary       string
	LegacyDBName        string
	SourceDBName        string
	// MetadataVerified / ChecksumVerified 由备份完成时「写附属文件 + 立即回读校验」得出，
	// 经 persistBackupSetSuccess 落库到 insight_backup_sets，供恢复预检直接读取。
	// 恢复侧不再依赖现场 find 文件，避免直传/跨节点场景下产物与附属文件不在同一处。
	MetadataVerified bool
	ChecksumVerified bool
}

func formatCommandFailureOutput(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "\r\n", "\n")
	trimmed = strings.ReplaceAll(trimmed, "\r", "\n")
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 6 {
		lines = lines[:6]
	}
	return strings.Join(lines, " | ")
}

func isInstanceHostLocal(host string) bool {
	h := strings.TrimSpace(strings.ToLower(host))
	if h == "" || h == "localhost" {
		return true
	}
	if strings.HasPrefix(h, "127.") {
		return true
	}
	if h == "::1" || h == "[::1]" {
		return true
	}
	if net.ParseIP(h) != nil {
		if ifaces, err := net.InterfaceAddrs(); err == nil {
			for _, a := range ifaces {
				var ip net.IP
				switch v := a.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip != nil && ip.String() == h {
					return true
				}
			}
		}
		return false
	}
	if addrs, err := net.LookupHost(h); err == nil {
		localSet := map[string]struct{}{}
		if ifaces, err := net.InterfaceAddrs(); err == nil {
			for _, a := range ifaces {
				switch v := a.(type) {
				case *net.IPNet:
					localSet[v.IP.String()] = struct{}{}
				case *net.IPAddr:
					localSet[v.IP.String()] = struct{}{}
				}
			}
		}
		for _, a := range addrs {
			if a == "127.0.0.1" || a == "::1" {
				return true
			}
			if _, ok := localSet[a]; ok {
				return true
			}
		}
	}
	return false
}

func chownPathForPGBackRest(ctx context.Context, path string, owner string, preferredGroup string) error {
	owner = strings.TrimSpace(owner)
	preferredGroup = strings.TrimSpace(preferredGroup)
	if owner == "" || strings.TrimSpace(path) == "" {
		return nil
	}
	attempted := []string{}
	groups := []string{}
	if preferredGroup != "" {
		groups = append(groups, preferredGroup)
	}
	if preferredGroup == "" || !strings.EqualFold(preferredGroup, owner) {
		groups = append(groups, owner)
	}
	var lastErr error
	for _, group := range groups {
		spec := owner + ":" + strings.TrimSpace(group)
		out, err := runLocalCommandCaptureAsRoot(ctx, "", "chown", []string{spec, path}, nil, nil)
		if err == nil {
			return nil
		}
		msg := spec
		if detail := formatCommandFailureOutput(out); detail != "" {
			msg += " -> " + detail
		}
		attempted = append(attempted, msg)
		lastErr = err
	}
	if len(attempted) > 0 {
		return fmt.Errorf("%w（尝试=%s）", lastErr, strings.Join(attempted, " ; "))
	}
	return lastErr
}

func validatePostgresDataDirAccessible(ctx context.Context, path string) error {
	dir := strings.TrimSpace(path)
	if dir == "" {
		return fmt.Errorf("PostgreSQL 数据目录为空")
	}
	output, err := runLocalCommandCaptureAsDBUser(ctx, "PostgreSQL", "/", "/bin/sh", []string{"-lc", "test -d " + shellQuote(dir) + " && test -x " + shellQuote(dir) + " && echo DIR_OK"}, nil, nil)
	lines := strings.Split(strings.ReplaceAll(output, "\r", ""), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if line == "DIR_OK" {
			return nil
		}
		break
	}
	if detail := formatCommandFailureOutput(output); detail != "" {
		return fmt.Errorf("pgBackRest 需要访问本机 PostgreSQL 数据目录，当前目录不可用: %s（输出=%s）", dir, detail)
	}
	if err != nil {
		return fmt.Errorf("pgBackRest 需要访问本机 PostgreSQL 数据目录，当前目录不可用: %s（原因=%v）", dir, err)
	}
	return fmt.Errorf("pgBackRest 需要访问本机 PostgreSQL 数据目录，当前目录不可用: %s", dir)
}

func detectSharedGroupForUsers(ctx context.Context, ownerUser string, currentUser string) string {
	ownerUser = strings.TrimSpace(ownerUser)
	currentUser = strings.TrimSpace(currentUser)
	if ownerUser == "" || currentUser == "" {
		return currentUser
	}
	groupOut, grpErr := runLocalCommandCaptureAsRoot(ctx, "", "id", []string{"-gn", ownerUser}, nil, nil)
	if grpErr != nil {
		return currentUser
	}
	primaryGroup := strings.TrimSpace(groupOut)
	if primaryGroup == "" {
		return currentUser
	}
	groupsOut, groupsErr := runLocalCommandCaptureAsRoot(ctx, "", "id", []string{"-Gn", currentUser}, nil, nil)
	if groupsErr != nil {
		return currentUser
	}
	for _, group := range strings.Fields(groupsOut) {
		if strings.EqualFold(strings.TrimSpace(group), primaryGroup) {
			return primaryGroup
		}
	}
	return currentUser
}

// 任务「高级选项」中「单次执行最长运行时间（小时）」的取值边界。
const (
	// DriverConfigKeyMaxRuntimeHours driver_config 中该参数的键名（备份/恢复共用）。
	DriverConfigKeyMaxRuntimeHours = "max_runtime_hours"
	maxRuntimeHoursMin             = 1
	maxRuntimeHoursMax             = 720 // 30 天，避免误配置成近乎无限
)

// resolveDriverConfigMaxRuntimeHours 从 driver_config 原始 JSON 读取任务级「单次执行最长运行时间（小时）」。
// 返回 0 表示未配置或取值非法，上层将回落到环境变量 → 平台默认值。
func resolveDriverConfigMaxRuntimeHours(raw datatypes.JSON) int {
	if len(raw) == 0 {
		return 0
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return 0
	}
	hours := anyToInt(data[DriverConfigKeyMaxRuntimeHours], 0)
	if hours < maxRuntimeHoursMin || hours > maxRuntimeHoursMax {
		return 0
	}
	return hours
}

func ExecuteBackupJob(input BackupExecutionInput) (*BackupExecutionResult, error) {
	if strings.TrimSpace(input.DBType) == "" {
		return nil, fmt.Errorf("DBType 不能为空")
	}
	driverType := normalizeEnumOrDefault(input.DriverType, "LOGICAL")
	if input.OutputBaseDir == "" {
		return nil, fmt.Errorf("输出目录不能为空")
	}
	runDirSpec := input.RunDirSpec()
	dir := resolveBackupRunDirByDriver(input.OutputBaseDir, driverType, input.DBType, runDirSpec)
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	logPath := resolveBackupRunLogPathByDriver(input.OutputBaseDir, driverType, input.DBType, runDirSpec)
	pgRuntimeOwner := ""
	pgRuntimeGroup := ""
	if driverType == "PG_BACKREST" {
		pgOSUser, pgOSUserErr := detectDatabaseOSUser("PostgreSQL")
		if pgOSUserErr == nil && strings.TrimSpace(pgOSUser) != "" && strings.TrimSpace(pgOSUser) != strings.TrimSpace(currentOSUser()) {
			effectiveUser := strings.TrimSpace(pgOSUser)
			pgRuntimeOwner = effectiveUser
			if _, mkErr := runLocalCommandCaptureAsRoot(context.Background(), "", "mkdir", []string{"-p", filepath.Dir(logPath)}, nil, nil); mkErr != nil {
				return nil, fmt.Errorf("创建日志目录失败: %w（目录=%s）", mkErr, filepath.Dir(logPath))
			}
			logDir := filepath.Dir(logPath)
			currentUser := strings.TrimSpace(currentOSUser())
			sharedGroup := detectSharedGroupForUsers(context.Background(), effectiveUser, currentUser)
			if strings.TrimSpace(sharedGroup) == "" {
				sharedGroup = currentUser
			}
			pgRuntimeGroup = strings.TrimSpace(sharedGroup)
			dirs := []string{dir, logDir}
			for _, d := range dirs {
				if chErr := chownPathForPGBackRest(context.Background(), d, effectiveUser, sharedGroup); chErr != nil {
					return nil, fmt.Errorf("调整备份输出目录属主失败: %w（目录=%s）", chErr, d)
				}
				if chmodOut, chErr := runLocalCommandCaptureAsRoot(context.Background(), "", "chmod", []string{"2775", d}, nil, nil); chErr != nil {
					if detail := formatCommandFailureOutput(chmodOut); detail != "" {
						return nil, fmt.Errorf("调整备份输出目录权限失败: %w（目录=%s；输出=%s）", chErr, d, detail)
					}
					return nil, fmt.Errorf("调整备份输出目录权限失败: %w（目录=%s）", chErr, d)
				}
			}
		}
	}
	localLogFile, err := openBackupRunLogger(logPath)
	if err != nil && driverType == "PG_BACKREST" && strings.TrimSpace(pgRuntimeOwner) != "" && isPermissionDeniedErr(err) {
		logDir := filepath.Dir(logPath)
		if repairErr := chownPathForPGBackRest(context.Background(), logDir, pgRuntimeOwner, pgRuntimeGroup); repairErr == nil {
			_, _ = runLocalCommandCaptureAsRoot(context.Background(), "", "chmod", []string{"2775", logDir}, nil, nil)
			localLogFile, err = openBackupRunLogger(logPath)
		}
	}
	if err != nil {
		return nil, err
	}
	defer localLogFile.Close()
	if driverType == "PG_BACKREST" && strings.TrimSpace(pgRuntimeOwner) != "" {
		group := strings.TrimSpace(pgRuntimeGroup)
		if group == "" {
			group = pgRuntimeOwner
		}
		_, _ = runLocalCommandCaptureAsRoot(context.Background(), "", "chown", []string{pgRuntimeOwner + ":" + group, logPath}, nil, nil)
		_, _ = runLocalCommandCaptureAsRoot(context.Background(), "", "chmod", []string{"0664", logPath}, nil, nil)
	}
	var logFile backupRunLoggerLike = localLogFile
	if input.ExtraLogWriter != nil {
		logFile = &teeBackupRunLogger{local: localLogFile, extra: input.ExtraLogWriter, path: logPath}
	}
	logFile.Infof("备份开始 task_id=%s run_id=%d db_type=%s driver_type=%s backup_type=%s object_mode=%s", input.TaskID, input.RunID, input.DBType, driverType, input.BackupType, input.ObjectMode)
	logFile.Infof("输出目录: %s", dir)
	logFile.Infof("日志文件: %s", logPath)

	var rawSpec map[string]any
	if len(input.ObjectSpec) > 0 {
		_ = json.Unmarshal(input.ObjectSpec, &rawSpec)
	}
	normalized, err := normalizeObjectSpec(input.DBType, driverType, input.ObjectMode, input.SourceDBName, input.BackupType, rawSpec)
	if err != nil {
		logFile.Errorf("备份对象归一化失败: %v", err)
		return nil, err
	}
	logFile.Infof("对象摘要: %s", normalized.ObjectSummary)

	extra := parseExtraConfig(input.Instance.ExtraConfig)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, runControl := attachBackupRunControl(ctx, input.RunID, logFile)
	defer runControl.Close()
	// 任务级兜底上限：任务「高级选项」的「单次执行最长运行时间」，未配置时回落环境变量/平台默认值。
	// 在此统一注入后，mysqldump / pg_dump / xtrabackup / pgBackRest 四条执行链路共用同一上限。
	if hours := resolveDriverConfigMaxRuntimeHours(input.DriverConfig); hours > 0 {
		ctx = withBackupProcessMaxRuntime(ctx, hours)
		logFile.Infof("单次执行最长运行时间: %d 小时（任务级配置）", hours)
	}

	if isMySQLPhysicalDriverType(driverType) {
		return executeMySQLPhysicalBackupJob(ctx, runControl, logFile, dir, logPath, input, normalized, extra)
	}
	if isPostgresPhysicalDriverType(driverType) {
		return executePhysicalBackupJob(ctx, runControl, logFile, dir, logPath, input, normalized, extra)
	}

	jobs := []dumpJob{}
	switch {
	case strings.EqualFold(strings.TrimSpace(input.DBType), "MySQL"):
		jobs = normalized.Jobs
	case isPostgresLogicalFamily(input.DBType):
		// PostgreSQL / KingbaseES 共用同一套对象模型：ALL = 逐库 sys_dump，
		// 自定义 = 按库/schema/表取子集。金仓的库清单同样走 PG 协议查询。
		if normalized.ObjectMode == "ALL" {
			sslmode := extra["sslmode"]
			defaultDB := firstNonEmpty(extra["database"], logicalDefaultDatabase(input.DBType))
			dbs, err := listPostgresDatabases(input.Instance.Hostname, input.Instance.Port, input.Instance.User, input.Instance.Password, defaultDB, sslmode)
			if err != nil {
				return nil, err
			}
			for _, db := range dbs {
				jobs = append(jobs, dumpJob{DBName: db, FileName: fmt.Sprintf("%s.dump", db)})
			}
		} else {
			jobs = normalized.Jobs
		}
	default:
		return nil, fmt.Errorf("不支持的 DB 类型")
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("未生成备份对象")
	}

	outputFiles := make([]string, 0, len(jobs))
	serverVersion := ""
	tool := ""
	toolImage := ""
	artifactPlan, planErr := PlanBackupArtifact(input.DBType, driverType, input.ObjectMode, input.SourceDBName, input.BackupType, input.ObjectSpec, input.DriverConfig)
	if planErr != nil {
		return nil, planErr
	}
	artifactName := strings.TrimSpace(artifactPlan.ArtifactName)
	var resultSizeBytes int64
	var unifiedPlan *unifiedBackupExecutionPlan
	if strings.EqualFold(strings.TrimSpace(driverType), "LOGICAL") {
		unifiedPlan, err = buildUnifiedBackupExecutionPlanForRun(ctx, input)
		if err != nil {
			return nil, err
		}
	}
	useStream := input.DirectStream && input.StreamSink != nil
	var artifactStream *backupArtifactStream
	if useStream {
		artifactStream = newBackupArtifactStream(input.StreamSink, artifactPlan.StreamCompression, artifactPlan.CompressionLevel)
		logFile.Infof("[DirectStream 零落盘模式] 已启用流式直写 target=%s artifact=%s compression=%s level=%s", input.StreamSink.RemoteTarget(), artifactName, artifactPlan.StreamCompression, artifactPlan.CompressionLevel)
	}

	switch {
	case strings.EqualFold(strings.TrimSpace(input.DBType), "MySQL"):
		if err := runControl.Checkpoint(logFile); err != nil {
			return nil, err
		}
		if unifiedPlan == nil || unifiedPlan.Logical == nil || unifiedPlan.Logical.Tool == nil {
			return nil, fmt.Errorf("逻辑备份统一执行计划未生成 MySQL 工具信息")
		}
		serverVersion = strings.TrimSpace(unifiedPlan.ServerVersion)
		logFile.Infof("MySQL 版本: %s", serverVersion)
		toolSpec := unifiedPlan.Logical.Tool
		tool = toolSpec.Name
		toolImage = toolSpec.Source
		logFile.Infof("命中备份工具: %s (%s)", toolSpec.Path, toolSpec.Source)
		mysqlServerInfo := parseDBVersionInfo(serverVersion)
		mysqlClientVersion := probeCommandVersion(toolSpec.Path)
		mysqlClientInfo := parseDBVersionInfo(mysqlClientVersion)
		logFile.Infof("MySQL 客户端版本: %s", displayOrDefault(strings.TrimSpace(mysqlClientVersion), "unknown"))
		for _, job := range jobs {
			mysqlTabs := job.MySQLTabs
			mysqlIgnoreTabs := uniqueStrings(job.MySQLExcludeTabs)
			if len(job.MySQLExcludePatterns) > 0 {
				all, listErr := listMySQLTables(input.Instance.Hostname, input.Instance.Port, input.Instance.User, input.Instance.Password, job.DBName)
				if listErr != nil {
					logFile.Errorf("读取 MySQL 表清单失败 database=%s: %v", job.DBName, listErr)
					return nil, listErr
				}
				for _, p := range uniqueStrings(job.MySQLExcludePatterns) {
					for _, t := range all {
						if matchWildcard(t, p) {
							mysqlIgnoreTabs = append(mysqlIgnoreTabs, t)
						}
					}
				}
				mysqlIgnoreTabs = uniqueStrings(mysqlIgnoreTabs)
			}
			if len(mysqlTabs) > 0 && len(mysqlIgnoreTabs) > 0 {
				filtered := make([]string, 0, len(mysqlTabs))
				ignoreSet := map[string]struct{}{}
				for _, t := range mysqlIgnoreTabs {
					ignoreSet[t] = struct{}{}
				}
				for _, t := range mysqlTabs {
					if _, ok := ignoreSet[t]; ok {
						continue
					}
					filtered = append(filtered, t)
				}
				mysqlTabs = filtered
				if len(mysqlTabs) == 0 {
					logFile.Errorf("MySQL 备份对象为空 database=%s", job.DBName)
					return nil, fmt.Errorf("MySQL 备份对象为空：已全部被排除")
				}
			}
			args := []string{
				"-h", input.Instance.Hostname,
				"-P", fmt.Sprintf("%d", input.Instance.Port),
				"-u", input.Instance.User,
				"--single-transaction",
				"--quick",
				"--hex-blob",
			}
			targetPath := filepath.Join(dir, job.FileName)
			if !useStream {
				args = append(args, "--result-file="+targetPath)
			}
			if shouldDisableMySQLGTIDPurged(mysqlServerInfo, mysqlClientInfo) {
				args = append(args, "--set-gtid-purged=OFF")
			}
			if shouldDisableMySQLColumnStatistics(mysqlClientInfo) {
				args = append(args, "--column-statistics=0")
			}
			switch strings.ToUpper(strings.TrimSpace(job.BackupType)) {
			case "SCHEMA":
				args = append(args, "--routines", "--events", "--triggers", "--no-data")
			case "DATA":
				args = append(args, "--no-create-info", "--skip-triggers")
			default:
				args = append(args, "--routines", "--events", "--triggers")
			}
			if job.MySQLAll {
				args = append(args, "--all-databases")
			} else {
				args = append(args, job.DBName)
				for _, t := range mysqlIgnoreTabs {
					args = append(args, "--ignore-table="+job.DBName+"."+t)
				}
				if len(mysqlTabs) > 0 {
					args = append(args, mysqlTabs...)
				}
			}
			env := withLibraryPath(map[string]string{"MYSQL_PWD": input.Instance.Password}, toolSpec.LibDirs)
			localDB := isInstanceHostLocal(input.Instance.Hostname)
			runAsUser := strings.TrimSpace(unifiedPlan.Logical.EffectiveOSUser)
			if useStream {
				entryWriter, openErr := artifactStream.WriterForEntry(job.FileName)
				if openErr != nil {
					return nil, openErr
				}
				logFile.Infof("[DirectStream 零落盘模式] 开始导出 MySQL → %s target=%s entry=%s compression=%s (local_db=%v)", displayOrDefault(job.DBName, "ALL"), input.StreamSink.RemoteTarget(), job.FileName, artifactPlan.StreamCompression, localDB)
				logFile.Infof("执行工具: %s (%s) [stdout→sink, 无本地 result-file]", toolSpec.Path, toolSpec.Source)
				var execErr error
				if localDB {
					if runAsUser != "" {
						execErr = runLocalCommandAsUserWithStdoutOverride(ctx, runAsUser, "", toolSpec.Path, args, env, logFile, entryWriter)
					} else {
						execErr = runLocalCommandAsDBUserWithStdoutOverride(ctx, "MySQL", "", toolSpec.Path, args, env, logFile, entryWriter)
					}
				} else {
					execErr = runLocalCommandWithStdoutOverride(ctx, "", toolSpec.Path, args, env, logFile, entryWriter)
				}
				if execErr != nil {
					if runControl.StopRequested() {
						return nil, ErrBackupRunStopped
					}
					logFile.Errorf("[DirectStream] 导出 MySQL 失败 database=%s (local_db=%v): %v", displayOrDefault(job.DBName, "ALL"), localDB, execErr)
					return nil, execErr
				}
				logFile.Infof("[DirectStream] 导出完成 target=%s entry=%s", input.StreamSink.RemoteTarget(), job.FileName)
			} else {
				logFile.Infof("开始导出 MySQL database=%s tables=%d output=%s (local_db=%v)", displayOrDefault(job.DBName, "ALL"), len(mysqlTabs), targetPath, localDB)
				logFile.Infof("执行工具: %s (%s)", toolSpec.Path, toolSpec.Source)
				var runErr error
				if localDB {
					if runAsUser != "" {
						runErr = runLocalCommandAsUser(ctx, runAsUser, "", toolSpec.Path, args, env, logFile)
					} else {
						runErr = runLocalCommandAsDBUser(ctx, "MySQL", "", toolSpec.Path, args, env, logFile)
					}
				} else {
					runErr = runLocalCommand(ctx, "", toolSpec.Path, args, env, logFile)
				}
				if runErr != nil {
					if runControl.StopRequested() {
						return nil, ErrBackupRunStopped
					}
					logFile.Errorf("导出 MySQL 失败 database=%s (local_db=%v): %v", displayOrDefault(job.DBName, "ALL"), localDB, runErr)
					return nil, runErr
				}
				if size, statErr := fileSize(targetPath); statErr == nil {
					logFile.Infof("导出完成 file=%s size=%d bytes", job.FileName, size)
					resultSizeBytes += size
				} else {
					logFile.Warnf("导出完成但读取文件大小失败 file=%s: %v", job.FileName, statErr)
				}
				outputFiles = append(outputFiles, job.FileName)
			}
		}
	case isPostgresLogicalFamily(input.DBType):
		if err := runControl.Checkpoint(logFile); err != nil {
			return nil, err
		}
		if unifiedPlan == nil || unifiedPlan.Logical == nil || unifiedPlan.Logical.Tool == nil {
			return nil, fmt.Errorf("逻辑备份统一执行计划未生成 %s 工具信息", logicalDBLabel(input.DBType))
		}
		sslmode := extra["sslmode"]
		serverVersion = strings.TrimSpace(unifiedPlan.ServerVersion)
		logFile.Infof("%s 版本: %s", logicalDBLabel(input.DBType), serverVersion)
		toolSpec := unifiedPlan.Logical.Tool
		tool = toolSpec.Name
		toolImage = toolSpec.Source
		logFile.Infof("命中备份工具: %s (%s)", toolSpec.Path, toolSpec.Source)
		// 金仓客户端与 PG 同源，兼容 PGPASSWORD；部分版本改读 KINGBASE_PASSWORD，
		// 两个都设，避免因版本差异导致「密码没生效」的密码认证失败。
		env := map[string]string{"PGPASSWORD": input.Instance.Password}
		if isKingbaseDBType(input.DBType) {
			env["KINGBASE_PASSWORD"] = input.Instance.Password
		}
		if sslmode != "" {
			env["PGSSLMODE"] = sslmode
		}
		for _, job := range jobs {
			pgTables := job.PgTables
			pgExcludeTables := uniqueStrings(job.PgExcludeTables)
			pgExcludePatterns := uniqueStrings(job.PgExcludePatterns)
			if len(pgTables) > 0 && (len(pgExcludeTables) > 0 || len(pgExcludePatterns) > 0) {
				exactSet := map[string]struct{}{}
				for _, t := range pgExcludeTables {
					exactSet[t] = struct{}{}
				}
				filtered := make([]string, 0, len(pgTables))
				for _, t := range pgTables {
					if _, ok := exactSet[t]; ok {
						continue
					}
					skip := false
					for _, p := range pgExcludePatterns {
						if matchWildcard(t, p) {
							skip = true
							break
						}
					}
					if skip {
						continue
					}
					filtered = append(filtered, t)
				}
				pgTables = filtered
				if len(pgTables) == 0 {
					logFile.Errorf("PostgreSQL 备份对象为空 database=%s", job.DBName)
					return nil, fmt.Errorf("PostgreSQL 备份对象为空：已全部被排除")
				}
			}
			args := []string{
				"-h", input.Instance.Hostname,
				"-p", fmt.Sprintf("%d", input.Instance.Port),
				"-U", input.Instance.User,
				"-d", job.DBName,
				"--no-owner",
				// 用 --no-privileges 而不是 --no-acl：两者在 PG 里等价，
				// 但金仓各版本的 sys_dump 对 --no-acl 这别名支持不一致，
				// 长名 --no-privileges 是两边都稳的那个。
				"--no-privileges",
			}
			switch strings.ToUpper(strings.TrimSpace(job.BackupType)) {
			case "SCHEMA":
				args = append(args, "--schema-only")
			case "DATA":
				args = append(args, "--data-only")
			}
			for _, sc := range job.PgSchemas {
				args = append(args, "-n", sc)
			}
			for _, t := range pgTables {
				args = append(args, "-t", t)
			}
			for _, t := range pgExcludeTables {
				args = append(args, "--exclude-table="+t)
			}
			for _, p := range pgExcludePatterns {
				args = append(args, "--exclude-table="+p)
			}
			targetPath := filepath.Join(dir, job.FileName)
			parallelWorkers := recommendedPostgresDumpWorkers(job)
			if isKingbaseDBType(input.DBType) {
				// 金仓 sys_dump 对目录格式（-Fd）与并行（-j）的支持随版本/授权版本而异，
				// 未逐版本实测前统一走自定义格式单文件（-Fc）：
				// 产物是标准 PGDMP 容器，sys_restore 可直接读，多库场景由外层打 zip。
				parallelWorkers = 1
			}
			useParallel := parallelWorkers > 1 && !useStream
			if useParallel {
				args = append(args, "-Fd", "-j", fmt.Sprintf("%d", parallelWorkers), "-f", targetPath)
			} else {
				args = append(args, "-Fc")
				if !useStream {
					args = append(args, "-f", targetPath)
				}
			}
			if useStream {
				entryWriter, openErr := artifactStream.WriterForEntry(job.FileName)
				if openErr != nil {
					return nil, openErr
				}
				logFile.Infof("[DirectStream 零落盘模式] 开始导出 %s database=%s entry=%s target=%s compression=%s format=custom", logicalDBLabel(input.DBType), job.DBName, job.FileName, input.StreamSink.RemoteTarget(), artifactPlan.StreamCompression)
				logFile.Infof("执行工具: %s (%s) [stdout→sink, 无本地 result-file]", toolSpec.Path, toolSpec.Source)
				if strings.TrimSpace(unifiedPlan.Logical.EffectiveOSUser) != "" {
					err = runLocalCommandAsUserWithStdoutOverride(ctx, unifiedPlan.Logical.EffectiveOSUser, "", toolSpec.Path, args, withLibraryPath(env, toolSpec.LibDirs), logFile, entryWriter)
				} else {
					err = runLocalCommandAsDBUserWithStdoutOverride(ctx, input.DBType, "", toolSpec.Path, args, withLibraryPath(env, toolSpec.LibDirs), logFile, entryWriter)
				}
				if err != nil {
					if runControl.StopRequested() {
						return nil, ErrBackupRunStopped
					}
					logFile.Errorf("导出 %s 失败 database=%s: %v", logicalDBLabel(input.DBType), job.DBName, err)
					return nil, err
				}
				logFile.Infof("[DirectStream] 导出 %s 完成 database=%s entry=%s", logicalDBLabel(input.DBType), job.DBName, job.FileName)
			} else {
				logFile.Infof("开始导出 %s database=%s schemas=%d tables=%d output=%s format=%s workers=%d", logicalDBLabel(input.DBType), job.DBName, len(job.PgSchemas), len(pgTables), targetPath, postgresDumpFormatLabel(useParallel), parallelWorkers)
				logFile.Infof("执行工具: %s (%s)", toolSpec.Path, toolSpec.Source)
				if strings.TrimSpace(unifiedPlan.Logical.EffectiveOSUser) != "" {
					err = runLocalCommandAsUser(ctx, unifiedPlan.Logical.EffectiveOSUser, "", toolSpec.Path, args, withLibraryPath(env, toolSpec.LibDirs), logFile)
				} else {
					err = runLocalCommandAsDBUser(ctx, input.DBType, "", toolSpec.Path, args, withLibraryPath(env, toolSpec.LibDirs), logFile)
				}
				if err != nil {
					if runControl.StopRequested() {
						return nil, ErrBackupRunStopped
					}
					logFile.Errorf("导出 %s 失败 database=%s: %v", logicalDBLabel(input.DBType), job.DBName, err)
					return nil, err
				}
				if size, statErr := pathSize(targetPath); statErr == nil {
					logFile.Infof("导出完成 file=%s size=%d bytes", job.FileName, size)
				} else {
					logFile.Warnf("导出完成但读取文件大小失败 file=%s: %v", job.FileName, statErr)
				}
				outputFiles = append(outputFiles, job.FileName)
			}
		}
	}

	if artifactName == "" && len(outputFiles) > 0 {
		artifactName = outputFiles[0]
	}
	if artifactName == "" {
		artifactName = "all_databases.sql"
	}
	if err := runControl.Checkpoint(logFile); err != nil {
		return nil, err
	}
	artifactPath := ""
	if useStream {
		if err := artifactStream.Close(); err != nil {
			logFile.Errorf("[DirectStream] 关闭存储目标 sink 失败: %v", err)
			return nil, err
		}
		resultSizeBytes = artifactStream.SizeBytes()
		logFile.Infof("[DirectStream] 产物直写完成 artifact=%s size=%d bytes", artifactName, resultSizeBytes)
	} else {
		artifactPath = filepath.Join(dir, artifactName)
		if len(outputFiles) == 0 {
			artifactPath = ""
		}
		shouldZip := len(outputFiles) > 1 || (len(outputFiles) > 0 && isPostgresLogicalFamily(input.DBType))
		if shouldZip {
			artifactName = "backup.zip"
			artifactPath = filepath.Join(dir, artifactName)
			logFile.Infof("开始归档输出文件 count=%d target=%s", len(outputFiles), artifactPath)
			if err := zipFiles(artifactPath, dir, outputFiles); err != nil {
				logFile.Errorf("归档备份文件失败: %v", err)
				return nil, err
			}
			if size, statErr := fileSize(artifactPath); statErr == nil {
				logFile.Infof("归档完成 artifact=%s size=%d bytes", artifactName, size)
				resultSizeBytes = size
			}
		} else if artifactPlan.StreamCompression == ArtifactCompressionGzip && len(outputFiles) == 1 {
			// 非流式回退路径：本地落盘后补做 gzip，避免产出 .gz 后缀但内容为明文的产物
			sourcePath := filepath.Join(dir, outputFiles[0])
			if sourcePath == artifactPath {
				logFile.Warnf("产物名与源文件重名，跳过 gzip 压缩 artifact=%s", artifactName)
			} else {
				logFile.Infof("开始 gzip 压缩产物 source=%s target=%s", sourcePath, artifactPath)
				if err := gzipFileTo(sourcePath, artifactPath, gzipLevelForArtifact(artifactPlan.CompressionLevel)); err != nil {
					logFile.Errorf("gzip 压缩产物失败: %v", err)
					return nil, err
				}
				if err := os.Remove(sourcePath); err != nil {
					logFile.Warnf("清理未压缩中间产物失败 file=%s: %v", sourcePath, err)
				}
				if size, statErr := fileSize(artifactPath); statErr == nil {
					logFile.Infof("压缩完成 artifact=%s size=%d bytes", artifactName, size)
					resultSizeBytes = size
				}
			}
		}
	}
	if err := runControl.Checkpoint(logFile); err != nil {
		return nil, err
	}
	var sha string
	size := resultSizeBytes
	if artifactPath != "" {
		logFile.Infof("开始计算校验和 artifact=%s", artifactPath)
		var shaErr error
		sha, size, shaErr = fileSha256(artifactPath)
		if shaErr != nil {
			logFile.Errorf("计算校验和失败: %v", shaErr)
			return nil, shaErr
		}
		logFile.Infof("校验完成 size=%d bytes sha256=%s", size, sha)
	} else if artifactStream != nil {
		// 零落盘直传：文件没有落盘，但仍可从流的累计摘要器取到最终产物 sha256
		sha = artifactStream.Sha256()
		logFile.Infof("[DirectStream 零落盘模式] 采用流累计摘要 sha256=%s size=%d bytes", sha, resultSizeBytes)
	}
	// 备份集附属文件：恢复预检的「元数据 / 校验和」两项依赖它们，必须与产物同目录。
	// 写失败只降级告警，不得让已经成功的备份失败；校验结果随本函数返回并落库，
	// 恢复侧直接读字段，不再现场 find 文件。
	metadataOK, checksumOK := writeAndVerifyBackupSetSidecar(dir,
		buildBackupSetMetadata(input, artifactName, size, sha, BackupTypeLogical, tool, serverVersion),
		[]backupChecksumEntry{{Name: artifactName, Sha256: sha}},
	)
	if !metadataOK || !checksumOK {
		logFile.Warnf("写入备份集附属文件校验未通过（不影响备份结果）: metadata=%v checksum=%v", metadataOK, checksumOK)
	}
	sourceDBName := normalized.LegacyDBName
	if sourceDBName == "" {
		sourceDBName = input.SourceDBName
	}
	logFile.Infof("备份执行完成 artifact=%s directStream=%v", artifactName, artifactPath == "")
	return &BackupExecutionResult{
		ArtifactName:  artifactName,
		ArtifactPath:  artifactPath,
		SizeBytes:     size,
		Sha256:        sha,
		ServerVersion: serverVersion,
		Tool:          tool,
		ToolImage:     toolImage,
		LogPath:       logPath,
		LogDir:        dir,
		OutputDir:     input.OutputBaseDir,
		ObjectMode:    normalized.ObjectMode,
		ObjectSpec:    normalized.ObjectSpec,
		ObjectSummary: normalized.ObjectSummary,
		LegacyDBName:  normalized.LegacyDBName,
		SourceDBName:  sourceDBName,

		MetadataVerified: metadataOK,
		ChecksumVerified: checksumOK,
	}, nil
}

func executePhysicalBackupJob(ctx context.Context, runControl *backupRunControl, logFile backupRunLogWriter, dir string, logPath string, input BackupExecutionInput, normalized *normalizedObjectSpec, extra map[string]string) (*BackupExecutionResult, error) {
	if strings.TrimSpace(input.DBType) != "PostgreSQL" {
		return nil, fmt.Errorf("物理备份当前仅支持 PostgreSQL")
	}
	driverType := strings.ToUpper(strings.TrimSpace(input.DriverType))
	if driverType != "PG_BACKREST" {
		return nil, fmt.Errorf("PostgreSQL 原物理备份方式已下线，请改用 PG_BACKREST")
	}
	if err := runControl.Checkpoint(logFile); err != nil {
		return nil, err
	}
	var pgOSUserForPhysical string
	var pgSharedGroupForPhysical string
	if osUserTmp, osUserErr := detectDatabaseOSUser("PostgreSQL"); osUserErr == nil && strings.TrimSpace(osUserTmp) != "" && strings.TrimSpace(osUserTmp) != strings.TrimSpace(currentOSUser()) {
		pgOSUserForPhysical = strings.TrimSpace(osUserTmp)
		currentUser := strings.TrimSpace(currentOSUser())
		pgSharedGroupForPhysical = detectSharedGroupForUsers(ctx, pgOSUserForPhysical, currentUser)
	}
	sslmode := extra["sslmode"]
	plan, serverVersion, err := buildPGBackRestExecutionPlanForRun(ctx, input)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.Tool == nil || plan.PSQLTool == nil {
		return nil, fmt.Errorf("PG_BACKREST 统一执行计划未生成完整工具信息")
	}
	pgBackRestTool := plan.Tool
	cfg, err := loadPGBackRestDriverConfig(input.DriverConfig)
	if err != nil {
		logFile.Errorf("解析 pgBackRest 配置失败: %v", err)
		return nil, err
	}
	repoRemoteMode := plan.RepoRemote
	repoDir := strings.TrimSpace(plan.RepoDir)
	if cfg.IsS3Repo() {
		// S3 仓库：repoDir 是对象存储里的前缀（repo1-path），本地不需要也不允许创建同名目录
		logFile.Infof("pgBackRest S3 仓库模式：bucket=%s endpoint=%s region=%s uri-style=%s repo-prefix=%s",
			cfg.S3Bucket, cfg.S3Endpoint, cfg.S3Region, displayOrDefault(strings.TrimSpace(cfg.S3URIStyle), "host"), repoDir)
	} else if repoRemoteMode {
		logFile.Infof("异地仓库模式启用直写拓扑：源端 -> 远端 repo=%s@%s:%d%s", displayOrDefault(strings.TrimSpace(cfg.RepoUser), "hs-ops"), cfg.RepoHost, cfg.RepoPort, repoDir)
	} else {
		if err := ensureDir(repoDir); err != nil {
			if strings.TrimSpace(cfg.RepoHost) == "" {
				return nil, err
			}
		}
	}
	pgDataPath := strings.TrimSpace(plan.PGDataPath)
	if err := validatePostgresDataDirAccessible(ctx, pgDataPath); err != nil {
		return nil, err
	}
	defaultDatabase := strings.TrimSpace(extra["database"])
	isLocalDB := cfg.RepoRemote
	env := buildPostgresRuntimeEnv(input.Instance, defaultDatabase, sslmode, isLocalDB)
	stanza := strings.TrimSpace(plan.Stanza)
	repoHostCmd := ""
	repoRemoteRuntime := &pgBackRestRepoRemoteRuntime{Env: map[string]string{}}
	if repoRemoteMode {
		repoHostCmd = resolveRemotePostgresToolCommand(cfg.RepoToolsDir, serverVersion, "pgbackrest")
		repoRemoteRuntime, err = preparePGBackRestRepoRemoteRuntime(ctx, dir, cfg, logFile, pgOSUserForPhysical, pgSharedGroupForPhysical)
		if err != nil {
			return nil, err
		}
		defer repoRemoteRuntime.Cleanup(logFile)
		for key, value := range repoRemoteRuntime.Env {
			env[key] = value
		}
	}
	logFile.Infof("pgBackRest 仓库目录: %s", repoDir)
	if cfg.RepoRemote && strings.TrimSpace(cfg.RepoHost) != "" {
		logFile.Infof("pgBackRest 仓库主机: %s:%d user=%s", cfg.RepoHost, cfg.RepoPort, cfg.RepoUser)
	}
	logFile.Infof("pgBackRest stanza: %s", stanza)
	logFile.Infof("pgBackRest 并行数: %d", cfg.ProcessMax)
	logFile.Infof("pgBackRest 备份类型: %s", cfg.BackupType)
	logFile.Infof("PostgreSQL data_directory: %s", pgDataPath)
	logFile.Infof("pgBackRest 连接实例: port=%d user=%s db=%s", input.Instance.Port, displayOrDefault(strings.TrimSpace(env["PGUSER"]), input.Instance.User), displayOrDefault(strings.TrimSpace(env["PGDATABASE"]), "postgres"))
	if repoRemoteMode {
		return executePGBackRestViaRepoHost(ctx, runControl, logFile, dir, logPath, input, normalized, extra, serverVersion, cfg, repoDir, stanza, pgDataPath, repoRemoteRuntime, pgOSUserForPhysical)
	}
	registerPGBackRestLocalStopHandler(runControl, logFile, dir, stanza)
	configPath := filepath.Join(dir, "pgbackrest.conf")
	configText := buildPGBackRestConfig(cfg, repoDir, repoHostCmd, repoRemoteRuntime.SSHCommand, stanza, pgDataPath, input.Instance.Port, input.Instance.User, isLocalDB)
	if err := os.WriteFile(configPath, []byte(configText), 0o644); err != nil {
		return nil, err
	}
	if strings.TrimSpace(pgOSUserForPhysical) != "" {
		confGroup := pgSharedGroupForPhysical
		if confGroup == "" {
			confGroup = pgOSUserForPhysical
		}
		_, _ = runLocalCommandCaptureAsRoot(ctx, "", "chown", []string{pgOSUserForPhysical + ":" + confGroup, configPath}, nil, nil)
		_, _ = runLocalCommandCaptureAsRoot(ctx, "", "chmod", []string{"0664", configPath}, nil, nil)
	}
	if err := runControl.Checkpoint(logFile); err != nil {
		return nil, err
	}
	logFile.Infof("执行工具: %s (%s)", pgBackRestTool.Path, pgBackRestTool.Source)
	commonArgs := []string{
		"--config=" + configPath,
		"--stanza=" + stanza,
		"--log-level-console=detail",
	}
	stanzaOutput, err := runLocalCommandCaptureAsDBUser(ctx, "PostgreSQL", "", pgBackRestTool.Path, append(commonArgs, "stanza-create"), withLibraryPath(env, pgBackRestTool.LibDirs), logFile)
	if err != nil {
		if !strings.Contains(strings.ToLower(stanzaOutput), "already exists") {
			logFile.Errorf("执行 stanza-create 失败: %v", err)
			return nil, err
		}
		logFile.Warnf("stanza 已存在，继续执行检查和备份")
	}
	if err := runControl.Checkpoint(logFile); err != nil {
		return nil, err
	}
	if _, err := runLocalCommandCaptureAsDBUser(ctx, "PostgreSQL", "", pgBackRestTool.Path, append(append([]string(nil), commonArgs...), "--archive-check=n", "check"), withLibraryPath(env, pgBackRestTool.LibDirs), logFile); err != nil {
		logFile.Warnf("执行 pgBackRest check 失败（降级为告警不终止，pgBackRest 2.53 以下无法跳过 WAL 归档校验）：%v", err)
	}
	effectiveBackupType := cfg.BackupType
	if effectiveBackupType == "" {
		effectiveBackupType = "FULL"
	}
	switch strings.ToUpper(effectiveBackupType) {
	case "INCR", "DIFF":
		preInfoArgs := []string{
			"--config=" + configPath,
			"--stanza=" + stanza,
			"--log-level-console=off",
			"--output=json",
			"info",
		}
		preInfoOutput, preInfoErr := runLocalCommandCaptureAsDBUser(ctx, "PostgreSQL", "", pgBackRestTool.Path, preInfoArgs, withLibraryPath(env, pgBackRestTool.LibDirs), nil)
		if preInfoErr == nil && preInfoOutput != "" {
			hasFullBase := false
			var preInfo []map[string]any
			if jsonErr := json.Unmarshal([]byte(preInfoOutput), &preInfo); jsonErr == nil {
				stanzaName := strings.TrimSpace(stanza)
				for _, item := range preInfo {
					name := strings.TrimSpace(fmt.Sprintf("%v", item["name"]))
					if stanzaName != "" && name != stanzaName {
						continue
					}
					backups, ok := item["backup"].([]any)
					if !ok {
						continue
					}
					for _, rawBackup := range backups {
						backupItem, castOK := rawBackup.(map[string]any)
						if !castOK {
							continue
						}
						bt := strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", backupItem["type"])))
						if bt == "FULL" {
							hasFullBase = true
							break
						}
					}
					if hasFullBase {
						break
					}
				}
			}
			if !hasFullBase {
				logFile.Warnf("当前仓库未找到可用 FULL 备份，原计划执行 %s，智能降级为 FULL 备份", effectiveBackupType)
				effectiveBackupType = "FULL"
			} else {
				logFile.Infof("前置检查：存在可用 FULL 备份，将按计划执行 %s 备份", effectiveBackupType)
			}
		} else if preInfoErr != nil {
			logFile.Warnf("前置 FULL 检查执行 info 失败（可能为首次备份），降级为 FULL：%v", preInfoErr)
			effectiveBackupType = "FULL"
		}
	default:
		logFile.Infof("前置检查：备份类型为 %s，无需检查基线", effectiveBackupType)
	}
	if err := runControl.Checkpoint(logFile); err != nil {
		return nil, err
	}
	backupArgs := append(commonArgs,
		"--type="+strings.ToLower(effectiveBackupType),
		fmt.Sprintf("--process-max=%d", cfg.ProcessMax),
		"backup",
		"--no-archive-check",
	)
	logFile.Infof("开始执行 PostgreSQL pgBackRest 物理备份 repo=%s type=%s", repoDir, effectiveBackupType)
	if _, err := runLocalCommandCaptureAsDBUser(ctx, "PostgreSQL", "", pgBackRestTool.Path, backupArgs, withLibraryPath(env, pgBackRestTool.LibDirs), logFile); err != nil {
		if runControl.StopRequested() {
			return nil, ErrBackupRunStopped
		}
		logFile.Errorf("执行 pgBackRest 备份失败: %v", err)
		return nil, err
	}
	if err := runControl.Checkpoint(logFile); err != nil {
		return nil, err
	}
	infoArgs := []string{
		"--config=" + configPath,
		"--stanza=" + stanza,
		"--log-level-console=off",
		"--output=json",
		"info",
	}
	infoOutput, infoErr := runLocalCommandCaptureAsDBUser(ctx, "PostgreSQL", "", pgBackRestTool.Path, infoArgs, withLibraryPath(env, pgBackRestTool.LibDirs), nil)
	if infoErr != nil {
		logFile.Errorf("读取 pgBackRest 备份信息失败: %v", infoErr)
		return nil, infoErr
	}
	backupMeta, err := parsePGBackRestInfo(infoOutput, stanza)
	if err != nil {
		logFile.Errorf("解析 pgBackRest 备份信息失败: %v", err)
		return nil, err
	}
	finalRepoDir := repoDir
	finalSize := int64(0)
	if cfg.IsS3Repo() {
		// S3 仓库：du 不可用，仓库占用从 pgBackRest info JSON 的 repo.size 读取（解析失败降级为 0）
		finalSize = backupMeta.RepoSize
		if finalSize <= 0 {
			logFile.Warnf("未能从 pgBackRest info 中读取仓库大小（repo.size），备份集大小记录为 0")
		}
	} else if repoRemoteMode {
		if sizeOut, sizeErr := runPGBackRestRepoRemoteCommand(ctx, repoRemoteRuntime, cfg, "du -sb "+shellQuote(finalRepoDir)+" | awk '{print $1}'", logFile); sizeErr == nil {
			if n, pErr := strconv.ParseInt(strings.TrimSpace(sizeOut), 10, 64); pErr == nil && n > 0 {
				finalSize = n
			}
		} else {
			logFile.Warnf("读取远端 pgBackRest 仓库大小失败（降级为告警）: %v", sizeErr)
		}
	} else {
		if sizeOut, sizeErr := runLocalCommandCaptureAsRoot(ctx, "", "du", []string{"-sb", repoDir}, nil, nil); sizeErr == nil {
			fields := strings.Fields(sizeOut)
			if len(fields) > 0 {
				if n, pErr := strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 64); pErr == nil && n > 0 {
					finalSize = n
				}
			}
		}
	}
	metadataSink := BackupStreamSink(nil)
	if input.DirectStream && input.StreamSink != nil {
		metadataSink = input.StreamSink
	}
	artifactName, artifactPath, size, sha, err := emitPGBackRestMetadataArtifact(dir, map[string]any{
		"run_id":             input.RunID,
		"task_id":            input.TaskID,
		"source_instance_id": input.SourceInstanceID,
		"db_type":            input.DBType,
		"driver_type":        driverType,
		"server_version":     serverVersion,
		"stanza":             stanza,
		"repo_path":          finalRepoDir,
		"pg_data_path":       pgDataPath,
		"backup_type":        backupMeta.BackupType,
		"process_max":        cfg.ProcessMax,
		"backup_label":       backupMeta.Label,
		"backup_reference":   backupMeta.Reference,
		"backup_timestamp":   backupMeta.Timestamp,
		"info":               backupMeta.Raw,
	}, metadataSink)
	if err != nil {
		logFile.Errorf("生成 pgBackRest 备份描述文件失败: %v", err)
		return nil, err
	}
	if finalSize > 0 {
		size = finalSize
	}
	metadataOK, checksumOK := writePGBackRestSidecar(dir, logFile, input, stanza, serverVersion, artifactName, size, sha, backupMeta, finalRepoDir)
	logFile.Infof("pgBackRest 备份完成 label=%s artifact=%s size=%d bytes sha256=%s repo=%s", backupMeta.Label, artifactPath, size, sha, finalRepoDir)
	return &BackupExecutionResult{
		ArtifactName:  artifactName,
		ArtifactPath:  artifactPath,
		SizeBytes:     size,
		Sha256:        sha,
		ServerVersion: serverVersion,
		Tool:          pgBackRestTool.Name,
		ToolImage:     pgBackRestTool.Source,
		LogPath:       logPath,
		LogDir:        dir,
		OutputDir:     finalRepoDir,
		ObjectMode:    normalized.ObjectMode,
		ObjectSpec:    normalized.ObjectSpec,
		ObjectSummary: normalized.ObjectSummary,
		LegacyDBName:  "",
		SourceDBName:  "",

		MetadataVerified: metadataOK,
		ChecksumVerified: checksumOK,
	}, nil
}

type pgBackRestBackupMetadata struct {
	Label      string
	BackupType string
	Reference  []string
	Timestamp  int64
	Raw        map[string]any
	// RepoSize 仓库占用字节数（info JSON backup.repo.size）；BackupSize 数据库大小（backup.size）。
	// S3 仓库模式下 du 不可用，RepoSize 是备份集大小的唯一来源。
	RepoSize   int64
	BackupSize int64
}

func executePGBackRestViaRepoHost(ctx context.Context, runControl *backupRunControl, logFile backupRunLogWriter, dir string, logPath string, input BackupExecutionInput, normalized *normalizedObjectSpec, extra map[string]string, serverVersion string, cfg pgBackRestDriverConfig, repoDir string, stanza string, pgDataPath string, repoRemoteRuntime *pgBackRestRepoRemoteRuntime, pgOSUser string) (*BackupExecutionResult, error) {
	sourceSSHUser, sourceSSHPass, sourceSSHKey, sourceSSHPort, err := resolveBackupInstanceSSHCredentials(extra, input.Instance.User, input.Instance.Password)
	if err != nil {
		return nil, err
	}
	sourceRunner, err := newSSHRunner(&backupNodeRemoteConfig{
		Host:          strings.TrimSpace(input.Instance.Hostname),
		Port:          sourceSSHPort,
		SSHUser:       defaultBackupNodeSSHUser(sourceSSHUser),
		SSHPassword:   strings.TrimSpace(sourceSSHPass),
		SSHPrivateKey: strings.TrimSpace(sourceSSHKey),
	})
	if err != nil {
		return nil, fmt.Errorf("连接源端主机检查 pgBackRest 失败: %w", err)
	}
	defer sourceRunner.Close()
	sourceToolSpec, err := resolveRemotePostgresToolSpec(sourceRunner, defaultBackupNodeToolsDir(""), serverVersion, "pgbackrest")
	if err != nil {
		return nil, fmt.Errorf("识别源端 pgBackRest 工具失败: %w", err)
	}
	repoRunner, err := newSSHRunner(&backupNodeRemoteConfig{
		Host:          strings.TrimSpace(cfg.RepoHost),
		Port:          cfg.RepoPort,
		SSHUser:       defaultBackupNodeSSHUser(cfg.RepoUser),
		SSHPassword:   strings.TrimSpace(cfg.RepoSSHPassword),
		SSHPrivateKey: strings.TrimSpace(cfg.RepoSSHPrivateKey),
		ToolsDir:      defaultBackupNodeToolsDir(cfg.RepoToolsDir),
	})
	if err != nil {
		return nil, fmt.Errorf("连接仓库节点检查 pgBackRest 失败: %w", err)
	}
	defer repoRunner.Close()
	repoToolSpec, err := resolveRemotePostgresToolSpec(repoRunner, defaultBackupNodeToolsDir(cfg.RepoToolsDir), serverVersion, "pgbackrest")
	if err != nil {
		return nil, fmt.Errorf("识别仓库节点 pgBackRest 工具失败: %w", err)
	}
	if strings.TrimSpace(sourceToolSpec.VersionLine) != "" && strings.TrimSpace(repoToolSpec.VersionLine) != "" &&
		!strings.EqualFold(strings.TrimSpace(sourceToolSpec.VersionLine), strings.TrimSpace(repoToolSpec.VersionLine)) {
		return nil, fmt.Errorf("源端与仓库端 pgBackRest 版本不一致：source=%s (%s), repo=%s (%s)", sourceToolSpec.VersionLine, sourceToolSpec.Path, repoToolSpec.VersionLine, repoToolSpec.Path)
	}
	logFile.Infof("源端 pgBackRest: %s (%s)", sourceToolSpec.Path, displayOrDefault(strings.TrimSpace(sourceToolSpec.VersionLine), "unknown"))
	logFile.Infof("仓库端 pgBackRest: %s (%s)", repoToolSpec.Path, displayOrDefault(strings.TrimSpace(repoToolSpec.VersionLine), "unknown"))
	repoWorkDir := filepath.ToSlash(filepath.Join("/tmp", "goinsight-pgbackrest", sanitizePathSegment(input.TaskID, "task"), fmt.Sprintf("%d", input.RunID)))
	defer func() {
		_, _ = runPGBackRestRepoRemoteCommand(context.Background(), repoRemoteRuntime, cfg, "rm -rf "+shellQuote(repoWorkDir), nil)
	}()
	if _, err := runPGBackRestRepoRemoteCommand(ctx, repoRemoteRuntime, cfg, "mkdir -p "+shellQuote(repoWorkDir)+" "+shellQuote(filepath.ToSlash(filepath.Join(repoWorkDir, "lock")))+" "+shellQuote(filepath.ToSlash(filepath.Join(repoWorkDir, "log")))+" "+shellQuote(filepath.ToSlash(filepath.Join(repoWorkDir, "spool"))), logFile); err != nil {
		return nil, fmt.Errorf("初始化仓库机运行目录失败: %w", err)
	}
	sourceSSHScriptPath := filepath.ToSlash(filepath.Join(repoWorkDir, "pgbackrest-source-ssh.sh"))
	sourceToolCmd := buildRemotePGBackRestSourceToolCommand(sourceToolSpec, pgOSUser)
	sourceRuntime, err := buildRepoHostSourceSSHRuntime(repoWorkDir, sourceSSHScriptPath, sourceSSHPass, sourceSSHKey, sourceSSHPort)
	if err != nil {
		return nil, err
	}
	if _, err := runPGBackRestRepoRemoteCommand(ctx, repoRemoteRuntime, cfg, sourceRuntime.SetupCommand, logFile); err != nil {
		return nil, fmt.Errorf("准备仓库机到源端 SSH 运行时失败: %w", err)
	}
	repoConfigPath := filepath.ToSlash(filepath.Join(repoWorkDir, "pgbackrest.conf"))
	repoConfigText := buildPGBackRestRepoHostConfig(repoWorkDir, repoDir, sourceSSHScriptPath, sourceToolCmd, stanza, pgDataPath, input.Instance.Hostname, sourceSSHUser, sourceSSHPort, input.Instance.Port, input.Instance.User, cfg.StartFast, cfg.ProcessMax)
	writeConfigCmd := "cat > " + shellQuote(repoConfigPath) + " <<'EOF'\n" + repoConfigText + "\nEOF\nchmod 640 " + shellQuote(repoConfigPath)
	if _, err := runPGBackRestRepoRemoteCommand(ctx, repoRemoteRuntime, cfg, writeConfigCmd, logFile); err != nil {
		return nil, fmt.Errorf("写入仓库机 pgbackrest.conf 失败: %w", err)
	}
	registerPGBackRestRepoHostStopHandler(runControl, logFile, cfg, repoRemoteRuntime, dir, repoWorkDir, repoConfigPath, sourceSSHScriptPath, sourceSSHUser, input.Instance.Hostname, stanza, pgDataPath)
	repoPGBackRestCmd := repoToolSpec.ShellCommand()
	runRepoPGBackRest := func(logWriter io.Writer, args ...string) (string, error) {
		cmd := repoPGBackRestCmd + " " + joinShellCommand(args)
		return runPGBackRestRepoRemoteCommand(ctx, repoRemoteRuntime, cfg, cmd, logWriter)
	}
	commonArgs := []string{
		"--config=" + repoConfigPath,
		"--stanza=" + stanza,
		"--log-level-console=detail",
	}
	stanzaOutput, err := runRepoPGBackRest(logFile, append(commonArgs, "stanza-create")...)
	if err != nil {
		if !strings.Contains(strings.ToLower(stanzaOutput), "already exists") {
			logFile.Errorf("执行仓库机 stanza-create 失败: %v", err)
			return nil, err
		}
		logFile.Warnf("仓库机 stanza 已存在，继续执行检查和备份")
	}
	if err := runControl.Checkpoint(logFile); err != nil {
		return nil, err
	}
	if _, err := runRepoPGBackRest(logFile, append(append([]string(nil), commonArgs...), "--archive-check=n", "check")...); err != nil {
		logFile.Warnf("执行仓库机 pgBackRest check 失败（降级为告警不终止）: %v", err)
	}
	effectiveBackupType := cfg.BackupType
	if effectiveBackupType == "" {
		effectiveBackupType = "FULL"
	}
	logFile.Infof("前置检查：备份类型为 %s，无需检查基线", effectiveBackupType)
	if err := runControl.Checkpoint(logFile); err != nil {
		return nil, err
	}
	backupArgs := append(commonArgs,
		"--type="+strings.ToLower(effectiveBackupType),
		fmt.Sprintf("--process-max=%d", cfg.ProcessMax),
		"--no-archive-check",
		"backup",
	)
	logFile.Infof("开始执行 PostgreSQL pgBackRest 物理备份 repo=%s type=%s（仓库机拉取模式）", repoDir, effectiveBackupType)
	if _, err := runRepoPGBackRest(logFile, backupArgs...); err != nil {
		if runControl.StopRequested() {
			return nil, ErrBackupRunStopped
		}
		logFile.Errorf("执行仓库机 pgBackRest 备份失败: %v", err)
		return nil, err
	}
	if err := runControl.Checkpoint(logFile); err != nil {
		return nil, err
	}
	infoOutput, infoErr := runRepoPGBackRest(nil,
		"--config="+repoConfigPath,
		"--stanza="+stanza,
		"--log-level-console=off",
		"--output=json",
		"info",
	)
	if infoErr != nil {
		logFile.Errorf("读取仓库机 pgBackRest 备份信息失败: %v", infoErr)
		return nil, infoErr
	}
	backupMeta, err := parsePGBackRestInfo(infoOutput, stanza)
	if err != nil {
		logFile.Errorf("解析仓库机 pgBackRest 备份信息失败: %v", err)
		return nil, err
	}
	finalSize := int64(0)
	if sizeOut, sizeErr := runPGBackRestRepoRemoteCommand(ctx, repoRemoteRuntime, cfg, "du -sb "+shellQuote(repoDir)+" | awk '{print $1}'", logFile); sizeErr == nil {
		if n, pErr := strconv.ParseInt(strings.TrimSpace(sizeOut), 10, 64); pErr == nil && n > 0 {
			finalSize = n
		}
	} else {
		logFile.Warnf("读取远端 pgBackRest 仓库大小失败（降级为告警）: %v", sizeErr)
	}
	metadataSink := BackupStreamSink(nil)
	if input.DirectStream && input.StreamSink != nil {
		metadataSink = input.StreamSink
	}
	artifactName, artifactPath, size, sha, err := emitPGBackRestMetadataArtifact(dir, map[string]any{
		"run_id":             input.RunID,
		"task_id":            input.TaskID,
		"source_instance_id": input.SourceInstanceID,
		"db_type":            input.DBType,
		"driver_type":        strings.ToUpper(strings.TrimSpace(input.DriverType)),
		"server_version":     serverVersion,
		"stanza":             stanza,
		"repo_path":          repoDir,
		"pg_data_path":       pgDataPath,
		"backup_type":        backupMeta.BackupType,
		"process_max":        cfg.ProcessMax,
		"backup_label":       backupMeta.Label,
		"backup_reference":   backupMeta.Reference,
		"backup_timestamp":   backupMeta.Timestamp,
		"info":               backupMeta.Raw,
	}, metadataSink)
	if err != nil {
		logFile.Errorf("生成 pgBackRest 备份描述文件失败: %v", err)
		return nil, err
	}
	if finalSize > 0 {
		size = finalSize
	}
	metadataOK, checksumOK := writePGBackRestSidecar(dir, logFile, input, stanza, serverVersion, artifactName, size, sha, backupMeta, repoDir)
	logFile.Infof("pgBackRest 备份完成 label=%s artifact=%s size=%d bytes sha256=%s repo=%s", backupMeta.Label, artifactPath, size, sha, repoDir)
	return &BackupExecutionResult{
		ArtifactName:  artifactName,
		ArtifactPath:  artifactPath,
		SizeBytes:     size,
		Sha256:        sha,
		ServerVersion: serverVersion,
		Tool:          "pgbackrest",
		ToolImage:     "remote:" + repoToolSpec.Path,
		LogPath:       logPath,
		LogDir:        dir,
		OutputDir:     repoDir,
		ObjectMode:    normalized.ObjectMode,
		ObjectSpec:    normalized.ObjectSpec,
		ObjectSummary: normalized.ObjectSummary,
		LegacyDBName:  "",
		SourceDBName:  "",

		MetadataVerified: metadataOK,
		ChecksumVerified: checksumOK,
	}, nil
}

type repoHostSourceSSHRuntime struct {
	SetupCommand string
}

func resolveBackupInstanceSSHCredentials(extra map[string]string, dbUser string, dbPassword string) (string, string, string, int, error) {
	sshUser := strings.TrimSpace(extra["ssh_user"])
	// extra_config 里的 SSH 凭据已加密入库；历史明文由 decryptCredential 原样返回
	sshPassword := decryptCredential(extra["ssh_password"])
	sshPrivateKey := decryptCredential(extra["ssh_private_key"])
	sshPort := 22
	if portText := strings.TrimSpace(extra["ssh_port"]); portText != "" {
		if v, err := strconv.Atoi(portText); err == nil && v > 0 && v <= 65535 {
			sshPort = v
		}
	}
	if sshUser == "" {
		if strings.TrimSpace(dbUser) != "" {
			sshUser = strings.TrimSpace(dbUser)
		} else {
			sshUser = "root"
		}
	}
	if sshPassword == "" && sshPrivateKey == "" {
		sshPassword = strings.TrimSpace(dbPassword)
	}
	if sshPassword == "" && sshPrivateKey == "" {
		return "", "", "", 0, fmt.Errorf("PG_BACKREST 异地直写改为仓库机拉取模式后，需要源端主机 SSH 凭据。请在数据源扩展参数中补充 ssh_user/ssh_password（或 ssh_private_key、ssh_port）")
	}
	return sshUser, sshPassword, sshPrivateKey, sshPort, nil
}

func buildRemotePGBackRestSourceToolCommand(toolSpec *remotePostgresToolSpec, pgOSUser string) string {
	parts := []string{}
	if u := strings.TrimSpace(pgOSUser); u != "" {
		parts = append(parts, "sudo", "-n", "-u", u)
	}
	if toolSpec != nil && len(toolSpec.LibDirs) > 0 {
		parts = append(parts, "env", "LD_LIBRARY_PATH="+strings.Join(toolSpec.LibDirs, ":"))
	}
	path := "pgbackrest"
	if toolSpec != nil && strings.TrimSpace(toolSpec.Path) != "" {
		path = strings.TrimSpace(toolSpec.Path)
	}
	parts = append(parts, path, "--config=/dev/null")
	return joinShellCommand(parts)
}

func buildRepoHostSourceSSHRuntime(repoWorkDir string, scriptPath string, sshPassword string, sshPrivateKey string, sshPort int) (*repoHostSourceSSHRuntime, error) {
	sshOptions := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", fmt.Sprintf("Port=%d", sshPort),
	}
	setupParts := []string{"mkdir -p " + shellQuote(repoWorkDir)}
	commandParts := []string{"ssh"}
	if strings.TrimSpace(sshPrivateKey) != "" {
		keyPath := filepath.ToSlash(filepath.Join(repoWorkDir, "pgbackrest-source.key"))
		setupParts = append(setupParts,
			"cat > "+shellQuote(keyPath)+" <<'EOF'\n"+sshPrivateKey+"\nEOF",
			"chmod 600 "+shellQuote(keyPath),
		)
		commandParts = []string{"ssh", "-i", keyPath}
		sshOptions = append(sshOptions, "-o", "BatchMode=yes")
	} else {
		commandParts = []string{"sshpass", "-p", sshPassword, "ssh"}
		sshOptions = append(sshOptions,
			"-o", "BatchMode=no",
			"-o", "PasswordAuthentication=yes",
			"-o", "PreferredAuthentications=password",
			"-o", "PubkeyAuthentication=no",
			"-o", "NumberOfPasswordPrompts=1",
		)
	}
	commandParts = append(commandParts, sshOptions...)
	scriptText := "#!/usr/bin/env bash\nset -euo pipefail\nexec " + joinShellCommand(commandParts) + " \"$@\"\n"
	setupParts = append(setupParts,
		"cat > "+shellQuote(scriptPath)+" <<'EOF'\n"+scriptText+"\nEOF",
		"chmod 750 "+shellQuote(scriptPath),
	)
	return &repoHostSourceSSHRuntime{SetupCommand: strings.Join(setupParts, "\n")}, nil
}

func buildPGBackRestRepoHostConfig(repoWorkDir string, repoPath string, sshCommand string, sourceToolCmd string, stanza string, pgDataPath string, sourceHost string, sourceSSHUser string, sourceSSHPort int, pgPort int, pgUser string, startFast bool, processMax int) string {
	startFastValue := "n"
	if startFast {
		startFastValue = "y"
	}
	lockPath := filepath.ToSlash(filepath.Join(repoWorkDir, "lock"))
	logPath := filepath.ToSlash(filepath.Join(repoWorkDir, "log"))
	spoolPath := filepath.ToSlash(filepath.Join(repoWorkDir, "spool"))
	lines := []string{
		"[global]",
		fmt.Sprintf("repo1-path=%s", repoPath),
		fmt.Sprintf("process-max=%d", processMax),
		fmt.Sprintf("start-fast=%s", startFastValue),
		fmt.Sprintf("lock-path=%s", lockPath),
		fmt.Sprintf("log-path=%s", logPath),
		fmt.Sprintf("spool-path=%s", spoolPath),
		"log-level-file=off",
		"repo1-retention-full=9999999",
		fmt.Sprintf("cmd-ssh=%s", sshCommand),
		"",
		fmt.Sprintf("[%s]", stanza),
		fmt.Sprintf("pg1-host=%s", sourceHost),
		fmt.Sprintf("pg1-host-user=%s", sourceSSHUser),
		fmt.Sprintf("pg1-host-port=%d", sourceSSHPort),
		fmt.Sprintf("pg1-host-cmd=%s", sourceToolCmd),
		fmt.Sprintf("pg1-path=%s", pgDataPath),
	}
	if pgPort > 0 {
		lines = append(lines, fmt.Sprintf("pg1-port=%d", pgPort))
	}
	if strings.TrimSpace(pgUser) != "" {
		lines = append(lines, fmt.Sprintf("pg1-user=%s", strings.TrimSpace(pgUser)))
	}
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

func runLocalCommandCapture(ctx context.Context, workDir string, binPath string, args []string, env map[string]string, logWriter io.Writer) (string, error) {
	var buf bytes.Buffer
	var writer io.Writer = &buf
	if logWriter != nil {
		writer = io.MultiWriter(logWriter, &buf)
	}
	err := runLocalCommand(ctx, workDir, binPath, args, env, writer)
	return strings.TrimSpace(buf.String()), err
}

func detectPostgresSetting(ctx context.Context, toolSpec *localToolSpec, instance BackupExecutionInstance, database string, sslmode string, setting string, logWriter io.Writer) (string, error) {
	if toolSpec == nil {
		return "", fmt.Errorf("psql 工具不可用")
	}
	dbName := strings.TrimSpace(database)
	if dbName == "" {
		dbName = "postgres"
	}
	args := []string{
		"-h", instance.Hostname,
		"-p", fmt.Sprintf("%d", instance.Port),
		"-U", instance.User,
		"-d", dbName,
		"-Atqc", "SHOW " + strings.TrimSpace(setting),
	}
	env := buildPostgresRuntimeEnv(instance, dbName, sslmode, false)
	output, err := runLocalCommandCaptureAsDBUser(ctx, "PostgreSQL", "", toolSpec.Path, args, withLibraryPath(env, toolSpec.LibDirs), logWriter)
	if err != nil {
		return "", err
	}
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line, nil
		}
	}
	return "", fmt.Errorf("未读取到 PostgreSQL 参数 %s", strings.TrimSpace(setting))
}

func buildPostgresRuntimeEnv(instance BackupExecutionInstance, database string, sslmode string, isLocalDB bool) map[string]string {
	env := map[string]string{}
	if password := strings.TrimSpace(instance.Password); password != "" {
		env["PGPASSWORD"] = password
	}
	if !isLocalDB {
		if host := strings.TrimSpace(instance.Hostname); host != "" {
			env["PGHOST"] = host
		}
		if user := strings.TrimSpace(instance.User); user != "" {
			env["PGUSER"] = user
		}
	}
	if instance.Port > 0 {
		env["PGPORT"] = fmt.Sprintf("%d", instance.Port)
	}
	if dbName := strings.TrimSpace(database); dbName != "" {
		env["PGDATABASE"] = dbName
	}
	if mode := strings.TrimSpace(sslmode); mode != "" {
		env["PGSSLMODE"] = mode
	}
	return env
}

// pgBackRestS3RepoLines 生成 pgBackRest 对象存储仓库的 [global] 配置行（repo1-type=s3 及 S3 连接参数）。
// endpoint 剥离 http(s):// 前缀（pgBackRest 期望 host[:port]，TLS 由其自身处理）。
func pgBackRestS3RepoLines(cfg pgBackRestDriverConfig) []string {
	endpoint := strings.TrimSpace(cfg.S3Endpoint)
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")
	region := strings.TrimSpace(cfg.S3Region)
	if region == "" {
		region = "us-east-1"
	}
	uriStyle := strings.ToLower(strings.TrimSpace(cfg.S3URIStyle))
	if uriStyle != "path" && uriStyle != "host" {
		uriStyle = "host"
	}
	return []string{
		"repo1-type=s3",
		fmt.Sprintf("repo1-s3-bucket=%s", strings.TrimSpace(cfg.S3Bucket)),
		fmt.Sprintf("repo1-s3-endpoint=%s", endpoint),
		fmt.Sprintf("repo1-s3-region=%s", region),
		fmt.Sprintf("repo1-s3-key=%s", strings.TrimSpace(cfg.S3AccessKey)),
		fmt.Sprintf("repo1-s3-key-secret=%s", strings.TrimSpace(cfg.S3SecretKey)),
		fmt.Sprintf("repo1-s3-uri-style=%s", uriStyle),
	}
}

// pgBackRestS3CommandOptions 生成 pgBackRest 命令行形式的 S3 仓库参数（用于不落配置文件的远端一次性命令，
// 例如恢复预检脚本里的 `pgbackrest info`）。返回值中每个参数值都做了 shell 单引号转义，
// 可安全内嵌到远端 shell 命令串中执行。返回空串表示非 S3 模式。
func pgBackRestS3CommandOptions(cfg pgBackRestDriverConfig) string {
	if !cfg.IsS3Repo() {
		return ""
	}
	lines := pgBackRestS3RepoLines(cfg)
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		if idx := strings.Index(line, "="); idx > 0 {
			parts = append(parts, "--"+line[:idx]+"="+shellQuote(line[idx+1:]))
		}
	}
	return strings.Join(parts, " ")
}

func buildPGBackRestConfig(cfg pgBackRestDriverConfig, repoPath string, repoHostCmd string, sshCommand string, stanza string, pgDataPath string, port int, pgUser string, isLocalDB bool) string {
	startFastValue := "n"
	if cfg.StartFast {
		startFastValue = "y"
	}
	lines := []string{
		"[global]",
		fmt.Sprintf("repo1-path=%s", repoPath),
	}
	if cfg.IsS3Repo() {
		lines = append(lines, pgBackRestS3RepoLines(cfg)...)
		// 与 repo-host 模式行为对齐：平台不做过期清理（repo1-retention-full 设极大值防 pgBackRest 自动 expire 误删）
		lines = append(lines, "repo1-retention-full=9999999")
	}
	lines = append(lines,
		fmt.Sprintf("process-max=%d", cfg.ProcessMax),
		fmt.Sprintf("start-fast=%s", startFastValue),
	)
	if cfg.RepoRemote && strings.TrimSpace(cfg.RepoHost) != "" {
		lines = append(lines,
			fmt.Sprintf("repo1-host=%s", strings.TrimSpace(cfg.RepoHost)),
			fmt.Sprintf("repo1-host-user=%s", displayOrDefault(strings.TrimSpace(cfg.RepoUser), "root")),
		)
		if cfg.RepoPort > 0 {
			lines = append(lines, fmt.Sprintf("repo1-host-port=%d", cfg.RepoPort))
		}
		remoteCmd := strings.TrimSpace(repoHostCmd)
		if remoteCmd == "" {
			remoteCmd = "pgbackrest"
		}
		remoteCmd = remoteCmd + " --config=/dev/null"
		lines = append(lines, fmt.Sprintf("repo1-host-cmd=%s", remoteCmd))
		if strings.TrimSpace(sshCommand) != "" {
			lines = append(lines, fmt.Sprintf("cmd-ssh=%s", strings.TrimSpace(sshCommand)))
		}
	}
	lines = append(lines, "",
		fmt.Sprintf("[%s]", stanza),
		fmt.Sprintf("pg1-path=%s", pgDataPath),
	)
	socketDirs := []string{"/var/run/postgresql", "/tmp"}
	foundSocketDir := ""
	for _, d := range socketDirs {
		sockPath := filepath.Join(d, fmt.Sprintf(".s.PGSQL.%d", port))
		if st, err := os.Stat(sockPath); err == nil && st.Mode()&os.ModeSocket != 0 {
			foundSocketDir = d
			break
		}
	}
	if foundSocketDir != "" {
		lines = append(lines, fmt.Sprintf("pg1-socket-path=%s", foundSocketDir))
	}
	if port > 0 {
		lines = append(lines, fmt.Sprintf("pg1-port=%d", port))
	}
	if !isLocalDB && strings.TrimSpace(pgUser) != "" {
		lines = append(lines, fmt.Sprintf("pg1-user=%s", strings.TrimSpace(pgUser)))
	}
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

type installedPGBackRestConfig struct {
	ConfigPath string
	Repo1Path  string
	Stanza     string
}

type installedPGBackRestStanza struct {
	Name    string
	PG1Path string
}

func resolveInstalledLocalPGBackRestConfig(ctx context.Context, psqlTool *localToolSpec, instance BackupExecutionInstance, defaultDatabase string, sslmode string, pgDataPath string) (*installedPGBackRestConfig, error) {
	archiveCommand, err := detectPostgresSetting(ctx, psqlTool, instance, defaultDatabase, sslmode, "archive_command", nil)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(strings.ToLower(archiveCommand), "pgbackrest") {
		return nil, nil
	}
	preferredStanza := extractPGBackRestArchiveStanza(archiveCommand)
	return readInstalledPGBackRestConfig("/etc/pgbackrest/pgbackrest.conf", preferredStanza, pgDataPath)
}

func extractPGBackRestArchiveStanza(command string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`--stanza=([A-Za-z0-9_-]+)`),
		regexp.MustCompile(`--stanza\s+([A-Za-z0-9_-]+)`),
	}
	for _, pattern := range patterns {
		matches := pattern.FindStringSubmatch(command)
		if len(matches) >= 2 {
			return strings.TrimSpace(matches[1])
		}
	}
	return ""
}

func readInstalledPGBackRestConfig(configPath string, preferredStanza string, pgDataPath string) (*installedPGBackRestConfig, error) {
	file, err := os.Open(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	repo1Path := ""
	currentSection := "global"
	stanzas := map[string]installedPGBackRestStanza{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if currentSection == "global" && key == "repo1-path" {
			repo1Path = value
			continue
		}
		if key == "pg1-path" && currentSection != "" && currentSection != "global" {
			stanzas[currentSection] = installedPGBackRestStanza{Name: currentSection, PG1Path: value}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if repo1Path == "" {
		return nil, nil
	}
	if preferredStanza != "" {
		if stanza, ok := stanzas[preferredStanza]; ok && samePath(stanza.PG1Path, pgDataPath) {
			return &installedPGBackRestConfig{ConfigPath: configPath, Repo1Path: repo1Path, Stanza: stanza.Name}, nil
		}
	}
	for _, stanza := range stanzas {
		if samePath(stanza.PG1Path, pgDataPath) {
			return &installedPGBackRestConfig{ConfigPath: configPath, Repo1Path: repo1Path, Stanza: stanza.Name}, nil
		}
	}
	return nil, nil
}

func samePath(left string, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func parsePGBackRestInfo(raw string, stanza string) (*pgBackRestBackupMetadata, error) {
	var info []map[string]any
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return nil, err
	}
	stanzaName := strings.TrimSpace(stanza)
	for _, item := range info {
		name := strings.TrimSpace(fmt.Sprintf("%v", item["name"]))
		if stanzaName != "" && name != stanzaName {
			continue
		}
		backups, ok := item["backup"].([]any)
		if !ok || len(backups) == 0 {
			continue
		}
		var latest map[string]any
		var latestStop int64
		for _, rawBackup := range backups {
			backupItem, ok := rawBackup.(map[string]any)
			if !ok {
				continue
			}
			stopAt := int64(0)
			if ts, ok := backupItem["timestamp"].(map[string]any); ok {
				stopAt = int64(anyToInt(ts["stop"], 0))
			}
			if latest == nil || stopAt >= latestStop {
				latest = backupItem
				latestStop = stopAt
			}
		}
		if latest == nil {
			continue
		}
		meta := &pgBackRestBackupMetadata{
			Label:      strings.TrimSpace(fmt.Sprintf("%v", latest["label"])),
			BackupType: strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", latest["type"]))),
			Timestamp:  latestStop,
			Raw:        latest,
		}
		if refs, ok := latest["reference"].([]any); ok {
			for _, ref := range refs {
				text := strings.TrimSpace(fmt.Sprintf("%v", ref))
				if text != "" {
					meta.Reference = append(meta.Reference, text)
				}
			}
		}
		// pgBackRest info JSON: backup: {size: <DB size>, repo: {size: <repo size>}}
		if backupInfo, ok := latest["backup"].(map[string]any); ok {
			meta.BackupSize = int64(anyToInt(backupInfo["size"], 0))
			if repoInfo, ok := backupInfo["repo"].(map[string]any); ok {
				meta.RepoSize = int64(anyToInt(repoInfo["size"], 0))
			}
		}
		if meta.Label == "" {
			return nil, fmt.Errorf("pgBackRest info 中缺少备份标签")
		}
		if meta.BackupType == "" {
			meta.BackupType = "FULL"
		}
		return meta, nil
	}
	return nil, fmt.Errorf("未找到 stanza=%s 的 pgBackRest 备份信息", stanzaName)
}

func emitPGBackRestMetadataArtifact(dir string, payload map[string]any, streamSink BackupStreamSink) (string, string, int64, string, error) {
	fileName := "pgbackrest-backup.json"
	buf, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", "", 0, "", err
	}
	if streamSink != nil {
		if _, err := streamSink.Write(buf); err != nil {
			_ = streamSink.Close()
			return "", "", 0, "", err
		}
		if err := streamSink.Close(); err != nil {
			return "", "", 0, "", err
		}
		return fileName, "", int64(len(buf)), "", nil
	}
	filePath := filepath.Join(dir, fileName)
	if err := os.WriteFile(filePath, buf, 0o644); err != nil {
		return "", "", 0, "", err
	}
	sha, size, err := fileSha256(filePath)
	if err != nil {
		return "", "", 0, "", err
	}
	return fileName, filePath, size, sha, nil
}

func writePGBackRestMetadataFile(dir string, payload map[string]any) (string, string, int64, string, error) {
	label := sanitizePathSegment(displayOrDefault(fmt.Sprintf("%v", payload["backup_label"]), "pgbackrest-backup"), "pgbackrest-backup")
	fileName := label + ".json"
	filePath := filepath.Join(dir, fileName)
	buf, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", "", 0, "", err
	}
	if err := os.WriteFile(filePath, buf, 0o644); err != nil {
		return "", "", 0, "", err
	}
	sha, size, err := fileSha256(filePath)
	if err != nil {
		return "", "", 0, "", err
	}
	return fileName, filePath, size, sha, nil
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func pathSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return info.Size(), nil
	}
	var total int64
	err = filepath.Walk(path, func(_ string, walkInfo os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if walkInfo == nil || walkInfo.IsDir() {
			return nil
		}
		total += walkInfo.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

func recommendedPostgresDumpWorkers(job dumpJob) int {
	objectCount := len(job.PgTables)
	if objectCount == 0 && len(job.PgSchemas) == 0 {
		objectCount = 4
	}
	if objectCount == 0 && len(job.PgSchemas) > 0 {
		objectCount = len(job.PgSchemas)
	}
	if objectCount <= 1 && len(job.PgSchemas) == 0 {
		return 1
	}
	cpuLimit := runtime.NumCPU() / 2
	if cpuLimit <= 0 {
		cpuLimit = 1
	}
	maxWorkers := minInt(4, cpuLimit)
	if maxWorkers < 2 {
		maxWorkers = 2
	}
	if objectCount > 0 {
		maxWorkers = minInt(maxWorkers, objectCount)
	}
	if maxWorkers <= 0 {
		return 1
	}
	return maxWorkers
}

func postgresDumpFormatLabel(parallel bool) string {
	if parallel {
		return "directory"
	}
	return "custom"
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func displayOrDefault(value string, fallback string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return fallback
	}
	return text
}

func shouldDisableMySQLGTIDPurged(serverInfo dbVersionInfo, clientInfo dbVersionInfo) bool {
	if serverInfo.IsMariaDB || clientInfo.IsMariaDB {
		return false
	}
	return true
}

func shouldDisableMySQLColumnStatistics(clientInfo dbVersionInfo) bool {
	if clientInfo.IsMariaDB {
		return false
	}
	return clientInfo.Major >= 8
}

func matchWildcard(input string, pattern string) bool {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return false
	}
	escaped := regexp.QuoteMeta(p)
	escaped = strings.ReplaceAll(escaped, `\*`, ".*")
	escaped = strings.ReplaceAll(escaped, `\%`, ".*")
	escaped = strings.ReplaceAll(escaped, `\_`, ".")
	re, err := regexp.Compile("^" + escaped + "$")
	if err != nil {
		return false
	}
	return re.MatchString(input)
}

func sanitizePathSegment(value string, fallback string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return fallback
	}
	text = strings.ReplaceAll(text, "/", "_")
	text = strings.ReplaceAll(text, "\\", "_")
	text = strings.ReplaceAll(text, "..", "_")
	if text == "" {
		return fallback
	}
	return text
}

func replaceSSHOptionValue(args []string, oldValue string, newValue string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		if out[i] == oldValue {
			out[i] = newValue
		}
	}
	return out
}

func resolveRemotePostgresToolCommand(toolsDir string, serverVersion string, toolName string) string {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return ""
	}
	base := strings.TrimSpace(toolsDir)
	if base == "" {
		return name
	}
	versions := postgresClientVersionCandidates(serverVersion)
	if requiresPostgresExactMajor(name) {
		if exact := postgresExactClientVersion(serverVersion); exact != "" {
			versions = []string{exact}
		}
	}
	for _, version := range versions {
		binPath := filepath.Join(base, "postgres", version, "bin", name)
		libPath := filepath.Join(base, "postgres", version, "lib")
		if strings.TrimSpace(version) == "" {
			continue
		}
		if toolExistsRemoteCandidate(binPath) {
			if dirExistsRemoteCandidate(libPath) {
				return fmt.Sprintf("env LD_LIBRARY_PATH=%s %s", shellQuote(libPath), shellQuote(binPath))
			}
			return shellQuote(binPath)
		}
	}
	fallback := filepath.Join(base, "postgres", "bin", name)
	if toolExistsRemoteCandidate(fallback) {
		return shellQuote(fallback)
	}
	return name
}

func toolExistsRemoteCandidate(path string) bool {
	info, err := os.Stat(strings.TrimSpace(path))
	if err != nil {
		return false
	}
	mode := info.Mode()
	return !mode.IsDir() && mode&0o111 != 0
}

func dirExistsRemoteCandidate(path string) bool {
	info, err := os.Stat(strings.TrimSpace(path))
	if err != nil {
		return false
	}
	return info.IsDir()
}

type pgBackRestRepoRemoteRuntime struct {
	SSHCommand   string
	Env          map[string]string
	cleanupPaths []string
}

type remotePostgresToolSpec struct {
	Path        string
	VersionLine string
	LibDirs     []string
}

func (s *remotePostgresToolSpec) ShellCommand(args ...string) string {
	parts := []string{}
	if s != nil && len(s.LibDirs) > 0 {
		parts = append(parts, "env", "LD_LIBRARY_PATH="+strings.Join(s.LibDirs, ":"))
	}
	path := ""
	if s != nil {
		path = strings.TrimSpace(s.Path)
	}
	if path == "" {
		path = "pgbackrest"
	}
	parts = append(parts, path)
	parts = append(parts, args...)
	return joinShellCommand(parts)
}

func parseRemoteLibDirs(raw string) []string {
	items := []string{}
	seen := map[string]struct{}{}
	for _, item := range strings.Split(strings.TrimSpace(raw), ":") {
		dir := strings.TrimSpace(item)
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		items = append(items, dir)
	}
	return items
}

func resolveRemotePostgresToolSpec(runner *sshRunner, toolsDir string, serverVersion string, toolName string) (*remotePostgresToolSpec, error) {
	if runner == nil {
		return nil, fmt.Errorf("SSH runner 不能为空")
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		return nil, fmt.Errorf("工具名不能为空")
	}
	candidates := remotePostgresToolCandidates(toolsDir, serverVersion, name)
	toolToken := "__PATH_LOOKUP__:" + name
	quotedCandidates := make([]string, 0, len(candidates)+1)
	for _, candidate := range candidates {
		quotedCandidates = append(quotedCandidates, shellQuote(candidate))
	}
	quotedCandidates = append(quotedCandidates, shellQuote(toolToken))
	script := strings.Join([]string{
		"set -e",
		"export PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:$PATH",
		"FOUND=''",
		"for candidate in " + strings.Join(quotedCandidates, " ") + "; do",
		"  if [ \"$candidate\" = " + shellQuote(toolToken) + " ]; then",
		"    if command -v " + shellQuote(name) + " >/dev/null 2>&1; then FOUND=$(command -v " + shellQuote(name) + "); break; fi",
		"  elif [ -x \"$candidate\" ]; then",
		"    FOUND=$candidate; break",
		"  fi",
		"done",
		"if [ -z \"$FOUND\" ]; then echo " + shellQuote("CANDIDATES="+strings.Join(candidates, ",")) + "; exit 41; fi",
		"LIB_PATHS=''",
		"for libdir in \"$(dirname \"$FOUND\")/lib\" \"$(dirname \"$(dirname \"$FOUND\")\")/lib\" \"$(dirname \"$(dirname \"$(dirname \"$FOUND\")\")\")/lib\"; do",
		"  if [ -d \"$libdir\" ]; then",
		"    if [ -n \"$LIB_PATHS\" ]; then LIB_PATHS=\"$LIB_PATHS:$libdir\"; else LIB_PATHS=\"$libdir\"; fi",
		"  fi",
		"done",
		"if [ -n \"$LIB_PATHS\" ]; then VERSION_LINE=$(env LD_LIBRARY_PATH=\"$LIB_PATHS\" \"$FOUND\" --version 2>/dev/null | head -n1 | tr -d '\\r'); else VERSION_LINE=$($FOUND --version 2>/dev/null | head -n1 | tr -d '\\r'); fi",
		"echo FOUND=$FOUND",
		"echo LIB_PATHS=$LIB_PATHS",
		"echo VERSION_LINE=$VERSION_LINE",
		"echo RESOLVED_PATH=$PATH",
	}, "\n")
	output, err := runner.Run(script)
	if err != nil {
		return nil, fmt.Errorf("%w\n%s", err, strings.TrimSpace(output))
	}
	parsed := parseKeyValueOutput(output)
	found := strings.TrimSpace(parsed["FOUND"])
	if found == "" {
		return nil, fmt.Errorf("未找到 %s，可选路径=%s", name, strings.TrimSpace(parsed["CANDIDATES"]))
	}
	return &remotePostgresToolSpec{
		Path:        found,
		VersionLine: strings.TrimSpace(parsed["VERSION_LINE"]),
		LibDirs:     parseRemoteLibDirs(parsed["LIB_PATHS"]),
	}, nil
}

func (r *pgBackRestRepoRemoteRuntime) Cleanup(logFile backupRunLogWriter) {
	if r == nil {
		return
	}
	for _, path := range r.cleanupPaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			if logFile != nil {
				logFile.Warnf("清理 pgBackRest 异地仓库运行时文件失败 path=%s: %v", path, err)
			}
		}
	}
}

func preparePGBackRestRepoRemoteRuntime(ctx context.Context, runDir string, cfg pgBackRestDriverConfig, logFile backupRunLogWriter, pgOSUser string, pgSharedGroup string) (*pgBackRestRepoRemoteRuntime, error) {
	runtimeCfg := &pgBackRestRepoRemoteRuntime{Env: map[string]string{}}
	if !cfg.RepoRemote || strings.TrimSpace(cfg.RepoHost) == "" {
		return runtimeCfg, nil
	}
	dbOSUser, err := detectDatabaseOSUser("PostgreSQL")
	if err != nil {
		return nil, err
	}
	repoUserHome := ""
	if runtime.GOOS == "linux" {
		if homeOut, homeErr := runLocalCommandCaptureAsRoot(ctx, "", "sh", []string{"-lc", "getent passwd " + dbOSUser + " | cut -d: -f6"}, nil, nil); homeErr == nil && strings.TrimSpace(homeOut) != "" {
			repoUserHome = strings.TrimSpace(homeOut)
		}
	}
	if repoUserHome == "" {
		repoUserHome = "/var/lib/" + dbOSUser
	}
	sshKeyPath := filepath.Join(repoUserHome, ".ssh", "id_ed25519")
	sshOptions := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes",
		"-o", "LogLevel=ERROR",
	}
	if cfg.RepoPort > 0 {
		sshOptions = append(sshOptions, "-o", fmt.Sprintf("Port=%d", cfg.RepoPort))
	}
	commandPrefix := []string{"ssh"}
	if keyOut, keyErr := runLocalCommandCaptureAsDBUser(ctx, "PostgreSQL", "", "bash", []string{"-lc", "test -f " + shellQuote(sshKeyPath) + " && test -r " + shellQuote(sshKeyPath) + " && echo OK || echo MISSING"}, nil, nil); keyErr == nil && strings.TrimSpace(keyOut) == "OK" {
		sshOptions = append(sshOptions, "-i", sshKeyPath)
		logFile.Infof("异地仓库直写：使用 %s 专属 SSH Key=%s", dbOSUser, sshKeyPath)
	} else if repoKey := strings.TrimSpace(cfg.RepoSSHPrivateKey); repoKey != "" {
		keyPath := filepath.Join(runDir, "pgbackrest-repo-id")
		if err := os.WriteFile(keyPath, []byte(repoKey+"\n"), 0o600); err != nil {
			return nil, fmt.Errorf("写入异地仓库 SSH 私钥临时文件失败: %w", err)
		}
		runtimeCfg.cleanupPaths = append(runtimeCfg.cleanupPaths, keyPath)
		if err := adjustPGBackRestRuntimeFileOwnership(ctx, keyPath, "0600", pgOSUser, pgSharedGroup); err != nil {
			return nil, err
		}
		sshOptions = append(sshOptions, "-i", keyPath, "-o", "IdentitiesOnly=yes")
		logFile.Infof("异地仓库直写：fallback 使用存储目标 SSH 私钥临时文件=%s", keyPath)
	} else if repoPass := strings.TrimSpace(cfg.RepoSSHPassword); repoPass != "" {
		if _, lookErr := exec.LookPath("sshpass"); lookErr != nil {
			return nil, fmt.Errorf("执行节点缺少 sshpass 工具，无法使用密码登录 SSH 直写异地仓库，请先安装 sshpass")
		}
		commandPrefix = []string{"sshpass", "-e", "ssh"}
		runtimeCfg.Env["SSHPASS"] = repoPass
		sshOptions = replaceSSHOptionValue(sshOptions, "BatchMode=yes", "BatchMode=no")
		sshOptions = append(sshOptions,
			"-o", "PasswordAuthentication=yes",
			"-o", "PreferredAuthentications=password",
			"-o", "PubkeyAuthentication=no",
			"-o", "NumberOfPasswordPrompts=1",
		)
		logFile.Infof("异地仓库直写：fallback 使用存储目标 SSH 密码登录（sshpass）")
	} else {
		logFile.Infof("异地仓库直写：未检测到专属 SSH Key，使用执行节点默认 SSH 认证链路")
	}
	scriptPath := filepath.Join(runDir, "pgbackrest-repo-ssh.sh")
	debugPath := filepath.Join(runDir, "pgbackrest-repo-ssh.debug.log")
	commandParts := append([]string(nil), commandPrefix...)
	commandParts = append(commandParts, sshOptions...)
	baseExecCmd := joinShellCommand(commandParts)
	scriptText := "#!/usr/bin/env bash\nset -euo pipefail\n" +
		"filtered_args=()\n" +
		"while (($#)); do\n" +
		"  if [[ \"$1\" == \"-o\" && $# -ge 2 ]]; then\n" +
		"    case \"$2\" in\n" +
		"      PasswordAuthentication=*|PubkeyAuthentication=*|PreferredAuthentications=*|BatchMode=*|NumberOfPasswordPrompts=*)\n" +
		"        shift 2\n" +
		"        continue\n" +
		"        ;;\n" +
		"    esac\n" +
		"    filtered_args+=(\"$1\" \"$2\")\n" +
		"    shift 2\n" +
		"    continue\n" +
		"  fi\n" +
		"  filtered_args+=(\"$1\")\n" +
		"  shift\n" +
		"done\n" +
		"# #region debug-point A-B-C:pgbackrest-cmd-ssh\n" +
		"{\n" +
		"  echo \"ts=$(date '+%Y-%m-%d %H:%M:%S')\"\n" +
		"  echo \"argv_count=${#filtered_args[@]}\"\n" +
		"  echo \"raw_argv=$*\"\n" +
		"  echo \"filtered_argv=${filtered_args[*]}\"\n" +
		"  if [ -n \"${SSHPASS:-}\" ]; then echo \"sshpass_present=1\"; else echo \"sshpass_present=0\"; fi\n" +
		"  echo \"exec_cmd=" + strings.ReplaceAll(baseExecCmd, "\"", "\\\"") + " ${filtered_args[*]}\"\n" +
		"  echo \"---\"\n" +
		"} >>" + shellQuote(debugPath) + " 2>&1\n" +
		"# #endregion\n" +
		"exec " + baseExecCmd + " \"${filtered_args[@]}\"\n"
	if err := os.WriteFile(scriptPath, []byte(scriptText), 0o750); err != nil {
		return nil, fmt.Errorf("写入 pgBackRest 异地仓库 SSH 包装脚本失败: %w", err)
	}
	runtimeCfg.cleanupPaths = append(runtimeCfg.cleanupPaths, scriptPath)
	if err := adjustPGBackRestRuntimeFileOwnership(ctx, scriptPath, "0750", pgOSUser, pgSharedGroup); err != nil {
		return nil, err
	}
	runtimeCfg.SSHCommand = scriptPath
	return runtimeCfg, nil
}

func adjustPGBackRestRuntimeFileOwnership(ctx context.Context, path string, mode string, pgOSUser string, pgSharedGroup string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if strings.TrimSpace(pgOSUser) == "" || strings.TrimSpace(pgOSUser) == strings.TrimSpace(currentOSUser()) {
		if mode != "" {
			if err := os.Chmod(path, parseFileMode(mode)); err != nil {
				return fmt.Errorf("调整运行时文件权限失败: %w（文件=%s）", err, path)
			}
		}
		return nil
	}
	group := strings.TrimSpace(pgSharedGroup)
	if group == "" {
		group = strings.TrimSpace(pgOSUser)
	}
	if _, err := runLocalCommandCaptureAsRoot(ctx, "", "chown", []string{pgOSUser + ":" + group, path}, nil, nil); err != nil {
		return fmt.Errorf("调整运行时文件属主失败: %w（文件=%s）", err, path)
	}
	if mode != "" {
		if _, err := runLocalCommandCaptureAsRoot(ctx, "", "chmod", []string{mode, path}, nil, nil); err != nil {
			return fmt.Errorf("调整运行时文件权限失败: %w（文件=%s）", err, path)
		}
	}
	return nil
}

func parseFileMode(text string) os.FileMode {
	value, err := strconv.ParseUint(strings.TrimSpace(text), 8, 32)
	if err != nil {
		return 0o600
	}
	return os.FileMode(value)
}

func joinShellCommand(parts []string) string {
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		quoted = append(quoted, shellQuote(part))
	}
	return strings.Join(quoted, " ")
}

func registerPGBackRestLocalStopHandler(runControl *backupRunControl, logFile backupRunLogWriter, runDir string, stanza string) {
	if runControl == nil {
		return
	}
	runControl.AddStopHandler(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if logFile != nil {
			logFile.Warnf("开始执行 PG 物理备份本机停止清理 run_dir=%s stanza=%s", runDir, stanza)
		}
		output, err := runLocalCommandCaptureAsRoot(ctx, "", "bash", []string{"-lc", buildPGBackRestLocalStopScript(runDir, stanza)}, nil, nil)
		if err != nil {
			if logFile != nil {
				logFile.Warnf("执行 PG 物理备份本机停止清理失败: %v output=%s", err, formatCommandFailureOutput(output))
			}
			return
		}
		if logFile != nil && strings.TrimSpace(output) != "" {
			logFile.Warnf("PG 物理备份本机停止清理完成: %s", formatCommandFailureOutput(output))
		}
	})
}

func registerPGBackRestRepoHostStopHandler(runControl *backupRunControl, logFile backupRunLogWriter, cfg pgBackRestDriverConfig, repoRemoteRuntime *pgBackRestRepoRemoteRuntime, runDir string, repoWorkDir string, repoConfigPath string, sourceSSHScriptPath string, sourceSSHUser string, sourceHost string, stanza string, pgDataPath string) {
	if runControl == nil {
		return
	}
	registerPGBackRestLocalStopHandler(runControl, logFile, runDir, stanza)
	if repoRemoteRuntime == nil || strings.TrimSpace(repoRemoteRuntime.SSHCommand) == "" {
		return
	}
	runControl.AddStopHandler(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if logFile != nil {
			logFile.Warnf("开始执行 PG 物理备份仓库机停止清理 repo_work_dir=%s stanza=%s", repoWorkDir, stanza)
		}
		cmd := buildPGBackRestRepoHostStopScript(repoConfigPath, repoWorkDir, sourceSSHScriptPath, sourceSSHUser, sourceHost, stanza, pgDataPath)
		output, err := runPGBackRestRepoRemoteCommand(ctx, repoRemoteRuntime, cfg, cmd, nil)
		if err != nil {
			if logFile != nil {
				logFile.Warnf("执行 PG 物理备份仓库机停止清理失败: %v output=%s", err, formatCommandFailureOutput(output))
			}
			return
		}
		if logFile != nil && strings.TrimSpace(output) != "" {
			logFile.Warnf("PG 物理备份仓库机停止清理完成: %s", formatCommandFailureOutput(output))
		}
	})
}

func buildPGBackRestLocalStopScript(runDir string, stanza string) string {
	stanzaArg := "--stanza=" + strings.TrimSpace(stanza)
	return strings.Join([]string{
		"set +e",
		"SELF=$$",
		"PIDS=$(ps -eo pid=,args= | awk -v self=\"$SELF\" -v run_dir=" + shellQuote(strings.TrimSpace(runDir)) + " -v stanza=" + shellQuote(stanzaArg) + " '",
		"  {",
		"    pid=$1; line=$0;",
		"    if (pid == self) next;",
		"    if ((run_dir != \"\" && index(line, run_dir) > 0) ||",
		"        (stanza != \"\" && index(line, stanza) > 0 && (index(line, \"pgbackrest\") > 0 || index(line, \"sshpass\") > 0 || index(line, \"ssh \") > 0 || index(line, \"sudo \") > 0 || index(line, \"/bin/sh -lc\") > 0))) {",
		"      print pid",
		"    }",
		"  }' | sort -u | xargs echo -n)",
		"if [ -n \"$PIDS\" ]; then",
		"  kill -TERM $PIDS 2>/dev/null || true",
		"  sleep 2",
		"  REMAIN=$(ps -o pid= -p $PIDS 2>/dev/null | xargs echo -n)",
		"  if [ -n \"$REMAIN\" ]; then",
		"    kill -KILL $REMAIN 2>/dev/null || true",
		"  fi",
		"fi",
		"echo local_pids=${PIDS:-none}",
	}, "\n")
}

func buildPGBackRestRepoHostStopScript(repoConfigPath string, repoWorkDir string, sourceSSHScriptPath string, sourceSSHUser string, sourceHost string, stanza string, pgDataPath string) string {
	stanzaArg := "--stanza=" + strings.TrimSpace(stanza)
	sourceTarget := strings.TrimSpace(sourceSSHUser) + "@" + strings.TrimSpace(sourceHost)
	sourceRemoteKill := joinShellCommand([]string{
		"sudo", "-n", "/bin/sh", "-lc", buildPGBackRestSourceRemoteStopScript(stanza, pgDataPath),
	})
	lines := []string{
		"set +e",
		"SELF=$$",
	}
	if strings.TrimSpace(sourceSSHScriptPath) != "" && strings.TrimSpace(sourceTarget) != "" {
		lines = append(lines,
			"SOURCE_STOP_CMD="+shellQuote(joinShellCommand([]string{sourceSSHScriptPath, sourceTarget, sourceRemoteKill})),
			"if [ -x "+shellQuote(sourceSSHScriptPath)+" ]; then",
			"  eval \"$SOURCE_STOP_CMD\" >/dev/null 2>&1 || true",
			"fi",
		)
	}
	lines = append(lines,
		"REPO_PIDS=$(ps -eo pid=,args= | awk -v self=\"$SELF\" -v cfg="+shellQuote(strings.TrimSpace(repoConfigPath))+" -v work="+shellQuote(strings.TrimSpace(repoWorkDir))+" -v stanza="+shellQuote(stanzaArg)+" '",
		"  {",
		"    pid=$1; line=$0;",
		"    if (pid == self) next;",
		"    if ((cfg != \"\" && index(line, cfg) > 0 && index(line, stanza) > 0 && index(line, \"pgbackrest\") > 0) ||",
		"        (work != \"\" && index(line, work) > 0 && (index(line, \"pgbackrest\") > 0 || index(line, \"sshpass\") > 0 || index(line, \"ssh \") > 0 || index(line, \"pgbackrest-source-ssh.sh\") > 0))) {",
		"      print pid",
		"    }",
		"  }' | sort -u | xargs echo -n)",
		"if [ -n \"$REPO_PIDS\" ]; then",
		"  kill -TERM $REPO_PIDS 2>/dev/null || true",
		"  sleep 2",
		"  REPO_REMAIN=$(ps -o pid= -p $REPO_PIDS 2>/dev/null | xargs echo -n)",
		"  if [ -n \"$REPO_REMAIN\" ]; then",
		"    kill -KILL $REPO_REMAIN 2>/dev/null || true",
		"  fi",
		"fi",
		"echo repo_pids=${REPO_PIDS:-none}",
	)
	return strings.Join(lines, "\n")
}

func buildPGBackRestSourceRemoteStopScript(stanza string, pgDataPath string) string {
	stanzaArg := "--stanza=" + strings.TrimSpace(stanza)
	pgDataArg := "--pg1-path=" + strings.TrimSpace(pgDataPath)
	return strings.Join([]string{
		"set +e",
		"SELF=$$",
		"PIDS=$(ps -eo pid=,args= | awk -v self=\"$SELF\" -v stanza=" + shellQuote(stanzaArg) + " -v pgpath=" + shellQuote(pgDataArg) + " '",
		"  {",
		"    pid=$1; line=$0;",
		"    if (pid == self) next;",
		"    if (index(line, \"pgbackrest\") > 0 && index(line, stanza) > 0 && (index(line, \"backup:remote\") > 0 || index(line, pgpath) > 0)) {",
		"      print pid",
		"    }",
		"  }' | sort -u | xargs echo -n)",
		"if [ -n \"$PIDS\" ]; then",
		"  kill -TERM $PIDS 2>/dev/null || true",
		"  sleep 2",
		"  REMAIN=$(ps -o pid= -p $PIDS 2>/dev/null | xargs echo -n)",
		"  if [ -n \"$REMAIN\" ]; then",
		"    kill -KILL $REMAIN 2>/dev/null || true",
		"  fi",
		"fi",
		"echo source_pids=${PIDS:-none}",
	}, "\n")
}

func runPGBackRestRepoRemoteCommand(ctx context.Context, runtimeCfg *pgBackRestRepoRemoteRuntime, cfg pgBackRestDriverConfig, remoteCmd string, logFile io.Writer) (string, error) {
	if runtimeCfg == nil || strings.TrimSpace(runtimeCfg.SSHCommand) == "" {
		return "", fmt.Errorf("异地仓库 SSH 运行时未初始化")
	}
	target := displayOrDefault(strings.TrimSpace(cfg.RepoUser), "root") + "@" + strings.TrimSpace(cfg.RepoHost)
	return runLocalCommandCaptureAsDBUser(ctx, "PostgreSQL", "", runtimeCfg.SSHCommand, []string{target, remoteCmd}, runtimeCfg.Env, logFile)
}

type writeCounter struct {
	mu sync.Mutex
	n  int64
}

func (c *writeCounter) Write(p []byte) (int, error) {
	n := len(p)
	c.mu.Lock()
	c.n += int64(n)
	c.mu.Unlock()
	return n, nil
}

func (c *writeCounter) Value() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

type flushWriter struct {
	w io.Writer
}

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err != nil {
		return n, err
	}
	if flusher, ok := f.w.(interface{ Flush() error }); ok {
		_ = flusher.Flush()
	} else if syncer, ok := f.w.(interface{ Sync() error }); ok {
		_ = syncer.Sync()
	}
	return n, nil
}

type backupArtifactStream struct {
	sink        BackupStreamSink
	zipWriter   *zip.Writer
	gzipWriter  *gzip.Writer
	plain       io.Writer
	sizeCounter *writeCounter
	hasher      hash.Hash
}

func newBackupArtifactStream(sink BackupStreamSink, compression string, level string) *backupArtifactStream {
	counter := &writeCounter{}
	hasher := sha256.New()
	// hasher 与 sink 挂在同一个 MultiWriter 上：zip/gzip 的 Close 也会把尾部字节写进来，
	// 因此 Close 之后取到的摘要就是最终产物（含压缩封装）的完整 sha256。
	baseWriter := io.MultiWriter(sink, counter, hasher)
	stream := &backupArtifactStream{
		sink:        sink,
		plain:       &flushWriter{w: baseWriter},
		sizeCounter: counter,
		hasher:      hasher,
	}
	switch strings.ToLower(strings.TrimSpace(compression)) {
	case ArtifactCompressionZip:
		stream.zipWriter = zip.NewWriter(baseWriter)
	case ArtifactCompressionGzip:
		// 压缩级别来自任务级选项：FAST（吞吐优先，默认）/ HIGH（压缩比优先）
		gz, err := gzip.NewWriterLevel(baseWriter, gzipLevelForArtifact(level))
		if err != nil {
			gz = gzip.NewWriter(baseWriter)
		}
		stream.gzipWriter = gz
	}
	return stream
}

func (s *backupArtifactStream) WriterForEntry(name string) (io.Writer, error) {
	if s == nil {
		return nil, fmt.Errorf("stream sink 未初始化")
	}
	if s.zipWriter != nil {
		hdr := &zip.FileHeader{
			Name:   filepath.ToSlash(strings.TrimSpace(name)),
			Method: zip.Deflate,
		}
		hdr.Modified = time.Now()
		return s.zipWriter.CreateHeader(hdr)
	}
	if s.gzipWriter != nil {
		// gzip 为单文件流：所有条目写入同一压缩流，产物名已由 plan 打上 .gz 后缀
		return s.gzipWriter, nil
	}
	return s.plain, nil
}

func (s *backupArtifactStream) Close() error {
	if s == nil || s.sink == nil {
		return nil
	}
	if s.gzipWriter != nil {
		if err := s.gzipWriter.Close(); err != nil {
			_ = s.sink.Close()
			return err
		}
	}
	if s.zipWriter != nil {
		if err := s.zipWriter.Close(); err != nil {
			_ = s.sink.Close()
			return err
		}
	}
	return s.sink.Close()
}

func (s *backupArtifactStream) SizeBytes() int64 {
	if s == nil || s.sizeCounter == nil {
		return 0
	}
	return s.sizeCounter.Value()
}

// Sha256 返回直传过程中累计的产物摘要，必须在 Close 之后调用。
func (s *backupArtifactStream) Sha256() string {
	if s == nil || s.hasher == nil {
		return ""
	}
	return hex.EncodeToString(s.hasher.Sum(nil))
}
