package services

// ============================================================================
// 实例角色与「指标的角色适用性」
//
// 背景：PostgreSQL 有一批指标只在特定拓扑角色下才存在 ——
// 「备库 APPLY 延迟 / 备库发送延迟」依赖 pg_stat_replication（仅主库有内容），
// 「逻辑复制延迟 / 复制槽堆积」依赖 pg_replication_slots（单机通常没有）。
// 单机实例上这些指标是**结构性不存在**，而不是"采不到"。
//
// 因此必须严格区分两种状态：
//
//   ┌── 结构性不适用（本文件的职责）──┐   ┌── 采不到（既有 quality 机制）──┐
//   │ 该角色下不存在这条指标          │   │ 指标存在，但查询失败/无值      │
//   │ → 不采集、不进告警统计，        │   │ → 保留卡片，显示"—"+原因，     │
//   │   明细页保留条目并标注原因      │   │   可按需纳入异常提示          │
//   │   （早期版本是整行隐藏，导致    │   │                                │
//   │    "模板有、明细没有"像漏配）   │   │                                │
//   └─────────────────────────────────┘   └────────────────────────────────┘
//
// 举例：单机实例不应显示"备库 APPLY 延迟 无数据"，它会让人怀疑是不是复制挂了；
// 而主库上写了的复制延迟查不出来，必须显示"采集失败：pg_stat_replication 不可用"。
//
// 设计原则：
//  1. 单一事实来源：适用性只在 roleScopedMetrics 里声明一次，
//     采集、展示目录、告警三条链路都从这里消费，避免各写一套导致口径漂移；
//  2. 未知角色一律 fail-open：角色没探测出来时绝不裁剪任何指标。
//     宁可多展示一个"无数据"，也不能因为识别不了角色就把真实数据藏起来；
//  3. 按引擎隔离：目前只有 PostgreSQL 有受拓扑约束的指标，其它引擎不受影响。
// ============================================================================

import (
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/global"
)

// 实例架构角色取值，与 insight_instances.instance_role 保持一致
// （该字段建表时就留了 master/slave/standalone 三个取值）。
const (
	// RoleMaster 主库：有下游备库或复制槽的写入节点。
	RoleMaster = "master"
	// RoleSlave 备库：处于恢复中（pg_is_in_recovery() = true）。
	RoleSlave = "slave"
	// RoleStandalone 单机：不参与流复制拓扑，既无下游备库也无复制槽。
	RoleStandalone = "standalone"
	// RoleCascade 级联备库：自身在恢复中，同时还有下游。
	// 当前探测链路尚不产出该值，先留出位置，避免将来到处改常量。
	RoleCascade = "cascade"
	// ReplicaCountUnknown 表示"无权查看下游数量"，该信号不可信。
	ReplicaCountUnknown = -1
)

// roleScopedMetric 一条「受实例拓扑角色约束」的指标声明。
type roleScopedMetric struct {
	MetricKey string
	// Applicable 适用场景取值集合。命中即表示"该角色下这条指标存在"。
	Applicable map[string]bool
	// Note 不适用的原因，用于自证与排障（不面向终端用户）。
	Note string
}

// roleScopedMetrics 角色适用性矩阵：按小写引擎标识索引。
//
// 扩展方式（新增角色或新增受限指标都只改这里）：
//   - 新增受限指标：往对应引擎的切片里追加一条 roleScopedMetric；
//   - 新增角色：在 Applicable 里加入新角色常量，并在 normalizeInstanceRole 注册别名。
//
// PostgreSQL 四项口径说明：
//   - eng.replay_lag_seconds / eng.sent_lag_bytes：数据来自 pg_stat_replication，
//     该视图只有主库上有内容（备库除非是级联的上游节点），因此只在 master 适用；
//     级联备库（RoleCascade）同样是上游，届时把 cascade 加入 Applicable 即可。
//   - eng.logical_rep_lag_bytes / eng.repl_slot_lag_bytes：数据来自 pg_replication_slots，
//     单机没有复制拓扑时不会有槽；从库若无级联订阅也不会有。
//
// kingbase 与 postgresql 共用同一份矩阵：金仓兼容 PG 协议，角色探测与
// 复制视图完全一致（inferPostgresRole 直接复用），适用性没有差异。
var roleScopedMetrics = map[string][]roleScopedMetric{
	"postgresql": postgresRoleScopedMetrics,
	"kingbase":   postgresRoleScopedMetrics,
}

