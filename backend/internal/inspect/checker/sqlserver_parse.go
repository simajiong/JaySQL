package checker

// SQL Server 引擎的解析层
//
// 与 PostgreSQL 引擎同样的取舍：不使用 TiDB AST（T-SQL 方言完全不兼容），
// 而是在「语法感知拆分」（pkg/sqlsplit）之上做轻量文本解析，抽取表/列/索引/约束信息。
// 复用 pg_parse.go 里的通用文本工具（findTopLevelParen / extractIndexKeyColumns /
// stripPGStringsAndComments），只把「SQL Server 方言特有的部分」重写：
//
//	T-SQL 语法                         与 PG / MySQL 的差异
//	[name] / [schema].[name]           方括号引用；默认 schema 是 dbo 而不是 current_schema
//	IDENTITY(seed, increment)          自增列；没有 AUTO_INCREMENT，也没有 UNSIGNED
//	NVARCHAR(n) / VARCHAR(MAX)         长度按字符计；MAX 是无上限，不能当超长数字比
//	COLLATE Latin1_General_CI_AS       列级排序规则（没有字符集概念）
//	PK CLUSTERED / NONCLUSTERED        主键/索引带聚集属性，默认聚集
//	SELECT ... INTO t                  CTAS 的 T-SQL 写法（不是 CREATE TABLE ... AS）
//	TOP (n)                            没有 LIMIT，UPDATE/DELETE 用 TOP 限量
//	EXEC sp_addextendedproperty        注释（没有内联 COMMENT，也没有 COMMENT ON）
//	EXEC sp_rename                     改名（没有 ALTER TABLE ... RENAME COLUMN）
//	GO                                 批处理分隔符（不是语句，需先剥离）

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/simajiong/jaysql/pkg/sqlsplit"
)

// sqlServerNamedArgRe 匹配扩展属性语句的命名参数：@level0name=N'dbo'
// （N 前缀是可选的 Unicode 字面量标记，值里的单引号以 ” 转义）
var sqlServerNamedArgRe = regexp.MustCompile(`(?is)@level([0-9])name\s*=\s*N?'((?:[^']|'')*)'`)

// sqlServerDefaultSchema SQL Server 未限定 schema 时的默认值
const sqlServerDefaultSchema = "dbo"

// sqlServerExtraDDLPrefixes 按 DDL 归类的前缀：EXEC 系列是对象运维动作
// （sp_addextendedproperty / sp_rename / sp_help 等），不属于 DML/EXPORT，
// 归到 DDL 与「结构变更」的语义一致。实现统一下沉到 pkg/sqlsplit，
// 与工单提交链路（orders/services/order_plan）共用同口径。
var sqlServerExtraDDLPrefixes = sqlsplit.TSQLExtraDDLPrefixes

// sqlServerName 三段式对象名：database.schema.object（T-SQL 还允许 server.db.schema.object）
type sqlServerName struct {
	Database string
	Schema   string
	Object   string
}

// EffectiveSchema 返回实际使用的 schema：未限定时为 dbo。
func (n sqlServerName) EffectiveSchema() string {
	if s := strings.TrimSpace(n.Schema); s != "" {
		return s
	}
	return sqlServerDefaultSchema
}

// Display 返回用于提示文案的对象名（表名本身，不带 schema）。
func (n sqlServerName) Display() string {
	return n.Object
}

// Key 返回大小写不敏感的比较键（SQL Server 默认排序规则下标识符不区分大小写）。
func (n sqlServerName) Key() string {
	return strings.ToLower(strings.TrimSpace(n.Object))
}

// trimSQLServerStmtTail 去掉语句末尾的空白与分号。
//
// sqlsplit.Split 会保留语句结尾的 `;`，而 `ALTER TABLE t DROP COLUMN c;` 这类语句在
// 按逗号/空白切分后会把 `;` 粘到最后一个标识符上（`c;`），导致后续「列是否存在」
// 之类的比对全部落空。解析入口统一剥一次即可。
func trimSQLServerStmtTail(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasSuffix(s, ";") {
		s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	}
	return s
}

