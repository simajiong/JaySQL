package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/config"
)

// ---------------------------------------------------------------------------
// parseTestDecodingLine：样本格式取自 PostgreSQL 源码回归期望输出
// contrib/test_decoding/expected/*.out（REL_15_STABLE 实测校准）。
// ---------------------------------------------------------------------------

func TestParseTestDecodingLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want *decodedChange
	}{
		{
			name: "INSERT 含数值/字符串/NULL",
			line: "table public.replication_example: INSERT: id[integer]:6 somedata[integer]:2 text[character varying]:'4' bar[integer]:null",
			want: &decodedChange{
				Op: "INSERT", Schema: "public", Table: "replication_example",
				Columns: []decodedColumn{
					{Name: "id", Type: "integer", Value: "6"},
					{Name: "somedata", Type: "integer", Value: "2"},
					{Name: "text", Type: "character varying", Value: "'4'"},
					{Name: "bar", Type: "integer", Value: "null", IsNull: true},
				},
			},
		},
		{
			name: "UPDATE old-key/new-tuple（源码标准形态：分隔为 ' : new-tuple:'）",
			line: "table public.table_with_pkey: UPDATE: old-key: id[integer]:2 : new-tuple: id[integer]:-2 data[integer]:3",
			want: &decodedChange{
				Op: "UPDATE", Schema: "public", Table: "table_with_pkey",
				OldKey: []decodedColumn{
					{Name: "id", Type: "integer", Value: "2"},
				},
				Columns: []decodedColumn{
					{Name: "id", Type: "integer", Value: "-2"},
					{Name: "data", Type: "integer", Value: "3"},
				},
			},
		},
		{
			name: "UPDATE FULL 整行旧值",
			line: "table public.table_without_key: UPDATE: old-key: id[integer]:2 data[integer]:3 new-tuple: id[integer]:-2 data[integer]:3 new_column[text]:null",
			want: &decodedChange{
				Op: "UPDATE", Schema: "public", Table: "table_without_key",
				OldKey: []decodedColumn{
					{Name: "id", Type: "integer", Value: "2"},
					{Name: "data", Type: "integer", Value: "3"},
				},
				Columns: []decodedColumn{
					{Name: "id", Type: "integer", Value: "-2"},
					{Name: "data", Type: "integer", Value: "3"},
					{Name: "new_column", Type: "text", Value: "null", IsNull: true},
				},
			},
		},
		{
			name: "字符串值含空格与转义单引号",
			line: `table public.t_order: UPDATE: old-key: id[bigint]:1001 remark[text]:'O''Reilly 渠道' : new-tuple: id[bigint]:1001 remark[text]:'changed'`,
			want: &decodedChange{
				Op: "UPDATE", Schema: "public", Table: "t_order",
				OldKey: []decodedColumn{
					{Name: "id", Type: "bigint", Value: "1001"},
					{Name: "remark", Type: "text", Value: `'O''Reilly 渠道'`},
				},
				Columns: []decodedColumn{
					{Name: "id", Type: "bigint", Value: "1001"},
					{Name: "remark", Type: "text", Value: "'changed'"},
				},
			},
		},
		{
			name: "DELETE 仅主键（REPLICA IDENTITY DEFAULT）",
			line: "table public.table_with_pkey: DELETE: id[integer]:1",
			want: &decodedChange{
				Op: "DELETE", Schema: "public", Table: "table_with_pkey",
				OldKey: []decodedColumn{{Name: "id", Type: "integer", Value: "1"}},
			},
		},
		{
			name: "DELETE 整行（REPLICA IDENTITY FULL）",
			line: "table public.table_without_key: DELETE: id[integer]:2 data[integer]:3 new_column[text]:'someval'",
			want: &decodedChange{
				Op: "DELETE", Schema: "public", Table: "table_without_key",
				OldKey: []decodedColumn{
					{Name: "id", Type: "integer", Value: "2"},
					{Name: "data", Type: "integer", Value: "3"},
					{Name: "new_column", Type: "text", Value: "'someval'"},
				},
			},
		},
		{
			name: "DELETE 无旧行",
			line: "table public.table_without_key: DELETE: (no-tuple-data)",
			want: &decodedChange{
				Op: "DELETE", Schema: "public", Table: "table_without_key",
			},
		},
		{
			name: "含负数与时间戳字符串",
			line: `table public.t_order: UPDATE: old-key: id[bigint]:2001 status[character varying]:'paid' : new-tuple: id[bigint]:2001 status[character varying]:'shipped' updated_at[timestamp with time zone]:'2026-11-12 10:05:00+08'`,
			want: &decodedChange{
				Op: "UPDATE", Schema: "public", Table: "t_order",
				OldKey: []decodedColumn{
					{Name: "id", Type: "bigint", Value: "2001"},
					{Name: "status", Type: "character varying", Value: "'paid'"},
				},
				Columns: []decodedColumn{
					{Name: "id", Type: "bigint", Value: "2001"},
					{Name: "status", Type: "character varying", Value: "'shipped'"},
					{Name: "updated_at", Type: "timestamp with time zone", Value: `'2026-11-12 10:05:00+08'`},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseTestDecodingLine(tc.line)
			if !ok {
				t.Fatalf("应识别为行事件，实际未识别：%s", tc.line)
			}
			if got.Op != tc.want.Op || got.Schema != tc.want.Schema || got.Table != tc.want.Table {
				t.Fatalf("基础字段不符：got op=%s schema=%s table=%s", got.Op, got.Schema, got.Table)
			}
			if !colsEqual(got.Columns, tc.want.Columns) {
				t.Fatalf("新元组不符：\ngot  %#v\nwant %#v", got.Columns, tc.want.Columns)
			}
			if !colsEqual(got.OldKey, tc.want.OldKey) {
				t.Fatalf("旧元组不符：\ngot  %#v\nwant %#v", got.OldKey, tc.want.OldKey)
			}
		})
	}
}

