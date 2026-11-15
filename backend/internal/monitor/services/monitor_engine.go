package services

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/simajiong/jaysql/internal/common/sqlserverdsn"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"

	"database/sql"
	_ "github.com/microsoft/go-mssqldb"
	go_ora "github.com/sijms/go-ora/v2"
)

// ============================================================================
// 引擎层 / 资源层采样采集器
//
// 统一产出 []MetricSample（每个采样点自带 quality），取代原先"每个引擎各写一套
// MetricValue 拼装"的做法 —— 那套做法的结构性缺陷是 metricRate() 无条件标
// Available:true，导致"没采到"与"值就是 0"在页面上无法区分。
//
// 采集器接收 keys 参数：只跑配置里勾选启用的指标对应的查询。
// 这是 100 实例规模下控制开销的关键 —— 额外的往返（pg_stat_statements、
// pg_stat_replication、innodb_trx）能被真正省掉。
// ============================================================================

// sampleBuilder 便于按顺序追加采样点
type sampleBuilder struct {
	items []MetricSample
	src   string
}

func newSampleBuilder(source string) *sampleBuilder {
	return &sampleBuilder{items: make([]MetricSample, 0, 32), src: source}
}

func (b *sampleBuilder) ok(key string, value float64, unit string) {
	b.items = append(b.items, MetricSample{Key: key, Value: value, Unit: unit, Quality: QualityOK, Source: b.src})
}

func (b *sampleBuilder) counter(key string, value float64, unit string) {
	b.items = append(b.items, MetricSample{Key: key, Value: value, Unit: unit, Quality: QualityOK, Source: b.src, IsCounter: true})
}

func (b *sampleBuilder) add(key string, value float64, unit string, quality string, reason string) {
	b.items = append(b.items, MetricSample{Key: key, Value: value, Unit: unit, Quality: quality, Reason: reason, Source: b.src})
}

func (b *sampleBuilder) unsupported(key string, unit string, reason string) {
	b.add(key, 0, unit, QualityUnsupported, reason)
}

func (b *sampleBuilder) noData(key string, unit string, reason string) {
	b.add(key, 0, unit, QualityNoData, reason)
}

func (b *sampleBuilder) list() []MetricSample {
	return b.items
}

// wantMetric 判断某个指标是否在本轮采集范围内。
//
// 语义（与模板门控对齐，别改回"空集合 = 全采"）：
//   - keys == nil    → 全采。这是"字典不可用"的兜底，宁可多采也不能停摆；
//   - keys 为空切片  → 一个都不采。这是可表达的合法结论：模板与字典总闸取交集后
//     确实没有指标可采。若把空切片也当成全采，总闸就被绕过了。
//
// 两个调用方（采集器与卡片构建）拿到的 keys 都来自 resolveInstanceMetricKeys，
// 它保证"全采"时返回的是显式的字典全集，不会返回空切片。
func wantMetric(keys []string, metricKey string) bool {
	if keys == nil {
		return true
	}
	for _, item := range keys {
		if item == metricKey {
			return true
		}
	}
	return false
}

// collectEngineSamples 按引擎路由到具体采集器。
// 返回的 samples 已经带上 quality；err 只在"整体不可用"（连不上/认证失败）时非空。
func collectEngineSamples(ctx context.Context, row monitorInstanceRow, keys []string) ([]MetricSample, error) {
	switch resolveEngine(row.DbType) {
	case "mysql", "tidb", "doris":
		return collectMySQLSamples(ctx, row, keys)
	case "postgresql", "kingbase", "kingbasees":
		// 金仓兼容 PG 协议，pg_stat_* 系列视图同名同义，直接复用 PG 采集器
		return collectPostgresSamples(ctx, row, keys)
	case "clickhouse":
		return collectClickHouseSamples(ctx, row, keys)
	case "sqlserver":
		return collectSQLServerSamples(ctx, row, keys)
	case "oracle":
		return collectOracleSamples(ctx, row, keys)
	default:
		return nil, fmt.Errorf("当前数据库类型暂未实现运行态监控采集：%s", row.DbType)
	}
}

// ---------------------------------------------------------------------------
// MySQL / TiDB / Doris（兼容 MySQL 协议）
// ---------------------------------------------------------------------------

// mysqlStatusSQL 一次往返取回全部状态变量。
//
// 变量来源与口径统一为 SHOW GLOBAL STATUS：
//   - 累计型（Com_*、Select_scan、Created_tmp_disk_tables、Innodb_rows_*、Innodb_data_*、
//     Innodb_row_lock_time/waits、Innodb_log_*、Innodb_os_log_*、Sort_rows、Slow_queries 等）在代码里按
//     (后一次采样值 - 前一次采样值) / 采样间隔秒数 求每秒速率；
//   - 瞬时/存量型（Innodb_buffer_pool_pages_dirty、Innodb_num_open_files、
//     Open_files、Open_tables 等）直接上报原值。
//
// 变量不存在（低版本或非 InnoDB 引擎）时返回空行，调用方据此标 unsupported，
// 绝不当 0 用 —— 这是"没采到"与"值就是 0"的分界线。
const mysqlStatusSQL = `SHOW GLOBAL STATUS WHERE Variable_name IN (
	'Questions','Queries','Com_commit','Com_rollback','Innodb_rows_read','Innodb_rows_inserted',
	'Innodb_rows_updated','Innodb_rows_deleted','Bytes_received','Bytes_sent','Threads_connected',
	'Threads_running','Uptime','Innodb_buffer_pool_reads','Innodb_buffer_pool_read_requests',
	'Innodb_buffer_pool_pages_total','Innodb_buffer_pool_pages_free','Innodb_row_lock_current_waits',
	'Created_tmp_disk_tables','Created_tmp_tables',
	'Slow_queries','Aborted_connects','Aborted_clients','Binlog_bytes_written',
	'Innodb_os_log_written','Innodb_log_waits','Innodb_buffer_pool_pages_dirty',
	'Innodb_buffer_pool_pages_flushed','Innodb_buffer_pool_read_ahead',
	'Innodb_buffer_pool_read_ahead_evicted','Innodb_dblwr_writes',
	'Table_open_cache_hits','Table_open_cache_misses',
	'Rpl_semi_sync_source_status','Rpl_semi_sync_master_status','Rpl_semi_sync_replica_status',
	'Com_delete','Com_insert','Com_insert_select','Com_replace','Com_replace_select',
	'Com_select','Com_update','Select_scan',
	'Innodb_buffer_pool_write_requests','Innodb_data_read','Innodb_data_written',
	'Innodb_row_lock_time','Innodb_row_lock_time_avg','Innodb_row_lock_waits',
	'Innodb_data_fsyncs','Sort_rows',
	'Innodb_log_write_requests','Innodb_log_writes','Innodb_os_log_fsyncs',
	'Innodb_num_open_files','Open_files','Open_tables'
)`

// hasStatusVar 判断状态变量是否真的返回了行。
// 值缺省为 0 与"变量不存在"（低版本/未启用插件）必须分开 —— 后者要标 unsupported。
func hasStatusVar(values map[string]float64, names ...string) (float64, bool) {
	for _, name := range names {
		if value, ok := values[name]; ok {
			return value, true
		}
	}
	return 0, false
}

func collectMySQLSamples(ctx context.Context, row monitorInstanceRow, keys []string) ([]MetricSample, error) {
	password, err := secret.Decrypt(row.Password)
	if err != nil {
		return nil, fmt.Errorf("实例密码解密失败")
	}
	timeout := instanceTimeoutSeconds(row)
	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 整轮共用一个会话：改造前这里是一个 dao.DB，而 dao.DB.Query 的语义是
	// 「一次查询 = 一次 sql.Open + 一次完整握手认证 + Close」，一轮约 10 条 SQL
	// 就等于建连 10 次 —— 握手成本远大于查询本身，还会推高业务库的 Connections /
	// Aborted_connects（后者正是本平台在采集的指标，属于自我污染）。
	// 换成会话后一轮只建一次连接，后续所有查询复用它。
	session, err := dao.OpenMySQLSession(dao.SessionConfig{
		User:     row.User,
		Password: password,
		Host:     row.Hostname,
		Port:     row.Port,
		Params:   map[string]string{"charset": "utf8mb4"},
		Ctx:      childCtx,
	})
	if err != nil {
		return nil, fmt.Errorf("MySQL 指标采集失败：%s", err.Error())
	}
	defer session.Close()

	builder := newSampleBuilder("direct")
	_, statusRows, err := session.Query(mysqlStatusSQL)
	if err != nil {
		return nil, fmt.Errorf("MySQL 指标采集失败：%s", err.Error())
	}
	// num = 数值视图（速率/占比计算用），raw = 原文视图（ON/OFF 这类非数值状态用）。
	// 原来半同步状态要单独再查一次 SHOW GLOBAL STATUS 取原文，现在主查询这同一份结果
	// 里就有，省掉一次往返 —— 主查询的 WHERE 本来就包含那三个半同步变量。
	values := map[string]float64{}
	rawValues := map[string]string{}
	for _, item := range *statusRows {
		name := toString(item["Variable_name"])
		values[name] = toFloat64(item["Value"])
		rawValues[name] = toString(item["Value"])
	}
	uptime := values["Uptime"]

	// TiDB 只实现了极少数 MySQL 状态变量（Uptime / Ssl_* / server_id / ddl_schema_version /
	// tidb_gc_*，见官方 status-variables 文档）。Questions / Com_* / Threads_* / Slow_queries /
	// Bytes_* 在 TiDB 上根本不存在 —— 如果沿用 MySQL 口径直取 map，缺失会静默变成 0，
	// 页面画出来是一条贴着 0 走的"真实曲线"，比报错更有害。
	// 因此 TiDB 走独立取数：连接数来自 processlist，语句与事务来自 statements_summary。
	isTiDB := resolveEngine(row.DbType) == "tidb"

	// 连接上限：拿不到就标 unsupported，而不是当 0 用
	maxConnections := 0.0
	if _, maxRows, maxErr := session.Query(`SHOW VARIABLES LIKE 'max_connections'`); maxErr == nil && maxRows != nil && len(*maxRows) > 0 {
		maxConnections = toFloat64((*maxRows)[0]["Value"])
	}

	counters := map[string]float64{
		MetricPerfQPS:            pickFirstNonZero(values["Questions"], values["Queries"]),
		MetricPerfTPS:            values["Com_commit"] + values["Com_rollback"],
		MetricPerfReadRows:       values["Innodb_rows_read"],
		MetricPerfWriteRows:      values["Innodb_rows_inserted"] + values["Innodb_rows_updated"] + values["Innodb_rows_deleted"],
		MetricPerfReadBytes:      values["Bytes_sent"],
		MetricPerfWriteBytes:     values["Bytes_received"],
		MetricEngSlowQueries:     values["Slow_queries"],
		MetricEngAbortedConns:    values["Aborted_connects"] + values["Aborted_clients"],
		MetricKernRedoWriteRate:  values["Innodb_os_log_written"],
		MetricKernFlushRate:      values["Innodb_buffer_pool_pages_flushed"],
		MetricKernDBLWRWriteRate: values["Innodb_dblwr_writes"],

		// 语句执行次数（Com_*）与全表扫描：累计计数器，统一走 Δ/Δt
		MetricEngComDelete:        values["Com_delete"],
		MetricEngComInsert:        values["Com_insert"],
		MetricEngComInsertSelect:  values["Com_insert_select"],
		MetricEngComReplace:       values["Com_replace"],
		MetricEngComReplaceSelect: values["Com_replace_select"],
		MetricEngComSelect:        values["Com_select"],
		MetricEngComUpdate:        values["Com_update"],
		MetricEngSelectScan:       values["Select_scan"],

		// InnoDB 方言补充：Buffer Pool / Data IO / 行锁 / 行操作 / Redo（均为累计计数器）
		MetricKernBufferReadReq:    values["Innodb_buffer_pool_read_requests"],
		MetricKernBufferWriteReq:   values["Innodb_buffer_pool_write_requests"],
		MetricKernDataReadRate:     values["Innodb_data_read"],
		MetricKernDataWrittenRate:  values["Innodb_data_written"],
		MetricKernRowLockTimeRate:  values["Innodb_row_lock_time"],
		MetricKernRowLockWaitsRate: values["Innodb_row_lock_waits"],
		MetricKernDataFsyncsRate:   values["Innodb_data_fsyncs"],
		MetricKernSortRowsRate:     values["Sort_rows"],
		MetricKernRowsDeletedRate:  values["Innodb_rows_deleted"],
		MetricKernRowsInsertedRate: values["Innodb_rows_inserted"],
		MetricKernRowsUpdatedRate:  values["Innodb_rows_updated"],
		MetricKernLogWriteReqRate:  values["Innodb_log_write_requests"],
		MetricKernLogWritesRate:    values["Innodb_log_writes"],
		MetricKernOsLogFsyncsRate:  values["Innodb_os_log_fsyncs"],
	}
	if binlogBytes, ok := hasStatusVar(values, "Binlog_bytes_written"); ok {
		counters[MetricEngBinlogWALRate] = binlogBytes
	}

	// innodb_metrics 里的计数器（死锁/页分裂）需要先开开关才有值，
	// 取到就并入速率计算；取不到在产出采样时标 unsupported。
	//
	// 两个计数器合成**一次** IN 查询：改造前是各查一次、各建一条连接。
	// 仍然"按需才跑"：两个指标都不在模板里就完全不查。
	deadlockOK := false
	pageSplitOK := false
	wantedCounters := make([]string, 0, 2)
	if wantMetric(keys, MetricEngDeadlocks) {
		wantedCounters = append(wantedCounters, "lock_deadlocks")
	}
	if wantMetric(keys, MetricKernPageSplitRate) {
		wantedCounters = append(wantedCounters, "index_page_splits")
	}
	if len(wantedCounters) > 0 {
		metricValues := queryInnoDBMetricCounters(session, wantedCounters)
		if value, ok := metricValues["lock_deadlocks"]; ok {
			counters[MetricEngDeadlocks] = value
			deadlockOK = true
		}
		if value, ok := metricValues["index_page_splits"]; ok {
			counters[MetricKernPageSplitRate] = value
			pageSplitOK = true
		}
	}

	rates := directRateCache.compute(row.InstanceID, counters, uptime)

	unitOf := func(metricKey string) string {
		for _, item := range builtinMetricCatalog {
			if item.MetricKey == metricKey {
				return item.Unit
			}
		}
		return ""
	}

	// QPS / TPS / 协议流量：TiDB 无对应状态变量，由 appendTiDBEngineSamples 换源产出。
	if wantMetric(keys, MetricPerfQPS) && !isTiDB {
		builder.counter(MetricPerfQPS, rates[MetricPerfQPS], unitOf(MetricPerfQPS))
	}
	if wantMetric(keys, MetricPerfTPS) && !isTiDB {
		builder.counter(MetricPerfTPS, rates[MetricPerfTPS], unitOf(MetricPerfTPS))
	}
	if wantMetric(keys, MetricPerfReadRows) {
		builder.counter(MetricPerfReadRows, rates[MetricPerfReadRows], unitOf(MetricPerfReadRows))
	}
	if wantMetric(keys, MetricPerfWriteRows) {
		builder.counter(MetricPerfWriteRows, rates[MetricPerfWriteRows], unitOf(MetricPerfWriteRows))
	}
	// 协议收发字节：TiDB 不提供 Bytes_sent / Bytes_received，由 appendTiDBEngineSamples
	// 显式标 unsupported（不冒充 0）。
	if wantMetric(keys, MetricPerfReadBytes) && !isTiDB {
		builder.counter(MetricPerfReadBytes, rates[MetricPerfReadBytes], unitOf(MetricPerfReadBytes))
	}
	if wantMetric(keys, MetricPerfWriteBytes) && !isTiDB {
		builder.counter(MetricPerfWriteBytes, rates[MetricPerfWriteBytes], unitOf(MetricPerfWriteBytes))
	}

	if wantMetric(keys, MetricPerfCacheHitRatio) {
		readRequests := values["Innodb_buffer_pool_read_requests"]
		bufferMiss := values["Innodb_buffer_pool_reads"]
		if readRequests > 0 {
			builder.ok(MetricPerfCacheHitRatio, (1-(bufferMiss/readRequests))*100, "%")
		} else {
			builder.noData(MetricPerfCacheHitRatio, "%", "实例启动后尚无缓冲池读请求")
		}
	}
	if wantMetric(keys, MetricCapBufferPoolPct) {
		total := values["Innodb_buffer_pool_pages_total"]
		free := values["Innodb_buffer_pool_pages_free"]
		if total > 0 {
			builder.ok(MetricCapBufferPoolPct, (total-free)/total*100, "%")
		} else {
			builder.unsupported(MetricCapBufferPoolPct, "%", "非 InnoDB 引擎或缓冲池指标不可用")
		}
	}
	if wantMetric(keys, MetricCapTmpDiskPct) {
		createdTmp := values["Created_tmp_tables"]
		if createdTmp > 0 {
			builder.ok(MetricCapTmpDiskPct, values["Created_tmp_disk_tables"]/createdTmp*100, "%")
		} else {
			builder.noData(MetricCapTmpDiskPct, "%", "实例启动后尚未创建临时表")
		}
	}
	if wantMetric(keys, MetricPerfLocks) && !isTiDB {
		builder.ok(MetricPerfLocks, values["Innodb_row_lock_current_waits"], "count")
	}
	// 连接数 / 活跃连接：TiDB 无 Threads_connected / Threads_running，改由 processlist 统计。
	if wantMetric(keys, MetricPerfConnCount) && !isTiDB {
		builder.ok(MetricPerfConnCount, values["Threads_connected"], "count")
	}
	if wantMetric(keys, MetricPerfActiveConn) && !isTiDB {
		builder.ok(MetricPerfActiveConn, values["Threads_running"], "count")
	}
	if wantMetric(keys, MetricPerfMaxConn) {
		if maxConnections > 0 {
			builder.ok(MetricPerfMaxConn, maxConnections, "count")
		} else {
			builder.unsupported(MetricPerfMaxConn, "count", "无法读取 max_connections 变量")
		}
	}
	// 派生：连接使用率 / 活跃连接占比 —— 只有分子分母都可信时才产出，避免"除零得 0"。
	// TiDB 的 max_connections 默认 0（不限制连接数），占比无意义，由 TiDB 分支给出专属文案。
	if maxConnections > 0 {
		if wantMetric(keys, MetricCapConnUsedPct) {
			builder.ok(MetricCapConnUsedPct, values["Threads_running"]/maxConnections*100, "%")
		}
		if wantMetric(keys, MetricCapThreadUsedPct) {
			builder.ok(MetricCapThreadUsedPct, values["Threads_connected"]/maxConnections*100, "%")
		}
	} else if !isTiDB {
		if wantMetric(keys, MetricCapConnUsedPct) {
			builder.unsupported(MetricCapConnUsedPct, "%", "最大连接数不可用，无法计算占比")
		}
		if wantMetric(keys, MetricCapThreadUsedPct) {
			builder.unsupported(MetricCapThreadUsedPct, "%", "最大连接数不可用，无法计算占比")
		}
	}

	// 长事务（额外一次往返，按需才跑）
	// TiDB 没有 innodb_trx，改查 information_schema.tidb_trx —— 语义一致：
	// 长事务在 MySQL 上阻碍 purge，在 TiDB 上阻碍 MVCC GC safe point 推进。
	longTrxSQL := `SELECT COUNT(*) AS c FROM information_schema.innodb_trx
			WHERE TIME_TO_SEC(TIMEDIFF(NOW(), trx_started)) > 60`
	if resolveEngine(row.DbType) == "tidb" {
		longTrxSQL = `SELECT COUNT(*) AS c FROM information_schema.tidb_trx
			WHERE TIME_TO_SEC(TIMEDIFF(NOW(6), start_time)) > 60`
	}
	if wantMetric(keys, MetricPerfLongTrx) {
		if _, rows, longErr := session.Query(longTrxSQL); longErr == nil && rows != nil && len(*rows) > 0 {
			builder.ok(MetricPerfLongTrx, toFloat64((*rows)[0]["c"]), "count")
		} else if longErr != nil {
			builder.add(MetricPerfLongTrx, 0, "count", QualityError, "长事务查询失败："+trimError(longErr))
		}
	}
	if wantMetric(keys, MetricRiskNoPKTables) {
		if _, rows, pkErr := session.Query(`SELECT COUNT(*) AS c FROM information_schema.tables t
			WHERE t.table_schema NOT IN ('mysql','information_schema','performance_schema','sys')
			  AND NOT EXISTS (
				SELECT 1 FROM information_schema.statistics s
				WHERE s.table_schema = t.table_schema AND s.table_name = t.table_name AND s.index_name = 'PRIMARY'
			  )`); pkErr == nil && rows != nil && len(*rows) > 0 {
			builder.ok(MetricRiskNoPKTables, toFloat64((*rows)[0]["c"]), "count")
		} else if pkErr != nil {
			builder.add(MetricRiskNoPKTables, 0, "count", QualityError, "无主键表统计失败："+trimError(pkErr))
		}
	}

	appendMySQLExtendedSamples(builder, session, values, rawValues, rates, keys, deadlockOK, pageSplitOK, isTiDB)
	appendMySQLStatusDerivedSamples(builder, values, rates, keys, isTiDB)
	appendMySQLReplicationSamples(builder, session, row.InstanceID, keys)
	// TiDB 特有：engine 层换源（processlist / statements_summary）+ 集群组件。
	// MySQL / Doris 没有这些视图，不进该分支 —— 查了必然报错。
	if isTiDB {
		appendTiDBEngineSamples(builder, session, row.InstanceID, maxConnections, keys)
		appendTiDBClusterSamples(builder, session, keys)
	}
	// 会话中途断开时，后面每条查询都被短路成同一个错误，页面看到的是
	// "一堆指标各自报错"，而真实原因只有一条 —— 这里补一条日志把原因说清楚，
	// 避免排障时把"连接被服务端掐了"误判成"这些指标都不支持"。
	if broken := session.BrokenErr(); broken != nil {
		global.App.Log.Warnf("[monitor] 实例 %s 采集途中连接中断，本轮指标可能残缺：%v", row.InstanceID, broken)
	}
	return builder.list(), nil
}

// tidbStmtWindow 是 TiDB 语句统计窗口的一次快照。
//
// statements_summary 按窗口聚合（默认 30 分钟，SUMMARY_END_TIME 标识窗口终点），
// SUM(EXEC_COUNT) 不是单调累计计数器 —— 窗口切换时整个清空重算。
// 直接交给 directRateCache 差分，窗口重置那一轮会命中"计数器回退"分支回落到
// current/uptime；生产实例 uptime 是几天量级时，这个回落值会低到接近 0，
// 看起来就像"QPS 掉到 0"。所以这里自己记窗口标识：窗口没变才算差值，
// 变了就把本轮当新基线、明确报 no_data（缺一个点，但不会给出假数）。
type tidbStmtWindow struct {
	windowEnd string
	at        time.Time
	totals    map[string]float64
}