// unquoteSQLServerIdent 去掉标识符的引用符号：[] / "" / “ 与尾部分号。
func unquoteSQLServerIdent(s string) string {
	s = trimSQLServerStmtTail(s)
	for {
		switch {
		case len(s) >= 2 && strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"):
			s = strings.TrimSpace(s[1 : len(s)-1])
		case len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`):
			s = strings.TrimSpace(s[1 : len(s)-1])
		case len(s) >= 2 && strings.HasPrefix(s, "`") && strings.HasSuffix(s, "`"):
			s = strings.TrimSpace(s[1 : len(s)-1])
		default:
			return s
		}
	}
}

// sqlServerKey 归一化为比较键：去引用符号 + 小写。
func sqlServerKey(s string) string {
	return strings.ToLower(unquoteSQLServerIdent(s))
}

// sqlServerHasWordPrefix 判断 s 是否以完整单词 kw 开头（kw 必须小写）。
//
// 用 HasPrefix 直接匹配会把 `unique_idx` 这类合法索引名当成 `UNIQUE` 关键字吃掉，
// 因此要求关键字后面紧跟非单词字符或字符串结束。
func sqlServerHasWordPrefix(s, kw string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if !strings.HasPrefix(s, kw) {
		return false
	}
	if len(s) == len(kw) {
		return true
	}
	return !isSQLServerWordRune(rune(s[len(kw)]))
}

// splitSQLServerNameParts 按「括号外」的点号拆分对象名。
func splitSQLServerNameParts(s string) []string {
	var parts []string
	var cur strings.Builder
	depth := 0
	inBracket, inDouble := false, false
	for _, r := range s {
		switch {
		case inBracket:
			if r == ']' {
				inBracket = false
				continue
			}
			cur.WriteRune(r)
			continue
		case inDouble:
			if r == '"' {
				inDouble = false
				continue
			}
			cur.WriteRune(r)
			continue
		}
		switch r {
		case '[':
			inBracket = true
		case '"':
			inDouble = true
		case '(':
			depth++
			cur.WriteRune(r)
		case ')':
			if depth > 0 {
				depth--
			}
			cur.WriteRune(r)
		case '.':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(cur.String()))
				cur.Reset()
				continue
			}
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	parts = append(parts, strings.TrimSpace(cur.String()))
	return parts
}

// parseSQLServerName 解析 [db.][schema.]object 形式的对象名。
func parseSQLServerName(s string) (sqlServerName, bool) {
	parts := splitSQLServerNameParts(strings.TrimSpace(s))
	// 去掉空段（如尾部多一个点号）
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			cleaned = append(cleaned, p)
		}
	}
	var name sqlServerName
	switch len(cleaned) {
	case 0:
		return name, false
	case 1:
		name.Object = cleaned[0]
	case 2:
		name.Schema, name.Object = cleaned[0], cleaned[1]
	case 3:
		name.Database, name.Schema, name.Object = cleaned[0], cleaned[1], cleaned[2]
	default: // server.database.schema.object：取末三段
		name.Database, name.Schema, name.Object = cleaned[len(cleaned)-3], cleaned[len(cleaned)-2], cleaned[len(cleaned)-1]
	}
	if strings.TrimSpace(name.Object) == "" {
		return name, false
	}
	return name, true
}

// splitSQLServerTopLevel 按顶层分隔符拆分（括号 / 单引号 / 双引号 / 方括号感知）。
//
// 在 pg 版 splitTopLevel 的基础上补了方括号：T-SQL 的 `[Order Date]`、`[a,b]`
// 是完全合法的标识符写法，方括号内的逗号不能当分隔符。
func splitSQLServerTopLevel(s string, sep byte) []string {
	var parts []string
	var cur strings.Builder
	depth := 0
	inSingle, inDouble, inBracket := false, false, false
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case inSingle:
			cur.WriteRune(c)
			if c == '\'' {
				if i+1 < len(runes) && runes[i+1] == '\'' {
					cur.WriteRune('\'')
					i++
					continue
				}
				inSingle = false
			}
			continue
		case inDouble:
			cur.WriteRune(c)
			if c == '"' {
				inDouble = false
			}
			continue
		case inBracket:
			if c == ']' {
				inBracket = false
				continue
			}
			cur.WriteRune(c)
			continue
		}
		switch c {
		case '\'':
			inSingle = true
			cur.WriteRune(c)
		case '"':
			inDouble = true
			cur.WriteRune(c)
		case '[':
			inBracket = true
		case '(':
			depth++
			cur.WriteRune(c)
		case ')':
			if depth > 0 {
				depth--
			}
			cur.WriteRune(c)
		default:
			if byte(c) == sep && depth == 0 {
				parts = append(parts, cur.String())
				cur.Reset()
				continue
			}
			cur.WriteRune(c)
		}
	}
	parts = append(parts, cur.String())
	return parts
}

// sqlServerColumn SQL Server 列定义
type sqlServerColumn struct {
	Name       string
	Type       string // 规范化基础类型名（小写，去括号与长度）
	Len        int    // char/varchar/nchar/nvarchar/binary/varbinary 的 n；0=未指定
	IsMax      bool   // VARCHAR(MAX) / NVARCHAR(MAX) / VARBINARY(MAX)
	NotNull    bool
	HasDefault bool
	IsIdentity bool
	// IdentitySeed IDENTITY(seed, increment) 的 seed（无显式 seed 时为 1）
	IdentitySeed int
	HasCollate   bool
	CollateName  string
	IsComputed   bool // AS (...) 计算列，不参与类型/默认值/注释类规则
	HasPK        bool
	HasUnique    bool
	HasFK        bool
	Raw          string
}

// sqlServerIndex SQL Server 索引/唯一约束定义
type sqlServerIndex struct {
	Name      string
	Table     sqlServerName
	Columns   []string
	Unique    bool
	Clustered bool
	Explicit  bool
	Raw       string
}

// sqlServerTable CREATE TABLE 的解析结果
type sqlServerTable struct {
	Name          sqlServerName
	Columns       []sqlServerColumn
	PrimaryKeys   []string
	PKName        string
	PKClustered   bool
	UniqueKeys    [][]string
	UniqueNames   []string
	UniqueIndexes []*sqlServerIndex
	HasForeignKey bool
	Raw           string
}

// splitSQLServerTypeLen 拆类型名与长度：VARCHAR(50) -> ("varchar",50,false)；
// NVARCHAR(MAX) -> ("nvarchar",0,true)；DECIMAL(10,2) -> ("decimal",10,false)。
func splitSQLServerTypeLen(t string) (base string, length int, isMax bool) {
	t = strings.ToLower(strings.TrimSpace(unquoteSQLServerIdent(t)))
	if i := strings.IndexByte(t, '('); i > 0 {
		base = strings.TrimSpace(t[:i])
		inner := strings.TrimSpace(strings.TrimSuffix(t[i+1:], ")"))
		if j := strings.IndexByte(inner, ','); j >= 0 {
			inner = strings.TrimSpace(inner[:j])
		}
		if strings.EqualFold(inner, "max") {
			return base, 0, true
		}
		n, err := strconv.Atoi(inner)
		if err != nil {
			return base, 0, false
		}
		return base, n, false
	}
	return strings.TrimSpace(t), 0, false
}

// parseSQLServerColumnDef 解析单列定义。
func parseSQLServerColumnDef(item string) (sqlServerColumn, bool) {
	col := sqlServerColumn{Raw: item}
	text := strings.TrimSpace(item)
	if text == "" {
		return col, false
	}
	// 列名：方括号 / 双引号 / 裸标识符
	rest := text
	if strings.HasPrefix(text, "[") {
		end := strings.IndexByte(text, ']')
		if end < 0 {
			return col, false
		}
		col.Name = strings.TrimSpace(text[1:end])
		rest = strings.TrimSpace(text[end+1:])
	} else if strings.HasPrefix(text, `"`) {
		end := strings.IndexByte(text[1:], '"')
		if end < 0 {
			return col, false
		}
		col.Name = strings.TrimSpace(text[1 : end+1])
		rest = strings.TrimSpace(text[end+2:])
	} else {
		fields := strings.Fields(text)
		if len(fields) < 2 {
			return col, false
		}
		col.Name = fields[0]
		rest = strings.TrimSpace(text[len(fields[0]):])
	}
	if col.Name == "" || strings.ContainsAny(col.Name, "()") {
		return col, false
	}
	upper := strings.ToUpper(rest)

	// 计算列：name AS (表达式) —— 没有类型声明
	if strings.HasPrefix(upper, "AS ") || strings.HasPrefix(upper, "AS(") {
		col.IsComputed = true
		return col, true
	}

	// 类型：第一个空白/括号前的 token（可能带 (n,m) 后缀，可能被方括号包起来）
	col.Type, col.Len, col.IsMax = splitSQLServerTypeLen(firstSQLServerToken(rest))
	if col.Type == "" {
		return col, false
	}

	col.NotNull = strings.Contains(upper, "NOT NULL")
	col.HasDefault = strings.Contains(upper, " DEFAULT ") || strings.HasPrefix(upper, "DEFAULT ")
	col.IsIdentity = strings.Contains(upper, "IDENTITY")
	col.IdentitySeed = 1
	if col.IsIdentity {
		col.HasDefault = true // 自增列天然有值，不要求再声明默认值
		if seed, ok := sqlServerIdentitySeed(upper); ok {
			col.IdentitySeed = seed
		}
	}
	if idx := strings.Index(upper, "COLLATE"); idx >= 0 {
		col.HasCollate = true
		col.CollateName = firstSQLServerToken(strings.TrimSpace(rest[idx+len("COLLATE"):]))
	}
	col.HasPK = strings.Contains(upper, "PRIMARY KEY")
	col.HasUnique = strings.Contains(upper, "UNIQUE")
	col.HasFK = strings.Contains(upper, "REFERENCES")
	return col, true
}

// firstSQLServerToken 取出开头的第一个 token，遇到空白或左括号停止（保留括号内的内容）。
// 例：`NVARCHAR(50) NOT NULL` -> `NVARCHAR(50)`；`[int] NULL` -> `[int]`。
func firstSQLServerToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "[") {
		if end := strings.IndexByte(s, ']'); end >= 0 {
			return s[:end+1]
		}
	}
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
			b.WriteRune(r)
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			b.WriteRune(r)
			continue
		}
		if depth == 0 && (r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ';') {
			return b.String()
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sqlServerIdentitySeed 从 `IDENTITY(3,1)` 里取 seed。取不到时返回 ok=false（调用方按 1 处理）。
func sqlServerIdentitySeed(upper string) (int, bool) {
	idx := strings.Index(upper, "IDENTITY")
	if idx < 0 {
		return 0, false
	}
	rest := upper[idx+len("IDENTITY"):]
	open := strings.IndexByte(rest, '(')
	if open < 0 {
		return 0, false
	}
	inner := rest[open+1:]
	if closeIdx := strings.IndexByte(inner, ')'); closeIdx >= 0 {
		inner = inner[:closeIdx]
	}
	seedPart := inner
	if comma := strings.IndexByte(inner, ','); comma >= 0 {
		seedPart = inner[:comma]
	}
	n, err := strconv.Atoi(strings.TrimSpace(seedPart))
	if err != nil {
		return 0, false
	}
	return n, true
}

// trimSQLServerCreateTablePrefix 剥掉 `CREATE TABLE` 前缀（含 IF NOT EXISTS 变体），
// 返回表名之后/括号之前的剩余文本与是否成功。
func trimSQLServerCreateTablePrefix(stmt string) (sqlServerName, string, bool) {
	lower := strings.ToLower(stmt)
	idx := strings.Index(lower, "create table")
	if idx < 0 {
		return sqlServerName{}, "", false
	}
	rest := strings.TrimSpace(stmt[idx+len("create table"):])
	open, closeIdx := findTopLevelParen(rest)
	if open < 0 || closeIdx <= open {
		return sqlServerName{}, "", false
	}
	head := strings.TrimSpace(rest[:open])
	name, ok := parseSQLServerName(head)
	if !ok {
		return sqlServerName{}, "", false
	}
	return name, rest[open+1 : closeIdx], true
}

// parseSQLServerCreateTable 解析 CREATE TABLE。
func parseSQLServerCreateTable(stmt string) (*sqlServerTable, bool) {
	name, body, ok := trimSQLServerCreateTablePrefix(stmt)
	if !ok {
		return nil, false
	}
	def := &sqlServerTable{Name: name, Raw: stmt}
	for _, item := range splitSQLServerTopLevel(body, ',') {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		upper := strings.ToUpper(item)
		switch {
		case strings.HasPrefix(upper, "CONSTRAINT ") ||
			strings.HasPrefix(upper, "PRIMARY KEY") ||
			strings.HasPrefix(upper, "UNIQUE") ||
			strings.HasPrefix(upper, "FOREIGN KEY") ||
			strings.HasPrefix(upper, "CHECK") ||
			strings.HasPrefix(upper, "INDEX") ||
			strings.HasPrefix(upper, "PERIOD"):
			parseSQLServerTableConstraint(item, def)
			continue
		}
		col, ok := parseSQLServerColumnDef(item)
		if !ok {
			continue
		}
		def.Columns = append(def.Columns, col)
		upperLower := strings.ToUpper(item)
		clustered := !strings.Contains(upperLower, "NONCLUSTERED")
		if col.HasPK {
			def.PrimaryKeys = append(def.PrimaryKeys, col.Name)
			def.PKClustered = clustered
		}
		if col.HasUnique {
			def.UniqueKeys = append(def.UniqueKeys, []string{col.Name})
			def.UniqueNames = append(def.UniqueNames, "")
		}
		if col.HasFK {
			def.HasForeignKey = true
		}
	}
	return def, true
}

// parseSQLServerTableConstraint 解析表级约束（可带 CONSTRAINT 名字前缀）。
func parseSQLServerTableConstraint(item string, def *sqlServerTable) {
	rest := strings.TrimSpace(item)
	name := ""
	if strings.HasPrefix(strings.ToUpper(rest), "CONSTRAINT ") {
		fields := strings.Fields(rest)
		if len(fields) >= 2 {
			name = unquoteSQLServerIdent(fields[1])
		}
		if len(fields) > 2 {
			rest = strings.Join(fields[2:], " ")
		}
	}
	upper := strings.ToUpper(rest)
	clustered := !strings.Contains(upper, "NONCLUSTERED")
	switch {
	case strings.HasPrefix(upper, "PRIMARY KEY"):
		def.PrimaryKeys = append(def.PrimaryKeys, extractSQLServerColumnList(rest)...)
		if name != "" {
			def.PKName = name
		}
		def.PKClustered = clustered
	case strings.HasPrefix(upper, "UNIQUE"):
		if cols := extractSQLServerColumnList(rest); len(cols) > 0 {
			def.UniqueKeys = append(def.UniqueKeys, cols)
			def.UniqueNames = append(def.UniqueNames, name)
			def.UniqueIndexes = append(def.UniqueIndexes, &sqlServerIndex{
				Name: name, Table: def.Name, Columns: cols, Unique: true,
				Clustered: clustered, Explicit: name != "", Raw: item,
			})
		}
	case strings.HasPrefix(upper, "FOREIGN KEY"), strings.HasPrefix(upper, "REFERENCES"):
		def.HasForeignKey = true
	case strings.HasPrefix(upper, "INDEX"):
		// CREATE TABLE 内联非约束索引：INDEX idx_name (col)
		rest2 := strings.TrimSpace(rest[len("INDEX"):])
		fields := strings.Fields(rest2)
		idx := &sqlServerIndex{Table: def.Name, Clustered: clustered, Raw: item}
		if len(fields) > 0 {
			idx.Name = unquoteSQLServerIdent(fields[0])
			idx.Explicit = idx.Name != ""
			idx.Columns = extractSQLServerColumnList(rest2)
		}
		if len(idx.Columns) > 0 {
			def.UniqueIndexes = append(def.UniqueIndexes, idx)
		}
	}
}

// parseSQLServerCreateIndex 解析
// `CREATE [UNIQUE] [CLUSTERED|NONCLUSTERED] INDEX name ON table (cols) [INCLUDE (...)] [WHERE ...]`
func parseSQLServerCreateIndex(stmt string) (*sqlServerIndex, bool) {
	clean := stripPGStringsAndComments(stmt)
	lower := strings.ToLower(clean)
	idx := strings.Index(lower, "create")
	if idx < 0 {
		return nil, false
	}
	rest := trimSQLServerStmtTail(clean[idx+len("create"):])
	lowerRest := strings.ToLower(rest)
	if !sqlServerHasWordPrefix(lowerRest, "index") &&
		!sqlServerHasWordPrefix(lowerRest, "unique") &&
		!sqlServerHasWordPrefix(lowerRest, "clustered") &&
		!sqlServerHasWordPrefix(lowerRest, "nonclustered") {
		return nil, false
	}
	def := &sqlServerIndex{Raw: stmt}
	// 逐段消费修饰词。注意必须「先 trim 再切片」：rest 往往以空格开头，
	// 直接用 rest[len(kw):] 会少切一个字符，把 `UNIQUE` 切成 `X`。
	for {
		current := strings.TrimSpace(rest)
		lowerRest = strings.ToLower(current)
		switch {
		case sqlServerHasWordPrefix(lowerRest, "unique"):
			def.Unique = true
			rest = current[len("unique"):]
		case sqlServerHasWordPrefix(lowerRest, "nonclustered"):
			def.Clustered = false
			rest = current[len("nonclustered"):]
		case sqlServerHasWordPrefix(lowerRest, "clustered"):
			def.Clustered = true
			rest = current[len("clustered"):]
		case sqlServerHasWordPrefix(lowerRest, "index"):
			rest = current[len("index"):]
		default:
			rest = current
			goto modifiersDone
		}
	}
modifiersDone:
	rest = strings.TrimSpace(rest)
	// 索引名
	token := firstSQLServerToken(rest)
	if token == "" {
		return nil, false
	}
	def.Name = unquoteSQLServerIdent(token)
	def.Explicit = def.Name != ""
	rest = strings.TrimSpace(rest[len(token):])
	// 必须紧跟 ON
	if !sqlServerHasWordPrefix(rest, "on") {
		return nil, false
	}
	rest = strings.TrimSpace(rest[len("on"):])
	open, closeIdx := findTopLevelParen(rest)
	if open < 0 || closeIdx <= open {
		return nil, false
	}
	name, ok := parseSQLServerName(rest[:open])
	if !ok {
		return nil, false
	}
	def.Table = name
	def.Columns = extractIndexKeyColumns(rest[open+1 : closeIdx])
	if len(def.Columns) == 0 {
		return nil, false
	}
	return def, true
}

// extractIndexKeyColumns 提取索引键列（跳过 ASC/DESC 排序方向）。
func extractIndexKeyColumns(s string) []string {
	var cols []string
	for _, item := range splitSQLServerTopLevel(s, ',') {
		fields := strings.Fields(strings.TrimSpace(item))
		if len(fields) == 0 {
			continue
		}
		name := unquoteSQLServerIdent(fields[0])
		if name == "" || strings.ContainsAny(name, "()") {
			continue
		}
		cols = append(cols, name)
	}
	return cols
}

// sqlServerAlterActions ALTER TABLE 的动作集合
type sqlServerAlterActions struct {
	Name          sqlServerName
	AddColumns    []sqlServerColumn
	AddPK         []string
	AddPKName     string
	AddUnique     []string // 新增唯一约束的列
	AddUniqueName string
	AddFK         bool
	DropColumns   []string
	AlterColumns  []string
	DropIndexes   []string
	// CheckOnly 仅用于探测表是否存在（语句本身没有可判定动作）
	CheckOnly bool
}

// parseSQLServerAlterTable 解析 ALTER TABLE 的动作。
//
// T-SQL 的 ALTER TABLE 允许多个动作用逗号连接，且 `ADD a int, b int` 的第二个
// 动作省略了 `ADD` 关键字 —— 因此需要「继承上一个动作关键字」。
func parseSQLServerAlterTable(stmt string) (*sqlServerAlterActions, bool) {
	clean := stripPGStringsAndComments(stmt)
	lower := strings.ToLower(clean)
	idx := strings.Index(lower, "alter table")
	if idx < 0 {
		return nil, false
	}
	rest := trimSQLServerStmtTail(clean[idx+len("alter table"):])
	if sqlServerHasWordPrefix(rest, "only") {
		rest = strings.TrimSpace(rest[len("only"):])
	}
	// 表名：取到第一个空白（或行尾）为止；带括号的情况不存在
	nameToken := firstSQLServerToken(rest)
	if nameToken == "" {
		return nil, false
	}
	name, ok := parseSQLServerName(nameToken)
	if !ok {
		return nil, false
	}
	act := &sqlServerAlterActions{Name: name, CheckOnly: true}
	body := strings.TrimSpace(rest[len(nameToken):])
	if body == "" {
		return act, true
	}

	current := ""
	for _, seg := range splitSQLServerTopLevel(body, ',') {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		lowerSeg := strings.ToLower(seg)
		switch {
		case strings.HasPrefix(lowerSeg, "with check add"), strings.HasPrefix(lowerSeg, "with nocheck add"):
			seg = strings.TrimSpace(seg[strings.Index(lowerSeg, "add")+len("add"):])
			lowerSeg = strings.ToLower(seg)
			current = "add"
		case strings.HasPrefix(lowerSeg, "add "):
			seg = strings.TrimSpace(seg[len("add"):])
			lowerSeg = strings.ToLower(seg)
			current = "add"
		case strings.HasPrefix(lowerSeg, "drop "):
			seg = strings.TrimSpace(seg[len("drop"):])
			lowerSeg = strings.ToLower(seg)
			current = "drop"
		case strings.HasPrefix(lowerSeg, "alter "):
			seg = strings.TrimSpace(seg[len("alter"):])
			lowerSeg = strings.ToLower(seg)
			current = "alter"
		case strings.HasPrefix(lowerSeg, "check constraint"),
			strings.HasPrefix(lowerSeg, "nocheck constraint"),
			strings.HasPrefix(lowerSeg, "enable trigger"),
			strings.HasPrefix(lowerSeg, "disable trigger"),
			strings.HasPrefix(lowerSeg, "switch"),
			strings.HasPrefix(lowerSeg, "rebuild"),
			strings.HasPrefix(lowerSeg, "set "):
			continue
		}
		if strings.HasPrefix(lowerSeg, "column ") {
			seg = strings.TrimSpace(seg[len("column"):])
			lowerSeg = strings.ToLower(seg)
		}

		switch current {
		case "add":
			switch {
			case strings.HasPrefix(lowerSeg, "constraint "):
				parseSQLServerAddConstraint(seg, act)
			case strings.HasPrefix(lowerSeg, "primary key"):
				act.AddPK = extractSQLServerColumnList(seg)
			case strings.HasPrefix(lowerSeg, "unique"):
				act.AddUnique = extractSQLServerColumnList(seg)
			case strings.HasPrefix(lowerSeg, "foreign key"), strings.HasPrefix(lowerSeg, "references"):
				act.AddFK = true
			case strings.HasPrefix(lowerSeg, "index "):
				// ADD INDEX 不是 T-SQL 语法（会直接失败），不当列定义处理
			default:
				if col, ok := parseSQLServerColumnDef(seg); ok {
					act.AddColumns = append(act.AddColumns, col)
				}
			}
		case "drop":
			switch {
			case strings.HasPrefix(lowerSeg, "column "):
				act.DropColumns = append(act.DropColumns, unquoteSQLServerIdent(strings.TrimSpace(seg[len("column"):])))
			case strings.HasPrefix(lowerSeg, "constraint "):
				// DROP CONSTRAINT 的对象名是约束名，不是列名，不参与列存在性判定
			case strings.HasPrefix(lowerSeg, "index "):
				act.DropIndexes = append(act.DropIndexes, unquoteSQLServerIdent(strings.TrimSpace(seg[len("index"):])))
			default:
				// `DROP col` 在 T-SQL 里非法（必须写 DROP COLUMN），按列名兜底
				act.DropColumns = append(act.DropColumns, unquoteSQLServerIdent(firstSQLServerToken(seg)))
			}
		case "alter":
			// ALTER COLUMN col newtype [NULL|NOT NULL]
			act.AlterColumns = append(act.AlterColumns, unquoteSQLServerIdent(firstSQLServerToken(seg)))
		}
	}
	act.CheckOnly = len(act.AddColumns) == 0 && len(act.AddPK) == 0 && len(act.AddUnique) == 0 &&
		!act.AddFK && len(act.DropColumns) == 0 && len(act.AlterColumns) == 0 && len(act.DropIndexes) == 0
	return act, true
}

// parseSQLServerAddConstraint 解析 ADD CONSTRAINT name PRIMARY KEY/UNIQUE/FOREIGN KEY (...)
func parseSQLServerAddConstraint(seg string, act *sqlServerAlterActions) {
	rest := strings.TrimSpace(seg[len("constraint"):])
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return
	}
	cname := unquoteSQLServerIdent(fields[0])
	if len(fields) > 1 {
		rest = strings.Join(fields[1:], " ")
	} else {
		rest = ""
	}
	upper := strings.ToUpper(rest)
	switch {
	case strings.HasPrefix(upper, "PRIMARY KEY"):
		act.AddPK = extractSQLServerColumnList(rest)
		act.AddPKName = cname
	case strings.HasPrefix(upper, "UNIQUE"):
		act.AddUnique = extractSQLServerColumnList(rest)
		act.AddUniqueName = cname
	case strings.HasPrefix(upper, "FOREIGN KEY"), strings.HasPrefix(upper, "REFERENCES"):
		act.AddFK = true
	}
}

