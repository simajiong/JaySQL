package checker

import (
	"regexp"
	"strings"
)

var (
	pgIfNotExistsRe = regexp.MustCompile(`(?is)^\s*if\s+not\s+exists\s+`)
	pgIfExistsRe    = regexp.MustCompile(`(?is)^\s*if\s+exists\s+`)
	pgNamePattern   = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	// pgLimitRowsRe 提取 UPDATE/DELETE 语句 LIMIT 的常量行数
	pgLimitRowsRe = regexp.MustCompile(`(?i)\blimit\s+(\d+)`)
	// 列名要求大写时的正则（规则 limit_col_name_case 的 name_case=upper）
	pgUpperNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// stripPGStringsAndComments 把字符串字面量与注释替换为等长空格，
// 便于用关键字定位（避免字符串里出现 where / join 等词造成误判）。
func stripPGStringsAndComments(s string) string {
	out := []byte(s)
	inSingle, inDouble, inLine, inBlock := false, false, false, false
	for i := 0; i < len(out); i++ {
		c := out[i]
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
			} else {
				out[i] = ' '
			}
			continue
		case inBlock:
			if c == '*' && i+1 < len(out) && out[i+1] == '/' {
				out[i], out[i+1] = ' ', ' '
				i++
				inBlock = false
				continue
			}
			out[i] = ' '
			continue
		case inSingle:
			if c == '\'' {
				if i+1 < len(out) && out[i+1] == '\'' {
					out[i], out[i+1] = ' ', ' '
					i++
					continue
				}
				inSingle = false
				continue
			}
			out[i] = ' '
			continue
		case inDouble:
			if c == '"' {
				inDouble = false
				continue
			}
			// 标识符内容保留（表名可能是 "Order"）
			continue
		}
		switch c {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '-':
			if i+1 < len(out) && out[i+1] == '-' {
				inLine = true
				out[i], out[i+1] = ' ', ' '
				i++
			}
		case '/':
			if i+1 < len(out) && out[i+1] == '*' {
				inBlock = true
				out[i], out[i+1] = ' ', ' '
				i++
			}
		}
	}
	return string(out)
}

// pgDMLAnalysis DML 语句的文本级结构分析
type pgDMLAnalysis struct {
	Kind     string // insert / update / delete
	Table    string
	// TableSchema 语句显式指定的 schema（如 update sch.t；未限定为空，走工单 InspectSchema）
	TableSchema string
	HasWhere      bool
	WhereSubquery   bool
	JoinMissingOn   bool
	InsertValueRows int
	// InsertExplicitCols INSERT 显式字段列表的列数（未指定为 0；规则 insert_stmt_columns_match_values）
	InsertExplicitCols int
	// InsertValueCols 每行 VALUES 的值个数（与 InsertValueRows 等长；INSERT ... SELECT 为 nil）
	InsertValueCols []int
	// JoinTableCount UPDATE/DELETE 参与操作的表数量（含主表；USING/JOIN 各计 1）
	JoinTableCount int
	// LimitRows UPDATE/DELETE 语句 LIMIT 的常量行数（无 LIMIT 或无法解析时为 0）
	LimitRows int
	// WhereClause UPDATE/DELETE 的 WHERE 条件原文（到 order by / limit / returning 为止；
	// 供预审阶段做影响行数估算，规则 limit_update_delete_affected_rows）。无 WHERE 时为空。
	WhereClause string
	// UpdateSetColumns UPDATE 语句 SET 子句的列名（小写）
	UpdateSetColumns []string
	// MaxInElements WHERE 条件中 IN 列表的最大元素个数（无 IN 时为 0）
	MaxInElements int
	OK            bool
}

