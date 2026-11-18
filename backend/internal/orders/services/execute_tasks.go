package services

// 执行链路不做数据授权校验（2026-10-23 起）：
// 变更(DML/DDL)与导出(EXPORT)的审批对象就是工单本身的内容，工单「提交 → 审批 → 执行」
// 已经是这两类操作的全部审批；再要求一份按库/表申请的常驻数据授权，等于同一件事审两遍，
// 而且常驻授权在有效期内可执行任意变更，比工单审批更宽。
// 数据访问授权只约束查询接口（见 das/services/access_action_scope.go）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/lock"
	"github.com/simajiong/jaysql/pkg/secret"
	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/orders/api/base"
	"github.com/simajiong/jaysql/internal/orders/api/execute"
	"github.com/simajiong/jaysql/internal/orders/api/mysql"
	"github.com/simajiong/jaysql/internal/orders/forms"
	ordersModels "github.com/simajiong/jaysql/internal/orders/models"

	dasServices "github.com/simajiong/jaysql/internal/das/services"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ------------------------------------------------------------------
// 执行计划（执行方式 + 错误处理 + 备份失败处理）
//
// 这三个选项此前只停留在表单层：forms 做了 binding 校验、前端也正确传参，
// 但服务层从未读取，buildExecutionConfig() 也从未被调用（死代码），
// 于是「立即/定时/手动」三种选择走的都是同一条同步执行路径 —— 表现为「选了没效果」。
// 这里统一收敛为 ExecutionPolicies，并落到工单记录：
//   execution_mode / scheduled_at → 独立列（定时调度器需要按索引扫描到期工单）
//   execution_config(JSON)        → 四个字段的完整快照（供审计与定时触发时回读策略）
// ------------------------------------------------------------------

const (
	// 执行方式
	ExecutionModeImmediate = "IMMEDIATE" // 立即执行：提交即同步执行（历史行为）
	ExecutionModeScheduled = "SCHEDULED" // 定时执行：登记计划，由调度器到期触发
	ExecutionModeManual    = "MANUAL"    // 手动执行：只登记模式，由「单条执行」逐条触发

	// 错误处理策略：执行出错后的行为
	ErrorPolicyStop     = "STOP"     // 终止任务（默认）：不再执行后续任务
	ErrorPolicyContinue = "CONTINUE" // 继续任务：跳过失败任务继续执行后续任务
	ErrorPolicyRollback = "ROLLBACK" // 回滚任务：回滚本次已执行成功的任务后终止

	// 备份失败处理策略：回滚 SQL（备份依据）生成失败后的行为
	BackupPolicyContinue = "CONTINUE" // 继续任务
	BackupPolicyStop     = "STOP"     // 停止任务（默认）

	orderTimeLayout = "2006-01-02 15:04:05"
)

// 可重试的「工单正忙」状态：定时调度器据此决定是否写回定时计划、下一轮重试。
// 与「状态不允许执行」这类终态错误区分开——后者重试多少次都不会成功。
var (
	ErrOrderBusy          = errors.New("工单正在执行中，请稍后再试")
	ErrOrderTaskExecuting = errors.New("当前有任务正在执行中，请先等待执行完成")
)

// ExecutionPolicies 一次执行的完整策略集合
type ExecutionPolicies struct {
	ErrorPolicy   string
	BackupPolicy  string
	ExecutionMode string
	ScheduledAt   string
}

// normalizeOneOf 把外部传入值收敛到白名单内，非法/空值一律回落到默认值。
// 表单 binding 已做一层校验，这里是服务层兜底（调度器、内部调用不经过表单）。
func normalizeOneOf(value, fallback string, allowed ...string) string {
	v := strings.ToUpper(strings.TrimSpace(value))
	for _, item := range allowed {
		if v == item {
			return v
		}
	}
	return fallback
}

// NewExecutionPolicies 归一化外部传入的执行计划
func NewExecutionPolicies(errorPolicy, backupPolicy, executionMode, scheduledAt string) ExecutionPolicies {
	return ExecutionPolicies{
		ErrorPolicy:   normalizeOneOf(errorPolicy, ErrorPolicyStop, ErrorPolicyStop, ErrorPolicyContinue, ErrorPolicyRollback),
		BackupPolicy:  normalizeOneOf(backupPolicy, BackupPolicyStop, BackupPolicyContinue, BackupPolicyStop),
		ExecutionMode: normalizeOneOf(executionMode, ExecutionModeImmediate, ExecutionModeImmediate, ExecutionModeScheduled, ExecutionModeManual),
		ScheduledAt:   strings.TrimSpace(scheduledAt),
	}
}

func (p ExecutionPolicies) errorPolicy() string {
	return normalizeOneOf(p.ErrorPolicy, ErrorPolicyStop, ErrorPolicyStop, ErrorPolicyContinue, ErrorPolicyRollback)
}

func (p ExecutionPolicies) backupPolicy() string {
	return normalizeOneOf(p.BackupPolicy, BackupPolicyStop, BackupPolicyContinue, BackupPolicyStop)
}

// configJSON 序列化为 execution_config 快照
func (p ExecutionPolicies) configJSON() datatypes.JSON {
	return buildExecutionConfig(p.ErrorPolicy, p.BackupPolicy, p.ExecutionMode, p.ScheduledAt)
}

