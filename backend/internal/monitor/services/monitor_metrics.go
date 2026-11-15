package services

import (
	"sort"
	"strings"
)

// ============================================================================
// 一、数据质量模型
//
// 这是整套监控改造里性价比最高的一条：把"没采到"从"值就是 0"里彻底分离出来。
// 历史上 3 个 PG 实例 QPS 恒为 0（未装 pg_stat_statements 时错误被静默吞掉）、
// 5 个实例 CPU/内存/磁盘恒为 0（未接 Prometheus 时不报错只留 0），
// 页面却把 0 当成真实值展示 —— 这是最危险的一类错觉。
// ============================================================================

const (
	// QualityOK 采到了，值可信
	QualityOK = "ok"
	// QualityUnsupported 该引擎不支持此指标（如 MySQL 的 risk.xid_age）
	QualityUnsupported = "unsupported"
	// QualityStale 上次采到了但本轮没采到，值是旧的
	QualityStale = "stale"
	// QualityError 采集出错（超时/认证失败/网络不可达/SQL 不支持）
	QualityError = "error"
	// QualityNoData 采集成功但该指标无数据（如 PG 无备库时复制延迟）
	QualityNoData = "no_data"
	// QualityDisabled 该指标不在实例绑定的指标模板内，本轮根本没有采集。
	//
	// 与 QualityUnsupported 的区别是本条语义的全部意义：unsupported 是"我们试图采集但
	// 这个引擎/权限拿不到"（故障或客观限制），disabled 是"配置上就没打算采"（人的选择）。
	// 两者混用同一个标记，运维就无法判断缺失是"忘了勾选"还是"真的出问题了"。
	QualityDisabled = "disabled"
)

// QualityRank 越小越"可信"；goodQuality 用来在合并多来源采样时挑选最优值。
var qualityRank = map[string]int{
	QualityOK:          0,
	QualityStale:       1,
	QualityNoData:      2,
	QualityUnsupported: 3,
	QualityError:       4,
	// disabled 排最后：它既不占"可信"也不冒充故障，只是没采
	QualityDisabled: 5,
}

// qualityUnknownRank 是未登记 quality 的权重。
//
// 必须显式大于所有已知状态：直接读 map 的零值会拿到 0，与 QualityOK 同权，
// 于是任何拼写漂移（上游新增状态、DB 里的脏值）都会被排到最前面冒充"最可信"。
var qualityUnknownRank = 99

// qualityWeight 安全地取 quality 权重，未登记一律按最不可信处理。
func qualityWeight(quality string) int {
	if rank, ok := qualityRank[quality]; ok {
		return rank
	}
	return qualityUnknownRank
}

// MetricValue.Available 为 true 的充要条件是 quality == ok。
// 页面据此渲染：ok 显示数值，其余一律显示 "—" + 原因，绝不显示 0。
func qualityIsAvailable(quality string) bool {
	return quality == QualityOK
}

// ============================================================================
// 二、指标采样与三段式命名
//
// 统一为 <域>.<对象>.<度量> 三段式，替代 locks / qps 这类裸名 ——
// 不同引擎语义会漂移，且裸名无法承载"同一逻辑指标在不同引擎的不同来源"。
// 兼容层见 legacyMetricAlias：老规则里的 15 个裸名会被映射过去，老规则不失效。
// ============================================================================

type MetricSample struct {
	Key       string
	Value     float64
	Unit      string
	Quality   string
	Reason    string
	SubDims   string
	Source    string
	IsCounter bool
}