var postgresRoleScopedMetrics = []roleScopedMetric{
	{
		MetricKey:  MetricEngReplayLagSeconds,
		Applicable: map[string]bool{RoleMaster: true},
		Note:       "依赖 pg_stat_replication，仅主库视角存在",
	},
	{
		MetricKey:  MetricEngSentLagBytes,
		Applicable: map[string]bool{RoleMaster: true},
		Note:       "依赖 pg_stat_replication，仅主库视角存在",
	},
	{
		MetricKey:  MetricEngLogicalRepLagBytes,
		Applicable: map[string]bool{RoleMaster: true},
		Note:       "依赖 pg_replication_slots(logical)，单机无复制槽时不存在",
	},
	{
		MetricKey:  MetricEngReplSlotLag,
		Applicable: map[string]bool{RoleMaster: true},
		Note:       "依赖 pg_replication_slots，单机无复制槽时不存在",
	},
	{
		MetricKey:  MetricReplLagSeconds,
		Applicable: map[string]bool{RoleSlave: true, RoleCascade: true},
		Note:       "pg_last_xact_replay_timestamp 仅在备库（含级联备库）上有值，主库/单机无数据",
	},
}

// roleInapplicableReason 生成面向运维的「该指标在当前角色下不适用」说明，
// 由监控明细页直接显示在卡片底部。
//
// 口径：说清"当前是什么角色 + 为什么不适用"，而不是干巴巴一句"无数据"——
// 否则运维分不清是复制真的挂了，还是这台机器本来就没有复制拓扑。
func roleInapplicableReason(engineKey, metricKey, role string) string {
	label := instanceRoleLabel(normalizeInstanceRole(role))
	if label == "" {
		label = "当前拓扑"
	}
	base := fmt.Sprintf("当前实例角色为「%s」，该指标结构性不存在", label)
	if scoped := roleScopedMetricOf(engineKey, metricKey); scoped != nil && scoped.Note != "" {
		return base + "：" + scoped.Note
	}
	return base + "，故不采集也不产生告警"
}

// normalizeInstanceRole 把各种写法收敛到标准角色取值；识别不了返回空串。
//
// 空串是"未知"，调用方必须按 fail-open 处理 —— 这是本文件最重要的一条纪律。
func normalizeInstanceRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleMaster, "primary", "source", "主库":
		return RoleMaster
	case RoleSlave, "standby", "replica", "secondary", "从库", "备库":
		return RoleSlave
	case RoleStandalone, "single", "none", "单机":
		return RoleStandalone
	case RoleCascade, "cascade-standby", "级联备库":
		return RoleCascade
	default:
		return ""
	}
}

// isRoleScopedMetric 该引擎下的这条指标是否受角色约束。
func isRoleScopedMetric(engineKey, metricKey string) bool {
	return roleScopedMetricOf(engineKey, metricKey) != nil
}

func roleScopedMetricOf(engineKey, metricKey string) *roleScopedMetric {
	list, ok := roleScopedMetrics[strings.ToLower(strings.TrimSpace(engineKey))]
	if !ok {
		return nil
	}
	for i := range list {
		if strings.TrimSpace(list[i].MetricKey) == strings.TrimSpace(metricKey) {
			return &list[i]
		}
	}
	return nil
}

// metricApplicableToRole 判定某指标在当前实例角色下是否应当存在。
//
// 返回值策略：
//   - 指标不受角色约束 → true（普通指标完全不受本机制影响）；
//   - 受约束且角色已知 → 查矩阵；
//   - 受约束但角色未知（空串/非预期值）→ true（fail-open，保持既有行为）。
func metricApplicableToRole(engineKey, metricKey, role string) bool {
	scoped := roleScopedMetricOf(engineKey, metricKey)
	if scoped == nil {
		return true
	}
	normalized := normalizeInstanceRole(role)
	if normalized == "" {
		return true
	}
	return scoped.Applicable[normalized]
}

