package services

import (
	"encoding/json"
	"fmt"
	"strings"

	orderScopes "github.com/simajiong/jaysql/internal/orders/scopes"
)

func supportsDeepInspect(dbType string) bool {
	// 白名单统一由 scopes.SupportedOrderDbTypes 提供（列表页、数据源下拉、执行链同一口径）。
	// TiDB/Doris 复用 MySQL 审核引擎（TiDB parser + MySQL 协议探测，引擎内已有
	// IsTiDB 版本分支）；Kingbase 复用 PG 引擎；Oracle / SQLServer 走专用文本解析引擎，
	// 连库规则失败即跳过。
	return orderScopes.SupportsOrderDbType(dbType)
}

// isOracleDB 判断是否为 Oracle 数据源（工单目标解析 / 规范引擎分发共用）
func isOracleDB(dbType string) bool {
	return strings.EqualFold(strings.TrimSpace(dbType), "Oracle")
}

// tidbParserCompatibleDB 该数据库类型的 SQL 能否交给 TiDB parser 解析。
//
// TiDB parser 只认 MySQL 方言：MySQL/TiDB/Doris 兼容；PostgreSQL / SQLServer /
// Oracle 等语法不兼容（NUMBER / VARCHAR2 / GENERATED...AS IDENTITY / 注释里的
// 中文等都会让它直接报「SQL解析错误」）。工单创建链路不得对后者做 parser 硬校验
// —— 语法与规范问题统一交给规范预审（Oracle 走专用文本解析引擎），解析计数
// 一律用语法感知拆分（sqlsplit.Split），保证「保存不被解析器误拦」。
func tidbParserCompatibleDB(dbType string) bool {
	switch strings.ToLower(strings.TrimSpace(dbType)) {
	case "mysql", "tidb", "doris":
		return true
	default:
		return false
	}
}

// oracleServiceName 从实例 extra_config 中取 Oracle 服务名（未配置返回空串）
func oracleServiceName(extraConfig []byte) string {
	cfg := struct {
		ServiceName string `json:"service_name"`
	}{}
	if len(extraConfig) > 0 {
		_ = json.Unmarshal(extraConfig, &cfg)
	}
	return strings.TrimSpace(cfg.ServiceName)
}

// inspectConnTarget 返回审核引擎的连接与探测目标。
// MySQL 的 database/schema 同义，连接目标就是库名（s.Schema）；
// PG 是 database/schema 两级：连接串用 database，schema 单独传给 dao
// 用于表/视图存在性探测（information_schema.table_schema 限定）。
func inspectConnTarget(dbType string, database, schema string) (connDB string, inspectSchema string) {
	schema = strings.TrimSpace(schema)
	database = strings.TrimSpace(database)
	if isPgLikeDB(dbType) {
		connDB := database
		if connDB == "" {
			connDB = schema
		}
		// schema 字段是「database.schema」完整资源路径；探测 information_schema/pg_catalog
		// 时必须用裸 schema 名，否则 table_schema/schemaname 永远匹配不上，存在性探测静默失效。
		if connDB != "" && strings.HasPrefix(schema, connDB+".") {
			schema = strings.TrimPrefix(schema, connDB+".")
		}
		return connDB, schema
	}
	return schema, schema
}

func supportsOrderExport(dbType string) bool {
	switch dbType {
	case "MySQL", "TiDB", "Doris":
		return true
	default:
		return false
	}
}

func validateOrderSQLTypeSupport(dbType, sqlType string) error {
	if dbType == "" || sqlType == "" {
		return nil
	}
	if sqlType == "EXPORT" {
		return fmt.Errorf("当前版本仅支持DDL/DML工单，不支持导出工单")
	}
	// MIXED（DDL + DML 同单）与 DDL/DML 走同一条审批与执行链路，
	// 唯一的差异在任务分派：每条任务按自己的类型选执行器。
	// 支持范围统一取 scopes.SupportedOrderDbTypes（与列表页、数据源下拉同源）。
	if !orderScopes.SupportsOrderDbType(dbType) {
		return fmt.Errorf("当前数据库类型暂不支持工单: %s", dbType)
	}
	return nil
}