// 指标键常量（三段式）
const (
	MetricAvailUp          = "avail.up"
	MetricAvailCollectMs   = "avail.collect_ms"
	MetricReplIORunning    = "repl.io_running"
	MetricReplSQLRunning   = "repl.sql_running"
	MetricReplLagSeconds   = "repl.lag_seconds"
	MetricCapConnUsedPct   = "cap.conn_used_pct"
	MetricCapThreadUsedPct = "cap.conn_thread_used_pct"
	MetricCapTmpDiskPct    = "cap.tmp_disk_pct"
	MetricCapBufferPoolPct = "cap.buffer_pool_used_pct"
	// Oracle 专属容量指标（engineScope=Oracle，仅在 Oracle 模板勾选清单中出现）
	MetricCapTablespaceUsedPct = "cap.tablespace_used_pct" // 最大表空间使用率（dba_data_files ∩ dba_free_space）
	MetricCapProcessUsedPct    = "cap.process_used_pct"    // 进程数使用率（v$process / processes 参数）
	MetricPerfQPS              = "perf.qps"
	MetricPerfTPS              = "perf.tps"
	MetricPerfReadRows         = "perf.read_rows"
	MetricPerfWriteRows        = "perf.write_rows"
	MetricPerfReadBytes        = "perf.read_bytes"
	MetricPerfWriteBytes       = "perf.write_bytes"
	MetricPerfCacheHitRatio    = "perf.cache_hit_ratio"
	MetricPerfLocks            = "perf.locks"
	MetricPerfConnCount        = "perf.conn_count"
	MetricPerfActiveConn       = "perf.active_conn_count"
	MetricPerfMaxConn          = "perf.max_conn_count"
	MetricPerfLongTrx          = "perf.long_trx_count"
	MetricRiskXIDAge           = "risk.xid_age"
	MetricRiskNoPKTables       = "risk.no_pk_tables"

	// 资源层（走 Prometheus，node_exporter 口径）
	MetricResCPU        = "res.cpu"
	MetricResMemory     = "res.memory"
	MetricResDisk       = "res.disk"
	MetricResDiskUsed   = "res.disk_used"
	MetricResIO         = "res.io"
	MetricResNetworkIn  = "res.network_in"
	MetricResNetworkOut = "res.network_out"

	// 资源层扩展：主机层 15 项（全部走 node_exporter，不直连数据库）
	MetricResCPULoad1       = "res.cpu_load1"
	MetricResCtxSwitches    = "res.ctx_switches"
	MetricResSwapUsedPct    = "res.swap_used_pct"
	MetricResDiskIOPS       = "res.disk_iops"
	MetricResDiskThroughput = "res.disk_throughput"
	MetricResDiskAwaitMs    = "res.disk_await_ms"
	MetricResNetErrors      = "res.net_errors"
	MetricResTCPEstablished = "res.tcp_established"
	MetricResFDUsedPct      = "res.fd_used_pct"
	MetricResTimeOffsetS    = "res.time_offset_s"

	// 引擎层扩展：跨库通用语义层，MySQL/PG 各自维护采集 SQL 映射
	MetricEngAbortedConns   = "eng.aborted_conns"
	MetricEngSlowQueries    = "eng.slow_queries"
	MetricEngDeadlocks      = "eng.deadlocks"
	MetricEngBinlogWALRate  = "eng.binlog_wal_rate"
	MetricEngReplSlotLag    = "eng.repl_slot_lag_bytes"
	MetricEngSemiSyncStatus = "eng.semisync_status"
	MetricEngLargestTable   = "eng.largest_table_bytes"

	// 语句执行次数（Com_* 系列）：SHOW GLOBAL STATUS 的累计计数器，按 Δvalue/Δt 求每秒速率。
	// 与 perf.qps / perf.tps 的关系：QPS/TPS 是聚合口径，这里是按语句类型的拆解，
	// 用于定位"哪类语句在涨"。名称沿用状态变量，避免与聚合指标混淆。
	MetricEngComDelete        = "eng.com_delete"
	MetricEngComInsert        = "eng.com_insert"
	MetricEngComInsertSelect  = "eng.com_insert_select"
	MetricEngComReplace       = "eng.com_replace"
	MetricEngComReplaceSelect = "eng.com_replace_select"
	MetricEngComSelect        = "eng.com_select"
	MetricEngComUpdate        = "eng.com_update"
	// 全表扫描查询数（Select_scan，累计值 → 速率）
	MetricEngSelectScan = "eng.select_scan"

	// 服务器层临时表与文件/表缓存计数：同样来自 SHOW GLOBAL STATUS，
	// 四项都是瞬时/累计存量，直接上报原值，不做速率换算。
	MetricEngTmpDiskTables   = "eng.tmp_disk_tables"   // Created_tmp_disk_tables
	MetricEngInnodbOpenFiles = "eng.innodb_open_files" // Innodb_num_open_files
	MetricEngOpenFiles       = "eng.open_files"        // Open_files
	MetricEngOpenTables      = "eng.open_tables"       // Open_tables

	// PostgreSQL 附件指标：与《PostgreSQL 监控指标》附件的八类项一一对应。
	// 键名沿用附件给出的原始指标名（语义名），便于与 pg 侧口径对账；
	// 全部走 pg_stat_* / pg_replication_slots / pg_database，不依赖任何扩展。
	// 适用版本均为 10 – 18（pg_wal_lsn_diff / replay_lag / backend_xid 等均在 10 起可用）。
	MetricEngLogicalRepLagBytes = "eng.logical_rep_lag_bytes" // 逻辑复制槽 WAL 堆积（confirmed_flush_lsn）
	MetricEngReplayLagSeconds   = "eng.replay_lag_seconds"    // 备库 APPLY 延迟（pg_stat_replication.replay_lag）
	MetricEngSentLagBytes       = "eng.sent_lag_bytes"        // 已发送未回放的 WAL 字节（sent_lsn - replay_lsn）
	MetricEngIdleInTrx1s        = "eng.idle_in_trx_1s"        // 空闲事务（idle in transaction）超 1 秒
	MetricEngIdleInTrx3s        = "eng.idle_in_trx_3s"        // 超 3 秒
	MetricEngIdleInTrx5s        = "eng.idle_in_trx_5s"        // 超 5 秒
	MetricEngLongQuery1s        = "eng.long_query_1s"         // 活跃查询执行超 1 秒
	MetricEngLongQuery3s        = "eng.long_query_3s"         // 超 3 秒
	MetricEngLongQuery5s        = "eng.long_query_5s"         // 超 5 秒
	MetricEngSwellTimeSeconds   = "eng.swell_time_seconds"    // 膨胀点：最老事务（含 2PC）已持续秒数
	MetricEngXactCommitRate     = "eng.xact_commit_rate"      // xact_commit 每秒增量
	MetricEngXactRollbackRate   = "eng.xact_rollback_rate"    // xact_rollback 每秒增量
	MetricEngTupInsertedRate    = "eng.tup_inserted_rate"     // tup_inserted 每秒增量
	MetricEngTupUpdatedRate     = "eng.tup_updated_rate"      // tup_updated 每秒增量
	MetricEngTupDeletedRate     = "eng.tup_deleted_rate"      // tup_deleted 每秒增量
	MetricEngTupFetchedRate     = "eng.tup_fetched_rate"      // tup_fetched 每秒增量（索引扫描回表）
	MetricEngTupReturnedRate    = "eng.tup_returned_rate"     // tup_returned 每秒增量（全表扫描）
	MetricPerfIdleInTrxCount    = "perf.idle_in_trx_count"    // 当前处于 idle in transaction 的会话数
	MetricPerfWaitingConnCount  = "perf.waiting_conn_count"   // 当前处于等待状态的连接数（wait_event IS NOT NULL）

	// ---------------- SQL Server 引擎层指标 ----------------
	// 键名沿用 SQL Server 侧的语义名，便于与官方口径对账；
	// 全部走 sys.dm_os_performance_counters / sys.dm_exec_* / sys.dm_io_virtual_file_stats /
	// sys.dm_os_wait_stats 等内置动态管理视图，不依赖任何扩展或第三方存储过程。
	// 适用版本 2012 起（dm_os_performance_counters 与 dm_io_virtual_file_stats 自 2008 起可用，
	// 这里统一按 2012+ 的口径取值，避免老版本上出现半空结果）。
	MetricEngLoginsRate         = "eng.logins_rate"                // 每秒登录次数（General Statistics / Logins/sec）
	MetricEngBatchReqRate       = "eng.batch_req_rate"             // 批处理请求速率（SQL Statistics / Batch Requests/sec）
	MetricEngSQLCompRate        = "eng.sql_compilation_rate"       // SQL 编译速率
	MetricEngSQLReCompRate      = "eng.sql_recompilation_rate"     // SQL 重编译速率
	MetricEngLockWaitRate       = "eng.lock_wait_rate"             // 锁等待速率（Locks / Lock Waits/sec）
	MetricEngLockTimeoutRate    = "eng.lock_timeout_rate"          // 锁超时速率（Locks / Lock Timeouts/sec）
	MetricEngLockAvgWaitMs      = "eng.lock_avg_wait_ms"           // 平均锁等待时长（Locks / Average Wait Time (ms)）
	MetricEngPageLifeExpectancy = "eng.page_life_expectancy"       // 页生命周期（Buffer Manager / Page life expectancy）
	MetricEngMemGrantsPending   = "eng.memory_grants_pending"      // 等待内存授予的查询数
	MetricEngSQLMemoryBytes     = "eng.sql_memory_used_bytes"      // 数据库内存占用（Memory Manager / Total Server Memory (KB)）
	MetricEngSQLCPUPct          = "eng.sql_cpu_util_pct"           // 数据库进程 CPU 占用率（dm_os_ring_buffers）
	MetricEngLogGrowths         = "eng.log_growths"                // 日志自动增长次数（Databases / Log Growths）
	MetricEngLogFlushWaitRate   = "eng.log_flush_wait_rate"        // 日志刷新等待速率（Databases / Log Flush Waits/sec）
	MetricEngLogFlushBytesRate  = "eng.log_flush_bytes_rate"       // 日志刷新吞吐（Databases / Log Bytes Flushed/sec）
	MetricEngLogUsedPct         = "eng.log_used_pct"               // 日志文件使用率（Databases / Percent Log Used）
	MetricEngDataFileBytes      = "eng.data_file_size_bytes"       // 数据文件总大小
	MetricEngLogFileBytes       = "eng.log_file_size_bytes"        // 日志文件总大小
	MetricEngTempdbUsedBytes    = "eng.tempdb_used_bytes"          // TempDB 已用空间
	MetricEngTempdbVersionBytes = "eng.tempdb_version_store_bytes" // 版本存储占用
	MetricEngIOReadBytesRate    = "eng.io_read_bytes_rate"         // 数据文件读吞吐
	MetricEngIOWriteBytesRate   = "eng.io_write_bytes_rate"        // 数据文件写吞吐
	MetricEngIOReadLatencyMs    = "eng.io_read_latency_ms"         // 数据文件读延迟
	MetricEngIOWriteLatencyMs   = "eng.io_write_latency_ms"        // 数据文件写延迟
	MetricEngWaitTimeMsRate     = "eng.wait_time_ms_rate"          // 等待总时长速率（dm_os_wait_stats）

	// 内核层：区分数据库方言（InnoDB / PostgreSQL 专有）
	MetricKernRedoWriteRate     = "kern.redo_write_rate"
	MetricKernWALSwitchRate     = "kern.wal_switch_rate"
	MetricKernCheckpointAge     = "kern.checkpoint_age_bytes"
	MetricKernBufferDirtyPct    = "kern.buffer_dirty_pct"
	MetricKernFlushRate         = "kern.flush_rate"
	MetricKernHistoryList       = "kern.history_list_length"
	MetricKernDeadTuples        = "kern.dead_tuples"
	MetricKernAutovacuumWorkers = "kern.autovacuum_workers"
	MetricKernRollbackRatio     = "kern.rollback_ratio"
	MetricKernLWLockWaits       = "kern.lwlock_waits"
	MetricKernReadAheadEvict    = "kern.read_ahead_evict_pct"
	MetricKernPageSplitRate     = "kern.page_split_rate"
	MetricKernTableCacheHit     = "kern.table_cache_hit_pct"
	MetricKernDBLWRWriteRate    = "kern.dblwr_write_rate"
	MetricKernWALFPIRate        = "kern.wal_fpi_rate"
	MetricKernCheckpointReqPct  = "kern.checkpoint_req_pct"

	// InnoDB 方言补充指标（MySQL/TiDB/Doris）：全部来自 SHOW GLOBAL STATUS，
	// 累计型（_total / 时间总量）按 gauge 原值上报，速率型经 rate 缓存求 Δ/Δt。
	MetricKernBufferDirtyPages = "kern.buffer_dirty_pages"       // Innodb_buffer_pool_pages_dirty（脏页数，gauge）
	MetricKernBufferReadReq    = "kern.buffer_read_req_rate"     // Innodb_buffer_pool_read_requests（逻辑读，速率）
	MetricKernBufferWriteReq   = "kern.buffer_write_req_rate"    // Innodb_buffer_pool_write_requests（写请求，速率）
	MetricKernDataReadRate     = "kern.innodb_data_read_rate"    // Innodb_data_read（读字节，速率）
	MetricKernDataWrittenRate  = "kern.innodb_data_written_rate" // Innodb_data_written（写字节，速率）
	MetricKernRowLockTimeMs    = "kern.row_lock_time_ms"         // Innodb_row_lock_time（累计等待时长，gauge）
	MetricKernRowLockTimeAvgMs = "kern.row_lock_time_avg_ms"     // Innodb_row_lock_time_avg（平均等待时长，gauge）
	MetricKernRowLockTimeRate  = "kern.row_lock_time_rate"       // Innodb_row_lock_time 的每秒增量
	MetricKernRowLockWaits     = "kern.row_lock_waits"           // Innodb_row_lock_waits（累计等待次数，gauge）
	MetricKernRowLockWaitsRate = "kern.row_lock_waits_rate"      // 每秒等待次数
	MetricKernDataFsyncsRate   = "kern.data_fsyncs_rate"         // Innodb_data_fsyncs（fsync 速率）
	MetricKernSortRowsRate     = "kern.sort_rows_rate"           // Sort_rows（排序行数速率）
	MetricKernRowsDeletedRate  = "kern.rows_deleted_rate"        // Innodb_rows_deleted（删除行速率）
	MetricKernRowsInsertedRate = "kern.rows_inserted_rate"       // Innodb_rows_inserted（插入行速率）
	MetricKernRowsUpdatedRate  = "kern.rows_updated_rate"        // Innodb_rows_updated（更新行速率）
	MetricKernLogWriteReqRate  = "kern.log_write_req_rate"       // Innodb_log_write_requests（日志写请求速率）
	MetricKernLogWritesRate    = "kern.log_writes_rate"          // Innodb_log_writes（物理写 redo 速率）
	MetricKernOsLogFsyncsRate  = "kern.os_log_fsyncs_rate"       // Innodb_os_log_fsyncs（日志 fsync 速率）

	// TiDB 方言（经 MySQL 协议查 INFORMATION_SCHEMA 的 TiDB 特有表，全部为实例/集群级瞬时值）
	MetricEngTIDBTrxActive     = "eng.tidb_trx_active"      // tidb_trx 活跃事务数
	MetricEngTIDBLockWaits     = "eng.tidb_lock_waits"      // data_lock_waits 锁等待数
	MetricKernTIDBNodeTotal    = "kern.tidb_node_total"     // CLUSTER_INFO 中 TiDB Server 节点数
	MetricKernTIDBNodeAbnormal = "kern.tidb_node_abnormal"  // CLUSTER_INFO 中状态非 UP 的组件节点数
	MetricKernTiKVStoreTotal   = "kern.tikv_store_total"    // TiKV store 总数
	MetricKernTiKVStoreDown    = "kern.tikv_store_down"     // 非 Up 状态的 TiKV store 数
	MetricKernTiKVRegionTotal  = "kern.tikv_region_total"   // TiKV region 总数
	MetricKernTiKVStoreUsedPct = "kern.tikv_store_used_pct" // TiKV store 最大存储使用率
	MetricKernTiKVHotRegions   = "kern.tikv_hot_regions"    // 热点 region 数
)

// legacyMetricAlias 老规则 metric_key（裸名）→ 三段式键。
// 兼容层保证历史上播种的 12 条默认规则与用户自建规则都继续生效。
var legacyMetricAlias = map[string]string{
	"cpu":                     MetricResCPU,
	"memory":                  MetricResMemory,
	"disk":                    MetricResDisk,
	"io":                      MetricResIO,
	"qps":                     MetricPerfQPS,
	"tps":                     MetricPerfTPS,
	"read_rows":               MetricPerfReadRows,
	"write_rows":              MetricPerfWriteRows,
	"read_bytes":              MetricPerfReadBytes,
	"write_bytes":             MetricPerfWriteBytes,
	"cache_hit_ratio":         MetricPerfCacheHitRatio,
	"locks":                   MetricPerfLocks,
	"connections":             MetricPerfConnCount,
	"active_connections":      MetricPerfActiveConn,
	"active_connection_ratio": MetricCapConnUsedPct,
}

func canonicalMetricKey(key string) string {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return ""
	}
	if canonical, ok := legacyMetricAlias[trimmed]; ok {
		return canonical
	}
	return trimmed
}

// ============================================================================
// 三、指标定义（平台在册指标的唯一来源）
//
// 现状是全平台只认 15 个 metric_key，硬编码在 metricValueByKey switch、metricLabel map、
// 前端下拉与 buildDefaultAlertRules 四处 —— 结果是"能配阈值，但加不了指标"。
// 改成清单驱动后：新增指标 = 数组里加一项，新增引擎 = 加一个采集器实现，前端零改动。
// ============================================================================

type MetricDescriptor struct {
	MetricKey     string
	DisplayName   string
	Category      string
	Scope         string
	EngineScope   string
	PlatformScope string
	ValueType     string
	Unit          string
	SubDims       string
	SourceType    string
	SourceRef     string
	Agg           string
	// DefaultOperator 指标语义决定的比较方向（如复制延迟用 >=、可达性用 !=）。
	// 只用于告警配置页新建档位时的默认比较符与方向锁定提示，不参与任何阈值判定。
	// 原 DefaultThreshold（默认阈值）已随指标字典下线移除：
	// 阈值唯一来源是「告警配置 / 告警模板」配置的规则，平台不再内置任何默认阈值。
	DefaultOperator string
	DefaultLevel    string
	DisplayHint     string
	Advice          string
}

const (
	CategoryAvailability = "availability"
	CategoryCapacity     = "capacity"
	CategoryPerformance  = "performance"
	CategoryRisk         = "risk"
	CategoryChange       = "change"
)

// 三层架构：主机（Prometheus 通道）→ 引擎（跨库通用语义，直连采集）→ 内核（方言专有）
const (
	LayerHost   = "host"
	LayerEngine = "engine"
	LayerKernel = "kernel"
)

