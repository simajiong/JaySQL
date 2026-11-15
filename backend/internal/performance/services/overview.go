package services

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/performance/forms"
)

type overviewSlowSQLSummary struct {
	Count       int64   `json:"count"`
	AvgDuration float64 `json:"avg_duration"`
	MaxDuration int64   `json:"max_duration"`
}

type overviewTopInstance struct {
	InstanceID      string  `json:"instance_id"`
	Remark          string  `json:"remark"`
	Target          string  `json:"target"`
	DBType          string  `json:"db_type"`
	EnvironmentName string  `json:"environment_name"`
	SlowSQLCount    int64   `json:"slow_sql_count"`
	AvgDuration     float64 `json:"avg_duration"`
	MaxDuration     int64   `json:"max_duration"`
}

type overviewUnsupportedGroup struct {
	DBType string `json:"db_type"`
	Count  int64  `json:"count"`
}

type GetOverviewService struct {
	Form     *forms.GetOverviewForm
	C        *gin.Context
	Username string
}

func (s *GetOverviewService) Run() (map[string]any, error) {
	instances, err := queryPerformanceInstances(s.Form.Search)
	if err != nil {
		return nil, err
	}

	// —— 顶部筛选：数据库类型 / 业务线（对整页数据生效） ——
	dbTypeFilter := strings.TrimSpace(s.Form.DBType)
	businessFilter := strings.TrimSpace(s.Form.Business)
	bizIndex := loadPerformanceBusinessIndex()
	if dbTypeFilter != "" || businessFilter != "" {
		filtered := make([]instanceOptionRow, 0, len(instances))
		for _, item := range instances {
			if dbTypeFilter != "" && item.DBType != dbTypeFilter {
				continue
			}
			if businessFilter != "" && bizIndex.byInstance[item.InstanceID] != businessFilter {
				continue
			}
			filtered = append(filtered, item)
		}
		instances = filtered
	}

	supportedInstances := make([]instanceOptionRow, 0, len(instances))
	unsupportedInstances := make([]instanceOptionRow, 0)
	supportedCountByDBType := map[string]int{
		"MySQL":      0,
		"PostgreSQL": 0,
	}
	for _, item := range instances {
		if item.Supported {
			supportedInstances = append(supportedInstances, item)
			supportedCountByDBType[strings.TrimSpace(item.DBType)]++
			continue
		}
		unsupportedInstances = append(unsupportedInstances, item)
	}

	var slowSummary overviewSlowSQLSummary
	slowSummaryTx := global.App.DB.Table("insight_performance_slowlog_entries r").
		Select("count(*) as count, ifnull(avg(r.query_time_ms), 0) as avg_duration, ifnull(max(r.query_time_ms), 0) as max_duration").
		Joins("join insight_instances i on i.instance_id = r.instance_id").
		// 慢SQL 概览同样覆盖三类引擎，口径与明细/统计保持一致
		Where("r.db_type in ?", supportedDBTypes).
		Where("r.event_time >= DATE_SUB(NOW(), INTERVAL 1 DAY)")
	if keyword := strings.TrimSpace(s.Form.Search); keyword != "" {
		like := "%" + keyword + "%"
		slowSummaryTx = slowSummaryTx.Where(
			"i.instance_id like ? or i.remark like ? or i.hostname like ? or r.schema_name like ? or r.sql_text like ?",
			like, like, like, like, like,
		)
	}
	// 概览的汇总数字同样只能覆盖「当前账号被授权可见」的实例：
	if err := slowSummaryTx.Scan(&slowSummary).Error; err != nil {
		return nil, err
	}

	var topInstances []overviewTopInstance
	topInstancesTx := global.App.DB.Table("insight_performance_slowlog_entries r").
		Select(`
			i.instance_id,
			i.remark,
			concat(i.hostname, ':', i.port) as target,
			i.db_type,
			ifnull(env.name, '') as environment_name,
			count(*) as slow_sql_count,
                        ifnull(avg(r.query_time_ms), 0) as avg_duration,
                        ifnull(max(r.query_time_ms), 0) as max_duration
		`).
		Joins("join insight_instances i on i.instance_id = r.instance_id").
		Joins("left join insight_instance_environments env on env.id = i.environment").
		Where("r.db_type in ?", supportedDBTypes).
		Where("r.event_time >= DATE_SUB(NOW(), INTERVAL 1 DAY)").
		Group("i.instance_id, i.remark, i.hostname, i.port, i.db_type, env.name").
		Order("max_duration desc, slow_sql_count desc").
		Limit(8)
	if keyword := strings.TrimSpace(s.Form.Search); keyword != "" {
		like := "%" + keyword + "%"
		topInstancesTx = topInstancesTx.Where(
			"i.instance_id like ? or i.remark like ? or i.hostname like ? or r.schema_name like ? or r.sql_text like ?",
			like, like, like, like, like,
		)
	}
	if err := topInstancesTx.Scan(&topInstances).Error; err != nil {
		return nil, err
	}

	var unsupportedGroups []overviewUnsupportedGroup
	unsupportedGroupTx := global.App.DB.Table("insight_instances i").
		Select("i.db_type, count(*) as count").
		Where("i.db_type not in ?", supportedDBTypes).
		Group("i.db_type").
		Order("count desc, i.db_type asc")
	if keyword := strings.TrimSpace(s.Form.Search); keyword != "" {
		like := "%" + keyword + "%"
		unsupportedGroupTx = unsupportedGroupTx.Where("i.instance_id like ? or i.remark like ? or i.hostname like ?", like, like, like)
	}
	if err := unsupportedGroupTx.Scan(&unsupportedGroups).Error; err != nil {
		return nil, err
	}

	var snapshotParamStats struct {
		Total         int64
		EnabledCount  int64
		InstanceCount int64
	}
	if err := global.App.DB.Table("insight_performance_snapshot_params").
		Select(`
			count(*) as total,
			sum(case when enabled = 1 then 1 else 0 end) as enabled_count,
			sum(case when scope_type = 'instance' then 1 else 0 end) as instance_count
		`).
		Scan(&snapshotParamStats).Error; err != nil {
		return nil, err
	}

	// 慢日志能力分层：哪些实例真的能采、哪些需要提醒
	slowLogCounts, slowLogAttention := splitSlowLogAttention(supportedInstances)

	// —— 概览仪表盘：KPI / 慢SQL趋势 / 资源瓶颈Top5 / 实例性能明细 ——
	dashboard := buildPerformanceDashboard(supportedInstances, s.Form.RangeDays)

	return map[string]any{
		"supported_db_types": supportedDBTypes,
		"business_options":   bizIndex.list,
		"summary": map[string]any{
			"total_instances":         len(instances),
			"supported_instances":     len(supportedInstances),
			"unsupported_instances":   len(unsupportedInstances),
			"mysql_instances":         supportedCountByDBType["MySQL"],
			"postgresql_instances":    supportedCountByDBType["PostgreSQL"],
			"recent_slow_sql_count":   slowSummary.Count,
			"recent_avg_duration_ms":  slowSummary.AvgDuration,
			"recent_max_duration_ms":  slowSummary.MaxDuration,
			"snapshot_param_total":    snapshotParamStats.Total,
			"snapshot_param_enabled":  snapshotParamStats.EnabledCount,
			"snapshot_param_instance": snapshotParamStats.InstanceCount,

			"slowlog_collectable":     slowLogCounts[capabilityEnabledFile],
			"slowlog_table_only":      slowLogCounts[capabilityEnabledTable],
			"slowlog_disabled":        slowLogCounts[capabilityDisabled],
			"slowlog_probe_failed":    slowLogCounts[capabilityProbeFailed],
			"slowlog_file_missing":    slowLogCounts[capabilityFileMissing],
			"slowlog_attention_total": len(slowLogAttention),
		},
		"supported_instances":         supportedInstances,
		"unsupported_instances":       unsupportedInstances,
		"unsupported_groups":          unsupportedGroups,
		"top_slow_instances":          topInstances,
		"slowlog_attention_instances": slowLogAttention,

		"kpis":             dashboard.kpis,
		"slow_trend":       dashboard.slowTrend,
		"top_resource":     dashboard.topResource,
		"detail_instances": dashboard.detailInstances,
	}, nil
}

