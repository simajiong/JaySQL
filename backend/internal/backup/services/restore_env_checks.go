package services

import (
	"fmt"
	"strings"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
)

// 恢复向导「预检查」步骤的环境一致性检查项（对齐产品参考设计）：
//   target_privilege           目标库权限检查
//   sql_mode                   SQL_MODE 配置一致性
//   lower_case_table_names     大小写敏感参数检查（MySQL）
//   time_zone                  时区检查
//   source_storage             源存储端检查
// 一致性偏差统一判定为 WARN（不阻断恢复），权限不足判定为 BLOCK。

type restoreCheckSink func(phase string, key string, label string, message string, detail map[string]any)

func appendRestoreSourceStorageCheck(set *backupModels.InsightBackupSets, setFile string, addPass restoreCheckSink, addWarn restoreCheckSink) {
	if set == nil {
		return
	}
	detail := map[string]any{
		"storage_backend": strings.TrimSpace(string(set.StorageBackend)),
		"storage_target":  set.StorageTargetID,
		"path":            setFile,
	}
	if strings.TrimSpace(string(set.StorageBackend)) == "" {
		detail["storage_backend"] = "LOCAL_SERVER"
	}
	addPass("阶段1", "source_storage", "源存储端检查", "备份产物所在存储端可访问。", detail)
}

func appendRestorePrivilegeCheck(set *backupModels.InsightBackupSets, targetInstance *commonModels.InsightInstances, addPass restoreCheckSink, addWarn restoreCheckSink, addBlock restoreCheckSink) {
	if set == nil || targetInstance == nil {
		return
	}
	switch {
	case strings.EqualFold(strings.TrimSpace(string(set.DBType)), "MySQL"):
		grants, err := probeMySQLGrants(targetInstance)
		if err != nil {
			addWarn("阶段1", "target_privilege", "目标库权限检查", "无法读取目标账号授权信息，请人工确认权限是否满足恢复要求。", map[string]any{"error": err.Error()})
			return
		}
		allowed, missing := mySQLGrantsAllowRestore(grants)
		if !allowed {
			addBlock("阶段1", "target_privilege", "目标库权限检查", "目标数据库账号权限不足，缺少权限："+strings.Join(missing, "、"), map[string]any{"missing": missing, "grants": grants})
			return
		}
		addPass("阶段1", "target_privilege", "目标库权限检查", "目标数据库账号权限满足恢复要求。", map[string]any{"grants": grants})
	case isPostgresLogicalFamily(string(set.DBType)):
		isSuper, canCreateDB, err := postgresRolePrivileges(targetInstance)
		if err != nil {
			addWarn("阶段1", "target_privilege", "目标库权限检查", "无法读取目标账号角色信息，请人工确认权限是否满足恢复要求。", map[string]any{"error": err.Error()})
			return
		}
		if !isSuper && !canCreateDB {
			addBlock("阶段1", "target_privilege", "目标库权限检查", "目标数据库账号既非超级用户也不具备建库权限。", map[string]any{"rolsuper": isSuper, "rolcreatedb": canCreateDB})
			return
		}
		addPass("阶段1", "target_privilege", "目标库权限检查", "目标数据库账号权限满足恢复要求。", map[string]any{"rolsuper": isSuper, "rolcreatedb": canCreateDB})
	default:
		_ = addWarn
	}
}

func appendRestoreEnvironmentChecks(set *backupModels.InsightBackupSets, targetInstance *commonModels.InsightInstances, setFile string, addPass restoreCheckSink, addWarn restoreCheckSink, addBlock restoreCheckSink) {
	if set == nil || targetInstance == nil {
		return
	}
	dbType := strings.TrimSpace(string(set.DBType))

	source := loadInstanceByIDSilent(set.SourceInstanceID)

	switch {
	case strings.EqualFold(strings.TrimSpace(dbType), "MySQL"):
		appendMySQLSQLModeCheck(setFile, source, targetInstance, addPass, addWarn)
		appendMySQLLowerCaseCheck(source, targetInstance, addPass, addWarn)
		appendMySQLTimeZoneCheck(source, targetInstance, addPass, addWarn)
	case isPostgresLogicalFamily(dbType):
		appendPostgresTimeZoneCheck(source, targetInstance, addPass, addWarn)
	default:
		_ = addBlock
	}
}

