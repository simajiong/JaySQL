package services

// ============================================================================
// 实例画像采集回写
//
// 背景：监控明细页「实例概览」（第二部分）要展示数据库版本 / 操作系统 / 规格 /
// 运行时长 / 角色 / 探测耗时，但这些字段原先在库里没有任何写入方 ——
// 页面只能渲染成 "—" + "未采集"，而且永远是这个状态。
//
// 这一层把"能探测到的事实"真正采回来写进 insight_instances：
//   - 引擎侧（直连）：版本、角色（主/从）、运行时长；
//   - 主机侧（Prometheus）：操作系统与内核、CPU 核数、内存、磁盘容量；
//   - 网络侧：真实 TCP 拨号耗时。
//
// 三条纪律：
//   1. 节流：这些都是"几乎不变"的静态事实，跟着 10 秒一轮的指标采集反复查纯属浪费，
//      按 profileFactsInterval 刷一次即可（profile_facts_at 为空 = 从未采过，立即补）；
//   2. 只写非空：采不到就保持原值不动，绝不用空串/0 覆盖已有数据 —— 否则一次网络抖动
//      就能把已经采到的画像清空；
//   3. 一个都没采到就不打时间戳，下一轮继续尝试，避免"失败也被节流一小时"。
//
// 采不到的字段（部署形态 / 高可用模式 / 磁盘类型）属于运维录入的产品事实，
// 数据库侧探测不出来，保持 available=false + reason，不做猜测。
// ============================================================================

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/common/sqlserverdsn"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"
)

// profileFactsInterval 画像静态事实的刷新间隔。
// 版本、规格、角色这类事实以天为单位变化，1 小时足够"看得见变化"，
// 又不至于给每个实例每小时增加太多往返。
const profileFactsInterval = time.Hour

// profileFacts 一轮回写采集到的画像事实。零值表示"没采到"，不参与回写。
type profileFacts struct {
	DbVersion      string
	InstanceRole   string
	UptimeSeconds  int64
	OsName         string
	OsKernel       string
	CpuCores       int
	MemoryGB       float64
	DiskGB         float64
	ProbeLatencyMs int
}

// refreshInstanceProfileFacts 采集并回写实例画像。
// force=true 时跳过节流（"立即采集"用它，保证点一次就能刷新画像）。
//
// 这里的错误一律只记日志不返回：画像采集失败不影响本轮指标落库，
// 也不应该让整个采集任务失败 —— 指标与画像是两条独立的可用性。
func refreshInstanceProfileFacts(
	ctx context.Context,
	row monitorInstanceRow,
	promStatus PrometheusStatus,
	promClient *prometheusClient,
	force bool,
) {
	if global.App.DB == nil || row.InstanceID == "" {
		return
	}
	if !force && !profileFactsDue(row.InstanceID) {
		return
	}

	childCtx, cancel := context.WithTimeout(ctx, instanceTimeoutSeconds(row)+5*time.Second)
	defer cancel()

	facts := collectEngineProfileFacts(childCtx, row)
	host := collectHostProfileFacts(childCtx, promClient, promStatus, row)
	facts.OsName = host.OsName
	facts.OsKernel = host.OsKernel
	facts.CpuCores = host.CpuCores
	facts.MemoryGB = host.MemoryGB
	facts.DiskGB = host.DiskGB
	facts.ProbeLatencyMs = probeTCPLatencyMs(row)

	// 引擎侧"该采却没采到"必须能尽快重试：一次超时若换来一小时节流，
	// 页面上就是长达一小时的空版本（2026-10-18 的 PG 15.19 实例即此）。
	// 对本身就不支持画像采集的引擎（SQL Server / MongoDB 等）不做此要求，否则会每轮空转。
	engineExpected := engineImplemented(row.DbType)
	engineSatisfied := !engineExpected || facts.DbVersion != "" || facts.UptimeSeconds > 0
	if !engineSatisfied {
		markProfileFactsRetry(row.InstanceID)
	}

	if global.App.Log != nil && (facts.DbVersion != "" || facts.UptimeSeconds > 0) {
		global.App.Log.Debugf("[monitor] 实例 %s 画像回写：版本=%s 角色=%s 运行时长=%ds OS=%s 核数=%d",
			row.InstanceID, facts.DbVersion, facts.InstanceRole, facts.UptimeSeconds, facts.OsName, facts.CpuCores)
	}
	if persistProfileFacts(row.InstanceID, facts, engineSatisfied) {
		markProfileFactsDone(row.InstanceID)
	}
}

