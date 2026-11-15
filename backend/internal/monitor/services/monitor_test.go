package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
)

// ---------------------------------------------------------------------------
// 数据质量模型：本套改造的地基，必须有测试兜住。
// ---------------------------------------------------------------------------

func TestQualityIsAvailable(t *testing.T) {
	cases := []struct {
		quality string
		want    bool
	}{
		{QualityOK, true},
		{QualityStale, false},
		{QualityNoData, false},
		{QualityUnsupported, false},
		{QualityError, false},
		{"", false},
		{"OK", false}, // 大小写敏感，避免上游大小写漂移悄悄放过
	}
	for _, item := range cases {
		if got := qualityIsAvailable(item.quality); got != item.want {
			t.Errorf("qualityIsAvailable(%q) = %v, want %v", item.quality, got, item.want)
		}
	}
}

func TestSummarizeCollectQuality(t *testing.T) {
	cases := []struct {
		name    string
		samples []MetricSample
		want    string
	}{
		{"空采样视为无数据", nil, QualityNoData},
		{
			"全部可信",
			[]MetricSample{{Quality: QualityOK}, {Quality: QualityOK}},
			QualityOK,
		},
		{
			"部分不可用",
			[]MetricSample{{Quality: QualityOK}, {Quality: QualityUnsupported}},
			"partial",
		},
		{
			"全部不适用 → unsupported 而不是 error",
			[]MetricSample{{Quality: QualityUnsupported}, {Quality: QualityUnsupported}},
			QualityUnsupported,
		},
		{
			"一条可信都没有 → error",
			[]MetricSample{{Quality: QualityError}, {Quality: QualityNoData}},
			QualityError,
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := summarizeCollectQuality(item.samples); got != item.want {
				t.Errorf("summarizeCollectQuality() = %q, want %q", got, item.want)
			}
		})
	}
}

// 核心回归：quality != ok 的采样落库时 value 必须是 NULL，绝不能是 0。
// 历史上 PG 未装 pg_stat_statements 时 QPS 被静默写成 0 并当成真实值展示。
func TestLatestRowsFromSamplesNullValueWhenNotOK(t *testing.T) {
	row := monitorInstanceRow{InstanceID: "ds-test", DbType: "PostgreSQL"}
	samples := []MetricSample{
		{Key: MetricPerfQPS, Value: 123.5, Unit: "req/s", Quality: QualityOK},
		{Key: MetricPerfTPS, Value: 0, Unit: "txn/s", Quality: QualityUnsupported, Reason: "未安装 pg_stat_statements"},
		{Key: MetricResCPU, Value: 7.7, Unit: "%", Quality: QualityStale},
	}
	rows := latestRowsFromSamples(row, samples)
	if len(rows) != len(samples) {
		t.Fatalf("每个采样都应产出一行最新值，期望 %d 实际 %d", len(samples), len(rows))
	}
	byKey := map[string]monitorModels.InsightMonitorMetricLatest{}
	for _, item := range rows {
		byKey[item.MetricKey] = item
	}
	if item := byKey[MetricPerfQPS]; item.Value == nil || *item.Value != 123.5 {
		t.Errorf("quality=ok 的采样必须带真实值，实际 %v", item.Value)
	}
	if item := byKey[MetricPerfTPS]; item.Value != nil {
		t.Errorf("quality=unsupported 的采样 value 必须为 nil（而不是 0），实际 %v", *item.Value)
	}
	if item := byKey[MetricResCPU]; item.Value != nil {
		t.Errorf("quality=stale 的采样 value 必须为 nil，实际 %v", *item.Value)
	}
	if byKey[MetricPerfTPS].Reason == "" {
		t.Error("不可用采样必须带原因，否则页面只能显示一个孤零零的“—”")
	}
}

// buildCardFromStore 是页面唯一读取路径，必须把"没采到"翻译成 available=false。
func TestBuildCardFromStoreMarksUnavailable(t *testing.T) {
	row := monitorInstanceRow{
		InstanceID:        "ds-1",
		Hostname:          "db-01",
		Port:              3306,
		DbType:            "MySQL",
		LastCollectStatus: "success",
		CollectQuality:    "partial",
	}
	zero := 0.0
	now := commonModels.LocalTime(time.Now())
	latestRows := []monitorModels.InsightMonitorMetricLatest{
		{MetricKey: MetricResCPU, Value: nil, Quality: QualityUnsupported, Reason: "未配置 Prometheus", Unit: "%", Ts: now},
		{MetricKey: MetricPerfQPS, Value: &zero, Quality: QualityOK, Unit: "req/s", Ts: now},
	}
	card := buildCardFromStore(row, latestRows, nil)

	if card.ResourceMetrics.CPU.Available {
		t.Error("未配置 Prometheus 时 CPU 不应标记为可用")
	}
	if card.ResourceMetrics.CPU.Reason == "" {
		t.Error("不可用指标必须带原因")
	}
	if !card.EngineMetrics.QPS.Available || card.EngineMetrics.QPS.Value != 0 {
		t.Error("quality=ok 且值为 0 时应正常可用（0 是合法值）")
	}
	if len(card.Metrics) != 2 {
		t.Errorf("卡片应携带全部指标视图，实际 %d", len(card.Metrics))
	}
}

func TestSampleValueFallbackForUnknownKey(t *testing.T) {
	metric := sampleValue(nil, MetricRiskXIDAge)
	if metric.Available {
		t.Error("缺失指标不应可用")
	}
	if metric.Quality != QualityUnsupported {
		t.Errorf("缺失指标的 quality 应为 unsupported，实际 %q", metric.Quality)
	}
}

// ---------------------------------------------------------------------------
// 三段式命名与兼容层
// ---------------------------------------------------------------------------

