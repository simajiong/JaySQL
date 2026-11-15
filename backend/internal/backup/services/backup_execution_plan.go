package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	"gorm.io/datatypes"
)

type unifiedBackupExecutionPlan struct {
	DBType        string                      `json:"db_type"`
	DriverType    string                      `json:"driver_type"`
	BackupType    string                      `json:"backup_type"`
	ServerVersion string                      `json:"server_version,omitempty"`
	Artifact      *BackupArtifactPlan         `json:"artifact,omitempty"`
	Logical       *logicalExecutionPlan       `json:"logical,omitempty"`
	MySQLPhysical *mysqlPhysicalExecutionPlan `json:"mysql_physical,omitempty"`
	PGBackRest    *pgBackRestExecutionPlan    `json:"pg_backrest,omitempty"`
}

type logicalExecutionPlan struct {
	EffectiveOSUser string            `json:"effective_os_user,omitempty"`
	OSUserSource    string            `json:"os_user_source,omitempty"`
	Tool            *localToolSpec    `json:"tool,omitempty"`
	ToolCheckUser   string            `json:"tool_check_user,omitempty"`
	VersionLine     string            `json:"version_line,omitempty"`
	RuntimeInspect  map[string]string `json:"runtime_inspect,omitempty"`
	RepositoryPath  string            `json:"repository_path,omitempty"`
}

type mysqlPhysicalExecutionPlan struct {
	EffectiveOSUser      string                      `json:"effective_os_user,omitempty"`
	OSUserSource         string                      `json:"os_user_source,omitempty"`
	Tool                 *localToolSpec              `json:"tool,omitempty"`
	ToolCheckUser        string                      `json:"tool_check_user,omitempty"`
	AutoProvisionSummary string                      `json:"auto_provision_summary,omitempty"`
	RuntimeInspect       map[string]string           `json:"runtime_inspect,omitempty"`
	RuntimeVars          map[string]string           `json:"runtime_vars,omitempty"`
	Admission            *mysqlPhysicalAdmissionPlan `json:"admission,omitempty"`
	RequestedBackupType  string                      `json:"requested_backup_type,omitempty"`
	EffectiveBackupType  string                      `json:"effective_backup_type,omitempty"`
	BaseInfo             *mysqlPhysicalBackupBase    `json:"base_info,omitempty"`
	Warnings             []string                    `json:"warnings,omitempty"`
}

type pgBackRestExecutionPlan struct {
	PGOSUser            string                  `json:"pg_os_user,omitempty"`
	Tool                *localToolSpec          `json:"tool,omitempty"`
	PSQLTool            *localToolSpec          `json:"psql_tool,omitempty"`
	RepoRemote          bool                    `json:"repo_remote"`
	RepoDir             string                  `json:"repo_dir,omitempty"`
	Stanza              string                  `json:"stanza,omitempty"`
	PGDataPath          string                  `json:"pg_data_path,omitempty"`
	RequestedBackupType string                  `json:"requested_backup_type,omitempty"`
	SourceTool          *remotePostgresToolSpec `json:"source_tool,omitempty"`
	RepoTool            *remotePostgresToolSpec `json:"repo_tool,omitempty"`
}

func buildUnifiedBackupExecutionPlanForPrecheck(ctx *backupTaskPrecheckContext) (*unifiedBackupExecutionPlan, error) {
	if ctx == nil || ctx.Instance == nil || ctx.Form == nil {
		return nil, fmt.Errorf("预检查上下文不存在")
	}
	artifact, _ := PlanBackupArtifact(string(ctx.Instance.DbType), ctx.DriverType, ctx.Form.ObjectMode, "", ctx.BackupType, marshalAnyJSON(ctx.Form.ObjectSpec), marshalAnyJSON(ctx.Form.DriverConfig))
	plan := &unifiedBackupExecutionPlan{
		DBType:        strings.TrimSpace(string(ctx.Instance.DbType)),
		DriverType:    strings.TrimSpace(ctx.DriverType),
		BackupType:    strings.TrimSpace(ctx.BackupType),
		ServerVersion: strings.TrimSpace(ctx.ServerVersion),
		Artifact:      artifact,
	}
	switch strings.ToUpper(strings.TrimSpace(ctx.DriverType)) {
	case "LOGICAL":
		logicalPlan, err := buildLogicalExecutionPlanForPrecheck(ctx)
		plan.Logical = logicalPlan
		return plan, err
	case "XTRABACKUP":
		mysqlPlan, err := buildMySQLPhysicalExecutionPlanForPrecheck(ctx)
		plan.MySQLPhysical = mysqlPlan
		return plan, err
	case "PG_BACKREST":
		pgPlan, err := buildPGBackRestExecutionPlanForPrecheck(context.Background(), ctx)
		plan.PGBackRest = pgPlan
		return plan, err
	default:
		return nil, fmt.Errorf("暂不支持的执行计划驱动: %s", strings.TrimSpace(ctx.DriverType))
	}
}