// profileFactsCache 进程内节流缓存：实例ID → 上次成功回写时间。
// 采集 10 秒一轮，若每次都去库里查 profile_facts_at，100 个实例就是每分钟
// 600 次无谓的主键查询。库里的时间戳仍为准（进程重启后靠它恢复），
// 这里只是挡掉"绝大多数轮次"的第一道闸。
var (
	profileFactsMu    sync.Mutex
	profileFactsCache = map[string]time.Time{}
)

// profileRetryInterval 引擎侧采集失败后的重试间隔。
//
// 画像正常按 profileFactsInterval（1 小时）节流，但"引擎没采到"通常是临时故障
// （连接超时、网络抖动），等满一小时意味着页面上空一小时。
// 失败后按 30 秒重试 —— 比采集周期长，不会变成每轮空转，又能让故障自愈后快速补齐。
const profileRetryInterval = 30 * time.Second

var (
	profileRetryMu sync.Mutex
	profileRetryAt = map[string]time.Time{}
)

// markProfileFactsRetry 标记一次引擎侧采集失败，30 秒后允许重试。
func markProfileFactsRetry(instanceID string) {
	profileRetryMu.Lock()
	profileRetryAt[instanceID] = time.Now().Add(profileRetryInterval)
	profileRetryMu.Unlock()
}

// profileFactsDue 判断是否需要刷新画像。查不到实例或从未采过时返回 true。
func profileFactsDue(instanceID string) bool {
	// 失败重试优先于小时级节流：上一轮引擎没采到时，30 秒后就该再试一次。
	profileRetryMu.Lock()
	if at, ok := profileRetryAt[instanceID]; ok {
		if time.Now().Before(at) {
			profileRetryMu.Unlock()
			return false
		}
		delete(profileRetryAt, instanceID)
	}
	profileRetryMu.Unlock()

	profileFactsMu.Lock()
	if last, ok := profileFactsCache[instanceID]; ok && time.Since(last) < profileFactsInterval {
		profileFactsMu.Unlock()
		return false
	}
	profileFactsMu.Unlock()

	var dest struct {
		ProfileFactsAt *time.Time
	}
	if err := global.App.DB.Table("insight_instances").
		Select("profile_facts_at").
		Where("instance_id = ?", instanceID).
		Scan(&dest).Error; err != nil {
		// 查不到就按"该采"处理：宁可多采一次，也不要因为一次查询抖动长期缺数。
		return true
	}
	if dest.ProfileFactsAt == nil {
		return true
	}
	due := time.Since(*dest.ProfileFactsAt) >= profileFactsInterval
	if !due {
		// 库里显示还不到时间，顺手把缓存补上，避免下一轮重复查库。
		profileFactsMu.Lock()
		profileFactsCache[instanceID] = *dest.ProfileFactsAt
		profileFactsMu.Unlock()
	}
	return due
}

// markProfileFactsDone 记录本实例刚刚完成一次画像回写。
// 只在真的写进库之后调用：失败的一轮不该换来一小时的静默。
func markProfileFactsDone(instanceID string) {
	profileFactsMu.Lock()
	profileFactsCache[instanceID] = time.Now()
	profileFactsMu.Unlock()

	// 本轮已经成功，清掉失败重试标记，避免残留状态影响后续判定。
	profileRetryMu.Lock()
	delete(profileRetryAt, instanceID)
	profileRetryMu.Unlock()
}

// persistProfileFacts 把采集到的事实回写实例行。
//
// 只写非空字段。engineSatisfied=false（引擎侧该采却没采到）时不刷新
// profile_facts_at —— 主机侧事实照写，但下一轮必须能重试，
// 不能被"这轮发生过采集"的错觉节流掉一小时。
// 返回值表示"本轮画像是否算完成"，决定进程内节流缓存是否落子。
func persistProfileFacts(instanceID string, facts profileFacts, engineSatisfied bool) bool {
	updates := profileFactsUpdates(facts)
	if len(updates) == 0 {
		return false
	}
	if engineSatisfied {
		updates["profile_facts_at"] = commonModels.LocalTime(time.Now())
	}
	if err := global.App.DB.Table("insight_instances").
		Where("instance_id = ?", instanceID).
		Updates(updates).Error; err != nil {
		if global.App.Log != nil {
			global.App.Log.Warnf("[monitor] 实例 %s 画像回写失败: %v", instanceID, err)
		}
		return false
	}
	return engineSatisfied
}