var tidbStmtWindows sync.Map // instanceID -> tidbStmtWindow

// appendTiDBEngineSamples 产出 TiDB 在 engine 层的真实取数。
//
// 背景：TiDB 的 SHOW GLOBAL STATUS 只有 Uptime / Ssl_* / server_id / ddl_schema_version /
// tidb_gc_*（v7.5.1 实测 9 个变量），MySQL 那套 Questions / Com_* / Threads_* /
// Slow_queries / Bytes_* 一个都没有。硬按 MySQL 口径取，缺失会被 map 零值吞掉，
// 页面画成一条贴着 0 的"真实曲线" —— 这比报错更有害，因为没人会去查。
//
// 换源口径（均为 TiDB 官方视图）：
//   - 连接数 / 活跃连接 → information_schema.processlist（COMMAND <> 'Sleep' 为活跃）
//   - QPS / TPS / DML 速率 / 慢查询速率 → information_schema.statements_summary
//     （按 STMT_TYPE 归类；慢查询阈值取 @@tidb_slow_log_threshold，AVG_LATENCY 是纳秒）
//   - 明确没有对应数据的项（协议字节、Select_scan、INSERT/REPLACE ... SELECT）
//     → unsupported + 替代方案，不冒充 0。
func appendTiDBEngineSamples(
	builder *sampleBuilder,
	session *dao.MySQLSession,
	instanceID string,
	maxConnections float64,
	keys []string,
) {
	// ---------------- 连接数 / 活跃连接 ----------------
	connTotal, connActive := 0.0, 0.0
	connOK := false
	if wantMetric(keys, MetricPerfConnCount) || wantMetric(keys, MetricPerfActiveConn) ||
		wantMetric(keys, MetricCapThreadUsedPct) || wantMetric(keys, MetricCapConnUsedPct) {
		_, rows, err := session.Query(`SELECT COUNT(*) AS total,
			SUM(CASE WHEN command <> 'Sleep' THEN 1 ELSE 0 END) AS active
			FROM information_schema.processlist`)
		switch {
		case err == nil && rows != nil && len(*rows) > 0:
			connTotal = toFloat64((*rows)[0]["total"])
			connActive = toFloat64((*rows)[0]["active"])
			connOK = true
		case err != nil:
			reason := "连接信息查询失败（information_schema.processlist）：" + trimError(err)
			if wantMetric(keys, MetricPerfConnCount) {
				builder.add(MetricPerfConnCount, 0, "count", QualityError, reason)
			}
			if wantMetric(keys, MetricPerfActiveConn) {
				builder.add(MetricPerfActiveConn, 0, "count", QualityError, reason)
			}
		}
		if connOK {
			if wantMetric(keys, MetricPerfConnCount) {
				builder.ok(MetricPerfConnCount, connTotal, "count")
			}
			if wantMetric(keys, MetricPerfActiveConn) {
				builder.ok(MetricPerfActiveConn, connActive, "count")
			}
		}
	}

	// ---------------- 连接使用率 / 活跃连接占比 ----------------
	// TiDB 的 max_connections 默认 0，含义是"不限制"，不是"读不到"。
	// 照 MySQL 那句"最大连接数不可用"会把运维引向检查变量权限，方向就错了。
	if wantMetric(keys, MetricCapThreadUsedPct) || wantMetric(keys, MetricCapConnUsedPct) {
		if maxConnections > 0 {
			if connOK {
				if wantMetric(keys, MetricCapThreadUsedPct) {
					builder.ok(MetricCapThreadUsedPct, connTotal/maxConnections*100, "%")
				}
				if wantMetric(keys, MetricCapConnUsedPct) {
					builder.ok(MetricCapConnUsedPct, connActive/maxConnections*100, "%")
				}
			}
		} else {
			reason := "TiDB 的 max_connections 为 0（默认不限制连接数），连接使用率不适用；如需观测连接压力请看「连接数」与「活跃连接数」"
			if wantMetric(keys, MetricCapThreadUsedPct) {
				builder.unsupported(MetricCapThreadUsedPct, "%", reason)
			}
			if wantMetric(keys, MetricCapConnUsedPct) {
				builder.unsupported(MetricCapConnUsedPct, "%", reason)
			}
		}
	}

	// ---------------- 语句统计（statements_summary） ----------------
	needStmt := wantMetric(keys, MetricPerfQPS) || wantMetric(keys, MetricPerfTPS) ||
		wantMetric(keys, MetricEngSlowQueries) ||
		wantMetric(keys, MetricEngComSelect) || wantMetric(keys, MetricEngComInsert) ||
		wantMetric(keys, MetricEngComUpdate) || wantMetric(keys, MetricEngComDelete) ||
		wantMetric(keys, MetricEngComReplace)

	if needStmt {
		// 慢日志阈值单位毫秒（默认 300），AVG_LATENCY 单位纳秒 —— 换算后再比较。
		thresholdNs := int64(300 * 1e6)
		if _, rows, err := session.Query(`SELECT @@tidb_slow_log_threshold AS th`); err == nil && rows != nil && len(*rows) > 0 {
			if ms := toFloat64((*rows)[0]["th"]); ms > 0 {
				thresholdNs = int64(ms * 1e6)
			}
		}
		query := fmt.Sprintf(`SELECT STMT_TYPE, SUM(EXEC_COUNT) AS execs,
			SUM(CASE WHEN AVG_LATENCY > %d THEN EXEC_COUNT ELSE 0 END) AS slow_execs,
			MAX(SUMMARY_END_TIME) AS win_end
			FROM information_schema.statements_summary GROUP BY STMT_TYPE`, thresholdNs)
		_, rows, err := session.Query(query)
		switch {
		case err != nil:
			reason := "TiDB 语句统计查询失败（information_schema.statements_summary）：" + trimError(err)
			for _, key := range []string{MetricPerfQPS, MetricPerfTPS, MetricEngSlowQueries,
				MetricEngComSelect, MetricEngComInsert, MetricEngComUpdate, MetricEngComDelete, MetricEngComReplace} {
				if wantMetric(keys, key) {
					builder.add(key, 0, "count/s", QualityError, reason)
				}
			}
		case rows == nil || len(*rows) == 0:
			reason := "statements_summary 暂时没有语句记录（tidb_enable_stmt_summary 关闭或实例刚启动）"
			for _, key := range []string{MetricPerfQPS, MetricPerfTPS, MetricEngSlowQueries,
				MetricEngComSelect, MetricEngComInsert, MetricEngComUpdate, MetricEngComDelete, MetricEngComReplace} {
				if wantMetric(keys, key) {
					builder.noData(key, "count/s", reason)
				}
			}
		default:
			totals := map[string]float64{}
			windowEnd := ""
			for _, item := range *rows {
				stmtType := strings.ToLower(strings.TrimSpace(toString(item["STMT_TYPE"])))
				execs := toFloat64(item["execs"])
				totals[stmtType] += execs
				totals["__total__"] += execs
				totals["__slow__"] += toFloat64(item["slow_execs"])
				if end := strings.TrimSpace(toString(item["win_end"])); end > windowEnd {
					windowEnd = end
				}
			}

			// TPS 口径对齐 TiDB Dashboard：用 DML 语句（Insert/Update/Delete/Replace）
			// 的执行速率，而不是 STMT_TYPE='Commit'。原因是 TiDB 的 statements_summary
			// 只记录显式事务的 Commit，autocommit 业务跑一整天也几乎全是 0。
			totals["__dml__"] = totals["insert"] + totals["update"] + totals["delete"] + totals["replace"]

			now := time.Now()
			sameWindow := false
			elapsed := 0.0
			var prevTotals map[string]float64
			if cached, ok := tidbStmtWindows.Load(instanceID); ok {
				snap := cached.(tidbStmtWindow)
				if windowEnd != "" && snap.windowEnd == windowEnd {
					sameWindow = true
					elapsed = now.Sub(snap.at).Seconds()
					prevTotals = snap.totals
				}
			}

			specs := []struct {
				metricKey string
				bucket    string
			}{
				{MetricPerfQPS, "__total__"},
				{MetricPerfTPS, "__dml__"},
				{MetricEngComSelect, "select"},
				{MetricEngComInsert, "insert"},
				{MetricEngComUpdate, "update"},
				{MetricEngComDelete, "delete"},
				{MetricEngComReplace, "replace"},
				{MetricEngSlowQueries, "__slow__"},
			}
			for _, spec := range specs {
				if !wantMetric(keys, spec.metricKey) {
					continue
				}
				rate, reason := tidbStmtRate(totals[spec.bucket], prevTotals[spec.bucket], sameWindow, elapsed)
				if reason != "" {
					builder.noData(spec.metricKey, "count/s", reason)
					continue
				}
				builder.counter(spec.metricKey, rate, "count/s")
			}

			tidbStmtWindows.Store(instanceID, tidbStmtWindow{windowEnd: windowEnd, at: now, totals: totals})
		}
	}

	// ---------------- TiDB 没有对应数据的项：说清原因与替代方案 ----------------
	if wantMetric(keys, MetricEngComInsertSelect) {
		builder.unsupported(MetricEngComInsertSelect, "count/s", "TiDB 的语句统计只按 STMT_TYPE 归类，不区分 INSERT ... SELECT")
	}
	if wantMetric(keys, MetricEngComReplaceSelect) {
		builder.unsupported(MetricEngComReplaceSelect, "count/s", "TiDB 的语句统计只按 STMT_TYPE 归类，不区分 REPLACE ... SELECT")
	}
	if wantMetric(keys, MetricEngSelectScan) {
		builder.unsupported(MetricEngSelectScan, "count/s", "TiDB 不提供 Select_scan 计数器；全表扫描请用 statements_summary 的 AVG_TOTAL_KEYS 或慢日志分析")
	}
	if wantMetric(keys, MetricPerfReadBytes) {
		builder.unsupported(MetricPerfReadBytes, "B/s", "TiDB 不提供 Bytes_sent 状态变量，协议流量请经 Prometheus（tidb_server_packet_io_bytes）采集")
	}
	if wantMetric(keys, MetricPerfWriteBytes) {
		builder.unsupported(MetricPerfWriteBytes, "B/s", "TiDB 不提供 Bytes_received 状态变量，协议流量请经 Prometheus（tidb_server_packet_io_bytes）采集")
	}
}

// appendTiDBClusterSamples 产出 TiDB 特有的集群组件指标（engine 层事务/锁 + kernel 层拓扑）。
//
// 只在 engine == "tidb" 时调用（MySQL / Doris 没有这些表）。每个指标独立容错：
//   - 表不存在或被移除（低版本无 data_lock_waits、TiDB 7.5+ 移除 tikv_store_status）
//     → unsupported，绝不静默缺数 —— 口径与「状态变量不存在」一致；
//   - 权限不足等真实错误 → error 并保留原始错误文本。
//
// wantMetric 门控：指标不在模板里就完全不发查询，与 MySQL 侧「按需才跑」一致；
// 一次需要多个 store 指标时共享同一条 tikv_store_status 查询，避免重复往返。
func appendTiDBClusterSamples(builder *sampleBuilder, session *dao.MySQLSession, keys []string) {
	if wantMetric(keys, MetricEngTIDBTrxActive) {
		if _, rows, err := session.Query(`SELECT COUNT(*) AS c FROM information_schema.tidb_trx`); err == nil && rows != nil && len(*rows) > 0 {
			builder.ok(MetricEngTIDBTrxActive, toFloat64((*rows)[0]["c"]), "count")
		} else if err != nil {
			builder.add(MetricEngTIDBTrxActive, 0, "count", QualityError, "活跃事务统计失败："+trimError(err))
		}
	}
	if wantMetric(keys, MetricEngTIDBLockWaits) {
		if _, rows, err := session.Query(`SELECT COUNT(*) AS c FROM information_schema.data_lock_waits`); err == nil && rows != nil && len(*rows) > 0 {
			builder.ok(MetricEngTIDBLockWaits, toFloat64((*rows)[0]["c"]), "count")
		} else if err != nil {
			builder.add(MetricEngTIDBLockWaits, 0, "count", QualityError, "锁等待统计失败："+trimError(err))
		}
	}
	if wantMetric(keys, MetricKernTIDBNodeTotal) || wantMetric(keys, MetricKernTIDBNodeAbnormal) {
		// cluster_info 的列在各版本不同：老版本有 STATUS，7.5 实测只有
		// TYPE / INSTANCE / STATUS_ADDRESS / VERSION / GIT_HASH / START_TIME / UPTIME / SERVER_ID。
		// 写死 STATUS 在 7.5+ 会直接报 Unknown column —— 只用跨版本稳定的列。
		_, rows, err := session.Query(`SELECT type, uptime FROM information_schema.cluster_info`)
		switch {
		case err != nil:
			if wantMetric(keys, MetricKernTIDBNodeTotal) {
				builder.add(MetricKernTIDBNodeTotal, 0, "count", QualityError, "集群拓扑查询失败："+trimError(err))
			}
			if wantMetric(keys, MetricKernTIDBNodeAbnormal) {
				builder.add(MetricKernTIDBNodeAbnormal, 0, "count", QualityError, "集群拓扑查询失败："+trimError(err))
			}
		default:
			total, abnormal := 0.0, 0.0
			if rows != nil {
				for _, item := range *rows {
					nodeType := strings.ToLower(strings.TrimSpace(toString(item["type"])))
					// 只统计 TiDB Server 节点：指标名就是「TiDB 节点数」，
					// tikv / pd 行由 TiKV 那组指标单独覆盖。
					if nodeType != "tidb" {
						continue
					}
					total++
					// 存活判据：上报了 UPTIME 即视为在运行。cluster_info 在 7.5
					// 不再提供 STATUS 列，单机部署的 tikv 行 UPTIME 恒空，
					// 若把"空"当异常，单机实例会恒报 1 个异常组件。
					uptimeText := strings.ToLower(strings.TrimSpace(toString(item["uptime"])))
					if uptimeText == "" || uptimeText == "0s" {
						abnormal++
					}
				}
			}
			if wantMetric(keys, MetricKernTIDBNodeTotal) {
				builder.ok(MetricKernTIDBNodeTotal, total, "count")
			}
			if wantMetric(keys, MetricKernTIDBNodeAbnormal) {
				builder.ok(MetricKernTIDBNodeAbnormal, abnormal, "count")
			}
		}
	}
	if wantMetric(keys, MetricKernTiKVStoreTotal) || wantMetric(keys, MetricKernTiKVStoreDown) ||
		wantMetric(keys, MetricKernTiKVRegionTotal) || wantMetric(keys, MetricKernTiKVStoreUsedPct) {
		// 列名以 v7.5 实测为准：STORE_ID / ADDRESS / STORE_STATE_NAME / CAPACITY / AVAILABLE /
		// REGION_COUNT。CAPACITY 与 AVAILABLE 是 varchar（可能是 "100GiB" 这类带单位文本），
		// 不能直接当数字读，统一走 parseTiKVSize 换算成字节。
		_, rows, err := session.Query(`SELECT store_state_name, region_count, capacity, available FROM information_schema.tikv_store_status`)
		if err != nil {
			reason := tiKVUnavailableReason(err)
			for _, key := range []string{MetricKernTiKVStoreTotal, MetricKernTiKVStoreDown, MetricKernTiKVRegionTotal, MetricKernTiKVStoreUsedPct} {
				if wantMetric(keys, key) {
					builder.unsupported(key, "count", reason)
				}
			}
		} else {
			total, down, regions, usedPct := 0.0, 0.0, 0.0, 0.0
			if rows != nil {
				for _, item := range *rows {
					total++
					state := strings.ToUpper(strings.TrimSpace(toString(item["store_state_name"])))
					if state != "UP" {
						down++
					}
					regions += toFloat64(item["region_count"])
					capacity := parseTiKVSize(toString(item["capacity"]))
					available := parseTiKVSize(toString(item["available"]))
					if capacity > 0 {
						if pct := (capacity - available) / capacity * 100; pct > usedPct {
							usedPct = pct
						}
					}
				}
			}
			if wantMetric(keys, MetricKernTiKVStoreTotal) {
				builder.ok(MetricKernTiKVStoreTotal, total, "count")
			}
			if wantMetric(keys, MetricKernTiKVStoreDown) {
				builder.ok(MetricKernTiKVStoreDown, down, "count")
			}
			if wantMetric(keys, MetricKernTiKVRegionTotal) {
				builder.ok(MetricKernTiKVRegionTotal, regions, "count")
			}
			if wantMetric(keys, MetricKernTiKVStoreUsedPct) {
				builder.ok(MetricKernTiKVStoreUsedPct, usedPct, "%")
			}
		}
	}
	if wantMetric(keys, MetricKernTiKVHotRegions) {
		if _, rows, err := session.Query(`SELECT COUNT(*) AS c FROM information_schema.tidb_hot_regions`); err == nil && rows != nil && len(*rows) > 0 {
			builder.ok(MetricKernTiKVHotRegions, toFloat64((*rows)[0]["c"]), "count")
		} else if err != nil {
			builder.unsupported(MetricKernTiKVHotRegions, "count", "热点 Region 查询失败："+tiKVUnavailableReason(err))
		}
	}
}

// tidbStmtRate 计算 TiDB 语句统计的速率；第二个返回值非空表示本轮不能产出速率，
// 调用方必须按 no_data 上报（不能当 0，否则"窗口刚重置"会被读成"业务停了"）。
func tidbStmtRate(current, previous float64, sameWindow bool, elapsed float64) (float64, string) {
	switch {
	case !sameWindow || elapsed <= 0:
		return 0, "TiDB 语句统计窗口刚重置，下一轮开始产出速率"
	case current < previous:
		return 0, "TiDB 语句统计计数回退（statements_summary 被重置），已重新建立基线"
	default:
		return (current - previous) / elapsed, ""
	}
}

// parseTiKVSize 把 tikv_store_status 的容量文本换算成字节。
//
// CAPACITY / AVAILABLE 在 information_schema 里是 varchar：不同版本可能返回纯字节数
// （"1073741824"），也可能返回人类可读文本（"100GiB" / "1.5TiB" / "512MiB"）。
// 直接 ParseFloat 遇带单位的值会得 0，进而把"存储使用率"算成 0% 或整项跳过 —— 必须按后缀换算。
func parseTiKVSize(text string) float64 {
	value := strings.ToUpper(strings.TrimSpace(text))
	if value == "" {
		return 0
	}
	units := []struct {
		suffix string
		factor float64
	}{
		{"TIB", 1 << 40}, {"TB", 1e12},
		{"GIB", 1 << 30}, {"GB", 1e9},
		{"MIB", 1 << 20}, {"MB", 1e6},
		{"KIB", 1 << 10}, {"KB", 1e3},
		{"B", 1},
	}
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			return toFloat64(strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))) * unit.factor
		}
	}
	return toFloat64(value)
}

// tiKVUnavailableReason 把 TiKV / PD 侧失败翻译成可执行的排查方向。
//
// 同一句「TiKV store 信息不可用」背后有三种完全不同的原因，混在一起写会把人带偏：
//   - pd unavailable：单机 tidb-server 未部署 PD/TiKV，或 PD 未就绪（7.5 实测）；
//   - 表不存在：7.5+ 已移除 tikv_store_status 视图；
//   - 其他：权限或网络问题，保留原始错误。
func tiKVUnavailableReason(err error) string {
	if err == nil {
		return ""
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "pd unavailable"):
		return "PD 不可达（单机 tidb-server 未部署 PD/TiKV，或 PD 未就绪）；TiKV 指标请经 Prometheus 采集"
	case strings.Contains(lower, "doesn't exist"), strings.Contains(lower, "unknown table"):
		return "当前 TiDB 版本无 tikv_store_status 视图（7.5+ 已移除）；TiKV 指标请经 Prometheus 采集"
	default:
		return "TiKV store 信息查询失败：" + trimError(err)
	}
}

// queryInnoDBMetricCounters 一次取回 innodb_metrics 里的多个计数器。
//
// 该表默认关闭采集（status='disabled'），取不到对应 name 就不出现在结果里，
// 调用方据此标 unsupported —— 这是正常状态，不是采集失败。
// names 只接受代码常量（绝不接收外部输入），可直接拼接。
func queryInnoDBMetricCounters(session *dao.MySQLSession, names []string) map[string]float64 {
	result := make(map[string]float64, len(names))
	if session == nil || len(names) == 0 {
		return result
	}
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, "'"+name+"'")
	}
	_, rows, err := session.Query(`SELECT name, ` + "`count`" + ` AS c FROM information_schema.innodb_metrics
		WHERE name IN (` + strings.Join(quoted, ",") + `) AND status = 'enabled'`)
	if err != nil || rows == nil {
		return result
	}
	for _, item := range *rows {
		result[toString(item["name"])] = toFloat64(item["c"])
	}
	return result
}

// 匹配 SHOW ENGINE INNODB STATUS 文本里的关键数值
var (
	historyListPattern    = regexp.MustCompile(`History list length\s+(\d+)`)
	logSequencePattern    = regexp.MustCompile(`Log sequence number\s+(\d+)`)
	lastCheckpointPattern = regexp.MustCompile(`Last checkpoint at\s+(\d+)`)
)

