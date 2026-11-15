package services

import (
	"strings"
	"testing"
)

// 回归：金仓实例（KingbaseES）展开 Tables 时曾整组报错
// 「加载 "public" 对象列表失败」，根因是 PG 段 SQL 用了多参 concat()，
// 而金仓的 sys.concat 只有 concat(text, text) 两参重载。
// 这里锁住两条约定：不出现 concat(、列拼接必须用 ||。
func TestPostgresTablesQueryStaysKingbaseCompatible(t *testing.T) {
	query := postgresTablesQuery("public")

	if strings.Contains(strings.ToLower(query), "concat(") {
		t.Fatalf("PG/金仓共用 SQL 不能用 concat()，金仓只有两参重载；请改用 ||\n%s", query)
	}
	if !strings.Contains(query, "||") {
		t.Fatalf("列拼接应使用 || 运算符\n%s", query)
	}
	// 域类型必须显式转 text，否则金仓匹配不到字符串算子
	for _, want := range []string{"cols.column_name::text", "cols.data_type::text"} {
		if !strings.Contains(query, want) {
			t.Fatalf("缺少显式类型转换 %q\n%s", want, query)
		}
	}
	if !strings.Contains(query, "table_schema='public'") {
		t.Fatalf("schema 参数未正确拼入\n%s", query)
	}
	// 表/列元信息字段保持齐全，避免修拼接时误删
	for _, want := range []string{"table_rows", "data_length_kb", "table_comment", "string_agg"} {
		if !strings.Contains(query, want) {
			t.Fatalf("SQL 缺少字段 %q\n%s", want, query)
		}
	}
}