// profileFactsUpdates 把画像事实转成待更新字段集合。
// 单独抽出来是为了能被单测直接覆盖 —— "只写非空"这条纪律一旦破防，
// 一次网络抖动就会把已经采到的画像清空，必须有测试兜住。
func profileFactsUpdates(facts profileFacts) map[string]any {
	updates := map[string]any{}
	if facts.DbVersion != "" {
		updates["db_version"] = truncateText(facts.DbVersion, 64)
	}
	if facts.InstanceRole != "" {
		updates["instance_role"] = facts.InstanceRole
	}
	if facts.UptimeSeconds > 0 {
		updates["uptime_seconds"] = facts.UptimeSeconds
	}
	if facts.OsName != "" {
		updates["os_name"] = truncateText(facts.OsName, 64)
	}
	if facts.OsKernel != "" {
		updates["os_kernel"] = truncateText(facts.OsKernel, 128)
	}
	if facts.CpuCores > 0 {
		updates["cpu_cores"] = facts.CpuCores
	}
	if facts.MemoryGB > 0 {
		updates["memory_gb"] = facts.MemoryGB
	}
	if facts.DiskGB > 0 {
		updates["disk_gb"] = facts.DiskGB
	}
	if facts.ProbeLatencyMs > 0 {
		updates["probe_latency_ms"] = facts.ProbeLatencyMs
	}
	return updates
}

// ---------------------------------------------------------------------------
// 引擎侧：版本 / 角色 / 运行时长
// ---------------------------------------------------------------------------

// collectEngineProfileFacts 直连数据库取静态事实。采不到返回零值，由调用方保持原状。
func collectEngineProfileFacts(ctx context.Context, row monitorInstanceRow) profileFacts {
	facts := profileFacts{}
	password, err := secret.Decrypt(row.Password)
	if err != nil {
		return facts
	}

	switch resolveEngine(row.DbType) {
	case "mysql", "tidb", "doris", "mariadb":
		facts.DbVersion, facts.InstanceRole, facts.UptimeSeconds = collectMySQLProfileFacts(ctx, row, password)
	case "postgresql", "kingbase", "kingbasees":
		facts.DbVersion, facts.InstanceRole, facts.UptimeSeconds = collectPostgresProfileFacts(ctx, row, password)
	case "clickhouse":
		facts.DbVersion, facts.UptimeSeconds = collectClickHouseProfileFacts(ctx, row, password)
	case "oracle":
		facts.DbVersion, facts.InstanceRole, facts.UptimeSeconds = collectOracleProfileFacts(ctx, row, password)
	case "sqlserver":
		// 2026-11-11 补齐：此前 SQL Server 落在 default 分支不回写，
		// 数据源总览的 SQLServer 实例永远没有版本号，且 engineSatisfied
		// 永不满足导致每 30 秒空转重试。
		facts.DbVersion, facts.InstanceRole, facts.UptimeSeconds = collectSQLServerProfileFacts(ctx, row, password)
	default:
		// MongoDB 等暂不回写：保持 available=false + reason，
		// 而不是写入一个"看起来有值"的猜测。
	}
	return facts
}

