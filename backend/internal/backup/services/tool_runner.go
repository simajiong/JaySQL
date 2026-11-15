package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type localToolSpec struct {
	Path    string
	Name    string
	Version string
	Source  string
	LibDirs []string
}

type dbVersionInfo struct {
	Raw       string
	Prefix    string
	Major     int
	Minor     int
	IsMariaDB bool
}

var (
	versionPrefixRe = regexp.MustCompile(`^\d+(?:\.\d+)?`)
	versionAnyRe    = regexp.MustCompile(`\d+(?:\.\d+)?`)
)

func runLocalCommand(ctx context.Context, workDir string, binPath string, args []string, env map[string]string, logWriter io.Writer) error {
	return runLocalCommandWithStdoutOverride(ctx, workDir, binPath, args, env, logWriter, nil)
}

func runLocalCommandWithStdoutOverride(ctx context.Context, workDir string, binPath string, args []string, env map[string]string, logWriter io.Writer, stdoutOverride io.Writer) error {
	cmd := exec.CommandContext(ctx, binPath, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), mapToEnv(env)...)
	}
	if stdoutOverride != nil {
		cmd.Stdout = stdoutOverride
	} else {
		cmd.Stdout = logWriter
	}
	stderrW := logWriter
	if logWriter != nil && stdoutOverride != nil {
		stderrW = logWriter
	} else if logWriter == nil && stdoutOverride != nil {
		stderrW = io.Discard
	}
	cmd.Stderr = stderrW
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
			if cmd.Process == nil {
				return
			}
			if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
				_ = syscall.Kill(-pgid, syscall.SIGTERM)
				time.Sleep(2 * time.Second)
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
				return
			}
			_ = cmd.Process.Kill()
		}
	}()
	return cmd.Run()
}

func withLibraryPath(env map[string]string, libDirs []string) map[string]string {
	if len(libDirs) == 0 {
		return env
	}
	out := map[string]string{}
	for k, v := range env {
		out[k] = v
	}
	parts := []string{}
	seen := map[string]struct{}{}
	for _, item := range libDirs {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		cleaned := filepath.Clean(v)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		parts = append(parts, cleaned)
	}
	if len(parts) == 0 {
		return env
	}
	existing := strings.TrimSpace(out["LD_LIBRARY_PATH"])
	if existing == "" {
		existing = strings.TrimSpace(os.Getenv("LD_LIBRARY_PATH"))
	}
	if existing != "" {
		parts = append(parts, existing)
	}
	out["LD_LIBRARY_PATH"] = strings.Join(parts, ":")
	return out
}

func resolveMySQLClientTool(toolName string, serverVersion string) (*localToolSpec, error) {
	versions := mysqlClientVersionCandidates(serverVersion)
	info := parseDBVersionInfo(serverVersion)
	toolNames := []string{toolName}
	if info.IsMariaDB && strings.EqualFold(strings.TrimSpace(toolName), "mysqldump") {
		toolNames = []string{"mariadb-dump", "mysqldump"}
	}
	errs := make([]string, 0, len(toolNames))
	for _, name := range toolNames {
		spec, err := resolveLocalTool("mysql", name, versions)
		if err == nil {
			return spec, nil
		}
		spec, dbUserErr := resolveMySQLClientToolAsDBUser(name, serverVersion)
		if dbUserErr == nil {
			return spec, nil
		}
		err = fmt.Errorf("%v; 数据库用户上下文探测失败: %w", err, dbUserErr)
		errs = append(errs, fmt.Sprintf("%s: %v", name, err))
	}
	return nil, fmt.Errorf("解析 MySQL 客户端工具失败: %s", strings.Join(errs, "; "))
}

func resolveMySQLClientToolAsDBUser(toolName string, serverVersion string) (*localToolSpec, error) {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return nil, fmt.Errorf("工具名不能为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := runLocalCommandCaptureAsDBUser(
		ctx,
		"MySQL",
		"",
		"/bin/sh",
		[]string{"-lc", buildResolveMySQLToolAsDBUserScript(name, serverVersion)},
		nil,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("未找到 %s 工具，请将其随程序发布到 tools/mysql/<version>/ 目录、加入 PATH，或确保数据库 OS 用户可直接执行 %s: %w", name, name, err)
	}
	parsed := parseKeyValueOutput(output)
	path := strings.TrimSpace(parsed["TOOL_PATH"])
	if path == "" {
		return nil, fmt.Errorf("数据库 OS 用户上下文未找到 %s 工具", name)
	}
	return &localToolSpec{
		Path:    path,
		Name:    name,
		Version: strings.TrimSpace(parsed["VERSION_LINE"]),
		Source:  "db_user:path",
		LibDirs: detectToolLibDirs(path),
	}, nil
}

func buildResolveMySQLToolAsDBUserScript(toolName string, serverVersion string) string {
	name := strings.TrimSpace(toolName)
	version := strings.TrimSpace(versionPrefixRe.FindString(strings.TrimSpace(serverVersion)))
	major := ""
	if version != "" {
		parts := strings.Split(version, ".")
		if len(parts) > 0 {
			major = strings.TrimSpace(parts[0])
		}
	}
	return strings.Join([]string{
		"set -e",
		"TOOL_NAME=" + shellQuote(name),
		"MYSQL_MAJOR=" + shellQuote(major),
		"MYSQL_VERSION=" + shellQuote(version),
		buildMySQLToolLookupSnippet(),
		`if [ -z "$TOOL_PATH" ]; then exit 41; fi`,
		`echo TOOL_PATH=$TOOL_PATH`,
		`echo VERSION_LINE=$VERSION_LINE`,
	}, "\n")
}

func resolvePostgresClientTool(toolName string, serverVersion string) (*localToolSpec, error) {
	versions := postgresClientVersionCandidates(serverVersion)
	if requiresPostgresExactMajor(toolName) {
		if exact := postgresExactClientVersion(serverVersion); exact != "" {
			versions = []string{exact}
		}
	}
	spec, err := resolveLocalTool("postgres", toolName, versions)
	if err != nil {
		spec, err = resolvePostgresClientToolAsDBUser(toolName, serverVersion)
		if err != nil {
			return nil, err
		}
	}
	if requiresPostgresExactMajor(toolName) {
		if err := validatePostgresClientCompatibility(toolName, spec, serverVersion); err != nil {
			return nil, err
		}
	}
	return spec, nil
}