// metricLayer 按指标键前缀归属层级：res.* 主机层、kern.* 内核层、其余为引擎层。
// 明细页据此把指标分为「主机 / 引擎 / 内核」三组展示。
func metricLayer(metricKey string) string {
	switch {
	case strings.HasPrefix(metricKey, "res."):
		return LayerHost
	case strings.HasPrefix(metricKey, "kern."):
		return LayerKernel
	default:
		return LayerEngine
	}
}

// layerLabel 层级展示名。engine 对应数据库通用语义层（连接/语句/复制等），
// kernel 对应存储引擎专有层（MySQL 系为 InnoDB，PG 系为 WAL/vacuum，TiDB 系为 TiKV/PD 集群组件）。
func layerLabel(layer string) string {
	switch layer {
	case LayerHost:
		return "主机层"
	case LayerEngine:
		return "数据库层"
	case LayerKernel:
		return "引擎内核指标"
	default:
		return layer
	}
}

// engineScopeMySQL 等常量用于字典的 engine_scope 白名单
const (
	engineScopeMySQLGroup = "MySQL,TiDB,Doris"
	// 金仓复用 PG 采集器（pg_stat_* 同名同义），凡是 PostgreSQL 可采的指标金仓同样适用。
	// 2026-11-11 修复：金仓接入时漏改 EngineScope，字典层把全部 PG 系指标拦在
	// loadEnabledMetricKeys 之外，模板勾了也采不到 —— 明细页只剩主机层指标。
	engineScopePostgreSQL = "PostgreSQL,Kingbase"
	engineScopeAllEngines = ""
	engineScopeSQLServer  = "SQLServer"
	engineScopeOracle     = "Oracle"
	engineScopeClickHouse = "ClickHouse"
	engineScopeTIDB       = "TiDB"
)

