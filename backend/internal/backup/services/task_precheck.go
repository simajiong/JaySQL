package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/sirupsen/logrus"
	"gorm.io/datatypes"

	dasDao "github.com/simajiong/jaysql/internal/das/dao"
)

const (
	backupTaskPrecheckSeverityError   = "ERROR"
	backupTaskPrecheckSeverityWarning = "WARNING"
	backupTaskPrecheckSeverityInfo    = "INFO"
)

type backupTaskPrecheckItem struct {
	ItemKey  string         `json:"item_key"`
	Label    string         `json:"label"`
	Severity string         `json:"severity"`
	Blocking bool           `json:"blocking"`
	Passed   bool           `json:"passed"`
	Message  string         `json:"message"`
	Detail   map[string]any `json:"detail,omitempty"`
}

type backupTaskPrecheckResolved struct {
	TaskID               string `json:"task_id"`
	SourceHost           string `json:"source_host"`
	SourcePort           int    `json:"source_port"`
	SourceDatabase       string `json:"source_database"`
	ExecutorNodeID       uint64 `json:"executor_node_id"`
	ExecutorHost         string `json:"executor_host"`
	RepositoryNodeID     uint64 `json:"repository_node_id"`
	RepositoryHost       string `json:"repository_host"`
	RepositoryPath       string `json:"repository_path"`
	Stanza               string `json:"stanza"`
	ServerVersion        string `json:"server_version"`
	ServerMajor          string `json:"server_major"`
	RecommendedToolMajor string `json:"recommended_tool_major"`
	PGDataPath           string `json:"pg_data_path"`
	PGBackRestExpected   string `json:"pgbackrest_expected_archive_command"`
	EstimatedSizeBytes   int64  `json:"estimated_size_bytes"`
	RepositoryFreeBytes  int64  `json:"repository_free_bytes"`
	MaxWalSenders        string `json:"max_wal_senders"`
}

type backupTaskPrecheckContext struct {
	C                   *gin.Context
	Form                *backupForms.PrecheckBackupTaskForm
	Instance            *commonModels.InsightInstances
	ExecutorNode        *backupModels.InsightBackupNodes
	RepositoryNode      *backupModels.InsightBackupNodes
	StorageTarget       *backupModels.InsightBackupStorageTargets
	Task                *backupModels.InsightBackupTasks
	DriverType          string
	BackupType          string
	DriverConfig        pgBackRestDriverConfig
	MySQLPhysicalConfig mysqlPhysicalDriverConfig
	LogicalConfig       logicalBackupDriverConfig
	OutputDir           string
	ServerVersion       string
	ServerMajor         string
	PGOSUser            string
	PGDataPath          string
	SourceDatabase      string
	SSLMode             string
	RepoPath            string
	Stanza              string
	RepoIsS3            bool // 仓库为 S3 兼容对象存储（repo1-type=s3）：无仓库节点，连通性/stanza 校验走对象存储
	MaxRuntimeHours     int  // 任务「高级选项」的单次执行最长运行时间（小时），0 表示未配置（影子任务需原样带过去）
	RuntimeInspect      map[string]string
	ExecutionPlan       *unifiedBackupExecutionPlan
	Items               []backupTaskPrecheckItem
	Resolved            backupTaskPrecheckResolved
}

type PrecheckBackupTaskService struct {
	*backupForms.PrecheckBackupTaskForm
	C *gin.Context
}

func (s *PrecheckBackupTaskService) Run() (any, error) {
	result, err := runBackupTaskPrecheck(s.PrecheckBackupTaskForm, s.C)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func buildBackupTaskPrecheckContext(form *backupForms.PrecheckBackupTaskForm, c *gin.Context) (*backupTaskPrecheckContext, error) {
	instance, err := loadInstanceByID(form.SourceInstanceID)
	if err != nil {
		return nil, err
	}
	backupType := normalizeBackupType(form.BackupType)
	driverType, driverConfigJSON, err := normalizeBackupDriverConfig(string(instance.DbType), form.DriverType, backupType, form.DriverConfig)
	if err != nil {
		logBackupTaskPrecheckConfigStage(c, "normalize_failed", form, string(instance.DbType), "", nil, nil, nil, err)
		return nil, err
	}
	logBackupTaskPrecheckConfigStage(c, "normalized", form, string(instance.DbType), driverType, form.DriverConfig, driverConfigJSON, nil, nil)
	if strings.EqualFold(strings.TrimSpace(driverType), "LOGICAL") {
		return buildLogicalBackupTaskPrecheckContext(form, c, instance, backupType, driverConfigJSON)
	}
	if strings.EqualFold(strings.TrimSpace(driverType), "XTRABACKUP") {
		if !strings.EqualFold(strings.TrimSpace(string(instance.DbType)), "MySQL") {
			return nil, fmt.Errorf("当前仅支持 MySQL XTRABACKUP 物理备份启动前预校验")
		}
		return buildMySQLPhysicalBackupTaskPrecheckContext(form, c, instance, backupType, driverConfigJSON)
	}
	if !strings.EqualFold(strings.TrimSpace(string(instance.DbType)), "PostgreSQL") {
		return nil, fmt.Errorf("当前仅支持 PostgreSQL 物理备份启动前预校验")
	}
	if !strings.EqualFold(strings.TrimSpace(driverType), "PG_BACKREST") {
		return nil, fmt.Errorf("当前仅支持 PG_BACKREST 物理备份启动前预校验")
	}
	normalized, err := normalizeObjectSpec(string(instance.DbType), driverType, form.ObjectMode, "", backupType, form.ObjectSpec)
	if err != nil {
		return nil, err
	}
	_ = normalized
	if _, err := normalizeScheduleDays(form.ScheduleDays); err != nil {
		return nil, err
	}
	if _, err := normalizeScheduleTime(form.ScheduleTime); err != nil {
		return nil, err
	}
	outputDir, err := resolveBackupOutputBaseDir(normalizeBackupTaskOutputDir(form.OutputDir))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(outputDir) == "" {
		return nil, fmt.Errorf("备份任务必须配置备份目录（output_dir）")
	}
	storageTargetID, _, err := resolveAndValidateStorageTargetForTask(driverType, form.StorageTargetID)
	if err != nil {
		return nil, err
	}
	storageTarget, err := loadStorageTargetByID(storageTargetID)
	if err != nil {
		return nil, err
	}
	effectiveExecutorNodeID := form.ExecutorNodeID
	if effectiveExecutorNodeID == 0 {
		node, ensureErr := ensurePGBackRestExecutorNodeForInstance(c, instance)
		if ensureErr != nil {
			return nil, ensureErr
		}
		effectiveExecutorNodeID = node.ID
	}
	executorNode, err := loadBackupNodeByID(effectiveExecutorNodeID)
	if err != nil {
		return nil, err
	}
	driverCfg, err := loadPGBackRestDriverConfig(driverConfigJSON)
	if err != nil {
		return nil, err
	}
	taskID, err := normalizeProvidedBackupTaskID(form.TaskID)
	if err != nil {
		return nil, err
	}
	if taskID == "" {
		taskID, err = generateBackupTaskID()
		if err != nil {
			return nil, err
		}
	}
	// S3 兼容对象存储仓库：无仓库节点；文件仓库才解析虚拟仓库节点
	isRepoS3 := storageTarget != nil && pgBackRestStorageTargetIsS3(storageTarget)
	var repositoryNode *backupModels.InsightBackupNodes
	if !isRepoS3 {
		if form.RepositoryNodeID > 0 {
			repositoryNode, err = loadBackupNodeByID(form.RepositoryNodeID)
		} else {
			repositoryNode, err = resolveVirtualRepoNodeFromStorageTarget(storageTargetID)
		}
		if err != nil {
			return nil, err
		}
	}
	if isRepoS3 {
		// 预检期的 S3 连接参数来自存储目标（driver_config 中的 S3 字段要等 prepare 阶段才写入），
		// 这里解密后填充，供 buildPGBackRestS3PrecheckConfig 生成合法的预检配置。
		_, _, s3AK, s3SK, decErr := decryptStorageTargetCredentials(storageTarget)
		if decErr != nil {
			return nil, fmt.Errorf("解密对象存储凭据失败: %w", decErr)
		}
		driverCfg.RepoType = "s3"
		driverCfg.S3Bucket = strings.TrimSpace(storageTarget.S3Bucket)
		driverCfg.S3Endpoint = strings.TrimSpace(storageTarget.S3Endpoint)
		driverCfg.S3Region = strings.TrimSpace(storageTarget.S3Region)
		driverCfg.S3AccessKey = s3AK
		driverCfg.S3SecretKey = s3SK
		if storageTarget.S3ForcePathStyle {
			driverCfg.S3URIStyle = "path"
		} else {
			driverCfg.S3URIStyle = "host"
		}
	}
	// S3 仓库：repo_path 是桶内 key 前缀，与 preparePGBackRestRunDriverConfig 的 S3 分支同构；
	// 文件仓库：沿用 outputDir 派生本地仓库目录。
	repoPath := ""
	if isRepoS3 {
		repoBase := strings.TrimSpace(storageTarget.BasePath)
		if repoBase == "" {
			repoBase = "/goinsight-backup"
		}
		repoPath = resolvePGBackRestRepoDir(repoBase, form.SourceInstanceID, taskID)
		if !strings.HasPrefix(repoPath, "/") {
			repoPath = "/" + repoPath
		}
	} else {
		repoPath = resolvePGBackRestRepoDir(outputDir, form.SourceInstanceID, taskID)
	}
	extra := parseExtraConfig(instance.ExtraConfig)
	sourceDatabase := firstNonEmpty(strings.TrimSpace(extra["database"]), "postgres")
	sslMode := strings.TrimSpace(extra["sslmode"])
	serverVersion, err := probePostgresVersion(instance.Hostname, instance.Port, strings.TrimSpace(string(instance.User)), strings.TrimSpace(string(instance.Password)), sourceDatabase, sslMode)
	if err != nil {
		return nil, fmt.Errorf("探测 PostgreSQL 服务端版本失败: %w", err)
	}
	if err := validatePostgresPhysicalVersion(serverVersion); err != nil {
		return nil, err
	}
	serverMajor, err := postgresMajorTag(serverVersion)
	if err != nil {
		return nil, err
	}
	pgOSUser := ""
	if osUserTmp, osUserErr := detectDatabaseOSUser("PostgreSQL"); osUserErr == nil && strings.TrimSpace(osUserTmp) != "" && strings.TrimSpace(osUserTmp) != strings.TrimSpace(currentOSUser()) {
		pgOSUser = strings.TrimSpace(osUserTmp)
	}
	stanza := strings.TrimSpace(driverCfg.Stanza)
	if stanza == "" {
		if archiveCommand, archiveErr := probePostgresArchiveCommand(instance); archiveErr == nil {
			stanza = extractPGBackRestArchiveStanza(archiveCommand)
		}
	}
	if stanza == "" {
		stanza = fmt.Sprintf("pg_%d_%d_%s", form.SourceInstanceID, instance.Port, sanitizePathSegment(taskID, "task"))
	}
	precheckCtx := &backupTaskPrecheckContext{
		C:               c,
		Form:            form,
		Instance:        instance,
		ExecutorNode:    executorNode,
		RepositoryNode:  repositoryNode,
		StorageTarget:   storageTarget,
		DriverType:      driverType,
		BackupType:      backupType,
		DriverConfig:    driverCfg,
		MaxRuntimeHours: precheckMaxRuntimeHours(form),
		OutputDir:       outputDir,
		ServerVersion:   serverVersion,
		ServerMajor:     serverMajor,
		PGOSUser:        pgOSUser,
		SourceDatabase:  sourceDatabase,
		SSLMode:         sslMode,
		RepoPath:        repoPath,
		Stanza:          stanza,
		RepoIsS3:        isRepoS3,
		Resolved: backupTaskPrecheckResolved{
			TaskID:               taskID,
			SourceHost:           strings.TrimSpace(instance.Hostname),
			SourcePort:           instance.Port,
			SourceDatabase:       sourceDatabase,
			ExecutorNodeID:       executorNode.ID,
			ExecutorHost:         strings.TrimSpace(executorNode.Host),
			RepositoryNodeID:     precheckRepoNodeID(repositoryNode),
			RepositoryHost:       precheckRepoNodeHost(repositoryNode),
			RepositoryPath:       repoPath,
			Stanza:               stanza,
			ServerVersion:        serverVersion,
			ServerMajor:          serverMajor,
			RecommendedToolMajor: serverMajor,
			PGBackRestExpected:   fmt.Sprintf("pgbackrest --stanza=%s archive-push %%p", stanza),
		},
	}
	return precheckCtx, nil
}

func runBackupTaskPrecheck(form *backupForms.PrecheckBackupTaskForm, c *gin.Context) (map[string]any, error) {
	ctx, err := buildBackupTaskPrecheckContext(form, c)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(ctx.DriverType), "PG_BACKREST") {
		executeBackupTaskPrecheck(ctx)
	} else if strings.EqualFold(strings.TrimSpace(ctx.DriverType), "XTRABACKUP") {
		executeMySQLPhysicalBackupTaskPrecheck(ctx)
	} else {
		executeLogicalBackupTaskPrecheck(ctx)
	}
	if plan, planErr := buildUnifiedBackupExecutionPlanForPrecheck(ctx); planErr == nil && plan != nil {
		ctx.ExecutionPlan = plan
		ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
			ItemKey:  "execution_plan",
			Label:    "统一执行计划",
			Severity: backupTaskPrecheckSeverityInfo,
			Blocking: true,
			Passed:   true,
			Message:  "准入校验与执行阶段已对齐到同一套执行计划。",
			Detail: map[string]any{
				"driver_type":    strings.TrimSpace(plan.DriverType),
				"db_type":        strings.TrimSpace(plan.DBType),
				"server_version": strings.TrimSpace(plan.ServerVersion),
				"artifact":       plan.Artifact,
				"plan":           plan,
			},
		})
	}
	result := map[string]any{
		"task_id":           ctx.Resolved.TaskID,
		"passed":            backupTaskPrecheckPassed(ctx.Items),
		"can_start":         backupTaskPrecheckPassed(ctx.Items),
		"summary":           summarizeBackupTaskPrecheck(ctx.Items),
		"blocking_failures": countBackupTaskPrecheckItems(ctx.Items, true, false),
		"warning_count":     countBackupTaskPrecheckWarnings(ctx.Items),
		"items":             ctx.Items,
		"resolved":          ctx.Resolved,
		"execution_plan":    ctx.ExecutionPlan,
	}
	persistBackupTaskPrecheckSnapshot(ctx.Resolved.TaskID, result)
	return result, nil
}

func buildLogicalBackupTaskPrecheckContext(form *backupForms.PrecheckBackupTaskForm, c *gin.Context, instance *commonModels.InsightInstances, backupType string, driverConfigJSON datatypes.JSON) (*backupTaskPrecheckContext, error) {
	if !strings.EqualFold(strings.TrimSpace(string(instance.DbType)), "MySQL") && !isPostgresLogicalFamily(string(instance.DbType)) {
		return nil, fmt.Errorf("当前仅支持 MySQL/PostgreSQL/Kingbase 逻辑备份启动前预校验")
	}
	logicalCfg, err := loadLogicalBackupDriverConfig(driverConfigJSON)
	if err != nil {
		logBackupTaskPrecheckConfigStage(c, "logical_load_failed", form, string(instance.DbType), "LOGICAL", form.DriverConfig, driverConfigJSON, nil, err)
		return nil, err
	}
	logBackupTaskPrecheckConfigStage(c, "logical_loaded", form, string(instance.DbType), "LOGICAL", form.DriverConfig, driverConfigJSON, logicalCfg, nil)
	if _, err := normalizeObjectSpec(string(instance.DbType), "LOGICAL", form.ObjectMode, "", backupType, form.ObjectSpec); err != nil {
		return nil, err
	}
	if _, err := normalizeScheduleDays(form.ScheduleDays); err != nil {
		return nil, err
	}
	if _, err := normalizeScheduleTime(form.ScheduleTime); err != nil {
		return nil, err
	}
	outputDir, err := resolveBackupOutputBaseDir(normalizeBackupTaskOutputDir(form.OutputDir))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(outputDir) == "" {
		return nil, fmt.Errorf("备份任务必须配置备份目录（output_dir）")
	}
	storageTargetID, _, err := resolveAndValidateStorageTargetForTask("LOGICAL", form.StorageTargetID)
	if err != nil {
		return nil, err
	}
	storageTarget, err := loadStorageTargetByID(storageTargetID)
	if err != nil {
		return nil, err
	}
	effectiveExecutorNodeID := form.ExecutorNodeID
	if effectiveExecutorNodeID == 0 {
		node, ensureErr := ensureLogicalBackupDBHostNode(c, instance)
		if ensureErr != nil {
			return nil, ensureErr
		}
		effectiveExecutorNodeID = node.ID
	}
	executorNode, err := loadBackupNodeByID(effectiveExecutorNodeID)
	if err != nil {
		return nil, err
	}
	taskID, err := normalizeProvidedBackupTaskID(form.TaskID)
	if err != nil {
		return nil, err
	}
	if taskID == "" {
		taskID, err = generateBackupTaskID()
		if err != nil {
			return nil, err
		}
	}
	extra := parseExtraConfig(instance.ExtraConfig)
	sourceDatabase := strings.TrimSpace(extra["database"])
	sslMode := strings.TrimSpace(extra["sslmode"])
	if isPostgresLogicalFamily(string(instance.DbType)) {
		sourceDatabase = firstNonEmpty(sourceDatabase, logicalDefaultDatabase(string(instance.DbType)))
	} else {
		sourceDatabase = firstNonEmpty(sourceDatabase, "mysql")
	}
	serverVersion := ""
	serverMajor := ""
	recommendedToolMajor := ""
	if string(instance.DbType) == "MySQL" {
		serverVersion, err = probeMySQLVersion(strings.TrimSpace(instance.Hostname), instance.Port, strings.TrimSpace(string(instance.User)), strings.TrimSpace(string(instance.Password)))
		if err != nil {
			return nil, fmt.Errorf("探测 MySQL 服务端版本失败: %w", err)
		}
		recommendedToolMajor = strings.TrimSpace(serverVersion)
	} else {
		serverVersion, err = probePostgresVersion(strings.TrimSpace(instance.Hostname), instance.Port, strings.TrimSpace(string(instance.User)), strings.TrimSpace(string(instance.Password)), sourceDatabase, sslMode)
		if err != nil {
			return nil, fmt.Errorf("探测 %s 服务端版本失败: %w", logicalDBLabel(string(instance.DbType)), err)
		}
		if isKingbaseDBType(string(instance.DbType)) {
			// 金仓版本形如 V009R001C010，没有 PG major 概念，客户端与内核同包发布，
			// 不做「建议安装 PostgreSQL N 客户端」的提示，避免把用户引到错的地方。
			recommendedToolMajor = ""
		} else {
			serverMajor, _ = postgresMajorTag(serverVersion)
			recommendedToolMajor = firstNonEmpty(serverMajor, serverVersion)
		}
	}
	repositoryPath := strings.TrimSpace(outputDir)
	if string(storageTarget.Type) == "LOCAL_SERVER" {
		repositoryPath = joinLogicalPrecheckRepositoryPath(storageTarget.BasePath, outputDir)
	}
	return &backupTaskPrecheckContext{
		C:               c,
		Form:            form,
		Instance:        instance,
		ExecutorNode:    executorNode,
		StorageTarget:   storageTarget,
		DriverType:      "LOGICAL",
		BackupType:      backupType,
		LogicalConfig:   logicalCfg,
		MaxRuntimeHours: precheckMaxRuntimeHours(form),
		OutputDir:       outputDir,
		ServerVersion:   serverVersion,
		ServerMajor:     serverMajor,
		SourceDatabase:  sourceDatabase,
		SSLMode:         sslMode,
		RepoPath:        repositoryPath,
		Resolved: backupTaskPrecheckResolved{
			TaskID:               taskID,
			SourceHost:           strings.TrimSpace(instance.Hostname),
			SourcePort:           instance.Port,
			SourceDatabase:       sourceDatabase,
			ExecutorNodeID:       executorNode.ID,
			ExecutorHost:         strings.TrimSpace(executorNode.Host),
			RepositoryPath:       repositoryPath,
			ServerVersion:        serverVersion,
			ServerMajor:          serverMajor,
			RecommendedToolMajor: recommendedToolMajor,
		},
	}, nil
}