func buildUnifiedBackupExecutionPlanForRun(ctx context.Context, input BackupExecutionInput) (*unifiedBackupExecutionPlan, error) {
	artifact, _ := PlanBackupArtifact(input.DBType, input.DriverType, input.ObjectMode, input.SourceDBName, input.BackupType, input.ObjectSpec, input.DriverConfig)
	plan := &unifiedBackupExecutionPlan{
		DBType:     strings.TrimSpace(input.DBType),
		DriverType: strings.TrimSpace(input.DriverType),
		BackupType: strings.TrimSpace(input.BackupType),
		Artifact:   artifact,
	}
	switch strings.ToUpper(strings.TrimSpace(input.DriverType)) {
	case "LOGICAL":
		logicalPlan, serverVersion, err := buildLogicalExecutionPlanForRun(ctx, input)
		plan.ServerVersion = strings.TrimSpace(serverVersion)
		plan.Logical = logicalPlan
		return plan, err
	case "XTRABACKUP":
		mysqlPlan, serverVersion, err := buildMySQLPhysicalExecutionPlanForRun(ctx, input)
		plan.ServerVersion = strings.TrimSpace(serverVersion)
		plan.MySQLPhysical = mysqlPlan
		return plan, err
	case "PG_BACKREST":
		pgPlan, serverVersion, err := buildPGBackRestExecutionPlanForRun(ctx, input)
		plan.ServerVersion = strings.TrimSpace(serverVersion)
		plan.PGBackRest = pgPlan
		return plan, err
	default:
		return nil, fmt.Errorf("暂不支持的执行计划驱动: %s", strings.TrimSpace(input.DriverType))
	}
}

func buildLogicalExecutionPlanForPrecheck(ctx *backupTaskPrecheckContext) (*logicalExecutionPlan, error) {
	if ctx == nil {
		return nil, fmt.Errorf("预检查上下文不存在")
	}
	effectiveCfg := ctx.LogicalConfig
	runtimeInspect := ctx.RuntimeInspect
	osUserSource := ""
	if strings.TrimSpace(effectiveCfg.OSUser) != "" {
		osUserSource = "user_config"
	} else {
		effectiveOSUser, parsed, detectedSource, err := resolveRemoteExecutionOSUserForPrecheck(ctx, effectiveCfg.OSUser)
		if err != nil {
			return nil, err
		}
		runtimeInspect = parsed
		effectiveCfg.OSUser = effectiveOSUser
		osUserSource = detectedSource
	}
	resolution, err := resolveLogicalBackupToolForPrecheck(ctx, effectiveCfg)
	if err != nil {
		return nil, err
	}
	plan := &logicalExecutionPlan{
		EffectiveOSUser: strings.TrimSpace(effectiveCfg.OSUser),
		OSUserSource:    strings.TrimSpace(osUserSource),
		RuntimeInspect:  runtimeInspect,
		RepositoryPath:  strings.TrimSpace(ctx.RepoPath),
	}
	if resolution != nil {
		plan.Tool = resolution.Spec
		plan.ToolCheckUser = strings.TrimSpace(resolution.ToolUser)
		plan.VersionLine = strings.TrimSpace(resolution.VersionLine)
		if plan.EffectiveOSUser == "" {
			plan.EffectiveOSUser = strings.TrimSpace(resolution.EffectiveOSUser)
		}
	}
	return plan, nil
}

