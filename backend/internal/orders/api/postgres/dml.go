package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/simajiong/jaysql/internal/config"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/orders/api/base"
	"github.com/simajiong/jaysql/pkg/sqlsplit"
	"github.com/simajiong/jaysql/pkg/utils"
)

// ExecutePostgresDML 执行 PostgreSQL（含 Kingbase）DML 工单，并按条件生成精确回滚 SQL。
//
// 与 MySQL 侧（internal/orders/api/mysql/dml.go）的链路对照：
//
//	MySQL                                   PostgreSQL（本实现）
//	────────────────────────────────────    ────────────────────────────────────
//	binlog start/end position               复制槽创建点 → pg_current_wal_lsn()
//	连接 Connection ID 过滤                 显式事务的 txid（低 32 位）过滤
//	解析 RowsEvent 行事件                   pg_logical_slot_peek_changes + test_decoding
//	影响行数 10 万硬编码阈值                 order.pg_rollback_max_rows 可配置（默认 1000）
//
// 执行语义保持与既有实现一致：逐条执行、失败即停（每条语句各自成事务，
// 显式 BEGIN/COMMIT 包裹是为了同时取到该事务的 txid 用于解码过滤）。
type ExecutePostgresDML struct {
	*base.DBConfig
}

type postgresExtraConfig struct {
	SSLMode string `json:"sslmode"`
}

func (e *ExecutePostgresDML) Run() (data base.ReturnData, err error) {
	logger := base.NewExecuteLogger()
	publisher := base.NewPublisher()
	logFn := func(msg string) {
		cleaned := strings.TrimSpace(msg)
		formatted := logger.Add(cleaned) + "\n"
		publisher.Publish(e.OrderID, e.TaskID, utils.RenderLogStream, formatted)
	}

	ctx := context.Background()

	// ---------- 连接 ----------
	databaseName, _, err := parseDatabaseAndSchema(e.Schema)
	if err != nil {
		data.Error = err.Error()
		return data, err
	}
	cfg := postgresExtraConfig{}
	if len(e.ExtraConfig) > 0 {
		_ = json.Unmarshal(e.ExtraConfig, &cfg)
	}
	sslMode := cfg.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	connString := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		e.UserName, e.Password, e.Hostname, e.Port, databaseName, sslMode)
	conn, err := pgx.Connect(ctx, connString)
	if err != nil {
		logFn(fmt.Sprintf("访问数据库(%s:%d)失败，错误：%s", e.Hostname, e.Port, err.Error()))
		data.ExecuteLog = logger.String()
		data.Error = err.Error()
		return data, err
	}
	defer conn.Close(ctx)
	logFn(fmt.Sprintf("访问数据库(%s:%d)成功", e.Hostname, e.Port))
	logFn(fmt.Sprintf("数据库Backend PID：%d", conn.PgConn().PID()))

	// ---------- 回滚能力预检（轻量，绝不阻塞执行） ----------
	slot := e.prepareRollback(ctx, conn, logFn, &data)
	if slot != nil {
		// 槽创建点即解码起点；无论后续成败，用完必须释放，否则会钉住 WAL。
		defer func() {
			if dropErr := slot.drop(ctx); dropErr != nil {
				logFn(fmt.Sprintf("删除临时复制槽 %s 失败（不影响工单结果，请手动执行 SELECT pg_drop_replication_slot('%s')）：%s",
					slot.name, slot.name, dropErr.Error()))
			}
		}()
	}

	// ---------- 拆分语句 ----------
	parts := sqlsplit.Split(e.SQL)
	if len(parts) == 0 {
		parts = []string{e.SQL}
	}
	validParts := make([]string, 0, len(parts))
	for _, p := range parts {
		cleaned := strings.TrimSpace(p)
		cleaned = strings.TrimSuffix(cleaned, ";")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned != "" {
			validParts = append(validParts, cleaned)
		}
	}
	if len(validParts) == 0 {
		data.ExecuteLog = logger.String()
		data.Error = "没有可执行的SQL语句"
		return data, errors.New(data.Error)
	}

	// ---------- 执行（显式事务 + 记录 txid） ----------
	var xids []uint32
	var totalAffected int64
	startTime := time.Now()
	for idx, one := range validParts {
		logFn(fmt.Sprintf("开始执行第 %d/%d 条SQL", idx+1, len(validParts)))
		startT := time.Now()
		n, xid, execErr := execInTx(ctx, conn, one)
		cost := utils.HumanfriendlyTimeUnit(time.Since(startT))
		if execErr != nil {
			logFn(fmt.Sprintf("第 %d/%d 条SQL执行失败，错误：%s，耗时：%s", idx+1, len(validParts), execErr.Error(), cost))
			data.ExecuteLog = logger.String()
			data.Error = execErr.Error()
			data.AffectedRows = totalAffected
			return data, execErr
		}
		totalAffected += n
		xids = append(xids, xid)
		logFn(fmt.Sprintf("第 %d/%d 条SQL执行成功，影响行数%d，耗时：%s", idx+1, len(validParts), n, cost))
	}
	executeCostTime := utils.HumanfriendlyTimeUnit(time.Since(startTime))
	logFn(fmt.Sprintf("SQL全部执行成功，累计影响行数%d，总耗时：%s", totalAffected, executeCostTime))
	data.ExecuteLog = logger.String()
	data.AffectedRows = totalAffected
	data.ExecuteCostTime = executeCostTime

	// ---------- 回滚 SQL 生成 ----------
	if slot != nil {
		e.buildRollback(ctx, conn, slot, xids, totalAffected, logFn, &data)
	}
	data.ExecuteLog = logger.String()
	return data, nil
}