func buildMySQLPhysicalBackupTaskPrecheckContext(form *backupForms.PrecheckBackupTaskForm, c *gin.Context, instance *commonModels.InsightInstances, backupType string, driverConfigJSON datatypes.JSON) (*backupTaskPrecheckContext, error) {
	driverCfg, err := loadMySQLPhysicalDriverConfig(driverConfigJSON)
	if err != nil {
		return nil, err
	}
	if _, err := normalizeObjectSpec(string(instance.DbType), "XTRABACKUP", form.ObjectMode, "", backupType, form.ObjectSpec); err != nil {
		return nil, err
	}
	if _, err := normalizeScheduleDays(form.ScheduleDays); err != nil {
		return nil, err
	}
	if _, err := normalizeScheduleTime(form.ScheduleTime); err != nil {
		return nil, err
	}
	outputDir, err := resolveBackupOutputBaseDir(normalizeBackupTaskOutputDir(form.OutputDir))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(outputDir) == "" {
		return nil, fmt.Errorf("备份任务必须配置备份目录（output_dir）")
	}
	storageTargetID, _, err := resolveAndValidateStorageTargetForTask("XTRABACKUP", form.StorageTargetID)
	if err != nil {
		return nil, err
	}
	storageTarget, err := loadStorageTargetByID(storageTargetID)
	if err != nil {
		return nil, err
	}
	node, ensureErr := ensureLogicalBackupDBHostNode(c, instance)
	if ensureErr != nil {
		return nil, ensureErr
	}
	taskID, err := normalizeProvidedBackupTaskID(form.TaskID)
	if err != nil {
		return nil, err
	}
	if taskID == "" {
		taskID, err = generateBackupTaskID()
		if err != nil {
			return nil, err
		}
	}
	serverVersion, err := probeMySQLVersion(strings.TrimSpace(instance.Hostname), instance.Port, strings.TrimSpace(string(instance.User)), strings.TrimSpace(string(instance.Password)))
	if err != nil {
		return nil, fmt.Errorf("探测 MySQL 服务端版本失败: %w", err)
	}
	recommendedToolMajor, err := mysqlPhysicalSupportedVersionFamily(serverVersion)
	if err != nil {
		return nil, err
	}
	repositoryPath := strings.TrimSpace(outputDir)
	if strings.EqualFold(strings.TrimSpace(string(storageTarget.Type)), backupModels.StorageTargetTypeLocalServer) {
		repositoryPath = joinLogicalPrecheckRepositoryPath(storageTarget.BasePath, outputDir)
	}
	return &backupTaskPrecheckContext{
		C:                   c,
		Form:                form,
		Instance:            instance,
		ExecutorNode:        node,
		StorageTarget:       storageTarget,
		DriverType:          "XTRABACKUP",
		BackupType:          backupType,
		MySQLPhysicalConfig: driverCfg,
		MaxRuntimeHours:     precheckMaxRuntimeHours(form),
		OutputDir:           outputDir,
		ServerVersion:       serverVersion,
		SourceDatabase:      "mysql",
		RepoPath:            repositoryPath,
		Resolved: backupTaskPrecheckResolved{
			TaskID:               taskID,
			SourceHost:           strings.TrimSpace(instance.Hostname),
			SourcePort:           instance.Port,
			SourceDatabase:       "mysql",
			ExecutorNodeID:       node.ID,
			ExecutorHost:         strings.TrimSpace(node.Host),
			RepositoryPath:       repositoryPath,
			ServerVersion:        serverVersion,
			RecommendedToolMajor: recommendedToolMajor,
		},
	}, nil
}

func logBackupTaskPrecheckConfigStage(c *gin.Context, stage string, form *backupForms.PrecheckBackupTaskForm, dbType string, driverType string, rawDriverConfig map[string]any, normalized datatypes.JSON, logicalCfg any, stageErr error) {
	fields := logrus.Fields{
		"request_id":         precheckRequestID(c),
		"stage":              strings.TrimSpace(stage),
		"task_id":            strings.TrimSpace(form.TaskID),
		"source_instance_id": form.SourceInstanceID,
		"db_type":            strings.TrimSpace(dbType),
		"driver_type":        strings.TrimSpace(driverType),
		"backup_type":        strings.TrimSpace(form.BackupType),
		"raw_driver_config":  rawDriverConfig,
		"normalized_config":  strings.TrimSpace(string(normalized)),
	}
	if rawDriverConfig != nil {
		fields["raw_driver_config_keys"] = strings.Join(sortedMapKeys(rawDriverConfig), ",")
		fields["raw_os_user"] = strings.TrimSpace(fmt.Sprintf("%v", rawDriverConfig["os_user"]))
		fields["raw_process_user"] = strings.TrimSpace(fmt.Sprintf("%v", rawDriverConfig["process_user"]))
		fields["raw_db_os_user"] = strings.TrimSpace(fmt.Sprintf("%v", rawDriverConfig["db_os_user"]))
		fields["raw_tool_path"] = strings.TrimSpace(fmt.Sprintf("%v", rawDriverConfig["tool_path"]))
		fields["raw_dump_path"] = strings.TrimSpace(fmt.Sprintf("%v", rawDriverConfig["dump_path"]))
		fields["raw_pg_dump_path"] = strings.TrimSpace(fmt.Sprintf("%v", rawDriverConfig["pg_dump_path"]))
	}
	if cfg, ok := logicalCfg.(logicalBackupDriverConfig); ok {
		fields["logical_process_max"] = cfg.ProcessMax
		fields["logical_os_user"] = strings.TrimSpace(cfg.OSUser)
		fields["logical_tool_path"] = strings.TrimSpace(cfg.ToolPath)
	}
	if stageErr != nil {
		fields["error"] = stageErr.Error()
		if global.App.Log != nil {
			global.App.Log.WithFields(fields).Warn("备份预检查驱动配置链路")
		}
		writePrecheckDebugStdout("precheck_driver_config", fields)
		return
	}
	if global.App.Log != nil {
		global.App.Log.WithFields(fields).Info("备份预检查驱动配置链路")
	}
	writePrecheckDebugStdout("precheck_driver_config", fields)
}

func sortedMapKeys(data map[string]any) []string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func writePrecheckDebugStdout(tag string, fields logrus.Fields) {
	if len(fields) == 0 {
		return
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		fmt.Printf("[goinsight-debug] tag=%s marshal_error=%v\n", strings.TrimSpace(tag), err)
		return
	}
	fmt.Printf("[goinsight-debug] tag=%s payload=%s\n", strings.TrimSpace(tag), string(payload))
}

func executeLogicalBackupTaskPrecheck(ctx *backupTaskPrecheckContext) {
	addLogicalDatabaseVersionInfoItem(ctx)
	checkLogicalExecutorNode(ctx)
	checkLogicalSourceSSHConnectivity(ctx)
	checkLogicalDatabaseRuntime(ctx)
	checkLogicalBackupTool(ctx)
	checkLogicalStorageTarget(ctx)
	checkLogicalExecutorSSHPassDependency(ctx)
}

func executeMySQLPhysicalBackupTaskPrecheck(ctx *backupTaskPrecheckContext) {
	addLogicalDatabaseVersionInfoItem(ctx)
	checkMySQLPhysicalExecutorNode(ctx)
	checkLogicalSourceSSHConnectivity(ctx)
	checkMySQLPhysicalRuntime(ctx)
	checkMySQLPhysicalStorageEngine(ctx)
	checkMySQLPhysicalPrivileges(ctx)
	checkMySQLPhysicalBackupTool(ctx)
	checkMySQLPhysicalDataDirAccess(ctx)
	checkMySQLPhysicalInnoDBDataAccess(ctx)
	checkMySQLPhysicalRedoAccess(ctx)
	checkMySQLPhysicalUndoAccess(ctx)
	checkLogicalStorageTarget(ctx)
	checkLogicalExecutorSSHPassDependency(ctx)
}

func checkMySQLPhysicalExecutorNode(ctx *backupTaskPrecheckContext) {
	if ctx.ExecutorNode == nil || ctx.ExecutorNode.ID == 0 {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("executor_node", "执行环境节点校验", "未解析到 MySQL 物理备份执行节点", nil))
		return
	}
	if repairedNode, repairResult := tryAutoRepairBackupNode(ctx.C, ctx.ExecutorNode, ctx.Instance, "MySQL 物理备份执行节点"); repairResult != nil {
		if repairResult.Attempted {
			if repairResult.Recovered {
				ctx.Items = append(ctx.Items, newBackupTaskPrecheckInfo("executor_node_auto_repair", "执行环境自动修复", repairResult.Summary, repairResult.Detail))
			} else {
				ctx.Items = append(ctx.Items, newBackupTaskPrecheckWarning("executor_node_auto_repair", "执行环境自动修复", repairResult.Summary, repairResult.Detail))
			}
		}
		if repairedNode != nil {
			ctx.ExecutorNode = repairedNode
		}
	}
	detail := map[string]any{
		"node_id": ctx.ExecutorNode.ID,
		"host":    strings.TrimSpace(ctx.ExecutorNode.Host),
		"status":  strings.TrimSpace(string(ctx.ExecutorNode.Status)),
	}
	if !strings.EqualFold(strings.TrimSpace(string(ctx.ExecutorNode.Status)), "ONLINE") {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("executor_node", "执行环境节点校验", "MySQL 物理备份执行节点当前不在线，请先恢复数据库主机 Agent", detail))
		return
	}
	if !strings.EqualFold(strings.TrimSpace(ctx.ExecutorNode.Host), strings.TrimSpace(ctx.Instance.Hostname)) {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("executor_topology", "执行拓扑校验", "MySQL 物理备份要求执行节点与数据源主机一致", detail))
		return
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "executor_node",
		Label:    "执行环境节点校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  "MySQL 物理备份执行节点在线，且与数据源主机一致。",
		Detail:   detail,
	})
}

func checkMySQLPhysicalRuntime(ctx *backupTaskPrecheckContext) {
	detail := map[string]any{
		"configured_host": strings.TrimSpace(ctx.Instance.Hostname),
		"configured_port": ctx.Instance.Port,
	}
	vars, err := probeMySQLPhysicalVariables(strings.TrimSpace(ctx.Instance.Hostname), ctx.Instance.Port, strings.TrimSpace(string(ctx.Instance.User)), strings.TrimSpace(string(ctx.Instance.Password)))
	if err != nil {
		detail["error"] = err.Error()
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_runtime", "MySQL 运行时校验", "无法读取 MySQL 物理备份关键参数", detail))
		return
	}
	detail["datadir"] = strings.TrimSpace(vars["datadir"])
	detail["innodb_log_group_home_dir"] = strings.TrimSpace(vars["innodb_log_group_home_dir"])
	detail["innodb_undo_directory"] = strings.TrimSpace(vars["innodb_undo_directory"])
	detail["innodb_data_home_dir"] = strings.TrimSpace(vars["innodb_data_home_dir"])
	detail["innodb_directories"] = splitMySQLPhysicalConfiguredDirs(strings.TrimSpace(vars["innodb_directories"]))
	detail["redo_base_dir"] = strings.TrimSpace(vars["redo_base_dir"])
	detail["undo_base_dir"] = strings.TrimSpace(vars["undo_base_dir"])
	detail["data_home_base_dir"] = strings.TrimSpace(vars["data_home_base_dir"])
	detail["innodb_directories_list"] = splitMySQLPhysicalConfiguredDirs(strings.TrimSpace(vars["innodb_directories_list"]))
	detail["log_bin"] = strings.TrimSpace(vars["log_bin"])
	detail["version_comment"] = strings.TrimSpace(vars["version_comment"])
	detail["server_uuid"] = strings.TrimSpace(vars["server_uuid"])
	message := fmt.Sprintf("已识别 MySQL datadir=%s，data_home_base_dir=%s，redo_base_dir=%s，undo_base_dir=%s，log_bin=%s。", displayOrDefault(strings.TrimSpace(vars["datadir"]), "-"), displayOrDefault(strings.TrimSpace(vars["data_home_base_dir"]), "-"), displayOrDefault(strings.TrimSpace(vars["redo_base_dir"]), "-"), displayOrDefault(strings.TrimSpace(vars["undo_base_dir"]), "-"), displayOrDefault(strings.TrimSpace(vars["log_bin"]), "-"))
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "mysql_runtime",
		Label:    "MySQL 运行时校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  message,
		Detail:   detail,
	})
}

func checkMySQLPhysicalBackupTool(ctx *backupTaskPrecheckContext) {
	targetVersion, versionErr := mysqlPhysicalSupportedVersionFamily(ctx.ServerVersion)
	if versionErr != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_physical_tool", "MySQL 物理备份工具校验", versionErr.Error(), map[string]any{
			"server_version": strings.TrimSpace(ctx.ServerVersion),
		}))
		return
	}
	plan, err := buildMySQLPhysicalExecutionPlanForPrecheck(ctx)
	if err != nil {
		effectiveOSUser := strings.TrimSpace(ctx.MySQLPhysicalConfig.OSUser)
		if effectiveOSUser == "" {
			if detected, _, _, detectedErr := resolveRemoteExecutionOSUserForPrecheck(ctx, ""); detectedErr == nil {
				effectiveOSUser = detected
			}
		}
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_physical_tool", "MySQL 物理备份工具校验", "数据库主机未就绪可用的 xtrabackup 工具", map[string]any{
			"server_version":          strings.TrimSpace(ctx.ServerVersion),
			"target_xtrabackup_major": targetVersion,
			"tool_path":               strings.TrimSpace(ctx.MySQLPhysicalConfig.ToolPath),
			"os_user":                 effectiveOSUser,
			"process_user":            strings.TrimSpace(ctx.RuntimeInspect["PROCESS_USER"]),
			"error":                   err.Error(),
		}))
		return
	}
	if ctx.ExecutionPlan == nil {
		ctx.ExecutionPlan = &unifiedBackupExecutionPlan{
			DBType:        strings.TrimSpace(string(ctx.Instance.DbType)),
			DriverType:    strings.TrimSpace(ctx.DriverType),
			BackupType:    strings.TrimSpace(ctx.BackupType),
			ServerVersion: strings.TrimSpace(ctx.ServerVersion),
		}
	}
	ctx.ExecutionPlan.MySQLPhysical = plan
	message := "数据库主机已检测到可用的 xtrabackup 工具。"
	if strings.TrimSpace(plan.AutoProvisionSummary) != "" {
		message = "数据库主机原先缺少 xtrabackup，系统已自动下发匹配版本并完成复检。"
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "mysql_physical_tool",
		Label:    "MySQL 物理备份工具校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  message,
		Detail: map[string]any{
			"tool_path":               strings.TrimSpace(plan.Tool.Path),
			"tool_source":             strings.TrimSpace(plan.Tool.Source),
			"server_version":          strings.TrimSpace(ctx.ServerVersion),
			"target_xtrabackup_major": targetVersion,
			"process_max":             ctx.MySQLPhysicalConfig.ProcessMax,
			"os_user":                 strings.TrimSpace(plan.EffectiveOSUser),
			"os_user_source":          strings.TrimSpace(plan.OSUserSource),
			"tool_check_user":         strings.TrimSpace(plan.ToolCheckUser),
			"auto_provision_summary":  strings.TrimSpace(plan.AutoProvisionSummary),
		},
	})
}

func ensureMySQLPhysicalAdmissionForPrecheck(ctx *backupTaskPrecheckContext) (*mysqlPhysicalAdmissionPlan, error) {
	if ctx == nil || ctx.Instance == nil {
		return nil, fmt.Errorf("实例信息不存在")
	}
	if ctx.ExecutionPlan != nil && ctx.ExecutionPlan.MySQLPhysical != nil && ctx.ExecutionPlan.MySQLPhysical.Admission != nil {
		return ctx.ExecutionPlan.MySQLPhysical.Admission, nil
	}
	runtimeVars, err := probeMySQLPhysicalVariables(strings.TrimSpace(ctx.Instance.Hostname), ctx.Instance.Port, strings.TrimSpace(string(ctx.Instance.User)), strings.TrimSpace(string(ctx.Instance.Password)))
	if err != nil {
		return nil, err
	}
	admission, err := probeMySQLPhysicalAdmissionFromDB(strings.TrimSpace(ctx.Instance.Hostname), ctx.Instance.Port, strings.TrimSpace(string(ctx.Instance.User)), strings.TrimSpace(string(ctx.Instance.Password)), ctx.ServerVersion)
	if err != nil {
		return nil, err
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
	effectiveOSUser, _, _, err := resolveRemoteExecutionOSUserForPrecheck(ctx, ctx.MySQLPhysicalConfig.OSUser)
	if err != nil {
		return nil, err
	}
	fileAccess, err := probeMySQLPhysicalPathsRemote(
		runner,
		effectiveOSUser,
		strings.TrimSpace(runtimeVars["datadir"]),
		strings.TrimSpace(runtimeVars["redo_base_dir"]),
		strings.TrimSpace(runtimeVars["undo_base_dir"]),
		strings.TrimSpace(runtimeVars["data_home_base_dir"]),
		splitMySQLPhysicalConfiguredDirs(strings.TrimSpace(runtimeVars["innodb_directories_list"])),
	)
	if err != nil {
		return nil, err
	}
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
	if ctx.ExecutionPlan == nil {
		ctx.ExecutionPlan = &unifiedBackupExecutionPlan{
			DBType:        strings.TrimSpace(string(ctx.Instance.DbType)),
			DriverType:    strings.TrimSpace(ctx.DriverType),
			BackupType:    strings.TrimSpace(ctx.BackupType),
			ServerVersion: strings.TrimSpace(ctx.ServerVersion),
		}
	}
	if ctx.ExecutionPlan.MySQLPhysical == nil {
		ctx.ExecutionPlan.MySQLPhysical = &mysqlPhysicalExecutionPlan{
			EffectiveOSUser: effectiveOSUser,
			RuntimeInspect:  ctx.RuntimeInspect,
			RuntimeVars:     runtimeVars,
		}
	}
	ctx.ExecutionPlan.MySQLPhysical.Admission = admission
	if ctx.ExecutionPlan != nil && ctx.ExecutionPlan.MySQLPhysical != nil {
		ctx.ExecutionPlan.MySQLPhysical.RuntimeVars = runtimeVars
	}
	return admission, nil
}

func checkMySQLPhysicalStorageEngine(ctx *backupTaskPrecheckContext) {
	admission, err := ensureMySQLPhysicalAdmissionForPrecheck(ctx)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_physical_engine", "MySQL 存储引擎校验", "无法确认实例是否全部为 InnoDB 表", map[string]any{
			"error":          err.Error(),
			"server_version": strings.TrimSpace(ctx.ServerVersion),
		}))
		return
	}
	if admission.NonInnoDBTableCount > 0 {
		detail := map[string]any{
			"non_innodb_table_count": admission.NonInnoDBTableCount,
			"sample_tables":          admission.NonInnoDBTables,
		}
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_physical_engine", "MySQL 存储引擎校验", "当前 MySQL 物理备份仅支持 InnoDB 表，请先迁移或排除非 InnoDB 表。", detail))
		return
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "mysql_physical_engine",
		Label:    "MySQL 存储引擎校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  "实例中的业务表均为 InnoDB，可执行 XtraBackup 物理备份。",
		Detail: map[string]any{
			"non_innodb_table_count": 0,
		},
	})
}

func checkMySQLPhysicalPrivileges(ctx *backupTaskPrecheckContext) {
	admission, err := ensureMySQLPhysicalAdmissionForPrecheck(ctx)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_physical_privileges", "MySQL 备份权限校验", "无法确认物理备份账号权限", map[string]any{
			"error":          err.Error(),
			"server_version": strings.TrimSpace(ctx.ServerVersion),
		}))
		return
	}
	detail := map[string]any{
		"check_user":          strings.TrimSpace(admission.PrivilegeCheckUser),
		"active_roles":        admission.ActiveRoles,
		"required_privileges": admission.RequiredPrivileges,
		"granted_privileges":  admission.GrantedPrivileges,
		"privilege_notes":     admission.PrivilegeNotes,
	}
	if len(admission.MissingPrivileges) > 0 {
		detail["missing_privileges"] = admission.MissingPrivileges
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_physical_privileges", "MySQL 备份权限校验", "备份账号缺少 MySQL 物理备份所需权限。", detail))
		return
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "mysql_physical_privileges",
		Label:    "MySQL 备份权限校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  "备份账号已具备 MySQL 物理备份所需权限。",
		Detail:   detail,
	})
}

