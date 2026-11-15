package services

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"github.com/simajiong/jaysql/internal/common/dbfamily"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	commonServices "github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/internal/das/dao"
	performanceModels "github.com/simajiong/jaysql/internal/performance/models"
	"github.com/simajiong/jaysql/internal/global"
)

/* ================= 统一参数条目 ================= */

// 生效方式：列表展示与历史记录共用
const (
	paramEffectImmediate = "immediate" // 立即生效
	paramEffectNewConn   = "new_conn"  // 新连接生效
	paramEffectRestart   = "restart"   // 重启后生效
)

// ParamEntry 五类数据库参数的统一视图。
// DefaultValue/Description 等元数据来源：PG/Oracle/SS 系统视图自带；
// MySQL/TiDB 无元数据来源，由内置常用参数清单增强，清单外参数留空。
type ParamEntry struct {
	Name           string `json:"name"`
	CurrentValue   string `json:"current_value"`
	DefaultValue   string `json:"default_value"`
	DataType       string `json:"data_type"`
	Description    string `json:"description"`
	Effect         string `json:"effect"`
	NeedRestart    bool   `json:"need_restart"`
	Risk           string `json:"risk"`
	MinValue       string `json:"min_value"`
	MaxValue       string `json:"max_value"`
	Source         string `json:"source"`
	PendingRestart bool   `json:"pending_restart"`
}

/* ================= 内置参数清单（MySQL/TiDB 元数据增强 + 全库高危标记） ================= */

type builtinParamMeta struct {
	DataType    string
	Description string
	DefaultValue string
	Risk        string
	NeedRestart bool
}

// mysqlBuiltinParams：MySQL 常用参数元数据（needRestart 按 8.0 口径标注）
var mysqlBuiltinParams = map[string]builtinParamMeta{
	"innodb_buffer_pool_size":            {"容量", "InnoDB 缓冲池大小，缓存数据页与索引页", "134217728", "high", false},
	"max_connections":                    {"整数", "最大并发连接数", "151", "high", false},
	"max_allowed_packet":                 {"容量", "单包最大体积，过大有内存风险", "67108864", "mid", false},
	"innodb_log_file_size":               {"容量", "redo log 单文件大小（8.0.30 前为静态参数）", "48M", "high", true},
	"innodb_redo_log_capacity":           {"容量", "8.0.30+ redo 总容量，动态替代 log_file_size", "104857600", "mid", false},
	"innodb_flush_log_at_trx_commit":     {"整数", "事务日志刷盘策略，2 有丢 1 秒数据风险", "1", "high", false},
	"sync_binlog":                        {"整数", "binlog 刷盘策略，1 最安全", "1", "high", false},
	"binlog_format":                      {"枚举", "binlog 格式 ROW/STATEMENT/MIXED", "ROW", "mid", false},
	"long_query_time":                    {"时间(秒)", "慢查询判定阈值", "10", "low", false},
	"slow_query_log":                     {"布尔", "是否记录慢查询日志", "OFF", "low", false},
	"slow_query_log_file":                {"字符串", "慢查询日志文件路径", "", "low", false},
	"log_output":                         {"枚举", "慢日志输出 FILE/TABLE", "FILE", "low", false},
	"log_timestamps":                     {"枚举", "日志时间戳时区 SYSTEM/UTC", "UTC", "low", false},
	"wait_timeout":                       {"时间(秒)", "空闲连接超时时间", "28800", "low", false},
	"interactive_timeout":                {"时间(秒)", "交互式连接空闲超时", "28800", "low", false},
	"connect_timeout":                    {"时间(秒)", "建立连接超时", "10", "low", false},
	"net_read_timeout":                   {"时间(秒)", "网络读超时", "30", "low", false},
	"net_write_timeout":                  {"时间(秒)", "网络写超时", "60", "low", false},
	"character_set_server":               {"枚举", "服务端默认字符集", "utf8mb4", "mid", true},
	"collation_server":                   {"枚举", "服务端默认排序规则", "utf8mb4_0900_ai_ci", "mid", true},
	"time_zone":                          {"字符串", "会话默认时区", "SYSTEM", "low", false},
	"transaction_isolation":              {"枚举", "默认事务隔离级别", "REPEATABLE-READ", "mid", false},
	"sql_mode":                           {"枚举", "SQL 模式组合", "", "mid", false},
	"lower_case_table_names":             {"整数", "表名大小写策略，主从必须一致", "0", "high", true},
	"skip_name_resolve":                  {"布尔", "跳过 DNS 解析", "OFF", "low", true},
	"skip_networking":                    {"布尔", "关闭 TCP 监听，仅本地 socket", "OFF", "high", true},
	"innodb_page_size":                   {"容量", "InnoDB 页大小，建库后不可改", "16384", "high", true},
	"innodb_flush_method":                {"枚举", "刷盘方式 O_DIRECT 等", "", "mid", true},
	"innodb_io_capacity":                 {"整数", "InnoDB 后台 IO 能力", "200", "mid", false},
	"innodb_io_capacity_max":             {"整数", "InnoDB 后台 IO 上限", "2000", "mid", false},
	"innodb_lock_wait_timeout":           {"时间(秒)", "行锁等待超时", "50", "low", false},
	"innodb_rollback_on_timeout":         {"布尔", "锁超时回滚整个事务", "OFF", "mid", true},
	"table_open_cache":                   {"整数", "表缓存数量", "4000", "low", false},
	"thread_cache_size":                  {"整数", "线程缓存数量", "9", "low", false},
	"tmp_table_size":                     {"容量", "内存临时表上限", "16777216", "low", false},
	"max_heap_table_size":                {"容量", "MEMORY 表上限", "16777216", "low", false},
	"sort_buffer_size":                   {"容量", "排序缓冲区（会话级）", "262144", "low", false},
	"join_buffer_size":                   {"容量", "join 缓冲区（会话级）", "262144", "low", false},
	"read_rnd_buffer_size":               {"容量", "随机读缓冲区", "262144", "low", false},
	"binlog_expire_logs_seconds":         {"整数", "binlog 保留秒数", "2592000", "mid", false},
	"expire_logs_days":                   {"整数", "binlog 保留天数（旧参数）", "0", "mid", false},
	"log_bin_trust_function_creators":    {"布尔", "允许创建不安全函数", "OFF", "mid", false},
	"performance_schema":                 {"布尔", "性能_schema 开关", "ON", "high", true},
	"default_password_lifetime":          {"整数", "密码有效期天数", "0", "low", false},
	"max_connect_errors":                 {"整数", "主机连接错误阈值", "100", "low", false},
	"proxy_user":                         {"字符串", "代理用户", "", "low", false},
	"read_only":                          {"布尔", "实例只读（超管除外）", "OFF", "high", false},
	"super_read_only":                    {"布尔", "超级只读", "OFF", "high", false},
}

