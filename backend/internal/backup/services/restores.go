package services

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/pagination"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type GetRestoreRunsService struct {
	*backupForms.GetRestoreRunsForm
	C *gin.Context
}

func (s *GetRestoreRunsService) Run() (data interface{}, total int64, err error) {
	// 列表是最高频的读路径，借此收口失联的 STOP_REQUESTED 任务，
	// 避免其永久卡在「终止不了 / 删不掉 / 不能重跑」的死状态。
	if _, cleanupErr := CleanupStaleRestoreStopRequestedRuns(); cleanupErr != nil {
		global.App.Log.Warnf("清理失联的终止中恢复任务失败: %v", cleanupErr)
	}
	type row struct {
		ID                uint64                 `json:"id"`
		TaskID            string                 `json:"task_id"`
		Name              string                 `json:"name"`
		BackupSetID       uint64                 `json:"backup_set_id"`
		DBType            string                 `json:"db_type"`
		DriverType        string                 `json:"driver_type"`
		ObjectMode        string                 `json:"object_mode"`
		SourceInstanceID  uint64                 `json:"source_instance_id"`
		TargetInstanceID  uint64                 `json:"target_instance_id"`
		TargetDBName      string                 `json:"target_db_name"`
		TargetDisplay     string                 `json:"target_display"`
		Status            string                 `json:"status"`
		StartAt           commonModels.LocalTime `json:"start_at"`
		EndAt             commonModels.LocalTime `json:"end_at"`
		UpdatedAt         commonModels.LocalTime `json:"updated_at"`
		LastReportAt      commonModels.LocalTime `json:"last_report_at"`
		CurrentStage      string                 `json:"current_stage"`
		CurrentStageLabel string                 `json:"current_stage_label"`
		ProgressPercent   int                    `json:"progress_percent"`
		CurrentObject     string                 `json:"current_object"`
		LogPath           string                 `json:"log_path"`
		LogExcerpt        string                 `json:"log_excerpt"`
		ErrorMessage      string                 `json:"error_message"`
		CreatedAt         commonModels.LocalTime `json:"created_at"`
		SetName           string                 `json:"set_name"`
		SetFileName       string                 `json:"set_file_name"`
		SetCreatedAt      commonModels.LocalTime `json:"set_created_at"`
		BackupType        string                 `json:"backup_type"`
		ObjectSummary     string                 `json:"object_summary"`
		ExecutionMode     string                 `json:"execution_mode"`
		Options           datatypes.JSON         `json:"options"`
		SourceHost        string                 `json:"source_host"`
		SourcePort        int                    `json:"source_port"`
		SourceRemark      string                 `json:"source_remark"`
		TargetHost        string                 `json:"target_host"`
		TargetPort        int                    `json:"target_port"`
		TargetRemark      string                 `json:"target_remark"`
		CreatedBy         string                 `json:"created_by"`
		DurationSeconds   int64                  `json:"duration_seconds"`
	}
	var list []row
	tx := global.App.DB.Table("insight_restore_runs rr").
		Select(`rr.id, rr.task_id, rr.name, rr.backup_set_id, rr.db_type, rr.target_instance_id, rr.target_db_name, rr.status, rr.start_at, rr.end_at, rr.updated_at,
			rr.last_report_at, rr.current_stage, rr.progress_percent, rr.current_object, rr.log_path, rr.log_excerpt, rr.error_message, rr.created_at,
			rr.created_by, rr.source_instance_id, rr.driver_type, rr.options,
			bs.name as set_name, bs.file_name as set_file_name, bs.created_at as set_created_at, bs.backup_type as backup_type,
			bs.object_summary as object_summary, bs.object_mode as object_mode,
			si.hostname as source_host, si.port as source_port, si.remark as source_remark,
			i.hostname as target_host, i.port as target_port, i.remark as target_remark`).
		Joins("left join insight_backup_sets bs on bs.id = rr.backup_set_id").
		Joins("left join insight_instances si on si.id = rr.source_instance_id").
		Joins("left join insight_instances i on i.id = rr.target_instance_id").
		Where("COALESCE(rr.restore_kind, 'LOGICAL') <> ?", restoreKindPhysical).
		Order("rr.id desc")
	if s.Search != "" {
		text := "%" + s.Search + "%"
		tx = tx.Where(`rr.task_id like ? or rr.name like ? or cast(rr.id as char) like ? or rr.target_db_name like ? or bs.name like ? or
			si.hostname like ? or si.remark like ? or i.hostname like ? or i.remark like ?`,
			text, text, text, text, text, text, text, text, text)
	}
	if s.DBType != "" {
		tx = tx.Where("rr.db_type = ?", s.DBType)
	}
	if s.TargetInstanceID != 0 {
		tx = tx.Where("rr.target_instance_id = ?", s.TargetInstanceID)
	}
	if strings.TrimSpace(s.Status) != "" {
		switch strings.ToUpper(strings.TrimSpace(s.Status)) {
		case "RUNNING":
			tx = tx.Where("rr.status IN ?", []string{"PENDING", "RUNNING", "STOP_REQUESTED"})
		default:
			tx = tx.Where("rr.status = ?", strings.TrimSpace(s.Status))
		}
	}
	if startAt, parseErr := parseRestoreFilterTime(s.StartAtFrom, false); parseErr == nil && !startAt.IsZero() {
		tx = tx.Where("rr.created_at >= ?", startAt)
	}
	if endAt, parseErr := parseRestoreFilterTime(s.StartAtTo, true); parseErr == nil && !endAt.IsZero() {
		tx = tx.Where("rr.created_at <= ?", endAt)
	}
	total = pagination.Pager(&s.PaginationQ, tx, &list)
	for i := range list {
		list[i].TaskID = restoreTaskIDOrFallback(list[i].TaskID, list[i].ID)
		list[i].Name = normalizeRestoreRunName(list[i].Name, list[i].SetName, list[i].TargetDBName)
		list[i].TargetDisplay = deriveRestoreTargetDisplay(list[i].DBType, list[i].ObjectMode, list[i].TargetDBName)
		list[i].CurrentStageLabel = restoreStageLabel(list[i].CurrentStage)
		list[i].ProgressPercent = clampPercent(list[i].ProgressPercent)
		list[i].ExecutionMode = extractLogicalRestoreExecutionMode(list[i].Options)
		if strings.EqualFold(strings.TrimSpace(list[i].Status), "SUCCESS") {
			list[i].ProgressPercent = 100
		}
		list[i].DurationSeconds = calcRestoreDurationSeconds(list[i].StartAt, list[i].EndAt, list[i].Status)
		if strings.TrimSpace(list[i].LogExcerpt) == "" {
			list[i].LogExcerpt = tailLogExcerpt(list[i].LogPath)
		}
	}
	return &list, total, nil
}