// collectMySQLProfileFacts MySQL 系（含 TiDB / Doris / MariaDB）画像事实。
//
// 四条查询，任一失败不影响其它项：
//   - 版本与只读：SELECT VERSION(), @@read_only
//   - 运行时长：SHOW GLOBAL STATUS 的 Uptime
//   - 角色：SHOW REPLICA STATUS / SHOW SLAVE STATUS 有结果即为从库
//
// 四条走同一条会话连接：改造前每条各建一次连接（最坏 4 条），
// 握手开销比查询本身还大，而画像虽然节流到 1 小时一次，
// 但它是"实例刚接入时要连着采好几轮"的链路 —— 这里省下来的是实打实的连接数。
func collectMySQLProfileFacts(ctx context.Context, row monitorInstanceRow, password string) (string, string, int64) {
	session, err := dao.OpenMySQLSession(dao.SessionConfig{
		User:     row.User,
		Password: password,
		Host:     row.Hostname,
		Port:     row.Port,
		Params:   map[string]string{"charset": "utf8mb4"},
		Ctx:      ctx,
	})
	if err != nil {
		return "", "", 0
	}
	defer session.Close()

	version := ""
	readOnly := false
	if _, rows, err := session.Query(`SELECT VERSION() AS db_version, @@read_only AS read_only`); err == nil && rows != nil && len(*rows) > 0 {
		version = normalizeDbVersion(toString((*rows)[0]["db_version"]))
		readOnly = toString((*rows)[0]["read_only"]) == "1"
	}

	var uptime int64
	if _, rows, err := session.Query(`SHOW GLOBAL STATUS WHERE Variable_name = 'Uptime'`); err == nil && rows != nil && len(*rows) > 0 {
		uptime = int64(toFloat64((*rows)[0]["Value"]))
	}

	role := ""
	switch {
	case mysqlHasReplicaStatus(session):
		role = "slave"
	case readOnly:
		// 只读但没配复制：常见于从库或只读实例，按从库展示比按主库更贴近事实。
		role = "slave"
	default:
		role = "master"
	}
	return version, role, uptime
}

// mysqlHasReplicaStatus 判断实例是否配置了复制（有结果行即是从库）。
// MySQL 8.0.22+ 推荐 SHOW REPLICA STATUS，老版本只有 SHOW SLAVE STATUS，
// 两个都试：任一成功且有行即认为在复制链路里。
func mysqlHasReplicaStatus(session *dao.MySQLSession) bool {
	for _, stmt := range []string{"SHOW REPLICA STATUS", "SHOW SLAVE STATUS"} {
		if _, rows, err := session.Query(stmt); err == nil && rows != nil && len(*rows) > 0 {
			return true
		}
	}
	return false
}

// collectPostgresProfileFacts PostgreSQL 画像事实（单条查询取全）。
func collectPostgresProfileFacts(ctx context.Context, row monitorInstanceRow, password string) (string, string, int64) {
	cfg, err := parseExtraConfig(row.ExtraConfig)
	if err != nil {
		logProfileSkip(row, "PostgreSQL", "扩展参数解析失败", err)
		return "", "", 0
	}
	db := dao.PostgresDB{
		User:     row.User,
		Password: password,
		Host:     row.Hostname,
		Port:     row.Port,
		Database: getStringConfig(cfg, "database", "postgres"),
		SSLMode:  getStringConfig(cfg, "sslmode", "disable"),
		Ctx:      ctx,
	}
	// current_setting('server_version') 在部分发行版上带构建后缀（如 "15.3 (Debian 15.3-1)"），
	// 由 normalizeDbVersion 取前导版本号。
	_, rows, err := db.Query(`SELECT
		current_setting('server_version') AS db_version,
		pg_is_in_recovery() AS in_recovery,
		EXTRACT(EPOCH FROM (now() - pg_postmaster_start_time()))::bigint AS uptime_seconds`)
	if err != nil {
		logProfileSkip(row, "PostgreSQL", "查询失败", err)
		return "", "", 0
	}
	if rows == nil || len(*rows) == 0 {
		logProfileSkip(row, "PostgreSQL", "查询无返回行", nil)
		return "", "", 0
	}
	item := (*rows)[0]
	version := normalizeDbVersion(toString(item["db_version"]))
	// 角色：pg_is_in_recovery() 只能区分"备库 / 非备库"。
	// 单机（既不处于恢复中、也没有任何下游与复制槽）必须单独识别 ——
	// 否则单机实例会被标成 master，进而展示出压根不存在的复制类指标。
	// 这里与采集侧共用同一套推断逻辑（inferPostgresRole），避免两处口径漂移。
	role := RoleSlave
	if !strings.EqualFold(toString(item["in_recovery"]), "true") {
		if probed := probePostgresRole(db); probed != "" {
			role = probed
		} else {
			// 拿不到可信的"是否有下游/复制槽"信号时保持既有二值语义，
			// 绝不让画像因为一次探针失败就丢掉角色。
			role = RoleMaster
		}
	}
	return version, role, int64(toFloat64(item["uptime_seconds"]))
}

