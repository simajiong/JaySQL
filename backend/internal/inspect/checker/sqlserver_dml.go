package checker

// SQL Server 的 DML 文本分析
//
// 复用 PostgreSQL 引擎的 analyzePGDML（INSERT/UPDATE/DELETE 的通用结构分析），
// 只做两件 T-SQL 专属的事：
//  1. 把 T-SQL 的写法归一化成 analyzePGDML 认识的形式：
//     `UPDATE TOP (10) t SET ...`（去 TOP）、`DELETE a FROM t a JOIN ...`（补 FROM）、
//     `INSERT t (a) VALUES (...)`（补 INTO）；
//  2. 用 TOP (n) 填充 LimitRows —— T-SQL 没有 LIMIT，等价的限量语法只有 TOP。

import (
	"regexp"
	"strings"
)

// sqlServerDMLAnalysis DML 语句的文本级结构分析
type sqlServerDMLAnalysis struct {
	pgDMLAnalysis
	// WhereClause WHERE 之后的原始条件文本（用于 COUNT 估算与存在性探测；无法定位时为空）
	WhereClause string
}

// analyzeSQLServerDML 解析 UPDATE/DELETE/INSERT 的关键结构
func analyzeSQLServerDML(stmt string) sqlServerDMLAnalysis {
	res := sqlServerDMLAnalysis{pgDMLAnalysis: analyzePGDML(normalizeSQLServerDML(stmt))}
	clean := stripPGStringsAndComments(stmt)
	if res.Kind == "" {
		// 归一化失败时兜底再试一次原文（例如 DELETE 的特殊写法）
		res.pgDMLAnalysis = analyzePGDML(stmt)
	}
	if !res.OK {
		return res
	}
	if res.Kind != "insert" {
		// TOP (n) 是 T-SQL 的限量语法，等价于 MySQL/PG 的 LIMIT
		res.LimitRows = sqlServerTopRows(clean)
		if idx := sqlServerLastTopLevelKeyword(clean, "top"); idx >= 0 {
			// TOP 只出现在语句头部附近（UPDATE TOP (n) t / DELETE TOP (n) FROM t）
			res.LimitRows = sqlServerTopRows(clean[idx:])
		}
		// 关联表数按 T-SQL 语义重算（JOIN 在 SET 之后）
		res.JoinTableCount = sqlServerJoinTableCount(clean)
		// 多表写法的目标位置是别名（UPDATE o ... FROM dbo.t_order o），
		// 必须还原成真实表名，否则提示文案与对象存在性探测都会对到别名上
		if real := sqlServerResolveTableAlias(clean, res.Table); real != "" {
			res.Table = real
		}
		res.WhereClause = sqlServerWhereClause(stmt, clean)
	}
	return res
}

// sqlServerResolveTableAlias 把 UPDATE/DELETE 的目标别名还原为真实表名。
//
// `UPDATE o SET ... FROM dbo.t_order o JOIN dbo.t_user u ON ...` 的目标是别名 o，
// 直接拿它去 sys.objects 探测或写进结论都是错的。
// 找不到对应别名（或目标本身就是真实表名）时返回空串，调用方保留原值。
func sqlServerResolveTableAlias(clean, target string) string {
	target = strings.ToLower(unquoteSQLServerIdent(target))
	if target == "" || strings.Contains(target, ".") {
		return ""
	}
	fromIdx := sqlServerLastTopLevelKeyword(clean, "from")
	if fromIdx < 0 {
		return ""
	}
	seg := clean[fromIdx+len("from"):]
	for _, kw := range []string{"where", "order", "group", "having", "option", "for", "output"} {
		if i := sqlServerFirstTopLevelKeyword(seg, kw); i >= 0 {
			seg = seg[:i]
		}
	}
	for _, item := range splitSQLServerTopLevel(seg, ',') {
		fields := strings.Fields(strings.TrimSpace(item))
		// 去掉 JOIN / LEFT / INNER 等前缀（`JOIN dbo.t_user u ON ...`）
		for len(fields) > 0 && isSQLServerFromKeyword(fields[0]) {
			fields = fields[1:]
		}
		if len(fields) < 2 {
			continue
		}
		table := fields[0]
		alias := fields[1]
		if strings.EqualFold(alias, "as") && len(fields) >= 3 {
			alias = fields[2]
		}
		if strings.EqualFold(unquoteSQLServerIdent(alias), target) {
			return unquoteSQLServerIdent(table)
		}
	}
	return ""
}

// isSQLServerFromKeyword FROM 子句里的连接类关键字（不可能是表名）
func isSQLServerFromKeyword(tok string) bool {
	switch strings.ToLower(tok) {
	case "join", "inner", "left", "right", "full", "cross", "outer", "on", "as", "apply":
		return true
	}
	return false
}