// analyzePGDML 解析 UPDATE/DELETE/INSERT 的关键结构（不解析表达式）
func analyzePGDML(stmt string) pgDMLAnalysis {
	res := pgDMLAnalysis{}
	clean := stripPGStringsAndComments(stmt)
	lower := strings.ToLower(clean)
	trimmed := strings.TrimSpace(lower)

	var tablePart string
	switch {
	case strings.HasPrefix(trimmed, "update"):
		res.Kind = "update"
		tablePart = strings.TrimSpace(clean[len("update"):])
	case strings.HasPrefix(trimmed, "delete"):
		res.Kind = "delete"
		rest := strings.TrimSpace(clean[len("delete"):])
		if !strings.HasPrefix(strings.ToLower(rest), "from") {
			return res
		}
		tablePart = strings.TrimSpace(rest[len("from"):])
	case strings.HasPrefix(trimmed, "insert"):
		res.Kind = "insert"
		rest := strings.TrimSpace(clean[len("insert"):])
		if !strings.HasPrefix(strings.ToLower(rest), "into") {
			return res
		}
		tablePart = strings.TrimSpace(rest[len("into"):])
	default:
		return res
	}

	// 表名：取到第一个空白或左括号为止（去掉 ONLY / schema）
	fields := strings.Fields(tablePart)
	if len(fields) == 0 {
		return res
	}
	nameToken := fields[0]
	if strings.EqualFold(nameToken, "only") && len(fields) > 1 {
		nameToken = fields[1]
	}
	if i := strings.IndexByte(nameToken, '('); i > 0 {
		nameToken = nameToken[:i]
	}
	nameToken = strings.Trim(nameToken, `"`)
	if id, ok := parsePGQualifiedName(nameToken); ok {
		res.Table = id.Name
		res.TableSchema = id.Schema
	}

	whereIdx := indexOfKeyword(clean, "where")
	res.HasWhere = whereIdx >= 0
	if res.HasWhere {
		// WHERE 条件原文：到 order by / limit / returning / for update 为止
		// （COUNT 用；供预审阶段做影响行数估算）
		after := clean[whereIdx+len("where"):]
		for _, tail := range []string{"order by", "limit", "returning", "for update"} {
			if i := indexOfKeyword(after, tail); i >= 0 {
				after = after[:i]
			}
		}
		res.WhereClause = strings.TrimSpace(after)
	}

	if res.HasWhere {
		after := clean[whereIdx+len("where"):]
		lowerAfter := strings.ToLower(after)
		if strings.Contains(lowerAfter, "(select") || strings.Contains(lowerAfter, "( select") ||
			strings.Contains(lowerAfter, "(select ") {
			res.WhereSubquery = true
		}
	}

	if res.Kind != "insert" {
		res.JoinMissingOn = pgJoinMissingOn(clean)
		res.JoinTableCount = pgDMLJoinTableCount(clean, res.Kind)
		res.LimitRows = pgDMLLimitRows(clean)
		res.MaxInElements = pgWhereInMaxElements(clean)
		if res.Kind == "update" {
			res.UpdateSetColumns = pgUpdateSetColumns(clean)
		}
	} else {
		res.InsertValueRows = pgInsertValueRows(clean)
		res.InsertExplicitCols, res.InsertValueCols = pgInsertShapes(clean)
	}
	res.OK = true
	return res
}

// pgJoinMissingOn 判断是否存在缺少 ON 子句的 JOIN
func pgJoinMissingOn(clean string) bool {
	tokens := pgWordTokens(strings.ToLower(clean))
	for i := 0; i < len(tokens); i++ {
		if tokens[i] != "join" {
			continue
		}
		// 向后找 on；遇到 where/group/order/limit/returning/on 之前先遇到另一个 join 视为缺 ON
		foundOn := false
		for j := i + 1; j < len(tokens); j++ {
			switch tokens[j] {
			case "on":
				foundOn = true
			case "where", "group", "order", "limit", "having", "returning", "union", "join":
				// 到达下一子句仍未遇到 ON
				j = len(tokens)
			}
			if foundOn {
				break
			}
		}
		if !foundOn {
			return true
		}
	}
	return false
}

// pgInsertValueRows 统计 INSERT ... VALUES 的顶层括号组数量（一行值 = 一组括号）
func pgInsertValueRows(clean string) int {
	idx := indexOfKeyword(clean, "values")
	if idx < 0 {
		return 0
	}
	rest := clean[idx+len("values"):]
	depth := 0
	rows := 0
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '(':
			depth++
			if depth == 1 {
				rows++
			}
		case ')':
			if depth > 0 {
				depth--
			}
			if depth == 0 {
				// 一组值结束：后面若不是逗号则结束解析
				tail := strings.TrimSpace(rest[i+1:])
				if !strings.HasPrefix(tail, ",") {
					return rows
				}
			}
		}
	}
	return rows
}