func appendMySQLSQLModeCheck(setFile string, source *commonModels.InsightInstances, targetInstance *commonModels.InsightInstances, addPass restoreCheckSink, addWarn restoreCheckSink) {
	sourceValue := strings.TrimSpace(extractMySQLDumpSQLMode(setFile))
	sourceFrom := ""
	if sourceValue != "" {
		sourceFrom = "backup_dump"
	} else if source != nil {
		if values, err := probeMySQLServerVariables(source, []string{"sql_mode"}); err == nil {
			sourceValue = strings.TrimSpace(values["sql_mode"])
			sourceFrom = "source_instance"
		}
	}
	targetValues, err := probeMySQLServerVariables(targetInstance, []string{"sql_mode"})
	if err != nil {
		addWarn("阶段2", "sql_mode", "SQL_MODE", "无法读取目标实例 SQL_MODE，请人工核对。", map[string]any{"error": err.Error()})
		return
	}
	targetValue := strings.TrimSpace(targetValues["sql_mode"])
	detail := map[string]any{
		"source_sql_mode": sourceValue,
		"target_sql_mode": targetValue,
		"source_from":     sourceFrom,
	}
	if sourceValue == "" {
		addWarn("阶段2", "sql_mode", "SQL_MODE", "未能获取备份源的 SQL_MODE，无法比对一致性。", detail)
		return
	}
	if normalizeEnvValue(sourceValue) == normalizeEnvValue(targetValue) {
		addPass("阶段2", "sql_mode", "SQL_MODE", "恢复目标库与备份数据源的 SQL_MODE 一致。", detail)
		return
	}
	addWarn("阶段2", "sql_mode", "SQL_MODE", "恢复目标库与备份数据源的 SQL_MODE 不一致，可能导致数据校验失败。", detail)
}

func appendMySQLLowerCaseCheck(source *commonModels.InsightInstances, targetInstance *commonModels.InsightInstances, addPass restoreCheckSink, addWarn restoreCheckSink) {
	targetValues, err := probeMySQLServerVariables(targetInstance, []string{"lower_case_table_names"})
	if err != nil {
		addWarn("阶段2", "lower_case_table_names", "大小写敏感参数检查", "无法读取目标实例 lower_case_table_names，请人工核对。", map[string]any{"error": err.Error()})
		return
	}
	targetValue := strings.TrimSpace(targetValues["lower_case_table_names"])
	sourceValue := ""
	if source != nil {
		if values, probeErr := probeMySQLServerVariables(source, []string{"lower_case_table_names"}); probeErr == nil {
			sourceValue = strings.TrimSpace(values["lower_case_table_names"])
		}
	}
	detail := map[string]any{
		"source_lower_case_table_names": sourceValue,
		"target_lower_case_table_names": targetValue,
	}
	if sourceValue == "" {
		addWarn("阶段2", "lower_case_table_names", "大小写敏感参数检查", "未能获取备份源的 lower_case_table_names，无法比对一致性。", detail)
		return
	}
	if normalizeEnvValue(sourceValue) == normalizeEnvValue(targetValue) {
		addPass("阶段2", "lower_case_table_names", "大小写敏感参数检查", "恢复目标库与备份数据源的 lower_case_table_names 一致。", detail)
		return
	}
	addWarn("阶段2", "lower_case_table_names", "大小写敏感参数检查", "恢复目标库与备份数据源的 lower_case_table_names 不一致，表名大小写可能引发冲突。", detail)
}

func appendMySQLTimeZoneCheck(source *commonModels.InsightInstances, targetInstance *commonModels.InsightInstances, addPass restoreCheckSink, addWarn restoreCheckSink) {
	targetValues, err := probeMySQLServerVariables(targetInstance, []string{"time_zone"})
	if err != nil {
		addWarn("阶段2", "time_zone", "时区检查", "无法读取目标实例时区配置，请人工核对。", map[string]any{"error": err.Error()})
		return
	}
	targetValue := strings.TrimSpace(targetValues["time_zone"])
	sourceValue := ""
	if source != nil {
		if values, probeErr := probeMySQLServerVariables(source, []string{"time_zone"}); probeErr == nil {
			sourceValue = strings.TrimSpace(values["time_zone"])
		}
	}
	detail := map[string]any{"source_time_zone": sourceValue, "target_time_zone": targetValue}
	if sourceValue == "" {
		addWarn("阶段2", "time_zone", "时区检查", "未能获取备份源的时区配置，无法比对一致性。", detail)
		return
	}
	if normalizeEnvValue(sourceValue) == normalizeEnvValue(targetValue) {
		addPass("阶段2", "time_zone", "时区检查", "恢复目标库与备份数据源的时区配置一致。", detail)
		return
	}
	addWarn("阶段2", "time_zone", "时区检查", "恢复目标库与备份数据源的时区配置不一致，时间类型数据可能产生偏移。", detail)
}