// appendMySQLExtendedSamples 产出引擎层扩展与内核层（InnoDB 方言）指标。
//
// rawValues 是主状态查询里 Non-numeric 变量的原文视图（半同步状态是 ON/OFF 字符串，
// 走数值视图会变成 0 而丢失语义）。改造前为了拿它要多跑一次 SHOW GLOBAL STATUS。
// 方言不适用或权限不足的指标显式标记 unsupported/error，绝不静默缺数。
func appendMySQLExtendedSamples(
	builder *sampleBuilder,
	session *dao.MySQLSession,
	values map[string]float64,
	rawValues map[string]string,
	rates map[string]float64,
	keys []string,
	deadlockOK, pageSplitOK bool,
	isTiDB bool,
) {
	// 慢查询速率：TiDB 没有 Slow_queries 状态变量，改由 statements_summary 中
	// AVG_LATENCY 超过 @@tidb_slow_log_threshold 的语句数产出（见 appendTiDBEngineSamples）。
	if wantMetric(keys, MetricEngSlowQueries) && !isTiDB {
		builder.counter(MetricEngSlowQueries, rates[MetricEngSlowQueries], "count/s")
	}
	if wantMetric(keys, MetricEngAbortedConns) {
		builder.counter(MetricEngAbortedConns, rates[MetricEngAbortedConns], "count/s")
	}
	if wantMetric(keys, MetricEngBinlogWALRate) {
		if _, ok := hasStatusVar(values, "Binlog_bytes_written"); ok {
			builder.counter(MetricEngBinlogWALRate, rates[MetricEngBinlogWALRate], "B/s")
		} else {
			builder.unsupported(MetricEngBinlogWALRate, "B/s", "Binlog_bytes_written 状态变量不可用（低版本或未开启 binlog）")
		}
	}
	if wantMetric(keys, MetricEngDeadlocks) {
		if deadlockOK {
			builder.counter(MetricEngDeadlocks, rates[MetricEngDeadlocks], "count/s")
		} else {
			builder.unsupported(MetricEngDeadlocks, "count/s", "innodb_metrics 的 lock_deadlocks 未启用（SET GLOBAL innodb_monitor_enable='lock_deadlocks'）")
		}
	}

	// 半同步状态是字符串（ON/OFF），状态变量表按浮点转换会丢失。
	// 直接复用主查询的原文视图，不再单独发起一次查询。
	if wantMetric(keys, MetricEngSemiSyncStatus) {
		on := false
		present := false
		for _, name := range []string{
			"Rpl_semi_sync_source_status",
			"Rpl_semi_sync_master_status",
			"Rpl_semi_sync_replica_status",
		} {
			if value, ok := rawValues[name]; ok {
				present = true
				if strings.EqualFold(value, "ON") {
					on = true
				}
			}
		}
		if !present {
			builder.unsupported(MetricEngSemiSyncStatus, "", "未安装或未启用半同步复制插件")
		} else if on {
			builder.ok(MetricEngSemiSyncStatus, 1, "")
		} else {
			builder.add(MetricEngSemiSyncStatus, 0, "", QualityOK, "半同步已退化为异步复制")
		}
	}

	if wantMetric(keys, MetricEngLargestTable) {
		if _, rows, err := session.Query(`SELECT COALESCE(MAX(data_length + index_length), 0) AS c
			FROM information_schema.tables
			WHERE table_schema NOT IN ('mysql','information_schema','performance_schema','sys')`); err == nil && rows != nil && len(*rows) > 0 {
			builder.ok(MetricEngLargestTable, toFloat64((*rows)[0]["c"]), "B")
		} else if err != nil {
			builder.add(MetricEngLargestTable, 0, "B", QualityError, "最大表统计失败："+trimError(err))
		}
	}

	// ---------------- 内核层（InnoDB） ----------------
	if wantMetric(keys, MetricKernRedoWriteRate) {
		if _, ok := hasStatusVar(values, "Innodb_os_log_written"); ok {
			builder.counter(MetricKernRedoWriteRate, rates[MetricKernRedoWriteRate], "B/s")
		} else {
			builder.unsupported(MetricKernRedoWriteRate, "B/s", "非 InnoDB 引擎，无 redo 计数器")
		}
	}
	if wantMetric(keys, MetricKernBufferDirtyPct) {
		total := values["Innodb_buffer_pool_pages_total"]
		if dirty, ok := hasStatusVar(values, "Innodb_buffer_pool_pages_dirty"); ok && total > 0 {
			builder.ok(MetricKernBufferDirtyPct, dirty/total*100, "%")
		} else {
			builder.unsupported(MetricKernBufferDirtyPct, "%", "非 InnoDB 引擎或缓冲池指标不可用")
		}
	}
	if wantMetric(keys, MetricKernFlushRate) {
		if _, ok := hasStatusVar(values, "Innodb_buffer_pool_pages_flushed"); ok {
			builder.counter(MetricKernFlushRate, rates[MetricKernFlushRate], "pages/s")
		} else {
			builder.unsupported(MetricKernFlushRate, "pages/s", "非 InnoDB 引擎，无刷脏计数器")
		}
	}
	if wantMetric(keys, MetricKernDBLWRWriteRate) {
		if _, ok := hasStatusVar(values, "Innodb_dblwr_writes"); ok {
			builder.counter(MetricKernDBLWRWriteRate, rates[MetricKernDBLWRWriteRate], "pages/s")
		} else {
			builder.unsupported(MetricKernDBLWRWriteRate, "pages/s", "非 InnoDB 引擎，无 doublewrite 计数器")
		}
	}
	if wantMetric(keys, MetricKernRollbackRatio) {
		commit := values["Com_commit"]
		rollback := values["Com_rollback"]
		if commit+rollback > 0 {
			builder.ok(MetricKernRollbackRatio, rollback/(commit+rollback)*100, "%")
		} else {
			builder.noData(MetricKernRollbackRatio, "%", "实例启动后尚无事务提交或回滚")
		}
	}
	if wantMetric(keys, MetricKernReadAheadEvict) {
		readAhead := values["Innodb_buffer_pool_read_ahead"]
		if evicted, ok := hasStatusVar(values, "Innodb_buffer_pool_read_ahead_evicted"); ok {
			if readAhead > 0 {
				builder.ok(MetricKernReadAheadEvict, evicted/readAhead*100, "%")
			} else {
				builder.noData(MetricKernReadAheadEvict, "%", "实例启动后尚未触发预读")
			}
		} else {
			builder.unsupported(MetricKernReadAheadEvict, "%", "非 InnoDB 引擎，无预读计数器")
		}
	}
	if wantMetric(keys, MetricKernTableCacheHit) {
		hits, hitsOK := hasStatusVar(values, "Table_open_cache_hits")
		misses, missesOK := hasStatusVar(values, "Table_open_cache_misses")
		switch {
		case !hitsOK || !missesOK:
			builder.unsupported(MetricKernTableCacheHit, "%", "MySQL 5.6 及以下无表缓存命中计数器")
		case hits+misses > 0:
			builder.ok(MetricKernTableCacheHit, hits/(hits+misses)*100, "%")
		default:
			builder.noData(MetricKernTableCacheHit, "%", "实例启动后尚无表缓存访问")
		}
	}
	if wantMetric(keys, MetricKernPageSplitRate) {
		if pageSplitOK {
			builder.counter(MetricKernPageSplitRate, rates[MetricKernPageSplitRate], "count/s")
		} else {
			builder.unsupported(MetricKernPageSplitRate, "count/s", "innodb_metrics 的 index_page_splits 未启用（SET GLOBAL innodb_monitor_enable='index_page_splits'）")
		}
	}

	// Undo 历史链与 Checkpoint 年龄都需要解析 SHOW ENGINE INNODB STATUS（需 PROCESS 权限），
	// 合并为一次查询；权限不足时两个指标同时标 error，原因可见。
	wantHistory := wantMetric(keys, MetricKernHistoryList)
	wantCheckpoint := wantMetric(keys, MetricKernCheckpointAge)
	if wantHistory || wantCheckpoint {
		_, rows, err := session.Query("SHOW ENGINE INNODB STATUS")
		if err != nil || rows == nil || len(*rows) == 0 {
			reason := "SHOW ENGINE INNODB STATUS 执行失败（需要 PROCESS 权限）"
			if err != nil {
				reason += "：" + trimError(err)
			}
			if wantHistory {
				builder.add(MetricKernHistoryList, 0, "count", QualityError, reason)
			}
			if wantCheckpoint {
				builder.add(MetricKernCheckpointAge, 0, "B", QualityError, reason)
			}
		} else {
			text := toString((*rows)[0]["Status"])
			if wantHistory {
				if match := historyListPattern.FindStringSubmatch(text); len(match) == 2 {
					builder.ok(MetricKernHistoryList, toFloat64(match[1]), "count")
				} else {
					builder.noData(MetricKernHistoryList, "count", "引擎状态文本中未找到 History list length")
				}
			}
			if wantCheckpoint {
				lsnMatch := logSequencePattern.FindStringSubmatch(text)
				ckptMatch := lastCheckpointPattern.FindStringSubmatch(text)
				if len(lsnMatch) == 2 && len(ckptMatch) == 2 {
					age := toFloat64(lsnMatch[1]) - toFloat64(ckptMatch[1])
					if age < 0 {
						age = 0
					}
					builder.ok(MetricKernCheckpointAge, age, "B")
				} else {
					builder.noData(MetricKernCheckpointAge, "B", "引擎状态文本中未找到 LSN 检查点信息")
				}
			}
		}
	}
}

// mysqlCounterSample 产出「SHOW GLOBAL STATUS 累计计数器 → 每秒速率」的采样点。
//
// 口径与附件一致：速率 = (后一次采样值 - 前一次采样值) / 采样间隔秒数，
// 由 directRateCache 维护上一轮快照；首轮回退到 当前值 / Uptime。
// 变量不存在（低版本 / 非 InnoDB 引擎）→ unsupported；变量存在但值为 0 → 正常上报 0，
// 这两者必须分开，否则"没采到"会被页面读成"真的很闲"。
func mysqlCounterSample(
	builder *sampleBuilder,
	values map[string]float64,
	rates map[string]float64,
	keys []string,
	metricKey string,
	statusVar string,
	unit string,
) {
	if !wantMetric(keys, metricKey) {
		return
	}
	if _, ok := hasStatusVar(values, statusVar); !ok {
		builder.unsupported(metricKey, unit, fmt.Sprintf("状态变量 %s 不可用（非 InnoDB 引擎或低版本 MySQL）", statusVar))
		return
	}
	builder.counter(metricKey, rates[metricKey], unit)
}

// mysqlGaugeSample 产出「瞬时值 / 存量值」采样点（不做速率换算，直接取状态变量原值）。
func mysqlGaugeSample(
	builder *sampleBuilder,
	values map[string]float64,
	keys []string,
	metricKey string,
	statusVar string,
	unit string,
) {
	if !wantMetric(keys, metricKey) {
		return
	}
	value, ok := hasStatusVar(values, statusVar)
	if !ok {
		builder.unsupported(metricKey, unit, fmt.Sprintf("状态变量 %s 不可用（非 InnoDB 引擎或低版本 MySQL）", statusVar))
		return
	}
	builder.ok(metricKey, value, unit)
}

// appendMySQLStatusDerivedSamples 产出「附件口径补齐」的那批指标：
// Com_* 语句执行次数、Select_scan、InnoDB Buffer Pool / Data IO / 行锁 / 行操作 / Redo。
// 全部来自同一次 SHOW GLOBAL STATUS，不额外增加往返、不引入新依赖。
func appendMySQLStatusDerivedSamples(
	builder *sampleBuilder,
	values map[string]float64,
	rates map[string]float64,
	keys []string,
	isTiDB bool,
) {
	// ---------------- 语句执行次数（Com_* 系列）----------------
	// TiDB 不实现 Com_* 状态变量，这几个指标由 statements_summary 的 STMT_TYPE 归类产出
	// （见 appendTiDBEngineSamples）。这里必须跳过，否则会先写一条 unsupported 再写一条真实速率。
	if !isTiDB {
		mysqlCounterSample(builder, values, rates, keys, MetricEngComDelete, "Com_delete", "count/s")
		mysqlCounterSample(builder, values, rates, keys, MetricEngComInsert, "Com_insert", "count/s")
		mysqlCounterSample(builder, values, rates, keys, MetricEngComInsertSelect, "Com_insert_select", "count/s")
		mysqlCounterSample(builder, values, rates, keys, MetricEngComReplace, "Com_replace", "count/s")
		mysqlCounterSample(builder, values, rates, keys, MetricEngComReplaceSelect, "Com_replace_select", "count/s")
		mysqlCounterSample(builder, values, rates, keys, MetricEngComSelect, "Com_select", "count/s")
		mysqlCounterSample(builder, values, rates, keys, MetricEngComUpdate, "Com_update", "count/s")
		mysqlCounterSample(builder, values, rates, keys, MetricEngSelectScan, "Select_scan", "count/s")
	}

	// ---------------- 临时表与文件 / 表缓存 ----------------
	// 四项都是瞬时/累计存量，直接取原值；速率与占比视角由既有的
	// 「临时表落盘率」（Created_tmp_disk_tables / Created_tmp_tables）覆盖。
	mysqlGaugeSample(builder, values, keys, MetricEngTmpDiskTables, "Created_tmp_disk_tables", "count")
	mysqlGaugeSample(builder, values, keys, MetricEngInnodbOpenFiles, "Innodb_num_open_files", "count")
	mysqlGaugeSample(builder, values, keys, MetricEngOpenFiles, "Open_files", "count")
	mysqlGaugeSample(builder, values, keys, MetricEngOpenTables, "Open_tables", "count")

	// ---------------- InnoDB Buffer Pool ----------------
	mysqlGaugeSample(builder, values, keys, MetricKernBufferDirtyPages, "Innodb_buffer_pool_pages_dirty", "count")
	mysqlCounterSample(builder, values, rates, keys, MetricKernBufferReadReq, "Innodb_buffer_pool_read_requests", "count/s")
	mysqlCounterSample(builder, values, rates, keys, MetricKernBufferWriteReq, "Innodb_buffer_pool_write_requests", "count/s")

	// ---------------- InnoDB Data 读写吞吐量 ----------------
	mysqlCounterSample(builder, values, rates, keys, MetricKernDataReadRate, "Innodb_data_read", "B/s")
	mysqlCounterSample(builder, values, rates, keys, MetricKernDataWrittenRate, "Innodb_data_written", "B/s")

	// ---------------- InnoDB 行锁 ----------------
	// 累计量与速率分开上报：累计量用于"启动以来总共等了多久/多少次"，
	// 速率用于告警；平均等待优先用 MySQL 自带的 Innodb_row_lock_time_avg，
	// 取不到时按附件口径 avg = time / waits 推导。
	mysqlGaugeSample(builder, values, keys, MetricKernRowLockTimeMs, "Innodb_row_lock_time", "ms")
	mysqlGaugeSample(builder, values, keys, MetricKernRowLockWaits, "Innodb_row_lock_waits", "count")
	mysqlCounterSample(builder, values, rates, keys, MetricKernRowLockTimeRate, "Innodb_row_lock_time", "ms/s")
	mysqlCounterSample(builder, values, rates, keys, MetricKernRowLockWaitsRate, "Innodb_row_lock_waits", "count/s")
	if wantMetric(keys, MetricKernRowLockTimeAvgMs) {
		avg, hasAvg := hasStatusVar(values, "Innodb_row_lock_time_avg")
		_, hasWaits := hasStatusVar(values, "Innodb_row_lock_waits")
		switch {
		case hasAvg:
			// MySQL 自带该状态变量（5.7+），直接取，避免与平台算法口径打架
			builder.ok(MetricKernRowLockTimeAvgMs, avg, "ms")
		case !hasWaits:
			// 连等待次数都没有 → 不是"还没发生等待"，而是这个引擎根本没有行锁计数器
			builder.unsupported(MetricKernRowLockTimeAvgMs, "ms", "状态变量 Innodb_row_lock_waits 不可用（非 InnoDB 引擎或低版本 MySQL）")
		case values["Innodb_row_lock_waits"] > 0:
			// 低版本没有该变量时按附件口径推导：avg = time / waits
			builder.ok(MetricKernRowLockTimeAvgMs, values["Innodb_row_lock_time"]/values["Innodb_row_lock_waits"], "ms")
		default:
			builder.noData(MetricKernRowLockTimeAvgMs, "ms", "实例启动后尚未发生行锁等待，平均等待时长无法计算")
		}
	}

	// ---------------- InnoDB 行操作 / 刷盘 / 排序 ----------------
	mysqlCounterSample(builder, values, rates, keys, MetricKernRowsDeletedRate, "Innodb_rows_deleted", "rows/s")
	mysqlCounterSample(builder, values, rates, keys, MetricKernRowsInsertedRate, "Innodb_rows_inserted", "rows/s")
	mysqlCounterSample(builder, values, rates, keys, MetricKernRowsUpdatedRate, "Innodb_rows_updated", "rows/s")
	mysqlCounterSample(builder, values, rates, keys, MetricKernDataFsyncsRate, "Innodb_data_fsyncs", "count/s")
	mysqlCounterSample(builder, values, rates, keys, MetricKernSortRowsRate, "Sort_rows", "count/s")

	// ---------------- InnoDB Redo Log ----------------
	mysqlCounterSample(builder, values, rates, keys, MetricKernLogWriteReqRate, "Innodb_log_write_requests", "count/s")
	mysqlCounterSample(builder, values, rates, keys, MetricKernLogWritesRate, "Innodb_log_writes", "count/s")
	mysqlCounterSample(builder, values, rates, keys, MetricKernOsLogFsyncsRate, "Innodb_os_log_fsyncs", "count/s")
}

// appendMySQLReplicationSamples 采集复制相关指标。
// 主库 / 未配置复制的实例会得到 quality=unsupported —— 这不是故障，
// 关键是要让"复制中断"类规则不会因为读不到而从"无法判定"变成"误报"。
// mysqlReplicaStmt 记住每个实例可用的复制状态语句（值："REPLICA" / "SLAVE"）。
//
// 不记忆时每轮要试两条：8.0 上 SHOW REPLICA STATUS 返回空行后还会再试 SLAVE，
// 5.7 上 REPLICA 语句报 1064 也要再试 SLAVE —— 也就是说"主库"这个最常见的场景
// 每轮固定多跑一条废查询，而它恰恰是最不该给数据库加负载的场景。
//
// 只在语句**执行成功**时写入记忆（返回空行也算成功）：空行代表"不是从库"，
// 不代表语句不被支持，不能据此把记忆删掉。记错方向的代价只是一条多余查询，
// 不会漏采 —— 记忆里那条语句跑失败时会立刻回退试另一条。
//
// 键的数量上界就是实例总数（几百），实例下线只残留一个字符串，无需额外回收。
var mysqlReplicaStmt sync.Map

const (
	mysqlReplicaStatusStmt = "SHOW REPLICA STATUS"
	mysqlSlaveStatusStmt   = "SHOW SLAVE STATUS"
)

func appendMySQLReplicationSamples(builder *sampleBuilder, session *dao.MySQLSession, instanceID string, keys []string) {
	wantRepl := wantMetric(keys, MetricReplIORunning) ||
		wantMetric(keys, MetricReplSQLRunning) ||
		wantMetric(keys, MetricReplLagSeconds)
	if !wantRepl {
		return
	}

	// 有记忆就直接用，没有则从 8.0.22+ 的新名字开始试。
	stmt := mysqlReplicaStatusStmt
	remembered, hasMemory := mysqlReplicaStmt.Load(instanceID)
	if hasMemory {
		if v, _ := remembered.(string); v == "SLAVE" {
			stmt = mysqlSlaveStatusStmt
		}
	}
	_, rows, err := session.Query(stmt)
	if err != nil || (!hasMemory && (rows == nil || len(*rows) == 0)) {
		// MySQL 8.0.22 之前只有 SLAVE。已有记忆时不再试另一条：
		// 那条语句上一轮跑通过，返回空行说明它就不是从库。
		fallback := mysqlSlaveStatusStmt
		if stmt == fallback {
			fallback = mysqlReplicaStatusStmt
		}
		stmt = fallback
		_, rows, err = session.Query(stmt)
	}
	if err == nil {
		mysqlReplicaStmt.Store(instanceID, stmt)
	}
	replNote := "该实例未配置复制（非从库）"
	if err != nil {
		builder.add(MetricReplIORunning, 0, "", QualityError, "复制状态查询失败："+trimError(err))
		builder.add(MetricReplSQLRunning, 0, "", QualityError, "复制状态查询失败："+trimError(err))
		builder.add(MetricReplLagSeconds, 0, "s", QualityError, "复制状态查询失败："+trimError(err))
		return
	}
	if rows == nil || len(*rows) == 0 {
		if wantMetric(keys, MetricReplIORunning) {
			builder.unsupported(MetricReplIORunning, "", replNote)
		}
		if wantMetric(keys, MetricReplSQLRunning) {
			builder.unsupported(MetricReplSQLRunning, "", replNote)
		}
		if wantMetric(keys, MetricReplLagSeconds) {
			builder.unsupported(MetricReplLagSeconds, "s", replNote)
		}
		return
	}
	first := (*rows)[0]
	ioRunning := strings.EqualFold(toString(first["Replica_IO_Running"]), "Yes")
	sqlRunning := strings.EqualFold(toString(first["Replica_SQL_Running"]), "Yes")
	if wantMetric(keys, MetricReplIORunning) {
		if ioRunning {
			builder.ok(MetricReplIORunning, 1, "")
		} else {
			reason := "复制 IO 线程已停止"
			if lastErr := toString(first["Last_IO_Error"]); lastErr != "" {
				reason += "：" + truncateText(lastErr, 160)
			}
			builder.add(MetricReplIORunning, 0, "", QualityOK, reason)
		}
	}
	if wantMetric(keys, MetricReplSQLRunning) {
		if sqlRunning {
			builder.ok(MetricReplSQLRunning, 1, "")
		} else {
			reason := "复制 SQL 线程已停止"
			if lastErr := toString(first["Last_SQL_Error"]); lastErr != "" {
				reason += "：" + truncateText(lastErr, 160)
			}
			builder.add(MetricReplSQLRunning, 0, "", QualityOK, reason)
		}
	}
	if wantMetric(keys, MetricReplLagSeconds) {
		if ioRunning || sqlRunning {
			builder.ok(MetricReplLagSeconds, toFloat64(first["Seconds_Behind_Source"]), "s")
		} else {
			// 复制已中断时延迟秒数没有参考意义
			builder.noData(MetricReplLagSeconds, "s", "复制线程未运行，延迟不可判定")
		}
	}
}

// ---------------------------------------------------------------------------
// PostgreSQL
// ---------------------------------------------------------------------------

