package services

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/monitor/forms"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"gorm.io/gorm"
)

// ============================================================================
// 指标模板
//
// 设计要点（与评审结论一一对应）：
//   1. 模板只管辖"引擎层 + 内核层"指标。主机层（res.*）与数据库类型无关，
//      同一台机器上 MySQL 与 PG 是同一份数据，强行按类型做模板会重复采集或互相打架，
//      因此主机层走旁路：不受模板裁剪，始终按字典全局开关采集。
//   2. 每个类型有一个内置默认模板（is_default=true），实例未绑定或绑定失效时回落 ——
//      "未绑定 = 不采集"会造成完全静默的监控盲区，必须兜底。
//   3. 三层过滤：字典全局启用（总闸）∩ 实例绑定模板（分闸）∩ EngineScope（合法性）。
//      全局停用的指标，任何模板都采不了。
//   4. "未纳入模板"与"不可用"是两种语义：前者不是故障，明细页单独展示，绝不混用同一个标记。
// ============================================================================

// ---------------------------------------------------------------------------
// 指标字典索引：模板校验与展示都要按 key 取元信息
// ---------------------------------------------------------------------------

var builtinMetricByKey = func() map[string]MetricDescriptor {
	index := make(map[string]MetricDescriptor, len(builtinMetricCatalog))
	for _, item := range builtinMetricCatalog {
		index[item.MetricKey] = item
	}
	return index
}()

func metricDescriptorOf(key string) (MetricDescriptor, bool) {
	item, ok := builtinMetricByKey[canonicalMetricKey(key)]
	return item, ok
}

// templateApplicableMetrics 该引擎在模板里"可以勾选"的全部指标（引擎层 + 内核层）。
// 主机层不在其中：不参与模板，也就不该出现在勾选界面里制造误解。
func templateApplicableMetrics(engine string) []MetricDescriptor {
	engineKey := resolveEngine(engine)
	result := make([]MetricDescriptor, 0, 40)
	for _, item := range builtinMetricCatalog {
		if !collectScopeEnabled(item.Scope) {
			continue
		}
		if !engineInScope(engineKey, item.EngineScope) {
			continue
		}
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		leftLayer, rightLayer := metricLayer(result[i].MetricKey), metricLayer(result[j].MetricKey)
		if leftLayer != rightLayer {
			return layerOrder(leftLayer) < layerOrder(rightLayer)
		}
		if result[i].Category != result[j].Category {
			return categoryOrder(result[i].Category) < categoryOrder(result[j].Category)
		}
		return result[i].MetricKey < result[j].MetricKey
	})
	return result
}

// templateApplicableMetricsWithHost 模板编辑界面"可以勾选"的指标全集。
//
// 与 templateApplicableMetrics 的差别：纳入主机层（host）指标，适用于
// MySQL / PostgreSQL / Oracle —— 这三个类型的明细页「基础指标」Tab 展示了 res.*，
// 默认模板已按展示目录同步，若勾选界面看不到主机层，已勾选的 res.* 会被判成
// "该类型当前不适用"的残留项，界面与模板实际内容撕裂。
//
// 主机层由 Prometheus 统一采集，不参与采集范围裁剪，纳入勾选不影响采集行为。
func templateApplicableMetricsWithHost(engine string) []MetricDescriptor {
	base := templateApplicableMetrics(engine)
	engineKey := resolveEngine(engine)
	if !hostMetricsInTemplate(engineKey) {
		return base
	}
	host := make([]MetricDescriptor, 0, 16)
	for _, item := range builtinMetricCatalog {
		if item.Scope != "host" {
			continue
		}
		if !engineInScope(engineKey, item.EngineScope) {
			continue
		}
		host = append(host, item)
	}
	sort.SliceStable(host, func(i, j int) bool {
		if host[i].Category != host[j].Category {
			return categoryOrder(host[i].Category) < categoryOrder(host[j].Category)
		}
		return host[i].MetricKey < host[j].MetricKey
	})
	result := make([]MetricDescriptor, 0, len(host)+len(base))
	result = append(result, host...)
	result = append(result, base...)
	return result
}

// hostMetricsInTemplate 判断该数据库类型的模板是否纳入主机层指标。
//
// 口径：明细页「基础指标」Tab 实际展示主机层指标的类型才纳入 —— MySQL /
// PostgreSQL / Oracle / SQLServer / TiDB。五者的主机层同源（Prometheus + node_exporter，
// 采集侧不区分数据库类型），展示目录也逐项对齐，模板据此勾选才能与明细页一致。
// TiDB 有专属展示目录（tidbDisplayItems），与 MySQL 走同一套模板治理。
// Doris 仍复用 MySQL 目录但不纳入主机层治理（明细不做模板裁剪），
// 其余类型（ClickHouse）没有主机层展示，同样不纳入。
func hostMetricsInTemplate(engine string) bool {
	switch resolveEngine(engine) {
	case "mysql", "postgresql", "kingbase", "oracle", "sqlserver", "tidb":
		return true
	default:
		return false
	}
}

func layerOrder(layer string) int {
	switch layer {
	case LayerHost:
		return 0
	case LayerEngine:
		return 1
	case LayerKernel:
		return 2
	default:
		return 9
	}
}

// ---------------------------------------------------------------------------
// 模板内存缓存
//
// 采集循环与卡片构建都会调用模板解析，每个实例每轮一次；直接查库会让
// 100 实例 × 每个页面请求都产生额外查询。因此加一层带 TTL 的缓存，
// 并在任何模板写操作后立即失效，保证"改完就生效"。
// ---------------------------------------------------------------------------

type metricTemplate struct {
	ID          uint64
	Name        string
	DisplayName string
	DBType      string
	Description string
	Builtin     bool
	IsDefault   bool
	Enabled     bool
	Keys        []string
	KeySet      map[string]struct{}
}

func (t *metricTemplate) contains(key string) bool {
	if t == nil {
		return false
	}
	_, ok := t.KeySet[key]
	return ok
}

const metricTemplateCacheTTL = 15 * time.Second

var (
	metricTemplateMu       sync.RWMutex
	metricTemplateLoadedAt time.Time
	metricTemplateByName   = map[string]*metricTemplate{}
	metricTemplateByType   = map[string]*metricTemplate{}
	metricTemplateSnapshot []*metricTemplate
)

// invalidateMetricTemplateCache 模板写操作后调用：下一次读取立即重新查库。
func invalidateMetricTemplateCache() {
	metricTemplateMu.Lock()
	metricTemplateLoadedAt = time.Time{}
	metricTemplateMu.Unlock()
}