// sqlServerExtendedProperty 扩展属性写入语句（表/列注释的唯一载体）
type sqlServerExtendedProperty struct {
	Name   sqlServerName
	Column string // 空表示表级注释
}

// parseSQLServerExtendedProperty 解析 EXEC sp_addextendedproperty / sp_updateextendedproperty。
//
// 关键点：T-SQL 里没有 COMMENT 语法，表/列注释只能写成扩展属性，且有两种写法：
//
//	EXEC sp_addextendedproperty 'MS_Description', '备注', 'SCHEMA', 'dbo', 'TABLE', 't', 'COLUMN', 'c'
//	EXEC sp_addextendedproperty @name=N'MS_Description', @value=N'备注',
//	     @level0type=N'SCHEMA', @level0name=N'dbo', @level1type=N'TABLE', @level1name=N't',
//	     @level2type=N'COLUMN', @level2name=N'c'
//
// 这里不区分写法，直接按「位置参数序列」抽 @levelNname 对应的值：找出全部字符串字面量，
// 再从里面挑 schema/table/column 三段。为避免误判，只认类型标记（SCHEMA/TABLE/COLUMN）之后的那个字面量。
func parseSQLServerExtendedProperty(stmt string) ([]sqlServerExtendedProperty, bool) {
	clean := stripPGStringsAndComments(stmt)
	lower := strings.ToLower(clean)
	idx := strings.Index(lower, "sp_addextendedproperty")
	if idx < 0 {
		idx = strings.Index(lower, "sp_updateextendedproperty")
	}
	if idx < 0 {
		return nil, false
	}
	// 用原文抽字面量（strip 之后字符串内容已被抹成空格）
	body := stmt[idx:]
	literals := extractSQLServerStringLiterals(body)
	// 命名参数写法：@level0name=N'...' / @level1name=N'...' / @level2name=N'...'
	levels := map[string]string{}
	for _, m := range sqlServerNamedArgRe.FindAllStringSubmatch(body, -1) {
		levels[m[1]] = strings.ReplaceAll(m[2], "''", "'")
	}
	var schema, table, column string
	if v, ok := levels["0"]; ok {
		schema = v
	}
	if v, ok := levels["1"]; ok {
		table = v
	}
	if v, ok := levels["2"]; ok {
		column = v
	}
	if table == "" {
		// 位置参数写法：按 'SCHEMA','dbo','TABLE','t'[,'COLUMN','c'] 的配对顺序取
		for i := 0; i+1 < len(literals); i++ {
			switch strings.ToUpper(strings.TrimSpace(literals[i])) {
			case "SCHEMA":
				schema = literals[i+1]
				i++
			case "TABLE":
				table = literals[i+1]
				i++
			case "COLUMN":
				column = literals[i+1]
				i++
			}
		}
	}
	if strings.TrimSpace(table) == "" {
		return nil, false
	}
	name := sqlServerName{Schema: strings.TrimSpace(schema), Object: strings.TrimSpace(table)}
	return []sqlServerExtendedProperty{{Name: name, Column: strings.TrimSpace(column)}}, true
}