func collectPostgresSamples(ctx context.Context, row monitorInstanceRow, keys []string) ([]MetricSample, error) {
	password, err := secret.Decrypt(row.Password)
	if err != nil {
		return nil, fmt.Errorf("实例密码解密失败")
	}
	cfg, err := parseExtraConfig(row.ExtraConfig)
	if err != nil {
		return nil, err
	}
	database := getStringConfig(cfg, "database", "postgres")
	sslMode := getStringConfig(cfg, "sslmode", "disable")

	childCtx, cancel := context.WithTimeout(ctx, instanceTimeoutSeconds(row))
	defer cancel()

	connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		url.QueryEscape(row.User), url.QueryEscape(password),
		row.Hostname, row.Port, url.PathEscape(database), url.QueryEscape(sslMode))
	conn, err := pgx.Connect(childCtx, connStr)
	if err != nil {
		return nil, fmt.Errorf("PostgreSQL 指标采集失败：%s", err.Error())
	}
	defer conn.Close(childCtx)

	builder := newSampleBuilder("direct")

	var (
		uptime            float64
		txTotal           float64
		commitTotal       float64
		rollbackTotal     float64
		deadlockTotal     float64
		readRowsTotal     float64
		writeRowsTotal    float64
		blocksRead        float64
		blocksHit         float64
		blockSize         float64
		connections       float64
		activeConnections float64
		lockWaits         float64
		maxConnections    float64
		// 会话明细可读性三件套：随主查询带回，避免为权限自检多一次往返。
		// 语义见 monitor_pg_activity_priv.go —— 缺权限时 pg_stat_activity 中
		// 其他会话的列会是 NULL，带列过滤的 COUNT 会静默偏小而不是报错。
		isSuperUser    bool
		hasStatsRole   bool
		hasMonitorRole bool
		// 实例角色探测三件套：同样随主查询带回。
		// pg_is_in_recovery() 无权限门槛，是判定真伪的唯一权威依据；
		// pg_stat_replication 受 security-restricted 约束，无 pg_read_all_stats /
		// pg_monitor / 超级用户权限时返回零行而非报错，"看不见"与"没有下游"无法区分，
		// 所以下面用 CASE 把不可信的情况直接折算成 -1（ReplicaCountUnknown），
		// 由 inferPostgresRole 按"未知"处理，绝不把看不见推断成单机。
		inRecovery   bool
		slotCount    int
		replicaCount int
		// 附件「操作行数」：pg_stat_database 的五个元组累计计数器，与主查询同一次往返取回
		tupInserted float64
		tupUpdated  float64
		tupDeleted  float64
		tupFetched  float64
		tupReturned float64
	)
	mainSQL := `
		SELECT
			EXTRACT(EPOCH FROM now() - pg_postmaster_start_time()) AS uptime_seconds,
			COALESCE(SUM(xact_commit + xact_rollback), 0) AS tx_total,
			COALESCE(SUM(xact_commit), 0) AS commit_total,
			COALESCE(SUM(xact_rollback), 0) AS rollback_total,
			COALESCE(SUM(deadlocks), 0) AS deadlock_total,
			COALESCE(SUM(tup_returned + tup_fetched), 0) AS read_rows_total,
			COALESCE(SUM(tup_inserted + tup_updated + tup_deleted), 0) AS write_rows_total,
			COALESCE(SUM(tup_inserted), 0) AS tup_inserted_total,
			COALESCE(SUM(tup_updated), 0) AS tup_updated_total,
			COALESCE(SUM(tup_deleted), 0) AS tup_deleted_total,
			COALESCE(SUM(tup_fetched), 0) AS tup_fetched_total,
			COALESCE(SUM(tup_returned), 0) AS tup_returned_total,
			COALESCE(SUM(blks_read), 0) AS blks_read_total,
			COALESCE(SUM(blks_hit), 0) AS blks_hit_total,
			(SELECT current_setting('block_size')::bigint) AS block_size,
			(SELECT COUNT(*) FROM pg_stat_activity) AS connections,
			(SELECT COUNT(*) FROM pg_stat_activity WHERE state = 'active') AS active_connections,
			(SELECT COUNT(*) FROM pg_locks WHERE NOT granted) AS lock_waits,
			(SELECT current_setting('max_connections')::bigint) AS max_connections,
			COALESCE((SELECT rolsuper FROM pg_roles WHERE rolname = current_user), false) AS is_superuser,
			pg_has_role(current_user, 'pg_read_all_stats', 'MEMBER') AS has_pg_read_all_stats,
			pg_has_role(current_user, 'pg_monitor', 'MEMBER') AS has_pg_monitor,
			pg_is_in_recovery() AS in_recovery,
			(SELECT COUNT(*) FROM pg_replication_slots) AS slot_count,
			CASE WHEN
				COALESCE((SELECT rolsuper FROM pg_roles WHERE rolname = current_user), false)
				OR pg_has_role(current_user, 'pg_read_all_stats', 'MEMBER')
				OR pg_has_role(current_user, 'pg_monitor', 'MEMBER')
			THEN (SELECT COUNT(*) FROM pg_stat_replication) ELSE -1 END AS replica_count
		FROM pg_stat_database
		WHERE datname NOT IN ('template0', 'template1')
	`
	if err := conn.QueryRow(childCtx, mainSQL).Scan(
		&uptime, &txTotal, &commitTotal, &rollbackTotal, &deadlockTotal,
		&readRowsTotal, &writeRowsTotal,
		&tupInserted, &tupUpdated, &tupDeleted, &tupFetched, &tupReturned,
		&blocksRead, &blocksHit,
		&blockSize, &connections, &activeConnections, &lockWaits, &maxConnections,
		&isSuperUser, &hasStatsRole, &hasMonitorRole,
		&inRecovery, &slotCount, &replicaCount,
	); err != nil {
		return nil, fmt.Errorf("PostgreSQL 指标采集失败：%s", err.Error())
	}
	activityPriv := pgActivityReadabilityOf(isSuperUser, hasStatsRole, hasMonitorRole)

	// 实例角色：决定哪些指标在这个拓扑下"压根不存在"（见 monitor_role_scope.go）。
	// 结果必须在采集侧就地回写 insight_instances.instance_role：
	// 画像采集按小时节流等不起，而角色一旦变化（如单机改成主从），
	// 采集范围与展示口径必须立刻跟上。写回失败只记录日志，绝不影响本轮指标。
	syncInstanceRole(row, inferPostgresRole(pgRoleSignals{
		InRecovery:   inRecovery,
		SlotCount:    slotCount,
		ReplicaCount: replicaCount,
	}))
	// 会话明细不可读时，所有依赖被屏蔽列的统一停用原因（详见 pgActivityDenialReason）。
	activityDenialReason := ""
	if !activityPriv.DetailReadable {
		activityDenialReason = pgActivityDenialReason(activityPriv)
	}

	// QPS 只有装了 pg_stat_statements 才拿得到。
	// 关键：拿不到时标 unsupported，绝不返回 0 —— 这是历史上最危险的一类错觉。
	callsTotal := 0.0
	qpsQuality := QualityOK
	qpsReason := ""
	if wantMetric(keys, MetricPerfQPS) {
		if queryErr := conn.QueryRow(childCtx,
			`SELECT COALESCE(SUM(calls), 0) FROM pg_stat_statements`).Scan(&callsTotal); queryErr != nil {
			qpsQuality = QualityUnsupported
			qpsReason = "未安装扩展 pg_stat_statements，无法统计语句调用次数"
		}
	}

	counters := map[string]float64{
		MetricPerfQPS:        callsTotal,
		MetricPerfTPS:        txTotal,
		MetricPerfReadRows:   readRowsTotal,
		MetricPerfWriteRows:  writeRowsTotal,
		MetricPerfReadBytes:  blocksRead * blockSize,
		MetricPerfWriteBytes: 0,
		MetricEngDeadlocks:   deadlockTotal,
	}

	// 附件「每秒事务数 / 操作行数」：同源于 pg_stat_database，全部是累计计数器。
	// 只在进入采集范围时才入表，避免给不需要的实例白算速率。
	if wantMetric(keys, MetricEngXactCommitRate) {
		counters[MetricEngXactCommitRate] = commitTotal
	}
	if wantMetric(keys, MetricEngXactRollbackRate) {
		counters[MetricEngXactRollbackRate] = rollbackTotal
	}
	if wantMetric(keys, MetricEngTupInsertedRate) {
		counters[MetricEngTupInsertedRate] = tupInserted
	}
	if wantMetric(keys, MetricEngTupUpdatedRate) {
		counters[MetricEngTupUpdatedRate] = tupUpdated
	}
	if wantMetric(keys, MetricEngTupDeletedRate) {
		counters[MetricEngTupDeletedRate] = tupDeleted
	}
	if wantMetric(keys, MetricEngTupFetchedRate) {
		counters[MetricEngTupFetchedRate] = tupFetched
	}
	if wantMetric(keys, MetricEngTupReturnedRate) {
		counters[MetricEngTupReturnedRate] = tupReturned
	}

	// WAL 写入量：优先 pg_stat_wal（PG14+），低版本回退到 LSN 差值；
	// LSN 差值同时用于推导 WAL 段切换速率（累计段号 = LSN / wal_segment_size）。
	walBytesTotal := 0.0
	walOK := false
	walFPITotal := 0.0
	fpiOK := false
	walSwitchOK := false
	wantWAL := wantMetric(keys, MetricKernRedoWriteRate) || wantMetric(keys, MetricEngBinlogWALRate) ||
		wantMetric(keys, MetricKernWALFPIRate) || wantMetric(keys, MetricKernWALSwitchRate)
	if wantWAL {
		var walBytes, walFPI float64
		if queryErr := conn.QueryRow(childCtx,
			`SELECT COALESCE(wal_bytes, 0), COALESCE(wal_fpi, 0) FROM pg_stat_wal`).Scan(&walBytes, &walFPI); queryErr == nil {
			walBytesTotal, walFPITotal = walBytes, walFPI
			walOK, fpiOK = true, true
		}
		var lsnDiff float64
		if lsnErr := conn.QueryRow(childCtx,
			`SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), '0/0')::float8`).Scan(&lsnDiff); lsnErr == nil {
			if !walOK {
				walBytesTotal = lsnDiff
				walOK = true
			}
			var segSize float64
			if segErr := conn.QueryRow(childCtx,
				`SELECT pg_size_bytes(current_setting('wal_segment_size'))::float8`).Scan(&segSize); segErr == nil && segSize > 0 {
				counters[MetricKernWALSwitchRate] = lsnDiff / segSize
				walSwitchOK = true
			}
		}
		if walOK {
			counters[MetricKernRedoWriteRate] = walBytesTotal
			counters[MetricEngBinlogWALRate] = walBytesTotal
		}
		if fpiOK {
			counters[MetricKernWALFPIRate] = walFPITotal
		}
	}

	// BGWriter 计数器：刷脏速率（counter）与请求型检查点占比（gauge 输入）
	bgTimed, bgReq, bgFlush := 0.0, 0.0, 0.0
	bgOK := false
	if wantMetric(keys, MetricKernFlushRate) || wantMetric(keys, MetricKernCheckpointReqPct) {
		if queryErr := conn.QueryRow(childCtx,
			`SELECT checkpoints_timed, checkpoints_req, buffers_checkpoint + buffers_clean FROM pg_stat_bgwriter`).
			Scan(&bgTimed, &bgReq, &bgFlush); queryErr == nil {
			bgOK = true
			counters[MetricKernFlushRate] = bgFlush
		}
	}

	rates := directRateCache.compute(row.InstanceID, counters, uptime)

	if wantMetric(keys, MetricPerfQPS) {
		if qpsQuality == QualityOK {
			builder.counter(MetricPerfQPS, rates[MetricPerfQPS], "req/s")
		} else {
			builder.add(MetricPerfQPS, 0, "req/s", qpsQuality, qpsReason)
		}
	}
	if wantMetric(keys, MetricPerfTPS) {
		builder.counter(MetricPerfTPS, rates[MetricPerfTPS], "txn/s")
	}
	if wantMetric(keys, MetricPerfReadRows) {
		builder.counter(MetricPerfReadRows, rates[MetricPerfReadRows], "rows/s")
	}
	if wantMetric(keys, MetricPerfWriteRows) {
		builder.counter(MetricPerfWriteRows, rates[MetricPerfWriteRows], "rows/s")
	}
	if wantMetric(keys, MetricPerfReadBytes) {
		builder.counter(MetricPerfReadBytes, rates[MetricPerfReadBytes], "B/s")
	}
	if wantMetric(keys, MetricPerfWriteBytes) {
		builder.unsupported(MetricPerfWriteBytes, "B/s", "PostgreSQL 未暴露写字节计数器")
	}
	if wantMetric(keys, MetricPerfCacheHitRatio) {
		if blocksHit+blocksRead > 0 {
			builder.ok(MetricPerfCacheHitRatio, blocksHit/(blocksHit+blocksRead)*100, "%")
		} else {
			builder.noData(MetricPerfCacheHitRatio, "%", "实例启动后尚无块读请求")
		}
	}
	if wantMetric(keys, MetricPerfLocks) {
		builder.ok(MetricPerfLocks, lockWaits, "count")
	}
	if wantMetric(keys, MetricPerfConnCount) {
		// COUNT(*) 不看任何"会话内部"列，官方保证会话存在性对所有用户可见 → 恒可信。
		builder.ok(MetricPerfConnCount, connections, "count")
	}
	if wantMetric(keys, MetricPerfActiveConn) {
		// state 列属于"会话明细"，缺权限时其他会话的 state 为 NULL，
		// 过滤结果会静默偏小。宁可标 unsupported，也不给错数。
		if activityPriv.DetailReadable {
			builder.ok(MetricPerfActiveConn, activeConnections, "count")
		} else {
			builder.unsupported(MetricPerfActiveConn, "count", activityDenialReason)
		}
	}
	if wantMetric(keys, MetricPerfMaxConn) {
		if maxConnections > 0 {
			builder.ok(MetricPerfMaxConn, maxConnections, "count")
		} else {
			builder.unsupported(MetricPerfMaxConn, "count", "无法读取 max_connections")
		}
	}
	if maxConnections > 0 {
		if wantMetric(keys, MetricCapConnUsedPct) {
			if activityPriv.DetailReadable {
				builder.ok(MetricCapConnUsedPct, activeConnections/maxConnections*100, "%")
			} else {
				// 分子是活跃连接数，明细不可读时算出来的是假占比。
				builder.unsupported(MetricCapConnUsedPct, "%", activityDenialReason)
			}
		}
		if wantMetric(keys, MetricCapThreadUsedPct) {
			// 分子是会话总数（COUNT(*)），不受明细列屏蔽影响，照常产出。
			builder.ok(MetricCapThreadUsedPct, connections/maxConnections*100, "%")
		}
	} else {
		if wantMetric(keys, MetricCapConnUsedPct) {
			builder.unsupported(MetricCapConnUsedPct, "%", "最大连接数不可用，无法计算占比")
		}
		if wantMetric(keys, MetricCapThreadUsedPct) {
			builder.unsupported(MetricCapThreadUsedPct, "%", "最大连接数不可用，无法计算占比")
		}
	}

	// PG 独有且致命的指标：事务 ID 回卷
	if wantMetric(keys, MetricRiskXIDAge) {
		var xidAge float64
		if queryErr := conn.QueryRow(childCtx,
			`SELECT COALESCE(MAX(age(datfrozenxid)), 0) FROM pg_database`).Scan(&xidAge); queryErr == nil {
			builder.ok(MetricRiskXIDAge, xidAge, "count")
		} else {
			builder.add(MetricRiskXIDAge, 0, "count", QualityError, "事务ID年龄查询失败："+trimError(queryErr))
		}
	}
	if wantMetric(keys, MetricPerfLongTrx) {
		// xact_start 同样属于被屏蔽列：缺权限时"没有活跃事务"本来也是 NULL，
		// 两者无从区分，只能按不可读处理。
		if activityPriv.DetailReadable {
			var longTrx float64
			if queryErr := conn.QueryRow(childCtx, `
			SELECT COUNT(*) FROM pg_stat_activity
			WHERE xact_start IS NOT NULL AND now() - xact_start > interval '60 seconds'
		`).Scan(&longTrx); queryErr == nil {
				builder.ok(MetricPerfLongTrx, longTrx, "count")
			} else {
				builder.add(MetricPerfLongTrx, 0, "count", QualityError, "长事务查询失败："+trimError(queryErr))
			}
		} else {
			builder.unsupported(MetricPerfLongTrx, "count", activityDenialReason)
		}
	}
	if wantMetric(keys, MetricRiskNoPKTables) {
		var noPK float64
		if queryErr := conn.QueryRow(childCtx, `
			SELECT count(*) FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.relkind = 'r' AND n.nspname NOT IN ('pg_catalog','information_schema')
			  AND NOT EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.oid AND i.indisprimary)
		`).Scan(&noPK); queryErr == nil {
			builder.ok(MetricRiskNoPKTables, noPK, "count")
		} else {
			builder.add(MetricRiskNoPKTables, 0, "count", QualityError, "无主键表统计失败："+trimError(queryErr))
		}
	}

	appendPostgresExtendedSamples(builder, childCtx, conn, commitTotal, rollbackTotal, rates, keys, walOK, fpiOK, walSwitchOK, bgOK, bgTimed, bgReq, activityPriv)
	appendPostgresAttachmentSamples(builder, childCtx, conn, rates, keys, activityPriv)
	appendPostgresReplicationSamples(builder, childCtx, conn, keys)
	return builder.list(), nil
}

// appendPostgresAttachmentSamples 产出《PostgreSQL 监控指标》附件定义的指标项：
// 延迟（逻辑复制 / APPLY / 发送）、长事务、慢查询、膨胀点、每秒事务数、操作行数、会话连接。
//
// 与既有 PG 采集的分工：主查询负责累计计数器（这里只做速率换算），本函数负责
// 瞬时状态类（pg_stat_activity）与复制类（pg_stat_replication / pg_replication_slots）。
//
// 约束沿用既有约定：
//   - 一次往返取回一组指标（100 实例规模下往返次数是要害）；
//   - 拿不到就明说 —— unsupported / no_data / error + 原因，绝不返回 0 冒充"很健康"；
//   - 附件要求 PostgreSQL 10 – 18：低版本缺列会查询报错，统一转成带版本提示的 error。
func appendPostgresAttachmentSamples(
	builder *sampleBuilder,
	ctx context.Context,
	conn *pgx.Conn,
	rates map[string]float64,
	keys []string,
	priv pgActivityReadability,
) {
	wantAny := func(candidates ...string) bool {
		for _, key := range candidates {
			if wantMetric(keys, key) {
				return true
			}
		}
		return false
	}

	// ---------------- 每秒事务数 / 操作行数（累计计数器 → 速率） ----------------
	rateMetrics := []struct {
		key  string
		unit string
	}{
		{MetricEngXactCommitRate, "count/s"},
		{MetricEngXactRollbackRate, "count/s"},
		{MetricEngTupInsertedRate, "rows/s"},
		{MetricEngTupUpdatedRate, "rows/s"},
		{MetricEngTupDeletedRate, "rows/s"},
		{MetricEngTupFetchedRate, "rows/s"},
		{MetricEngTupReturnedRate, "rows/s"},
	}
	for _, m := range rateMetrics {
		if wantMetric(keys, m.key) {
			builder.counter(m.key, rates[m.key], m.unit)
		}
	}

	// ---------------- 长事务 / 慢查询 / 会话连接（pg_stat_activity 瞬时快照） ----------------
	// 八个计数合并成一条 SQL：阈值判断用 FILTER，避免 8 次往返。
	// state_change / query_start 与 now() 的差值即"已空闲/已执行"时长，
	// 与附件口径一致（idle in transaction 看 state_change，活跃查询看 query_start）。
	activityMetrics := []struct {
		key  string
		unit string
	}{
		{MetricEngIdleInTrx1s, "count"},
		{MetricEngIdleInTrx3s, "count"},
		{MetricEngIdleInTrx5s, "count"},
		{MetricEngLongQuery1s, "count"},
		{MetricEngLongQuery3s, "count"},
		{MetricEngLongQuery5s, "count"},
		{MetricPerfIdleInTrxCount, "count"},
		{MetricPerfWaitingConnCount, "count"},
	}
	activityKeys := make([]string, 0, len(activityMetrics))
	for _, m := range activityMetrics {
		activityKeys = append(activityKeys, m.key)
	}
	if wantAny(activityKeys...) {
		// 这八项全部依赖 state / state_change / query_start / wait_event 中的一列或多列。
		// 明细不可读时整块停用：它们的共同特征是"COUNT 带列过滤"，
		// 列被置 NULL 的结果是**静默返回偏小值（常为 0）**而非报错，
		// 一旦放行就会在页面上表现为"永远没有慢查询、永远没有长事务"。
		if !priv.DetailReadable {
			for _, m := range activityMetrics {
				if wantMetric(keys, m.key) {
					builder.unsupported(m.key, m.unit, pgActivityDenialReason(priv))
				}
			}
		} else {
			var idle1s, idle3s, idle5s, query1s, query3s, query5s, idleTrx, waiting float64
			err := conn.QueryRow(ctx, `
			SELECT
				COUNT(*) FILTER (WHERE state = 'idle in transaction' AND now() - state_change > interval '1 second'),
				COUNT(*) FILTER (WHERE state = 'idle in transaction' AND now() - state_change > interval '3 seconds'),
				COUNT(*) FILTER (WHERE state = 'idle in transaction' AND now() - state_change > interval '5 seconds'),
				COUNT(*) FILTER (WHERE state = 'active' AND now() - query_start > interval '1 second'),
				COUNT(*) FILTER (WHERE state = 'active' AND now() - query_start > interval '3 seconds'),
				COUNT(*) FILTER (WHERE state = 'active' AND now() - query_start > interval '5 seconds'),
				COUNT(*) FILTER (WHERE state = 'idle in transaction'),
				COUNT(*) FILTER (WHERE wait_event IS NOT NULL AND backend_type = 'client backend')
			FROM pg_stat_activity`).
				Scan(&idle1s, &idle3s, &idle5s, &query1s, &query3s, &query5s, &idleTrx, &waiting)
			if err != nil {
				reason := "pg_stat_activity 查询失败：" + trimError(err)
				for _, m := range activityMetrics {
					if wantMetric(keys, m.key) {
						builder.add(m.key, 0, m.unit, QualityError, reason)
					}
				}
			} else {
				values := []float64{idle1s, idle3s, idle5s, query1s, query3s, query5s, idleTrx, waiting}
				for i, m := range activityMetrics {
					if wantMetric(keys, m.key) {
						builder.ok(m.key, values[i], m.unit)
					}
				}
			}
		}
	}

	// ---------------- 膨胀点 ----------------
	// 最老事务（backend_xid / backend_xmin 非空）与最老的未决 2PC 事务取更早者，
	// 与当前时间的差即"多久以前的垃圾可以被回收"。least() 在 PG 下忽略 NULL，
	// 两个源都没有时结果为 NULL → 标 no_data（表示当前无膨胀风险，不是 0）。
	if wantMetric(keys, MetricEngSwellTimeSeconds) {
		// backend_xid / backend_xmin 是被屏蔽列的典型成员，
		// 后果与长事务一致：过滤后恒无匹配 → 恒返回"无膨胀风险"。
		if !priv.DetailReadable {
			builder.unsupported(MetricEngSwellTimeSeconds, "s", pgActivityDenialReason(priv))
		} else {
			var swell sql.NullFloat64
			if err := conn.QueryRow(ctx, `
			WITH oldest AS (
				SELECT min(xact_start) AS m
				FROM pg_stat_activity
				WHERE backend_xid IS NOT NULL OR backend_xmin IS NOT NULL
			), oldest_2pc AS (
				SELECT min(prepared) AS m
				FROM pg_prepared_xacts
			)
			SELECT EXTRACT(EPOCH FROM (now() - least(oldest.m, oldest_2pc.m)))
			FROM oldest, oldest_2pc`).Scan(&swell); err != nil {
				builder.add(MetricEngSwellTimeSeconds, 0, "s", QualityError, "膨胀点查询失败："+trimError(err))
			} else if !swell.Valid {
				builder.noData(MetricEngSwellTimeSeconds, "s", "当前无活跃事务与未决 2PC 事务")
			} else {
				builder.ok(MetricEngSwellTimeSeconds, swell.Float64, "s")
			}
		}
	}

	// ---------------- 延迟：备库 APPLY 延迟 / 发送延迟（主库视角） ----------------
	// 附件原文给的两条 SQL 有一处笔误：(sent_lsn - replay_lsn) 在 PG 下 pg_lsn 没有减号运算，
	// 且 EXTRACT(EPOCH FROM numeric) 也不合法 —— 正确写法是 pg_wal_lsn_diff() 取字节差。
	// 这里按正确方言实现，单位保持字节（展示层换算成 MB）。
	if wantAny(MetricEngReplayLagSeconds, MetricEngSentLagBytes) {
		var replayLag, sentLag sql.NullFloat64
		var replicaCount int
		err := conn.QueryRow(ctx, `
			SELECT
				MAX(EXTRACT(EPOCH FROM replay_lag)),
				MAX(pg_wal_lsn_diff(sent_lsn, replay_lsn)),
				COUNT(*)
			FROM pg_stat_replication`).Scan(&replayLag, &sentLag, &replicaCount)
		switch {
		case err != nil:
			reason := "pg_stat_replication 不可用（需 PostgreSQL 10+ 且具备 pg_monitor 权限）：" + trimError(err)
			if wantMetric(keys, MetricEngReplayLagSeconds) {
				builder.add(MetricEngReplayLagSeconds, 0, "s", QualityError, reason)
			}
			if wantMetric(keys, MetricEngSentLagBytes) {
				builder.add(MetricEngSentLagBytes, 0, "B", QualityError, reason)
			}
		case replicaCount == 0:
			note := "该实例无流复制备库（pg_stat_replication 为空），主库视角的备库延迟不适用"
			if wantMetric(keys, MetricEngReplayLagSeconds) {
				builder.unsupported(MetricEngReplayLagSeconds, "s", note)
			}
			if wantMetric(keys, MetricEngSentLagBytes) {
				builder.unsupported(MetricEngSentLagBytes, "B", note)
			}
		default:
			if wantMetric(keys, MetricEngReplayLagSeconds) {
				if replayLag.Valid {
					builder.ok(MetricEngReplayLagSeconds, replayLag.Float64, "s")
				} else {
					builder.noData(MetricEngReplayLagSeconds, "s", "备库尚未上报 replay_lag（刚建立复制或未开启 track_commit_timestamp 之外的场景）")
				}
			}
			if wantMetric(keys, MetricEngSentLagBytes) {
				if sentLag.Valid {
					builder.ok(MetricEngSentLagBytes, sentLag.Float64, "B")
				} else {
					builder.noData(MetricEngSentLagBytes, "B", "备库尚未回放，无法计算 sent_lsn 与 replay_lsn 的差值")
				}
			}
		}
	}

	// ---------------- 延迟：逻辑复制槽 WAL 堆积 ----------------
	// 与「复制槽堆积」（eng.repl_slot_lag_bytes，restart_lsn、全部槽位）互补：
	// 这里按附件口径只取逻辑槽的 confirmed_flush_lsn 落后量。
	if wantMetric(keys, MetricEngLogicalRepLagBytes) {
		var lag sql.NullFloat64
		var slotCount int
		if err := conn.QueryRow(ctx, `
			SELECT
				MAX(pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn)),
				COUNT(*)
			FROM pg_replication_slots
			WHERE slot_type = 'logical'`).Scan(&lag, &slotCount); err != nil {
			builder.add(MetricEngLogicalRepLagBytes, 0, "B", QualityError,
				"pg_replication_slots 不可用（需 PostgreSQL 10+ 且具备 pg_monitor 权限）："+trimError(err))
		} else if slotCount == 0 {
			builder.noData(MetricEngLogicalRepLagBytes, "B", "当前实例无逻辑复制槽")
		} else if !lag.Valid {
			builder.noData(MetricEngLogicalRepLagBytes, "B", "逻辑复制槽尚未产生 confirmed_flush_lsn（订阅端未确认消费）")
		} else {
			builder.ok(MetricEngLogicalRepLagBytes, lag.Float64, "B")
		}
	}
}