// tidbBuiltinParams：TiDB 常用参数（TiDB 参数绝大多数在线生效，静态项极少）
var tidbBuiltinParams = map[string]builtinParamMeta{
	"tidb_mem_quota_query":        {"容量", "单查询内存上限，超限触发 action", "1073741824", "high", false},
	"max_connections":             {"整数", "TiDB 最大连接数（0 无限制）", "0", "high", false},
	"txn-total-size-limit":        {"容量", "单事务大小上限", "104857600", "mid", false},
	"txn-entry-size-limit":        {"容量", "单行大小上限", "6291456", "mid", false},
	"tidb_gc_run_interval":        {"时间", "GC 触发间隔", "10m0s", "low", false},
	"tidb_gc_life_time":           {"时间", "数据保留时长", "10m0s", "mid", false},
	"tidb_distsql_scan_concurrency": {"整数", "distsql 扫描并发度", "15", "mid", false},
	"tidb_enable_point_plan_cache": {"布尔", "点查 plan cache 开关", "ON", "low", false},
	"new_collations_enabled_on_first_bootstrap": {"布尔", "仅首次 bootstrap 生效，集群初始化后不可改", "ON", "high", true},
	"tidb_slow_log_threshold":     {"整数", "慢日志阈值（毫秒）", "300", "low", false},
	"tidb_query_log_max_len":      {"整数", "慢日志 SQL 最大长度", "4096", "low", false},
	"tidb_batch_insert":           {"布尔", "批量 insert 改写", "OFF", "mid", false},
}

// pgRiskNames / midRiskNames：高危与中风险参数（全库通用按名匹配，PG/SS/Oracle 系统视图无风险字段）
var paramHighRiskNames = map[string]bool{
	// 通用
	"max_connections": true, "shared_buffers": true, "work_mem": true,
	"wal_level": true, "fsync": true, "synchronous_commit": true, "full_page_writes": true,
	"max_wal_senders": true, "maintenance_work_mem": true, "autovacuum": true,
	"sga_target": true, "pga_aggregate_target": true, "processes": true, "db_block_size": true,
	"max server memory (MB)": true, "max worker threads": true,
}
var paramMidRiskNames = map[string]bool{
	"checkpoint_timeout": true, "max_wal_size": true, "min_wal_size": true,
	"autovacuum_max_workers": true, "autovacuum_vacuum_cost_limit": true,
	"max degree of parallelism": true, "fill factor (%)": true, "cost threshold for parallelism": true,
	"max_allowed_packet": true, "binlog_format": true, "sql_mode": true, "character_set_server": true,
	"transaction_isolation": true, "innodb_redo_log_capacity": true, "open_cursors": true,
	"undo_retention": true, "audit_trail": true, "txn-total-size-limit": true, "txn-entry-size-limit": true,
	"tidb_gc_life_time": true, "tidb_distsql_scan_concurrency": true, "tidb_batch_insert": true,
	"innodb_flush_method": true, "innodb_io_capacity": true, "innodb_io_capacity_max": true,
	"innodb_lock_wait_timeout": true, "binlog_expire_logs_seconds": true, "expire_logs_days": true,
}

/* ================= 服务：实时参数列表 ================= */

type GetInstanceParamsService struct {
	C          *gin.Context
	InstanceID string
}

type instanceBrief struct {
	InstanceID string `json:"instance_id"`
	Hostname   string `json:"hostname"`
	Port       int    `json:"port"`
	DbType     string `json:"db_type"`
	Remark     string `json:"remark"`
}

type instanceParamsResult struct {
	Instance instanceBrief `json:"instance"`
	Params   []ParamEntry  `json:"params"`
}

func (s *GetInstanceParamsService) Run() (*instanceParamsResult, error) {
	instance, err := loadParamTargetInstance(s.InstanceID)
	if err != nil {
		return nil, err
	}
	params, err := collectInstanceParams(instance, paramQueryTimeout)
	if err != nil {
		return nil, err
	}
	sort.Slice(params, func(i, j int) bool { return params[i].Name < params[j].Name })
	return &instanceParamsResult{
		Instance: instanceBriefOf(instance),
		Params:   params,
	}, nil
}