// builtinMetricCatalog 是平台在册指标的定义表（代码内常量，唯一来源）。
//
// 它承载的是"指标是什么"：三段式键、展示名、单位、分类、适用引擎、采集源（SQL / PromQL）、聚合方式。
// 采集引擎、落库、通知渲染、告警模板校验全部读它 —— 这是采集链路的地基，不能删。
//
// 而"运维可改写的副本"那部分（原 insight_monitor_metric_catalog 表的 enabled / default_threshold
// 等字段）已随指标字典下线，见 models.go 的说明。
var builtinMetricCatalog = []MetricDescriptor{
	// ---------------- L0 可用性 ----------------
	{MetricAvailUp, "实例可达性", CategoryAvailability, "engine", engineScopeAllEngines, "", "gauge", "", "", "engine_sql", "select 1", "last", "!=", "高", "stat",
		"检查实例网络连通性与账号权限；确认实例未被防火墙拦截"},
	{MetricAvailCollectMs, "采集耗时", CategoryAvailability, "engine", engineScopeAllEngines, "", "duration", "ms", "", "engine_sql", "local timer", "last", ">=", "低", "line",
		"采集耗时偏高通常意味着实例负载高或网络抖动；确认是否影响业务"},
	{MetricReplIORunning, "复制 IO 线程", CategoryAvailability, "engine", "MySQL,Doris", "", "gauge", "", "", "engine_sql", "SHOW REPLICA STATUS", "last", "!=", "高", "stat",
		"SHOW REPLICA STATUS 查看 Last_IO_Error；检查主库可达性与复制账号权限"},
	{MetricReplSQLRunning, "复制 SQL 线程", CategoryAvailability, "engine", engineScopeMySQLGroup, "", "gauge", "", "", "engine_sql", "SHOW REPLICA STATUS", "last", "!=", "高", "stat",
		"SHOW REPLICA STATUS 查看 Last_SQL_Error；通常为主键冲突或表结构不一致"},
	{MetricReplLagSeconds, "复制延迟", CategoryAvailability, "engine", "MySQL,PostgreSQL,Kingbase", "", "duration", "s", "", "engine_sql", "Seconds_Behind_Source / pg_last_xact_replay_timestamp", "last", ">=", "高", "line",
		"确认备库回放压力；检查大事务、DDL 与单线程回放瓶颈"},

	// ---------------- L1 容量 ----------------
	{MetricCapConnUsedPct, "活跃连接占比", CategoryCapacity, "engine", engineScopeAllEngines, "", "percent", "%", "", "engine_sql", "active/max_connections", "last", ">=", "高", "gauge",
		"活跃连接接近上限时会拒绝新连接；检查慢查询与连接泄漏"},
	{MetricCapThreadUsedPct, "连接使用率", CategoryCapacity, "engine", engineScopeAllEngines, "", "percent", "%", "", "engine_sql", "Threads_connected/max_connections", "last", ">=", "中", "gauge",
		"连接使用率持续偏高需评估调大 max_connections 或引入连接池"},
	{MetricCapTmpDiskPct, "临时表落盘率", CategoryCapacity, "engine", engineScopeMySQLGroup, "", "percent", "%", "", "engine_sql", "Created_tmp_disk_tables/Created_tmp_tables", "last", ">=", "低", "line",
		"临时表大量落盘通常是排序/分组缺少合适索引，建议开启慢查询定位"},
	{MetricCapBufferPoolPct, "Buffer Pool 使用率", CategoryCapacity, "engine", "MySQL,Doris", "", "percent", "%", "", "engine_sql", "Innodb_buffer_pool_pages_*", "last", ">=", "低", "line",
		"Buffer Pool 逼近上限会导致物理读增加，评估是否调大 innodb_buffer_pool_size"},
	{MetricCapTablespaceUsedPct, "最大表空间使用率", CategoryCapacity, "engine", engineScopeOracle, "", "percent", "%", "", "engine_sql", "dba_data_files + dba_free_space（按表空间求和后取最大值，不含临时表空间）", "max", ">=", "高", "gauge",
		"表空间写满会直接导致业务写入失败（ORA-01653/01654）；优先清理/归档历史数据或自动扩展数据文件。该指标不含临时表空间，临时空间溢出看排序类报错"},
	{MetricCapProcessUsedPct, "进程数使用率", CategoryCapacity, "engine", engineScopeOracle, "", "percent", "%", "", "engine_sql", "v$process 数量 / processes 参数", "last", ">=", "高", "gauge",
		"processes 参数打满后新建会话报 ORA-00020；结合连接数与共享服务器配置评估调大参数或引入连接池"},
	{MetricResCPU, "CPU 使用率", CategoryCapacity, "host", engineScopeAllEngines, "linux,windows,docker", "percent", "%", "", "prometheus",
		`clamp_min(100 - avg(irate(node_cpu_seconds_total{mode="idle"}[5m])) * 100, 0)`, "avg", ">=", "高", "gauge",
		"确认是数据库自身消耗还是同机其他进程；结合 QPS/慢查询判断"},
	{MetricResMemory, "内存使用率", CategoryCapacity, "host", engineScopeAllEngines, "linux,windows,docker", "percent", "%", "", "prometheus",
		`clamp_min((1 - avg(node_memory_MemAvailable_bytes) / avg(node_memory_MemTotal_bytes)) * 100, 0)`, "avg", ">=", "高", "gauge",
		"警惕 OOM Killer 杀掉数据库进程；关注 Buffer Pool 与连接内存占用"},
	{MetricResDisk, "磁盘使用率", CategoryCapacity, "host", engineScopeAllEngines, "linux,windows,docker", "percent", "%", "", "prometheus",
		`max(100 * (1 - node_filesystem_avail_bytes{fstype!~"tmpfs|overlay|squashfs|ramfs|nsfs",mountpoint!~"/boot.*|/run.*|/sys.*|/dev.*"} / node_filesystem_size_bytes{fstype!~"tmpfs|overlay|squashfs|ramfs|nsfs",mountpoint!~"/boot.*|/run.*|/sys.*|/dev.*"}))`, "max", ">=", "高", "gauge",
		"磁盘写满会导致数据库只读甚至崩溃；优先清理 binlog/慢日志或扩容。口径已排除 /boot 等启动分区，避免小分区盖掉数据分区的真实使用率"},
	// 磁盘使用量（绝对容量）：与磁盘使用率同源于 node_filesystem_*，
	// 口径为「所有真实分区的已用字节之和」，已用 = size - avail（与使用率一致，排除 root 保留块）。
	// max by (device) 用于给同一块盘被重复挂载（bind mount）去重，避免同一分区被累加两次。
	// 文件系统集合与使用率保持一致（同样排除 /boot 等启动分区），否则百分比与字节数互相矛盾。
	{MetricResDiskUsed, "磁盘使用量", CategoryCapacity, "host", engineScopeAllEngines, "linux,windows,docker", "gauge", "B", "", "prometheus",
		`sum(max by (device) (node_filesystem_size_bytes{fstype!~"tmpfs|overlay|squashfs|ramfs|nsfs",mountpoint!~"/boot.*|/run.*|/sys.*|/dev.*"})) - sum(max by (device) (node_filesystem_avail_bytes{fstype!~"tmpfs|overlay|squashfs|ramfs|nsfs",mountpoint!~"/boot.*|/run.*|/sys.*|/dev.*"}))`,
		"last", "", "低", "gauge",
		"绝对使用量用于容量规划（还剩多少 GB、多久写满），告警仍以磁盘使用率为准"},
	{MetricResIO, "IO 使用率", CategoryCapacity, "host", engineScopeAllEngines, "linux,windows,docker", "percent", "%", "", "prometheus",
		`sum(irate(node_disk_io_time_seconds_total[5m])) * 100`, "avg", ">=", "中", "line",
		"IO 饱和会直接放大查询延迟；检查是否存在大量物理读或刷脏"},
	{MetricResNetworkIn, "网络流入流量", CategoryCapacity, "host", engineScopeAllEngines, "linux,windows,docker", "gauge", "B/s", "", "prometheus",
		`sum(irate(node_network_receive_bytes_total[5m]))`, "max", "", "低", "line",
		"网络流入突增通常是大批量导入或主库 binlog 拉取"},
	{MetricResNetworkOut, "网络流出流量", CategoryCapacity, "host", engineScopeAllEngines, "linux,windows,docker", "gauge", "B/s", "", "prometheus",
		`sum(irate(node_network_transmit_bytes_total[5m]))`, "max", "", "低", "line",
		"网络流出突增通常是全表扫描返回大量数据或备份传输"},

	// ---------------- L2 性能 ----------------
	{MetricPerfQPS, "QPS", CategoryPerformance, "engine", engineScopeAllEngines, "", "counter_rate", "req/s", "", "engine_sql", "Questions / pg_stat_statements.calls / SQL Statistics.Batch Requests/sec", "avg", "", "中", "line",
		"QPS 突降通常意味着上游流量异常或实例不可用，而非数据库本身"},
	{MetricPerfTPS, "TPS", CategoryPerformance, "engine", engineScopeAllEngines, "", "counter_rate", "txn/s", "", "engine_sql", "Com_commit+Com_rollback / xact_commit+xact_rollback", "avg", "", "中", "line",
		"TPS 与 QPS 比例失衡时关注事务粒度是否过大"},
	{MetricPerfReadRows, "每秒读行数", CategoryPerformance, "engine", engineScopeAllEngines, "", "counter_rate", "rows/s", "", "engine_sql", "Innodb_rows_read / tup_returned+tup_fetched / dm_exec_query_stats.total_rows", "avg", "", "低", "line", ""},
	{MetricPerfWriteRows, "每秒写行数", CategoryPerformance, "engine", engineScopeAllEngines, "", "counter_rate", "rows/s", "", "engine_sql", "Innodb_rows_inserted/updated/deleted / dm_db_index_usage_stats.user_updates", "avg", "", "低", "line", ""},
	{MetricPerfReadBytes, "协议流出流量", CategoryPerformance, "engine", engineScopeAllEngines, "", "counter_rate", "B/s", "", "engine_sql", "Bytes_sent / blks_read*block_size / dm_exec_connections.num_writes×包大小", "avg", "", "低", "line",
		"服务端协议层每秒发给客户端的字节数（Bytes_sent）；SQL Server 无实例级字节计数器，按 TDS 包数×包大小近似（上界）；与主机网卡流量（node_network_*）口径不同"},
	{MetricPerfWriteBytes, "协议流入流量", CategoryPerformance, "engine", engineScopeAllEngines, "", "counter_rate", "B/s", "", "engine_sql", "Bytes_received / dm_exec_connections.num_reads×包大小", "avg", "", "低", "line",
		"服务端协议层每秒收到客户端的字节数（Bytes_received）；SQL Server 无实例级字节计数器，按 TDS 包数×包大小近似（上界）；与主机网卡流量（node_network_*）口径不同"},
	{MetricPerfCacheHitRatio, "缓存命中率", CategoryPerformance, "engine", "MySQL,Doris,PostgreSQL,Kingbase,Oracle,SQLServer", "", "percent", "%", "", "engine_sql", "Innodb_buffer_pool_read_requests / blks_hit / buffer cache 命中率 / SQL Server Buffer Manager 命中率", "avg", "<=", "中", "line",
		"命中率低说明读放大严重；评估 Buffer Pool 大小与索引覆盖（Oracle 为 buffer cache hit ratio，SQL Server 为 Buffer cache hit ratio）"},
	{MetricPerfLocks, "锁等待数", CategoryPerformance, "engine", "MySQL,Doris,PostgreSQL,Kingbase,Oracle,SQLServer,ClickHouse", "", "count", "count", "", "engine_sql", "Innodb_row_lock_current_waits / pg_locks not granted", "max", ">=", "中", "line",
		"定位阻塞源：MySQL 查 performance_schema.data_lock_waits，PG 用 pg_blocking_pids()"},
	{MetricPerfConnCount, "连接数", CategoryPerformance, "engine", engineScopeAllEngines, "", "count", "count", "", "engine_sql", "Threads_connected / pg_stat_activity", "last", "", "低", "line", ""},
	{MetricPerfActiveConn, "活跃连接数", CategoryPerformance, "engine", engineScopeAllEngines, "", "count", "count", "", "engine_sql", "Threads_running / pg_stat_activity state=active", "last", "", "中", "line", ""},
	{MetricPerfMaxConn, "最大连接数", CategoryPerformance, "engine", engineScopeAllEngines, "", "count", "count", "", "engine_sql", "max_connections", "last", "", "低", "stat", ""},
	{MetricPerfLongTrx, "长事务数", CategoryPerformance, "engine", "MySQL,TiDB,Doris,PostgreSQL,Kingbase,Oracle,SQLServer", "", "count", "count", "", "engine_sql", "information_schema.innodb_trx / pg_stat_activity.xact_start / v$session 长活跃事务 / dm_tran_active_transactions", "max", ">=", "中", "line",
		"长事务会阻碍 purge 与 autovacuum，导致 undo/膨胀与锁堆积（Oracle 口径：事务会话挂起超 60 秒；SQL Server 口径：未提交事务开启超 60 秒）"},

	// ---------------- L3 风险 ----------------
	{MetricRiskXIDAge, "事务ID回卷风险", CategoryRisk, "engine", engineScopePostgreSQL, "", "count", "count", "", "engine_sql", "age(datfrozenxid)", "max", ">=", "高", "gauge",
		"事务 ID 回卷会强制停机；立即检查 autovacuum 是否被长事务或复制槽阻塞"},
	{MetricRiskNoPKTables, "无主键表数", CategoryRisk, "engine", "MySQL,TiDB,Doris,PostgreSQL,Kingbase", "", "count", "count", "", "engine_sql", "information_schema.tables without primary key", "sum", ">=", "低", "stat",
		"无主键表影响复制性能与在线 DDL；建议补主键或唯一键"},

	// ---------------- 主机层扩展（node_exporter） ----------------
	{MetricResCPULoad1, "CPU 负载（1分钟）", CategoryCapacity, "host", engineScopeAllEngines, "linux,windows,docker", "gauge", "", "", "prometheus",
		`avg(node_load1)`, "avg", "", "低", "line",
		"负载持续高于核数说明进程排队；结合 CPU 使用率与 IO 延迟区分是算力还是 IO 瓶颈"},
	{MetricResCtxSwitches, "上下文切换", CategoryPerformance, "host", engineScopeAllEngines, "linux,windows,docker", "counter_rate", "ops/s", "", "prometheus",
		`sum(irate(node_context_switches_total[5m]))`, "avg", "", "低", "line",
		"切换突增常见于线程争抢或中断风暴；对比基线而非绝对值"},
	{MetricResSwapUsedPct, "Swap 使用率", CategoryCapacity, "host", engineScopeAllEngines, "linux,windows,docker", "percent", "%", "", "prometheus",
		`clamp_min((1 - avg(node_memory_SwapFree_bytes) / clamp_min(avg(node_memory_SwapTotal_bytes), 1)) * 100, 0)`, "avg", ">=", "中", "gauge",
		"数据库进程被换出会造成毫秒级抖动放大为秒级；建议 swappiness 调低"},
	{MetricResDiskIOPS, "磁盘 IOPS", CategoryPerformance, "host", engineScopeAllEngines, "linux,windows,docker", "counter_rate", "ops/s", "", "prometheus",
		`sum(irate(node_disk_reads_completed_total[5m])) + sum(irate(node_disk_writes_completed_total[5m]))`, "avg", "", "低", "line",
		"接近磁盘规格上限时查询延迟会整体抬升；关注是否由刷脏或物理读放大引起"},
	{MetricResDiskThroughput, "磁盘吞吐", CategoryPerformance, "host", engineScopeAllEngines, "linux,windows,docker", "counter_rate", "B/s", "", "prometheus",
		`sum(irate(node_disk_read_bytes_total[5m])) + sum(irate(node_disk_written_bytes_total[5m]))`, "avg", "", "低", "line",
		"顺序写大户是 redo/WAL 与 binlog；读突增通常是物理读放大或备份"},
	{MetricResDiskAwaitMs, "磁盘延迟", CategoryPerformance, "host", engineScopeAllEngines, "linux,windows,docker", "gauge", "ms", "", "prometheus",
		`1000 * (sum(irate(node_disk_read_time_seconds_total[5m])) + sum(irate(node_disk_write_time_seconds_total[5m]))) / clamp_min(sum(irate(node_disk_reads_completed_total[5m])) + sum(irate(node_disk_writes_completed_total[5m])), 1)`, "avg", ">=", "中", "line",
		"机械盘 >10ms、SSD >2ms 即需关注；延迟飙升常先于 IOPS 打满出现"},
	{MetricResNetErrors, "网络错误/丢包", CategoryPerformance, "host", engineScopeAllEngines, "linux,windows,docker", "counter_rate", "count/s", "", "prometheus",
		`sum(irate(node_network_receive_errs_total[5m])) + sum(irate(node_network_transmit_errs_total[5m])) + sum(irate(node_network_receive_drop_total[5m])) + sum(irate(node_network_transmit_drop_total[5m]))`, "max", ">=", "中", "line",
		"丢包会放大跨机复制与连接超时；检查网卡、网线与交换机"},
	{MetricResTCPEstablished, "TCP 连接数", CategoryCapacity, "host", engineScopeAllEngines, "linux,windows,docker", "gauge", "count", "", "prometheus",
		`avg(node_netstat_Tcp_CurrEstab)`, "avg", "", "低", "line",
		"连接数逼近 ulimit 会导致新连接失败；对比引擎层连接数看连接泄漏"},
	{MetricResFDUsedPct, "文件句柄使用率", CategoryCapacity, "host", engineScopeAllEngines, "linux,windows,docker", "percent", "%", "", "prometheus",
		`clamp_min(100 * avg(node_filefd_allocated) / clamp_min(avg(node_filefd_maximum), 1), 0)`, "avg", ">=", "高", "gauge",
		"句柄耗尽会导致数据库无法打开表文件甚至崩溃；调大 ulimit 与 open_files_limit"},
	{MetricResTimeOffsetS, "时钟偏移", CategoryRisk, "host", engineScopeAllEngines, "linux,windows,docker", "gauge", "s", "", "prometheus",
		`max(abs(node_timex_offset_seconds))`, "max", ">=", "中", "line",
		"时钟漂移影响备份时间戳、复制心跳与证书校验；确认 NTP 服务状态"},

	// ---------------- 引擎层扩展（跨库通用语义） ----------------
	{MetricEngAbortedConns, "连接错误速率", CategoryPerformance, "engine", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Aborted_connects+Aborted_clients", "avg", "", "中", "line",
		"连接被中止常见于认证失败、网络抖动或 wait_timeout 过短"},
	{MetricEngSlowQueries, "慢查询速率", CategoryPerformance, "engine", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Slow_queries", "avg", "", "中", "line",
		"慢查询是性能退化的先行指标；结合慢日志定位具体 SQL"},
	{MetricEngDeadlocks, "死锁速率", CategoryRisk, "engine", "MySQL,TiDB,Doris,PostgreSQL,Kingbase,SQLServer", "", "counter_rate", "count/s", "", "engine_sql", "innodb_metrics lock_deadlocks / pg_stat_database.deadlocks / Locks Number of Deadlocks/sec", "max", "", "高", "line",
		"死锁本身会被自动回滚，频发说明事务并发模型或索引设计有问题"},
	{MetricEngBinlogWALRate, "日志产生速率", CategoryCapacity, "engine", "MySQL,TiDB,Doris,PostgreSQL,Kingbase", "", "counter_rate", "B/s", "", "engine_sql", "Binlog_bytes_written / WAL LSN 差值", "avg", "", "低", "line",
		"日志速率决定磁盘增长与复制带宽需求；突增通常对应批量写入或 DDL"},
	{MetricEngReplSlotLag, "复制槽堆积", CategoryRisk, "engine", engineScopePostgreSQL, "", "gauge", "B", "", "engine_sql", "pg_replication_slots", "max", "", "高", "gauge",
		"失效复制槽会导致 WAL 无限堆积直至磁盘写满；确认对应订阅/备库状态"},
	{MetricEngSemiSyncStatus, "半同步复制状态", CategoryAvailability, "engine", engineScopeMySQLGroup, "", "gauge", "", "", "engine_sql", "Rpl_semi_sync_source_status", "last", "!=", "高", "stat",
		"半同步退化为异步意味着数据可靠性降级；检查备库 ACK 是否超时"},
	{MetricEngLargestTable, "最大表尺寸", CategoryCapacity, "engine", "MySQL,TiDB,Doris,PostgreSQL,Kingbase", "", "gauge", "B", "", "engine_sql", "information_schema.tables / pg_total_relation_size", "max", "", "低", "stat",
		"单表过大会放大备份、DDL 与碎片治理成本；提前规划分区或归档"},

	// 语句执行次数（Com_*）：与聚合口径的 QPS/TPS 互补，用于按语句类型定位增量来源
	{MetricEngComSelect, "SELECT 执行次数", CategoryPerformance, "engine", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Com_select", "avg", "", "低", "line",
		"每秒 SELECT 语句执行次数（Com_select 增量 ÷ 采集间隔）。突增先看是否有新上线查询或缓存失效，结合全表扫描次数判断是否索引失效"},
	{MetricEngComInsert, "INSERT 执行次数", CategoryPerformance, "engine", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Com_insert", "avg", "", "低", "line",
		"每秒 INSERT 语句执行次数（Com_insert 增量 ÷ 采集间隔）。批量导入期抬升属正常；长期高位关注磁盘写与 redo 压力"},
	{MetricEngComInsertSelect, "INSERT SELECT 执行次数", CategoryPerformance, "engine", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Com_insert_select", "avg", "", "低", "line",
		"每秒 INSERT...SELECT 执行次数（Com_insert_select 增量 ÷ 采集间隔）。该语句会持有源表锁并放大复制延迟，建议低峰执行"},
	{MetricEngComUpdate, "UPDATE 执行次数", CategoryPerformance, "engine", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Com_update", "avg", "", "低", "line",
		"每秒 UPDATE 语句执行次数（Com_update 增量 ÷ 采集间隔）。与行锁等待联动看；突增时确认是否有批量任务"},
	{MetricEngComDelete, "DELETE 执行次数", CategoryPerformance, "engine", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Com_delete", "avg", "", "低", "line",
		"每秒 DELETE 语句执行次数（Com_delete 增量 ÷ 采集间隔）。大量删除会产生 undo 与碎片；大表删除建议分批并关注历史链长度"},
	{MetricEngComReplace, "REPLACE 执行次数", CategoryPerformance, "engine", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Com_replace", "avg", "", "低", "line",
		"每秒 REPLACE 执行次数（Com_replace 增量 ÷ 采集间隔）。REPLACE 实为删除+插入，会放大 undo 与自增 ID 消耗，建议改为 INSERT ... ON DUPLICATE KEY UPDATE"},
	{MetricEngComReplaceSelect, "REPLACE SELECT 执行次数", CategoryPerformance, "engine", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Com_replace_select", "avg", "", "低", "line",
		"每秒 REPLACE...SELECT 执行次数（Com_replace_select 增量 ÷ 采集间隔）。代价高于等价的 upsert，且会阻塞复制，建议改写"},
	{MetricEngSelectScan, "全表扫描次数", CategoryPerformance, "engine", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Select_scan", "avg", "", "中", "line",
		"每秒触发全表扫描的查询次数（Select_scan 增量 ÷ 采集间隔）。持续增长说明索引缺失或失效；结合慢查询定位具体 SQL"},
	{MetricEngTmpDiskTables, "磁盘临时表数", CategoryPerformance, "engine", engineScopeMySQLGroup, "", "gauge", "count", "", "engine_sql", "Created_tmp_disk_tables", "max", "", "中", "line",
		"该值持续增长说明大量排序/分组在磁盘上完成；核对 tmp_table_size 与 max_heap_table_size"},
	{MetricEngInnodbOpenFiles, "InnoDB 打开文件数", CategoryCapacity, "engine", engineScopeMySQLGroup, "", "gauge", "count", "", "engine_sql", "Innodb_num_open_files", "last", "", "低", "line",
		"反映 InnoDB 文件描述符占用；接近 innodb_open_files 限制时需调大该参数"},
	{MetricEngOpenFiles, "服务器打开文件数", CategoryCapacity, "engine", engineScopeMySQLGroup, "", "gauge", "count", "", "engine_sql", "Open_files（不含 InnoDB 文件、管道与套接字）", "last", "", "低", "line",
		"持续增长可能句柄泄漏；与 open_files_limit 对比评估是否需要调大"},
	{MetricEngOpenTables, "表缓存打开表数", CategoryCapacity, "engine", engineScopeMySQLGroup, "", "gauge", "count", "", "engine_sql", "Open_tables", "last", "", "低", "line",
		"接近 table_open_cache 或 Opened_tables 增长过快，说明表缓存偏小，需调大 table_open_cache"},

	// ---------------- PostgreSQL 附件指标（10 – 18） ----------------
	// 延迟
	{MetricEngLogicalRepLagBytes, "逻辑复制延迟", CategoryRisk, "engine", engineScopePostgreSQL, "", "gauge", "B", "", "engine_sql", "pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn)", "max", ">=", "高", "gauge",
		"逻辑复制槽的 WAL 堆积量（confirmed_flush_lsn 落后量）；订阅端掉线会让 WAL 无限堆积直至磁盘写满"},
	{MetricEngReplayLagSeconds, "备库 APPLY 延迟", CategoryAvailability, "engine", engineScopePostgreSQL, "", "duration", "s", "", "engine_sql", "pg_stat_replication.replay_lag", "last", ">=", "高", "line",
		"主库视角下备库回放 WAL 的滞后时间（PG10+ 的 replay_lag）；持续升高说明备库 IO/CPU 跟不上或复制中断"},
	{MetricEngSentLagBytes, "备库发送延迟", CategoryAvailability, "engine", engineScopePostgreSQL, "", "gauge", "B", "", "engine_sql", "pg_wal_lsn_diff(sent_lsn, replay_lsn)", "max", ">=", "中", "line",
		"已发送给备库但尚未回放的 WAL 字节数；与 APPLY 延迟配合区分「网络传不动」还是「备库回放慢」"},
	// 长事务
	{MetricEngIdleInTrx1s, "空闲事务数(>1s)", CategoryRisk, "engine", engineScopePostgreSQL, "", "count", "count", "", "engine_sql", "pg_stat_activity idle in transaction", "max", ">=", "中", "line",
		"持有事务但空闲超过 1 秒的会话；空闲事务会阻塞 vacuum 回收死元组，是表膨胀的首要成因"},
	{MetricEngIdleInTrx3s, "空闲事务数(>3s)", CategoryRisk, "engine", engineScopePostgreSQL, "", "count", "count", "", "engine_sql", "pg_stat_activity idle in transaction", "max", ">=", "中", "line",
		"持有事务但空闲超过 3 秒的会话；确认应用是否在事务里等待外部调用或交互"},
	{MetricEngIdleInTrx5s, "空闲事务数(>5s)", CategoryRisk, "engine", engineScopePostgreSQL, "", "count", "count", "", "engine_sql", "pg_stat_activity idle in transaction", "max", ">=", "高", "line",
		"持有事务但空闲超过 5 秒的会话；长空闲事务同时会拖大膨胀点并阻塞 DDL 拿锁"},
	// 慢查询
	{MetricEngLongQuery1s, "慢查询数(>1s)", CategoryPerformance, "engine", engineScopePostgreSQL, "", "count", "count", "", "engine_sql", "pg_stat_activity state=active", "max", ">=", "中", "line",
		"当前执行时间超过 1 秒的活跃查询数（瞬时值，非累计速率）；用于判断当下是否有语句堆积"},
	{MetricEngLongQuery3s, "慢查询数(>3s)", CategoryPerformance, "engine", engineScopePostgreSQL, "", "count", "count", "", "engine_sql", "pg_stat_activity state=active", "max", ">=", "高", "line",
		"当前执行时间超过 3 秒的活跃查询数；结合 wait_event 区分是在跑还是在等锁"},
	{MetricEngLongQuery5s, "慢查询数(>5s)", CategoryPerformance, "engine", engineScopePostgreSQL, "", "count", "count", "", "engine_sql", "pg_stat_activity state=active", "max", ">=", "高", "line",
		"当前执行时间超过 5 秒的活跃查询数；持续存在时应抓 pg_stat_activity 定位具体 SQL"},
	// 膨胀点
	{MetricEngSwellTimeSeconds, "膨胀点", CategoryRisk, "engine", engineScopePostgreSQL, "", "duration", "s", "", "engine_sql", "最老 backend_xid/backend_xmin 事务 + pg_prepared_xacts", "max", ">=", "高", "line",
		"最老活跃事务（含 2PC 未决事务）已持续的秒数，决定「多久以前的垃圾可以被回收」；越长死元组越收不掉"},
	// 每秒事务数
	{MetricEngXactCommitRate, "每秒事务提交数", CategoryPerformance, "engine", engineScopePostgreSQL, "", "counter_rate", "count/s", "", "engine_sql", "pg_stat_database.xact_commit", "avg", "", "低", "line",
		"xact_commit 的每秒增量；与回滚速率一起刻画业务写入强度"},
	{MetricEngXactRollbackRate, "每秒事务回滚数", CategoryPerformance, "engine", engineScopePostgreSQL, "", "counter_rate", "count/s", "", "engine_sql", "pg_stat_database.xact_rollback", "avg", ">=", "中", "line",
		"xact_rollback 的每秒增量；异常升高通常意味着约束冲突、序列化失败或应用重试风暴"},
	// 操作行数
	{MetricEngTupInsertedRate, "每秒插入记录数", CategoryPerformance, "engine", engineScopePostgreSQL, "", "counter_rate", "rows/s", "", "engine_sql", "pg_stat_database.tup_inserted", "avg", "", "低", "line",
		"tup_inserted 每秒增量；突增对应批量导入，需关注 WAL 与磁盘写压力"},
	{MetricEngTupUpdatedRate, "每秒更新记录数", CategoryPerformance, "engine", engineScopePostgreSQL, "", "counter_rate", "rows/s", "", "engine_sql", "pg_stat_database.tup_updated", "avg", "", "低", "line",
		"tup_updated 每秒增量；PG 的更新是插入新版本，会直接产生死元组与膨胀"},
	{MetricEngTupDeletedRate, "每秒删除记录数", CategoryPerformance, "engine", engineScopePostgreSQL, "", "counter_rate", "rows/s", "", "engine_sql", "pg_stat_database.tup_deleted", "avg", "", "低", "line",
		"tup_deleted 每秒增量；大批量删除后需确认 autovacuum 跟得上"},
	{MetricEngTupFetchedRate, "每秒索引扫描回表记录数", CategoryPerformance, "engine", engineScopePostgreSQL, "", "counter_rate", "rows/s", "", "engine_sql", "pg_stat_database.tup_fetched", "avg", "", "低", "line",
		"tup_fetched 每秒增量，即通过索引取回的行数；与 tup_returned 对比可估算索引命中质量"},
	{MetricEngTupReturnedRate, "每秒全表扫描记录数", CategoryPerformance, "engine", engineScopePostgreSQL, "", "counter_rate", "rows/s", "", "engine_sql", "pg_stat_database.tup_returned", "avg", "", "中", "line",
		"tup_returned 每秒增量，即顺序扫描返回的行数；占读取总量比例过高说明索引缺失或失效"},
	// 连接数
	{MetricPerfIdleInTrxCount, "空闲事务会话数", CategoryPerformance, "engine", engineScopePostgreSQL, "", "count", "count", "", "engine_sql", "pg_stat_activity state='idle in transaction'", "last", ">=", "中", "line",
		"当前处于「事务中空闲」状态的会话数；这类会话持有快照却不干活，是膨胀与锁等待的温床"},
	{MetricPerfWaitingConnCount, "等待中连接数", CategoryPerformance, "engine", "PostgreSQL,Kingbase,SQLServer", "", "count", "count", "", "engine_sql", "pg_stat_activity wait_event IS NOT NULL / dm_exec_requests wait_type IS NOT NULL（仅客户端连接）", "last", ">=", "中", "line",
		"当前处于等待状态的会话数（PG 用 wait_event、SQL Server 用 wait_type，均已排除后台进程，否则空闲实例也会被后台进程的常驻等待刷成非零）；持续不为 0 说明存在锁或 IO 瓶颈"},

	// ---------------- SQL Server 引擎层指标（2012+） ----------------
	// 会话与连接
	{MetricEngLoginsRate, "登录速率", CategoryPerformance, "engine", engineScopeSQLServer, "", "counter_rate", "count/s", "", "engine_sql", "sys.dm_os_performance_counters：General Statistics / Logins/sec", "avg", "", "低", "line",
		"登录速率突增通常意味着连接池失效或应用反复重连；与连接数联动判断是否连接泄漏"},
	// 语句与事务吞吐
	{MetricEngBatchReqRate, "批处理请求速率", CategoryPerformance, "engine", engineScopeSQLServer, "", "counter_rate", "req/s", "", "engine_sql", "sys.dm_os_performance_counters：SQL Statistics / Batch Requests/sec（实例级累计值，按 Δ值/Δt 求速率）", "avg", "", "中", "line",
		"SQL Server 的吞吐主口径（等价于 MySQL 的 QPS）；突降通常意味着上游流量异常或实例不可用，而非数据库本身"},
	{MetricEngSQLCompRate, "SQL 编译速率", CategoryPerformance, "engine", engineScopeSQLServer, "", "counter_rate", "count/s", "", "engine_sql", "sys.dm_os_performance_counters：SQL Statistics / SQL Compilations/sec", "avg", "", "低", "line",
		"编译消耗 CPU 且需持有编译锁；持续高位说明语句未参数化，优先改造为参数化查询或使用计划缓存复用"},
	{MetricEngSQLReCompRate, "SQL 重编译速率", CategoryPerformance, "engine", engineScopeSQLServer, "", "counter_rate", "count/s", "", "engine_sql", "sys.dm_os_performance_counters：SQL Statistics / SQL Re-Compilations/sec", "avg", ">=", "中", "line",
		"重编译常见于统计信息变更、临时表与会话 SET 选项不一致；占比过高会显著抬升 CPU 并挤占计划缓存"},
	// 锁与阻塞
	{MetricEngLockWaitRate, "锁等待速率", CategoryPerformance, "engine", engineScopeSQLServer, "", "counter_rate", "count/s", "", "engine_sql", "sys.dm_os_performance_counters：Locks / Lock Waits/sec（_Total）", "max", ">=", "中", "line",
		"锁等待速率抬升先于阻塞堆积出现；结合锁等待数与 sys.dm_exec_requests 定位具体语句和热点对象"},
	{MetricEngLockTimeoutRate, "锁超时速率", CategoryRisk, "engine", engineScopeSQLServer, "", "counter_rate", "count/s", "", "engine_sql", "sys.dm_os_performance_counters：Locks / Lock Timeouts/sec（_Total）", "max", ">=", "高", "line",
		"锁超时会让业务语句直接失败；检查长事务与缺失索引导致的锁范围放大"},
	{MetricEngLockAvgWaitMs, "平均锁等待时长", CategoryPerformance, "engine", engineScopeSQLServer, "", "gauge", "ms", "", "engine_sql", "sys.dm_os_performance_counters：Locks / Average Wait Time (ms)（_Total）", "max", ">=", "中", "line",
		"平均等待超过 100ms 即会明显影响事务响应；定位阻塞源头并缩短事务持锁时间"},
	// 缓冲池与内存
	{MetricEngPageLifeExpectancy, "Page Life Expectancy", CategoryPerformance, "engine", engineScopeSQLServer, "", "gauge", "s", "", "engine_sql", "sys.dm_os_performance_counters：Buffer Manager / Page life expectancy", "last", "<=", "高", "gauge",
		"数据页在缓冲池中的平均驻留秒数，是内存压力最直接的指标；持续低于 300 秒说明缓冲池被反复换出，需扩容内存或治理大范围扫描"},
	{MetricEngMemGrantsPending, "内存授予等待数", CategoryRisk, "engine", engineScopeSQLServer, "", "gauge", "count", "", "engine_sql", "sys.dm_os_performance_counters：Memory Manager / Memory Grants Pending", "max", ">=", "高", "line",
		"等待内存授予的查询会先排队再执行；常见成因是大排序/哈希缺少合适索引或统计信息过期"},
	{MetricEngSQLMemoryBytes, "数据库内存占用", CategoryCapacity, "engine", engineScopeSQLServer, "", "gauge", "B", "", "engine_sql", "sys.dm_os_performance_counters：Memory Manager / Total Server Memory (KB)", "last", "", "低", "gauge",
		"SQL Server 实际占用的内存（含缓冲池与计划缓存）；与主机内存使用率联动判断是否挤压系统与其他进程"},
	{MetricEngSQLCPUPct, "数据库 CPU 占用率", CategoryPerformance, "engine", engineScopeSQLServer, "", "percent", "%", "", "engine_sql", "sys.dm_os_ring_buffers：RING_BUFFER_SCHEDULER_MONITOR 的 ProcessUtilization", "last", ">=", "高", "gauge",
		"SQL Server 进程自身的 CPU 使用率；与主机 CPU 使用率对比可区分是数据库还是同机其他进程在消耗算力"},
	// 事务日志
	{MetricEngLogGrowths, "日志增长次数", CategoryRisk, "engine", engineScopeSQLServer, "", "counter_rate", "count/s", "", "engine_sql", "sys.dm_os_performance_counters：Databases / Log Growths（各库求和）", "max", ">=", "中", "line",
		"日志自动增长会阻塞写入并产生 VLF 碎片；改为预分配足够大的日志文件并按固定增量增长"},
	{MetricEngLogFlushWaitRate, "日志刷新等待速率", CategoryPerformance, "engine", engineScopeSQLServer, "", "counter_rate", "count/s", "", "engine_sql", "sys.dm_os_performance_counters：Databases / Log Flush Waits/sec（各库求和）", "max", ">=", "中", "line",
		"日志刷新等待抬升说明日志磁盘写不动；检查日志文件所在存储的写延迟与吞吐"},
	{MetricEngLogFlushBytesRate, "日志刷新吞吐", CategoryPerformance, "engine", engineScopeSQLServer, "", "counter_rate", "B/s", "", "engine_sql", "sys.dm_os_performance_counters：Databases / Log Bytes Flushed/sec（各库求和）", "avg", "", "低", "line",
		"日志写入吞吐决定事务提交成本与镜像/AlwaysOn 的传输压力；突增通常对应批量写入或大事务"},
	{MetricEngLogUsedPct, "日志文件使用率", CategoryCapacity, "engine", engineScopeSQLServer, "", "percent", "%", "", "engine_sql", "sys.dm_os_performance_counters：Databases / Log File(s) Used Size (KB) ÷ Log File(s) Size (KB)（取各库最大值，回退 Percent Log Used）", "max", ">=", "高", "gauge",
		"日志写满会直接阻塞所有写入（错误 9002）；确认日志备份是否正常执行、恢复模式是否与备份策略匹配"},
	// 容量：数据文件 / 日志文件 / TempDB
	{MetricEngDataFileBytes, "数据文件大小", CategoryCapacity, "engine", engineScopeSQLServer, "", "gauge", "B", "", "engine_sql", "sys.dm_os_performance_counters：Databases / Data File(s) Size (KB)（_Total）", "last", "", "低", "gauge",
		"实例所有数据文件的总大小（不含日志）；用于容量规划与增长趋势判断"},
	{MetricEngLogFileBytes, "日志文件大小", CategoryCapacity, "engine", engineScopeSQLServer, "", "gauge", "B", "", "engine_sql", "sys.dm_os_performance_counters：Databases / Log File(s) Size (KB)（_Total）", "last", "", "低", "gauge",
		"实例所有事务日志文件的总大小；增长过快说明日志备份间隔过长或存在未提交的长事务"},
	{MetricEngTempdbUsedBytes, "TempDB 已用空间", CategoryCapacity, "engine", engineScopeSQLServer, "", "gauge", "B", "", "engine_sql", "Databases / Data File(s) Size (KB)（tempdb）− Transactions / Free Space in tempdb (KB)", "last", "", "中", "gauge",
		"TempDB 被临时对象、排序哈希与版本存储共同消耗；打满会导致整实例写入失败，需拆分数据文件或治理大查询"},
	{MetricEngTempdbVersionBytes, "版本存储占用", CategoryCapacity, "engine", engineScopeSQLServer, "", "gauge", "B", "", "engine_sql", "sys.dm_os_performance_counters：Transactions / Version Store Size (KB)", "last", ">=", "中", "gauge",
		"版本存储由快照隔离与 AlwaysOn 可读副本驱动；长期不回落说明存在未提交的长事务导致快照无法清理"},
	// 数据文件 IO
	{MetricEngIOReadBytesRate, "数据文件读吞吐", CategoryPerformance, "engine", engineScopeSQLServer, "", "counter_rate", "B/s", "", "engine_sql", "sys.dm_io_virtual_file_stats：num_of_bytes_read 增量", "avg", "", "低", "line",
		"物理读吞吐高说明缓冲池命中不足；与 Page Life Expectancy 联动判断内存是否够用"},
	{MetricEngIOWriteBytesRate, "数据文件写吞吐", CategoryPerformance, "engine", engineScopeSQLServer, "", "counter_rate", "B/s", "", "engine_sql", "sys.dm_io_virtual_file_stats：num_of_bytes_written 增量", "avg", "", "低", "line",
		"写吞吐主要来自检查点与惰性写；逼近磁盘上限时先扩容 IO 能力再谈参数调优"},
	{MetricEngIOReadLatencyMs, "数据文件读延迟", CategoryPerformance, "engine", engineScopeSQLServer, "", "gauge", "ms", "", "engine_sql", "Δio_stall_read_ms / Δnum_of_reads（sys.dm_io_virtual_file_stats）；本轮无新增 IO 时取实例启动以来累计均值", "avg", ">=", "中", "line",
		"单次读的平均等待时长；SSD 超过 5ms、机械盘超过 20ms 即需关注存储或物理读放大"},
	{MetricEngIOWriteLatencyMs, "数据文件写延迟", CategoryPerformance, "engine", engineScopeSQLServer, "", "gauge", "ms", "", "engine_sql", "Δio_stall_write_ms / Δnum_of_writes（sys.dm_io_virtual_file_stats）；本轮无新增 IO 时取实例启动以来累计均值", "avg", ">=", "中", "line",
		"写延迟高会拖慢提交与检查点；优先排查日志文件与数据文件是否共用同一块物理盘"},
	// 等待统计
	{MetricEngWaitTimeMsRate, "等待总时长速率", CategoryPerformance, "engine", engineScopeSQLServer, "", "counter_rate", "ms/s", "", "engine_sql", "sys.dm_os_wait_stats：wait_time_ms 增量（已排除空闲类等待）", "avg", ">=", "中", "line",
		"每秒累积的等待毫秒数，刻画实例整体等待压力；用于发现「吞吐上不去但没有明显慢查询」的隐性瓶颈，具体定位仍需看等待类型排名"},

	// ---------------- TiDB 方言（engine 层：TiDB 特有表；kernel 层：集群组件） ----------------
	// 采集口径：全部经 MySQL 协议直连查询（TiDB Server 是 SQL 层的单一入口），
	// 不依赖部署 Prometheus。INFORMATION_SCHEMA.TIKV_STORE_STATUS 在 TiDB 7.5+ 已被移除，
	// 相关指标在受影响版本上显式标 unsupported（口径与「变量不存在」一致），不会静默缺数。
	// 事务与锁
	{MetricEngTIDBTrxActive, "活跃事务数", CategoryPerformance, "engine", engineScopeTIDB, "", "gauge", "count", "", "engine_sql", "information_schema.tidb_trx 行数", "max", ">", "中", "gauge",
		"TiDB 集群当前活跃事务总数；长事务会阻塞 TiKV 的 MVCC GC，导致旧版本堆积与 Region 空间膨胀"},
	{MetricEngTIDBLockWaits, "锁等待数", CategoryPerformance, "engine", engineScopeTIDB, "", "gauge", "count", "", "engine_sql", "information_schema.data_lock_waits 行数", "max", ">", "中", "line",
		"正在等锁的事务对数量；配合 DATA_LOCK_WAITS 定位阻塞源，再查 TIDB_TRX 找到持有事务"},
	// 集群组件健康（PD / TiDB / TiKV）
	{MetricKernTIDBNodeTotal, "TiDB 节点数", CategoryAvailability, "kernel", engineScopeTIDB, "", "gauge", "count", "", "engine_sql", "information_schema.cluster_info WHERE type='tidb'", "last", "", "低", "stat",
		"接入层的 TiDB Server 节点总数；从本实例视角只反映可见的集群拓扑，缩容后数量应回落"},
	{MetricKernTIDBNodeAbnormal, "异常组件节点数", CategoryAvailability, "kernel", engineScopeTIDB, "", "gauge", "count", "", "engine_sql", "information_schema.cluster_info 中 status != 'UP' 的节点数（含 tidb/tikv/pd/tiflash/cdc）", "max", ">", "高", "line",
		"任意组件（TiDB/TiKV/PD/TiFlash/TiCDC）出现非 UP 节点都意味着集群拓扑受损；持续非 0 必须处理"},
	{MetricKernTiKVStoreTotal, "TiKV Store 数", CategoryAvailability, "kernel", engineScopeTIDB, "", "gauge", "count", "", "engine_sql", "information_schema.tikv_store_status（TiDB 7.5+ 已移除该表，需经 Prometheus 采集）", "last", "", "低", "stat",
		"TiKV 存储节点总数；数量异常回落说明有 store 被摘除或下线"},
	{MetricKernTiKVStoreDown, "异常 TiKV Store 数", CategoryAvailability, "kernel", engineScopeTIDB, "", "gauge", "count", "", "engine_sql", "information_schema.tikv_store_status 中 state_name 不为 Up 的 store 数（TiDB 7.5+ 需经 Prometheus 采集）", "max", ">", "高", "line",
		"非 Up 的 store 会触发副本重调度；多于副本数-1 时存在数据不可用风险"},
	{MetricKernTiKVRegionTotal, "Region 总数", CategoryCapacity, "kernel", engineScopeTIDB, "", "gauge", "count", "", "engine_sql", "information_schema.tikv_store_status.region_count 求和（TiDB 7.5+ 需经 Prometheus 采集）", "max", "", "低", "gauge",
		"集群数据分布的粒度总量；增长速率反映写入热度与容量水位，单 Region 默认 96MB 触发分裂"},
	{MetricKernTiKVStoreUsedPct, "TiKV 存储使用率", CategoryCapacity, "kernel", engineScopeTIDB, "", "percent", "%", "", "engine_sql", "information_schema.tikv_store_status：(capacity − available) / capacity 取最大（TiDB 7.5+ 需经 Prometheus 采集）", "max", ">=", "高", "gauge",
		"使用率最高的 store 决定集群写入上限；超过 80% 会触发 PD 空间不足告警并可能拒绝写入"},
	{MetricKernTiKVHotRegions, "热点 Region 数", CategoryPerformance, "kernel", engineScopeTIDB, "", "gauge", "count", "", "engine_sql", "information_schema.tidb_hot_regions 行数（按读/写热点统计）", "max", ">", "中", "line",
		"热点 Region 意味着流量集中在个别 TiKV 节点；持续存在时考虑表设计打散或调大 Region 热点调度"},

	// ---------------- 内核层（方言专有） ----------------
	{MetricKernRedoWriteRate, "Redo/WAL 写入速率", CategoryPerformance, "kernel", "MySQL,TiDB,Doris,PostgreSQL,Kingbase,Oracle", "", "counter_rate", "B/s", "", "engine_sql", "Innodb_os_log_written / pg_stat_wal.wal_bytes / v$sysstat redo size", "avg", "", "低", "line",
		"写放大与 IO 压力的根源指标；速率与磁盘写吞吐偏离时关注双写与全页写（Oracle 口径为 redo size 每秒增量）"},
	{MetricKernWALSwitchRate, "WAL 段切换速率", CategoryPerformance, "kernel", engineScopePostgreSQL, "", "counter_rate", "count/s", "", "engine_sql", "pg_ls_waldir()", "avg", "", "低", "line",
		"切换过于频繁说明 wal_keep 或 checkpoint 配置不匹配写入量"},
	{MetricKernCheckpointAge, "Checkpoint 年龄", CategoryRisk, "kernel", engineScopeMySQLGroup, "", "gauge", "B", "", "engine_sql", "SHOW ENGINE INNODB STATUS", "max", "", "高", "gauge",
		"年龄逼近 redo 容量上限会触发激进刷脏风暴；同时决定崩溃恢复时长"},
	{MetricKernBufferDirtyPct, "缓冲池脏页比例", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "percent", "%", "", "engine_sql", "Innodb_buffer_pool_pages_dirty/total", "last", ">=", "中", "gauge",
		"脏页比例过高说明刷脏跟不上写入；突发刷脏会造成查询抖动"},
	{MetricKernFlushRate, "刷脏速率", CategoryPerformance, "kernel", "MySQL,TiDB,Doris,PostgreSQL,Kingbase", "", "counter_rate", "pages/s", "", "engine_sql", "Innodb_buffer_pool_pages_flushed / pg_stat_bgwriter", "avg", "", "低", "line",
		"与磁盘写吞吐联动看：IO 被刷脏吃掉时业务读写会排队"},
	{MetricKernHistoryList, "Undo 历史链长度", CategoryRisk, "kernel", engineScopeMySQLGroup, "", "gauge", "count", "", "engine_sql", "SHOW ENGINE INNODB STATUS", "max", ">=", "高", "line",
		"长事务阻碍 purge 导致 undo 膨胀；定位并结束长事务后观察回落"},
	{MetricKernDeadTuples, "死元组数", CategoryRisk, "kernel", engineScopePostgreSQL, "", "gauge", "count", "", "engine_sql", "pg_stat_user_tables.n_dead_tup", "max", "", "中", "line",
		"死元组堆积导致表膨胀与扫描变慢；确认 autovacuum 是否跟得上"},
	{MetricKernAutovacuumWorkers, "Autovacuum 工作进程", CategoryPerformance, "kernel", engineScopePostgreSQL, "", "gauge", "count", "", "engine_sql", "pg_stat_activity", "last", "", "低", "stat",
		"长期打满 worker 上限说明回收压力大；检查单表级 vacuum 配置"},
	{MetricKernRollbackRatio, "事务回滚率", CategoryRisk, "kernel", "MySQL,TiDB,Doris,PostgreSQL,Kingbase", "", "percent", "%", "", "engine_sql", "rollback/(commit+rollback)", "last", ">=", "中", "gauge",
		"回滚率异常升高通常对应应用错误、死锁或唯一键冲突"},
	{MetricKernLWLockWaits, "轻量锁等待", CategoryPerformance, "kernel", engineScopePostgreSQL, "", "gauge", "count", "", "engine_sql", "pg_stat_activity wait_event_type=LWLock", "max", "", "中", "line",
		"LWLock 等待说明内核内部争抢；结合 wait_event 定位具体闩锁"},
	{MetricKernReadAheadEvict, "预读失效率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "percent", "%", "", "engine_sql", "read_ahead_evicted/read_ahead", "last", "", "低", "line",
		"预读页未被访问即被逐出是纯粹的 IO 浪费；常见于大范围扫描"},
	{MetricKernPageSplitRate, "索引页分裂速率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "innodb_metrics index_page_splits", "avg", "", "低", "line",
		"频繁页分裂导致索引碎片化与写放大；检查自增主键与填充因子"},
	{MetricKernTableCacheHit, "表缓存命中率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "percent", "%", "", "engine_sql", "Table_open_cache_hits/misses", "last", "<=", "低", "gauge",
		"命中率低说明 table_open_cache 不足；每次 miss 都要重新打开表文件"},
	{MetricKernDBLWRWriteRate, "Doublewrite 写入速率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "pages/s", "", "engine_sql", "Innodb_dblwr_writes", "avg", "", "低", "line",
		"双写是崩溃安全的 IO 代价；与 redo 速率联动判断写放大"},
	{MetricKernWALFPIRate, "全页写速率", CategoryPerformance, "kernel", engineScopePostgreSQL, "", "counter_rate", "count/s", "", "engine_sql", "pg_stat_wal.wal_fpi", "avg", "", "低", "line",
		"checkpoint 后的首次写会记录整页；FPI 突增对应 WAL 体积放大"},
	{MetricKernCheckpointReqPct, "请求型检查点占比", CategoryPerformance, "kernel", engineScopePostgreSQL, "", "percent", "%", "", "engine_sql", "pg_stat_bgwriter checkpoints_req/timed", "last", "", "中", "gauge",
		"请求型占比高说明 checkpoint 被 WAL 推着走；调大 max_wal_size 缓解"},

	// InnoDB 方言补充：Buffer Pool / Data IO / 行锁 / 行操作 / Redo
	{MetricKernBufferDirtyPages, "缓冲池脏页数", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "gauge", "count", "", "engine_sql", "Innodb_buffer_pool_pages_dirty", "last", "", "中", "line",
		"脏页数持续堆积说明刷脏跟不上写入；与刷脏速率联动判断是否 IO 瓶颈"},
	{MetricKernBufferReadReq, "缓冲池读请求速率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Innodb_buffer_pool_read_requests", "avg", "", "低", "line",
		"逻辑读速率是实例读压力的直接体现；与命中率联动判断读放大"},
	{MetricKernBufferWriteReq, "缓冲池写请求速率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Innodb_buffer_pool_write_requests", "avg", "", "低", "line",
		"写请求速率抬升时关注脏页比例与刷脏是否跟得上"},
	{MetricKernDataReadRate, "InnoDB 读吞吐", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "B/s", "", "engine_sql", "Innodb_data_read", "avg", "", "低", "line",
		"读吞吐高说明物理读放大；结合命中率与临时表落盘排查"},
	{MetricKernDataWrittenRate, "InnoDB 写吞吐", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "B/s", "", "engine_sql", "Innodb_data_written", "avg", "", "低", "line",
		"写吞吐与 redo、刷脏速率联动；逼近磁盘上限时先落盘再调优"},
	{MetricKernRowLockTimeMs, "行锁等待总时长", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "gauge", "ms", "", "engine_sql", "Innodb_row_lock_time（累计）", "max", "", "中", "line",
		"累计值本身不具告警意义，看每秒增量与平均等待时长；突增说明热点行竞争"},
	{MetricKernRowLockTimeAvgMs, "行锁平均等待时长", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "gauge", "ms", "", "engine_sql", "Innodb_row_lock_time_avg 或 time/waits", "max", ">=", "中", "line",
		"平均等待超过 100ms 即会明显影响事务响应；定位热点行并拆分事务"},
	{MetricKernRowLockTimeRate, "行锁等待时长速率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "ms/s", "", "engine_sql", "Innodb_row_lock_time", "avg", "", "中", "line",
		"每秒被锁住的累计时长，能反映并发争抢强度；结合等待次数看单次代价"},
	{MetricKernRowLockWaits, "行锁等待次数", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "gauge", "count", "", "engine_sql", "Innodb_row_lock_waits（累计）", "max", "", "中", "line",
		"累计等待次数持续增长说明存在稳定热点；用 data_lock_waits 定位阻塞源"},
	{MetricKernRowLockWaitsRate, "行锁等待速率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Innodb_row_lock_waits", "avg", "", "中", "line",
		"等待速率与回滚率联动看：突增通常伴随死锁或热点更新"},
	{MetricKernDataFsyncsRate, "数据文件 fsync 速率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Innodb_data_fsyncs", "avg", "", "低", "line",
		"fsync 频繁意味着刷盘压力大；与 innodb_flush_neighbors、IO 能力一起评估"},
	{MetricKernSortRowsRate, "排序行速率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Sort_rows", "avg", "", "低", "line",
		"排序行数高且临时表落盘率高，说明缺少合适索引或 sort_buffer 偏小"},
	{MetricKernRowsDeletedRate, "删除行速率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "rows/s", "", "engine_sql", "Innodb_rows_deleted", "avg", "", "低", "line",
		"删除速率与 undo 历史链联动；大批量删除建议分批限速"},
	{MetricKernRowsInsertedRate, "插入行速率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "rows/s", "", "engine_sql", "Innodb_rows_inserted", "avg", "", "低", "line",
		"插入速率决定 redo 与刷脏压力；与写吞吐一起判断磁盘容量规划"},
	{MetricKernRowsUpdatedRate, "更新行速率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "rows/s", "", "engine_sql", "Innodb_rows_updated", "avg", "", "低", "line",
		"更新速率高会带来行锁与 undo 压力；关注热点行与批量更新"},
	{MetricKernLogWriteReqRate, "日志写请求速率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Innodb_log_write_requests", "avg", "", "低", "line",
		"请求数与物理写次数差距越大，说明组提交合并效果越好"},
	{MetricKernLogWritesRate, "Redo 物理写速率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Innodb_log_writes", "avg", "", "低", "line",
		"物理写次数逼近磁盘能力时事务提交会阻塞；关注 redo 文件大小与磁盘延迟"},
	{MetricKernOsLogFsyncsRate, "Redo fsync 速率", CategoryPerformance, "kernel", engineScopeMySQLGroup, "", "counter_rate", "count/s", "", "engine_sql", "Innodb_os_log_fsyncs", "avg", "", "低", "line",
		"fsync 次数决定提交延迟；与 innodb_flush_log_at_trx_commit 配置一起看"},
}