// ============================================================================
// 概览仪表盘：KPI 卡 / 慢SQL数量趋势 / 资源瓶颈 Top5 / 实例性能明细
//
// 数据源全部复用现有链路，不新增采集：
//   - 性能指标（QPS/TPS/CPU/内存/连接水位）：insight_monitor_metric_latest（quality='ok'）
//   - 慢SQL（条数/占比/趋势/均值）：insight_performance_slowlog_entries（近 24h / 近 N 天）
// 实例展示名口径与平台一致：Remark 优先，回落 hostname:port。
// ============================================================================

// overviewBizIndex 业务线索引：下拉选项 + 实例→业务映射。
type overviewBizIndex struct {
	list       []string
	byInstance map[string]string
}

func loadPerformanceBusinessIndex() overviewBizIndex {
	index := overviewBizIndex{list: []string{}, byInstance: map[string]string{}}
	type row struct {
		InstanceID string
		Business   string
	}
	var rows []row
	if err := global.App.DB.Table("insight_monitor_target_configs").
		Select("instance_id, business").
		Where("business is not null and business <> ''").
		Scan(&rows).Error; err != nil {
		return index
	}
	seen := map[string]struct{}{}
	for _, item := range rows {
		business := strings.TrimSpace(item.Business)
		if business == "" {
			continue
		}
		index.byInstance[item.InstanceID] = business
		if _, ok := seen[business]; !ok {
			seen[business] = struct{}{}
			index.list = append(index.list, business)
		}
	}
	sort.Strings(index.list)
	return index
}

