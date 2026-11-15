package services

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	aiForms "github.com/simajiong/jaysql/internal/ai/forms"
	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	backupServices "github.com/simajiong/jaysql/internal/backup/services"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ============================================================
// 确认创建备份任务（AI 对话草稿卡片 → 落库）
//
// 与「确认创建工单」同一套口径：
//   - 模型只负责把意图翻译成结构化参数（实例、备份对象、备份范围、调度）；
//   - 平台负责校验（实例必须已登记、备份方式与数据库类型必须匹配、范围必须合法）；
//   - 落库动作只在用户在卡片上点「确认创建」之后发生，创建人取当前登录会话。
//
// 创建本身复用 backup 模块的 CreateBackupTaskService：预检查、执行节点解析、
// 调度注册、物理备份的 Stanza 探测全部沿用既有实现，AI 链路不另起一套。
// ============================================================

// ConfirmCreateBackupService 用户在 AI 对话里点「确认创建」后真正建备份任务
type ConfirmCreateBackupService struct {
	Form     *aiForms.AICreateBackupConfirmForm
	Username string
	C        *gin.Context
}

// ConfirmCreateBackupResult 返回给对话卡片，用于就地展示创建结果
type ConfirmCreateBackupResult struct {
	TaskID        string `json:"task_id"`
	TaskName      string `json:"task_name"`
	Instance      string `json:"instance"`
	DBType        string `json:"db_type"`
	DriverType    string `json:"driver_type"`
	ObjectSummary string `json:"object_summary"`
	ScheduleText  string `json:"schedule_text"`
	RetentionDays int    `json:"retention_days"`
	StorageTarget string `json:"storage_target"`
	CreatedBy     string `json:"created_by"`
	DetailURL     string `json:"detail_url"`
}

// backupScheduleWeekdays 调度星期白名单（与备份模块 allowedScheduleDays 同一口径）
var backupScheduleWeekdays = map[string]string{
	"MON": "周一", "TUE": "周二", "WED": "周三", "THU": "周四",
	"FRI": "周五", "SAT": "周六", "SUN": "周日",
}

// backupObjectIdentPattern 库名白名单：与备份对象校验同一字符集，杜绝注入
var backupObjectIdentPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (s *ConfirmCreateBackupService) Run() (*ConfirmCreateBackupResult, error) {
	creator := strings.TrimSpace(s.Username)
	if creator == "" {
		return nil, fmt.Errorf("无法确认当前登录用户，已中止创建")
	}
	if s.Form == nil {
		return nil, fmt.Errorf("缺少备份任务参数")
	}

	// 1. 实例必须已在平台登记；数据库类型一律以实例为准，不接受调用方指定
	var instance commonModels.InsightInstances
	err := global.App.DB.Table("`insight_instances`").
		Where("instance_id = ?", strings.TrimSpace(s.Form.InstanceID)).
		First(&instance).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("实例 %s 未在平台登记，请先在「数据源」中录入后再创建备份任务", strings.TrimSpace(s.Form.InstanceID))
		}
		return nil, fmt.Errorf("查询实例失败: %w", err)
	}
	dbType := string(instance.DbType)

	// 2. 备份方式：未指定时按数据库类型推导，并校验与实例类型匹配
	driverType, err := resolveBackupDriverType(dbType, s.Form.DriverType)
	if err != nil {
		return nil, err
	}

	// 3. 备份范围：物理备份是实例级全量，只有逻辑备份才能挑库
	objectMode := strings.ToUpper(strings.TrimSpace(s.Form.ObjectMode))
	if objectMode == "" {
		objectMode = "ALL"
	}
	if isPhysicalBackupDriver(driverType) {
		if objectMode != "ALL" {
			return nil, fmt.Errorf("%s 是实例级全量备份，不支持指定库表，请把 object_mode 改为 ALL", driverTypeText(driverType))
		}
	}
	objectSpec, objectSummary, err := buildBackupObjectSpec(dbType, objectMode, s.Form.Databases)
	if err != nil {
		return nil, err
	}

	// 4. 存储目标：给了关键字就按名字匹配，否则回退备份中心（LOCAL_SERVER）
	storageTarget, err := resolveBackupStorageTarget(s.Form.StorageTarget)
	if err != nil {
		return nil, err
	}

	// 5. 调度与保留天数
	scheduleDays, err := normalizeBackupScheduleDays(s.Form.ScheduleDays)
	if err != nil {
		return nil, err
	}
	scheduleTime := strings.TrimSpace(s.Form.ScheduleTime)
	if scheduleTime == "" {
		scheduleTime = "01:00"
	}
	if _, err := time.Parse("15:04", scheduleTime); err != nil {
		return nil, fmt.Errorf("调度时间格式非法（应为 HH:MM）：%s", scheduleTime)
	}
	retentionDays := s.Form.RetentionDays
	if retentionDays <= 0 {
		retentionDays = 7
	}

	// 6. 复用备份模块的创建服务：预检查 / 节点解析 / 调度注册全部沿用
	form := backupForms.CreateBackupTaskForm{
		Name:             strings.TrimSpace(s.Form.Name),
		SourceInstanceID: instance.ID,
		StorageTargetID:  storageTarget.ID,
		BackupType:       normalizeBackupTaskType(s.Form.BackupType),
		ObjectMode:       objectMode,
		ObjectSpec:       objectSpec,
		DriverType:       driverType,
		ScheduleDays:     scheduleDays,
		ScheduleTime:     scheduleTime,
		RetentionDays:    retentionDays,
	}
	service := backupServices.CreateBackupTaskService{
		CreateBackupTaskForm: &form,
		C:                    s.C,
		CreatedByOverride:    creator,
	}
	task, err := service.Run()
	if err != nil {
		return nil, err
	}

	return &ConfirmCreateBackupResult{
		TaskID:        task.TaskID,
		TaskName:      task.Name,
		Instance:      fmt.Sprintf("%s:%d", strings.TrimSpace(instance.Hostname), instance.Port),
		DBType:        dbType,
		DriverType:    driverType,
		ObjectSummary: objectSummary,
		ScheduleText:  buildBackupScheduleText(scheduleDays, scheduleTime),
		RetentionDays: retentionDays,
		StorageTarget: storageTarget.Name,
		CreatedBy:     creator,
		DetailURL:     "/backup/task",
	}, nil
}

