package checker

// SQL Server 语句分发的空白鲁棒性回归（与 PG 同源问题，见 sql_whitespace.go）
//
// `create  table  t(...)` 这类多空白写法此前会让 parseSQLServerCreateTable 里
// `strings.Index(lower, "create table")` 落空 → 解析失败 → 整条 DDL 零结论。

import (
	"sort"
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
)

func sqlServerKeys(t *testing.T, sql string) []string {
	t.Helper()
	params := config.DefaultInspectParams()
	params.CHECK_TABLE_PRIMARY_KEY = true
	keys := map[string]bool{}
	for _, d := range runSQLServer(t, sql, params) {
		for _, s := range d.Summary {
			if s.RuleKey != "" {
				keys[s.RuleKey] = true
			}
		}
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestSQLServerStatementDispatchWhitespaceInsensitive(t *testing.T) {
	base := sqlServerKeys(t, "create table t_ws_case (id int);")
	if len(base) == 0 {
		t.Fatalf("基线语句未产生任何结论，用例无效")
	}
	variants := []string{
		"create  table  t_ws_case (id int);",
		"create\ttable\tt_ws_case (id int);",
		"create\n  table\n  t_ws_case (id int);",
		"  CREATE   TABLE   t_ws_case (id int);",
	}
	for _, sql := range variants {
		got := sqlServerKeys(t, sql)
		if strings.Join(got, ",") != strings.Join(base, ",") {
			t.Errorf("结论不一致\n  sql: %q\n  单空格=%v\n  实际=%v", sql, base, got)
		}
	}
}