// appendPostgresExtendedSamples 产出引擎层扩展与内核层（PostgreSQL 方言）指标。
// 与 MySQL 对称：方言不适用 → unsupported；权限/版本不足 → 显式 error 或 unsupported + 原因。
func appendPostgresExtendedSamples(
	builder *sampleBuilder,
	ctx context.Context,
	conn *pgx.Conn,
	commitTotal, rollbackTotal float64,
	rates map[string]float64,
	keys []string,
	walOK, fpiOK, walSwitchOK, bgOK bool,
	bgTimed, bgReq float64,
	priv pgActivityReadability,
) {
	if wantMetric(keys, MetricEngSlowQueries) {
		builder.unsupported(MetricEngSlowQueries, "count/s", "PostgreSQL 无全局慢查询计数器（需 log_min_duration_statement 配合日志解析，后续版本接入）")
	}
	if wantMetric(keys, MetricEngAbortedConns) {
		builder.unsupported(MetricEngAbortedConns, "count/s", "PostgreSQL 未暴露连接中止计数器")
	}
	if wantMetric(keys, MetricEngBinlogWALRate) {
		if walOK {
			builder.counter(MetricEngBinlogWALRate, rates[MetricEngBinlogWALRate], "B/s")
		} else {
			builder.add(MetricEngBinlogWALRate, 0, "B/s", QualityError, "pg_stat_wal 与 WAL LSN 均不可读（权限不足）")
		}
	}
	if wantMetric(keys, MetricEngDeadlocks) {
		builder.counter(MetricEngDeadlocks, rates[MetricEngDeadlocks], "count/s")
	}
	if wantMetric(keys, MetricEngSemiSyncStatus) {
		var syncCount, replicaCount float64
		if queryErr := conn.QueryRow(ctx, `
			SELECT
				COUNT(*) FILTER (WHERE sync_state IN ('sync', 'quorum')),
				COUNT(*)
			FROM pg_stat_replication`).Scan(&syncCount, &replicaCount); queryErr != nil {
			builder.add(MetricEngSemiSyncStatus, 0, "", QualityError, "复制状态查询失败："+trimError(queryErr))
		} else if replicaCount == 0 {
			builder.unsupported(MetricEngSemiSyncStatus, "", "未配置流复制备库")
		} else if syncCount > 0 {
			builder.ok(MetricEngSemiSyncStatus, 1, "")
		} else {
			builder.add(MetricEngSemiSyncStatus, 0, "", QualityOK, "当前为异步复制（无 sync/quorum 备库）")
		}
	}
	if wantMetric(keys, MetricEngLargestTable) {
		var size float64
		if queryErr := conn.QueryRow(ctx, `
			SELECT COALESCE(MAX(pg_total_relation_size(c.oid)), 0)
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.relkind = 'r' AND n.nspname NOT IN ('pg_catalog', 'information_schema')`).Scan(&size); queryErr == nil {
			builder.ok(MetricEngLargestTable, size, "B")
		} else {
			builder.add(MetricEngLargestTable, 0, "B", QualityError, "最大表统计失败："+trimError(queryErr))
		}
	}
	if wantMetric(keys, MetricEngReplSlotLag) {
		var lag sql.NullFloat64
		if queryErr := conn.QueryRow(ctx, `
			SELECT MAX(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn))
			FROM pg_replication_slots`).Scan(&lag); queryErr != nil {
			builder.add(MetricEngReplSlotLag, 0, "B", QualityError, "复制槽查询失败："+trimError(queryErr))
		} else if !lag.Valid {
			builder.noData(MetricEngReplSlotLag, "B", "当前实例无复制槽")
		} else {
			builder.ok(MetricEngReplSlotLag, lag.Float64, "B")
		}
	}

	// ---------------- 内核层（PostgreSQL） ----------------
	if wantMetric(keys, MetricKernRedoWriteRate) {
		if walOK {
			builder.counter(MetricKernRedoWriteRate, rates[MetricKernRedoWriteRate], "B/s")
		} else {
			builder.add(MetricKernRedoWriteRate, 0, "B/s", QualityError, "pg_stat_wal 与 WAL LSN 均不可读（权限不足）")
		}
	}
	if wantMetric(keys, MetricKernWALSwitchRate) {
		if walSwitchOK {
			builder.counter(MetricKernWALSwitchRate, rates[MetricKernWALSwitchRate], "count/s")
		} else {
			builder.add(MetricKernWALSwitchRate, 0, "count/s", QualityError, "WAL LSN 不可读，无法推导段切换速率")
		}
	}
	if wantMetric(keys, MetricKernWALFPIRate) {
		if fpiOK {
			builder.counter(MetricKernWALFPIRate, rates[MetricKernWALFPIRate], "count/s")
		} else {
			builder.unsupported(MetricKernWALFPIRate, "count/s", "PostgreSQL 14 以下无 pg_stat_wal 视图")
		}
	}
	if wantMetric(keys, MetricKernFlushRate) {
		if bgOK {
			builder.counter(MetricKernFlushRate, rates[MetricKernFlushRate], "pages/s")
		} else {
			builder.add(MetricKernFlushRate, 0, "pages/s", QualityError, "pg_stat_bgwriter 查询失败")
		}
	}
	if wantMetric(keys, MetricKernCheckpointReqPct) {
		if bgOK && bgTimed+bgReq > 0 {
			builder.ok(MetricKernCheckpointReqPct, bgReq/(bgTimed+bgReq)*100, "%")
		} else if bgOK {
			builder.noData(MetricKernCheckpointReqPct, "%", "实例启动后尚未发生检查点")
		} else {
			builder.add(MetricKernCheckpointReqPct, 0, "%", QualityError, "pg_stat_bgwriter 查询失败")
		}
	}
	if wantMetric(keys, MetricKernDeadTuples) {
		var dead float64
		if queryErr := conn.QueryRow(ctx,
			`SELECT COALESCE(SUM(n_dead_tup), 0) FROM pg_stat_user_tables`).Scan(&dead); queryErr == nil {
			builder.ok(MetricKernDeadTuples, dead, "count")
		} else {
			builder.add(MetricKernDeadTuples, 0, "count", QualityError, "死元组统计失败："+trimError(queryErr))
		}
	}
	if wantMetric(keys, MetricKernAutovacuumWorkers) {
		// 例外之一：只按 backend_type 过滤。backend_type 属于"会话的一般属性"，
		// 官方明确对所有用户可见，因此这一项不受明细权限影响。
		var workers float64
		if queryErr := conn.QueryRow(ctx,
			`SELECT COUNT(*) FROM pg_stat_activity WHERE backend_type = 'autovacuum worker'`).Scan(&workers); queryErr == nil {
			builder.ok(MetricKernAutovacuumWorkers, workers, "count")
		} else {
			builder.add(MetricKernAutovacuumWorkers, 0, "count", QualityError, "autovacuum 进程统计失败："+trimError(queryErr))
		}
	}
	if wantMetric(keys, MetricKernRollbackRatio) {
		if commitTotal+rollbackTotal > 0 {
			builder.ok(MetricKernRollbackRatio, rollbackTotal/(commitTotal+rollbackTotal)*100, "%")
		} else {
			builder.noData(MetricKernRollbackRatio, "%", "实例启动后尚无事务提交或回滚")
		}
	}
	if wantMetric(keys, MetricKernLWLockWaits) {
		// wait_event_type 属于明细列，且"没有等待事件时本来就是 NULL"，
		// 无法用 IS NULL 区分，只能按权限声明判定。
		if priv.DetailReadable {
			var waits float64
			if queryErr := conn.QueryRow(ctx,
				`SELECT COUNT(*) FROM pg_stat_activity WHERE wait_event_type = 'LWLock'`).Scan(&waits); queryErr == nil {
				builder.ok(MetricKernLWLockWaits, waits, "count")
			} else {
				builder.add(MetricKernLWLockWaits, 0, "count", QualityError, "轻量锁统计失败："+trimError(queryErr))
			}
		} else {
			builder.unsupported(MetricKernLWLockWaits, "count", pgActivityDenialReason(priv))
		}
	}

	// InnoDB 专属概念在 PG 下显式标 unsupported（进入采集范围时给出可见原因）
	if wantMetric(keys, MetricKernHistoryList) {
		builder.unsupported(MetricKernHistoryList, "count", "PostgreSQL MVCC 无 undo 历史链概念")
	}
	if wantMetric(keys, MetricKernBufferDirtyPct) {
		builder.unsupported(MetricKernBufferDirtyPct, "%", "需 pg_buffercache 扩展方可统计脏页，暂不采集")
	}
	if wantMetric(keys, MetricKernReadAheadEvict) {
		builder.unsupported(MetricKernReadAheadEvict, "%", "PostgreSQL 无预读逐出计数器")
	}
	if wantMetric(keys, MetricKernPageSplitRate) {
		builder.unsupported(MetricKernPageSplitRate, "count/s", "PostgreSQL 不暴露 B-tree 页分裂计数器")
	}
	if wantMetric(keys, MetricKernTableCacheHit) {
		builder.unsupported(MetricKernTableCacheHit, "%", "PostgreSQL 无表缓存命中计数器")
	}
	if wantMetric(keys, MetricKernDBLWRWriteRate) {
		builder.unsupported(MetricKernDBLWRWriteRate, "pages/s", "PostgreSQL 无 doublewrite 机制（崩溃安全由 full_page_write 保证）")
	}
}

func appendPostgresReplicationSamples(builder *sampleBuilder, ctx context.Context, conn *pgx.Conn, keys []string) {
	wantRepl := wantMetric(keys, MetricReplIORunning) ||
		wantMetric(keys, MetricReplSQLRunning) ||
		wantMetric(keys, MetricReplLagSeconds)
	if !wantRepl {
		return
	}
	// 本机是否有回放进度：有 → 这是备库；无 → 可能是主库
	var replayLag sql.NullFloat64
	replayErr := conn.QueryRow(ctx,
		`SELECT EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp()))`).Scan(&replayLag)

	var replicaCount int
	if countErr := conn.QueryRow(ctx,
		`SELECT COUNT(*) FROM pg_stat_replication`).Scan(&replicaCount); countErr != nil {
		replicaCount = -1
	}

	if replayErr != nil || !replayLag.Valid {
		// 主库（或从未回放过）→ 复制指标不适用，标 unsupported 而不是"中断"
		note := "该实例为主库或未进行流复制回放"
		if replicaCount > 0 {
			note = fmt.Sprintf("该实例为主库，当前有 %d 个备库连接；备库专属指标在此不适用", replicaCount)
		}
		if wantMetric(keys, MetricReplIORunning) {
			builder.unsupported(MetricReplIORunning, "", note)
		}
		if wantMetric(keys, MetricReplSQLRunning) {
			builder.unsupported(MetricReplSQLRunning, "", note)
		}
		if wantMetric(keys, MetricReplLagSeconds) {
			builder.unsupported(MetricReplLagSeconds, "s", note)
		}
		return
	}

	// 备库：回放进度可读即视为复制线程正常
	if wantMetric(keys, MetricReplIORunning) {
		builder.ok(MetricReplIORunning, 1, "")
	}
	if wantMetric(keys, MetricReplSQLRunning) {
		builder.ok(MetricReplSQLRunning, 1, "")
	}
	if wantMetric(keys, MetricReplLagSeconds) {
		builder.ok(MetricReplLagSeconds, replayLag.Float64, "s")
	}
}

// ---------------------------------------------------------------------------
// ClickHouse
// ---------------------------------------------------------------------------

func collectClickHouseSamples(ctx context.Context, row monitorInstanceRow, keys []string) ([]MetricSample, error) {
	password, err := secret.Decrypt(row.Password)
	if err != nil {
		return nil, fmt.Errorf("实例密码解密失败")
	}
	childCtx, cancel := context.WithTimeout(ctx, instanceTimeoutSeconds(row))
	defer cancel()

	// 一轮两条 SQL 走同一条会话连接：改造前是每条 Query 各自 Open+Ping，
	// 即一轮采集在 CK 上留下 2 次握手 + 2 次 Ping，握手成本反而高于查询本身。
	session, err := dao.OpenClickHouseSession(dao.ClickHouseSessionConfig{
		User:     row.User,
		Password: password,
		Host:     row.Hostname,
		Port:     row.Port,
		Database: "default",
		Settings: map[string]any{},
		Ctx:      childCtx,
	})
	if err != nil {
		return nil, fmt.Errorf("ClickHouse 指标采集失败：%s", err.Error())
	}
	defer session.Close()

	builder := newSampleBuilder("direct")
	_, rows, err := session.Query(`
		SELECT
			(SELECT value FROM system.asynchronous_metrics WHERE metric = 'Uptime') AS uptime_seconds,
			sumIf(value, event = 'Query') AS qps_total,
			sumIf(value, event = 'SelectedRows') AS read_rows_total,
			sumIf(value, event = 'InsertedRows') AS write_rows_total,
			sumIf(value, event = 'SelectedBytes') AS read_bytes_total,
			sumIf(value, event = 'InsertedBytes') AS write_bytes_total
		FROM system.events
		WHERE event IN ('Query', 'SelectedRows', 'InsertedRows', 'SelectedBytes', 'InsertedBytes')
	`)
	if err != nil {
		return nil, fmt.Errorf("ClickHouse 指标采集失败：%s", err.Error())
	}
	if rows == nil || len(*rows) == 0 {
		return nil, fmt.Errorf("ClickHouse 指标采集失败：返回空结果")
	}
	first := (*rows)[0]

	connections, active := 0.0, 0.0
	if _, metricRows, metricErr := session.Query(`
		SELECT
			sumIf(value, metric = 'TCPConnection') + sumIf(value, metric = 'HTTPConnection') AS connections,
			sumIf(value, metric = 'Query') AS active_queries
		FROM system.metrics
		WHERE metric IN ('TCPConnection', 'HTTPConnection', 'Query')
	`); metricErr == nil && metricRows != nil && len(*metricRows) > 0 {
		connections = toFloat64((*metricRows)[0]["connections"])
		active = toFloat64((*metricRows)[0]["active_queries"])
	}

	uptime := toFloat64(first["uptime_seconds"])
	rates := directRateCache.compute(row.InstanceID, map[string]float64{
		MetricPerfQPS:        toFloat64(first["qps_total"]),
		MetricPerfReadRows:   toFloat64(first["read_rows_total"]),
		MetricPerfWriteRows:  toFloat64(first["write_rows_total"]),
		MetricPerfReadBytes:  toFloat64(first["read_bytes_total"]),
		MetricPerfWriteBytes: toFloat64(first["write_bytes_total"]),
	}, uptime)

	if wantMetric(keys, MetricPerfQPS) {
		builder.counter(MetricPerfQPS, rates[MetricPerfQPS], "req/s")
	}
	if wantMetric(keys, MetricPerfTPS) {
		builder.unsupported(MetricPerfTPS, "txn/s", "ClickHouse 无事务计数器")
	}
	if wantMetric(keys, MetricPerfReadRows) {
		builder.counter(MetricPerfReadRows, rates[MetricPerfReadRows], "rows/s")
	}
	if wantMetric(keys, MetricPerfWriteRows) {
		builder.counter(MetricPerfWriteRows, rates[MetricPerfWriteRows], "rows/s")
	}
	if wantMetric(keys, MetricPerfReadBytes) {
		builder.counter(MetricPerfReadBytes, rates[MetricPerfReadBytes], "B/s")
	}
	if wantMetric(keys, MetricPerfWriteBytes) {
		builder.counter(MetricPerfWriteBytes, rates[MetricPerfWriteBytes], "B/s")
	}
	if wantMetric(keys, MetricPerfCacheHitRatio) {
		builder.unsupported(MetricPerfCacheHitRatio, "%", "ClickHouse 无缓冲池命中率概念")
	}
	if wantMetric(keys, MetricPerfLocks) {
		builder.unsupported(MetricPerfLocks, "count", "ClickHouse 无行锁等待")
	}
	if wantMetric(keys, MetricPerfConnCount) {
		builder.ok(MetricPerfConnCount, connections, "count")
	}
	if wantMetric(keys, MetricPerfActiveConn) {
		builder.ok(MetricPerfActiveConn, active, "count")
	}
	if wantMetric(keys, MetricPerfMaxConn) {
		builder.unsupported(MetricPerfMaxConn, "count", "ClickHouse 无 max_connections 配置项")
	}
	if wantMetric(keys, MetricCapConnUsedPct) {
		builder.unsupported(MetricCapConnUsedPct, "%", "ClickHouse 无连接上限，占比不适用")
	}
	if wantMetric(keys, MetricCapThreadUsedPct) {
		builder.unsupported(MetricCapThreadUsedPct, "%", "ClickHouse 无连接上限，占比不适用")
	}
	if wantMetric(keys, MetricReplIORunning) {
		builder.unsupported(MetricReplIORunning, "", "ClickHouse 复制机制与主从不同，暂未采集")
	}
	if wantMetric(keys, MetricReplSQLRunning) {
		builder.unsupported(MetricReplSQLRunning, "", "ClickHouse 复制机制与主从不同，暂未采集")
	}
	if wantMetric(keys, MetricReplLagSeconds) {
		builder.unsupported(MetricReplLagSeconds, "s", "ClickHouse 复制机制与主从不同，暂未采集")
	}
	return builder.list(), nil
}

// ---------------------------------------------------------------------------
// SQL Server（sys.dm_os_performance_counters / sys.dm_exec_* / sys.dm_io_virtual_file_stats /
// sys.dm_os_wait_stats 直连采集）
//
// 分组按需查询：会话请求、性能计数器、数据文件 IO、等待统计、CPU 环形缓冲各一条 SQL，
// 由 wantMetric 门控——模板没勾选的分组一次往返都不会发生（100 实例规模下的开销红线）。
//
// 速率口径：dm_os_performance_counters 里以 /sec 结尾的计数器是累计值（PERF_COUNTER_BULK_COUNT），
// 与 dm_io_virtual_file_stats / dm_os_wait_stats 的累计列一样，统一交给 directRateCache
// 按 Δ值/Δt 求每秒速率；首轮以实例启动时长（dm_os_sys_info.sqlserver_start_time）为回退分母。
// ---------------------------------------------------------------------------

