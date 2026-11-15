package services

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/requestid"
	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/pagination"
	"github.com/simajiong/jaysql/pkg/secret"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type GetBackupTasksService struct {
	*backupForms.GetBackupTasksForm
	C *gin.Context
}

// #region debug-point B:backup-service-report
func reportBackupExecDebugFromService(c *gin.Context, hypothesisID string, location string, msg string, data map[string]any) {
	go func() {
		if data == nil {
			data = map[string]any{}
		}
		if c != nil {
			if _, exists := data["request_id"]; !exists {
				data["request_id"] = requestid.Get(c)
			}
		}
		debugURL := "http://127.0.0.1:7777/event"
		sessionID := "backup-exec-disconnect"
		if raw, err := os.ReadFile(".dbg/backup-exec-disconnect.env"); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.HasPrefix(line, "DEBUG_SERVER_URL=") {
					debugURL = strings.TrimSpace(strings.TrimPrefix(line, "DEBUG_SERVER_URL="))
				} else if strings.HasPrefix(line, "DEBUG_SESSION_ID=") {
					sessionID = strings.TrimSpace(strings.TrimPrefix(line, "DEBUG_SESSION_ID="))
				}
			}
		}
		payload := map[string]any{
			"sessionId":    sessionID,
			"runId":        "pre-fix",
			"hypothesisId": hypothesisID,
			"location":     location,
			"msg":          "[DEBUG] " + msg,
			"data":         data,
			"ts":           time.Now().UnixMilli(),
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return
		}
		req, err := http.NewRequest(http.MethodPost, debugURL, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 800 * time.Millisecond}
		resp, err := client.Do(req)
		if err == nil && resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
}

// #endregion

func (s *GetBackupTasksService) Run() (data interface{}, total int64, err error) {
	refreshBackupNodeStatus()
	if _, cleanupErr := cleanupStaleStopRequestedRuns(); cleanupErr != nil {
		return nil, 0, cleanupErr
	}
	if _, cleanupErr := cleanupStaleRunningRuns(); cleanupErr != nil {
		return nil, 0, cleanupErr
	}
	type row struct {
		ID                 string                 `json:"id"`
		DisplayID          string                 `json:"display_id"`
		Name               string                 `json:"name"`
		DBType             string                 `json:"db_type"`
		DriverType         string                 `json:"driver_type"`
		SourceInstanceID   uint64                 `json:"source_instance_id"`
		ExecutorNodeID     uint64                 `json:"executor_node_id"`
		ExecutorNodeName   string                 `json:"executor_node_name"`
		RepositoryNodeID   uint64                 `json:"repository_node_id"`
		RepositoryNodeName string                 `json:"repository_node_name"`
		StorageTargetID    uint64                 `json:"storage_target_id"`
		SourceDBName       string                 `json:"source_db_name"`
		ObjectMode         string                 `json:"object_mode"`
		ObjectSpec         datatypes.JSON         `json:"object_spec"`
		ObjectSummary      string                 `json:"object_summary"`
		OutputDir          string                 `json:"output_dir"`
		ScheduleDays       datatypes.JSON         `json:"schedule_days"`
		ScheduleTime       string                 `json:"schedule_time"`
		Enabled            bool                   `json:"enabled"`
		CreatedBy          string                 `json:"created_by"`
		CreatedAt          commonModels.LocalTime `json:"created_at"`
		UpdatedAt          commonModels.LocalTime `json:"updated_at"`
		InstanceHost       string                 `json:"instance_host"`
		InstancePort       int                    `json:"instance_port"`
		InstanceRemark     string                 `json:"instance_remark"`
		ExecutorNodeStatus string                 `json:"executor_node_status"`
		LastStatus         string                 `json:"last_status"`
		LastStartAt        commonModels.LocalTime `json:"last_start_at"`
		LastEndAt          commonModels.LocalTime `json:"last_end_at"`
		LastRunID          uint64                 `json:"last_run_id"`
		LastCreatedBy      string                 `json:"last_created_by"`
		LastTriggerMode    string                 `json:"last_trigger_mode"`
		LastErrorMessage   string                 `json:"last_error_message"`
		LastFilePath       string                 `json:"last_file_path"`
		LastFileName       string                 `json:"last_file_name"`
		LastLogPath        string                 `json:"last_log_path"`
		LastLogExcerpt     string                 `json:"last_log_excerpt"`
		LastSetID          uint64                 `json:"last_set_id"`
		TaskStatus         string                 `json:"task_status"`
	}
	var list []row

	sub := global.App.DB.Table("insight_backup_runs br").
		Select("br.task_id, br.status, br.start_at, br.end_at, br.id, br.created_by, br.error_message, br.file_path, br.file_name, br.log_path, br.log_excerpt").
		Joins("join (select task_id, max(id) as max_id from insight_backup_runs group by task_id) t on br.id = t.max_id")

	setSub := global.App.DB.Table("insight_backup_sets").
		Select("run_id, id")

	tx := global.App.DB.Table("insight_backup_tasks bt").
		Select(`bt.task_id as id, bt.name, bt.db_type, bt.driver_type, bt.source_instance_id, bt.executor_node_id, n.name as executor_node_name, bt.repository_node_id, rn.name as repository_node_name, bt.storage_target_id as storage_target_id, n.status as executor_node_status, bt.source_db_name, bt.object_mode, bt.object_spec, bt.object_summary, bt.output_dir, bt.schedule_days, bt.schedule_time, bt.enabled, bt.created_by, bt.created_at, bt.updated_at,
			i.hostname as instance_host, i.port as instance_port, i.remark as instance_remark,
			r.status as last_status, r.start_at as last_start_at, r.end_at as last_end_at, r.id as last_run_id,
			r.created_by as last_created_by,
			r.error_message as last_error_message, r.file_path as last_file_path, r.file_name as last_file_name,
			r.log_path as last_log_path, r.log_excerpt as last_log_excerpt, s.id as last_set_id`).
		Joins("left join insight_instances i on i.id = bt.source_instance_id").
		Joins("left join insight_backup_nodes n on n.id = bt.executor_node_id").
		Joins("left join insight_backup_nodes rn on rn.id = bt.repository_node_id").
		Joins("left join (?) r on r.task_id = bt.task_id", sub).
		Joins("left join (?) s on s.run_id = r.id", setSub).
		Where("bt.deleted_at IS NULL").
		Order("bt.created_at desc, bt.id desc")

	if s.Search != "" {
		like := "%" + strings.ToLower(strings.TrimSpace(s.Search)) + "%"
		tx = tx.Where(`lower(bt.task_id) like ?
			or lower(bt.name) like ?
			or lower(bt.object_summary) like ?
			or lower(bt.output_dir) like ?
			or lower(i.hostname) like ?
			or lower(i.remark) like ?
			or lower(bt.created_by) like ?`, like, like, like, like, like, like, like)
	}
	if s.DBType != "" {
		tx = tx.Where("bt.db_type = ?", s.DBType)
	}
	if s.InstanceID != 0 {
		tx = tx.Where("bt.source_instance_id = ?", s.InstanceID)
	}
	// 「只看我创建的」：与首页「我的资源 · 备份任务」卡同一口径（created_by = 当前用户）
	if s.Mine {
		tx = tx.Where("bt.created_by = ?", currentUsername(s.C))
	}

	total = pagination.Pager(&s.PaginationQ, tx, &list)
	for i := range list {
		list[i].DisplayID = normalizeBackupTaskID(list[i].ID)
		list[i].TaskStatus = deriveBackupTaskStatus(list[i].Enabled, list[i].LastStatus)
		list[i].LastTriggerMode = deriveTriggerMode(list[i].LastCreatedBy)
	}
	return &list, total, nil
}

func normalizeBackupTaskID(id string) string {
	value := strings.TrimSpace(strings.ToLower(id))
	if strings.HasPrefix(value, "bk-") && len(value) == 15 {
		return value
	}
	return value
}

func countActiveBackupRuns(taskID string) (int64, error) {
	var count int64
	err := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("task_id = ? AND status IN ?", strings.TrimSpace(taskID), []string{"PENDING", "RUNNING", "STOP_REQUESTED"}).
		Count(&count).Error
	return count, err
}

func generateBackupTaskID() (string, error) {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	const randomLen = 12
	buf := make([]byte, randomLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, 0, 3+randomLen)
	out = append(out, 'b', 'k', '-')
	for _, b := range buf {
		out = append(out, alphabet[int(b)%len(alphabet)])
	}
	return string(out), nil
}

func normalizeProvidedBackupTaskID(raw string) (string, error) {
	text := strings.TrimSpace(strings.ToLower(raw))
	if text == "" {
		return "", nil
	}
	if len(text) != 15 || !strings.HasPrefix(text, "bk-") {
		return "", fmt.Errorf("task_id 非法，必须为 bk- 开头的 15 位标识")
	}
	for _, ch := range text[3:] {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'z') {
			return "", fmt.Errorf("task_id 非法，仅支持小写字母和数字")
		}
	}
	return text, nil
}

func deriveBackupTaskStatus(enabled bool, lastStatus string) string {
	switch strings.ToUpper(strings.TrimSpace(lastStatus)) {
	case "RUNNING":
		return "RUNNING"
	case "PENDING":
		return "RUNNING"
	case "STOP_REQUESTED":
		return "RUNNING"
	case "STOPPED":
		return "STOPPED"
	case "FAILED":
		return "FAILED"
	case "SUCCESS":
		return "SUCCESS"
	default:
		return "NOT_STARTED"
	}
}

type CreateBackupTaskService struct {
	*backupForms.CreateBackupTaskForm
	C *gin.Context
	// CreatedByOverride 非空时覆盖创建人/更新人（如 AI 链路标记为 "AI"）
	CreatedByOverride string
}