func resolveConfiguredLogicalToolSpec(dbType string, serverVersion string, cfg logicalBackupDriverConfig) (*localToolSpec, error) {
	toolPath := strings.TrimSpace(cfg.ToolPath)
	if toolPath == "" {
		return nil, nil
	}
	if filepath.IsAbs(toolPath) {
		info, err := os.Stat(toolPath)
		if err != nil {
			return nil, fmt.Errorf("用户指定的逻辑备份工具不存在: %w", err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("用户指定的逻辑备份工具路径不能是目录: %s", toolPath)
		}
		if info.Mode()&0o111 == 0 {
			return nil, fmt.Errorf("用户指定的逻辑备份工具不可执行: %s", toolPath)
		}
	}
	spec := &localToolSpec{
		Path:   toolPath,
		Name:   filepath.Base(toolPath),
		Source: "user_config:path",
	}
	if filepath.IsAbs(toolPath) {
		spec.Version = probeCommandVersion(toolPath)
		spec.LibDirs = detectToolLibDirs(toolPath)
	}
	if filepath.IsAbs(toolPath) && strings.EqualFold(strings.TrimSpace(dbType), "PostgreSQL") {
		if err := validatePostgresClientCompatibility("pg_dump", spec, serverVersion); err != nil {
			return nil, err
		}
	}
	return spec, nil
}

// resolveLogicalClientTool 解析逻辑备份/恢复所需的客户端工具。
//   - PostgreSQL：沿用原有解析链（随程序发布目录 → DB OS 用户上下文 + major 强校验）
//   - KingbaseES：跳过 major 校验，按装机目录与金仓 OS 用户查找 sys_dump / sys_restore / ksql
func resolveLogicalClientTool(dbType string, role string, serverVersion string) (*localToolSpec, error) {
	if !isKingbaseDBType(dbType) {
		return resolvePostgresClientTool(logicalToolName(dbType, role), serverVersion)
	}
	candidates := logicalToolNameCandidates(dbType, role)
	var lastErr error
	for _, name := range candidates {
		spec, err := resolveKingbaseClientTool(name)
		if err == nil {
			return spec, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf(
		"未找到金仓客户端工具 %v：请确认数据库服务器已安装完整客户端，或将 sys_dump/sys_restore/ksql 所在目录加入 PATH（默认 /opt/Kingbase/ES/V8/Server/bin），也可在任务「高级选项」里手填工具路径。底层错误: %v",
		candidates, lastErr,
	)
}

func resolveKingbaseClientTool(toolName string) (*localToolSpec, error) {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return nil, fmt.Errorf("工具名不能为空")
	}
	buildSpec := func(path string, version string, source string) *localToolSpec {
		return &localToolSpec{
			Path:    path,
			Name:    name,
			Version: version,
			Source:  source,
			LibDirs: detectToolLibDirs(path),
		}
	}
	// 1) 随程序发布的 tools 目录 / 常见装机目录直接命中（不走 sudo，最快）
	for _, base := range backupToolBaseDirs() {
		for _, candidate := range toolCandidatePaths(base, "kingbase", "", name) {
			if isExecutableFile(candidate) {
				return buildSpec(candidate, probeCommandVersion(candidate), "local:kingbase"), nil
			}
		}
	}
	for _, dir := range logicalToolInstallDirs("Kingbase") {
		candidate := filepath.Join(dir, name)
		if isExecutableFile(candidate) {
			return buildSpec(candidate, probeCommandVersion(candidate), "local:kingbase"), nil
		}
	}
	// 2) 以金仓 OS 用户身份在 PATH 中查找（README 里让运维把 bin 加进 kingbase 用户的 PATH）
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := runLocalCommandCaptureAsDBUser(
		ctx,
		"Kingbase",
		"",
		"/bin/sh",
		[]string{"-lc", buildResolveKingbaseToolAsDBUserScript(name)},
		nil,
		nil,
	)
	if err != nil {
		return nil, err
	}
	parsed := parseKeyValueOutput(output)
	path := strings.TrimSpace(parsed["TOOL_PATH"])
	if path == "" {
		return nil, fmt.Errorf("数据库 OS 用户上下文未找到 %s", name)
	}
	return buildSpec(path, strings.TrimSpace(parsed["VERSION_LINE"]), "db_user:path"), nil
}

func buildResolveKingbaseToolAsDBUserScript(toolName string) string {
	return strings.Join([]string{
		"set -e",
		"TOOL_NAME=" + shellQuote(strings.TrimSpace(toolName)),
		buildKingbaseToolLookupSnippet(),
		`if [ -z "$TOOL_PATH" ]; then exit 41; fi`,
		`echo TOOL_PATH=$TOOL_PATH`,
		`echo VERSION_LINE=$VERSION_LINE`,
	}, "\n")
}

func resolvePostgresClientToolAsDBUser(toolName string, serverVersion string) (*localToolSpec, error) {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return nil, fmt.Errorf("工具名不能为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := runLocalCommandCaptureAsDBUser(
		ctx,
		"PostgreSQL",
		"",
		"/bin/sh",
		[]string{"-lc", buildResolvePostgresToolAsDBUserScript(name, serverVersion)},
		nil,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("未找到 %s 工具，请将其随程序发布到 tools/postgres/<version>/ 目录、加入 PATH，或确保数据库 OS 用户可直接执行 %s: %w", name, name, err)
	}
	parsed := parseKeyValueOutput(output)
	path := strings.TrimSpace(parsed["TOOL_PATH"])
	if path == "" {
		return nil, fmt.Errorf("数据库 OS 用户上下文未找到 %s 工具", name)
	}
	return &localToolSpec{
		Path:    path,
		Name:    name,
		Version: strings.TrimSpace(parsed["VERSION_LINE"]),
		Source:  "db_user:path",
		LibDirs: detectToolLibDirs(path),
	}, nil
}

func buildResolvePostgresToolAsDBUserScript(toolName string, serverVersion string) string {
	name := strings.TrimSpace(toolName)
	major := strings.TrimSpace(postgresExactClientVersion(serverVersion))
	return strings.Join([]string{
		"set -e",
		"TOOL_NAME=" + shellQuote(name),
		"PG_MAJOR=" + shellQuote(major),
		buildPostgresToolLookupSnippet(),
		`if [ -z "$TOOL_PATH" ]; then exit 41; fi`,
		`echo TOOL_PATH=$TOOL_PATH`,
		`echo VERSION_LINE=$VERSION_LINE`,
	}, "\n")
}

func buildMySQLToolLookupSnippet() string {
	candidates := []string{
		`"/usr/local/mysql/bin/$TOOL_NAME"`,
		`"/usr/mysql/bin/$TOOL_NAME"`,
		`"/usr/local/mariadb/bin/$TOOL_NAME"`,
		`"/opt/mysql/bin/$TOOL_NAME"`,
		`"/opt/mariadb/bin/$TOOL_NAME"`,
		`"/usr/local/mysql/$MYSQL_MAJOR/bin/$TOOL_NAME"`,
		`"/usr/local/mariadb/$MYSQL_MAJOR/bin/$TOOL_NAME"`,
		`"/usr/local/bin/$TOOL_NAME"`,
		`"/usr/bin/$TOOL_NAME"`,
		`"/bin/$TOOL_NAME"`,
	}
	return strings.Join([]string{
		`find_first_executable() { for candidate in "$@"; do if [ -n "$candidate" ] && [ -x "$candidate" ]; then printf "%s\n" "$candidate"; return 0; fi; done; return 1; }`,
		`export PATH="${PROCESS_BIN_DIR:+$PROCESS_BIN_DIR:}/usr/local/mysql/bin:/usr/mysql/bin:/usr/local/mariadb/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$PATH"`,
		`TOOL_PATH=$(command -v "$TOOL_NAME" 2>/dev/null || true)`,
		`if [ -z "$TOOL_PATH" ] && [ -n "$PROCESS_BIN_DIR" ]; then TOOL_PATH=$(find_first_executable "$PROCESS_BIN_DIR/$TOOL_NAME" 2>/dev/null || true); fi`,
		`if [ -z "$TOOL_PATH" ]; then TOOL_PATH=$(find_first_executable ` + strings.Join(candidates, " ") + ` 2>/dev/null || true); fi`,
		`VERSION_LINE=""`,
		`if [ -n "$TOOL_PATH" ]; then VERSION_LINE=$("$TOOL_PATH" --version 2>/dev/null | head -n1 || true); fi`,
	}, "\n")
}

func buildPostgresToolLookupSnippet() string {
	candidates := []string{
		`"/usr/local/pgsql/bin/$TOOL_NAME"`,
		`"/usr/pgsql/bin/$TOOL_NAME"`,
		`"/usr/lib/postgresql/$PG_MAJOR/bin/$TOOL_NAME"`,
		`"/usr/pgsql-$PG_MAJOR/bin/$TOOL_NAME"`,
		`"/usr/local/pgsql-$PG_MAJOR/bin/$TOOL_NAME"`,
		`"/usr/local/postgresql/$PG_MAJOR/bin/$TOOL_NAME"`,
		`"/opt/pgsql-$PG_MAJOR/bin/$TOOL_NAME"`,
		`"/opt/postgresql/$PG_MAJOR/bin/$TOOL_NAME"`,
		`"/usr/local/bin/$TOOL_NAME"`,
		`"/usr/bin/$TOOL_NAME"`,
		`"/bin/$TOOL_NAME"`,
	}
	return strings.Join([]string{
		`find_first_executable() { for candidate in "$@"; do if [ -n "$candidate" ] && [ -x "$candidate" ]; then printf "%s\n" "$candidate"; return 0; fi; done; return 1; }`,
		`export PATH="${PROCESS_BIN_DIR:+$PROCESS_BIN_DIR:}/usr/local/pgsql/bin:/usr/pgsql/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$PATH"`,
		`TOOL_PATH=$(command -v "$TOOL_NAME" 2>/dev/null || true)`,
		`if [ -z "$TOOL_PATH" ] && [ -n "$PROCESS_BIN_DIR" ]; then TOOL_PATH=$(find_first_executable "$PROCESS_BIN_DIR/$TOOL_NAME" 2>/dev/null || true); fi`,
		`if [ -z "$TOOL_PATH" ]; then TOOL_PATH=$(find_first_executable ` + strings.Join(candidates, " ") + ` 2>/dev/null || true); fi`,
		`VERSION_LINE=""`,
		`if [ -n "$TOOL_PATH" ]; then VERSION_LINE=$("$TOOL_PATH" --version 2>/dev/null | head -n1 || true); fi`,
	}, "\n")
}

// buildKingbaseToolLookupSnippet 金仓客户端探测片段。
// 与 PG 版的结构完全一致，只是候选目录换成金仓装机目录：
// 金仓默认把 sys_dump / sys_restore / ksql 放在 <安装目录>/Server/bin 下，
// 不在 PATH 里，也没有 /usr/pgsql-<major>/bin 这种按 major 分目录的布局。
func buildKingbaseToolLookupSnippet() string {
	candidates := []string{
		`"/opt/Kingbase/ES/V8/Server/bin/$TOOL_NAME"`,
		`"/opt/Kingbase/ES/V8/bin/$TOOL_NAME"`,
		`"/opt/Kingbase/Server/bin/$TOOL_NAME"`,
		`"/home/kingbase/Kingbase/ES/V8/Server/bin/$TOOL_NAME"`,
		`"/usr/local/Kingbase/ES/V8/Server/bin/$TOOL_NAME"`,
		`"/usr/local/bin/$TOOL_NAME"`,
		`"/usr/bin/$TOOL_NAME"`,
		`"/bin/$TOOL_NAME"`,
	}
	return strings.Join([]string{
		`find_first_executable() { for candidate in "$@"; do if [ -n "$candidate" ] && [ -x "$candidate" ]; then printf "%s\n" "$candidate"; return 0; fi; done; return 1; }`,
		`export PATH="${PROCESS_BIN_DIR:+$PROCESS_BIN_DIR:}/opt/Kingbase/ES/V8/Server/bin:/opt/Kingbase/ES/V8/bin:/opt/Kingbase/Server/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$PATH"`,
		`TOOL_PATH=$(command -v "$TOOL_NAME" 2>/dev/null || true)`,
		`if [ -z "$TOOL_PATH" ] && [ -n "$PROCESS_BIN_DIR" ]; then TOOL_PATH=$(find_first_executable "$PROCESS_BIN_DIR/$TOOL_NAME" 2>/dev/null || true); fi`,
		`if [ -z "$TOOL_PATH" ]; then TOOL_PATH=$(find_first_executable ` + strings.Join(candidates, " ") + ` 2>/dev/null || true); fi`,
		`VERSION_LINE=""`,
		`if [ -n "$TOOL_PATH" ]; then VERSION_LINE=$("$TOOL_PATH" --version 2>/dev/null | head -n1 || true); fi`,
	}, "\n")
}

func resolveLocalTool(family string, toolName string, versions []string) (*localToolSpec, error) {	name := strings.TrimSpace(toolName)
	if name == "" {
		return nil, fmt.Errorf("工具名不能为空")
	}
	fam := strings.TrimSpace(family)
	bases := backupToolBaseDirs()
	for _, base := range bases {
		for _, version := range versions {
			for _, candidate := range toolCandidatePaths(base, fam, version, name) {
				if isExecutableFile(candidate) {
					return &localToolSpec{
						Path:    candidate,
						Name:    name,
						Version: version,
						Source:  fmt.Sprintf("local:%s/%s", fam, version),
						LibDirs: detectToolLibDirs(candidate),
					}, nil
				}
			}
		}
		for _, candidate := range toolCandidatePaths(base, fam, "", name) {
			if isExecutableFile(candidate) {
				return &localToolSpec{
					Path:    candidate,
					Name:    name,
					Version: "",
					Source:  fmt.Sprintf("local:%s/default", fam),
					LibDirs: detectToolLibDirs(candidate),
				}, nil
			}
		}
	}
	for _, version := range versions {
		for _, candidate := range systemToolCandidatePaths(fam, version, name) {
			if isExecutableFile(candidate) {
				return &localToolSpec{
					Path:    candidate,
					Name:    name,
					Version: version,
					Source:  fmt.Sprintf("system:%s/%s", fam, version),
					LibDirs: detectToolLibDirs(candidate),
				}, nil
			}
		}
	}
	if path, err := exec.LookPath(name); err == nil && strings.TrimSpace(path) != "" {
		return &localToolSpec{
			Path:    path,
			Name:    name,
			Version: "",
			Source:  "path",
			LibDirs: nil,
		}, nil
	}
	return nil, fmt.Errorf("未找到 %s 工具，请将其随程序发布到 tools/%s/<version>/ 目录或加入 PATH", name, fam)
}

func systemToolCandidatePaths(family string, version string, toolName string) []string {
	fam := strings.TrimSpace(strings.ToLower(family))
	ver := strings.TrimSpace(version)
	name := strings.TrimSpace(toolName)
	if fam == "" || ver == "" || name == "" {
		return nil
	}
	out := make([]string, 0, 12)
	add := func(p string) {
		v := strings.TrimSpace(p)
		if v == "" {
			return
		}
		for _, existing := range out {
			if existing == v {
				return
			}
		}
		out = append(out, filepath.Clean(v))
	}
	switch fam {
	case "postgres":
		add(filepath.Join("/usr/bin", name))
		add(filepath.Join("/usr/local/bin", name))
		add(filepath.Join("/usr/local/pgsql", "bin", name))
		add(filepath.Join("/usr/pgsql", "bin", name))
		add(filepath.Join("/bin", name))
		add(filepath.Join("/snap/bin", name))
		add(filepath.Join("/usr/lib/postgresql", ver, "bin", name))
		add(filepath.Join("/usr/pgsql-"+ver, "bin", name))
		add(filepath.Join("/usr/local/pgsql-"+ver, "bin", name))
		add(filepath.Join("/usr/local/postgresql", ver, "bin", name))
		add(filepath.Join("/opt/pgsql-"+ver, "bin", name))
		add(filepath.Join("/opt/postgresql", ver, "bin", name))
	case "mysql":
		add(filepath.Join("/usr/bin", name))
		add(filepath.Join("/usr/local/bin", name))
		add(filepath.Join("/bin", name))
		mysqlBases := []string{
			"/usr/local/mysql",
			"/usr/local/mariadb",
			"/opt/mysql",
			"/opt/mariadb",
			"/app/mysql",
			"/app/mariadb",
			"/data/mysql",
			"/data/mariadb",
			"/home/mysql",
		}
		for _, base := range mysqlBases {
			add(filepath.Join(base, "bin", name))
			add(filepath.Join(base, ver, "bin", name))
			add(filepath.Join(base, "mysql-"+ver, "bin", name))
			add(filepath.Join(base, "mariadb-"+ver, "bin", name))
			add(filepath.Join(base, ver, "client", "bin", name))
			add(filepath.Join(base, "client", "bin", name))
		}
		add(filepath.Join("/usr/local/opt/mysql", "bin", name))
		add(filepath.Join("/usr/local/opt/mariadb", "bin", name))
		add(filepath.Join("/usr/local/opt/mysql-client", "bin", name))
	}
	return out
}

func probeCommandVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out))
	}
	return strings.TrimSpace(string(out))
}