// probePostgresRole 独立探针：补取"下游数量 / 复制槽数量"以区分主库与单机。
//
// 为什么单独一次查询而不内联进上面的画像查询：画像里版本与运行时长是最基础的事实，
// 不能为了多挂两个子查询把整个画像赌上。探针失败返回空串，由调用方回落到二值判定。
//
// pg_stat_replication 受 security-restricted 约束，无 pg_read_all_stats / pg_monitor /
// 超级用户权限时返回零行而非报错，"看不见"与"没有下游"在结果上无法区分，
// 因此用 CASE 把不可信的情形折算成 -1（ReplicaCountUnknown），由 inferPostgresRole 按未知处理。
func probePostgresRole(db dao.PostgresDB) string {
	_, rows, err := db.Query(`SELECT
		(SELECT COUNT(*) FROM pg_replication_slots) AS slot_count,
		CASE WHEN
			COALESCE((SELECT rolsuper FROM pg_roles WHERE rolname = current_user), false)
			OR pg_has_role(current_user, 'pg_read_all_stats', 'MEMBER')
			OR pg_has_role(current_user, 'pg_monitor', 'MEMBER')
		THEN (SELECT COUNT(*) FROM pg_stat_replication) ELSE -1 END AS replica_count`)
	if err != nil || rows == nil || len(*rows) == 0 {
		return ""
	}
	item := (*rows)[0]
	// 列缺失或取值非法时保守回 unknown：把"读不到"当成"没有下游"会误判成单机，
	// 进而把真主库的复制延迟整片藏掉 —— 那比多显示一个"无数据"严重得多。
	return inferPostgresRole(pgRoleSignals{
		SlotCount:    probeRoleCount(item["slot_count"], 0),
		ReplicaCount: probeRoleCount(item["replica_count"], ReplicaCountUnknown),
	})
}

// probeRoleCount 把探针返回的计数列转成 int，拿不到就用 fallback。
func probeRoleCount(value any, fallback int) int {
	if value == nil {
		return fallback
	}
	text := strings.TrimSpace(toString(value))
	if text == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(text)
	if err != nil {
		return fallback
	}
	return parsed
}

// collectClickHouseProfileFacts ClickHouse 画像事实：version() / uptime()。
// 角色不推导：CH 的副本角色依赖 Replicated 表引擎与 zookeeper 路径，
// 没有稳定的单值答案，宁可不写也不要写错。
func collectClickHouseProfileFacts(ctx context.Context, row monitorInstanceRow, password string) (string, int64) {
	db := dao.ClickhouseDB{
		User:     row.User,
		Password: password,
		Host:     row.Hostname,
		Port:     row.Port,
		Database: "default",
		Settings: map[string]any{},
		Ctx:      ctx,
	}
	_, rows, err := db.Query(`SELECT version() AS db_version, uptime() AS uptime_seconds`)
	if err != nil || rows == nil || len(*rows) == 0 {
		return "", 0
	}
	return normalizeDbVersion(toString((*rows)[0]["db_version"])), int64(toFloat64((*rows)[0]["uptime_seconds"]))
}

// oracleProfileFactsSQL 取 Oracle 版本 / 角色 / 运行时长（12.2+ 口径）。
//
// 版本取 version_full 而不是 version：Oracle 12.2 起 v$instance.version 只给主版本
// （19c 一律是 19.0.0.0.0），version_full 才带 Release Update（19.3.0.0.0）。
// 对 DBA 来说「19.0.0.0.0」区分不出打了哪个 RU，几乎没有信息量 ——
// 页面显示的必须是 19.3.0.0.0 这种能定位补丁级别的版本。
//
// 三个别名都用双引号锁成小写：Oracle 会把不带引号的标识符折叠成大写
// （AS db_version → DB_VERSION），而 dao.OracleDB 直接拿 rows.Columns() 做 map key，
// 大小写对不上就会取到 nil。提成包级常量是为了让单测能断言引号没被改掉。
const oracleProfileFactsSQL = `SELECT
		(SELECT version_full FROM v$instance) AS "db_version",
		(SELECT database_role FROM v$database) AS "db_role",
		(SELECT ROUND((SYSDATE - startup_time) * 86400) FROM v$instance) AS "uptime_seconds"
	FROM dual`

