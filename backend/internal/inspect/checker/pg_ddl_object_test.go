package checker

// PostgreSQL DDL 对象级规则测试
//
// 覆盖本次新增的 7 个能力项（与 MySQL 侧一一对应）：
//   obj_col_must_exist / obj_idx_must_exist / obj_col_must_not_exist /
//   obj_idx_must_not_exist / obj_tbl_must_not_exist /
//   disable_create_table_as / disable_create_table_like /
//   merge_multiple_alter_stmt_same_table（另含已有的 disable_view_related_op 的闸门口径）
//
// 元信息由桩实现注入（pgMetaResolver），不依赖真实 PG 实例：
// 这与「连不上库就跳过、绝不猜测」的引擎约定一致，同时让断言完全确定。

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
)

// ---------- 桩实现 ----------

type stubPGMeta struct {
	tables  map[string]bool
	columns map[string]map[string]bool
	indexes map[string]bool
	// CountWhere 桩：countValue 为桩返回的行数，countErr 非空模拟连库失败，
	// countCalls 记录每次调用入参（断言下推是否正确）
	countValue int64
	countErr   error
	countCalls []countWhereCall
}

func newStubPGMeta() *stubPGMeta {
	return &stubPGMeta{tables: map[string]bool{}, columns: map[string]map[string]bool{}, indexes: map[string]bool{}}
}

func (s *stubPGMeta) withTable(name string, cols ...string) *stubPGMeta {
	key := normalizePGIdent(name)
	s.tables[key] = true
	if s.columns[key] == nil {
		s.columns[key] = map[string]bool{}
	}
	for _, c := range cols {
		s.columns[key][normalizePGIdent(c)] = true
	}
	return s
}

func (s *stubPGMeta) withIndex(name string) *stubPGMeta {
	s.indexes[normalizePGIdent(name)] = true
	return s
}

func (s *stubPGMeta) TableExists(table string) (bool, error) {
	return s.tables[normalizePGIdent(table)], nil
}

func (s *stubPGMeta) ColumnExists(table, column string) (bool, error) {
	return s.columns[normalizePGIdent(table)][normalizePGIdent(column)], nil
}

func (s *stubPGMeta) IndexExists(index string) (bool, error) {
	return s.indexes[normalizePGIdent(index)], nil
}

// CountWhere 影响行数桩：count 值由用例注入；err 非空时模拟连库失败（规则应静默跳过）。
// calls 记录调用入参，供断言「无 WHERE 不触发 / 表名与 WHERE 原文正确下推」。
type countWhereCall struct {
	table, schema, where string
}

func (s *stubPGMeta) CountWhere(table, schema, whereClause string) (int64, error) {
	s.countCalls = append(s.countCalls, countWhereCall{table, schema, whereClause})
	if s.countErr != nil {
		return 0, s.countErr
	}
	return s.countValue, nil
}

// runPGWithMeta 带元信息桩执行 PG 预审
func runPGWithMeta(t *testing.T, sql string, params config.InspectParams, meta pgMetaResolver) []ReturnData {
	t.Helper()
	svc := &SyntaxInspectService{
		DbType:        "PostgreSQL",
		SqlText:       sql,
		InspectParams: params,
		pgMeta:        meta,
	}
	data, err := svc.runPGInspect()
	if err != nil {
		t.Fatalf("PG 预审执行失败: %v", err)
	}
	return data
}

// findSummary 按 rule_key 找第一条审核项
func findSummary(data []ReturnData, ruleKey string) *controllers.SummaryItem {
	for _, row := range data {
		for i := range row.Summary {
			if row.Summary[i].RuleKey == ruleKey {
				return &row.Summary[i]
			}
		}
	}
	return nil
}

// ---------- 解析层单测 ----------