/* ================= 服务：多实例对比 ================= */

type DiffInstanceParamsService struct {
	C           *gin.Context
	InstanceIDs []string
}

type diffRow struct {
	ParamEntry
	Values map[string]string `json:"values"`
	Diff   bool              `json:"diff"`
}

type diffResult struct {
	Instances []instanceBrief `json:"instances"`
	Rows      []diffRow       `json:"rows"`
}

func (s *DiffInstanceParamsService) Run() (*diffResult, error) {
	if len(s.InstanceIDs) < 2 || len(s.InstanceIDs) > 5 {
		return nil, fmt.Errorf("请选择 2~5 个实例进行对比")
	}
	instances := make([]*commonModels.InsightInstances, 0, len(s.InstanceIDs))
	family := ""
	for _, id := range s.InstanceIDs {
		inst, err := loadParamTargetInstance(id)
		if err != nil {
			return nil, err
		}
		f := dbfamily.EffectiveFamily(string(inst.DbType), inst.ExtraConfig)
		if family == "" {
			family = f
		} else if family != f {
			return nil, fmt.Errorf("仅支持同数据库家族的实例对比（%s 与 %s 家族不同）", id, s.InstanceIDs[0])
		}
		instances = append(instances, inst)
	}

	type collected struct {
		brief  instanceBrief
		params []ParamEntry
	}
	results := make([]collected, 0, len(instances))
	for _, inst := range instances {
		params, err := collectInstanceParams(inst, paramQueryTimeout)
		if err != nil {
			return nil, fmt.Errorf("实例 %s 参数查询失败: %w", inst.InstanceID, err)
		}
		results = append(results, collected{brief: instanceBriefOf(inst), params: params})
	}

	// 以参数名为键聚合：元数据取第一个能提供该参数的实例
	index := make(map[string]*diffRow)
	order := make([]string, 0)
	for _, c := range results {
		for _, p := range c.params {
			row, ok := index[p.Name]
			if !ok {
				row = &diffRow{ParamEntry: p, Values: map[string]string{}}
				index[p.Name] = row
				order = append(order, p.Name)
			}
			row.Values[c.brief.InstanceID] = p.CurrentValue
		}
	}
	rows := make([]diffRow, 0, len(order))
	for _, name := range order {
		row := index[name]
		seen := map[string]bool{}
		for _, v := range row.Values {
			seen[v] = true
		}
		row.Diff = len(seen) > 1
		rows = append(rows, *row)
	}
	briefs := make([]instanceBrief, 0, len(results))
	for _, c := range results {
		briefs = append(briefs, c.brief)
	}
	return &diffResult{Instances: briefs, Rows: rows}, nil
}

/* ================= 服务：修改历史 ================= */

type GetParamChangesService struct {
	C         *gin.Context
	InstanceID string
	ParamName string
	Operator  string
}