func parseDBVersionInfo(raw string) dbVersionInfo {
	text := strings.TrimSpace(raw)
	info := dbVersionInfo{
		Raw:       text,
		IsMariaDB: strings.Contains(strings.ToLower(text), "mariadb"),
	}
	prefix := versionPrefixRe.FindString(text)
	if prefix == "" {
		prefix = versionAnyRe.FindString(text)
	}
	info.Prefix = prefix
	if prefix == "" {
		return info
	}
	parts := strings.Split(prefix, ".")
	if len(parts) > 0 {
		info.Major, _ = strconv.Atoi(parts[0])
	}
	if len(parts) > 1 {
		info.Minor, _ = strconv.Atoi(parts[1])
	}
	return info
}

func requiresPostgresExactMajor(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "pg_dump", "pg_restore", "pgbackrest":
		return true
	default:
		return false
	}
}

func validatePostgresClientCompatibility(toolName string, spec *localToolSpec, serverVersion string) error {
	if spec == nil {
		return fmt.Errorf("%s 工具信息为空", toolName)
	}
	serverInfo := parseDBVersionInfo(serverVersion)
	if serverInfo.Prefix == "" {
		return fmt.Errorf("无法解析 PostgreSQL 服务端版本: %s", strings.TrimSpace(serverVersion))
	}
	clientVersion := strings.TrimSpace(spec.Version)
	if clientVersion == "" {
		clientVersion = probeCommandVersion(spec.Path)
	}
	clientInfo := parseDBVersionInfo(clientVersion)
	if clientInfo.Prefix == "" {
		return fmt.Errorf("无法识别 %s 客户端版本，工具=%s 原始版本=%s", toolName, spec.Path, strings.TrimSpace(clientVersion))
	}
	if clientInfo.Major != serverInfo.Major {
		return fmt.Errorf("%s 客户端版本不兼容：服务端=%s，客户端=%s，工具=%s。请在备份节点安装 PostgreSQL %d 客户端，或发布到 tools/postgres/%d/%s", toolName, strings.TrimSpace(serverVersion), strings.TrimSpace(clientVersion), spec.Path, serverInfo.Major, serverInfo.Major, toolName)
	}
	return nil
}

