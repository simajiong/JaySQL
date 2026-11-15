package checker

// 补齐的 15 条 PG DML 规则的纯文本回归（不连库）
//
// 这些规则此前在 PG 引擎里完全没有判定，规范里开了也不报（静默失效）。
// 本组用例锁定「文本级可判定」的部分：INSERT 字段列表解析、SET 分隔符、
// WHERE 类四条规则、SYSDATE()。依赖表结构的规则（主键/唯一键/索引列/NOT NULL）
// 由后端实测用例覆盖（pg_rule_matrix_live_test.go）。

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
)

// runPGTextRules 只跑 PG 引擎（不连库），返回命中的 rule key 集合
func runPGTextRules(t *testing.T, sql string, apply func(*config.InspectParams)) map[string]bool {
	t.Helper()
	params := config.DefaultInspectParams()
	if apply != nil {
		apply(&params)
	}
	svc := &SyntaxInspectService{
		DbType:        "PostgreSQL",
		InspectSchema: "public",
		DBSchema:      "postgres",
		SqlText:       sql,
		InspectParams: params,
	}
	datas, err := svc.runPGInspect()
	if err != nil {
		t.Fatalf("审核失败: %v, sql=%s", err, sql)
	}
	keys := map[string]bool{}
	for _, d := range datas {
		for _, s := range d.Summary {
			if s.RuleKey != "" {
				keys[s.RuleKey] = true
			}
		}
	}
	return keys
}

func requireKey(t *testing.T, keys map[string]bool, want, sql string, all []controllers.SummaryItem) {
	t.Helper()
	if !keys[want] {
		t.Errorf("未命中 %s（sql=%s），实际命中=%v", want, sql, keys)
	}
}

func TestPGInsertColumnListParsing(t *testing.T) {
	cases := []struct {
		sql   string
		want  []string
		has   bool
		title string
	}{
		{"INSERT INTO t (id, name) VALUES (1, 'a')", []string{"id", "name"}, true, "带字段列表"},
		{"INSERT INTO public.t (id, name) VALUES (1, 'a')", []string{"id", "name"}, true, "带 schema 前缀"},
		{"INSERT INTO \"Order\" (id) VALUES (1)", []string{"id"}, true, "带双引号表名"},
		{"INSERT INTO t VALUES (1, 'a')", nil, false, "无字段列表"},
		{"INSERT INTO t(id,name) VALUES (1,'a')", []string{"id", "name"}, true, "紧凑写法"},
	}
	for _, c := range cases {
		cols, has := pgInsertColumnList(stripPGStringsAndComments(c.sql))
		if has != c.has {
			t.Errorf("%s: has=%v want %v (cols=%v)", c.title, has, c.has, cols)
			continue
		}
		if len(cols) != len(c.want) {
			t.Errorf("%s: cols=%v want %v", c.title, cols, c.want)
			continue
		}
		for i := range cols {
			if !strings.EqualFold(cols[i], c.want[i]) {
				t.Errorf("%s: cols=%v want %v", c.title, cols, c.want)
				break
			}
		}
	}
}

