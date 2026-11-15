package services

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type logicalToolResolution struct {
	Spec            *localToolSpec
	ToolUser        string
	VersionLine     string
	ToolError       string
	RawOutput       string
	Parsed          map[string]string
	EffectiveOSUser string
}

func resolveLogicalBackupToolLocally(ctx context.Context, dbType string, serverVersion string, cfg logicalBackupDriverConfig) (*logicalToolResolution, error) {
	return resolveLogicalBackupToolLocallyWithFallbackUser(ctx, dbType, serverVersion, cfg, "")
}

func resolveLogicalBackupToolLocallyWithFallbackUser(ctx context.Context, dbType string, serverVersion string, cfg logicalBackupDriverConfig, fallbackOSUser string) (*logicalToolResolution, error) {
	runCtx := ctx
	if runCtx == nil {
		runCtx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(runCtx, 10*time.Second)
	defer cancel()

	effectiveCfg := cfg
	effectiveCfg.OSUser = firstNonEmpty(strings.TrimSpace(cfg.OSUser), strings.TrimSpace(fallbackOSUser))
	script := buildLogicalToolResolveProbeScript(dbType, serverVersion, effectiveCfg.ToolPath)

	var (
		output string
		err    error
	)
	if strings.TrimSpace(effectiveCfg.OSUser) != "" {
		output, err = runLocalCommandCaptureAsUser(runCtx, effectiveCfg.OSUser, "", "/bin/sh", []string{"-lc", script}, nil, nil)
	} else {
		output, err = runLocalCommandCaptureAsDBUser(runCtx, dbType, "", "/bin/sh", []string{"-lc", script}, nil, nil)
	}
	return finalizeLogicalToolResolution(dbType, serverVersion, effectiveCfg, output, err)
}

func resolveLogicalBackupToolRemotely(ctx *backupTaskPrecheckContext, cfg logicalBackupDriverConfig) (*logicalToolResolution, error) {
	if ctx == nil || ctx.Instance == nil {
		return nil, fmt.Errorf("实例信息不存在")
	}
	sshUser, sshPassword, sshPrivateKey, sshPort, ok, reason := resolveSSHCredentialsFromInstance(ctx.Instance)
	if !ok {
		return nil, fmt.Errorf("缺少 SSH 凭据: %s", reason)
	}
	remoteCfg := &backupNodeRemoteConfig{
		Host:          strings.TrimSpace(ctx.Instance.Hostname),
		Port:          sshPort,
		SSHUser:       sshUser,
		SSHPassword:   sshPassword,
		SSHPrivateKey: sshPrivateKey,
	}
	runner, err := newSSHRunner(remoteCfg)
	if err != nil {
		return nil, err
	}
	defer runner.Close()

	effectiveCfg := cfg
	if strings.TrimSpace(effectiveCfg.OSUser) == "" {
		effectiveOSUser, _, _, resolveErr := resolveRemoteExecutionOSUserForPrecheck(ctx, effectiveCfg.OSUser)
		if resolveErr != nil {
			return nil, fmt.Errorf("无法自动识别数据库进程用户，无法在远端统一解析工具: %w", resolveErr)
		}
		effectiveCfg.OSUser = effectiveOSUser
	}

	probeScript := buildLogicalToolResolveProbeScript(string(ctx.Instance.DbType), ctx.ServerVersion, effectiveCfg.ToolPath)
	script := buildRemoteLogicalToolResolveScript(effectiveCfg.OSUser, probeScript)
	output, runErr := runner.Run(script)
	if runErr != nil && !strings.EqualFold(strings.TrimSpace(remoteCfg.SSHUser), "root") {
		output, runErr = runner.Run(wrapRemoteRootScript(remoteCfg, script))
	}
	resolution, resolveErr := finalizeLogicalToolResolution(string(ctx.Instance.DbType), ctx.ServerVersion, effectiveCfg, output, runErr)
	if resolution == nil {
		resolution = &logicalToolResolution{
			Parsed:          parseKeyValueOutput(output),
			RawOutput:       strings.TrimSpace(output),
			EffectiveOSUser: strings.TrimSpace(effectiveCfg.OSUser),
		}
	}
	logLogicalToolInspectionDebug(ctx, remoteCfg, resolution.Parsed, runErr)
	if resolveErr != nil {
		return resolution, resolveErr
	}
	return resolution, nil
}

func resolveLogicalBackupToolForPrecheck(ctx *backupTaskPrecheckContext, cfg logicalBackupDriverConfig) (*logicalToolResolution, error) {
	if ctx == nil || ctx.Instance == nil {
		return nil, fmt.Errorf("实例信息不存在")
	}
	effectiveCfg := cfg
	if strings.TrimSpace(effectiveCfg.OSUser) == "" && ctx.RuntimeInspect != nil {
		effectiveCfg.OSUser = strings.TrimSpace(ctx.RuntimeInspect["PROCESS_USER"])
	}
	if isInstanceHostLocal(strings.TrimSpace(ctx.Instance.Hostname)) {
		return resolveLogicalBackupToolLocallyWithFallbackUser(context.Background(), string(ctx.Instance.DbType), ctx.ServerVersion, effectiveCfg, strings.TrimSpace(effectiveCfg.OSUser))
	}
	return resolveLogicalBackupToolRemotely(ctx, effectiveCfg)
}

func finalizeLogicalToolResolution(dbType string, serverVersion string, cfg logicalBackupDriverConfig, output string, runErr error) (*logicalToolResolution, error) {
	parsed := parseKeyValueOutput(output)
	resolution := &logicalToolResolution{
		ToolUser:        strings.TrimSpace(parsed["TOOL_USER"]),
		VersionLine:     strings.TrimSpace(parsed["VERSION_LINE"]),
		ToolError:       strings.TrimSpace(parsed["TOOL_ERROR"]),
		RawOutput:       strings.TrimSpace(output),
		Parsed:          parsed,
		EffectiveOSUser: strings.TrimSpace(cfg.OSUser),
	}

	toolPath := strings.TrimSpace(parsed["TOOL_PATH"])
	if toolPath != "" {
		resolution.Spec = &localToolSpec{
			Path:    toolPath,
			Name:    filepath.Base(toolPath),
			Version: resolution.VersionLine,
			Source:  logicalToolSource(cfg.ToolPath),
			LibDirs: detectToolLibDirs(toolPath),
		}
		// 版本兼容校验只对 PostgreSQL 有意义：它要求客户端 major == 服务端 major。
		// 金仓版本串是 V009R001C010，没有 PG major 概念，工具与内核同包发布，天然配套，
		// 强行按 PG 规则比对只会把可用环境判成不可用。
		if isPostgresLogicalFamily(dbType) && !isKingbaseDBType(dbType) {
			if err := validatePostgresClientCompatibility(logicalToolName(dbType, logicalToolRoleDump), resolution.Spec, serverVersion); err != nil {
				return resolution, err
			}
		}
		return resolution, nil
	}

	if runErr != nil {
		errMsg := logicalToolErrorMessage(resolution.ToolError, cfg.OSUser, resolution.RawOutput)
		if errMsg == "" {
			errMsg = runErr.Error()
		}
		return resolution, fmt.Errorf("逻辑备份工具解析失败: %s", strings.TrimSpace(errMsg))
	}

	if resolution.RawOutput == "" {
		return resolution, fmt.Errorf("逻辑备份工具解析失败: 远端工具探测未返回任何输出")
	}

	if resolution.ToolError != "" {
		return resolution, fmt.Errorf("逻辑备份工具解析失败: %s", logicalToolErrorMessage(resolution.ToolError, cfg.OSUser, resolution.RawOutput))
	}
	if strings.TrimSpace(cfg.ToolPath) != "" {
		return resolution, fmt.Errorf("用户填写的逻辑备份工具不可用或未解析到可执行文件")
	}
	return resolution, fmt.Errorf("未检测到可用的逻辑备份工具")
}

func logicalToolErrorMessage(toolError string, osUser string, fallback string) string {
	switch strings.TrimSpace(toolError) {
	case "target_tool_user_empty":
		return "未识别到数据库进程用户，无法切换到对应操作系统用户执行工具探测"
	case "target_tool_user_not_found":
		if strings.TrimSpace(osUser) != "" {
			return fmt.Sprintf("目标操作系统用户不存在: %s", strings.TrimSpace(osUser))
		}
		return "目标操作系统用户不存在"
	case "configured_tool_not_executable":
		return "用户填写的逻辑备份工具不可执行"
	case "configured_tool_not_found":
		return "用户填写的逻辑备份工具未解析到可执行文件"
	case "tool_not_found":
		return "未检测到可用的逻辑备份工具"
	case "sudo_missing":
		return "目标机器未安装 sudo，无法切换到数据库操作系统用户执行工具探测"
	case "remote_probe_output_empty":
		return "远端工具探测未返回任何输出"
	default:
		return strings.TrimSpace(firstNonEmpty(toolError, fallback))
	}
}

func logicalToolSource(configuredToolPath string) string {
	if strings.TrimSpace(configuredToolPath) != "" {
		return "user_config:path"
	}
	return "auto_lookup"
}

func buildLogicalToolResolveProbeScript(dbType string, serverVersion string, toolPath string) string {
	normalizedDBType := strings.TrimSpace(dbType)
	normalizedToolPath := strings.TrimSpace(toolPath)

	buildConfiguredToolScript := func(lookupSnippet string, envLines []string) string {
		lines := []string{
			"INPUT_TOOL_PATH=" + shellQuote(normalizedToolPath),
			`TOOL_PATH=""`,
			`VERSION_LINE=""`,
			`TOOL_ERROR=""`,
			`STATUS=0`,
			`if [ "${INPUT_TOOL_PATH#/}" != "$INPUT_TOOL_PATH" ]; then`,
			`  if [ ! -x "$INPUT_TOOL_PATH" ]; then TOOL_ERROR="configured_tool_not_executable"; STATUS=41; else TOOL_PATH="$INPUT_TOOL_PATH"; fi`,
			`elif [ "${INPUT_TOOL_PATH#*/}" != "$INPUT_TOOL_PATH" ]; then`,
			`  if [ ! -x "$INPUT_TOOL_PATH" ]; then TOOL_ERROR="configured_tool_not_executable"; STATUS=41; else TOOL_PATH="$INPUT_TOOL_PATH"; fi`,
			`else`,
		}
		for _, line := range envLines {
			lines = append(lines, "  "+line)
		}
		for _, line := range strings.Split(lookupSnippet, "\n") {
			lines = append(lines, "  "+line)
		}
		lines = append(lines,
			`  if [ -z "$TOOL_PATH" ]; then TOOL_ERROR="configured_tool_not_found"; STATUS=41; fi`,
			`fi`,
			`if [ -n "$TOOL_PATH" ]; then VERSION_LINE=$("$TOOL_PATH" --version 2>/dev/null | head -n1 || true); fi`,
			`TOOL_USER=$(id -un 2>/dev/null || true)`,
			`echo TOOL_USER=${TOOL_USER}`,
			`echo TOOL_PATH=${TOOL_PATH}`,
			`echo VERSION_LINE=${VERSION_LINE}`,
			`echo TOOL_ERROR=$(printf '%s' "$TOOL_ERROR" | tr '\n' ';')`,
			`exit "$STATUS"`,
		)
		return strings.Join(lines, "\n")
	}

	if strings.EqualFold(normalizedDBType, "MySQL") {
		version := strings.TrimSpace(versionPrefixRe.FindString(strings.TrimSpace(serverVersion)))
		major := ""
		if version != "" {
			parts := strings.Split(version, ".")
			if len(parts) > 0 {
				major = strings.TrimSpace(parts[0])
			}
		}
		if normalizedToolPath != "" {
			return strings.Join([]string{
				"set +e",
				shellProfileInitSnippet(),
				buildConfiguredToolScript(buildMySQLToolLookupSnippet(), []string{
					"PROCESS_BIN_DIR=${PROCESS_BIN_DIR:-}",
					"MYSQL_MAJOR=" + shellQuote(major),
					"MYSQL_VERSION=" + shellQuote(version),
					`TOOL_NAME="$INPUT_TOOL_PATH"`,
				}),
			}, "\n")
		}
		return strings.Join([]string{
			"set +e",
			shellProfileInitSnippet(),
			"PROCESS_BIN_DIR=${PROCESS_BIN_DIR:-}",
			"MYSQL_MAJOR=" + shellQuote(major),
			"MYSQL_VERSION=" + shellQuote(version),
			`TOOL_PATH=""`,
			`VERSION_LINE=""`,
			`TOOL_ERROR=""`,
			`STATUS=0`,
			"TOOL_NAME=mariadb-dump",
			buildMySQLToolLookupSnippet(),
			`if [ -z "$TOOL_PATH" ]; then TOOL_NAME=mysqldump; ` + strings.ReplaceAll(buildMySQLToolLookupSnippet(), "\n", "; ") + ` fi`,
			`if [ -z "$TOOL_PATH" ]; then TOOL_ERROR="tool_not_found"; STATUS=41; fi`,
			`TOOL_USER=$(id -un 2>/dev/null || true)`,
			`echo TOOL_USER=${TOOL_USER}`,
			`echo TOOL_PATH=${TOOL_PATH}`,
			`echo VERSION_LINE=${VERSION_LINE}`,
			`echo TOOL_ERROR=$(printf '%s' "$TOOL_ERROR" | tr '\n' ';')`,
			`exit "$STATUS"`,
		}, "\n")
	}

	// PostgreSQL / KingbaseES 共用这一段探测脚本，差异只有工具名与安装目录：
	//   PG      → pg_dump，按 PG_MAJOR 拼 /usr/pgsql-<major>/bin
	//   Kingbase → sys_dump，直接查 /opt/Kingbase/ES/V8/Server/bin 等固定装机目录
	pgMajor := strings.TrimSpace(postgresExactClientVersion(logicalServerVersionForTool(normalizedDBType, serverVersion)))
	dumpToolName := logicalToolName(normalizedDBType, logicalToolRoleDump)
	lookupSnippet := buildPostgresToolLookupSnippet()
	if isKingbaseDBType(normalizedDBType) {
		lookupSnippet = buildKingbaseToolLookupSnippet()
	}
	if normalizedToolPath != "" {
		return strings.Join([]string{
			"set +e",
			shellProfileInitSnippet(),
			buildConfiguredToolScript(lookupSnippet, []string{
				"PROCESS_BIN_DIR=${PROCESS_BIN_DIR:-}",
				"PG_MAJOR=" + shellQuote(pgMajor),
				`TOOL_NAME="$INPUT_TOOL_PATH"`,
			}),
		}, "\n")
	}
	return strings.Join([]string{
		"set +e",
		shellProfileInitSnippet(),
		"PROCESS_BIN_DIR=${PROCESS_BIN_DIR:-}",
		"PG_MAJOR=" + shellQuote(pgMajor),
		`TOOL_PATH=""`,
		`VERSION_LINE=""`,
		`TOOL_ERROR=""`,
		`STATUS=0`,
		"TOOL_NAME=" + shellQuote(dumpToolName),
		lookupSnippet,
		`if [ -z "$TOOL_PATH" ]; then TOOL_ERROR="tool_not_found"; STATUS=41; fi`,
		`TOOL_USER=$(id -un 2>/dev/null || true)`,
		`echo TOOL_USER=${TOOL_USER}`,
		`echo TOOL_PATH=${TOOL_PATH}`,
		`echo VERSION_LINE=${VERSION_LINE}`,
		`echo TOOL_ERROR=$(printf '%s' "$TOOL_ERROR" | tr '\n' ';')`,
		`exit "$STATUS"`,
	}, "\n")
}

func buildRemoteLogicalToolResolveScript(targetUser string, probeScript string) string {
	return strings.Join([]string{
		"set +e",
		"RAW_TARGET_TOOL_USER=" + shellQuote(strings.TrimSpace(targetUser)),
		`TARGET_TOOL_USER="$RAW_TARGET_TOOL_USER"`,
		"CURRENT_USER=$(id -un 2>/dev/null || true)",
		`if [ -z "$TARGET_TOOL_USER" ]; then TARGET_TOOL_USER="$CURRENT_USER"; fi`,
		`TARGET_HOME=$(awk -F: -v u="$TARGET_TOOL_USER" '$1==u{print $6; exit}' /etc/passwd 2>/dev/null || true)`,
		`TARGET_USER_EXISTS=0`,
		`if [ -n "$TARGET_TOOL_USER" ] && id -u "$TARGET_TOOL_USER" >/dev/null 2>&1; then TARGET_USER_EXISTS=1; fi`,
		`TARGET_SCRIPT_FILE=/tmp/goinsight-logical-tool-resolve-$$.sh`,
		`TARGET_OUTPUT_FILE=/tmp/goinsight-logical-tool-resolve-output-$$.log`,
		`cat <<'__GOINSIGHT_LOGICAL_TOOL__' >"$TARGET_SCRIPT_FILE"`,
		probeScript,
		"__GOINSIGHT_LOGICAL_TOOL__",
		`chmod 755 "$TARGET_SCRIPT_FILE"`,
		`rm -f "$TARGET_OUTPUT_FILE"`,
		`touch "$TARGET_OUTPUT_FILE"`,
		`append_output() { printf '%s\n' "$1" >>"$TARGET_OUTPUT_FILE"; }`,
		`append_output "DEBUG_CURRENT_USER=${CURRENT_USER}"`,
		`append_output "DEBUG_RAW_TARGET_TOOL_USER=${RAW_TARGET_TOOL_USER}"`,
		`append_output "DEBUG_TARGET_TOOL_USER=${TARGET_TOOL_USER}"`,
		`append_output "DEBUG_TARGET_USER_EXISTS=${TARGET_USER_EXISTS}"`,
		`append_output "DEBUG_TARGET_HOME=${TARGET_HOME}"`,
		`append_output "DEBUG_REMOTE_SCRIPT=${TARGET_SCRIPT_FILE}"`,
		`STATUS=0`,
		`if [ -z "$TARGET_TOOL_USER" ]; then`,
		`  append_output "TOOL_ERROR=target_tool_user_empty"`,
		`  STATUS=62`,
		`elif [ "$TARGET_USER_EXISTS" != "1" ]; then`,
		`  append_output "TOOL_ERROR=target_tool_user_not_found"`,
		`  STATUS=63`,
		`elif [ "$CURRENT_USER" = "$TARGET_TOOL_USER" ]; then`,
		`  if [ -n "$TARGET_HOME" ]; then export HOME="$TARGET_HOME"; fi`,
		`  env HOME="${HOME:-$TARGET_HOME}" /bin/sh "$TARGET_SCRIPT_FILE" >>"$TARGET_OUTPUT_FILE" 2>&1`,
		`  STATUS=$?`,
		`elif command -v sudo >/dev/null 2>&1; then`,
		`  if [ -n "$TARGET_HOME" ]; then`,
		`    sudo -n -H -u "$TARGET_TOOL_USER" -- env HOME="$TARGET_HOME" /bin/sh "$TARGET_SCRIPT_FILE" >>"$TARGET_OUTPUT_FILE" 2>&1`,
		`  else`,
		`    sudo -n -H -u "$TARGET_TOOL_USER" -- /bin/sh "$TARGET_SCRIPT_FILE" >>"$TARGET_OUTPUT_FILE" 2>&1`,
		`  fi`,
		`  STATUS=$?`,
		`else`,
		`  append_output "TOOL_ERROR=sudo_missing"`,
		`  STATUS=61`,
		`fi`,
		`if [ ! -s "$TARGET_OUTPUT_FILE" ]; then append_output "TOOL_ERROR=remote_probe_output_empty"; fi`,
		`cat "$TARGET_OUTPUT_FILE" 2>/dev/null || true`,
		`rm -f "$TARGET_OUTPUT_FILE"`,
		`rm -f "$TARGET_SCRIPT_FILE"`,
		`exit "$STATUS"`,
	}, "\n")
}