func loadMetricTemplateCache() (map[string]*metricTemplate, map[string]*metricTemplate, []*metricTemplate) {
	metricTemplateMu.RLock()
	fresh := !metricTemplateLoadedAt.IsZero() && time.Since(metricTemplateLoadedAt) < metricTemplateCacheTTL
	if fresh {
		byName, byType, list := metricTemplateByName, metricTemplateByType, metricTemplateSnapshot
		metricTemplateMu.RUnlock()
		return byName, byType, list
	}
	metricTemplateMu.RUnlock()

	byName := map[string]*metricTemplate{}
	byType := map[string]*metricTemplate{}
	list := make([]*metricTemplate, 0, 8)
	if global.App != nil && global.App.DB != nil {
		rows := make([]monitorModels.InsightMonitorMetricTemplates, 0)
		if err := global.App.DB.Order("db_type asc, id asc").Find(&rows).Error; err != nil {
			global.App.Log.Warnf("load metric templates failed: %v", err)
		} else {
			links := make([]monitorModels.InsightMonitorTemplateMetrics, 0)
			if err := global.App.DB.Find(&links).Error; err != nil {
				global.App.Log.Warnf("load template metrics failed: %v", err)
			}
			keyMap := map[uint64][]string{}
			for _, link := range links {
				keyMap[link.TemplateID] = append(keyMap[link.TemplateID], link.MetricKey)
			}
			for index := range rows {
				row := rows[index]
				tmpl := &metricTemplate{
					ID:          row.ID,
					Name:        strings.ToLower(strings.TrimSpace(row.Name)),
					DisplayName: row.DisplayName,
					DBType:      resolveEngine(row.DBType),
					Description: row.Description,
					Builtin:     row.Builtin,
					IsDefault:   row.IsDefault,
					Enabled:     row.Enabled,
					Keys:        keyMap[row.ID],
					KeySet:      map[string]struct{}{},
				}
				if tmpl.Keys == nil {
					tmpl.Keys = make([]string, 0)
				}
				sort.Strings(tmpl.Keys)
				for _, key := range tmpl.Keys {
					tmpl.KeySet[key] = struct{}{}
				}
				list = append(list, tmpl)
				if tmpl.Name != "" {
					byName[tmpl.Name] = tmpl
				}
				if tmpl.IsDefault && tmpl.DBType != "" {
					byType[tmpl.DBType] = tmpl
				}
			}
		}
	}

	metricTemplateMu.Lock()
	metricTemplateByName, metricTemplateByType, metricTemplateSnapshot = byName, byType, list
	metricTemplateLoadedAt = time.Now()
	metricTemplateMu.Unlock()
	// 注意失败方向：查库失败时这里是空集合，resolveTemplateForInstance 会返回 nil，
	// 调用方随即退化为"按字典全量采集"。宁可多采，不可静默少采。
	return byName, byType, list
}

// resolveTemplateForInstance 解析实例实际生效的模板。
//
// 返回值 fallback 表示"绑定标识无效，已回落到该类型默认模板"，
// 用于页面提示，不用来报错 —— 老数据里的 default/oltp/report 会走这条路。
func resolveTemplateForInstance(engine string, boundName string) (tmpl *metricTemplate, fallback bool) {
	byName, byType, _ := loadMetricTemplateCache()
	engineKey := resolveEngine(engine)
	trimmed := strings.ToLower(strings.TrimSpace(boundName))
	// "default" 是旧版前端下拉里的占位值，语义就是"用这个类型的默认模板"
	if trimmed != "" && trimmed != "default" {
		if found, ok := byName[trimmed]; ok && found.Enabled {
			return found, false
		}
	}
	if found, ok := byType[engineKey]; ok {
		return found, true
	}
	return nil, true
}

// metricVisibleUnderCurrentConfig 判定某个指标是否属于"该实例当前配置"的展示范围。
//
// 这是「模板 ∩ 角色」口径的唯一实现，两个调用方必须共用它：
//   - 监控卡片渲染 buildCardFromStore（大屏 / 监控对象卡片）；
//   - 「部分可用」下钻明细 listQualityDegradations。
//
// 分两处各写一遍就一定会漂移：卡片侧早就按模板过滤掉了模板外的指标，而降级明细
// 直接对 insight_monitor_metric_latest 全表查 quality <> 'ok'。于是"裁剪模板 /
// 换绑模板"之后，那些不再被采集的历史行会被永久留在明细里报「取不到」——
// 页面上就表现为"模板里压根没勾集群指标，却提示集群指标获取不到"。
//
// res.* 例外：主机层由 Prometheus + node_exporter 统一采集，不进指标模板，
// 不受模板裁剪影响。
func metricVisibleUnderCurrentConfig(dbType, boundTemplate, instanceRole, metricKey string) bool {
	if strings.HasPrefix(metricKey, "res.") {
		return true
	}
	// 没有默认模板（模板表未播种）→ 不过滤，与"全量采集"的兜底语义一致。
	currentTemplate, _ := resolveTemplateForInstance(dbType, boundTemplate)
	if currentTemplate != nil && !currentTemplate.contains(metricKey) {
		return false
	}
	// 角色适用性：本实例角色下结构性不存在的指标（如单机实例的备库复制延迟），
	// 即便 metric_latest 里还留着升级前写入的历史行也不再展示 —— 那是陈列的旧数据。
	return metricApplicableToRole(resolveEngine(dbType), metricKey, instanceRole)
}

// resolveInstanceMetricKeys 计算实例本轮应采集的指标键（引擎层 + 内核层）。
//
// 四层过滤：字典全局启用 ∩ 实例绑定模板 ∩ EngineScope（由 loadEnabledMetricKeys 完成）
// ∩ 实例角色适用性（由 filterRoleInapplicableKeys 完成）。
// 模板表为空（未播种）时退化为今天的行为：按全局字典全采，不会因为"没有模板"而停摆。
//
// 第四层是"结构性不存在"的唯一执行点：单机实例不应产出备库延迟这类指标，
// 因此它既不采集、不写最新值表、不生成告警，也不会被明细页补成"无数据"卡片
// —— 下游三处（采集器 / appendMissingMetricViews / alertMetricExcludedByTemplate）
// 全部复用本函数，口径天然一致。
//
// 角色未知（instance_role 为空）时不裁剪任何指标：宁可展示一个"无数据"，
// 也不能因为识别不了角色而把真实数据藏起来。
//
// 返回值约定（与 wantMetric 严格对应，不要返回 nil）：
//   - 无模板 → 返回字典全集，表示"全采"；
//   - 有模板 → 返回交集，可能是"非 nil 的空切片"，语义是"本轮一个都不采"。
func resolveInstanceMetricKeys(row monitorInstanceRow) ([]string, *metricTemplate, bool) {
	engineKey := resolveEngine(row.DbType)
	globalKeys := filterRoleInapplicableKeys(engineKey, row.InstanceRole, loadEnabledMetricKeys(row.DbType))
	tmpl, fallback := resolveTemplateForInstance(row.DbType, row.MetricsTemplate)
	if tmpl == nil || len(tmpl.Keys) == 0 {
		return globalKeys, tmpl, fallback
	}
	keys := make([]string, 0, len(tmpl.Keys))
	for _, key := range globalKeys {
		if tmpl.contains(key) {
			keys = append(keys, key)
		}
	}
	return keys, tmpl, fallback
}

// ---------------------------------------------------------------------------
// 指标退役：把"该引擎上结构性取不到数"的指标从所有模板里摘掉
// ---------------------------------------------------------------------------

// unavailableMetricKeysByEngine 各引擎上「采集侧恒定 unsupported」的指标键。
//
// 收录标准很硬，不是"当前环境取不到"，而是**这个引擎根本没有对应的数据源**
// （计数器不存在 / 语义不成立），换版本、换权限、换配置都一样取不到。
// 这类指标留在模板里只会产出恒为「不支持」的空卡：既占版面，又让实例永远挂着
// "部分可用"下不去，还会在降级明细里长期报错。
//
// 与展示目录的关系：改 monitor_display_layout.go 只能同步默认模板
// （默认模板以展示目录为唯一事实来源），运维从旧版默认模板复制出来的自定义模板
// 是不随发版更新的。所以退役必须单独对"该类型的全部模板"执行一遍。
var unavailableMetricKeysByEngine = map[string][]string{
	"tidb": {
		MetricCapConnUsedPct,     // 活跃连接占比：TiDB 的 max_connections 默认 0（不限制连接数），分母不成立
		MetricCapThreadUsedPct,   // 连接使用率：同上
		MetricEngComInsertSelect, // INSERT...SELECT：statements_summary 的 STMT_TYPE 不做此细分
		MetricEngComReplaceSelect,
		MetricEngSelectScan, // 全表扫描次数：TiDB 不提供 Select_scan 计数器
		MetricPerfReadBytes, // 协议流出流量：TiDB 的 SHOW GLOBAL STATUS 不暴露 Bytes_sent
		MetricPerfWriteBytes,
	},
}