func (s *GetParamChangesService) Run() ([]performanceModels.InsightPerformanceParamChanges, error) {
	var list []performanceModels.InsightPerformanceParamChanges
	tx := global.App.DB.Table("insight_performance_param_changes").
		Order("id desc").Limit(200)
	if s.InstanceID != "" {
		tx = tx.Where("instance_id=?", s.InstanceID)
	}
	if s.ParamName != "" {
		tx = tx.Where("param_name=?", s.ParamName)
	}
	if s.Operator != "" {
		tx = tx.Where("operator=?", s.Operator)
	}
	if err := tx.Scan(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

/* ================= 服务：修改参数 ================= */

type ModifyInstanceParamService struct {
	C          *gin.Context
	InstanceID string
	ParamName  string
	NewValue   string
	Reason     string
	AckRisk    bool
	Operator   string
	RollbackOf uint64
}

var paramNameRegexp = regexp.MustCompile(`^[A-Za-z0-9_.@() %\-]+$`)

func (s *ModifyInstanceParamService) Run() (map[string]any, error) {
	instance, err := loadParamTargetInstance(s.InstanceID)
	if err != nil {
		return nil, err
	}
	params, err := collectInstanceParams(instance, paramQueryTimeout)
	if err != nil {
		return nil, err
	}
	var entry *ParamEntry
	for i := range params {
		if params[i].Name == s.ParamName {
			entry = &params[i]
			break
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("参数`%s`在该实例上不存在（以实时查询结果为准，防止误输入）", s.ParamName)
	}
	// 需重启生效的参数不允许在线修改（前端修改/历史按钮同步置灰）；
	// 回滚也走本入口，同样被拦——重启类参数的恢复本身也要重启，留待维护窗口处理
	if entry.Effect == paramEffectRestart {
		return nil, fmt.Errorf("参数`%s`需重启才能生效，不允许在线修改，请在维护窗口变更", s.ParamName)
	}
	if strings.TrimSpace(s.NewValue) == "" {
		return nil, fmt.Errorf("新值不能为空（如需恢复默认请填写默认值 %s）", entry.DefaultValue)
	}
	if s.NewValue == entry.CurrentValue {
		return nil, fmt.Errorf("新值与当前值相同，无需修改")
	}
	if strings.Contains(s.NewValue, ";") || strings.Contains(s.NewValue, "--") ||
		strings.Contains(s.NewValue, "/*") || strings.Contains(s.NewValue, "\n") || strings.Contains(s.NewValue, "\r") {
		return nil, fmt.Errorf("新值包含非法字符")
	}
	if entry.Risk == "high" && !s.AckRisk {
		return nil, fmt.Errorf("高危参数修改需勾选风险确认")
	}

	oldValue := entry.CurrentValue
	effective, execMsg, execErr := modifyParamOnInstance(instance, entry, s.NewValue)
	execResult := "success"
	message := execMsg
	if execErr != nil {
		execResult = "failed"
		message = execErr.Error()
	}

	change := performanceModels.InsightPerformanceParamChanges{
		InstanceID:  instance.InstanceID,
		DBType:      instance.DbType,
		ParamName:   s.ParamName,
		OldValue:    oldValue,
		NewValue:    s.NewValue,
		Scope:       "GLOBAL",
		Effective:   effective,
		Operator:    s.Operator,
		Reason:      s.Reason,
		ExecResult:  execResult,
		ExecMessage: message,
		RollbackOf:  s.RollbackOf,
	}
	if err := global.App.DB.Create(&change).Error; err != nil {
		global.App.Log.Error("参数修改历史落库失败: " + err.Error())
	}

	commonServices.TryWriteAuditLog(s.C, commonServices.AuditLogEvent{
		Username:     s.Operator,
		ModuleCode:   "performance",
		ActionCode:   "param.modify",
		ResourceType: "instance_param",
		ResourceID:   instance.InstanceID,
		Summary: fmt.Sprintf("修改参数 %s（实例 %s）：%s → %s，结果 %s",
			s.ParamName, instance.InstanceID, oldValue, s.NewValue, execResult),
		BeforeData: oldValue,
		AfterData:  s.NewValue,
	})

	if execErr != nil {
		return nil, fmt.Errorf("参数修改失败：%s（已记录失败历史）", execErr.Error())
	}

	// 修改后复核：重新读取实际值，与目标值不一致时提示（不判定失败）
	if actual, verr := verifyParamValue(instance, s.ParamName); verr == nil && actual != "" && actual != s.NewValue {
		note := fmt.Sprintf("复核读取值为 %s，与目标值 %s 不一致，可能需重连或重启后生效", actual, s.NewValue)
		message = strings.TrimSpace(message + "；" + note)
		global.App.DB.Model(&change).Update("exec_message", message)
	}

	return map[string]any{
		"change_id":     change.ID,
		"effective":     effective,
		"exec_result":   execResult,
		"exec_message":  message,
		"old_value":     oldValue,
		"verify_hint":   postModifyHint(instance, effective),
	}, nil
}

/* ================= 服务：回滚 ================= */

type RollbackParamService struct {
	C        *gin.Context
	ChangeID uint64
	Operator string
}

func (s *RollbackParamService) Run() (map[string]any, error) {
	var change performanceModels.InsightPerformanceParamChanges
	if err := global.App.DB.Where("id=?", s.ChangeID).Take(&change).Error; err != nil {
		return nil, fmt.Errorf("历史记录不存在")
	}
	if change.ExecResult != "success" {
		return nil, fmt.Errorf("仅成功执行的变更可回滚")
	}
	svc := &ModifyInstanceParamService{
		C:          s.C,
		InstanceID: change.InstanceID,
		ParamName:  change.ParamName,
		NewValue:   change.OldValue,
		Reason:     fmt.Sprintf("回滚变更 #%d（%s → %s）", change.ID, change.OldValue, change.NewValue),
		AckRisk:    true, // 回滚是恢复历史已知值，高危确认随原变更
		Operator:   s.Operator,
		RollbackOf: change.ID,
	}
	return svc.Run()
}

/* ================= 实例加载与连接 ================= */

const paramQueryTimeout = 8 * time.Second

func loadParamTargetInstance(instanceID string) (*commonModels.InsightInstances, error) {
	var instance commonModels.InsightInstances
	if err := global.App.DB.Where("instance_id=?", strings.TrimSpace(instanceID)).Take(&instance).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("实例不存在")
		}
		return nil, err
	}
	return &instance, nil
}

func instanceBriefOf(instance *commonModels.InsightInstances) instanceBrief {
	return instanceBrief{
		InstanceID: instance.InstanceID,
		Hostname:   instance.Hostname,
		Port:       instance.Port,
		DbType:     string(instance.DbType),
		Remark:     instance.Remark,
	}
}

func paramQueryContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}

// collectInstanceParams 按 db family 分派实时参数采集
func collectInstanceParams(instance *commonModels.InsightInstances, timeout time.Duration) ([]ParamEntry, error) {
	family := dbfamily.EffectiveFamily(string(instance.DbType), instance.ExtraConfig)
	var params []ParamEntry
	var err error
	switch family {
	case dbfamily.FamilyMySQL:
		if strings.EqualFold(string(instance.DbType), "TiDB") {
			params, err = collectMySQLParams(instance, timeout, tidbBuiltinParams)
		} else {
			params, err = collectMySQLParams(instance, timeout, mysqlBuiltinParams)
		}
	case dbfamily.FamilyPostgres:
		params, err = collectPostgresParams(instance, timeout)
	case dbfamily.FamilyOracle:
		params, err = collectOracleParams(instance, timeout)
	case dbfamily.FamilySQLServer:
		params, err = collectSQLServerParams(instance, timeout)
	default:
		return nil, fmt.Errorf("参数管理暂不支持该数据库类型：%s", instance.DbType)
	}
	if err != nil {
		return nil, err
	}
	// 统一风险口径（所有数据库家族）：需重启才能生效的参数一律按高风险处理，
	// 修改/回滚入口同口径拒绝（见 ModifyInstanceParamService.Run）
	for i := range params {
		if params[i].Effect == paramEffectRestart {
			params[i].Risk = "high"
		}
	}
	return params, nil
}