func backupToolBaseDirs() []string {
	items := make([]string, 0, 8)
	add := func(path string) {
		v := strings.TrimSpace(path)
		if v == "" {
			return
		}
		cleaned := filepath.Clean(v)
		for _, existing := range items {
			if existing == cleaned {
				return
			}
		}
		items = append(items, cleaned)
	}
	add(os.Getenv("GOINSIGHT_BACKUP_TOOL_HOME"))
	add(defaultBackupNodeToolsDir(""))
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		add(filepath.Join(exeDir, "tools"))
		add(filepath.Join(exeDir, "backup-tools"))
		add(exeDir)
	}
	if wd, err := os.Getwd(); err == nil {
		add(filepath.Join(wd, "tools"))
		add(filepath.Join(wd, "backup-tools"))
		add(wd)
	}
	return items
}

func toolCandidatePaths(base string, family string, version string, toolName string) []string {
	if strings.TrimSpace(base) == "" {
		return nil
	}
	if strings.TrimSpace(version) == "" {
		return []string{
			filepath.Join(base, family, toolName),
			filepath.Join(base, family, "bin", toolName),
			filepath.Join(base, toolName),
		}
	}
	return []string{
		filepath.Join(base, family, version, toolName),
		filepath.Join(base, family, version, "bin", toolName),
		filepath.Join(base, family, version, "client", toolName),
	}
}