func checkMySQLPhysicalDataDirAccess(ctx *backupTaskPrecheckContext) {
	admission, err := ensureMySQLPhysicalAdmissionForPrecheck(ctx)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_physical_datadir", "MySQL datadir 可读性校验", "无法确认 datadir 读取权限", map[string]any{
			"error": err.Error(),
		}))
		return
	}
	detail := map[string]any{
		"datadir":    strings.TrimSpace(admission.Datadir),
		"check_user": strings.TrimSpace(admission.DatadirCheckUser),
		"readable":   admission.DatadirReadable,
	}
	if !admission.DatadirReadable {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_physical_datadir", "MySQL datadir 可读性校验", "执行用户无法读取 MySQL datadir。", detail))
		return
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "mysql_physical_datadir",
		Label:    "MySQL datadir 可读性校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  "执行用户已具备 datadir 读取权限。",
		Detail:   detail,
	})
}

func checkMySQLPhysicalRedoAccess(ctx *backupTaskPrecheckContext) {
	admission, err := ensureMySQLPhysicalAdmissionForPrecheck(ctx)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_physical_redo", "MySQL redo 可读性校验", "无法确认 redo 文件读取权限", map[string]any{
			"error": err.Error(),
		}))
		return
	}
	detail := map[string]any{
		"redo_search_base":          strings.TrimSpace(admission.RedoSearchBase),
		"check_user":                strings.TrimSpace(admission.RedoCheckUser),
		"redo_log_paths":            admission.RedoLogPaths,
		"unreadable_redo_log_paths": admission.UnreadableRedoLogPaths,
	}
	if len(admission.RedoLogPaths) == 0 || len(admission.UnreadableRedoLogPaths) > 0 {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_physical_redo", "MySQL redo 可读性校验", "执行用户无法完整读取 redo 文件。", detail))
		return
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "mysql_physical_redo",
		Label:    "MySQL redo 可读性校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  "执行用户已具备 redo 文件读取权限。",
		Detail:   detail,
	})
}

func checkMySQLPhysicalInnoDBDataAccess(ctx *backupTaskPrecheckContext) {
	admission, err := ensureMySQLPhysicalAdmissionForPrecheck(ctx)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_physical_innodb_data", "MySQL InnoDB 数据目录校验", "无法确认 InnoDB 物理数据目录读取权限", map[string]any{
			"error": err.Error(),
		}))
		return
	}
	detail := map[string]any{
		"data_home_search_base": strings.TrimSpace(admission.DataHomeSearchBase),
		"check_user":            strings.TrimSpace(admission.DataCheckUser),
		"data_dir_paths":        admission.DataDirPaths,
		"unreadable_data_dirs":  admission.UnreadableDataDirPaths,
		"data_file_paths":       admission.DataFilePaths,
		"unreadable_data_files": admission.UnreadableDataFiles,
	}
	if strings.TrimSpace(admission.DataHomeSearchBase) != "" || len(admission.DataDirPaths) > 0 || len(admission.UnreadableDataDirPaths) > 0 {
		detail["data_home_dir_readable"] = admission.DataHomeDirReadable
	}
	if len(admission.UnreadableDataDirPaths) > 0 || len(admission.UnreadableDataFiles) > 0 {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_physical_innodb_data", "MySQL InnoDB 数据目录校验", "执行用户无法完整读取 InnoDB 物理数据目录或数据文件。", detail))
		return
	}
	message := "执行用户已具备 InnoDB 物理数据目录读取权限。"
	if strings.TrimSpace(admission.DataHomeSearchBase) == "" && len(admission.DataDirPaths) == 0 {
		message = "当前实例未配置独立 InnoDB 数据目录，按默认 datadir 路径继续。"
	} else if len(admission.DataFilePaths) == 0 {
		message = "已校验 InnoDB 物理数据目录可读，当前未检测到额外 InnoDB 数据文件，按实例现状继续。"
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "mysql_physical_innodb_data",
		Label:    "MySQL InnoDB 数据目录校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  message,
		Detail:   detail,
	})
}

func checkMySQLPhysicalUndoAccess(ctx *backupTaskPrecheckContext) {
	admission, err := ensureMySQLPhysicalAdmissionForPrecheck(ctx)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_physical_undo", "MySQL undo 可读性校验", "无法确认 undo 文件读取权限", map[string]any{
			"error": err.Error(),
		}))
		return
	}
	detail := map[string]any{
		"undo_search_base":          strings.TrimSpace(admission.UndoSearchBase),
		"check_user":                strings.TrimSpace(admission.UndoCheckUser),
		"undo_dir_readable":         admission.UndoDirReadable,
		"undo_log_paths":            admission.UndoLogPaths,
		"unreadable_undo_log_paths": admission.UnreadableUndoLogPaths,
	}
	if strings.TrimSpace(admission.UndoSearchBase) != "" && (!admission.UndoDirReadable || len(admission.UnreadableUndoLogPaths) > 0) {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("mysql_physical_undo", "MySQL undo 可读性校验", "执行用户无法完整读取 undo 目录或 undo 文件。", detail))
		return
	}
	message := "执行用户已具备 undo 目录读取权限。"
	if len(admission.UndoLogPaths) == 0 {
		message = "已校验 undo 目录可读，当前未检测到独立 undo 文件，按实例现状继续。"
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "mysql_physical_undo",
		Label:    "MySQL undo 可读性校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  message,
		Detail:   detail,
	})
}

func buildMySQLPhysicalExecutorRemoteConfig(ctx *backupTaskPrecheckContext) (*backupNodeRemoteConfig, error) {
	if ctx == nil || ctx.Instance == nil {
		return nil, fmt.Errorf("实例信息不存在")
	}
	sshUser, sshPassword, sshPrivateKey, sshPort, ok, reason := resolveSSHCredentialsFromInstance(ctx.Instance)
	cfg := &backupNodeRemoteConfig{
		Host:     strings.TrimSpace(ctx.Instance.Hostname),
		Port:     sshPort,
		ToolsDir: defaultBackupNodeToolsDir(""),
	}
	if ctx.ExecutorNode != nil {
		if host := strings.TrimSpace(ctx.ExecutorNode.Host); host != "" {
			cfg.Host = host
		}
		if ctx.ExecutorNode.Port > 0 {
			cfg.Port = ctx.ExecutorNode.Port
		}
		if toolsDir := strings.TrimSpace(ctx.ExecutorNode.ToolsDir); toolsDir != "" {
			cfg.ToolsDir = toolsDir
		}
		if user := strings.TrimSpace(ctx.ExecutorNode.SSHUser); user != "" {
			cfg.SSHUser = defaultBackupNodeSSHUser(user)
		}
		if password := strings.TrimSpace(ctx.ExecutorNode.SSHPassword); password != "" {
			cfg.SSHPassword = password
		}
		if privateKey := strings.TrimSpace(ctx.ExecutorNode.SSHPrivateKey); privateKey != "" {
			cfg.SSHPrivateKey = privateKey
		}
	}
	if strings.TrimSpace(cfg.SSHUser) == "" {
		if !ok {
			return nil, fmt.Errorf("缺少 SSH 凭据: %s", reason)
		}
		cfg.SSHUser = defaultBackupNodeSSHUser(sshUser)
	}
	if strings.TrimSpace(cfg.SSHPassword) == "" && strings.TrimSpace(cfg.SSHPrivateKey) == "" {
		if !ok {
			return nil, fmt.Errorf("缺少 SSH 凭据: %s", reason)
		}
		cfg.SSHPassword = strings.TrimSpace(sshPassword)
		cfg.SSHPrivateKey = strings.TrimSpace(sshPrivateKey)
	}
	if cfg.Port <= 0 {
		if ok && sshPort > 0 {
			cfg.Port = sshPort
		} else {
			cfg.Port = 22
		}
	}
	cfg.ToolsDir = defaultBackupNodeToolsDir(cfg.ToolsDir)
	return cfg, nil
}

func addLogicalDatabaseVersionInfoItem(ctx *backupTaskPrecheckContext) {
	label := "数据库版本识别"
	message := fmt.Sprintf("当前 %s 版本为 %s，已纳入本次逻辑备份预检查。", strings.TrimSpace(string(ctx.Instance.DbType)), strings.TrimSpace(ctx.ServerVersion))
	detail := map[string]any{
		"db_type":        strings.TrimSpace(string(ctx.Instance.DbType)),
		"server_version": strings.TrimSpace(ctx.ServerVersion),
	}
	if strings.EqualFold(string(ctx.Instance.DbType), "PostgreSQL") && strings.TrimSpace(ctx.ServerMajor) != "" {
		detail["server_major"] = strings.TrimSpace(ctx.ServerMajor)
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "database_version_info",
		Label:    label,
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  message,
		Detail:   detail,
	})
}

func checkLogicalExecutorNode(ctx *backupTaskPrecheckContext) {
	if ctx.ExecutorNode == nil || ctx.ExecutorNode.ID == 0 {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("executor_node", "执行环境节点校验", "未解析到逻辑备份执行节点", nil))
		return
	}
	if repairedNode, repairResult := tryAutoRepairBackupNode(ctx.C, ctx.ExecutorNode, ctx.Instance, "逻辑备份执行节点"); repairResult != nil {
		if repairResult.Attempted {
			if repairResult.Recovered {
				ctx.Items = append(ctx.Items, newBackupTaskPrecheckInfo("executor_node_auto_repair", "执行环境自动修复", repairResult.Summary, repairResult.Detail))
			} else {
				ctx.Items = append(ctx.Items, newBackupTaskPrecheckWarning("executor_node_auto_repair", "执行环境自动修复", repairResult.Summary, repairResult.Detail))
			}
		}
		if repairedNode != nil {
			ctx.ExecutorNode = repairedNode
		}
	}
	detail := map[string]any{
		"node_id": ctx.ExecutorNode.ID,
		"host":    strings.TrimSpace(ctx.ExecutorNode.Host),
		"status":  strings.TrimSpace(string(ctx.ExecutorNode.Status)),
	}
	if !strings.EqualFold(strings.TrimSpace(string(ctx.ExecutorNode.Status)), "ONLINE") {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("executor_node", "执行环境节点校验", "逻辑备份执行节点当前不在线，且系统自动修复/重建后仍未恢复，请先处理执行环境后再启动备份", detail))
		return
	}
	if !strings.EqualFold(strings.TrimSpace(ctx.ExecutorNode.Host), strings.TrimSpace(ctx.Instance.Hostname)) {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("executor_topology", "执行拓扑校验", "逻辑备份要求执行节点与数据源主机一致，避免远端全量拉库", detail))
		return
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "executor_node",
		Label:    "执行环境节点校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  "逻辑备份执行节点在线，且与数据源主机一致。",
		Detail:   detail,
	})
}

func checkLogicalSourceSSHConnectivity(ctx *backupTaskPrecheckContext) {
	sshUser, sshPassword, sshPrivateKey, sshPort, ok, reason := resolveSSHCredentialsFromInstance(ctx.Instance)
	if !ok {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("source_ssh", "数据库主机 SSH 校验", "未配置可用的数据库主机 SSH 凭据", map[string]any{"reason": reason}))
		return
	}
	runner, err := newSSHRunner(&backupNodeRemoteConfig{
		Host:          strings.TrimSpace(ctx.Instance.Hostname),
		Port:          sshPort,
		SSHUser:       sshUser,
		SSHPassword:   sshPassword,
		SSHPrivateKey: sshPrivateKey,
	})
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("source_ssh", "数据库主机 SSH 校验", "无法连接数据库主机执行备份前环境检查", map[string]any{
			"host":  strings.TrimSpace(ctx.Instance.Hostname),
			"port":  sshPort,
			"user":  sshUser,
			"error": err.Error(),
		}))
		return
	}
	defer runner.Close()
	output, runErr := runner.Run("set -e\nexport PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:$PATH\nWHOAMI=$(id -un 2>/dev/null || true)\nHOSTNAME=$(hostname 2>/dev/null || true)\necho WHOAMI=${WHOAMI}\necho HOSTNAME=${HOSTNAME}")
	if runErr != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("source_ssh", "数据库主机 SSH 校验", "数据库主机 SSH 连通正常，但执行基础命令失败", map[string]any{
			"error":  runErr.Error(),
			"output": strings.TrimSpace(output),
		}))
		return
	}
	parsed := parseKeyValueOutput(output)
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "source_ssh",
		Label:    "数据库主机 SSH 校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  "数据库主机 SSH 连通正常，已可执行逻辑备份前置探测。",
		Detail: map[string]any{
			"host":     strings.TrimSpace(ctx.Instance.Hostname),
			"port":     sshPort,
			"user":     sshUser,
			"whoami":   strings.TrimSpace(parsed["WHOAMI"]),
			"hostname": strings.TrimSpace(parsed["HOSTNAME"]),
		},
	})
}

func checkLogicalDatabaseRuntime(ctx *backupTaskPrecheckContext) {
	if ctx != nil && (strings.TrimSpace(ctx.LogicalConfig.OSUser) != "" || strings.TrimSpace(ctx.LogicalConfig.ToolPath) != "") {
		detail := map[string]any{
			"configured_host": strings.TrimSpace(ctx.Instance.Hostname),
			"configured_port": ctx.Instance.Port,
			"db_type":         strings.TrimSpace(string(ctx.Instance.DbType)),
			"os_user":         strings.TrimSpace(ctx.LogicalConfig.OSUser),
			"tool_path":       strings.TrimSpace(ctx.LogicalConfig.ToolPath),
		}
		ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
			ItemKey:  "database_runtime",
			Label:    "数据库运行时校验",
			Severity: backupTaskPrecheckSeverityInfo,
			Blocking: true,
			Passed:   true,
			Message:  "已使用用户手工填写的操作系统用户和 dump 路径，跳过自动识别数据库监听进程。",
			Detail:   detail,
		})
		return
	}
	parsed, output, err := ensureLogicalSourceRuntimeInspection(ctx)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("database_runtime", "数据库运行时校验", "无法识别数据库实例监听进程及其操作系统用户", map[string]any{
			"configured_host": strings.TrimSpace(ctx.Instance.Hostname),
			"configured_port": ctx.Instance.Port,
			"db_type":         strings.TrimSpace(string(ctx.Instance.DbType)),
			"error":           err.Error(),
			"output":          strings.TrimSpace(output),
		}))
		return
	}
	processUser := strings.TrimSpace(parsed["PROCESS_USER"])
	processName := strings.TrimSpace(parsed["PROCESS_NAME"])
	toolUser := strings.TrimSpace(parsed["TOOL_USER"])
	pgDumpPath := strings.TrimSpace(parsed["PG_DUMP"])
	mysqlDumpPath := strings.TrimSpace(parsed["MYSQLDUMP"])
	mariaDBDumpPath := strings.TrimSpace(parsed["MARIADB_DUMP"])
	detail := map[string]any{
		"configured_host": strings.TrimSpace(ctx.Instance.Hostname),
		"configured_port": ctx.Instance.Port,
		"listen_addr":     strings.TrimSpace(parsed["LISTEN_ADDR"]),
		"process_exe":     strings.TrimSpace(parsed["PROCESS_EXE"]),
		"process_bin_dir": strings.TrimSpace(parsed["PROCESS_BIN_DIR"]),
		"process_pid":     strings.TrimSpace(parsed["PROCESS_PID"]),
		"process_name":    processName,
		"process_user":    processUser,
		"tool_check_user": toolUser,
	}
	if strings.EqualFold(string(ctx.Instance.DbType), "MySQL") {
		detail["has_mysqldump"] = mysqlDumpPath != ""
		detail["mysqldump_path"] = mysqlDumpPath
		detail["has_mariadb_dump"] = mariaDBDumpPath != ""
		detail["mariadb_dump_path"] = mariaDBDumpPath
	} else {
		detail["has_pg_dump"] = pgDumpPath != ""
		detail["pg_dump_path"] = pgDumpPath
	}
	message := fmt.Sprintf("已根据 %s:%d 识别到数据库监听进程 %s（user=%s），并在该用户上下文完成逻辑备份工具探测。", strings.TrimSpace(ctx.Instance.Hostname), ctx.Instance.Port, displayOrDefault(processName, "unknown"), displayOrDefault(processUser, "unknown"))
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "database_runtime",
		Label:    "数据库运行时校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  message,
		Detail:   detail,
	})
}

func checkLogicalBackupTool(ctx *backupTaskPrecheckContext) {
	plan, err := buildLogicalExecutionPlanForPrecheck(ctx)
	if err != nil {
		effectiveCfg := ctx.LogicalConfig
		runtimeInspect := ctx.RuntimeInspect
		if plan != nil {
			effectiveCfg.OSUser = firstNonEmpty(strings.TrimSpace(plan.EffectiveOSUser), strings.TrimSpace(effectiveCfg.OSUser))
			runtimeInspect = plan.RuntimeInspect
		}
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("logical_backup_tool", "逻辑备份工具校验", "数据库主机未检测到可用的逻辑备份工具", buildLogicalBackupToolFailureDetail(ctx, effectiveCfg, runtimeInspect, nil, err)))
		return
	}
	if ctx.ExecutionPlan == nil {
		ctx.ExecutionPlan = &unifiedBackupExecutionPlan{
			DBType:        strings.TrimSpace(string(ctx.Instance.DbType)),
			DriverType:    strings.TrimSpace(ctx.DriverType),
			BackupType:    strings.TrimSpace(ctx.BackupType),
			ServerVersion: strings.TrimSpace(ctx.ServerVersion),
		}
	}
	ctx.ExecutionPlan.Logical = plan
	if plan.RuntimeInspect != nil {
		ctx.RuntimeInspect = plan.RuntimeInspect
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "logical_backup_tool",
		Label:    "逻辑备份工具校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  "数据库主机已按统一执行计划解析到可用的逻辑备份工具。",
		Detail: map[string]any{
			"tool_path":          strings.TrimSpace(plan.Tool.Path),
			"version_line":       strings.TrimSpace(plan.VersionLine),
			"db_type":            strings.TrimSpace(string(ctx.Instance.DbType)),
			"server_version":     strings.TrimSpace(ctx.ServerVersion),
			"process_user":       strings.TrimSpace(ctx.RuntimeInspect["PROCESS_USER"]),
			"tool_check_user":    strings.TrimSpace(plan.ToolCheckUser),
			"effective_os_user":  strings.TrimSpace(plan.EffectiveOSUser),
			"configured_host":    strings.TrimSpace(ctx.Instance.Hostname),
			"configured_port":    ctx.Instance.Port,
			"listening_address":  strings.TrimSpace(ctx.RuntimeInspect["LISTEN_ADDR"]),
			"process_exe":        strings.TrimSpace(ctx.RuntimeInspect["PROCESS_EXE"]),
			"process_bin_dir":    strings.TrimSpace(ctx.RuntimeInspect["PROCESS_BIN_DIR"]),
			"process_name":       strings.TrimSpace(ctx.RuntimeInspect["PROCESS_NAME"]),
			"process_identifier": strings.TrimSpace(ctx.RuntimeInspect["PROCESS_PID"]),
		},
	})
}

func checkLogicalBackupToolWithUserConfig(ctx *backupTaskPrecheckContext) {
	checkLogicalBackupTool(ctx)
}