// metricKeyRetiredForEngine 该指标键在此引擎上是否已被退役（结构上取不到数）。
//
// 退役表只在「指标退役」与「告警规则裁剪」两处读取：前者摘模板勾选、后者删残留规则。
// 两端共用同一张表，避免出现"模板里没了、告警规则还挂着"的半吊子状态。
func metricKeyRetiredForEngine(engineKey, metricKey string) bool {
	keys, ok := unavailableMetricKeysByEngine[resolveEngine(engineKey)]
	if !ok {
		return false
	}
	for _, key := range keys {
		if key == metricKey {
			return true
		}
	}
	return false
}

// retireUnavailableTemplateMetrics 摘掉已退役指标，并清掉它们的残留最新值。
//
// 幂等：删不到就删不到，重复执行无副作用，只在首次生效时打日志。
// 失败只记日志 —— 这是发版期的整理动作，不该因为它失败就阻断启动。
func retireUnavailableTemplateMetrics() {
	if global.App.DB == nil || len(unavailableMetricKeysByEngine) == 0 {
		return
	}
	retiredLinks, retiredRows := 0, 0
	for engineKey, keys := range unavailableMetricKeysByEngine {
		if len(keys) == 0 {
			continue
		}
		// 覆盖该类型的全部模板（含运维自建的自定义模板），而不是只动默认模板。
		templateIDs := make([]uint64, 0, 4)
		if err := global.App.DB.Table("insight_monitor_metric_templates").
			Where("LOWER(db_type) = ?", engineKey).Pluck("id", &templateIDs).Error; err != nil {
			global.App.Log.Warnf("[monitor] 退役指标：查询 %s 模板失败: %v", engineKey, err)
			continue
		}
		if len(templateIDs) == 0 {
			continue
		}
		result := global.App.DB.
			Where("template_id IN ? AND metric_key IN ?", templateIDs, keys).
			Delete(&monitorModels.InsightMonitorTemplateMetrics{})
		if result.Error != nil {
			global.App.Log.Warnf("[monitor] 退役指标：清理 %s 模板指标失败: %v", engineKey, result.Error)
			continue
		}
		retiredLinks += int(result.RowsAffected)

		// 这些键不会再被采集，metric_latest 里的旧行就是"模板里没有、明细里还在报"的幽灵行。
		// 只删该类型实例 + 这些键，范围精确，不动其它任何数据。
		instanceIDs := make([]string, 0, 8)
		if err := global.App.DB.Table("insight_instances").
			Where("LOWER(db_type) = ?", engineKey).Pluck("instance_id", &instanceIDs).Error; err != nil {
			global.App.Log.Warnf("[monitor] 退役指标：查询 %s 实例失败: %v", engineKey, err)
			continue
		}
		if len(instanceIDs) == 0 {
			continue
		}
		latest := global.App.DB.
			Where("instance_id IN ? AND metric_key IN ?", instanceIDs, keys).
			Delete(&monitorModels.InsightMonitorMetricLatest{})
		if latest.Error != nil {
			global.App.Log.Warnf("[monitor] 退役指标：清理 %s 历史最新值失败: %v", engineKey, latest.Error)
			continue
		}
		retiredRows += int(latest.RowsAffected)
	}
	if retiredLinks > 0 || retiredRows > 0 {
		global.App.Log.Infof("[monitor] 指标退役：移除 %d 项模板勾选，清理 %d 条残留最新值", retiredLinks, retiredRows)
	}
}

// ---------------------------------------------------------------------------
// 播种：每个已实现采集的引擎一个内置默认模板
// ---------------------------------------------------------------------------

// EnsureMetricTemplates 幂等播种默认模板，并随发版同步指标集合。
//
// 与指标字典播种的口径差异（这是刻意的）：
//   - 字典：已存在的行只同步元信息，保留运维改过的 enabled / 阈值；
//   - 默认模板：
//   - MySQL / PostgreSQL / Oracle：以「前端展示目录」为唯一事实来源，模板勾选内容与明细页展示完全一致；
//   - 其他引擎：补齐"字典里有、模板里没有"的指标，让新版本新增的采集项自动生效。
//
// 想裁剪指标只有一条正路：复制成自定义模板后裁剪（模板勾选 ∩ 引擎适用性）。
// 原「在指标字典里全局停用」这条途径已随字典下线。
func EnsureMetricTemplates() error {
	if global.App.DB == nil {
		return nil
	}
	created, topped, synced := 0, 0, 0
	for _, capability := range engineCapabilities {
		if !capability.Implemented {
			continue
		}
		engineKey := capability.Key
		name := engineKey + "-default"
		displayName := capability.Label + " 默认模板"

		var record monitorModels.InsightMonitorMetricTemplates
		err := global.App.DB.Where("name = ?", name).Take(&record).Error
		if err == gorm.ErrRecordNotFound {
			record = monitorModels.InsightMonitorMetricTemplates{
				Model:       &commonModels.Model{},
				Name:        name,
				DisplayName: displayName,
				DBType:      engineKey,
				Description: "内置默认模板：包含该类型全部适用指标，随发版自动补齐新指标",
				Builtin:     true,
				IsDefault:   true,
				Enabled:     true,
			}
			if createErr := global.App.DB.Create(&record).Error; createErr != nil {
				return fmt.Errorf("播种默认模板 %s 失败: %w", name, createErr)
			}
			created++
		} else if err != nil {
			return err
		}

		// MySQL / PostgreSQL / Oracle 默认模板以「展示目录」为唯一事实来源，
		// 保证模板勾选内容与前端明细页实际展示指标完全一致（含主机层 res.*）。
		// 判定条件是"该类型受主机层治理 + 确有展示目录"：没有目录的类型若走这条
		// 分支，replaceTemplateMetrics 会拿空集合把模板清空。
		if targetKeys := displayLayoutMetricKeys(engineKey); hostMetricsInTemplate(engineKey) && len(targetKeys) > 0 {
			if err := replaceTemplateMetrics(record.ID, targetKeys); err != nil {
				return fmt.Errorf("同步默认模板 %s 与展示目录失败: %w", name, err)
			}
			synced += len(targetKeys)
			continue
		}

		// Doris 复用 MySQL 展示目录，但按既有约定不纳入主机层模板治理。
		// 目录里的引擎层 / 内核层键如果缺了，明细页会展示"永远无数据"的卡片
		//（采集被模板门控），因此这里做**增量补齐**：只加不减，避免裁掉
		// 运维在字典上追加、且可能有告警依赖的采集项。
		// TiDB 已有专属展示目录，走上面的「展示目录为唯一事实来源」全量同步分支。
		if engineKey == "doris" {
			existing := map[string]struct{}{}
			links := make([]monitorModels.InsightMonitorTemplateMetrics, 0)
			if err := global.App.DB.Where("template_id = ?", record.ID).Find(&links).Error; err == nil {
				for _, link := range links {
					existing[link.MetricKey] = struct{}{}
				}
			}
			now := commonModels.LocalTime(time.Now())
			additions := make([]monitorModels.InsightMonitorTemplateMetrics, 0, 16)
			for _, key := range displayLayoutMetricKeys(engineKey) {
				if descriptor, ok := metricDescriptorOf(key); ok && descriptor.Scope == "host" {
					continue // 主机层不参与该类型模板治理（与 hostMetricsInTemplate 口径一致）
				}
				if _, ok := existing[key]; ok {
					continue
				}
				additions = append(additions, monitorModels.InsightMonitorTemplateMetrics{
					TemplateID: record.ID, MetricKey: key, CreatedAt: now,
				})
			}
			if len(additions) > 0 {
				if err := global.App.DB.CreateInBatches(&additions, 200).Error; err != nil {
					return fmt.Errorf("补齐默认模板 %s 展示目录指标失败: %w", name, err)
				}
				topped += len(additions)
			}
		}

		existing := map[string]struct{}{}
		links := make([]monitorModels.InsightMonitorTemplateMetrics, 0)
		if err := global.App.DB.Where("template_id = ?", record.ID).Find(&links).Error; err == nil {
			for _, link := range links {
				existing[link.MetricKey] = struct{}{}
			}
		}
		now := commonModels.LocalTime(time.Now())
		additions := make([]monitorModels.InsightMonitorTemplateMetrics, 0, 16)
		for _, item := range templateApplicableMetrics(engineKey) {
			if _, ok := existing[item.MetricKey]; ok {
				continue
			}
			additions = append(additions, monitorModels.InsightMonitorTemplateMetrics{
				TemplateID: record.ID, MetricKey: item.MetricKey, CreatedAt: now,
			})
		}
		if len(additions) > 0 {
			if err := global.App.DB.CreateInBatches(&additions, 200).Error; err != nil {
				return fmt.Errorf("补齐默认模板 %s 指标失败: %w", name, err)
			}
			topped += len(additions)
		}
	}
	if created > 0 || topped > 0 || synced > 0 {
		global.App.Log.Infof("[monitor] 指标模板播种：新增 %d 个默认模板，补齐 %d 项指标，同步 %d 项展示目录指标", created, topped, synced)
	}
	// 退役必须放在播种之后：默认模板刚被展示目录全量同步过，
	// 这里再把"该引擎上取不到数"的键从全部模板（含自定义模板）里摘掉。
	retireUnavailableTemplateMetrics()
	invalidateMetricTemplateCache()
	return nil
}