func collectMySQLParams(instance *commonModels.InsightInstances, timeout time.Duration, meta map[string]builtinParamMeta) ([]ParamEntry, error) {
	ctx, cancel := paramQueryContext(timeout)
	defer cancel()
	db := dao.DB{
		User:     instance.User,
		Password: instancePlainPassword(instance),
		Host:     strings.TrimSpace(instance.Hostname),
		Port:     instance.Port,
		Database: "",
		Params:   map[string]string{"charset": "utf8mb4", "timeout": "5s", "readTimeout": "8s", "writeTimeout": "8s"},
		Ctx:      ctx,
	}
	_, rows, err := db.Query("SHOW VARIABLES")
	if err != nil {
		return nil, fmt.Errorf("连接实例查询参数失败: %w", err)
	}
	params := make([]ParamEntry, 0, len(*rows))
	for _, row := range *rows {
		name := pgText(row, "Variable_name")
		if name == "" {
			continue
		}
		p := ParamEntry{
			Name:    name,
			CurrentValue: pgText(row, "Value"),
			Risk:    "low",
			Effect:  paramEffectImmediate,
		}
		if m, ok := meta[name]; ok {
			p.DataType = m.DataType
			p.Description = m.Description
			p.DefaultValue = m.DefaultValue
			p.NeedRestart = m.NeedRestart
			if m.NeedRestart {
				p.Effect = paramEffectRestart
			}
		}
		p.Risk = classifyRisk(name)
		params = append(params, p)
	}
	return params, nil
}

// pg_settings 没有 default_val 列（SQLSTATE 42703）——PG 的编译内置默认值列是 boot_val；
// 保持别名 default_val，下游 pgText(row, "default_val") 取数不变
var paramPgSettingsQuery = `SELECT name, setting, COALESCE(unit,'') AS unit, COALESCE(boot_val,'') AS default_val,
COALESCE(vartype,'') AS vartype, COALESCE(context,'') AS context, COALESCE(min_val,'') AS min_val,
COALESCE(max_val,'') AS max_val, COALESCE(short_desc,'') AS short_desc, COALESCE(source,'') AS source,
COALESCE(pending_restart,false) AS pending_restart
FROM pg_settings ORDER BY name`

func collectPostgresParams(instance *commonModels.InsightInstances, timeout time.Duration) ([]ParamEntry, error) {
	ctx, cancel := paramQueryContext(timeout)
	defer cancel()
	db := dao.PostgresDB{
		User:     instance.User,
		Password: instancePlainPassword(instance),
		Host:     strings.TrimSpace(instance.Hostname),
		Port:     instance.Port,
		Database: pgConnectDatabase(instance),
		Ctx:      ctx,
	}
	_, rows, err := db.Query(paramPgSettingsQuery)
	if err != nil {
		return nil, fmt.Errorf("连接实例查询 pg_settings 失败: %w", err)
	}
	params := make([]ParamEntry, 0, len(*rows))
	for _, row := range *rows {
		name := pgText(row, "name")
		if name == "" {
			continue
		}
		setting := pgText(row, "setting")
		unit := pgText(row, "unit")
		current := setting + unit
		defVal := pgText(row, "default_val")
		if defVal != "" {
			defVal = defVal + unit
		}
		contextStr := pgText(row, "context")
		p := ParamEntry{
			Name:           name,
			CurrentValue:   current,
			DefaultValue:   defVal,
			DataType:       pgText(row, "vartype"),
			Description:    pgText(row, "short_desc"),
			MinValue:       pgText(row, "min_val"),
			MaxValue:       pgText(row, "max_val"),
			Source:         pgText(row, "source"),
			PendingRestart: pgText(row, "pending_restart") == "true",
			Risk:           classifyRisk(name),
		}
		switch contextStr {
		case "postmaster":
			p.Effect = paramEffectRestart
			p.NeedRestart = true
		case "sighup":
			p.Effect = paramEffectImmediate
		default: // superuser / user / backend
			p.Effect = paramEffectNewConn
		}
		if p.PendingRestart {
			p.Effect = paramEffectRestart
			p.NeedRestart = true
		}
		params = append(params, p)
	}
	return params, nil
}