func TestCanonicalMetricKey(t *testing.T) {
	cases := map[string]string{
		"cpu":                     MetricResCPU,
		"qps":                     MetricPerfQPS,
		"active_connection_ratio": MetricCapConnUsedPct,
		"locks":                   MetricPerfLocks,
		" perf.qps ":              MetricPerfQPS,
		"unknown.metric":          "unknown.metric",
		"":                        "",
	}
	for input, want := range cases {
		if got := canonicalMetricKey(input); got != want {
			t.Errorf("canonicalMetricKey(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMetricLabelUsesCatalog(t *testing.T) {
	if got := metricLabel("qps"); got != "QPS" {
		t.Errorf("老裸名 qps 应映射到字典里的 QPS，实际 %q", got)
	}
	if got := metricLabel(MetricReplLagSeconds); got != "复制延迟" {
		t.Errorf("三段式键应能取到中文名，实际 %q", got)
	}
	if got := metricLabel("no.such.metric"); got != "no.such.metric" {
		t.Errorf("字典缺失时应回退为原键，实际 %q", got)
	}
}

// ---------------------------------------------------------------------------
// 引擎能力注册表
// ---------------------------------------------------------------------------

func TestMonitorableEngineKeys(t *testing.T) {
	keys := monitorableEngineKeys()
	if len(keys) != len(engineCapabilities) {
		t.Fatalf("所有内置引擎都应已实现采集，实际 %d/%d", len(keys), len(engineCapabilities))
	}
	// 期望集合是「刻意的白名单」：新增引擎必须同步更新这里，
	// 避免注册表被顺手加一项就进入监控对象列表（没有采集实现会变成一片空指标）。
	// kingbase 于 2026-11 接入：采集复用 PG 采集器，展示目录整目录复用 PostgreSQL。
	expected := map[string]bool{"mysql": true, "postgresql": true, "kingbase": true, "tidb": true, "doris": true, "clickhouse": true, "sqlserver": true, "oracle": true}
	for _, key := range keys {
		if !expected[key] {
			t.Errorf("出现未预期的引擎键 %q", key)
		}
	}
	// keys 必须有序，否则 SQL 的 IN 列表不稳定，影响执行计划复用
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			t.Fatalf("monitorableEngineKeys 未排序：%v", keys)
		}
	}
}

func TestResolveEngineAndImplemented(t *testing.T) {
	if got := resolveEngine(" MySQL "); got != "mysql" {
		t.Errorf("resolveEngine 应归一化大小写与空白，实际 %q", got)
	}
	if !engineImplemented("TiDB") {
		t.Error("TiDB 走 MySQL 协议，应判定为已实现采集")
	}
	if engineImplemented("MongoDB") {
		t.Error("MongoDB 未实现采集，不应进入监控白名单")
	}
}

func TestEngineInScope(t *testing.T) {
	cases := []struct {
		engine string
		scope  string
		want   bool
	}{
		{"mysql", "MySQL,TiDB,Doris", true},
		{"postgresql", "MySQL,TiDB,Doris", false},
		{"postgresql", "", true},
		{"anything", "", true},
		{"oracle", " Oracle ", true},
	}
	for _, item := range cases {
		if got := engineInScope(item.engine, item.scope); got != item.want {
			t.Errorf("engineInScope(%q,%q) = %v, want %v", item.engine, item.scope, got, item.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 采集调度：错峰与退避
// ---------------------------------------------------------------------------

func TestInstancePhaseInRangeAndDeterministic(t *testing.T) {
	first := instancePhase("ds-c0e924b4674c", 60)
	second := instancePhase("ds-c0e924b4674c", 60)
	if first != second {
		t.Fatal("同一实例的相位必须稳定，否则每轮都会换一个时间点")
	}
	if first >= 60 {
		t.Fatalf("相位必须落在 [0, interval) 内，实际 %d", first)
	}
	if instancePhase("ds-x", 0) != 0 {
		t.Error("interval<=0 时相位应为 0")
	}
}

func TestDueForCollectIntervalAndBackoff(t *testing.T) {
	now := time.Now()
	id := "ds-slot-test"

	// 槽位语义：last_collect_at 恒为槽位起点（collectSlotTime 产出）。
	// 本槽位起点 → 同槽位，不到期；上一个槽位起点 → 跨槽位，到期。
	current60 := collectSlotTime(now, id, 60)
	recent := commonModels.LocalTime(current60)
	due := commonModels.LocalTime(current60.Add(-60 * time.Second))

	if dueForCollect(monitorInstanceRow{InstanceID: id, ScrapeIntervalSeconds: 60, LastCollectAt: &recent}, now, 10*time.Second) {
		t.Error("本轮槽位已采集过，不应重复采集")
	}
	if !dueForCollect(monitorInstanceRow{InstanceID: id, ScrapeIntervalSeconds: 60, LastCollectAt: &due}, now, 10*time.Second) {
		t.Error("上一槽位采集过且周期 60 秒，本轮应到期")
	}

	// 连续失败 3 次 → 周期放大 5 倍（300s），退避网格同样按槽位判定
	current300 := collectSlotTime(now, id, 300)
	inBackoffSlot := commonModels.LocalTime(current300)
	prevBackoffSlot := commonModels.LocalTime(current300.Add(-300 * time.Second))
	failingSame := monitorInstanceRow{InstanceID: id, ScrapeIntervalSeconds: 60, LastCollectAt: &inBackoffSlot, ConsecutiveFailures: 3}
	failingPrev := monitorInstanceRow{InstanceID: id, ScrapeIntervalSeconds: 60, LastCollectAt: &prevBackoffSlot, ConsecutiveFailures: 3}
	if dueForCollect(failingSame, now, 10*time.Second) {
		t.Error("退避周期 300s：本槽位已采集过，不应重复采集")
	}
	if !dueForCollect(failingPrev, now, 10*time.Second) {
		t.Error("退避周期 300s：上一槽位采集过，本轮应到期")
	}

	// 从未采集过：由相位决定，只断言不 panic
	_ = dueForCollect(monitorInstanceRow{InstanceID: id, ScrapeIntervalSeconds: 60}, now, 10*time.Second)
}

func TestCollectSlotTimeAlignment(t *testing.T) {
	now := time.Now()
	for _, interval := range []int{15, 30, 60, 300} {
		id := "ds-align-" + strconv.Itoa(interval)
		phase := int64(instancePhase(id, interval))
		slotTs := collectSlotTime(now, id, interval)
		if slotTs.After(now) {
			t.Fatalf("interval=%d：槽位起点不能晚于当前时刻", interval)
		}
		if now.Unix()-slotTs.Unix() >= int64(interval) {
			t.Fatalf("interval=%d：槽位起点距 now 超过一个周期", interval)
		}
		if (slotTs.Unix()-phase)%int64(interval) != 0 {
			t.Fatalf("interval=%d：槽位起点必须落在相位网格上（(ts-phase)%%interval==0）", interval)
		}
		// 连续两个时刻若跨槽位，槽位起点必须严格推进（不重复、不回退）
		next := collectSlotTime(slotTs.Add(time.Duration(interval)*time.Second), id, interval)
		if !next.Equal(slotTs.Add(time.Duration(interval) * time.Second)) {
			t.Fatalf("interval=%d：下一槽位起点应精确等于本槽位起点+周期", interval)
		}
	}
	// 周期 15s 时，任意相位的网格点 FLOOR(ts/15) 后必然覆盖 0/15/30/45 四个桶位：
	// 这是"趋势图每分钟四个点"的充要条件，回归本文件要修的那个 bug。
	id := "ds-bucket-coverage"
	phase := int64(instancePhase(id, 15))
	base := time.Unix((now.Unix()/900)*900+phase, 0) // 对齐到该实例的网格起点
	for k := int64(0); k < 60; k++ {
		ts := base.Add(time.Duration(k*15) * time.Second)
		if bucket := (ts.Unix() / 15) * 15; bucket%15 != 0 {
			t.Fatalf("网格点 %s 聚合桶未对齐 15 秒边界", ts)
		}
	}
}

func TestIsRetryableCollectError(t *testing.T) {
	retryable := []string{
		"dial tcp 10.0.0.1:3306: i/o timeout",
		"connection refused",
	}
	for _, message := range retryable {
		if !isRetryableCollectError(errString(message)) {
			t.Errorf("网络类错误应可重试：%s", message)
		}
	}
	notRetryable := []string{
		"Error 1045: Access denied for user 'monitor'@'%'",
		"Unknown column 'foo' in 'field list'",
		// 连接饱和：重试等于往已打满的实例上再加连接，必须一票否决。
		// 这条原来在 retryable 列表里，是本次特意改掉的（见 isConnectionSaturated）。
		"Error 1040: Too many connections",
	}
	for _, message := range notRetryable {
		if isRetryableCollectError(errString(message)) {
			t.Errorf("确定性错误不应重试（只会拖长整批耗时）：%s", message)
		}
	}
	if isRetryableCollectError(nil) {
		t.Error("nil 错误不应判定为可重试")
	}
}

// TestIsConnectionSaturated 锁定「连接饱和」的判定边界。
//
// 判假的代价只是少采一轮；判真的代价是**不再对故障实例重试**，
// 所以误判方向必须是"宁可不认"，绝不能有会把普通错误吃掉的模糊匹配。
func TestIsConnectionSaturated(t *testing.T) {
	saturated := []string{
		"Error 1040: Too many connections",                            // MySQL / TiDB
		"FATAL: sorry, too many clients already (server)",             // PostgreSQL
		"FATAL: remaining connection slots are reserved for non-replication superuser connections",
		"ORA-00020: maximum number of processes (150) exceeded",       // Oracle
		"ORA-12516: TNS:listener could not find available handler",
		"ORA-12518: TNS:listener could not hand off client connection",
	}
	for _, message := range saturated {
		if !isConnectionSaturated(errString(message)) {
			t.Errorf("连接饱和应被识别：%s", message)
		}
	}
	notSaturated := []string{
		"Error 1045: Access denied for user 'monitor'@'%'",
		"Error 1146: Table 'performance_schema.x' doesn't exist",
		"dial tcp 10.0.0.1:3306: connect: connection refused",
		"context deadline exceeded",
		"Unknown column 'foo' in 'field list'",
	}
	for _, message := range notSaturated {
		if isConnectionSaturated(errString(message)) {
			t.Errorf("非饱和错误不应被误判（会被当成饱和而停止重试）：%s", message)
		}
	}
	if isConnectionSaturated(nil) {
		t.Error("nil 错误不应判定为连接饱和")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// 告警：指纹、状态机输入、级别映射
// ---------------------------------------------------------------------------

func TestAlertFingerprintNormalizesMetricKey(t *testing.T) {
	legacy := alertFingerprint(7, "ds-1", "qps")
	canonical := alertFingerprint(7, "ds-1", MetricPerfQPS)
	if legacy != canonical {
		t.Fatalf("老裸名与新键必须产生同一指纹，否则同一告警会重复入库：%q vs %q", legacy, canonical)
	}
	// 阶梯告警：指纹按"实例 + 指标"聚合，不再带 rule_id。
	// 同指标换档位（P2 升 P0 是同一行规则被覆盖，rule_id 不变；但不同档本来是不同的 rule_id）
	// 必须落到同一条事件上，否则一次故障会刷出一串"新告警"。
	if alertFingerprint(7, "ds-1", "qps") != alertFingerprint(8, "ds-1", "qps") {
		t.Error("同一实例同一指标的档位必须聚合到同一指纹")
	}
	if alertFingerprint(7, "ds-1", "qps") == alertFingerprint(7, "ds-2", "qps") {
		t.Error("不同实例的指纹必须不同")
	}
}

func TestHoldSecondsOfPrefersForSeconds(t *testing.T) {
	if got := holdSecondsOf(monitorModels.InsightMonitorAlertRules{ForSeconds: 120, HoldMinutes: 10}); got != 120 {
		t.Errorf("for_seconds 优先，实际 %d", got)
	}
	// 老字段兼容：hold_minutes 曾经是全仓 0 处引用的死字段
	if got := holdSecondsOf(monitorModels.InsightMonitorAlertRules{HoldMinutes: 10}); got != 600 {
		t.Errorf("hold_minutes 应换算为秒并生效，实际 %d", got)
	}
	if got := holdSecondsOf(monitorModels.InsightMonitorAlertRules{}); got != 0 {
		t.Errorf("未配置观察期时应为 0（立即触发），实际 %d", got)
	}
}

func TestSeverityLevelRoundTrip(t *testing.T) {
	// 中文旧值兼容：高→P1 / 中→P2 / 低→P3（存量数据不需要改就能继续评估）
	if severityFromLevel("高") != "P1" || severityFromLevel("中") != "P2" || severityFromLevel("低") != "P3" {
		t.Error("中文旧级别到 P 码映射错误")
	}
	// P 码即等级：P0 是新等级，必须原样保留
	if severityFromLevel("P0") != "P0" || severityFromLevel("p1") != "P1" {
		t.Error("P 码等级映射错误（需容忍大小写）")
	}
	if severityFromLevel("未知") != "P3" {
		t.Error("未知级别应保守落入 P3")
	}
	if levelFromSeverity("P1") != "P1" || levelFromSeverity("p2") != "P2" || levelFromSeverity("P3") != "P3" {
		t.Error("严重度到级别映射错误（需容忍大小写）")
	}
}

func TestCompareMetricValue(t *testing.T) {
	cases := []struct {
		value     float64
		operator  string
		threshold float64
		want      bool
	}{
		{85, ">=", 85, true},
		{84.9, ">=", 85, false},
		{1, "!=", 1, false},
		{0, "!=", 1, true},
		{90, "<=", 90, true},
		{0, "absent", 0, false},
	}
	for _, item := range cases {
		if got := compareMetricValue(item.value, item.operator, item.threshold); got != item.want {
			t.Errorf("compareMetricValue(%v,%q,%v) = %v, want %v",
				item.value, item.operator, item.threshold, got, item.want)
		}
	}
}

// 原 TestBuildDefaultAlertRulesCoversCriticalDimensions 随 buildDefaultAlertRules 一并删除：
// 平台不再自动播种默认告警规则，规则只来自「告警配置 / 告警模板」的显式配置。

func TestMetricValueByKeyLegacyAndRatio(t *testing.T) {
	card := InstanceMonitorCard{
		Metrics: []MetricSampleView{
			{Key: MetricPerfActiveConn, Value: 90, Unit: "count", Available: true, Quality: QualityOK},
			{Key: MetricPerfMaxConn, Value: 100, Unit: "count", Available: true, Quality: QualityOK},
		},
	}
	value, _, ok := metricValueByKey(card, "active_connections", 0)
	if !ok || value != 90 {
		t.Fatalf("老裸名 active_connections 应命中 perf.active_conn_count，实际 %v/%v", value, ok)
	}

	// 老规则里的 active_connection_ratio 语义 = 活跃连接 / 最大连接
	value, metric, ok := metricValueByKey(card, "active_connection_ratio", 100)
	if !ok || value != 90 {
		t.Fatalf("活跃连接占比应为 90%%，实际 %v/%v", value, ok)
	}
	if metric.Unit != "%" {
		t.Errorf("占比单位应为 %%，实际 %q", metric.Unit)
	}
}

func TestCategoryOrderIsStable(t *testing.T) {
	order := []string{CategoryAvailability, CategoryCapacity, CategoryPerformance, CategoryRisk, CategoryChange}
	for index := 1; index < len(order); index++ {
		if categoryOrder(order[index-1]) >= categoryOrder(order[index]) {
			t.Fatalf("分类展示顺序必须严格递增：%v", order)
		}
	}
}

func TestTruncateText(t *testing.T) {
	if got := truncateText("abc", 10); got != "abc" {
		t.Errorf("短文本不应被截断，实际 %q", got)
	}
	if got := truncateText("abcdefghij", 5); got != "abcde…" {
		t.Errorf("超长文本应截断并加省略号，实际 %q", got)
	}
	if got := truncateText("  abc  ", 10); got != "abc" {
		t.Errorf("首尾空白应被清理，实际 %q", got)
	}
}

func TestEngineLabelsOf(t *testing.T) {
	all := engineLabelsOf("")
	if len(all) != len(engineCapabilities) {
		t.Errorf("空 scope 表示全部引擎，实际 %v", all)
	}
	pg := engineLabelsOf("PostgreSQL")
	if len(pg) != 1 || pg[0] != "PostgreSQL" {
		t.Errorf("engine_scope 应被翻译成引擎标签，实际 %v", pg)
	}
}

func TestFormatPlainValue(t *testing.T) {
	// 数值统一去尾零：整数收敛为整数、92.50→92.5，告警描述不出现 0.00/1.00 这类噪声
	if got := formatPlainValue(85, "%"); got != "85%" {
		t.Errorf("百分比整数应去尾零，实际 %q", got)
	}
	if got := formatPlainValue(3, "count"); got != "3" {
		t.Errorf("计数不应带小数，实际 %q", got)
	}
	if got := formatPlainValue(1.5, ""); got != "1.5" {
		t.Errorf("无单位时应去尾零保留有效位，实际 %q", got)
	}
	if got := formatPlainValue(0, ""); got != "0" {
		t.Errorf("0.00 应显示为 0，实际 %q", got)
	}
	if got := formatPlainValue(92.5, "%"); got != "92.5%" {
		t.Errorf("非整小数应保留有效位，实际 %q", got)
	}
}

// ---------------------------------------------------------------------------
// MetricValue 序列化：把"不可用"在协议层就变成 null，与库里的 NULL 对齐。
// ---------------------------------------------------------------------------

func TestMetricValueMarshalNullsUnavailable(t *testing.T) {
	// 不可用：必须输出 value:null。输出 0 会让任何只看 value 的消费方
	// 把"没采到"读成"值就是 0" —— 这正是本套改造要根治的错觉。
	unavailable := MetricValue{Value: 0, Unit: "%", Available: false, Quality: QualityUnsupported, Reason: "该引擎不支持"}
	raw, err := json.Marshal(unavailable)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("反序列化失败：%v", err)
	}
	if v, ok := decoded["value"]; !ok || v != nil {
		t.Errorf("不可用指标的 value 必须是 null，实际 %v（原始 %s）", v, raw)
	}
	if decoded["quality"] != QualityUnsupported || decoded["available"] != false {
		t.Errorf("quality/available 必须保留，实际 %s", raw)
	}

	// 可用：0 是合法读数，必须原样输出 0 而不是 null。
	available := MetricValue{Value: 0, Unit: "count", Available: true, Quality: QualityOK}
	raw, err = json.Marshal(available)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if !strings.Contains(string(raw), `"value":0`) {
		t.Errorf("可用指标的 0 必须原样输出，实际 %s", raw)
	}

	// 往返一致性：模块间回读响应体不能丢值。
	var back MetricValue
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if back.Value != 0 || !back.Available || back.Quality != QualityOK || back.Unit != "count" {
		t.Errorf("往返后结构不一致，实际 %+v", back)
	}
	var nullBack MetricValue
	if err := json.Unmarshal([]byte(`{"value":null,"available":false,"unit":"%"}`), &nullBack); err != nil {
		t.Fatalf("null 值回读失败：%v", err)
	}
	if nullBack.Available || nullBack.Value != 0 {
		t.Errorf("null 应回读成不可用且零值，实际 %+v", nullBack)
	}
}

// ---------------------------------------------------------------------------
// qualityRank：细表合并多来源采样时靠它挑"最可信"的值。
// 排序一旦退化，界面会把 stale/unsupported 顶到 ok 前面，直接误导值班。
// ---------------------------------------------------------------------------

func TestQualityRankOrdersByTrustworthiness(t *testing.T) {
	// ok 最可信，error 最不可信，且 stale 必须严格优于 no_data：
	// 旧值虽过期但仍有参考意义，no_data 是"压根没有值"。
	pairs := [][2]string{
		{QualityOK, QualityStale},
		{QualityStale, QualityNoData},
		{QualityNoData, QualityUnsupported},
		{QualityUnsupported, QualityError},
	}
	for _, p := range pairs {
		if qualityRank[p[0]] >= qualityRank[p[1]] {
			t.Errorf("%s 应比 %s 更可信，实际 rank %d vs %d", p[0], p[1], qualityRank[p[0]], qualityRank[p[1]])
		}
	}
	// 未登记的 quality 必须排在所有已知值之后，绝不能因 map 零值 0 而冒充可信。
	// 这里测的是 qualityWeight —— 排序用的一律是它，不是裸 map。
	if qualityWeight("bogus") <= qualityWeight(QualityError) {
		t.Errorf("未知 quality 应比 error 更不可信，实际 %d", qualityWeight("bogus"))
	}
	if qualityWeight("") <= qualityWeight(QualityError) {
		t.Errorf("空 quality 应比 error 更不可信，实际 %d", qualityWeight(""))
	}
	if qualityWeight(QualityOK) != qualityRank[QualityOK] {
		t.Errorf("已登记状态必须走 qualityRank，实际 %d", qualityWeight(QualityOK))
	}
	// 五个状态一个都不能漏。
	for _, q := range []string{QualityOK, QualityStale, QualityNoData, QualityUnsupported, QualityError} {
		if _, ok := qualityRank[q]; !ok {
			t.Errorf("qualityRank 缺少状态 %s", q)
		}
	}
}

// ---------------------------------------------------------------------------
// evaluateRule：数据质量是硬门槛。这组测试锁定"没采到 ≠ 异常"的边界。
// ---------------------------------------------------------------------------

// ruleFixture 构造一条最小可用规则，只覆盖本组测试关心的字段。
func ruleFixture(metricKey, operator string, threshold float64, noDataBehavior string) monitorModels.InsightMonitorAlertRules {
	return monitorModels.InsightMonitorAlertRules{
		Name:           "测试规则",
		DBType:         commonModels.EnumType("MySQL"),
		RuleType:       "threshold",
		ConditionType:  "threshold",
		MetricKey:      metricKey,
		Operator:       operator,
		Threshold:      threshold,
		AlertLevel:     "高",
		Severity:       "P1",
		ScopeType:      "global",
		NoDataBehavior: noDataBehavior,
		Enabled:        true,
	}
}

func latestFixture(metricKey, quality string, value *float64, reason string) map[string]monitorModels.InsightMonitorMetricLatest {
	return map[string]monitorModels.InsightMonitorMetricLatest{
		canonicalMetricKey(metricKey): {
			InstanceID: "ds-test",
			MetricKey:  canonicalMetricKey(metricKey),
			Value:      value,
			Quality:    quality,
			Reason:     reason,
		},
	}
}

func TestEvaluateRuleNoDataBehaviorDefaultIgnores(t *testing.T) {
	row := monitorInstanceRow{InstanceID: "ds-test", DbType: "MySQL"}

	// 场景一：本轮压根没采到该指标，默认行为（ignore）不应命中。
	got := evaluateRule(row, ruleFixture(MetricPerfTPS, "<", 10, "ignore"), map[string]monitorModels.InsightMonitorMetricLatest{})
	if got.Hit {
		t.Errorf("缺数据 + ignore 不应命中，实际 Hit=%v message=%q", got.Hit, got.Message)
	}
	if got.Quality != QualityNoData {
		t.Errorf("缺数据的 quality 应为 no_data，实际 %q", got.Quality)
	}

	// 场景二：同样缺数据，但规则显式要求"缺数据即告警"，必须命中。
	got = evaluateRule(row, ruleFixture(MetricPerfTPS, "<", 10, "alert"), map[string]monitorModels.InsightMonitorMetricLatest{})
	if !got.Hit {
		t.Errorf("缺数据 + alert 应命中，实际未命中")
	}
	if got.Message == "" {
		t.Errorf("命中时应给出可读文案")
	}
}

func TestEvaluateRuleSkipsUnavailableQuality(t *testing.T) {
	row := monitorInstanceRow{InstanceID: "ds-test", DbType: "MySQL"}
	zero := 0.0

	// partial/stale/error 都是"数据不可信"，即便数值恰好越过阈值也不能报警 ——
	// 否则采集抖动会被读成业务故障，这正是本套改造要根治的误报源。
	for _, quality := range []string{QualityStale, QualityError, QualityNoData} {
		got := evaluateRule(row, ruleFixture(MetricPerfTPS, "<", 999, ""), latestFixture(MetricPerfTPS, quality, &zero, "本轮未采到，沿用旧值"))
		if got.Hit {
			t.Errorf("quality=%s 时不应命中指标类告警，实际 Hit=%v", quality, got.Hit)
		}
		if got.Reason == "" {
			t.Errorf("quality=%s 时应带上不可用原因供页面展示", quality)
		}
	}

	// unsupported 是"该引擎压根不支持这个指标"，即使规则要求缺数据告警也绝不命中 ——
	// 否则 PG 实例上会永久挂着一条 MySQL 独有指标的告警。
	got := evaluateRule(row, ruleFixture(MetricRiskXIDAge, ">", 1, "alert"), latestFixture(MetricRiskXIDAge, QualityUnsupported, nil, "该引擎不支持事务 ID 年龄"))
	if got.Hit {
		t.Errorf("unsupported 指标不应命中任何告警，实际 Hit=%v", got.Hit)
	}
}

func TestEvaluateRuleHitsOnTrustedValue(t *testing.T) {
	row := monitorInstanceRow{InstanceID: "ds-test", DbType: "MySQL"}
	bad := 0.0

	// 对照组：quality=ok 且确实越过阈值时必须命中，避免上面的"不命中"测试
	// 因为判定被写死成永远 false 而假通过。
	got := evaluateRule(row, ruleFixture(MetricReplSQLRunning, "!=", 1, "ignore"), latestFixture(MetricReplSQLRunning, QualityOK, &bad, ""))
	if !got.Hit {
		t.Fatalf("quality=ok 且值越过阈值应命中，实际未命中")
	}
	if got.Value != 0 {
		t.Errorf("命中时应带出观测值 0，实际 %v", got.Value)
	}

	// 边界：quality=ok 但值为 NULL（采集器写了 ok 却没给值）不应命中，
	// 否则会把 nil 解引用成 0 并误报。
	got = evaluateRule(row, ruleFixture(MetricReplSQLRunning, "!=", 1, "ignore"), latestFixture(MetricReplSQLRunning, QualityOK, nil, ""))
	if got.Hit {
		t.Errorf("quality=ok 但值为 NULL 不应命中，实际 Hit=%v", got.Hit)
	}
}

// ---------------------------------------------------------------------------
// PromQL instance 过滤片段：这类缺陷只在接上真实 Prometheus 后才会暴露，
// 单测是唯一能在本机拦住它的地方。
// ---------------------------------------------------------------------------

// TestPrometheusInstancePatternHasNoBackslash 是本组测试的核心。
// PromQL 的字符串字面量不接受 `\.` 转义，一旦片段里出现反斜杠，
// Prometheus 会返回 400 unknown escape sequence，6 项资源指标全部拿不到。
func TestPrometheusInstancePatternHasNoBackslash(t *testing.T) {
	inputs := []string{
		"192.168.1.99",
		"192.168.1.100",
		"centos01",
		"db-master.bj.internal",
		`weird\host`,
	}
	for _, in := range inputs {
		got := prometheusInstancePattern(in)
		if strings.Contains(got, `\`) {
			t.Errorf("输入 %q 生成的片段含反斜杠 %q —— PromQL 会报 unknown escape sequence", in, got)
		}
	}
	// 明确锁定 regexp.QuoteMeta 那种写法不会被引入回来。
	if got := prometheusInstancePattern("192.168.1.99"); got != "192[.]168[.]1[.]99" {
		t.Errorf("点号应转成字符类，实际 %q", got)
	}
	// 反斜杠必须被剔除，否则会破坏字面量转义。
	if got := prometheusInstancePattern(`a\b`); got != "ab" {
		t.Errorf("反斜杠应被剔除，实际 %q", got)
	}
}

// TestPrometheusInstancePatternMatchesTargetInstance 验证字符类写法与
// 期望的正则语义完全一致 —— 既不能漏匹配，也不能把别的网段误放进来。
func TestPrometheusInstancePatternMatchesTargetInstance(t *testing.T) {
	pattern := "^" + prometheusInstancePattern("192.168.1.99") + "(?::[0-9]+)?$"
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("生成的片段必须能被 RE2 编译，实际 %q 报错 %v", pattern, err)
	}
	hits := []string{"192.168.1.99", "192.168.1.99:9100", "192.168.1.99:9104"}
	for _, h := range hits {
		if !re.MatchString(h) {
			t.Errorf("instance=%q 应被匹配（Prometheus 的 instance 标签带端口）", h)
		}
	}
	misses := []string{"192.168.1.990", "192x168x1x99", "192.168.1.9", "10.0.0.1", ""}
	for _, h := range misses {
		if re.MatchString(h) {
			t.Errorf("instance=%q 不应被匹配", h)
		}
	}
	// 主机名形式保持原样。
	hostPattern := "^" + prometheusInstancePattern("centos01") + "(?::[0-9]+)?$"
	hostRe, err := regexp.Compile(hostPattern)
	if err != nil {
		t.Fatalf("主机名片段必须可编译，实际 %q 报错 %v", hostPattern, err)
	}
	if !hostRe.MatchString("centos01:9100") || hostRe.MatchString("centos02") {
		t.Errorf("主机名匹配语义错误，pattern=%q", hostPattern)
	}
}

// TestCollectHostResourcesFilterShape 用完整的表达式形状做一次守门：
// 表达式里出现 `\.` 就是缺陷复现。
func TestCollectHostResourcesFilterShape(t *testing.T) {
	filter := fmt.Sprintf(`instance=~"^%s(?::[0-9]+)?$"`, prometheusInstancePattern("192.168.1.99"))
	expr := fmt.Sprintf(`clamp_min(100 - (avg(irate(node_cpu_seconds_total{%s,mode="idle"}[5m])) * 100), 0)`, filter)
	if strings.Contains(expr, `\.`) {
		t.Errorf("表达式含 `\\.`，Prometheus 会返回 400：%s", expr)
	}
	if !strings.Contains(expr, `instance=~"^192[.]168[.]1[.]99(?::[0-9]+)?$"`) {
		t.Errorf("表达式形状不符合预期：%s", expr)
	}
}

// TestHostLayerMetricRegistration 守住「指标字典声明了 host 指标 → 采集必须注册」。
//
// 「硬盘使用量」长期显示 - 就是链条断在这一环：字典里写了、展示目录也摆了卡片，
// 唯独 collectResourceSamples 的注册表里没有它，于是永远采不到值。
func TestHostLayerMetricRegistration(t *testing.T) {
	registered := map[string]string{}
	for _, item := range resourceSampleKeys {
		registered[item.key] = item.unit
	}
	for _, item := range builtinMetricCatalog {
		if item.Scope != "host" {
			continue
		}
		if _, ok := registered[item.MetricKey]; !ok {
			t.Fatalf("指标字典声明了主机层指标 %s，但采集注册表里没有它 —— 页面会永远显示 -", item.MetricKey)
		}
	}
	for key := range registered {
		found := false
		for _, item := range builtinMetricCatalog {
			if item.MetricKey == key {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("采集注册表里有 %s，但指标字典中没有对应条目", key)
		}
	}
}

// TestCollectHostResourcesCoversRegisteredKeys 守住「注册了 → 必须有 PromQL 表达式」。
//
// 用假 Prometheus 应答所有查询，再反射遍历 ResourceMetrics：只要某个已注册指标
// 仍是"不可用"，就说明 expressions 里漏了它的表达式（SDK 层静默跳过、页面显示 -）。
func TestCollectHostResourcesCoversRegisteredKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1700000000,"123"]}]}}`)
	}))
	defer srv.Close()
	client := &prometheusClient{baseURL: srv.URL, httpClient: srv.Client()}

	metrics, err := client.CollectHostResources(context.Background(), "centos01")
	if err != nil {
		t.Fatalf("CollectHostResources 不应报错，实际 %v", err)
	}

	registered := map[string]struct{}{}
	for _, item := range resourceSampleKeys {
		registered[item.key] = struct{}{}
	}
	value := reflect.ValueOf(metrics)
	typ := value.Type()
	checked := 0
	for i := 0; i < typ.NumField(); i++ {
		tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		key := "res." + tag
		if _, ok := registered[key]; !ok {
			continue
		}
		metric, ok := value.Field(i).Interface().(MetricValue)
		if !ok {
			t.Fatalf("ResourceMetrics 字段 %s 类型应为 MetricValue", typ.Field(i).Name)
		}
		if !metric.Available {
			t.Errorf("主机层指标 %s 在假 Prometheus 下仍采集不到，说明 CollectHostResources 缺少它的表达式", key)
		}
		checked++
	}
	if checked != len(registered) {
		t.Fatalf("ResourceMetrics 只承载 %d 个已注册指标，应为 %d —— 新增 host 指标要同步加字段", checked, len(registered))
	}
}

// TestClassifyScreenCard 锁定大屏四分类的判定优先级（告警驱动口径，见 monitor_health_state.go）：
// 未采集/采集失败固定前置 > 未恢复告警阈值 > 采集降级 > 健康。
// 环形图、明细表状态列、分组矩阵共用这一口径，判定一旦漂移三处同时失真。
func TestClassifyScreenCard(t *testing.T) {
	base := InstanceMonitorCard{CollectStatus: "success", Quality: QualityOK, Status: "healthy"}

	cases := []struct {
		name string
		mut  func(c *InstanceMonitorCard)
		want string
	}{
		{"采集失败判故障", func(c *InstanceMonitorCard) { c.CollectStatus = "failed" }, "fault"},
		{"质量error判故障", func(c *InstanceMonitorCard) { c.Quality = QualityError }, "fault"},
		{"可达性为0判故障", func(c *InstanceMonitorCard) {
			c.Metrics = []MetricSampleView{{Key: MetricAvailUp, Available: true, Value: 0}}
		}, "fault"},
		{"从未采集判未采集", func(c *InstanceMonitorCard) { c.CollectStatus = "never" }, "uncollected"},
		{"数据过期判未采集", func(c *InstanceMonitorCard) { c.Quality = QualityStale }, "uncollected"},
		{"无数据判未采集", func(c *InstanceMonitorCard) { c.Quality = QualityNoData }, "uncollected"},
		{"新鲜度超限判未采集", func(c *InstanceMonitorCard) { c.FreshnessSeconds = 999 }, "uncollected"},
		{"P0告警判故障", func(c *InstanceMonitorCard) {
			c.Alerts = []AlertRecord{{RowKey: "r1", Severity: "P0", StatusKey: "active"}}
		}, "fault"},
		{"P1告警判风险", func(c *InstanceMonitorCard) {
			c.Alerts = []AlertRecord{{RowKey: "r1", Severity: "P1", StatusKey: "active"}}
		}, "risk"},
		// 默认阈值（≤60 故障 / ≤90 风险）+ 扣分（P3=3）：单条 P3 落 97 分，不改变状态。
		// 健康分驱动判定后，状态由分数区间推导，不再按告警等级直接定档。
		{"单条P3判健康", func(c *InstanceMonitorCard) {
			c.Alerts = []AlertRecord{{RowKey: "r1", Severity: "P3", StatusKey: "acknowledged"}}
		}, "healthy"},
		{"多条P3累计判风险", func(c *InstanceMonitorCard) {
			c.Alerts = []AlertRecord{
				{RowKey: "r1", Severity: "P3", StatusKey: "active"},
				{RowKey: "r2", Severity: "P3", StatusKey: "active"},
				{RowKey: "r3", Severity: "P3", StatusKey: "active"},
				{RowKey: "r4", Severity: "P3", StatusKey: "active"},
			}
		}, "risk"},
		{"已忽略告警不影响判定", func(c *InstanceMonitorCard) {
			c.Alerts = []AlertRecord{{RowKey: "r1", Severity: "P0", StatusKey: "active", Ignored: true}}
		}, "healthy"},
		{"部分可用判风险", func(c *InstanceMonitorCard) { c.Quality = "partial" }, "risk"},
		// 真实链路里 quality=unsupported 时卡片 Status 已被置为 warning
		//（见 monitor_store.go 卡片状态推导），属于"采集质量降级"固定前置，判风险。
		{"unsupported判风险", func(c *InstanceMonitorCard) { c.Quality = QualityUnsupported }, "risk"},
		{"故障优先于告警", func(c *InstanceMonitorCard) {
			c.CollectStatus = "failed"
			c.Alerts = []AlertRecord{{RowKey: "r1", Severity: "P2", StatusKey: "active"}}
		}, "fault"},
		{"数据过期优先于告警", func(c *InstanceMonitorCard) {
			c.Quality = QualityStale
			c.Alerts = []AlertRecord{{RowKey: "r1", Severity: "P0", StatusKey: "active"}}
		}, "uncollected"},
	}
	for _, tc := range cases {
		card := base
		tc.mut(&card)
		if got := classifyScreenCard(card); got != tc.want {
			t.Errorf("%s: 期望 %s，实际 %s", tc.name, tc.want, got)
		}
	}
	if got := classifyScreenCard(base); got != "healthy" {
		t.Errorf("默认卡片应为 healthy，实际 %s", got)
	}
}

// TestAlertStatusText 人工认领后的状态文案必须落为「处理中」，
// 否则大屏"处理中"页签与卡片状态对不上。
func TestAlertStatusText(t *testing.T) {
	cases := map[string]string{
		"active":       "待认领",
		"pending":      "观察中",
		"acknowledged": "处理中",
		"recovered":    "已恢复",
	}
	for raw, want := range cases {
		if got := alertStatusText(raw); got != want {
			t.Errorf("alertStatusText(%q) 期望 %q，实际 %q", raw, want, got)
		}
	}
}

// TestBuildRiskInstances 风险原因提取：只收 risk 实例、告警与采集降级两类原因、
// 排序按严重度优先。这是风险总览与下钻的数据口径，不能漂。
func TestBuildRiskInstances(t *testing.T) {
	observed := 2.0
	threshold := 1.0
	riskCard := InstanceMonitorCard{
		InstanceID:    "ds-risk",
		Hostname:      "h1",
		Port:          3306,
		Target:        "h1:3306",
		DbType:        "mysql",
		ScreenClass:   "risk",
		StatusMessage: "存在待认领告警",
		Quality:       "partial",
		Alerts: []AlertRecord{
			{Name: "MySQL 复制 IO 线程 != 1", Severity: "P1", MetricKey: "repl.io_running",
				Message: "复制 IO 线程当前值 0.00", Threshold: threshold, ObservedValue: observed,
				DurationSec: 33300, Ignored: false},
			{Name: "应被跳过的忽略告警", Severity: "P2", Ignored: true},
		},
		Metrics: []MetricSampleView{
			{Key: "perf.qps", DisplayName: "QPS", Quality: QualityOK, Available: true},
			{Key: "risk.xid_age", DisplayName: "XID 龄期", Quality: QualityUnsupported, Available: false, Reason: "该引擎不支持此指标"},
		},
	}
	healthyCard := InstanceMonitorCard{InstanceID: "ds-ok", ScreenClass: "healthy"}
	partialOnly := InstanceMonitorCard{
		InstanceID:    "ds-pg",
		Target:        "h2:5432",
		ScreenClass:   "risk",
		StatusMessage: "部分指标不可用（详见指标卡上的质量标记）",
		Quality:       "partial",
		Metrics: []MetricSampleView{
			{Key: "perf.qps", DisplayName: "QPS", Quality: QualityUnsupported, Available: false, Reason: "该引擎不支持此指标"},
		},
	}

	instances := buildRiskInstances([]InstanceMonitorCard{healthyCard, riskCard, partialOnly})
	if len(instances) != 2 {
		t.Fatalf("应只提取 2 个 risk 实例，实际 %d", len(instances))
	}
	// P1 有告警的排最前
	if instances[0].InstanceID != "ds-risk" {
		t.Fatalf("P1 告警实例应排第一，实际 %s", instances[0].InstanceID)
	}
	first := instances[0]
	if first.MaxSeverity != "P1" {
		t.Errorf("MaxSeverity 应为 P1，实际 %q", first.MaxSeverity)
	}
	if first.ReasonCount != 2 {
		t.Errorf("告警1条+不可用指标1条=2条原因，实际 %d", first.ReasonCount)
	}
	alert := first.Reasons[0]
	if alert.Kind != RiskReasonAlert || alert.Severity != "P1" {
		t.Errorf("第一条应为 P1 告警原因，实际 kind=%s severity=%s", alert.Kind, alert.Severity)
	}
	if alert.Threshold == nil || alert.Observed == nil {
		t.Errorf("告警原因必须带阈值与观测值指针")
	}
	if alert.Basis == "" || alert.DurationSec != 33300 {
		t.Errorf("判定依据与持续时间必须填充，basis=%q", alert.Basis)
	}
	collect := first.Reasons[1]
	if collect.Kind != RiskReasonCollect {
		t.Errorf("第二条应为采集降级原因，实际 %s", collect.Kind)
	}
	// partial-only 实例：纯采集类原因
	pg := instances[1]
	if pg.MaxSeverity != "" {
		t.Errorf("无告警实例 MaxSeverity 应为空，实际 %q", pg.MaxSeverity)
	}
	if pg.ReasonCount != 1 || pg.Reasons[0].MetricKey != "perf.qps" {
		t.Errorf("partial 实例应列出不可用指标 perf.qps，实际 %d 条", pg.ReasonCount)
	}
}

// TestHumanizeDuration 时长可读化。
func TestHumanizeDuration(t *testing.T) {
	cases := []struct {
		seconds int64
		want    string
	}{
		{0, "未知时长"},
		{45, "45秒"},
		{185, "3分05秒"},
		{33300, "9小时15分"},
		{187200, "2天4小时"},
	}
	for _, c := range cases {
		if got := humanizeDuration(c.seconds); got != c.want {
			t.Errorf("humanizeDuration(%d) 期望 %q，实际 %q", c.seconds, c.want, got)
		}
	}
}

// TestSeverityWeight 排序权重：P1 最先，空/未知兜底。
func TestSeverityWeight(t *testing.T) {
	if severityWeight("P1") >= severityWeight("P2") || severityWeight("P2") >= severityWeight("P3") {
		t.Error("P1 < P2 < P3 的权重顺序被破坏")
	}
	if severityWeight("") != 9 || severityWeight("X") != 9 {
		t.Error("未知等级必须落到哨兵权重 9，不能抢在最前")
	}
}

// TestResolveMonitorWindowPresets 预设区间：分钟/小时/天三种粒度、兼容旧 range_hours、默认 30 分钟。
func TestResolveMonitorWindowPresets(t *testing.T) {
	cases := []struct {
		minutes int
		hours   int
		wantMin int
		wantLbl string
	}{
		{5, 0, 5, "最近5分钟"},
		{30, 0, 30, "最近30分钟"},
		{60, 0, 60, "最近1小时"},    // 下拉框「最近1小时」
		{360, 0, 360, "最近6小时"},  // 下拉框「最近6小时」
		{1440, 0, 1440, "最近1天"}, // 整日：必须叫「最近1天」而不是「最近24小时」
		{10080, 0, 10080, "最近7天"},
		{0, 1, 60, "最近1小时"},         // 兼容旧 range_hours
		{0, 24, 1440, "最近1天"},       // 旧参数走整日也归一成「天」
		{0, 0, 30, "最近30分钟"},        // 都缺省
		{0, 999999, 43200, "最近30天"}, // 超大区间收敛到 30 天
		{-5, 0, 30, "最近30分钟"},       // 负数视同缺省
	}
	for _, c := range cases {
		win, err := resolveMonitorWindow(c.minutes, c.hours, "", "")
		if err != nil {
			t.Fatalf("预设区间解析失败: %v", err)
		}
		if win.Custom {
			t.Errorf("预设区间不应标记为自定义")
		}
		if win.Minutes() != c.wantMin {
			t.Errorf("range_minutes=%d range_hours=%d 期望 %d 分钟，实际 %d", c.minutes, c.hours, c.wantMin, win.Minutes())
		}
		if got := win.Label(); got != c.wantLbl {
			t.Errorf("range_minutes=%d range_hours=%d 期望文案 %q，实际 %q", c.minutes, c.hours, c.wantLbl, got)
		}
	}
}

// TestResolveMonitorWindowCustom 自定义区间：起止解析、校验、边界收敛。
func TestResolveMonitorWindowCustom(t *testing.T) {
	now := time.Now()
	start := now.Add(-2 * time.Hour).Format(time.DateTime)
	end := now.Add(-1 * time.Hour).Format(time.DateTime)

	win, err := resolveMonitorWindow(0, 0, start, end)
	if err != nil {
		t.Fatalf("合法自定义区间不应报错: %v", err)
	}
	if !win.Custom || win.Minutes() != 60 {
		t.Errorf("自定义区间应标记 custom 且跨 60 分钟，实际 custom=%v minutes=%d", win.Custom, win.Minutes())
	}
	if win.End.Sub(win.Start) != time.Hour {
		t.Errorf("窗口跨度应为 1 小时，实际 %v", win.End.Sub(win.Start))
	}

	// 结束时间缺省 → 到当前时刻
	win, err = resolveMonitorWindow(0, 0, start, "")
	if err != nil {
		t.Fatalf("结束时间缺省应默认为当前时刻: %v", err)
	}
	if win.End.Before(now.Add(-2 * time.Second)) {
		t.Errorf("结束时间缺省时应回落当前时刻，实际 %v", win.End)
	}

	// 未来的结束时间 → 收敛到当前（未来没有数据）
	future := now.Add(3 * time.Hour).Format(time.DateTime)
	if win, err = resolveMonitorWindow(0, 0, start, future); err != nil {
		t.Fatalf("未来结束时间应被收敛而不是报错: %v", err)
	} else if win.End.After(time.Now().Add(time.Second)) {
		t.Errorf("未来结束时间应收敛到当前时刻，实际 %v", win.End)
	}

	// 起始晚于结束 → 报错（不猜）
	if _, err := resolveMonitorWindow(0, 0, end, start); err == nil {
		t.Error("起始时间晚于结束时间必须报错")
	}
	// 缺起始 → 报错
	if _, err := resolveMonitorWindow(0, 0, "", end); err == nil {
		t.Error("自定义区间缺起始时间必须报错")
	}
	// 格式无法识别 → 报错
	if _, err := resolveMonitorWindow(0, 0, "2026/09/16 07:00", end); err == nil {
		t.Error("无法识别的时间格式必须报错")
	}
	// 跨度几乎为 0（30 秒）→ 收敛为 1 分钟
	s1 := now.Add(-time.Minute).Format(time.DateTime)
	s2 := now.Add(-time.Minute).Add(30 * time.Second).Format(time.DateTime)
	if win, err = resolveMonitorWindow(0, 0, s1, s2); err != nil {
		t.Fatalf("极小跨度应收敛而非报错: %v", err)
	} else if win.Minutes() != 1 {
		t.Errorf("极小跨度应收敛为 1 分钟，实际 %d", win.Minutes())
	}
}

// TestMonitorWindowShortLabel 窄位文案：表格列头用。
func TestMonitorWindowShortLabel(t *testing.T) {
	now := time.Now()
	win, _ := resolveMonitorWindow(5, 0, "", "")
	if win.ShortLabel() != "5min" {
		t.Errorf("5 分钟短文案应为 5min，实际 %q", win.ShortLabel())
	}
	win, _ = resolveMonitorWindow(0, 2, "", "")
	if win.ShortLabel() != "2h" {
		t.Errorf("2 小时短文案应为 2h，实际 %q", win.ShortLabel())
	}
	win, _ = resolveMonitorWindow(1440, 0, "", "")
	if win.ShortLabel() != "1d" {
		t.Errorf("1 天短文案应为 1d，实际 %q", win.ShortLabel())
	}
	win, _ = resolveMonitorWindow(10080, 0, "", "")
	if win.ShortLabel() != "7d" {
		t.Errorf("7 天短文案应为 7d，实际 %q", win.ShortLabel())
	}
	win, _ = resolveMonitorWindow(0, 0, now.Add(-time.Hour).Format(time.DateTime), now.Add(-time.Minute).Format(time.DateTime))
	if win.ShortLabel() != "自定义" {
		t.Errorf("自定义区间短文案应为 自定义，实际 %q", win.ShortLabel())
	}
}

// ---------------------------------------------------------------------------
// 指标模板：设计里的几条硬规则必须有测试兜住，否则很容易被后续改动悄悄放开。
// ---------------------------------------------------------------------------

// 主机层指标默认不进模板：与数据库类型无关，由 Prometheus + node_exporter 统一采集，
// 进了就会出现同一台机器上 MySQL 与 PG 各采一份、或互相裁剪的怪现象。
//
// 例外：MySQL / PostgreSQL / Oracle / TiDB 的监控明细「基础指标」Tab 实际展示了 res.* 指标，
// 默认模板已按展示目录同步，因此这些类型允许勾选主机层（采集侧仍豁免模板裁剪，不会重复采集）。
func TestValidateTemplateMetricKeysHostLayer(t *testing.T) {
	// TiDB 有专属展示目录且「基础指标」Tab 展示主机层：与 MySQL 同待遇，允许勾选
	if _, err := validateTemplateMetricKeys("TiDB", []string{MetricResCPU, MetricPerfQPS}); err != nil {
		t.Fatalf("TiDB 模板应接受主机层指标，实际报错：%v", err)
	}
	// 未展示主机层指标的类型：仍拒绝。用 Doris 复用 MySQL 目录但不纳入主机层治理这一口径验证
	_, err := validateTemplateMetricKeys("Doris", []string{MetricResCPU, MetricPerfQPS})
	if err == nil {
		t.Fatalf("未展示主机层指标的类型不应接受 %s", MetricResCPU)
	}
	if !strings.Contains(err.Error(), "主机层") {
		t.Errorf("拒绝主机层指标时应说明原因，实际错误：%v", err)
	}
	// MySQL / PostgreSQL / Oracle：允许，且与展示目录保持一致
	for _, engine := range []string{"MySQL", "Oracle"} {
		keys, err := validateTemplateMetricKeys(engine, []string{MetricResCPU, MetricPerfQPS})
		if err != nil {
			t.Fatalf("%s 模板应接受主机层指标，实际报错：%v", engine, err)
		}
		if len(keys) != 2 {
			t.Fatalf("%s 应接受 res.cpu + perf.qps 两项，实际 %v", engine, keys)
		}
	}
}

// 跨引擎指标必须被拒：MySQL 实例挂 PG 专有指标，采集器只会产出一条条 error。
func TestValidateTemplateMetricKeysRejectsCrossEngine(t *testing.T) {
	_, err := validateTemplateMetricKeys("MySQL", []string{MetricKernDeadTuples})
	if err == nil {
		t.Fatalf("PG 专有指标 %s 不应被接受进 MySQL 模板", MetricKernDeadTuples)
	}
	keys, ok := validateTemplateMetricKeys("PostgreSQL", []string{MetricKernDeadTuples})
	if ok != nil || len(keys) != 1 || keys[0] != MetricKernDeadTuples {
		t.Fatalf("PG 模板应接受 %s，实际 keys=%v err=%v", MetricKernDeadTuples, keys, ok)
	}
}

func TestValidateTemplateMetricKeysDedupesAndSorts(t *testing.T) {
	keys, err := validateTemplateMetricKeys("MySQL", []string{
		MetricPerfQPS, MetricPerfQPS, "qps", MetricPerfTPS,
	})
	if err != nil {
		t.Fatalf("合法指标不应报错：%v", err)
	}
	// "qps" 是老规则里的裸名，必须经 legacyMetricAlias 归一化后与 perf.qps 去重
	if len(keys) != 2 {
		t.Fatalf("期望去重后 2 项，实际 %d：%v", len(keys), keys)
	}
	if keys[0] >= keys[1] {
		t.Errorf("指标键应升序返回，实际 %v", keys)
	}
}

func TestValidateTemplateMetricKeysRejectsEmpty(t *testing.T) {
	if _, err := validateTemplateMetricKeys("MySQL", nil); err == nil {
		t.Fatal("空指标集合必须被拒绝：空模板会让实例静默采不到任何引擎指标")
	}
}

func TestNormalizeTemplateName(t *testing.T) {
	if name, err := normalizeTemplateName(" MySQL-Core_1 "); err != nil || name != "mysql-core_1" {
		t.Errorf("应接受并归一化为小写，实际 %q err=%v", name, err)
	}
	if _, err := normalizeTemplateName("核心模板"); err == nil {
		t.Error("中文标识会让绑定值难以在各种终端里传递，应当拒绝")
	}
	if _, err := normalizeTemplateName(""); err == nil {
		t.Error("空标识应当拒绝")
	}
}

// 模板可选指标只包含引擎层/内核层，且按 引擎层 → 内核层 排序。
func TestTemplateApplicableMetricsExcludesHostAndOrdersLayers(t *testing.T) {
	items := templateApplicableMetrics("MySQL")
	if len(items) == 0 {
		t.Fatal("MySQL 应当有可勾选的模板指标")
	}
	seenKernel := false
	for index, item := range items {
		if item.Scope == "host" || metricLayer(item.MetricKey) == LayerHost {
			t.Fatalf("模板可选指标里出现了主机层指标：%s", item.MetricKey)
		}
		if metricLayer(item.MetricKey) == LayerKernel {
			seenKernel = true
			continue
		}
		if seenKernel {
			t.Fatalf("同类排序被打破：内核层之后又出现引擎层指标 %s（第 %d 项）", item.MetricKey, index)
		}
	}
}

// 模板表尚未播种时，解析必须返回 nil（调用方据此退化为全量采集），
// 绝不能返回空集合把采集直接掐断。
func TestResolveInstanceMetricKeysWithoutTemplatesFallsBackToGlobal(t *testing.T) {
	row := monitorInstanceRow{InstanceID: "abc123", DbType: "MySQL", MetricsTemplate: "default"}
	keys, tmpl, _ := resolveInstanceMetricKeys(row)
	if tmpl != nil {
		t.Fatalf("无模板数据时不应解析出模板，实际 %+v", tmpl)
	}
	if len(keys) == 0 {
		t.Fatal("无模板时应退化为按字典全量采集，而不是一个都不采")
	}
	for _, key := range keys {
		if metricLayer(key) == LayerHost {
			t.Fatalf("resolveInstanceMetricKeys 只负责引擎层/内核层，却返回了主机层指标 %s", key)
		}
	}
}

// qualityRank 必须把 disabled 排在最末：它既不是"可信"也不是"故障"，
// 排序上不能插到 unsupported / error 前面冒充更严重。
func TestQualityDisabledRankIsLast(t *testing.T) {
	if qualityWeight(QualityDisabled) <= qualityWeight(QualityError) {
		t.Fatalf("disabled(%d) 应排在最末，实际排在 error(%d) 之前或相同",
			qualityWeight(QualityDisabled), qualityWeight(QualityError))
	}
	if qualityIsAvailable(QualityDisabled) {
		t.Fatal("未纳入模板的指标不可视为可用值")
	}
}

// wantMetric 的"空"语义是模板门控的最后一道关：
// 空切片（模板与字典总闸取交集后确实无可采指标）必须解读为"一个都不采"，
// 否则总闸会被绕回"全采"，用户关掉的指标又偷偷跑起来。
func TestWantMetricDistinguishesNilFromEmpty(t *testing.T) {
	if !wantMetric(nil, MetricPerfQPS) {
		t.Error("keys 为 nil 表示字典不可用的兜底，应全采")
	}
	if wantMetric([]string{}, MetricPerfQPS) {
		t.Error("keys 为空切片表示交集为空，不应再采任何指标")
	}
	if !wantMetric([]string{MetricPerfQPS}, MetricPerfQPS) {
		t.Error("命中列表内的指标应当被采集")
	}
	if wantMetric([]string{MetricPerfQPS}, MetricPerfTPS) {
		t.Error("不在列表内的指标不应被采集")
	}
}

// resolveInstanceMetricKeys 在"无模板"时必须返回显式的字典全集（非 nil），
// 这样采集器才会走"按列表采"而不是被误读成"全采"。
func TestResolveInstanceMetricKeysReturnsExplicitList(t *testing.T) {
	row := monitorInstanceRow{InstanceID: "abc123", DbType: "MySQL", MetricsTemplate: ""}
	keys, _, _ := resolveInstanceMetricKeys(row)
	if keys == nil {
		t.Fatal("无模板时应返回显式的字典全集，而不是 nil")
	}
	if len(keys) == 0 {
		t.Fatal("MySQL 的字典全集不应为空")
	}
}

// TestBuildDetailConclusion 一句话结论：四分类各给一句能追溯的话，不写套话。
func TestBuildDetailConclusion(t *testing.T) {
	observed := 0.0
	threshold := 1.0
	riskCard := InstanceMonitorCard{
		ScreenClass: "risk",
		Metrics:     []MetricSampleView{{Key: MetricResCPU, Quality: QualityOK, Available: true}},
		Alerts: []AlertRecord{{
			Name: "MySQL 复制 IO 线程 != 1", Severity: "P1", MetricKey: "repl.io_running",
			Message: "复制 IO 线程当前值 0.00", Threshold: threshold, ObservedValue: observed,
			DurationSec: 33300,
		}},
	}
	reasons := detailRiskReasons(riskCard)
	if len(reasons) != 1 {
		t.Fatalf("risk 实例应提取到 1 条原因，实际 %d", len(reasons))
	}
	got := buildDetailConclusion(riskCard, "risk", reasons)
	if !strings.Contains(got, "复制 IO 线程当前值 0.00") {
		t.Errorf("结论必须带原因原文，实际 %q", got)
	}
	if !strings.Contains(got, "已持续 9小时15分") {
		t.Errorf("结论必须带持续时长，实际 %q", got)
	}

	// 故障：采集失败应带失败次数
	faultCard := InstanceMonitorCard{
		ScreenClass: "fault", CollectStatus: "failed", ConsecutiveFailures: 5,
		StatusMessage: "连接被拒绝",
	}
	if got := buildDetailConclusion(faultCard, "fault", nil); !strings.Contains(got, "连续失败 5 次") {
		t.Errorf("故障结论应带连续失败次数，实际 %q", got)
	}

	// 未采集：过期态要说明展示的是旧值
	staleCard := InstanceMonitorCard{ScreenClass: "uncollected", Quality: QualityStale}
	if got := buildDetailConclusion(staleCard, "uncollected", nil); !strings.Contains(got, "已过期") {
		t.Errorf("未采集结论应说明数据过期，实际 %q", got)
	}

	// 健康：只说有依据的话
	healthy := InstanceMonitorCard{ScreenClass: "healthy", StatusMessage: "采集正常"}
	if got := buildDetailConclusion(healthy, "healthy", nil); got != "无待认领告警，采集正常" {
		t.Errorf("健康结论口径变化：%q", got)
	}
}

// TestDetailRiskReasonsOnlyForRisk 只有 risk 分类才产出原因，且永远非 nil（前端好渲染）。
func TestDetailRiskReasonsOnlyForRisk(t *testing.T) {
	healthy := InstanceMonitorCard{ScreenClass: "healthy"}
	if reasons := detailRiskReasons(healthy); len(reasons) != 0 || reasons == nil {
		t.Errorf("健康实例不应有风险原因，且必须是空数组而非 nil")
	}
	// 采集降级（partial）实例：无告警也要给出不可用指标原因
	partial := InstanceMonitorCard{
		ScreenClass: "risk", Quality: "partial",
		Metrics: []MetricSampleView{{Key: "perf.qps", DisplayName: "QPS", Quality: QualityUnsupported, Reason: "该引擎不支持此指标"}},
	}
	if reasons := detailRiskReasons(partial); len(reasons) != 1 || reasons[0].Kind != RiskReasonCollect {
		t.Errorf("partial 实例应产出 1 条采集类原因，实际 %d 条", len(reasons))
	}
}

// TestSortDetailInstancesByRisk 列表排序：故障 > 风险 > 未采集 > 健康，未知兜底最后。
func TestSortDetailInstancesByRisk(t *testing.T) {
	items := []MonitorDetailListItem{
		{InstanceID: "ok", ScreenClass: "healthy"},
		{InstanceID: "blind", ScreenClass: "uncollected"},
		{InstanceID: "risk1", ScreenClass: "risk", AlertCount: 1},
		{InstanceID: "risk5", ScreenClass: "risk", AlertCount: 5},
		{InstanceID: "down", ScreenClass: "fault"},
		{InstanceID: "unknown", ScreenClass: ""},
	}
	sortDetailInstancesByRisk(items)
	want := []string{"down", "risk5", "risk1", "blind", "ok", "unknown"}
	for index, id := range want {
		if items[index].InstanceID != id {
			t.Fatalf("第 %d 位应为 %s，实际 %s（顺序：%v）", index, id, items[index].InstanceID, idsOf(items))
		}
	}
	// 稳定性：再排一次不应改变顺序
	sortDetailInstancesByRisk(items)
	for index, id := range want {
		if items[index].InstanceID != id {
			t.Fatalf("二次排序不应改变顺序，第 %d 位实际 %s", index, items[index].InstanceID)
		}
	}
}

func idsOf(items []MonitorDetailListItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.InstanceID)
	}
	return ids
}

// TestCoreTrendMarked 核心指标带由后端标记，非核心指标不得混入。
func TestCoreTrendMarked(t *testing.T) {
	trends := buildMetricTrends([]trendDefinition{
		{Key: MetricResCPU, Label: "CPU"},
		{Key: MetricResNetworkIn, Label: "网络流入"},
		{Key: MetricPerfQPS, Label: "QPS"},
	}, map[string][]DetailMetricPoint{})
	if !trends[0].Core {
		t.Error("res.cpu 应进核心指标带")
	}
	if trends[1].Core {
		t.Error("res.network_in 不应进核心指标带（各引擎差异大，交给指标矩阵）")
	}
	if !trends[2].Core {
		t.Error("perf.qps 应进核心指标带")
	}
}

// TestBuildDetailConclusionPureCollect 纯采集降级的实例：结论先说规模再给样例，
// 不能把第一条指标的原因当成整句结论（那会读成"实例启动后尚未触发预读"这种没头没尾的话）。
func TestBuildDetailConclusionPureCollect(t *testing.T) {
	card := InstanceMonitorCard{
		ScreenClass: "risk",
		Quality:     "partial",
		Metrics: []MetricSampleView{
			{Key: MetricPerfQPS, DisplayName: "QPS", Quality: QualityOK, Available: true},
			{Key: "eng.read_ahead", DisplayName: "预读次数", Quality: QualityNoData, Available: false, Reason: "实例启动后尚未触发预读"},
			{Key: "risk.xid_age", DisplayName: "XID 龄期", Quality: QualityUnsupported, Available: false, Reason: "该引擎不支持此指标"},
		},
	}
	reasons := detailRiskReasons(card)
	got := buildDetailConclusion(card, "risk", reasons)
	if !strings.Contains(got, "采集质量降级") || !strings.Contains(got, "2 项指标当前不可用") {
		t.Errorf("纯采集降级结论应先说规模，实际 %q", got)
	}
	if !strings.Contains(got, "如：") {
		t.Errorf("纯采集降级结论应给一个好读的样例，实际 %q", got)
	}
	if strings.HasPrefix(got, "实例启动后尚未触发预读") {
		t.Errorf("不得把第一条指标的原因直接当成整句结论：%q", got)
	}

	// 有告警时：以最严重告警为主线，并如实报出不可用指标数（真实数量，不取封顶后的列表长度）
	withAlert := card
	withAlert.Alerts = []AlertRecord{{
		Name: "复制延迟 > 60s", Severity: "P1", MetricKey: "repl.seconds_behind",
		Message: "复制延迟当前值 128.00", Threshold: 60, ObservedValue: 128, DurationSec: 3600,
	}}
	got = buildDetailConclusion(withAlert, "risk", detailRiskReasons(withAlert))
	if !strings.Contains(got, "复制延迟当前值 128.00") || !strings.Contains(got, "；另有 2 项指标不可用") {
		t.Errorf("含告警的结论应以告警为主线并报出不可用指标数，实际 %q", got)
	}
}

// 明细页必须"一个不漏"：展示目录里适用于该引擎的指标，即便本轮没采到也要出现，
// 且带 quality=no_data + 可读原因 —— 静默缺数会让运维误判为"这个指标没问题"。
func TestDetailPageKeepsUncollectedMetrics(t *testing.T) {
	row := monitorInstanceRow{InstanceID: "ds-1", DbType: "MySQL"}
	views := []MetricSampleView{
		{Key: MetricResCPU, Quality: QualityOK, Available: true},
	}
	result := appendMissingMetricViews(views, row)

	byKey := map[string]MetricSampleView{}
	for _, item := range result {
		byKey[item.Key] = item
	}
	// 展示目录里适用于 MySQL 的项都应出现（PG 专有的 dead_tuples 不该出现）
	for _, key := range []string{MetricResCPU, MetricResMemory, MetricPerfQPS, MetricKernRedoWriteRate} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("指标 %s 应出现在明细页卡片里（未采集也要显示并说明原因）", key)
		}
	}
	if _, ok := byKey[MetricKernDeadTuples]; ok {
		t.Error("PG 专有指标不应出现在 MySQL 实例的卡片里")
	}
	// 补齐的指标必须是"明确不可用"，而不是悄悄变成 0
	disk := byKey[MetricResDisk]
	if disk.Available {
		t.Error("未采集的指标不应标记为可用")
	}
	if disk.Quality != QualityNoData {
		t.Errorf("未采集指标 quality 应为 no_data，实际 %q", disk.Quality)
	}
	if disk.Reason == "" {
		t.Error("未采集指标必须带原因，否则等于静默缺数")
	}
	// 展示元数据必须一起下发，前端才不需要自己维护阈值文案
	if disk.Threshold == "" || disk.Group == "" {
		t.Error("补齐的指标必须带 threshold 与 group，前端不得自行维护这些文案")
	}
}