// ---------------------------------------------------------------------------
// DTO
// ---------------------------------------------------------------------------

// 主机层采集口径说明此前由 metricsTemplateHostPolicyNote 下发，现已整体移除：
// 主机层指标在 MySQL / PostgreSQL 已直接纳入模板可勾选池，其他类型不进模板，
// 勾选界面各自能看出结果，不再需要一段只读说明。对应场景 host_policy 一律下发空串。

type MetricTemplateMetricItem struct {
	MetricKey     string   `json:"metric_key"`
	DisplayName   string   `json:"display_name"`
	Layer         string   `json:"layer"`
	LayerLabel    string   `json:"layer_label"`
	Category      string   `json:"category"`
	CategoryLabel string   `json:"category_label"`
	Unit          string   `json:"unit"`
	SourceType    string   `json:"source_type"`
	ValueType     string   `json:"value_type"`
	EngineScope   string   `json:"engine_scope"`
	Engines       []string `json:"engines"`
	// Enabled 是字典里的"全局启用"总闸：为 false 时，任何模板勾了也采不到
	Enabled bool `json:"enabled"`
	// Available 表示该指标在平台有采集源。监控明细里展示但无采集源的指标
	// 会以 available=false 出现在勾选清单里（不可勾选、不进模板、不参与采集）。
	Available bool `json:"available"`
	// DetailItem 该指标在监控明细中所属的监控项（如 网络流量 / 会话连接），便于对账
	DetailItem string `json:"detail_item"`
	Advice     string `json:"advice"`
}

type MetricTemplateItem struct {
	ID             uint64 `json:"id"`
	Name           string `json:"name"`
	DisplayName    string `json:"display_name"`
	DBType         string `json:"db_type"`
	DBTypeLabel    string `json:"db_type_label"`
	Description    string `json:"description"`
	Builtin        bool   `json:"builtin"`
	IsDefault      bool   `json:"is_default"`
	Enabled        bool   `json:"enabled"`
	MetricCount    int    `json:"metric_count"`
	BoundInstances int    `json:"bound_instances"`
	UpdatedAt      string `json:"updated_at"`
}

type MetricTemplateListData struct {
	GeneratedAt string               `json:"generated_at"`
	DBTypes     []FilterOption       `json:"db_types"`
	Engines     []EngineCapability   `json:"engines"`
	HostPolicy  string               `json:"host_policy"`
	Templates   []MetricTemplateItem `json:"templates"`
}

type MetricTemplateDetailData struct {
	GeneratedAt string                     `json:"generated_at"`
	Template    MetricTemplateItem         `json:"template"`
	Metrics     []MetricTemplateMetricItem `json:"metrics"`
	Applicable  []MetricTemplateMetricItem `json:"applicable"`
	// HostMetrics 仅用于展示"哪些指标不受模板管辖"，前端不可勾选
	HostMetrics []MetricTemplateMetricItem `json:"host_metrics"`
	HostPolicy  string                     `json:"host_policy"`
}

type MetricTemplateBindResult struct {
	TemplateName string `json:"template_name"`
	Bound        int    `json:"bound"`
	Mode         string `json:"mode"`
}

// ---------------------------------------------------------------------------
// 读取
// ---------------------------------------------------------------------------

func metricTemplateItemOf(tmpl *metricTemplate, bound int) MetricTemplateItem {
	label := tmpl.DBType
	if capability, ok := engineCapabilityIndex[tmpl.DBType]; ok {
		label = capability.Label
	}
	return MetricTemplateItem{
		ID:             tmpl.ID,
		Name:           tmpl.Name,
		DisplayName:    tmpl.DisplayName,
		DBType:         tmpl.DBType,
		DBTypeLabel:    label,
		Description:    tmpl.Description,
		Builtin:        tmpl.Builtin,
		IsDefault:      tmpl.IsDefault,
		Enabled:        tmpl.Enabled,
		MetricCount:    len(tmpl.Keys),
		BoundInstances: bound,
	}
}

func metricTemplateMetricItemOf(item MetricDescriptor) MetricTemplateMetricItem {
	layer := metricLayer(item.MetricKey)
	return MetricTemplateMetricItem{
		MetricKey:     item.MetricKey,
		DisplayName:   item.DisplayName,
		Layer:         layer,
		LayerLabel:    layerLabel(layer),
		Category:      item.Category,
		CategoryLabel: categoryLabel(item.Category),
		Unit:          item.Unit,
		SourceType:    item.SourceType,
		ValueType:     item.ValueType,
		EngineScope:   item.EngineScope,
		Engines:       engineLabelsOf(item.EngineScope),
		// 字典总闸已随指标字典下线：有采集源的内置指标一律可勾选，
		// 是否真的采集只由指标模板决定（模板勾选 ∩ 引擎适用性）。
		Enabled: true,
		// 内置指标都有采集源；无采集源的明细行走 displayLayoutApplicableItems 的桩分支
		Available: true,
		Advice:    item.Advice,
	}
}

// detailOnlyMetricKeyPrefix 无采集源明细行的伪键前缀：仅保证前端渲染行唯一，
// 不会进入模板存储（前端禁用勾选；validateTemplateMetricKeys 也会拒绝字典外的键）。
const detailOnlyMetricKeyPrefix = "detail-only:"