// filterRoleInapplicableKeys 剔除本实例角色下结构性不存在的指标键。
//
// 这是「不渲染 / 不采集 / 不告警」的唯一执行点，三个调用方须共用它：
// 采集门控（resolveInstanceMetricKeys）、明细展示目录（MonitorDisplayLayoutFor）、
// 以及卡片补齐（appendMissingMetricViews）。
//
// 入参 keys 为 nil 表示"全采"兜底，此时原样返回，由 caller 保证字典全集已显式给出。
func filterRoleInapplicableKeys(engineKey, role string, keys []string) []string {
	if keys == nil {
		return keys
	}
	filtered := make([]string, 0, len(keys))
	for _, key := range keys {
		if !metricApplicableToRole(engineKey, key, role) {
			continue
		}
		filtered = append(filtered, key)
	}
	return filtered
}

// ---------------------------------------------------------------------------
// PostgreSQL 角色推断
// ---------------------------------------------------------------------------

// pgRoleSignals PG 角色推断所需的原始信号，全部可内联进主查询一次往返取回。
type pgRoleSignals struct {
	// InRecovery 对应 pg_is_in_recovery()：无任何权限门槛，判定真伪的唯一权威依据。
	InRecovery bool
	// ReplicaCount pg_stat_replication 行数；不可信时填 ReplicaCountUnknown。
	// 该视图受 security-restricted 约束：无 pg_read_all_stats / pg_monitor 时返回零行
	// 而非报错，所以"看不见"与"没有下游"在结果上无法区分 —— 必须先验有权再采信。
	ReplicaCount int
	// SlotCount pg_replication_slots 行数，用于识别"有复制拓扑但下游暂时断开"的主库。
	SlotCount int
}

// inferPostgresRole 由原始信号推断实例角色。返回空串表示无法判定。
//
// 判定表：
//
//	in_recovery=true          → slave
//	存在复制槽                → master（有复制拓扑）
//	下游数 > 0（可信）        → master
//	下游数 = 0（可信）且无槽  → standalone
//	下游数不可信且无槽        → ""（未知，调用方按 fail-open 处理）
//
// "存在复制槽"为什么要单独前置而不是与下游数放在一起：
// pg_stat_replication 需要 pg_read_all_stats / pg_monitor，而 pg_replication_slots
// 是普通 catalog 视图，可读门槛低得多。账号只有 USAGE 时前者必然看不见，
// 若此时直接放弃推断，绝大多数单机实例就永远识别不出来。
func inferPostgresRole(s pgRoleSignals) string {
	if s.InRecovery {
		return RoleSlave
	}
	if s.SlotCount > 0 {
		return RoleMaster
	}
	if s.ReplicaCount == ReplicaCountUnknown {
		// 无权查看下游数量且没有复制槽：分不清"单机"和"看不见"。
		// 此时若保守判单机，会把真主库的复制延迟藏掉 —— 那比多显示一个"无数据"严重得多。
		return ""
	}
	if s.ReplicaCount > 0 {
		return RoleMaster
	}
	return RoleStandalone
}

// syncInstanceRole 把采集侧探测到的角色写回 insight_instances.instance_role。
//
// 为什么采集侧要自己写，而不只依赖画像采集：
// 画像采集（monitor_profile_collect.go）按 profileFactsInterval = 1 小时节流，
// 拓扑变化后最长要一小时才反映到展示与采集范围上；采集侧每轮都能看到真实状态，
// 就地写回可以让"角色适用性"与"当前拓扑"保持在同一个数量级的时间误差内。
//
// 三条纪律：
//  1. 只在判定值与库值不一致时写，避免每 10 秒一次无谓 UPDATE；
//  2. 判定为未知（空串）时什么都不做 —— 探测不出角色不代表角色变了，
//     写空值会把历史上已认定的角色冲掉；
//  3. 写失败只记录日志，绝不影响本轮指标采集。
func syncInstanceRole(row monitorInstanceRow, role string) {
	if role == "" || row.InstanceID == "" {
		return
	}
	if normalizeInstanceRole(row.InstanceRole) == role {
		return
	}
	if global.App.DB == nil {
		return
	}
	if err := global.App.DB.Table("insight_instances").
		Where("instance_id = ?", row.InstanceID).
		Update("instance_role", role).Error; err != nil {
		if global.App.Log != nil {
			global.App.Log.Warnf("[monitor] 实例 %s 角色回写失败（%s → %s）：%v",
				row.InstanceID, row.InstanceRole, role, err)
		}
		return
	}
	if global.App.Log != nil {
		global.App.Log.Infof("[monitor] 实例 %s 角色变更：%q → %q（本角色下不适用的复制类指标将不再展示与采集）",
			row.InstanceID, row.InstanceRole, role)
	}
}