func extractLogicalRestoreExecutionMode(raw datatypes.JSON) string {
	if len(raw) == 0 {
		return ""
	}
	var options struct {
		ExecutionMode string `json:"execution_mode"`
	}
	if err := json.Unmarshal(raw, &options); err != nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(options.ExecutionMode))
}

func generateRestoreTaskID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err == nil {
		return "rs-" + strings.ToLower(hex.EncodeToString(buf))
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("restore-%d", time.Now().UnixNano())))
	return "rs-" + strings.ToLower(hex.EncodeToString(sum[:6]))
}

func restoreTaskIDOrFallback(taskID string, numericID uint64) string {
	text := strings.TrimSpace(taskID)
	if len(text) == 15 && strings.HasPrefix(strings.ToLower(text), "rs-") {
		return strings.ToLower(text)
	}
	if text != "" {
		return strings.ToLower(text)
	}
	return generateRestoreTaskID()
}

func normalizeRestoreRunName(name string, setName string, targetDBName string) string {
	text := strings.TrimSpace(name)
	if text != "" {
		return text
	}
	if strings.TrimSpace(setName) != "" {
		return strings.TrimSpace(setName)
	}
	if strings.TrimSpace(targetDBName) != "" {
		return strings.TrimSpace(targetDBName)
	}
	return "恢复任务"
}