// displayLayoutApplicableItems 模板勾选清单的「明细口径」：以监控明细展示目录为
// 唯一事实来源，多值指标逐行展开、保持原始名称/单位/顺序；无采集源的行标记
// available=false 照常列出，绝不静默丢弃，也绝不混进可采集集合。
func displayLayoutApplicableItems(dbType string) []MetricTemplateMetricItem {
	rows := displayLayoutTemplateItems(dbType)
	if len(rows) == 0 {
		return nil
	}
	items := make([]MetricTemplateMetricItem, 0, len(rows))
	for _, row := range rows {
		if row.MetricKey != "" {
			if desc, ok := metricDescriptorOf(row.MetricKey); ok {
				entry := metricTemplateMetricItemOf(desc)
				// 层级以展示目录（row.Tab）为准，不能用 metricTemplateMetricItemOf 里
				// 按 key 前缀推导的结果覆盖：明细页目录把 缓存命中率 / Buffer Pool 使用率 /
				// 锁等待数 / 每秒读行数 等 perf.*/cap.* 键归入 InnoDB 引擎层（kernel），
				// 前缀推导却会给「数据库层」，导致模板勾选界面与监控明细页分类不一致。
				entry.Layer = row.Tab
				entry.LayerLabel = layerLabel(row.Tab)
				entry.Available = true
				entry.DetailItem = row.ItemName
				items = append(items, entry)
				continue
			}
			// 采集键不在字典里（目录与字典漂移，理论上不该发生）：按不可用处理，不静默吞掉
		}
		advice := "监控明细已展示该指标，但平台当前没有采集源，模板不参与采集"
		if row.Definition != "" {
			advice = row.Definition + "；" + advice
		}
		items = append(items, MetricTemplateMetricItem{
			MetricKey:   detailOnlyMetricKeyPrefix + row.ItemKey + "/" + row.SeriesKey,
			DisplayName: row.SeriesKey,
			Layer:       row.Tab,
			LayerLabel:  layerLabel(row.Tab),
			Unit:        row.Unit,
			Enabled:     false,
			Available:   false,
			DetailItem:  row.ItemName,
			Engines:     []string{},
			Advice:      advice,
		})
	}
	return items
}

// displayLayoutCatalogItems 指标目录接口（layout=1）用的明细口径清单。
// 有采集源的行直接复用字典条目（保留全局启用状态/分类/阈值建议等元信息），
// 无采集源的行构造 available=false 的占位条目，绝不静默丢弃。
func displayLayoutCatalogItems(engine string) []MetricCatalogItem {
	rows := displayLayoutTemplateItems(engine)
	if len(rows) == 0 {
		return nil
	}
	base := make(map[string]MetricCatalogItem)
	if items, err := LoadMetricCatalogItems(engine); err == nil {
		for _, item := range items {
			base[item.MetricKey] = item
		}
	}
	result := make([]MetricCatalogItem, 0, len(rows))
	for _, row := range rows {
		if row.MetricKey != "" {
			if item, ok := base[row.MetricKey]; ok {
				item.Available = true
				item.DetailItem = row.ItemName
				// 指标说明兜底：字典 advice 为空时用明细页 definition 补位，
				// 保证规则配置与监控明细对同一指标的解释一致（2026-09-25：
				// perf.conn_count/perf.active_conn_count 在规则配置里说明为空，
				// 而明细页已带「线程 vs 连接」概念边界说明）。
				if strings.TrimSpace(item.Advice) == "" && strings.TrimSpace(row.Definition) != "" {
					item.Advice = row.Definition
				}
				// 层级必须以展示目录（row.Tab）为准：字典行的 Layer 原本是留空、
				// 前端按 key 前缀推导，perf.*/cap.* 键（缓存命中率 / Buffer Pool 使用率 /
				// 锁等待数 / 每秒读行数）会被错标成「数据库层」，与监控明细页不一致。
				// 模板弹窗（layout=1）走的就是这条路径，必须与 displayLayoutApplicableItems 同口径。
				item.Layer = row.Tab
				result = append(result, item)
				continue
			}
			// 采集键不在字典里（目录与字典漂移）：同无采集源处理，不静默吞掉
		}
		advice := "监控明细已展示该指标，但平台当前没有采集源，模板不参与采集"
		if row.Definition != "" {
			advice = row.Definition + "；" + advice
		}
		result = append(result, MetricCatalogItem{
			MetricKey:   detailOnlyMetricKeyPrefix + row.ItemKey + "/" + row.SeriesKey,
			DisplayName: row.SeriesKey,
			Layer:       row.Tab,
			Scope:       row.Tab,
			Unit:        row.Unit,
			Engines:     []string{},
			Available:   false,
			DetailItem:  row.ItemName,
			Advice:      advice,
			Enabled:     false,
		})
	}
	return result
}

// 「指标字典」下线说明：原先这里有 metricCatalogCacheTTL / loadMetricCatalogEnabled /
// metricGloballyEnabled 三个函数，读 insight_monitor_metric_catalog 表拿"全局启用总闸"。
// 字典表已移除，指标是否可采集只由指标模板决定，不再有字典这一层总闸。

// templateByName 按标识精确取模板（含 enabled=false 的）。
//
// 与 resolveTemplateForInstance 的区别很关键：后者是"实例侧解析"，会跳过停用模板
// 回落到默认模板；而模板管理页要编辑的就是那条停用模板本身，不能被回落逻辑顶掉。
func templateByName(name string) *metricTemplate {
	byName, _, _ := loadMetricTemplateCache()
	return byName[strings.ToLower(strings.TrimSpace(name))]
}

// rawBindingCount 统计"配置里字面写到该模板名"的实例数。
// 删除保护用它而不是解析后的归属：停用模板的生效实例算在默认模板头上，
// 但那些实例的绑定值仍然指向它，直接删掉会留下悬空引用。
func rawBindingCount(name string) int {
	if global.App == nil || global.App.DB == nil {
		return 0
	}
	var count int64
	if err := global.App.DB.Model(&monitorModels.InsightMonitorTargetConfigs{}).
		Where("LOWER(TRIM(COALESCE(metrics_template, ''))) = ?", strings.ToLower(strings.TrimSpace(name))).
		Count(&count).Error; err != nil {
		global.App.Log.Warnf("count template bindings failed: %v", err)
		return 0
	}
	return int(count)
}

// templateBindingCounts 统计每个模板实际生效的实例数。
// 口径是"解析后的归属"而不是 metrics_template 字面值：
// 未绑定或绑定失效的实例算在默认模板头上，页面数字才不会漏。
func templateBindingCounts() map[string]int {
	result := map[string]int{}
	if global.App == nil || global.App.DB == nil {
		return result
	}
	type bindRow struct {
		InstanceID      string
		DBType          string
		MetricsTemplate string
	}
	rows := make([]bindRow, 0)
	if err := global.App.DB.Table("insight_monitor_target_configs").
		Select("instance_id, db_type, COALESCE(metrics_template, '') as metrics_template").
		Find(&rows).Error; err != nil {
		global.App.Log.Warnf("load template bindings failed: %v", err)
		return result
	}
	for _, row := range rows {
		tmpl, _ := resolveTemplateForInstance(row.DBType, row.MetricsTemplate)
		if tmpl == nil {
			continue
		}
		result[tmpl.Name]++
	}
	return result
}

type GetMetricTemplateListService struct {
	DBType string
	C      *gin.Context
}

func (s *GetMetricTemplateListService) Run() (*MetricTemplateListData, error) {
	if err := EnsureMetricTemplates(); err != nil {
		global.App.Log.Warnf("seed metric templates failed: %v", err)
	}
	_, _, list := loadMetricTemplateCache()
	counts := templateBindingCounts()
	engineFilter := resolveEngine(s.DBType)

	data := &MetricTemplateListData{
		GeneratedAt: time.Now().Format(time.DateTime),
		Engines:     EngineCapabilityList(),
		Templates:   make([]MetricTemplateItem, 0, len(list)),
	}
	seenTypes := map[string]struct{}{}
	for _, tmpl := range list {
		if engineFilter != "" && tmpl.DBType != engineFilter {
			continue
		}
		item := metricTemplateItemOf(tmpl, counts[tmpl.Name])
		if tmpl.Builtin {
			// 内置模板的更新时间没有运维意义，避免误导"我改过它"
			item.UpdatedAt = ""
		}
		data.Templates = append(data.Templates, item)
		if _, ok := seenTypes[tmpl.DBType]; !ok {
			seenTypes[tmpl.DBType] = struct{}{}
			label := tmpl.DBType
			if capability, ok := engineCapabilityIndex[tmpl.DBType]; ok {
				label = capability.Label
			}
			data.DBTypes = append(data.DBTypes, FilterOption{Value: tmpl.DBType, Label: label})
		}
	}
	sort.SliceStable(data.Templates, func(i, j int) bool {
		left, right := data.Templates[i], data.Templates[j]
		if left.DBType != right.DBType {
			return left.DBType < right.DBType
		}
		if left.IsDefault != right.IsDefault {
			return left.IsDefault
		}
		return left.Name < right.Name
	})
	sort.SliceStable(data.DBTypes, func(i, j int) bool { return data.DBTypes[i].Value < data.DBTypes[j].Value })
	return data, nil
}