func (s *CreateBackupTaskService) Run() (*backupModels.InsightBackupTasks, error) {
	instance, err := loadInstanceByID(s.SourceInstanceID)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(string(instance.DbType)), "MySQL") && !isPostgresLogicalFamily(string(instance.DbType)) {
		return nil, fmt.Errorf("仅支持 MySQL/PostgreSQL/Kingbase 实例")
	}

	backupType := normalizeBackupType(s.BackupType)
	driverType, driverConfig, err := normalizeBackupDriverConfig(string(instance.DbType), s.DriverType, backupType, s.DriverConfig)
	if err != nil {
		return nil, err
	}
        if strings.EqualFold(strings.TrimSpace(driverType), "PG_BACKREST") {
		if archiveCommand, archiveErr := probePostgresArchiveCommand(instance); archiveErr == nil {
			if stanza := extractPGBackRestArchiveStanza(archiveCommand); stanza != "" {
				driverConfig, err = setPGBackRestDriverConfigStanza(driverConfig, stanza)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	normalized, err := normalizeObjectSpec(string(instance.DbType), driverType, s.ObjectMode, "", backupType, s.ObjectSpec)
	if err != nil {
		return nil, err
	}
	scheduleDays, err := normalizeScheduleDays(s.ScheduleDays)
	if err != nil {
		return nil, err
	}
	scheduleTime, err := normalizeScheduleTime(s.ScheduleTime)
	if err != nil {
		return nil, err
	}
	retentionDays := normalizeRetentionDays(s.RetentionDays)

	effectiveExecutorNodeID := uint64(0)
        if driverType == "PG_BACKREST" {
		effectiveExecutorNodeID = s.ExecutorNodeID
		if effectiveExecutorNodeID == 0 {
			node, ensureErr := ensurePGBackRestExecutorNodeForInstance(s.C, instance)
			if ensureErr != nil {
				return nil, ensureErr
			}
			effectiveExecutorNodeID = node.ID
		}
        } else if driverType == "XTRABACKUP" {
                if s.ExecutorNodeID != 0 {
                        return nil, fmt.Errorf("MySQL 物理备份不允许手工选择执行节点：xtrabackup/mariabackup 必须在源数据库主机本机执行")
                }
                node, nodeErr := ensureLogicalBackupDBHostNode(s.C, instance)
                if nodeErr != nil {
                        return nil, nodeErr
                }
                if node == nil || node.ID == 0 {
                        return nil, fmt.Errorf("MySQL 物理备份无法自动创建对应 DB 主机备份节点（host=%s）", strings.TrimSpace(instance.Hostname))
                }
                effectiveExecutorNodeID = node.ID
        } else {
		if s.ExecutorNodeID != 0 {
			return nil, fmt.Errorf("逻辑备份不允许选择执行节点：mysqldump/pg_dump 必须在源数据库服务器本机由 DB 主机 Agent 执行（避免 dump 走到远端、拉取全量数据再推备份中心的无效开销）")
		}
		node, nodeErr := ensureLogicalBackupDBHostNode(s.C, instance)
		if nodeErr != nil {
			return nil, nodeErr
		}
		if node == nil || node.ID == 0 {
			return nil, fmt.Errorf("逻辑备份无法自动创建对应 DB 主机备份节点（host=%s）", strings.TrimSpace(instance.Hostname))
		}
		effectiveExecutorNodeID = node.ID
	}
	outputDir, err := resolveBackupOutputBaseDir(normalizeBackupTaskOutputDir(s.OutputDir))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(outputDir) == "" {
		return nil, fmt.Errorf("备份任务必须配置备份目录（output_dir）：用于在 Agent 临时生成元数据/日志和备份归档文件")
	}
	_ = driverType

	enabled := true
	if s.Enabled != nil {
		enabled = *s.Enabled
	}
	repositoryNodeID := uint64(0)
        if driverType == "PG_BACKREST" {
		repositoryNodeID = s.RepositoryNodeID
	}

	storageTargetID, storageBackend, err := resolveAndValidateStorageTargetForTask(driverType, s.StorageTargetID)
	if err != nil {
		return nil, err
	}
	_ = storageBackend
	var storageConfig datatypes.JSON
	scheduleDaysJSON, err := marshalStringSliceOrNil(scheduleDays)
	if err != nil {
		return nil, err
	}

	taskCreator := currentUsername(s.C)
	if strings.TrimSpace(s.CreatedByOverride) != "" {
		taskCreator = strings.TrimSpace(s.CreatedByOverride)
	}
	task := backupModels.InsightBackupTasks{
		TaskID:           "",
		Name:             s.Name,
		DBType:           instance.DbType,
		SourceInstanceID: s.SourceInstanceID,
		ExecutorNodeID:   effectiveExecutorNodeID,
		RepositoryNodeID: repositoryNodeID,
		SourceDBName:     normalized.LegacyDBName,
		BackupType:       commonModels.EnumType(backupType),
		ObjectMode:       commonModels.EnumType(normalized.ObjectMode),
		ObjectSpec:       normalized.ObjectSpec,
		ObjectSummary:    normalized.ObjectSummary,
		OutputDir:        outputDir,
		DriverType:       commonModels.EnumType(driverType),
		DriverConfig:     driverConfig,
		StorageBackend:   commonModels.EnumType(storageBackend),
		StorageConfig:    storageConfig,
		StorageTargetID:  storageTargetID,
		ScheduleDays:     scheduleDaysJSON,
		ScheduleTime:     scheduleTime,
		RetentionDays:    retentionDays,
		Enabled:          enabled,
		CreatedBy:        taskCreator,
		UpdatedBy:        taskCreator,
	}
	providedTaskID, err := normalizeProvidedBackupTaskID(s.TaskID)
	if err != nil {
		return nil, err
	}
	tryCount := 5
	if providedTaskID != "" {
		tryCount = 1
	}
	for i := 0; i < tryCount; i++ {
		taskID := providedTaskID
		if taskID == "" {
			idValue, idErr := generateBackupTaskID()
			if idErr != nil {
				return nil, idErr
			}
			taskID = idValue
		}
		task.TaskID = taskID
		if err := global.App.DB.Create(&task).Error; err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
				if providedTaskID != "" {
					return nil, fmt.Errorf("预校验生成的 task_id 已被占用，请重新执行预校验后再启动任务")
				}
				continue
			}
			return nil, err
		}
		SyncBackupTaskSchedulesAsync()
		return &task, nil
	}
	return nil, fmt.Errorf("生成备份任务ID失败")
}

type UpdateBackupTaskService struct {
	*backupForms.UpdateBackupTaskForm
	C  *gin.Context
	ID string
}

