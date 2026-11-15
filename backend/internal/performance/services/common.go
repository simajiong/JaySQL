package services

import (
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/global"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"gorm.io/datatypes"
)

// supportedDBTypes 性能分析模块（慢 SQL）支持的数据库类型。
//
// 口径与 slowLogEngineSupported 保持一致：凡是 dbfamily.EffectiveFamily 落到
// mysql / postgresql / oracle 三族的都算支持。之所以两份并存，是因为这里要
// 直接拼进 SQL 的 IN 列表，而 effective family 判定是 Go 侧逻辑。
var supportedDBTypes = []string{"MySQL", "PostgreSQL", "Kingbase", "Oracle"}

type instanceOptionRow struct {
	InstanceID       string `json:"instance_id"`
	DBType           string `json:"db_type"`
	Remark           string `json:"remark"`
	Hostname         string `json:"hostname"`
	Port             int    `json:"port"`
	EnvironmentName  string `json:"environment_name"`
	LastCheckStatus  string `json:"last_check_status"`
	Supported        bool   `json:"supported"`
	UnsupportedCause string `json:"unsupported_cause,omitempty"`

	// 慢日志能力（来自探测，供概览页提醒与采集页过滤共用）
	CapabilityState     string `json:"capability_state,omitempty"`
	CapabilityLabel     string `json:"capability_label,omitempty"`
	Collectable         bool   `json:"collectable"`
	SlowQueryLogEnabled bool   `json:"slow_query_log_enabled"`
	LogOutputMode       string `json:"log_output_mode,omitempty"`
	ResolvedLogPath     string `json:"resolved_log_path,omitempty"`
	LongQueryTimeMS     int    `json:"long_query_time_ms,omitempty"`
	CollectStatus       string `json:"collect_status,omitempty"`
	SlowLogCause        string `json:"slow_log_cause,omitempty"`
	SlowLogAdvice       string `json:"slow_log_advice,omitempty"`
	// DbVersion 实例画像采集到的版本串（如 8.0.43 / 19.3.0.0.0），可能为空。
	// 前端把它与 db_type 拼成「MySQL 8.0.43」展示，用户一眼能确认版本前提是否满足。
	DbVersion string `json:"db_version,omitempty"`
	// FailureCode 采集失败原因码，前端拿它反查前提条件清单里的对应项。
	FailureCode string `json:"failure_code,omitempty"`
	// Source 数据来源：FILE（文件增量）或 V$SQL（Oracle 采样）。
	Source string `json:"source,omitempty"`
	// CreatedAt 实例创建时间。仅供前端复现「实例管理列表」的默认排序
	// （created_at desc, id desc）来挑选默认选中实例，不参与任何权限或采集判定。
	CreatedAt time.Time `json:"created_at"`
	// ProbeError 探测/采集阶段带上下文的错误文本，仅供诊断接口使用。
	ProbeError string `json:"probe_error,omitempty"`
	// ExtraConfig 原始扩展配置。含 SSH 口令等敏感字段，禁止出现在任何对外响应里，
	// 只在服务端用于 OceanBase 租户模式等引擎族判定。
	ExtraConfig datatypes.JSON `json:"-"`
}

func supportedDBTypeSet() map[string]struct{} {
	out := make(map[string]struct{}, len(supportedDBTypes))
	for _, item := range supportedDBTypes {
		out[item] = struct{}{}
	}
	return out
}

// isSupportedDBType 判断 db_type 是否在慢 SQL 采集支持范围内。
//
// 只做字符串层面的快筛（用于 SQL 的 IN 列表）；涉及 OceanBase 租户模式的
// 精确判定走 slowLogEngineSupported，那里会解析 extra_config。
func isSupportedDBType(dbType string) bool {
	_, ok := supportedDBTypeSet()[strings.TrimSpace(dbType)]
	return ok
}

func buildUnsupportedCause(dbType string) string {
	if isSupportedDBType(dbType) {
		return ""
	}
	return fmt.Sprintf("当前慢 SQL 采集暂不支持 %s，仅支持 MySQL / PostgreSQL / Oracle 三类引擎",
		strings.TrimSpace(dbType))
}