func buildLogicalBackupToolFailureDetail(ctx *backupTaskPrecheckContext, cfg logicalBackupDriverConfig, runtimeInspect map[string]string, resolution *logicalToolResolution, err error) map[string]any {
	detail := map[string]any{
		"db_type":         strings.TrimSpace(string(ctx.Instance.DbType)),
		"configured_host": strings.TrimSpace(ctx.Instance.Hostname),
		"configured_port": ctx.Instance.Port,
		"os_user":         strings.TrimSpace(cfg.OSUser),
		"tool_path":       strings.TrimSpace(cfg.ToolPath),
	}
	if runtimeInspect != nil {
		detail["process_user"] = strings.TrimSpace(runtimeInspect["PROCESS_USER"])
		detail["process_name"] = strings.TrimSpace(runtimeInspect["PROCESS_NAME"])
		detail["process_exe"] = strings.TrimSpace(runtimeInspect["PROCESS_EXE"])
		detail["process_bin_dir"] = strings.TrimSpace(runtimeInspect["PROCESS_BIN_DIR"])
		detail["listening_address"] = strings.TrimSpace(runtimeInspect["LISTEN_ADDR"])
	}
	if resolution != nil {
		if resolution.Spec != nil {
			detail["resolved_tool_path"] = strings.TrimSpace(resolution.Spec.Path)
		}
		detail["tool_error"] = strings.TrimSpace(resolution.ToolError)
		detail["tool_check_user"] = strings.TrimSpace(resolution.ToolUser)
		detail["version_line"] = strings.TrimSpace(resolution.VersionLine)
		for _, key := range []string{"DEBUG_CURRENT_USER", "DEBUG_TARGET_TOOL_USER", "DEBUG_TARGET_USER_EXISTS", "DEBUG_TARGET_HOME", "DEBUG_REMOTE_SCRIPT"} {
			if value := strings.TrimSpace(resolution.Parsed[key]); value != "" {
				detail[strings.ToLower(key)] = value
			}
		}
		if raw := strings.TrimSpace(resolution.RawOutput); raw != "" {
			detail["raw_output"] = raw
		}
	}
	if err != nil {
		detail["error"] = err.Error()
	}
	return detail
}

func inspectLogicalBackupToolWithUserConfig(ctx *backupTaskPrecheckContext) (map[string]string, string, error) {
	if ctx == nil || ctx.Instance == nil {
		return nil, "", fmt.Errorf("实例信息不存在")
	}
	sshUser, sshPassword, sshPrivateKey, sshPort, ok, reason := resolveSSHCredentialsFromInstance(ctx.Instance)
	if !ok {
		return nil, "", fmt.Errorf("缺少 SSH 凭据: %s", reason)
	}
	cfg := &backupNodeRemoteConfig{
		Host:          strings.TrimSpace(ctx.Instance.Hostname),
		Port:          sshPort,
		SSHUser:       sshUser,
		SSHPassword:   sshPassword,
		SSHPrivateKey: sshPrivateKey,
	}
	runner, err := newSSHRunner(cfg)
	if err != nil {
		return nil, "", err
	}
	defer runner.Close()
	debugTraceID := logicalToolDebugTraceID(ctx)
	debugPhaseFile := logicalToolDebugPhaseFile(debugTraceID)
	script := buildLogicalToolInspectionScriptWithUserConfig(ctx)
	output, runErr := runner.Run(script)
	if runErr != nil && !strings.EqualFold(strings.TrimSpace(cfg.SSHUser), "root") {
		output, runErr = runner.Run(wrapRemoteRootScript(cfg, script))
	}
	phaseTraceFallback := ""
	if strings.TrimSpace(output) == "" || !strings.Contains(output, "DEBUG_PHASE_TRACE=") {
		phaseTraceFallback, _ = readRemoteDebugTraceFile(runner, debugPhaseFile)
	}
	// #region debug-point B:logical-tool-user-config-remote-output
	debugReportPgdumpPathCheck("B", "task_precheck.go:inspectLogicalBackupToolWithUserConfig", "remote logical backup tool inspection finished", map[string]any{
		"ssh_user":             strings.TrimSpace(cfg.SSHUser),
		"host":                 strings.TrimSpace(cfg.Host),
		"port":                 cfg.Port,
		"trace_id":             debugTraceID,
		"phase_file":           debugPhaseFile,
		"phase_trace_fallback": strings.TrimSpace(phaseTraceFallback),
		"run_error": func() string {
			if runErr != nil {
				return runErr.Error()
			}
			return ""
		}(),
		"raw_output":  output,
		"script_hint": firstNonEmpty(strings.TrimSpace(ctx.LogicalConfig.ToolPath), "<empty>"),
		"os_user":     strings.TrimSpace(ctx.LogicalConfig.OSUser),
	})
	// #endregion
	parsedOutput := parseKeyValueOutput(output)
	logLogicalToolInspectionDebug(ctx, cfg, parsedOutput, runErr)
	if runErr != nil {
		return nil, output, runErr
	}
	return parsedOutput, output, nil
}

func logLogicalToolInspectionDebug(ctx *backupTaskPrecheckContext, cfg *backupNodeRemoteConfig, parsed map[string]string, runErr error) {
	fields := logrus.Fields{
		"request_id":             logicalToolDebugRequestID(ctx),
		"debug_trace_id":         strings.TrimSpace(parsed["DEBUG_TRACE_ID"]),
		"debug_phase_file":       strings.TrimSpace(parsed["DEBUG_PHASE_FILE"]),
		"debug_phase_trace":      strings.TrimSpace(parsed["DEBUG_PHASE_TRACE"]),
		"configured_host":        logicalToolDebugHost(ctx, cfg),
		"configured_port":        logicalToolDebugPort(ctx, cfg),
		"db_type":                logicalToolDebugDBType(ctx),
		"os_user":                logicalToolDebugOSUser(ctx),
		"input_tool_path":        logicalToolDebugInputToolPath(ctx),
		"tool_error":             strings.TrimSpace(parsed["TOOL_ERROR"]),
		"debug_tool_status":      strings.TrimSpace(parsed["DEBUG_TOOL_STATUS"]),
		"debug_tool_output_len":  strings.TrimSpace(parsed["DEBUG_TOOL_OUTPUT_LEN"]),
		"debug_current_user":     strings.TrimSpace(parsed["DEBUG_CURRENT_USER"]),
		"debug_target_tool_user": strings.TrimSpace(parsed["DEBUG_TARGET_TOOL_USER"]),
		"debug_target_home":      strings.TrimSpace(parsed["DEBUG_TARGET_HOME"]),
		"resolved_tool_path":     strings.TrimSpace(parsed["TOOL_PATH"]),
		"tool_user":              strings.TrimSpace(parsed["TOOL_USER"]),
		"version_line":           strings.TrimSpace(parsed["VERSION_LINE"]),
	}
	if cfg != nil {
		fields["ssh_user"] = strings.TrimSpace(cfg.SSHUser)
		fields["ssh_port"] = cfg.Port
	}
	if runErr != nil {
		fields["run_error"] = runErr.Error()
	}
	if global.App.Log != nil {
		global.App.Log.WithFields(fields).Info("逻辑备份工具校验调试")
	}
	writePrecheckDebugStdout("logical_tool_inspection", fields)
}

func precheckRequestID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return requestid.Get(c)
}

func logicalToolDebugRequestID(ctx *backupTaskPrecheckContext) string {
	if ctx == nil || ctx.C == nil {
		return ""
	}
	return requestid.Get(ctx.C)
}

func logicalToolDebugHost(ctx *backupTaskPrecheckContext, cfg *backupNodeRemoteConfig) string {
	if ctx != nil && ctx.Instance != nil && strings.TrimSpace(ctx.Instance.Hostname) != "" {
		return strings.TrimSpace(ctx.Instance.Hostname)
	}
	if cfg != nil {
		return strings.TrimSpace(cfg.Host)
	}
	return ""
}

func logicalToolDebugPort(ctx *backupTaskPrecheckContext, cfg *backupNodeRemoteConfig) int {
	if ctx != nil && ctx.Instance != nil && ctx.Instance.Port > 0 {
		return ctx.Instance.Port
	}
	if cfg != nil {
		return cfg.Port
	}
	return 0
}

func logicalToolDebugDBType(ctx *backupTaskPrecheckContext) string {
	if ctx == nil || ctx.Instance == nil {
		return ""
	}
	return strings.TrimSpace(string(ctx.Instance.DbType))
}

func logicalToolDebugOSUser(ctx *backupTaskPrecheckContext) string {
	if ctx == nil {
		return ""
	}
	return strings.TrimSpace(ctx.LogicalConfig.OSUser)
}

func logicalToolDebugInputToolPath(ctx *backupTaskPrecheckContext) string {
	if ctx == nil {
		return ""
	}
	return strings.TrimSpace(ctx.LogicalConfig.ToolPath)
}

func logicalToolDebugTraceID(ctx *backupTaskPrecheckContext) string {
	requestID := logicalToolDebugRequestID(ctx)
	requestID = strings.TrimSpace(requestID)
	if requestID != "" {
		return sanitizePathSegment(requestID, "logical-tool-debug")
	}
	return fmt.Sprintf("logical-tool-%d", time.Now().UnixNano())
}

func logicalToolDebugPhaseFile(traceID string) string {
	traceID = sanitizePathSegment(strings.TrimSpace(traceID), "logical-tool-debug")
	return "/tmp/goinsight-logical-tool-phase-" + traceID + ".log"
}

func readRemoteDebugTraceFile(runner *sshRunner, path string) (string, error) {
	if runner == nil || strings.TrimSpace(path) == "" {
		return "", nil
	}
	script := strings.Join([]string{
		"set +e",
		"TRACE_FILE=" + shellQuote(path),
		`if [ -f "$TRACE_FILE" ]; then cat "$TRACE_FILE"; fi`,
	}, "\n")
	output, err := runner.Run(script)
	return strings.TrimSpace(output), err
}

func buildLogicalToolInspectionScriptWithUserConfig(ctx *backupTaskPrecheckContext) string {
	dbType := strings.TrimSpace(string(ctx.Instance.DbType))
	osUser := strings.TrimSpace(ctx.LogicalConfig.OSUser)
	toolPath := strings.TrimSpace(ctx.LogicalConfig.ToolPath)
	serverVersion := strings.TrimSpace(ctx.ServerVersion)
	debugTraceID := logicalToolDebugTraceID(ctx)
	debugPhaseFile := logicalToolDebugPhaseFile(debugTraceID)
	toolProbeScript := ""
	if strings.EqualFold(dbType, "MySQL") {
		version := strings.TrimSpace(versionPrefixRe.FindString(serverVersion))
		major := ""
		if version != "" {
			parts := strings.Split(version, ".")
			if len(parts) > 0 {
				major = strings.TrimSpace(parts[0])
			}
		}
		if toolPath != "" {
			toolProbeScript = strings.Join([]string{
				"INPUT_TOOL_PATH=" + shellQuote(toolPath),
				"TOOL_PATH=",
				`if [ "${INPUT_TOOL_PATH#/}" != "$INPUT_TOOL_PATH" ]; then`,
				`  if [ ! -x "$INPUT_TOOL_PATH" ]; then exit 41; fi`,
				`  TOOL_PATH="$INPUT_TOOL_PATH"`,
				`elif [ "${INPUT_TOOL_PATH#*/}" != "$INPUT_TOOL_PATH" ]; then`,
				`  if [ ! -x "$INPUT_TOOL_PATH" ]; then exit 41; fi`,
				`  TOOL_PATH="$INPUT_TOOL_PATH"`,
				`else`,
				"  PROCESS_BIN_DIR=${PROCESS_BIN_DIR:-}",
				"  MYSQL_MAJOR=" + shellQuote(major),
				"  MYSQL_VERSION=" + shellQuote(version),
				`  TOOL_NAME="$INPUT_TOOL_PATH"`,
				strings.ReplaceAll(buildMySQLToolLookupSnippet(), "\n", "\n  "),
				`  if [ -z "$TOOL_PATH" ]; then exit 41; fi`,
				`fi`,
				`VERSION_LINE=$("$TOOL_PATH" --version 2>/dev/null | head -n1 || true)`,
				"WHOAMI=$(id -un 2>/dev/null || true)",
				"echo TOOL_USER=${WHOAMI}",
				"echo TOOL_PATH=${TOOL_PATH}",
				"echo VERSION_LINE=${VERSION_LINE}",
			}, "\n")
		} else {
			toolProbeScript = strings.Join([]string{
				"PROCESS_BIN_DIR=${PROCESS_BIN_DIR:-}",
				"MYSQL_MAJOR=" + shellQuote(major),
				"MYSQL_VERSION=" + shellQuote(version),
				"TOOL_NAME=mariadb-dump",
				buildMySQLToolLookupSnippet(),
				`if [ -z "$TOOL_PATH" ]; then TOOL_NAME=mysqldump; ` + strings.ReplaceAll(buildMySQLToolLookupSnippet(), "\n", "; ") + ` fi`,
				`if [ -z "$TOOL_PATH" ]; then exit 41; fi`,
				"WHOAMI=$(id -un 2>/dev/null || true)",
				"echo TOOL_USER=${WHOAMI}",
				"echo TOOL_PATH=${TOOL_PATH}",
				"echo VERSION_LINE=${VERSION_LINE}",
			}, "\n")
		}
	} else {
		if toolPath != "" {
			toolProbeScript = strings.Join([]string{
				"INPUT_TOOL_PATH=" + shellQuote(toolPath),
				"TOOL_PATH=",
				`if [ "${INPUT_TOOL_PATH#/}" != "$INPUT_TOOL_PATH" ]; then`,
				`  if [ ! -x "$INPUT_TOOL_PATH" ]; then exit 41; fi`,
				`  TOOL_PATH="$INPUT_TOOL_PATH"`,
				`elif [ "${INPUT_TOOL_PATH#*/}" != "$INPUT_TOOL_PATH" ]; then`,
				`  if [ ! -x "$INPUT_TOOL_PATH" ]; then exit 41; fi`,
				`  TOOL_PATH="$INPUT_TOOL_PATH"`,
				`else`,
				"  PROCESS_BIN_DIR=${PROCESS_BIN_DIR:-}",
				"  PG_MAJOR=" + shellQuote(strings.TrimSpace(postgresExactClientVersion(serverVersion))),
				`  TOOL_NAME="$INPUT_TOOL_PATH"`,
				strings.ReplaceAll(buildPostgresToolLookupSnippet(), "\n", "\n  "),
				`  if [ -z "$TOOL_PATH" ]; then exit 41; fi`,
				`fi`,
				`VERSION_LINE=$("$TOOL_PATH" --version 2>/dev/null | head -n1 || true)`,
				"WHOAMI=$(id -un 2>/dev/null || true)",
				"echo TOOL_USER=${WHOAMI}",
				"echo TOOL_PATH=${TOOL_PATH}",
				"echo VERSION_LINE=${VERSION_LINE}",
			}, "\n")
		} else {
			toolProbeScript = strings.Join([]string{
				"PROCESS_BIN_DIR=${PROCESS_BIN_DIR:-}",
				"PG_MAJOR=" + shellQuote(strings.TrimSpace(postgresExactClientVersion(serverVersion))),
				"TOOL_NAME=pg_dump",
				buildPostgresToolLookupSnippet(),
				`if [ -z "$TOOL_PATH" ]; then exit 41; fi`,
				"WHOAMI=$(id -un 2>/dev/null || true)",
				"echo TOOL_USER=${WHOAMI}",
				"echo TOOL_PATH=${TOOL_PATH}",
				"echo VERSION_LINE=${VERSION_LINE}",
			}, "\n")
		}
	}
	targetRunScript := strings.Join([]string{
		"set -e",
		"# #region debug-point C:logical-tool-target-shell-entry",
		`printf '%s\n' "target:start" >>"$PHASE_FILE" 2>/dev/null || true`,
		`printf '%s\n' "target:user=$(id -un 2>/dev/null || true)" >>"$PHASE_FILE" 2>/dev/null || true`,
		"# #endregion",
		shellProfileInitSnippet(),
		"# #region debug-point C:logical-tool-target-after-profile",
		`printf '%s\n' "target:after-profile" >>"$PHASE_FILE" 2>/dev/null || true`,
		"# #endregion",
		"# #region debug-point C:logical-tool-probe-start",
		`printf '%s\n' "target:tool-probe:start" >>"$PHASE_FILE" 2>/dev/null || true`,
		"# #endregion",
		toolProbeScript,
		"# #region debug-point C:logical-tool-probe-end",
		`printf '%s\n' "target:tool-probe:end" >>"$PHASE_FILE" 2>/dev/null || true`,
		"# #endregion",
	}, "\n")
	return strings.Join([]string{
		"set -e",
		"CONFIG_TOOL_USER=" + shellQuote(osUser),
		"DEBUG_TRACE_ID=" + shellQuote(debugTraceID),
		"PHASE_FILE=" + shellQuote(debugPhaseFile),
		"TARGET_SCRIPT_FILE=/tmp/goinsight-logical-tool-target-${DEBUG_TRACE_ID}.sh",
		"TOOL_OUTPUT_FILE=/tmp/goinsight-logical-tool-output-${DEBUG_TRACE_ID}.log",
		"CURRENT_USER=$(id -un 2>/dev/null || true)",
		"TARGET_TOOL_USER=${CONFIG_TOOL_USER:-$CURRENT_USER}",
		`TARGET_HOME=$(awk -F: -v u="$TARGET_TOOL_USER" '$1==u{print $6; exit}' /etc/passwd 2>/dev/null || true)`,
		"# #region debug-point C:logical-tool-parent-entry",
		`rm -f "$PHASE_FILE"`,
		`rm -f "$TARGET_SCRIPT_FILE" "$TOOL_OUTPUT_FILE"`,
		`append_phase() { printf '%s\n' "$1" >>"$PHASE_FILE" 2>/dev/null || true; }`,
		`append_phase "parent:start current=${CURRENT_USER} target=${TARGET_TOOL_USER}"`,
		`export PHASE_FILE`,
		"# #endregion",
		`cat <<'__GOINSIGHT_LOGICAL_TOOL__' >"$TARGET_SCRIPT_FILE"`,
		targetRunScript,
		"__GOINSIGHT_LOGICAL_TOOL__",
		`chmod 644 "$TARGET_SCRIPT_FILE"`,
		"set +e",
		`append_phase "run_as_target_user:enter current=${CURRENT_USER} target=${TARGET_TOOL_USER}"`,
		"if [ \"$CURRENT_USER\" = \"$TARGET_TOOL_USER\" ]; then",
		`  append_phase "run_as_target_user:direct-shell"`,
		"  if [ -n \"$TARGET_HOME\" ]; then export HOME=\"$TARGET_HOME\"; fi",
		`  env HOME="${HOME:-$TARGET_HOME}" PHASE_FILE="$PHASE_FILE" /bin/sh "$TARGET_SCRIPT_FILE" >"$TOOL_OUTPUT_FILE" 2>&1`,
		"  TOOL_STATUS=$?",
		"elif command -v sudo >/dev/null 2>&1; then",
		`  append_phase "run_as_target_user:sudo-shell home=${TARGET_HOME}"`,
		"  if [ -n \"$TARGET_HOME\" ]; then",
		`    sudo -n -H -u "$TARGET_TOOL_USER" -- env HOME="$TARGET_HOME" PHASE_FILE="$PHASE_FILE" /bin/sh "$TARGET_SCRIPT_FILE" >"$TOOL_OUTPUT_FILE" 2>&1`,
		"  else",
		`    sudo -n -H -u "$TARGET_TOOL_USER" -- env PHASE_FILE="$PHASE_FILE" /bin/sh "$TARGET_SCRIPT_FILE" >"$TOOL_OUTPUT_FILE" 2>&1`,
		"  fi",
		"  TOOL_STATUS=$?",
		"else",
		`  append_phase "run_as_target_user:sudo-missing"`,
		`  printf '%s\n' "sudo_missing" >"$TOOL_OUTPUT_FILE"`,
		"  TOOL_STATUS=61",
		"fi",
		"set -e",
		"TOOL_OUTPUT=$(cat \"$TOOL_OUTPUT_FILE\" 2>/dev/null || true)",
		"rm -f \"$TOOL_OUTPUT_FILE\" \"$TARGET_SCRIPT_FILE\"",
		`DEBUG_PHASE_TRACE=$(tr '\n' '|' < "$PHASE_FILE" 2>/dev/null | sed 's/|$//' || true)`,
		"echo DEBUG_TRACE_ID=${DEBUG_TRACE_ID}",
		"echo DEBUG_PHASE_FILE=${PHASE_FILE}",
		"echo DEBUG_PHASE_TRACE=${DEBUG_PHASE_TRACE}",
		"echo DEBUG_CURRENT_USER=${CURRENT_USER}",
		"echo DEBUG_TARGET_TOOL_USER=${TARGET_TOOL_USER}",
		"echo DEBUG_TARGET_HOME=${TARGET_HOME}",
		"echo DEBUG_TOOL_STATUS=${TOOL_STATUS}",
		"echo DEBUG_TOOL_OUTPUT_LEN=$(printf '%s' \"$TOOL_OUTPUT\" | wc -c | awk '{print $1}')",
		"if [ \"$TOOL_STATUS\" -ne 0 ]; then echo TOOL_ERROR=$(printf '%s' \"$TOOL_OUTPUT\" | tr '\\n' ';'); exit \"$TOOL_STATUS\"; fi",
		"printf '%s\\n' \"$TOOL_OUTPUT\"",
	}, "\n")
}

