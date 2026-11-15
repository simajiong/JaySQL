package services

import (
	"strings"
	"testing"
)

// 回归：Oracle 实例展开 HR.Tables 曾整组报「加载 "HR" 对象列表失败」，
// 根因是 SQL 为重构残壳——内联子查询缺别名、外层引用了不存在的 cc 前缀，
// Oracle 抛 ORA-00904。这里锁住三条约定：
//  1. 单层查询：不出现 FROM ( 内联视图（残壳温床）；
//  2. 三张字典表齐全：*_tab_columns / *_objects / *_col_comments
//     （*_objects 而非 *_tables：DBA 特权账号在 all_tables 上曾拿不到行）；
//  3. schema 参数大写并经字符串字面量安全拼入。
func assertOracleTablesQueryShape(t *testing.T, query string) {
	t.Helper()
	if strings.Contains(strings.ToUpper(query), "FROM (") {
		t.Fatalf("Oracle 表元信息查询不应包含内联视图（历史残壳导致 ORA-00904）\n%s", query)
	}
	for _, want := range []string{
		"_tab_columns c",
		"_objects o",
		"object_type = 'TABLE'",
		"_col_comments cc",
		"c.owner AS table_schema",
		"cc.comments AS column_comment",
		"WHERE c.owner = 'HR'",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("SQL 缺少关键结构 %q\n%s", want, query)
		}
	}
	if strings.Contains(strings.ToUpper(query), "ALL_TABLES") || strings.Contains(strings.ToUpper(query), "DBA_TABLES") {
		t.Fatalf("不应回退到 *_tables（DBA 特权账号可见性过严）\n%s", query)
	}
	// 输出列名与 MySQL/PG 版本口径一致（Go 侧按这些键取值）
	for _, want := range []string{"table_schema", "table_name", "column_name", "column_comment"} {
		if !strings.Contains(query, want) {
			t.Fatalf("SQL 缺少输出列 %q\n%s", want, query)
		}
	}
}

func TestOracleTablesQueryShape(t *testing.T) {
	assertOracleTablesQueryShape(t, oracleTablesQuery("all", "HR"))
	assertOracleTablesQueryShape(t, oracleTablesQuery("dba", "HR"))
}

// 回归（2026-11-12 生产实锤）：DBA 特权账号未被显式授权 HR/DBA1 的表时，
// all_* 字典一行都拿不到（dba_users/dba_segments 却全可见，模式树连大小都有），
// Tables 被伪装成「该库下暂无表」。约定：先查 all_*，为空降级 dba_*，
// 两个目录前缀必须都能正确拼出三张字典表。
func TestOracleTablesQueryCatalogFallback(t *testing.T) {
	all := oracleTablesQuery("all", "HR")
	dba := oracleTablesQuery("dba", "HR")

	for _, want := range []string{"all_tab_columns", "all_objects", "all_col_comments"} {
		if !strings.Contains(all, want) {
			t.Fatalf("all 目录版本缺少 %q\n%s", want, all)
		}
		if strings.Contains(all, "dba_") {
			t.Fatalf("all 目录版本不应包含 dba_ 字典\n%s", all)
		}
	}
	for _, want := range []string{"dba_tab_columns", "dba_objects", "dba_col_comments"} {
		if !strings.Contains(dba, want) {
			t.Fatalf("dba 目录版本缺少 %q\n%s", want, dba)
		}
		if strings.Contains(dba, "all_") {
			t.Fatalf("dba 目录版本不应包含 all_ 字典\n%s", dba)
		}
	}
}

// schema 需转大写（Oracle 字典视图的 owner 统一大写存储），调用方负责ToUpper，
// 这里验证小写输入时拼入的字面量原样（不吞字符）。
func TestOracleTablesQueryQuote(t *testing.T) {
	query := oracleTablesQuery("all", "hr")
	if !strings.Contains(query, `'hr'`) {
		t.Fatalf("schema 字面量未正确拼入\n%s", query)
	}
}