// extractSQLServerStringLiterals 抽取 SQL 文本里的单引号字符串（含 N'...' 前缀）。
func extractSQLServerStringLiterals(s string) []string {
	var out []string
	var cur strings.Builder
	inSingle := false
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if !inSingle {
			if c == '\'' {
				inSingle = true
				cur.Reset()
			}
			continue
		}
		if c == '\'' {
			if i+1 < len(runes) && runes[i+1] == '\'' {
				cur.WriteRune('\'')
				i++
				continue
			}
			inSingle = false
			out = append(out, cur.String())
			continue
		}
		cur.WriteRune(c)
	}
	return out
}

// sqlServerRename sp_rename 的目标类型
type sqlServerRename struct {
	Kind    string // TABLE / COLUMN / INDEX
	Table   sqlServerName
	OldName string
	NewName string
}

// parseSQLServerSpRename 解析 EXEC sp_rename 'obj', 'new', ['COLUMN'|'INDEX'|'OBJECT']。
func parseSQLServerSpRename(stmt string) (*sqlServerRename, bool) {
	lower := strings.ToLower(stmt)
	idx := strings.Index(lower, "sp_rename")
	if idx < 0 {
		return nil, false
	}
	literals := extractSQLServerStringLiterals(stmt[idx:])
	if len(literals) < 2 {
		return nil, false
	}
	kind := "TABLE"
	if len(literals) >= 3 {
		switch strings.ToUpper(strings.TrimSpace(literals[2])) {
		case "COLUMN":
			kind = "COLUMN"
		case "INDEX":
			kind = "INDEX"
		case "OBJECT", "TABLE":
			kind = "TABLE"
		}
	}
	res := &sqlServerRename{Kind: kind, NewName: strings.TrimSpace(literals[1])}
	if kind == "TABLE" {
		name, ok := parseSQLServerName(literals[0])
		if !ok {
			return nil, false
		}
		res.Table = name
		res.OldName = name.Object
		return res, true
	}
	// sp_rename 'orders.col', 'newcol', 'COLUMN'：目标写成 table.column
	parts := splitSQLServerNameParts(strings.TrimSpace(literals[0]))
	if len(parts) < 2 {
		return nil, false
	}
	res.OldName = unquoteSQLServerIdent(parts[len(parts)-1])
	name, ok := parseSQLServerName(strings.Join(parts[:len(parts)-1], "."))
	if !ok {
		return nil, false
	}
	res.Table = name
	return res, true
}