func ensureLogicalSourceRuntimeInspection(ctx *backupTaskPrecheckContext) (map[string]string, string, error) {
	if ctx == nil {
		return nil, "", fmt.Errorf("预检查上下文不存在")
	}
	if ctx.RuntimeInspect != nil {
		return ctx.RuntimeInspect, "", nil
	}
	sshUser, sshPassword, sshPrivateKey, sshPort, ok, reason := resolveSSHCredentialsFromInstance(ctx.Instance)
	if !ok {
		return nil, "", fmt.Errorf("缺少 SSH 凭据，无法检查逻辑备份工具: %s", reason)
	}
	cfg := &backupNodeRemoteConfig{
		Host:          strings.TrimSpace(ctx.Instance.Hostname),
		Port:          sshPort,
		SSHUser:       sshUser,
		SSHPassword:   sshPassword,
		SSHPrivateKey: sshPrivateKey,
	}
	runner, err := newSSHRunner(cfg)
	if err != nil {
		return nil, "", fmt.Errorf("无法连接数据库主机识别逻辑备份工具: %w", err)
	}
	defer runner.Close()
	script := buildLogicalSourceRuntimeInspectionScript(ctx)
	output, runErr := runner.Run(script)
	if runErr != nil {
		needsRootRetry := !strings.EqualFold(strings.TrimSpace(cfg.SSHUser), "root")
		if needsRootRetry {
			rootOutput, rootErr := runner.Run(wrapRemoteRootScript(cfg, script))
			if rootErr == nil {
				parsed := parseKeyValueOutput(rootOutput)
				ctx.RuntimeInspect = parsed
				return parsed, rootOutput, nil
			}
			return nil, rootOutput, rootErr
		}
		return nil, output, runErr
	}
	parsed := parseKeyValueOutput(output)
	ctx.RuntimeInspect = parsed
	return parsed, output, nil
}

func buildLogicalSourceRuntimeInspectionScript(ctx *backupTaskPrecheckContext) string {
	configuredHost := ""
	configuredPort := 0
	dbType := ""
	toolProbeScript := ""
	serverVersion := ""
	if ctx != nil && ctx.Instance != nil {
		configuredHost = strings.TrimSpace(ctx.Instance.Hostname)
		configuredPort = ctx.Instance.Port
		dbType = strings.TrimSpace(string(ctx.Instance.DbType))
	}
	if ctx != nil {
		serverVersion = strings.TrimSpace(ctx.ServerVersion)
	}
	if strings.EqualFold(dbType, "MySQL") {
		version := strings.TrimSpace(versionPrefixRe.FindString(serverVersion))
		major := ""
		if version != "" {
			parts := strings.Split(version, ".")
			if len(parts) > 0 {
				major = strings.TrimSpace(parts[0])
			}
		}
		toolProbeScript = strings.Join([]string{
			"PROCESS_BIN_DIR=${PROCESS_BIN_DIR:-}",
			"MYSQL_MAJOR=" + shellQuote(major),
			"MYSQL_VERSION=" + shellQuote(version),
			"TOOL_NAME=mariadb-dump",
			buildMySQLToolLookupSnippet(),
			"MARIADB_DUMP=${TOOL_PATH}",
			"MARIADB_DUMP_VERSION=${VERSION_LINE}",
			"TOOL_NAME=mysqldump",
			buildMySQLToolLookupSnippet(),
			"MYSQLDUMP=${TOOL_PATH}",
			"MYSQLDUMP_VERSION=${VERSION_LINE}",
			"WHOAMI=$(id -un 2>/dev/null || true)",
			"echo TOOL_USER=${WHOAMI}",
			"echo MYSQLDUMP=${MYSQLDUMP}",
			"echo MYSQLDUMP_VERSION=${MYSQLDUMP_VERSION}",
			"echo MARIADB_DUMP=${MARIADB_DUMP}",
			"echo MARIADB_DUMP_VERSION=${MARIADB_DUMP_VERSION}",
		}, "\n")
	} else {
		toolProbeScript = strings.Join([]string{
			"PROCESS_BIN_DIR=${PROCESS_BIN_DIR:-}",
			"PG_MAJOR=" + shellQuote(strings.TrimSpace(postgresExactClientVersion(serverVersion))),
			"TOOL_NAME=pg_dump",
			buildPostgresToolLookupSnippet(),
			"PG_DUMP=${TOOL_PATH}",
			"PG_DUMP_VERSION=${VERSION_LINE}",
			"WHOAMI=$(id -un 2>/dev/null || true)",
			"echo TOOL_USER=${WHOAMI}",
			"echo PG_DUMP=${PG_DUMP}",
			"echo PG_DUMP_VERSION=${PG_DUMP_VERSION}",
		}, "\n")
	}
	return strings.Join([]string{
		"set -e",
		"export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$PATH",
		"CONFIG_HOST=" + shellQuote(configuredHost),
		"DB_TYPE=" + shellQuote(dbType),
		fmt.Sprintf("TARGET_PORT=%d", configuredPort),
		"CURRENT_USER=$(id -un 2>/dev/null || true)",
		"if ! command -v ss >/dev/null 2>&1; then echo ERROR=ss_not_found; exit 51; fi",
		"SS_LINE=$(ss -lntpH 2>/dev/null | awk -v port=\"$TARGET_PORT\" -v host=\"$CONFIG_HOST\" '\n" +
			"function match_port(v) { return (v ~ (\":\" port \"$\")) || (v ~ (\"\\\\]:\" port \"$\")) }\n" +
			"function addr_only(v, a) { a=v; sub(/:[^:]*$/, \"\", a); gsub(/^\\[/, \"\", a); gsub(/\\]$/, \"\", a); return a }\n" +
			"{\n" +
			"  if (!match_port($4)) next;\n" +
			"  if (fallback == \"\") fallback = $0;\n" +
			"  addr = addr_only($4);\n" +
			"  if (host == \"\" || addr == host || addr == \"*\" || addr == \"0.0.0.0\" || addr == \"::\") { print; exit }\n" +
			"}\n" +
			"END { if (fallback != \"\") print fallback }')",
		"if [ -z \"$SS_LINE\" ]; then echo ERROR=port_process_not_found; exit 52; fi",
		"LISTEN_ADDR=$(printf '%s\\n' \"$SS_LINE\" | awk '{print $4}')",
		"PROCESS_PID=$(printf '%s\\n' \"$SS_LINE\" | sed -n 's/.*pid=\\([0-9][0-9]*\\).*/\\1/p' | head -n1)",
		"if [ -z \"$PROCESS_PID\" ]; then echo ERROR=pid_not_visible; echo SS_LINE=$SS_LINE; exit 53; fi",
		"PROCESS_USER=$(ps -o user= -p \"$PROCESS_PID\" 2>/dev/null | head -n1 | xargs || true)",
		"PROCESS_NAME=$(ps -o comm= -p \"$PROCESS_PID\" 2>/dev/null | head -n1 | xargs || true)",
		"PROCESS_EXE=$(readlink -f \"/proc/$PROCESS_PID/exe\" 2>/dev/null || true)",
		"PROCESS_BIN_DIR=$(dirname \"$PROCESS_EXE\" 2>/dev/null || true)",
		"if [ -z \"$PROCESS_USER\" ]; then echo ERROR=process_user_not_found; exit 54; fi",
		"export PROCESS_BIN_DIR",
		"SHELL_INIT=" + shellQuote(shellProfileInitSnippet()),
		`PROCESS_HOME=$(awk -F: -v u="$PROCESS_USER" '$1==u{print $6; exit}' /etc/passwd 2>/dev/null || true)`,
		"run_as_process_user() {",
		"  CMD=\"$1\"",
		"  if [ \"$CURRENT_USER\" = \"$PROCESS_USER\" ]; then if [ -n \"$PROCESS_HOME\" ]; then export HOME=\"$PROCESS_HOME\"; fi; env HOME=\"${HOME:-$PROCESS_HOME}\" SHELL_INIT=\"$SHELL_INIT\" CMD=\"$CMD\" /bin/sh -lc 'eval \"$SHELL_INIT\"; eval \"$CMD\"'; return $?; fi",
		"  if command -v sudo >/dev/null 2>&1; then if [ -n \"$PROCESS_HOME\" ]; then sudo -n -H -u \"$PROCESS_USER\" -- env HOME=\"$PROCESS_HOME\" SHELL_INIT=\"$SHELL_INIT\" CMD=\"$CMD\" /bin/sh -lc 'eval \"$SHELL_INIT\"; eval \"$CMD\"'; else sudo -n -H -u \"$PROCESS_USER\" -- env SHELL_INIT=\"$SHELL_INIT\" CMD=\"$CMD\" /bin/sh -lc 'eval \"$SHELL_INIT\"; eval \"$CMD\"'; fi; return $?; fi",
		"  echo sudo_missing >&2",
		"  return 61",
		"}",
		"set +e",
		"TOOL_OUTPUT=$(run_as_process_user " + shellQuote(toolProbeScript) + " 2>&1)",
		"TOOL_STATUS=$?",
		"set -e",
		"if [ \"$TOOL_STATUS\" -ne 0 ]; then echo ERROR=tool_probe_failed; echo TOOL_OUTPUT=$(printf '%s' \"$TOOL_OUTPUT\" | tr '\\n' ';'); exit \"$TOOL_STATUS\"; fi",
		"echo CONFIG_HOST=$CONFIG_HOST",
		"echo TARGET_PORT=$TARGET_PORT",
		"echo DB_TYPE=$DB_TYPE",
		"echo LISTEN_ADDR=$LISTEN_ADDR",
		"echo PROCESS_PID=$PROCESS_PID",
		"echo PROCESS_USER=$PROCESS_USER",
		"echo PROCESS_NAME=$PROCESS_NAME",
		"echo PROCESS_EXE=$PROCESS_EXE",
		"echo PROCESS_BIN_DIR=$PROCESS_BIN_DIR",
		"printf '%s\\n' \"$TOOL_OUTPUT\"",
	}, "\n")
}

func inspectLogicalSourceToolVersion(ctx *backupTaskPrecheckContext, toolPath string, processUser string) (map[string]string, error) {
	if ctx == nil || ctx.Instance == nil {
		return nil, fmt.Errorf("实例信息不存在")
	}
	toolPath = strings.TrimSpace(toolPath)
	processUser = strings.TrimSpace(processUser)
	if toolPath == "" || processUser == "" {
		return nil, fmt.Errorf("缺少工具路径或进程用户")
	}
	sshUser, sshPassword, sshPrivateKey, sshPort, ok, reason := resolveSSHCredentialsFromInstance(ctx.Instance)
	if !ok {
		return nil, fmt.Errorf("缺少 SSH 凭据: %s", reason)
	}
	cfg := &backupNodeRemoteConfig{
		Host:          strings.TrimSpace(ctx.Instance.Hostname),
		Port:          sshPort,
		SSHUser:       sshUser,
		SSHPassword:   sshPassword,
		SSHPrivateKey: sshPrivateKey,
	}
	runner, err := newSSHRunner(cfg)
	if err != nil {
		return nil, err
	}
	defer runner.Close()
	script := strings.Join([]string{
		"set -e",
		"export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$PATH",
		"PROCESS_USER=" + shellQuote(processUser),
		"TOOL_PATH=" + shellQuote(toolPath),
		"SHELL_INIT=" + shellQuote(shellProfileInitSnippet()),
		`PROCESS_HOME=$(awk -F: -v u="$PROCESS_USER" '$1==u{print $6; exit}' /etc/passwd 2>/dev/null || true)`,
		"run_as_process_user() {",
		"  CMD=\"$1\"",
		"  if [ \"$(id -un 2>/dev/null || true)\" = \"$PROCESS_USER\" ]; then if [ -n \"$PROCESS_HOME\" ]; then export HOME=\"$PROCESS_HOME\"; fi; env HOME=\"${HOME:-$PROCESS_HOME}\" SHELL_INIT=\"$SHELL_INIT\" CMD=\"$CMD\" /bin/sh -lc 'eval \"$SHELL_INIT\"; eval \"$CMD\"'; return $?; fi",
		"  if command -v sudo >/dev/null 2>&1; then if [ -n \"$PROCESS_HOME\" ]; then sudo -n -H -u \"$PROCESS_USER\" -- env HOME=\"$PROCESS_HOME\" SHELL_INIT=\"$SHELL_INIT\" CMD=\"$CMD\" /bin/sh -lc 'eval \"$SHELL_INIT\"; eval \"$CMD\"'; else sudo -n -H -u \"$PROCESS_USER\" -- env SHELL_INIT=\"$SHELL_INIT\" CMD=\"$CMD\" /bin/sh -lc 'eval \"$SHELL_INIT\"; eval \"$CMD\"'; fi; return $?; fi",
		"  echo sudo_missing >&2",
		"  return 61",
		"}",
		"set +e",
		"VERSION_LINE=$(run_as_process_user \"$TOOL_PATH --version 2>/dev/null | head -n1 | tr -d '\\r'\")",
		"STATUS=$?",
		"set -e",
		"if [ \"$STATUS\" -ne 0 ]; then exit \"$STATUS\"; fi",
		"echo VERSION_LINE=$VERSION_LINE",
	}, "\n")
	output, runErr := runner.Run(script)
	if runErr != nil && !strings.EqualFold(strings.TrimSpace(cfg.SSHUser), "root") {
		output, runErr = runner.Run(wrapRemoteRootScript(cfg, script))
	}
	if runErr != nil {
		return nil, runErr
	}
	return parseKeyValueOutput(output), nil
}

func checkLogicalStorageTarget(ctx *backupTaskPrecheckContext) {
	if ctx.StorageTarget == nil || ctx.StorageTarget.ID == 0 {
		return
	}
	result, err := (&PrecheckStorageTargetService{
		PrecheckStorageTargetForm: &backupForms.PrecheckStorageTargetForm{TargetID: ctx.StorageTarget.ID},
		ID:                        ctx.StorageTarget.ID,
	}).Run()
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("storage_target", "存储目标连通性校验", "执行存储目标预检查失败", map[string]any{"error": err.Error()}))
		return
	}
	detail := map[string]any{
		"target_id":       ctx.StorageTarget.ID,
		"type":            strings.TrimSpace(string(ctx.StorageTarget.Type)),
		"host":            strings.TrimSpace(ctx.StorageTarget.Host),
		"base_path":       strings.TrimSpace(ctx.StorageTarget.BasePath),
		"repository_path": strings.TrimSpace(ctx.Resolved.RepositoryPath),
	}
	if result.Capability != nil {
		detail["free_bytes"] = result.Capability.FreeBytes
		detail["fs_type"] = result.Capability.FsType
	}
	if result.OK {
		message := "存储目标连通正常，可用于接收逻辑备份输出。"
		if len(result.Warnings) > 0 {
			message = message + " " + strings.Join(result.Warnings, "；")
		}
		ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
			ItemKey:  "storage_target",
			Label:    "存储目标连通性校验",
			Severity: backupTaskPrecheckSeverityInfo,
			Blocking: true,
			Passed:   true,
			Message:  message,
			Detail:   detail,
		})
		return
	}
	detail["err_code"] = result.ErrCode
	detail["err_message"] = result.ErrMessage
	ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("storage_target", "存储目标连通性校验", firstNonEmpty(strings.TrimSpace(result.ErrMessage), "存储目标预检查未通过"), detail))
}

func checkLogicalExecutorSSHPassDependency(ctx *backupTaskPrecheckContext) {
	if ctx.StorageTarget == nil || ctx.StorageTarget.ID == 0 {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(string(ctx.StorageTarget.Type)), backupModels.StorageTargetTypeLocalServer) {
		return
	}
	sshPassword, sshPrivateKey, _, _, err := decryptStorageTargetCredentials(ctx.StorageTarget)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("executor_sshpass", "执行节点 sshpass 校验", "无法解密存储目标 SSH 凭据，无法判断是否需要 sshpass", map[string]any{
			"target_id": ctx.StorageTarget.ID,
			"error":     err.Error(),
		}))
		return
	}
	usePasswordOnly := strings.TrimSpace(sshPassword) != "" && strings.TrimSpace(sshPrivateKey) == ""
	if !usePasswordOnly {
		return
	}
	if ctx.ExecutorNode == nil || ctx.ExecutorNode.ID == 0 {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("executor_sshpass", "执行节点 sshpass 校验", "未解析到执行节点，无法检查 sshpass 依赖", map[string]any{
			"target_id": ctx.StorageTarget.ID,
		}))
		return
	}
	cfg, cfgErr := buildStoredBackupNodeRemoteConfig(ctx.ExecutorNode)
	if cfgErr != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("executor_sshpass", "执行节点 sshpass 校验", "无法读取执行节点 SSH 配置，无法检查 sshpass", map[string]any{
			"target_id": ctx.StorageTarget.ID,
			"node_id":   ctx.ExecutorNode.ID,
			"host":      strings.TrimSpace(ctx.ExecutorNode.Host),
			"error":     cfgErr.Error(),
		}))
		return
	}
	runner, dialErr := newSSHRunner(cfg)
	if dialErr != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("executor_sshpass", "执行节点 sshpass 校验", "无法连接执行节点检查 sshpass 依赖", map[string]any{
			"target_id": ctx.StorageTarget.ID,
			"node_id":   ctx.ExecutorNode.ID,
			"host":      strings.TrimSpace(ctx.ExecutorNode.Host),
			"error":     dialErr.Error(),
		}))
		return
	}
	defer runner.Close()
	output, runErr := runner.Run(strings.Join([]string{
		"set -e",
		"export PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:$PATH",
		"FOUND=$(command -v sshpass 2>/dev/null || true)",
		"if [ -z \"$FOUND\" ]; then exit 41; fi",
		"VERSION_LINE=$($FOUND -V 2>&1 | head -n1 | tr -d '\\r')",
		"echo FOUND=$FOUND",
		"echo VERSION_LINE=$VERSION_LINE",
	}, "\n"))
	if runErr != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("executor_sshpass", "执行节点 sshpass 校验", "当前存储目标使用 SSH 密码登录，执行节点必须安装 sshpass", map[string]any{
			"target_id":        ctx.StorageTarget.ID,
			"target_host":      strings.TrimSpace(ctx.StorageTarget.Host),
			"target_port":      ctx.StorageTarget.Port,
			"ssh_auth_mode":    "password",
			"ssh_private_key":  false,
			"executor_node_id": ctx.ExecutorNode.ID,
			"executor_host":    strings.TrimSpace(ctx.ExecutorNode.Host),
			"error":            runErr.Error(),
			"output":           strings.TrimSpace(output),
			"suggestion":       "请在执行节点安装 sshpass，或改为为存储目标配置 SSH 私钥登录后再重试。",
		}))
		return
	}
	parsed := parseKeyValueOutput(output)
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "executor_sshpass",
		Label:    "执行节点 sshpass 校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  "存储目标当前使用 SSH 密码登录，执行节点已检测到 sshpass。",
		Detail: map[string]any{
			"target_id":        ctx.StorageTarget.ID,
			"target_host":      strings.TrimSpace(ctx.StorageTarget.Host),
			"target_port":      ctx.StorageTarget.Port,
			"ssh_auth_mode":    "password",
			"executor_node_id": ctx.ExecutorNode.ID,
			"executor_host":    strings.TrimSpace(ctx.ExecutorNode.Host),
			"tool_path":        strings.TrimSpace(parsed["FOUND"]),
			"version_line":     strings.TrimSpace(parsed["VERSION_LINE"]),
		},
	})
}

