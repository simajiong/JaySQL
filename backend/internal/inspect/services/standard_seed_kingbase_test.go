package services

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
)

// 金仓（Kingbase）种子一致性守门测试。
//
// 设计口径：金仓段「严格一一镜像」PostgreSQL 段（同 TiDB 镜像 MySQL）。金仓兼容
// PG 协议与方言，审核引擎就是 PG 那套（checker/pg_rules.go），rule_key 后半段
// 完全一致，独立 db_type=kingbase 只为在规范页按库区分配置与展示 logo。
//
// 因此守四条红线：
//  1. TestKingbaseSeedMirrorsPostgres：后缀集合双向相等（少了 ⇒ 金仓实例走判级兜底；
//     多了 ⇒ 幽灵规则，页面可配但引擎永不触发）。
//  2. TestKingbaseSeedFieldAligned：同后缀条目的类别/规则类型/级别/参数/名称逐字段一致，
//     否则「PG 改了参数忘了同步金仓」，金仓工单会静默走旧口径。
//  3. TestKingbaseParamsMatchPostgres：把整段规则分别 apply 到默认参数上，两份
//     InspectParams 必须完全相同——直接证明「金仓实例配出来的审核参数与 PG 一致」。
//  4. TestNormalizeRuleDbTypeKingbase / TestKingbaseRuleKeyAlias：归一化与参数映射别名。

func TestKingbaseSeedMirrorsPostgres(t *testing.T) {
	pgRules := seedRulesByDb("postgres")
	kbRules := seedRulesByDb("kingbase")

	if len(pgRules) == 0 {
		t.Fatal("postgres 种子为空，测试前提不成立")
	}
	if len(kbRules) == 0 {
		t.Fatal("种子里没有任何 kingbase.* 规则：金仓实例会退化成零规则兜底")
	}

	for suffix := range pgRules {
		if _, ok := kbRules[suffix]; !ok {
			t.Errorf("PG 规则 %q 缺少对应的 kingbase.* 种子：金仓工单会走判级兜底，请补种子", suffix)
		}
	}
	for suffix := range kbRules {
		if _, ok := pgRules[suffix]; !ok {
			t.Errorf("种子里的 kingbase.* 规则 %q 在 PG 段没有对应项：金仓复用 PG 引擎，引擎不会产出该 key，属于幽灵规则", suffix)
		}
	}
}

func TestKingbaseSeedFieldAligned(t *testing.T) {
	pgRules := seedRulesByDb("postgres")
	kbRules := seedRulesByDb("kingbase")

	for suffix, p := range pgRules {
		k, ok := kbRules[suffix]
		if !ok {
			continue // 缺失已在 TestKingbaseSeedMirrorsPostgres 报错
		}
		if k.Category != p.Category {
			t.Errorf("%q 的 Category 不一致：postgres=%q kingbase=%q", suffix, p.Category, k.Category)
		}
		if k.RuleType != p.RuleType {
			t.Errorf("%q 的 RuleType 不一致：postgres=%q kingbase=%q", suffix, p.RuleType, k.RuleType)
		}
		if k.BaseLevel != p.BaseLevel || k.Level != p.Level {
			t.Errorf("%q 的级别不一致：postgres=(%q,%q) kingbase=(%q,%q)", suffix, p.BaseLevel, p.Level, k.BaseLevel, k.Level)
		}
		if string(k.Params) != string(p.Params) {
			t.Errorf("%q 的参数不一致：postgres=%s kingbase=%s", suffix, p.Params, k.Params)
		}
		if k.Name != p.Name || k.Description != p.Description {
			t.Errorf("%q 的名称/描述不一致：postgres=(%q,%q) kingbase=(%q,%q)", suffix, p.Name, p.Description, k.Name, k.Description)
		}
		if k.Implemented != p.Implemented {
			t.Errorf("%q 的 Implemented 不一致：postgres=%v kingbase=%v", suffix, p.Implemented, k.Implemented)
		}
		if k.DbType != "kingbase" {
			t.Errorf("%q 的 DbType=%q，期望 kingbase", k.RuleKey, k.DbType)
		}
		if !isAllowedRuleType(k.RuleType) {
			t.Errorf("%q 的 RuleType=%q 非法", k.RuleKey, k.RuleType)
		}
	}
}

// TestKingbaseParamsMatchPostgres 参数装配等价性：
// 整段 postgres 规则与整段 kingbase 规则分别装配到默认参数，两份必须完全相同。
// 只要 applyStandardRule 的别名漏了 kingbase.*，对应开关不会被点亮，本测试立刻失败。
func TestKingbaseParamsMatchPostgres(t *testing.T) {
	pgParams := config.DefaultInspectParams()
	kbParams := config.DefaultInspectParams()

	for _, r := range defaultStandardRules() {
		if hasRulePrefix(r.RuleKey, "postgres") {
			applyStandardRule(&pgParams, r)
		}
	}
	for _, r := range defaultStandardRules() {
		if hasRulePrefix(r.RuleKey, "kingbase") {
			applyStandardRule(&kbParams, r)
		}
	}

	if !reflect.DeepEqual(pgParams, kbParams) {
		pj, _ := json.Marshal(pgParams)
		kj, _ := json.Marshal(kbParams)
		t.Errorf("postgres 与 kingbase 装配出的 InspectParams 不一致\npostgres: %s\nkingbase: %s", pj, kj)
	}
}

func TestNormalizeRuleDbTypeKingbase(t *testing.T) {
	for _, in := range []string{"Kingbase", "kingbase", "KingbaseES", "kingbase-es", " Kingbase "} {
		if got := normalizeRuleDbType(in); got != "kingbase" {
			t.Errorf("normalizeRuleDbType(%q) = %q, want kingbase", in, got)
		}
	}
}

func TestKingbaseRuleKeyAlias(t *testing.T) {
	cases := map[string]string{
		"kingbase.struct.table_must_have_pk":                 "postgres.struct.table_must_have_pk",
		"kingbase.query_update.where_avoid_leading_wildcard": "postgres.query_update.where_avoid_leading_wildcard",
		// 其它段不受影响
		"postgres.struct.table_must_have_pk": "postgres.struct.table_must_have_pk",
		"mysql.struct.table_must_have_pk":    "mysql.struct.table_must_have_pk",
	}
	for in, want := range cases {
		if got := standardRuleKeyAlias(in); got != want {
			t.Errorf("standardRuleKeyAlias(%q) = %q, want %q", in, got, want)
		}
	}
}

func hasRulePrefix(ruleKey, db string) bool {
	return len(ruleKey) > len(db) && ruleKey[:len(db)+1] == db+"."
}
