package services

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/pkg/secret"
)

// ============================================================================
// 负载快照采集器：对单个 MySQL / PG 实例执行一批诊断 SQL，产出分章节 payload。
//
// 设计约定：
//   1. 两点采样 + 差分（AWR 式）：基线（captureLoadSnapshotBaseline）与终点
//      （captureLoadSnapshotEnd）各采一轮，计数器按 Δvalue/Δt 求速率。
//   2. 能力探测显式降级：探测失败 / 权限不足 / 扩展缺失的章节标 unsupported + 原因，
//      绝不给 0 冒充数据（与监控指标 quality 口径一致）。
//   3. 版本分支收敛在采集层：MySQL 5.7 innodb_lock_waits vs 8.0 data_lock_waits、
//      PG pg_stat_wal(14+) vs LSN 差分、pg_stat_statements 列名 13 前后不同。
// ============================================================================

// snapQuerier 统一 MySQL / PG 的「执行一条 SQL 返回行」抽象，值一律转字符串。
type snapQuerier interface {
	query(sql string) ([]map[string]string, error)
}

type snapshotSection struct {
	Status string         `json:"status"` // ok / unsupported
	Reason string         `json:"reason,omitempty"`
	Data   map[string]any `json:"data,omitempty"`
}

func snapSectionOK(data map[string]any) *snapshotSection {
	return &snapshotSection{Status: "ok", Data: data}
}

func snapSectionUnsupported(reason string) *snapshotSection {
	return &snapshotSection{Status: "unsupported", Reason: reason}
}

func snapRowsToMap(rows []map[string]string, keyCol, valCol string) map[string]string {
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row[keyCol]] = row[valCol]
	}
	return out
}

