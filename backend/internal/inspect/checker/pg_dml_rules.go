package checker

// PG 引擎的 DML 文本类规则（2026-11-10 补齐）
//
// 背景：PG 引擎此前只实现了 12 条 DML 规则（WHERE 存在性、JOIN ON、LIMIT/影响行数/
// IN 元素数/时间列/INSERT 行数/值匹配/表存在性），规范目录里的另外 15 条规则
// 在 MySQL/Oracle/SQLServer 引擎都有判定，PG 侧却一条都不产出 —— 表现为
// 「规范里开了，工单预审却永远不报」，属于静默失效。
//
// 本文件按 MySQL/Oracle 的同名规则口径补齐，遵守 PG 引擎的统一约定：
//  1. 纯文本可判定的规则（WHERE 类、SET 分隔符、字段重复、未指定字段列表、
//     SYSDATE()、多表前缀）不连库；
//  2. 依赖表结构的规则（主键/唯一键/索引列/NOT NULL 列/列存在性）连库查询，
//     查询失败一律跳过，绝不猜测式误报；
//  3. 规则开关与级别仍由规范映射出的 InspectParams 决定，引擎只做执行者。

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
)

var (
	// LIKE/ILIKE 字面量：捕获引号内内容，用于前通配符与「无通配符」判定
	pgLikeLiteralRe = regexp.MustCompile(`(?i)\b(?:i?like)\s+'([^']*)'`)
	// SYSDATE()：PG 无此函数，但迁移自 MySQL 的语句里常见，保留同口径告警
	pgSysdateRe = regexp.MustCompile(`(?i)\bsysdate\s*\(`)
	// NOT IN / NOT LIKE
	pgNotInLikeRe = regexp.MustCompile(`(?i)\bnot\s+(?:in|like)\b`)
)

// inspectPGDMLTextRules 补齐的 15 条 DML 规则判定入口。
// orig 为原始语句（字符串字面量保留），clean 为 stripPGStringsAndComments 后的等长文本。
func (c *pgInspectContext) inspectPGDMLTextRules(orig, clean string, res pgDMLAnalysis) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	table := res.Table

	switch res.Kind {
	case "update", "delete":
		items = append(items, c.checkPGWhereRules(orig, clean, res)...)
		if res.Kind == "update" {
			items = append(items, c.checkPGUpdateRules(clean, res)...)
		}
		if res.Kind == "delete" && c.params.CHECK_DELETE_TABLE_EXISTS && strings.TrimSpace(table) != "" {
			// 目标表存在性：DELETE 写错表名/模式是最高频的低级错误（PG 42P01）
			if exists, err := c.metaResolverFor(res.TableSchema).TableExists(table); err == nil && !exists {
				items = append(items, pgObjectMissing("delete_stmt_check_table_exists", table,
					"① 核对表名拼写与大小写（PG 未加引号的标识符会折叠为小写）；② 确认 schema / search_path 是否正确；③ 若确认存在仍被报缺失，请先同步实例元数据再重新检查",
					"表`%s`不存在，无法执行 `DELETE`", table))
			}
		}
	case "insert":
		items = append(items, c.checkPGInsertRules(clean, res)...)
	}
	return items
}

// ---------- WHERE 类规则 ----------

