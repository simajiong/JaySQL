package services

import (
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/models"
)

// ============================================================================
// 金仓（Kingbase）SQL 规范目录
//
// 金仓 KingbaseES V8 兼容 PostgreSQL 协议与方言（pg_catalog / pg_stat_* /
// SERIAL、COMMENT ON、GENERATED ... AS IDENTITY 全部可用），审核引擎直接复用
// PG 文本解析链（checker.pg_rules.go），因此规范目录不需要另写一套判定逻辑，
// 只需要一套「属于金仓自己」的规则条目：
//
//   - 独立条目（rule_key 前缀 kingbase.，db_type=kingbase）的价值在于「可独立配置」：
//     规范页里能看到金仓图标、能单独勾选/关闭/改阈值，而不必连 PostgreSQL 一起改；
//     这与 TiDB 镜像 MySQL 段的既有做法一致。
//   - 目录内容由 postgres 段**程序化镜像**生成，不逐条手抄：PG 段改参数/改级别/
//     改描述时金仓段自动跟随，不会出现「PG 改了、金仓没改」的静默漂移
//     （standard_seed_kingbase_test.go 把这条约束锁成回归）。
//   - 引擎产出的 rule_key 仍是 postgres.*（同一份引擎代码），预审结果与规则级别的
//     关联靠「去掉库前缀后的后缀匹配」完成（orders/services/syntax.go
//     matchRuleByKeyOrText），参数装配侧则由 applyStandardRule 里的
//     kingbase.* → postgres.* 别名统一映射。
// ============================================================================

const (
	postgresRulePrefix = "postgres."
	kingbaseRulePrefix = "kingbase."
)

// kingbaseRulesFromPostgres 按 postgres 段镜像出 kingbase 段规则目录。
//
// 只镜像 db_type == postgres 的条目；mysql/tidb/oracle/sqlserver 段里存在而 PG
// 段没有的能力项（UNSIGNED / 存储引擎 / 字符集 / ZEROFILL 等）不会出现在金仓段，
// 因为金仓上不存在这些语义，收录即幽灵规则。
func kingbaseRulesFromPostgres(rules []models.InsightSQLStandardRule) []models.InsightSQLStandardRule {
	mirrored := make([]models.InsightSQLStandardRule, 0, len(rules))
	for _, rule := range rules {
		if !strings.EqualFold(rule.DbType, "postgres") || !strings.HasPrefix(rule.RuleKey, postgresRulePrefix) {
			continue
		}
		clone := rule
		clone.RuleKey = kingbaseRulePrefix + strings.TrimPrefix(rule.RuleKey, postgresRulePrefix)
		clone.DbType = "kingbase"
		mirrored = append(mirrored, clone)
	}
	return mirrored
}
