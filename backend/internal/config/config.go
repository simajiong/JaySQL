package config

import "strings"

type App struct {
	ListenAddress string `mapstructure:"listen_address" json:"listen_address" yaml:"listen_address"`
	Environment   string `mapstructure:"environment" json:"environment" yaml:"environment"`
	SECRET_KEY    string `mapstructure:"secret_key" json:"secret_key" yaml:"secret_key"`
}

type Crontab struct {
	SyncDBMetas string `mapstructure:"sync_db_metas" json:"sync_db_metas" yaml:"sync_db_metas"`
	// MetaRefreshTTL 库/schema 元数据快照的「读时惰性刷新」窗口（秒）。
	//
	// 刷新本身不再依赖定时任务：建库/改库成功后平台会主动失效（见
	// common/tasks/meta_refresh.go），本项只决定「读接口发现快照过期后
	// 多久补刷一次」，用于兜住平台无感知的变更（DBA 直连客户端建库）。
	//   0（或不配）  回落默认 60s
	//   负数         关闭读时惰性刷新，只保留写后失效 + 定时对账
	MetaRefreshTTL int `mapstructure:"meta_refresh_ttl" json:"meta_refresh_ttl" yaml:"meta_refresh_ttl"`
	// 实例连通性探活。留空/不配一律回落代码默认值（见 common/services/instance_probe.go）：
	// tick @every 30s、异常实例 60s 复测、可用实例 600s 兜底。
	InstanceProbe              string `mapstructure:"instance_probe" json:"instance_probe" yaml:"instance_probe"`
	InstanceProbeErrorInterval int    `mapstructure:"instance_probe_error_interval" json:"instance_probe_error_interval" yaml:"instance_probe_error_interval"`
	InstanceProbeOKInterval    int    `mapstructure:"instance_probe_ok_interval" json:"instance_probe_ok_interval" yaml:"instance_probe_ok_interval"`
	InstanceProbeBatchSize     int    `mapstructure:"instance_probe_batch_size" json:"instance_probe_batch_size" yaml:"instance_probe_batch_size"`
}

type Log struct {
	Level   string `mapstructure:"level" json:"level" yaml:"level"`
	RootDir string `mapstructure:"root_dir" json:"root_dir" yaml:"root_dir"`
}

type Database struct {
	Driver          string `mapstructure:"driver" json:"driver" yaml:"driver"`
	Host            string `mapstructure:"host" json:"host" yaml:"host"`
	Port            int    `mapstructure:"port" json:"port" yaml:"port"`
	Database        string `mapstructure:"database" json:"database" yaml:"database"`
	UserName        string `mapstructure:"username" json:"username" yaml:"username"`
	Password        string `mapstructure:"password" json:"password" yaml:"password"`
	Charset         string `mapstructure:"charset" json:"charset" yaml:"charset"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns" json:"max_idle_conns" yaml:"max_idle_conns"`
	MaxOpenConns    int    `mapstructure:"max_open_conns" json:"max_open_conns" yaml:"max_open_conns"`
	ConnMaxIdleTime int    `mapstructure:"conn_max_idle_time" json:"conn_max_idle_time" yaml:"conn_max_idle_time"`
	ConnMaxLifetime int    `mapstructure:"conn_max_life_time" json:"conn_max_life_time" yaml:"conn_max_life_time"`
}

// Deprecated: Redis 已不再是 JaySQL 的依赖（2026-10 移除）。
// 工单日志推送改走进程内总线（pkg/eventbus），执行锁改走进程内带租约锁（pkg/lock）。
// 结构体保留仅为兼容旧配置文件：viper 读到多余的 redis 段会静默忽略，
// 已部署环境的 config.yaml 无需改动即可继续启动；新部署可直接删除该段。
type Redis struct {
	Host     string `mapstructure:"host" json:"host" yaml:"host"`
	Port     int    `mapstructure:"port" json:"port" yaml:"port"`
	DB       int    `mapstructure:"db" json:"db" yaml:"db"`
	Password string `mapstructure:"password" json:"password" yaml:"password"`
}

type Das struct {
	MaxExecutionTime  uint64 `mapstructure:"max_execution_time" json:"max_execution_time" yaml:"max_execution_time"`
	DefaultReturnRows uint64 `mapstructure:"default_return_rows" json:"default_return_rows" yaml:"default_return_rows"`
	MaxReturnRows     uint64 `mapstructure:"max_return_rows" json:"max_return_rows" yaml:"max_return_rows"`
}

type Ghost struct {
	Path string   `mapstructure:"path" json:"path" yaml:"path"`
	Args []string `mapstructure:"args" json:"args" yaml:"args"`
}

