package services

// ============================================================================
// 监控明细页「展示目录」——按数据库类型定制的 Tab 与监控项
//
// 背景：明细页的 Tab 与监控项曾经完全由「指标字典 + 模板」推导，前端按 layer
// （host/engine/kernel）分组展示。业务侧给出的口径是另一套：以运维熟悉的
// 监控项为单位（如「网络流量」= input_traffic + output_traffic 两根线），
// 并且严格指定了监控项名称、定义、单位与排列顺序。
//
// 设计原则：
//  1. 目录是「唯一事实来源」：Tab 数量、监控项顺序、名称、定义、单位全部由此文件
//     决定，前端只负责按目录排版，不得自行增删或排序 —— 避免前后端各写一套口径。
//  2. 一个监控项可以包含多行指标：每行是一根曲线（Series），顺序即图例顺序。
//  3. Series.MetricKey 是平台实际采集键；为空表示「业务口径里有，但当前没有采集源」，
//     此时卡片照常渲染、值与曲线为空并显示原因 —— 绝不静默删项，也绝不编数据。
//  4. 目录按 db_type 下发：MySQL 三个 Tab，PostgreSQL 两个 Tab；
//     附件里没有的指标一律不补，未收录的类型回落到旧的字典推导（前端兼容）。
// ============================================================================

import (
	"sort"
	"strings"
)

// DisplaySeries 一个监控项内的一行指标（一根曲线）。
type DisplaySeries struct {
	// Key 附件里的指标名（展示用，如 input_traffic）；附件未给出的用监控项名兜底。
	Key string `json:"key"`
	// MetricKey 平台实际采集键（如 perf.write_bytes）。空串 = 当前无采集源。
	MetricKey string `json:"metric_key"`
	// Definition 指标定义 / 说明，严格按附件录入。
	Definition string `json:"definition"`
	// Unit 单位，严格按附件录入；附件未给出则为空串（不自行推断）。
	Unit string `json:"unit"`
	// Scale 采集值 → 展示值的换算系数（默认 1）。
	// 仅用于「采集单位与附件单位量纲一致但进制不同」的确定性换算，
	// 例如采集 B/s、附件要求 KB 时填 1/1024。禁止用在这里做语义猜测。
	Scale float64 `json:"scale"`
	Order int     `json:"order"`
	// NotApplicable 该行指标在当前实例角色下结构性不存在（如单机实例上的「备库 APPLY 延迟」）。
	// 默认 false（字面量初始化的行指标均不受角色约束），只有 MonitorDisplayLayoutFor
	// 按实例角色判定后才置 true。
	//
	// 与「模板未勾选」「采集失败」是三种不同状态，必须分开表达：
	//   - 模板未勾选 → 整行不下发（filterDisplayByTemplate）；
	//   - 采集失败 → 有值但 quality != ok，显示「数据不可用」；
	//   - 角色不适用 → 本字段为 true，卡片照常渲染但值为空，并显示 NotApplicableReason。
	// 以前这一类被直接丢弃，导致「指标模板里有、监控明细里没有」看起来像漏配。
	NotApplicable bool `json:"not_applicable"`
	// NotApplicableReason 不适用原因（面向运维，直接显示在卡片底部）。
	NotApplicableReason string `json:"not_applicable_reason"`
}

// DisplayItem 一个监控项（页面上的一张卡片）。
type DisplayItem struct {
	Key    string          `json:"key"`
	Tab    string          `json:"tab"` // host / engine / kernel
	Name   string          `json:"name"`
	Order  int             `json:"order"`
	Series []DisplaySeries `json:"series"`
}

// DisplayTab 一个 Tab。
type DisplayTab struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// MonitorDisplayLayout 某个数据库类型的完整展示目录。
type MonitorDisplayLayout struct {
	DbType string        `json:"db_type"`
	Tabs   []DisplayTab  `json:"tabs"`
	Items  []DisplayItem `json:"items"`
}

// KB = 1024 字节：附件里网络/字节类指标的单位换算基准。
const (
	scaleByteToKB = 1.0 / 1024
	scaleByteToMB = 1.0 / 1024 / 1024
	scaleNoop     = 1.0
)