// parseExecutionPolicies 从 execution_config 快照回读策略（定时触发、重跑时使用）
func parseExecutionPolicies(raw datatypes.JSON) ExecutionPolicies {
	if len(raw) == 0 {
		return NewExecutionPolicies("", "", ExecutionModeImmediate, "")
	}
	var config map[string]string
	if err := json.Unmarshal(raw, &config); err != nil {
		return NewExecutionPolicies("", "", ExecutionModeImmediate, "")
	}
	return NewExecutionPolicies(config["error_policy"], config["backup_policy"], config["execution_mode"], config["scheduled_at"])
}

// parseScheduledAt 解析定时执行时间（允许的时间格式与前端 datetime 选择器一致）
func parseScheduledAt(raw string) (time.Time, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return time.Time{}, errors.New("定时执行必须指定执行时间")
	}
	t, err := time.ParseInLocation(orderTimeLayout, v, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("定时执行时间格式不正确（应为 YYYY-MM-DD HH:mm:ss）")
	}
	return t, nil
}

// persistExecutionPlan 落库本次执行计划。
// 同时写 execution_config 快照，便于审计与定时触发时按同一套策略执行。
func persistExecutionPlan(orderID string, p ExecutionPolicies) error {
	updates := map[string]any{
		"execution_mode":   p.ExecutionMode,
		"execution_config": p.configJSON(),
	}
	if p.ExecutionMode == ExecutionModeScheduled && p.ScheduledAt != "" {
		updates["scheduled_at"] = p.ScheduledAt
	} else {
		updates["scheduled_at"] = nil
	}
	return global.App.DB.Model(&ordersModels.InsightOrderRecords{}).
		Where("order_id = ?", orderID).
		Updates(updates).Error
}

func updateOrderStatusAfterExecution(orderID string) error {
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		var counts struct {
			Failed    int64
			Executing int64
			Pending   int64
			Completed int64
			Total     int64
		}
		if err := tx.Table("`insight_order_tasks`").
			Select(`
				COUNT(*) AS total,
				SUM(CASE WHEN progress = 'FAILED' THEN 1 ELSE 0 END) AS failed,
				SUM(CASE WHEN progress = 'EXECUTING' THEN 1 ELSE 0 END) AS executing,
				SUM(CASE WHEN progress = 'PENDING' THEN 1 ELSE 0 END) AS pending,
				SUM(CASE WHEN progress = 'COMPLETED' THEN 1 ELSE 0 END) AS completed
			`).
			Where("order_id = ?", orderID).
			Scan(&counts).Error; err != nil {
			return err
		}
		if counts.Total == 0 {
			return nil
		}
		nextProgress := "EXECUTING"
		switch {
		case counts.Failed > 0:
			nextProgress = "FAILED"
		case counts.Executing > 0:
			nextProgress = "EXECUTING"
		case counts.Pending == 0 && counts.Completed == counts.Total:
			nextProgress = "COMPLETED"
		}
		updates := map[string]any{"progress": nextProgress}
		// 工单执行结束（成功或失败）时落执行完成时间，保证「执行时间」字段不为空
		if nextProgress == "COMPLETED" || nextProgress == "FAILED" {
			updates["executed_at"] = time.Now().Format(orderTimeLayout)
		}
		return tx.Model(&ordersModels.InsightOrderRecords{}).
			Where("order_id = ?", orderID).
			Updates(updates).Error
	})
}

// 尝试获取工单执行锁，返回 false 表示已有执行在进行。
// 参数保留 context.Context（*gin.Context 也满足），便于定时调度器以非 HTTP 上下文加锁。
//
// 实现沿革：原先基于 Redis SetNX 且过期时间传 0（永不过期），进程崩溃后
// 该工单会被永久锁死，只能手工清理 Redis key。现改为进程内带租约锁
// （pkg/lock）：进程重启锁自然消失；同进程内漏掉释放时，
// 最坏 6 小时（lock.DefaultTTL）后自动恢复可执行。
func acquireOrderLock(ctx context.Context, orderID string) (bool, error) {
	key := fmt.Sprintf("order:lock:%s", orderID)
	switch global.App.Locks.Acquire(key) {
	case lock.StatusAcquired:
		return true, nil
	case lock.StatusReclaimed:
		// 抢到了一个过期的残留锁：上一次执行没有正常走到释放。
		// 执行继续，但必须留痕——这是执行路径存在漏洞的信号。
		global.App.Log.Warnf("工单锁租约已过期被回收（上次执行未正常释放）: order_id=%s", orderID)
		return true, nil
	default:
		return false, nil
	}
}

// 释放锁
func releaseOrderLock(ctx context.Context, orderID string) error {
	key := fmt.Sprintf("order:lock:%s", orderID)
	global.App.Locks.Release(key)
	return nil
}

// 根据子任务状态汇总工单状态
func updateOrderStatusToFinish(orderID string) error {
	return updateOrderStatusAfterExecution(orderID)
}

// 判断当前工单是否没有执行中的任务
func noTaskExecuting(orderID string) bool {
	var count int64
	global.App.DB.Table("`insight_order_tasks`").
		Where("order_id = ? AND progress = 'EXECUTING'", orderID).
		Count(&count)
	return count == 0
}

func CheckOrderExecutable(record *ordersModels.InsightOrderRecords) error {
	if !utils.IsContain([]string{"APPROVED", "EXECUTING", "FAILED"}, string(record.Progress)) {
		progressMap := map[string]string{
			"WAIT_PRECHECK": "待预审",
			"PENDING":       "审批中",
			"APPROVED":      "审批通过",
			"REJECTED":      "审批驳回",
			"EXECUTING":     "执行中",
			"COMPLETED":     "执行成功",
			"FAILED":        "执行失败",
			"TERMINATED":    "已终止",
			"WITHDRAWN":     "已撤回",
		}
		progressCN := progressMap[string(record.Progress)]
		if progressCN == "" {
			progressCN = string(record.Progress)
		}
		return fmt.Errorf("当前工单%s，禁止执行", progressCN)
	}
	return nil
}

