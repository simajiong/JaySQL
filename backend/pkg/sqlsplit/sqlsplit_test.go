package sqlsplit

import (
	"strings"
	"testing"
)

// 用户实际报错的 PG 建表工单（含 IDENTITY 主键、COMMENT ON、普通索引）
const pgDemoSQL = `-- 创建业务表，单主键标准模板
CREATE TABLE public.t_demo (
    id bigint GENERATED ALWAYS AS IDENTITY,
    name varchar(128) NOT NULL,
    status smallint NOT NULL DEFAULT 1,
    remark text,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz,
    CONSTRAINT pk_t_demo PRIMARY KEY (id)
);
-- 表注释
COMMENT ON TABLE public.t_demo IS '演示业务表';
-- 字段注释
COMMENT ON COLUMN public.t_demo.id IS '主键ID';
COMMENT ON COLUMN public.t_demo.name IS '名称';
COMMENT ON COLUMN public.t_demo.status IS '状态：0禁用，1正常，2删除';
COMMENT ON COLUMN public.t_demo.remark IS '备注信息';
COMMENT ON COLUMN public.t_demo.created_at IS '创建时间';
COMMENT ON COLUMN public.t_demo.updated_at IS '更新时间';
-- 业务普通索引（按需添加）
CREATE INDEX idx_t_demo_status ON public.t_demo(status);`

func TestSplitPGDemoSQL(t *testing.T) {
	parts := Split(pgDemoSQL)
	// CREATE TABLE + 表注释 + 6条字段注释 + CREATE INDEX = 9
	if len(parts) != 9 {
		t.Fatalf("期望拆出 9 条语句，实际 %d 条", len(parts))
	}
	for i, p := range parts {
		if strings.Contains(p, "COMMENT ON TABLE") && i != 1 {
			// 表注释应是第二条语句
			t.Errorf("表注释语句位置不对：i=%d", i)
		}
	}
	if !strings.Contains(parts[0], "GENERATED ALWAYS AS IDENTITY") {
		t.Errorf("第一条语句应为 CREATE TABLE，实际：%s", head(parts[0]))
	}
	if !strings.HasPrefix(NormalizeLeading(parts[8]), "create index") {
		t.Errorf("最后一条应为 CREATE INDEX，实际：%s", head(parts[8]))
	}
}

// NormalizeLeading 必须折叠空白：多空格/Tab/换行写法与单空格等价。
// 回归用例来自线上工单 `create  table  t_01(id int)`——双空格导致 PG 引擎
// 语句分发落空、整条 DDL 零结论（规范开了也永远不报）。
func TestNormalizeLeadingCollapsesWhitespace(t *testing.T) {
	cases := []string{
		"create  table  t_01(id int)",
		"create\ttable\tt_01(id int)",
		"create\n  table\n  t_01(id int)",
		"  CREATE   TABLE   t_01(id int)  ",
	}
	for _, sql := range cases {
		if got := NormalizeLeading(sql); !strings.HasPrefix(got, "create table t_01") {
			t.Errorf("NormalizeLeading(%q) = %q，期望以 \"create table t_01\" 开头", sql, got)
		}
	}
	// 前导注释 + 多个空白同样要归一
	if got := NormalizeLeading("-- 说明\n  create   table  t2(a int)"); !strings.HasPrefix(got, "create table t2") {
		t.Errorf("带注释的多空白语句归一失败：%q", got)
	}
}

func TestSplitCommentSemicolon(t *testing.T) {
	// 注释里出现分号不能被误拆
	sql := "-- 注释里有分号; 测试\nSELECT 1;\n/* 块注释; 也有分号 */UPDATE t SET a=1;"
	parts := Split(sql)
	if len(parts) != 2 {
		t.Fatalf("期望 2 条语句，实际 %d 条：%v", len(parts), parts)
	}
}

func TestSplitDollarQuoted(t *testing.T) {
	sql := "CREATE FUNCTION f() RETURNS void AS $$ BEGIN RAISE NOTICE 'hi; there'; END $$ LANGUAGE plpgsql;\nDO $$ BEGIN END $$;"
	parts := Split(sql)
	if len(parts) != 2 {
		t.Fatalf("期望 2 条语句，实际 %d 条", len(parts))
	}
}

func TestStatementType(t *testing.T) {
	cases := map[string]string{
		"CREATE TABLE a (id int);":                     "DDL",
		"COMMENT ON TABLE a IS 'x';":                   "DDL",
		"CREATE INDEX idx ON a(col);":                  "DDL",
		"DO $$ BEGIN END $$;":                          "DDL",
		"INSERT INTO a VALUES (1);":                    "DML",
		"UPDATE a SET b=1;":                            "DML",
		"SELECT * FROM a;":                             "EXPORT",
		"-- 注释\nWITH c AS (SELECT 1) SELECT * FROM c;": "EXPORT",
		"GRANT SELECT ON a TO b;":                      "DDL",
	}
	for sql, want := range cases {
		if got := StatementType(sql); got != want {
			t.Errorf("StatementType(%q) = %q, want %q", head(sql), got, want)
		}
	}
}

func TestSplitTypedMixed(t *testing.T) {
	_, types, err := SplitTyped("CREATE TABLE a (id int); SELECT 1;")
	if err != nil {
		t.Fatalf("两条语句类型均可识别，不应报错: %v", err)
	}
	if len(types) != 2 || types[0] != "DDL" || types[1] != "EXPORT" {
		t.Fatalf("类型识别错误: %v", types)
	}
}

func TestSplitTypedPGDemo(t *testing.T) {
	_, types, err := SplitTyped(pgDemoSQL)
	if err != nil {
		t.Fatalf("PG 示例工单应全部可识别类型: %v", err)
	}
	for _, ty := range types {
		if ty != "DDL" {
			t.Fatalf("示例工单应全为 DDL，出现 %s", ty)
		}
	}
}

func head(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 50 {
		return s[:50]
	}
	return s
}