// checkPGWhereRules WHERE 条件上的四条规则：
// 前通配符、LIKE 无通配符、NOT IN/NOT LIKE、OR 连接。
// 判定用原始 WHERE 片段（clean 会把字面量抹成空格，看不到 '%xxx'）。
func (c *pgInspectContext) checkPGWhereRules(orig, clean string, res pgDMLAnalysis) []controllers.SummaryItem {
	rawWhere := pgRawWhereClause(orig, clean)
	if strings.TrimSpace(rawWhere) == "" {
		return nil
	}
	table := res.Table
	var items []controllers.SummaryItem

	if c.params.CHECK_WHERE_LEADING_WILDCARD {
		for _, m := range pgLikeLiteralRe.FindAllStringSubmatch(rawWhere, -1) {
			if strings.HasPrefix(m[1], "%") {
				items = append(items, pgSummary("where_avoid_leading_wildcard",
					"WHERE 条件中不建议使用前通配符查找（`%s`）：索引无法使用，将退化为全表扫描（表`%s`）", m[1], table))
				break
			}
		}
	}
	if c.params.CHECK_WHERE_LIKE_WITHOUT_WILDCARD {
		for _, m := range pgLikeLiteralRe.FindAllStringSubmatch(rawWhere, -1) {
			lit := m[1]
			if lit == "" {
				continue
			}
			if strings.ContainsAny(lit, "%_") {
				continue
			}
			items = append(items, pgSummary("where_like_without_wildcard",
				"WHERE 条件中 `LIKE '%s'` 未使用通配符：等价于等值比较却无法走索引，建议改用 `=`（表`%s`）", lit, table))
			break
		}
	}
	if c.params.CHECK_WHERE_NOT_IN_NOT_LIKE {
		if m := pgNotInLikeRe.FindString(rawWhere); m != "" {
			items = append(items, pgSummary("where_avoid_not_in_not_like",
				"WHERE 条件中不建议使用反向查询（`%s`）：索引利用率低且 NULL 语义易出错（表`%s`）", strings.ToUpper(m), table))
		}
	}
	if c.params.CHECK_WHERE_OR_CONDITION {
		if pgHasTopLevelOperator(rawWhere, "or") {
			items = append(items, pgSummary("where_or_condition_check",
				"WHERE 条件中使用 `OR` 连接过滤条件：可能导致索引失效或结果集放大，建议改写为 `UNION ALL` 或 `IN`（表`%s`）", table))
		}
	}
	return items
}

// pgRawWhereClause 取原始语句里的 WHERE 片段（保留字面量）。
// clean 由 stripPGStringsAndComments 产出，与原文等长，因此下标可直接对齐。
func pgRawWhereClause(orig, clean string) string {
	if len(orig) != len(clean) {
		return ""
	}
	idx := indexOfKeyword(clean, "where")
	if idx < 0 {
		return ""
	}
	start := idx + len("where")
	end := len(clean)
	after := clean[start:]
	for _, tail := range []string{"order by", "limit", "returning", "for update"} {
		if i := indexOfKeyword(after, tail); i >= 0 && start+i < end {
			end = start + i
		}
	}
	return strings.TrimSpace(orig[start:end])
}

// pgHasTopLevelOperator 判断片段里是否存在顶层（括号外）的指定关键字。
func pgHasTopLevelOperator(s, kw string) bool {
	depth := 0
	lower := strings.ToLower(s)
	for i := 0; i < len(lower); i++ {
		switch lower[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth != 0 || i+len(kw) > len(lower) || lower[i:i+len(kw)] != kw {
			continue
		}
		leftOK := i == 0 || !isPGWordByte(lower[i-1])
		rightOK := i+len(kw) == len(lower) || !isPGWordByte(lower[i+len(kw)])
		if leftOK && rightOK {
			return true
		}
	}
	return false
}

func isPGWordByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '_':
		return true
	}
	return false
}

// ---------- UPDATE 类规则 ----------