type GetMetricTemplateDetailService struct {
	ID uint64
	C  *gin.Context
}

func (s *GetMetricTemplateDetailService) Run() (*MetricTemplateDetailData, error) {
	record, err := findTemplateRecord(s.ID)
	if err != nil {
		return nil, err
	}
	// 用精确查找而不是实例侧解析：停用的模板也要能查看/编辑自己的指标集合
	tmpl := templateByName(record.Name)
	if tmpl == nil {
		return nil, fmt.Errorf("模板 %s 不存在或已被删除", record.Name)
	}
	counts := templateBindingCounts()

	applicable := make([]MetricTemplateMetricItem, 0, 40)
	applicableKeys := map[string]struct{}{}
	// MySQL / PostgreSQL（含共用 MySQL 目录的 TiDB / Doris）：勾选清单改用「明细口径」，
	// 与监控明细页逐行对齐；其余类型维持字典口径。
	if len(displayLayoutTemplateItems(record.DBType)) > 0 {
		applicable = displayLayoutApplicableItems(record.DBType)
	} else {
		for _, item := range templateApplicableMetricsWithHost(record.DBType) {
			applicable = append(applicable, metricTemplateMetricItemOf(item))
		}
	}
	for _, entry := range applicable {
		if entry.Available {
			applicableKeys[entry.MetricKey] = struct{}{}
		}
	}

	// 已勾选：按适用指标的顺序输出，保证前端勾选界面与已选列表口径一致
	selected := make([]MetricTemplateMetricItem, 0, len(tmpl.Keys))
	for _, item := range applicable {
		if tmpl.contains(item.MetricKey) {
			selected = append(selected, item)
		}
	}
	// 模板里可能残留"该类型当前不适用"的历史指标（引擎范围调整后），单独列出而不是静默吞掉
	stale := make([]MetricTemplateMetricItem, 0)
	for _, key := range tmpl.Keys {
		if _, ok := applicableKeys[key]; ok {
			continue
		}
		if descriptor, ok := metricDescriptorOf(key); ok {
			stale = append(stale, metricTemplateMetricItemOf(descriptor))
			continue
		}
		stale = append(stale, MetricTemplateMetricItem{MetricKey: key, DisplayName: key, Enabled: false,
			Engines: []string{}, Advice: "该指标已不在内置指标清单中，采集器不会产出数据"})
	}
	selected = append(selected, stale...)

	// 主机层已纳入可勾选范围的类型，不再单独输出只读的主机层清单，避免界面重复展示
	hostMetrics := make([]MetricTemplateMetricItem, 0, 20)
	// 主机层口径说明已移除，两个分支都不再下发提示文案（详见文件上方注释）
	hostPolicy := ""
	if !hostMetricsInTemplate(record.DBType) {
		for _, item := range builtinMetricCatalog {
			if item.Scope != "host" {
				continue
			}
			hostMetrics = append(hostMetrics, metricTemplateMetricItemOf(item))
		}
	}

	item := metricTemplateItemOf(tmpl, counts[tmpl.Name])
	if !tmpl.Builtin {
		item.UpdatedAt = time.Time(record.UpdatedAt).Format(time.DateTime)
	}
	return &MetricTemplateDetailData{
		GeneratedAt: time.Now().Format(time.DateTime),
		Template:    item,
		Metrics:     selected,
		Applicable:  applicable,
		HostMetrics: hostMetrics,
		HostPolicy:  hostPolicy,
	}, nil
}

func findTemplateRecord(id uint64) (*monitorModels.InsightMonitorMetricTemplates, error) {
	if id == 0 {
		return nil, fmt.Errorf("模板ID不能为空")
	}
	var record monitorModels.InsightMonitorMetricTemplates
	if err := global.App.DB.Where("id = ?", id).Take(&record).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("模板不存在")
		}
		return nil, err
	}
	return &record, nil
}

// ---------------------------------------------------------------------------
// 写入：新建 / 修改 / 删除
// ---------------------------------------------------------------------------

func normalizeTemplateName(value string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(value))
	if name == "" {
		return "", fmt.Errorf("模板标识不能为空")
	}
	if len(name) > 64 {
		return "", fmt.Errorf("模板标识最长 64 个字符")
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return "", fmt.Errorf("模板标识只能包含小写字母、数字、中划线与下划线")
	}
	return name, nil
}

// validateTemplateMetricKeys 校验并归一化模板指标：拒绝不适用该引擎的指标，
// 主机层仅在明细页确实展示主机层指标、且模板纳入主机层治理的引擎
// （MySQL / PostgreSQL / Oracle）允许勾选。
// 这是"前端过滤 + 后端校验"的后半截：只靠前端过滤，手工调接口就能把 PG 指标塞进 MySQL 模板。
func validateTemplateMetricKeys(engine string, keys []string) ([]string, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("至少需要勾选一个指标；若确实不想采集，请直接停用该实例的监控")
	}
	engineKey := resolveEngine(engine)
	seen := map[string]struct{}{}
	result := make([]string, 0, len(keys))
	rejected := make([]string, 0)
	for _, raw := range keys {
		key := canonicalMetricKey(raw)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		descriptor, ok := metricDescriptorOf(key)
		if !ok {
			rejected = append(rejected, key+"（不是平台在册指标）")
			continue
		}
		if !collectScopeEnabled(descriptor.Scope) {
			// MySQL / PostgreSQL 明细页展示了主机层指标，默认模板已同步，允许勾选；其他类型仍拒绝
			if descriptor.Scope != "host" || !hostMetricsInTemplate(engineKey) {
				rejected = append(rejected, key+"（主机层指标与数据库类型无关，不进模板）")
				continue
			}
		}
		if !engineInScope(engineKey, descriptor.EngineScope) {
			rejected = append(rejected, key+"（不适用于 "+engineKey+"）")
			continue
		}
		result = append(result, key)
	}
	if len(rejected) > 0 {
		return nil, fmt.Errorf("以下指标不能加入该模板：%s", strings.Join(rejected, "、"))
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("至少需要勾选一个指标")
	}
	sort.Strings(result)
	return result, nil
}

// replaceTemplateMetrics 全量替换模板的指标集合（事务内先删后插）。
func replaceTemplateMetrics(templateID uint64, keys []string) error {
	now := commonModels.LocalTime(time.Now())
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("template_id = ?", templateID).
			Delete(&monitorModels.InsightMonitorTemplateMetrics{}).Error; err != nil {
			return err
		}
		if len(keys) == 0 {
			return nil
		}
		rows := make([]monitorModels.InsightMonitorTemplateMetrics, 0, len(keys))
		for _, key := range keys {
			rows = append(rows, monitorModels.InsightMonitorTemplateMetrics{
				TemplateID: templateID, MetricKey: key, CreatedAt: now,
			})
		}
		return tx.CreateInBatches(&rows, 200).Error
	})
}

type CreateMetricTemplateService struct {
	*forms.SaveMetricTemplateForm
	C *gin.Context
}