// collectScopeEnabled 该 scope 是否参与直连采集（引擎层与内核层都走 SQL 直连）。
func collectScopeEnabled(scope string) bool {
	return scope == "engine" || scope == "kernel"
}

// loadEnabledMetricKeys 返回参与采集的指标键。
//
// 改造说明：指标字典模块已下线，采集范围不再由"运维在字典里勾选启用"决定，
// 固定取代码内置的指标全集（builtinMetricCatalog）按引擎过滤后的结果。
// 指标是否被采集只由指标模板决定（模板勾选 ∩ 引擎适用性），不再有"字典总闸"这一层。
func loadEnabledMetricKeys(engine string) []string {
	keys := make([]string, 0, len(builtinMetricCatalog))
	for _, item := range builtinMetricCatalog {
		if collectScopeEnabled(item.Scope) && engineInScope(engine, item.EngineScope) {
			keys = append(keys, item.MetricKey)
		}
	}
	return keys
}

// MetricCatalogItem 指标的对外结构（指标模板勾选 / 告警配置指标清单共用）。
//
// 注意：它叫 CatalogItem 但已不再是"指标字典"——字典模块下线后，
// 这份结构只是把代码内置的指标定义（builtinMetricCatalog）映射给前端，
// 没有任何数据库来源，也没有可运维改写的字段（原 builtin / enabled 已移除）。
type MetricCatalogItem struct {
	MetricKey     string   `json:"metric_key"`
	DisplayName   string   `json:"display_name"`
	Category      string   `json:"category"`
	CategoryLabel string   `json:"category_label"`
	Scope         string   `json:"scope"`
	EngineScope   string   `json:"engine_scope"`
	Engines       []string `json:"engines"`
	ValueType     string   `json:"value_type"`
	Unit          string   `json:"unit"`
	SubDims       string   `json:"sub_dims"`
	SourceType    string   `json:"source_type"`
	SourceRef     string   `json:"source_ref"`
	Agg           string   `json:"agg"`
	DisplayHint   string   `json:"display_hint"`
	Advice        string   `json:"advice"`
	// Enabled 是否可作为指标模板的采集项：有采集源的内置指标为 true，
	// 无采集源的明细占位行（detail-only）为 false —— 模板勾选界面据此禁用勾选。
	// 注意它不是原字典的"全局启用总闸"：那个开关随指标字典一并下线，
	// 指标是否采集只由指标模板决定，不再有第二层总闸。
	Enabled bool `json:"enabled"`
	// Layer 明细口径行的层级（host/engine/kernel）；layout=1 目录里所有行都按展示目录的
	// Tab 填写（见 displayLayoutCatalogItems），前端无需再按 key 前缀推导
	Layer string `json:"layer"`
	// Available 是否有采集源；明细里有、平台没采的行 available=false（不可勾选、不进模板）
	Available bool `json:"available"`
	// DetailItem 该行在监控明细中所属的监控项（如 网络流量），便于与明细页对账
	DetailItem string `json:"detail_item"`
}