func buildExecutionConfig(errorPolicy string, backupPolicy string, executionMode string, scheduledAt string) datatypes.JSON {
	config := map[string]string{
		"error_policy":   strings.TrimSpace(errorPolicy),
		"backup_policy":  strings.TrimSpace(backupPolicy),
		"execution_mode": strings.TrimSpace(executionMode),
		"scheduled_at":   strings.TrimSpace(scheduledAt),
	}
	data, _ := json.Marshal(config)
	return datatypes.JSON(data)
}

// 操作历史中的 SQL 摘要：压缩空白并截断，避免日志内容过长
func truncateSQLHint(sql string) string {
	s := strings.Join(strings.Fields(strings.TrimSpace(sql)), " ")
	runes := []rune(s)
	if len(runes) > 60 {
		return string(runes[:60]) + "..."
	}
	return s
}

// 写执行相关操作日志（失败仅记日志不阻断执行流程）
func writeExecuteLog(orderID, username, msg string) {
	_ = global.App.DB.Transaction(func(tx *gorm.DB) error {
		return WriteOrderLog(tx, orderID, username, msg)
	})
}

// normalizeOrderTargetSchema 归一化工单目标资源路径。
// PG 类实例（PostgreSQL/SQLServer）的 schema 字段存「database.schema」完整资源路径
// （与前端资源树、DAS 在线查询、执行器 parseDatabaseAndSchema 的口径一致）。
// 这里做幂等规范化：缺 database 前缀则补上（兼容历史裸 schema 数据），已有前缀则原样保留，
// 避免二次拼接产生「postgres.postgres.public」这类不存在的路径导致 search_path 失效（SQLSTATE 3F000）。
func normalizeOrderTargetSchema(dbType, database, schema string) string {
	db := strings.TrimSpace(database)
	sc := strings.TrimSpace(schema)
	if !isPgLikeDB(dbType) {
		// MySQL 类实例仅维护一个库字段，schema 与 database 同义，保持历史口径
		if sc == "" {
			sc = db
		}
		return sc
	}
	if sc == "" {
		return db
	}
	if db != "" && sc != db && !strings.HasPrefix(sc, db+".") {
		return db + "." + sc
	}
	return sc
}

// loadOrderDBConfig 组装工单目标实例的连接配置（含密码解密）。
// 执行（executeTask）与回滚（rollbackSucceededTasks）共用，避免两处拼装口径漂移。
// sqlTypeOverride 非空时覆盖工单自身的 SQL 类型（回滚语句一律按 DML 处理）。
func loadOrderDBConfig(orderID, taskID, sqlText, sqlTypeOverride string) (*base.DBConfig, error) {
	// 获取DB配置信息
	type Record struct {
		Hostname         string
		Port             uint16
		User             string
		Password         string
		Database         string
		Schema           string
		ExtraConfig      []byte
		DBType           string
		SQLType          string
		ExportFileFormat string
	}
	var record Record
	tx := global.App.DB.Table("`insight_order_records` a").
		Select("a.db_type,a.sql_type,a.database,a.schema,a.export_file_format,b.hostname,b.port,b.user,b.password,b.extra_config").
		Joins("join `insight_instances` b on a.instance_id=b.instance_id").
		Where("a.order_id=?", orderID).Take(&record)
	if tx.RowsAffected == 0 {
		return nil, errors.New("执行失败，没有发现工单关联的数据库信息")
	}
	// 解密密码
	plainPassword, err := secret.Decrypt(record.Password)
	if err != nil {
		return nil, fmt.Errorf("解密实例连接密码失败：%v", err)
	}
	sqlType := record.SQLType
	if strings.TrimSpace(sqlTypeOverride) != "" {
		sqlType = sqlTypeOverride
	}
	return &base.DBConfig{
		Hostname:         record.Hostname,
		Port:             record.Port,
		UserName:         record.User,
		Password:         plainPassword,
		Schema:           normalizeOrderTargetSchema(record.DBType, record.Database, record.Schema),
		ExtraConfig:      record.ExtraConfig,
		DBType:           record.DBType,
		SQLType:          sqlType,
		ExportFileFormat: record.ExportFileFormat,
		SQL:              sqlText,
		OrderID:          orderID,
		TaskID:           taskID,
	}, nil
}

// 执行任务
//
// sqlType 按**任务自身**的类型传入（不再统一用整单类型）：混合工单里 DDL 与 DML
// 混排，只有按条分派才能分别进入 DDL / DML 执行通道。存量任务 sql_type 为空时
// 回落为整单类型，行为与历史一致。
func executeTask(task ordersModels.InsightOrderTasks) (string, error) {
	config, err := loadOrderDBConfig(task.OrderID, task.TaskID, task.SQL, string(task.SQLType))
	if err != nil {
		returnData := base.ReturnData{Error: err.Error()}
		data, _ := json.Marshal(returnData)
		return string(data), err
	}
	// 执行工单
	executor := execute.NewExecuteSQLAPI(config)
	returnData, err := executor.Run()
	// 转换为json
	data, _ := json.Marshal(returnData)
	return string(data), err
}

// backupFailure 判定是否为「备份失败」（SQL 已成功、仅回滚依据缺失）
func backupFailure(err error) bool {
	var backupErr base.BackupSQLError
	return errors.As(err, &backupErr)
}