// oracleProfileFactsLegacySQL 回退版本：12.1 及更早没有 version_full 列，
// 查它会报 ORA-00904（无效标识符）。那些版本只能用 version（主版本口径）。
const oracleProfileFactsLegacySQL = `SELECT
		(SELECT version FROM v$instance) AS "db_version",
		(SELECT database_role FROM v$database) AS "db_role",
		(SELECT ROUND((SYSDATE - startup_time) * 86400) FROM v$instance) AS "uptime_seconds"
	FROM dual`

// collectOracleProfileFacts Oracle 画像事实：v$instance / v$database。
//
// ⚠️ 别名必须带双引号：Oracle 会把不带引号的标识符统一折叠成大写
// （AS db_version → DB_VERSION），而 dao.OracleDB 的返回 map 直接用 rows.Columns()
// 当 key —— 不带引号时按小写 key 取值永远取不到，toString(nil) 会得到字符串
// "<nil>" 并写进 db_version，页面上就显示成「Oracle <nil>」。
// MySQL / PostgreSQL 返回的列名本来就是小写，所以只有 Oracle 中招。
// 取值仍走 oracleProfileValue 做一次大小写兜底，避免以后有人改回不带引号的写法。
func collectOracleProfileFacts(ctx context.Context, row monitorInstanceRow, password string) (string, string, int64) {
	cfg, err := parseExtraConfig(row.ExtraConfig)
	if err != nil {
		logProfileSkip(row, "Oracle", "扩展参数解析失败", err)
		return "", "", 0
	}
	serviceName := getStringConfig(cfg, "service_name", "")
	if serviceName == "" {
		logProfileSkip(row, "Oracle", "service_name 未配置", nil)
		return "", "", 0
	}
	db := dao.OracleDB{
		User:        row.User,
		Password:    password,
		Host:        row.Hostname,
		Port:        row.Port,
		ServiceName: serviceName,
		Ctx:         ctx,
	}
	_, rows, err := db.Query(oracleProfileFactsSQL)
	if err != nil && strings.Contains(strings.ToUpper(err.Error()), "ORA-00904") {
		// version_full 从 12.2 起才有；12.1 及更早报 ORA-00904（无效标识符），
		// 回退到各版本都有的 version —— 只含主版本，但总比采不到强。
		_, rows, err = db.Query(oracleProfileFactsLegacySQL)
	}
	if err != nil {
		logProfileSkip(row, "Oracle", "查询失败", err)
		return "", "", 0
	}
	if rows == nil || len(*rows) == 0 {
		logProfileSkip(row, "Oracle", "查询无返回行", nil)
		return "", "", 0
	}
	item := (*rows)[0]
	version := normalizeDbVersion(toString(oracleProfileValue(item, "db_version")))
	role := "master"
	if strings.Contains(strings.ToUpper(toString(oracleProfileValue(item, "db_role"))), "STANDBY") {
		role = "slave"
	}
	return version, role, int64(toFloat64(oracleProfileValue(item, "uptime_seconds")))
}

// oracleProfileValue 按列名取值，先精确匹配、再忽略大小写兜底。
// Oracle 的列名大小写取决于别名有没有加引号，这里不假设驱动一定给了哪种。
func oracleProfileValue(item map[string]any, name string) any {
	if value, ok := item[name]; ok {
		return value
	}
	for key, value := range item {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return nil
}

// collectSQLServerProfileFacts SQL Server 画像事实。
//   - 版本：SERVERPROPERTY('ProductVersion')，任意登录可查，无需 VIEW SERVER STATE；
//   - 运行时长：sys.dm_os_sys_info.sqlserver_start_time，无权限时失败可容忍；
//   - 角色：AlwaysOn 可用组拓扑探测复杂，不猜 —— 返回空串保持原值。
//
// 连接方式与 collectSQLServerSamples 一致（sqlserverdsn 统一处理老版本 TLS 握手）。
func collectSQLServerProfileFacts(ctx context.Context, row monitorInstanceRow, password string) (string, string, int64) {
	cfg, err := parseExtraConfig(row.ExtraConfig)
	if err != nil {
		logProfileSkip(row, "SQLServer", "扩展参数解析失败", err)
		return "", "", 0
	}
	dsn := sqlserverdsn.DSN(row.User, password, row.Hostname, row.Port, getStringConfig(cfg, "database", "master"))
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		logProfileSkip(row, "SQLServer", "连接串构建失败", err)
		return "", "", 0
	}
	defer db.Close()

	var version string
	if err := db.QueryRowContext(ctx,
		`SELECT CAST(SERVERPROPERTY('ProductVersion') AS nvarchar(128)) AS db_version`).Scan(&version); err != nil {
		logProfileSkip(row, "SQLServer", "查询版本失败", err)
		return "", "", 0
	}

	var uptime int64
	_ = db.QueryRowContext(ctx,
		`SELECT CAST(DATEDIFF(second, sqlserver_start_time, GETDATE()) AS bigint) AS uptime_seconds
		 FROM sys.dm_os_sys_info`).Scan(&uptime)

	return normalizeDbVersion(version), "", uptime
}