func categoryLabel(category string) string {
	switch category {
	case CategoryAvailability:
		return "可用性"
	case CategoryCapacity:
		return "容量"
	case CategoryPerformance:
		return "性能"
	case CategoryRisk:
		return "风险"
	case CategoryChange:
		return "变更"
	default:
		return category
	}
}

func engineInScope(engine string, scope string) bool {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return true
	}
	normalized := strings.ToLower(strings.TrimSpace(engine))
	for _, item := range strings.Split(scope, ",") {
		if strings.ToLower(strings.TrimSpace(item)) == normalized {
			return true
		}
	}
	return false
}

// ============================================================================
// 四、引擎能力注册表
//
// 现状是被入口 SQL 卡死：listInstances() 硬编码 db_type IN ('MySQL','PostgreSQL')，
// 而 collectDirectEngineMetrics 已实现 6 种引擎。结果是加 TiDB 实例进监控页根本看不到。
// 改成注册表后，"不支持"变成页面上可见的状态，而不是静默丢失。
// ============================================================================

type EngineCapability struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Implemented bool   `json:"implemented"`
	Note        string `json:"note"`
}

var engineCapabilities = []EngineCapability{
	{"mysql", "MySQL", true, "SHOW GLOBAL STATUS + SHOW REPLICA STATUS 直连采集"},
	{"postgresql", "PostgreSQL", true, "pg_stat_database / pg_stat_activity / pg_locks / pg_stat_replication"},
	{"kingbase", "Kingbase", true, "兼容 PostgreSQL 协议，复用 PG 采集器（pg_stat_database / pg_stat_activity / pg_locks / pg_stat_replication）"},
	{"tidb", "TiDB", true, "兼容 MySQL 协议（SHOW GLOBAL STATUS）+ TiDB 特有表（cluster_info / tidb_trx / data_lock_waits / tikv_store_status / tidb_hot_regions）直连采集；TiKV 细分指标（QPS/延迟等）建议走 Prometheus"},
	{"doris", "Doris", true, "兼容 MySQL 协议，复用 MySQL 采集器"},
	{"clickhouse", "ClickHouse", true, "system.events + system.metrics"},
	{"sqlserver", "SQLServer", true, "sys.dm_os_performance_counters / sys.dm_exec_* / sys.dm_io_virtual_file_stats / sys.dm_os_wait_stats 直连采集"},
	{"oracle", "Oracle", true, "v$sysstat / v$session / v$resource_limit / dba_data_files 直连采集"},
}