func detectToolLibDirs(toolPath string) []string {
	path := strings.TrimSpace(toolPath)
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	candidates := []string{
		filepath.Join(dir, "lib"),
		filepath.Join(filepath.Dir(dir), "lib"),
		filepath.Join(filepath.Dir(filepath.Dir(dir)), "lib"),
	}
	out := []string{}
	seen := map[string]struct{}{}
	for _, item := range candidates {
		info, err := os.Stat(item)
		if err != nil || !info.IsDir() {
			continue
		}
		cleaned := filepath.Clean(item)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

func mysqlClientVersionCandidates(serverVersion string) []string {
	candidates := []string{}
	prefix := versionPrefixRe.FindString(strings.TrimSpace(serverVersion))
	if prefix != "" {
		parts := strings.Split(prefix, ".")
		if len(parts) >= 2 {
			candidates = append(candidates, parts[0]+"."+parts[1])
		} else {
			candidates = append(candidates, parts[0])
		}
	}
	info := parseDBVersionInfo(serverVersion)
	switch {
	case info.Major == 8 && info.Minor == 4:
		candidates = append(candidates, "8.4", "8.0", "5.7")
	case info.Major >= 8:
		candidates = append(candidates, "8.0", "8.4", "5.7")
	default:
		candidates = append(candidates, "5.7", "8.0", "8.4")
	}
	return uniqueStrings(candidates)
}

func postgresClientVersionCandidates(serverVersion string) []string {
	supported := []string{"17", "16", "15", "14", "13", "12", "11", "10", "9.6"}
	candidates := []string{}
	prefix := versionPrefixRe.FindString(strings.TrimSpace(serverVersion))
	if strings.HasPrefix(prefix, "9.6") {
		candidates = append(candidates, "9.6")
	} else if prefix != "" {
		parts := strings.Split(prefix, ".")
		if len(parts) > 0 && parts[0] != "" {
			candidates = append(candidates, parts[0])
		}
	}
	candidates = append(candidates, supported...)
	return uniqueStrings(candidates)
}

func postgresExactClientVersion(serverVersion string) string {
	prefix := versionPrefixRe.FindString(strings.TrimSpace(serverVersion))
	if strings.HasPrefix(prefix, "9.6") {
		return "9.6"
	}
	if prefix == "" {
		return ""
	}
	parts := strings.Split(prefix, ".")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return ""
	}
	return parts[0]
}

func passwordlessSudoHint(user string) string {
	u := strings.TrimSpace(user)
	if u == "" {
		u = "<SSH_USER>"
	}
	return fmt.Sprintf("SSH 用户 %s 缺少 passwordless sudo 权限，请先在数据库服务器以 root 身份执行：\n"+
		"  echo '%s ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/99-goinsight-backup-agent && chmod 440 /etc/sudoers.d/99-goinsight-backup-agent\n"+
		"如需最小权限，也可仅放行安装目录写入、systemctl、数据库备份工具及 sudo -u <DB_OS_USER> 命令。", u, u)
}

func requirePasswordlessSudo() error {
	out, err := exec.Command("sudo", "-n", "true").CombinedOutput()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if strings.Contains(msg, "a password is required") || strings.Contains(strings.ToLower(msg), "sudo: ") {
		return fmt.Errorf("%s. %s", strings.TrimSpace(msg), passwordlessSudoHint(os.Getenv("USER")))
	}
	return fmt.Errorf("passwordless sudo 检查失败：%w: %s", err, msg)
}

func detectDatabaseOSUser(dbType string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(dbType))
	var commPatterns []string
	var fallback string
	switch normalized {
	case "MYSQL", "TIDB", "MARIADB", "MYS":
		commPatterns = []string{"mysqld", "mariadbd", "tidb-server", "tikv-server", "pd-server"}
		fallback = "mysql"
	case "DORIS":
		commPatterns = []string{"doris_fe", "doris_be", "doris"}
		fallback = "doris"
	case "POSTGRESQL", "PGSQL", "PG":
		commPatterns = []string{"postmaster", "postgres"}
		fallback = "postgres"
	case "KINGBASE", "KINGBASEES", "KINGBASE ES", "KINGBASE-ES", "KINGBASEV8":
		// 金仓服务端进程名是 kingbase（sys_ctl 拉起的单进程模型），
		// 部分版本仍以 postgres 命名，二者都匹配；OS 用户默认 kingbase。
		commPatterns = []string{"kingbase", "postmaster", "postgres"}
		fallback = "kingbase"
	default:
		return "", fmt.Errorf("未知的数据库类型 %s，无法自动探测数据库安装部署 OS 用户", dbType)
	}
	commList := strings.Join(commPatterns, "|")
	script := fmt.Sprintf(
		`ps -eo ruser=,user=,comm= 2>/dev/null | awk -v cl="%s" 'BEGIN{split(cl,a,"|"); for(i in a) pat[a[i]]=1; seen_r=""; seen_u=""}
{
  r=$1; u=$2; c=$3; if(!pat[c]) next
  if(r!="" && r!="root" && index(seen_r,"|"r"|")==0){ print r; seen_r=seen_r r "|" }
  if(u!="" && u!="root" && u!=r && index(seen_u,"|"u"|")==0){ print u; seen_u=seen_u u "|" }
}'`,
		commList,
	)
	out, err := exec.Command("sh", "-lc", script).CombinedOutput()
	rawList := strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n")
	candidates := make([]string, 0, len(rawList))
	seen := map[string]struct{}{}
	for _, line := range rawList {
		v := strings.TrimSpace(line)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		if strings.ContainsAny(v, " \t\r\n+/\\") {
			continue
		}
		seen[v] = struct{}{}
		candidates = append(candidates, v)
	}
	if fallback != "" {
		if _, ok := seen[fallback]; !ok {
			candidates = append(candidates, fallback)
		}
	}
	for _, u := range candidates {
		if _, idErr := exec.Command("id", "-u", u).Output(); idErr == nil {
			return u, nil
		}
	}
	n := strings.ToUpper(strings.TrimSpace(normalized))
	switch n {
	case "MYSQL", "MARIADB", "TIDB", "MYS", "DORIS":
		fb := strings.TrimSpace(currentOSUser())
		if fb != "" {
			return fb, nil
		}
	}
	return "", fmt.Errorf("未能在本机探测到 %s 数据库进程 owner（ps 输出=%q，错误=%v）；请确认数据库已启动，或手动在 /etc/passwd 中创建 %s 用户后再试", dbType, strings.TrimSpace(string(out)), err, fallback)
}