func collectOracleParams(instance *commonModels.InsightInstances, timeout time.Duration) ([]ParamEntry, error) {
	ctx, cancel := paramQueryContext(timeout)
	defer cancel()
	db := dao.OracleDB{
		User:        instance.User,
		Password:    instancePlainPassword(instance),
		Host:        strings.TrimSpace(instance.Hostname),
		Port:        instance.Port,
		ServiceName: serviceNameFromExtra(instance.ExtraConfig),
		Ctx:         ctx,
	}
	if db.ServiceName == "" {
		return nil, fmt.Errorf("实例缺少 Oracle 服务名（extra_config.service_name）")
	}
	query := `SELECT name, COALESCE(value,'') AS value, COALESCE(default_value,'') AS default_value,
COALESCE(type,0) AS ptype, COALESCE(issys_modifiable,'') AS issys_modifiable,
COALESCE(description,'') AS description, COALESCE(isdefault,'FALSE') AS isdefault
FROM v$parameter ORDER BY name`
	_, rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("连接实例查询 v$parameter 失败: %w", err)
	}
	oracleTypeNames := map[string]string{"1": "布尔", "2": "字符串", "3": "整数", "4": "参数", "5": "保留", "6": "大整数"}
	params := make([]ParamEntry, 0, len(*rows))
	for _, row := range *rows {
		name := pgText(row, "name")
		if name == "" {
			continue
		}
		modifiable := strings.ToUpper(pgText(row, "issys_modifiable"))
		isDefault := strings.ToUpper(pgText(row, "isdefault")) == "TRUE"
		p := ParamEntry{
			Name:         name,
			CurrentValue: pgText(row, "value"),
			DataType:     oracleTypeNames[pgText(row, "ptype")],
			Description:  pgText(row, "description"),
			Risk:         classifyRisk(name),
		}
		if isDefault {
			p.DefaultValue = p.CurrentValue
		}
		switch modifiable {
		case "FALSE":
			p.Effect = paramEffectRestart
			p.NeedRestart = true
		case "DEFERRED":
			p.Effect = paramEffectNewConn
		default: // IMMEDIATE 等
			p.Effect = paramEffectImmediate
		}
		params = append(params, p)
	}
	return params, nil
}

func collectSQLServerParams(instance *commonModels.InsightInstances, timeout time.Duration) ([]ParamEntry, error) {
	ctx, cancel := paramQueryContext(timeout)
	defer cancel()
	db := dao.SQLServerDB{
		User:     instance.User,
		Password: instancePlainPassword(instance),
		Host:     strings.TrimSpace(instance.Hostname),
		Port:     instance.Port,
		Database: "master",
		Ctx:      ctx,
	}
	query := `SELECT name, CONVERT(varchar(64), value) AS configured_value,
CONVERT(varchar(64), value_in_use) AS value_in_use,
CONVERT(varchar(64), minimum) AS minimum, CONVERT(varchar(64), maximum) AS maximum,
CONVERT(int, is_dynamic) AS is_dynamic
FROM sys.configurations ORDER BY name`
	_, rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("连接实例查询 sys.configurations 失败: %w", err)
	}
	params := make([]ParamEntry, 0, len(*rows))
	for _, row := range *rows {
		name := pgText(row, "name")
		if name == "" {
			continue
		}
		configured := pgText(row, "configured_value")
		inUse := pgText(row, "value_in_use")
		isDynamic := parseBoolRaw(pgText(row, "is_dynamic"))
		p := ParamEntry{
			Name:         name,
			CurrentValue: inUse,
			DefaultValue: "",
			DataType:     "整数",
			MinValue:     pgText(row, "minimum"),
			MaxValue:     pgText(row, "maximum"),
			Source:       "sp_configure",
			Risk:         classifyRisk(name),
		}
		// configured != in_use → 已改配置但待重启
		if configured != inUse {
			p.PendingRestart = true
			p.Effect = paramEffectRestart
			p.NeedRestart = true
		} else if isDynamic {
			p.Effect = paramEffectImmediate
		} else {
			p.Effect = paramEffectRestart
			p.NeedRestart = true
		}
		params = append(params, p)
	}
	return params, nil
}

func parseBoolRaw(raw string) bool {
	raw = strings.TrimSpace(raw)
	return raw == "1" || raw == "true" || raw == "TRUE" || raw == "\x01"
}

func classifyRisk(name string) string {
	if paramHighRiskNames[name] {
		return "high"
	}
	if paramMidRiskNames[name] {
		return "mid"
	}
	return "low"
}

/* ================= 修改与复核 ================= */

// modifyParamOnInstance 按 family 分派修改。静态参数的处理策略：
//   - PG postmaster：允许 ALTER SYSTEM SET（写入配置，重启后生效）
//   - Oracle FALSE：允许 SCOPE=SPFILE（写入 spfile，重启后生效）
//   - MySQL 内置清单 static（如 innodb_log_file_size）：拒绝在线修改，返回建议语句
//   - SQL Server is_dynamic=0：拒绝在线修改
func modifyParamOnInstance(instance *commonModels.InsightInstances, entry *ParamEntry, newValue string) (effective string, msg string, err error) {
	family := dbfamily.EffectiveFamily(string(instance.DbType), instance.ExtraConfig)
	switch family {
	case dbfamily.FamilyMySQL:
		return modifyMySQLParam(instance, entry, newValue)
	case dbfamily.FamilyPostgres:
		return modifyPostgresParam(instance, entry, newValue)
	case dbfamily.FamilyOracle:
		return modifyOracleParam(instance, entry, newValue)
	case dbfamily.FamilySQLServer:
		return modifySQLServerParam(instance, entry, newValue)
	default:
		return "", "", fmt.Errorf("暂不支持该数据库类型的参数修改")
	}
}