func (s *UpdateBackupTaskService) Run() error {
	instance, err := loadInstanceByID(s.SourceInstanceID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(string(instance.DbType)), "MySQL") && !isPostgresLogicalFamily(string(instance.DbType)) {
		return fmt.Errorf("仅支持 MySQL/PostgreSQL/Kingbase 实例")
	}

	var current backupModels.InsightBackupTasks
	if err := global.App.DB.Where("task_id = ?", s.ID).First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("备份任务不存在")
		}
		return err
	}
	driverTypeInput := strings.TrimSpace(s.DriverType)
	driverConfigInput := s.DriverConfig
	if driverTypeInput == "" {
		driverTypeInput = string(current.DriverType)
		if driverConfigInput == nil && len(current.DriverConfig) > 0 {
			_ = json.Unmarshal(current.DriverConfig, &driverConfigInput)
		}
	}
	backupType := normalizeBackupType(s.BackupType)
	if strings.TrimSpace(s.BackupType) == "" {
		backupType = normalizeBackupType(string(current.BackupType))
	}
	driverType, driverConfig, err := normalizeBackupDriverConfig(string(instance.DbType), driverTypeInput, backupType, driverConfigInput)
	if err != nil {
		return err
	}
        if strings.EqualFold(strings.TrimSpace(driverType), "PG_BACKREST") {
		if archiveCommand, archiveErr := probePostgresArchiveCommand(instance); archiveErr == nil {
			if stanza := extractPGBackRestArchiveStanza(archiveCommand); stanza != "" {
				driverConfig, err = setPGBackRestDriverConfigStanza(driverConfig, stanza)
				if err != nil {
					return err
				}
			}
		}
	}
	var outputDir string
	repositoryNodeID := s.RepositoryNodeID
	if repositoryNodeID == 0 {
		repositoryNodeID = current.RepositoryNodeID
	}
        if driverType != "PG_BACKREST" {
		repositoryNodeID = 0
	}
	normalized, err := normalizeObjectSpec(string(instance.DbType), driverType, s.ObjectMode, "", backupType, s.ObjectSpec)
	if err != nil {
		return err
	}
	scheduleDays, err := mergeScheduleDays(current.ScheduleDays, s.ScheduleDays)
	if err != nil {
		return err
	}
	scheduleTime, err := mergeScheduleTime(current.ScheduleTime, s.ScheduleTime)
	if err != nil {
		return err
	}
	retentionDays := normalizeRetentionDays(current.RetentionDays)
	if s.RetentionDays > 0 {
		retentionDays = normalizeRetentionDays(s.RetentionDays)
	}

	effectiveExecutorNodeID := uint64(0)
        if driverType == "PG_BACKREST" {
		effectiveExecutorNodeID = s.ExecutorNodeID
		if effectiveExecutorNodeID == 0 && current.ExecutorNodeID != 0 {
			effectiveExecutorNodeID = current.ExecutorNodeID
		}
		if effectiveExecutorNodeID == 0 {
			node, ensureErr := ensurePGBackRestExecutorNodeForInstance(s.C, instance)
			if ensureErr != nil {
				return ensureErr
			}
			effectiveExecutorNodeID = node.ID
		}
        } else if driverType == "XTRABACKUP" {
                if s.ExecutorNodeID != 0 && s.ExecutorNodeID != current.ExecutorNodeID {
                        return fmt.Errorf("MySQL 物理备份不允许手工选择执行节点：xtrabackup/mariabackup 必须在源数据库主机本机执行")
                }
                effectiveExecutorNodeID = current.ExecutorNodeID
                if effectiveExecutorNodeID == 0 {
                        node, nodeErr := ensureLogicalBackupDBHostNode(s.C, instance)
                        if nodeErr != nil {
                                return nodeErr
                        }
                        if node == nil || node.ID == 0 {
                                return fmt.Errorf("MySQL 物理备份无法自动创建对应 DB 主机备份节点（host=%s）", strings.TrimSpace(instance.Hostname))
                        }
                        effectiveExecutorNodeID = node.ID
                }
        } else {
		if s.ExecutorNodeID != 0 && s.ExecutorNodeID != current.ExecutorNodeID {
			return fmt.Errorf("逻辑备份不允许选择执行节点：mysqldump/pg_dump 必须在源数据库服务器本机由 DB 主机 Agent 执行")
		}
		effectiveExecutorNodeID = current.ExecutorNodeID
		if effectiveExecutorNodeID == 0 {
			node, nodeErr := ensureLogicalBackupDBHostNode(s.C, instance)
			if nodeErr != nil {
				return nodeErr
			}
			if node == nil || node.ID == 0 {
				return fmt.Errorf("逻辑备份无法自动创建对应 DB 主机备份节点（host=%s）", strings.TrimSpace(instance.Hostname))
			}
			effectiveExecutorNodeID = node.ID
		}
	}
	rawOutputDir := normalizeBackupTaskOutputDir(s.OutputDir)
	if strings.TrimSpace(rawOutputDir) == "" && strings.TrimSpace(current.OutputDir) != "" {
		rawOutputDir = strings.TrimSpace(current.OutputDir)
	}
	outputDir, err = resolveBackupOutputBaseDir(rawOutputDir)
	if err != nil {
		return err
	}
	if strings.TrimSpace(outputDir) == "" {
		return fmt.Errorf("备份任务必须配置备份目录（output_dir）：用于在 Agent 临时生成元数据/日志和备份归档文件")
	}
	_ = driverType

	enabled := true
	if s.Enabled != nil {
		enabled = *s.Enabled
	}

	effectiveStorageTargetID := s.StorageTargetID
	if effectiveStorageTargetID == 0 {
		// 更新请求未携带 storage_target_id 时（如仅改保存时间/调度周期等轻量字段），
		// 回退到任务当前绑定的存储目标，避免误报「请选择备份存储目标」。
		effectiveStorageTargetID = current.StorageTargetID
	}
	storageTargetID, storageBackend, err := resolveAndValidateStorageTargetForTask(driverType, effectiveStorageTargetID)
	if err != nil {
		return err
	}
	_ = storageBackend
	var storageConfig datatypes.JSON
	scheduleDaysJSON, err := marshalStringSliceOrNil(scheduleDays)
	if err != nil {
		return err
	}

	// 仅当本次请求显式带上保存时间时才做保留期对齐：启停、改调度、只改名等轻量操作
	// 不应触碰历史备份。保留期在创建执行记录与备份集时被快照写入、历史上不回溯，
	// 因此写库前先算清「对齐后立即消失的备份」，存在则要求显式确认，避免静默清理。
	if s.RetentionDays > 0 {
		retentionImpact, impactErr := EvaluateTaskRetentionImpact(s.ID, retentionDays)
		if impactErr != nil {
			return impactErr
		}
		if retentionImpact.RequiresConfirm && !s.ConfirmRetentionReconcile {
			return &RetentionReconcileRequiredError{Impact: retentionImpact}
		}
	}

	updates := map[string]any{
		"name":               s.Name,
		"db_type":            instance.DbType,
		"source_instance_id": s.SourceInstanceID,
		"executor_node_id":   effectiveExecutorNodeID,
		"repository_node_id": repositoryNodeID,
		"source_db_name":     normalized.LegacyDBName,
		"backup_type":        backupType,
		"object_mode":        normalized.ObjectMode,
		"object_spec":        normalized.ObjectSpec,
		"object_summary":     normalized.ObjectSummary,
		"output_dir":         outputDir,
		"driver_type":        driverType,
		"driver_config":      driverConfig,
		"storage_backend":    storageBackend,
		"storage_config":     storageConfig,
		"storage_target_id":  storageTargetID,
		"schedule_days":      scheduleDaysJSON,
		"schedule_time":      scheduleTime,
		"retention_days":     retentionDays,
		"enabled":            enabled,
		"updated_by":         currentUsername(s.C),
	}
	if err := global.App.DB.Model(&backupModels.InsightBackupTasks{}).Where("task_id = ?", s.ID).Updates(updates).Error; err != nil {
		return err
	}
	// 对齐历史记录：把该任务下已有执行记录与备份集的保留期同步到当前值。
	// 重算失败不影响任务配置本身已生效（清理器与下次保存会兜底），只记录告警。
	if s.RetentionDays > 0 {
		if runsUpdated, setsUpdated, reconcileErr := ReconcileTaskRetention(s.ID, retentionDays); reconcileErr != nil {
			global.App.Log.Warnf("reconcile backup task retention failed: %v, task_id: %s", reconcileErr, s.ID)
		} else if runsUpdated > 0 || setsUpdated > 0 {
			global.App.Log.Infof("reconcile backup task retention success, task_id: %s, retention_days: %d, runs: %d, sets: %d",
				s.ID, retentionDays, runsUpdated, setsUpdated)
		}
	}
	SyncBackupTaskSchedulesAsync()
	return nil
}

// UpdateBackupTaskNameService 只更新任务名称。
//
// 之所以单独提供，是因为完整 Update 服务会做驱动规范化、执行节点 ensure、
// 输出目录解析、PG archive 探测等副作用；其中「逻辑备份/XTRABACKUP 不允许
// 指定 executor_node_id」的校验会让只带列表字段的改名请求直接失败。
type UpdateBackupTaskNameService struct {
	*backupForms.UpdateBackupTaskNameForm
	C  *gin.Context
	ID string
}

func (s *UpdateBackupTaskNameService) Run() error {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		return fmt.Errorf("任务名称不能为空")
	}
	if runeLen := len([]rune(name)); runeLen < 2 {
		return fmt.Errorf("任务名称至少 2 个字符")
	} else if runeLen > 64 {
		return fmt.Errorf("任务名称最长 64 个字符")
	}
	var current backupModels.InsightBackupTasks
	if err := global.App.DB.Where("task_id = ?", s.ID).First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("备份任务不存在")
		}
		return err
	}
	if strings.TrimSpace(current.Name) == name {
		return nil
	}
	return global.App.DB.Model(&backupModels.InsightBackupTasks{}).
		Where("task_id = ?", s.ID).
		Updates(map[string]any{
			"name":       name,
			"updated_by": currentUsername(s.C),
		}).Error
}

type PhysicalBackupPrepareError struct {
	Stage        string `json:"stage"`
	Title        string `json:"title"`
	Suggestion   string `json:"suggestion"`
	Detail       string `json:"detail"`
	RawError     string `json:"raw_error,omitempty"`
	FixDoc       string `json:"fix_doc,omitempty"`
	instanceHost string
}

func (e *PhysicalBackupPrepareError) Error() string {
	if e == nil {
		return ""
	}
	suffix := ""
	if e.Suggestion != "" {
		suffix = "；" + e.Suggestion
	}
	return e.Title + suffix
}