// mysqlDisplayItems MySQL 展示目录：基础指标 / 数据库指标 / 引擎指标三个 Tab。
// 顺序与附件表格逐行一致，不增不减。
var mysqlDisplayItems = []DisplayItem{
	// ---------------- 基础指标 ----------------
	{Key: "mysql.host.cpu_usage", Tab: LayerHost, Name: "CPU 使用率", Order: 1, Series: []DisplaySeries{
		{Key: "cpu_usage", MetricKey: MetricResCPU, Definition: "实例 CPU 使用率，统计对象为实例所在主机的 CPU（全部核心合计），计算口径为采集周期内非空闲时间占总 CPU 时间的比例，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "mysql.host.memory_usage", Tab: LayerHost, Name: "内存使用率", Order: 2, Series: []DisplaySeries{
		{Key: "mem_usage", MetricKey: MetricResMemory, Definition: "实例内存使用率，统计对象为实例所在主机的内存，计算口径为已使用内存占操作系统总内存的比例，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "mysql.host.disk_usage", Tab: LayerHost, Name: "磁盘使用率", Order: 3, Series: []DisplaySeries{
		{Key: "disk_usage", MetricKey: MetricResDisk, Definition: "磁盘使用率，统计对象为实例所在主机的全部真实磁盘分区（已排除 tmpfs/overlay 等伪文件系统与 /boot 启动分区），计算口径为这些分区中已用空间占比最高的那个分区，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	// 磁盘使用量（绝对容量）：与「磁盘使用率」同源（node_filesystem_*），
	// 已用 = size - avail，按 device 去重后求和，得到该主机所有真实磁盘分区的已用总量。
	{Key: "mysql.host.disk_used", Tab: LayerHost, Name: "磁盘使用量", Order: 4, Series: []DisplaySeries{
		// Unit 与 Scale 必须与采集口径一致：采集表达式得到的是原始字节（B），
		// 前端 chartUnits 会根据 unitDim('B') 自动换算为 GB/MB 展示。
		// 若这里写 Unit='GB' + Scale=ByteToGB，后端把字节折成 GB 后再下发，
		// 前端又会按 GB 做 toBytes 再乘 1024^3，导致 Tooltip 出现 59128532992 GB。
		{Key: "disk_used", MetricKey: MetricResDiskUsed, Definition: "磁盘使用量，统计对象为实例所在主机的全部真实磁盘分区（已排除 tmpfs/overlay 等伪文件系统与 /boot 启动分区，同一块盘重复挂载只计一次），计算口径为各分区总容量减去可用容量后的已用空间之和，取值范围 0 ~ 该主机磁盘总容量", Unit: "B", Order: 1, Scale: scaleNoop},
	}},
	// 数据库网络流量：客户端视角的收发字节速率，对应 MySQL Bytes_received / Bytes_sent。
	// 注意这是「MySQL 服务端协议层」而非主机网卡：走 mysqldump / binlog dump 都计，
	// 但 scp、应用自身的外网流量一概不计。要看整机网卡见下方 net_traffic_host。
	{Key: "mysql.host.network_traffic", Tab: LayerHost, Name: "数据库网络流量", Order: 5, Series: []DisplaySeries{
		{Key: "input_traffic", MetricKey: MetricPerfWriteBytes, Definition: "平均每秒从所有客户端接收到的字节数", Unit: "KB", Order: 1, Scale: scaleByteToKB},
		{Key: "output_traffic", MetricKey: MetricPerfReadBytes, Definition: "平均每秒发送给所有客户端的字节数", Unit: "KB", Order: 2, Scale: scaleByteToKB},
	}},
	// 主机网卡流量：整机视角，含 scp / SSH / 应用外网等一切经过物理网卡的流量。
	// 来源 Prometheus node_network_*（已排除 lo/docker/veth/br/cni），
	// 与上面「数据库网络流量」差着一两个数量级属正常 —— 两者统计对象完全不同。
	{Key: "mysql.host.net_traffic_host", Tab: LayerHost, Name: "主机网络流量", Order: 6, Series: []DisplaySeries{
		{Key: "net_in", MetricKey: MetricResNetworkIn, Definition: "主机网卡平均每秒接收字节数（已排除回环与容器虚拟网卡）", Unit: "B/s", Order: 1, Scale: scaleNoop},
		{Key: "net_out", MetricKey: MetricResNetworkOut, Definition: "主机网卡平均每秒发送字节数（已排除回环与容器虚拟网卡）", Unit: "B/s", Order: 2, Scale: scaleNoop},
	}},
	{Key: "mysql.host.iops", Tab: LayerHost, Name: "IOPS", Order: 7, Series: []DisplaySeries{
		{Key: "data_io_bytes", MetricKey: MetricResDiskThroughput, Definition: "每秒读写吞吐量", Unit: "B/s", Order: 1, Scale: scaleNoop},
		{Key: "data_io_ps", MetricKey: MetricResDiskIOPS, Definition: "读写次数", Unit: "Count", Order: 2, Scale: scaleNoop},
	}},
	// ---- 主机层扩展（node_exporter，8-11）：与告警模板默认规则对齐补入 ----
	// 这些指标一直在采（res.* 不受模板门控），此前只是明细页没有展示，
	// 导致「IO 使用率 / Swap / 磁盘延迟 / 网络错误 / 文件句柄 / 时钟偏移」
	// 出现在告警规则里却查不到曲线。补齐后明细 ⊇ 告警规则集。
	{Key: "mysql.host.io_latency", Tab: LayerHost, Name: "IO 与磁盘延迟", Order: 8, Series: []DisplaySeries{
		{Key: "io_usage", MetricKey: MetricResIO, Definition: "磁盘 IO 使用率，统计对象为实例所在主机的全部真实磁盘，计算口径为采集周期内磁盘处理 IO 请求的时间占比，取值范围 0 ~ 100，持续接近 100 说明磁盘趋于饱和", Unit: "%", Order: 1, Scale: scaleNoop},
		{Key: "disk_await", MetricKey: MetricResDiskAwaitMs, Definition: "磁盘平均 IO 延迟，统计对象为实例所在主机的全部真实磁盘，计算口径为单次读写操作的平均耗时（毫秒）；机械盘 >10ms、SSD >2ms 即需关注", Unit: "ms", Order: 2, Scale: scaleNoop},
	}},
	{Key: "mysql.host.swap", Tab: LayerHost, Name: "Swap 使用率", Order: 9, Series: []DisplaySeries{
		{Key: "swap_used", MetricKey: MetricResSwapUsedPct, Definition: "Swap 使用率，统计对象为实例所在主机的操作系统交换分区，计算口径为已用 Swap 占 Swap 总量的比例，取值范围 0 ~ 100；数据库进程被换出会造成明显的延迟抖动", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "mysql.host.net_errors", Tab: LayerHost, Name: "网络错误/丢包", Order: 10, Series: []DisplaySeries{
		{Key: "net_errors", MetricKey: MetricResNetErrors, Definition: "主机网卡每秒错误与丢包总数，统计对象为实例所在主机的物理网卡（已排除回环与容器虚拟网卡），计算口径为收发错误（errs）与丢包（drop）速率之和；丢包会放大跨机复制与连接超时", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "mysql.host.proc_clock", Tab: LayerHost, Name: "进程与时钟", Order: 11, Series: []DisplaySeries{
		{Key: "fd_used", MetricKey: MetricResFDUsedPct, Definition: "文件句柄使用率，统计对象为实例所在主机的系统级文件句柄，计算口径为已分配句柄数占句柄上限的比例，取值范围 0 ~ 100；句柄耗尽会导致无法打开表文件甚至实例崩溃", Unit: "%", Order: 1, Scale: scaleNoop},
		{Key: "time_offset", MetricKey: MetricResTimeOffsetS, Definition: "主机时钟偏移，统计对象为实例所在主机与 NTP 源的时钟偏差，计算口径为时钟偏差绝对值的最大值（秒）；时钟漂移影响复制心跳、备份时间戳与证书校验", Unit: "s", Order: 2, Scale: scaleNoop},
	}},

	// ---------------- 数据库指标 ----------------
	{Key: "mysql.engine.session", Tab: LayerEngine, Name: "会话连接", Order: 1, Series: []DisplaySeries{
		// 会话/连接视角：与「线程」卡片共用同一存储键，但语义是客户端会话而非服务端线程。
		// MySQL 线程每连接架构下二者数值相等，线程池插件下不等（见线程卡片注释）。
		{Key: "active_session", MetricKey: MetricPerfActiveConn, Definition: "当前活跃会话，即正在执行语句（非休眠）的客户端连接数，取自 SHOW STATUS 的 Threads_running，数值上等于线程卡片中的活跃线程数", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "total_session", MetricKey: MetricPerfConnCount, Definition: "当前全部会话，即已打开的客户端连接总数，取自 SHOW STATUS 的 Threads_connected，数值上等于线程卡片中的全部线程数；启用线程池插件后两者可能不等", Unit: "Count", Order: 2, Scale: scaleNoop},
		// 连接容量水位（12-13）：告警模板「活跃连接占比 / 连接使用率」对应的展示曲线
		{Key: "active_conn_pct", MetricKey: MetricCapConnUsedPct, Definition: "活跃连接占比，计算口径为活跃连接数除以最大连接数（max_connections）再乘 100，取值范围 0 ~ 100，接近 100 时新连接将被拒绝", Unit: "%", Order: 3, Scale: scaleNoop},
		{Key: "conn_thread_pct", MetricKey: MetricCapThreadUsedPct, Definition: "连接使用率，计算口径为当前连接总数（Threads_connected）除以最大连接数（max_connections）再乘 100，取值范围 0 ~ 100，持续偏高需评估调大 max_connections 或引入连接池", Unit: "%", Order: 4, Scale: scaleNoop},
	}},
	{Key: "mysql.engine.qps_tps", Tab: LayerEngine, Name: "QPS/TPS", Order: 2, Series: []DisplaySeries{
		{Key: "qps", MetricKey: MetricPerfQPS, Definition: "计算公式：Queries / Uptime", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "tps", MetricKey: MetricPerfTPS, Definition: "计算公式：(Com_insert + Com_insert_select + Com_update + Com_update_multi + Com_delete_multi + Com_delete + Com_replace + Com_replace_select) / Uptime", Unit: "Count", Order: 2, Scale: scaleNoop},
	}},
	{Key: "mysql.engine.dml_rate", Tab: LayerEngine, Name: "SQL执行次数", Order: 3, Series: []DisplaySeries{
		{Key: "delete", MetricKey: MetricEngComDelete, Definition: "平均每秒 DELETE 语句执行次数", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "insert", MetricKey: MetricEngComInsert, Definition: "平均每秒 INSERT 语句执行次数", Unit: "Count", Order: 2, Scale: scaleNoop},
		{Key: "insert_select", MetricKey: MetricEngComInsertSelect, Definition: "平均每秒 INSERT...SELECT 语句执行次数", Unit: "Count", Order: 3, Scale: scaleNoop},
		{Key: "replace", MetricKey: MetricEngComReplace, Definition: "平均每秒 REPLACE 语句执行次数", Unit: "Count", Order: 4, Scale: scaleNoop},
		{Key: "replace_select", MetricKey: MetricEngComReplaceSelect, Definition: "平均每秒 REPLACE...SELECT 语句执行次数", Unit: "Count", Order: 5, Scale: scaleNoop},
		{Key: "select", MetricKey: MetricEngComSelect, Definition: "平均每秒 SELECT 语句执行次数", Unit: "Count", Order: 6, Scale: scaleNoop},
		{Key: "update", MetricKey: MetricEngComUpdate, Definition: "平均每秒 UPDATE 语句执行次数", Unit: "Count", Order: 7, Scale: scaleNoop},
	}},
	{Key: "mysql.engine.select_scan", Tab: LayerEngine, Name: "全表扫描次数", Order: 4, Series: []DisplaySeries{
		{Key: "select_scan", MetricKey: MetricEngSelectScan, Definition: "平均每秒触发全表扫描的查询次数", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "mysql.engine.threads", Tab: LayerEngine, Name: "线程", Order: 5, Series: []DisplaySeries{
		// 注意：线程曲线与「会话连接」卡片共用 perf.conn_count / perf.active_conn_count 两个存储键
		// （采集源都是 SHOW STATUS 的线程计数器）。「线程」与「连接」是两个概念：
		// MySQL 线程每连接架构下数值恰好相等；线程池插件（企业版/Percona）下二者不等。
		{Key: "threads_connected", MetricKey: MetricPerfConnCount, Definition: "当前全部线程数，取自 SHOW STATUS 的 Threads_connected。MySQL 线程每连接架构下数值上等于当前客户端连接数，因此与告警目录中的「连接数」是同一个指标；启用线程池插件后线程数与连接数不再相等", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "threads_running", MetricKey: MetricPerfActiveConn, Definition: "当前活跃线程数，取自 SHOW STATUS 的 Threads_running（正在执行语句、非休眠状态的线程），数值上等于当前活跃连接数，与告警目录中的「活跃连接数」是同一个指标", Unit: "Count", Order: 2, Scale: scaleNoop},
	}},
	{Key: "mysql.engine.slow_queries", Tab: LayerEngine, Name: "慢查询数量", Order: 6, Series: []DisplaySeries{
		{Key: "slow_queries", MetricKey: MetricEngSlowQueries, Definition: "每秒慢查询数", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "mysql.engine.replica_threads", Tab: LayerEngine, Name: "节点复制线程状态", Order: 7, Series: []DisplaySeries{
		// 主从复制的 IO/SQL 线程，与上面「线程」卡片（客户端连接线程）完全是两回事；
		// 告警目录里对应「MySQL 复制 IO 线程」「MySQL 复制 SQL 线程」两条规则（repl.*）。
		{Key: "slave_io_running", MetricKey: MetricReplIORunning, Definition: "备库复制 IO 线程是否连接，取自 SHOW REPLICA STATUS 的 Replica_IO_Running（1=运行中）；对应告警规则「MySQL 复制 IO 线程 != 1」，与线程卡片中的客户端连接线程无关", Unit: "-", Order: 1, Scale: scaleNoop},
		{Key: "slave_sql_running", MetricKey: MetricReplSQLRunning, Definition: "备库复制 SQL 线程是否连接，取自 SHOW REPLICA STATUS 的 Replica_SQL_Running（1=运行中）；对应告警规则「MySQL 复制 SQL 线程 != 1」，与线程卡片中的客户端连接线程无关", Unit: "-", Order: 2, Scale: scaleNoop},
	}},
	// 复制延迟：与上面两条 0/1 布尔序列同源（SHOW REPLICA STATUS），但量纲是秒级数值。
	// 塞进同图会让 Y 轴值域被拉到几百秒，布尔线被压到贴底看不出状态，因此单独立项。
	// 命名与单位与指标字典一致（字典：复制延迟，单位 s）；告警模板默认规则即 >=300s。
	// TiDB / Doris 复用本目录，二者走 MySQL 采集分支，查不到时给出明确原因，不会常驻空卡。
	{Key: "mysql.engine.replica_lag", Tab: LayerEngine, Name: "复制延迟", Order: 8, Series: []DisplaySeries{
		{Key: "seconds_behind_source", MetricKey: MetricReplLagSeconds, Definition: "复制延迟，统计对象为备库复制通道，计算口径为 SHOW REPLICA STATUS 的 Seconds_Behind_Source（秒）；主库或未配置复制的实例无数据", Unit: "s", Order: 1, Scale: scaleNoop},
	}},
	// 临时表：原挂在「基础指标」。这四项都是 MySQL 服务器层状态变量（SHOW GLOBAL STATUS），
	// 与实例强相关、与主机无关，归入「数据库指标」更符合分层语义，并置于该分类末位。
	{Key: "mysql.engine.tmp_tables", Tab: LayerEngine, Name: "临时表", Order: 9, Series: []DisplaySeries{
		{Key: "created_tmp_disk_tables", MetricKey: MetricEngTmpDiskTables, Definition: "MySQL执行语句时在硬盘上自动创建的临时表的数量", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "Innodb_num_open_files", MetricKey: MetricEngInnodbOpenFiles, Definition: "Innodb打开文件数", Unit: "Count", Order: 2, Scale: scaleNoop},
		{Key: "open_files", MetricKey: MetricEngOpenFiles, Definition: "打开的文件数", Unit: "Count", Order: 3, Scale: scaleNoop},
		{Key: "open_tables", MetricKey: MetricEngOpenTables, Definition: "打开表数", Unit: "Count", Order: 4, Scale: scaleNoop},
		// 临时表落盘率（15）：告警模板「临时表落盘率 ≥30%」对应的展示曲线
		{Key: "tmp_disk_pct", MetricKey: MetricCapTmpDiskPct, Definition: "临时表落盘率，计算口径为 Created_tmp_disk_tables 除以 Created_tmp_tables 再乘 100，取值范围 0 ~ 100；偏高通常是排序/分组缺少合适索引，建议开启慢查询定位", Unit: "%", Order: 5, Scale: scaleNoop},
	}},
	// ---- 数据库指标扩展（12-15）：与告警模板默认规则对齐补入 ----
	{Key: "mysql.engine.availability", Tab: LayerEngine, Name: "采集与可用性", Order: 10, Series: []DisplaySeries{
		// avail.up 由采集器周期性 select 1 探测得出：1=可达，非 1=无法连接
		{Key: "avail_up", MetricKey: MetricAvailUp, Definition: "实例可达性，采集器周期性对实例执行 select 1 探测，1 表示可达，非 1 表示无法连接（网络不通、实例宕机或账号权限异常）", Unit: "-", Order: 1, Scale: scaleNoop},
		{Key: "collect_ms", MetricKey: MetricAvailCollectMs, Definition: "采集耗时，本次采集周期内完成该实例全部指标采集所消耗的时间（毫秒），持续偏高说明实例负载高或到实例的网络抖动", Unit: "ms", Order: 2, Scale: scaleNoop},
	}},
	{Key: "mysql.engine.long_trx", Tab: LayerEngine, Name: "长事务", Order: 11, Series: []DisplaySeries{
		{Key: "long_trx_count", MetricKey: MetricPerfLongTrx, Definition: "长事务数，统计对象为当前实例上已持续超过 60 秒仍未结束的事务（information_schema.innodb_trx）；长事务会阻碍 purge 并导致 undo 膨胀与锁堆积", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "mysql.engine.semisync", Tab: LayerEngine, Name: "半同步复制", Order: 12, Series: []DisplaySeries{
		{Key: "semisync_status", MetricKey: MetricEngSemiSyncStatus, Definition: "半同步复制状态，取值 1 表示半同步复制正常工作，0 表示已退化为异步复制（半同步超时或插件关闭）；未安装半同步插件的实例无数据", Unit: "-", Order: 1, Scale: scaleNoop},
	}},
	{Key: "mysql.engine.no_pk_tables", Tab: LayerEngine, Name: "无主键表", Order: 13, Series: []DisplaySeries{
		{Key: "no_pk_tables", MetricKey: MetricRiskNoPKTables, Definition: "无主键表数，统计对象为实例业务库中所有没有主键的表；无主键表影响复制性能与在线 DDL，建议补主键或唯一键", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},

	// ---------------- 引擎指标 ----------------
	{Key: "mysql.kernel.buffer_hit", Tab: LayerKernel, Name: "InnoDB Buffer Pool 命中率", Order: 1, Series: []DisplaySeries{
		{Key: "Innodb_buffer_dirty_ratio", MetricKey: MetricKernBufferDirtyPct, Definition: "InnoDB Buffer Pool 脏页比率；计算公式：Innodb_buffer_pool_pages_dirty / Innodb_buffer_pool_pages_data * 100%", Unit: "%", Order: 1, Scale: scaleNoop},
		{Key: "Innodb_buffer_read_hit", MetricKey: MetricPerfCacheHitRatio, Definition: "缓冲池读命中率", Unit: "%", Order: 2, Scale: scaleNoop},
		// 缓冲池利用率与既有的 cap.buffer_pool_used_pct 口径一致（已用页/总页），直接复用，不新增采集
		{Key: "Innodb_buffer_use_ratio", MetricKey: MetricCapBufferPoolPct, Definition: "缓冲池利用率", Unit: "%", Order: 3, Scale: scaleNoop},
	}},
	{Key: "mysql.kernel.buffer_dirty", Tab: LayerKernel, Name: "InnoDB Buffer Pool脏块", Order: 2, Series: []DisplaySeries{
		{Key: "Innodb_buffer_dirty", MetricKey: MetricKernBufferDirtyPages, Definition: "InnoDB Buffer Pool脏块数量", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "mysql.kernel.buffer_requests", Tab: LayerKernel, Name: "InnoDB Buffer Pool 请求速率", Order: 3, Series: []DisplaySeries{
		{Key: "Innodb_buffer_pool_read_requests", MetricKey: MetricKernBufferReadReq, Definition: "InnoDB 平均每秒从Buffer Pool读取页的次数（逻辑读）", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "Innodb_buffer_pool_write_requests", MetricKey: MetricKernBufferWriteReq, Definition: "InnoDB 平均每秒往Buffer Pool写入页的次数", Unit: "Count", Order: 2, Scale: scaleNoop},
	}},
	{Key: "mysql.kernel.innodb_data_io", Tab: LayerKernel, Name: "InnoDB Data 读写吞吐量", Order: 4, Series: []DisplaySeries{
		// 采集值单位是 B/s，附件口径为 KB/s → Scale 做确定性换算
		{Key: "Innodb_data_read", MetricKey: MetricKernDataReadRate, Definition: "InnoDB 平均每秒读字节数", Unit: "KB", Order: 1, Scale: scaleByteToKB},
		{Key: "Innodb_data_written", MetricKey: MetricKernDataWrittenRate, Definition: "InnoDB 平均每秒写字节数", Unit: "KB", Order: 2, Scale: scaleByteToKB},
	}},
	{Key: "mysql.kernel.row_lock", Tab: LayerKernel, Name: "Innodb行锁", Order: 5, Series: []DisplaySeries{
		{Key: "innodb_row_lock_current_waits", MetricKey: MetricPerfLocks, Definition: "InnoDB 表当前等待row locks次数", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "innodb_row_lock_time", MetricKey: MetricKernRowLockTimeMs, Definition: "从系统启动到现在锁定的总时间", Unit: "ms", Order: 2, Scale: scaleNoop},
		{Key: "innodb_row_lock_time_avg", MetricKey: MetricKernRowLockTimeAvgMs, Definition: "InnoDB 表平均等待row locks时间", Unit: "ms", Order: 3, Scale: scaleNoop},
		{Key: "innodb_row_lock_time_ps", MetricKey: MetricKernRowLockTimeRate, Definition: "InnoDB 表每秒等待row locks时间", Unit: "ms", Order: 4, Scale: scaleNoop},
		{Key: "innodb_row_lock_waits", MetricKey: MetricKernRowLockWaits, Definition: "InnoDB 表总等待row locks次数", Unit: "Count", Order: 5, Scale: scaleNoop},
		{Key: "innodb_row_lock_waits_ps", MetricKey: MetricKernRowLockWaitsRate, Definition: "InnoDB 表每秒等待row locks次数", Unit: "Count", Order: 6, Scale: scaleNoop},
	}},
	{Key: "mysql.kernel.pages_flushed", Tab: LayerKernel, Name: "InnoDB内存页", Order: 6, Series: []DisplaySeries{
		{Key: "Innodb_buffer_pool_pages_flushed", MetricKey: MetricKernFlushRate, Definition: "InnoDB Buffer Pool 刷Page请求数量", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "mysql.kernel.fsyncs", Tab: LayerKernel, Name: "刷盘次数", Order: 7, Series: []DisplaySeries{
		{Key: "Innodb_data_fsyncs", MetricKey: MetricKernDataFsyncsRate, Definition: "InnoDB 平均每秒fsync操作次数", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "mysql.kernel.sort_rows", Tab: LayerKernel, Name: "每秒排序行数", Order: 8, Series: []DisplaySeries{
		{Key: "sort_rows", MetricKey: MetricKernSortRowsRate, Definition: "每秒排序行数", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "mysql.kernel.row_ops", Tab: LayerKernel, Name: "InnoDB 每秒操作行数", Order: 9, Series: []DisplaySeries{
		{Key: "Innodb_rows_deleted", MetricKey: MetricKernRowsDeletedRate, Definition: "InnoDB 平均每秒删除的行数", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "Innodb_rows_inserted", MetricKey: MetricKernRowsInsertedRate, Definition: "InnoDB 平均每秒插入的行数", Unit: "Count", Order: 2, Scale: scaleNoop},
		{Key: "Innodb_rows_read", MetricKey: MetricPerfReadRows, Definition: "InnoDB 平均每秒读取的行数", Unit: "Count", Order: 3, Scale: scaleNoop},
		{Key: "Innodb_rows_updated", MetricKey: MetricKernRowsUpdatedRate, Definition: "InnoDB 平均每秒更新的行数", Unit: "Count", Order: 4, Scale: scaleNoop},
	}},
	{Key: "mysql.kernel.redo_bytes", Tab: LayerKernel, Name: "InnoDB Redo 写入量", Order: 10, Series: []DisplaySeries{
		{Key: "Innodb_os_log_written", MetricKey: MetricKernRedoWriteRate, Definition: "InnoDB每秒写入redo log日志量", Unit: "B/s", Order: 1, Scale: scaleNoop},
	}},
	{Key: "mysql.kernel.redo_writes", Tab: LayerKernel, Name: "InnoDB Redo 写次数", Order: 11, Series: []DisplaySeries{
		{Key: "Innodb_log_write_requests", MetricKey: MetricKernLogWriteReqRate, Definition: "每秒InnoDB日志写请求数", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "Innodb_log_writes", MetricKey: MetricKernLogWritesRate, Definition: "Innodb 平均每秒物理写Redo Log File次数", Unit: "Count", Order: 2, Scale: scaleNoop},
		{Key: "Innodb_os_log_fsyncs", MetricKey: MetricKernOsLogFsyncsRate, Definition: "平均每秒向日志文件完成的fsync()写数量", Unit: "Count", Order: 3, Scale: scaleNoop},
	}},
}

// postgresqlDisplayItems PostgreSQL 展示目录：基础指标（主机层）+ 数据库指标（引擎层）。
//
// 「基础指标」= 主机层指标（res.*），由 Prometheus + node_exporter 采集，
// 采集侧不区分数据库类型（见 collectResourceSamples：无 dbType 分支、无模板门控），
// 所以 PG 与 MySQL 共用同一份主机指标，这里必须与 MySQL 的「基础指标」保持一致 ——
// 此前 PG 该 Tab 为空，页面却提示"未获取到数据、请检查 Prometheus 配置"，
// 把"目录里没有监控项"误报成"采集失败"（实际 res.* 一直是 ok）。
//
// 唯一不照搬的是「网络流量」：MySQL 用的是引擎层 perf.write_bytes / perf.read_bytes
// （客户端视角的 Bytes_received / Bytes_sent），而 PG 没有对应的收发字节统计 ——
// 采集侧 perf.write_bytes 恒为 unsupported、perf.read_bytes 是磁盘块读字节（常为 0），
// 补进去只会得到一条恒空的曲线，比不展示更糟。等 PG 侧真有网络口径了再补。
var postgresqlDisplayItems = []DisplayItem{
	// ---------------- 基础指标 ----------------
	{Key: "pg.host.cpu_usage", Tab: LayerHost, Name: "CPU 使用率", Order: 1, Series: []DisplaySeries{
		{Key: "cpu_usage", MetricKey: MetricResCPU, Definition: "实例 CPU 使用率，统计对象为实例所在主机的 CPU（全部核心合计），计算口径为采集周期内非空闲时间占总 CPU 时间的比例，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "pg.host.memory_usage", Tab: LayerHost, Name: "内存使用率", Order: 2, Series: []DisplaySeries{
		{Key: "mem_usage", MetricKey: MetricResMemory, Definition: "实例内存使用率，统计对象为实例所在主机的内存，计算口径为已使用内存占操作系统总内存的比例，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "pg.host.disk_usage", Tab: LayerHost, Name: "磁盘使用率", Order: 3, Series: []DisplaySeries{
		{Key: "disk_usage", MetricKey: MetricResDisk, Definition: "磁盘使用率，统计对象为实例所在主机的全部真实磁盘分区（已排除 tmpfs/overlay 等伪文件系统与 /boot 启动分区），计算口径为这些分区中已用空间占比最高的那个分区，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	// 磁盘使用量（绝对容量）：与「磁盘使用率」同源（node_filesystem_*），
	// 已用 = size - avail，按 device 去重后求和，得到该主机所有真实磁盘分区的已用总量。
	{Key: "pg.host.disk_used", Tab: LayerHost, Name: "磁盘使用量", Order: 4, Series: []DisplaySeries{
		// 与 MySQL 侧保持一致：采集值为原始字节，由前端自动换算展示。
		{Key: "disk_used", MetricKey: MetricResDiskUsed, Definition: "磁盘使用量，统计对象为实例所在主机的全部真实磁盘分区（已排除 tmpfs/overlay 等伪文件系统与 /boot 启动分区，同一块盘重复挂载只计一次），计算口径为各分区总容量减去可用容量后的已用空间之和，取值范围 0 ~ 该主机磁盘总容量", Unit: "B", Order: 1, Scale: scaleNoop},
	}},
	// 主机网卡流量（同上：整机视角，与数据库协议层无关）
	{Key: "pg.host.net_traffic_host", Tab: LayerHost, Name: "主机网络流量", Order: 5, Series: []DisplaySeries{
		{Key: "net_in", MetricKey: MetricResNetworkIn, Definition: "主机网卡平均每秒接收字节数（已排除回环与容器虚拟网卡）", Unit: "B/s", Order: 1, Scale: scaleNoop},
		{Key: "net_out", MetricKey: MetricResNetworkOut, Definition: "主机网卡平均每秒发送字节数（已排除回环与容器虚拟网卡）", Unit: "B/s", Order: 2, Scale: scaleNoop},
	}},
	{Key: "pg.host.iops", Tab: LayerHost, Name: "IOPS", Order: 6, Series: []DisplaySeries{
		{Key: "data_io_bytes", MetricKey: MetricResDiskThroughput, Definition: "每秒读写吞吐量", Unit: "B/s", Order: 1, Scale: scaleNoop},
		{Key: "data_io_ps", MetricKey: MetricResDiskIOPS, Definition: "读写次数", Unit: "Count", Order: 2, Scale: scaleNoop},
	}},
	// ---- 主机层扩展（7-10）：res.* 由 Prometheus 统一采集、与数据库类型无关，
	// 与 MySQL「基础指标」保持同源同口径，同时覆盖告警模板默认规则的展示需求 ----
	{Key: "pg.host.io_latency", Tab: LayerHost, Name: "IO 与磁盘延迟", Order: 7, Series: []DisplaySeries{
		{Key: "io_usage", MetricKey: MetricResIO, Definition: "磁盘 IO 使用率，统计对象为实例所在主机的全部真实磁盘，计算口径为采集周期内磁盘处理 IO 请求的时间占比，取值范围 0 ~ 100，持续接近 100 说明磁盘趋于饱和", Unit: "%", Order: 1, Scale: scaleNoop},
		{Key: "disk_await", MetricKey: MetricResDiskAwaitMs, Definition: "磁盘平均 IO 延迟，统计对象为实例所在主机的全部真实磁盘，计算口径为单次读写操作的平均耗时（毫秒）；机械盘 >10ms、SSD >2ms 即需关注", Unit: "ms", Order: 2, Scale: scaleNoop},
	}},
	{Key: "pg.host.swap", Tab: LayerHost, Name: "Swap 使用率", Order: 8, Series: []DisplaySeries{
		{Key: "swap_used", MetricKey: MetricResSwapUsedPct, Definition: "Swap 使用率，统计对象为实例所在主机的操作系统交换分区，计算口径为已用 Swap 占 Swap 总量的比例，取值范围 0 ~ 100；数据库进程被换出会造成明显的延迟抖动", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "pg.host.net_errors", Tab: LayerHost, Name: "网络错误/丢包", Order: 9, Series: []DisplaySeries{
		{Key: "net_errors", MetricKey: MetricResNetErrors, Definition: "主机网卡每秒错误与丢包总数，统计对象为实例所在主机的物理网卡（已排除回环与容器虚拟网卡），计算口径为收发错误（errs）与丢包（drop）速率之和；丢包会放大跨机复制与连接超时", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "pg.host.proc_clock", Tab: LayerHost, Name: "进程与时钟", Order: 10, Series: []DisplaySeries{
		{Key: "fd_used", MetricKey: MetricResFDUsedPct, Definition: "文件句柄使用率，统计对象为实例所在主机的系统级文件句柄，计算口径为已分配句柄数占句柄上限的比例，取值范围 0 ~ 100；句柄耗尽会导致无法打开表文件甚至实例崩溃", Unit: "%", Order: 1, Scale: scaleNoop},
		{Key: "time_offset", MetricKey: MetricResTimeOffsetS, Definition: "主机时钟偏移，统计对象为实例所在主机与 NTP 源的时钟偏差，计算口径为时钟偏差绝对值的最大值（秒）；时钟漂移影响复制心跳、备份时间戳与证书校验", Unit: "s", Order: 2, Scale: scaleNoop},
	}},
	// ---------------- 数据库指标 ----------------
	//
	// 采集口径全部来自《PostgreSQL 监控指标》附件，适用版本 10 – 18。
	// 层级说明：除「数据库最大年龄」按库计算取最大值外，其余均为实例级
	// （pg_stat_database / pg_stat_activity 是实例全局视图，不带库维度拆分）。
	// 逻辑复制延迟：pg_current_wal_lsn() - confirmed_flush_lsn，只统计 slot_type='logical'
	{Key: "pg.engine.logical_rep_lag", Tab: LayerEngine, Name: "逻辑复制延迟", Order: 1, Series: []DisplaySeries{
		{Key: "logical_rep_lag_bytes", MetricKey: MetricEngLogicalRepLagBytes, Definition: "逻辑复制延迟，统计对象为实例上的逻辑复制槽，计算口径为当前 WAL 位点与订阅端已确认消费位点（confirmed_flush_lsn）之间的 WAL 字节差，取全部逻辑槽中的最大值", Unit: "B", Order: 1, Scale: scaleNoop},
	}},
	// 复制槽堆积：restart_lsn 是槽位要求保留的最早 WAL 位置，决定磁盘能否回收
	{Key: "pg.engine.repl_slot_lag", Tab: LayerEngine, Name: "复制槽堆积", Order: 2, Series: []DisplaySeries{
		{Key: "repl_slot_lag_bytes", MetricKey: MetricEngReplSlotLag, Definition: "复制槽堆积，统计对象为实例上的全部复制槽（含物理槽），计算口径为当前 WAL 位点与各槽位要求保留的最早位点（restart_lsn）之间的 WAL 字节差的最大值，堆积越大说明槽位阻塞 WAL 回收越严重", Unit: "B", Order: 1, Scale: scaleNoop},
	}},
	// 备库 APPLY 延迟：主库视角 pg_stat_replication.replay_lag（PG10+）
	{Key: "pg.engine.replay_lag", Tab: LayerEngine, Name: "备库 APPLY 延迟", Order: 3, Series: []DisplaySeries{
		{Key: "replay_lag_seconds", MetricKey: MetricEngReplayLagSeconds, Definition: "备库 APPLY 延迟，统计对象为主库上可见的全部流复制备库，计算口径为 WAL 被备库写入到被回放之间的时间间隔（pg_stat_replication.replay_lag）的最大值", Unit: "s", Order: 1, Scale: scaleNoop},
	}},
	// 发送延迟：已发往备库但尚未回放的 WAL 字节数
	{Key: "pg.engine.sent_lag", Tab: LayerEngine, Name: "备库发送延迟", Order: 4, Series: []DisplaySeries{
		{Key: "sent_lag_bytes", MetricKey: MetricEngSentLagBytes, Definition: "PostgreSQL 备库发送延迟，统计对象为主库上可见的全部流复制备库，计算口径为已发送位点（sent_lsn）与已回放位点（replay_lsn）之间的 WAL 字节差的最大值", Unit: "B", Order: 1, Scale: scaleNoop},
	}},
	// 复制延迟（备库视角）：告警模板「复制延迟 ≥300s」对应的展示曲线
	{Key: "pg.engine.repl_lag", Tab: LayerEngine, Name: "复制延迟", Order: 5, Series: []DisplaySeries{
		{Key: "repl_lag_seconds", MetricKey: MetricReplLagSeconds, Definition: "复制延迟（备库视角），统计对象为本机作为流复制备库时的回放进度，计算口径为最近回放事务时间（pg_last_xact_replay_timestamp）距当前的秒数；主库上无数据", Unit: "s", Order: 1, Scale: scaleNoop},
	}},
	// 卡片内容为「空闲事务（idle in transaction）1s / 3s / 5s」计数，故展示名为「空闲事务」；
	// 「长事务」另指 Order 16 的 pg.engine.long_trx（xact_start 超过 60s 仍未结束的事务）。
	{Key: "pg.engine.long_idle_trx", Tab: LayerEngine, Name: "空闲事务", Order: 6, Series: []DisplaySeries{
		{Key: "long_idle_in_transaction_5s", MetricKey: MetricEngIdleInTrx5s, Definition: "当前系统中，有多少长期（超过5秒）处于空闲的事务", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "long_idle_in_transaction_1s", MetricKey: MetricEngIdleInTrx1s, Definition: "当前系统中，有多少长期（超过1秒）处于空闲的事务", Unit: "Count", Order: 2, Scale: scaleNoop},
		{Key: "long_idle_in_transaction_3s", MetricKey: MetricEngIdleInTrx3s, Definition: "当前系统中，有多少长期（超过3秒）处于空闲的事务", Unit: "Count", Order: 3, Scale: scaleNoop},
	}},
	{Key: "pg.engine.long_query", Tab: LayerEngine, Name: "慢查询", Order: 7, Series: []DisplaySeries{
		// 注意：这是"当前正在执行且已超过阈值"的语句数（瞬时值），不是累计慢查询条数
		{Key: "long_query_5s", MetricKey: MetricEngLongQuery5s, Definition: "当前系统中执行时间超过5秒的SQL有多少条", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "long_query_1s", MetricKey: MetricEngLongQuery1s, Definition: "当前系统中执行时间超过1秒的SQL有多少条", Unit: "Count", Order: 2, Scale: scaleNoop},
		{Key: "long_query_3s", MetricKey: MetricEngLongQuery3s, Definition: "当前系统中执行时间超过3秒的SQL有多少条", Unit: "Count", Order: 3, Scale: scaleNoop},
	}},
	{Key: "pg.engine.swell_time", Tab: LayerEngine, Name: "膨胀点", Order: 8, Series: []DisplaySeries{
		{Key: "swell_time", MetricKey: MetricEngSwellTimeSeconds, Definition: "膨胀点，多久以前的垃圾可以被回收（反映了当前最长事务的持续时间，取最老活跃事务与最老未决 2PC 事务中更早的一个）", Unit: "s", Order: 1, Scale: scaleNoop},
	}},
	{Key: "pg.engine.xact_rate", Tab: LayerEngine, Name: "每秒事务数", Order: 9, Series: []DisplaySeries{
		{Key: "xact_commit", MetricKey: MetricEngXactCommitRate, Definition: "每秒事务提交数", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "deadlocks", MetricKey: MetricEngDeadlocks, Definition: "每秒死锁数", Unit: "Count", Order: 2, Scale: scaleNoop},
		{Key: "xact_rollback", MetricKey: MetricEngXactRollbackRate, Definition: "每秒事务回滚数", Unit: "Count", Order: 3, Scale: scaleNoop},
		{Key: "tps", MetricKey: MetricPerfTPS, Definition: "实例tps，提交数与回滚数之和的每秒变化率", Unit: "Count", Order: 4, Scale: scaleNoop},
	}},
	{Key: "pg.engine.tup_ops", Tab: LayerEngine, Name: "操作行数", Order: 10, Series: []DisplaySeries{
		{Key: "tup_deleted", MetricKey: MetricEngTupDeletedRate, Definition: "每秒删除记录数", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "tup_fetched", MetricKey: MetricEngTupFetchedRate, Definition: "每秒索引扫描回表记录数", Unit: "Count", Order: 2, Scale: scaleNoop},
		{Key: "tup_inserted", MetricKey: MetricEngTupInsertedRate, Definition: "每秒插入记录数", Unit: "Count", Order: 3, Scale: scaleNoop},
		{Key: "tup_returned", MetricKey: MetricEngTupReturnedRate, Definition: "每秒全表扫描记录数", Unit: "Count", Order: 4, Scale: scaleNoop},
		{Key: "tup_updated", MetricKey: MetricEngTupUpdatedRate, Definition: "每秒更新记录数", Unit: "Count", Order: 5, Scale: scaleNoop},
	}},
	{Key: "pg.engine.connections", Tab: LayerEngine, Name: "连接数", Order: 11, Series: []DisplaySeries{
		{Key: "active_session", MetricKey: MetricPerfActiveConn, Definition: "当前活跃连接数", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "total_session", MetricKey: MetricPerfConnCount, Definition: "当前连接总数", Unit: "Count", Order: 2, Scale: scaleNoop},
		{Key: "idle_in_transaction", MetricKey: MetricPerfIdleInTrxCount, Definition: "当前处于空闲事务状态的会话数", Unit: "Count", Order: 3, Scale: scaleNoop},
		// 附件口径是 wait_event IS NOT NULL，但那样会把 autovacuum launcher / checkpointer 等
		// 后台进程算进来（实测空闲实例上 6 个会话里 5 个"等待中"）；这里只统计客户端连接。
		{Key: "waiting_connection", MetricKey: MetricPerfWaitingConnCount, Definition: "当前处于等待状态的连接数，统计对象为客户端连接（已排除 autovacuum launcher、checkpointer 等后台进程），计算口径为 wait_event 非空的会话数", Unit: "Count", Order: 4, Scale: scaleNoop},
		// 连接容量水位：告警模板「活跃连接占比 / 连接使用率」对应的展示曲线
		{Key: "active_conn_pct", MetricKey: MetricCapConnUsedPct, Definition: "活跃连接占比，计算口径为活跃连接数除以最大连接数（max_connections）再乘 100，取值范围 0 ~ 100，接近 100 时新连接将被拒绝", Unit: "%", Order: 5, Scale: scaleNoop},
		{Key: "conn_thread_pct", MetricKey: MetricCapThreadUsedPct, Definition: "连接使用率，计算口径为当前连接总数除以最大连接数（max_connections）再乘 100，取值范围 0 ~ 100，持续偏高需评估调大 max_connections 或引入连接池", Unit: "%", Order: 6, Scale: scaleNoop},
	}},
	// 数据库最大年龄：按库计算 age(datfrozenxid) 后取最大值，接近 20 亿将触发只读保护
	{Key: "pg.engine.xid_age", Tab: LayerEngine, Name: "数据库最大年龄", Order: 12, Series: []DisplaySeries{
		{Key: "max_age", MetricKey: MetricRiskXIDAge, Definition: "数据库最大年龄，统计对象为实例上的全部数据库，计算口径为各库最老未冻结事务 ID（datfrozenxid）到当前已消耗 XID 数量的最大值，接近 20 亿时数据库将进入只读模式以防止 XID 回绕", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	// ---- 数据库指标扩展（9-13）：与告警模板默认规则对齐补入 ----
	{Key: "pg.engine.availability", Tab: LayerEngine, Name: "采集与可用性", Order: 13, Series: []DisplaySeries{
		// avail.up 由采集器周期性 select 1 探测得出：1=可达，非 1=无法连接
		{Key: "avail_up", MetricKey: MetricAvailUp, Definition: "实例可达性，采集器周期性对实例执行 select 1 探测，1 表示可达，非 1 表示无法连接（网络不通、实例宕机或账号权限异常）", Unit: "-", Order: 1, Scale: scaleNoop},
		{Key: "collect_ms", MetricKey: MetricAvailCollectMs, Definition: "采集耗时，本次采集周期内完成该实例全部指标采集所消耗的时间（毫秒），持续偏高说明实例负载高或到实例的网络抖动", Unit: "ms", Order: 2, Scale: scaleNoop},
	}},
	{Key: "pg.engine.cache_hit", Tab: LayerEngine, Name: "缓存命中率", Order: 14, Series: []DisplaySeries{
		{Key: "cache_hit_ratio", MetricKey: MetricPerfCacheHitRatio, Definition: "缓存命中率，统计对象为实例全部数据库的共享缓冲区，计算口径为 blks_hit 除以 blks_hit 与 blks_read 之和再乘 100，取值范围 0 ~ 100；命中率低说明读放大严重，需评估 shared_buffers 与索引覆盖", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "pg.engine.lock_waits", Tab: LayerEngine, Name: "锁等待", Order: 15, Series: []DisplaySeries{
		{Key: "locks_waiting", MetricKey: MetricPerfLocks, Definition: "锁等待数，统计对象为实例上当前未获得授权（granted=false）的锁请求，即正在排队等锁的连接数；定位阻塞源可用 pg_blocking_pids()", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "pg.engine.long_trx", Tab: LayerEngine, Name: "长事务", Order: 16, Series: []DisplaySeries{
		{Key: "long_trx_count", MetricKey: MetricPerfLongTrx, Definition: "长事务数，统计对象为当前实例上已持续超过 60 秒仍未结束的事务（pg_stat_activity.xact_start）；长事务会阻碍 autovacuum 回收死元组并导致膨胀", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "pg.engine.no_pk_tables", Tab: LayerEngine, Name: "无主键表", Order: 17, Series: []DisplaySeries{
		{Key: "no_pk_tables", MetricKey: MetricRiskNoPKTables, Definition: "无主键表数，统计对象为实例业务库中所有没有主键的表；无主键表影响逻辑复制与 UPSERT 语义，建议补主键或唯一键", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
}

// sqlserverDisplayItems SQL Server 展示目录：基础指标（主机层）+ 数据库指标（引擎层）两个 Tab。
//
// 「基础指标」与 PostgreSQL 逐项一致（10 个监控项 / 14 行曲线）：主机层 res.* 由
// Prometheus + node_exporter 采集，采集侧不区分数据库类型 —— 同一台机器上的 MySQL /
// PG / SQL Server 拿到的是同一份 res.* 数据，因此单位、定义、顺序必须一字不差，
// 否则同一个主机指标会在三种实例的明细页上出现三种读数。
//
// 唯一没有照搬 MySQL / Oracle 的是「数据库网络流量」：那两个类型用引擎层的
// perf.write_bytes / perf.read_bytes（Bytes_received / Bytes_sent、SQL*Net 计数），
// 而 SQL Server 的 dm_os_performance_counters 没有对应的客户端收发字节计数器，
// 补进去只会得到一条恒空的曲线。网络口径统一由主机层的「主机网络流量」承担。
//
// 数据库指标全部来自 sys.dm_os_performance_counters / sys.dm_exec_* /
// sys.dm_io_virtual_file_stats / sys.dm_os_wait_stats，适用版本 2012 起。
var sqlserverDisplayItems = []DisplayItem{
	// ---------------- 基础指标（Prometheus，node_exporter 口径） ----------------
	{Key: "sqlserver.host.cpu_usage", Tab: LayerHost, Name: "CPU 使用率", Order: 1, Series: []DisplaySeries{
		{Key: "cpu_usage", MetricKey: MetricResCPU, Definition: "实例 CPU 使用率，统计对象为实例所在主机的 CPU（全部核心合计），计算口径为采集周期内非空闲时间占总 CPU 时间的比例，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "sqlserver.host.memory_usage", Tab: LayerHost, Name: "内存使用率", Order: 2, Series: []DisplaySeries{
		{Key: "mem_usage", MetricKey: MetricResMemory, Definition: "实例内存使用率，统计对象为实例所在主机的内存，计算口径为已使用内存占操作系统总内存的比例，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "sqlserver.host.disk_usage", Tab: LayerHost, Name: "磁盘使用率", Order: 3, Series: []DisplaySeries{
		{Key: "disk_usage", MetricKey: MetricResDisk, Definition: "磁盘使用率，统计对象为实例所在主机的全部真实磁盘分区（已排除 tmpfs/overlay 等伪文件系统与 /boot 启动分区），计算口径为这些分区中已用空间占比最高的那个分区，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	// 磁盘使用量（绝对容量）：与「磁盘使用率」同源（node_filesystem_*），
	// 已用 = size - avail，按 device 去重后求和，得到该主机所有真实磁盘分区的已用总量。
	{Key: "sqlserver.host.disk_used", Tab: LayerHost, Name: "磁盘使用量", Order: 4, Series: []DisplaySeries{
		// 与 MySQL / PG 侧保持一致：采集值为原始字节，由前端自动换算展示。
		{Key: "disk_used", MetricKey: MetricResDiskUsed, Definition: "磁盘使用量，统计对象为实例所在主机的全部真实磁盘分区（已排除 tmpfs/overlay 等伪文件系统与 /boot 启动分区，同一块盘重复挂载只计一次），计算口径为各分区总容量减去可用容量后的已用空间之和，取值范围 0 ~ 该主机磁盘总容量", Unit: "B", Order: 1, Scale: scaleNoop},
	}},
	// 主机网卡流量：整机视角，含 scp / SSH / 应用外网等一切经过物理网卡的流量。
	{Key: "sqlserver.host.net_traffic_host", Tab: LayerHost, Name: "主机网络流量", Order: 5, Series: []DisplaySeries{
		{Key: "net_in", MetricKey: MetricResNetworkIn, Definition: "主机网卡平均每秒接收字节数（已排除回环与容器虚拟网卡）", Unit: "B/s", Order: 1, Scale: scaleNoop},
		{Key: "net_out", MetricKey: MetricResNetworkOut, Definition: "主机网卡平均每秒发送字节数（已排除回环与容器虚拟网卡）", Unit: "B/s", Order: 2, Scale: scaleNoop},
	}},
	{Key: "sqlserver.host.iops", Tab: LayerHost, Name: "IOPS", Order: 6, Series: []DisplaySeries{
		{Key: "data_io_bytes", MetricKey: MetricResDiskThroughput, Definition: "每秒读写吞吐量", Unit: "B/s", Order: 1, Scale: scaleNoop},
		{Key: "data_io_ps", MetricKey: MetricResDiskIOPS, Definition: "读写次数", Unit: "Count", Order: 2, Scale: scaleNoop},
	}},
	{Key: "sqlserver.host.io_latency", Tab: LayerHost, Name: "IO 与磁盘延迟", Order: 7, Series: []DisplaySeries{
		{Key: "io_usage", MetricKey: MetricResIO, Definition: "磁盘 IO 使用率，统计对象为实例所在主机的全部真实磁盘，计算口径为采集周期内磁盘处理 IO 请求的时间占比，取值范围 0 ~ 100，持续接近 100 说明磁盘趋于饱和", Unit: "%", Order: 1, Scale: scaleNoop},
		{Key: "disk_await", MetricKey: MetricResDiskAwaitMs, Definition: "磁盘平均 IO 延迟，统计对象为实例所在主机的全部真实磁盘，计算口径为单次读写操作的平均耗时（毫秒）；机械盘 >10ms、SSD >2ms 即需关注", Unit: "ms", Order: 2, Scale: scaleNoop},
	}},
	{Key: "sqlserver.host.swap", Tab: LayerHost, Name: "Swap 使用率", Order: 8, Series: []DisplaySeries{
		{Key: "swap_used", MetricKey: MetricResSwapUsedPct, Definition: "Swap 使用率，统计对象为实例所在主机的操作系统交换分区，计算口径为已用 Swap 占 Swap 总量的比例，取值范围 0 ~ 100；数据库进程被换出会造成明显的延迟抖动", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "sqlserver.host.net_errors", Tab: LayerHost, Name: "网络错误/丢包", Order: 9, Series: []DisplaySeries{
		{Key: "net_errors", MetricKey: MetricResNetErrors, Definition: "主机网卡每秒错误与丢包总数，统计对象为实例所在主机的物理网卡（已排除回环与容器虚拟网卡），计算口径为收发错误（errs）与丢包（drop）速率之和；丢包会放大 AlwaysOn 传输与连接超时", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "sqlserver.host.proc_clock", Tab: LayerHost, Name: "进程与时钟", Order: 10, Series: []DisplaySeries{
		{Key: "fd_used", MetricKey: MetricResFDUsedPct, Definition: "文件句柄使用率，统计对象为实例所在主机的系统级文件句柄，计算口径为已分配句柄数占句柄上限的比例，取值范围 0 ~ 100；句柄耗尽会导致无法打开数据文件甚至实例崩溃", Unit: "%", Order: 1, Scale: scaleNoop},
		{Key: "time_offset", MetricKey: MetricResTimeOffsetS, Definition: "主机时钟偏移，统计对象为实例所在主机与 NTP 源的时钟偏差，计算口径为时钟偏差绝对值的最大值（秒）；时钟漂移影响 AlwaysOn 心跳、备份时间戳与证书校验", Unit: "s", Order: 2, Scale: scaleNoop},
	}},
	// ---------------- 数据库指标（sys.dm_* 直连采集） ----------------
	{Key: "sqlserver.engine.availability", Tab: LayerEngine, Name: "可用性", Order: 1, Series: []DisplaySeries{
		{Key: "avail_up", MetricKey: MetricAvailUp, Definition: "实例可达性，采集器周期性对实例执行 select 1 探测，1 表示可达，非 1 表示无法连接（网络不通、实例宕机或账号权限异常）", Unit: "-", Order: 1, Scale: scaleNoop},
		{Key: "collect_ms", MetricKey: MetricAvailCollectMs, Definition: "采集耗时，本次采集周期内完成该实例全部指标采集所消耗的时间（毫秒），持续偏高说明实例负载高或到实例的网络抖动", Unit: "ms", Order: 2, Scale: scaleNoop},
	}},
	// 吞吐：SQL Server 的主口径是「批处理请求速率」而非 QPS —— 一个批处理可以包含多条语句，
	// 官方与主流监控口径都以 Batch Requests/sec 计，这里沿用官方命名，不折算成 QPS。
	{Key: "sqlserver.engine.throughput", Tab: LayerEngine, Name: "吞吐", Order: 2, Series: []DisplaySeries{
		{Key: "batch_req", MetricKey: MetricEngBatchReqRate, Definition: "每秒批处理请求数，来源 sys.dm_os_performance_counters 的 SQL Statistics / Batch Requests/sec，计算口径为累计值的每秒增量；SQL Server 的吞吐主口径", Unit: "req/s", Order: 1, Scale: scaleNoop},
		{Key: "tps", MetricKey: MetricPerfTPS, Definition: "每秒事务数，来源 Databases / Transactions/sec（_Total）的每秒增量", Unit: "txn/s", Order: 2, Scale: scaleNoop},
		{Key: "sql_compilation", MetricKey: MetricEngSQLCompRate, Definition: "每秒 SQL 编译次数，来源 SQL Statistics / SQL Compilations/sec；持续高位说明语句未参数化", Unit: "Count/s", Order: 3, Scale: scaleNoop},
		{Key: "sql_recompilation", MetricKey: MetricEngSQLReCompRate, Definition: "每秒 SQL 重编译次数，来源 SQL Statistics / SQL Re-Compilations/sec；抬升常见于统计信息变更或临时表", Unit: "Count/s", Order: 4, Scale: scaleNoop},
		{Key: "logins", MetricKey: MetricEngLoginsRate, Definition: "每秒登录次数，来源 General Statistics / Logins/sec；突增通常意味着连接池失效或应用反复重连", Unit: "Count/s", Order: 5, Scale: scaleNoop},
	}},
	{Key: "sqlserver.engine.connections", Tab: LayerEngine, Name: "连接与会话", Order: 3, Series: []DisplaySeries{
		{Key: "total_session", MetricKey: MetricPerfConnCount, Definition: "当前用户会话总数，统计对象为 sys.dm_exec_sessions 中 is_user_process=1 的会话", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "active_session", MetricKey: MetricPerfActiveConn, Definition: "当前活跃会话数，统计对象为 is_user_process=1 且 status='running' 的会话，即正在执行请求的会话", Unit: "Count", Order: 2, Scale: scaleNoop},
		{Key: "waiting_connection", MetricKey: MetricPerfWaitingConnCount, Definition: "当前处于等待状态的会话数，统计对象为 sys.dm_exec_requests 中 wait_type 非空的请求（已排除后台任务），持续不为 0 说明存在锁或 IO 瓶颈", Unit: "Count", Order: 3, Scale: scaleNoop},
		{Key: "max_connections", MetricKey: MetricPerfMaxConn, Definition: "最大连接数，来源 sys.configurations 的 user connections 配置；配置为 0（不限制）时按默认值 32767 上报", Unit: "Count", Order: 4, Scale: scaleNoop},
	}},
	{Key: "sqlserver.engine.conn_usage", Tab: LayerEngine, Name: "连接使用率", Order: 4, Series: []DisplaySeries{
		{Key: "conn_thread_used_pct", MetricKey: MetricCapThreadUsedPct, Definition: "连接使用率，计算口径为用户会话总数除以最大连接数再乘 100，取值范围 0 ~ 100；持续偏高需评估调大 user connections 或引入连接池", Unit: "%", Order: 1, Scale: scaleNoop},
		{Key: "active_conn_ratio", MetricKey: MetricCapConnUsedPct, Definition: "活跃连接占比，计算口径为活跃会话数除以最大连接数再乘 100，取值范围 0 ~ 100", Unit: "%", Order: 2, Scale: scaleNoop},
	}},
	{Key: "sqlserver.engine.locks", Tab: LayerEngine, Name: "锁与阻塞", Order: 5, Series: []DisplaySeries{
		{Key: "blocked_sessions", MetricKey: MetricPerfLocks, Definition: "阻塞会话数，统计对象为 sys.dm_exec_requests 中 blocking_session_id 非 0 的请求，即正被其他会话阻塞的请求；持续不为 0 需沿阻塞链定位源头", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "lock_wait_rate", MetricKey: MetricEngLockWaitRate, Definition: "每秒锁等待次数，来源 Locks / Lock Waits/sec（_Total）", Unit: "Count/s", Order: 2, Scale: scaleNoop},
		{Key: "lock_timeout_rate", MetricKey: MetricEngLockTimeoutRate, Definition: "每秒锁超时次数，来源 Locks / Lock Timeouts/sec（_Total）；锁超时会让业务语句直接失败", Unit: "Count/s", Order: 3, Scale: scaleNoop},
		{Key: "lock_avg_wait_ms", MetricKey: MetricEngLockAvgWaitMs, Definition: "平均锁等待时长，来源 Locks / Average Wait Time (ms)（_Total）；超过 100ms 即会明显影响事务响应", Unit: "ms", Order: 4, Scale: scaleNoop},
		{Key: "deadlocks", MetricKey: MetricEngDeadlocks, Definition: "每秒死锁数，来源 Locks / Number of Deadlocks/sec（_Total）；死锁本身会被自动回滚，频发说明事务并发模型或索引设计有问题", Unit: "Count/s", Order: 5, Scale: scaleNoop},
		{Key: "long_trx", MetricKey: MetricPerfLongTrx, Definition: "长事务数，统计对象为 sys.dm_tran_active_transactions 中开启超过 60 秒仍未提交的事务；长事务会放大日志与版本存储并阻塞日志截断", Unit: "Count", Order: 6, Scale: scaleNoop},
	}},
	{Key: "sqlserver.engine.cache_hit", Tab: LayerEngine, Name: "缓存命中率", Order: 6, Series: []DisplaySeries{
		{Key: "cache_hit_ratio", MetricKey: MetricPerfCacheHitRatio, Definition: "缓冲池命中率，来源 Buffer Manager / Buffer cache hit ratio（按 cntr_value 与 base 之比计算），取值范围 0 ~ 100；命中率低说明物理读放大，需评估内存与索引覆盖", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "sqlserver.engine.ple", Tab: LayerEngine, Name: "Page Life Expectancy", Order: 7, Series: []DisplaySeries{
		{Key: "page_life_expectancy", MetricKey: MetricEngPageLifeExpectancy, Definition: "页生命周期，来源 Buffer Manager / Page life expectancy，即数据页在缓冲池中的平均驻留秒数；持续低于 300 秒说明缓冲池被反复换出，是内存压力最直接的证据", Unit: "s", Order: 1, Scale: scaleNoop},
	}},
	{Key: "sqlserver.engine.cpu_memory", Tab: LayerEngine, Name: "数据库 CPU 与内存", Order: 8, Series: []DisplaySeries{
		{Key: "sql_cpu_util", MetricKey: MetricEngSQLCPUPct, Definition: "SQL Server 进程自身的 CPU 占用率，来源 sys.dm_os_ring_buffers 的 RING_BUFFER_SCHEDULER_MONITOR（ProcessUtilization），取值范围 0 ~ 100；与主机 CPU 使用率对比可区分是数据库还是同机其他进程在耗算力", Unit: "%", Order: 1, Scale: scaleNoop},
		{Key: "sql_memory_used", MetricKey: MetricEngSQLMemoryBytes, Definition: "SQL Server 实际占用的内存，来源 Memory Manager / Total Server Memory (KB)（含缓冲池与计划缓存）", Unit: "B", Order: 2, Scale: scaleNoop},
	}},
	{Key: "sqlserver.engine.mem_grant", Tab: LayerEngine, Name: "内存授予等待", Order: 9, Series: []DisplaySeries{
		{Key: "memory_grants_pending", MetricKey: MetricEngMemGrantsPending, Definition: "等待内存授予的查询数，来源 Memory Manager / Memory Grants Pending；不为 0 说明有大排序/哈希在排队，通常与索引缺失或统计信息过期相关", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	// 事务日志：刷新吞吐与等待成对出现，用于区分「日志写得多」与「日志写不动」
	{Key: "sqlserver.engine.log_flush", Tab: LayerEngine, Name: "日志刷新", Order: 10, Series: []DisplaySeries{
		{Key: "log_flush_bytes", MetricKey: MetricEngLogFlushBytesRate, Definition: "每秒写入事务日志的字节数，来源各库 Log Bytes Flushed/sec 求和后的每秒增量；决定事务提交成本与 AlwaysOn 传输压力", Unit: "B/s", Order: 1, Scale: scaleNoop},
		{Key: "log_flush_wait_rate", MetricKey: MetricEngLogFlushWaitRate, Definition: "每秒日志刷新等待次数，来源各库 Log Flush Waits/sec 求和；抬升说明日志磁盘写不动，需检查日志所在存储的写延迟", Unit: "Count/s", Order: 2, Scale: scaleNoop},
	}},
	{Key: "sqlserver.engine.log_growth", Tab: LayerEngine, Name: "日志增长", Order: 11, Series: []DisplaySeries{
		{Key: "log_growths", MetricKey: MetricEngLogGrowths, Definition: "每秒日志自动增长次数，来源各库 Log Growths 求和后的每秒增量；自动增长会阻塞写入并产生 VLF 碎片，应改为预分配", Unit: "Count/s", Order: 1, Scale: scaleNoop},
	}},
	{Key: "sqlserver.engine.log_used", Tab: LayerEngine, Name: "日志文件使用率", Order: 12, Series: []DisplaySeries{
		{Key: "log_used_pct", MetricKey: MetricEngLogUsedPct, Definition: "日志文件使用率，来源各库 Percent Log Used 的最大值，取值范围 0 ~ 100；写满会阻塞全部写入（错误 9002），需确认日志备份是否正常", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "sqlserver.engine.file_size", Tab: LayerEngine, Name: "数据与日志文件大小", Order: 13, Series: []DisplaySeries{
		{Key: "data_file_size", MetricKey: MetricEngDataFileBytes, Definition: "实例全部数据文件的总大小（不含日志），来源 Databases / Data File(s) Size (KB)（_Total）", Unit: "B", Order: 1, Scale: scaleNoop},
		{Key: "log_file_size", MetricKey: MetricEngLogFileBytes, Definition: "实例全部事务日志文件的总大小，来源 Databases / Log File(s) Size (KB)（_Total）", Unit: "B", Order: 2, Scale: scaleNoop},
	}},
	{Key: "sqlserver.engine.tempdb", Tab: LayerEngine, Name: "TempDB 使用", Order: 14, Series: []DisplaySeries{
		{Key: "tempdb_used", MetricKey: MetricEngTempdbUsedBytes, Definition: "TempDB 已用空间，计算口径为 tempdb 数据文件总大小减去 Free Space in tempdb；被临时对象、排序哈希与版本存储共同消耗，打满会导致整实例写入失败", Unit: "B", Order: 1, Scale: scaleNoop},
		{Key: "tempdb_version_store", MetricKey: MetricEngTempdbVersionBytes, Definition: "版本存储占用，来源 Transactions / Version Store Size (KB)；由快照隔离与可读副本驱动，长期不回落说明存在未提交的长事务", Unit: "B", Order: 2, Scale: scaleNoop},
	}},
	{Key: "sqlserver.engine.io_throughput", Tab: LayerEngine, Name: "数据文件读写吞吐", Order: 15, Series: []DisplaySeries{
		{Key: "io_read_bytes", MetricKey: MetricEngIOReadBytesRate, Definition: "每秒从数据文件读取的字节数，来源 sys.dm_io_virtual_file_stats 的 num_of_bytes_read 增量；物理读高说明缓冲池命中不足", Unit: "B/s", Order: 1, Scale: scaleNoop},
		{Key: "io_write_bytes", MetricKey: MetricEngIOWriteBytesRate, Definition: "每秒写入数据文件的字节数，来源 sys.dm_io_virtual_file_stats 的 num_of_bytes_written 增量；主要来自检查点与惰性写", Unit: "B/s", Order: 2, Scale: scaleNoop},
	}},
	{Key: "sqlserver.engine.io_latency", Tab: LayerEngine, Name: "数据文件 IO 延迟", Order: 16, Series: []DisplaySeries{
		{Key: "io_read_latency", MetricKey: MetricEngIOReadLatencyMs, Definition: "单次读的平均等待时长，计算口径为 io_stall_read_ms 增量除以 num_of_reads 增量（sys.dm_io_virtual_file_stats）；SSD 超过 5ms、机械盘超过 20ms 即需关注", Unit: "ms", Order: 1, Scale: scaleNoop},
		{Key: "io_write_latency", MetricKey: MetricEngIOWriteLatencyMs, Definition: "单次写的平均等待时长，计算口径为 io_stall_write_ms 增量除以 num_of_writes 增量（sys.dm_io_virtual_file_stats）；写延迟高会拖慢提交与检查点", Unit: "ms", Order: 2, Scale: scaleNoop},
	}},
	{Key: "sqlserver.engine.wait_stats", Tab: LayerEngine, Name: "等待统计", Order: 17, Series: []DisplaySeries{
		{Key: "wait_time_ms_rate", MetricKey: MetricEngWaitTimeMsRate, Definition: "每秒累积的等待毫秒数，来源 sys.dm_os_wait_stats 的 wait_time_ms 增量（已排除空闲类等待）；刻画实例整体等待压力，用于发现「吞吐上不去但没有明显慢查询」的隐性瓶颈", Unit: "ms/s", Order: 1, Scale: scaleNoop},
	}},
}

// displayLayoutDef 一个数据库类型的 Tab + 监控项定义。
type displayLayoutDef struct {
	Tabs  []DisplayTab
	Items []DisplayItem
}

// oracleDisplayItems Oracle 展示目录：基础指标（主机层）+ 数据库指标两个 Tab。
//
// 基础指标与 MySQL / PostgreSQL 逐项一致（11 个监控项 / 16 行曲线）：
// 主机层由 Prometheus + node_exporter 采集，与数据库类型无关 —— 同一台机器上的
// MySQL / PG / Oracle 拿到的是同一份 res.* 数据。此前 Oracle 只列了 CPU / 内存 /
// 磁盘三项，其余主机指标（磁盘使用量、IOPS、IO 延迟、Swap、网络错误、文件句柄、
// 时钟偏移）一直在采却无处展示，模板里也无从勾选；运维在告警规则里能看到这些
// 指标名，明细页却查不到曲线。补齐后三个类型的主机口径完全对齐。
//
// 与 MySQL 的差异仅在「数据库网络流量」的位置：MySQL 放在基础指标 Tab（与主机
// 流量并列对比），Oracle 同样并入基础指标，避免同一对 perf.* 键在两个 Tab 各画
// 一张卡片。数据库指标全部来自 v$sysstat / v$session / v$resource_limit / dba_*
// 直连采集；Oracle 没有同语义计数器的指标（如 Com_* 语句计数）不进目录，
// 避免整页「—」。
var oracleDisplayItems = []DisplayItem{
	// ---------------- 基础指标（Prometheus，node_exporter 口径） ----------------
	{Key: "oracle.host.cpu_usage", Tab: LayerHost, Name: "CPU 使用率", Order: 1, Series: []DisplaySeries{
		{Key: "cpu_usage", MetricKey: MetricResCPU, Definition: "实例 CPU 使用率，统计对象为实例所在主机的 CPU（全部核心合计），计算口径为采集周期内非空闲时间占总 CPU 时间的比例，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "oracle.host.memory_usage", Tab: LayerHost, Name: "内存使用率", Order: 2, Series: []DisplaySeries{
		{Key: "mem_usage", MetricKey: MetricResMemory, Definition: "实例内存使用率，统计对象为实例所在主机的内存，计算口径为已使用内存占操作系统总内存的比例，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "oracle.host.disk_usage", Tab: LayerHost, Name: "磁盘使用率", Order: 3, Series: []DisplaySeries{
		{Key: "disk_usage", MetricKey: MetricResDisk, Definition: "磁盘使用率，统计对象为实例所在主机的全部真实磁盘分区（已排除 tmpfs/overlay 等伪文件系统与 /boot 启动分区），计算口径为这些分区中已用空间占比最高的那个分区，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	// 磁盘使用量（绝对容量）：与「磁盘使用率」同源（node_filesystem_*），
	// 已用 = size - avail，按 device 去重后求和，得到该主机所有真实磁盘分区的已用总量。
	{Key: "oracle.host.disk_used", Tab: LayerHost, Name: "磁盘使用量", Order: 4, Series: []DisplaySeries{
		// Unit / Scale 与采集口径一致：采集值是原始字节（B），由前端按 unitDim('B') 自动换算。
		{Key: "disk_used", MetricKey: MetricResDiskUsed, Definition: "磁盘使用量，统计对象为实例所在主机的全部真实磁盘分区（已排除 tmpfs/overlay 等伪文件系统与 /boot 启动分区，同一块盘重复挂载只计一次），计算口径为各分区总容量减去可用容量后的已用空间之和，取值范围 0 ~ 该主机磁盘总容量", Unit: "B", Order: 1, Scale: scaleNoop},
	}},
	// 数据库网络流量：客户端视角的收发字节速率（v$sysstat 的 SQL*Net 计数），
	// 与下方「主机网络流量」统计对象不同：走 RMAN / 客户端都计，scp 与实例无关的流量不计。
	{Key: "oracle.host.network_traffic", Tab: LayerHost, Name: "数据库网络流量", Order: 5, Series: []DisplaySeries{
		{Key: "input_traffic", MetricKey: MetricPerfWriteBytes, Definition: "平均每秒从所有客户端接收到的字节数，来源 v$sysstat bytes received via SQL*Net from client；与主机网卡流量口径不同", Unit: "KB", Order: 1, Scale: scaleByteToKB},
		{Key: "output_traffic", MetricKey: MetricPerfReadBytes, Definition: "平均每秒发送给所有客户端的字节数，来源 v$sysstat bytes sent via SQL*Net to client；与主机网卡流量口径不同", Unit: "KB", Order: 2, Scale: scaleByteToKB},
	}},
	// 主机网卡流量：整机视角，含 scp / SSH / 应用外网等一切经过物理网卡的流量。
	{Key: "oracle.host.net_traffic_host", Tab: LayerHost, Name: "主机网络流量", Order: 6, Series: []DisplaySeries{
		{Key: "net_in", MetricKey: MetricResNetworkIn, Definition: "主机网卡平均每秒接收字节数（已排除回环与容器虚拟网卡）", Unit: "B/s", Order: 1, Scale: scaleNoop},
		{Key: "net_out", MetricKey: MetricResNetworkOut, Definition: "主机网卡平均每秒发送字节数（已排除回环与容器虚拟网卡）", Unit: "B/s", Order: 2, Scale: scaleNoop},
	}},
	{Key: "oracle.host.iops", Tab: LayerHost, Name: "IOPS", Order: 7, Series: []DisplaySeries{
		{Key: "data_io_bytes", MetricKey: MetricResDiskThroughput, Definition: "每秒读写吞吐量", Unit: "B/s", Order: 1, Scale: scaleNoop},
		{Key: "data_io_ps", MetricKey: MetricResDiskIOPS, Definition: "读写次数", Unit: "Count", Order: 2, Scale: scaleNoop},
	}},
	{Key: "oracle.host.io_latency", Tab: LayerHost, Name: "IO 与磁盘延迟", Order: 8, Series: []DisplaySeries{
		{Key: "io_usage", MetricKey: MetricResIO, Definition: "磁盘 IO 使用率，统计对象为实例所在主机的全部真实磁盘，计算口径为采集周期内磁盘处理 IO 请求的时间占比，取值范围 0 ~ 100，持续接近 100 说明磁盘趋于饱和", Unit: "%", Order: 1, Scale: scaleNoop},
		{Key: "disk_await", MetricKey: MetricResDiskAwaitMs, Definition: "磁盘平均 IO 延迟，统计对象为实例所在主机的全部真实磁盘，计算口径为单次读写操作的平均耗时（毫秒）；机械盘 >10ms、SSD >2ms 即需关注", Unit: "ms", Order: 2, Scale: scaleNoop},
	}},
	{Key: "oracle.host.swap", Tab: LayerHost, Name: "Swap 使用率", Order: 9, Series: []DisplaySeries{
		{Key: "swap_used", MetricKey: MetricResSwapUsedPct, Definition: "Swap 使用率，统计对象为实例所在主机的操作系统交换分区，计算口径为已用 Swap 占 Swap 总量的比例，取值范围 0 ~ 100；数据库进程被换出会造成明显的延迟抖动", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "oracle.host.net_errors", Tab: LayerHost, Name: "网络错误/丢包", Order: 10, Series: []DisplaySeries{
		{Key: "net_errors", MetricKey: MetricResNetErrors, Definition: "主机网卡每秒错误与丢包总数，统计对象为实例所在主机的物理网卡（已排除回环与容器虚拟网卡），计算口径为收发错误（errs）与丢包（drop）速率之和；丢包会放大 Data Guard 传输与连接超时", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "oracle.host.proc_clock", Tab: LayerHost, Name: "进程与时钟", Order: 11, Series: []DisplaySeries{
		{Key: "fd_used", MetricKey: MetricResFDUsedPct, Definition: "文件句柄使用率，统计对象为实例所在主机的系统级文件句柄，计算口径为已分配句柄数占句柄上限的比例，取值范围 0 ~ 100；句柄耗尽会导致无法打开数据文件甚至实例崩溃", Unit: "%", Order: 1, Scale: scaleNoop},
		{Key: "time_offset", MetricKey: MetricResTimeOffsetS, Definition: "主机时钟偏移，统计对象为实例所在主机与 NTP 源的时钟偏差，计算口径为时钟偏差绝对值的最大值（秒）；时钟漂移影响 Data Guard 心跳、备份时间戳与证书校验", Unit: "s", Order: 2, Scale: scaleNoop},
	}},
	// ---------------- 数据库指标（v$ / dba_* 直连采集） ----------------
	{Key: "oracle.engine.availability", Tab: LayerEngine, Name: "可用性", Order: 1, Series: []DisplaySeries{
		{Key: "avail_up", MetricKey: MetricAvailUp, Definition: "实例可达性，采集器周期性对实例执行连通探测，1 表示可达，非 1 表示无法连接（网络不通、实例宕机或账号权限异常）", Unit: "-", Order: 1, Scale: scaleNoop},
		{Key: "collect_ms", MetricKey: MetricAvailCollectMs, Definition: "采集耗时，本次采集周期内完成该实例全部指标采集所消耗的时间（毫秒），持续偏高说明实例负载高或到实例的网络抖动", Unit: "ms", Order: 2, Scale: scaleNoop},
	}},
	{Key: "oracle.engine.qps_tps", Tab: LayerEngine, Name: "吞吐", Order: 2, Series: []DisplaySeries{
		{Key: "qps", MetricKey: MetricPerfQPS, Definition: "每秒执行语句数，v$sysstat execute count 的每秒增量", Unit: "req/s", Order: 1, Scale: scaleNoop},
		{Key: "tps", MetricKey: MetricPerfTPS, Definition: "每秒事务数，v$sysstat user commits 与 user rollbacks 之和的每秒增量", Unit: "txn/s", Order: 2, Scale: scaleNoop},
	}},
	{Key: "oracle.engine.connections", Tab: LayerEngine, Name: "连接与进程", Order: 3, Series: []DisplaySeries{
		{Key: "connections", MetricKey: MetricPerfConnCount, Definition: "当前用户会话总数，统计对象为 v$session 中 type='USER' 的会话", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "active_connections", MetricKey: MetricPerfActiveConn, Definition: "当前处于 ACTIVE 状态的用户会话数，即正在执行 SQL 或事务的会话", Unit: "Count", Order: 2, Scale: scaleNoop},
		{Key: "process_used", MetricKey: MetricCapProcessUsedPct, Definition: "进程数使用率，统计对象为 v$process 会话数占 processes 参数上限的比例，取值范围 0 ~ 100；打满后新建会话报 ORA-00020", Unit: "%", Order: 3, Scale: scaleNoop},
	}},
	{Key: "oracle.engine.conn_usage", Tab: LayerEngine, Name: "连接使用率", Order: 4, Series: []DisplaySeries{
		{Key: "conn_thread_used_pct", MetricKey: MetricCapThreadUsedPct, Definition: "连接使用率，统计对象为用户会话总数占 sessions 参数上限的比例，取值范围 0 ~ 100", Unit: "%", Order: 1, Scale: scaleNoop},
		{Key: "active_conn_ratio", MetricKey: MetricCapConnUsedPct, Definition: "活跃连接占比，统计对象为 ACTIVE 会话数占 sessions 参数上限的比例，取值范围 0 ~ 100", Unit: "%", Order: 2, Scale: scaleNoop},
	}},
	{Key: "oracle.engine.cache_hit", Tab: LayerEngine, Name: "缓存命中率", Order: 5, Series: []DisplaySeries{
		{Key: "cache_hit_ratio", MetricKey: MetricPerfCacheHitRatio, Definition: "Buffer Cache 命中率，统计对象为实例全部数据块访问，计算口径为 1 减去 physical reads 与逻辑读（db block gets + consistent gets）之比再乘 100，取值范围 0 ~ 100；低于 90% 通常意味着物理读放大", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	// 「数据库网络流量」已并入基础指标 Tab（与 MySQL 一致：协议层流量与主机网卡流量
	// 相邻对比），此处不再重复建卡，避免同一对 perf.* 键在两处各画一遍。
	{Key: "oracle.engine.locks_trx", Tab: LayerEngine, Name: "锁与事务", Order: 6, Series: []DisplaySeries{
		{Key: "locks", MetricKey: MetricPerfLocks, Definition: "锁等待数，统计对象为 v$session 中 blocking_session 非空的会话数，即正被其他会话阻塞的会话；持续不为 0 需沿阻塞链定位源头", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "long_trx", MetricKey: MetricPerfLongTrx, Definition: "长事务数，统计对象为挂着活跃事务（taddr 非空）且超过 60 秒未活动的用户会话；长事务会放大 undo 占用并阻碍回滚段回收", Unit: "Count", Order: 2, Scale: scaleNoop},
	}},
	{Key: "oracle.engine.redo", Tab: LayerEngine, Name: "Redo 写入速率", Order: 7, Series: []DisplaySeries{
		{Key: "redo_write", MetricKey: MetricKernRedoWriteRate, Definition: "每秒产生的 redo 字节数，来源 v$sysstat redo size 的每秒增量；与磁盘写吞吐联动评估 IO 压力", Unit: "B/s", Order: 1, Scale: scaleNoop},
	}},
	{Key: "oracle.engine.tablespace", Tab: LayerEngine, Name: "表空间使用率", Order: 8, Series: []DisplaySeries{
		{Key: "tablespace_used", MetricKey: MetricCapTablespaceUsedPct, Definition: "最大表空间使用率，统计对象为全部永久表空间（dba_data_files 与 dba_free_space 按表空间求和计算，不含临时表空间），取使用率最高者，取值范围 0 ~ 100；写满会导致写入失败（ORA-01653/01654）", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
}

var mysqlTabs = []DisplayTab{
	{Key: LayerHost, Label: "基础指标"},
	{Key: LayerEngine, Label: "数据库指标"},
	{Key: LayerKernel, Label: "引擎指标"},
}

// tidbDisplayItems TiDB 展示目录：基础指标 / 数据库指标 / 集群指标三个 Tab。
//
// 结构与 MySQL 保持一致，但不再整目录复用 MySQL —— 旧做法把 InnoDB 的
// Buffer Pool / 行锁 / Redo 等方言指标也塞进了 TiDB 模板，而 TiDB 的
// SHOW GLOBAL STATUS 根本没有这些计数器，模板里勾了就是一排恒空卡。
//
//   - 基础指标：与 MySQL / PG 同源同口径的 res.*（Prometheus + node_exporter，
//     采集侧不区分数据库类型），逐项照搬，单位与定义一字不差；
//   - 数据库指标：只保留 TiDB（兼容 MySQL 协议）真正能采到的语义层指标
//     （SHOW GLOBAL STATUS 的 Questions / Com_* / Threads_* / Slow_queries 等），
//     外加 TiDB 特有表的三张卡（长事务走 tidb_trx、活跃事务、锁等待走 data_lock_waits）；
//   - 集群指标：TiDB 特有的分布式组件视角（cluster_info / tikv_store_status /
//     tidb_hot_regions），这是 TiDB 区别于单机 MySQL 的核心监控面。
var tidbDisplayItems = []DisplayItem{
	// ---------------- 基础指标 ----------------
	{Key: "tidb.host.cpu_usage", Tab: LayerHost, Name: "CPU 使用率", Order: 1, Series: []DisplaySeries{
		{Key: "cpu_usage", MetricKey: MetricResCPU, Definition: "实例 CPU 使用率，统计对象为实例所在主机的 CPU（全部核心合计），计算口径为采集周期内非空闲时间占总 CPU 时间的比例，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "tidb.host.memory_usage", Tab: LayerHost, Name: "内存使用率", Order: 2, Series: []DisplaySeries{
		{Key: "mem_usage", MetricKey: MetricResMemory, Definition: "实例内存使用率，统计对象为实例所在主机的内存，计算口径为已使用内存占操作系统总内存的比例，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "tidb.host.disk_usage", Tab: LayerHost, Name: "磁盘使用率", Order: 3, Series: []DisplaySeries{
		{Key: "disk_usage", MetricKey: MetricResDisk, Definition: "磁盘使用率，统计对象为实例所在主机的全部真实磁盘分区（已排除 tmpfs/overlay 等伪文件系统与 /boot 启动分区），计算口径为这些分区中已用空间占比最高的那个分区，取值范围 0 ~ 100，最高 100", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "tidb.host.disk_used", Tab: LayerHost, Name: "磁盘使用量", Order: 4, Series: []DisplaySeries{
		// 与 MySQL / PG 侧保持一致：采集值为原始字节，由前端自动换算展示。
		{Key: "disk_used", MetricKey: MetricResDiskUsed, Definition: "磁盘使用量，统计对象为实例所在主机的全部真实磁盘分区（已排除 tmpfs/overlay 等伪文件系统与 /boot 启动分区，同一块盘重复挂载只计一次），计算口径为各分区总容量减去可用容量后的已用空间之和，取值范围 0 ~ 该主机磁盘总容量", Unit: "B", Order: 1, Scale: scaleNoop},
	}},
	// 数据库网络流量（perf.read_bytes / perf.write_bytes）已从 TiDB 目录移除：
	// TiDB 的 SHOW GLOBAL STATUS 不暴露 Bytes_sent / Bytes_received，
	// 采集分支对这两项恒定返回 unsupported，留在目录里就是一排永不更新的空卡。
	// 协议层流量请走 Prometheus 的 tidb_server_packet_io_bytes；主机整体流量看下面这张卡。
	// 主机网卡流量：整机视角，含 scp / SSH / 应用外网等一切经过物理网卡的流量。
	{Key: "tidb.host.net_traffic_host", Tab: LayerHost, Name: "主机网络流量", Order: 5, Series: []DisplaySeries{
		{Key: "net_in", MetricKey: MetricResNetworkIn, Definition: "主机网卡平均每秒接收字节数（已排除回环与容器虚拟网卡）", Unit: "B/s", Order: 1, Scale: scaleNoop},
		{Key: "net_out", MetricKey: MetricResNetworkOut, Definition: "主机网卡平均每秒发送字节数（已排除回环与容器虚拟网卡）", Unit: "B/s", Order: 2, Scale: scaleNoop},
	}},
	{Key: "tidb.host.iops", Tab: LayerHost, Name: "IOPS", Order: 6, Series: []DisplaySeries{
		{Key: "data_io_bytes", MetricKey: MetricResDiskThroughput, Definition: "每秒读写吞吐量", Unit: "B/s", Order: 1, Scale: scaleNoop},
		{Key: "data_io_ps", MetricKey: MetricResDiskIOPS, Definition: "读写次数", Unit: "Count", Order: 2, Scale: scaleNoop},
	}},
	{Key: "tidb.host.io_latency", Tab: LayerHost, Name: "IO 与磁盘延迟", Order: 7, Series: []DisplaySeries{
		{Key: "io_usage", MetricKey: MetricResIO, Definition: "磁盘 IO 使用率，统计对象为实例所在主机的全部真实磁盘，计算口径为采集周期内磁盘处理 IO 请求的时间占比，取值范围 0 ~ 100，持续接近 100 说明磁盘趋于饱和", Unit: "%", Order: 1, Scale: scaleNoop},
		{Key: "disk_await", MetricKey: MetricResDiskAwaitMs, Definition: "磁盘平均 IO 延迟，统计对象为实例所在主机的全部真实磁盘，计算口径为单次读写操作的平均耗时（毫秒）；机械盘 >10ms、SSD >2ms 即需关注", Unit: "ms", Order: 2, Scale: scaleNoop},
	}},
	{Key: "tidb.host.swap", Tab: LayerHost, Name: "Swap 使用率", Order: 8, Series: []DisplaySeries{
		{Key: "swap_used", MetricKey: MetricResSwapUsedPct, Definition: "Swap 使用率，统计对象为实例所在主机的操作系统交换分区，计算口径为已用 Swap 占 Swap 总量的比例，取值范围 0 ~ 100；数据库进程被换出会造成明显的延迟抖动", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "tidb.host.net_errors", Tab: LayerHost, Name: "网络错误/丢包", Order: 9, Series: []DisplaySeries{
		{Key: "net_errors", MetricKey: MetricResNetErrors, Definition: "主机网卡每秒错误与丢包总数，统计对象为实例所在主机的物理网卡（已排除回环与容器虚拟网卡），计算口径为收发错误（errs）与丢包（drop）速率之和；丢包会放大跨机复制与连接超时", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "tidb.host.proc_clock", Tab: LayerHost, Name: "进程与时钟", Order: 10, Series: []DisplaySeries{
		{Key: "fd_used", MetricKey: MetricResFDUsedPct, Definition: "文件句柄使用率，统计对象为实例所在主机的系统级文件句柄，计算口径为已分配句柄数占句柄上限的比例，取值范围 0 ~ 100；句柄耗尽会导致无法打开表文件甚至实例崩溃", Unit: "%", Order: 1, Scale: scaleNoop},
		{Key: "time_offset", MetricKey: MetricResTimeOffsetS, Definition: "主机时钟偏移，统计对象为实例所在主机与 NTP 源的时钟偏差，计算口径为时钟偏差绝对值的最大值（秒）；时钟漂移影响 TiKV 心跳与 PD TSO 授时链路", Unit: "s", Order: 2, Scale: scaleNoop},
	}},

	// ---------------- 数据库指标 ----------------
	// 会话连接：只保留 TiDB 真能取到数的连接数 / 活跃连接数。
	// 已移除「活跃连接占比」「连接使用率」：TiDB 的 max_connections 默认 0
	//（语义是"不限制连接数"，不是"读不到"），占比的分母不成立，
	// 采集侧恒定 unsupported。观测连接压力请直接看这两条绝对值。
	{Key: "tidb.engine.session", Tab: LayerEngine, Name: "会话连接", Order: 1, Series: []DisplaySeries{
		{Key: "active_session", MetricKey: MetricPerfActiveConn, Definition: "当前活跃会话，即正在执行语句（非休眠）的客户端连接数，取自 information_schema.processlist 中 command <> 'Sleep' 的连接数", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "total_session", MetricKey: MetricPerfConnCount, Definition: "当前全部会话，即已打开的客户端连接总数，取自 information_schema.processlist 的总行数", Unit: "Count", Order: 2, Scale: scaleNoop},
	}},
	{Key: "tidb.engine.qps_tps", Tab: LayerEngine, Name: "QPS/TPS", Order: 2, Series: []DisplaySeries{
		{Key: "qps", MetricKey: MetricPerfQPS, Definition: "计算公式：Queries / Uptime", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "tps", MetricKey: MetricPerfTPS, Definition: "计算公式：(Com_commit + Com_rollback) / Uptime；TiDB 的事务提交链路最终由 TiKV 两阶段提交承载", Unit: "Count", Order: 2, Scale: scaleNoop},
	}},
	// SQL 执行次数：只保留 TiDB statements_summary 的 STMT_TYPE 真正区分得出来的五类。
	// 已移除 INSERT...SELECT / REPLACE...SELECT：STMT_TYPE 只做
	// Select/Insert/Update/Delete/Replace 粗粒度归类，没有这两种细分计数。
	{Key: "tidb.engine.dml_rate", Tab: LayerEngine, Name: "SQL执行次数", Order: 3, Series: []DisplaySeries{
		{Key: "delete", MetricKey: MetricEngComDelete, Definition: "平均每秒 DELETE 语句执行次数", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "insert", MetricKey: MetricEngComInsert, Definition: "平均每秒 INSERT 语句执行次数", Unit: "Count", Order: 2, Scale: scaleNoop},
		{Key: "replace", MetricKey: MetricEngComReplace, Definition: "平均每秒 REPLACE 语句执行次数", Unit: "Count", Order: 3, Scale: scaleNoop},
		{Key: "select", MetricKey: MetricEngComSelect, Definition: "平均每秒 SELECT 语句执行次数", Unit: "Count", Order: 4, Scale: scaleNoop},
		{Key: "update", MetricKey: MetricEngComUpdate, Definition: "平均每秒 UPDATE 语句执行次数", Unit: "Count", Order: 5, Scale: scaleNoop},
	}},
	// 「全表扫描次数」整张卡已移除：TiDB 不提供 Select_scan 计数器，
	// 采集侧恒定 unsupported。看扫描量请查 statements_summary 的 AVG_TOTAL_KEYS
	// 或用慢日志 / TiDB Dashboard 的 Top SQL。
	{Key: "tidb.engine.threads", Tab: LayerEngine, Name: "线程", Order: 4, Series: []DisplaySeries{
		{Key: "threads_connected", MetricKey: MetricPerfConnCount, Definition: "当前全部线程数，取自 information_schema.processlist 的总行数。TiDB 每连接一个处理 goroutine 组，数值上等于当前客户端连接数", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "threads_running", MetricKey: MetricPerfActiveConn, Definition: "当前活跃线程数，取自 information_schema.processlist 中 command <> 'Sleep' 的连接数（正在执行语句、非休眠状态）", Unit: "Count", Order: 2, Scale: scaleNoop},
	}},
	{Key: "tidb.engine.slow_queries", Tab: LayerEngine, Name: "慢查询数量", Order: 5, Series: []DisplaySeries{
		{Key: "slow_queries", MetricKey: MetricEngSlowQueries, Definition: "每秒慢查询数，取自 SHOW STATUS 的 Slow_queries（按 tidb_slow_log_threshold 阈值统计）；定位具体语句请查 INFORMATION_SCHEMA.SLOW_QUERY / STATEMENTS_SUMMARY", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "tidb.engine.availability", Tab: LayerEngine, Name: "采集与可用性", Order: 6, Series: []DisplaySeries{
		{Key: "avail_up", MetricKey: MetricAvailUp, Definition: "实例可达性，采集器周期性对实例执行 select 1 探测，1 表示可达，非 1 表示无法连接（网络不通、实例宕机或账号权限异常）", Unit: "-", Order: 1, Scale: scaleNoop},
		{Key: "collect_ms", MetricKey: MetricAvailCollectMs, Definition: "采集耗时，本次采集周期内完成该实例全部指标采集所消耗的时间（毫秒），持续偏高说明实例负载高或到实例的网络抖动", Unit: "ms", Order: 2, Scale: scaleNoop},
	}},
	// 长事务：TiDB 没有 innodb_trx，采集分支改查 information_schema.tidb_trx（TRX_STARTED 超过 60 秒）。
	// 长事务阻碍 TiKV 的 MVCC GC safe point 推进，是 TiDB 上最典型的稳定性风险。
	{Key: "tidb.engine.long_trx", Tab: LayerEngine, Name: "长事务", Order: 7, Series: []DisplaySeries{
		{Key: "long_trx_count", MetricKey: MetricPerfLongTrx, Definition: "长事务数，统计对象为当前集群上已持续超过 60 秒仍未结束的事务（information_schema.tidb_trx）；长事务会阻碍 MVCC GC 推进 safe point，导致 TiKV 旧版本堆积与空间膨胀", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	// 活跃事务与锁等待：TiDB 特有表，单机 MySQL 没有对应语义。
	{Key: "tidb.engine.tidb_trx", Tab: LayerEngine, Name: "活跃事务", Order: 8, Series: []DisplaySeries{
		{Key: "trx_active", MetricKey: MetricEngTIDBTrxActive, Definition: "活跃事务数，统计对象为 information_schema.tidb_trx 的全部事务行（含空闲事务）；与「长事务」的区别是这里包含刚开启的正常事务，看的是并发水位而非持续时间", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "tidb.engine.lock_waits", Tab: LayerEngine, Name: "锁等待", Order: 9, Series: []DisplaySeries{
		{Key: "lock_waits", MetricKey: MetricEngTIDBLockWaits, Definition: "锁等待数，统计对象为 information_schema.data_lock_waits 的等待事务对数量；持续非 0 说明存在行锁竞争，配合 TIDB_TRX 与 TIDB_DEADLOCKS 定位阻塞链", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "tidb.engine.no_pk_tables", Tab: LayerEngine, Name: "无主键表", Order: 10, Series: []DisplaySeries{
		{Key: "no_pk_tables", MetricKey: MetricRiskNoPKTables, Definition: "无主键表数，统计对象为实例业务库中所有没有主键的表；无主键表影响 TiDB 的数据迁移工具（DM/TiCDC 依赖主键定位行）与批量更新效率，建议补主键或唯一键", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},

	// ---------------- 集群指标（TiDB 特有：PD / TiKV 组件视角） ----------------
	// 采集口径说明：全部经 MySQL 协议查 INFORMATION_SCHEMA 的 TiDB 特有表。
	// TIKV_STORE_STATUS 在 TiDB 7.5+ 被移除（改由 PD API / Prometheus 承载），
	// 受影响版本上相关卡片显式标注「不支持」，不会静默缺数。
	{Key: "tidb.kernel.cluster_nodes", Tab: LayerKernel, Name: "集群节点", Order: 1, Series: []DisplaySeries{
		{Key: "tidb_nodes", MetricKey: MetricKernTIDBNodeTotal, Definition: "TiDB Server 节点数，取自 information_schema.cluster_info 中 type='tidb' 的行数；从本实例视角反映接入层拓扑", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "abnormal_nodes", MetricKey: MetricKernTIDBNodeAbnormal, Definition: "异常组件节点数，统计对象为 cluster_info 中 status 不为 UP 的全部组件节点（TiDB / TiKV / PD / TiFlash / TiCDC）；持续非 0 说明集群拓扑受损", Unit: "Count", Order: 2, Scale: scaleNoop},
	}},
	{Key: "tidb.kernel.tikv_stores", Tab: LayerKernel, Name: "TiKV Store", Order: 2, Series: []DisplaySeries{
		{Key: "store_total", MetricKey: MetricKernTiKVStoreTotal, Definition: "TiKV Store 总数，取自 information_schema.tikv_store_status 的行数；异常回落说明有 store 被摘除或下线", Unit: "Count", Order: 1, Scale: scaleNoop},
		{Key: "store_down", MetricKey: MetricKernTiKVStoreDown, Definition: "异常 TiKV Store 数，统计对象为 state_name 不为 Up 的 store；多于副本数减一时存在数据不可用风险", Unit: "Count", Order: 2, Scale: scaleNoop},
	}},
	{Key: "tidb.kernel.tikv_storage", Tab: LayerKernel, Name: "TiKV 存储使用率", Order: 3, Series: []DisplaySeries{
		{Key: "store_used_pct", MetricKey: MetricKernTiKVStoreUsedPct, Definition: "TiKV 存储使用率，统计对象为全部 TiKV store，计算口径为 (capacity − available) / capacity 取最大值；超过 80% 会触发 PD 空间告警并可能拒绝写入", Unit: "%", Order: 1, Scale: scaleNoop},
	}},
	{Key: "tidb.kernel.tikv_regions", Tab: LayerKernel, Name: "Region 总数", Order: 4, Series: []DisplaySeries{
		{Key: "region_total", MetricKey: MetricKernTiKVRegionTotal, Definition: "Region 总数，统计对象为全部 TiKV store 的 region_count 之和；增长速率反映写入热度，单个 Region 默认 96MB 触发分裂", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
	{Key: "tidb.kernel.tikv_hot_regions", Tab: LayerKernel, Name: "热点 Region", Order: 5, Series: []DisplaySeries{
		{Key: "hot_regions", MetricKey: MetricKernTiKVHotRegions, Definition: "热点 Region 数，统计对象为 information_schema.tidb_hot_regions 的行数（含读/写热点）；热点集中在个别节点时会形成单点瓶颈", Unit: "Count", Order: 1, Scale: scaleNoop},
	}},
}

var tidbTabs = []DisplayTab{
	{Key: LayerHost, Label: "基础指标"},
	{Key: LayerEngine, Label: "数据库指标"},
	{Key: LayerKernel, Label: "集群指标"},
}

// displayLayouts 按小写引擎标识索引的展示目录。
var displayLayouts = map[string]displayLayoutDef{
	"mysql": {Tabs: mysqlTabs, Items: mysqlDisplayItems},
	// TiDB：专属展示目录（MySQL 兼容的语义层指标 + TiDB 特有的事务/锁/集群组件指标），
	// 不再整目录复用 MySQL —— InnoDB 方言指标在 TiDB 上没有采集源，勾进模板只会恒空。
	// Doris 继续复用 MySQL 目录（同样兼容 MySQL 协议与状态变量）。
	"tidb":  {Tabs: tidbTabs, Items: tidbDisplayItems},
	"doris": {Tabs: mysqlTabs, Items: mysqlDisplayItems},
	"postgresql": {
		// PostgreSQL 两个 Tab：基础指标（主机层 res.*，与 MySQL 同源同采集）+ 数据库指标（pg_stat_*）。
		// 主机层不再留空——数据一直在采，空目录只会让页面误报"采集失败"。
		Tabs: []DisplayTab{
			{Key: LayerHost, Label: "基础指标"},
			{Key: LayerEngine, Label: "数据库指标"},
		},
		Items: postgresqlDisplayItems,
	},
	// 金仓 KingbaseES：兼容 PG 协议，pg_stat_* 指标 key 与 PostgreSQL 完全一致，
	// 展示目录整目录复用（指标 key 相同才能让模板、告警、明细页三处对齐）。
	"kingbase": {
		Tabs: []DisplayTab{
			{Key: LayerHost, Label: "基础指标"},
			{Key: LayerEngine, Label: "数据库指标"},
		},
		Items: postgresqlDisplayItems,
	},
	// Oracle：基础指标（推荐告警集的 res.*，不受模板门控）+ 数据库指标（v$ / dba_* 直连）
	"oracle": {
		Tabs: []DisplayTab{
			{Key: LayerHost, Label: "基础指标"},
			{Key: LayerEngine, Label: "数据库指标"},
		},
		Items: oracleDisplayItems,
	},
	// SQL Server：基础指标（与 PG 同源同口径的 res.*）+ 数据库指标（sys.dm_* 直连）
	"sqlserver": {
		Tabs: []DisplayTab{
			{Key: LayerHost, Label: "基础指标"},
			{Key: LayerEngine, Label: "数据库指标"},
		},
		Items: sqlserverDisplayItems,
	},
}

// MonitorDisplayLayoutFor 取某个数据库类型的展示目录，并按实例角色裁剪。
//
// role 传实例角色（master / slave / standalone / cascade）；传空串表示角色未知或
// 调用方与具体实例无关（例如拼接模板勾选清单），此时不做任何裁剪。
//
// 角色不适用（原「裁剪」）的处理口径：受角色约束的指标**不再从目录里删除**，
// 而是保留该行、置 NotApplicable=true 并带上原因，由前端显示「无数据 + 原因」。
//
// 为什么要改：指标模板是「按数据库类型」配置的，模板里永远列出该类型的全部指标；
// 而明细页按实例角色整行丢弃，于是单机实例上「模板有 5 个复制指标、明细一个都没有」，
// 看上去像漏配，实际上是被角色规则藏起来了 —— 用户无从区分"没配"和"不适用"。
// 保留 + 标注原因后，两处列表天然对齐，原因也自解释。
//
// 采集 / 告警口径不受影响：resolveInstanceMetricKeys 与 filterRoleInapplicableKeys
// 仍然剔除这些键，所以它们既不采集、也不产生告警，卡片只是"存在但为空"。
//
// 目录定义本身（Tabs / Items / Series）仍是一个不修改的包级变量，
// 标记只发生在本次返回的副本上。
func MonitorDisplayLayoutFor(dbType string, role string) *MonitorDisplayLayout {
	entry, ok := displayLayouts[strings.ToLower(strings.TrimSpace(dbType))]
	if !ok {
		return nil
	}
	engineKey := strings.ToLower(strings.TrimSpace(dbType))
	// 拷贝一份返回：调用方拿到的切片是只读展示数据，避免被上游 append 污染包级变量
	items := make([]DisplayItem, 0, len(entry.Items))
	for _, item := range entry.Items {
		series := make([]DisplaySeries, 0, len(item.Series))
		for _, s := range item.Series {
			// 角色不适用：不下发"假数据"，但也绝不删行 —— 保留该行并标注原因，
			// 页面上表现为「无数据：当前实例角色为 X，该指标结构性不存在」。
			// 这样「指标模板有、明细没有」的差异就消失了，运维一眼能看出是不适用而非漏配。
			if !metricApplicableToRole(engineKey, s.MetricKey, role) {
				s.NotApplicable = true
				s.NotApplicableReason = roleInapplicableReason(engineKey, s.MetricKey, role)
				series = append(series, s)
				continue
			}
			if s.Scale == 0 {
				s.Scale = scaleNoop
			}
			series = append(series, s)
		}
		if len(series) == 0 {
			continue
		}
		items = append(items, DisplayItem{
			Key:    item.Key,
			Tab:    item.Tab,
			Name:   item.Name,
			Order:  item.Order,
			Series: series,
		})
	}
	// 只下发真正有监控项的 Tab：指标迁移或裁剪后，原分类可能一个监控项都不剩，
	// 继续下发会留下空标题、空区块和多余分割线。这里按 items 实际归属动态裁剪，
	// 分层配置仍以 Tabs 为唯一来源——后续补回监控项，Tab 会自动重新出现。
	used := map[string]struct{}{}
	for _, item := range items {
		used[item.Tab] = struct{}{}
	}
	tabs := make([]DisplayTab, 0, len(entry.Tabs))
	for _, tab := range entry.Tabs {
		if _, ok := used[tab.Key]; !ok {
			continue
		}
		tabs = append(tabs, tab)
	}
	return &MonitorDisplayLayout{
		DbType: strings.ToLower(strings.TrimSpace(dbType)),
		Tabs:   tabs,
		Items:  items,
	}
}

// displayLayoutMetricKeys 返回某数据库类型展示目录中、应进入指标模板的采集键集合。
//
// 用途：让「指标模板」的默认集合与「前端展示目录」对齐。展示目录是明细页的唯一事实来源，
// 明细页展示什么，模板就勾选什么，一层不差。
// Series.MetricKey 为空的项表示"业务口径里有、但当前没有采集源"，这类指标不进模板，
// 避免模板里出现勾了也采不到数据的项。
//
// 主机层（res.*）同样纳入：MySQL 明细页的基础指标 Tab 展示了 CPU / 内存 / 磁盘 / IOPS，
// 模板必须与之一致。主机层由 Prometheus 统一采集、不参与"模板裁剪采集范围"的过滤
// （见 resolveInstanceMetricKeys 与 monitor_store.go 对 res.* 的豁免），
// 因此进模板只是对齐展示口径，不会引发重复采集。
//
// 调用方限定：仅 MySQL / PostgreSQL 默认模板的播种流程使用。
func displayLayoutMetricKeys(dbType string) []string {
	entry, ok := displayLayouts[strings.ToLower(strings.TrimSpace(dbType))]
	if !ok {
		return nil
	}
	seen := map[string]struct{}{}
	keys := make([]string, 0)
	for _, item := range entry.Items {
		for _, s := range item.Series {
			key := strings.TrimSpace(s.MetricKey)
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// ---------------------------------------------------------------------------
// 模板勾选界面的「明细口径」展开清单
//
// 指标模板的勾选清单原本按指标字典输出（按 key 排序），与监控明细的展示顺序、
// 命名、口径都对不上。业务口径以明细页为唯一事实来源：
//   - 多值指标（如 网络流量 = input_traffic + output_traffic）逐行展开，一行一个；
//   - 保持附件的原始名称、单位与排列顺序，不合并、不去重；
//   - 无采集源的行照常列出（MetricKey 为空），由调用方标记「不可用」。
// ---------------------------------------------------------------------------

// LayoutTemplateItem 明细口径展开后的单个指标行。
type LayoutTemplateItem struct {
	ItemKey    string // 所属监控项标识（如 mysql.host.network_traffic）
	ItemName   string // 所属监控项名称（如 网络流量）
	Tab        string // host / engine / kernel
	SeriesKey  string // 明细里的原始指标名（如 input_traffic）
	Unit       string // 明细口径单位
	MetricKey  string // 平台采集键；空串 = 无采集源
	Definition string // 明细口径说明
}

// displayLayoutTemplateItems 把展示目录逐行展开成模板勾选清单。
// 顺序 = 目录顺序（Tab → 监控项 → 行指标），不做任何排序与去重。
// 模板勾选清单是"类型級"的全量目录，不针对具体实例，
// 因此传空角色：角色裁剪只作用于明细页运行时，模板里这些指标照常可勾选。
func displayLayoutTemplateItems(dbType string) []LayoutTemplateItem {
	layout := MonitorDisplayLayoutFor(dbType, "")
	if layout == nil {
		return nil
	}
	rows := make([]LayoutTemplateItem, 0, 64)
	for _, item := range layout.Items {
		for _, s := range item.Series {
			rows = append(rows, LayoutTemplateItem{
				ItemKey:    item.Key,
				ItemName:   item.Name,
				Tab:        item.Tab,
				SeriesKey:  s.Key,
				Unit:       s.Unit,
				MetricKey:  s.MetricKey,
				Definition: s.Definition,
			})
		}
	}
	return rows
}