func buildLogicalExecutionPlanForRun(ctx context.Context, input BackupExecutionInput) (*logicalExecutionPlan, string, error) {
	cfg, err := loadLogicalBackupDriverConfig(input.DriverConfig)
	if err != nil {
		return nil, "", err
	}
	serverVersion, err := probeServerVersionForLogical(input)
	if err != nil {
		return nil, "", err
	}
	fallbackOSUser := detectDatabaseOSUserOrEmpty(input.DBType)
	resolvedOSUser, osUserSource := resolveLocalExecutionOSUser(cfg.OSUser, parseExtraConfig(input.Instance.ExtraConfig), fallbackOSUser)
	resolution, err := resolveLogicalBackupToolLocallyWithFallbackUser(ctx, input.DBType, serverVersion, cfg, fallbackOSUser)
	if err != nil {
		return nil, serverVersion, err
	}
	repositoryPath := strings.TrimSpace(input.OutputBaseDir)
	if input.StorageTarget != nil && strings.EqualFold(strings.TrimSpace(input.StorageTarget.Type), backupModels.StorageTargetTypeLocalServer) {
		repositoryPath = joinLogicalPrecheckRepositoryPath(input.StorageTarget.BasePath, input.OutputBaseDir)
	}
	plan := &logicalExecutionPlan{
		EffectiveOSUser: resolvedOSUser,
		OSUserSource:    osUserSource,
		RepositoryPath:  repositoryPath,
	}
	if resolution != nil {
		plan.Tool = resolution.Spec
		plan.ToolCheckUser = strings.TrimSpace(resolution.ToolUser)
		plan.VersionLine = strings.TrimSpace(resolution.VersionLine)
		if plan.EffectiveOSUser == "" {
			plan.EffectiveOSUser = strings.TrimSpace(resolution.EffectiveOSUser)
		}
	}
	return plan, serverVersion, nil
}