// sqlServerObjectKind 判断 DDL 头属于哪一类对象（view/procedure/function/trigger/index/""）。
func sqlServerObjectKind(head string) string {
	h := strings.ToLower(strings.TrimSpace(head))
	for _, prefix := range []string{"create or alter ", "create ", "alter ", "drop "} {
		if !strings.HasPrefix(h, prefix) {
			continue
		}
		rest := strings.TrimSpace(h[len(prefix):])
		switch {
		case strings.HasPrefix(rest, "view"):
			return "view"
		case strings.HasPrefix(rest, "procedure"), strings.HasPrefix(rest, "proc"):
			return "procedure"
		case strings.HasPrefix(rest, "function"):
			return "function"
		case strings.HasPrefix(rest, "trigger"):
			return "trigger"
		case strings.HasPrefix(rest, "index"), strings.HasPrefix(rest, "unique"), strings.HasPrefix(rest, "clustered"), strings.HasPrefix(rest, "nonclustered"):
			return "index"
		}
	}
	return ""
}

// parseSQLServerViewStatement 解析 CREATE/ALTER/DROP VIEW [IF EXISTS] name。
func parseSQLServerViewStatement(stmt string) (op string, view sqlServerName, ok bool) {
	clean := stripPGStringsAndComments(stmt)
	lower := strings.ToLower(strings.TrimSpace(clean))
	var rest string
	switch {
	case strings.HasPrefix(lower, "create or alter view"):
		op, rest = "CREATE OR ALTER VIEW", strings.TrimSpace(clean[len("create or alter view"):])
	case strings.HasPrefix(lower, "create view"):
		op, rest = "CREATE VIEW", strings.TrimSpace(clean[len("create view"):])
	case strings.HasPrefix(lower, "alter view"):
		op, rest = "ALTER VIEW", strings.TrimSpace(clean[len("alter view"):])
	case strings.HasPrefix(lower, "drop view"):
		op, rest = "DROP VIEW", strings.TrimSpace(clean[len("drop view"):])
	default:
		return "", sqlServerName{}, false
	}
	lowerRest := strings.ToLower(rest)
	if strings.HasPrefix(lowerRest, "if exists") {
		rest = strings.TrimSpace(rest[len("if exists"):])
	}
	name, ok2 := parseSQLServerName(firstSQLServerToken(rest))
	if !ok2 {
		return op, sqlServerName{}, true
	}
	return op, name, true
}