func validateRestoreRunInput(form *backupForms.CreateRestoreRunForm) (*backupModels.InsightBackupSets, *commonModels.InsightInstances, error) {
	set, err := LoadBackupSet(form.BackupSetID)
	if err != nil {
		return nil, nil, err
	}
	if isPostgresPhysicalDriverType(string(set.DriverType)) {
		return nil, nil, fmt.Errorf("当前接口仅支持 MySQL / PostgreSQL 逻辑备份恢复")
	}
	targetInstance, err := loadInstanceByID(form.TargetInstanceID)
	if err != nil {
		return nil, nil, err
	}
	// 同家族内放行（Kingbase ↔ KingbaseES / PostgreSQL ↔ Kingbase 的写法与协议差异），
	// 跨家族（如 MySQL 备份恢复到金仓）仍然拒绝。
	if !sameLogicalFamily(string(set.DBType), string(targetInstance.DbType)) {
		return nil, nil, fmt.Errorf("目标实例类型与备份集类型不一致")
	}
	return set, targetInstance, nil
}

type CreateRestoreRunService struct {
	*backupForms.CreateRestoreRunForm
	C *gin.Context
}

func (s *CreateRestoreRunService) Run() (*backupModels.InsightRestoreRuns, error) {
	// 恢复 = 读源备份集（export）+ 写目标实例（dml），两个方向都要有授权。
	// 此前这里完全没有数据授权校验，等于绕开授权把整份数据写进任意实例。
	if err := CheckRestoreAccess(currentUsername(s.C), s.BackupSetID, s.TargetInstanceID,
		firstNonEmpty(s.TargetDBName, s.TargetDBSuffix)); err != nil {
		return nil, err
	}
	plan, err := prepareLogicalRestorePlan(buildLogicalRestorePlanInputFromCreateForm(s.CreateRestoreRunForm), s.C)
	if err != nil {
		return nil, err
	}

	restore := backupModels.InsightRestoreRuns{
		Model:            &commonModels.Model{},
		TaskID:           generateRestoreTaskID(),
		Name:             normalizeRestoreRunName(s.Name, plan.Set.Name, firstNonEmpty(plan.Options.TargetDBName, plan.Options.TargetDBSuffix)),
		BackupSetID:      plan.Set.ID,
		SourceInstanceID: plan.Set.SourceInstanceID,
		DBType:           plan.Set.DBType,
		RestoreKind:      restoreKindLogical,
		DriverType:       plan.Set.DriverType,
		RestoreMode:      commonModels.EnumType(plan.Options.RestoreMode),
		TargetLevel:      restoreTargetLevelDatabase,
		TargetInstanceID: s.TargetInstanceID,
		TargetName:       firstNonEmpty(plan.Options.TargetDBName, plan.Options.TargetDBSuffix),
		TargetDBName:     firstNonEmpty(plan.Options.TargetDBName, plan.Options.TargetDBSuffix),
		ResolvedContext:  plan.ResolvedJSON,
		Options:          plan.OptionsJSON,
		Status:           commonModels.EnumType(plan.Status),
		CurrentStage:     plan.Stage,
		ProgressPercent:  0,
		CurrentAction:    plan.Action,
		RiskLevel:        plan.RiskLevel,
		PrecheckSummary:  plan.PrecheckJSON,
		ErrorMessage:     plan.ErrorMessage,
		CreatedBy:        currentUsername(s.C),
	}
	if err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&restore).Error; err != nil {
			return err
		}
		for _, item := range plan.Items {
			item.RestoreRunID = restore.ID
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if plan.AutoExecute {
		go executeRestoreRun(restore.ID)
	}
	return &restore, nil
}