type overviewKPIs struct {
	TotalInstances   int     `json:"total_instances"`
	MysqlInstances   int     `json:"mysql_instances"`
	PgInstances      int     `json:"postgresql_instances"`
	AvgQPS           float64 `json:"avg_qps"`
	PeakQPS          float64 `json:"peak_qps"`
	AvgRTMS          float64 `json:"avg_rt_ms"`
	MaxRTMS          float64 `json:"max_rt_ms"`
	SlowRatioPct     float64 `json:"slow_ratio_pct"`
	SlowCount24h     int64   `json:"slow_count_24h"`
	ConnUsedPct      float64 `json:"conn_used_pct"`
	ConnCurrentTotal int64   `json:"conn_current_total"`
	ConnMaxTotal     int64   `json:"conn_max_total"`
}

type overviewTrendPoint struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

type overviewResourceRow struct {
	InstanceID string  `json:"instance_id"`
	Name       string  `json:"name"`
	Target     string  `json:"target"`
	DBType     string  `json:"db_type"`
	CPU        float64 `json:"cpu"`
	Memory     float64 `json:"memory"`
	// 以下指针为 nil 表示该实例无此指标的有效采集数据（quality!=ok 或未采集）
	DiskUsedPct  *float64 `json:"disk_used_pct"`   // 磁盘使用率 %
	DiskUsedByte *float64 `json:"disk_used_bytes"` // 磁盘使用量字节（前端换算 GB/TB 展示）
	IOPS         *float64 `json:"iops"`            // 磁盘 IOPS
	SlowCount    int64    `json:"slow_count"`      // 近 24h 慢SQL条数
}

type overviewDetailRow struct {
	InstanceID  string  `json:"instance_id"`
	Name        string  `json:"name"`
	Target      string  `json:"target"`
	DBType      string  `json:"db_type"`
	QPS         float64 `json:"qps"`
	TPS         float64 `json:"tps"`
	AvgRTMS     float64 `json:"avg_rt_ms"`
	CPU         float64 `json:"cpu"`
	Memory      float64 `json:"memory"`
	SlowSQL24h  int64   `json:"slow_sql_24h"`
	ConnUsedPct float64 `json:"conn_used_pct"`
	Level       string  `json:"level"` // danger / warn / normal
}

type overviewDashboard struct {
	kpis            overviewKPIs
	slowTrend       []overviewTrendPoint
	topResource     []overviewResourceRow
	detailInstances []overviewDetailRow
}

// performanceWaterLevel 性能水位分级：任一硬指标越限=危险；接近越限或慢SQL量大=预警。
func performanceWaterLevel(cpu, memory, connUsedPct float64, slowCount int64) string {
	if cpu >= 85 || memory >= 90 || connUsedPct >= 85 {
		return "danger"
	}
	if cpu >= 70 || memory >= 80 || connUsedPct >= 70 || slowCount >= 100 {
		return "warn"
	}
	return "normal"
}