// prepareRollback 在执行前完成回滚能力的所有前置检查：
// wal_level、配置开关、建槽。任何一步失败都只记录轻量提醒并返回 nil，
// 绝不返回 error —— 回滚能力缺失不能阻塞工单执行。
func (e *ExecutePostgresDML) prepareRollback(ctx context.Context, conn *pgx.Conn,
	logFn func(string), data *base.ReturnData) *logicalSlot {

	maxRows := rollbackMaxRows()
	if maxRows < 0 {
		logFn("PostgreSQL 回滚 SQL 生成已被配置关闭（order.pg_rollback_max_rows < 0），跳过备份")
		data.BackupStatus = "已跳过（配置关闭）"
		return nil
	}

	walLevel, err := queryWALLevel(ctx, conn)
	if err != nil {
		// 连接都成功了却查不到 wal_level：极小概率（权限回收），同样轻量跳过
		logFn(fmt.Sprintf("查询实例 wal_level 失败，跳过回滚 SQL 生成（不影响执行）：%s", err.Error()))
		data.BackupStatus = "已跳过（无法确认 wal_level）"
		return nil
	}
	if walLevel != "logical" {
		logFn(fmt.Sprintf(
			"实例 wal_level=%s，未开启逻辑解码，本次工单不生成回滚 SQL（不影响正常执行）。如需回滚能力：在 postgresql.conf 中设置 wal_level=logical 并重启实例（该参数为重启级），无需其他变更",
			walLevel))
		data.BackupStatus = fmt.Sprintf("已跳过（实例未开启 wal_level=logical，当前 %s）", walLevel)
		return nil
	}

	slot, err := createLogicalSlot(ctx, conn, e.TaskID)
	if err != nil {
		logFn(fmt.Sprintf(
			"创建临时逻辑解码槽失败，跳过回滚 SQL 生成（不影响正常执行）。常见原因：执行账号缺少复制槽权限（需授予 pg_create_replication_slot 预定义角色）或实例未安装 test_decoding 插件。错误：%s",
			err.Error()))
		data.BackupStatus = "已跳过（无法创建逻辑解码槽）"
		return nil
	}
	logFn(fmt.Sprintf("已创建临时逻辑解码槽 %s（回滚 SQL 解码起点）", slot.name))
	return slot
}

// rollbackMaxRows 读取配置的影响行数上限（已应用默认值）。
func rollbackMaxRows() int64 {
	if global.App == nil {
		return config.DefaultPGRollbackMaxRows
	}
	return global.App.Config.OrderSettings().PGRollbackMaxRows
}

