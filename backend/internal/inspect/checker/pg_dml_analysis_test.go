package checker

import "testing"

func TestPGDMLAnalysisTmp(t *testing.T) {
	cases := []struct {
		name       string
		sql        string
		kind       string
		joins      int
		limit      int
		setCols    int
		hasUpdTime bool
		maxIn      int
	}{
		{name: "update单表带limit", sql: "UPDATE t_order SET status = 1 WHERE id > 100 LIMIT 10", kind: "update", joins: 1, limit: 10, setCols: 1, hasUpdTime: false, maxIn: 0},
		{name: "update两表join", sql: "UPDATE a JOIN b ON a.id = b.id SET a.status = 1 WHERE b.x = 2", kind: "update", joins: 2, limit: 0, setCols: 1, hasUpdTime: false, maxIn: 0},
		{name: "update含update_time", sql: "UPDATE t SET status = 1, update_time = now() WHERE id IN (1,2,3)", kind: "update", joins: 1, limit: 0, setCols: 2, hasUpdTime: true, maxIn: 3},
		{name: "delete using两表", sql: "DELETE FROM a USING b WHERE a.id = b.id", kind: "delete", joins: 2, limit: 0, setCols: 0, hasUpdTime: false, maxIn: 0},
		{name: "delete where in大量", sql: "DELETE FROM t WHERE id IN (1,2,3,4,5,6,7)", kind: "delete", joins: 1, limit: 0, setCols: 0, hasUpdTime: false, maxIn: 7},
		{name: "in列表含字符串", sql: "UPDATE t SET status = 0 WHERE name IN ('a', 'b', 'c') AND id IN (1)", kind: "update", joins: 1, limit: 0, setCols: 1, hasUpdTime: false, maxIn: 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := analyzePGDML(c.sql)
			if !res.OK || res.Kind != c.kind {
				t.Fatalf("kind=%q ok=%v, want %q", res.Kind, res.OK, c.kind)
			}
			if res.JoinTableCount != c.joins {
				t.Errorf("JoinTableCount=%d want %d", res.JoinTableCount, c.joins)
			}
			if res.LimitRows != c.limit {
				t.Errorf("LimitRows=%d want %d", res.LimitRows, c.limit)
			}
			if len(res.UpdateSetColumns) != c.setCols {
				t.Errorf("UpdateSetColumns=%v want %d cols", res.UpdateSetColumns, c.setCols)
			}
			hasTime := false
			for _, col := range res.UpdateSetColumns {
				if col == "update_time" {
					hasTime = true
				}
			}
			if hasTime != c.hasUpdTime {
				t.Errorf("hasUpdateTime=%v want %v (cols=%v)", hasTime, c.hasUpdTime, res.UpdateSetColumns)
			}
			if res.MaxInElements != c.maxIn {
				t.Errorf("MaxInElements=%d want %d", res.MaxInElements, c.maxIn)
			}
		})
	}
}

// TestPGDMLWhereClause 影响行数估算依赖 WHERE 条件原文（规则 limit_update_delete_affected_rows），
// 提取须到 order by / limit / returning / for update 为止。
func TestPGDMLWhereClause(t *testing.T) {
	cases := []struct {
		name  string
		sql   string
		where string
	}{
		{name: "简单where", sql: "UPDATE t_order SET status = 1 WHERE id > 100", where: "id > 100"},
		{name: "where到order_by为止", sql: "UPDATE t SET a = 1 WHERE id = 2 ORDER BY id LIMIT 5", where: "id = 2"},
		{name: "delete带returning", sql: "DELETE FROM t WHERE id IN (1,2) RETURNING id", where: "id IN (1,2)"},
		{name: "schema限定表", sql: "UPDATE public.t SET a = 1 WHERE id = 9", where: "id = 9"},
		{name: "无where", sql: "DELETE FROM t", where: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := analyzePGDML(c.sql)
			if !res.OK {
				t.Fatalf("analyzePGDML not OK")
			}
			if res.WhereClause != c.where {
				t.Errorf("WhereClause=%q want %q", res.WhereClause, c.where)
			}
		})
	}
	res := analyzePGDML("UPDATE public.t_order SET a = 1 WHERE id = 9")
	if res.TableSchema != "public" || res.Table != "t_order" {
		t.Errorf("TableSchema=%q Table=%q, want public/t_order", res.TableSchema, res.Table)
	}
}