// pgWordTokens 取词序列（小写），忽略标点
func pgWordTokens(s string) []string {
	var tokens []string
	var cur strings.Builder
	for _, r := range s {
		if isPGWordRune(r) {
			cur.WriteRune(r)
			continue
		}
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

// pgDMLJoinTableCount 统计 UPDATE/DELETE 参与操作的表数量（含主表）。
// UPDATE：主表 + 表名到 SET 之间的 join 数 + PG 专有的 `SET ... FROM t2[, t3]` 表数；
// DELETE：from 表 + using/join 数。
// 仅做文本级统计，跨子查询场景按保守口径计数（FROM 出现在括号内时不计）。
func pgDMLJoinTableCount(clean, kind string) int {
	lower := strings.ToLower(clean)
	var span string
	switch kind {
	case "update":
		start := indexOfKeyword(clean, strings.TrimSpace(kind)) // update 关键字之后
		setIdx := strings.Index(lower[start:], " set ")
		if setIdx < 0 {
			setIdx = len(lower) - start
		}
		span = lower[start : start+setIdx]
	case "delete":
		start := strings.Index(lower, " from ")
		if start < 0 {
			return 1
		}
		end := len(lower)
		for _, kw := range []string{" where ", " limit ", " returning "} {
			if i := strings.Index(lower[start:], kw); i >= 0 && start+i < end {
				end = start + i
			}
		}
		span = lower[start:end]
	default:
		return 1
	}
	count := 1
	for _, kw := range []string{" join ", " using ", ","} {
		count += strings.Count(span, kw)
	}
	if kind == "update" {
		// PG 的多表 UPDATE 写成 `UPDATE t SET ... FROM t2, t3 WHERE ...`：
		// 主表之后还有 FROM 子句，其表数必须计入，否则「多表关联」类规则整体失效。
		if fromIdx := pgTopLevelFromIndex(clean); fromIdx >= 0 {
			seg := lower[fromIdx+len("from"):]
			for _, kw := range []string{" where ", " limit ", " returning ", " order by "} {
				if i := strings.Index(seg, kw); i >= 0 {
					seg = seg[:i]
				}
			}
			add := 1 + strings.Count(seg, ",") + strings.Count(seg, " join ")
			count += add
		}
	}
	return count
}

// pgTopLevelFromIndex 返回括号外第一个 `from` 关键字的起始下标（找不到返回 -1）。
// 用于区分 `UPDATE ... SET ... FROM t2` 与 `SET x = (SELECT ... FROM y)`。
func pgTopLevelFromIndex(clean string) int {
	lower := strings.ToLower(clean)
	depth := 0
	for i := 0; i+4 <= len(lower); i++ {
		switch lower[i] {
		case '(':
			depth++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 || lower[i:i+4] != "from" {
			continue
		}
		leftOK := i == 0 || !isPGWordByte(lower[i-1])
		rightOK := i+4 == len(lower) || !isPGWordByte(lower[i+4])
		if leftOK && rightOK {
			return i
		}
	}
	return -1
}

// pgDMLLimitRows 提取 UPDATE/DELETE 语句 LIMIT 的常量行数（无 LIMIT 或非纯数字时为 0）
func pgDMLLimitRows(clean string) int {
	m := pgLimitRowsRe.FindStringSubmatch(clean)
	if m == nil {
		return 0
	}
	n := 0
	for _, c := range m[1] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
		if n > 1<<30 {
			return 0
		}
	}
	return n
}

// pgUpdateSetColumns 提取 UPDATE 语句 SET 子句的列名（小写、去表前缀）
func pgUpdateSetColumns(clean string) []string {
	lower := strings.ToLower(clean)
	setIdx := strings.Index(lower, " set ")
	if setIdx < 0 {
		return nil
	}
	rest := clean[setIdx+len(" set "):]
	end := len(rest)
	for _, kw := range []string{" where ", " limit ", " returning "} {
		if i := strings.Index(strings.ToLower(rest), kw); i >= 0 && i < end {
			end = i
		}
	}
	var cols []string
	depth := 0
	var cur strings.Builder
	flush := func() {
		item := strings.TrimSpace(cur.String())
		cur.Reset()
		if item == "" {
			return
		}
		// 取 = 左侧部分作为列
		if i := strings.Index(item, "="); i >= 0 {
			item = strings.TrimSpace(item[:i])
		}
		item = strings.Trim(item, "\"`")
		if i := strings.LastIndex(item, "."); i >= 0 {
			item = item[i+1:]
		}
		if item != "" {
			cols = append(cols, strings.ToLower(item))
		}
	}
	for i := 0; i < len(rest[:end]); i++ {
		switch rest[i] {
		case '(':
			depth++
			cur.WriteByte(rest[i])
		case ')':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(rest[i])
		case ',':
			if depth == 0 {
				flush()
				continue
			}
			cur.WriteByte(rest[i])
		default:
			cur.WriteByte(rest[i])
		}
	}
	flush()
	return cols
}

// pgWhereInMaxElements 统计 WHERE 条件中 IN (…) 列表的最大元素个数（顶层逗号 + 1）。
// 文本级口径：定位 where 之后每个 `in (`，取匹配括号内顶层逗号数最大值。
func pgWhereInMaxElements(clean string) int {
	lower := strings.ToLower(clean)
	whereIdx := indexOfKeyword(clean, "where")
	if whereIdx < 0 {
		return 0
	}
	seg := lower[whereIdx:]
	orig := clean[whereIdx:]
	max := 0
	for i := 0; i+4 <= len(seg); i++ {
		if seg[i:i+4] != " in " {
			continue
		}
		// 找 in 后的第一个 (
		j := i + 4
		for j < len(seg) && seg[j] != '(' && seg[j] != ';' {
			j++
		}
		if j >= len(seg) || seg[j] != '(' {
			continue
		}
		depth := 0
		elems := 1
		// seg 与 orig 同源于 whereIdx 起的切片，索引天然对齐
		for k := j; k < len(orig); k++ {
			switch orig[k] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					if elems > max {
						max = elems
					}
					i = k
					k = len(orig)
				}
			case ',':
				if depth == 1 {
					elems++
				}
			}
		}
	}
	return max
}

