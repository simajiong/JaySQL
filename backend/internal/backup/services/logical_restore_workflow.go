package services

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	dasDao "github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
)

const (
	restoreExecutionModePlan    = "PLAN"
	restoreExecutionModeExecute = "EXECUTE"

	logicalRestoreModeNew       = "NEW"
	logicalRestoreModeOverwrite = "OVERWRITE"
	logicalRestoreModeAppend    = "APPEND"
)

type logicalRestoreCheckItem struct {
	Phase   string         `json:"phase"`
	ItemKey string         `json:"item_key"`
	Label   string         `json:"label"`
	Status  string         `json:"status"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Detail  map[string]any `json:"detail,omitempty"`
}

type logicalRestorePlanResult struct {
	Set            *backupModels.InsightBackupSets
	TargetInstance *commonModels.InsightInstances
	Options        logicalRestoreRunOptions
	Resolved       logicalRestoreResolvedContext
	PrecheckJSON   datatypes.JSON
	OptionsJSON    datatypes.JSON
	ResolvedJSON   datatypes.JSON
	RiskLevel      string
	Status         string
	Stage          string
	Action         string
	ErrorMessage   string
	AutoExecute    bool
	Items          []backupModels.InsightRestoreRunItems
}

type logicalRestorePlanInput struct {
	Name                    string
	BackupSetID             uint64
	TargetInstanceID        uint64
	TargetDBName            string
	TargetDBSuffix          string
	Mode                    string
	RestoreMode             string
	SelectedDatabases       []string
	SelectedSchemas         []string
	SelectedTables          []string
	Parallel                int
	Timeout                 string
	TargetCharset           string
	TargetCollation         string
	TargetEncoding          string
	CloseBinlog             *bool
	ForeignKeyChecks        *bool
	ExitOnError             *bool
	NoOwner                 *bool
	NoPrivileges            *bool
	CleanIfExists           *bool
	IsCloud                 *bool
	ExecuteConfirmed        *bool
	ObjectScope             string
	DBMappings              []logicalRestoreDBMapping
	SchemaConflictPolicy    string
	DataConflictPolicy      string
	RestoreSchema           *bool
	RestoreData             *bool
}

func buildLogicalRestorePlanInputFromCreateForm(form *backupForms.CreateRestoreRunForm) logicalRestorePlanInput {
	if form == nil {
		return logicalRestorePlanInput{}
	}
	return logicalRestorePlanInput{
		Name:                    form.Name,
		BackupSetID:             form.BackupSetID,
		TargetInstanceID:        form.TargetInstanceID,
		TargetDBName:            form.TargetDBName,
		TargetDBSuffix:          form.TargetDBSuffix,
		Mode:                    form.Mode,
		RestoreMode:             form.RestoreMode,
		SelectedDatabases:       form.SelectedDatabases,
		SelectedSchemas:         form.SelectedSchemas,
		SelectedTables:          form.SelectedTables,
		Parallel:                form.Parallel,
		Timeout:                 form.Timeout,
		TargetCharset:           form.TargetCharset,
		TargetCollation:         form.TargetCollation,
		TargetEncoding:          form.TargetEncoding,
		CloseBinlog:             form.CloseBinlog,
		ForeignKeyChecks:        form.ForeignKeyChecks,
		ExitOnError:             form.ExitOnError,
		NoOwner:                 form.NoOwner,
		NoPrivileges:            form.NoPrivileges,
		CleanIfExists:           form.CleanIfExists,
		IsCloud:                 form.IsCloud,
		ExecuteConfirmed:        form.ExecuteConfirmed,
		ObjectScope:             form.ObjectScope,
		DBMappings:              mapRestoreFormDBMappings(form.DBMappings),
		SchemaConflictPolicy:    form.SchemaConflictPolicy,
		DataConflictPolicy:      form.DataConflictPolicy,
		RestoreSchema:           form.RestoreSchema,
		RestoreData:             form.RestoreData,
	}
}

func buildLogicalRestorePlanInputFromUpdateForm(form *backupForms.UpdateRestoreRunForm) logicalRestorePlanInput {
	if form == nil {
		return logicalRestorePlanInput{}
	}
	return logicalRestorePlanInput{
		Name:                    form.Name,
		BackupSetID:             form.BackupSetID,
		TargetInstanceID:        form.TargetInstanceID,
		TargetDBName:            form.TargetDBName,
		TargetDBSuffix:          form.TargetDBSuffix,
		Mode:                    form.Mode,
		RestoreMode:             form.RestoreMode,
		SelectedDatabases:       form.SelectedDatabases,
		SelectedSchemas:         form.SelectedSchemas,
		SelectedTables:          form.SelectedTables,
		Parallel:                form.Parallel,
		Timeout:                 form.Timeout,
		TargetCharset:           form.TargetCharset,
		TargetCollation:         form.TargetCollation,
		TargetEncoding:          form.TargetEncoding,
		CloseBinlog:             form.CloseBinlog,
		ForeignKeyChecks:        form.ForeignKeyChecks,
		ExitOnError:             form.ExitOnError,
		NoOwner:                 form.NoOwner,
		NoPrivileges:            form.NoPrivileges,
		CleanIfExists:           form.CleanIfExists,
		IsCloud:                 form.IsCloud,
		ExecuteConfirmed:        form.ExecuteConfirmed,
		ObjectScope:             form.ObjectScope,
		DBMappings:              mapRestoreFormDBMappings(form.DBMappings),
		SchemaConflictPolicy:    form.SchemaConflictPolicy,
		DataConflictPolicy:      form.DataConflictPolicy,
		RestoreSchema:           form.RestoreSchema,
		RestoreData:             form.RestoreData,
	}
}

// mapRestoreFormDBMappings 归一化「配置映射」入参：去空白、丢弃空行。
func mapRestoreFormDBMappings(items []backupForms.RestoreDBMapping) []logicalRestoreDBMapping {
	if len(items) == 0 {
		return nil
	}
	out := make([]logicalRestoreDBMapping, 0, len(items))
	for _, item := range items {
		source := strings.TrimSpace(item.SourceDB)
		target := strings.TrimSpace(item.TargetDB)
		if source == "" || target == "" {
			continue
		}
		out = append(out, logicalRestoreDBMapping{SourceDB: source, TargetDB: target})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func prepareLogicalRestorePlan(input logicalRestorePlanInput, c *gin.Context) (*logicalRestorePlanResult, error) {
	// 顺序要求：必须先归一化 options（内含「冲突策略 → 底层 restore_mode」的推导），
	// 再把推导结果回写给目标校验函数。
	// 原因：向导只提交 schema_conflict_policy / data_conflict_policy 等策略字段，
	// restore_mode 由后端推导；若先校验就会出现「未填 restore_mode → 直接报错」，
	// 使策略推导永远走不到。历史调用显式传 restore_mode 时行为不变。
	options := normalizeLogicalRestoreOptions(input)
	input.RestoreMode = options.RestoreMode
	set, targetInstance, err := validateLogicalRestorePlanTarget(input)
	if err != nil {
		return nil, err
	}
	if err := validateRestoreTypeSelection(options); err != nil {
		return nil, err
	}
	setFile, err := ValidateBackupSetFile(set)
	if err != nil {
		return nil, err
	}
	// 对象存储引用（st:<id>:///path）在服务端本地没有实体文件，执行阶段会按需下载。
	backupRefIsObjectStorage := strings.HasPrefix(setFile, "st:")
	resolved := logicalRestoreResolvedContext{
		BackupFile:         setFile,
		SourceVersion:      strings.TrimSpace(set.ServerVersion),
		BackupSizeBytes:    set.SizeBytes,
		EstimatedSizeBytes: set.SizeBytes,
	}
	if !backupRefIsObjectStorage {
		if stat, statErr := os.Stat(setFile); statErr == nil {
			resolved.BackupSizeBytes = stat.Size()
			resolved.EstimatedSizeBytes = stat.Size()
		}
	}

	items := make([]logicalRestoreCheckItem, 0, 24)
	addPass := func(phase string, key string, label string, message string, detail map[string]any) {
		items = append(items, logicalRestoreCheckItem{Phase: phase, ItemKey: key, Label: label, Status: "PASS", Level: "INFO", Message: message, Detail: detail})
	}
	addWarn := func(phase string, key string, label string, message string, detail map[string]any) {
		items = append(items, logicalRestoreCheckItem{Phase: phase, ItemKey: key, Label: label, Status: "WARN", Level: "WARNING", Message: message, Detail: detail})
	}
	addBlock := func(phase string, key string, label string, message string, detail map[string]any) {
		items = append(items, logicalRestoreCheckItem{Phase: phase, ItemKey: key, Label: label, Status: "BLOCK", Level: "BLOCKER", Message: message, Detail: detail})
	}

	if backupRefIsObjectStorage {
		addPass("阶段1", "backup_file", "备份文件", "备份文件位于对象存储，执行阶段将下载到本地/目标机。", map[string]any{"path": setFile, "size_bytes": set.SizeBytes})
	} else if info, statErr := os.Stat(setFile); statErr != nil {
		addBlock("阶段1", "backup_file", "备份文件", "备份文件不可访问", map[string]any{"path": setFile, "error": statErr.Error()})
	} else if info.Size() <= 0 {
		addBlock("阶段1", "backup_file", "备份文件", "备份文件为空", map[string]any{"path": setFile})
	} else {
		addPass("阶段1", "backup_file", "备份文件", "备份文件存在且可读。", map[string]any{"path": setFile, "size_bytes": info.Size()})
	}

	appendRestoreSourceStorageCheck(set, setFile, addPass, addWarn)

	// 验证状态优先读落库字段：备份完成时已做过「写附属文件 + 回读校验」，结果写入
	// metadata_verified / checksum_verified / verified_at。
	//
	// 不再现场 find 文件的原因：DirectStream 直传模式下产物经 SSH 直写存储目标，
	// 附属文件不随行，现场查找必然失败 —— 这会让所有直传备份集在生产环境被
	// verified_backupset 一票否决（备份成功却永远无法恢复）。
	//
	// verified_at 为 NULL 表示历史备份集从未执行过验证流程，此时回退旧的现场查找逻辑，
	// 保证升级后存量备份集的可用性与升级前完全一致。
	if set.VerifiedAt != nil {
		resolved.MetadataVerified = set.MetadataVerified
		resolved.ChecksumVerified = set.ChecksumVerified
		setDetail := map[string]any{
			"source":      "backup_set_column",
			"verified_at": set.VerifiedAt.Format("2006-01-02 15:04:05"),
		}
		if resolved.MetadataVerified {
			addPass("阶段1", "metadata", "元数据", "备份集元数据已在备份完成时校验通过。", setDetail)
		} else {
			addWarn("阶段1", "metadata", "元数据", "备份完成时元数据校验未通过，当前备份集属于未完全验证状态。", setDetail)
		}
		if resolved.ChecksumVerified {
			addPass("阶段1", "checksum", "校验和", "备份集校验和已在备份完成时校验通过。", setDetail)
		} else {
			addWarn("阶段1", "checksum", "校验和", "备份完成时校验和校验未通过，将仅依赖平台登记信息。", setDetail)
		}
	} else {
		metadataPath, checksumPath := detectRestoreMetadataFiles(set, setFile)
		resolved.MetadataFile = metadataPath
		resolved.ChecksumFile = checksumPath
		metadataVerified, metadataDetail, metadataErr := validateRestoreMetadata(set, metadataPath)
		if !metadataVerified && metadataErr == nil {
			metadataDetail["searched_dirs"] = restoreArtifactSearchDirs(set, setFile)
		}
		switch {
		case metadataErr != nil:
			addBlock("阶段1", "metadata", "元数据", metadataErr.Error(), metadataDetail)
		case metadataVerified:
			resolved.MetadataVerified = true
			addPass("阶段1", "metadata", "元数据", "备份集元数据校验通过。", metadataDetail)
		default:
			addWarn("阶段1", "metadata", "元数据", "未找到可验证的元数据文件，当前备份集属于未完全验证状态。", metadataDetail)
		}

		checksumVerified, checksumDetail, checksumErr := validateRestoreChecksum(set, setFile, checksumPath)
		if !checksumVerified && checksumErr == nil {
			checksumDetail["searched_dirs"] = restoreArtifactSearchDirs(set, setFile)
		}
		switch {
		case checksumErr != nil:
			addBlock("阶段1", "checksum", "校验和", checksumErr.Error(), checksumDetail)
		case checksumVerified:
			resolved.ChecksumVerified = true
			addPass("阶段1", "checksum", "校验和", "备份文件校验和验证通过。", checksumDetail)
		default:
			addWarn("阶段1", "checksum", "校验和", "未找到可验证的校验和文件，将仅依赖平台登记信息。", checksumDetail)
		}
	}

	sources, artifactFormat, inspectErr := buildLogicalRestoreSourcePlans(set, setFile, options)
	if inspectErr != nil {
		addBlock("阶段1", "artifact_format", "备份格式", inspectErr.Error(), map[string]any{"file": set.FileName})
	} else {
		resolved.ArtifactFormat = artifactFormat
		resolved.Sources = sources
		addPass("阶段1", "artifact_format", "备份格式", "备份文件格式探测通过。", map[string]any{"format": artifactFormat, "source_count": len(sources)})
	}

	appendRestoreObjectScopeCheck(options, resolved, addPass, addWarn)

	if len(sources) > 0 {
		resolved.Commands = buildLogicalRestoreCommandPreview(set, targetInstance, options, sources, setFile)
	}

	targetVersion, versionErr := probeLogicalRestoreTargetVersion(set, targetInstance)
	if versionErr != nil {
		addBlock("阶段1", "target_connectivity", "目标实例连通性", "无法读取目标实例版本信息。", map[string]any{"error": versionErr.Error()})
	} else {
		resolved.TargetVersion = targetVersion
		addPass("阶段1", "target_connectivity", "目标实例连通性", "目标实例可连接。", map[string]any{"version": targetVersion, "host": targetInstance.Hostname, "port": targetInstance.Port})
		if sourceHigherThanTarget(set.ServerVersion, targetVersion, strings.TrimSpace(string(set.DBType))) {
			addBlock("阶段2", "version_compatibility", "版本兼容性", "不允许将高版本逻辑备份恢复到低版本目标实例。", map[string]any{"source_version": set.ServerVersion, "target_version": targetVersion})
		} else {
			addPass("阶段2", "version_compatibility", "版本兼容性", "源端与目标端版本兼容。", map[string]any{"source_version": set.ServerVersion, "target_version": targetVersion})
		}
	}

	readOnly, readOnlyDetail, readOnlyErr := detectLogicalRestoreReadOnly(set, targetInstance)
	switch {
	case readOnlyErr != nil:
		addWarn("阶段1", "read_only_probe", "只读状态", "目标实例只读状态探测失败，请人工确认。", map[string]any{"error": readOnlyErr.Error()})
	case readOnly:
		addBlock("阶段2", "read_only", "只读状态", "目标实例当前为只读，不能执行恢复导入。", readOnlyDetail)
	default:
		addPass("阶段1", "read_only", "只读状态", "目标实例允许写入。", readOnlyDetail)
	}

	appendRestorePrivilegeCheck(set, targetInstance, addPass, addWarn, addBlock)
	appendRestoreEnvironmentChecks(set, targetInstance, setFile, addPass, addWarn, addBlock)
	validateLogicalRestoreModeChecks(set, targetInstance, options, sources, addPass, addBlock, addWarn)
	validateLogicalRestoreSafetyChecks(options, addPass, addBlock, addWarn)

	riskLevel := deriveLogicalRestoreRiskLevel(options, items)
	precheckValue := gin.H{
		"summary":    summarizeLogicalRestorePrecheck(items),
		"items":      items,
		"resolved":   resolved,
		"plan":       gin.H{"commands": resolved.Commands, "sources": resolved.Sources},
		"risk_level": riskLevel,
	}
	precheckJSON, err := marshalJSONValue(precheckValue)
	if err != nil {
		return nil, err
	}
	optionsJSON, err := buildLogicalRestoreOptionsJSON(options)
	if err != nil {
		return nil, err
	}
	resolvedJSON, err := buildLogicalRestoreResolvedContextJSON(resolved)
	if err != nil {
		return nil, err
	}

	runItems := make([]backupModels.InsightRestoreRunItems, 0, len(resolved.Sources))
	for idx, source := range resolved.Sources {
		runItems = append(runItems, backupModels.InsightRestoreRunItems{
			Model:            &commonModels.Model{},
			SourceObjectName: source.SourceObjectName,
			TargetObjectName: source.TargetObjectName,
			Status:           "PENDING",
			CurrentStage:     restoreStagePending,
			DisplayOrder:     idx + 1,
		})
	}

	autoExecute := strings.EqualFold(options.ExecutionMode, restoreExecutionModeExecute)
	status := "PENDING"
	stage := restoreStagePrecheck
	action := "已生成恢复计划，等待确认执行"
	errorMessage := ""
	if hasLogicalRestoreBlocker(items) {
		status = "FAILED"
		stage = restoreStagePrecheck
		action = "预检查未通过"
		errorMessage = summarizeLogicalRestoreBlockers(items)
	} else if autoExecute {
		stage = restoreStagePending
		action = "预检查通过，等待执行"
	}
	_ = c
	return &logicalRestorePlanResult{
		Set:            set,
		TargetInstance: targetInstance,
		Options:        options,
		Resolved:       resolved,
		PrecheckJSON:   precheckJSON,
		OptionsJSON:    optionsJSON,
		ResolvedJSON:   resolvedJSON,
		RiskLevel:      riskLevel,
		Status:         status,
		Stage:          stage,
		Action:         action,
		ErrorMessage:   errorMessage,
		AutoExecute:    autoExecute && !hasLogicalRestoreBlocker(items),
		Items:          runItems,
	}, nil
}

func validateLogicalRestorePlanTarget(input logicalRestorePlanInput) (*backupModels.InsightBackupSets, *commonModels.InsightInstances, error) {
	if strings.TrimSpace(input.RestoreMode) == "" {
		return nil, nil, fmt.Errorf("restore_mode 不能为空")
	}
	set, err := LoadBackupSet(input.BackupSetID)
	if err != nil {
		return nil, nil, err
	}
	if isPostgresPhysicalDriverType(string(set.DriverType)) {
		return nil, nil, fmt.Errorf("当前接口仅支持 MySQL / PostgreSQL 逻辑备份恢复")
	}
	targetInstance, err := loadInstanceByID(input.TargetInstanceID)
	if err != nil {
		return nil, nil, err
	}
	if err := ensureRestoreHomogeneous(set, targetInstance); err != nil {
		return nil, nil, err
	}
	return set, targetInstance, nil
}

func normalizeLogicalRestoreOptions(input logicalRestorePlanInput) logicalRestoreRunOptions {
	mode := strings.ToUpper(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = restoreExecutionModePlan
	}
	restoreMode := strings.ToUpper(strings.TrimSpace(input.RestoreMode))
	options := logicalRestoreRunOptions{
		ExecutionMode:     mode,
		RestoreMode:       restoreMode,
		TargetDBName:      strings.TrimSpace(input.TargetDBName),
		TargetDBSuffix:    strings.TrimSpace(input.TargetDBSuffix),
		SelectedDatabases: uniqueStrings(input.SelectedDatabases),
		SelectedSchemas:   uniqueStrings(input.SelectedSchemas),
		SelectedTables:    uniqueStrings(input.SelectedTables),
		Parallel:          input.Parallel,
		Timeout:           strings.TrimSpace(input.Timeout),
		TargetCharset:     strings.TrimSpace(input.TargetCharset),
		TargetCollation:   strings.TrimSpace(input.TargetCollation),
		TargetEncoding:    strings.TrimSpace(input.TargetEncoding),
		DBMappings:        normalizeRestoreDBMappings(input.DBMappings),
	}
	if options.Parallel <= 0 {
		options.Parallel = 4
	}
	if options.Timeout == "" {
		options.Timeout = "4h"
	}
	options.CloseBinlog = boolValueOrDefault(input.CloseBinlog, false)
	options.ForeignKeyChecks = boolValueOrDefault(input.ForeignKeyChecks, false)
	options.ExitOnError = boolValueOrDefault(input.ExitOnError, true)
	options.NoOwner = boolValueOrDefault(input.NoOwner, true)
	options.NoPrivileges = boolValueOrDefault(input.NoPrivileges, true)
	options.CleanIfExists = boolValueOrDefault(input.CleanIfExists, false)
	options.IsCloud = boolValueOrDefault(input.IsCloud, false)
	options.ExecuteConfirmed = boolValueOrDefault(input.ExecuteConfirmed, false)

	// 恢复类型：默认「表结构 + 全量数据」都恢复（与历史行为一致）。
	options.RestoreSchema = boolValueOrDefault(input.RestoreSchema, true)
	options.RestoreData = boolValueOrDefault(input.RestoreData, true)

	// 恢复对象范围：显式优先，其次按是否选了部分库推断。
	scope := strings.ToUpper(strings.TrimSpace(input.ObjectScope))
	switch scope {
	case restoreObjectScopeCustom:
		options.ObjectScope = restoreObjectScopeCustom
	case restoreObjectScopeAll:
		options.ObjectScope = restoreObjectScopeAll
	default:
		if len(options.SelectedDatabases) > 0 || len(options.DBMappings) > 0 {
			options.ObjectScope = restoreObjectScopeCustom
		} else {
			options.ObjectScope = restoreObjectScopeAll
		}
	}

	// 冲突策略：显式优先，其次由底层 restore_mode 反推（历史任务）。
	options.SchemaConflictPolicy = normalizeConflictPolicy(input.SchemaConflictPolicy, deriveSchemaConflictPolicy(options.RestoreMode), restoreSchemaConflictSkip, restoreSchemaConflictAbort)
	options.DataConflictPolicy = normalizeConflictPolicy(input.DataConflictPolicy, deriveDataConflictPolicy(options.RestoreMode), restoreDataConflictSkip, restoreDataConflictTruncate)

	// 底层 restore_mode：显式给定优先；否则由两个冲突策略推导，保证执行链路不变。
	if options.RestoreMode == "" {
		options.RestoreMode = deriveRestoreModeFromPolicies(options.SchemaConflictPolicy, options.DataConflictPolicy)
	}

	if options.TargetDBSuffix == "" && len(options.SelectedDatabases) > 1 {
		options.TargetDBSuffix = options.TargetDBName
	}
	return options
}

func normalizeConflictPolicy(explicit string, fallback string, allowed ...string) string {
	value := strings.ToUpper(strings.TrimSpace(explicit))
	if value != "" {
		for _, item := range allowed {
			if value == item {
				return value
			}
		}
	}
	fallback = strings.ToUpper(strings.TrimSpace(fallback))
	for _, item := range allowed {
		if fallback == item {
			return fallback
		}
	}
	if len(allowed) > 0 {
		return allowed[0]
	}
	return ""
}

func normalizeRestoreDBMappings(items []logicalRestoreDBMapping) []logicalRestoreDBMapping {
	if len(items) == 0 {
		return nil
	}
	out := make([]logicalRestoreDBMapping, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		source := strings.TrimSpace(item.SourceDB)
		target := strings.TrimSpace(item.TargetDB)
		if source == "" || target == "" {
			continue
		}
		key := strings.ToLower(source)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, logicalRestoreDBMapping{SourceDB: source, TargetDB: target})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validateRestoreTypeSelection 校验恢复类型至少选一项，否则整个恢复没有任何意义。
func validateRestoreTypeSelection(options logicalRestoreRunOptions) error {
	if !options.RestoreSchema && !options.RestoreData {
		return fmt.Errorf("恢复类型至少需要选择「表结构」或「全量数据」其中一项")
	}
	return nil
}

func boolValueOrDefault(v *bool, defaultValue bool) bool {
	if v == nil {
		return defaultValue
	}
	return *v
}

// restoreArtifactSearchDirs 返回备份集附属文件（metadata.json / checksums.sha256）的候选目录：
// 产物所在目录 + 备份任务输出目录，去重且忽略空值。
func restoreArtifactSearchDirs(set *backupModels.InsightBackupSets, setFile string) []string {
	candidates := []string{
		filepath.Dir(strings.TrimSpace(setFile)),
		strings.TrimSpace(set.OutputDir),
	}
	out := make([]string, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, base := range candidates {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		if _, ok := seen[base]; ok {
			continue
		}
		seen[base] = struct{}{}
		out = append(out, base)
	}
	return out
}

func detectRestoreMetadataFiles(set *backupModels.InsightBackupSets, setFile string) (string, string) {
	findFirst := func(names []string) string {
		for _, base := range restoreArtifactSearchDirs(set, setFile) {
			for _, name := range names {
				candidate := filepath.Join(base, name)
				if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
					return candidate
				}
			}
		}
		return ""
	}
	return findFirst([]string{"metadata.json", "metadata.txt"}), findFirst([]string{"checksums.sha256", "md5sum.txt"})
}

func validateRestoreMetadata(set *backupModels.InsightBackupSets, metadataPath string) (bool, map[string]any, error) {
	detail := map[string]any{}
	if strings.TrimSpace(metadataPath) == "" {
		return false, detail, nil
	}
	detail["path"] = metadataPath
	buf, err := os.ReadFile(metadataPath)
	if err != nil {
		return false, detail, fmt.Errorf("读取 metadata 文件失败: %w", err)
	}
	if !strings.HasSuffix(strings.ToLower(metadataPath), ".json") {
		// 纯文本元数据无法做字段级校验，不能计为验证通过
		detail["reason"] = "metadata.txt 为纯文本，无法校验关键字段"
		return false, detail, nil
	}
	var raw map[string]any
	if err := json.Unmarshal(buf, &raw); err != nil {
		return false, detail, fmt.Errorf("metadata.json 解析失败: %w", err)
	}
	// 必须用 key 存在性取值：raw 里没有该键时 fmt.Sprintf("%v", nil) 会得到字符串 "<nil>"，
	// 那样历史/手写 metadata 会被误判成「声明了非法的 backup_type」而阻断恢复。
	backupType := restoreMetadataStringField(raw, "backup_type")
	sourceDBType := restoreMetadataStringField(raw, "source_db_type")
	if backupType == "" && sourceDBType == "" {
		// 元数据里没有任何可校验字段（历史文件或手写文件），同样视为未验证
		detail["reason"] = "metadata.json 缺少 backup_type / source_db_type"
		return false, detail, nil
	}
	if backupType != "" && !strings.EqualFold(backupType, "logical") {
		return false, detail, fmt.Errorf("metadata 声明的 backup_type=%s，不属于逻辑备份", backupType)
	}
	if sourceDBType != "" && !strings.EqualFold(sourceDBType, string(set.DBType)) {
		return false, detail, fmt.Errorf("metadata 中的 source_db_type=%s 与备份集类型不一致", sourceDBType)
	}
	detail["backup_type"] = backupType
	detail["source_db_type"] = sourceDBType
	return true, detail, nil
}

// restoreMetadataStringField 按 key 存在性取字符串字段，缺失一律返回空串（而非 "<nil>"）。
func restoreMetadataStringField(raw map[string]any, key string) string {
	value, ok := raw[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func validateRestoreChecksum(set *backupModels.InsightBackupSets, setFile string, checksumPath string) (bool, map[string]any, error) {
	detail := map[string]any{"artifact": filepath.Base(setFile)}
	if strings.TrimSpace(checksumPath) == "" && strings.TrimSpace(set.Sha256) == "" {
		return false, detail, nil
	}
	sha, size, err := fileSha256(setFile)
	if err != nil {
		return false, detail, fmt.Errorf("计算备份文件 sha256 失败: %w", err)
	}
	detail["actual_sha256"] = sha
	detail["size_bytes"] = size
	if strings.TrimSpace(set.Sha256) != "" {
		detail["registered_sha256"] = strings.TrimSpace(set.Sha256)
		if !strings.EqualFold(strings.TrimSpace(set.Sha256), sha) {
			return false, detail, fmt.Errorf("备份文件 sha256 与平台登记值不一致")
		}
	}
	if strings.TrimSpace(checksumPath) != "" {
		detail["checksum_file"] = checksumPath
		buf, err := os.ReadFile(checksumPath)
		if err != nil {
			return false, detail, fmt.Errorf("读取 checksums 文件失败: %w", err)
		}
		expected := parseChecksumFromFile(string(buf), filepath.Base(setFile))
		if expected == "" {
			// 校验和文件里没有本产物的条目：等于没验证，不能计为通过
			detail["reason"] = "checksums 文件中未包含该备份产物的条目"
			return false, detail, nil
		}
		detail["file_sha256"] = expected
		if !strings.EqualFold(expected, sha) {
			return false, detail, fmt.Errorf("备份文件 sha256 与 checksums 文件不一致")
		}
	}
	return true, detail, nil
}

func parseChecksumFromFile(content string, fileName string) string {
	target := strings.TrimSpace(fileName)
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == target {
			return strings.TrimSpace(fields[0])
		}
	}
	return ""
}

func buildLogicalRestoreSourcePlans(set *backupModels.InsightBackupSets, setFile string, options logicalRestoreRunOptions) ([]logicalRestoreSourcePlan, string, error) {
	switch {
	case strings.EqualFold(strings.TrimSpace(string(set.DBType)), "MySQL"):
		return buildMySQLRestoreSourcePlans(set, setFile, options)
	case isPostgresLogicalFamily(string(set.DBType)):
		return buildPostgresRestoreSourcePlans(set, setFile, options)
	default:
		return nil, "", fmt.Errorf("不支持的 DB 类型")
	}
}

// resolveMySQLSingleFileTarget 解析 MySQL 单文件备份（.sql / .sql.gz）的目标库名：
// 全实例备份返回空串（表示按 dump 内原库名导入）；否则优先 target_db_name，
// 其次使用「配置映射」中的单条映射目标库，保持与向导交互一致。
func resolveMySQLSingleFileTarget(set *backupModels.InsightBackupSets, options logicalRestoreRunOptions) string {
	if set == nil || strings.EqualFold(strings.TrimSpace(string(set.ObjectMode)), "ALL") {
		return ""
	}
	if direct := strings.TrimSpace(options.TargetDBName); direct != "" {
		return direct
	}
	if len(options.DBMappings) == 1 {
		return strings.TrimSpace(options.DBMappings[0].TargetDB)
	}
	return ""
}

func buildMySQLRestoreSourcePlans(set *backupModels.InsightBackupSets, setFile string, options logicalRestoreRunOptions) ([]logicalRestoreSourcePlan, string, error) {
	lower := strings.ToLower(strings.TrimSpace(setFile))
	switch {
	case strings.HasSuffix(lower, ".zip"):
		plans, err := inspectMySQLRestoreZipSources(setFile, options)
		return plans, "zip", err
	case strings.HasSuffix(lower, ".sql.gz"):
		target := resolveMySQLSingleFileTarget(set, options)
		if target == "" && !strings.EqualFold(strings.TrimSpace(string(set.ObjectMode)), "ALL") {
			return nil, "", fmt.Errorf("MySQL 单库逻辑恢复必须指定 target_db_name")
		}
		if err := validateMySQLDumpFile(setFile); err != nil {
			return nil, "", err
		}
		return []logicalRestoreSourcePlan{{
			SourceObjectName: firstNonEmpty(strings.TrimSpace(set.SourceDBName), strings.TrimSpace(set.ObjectSummary), strings.TrimSpace(set.Name)),
			TargetObjectName: target,
			Format:           "sql.gz",
			AllDatabases:     strings.EqualFold(strings.TrimSpace(string(set.ObjectMode)), "ALL"),
		}}, "sql.gz", nil
	default:
		if err := validateMySQLDumpFile(setFile); err != nil {
			return nil, "", err
		}
		target := resolveMySQLSingleFileTarget(set, options)
		if target == "" && !strings.EqualFold(strings.TrimSpace(string(set.ObjectMode)), "ALL") {
			return nil, "", fmt.Errorf("MySQL 单库逻辑恢复必须指定 target_db_name")
		}
		return []logicalRestoreSourcePlan{{
			SourceObjectName: firstNonEmpty(strings.TrimSpace(set.SourceDBName), strings.TrimSpace(set.ObjectSummary), strings.TrimSpace(set.Name)),
			TargetObjectName: target,
			Format:           "sql",
			AllDatabases:     strings.EqualFold(strings.TrimSpace(string(set.ObjectMode)), "ALL"),
		}}, "sql", nil
	}
}

// resolvePostgresSingleFileTarget 解析 PG 单对象备份（directory / custom / plain）的目标库名：
// 优先 target_db_name，其次「配置映射」单条映射，最后回落备份集登记的源库名。
func resolvePostgresSingleFileTarget(set *backupModels.InsightBackupSets, options logicalRestoreRunOptions) string {
	if direct := strings.TrimSpace(options.TargetDBName); direct != "" {
		return direct
	}
	if len(options.DBMappings) == 1 {
		if mapped := strings.TrimSpace(options.DBMappings[0].TargetDB); mapped != "" {
			return mapped
		}
	}
	if set != nil {
		return strings.TrimSpace(set.SourceDBName)
	}
	return ""
}

func buildPostgresRestoreSourcePlans(set *backupModels.InsightBackupSets, setFile string, options logicalRestoreRunOptions) ([]logicalRestoreSourcePlan, string, error) {
	info, err := os.Stat(setFile)
	if err == nil && info.IsDir() {
		if _, err := os.Stat(filepath.Join(setFile, "toc.dat")); err != nil {
			return nil, "", fmt.Errorf("PostgreSQL directory 备份缺少 toc.dat")
		}
		target := resolvePostgresSingleFileTarget(set, options)
		if target == "" {
			return nil, "", fmt.Errorf("PostgreSQL 目录备份恢复必须指定 target_db_name")
		}
		return []logicalRestoreSourcePlan{{
			SourceObjectName: firstNonEmpty(strings.TrimSpace(set.SourceDBName), strings.TrimSpace(set.ObjectSummary), strings.TrimSpace(set.Name)),
			TargetObjectName: target,
			Format:           "directory",
			IsDir:            true,
			SelectedSchemas:  uniqueStrings(options.SelectedSchemas),
			SelectedTables:   uniqueStrings(options.SelectedTables),
		}}, "directory", nil
	}
	lower := strings.ToLower(strings.TrimSpace(setFile))
	switch {
	case strings.HasSuffix(lower, ".zip"):
		sources, err := inspectPostgresLogicalRestoreSources(setFile)
		if err != nil {
			return nil, "", err
		}
		selectedDBs := map[string]struct{}{}
		for _, item := range options.SelectedDatabases {
			selectedDBs[strings.TrimSpace(item)] = struct{}{}
		}
		plans := make([]logicalRestoreSourcePlan, 0, len(sources))
		suffix := strings.TrimSpace(options.TargetDBSuffix)
		if suffix == "" {
			suffix = strings.TrimSpace(options.TargetDBName)
		}
		for _, source := range sources {
			if len(selectedDBs) > 0 {
				if _, ok := selectedDBs[strings.TrimSpace(source.SourceDBName)]; !ok {
					continue
				}
			}
			targetDB, buildErr := buildPostgresRestoreTargetName(source.SourceDBName, suffix)
			if mapped, ok := logicalRestoreTargetForSource(options, source.SourceDBName); ok {
				targetDB, buildErr = mapped, validateIdent(mapped, "目标库名")
			}
			if buildErr != nil {
				return nil, "", buildErr
			}
			plans = append(plans, logicalRestoreSourcePlan{
				SourceObjectName: source.SourceDBName,
				TargetObjectName: targetDB,
				EntryName:        source.EntryName,
				Format:           ternaryString(source.IsDir, "directory", "custom"),
				IsDir:            source.IsDir,
			})
		}
		if len(plans) == 0 {
			return nil, "", fmt.Errorf("未命中任何 PostgreSQL 恢复对象，请检查 selected_databases")
		}
		return plans, "zip", nil
	case strings.HasSuffix(lower, ".sql"), strings.HasSuffix(lower, ".sql.gz"):
		target := resolvePostgresSingleFileTarget(set, options)
		if target == "" {
			return nil, "", fmt.Errorf("PostgreSQL plain SQL 恢复必须指定 target_db_name")
		}
		format := "plain"
		if strings.HasSuffix(lower, ".sql.gz") {
			format = "plain.gz"
		}
		if len(options.SelectedSchemas)+len(options.SelectedTables) > 0 {
			return nil, "", fmt.Errorf("PostgreSQL plain SQL 备份暂不支持按 schema/table 二次筛选恢复")
		}
		return []logicalRestoreSourcePlan{{
			SourceObjectName: firstNonEmpty(strings.TrimSpace(set.SourceDBName), strings.TrimSpace(set.ObjectSummary), strings.TrimSpace(set.Name)),
			TargetObjectName: target,
			Format:           format,
		}}, format, nil
	default:
		if isPostgresCustomDumpFile(setFile) {
			target := strings.TrimSpace(options.TargetDBName)
			if target == "" {
				target = strings.TrimSpace(set.SourceDBName)
			}
			if target == "" {
				return nil, "", fmt.Errorf("PostgreSQL 逻辑恢复必须指定 target_db_name")
			}
			return []logicalRestoreSourcePlan{{
				SourceObjectName: firstNonEmpty(strings.TrimSpace(set.SourceDBName), strings.TrimSpace(set.ObjectSummary), strings.TrimSpace(set.Name)),
				TargetObjectName: target,
				Format:           "custom",
				SelectedSchemas:  uniqueStrings(options.SelectedSchemas),
				SelectedTables:   uniqueStrings(options.SelectedTables),
			}}, "custom", nil
		}
		return nil, "", fmt.Errorf("无法识别 PostgreSQL 备份格式：%s", filepath.Base(setFile))
	}
}

func inspectMySQLRestoreZipSources(zipPath string, options logicalRestoreRunOptions) ([]logicalRestoreSourcePlan, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	selected := map[string]struct{}{}
	for _, item := range options.SelectedDatabases {
		selected[strings.TrimSpace(item)] = struct{}{}
	}
	plans := make([]logicalRestoreSourcePlan, 0)
	seen := map[string]struct{}{}
	for _, file := range r.File {
		name := filepath.ToSlash(strings.TrimSpace(file.Name))
		if name == "" || strings.HasSuffix(name, "/") || strings.Count(name, "/") > 0 {
			continue
		}
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".sql") && !strings.HasSuffix(lower, ".sql.gz") {
			continue
		}
		sourceDB := strings.TrimSuffix(strings.TrimSuffix(name, ".gz"), ".sql")
		if len(selected) > 0 {
			if _, ok := selected[sourceDB]; !ok {
				continue
			}
		}
		if _, ok := seen[sourceDB]; ok {
			continue
		}
		seen[sourceDB] = struct{}{}
		targetDB := sourceDB
		if mapped, ok := logicalRestoreTargetForSource(options, sourceDB); ok {
			targetDB = mapped
		} else if strings.TrimSpace(options.TargetDBSuffix) != "" {
			targetDB = sourceDB + strings.TrimSpace(options.TargetDBSuffix)
		}
		plans = append(plans, logicalRestoreSourcePlan{
			SourceObjectName: sourceDB,
			TargetObjectName: targetDB,
			EntryName:        name,
			Format:           ternaryString(strings.HasSuffix(lower, ".gz"), "sql.gz", "sql"),
		})
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].SourceObjectName < plans[j].SourceObjectName })
	if len(plans) == 0 {
		return nil, fmt.Errorf("未在 MySQL zip 备份集中识别到可恢复的 SQL 文件")
	}
	return plans, nil
}

func validateMySQLDumpFile(path string) error {
	// 对象存储引用（st:<id>:///path）在服务端本地没有实体文件，执行阶段下载到本地后再做头校验；
	// 预检查阶段已通过 metadata/checksum 落库字段确认备份集有效性。
	if strings.HasPrefix(strings.TrimSpace(path), "st:") {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(path))
	if strings.HasSuffix(lower, ".gz") {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("MySQL gzip 备份不可解析: %w", err)
		}
		defer gz.Close()
		header := make([]byte, 256)
		n, _ := gz.Read(header)
		text := string(header[:n])
		if !strings.Contains(text, "MySQL dump") && !strings.Contains(text, "MariaDB dump") && !strings.Contains(strings.ToUpper(text), "CREATE") {
			return fmt.Errorf("MySQL dump 文件头校验失败")
		}
		return nil
	}
	// 只读取文件头 512 字节做校验，避免把数 GB 级备份文件整读进内存导致 OOM
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	sample := string(buf[:n])
	if !strings.Contains(sample, "MySQL dump") && !strings.Contains(sample, "MariaDB dump") && !strings.Contains(strings.ToUpper(sample), "CREATE") {
		return fmt.Errorf("MySQL dump 文件头校验失败")
	}
	return nil
}

func isPostgresCustomDumpFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 5)
	if _, err := io.ReadFull(f, buf); err != nil {
		return false
	}
	return string(buf) == "PGDMP"
}

func probeLogicalRestoreTargetVersion(set *backupModels.InsightBackupSets, targetInstance *commonModels.InsightInstances) (string, error) {
	if set == nil || targetInstance == nil {
		return "", fmt.Errorf("恢复目标信息不完整")
	}
	switch {
	case strings.EqualFold(strings.TrimSpace(string(set.DBType)), "MySQL"):
		return probeMySQLVersion(targetInstance.Hostname, targetInstance.Port, targetInstance.User, targetInstance.Password)
	case isPostgresLogicalFamily(string(set.DBType)):
		extra := parseExtraConfig(targetInstance.ExtraConfig)
		return probePostgresVersion(targetInstance.Hostname, targetInstance.Port, targetInstance.User, targetInstance.Password, firstNonEmpty(extra["database"], logicalDefaultDatabase(string(set.DBType))), extra["sslmode"])
	default:
		return "", fmt.Errorf("不支持的 DB 类型")
	}
}

func detectLogicalRestoreReadOnly(set *backupModels.InsightBackupSets, targetInstance *commonModels.InsightInstances) (bool, map[string]any, error) {
	detail := map[string]any{}
	if set == nil || targetInstance == nil {
		return false, detail, fmt.Errorf("目标实例为空")
	}
	switch {
	case strings.EqualFold(strings.TrimSpace(string(set.DBType)), "MySQL"):
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		db := dasDao.DB{
			User:     targetInstance.User,
			Password: targetInstance.Password,
			Host:     targetInstance.Hostname,
			Port:     targetInstance.Port,
			Params:   map[string]string{"charset": "utf8mb4", "parseTime": "True", "loc": "Local"},
			Ctx:      ctx,
		}
		_, rows, err := db.Query("select @@global.read_only as read_only, @@global.super_read_only as super_read_only")
		if err != nil {
			return false, detail, err
		}
		if rows == nil || len(*rows) == 0 {
			return false, detail, fmt.Errorf("未读取到 MySQL 只读状态")
		}
		row := (*rows)[0]
		detail["read_only"] = fmt.Sprintf("%v", row["read_only"])
		detail["super_read_only"] = fmt.Sprintf("%v", row["super_read_only"])
		return isTruthLike(row["read_only"]) || isTruthLike(row["super_read_only"]), detail, nil
	case isPostgresLogicalFamily(string(set.DBType)):
		extra := parseExtraConfig(targetInstance.ExtraConfig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		db := dasDao.PostgresDB{
			User:     targetInstance.User,
			Password: targetInstance.Password,
			Host:     targetInstance.Hostname,
			Port:     targetInstance.Port,
			Database: firstNonEmpty(extra["database"], logicalDefaultDatabase(string(set.DBType))),
			SSLMode:  extra["sslmode"],
			Ctx:      ctx,
		}
		_, rows, err := db.Query("select current_setting('transaction_read_only', true) as transaction_read_only, pg_is_in_recovery()::text as in_recovery")
		if err != nil {
			return false, detail, err
		}
		if rows == nil || len(*rows) == 0 {
			return false, detail, fmt.Errorf("未读取到 %s 只读状态", logicalDBLabel(string(set.DBType)))
		}
		row := (*rows)[0]
		detail["transaction_read_only"] = fmt.Sprintf("%v", row["transaction_read_only"])
		detail["in_recovery"] = fmt.Sprintf("%v", row["in_recovery"])
		return isTruthLike(row["transaction_read_only"]) || isTruthLike(row["in_recovery"]), detail, nil
	default:
		return false, detail, fmt.Errorf("不支持的 DB 类型")
	}
}

func isTruthLike(v any) bool {
	text := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", v)))
	return text == "1" || text == "on" || text == "true" || text == "yes"
}

func validateLogicalRestoreModeChecks(set *backupModels.InsightBackupSets, targetInstance *commonModels.InsightInstances, options logicalRestoreRunOptions, sources []logicalRestoreSourcePlan, addPass func(string, string, string, string, map[string]any), addBlock func(string, string, string, string, map[string]any), addWarn func(string, string, string, string, map[string]any)) {
	if len(sources) == 0 {
		return
	}
	for _, source := range sources {
		if strings.EqualFold(strings.TrimSpace(string(set.DBType)), "MySQL") && source.AllDatabases && strings.EqualFold(options.RestoreMode, logicalRestoreModeOverwrite) {
			addBlock("阶段2", "restore_mode_mysql_all", "恢复模式", "MySQL 全实例 SQL 备份暂不支持自动覆盖恢复，请改为 NEW/APPEND 或使用分库 zip 备份。", map[string]any{"source": source.SourceObjectName})
			continue
		}
		exists, detail, err := logicalRestoreTargetExists(set, targetInstance, source.TargetObjectName)
		if err != nil {
			addWarn("阶段1", "target_exists_probe", "目标对象探测", "目标对象存在性探测失败，请人工确认。", map[string]any{"target": source.TargetObjectName, "error": err.Error()})
			continue
		}
		detail["target"] = source.TargetObjectName
		switch options.RestoreMode {
		case logicalRestoreModeNew:
			if source.AllDatabases {
				addPass("阶段2", "restore_mode_new", "恢复模式", "全实例恢复将按备份内原库名导入。", detail)
				continue
			}
			if exists {
				addBlock("阶段2", "target_exists", "目标对象存在性", "NEW 模式要求目标库不存在。", detail)
			} else {
				addPass("阶段2", "target_exists", "目标对象存在性", "目标库不存在，满足 NEW 模式要求。", detail)
			}
		case logicalRestoreModeOverwrite:
			if !exists && !source.AllDatabases {
				addWarn("阶段2", "target_exists", "目标对象存在性", "OVERWRITE 模式下目标库当前不存在，将退化为新建恢复。", detail)
			} else {
				addPass("阶段2", "target_exists", "目标对象存在性", "目标库存在，可执行覆盖恢复。", detail)
			}
		case logicalRestoreModeAppend:
			if source.AllDatabases {
				addPass("阶段2", "target_exists", "目标对象存在性", "全实例追加恢复将直接导入备份中的 SQL。", detail)
				continue
			}
			if !exists {
				addPass("阶段2", "target_exists", "目标对象存在性", "目标库当前不存在，将在恢复时自动创建。", detail)
			} else {
				addPass("阶段2", "target_exists", "目标对象存在性", "目标库已存在，将按冲突策略导入。", detail)
			}
		}
	}
}

func validateLogicalRestoreSafetyChecks(options logicalRestoreRunOptions, addPass func(string, string, string, string, map[string]any), addBlock func(string, string, string, string, map[string]any), addWarn func(string, string, string, string, map[string]any)) {
	if strings.EqualFold(options.ExecutionMode, restoreExecutionModeExecute) {
		if !options.ExecuteConfirmed {
			addBlock("阶段0", "execute_confirmed", "执行确认", "执行模式必须显式提供 execute_confirmed=true。", nil)
		} else {
			addPass("阶段0", "execute_confirmed", "执行确认", "执行确认已提供。", nil)
		}
	} else {
		addPass("阶段0", "mode", "计划模式", "当前为计划模式，仅生成恢复计划与命令预览。", nil)
	}
	if options.CloseBinlog {
		addWarn("阶段2", "close_binlog", "Binlog 开关", "已请求关闭 binlog，请确认不存在主从或审计影响。", nil)
	}
	if options.CleanIfExists {
		addWarn("阶段2", "clean_if_exists", "清理选项", "已启用 clean_if_exists，执行阶段将按覆盖模式处理目标对象。", nil)
	}
}

func logicalRestoreTargetExists(set *backupModels.InsightBackupSets, targetInstance *commonModels.InsightInstances, targetDB string) (bool, map[string]any, error) {
	detail := map[string]any{}
	if strings.TrimSpace(targetDB) == "" {
		return false, detail, nil
	}
	switch {
	case strings.EqualFold(strings.TrimSpace(string(set.DBType)), "MySQL"):
		exists, err := mysqlDatabaseExists(targetInstance.Hostname, targetInstance.Port, targetInstance.User, targetInstance.Password, targetDB)
		return exists, detail, err
	case isPostgresLogicalFamily(string(set.DBType)):
		extra := parseExtraConfig(targetInstance.ExtraConfig)
		exists, err := postgresDatabaseExists(targetInstance.Hostname, targetInstance.Port, targetInstance.User, targetInstance.Password, firstNonEmpty(extra["database"], logicalDefaultDatabase(string(set.DBType))), extra["sslmode"], targetDB)
		return exists, detail, err
	default:
		return false, detail, fmt.Errorf("不支持的 DB 类型")
	}
}

func summarizeLogicalRestorePrecheck(items []logicalRestoreCheckItem) map[string]any {
	summary := map[string]any{
		"total":    len(items),
		"passed":   0,
		"warnings": 0,
		"blocked":  0,
	}
	for _, item := range items {
		switch strings.ToUpper(strings.TrimSpace(item.Status)) {
		case "PASS":
			summary["passed"] = summary["passed"].(int) + 1
		case "WARN":
			summary["warnings"] = summary["warnings"].(int) + 1
		case "BLOCK":
			summary["blocked"] = summary["blocked"].(int) + 1
		}
	}
	summary["passed_all"] = summary["blocked"].(int) == 0
	return summary
}

func hasLogicalRestoreBlocker(items []logicalRestoreCheckItem) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Status), "BLOCK") {
			return true
		}
	}
	return false
}

func summarizeLogicalRestoreBlockers(items []logicalRestoreCheckItem) string {
	messages := make([]string, 0, 4)
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Status), "BLOCK") {
			messages = append(messages, item.Message)
		}
		if len(messages) >= 4 {
			break
		}
	}
	if len(messages) == 0 {
		return ""
	}
	return strings.Join(messages, "；")
}

func deriveLogicalRestoreRiskLevel(options logicalRestoreRunOptions, items []logicalRestoreCheckItem) string {
	if strings.EqualFold(options.RestoreMode, logicalRestoreModeOverwrite) {
		return "HIGH"
	}
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Status), "WARN") {
			return "MEDIUM"
		}
	}
	return "LOW"
}

func buildLogicalRestoreCommandPreview(set *backupModels.InsightBackupSets, targetInstance *commonModels.InsightInstances, options logicalRestoreRunOptions, sources []logicalRestoreSourcePlan, setFile string) []string {
	commands := make([]string, 0, len(sources)*2)
	for _, source := range sources {
		switch {
		case strings.EqualFold(strings.TrimSpace(string(set.DBType)), "MySQL"):
			if source.TargetObjectName != "" {
				if strings.EqualFold(options.RestoreMode, logicalRestoreModeOverwrite) {
					commands = append(commands, fmt.Sprintf("mysql --defaults-extra-file=/path/to/my.cnf -h %s -P %d -e %q", targetInstance.Hostname, targetInstance.Port, buildMySQLOverwriteSQL(source.TargetObjectName, options)))
				} else if strings.EqualFold(options.RestoreMode, logicalRestoreModeNew) {
					commands = append(commands, fmt.Sprintf("mysql --defaults-extra-file=/path/to/my.cnf -h %s -P %d -e %q", targetInstance.Hostname, targetInstance.Port, buildMySQLCreateDBSQL(source.TargetObjectName, options)))
				}
			}
			importCmd := fmt.Sprintf("mysql --defaults-extra-file=/path/to/my.cnf -h %s -P %d", targetInstance.Hostname, targetInstance.Port)
			if source.TargetObjectName != "" {
				importCmd += " " + shellQuote(source.TargetObjectName)
			}
			if strings.EqualFold(source.Format, "sql.gz") {
				importCmd = fmt.Sprintf("gzip -dc %s | %s", shellQuote(resolveSourcePreviewPath(setFile, source)), importCmd)
			} else {
				importCmd = fmt.Sprintf("%s < %s", importCmd, shellQuote(resolveSourcePreviewPath(setFile, source)))
			}
			commands = append(commands, importCmd)
		case isPostgresLogicalFamily(string(set.DBType)):
			// 预览命令里的客户端名要跟着库类型走：金仓是 ksql / sys_restore
			queryTool := logicalToolName(string(set.DBType), logicalToolRoleQuery)
			adminDB := logicalDefaultDatabase(string(set.DBType))
			if source.TargetObjectName != "" {
				if strings.EqualFold(options.RestoreMode, logicalRestoreModeOverwrite) {
					commands = append(commands, fmt.Sprintf("%s \"host=%s port=%d dbname=%s user=%s\" -v ON_ERROR_STOP=1 -c %q", queryTool, targetInstance.Hostname, targetInstance.Port, adminDB, targetInstance.User, buildPostgresOverwriteSQL(source.TargetObjectName, options)))
				} else if strings.EqualFold(options.RestoreMode, logicalRestoreModeNew) {
					commands = append(commands, fmt.Sprintf("%s \"host=%s port=%d dbname=%s user=%s\" -v ON_ERROR_STOP=1 -c %q", queryTool, targetInstance.Hostname, targetInstance.Port, adminDB, targetInstance.User, buildPostgresCreateDBSQL(source.TargetObjectName, options)))
				}
			}
			restoreCmd := buildPostgresRestorePreviewCommand(string(set.DBType), targetInstance, options, source, resolveSourcePreviewPath(setFile, source))
			commands = append(commands, restoreCmd)
		}
	}
	return commands
}

func resolveSourcePreviewPath(setFile string, source logicalRestoreSourcePlan) string {
	if strings.TrimSpace(source.EntryName) == "" {
		return setFile
	}
	return filepath.ToSlash(filepath.Join(filepath.Dir(setFile), source.EntryName))
}

func sourceHigherThanTarget(sourceVersion string, targetVersion string, dbType string) bool {
	sourceParts := versionParts(sourceVersion)
	targetParts := versionParts(targetVersion)
	if len(sourceParts) == 0 || len(targetParts) == 0 {
		return false
	}
	if isPostgresLogicalFamily(dbType) {
		// PG 家族只比较 major（16 → 15 允许，15 → 16 拒绝）
		return sourceParts[0] > targetParts[0]
	}
	maxLen := len(sourceParts)
	if len(targetParts) > maxLen {
		maxLen = len(targetParts)
	}
	for i := 0; i < maxLen; i++ {
		sourcePart := 0
		targetPart := 0
		if i < len(sourceParts) {
			sourcePart = sourceParts[i]
		}
		if i < len(targetParts) {
			targetPart = targetParts[i]
		}
		if sourcePart == targetPart {
			continue
		}
		return sourcePart > targetPart
	}
	return false
}

func versionParts(raw string) []int {
	text := strings.TrimSpace(raw)
	prefixBuilder := strings.Builder{}
	started := false
	for _, ch := range text {
		if ch >= '0' && ch <= '9' {
			prefixBuilder.WriteRune(ch)
			started = true
			continue
		}
		if started && ch == '.' {
			prefixBuilder.WriteRune(ch)
			continue
		}
		if started {
			break
		}
	}
	prefix := strings.Trim(strings.TrimSpace(prefixBuilder.String()), ".")
	if prefix == "" {
		return nil
	}
	parts := strings.Split(prefix, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		value := 0
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				break
			}
			value = value*10 + int(ch-'0')
		}
		out = append(out, value)
	}
	return out
}

func executeLogicalRestoreRun(parent context.Context, rr backupModels.InsightRestoreRuns, logDir string, logPath string, logWriter io.Writer, runControl *restoreRunControl) error {
	set, err := LoadBackupSet(rr.BackupSetID)
	if err != nil {
		return err
	}
	targetInstance, err := loadInstanceByID(rr.TargetInstanceID)
	if err != nil {
		return err
	}
	options := decodeLogicalRestoreOptionsJSON(rr.Options)
	resolved := decodeLogicalRestoreResolvedContextJSON(rr.ResolvedContext)
	timeout := parseLogicalRestoreTimeout(options.Timeout)
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// 逻辑恢复在服务端进程内执行（无执行节点），状态仅在 10/20/65/100 四个节点更新。
	// 单库大文件导入可能持续数分钟，期间 last_report_at 停滞会触发失联误判
	// （前端「未收到执行节点心跳」/ 终止守护 restore_stop_state.go）。这里起周期
	// 心跳证明进程存活；仅当 run 仍为 RUNNING 时才刷新，不覆盖终态。
	heartbeatStop := make(chan struct{})
	defer close(heartbeatStop)
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				global.App.DB.Model(&backupModels.InsightRestoreRuns{}).
					Where("id = ? AND status = ?", rr.ID, "RUNNING").
					Update("last_report_at", commonModels.LocalTime(nowLocalTime()))
			case <-heartbeatStop:
				return
			}
		}
	}()
	updateLogicalRestoreRunState(rr.ID, "RUNNING", restoreStagePrepareSource, 10, "准备恢复环境", logPath, "")
	switch {
	case strings.EqualFold(strings.TrimSpace(string(rr.DBType)), "MySQL"):
		return executeMySQLLogicalRestore(ctx, rr, set, targetInstance, options, resolved, logDir, logPath, logWriter, runControl)
	case isPostgresLogicalFamily(string(rr.DBType)):
		return executePostgresLogicalRestore(ctx, rr, set, targetInstance, options, resolved, logDir, logPath, logWriter, runControl)
	default:
		return fmt.Errorf("不支持的 DB 类型")
	}
}

func executeMySQLLogicalRestore(ctx context.Context, rr backupModels.InsightRestoreRuns, _ *backupModels.InsightBackupSets, targetInstance *commonModels.InsightInstances, options logicalRestoreRunOptions, resolved logicalRestoreResolvedContext, logDir string, logPath string, logWriter io.Writer, runControl *restoreRunControl) error {
	serverVersion, err := probeMySQLVersion(targetInstance.Hostname, targetInstance.Port, targetInstance.User, targetInstance.Password)
	if err != nil {
		return err
	}
	mysqlTool, err := resolveMySQLClientTool("mysql", serverVersion)
	if err != nil {
		return err
	}
	env := withLibraryPath(map[string]string{"MYSQL_PWD": targetInstance.Password}, mysqlTool.LibDirs)
	// 备份文件暂存到目标机并在目标机本地解压（远程模式）；清理函数保证成功/失败都删除临时目录。
	sourceRoot, stage, cleanup, err := prepareLogicalRestoreSourceRoot(ctx, resolved, targetInstance, logWriter)
	if err != nil {
		return err
	}
	defer cleanup()
	// 远程模式下导入要读目标机上的备份文件；本地模式若从对象存储下载了临时文件，
	// 也优先使用本地下载路径，避免仍用 st: 引用导致导入失败。
	sourceFile := resolved.BackupFile
	if stage != nil {
		if stage.Access.IsRemote() && strings.TrimSpace(stage.Artifact) != "" {
			sourceFile = stage.Artifact
		} else if strings.TrimSpace(stage.LocalDownloadPath) != "" {
			sourceFile = stage.LocalDownloadPath
		}
	}
	// 远程模式：导入命令在目标机本地执行，客户端路径取目标机上的实际路径，缺失即报错中止。
	remoteMySQLPath := ""
	if stage != nil && stage.Access.IsRemote() {
		remoteMySQLPath, err = resolveRemoteToolPath(ctx, stage.Access, "mysql", logWriter)
		if err != nil {
			return err
		}
	}
	itemMap := loadRestoreRunItemsBySource(rr.ID)
	for idx, source := range resolved.Sources {
		if stopErr := checkLogicalRestoreRunStop(runControl); stopErr != nil {
			return stopErr
		}
		itemID := itemMap[itemKey(source.SourceObjectName, source.TargetObjectName)]
		sourcePath := resolveLogicalRestoreSourcePath(sourceFile, sourceRoot, source)
		dbLogPath := logicalRestoreItemLogPath(logDir, source, idx)
		updateRestoreRunItem(itemID, restoreItemPatch{Status: "RUNNING", CurrentStage: restoreStagePrepareTarget, ProgressPercent: 20, LogPath: dbLogPath, Started: true})
		updateLogicalRestoreRunState(rr.ID, "RUNNING", restoreStagePrepareTarget, 20, "准备目标库", logPath, source.TargetObjectName)
		if source.TargetObjectName != "" {
			if targetErr := prepareMySQLTarget(ctx, mysqlTool, env, targetInstance, source.TargetObjectName, options, logWriter); targetErr != nil {
				updateRestoreRunItem(itemID, restoreItemPatch{Status: "FAILED", CurrentStage: restoreStagePrepareTarget, ProgressPercent: 20, LogPath: dbLogPath, ErrorMessage: targetErr.Error(), Finished: true})
				return targetErr
			}
		}
		updateRestoreRunItem(itemID, restoreItemPatch{Status: "RUNNING", CurrentStage: restoreStageRestoring, ProgressPercent: 65, LogPath: dbLogPath})
		updateLogicalRestoreRunState(rr.ID, "RUNNING", restoreStageRestoring, 65, "导入逻辑备份", logPath, displayOrDefault(source.TargetObjectName, source.SourceObjectName))
		// 数据冲突策略「先删除，再重新写入」：导入前清空目标库既有表数据。
		if strings.EqualFold(strings.TrimSpace(options.DataConflictPolicy), restoreDataConflictTruncate) && source.TargetObjectName != "" {
			if truncErr := truncateMySQLTargetTables(ctx, mysqlTool, env, targetInstance, source.TargetObjectName, logWriter); truncErr != nil {
				updateRestoreRunItem(itemID, restoreItemPatch{Status: "FAILED", CurrentStage: restoreStageRestoring, ProgressPercent: 65, LogPath: dbLogPath, ErrorMessage: truncErr.Error(), Finished: true})
				return truncErr
			}
		}
		if importErr := runMySQLRestoreImport(ctx, mysqlTool, env, targetInstance, source.TargetObjectName, sourcePath, source.Format, options, stage, remoteMySQLPath, logWriter); importErr != nil {
			updateRestoreRunItem(itemID, restoreItemPatch{Status: "FAILED", CurrentStage: restoreStageRestoring, ProgressPercent: 65, LogPath: dbLogPath, ErrorMessage: importErr.Error(), Finished: true})
			return importErr
		}
		if source.TargetObjectName != "" {
			_ = analyzeMySQLDatabase(targetInstance, source.TargetObjectName)
		}
		updateRestoreRunItem(itemID, restoreItemPatch{Status: "SUCCESS", CurrentStage: restoreStageFinished, ProgressPercent: 100, LogPath: dbLogPath, Finished: true})
		refreshRestoreRunFromItems(rr.ID, restoreStageVerify, logPath)
	}
	resultJSON, err := buildMySQLRestoreResultSummary(targetInstance, resolved, options, logPath)
	if err != nil {
		return err
	}
	finalizeLogicalRestoreSuccess(rr.ID, logPath, resultJSON)
	return nil
}

func executePostgresLogicalRestore(ctx context.Context, rr backupModels.InsightRestoreRuns, _ *backupModels.InsightBackupSets, targetInstance *commonModels.InsightInstances, options logicalRestoreRunOptions, resolved logicalRestoreResolvedContext, logDir string, logPath string, logWriter io.Writer, runControl *restoreRunControl) error {
	// 本函数同时服务 PostgreSQL 与 KingbaseES：工具名、默认库、OS 用户都由 dbType 决定，
	// 流程本身完全一致（sys_restore/sys_dump 与 pg_restore/pg_dump 参数同源）。
	dbType := string(rr.DBType)
	extra := parseExtraConfig(targetInstance.ExtraConfig)
	adminDB := firstNonEmpty(extra["database"], logicalDefaultDatabase(dbType))
	serverVersion, err := probePostgresVersion(targetInstance.Hostname, targetInstance.Port, targetInstance.User, targetInstance.Password, adminDB, extra["sslmode"])
	if err != nil {
		return err
	}
	psqlTool, err := resolveLogicalClientTool(dbType, logicalToolRoleQuery, serverVersion)
	if err != nil {
		return err
	}
	pgRestoreTool, err := resolveLogicalClientTool(dbType, logicalToolRoleRestore, serverVersion)
	if err != nil {
		return err
	}
	env := map[string]string{"PGPASSWORD": targetInstance.Password}
	if isKingbaseDBType(dbType) {
		env["KINGBASE_PASSWORD"] = targetInstance.Password
	}
	if strings.TrimSpace(extra["sslmode"]) != "" {
		env["PGSSLMODE"] = strings.TrimSpace(extra["sslmode"])
	}
	env = withLibraryPath(env, append(psqlTool.LibDirs, pgRestoreTool.LibDirs...))
	// 备份文件暂存到目标机并在目标机本地解压（远程模式）；清理函数保证成功/失败都删除临时目录。
	sourceRoot, stage, cleanup, err := prepareLogicalRestoreSourceRoot(ctx, resolved, targetInstance, logWriter)
	if err != nil {
		return err
	}
	defer cleanup()
	// 远程模式下导入要读目标机上的备份文件；本地模式若从对象存储下载了临时文件，
	// 也优先使用本地下载路径。
	sourceFile := resolved.BackupFile
	if stage != nil {
		if stage.Access.IsRemote() && strings.TrimSpace(stage.Artifact) != "" {
			sourceFile = stage.Artifact
		} else if strings.TrimSpace(stage.LocalDownloadPath) != "" {
			sourceFile = stage.LocalDownloadPath
		}
	}
	// 远程模式：导入在目标机本地执行，sys_restore / ksql（或 pg_restore / psql）
	// 用目标机上的路径，缺失即报错中止。
	remotePgRestorePath := ""
	remotePsqlPath := ""
	if stage != nil && stage.Access.IsRemote() {
		remotePgRestorePath, err = resolveRemoteToolPath(ctx, stage.Access, logicalToolName(dbType, logicalToolRoleRestore), logWriter)
		if err != nil {
			return err
		}
		remotePsqlPath, err = resolveRemoteToolPath(ctx, stage.Access, logicalToolName(dbType, logicalToolRoleQuery), logWriter)
		if err != nil {
			return err
		}
	}
	itemMap := loadRestoreRunItemsBySource(rr.ID)
	for idx, source := range resolved.Sources {
		if stopErr := checkLogicalRestoreRunStop(runControl); stopErr != nil {
			return stopErr
		}
		itemID := itemMap[itemKey(source.SourceObjectName, source.TargetObjectName)]
		sourcePath := resolveLogicalRestoreSourcePath(sourceFile, sourceRoot, source)
		dbLogPath := logicalRestoreItemLogPath(logDir, source, idx)
		updateRestoreRunItem(itemID, restoreItemPatch{Status: "RUNNING", CurrentStage: restoreStagePrepareTarget, ProgressPercent: 20, LogPath: dbLogPath, Started: true})
		updateLogicalRestoreRunState(rr.ID, "RUNNING", restoreStagePrepareTarget, 20, "准备目标库", logPath, source.TargetObjectName)
		if targetErr := preparePostgresTarget(ctx, psqlTool, env, targetInstance, adminDB, source.TargetObjectName, options, logWriter); targetErr != nil {
			updateRestoreRunItem(itemID, restoreItemPatch{Status: "FAILED", CurrentStage: restoreStagePrepareTarget, ProgressPercent: 20, LogPath: dbLogPath, ErrorMessage: targetErr.Error(), Finished: true})
			return targetErr
		}
		updateRestoreRunItem(itemID, restoreItemPatch{Status: "RUNNING", CurrentStage: restoreStageRestoring, ProgressPercent: 65, LogPath: dbLogPath})
		updateLogicalRestoreRunState(rr.ID, "RUNNING", restoreStageRestoring, 65, "导入逻辑备份", logPath, source.TargetObjectName)
		if importErr := runPostgresRestoreImport(ctx, psqlTool, pgRestoreTool, env, targetInstance, source.TargetObjectName, sourcePath, source, options, stage, remotePgRestorePath, remotePsqlPath, logWriter); importErr != nil {
			updateRestoreRunItem(itemID, restoreItemPatch{Status: "FAILED", CurrentStage: restoreStageRestoring, ProgressPercent: 65, LogPath: dbLogPath, ErrorMessage: importErr.Error(), Finished: true})
			return importErr
		}
		_ = analyzePostgresDatabase(ctx, psqlTool, env, targetInstance, source.TargetObjectName, logWriter)
		updateRestoreRunItem(itemID, restoreItemPatch{Status: "SUCCESS", CurrentStage: restoreStageFinished, ProgressPercent: 100, LogPath: dbLogPath, Finished: true})
		refreshRestoreRunFromItems(rr.ID, restoreStageVerify, logPath)
	}
	resultJSON, err := buildPostgresRestoreResultSummary(targetInstance, resolved, options, logPath)
	if err != nil {
		return err
	}
	finalizeLogicalRestoreSuccess(rr.ID, logPath, resultJSON)
	return nil
}

// prepareLogicalRestoreSourceRoot 把备份文件暂存到「待恢复的目标机器」并在目标机本地解压。
//
// 与旧实现的区别：
//   - 旧：在服务端本机 <logDir>/restore-src 解压，导入后残留（PG 场景十几 GB 长期占用系统盘）；
//   - 新：备份文件先传输到目标机，在目标机 mktemp -d 的独立临时目录里解压，
//     解压脚本自带 trap（异常退出即清理），Go 侧再由调用方 defer cleanup 兜底，
//     成功/失败/中途报错都会删除本次临时目录（删除前校验路径确为本次临时目录）。
//
// 返回：源根目录（zip 场景有效）、暂存现场、清理函数（调用方必须 defer）。
func prepareLogicalRestoreSourceRoot(ctx context.Context, resolved logicalRestoreResolvedContext, targetInstance *commonModels.InsightInstances, logWriter io.Writer) (string, *restoreSourceStage, func(), error) {
	needsUnzip := strings.EqualFold(strings.TrimSpace(resolved.ArtifactFormat), "zip")
	access := resolveRestoreTargetAccess(targetInstance, logWriter)
	stage := &restoreSourceStage{Access: access}
	root, err := stageRestoreSource(ctx, stage, resolved.BackupFile, needsUnzip, logWriter)
	cleanup := func() { cleanupRestoreStage(stage, logWriter) }
	if err != nil {
		// 暂存失败：立即清理，避免残留半个临时目录。
		cleanup()
		return "", nil, func() {}, err
	}
	return root, stage, cleanup, nil
}

func resolveLogicalRestoreSourcePath(backupFile string, sourceRoot string, source logicalRestoreSourcePlan) string {
	if strings.TrimSpace(source.EntryName) == "" {
		return strings.TrimSpace(backupFile)
	}
	return filepath.Join(sourceRoot, filepath.FromSlash(source.EntryName))
}

func loadRestoreRunItemsBySource(runID uint64) map[string]uint64 {
	type row struct {
		ID         uint64
		SourceName string
		TargetName string
	}
	rows := make([]row, 0)
	_ = global.App.DB.Table("insight_restore_run_items").
		Select("id, source_object_name as source_name, target_object_name as target_name").
		Where("restore_run_id = ?", runID).
		Scan(&rows).Error
	out := map[string]uint64{}
	for _, row := range rows {
		out[itemKey(row.SourceName, row.TargetName)] = row.ID
	}
	return out
}

func itemKey(source string, target string) string {
	return strings.TrimSpace(source) + "->" + strings.TrimSpace(target)
}

func logicalRestoreItemLogPath(logDir string, source logicalRestoreSourcePlan, index int) string {
	base := strings.TrimSpace(source.TargetObjectName)
	if base == "" {
		base = strings.TrimSpace(source.SourceObjectName)
	}
	if base == "" {
		base = fmt.Sprintf("item-%d", index+1)
	}
	return filepath.Join(logDir, fmt.Sprintf("%s.log", sanitizeRestoreLogName(base)))
}

func checkLogicalRestoreRunStop(runControl *restoreRunControl) error {
	if runControl != nil && runControl.StopRequested() {
		return ErrRestoreRunStopped
	}
	return nil
}

func updateLogicalRestoreRunState(id uint64, status string, stage string, percent int, action string, logPath string, currentObject string) {
	updates := map[string]any{
		"status":           status,
		"current_stage":    stage,
		"progress_percent": clampPercent(percent),
		"current_action":   strings.TrimSpace(action),
		"last_report_at":   commonModels.LocalTime(nowLocalTime()),
	}
	if strings.TrimSpace(logPath) != "" {
		updates["log_path"] = strings.TrimSpace(logPath)
		updates["log_excerpt"] = tailLogExcerpt(strings.TrimSpace(logPath))
	}
	if strings.TrimSpace(currentObject) != "" {
		updates["current_object"] = strings.TrimSpace(currentObject)
	}
	if strings.EqualFold(strings.TrimSpace(status), "RUNNING") {
		updates["start_at"] = commonModels.LocalTime(nowLocalTime())
	}
	global.App.DB.Model(&backupModels.InsightRestoreRuns{}).Where("id = ?", id).Updates(updates)
}

func finalizeLogicalRestoreSuccess(id uint64, logPath string, resultSummary datatypes.JSON) {
	updates := map[string]any{
		"status":           "SUCCESS",
		"current_stage":    restoreStageFinished,
		"progress_percent": 100,
		"current_action":   "恢复完成",
		"last_report_at":   commonModels.LocalTime(nowLocalTime()),
		"end_at":           commonModels.LocalTime(nowLocalTime()),
		"log_path":         strings.TrimSpace(logPath),
		"log_excerpt":      tailLogExcerpt(strings.TrimSpace(logPath)),
		"result_summary":   resultSummary,
	}
	global.App.DB.Model(&backupModels.InsightRestoreRuns{}).Where("id = ?", id).Updates(updates)
}

func buildMySQLCreateDBSQL(targetDB string, options logicalRestoreRunOptions) string {
	sql := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", quoteMySQLIdent(targetDB))
	if strings.TrimSpace(options.TargetCharset) != "" {
		sql += " DEFAULT CHARACTER SET " + strings.TrimSpace(options.TargetCharset)
	}
	if strings.TrimSpace(options.TargetCollation) != "" {
		sql += " COLLATE " + strings.TrimSpace(options.TargetCollation)
	}
	return sql + ";"
}

func buildMySQLOverwriteSQL(targetDB string, options logicalRestoreRunOptions) string {
	return fmt.Sprintf("DROP DATABASE IF EXISTS %s; %s", quoteMySQLIdent(targetDB), buildMySQLCreateDBSQL(targetDB, options))
}

func prepareMySQLTarget(ctx context.Context, mysqlTool *localToolSpec, env map[string]string, targetInstance *commonModels.InsightInstances, targetDB string, options logicalRestoreRunOptions, logWriter io.Writer) error {
	if strings.TrimSpace(targetDB) == "" {
		return nil
	}
	switch strings.TrimSpace(options.RestoreMode) {
	case logicalRestoreModeNew:
		return runLocalCommand(ctx, "", mysqlTool.Path, []string{"-h", targetInstance.Hostname, "-P", fmt.Sprintf("%d", targetInstance.Port), "-u", targetInstance.User, "-e", buildMySQLCreateDBSQL(targetDB, options)}, env, logWriter)
	case logicalRestoreModeOverwrite:
		return runLocalCommand(ctx, "", mysqlTool.Path, []string{"-h", targetInstance.Hostname, "-P", fmt.Sprintf("%d", targetInstance.Port), "-u", targetInstance.User, "-e", buildMySQLOverwriteSQL(targetDB, options)}, env, logWriter)
	case logicalRestoreModeAppend:
		// 结构冲突策略「忽略冲突，继续任务」：目标库不存在时自动创建（IF NOT EXISTS），已存在则直接复用。
		return runLocalCommand(ctx, "", mysqlTool.Path, []string{"-h", targetInstance.Hostname, "-P", fmt.Sprintf("%d", targetInstance.Port), "-u", targetInstance.User, "-e", buildMySQLCreateDBSQL(targetDB, options)}, env, logWriter)
	default:
		return fmt.Errorf("不支持的恢复模式: %s", options.RestoreMode)
	}
}

func runMySQLRestoreImport(ctx context.Context, mysqlTool *localToolSpec, env map[string]string, targetInstance *commonModels.InsightInstances, targetDB string, sourcePath string, format string, options logicalRestoreRunOptions, stage *restoreSourceStage, remoteToolPath string, logWriter io.Writer) error {
	// 远程模式（备份文件已暂存到目标机）：导入命令在目标机本地执行，客户端用目标机上的路径。
	binPath := ""
	if mysqlTool != nil {
		binPath = mysqlTool.Path
	}
	if strings.TrimSpace(remoteToolPath) != "" {
		binPath = strings.TrimSpace(remoteToolPath)
	}
	args := []string{"-h", targetInstance.Hostname, "-P", fmt.Sprintf("%d", targetInstance.Port), "-u", targetInstance.User}
	if strings.TrimSpace(targetDB) != "" {
		args = append(args, targetDB)
	}
	// 冲突策略决定导入是否遇错终止：ABORT 一律中断；其余沿用 exit_on_error 开关。
	abortOnConflict := strings.EqualFold(strings.TrimSpace(options.DataConflictPolicy), restoreDataConflictAbort) ||
		strings.EqualFold(strings.TrimSpace(options.SchemaConflictPolicy), restoreSchemaConflictAbort)
	forceImport := !options.ExitOnError && !abortOnConflict
	if forceImport {
		args = append(args, "--force")
	}
	prefixSQL := make([]string, 0, 3)
	if !options.ForeignKeyChecks {
		prefixSQL = append(prefixSQL, "SET FOREIGN_KEY_CHECKS=0;", "SET UNIQUE_CHECKS=0;")
	}
	if options.CloseBinlog {
		prefixSQL = append(prefixSQL, "SET SESSION sql_log_bin=0;")
	}
	bodyCmd := "cat " + shellQuote(sourcePath)
	if strings.EqualFold(strings.TrimSpace(format), "sql.gz") {
		bodyCmd = "gzip -dc " + shellQuote(sourcePath)
	}
	// 恢复类型过滤：仅表结构 / 仅全量数据时，按语句类型流式过滤 SQL 文本。
	if filter := buildMySQLRestoreTypeFilter(options.RestoreSchema, options.RestoreData); filter != "" {
		bodyCmd = bodyCmd + " | " + filter
	}
	if len(prefixSQL) > 0 {
		bodyCmd = "{ printf '%s\\n' " + shellQuote(strings.Join(prefixSQL, " ")) + "; " + bodyCmd + "; }"
	}
	shellCmd := bodyCmd + " | " + joinShellCommand(append([]string{binPath}, args...))
	return runRestoreImportShell(ctx, stage, shellCmd, env, logWriter)
}

// runRestoreImportShell 执行导入命令：
//   - 本地模式：服务端本机 /bin/sh -lc（与改造前一致）；
//   - 远程模式：通过 SSH 在目标机本地 bash -s 执行（备份文件已暂存到目标机，无需回传）。
func runRestoreImportShell(ctx context.Context, stage *restoreSourceStage, shellCmd string, env map[string]string, logWriter io.Writer) error {
	if stage != nil && stage.Access.IsRemote() {
		stageLog(logWriter, "[stage] 目标机 %s 本地执行导入命令", stage.Access.Host)
		return runRemoteShell(ctx, stage.Access, shellCmd, env, logWriter)
	}
	// 本地模式启用 pipefail，避免 gzip/awk 等管道前段失败时被 mysql 的退出码掩盖。
	return runLocalCommand(ctx, "", "/bin/sh", []string{"-lc", "set -o pipefail; " + shellCmd}, env, logWriter)
}

// buildMySQLRestoreTypeFilter 按恢复类型构造 SQL 文本流过滤器。
// mysqldump 输出中每条语句以 `;` 结尾，故用 awk 状态机整段跳过目标语句：
//   - 仅表结构：丢弃 INSERT / REPLACE 语句
//   - 仅数据：丢弃 CREATE / DROP / ALTER / RENAME / TRUNCATE 等 DDL 语句
// 两种类型都恢复时返回空串（不额外过滤，保持零开销）。
func buildMySQLRestoreTypeFilter(restoreSchema bool, restoreData bool) string {
	if restoreSchema && restoreData {
		return ""
	}
	keepSchema := "0"
	if restoreSchema {
		keepSchema = "1"
	}
	keepData := "0"
	if restoreData {
		keepData = "1"
	}
	// skip：丢弃当前语句直到遇到以 `;` 结尾的行；emit：继续输出当前语句直到遇到 `;`。
	script := `awk -v ks=` + keepSchema + ` -v kd=` + keepData + ` '` +
		`BEGIN{skip=0;emit=0}` +
		`{line=$0;` +
		`if(skip){if(line ~ /;[ \t]*$/)skip=0; next}` +
		`if(emit){print; if(line ~ /;[ \t]*$/)emit=0; next}` +
		`if(line ~ /^[ \t]*(INSERT|REPLACE)[ \t]+INTO/){` +
		`if(kd=="1"){print; if(line !~ /;[ \t]*$/)emit=1}else if(line !~ /;[ \t]*$/)skip=1; next}` +
		`if(line ~ /^[ \t]*(CREATE|DROP|ALTER|RENAME|TRUNCATE)[ \t]+/){` +
		`if(ks=="1"){print; if(line !~ /;[ \t]*$/)emit=1}else if(line !~ /;[ \t]*$/)skip=1; next}` +
		`print}'`
	return script
}