// normalizeSQLServerDML 把 T-SQL 的 DML 写法归一化成 PostgreSQL 风格，
// 便于复用 analyzePGDML 的结构分析。
func normalizeSQLServerDML(stmt string) string {
	clean := stripPGStringsAndComments(stmt)
	lower := strings.ToLower(strings.TrimSpace(clean))
	switch {
	case strings.HasPrefix(lower, "update"):
		rest := strings.TrimSpace(clean[len("update"):])
		return "update " + stripSQLServerTopPrefix(rest)
	case strings.HasPrefix(lower, "delete"):
		rest := stripSQLServerTopPrefix(strings.TrimSpace(clean[len("delete"):]))
		if !strings.HasPrefix(strings.ToLower(rest), "from") {
			// DELETE 别名 FROM 表 别名 [JOIN ...]：目标就是 FROM 后的第一张表
			target := firstSQLServerToken(rest)
			remainder := strings.TrimSpace(rest[len(target):])
			if idx := indexOfKeyword(strings.ToLower(remainder), "from"); idx >= 0 {
				rest = "from " + strings.TrimSpace(remainder[idx+len("from"):])
			} else {
				rest = "from " + rest
			}
		}
		return "delete " + rest
	case strings.HasPrefix(lower, "insert"):
		rest := strings.TrimSpace(clean[len("insert"):])
		if !strings.HasPrefix(strings.ToLower(rest), "into") {
			rest = "into " + rest
		}
		return "insert " + rest
	}
	return stmt
}

// stripSQLServerTopPrefix 去掉语句头部的 TOP (n) / TOP (n) PERCENT。
func stripSQLServerTopPrefix(rest string) string {
	lower := strings.ToLower(strings.TrimSpace(rest))
	if !strings.HasPrefix(lower, "top") {
		return rest
	}
	rest = strings.TrimSpace(rest[len("top"):])
	if strings.HasPrefix(rest, "(") {
		if idx := strings.IndexByte(rest, ')'); idx > 0 {
			rest = strings.TrimSpace(rest[idx+1:])
		}
	} else {
		token := firstSQLServerToken(rest)
		rest = strings.TrimSpace(rest[len(token):])
	}
	if strings.HasPrefix(strings.ToLower(rest), "percent") {
		rest = strings.TrimSpace(rest[len("percent"):])
	}
	return rest
}