// sqlServerSelectIntoTable 判断 `SELECT ... INTO <table> ...`（T-SQL 的 CTAS 写法）。
//
// 只认顶层 INTO 且后面紧跟对象名、并且不是 INSERT ... INTO 的场景（INSERT 已由语句头
// 判定为 DML，不会走到这里）。同时排除 `INTO @tablevar`（表变量，不是建表）。
func sqlServerSelectIntoTable(stmt string) (sqlServerName, bool) {
	clean := stripPGStringsAndComments(stmt)
	lower := strings.ToLower(strings.TrimSpace(clean))
	if !strings.HasPrefix(lower, "select") && !strings.HasPrefix(lower, "with") {
		return sqlServerName{}, false
	}
	// 顶层扫描 INTO（括号内不算）
	depth := 0
	inSingle, inDouble, inBracket := false, false, false
	runes := []rune(clean)
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
			if !hasWordAt(runes, i+1, "into") {
				continue
			}
			rest := strings.TrimSpace(string(runes[i+1+len("into"):]))
			if rest == "" || strings.HasPrefix(rest, "@") || strings.HasPrefix(rest, "#") {
				return sqlServerName{}, false
			}
			name, ok := parseSQLServerName(firstSQLServerToken(rest))
			if !ok {
				return sqlServerName{}, false
			}
			return name, true
		}
	}
	return sqlServerName{}, false
}