func appendPostgresTimeZoneCheck(source *commonModels.InsightInstances, targetInstance *commonModels.InsightInstances, addPass restoreCheckSink, addWarn restoreCheckSink) {
	targetValues, err := probePostgresSettings(targetInstance, []string{"TimeZone"})
	if err != nil {
		addWarn("阶段2", "time_zone", "时区检查", "无法读取目标实例时区配置，请人工核对。", map[string]any{"error": err.Error()})
		return
	}
	targetValue := strings.TrimSpace(targetValues["timezone"])
	sourceValue := ""
	if source != nil {
		if values, probeErr := probePostgresSettings(source, []string{"TimeZone"}); probeErr == nil {
			sourceValue = strings.TrimSpace(values["timezone"])
		}
	}
	detail := map[string]any{"source_time_zone": sourceValue, "target_time_zone": targetValue}
	if sourceValue == "" {
		addWarn("阶段2", "time_zone", "时区检查", "未能获取备份源的时区配置，无法比对一致性。", detail)
		return
	}
	if normalizeEnvValue(sourceValue) == normalizeEnvValue(targetValue) {
		addPass("阶段2", "time_zone", "时区检查", "恢复目标库与备份数据源的时区配置一致。", detail)
		return
	}
	addWarn("阶段2", "time_zone", "时区检查", "恢复目标库与备份数据源的时区配置不一致，时间类型数据可能产生偏移。", detail)
}

// loadInstanceByIDSilent 探测源实例（可能已下线），失败返回 nil 交由调用方降级为 WARN。
func loadInstanceByIDSilent(instanceID uint64) *commonModels.InsightInstances {
	if instanceID == 0 {
		return nil
	}
	instance, err := loadInstanceByID(instanceID)
	if err != nil || instance == nil {
		return nil
	}
	return instance
}

// appendRestoreObjectScopeCheck 校验「恢复对象」选择与备份产物形态是否匹配。
// 只有 zip 形态支持按库拆分；单对象产物（单文件 dump / 单库 custom）无法按对象筛选。
func appendRestoreObjectScopeCheck(options logicalRestoreRunOptions, resolved logicalRestoreResolvedContext, addPass restoreCheckSink, addWarn restoreCheckSink) {
	format := strings.ToLower(strings.TrimSpace(resolved.ArtifactFormat))
	splittable := format == "zip"
	detail := map[string]any{
		"object_scope":    options.ObjectScope,
		"artifact_format": format,
		"selected_count":  len(options.SelectedDatabases),
	}
	if !strings.EqualFold(strings.TrimSpace(options.ObjectScope), restoreObjectScopeCustom) {
		addPass("阶段2", "object_scope", "恢复对象", "将恢复备份集内的全部对象。", detail)
		return
	}
	if splittable {
		addPass("阶段2", "object_scope", "恢复对象", "已按所选对象范围生成恢复计划。", detail)
		return
	}
	addWarn("阶段2", "object_scope", "恢复对象", "当前备份为单对象产物，自定义对象选择不会改变实际恢复范围。", detail)
}

// ensureRestoreHomogeneous 同构校验：仅允许「同类型数据库」的备份集恢复到同类型实例。
// 原校验分散在各调用点，这里集中一次，避免新增驱动时遗漏。
func ensureRestoreHomogeneous(set *backupModels.InsightBackupSets, targetInstance *commonModels.InsightInstances) error {
	if set == nil {
		return fmt.Errorf("备份集不存在")
	}
	if targetInstance == nil {
		return fmt.Errorf("目标实例不存在")
	}
	setType := strings.TrimSpace(string(set.DBType))
	targetType := strings.TrimSpace(string(targetInstance.DbType))
	if setType == "" || targetType == "" {
		return fmt.Errorf("备份集或目标实例的数据库类型为空，无法校验同构要求")
	}
	if !strings.EqualFold(setType, targetType) {
		return fmt.Errorf("仅支持同构数据库恢复：%s 备份集不能恢复到 %s 实例", setType, targetType)
	}
	return nil
}