func joinLogicalPrecheckRepositoryPath(basePath string, outputDir string) string {
	base := strings.TrimSpace(basePath)
	sub := strings.TrimSpace(outputDir)
	switch {
	case base == "" && sub == "":
		return ""
	case base == "":
		return sub
	case sub == "":
		return base
	default:
		return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(sub, "/")
	}
}

func persistBackupTaskPrecheckSnapshot(taskID string, result map[string]any) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || result == nil {
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return
	}
	status := "FAILED"
	if backupTaskPrecheckPassed(extractBackupTaskPrecheckItems(result["items"])) {
		status = "PASSED"
	}
	_ = global.App.DB.Model(&backupModels.InsightBackupTasks{}).
		Where("task_id = ?", taskID).
		Updates(map[string]any{
			"last_precheck_status": status,
			"last_precheck_at":     commonModels.LocalTime(nowLocalTime()),
			"last_precheck_result": datatypes.JSON(raw),
		}).Error
}

func extractBackupTaskPrecheckItems(value any) []backupTaskPrecheckItem {
	items, ok := value.([]backupTaskPrecheckItem)
	if ok {
		return items
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var decoded []backupTaskPrecheckItem
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil
	}
	return decoded
}

func buildPrecheckBackupTaskFormFromTask(task *backupModels.InsightBackupTasks) (*backupForms.PrecheckBackupTaskForm, error) {
	if task == nil {
		return nil, fmt.Errorf("备份任务不能为空")
	}
	var objectSpec map[string]any
	if len(task.ObjectSpec) > 0 {
		if err := json.Unmarshal(task.ObjectSpec, &objectSpec); err != nil {
			return nil, err
		}
	}
	var driverConfig map[string]any
	if len(task.DriverConfig) > 0 {
		if err := json.Unmarshal(task.DriverConfig, &driverConfig); err != nil {
			return nil, err
		}
	}
	return &backupForms.PrecheckBackupTaskForm{
		TaskID:           task.TaskID,
		Name:             task.Name,
		SourceInstanceID: task.SourceInstanceID,
		StorageTargetID:  task.StorageTargetID,
		ExecutorNodeID:   task.ExecutorNodeID,
		RepositoryNodeID: task.RepositoryNodeID,
		BackupType:       string(task.BackupType),
		ObjectMode:       string(task.ObjectMode),
		ObjectSpec:       objectSpec,
		OutputDir:        task.OutputDir,
		DriverType:       string(task.DriverType),
		DriverConfig:     driverConfig,
		ScheduleDays:     parseScheduleDays(task.ScheduleDays),
		ScheduleTime:     task.ScheduleTime,
		RetentionDays:    task.RetentionDays,
		Enabled:          boolPtr(task.Enabled),
	}, nil
}

func runBackupTaskPrecheckForTask(task *backupModels.InsightBackupTasks) (map[string]any, error) {
	form, err := buildPrecheckBackupTaskFormFromTask(task)
	if err != nil {
		return nil, err
	}
	result, err := runBackupTaskPrecheck(form, nil)
	if err != nil {
		return nil, err
	}
	if !backupTaskPrecheckResultPassed(result) {
		return nil, fmt.Errorf("%s", strings.TrimSpace(fmt.Sprintf("%v", result["summary"])))
	}
	return result, nil
}

func boolPtr(v bool) *bool {
	return &v
}

func backupTaskPrecheckResultPassed(result map[string]any) bool {
	if result == nil {
		return false
	}
	if passed, ok := result["passed"].(bool); ok {
		return passed
	}
	raw, err := json.Marshal(result["items"])
	if err != nil {
		return false
	}
	var items []backupTaskPrecheckItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return false
	}
	return backupTaskPrecheckPassed(items)
}

func addDatabaseVersionInfoItem(ctx *backupTaskPrecheckContext) {
	serverVersion := strings.TrimSpace(ctx.ServerVersion)
	serverMajor := strings.TrimSpace(ctx.ServerMajor)
	recommendedMajor := strings.TrimSpace(ctx.Resolved.RecommendedToolMajor)
	supportedRange := "9.6 - 17"
	message := fmt.Sprintf("当前 PostgreSQL 版本为 %s，建议选择 PostgreSQL %s 对应工具链的 pgBackRest。", serverVersion, recommendedMajor)
	if serverVersion == "" || serverMajor == "" {
		message = "未能完整识别数据库版本，请人工确认 PostgreSQL 版本并选择匹配的大版本工具链。"
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "postgres_version_info",
		Label:    "数据库版本识别",
		Severity: backupTaskPrecheckSeverityWarning,
		Blocking: false,
		Passed:   true,
		Message:  message,
		Detail: map[string]any{
			"server_version":          serverVersion,
			"server_major":            serverMajor,
			"recommended_tool_major":  recommendedMajor,
			"supported_tool_versions": supportedRange,
		},
	})
}

func executeBackupTaskPrecheck(ctx *backupTaskPrecheckContext) {
	querySettingsAndPrivileges(ctx)
	addDatabaseVersionInfoItem(ctx)
	checkExecutorAndRepositoryConnectivity(ctx)
	checkSourceSSHDirectoryPermissions(ctx)
	checkPGBackRestVersionCompatibility(ctx)
	checkPGBackRestVersionConsistency(ctx)
	checkStanzaAndRepo(ctx)
	checkRepositoryDiskForecast(ctx)
}

func querySettingsAndPrivileges(ctx *backupTaskPrecheckContext) {
	dbCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	db := dasDao.PostgresDB{
		User:     strings.TrimSpace(string(ctx.Instance.User)),
		Password: strings.TrimSpace(string(ctx.Instance.Password)),
		Host:     strings.TrimSpace(ctx.Instance.Hostname),
		Port:     ctx.Instance.Port,
		Database: ctx.SourceDatabase,
		SSLMode:  ctx.SSLMode,
		Ctx:      dbCtx,
	}
	query := `select
coalesce(current_setting('archive_command', true), '') as archive_command,
coalesce(current_setting('archive_mode', true), '') as archive_mode,
coalesce(current_setting('wal_level', true), '') as wal_level,
coalesce(current_setting('max_wal_senders', true), '') as max_wal_senders,
coalesce(current_setting('data_directory', true), '') as data_directory,
coalesce(pg_is_in_recovery()::text, 'false') as is_in_recovery,
coalesce(current_user, '') as current_user,
coalesce((select rolreplication::text from pg_roles where rolname = current_user), 'false') as has_replication,
coalesce((select rolsuper::text from pg_roles where rolname = current_user), 'false') as is_superuser,
coalesce((select sum(pg_database_size(datname)) from pg_database where datallowconn), 0)::text as estimated_size_bytes`
	_, rows, err := db.Query(query)
	if err != nil {
		detail := map[string]any{
			"host":     ctx.Instance.Hostname,
			"port":     ctx.Instance.Port,
			"database": ctx.SourceDatabase,
			"error":    err.Error(),
		}
		ctx.Items = append(ctx.Items,
			newBackupTaskPrecheckFailure("pg_archive_command", "PG 参数 archive_command 校验", "读取 pg_settings 失败，无法校验 archive_command", detail),
			newBackupTaskPrecheckFailure("pg_archive_mode", "PG 参数 archive_mode 校验", "读取 pg_settings 失败，无法校验 archive_mode", detail),
			newBackupTaskPrecheckFailure("pg_wal_level", "PG 参数 wal_level 校验", "读取 pg_settings 失败，无法校验 wal_level", detail),
			newBackupTaskPrecheckFailure("db_replication_privilege", "备份账号 REPLICATION 权限校验", "读取 pg_roles 失败，无法校验 REPLICATION 权限", detail),
			newBackupTaskPrecheckWarning("pg_max_wal_senders", "max_wal_senders 提示", "读取 max_wal_senders 失败，请人工评估 WAL 发送能力", detail),
		)
		return
	}
	if rows == nil || len(*rows) == 0 {
		detail := map[string]any{"error": "empty result"}
		ctx.Items = append(ctx.Items,
			newBackupTaskPrecheckFailure("pg_archive_command", "PG 参数 archive_command 校验", "未读取到 pg_settings 结果，无法校验 archive_command", detail),
			newBackupTaskPrecheckFailure("pg_archive_mode", "PG 参数 archive_mode 校验", "未读取到 pg_settings 结果，无法校验 archive_mode", detail),
			newBackupTaskPrecheckFailure("pg_wal_level", "PG 参数 wal_level 校验", "未读取到 pg_settings 结果，无法校验 wal_level", detail),
			newBackupTaskPrecheckFailure("db_replication_privilege", "备份账号 REPLICATION 权限校验", "未读取到当前账号权限信息", detail),
			newBackupTaskPrecheckWarning("pg_max_wal_senders", "max_wal_senders 提示", "未读取到 max_wal_senders，请人工评估 WAL 发送能力", detail),
		)
		return
	}
	row := (*rows)[0]
	archiveCommand := strings.TrimSpace(fmt.Sprintf("%v", row["archive_command"]))
	archiveMode := strings.TrimSpace(fmt.Sprintf("%v", row["archive_mode"]))
	walLevel := strings.TrimSpace(fmt.Sprintf("%v", row["wal_level"]))
	maxWalSenders := strings.TrimSpace(fmt.Sprintf("%v", row["max_wal_senders"]))
	dataDirectory := strings.TrimSpace(fmt.Sprintf("%v", row["data_directory"]))
	currentUser := strings.TrimSpace(fmt.Sprintf("%v", row["current_user"]))
	hasReplication := strings.EqualFold(strings.TrimSpace(fmt.Sprintf("%v", row["has_replication"])), "true")
	isSuperuser := strings.EqualFold(strings.TrimSpace(fmt.Sprintf("%v", row["is_superuser"])), "true")
	estimatedSizeBytes, _ := strconv.ParseInt(strings.TrimSpace(fmt.Sprintf("%v", row["estimated_size_bytes"])), 10, 64)
	ctx.Resolved.EstimatedSizeBytes = estimatedSizeBytes
	ctx.Resolved.MaxWalSenders = maxWalSenders
	ctx.PGDataPath = dataDirectory
	ctx.Resolved.PGDataPath = dataDirectory
	if resolvedStanza := extractPGBackRestArchiveStanza(archiveCommand); resolvedStanza != "" {
		ctx.Stanza = resolvedStanza
		ctx.DriverConfig.Stanza = resolvedStanza
		ctx.Resolved.Stanza = resolvedStanza
		ctx.Resolved.PGBackRestExpected = fmt.Sprintf("pgbackrest --stanza=%s archive-push %%p", resolvedStanza)
	}
	expectedArchiveCommand := strings.TrimSpace(ctx.Resolved.PGBackRestExpected)

	if archiveCommand == expectedArchiveCommand {
		ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
			ItemKey:  "pg_archive_command",
			Label:    "PG 参数 archive_command 校验",
			Severity: backupTaskPrecheckSeverityInfo,
			Blocking: true,
			Passed:   true,
			Message:  "archive_command 与当前任务 stanza 配置完全一致。",
			Detail: map[string]any{
				"actual":   archiveCommand,
				"expected": expectedArchiveCommand,
			},
		})
	} else {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pg_archive_command", "PG 参数 archive_command 校验", "archive_command 与当前任务 stanza 要求不一致", map[string]any{
			"actual":   archiveCommand,
			"expected": expectedArchiveCommand,
		}))
	}

	if strings.EqualFold(archiveMode, "on") {
		ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
			ItemKey:  "pg_archive_mode",
			Label:    "PG 参数 archive_mode 校验",
			Severity: backupTaskPrecheckSeverityInfo,
			Blocking: true,
			Passed:   true,
			Message:  "archive_mode=on。",
			Detail:   map[string]any{"actual": archiveMode},
		})
	} else {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pg_archive_mode", "PG 参数 archive_mode 校验", "archive_mode 必须为 on", map[string]any{"actual": archiveMode}))
	}

	walLevelRank := map[string]int{"minimal": 0, "replica": 1, "logical": 2}
	if walLevelRank[strings.ToLower(walLevel)] >= walLevelRank["replica"] {
		ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
			ItemKey:  "pg_wal_level",
			Label:    "PG 参数 wal_level 校验",
			Severity: backupTaskPrecheckSeverityInfo,
			Blocking: true,
			Passed:   true,
			Message:  "wal_level 满足 replica 或更高要求。",
			Detail:   map[string]any{"actual": walLevel},
		})
	} else {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pg_wal_level", "PG 参数 wal_level 校验", "wal_level 必须为 replica 或 logical", map[string]any{"actual": walLevel}))
	}

	if hasReplication || isSuperuser {
		privilegeSource := "REPLICATION"
		if isSuperuser && !hasReplication {
			privilegeSource = "SUPERUSER"
		}
		ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
			ItemKey:  "db_replication_privilege",
			Label:    "备份账号 REPLICATION 权限校验",
			Severity: backupTaskPrecheckSeverityInfo,
			Blocking: true,
			Passed:   true,
			Message:  "当前备份账号具备复制能力。",
			Detail: map[string]any{
				"current_user":     firstNonEmpty(currentUser, strings.TrimSpace(string(ctx.Instance.User))),
				"rolreplication":   hasReplication,
				"rolsuper":         isSuperuser,
				"privilege_source": privilegeSource,
			},
		})
	} else {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("db_replication_privilege", "备份账号 REPLICATION 权限校验", "当前备份账号既不是 SUPERUSER，也没有 REPLICATION 权限", map[string]any{
			"current_user":   firstNonEmpty(currentUser, strings.TrimSpace(string(ctx.Instance.User))),
			"rolreplication": hasReplication,
			"rolsuper":       isSuperuser,
		}))
	}

	warnMessage := "max_wal_senders 已读取，请结合并发备份、流复制和归档压力综合评估。"
	if maxWalSenders == "" {
		warnMessage = "未读取到 max_wal_senders，请人工评估 WAL 发送能力。"
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "pg_max_wal_senders",
		Label:    "max_wal_senders 提示",
		Severity: backupTaskPrecheckSeverityWarning,
		Blocking: false,
		Passed:   true,
		Message:  warnMessage,
		Detail:   map[string]any{"value": maxWalSenders},
	})
}

// pgBackRestStorageTargetIsS3 存储目标是否为 S3 兼容对象存储（pgBackRest repo1-type=s3 仓库）。
func pgBackRestStorageTargetIsS3(target *backupModels.InsightBackupStorageTargets) bool {
	if target == nil {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(string(target.Type))) {
	case "OSS_ALIYUN", "COS_TENCENT", "S3_MINIO", "OBS_HUAWEI":
		return true
	default:
		return false
	}
}

func precheckRepoNodeID(repositoryNode *backupModels.InsightBackupNodes) uint64 {
	if repositoryNode == nil {
		return 0
	}
	return repositoryNode.ID
}

func precheckRepoNodeHost(repositoryNode *backupModels.InsightBackupNodes) string {
	if repositoryNode == nil {
		return ""
	}
	return strings.TrimSpace(repositoryNode.Host)
}

// precheckS3RepoClient 用存储目标凭据构造 S3 客户端（预校验连通性检查用）。
func precheckS3RepoClient(target *backupModels.InsightBackupStorageTargets) (*S3Client, error) {
	if target == nil {
		return nil, fmt.Errorf("存储目标配置为空")
	}
	_, _, s3AK, s3SK, err := decryptStorageTargetCredentials(target)
	if err != nil {
		return nil, fmt.Errorf("解密对象存储凭据失败: %w", err)
	}
	if strings.TrimSpace(target.S3Bucket) == "" || strings.TrimSpace(target.S3Endpoint) == "" {
		return nil, fmt.Errorf("对象存储目标缺少 bucket/endpoint 配置")
	}
	if strings.TrimSpace(s3AK) == "" || strings.TrimSpace(s3SK) == "" {
		return nil, fmt.Errorf("对象存储目标缺少 AK/SK 凭据")
	}
	return NewS3Client(S3ClientOptions{
		Endpoint:       strings.TrimSpace(target.S3Endpoint),
		Region:         strings.TrimSpace(target.S3Region),
		Bucket:         strings.TrimSpace(target.S3Bucket),
		AccessKey:      strings.TrimSpace(s3AK),
		SecretKey:      strings.TrimSpace(s3SK),
		ForcePathStyle: target.S3ForcePathStyle,
		HTTPTimeout:    mysqlPhysicalObjectProbeTimeout,
	})
}

func checkExecutorAndRepositoryConnectivity(ctx *backupTaskPrecheckContext) {
	if ctx.RepoIsS3 {
		// S3 仓库模式：仓库侧无 SSH 拓扑可校验，但执行节点（数据库主机）的 sudo/工具能力仍必须过检
		if repairedNode, repairResult := tryAutoRepairBackupNode(ctx.C, ctx.ExecutorNode, ctx.Instance, "PG 物理备份执行节点"); repairResult != nil {
			if repairResult.Attempted {
				if repairResult.Recovered {
					ctx.Items = append(ctx.Items, newBackupTaskPrecheckInfo("executor_node_auto_repair", "执行节点自动修复", repairResult.Summary, repairResult.Detail))
				} else {
					ctx.Items = append(ctx.Items, newBackupTaskPrecheckWarning("executor_node_auto_repair", "执行节点自动修复", repairResult.Summary, repairResult.Detail))
				}
			}
			if repairedNode != nil {
				ctx.ExecutorNode = repairedNode
			}
		}
		if err := validatePGBackRestFixedChecks(ctx.Instance, buildBackupTaskPrecheckShadowTask(ctx), ctx.ExecutorNode, nil, ctx.ServerVersion, false); err != nil {
			ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("executor_repository_prerequisite", "执行节点基础能力校验", "执行节点基础能力校验失败", map[string]any{"error": err.Error()}))
			return
		}
		checkObjectStorageRepoConnectivity(ctx)
		return
	}
	repoRemote := !sameBackupNodeHost(ctx.ExecutorNode, ctx.RepositoryNode)
	if repairedNode, repairResult := tryAutoRepairBackupNode(ctx.C, ctx.ExecutorNode, ctx.Instance, "PG 物理备份执行节点"); repairResult != nil {
		if repairResult.Attempted {
			if repairResult.Recovered {
				ctx.Items = append(ctx.Items, newBackupTaskPrecheckInfo("executor_node_auto_repair", "执行节点自动修复", repairResult.Summary, repairResult.Detail))
			} else {
				ctx.Items = append(ctx.Items, newBackupTaskPrecheckWarning("executor_node_auto_repair", "执行节点自动修复", repairResult.Summary, repairResult.Detail))
			}
		}
		if repairedNode != nil {
			ctx.ExecutorNode = repairedNode
		}
	}
	if repoRemote && ctx.RepositoryNode != nil {
		if repairedNode, repairResult := tryAutoRepairBackupNode(ctx.C, ctx.RepositoryNode, nil, "PG 物理备份仓库节点"); repairResult != nil {
			if repairResult.Attempted {
				if repairResult.Recovered {
					ctx.Items = append(ctx.Items, newBackupTaskPrecheckInfo("repository_node_auto_repair", "仓库节点自动修复", repairResult.Summary, repairResult.Detail))
				} else {
					ctx.Items = append(ctx.Items, newBackupTaskPrecheckWarning("repository_node_auto_repair", "仓库节点自动修复", repairResult.Summary, repairResult.Detail))
				}
			}
			if repairedNode != nil {
				ctx.RepositoryNode = repairedNode
			}
		}
	}
	if err := validatePGBackRestFixedChecks(ctx.Instance, buildBackupTaskPrecheckShadowTask(ctx), ctx.ExecutorNode, ctx.RepositoryNode, ctx.ServerVersion, repoRemote); err != nil {
		itemKey := "source_to_repo_ssh"
		label := "数据库主机到备份中心 SSH 连通性校验"
		message := "执行节点或仓库节点基础能力校验失败"
		detail := map[string]any{"error": err.Error()}
		if prepErr, ok := AsPhysicalBackupPrepareError(err); ok {
			switch strings.TrimSpace(prepErr.Stage) {
			case "executor_connect", "repository_tool_connect", "repo_access_remote", "repo_access_local":
				label = "数据库主机到备份中心 SSH 连通性校验"
			default:
				itemKey = "executor_repository_prerequisite"
				label = "执行节点与仓库节点基础能力校验"
			}
			if strings.TrimSpace(prepErr.Title) != "" {
				message = strings.TrimSpace(prepErr.Title)
			}
			detail = map[string]any{
				"stage":      prepErr.Stage,
				"title":      prepErr.Title,
				"suggestion": prepErr.Suggestion,
				"detail":     prepErr.Detail,
				"raw_error":  prepErr.RawError,
			}
		}
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure(itemKey, label, message, detail))
		return
	}
	if repoRemote {
		if err := ensurePGBackRestRepositoryAccess(ctx.Instance, ctx.ExecutorNode, ctx.RepositoryNode, ctx.RepoPath, true); err != nil {
			ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("source_to_repo_ssh", "数据库主机到备份中心 SSH 连通性校验", "数据库主机到备份中心 SSH 连通性校验失败", map[string]any{
				"executor_host":   ctx.ExecutorNode.Host,
				"repository_host": ctx.RepositoryNode.Host,
				"repo_path":       ctx.RepoPath,
				"error":           err.Error(),
			}))
			return
		}
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "source_to_repo_ssh",
		Label:    "数据库主机到备份中心 SSH 连通性校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  "数据库主机到备份中心 SSH 连通正常。",
		Detail: map[string]any{
			"executor_host":   ctx.ExecutorNode.Host,
			"repository_host": ctx.RepositoryNode.Host,
			"repo_path":       ctx.RepoPath,
		},
	})
}