func (c *pgInspectContext) checkPGUpdateRules(clean string, res pgDMLAnalysis) []controllers.SummaryItem {
	table := res.Table
	assigns := pgSetAssignments(clean)
	setClause := pgSetClause(clean)
	var items []controllers.SummaryItem

	// SET 多列之间用 AND 分隔（规则 update_stmt_set_separator_check）。
	// 顶层 AND 出现在 SET 子句内即判为分隔符误用；`SET a = (b AND c)` 在括号内，不误报。
	if c.params.CHECK_UPDATE_SET_SEPARATOR && pgHasTopLevelOperator(setClause, "and") {
		items = append(items, pgSummary("update_stmt_set_separator_check",
			"UPDATE 语句 SET 多个列之间必须用逗号分隔，检测到使用 `AND`（表`%s`）", table))
	}

	// 多表 UPDATE 的 SET 列需带表前缀（规则 update_multi_table_column_prefix_check）。
	// PG 的多表写法是 `UPDATE t1 SET ... FROM t2 ...`，JoinTableCount 已把 FROM 计入。
	if c.params.CHECK_UPDATE_MULTI_TABLE_COLUMN_PREFIX && res.JoinTableCount > 1 {
		for _, a := range assigns {
			if !strings.Contains(a.LHS, ".") {
				items = append(items, pgSummary("update_multi_table_column_prefix_check",
					"多表 UPDATE 语句中 SET 的列`%s`未指定表前缀：列名歧义会导致更新到非预期字段（表`%s`）", a.LHS, table))
				break
			}
		}
	}

	if len(res.UpdateSetColumns) == 0 || strings.TrimSpace(table) == "" {
		return items
	}

	// SET 列存在性（规则 update_stmt_check_column_exists）。
	// 与 INSERT 的目标对象存在性共用 CHECK_INSERT_OBJECT_EXISTS 开关（同一能力族）。
	if c.params.CHECK_INSERT_OBJECT_EXISTS {
		if exists, err := c.metaResolverFor(res.TableSchema).TableExists(table); err == nil && exists {
			for _, col := range res.UpdateSetColumns {
				col = strings.TrimSpace(col)
				if col == "" || isPGFunctionCallToken(col) {
					continue
				}
				if ok, err := c.columnExistsIn(res.TableSchema, table, col); err == nil && !ok {
					items = append(items, pgObjectMissing("update_stmt_check_column_exists", col,
						"① 核对列名拼写与大小写；② 该列可能已被其他工单删除，请先同步实例元数据再重新检查",
						"列`%s`不存在（表`%s`）", col, table))
				}
			}
		}
	}

	// 更新主键 / 唯一键：依赖表结构，查询失败跳过
	if c.params.CHECK_UPDATE_UPDATES_PK || c.params.CHECK_UPDATE_UPDATES_UNIQUE_KEY {
		pkCols, pkErr := c.pgConstraintColumns(res.TableSchema, table, "PRIMARY KEY")
		if pkErr == nil {
			if c.params.CHECK_UPDATE_UPDATES_PK {
				if hit := pgIntersect(res.UpdateSetColumns, pkCols); hit != "" {
					items = append(items, pgSummary("update_stmt_check_updates_pk",
						"UPDATE 语句不允许更新主键列`%s`（表`%s`）", hit, table))
				}
			}
			if c.params.CHECK_UPDATE_UPDATES_UNIQUE_KEY {
				uniqCols, err := c.pgConstraintColumns(res.TableSchema, table, "UNIQUE")
				if err == nil {
					// UNION 去重：主键已由 CHECK_UPDATE_UPDATES_PK 覆盖，不重复告警
					if hit := pgIntersect(res.UpdateSetColumns, uniqCols); hit != "" && !containsFold(pkCols, hit) {
						items = append(items, pgSummary("update_stmt_check_updates_unique_key",
							"UPDATE 语句不建议更新唯一键列`%s`：并发下易产生唯一键冲突与死锁（表`%s`）", hit, table))
					}
				}
			}
		}
	}

	// 索引列参与运算/函数（WHERE 规则，但依赖表上的索引列，放在这里统一取元信息）
	if c.params.CHECK_WHERE_INDEX_COL_CALC {
		items = append(items, c.checkPGIndexColCalc(clean, res)...)
	}
	return items
}