var engineCapabilityIndex = func() map[string]EngineCapability {
	index := make(map[string]EngineCapability, len(engineCapabilities))
	for _, item := range engineCapabilities {
		index[item.Key] = item
	}
	return index
}()

// resolveEngine 归一化引擎标识。
//
// 除了去空白 + 转小写，还要把「同一引擎的别名」收敛到一个 key：
// 金仓的历史写法有 kingbase / kingbasees / kingbase es 多种，而采集分支、
// 展示目录（displayLayouts）、模板治理（hostMetricsInTemplate）都按 key 索引，
// 不归一就会出现「采集认、展示不认」的半通状态 —— 金仓实例少一整个
// 「基础指标」Tab 就是这么来的。归一规则与 internal/common/dbfamily 保持一致。
func resolveEngine(dbType string) string {
	switch key := strings.ToLower(strings.TrimSpace(dbType)); key {
	case "kingbasees", "kingbase es", "kingbase-es":
		return "kingbase"
	default:
		return key
	}
}

// engineImplemented 该引擎是否具备引擎层采集能力。
// 未实现的引擎不会被隐藏，而是在监控对象页标灰并给出说明 —— 让"不支持"可见。
func engineImplemented(dbType string) bool {
	capability, ok := engineCapabilityIndex[resolveEngine(dbType)]
	if !ok {
		return false
	}
	return capability.Implemented
}

// monitorableEngineKeys 返回所有已实现采集的引擎（小写），用于构造 IN 查询。
func monitorableEngineKeys() []string {
	keys := make([]string, 0, len(engineCapabilities))
	for _, item := range engineCapabilities {
		if item.Implemented {
			keys = append(keys, item.Key)
		}
	}
	sort.Strings(keys)
	return keys
}

// EngineCapabilityList 供 API/前端展示
func EngineCapabilityList() []EngineCapability {
	result := make([]EngineCapability, len(engineCapabilities))
	copy(result, engineCapabilities)
	return result
}