// sqlServerTopLevelKeywordPositions 返回关键字在括号/引号/方括号之外出现的所有位置。
//
// 关键字必须以空白开头（避免命中 `offset`/`settings` 之类的词尾）。
func sqlServerTopLevelKeywordPositions(clean, keyword string) []int {
	lower := strings.ToLower(clean)
	target := strings.ToLower(keyword)
	if target == "" {
		return nil
	}
	var out []int
	runes := []rune(lower)
	targetRunes := []rune(target)
	depth := 0
	inSingle, inDouble, inBracket := false, false, false
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
			continue
		case inDouble:
			if c == '"' {
				inDouble = false
			}
			continue
		case inBracket:
			if c == ']' {
				inBracket = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '[':
			inBracket = true
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ' ', '\t', '\n', '\r':
			if depth != 0 {
				continue
			}
			start := i + 1
			if start+len(targetRunes) > len(runes) {
				continue
			}
			ok := true
			for j := range targetRunes {
				if runes[start+j] != targetRunes[j] {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			end := start + len(targetRunes)
			if end < len(runes) && isSQLServerWordRune(runes[end]) {
				continue
			}
			// 偏移换算回原字符串（lower 与 clean 字节长度一致）
			offset := 0
			for k := 0; k < start; k++ {
				offset += len(string(runes[k]))
			}
			out = append(out, offset)
		}
	}
	return out
}

// sqlServerFirstTopLevelKeyword 返回首个括号外的关键字位置（-1 表示没有）。
func sqlServerFirstTopLevelKeyword(clean, keyword string) int {
	pos := sqlServerTopLevelKeywordPositions(clean, keyword)
	if len(pos) == 0 {
		return -1
	}
	return pos[0]
}

// sqlServerLastTopLevelKeyword 找最后一个括号外的关键字位置（-1 表示没有）。
//
// 取「最后一个」而不是第一个：UPDATE 的 WHERE 一定在 SET/FROM 之后，
// 子查询里的 WHERE 在括号内会被跳过。
func sqlServerLastTopLevelKeyword(clean, keyword string) int {
	pos := sqlServerTopLevelKeywordPositions(clean, keyword)
	if len(pos) == 0 {
		return -1
	}
	return pos[len(pos)-1]
}

// sqlServerJoinTableCount 统计 UPDATE/DELETE 参与操作的表数量（含主表）。
//
// 不能复用 pgDMLJoinTableCount：T-SQL 没有 `UPDATE a JOIN b ON ... SET` 的写法，
// 多表更新一律写成 `UPDATE 别名 SET ... FROM 主表 别名 JOIN 从表 ON ...`，
// 也就是 JOIN 出现在 SET 之后，而 PG 版本只统计 SET 之前的片段。
func sqlServerJoinTableCount(clean string) int {
	fromIdx := sqlServerLastTopLevelKeyword(clean, "from")
	if fromIdx < 0 {
		return 1
	}
	seg := clean[fromIdx+len("from"):]
	for _, kw := range []string{"where", "order", "group", "having", "option", "for", "output"} {
		if i := sqlServerFirstTopLevelKeyword(seg, kw); i >= 0 {
			seg = seg[:i]
		}
	}
	count := 1 + len(sqlServerTopLevelKeywordPositions(seg, "join"))
	if parts := splitSQLServerTopLevel(seg, ','); len(parts) > 1 {
		count += len(parts) - 1
	}
	return count
}

// sqlServerSetColumn UPDATE 的 SET 目标列
type sqlServerSetColumn struct {
	// Raw 去掉引用符号后的原始写法（可能带表前缀，用于提示文案）
	Raw string
	// Bare 去掉表前缀的列名
	Bare string
	// Prefixed 是否带表前缀（多表 UPDATE 必须带）
	Prefixed bool
}

// sqlServerUpdateSetColumns 提取 UPDATE 的 SET 目标列。
//
// 与 pgUpdateSetColumns 的差异：这里保留表前缀（多表 UPDATE 前缀校验需要它），
// 且按 T-SQL 的子句顺序截断到 FROM/WHERE/OUTPUT/OPTION。
func sqlServerUpdateSetColumns(clean string) []sqlServerSetColumn {
	setIdx := sqlServerFirstTopLevelKeyword(clean, "set")
	if setIdx < 0 {
		return nil
	}
	seg := clean[setIdx+len("set"):]
	for _, kw := range []string{"from", "where", "output", "option"} {
		if i := sqlServerFirstTopLevelKeyword(seg, kw); i >= 0 {
			seg = seg[:i]
		}
	}
	var out []sqlServerSetColumn
	for _, item := range splitSQLServerTopLevel(seg, ',') {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		left := item
		if i := strings.Index(left, "="); i >= 0 {
			left = strings.TrimSpace(left[:i])
		}
		if left == "" {
			continue
		}
		parts := splitSQLServerNameParts(left)
		cleaned := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = unquoteSQLServerIdent(p); p != "" {
				cleaned = append(cleaned, p)
			}
		}
		if len(cleaned) == 0 {
			continue
		}
		out = append(out, sqlServerSetColumn{
			Raw:      strings.Join(cleaned, "."),
			Bare:     strings.ToLower(cleaned[len(cleaned)-1]),
			Prefixed: len(cleaned) > 1,
		})
	}
	return out
}

// sqlServerWhereClause 取 WHERE 之后的原始条件文本（截断尾部非 WHERE 子句）。
func sqlServerWhereClause(stmt, clean string) string {
	idx := sqlServerLastTopLevelKeyword(clean, "where")
	if idx < 0 || idx >= len(stmt) {
		return ""
	}
	where := stmt[idx+len("where"):]
	// 尾部子句截断（ORDER BY / OPTION / FOR UPDATE）
	whereClean := stripPGStringsAndComments(where)
	cut := len(where)
	for _, kw := range []string{"order by", "option", "for update", "for xml", "for json"} {
		if i := indexOfKeyword(whereClean, kw); i >= 0 && i < cut {
			cut = i
		}
	}
	where = where[:cut]
	where = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(where), ";"))
	return where
}

// sqlServerInsertColumns 取 INSERT 的显式字段列表
func sqlServerInsertColumns(clean string) []string {
	lower := strings.ToLower(clean)
	idx := indexOfKeyword(lower, "into")
	if idx < 0 {
		return nil
	}
	rest := strings.TrimSpace(clean[idx+len("into"):])
	token := firstSQLServerToken(rest)
	if token == "" {
		return nil
	}
	rest = strings.TrimSpace(rest[len(token):])
	if !strings.HasPrefix(rest, "(") {
		return nil
	}
	open, closeIdx := findTopLevelParen(rest)
	if open < 0 || closeIdx <= open {
		return nil
	}
	return extractIndexKeyColumns(rest[open+1 : closeIdx])
}