// pgInsertShapes 取 INSERT 语句的显式字段列表列数与每行 VALUES 的值个数。
//
// 入参是 stripPGStringsAndComments 处理后的文本：字符串字面量与注释已被替换为空格，
// 括号扫描无需处理引号转义。未指定字段列表时 cols 为 0；INSERT ... SELECT 没有
// VALUES 行时 sizes 为 nil —— 两者都无法静态判定值个数，由调用方跳过。
func pgInsertShapes(clean string) (cols int, sizes []int) {
	valIdx := indexOfKeyword(clean, "values")
	if valIdx < 0 {
		return 0, nil
	}
	// ① 字段列表：insert into <表名> 之后、values 之前的第一对括号
	head := clean[:valIdx]
	lowerHead := strings.ToLower(head)
	if intoIdx := strings.Index(lowerHead, "into"); intoIdx >= 0 {
		i := intoIdx + len("into")
		// 跳过表名 token（可能带 schema 点与双引号）
		for i < len(head) && head[i] != '(' && head[i] != ' ' && head[i] != '\t' && head[i] != '\n' && head[i] != '\r' {
			i++
		}
		for i < len(head) && (head[i] == ' ' || head[i] == '\t' || head[i] == '\n' || head[i] == '\r') {
			i++
		}
		if i < len(head) && head[i] == '(' {
			if closeIdx := pgMatchingParen(head, i); closeIdx > 0 {
				cols = pgTopLevelItemCount(head[i+1 : closeIdx])
			}
		}
	}
	// ② VALUES 行：顶层括号组逐个取值个数，组间以逗号分隔；非 '(' 开头即结束
	rest := clean[valIdx+len("values"):]
	for i := 0; i < len(rest); {
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t' || rest[i] == '\n' || rest[i] == '\r') {
			i++
		}
		if i >= len(rest) || rest[i] != '(' {
			break
		}
		closeIdx := pgMatchingParen(rest, i)
		if closeIdx < 0 {
			break
		}
		sizes = append(sizes, pgTopLevelItemCount(rest[i+1:closeIdx]))
		i = closeIdx + 1
		// 行间分隔符：允许一个逗号；遇到其它字符（on conflict / returning 等）即结束
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t' || rest[i] == '\n' || rest[i] == '\r') {
			i++
		}
		if i < len(rest) && rest[i] == ',' {
			i++
		} else {
			break
		}
	}
	return cols, sizes
}

// pgMatchingParen 返回与 s[open] 配平的 ')' 下标，无配平时返回 -1
func pgMatchingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// pgTopLevelItemCount 统计括号内顶层元素个数（按顶层逗号切分；空内容为 0）
func pgTopLevelItemCount(s string) int {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	n := 1
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				n++
			}
		}
	}
	return n
}