func TestParseAlterTableActions(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want pgAlterActions
		ok   bool
	}{
		{
			name: "DROP COLUMN",
			sql:  "ALTER TABLE public.t_demo DROP COLUMN remark;",
			want: pgAlterActions{Table: "t_demo", Schema: "public", DropColumns: []string{"remark"}},
			ok:   true,
		},
		{
			name: "DROP 省略 COLUMN 关键字",
			sql:  "ALTER TABLE t_demo DROP remark",
			want: pgAlterActions{Table: "t_demo", DropColumns: []string{"remark"}},
			ok:   true,
		},
		{
			name: "DROP COLUMN IF EXISTS 不收集",
			sql:  "ALTER TABLE t_demo DROP COLUMN IF EXISTS remark CASCADE",
			want: pgAlterActions{Table: "t_demo"},
			ok:   true,
		},
		{
			name: "ALTER COLUMN TYPE",
			sql:  "ALTER TABLE t_demo ALTER COLUMN status TYPE bigint USING status::bigint",
			want: pgAlterActions{Table: "t_demo", AlterColumns: []string{"status"}},
			ok:   true,
		},
		{
			name: "ALTER 省略 COLUMN 关键字且 SET NOT NULL",
			sql:  "ALTER TABLE t_demo ALTER name SET NOT NULL",
			want: pgAlterActions{Table: "t_demo", AlterColumns: []string{"name"}},
			ok:   true,
		},
		{
			name: "RENAME COLUMN",
			sql:  "ALTER TABLE t_demo RENAME COLUMN old_name TO new_name",
			want: pgAlterActions{Table: "t_demo", RenameColumns: []pgRenamePair{{Old: "old_name", New: "new_name"}}},
			ok:   true,
		},
		{
			name: "RENAME TO 表",
			sql:  "ALTER TABLE public.t_demo RENAME TO t_demo_new",
			want: pgAlterActions{Table: "t_demo", Schema: "public", RenameTable: "t_demo_new"},
			ok:   true,
		},
		{
			name: "ALTER TABLE IF EXISTS + ONLY + 多子句",
			sql:  "ALTER TABLE IF EXISTS ONLY public.t_demo DROP COLUMN a, ALTER COLUMN b SET DEFAULT 1",
			want: pgAlterActions{Table: "t_demo", Schema: "public", IfExists: true, DropColumns: []string{"a"}, AlterColumns: []string{"b"}},
			ok:   true,
		},
		{
			name: "双引号包裹的 schema.table",
			sql:  `ALTER TABLE "audit"."T_Demo" DROP COLUMN "Remark"`,
			want: pgAlterActions{Table: "T_Demo", Schema: "audit", DropColumns: []string{"Remark"}},
			ok:   true,
		},
		{
			name: "DROP CONSTRAINT 不当作列",
			sql:  "ALTER TABLE t_demo DROP CONSTRAINT uk_t_demo_name",
			want: pgAlterActions{Table: "t_demo"},
			ok:   true,
		},
		{
			name: "ALTER CONSTRAINT 不当作列",
			sql:  "ALTER TABLE t_demo ALTER CONSTRAINT fk_demo DEFERRABLE",
			want: pgAlterActions{Table: "t_demo"},
			ok:   true,
		},
		{
			name: "非 ALTER TABLE 语句",
			sql:  "CREATE INDEX idx_x ON t_demo(a)",
			ok:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseAlterTableActions(c.sql)
			if ok != c.ok {
				t.Fatalf("ok=%v want %v", ok, c.ok)
			}
			if !ok {
				return
			}
			if got.Table != c.want.Table {
				t.Errorf("table=%q want %q", got.Table, c.want.Table)
			}
			if got.Schema != c.want.Schema {
				t.Errorf("schema=%q want %q", got.Schema, c.want.Schema)
			}
			if got.IfExists != c.want.IfExists {
				t.Errorf("ifExists=%v want %v", got.IfExists, c.want.IfExists)
			}
			if strings.Join(got.DropColumns, ",") != strings.Join(c.want.DropColumns, ",") {
				t.Errorf("dropCols=%v want %v", got.DropColumns, c.want.DropColumns)
			}
			if strings.Join(got.AlterColumns, ",") != strings.Join(c.want.AlterColumns, ",") {
				t.Errorf("alterCols=%v want %v", got.AlterColumns, c.want.AlterColumns)
			}
			if len(got.RenameColumns) != len(c.want.RenameColumns) {
				t.Fatalf("renameCols=%v want %v", got.RenameColumns, c.want.RenameColumns)
			}
			for i := range got.RenameColumns {
				if got.RenameColumns[i] != c.want.RenameColumns[i] {
					t.Errorf("renameCols[%d]=%v want %v", i, got.RenameColumns[i], c.want.RenameColumns[i])
				}
			}
			if got.RenameTable != c.want.RenameTable {
				t.Errorf("renameTable=%q want %q", got.RenameTable, c.want.RenameTable)
			}
		})
	}
}