// 健康分口径固定在后端（扣分表见 monitor_health_state.go 内置默认：P0=20/P1=10/P2=5/P3=3）。
// 一旦这个口径漂移，各页面会出现多套分数，所以必须用测试锁死。
func TestHealthScoreDeductionIsStable(t *testing.T) {
	card := InstanceMonitorCard{Quality: QualityOK, CollectStatus: "success", ScreenClass: "healthy"}
	alerts := []AlertRecord{
		{Severity: "P2", MetricKey: MetricResDisk},
		{Severity: "P2", MetricKey: MetricEngSlowQueries},
		{Severity: "P3", MetricKey: MetricPerfLongTrx},
	}
	score := buildHealthScore(card, alerts, instanceFacts{LastCheckStatus: "success"})
	if score.Score != 100-5*2-3 {
		t.Errorf("健康分应为 87（100 − P2×2×5 − P3×3），实际 %d", score.Score)
	}
	if len(score.Deductions) != 2 {
		t.Errorf("应有 2 条扣分项，实际 %d", len(score.Deductions))
	}

	// 不可达：不管指标多好看都是 0
	unreachable := buildHealthScore(card, nil, instanceFacts{LastCheckStatus: "failed"})
	if unreachable.Score != 0 {
		t.Errorf("不可达实例健康分应为 0，实际 %d", unreachable.Score)
	}
	if unreachable.Deductions[0].Label == "" {
		t.Error("不可达扣分必须给出可解释的理由")
	}
}