// truncateMySQLTargetTables 数据冲突策略「先删除，再重新写入」：导入前清空目标库所有表。
func truncateMySQLTargetTables(ctx context.Context, mysqlTool *localToolSpec, env map[string]string, targetInstance *commonModels.InsightInstances, targetDB string, logWriter io.Writer) error {
	database := strings.TrimSpace(targetDB)
	if database == "" {
		return nil
	}
	tables, err := listMySQLTables(targetInstance.Hostname, targetInstance.Port, targetInstance.User, targetInstance.Password, database)
	if err != nil {
		return fmt.Errorf("读取目标库表清单失败: %w", err)
	}
	if len(tables) == 0 {
		return nil
	}
	stmts := []string{"SET FOREIGN_KEY_CHECKS=0;"}
	for _, table := range tables {
		name := strings.TrimSpace(table)
		if name == "" {
			continue
		}
		stmts = append(stmts, fmt.Sprintf("TRUNCATE TABLE %s.%s;", quoteMySQLIdent(database), quoteMySQLIdent(name)))
	}
	stmts = append(stmts, "SET FOREIGN_KEY_CHECKS=1;")
	if logWriter != nil {
		fmt.Fprintf(logWriter, "[prepare] 数据冲突策略=先删除再写入，清空目标库 %s 的 %d 张表\n", database, len(tables))
	}
	return runLocalCommand(ctx, "", mysqlTool.Path, []string{
		"-h", targetInstance.Hostname,
		"-P", fmt.Sprintf("%d", targetInstance.Port),
		"-u", targetInstance.User,
		"-e", strings.Join(stmts, " "),
	}, env, logWriter)
}