func AsPhysicalBackupPrepareError(err error) (*PhysicalBackupPrepareError, bool) {
	if err == nil {
		return nil, false
	}
	var target *PhysicalBackupPrepareError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

func newPhysicalPrepareError(stage, title, suggestion string) *PhysicalBackupPrepareError {
	return &PhysicalBackupPrepareError{
		Stage:      stage,
		Title:      title,
		Suggestion: suggestion,
	}
}

func (e *PhysicalBackupPrepareError) WithHost(host string) *PhysicalBackupPrepareError {
	e.instanceHost = host
	return e
}

func (e *PhysicalBackupPrepareError) WithDetail(detail string) *PhysicalBackupPrepareError {
	e.Detail = detail
	return e
}

func (e *PhysicalBackupPrepareError) WithRaw(err error) *PhysicalBackupPrepareError {
	if err == nil {
		return e
	}
	e.RawError = err.Error()
	if e.Detail == "" {
		e.Detail = err.Error()
	}
	return e
}

func resolveSSHCredentialsFromInstance(instance *commonModels.InsightInstances) (sshUser string, sshPassword string, sshPrivateKey string, sshPort int, ok bool, reason string) {
	if instance == nil {
		return "", "", "", 22, false, "实例信息不存在"
	}
	sshPort = 22
	var extra map[string]any
	if len(instance.ExtraConfig) > 0 {
		_ = json.Unmarshal(instance.ExtraConfig, &extra)
	}
	explicitSSHUser := ""
	explicitSSHPwd := ""
	explicitSSHKey := ""
	explicitSSHPort := false
	if extra != nil {
		if v, _ := extra["ssh_port"].(float64); v > 0 {
			sshPort = int(v)
			explicitSSHPort = true
		} else if s, _ := extra["ssh_port"].(string); s != "" {
			if n, parseErr := fmt.Sscanf(s, "%d", &sshPort); parseErr != nil || n != 1 {
				sshPort = 22
			} else {
				explicitSSHPort = true
			}
		}
		explicitSSHUser, _ = extra["ssh_user"].(string)
		explicitSSHPwd, _ = extra["ssh_password"].(string)
		explicitSSHKey, _ = extra["ssh_private_key"].(string)
	}
	explicitSSHUser = strings.TrimSpace(explicitSSHUser)
	// extra_config 里的 SSH 凭据已加密入库；历史明文由 decryptCredential 原样返回
	explicitSSHPwd = decryptCredential(explicitSSHPwd)
	explicitSSHKey = decryptCredential(explicitSSHKey)
	sshUser = explicitSSHUser
	sshPassword = explicitSSHPwd
	sshPrivateKey = explicitSSHKey
	fallbackNote := ""
	if sshUser == "" {
		if strings.TrimSpace(string(instance.User)) != "" {
			sshUser = strings.TrimSpace(string(instance.User))
			fallbackNote = "SSH 用户回退到数据库连接用户（可能不是主机系统用户）"
		} else {
			sshUser = "root"
			fallbackNote = "SSH 用户回退到 root（未在数据源扩展参数中配置 ssh_user）"
		}
	}
	if sshPassword == "" && sshPrivateKey == "" {
		if decrypted, decryptErr := secret.Decrypt(instance.Password); decryptErr == nil {
			fallback := strings.TrimSpace(decrypted)
			if fallback != "" {
				sshPassword = fallback
				if fallbackNote == "" {
					fallbackNote = "SSH 密码回退到数据库连接密码（请确认数据库用户密码与主机用户密码一致）"
				} else {
					fallbackNote += "，且 SSH 密码回退到数据库连接密码（请确认数据库用户密码与主机用户密码一致）"
				}
			}
		}
	}
	sshUser = strings.TrimSpace(sshUser)
	sshPassword = strings.TrimSpace(sshPassword)
	sshPrivateKey = strings.TrimSpace(sshPrivateKey)
	if sshPassword == "" && sshPrivateKey == "" {
		return sshUser, "", "", sshPort, false, "缺少 SSH 登录凭据"
	}
	if fallbackNote != "" {
		_ = explicitSSHUser
		_ = explicitSSHPort
		_ = explicitSSHPwd
		_ = explicitSSHKey
		global.App.Log.Warnf("[resolveSSHCredentialsFromInstance] instance=%s:%d 使用回退凭据：%s", strings.TrimSpace(instance.Hostname), instance.Port, fallbackNote)
	}
	return sshUser, sshPassword, sshPrivateKey, sshPort, true, fallbackNote
}

func defaultExecutorNodeBaseDir() string {
	return "/usr/local/goinsight/backup-repo"
}

func normalizeBackupTaskOutputDir(outputDir string) string {
	text := strings.TrimSpace(outputDir)
	if text == "" {
		return ""
	}
	return text
}

func ensurePGBackRestExecutorNodeForInstance(c *gin.Context, instance *commonModels.InsightInstances) (*backupModels.InsightBackupNodes, error) {
	if instance == nil {
		return nil, fmt.Errorf("数据源不存在")
	}
	host := strings.TrimSpace(instance.Hostname)
	if host == "" {
		return nil, newPhysicalPrepareError(
			"instance",
			"无法自动准备备份程序：数据源主机地址为空",
			"请在「数据源管理」中编辑该数据源，补充主机 IP/域名后再重试",
		)
	}
	existingNode, loadErr := loadBackupNodeByHost(host)
	if loadErr == nil && existingNode != nil {
		sshUserNow, sshPasswordNow, sshPrivateKeyNow, sshPortNow, sshOkNow, _ := resolveSSHCredentialsFromInstance(instance)
		if !sshOkNow {
			return existingNode, nil
		}
		nodeSSHUser := strings.TrimSpace(existingNode.SSHUser)
		// 节点凭据在库里是密文，比对前先解密（历史明文原样返回）
		nodePassword := decryptCredential(existingNode.SSHPassword)
		nodeKey := decryptCredential(existingNode.SSHPrivateKey)
		nodePort := existingNode.Port
		if nodePort <= 0 {
			nodePort = 22
		}
		candidate := strings.TrimSpace(existingNode.Remark)
		autoCreated := strings.Contains(candidate, "自动创建") || strings.HasPrefix(strings.TrimSpace(existingNode.Name), "pg-physical-auto-")
		mismatch := false
		serverURLNow := strings.TrimSpace(existingNode.ServerURL)
		resolvedServerURL, resolveServerErr := resolveBackupNodeServerURL(serverURLNow, c, host)
		if resolveServerErr != nil {
			return nil, newPhysicalPrepareError(
				"server_url",
				"平台服务地址对数据库主机不可达",
				"请使用数据库主机可访问的系统地址打开当前页面，或在通知配置中设置一个可被数据库主机访问的系统地址后重试。",
			).WithHost(host).WithRaw(resolveServerErr)
		}
		if autoCreated {
			if nodeSSHUser != sshUserNow {
				mismatch = true
			}
			if nodePort != sshPortNow {
				mismatch = true
			}
			if sshPasswordNow != "" && nodePassword != sshPasswordNow {
				mismatch = true
			}
			if sshPrivateKeyNow != "" && nodeKey != sshPrivateKeyNow {
				mismatch = true
			}
			if nodePassword == "" && nodeKey == "" {
				mismatch = true
			}
			if serverURLNow != resolvedServerURL {
				mismatch = true
			}
		}
		if autoCreated && mismatch {
			if sshPrivateKeyNow != "" {
				nodeKey = sshPrivateKeyNow
			} else if sshPasswordNow != "" {
				nodePassword = sshPasswordNow
			}
			updates := map[string]any{
				"ssh_user":   sshUserNow,
				"port":       sshPortNow,
				"server_url": resolvedServerURL,
				"updated_at": time.Now(),
				"updated_by": currentUsername(c),
			}
			// 凭据必须加密后写入（幂等：encryptCredential 对已是密文的值原样返回）
			if sshPasswordNow != "" {
				encrypted, err := encryptCredential(sshPasswordNow)
				if err != nil {
					return nil, fmt.Errorf("备份节点 SSH 密码加密失败: %w", err)
				}
				updates["ssh_password"] = encrypted
			}
			if sshPrivateKeyNow != "" {
				encrypted, err := encryptCredential(sshPrivateKeyNow)
				if err != nil {
					return nil, fmt.Errorf("备份节点 SSH 私钥加密失败: %w", err)
				}
				updates["ssh_private_key"] = encrypted
			}
			_ = global.App.DB.Model(&backupModels.InsightBackupNodes{}).
				Where("id = ?", existingNode.ID).
				Updates(updates).Error
			return loadBackupNodeByID(existingNode.ID)
		}
		return existingNode, nil
	}
	if loadErr != nil && !strings.Contains(strings.ToLower(loadErr.Error()), "未找到") && !strings.Contains(strings.ToLower(loadErr.Error()), "not found") {
		return nil, newPhysicalPrepareError(
			"node_lookup",
			"检查主机备份节点失败",
			"请稍后重试，或在「系统管理 → 备份节点」中手动添加一台 host 为 "+host+" 的节点",
		).WithHost(host).WithRaw(loadErr)
	}
	sshUser, sshPassword, sshPrivateKey, sshPort, sshOk, sshReason := resolveSSHCredentialsFromInstance(instance)
	if !sshOk {
		errTitle := "当前数据源未配置主机登录凭据（用户名/密码 或 SSH 密钥）"
		suggestion := "物理备份需要在数据库主机上运行备份程序，请在「数据源管理 → 编辑连接 → 扩展参数」里补充 ssh_user / ssh_password（或 ssh_private_key、ssh_port）信息后重试。"
		if sshReason != "" && strings.Contains(sshReason, "缺少") {
			errTitle = "当前数据源缺少主机 SSH 登录凭据"
		}
		return nil, newPhysicalPrepareError("ssh_credential", errTitle, suggestion).WithHost(host).WithDetail(sshReason)
	}
	baseDir := defaultExecutorNodeBaseDir()
	nodeName := fmt.Sprintf("pg-physical-auto-%s-%d", strings.ReplaceAll(host, ".", "_"), sshPort)
	remarkSuffix := ""
	if strings.TrimSpace(instance.Remark) != "" {
		remarkSuffix = fmt.Sprintf("（关联数据源：%s）", instance.Remark)
	}
	remark := fmt.Sprintf("系统在创建 PostgreSQL 物理备份任务时自动创建，请勿删除%s", remarkSuffix)
	token := strings.ReplaceAll(func() string {
		b := make([]byte, 18)
		_, _ = rand.Read(b)
		return fmt.Sprintf("%x", b)
	}(), "-", "")
	serverURL, resolveErr := resolveBackupNodeServerURL("", c, host)
	if resolveErr != nil {
		return nil, newPhysicalPrepareError(
			"server_url",
			"平台服务地址对数据库主机不可达",
			"请使用数据库主机可访问的系统地址打开当前页面，或在通知配置中设置一个可被数据库主机访问的系统地址后重试。",
		).WithHost(host).WithRaw(resolveErr)
	}
	// 节点凭据加密入库；sshUser/sshPassword 等明文仍用于本次安装操作（不落库）
	encryptedPassword, encErr := encryptCredential(sshPassword)
	if encErr != nil {
		return nil, newPhysicalPrepareError("ssh_credential", "备份节点 SSH 密码加密失败", "请检查系统密钥配置后重试").WithHost(host).WithRaw(encErr)
	}
	encryptedKey, encErr := encryptCredential(sshPrivateKey)
	if encErr != nil {
		return nil, newPhysicalPrepareError("ssh_credential", "备份节点 SSH 私钥加密失败", "请检查系统密钥配置后重试").WithHost(host).WithRaw(encErr)
	}
	node := &backupModels.InsightBackupNodes{
		Model:         &commonModels.Model{},
		Name:          nodeName,
		Host:          host,
		Port:          sshPort,
		BaseDir:       baseDir,
		SSHUser:       sshUser,
		SSHPassword:   encryptedPassword,
		SSHPrivateKey: encryptedKey,
		ServerURL:     serverURL,
		InstallDir:    defaultBackupNodeInstallDir(""),
		ToolsDir:      defaultBackupNodeToolsDir(""),
		ServiceName:   "",
		LogPath:       "",
		Token:         token,
		Status:        commonModels.EnumType("OFFLINE"),
		Remark:        remark,
		CreatedBy:     currentUsername(c),
		UpdatedBy:     currentUsername(c),
		LastHeartbeat: commonModels.LocalTime{},
	}
	node.ServiceName = defaultBackupNodeServiceName("", 0)
	node.LogPath = defaultBackupNodeLogPath("", node.InstallDir)
	createErr := global.App.DB.Create(node).Error
	if createErr != nil {
		if strings.Contains(strings.ToLower(createErr.Error()), "duplicate") || strings.Contains(strings.ToLower(createErr.Error()), "unique") {
			return loadBackupNodeByHost(host)
		}
		return nil, newPhysicalPrepareError(
			"node_create",
			"为数据库主机自动创建备份节点失败",
			"请确认平台数据库可写入，或手动在「系统管理 → 备份节点」里新增一条 host="+host+" 的节点",
		).WithHost(host).WithRaw(createErr)
	}
	node.ServiceName = defaultBackupNodeServiceName("", node.ID)
	node.LogPath = defaultBackupNodeLogPath("", node.InstallDir)
	_ = global.App.DB.Model(&backupModels.InsightBackupNodes{}).
		Where("id = ?", node.ID).
		Updates(map[string]any{
			"service_name": node.ServiceName,
			"log_path":     node.LogPath,
			"updated_by":   currentUsername(c),
		}).Error
	operateForm := &backupForms.OperateBackupNodeForm{
		Action:        "install_agent",
		SSHUser:       sshUser,
		SSHPassword:   sshPassword,
		SSHPrivateKey: sshPrivateKey,
		ServerURL:     serverURL,
		InstallDir:    node.InstallDir,
		ToolsDir:      node.ToolsDir,
		ServiceName:   node.ServiceName,
		LogPath:       node.LogPath,
		WaitOnlineSec: 15,
	}
	operate := OperateBackupNodeService{
		OperateBackupNodeForm: operateForm,
		C:                     c,
		ID:                    node.ID,
	}
	_, operateErr := operate.Run()
	if operateErr == nil {
		return loadBackupNodeByID(node.ID)
	}
	errMsg := strings.ToLower(operateErr.Error())
	title := ""
	suggestion := ""
	stage := "install_agent"
	switch {
	case strings.Contains(errMsg, "连接备份机失败"),
		strings.Contains(errMsg, "timeout"),
		strings.Contains(errMsg, "i/o timeout"),
		strings.Contains(errMsg, "connection refused"),
		strings.Contains(errMsg, "no route to host"):
		title = fmt.Sprintf("无法连接到数据库主机 %s:%d", host, sshPort)
		suggestion = "请检查：数据库主机 IP 是否可达、防火墙是否放行了 22 端口（或自定义 SSH 端口）、SSH 服务是否已启动。"
		stage = "ssh_connect"
	case strings.Contains(errMsg, "ssh: handshake failed"),
		strings.Contains(errMsg, "unable to authenticate"),
		strings.Contains(errMsg, "password"),
		strings.Contains(errMsg, "私钥"),
		strings.Contains(errMsg, "permission denied"):
		title = fmt.Sprintf("登录数据库主机失败：SSH 用户 %s 认证失败", sshUser)
		suggestion = "请在「数据源管理 → 编辑连接 → 扩展参数」里确认 ssh_user、ssh_password/ssh_private_key 是否正确，注意有些主机禁用了密码登录。"
		stage = "ssh_auth"
	case strings.Contains(errMsg, "sudo"), strings.Contains(errMsg, "权限"), strings.Contains(errMsg, "permission denied"):
		title = fmt.Sprintf("数据库主机用户 %s 权限不足", sshUser)
		suggestion = "备份程序需要在主机上创建目录、部署 Agent，建议使用 root 或具有 sudo NOPASSWD 权限的账户；或提前创建 /usr/local/goinsight/ 并赋予该用户写权限。"
		stage = "ssh_perm"
	case strings.Contains(errMsg, "空间不足"), strings.Contains(errMsg, "no space"):
		title = "数据库主机磁盘空间不足，无法部署备份程序"
		suggestion = "请清理 /usr/local/goinsight、/tmp 或根分区空间后重试。"
		stage = "install_disk"
	case strings.Contains(errMsg, "systemctl") || strings.Contains(errMsg, "systemd"):
		title = "备份程序部署成功，但启动失败（systemd 托管失败）"
		suggestion = "请确保数据库主机已启用 systemd，或手动执行安装脚本中生成的 systemctl enable --now 命令。"
		stage = "install_systemd"
	default:
		title = "备份程序初始化失败，请检查数据库主机环境"
		suggestion = "以上错误来自数据库主机执行日志，建议复制「详细信息」贴给运维/DBA，或先手动 SSH 登录主机确认网络、账户、权限、磁盘情况后再重试。"
		stage = "install_general"
	}
	return nil, newPhysicalPrepareError(stage, title, suggestion).
		WithHost(host).
		WithDetail(operateErr.Error())
}

func ensureLogicalBackupDBHostNode(c *gin.Context, instance *commonModels.InsightInstances) (*backupModels.InsightBackupNodes, error) {
	if instance == nil {
		return nil, fmt.Errorf("数据源不存在")
	}
	host := strings.TrimSpace(instance.Hostname)
	if host == "" {
		return nil, fmt.Errorf("逻辑备份需要数据库主机地址，但当前数据源 hostname 为空，请先编辑数据源补充主机 IP/域名")
	}
	existingNode, loadErr := loadBackupNodeByHost(host)
	if loadErr == nil && existingNode != nil {
		sshUserNow, sshPasswordNow, sshPrivateKeyNow, sshPortNow, sshOkNow, _ := resolveSSHCredentialsFromInstance(instance)
		if !sshOkNow {
			return existingNode, nil
		}
		nodeSSHUser := strings.TrimSpace(existingNode.SSHUser)
		// 节点凭据在库里是密文，比对前先解密（历史明文原样返回）
		nodePassword := decryptCredential(existingNode.SSHPassword)
		nodeKey := decryptCredential(existingNode.SSHPrivateKey)
		nodePort := existingNode.Port
		if nodePort <= 0 {
			nodePort = 22
		}
		candidate := strings.TrimSpace(existingNode.Remark)
		nodeNameNow := strings.TrimSpace(existingNode.Name)
		autoCreated := strings.Contains(candidate, "自动创建") ||
			strings.Contains(candidate, "逻辑备份") ||
			strings.Contains(candidate, "DB 主机") ||
			strings.HasPrefix(nodeNameNow, "dbhost-auto-") ||
			strings.HasPrefix(nodeNameNow, "logical-auto-")
		mismatch := false
		serverURLNow := strings.TrimSpace(existingNode.ServerURL)
		resolvedServerURL, resolveServerErr := resolveBackupNodeServerURL(serverURLNow, c, host)
		if resolveServerErr != nil {
			return nil, fmt.Errorf("校验 DB 主机 %s 的平台服务地址失败：%w。请使用数据库主机可访问的系统地址打开当前页面，或在通知配置中设置可被数据库主机访问的系统地址后重试", host, resolveServerErr)
		}
		if autoCreated {
			if nodeSSHUser != sshUserNow {
				mismatch = true
			}
			if nodePort != sshPortNow {
				mismatch = true
			}
			if sshPasswordNow != "" && nodePassword != sshPasswordNow {
				mismatch = true
			}
			if sshPrivateKeyNow != "" && nodeKey != sshPrivateKeyNow {
				mismatch = true
			}
			if nodePassword == "" && nodeKey == "" {
				mismatch = true
			}
			if serverURLNow != resolvedServerURL {
				mismatch = true
			}
		}
		if autoCreated && mismatch {
			if sshPrivateKeyNow != "" {
				nodeKey = sshPrivateKeyNow
			} else if sshPasswordNow != "" {
				nodePassword = sshPasswordNow
			}
			updates := map[string]any{
				"ssh_user":   sshUserNow,
				"port":       sshPortNow,
				"server_url": resolvedServerURL,
				"updated_at": time.Now(),
				"updated_by": currentUsername(c),
			}
			// 凭据必须加密后写入（幂等：encryptCredential 对已是密文的值原样返回）
			if sshPasswordNow != "" {
				encrypted, err := encryptCredential(sshPasswordNow)
				if err != nil {
					return nil, fmt.Errorf("备份节点 SSH 密码加密失败: %w", err)
				}
				updates["ssh_password"] = encrypted
			}
			if sshPrivateKeyNow != "" {
				encrypted, err := encryptCredential(sshPrivateKeyNow)
				if err != nil {
					return nil, fmt.Errorf("备份节点 SSH 私钥加密失败: %w", err)
				}
				updates["ssh_private_key"] = encrypted
			}
			_ = global.App.DB.Model(&backupModels.InsightBackupNodes{}).
				Where("id = ?", existingNode.ID).
				Updates(updates).Error
			return loadBackupNodeByID(existingNode.ID)
		}
		return existingNode, nil
	}
	if loadErr != nil && !strings.Contains(strings.ToLower(loadErr.Error()), "未找到") && !strings.Contains(strings.ToLower(loadErr.Error()), "not found") {
		return nil, fmt.Errorf("检查 DB 主机备份节点失败（host=%s）：%w", host, loadErr)
	}
	sshUser, sshPassword, sshPrivateKey, sshPort, sshOk, sshReason := resolveSSHCredentialsFromInstance(instance)
	if !sshOk {
		return nil, fmt.Errorf("逻辑备份需要数据库主机 SSH 凭据：当前数据源（%s）未配置 ssh_user/ssh_password（或 ssh_private_key、ssh_port）。请在「数据源管理 → 编辑连接 → 扩展参数」里补充 SSH 登录信息后重试。细节：%s", host, sshReason)
	}
	baseDir := defaultExecutorNodeBaseDir()
	suffix := ""
	if strings.TrimSpace(instance.Remark) != "" {
		suffix = fmt.Sprintf("（关联数据源：%s）", strings.TrimSpace(instance.Remark))
	}
	nodeName := fmt.Sprintf("dbhost-auto-%s-%d", strings.ReplaceAll(host, ".", "_"), sshPort)
	remark := fmt.Sprintf("系统在创建逻辑备份任务时自动创建，对应 DB 主机 %s；请勿删除。逻辑备份=DB 主机本机执行 mysqldump/pg_dump，再管道直传备份存储目标。%s", host, suffix)
	token := strings.ReplaceAll(func() string {
		b := make([]byte, 20)
		_, _ = rand.Read(b)
		return fmt.Sprintf("%x", b)
	}(), "-", "")
	serverURL, resolveErr := resolveBackupNodeServerURL("", c, host)
	if resolveErr != nil {
		return nil, fmt.Errorf("为 DB 主机 %s 自动创建备份节点前，校验平台服务地址失败：%w。请使用数据库主机可访问的系统地址打开当前页面，或在通知配置中设置可被数据库主机访问的系统地址后重试", host, resolveErr)
	}
	encryptedPassword, encErr := encryptCredential(sshPassword)
	if encErr != nil {
		return nil, fmt.Errorf("备份节点 SSH 密码加密失败：%w", encErr)
	}
	encryptedKey, encErr := encryptCredential(sshPrivateKey)
	if encErr != nil {
		return nil, fmt.Errorf("备份节点 SSH 私钥加密失败：%w", encErr)
	}
	node := &backupModels.InsightBackupNodes{
		Model:         &commonModels.Model{},
		Name:          nodeName,
		Host:          host,
		Port:          sshPort,
		BaseDir:       baseDir,
		SSHUser:       sshUser,
		SSHPassword:   encryptedPassword,
		SSHPrivateKey: encryptedKey,
		ServerURL:     serverURL,
		InstallDir:    defaultBackupNodeInstallDir(""),
		ToolsDir:      defaultBackupNodeToolsDir(""),
		ServiceName:   "",
		LogPath:       "",
		Token:         token,
		Status:        commonModels.EnumType("OFFLINE"),
		Remark:        remark,
		CreatedBy:     currentUsername(c),
		UpdatedBy:     currentUsername(c),
		LastHeartbeat: commonModels.LocalTime{},
	}
	node.ServiceName = defaultBackupNodeServiceName("", 0)
	node.LogPath = defaultBackupNodeLogPath("", node.InstallDir)
	if createErr := global.App.DB.Create(node).Error; createErr != nil {
		if strings.Contains(strings.ToLower(createErr.Error()), "duplicate") || strings.Contains(strings.ToLower(createErr.Error()), "unique") {
			return loadBackupNodeByHost(host)
		}
		return nil, fmt.Errorf("为 DB 主机 %s 自动创建备份节点记录失败：%w", host, createErr)
	}
	node.ServiceName = defaultBackupNodeServiceName("", node.ID)
	node.LogPath = defaultBackupNodeLogPath("", node.InstallDir)
	_ = global.App.DB.Model(&backupModels.InsightBackupNodes{}).
		Where("id = ?", node.ID).
		Updates(map[string]any{
			"service_name": node.ServiceName,
			"log_path":     node.LogPath,
			"updated_by":   currentUsername(c),
		}).Error
	operateForm := &backupForms.OperateBackupNodeForm{
		Action:        "install_agent",
		SSHUser:       sshUser,
		SSHPassword:   sshPassword,
		SSHPrivateKey: sshPrivateKey,
		ServerURL:     serverURL,
		InstallDir:    node.InstallDir,
		ToolsDir:      node.ToolsDir,
		ServiceName:   node.ServiceName,
		LogPath:       node.LogPath,
		WaitOnlineSec: 15,
	}
	operate := OperateBackupNodeService{
		OperateBackupNodeForm: operateForm,
		C:                     c,
		ID:                    node.ID,
	}
	_, operateErr := operate.Run()
	if operateErr == nil {
		return loadBackupNodeByID(node.ID)
	}
	errMsg := strings.ToLower(operateErr.Error())
	title := ""
	suggestion := ""
	stage := "install_agent"
	switch {
	case strings.Contains(errMsg, "连接备份机失败"),
		strings.Contains(errMsg, "timeout"),
		strings.Contains(errMsg, "i/o timeout"),
		strings.Contains(errMsg, "connection refused"),
		strings.Contains(errMsg, "no route to host"):
		title = fmt.Sprintf("无法连接到数据库主机 %s:%d", host, sshPort)
		suggestion = "请检查：数据库主机 IP 是否可达、防火墙是否放行了 22 端口（或自定义 SSH 端口）、SSH 服务是否已启动。"
		stage = "ssh_connect"
	case strings.Contains(errMsg, "ssh: handshake failed"),
		strings.Contains(errMsg, "unable to authenticate"),
		strings.Contains(errMsg, "password"),
		strings.Contains(errMsg, "私钥"),
		strings.Contains(errMsg, "permission denied"):
		title = fmt.Sprintf("登录数据库主机失败：SSH 用户 %s 认证失败", sshUser)
		suggestion = "请在「数据源管理 → 编辑连接 → 扩展参数」里确认 ssh_user、ssh_password/ssh_private_key 是否正确，注意有些主机禁用了密码登录。"
		stage = "ssh_auth"
	case strings.Contains(errMsg, "sudo"), strings.Contains(errMsg, "权限"), strings.Contains(errMsg, "permission denied"):
		title = fmt.Sprintf("数据库主机用户 %s 权限不足", sshUser)
		suggestion = "备份程序需要在主机上创建目录、部署 Agent；SSH 用户必须具有 passwordless sudo 权限；请先在数据库主机以 root 身份执行：echo '" + sshUser + " ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/99-goinsight-backup-agent && chmod 440 /etc/sudoers.d/99-goinsight-backup-agent"
		stage = "ssh_perm"
	case strings.Contains(errMsg, "空间不足"), strings.Contains(errMsg, "no space"):
		title = "数据库主机磁盘空间不足，无法部署备份程序"
		suggestion = "请清理 /usr/local/goinsight、/tmp 或根分区空间后重试。"
		stage = "install_disk"
	case strings.Contains(errMsg, "systemctl") || strings.Contains(errMsg, "systemd"):
		title = "备份程序部署成功，但启动失败（systemd 托管失败）"
		suggestion = "请确保数据库主机已启用 systemd，或手动执行安装脚本中生成的 systemctl enable --now 命令。"
		stage = "install_systemd"
	default:
		title = "备份程序初始化失败，请检查数据库主机环境"
		suggestion = "以上错误来自数据库主机执行日志，建议复制「详细信息」贴给运维/DBA，或先手动 SSH 登录主机确认网络、账户、权限、磁盘情况后再重试。"
		stage = "install_general"
	}
	_ = global.App.DB.Model(&backupModels.InsightBackupNodes{}).
		Where("id = ?", node.ID).
		Updates(map[string]any{
			"remark":     fmt.Sprintf("部署失败（%s）：%s；请根据错误提示修复后重新保存备份任务/数据源，系统会自动重试部署", stage, truncateString(operateErr.Error(), 220)),
			"status":     commonModels.EnumType("OFFLINE"),
			"updated_at": time.Now(),
			"updated_by": currentUsername(c),
		}).Error
	return nil, newPhysicalPrepareError(stage, title, suggestion).
		WithHost(host).
		WithDetail(operateErr.Error())
}

func truncateString(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

type DeleteBackupTaskService struct {
	*backupForms.DeleteBackupTaskForm
	C  *gin.Context
	ID string
}

func (s *DeleteBackupTaskService) Run() error {
	var task backupModels.InsightBackupTasks
	if err := global.App.DB.Where("task_id = ?", s.ID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("备份任务不存在")
		}
		return err
	}
	if strings.TrimSpace(task.Name) != strings.TrimSpace(s.ConfirmName) {
		return fmt.Errorf("任务名称输入不正确")
	}
	if _, err := cleanupStaleStopRequestedRunsForTask(s.ID); err != nil {
		return err
	}

	activeStatuses := []string{"PENDING", "RUNNING", "STOP_REQUESTED"}
	precleanCutoff := commonModels.LocalTime(nowLocalTime().Add(-10 * time.Second))
	precleanReason := fmt.Sprintf("删除前自动收口：任务 %s 长时间未真正进入执行且活动状态超过 %d 秒未变更，已终止", s.ID, 10)
	{
		updates := map[string]any{
			"status":        "STOPPED",
			"end_at":        commonModels.LocalTime(nowLocalTime()),
			"error_message": precleanReason,
			"updated_at":    nowLocalTime(),
		}
		tx := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
			Where("task_id = ? AND status IN ? AND updated_at <= ?", s.ID, []string{"PENDING", "STOP_REQUESTED"}, precleanCutoff)
		_ = tx.Updates(updates).Error
	}
	_ = global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("task_id = ? AND status = ? AND start_at IS NOT NULL AND start_at <= ?", s.ID, "RUNNING", precleanCutoff).
		Updates(map[string]any{
			"status":        "STOPPED",
			"end_at":        commonModels.LocalTime(nowLocalTime()),
			"error_message": fmt.Sprintf("删除前自动收口：任务 %s 处于运行中但活动状态超过 %d 秒未变更，已终止（请在执行节点侧检查残留进程）", s.ID, 10),
			"updated_at":    nowLocalTime(),
		}).Error

	var activeRuns int64
	if err := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Where("task_id = ? AND status IN ?", s.ID, activeStatuses).
		Count(&activeRuns).Error; err != nil {
		return err
	}
	if activeRuns > 0 {
		return fmt.Errorf("当前任务仍存在执行中的备份记录，无法删除！")
	}

	var executorNode *backupModels.InsightBackupNodes
	if task.ExecutorNodeID > 0 {
		executorNode, _ = loadBackupNodeByID(task.ExecutorNodeID)
	}
	if err := global.App.DB.Delete(&task).Error; err != nil {
		return err
	}
	tryAutoStopBackupNodeAfterTaskDeletion(s.C, executorNode, task.TaskID)
	SyncBackupTaskSchedulesAsync()
	return nil
}

func tryAutoStopBackupNodeAfterTaskDeletion(c *gin.Context, node *backupModels.InsightBackupNodes, deletedTaskID string) {
	if node == nil || node.ID == 0 {
		return
	}
	if !isAutoCreatedBackupNode(node) {
		return
	}
	shouldStop, reason, err := shouldStopAutoCreatedBackupNode(node, deletedTaskID)
	if err != nil {
		global.App.Log.Warnf("删除任务后评估自动节点回收失败 node_id=%d host=%s task_id=%s err=%v", node.ID, strings.TrimSpace(node.Host), deletedTaskID, err)
		return
	}
	if !shouldStop {
		global.App.Log.Infof("删除任务后保留自动节点运行 node_id=%d host=%s task_id=%s reason=%s", node.ID, strings.TrimSpace(node.Host), deletedTaskID, reason)
		return
	}
	form := &backupForms.OperateBackupNodeForm{
		Action:        "stop_agent",
		SSHUser:       strings.TrimSpace(node.SSHUser),
		// 库里的节点凭据是密文，操作前解密（历史明文原样返回）
		SSHPassword:   decryptCredential(node.SSHPassword),
		SSHPrivateKey: decryptCredential(node.SSHPrivateKey),
		ServerURL:     strings.TrimSpace(node.ServerURL),
		InstallDir:    strings.TrimSpace(node.InstallDir),
		ToolsDir:      strings.TrimSpace(node.ToolsDir),
		ServiceName:   strings.TrimSpace(node.ServiceName),
		LogPath:       strings.TrimSpace(node.LogPath),
	}
	service := OperateBackupNodeService{
		OperateBackupNodeForm: form,
		C:                     c,
		ID:                    node.ID,
	}
	if _, err := service.Run(); err != nil {
		global.App.Log.Warnf("删除任务后自动停止 backup-agent 失败 node_id=%d host=%s task_id=%s err=%v", node.ID, strings.TrimSpace(node.Host), deletedTaskID, err)
		return
	}
	global.App.Log.Infof("删除任务后自动停止 backup-agent 成功 node_id=%d host=%s task_id=%s", node.ID, strings.TrimSpace(node.Host), deletedTaskID)
}

func isAutoCreatedBackupNode(node *backupModels.InsightBackupNodes) bool {
	if node == nil {
		return false
	}
	remark := strings.TrimSpace(node.Remark)
	name := strings.TrimSpace(node.Name)
	return strings.Contains(remark, "自动创建") ||
		strings.Contains(remark, "逻辑备份") ||
		strings.Contains(remark, "DB 主机") ||
		strings.HasPrefix(name, "dbhost-auto-") ||
		strings.HasPrefix(name, "logical-auto-") ||
		strings.HasPrefix(name, "pg-physical-auto-")
}

func shouldStopAutoCreatedBackupNode(node *backupModels.InsightBackupNodes, deletedTaskID string) (bool, string, error) {
	if node == nil || node.ID == 0 {
		return false, "节点不存在", nil
	}
	hostNorm := normalizeBackupNodeHost(node.Host)
	if hostNorm == "" {
		return false, "节点主机为空", nil
	}
	type backupTaskUsageRow struct {
		TaskID            string
		SourceHost        string
		DriverType        string
		StorageTargetType string
		StorageTargetHost string
	}
	var taskRows []backupTaskUsageRow
	taskQuery := global.App.DB.Table("insight_backup_tasks bt").
		Select("bt.task_id, i.hostname as source_host, bt.driver_type, st.type as storage_target_type, st.host as storage_target_host").
		Joins("left join insight_instances i on i.id = bt.source_instance_id").
		Joins("left join insight_backup_storage_targets st on st.id = bt.storage_target_id and st.deleted_at is null").
		Where("bt.deleted_at IS NULL")
	if strings.TrimSpace(deletedTaskID) != "" {
		taskQuery = taskQuery.Where("bt.task_id <> ?", strings.TrimSpace(deletedTaskID))
	}
	if err := taskQuery.Scan(&taskRows).Error; err != nil {
		return false, "", err
	}
	for _, row := range taskRows {
		if normalizeBackupNodeHost(row.SourceHost) == hostNorm {
			return false, fmt.Sprintf("主机 %s 仍存在其他备份任务 %s", strings.TrimSpace(node.Host), strings.TrimSpace(row.TaskID)), nil
		}
		if strings.EqualFold(strings.TrimSpace(row.DriverType), "PG_BACKREST") &&
			strings.EqualFold(strings.TrimSpace(row.StorageTargetType), backupModels.StorageTargetTypeLocalServer) &&
			normalizeBackupNodeHost(row.StorageTargetHost) == hostNorm {
			return false, fmt.Sprintf("主机 %s 仍被其他 pgBackRest 任务作为仓库节点使用", strings.TrimSpace(node.Host)), nil
		}
	}

	type backupRunUsageRow struct {
		RunID             uint64
		SourceHost        string
		DriverType        string
		StorageTargetType string
		StorageTargetHost string
	}
	var runRows []backupRunUsageRow
	if err := global.App.DB.Table("insight_backup_runs br").
		Select("br.id as run_id, i.hostname as source_host, bt.driver_type, st.type as storage_target_type, st.host as storage_target_host").
		Joins("join insight_backup_tasks bt on bt.task_id = br.task_id and bt.deleted_at is null").
		Joins("left join insight_instances i on i.id = bt.source_instance_id").
		Joins("left join insight_backup_storage_targets st on st.id = bt.storage_target_id and st.deleted_at is null").
		Where("br.status IN ?", []string{"PENDING", "RUNNING", "STOP_REQUESTED"}).
		Scan(&runRows).Error; err != nil {
		return false, "", err
	}
	for _, row := range runRows {
		if normalizeBackupNodeHost(row.SourceHost) == hostNorm {
			return false, fmt.Sprintf("主机 %s 仍存在待执行或运行中的备份", strings.TrimSpace(node.Host)), nil
		}
		if strings.EqualFold(strings.TrimSpace(row.DriverType), "PG_BACKREST") &&
			strings.EqualFold(strings.TrimSpace(row.StorageTargetType), backupModels.StorageTargetTypeLocalServer) &&
			normalizeBackupNodeHost(row.StorageTargetHost) == hostNorm {
			return false, fmt.Sprintf("主机 %s 仍承担运行中的 pgBackRest 仓库用途", strings.TrimSpace(node.Host)), nil
		}
	}

	type restoreUsageRow struct {
		RunID        uint64
		TargetHost   string
		TargetNode   string
		TargetNodeID uint64
	}
	var restoreRows []restoreUsageRow
	if err := global.App.DB.Table("insight_restore_runs rr").
		Select("rr.id as run_id, rr.target_host, n.host as target_node, rr.target_node_id").
		Joins("left join insight_backup_nodes n on n.id = rr.target_node_id").
		Where("rr.status IN ?", []string{"PENDING", "RUNNING", "STOP_REQUESTED"}).
		Scan(&restoreRows).Error; err != nil {
		return false, "", err
	}
	for _, row := range restoreRows {
		if normalizeBackupNodeHost(row.TargetHost) == hostNorm || normalizeBackupNodeHost(row.TargetNode) == hostNorm {
			return false, fmt.Sprintf("主机 %s 仍存在待执行或运行中的恢复任务", strings.TrimSpace(node.Host)), nil
		}
	}
	return true, "主机已无其他任务、恢复或仓库用途", nil
}

type RunBackupTaskService struct {
	C  *gin.Context
	ID string
}

func (s *RunBackupTaskService) Run() (*backupModels.InsightBackupRuns, error) {
	// #region debug-point B:run-backup-task-service-entry
	reportBackupExecDebugFromService(s.C, "B", "backup/services/tasks.go:RunBackupTaskService:entry", "run backup task service started", map[string]any{
		"task_id": s.ID,
	})
	// #endregion
	var task backupModels.InsightBackupTasks
	if err := global.App.DB.Where("task_id = ?", s.ID).First(&task).Error; err != nil {
		// #region debug-point B:run-backup-task-service-load-task-error
		reportBackupExecDebugFromService(s.C, "B", "backup/services/tasks.go:RunBackupTaskService:load_task_error", "run backup task service failed to load task", map[string]any{
			"task_id": s.ID,
			"error":   err.Error(),
		})
		// #endregion
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("备份任务不存在")
		}
		return nil, err
	}
	if _, cleanupErr := cleanupStaleRunningRunsForTask(task.TaskID); cleanupErr != nil {
		return nil, cleanupErr
	}
	operator := currentUsername(s.C)
	if strings.TrimSpace(operator) == "" {
		operator = "system"
	}
	activeRuns, err := countActiveBackupRuns(task.TaskID)
	if err != nil {
		return nil, err
	}
	if activeRuns > 0 {
		return nil, fmt.Errorf("当前任务仍存在执行中的备份记录，请先等待结束或先收口异常运行记录")
	}
	// 手动触发入口：物理备份任务在此路径下一律执行全量，不进入基线判断流程。
	run, err := createBackupRunForTask(&task, operator, backupTriggerManual)
	if err != nil {
		// #region debug-point B:run-backup-task-service-create-run-error
		reportBackupExecDebugFromService(s.C, "B", "backup/services/tasks.go:RunBackupTaskService:create_run_error", "run backup task service failed to create run", map[string]any{
			"task_id":        task.TaskID,
			"operator":       operator,
			"driver_type":    strings.TrimSpace(string(task.DriverType)),
			"backup_type":    strings.TrimSpace(string(task.BackupType)),
			"executor_node":  task.ExecutorNodeID,
			"storage_target": task.StorageTargetID,
			"error":          err.Error(),
		})
		// #endregion
		return nil, err
	}
	// #region debug-point B:run-backup-task-service-success
	reportBackupExecDebugFromService(s.C, "B", "backup/services/tasks.go:RunBackupTaskService:success", "run backup task service created run", map[string]any{
		"task_id":        task.TaskID,
		"run_id":         run.ID,
		"run_status":     strings.TrimSpace(string(run.Status)),
		"operator":       operator,
		"driver_type":    strings.TrimSpace(string(run.DriverType)),
		"backup_type":    strings.TrimSpace(string(run.BackupType)),
		"executor_node":  run.ExecutorNodeID,
		"storage_target": run.StorageTargetID,
	})
	// #endregion
	return run, nil
}