func buildMySQLPhysicalExecutionPlanForPrecheck(ctx *backupTaskPrecheckContext) (*mysqlPhysicalExecutionPlan, error) {
	if ctx == nil {
		return nil, fmt.Errorf("预检查上下文不存在")
	}
	remoteCfg, err := buildMySQLPhysicalExecutorRemoteConfig(ctx)
	if err != nil {
		return nil, err
	}
	runner, err := newSSHRunner(remoteCfg)
	if err != nil {
		return nil, err
	}
	defer runner.Close()
	effectiveCfg := ctx.MySQLPhysicalConfig
	effectiveOSUser, runtimeInspect, osUserSource, err := resolveRemoteExecutionOSUserForPrecheck(ctx, effectiveCfg.OSUser)
	if err != nil {
		return nil, err
	}
	effectiveCfg.OSUser = effectiveOSUser
	toolSpec, autoProvisionSummary, toolUser, err := verifyRemoteMySQLPhysicalTool(runner, remoteCfg, ctx.ServerVersion, effectiveCfg, "xtrabackup")
	if err != nil {
		return nil, err
	}
	runtimeVars, err := probeMySQLPhysicalVariables(strings.TrimSpace(ctx.Instance.Hostname), ctx.Instance.Port, strings.TrimSpace(string(ctx.Instance.User)), strings.TrimSpace(string(ctx.Instance.Password)))
	if err != nil {
		return nil, err
	}
	admission, err := probeMySQLPhysicalAdmissionFromDB(strings.TrimSpace(ctx.Instance.Hostname), ctx.Instance.Port, strings.TrimSpace(string(ctx.Instance.User)), strings.TrimSpace(string(ctx.Instance.Password)), ctx.ServerVersion)
	if err != nil {
		return nil, err
	}
	if fileAccess, fileErr := probeMySQLPhysicalPathsRemote(
		runner,
		effectiveCfg.OSUser,
		strings.TrimSpace(runtimeVars["datadir"]),
		strings.TrimSpace(runtimeVars["redo_base_dir"]),
		strings.TrimSpace(runtimeVars["undo_base_dir"]),
		strings.TrimSpace(runtimeVars["data_home_base_dir"]),
		splitMySQLPhysicalConfiguredDirs(strings.TrimSpace(runtimeVars["innodb_directories_list"])),
	); fileErr != nil {
		return nil, fileErr
	} else if admission != nil && fileAccess != nil {
		admission.Datadir = firstNonEmpty(strings.TrimSpace(fileAccess.Datadir), strings.TrimSpace(runtimeVars["datadir"]))
		admission.DatadirCheckUser = strings.TrimSpace(fileAccess.DatadirCheckUser)
		admission.DatadirReadable = fileAccess.DatadirReadable
		admission.RedoSearchBase = strings.TrimSpace(fileAccess.RedoSearchBase)
		admission.RedoCheckUser = strings.TrimSpace(fileAccess.RedoCheckUser)
		admission.RedoLogPaths = append([]string(nil), fileAccess.RedoLogPaths...)
		admission.UnreadableRedoLogPaths = append([]string(nil), fileAccess.UnreadableRedoLogPaths...)
		admission.UndoSearchBase = strings.TrimSpace(fileAccess.UndoSearchBase)
		admission.UndoCheckUser = strings.TrimSpace(fileAccess.UndoCheckUser)
		admission.UndoDirReadable = fileAccess.UndoDirReadable
		admission.UndoLogPaths = append([]string(nil), fileAccess.UndoLogPaths...)
		admission.UnreadableUndoLogPaths = append([]string(nil), fileAccess.UnreadableUndoLogPaths...)
		admission.DataHomeSearchBase = strings.TrimSpace(fileAccess.DataHomeSearchBase)
		admission.DataCheckUser = strings.TrimSpace(fileAccess.DataCheckUser)
		admission.DataHomeDirReadable = fileAccess.DataHomeDirReadable
		admission.DataDirPaths = append([]string(nil), fileAccess.DataDirPaths...)
		admission.UnreadableDataDirPaths = append([]string(nil), fileAccess.UnreadableDataDirPaths...)
		admission.DataFilePaths = append([]string(nil), fileAccess.DataFilePaths...)
		admission.UnreadableDataFiles = append([]string(nil), fileAccess.UnreadableDataFiles...)
		admission.BlockingIssues = append([]string(nil), mysqlPhysicalAdmissionIssues(admission)...)
	}
	return &mysqlPhysicalExecutionPlan{
		EffectiveOSUser:      strings.TrimSpace(effectiveCfg.OSUser),
		OSUserSource:         strings.TrimSpace(osUserSource),
		Tool:                 toolSpec,
		ToolCheckUser:        strings.TrimSpace(toolUser),
		AutoProvisionSummary: strings.TrimSpace(autoProvisionSummary),
		RuntimeInspect:       runtimeInspect,
		RuntimeVars:          runtimeVars,
		Admission:            admission,
		RequestedBackupType:  normalizeMySQLPhysicalBackupType(ctx.BackupType),
		EffectiveBackupType:  normalizeMySQLPhysicalBackupType(ctx.BackupType),
	}, nil
}