// splitSQLStatements 拆分由多条语句用 ';' 拼接的回滚 SQL。
// Binlog 生成的反向语句形如 `INSERT INTO t(a) VALUES('a;b');\r\nINSERT INTO ...`，
// 直接按 ';' 切会把字符串值里的分号切断，因此这里做引号感知扫描：
// 只有「不在引号内」的分号才是语句边界；引号内支持 \ 转义与 ” 双写转义。
func splitSQLStatements(raw string) []string {
	var (
		out   []string
		buf   strings.Builder
		quote rune // 0 表示不在引号内
	)
	flush := func() {
		stmt := strings.TrimSpace(buf.String())
		buf.Reset()
		if stmt != "" {
			out = append(out, stmt)
		}
	}
	runes := []rune(raw)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote != 0:
			buf.WriteRune(r)
			if r == '\\' && i+1 < len(runes) {
				// 反斜杠转义：连同下一个字符一起吞掉
				buf.WriteRune(runes[i+1])
				i++
				continue
			}
			if r == quote {
				// 引号双写（''、""、``）是转义写法，继续留在引号内
				if i+1 < len(runes) && runes[i+1] == quote {
					buf.WriteRune(runes[i+1])
					i++
					continue
				}
				quote = 0
			}
		case r == '\'' || r == '"' || r == '`':
			quote = r
			buf.WriteRune(r)
		case r == ';':
			flush()
		default:
			buf.WriteRune(r)
		}
	}
	flush()
	return out
}

// executeRollbackStatement 执行单条回滚语句。
// 回滚语句来源：
//   - MySQL 类（MySQL/Doris/TiDB）：Binlog 行事件解析；
//   - PostgreSQL/Kingbase：逻辑解码（test_decoding + 临时复制槽）；
// 这里直连目标库逐条执行（不走工单执行器：避免为回滚动作再生成一次回滚 SQL）。
func executeRollbackStatement(config *base.DBConfig, stmt string) error {
	switch config.DBType {
	case "MySQL", "Doris", "TiDB":
	case "PostgreSQL", "Kingbase":
		return executePostgresRollbackStatement(config, stmt)
	default:
		return fmt.Errorf("当前数据库类型(%s)不支持自动回滚", config.DBType)
	}
	db, err := mysql.NewMySQLCnx(config)
	if err != nil {
		return err
	}
	defer db.Close()
	ch := make(chan int64, 1)
	if _, err = mysql.DaoMySQLExecute(db, stmt, ch); err != nil {
		return err
	}
	return nil
}

// executePostgresRollbackStatement 直连 PG 系实例执行单条回滚语句。
// 回滚语句可能是多语句块（BEGIN;...;COMMIT; 由调用方 splitSQLStatements 拆分），
// 这里逐条自动提交执行即可满足幂等性（语句本身已带 ON CONFLICT/IS NOT DISTINCT FROM 守卫）。
func executePostgresRollbackStatement(cfg *base.DBConfig, stmt string) error {
	databaseName, err := splitRollbackDatabase(cfg)
	if err != nil {
		return err
	}
	sslMode := "disable"
	if len(cfg.ExtraConfig) > 0 {
		var extra struct {
			SSLMode string `json:"sslmode"`
		}
		_ = json.Unmarshal(cfg.ExtraConfig, &extra)
		if extra.SSLMode != "" {
			sslMode = extra.SSLMode
		}
	}
	connString := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.UserName, cfg.Password, cfg.Hostname, cfg.Port, databaseName, sslMode)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, connString)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, stmt)
	return err
}

// splitRollbackDatabase 从 DBConfig.Schema（「database.schema」资源路径）拆出回滚直连的库名。
func splitRollbackDatabase(cfg *base.DBConfig) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(cfg.Schema), ".", 2)
	if parts[0] == "" {
		return "", fmt.Errorf("回滚连接缺少目标库信息")
	}
	return parts[0], nil
}

// rollbackSucceededTasks 回滚本次已执行成功的任务（error_policy=ROLLBACK），按执行逆序处理。
// 回滚依据是任务执行时由 Binlog 生成、存在 result.rollback_sql 的反向 SQL：
//   - 无回滚 SQL（非 MySQL 类变更 / 影响行数为 0 / 超过阈值跳过生成）→ 记录并跳过
//   - 单条失败不中断整体回滚，逐条记录，最终汇总
func rollbackSucceededTasks(orderID, username string, tasks []ordersModels.InsightOrderTasks) {
	if len(tasks) == 0 {
		writeExecuteLog(orderID, username, "按「执行出错后回滚任务」策略：本次没有执行成功的任务，无需回滚")
		return
	}
	writeExecuteLog(orderID, username, fmt.Sprintf("按「执行出错后回滚任务」策略：开始回滚本次执行成功的 %d 个任务（逆序）", len(tasks)))
	for i := len(tasks) - 1; i >= 0; i-- {
		task := tasks[i]
		var result struct {
			RollbackSQL string `json:"rollback_sql"`
		}
		_ = json.Unmarshal([]byte(task.Result), &result)
		sqlText := strings.TrimSpace(result.RollbackSQL)
		if sqlText == "" {
			// DDL 与「无数据变更」的 DML 都会走到这里，但原因完全不同：
			// 前者是结构变更天生不可回滚，后者是没改数据不需要回滚。
			// 混合工单里混着这两类，日志必须说清，否则会被误读成「回滚失败」。
			if strings.EqualFold(string(task.SQLType), orderSQLTypeDDL) {
				writeExecuteLog(orderID, username, fmt.Sprintf(
					"任务 %s 为 DDL 语句，结构变更不可回滚（无 binlog 反向 SQL），跳过；如已生效需人工处理", task.TaskID))
			} else {
				writeExecuteLog(orderID, username, fmt.Sprintf("任务 %s 无回滚 SQL（非 MySQL 类变更或影响行数为 0），跳过回滚", task.TaskID))
			}
			continue
		}
		statements := splitSQLStatements(sqlText)
		if len(statements) == 0 {
			writeExecuteLog(orderID, username, fmt.Sprintf("任务 %s 回滚 SQL 解析为空，跳过回滚", task.TaskID))
			continue
		}
		config, err := loadOrderDBConfig(orderID, task.TaskID, "", "DML")
		if err != nil {
			writeExecuteLog(orderID, username, fmt.Sprintf("任务 %s 回滚失败：%v", task.TaskID, err))
			continue
		}
		failedCount := 0
		for _, stmt := range statements {
			if execErr := executeRollbackStatement(config, stmt); execErr != nil {
				failedCount++
				writeExecuteLog(orderID, username, fmt.Sprintf("任务 %s 回滚语句执行失败：%v", task.TaskID, execErr))
			}
		}
		if failedCount == 0 {
			writeExecuteLog(orderID, username, fmt.Sprintf("任务 %s 回滚成功（%d 条语句）", task.TaskID, len(statements)))
			markTaskRolledBack(task.TaskID, len(statements))
		} else {
			writeExecuteLog(orderID, username, fmt.Sprintf("任务 %s 回滚未完全成功：%d/%d 条语句失败", task.TaskID, failedCount, len(statements)))
		}
	}
}