func (s *CreateMetricTemplateService) Run() (*MetricTemplateItem, error) {
	if err := EnsureMetricTemplates(); err != nil {
		global.App.Log.Warnf("seed metric templates failed: %v", err)
	}
	name, err := normalizeTemplateName(s.Name)
	if err != nil {
		return nil, err
	}
	engineKey := resolveEngine(s.DBType)
	if !engineImplemented(engineKey) {
		return nil, fmt.Errorf("暂不支持为 %s 创建指标模板（该引擎尚未实现监控采集）", s.DBType)
	}
	displayName := strings.TrimSpace(s.DisplayName)
	if displayName == "" {
		displayName = name
	}
	var count int64
	if err := global.App.DB.Model(&monitorModels.InsightMonitorMetricTemplates{}).
		Where("name = ?", name).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, fmt.Errorf("模板标识 %s 已存在，请换一个", name)
	}

	// 没显式给指标时，从 copy_from 指定的模板复制（默认取该类型默认模板）：
	// "以默认模板为基础裁剪"是最高频的使用方式。
	keys := make([]string, 0)
	if len(s.MetricKeys) > 0 {
		validated, err := validateTemplateMetricKeys(engineKey, s.MetricKeys)
		if err != nil {
			return nil, err
		}
		keys = validated
	} else {
		source, _ := resolveTemplateForInstance(engineKey, strings.TrimSpace(s.CopyFrom))
		if source == nil {
			return nil, fmt.Errorf("没有可复制的模板，请直接勾选指标")
		}
		for _, key := range source.Keys {
			if descriptor, ok := metricDescriptorOf(key); ok && engineInScope(engineKey, descriptor.EngineScope) {
				keys = append(keys, key)
			}
		}
		if len(keys) == 0 {
			return nil, fmt.Errorf("没有可复制的指标，请直接勾选指标")
		}
		sort.Strings(keys)
	}

	record := monitorModels.InsightMonitorMetricTemplates{
		Model:       &commonModels.Model{},
		Name:        name,
		DisplayName: displayName,
		DBType:      engineKey,
		Description: strings.TrimSpace(s.Description),
		Builtin:     false,
		IsDefault:   false,
		Enabled:     s.Enabled == nil || *s.Enabled,
	}
	if err := global.App.DB.Create(&record).Error; err != nil {
		return nil, fmt.Errorf("创建模板失败：%w", err)
	}
	if err := replaceTemplateMetrics(record.ID, keys); err != nil {
		return nil, fmt.Errorf("保存模板指标失败：%w", err)
	}
	invalidateMetricTemplateCache()

	tmpl := &metricTemplate{
		ID: record.ID, Name: name, DisplayName: displayName, DBType: engineKey,
		Description: record.Description, Builtin: false, IsDefault: false,
		Enabled: record.Enabled, Keys: keys,
	}
	item := metricTemplateItemOf(tmpl, 0)
	item.UpdatedAt = time.Now().Format(time.DateTime)
	return &item, nil
}

type UpdateMetricTemplateService struct {
	ID uint64
	*forms.UpdateMetricTemplateForm
	C *gin.Context
}

func (s *UpdateMetricTemplateService) Run() (*MetricTemplateItem, error) {
	record, err := findTemplateRecord(s.ID)
	if err != nil {
		return nil, err
	}
	if value := strings.TrimSpace(s.Name); value != "" &&
		strings.ToLower(value) != strings.ToLower(strings.TrimSpace(record.Name)) {
		// 模板标识是实例绑定的外键，改它会静默改变一批实例的采集范围
		return nil, fmt.Errorf("模板标识不可修改（实例靠它绑定），如需改名请新建模板后重新绑定")
	}
	updates := map[string]any{}
	if value := strings.TrimSpace(s.DisplayName); value != "" {
		updates["display_name"] = value
	}
	if s.Description != nil {
		updates["description"] = strings.TrimSpace(*s.Description)
	}
	if s.Enabled != nil {
		if record.Builtin && !*s.Enabled {
			return nil, fmt.Errorf("内置默认模板不可停用；如需缩小采集范围请新建自定义模板")
		}
		updates["enabled"] = *s.Enabled
	}
	if s.MetricKeys != nil {
		keys, err := validateTemplateMetricKeys(record.DBType, *s.MetricKeys)
		if err != nil {
			return nil, err
		}
		if record.Builtin {
			// 内置模板被裁剪后，发版补齐会在下次启动时把它改回去，属于静默反复。
			// 与其让运维困惑，不如直接拦住并指向正确做法。
			return nil, fmt.Errorf("内置默认模板的指标集合随发版自动补齐，不可裁剪；请复制为自定义模板后调整")
		}
		if err := replaceTemplateMetrics(record.ID, keys); err != nil {
			return nil, err
		}
		updates["updated_at"] = time.Now()
	}
	if len(updates) > 0 {
		if err := global.App.DB.Model(&monitorModels.InsightMonitorMetricTemplates{}).
			Where("id = ?", record.ID).Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	invalidateMetricTemplateCache()
	refreshed, err := findTemplateRecord(record.ID)
	if err != nil {
		return nil, err
	}
	tmpl := templateByName(refreshed.Name)
	if tmpl == nil {
		return nil, fmt.Errorf("模板读取失败")
	}
	item := metricTemplateItemOf(tmpl, templateBindingCounts()[tmpl.Name])
	item.UpdatedAt = time.Time(refreshed.UpdatedAt).Format(time.DateTime)
	return &item, nil
}

type DeleteMetricTemplateService struct {
	ID uint64
	C  *gin.Context
}

func (s *DeleteMetricTemplateService) Run() error {
	record, err := findTemplateRecord(s.ID)
	if err != nil {
		return err
	}
	if record.Builtin || record.IsDefault {
		return fmt.Errorf("内置默认模板不可删除（实例的兜底模板）")
	}
	// 用"绑定值字面命中"来保护删除：停用的模板不会被算进生效归属，
	// 但那些实例的 metrics_template 仍指向它，直接删会留下悬空引用。
	if raw := rawBindingCount(record.Name); raw > 0 {
		return fmt.Errorf("仍有 %d 个实例绑定该模板，请先在「监控对象」里改绑或回落到默认模板", raw)
	}
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("template_id = ?", record.ID).
			Delete(&monitorModels.InsightMonitorTemplateMetrics{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&monitorModels.InsightMonitorMetricTemplates{}, record.ID).Error; err != nil {
			return err
		}
		invalidateMetricTemplateCache()
		return nil
	})
}

// ---------------------------------------------------------------------------
// 实例绑定
// ---------------------------------------------------------------------------

type BindTemplateInstancesService struct {
	ID uint64
	*forms.BindTemplateInstancesForm
	C *gin.Context
}

// Run 批量绑定 / 回落默认模板。
//
// 绑定时强校验"实例引擎 == 模板归属引擎"：MySQL 实例挂 PG 模板看似无害，
// 实际会让引擎层指标被两层过滤全部裁掉，最终表现为"采集正常但没有数据"。
func (s *BindTemplateInstancesService) Run() (*MetricTemplateBindResult, error) {
	record, err := findTemplateRecord(s.ID)
	if err != nil {
		return nil, err
	}
	if len(s.InstanceIDs) == 0 {
		return nil, fmt.Errorf("请选择要操作的实例")
	}
	mode := strings.ToLower(strings.TrimSpace(s.Mode))
	if mode == "" {
		mode = "bind"
	}
	engineKey := resolveEngine(record.DBType)

	type instanceRow struct {
		InstanceID string
		DbType     string
	}
	instances := make([]instanceRow, 0, len(s.InstanceIDs))
	if err := global.App.DB.Table("insight_instances").
		Select("instance_id, db_type").
		Where("instance_id IN ?", s.InstanceIDs).
		Find(&instances).Error; err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("所选实例不存在")
	}

	bound := 0
	for _, instance := range instances {
		if mode == "bind" && resolveEngine(instance.DbType) != engineKey {
			return nil, fmt.Errorf("实例 %s 是 %s，不能绑定 %s 模板",
				instance.InstanceID, instance.DbType, record.DBType)
		}
		target := record.Name
		if mode == "unbind" {
			target = "" // 空值 = 回落到该类型默认模板
		}
		if err := upsertTemplateBinding(instance.InstanceID, instance.DbType, target); err != nil {
			return nil, err
		}
		bound++
	}
	invalidateMetricTemplateCache()
	return &MetricTemplateBindResult{TemplateName: record.Name, Bound: bound, Mode: mode}, nil
}