func analyzeMySQLDatabase(targetInstance *commonModels.InsightInstances, database string) error {
	tables, err := listMySQLTables(targetInstance.Hostname, targetInstance.Port, targetInstance.User, targetInstance.Password, database)
	if err != nil || len(tables) == 0 {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db := dasDao.DB{
		User:     targetInstance.User,
		Password: targetInstance.Password,
		Host:     targetInstance.Hostname,
		Port:     targetInstance.Port,
		Database: database,
		Params:   map[string]string{"charset": "utf8mb4", "parseTime": "True", "loc": "Local"},
		Ctx:      ctx,
	}
	for _, table := range tables {
		_ = db.Execute("ANALYZE TABLE " + quoteMySQLIdent(table))
	}
	return nil
}

func buildPostgresCreateDBSQL(targetDB string, options logicalRestoreRunOptions) string {
	parts := []string{fmt.Sprintf("CREATE DATABASE %s", quotePGIdent(targetDB))}
	if strings.TrimSpace(options.TargetEncoding) != "" {
		parts = append(parts, "ENCODING "+shellQuote(strings.TrimSpace(options.TargetEncoding)))
	}
	parts = append(parts, "TEMPLATE template0")
	return strings.Join(parts, " ") + ";"
}

func buildPostgresOverwriteSQL(targetDB string, options logicalRestoreRunOptions) string {
	_ = options
	return fmt.Sprintf("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = %s AND pid <> pg_backend_pid(); DROP DATABASE IF EXISTS %s; %s",
		shellQuote(strings.TrimSpace(targetDB)), quotePGIdent(targetDB), buildPostgresCreateDBSQL(targetDB, options))
}

func preparePostgresTarget(ctx context.Context, psqlTool *localToolSpec, env map[string]string, targetInstance *commonModels.InsightInstances, adminDB string, targetDB string, options logicalRestoreRunOptions, logWriter io.Writer) error {
	if strings.TrimSpace(targetDB) == "" {
		return fmt.Errorf("PostgreSQL 目标库名不能为空")
	}
	switch strings.TrimSpace(options.RestoreMode) {
	case logicalRestoreModeNew:
		return runLocalCommand(ctx, "", psqlTool.Path, []string{"-h", targetInstance.Hostname, "-p", fmt.Sprintf("%d", targetInstance.Port), "-U", targetInstance.User, "-d", adminDB, "-v", "ON_ERROR_STOP=1", "-c", buildPostgresCreateDBSQL(targetDB, options)}, env, logWriter)
	case logicalRestoreModeOverwrite:
		return runLocalCommand(ctx, "", psqlTool.Path, []string{"-h", targetInstance.Hostname, "-p", fmt.Sprintf("%d", targetInstance.Port), "-U", targetInstance.User, "-d", adminDB, "-v", "ON_ERROR_STOP=1", "-c", buildPostgresOverwriteSQL(targetDB, options)}, env, logWriter)
	case logicalRestoreModeAppend:
		// 结构冲突策略「忽略冲突，继续任务」：PG 无 CREATE DATABASE IF NOT EXISTS，
		// 先探测目标库是否存在，不存在时自动创建后导入。
		exists, existsErr := postgresDatabaseExists(targetInstance.Hostname, targetInstance.Port, targetInstance.User, targetInstance.Password, adminDB, parseExtraConfig(targetInstance.ExtraConfig)["sslmode"], targetDB)
		if existsErr != nil {
			return existsErr
		}
		if exists {
			return nil
		}
		return runLocalCommand(ctx, "", psqlTool.Path, []string{"-h", targetInstance.Hostname, "-p", fmt.Sprintf("%d", targetInstance.Port), "-U", targetInstance.User, "-d", adminDB, "-v", "ON_ERROR_STOP=1", "-c", buildPostgresCreateDBSQL(targetDB, options)}, env, logWriter)
	default:
		return fmt.Errorf("不支持的恢复模式: %s", options.RestoreMode)
	}
}

func buildPostgresRestorePreviewCommand(dbType string, targetInstance *commonModels.InsightInstances, options logicalRestoreRunOptions, source logicalRestoreSourcePlan, sourcePath string) string {
	// 预览里只展示工具名（不展开绝对路径），金仓显示 ksql / sys_restore
	queryTool := logicalToolName(dbType, logicalToolRoleQuery)
	restoreTool := logicalToolName(dbType, logicalToolRoleRestore)
	if strings.EqualFold(source.Format, "plain") || strings.EqualFold(source.Format, "plain.gz") {
		base := fmt.Sprintf("%s \"host=%s port=%d dbname=%s user=%s\" -v ON_ERROR_STOP=1", queryTool, targetInstance.Hostname, targetInstance.Port, source.TargetObjectName, targetInstance.User)
		if strings.EqualFold(source.Format, "plain.gz") {
			return fmt.Sprintf("gzip -dc %s | %s", shellQuote(sourcePath), base)
		}
		return fmt.Sprintf("%s -f %s", base, shellQuote(sourcePath))
	}
	args := []string{
		restoreTool,
		"-h", targetInstance.Hostname,
		"-p", fmt.Sprintf("%d", targetInstance.Port),
		"-U", targetInstance.User,
		"-d", source.TargetObjectName,
	}
	if options.NoOwner {
		args = append(args, "--no-owner")
	}
	if options.NoPrivileges {
		args = append(args, "--no-privileges")
	}
	if options.ExitOnError {
		args = append(args, "--exit-on-error")
	}
	if strings.EqualFold(source.Format, "directory") {
		args = append(args, "-Fd", "-j", fmt.Sprintf("%d", options.Parallel))
	} else if options.Parallel > 1 {
		args = append(args, "-j", fmt.Sprintf("%d", options.Parallel))
	}
	for _, schema := range uniqueStrings(source.SelectedSchemas) {
		args = append(args, "-n", schema)
	}
	for _, table := range uniqueStrings(source.SelectedTables) {
		args = append(args, "-t", table)
	}
	args = append(args, sourcePath)
	return strings.Join(args, " ")
}

func runPostgresRestoreImport(ctx context.Context, psqlTool *localToolSpec, pgRestoreTool *localToolSpec, env map[string]string, targetInstance *commonModels.InsightInstances, targetDB string, sourcePath string, source logicalRestoreSourcePlan, options logicalRestoreRunOptions, stage *restoreSourceStage, remotePgRestorePath string, remotePsqlPath string, logWriter io.Writer) error {
	// 远程模式：客户端路径取目标机上的实际路径；本地模式沿用服务端本机解析结果。
	psqlBin := ""
	if psqlTool != nil {
		psqlBin = psqlTool.Path
	}
	if strings.TrimSpace(remotePsqlPath) != "" {
		psqlBin = strings.TrimSpace(remotePsqlPath)
	}
	pgRestoreBin := ""
	if pgRestoreTool != nil {
		pgRestoreBin = pgRestoreTool.Path
	}
	if strings.TrimSpace(remotePgRestorePath) != "" {
		pgRestoreBin = strings.TrimSpace(remotePgRestorePath)
	}
	if strings.EqualFold(source.Format, "plain") || strings.EqualFold(source.Format, "plain.gz") {
		// plain SQL 无法按类型拆分，明确拒绝而不是静默全量导入。
		if !options.RestoreSchema || !options.RestoreData {
			return fmt.Errorf("PostgreSQL plain SQL 备份暂不支持按「表结构 / 全量数据」拆分恢复，请使用 custom/directory 格式备份")
		}
		baseArgs := []string{"-h", targetInstance.Hostname, "-p", fmt.Sprintf("%d", targetInstance.Port), "-U", targetInstance.User, "-d", targetDB}
		if options.ExitOnError {
			baseArgs = append(baseArgs, "-v", "ON_ERROR_STOP=1")
		}
		if strings.EqualFold(source.Format, "plain.gz") {
			shellCmd := "gzip -dc " + shellQuote(sourcePath) + " | " + joinShellCommand(append([]string{psqlBin}, baseArgs...))
			return runRestoreImportShell(ctx, stage, shellCmd, env, logWriter)
		}
		baseArgs = append(baseArgs, "-f", sourcePath)
		return runRestoreImportShell(ctx, stage, joinShellCommand(append([]string{psqlBin}, baseArgs...)), env, logWriter)
	}
	args := []string{"-h", targetInstance.Hostname, "-p", fmt.Sprintf("%d", targetInstance.Port), "-U", targetInstance.User, "-d", targetDB}
	if options.NoOwner {
		args = append(args, "--no-owner")
	}
	if options.NoPrivileges {
		args = append(args, "--no-privileges")
	}
	abortOnConflict := strings.EqualFold(strings.TrimSpace(options.DataConflictPolicy), restoreDataConflictAbort) ||
		strings.EqualFold(strings.TrimSpace(options.SchemaConflictPolicy), restoreSchemaConflictAbort)
	if options.ExitOnError || abortOnConflict {
		args = append(args, "--exit-on-error")
	}
	// 数据冲突策略「先删除，再重新写入」在 PG 侧映射为 pg_restore --clean --if-exists。
	if options.CleanIfExists && strings.EqualFold(options.RestoreMode, logicalRestoreModeOverwrite) {
		args = append(args, "--clean", "--if-exists")
	} else if strings.EqualFold(strings.TrimSpace(options.DataConflictPolicy), restoreDataConflictTruncate) {
		args = append(args, "--clean", "--if-exists")
	}
	// 恢复类型：仅结构 / 仅数据（pg_restore 原生支持，等价 mysqldump --no-data / --no-create-info）。
	switch {
	case !options.RestoreSchema && options.RestoreData:
		args = append(args, "--data-only")
	case options.RestoreSchema && !options.RestoreData:
		args = append(args, "--schema-only")
	}
	if strings.EqualFold(source.Format, "directory") {
		args = append(args, "-Fd", "-j", fmt.Sprintf("%d", options.Parallel))
	} else if options.Parallel > 1 {
		args = append(args, "-j", fmt.Sprintf("%d", options.Parallel))
	}
	for _, schema := range uniqueStrings(source.SelectedSchemas) {
		args = append(args, "-n", schema)
	}
	for _, table := range uniqueStrings(source.SelectedTables) {
		args = append(args, "-t", table)
	}
	args = append(args, sourcePath)
	return runRestoreImportShell(ctx, stage, joinShellCommand(append([]string{pgRestoreBin}, args...)), env, logWriter)
}

func analyzePostgresDatabase(ctx context.Context, psqlTool *localToolSpec, env map[string]string, targetInstance *commonModels.InsightInstances, targetDB string, logWriter io.Writer) error {
	return runLocalCommand(ctx, "", psqlTool.Path, []string{"-h", targetInstance.Hostname, "-p", fmt.Sprintf("%d", targetInstance.Port), "-U", targetInstance.User, "-d", targetDB, "-v", "ON_ERROR_STOP=1", "-c", "ANALYZE;"}, env, logWriter)
}

func buildMySQLRestoreResultSummary(targetInstance *commonModels.InsightInstances, resolved logicalRestoreResolvedContext, options logicalRestoreRunOptions, logPath string) (datatypes.JSON, error) {
	validations := make([]map[string]any, 0, len(resolved.Sources))
	for _, source := range resolved.Sources {
		if strings.TrimSpace(source.TargetObjectName) == "" {
			continue
		}
		summary, err := inspectMySQLRestoreResult(targetInstance, source.TargetObjectName)
		if err != nil {
			validations = append(validations, map[string]any{"database": source.TargetObjectName, "error": err.Error()})
			continue
		}
		validations = append(validations, summary)
	}
	return marshalJSONValue(gin.H{
		"report_type":           "logical_restore",
		"db_type":               "MySQL",
		"restore_mode":          options.RestoreMode,
		"log_path":              logPath,
		"backup_set_verified":   resolved.MetadataVerified && resolved.ChecksumVerified,
		"postcheck_validations": validations,
	})
}

func buildPostgresRestoreResultSummary(targetInstance *commonModels.InsightInstances, resolved logicalRestoreResolvedContext, options logicalRestoreRunOptions, logPath string) (datatypes.JSON, error) {
	validations := make([]map[string]any, 0, len(resolved.Sources))
	for _, source := range resolved.Sources {
		summary, err := inspectPostgresRestoreResult(targetInstance, source.TargetObjectName)
		if err != nil {
			validations = append(validations, map[string]any{"database": source.TargetObjectName, "error": err.Error()})
			continue
		}
		validations = append(validations, summary)
	}
	return marshalJSONValue(gin.H{
		"report_type": "logical_restore",
		// 用目标实例的真实类型，避免金仓的恢复报告被写成 PostgreSQL
		"db_type":               strings.TrimSpace(string(targetInstance.DbType)),
		"restore_mode":          options.RestoreMode,
		"log_path":              logPath,
		"backup_set_verified":   resolved.MetadataVerified && resolved.ChecksumVerified,
		"postcheck_validations": validations,
	})
}

func inspectMySQLRestoreResult(targetInstance *commonModels.InsightInstances, database string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db := dasDao.DB{
		User:     targetInstance.User,
		Password: targetInstance.Password,
		Host:     targetInstance.Hostname,
		Port:     targetInstance.Port,
		Database: database,
		Params:   map[string]string{"charset": "utf8mb4", "parseTime": "True", "loc": "Local"},
		Ctx:      ctx,
	}
	query := fmt.Sprintf(`select
                (select count(*) from information_schema.tables where table_schema = '%s' and table_type = 'BASE TABLE') as tables_count,
                (select count(*) from information_schema.views where table_schema = '%s') as views_count,
                (select count(*) from information_schema.triggers where trigger_schema = '%s') as triggers_count,
                (select count(*) from information_schema.routines where routine_schema = '%s') as routines_count,
                (select count(*) from information_schema.events where event_schema = '%s') as events_count,
                (select count(*) from information_schema.table_constraints where table_schema = '%s' and constraint_type = 'PRIMARY KEY') as primary_keys,
                (select count(*) from information_schema.table_constraints where table_schema = '%s' and constraint_type = 'UNIQUE') as unique_constraints,
                (select count(*) from information_schema.referential_constraints where constraint_schema = '%s') as foreign_keys,
                (select group_concat(distinct engine order by engine separator ',') from information_schema.tables where table_schema = '%s' and engine is not null) as engines,
                (select default_character_set_name from information_schema.schemata where schema_name = '%s') as charset,
                (select default_collation_name from information_schema.schemata where schema_name = '%s') as collation,
                @@session.time_zone as time_zone`, escapeSingleQuotes(database), escapeSingleQuotes(database), escapeSingleQuotes(database), escapeSingleQuotes(database), escapeSingleQuotes(database), escapeSingleQuotes(database), escapeSingleQuotes(database), escapeSingleQuotes(database), escapeSingleQuotes(database), escapeSingleQuotes(database), escapeSingleQuotes(database))
	_, rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	if rows == nil || len(*rows) == 0 {
		return nil, fmt.Errorf("未读取到 MySQL 恢复后校验信息")
	}
	row := (*rows)[0]
	return map[string]any{
		"database":            database,
		"objects":             row,
		"permissions":         map[string]any{"restore_user_connect": true},
		"analyze":             true,
		"business_acceptance": "PENDING_MANUAL",
		"key_table_rows":      []any{},
	}, nil
}

func inspectPostgresRestoreResult(targetInstance *commonModels.InsightInstances, database string) (map[string]any, error) {
	extra := parseExtraConfig(targetInstance.ExtraConfig)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db := dasDao.PostgresDB{
		User:     targetInstance.User,
		Password: targetInstance.Password,
		Host:     targetInstance.Hostname,
		Port:     targetInstance.Port,
		Database: database,
		SSLMode:  extra["sslmode"],
		Ctx:      ctx,
	}
	query := `select
                (select count(*) from pg_class c join pg_namespace n on n.oid=c.relnamespace where c.relkind='r' and n.nspname not in ('pg_catalog','information_schema')) as tables_count,
                (select count(*) from pg_class c join pg_namespace n on n.oid=c.relnamespace where c.relkind in ('v','m') and n.nspname not in ('pg_catalog','information_schema')) as views_count,
                (select count(*) from pg_proc p join pg_namespace n on n.oid=p.pronamespace where n.nspname not in ('pg_catalog','information_schema')) as functions_count,
                (select count(*) from pg_trigger t join pg_class c on c.oid=t.tgrelid join pg_namespace n on n.oid=c.relnamespace where not t.tgisinternal and n.nspname not in ('pg_catalog','information_schema')) as triggers_count,
                (select count(*) from pg_constraint where contype='p') as primary_keys,
                (select count(*) from pg_constraint where contype='u') as unique_constraints,
                (select count(*) from pg_constraint where contype='f') as foreign_keys,
                (select string_agg(extname, ',' order by extname) from pg_extension) as extensions,
                (select pg_encoding_to_char(encoding) from pg_database where datname=current_database()) as encoding,
                current_setting('TimeZone') as time_zone`
	_, rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	if rows == nil || len(*rows) == 0 {
		return nil, fmt.Errorf("未读取到 PostgreSQL 恢复后校验信息")
	}
	row := (*rows)[0]
	return map[string]any{
		"database":            database,
		"objects":             row,
		"permissions":         map[string]any{"restore_user_connect": true},
		"analyze":             true,
		"business_acceptance": "PENDING_MANUAL",
		"key_table_rows":      []any{},
	}, nil
}

func parseLogicalRestoreTimeout(raw string) time.Duration {
	text := strings.TrimSpace(raw)
	if text == "" {
		return 4 * time.Hour
	}
	if d, err := time.ParseDuration(text); err == nil && d > 0 {
		return d
	}
	return 4 * time.Hour
}

func quoteMySQLIdent(name string) string {
	return "`" + strings.ReplaceAll(strings.TrimSpace(name), "`", "``") + "`"
}

func quotePGIdent(name string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(name), `"`, `""`) + `"`
}