// sqlServerInsertNullColumns 取出「显式插入 NULL」的字段（用于 NOT NULL 列校验）。
// 判定口径：第一行 VALUES 中值为 NULL 的位置，映射回字段列表。
func sqlServerInsertNullColumns(clean string, cols []string) []string {
	if len(cols) == 0 {
		return nil
	}
	lower := strings.ToLower(clean)
	idx := indexOfKeyword(lower, "values")
	if idx < 0 {
		return nil
	}
	rest := strings.TrimSpace(clean[idx+len("values"):])
	open, closeIdx := findTopLevelParen(rest)
	if open < 0 || closeIdx <= open {
		return nil
	}
	items := splitSQLServerTopLevel(rest[open+1:closeIdx], ',')
	var out []string
	for i, item := range items {
		if i >= len(cols) {
			break
		}
		if strings.EqualFold(strings.TrimSpace(item), "null") {
			out = append(out, cols[i])
		}
	}
	return out
}

// sqlServerInsertUsesVolatileTime INSERT 的 VALUES 里是否用了易变时间函数
func sqlServerInsertUsesVolatileTime(clean string) bool {
	lower := strings.ToLower(clean)
	idx := indexOfKeyword(lower, "values")
	if idx < 0 {
		return false
	}
	values := lower[idx:]
	for _, fn := range []string{"getdate(", "getdate (", "sysdatetime(", "sysdatetime (",
		"getutcdate(", "getutcdate (", "current_timestamp", "sysutcdatetime("} {
		if strings.Contains(values, fn) {
			return true
		}
	}
	return false
}

var (
	// like '%xxx' 前通配符
	sqlServerLeadingWildcardRe = regexp.MustCompile(`(?is)\blike\s+(?:N)?'%`)
	// like 'xxx' 无任何通配符
	sqlServerLikeNoWildcardRe = regexp.MustCompile(`(?is)\blike\s+(?:N)?'([^'%_]*)'`)
)

// sqlServerHasLeadingWildcard WHERE 中 LIKE 是否用了前通配符
func sqlServerHasLeadingWildcard(where string) bool {
	return sqlServerLeadingWildcardRe.MatchString(where)
}

// sqlServerHasLikeWithoutWildcard WHERE 中 LIKE 是否缺少通配符
func sqlServerHasLikeWithoutWildcard(where string) bool {
	return sqlServerLikeNoWildcardRe.MatchString(where)
}

// sqlServerHasColumnCalc 判断 WHERE 中该列是否被函数包裹或参与数学运算
func sqlServerHasColumnCalc(whereClean, col string) bool {
	quoted := regexp.QuoteMeta(col)
	// 函数包裹：func(col) / func( col , ...
	fnRe := regexp.MustCompile(`(?is)\b[a-z_][a-z0-9_]*\s*\(\s*\[?` + quoted + `\]?\s*[,)]`)
	if fnRe.MatchString(whereClean) {
		return true
	}
	// 参与算术：col + 1 / col * 2 / col - 1 / col / 2
	calcRe := regexp.MustCompile(`(?is)\b\[?` + quoted + `\]?\s*[+\-*/]`)
	return calcRe.MatchString(whereClean)
}

// sqlServerHasImplicitCast 判断 WHERE 中该列是否存在隐式类型转换。
//
// 两种典型场景（SQL Server 会把低优先级类型转成高优先级，导致索引失效）：
//   - 字符型列与数值字面量比较：`code = 123`（code 是 VARCHAR）
//   - 数值/日期型列与字符串字面量比较：`amount = '123'`
//
// 判定在原始文本上做——字符串内容在 strip 后会被抹掉，无法区分 `'123'` 与 `123`。
func sqlServerHasImplicitCast(where, col, colType string) bool {
	quoted := regexp.QuoteMeta(col)
	lhs := `(?is)\b\[?` + quoted + `\]?\s*(?:=|<>|!=|>=|<=|>|<)\s*`
	switch {
	case isSQLServerCharacterType(colType):
		return regexp.MustCompile(lhs + `\d`).MatchString(where)
	case isSQLServerNumericType(colType):
		return regexp.MustCompile(lhs + `(?:N)?'`).MatchString(where)
	}
	return false
}

func isSQLServerCharacterType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "char", "varchar", "nchar", "nvarchar", "text", "ntext", "sysname":
		return true
	}
	return false
}

func isSQLServerNumericType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "tinyint", "smallint", "int", "bigint", "bit",
		"decimal", "numeric", "money", "smallmoney", "float", "real":
		return true
	}
	return false
}