// checkPGIndexColCalc WHERE 中的索引列是否被算术运算或函数包裹（规则 where_avoid_index_col_arithmetic_or_func）
func (c *pgInspectContext) checkPGIndexColCalc(clean string, res pgDMLAnalysis) []controllers.SummaryItem {
	cols, err := c.pgIndexColumns(res.TableSchema, res.Table)
	if err != nil || len(cols) == 0 {
		return nil
	}
	whereClause := strings.ToLower(res.WhereClause)
	if strings.TrimSpace(whereClause) == "" {
		return nil
	}
	for _, col := range cols {
		col = strings.ToLower(strings.TrimSpace(col))
		if col == "" {
			continue
		}
		// 算术：col + / col - / col * / col /
		if regexp.MustCompile(`(?i)(^|[^a-z0-9_])`+regexp.QuoteMeta(col)+`\s*[+\-*/]`).MatchString(whereClause) {
			return []controllers.SummaryItem{pgSummary("where_avoid_index_col_arithmetic_or_func",
				"WHERE 条件中索引列`%s`参与了数学运算：索引失效（表`%s`）", col, res.Table)}
		}
		// 函数：func(col)
		if regexp.MustCompile(`(?i)[a-z_][a-z0-9_]*\s*\(\s*`+regexp.QuoteMeta(col)+`\s*[,)]`).MatchString(whereClause) {
			return []controllers.SummaryItem{pgSummary("where_avoid_index_col_arithmetic_or_func",
				"WHERE 条件中索引列`%s`被函数包裹：索引失效（表`%s`）", col, res.Table)}
		}
	}
	return nil
}

// ---------- INSERT 类规则 ----------

func (c *pgInspectContext) checkPGInsertRules(clean string, res pgDMLAnalysis) []controllers.SummaryItem {
	table := res.Table
	var items []controllers.SummaryItem

	cols, hasCols := pgInsertColumnList(clean)

	// 建议指定字段列表（规则 insert_stmt_specify_columns）
	if c.params.CHECK_INSERT_SPECIFY_COLUMNS && !hasCols {
		items = append(items, pgSummary("insert_stmt_specify_columns",
			"INSERT 语句建议显式指定字段列表：表结构变更后 `INSERT INTO %s VALUES (...)` 会直接失败或错位（表`%s`）", table, table))
	}

	// 字段名不能重复（规则 insert_stmt_col_name_no_dup）
	if c.params.CHECK_INSERT_COLUMN_NO_DUP && len(cols) > 0 {
		seen := map[string]bool{}
		for _, col := range cols {
			key := strings.ToLower(strings.TrimSpace(col))
			if key == "" {
				continue
			}
			if seen[key] {
				items = append(items, pgSummary("insert_stmt_col_name_no_dup",
					"INSERT 语句字段列表中存在重复字段`%s`（表`%s`）", col, table))
				break
			}
			seen[key] = true
		}
	}

	// 不建议使用 SYSDATE()（规则 insert_stmt_avoid_sysdate_func）
	if c.params.CHECK_INSERT_AVOID_SYSDATE && pgSysdateRe.MatchString(clean) {
		items = append(items, pgSummary("insert_stmt_avoid_sysdate_func",
			"INSERT 语句不建议使用 `SYSDATE()`：取的是函数执行时刻而非语句开始时间，主从/回放数据不一致（表`%s`）", table))
	}

	// NOT NULL 列插入 NULL（规则 insert_stmt_not_null_column_null_value）
	if c.params.CHECK_INSERT_NOT_NULL_COLUMN_NULL && len(cols) > 0 && strings.TrimSpace(table) != "" {
		rows := pgInsertValueRowsRaw(clean)
		if len(rows) > 0 {
			if notNull, err := c.pgNotNullColumns(res.TableSchema, table); err == nil && len(notNull) > 0 {
				for _, row := range rows {
					for i, val := range row {
						if i >= len(cols) {
							break
						}
						if !strings.EqualFold(strings.TrimSpace(val), "null") {
							continue
						}
						col := strings.TrimSpace(cols[i])
						if containsFold(notNull, col) {
							items = append(items, pgSummary("insert_stmt_not_null_column_null_value",
								"INSERT 语句为目标表 NOT NULL 列`%s`插入 NULL（表`%s`），执行必然失败", col, table))
							return items
						}
					}
				}
			}
		}
	}
	return items
}