// hasWordAt 判断 runes[pos:] 是否以完整单词 word 开头（大小写不敏感，后面必须是非标识符字符）。
func hasWordAt(runes []rune, pos int, word string) bool {
	if pos < 0 || pos+len(word) > len(runes) {
		return false
	}
	for i := 0; i < len(word); i++ {
		if toLowerASCII(runes[pos+i]) != word[i] {
			return false
		}
	}
	end := pos + len(word)
	if end < len(runes) && isSQLServerWordRune(runes[end]) {
		return false
	}
	return true
}

func toLowerASCII(r rune) byte {
	if r >= 'A' && r <= 'Z' {
		return byte(r - 'A' + 'a')
	}
	if r > 255 {
		return 0
	}
	return byte(r)
}

func isSQLServerWordRune(r rune) bool {
	return r == '_' || r == '@' || r == '#' || r == '$' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// sqlServerTopRows 解析 TOP (n) / TOP n 的常量行数；无 TOP 返回 0。
func sqlServerTopRows(clean string) int {
	lower := strings.ToLower(clean)
	idx := strings.Index(lower, "top")
	for idx >= 0 {
		after := strings.TrimSpace(lower[idx+len("top"):])
		prefixOK := idx == 0 || !isSQLServerWordRune(rune(lower[idx-1]))
		if prefixOK {
			if strings.HasPrefix(after, "(") {
				if closeIdx := strings.IndexByte(after, ')'); closeIdx > 0 {
					if n, err := strconv.Atoi(strings.TrimSpace(after[1:closeIdx])); err == nil {
						return n
					}
					return 0
				}
			}
			token := firstSQLServerToken(after)
			if n, err := strconv.Atoi(strings.TrimSpace(token)); err == nil {
				return n
			}
		}
		next := strings.Index(lower[idx+len("top"):], "top")
		if next < 0 {
			break
		}
		idx = idx + len("top") + next
	}
	return 0
}

// sqlServerHasKeyword 判断语句里是否出现某个独立关键字（调用方需先 stripPGStringsAndComments）。
func sqlServerHasKeyword(clean, keyword string) bool {
	return indexOfKeyword(clean, strings.ToLower(keyword)) >= 0
}

// sqlServerTopLevelKeyword 判断关键字是否出现在括号之外的顶层。
//
// 直接复用 Oracle 引擎的实现：该函数只处理括号深度与词边界（末尾是空格的写法如
// " and " 也能正确命中），与具体方言无关；而 indexOfKeyword 对 " or " 这类带空格
// 的写法会因为「关键字前一字符是词字符」而永远失配。
func sqlServerTopLevelKeyword(s, keyword string) bool {
	return oracleTopLevelKeyword(s, keyword)
}

// extractSQLServerColumnList 提取括号内的列名列表（识别 [列名] 与 ASC/DESC 排序方向）。
func extractSQLServerColumnList(s string) []string {
	open, closeIdx := findTopLevelParen(s)
	if open < 0 || closeIdx <= open {
		return nil
	}
	return extractIndexKeyColumns(s[open+1 : closeIdx])
}

// stripSQLServerGoBatch 把独立的 GO 批处理行替换为分号。
// 实现已下沉到 pkg/sqlsplit.StripGoBatches（与工单提交链路共用），这里保留薄包装。
func stripSQLServerGoBatch(sqlText string) string {
	return sqlsplit.StripGoBatches(sqlText)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// sqlServerNamePattern 表名/列名不带引号时的合法字符集（小写）
var (
	sqlServerLowerNamePattern = pgNamePattern
	sqlServerUpperNamePattern = pgUpperNamePattern
)