func TestPGNewDMLRulesTextLevel(t *testing.T) {
	dupCols := func(p *config.InspectParams) { p.CHECK_INSERT_COLUMN_NO_DUP = true }
	specifyCols := func(p *config.InspectParams) { p.CHECK_INSERT_SPECIFY_COLUMNS = true }
	sysdate := func(p *config.InspectParams) { p.CHECK_INSERT_AVOID_SYSDATE = true }
	setSep := func(p *config.InspectParams) { p.CHECK_UPDATE_SET_SEPARATOR = true }
	leadingWildcard := func(p *config.InspectParams) { p.CHECK_WHERE_LEADING_WILDCARD = true }
	likeNoWildcard := func(p *config.InspectParams) { p.CHECK_WHERE_LIKE_WITHOUT_WILDCARD = true }
	notIn := func(p *config.InspectParams) { p.CHECK_WHERE_NOT_IN_NOT_LIKE = true }
	orCond := func(p *config.InspectParams) { p.CHECK_WHERE_OR_CONDITION = true }

	t.Run("字段名重复", func(t *testing.T) {
		sql := "INSERT INTO t (id, name, name) VALUES (1, 'a', 'b');"
		requireKey(t, runPGTextRules(t, sql, dupCols), "insert_stmt_col_name_no_dup", sql, nil)
	})
	t.Run("未指定字段列表", func(t *testing.T) {
		sql := "INSERT INTO t VALUES (1, 'a');"
		requireKey(t, runPGTextRules(t, sql, specifyCols), "insert_stmt_specify_columns", sql, nil)
	})
	t.Run("指定字段列表时不报未指定", func(t *testing.T) {
		sql := "INSERT INTO t (id, name) VALUES (1, 'a');"
		if runPGTextRules(t, sql, specifyCols)["insert_stmt_specify_columns"] {
			t.Errorf("已指定字段列表却报「未指定字段列表」")
		}
	})
	t.Run("SYSDATE函数", func(t *testing.T) {
		sql := "INSERT INTO t (id, create_time) VALUES (1, sysdate());"
		requireKey(t, runPGTextRules(t, sql, sysdate), "insert_stmt_avoid_sysdate_func", sql, nil)
	})
	t.Run("字面量中的sysdate不算", func(t *testing.T) {
		sql := "INSERT INTO t (id, name) VALUES (1, 'sysdate()');"
		if runPGTextRules(t, sql, sysdate)["insert_stmt_avoid_sysdate_func"] {
			t.Errorf("字符串字面量里的 sysdate() 被误报")
		}
	})
	t.Run("SET用AND分隔", func(t *testing.T) {
		sql := "UPDATE t SET a = 1 AND b = 2 WHERE id = 1;"
		requireKey(t, runPGTextRules(t, sql, setSep), "update_stmt_set_separator_check", sql, nil)
	})
	t.Run("SET值内布尔表达式不误报", func(t *testing.T) {
		sql := "UPDATE t SET a = (b > 1 AND c < 2) WHERE id = 1;"
		if runPGTextRules(t, sql, setSep)["update_stmt_set_separator_check"] {
			t.Errorf("括号内的 AND 被误报为 SET 分隔符错误")
		}
	})
	t.Run("前通配符", func(t *testing.T) {
		sql := "UPDATE t SET a = 1 WHERE name LIKE '%abc';"
		requireKey(t, runPGTextRules(t, sql, leadingWildcard), "where_avoid_leading_wildcard", sql, nil)
	})
	t.Run("后通配符不误报", func(t *testing.T) {
		sql := "UPDATE t SET a = 1 WHERE name LIKE 'abc%';"
		if runPGTextRules(t, sql, leadingWildcard)["where_avoid_leading_wildcard"] {
			t.Errorf("后通配符被误报")
		}
	})
	t.Run("LIKE无通配符", func(t *testing.T) {
		sql := "UPDATE t SET a = 1 WHERE name LIKE 'abc';"
		requireKey(t, runPGTextRules(t, sql, likeNoWildcard), "where_like_without_wildcard", sql, nil)
	})
	t.Run("NOT_IN", func(t *testing.T) {
		sql := "DELETE FROM t WHERE id NOT IN (1, 2);"
		requireKey(t, runPGTextRules(t, sql, notIn), "where_avoid_not_in_not_like", sql, nil)
	})
	t.Run("NOT_LIKE", func(t *testing.T) {
		sql := "DELETE FROM t WHERE name NOT LIKE '%x%';"
		requireKey(t, runPGTextRules(t, sql, notIn), "where_avoid_not_in_not_like", sql, nil)
	})
	t.Run("OR条件", func(t *testing.T) {
		sql := "UPDATE t SET a = 1 WHERE id = 1 OR status = 1;"
		requireKey(t, runPGTextRules(t, sql, orCond), "where_or_condition_check", sql, nil)
	})
	t.Run("括号内OR不误报", func(t *testing.T) {
		sql := "UPDATE t SET a = 1 WHERE id IN (1, 2) AND (status = 1 OR status = 2);"
		if runPGTextRules(t, sql, orCond)["where_or_condition_check"] {
			t.Errorf("括号内的 OR 被误报")
		}
	})
}

func TestPGTopLevelFromIndex(t *testing.T) {
	cases := []struct {
		sql  string
		want int
		desc string
	}{
		{"UPDATE t SET a = 1 FROM u WHERE t.id = u.id", 17, "顶层 FROM 应被找到"},
		{"UPDATE t SET a = (SELECT max(x) FROM u) WHERE id = 1", -1, "子查询内的 FROM 不算"},
		{"UPDATE t SET a = 1 WHERE id = 1", -1, "无 FROM"},
	}
	for _, c := range cases {
		got := pgTopLevelFromIndex(c.sql)
		if c.want < 0 && got >= 0 {
			t.Errorf("%s: 期望未找到，实际 %d", c.desc, got)
			continue
		}
		if c.want >= 0 && got < 0 {
			t.Errorf("%s: 期望找到 FROM，实际未找到", c.desc)
		}
	}
}

func TestPGUpdateFromCountsAsMultiTable(t *testing.T) {
	res := analyzePGDML("UPDATE t SET a = 1 FROM u WHERE t.id = u.id")
	if !res.OK || res.JoinTableCount != 2 {
		t.Fatalf("PG 多表 UPDATE 应计 2 张表，实际 %d", res.JoinTableCount)
	}
}