// logProfileSkip 记录引擎侧画像采集的跳过原因。
// 采集失败原本完全静默，导致「版本一直是空」只能在库里反复推断 ——
// 这里补上可观测性：只打实例标识与原因，绝不打密码。
func logProfileSkip(row monitorInstanceRow, engine, reason string, err error) {
	if global.App == nil || global.App.Log == nil {
		return
	}
	if err != nil {
		global.App.Log.Warnf("[monitor] %s 画像采集跳过 instance=%s addr=%s:%d reason=%s err=%v",
			engine, row.InstanceID, row.Hostname, row.Port, reason, err)
		return
	}
	global.App.Log.Warnf("[monitor] %s 画像采集跳过 instance=%s addr=%s:%d reason=%s",
		engine, row.InstanceID, row.Hostname, row.Port, reason)
}

// normalizeDbVersion 把各引擎五花八门的版本串归一化成可展示的短版本。
//   - MySQL  "8.0.34-log"              → "8.0.34"
//   - TiDB   "8.0.11-TiDB-v8.5.1"      → "8.5.1"（取 -v 尾段的真实 TiDB 版本，
//     前导段只是 MySQL 兼容版本号，展示它会让用户误以为是 MySQL 8.0.11）
//   - PG     "15.3 (Debian ...)"       → "15.3"
//   - PG     "PostgreSQL 15.3 on x86_64..." → "15.3"
//
// 版本串只保留版本号本身，不带引擎名：展示处一律拼「db_type + 空格 + db_version」，
// 再拼一次 "-TiDB" 就会出现「TiDB 8.0.11-TiDB」这种重复。
//
// 取不到前导版本号时原样截断返回，不要返回空串把"有值"变成"没值"。
func normalizeDbVersion(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	// 驱动把 NULL 列格式化成字符串哨兵时（fmt.Sprintf("%v", nil) → "<nil>"），
	// 必须当成"没取到"。否则它会绕过下面的版本号解析、原样写进 db_version，
	// 前端拼成「Oracle <nil>」——这正是本次问题的表象。这层与引擎无关。
	switch strings.ToLower(text) {
	case "<nil>", "<null>", "nil", "null", "none":
		return ""
	}
	fields := strings.Fields(text)
	head := fields[0]
	if strings.EqualFold(head, "PostgreSQL") && len(fields) > 1 {
		head = fields[1]
	}
	base := leadingNumeric(head)
	if base == "" {
		return truncateText(text, 64)
	}
	if strings.Contains(strings.ToLower(text), "tidb") {
		// TiDB 的 VERSION() 形如 "8.0.11-TiDB-v8.5.1"：前导段是 MySQL 兼容版本，
		// 用户真正关心的 TiDB 版本在 "-v" 尾段，取尾段展示（如 "8.5.1"）。
		// 尾段解析不出（老版本无 -v 后缀，如 "8.0.11-TiDB"）说明前导段本身就是
		// TiDB 版本，直接用前导段，绝不返回空串。
		if idx := strings.LastIndex(strings.ToLower(text), "-tidb-v"); idx >= 0 {
			if tail := leadingNumeric(text[idx+len("-tidb-v"):]); tail != "" {
				base = tail
			}
		}
	}
	return truncateText(base, 64)
}

// leadingNumeric 取字符串前导的数字/点段（"8.5.1-rel" → "8.5.1"，"x.y" → ""）。
func leadingNumeric(s string) string {
	end := 0
	for end < len(s) && (s[end] == '.' || (s[end] >= '0' && s[end] <= '9')) {
		end++
	}
	return strings.TrimRight(s[:end], ".")
}

// ---------------------------------------------------------------------------
// 主机侧：操作系统 / 内核 / CPU / 内存 / 磁盘
// ---------------------------------------------------------------------------