func snapF(m map[string]string, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok || v == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// snapDelta 计数器差分：(end - start) / seconds。任一端缺失时返回 nil（不造数据）。
func snapDelta(end, start map[string]string, key string, seconds float64) *float64 {
	e, eok := snapF(end, key)
	s, sok := snapF(start, key)
	if !eok || !sok || seconds <= 0 {
		return nil
	}
	v := (e - s) / seconds
	return &v
}

func snapStr(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

func snapErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// snapNum 单值取数：可解析返回 float64，否则 nil（缺数据时绝不造 0）。
func snapNum(m map[string]string, key string) any {
	if v, ok := snapF(m, key); ok {
		return v
	}
	return nil
}

// ---------------------------------------------------------------------------
// 连接层
// ---------------------------------------------------------------------------

// snapMySQLQ 走「会话」而不是 dao.DB：
// 一次负载快照要跑十几条 SQL（SHOW STATUS / VARIABLES / PFS digest / 表统计…），
// 而 dao.DB.Query 的语义是"一条 SQL = 一次 sql.Open + 握手认证 + Close"——
// 一次快照就会在业务库上留下十几条连接。偏偏负载快照是"实例已经扛不住了才去看"
// 的功能，这时候再往上叠十几条连接是最坏时机。
//
// cancel 由会话自己持有：见 connectLoadSnapshot 里对 `_ = cancel` 的说明。
type snapMySQLQ struct {
	session *dao.MySQLSession
	cancel  context.CancelFunc
}

func (a *snapMySQLQ) query(sql string) ([]map[string]string, error) {
	_, rows, err := a.session.Query(sql)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, nil
	}
	out := make([]map[string]string, 0, len(*rows))
	for _, item := range *rows {
		converted := make(map[string]string, len(item))
		for k, v := range item {
			converted[k] = strings.TrimSpace(toString(v))
		}
		out = append(out, converted)
	}
	return out, nil
}

type snapPgQ struct {
	conn *pgx.Conn
	ctx  context.Context
}

func (a *snapPgQ) query(sql string) ([]map[string]string, error) {
	rows, err := a.conn.Query(a.ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]string, 0, 16)
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		item := make(map[string]string, len(values))
		for i, fd := range rows.FieldDescriptions() {
			item[string(fd.Name)] = strings.TrimSpace(toString(values[i]))
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// loadSnapshotQueryTimeout 快照查询预算（整个快照会话共用一次，不是每条 SQL 一次）。
const loadSnapshotQueryTimeout = 120 * time.Second

// connectLoadSnapshot 按实例配置建连接（复用采集引擎的解密与连接参数口径）。
func connectLoadSnapshot(ctx context.Context, row monitorInstanceRow) (snapQuerier, error) {
	password, err := secret.Decrypt(row.Password)
	if err != nil {
		return nil, fmt.Errorf("实例密码解密失败")
	}
	engine := resolveEngine(row.DbType)
	switch engine {
	case "postgresql", "kingbase", "kingbasees":
		// 金仓兼容 PG 协议，负载快照的基线采集（pg_stat_database / pg_stat_statements / WAL LSN）同一套 SQL
		cfg, err := parseExtraConfig(row.ExtraConfig)
		if err != nil {
			return nil, err
		}
		database := getStringConfig(cfg, "database", "postgres")
		sslMode := getStringConfig(cfg, "sslmode", "disable")
		connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
			url.QueryEscape(row.User), url.QueryEscape(password),
			row.Hostname, row.Port, url.PathEscape(database), url.QueryEscape(sslMode))
		conn, err := pgx.Connect(ctx, connStr)
		if err != nil {
			return nil, fmt.Errorf("PostgreSQL 连接失败：%s", err.Error())
		}
		return &snapPgQ{conn: conn, ctx: ctx}, nil
	case "mysql", "tidb":
		// 快照里的 SQL 普遍偏重（PFS digest 汇总、information_schema 全表统计），
		// 单条给到 120 秒，与改造前 dao.DB 上挂的超时一致，只是改成"整个会话共用一次"。
		//
		// cancel 必须存下来由 closeLoadSnapshotConn 调用：老代码写的是 `_ = cancel`，
		// 结果这个 120 秒计时器永远只能靠超时自己到期回收（context 泄漏），
		// 快照提前失败时也无法取消正在跑的重查询。
		childCtx, cancel := context.WithTimeout(ctx, loadSnapshotQueryTimeout)
		session, err := dao.OpenMySQLSession(dao.SessionConfig{
			User:     row.User,
			Password: password,
			Host:     row.Hostname,
			Port:     row.Port,
			Params:   map[string]string{"charset": "utf8mb4"},
			Ctx:      childCtx,
		})
		if err != nil {
			// sql.Open 只在 DSN 非法时报错，这里兜的正是这种情况，cancel 必须放掉。
			cancel()
			return nil, fmt.Errorf("MySQL 连接失败：%s", err.Error())
		}
		return &snapMySQLQ{session: session, cancel: cancel}, nil
	default:
		return nil, fmt.Errorf("负载快照暂不支持该引擎：%s", row.DbType)
	}
}

// ---------------------------------------------------------------------------
// MySQL
// ---------------------------------------------------------------------------

const mysqlLoadStatusSQL = "SHOW GLOBAL STATUS"
const mysqlLoadVariablesSQL = "SHOW VARIABLES"
const mysqlLoadDigestSQL = `
SELECT DIGEST, DIGEST_TEXT, SCHEMA_NAME, COUNT_STAR, SUM_TIMER_WAIT, SUM_LOCK_TIME,
       SUM_ROWS_SENT, SUM_ROWS_EXAMINED
FROM performance_schema.events_statements_summary_by_digest
WHERE SCHEMA_NAME IS NULL OR SCHEMA_NAME NOT IN ('mysql','performance_schema','information_schema','sys')
ORDER BY SUM_TIMER_WAIT DESC LIMIT 300`

const mysqlLoadLongTrxSQL = `
SELECT trx_id, trx_started, TIMESTAMPDIFF(SECOND, trx_started, NOW()) AS run_secs,
       trx_state, trx_rows_locked, trx_rows_modified, trx_query
FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 10`

const mysqlLoadLockWaits57SQL = `
SELECT r.trx_mysql_thread_id AS waiting_thread, LEFT(r.trx_query, 200) AS waiting_query,
       b.trx_mysql_thread_id AS blocking_thread, LEFT(b.trx_query, 200) AS blocking_query,
       TIMESTAMPDIFF(SECOND, r.trx_wait_started, NOW()) AS wait_secs
FROM information_schema.innodb_lock_waits w
JOIN information_schema.innodb_trx b ON b.trx_id = w.blocking_trx_id
JOIN information_schema.innodb_trx r ON r.trx_id = w.requesting_trx_id`

const mysqlLoadLockWaits80SQL = `
SELECT r.trx_mysql_thread_id AS waiting_thread, LEFT(r.trx_query, 200) AS waiting_query,
       b.trx_mysql_thread_id AS blocking_thread, LEFT(b.trx_query, 200) AS blocking_query,
       TIMESTAMPDIFF(SECOND, r.trx_wait_started, NOW()) AS wait_secs
FROM performance_schema.data_lock_waits w
JOIN information_schema.innodb_trx b ON b.trx_id = w.BLOCKING_ENGINE_TRANSACTION_ID
JOIN information_schema.innodb_trx r ON r.trx_id = w.REQUESTING_ENGINE_TRANSACTION_ID`

const mysqlLoadProcesslistSQL = `
SELECT user, SUBSTRING_INDEX(host, ':', 1) AS host, COUNT(*) AS conns
FROM information_schema.processlist GROUP BY user, host ORDER BY conns DESC LIMIT 20`

const mysqlLoadUsersSQL = "SELECT user, host FROM mysql.user ORDER BY user, host LIMIT 100"

const mysqlLoadDatabasesSQL = `
SELECT table_schema AS name, SUM(data_length + index_length) AS size_bytes, COUNT(*) AS table_count
FROM information_schema.tables GROUP BY table_schema ORDER BY size_bytes DESC LIMIT 50`

const mysqlLoadTopTablesSQL = `
SELECT table_schema AS table_schema, table_name AS table_name, engine AS engine,
       table_rows AS table_rows, data_length AS data_length, index_length AS index_length
FROM information_schema.tables
WHERE table_type = 'BASE TABLE'
  AND table_schema NOT IN ('mysql','performance_schema','information_schema','sys')
ORDER BY data_length + index_length DESC LIMIT 50`

const mysqlLoadUnusedIndexesSQL = `
SELECT object_schema, object_name, index_name
FROM performance_schema.table_io_waits_summary_by_index_usage
WHERE index_name IS NOT NULL
  AND object_schema NOT IN ('mysql','performance_schema','information_schema','sys')
  AND count_star = 0 LIMIT 20`

// mysqlLoadParamKeys 参数章节展示的关键参数（5.7/8.0 名字差异在读取时互补）。
var mysqlLoadParamKeys = []string{
	"max_connections", "innodb_buffer_pool_size", "innodb_page_size", "long_query_time",
	"innodb_flush_log_at_trx_commit", "sync_binlog", "binlog_format", "binlog_row_image",
	"character_set_server", "collation_server", "time_zone", "slow_query_log", "log_bin",
	"max_allowed_packet", "table_open_cache", "thread_cache_size", "tmp_table_size",
	"innodb_io_capacity", "innodb_flush_method", "innodb_print_all_deadlocks",
}

// mysqlBaselineCounters 参与差分的 SHOW GLOBAL STATUS 计数器清单。
var mysqlBaselineCounters = []string{
	"Queries", "Questions", "Com_commit", "Com_rollback", "Slow_queries",
	"Innodb_row_lock_waits", "Innodb_row_lock_time", "Innodb_deadlocks",
	"Innodb_buffer_pool_read_requests", "Innodb_buffer_pool_reads",
	"Innodb_buffer_pool_pages_total", "Innodb_buffer_pool_pages_dirty",
	"Innodb_buffer_pool_pages_data", "Innodb_buffer_pool_pages_free",
	"Created_tmp_disk_tables", "Threads_connected", "Threads_running", "Max_used_connections",
}

// captureLoadSnapshotBaseline 基线采样：状态计数器 + digest/statements 快照（供终点差分）。
// 返回 (基线原始数据, 版本号, 错误)。
func captureLoadSnapshotBaseline(q snapQuerier, engine string) (map[string]any, string, error) {
	switch engine {
	case "mysql", "tidb":
		statusRows, err := q.query(mysqlLoadStatusSQL)
		if err != nil {
			return nil, "", fmt.Errorf("SHOW GLOBAL STATUS 失败：%s", err.Error())
		}
		status := snapRowsToMap(statusRows, "Variable_name", "Value")
		varRows, err := q.query(mysqlLoadVariablesSQL)
		if err != nil {
			return nil, "", fmt.Errorf("SHOW VARIABLES 失败：%s", err.Error())
		}
		variables := snapRowsToMap(varRows, "Variable_name", "Value")
		counters := make(map[string]float64, len(mysqlBaselineCounters))
		for _, key := range mysqlBaselineCounters {
			if v, ok := snapF(status, key); ok {
				counters[key] = v
			}
		}
		baseline := map[string]any{"counters": counters}
		// digest 基线：PFS 未开启 / 权限不足时，SQL Top 章节在终点降级
		if digestRows, err := q.query(mysqlLoadDigestSQL); err == nil {
			baseline["digests"] = digestRows
		}
		return baseline, snapStr(variables, "version", ""), nil
	case "postgresql", "kingbase", "kingbasees":
		statsRows, err := q.query(pgLoadStatsSQL)
		if err != nil {
			return nil, "", fmt.Errorf("pg_stat_database 查询失败：%s", err.Error())
		}
		baseline := map[string]any{"stats": statsRows}
		verNum := 0
		if verRows, err := q.query(pgLoadVersionSQL); err == nil && len(verRows) > 0 {
			if v, ok := snapF(verRows[0], "ver_num"); ok {
				verNum = int(v)
			}
		}
		// pg_stat_statements 基线（未装扩展时 SQL Top 章节在终点降级）
		if pssRows, err := q.query(pgLoadStatementsSQL(verNum)); err == nil {
			baseline["statements"] = pssRows
		}
		// WAL：LSN 基线（PG14+ 另用 pg_stat_wal 差分，这里统一兜底）
		if lsnRows, err := q.query("SELECT pg_current_wal_lsn()::text AS lsn"); err == nil && len(lsnRows) > 0 {
			baseline["wal_lsn"] = lsnRows[0]["lsn"]
		}
		baseline["ver_num"] = verNum
		return baseline, strconv.Itoa(verNum), nil
	}
	return nil, "", fmt.Errorf("未知引擎：%s", engine)
}

// captureLoadSnapshotEnd 终点采样：端点快照章节 + 基线差分，产出完整 sections。
func captureLoadSnapshotEnd(q snapQuerier, engine string, baseline map[string]any, windowSeconds int, verNum int) (map[string]any, error) {
	switch engine {
	case "mysql", "tidb":
		return mysqlLoadSnapshotEnd(q, baseline, windowSeconds)
	case "postgresql", "kingbase", "kingbasees":
		return pgLoadSnapshotEnd(q.(*snapPgQ), baseline, windowSeconds, verNum)
	}
	return nil, fmt.Errorf("未知引擎：%s", engine)
}

func mysqlCountersString(counters map[string]any) map[string]string {
	out := make(map[string]string, len(counters))
	for k, v := range counters {
		if f, ok := v.(float64); ok {
			out[k] = strconv.FormatFloat(f, 'f', -1, 64)
		}
	}
	return out
}

func mysqlLoadSnapshotEnd(q snapQuerier, baseline map[string]any, windowSeconds int) (map[string]any, error) {
	statusRows, err := q.query(mysqlLoadStatusSQL)
	if err != nil {
		return nil, fmt.Errorf("SHOW GLOBAL STATUS 失败：%s", err.Error())
	}
	status := snapRowsToMap(statusRows, "Variable_name", "Value")
	varRows, err := q.query(mysqlLoadVariablesSQL)
	if err != nil {
		return nil, fmt.Errorf("SHOW VARIABLES 失败：%s", err.Error())
	}
	variables := snapRowsToMap(varRows, "Variable_name", "Value")

	endCounters := make(map[string]any, len(mysqlBaselineCounters))
	for _, key := range mysqlBaselineCounters {
		if v, ok := snapF(status, key); ok {
			endCounters[key] = v
		}
	}
	endStr := mysqlCountersString(endCounters)
	baseStr := mysqlCountersString(map[string]any{})
	if baseRaw, ok := baseline["counters"].(map[string]any); ok {
		baseStr = mysqlCountersString(baseRaw)
	}
	seconds := float64(windowSeconds)

	endDigestRows, digestErr := q.query(mysqlLoadDigestSQL)

	sections := map[string]any{
		"basics": snapSectionOK(map[string]any{
			"version":              snapStr(variables, "version", ""),
			"version_comment":      snapStr(variables, "version_comment", ""),
			"uptime_seconds":       snapNum(status, "Uptime"),
			"charset":              snapStr(variables, "character_set_server", ""),
			"collation":            snapStr(variables, "collation_server", ""),
			"time_zone":            snapStr(variables, "time_zone", ""),
			"threads_connected":    snapNum(status, "Threads_connected"),
			"threads_running":      snapNum(status, "Threads_running"),
			"max_used_connections": snapNum(status, "Max_used_connections"),
			"qps":                  pickFirstNonZero(pick(status, "Questions"), pick(status, "Queries")),
		}),
	}
	// 参数章节
	paramItems := make([]map[string]any, 0, len(mysqlLoadParamKeys)+2)
	for _, key := range mysqlLoadParamKeys {
		if v, ok := variables[key]; ok {
			paramItems = append(paramItems, map[string]any{"name": key, "value": v})
		}
	}
	for _, key := range []string{"transaction_isolation", "tx_isolation"} {
		if v, ok := variables[key]; ok {
			paramItems = append(paramItems, map[string]any{"name": key, "value": v})
		}
	}
	sections["params"] = snapSectionOK(map[string]any{"items": paramItems})

	// SQL Top：digest 差分
	if digestErr != nil || baseDigestsMissing(baseline) {
		sections["sql_top"] = snapSectionUnsupported("performance_schema 摘要表不可读（未开启 PFS 或权限不足），SQL Top 无法差分")
	} else {
		sections["sql_top"] = mysqlDiffDigests(baseline["digests"].([]map[string]string), endDigestRows, seconds)
	}

	// 慢查询计数差分
	sections["slow"] = snapSectionOK(map[string]any{
		"slow_total": snapDelta(endStr, baseStr, "Slow_queries", seconds),
	})

	// 锁：计数差分 + 阻塞链 + 长事务
	lockData := map[string]any{
		"lock_waits":    snapDelta(endStr, baseStr, "Innodb_row_lock_waits", seconds),
		"lock_time_sec": snapDelta(endStr, baseStr, "Innodb_row_lock_time", seconds),
		"deadlocks":     snapDelta(endStr, baseStr, "Innodb_deadlocks", seconds),
	}
	// 阻塞链：8.0 视图优先，失败回落 5.7 视图（版本分支收敛点）
	waits, waitErr := q.query(mysqlLoadLockWaits80SQL)
	if waitErr != nil {
		waits, waitErr = q.query(mysqlLoadLockWaits57SQL)
	}
	if waitErr == nil {
		lockData["blocking"] = waits
	} else {
		lockData["blocking_error"] = "锁等待视图不可读：" + waitErr.Error()
	}
	if longTrx, err := q.query(mysqlLoadLongTrxSQL); err == nil {
		lockData["long_trx"] = longTrx
	}
	sections["locks"] = snapSectionOK(lockData)

	// 内存：buffer pool
	readReq := snapDelta(endStr, baseStr, "Innodb_buffer_pool_read_requests", seconds)
	physReads := snapDelta(endStr, baseStr, "Innodb_buffer_pool_reads", seconds)
	var hitRatio *float64
	if readReq != nil && physReads != nil && *readReq > 0 {
		v := (1 - *physReads / *readReq) * 100
		hitRatio = &v
	}
	sections["memory"] = snapSectionOK(map[string]any{
		"buffer_pool_size": snapStr(variables, "innodb_buffer_pool_size", ""),
		"hit_ratio":        hitRatio,
		"pages_total":      snapNum(status, "Innodb_buffer_pool_pages_total"),
		"pages_dirty":      snapNum(status, "Innodb_buffer_pool_pages_dirty"),
		"pages_data":       snapNum(status, "Innodb_buffer_pool_pages_data"),
		"pages_free":       snapNum(status, "Innodb_buffer_pool_pages_free"),
		"tmp_disk_tables":  snapDelta(endStr, baseStr, "Created_tmp_disk_tables", seconds),
	})

	// 用户
	if users, err := q.query(mysqlLoadUsersSQL); err != nil {
		sections["users"] = snapSectionUnsupported("mysql.user 不可读（权限不足），账号面盘点降级：" + err.Error())
	} else {
		userData := map[string]any{"users": users}
		if conns, err := q.query(mysqlLoadProcesslistSQL); err == nil {
			userData["connections"] = conns
		}
		sections["users"] = snapSectionOK(userData)
	}

	// 库大小
	if rows, err := q.query(mysqlLoadDatabasesSQL); err != nil {
		sections["databases"] = snapSectionUnsupported("information_schema.tables 聚合失败：" + err.Error())
	} else {
		sections["databases"] = snapSectionOK(map[string]any{"items": rows})
	}

	// Top 表
	if rows, err := q.query(mysqlLoadTopTablesSQL); err != nil {
		sections["tables"] = snapSectionUnsupported("information_schema.tables 查询失败：" + err.Error())
	} else {
		sections["tables"] = snapSectionOK(map[string]any{"items": rows})
	}

	// 索引
	if rows, err := q.query(mysqlLoadUnusedIndexesSQL); err != nil {
		sections["indexes"] = snapSectionUnsupported("performance_schema 索引使用统计不可读（未开启 PFS 或权限不足）")
	} else {
		sections["indexes"] = snapSectionOK(map[string]any{"unused": rows})
	}

	// binlog
	binlogData := map[string]any{
		"log_bin":       snapStr(variables, "log_bin", ""),
		"binlog_format": snapStr(variables, "binlog_format", ""),
		"row_image":     snapStr(variables, "binlog_row_image", ""),
		"sync_binlog":   snapStr(variables, "sync_binlog", ""),
	}
	if v, ok := variables["binlog_expire_logs_seconds"]; ok {
		binlogData["expire"] = v + " s"
	} else if v, ok := variables["expire_logs_days"]; ok {
		binlogData["expire"] = v + " 天"
	}
	if files, err := q.query("SHOW BINARY LOGS"); err == nil {
		binlogData["files"] = files
		total := 0.0
		for _, f := range files {
			if size, ok := snapF(f, "File_size"); ok {
				total += size
			}
		}
		binlogData["total_bytes"] = total
	} else {
		binlogData["files_error"] = "SHOW BINARY LOGS 失败（可能无 REPLICATION CLIENT 权限）"
	}
	sections["binlog"] = snapSectionOK(binlogData)

	return sections, nil
}

func baseDigestsMissing(baseline map[string]any) bool {
	rows, ok := baseline["digests"].([]map[string]string)
	return !ok || rows == nil
}

func mysqlDiffDigests(baseRows, endRows []map[string]string, seconds float64) *snapshotSection {
	baseByID := map[string]map[string]string{}
	for _, row := range baseRows {
		baseByID[snapStr(row, "DIGEST", "")] = row
	}
	type sqlItem struct {
		DigestText   string   `json:"digest_text"`
		Schema       string   `json:"schema"`
		Count        *float64 `json:"count"`
		TimeSec      *float64 `json:"time_sec"`
		AvgMs        *float64 `json:"avg_ms"`
		RowsExamined *float64 `json:"rows_examined"`
	}
	items := make([]sqlItem, 0, 20)
	for _, row := range endRows {
		digest := snapStr(row, "DIGEST", "")
		base := baseByID[digest]
		count := snapDelta(row, base, "COUNT_STAR", seconds)
		if count == nil || *count <= 0 {
			continue // 窗口内没执行过的指纹不参与排名
		}
		item := sqlItem{
			DigestText:   snapStr(row, "DIGEST_TEXT", ""),
			Schema:       snapStr(row, "SCHEMA_NAME", ""),
			Count:        count,
			RowsExamined: snapDelta(row, base, "SUM_ROWS_EXAMINED", seconds),
		}
		// SUM_TIMER_WAIT 单位皮秒：差分后除以 1e12 得秒
		if e, eok := snapF(row, "SUM_TIMER_WAIT"); eok {
			if s, sok := snapF(base, "SUM_TIMER_WAIT"); sok {
				v := (e - s) / 1e12
				avg := v * 1000 / *count
				item.TimeSec = &v
				item.AvgMs = &avg
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		ti, tj := 0.0, 0.0
		if items[i].TimeSec != nil {
			ti = *items[i].TimeSec
		}
		if items[j].TimeSec != nil {
			tj = *items[j].TimeSec
		}
		return ti > tj
	})
	if len(items) > 20 {
		items = items[:20]
	}
	return snapSectionOK(map[string]any{"items": items})
}

// pick 返回状态变量值（缺失为 0），供 pickFirstNonZero 用。
func pick(m map[string]string, key string) float64 {
	v, _ := snapF(m, key)
	return v
}

// ---------------------------------------------------------------------------
// PostgreSQL
// ---------------------------------------------------------------------------

// pgLoadStatsSQL 当前库的 pg_stat_database 计数器（参与差分的核心行）。
const pgLoadStatsSQL = `
SELECT xact_commit, xact_rollback, blks_read, blks_hit, tup_returned, tup_fetched,
       tup_inserted, tup_updated, tup_deleted, deadlocks, temp_files, temp_bytes, conflicts
FROM pg_stat_database WHERE datname = current_database()`

// pgLoadStatementsSQL pg_stat_statements 查询（13 前后列名不同：total_time → total_exec_time）。
func pgLoadStatementsSQL(verNum int) string {
	timeCol := "total_time"
	if verNum >= 130000 {
		timeCol = "total_exec_time"
	}
	return fmt.Sprintf(`
SELECT queryid::text AS queryid, LEFT(query, 200) AS query, calls, %s,
       rows, shared_blks_read, shared_blks_hit, temp_blks_written
FROM pg_stat_statements ORDER BY %s DESC LIMIT 300`, timeCol, timeCol)
}

const pgLoadVersionSQL = `
SELECT version() AS version, current_setting('server_version_num')::text AS ver_num,
       pg_postmaster_start_time()::text AS start_time,
       current_setting('server_encoding') AS encoding`

const pgLoadWaitEventsSQL = `
SELECT wait_event_type, wait_event, COUNT(*) AS cnt
FROM pg_stat_activity WHERE wait_event IS NOT NULL GROUP BY 1, 2 ORDER BY cnt DESC LIMIT 15`

const pgLoadBlockingSQL = `
SELECT blocked.pid AS waiting_pid, blocked.usename AS waiting_user,
       LEFT(blocked.query, 200) AS waiting_query,
       blocking.pid AS blocking_pid, blocking.usename AS blocking_user,
       LEFT(blocking.query, 200) AS blocking_query,
       blocked.wait_event_type, blocked.wait_event,
       EXTRACT(EPOCH FROM (now() - blocked.query_start))::bigint AS wait_secs
FROM pg_stat_activity blocked
JOIN pg_locks bl ON bl.pid = blocked.pid AND NOT bl.granted
JOIN pg_locks gl ON gl.locktype = bl.locktype
  AND gl.relation IS NOT DISTINCT FROM bl.relation
  AND gl.page IS NOT DISTINCT FROM bl.page
  AND gl.tuple IS NOT DISTINCT FROM bl.tuple
  AND gl.virtualxid IS NOT DISTINCT FROM bl.virtualxid
  AND gl.transactionid IS NOT DISTINCT FROM bl.transactionid
  AND gl.classid IS NOT DISTINCT FROM bl.classid
  AND gl.objid IS NOT DISTINCT FROM bl.objid
  AND gl.objsubid IS NOT DISTINCT FROM bl.objsubid
  AND gl.granted
JOIN pg_stat_activity blocking ON blocking.pid = gl.pid`

const pgLoadUsersSQL = `
SELECT rolname, rolsuper, rolreplication, rolcanlogin, rolconnlimit
FROM pg_roles WHERE rolcanlogin ORDER BY rolname LIMIT 100`

const pgLoadDatabasesSQL = `
SELECT datname AS name, pg_database_size(datname) AS size_bytes
FROM pg_database WHERE datallowconn ORDER BY size_bytes DESC LIMIT 50`

const pgLoadTopTablesSQL = `
SELECT schemaname, relname, n_live_tup,
       pg_total_relation_size(schemaname || '.' || relname) AS size_bytes
FROM pg_stat_user_tables ORDER BY size_bytes DESC LIMIT 50`

const pgLoadUnusedIndexesSQL = `
SELECT schemaname, relname, indexrelname, idx_scan,
       pg_relation_size(indexrelid) AS size_bytes
FROM pg_stat_user_indexes WHERE idx_scan = 0 LIMIT 20`

const pgLoadSlotsSQL = `
SELECT slot_name, active, slot_type,
       pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn) AS retained_bytes
FROM pg_replication_slots`

const pgLoadParamsSQL = `
SELECT name, setting, COALESCE(unit, '') AS unit
FROM pg_settings WHERE name = ANY(ARRAY[
  'max_connections','shared_buffers','work_mem','maintenance_work_mem',
  'effective_cache_size','wal_level','archive_mode','max_wal_size',
  'checkpoint_timeout','checkpoint_completion_target','autovacuum',
  'autovacuum_max_workers','log_min_duration_statement','random_page_cost',
  'temp_buffers','default_statistics_target','max_replication_slots','synchronous_commit'])`

func pgLoadSnapshotEnd(q *snapPgQ, baseline map[string]any, windowSeconds, verNum int) (map[string]any, error) {
	seconds := float64(windowSeconds)

	statsRows, err := q.query(pgLoadStatsSQL)
	if err != nil {
		return nil, fmt.Errorf("pg_stat_database 查询失败：%s", err.Error())
	}
	var baseStats map[string]string
	if baseRows, ok := baseline["stats"].([]map[string]string); ok && len(baseRows) > 0 {
		baseStats = baseRows[0]
	}
	endStats := map[string]string{}
	if len(statsRows) > 0 {
		endStats = statsRows[0]
	}

	sections := map[string]any{}

	// 基本信息
	if verRows, err := q.query(pgLoadVersionSQL); err == nil && len(verRows) > 0 {
		sections["basics"] = snapSectionOK(map[string]any{
			"version":    snapStr(verRows[0], "version", ""),
			"start_time": snapStr(verRows[0], "start_time", ""),
			"encoding":   snapStr(verRows[0], "encoding", ""),
			"ver_num":    verNum,
		})
	} else {
		sections["basics"] = snapSectionUnsupported("版本信息查询失败")
	}

	// 参数
	if rows, err := q.query(pgLoadParamsSQL); err != nil {
		sections["params"] = snapSectionUnsupported("pg_settings 查询失败：" + snapErrString(err))
	} else {
		items := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			value := snapStr(row, "setting", "")
			if unit := snapStr(row, "unit", ""); unit != "" {
				value += " " + unit
			}
			items = append(items, map[string]any{"name": snapStr(row, "name", ""), "value": value})
		}
		sections["params"] = snapSectionOK(map[string]any{"items": items})
	}

	// SQL Top：pg_stat_statements 差分（未装扩展则整节降级）
	baseStmts, hasBase := baseline["statements"].([]map[string]string)
	endStmts, endErr := q.query(pgLoadStatementsSQL(verNum))
	if !hasBase || endErr != nil {
		sections["sql_top"] = snapSectionUnsupported("pg_stat_statements 扩展未安装或权限不足，SQL Top 无法差分")
	} else {
		sections["sql_top"] = pgDiffStatements(baseStmts, endStmts, seconds)
	}

	// 慢 SQL：PG 无全局慢查计数器，有扩展时给近似口径，否则显式降级
	if !hasBase || endErr != nil {
		sections["slow"] = snapSectionUnsupported("PG 无全局慢查计数器；安装 pg_stat_statements 后可用平均耗时近似排序")
	} else {
		sections["slow"] = snapSectionOK(map[string]any{"note": "PG 无全局慢查计数器，本节基于 pg_stat_statements 差分提供执行统计参考"})
	}

	// 差分统计（锁 / 内存共用）
	diff := func(key string) *float64 {
		return snapDelta(endStats, baseStats, key, seconds)
	}

	// 锁
	lockData := map[string]any{"deadlocks": diff("deadlocks")}
	if waits, err := q.query(pgLoadBlockingSQL); err == nil {
		lockData["blocking"] = waits
	}
	if events, err := q.query(pgLoadWaitEventsSQL); err == nil {
		lockData["wait_events"] = events
	}
	sections["locks"] = snapSectionOK(lockData)

	// 内存
	memData := map[string]any{}
	hit := diff("blks_hit")
	read := diff("blks_read")
	if hit != nil && read != nil && *hit+*read > 0 {
		v := *hit / (*hit + *read) * 100
		memData["hit_ratio"] = v
	}
	memData["temp_bytes"] = diff("temp_bytes")
	memData["temp_files"] = diff("temp_files")
	if paramRows, err := q.query(pgLoadParamsSQL); err == nil {
		pmap := snapRowsToMap(paramRows, "name", "setting")
		memData["shared_buffers"] = snapStr(pmap, "shared_buffers", "")
		memData["work_mem"] = snapStr(pmap, "work_mem", "")
	}
	sections["memory"] = snapSectionOK(memData)

	// 用户
	if users, err := q.query(pgLoadUsersSQL); err != nil {
		sections["users"] = snapSectionUnsupported("pg_roles 不可读：" + snapErrString(err))
	} else {
		userData := map[string]any{"users": users}
		if conns, err := q.query("SELECT usename, COUNT(*) AS conns FROM pg_stat_activity GROUP BY usename ORDER BY conns DESC LIMIT 20"); err == nil {
			userData["connections"] = conns
		}
		sections["users"] = snapSectionOK(userData)
	}

	// 库大小
	if rows, err := q.query(pgLoadDatabasesSQL); err != nil {
		sections["databases"] = snapSectionUnsupported("pg_database_size 失败（权限不足）：" + snapErrString(err))
	} else {
		sections["databases"] = snapSectionOK(map[string]any{"items": rows})
	}

	// Top 表
	if rows, err := q.query(pgLoadTopTablesSQL); err != nil {
		sections["tables"] = snapSectionUnsupported("pg_stat_user_tables 查询失败：" + snapErrString(err))
	} else {
		sections["tables"] = snapSectionOK(map[string]any{"items": rows})
	}

	// 索引
	if rows, err := q.query(pgLoadUnusedIndexesSQL); err != nil {
		sections["indexes"] = snapSectionUnsupported("pg_stat_user_indexes 查询失败：" + snapErrString(err))
	} else {
		sections["indexes"] = snapSectionOK(map[string]any{"unused": rows})
	}

	// WAL（PG 对应 binlog 章节）
	walData := map[string]any{}
	if walRows, err := q.query("SELECT wal_records, wal_fpi, wal_bytes FROM pg_stat_wal"); err == nil && len(walRows) > 0 {
		if total, ok := snapF(walRows[0], "wal_bytes"); ok {
			walData["wal_bytes_total"] = total
		}
	} else if baseLSN, ok := baseline["wal_lsn"].(string); ok && baseLSN != "" {
		// 低版本 LSN 差分兜底：窗口内 WAL 生成字节速率
		if endRows, err := q.query("SELECT pg_current_wal_lsn()::text AS lsn"); err == nil && len(endRows) > 0 {
			if diffRows, err := q.query(fmt.Sprintf("SELECT pg_wal_lsn_diff('%s', '%s')::text AS bytes",
				endRows[0]["lsn"], baseLSN)); err == nil && len(diffRows) > 0 {
				if v, ok := snapF(diffRows[0], "bytes"); ok {
					walData["wal_bytes_rate"] = v / seconds
				}
			}
		}
	}
	if slots, err := q.query(pgLoadSlotsSQL); err == nil {
		walData["slots"] = slots
	}
	if paramRows, err := q.query(pgLoadParamsSQL); err == nil {
		pmap := snapRowsToMap(paramRows, "name", "setting")
		walData["wal_level"] = snapStr(pmap, "wal_level", "")
		walData["archive_mode"] = snapStr(pmap, "archive_mode", "")
	}
	sections["binlog"] = snapSectionOK(walData)

	return sections, nil
}

func pgDiffStatements(baseRows, endRows []map[string]string, seconds float64) *snapshotSection {
	baseByID := map[string]map[string]string{}
	for _, row := range baseRows {
		baseByID[snapStr(row, "queryid", snapStr(row, "query", ""))] = row
	}
	items := make([]map[string]any, 0, 20)
	for _, row := range endRows {
		key := snapStr(row, "queryid", snapStr(row, "query", ""))
		base := baseByID[key]
		count := snapDelta(row, base, "calls", seconds)
		if count == nil || *count <= 0 {
			continue
		}
		item := map[string]any{
			"digest_text": snapStr(row, "query", ""),
			"count":       count,
		}
		// pg_stat_statements 时间单位毫秒 → 秒
		if e, eok := snapF(row, "total_time"); eok {
			if s, sok := snapF(base, "total_time"); sok {
				v := (e - s) / 1000
				avg := v * 1000 / *count
				item["time_sec"] = v
				item["avg_ms"] = avg
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		vi, _ := items[i]["time_sec"].(float64)
		vj, _ := items[j]["time_sec"].(float64)
		return vi > vj
	})
	if len(items) > 20 {
		items = items[:20]
	}
	return snapSectionOK(map[string]any{"items": items})
}