// sqlserverPerfCounterKeys 依赖「性能计数器查询」的指标键集合。
var sqlserverPerfCounterKeys = []string{
	MetricEngBatchReqRate, MetricEngSQLCompRate, MetricEngSQLReCompRate, MetricEngLoginsRate,
	MetricPerfTPS, MetricEngDeadlocks, MetricEngLockWaitRate, MetricEngLockTimeoutRate,
	MetricPerfQPS, MetricEngLockAvgWaitMs, MetricPerfCacheHitRatio, MetricEngPageLifeExpectancy,
	MetricEngMemGrantsPending, MetricEngSQLMemoryBytes,
	MetricEngLogGrowths, MetricEngLogFlushWaitRate, MetricEngLogFlushBytesRate, MetricEngLogUsedPct,
	MetricEngDataFileBytes, MetricEngLogFileBytes, MetricEngTempdbUsedBytes, MetricEngTempdbVersionBytes,
}

// wantAnyMetric 任一目标指标在本轮采集范围内即返回 true。
func wantAnyMetric(keys []string, targets ...string) bool {
	for _, target := range targets {
		if wantMetric(keys, target) {
			return true
		}
	}
	return false
}

// sqlserverCounterValue 取某个计数器的值：按 counter_name + instance_name 唯一定位。
// instance 传空串表示取不带实例名的那一行（Buffer Manager / Memory Manager 等实例级计数器）。
func sqlserverCounterValue(counters map[string]map[string]float64, name string, instance string) (float64, bool) {
	byInstance, ok := counters[name]
	if !ok {
		return 0, false
	}
	value, ok := byInstance[instance]
	return value, ok
}

// sqlserverCounterSum 按实例名求和（skipTotal=true 时排除 _Total，避免各库求和后重复计入总计）。
func sqlserverCounterSum(counters map[string]map[string]float64, name string, skipTotal bool) (float64, bool) {
	byInstance, ok := counters[name]
	if !ok {
		return 0, false
	}
	total := 0.0
	found := false
	for instance, value := range byInstance {
		if skipTotal && instance == "_Total" {
			continue
		}
		total += value
		found = true
	}
	return total, found
}

// sqlserverCounterMaxRatioByPair 逐库取「已用 ÷ 总量」占比的最大值（跳过 _Total）。
//
// 为什么不用 Percent Log Used 的 raw fraction 算法（cntr_value / base × 100）：
// SQL Server 在 sys.dm_os_performance_counters 里并不暴露「Percent Log Used base」行
// （该计数器 cntr_type = PERF_RAW_FRACTION = 65536，但分母没有对应计数器），
// 按 value/base 换算恒取不到值 → 指标永远是 no_data。
// 而 Log File(s) Used Size (KB) 与 Log File(s) Size (KB) 一定成对出现，相除即得使用率，
// 且能与 Percent Log Used 互相验证（实测 master 1438/2040 = 70.5%，对上 Percent Log Used = 70）。
func sqlserverCounterMaxRatioByPair(counters map[string]map[string]float64, usedName, totalName string) (float64, bool) {
	usedByName, okUsed := counters[usedName]
	totalByName, okTotal := counters[totalName]
	if !okUsed || !okTotal {
		return 0, false
	}
	maxValue := 0.0
	found := false
	for instance, usedValue := range usedByName {
		if instance == "_Total" {
			continue
		}
		totalValue, ok := totalByName[instance]
		if !ok || totalValue <= 0 {
			continue
		}
		ratio := usedValue / totalValue * 100
		if !found || ratio > maxValue {
			maxValue = ratio
		}
		found = true
	}
	return maxValue, found
}

// sqlserverCounterMax 取各库同名计数器（值本身已是百分比）的最大值，跳过 _Total。
// 仅用于 Percent Log Used 这类 SQL Server 已经算好百分比、可直接读取的计数器。
func sqlserverCounterMax(counters map[string]map[string]float64, name string) (float64, bool) {
	byInstance, ok := counters[name]
	if !ok {
		return 0, false
	}
	maxValue := 0.0
	found := false
	for instance, value := range byInstance {
		if instance == "_Total" {
			continue
		}
		if !found || value > maxValue {
			maxValue = value
		}
		found = true
	}
	return maxValue, found
}

// sqlserverIOLatency 计算数据文件平均 IO 延迟，并说明这个值是哪个口径算出来的。
//
// 优先用本轮增量均值（实时、精准）；本轮没有新增 IO 时退化成实例启动以来的累计均值，
// 而不是直接判 no_data —— 后者会让空闲实例的磁盘健康度长期无从判断。
// Cumulative=true 时调用方必须把口径写进 reason，避免被当成实时值读。
func sqlserverIOLatency(deltaStall, deltaOps, totalStall, totalOps float64) sqlserverLatencyResult {
	if deltaOps > 0 {
		return sqlserverLatencyResult{Value: deltaStall / deltaOps, HasData: true}
	}
	if totalOps > 0 {
		return sqlserverLatencyResult{Value: totalStall / totalOps, HasData: true, Cumulative: true}
	}
	return sqlserverLatencyResult{}
}

type sqlserverLatencyResult struct {
	Value      float64
	HasData    bool
	Cumulative bool // true：本轮无增量，Value 是实例启动以来的累计均值
}