func executeRestoreRun(restoreRunID uint64) {
	var rr backupModels.InsightRestoreRuns
	if err := global.App.DB.Where("id = ?", restoreRunID).First(&rr).Error; err != nil {
		return
	}
	switch strings.ToUpper(strings.TrimSpace(string(rr.Status))) {
	case "STOPPED":
		return
	case "STOP_REQUESTED":
		writeRestoreStopped(rr.ID, "", resolveRestoreStopReason(rr.CreatedBy))
		return
	}
	start := nowLocalTime()
	global.App.DB.Model(&backupModels.InsightRestoreRuns{}).Where("id = ?", rr.ID).Updates(map[string]any{
		"status":           "RUNNING",
		"start_at":         commonModels.LocalTime(start),
		"last_report_at":   commonModels.LocalTime(start),
		"current_stage":    restoreStagePrecheck,
		"progress_percent": 3,
	})

	if _, err := LoadBackupSet(rr.BackupSetID); err != nil {
		writeRestoreFailed(rr.ID, err)
		return
	}
	if _, err := loadInstanceByID(rr.TargetInstanceID); err != nil {
		writeRestoreFailed(rr.ID, err)
		return
	}
	baseDir, err := absMediaBackupDir()
	if err != nil {
		writeRestoreFailed(rr.ID, err)
		return
	}
	logDir := filepath.Join(baseDir, "restore", string(rr.DBType), fmt.Sprintf("%d", rr.TargetInstanceID), rr.TargetDBName, fmt.Sprintf("%d", rr.ID))
	if err := ensureDir(logDir); err != nil {
		writeRestoreFailed(rr.ID, err)
		return
	}
	logPath := filepath.Join(logDir, "run.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		writeRestoreFailed(rr.ID, err)
		return
	}
	defer logFile.Close()
	updateRestoreRunProgress(rr.ID, restoreRunProgressPatch{
		Status:          "RUNNING",
		CurrentStage:    restoreStagePrecheck,
		ProgressPercent: 5,
		LogPath:         logPath,
	})
	baseCtx, runControl := attachRestoreRunControl(context.Background(), rr.ID, logPath)
	defer runControl.Close()

	if err := executeLogicalRestoreRun(baseCtx, rr, logDir, logPath, logFile, runControl); err != nil {
		finalizeRestoreRunError(rr.ID, logPath, err, runControl)
	}
}

type postgresRestoreSource struct {
	SourceDBName string
	EntryName    string
	IsDir        bool
}

func deriveRestoreTargetDisplay(dbType string, objectMode string, targetDBName string) string {
	if strings.EqualFold(strings.TrimSpace(dbType), "MySQL") && strings.EqualFold(strings.TrimSpace(objectMode), "ALL") {
		suffix := strings.TrimSpace(targetDBName)
		if suffix == "" {
			return "全部实例（同名恢复）"
		}
		return fmt.Sprintf("全部实例（后缀：%s）", suffix)
	}
	if isPostgresLogicalFamily(dbType) && strings.EqualFold(strings.TrimSpace(objectMode), "ALL") {
		suffix := strings.TrimSpace(targetDBName)
		if suffix == "" {
			return "全部库（同名恢复）"
		}
		return fmt.Sprintf("全部库（后缀：%s）", suffix)
	}
	if strings.TrimSpace(targetDBName) == "" {
		return "-"
	}
	return targetDBName
}

func parseRestoreFilterTime(value string, endOfDay bool) (time.Time, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return time.Time{}, nil
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02",
		time.RFC3339,
	}
	var parsed time.Time
	var err error
	for _, layout := range layouts {
		parsed, err = time.ParseInLocation(layout, text, time.Local)
		if err == nil {
			if layout == "2006-01-02" && endOfDay {
				parsed = parsed.Add(24*time.Hour - time.Second)
			}
			return parsed, nil
		}
	}
	return time.Time{}, err
}

func isPostgresLogicalMultiRestoreSet(set *backupModels.InsightBackupSets) bool {
	if !isPostgresLogicalFamily(string(set.DBType)) ||
		!strings.EqualFold(strings.TrimSpace(string(set.DriverType)), "LOGICAL") {
		return false
	}
	if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(set.FileName)), ".zip") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(string(set.ObjectMode)), "ALL") || strings.TrimSpace(set.SourceDBName) == ""
}

func validatePostgresLogicalMultiRestoreCreate(set *backupModels.InsightBackupSets, targetInstance *commonModels.InsightInstances, suffix string) error {
	setFile, err := ValidateBackupSetFile(set)
	if err != nil {
		return err
	}
	sources, err := inspectPostgresLogicalRestoreSources(setFile)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return fmt.Errorf("未在备份集中识别到可恢复的 PostgreSQL 数据库")
	}
	targetNames := make([]string, 0, len(sources))
	seenNames := map[string]struct{}{}
	for _, item := range sources {
		targetName, buildErr := buildPostgresRestoreTargetName(item.SourceDBName, suffix)
		if buildErr != nil {
			return buildErr
		}
		if _, ok := seenNames[targetName]; ok {
			return fmt.Errorf("目标库名冲突：%s", targetName)
		}
		seenNames[targetName] = struct{}{}
		targetNames = append(targetNames, targetName)
	}
	extra := parseExtraConfig(targetInstance.ExtraConfig)
	sslmode := extra["sslmode"]
	existing, err := listPostgresDatabases(targetInstance.Hostname, targetInstance.Port, targetInstance.User, targetInstance.Password, extra["database"], sslmode)
	if err != nil {
		return err
	}
	existingSet := map[string]struct{}{}
	for _, item := range existing {
		existingSet[strings.TrimSpace(item)] = struct{}{}
	}
	conflicts := make([]string, 0, len(targetNames))
	for _, item := range targetNames {
		if _, ok := existingSet[item]; ok {
			conflicts = append(conflicts, item)
		}
	}
	if len(conflicts) > 0 {
		if len(conflicts) > 5 {
			return fmt.Errorf("目标实例中已存在部分目标库，请调整后缀或更换目标实例：%s 等 %d 个库", strings.Join(conflicts[:5], "、"), len(conflicts))
		}
		return fmt.Errorf("目标实例中已存在目标库，请调整后缀或更换目标实例：%s", strings.Join(conflicts, "、"))
	}
	return nil
}