func modifyMySQLParam(instance *commonModels.InsightInstances, entry *ParamEntry, newValue string) (string, string, error) {
	ctx, cancel := paramQueryContext(paramQueryTimeout)
	defer cancel()
	db := dao.DB{
		User: instance.User, Password: instancePlainPassword(instance),
		Host: strings.TrimSpace(instance.Hostname), Port: instance.Port,
		Params: map[string]string{"charset": "utf8mb4", "timeout": "5s", "readTimeout": "8s", "writeTimeout": "8s"},
		Ctx:    ctx,
	}
	isTiDB := strings.EqualFold(string(instance.DbType), "TiDB")

	// 静态参数：拒绝在线修改（TiDB 静态项同样处理）
	if entry.NeedRestart && entry.Effect == paramEffectRestart && mysqlBuiltinStaticReject(entry.Name) && !isTiDB {
		return paramEffectRestart, "", fmt.Errorf(
			"参数 %s 为静态参数，不支持在线修改；请修改 my.cnf 后安排重启（示例：在 [mysqld] 段追加 %s = %s）",
			entry.Name, entry.Name, newValue)
	}

	valueSQL := formatMySQLParamValue(newValue)
	stmt := "SET GLOBAL " + entry.Name + " = " + valueSQL
	persist := ""
	if !isTiDB {
		// MySQL 8.0+ 优先 SET PERSIST：内存立即生效 + mysqld-auto.cnf 持久化一步到位
		major, mvErr := mysqlMajorVersion(db)
		if mvErr != nil {
			return paramEffectImmediate, "", fmt.Errorf("读取数据库版本失败: %w", mvErr)
		}
		if major >= 8 {
			stmt = "SET PERSIST " + entry.Name + " = " + valueSQL
			persist = "（SET PERSIST，已持久化到 mysqld-auto.cnf）"
		} else {
			persist = "（SET GLOBAL，MySQL 5.7 重启后会丢失，请手工同步到 my.cnf）"
		}
	} else {
		persist = "（SET GLOBAL，持久化到 PD）"
	}
	if err := db.Execute(stmt); err != nil {
		return paramEffectImmediate, "", err
	}
	effect := paramEffectImmediate
	if entry.Effect == paramEffectNewConn {
		effect = paramEffectNewConn
	}
	return effect, "修改命令已执行" + persist, nil
}

func mysqlBuiltinStaticReject(name string) bool {
	// MySQL 内置清单中标注 needRestart 的静态参数（8.0 口径）
	for _, n := range []string{"innodb_log_file_size", "innodb_page_size", "lower_case_table_names",
		"skip_name_resolve", "skip_networking", "innodb_flush_method", "innodb_rollback_on_timeout",
		"character_set_server", "collation_server", "performance_schema"} {
		if n == name {
			return true
		}
	}
	return false
}

func formatMySQLParamValue(v string) string {
	if regexp.MustCompile(`^-?[0-9]+$`).MatchString(v) {
		return v
	}
	escaped := strings.ReplaceAll(v, "'", "''")
	return "'" + escaped + "'"
}

func mysqlMajorVersion(db dao.DB) (int, error) {
	_, rows, err := db.Query("SELECT VERSION() AS v")
	if err != nil {
		return 0, err
	}
	if len(*rows) == 0 {
		return 0, fmt.Errorf("版本查询无结果")
	}
	version := pgText((*rows)[0], "v")
	majorStr := strings.Split(version, ".")[0]
	major, err := strconv.Atoi(strings.TrimSpace(majorStr))
	if err != nil {
		return 0, fmt.Errorf("无法解析版本号 %s", version)
	}
	return major, nil
}

func modifyPostgresParam(instance *commonModels.InsightInstances, entry *ParamEntry, newValue string) (string, string, error) {
	ctx, cancel := paramQueryContext(paramQueryTimeout)
	defer cancel()
	db := dao.PostgresDB{
		User: instance.User, Password: instancePlainPassword(instance),
		Host: strings.TrimSpace(instance.Hostname), Port: instance.Port,
		Database: pgConnectDatabase(instance), Ctx: ctx,
	}
	escaped := strings.ReplaceAll(newValue, "'", "''")
	if err := db.Execute(fmt.Sprintf("ALTER SYSTEM SET %s = '%s'", entry.Name, escaped)); err != nil {
		return entry.Effect, "", err
	}
	if entry.Effect == paramEffectRestart {
		return paramEffectRestart,
			"已写入 postgresql.auto.conf，需重启实例后生效（pending_restart 将置为 true）", nil
	}
	if err := db.Execute("SELECT pg_reload_conf()"); err != nil {
		return entry.Effect, "", fmt.Errorf("配置已写入，但触发 reload 失败: %w", err)
	}
	if entry.Effect == paramEffectNewConn {
		return paramEffectNewConn, "已写入配置并 reload，对新连接生效", nil
	}
	return paramEffectImmediate, "已写入配置并 reload，立即生效", nil
}

func modifyOracleParam(instance *commonModels.InsightInstances, entry *ParamEntry, newValue string) (string, string, error) {
	ctx, cancel := paramQueryContext(paramQueryTimeout)
	defer cancel()
	db := dao.OracleDB{
		User: instance.User, Password: instancePlainPassword(instance),
		Host: strings.TrimSpace(instance.Hostname), Port: instance.Port,
		ServiceName: serviceNameFromExtra(instance.ExtraConfig), Ctx: ctx,
	}
	if db.ServiceName == "" {
		return entry.Effect, "", fmt.Errorf("实例缺少 Oracle 服务名（extra_config.service_name）")
	}
	scope := "SCOPE=BOTH"
	effective := paramEffectImmediate
	hint := "SCOPE=BOTH：内存立即生效并写入 spfile"
	if entry.Effect == paramEffectRestart {
		scope = "SCOPE=SPFILE"
		effective = paramEffectRestart
		hint = "静态参数：已写入 spfile，需重启实例后生效"
	} else if entry.Effect == paramEffectNewConn {
		effective = paramEffectNewConn
		hint = "DEFERRED 参数：对新会话生效"
	}
	if _, numErr := strconv.ParseFloat(newValue, 64); numErr == nil {
		if err := db.Execute(fmt.Sprintf("ALTER SYSTEM SET %s = %s %s", entry.Name, newValue, scope)); err != nil {
			return effective, "", err
		}
	} else {
		escaped := strings.ReplaceAll(newValue, "'", "''")
		if err := db.Execute(fmt.Sprintf("ALTER SYSTEM SET %s = '%s' %s", entry.Name, escaped, scope)); err != nil {
			return effective, "", err
		}
	}
	return effective, hint, nil
}