// queryPerformanceInstances 返回性能分析模块的实例清单。
//
// 【2026-10-24 权限解耦】这里**刻意不做**任何数据访问授权过滤：
// 能不能看到性能分析，只由 RBAC 功能权限 performance.view / performance.manage 决定
// （校验发生在路由层 middleware.RequirePermission，菜单与路由过滤发生在前端 meta.permission）。
// 数据访问授权（insight_das_data_grants）管的是「能不能操作某个实例的数据」，
// 与「能不能查看性能分析」是两套独立体系，不要再把 HasDataGrant / 可见实例集合混进来——
// 那会让只有 SQL Server 授权的用户看到一片空白，属于误伤。
//
// 包括「重探慢日志能力」（ReprobeSlowLogCapabilityService）在内都没有例外：
// 它与提交工单同一口径，持有 order.submit 即可对任意实例提交工单，
// 性能分析持有 performance.view 即可对任意实例重探，都不叠加数据授权。
func queryPerformanceInstances(search string) ([]instanceOptionRow, error) {
	type row struct {
		InstanceID      string
		DBType          string
		Remark          string
		Hostname        string
		Port            int
		EnvironmentName string
		LastCheckStatus string
		CapabilityState string
		SlowLogEnabled  bool
		LogOutputMode   string
		ResolvedLogPath string
		LongQueryTimeMS int
		CollectStatus   string
		ProbeError      string
		FailureCode     string
		Source          string
		DbVersion       string
		CreatedAt       time.Time
		ExtraConfig     datatypes.JSON
	}

	var rows []row
	tx := global.App.DB.Table("insight_instances a").
		Select(`
			a.instance_id,
			a.db_type,
			a.remark,
			a.hostname,
			a.port,
			ifnull(env.name,'') as environment_name,
			a.last_check_status,
			ifnull(c.capability_state,'') as capability_state,
			ifnull(c.slow_query_log_enabled, 0) as slow_log_enabled,
			ifnull(c.log_output_mode,'') as log_output_mode,
			ifnull(c.resolved_log_path,'') as resolved_log_path,
			ifnull(c.long_query_time_ms, 0) as long_query_time_ms,
			ifnull(c.collect_status,'') as collect_status,
			ifnull(c.probe_error,'') as probe_error,
			ifnull(c.failure_code,'') as failure_code,
			ifnull(c.source,'') as source,
			ifnull(a.db_version,'') as db_version,
			a.created_at,
			a.extra_config
		`).
		Joins("left join insight_instance_environments env on env.id = a.environment").
		Joins("left join insight_performance_slowlog_collectors c on c.instance_id = a.instance_id").
		Order("a.db_type asc, a.remark asc, a.hostname asc, a.port asc")

	if keyword := strings.TrimSpace(search); keyword != "" {
		like := "%" + keyword + "%"
		tx = tx.Where(
			"a.instance_id like ? or a.remark like ? or a.hostname like ? or ifnull(env.name,'') like ?",
			like, like, like, like,
		)
	}

	if err := tx.Scan(&rows).Error; err != nil {
		return nil, err
	}

	result := make([]instanceOptionRow, 0, len(rows))
	for _, item := range rows {
		// 用引擎族口径判定「支持」，与采集链路 slowLogEngineSupported 同一套：
		// 这样 OceanBase 无论 MySQL 模式还是 Oracle 模式都会进来，
		// 不会出现「探测说可采集、列表里却看不见」的不一致。
		supported := slowLogEngineSupported(commonModels.EnumType(item.DBType), []byte(item.ExtraConfig))
		state := strings.TrimSpace(item.CapabilityState)
		option := instanceOptionRow{
			InstanceID:          item.InstanceID,
			DBType:              item.DBType,
			Remark:              item.Remark,
			Hostname:            item.Hostname,
			Port:                item.Port,
			EnvironmentName:     item.EnvironmentName,
			LastCheckStatus:     item.LastCheckStatus,
			Supported:           supported,
			UnsupportedCause:    buildUnsupportedCause(item.DBType),
			DbVersion:           item.DbVersion,
			FailureCode:         strings.TrimSpace(item.FailureCode),
			Source:              strings.TrimSpace(item.Source),
			CreatedAt:           item.CreatedAt,
			CapabilityState:     state,
			Collectable:         supported && state == capabilityEnabledFile,
			SlowQueryLogEnabled: item.SlowLogEnabled,
			LogOutputMode:       item.LogOutputMode,
			ResolvedLogPath:     item.ResolvedLogPath,
			LongQueryTimeMS:     item.LongQueryTimeMS,
			CollectStatus:       item.CollectStatus,
			ProbeError:          strings.TrimSpace(item.ProbeError),
			ExtraConfig:         item.ExtraConfig,
		}
		if supported {
			option.CapabilityLabel = capabilityStateLabel(state)
			if slowLogCapabilityNeedsAttention(state) {
				option.SlowLogCause = capabilityCause(state, item.ProbeError)
				option.SlowLogAdvice = capabilityAdvice(state)
			}
		}
		result = append(result, option)
	}
	return result, nil
}

// filterCollectableInstances 只保留可采集（ENABLED_FILE）的实例。
// 供慢 SQL 分析页与参数管理页使用——不可采集的实例出现在这些页面只会造成困惑。
func filterCollectableInstances(list []instanceOptionRow) []instanceOptionRow {
	out := make([]instanceOptionRow, 0, len(list))
	for _, item := range list {
		if item.Collectable {
			out = append(out, item)
		}
	}
	return out
}

// splitSlowLogAttention 把「需要提醒」的实例按状态拆开，供概览页分色展示与计数。
func splitSlowLogAttention(list []instanceOptionRow) (map[string]int, []instanceOptionRow) {
	counts := map[string]int{
		capabilityEnabledFile:  0,
		capabilityEnabledTable: 0,
		capabilityDisabled:     0,
		capabilityProbeFailed:  0,
		capabilityFileMissing:  0,
		// UNSUPPORTED 以前漏在这一层，导致「引擎/参数不支持」的实例既不在列表、
		// 也不在页面提示里，无声消失。补上后页面能给出"还有几台不具备条件"。
		capabilityUnsupported: 0,
	}
	attention := make([]instanceOptionRow, 0)
	for _, item := range list {
		if !item.Supported {
			continue
		}
		state := strings.TrimSpace(item.CapabilityState)
		if _, known := counts[state]; !known {
			state = capabilityProbeFailed
		}
		counts[state]++
		if slowLogCapabilityNeedsAttention(state) {
			attention = append(attention, item)
		}
	}
	return counts, attention
}