func inspectPostgresLogicalRestoreSources(zipPath string) ([]postgresRestoreSource, error) {
	if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(zipPath)), ".zip") {
		return nil, fmt.Errorf("PostgreSQL 逻辑备份恢复当前要求备份集为 zip 归档")
	}
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	entries := map[string]postgresRestoreSource{}
	for _, file := range r.File {
		name := filepath.ToSlash(strings.TrimSpace(file.Name))
		if name == "" {
			continue
		}
		parts := strings.Split(name, "/")
		topLevel := strings.TrimSpace(parts[0])
		if topLevel == "" {
			continue
		}
		entry := entries[topLevel]
		entry.EntryName = topLevel
		entry.SourceDBName = strings.TrimSuffix(topLevel, ".dump")
		if len(parts) > 1 {
			entry.IsDir = true
		}
		entries[topLevel] = entry
	}
	list := make([]postgresRestoreSource, 0, len(entries))
	for _, item := range entries {
		if err := validateIdent(item.SourceDBName, "源库名"); err != nil {
			return nil, fmt.Errorf("备份集内存在非法 PostgreSQL 备份条目 %q: %w", item.EntryName, err)
		}
		list = append(list, item)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].SourceDBName < list[j].SourceDBName
	})
	return list, nil
}

func buildPostgresRestoreTargetName(sourceDB string, suffix string) (string, error) {
	targetName := strings.TrimSpace(sourceDB) + strings.TrimSpace(suffix)
	if err := validateIdent(targetName, "目标库名"); err != nil {
		return "", err
	}
	return targetName, nil
}