func currentOSUser() string {
	if u := strings.TrimSpace(os.Getenv("USER")); u != "" {
		return u
	}
	if out, err := exec.Command("id", "-un").Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			return v
		}
	}
	return ""
}

func shellProfileInitSnippet() string {
	return strings.Join([]string{
		`if [ -f /etc/profile ]; then . /etc/profile >/dev/null 2>&1 || true; fi`,
		`if [ -n "${HOME:-}" ] && [ -f "${HOME:-}/.profile" ]; then . "${HOME:-}/.profile" >/dev/null 2>&1 || true; fi`,
		`if [ -n "${HOME:-}" ] && [ -f "${HOME:-}/.bash_profile" ]; then . "${HOME:-}/.bash_profile" >/dev/null 2>&1 || true; fi`,
		`if [ -n "${HOME:-}" ] && [ -f "${HOME:-}/.bash_login" ]; then . "${HOME:-}/.bash_login" >/dev/null 2>&1 || true; fi`,
		`if [ -n "${HOME:-}" ] && [ -f "${HOME:-}/.zprofile" ]; then . "${HOME:-}/.zprofile" >/dev/null 2>&1 || true; fi`,
		`hash -r 2>/dev/null || true`,
	}, "; ")
}

func buildSudoCommandPrefix(dbOSUser string, env map[string]string, workDir string, binPath string, args []string) (string, []string, map[string]string, error) {
	dbUser := strings.TrimSpace(dbOSUser)
	if dbUser == "" {
		return "", nil, nil, fmt.Errorf("数据库 OS 用户不能为空")
	}
	if dbUser == currentOSUser() || strings.EqualFold(dbUser, strings.TrimSpace(currentOSUser())) {
		return binPath, append([]string(nil), args...), env, nil
	}
	if err := requirePasswordlessSudo(); err != nil {
		return "", nil, nil, err
	}
	keepKeys := []string{
		"MYSQL_PWD", "PGPASSWORD", "PGSSLMODE", "PGUSER", "PGDATABASE", "PGHOST", "PGPORT",
		"SSHPASS",
		"LD_LIBRARY_PATH", "PATH", "LANG",
		"GOINSIGHT_BACKUP_TOOL_HOME",
	}
	envCopy := map[string]string{}
	for k, v := range env {
		envCopy[k] = v
	}
	if strings.TrimSpace(binPath) != "" && filepath.IsAbs(binPath) {
		binDir := filepath.Clean(filepath.Dir(binPath))
		if binDir != "" && binDir != "." {
			existingPath := strings.TrimSpace(envCopy["PATH"])
			if existingPath == "" {
				existingPath = strings.TrimSpace(os.Getenv("PATH"))
			}
			seen := map[string]struct{}{}
			if existingPath != "" {
				for _, item := range strings.Split(existingPath, ":") {
					v := strings.TrimSpace(item)
					if v == "" {
						continue
					}
					seen[v] = struct{}{}
				}
			}
			if _, ok := seen[binDir]; !ok {
				if existingPath == "" {
					envCopy["PATH"] = binDir
				} else {
					envCopy["PATH"] = binDir + ":" + existingPath
				}
			}
		}
	}
	keepList := make([]string, 0, len(keepKeys))
	seen := map[string]struct{}{}
	for _, k := range keepKeys {
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		keepList = append(keepList, k)
	}
	targetHome := resolveUserHomeDir(dbUser)
	sudoArgs := buildSudoWrappedArgs(dbUser, targetHome, keepList, envCopy, workDir, binPath, args)
	return "sudo", sudoArgs, envCopy, nil
}