func buildPerformanceDashboard(supported []instanceOptionRow, rangeDays int) overviewDashboard {
	dash := overviewDashboard{
		slowTrend:       []overviewTrendPoint{},
		topResource:     []overviewResourceRow{},
		detailInstances: []overviewDetailRow{},
	}
	dash.kpis.TotalInstances = len(supported)
	for _, item := range supported {
		switch item.DBType {
		case "MySQL":
			dash.kpis.MysqlInstances++
		case "PostgreSQL":
			dash.kpis.PgInstances++
		case "Kingbase":
			// 金仓走 PG 通道，性能总览归入 PG 实例计数（指标口径与 PG 相同）
			dash.kpis.PgInstances++
		}
	}
	if len(supported) == 0 {
		return dash
	}
	if rangeDays != 3 && rangeDays != 7 && rangeDays != 14 && rangeDays != 30 {
		rangeDays = 7
	}

	ids := make([]string, 0, len(supported))
	targets := map[string]string{}
	names := map[string]string{}
	for _, item := range supported {
		ids = append(ids, item.InstanceID)
		targets[item.InstanceID] = fmt.Sprintf("%s:%d", item.Hostname, item.Port)
		name := strings.TrimSpace(item.Remark)
		if name == "" {
			name = targets[item.InstanceID]
		}
		names[item.InstanceID] = name
	}

	// —— 实时性能指标（监控侧 metric_latest，五态 quality=ok 才可信） ——
	type metricRow struct {
		InstanceID string
		MetricKey  string
		Value      float64
	}
	var metricRows []metricRow
	if err := global.App.DB.Table("insight_monitor_metric_latest").
		Select("instance_id, metric_key, value").
		Where("instance_id in ?", ids).
		Where("metric_key in ?", []string{
			"perf.qps", "perf.tps",
			"res.cpu", "res.memory",
			"cap.conn_used_pct", "perf.conn_count", "perf.max_conn_count",
			"res.disk", "res.disk_used", "res.disk_iops",
		}).
		Where("quality = ?", "ok").
		Where("value is not null").
		Scan(&metricRows).Error; err != nil {
		return dash
	}
	metrics := map[string]map[string]float64{}
	for _, row := range metricRows {
		if metrics[row.InstanceID] == nil {
			metrics[row.InstanceID] = map[string]float64{}
		}
		metrics[row.InstanceID][row.MetricKey] = row.Value
	}

	// —— 近 24h 慢SQL：按实例聚合 ——
	type instSlow struct {
		InstanceID string
		Cnt        int64
		AvgMS      float64
		MaxMS      float64
	}
	var instSlows []instSlow
	if err := global.App.DB.Table("insight_performance_slowlog_entries").
		Select("instance_id, count(*) as cnt, ifnull(avg(query_time_ms),0) as avg_ms, ifnull(max(query_time_ms),0) as max_ms").
		Where("instance_id in ?", ids).
		Where("event_time >= DATE_SUB(NOW(), INTERVAL 1 DAY)").
		Group("instance_id").
		Scan(&instSlows).Error; err != nil {
		return dash
	}
	slowByInstance := map[string]instSlow{}
	var slowTotal24h int64
	for _, item := range instSlows {
		slowByInstance[item.InstanceID] = item
		slowTotal24h += item.Cnt
	}
	dash.kpis.SlowCount24h = slowTotal24h

	// —— KPI 汇总 ——
	var qpsSum, peakQPS, rtSum float64
	var rtCount int
	var connCurSum, connMaxSum float64
	for _, id := range ids {
		m := metrics[id]
		if v := m["perf.qps"]; v > 0 {
			qpsSum += v
			if v > peakQPS {
				peakQPS = v
			}
		}
		if slow, ok := slowByInstance[id]; ok && slow.Cnt > 0 {
			rtSum += slow.AvgMS
			rtCount++
			if slow.MaxMS > dash.kpis.MaxRTMS {
				dash.kpis.MaxRTMS = slow.MaxMS
			}
		}
		connCurSum += m["perf.conn_count"]
		connMaxSum += m["perf.max_conn_count"]
	}
	if n := len(ids); n > 0 {
		dash.kpis.AvgQPS = math.Round(qpsSum / float64(n))
	}
	if rtCount > 0 {
		dash.kpis.AvgRTMS = math.Round(rtSum/float64(rtCount)*100) / 100
	}
	dash.kpis.PeakQPS = math.Round(peakQPS)
	// 慢SQL占比：24h 慢SQL条数 ÷（当前 QPS 合计 × 86400）——QPS 为瞬时值，占比是估算口径
	if totalQPS := qpsSum; totalQPS > 0 {
		dash.kpis.SlowRatioPct = math.Round(float64(slowTotal24h)/(totalQPS*86400)*10000) / 100
	}
	if connMaxSum > 0 {
		dash.kpis.ConnUsedPct = math.Round(connCurSum/connMaxSum*1000) / 10
	}
	dash.kpis.ConnCurrentTotal = int64(math.Round(connCurSum))
	dash.kpis.ConnMaxTotal = int64(math.Round(connMaxSum))

	// —— 慢SQL数量趋势（按天分桶，缺数日期补 0） ——
	type trendRow struct {
		Day   string
		Count int64
	}
	var trendRows []trendRow
	if err := global.App.DB.Table("insight_performance_slowlog_entries").
		Select("DATE_FORMAT(event_time, '%Y-%m-%d') as day, count(*) as count").
		Where("instance_id in ?", ids).
		Where(fmt.Sprintf("event_time >= DATE_SUB(NOW(), INTERVAL %d DAY)", rangeDays)).
		Group("day").
		Order("day asc").
		Scan(&trendRows).Error; err != nil {
		return dash
	}
	countByDay := map[string]int64{}
	for _, row := range trendRows {
		countByDay[row.Day] = row.Count
	}
	today := time.Now()
	for offset := rangeDays - 1; offset >= 0; offset-- {
		day := today.AddDate(0, 0, -offset).Format("2006-01-02")
		dash.slowTrend = append(dash.slowTrend, overviewTrendPoint{
			Date:  day[5:],
			Count: countByDay[day],
		})
	}

	// —— 实例性能明细 + 资源瓶颈 Top5 ——
	for _, item := range supported {
		m := metrics[item.InstanceID]
		slow := slowByInstance[item.InstanceID]
		row := overviewDetailRow{
			InstanceID:  item.InstanceID,
			Name:        names[item.InstanceID],
			Target:      targets[item.InstanceID],
			DBType:      item.DBType,
			QPS:         math.Round(m["perf.qps"]),
			TPS:         math.Round(m["perf.tps"]),
			AvgRTMS:     math.Round(slow.AvgMS*100) / 100,
			CPU:         math.Round(m["res.cpu"]*10) / 10,
			Memory:      math.Round(m["res.memory"]*10) / 10,
			SlowSQL24h:  slow.Cnt,
			ConnUsedPct: math.Round(m["cap.conn_used_pct"]*10) / 10,
		}
		row.Level = performanceWaterLevel(row.CPU, row.Memory, row.ConnUsedPct, row.SlowSQL24h)
		dash.detailInstances = append(dash.detailInstances, row)
	}
	// 按性能水位排序：危险 > 预警 > 正常，同级内 CPU 降序
	levelRank := map[string]int{"danger": 0, "warn": 1, "normal": 2}
	sort.SliceStable(dash.detailInstances, func(i, j int) bool {
		li, lj := levelRank[dash.detailInstances[i].Level], levelRank[dash.detailInstances[j].Level]
		if li != lj {
			return li < lj
		}
		return dash.detailInstances[i].CPU > dash.detailInstances[j].CPU
	})

	// Top5 资源瓶颈：返回全部实例的各指标值，由前端按所选指标
	// （CPU使用率/内存使用率/磁盘使用率/磁盘使用量/IOPS）排序取前 5
	metricPtr := func(m map[string]float64, key string) *float64 {
		if v, ok := m[key]; ok {
			return &v
		}
		return nil
	}
	for _, row := range dash.detailInstances {
		m := metrics[row.InstanceID]
		dash.topResource = append(dash.topResource, overviewResourceRow{
			InstanceID:   row.InstanceID,
			Name:         row.Name,
			Target:       row.Target,
			DBType:       row.DBType,
			CPU:          row.CPU,
			Memory:       row.Memory,
			DiskUsedPct:  metricPtr(m, "res.disk"),
			DiskUsedByte: metricPtr(m, "res.disk_used"),
			IOPS:         metricPtr(m, "res.disk_iops"),
			SlowCount:    row.SlowSQL24h,
		})
	}
	return dash
}