type StopBackupRunService struct {
	C     *gin.Context
	RunID uint64
}

type StopBackupRunResult struct {
	RunID              uint64 `json:"run_id"`
	Status             string `json:"status"`
	StageLabel         string `json:"stage_label"`
	ProgressStageLabel string `json:"progress_stage_label"`
	StopState          string `json:"stop_state"`
	StopStateLabel     string `json:"stop_state_label"`
	StopStateMessage   string `json:"stop_state_message"`
	StopStateHint      string `json:"stop_state_hint"`
}

func (s *StopBackupRunService) Run() (*StopBackupRunResult, error) {
	var run backupModels.InsightBackupRuns
	if err := global.App.DB.Where("id = ?", s.RunID).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("执行记录不存在")
		}
		return nil, err
	}
	operator := strings.TrimSpace(currentUsername(s.C))
	if operator == "" {
		operator = "system"
	}
	currentLogExcerpt := tailLogExcerpt(run.LogPath)
	progressStage := deriveRunProgressStageLabel(currentLogExcerpt, run.ErrorMessage)
	stageLabel := deriveRunStageLabel(string(run.Status), currentLogExcerpt, run.ErrorMessage)
	executorNodeStatus := ""
	if run.ExecutorNodeID != 0 {
		var node backupModels.InsightBackupNodes
		silentDB := global.App.DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
		if err := silentDB.Select("status").Where("id = ?", run.ExecutorNodeID).Take(&node).Error; err == nil {
			executorNodeStatus = string(node.Status)
		}
	}
	buildResult := func(status string, stopState stopStateDescriptor, stopMessage string) *StopBackupRunResult {
		messageText := strings.TrimSpace(stopMessage)
		if messageText == "" {
			messageText = stopState.Message
		}
		return &StopBackupRunResult{
			RunID:              run.ID,
			Status:             strings.TrimSpace(status),
			StageLabel:         stageLabel,
			ProgressStageLabel: progressStage,
			StopState:          stopState.State,
			StopStateLabel:     stopState.Label,
			StopStateMessage:   messageText,
			StopStateHint:      stopState.Hint,
		}
	}
	switch strings.ToUpper(strings.TrimSpace(string(run.Status))) {
	case "SUCCESS", "FAILED", "STOPPED":
		return nil, fmt.Errorf("当前备份已结束，无需终止")
	case "STOP_REQUESTED":
		stopState := deriveRunStopState(string(run.Status), progressStage, executorNodeStatus, run.UpdatedAt)
		return buildResult(string(run.Status), stopState, ""), nil
	case "PENDING":
		appendBackupRunLog(run.LogPath, "WARN", "收到终止请求，任务尚未执行，已直接终止，操作人=%s", operator)
		if err := global.App.DB.Model(&backupModels.InsightBackupRuns{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status":        "STOPPED",
			"end_at":        commonModels.LocalTime(nowLocalTime()),
			"error_message": fmt.Sprintf("用户手动终止备份，操作人=%s", operator),
			"log_excerpt":   tailLogExcerpt(run.LogPath),
		}).Error; err != nil {
			return nil, err
		}
		return buildResult("STOPPED", stopStateDescriptor{
			State:   "IMMEDIATE",
			Label:   "可立即终止",
			Message: "当前任务尚未开始执行，已直接终止。",
			Hint:    "本次不会进入备份节点，也不会生成备份产物。",
		}, ""), nil
	case "RUNNING":
		appendBackupRunLog(run.LogPath, "WARN", "收到终止请求，正在终止当前备份进程，操作人=%s", operator)
		if err := global.App.DB.Model(&backupModels.InsightBackupRuns{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status":        "STOP_REQUESTED",
			"error_message": fmt.Sprintf("用户手动终止中，操作人=%s", operator),
			"log_excerpt":   tailLogExcerpt(run.LogPath),
		}).Error; err != nil {
			return nil, err
		}
		stopState := deriveRunStopState("STOP_REQUESTED", progressStage, executorNodeStatus, commonModels.LocalTime(nowLocalTime()))
		return buildResult("STOP_REQUESTED", stopState, ""), nil
	default:
		return nil, fmt.Errorf("当前备份状态不支持终止")
	}
}