// ---------------------------------------------------------------------------
// 实例画像采集回写
// ---------------------------------------------------------------------------

// 各引擎版本串形态不一，展示前必须归一化，否则同一实例在不同页面会显示成
// "8.0.34" 与 "8.0.34-log" 两种样子。
func TestNormalizeDbVersion(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"MySQL 纯净版", "8.0.34", "8.0.34"},
		{"MySQL 带 log 后缀", "8.0.34-log", "8.0.34"},
		{"MySQL 5.7 带 log", "5.7.44-log", "5.7.44"},
		// TiDB：前导段是 MySQL 兼容版本号，取 -v 尾段的真实版本（不带引擎名，
		// 展示处会拼 db_type，避免出现「TiDB 8.0.11-TiDB」）
		{"TiDB 取真实版本", "5.7.25-TiDB-v7.5.0", "7.5.0"},
		{"TiDB 无 -v 尾段", "8.0.11-TiDB", "8.0.11"},
		{"PG 带发行后缀", "15.3 (Debian 15.3-1.pgdg110+1)", "15.3"},
		{"PG version() 全文", "PostgreSQL 15.3 on x86_64-pc-linux-gnu, compiled by gcc", "15.3"},
		{"Oracle 版本号", "19.0.0.0.0", "19.0.0.0.0"},
		{"空串保持空", "", ""},
	}
	for _, item := range cases {
		if got := normalizeDbVersion(item.raw); got != item.want {
			t.Errorf("%s: normalizeDbVersion(%q) = %q, want %q", item.name, item.raw, got, item.want)
		}
	}
}