// isKingbaseDBType 金仓（Kingbase / KingbaseES）归一判断。
// AI 侧的备份编排需要与备份模块保持同一套认知：金仓走逻辑备份（sys_dump），
// 它没有 pgBackRest / XtraBackup 工具链，不能被默认推导成物理备份。
func isKingbaseDBType(dbType string) bool {
	switch strings.ToLower(strings.TrimSpace(dbType)) {
	case "kingbase", "kingbasees", "kingbase es", "kingbase-es", "kingbasev8", "kingbase v8":
		return true
	}
	return false
}

// resolveBackupDriverType 备份方式推导与校验：
// 未指定时 MySQL → XTRABACKUP（物理），PostgreSQL → PG_BACKREST（物理），
// 金仓 → LOGICAL（金仓无物理备份工具链，只能逻辑备份）；
// 指定时必须与实例类型匹配，避免模型把物理备份挂到不支持的实例上。
func resolveBackupDriverType(dbType, raw string) (string, error) {
	driver := strings.ToUpper(strings.TrimSpace(raw))
	if driver == "" {
		if isKingbaseDBType(dbType) {
			return "LOGICAL", nil
		}
		if dbType == "PostgreSQL" {
			return "PG_BACKREST", nil
		}
		return "XTRABACKUP", nil
	}
	switch driver {
	case "XTRABACKUP":
		if dbType != "MySQL" {
			return "", fmt.Errorf("XtraBackup 物理备份仅支持 MySQL 实例，当前实例类型为 %s", dbType)
		}
	case "PG_BACKREST":
		if dbType != "PostgreSQL" {
			return "", fmt.Errorf("pgBackRest 物理备份仅支持 PostgreSQL 实例，当前实例类型为 %s", dbType)
		}
	case "LOGICAL":
	default:
		return "", fmt.Errorf("不支持的备份方式：%s（可选 LOGICAL / XTRABACKUP / PG_BACKREST）", driver)
	}
	return driver, nil
}