func TestParseTestDecodingNonRowLines(t *testing.T) {
	for _, line := range []string{
		"BEGIN 687",
		"COMMIT 687 (at 2026-11-12 10:00:00.123+08)",
		"message: transactional: 1 prefix: hi, sz: 6 content:hello",
		"opening a streamed block for transaction 687",
		"",
	} {
		if got, ok := parseTestDecodingLine(line); ok {
			t.Fatalf("非行事件不应被识别：%q → %#v", line, got)
		}
	}
}

func colsEqual(a, b []decodedColumn) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// buildRollbackStatements
// ---------------------------------------------------------------------------

func fakeMeta(metas map[string]tableMeta) tableMetaFunc {
	return func(_ context.Context, schema, table string) (tableMeta, error) {
		if m, ok := metas[schema+"."+table]; ok {
			return m, nil
		}
		return tableMeta{RelReplIdent: "d", PKColumns: []string{"id"}}, nil
	}
}

func TestBuildRollbackStatements(t *testing.T) {
	ctx := context.Background()
	metas := map[string]tableMeta{
		"public.t_order": {RelReplIdent: "f", PKColumns: []string{"id"}},
		"public.t_dict":  {RelReplIdent: "d", PKColumns: []string{"id"}},
	}

	insert, _ := parseTestDecodingLine(
		"table public.t_order: INSERT: id[bigint]:1001 status[character varying]:'paid' amount[numeric]:'199.00'")
	update, _ := parseTestDecodingLine(
		`table public.t_order: UPDATE: old-key: id[bigint]:1001 status[character varying]:'paid' amount[numeric]:'199.00' : new-tuple: id[bigint]:1001 status[character varying]:'refunded' amount[numeric]:'0.00'`)
	del, _ := parseTestDecodingLine(
		"table public.t_order: DELETE: id[bigint]:1002 status[character varying]:'completed' amount[numeric]:'120.00'")
	delDefault, _ := parseTestDecodingLine(
		"table public.t_dict: DELETE: id[integer]:20")
	updateDefault, _ := parseTestDecodingLine(
		`table public.t_dict: UPDATE: old-key: id[integer]:20 : new-tuple: id[integer]:20 label[character varying]:'新标签'`)

	stmts, skips := buildRollbackStatements(ctx, fakeMeta(metas),
		[]decodedChange{*insert, *update, *del, *delDefault, *updateDefault})

	if len(skips) != 2 {
		t.Fatalf("REPLICA IDENTITY DEFAULT 的 UPDATE/DELETE 各应跳过 1 条，实际 skips=%v", skips)
	}
	for _, s := range skips {
		if !strings.Contains(s, "REPLICA IDENTITY") {
			t.Fatalf("跳过原因应说明 REPLICA IDENTITY：%s", s)
		}
	}
	if len(stmts) != 3 {
		t.Fatalf("应生成 3 条语句（UPDATE/DELETE重建/INSERT删除），实际 %d 条：%v", len(stmts), stmts)
	}

	// 顺序必须倒序：最后发生的 DELETE 重建最先回放
	if !strings.HasPrefix(stmts[0], `INSERT INTO "public"."t_order"`) {
		t.Fatalf("第 1 条应为 DELETE 的逆向重建 INSERT：%s", stmts[0])
	}
	if !strings.Contains(stmts[0], "ON CONFLICT (\"id\") DO NOTHING") {
		t.Fatalf("重建 INSERT 应带幂等守卫：%s", stmts[0])
	}
	if !strings.Contains(stmts[0], `'completed'`) || !strings.Contains(stmts[0], "'120.00'") {
		t.Fatalf("重建 INSERT 应含整行旧值：%s", stmts[0])
	}

	// UPDATE 逆向：SET 旧值 + 乐观锁守卫
	var upd string
	for _, s := range stmts {
		if strings.HasPrefix(s, "UPDATE") {
			upd = s
		}
	}
	if upd == "" {
		t.Fatalf("应生成逆向 UPDATE")
	}
	if !strings.Contains(upd, `"status" = 'paid'`) || !strings.Contains(upd, `"amount" = '199.00'`) {
		t.Fatalf("逆向 UPDATE 应 SET 旧值：%s", upd)
	}
	if !strings.Contains(upd, `"status" IS NOT DISTINCT FROM 'refunded'`) {
		t.Fatalf("逆向 UPDATE 应以新值作守卫（IS NOT DISTINCT FROM）：%s", upd)
	}
	if strings.Contains(upd, `"id" = 1001,`) {
		t.Fatalf("未变更列（主键）不应出现在 SET 里：%s", upd)
	}

	// INSERT 逆向：按主键删除
	var delStmt string
	for _, s := range stmts {
		if strings.HasPrefix(s, "DELETE FROM") {
			delStmt = s
		}
	}
	if delStmt == "" {
		t.Fatalf("应生成逆向 DELETE")
	}
	if !strings.Contains(delStmt, `"id" = 1001`) {
		t.Fatalf("逆向 DELETE 应按主键定位：%s", delStmt)
	}
}