// buildRollback 在工单执行成功后生成回滚 SQL。
// 失败返回 base.BackupSQLError 语义（与 MySQL 侧一致：SQL 已生效，
// 失败的只是「生成回滚 SQL」，上层按备份失败策略处理，不能误报执行失败）。
func (e *ExecutePostgresDML) buildRollback(ctx context.Context, conn *pgx.Conn, slot *logicalSlot,
	xids []uint32, totalAffected int64, logFn func(string), data *base.ReturnData) {

	if totalAffected == 0 {
		data.BackupStatus = "已跳过（无数据变更）"
		return
	}
	maxRows := rollbackMaxRows()
	if totalAffected > maxRows {
		logFn(fmt.Sprintf(
			"影响行数 %d 超过回滚生成上限 %d（配置 order.pg_rollback_max_rows，可调整），已跳过回滚 SQL 生成；批量变更请分批执行，回滚请使用备份或闪回工具",
			totalAffected, maxRows))
		data.BackupStatus = fmt.Sprintf("已跳过（影响行数 %d 超过上限 %d）", totalAffected, maxRows)
		return
	}

	logFn("开始解码逻辑日志生成回滚SQL")
	startTime := time.Now()
	endLSN, err := queryCurrentWALLSN(ctx, conn)
	if err != nil {
		data.BackupStatus = "备份失败：" + err.Error()
		logFn("获取终止 WAL LSN 失败：" + err.Error())
		return
	}
	changes, err := peekDecodedChanges(ctx, conn, slot.name, endLSN, xids)
	if err != nil {
		data.BackupStatus = "备份失败：" + err.Error()
		logFn("解码逻辑日志失败：" + err.Error())
		return
	}
	if len(changes) == 0 {
		// 行数>0 却解码不到行事件：不静默——大概率环境问题（插件输出格式异常等）
		logFn("解码范围内未发现本工单的行变更事件，回滚 SQL 为空；请检查 test_decoding 插件输出是否正常")
		data.BackupStatus = "备份失败（未解码到行变更）"
		return
	}
	provider := &tableMetaProvider{conn: conn, cache: map[string]tableMeta{}}
	stmts, skips := buildRollbackStatements(ctx, provider.metaFor, changes)

	for _, s := range skips {
		logFn("回滚生成受限：" + s)
	}
	rollbackSQL := strings.Join(stmts, ";\r\n")
	data.RollbackSQL = rollbackSQL
	data.BackupCostTime = utils.HumanfriendlyTimeUnit(time.Since(startTime))
	if len(skips) > 0 {
		logFn(fmt.Sprintf("生成回滚SQL完成（%d 条语句，%d 条变更因表配置受限无法覆盖，详见上方日志），耗时：%s",
			len(stmts), len(skips), data.BackupCostTime))
		data.BackupStatus = fmt.Sprintf("备份成功（部分覆盖：%d 条语句；%d 条变更受限）", len(stmts), len(skips))
	} else {
		logFn(fmt.Sprintf("生成回滚SQL成功（%d 条语句），耗时：%s", len(stmts), data.BackupCostTime))
		data.BackupStatus = "备份成功"
	}
}

// execInTx 在显式事务中执行单条语句并返回 (影响行数, 事务 xid 低 32 位)。
//
// 为什么要包显式事务：pgx 连接默认每条语句独立自动提交，这里显式 BEGIN 后
// 在事务内取 txid_current()，使「事务号」与「语句产生的行变更」严格绑定，
// 后续解码按事务号过滤即可精确排除其他会话的并发写入（对应 MySQL 侧的
// Connection ID 过滤）。语句失败立即 ROLLBACK，与既有「逐条执行、失败即停」
// 的语义完全一致。
func execInTx(ctx context.Context, conn *pgx.Conn, stmt string) (int64, uint32, error) {
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		return 0, 0, err
	}
	var txid int64
	if err := conn.QueryRow(ctx, "SELECT txid_current()").Scan(&txid); err != nil {
		_, _ = conn.Exec(ctx, "ROLLBACK")
		return 0, 0, err
	}
	tag, err := conn.Exec(ctx, stmt)
	if err != nil {
		_, _ = conn.Exec(ctx, "ROLLBACK")
		return 0, 0, err
	}
	if _, err := conn.Exec(ctx, "COMMIT"); err != nil {
		_, _ = conn.Exec(ctx, "ROLLBACK")
		return 0, 0, err
	}
	// 逻辑解码输出的 xid 是 32 位；txid_current() 是 64 位（含 epoch 高位）。
	// 工单执行是秒级窗口，不存在回绕歧义，取低 32 位即可与解码结果对齐。
	return tag.RowsAffected(), uint32(txid & 0xFFFFFFFF), nil
}

// parseDatabaseAndSchema 拆分「database.schema」资源路径（与 extended.go 同口径）。
func parseDatabaseAndSchema(resourcePath string) (string, string, error) {
	parts := strings.SplitN(strings.TrimSpace(resourcePath), ".", 2)
	if len(parts) != 2 {
		return parts[0], "", nil
	}
	return parts[0], parts[1], nil
}