// buildBackupObjectSpec 组装备份对象：ALL 整实例，CUSTOM 按库（物理备份无对象定义）
func buildBackupObjectSpec(dbType, objectMode string, databases []string) (map[string]any, string, error) {
	if objectMode == "ALL" {
		return nil, "全部实例", nil
	}
	if len(databases) == 0 {
		return nil, "", fmt.Errorf("object_mode=CUSTOM 时必须给出 databases（要备份的库名）")
	}
	names := make([]string, 0, len(databases))
	seen := map[string]struct{}{}
	for _, item := range databases {
		name := strings.TrimSpace(item)
		if name == "" {
			continue
		}
		if !backupObjectIdentPattern.MatchString(name) {
			return nil, "", fmt.Errorf("库名 %s 非法：仅支持字母、数字、下划线(_)与短横线(-)", name)
		}
		if _, dup := seen[strings.ToLower(name)]; dup {
			continue
		}
		seen[strings.ToLower(name)] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, "", fmt.Errorf("object_mode=CUSTOM 时必须给出至少一个合法库名")
	}

	items := make([]map[string]any, 0, len(names))
	for _, name := range names {
		if dbType == "PostgreSQL" || isKingbaseDBType(dbType) {
			// PG / 金仓的库下还有 schema / table 维度，这里只到库级，schemas 留空表示整库
			items = append(items, map[string]any{"name": name, "schemas": []string{}, "tables": []any{}})
			continue
		}
		items = append(items, map[string]any{"name": name, "tables": []string{}})
	}
	return map[string]any{"databases": items}, strings.Join(names, "、"), nil
}

// resolveBackupStorageTarget 按关键字匹配存储目标；无关键字时回退备份中心 LOCAL_SERVER
func resolveBackupStorageTarget(keyword string) (*backupModels.InsightBackupStorageTargets, error) {
	query := global.App.DB.Model(&backupModels.InsightBackupStorageTargets{})
	if kw := strings.TrimSpace(keyword); kw != "" {
		var matched backupModels.InsightBackupStorageTargets
		if err := query.Where("name LIKE ?", "%"+kw+"%").Order("id ASC").First(&matched).Error; err == nil {
			return &matched, nil
		}
	}
	var local backupModels.InsightBackupStorageTargets
	if err := query.Where("type = ?", backupModels.StorageTargetTypeLocalServer).
		Order("connection_status = 'OK' DESC, id ASC").First(&local).Error; err != nil {
		return nil, fmt.Errorf("未找到可用的备份存储目标，请先到「备份中心 → 存储目标」中创建 LOCAL_SERVER 存储目标")
	}
	return &local, nil
}

// normalizeBackupScheduleDays 调度星期归一：空则每天，非法值直接报错（不静默丢弃）
func normalizeBackupScheduleDays(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}, nil
	}
	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, item := range raw {
		day := strings.ToUpper(strings.TrimSpace(item))
		if day == "" {
			continue
		}
		if _, ok := backupScheduleWeekdays[day]; !ok {
			return nil, fmt.Errorf("调度星期 %s 非法，请使用 MON/TUE/WED/THU/FRI/SAT/SUN", item)
		}
		if _, dup := seen[day]; dup {
			continue
		}
		seen[day] = struct{}{}
		out = append(out, day)
	}
	if len(out) == 0 {
		return []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}, nil
	}
	return out, nil
}

// normalizeBackupTaskType 备份类型：留空按全量，其余保持与备份模块一致的 FULL/SCHEMA/DATA
func normalizeBackupTaskType(raw string) string {
	backupType := strings.ToUpper(strings.TrimSpace(raw))
	switch backupType {
	case "SCHEMA", "DATA":
		return backupType
	default:
		return "FULL"
	}
}

func isPhysicalBackupDriver(driverType string) bool {
	return driverType == "XTRABACKUP" || driverType == "PG_BACKREST"
}

func driverTypeText(driverType string) string {
	switch driverType {
	case "XTRABACKUP":
		return "物理备份（XtraBackup）"
	case "PG_BACKREST":
		return "物理备份（pgBackRest）"
	default:
		return "逻辑备份（mysqldump/pg_dump/sys_dump）"
	}
}

func buildBackupScheduleText(days []string, scheduleTime string) string {
	if len(days) == 7 {
		return fmt.Sprintf("每天 %s 执行", scheduleTime)
	}
	names := make([]string, 0, len(days))
	for _, day := range days {
		names = append(names, backupScheduleWeekdays[day])
	}
	return fmt.Sprintf("每周 %s %s 执行", strings.Join(names, "、"), scheduleTime)
}