func TestParseCreateTableAsAndLike(t *testing.T) {
	ctas := []struct {
		sql  string
		want string
		ok   bool
	}{
		{"CREATE TABLE t_new AS SELECT * FROM t_src", "t_new", true},
		{"CREATE TABLE IF NOT EXISTS public.t_new AS SELECT 1", "t_new", true},
		{"CREATE UNLOGGED TABLE t_new AS SELECT 1", "t_new", true},
		{"CREATE TABLE t_new (a, b) AS SELECT 1, 2", "t_new", true},
		{"CREATE TABLE t_new (id bigint PRIMARY KEY)", "", false},
		{"CREATE TABLE t_new OF my_type", "", false},
		{"CREATE TABLE t_new PARTITION OF t_src FOR VALUES FROM (1) TO (10)", "", false},
		{"CREATE INDEX idx_a ON t_new(a)", "", false},
	}
	for _, c := range ctas {
		got, ok := parseCreateTableAs(c.sql)
		if ok != c.ok || got != c.want {
			t.Errorf("parseCreateTableAs(%q)=(%q,%v) want (%q,%v)", c.sql, got, ok, c.want, c.ok)
		}
	}

	like := []struct {
		sql    string
		table  string
		source string
		ok     bool
	}{
		{"CREATE TABLE t_copy (LIKE t_src INCLUDING ALL)", "t_copy", "t_src", true},
		{"CREATE TABLE IF NOT EXISTS t_copy (LIKE public.t_src)", "t_copy", "t_src", true},
		{"CREATE TABLE t_copy (id bigint)", "", "", false},
	}
	for _, c := range like {
		table, source, ok := parseCreateTableLike(c.sql)
		if ok != c.ok || table != c.table || source != c.source {
			t.Errorf("parseCreateTableLike(%q)=(%q,%q,%v) want (%q,%q,%v)", c.sql, table, source, ok, c.table, c.source, c.ok)
		}
	}
}