// upsertTemplateBinding 写入实例的模板绑定；没有配置行时补建一行（与 ensureTargetConfigRows 同口径）。
func upsertTemplateBinding(instanceID string, dbType string, templateName string) error {
	var existing monitorModels.InsightMonitorTargetConfigs
	err := global.App.DB.Where("instance_id = ?", instanceID).Take(&existing).Error
	if err == nil {
		return global.App.DB.Model(&monitorModels.InsightMonitorTargetConfigs{}).
			Where("instance_id = ?", instanceID).
			Updates(map[string]any{"metrics_template": templateName}).Error
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}
	record := monitorModels.InsightMonitorTargetConfigs{
		Model:                 &commonModels.Model{},
		InstanceID:            instanceID,
		Enabled:               true,
		DBType:                commonModels.EnumType(dbType),
		ResourceSource:        "prometheus",
		EngineSource:          "direct",
		ScrapeIntervalSeconds: 0, // 跟随平台周期：默认值不物化，平台改周期时自动跟上
		RetentionHours:        defaultRetentionHours,
		TimeoutSeconds:        defaultTimeoutSeconds,
		RetryCount:            2,
		MetricsTemplate:       templateName,
		DeployMode:            "standalone",
		LastCollectStatus:     "never",
		CollectQuality:        QualityNoData,
	}
	return global.App.DB.Create(&record).Error
}

// normalizeTemplateBinding 归一化实例保存时传入的模板标识。
// 空值与旧版的 "default" 都解析为该类型的默认模板名，保证老前端不会写进无效值。
func normalizeTemplateBinding(engine string, name string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	if trimmed == "" || trimmed == "default" {
		if tmpl, _ := resolveTemplateForInstance(engine, ""); tmpl != nil {
			return tmpl.Name, nil
		}
		return "", nil
	}
	tmpl, _ := resolveTemplateForInstance(engine, trimmed)
	_, _, list := loadMetricTemplateCache()
	if len(list) == 0 {
		// 模板还没播种（迁移尚未完成）：不做校验，避免把实例上的旧值改坏
		return trimmed, nil
	}
	if tmpl == nil || tmpl.Name != trimmed {
		return "", fmt.Errorf("模板 %s 不存在，请先在「指标模板」中创建", trimmed)
	}
	if resolveEngine(tmpl.DBType) != resolveEngine(engine) {
		return "", fmt.Errorf("模板 %s 归属 %s，不能用于 %s 实例", tmpl.Name, tmpl.DBType, engine)
	}
	return tmpl.Name, nil
}

// defaultTemplateName 该引擎的默认模板标识。
// 表里还没有模板（未播种）时返回空串，由调用方按"未绑定"处理并回落到全局字典。
func defaultTemplateName(engine string) string {
	if tmpl, _ := resolveTemplateForInstance(engine, ""); tmpl != nil {
		return tmpl.Name
	}
	return ""
}

// templateInfoForInstance 页面展示用的模板信息（标识失效时给出回落说明，不静默）。
// 模板绑定状态（下发给页面，前端只按状态渲染，判定口径只此一处）。
//
// 为什么要区分 Fallback 和 Unbound：两者后端行为相同（都用类型默认模板），
// 但语义完全不同 —— Unbound 是"本来就该这样"的健康状态，Fallback 是"配置失效了"的异常。
// 旧实现只发一个 Fallback bool，把两者混为一谈，页面把健康实例也标成橙色告警，
// 久了运维就对这个标签免疫了（狼来了）。
const (
	// TemplateStateBound 显式绑定且生效中的具名模板
	TemplateStateBound = "bound"
	// TemplateStateUnbound 未绑定（空值或旧版占位值 default），使用该类型默认模板，属正常状态
	TemplateStateUnbound = "unbound"
	// TemplateStateFallback 绑定值非空但已失效（模板被删/停用/改名），回落到默认模板，需人工处理
	TemplateStateFallback = "fallback"
)

type instanceTemplateInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	DBType      string `json:"db_type"`
	IsDefault   bool   `json:"is_default"`
	Builtin     bool   `json:"builtin"`
	MetricCount int    `json:"metric_count"`
	// BoundName 是实例配置里写的绑定值，可能与实际生效的模板不同
	BoundName string `json:"bound_name"`
	Fallback  bool   `json:"fallback"`
	// State 绑定状态枚举 bound / unbound / fallback，页面按它决定展示哪种标签
	State   string `json:"state"`
	Message string `json:"message"`
}

// classifyTemplateState 由绑定字面值 + 解析结果得出绑定状态。
// boundName 需已做 trim + lower。
// 注意：'default' 占位值已于 2026-09-18 全量迁移为各类型真实默认模板名，
// 且新数据不可能再出现（模型列默认值为空串、保存路径 normalizeTemplateBinding 归一化），
// 这里保留识别分支纯粹是防御历史库/手工改库的旧值。
func classifyTemplateState(boundName string, fallback bool) string {
	// "default" 是旧版前端下拉的占位值，语义就是"用类型默认模板" → 属于未绑定
	if boundName == "" || boundName == "default" {
		return TemplateStateUnbound
	}
	if fallback {
		return TemplateStateFallback
	}
	return TemplateStateBound
}

func templateInfoForInstance(row monitorInstanceRow) instanceTemplateInfo {
	tmpl, fallback := resolveTemplateForInstance(row.DbType, row.MetricsTemplate)
	boundName := strings.ToLower(strings.TrimSpace(row.MetricsTemplate))
	info := instanceTemplateInfo{
		BoundName: boundName,
		Fallback:  fallback,
		State:     classifyTemplateState(boundName, fallback),
	}
	if tmpl != nil {
		info.Name = tmpl.Name
		info.DisplayName = tmpl.DisplayName
		info.DBType = tmpl.DBType
		info.IsDefault = tmpl.IsDefault
		info.Builtin = tmpl.Builtin
		info.MetricCount = len(tmpl.Keys)
	}
	switch {
	case tmpl == nil:
		info.Message = "尚未配置指标模板，当前按内置指标全集采集（建议在「指标模板」中确认）"
	case info.State == TemplateStateFallback:
		info.Message = fmt.Sprintf("绑定的模板 %s 不存在或已停用，当前回落到默认模板 %s", boundName, tmpl.Name)
	case info.State == TemplateStateUnbound:
		info.Message = fmt.Sprintf("未绑定指标模板，当前使用该类型的默认模板 %s", tmpl.Name)
	}
	return info
}

// outOfTemplateMetrics 该实例"类型上适用、但不在模板内"的指标。
//
// 这是"不静默缺数"的落地点：模板外指标不显示为故障，但必须让运维看得见，
// 否则"忘了勾选"会和"指标采集失败"混成一团，谁也不知道到底少了什么。
func outOfTemplateMetrics(row monitorInstanceRow, limit int) []MetricTemplateMetricItem {
	tmpl, _ := resolveTemplateForInstance(row.DbType, row.MetricsTemplate)
	// 本角色下结构性不存在的指标连"模板未勾选"都不该列出来：
	// 那等于提示运维去勾选一个采不到、也不该采的指标。
	engineKey := resolveEngine(row.DbType)
	result := make([]MetricTemplateMetricItem, 0)
	for _, item := range templateApplicableMetrics(row.DbType) {
		if !metricApplicableToRole(engineKey, item.MetricKey, row.InstanceRole) {
			continue
		}
		if tmpl != nil && tmpl.contains(item.MetricKey) {
			continue
		}
		result = append(result, metricTemplateMetricItemOf(item))
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result
}