func collectSQLServerSamples(ctx context.Context, row monitorInstanceRow, keys []string) ([]MetricSample, error) {
	password, err := secret.Decrypt(row.Password)
	if err != nil {
		return nil, fmt.Errorf("实例密码解密失败")
	}
	cfg, err := parseExtraConfig(row.ExtraConfig)
	if err != nil {
		return nil, err
	}
	dsn := sqlserverdsn.DSN(row.User, password, row.Hostname, row.Port, getStringConfig(cfg, "database", "master"))

	childCtx, cancel := context.WithTimeout(ctx, instanceTimeoutSeconds(row))
	defer cancel()

	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, fmt.Errorf("SQLServer 指标采集失败：%s", err.Error())
	}
	defer db.Close()

	builder := newSampleBuilder("direct")
	produced := map[string]struct{}{}
	sqlserverUnsupportedReason := func(key string) string {
		switch {
		case strings.HasPrefix(key, "kern."):
			return "SQL Server 无 InnoDB 内核层指标，暂不采集"
		default:
			return "SQL Server 暂未实现该指标采集"
		}
	}

	// ---- uptime：实例启动秒数，首轮 counter 速率的回退分母（与 MySQL 的 Uptime 同角色） ----
	// 查询失败可容忍：首轮速率记 0，下一轮起用真实间隔。
	var uptime float64
	_ = db.QueryRowContext(childCtx,
		`SELECT DATEDIFF(second, sqlserver_start_time, GETDATE()) FROM sys.dm_os_sys_info`).Scan(&uptime)

	counters := map[string]float64{}

	// ---- 会话 / 请求 / 长事务 / 最大连接数：一条 SQL 一次往返 ----
	//
	// 口径说明：
	//   - 连接数     = is_user_process=1 的会话（排除系统后台会话）
	//   - 活跃连接数 = 上述会话中 status='running' 的（等价于 MySQL 的 Threads_running）
	//   - 等待中连接 = dm_exec_requests 里 wait_type 非空的请求（已排除 session_id<=50 的系统请求
	//     与仅代表后台任务计时的 MISCELLANEOUS，否则空闲实例也会被刷成非零）
	//   - 阻塞会话   = dm_exec_requests 里 blocking_session_id<>0 的请求
	//   - 长事务     = 开启超过 60 秒仍未提交的用户事务
	//   - 最大连接数 = user connections 配置；配置为 0（不限制）时按默认值 32767 上报
	var sessions, activeConn, waitingConn, blocking, longTrx, maxConn float64
	sessionErr := ""
	if wantAnyMetric(keys, MetricPerfConnCount, MetricPerfActiveConn, MetricPerfWaitingConnCount,
		MetricPerfLocks, MetricPerfLongTrx, MetricPerfMaxConn, MetricCapConnUsedPct, MetricCapThreadUsedPct) {
		queryErr := db.QueryRowContext(childCtx, `
SELECT
	(SELECT CAST(COUNT(*) AS FLOAT) FROM sys.dm_exec_sessions WHERE is_user_process = 1),
	(SELECT CAST(COUNT(*) AS FLOAT) FROM sys.dm_exec_sessions WHERE is_user_process = 1 AND status = N'running'),
	(SELECT CAST(COUNT(*) AS FLOAT) FROM sys.dm_exec_requests
		WHERE session_id > 50 AND wait_type IS NOT NULL AND wait_type <> N'MISCELLANEOUS'),
	(SELECT CAST(COUNT(*) AS FLOAT) FROM sys.dm_exec_requests WHERE blocking_session_id <> 0),
	(SELECT CAST(COUNT(*) AS FLOAT) FROM sys.dm_tran_active_transactions t
		JOIN sys.dm_tran_session_transactions st ON st.transaction_id = t.transaction_id
		WHERE DATEDIFF(second, t.transaction_begin_time, GETDATE()) > 60),
	(SELECT CAST(ISNULL(NULLIF(MAX(CAST(value_in_use AS BIGINT)), 0), 32767) AS FLOAT)
		FROM sys.configurations WHERE name = N'user connections')
`).Scan(&sessions, &activeConn, &waitingConn, &blocking, &longTrx, &maxConn)
		if queryErr != nil {
			sessionErr = "sys.dm_exec_sessions / sys.dm_exec_requests 查询失败（采集账号需 VIEW SERVER STATE 授权）：" + trimError(queryErr)
		}
	}

	// ---- 性能计数器：一条 SQL 取回全部相关计数器，按 counter_name + instance_name 归类 ----
	// object_name 会带实例名前缀（SQLServer: / MSSQL$XXX:），故用 %: 前缀匹配；
	// 同时用 counter_name 白名单收敛行数，避免 Locks 这类多实例对象一次返回上千行。
	perfCounters := map[string]map[string]float64{}
	perfErr := ""
	if wantAnyMetric(keys, sqlserverPerfCounterKeys...) {
		perfRows, queryErr := db.QueryContext(childCtx, `
SELECT RTRIM(counter_name) AS counter_name, RTRIM(instance_name) AS instance_name,
       CAST(cntr_value AS FLOAT) AS cntr_value
FROM sys.dm_os_performance_counters
WHERE (object_name LIKE N'%:SQL Statistics%' AND counter_name IN (N'Batch Requests/sec', N'SQL Compilations/sec', N'SQL Re-Compilations/sec'))
   OR (object_name LIKE N'%:General Statistics%' AND counter_name = N'Logins/sec')
   OR (object_name LIKE N'%:Buffer Manager%' AND counter_name IN (N'Buffer cache hit ratio', N'Buffer cache hit ratio base', N'Page life expectancy'))
   OR (object_name LIKE N'%:Memory Manager%' AND counter_name IN (N'Memory Grants Pending', N'Total Server Memory (KB)'))
   OR (object_name LIKE N'%:Locks%' AND instance_name = N'_Total' AND counter_name IN (N'Number of Deadlocks/sec', N'Lock Waits/sec', N'Lock Timeouts/sec', N'Average Wait Time (ms)', N'Average Wait Time Base'))
   OR (object_name LIKE N'%:Databases%' AND counter_name IN (N'Transactions/sec', N'Log Growths', N'Log Flush Waits/sec', N'Log Bytes Flushed/sec', N'Percent Log Used', N'Percent Log Used Base', N'Log File(s) Size (KB)', N'Log File(s) Used Size (KB)', N'Data File(s) Size (KB)'))
   OR (object_name LIKE N'%:Transactions%' AND counter_name IN (N'Free Space in tempdb (KB)', N'Version Store Size (KB)'))
`)
		if queryErr != nil {
			perfErr = "sys.dm_os_performance_counters 查询失败（采集账号需 VIEW SERVER STATE 授权）：" + trimError(queryErr)
		} else {
			for perfRows.Next() {
				var counterName, instanceName string
				var value float64
				if scanErr := perfRows.Scan(&counterName, &instanceName, &value); scanErr != nil {
					continue
				}
				counterName = strings.TrimSpace(counterName)
				instanceName = strings.TrimSpace(instanceName)
				if counterName == "" {
					continue
				}
				if _, ok := perfCounters[counterName]; !ok {
					perfCounters[counterName] = map[string]float64{}
				}
				perfCounters[counterName][instanceName] = value
			}
			perfRows.Close()
		}
	}

	// 累计型计数器入速率缓存：只有进了 counters 才会参与 Δ值/Δt。
	if perfErr == "" {
		if v, ok := sqlserverCounterValue(perfCounters, "Batch Requests/sec", ""); ok {
			// perf.qps 与 eng.batch_req_rate 同源：SQL Server 没有 Questions 这类
			// "语句数" 计数器，官方吞吐口径就是 Batch Requests/sec（一次往返里
			// 的批次数）。MySQL 侧 perf.qps 取 Questions，两者地位对等。
			counters[MetricEngBatchReqRate] = v
			counters[MetricPerfQPS] = v
		}
		if v, ok := sqlserverCounterValue(perfCounters, "SQL Compilations/sec", ""); ok {
			counters[MetricEngSQLCompRate] = v
		}
		if v, ok := sqlserverCounterValue(perfCounters, "SQL Re-Compilations/sec", ""); ok {
			counters[MetricEngSQLReCompRate] = v
		}
		if v, ok := sqlserverCounterValue(perfCounters, "Logins/sec", ""); ok {
			counters[MetricEngLoginsRate] = v
		}
		if v, ok := sqlserverCounterValue(perfCounters, "Transactions/sec", "_Total"); ok {
			counters[MetricPerfTPS] = v
		}
		if v, ok := sqlserverCounterValue(perfCounters, "Number of Deadlocks/sec", "_Total"); ok {
			counters[MetricEngDeadlocks] = v
		}
		if v, ok := sqlserverCounterValue(perfCounters, "Lock Waits/sec", "_Total"); ok {
			counters[MetricEngLockWaitRate] = v
		}
		if v, ok := sqlserverCounterValue(perfCounters, "Lock Timeouts/sec", "_Total"); ok {
			counters[MetricEngLockTimeoutRate] = v
		}
		if v, ok := sqlserverCounterSum(perfCounters, "Log Growths", true); ok {
			counters[MetricEngLogGrowths] = v
		}
		if v, ok := sqlserverCounterSum(perfCounters, "Log Flush Waits/sec", true); ok {
			counters[MetricEngLogFlushWaitRate] = v
		}
		if v, ok := sqlserverCounterSum(perfCounters, "Log Bytes Flushed/sec", true); ok {
			counters[MetricEngLogFlushBytesRate] = v
		}
	}

	// ---- 数据文件 IO：累计列求和后入速率缓存 ----
	var ioReadBytes, ioWriteBytes, ioStallReadMs, ioStallWriteMs, ioNumReads, ioNumWrites float64
	ioErr := ""
	if wantAnyMetric(keys, MetricEngIOReadBytesRate, MetricEngIOWriteBytesRate,
		MetricEngIOReadLatencyMs, MetricEngIOWriteLatencyMs) {
		queryErr := db.QueryRowContext(childCtx, `
SELECT ISNULL(SUM(CAST(num_of_bytes_read AS FLOAT)), 0),
       ISNULL(SUM(CAST(num_of_bytes_written AS FLOAT)), 0),
       ISNULL(SUM(CAST(io_stall_read_ms AS FLOAT)), 0),
       ISNULL(SUM(CAST(io_stall_write_ms AS FLOAT)), 0),
       ISNULL(SUM(CAST(num_of_reads AS FLOAT)), 0),
       ISNULL(SUM(CAST(num_of_writes AS FLOAT)), 0)
FROM sys.dm_io_virtual_file_stats(NULL, NULL)
`).Scan(&ioReadBytes, &ioWriteBytes, &ioStallReadMs, &ioStallWriteMs, &ioNumReads, &ioNumWrites)
		if queryErr != nil {
			ioErr = "sys.dm_io_virtual_file_stats 查询失败（采集账号需 VIEW SERVER STATE 授权）：" + trimError(queryErr)
		} else {
			counters["io_read_bytes"] = ioReadBytes
			counters["io_write_bytes"] = ioWriteBytes
			counters["io_stall_read_ms"] = ioStallReadMs
			counters["io_stall_write_ms"] = ioStallWriteMs
			counters["io_num_reads"] = ioNumReads
			counters["io_num_writes"] = ioNumWrites
		}
	}

	// ---- 等待统计：wait_time_ms 累计值（已排除空闲类等待） ----
	waitErr := ""
	if wantMetric(keys, MetricEngWaitTimeMsRate) {
		var waitTimeMs float64
		queryErr := db.QueryRowContext(childCtx, `
SELECT ISNULL(SUM(CAST(wait_time_ms AS FLOAT)), 0)
FROM sys.dm_os_wait_stats
WHERE wait_type <> N'MISCELLANEOUS'
  AND wait_type NOT LIKE N'%SLEEP%'
  AND wait_type NOT LIKE N'%IDLE%'
  AND wait_type NOT LIKE N'%_QUEUE'
  AND wait_type NOT IN (N'CLR_SEMAPHORE', N'SQLTRACE_BUFFER_FLUSH', N'REQUEST_FOR_DEADLOCK_SEARCH',
      N'XE_TIMER_EVENT', N'XE_DISPATCHER_WAIT', N'XE_LIVE_TARGET_TVF', N'BROKER_TASK_STOP',
      N'BROKER_TO_FLUSH', N'BROKER_EVENTHANDLER', N'WAITFOR', N'DIRTY_PAGE_POLL',
      N'DBMIRROR_DBM_MUTEX', N'DBMIRROR_EVENTS_QUEUE', N'DBMIRROR_WORKER_QUEUE', N'DBMIRRORING_CMD',
      N'HADR_FILESTREAM_IOMGR_IOCOMPLETION', N'ONDEMAND_TASK_QUEUE', N'SP_SERVER_DIAGNOSTICS_SLEEP',
      N'QDS_CLEANUP_STALE_QUERIES_TASK_MAIN_LOOP_SLEEP', N'QDS_PERSIST_TASK_MAIN_LOOP_SLEEP',
      N'FT_IFTS_SCHEDULER_IDLE_WAIT', N'CHECKPOINT_QUEUE', N'LOGMGR_QUEUE')
`).Scan(&waitTimeMs)
		if queryErr != nil {
			waitErr = "sys.dm_os_wait_stats 查询失败（采集账号需 VIEW SERVER STATE 授权）：" + trimError(queryErr)
		} else {
			counters["wait_time_ms"] = waitTimeMs
		}
	}

	// ---- 协议流量：SQL Server 没有实例级网络字节计数器 ----
	//
	// sys.dm_os_performance_counters 里带 Bytes 的计数器只属于 Availability Replica
	// （副本间传输）与 Broker/DBM Transport（Service Broker），没有"客户端 ↔ 服务端"
	// 的实例级字节计数，所以只能用 TDS 包数近似：
	//   num_reads  = 服务端从客户端读入的包 → 入方向（对应 MySQL Bytes_received → perf.write_bytes）
	//   num_writes = 服务端写给客户端的包   → 出方向（对应 MySQL Bytes_sent     → perf.read_bytes）
	// 包大小取每个连接各自的 net_packet_size，不用全实例平均值。
	//
	// 两点必须先说清，否则数值会被误读：
	//  1. 这是上界近似 —— TDS 包不一定被填满，短查询会高估；
	//  2. 该 DMV 只统计当前存活连接，连接断开后累计值会回退，此时速率缓存按
	//     current/uptime 兜底，得到的是"实例启动以来均值"而非本轮速率。
	netErr := ""
	if wantAnyMetric(keys, MetricPerfReadBytes, MetricPerfWriteBytes) {
		var bytesIn, bytesOut float64
		queryErr := db.QueryRowContext(childCtx, `
SELECT ISNULL(SUM(CAST(num_reads AS FLOAT) * CAST(net_packet_size AS FLOAT)), 0),
       ISNULL(SUM(CAST(num_writes AS FLOAT) * CAST(net_packet_size AS FLOAT)), 0)
FROM sys.dm_exec_connections
`).Scan(&bytesIn, &bytesOut)
		if queryErr != nil {
			netErr = "sys.dm_exec_connections 查询失败（采集账号需 VIEW SERVER STATE 授权）：" + trimError(queryErr)
		} else {
			counters[MetricPerfWriteBytes] = bytesIn
			counters[MetricPerfReadBytes] = bytesOut
		}
	}

	// ---- 每秒读写行数 ----
	//   - 读行数 = sys.dm_exec_query_stats.total_rows 累计（语句返回给客户端的行数），
	//     与 MySQL 的 Innodb_rows_read 同角色；计划缓存被淘汰时累计值会回退。
	//   - 写行数 = sys.dm_db_index_usage_stats.user_updates 累计，
	//     只取 index_id IN (0,1)（堆 / 聚集索引）：否则一个表有几个二级索引就被重复计几次，
	//     写入量会被放大数倍。语义是"表级写入操作次数"，含 insert / update / delete，
	//     与 MySQL 的 Innodb_rows_inserted+updated+deleted 同角色。
	rowStatErr := ""
	if wantAnyMetric(keys, MetricPerfReadRows, MetricPerfWriteRows) {
		var readRows, writeRows float64
		queryErr := db.QueryRowContext(childCtx, `
SELECT
	(SELECT ISNULL(SUM(CAST(total_rows AS FLOAT)), 0) FROM sys.dm_exec_query_stats),
	(SELECT ISNULL(SUM(CAST(user_updates AS FLOAT)), 0)
		FROM sys.dm_db_index_usage_stats WHERE index_id IN (0, 1))
`).Scan(&readRows, &writeRows)
		if queryErr != nil {
			rowStatErr = "sys.dm_exec_query_stats / sys.dm_db_index_usage_stats 查询失败（采集账号需 VIEW SERVER STATE 授权）：" + trimError(queryErr)
		} else {
			counters[MetricPerfReadRows] = readRows
			counters[MetricPerfWriteRows] = writeRows
		}
	}

	rates := directRateCache.compute(row.InstanceID, counters, uptime)

	// ---- 会话类 ----
	if wantMetric(keys, MetricPerfConnCount) {
		if sessionErr != "" {
			builder.add(MetricPerfConnCount, 0, "count", QualityError, sessionErr)
		} else {
			builder.ok(MetricPerfConnCount, sessions, "count")
		}
		markProduced(produced, MetricPerfConnCount)
	}
	if wantMetric(keys, MetricPerfActiveConn) {
		if sessionErr != "" {
			builder.add(MetricPerfActiveConn, 0, "count", QualityError, sessionErr)
		} else {
			builder.ok(MetricPerfActiveConn, activeConn, "count")
		}
		markProduced(produced, MetricPerfActiveConn)
	}
	if wantMetric(keys, MetricPerfWaitingConnCount) {
		if sessionErr != "" {
			builder.add(MetricPerfWaitingConnCount, 0, "count", QualityError, sessionErr)
		} else {
			builder.ok(MetricPerfWaitingConnCount, waitingConn, "count")
		}
		markProduced(produced, MetricPerfWaitingConnCount)
	}
	if wantMetric(keys, MetricPerfLocks) {
		if sessionErr != "" {
			builder.add(MetricPerfLocks, 0, "count", QualityError, sessionErr)
		} else {
			builder.ok(MetricPerfLocks, blocking, "count")
		}
		markProduced(produced, MetricPerfLocks)
	}
	if wantMetric(keys, MetricPerfLongTrx) {
		if sessionErr != "" {
			builder.add(MetricPerfLongTrx, 0, "count", QualityError, sessionErr)
		} else {
			builder.ok(MetricPerfLongTrx, longTrx, "count")
		}
		markProduced(produced, MetricPerfLongTrx)
	}
	if wantMetric(keys, MetricPerfMaxConn) {
		if sessionErr != "" {
			builder.add(MetricPerfMaxConn, 0, "count", QualityError, sessionErr)
		} else {
			builder.ok(MetricPerfMaxConn, maxConn, "count")
		}
		markProduced(produced, MetricPerfMaxConn)
	}
	if wantMetric(keys, MetricCapConnUsedPct) || wantMetric(keys, MetricCapThreadUsedPct) {
		switch {
		case sessionErr != "":
			if wantMetric(keys, MetricCapConnUsedPct) {
				builder.add(MetricCapConnUsedPct, 0, "%", QualityError, sessionErr)
			}
			if wantMetric(keys, MetricCapThreadUsedPct) {
				builder.add(MetricCapThreadUsedPct, 0, "%", QualityError, sessionErr)
			}
		case maxConn <= 0:
			if wantMetric(keys, MetricCapConnUsedPct) {
				builder.unsupported(MetricCapConnUsedPct, "%", "未取到 user connections 上限，无法计算占比")
			}
			if wantMetric(keys, MetricCapThreadUsedPct) {
				builder.unsupported(MetricCapThreadUsedPct, "%", "未取到 user connections 上限，无法计算占比")
			}
		default:
			if wantMetric(keys, MetricCapConnUsedPct) {
				builder.ok(MetricCapConnUsedPct, activeConn/maxConn*100, "%")
			}
			if wantMetric(keys, MetricCapThreadUsedPct) {
				builder.ok(MetricCapThreadUsedPct, sessions/maxConn*100, "%")
			}
		}
		markProduced(produced, MetricCapConnUsedPct)
		markProduced(produced, MetricCapThreadUsedPct)
	}

	// ---- 吞吐类（计数器速率） ----
	emitRate := func(key string, unit string, emptyReason string) {
		if _, ok := counters[key]; ok {
			builder.counter(key, rates[key], unit)
			return
		}
		if perfErr != "" {
			builder.add(key, 0, unit, QualityError, perfErr)
			return
		}
		builder.noData(key, unit, emptyReason)
	}
	// emitRateWithErr 与 emitRate 同形，只是错误来源换成各自的查询（协议流量走
	// dm_exec_connections、行数走 dm_exec_query_stats / dm_db_index_usage_stats），
	// 不能一律报成 perfErr —— 那样会把"行数 DMV 查不到"误报成"性能计数器查不到"。
	emitRateWithErr := func(key string, unit string, queryErr string, emptyReason string) {
		if _, ok := counters[key]; ok {
			builder.counter(key, rates[key], unit)
			return
		}
		if queryErr != "" {
			builder.add(key, 0, unit, QualityError, queryErr)
			return
		}
		builder.noData(key, unit, emptyReason)
	}
	if wantMetric(keys, MetricPerfQPS) {
		emitRate(MetricPerfQPS, "req/s", "sys.dm_os_performance_counters 未返回 SQL Statistics / Batch Requests/sec")
		markProduced(produced, MetricPerfQPS)
	}
	if wantMetric(keys, MetricPerfReadBytes) {
		emitRateWithErr(MetricPerfReadBytes, "B/s", netErr,
			"sys.dm_exec_connections 未返回连接包数（实例当前无客户端连接）")
		markProduced(produced, MetricPerfReadBytes)
	}
	if wantMetric(keys, MetricPerfWriteBytes) {
		emitRateWithErr(MetricPerfWriteBytes, "B/s", netErr,
			"sys.dm_exec_connections 未返回连接包数（实例当前无客户端连接）")
		markProduced(produced, MetricPerfWriteBytes)
	}
	if wantMetric(keys, MetricPerfReadRows) {
		emitRateWithErr(MetricPerfReadRows, "rows/s", rowStatErr,
			"sys.dm_exec_query_stats 未返回累计行数（计划缓存为空或实例刚启动）")
		markProduced(produced, MetricPerfReadRows)
	}
	if wantMetric(keys, MetricPerfWriteRows) {
		emitRateWithErr(MetricPerfWriteRows, "rows/s", rowStatErr,
			"sys.dm_db_index_usage_stats 未返回堆 / 聚集索引写入次数（实例重启后计数器会清零）")
		markProduced(produced, MetricPerfWriteRows)
	}
	if wantMetric(keys, MetricEngBatchReqRate) {
		emitRate(MetricEngBatchReqRate, "req/s", "sys.dm_os_performance_counters 未返回 Batch Requests/sec")
		markProduced(produced, MetricEngBatchReqRate)
	}
	if wantMetric(keys, MetricPerfTPS) {
		emitRate(MetricPerfTPS, "txn/s", "sys.dm_os_performance_counters 未返回 Databases / Transactions/sec")
		markProduced(produced, MetricPerfTPS)
	}
	if wantMetric(keys, MetricEngSQLCompRate) {
		emitRate(MetricEngSQLCompRate, "count/s", "sys.dm_os_performance_counters 未返回 SQL Compilations/sec")
		markProduced(produced, MetricEngSQLCompRate)
	}
	if wantMetric(keys, MetricEngSQLReCompRate) {
		emitRate(MetricEngSQLReCompRate, "count/s", "sys.dm_os_performance_counters 未返回 SQL Re-Compilations/sec")
		markProduced(produced, MetricEngSQLReCompRate)
	}
	if wantMetric(keys, MetricEngLoginsRate) {
		emitRate(MetricEngLoginsRate, "count/s", "sys.dm_os_performance_counters 未返回 Logins/sec")
		markProduced(produced, MetricEngLoginsRate)
	}
	if wantMetric(keys, MetricEngDeadlocks) {
		emitRate(MetricEngDeadlocks, "count/s", "sys.dm_os_performance_counters 未返回 Locks / Number of Deadlocks/sec")
		markProduced(produced, MetricEngDeadlocks)
	}
	if wantMetric(keys, MetricEngLockWaitRate) {
		emitRate(MetricEngLockWaitRate, "count/s", "sys.dm_os_performance_counters 未返回 Locks / Lock Waits/sec")
		markProduced(produced, MetricEngLockWaitRate)
	}
	if wantMetric(keys, MetricEngLockTimeoutRate) {
		emitRate(MetricEngLockTimeoutRate, "count/s", "sys.dm_os_performance_counters 未返回 Locks / Lock Timeouts/sec")
		markProduced(produced, MetricEngLockTimeoutRate)
	}
	if wantMetric(keys, MetricEngLogGrowths) {
		emitRate(MetricEngLogGrowths, "count/s", "sys.dm_os_performance_counters 未返回 Databases / Log Growths")
		markProduced(produced, MetricEngLogGrowths)
	}
	if wantMetric(keys, MetricEngLogFlushWaitRate) {
		emitRate(MetricEngLogFlushWaitRate, "count/s", "sys.dm_os_performance_counters 未返回 Databases / Log Flush Waits/sec")
		markProduced(produced, MetricEngLogFlushWaitRate)
	}
	if wantMetric(keys, MetricEngLogFlushBytesRate) {
		emitRate(MetricEngLogFlushBytesRate, "B/s", "sys.dm_os_performance_counters 未返回 Databases / Log Bytes Flushed/sec")
		markProduced(produced, MetricEngLogFlushBytesRate)
	}

	// ---- 平均锁等待时长（PERF_AVERAGE_BULK：必须配合 Average Wait Time Base 使用） ----
	if wantMetric(keys, MetricEngLockAvgWaitMs) {
		switch {
		case perfErr != "":
			builder.add(MetricEngLockAvgWaitMs, 0, "ms", QualityError, perfErr)
		default:
			value, okValue := sqlserverCounterValue(perfCounters, "Average Wait Time (ms)", "_Total")
			base, okBase := sqlserverCounterValue(perfCounters, "Average Wait Time Base", "_Total")
			if !okValue || !okBase || base <= 0 {
				builder.noData(MetricEngLockAvgWaitMs, "ms", "sys.dm_os_performance_counters 未返回 Average Wait Time (ms) 及其 Base")
			} else {
				builder.ok(MetricEngLockAvgWaitMs, value/base, "ms")
			}
		}
		markProduced(produced, MetricEngLockAvgWaitMs)
	}

	// ---- 缓冲池命中率（raw fraction：cntr_value / base × 100） ----
	if wantMetric(keys, MetricPerfCacheHitRatio) {
		switch {
		case perfErr != "":
			builder.add(MetricPerfCacheHitRatio, 0, "%", QualityError, perfErr)
		default:
			value, okValue := sqlserverCounterValue(perfCounters, "Buffer cache hit ratio", "")
			base, okBase := sqlserverCounterValue(perfCounters, "Buffer cache hit ratio base", "")
			if !okValue || !okBase || base <= 0 {
				builder.noData(MetricPerfCacheHitRatio, "%", "sys.dm_os_performance_counters 未返回 Buffer cache hit ratio 及其 base")
			} else {
				builder.ok(MetricPerfCacheHitRatio, value/base*100, "%")
			}
		}
		markProduced(produced, MetricPerfCacheHitRatio)
	}

	// ---- 瞬时量：PLE / 内存授予等待 / 数据库内存占用 ----
	if wantMetric(keys, MetricEngPageLifeExpectancy) {
		switch {
		case perfErr != "":
			builder.add(MetricEngPageLifeExpectancy, 0, "s", QualityError, perfErr)
		default:
			if v, ok := sqlserverCounterValue(perfCounters, "Page life expectancy", ""); ok {
				builder.ok(MetricEngPageLifeExpectancy, v, "s")
			} else {
				builder.noData(MetricEngPageLifeExpectancy, "s", "sys.dm_os_performance_counters 未返回 Page life expectancy")
			}
		}
		markProduced(produced, MetricEngPageLifeExpectancy)
	}
	if wantMetric(keys, MetricEngMemGrantsPending) {
		switch {
		case perfErr != "":
			builder.add(MetricEngMemGrantsPending, 0, "count", QualityError, perfErr)
		default:
			if v, ok := sqlserverCounterValue(perfCounters, "Memory Grants Pending", ""); ok {
				builder.ok(MetricEngMemGrantsPending, v, "count")
			} else {
				builder.noData(MetricEngMemGrantsPending, "count", "sys.dm_os_performance_counters 未返回 Memory Grants Pending")
			}
		}
		markProduced(produced, MetricEngMemGrantsPending)
	}
	if wantMetric(keys, MetricEngSQLMemoryBytes) {
		switch {
		case perfErr != "":
			builder.add(MetricEngSQLMemoryBytes, 0, "B", QualityError, perfErr)
		default:
			if v, ok := sqlserverCounterValue(perfCounters, "Total Server Memory (KB)", ""); ok {
				builder.ok(MetricEngSQLMemoryBytes, v*1024, "B")
			} else {
				builder.noData(MetricEngSQLMemoryBytes, "B", "sys.dm_os_performance_counters 未返回 Total Server Memory (KB)")
			}
		}
		markProduced(produced, MetricEngSQLMemoryBytes)
	}

	// ---- 日志文件使用率（各库取最大值） ----
	//
	// 口径见 sqlserverCounterMaxRatioByPair 的注释：先用两个 KB 计数器相除，
	// 取不到再回退读 Percent Log Used 的原文（该值本身就是整数百分比）。
	if wantMetric(keys, MetricEngLogUsedPct) {
		switch {
		case perfErr != "":
			builder.add(MetricEngLogUsedPct, 0, "%", QualityError, perfErr)
		default:
			if v, ok := sqlserverCounterMaxRatioByPair(perfCounters, "Log File(s) Used Size (KB)", "Log File(s) Size (KB)"); ok {
				builder.ok(MetricEngLogUsedPct, v, "%")
			} else if v, ok := sqlserverCounterMax(perfCounters, "Percent Log Used"); ok {
				builder.ok(MetricEngLogUsedPct, v, "%")
			} else {
				builder.noData(MetricEngLogUsedPct, "%", "sys.dm_os_performance_counters 未返回 Databases / Log File(s) Used Size (KB) 与 Percent Log Used")
			}
		}
		markProduced(produced, MetricEngLogUsedPct)
	}

	// ---- 容量：数据 / 日志文件大小（_Total 实例） ----
	emitFileSize := func(key string, counter string) {
		switch {
		case perfErr != "":
			builder.add(key, 0, "B", QualityError, perfErr)
		default:
			if v, ok := sqlserverCounterValue(perfCounters, counter, "_Total"); ok {
				builder.ok(key, v*1024, "B")
			} else {
				builder.noData(key, "B", "sys.dm_os_performance_counters 未返回 Databases / "+counter)
			}
		}
	}
	if wantMetric(keys, MetricEngDataFileBytes) {
		emitFileSize(MetricEngDataFileBytes, "Data File(s) Size (KB)")
		markProduced(produced, MetricEngDataFileBytes)
	}
	if wantMetric(keys, MetricEngLogFileBytes) {
		emitFileSize(MetricEngLogFileBytes, "Log File(s) Size (KB)")
		markProduced(produced, MetricEngLogFileBytes)
	}

	// ---- TempDB：已用 = 数据文件大小 − 空闲空间；版本存储单列 ----
	if wantMetric(keys, MetricEngTempdbUsedBytes) {
		switch {
		case perfErr != "":
			builder.add(MetricEngTempdbUsedBytes, 0, "B", QualityError, perfErr)
		default:
			size, okSize := sqlserverCounterValue(perfCounters, "Data File(s) Size (KB)", "tempdb")
			free, okFree := sqlserverCounterValue(perfCounters, "Free Space in tempdb (KB)", "")
			if !okSize || !okFree {
				builder.noData(MetricEngTempdbUsedBytes, "B", "sys.dm_os_performance_counters 未返回 tempdb 文件大小或 Free Space in tempdb")
			} else {
				used := (size - free) * 1024
				if used < 0 {
					used = 0
				}
				builder.ok(MetricEngTempdbUsedBytes, used, "B")
			}
		}
		markProduced(produced, MetricEngTempdbUsedBytes)
	}
	if wantMetric(keys, MetricEngTempdbVersionBytes) {
		switch {
		case perfErr != "":
			builder.add(MetricEngTempdbVersionBytes, 0, "B", QualityError, perfErr)
		default:
			if v, ok := sqlserverCounterValue(perfCounters, "Version Store Size (KB)", ""); ok {
				builder.ok(MetricEngTempdbVersionBytes, v*1024, "B")
			} else {
				builder.noData(MetricEngTempdbVersionBytes, "B", "sys.dm_os_performance_counters 未返回 Version Store Size (KB)")
			}
		}
		markProduced(produced, MetricEngTempdbVersionBytes)
	}

	// ---- 数据文件 IO：吞吐取速率、延迟取「等待时长增量 ÷ 次数增量」 ----
	if wantMetric(keys, MetricEngIOReadBytesRate) {
		if ioErr != "" {
			builder.add(MetricEngIOReadBytesRate, 0, "B/s", QualityError, ioErr)
		} else {
			builder.counter(MetricEngIOReadBytesRate, rates["io_read_bytes"], "B/s")
		}
		markProduced(produced, MetricEngIOReadBytesRate)
	}
	if wantMetric(keys, MetricEngIOWriteBytesRate) {
		if ioErr != "" {
			builder.add(MetricEngIOWriteBytesRate, 0, "B/s", QualityError, ioErr)
		} else {
			builder.counter(MetricEngIOWriteBytesRate, rates["io_write_bytes"], "B/s")
		}
		markProduced(produced, MetricEngIOWriteBytesRate)
	}
	// 延迟用「每操作平均等待毫秒」而非「每秒等待毫秒」：后者随并发数线性放大，
	// 无法判断单次 IO 是否变慢。分子分母都来自同一轮速率缓存，间隔一致、可除。
	//
	// 本轮 Δops = 0 时（空闲实例，或读全部命中缓冲池——实测一台有 2 万批次请求的
	// 实例连续两轮 num_of_reads 一动不动）直接标 no_data，指标会长期空白、看不出
	// 磁盘是否健康。这里退化成「实例启动以来的累计平均延迟」：值仍然可用，
	// reason 里写明口径，免得被当成实时值读。
	emitIOLatency := func(key string, stallKey string, opsKey string, totalStall, totalOps float64, emptyReason string) {
		if ioErr != "" {
			builder.add(key, 0, "ms", QualityError, ioErr)
			return
		}
		latency := sqlserverIOLatency(rates[stallKey], rates[opsKey], totalStall, totalOps)
		switch {
		case !latency.HasData:
			builder.noData(key, "ms", emptyReason)
		case latency.Cumulative:
			builder.add(key, latency.Value, "ms", QualityOK,
				"本轮无新增数据文件 IO，取实例启动以来累计平均延迟")
		default:
			builder.ok(key, latency.Value, "ms")
		}
	}
	if wantMetric(keys, MetricEngIOReadLatencyMs) {
		emitIOLatency(MetricEngIOReadLatencyMs, "io_stall_read_ms", "io_num_reads",
			ioStallReadMs, ioNumReads, "实例启动以来尚未产生数据文件读 IO")
		markProduced(produced, MetricEngIOReadLatencyMs)
	}
	if wantMetric(keys, MetricEngIOWriteLatencyMs) {
		emitIOLatency(MetricEngIOWriteLatencyMs, "io_stall_write_ms", "io_num_writes",
			ioStallWriteMs, ioNumWrites, "实例启动以来尚未产生数据文件写 IO")
		markProduced(produced, MetricEngIOWriteLatencyMs)
	}

	// ---- 等待统计 ----
	if wantMetric(keys, MetricEngWaitTimeMsRate) {
		if waitErr != "" {
			builder.add(MetricEngWaitTimeMsRate, 0, "ms/s", QualityError, waitErr)
		} else {
			builder.counter(MetricEngWaitTimeMsRate, rates["wait_time_ms"], "ms/s")
		}
		markProduced(produced, MetricEngWaitTimeMsRate)
	}

	// ---- 数据库 CPU 占用率（环形缓冲里的最新一条 SystemHealth 记录） ----
	if wantMetric(keys, MetricEngSQLCPUPct) {
		var cpuUtil sql.NullFloat64
		queryErr := db.QueryRowContext(childCtx, `
SELECT TOP 1 record.value('(./Record/SchedulerMonitorEvent/SystemHealth/ProcessUtilization)[1]', 'int') AS sql_cpu_util
FROM (
    SELECT TOP 1 CAST(record AS XML) AS record
    FROM sys.dm_os_ring_buffers
    WHERE ring_buffer_type = N'RING_BUFFER_SCHEDULER_MONITOR'
      AND record LIKE N'%<SystemHealth>%'
    ORDER BY timestamp DESC
) AS src`).Scan(&cpuUtil)
		switch {
		case queryErr != nil:
			builder.add(MetricEngSQLCPUPct, 0, "%", QualityError,
				"sys.dm_os_ring_buffers 查询失败（采集账号需 VIEW SERVER STATE 授权）："+trimError(queryErr))
		case !cpuUtil.Valid:
			builder.noData(MetricEngSQLCPUPct, "%", "环形缓冲中暂无 SystemHealth 记录（实例刚启动或缓冲区已被覆盖）")
		default:
			builder.ok(MetricEngSQLCPUPct, cpuUtil.Float64, "%")
		}
		markProduced(produced, MetricEngSQLCPUPct)
	}

	// ---- 收尾：keys 内未产出的指标全部落 unsupported，明细页可见、可排查 ----
	todo := keys
	if keys == nil {
		todo = loadEnabledMetricKeys(row.DbType)
	}
	for _, key := range todo {
		if _, ok := produced[key]; ok {
			continue
		}
		// avail.up / avail.collect_ms 由采集框架负责产出，引擎层不能碰：
		// 同批次 upsert 主键相同的行后者胜出，这里补一条 unsupported 会把框架刚写的
		// avail.up=1 覆盖掉，页面就变成"实例不可达"。
		if isFrameworkMetric(key) {
			continue
		}
		builder.unsupported(key, metricUnitOf(key), sqlserverUnsupportedReason(key))
	}
	return builder.list(), nil
}

// ---------------------------------------------------------------------------
// Oracle（v$sysstat / v$session / v$resource_limit / dba_* 直连采集）
// ---------------------------------------------------------------------------