func TestParseIndexObjectOp(t *testing.T) {
	cases := []struct {
		sql      string
		op       string
		names    []string
		schema   string
		ifExists bool
		renameTo string
		ok       bool
	}{
		{"DROP INDEX idx_t_demo_status", "DROP INDEX", []string{"idx_t_demo_status"}, "", false, "", true},
		{"DROP INDEX CONCURRENTLY IF EXISTS idx_a, public.idx_b", "DROP INDEX", []string{"idx_a", "idx_b"}, "public", true, "", true},
		{"ALTER INDEX public.idx_old RENAME TO idx_new", "ALTER INDEX", []string{"idx_old"}, "public", false, "idx_new", true},
		{"ALTER INDEX IF EXISTS idx_old SET (fillfactor = 70)", "ALTER INDEX", []string{"idx_old"}, "", true, "", true},
		{"CREATE INDEX idx_a ON t(a)", "", nil, "", false, "", false},
	}
	for _, c := range cases {
		op, ok := parseIndexObjectOp(c.sql)
		if ok != c.ok {
			t.Errorf("parseIndexObjectOp(%q) ok=%v want %v", c.sql, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if op.Op != c.op || op.IfExists != c.ifExists || op.RenameTo != c.renameTo || op.Schema != c.schema ||
			strings.Join(op.Names, ",") != strings.Join(c.names, ",") {
			t.Errorf("parseIndexObjectOp(%q)=%+v want op=%q names=%v schema=%q ifExists=%v renameTo=%q",
				c.sql, op, c.op, c.names, c.schema, c.ifExists, c.renameTo)
		}
	}
}

// TestPGMetaResolverSchemaSelection 语句显式限定 schema 时，元信息探针必须切到该 schema；
// 未限定时回退到工单 InspectSchema（否则「别的模式里的同名表」会被误报成不存在）。
func TestPGMetaResolverSchemaSelection(t *testing.T) {
	ctx := &pgInspectContext{schema: "public"}

	t.Run("语句限定 schema 时优先用语句的", func(t *testing.T) {
		resolver, ok := ctx.metaResolverFor("audit").(pgDBResolver)
		if !ok {
			t.Fatalf("期望 pgDBResolver，实际 %T", ctx.metaResolverFor("audit"))
		}
		if resolver.schema != "audit" {
			t.Errorf("schema=%q want %q", resolver.schema, "audit")
		}
	})

	t.Run("语句未限定时回退到工单 schema", func(t *testing.T) {
		resolver, ok := ctx.metaResolverFor("").(pgDBResolver)
		if !ok {
			t.Fatalf("期望 pgDBResolver，实际 %T", ctx.metaResolverFor(""))
		}
		if resolver.schema != "" {
			t.Errorf("schema=%q want 空串（由 ctx.schema 兜底）", resolver.schema)
		}
	})

	t.Run("注入桩实现时忽略 schema", func(t *testing.T) {
		stub := newStubPGMeta().withTable("t_demo")
		withStub := &pgInspectContext{schema: "public", meta: stub}
		if _, ok := withStub.metaResolverFor("audit").(*stubPGMeta); !ok {
			t.Fatalf("期望桩实现，实际 %T", withStub.metaResolverFor("audit"))
		}
	})
}

func TestParseViewStatement(t *testing.T) {
	cases := []struct {
		sql  string
		op   string
		view string
		ok   bool
	}{
		{"CREATE VIEW v_x AS SELECT 1", "CREATE VIEW", "v_x", true},
		{"CREATE OR REPLACE VIEW public.v_x AS SELECT 1", "CREATE OR REPLACE VIEW", "v_x", true},
		{"CREATE MATERIALIZED VIEW v_x AS SELECT 1", "CREATE MATERIALIZED VIEW", "v_x", true},
		{"DROP VIEW IF EXISTS v_x", "DROP VIEW", "v_x", true},
		{"REFRESH MATERIALIZED VIEW v_x", "REFRESH MATERIALIZED VIEW", "v_x", true},
		{"CREATE TABLE t_x (id bigint)", "", "", false},
	}
	for _, c := range cases {
		op, view, ok := parseViewStatement(c.sql)
		if ok != c.ok || op != c.op || view != c.view {
			t.Errorf("parseViewStatement(%q)=(%q,%q,%v) want (%q,%q,%v)", c.sql, op, view, ok, c.op, c.view, c.ok)
		}
	}
}

// ---------- 规则层单测（注入元信息桩） ----------

func TestPGAlterTableObjectRules(t *testing.T) {
	meta := newStubPGMeta().
		withTable("t_demo", "id", "name").
		withTable("t_exist")

	t.Run("DROP 不存在的列命中 obj_col_must_exist", func(t *testing.T) {
		data := runPGWithMeta(t, "ALTER TABLE t_demo DROP COLUMN ghost", config.DefaultInspectParams(), meta)
		item := findSummary(data, "obj_col_must_exist")
		if item == nil {
			t.Fatalf("期望命中 obj_col_must_exist，实际: %v", collectRuleKeys(data))
		}
		if item.Category != controllers.CategoryObjectMissing {
			t.Errorf("category=%q want %q", item.Category, controllers.CategoryObjectMissing)
		}
		if !strings.Contains(item.Message, "ghost") {
			t.Errorf("message 未包含列名: %q", item.Message)
		}
	})

	t.Run("DROP IF EXISTS 不存在的列不告警", func(t *testing.T) {
		data := runPGWithMeta(t, "ALTER TABLE t_demo DROP COLUMN IF EXISTS ghost", config.DefaultInspectParams(), meta)
		if keys := collectRuleKeys(data); hasRuleKey(keys, "obj_col_must_exist") {
			t.Fatalf("IF EXISTS 不应告警，实际: %v", keys)
		}
	})

	t.Run("ALTER COLUMN 不存在的列命中 obj_col_must_exist", func(t *testing.T) {
		data := runPGWithMeta(t, "ALTER TABLE t_demo ALTER COLUMN ghost TYPE bigint", config.DefaultInspectParams(), meta)
		if findSummary(data, "obj_col_must_exist") == nil {
			t.Fatalf("期望命中 obj_col_must_exist，实际: %v", collectRuleKeys(data))
		}
	})

	t.Run("表不存在只报表级结论，不衍生列级噪音", func(t *testing.T) {
		data := runPGWithMeta(t, "ALTER TABLE t_missing DROP COLUMN a, ALTER COLUMN b SET NOT NULL", config.DefaultInspectParams(), meta)
		keys := collectRuleKeys(data)
		if !hasRuleKey(keys, "obj_tbl_must_exist") {
			t.Fatalf("期望命中 obj_tbl_must_exist，实际: %v", keys)
		}
		if hasRuleKey(keys, "obj_col_must_exist") {
			t.Fatalf("表不存在时不应再报列缺失，实际: %v", keys)
		}
	})

	t.Run("ALTER TABLE IF EXISTS 不存在的表不告警", func(t *testing.T) {
		data := runPGWithMeta(t, "ALTER TABLE IF EXISTS t_missing DROP COLUMN a", config.DefaultInspectParams(), meta)
		if keys := collectRuleKeys(data); hasRuleKey(keys, "obj_tbl_must_exist") {
			t.Fatalf("IF EXISTS 不应告警，实际: %v", keys)
		}
	})

	t.Run("改名到已存在的列命中 obj_col_must_not_exist", func(t *testing.T) {
		data := runPGWithMeta(t, "ALTER TABLE t_demo RENAME COLUMN name TO id", config.DefaultInspectParams(), meta)
		item := findSummary(data, "obj_col_must_not_exist")
		if item == nil {
			t.Fatalf("期望命中 obj_col_must_not_exist，实际: %v", collectRuleKeys(data))
		}
		if item.Category != controllers.CategoryObjectConflict {
			t.Errorf("category=%q want %q", item.Category, controllers.CategoryObjectConflict)
		}
	})

	t.Run("改名到不冲突的新列名不告警", func(t *testing.T) {
		data := runPGWithMeta(t, "ALTER TABLE t_demo RENAME COLUMN name TO display_name", config.DefaultInspectParams(), meta)
		if keys := collectRuleKeys(data); hasRuleKey(keys, "obj_col_must_not_exist") {
			t.Fatalf("不应告警，实际: %v", keys)
		}
	})

	t.Run("改名表到已存在的表命中 obj_tbl_must_not_exist", func(t *testing.T) {
		data := runPGWithMeta(t, "ALTER TABLE t_demo RENAME TO t_exist", config.DefaultInspectParams(), meta)
		if findSummary(data, "obj_tbl_must_not_exist") == nil {
			t.Fatalf("期望命中 obj_tbl_must_not_exist，实际: %v", collectRuleKeys(data))
		}
	})
}

func TestPGIndexObjectRules(t *testing.T) {
	meta := newStubPGMeta().withIndex("idx_old").withIndex("idx_taken")

	t.Run("DROP 不存在的索引命中 obj_idx_must_exist", func(t *testing.T) {
		data := runPGWithMeta(t, "DROP INDEX idx_ghost", config.DefaultInspectParams(), meta)
		if findSummary(data, "obj_idx_must_exist") == nil {
			t.Fatalf("期望命中 obj_idx_must_exist，实际: %v", collectRuleKeys(data))
		}
	})

	t.Run("DROP INDEX IF EXISTS 不告警", func(t *testing.T) {
		data := runPGWithMeta(t, "DROP INDEX IF EXISTS idx_ghost", config.DefaultInspectParams(), meta)
		if keys := collectRuleKeys(data); hasRuleKey(keys, "obj_idx_must_exist") {
			t.Fatalf("IF EXISTS 不应告警，实际: %v", keys)
		}
	})

	t.Run("ALTER INDEX RENAME 到已存在索引名命中 obj_idx_must_not_exist", func(t *testing.T) {
		data := runPGWithMeta(t, "ALTER INDEX idx_old RENAME TO idx_taken", config.DefaultInspectParams(), meta)
		if findSummary(data, "obj_idx_must_not_exist") == nil {
			t.Fatalf("期望命中 obj_idx_must_not_exist，实际: %v", collectRuleKeys(data))
		}
	})

	t.Run("ALTER INDEX RENAME 到新名字同时校验旧索引存在", func(t *testing.T) {
		data := runPGWithMeta(t, "ALTER INDEX idx_ghost RENAME TO idx_new", config.DefaultInspectParams(), meta)
		if findSummary(data, "obj_idx_must_exist") == nil {
			t.Fatalf("期望命中 obj_idx_must_exist，实际: %v", collectRuleKeys(data))
		}
	})
}

func TestPGGateRules(t *testing.T) {
	meta := newStubPGMeta()

	t.Run("CTAS 默认禁止", func(t *testing.T) {
		data := runPGWithMeta(t, "CREATE TABLE t_new AS SELECT 1 AS id", config.DefaultInspectParams(), meta)
		item := findSummary(data, "disable_create_table_as")
		if item == nil {
			t.Fatalf("期望命中 disable_create_table_as，实际: %v", collectRuleKeys(data))
		}
		if item.Category != controllers.CategoryForbiddenStatement {
			t.Errorf("category=%q want %q", item.Category, controllers.CategoryForbiddenStatement)
		}
	})

	t.Run("CTAS 规则关闭后放行", func(t *testing.T) {
		p := config.DefaultInspectParams()
		p.ENABLE_CREATE_TABLE_AS = true
		data := runPGWithMeta(t, "CREATE TABLE t_new AS SELECT 1 AS id", p, meta)
		if keys := collectRuleKeys(data); hasRuleKey(keys, "disable_create_table_as") {
			t.Fatalf("规则关闭后不应命中，实际: %v", keys)
		}
	})

	t.Run("CTL（括号内 LIKE）默认禁止", func(t *testing.T) {
		data := runPGWithMeta(t, "CREATE TABLE t_copy (LIKE t_src INCLUDING ALL)", config.DefaultInspectParams(), meta)
		if findSummary(data, "disable_create_table_like") == nil {
			t.Fatalf("期望命中 disable_create_table_like，实际: %v", collectRuleKeys(data))
		}
	})

	t.Run("CTL 规则关闭后放行", func(t *testing.T) {
		p := config.DefaultInspectParams()
		p.ENABLE_CREATE_TABLE_LIKE = true
		data := runPGWithMeta(t, "CREATE TABLE t_copy (LIKE t_src)", p, meta)
		if keys := collectRuleKeys(data); hasRuleKey(keys, "disable_create_table_like") {
			t.Fatalf("规则关闭后不应命中，实际: %v", keys)
		}
	})

	t.Run("视图规则开启时 CREATE VIEW 命中语句级结论", func(t *testing.T) {
		p := config.DefaultInspectParams()
		p.ENABLE_CREATE_VIEW = false
		data := runPGWithMeta(t, "CREATE VIEW v_x AS SELECT 1", p, meta)
		item := findSummary(data, "disable_view_related_op")
		if item == nil {
			t.Fatalf("期望命中 disable_view_related_op，实际: %v", collectRuleKeys(data))
		}
		if item.Category != controllers.CategoryForbiddenStatement {
			t.Errorf("category=%q want %q", item.Category, controllers.CategoryForbiddenStatement)
		}
	})

	t.Run("视图允许时放行", func(t *testing.T) {
		p := config.DefaultInspectParams()
		p.ENABLE_CREATE_VIEW = true
		data := runPGWithMeta(t, "CREATE VIEW v_x AS SELECT 1", p, meta)
		if keys := collectRuleKeys(data); hasRuleKey(keys, "disable_view_related_op") {
			t.Fatalf("允许视图时不应命中，实际: %v", keys)
		}
	})

	t.Run("物化视图同样受视图规则约束", func(t *testing.T) {
		p := config.DefaultInspectParams()
		p.ENABLE_CREATE_VIEW = false
		data := runPGWithMeta(t, "CREATE MATERIALIZED VIEW mv_x AS SELECT 1", p, meta)
		if findSummary(data, "disable_view_related_op") == nil {
			t.Fatalf("期望命中 disable_view_related_op，实际: %v", collectRuleKeys(data))
		}
	})
}

func TestPGMergeAlterStatements(t *testing.T) {
	meta := newStubPGMeta().withTable("t_demo", "id", "name")
	sql := "ALTER TABLE t_demo ADD COLUMN a bigint;\nALTER TABLE t_demo ADD COLUMN b bigint;"

	t.Run("规则开启时提示合并", func(t *testing.T) {
		p := config.DefaultInspectParams()
		p.ENABLE_MERGE_ALTER_TABLE = true
		data := runPGWithMeta(t, sql, p, meta)
		item := findSummary(data, "merge_multiple_alter_stmt_same_table")
		if item == nil {
			t.Fatalf("期望命中 merge_multiple_alter_stmt_same_table，实际: %v", collectRuleKeys(data))
		}
		if !strings.Contains(item.Message, "PostgreSQL") || !strings.Contains(item.Message, "t_demo") {
			t.Errorf("message 内容不符合预期: %q", item.Message)
		}
	})

	t.Run("规则关闭时不提示", func(t *testing.T) {
		p := config.DefaultInspectParams()
		p.ENABLE_MERGE_ALTER_TABLE = false
		data := runPGWithMeta(t, sql, p, meta)
		if keys := collectRuleKeys(data); hasRuleKey(keys, "merge_multiple_alter_stmt_same_table") {
			t.Fatalf("规则关闭后不应命中，实际: %v", keys)
		}
	})

	t.Run("不同表的多条 ALTER 不提示", func(t *testing.T) {
		p := config.DefaultInspectParams()
		p.ENABLE_MERGE_ALTER_TABLE = true
		data := runPGWithMeta(t, "ALTER TABLE t_demo ADD COLUMN a bigint;\nALTER TABLE t_other ADD COLUMN b bigint;", p, meta)
		if keys := collectRuleKeys(data); hasRuleKey(keys, "merge_multiple_alter_stmt_same_table") {
			t.Fatalf("不同表不应命中，实际: %v", keys)
		}
	})
}