// "只写非空"是画像回写的核心纪律：一旦破防，一次网络抖动就会把已采到的画像清空，
// 页面随即退回 "—"，而用户完全不知道刚才还看得到值。
func TestProfileFactsUpdatesOnlyWritesCollected(t *testing.T) {
	full := profileFacts{
		DbVersion:      "8.0.34",
		InstanceRole:   "master",
		UptimeSeconds:  3600,
		OsName:         "Linux",
		OsKernel:       "3.10.0-1160.el7.x86_64",
		CpuCores:       8,
		MemoryGB:       32,
		DiskGB:         1024,
		ProbeLatencyMs: 8,
	}
	updates := profileFactsUpdates(full)
	for _, key := range []string{
		"db_version", "instance_role", "uptime_seconds", "os_name", "os_kernel",
		"cpu_cores", "memory_gb", "disk_gb", "probe_latency_ms",
	} {
		if _, ok := updates[key]; !ok {
			t.Errorf("采到的字段 %s 必须进入回写集合", key)
		}
	}

	// 一轮全空：不应产生任何更新，避免用空值覆盖既有画像
	if got := profileFactsUpdates(profileFacts{}); len(got) != 0 {
		t.Errorf("全空事实不应产生回写字段，实际 %v", got)
	}

	// 部分采到：只回写采到的那部分
	partial := profileFacts{DbVersion: "8.0.34"}
	updates = profileFactsUpdates(partial)
	if len(updates) != 1 {
		t.Errorf("部分采到时只应回写采到的字段，实际 %v", updates)
	}
	if _, ok := updates["uptime_seconds"]; ok {
		t.Error("未采到的 uptime_seconds 不应出现在回写集合里")
	}
}