func modifySQLServerParam(instance *commonModels.InsightInstances, entry *ParamEntry, newValue string) (string, string, error) {
	if entry.Effect == paramEffectRestart && !entry.PendingRestart {
		return paramEffectRestart, "", fmt.Errorf(
			"参数 %s 为静态参数（is_dynamic=0），不支持在线修改，请安排重启窗口手工调整", entry.Name)
	}
	ctx, cancel := paramQueryContext(paramQueryTimeout)
	defer cancel()
	db := dao.SQLServerDB{
		User: instance.User, Password: instancePlainPassword(instance),
		Host: strings.TrimSpace(instance.Hostname), Port: instance.Port,
		Database: "master", Ctx: ctx,
	}
	nameEscaped := strings.ReplaceAll(entry.Name, "'", "''")
	if _, numErr := strconv.ParseFloat(newValue, 64); numErr != nil {
		return paramEffectImmediate, "", fmt.Errorf("SQL Server 参数值必须为数字")
	}
	if err := db.Execute(fmt.Sprintf("EXEC sp_configure N'%s', %s; RECONFIGURE;", nameEscaped, newValue)); err != nil {
		return paramEffectImmediate, "", err
	}
	return paramEffectImmediate, "sp_configure + RECONFIGURE 已执行", nil
}

// verifyParamValue 修改后复核：单参读取实际值（尽力而为，失败不阻塞）
func verifyParamValue(instance *commonModels.InsightInstances, paramName string) (string, error) {
	ctx, cancel := paramQueryContext(paramQueryTimeout)
	defer cancel()
	family := dbfamily.EffectiveFamily(string(instance.DbType), instance.ExtraConfig)
	switch family {
	case dbfamily.FamilyMySQL:
		db := dao.DB{User: instance.User, Password: instancePlainPassword(instance),
			Host: strings.TrimSpace(instance.Hostname), Port: instance.Port,
			Params: map[string]string{"charset": "utf8mb4", "timeout": "5s", "readTimeout": "8s", "writeTimeout": "8s"}, Ctx: ctx}
		_, rows, err := db.Query("SHOW GLOBAL VARIABLES LIKE '" + strings.ReplaceAll(paramName, "'", "''") + "'")
		if err != nil || len(*rows) == 0 {
			return "", err
		}
		return pgText((*rows)[0], "Value"), nil
	case dbfamily.FamilyPostgres:
		db := dao.PostgresDB{User: instance.User, Password: instancePlainPassword(instance),
			Host: strings.TrimSpace(instance.Hostname), Port: instance.Port,
			Database: pgConnectDatabase(instance), Ctx: ctx}
		_, rows, err := db.Query("SELECT COALESCE(setting,'') AS setting, COALESCE(unit,'') AS unit FROM pg_settings WHERE name='" +
			strings.ReplaceAll(paramName, "'", "''") + "'")
		if err != nil || len(*rows) == 0 {
			return "", err
		}
		return pgText((*rows)[0], "setting") + pgText((*rows)[0], "unit"), nil
	case dbfamily.FamilyOracle:
		db := dao.OracleDB{User: instance.User, Password: instancePlainPassword(instance),
			Host: strings.TrimSpace(instance.Hostname), Port: instance.Port,
			ServiceName: serviceNameFromExtra(instance.ExtraConfig), Ctx: ctx}
		_, rows, err := db.Query("SELECT COALESCE(value,'') AS value FROM v$parameter WHERE name='" +
			strings.ReplaceAll(paramName, "'", "''") + "'")
		if err != nil || len(*rows) == 0 {
			return "", err
		}
		return pgText((*rows)[0], "value"), nil
	case dbfamily.FamilySQLServer:
		db := dao.SQLServerDB{User: instance.User, Password: instancePlainPassword(instance),
			Host: strings.TrimSpace(instance.Hostname), Port: instance.Port, Database: "master", Ctx: ctx}
		_, rows, err := db.Query("SELECT CONVERT(varchar(64), value_in_use) AS value_in_use FROM sys.configurations WHERE name='" +
			strings.ReplaceAll(paramName, "'", "''") + "'")
		if err != nil || len(*rows) == 0 {
			return "", err
		}
		return pgText((*rows)[0], "value_in_use"), nil
	}
	return "", fmt.Errorf("不支持的数据类型")
}

func postModifyHint(instance *commonModels.InsightInstances, effective string) string {
	switch effective {
	case paramEffectRestart:
		return "静态参数/待重启：修改已写入配置，需在重启窗口生效，请安排变更计划"
	case paramEffectNewConn:
		return "新连接生效：存量会话不受影响，新建立的连接将使用新值"
	default:
		return "立即生效：建议观察实例监控指标（连接数/内存/QPS）确认无异常"
	}
}