// pgInsertColumnList 取 INSERT 的显式字段列表（未指定时 has=false）
func pgInsertColumnList(clean string) (cols []string, has bool) {
	lower := strings.ToLower(clean)
	intoIdx := strings.Index(lower, "into")
	if intoIdx < 0 {
		return nil, false
	}
	i := intoIdx + len("into")
	// ① 跳过 into 与表名之间的空白
	for i < len(clean) && isPGSpace(clean[i]) {
		i++
	}
	// ② 跳过表名（可能带 schema 前缀与双引号）
	for i < len(clean) && clean[i] != '(' && !isPGSpace(clean[i]) {
		i++
	}
	// ③ 跳过表名后的空白，此时遇到左括号说明有显式字段列表
	for i < len(clean) && isPGSpace(clean[i]) {
		i++
	}
	if i >= len(clean) || clean[i] != '(' {
		return nil, false
	}
	closeIdx := pgMatchingParen(clean, i)
	if closeIdx <= i {
		return nil, false
	}
	for _, item := range pgSplitTopLevel(clean[i+1:closeIdx], ',') {
		name := strings.TrimSpace(item)
		name = strings.Trim(name, `"`)
		if name != "" {
			cols = append(cols, name)
		}
	}
	return cols, len(cols) > 0
}

// pgInsertValueRowsRaw 取每条 VALUES 行的顶层元素（保留 NULL 等关键字原文）
func pgInsertValueRowsRaw(clean string) [][]string {
	valIdx := indexOfKeyword(clean, "values")
	if valIdx < 0 {
		return nil
	}
	rest := clean[valIdx+len("values"):]
	var rows [][]string
	for i := 0; i < len(rest); {
		for i < len(rest) && isPGSpace(rest[i]) {
			i++
		}
		if i >= len(rest) || rest[i] != '(' {
			break
		}
		closeIdx := pgMatchingParen(rest, i)
		if closeIdx < 0 {
			break
		}
		rows = append(rows, pgSplitTopLevel(rest[i+1:closeIdx], ','))
		i = closeIdx + 1
		for i < len(rest) && isPGSpace(rest[i]) {
			i++
		}
		if i < len(rest) && rest[i] == ',' {
			i++
			continue
		}
		break
	}
	return rows
}

func isPGSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// pgSplitTopLevel 按顶层分隔符切分（括号内的分隔符不切）
func pgSplitTopLevel(s string, sep byte) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if s[i] == sep && depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	out = append(out, strings.TrimSpace(s[start:]))
	return out
}

// pgSetAssignment SET 子句里的一项赋值
type pgSetAssignment struct {
	LHS string // 等号左侧原文（可能带表前缀）
}

// pgSetAssignments 取 UPDATE 的 SET 赋值列表（顶层逗号切分）
func pgSetAssignments(clean string) []pgSetAssignment {
	clause := pgSetClause(clean)
	var out []pgSetAssignment
	for _, item := range pgSplitTopLevel(clause, ',') {
		lhs := item
		if i := strings.Index(item, "="); i >= 0 {
			lhs = strings.TrimSpace(item[:i])
		}
		lhs = strings.TrimSpace(lhs)
		if lhs == "" {
			continue
		}
		out = append(out, pgSetAssignment{LHS: lhs})
	}
	return out
}

// pgSetClause 取 SET 与 WHERE/ORDER BY/LIMIT/RETURNING 之间的原文
func pgSetClause(clean string) string {
	lower := strings.ToLower(clean)
	setIdx := strings.Index(lower, " set ")
	if setIdx < 0 {
		return ""
	}
	rest := clean[setIdx+len(" set "):]
	end := len(rest)
	lowerRest := strings.ToLower(rest)
	for _, kw := range []string{" where ", " order by ", " limit ", " returning "} {
		if i := strings.Index(lowerRest, kw); i >= 0 && i < end {
			end = i
		}
	}
	return strings.TrimSpace(rest[:end])
}

// ---------- 元信息查询（失败即跳过） ----------