// markTaskRolledBack 在任务结果里标记回滚状态（前端「执行详情」可见）
func markTaskRolledBack(taskID string, statementCount int) {
	var task ordersModels.InsightOrderTasks
	if err := global.App.DB.Where("task_id = ?", taskID).Take(&task).Error; err != nil {
		return
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(task.Result), &result); err != nil || result == nil {
		return
	}
	result["rolled_back"] = true
	result["rollback_statement_count"] = statementCount
	if data, err := json.Marshal(result); err == nil {
		global.App.DB.Model(&ordersModels.InsightOrderTasks{}).
			Where("task_id = ?", taskID).
			Update("result", string(data))
	}
}

// runOrderTasks 串行执行工单下所有未完成任务（批量执行内核）。
// 批量执行接口与定时调度器共用：两者只在「谁触发、用什么身份」上不同，
// 加锁、状态归约、策略分支、回滚必须完全一致。
func runOrderTasks(ctx context.Context, orderID, username string, policies ExecutionPolicies) error {
	var record ordersModels.InsightOrderRecords
	tx := global.App.DB.Table("`insight_order_records`").Where("order_id=?", orderID).Take(&record)
	if tx.Error != nil {
		return fmt.Errorf("查询工单记录失败: %v", tx.Error)
	}
	if tx.RowsAffected == 0 {
		return fmt.Errorf("工单记录`%s`不存在", orderID)
	}
	// 检查工单状态是否允许执行
	if err := CheckOrderExecutable(&record); err != nil {
		return err
	}
	// 判断当前工单是否存在执行中的任务，避免跳过执行中的任务执行其他的任务
	if !noTaskExecuting(orderID) {
		return ErrOrderTaskExecuting
	}
	// 获取锁
	locked, err := acquireOrderLock(ctx, orderID)
	if err != nil {
		return fmt.Errorf("获取工单锁失败: %v", err)
	}
	if !locked {
		return ErrOrderBusy
	}
	defer releaseOrderLock(ctx, orderID)
	// 更新当前工单进度为执行中。
	// 同时落 started_at：它是「排队等待多久」与「执行本身多久」的分界点，
	// 缺失时页面只能拿 executed_at（完成时刻）当执行时间，无法解释定时工单的触发延迟。
	if tx = global.App.DB.Model(&ordersModels.InsightOrderRecords{}).
		Where("order_id=?", orderID).
		Updates(map[string]any{
			"progress":    "EXECUTING",
			"executor":    username,
			"started_at":  time.Now().Format(orderTimeLayout),
			"executed_at": nil,
		}); tx.Error != nil {
		global.App.Log.Error(tx.Error)
		return tx.Error
	}
	// 获取当前工单所有的任务
	// 必须按 seq 显式排序：混合工单（DDL + DML 同单）的执行顺序 = 语句书写顺序，
	// 依赖「批量插入后主键自增」这种隐式顺序在并发/重排场景下不可靠。
	var tasks []ordersModels.InsightOrderTasks
	if tx = global.App.DB.Where("order_id=?", orderID).Order("seq asc, id asc").Find(&tasks); tx.Error != nil {
		return tx.Error
	}

	// 操作历史：提交执行
	writeExecuteLog(orderID, username, fmt.Sprintf(
		"用户%s提交执行，共%d个任务（错误处理：%s，备份失败处理：%s）",
		username, len(tasks), errorPolicyLabel(policies.errorPolicy()), backupPolicyLabel(policies.backupPolicy())))

	// 混合工单的执行约束要在动手之前说清楚：
	// DDL 在 MySQL/PG 上都是隐式提交、无法用 binlog/事务回滚，
	// 「执行出错后回滚任务」只能回滚 DML 部分。提前写进操作历史，
	// 事后才不会把它当成「平台回滚失败」。
	ddlTaskCount := 0
	for _, task := range tasks {
		if strings.EqualFold(string(task.SQLType), orderSQLTypeDDL) {
			ddlTaskCount++
		}
	}
	if ddlTaskCount > 0 {
		writeExecuteLog(orderID, username, fmt.Sprintf(
			"本工单包含 %d 条 DDL 语句，按语句书写顺序串行执行；DDL 一旦执行成功即不可回滚（结构变更无法通过事务或 binlog 逆向还原）", ddlTaskCount))
		if policies.errorPolicy() == ErrorPolicyRollback {
			writeExecuteLog(orderID, username,
				"注意：当前「执行出错后回滚任务」策略仅对 DML 语句生效，已执行的 DDL 不会回滚，需人工评估后另行处理")
		}
	}

	// 本次执行成功的任务（回滚策略的回滚对象，按执行顺序记录）
	succeeded := make([]ordersModels.InsightOrderTasks, 0, len(tasks))

	// 串行执行任务
	for _, task := range tasks {
		// 跳过已完成的任务，避免重复执行
		if task.Progress == "COMPLETED" {
			continue
		}

		// 更新当前任务进度为执行中，同时落任务级开始时间
		if tx = global.App.DB.Model(&ordersModels.InsightOrderTasks{}).
			Where("task_id=?", task.TaskID).
			Updates(map[string]any{"progress": "EXECUTING", "started_at": time.Now()}); tx.Error != nil {
			global.App.Log.Error(tx.Error)
			return tx.Error
		}

		// 操作历史：开始执行
		writeExecuteLog(orderID, username, fmt.Sprintf("开始执行任务，SQL：%s", truncateSQLHint(task.SQL)))

		// 执行任务
		data, err := executeTask(task)

		// 备份失败：SQL 已执行成功，仅回滚依据（备份）没拿到 → 按「备份失败处理」策略
		if err != nil && backupFailure(err) {
			global.App.DB.Model(&ordersModels.InsightOrderTasks{}).
				Where("task_id=?", task.TaskID).
				Updates(map[string]any{"progress": "COMPLETED", "result": data, "finished_at": time.Now()})
			writeExecuteLog(orderID, username, fmt.Sprintf("任务 %s 已执行成功，但备份失败：%v", task.TaskID, err))
			succeeded = append(succeeded, task)
			if policies.backupPolicy() == BackupPolicyContinue {
				writeExecuteLog(orderID, username, "按「备份失败后继续任务」策略，继续执行后续任务")
				continue
			}
			writeExecuteLog(orderID, username, "按「备份失败后停止任务」策略，后续任务不再执行")
			break
		}

		if err != nil {
			var taskProgress string
			// 错误类型断言，可以添加更多状态
			switch err.(type) {
			case base.SQLExecuteError:
				taskProgress = "FAILED"
			case base.RollbackSQLError:
				taskProgress = "COMPLETED"
			default:
				taskProgress = "FAILED"
			}
			global.App.DB.Model(&ordersModels.InsightOrderTasks{}).
				Where("task_id=?", task.TaskID).
				Updates(map[string]any{"progress": taskProgress, "result": data, "finished_at": time.Now()})
			writeExecuteLog(orderID, username, fmt.Sprintf("执行任务 %s 失败：%v", task.TaskID, err))

			switch policies.errorPolicy() {
			case ErrorPolicyContinue:
				writeExecuteLog(orderID, username, "按「执行出错后继续任务」策略，跳过失败任务继续执行后续任务")
				continue
			case ErrorPolicyRollback:
				rollbackSucceededTasks(orderID, username, succeeded)
			default:
				writeExecuteLog(orderID, username, "按「执行出错后终止任务」策略，后续任务不再执行")
			}
			break
		}

		global.App.DB.Model(&ordersModels.InsightOrderTasks{}).
			Where("task_id=?", task.TaskID).
			Updates(map[string]any{"progress": "COMPLETED", "result": data, "finished_at": time.Now()})

		// 工单里建库/删库成功后同样要立刻失效元数据快照：
		// 审批通过建出来的库，如果还要等定时任务，审批人和申请人都会以为「没建成功」。
		dasServices.NotifySchemaInventoryChanged(record.InstanceID, task.SQL)

		writeExecuteLog(orderID, username, fmt.Sprintf("任务执行成功，SQL：%s", truncateSQLHint(task.SQL)))
		succeeded = append(succeeded, task)
	}
	// 更新工单状态为已完成
	updateOrderStatusToFinish(orderID)
	return nil
}