func buildMySQLPhysicalExecutionPlanForRun(ctx context.Context, input BackupExecutionInput) (*mysqlPhysicalExecutionPlan, string, error) {
	cfg, err := loadMySQLPhysicalDriverConfig(input.DriverConfig)
	if err != nil {
		return nil, "", err
	}
	serverVersion, err := probeMySQLVersion(input.Instance.Hostname, input.Instance.Port, input.Instance.User, input.Instance.Password)
	if err != nil {
		return nil, "", err
	}
	fallbackOSUser := detectDatabaseOSUserOrEmpty("MySQL")
	configuredOrDetectedOSUser, osUserSource := resolveLocalExecutionOSUser(cfg.OSUser, parseExtraConfig(input.Instance.ExtraConfig), fallbackOSUser)
	toolSpec, effectiveOSUser, toolUser, err := resolveMySQLPhysicalToolLocallyWithFallbackUser(ctx, serverVersion, cfg, fallbackOSUser)
	if err != nil {
		return nil, serverVersion, err
	}
	runtimeVars, err := probeMySQLPhysicalVariables(input.Instance.Hostname, input.Instance.Port, input.Instance.User, input.Instance.Password)
	if err != nil {
		return nil, serverVersion, err
	}
	admission, err := probeMySQLPhysicalAdmissionFromDB(input.Instance.Hostname, input.Instance.Port, input.Instance.User, input.Instance.Password, serverVersion)
	if err != nil {
		return nil, serverVersion, err
	}
	if fileAccess, fileErr := probeMySQLPhysicalPathsLocal(
		ctx,
		effectiveOSUser,
		strings.TrimSpace(runtimeVars["datadir"]),
		strings.TrimSpace(runtimeVars["redo_base_dir"]),
		strings.TrimSpace(runtimeVars["undo_base_dir"]),
		strings.TrimSpace(runtimeVars["data_home_base_dir"]),
		splitMySQLPhysicalConfiguredDirs(strings.TrimSpace(runtimeVars["innodb_directories_list"])),
	); fileErr != nil {
		return nil, serverVersion, fileErr
	} else if admission != nil && fileAccess != nil {
		admission.Datadir = firstNonEmpty(strings.TrimSpace(fileAccess.Datadir), strings.TrimSpace(runtimeVars["datadir"]))
		admission.DatadirCheckUser = strings.TrimSpace(fileAccess.DatadirCheckUser)
		admission.DatadirReadable = fileAccess.DatadirReadable
		admission.RedoSearchBase = strings.TrimSpace(fileAccess.RedoSearchBase)
		admission.RedoCheckUser = strings.TrimSpace(fileAccess.RedoCheckUser)
		admission.RedoLogPaths = append([]string(nil), fileAccess.RedoLogPaths...)
		admission.UnreadableRedoLogPaths = append([]string(nil), fileAccess.UnreadableRedoLogPaths...)
		admission.UndoSearchBase = strings.TrimSpace(fileAccess.UndoSearchBase)
		admission.UndoCheckUser = strings.TrimSpace(fileAccess.UndoCheckUser)
		admission.UndoDirReadable = fileAccess.UndoDirReadable
		admission.UndoLogPaths = append([]string(nil), fileAccess.UndoLogPaths...)
		admission.UnreadableUndoLogPaths = append([]string(nil), fileAccess.UnreadableUndoLogPaths...)
		admission.DataHomeSearchBase = strings.TrimSpace(fileAccess.DataHomeSearchBase)
		admission.DataCheckUser = strings.TrimSpace(fileAccess.DataCheckUser)
		admission.DataHomeDirReadable = fileAccess.DataHomeDirReadable
		admission.DataDirPaths = append([]string(nil), fileAccess.DataDirPaths...)
		admission.UnreadableDataDirPaths = append([]string(nil), fileAccess.UnreadableDataDirPaths...)
		admission.DataFilePaths = append([]string(nil), fileAccess.DataFilePaths...)
		admission.UnreadableDataFiles = append([]string(nil), fileAccess.UnreadableDataFiles...)
		admission.BlockingIssues = append([]string(nil), mysqlPhysicalAdmissionIssues(admission)...)
	}
	requestedType := normalizeMySQLPhysicalBackupType(input.BackupType)
	if requestedType == "" {
		requestedType = "FULL"
	}
	effectiveType := requestedType
	var baseInfo *mysqlPhysicalBackupBase
	warnings := []string{}
	if effectiveType == "INCR" && input.StorageTarget != nil {
		// 基线读取失败（仓库不可达 / 存储目标类型不支持 / 目录不可读）不等于备份失败：
		// 与创建执行记录时的定时判定保持同一口径 —— 直接降级为全量，让本次备份产出新的基线。
		// 早期版本在这里直接 return error，导致「有降级配置却仍整单失败」（run 495 的根因之一）。
		var baseErr error
		baseInfo, baseErr = findLatestMySQLPhysicalBase(input.StorageTarget, input.RunDirSpec())
		if baseErr != nil {
			if !cfg.FallbackToFullWhenMissing {
				return nil, serverVersion, fmt.Errorf("读取 MySQL 物理备份基线失败: %w", baseErr)
			}
			effectiveType = "FULL"
			baseInfo = nil
			warnings = append(warnings, fmt.Sprintf("读取物理基线失败（%v），已自动降级为 FULL", baseErr))
		} else if baseInfo == nil || strings.TrimSpace(baseInfo.ToLSN) == "" {
			if cfg.FallbackToFullWhenMissing {
				effectiveType = "FULL"
				baseInfo = nil
				warnings = append(warnings, "未找到可用基线，已自动降级为 FULL")
			} else {
				return nil, serverVersion, fmt.Errorf("未找到可用的 MySQL 物理备份基线，无法执行 INCR")
			}
		}
	}
	return &mysqlPhysicalExecutionPlan{
		EffectiveOSUser:     firstNonEmpty(strings.TrimSpace(effectiveOSUser), configuredOrDetectedOSUser),
		OSUserSource:        osUserSource,
		Tool:                toolSpec,
		ToolCheckUser:       strings.TrimSpace(toolUser),
		RuntimeVars:         runtimeVars,
		Admission:           admission,
		RequestedBackupType: requestedType,
		EffectiveBackupType: effectiveType,
		BaseInfo:            baseInfo,
		Warnings:            warnings,
	}, serverVersion, nil
}

