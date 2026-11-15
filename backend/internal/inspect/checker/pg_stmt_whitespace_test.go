package checker

// PG 语句分发的空白鲁棒性回归
//
// 线上工单 `create  table  t_01(id int)`（关键字之间是双空格）审核结果 0 错误 0 警告，
// 但把同样语句写成单空格能命中 6 条规则。根因是 PG 引擎用
// `strings.HasPrefix(sqlsplit.NormalizeLeading(stmt), "create table")` 分发语句，
// 而 NormalizeLeading 此前只做小写化、不折叠空白，双空格直接落进 default 分支 → 整条语句零结论。
//
// 本用例锁定：同一语义的 SQL，无论关键字之间是单空格、多空格、Tab 还是换行，结论必须完全一致。

import (
	"sort"
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
)

func pgKeys(t *testing.T, sql string) []string {
	t.Helper()
	keys := runPGTextRules(t, sql, func(p *config.InspectParams) {
		// 开一条纯文本可判定的建表规则，保证基线必然有结论
		p.CHECK_TABLE_PRIMARY_KEY = true
	})
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestPGStatementDispatchWhitespaceInsensitive(t *testing.T) {
	base := pgKeys(t, "create table t_ws_case (id int);")
	if len(base) == 0 {
		t.Fatalf("基线语句未产生任何结论，用例无效")
	}

	variants := []string{
		"create  table  t_ws_case (id int);",          // 关键字双空格（线上工单原样）
		"create\ttable\tt_ws_case (id int);",          // Tab
		"create\n  table\n  t_ws_case (id int);",      // 换行
		"  CREATE   TABLE   t_ws_case (id int);",      // 大写 + 多空格 + 前导空白
		"-- 说明注释\ncreate  table  t_ws_case (id int);", // 前导注释 + 多空格
	}
	for _, sql := range variants {
		got := pgKeys(t, sql)
		if strings.Join(got, ",") != strings.Join(base, ",") {
			t.Errorf("结论不一致\n  sql: %q\n  单空格=%v\n  实际=%v", sql, base, got)
		}
	}
}
