package services

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/models"
)

// TiDB 种子一致性守门测试。
//
// 设计口径：TiDB 规范目录「严格一一镜像」MySQL 段。审核引擎侧 TiDB 复用 MySQL
// 引擎（TiDB parser 即 MySQL 方言，rule_key 后半段完全一致），独立 db_type=tidb
// 只为在规范页面按数据库类型区分配置与展示 logo。
//
// 因此守三条红线：
//  1. TestTiDBSeedMirrorsMySQL：后缀集合必须双向相等（少了 ⇒ TiDB 走判级兜底；
//     多了 ⇒ 幽灵规则，页面可配但引擎永不触发）。
//  2. TestTiDBSeedFieldAligned：同后缀条目的 类别/规则类型/默认级别/参数 必须
//     逐字段一致，否则「MySQL 改了参数忘了同步 TiDB」，TiDB 工单会静默走旧口径。
//  3. TestTiDBSeedDbTypeAndRuleTypeValid：db_type 与规则类型合法。
//
// 新增/修改 MySQL 规则时若忘记同步 tidb 条目，1 或 2 会直接失败。

// ruleSuffix 去掉 "{db}.{category}." 前缀，只留 rule_key 后缀
func ruleSuffix(ruleKey, db string) string {
	rest := strings.TrimPrefix(ruleKey, db+".")
	if i := strings.Index(rest, "."); i >= 0 {
		return rest[i+1:]
	}
	return rest
}

// seedRulesByDb 按 db_type 聚合种子规则
func seedRulesByDb(db string) map[string]models.InsightSQLStandardRule {
	idx := make(map[string]models.InsightSQLStandardRule)
	for _, r := range defaultStandardRules() {
		if strings.HasPrefix(r.RuleKey, db+".") {
			idx[ruleSuffix(r.RuleKey, db)] = r
		}
	}
	return idx
}

// TestTiDBSeedMirrorsMySQL TiDB 与 MySQL 规则集必须双向等价（不允许缺、不允许独有）
func TestTiDBSeedMirrorsMySQL(t *testing.T) {
	mysqlRules := seedRulesByDb("mysql")
	tidbRules := seedRulesByDb("tidb")

	if len(mysqlRules) == 0 {
		t.Fatal("mysql 种子为空，测试前提不成立")
	}

	for suffix := range mysqlRules {
		if _, ok := tidbRules[suffix]; !ok {
			t.Errorf("MySQL 规则 %q 缺少对应的 tidb.* 种子：TiDB 工单会走判级兜底（张冠李戴），请补种子", suffix)
		}
	}
	for suffix := range tidbRules {
		if _, ok := mysqlRules[suffix]; !ok {
			t.Errorf("种子里的 tidb.* 规则 %q 在 MySQL 段没有对应项：TiDB 复用 MySQL 引擎，引擎不会产出该 key，属于幽灵规则，请删除", suffix)
		}
	}
}

// TestTiDBSeedFieldAligned 镜像条目除 rule_key / DbType 外的关键字段必须一致
func TestTiDBSeedFieldAligned(t *testing.T) {
	mysqlRules := seedRulesByDb("mysql")
	tidbRules := seedRulesByDb("tidb")

	for suffix, m := range mysqlRules {
		d, ok := tidbRules[suffix]
		if !ok {
			continue // 缺失已在 TestTiDBSeedMirrorsMySQL 报错
		}
		if d.Category != m.Category {
			t.Errorf("%q 的 Category 不一致：mysql=%q tidb=%q", suffix, m.Category, d.Category)
		}
		if d.RuleType != m.RuleType {
			t.Errorf("%q 的 RuleType 不一致：mysql=%q tidb=%q", suffix, m.RuleType, d.RuleType)
		}
		if d.BaseLevel != m.BaseLevel || d.Level != m.Level {
			t.Errorf("%q 的级别不一致：mysql=(%q,%q) tidb=(%q,%q)", suffix, m.BaseLevel, m.Level, d.BaseLevel, d.Level)
		}
		if string(d.Params) != string(m.Params) {
			t.Errorf("%q 的参数不一致：mysql=%s tidb=%s（MySQL 改参数时必须同步 TiDB）", suffix, m.Params, d.Params)
		}
		if d.Name != m.Name {
			t.Errorf("%q 的名称不一致：mysql=%q tidb=%q", suffix, m.Name, d.Name)
		}
		if d.Implemented != m.Implemented {
			t.Errorf("%q 的 Implemented 不一致：mysql=%v tidb=%v", suffix, m.Implemented, d.Implemented)
		}
	}
}