func buildPGBackRestExecutionPlanForPrecheck(runCtx context.Context, ctx *backupTaskPrecheckContext) (*pgBackRestExecutionPlan, error) {
	if ctx == nil || ctx.Instance == nil {
		return nil, fmt.Errorf("预检查上下文不存在")
	}
	input := BackupExecutionInput{
		TaskID:           ctx.Resolved.TaskID,
		SourceInstanceID: ctx.Form.SourceInstanceID,
		DBType:           strings.TrimSpace(string(ctx.Instance.DbType)),
		DriverType:       strings.TrimSpace(ctx.DriverType),
		DriverConfig:     datatypes.JSON(ctx.mustMarshalDriverConfig()),
		BackupType:       strings.TrimSpace(ctx.BackupType),
		OutputBaseDir:    strings.TrimSpace(ctx.OutputDir),
		Instance: BackupExecutionInstance{
			Hostname:    strings.TrimSpace(ctx.Instance.Hostname),
			Port:        ctx.Instance.Port,
			User:        strings.TrimSpace(string(ctx.Instance.User)),
			Password:    strings.TrimSpace(string(ctx.Instance.Password)),
			DBType:      strings.TrimSpace(string(ctx.Instance.DbType)),
			ExtraConfig: ctx.Instance.ExtraConfig,
		},
	}
	plan, _, err := buildPGBackRestExecutionPlanForRun(runCtx, input)
	return plan, err
}