func writeRunFailed(runID uint64, logPath string, err error) {
	appendBackupRunLog(logPath, "ERROR", "备份执行失败: %v", err)
	_ = global.App.DB.Model(&backupModels.InsightBackupRuns{}).Where("id = ?", runID).Updates(map[string]any{
		"status":        "FAILED",
		"end_at":        commonModels.LocalTime(nowLocalTime()),
		"error_message": err.Error(),
		"log_path":      logPath,
		"log_excerpt":   tailLogExcerpt(logPath),
	}).Error
}

func writeRunStopped(runID uint64, logPath string, reason string) {
	appendBackupRunLog(logPath, "WARN", "备份执行已终止: %s", strings.TrimSpace(reason))
	_ = global.App.DB.Model(&backupModels.InsightBackupRuns{}).Where("id = ?", runID).Updates(map[string]any{
		"status":        "STOPPED",
		"end_at":        commonModels.LocalTime(nowLocalTime()),
		"error_message": strings.TrimSpace(reason),
		"log_path":      logPath,
		"log_excerpt":   tailLogExcerpt(logPath),
	}).Error
}

func startLocalBackupRunProgressLoop(runID uint64, logPath string) func() {
	done := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	update := func() {
		updates := map[string]any{
			"updated_at": nowLocalTime(),
		}
		if tail := tailLogExcerpt(logPath); strings.TrimSpace(tail) != "" {
			updates["log_excerpt"] = tail
		}
		if size := liveBackupRunSize("", logPath, 0, "", 0); size > 0 {
			updates["size_bytes"] = size
		}
		_ = global.App.DB.Model(&backupModels.InsightBackupRuns{}).
			Where("id = ? AND status = ?", runID, "RUNNING").
			Updates(updates).Error
	}
	update()
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				update()
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
		})
	}
}