type hostProfileFacts struct {
	OsName   string
	OsKernel string
	CpuCores int
	MemoryGB float64
	DiskGB   float64
}

// collectHostProfileFacts 经 Prometheus 取主机静态事实。
// 未配置 Prometheus 或实例关闭了资源采集时返回零值 —— 主机规格确实不可得，
// 交给 ProfileValue 的 available=false + reason 去解释。
func collectHostProfileFacts(
	ctx context.Context,
	client *prometheusClient,
	promStatus PrometheusStatus,
	row monitorInstanceRow,
) hostProfileFacts {
	facts := hostProfileFacts{}
	if client == nil || row.ResourceSourceSetting == "none" || !promStatus.Configured || !promStatus.Available {
		return facts
	}
	target := strings.TrimSpace(row.PrometheusTarget)
	if target == "" {
		target = row.Hostname
	}
	if target == "" {
		return facts
	}
	filter := fmt.Sprintf(`instance=~"^%s(?::[0-9]+)?$"`, prometheusInstancePattern(target))

	// 操作系统与内核：node_uname_info 的 sysname/release 标签。
	// 新版本 exporter 另有 node_os_info 带 pretty_name（如 "CentOS Linux 7 (Core)"），
	// 优先取它，取不到再落回 sysname。
	if labels, _, ok, err := client.queryFirstSeries(ctx, fmt.Sprintf(`node_os_info{%s}`, filter)); err == nil && ok {
		if pretty := strings.TrimSpace(labels["pretty_name"]); pretty != "" {
			facts.OsName = pretty
		}
	}
	if labels, _, ok, err := client.queryFirstSeries(ctx, fmt.Sprintf(`node_uname_info{%s}`, filter)); err == nil && ok {
		if facts.OsName == "" {
			facts.OsName = strings.TrimSpace(labels["sysname"])
		}
		facts.OsKernel = strings.TrimSpace(labels["release"])
	}

	// CPU 核数：按 cpu 维度去重后计数，逻辑核口径与 load 对比一致。
	if value, ok, err := client.query(ctx, fmt.Sprintf(`count(count by (cpu) (node_cpu_seconds_total{%s}))`, filter)); err == nil && ok && value > 0 {
		facts.CpuCores = int(value)
	}
	// 内存 / 磁盘：原始单位是字节，换算成 GB。
	if value, ok, err := client.query(ctx, fmt.Sprintf(`max(node_memory_MemTotal_bytes{%s})`, filter)); err == nil && ok && value > 0 {
		facts.MemoryGB = bytesToGB(value)
	}
	// 先按设备去重再求和：同一块盘挂多个目录时不会被重复计入。
	// 文件系统集合与「磁盘使用率 / 使用量」保持一致（排除伪文件系统与 /boot 启动分区），
	// 这样「容量 - 已用」才等于该集合的可用空间。
	if value, ok, err := client.query(ctx, fmt.Sprintf(
		`sum(max by (device) (node_filesystem_size_bytes{%s, fstype!~"tmpfs|overlay|squashfs|ramfs|nsfs|devtmpfs", mountpoint!~"/boot.*|/run.*|/sys.*|/dev.*"}))`, filter)); err == nil && ok && value > 0 {
		facts.DiskGB = bytesToGB(value)
	}
	return facts
}

func bytesToGB(value float64) float64 {
	gb := value / 1024 / 1024 / 1024
	if gb < 10 {
		return float64(int(gb*100+0.5)) / 100
	}
	return float64(int(gb*10+0.5)) / 10
}

// ---------------------------------------------------------------------------
// 网络侧：TCP 探测耗时
// ---------------------------------------------------------------------------

// probeTCPLatencyMs 真实拨号一次取握手耗时。
// 不用采集总耗时顶替：那是"连上 + 跑完若干查询"的时间，混在一起会让探测耗时
// 随指标数量漂移，失去"网络快不快"的判读价值。
func probeTCPLatencyMs(row monitorInstanceRow) int {
	host := strings.TrimSpace(row.Hostname)
	port := row.Port
	if host == "" || port <= 0 {
		return 0
	}
	started := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 3*time.Second)
	if err != nil {
		return 0
	}
	defer conn.Close()
	return int(time.Since(started).Milliseconds())
}