func buildPGBackRestExecutionPlanForRun(ctx context.Context, input BackupExecutionInput) (*pgBackRestExecutionPlan, string, error) {
	extra := parseExtraConfig(input.Instance.ExtraConfig)
	sslmode := extra["sslmode"]
	defaultDatabase := firstNonEmpty(strings.TrimSpace(extra["database"]), "postgres")
	serverVersion, err := probePostgresVersion(input.Instance.Hostname, input.Instance.Port, input.Instance.User, input.Instance.Password, defaultDatabase, sslmode)
	if err != nil {
		return nil, "", err
	}
	if err := validatePostgresPhysicalVersion(serverVersion); err != nil {
		return nil, serverVersion, err
	}
	pgBackRestTool, err := resolvePostgresClientTool("pgbackrest", serverVersion)
	if err != nil {
		return nil, serverVersion, err
	}
	psqlTool, err := resolvePostgresClientTool("psql", serverVersion)
	if err != nil {
		return nil, serverVersion, err
	}
	cfg, err := loadPGBackRestDriverConfig(input.DriverConfig)
	if err != nil {
		return nil, serverVersion, err
	}
	repoRemoteMode := cfg.RepoRemote && strings.TrimSpace(cfg.RepoHost) != ""
	if cfg.RepoRemote && !repoRemoteMode {
		return nil, serverVersion, fmt.Errorf("pgBackRest 异地仓库模式缺少 repo_host，无法直写远端仓库")
	}
	repoDir := strings.TrimSpace(cfg.RepoPath)
	if repoDir == "" {
		repoDir = resolvePGBackRestRepoDir(input.OutputBaseDir, input.SourceInstanceID, input.TaskID)
	}
	pgDataPath, err := detectPostgresSetting(ctx, psqlTool, input.Instance, defaultDatabase, sslmode, "data_directory", nil)
	if err != nil {
		return nil, serverVersion, err
	}
	stanza := strings.TrimSpace(cfg.Stanza)
	if archiveCommand, detectErr := detectPostgresSetting(ctx, psqlTool, input.Instance, defaultDatabase, sslmode, "archive_command", nil); detectErr == nil {
		if resolvedStanza := extractPGBackRestArchiveStanza(archiveCommand); resolvedStanza != "" {
			stanza = resolvedStanza
		}
	}
	if stanza == "" {
		stanza = fmt.Sprintf("pg_%d_%d_%s", input.SourceInstanceID, input.Instance.Port, sanitizePathSegment(input.TaskID, "task"))
	}
	// S3 仓库模式：repo 在对象存储上，绝不能采纳实例本机 /etc/pgbackrest 的 repo1-path
	//（那会把 OSS 前缀覆盖成本地目录），只允许继承其 stanza。
	if !repoRemoteMode && !cfg.IsS3Repo() {
		installedCfg, resolveErr := resolveInstalledLocalPGBackRestConfig(ctx, psqlTool, input.Instance, defaultDatabase, sslmode, pgDataPath)
		if resolveErr == nil && installedCfg != nil {
			if strings.TrimSpace(cfg.Stanza) != "" && !strings.EqualFold(strings.TrimSpace(cfg.Stanza), installedCfg.Stanza) {
				return nil, serverVersion, fmt.Errorf("当前实例已启用 pgBackRest 归档，任务 stanza=%s 与实例归档 stanza=%s 不一致，请统一后重试", strings.TrimSpace(cfg.Stanza), installedCfg.Stanza)
			}
			stanza = installedCfg.Stanza
			repoDir = installedCfg.Repo1Path
		}
	}
	plan := &pgBackRestExecutionPlan{
		PGOSUser:            detectDatabaseOSUserOrEmpty("PostgreSQL"),
		Tool:                pgBackRestTool,
		PSQLTool:            psqlTool,
		RepoRemote:          repoRemoteMode,
		RepoDir:             repoDir,
		Stanza:              stanza,
		PGDataPath:          pgDataPath,
		RequestedBackupType: firstNonEmpty(strings.TrimSpace(cfg.BackupType), "FULL"),
	}
	if repoRemoteMode {
		sourceTool, repoTool, resolveErr := resolvePGBackRestRemoteToolPair(input, cfg, extra, serverVersion)
		if resolveErr != nil {
			return nil, serverVersion, resolveErr
		}
		plan.SourceTool = sourceTool
		plan.RepoTool = repoTool
	}
	return plan, serverVersion, nil
}

func resolveMySQLPhysicalToolLocallyWithFallbackUser(ctx context.Context, serverVersion string, cfg mysqlPhysicalDriverConfig, fallbackOSUser string) (*localToolSpec, string, string, error) {
	runCtx := ctx
	if runCtx == nil {
		runCtx = context.Background()
	}
	effectiveUser := firstNonEmpty(strings.TrimSpace(cfg.OSUser), strings.TrimSpace(fallbackOSUser))
	script := buildMySQLPhysicalToolResolveProbeScript(serverVersion, cfg.ToolPath, remoteMySQLPhysicalToolCandidates("", serverVersion, "xtrabackup"), "xtrabackup")
	var (
		output string
		err    error
	)
	if effectiveUser != "" {
		output, err = runLocalCommandCaptureAsUser(runCtx, effectiveUser, "", "/bin/sh", []string{"-lc", script}, nil, nil)
	} else {
		output, err = runLocalCommandCaptureAsDBUser(runCtx, "MySQL", "", "/bin/sh", []string{"-lc", script}, nil, nil)
	}
	spec, toolUser, resolveErr := finalizeRemoteMySQLPhysicalToolResolution(serverVersion, cfg, output, err)
	if resolveErr != nil {
		return nil, effectiveUser, toolUser, resolveErr
	}
	return spec, effectiveUser, toolUser, nil
}