func errorPolicyLabel(policy string) string {
	switch policy {
	case ErrorPolicyContinue:
		return "执行出错后继续任务"
	case ErrorPolicyRollback:
		return "执行出错后回滚任务"
	default:
		return "执行出错后终止任务"
	}
}

func backupPolicyLabel(policy string) string {
	if policy == BackupPolicyContinue {
		return "备份失败后继续任务"
	}
	return "备份失败后停止任务"
}

// 执行单个任务
type ExecuteTaskService struct {
	*forms.ExecuteTaskForm
	C        *gin.Context
	Username string
}

func (s *ExecuteTaskService) Run() (err error) {
	// 单条执行固定为「立即执行」：执行方式只对整单提交有意义，
	// 弹窗里可能残留未提交的 SCHEDULED 选择，若沿用会出现「点了执行却没动静」。
	// 错误处理 / 备份失败处理策略沿用本次请求（用户在弹窗里的选择）。
	policies := NewExecutionPolicies(s.ErrorPolicy, s.BackupPolicy, ExecutionModeImmediate, "")

	// 检查工单记录是否存在
	var record ordersModels.InsightOrderRecords
	tx := global.App.DB.Table("`insight_order_records`").Where("order_id=?", s.OrderID).Take(&record)
	if tx.Error != nil {
		return fmt.Errorf("查询工单记录失败: %v", tx.Error)
	}
	if tx.RowsAffected == 0 {
		return fmt.Errorf("工单记录`%s`不存在", s.OrderID)
	}
	if record.Executor != s.Username {
		return fmt.Errorf("您不是工单执行人，没有执行工单权限")
	}
	// 检查工单状态是否允许执行
	if err := CheckOrderExecutable(&record); err != nil {
		return err
	}
	// 获取锁
	locked, err := acquireOrderLock(s.C, s.OrderID)
	if err != nil {
		return fmt.Errorf("获取工单锁失败: %v", err)
	}
	if !locked {
		return fmt.Errorf("工单正在执行中，请稍后再试")
	}
	defer releaseOrderLock(s.C, s.OrderID)
	// 获取任务记录
	var task ordersModels.InsightOrderTasks
	tx = global.App.DB.Table("`insight_order_tasks`").Where("task_id=? and order_id=?", s.TaskID, s.OrderID).Take(&task)
	if tx.RowsAffected == 0 {
		return fmt.Errorf("任务记录不存在")
	}
	// 跳过已完成的任务
	if task.Progress == "COMPLETED" {
		return errors.New("当前任务已完成，请勿重复执行")
	}
	// 跳过执行中的任务
	if task.Progress == "EXECUTING" {
		return errors.New("当前任务正在执行中，请勿重复执行")
	}
	// 更新当前任务进度为执行中，工单状态为执行中
	if err := func() error {
		return global.App.DB.Transaction(func(tx *gorm.DB) error {
			if tx = tx.Model(&ordersModels.InsightOrderTasks{}).
				Where("task_id=? and order_id=?", s.TaskID, s.OrderID).
				Updates(map[string]any{"progress": "EXECUTING", "executor": s.Username, "started_at": time.Now()}); tx.Error != nil {
				global.App.Log.Error(tx.Error)
				return tx.Error
			}
			if tx = tx.Model(&ordersModels.InsightOrderRecords{}).
				Where("order_id=?", s.OrderID).
				Updates(map[string]any{
					"progress": "EXECUTING",
					"executor": s.Username,
				}); tx.Error != nil {
				global.App.Log.Error(tx.Error)
				return tx.Error
			}

			return nil
		})
	}(); err != nil {
		return err
	}

	// 操作历史：提交执行
	writeExecuteLog(s.OrderID, s.Username, fmt.Sprintf("用户%s提交执行（单条任务：%s）", s.Username, task.TaskID))

	// 操作历史：开始执行
	writeExecuteLog(s.OrderID, s.Username, fmt.Sprintf("开始执行任务，SQL：%s", truncateSQLHint(task.SQL)))

	// 执行任务
	data, err := executeTask(task)

	// 备份失败：SQL 已成功，按「备份失败处理」策略记录，任务仍按执行成功计
	if err != nil && backupFailure(err) {
		global.App.DB.Model(&ordersModels.InsightOrderTasks{}).
			Where("task_id=? and order_id=?", s.TaskID, s.OrderID).
			Updates(map[string]any{"progress": "COMPLETED", "result": data, "finished_at": time.Now()})
		writeExecuteLog(s.OrderID, s.Username, fmt.Sprintf("任务 %s 已执行成功，但备份失败：%v", s.TaskID, err))
		if policies.backupPolicy() == BackupPolicyStop {
			writeExecuteLog(s.OrderID, s.Username, "按「备份失败后停止任务」策略：请检查备份/回滚依据是否符合预期后再继续")
		}
		_ = updateOrderStatusAfterExecution(s.OrderID)
		return nil
	}

	// 返回错误，更新任务状态
	if err != nil {
		var taskProgress string
		// 错误类型断言，可以添加更多状态
		switch err.(type) {
		case base.SQLExecuteError:
			taskProgress = "FAILED"
		case base.RollbackSQLError:
			taskProgress = "COMPLETED"
		default:
			taskProgress = "FAILED"
		}
		global.App.DB.Model(&ordersModels.InsightOrderTasks{}).
			Where("task_id=? and order_id=?", s.TaskID, s.OrderID).
			Updates(map[string]any{"progress": taskProgress, "result": data, "finished_at": time.Now()})
		_ = updateOrderStatusAfterExecution(s.OrderID)
		_ = global.App.DB.Transaction(func(tx *gorm.DB) error {
			return WriteOrderLog(tx, s.OrderID, s.Username, fmt.Sprintf("执行任务失败：%v", err))
		})
		return err
	}

	// 没有错误返回，更新任务状态为已完成
	global.App.DB.Model(&ordersModels.InsightOrderTasks{}).
		Where("task_id=? and order_id=?", s.TaskID, s.OrderID).
		Updates(map[string]any{"progress": "COMPLETED", "result": data, "finished_at": time.Now()})

	// 操作历史：执行成功
	writeExecuteLog(s.OrderID, s.Username, fmt.Sprintf("任务执行成功，SQL：%s", truncateSQLHint(task.SQL)))

	// 更新工单状态为已完成
	updateOrderStatusToFinish(s.OrderID)
	return nil
}