// TestTiDBSeedDbTypeAndRuleTypeValid 种子里的 tidb 规则 db_type 与规则类型必须合法
func TestTiDBSeedDbTypeAndRuleTypeValid(t *testing.T) {
	count := 0
	for _, r := range defaultStandardRules() {
		if !strings.HasPrefix(r.RuleKey, "tidb.") {
			continue
		}
		count++
		if r.DbType != "tidb" {
			t.Errorf("种子 %q 的 DbType=%q，期望 tidb", r.RuleKey, r.DbType)
		}
		if !isAllowedRuleType(r.RuleType) {
			t.Errorf("种子 %q 的 RuleType=%q 非法", r.RuleKey, r.RuleType)
		}
	}
	if count == 0 {
		t.Fatal("种子里没有任何 tidb.* 规则")
	}
}

// TestStandardSeedRuleKeyUnique 全部种子（含 mysql/postgres/oracle/tidb）规则 key 不得重复
func TestStandardSeedRuleKeyUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, r := range defaultStandardRules() {
		if seen[r.RuleKey] {
			t.Errorf("种子规则 key 重复：%q", r.RuleKey)
			continue
		}
		seen[r.RuleKey] = true
	}
}

// TestTiDBParamsMatchMySQL 参数装配等价性：
//
// 把整段 mysql 规则依次 applyStandardRule 到一份默认参数上，再把整段 tidb 规则
// apply 到另一份上，两份 InspectParams 必须完全相同。这条测试直接证明「TiDB 实例
// 通过规范配出来的审核参数与 MySQL 实例完全一致」——只要 applyStandardRule 里漏了
// 某个 tidb.* 分支，对应开关就不会被点亮，本条会立刻失败。
func TestTiDBParamsMatchMySQL(t *testing.T) {
	mysqlParams := config.DefaultInspectParams()
	tidbParams := config.DefaultInspectParams()

	// 按种子顺序分别装配（顺序会影响共用参数的多规则写入，两边顺序一致才能逐字段比）
	for _, r := range defaultStandardRules() {
		if strings.HasPrefix(r.RuleKey, "mysql.") {
			applyStandardRule(&mysqlParams, r)
		}
	}
	for _, r := range defaultStandardRules() {
		if strings.HasPrefix(r.RuleKey, "tidb.") {
			applyStandardRule(&tidbParams, r)
		}
	}

	if !reflect.DeepEqual(mysqlParams, tidbParams) {
		mj, _ := json.Marshal(mysqlParams)
		tj, _ := json.Marshal(tidbParams)
		t.Errorf("mysql 与 tidb 装配出的 InspectParams 不一致\nmysql: %s\ntidb : %s", mj, tj)
	}
}

// TestNormalizeRuleDbTypeCoversAllInstanceTypes 实例库类型 → 规范目录类型的映射守门
func TestNormalizeRuleDbTypeCoversAllInstanceTypes(t *testing.T) {
	cases := map[string]string{
		"MySQL":      "mysql",
		"PostgreSQL": "postgres",
		"Kingbase":   "kingbase", // 金仓有独立目录（按 postgres 段镜像生成）
		"KingbaseES": "kingbase", // 金仓产品全称，实例表里也可能出现
		"Oracle":     "oracle",
		"TiDB":       "tidb",
		"Doris":      "mysql",     // Doris 暂无独立目录，复用 MySQL 规则
		"SQLServer":  "sqlserver", // SQL Server 有独立目录
		"MSSQL":      "sqlserver", // 同一数据库的另一种写法
	}
	for in, want := range cases {
		if got := normalizeRuleDbType(in); got != want {
			t.Errorf("normalizeRuleDbType(%q) = %q, want %q", in, got, want)
		}
	}
}