func finalizeBackupRunSuccess(task *backupModels.InsightBackupTasks, run *backupModels.InsightBackupRuns, result *BackupExecutionResult) error {
	runStorageTargetID := run.StorageTargetID
	if result.StorageTargetID > 0 {
		runStorageTargetID = result.StorageTargetID
		run.StorageTargetID = result.StorageTargetID
	}
	set, err := persistBackupSetSuccess(task, run, result)
	if err != nil {
		appendBackupRunLog(result.LogPath, "ERROR", "写入备份集记录失败: %v", err)
		return err
	}
	appendBackupRunLog(result.LogPath, "INFO", "备份执行成功，写入备份集成功 set=%s", set.Name)
	err = global.App.DB.Model(&backupModels.InsightBackupRuns{}).Where("id = ?", run.ID).Updates(map[string]any{
		"status":                "SUCCESS",
		"end_at":                commonModels.LocalTime(nowLocalTime()),
		"executor_node_id":      run.ExecutorNodeID,
		"repository_node_id":    run.RepositoryNodeID,
		"object_mode":           result.ObjectMode,
		"object_spec":           result.ObjectSpec,
		"object_summary":        result.ObjectSummary,
		"output_dir":            result.OutputDir,
		"driver_type":           run.DriverType,
		"driver_config":         run.DriverConfig,
		"storage_backend":       run.StorageBackend,
		"storage_config":        run.StorageConfig,
		"storage_target_id":     runStorageTargetID,
		"storage_artifact_path": result.StorageArtifactPath,
		"tool":                  result.Tool,
		"tool_image":            result.ToolImage,
		"server_version":        result.ServerVersion,
		"file_name":             result.ArtifactName,
		"file_path":             result.ArtifactPath,
		"size_bytes":            result.SizeBytes,
		"sha256":                result.Sha256,
		"log_path":              result.LogPath,
		"log_excerpt":           tailLogExcerpt(result.LogPath),
	}).Error
	return err
}

