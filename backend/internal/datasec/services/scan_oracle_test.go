package services

import (
	"strings"
	"testing"
)

// 回归（2026-11-15）：敏感列自动发现此前只实现了 MySQL/PG 两族，
// 选择 Oracle 实例直接报「暂不支持表级选择 / 自动发现」。
// 新增 Oracle 支持后，本组用例锁定 SQL 形态与解析约定。

// 采样 SQL：12c+ FETCH FIRST 分页；schema/table/列名大写 + 双引号包裹；
// 列清单来自元数据（大写），因此 "COL" 原样包裹在 Oracle 里正确解析。
func TestOracleSampleQueryShape(t *testing.T) {
	q := oracleSampleQuery("HR", "EMPLOYEES", []string{"EMAIL", "PHONE_NUMBER"})
	if !strings.Contains(q, `SELECT "EMAIL", "PHONE_NUMBER" FROM "HR"."EMPLOYEES"`) {
		t.Fatalf("采样 SQL 形态错误：%s", q)
	}
	if !strings.Contains(q, "FETCH FIRST 500 ROWS ONLY") {
		t.Fatalf("应使用 FETCH FIRST 500 分页：%s", q)
	}
	if strings.Contains(strings.ToUpper(q), "LIMIT") {
		t.Fatalf("Oracle 不支持 LIMIT 语法：%s", q)
	}
}

// 列元数据 SQL：all_tab_columns 主表 + all_col_comments 注释 + owner/table 大写。
func TestOracleColumnsQueryShape(t *testing.T) {
	q := oracleColumnsQuery("HR", "EMPLOYEES")
	for _, want := range []string{
		"FROM all_tab_columns c",
		"LEFT JOIN all_col_comments cc",
		"cc.column_name = c.column_name",
		"WHERE c.owner = 'HR'",
		"c.table_name = 'EMPLOYEES'",
		"ORDER BY c.column_id",
		"AS column_name", "AS column_comment", "AS data_type",
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("列元数据 SQL 缺少 %q\n%s", want, q)
		}
	}
}

// go-ora 返回列名全大写；取值函数必须大小写无关（与 das getStringField 同款回归）。
func TestOracleRowStringCaseInsensitive(t *testing.T) {
	row := map[string]interface{}{
		"COLUMN_NAME":    "EMAIL",
		"Column_Comment": "邮箱", // 万一某驱动保留别名原样
	}
	if got := oracleRowString(row, "column_name"); got != "EMAIL" {
		t.Fatalf("大写 key 读取失败：%q", got)
	}
	if got := oracleRowString(row, "column_comment"); got != "邮箱" {
		t.Fatalf("混合大小写 key 读取失败：%q", got)
	}
	if got := oracleRowString(row, "not_exists"); got != "" {
		t.Fatalf("缺失 key 应返回空串：%q", got)
	}
	if got := oracleRowString(map[string]interface{}{"COLUMN_NAME": nil}, "column_name"); got != "" {
		t.Fatalf("nil 值应返回空串：%q", got)
	}
}

// 文本类型判定：VARCHAR2/CHAR/CLOB 参与（内容可采样），
// NUMBER/DATE/TIMESTAMP 等数值时间类不采样。
func TestOracleTypeIsText(t *testing.T) {
	for _, dt := range []string{"VARCHAR2", "varchar2", "CHAR", "NCHAR", "NVARCHAR2", "CLOB", "NCLOB", "LONG"} {
		if !oracleTypeIsText(dt) {
			t.Fatalf("%s 应判定为文本列", dt)
		}
	}
	for _, dt := range []string{"NUMBER", "DATE", "TIMESTAMP(6)", "RAW", "BLOB", "BINARY_DOUBLE", ""} {
		if oracleTypeIsText(dt) {
			t.Fatalf("%s 不应判定为文本列", dt)
		}
	}
}