func TestBuildRollbackStatementsNoPK(t *testing.T) {
	ctx := context.Background()
	metas := map[string]tableMeta{
		"public.t_dict": {RelReplIdent: "f"}, // 无主键，FULL
	}
	ch, _ := parseTestDecodingLine(
		"table public.t_dict: DELETE: code[character varying]:'PAID' label[character varying]:'已支付'")

	stmts, _ := buildRollbackStatements(ctx, fakeMeta(metas), []decodedChange{*ch})
	if len(stmts) != 1 {
		t.Fatalf("无主键但 FULL 的 DELETE 仍应可重建，实际 %d 条", len(stmts))
	}
	// 无主键 → 无法生成 ON CONFLICT 幂等守卫（重复回滚会重复插入，属已知限制）
	if strings.Contains(stmts[0], "ON CONFLICT") {
		t.Fatalf("无主键不应生成 ON CONFLICT：%s", stmts[0])
	}
	if !strings.Contains(stmts[0], `'已支付'`) {
		t.Fatalf("重建 INSERT 应含整行旧值：%s", stmts[0])
	}
}

// ---------------------------------------------------------------------------
// 槽名与配置
// ---------------------------------------------------------------------------

func TestSlotNameForTask(t *testing.T) {
	cases := map[string]string{
		"tk-ul1zqgiypfv1": "jaysql_rb_tk_ul1zqgiypfv1",
		"TK-ABC":          "jaysql_rb_tk_abc",
		"a b#c":           "jaysql_rb_a_b_c",
		"":                "jaysql_rb_",
	}
	for in, want := range cases {
		if got := slotNameForTask(in); got != want {
			t.Errorf("slotNameForTask(%q) = %q, want %q", in, got, want)
		}
	}
	// 超长任务号截断到 63 字节内
	long := "tk-" + strings.Repeat("x", 200)
	if got := slotNameForTask(long); len(got) > 63 {
		t.Errorf("超长任务号应截断到 63 字节内，实际 %d", len(got))
	}
}

func TestOrderSettingsDefaults(t *testing.T) {
	// 未配置（0）→ 默认 1000
	if got := (config.Configuration{}).OrderSettings().PGRollbackMaxRows; got != 1000 {
		t.Errorf("未配置应回落默认 1000，实际 %d", got)
	}
	// 负数 → 关闭（保留原值，由调用方判断 <0 跳过）
	if got := (config.Configuration{Order: config.Order{PGRollbackMaxRows: -1}}).OrderSettings().PGRollbackMaxRows; got != -1 {
		t.Errorf("负数应原样保留（表示关闭），实际 %d", got)
	}
	// 正值原样
	if got := (config.Configuration{Order: config.Order{PGRollbackMaxRows: 5000}}).OrderSettings().PGRollbackMaxRows; got != 5000 {
		t.Errorf("正值应原样保留，实际 %d", got)
	}
}

// 验证数值边界：生成语句里字面量原样回填（引号值已是合法 PG 字面量）
func TestRenderValue(t *testing.T) {
	if got := renderValue(decodedColumn{Value: `'O''Reilly'`}); got != `'O''Reilly'` {
		t.Errorf("引号字面量应原样回填，got %s", got)
	}
	if got := renderValue(decodedColumn{Value: "null", IsNull: true}); got != "NULL" {
		t.Errorf("NULL 应渲染为 NULL，got %s", got)
	}
	if got := renderValue(decodedColumn{Value: "true"}); got != "true" {
		t.Errorf("布尔应原样，got %s", got)
	}
}