// pgConstraintColumns 取表上指定约束类型（PRIMARY KEY / UNIQUE）覆盖的列
func (c *pgInspectContext) pgConstraintColumns(schema, table, constraintType string) ([]string, error) {
	if c.db == nil {
		return nil, fmt.Errorf("未初始化数据库连接")
	}
	schemaCond := "current_schema()"
	if s := strings.TrimSpace(schema); s != "" {
		schemaCond = sqlQuoteLiteral(s)
	}
	query := fmt.Sprintf(
		"SELECT a.attname AS name FROM information_schema.table_constraints tc "+
			"JOIN information_schema.key_column_usage k ON k.constraint_name = tc.constraint_name "+
			"AND k.table_schema = tc.table_schema AND k.table_name = tc.table_name "+
			"JOIN pg_namespace n ON n.nspname = tc.table_schema "+
			"JOIN pg_class cl ON cl.relname = tc.table_name AND cl.relnamespace = n.oid "+
			"JOIN pg_attribute a ON a.attrelid = cl.oid AND a.attname = k.column_name "+
			"WHERE tc.table_schema = %s AND tc.table_name = %s AND tc.constraint_type = %s",
		schemaCond, sqlQuoteLiteral(table), sqlQuoteLiteral(constraintType))
	return c.queryStringColumn(query)
}

// pgNotNullColumns 取表的 NOT NULL 列
func (c *pgInspectContext) pgNotNullColumns(schema, table string) ([]string, error) {
	if c.db == nil {
		return nil, fmt.Errorf("未初始化数据库连接")
	}
	schemaCond := "current_schema()"
	if s := strings.TrimSpace(schema); s != "" {
		schemaCond = sqlQuoteLiteral(s)
	}
	query := fmt.Sprintf(
		"SELECT a.attname AS name FROM pg_attribute a "+
			"JOIN pg_class c ON c.oid = a.attrelid "+
			"JOIN pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE n.nspname = %s AND lower(c.relname) = lower(%s) "+
			"AND a.attnum > 0 AND NOT a.attisdropped AND a.attnotnull",
		schemaCond, sqlQuoteLiteral(table))
	return c.queryStringColumn(query)
}

// pgIndexColumns 取表上所有索引覆盖的列（去重）
func (c *pgInspectContext) pgIndexColumns(schema, table string) ([]string, error) {
	if c.db == nil {
		return nil, fmt.Errorf("未初始化数据库连接")
	}
	schemaCond := "current_schema()"
	if s := strings.TrimSpace(schema); s != "" {
		schemaCond = sqlQuoteLiteral(s)
	}
	query := fmt.Sprintf(
		"SELECT DISTINCT a.attname AS name FROM pg_index i "+
			"JOIN pg_class t ON t.oid = i.indrelid "+
			"JOIN pg_namespace n ON n.oid = t.relnamespace "+
			"JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(i.indkey) "+
			"WHERE n.nspname = %s AND lower(t.relname) = lower(%s) AND a.attnum > 0",
		schemaCond, sqlQuoteLiteral(table))
	return c.queryStringColumn(query)
}

// queryStringColumn 执行查询并取出第一列（名为 name / count 或唯一列）的字符串值
func (c *pgInspectContext) queryStringColumn(query string) ([]string, error) {
	result, err := c.db.Query(query)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	var out []string
	for _, row := range *result {
		val, ok := row["name"]
		if !ok {
			// 兼容驱动把列名处理成其它大小写
			for k, v := range row {
				if strings.EqualFold(k, "name") {
					val, ok = v, true
					break
				}
			}
		}
		if !ok {
			continue
		}
		switch v := val.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				out = append(out, s)
			}
		case []byte:
			if s := strings.TrimSpace(string(v)); s != "" {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// ---------- 小工具 ----------

// pgIntersect 返回 setCols 中第一个命中 candidates 的列（大小写不敏感）
func pgIntersect(setCols, candidates []string) string {
	for _, col := range setCols {
		col = strings.TrimSpace(col)
		if col == "" {
			continue
		}
		if i := strings.LastIndex(col, "."); i >= 0 {
			col = col[i+1:]
		}
		col = strings.Trim(col, `"`)
		for _, cand := range candidates {
			if strings.EqualFold(col, cand) {
				return col
			}
		}
	}
	return ""
}

func containsFold(list []string, want string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

// isPGFunctionCallToken SET 左侧不是列（例如 `SET (a, b) = (1, 2)` 或函数调用）时跳过存在性判定
func isPGFunctionCallToken(col string) bool {
	return strings.ContainsAny(col, "()")
}