func resolvePGBackRestRemoteToolPair(input BackupExecutionInput, cfg pgBackRestDriverConfig, extra map[string]string, serverVersion string) (*remotePostgresToolSpec, *remotePostgresToolSpec, error) {
	sourceSSHUser, sourceSSHPass, sourceSSHKey, sourceSSHPort, err := resolveBackupInstanceSSHCredentials(extra, input.Instance.User, input.Instance.Password)
	if err != nil {
		return nil, nil, err
	}
	sourceRunner, err := newSSHRunner(&backupNodeRemoteConfig{
		Host:          strings.TrimSpace(input.Instance.Hostname),
		Port:          sourceSSHPort,
		SSHUser:       defaultBackupNodeSSHUser(sourceSSHUser),
		SSHPassword:   strings.TrimSpace(sourceSSHPass),
		SSHPrivateKey: strings.TrimSpace(sourceSSHKey),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("连接源端主机检查 pgBackRest 失败: %w", err)
	}
	defer sourceRunner.Close()
	sourceToolSpec, err := resolveRemotePostgresToolSpec(sourceRunner, defaultBackupNodeToolsDir(""), serverVersion, "pgbackrest")
	if err != nil {
		return nil, nil, fmt.Errorf("识别源端 pgBackRest 工具失败: %w", err)
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
		return nil, nil, fmt.Errorf("连接仓库节点检查 pgBackRest 失败: %w", err)
	}
	defer repoRunner.Close()
	repoToolSpec, err := resolveRemotePostgresToolSpec(repoRunner, defaultBackupNodeToolsDir(cfg.RepoToolsDir), serverVersion, "pgbackrest")
	if err != nil {
		return nil, nil, fmt.Errorf("识别仓库节点 pgBackRest 工具失败: %w", err)
	}
	if strings.TrimSpace(sourceToolSpec.VersionLine) != "" && strings.TrimSpace(repoToolSpec.VersionLine) != "" &&
		!strings.EqualFold(strings.TrimSpace(sourceToolSpec.VersionLine), strings.TrimSpace(repoToolSpec.VersionLine)) {
		return nil, nil, fmt.Errorf("源端与仓库端 pgBackRest 版本不一致：source=%s (%s), repo=%s (%s)", sourceToolSpec.VersionLine, sourceToolSpec.Path, repoToolSpec.VersionLine, repoToolSpec.Path)
	}
	return sourceToolSpec, repoToolSpec, nil
}

func probeServerVersionForLogical(input BackupExecutionInput) (string, error) {
	switch {
	case strings.EqualFold(strings.TrimSpace(input.DBType), "MySQL"):
		return probeMySQLVersion(input.Instance.Hostname, input.Instance.Port, input.Instance.User, input.Instance.Password)
	case isPostgresLogicalFamily(input.DBType):
		// 金仓同样用 PG 协议探测版本，返回形如 KingbaseES V009R001C010；
		// 该版本串不参与客户端 major 校验（见 logical_family.go 的说明）。
		extra := parseExtraConfig(input.Instance.ExtraConfig)
		return probePostgresVersion(
			input.Instance.Hostname,
			input.Instance.Port,
			input.Instance.User,
			input.Instance.Password,
			firstNonEmpty(strings.TrimSpace(extra["database"]), logicalDefaultDatabase(input.DBType)),
			strings.TrimSpace(extra["sslmode"]),
		)
	default:
		return "", fmt.Errorf("不支持的逻辑备份数据库类型: %s", strings.TrimSpace(input.DBType))
	}
}

func detectDatabaseOSUserOrEmpty(dbType string) string {
	user, err := detectDatabaseOSUser(dbType)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(user)
}

func marshalAnyJSON(v any) datatypes.JSON {
	if v == nil {
		return nil
	}
	buf, _ := json.Marshal(v)
	return datatypes.JSON(buf)
}