// checkObjectStorageRepoConnectivity S3 仓库模式下的「数据库主机 -> 对象存储」连通性检查。
// 服务端用存储目标凭据直接 HeadBucket：能通过即认为凭据有效、桶可达；
// 数据库主机到 OSS 端点的网络连通性由执行阶段 pgbackrest 实际读写兜底（同一段网络路径）。
func checkObjectStorageRepoConnectivity(ctx *backupTaskPrecheckContext) {
	client, err := precheckS3RepoClient(ctx.StorageTarget)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("source_to_repo_ssh", "对象存储仓库连通性校验", err.Error(), map[string]any{
			"storage_target_id": ctx.StorageTarget.ID,
			"storage_target":    strings.TrimSpace(ctx.StorageTarget.Name),
		}))
		return
	}
	headCtx, cancel := context.WithTimeout(context.Background(), mysqlPhysicalObjectProbeTimeout)
	defer cancel()
	if err := client.HeadBucket(headCtx); err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("source_to_repo_ssh", "对象存储仓库连通性校验", "对象存储桶不可达或凭据无效", map[string]any{
			"storage_target":  strings.TrimSpace(ctx.StorageTarget.Name),
			"s3_bucket":       strings.TrimSpace(ctx.StorageTarget.S3Bucket),
			"s3_endpoint":     strings.TrimSpace(ctx.StorageTarget.S3Endpoint),
			"repo_path":       ctx.RepoPath,
			"error":           err.Error(),
			"executor_host":   strings.TrimSpace(ctx.ExecutorNode.Host),
			"repository_mode": "object_storage",
		}))
		return
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "source_to_repo_ssh",
		Label:    "对象存储仓库连通性校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  "对象存储桶可达，凭据有效。",
		Detail: map[string]any{
			"storage_target":  strings.TrimSpace(ctx.StorageTarget.Name),
			"s3_bucket":       strings.TrimSpace(ctx.StorageTarget.S3Bucket),
			"s3_endpoint":     strings.TrimSpace(ctx.StorageTarget.S3Endpoint),
			"repo_path":       ctx.RepoPath,
			"executor_host":   strings.TrimSpace(ctx.ExecutorNode.Host),
			"repository_mode": "object_storage",
		},
	})
}

func checkSourceSSHDirectoryPermissions(ctx *backupTaskPrecheckContext) {
	sshUser, sshPassword, sshPrivateKey, sshPort, ok, reason := resolveSSHCredentialsFromInstance(ctx.Instance)
	if !ok {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("source_ssh_permissions", "数据库主机 SSH 目录权限校验", "无法解析数据库主机 SSH 凭据，无法校验 .ssh 权限", map[string]any{
			"reason": reason,
		}))
		return
	}
	runner, err := newSSHRunner(&backupNodeRemoteConfig{
		Host:          strings.TrimSpace(ctx.Instance.Hostname),
		Port:          sshPort,
		SSHUser:       sshUser,
		SSHPassword:   sshPassword,
		SSHPrivateKey: sshPrivateKey,
	})
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("source_ssh_permissions", "数据库主机 SSH 目录权限校验", "无法连接数据库主机检查 .ssh 权限", map[string]any{
			"host":  ctx.Instance.Hostname,
			"user":  sshUser,
			"error": err.Error(),
		}))
		return
	}
	defer runner.Close()
	script := strings.Join([]string{
		"set -e",
		"WHO_AM_I=$(id -un 2>/dev/null || true)",
		"[ -n \"$WHO_AM_I\" ] && [ \"$WHO_AM_I\" != \"root\" ] && HOME_DIR=$(eval echo \"~$WHO_AM_I\") || HOME_DIR=$HOME",
		"SSH_DIR=\"$HOME_DIR/.ssh\"",
		"AUTHORIZED_KEYS=\"$SSH_DIR/authorized_keys\"",
		"DIR_MODE=$(stat -c %a \"$SSH_DIR\" 2>/dev/null || stat -f %OLp \"$SSH_DIR\" 2>/dev/null || echo MISSING)",
		"FILE_MODE=$(stat -c %a \"$AUTHORIZED_KEYS\" 2>/dev/null || stat -f %OLp \"$AUTHORIZED_KEYS\" 2>/dev/null || echo MISSING)",
		"echo DIR_MODE=$DIR_MODE",
		"echo FILE_MODE=$FILE_MODE",
	}, "\n")
	output, runErr := runner.Run(script)
	if runErr != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("source_ssh_permissions", "数据库主机 SSH 目录权限校验", "执行 .ssh 权限校验失败", map[string]any{
			"error":  runErr.Error(),
			"output": output,
		}))
		return
	}
	parsed := parseKeyValueOutput(output)
	dirMode := normalizePermissionMode(parsed["DIR_MODE"])
	fileMode := normalizePermissionMode(parsed["FILE_MODE"])
	if dirMode == "700" && fileMode == "600" {
		ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
			ItemKey:  "source_ssh_permissions",
			Label:    "数据库主机 SSH 目录权限校验",
			Severity: backupTaskPrecheckSeverityInfo,
			Blocking: true,
			Passed:   true,
			Message:  ".ssh 目录权限为 700，authorized_keys 权限为 600。",
			Detail:   map[string]any{"dir_mode": dirMode, "authorized_keys_mode": fileMode},
		})
		return
	}
	ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("source_ssh_permissions", "数据库主机 SSH 目录权限校验", ".ssh 目录或 authorized_keys 权限不符合要求", map[string]any{
		"dir_mode":             dirMode,
		"authorized_keys_mode": fileMode,
		"expected_dir_mode":    "700",
		"expected_file_mode":   "600",
	}))
}

func checkStanzaAndRepo(ctx *backupTaskPrecheckContext) {
	if strings.TrimSpace(ctx.PGDataPath) == "" {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_stanza_check", "pgBackRest stanza-check 校验", "未读取到 data_directory，无法执行 stanza-check", nil))
		return
	}
	if ctx.RepoIsS3 {
		checkStanzaAndRepoObjectStorage(ctx)
		return
	}
	repoCfg, err := buildStoredBackupNodeRemoteConfig(ctx.RepositoryNode)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_stanza_check", "pgBackRest stanza-check 校验", "无法读取仓库节点 SSH 配置", map[string]any{"error": err.Error()}))
		return
	}
	repoRunner, err := newSSHRunner(repoCfg)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_stanza_check", "pgBackRest stanza-check 校验", "无法连接仓库节点执行 stanza-check", map[string]any{"error": err.Error()}))
		return
	}
	defer repoRunner.Close()
	sourceSSHUser, sourceSSHPass, sourceSSHKey, sourceSSHPort, err := resolveBackupInstanceSSHCredentials(parseExtraConfig(ctx.Instance.ExtraConfig), string(ctx.Instance.User), string(ctx.Instance.Password))
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_stanza_check", "pgBackRest stanza-check 校验", "无法解析数据库主机 SSH 凭据，无法执行 stanza-check", map[string]any{"error": err.Error()}))
		return
	}
	sourceRunner, err := newSSHRunner(&backupNodeRemoteConfig{
		Host:          strings.TrimSpace(ctx.Instance.Hostname),
		Port:          sourceSSHPort,
		SSHUser:       defaultBackupNodeSSHUser(sourceSSHUser),
		SSHPassword:   strings.TrimSpace(sourceSSHPass),
		SSHPrivateKey: strings.TrimSpace(sourceSSHKey),
	})
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_stanza_check", "pgBackRest stanza-check 校验", "无法连接数据库主机识别 pgBackRest 工具", map[string]any{"error": err.Error()}))
		return
	}
	defer sourceRunner.Close()
	sourceToolSpec, err := resolveRemotePostgresToolSpec(sourceRunner, defaultBackupNodeToolsDir(ctx.ExecutorNode.ToolsDir), ctx.ServerVersion, "pgbackrest")
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_stanza_check", "pgBackRest stanza-check 校验", "无法识别数据库主机 pgBackRest 工具", map[string]any{"error": err.Error()}))
		return
	}
	repoWorkDir := filepath.ToSlash(filepath.Join("/tmp", "goinsight-pgbackrest-precheck", sanitizePathSegment(ctx.Resolved.TaskID, "task"), strconv.FormatInt(time.Now().UnixNano(), 10)))
	_, _ = repoRunner.Run("rm -rf " + shellQuote(repoWorkDir))
	if _, runErr := repoRunner.Run("mkdir -p " + shellQuote(repoWorkDir) + " " + shellQuote(filepath.ToSlash(filepath.Join(repoWorkDir, "lock"))) + " " + shellQuote(filepath.ToSlash(filepath.Join(repoWorkDir, "log"))) + " " + shellQuote(filepath.ToSlash(filepath.Join(repoWorkDir, "spool")))); runErr != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_stanza_check", "pgBackRest stanza-check 校验", "初始化仓库机预检运行目录失败", map[string]any{"error": runErr.Error(), "repo_work_dir": repoWorkDir}))
		return
	}
	defer func() {
		_, _ = repoRunner.Run("rm -rf " + shellQuote(repoWorkDir))
	}()
	sourceSSHScriptPath := filepath.ToSlash(filepath.Join(repoWorkDir, "pgbackrest-source-ssh.sh"))
	sourceToolCmd := buildRemotePGBackRestSourceToolCommand(sourceToolSpec, ctx.PGOSUser)
	sourceRuntime, err := buildRepoHostSourceSSHRuntime(repoWorkDir, sourceSSHScriptPath, sourceSSHPass, sourceSSHKey, sourceSSHPort)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_stanza_check", "pgBackRest stanza-check 校验", "准备源端 SSH 运行时失败", map[string]any{"error": err.Error()}))
		return
	}
	if _, runErr := repoRunner.Run(sourceRuntime.SetupCommand); runErr != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_stanza_check", "pgBackRest stanza-check 校验", "准备仓库机到源端 SSH 运行时失败", map[string]any{"error": runErr.Error()}))
		return
	}
	repoConfigPath := filepath.ToSlash(filepath.Join(repoWorkDir, "pgbackrest.conf"))
	repoConfigText := buildPGBackRestRepoHostConfig(repoWorkDir, ctx.RepoPath, sourceSSHScriptPath, sourceToolCmd, ctx.Stanza, ctx.PGDataPath, ctx.Instance.Hostname, sourceSSHUser, sourceSSHPort, ctx.Instance.Port, string(ctx.Instance.User), ctx.DriverConfig.StartFast, ctx.DriverConfig.ProcessMax)
	writeConfigCmd := "cat > " + shellQuote(repoConfigPath) + " <<'EOF'\n" + repoConfigText + "\nEOF\nchmod 640 " + shellQuote(repoConfigPath)
	if _, runErr := repoRunner.Run(writeConfigCmd); runErr != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_stanza_check", "pgBackRest stanza-check 校验", "写入仓库机 pgbackrest.conf 失败", map[string]any{"error": runErr.Error()}))
		return
	}
	repoToolSpec, err := resolveRemotePostgresToolSpec(repoRunner, defaultBackupNodeToolsDir(ctx.RepositoryNode.ToolsDir), ctx.ServerVersion, "pgbackrest")
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_stanza_check", "pgBackRest stanza-check 校验", "无法识别仓库节点 pgBackRest 工具", map[string]any{"error": err.Error()}))
		return
	}
	repoPGBackRestCmd := repoToolSpec.ShellCommand()
	commonArgs := []string{
		"--config=" + repoConfigPath,
		"--stanza=" + ctx.Stanza,
		"--log-level-console=detail",
	}
	stanzaCreateCmd := repoPGBackRestCmd + " " + joinShellCommand(append(append([]string(nil), commonArgs...), "stanza-create"))
	stanzaCreateOutput, stanzaCreateErr := repoRunner.Run(stanzaCreateCmd)
	if stanzaCreateErr != nil && !strings.Contains(strings.ToLower(stanzaCreateOutput), "already exists") {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_stanza_check", "pgBackRest stanza-check 校验", "stanza-create 执行失败，请检查 stanza 与仓库目录初始化状态", map[string]any{
			"error":               stanzaCreateErr.Error(),
			"output":              stanzaCreateOutput,
			"stanza":              ctx.Stanza,
			"repo_path":           ctx.RepoPath,
			"pg_data_path":        ctx.PGDataPath,
			"source_tool_path":    sourceToolSpec.Path,
			"source_version_line": sourceToolSpec.VersionLine,
			"repo_tool_path":      repoToolSpec.Path,
			"repo_version_line":   repoToolSpec.VersionLine,
		}))
		return
	}
	cmd := repoPGBackRestCmd + " " + joinShellCommand(append(commonArgs,
		"--archive-check=n",
		"check",
	))
	output, err := repoRunner.Run(cmd)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_stanza_check", "pgBackRest stanza-check 校验", "stanza-check 执行失败，请检查 stanza 与仓库合法性", map[string]any{
			"error":                err.Error(),
			"output":               output,
			"stanza_create_output": stanzaCreateOutput,
			"stanza":               ctx.Stanza,
			"repo_path":            ctx.RepoPath,
			"pg_data_path":         ctx.PGDataPath,
			"source_tool_path":     sourceToolSpec.Path,
			"source_version_line":  sourceToolSpec.VersionLine,
			"repo_tool_path":       repoToolSpec.Path,
			"repo_version_line":    repoToolSpec.VersionLine,
		}))
		return
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "pgbackrest_stanza_check",
		Label:    "pgBackRest stanza-check 校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  "stanza-check 校验通过。",
		Detail: map[string]any{
			"stanza":               ctx.Stanza,
			"repo_path":            ctx.RepoPath,
			"pg_data_path":         ctx.PGDataPath,
			"stanza_create_output": strings.TrimSpace(stanzaCreateOutput),
			"output":               strings.TrimSpace(output),
			"source_tool_path":     sourceToolSpec.Path,
			"source_version_line":  sourceToolSpec.VersionLine,
			"repo_tool_path":       repoToolSpec.Path,
			"repo_version_line":    repoToolSpec.VersionLine,
		},
	})
}

// buildPGBackRestS3PrecheckConfig 生成预检用的 S3 仓库 pgbackrest 配置（在数据库主机上执行 stanza-check 用）。
// lock/log/spool 放在预检工作目录内，避免污染实例本机 /var；配置文件与执行期一致放工作目录。
func buildPGBackRestS3PrecheckConfig(workDir string, ctx *backupTaskPrecheckContext) string {
	startFastValue := "n"
	if ctx.DriverConfig.StartFast {
		startFastValue = "y"
	}
	lines := []string{
		"[global]",
		fmt.Sprintf("repo1-path=%s", ctx.RepoPath),
		"repo1-retention-full=9999999",
	}
	lines = append(lines, pgBackRestS3RepoLines(ctx.DriverConfig)...)
	lines = append(lines,
		fmt.Sprintf("process-max=%d", ctx.DriverConfig.ProcessMax),
		fmt.Sprintf("start-fast=%s", startFastValue),
		fmt.Sprintf("lock-path=%s", filepath.ToSlash(filepath.Join(workDir, "lock"))),
		fmt.Sprintf("log-path=%s", filepath.ToSlash(filepath.Join(workDir, "log"))),
		fmt.Sprintf("spool-path=%s", filepath.ToSlash(filepath.Join(workDir, "spool"))),
		"log-level-file=off",
		"",
		fmt.Sprintf("[%s]", ctx.Stanza),
		fmt.Sprintf("pg1-path=%s", strings.TrimSpace(ctx.PGDataPath)),
	)
	if ctx.Instance.Port > 0 {
		lines = append(lines, fmt.Sprintf("pg1-port=%d", ctx.Instance.Port))
	}
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

// checkStanzaAndRepoObjectStorage S3 仓库模式的 stanza-check：
// 在数据库主机本机以 postgres 用户执行 stanza-create + check（--archive-check=n），
// 仓库直写对象存储 —— 该检查同时验证了数据库主机到 OSS 的真实网络路径与 pgBackRest 的 S3 能力。
func checkStanzaAndRepoObjectStorage(ctx *backupTaskPrecheckContext) {
	itemKey := "pgbackrest_stanza_check"
	label := "pgBackRest stanza-check 校验（对象存储仓库）"
	sourceSSHUser, sourceSSHPass, sourceSSHKey, sourceSSHPort, err := resolveBackupInstanceSSHCredentials(parseExtraConfig(ctx.Instance.ExtraConfig), string(ctx.Instance.User), string(ctx.Instance.Password))
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure(itemKey, label, "无法解析数据库主机 SSH 凭据，无法执行 stanza-check", map[string]any{"error": err.Error()}))
		return
	}
	sourceRunner, err := newSSHRunner(&backupNodeRemoteConfig{
		Host:          strings.TrimSpace(ctx.Instance.Hostname),
		Port:          sourceSSHPort,
		SSHUser:       defaultBackupNodeSSHUser(sourceSSHUser),
		SSHPassword:   strings.TrimSpace(sourceSSHPass),
		SSHPrivateKey: strings.TrimSpace(sourceSSHKey),
	})
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure(itemKey, label, "无法连接数据库主机执行 stanza-check", map[string]any{"error": err.Error()}))
		return
	}
	defer sourceRunner.Close()
	sourceToolSpec, err := resolveRemotePostgresToolSpec(sourceRunner, defaultBackupNodeToolsDir(ctx.ExecutorNode.ToolsDir), ctx.ServerVersion, "pgbackrest")
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure(itemKey, label, "无法识别数据库主机 pgBackRest 工具", map[string]any{"error": err.Error()}))
		return
	}
	workDir := filepath.ToSlash(filepath.Join("/tmp", "goinsight-pgbackrest-precheck", sanitizePathSegment(ctx.Resolved.TaskID, "task"), strconv.FormatInt(time.Now().UnixNano(), 10)))
	_, _ = sourceRunner.Run("rm -rf " + shellQuote(workDir))
	if _, runErr := sourceRunner.Run("mkdir -p " + shellQuote(workDir) + " " + shellQuote(filepath.ToSlash(filepath.Join(workDir, "lock"))) + " " + shellQuote(filepath.ToSlash(filepath.Join(workDir, "log"))) + " " + shellQuote(filepath.ToSlash(filepath.Join(workDir, "spool"))) + " && chown -R postgres:postgres " + shellQuote(workDir)); runErr != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure(itemKey, label, "初始化数据库主机预检运行目录失败", map[string]any{"error": runErr.Error(), "work_dir": workDir}))
		return
	}
	defer func() {
		_, _ = sourceRunner.Run("rm -rf " + shellQuote(workDir))
	}()
	configPath := filepath.ToSlash(filepath.Join(workDir, "pgbackrest.conf"))
	// 与执行期一致：配置文件 0644（内含 S3 AK/SK，安全边界与执行期 pgbackrest.conf 相同）
	writeConfigCmd := "cat > " + shellQuote(configPath) + " <<'EOF'\n" + buildPGBackRestS3PrecheckConfig(workDir, ctx) + "\nEOF\nchmod 644 " + shellQuote(configPath)
	if _, runErr := sourceRunner.Run(writeConfigCmd); runErr != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure(itemKey, label, "写入预检 pgbackrest.conf 失败", map[string]any{"error": runErr.Error()}))
		return
	}
	commonArgs := "--config=" + configPath + " --stanza=" + shellQuote(ctx.Stanza) + " --log-level-console=detail"
	pgCmd := sourceToolSpec.ShellCommand()
	// 单命令 SSH 执行环境没有 run_pg_test 函数，这里内联定义等价的 postgres 用户切换包装
	pgRunHelper := `run_pg() {` +
		` if [ "$(id -un)" = "postgres" ]; then sh -lc "$1"; return $?; fi;` +
		` if [ "$(id -un)" = "root" ]; then su - postgres -s /bin/sh -c "$1"; return $?; fi;` +
		` if command -v sudo >/dev/null 2>&1; then sudo -n -u postgres sh -lc "$1"; return $?; fi;` +
		` return 1; }`
	runAsPostgres := func(cmd string) string { return pgRunHelper + "\nrun_pg " + shellQuote(cmd) }
	stanzaCreateCmd := runAsPostgres(pgCmd + " " + commonArgs + " stanza-create")
	stanzaCreateOutput, stanzaCreateErr := sourceRunner.Run(stanzaCreateCmd)
	if stanzaCreateErr != nil && !strings.Contains(strings.ToLower(stanzaCreateOutput), "already exists") {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure(itemKey, label, "stanza-create 执行失败，请检查对象存储凭据与 stanza 状态", map[string]any{
			"error":          stanzaCreateErr.Error(),
			"output":         stanzaCreateOutput,
			"stanza":         ctx.Stanza,
			"repo_path":      ctx.RepoPath,
			"s3_bucket":      strings.TrimSpace(ctx.StorageTarget.S3Bucket),
			"s3_endpoint":    strings.TrimSpace(ctx.StorageTarget.S3Endpoint),
			"pg_data_path":   ctx.PGDataPath,
			"source_tool":    sourceToolSpec.Path,
			"source_version": sourceToolSpec.VersionLine,
		}))
		return
	}
	// check 步骤用数据库主机默认配置（/etc/pgbackrest/pgbackrest.conf）验证真实归档链路：
	// archive_command 由默认配置驱动，WAL 会归档到默认配置的仓库路径；若用临时 conf（per-task 仓库路径）
	// 等 WAL 永远等不到。stanza 不存在时提示先用默认配置 stanza-create。
	checkOutput, checkErr := sourceRunner.Run(runAsPostgres(pgCmd + " --stanza=" + shellQuote(ctx.Stanza) + " --log-level-console=info check"))
	if checkErr != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure(itemKey, label, "stanza-check（WAL 归档验证）执行失败：请确认数据库主机 /etc/pgbackrest/pgbackrest.conf 与 archive_command 使用相同的 stanza 与仓库配置（必要时先以 postgres 用户执行 pgbackrest --stanza=<stanza> stanza-create）", map[string]any{
			"error":                checkErr.Error(),
			"output":               checkOutput,
			"stanza_create_output": stanzaCreateOutput,
			"stanza":               ctx.Stanza,
			"repo_path":            ctx.RepoPath,
		}))
		return
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  itemKey,
		Label:    label,
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  "stanza-create / check 执行成功，对象存储仓库可用。",
		Detail: map[string]any{
			"stanza":         ctx.Stanza,
			"repo_path":      ctx.RepoPath,
			"s3_bucket":      strings.TrimSpace(ctx.StorageTarget.S3Bucket),
			"s3_endpoint":    strings.TrimSpace(ctx.StorageTarget.S3Endpoint),
			"source_tool":    sourceToolSpec.Path,
			"source_version": sourceToolSpec.VersionLine,
			"output":         checkOutput,
		},
	})
}