func executePostgresLogicalMultiRestore(parent context.Context, rr backupModels.InsightRestoreRuns, set *backupModels.InsightBackupSets, setFile string, targetInstance *commonModels.InsightInstances, logDir string, logPath string, logFile io.Writer) error {
	updateRestoreRunProgress(rr.ID, restoreRunProgressPatch{
		Status:          "RUNNING",
		CurrentStage:    restoreStagePrecheck,
		ProgressPercent: 8,
		LogPath:         logPath,
	})
	extra := parseExtraConfig(targetInstance.ExtraConfig)
	sslmode := extra["sslmode"]
	serverVersion, err := probePostgresVersion(targetInstance.Hostname, targetInstance.Port, targetInstance.User, targetInstance.Password, extra["database"], sslmode)
	if err != nil {
		return err
	}
	psqlTool, err := resolvePostgresClientTool("psql", serverVersion)
	if err != nil {
		return err
	}
	pgRestoreTool, err := resolvePostgresClientTool("pg_restore", serverVersion)
	if err != nil {
		return err
	}
	sources, err := inspectPostgresLogicalRestoreSources(setFile)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return fmt.Errorf("未在备份集中识别到可恢复的 PostgreSQL 数据库")
	}
	items := make([]backupModels.InsightRestoreRunItems, 0, len(sources))
	itemBySource := make(map[string]*backupModels.InsightRestoreRunItems, len(sources))
	for idx, source := range sources {
		targetDB, buildErr := buildPostgresRestoreTargetName(source.SourceDBName, rr.TargetDBName)
		if buildErr != nil {
			return buildErr
		}
		item := backupModels.InsightRestoreRunItems{
			Model:            &commonModels.Model{},
			RestoreRunID:     rr.ID,
			SourceObjectName: source.SourceDBName,
			TargetObjectName: targetDB,
			Status:           "PENDING",
			CurrentStage:     restoreStagePending,
			DisplayOrder:     idx + 1,
		}
		if err := createRestoreRunItem(&item); err != nil {
			return err
		}
		items = append(items, item)
		itemBySource[source.SourceDBName] = &items[len(items)-1]
	}
	extractDir := filepath.Join(logDir, "restore-src")
	updateRestoreRunProgress(rr.ID, restoreRunProgressPatch{
		Status:          "RUNNING",
		CurrentStage:    restoreStagePrepareSource,
		ProgressPercent: 18,
		LogPath:         logPath,
	})
	if _, unzipErr := unzipFile(setFile, extractDir); unzipErr != nil {
		return unzipErr
	}
	env := map[string]string{"PGPASSWORD": targetInstance.Password}
	if sslmode != "" {
		env["PGSSLMODE"] = sslmode
	}
	ctx, cancel := context.WithTimeout(parent, 24*time.Hour)
	defer cancel()

	concurrency := recommendedPostgresMultiRestoreConcurrency(len(sources))
	perDatabaseWorkers := recommendedPostgresRestoreWorkers()
	if concurrency > 1 && perDatabaseWorkers > 1 {
		perDatabaseWorkers = maxInt(1, perDatabaseWorkers/concurrency)
	}
	var logMu sync.Mutex
	logf := func(format string, args ...any) {
		logMu.Lock()
		defer logMu.Unlock()
		fmt.Fprintf(logFile, format+"\n", args...)
	}
	logf("[plan] 备份集=%s 库数量=%d 并发库数=%d 每库最大并发=%d", set.FileName, len(sources), concurrency, perDatabaseWorkers)
	logf("[tool] %s (%s)", psqlTool.Path, psqlTool.Source)
	logf("[tool] %s (%s)", pgRestoreTool.Path, pgRestoreTool.Source)

	jobs := make(chan postgresRestoreSource)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	reportErr := func(err error) {
		errOnce.Do(func() {
			firstErr = err
			cancel()
		})
	}
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if ctx.Err() != nil {
					return
				}
				targetDB, buildErr := buildPostgresRestoreTargetName(item.SourceDBName, rr.TargetDBName)
				if buildErr != nil {
					reportErr(buildErr)
					return
				}
				restoreItem := itemBySource[item.SourceDBName]
				dbLogPath := filepath.Join(logDir, fmt.Sprintf("%s.log", sanitizeRestoreLogName(targetDB)))
				dbLogFile, openErr := os.OpenFile(dbLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
				if openErr != nil {
					reportErr(openErr)
					return
				}
				restoreSource := filepath.Join(extractDir, item.EntryName)
				logf("[db:%s] 开始恢复 -> %s，日志=%s", item.SourceDBName, targetDB, dbLogPath)
				if restoreItem != nil {
					updateRestoreRunItem(restoreItem.ID, restoreItemPatch{
						Status:          "RUNNING",
						CurrentStage:    restoreStagePrepareTarget,
						ProgressPercent: 15,
						LogPath:         dbLogPath,
						Started:         true,
					})
				}
				refreshRestoreRunFromItems(rr.ID, restoreStagePrepareTarget, logPath)
				createArgs := []string{
					"-h", targetInstance.Hostname,
					"-p", fmt.Sprintf("%d", targetInstance.Port),
					"-U", targetInstance.User,
					"-d", "postgres",
					"-c", fmt.Sprintf("CREATE DATABASE \"%s\";", targetDB),
				}
				if err := runLocalCommand(ctx, "", psqlTool.Path, createArgs, withLibraryPath(env, psqlTool.LibDirs), dbLogFile); err != nil {
					dbLogFile.Close()
					if restoreItem != nil {
						updateRestoreRunItem(restoreItem.ID, restoreItemPatch{
							Status:          "FAILED",
							CurrentStage:    restoreStagePrepareTarget,
							ProgressPercent: 15,
							LogPath:         dbLogPath,
							ErrorMessage:    err.Error(),
							Finished:        true,
						})
					}
					refreshRestoreRunFromItems(rr.ID, restoreStagePrepareTarget, logPath)
					reportErr(fmt.Errorf("创建目标库 %s 失败: %w", targetDB, err))
					return
				}
				restoreArgs := []string{
					"-h", targetInstance.Hostname,
					"-p", fmt.Sprintf("%d", targetInstance.Port),
					"-U", targetInstance.User,
					"-d", targetDB,
					"--no-owner",
					"--no-acl",
				}
				if item.IsDir {
					restoreArgs = append(restoreArgs, "-Fd", "-j", fmt.Sprintf("%d", perDatabaseWorkers))
				}
				restoreArgs = append(restoreArgs, restoreSource)
				if restoreItem != nil {
					updateRestoreRunItem(restoreItem.ID, restoreItemPatch{
						Status:          "RUNNING",
						CurrentStage:    restoreStageRestoring,
						ProgressPercent: 55,
						LogPath:         dbLogPath,
					})
				}
				refreshRestoreRunFromItems(rr.ID, restoreStageRestoring, logPath)
				if err := runLocalCommand(ctx, "", pgRestoreTool.Path, restoreArgs, withLibraryPath(env, pgRestoreTool.LibDirs), dbLogFile); err != nil {
					dbLogFile.Close()
					if restoreItem != nil {
						updateRestoreRunItem(restoreItem.ID, restoreItemPatch{
							Status:          "FAILED",
							CurrentStage:    restoreStageRestoring,
							ProgressPercent: 55,
							LogPath:         dbLogPath,
							ErrorMessage:    err.Error(),
							Finished:        true,
						})
					}
					refreshRestoreRunFromItems(rr.ID, restoreStageRestoring, logPath)
					reportErr(fmt.Errorf("恢复数据库 %s -> %s 失败: %w", item.SourceDBName, targetDB, err))
					return
				}
				dbLogFile.Close()
				if restoreItem != nil {
					updateRestoreRunItem(restoreItem.ID, restoreItemPatch{
						Status:          "SUCCESS",
						CurrentStage:    restoreStageFinished,
						ProgressPercent: 100,
						LogPath:         dbLogPath,
						Finished:        true,
					})
				}
				refreshRestoreRunFromItems(rr.ID, restoreStageRestoring, logPath)
				logf("[db:%s] 恢复完成 -> %s", item.SourceDBName, targetDB)
			}
		}()
	}