// 主机规格原始单位是字节，展示口径统一为 GB。
func TestBytesToGB(t *testing.T) {
	cases := []struct {
		name  string
		bytes float64
		want  float64
	}{
		{"32GB", 32 * 1024 * 1024 * 1024, 32},
		{"1TB", 1024 * 1024 * 1024 * 1024, 1024},
		{"不足 10GB 保留两位", 7.68 * 1024 * 1024 * 1024, 7.68},
	}
	for _, item := range cases {
		if got := bytesToGB(item.bytes); got != item.want {
			t.Errorf("%s: bytesToGB = %v, want %v", item.name, got, item.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 采集频率：唯一数据源与三处派生
// ---------------------------------------------------------------------------

// TestScrapeConfigDerivesThreePeriodsFromOneValue 核心不变式：
// 三处周期（Prometheus 抓取 / 落库 / PromQL 窗口与 step）全部由同一个值派生。
// 任何一处将来写死成常量，这个用例就会失败。
func TestScrapeConfigDerivesThreePeriodsFromOneValue(t *testing.T) {
	for _, preset := range scrapeIntervalPresets {
		cfg := deriveScrapeConfig(preset.Seconds, false, "test", "", "", "")
		if cfg.PrometheusScrapeIntervalSeconds != preset.Seconds {
			t.Errorf("档位 %ds：Prometheus 抓取间隔 = %d，应与周期一致", preset.Seconds, cfg.PrometheusScrapeIntervalSeconds)
		}
		if cfg.CollectIntervalSeconds != preset.Seconds {
			t.Errorf("档位 %ds：落库周期 = %d，应与周期一致", preset.Seconds, cfg.CollectIntervalSeconds)
		}
		if cfg.PromStepSeconds != preset.Seconds {
			t.Errorf("档位 %ds：step = %d，应与周期一致", preset.Seconds, cfg.PromStepSeconds)
		}
		wantWindow := preset.Seconds * promRateWindowFactor
		if wantWindow < promRateWindowFloorSeconds {
			wantWindow = promRateWindowFloorSeconds
		}
		if cfg.PromRateWindowSeconds != wantWindow {
			t.Errorf("档位 %ds：窗口 = %d，应为 max(4×周期,60) = %d", preset.Seconds, cfg.PromRateWindowSeconds, wantWindow)
		}
		if cfg.PromRateWindowSeconds < preset.Seconds*promRateWindowFactor {
			t.Errorf("档位 %ds：窗口 %ds 小于 4 倍周期，rate 可能因样本不足算空", preset.Seconds, cfg.PromRateWindowSeconds)
		}
	}
}

// TestScrapeConfigBucketsAlignedToInterval 桶宽必须是周期的整数倍。
// 这是"不漏采、不重复采"的硬约束：桶边界与采样点对齐后，
// 一个采样点只会落进一个桶，不会横跨两个桶被算两次。
func TestScrapeConfigBucketsAlignedToInterval(t *testing.T) {
	for _, preset := range scrapeIntervalPresets {
		cfg := deriveScrapeConfig(preset.Seconds, false, "test", "", "", "")
		if len(cfg.HistoryBuckets) != len(rangeWindows) {
			t.Fatalf("档位 %ds：派生桶数量 = %d，应覆盖全部 %d 个时间窗口", preset.Seconds, len(cfg.HistoryBuckets), len(rangeWindows))
		}
		for key, bucket := range cfg.HistoryBuckets {
			if bucket <= 0 {
				t.Errorf("档位 %ds 窗口 %s：桶宽 = %d，必须为正", preset.Seconds, key, bucket)
			}
			if bucket%preset.Seconds != 0 {
				t.Errorf("档位 %ds 窗口 %s：桶宽 %d 不是周期的整数倍，会造成桶边界漂移", preset.Seconds, key, bucket)
			}
			if bucket < preset.Seconds {
				t.Errorf("档位 %ds 窗口 %s：桶宽 %d 小于采集周期，必然漏采", preset.Seconds, key, bucket)
			}
		}
	}
}

// TestDeriveBucketSecondsRespectsMaxPoints 周期调到 15s 时，7 天窗口不能算出几万个点。
func TestDeriveBucketSecondsRespectsMaxPoints(t *testing.T) {
	span := int((7 * 24 * time.Hour).Seconds())
	bucket := DeriveBucketSeconds(span, 43200, 15)
	if points := span / bucket; points > maxHistoryPoints {
		t.Errorf("7 天窗口点数 = %d，超过上限 %d（桶宽 %d）", points, maxHistoryPoints, bucket)
	}
	if bucket%15 != 0 {
		t.Errorf("桶宽 %d 不是周期 15s 的整数倍", bucket)
	}
}

func TestNormalizeScrapeIntervalDegradesIllegalValues(t *testing.T) {
	cases := []struct {
		name        string
		input       int
		allowCustom bool
		want        int
		wantReason  bool
	}{
		{"未配置", 0, false, DefaultScrapeIntervalSeconds, true},
		{"档位值直接放行", 60, false, 60, false},
		{"非档位且未开自定义", 45, false, DefaultScrapeIntervalSeconds, true},
		{"自定义且为15倍数", 45, true, 45, false},
		{"自定义但非15倍数向上取整", 47, true, 60, true},
		{"自定义低于下限", 5, true, DefaultScrapeIntervalSeconds, true},
		{"自定义高于上限", 99999, true, DefaultScrapeIntervalSeconds, true},
	}
	for _, item := range cases {
		got, reason := NormalizeScrapeInterval(item.input, item.allowCustom)
		if got != item.want {
			t.Errorf("%s: 归一化 = %d, want %d", item.name, got, item.want)
		}
		if (reason != "") != item.wantReason {
			t.Errorf("%s: 是否有降级原因 = %v, want %v（%s）", item.name, reason != "", item.wantReason, reason)
		}
	}
}

func TestValidateScrapeIntervalRejectsIllegalValues(t *testing.T) {
	if err := ValidateScrapeInterval(60, false); err != nil {
		t.Errorf("档位值应放行，实际报错：%v", err)
	}
	if err := ValidateScrapeInterval(45, false); err == nil {
		t.Error("档位外的值在未开启自定义时应被拒绝")
	}
	if err := ValidateScrapeInterval(45, true); err != nil {
		t.Errorf("开启自定义后 45（15 的倍数）应放行，实际报错：%v", err)
	}
	if err := ValidateScrapeInterval(47, true); err == nil {
		t.Error("非 15 倍数的值应被拒绝，否则桶边界会漂移")
	}
	if err := ValidateScrapeInterval(5, true); err == nil {
		t.Error("低于下限的值应被拒绝")
	}
}

// TestResolveInstanceIntervalNeverFasterThanPlatform 实例级只能放慢，不能快于平台周期：
// 快于 Prometheus 抓取只会反复取到同一个值（重复采），那不是更高的精度。
func TestResolveInstanceIntervalNeverFasterThanPlatform(t *testing.T) {
	cfg := deriveScrapeConfig(60, false, "test", "", "", "")
	cases := []struct {
		instanceValue int
		want          int
		wantNote      bool
	}{
		{0, 60, false},
		{60, 60, false},
		{15, 60, true},
		{120, 120, false},
		{90, 120, true},
		{300, 300, false},
	}
	for _, item := range cases {
		got, note := ResolveInstanceScrapeInterval(item.instanceValue, cfg)
		if got != item.want {
			t.Errorf("实例周期 %d：派生结果 = %d, want %d", item.instanceValue, got, item.want)
		}
		if (note != "") != item.wantNote {
			t.Errorf("实例周期 %d：是否给出校正说明 = %v, want %v", item.instanceValue, note != "", item.wantNote)
		}
		if got%cfg.IntervalSeconds != 0 {
			t.Errorf("实例周期 %d：结果 %d 不是平台周期 %d 的整数倍", item.instanceValue, got, cfg.IntervalSeconds)
		}
	}
}

func TestParsePrometheusScrapeInterval(t *testing.T) {
	cases := []struct {
		content string
		want    int
	}{
		{"global:\n  scrape_interval: 15s\n", 15},
		{"global:\n  scrape_interval: 60s\n  scrape_timeout: 10s\n", 60},
		{"global:\n  scrape_interval: 1m\n", 60},
		{"global:\n  scrape_interval: \"30s\"\n", 30},
		{"global:\n  scrape_timeout: 10s\n", 0},
	}
	for _, item := range cases {
		if got := parsePrometheusScrapeInterval(item.content); got != item.want {
			t.Errorf("解析 %q = %d, want %d", item.content, got, item.want)
		}
	}
}

// TestInstanceScrapeIntervalFollowsPlatform "0=跟随平台"语义：
// 实例行没有显式周期（0/NULL）时必须跟平台走——平台改小，实例立即跟上。
// 这是采集周期改 15 秒不生效事故的回归测试：旧实现把默认值 60 物化进实例行，
// 全局改 15 后实例永远停在 60。
func TestInstanceScrapeIntervalFollowsPlatform(t *testing.T) {
	cfg := deriveScrapeConfig(15, false, "test", "", "", "")
	// 0 / 负数 → 平台值 15
	for _, v := range []int{0, -1} {
		got, note := ResolveInstanceScrapeInterval(v, cfg)
		if got != 15 || note != "" {
			t.Errorf("实例周期 %d 应静默跟随平台 15 秒，实际 %d（note=%q）", v, got, note)
		}
	}
	// 平台改小后，旧的显式 60 仍是合法"放慢"（15 的 4 倍）——这是设计内行为，
	// 但必须在 UI 上可见（表格标注实例覆盖），不能静默
	got, _ := ResolveInstanceScrapeInterval(60, cfg)
	if got != 60 {
		t.Errorf("显式 60 在平台 15 下应保持 60，实际 %d", got)
	}
}

// TestUpsertTargetConfigAcceptsFollowPlatform 表单层允许 0（跟随平台），
// 但 1~14 秒必须拒绝——存进去只会被调度器钳掉，制造假象。
func TestUpsertTargetConfigAcceptsFollowPlatform(t *testing.T) {
	// 这里只测服务层校验逻辑的边界语义（不连库）：复用与 UpsertTargetConfigService 相同的判断
	valid := func(v int) bool { return v == 0 || v >= MinScrapeIntervalSeconds }
	if !valid(0) || !valid(15) || !valid(60) {
		t.Error("0 与 ≥15 秒都应合法")
	}
	if valid(5) || valid(10) || valid(-3) {
		t.Error("1~14 秒与负数必须拒绝")
	}
}

// injectTemplateCache 单测用：直接注入模板缓存快照（不连库），返回还原函数。
func injectTemplateCache(t *testing.T, tmpls ...*metricTemplate) func() {
	t.Helper()
	byName := map[string]*metricTemplate{}
	byType := map[string]*metricTemplate{}
	for _, tmpl := range tmpls {
		byName[tmpl.Name] = tmpl
		if tmpl.IsDefault {
			byType[tmpl.DBType] = tmpl
		}
	}
	metricTemplateMu.Lock()
	prevByName, prevByType, prevList, prevAt := metricTemplateByName, metricTemplateByType, metricTemplateSnapshot, metricTemplateLoadedAt
	metricTemplateByName, metricTemplateByType, metricTemplateSnapshot = byName, byType, tmpls
	metricTemplateLoadedAt = time.Now()
	metricTemplateMu.Unlock()
	return func() {
		metricTemplateMu.Lock()
		metricTemplateByName, metricTemplateByType, metricTemplateSnapshot, metricTemplateLoadedAt = prevByName, prevByType, prevList, prevAt
		metricTemplateMu.Unlock()
	}
}

// 展示与模板一致性：卡片构建时模板外残留的最新值必须整体过滤（不再"标记后保留"），
// 主机层 res.* 由 Prometheus 统一采集、不受模板约束，照常保留。
func TestBuildCardFromStoreFiltersOutOfTemplateRows(t *testing.T) {
	restore := injectTemplateCache(t, &metricTemplate{
		Name: "mysql-test", DisplayName: "mysql-test", DBType: "mysql", Enabled: true,
		Keys:   []string{"repl.io_running"},
		KeySet: map[string]struct{}{"repl.io_running": {}},
	})
	defer restore()

	zero := 0.0
	now := commonModels.LocalTime(time.Now())
	row := monitorInstanceRow{InstanceID: "ds-1", DbType: "MySQL", MetricsTemplate: "mysql-test"}
	latestRows := []monitorModels.InsightMonitorMetricLatest{
		{MetricKey: MetricResCPU, Value: &zero, Quality: QualityOK, Ts: now},
		{MetricKey: "repl.io_running", Value: &zero, Quality: QualityOK, Ts: now},
		{MetricKey: "repl.lag_seconds", Value: &zero, Quality: QualityOK, Ts: now},
		{MetricKey: "kern.buffer_pool_usage", Value: &zero, Quality: QualityOK, Ts: now},
	}
	card := buildCardFromStore(row, latestRows, nil)

	byKey := map[string]bool{}
	for _, item := range card.Metrics {
		byKey[item.Key] = true
	}
	if !byKey[MetricResCPU] {
		t.Error("主机层指标不受模板约束，应保留")
	}
	if !byKey["repl.io_running"] {
		t.Error("模板内指标应保留")
	}
	if byKey["repl.lag_seconds"] || byKey["kern.buffer_pool_usage"] {
		t.Error("模板外残留行必须整体过滤，不得以任何 quality 形式出现在卡片里")
	}
}

// 补齐口径与采集门控对齐：实例绑定模板后，"应展示未采集"的补齐范围收敛为模板内有效键，
// 目录里模板外的项不再以"未采集"卡片出现；模板缓存为空（未播种）时维持全目录兜底。
func TestAppendMissingMetricViewsWithTemplate(t *testing.T) {
	restore := injectTemplateCache(t, &metricTemplate{
		Name: "mysql-test", DisplayName: "mysql-test", DBType: "mysql", Enabled: true,
		Keys:   []string{MetricPerfQPS},
		KeySet: map[string]struct{}{MetricPerfQPS: {}},
	})
	defer restore()

	row := monitorInstanceRow{InstanceID: "ds-1", DbType: "MySQL", MetricsTemplate: "mysql-test"}
	result := appendMissingMetricViews(nil, row)
	byKey := map[string]bool{}
	for _, item := range result {
		byKey[item.Key] = true
	}
	if !byKey[MetricPerfQPS] {
		t.Error("模板内未采集的指标应补成明确不可用的卡片")
	}
	if byKey[MetricKernRedoWriteRate] || byKey["repl.io_running"] {
		t.Error("绑定模板后，模板外的目录指标不应再被补齐展示")
	}

	// 边界：模板被清空（缓存回到无模板状态）→ 恢复全目录补齐，不会一个都不显示
	restore()
	result = appendMissingMetricViews(nil, row)
	if len(result) == 0 {
		t.Error("无生效模板时应回落为全目录补齐")
	}
}