func collectOracleSamples(ctx context.Context, row monitorInstanceRow, keys []string) ([]MetricSample, error) {
	password, err := secret.Decrypt(row.Password)
	if err != nil {
		return nil, fmt.Errorf("实例密码解密失败")
	}
	cfg, err := parseExtraConfig(row.ExtraConfig)
	if err != nil {
		return nil, err
	}
	serviceName := getStringConfig(cfg, "service_name", "")
	if serviceName == "" {
		return nil, fmt.Errorf("Oracle 服务名不能为空（请在实例扩展参数中配置 service_name）")
	}

	childCtx, cancel := context.WithTimeout(ctx, instanceTimeoutSeconds(row))
	defer cancel()

	dsn := go_ora.BuildUrl(row.Hostname, row.Port, serviceName, row.User, password, nil)
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		return nil, fmt.Errorf("Oracle 指标采集失败：%s", err.Error())
	}
	defer db.Close()

	builder := newSampleBuilder("direct")
	produced := map[string]struct{}{}
	// oracleUnsupportedReason 未产出指标的分类原因：Oracle 与 MySQL 的计数器语义差异大，
	// 统一一句"暂未采集"会让明细页的 — 不可排查，按前缀给差异化解释。
	oracleUnsupportedReason := func(key string) string {
		switch {
		case key == MetricPerfReadRows || key == MetricPerfWriteRows:
			return "Oracle 不提供行级操作计数器（v$sysstat 只有语句级统计）"
		case strings.HasPrefix(key, "eng."):
			return "Oracle 无同语义计数器，暂不采集"
		case strings.HasPrefix(key, "kern."):
			return "Oracle 内核层指标暂未适配"
		default:
			return "Oracle 暂未实现该指标采集"
		}
	}

	// uptime：实例启动秒数，首轮 counter 速率的回退分母（与 MySQL 的 Uptime 同角色）。
	// 查询失败可容忍：首轮速率记 0，下一轮起用真实间隔。
	var uptime float64
	_ = db.QueryRowContext(childCtx,
		`SELECT (SYSDATE - startup_time) * 86400 FROM v$instance`).Scan(&uptime)

	// ---- v$sysstat：QPS / TPS / 命中率 / 协议流量 / redo 的唯一来源 ----
	sysstat := map[string]float64{}
	sysstatErr := ""
	if wantMetric(keys, MetricPerfQPS) || wantMetric(keys, MetricPerfTPS) ||
		wantMetric(keys, MetricPerfCacheHitRatio) || wantMetric(keys, MetricPerfReadBytes) ||
		wantMetric(keys, MetricPerfWriteBytes) || wantMetric(keys, MetricKernRedoWriteRate) {
		statRows, queryErr := db.QueryContext(childCtx, `SELECT name, value FROM v$sysstat WHERE name IN (
			'execute count','user commits','user rollbacks',
			'db block gets','consistent gets','physical reads',
			'bytes sent via SQL*Net to client','bytes received via SQL*Net from client',
			'redo size')`)
		if queryErr != nil {
			sysstatErr = "v$sysstat 查询失败（采集账号需 V_$SYSSTAT 授权）：" + trimError(queryErr)
		} else {
			for statRows.Next() {
				var name string
				var value float64
				if scanErr := statRows.Scan(&name, &value); scanErr == nil {
					sysstat[name] = value
				}
			}
			statRows.Close()
		}
	}

	counters := map[string]float64{}
	if sysstatErr == "" {
		if v, ok := sysstat["execute count"]; ok {
			counters[MetricPerfQPS] = v
		}
		if commits, ok1 := sysstat["user commits"]; ok1 {
			rollbacks := sysstat["user rollbacks"]
			counters[MetricPerfTPS] = commits + rollbacks
		}
		if v, ok := sysstat["bytes sent via SQL*Net to client"]; ok {
			counters[MetricPerfReadBytes] = v
		}
		if v, ok := sysstat["bytes received via SQL*Net from client"]; ok {
			counters[MetricPerfWriteBytes] = v
		}
		if v, ok := sysstat["redo size"]; ok {
			counters[MetricKernRedoWriteRate] = v
		}
	}
	rates := directRateCache.compute(row.InstanceID, counters, uptime)

	if wantMetric(keys, MetricPerfQPS) {
		if _, ok := counters[MetricPerfQPS]; ok {
			builder.counter(MetricPerfQPS, rates[MetricPerfQPS], "req/s")
		} else if sysstatErr != "" {
			builder.add(MetricPerfQPS, 0, "req/s", QualityError, sysstatErr)
		} else {
			builder.noData(MetricPerfQPS, "req/s", "v$sysstat 未返回 execute count")
		}
		markProduced(produced, MetricPerfQPS)
	}
	if wantMetric(keys, MetricPerfTPS) {
		if _, ok := counters[MetricPerfTPS]; ok {
			builder.counter(MetricPerfTPS, rates[MetricPerfTPS], "txn/s")
		} else if sysstatErr != "" {
			builder.add(MetricPerfTPS, 0, "txn/s", QualityError, sysstatErr)
		} else {
			builder.noData(MetricPerfTPS, "txn/s", "v$sysstat 未返回 user commits / user rollbacks")
		}
		markProduced(produced, MetricPerfTPS)
	}
	if wantMetric(keys, MetricPerfCacheHitRatio) {
		dbGets, ok1 := sysstat["db block gets"]
		consGets, ok2 := sysstat["consistent gets"]
		physReads, ok3 := sysstat["physical reads"]
		switch {
		case sysstatErr != "":
			builder.add(MetricPerfCacheHitRatio, 0, "%", QualityError, sysstatErr)
		case !ok1 || !ok2 || !ok3:
			builder.noData(MetricPerfCacheHitRatio, "%", "v$sysstat 缺少 buffer cache 统计项")
		case dbGets+consGets <= 0:
			builder.noData(MetricPerfCacheHitRatio, "%", "实例启动后尚无逻辑读")
		default:
			builder.ok(MetricPerfCacheHitRatio, (1-physReads/(dbGets+consGets))*100, "%")
		}
		markProduced(produced, MetricPerfCacheHitRatio)
	}
	if wantMetric(keys, MetricPerfReadBytes) {
		if _, ok := counters[MetricPerfReadBytes]; ok {
			builder.counter(MetricPerfReadBytes, rates[MetricPerfReadBytes], "B/s")
		} else if sysstatErr != "" {
			builder.add(MetricPerfReadBytes, 0, "B/s", QualityError, sysstatErr)
		}
		markProduced(produced, MetricPerfReadBytes)
	}
	if wantMetric(keys, MetricPerfWriteBytes) {
		if _, ok := counters[MetricPerfWriteBytes]; ok {
			builder.counter(MetricPerfWriteBytes, rates[MetricPerfWriteBytes], "B/s")
		} else if sysstatErr != "" {
			builder.add(MetricPerfWriteBytes, 0, "B/s", QualityError, sysstatErr)
		}
		markProduced(produced, MetricPerfWriteBytes)
	}
	if wantMetric(keys, MetricKernRedoWriteRate) {
		if _, ok := counters[MetricKernRedoWriteRate]; ok {
			builder.counter(MetricKernRedoWriteRate, rates[MetricKernRedoWriteRate], "B/s")
		} else if sysstatErr != "" {
			builder.add(MetricKernRedoWriteRate, 0, "B/s", QualityError, sysstatErr)
		}
		markProduced(produced, MetricKernRedoWriteRate)
	}

	// ---- sessions / processes 上限：走 v$parameter，不再用 v$resource_limit ----
	//
	// v$resource_limit 是容器级固定表：采集连的是 PDB（service_name=orclpdb）时它恒返回
	// 0 行 —— SQL 不报错、只是空集，于是既没有查询错误也没有任何行，三个 cap.* 与
	// perf.max_conn_count 全部静默落到「无法读取上限」。v$parameter 在 CDB 根与 PDB 内
	// 都可用，取值与 show parameter 一致。
	//
	// value 是 VARCHAR2 且 0 表示"不限"，一律按字符串扫再解析，不再依赖驱动做类型转换。
	type resLimit struct{ current, limit float64 }
	limits := map[string]resLimit{}
	paramErr := ""
	needLimits := wantMetric(keys, MetricPerfMaxConn) || wantMetric(keys, MetricCapConnUsedPct) ||
		wantMetric(keys, MetricCapThreadUsedPct) || wantMetric(keys, MetricCapProcessUsedPct)
	if needLimits {
		paramRows, queryErr := db.QueryContext(childCtx, `SELECT LOWER(name), value
			FROM v$parameter WHERE LOWER(name) IN ('sessions','processes')`)
		if queryErr != nil {
			paramErr = "v$parameter 查询失败（采集账号需 SELECT ANY DICTIONARY）：" + trimError(queryErr)
		} else {
			for paramRows.Next() {
				var name, raw string
				if scanErr := paramRows.Scan(&name, &raw); scanErr != nil {
					continue
				}
				if value, ok := parseOracleLimitValue(raw); ok {
					limits[strings.ToLower(name)] = resLimit{limit: value}
				}
			}
			paramRows.Close()
		}
	}
	// processes 的实际用量单独取：v$resource_limit.current_utilization 在 PDB 里同样读不到，
	// 而 v$process 可读。只在真要算进程使用率时才多跑这一趟。
	if wantMetric(keys, MetricCapProcessUsedPct) {
		if p, ok := limits["processes"]; ok && p.limit > 0 {
			var used float64
			if queryErr := db.QueryRowContext(childCtx,
				`SELECT COUNT(*) FROM v$process`).Scan(&used); queryErr == nil {
				limits["processes"] = resLimit{current: used, limit: p.limit}
			}
		}
	}
	if wantMetric(keys, MetricPerfMaxConn) {
		if s, ok := limits["sessions"]; ok && s.limit > 0 {
			builder.ok(MetricPerfMaxConn, s.limit, "count")
		} else if paramErr != "" {
			builder.add(MetricPerfMaxConn, 0, "count", QualityError, paramErr)
		} else {
			builder.unsupported(MetricPerfMaxConn, "count", "v$parameter 中 sessions 为 0（实例未设上限）")
		}
		markProduced(produced, MetricPerfMaxConn)
	}
	if wantMetric(keys, MetricCapProcessUsedPct) {
		p := limits["processes"]
		switch {
		case paramErr != "":
			builder.add(MetricCapProcessUsedPct, 0, "%", QualityError, paramErr)
		case p.limit <= 0:
			builder.unsupported(MetricCapProcessUsedPct, "%", "v$parameter 中 processes 为 0（实例未设上限）")
		case p.current <= 0:
			builder.unsupported(MetricCapProcessUsedPct, "%", "无法读取 v$process 进程数")
		default:
			builder.ok(MetricCapProcessUsedPct, p.current/p.limit*100, "%")
		}
		markProduced(produced, MetricCapProcessUsedPct)
	}

	// ---- v$session：连接 / 活跃 / 锁等待 / 长事务，一次往返聚合 ----
	var connections, active, blocking, longTrx float64
	sessionErr := ""
	needSession := wantMetric(keys, MetricPerfConnCount) || wantMetric(keys, MetricPerfActiveConn) ||
		wantMetric(keys, MetricPerfLocks) || wantMetric(keys, MetricPerfLongTrx) ||
		wantMetric(keys, MetricCapConnUsedPct) || wantMetric(keys, MetricCapThreadUsedPct)
	if needSession {
		queryErr := db.QueryRowContext(childCtx, `SELECT
			COUNT(*),
			NVL(SUM(CASE WHEN status = 'ACTIVE' THEN 1 ELSE 0 END), 0),
			NVL(SUM(CASE WHEN blocking_session IS NOT NULL THEN 1 ELSE 0 END), 0),
			NVL(SUM(CASE WHEN taddr IS NOT NULL AND last_call_et > 60 THEN 1 ELSE 0 END), 0)
			FROM v$session WHERE type = 'USER'`).
			Scan(&connections, &active, &blocking, &longTrx)
		if queryErr != nil {
			sessionErr = "v$session 查询失败（采集账号需 V_$SESSION 授权）：" + trimError(queryErr)
		}
	}
	sessionErrorOr := func(reason string) (string, string) {
		if sessionErr != "" {
			return QualityError, sessionErr
		}
		return QualityNoData, reason
	}
	if wantMetric(keys, MetricPerfConnCount) {
		if sessionErr != "" {
			builder.add(MetricPerfConnCount, 0, "count", QualityError, sessionErr)
		} else {
			builder.ok(MetricPerfConnCount, connections, "count")
		}
		markProduced(produced, MetricPerfConnCount)
	}
	if wantMetric(keys, MetricPerfActiveConn) {
		if sessionErr != "" {
			builder.add(MetricPerfActiveConn, 0, "count", QualityError, sessionErr)
		} else {
			builder.ok(MetricPerfActiveConn, active, "count")
		}
		markProduced(produced, MetricPerfActiveConn)
	}
	if wantMetric(keys, MetricPerfLocks) {
		if q, reason := sessionErrorOr("无阻塞会话"); q == QualityError {
			builder.add(MetricPerfLocks, 0, "count", q, reason)
		} else {
			builder.ok(MetricPerfLocks, blocking, "count")
		}
		markProduced(produced, MetricPerfLocks)
	}
	if wantMetric(keys, MetricPerfLongTrx) {
		// 口径：挂着活跃事务（taddr 非空）且 last_call_et 超 60 秒的用户会话。
		// last_call_et 是"距最后一次活动"的秒数，对空闲中的事务会话即事务挂起时长。
		if q, reason := sessionErrorOr("无长事务"); q == QualityError {
			builder.add(MetricPerfLongTrx, 0, "count", q, reason)
		} else {
			builder.ok(MetricPerfLongTrx, longTrx, "count")
		}
		markProduced(produced, MetricPerfLongTrx)
	}
	if wantMetric(keys, MetricCapConnUsedPct) || wantMetric(keys, MetricCapThreadUsedPct) {
		sessionsLimit, hasLimit := limits["sessions"]
		if sessionErr != "" {
			if wantMetric(keys, MetricCapConnUsedPct) {
				builder.add(MetricCapConnUsedPct, 0, "%", QualityError, sessionErr)
			}
			if wantMetric(keys, MetricCapThreadUsedPct) {
				builder.add(MetricCapThreadUsedPct, 0, "%", QualityError, sessionErr)
			}
		} else if !hasLimit || sessionsLimit.limit <= 0 {
			reason := paramErr
			if reason == "" {
				reason = "v$parameter 中 sessions 为 0（实例未设上限），无法计算占比"
			}
			if wantMetric(keys, MetricCapConnUsedPct) {
				builder.unsupported(MetricCapConnUsedPct, "%", reason)
			}
			if wantMetric(keys, MetricCapThreadUsedPct) {
				builder.unsupported(MetricCapThreadUsedPct, "%", reason)
			}
		} else {
			if wantMetric(keys, MetricCapConnUsedPct) {
				builder.ok(MetricCapConnUsedPct, active/sessionsLimit.limit*100, "%")
			}
			if wantMetric(keys, MetricCapThreadUsedPct) {
				builder.ok(MetricCapThreadUsedPct, connections/sessionsLimit.limit*100, "%")
			}
		}
		markProduced(produced, MetricCapConnUsedPct)
		markProduced(produced, MetricCapThreadUsedPct)
	}

	// ---- 表空间使用率（dba_data_files ∩ dba_free_space，取最大值；不含临时表空间） ----
	if wantMetric(keys, MetricCapTablespaceUsedPct) {
		var maxUsed float64
		queryErr := db.QueryRowContext(childCtx, `SELECT NVL(MAX(used_pct), 0) FROM (
			SELECT (d.bytes - NVL(f.total_free, 0)) / d.bytes * 100 AS used_pct
			FROM (SELECT tablespace_name, SUM(bytes) bytes FROM dba_data_files GROUP BY tablespace_name) d
			JOIN (SELECT tablespace_name, SUM(bytes) total_free FROM dba_free_space GROUP BY tablespace_name) f
			  ON f.tablespace_name = d.tablespace_name
			WHERE d.bytes > 0)`).Scan(&maxUsed)
		if queryErr != nil {
			builder.add(MetricCapTablespaceUsedPct, 0, "%", QualityError,
				"dba_data_files/dba_free_space 查询失败（采集账号需 SELECT ANY DICTIONARY 或对应视图授权）："+trimError(queryErr))
		} else {
			builder.ok(MetricCapTablespaceUsedPct, maxUsed, "%")
		}
		markProduced(produced, MetricCapTablespaceUsedPct)
	}

	// ---- 收尾：keys 内未产出的指标全部落 unsupported，明细页可见、可排查 ----
	todo := keys
	if keys == nil {
		todo = loadEnabledMetricKeys(row.DbType)
	}
	for _, key := range todo {
		if _, ok := produced[key]; ok {
			continue
		}
		// avail.up / avail.collect_ms 由采集框架负责产出，引擎层不能碰：
		// 同一批次 upsert 主键相同的行后者胜出，这里补一条 unsupported 会把框架刚写的
		// avail.up=1 覆盖掉，页面就变成"实例不可达/暂未实现"。
		if isFrameworkMetric(key) {
			continue
		}
		builder.unsupported(key, metricUnitOf(key), oracleUnsupportedReason(key))
	}
	return builder.list(), nil
}

// markProduced 记录一个指标已在本轮产出（含 error/noData 占位，避免收尾再标 unsupported）。
func markProduced(set map[string]struct{}, key string) {
	set[key] = struct{}{}
}

// isFrameworkMetric 判断指标是否由采集框架（collectInstanceSamples）统一产出。
// 引擎采集器对这类指标一律跳过，既不能标 unsupported，也不该重复上报。
func isFrameworkMetric(key string) bool {
	return key == MetricAvailUp || key == MetricAvailCollectMs
}

// parseOracleLimitValue 解析 v$parameter.value 里的上限值。
// 该列是 VARCHAR2：正常为十进制上限；"0" 表示未设上限（Oracle 的 UNLIMITED 语义），
// 空串、非数字、负数一律视为不可用 —— 宁缺勿错，不能当 0 上报。
func parseOracleLimitValue(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

// unsupportedEngineSamples 用于"该引擎只实现了部分指标"的场景：
// 已有数值 → ok；其余引擎层指标 → unsupported（页面显示 — 并说明原因）。
func unsupportedEngineSamples(source string, dbType string, values map[string]float64) []MetricSample {
	builder := newSampleBuilder(source)
	unitOf := func(metricKey string) string {
		for _, item := range builtinMetricCatalog {
			if item.MetricKey == metricKey {
				return item.Unit
			}
		}
		return ""
	}
	for _, item := range builtinMetricCatalog {
		if !collectScopeEnabled(item.Scope) {
			continue
		}
		// 框架级指标（avail.*）由 collectInstanceSamples 统一产出，见 isFrameworkMetric。
		if isFrameworkMetric(item.MetricKey) {
			continue
		}
		if value, ok := values[item.MetricKey]; ok {
			builder.ok(item.MetricKey, value, unitOf(item.MetricKey))
			continue
		}
		if !engineInScope(dbType, item.EngineScope) {
			continue
		}
		builder.unsupported(item.MetricKey, item.Unit, fmt.Sprintf("%s 当前仅支持采集连接数类指标", dbType))
	}
	return builder.list()
}

// ---------------------------------------------------------------------------
// 资源层（Prometheus，node_exporter 口径）
// ---------------------------------------------------------------------------

// resourceSampleKeys 主机层采集注册表：key = 指标键，unit = 上报单位。
//
// 提到包级是为了能被测试直接校验 —— 「指标字典里加了 host 指标、却忘了在这里注册」
// 是本次「硬盘使用量显示 -」的根因：字典有、展示目录有、唯独采集没注册，
// 于是页面永远拿不到值。固化成包级变量后，新增 host 指标漏注册会直接测试失败。
var resourceSampleKeys = []struct {
	key  string
	unit string
}{
	{MetricResCPU, "%"},
	{MetricResMemory, "%"},
	{MetricResDisk, "%"},
	{MetricResDiskUsed, "B"},
	{MetricResIO, "%"},
	{MetricResNetworkIn, "B/s"},
	{MetricResNetworkOut, "B/s"},
	{MetricResCPULoad1, ""},
	{MetricResCtxSwitches, "ops/s"},
	{MetricResSwapUsedPct, "%"},
	{MetricResDiskIOPS, "ops/s"},
	{MetricResDiskThroughput, "B/s"},
	{MetricResDiskAwaitMs, "ms"},
	{MetricResNetErrors, "count/s"},
	{MetricResTCPEstablished, "count"},
	{MetricResFDUsedPct, "%"},
	{MetricResTimeOffsetS, "s"},
}

// collectResourceSamples 采集主机资源指标。
// 未配置 Prometheus 时全部标记 unsupported —— 这是"CPU 永远 0 分"的根因，
// 页面必须显示"— 未接入 Prometheus"，而不是一个看起来很健康的 0。
func collectResourceSamples(ctx context.Context, client *prometheusClient, promStatus PrometheusStatus, row monitorInstanceRow) []MetricSample {
	builder := newSampleBuilder("prometheus")
	resourceKeys := resourceSampleKeys

	reason := ""
	switch {
	case row.ResourceSourceSetting == "none":
		reason = "该实例采集策略中已关闭基础资源采集"
	case !promStatus.Configured:
		reason = "未配置 Prometheus（monitor.prometheus_url），主机资源指标不可用"
	case !promStatus.Available:
		reason = "Prometheus 已配置但当前不可用"
	}
	if reason != "" {
		for _, item := range resourceKeys {
			builder.unsupported(item.key, item.unit, reason)
		}
		return builder.list()
	}

	target := strings.TrimSpace(row.PrometheusTarget)
	if target == "" {
		target = row.Hostname
	}
	metrics, err := client.CollectHostResources(ctx, target)
	if err != nil {
		for _, item := range resourceKeys {
			builder.add(item.key, 0, item.unit, QualityError, "Prometheus 查询失败："+trimError(err))
		}
		return builder.list()
	}

	assign := func(key string, unit string, metric MetricValue) {
		// 先看是不是"查询失败"：超时、连接失败这类要标 error 而不是 noData。
		// 两者的处置完全不同 —— error 要人去修（Prometheus 慢/挂了），noData 只是没这条序列。
		if reason, failed := metrics.Errs[key]; failed && reason != "" {
			builder.add(key, 0, unit, QualityError, reason)
			return
		}
		switch {
		case metric.Available && qualityIsAvailable(qualityOf(metric)):
			builder.ok(key, metric.Value, unit)
		case metric.Available:
			builder.ok(key, metric.Value, unit)
		default:
			builder.noData(key, unit, "Prometheus 未返回该主机的时间序列（请确认 exporter 已运行且 instance 标签为 "+target+"）")
		}
	}
	assign(MetricResCPU, "%", metrics.CPU)
	assign(MetricResMemory, "%", metrics.Memory)
	assign(MetricResDisk, "%", metrics.Disk)
	assign(MetricResDiskUsed, "B", metrics.DiskUsed)
	assign(MetricResIO, "%", metrics.IO)
	assign(MetricResNetworkIn, "B/s", metrics.NetworkIn)
	assign(MetricResNetworkOut, "B/s", metrics.NetworkOut)
	assign(MetricResCPULoad1, "", metrics.CPULoad1)
	assign(MetricResCtxSwitches, "ops/s", metrics.CtxSwitches)
	assign(MetricResSwapUsedPct, "%", metrics.SwapUsedPct)
	assign(MetricResDiskIOPS, "ops/s", metrics.DiskIOPS)
	assign(MetricResDiskThroughput, "B/s", metrics.DiskThroughput)
	assign(MetricResDiskAwaitMs, "ms", metrics.DiskAwaitMs)
	assign(MetricResNetErrors, "count/s", metrics.NetErrors)
	assign(MetricResTCPEstablished, "count", metrics.TCPEstablished)
	assign(MetricResFDUsedPct, "%", metrics.FDUsedPct)
	assign(MetricResTimeOffsetS, "s", metrics.TimeOffsetS)
	return builder.list()
}

// ---------------------------------------------------------------------------
// 公共小工具
// ---------------------------------------------------------------------------

func instanceTimeoutSeconds(row monitorInstanceRow) time.Duration {
	seconds := row.TimeoutSeconds
	if seconds <= 0 {
		seconds = 5
	}
	if seconds > 60 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
}

func qualityOf(metric MetricValue) string {
	if strings.TrimSpace(metric.Quality) != "" {
		return metric.Quality
	}
	if metric.Available {
		return QualityOK
	}
	return QualityNoData
}

func trimError(err error) string {
	if err == nil {
		return ""
	}
	return truncateText(strings.TrimSpace(err.Error()), 160)
}

func truncateText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}
