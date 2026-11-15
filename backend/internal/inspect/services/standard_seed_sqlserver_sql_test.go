package services

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// 补种脚本 backend/sql/20261108_seed_sqlserver_rules.sql 与种子目录必须逐条一致：
//   - 脚本漏一条 → 存量环境少一条规则，引擎产出的 key 找不到对应规则，
//     结论会被「致命类别兜底」挂到别的规则上（张冠李戴）；
//   - 脚本多一条 → 页面能配但永不触发（幽灵规则）。
//
// 同时校验 UNION ALL 派生表首行必须显式写列别名：MySQL 只从第一条 SELECT 取列名，
// 首行是裸字面量时外层 r.rule_key 直接报 Error 1054 Unknown column 'r.rule_key'
// （历史故障，见该脚本 STEP 2 上方注释）。
func TestSQLServerSeedSQLMatchesCatalog(t *testing.T) {
	const path = "../../../sql/20261108_seed_sqlserver_rules.sql"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取补种脚本失败: %v", err)
	}
	script := string(raw)

	// 1. 截取 STEP 2 的派生表：CROSS JOIN ( ... ) r
	const marker = "CROSS JOIN ("
	start := strings.Index(script, marker)
	if start < 0 {
		t.Fatalf("脚本里找不到 %q，STEP 2 结构可能已变更", marker)
	}
	rest := script[start+len(marker):]
	end := strings.Index(rest, "\n) r")
	if end < 0 {
		t.Fatalf("脚本里找不到派生表别名 \"\\n) r\"，STEP 2 结构可能已变更")
	}
	block := rest[:end]

	// 2. 首条 SELECT 必须带全部列别名
	firstSel := block
	if idx := strings.Index(block, "UNION ALL SELECT"); idx >= 0 {
		firstSel = block[:idx]
	}
	for _, alias := range []string{
		"rule_key", "category", "name", "description",
		"rule_type", "level", "params", "sort_order",
	} {
		re := regexp.MustCompile(`(?i)\bAS\s+` + alias + `\b`)
		if !re.MatchString(firstSel) {
			t.Errorf("派生表首条 SELECT 缺少列别名 AS %s："+
				"UNION ALL 派生表的列名只取自第一条 SELECT，缺别名会让外层 r.%s 报 Error 1054",
				alias, alias)
		}
	}

	// 3. 提取 rule_key 并与种子目录比对
	keyRe := regexp.MustCompile(`'(sqlserver\.(?:struct|query_update)\.[a-z0-9_]+)'`)
	matched := keyRe.FindAllStringSubmatch(block, -1)
	if len(matched) == 0 {
		t.Fatal("派生表里没有解析到任何 sqlserver.* 规则键")
	}
	sqlKeys := make(map[string]struct{}, len(matched))
	for _, m := range matched {
		if _, dup := sqlKeys[m[1]]; dup {
			t.Errorf("脚本里 rule_key 重复: %s", m[1])
		}
		sqlKeys[m[1]] = struct{}{}
	}

	seedKeys := make(map[string]struct{})
	for _, r := range sqlserverStandardRules() {
		seedKeys[r.RuleKey] = struct{}{}
	}

	if len(sqlKeys) != len(seedKeys) {
		t.Errorf("规则条数不一致：脚本 %d 条，种子目录 %d 条", len(sqlKeys), len(seedKeys))
	}
	for k := range seedKeys {
		if _, ok := sqlKeys[k]; !ok {
			t.Errorf("种子目录有、补种脚本缺失: %s", k)
		}
	}
	for k := range sqlKeys {
		if _, ok := seedKeys[k]; !ok {
			t.Errorf("补种脚本有、种子目录不存在（幽灵规则）: %s", k)
		}
	}
}