// 批量执行任务
type ExecuteBatchTasksService struct {
	*forms.ExecuteBatchTasksForm
	C        *gin.Context
	Username string
}

func (s *ExecuteBatchTasksService) Run() (err error) {
	// 检查工单状态和执行权限
	var record ordersModels.InsightOrderRecords
	tx := global.App.DB.Table("`insight_order_records`").Where("order_id=?", s.OrderID).Take(&record)
	if tx.Error != nil {
		return fmt.Errorf("查询工单记录失败: %v", tx.Error)
	}
	// 检查工单记录是否存在
	if tx.RowsAffected == 0 {
		return fmt.Errorf("工单记录`%s`不存在", s.OrderID)
	}
	if record.Executor != s.Username {
		return fmt.Errorf("您不是工单执行人，没有执行工单权限")
	}
	// 检查工单状态是否允许执行
	if err := CheckOrderExecutable(&record); err != nil {
		return err
	}

	policies := NewExecutionPolicies(s.ErrorPolicy, s.BackupPolicy, s.ExecutionMode, s.ScheduledAt)

	switch policies.ExecutionMode {
	case ExecutionModeScheduled:
		// 定时执行：只登记计划，不执行。由调度器（每分钟扫描）到期触发，
		// 触发时按落库的 execution_config 策略执行。
		scheduledAt, parseErr := parseScheduledAt(policies.ScheduledAt)
		if parseErr != nil {
			return parseErr
		}
		if !scheduledAt.After(time.Now()) {
			return errors.New("定时执行时间必须晚于当前时间")
		}
		if err := persistExecutionPlan(s.OrderID, policies); err != nil {
			return fmt.Errorf("登记定时执行失败：%v", err)
		}
		// 注册进程内定时器：到点秒级触发，不依赖每分钟轮询（轮询只作重启兜底）
		RegisterScheduledPlan(s.OrderID, scheduledAt)
		writeExecuteLog(s.OrderID, s.Username, fmt.Sprintf(
			"用户%s提交执行：登记为「定时执行」，计划执行时间 %s（错误处理：%s，备份失败处理：%s）",
			s.Username, scheduledAt.Format(orderTimeLayout),
			errorPolicyLabel(policies.errorPolicy()), backupPolicyLabel(policies.backupPolicy())))
		return nil

	case ExecutionModeManual:
		// 手动执行：只登记模式，不执行任何任务；由执行人在任务列表逐条触发。
		if err := persistExecutionPlan(s.OrderID, policies); err != nil {
			return fmt.Errorf("登记手动执行失败：%v", err)
		}
		CancelScheduledPlan(s.OrderID)
		writeExecuteLog(s.OrderID, s.Username, fmt.Sprintf(
			"用户%s提交执行：登记为「手动执行」，请在任务列表逐条触发（错误处理：%s，备份失败处理：%s）",
			s.Username, errorPolicyLabel(policies.errorPolicy()), backupPolicyLabel(policies.backupPolicy())))
		return nil
	}

	// 立即执行：落计划快照（同时清除可能存在的定时计划）后同步执行
	if err := persistExecutionPlan(s.OrderID, policies); err != nil {
		return fmt.Errorf("登记执行计划失败：%v", err)
	}
	// 已改为立即执行，撤掉可能残留的定时触发点
	CancelScheduledPlan(s.OrderID)
	return runOrderTasks(context.Background(), s.OrderID, s.Username, policies)
}

