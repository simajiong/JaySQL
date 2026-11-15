// Package dbfamily 是「数据库类型 → 引擎家族」的唯一判定入口。
//
// 为什么单独抽一个叶子包：
//   - 同一个 db_type 在不同模块（纳管、连接测试、元数据同步、SQL 工作台、权限）
//     都要判一次「它到底是 MySQL 系还是 Oracle / PG 系」，历史上每个模块各写一份
//     switch（das/services 的 IsMySQLFamily、各 execute_*.go 的 strings.EqualFold…），
//     新增一种类型就要满地找。这里收敛成一份。
//   - OceanBase 一个类型对应两种租户模式（MySQL 模式 / Oracle 模式），只靠 db_type
//     字符串判不出来，必须结合 extra_config 里的 oceanbase_mode。判定口径只此一处。
//   - 包内不依赖任何业务包，避免 common/services、das/services、tasks 之间成环。
//
// 安全不变量：对非 OceanBase 类型，EffectiveFamily 的返回值与历史
// das/services.normalizeDBType + IsMySQLFamily / IsPostgresFamily 的口径完全一致，
// 因此把老调用点切到本包不会改变既有行为，只新增 OceanBase 的判定。
package dbfamily

import (
	"encoding/json"
	"fmt"
	"strings"
)

// OceanBase 租户模式取值（写入 extra_config.oceanbase_mode 的规范值）。
const (
	OceanBaseMySQL  = "mysql"
	OceanBaseOracle = "oracle"
)

// 引擎家族（小写）。与历史 IsMySQLFamily / IsPostgresFamily 的返回口径保持一致。
const (
	FamilyMySQL      = "mysql"
	FamilyPostgres   = "postgresql"
	FamilyOracle     = "oracle"
	FamilyClickHouse = "clickhouse"
	FamilySQLServer  = "sqlserver"
	FamilyMongoDB    = "mongodb"
)

// Normalize 归一化数据库类型字符串：去空白 + 转小写。
func Normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// IsOceanBase 判断数据库类型是否为 OceanBase。
func IsOceanBase(dbType string) bool {
	return Normalize(dbType) == "oceanbase"
}

// NormalizeMode 归一化 OceanBase 租户模式。
// 识别不了返回空串，由调用方决定兜底（通常为 MySQL 模式）。
func NormalizeMode(raw string) string {
	switch Normalize(raw) {
	case "mysql", "mysql_mode", "mysql-mode", "obmysql", "ob_mysql",
		"oceanbase_mysql", "oceanbase-mysql":
		return OceanBaseMySQL
	case "oracle", "oracle_mode", "oracle-mode", "oboracle", "ob_oracle",
		"oceanbase_oracle", "oceanbase-oracle":
		return OceanBaseOracle
	default:
		return ""
	}
}

// ParseExtraConfig 解析实例的 extra_config JSON，失败或为空时返回空 map（不 panic）。
func ParseExtraConfig(extraConfig []byte) map[string]any {
	if len(extraConfig) == 0 {
		return map[string]any{}
	}
	var cfg map[string]any
	if err := json.Unmarshal(extraConfig, &cfg); err != nil || cfg == nil {
		return map[string]any{}
	}
	return cfg
}

// OceanBaseMode 从 extra_config 解析 OceanBase 租户模式。
// 缺省 / 无法识别一律回落到 MySQL 模式（OceanBase 建租户时最常见的形态）。
func OceanBaseMode(extraConfig []byte) string {
	cfg := ParseExtraConfig(extraConfig)
	raw, ok := cfg["oceanbase_mode"]
	if !ok || raw == nil {
		return OceanBaseMySQL
	}
	mode := NormalizeMode(fmt.Sprintf("%v", raw))
	if mode == "" {
		return OceanBaseMySQL
	}
	return mode
}

// OceanBaseOracleMode 便捷判断：该 OceanBase 实例是否处于 Oracle 模式。
func OceanBaseOracleMode(extraConfig []byte) bool {
	return OceanBaseMode(extraConfig) == OceanBaseOracle
}

// EffectiveFamily 返回数据库类型对应的实际引擎家族（小写）。
//
// 对 OceanBase：MySQL 模式 → mysql；Oracle 模式 → oracle。
// 对其它类型：与历史口径逐字符一致；未知类型原样返回小写字符串，
// 便于调用方在错误信息里回显用户填的类型。
func EffectiveFamily(dbType string, extraConfig []byte) string {
	switch Normalize(dbType) {
	case "mysql", "tidb", "doris", "mariadb", "polardb", "polardb-mysql", "polardbmysql":
		return FamilyMySQL
	case "postgresql", "postgres", "polardb-pg", "polardb-postgresql", "polardbpg",
		// Kingbase（金仓 KingbaseES）兼容 PostgreSQL 协议与方言：连接驱动、系统字典
		// （pg_catalog / information_schema）、SQL 执行与审核规则全部可复用 PG 通道。
		// 归到 postgres 家族后，DAS 执行器、表元数据、schema 浏览、权限申请等
		// 所有基于家族判定（IsPostgres）的模块一次性打通，无需逐处加类型枚举。
		"kingbase", "kingbasees":
		return FamilyPostgres
	case "oracle":
		return FamilyOracle
	case "clickhouse":
		return FamilyClickHouse
	case "sqlserver":
		return FamilySQLServer
	case "mongodb":
		return FamilyMongoDB
	}
	if IsOceanBase(dbType) {
		if OceanBaseOracleMode(extraConfig) {
			return FamilyOracle
		}
		return FamilyMySQL
	}
	return Normalize(dbType)
}

// IsMySQL 判断该实例是否按 MySQL 协议/方言处理（含 OceanBase MySQL 模式）。
func IsMySQL(dbType string, extraConfig []byte) bool {
	return EffectiveFamily(dbType, extraConfig) == FamilyMySQL
}

// IsPostgres 判断该实例是否按 PostgreSQL 处理。
func IsPostgres(dbType string, extraConfig []byte) bool {
	return EffectiveFamily(dbType, extraConfig) == FamilyPostgres
}

// IsOracle 判断该实例是否按 Oracle 处理（含 OceanBase Oracle 模式）。
func IsOracle(dbType string, extraConfig []byte) bool {
	return EffectiveFamily(dbType, extraConfig) == FamilyOracle
}