func resolveUserHomeDir(user string) string {
	targetUser := strings.TrimSpace(user)
	if targetUser == "" {
		return ""
	}
	cmd := "getent passwd " + shellQuote(targetUser) + " | cut -d: -f6 || grep '^" + strings.ReplaceAll(targetUser, "'", "'\\''") + ":' /etc/passwd | cut -d: -f6"
	out, err := exec.Command("sh", "-lc", cmd).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func buildSudoWrappedArgs(dbUser string, targetHome string, keepList []string, envCopy map[string]string, workDir string, binPath string, args []string) []string {
	shellInit := shellProfileInitSnippet()
	sudoArgs := []string{"-n", "-H", "-E", "-u", dbUser}
	if len(keepList) > 0 {
		sudoArgs = append(sudoArgs, "--preserve-env="+strings.Join(keepList, ","))
	}
	sudoArgs = append(sudoArgs, "--")
	innerParts := []string{}
	targetWorkDir := strings.TrimSpace(workDir)
	if targetWorkDir == "" {
		targetWorkDir = "/"
	}
	if targetWorkDir != "" {
		innerParts = append(innerParts, "cd "+shellQuote(targetWorkDir))
	}
	innerParts = append(innerParts, "umask 022")
	if strings.TrimSpace(binPath) != "" && !filepath.IsAbs(strings.TrimSpace(binPath)) {
		innerParts = append(innerParts, `eval "$SHELL_INIT"`)
	}
	envAssigns := []string{}
	for _, k := range keepList {
		if v, ok := envCopy[k]; ok && v != "" {
			envAssigns = append(envAssigns, k+"="+shellQuote(v))
		}
	}
	if len(envAssigns) > 0 {
		innerParts = append(innerParts, "export "+strings.Join(envAssigns, " "))
	}
	quotedArgs := make([]string, 0, len(args)+1)
	quotedArgs = append(quotedArgs, shellQuote(binPath))
	for _, a := range args {
		quotedArgs = append(quotedArgs, shellQuote(a))
	}
	innerParts = append(innerParts, strings.Join(quotedArgs, " "))
	shellCommand := strings.Join(innerParts, " && ")
	envArgs := []string{}
	if strings.TrimSpace(targetHome) != "" {
		envArgs = append(envArgs, "HOME="+strings.TrimSpace(targetHome))
	}
	envArgs = append(envArgs, "SHELL_INIT="+shellInit, "CMD="+shellCommand)
	sudoArgs = append(sudoArgs, "env")
	sudoArgs = append(sudoArgs, envArgs...)
	sudoArgs = append(sudoArgs, "/bin/sh", "-lc", `eval "$CMD"`)
	return sudoArgs
}

func runLocalCommandAsDBUser(ctx context.Context, dbType string, workDir string, binPath string, args []string, env map[string]string, logWriter io.Writer) error {
	return runLocalCommandAsDBUserWithStdoutOverride(ctx, dbType, workDir, binPath, args, env, logWriter, nil)
}

func runLocalCommandAsDBUserWithStdoutOverride(ctx context.Context, dbType string, workDir string, binPath string, args []string, env map[string]string, logWriter io.Writer, stdoutOverride io.Writer) error {
	dbOSUser, err := detectDatabaseOSUser(dbType)
	if err != nil {
		return err
	}
	return runLocalCommandAsUserWithStdoutOverride(ctx, dbOSUser, workDir, binPath, args, env, logWriter, stdoutOverride)
}

func runLocalCommandAsUser(ctx context.Context, osUser string, workDir string, binPath string, args []string, env map[string]string, logWriter io.Writer) error {
	return runLocalCommandAsUserWithStdoutOverride(ctx, osUser, workDir, binPath, args, env, logWriter, nil)
}

func runLocalCommandAsUserWithStdoutOverride(ctx context.Context, osUser string, workDir string, binPath string, args []string, env map[string]string, logWriter io.Writer, stdoutOverride io.Writer) error {
	dbOSUser := strings.TrimSpace(osUser)
	binPath = strings.TrimSpace(binPath)
	if dbOSUser == "" {
		if binPath != "" && !filepath.IsAbs(binPath) {
			return runShellCommandWithStdoutOverride(ctx, workDir, binPath, args, env, logWriter, stdoutOverride)
		}
		return runLocalCommandWithStdoutOverride(ctx, workDir, binPath, args, env, logWriter, stdoutOverride)
	}
	realBin, realArgs, realEnv, err := buildSudoCommandPrefix(dbOSUser, env, workDir, binPath, args)
	if err != nil {
		return err
	}
	toolName := filepath.Base(binPath)
	killFallback := func(sig string) {
		if logWriter != nil {
			_, _ = fmt.Fprintf(logWriter, "[stop] 尝试 %s 终止 %s 用户下的 %s 进程树 (sudo/sh/%s 三层一并清理)...\n", strings.TrimPrefix(sig, "-"), dbOSUser, toolName, toolName)
		}
		dbosUser := dbOSUser
		tn := toolName
		for _, script := range []string{
			fmt.Sprintf(`if command -v pkill >/dev/null 2>&1; then sudo -n -E -u %s -- pkill -%s -x %s 2>/dev/null || true; fi`, dbosUser, sig, tn),
			fmt.Sprintf(`if command -v pkill >/dev/null 2>&1; then sudo -n -E -u %s -- pkill -%s -x sh 2>/dev/null || true; fi`, dbosUser, sig),
			fmt.Sprintf(`if command -v pkill >/dev/null 2>&1; then pkill -%s -f "^sudo[[:space:]]+-n[[:space:]]+-E[[:space:]]+-u[[:space:]]+%s" 2>/dev/null || true; fi`, sig, dbosUser),
			fmt.Sprintf(`if command -v pgrep >/dev/null 2>&1 && command -v kill >/dev/null 2>&1; then PIDS=$(sudo -n -E -u %s -- pgrep -x %s 2>/dev/null || true); if [ -n "$PIDS" ]; then echo "$PIDS" | xargs -r sudo -n -E kill -%s 2>/dev/null || true; fi; fi`, dbosUser, tn, sig),
			fmt.Sprintf(`PIDS=$(ps -eo user=,pid=,ppid=,args= 2>/dev/null | awk -v u=%s -v t="%s" '$1==u && ($4==t || $4=="sh" || index($0,t)>0) && $2!="" {print $2}' | sort -u | tr '\n' ' '); if [ -n "$PIDS" ]; then echo "$PIDS" | xargs -r sudo -n -E kill -%s 2>/dev/null || true; fi`, dbosUser, tn, sig),
		} {
			for i := 0; i < 2; i++ {
				_, _ = exec.Command("sh", "-lc", script).CombinedOutput()
			}
		}
	}
	// 兜底强杀看门狗（仅两种触发条件）：
	//  1) 上层取消（用户点“停止”/ctx 超时）→ 命令未在 graceStopWait 内退出时清理 sudo 三层进程树；
	//  2) 无任何取消但已连续执行超过 backupProcessMaxRuntime 的极端情况 → 最后防线。
	// 红线：禁止用固定的小阈值当作“正常执行上限”。MySQL 物理备份直传对象存储时受出口带宽约束，
	// 数十 GB 数据耗时可达数小时；历史实现固定 60 分钟强杀，导致正常大备份被 SIGTERM 打断，
	// 执行记录以 `exit status 143` 失败（run bk-1rvsr6htnt6f/399）。
	ctxWithKill, cancelKill := context.WithCancel(ctx)
	defer cancelKill()
	finished := make(chan struct{})
	go func() {
		select {
		case <-finished:
			return
		case <-ctxWithKill.Done():
			// 上层要求停止：给出优雅退出窗口，超时才做兜底清理
			select {
			case <-finished:
				return
			case <-time.After(graceStopWait):
			}
			if logWriter != nil {
				_, _ = fmt.Fprintf(logWriter, "[stop] 已请求终止，%s 用户下的 %s 在 %s 内未退出，执行兜底进程树清理。\n", dbOSUser, toolName, graceStopWait)
			}
		case <-time.After(backupProcessMaxRuntimeFrom(ctx)):
			limit := backupProcessMaxRuntimeFrom(ctx)
			if logWriter != nil {
				_, _ = fmt.Fprintf(logWriter, "[stop] %s 用户下的 %s 已连续执行超过 %s，触发兜底上限保护（可在任务「高级选项」的「单次执行最长运行时间」中调整，或用环境变量 GOINSIGHT_BACKUP_PROCESS_MAX_RUNTIME 覆盖）。\n", dbOSUser, toolName, limit)
			}
		}
		killFallback("TERM")
		select {
		case <-time.After(2 * time.Second):
		case <-time.After(5 * time.Second):
		}
		killFallback("KILL")
		select {
		case <-time.After(4 * time.Second):
		case <-time.After(6 * time.Second):
		}
		killFallback("KILL")
	}()
	runErr := runLocalCommandWithStdoutOverride(ctxWithKill, "", realBin, realArgs, realEnv, logWriter, stdoutOverride)
	close(finished)
	return runErr
}

// graceStopWait 上层取消后，等待命令自行退出的宽限期，超过则兜底清理进程树。
const graceStopWait = 10 * time.Second

// backupProcessHardLimitDefault 数据库命令在无上层取消时的兜底最长执行时间。
// 取值必须远大于正常备份/恢复耗时（受出口带宽限制，数十 GB 物理备份可达数小时）。
const backupProcessHardLimitDefault = 12 * time.Hour

// backupProcessMaxRuntime 返回兜底最长执行时间，可用环境变量覆盖（Go duration 语法，如 "24h"）。
func backupProcessMaxRuntime() time.Duration {
	raw := strings.TrimSpace(os.Getenv("GOINSIGHT_BACKUP_PROCESS_MAX_RUNTIME"))
	if raw == "" {
		return backupProcessHardLimitDefault
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return backupProcessHardLimitDefault
	}
	return parsed
}

// backupProcessMaxRuntimeKey 通过 context 传递任务级兜底上限（见 withBackupProcessMaxRuntime）。
type backupProcessMaxRuntimeKey struct{}

// withBackupProcessMaxRuntime 把任务级「单次执行最长运行时间」注入 ctx。
// hours <= 0 表示未配置，不注入，调用方将按 环境变量 → 平台默认值 逐级回落。
// 备份任务在「高级选项」中配置后，由执行入口（ExecuteBackupJob / 恢复入口）统一注入，
// 经 ctx 一路透传到 runLocalCommandAsUserWithStdoutOverride 的兜底看门狗。
func withBackupProcessMaxRuntime(ctx context.Context, hours int) context.Context {
	if ctx == nil || hours <= 0 {
		return ctx
	}
	return context.WithValue(ctx, backupProcessMaxRuntimeKey{}, time.Duration(hours)*time.Hour)
}

// backupProcessMaxRuntimeFrom 返回本次数据库命令的兜底最长执行时间，优先级：
//  1. 任务级配置（ctx 注入，来自任务「高级选项」的「单次执行最长运行时间」）；
//  2. 环境变量 GOINSIGHT_BACKUP_PROCESS_MAX_RUNTIME（Go duration 语法，如 "24h"）；
//  3. 平台默认 backupProcessHardLimitDefault。
func backupProcessMaxRuntimeFrom(ctx context.Context) time.Duration {
	if ctx != nil {
		if limit, ok := ctx.Value(backupProcessMaxRuntimeKey{}).(time.Duration); ok && limit > 0 {
			return limit
		}
	}
	return backupProcessMaxRuntime()
}

func runLocalCommandCaptureAsDBUser(ctx context.Context, dbType string, workDir string, binPath string, args []string, env map[string]string, logWriter io.Writer) (string, error) {
	dbOSUser, err := detectDatabaseOSUser(dbType)
	if err != nil {
		return "", err
	}
	return runLocalCommandCaptureAsUser(ctx, dbOSUser, workDir, binPath, args, env, logWriter)
}

func runLocalCommandCaptureAsUser(ctx context.Context, osUser string, workDir string, binPath string, args []string, env map[string]string, logWriter io.Writer) (string, error) {
	var buf bytes.Buffer
	var writer io.Writer = &buf
	if logWriter != nil {
		writer = io.MultiWriter(logWriter, &buf)
	}
	err := runLocalCommandAsUserWithStdoutOverride(ctx, osUser, workDir, binPath, args, env, writer, writer)
	return strings.TrimSpace(buf.String()), err
}

func runShellCommandWithStdoutOverride(ctx context.Context, workDir string, binPath string, args []string, env map[string]string, logWriter io.Writer, stdoutOverride io.Writer) error {
	innerParts := []string{}
	targetWorkDir := strings.TrimSpace(workDir)
	if targetWorkDir != "" {
		innerParts = append(innerParts, "cd "+shellQuote(targetWorkDir))
	}
	innerParts = append(innerParts, shellProfileInitSnippet())
	quotedArgs := make([]string, 0, len(args)+1)
	quotedArgs = append(quotedArgs, shellQuote(binPath))
	for _, a := range args {
		quotedArgs = append(quotedArgs, shellQuote(a))
	}
	innerParts = append(innerParts, strings.Join(quotedArgs, " "))
	return runLocalCommandWithStdoutOverride(ctx, "", "/bin/sh", []string{"-lc", strings.Join(innerParts, " && ")}, env, logWriter, stdoutOverride)
}

func runLocalCommandAsRoot(ctx context.Context, workDir string, binPath string, args []string, env map[string]string, logWriter io.Writer) error {
	return runLocalCommandAsRootWithStdoutOverride(ctx, workDir, binPath, args, env, logWriter, nil)
}

func runLocalCommandAsRootWithStdoutOverride(ctx context.Context, workDir string, binPath string, args []string, env map[string]string, logWriter io.Writer, stdoutOverride io.Writer) error {
	if err := requirePasswordlessSudo(); err != nil {
		return err
	}
	sudoArgs := []string{"-n", "-H", "--"}
	if strings.TrimSpace(binPath) == "" {
		return fmt.Errorf("执行命令不能为空")
	}
	sudoArgs = append(sudoArgs, binPath)
	sudoArgs = append(sudoArgs, args...)
	return runLocalCommandWithStdoutOverride(ctx, workDir, "sudo", sudoArgs, env, logWriter, stdoutOverride)
}

func runLocalCommandCaptureAsRoot(ctx context.Context, workDir string, binPath string, args []string, env map[string]string, logWriter io.Writer) (string, error) {
	var buf bytes.Buffer
	var writer io.Writer = &buf
	if logWriter != nil {
		writer = io.MultiWriter(logWriter, &buf)
	}
	err := runLocalCommandAsRootWithStdoutOverride(ctx, workDir, binPath, args, env, writer, writer)
	return strings.TrimSpace(buf.String()), err
}