type Notify struct {
	NoticeURL string `mapstructure:"notice_url" json:"notice_url" yaml:"notice_url"`
	Wechat    struct {
		Enable  bool   `mapstructure:"enable" json:"enable" yaml:"enable"`
		Webhook string `mapstructure:"webhook" json:"webhook" yaml:"webhook"`
	}
	Mail struct {
		Enable   bool   `mapstructure:"enable" json:"enable" yaml:"enable"`
		Username string `mapstructure:"username" json:"username" yaml:"username"`
		Password string `mapstructure:"password" json:"password" yaml:"password"`
		Host     string `mapstructure:"host" json:"host" yaml:"host"`
		Port     int    `mapstructure:"port" json:"port" yaml:"port"`
	}
	DingTalk struct {
		Enable   bool   `mapstructure:"enable" json:"enable" yaml:"enable"`
		Webhook  string `mapstructure:"webhook" json:"webhook" yaml:"webhook"`
		Keywords string `mapstructure:"keywords" json:"keywords" yaml:"keywords"`
	}
}

// AIConfig 大模型能力接入的平台级配置。
//
// 注意：服务商凭证（API Key / 端点地址）存储在数据库 insight_llm_providers 表
// 并经过 AES-256-GCM 加密，**禁止写在本配置文件里**。本节点只保留开关与安全策略。
type AIConfig struct {
	Enabled              bool   `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
	AllowInsecureHTTP    bool   `mapstructure:"allow_insecure_http" json:"allow_insecure_http" yaml:"allow_insecure_http"`
	FailoverEnabled      bool   `mapstructure:"failover_enabled" json:"failover_enabled" yaml:"failover_enabled"`
	DegradeToRuleEngine  bool   `mapstructure:"degrade_to_rule_engine" json:"degrade_to_rule_engine" yaml:"degrade_to_rule_engine"`
	HealthCheckInterval  string `mapstructure:"health_check_interval" json:"health_check_interval" yaml:"health_check_interval"`
	RequestTimeoutSecond int    `mapstructure:"request_timeout_seconds" json:"request_timeout_seconds" yaml:"request_timeout_seconds"`
}

// AISettings 返回 AI 模块的平台级配置，并对零值应用安全默认值。
//
// 老配置文件里没有 ai 节点时 viper 会填零值，其中 request_timeout_seconds=0
// 会让 HTTP 客户端立刻超时、health_check_interval="" 会让定时任务无法解析，
// 因此使用方一律通过本方法读取，不要直接读 c.AI。
func (c Configuration) AISettings() AIConfig {
	cfg := c.AI
	if cfg.RequestTimeoutSecond <= 0 {
		cfg.RequestTimeoutSecond = 60
	}
	if strings.TrimSpace(cfg.HealthCheckInterval) == "" {
		cfg.HealthCheckInterval = "*/5 * * * *"
	}
	return cfg
}

// Order 工单执行链的配置段。
type Order struct {
	// PGRollbackMaxRows 控制 PostgreSQL（含 Kingbase）DML 工单「回滚 SQL 生成」的影响行数上限：
	// 工单执行成功且累计影响行数不超过该值时，才通过逻辑解码（test_decoding 插件 + 临时复制槽）
	// 生成精确的逆向回滚语句；超过上限则跳过生成（批量清理场景请分批执行）。
	//   未配置 / 0  → 回落默认 1000 行
	//   负数        → 关闭 PostgreSQL 回滚 SQL 生成（工单照常执行）
	// 注意：该能力还要求实例 wal_level=logical，未开启时工单正常执行但会跳过回滚生成并记录提示。
	PGRollbackMaxRows int64 `mapstructure:"pg_rollback_max_rows" json:"pg_rollback_max_rows" yaml:"pg_rollback_max_rows"`
}

// DefaultPGRollbackMaxRows 是 PGRollbackMaxRows 的默认值。
// 与 MySQL 侧 10 万行的硬编码阈值不同：PG 逻辑解码需要逐行解析文本输出并生成
// 逐行逆向语句，且生成阶段在同一执行链内同步完成，默认收紧到 1000 行，
// 由用户按实际场景放宽或收紧。
const DefaultPGRollbackMaxRows int64 = 1000

// OrderSettings 返回工单链配置，并对零值应用默认值。
// 使用方一律通过本方法读取，不要直接读 c.Order。
func (c Configuration) OrderSettings() Order {
	cfg := c.Order
	if cfg.PGRollbackMaxRows == 0 {
		cfg.PGRollbackMaxRows = DefaultPGRollbackMaxRows
	}
	return cfg
}

type Configuration struct {
	App      App      `mapstructure:"app" json:"app" yaml:"app"`
	Crontab  Crontab  `mapstructure:"crontab" json:"crontab" yaml:"crontab"`
	Log      Log      `mapstructure:"log" json:"log" yaml:"log"`
	Database Database `mapstructure:"database" json:"database" yaml:"database"`
	Redis    Redis    `mapstructure:"redis" json:"redis" yaml:"redis"`
	Das      Das      `mapstructure:"das" json:"das" yaml:"das"`
	Ghost    Ghost    `mapstructure:"ghost" json:"ghost" yaml:"ghost"`
	Order    Order    `mapstructure:"order" json:"order" yaml:"order"`
	Notify   Notify   `mapstructure:"-" json:"notify" yaml:"-"`
	AI       AIConfig `mapstructure:"ai" json:"ai" yaml:"ai"`
}