// ------------------------------------------------------------------
// 定时执行入口（供 internal/orders/tasks 的调度器调用）
// ------------------------------------------------------------------

// ExecuteScheduledOrder 执行一个到期的定时工单。
// 触发身份沿用工单执行人（executor），保持「只有执行人能执行」的权限口径与操作历史一致；
// 策略从 execution_config 快照回读，保证与登记时选择的错误/备份策略完全一致。
func ExecuteScheduledOrder(orderID string) error {
	var record ordersModels.InsightOrderRecords
	if err := global.App.DB.Where("order_id = ?", orderID).Take(&record).Error; err != nil {
		return fmt.Errorf("查询工单记录失败: %v", err)
	}
	username := strings.TrimSpace(record.Executor)
	if username == "" {
		username = strings.TrimSpace(record.Applicant)
	}
	policies := parseExecutionPolicies(record.ExecutionConfig)
	// 计划已到期并进入执行，标记为立即执行，避免调度器重复认领
	policies.ExecutionMode = ExecutionModeImmediate
	scheduledAt := time.Time(record.ScheduledAt)
	if !scheduledAt.IsZero() {
		writeExecuteLog(orderID, username, fmt.Sprintf("定时执行触发（计划执行时间 %s）", scheduledAt.Format(orderTimeLayout)))
	} else {
		writeExecuteLog(orderID, username, "定时执行触发")
	}
	return runOrderTasks(context.Background(), orderID, username, policies)
}

// RestoreScheduledPlan 定时触发失败（工单正忙等可重试原因）时写回定时计划，
// 让后续扫描继续尝试，而不是静默丢掉用户设定的定时执行。
//
// 只把 execution_mode 改回 SCHEDULED：scheduled_at 在认领时并未清空（它是
// 「定时等待多久」的审计依据），因此这里无需重设，避免把计划时间改写成当前时间。
func RestoreScheduledPlan(orderID string) {
	if err := global.App.DB.Model(&ordersModels.InsightOrderRecords{}).
		Where("order_id = ?", orderID).
		Update("execution_mode", ExecutionModeScheduled).Error; err != nil {
		global.App.Log.Warnf("restore scheduled plan for order %s failed: %v", orderID, err)
		return
	}
	// 重新挂上进程内定时器，避免只能等兜底轮询
	var record ordersModels.InsightOrderRecords
	if err := global.App.DB.Where("order_id = ?", orderID).Take(&record).Error; err == nil {
		if scheduledAt := time.Time(record.ScheduledAt); !scheduledAt.IsZero() {
			RegisterScheduledPlan(orderID, scheduledAt)
		}
	}
}