func normalizeEnumOrDefault(v string, def string) string {
	text := strings.ToUpper(strings.TrimSpace(v))
	if text == "" {
		return def
	}
	return text
}

func marshalJSONMapOrNil(m map[string]any) (datatypes.JSON, error) {
	if m == nil {
		return nil, nil
	}
	buf, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 || string(buf) == "null" {
		return nil, nil
	}
	return datatypes.JSON(buf), nil
}

func validatePGBackRestTopology(instance *commonModels.InsightInstances, executorNode *backupModels.InsightBackupNodes, repositoryNode *backupModels.InsightBackupNodes) error {
	if instance == nil || executorNode == nil || repositoryNode == nil {
		return fmt.Errorf("pgBackRest 拓扑校验失败")
	}
	instanceHost := strings.ToLower(strings.TrimSpace(instance.Hostname))
	nodeHost := strings.ToLower(strings.TrimSpace(executorNode.Host))
	if instanceHost == "" || nodeHost == "" || instanceHost != nodeHost {
		return fmt.Errorf("pgBackRest 物理备份要求执行节点与 PostgreSQL 实例主机一致")
	}
	if strings.TrimSpace(repositoryNode.Host) == "" {
		return fmt.Errorf("pgBackRest 仓库节点地址不能为空")
	}
	return nil
}