enqueueLoop:
	for _, item := range sources {
		select {
		case <-ctx.Done():
			break enqueueLoop
		case jobs <- item:
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil && err != context.Canceled {
		return err
	}
	updateRestoreRunProgress(rr.ID, restoreRunProgressPatch{
		Status:          "RUNNING",
		CurrentStage:    restoreStageVerify,
		ProgressPercent: 95,
		LogPath:         logPath,
	})
	logf("[done] PostgreSQL 全实例逻辑恢复完成，总库数=%d，目标后缀=%s", len(sources), displayOrDefault(rr.TargetDBName, "(同名恢复)"))
	_ = logPath
	return nil
}

func recommendedPostgresMultiRestoreConcurrency(databaseCount int) int {
	if databaseCount <= 1 {
		return 1
	}
	cpuLimit := runtime.NumCPU() / 4
	if cpuLimit < 1 {
		cpuLimit = 1
	}
	if cpuLimit > 2 {
		cpuLimit = 2
	}
	if databaseCount < cpuLimit {
		return databaseCount
	}
	return cpuLimit
}

func sanitizeRestoreLogName(name string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", " ", "_")
	return replacer.Replace(strings.TrimSpace(name))
}

func writeRestoreFailed(id uint64, err error) {
	updateRestoreRunProgress(id, restoreRunProgressPatch{
		Status:       "FAILED",
		CurrentStage: restoreStageFinalize,
		ErrorMessage: err.Error(),
		Finished:     true,
	})
}

func writeRestoreFailedWithLog(id uint64, logPath string, err error) {
	updateRestoreRunProgress(id, restoreRunProgressPatch{
		Status:       "FAILED",
		CurrentStage: restoreStageFinalize,
		LogPath:      logPath,
		ErrorMessage: err.Error(),
		Finished:     true,
	})
}

func recommendedPostgresRestoreWorkers() int {
	workers := 2
	if cpu := runtime.NumCPU() / 2; cpu > workers {
		workers = cpu
	}
	if workers > 4 {
		workers = 4
	}
	if workers < 1 {
		return 1
	}
	return workers
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