func checkRepositoryDiskForecast(ctx *backupTaskPrecheckContext) {
	if ctx.RepoIsS3 {
		ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
			ItemKey:  "repository_free_space",
			Label:    "仓库剩余空间预估校验",
			Severity: backupTaskPrecheckSeverityInfo,
			Blocking: true,
			Passed:   true,
			Message:  "仓库为对象存储，容量由云端配额管理，跳过本地磁盘预估。",
			Detail: map[string]any{
				"estimated_size_bytes": ctx.Resolved.EstimatedSizeBytes,
				"repository_mode":      "object_storage",
			},
		})
		return
	}
	cfg, err := buildStoredBackupNodeRemoteConfig(ctx.RepositoryNode)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("repository_free_space", "备份中心剩余磁盘预估校验", "无法读取仓库节点 SSH 配置", map[string]any{"error": err.Error()}))
		return
	}
	runner, err := newSSHRunner(cfg)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("repository_free_space", "备份中心剩余磁盘预估校验", "无法连接仓库节点读取剩余磁盘", map[string]any{"error": err.Error()}))
		return
	}
	defer runner.Close()
	script := fmt.Sprintf("set -e\nTARGET_PARENT=$(dirname %s)\nmkdir -p \"$TARGET_PARENT\"\nAVAILABLE_BYTES=$(df -Pk \"$TARGET_PARENT\" 2>/dev/null | awk 'NR==2{print $4*1024}')\necho AVAILABLE_BYTES=${AVAILABLE_BYTES:-0}\n", shellQuote(ctx.RepoPath))
	output, runErr := runner.Run(script)
	if runErr != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("repository_free_space", "备份中心剩余磁盘预估校验", "读取仓库节点剩余磁盘失败", map[string]any{"error": runErr.Error(), "output": output}))
		return
	}
	availableBytes, _ := strconv.ParseInt(strings.TrimSpace(parseKeyValueOutput(output)["AVAILABLE_BYTES"]), 10, 64)
	ctx.Resolved.RepositoryFreeBytes = availableBytes
	if availableBytes > ctx.Resolved.EstimatedSizeBytes && availableBytes > 0 {
		ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
			ItemKey:  "repository_free_space",
			Label:    "备份中心剩余磁盘预估校验",
			Severity: backupTaskPrecheckSeverityInfo,
			Blocking: true,
			Passed:   true,
			Message:  "备份中心剩余磁盘大于预估备份大小。",
			Detail: map[string]any{
				"estimated_size_bytes": ctx.Resolved.EstimatedSizeBytes,
				"available_bytes":      availableBytes,
			},
		})
		return
	}
	ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("repository_free_space", "备份中心剩余磁盘预估校验", "备份中心剩余磁盘不足，必须大于预估备份大小", map[string]any{
		"estimated_size_bytes": ctx.Resolved.EstimatedSizeBytes,
		"available_bytes":      availableBytes,
	}))
}

func checkPGBackRestVersionCompatibility(ctx *backupTaskPrecheckContext) {
	sourceSSHUser, sourceSSHPass, sourceSSHKey, sourceSSHPort, err := resolveBackupInstanceSSHCredentials(parseExtraConfig(ctx.Instance.ExtraConfig), string(ctx.Instance.User), string(ctx.Instance.Password))
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_version_compatibility", "pgBackRest 版本兼容性校验", "无法解析数据库主机 SSH 凭据", map[string]any{"error": err.Error()}))
		return
	}
	sourceRunner, err := newSSHRunner(&backupNodeRemoteConfig{
		Host:          strings.TrimSpace(ctx.Instance.Hostname),
		Port:          sourceSSHPort,
		SSHUser:       defaultBackupNodeSSHUser(sourceSSHUser),
		SSHPassword:   strings.TrimSpace(sourceSSHPass),
		SSHPrivateKey: strings.TrimSpace(sourceSSHKey),
	})
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_version_compatibility", "pgBackRest 版本兼容性校验", "无法连接数据库主机检查 pgBackRest 版本", map[string]any{"error": err.Error()}))
		return
	}
	defer sourceRunner.Close()
	sourceToolSpec, err := resolveRemotePostgresToolSpec(sourceRunner, defaultBackupNodeToolsDir(ctx.ExecutorNode.ToolsDir), ctx.ServerVersion, "pgbackrest")
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_version_compatibility", "pgBackRest 版本兼容性校验", "数据库主机未检测到可用的 pgBackRest 工具", map[string]any{
			"postgres_major": ctx.ServerMajor,
			"error":          err.Error(),
		}))
		return
	}
	if ctx.RepoIsS3 {
		// S3 仓库模式没有独立仓库节点，pgbackrest 全部跑在数据库主机，只校验源端版本
		ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
			ItemKey:  "pgbackrest_version_compatibility",
			Label:    "pgBackRest 版本兼容性校验",
			Severity: backupTaskPrecheckSeverityInfo,
			Blocking: true,
			Passed:   true,
			Message:  "数据库主机 pgBackRest 工具可用（对象存储仓库模式，无独立仓库节点）。",
			Detail: map[string]any{
				"source_tool_path":    sourceToolSpec.Path,
				"source_version_line": sourceToolSpec.VersionLine,
				"repository_mode":     "object_storage",
			},
		})
		return
	}
	repoCfg, err := buildStoredBackupNodeRemoteConfig(ctx.RepositoryNode)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_version_compatibility", "pgBackRest 版本兼容性校验", "无法读取仓库节点 SSH 配置", map[string]any{"error": err.Error()}))
		return
	}
	repoRunner, err := newSSHRunner(repoCfg)
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_version_compatibility", "pgBackRest 版本兼容性校验", "无法连接仓库节点检查 pgBackRest 版本", map[string]any{"error": err.Error()}))
		return
	}
	defer repoRunner.Close()
	repoToolSpec, err := resolveRemotePostgresToolSpec(repoRunner, defaultBackupNodeToolsDir(ctx.RepositoryNode.ToolsDir), ctx.ServerVersion, "pgbackrest")
	if err != nil {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_version_compatibility", "pgBackRest 版本兼容性校验", "仓库节点未检测到可用的 pgBackRest 工具", map[string]any{
			"postgres_major": ctx.ServerMajor,
			"error":          err.Error(),
		}))
		return
	}
	ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
		ItemKey:  "pgbackrest_version_compatibility",
		Label:    "pgBackRest 版本兼容性校验",
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: true,
		Passed:   true,
		Message:  fmt.Sprintf("已检测到与 PostgreSQL %s 配套的 pgBackRest 工具。", ctx.ServerMajor),
		Detail: map[string]any{
			"postgres_major":      ctx.ServerMajor,
			"source_tool_path":    sourceToolSpec.Path,
			"source_version_line": sourceToolSpec.VersionLine,
			"repo_tool_path":      repoToolSpec.Path,
			"repo_version_line":   repoToolSpec.VersionLine,
		},
	})
}

func checkPGBackRestVersionConsistency(ctx *backupTaskPrecheckContext) {
	if ctx.RepoIsS3 {
		ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
			ItemKey:  "pgbackrest_version_consistency",
			Label:    "pgBackRest 源端/仓库端版本一致性校验",
			Severity: backupTaskPrecheckSeverityInfo,
			Blocking: true,
			Passed:   true,
			Message:  "对象存储仓库模式，pgBackRest 仅运行在数据库主机，无版本一致性要求。",
			Detail:   map[string]any{"repository_mode": "object_storage"},
		})
		return
	}
	compatibilityItem := backupTaskPrecheckItem{}
	found := false
	for _, item := range ctx.Items {
		if item.ItemKey == "pgbackrest_version_compatibility" {
			compatibilityItem = item
			found = true
			break
		}
	}
	if !found || !compatibilityItem.Passed {
		ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_version_consistency", "pgBackRest 源端/仓库端版本一致性校验", "未完成 pgBackRest 版本兼容性识别，无法校验版本一致性", compatibilityItem.Detail))
		return
	}
	sourceVersion := strings.TrimSpace(fmt.Sprintf("%v", compatibilityItem.Detail["source_version_line"]))
	repoVersion := strings.TrimSpace(fmt.Sprintf("%v", compatibilityItem.Detail["repo_version_line"]))
	if sourceVersion != "" && repoVersion != "" && strings.EqualFold(sourceVersion, repoVersion) {
		ctx.Items = append(ctx.Items, backupTaskPrecheckItem{
			ItemKey:  "pgbackrest_version_consistency",
			Label:    "pgBackRest 源端/仓库端版本一致性校验",
			Severity: backupTaskPrecheckSeverityInfo,
			Blocking: true,
			Passed:   true,
			Message:  "数据库主机与仓库节点的 pgBackRest 版本一致。",
			Detail: map[string]any{
				"source_tool_path":    compatibilityItem.Detail["source_tool_path"],
				"source_version_line": sourceVersion,
				"repo_tool_path":      compatibilityItem.Detail["repo_tool_path"],
				"repo_version_line":   repoVersion,
			},
		})
		return
	}
	ctx.Items = append(ctx.Items, newBackupTaskPrecheckFailure("pgbackrest_version_consistency", "pgBackRest 源端/仓库端版本一致性校验", "数据库主机与仓库节点的 pgBackRest 版本不一致，会导致 stanza-check / backup 失败", map[string]any{
		"source_tool_path":    compatibilityItem.Detail["source_tool_path"],
		"source_version_line": sourceVersion,
		"repo_tool_path":      compatibilityItem.Detail["repo_tool_path"],
		"repo_version_line":   repoVersion,
	}))
}

func buildBackupTaskPrecheckShadowTask(ctx *backupTaskPrecheckContext) *backupModels.InsightBackupTasks {
	task := &backupModels.InsightBackupTasks{
		TaskID:           ctx.Resolved.TaskID,
		SourceInstanceID: ctx.Form.SourceInstanceID,
		ExecutorNodeID:   ctx.ExecutorNode.ID,
		StorageTargetID:  ctx.StorageTarget.ID,
		OutputDir:        ctx.OutputDir,
		DriverType:       commonModels.EnumType("PG_BACKREST"),
		DriverConfig:     datatypes.JSON(ctx.mustMarshalDriverConfig()),
	}
	// 对象存储仓库（repo1-type=s3）没有仓库节点，RepositoryNode 为 nil
	if ctx.RepositoryNode != nil {
		task.RepositoryNodeID = ctx.RepositoryNode.ID
	}
	return task
}

// precheckMaxRuntimeHours 读取任务「高级选项」的「单次执行最长运行时间（小时）」。
// 未配置或越界返回 0；预检本身不依赖该值，仅为保证影子任务的 driver_config 完整。
func precheckMaxRuntimeHours(form *backupForms.PrecheckBackupTaskForm) int {
	if form == nil {
		return 0
	}
	hours := anyToInt(form.DriverConfig[DriverConfigKeyMaxRuntimeHours], 0)
	if hours < maxRuntimeHoursMin || hours > maxRuntimeHoursMax {
		return 0
	}
	return hours
}

func (ctx *backupTaskPrecheckContext) mustMarshalDriverConfig() []byte {
	raw := map[string]any{
		"backup_type": ctx.DriverConfig.BackupType,
		"process_max": ctx.DriverConfig.ProcessMax,
		"start_fast":  ctx.DriverConfig.StartFast,
		"stanza":      ctx.Stanza,
		"repo_path":   ctx.RepoPath,
	}
	if ctx.MaxRuntimeHours > 0 {
		raw[DriverConfigKeyMaxRuntimeHours] = ctx.MaxRuntimeHours
	}
	if ctx.RepositoryNode != nil {
		raw["repo_node_id"] = ctx.RepositoryNode.ID
		raw["repo_host"] = ctx.RepositoryNode.Host
		raw["repo_port"] = ctx.RepositoryNode.Port
		raw["repo_user"] = defaultBackupNodeSSHUser(ctx.RepositoryNode.SSHUser)
		raw["repo_tools_dir"] = defaultBackupNodeToolsDir(ctx.RepositoryNode.ToolsDir)
		raw["repo_remote"] = !sameBackupNodeHost(ctx.ExecutorNode, ctx.RepositoryNode)
		raw["repo_ssh_password"] = decryptCredential(ctx.RepositoryNode.SSHPassword)
		raw["repo_ssh_private_key"] = decryptCredential(ctx.RepositoryNode.SSHPrivateKey)
	} else if ctx.RepoIsS3 {
		// 对象存储仓库：与 preparePGBackRestRunDriverConfig 的 S3 分支同构（AK/SK 凭据由 prepare 解密后写入正式任务）
		raw["repo_type"] = "s3"
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return []byte("{}")
	}
	return encoded
}

// #region debug-point A:pgdump-path-check-helper
func debugReportPgdumpPathCheck(hypothesisID string, location string, msg string, data map[string]any) {
	payload, err := json.Marshal(map[string]any{
		"sessionId":    "pgdump-path-check",
		"runId":        "pre-fix",
		"hypothesisId": hypothesisID,
		"location":     location,
		"msg":          "[DEBUG] " + msg,
		"data":         data,
		"ts":           time.Now().UnixMilli(),
	})
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, "http://192.168.1.3:7777/event", bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Do(req)
	if err == nil && resp != nil {
		_ = resp.Body.Close()
	}
}

// #endregion

func newBackupTaskPrecheckFailure(itemKey string, label string, message string, detail map[string]any) backupTaskPrecheckItem {
	return backupTaskPrecheckItem{
		ItemKey:  itemKey,
		Label:    label,
		Severity: backupTaskPrecheckSeverityError,
		Blocking: true,
		Passed:   false,
		Message:  message,
		Detail:   detail,
	}
}

func newBackupTaskPrecheckWarning(itemKey string, label string, message string, detail map[string]any) backupTaskPrecheckItem {
	return backupTaskPrecheckItem{
		ItemKey:  itemKey,
		Label:    label,
		Severity: backupTaskPrecheckSeverityWarning,
		Blocking: false,
		Passed:   true,
		Message:  message,
		Detail:   detail,
	}
}

func newBackupTaskPrecheckInfo(itemKey string, label string, message string, detail map[string]any) backupTaskPrecheckItem {
	return backupTaskPrecheckItem{
		ItemKey:  itemKey,
		Label:    label,
		Severity: backupTaskPrecheckSeverityInfo,
		Blocking: false,
		Passed:   true,
		Message:  message,
		Detail:   detail,
	}
}

func parseKeyValueOutput(output string) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(strings.TrimSpace(output), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return out
}

func normalizePermissionMode(value string) string {
	text := strings.TrimSpace(value)
	if text == "" || strings.EqualFold(text, "MISSING") {
		return text
	}
	text = strings.TrimLeft(text, "0")
	if text == "" {
		return "0"
	}
	if len(text) > 3 {
		text = text[len(text)-3:]
	}
	return text
}

func summarizeBackupTaskPrecheck(items []backupTaskPrecheckItem) string {
	blockingFailures := countBackupTaskPrecheckItems(items, true, false)
	warnings := countBackupTaskPrecheckWarnings(items)
	if blockingFailures > 0 {
		return fmt.Sprintf("发现 %d 个强制失败项，%d 个警告项，当前不可启动。", blockingFailures, warnings)
	}
	if warnings > 0 {
		return fmt.Sprintf("全部强制校验通过，发现 %d 个警告项，可确认启动。", warnings)
	}
	return "全部强制校验通过，当前可确认启动。"
}

func countBackupTaskPrecheckItems(items []backupTaskPrecheckItem, blocking bool, passed bool) int {
	count := 0
	for _, item := range items {
		if item.Blocking == blocking && item.Passed == passed {
			count++
		}
	}
	return count
}

func countBackupTaskPrecheckWarnings(items []backupTaskPrecheckItem) int {
	count := 0
	for _, item := range items {
		if strings.EqualFold(item.Severity, backupTaskPrecheckSeverityWarning) {
			count++
		}
	}
	return count
}

func backupTaskPrecheckPassed(items []backupTaskPrecheckItem) bool {
	for _, item := range items {
		if item.Blocking && !item.Passed {
			return false
		}
	}
	return true
}
