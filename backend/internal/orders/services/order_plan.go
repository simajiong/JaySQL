package services

// ---------------------------------------------------------------------------
// SQL 工单「语句计划」—— 一个工单同时包含 DDL 与 DML 的唯一事实来源
//
// 收口前工单链路上有三处在各自推断 SQL 类型（commit / syntax / pre_inspect），
// 而且都按整段内容推断一次：只要第一条语句是 CREATE/ALTER，整单就被当成 DDL，
// 后面的 INSERT/UPDATE 被静默忽略；一旦两种类型都被识别出来，又直接报
// 「同一个工单中不允许混用多种SQL类型」。
//
// 这里改为**逐条语句**定型，产出 OrderPlan：
//   - 每条语句有自己的 SQLType（执行阶段按它分派 DDL/DML 执行器）
//   - 整单类型由语句集合归约（DDL / DML / EXPORT / MIXED）
//   - 顺带做混合工单的静态冲突分析（对象被删/改名后仍被引用等）
//
// 设计取舍：
//  1. **不重排语句**。DDL 与 DML 的依赖关系（先建表后灌数据、先加列后更新）
//     只有用户知道，平台重排会破坏语义（把 ALTER 提到 INSERT 之前可能直接失败）。
//     平台只做两件事：按用户书写顺序串行执行 + 静态检出「必然失败」的写法。
//  2. **只报真冲突**，不做风格建议。DROP TABLE 后还 INSERT 同表这类必然失败的写法
//     判 must；其余一律不报，避免合法混合写法被噪音淹没。
// ---------------------------------------------------------------------------

import (
	"fmt"
	"regexp"
	"strings"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/inspect/controllers"

	"github.com/simajiong/jaysql/pkg/parser"
	"github.com/simajiong/jaysql/pkg/sqlsplit"

	"github.com/pingcap/tidb/pkg/parser/ast"
)

// SQL 工单落库类型
const (
	orderSQLTypeDDL    = "DDL"
	orderSQLTypeDML    = "DML"
	orderSQLTypeExport = "EXPORT"
	// orderSQLTypeMixed 同一工单内 DDL 与 DML 并存
	orderSQLTypeMixed = "MIXED"
)

// orderStatementKind 语句的语义动作（只覆盖与「执行顺序冲突」相关的动作）
type orderStatementKind string

const (
	stmtKindCreateTable    orderStatementKind = "create_table"
	stmtKindDropTable      orderStatementKind = "drop_table"
	stmtKindTruncateTable  orderStatementKind = "truncate_table"
	stmtKindRenameTable    orderStatementKind = "rename_table"
	stmtKindAlterTable     orderStatementKind = "alter_table"
	stmtKindCreateDatabase orderStatementKind = "create_database"
	stmtKindDropDatabase   orderStatementKind = "drop_database"
	stmtKindCreateIndex    orderStatementKind = "create_index"
	stmtKindInsert         orderStatementKind = "insert"
	stmtKindUpdate         orderStatementKind = "update"
	stmtKindDelete         orderStatementKind = "delete"
	stmtKindOther          orderStatementKind = ""
)

// isDML 判断是否为数据变更类语句
func (k orderStatementKind) isDML() bool {
	switch k {
	case stmtKindInsert, stmtKindUpdate, stmtKindDelete:
		return true
	default:
		return false
	}
}

// OrderStatement 工单内的一条语句（定型结果）
type OrderStatement struct {
	// Seq 语句在工单内的执行序号，从 1 开始
	Seq int
	// Line 语句在原始提交文本中的起始行号，从 1 开始
	Line int
	// Text 语句原文
	Text string
	// SQLType 该语句自身的类型：DDL / DML / EXPORT
	SQLType string
	// Kind 语义动作
	Kind orderStatementKind
	// Table 主对象（表名，可能带库前缀；比较时取最后一段）
	Table string
	// RenameTo RENAME 的目标表名
	RenameTo string
	// DropColumns 该语句删除的列
	DropColumns []string
	// RenameColumns 该语句重命名的列：旧名 → 新名
	RenameColumns map[string]string
	// AddColumns 该语句（ALTER TABLE）新增的列
	AddColumns []string
	// CreateColumns 该语句（CREATE TABLE）定义的列。仅当表名后紧跟「(」时提取，
	// CREATE ... AS SELECT / LIKE 这类无法静态确定列集的写法为 nil
	CreateColumns []string
	// RefColumns 该语句引用到的列（INSERT 列清单 / UPDATE 的 SET 列）
	RefColumns []string
	// InsertValueCounts INSERT 每行 VALUES 的值个数（无 VALUES 行如 INSERT...SELECT 为 nil）
	InsertValueCounts []int
}

// OrderPlan 整单的语句计划
type OrderPlan struct {
	Statements []OrderStatement
	// SQLType 整单类型：DDL / DML / EXPORT / MIXED
	SQLType  string
	DDLCount int
	DMLCount int
}

// HasMixedStatements 是否 DDL 与 DML 并存
func (p OrderPlan) HasMixedStatements() bool {
	return p.DDLCount > 0 && p.DMLCount > 0
}

// countDDL 统计计划内的 DDL 语句条数
func (p OrderPlan) countDDL() int { return p.DDLCount }

// ---------------------------------------------------------------------------
// 语句拆分与逐条定型
// ---------------------------------------------------------------------------

// BuildOrderPlan 把工单内容拆成语句并按条定型。
//
// MySQL 类：优先用 TiDB parser 拿 AST（准确），AST 覆盖不到的语句（如 GRANT）
// 用语句头前缀兜底；parser 整体解析失败时退化为语法感知拆分 + 前缀识别，
// 保持与历史行为一致（真正的语法错误交给审核引擎报）。
// PG 类：TiDB parser 解析不了 PG 语法，直接走 sqlsplit.SplitTyped。
func BuildOrderPlan(content string, dbType string) (OrderPlan, error) {
	texts, types, err := splitOrderStatements(content, dbType)
	if err != nil {
		return OrderPlan{}, err
	}
	plan := OrderPlan{Statements: make([]OrderStatement, 0, len(texts))}
	cursor := 0
	for i := range texts {
		st := OrderStatement{
			Seq:     i + 1,
			Text:    texts[i],
			SQLType: types[i],
			Line:    locateStatementLine(content, texts[i], &cursor),
		}
		applyStatementFacts(&st)
		switch st.SQLType {
		case orderSQLTypeDDL:
			plan.DDLCount++
		case orderSQLTypeDML:
			plan.DMLCount++
		}
		plan.Statements = append(plan.Statements, st)
	}
	if len(plan.Statements) == 0 {
		return OrderPlan{}, fmt.Errorf("SQL内容不能为空")
	}
	if plan.SQLType, err = summarizeOrderSQLType(plan); err != nil {
		return OrderPlan{}, err
	}
	return plan, nil
}

// summarizeOrderSQLType 由语句集合归约整单类型。
// 导出语句与 DDL/DML 混用直接拒绝：导出走文件流、不落库，混在一起没有执行语义。
func summarizeOrderSQLType(plan OrderPlan) (string, error) {
	hasExport := false
	for _, st := range plan.Statements {
		if st.SQLType == orderSQLTypeExport {
			hasExport = true
			break
		}
	}
	if hasExport {
		if plan.DDLCount > 0 || plan.DMLCount > 0 {
			return "", fmt.Errorf("查询/导出语句不能与 DDL、DML 混在同一个工单，请拆分为独立工单")
		}
		return orderSQLTypeExport, nil
	}
	if plan.DDLCount > 0 && plan.DMLCount > 0 {
		return orderSQLTypeMixed, nil
	}
	if plan.DDLCount > 0 {
		return orderSQLTypeDDL, nil
	}
	if plan.DMLCount > 0 {
		return orderSQLTypeDML, nil
	}
	return "", fmt.Errorf("SQL内容不能为空")
}

// splitOrderStatements 拆分语句并逐条识别类型
func splitOrderStatements(content string, dbType string) ([]string, []string, error) {
	if isSQLServerDB(dbType) {
		// SQL Server：T-SQL 方言不能交给 TiDB parser，直接走语法感知拆分。
		// ① GO 是客户端批处理分隔符（SSMS/sqlcmd），不是 T-SQL 语句，先剥离，
		//    否则会单独或与后续语句粘成一条，报「无法识别类型」；
		// ② EXEC 系列按 DDL 归类，与预审链路（inspect/checker）同口径。
		return sqlsplit.SplitTypedExtra(sqlsplit.StripGoBatches(content), sqlsplit.TSQLExtraDDLPrefixes)
	}
	if isPgLikeDB(dbType) {
		return sqlsplit.SplitTyped(content)
	}
	if stmts, err := parser.NewParse(content, "", ""); err == nil && len(stmts) > 0 {
		texts := make([]string, 0, len(stmts))
		types := make([]string, 0, len(stmts))
		for _, stmt := range stmts {
			text := strings.TrimSpace(stmt.OriginalText())
			if text == "" {
				continue
			}
			sqlType := sqlTypeOfAST(stmt)
			if sqlType == "" {
				// AST 覆盖不到的语句（GRANT / REVOKE / COMMENT ON 等）用语句头兜底
				sqlType = sqlsplit.StatementType(text)
			}
			if sqlType == "" {
				head := sqlsplit.NormalizeLeading(text)
				if len(head) > 40 {
					head = head[:40]
				}
				return nil, nil, fmt.Errorf("暂不支持当前SQL类型（%s...），请提交标准 DDL、DML 或 SELECT 语句", head)
			}
			texts = append(texts, text)
			types = append(types, sqlType)
		}
		if len(texts) > 0 {
			return texts, types, nil
		}
	}
	// parser 解析失败：退回语法感知拆分 + 前缀识别
	return sqlsplit.SplitTyped(content)
}

// sqlTypeOfAST 按 AST 节点判定语句类型，未覆盖返回空串
func sqlTypeOfAST(stmt ast.StmtNode) string {
	switch stmt.(type) {
	case *ast.SelectStmt, *ast.SetOprStmt:
		return orderSQLTypeExport
	case *ast.DeleteStmt, *ast.InsertStmt, *ast.UpdateStmt:
		return orderSQLTypeDML
	case *ast.AlterTableStmt, *ast.AlterSequenceStmt, *ast.AlterPlacementPolicyStmt,
		*ast.AlterDatabaseStmt,
		*ast.CreateDatabaseStmt, *ast.CreateIndexStmt, *ast.CreateTableStmt, *ast.CreateViewStmt,
		*ast.CreateSequenceStmt, *ast.CreatePlacementPolicyStmt,
		*ast.DropDatabaseStmt, *ast.DropIndexStmt, *ast.DropTableStmt, *ast.DropSequenceStmt,
		*ast.DropPlacementPolicyStmt,
		*ast.RenameTableStmt, *ast.TruncateTableStmt:
		return orderSQLTypeDDL
	default:
		return ""
	}
}

// locateStatementLine 计算语句在原始文本中的起始行号。
// cursor 单调后移，保证重复语句也能按出现顺序逐一定位；
// parser 复写原文（大小写/空白）导致匹配不到时回落为 1。
func locateStatementLine(content, statement string, cursor *int) int {
	if content == "" || statement == "" {
		return 1
	}
	candidates := []string{statement, strings.TrimSpace(statement), strings.TrimSuffix(statement, ";")}
	for _, cand := range candidates {
		if cand == "" {
			continue
		}
		if idx := strings.Index(content[*cursor:], cand); idx >= 0 {
			abs := *cursor + idx
			*cursor = abs + len(cand)
			return strings.Count(content[:abs], "\n") + 1
		}
	}
	if idx := strings.Index(content, statement); idx >= 0 {
		return strings.Count(content[:idx], "\n") + 1
	}
	return 1
}

// ---------------------------------------------------------------------------
// 语句事实提取（对象 / 列变更）
// ---------------------------------------------------------------------------

// 标识符表达式：可选库前缀 + 可选引号包裹（MySQL 反引号 / PG 双引号）
const (
	sqlIdent = "[`\"]?[A-Za-z_][A-Za-z0-9_$]*[`\"]?(?:\\s*\\.\\s*[`\"]?[A-Za-z_][A-Za-z0-9_$]*[`\"]?)?"
	sqlCol   = "[`\"]?[A-Za-z_][A-Za-z0-9_$]*[`\"]?"
)

var (
	createTableRe = regexp.MustCompile(`(?is)^\s*create\s+(?:temporary\s+)?table\s+(?:if\s+not\s+exists\s+)?(` + sqlIdent + `)`)
	dropTableRe   = regexp.MustCompile(`(?is)^\s*drop\s+(?:temporary\s+)?table\s+(?:if\s+exists\s+)?(` + sqlIdent + `)`)
	truncateRe    = regexp.MustCompile(`(?is)^\s*truncate\s+(?:table\s+)?(?:only\s+)?(` + sqlIdent + `)`)
	renameTableRe = regexp.MustCompile(`(?is)^\s*rename\s+table\s+(` + sqlIdent + `)\s+to\s+(` + sqlIdent + `)`)
	alterTableRe  = regexp.MustCompile(`(?is)^\s*alter\s+table\s+(?:if\s+exists\s+)?(?:only\s+)?(` + sqlIdent + `)`)
	alterRenameRe = regexp.MustCompile(`(?is)^\s*rename\s+to\s+(` + sqlIdent + `)`)
	createDBRe    = regexp.MustCompile(`(?is)^\s*create\s+(?:database|schema)\s+(?:if\s+not\s+exists\s+)?(` + sqlIdent + `)`)
	dropDBRe      = regexp.MustCompile(`(?is)^\s*drop\s+(?:database|schema)\s+(?:if\s+exists\s+)?(` + sqlIdent + `)`)
	createIndexRe = regexp.MustCompile(`(?is)^\s*create\s+(?:unique\s+)?index\s+` + sqlCol + `\s+on\s+(` + sqlIdent + `)`)
	insertRe      = regexp.MustCompile(`(?is)^\s*(?:insert|replace)\s+(?:ignore\s+)?into\s+(` + sqlIdent + `)\s*\(([^)]*)\)`)
	insertBareRe  = regexp.MustCompile(`(?is)^\s*(?:insert|replace)\s+(?:ignore\s+)?into\s+(` + sqlIdent + `)`)
	updateRe      = regexp.MustCompile(`(?is)^\s*update\s+(?:ignore\s+)?(` + sqlIdent + `)\s+(?:as\s+` + sqlCol + `\s+)?set\s+([\s\S]*)$`)
	deleteRe      = regexp.MustCompile(`(?is)^\s*delete\s+from\s+(` + sqlIdent + `)`)
	colIdentRe    = regexp.MustCompile("[`\"]?([A-Za-z_][A-Za-z0-9_$]*)[`\"]?")
	setColRe      = regexp.MustCompile(`(?is)(?:^|,)\s*` + sqlCol + `\s*=`)
	valuesRe      = regexp.MustCompile(`(?is)\bvalues?\s*\(`)

	alterDropColRe   = regexp.MustCompile(`(?is)(?:^|,)\s*drop\s+(?:column\s+)?(` + sqlCol + `)`)
	alterRenameColRe = regexp.MustCompile(`(?is)(?:^|,)\s*rename\s+column\s+(` + sqlCol + `)\s+to\s+(` + sqlCol + `)`)
	alterChangeColRe = regexp.MustCompile(`(?is)(?:^|,)\s*change\s+(?:column\s+)?(` + sqlCol + `)\s+(` + sqlCol + `)`)
	alterAlterColRe  = regexp.MustCompile(`(?is)(?:^|,)\s*alter\s+(?:column\s+)?(` + sqlCol + `)\s+rename\s+to\s+(` + sqlCol + `)`)

	alterAddParenRe  = regexp.MustCompile(`(?is)^\s*add\s+(?:column\s+)?\(([\s\S]*)\)\s*$`)
	alterAddSingleRe = regexp.MustCompile(`(?is)^\s*add\s+(?:column\s+)?(` + sqlCol + `)`)
)

// alterDropReserved DROP 后面跟这些词时不是「删列」，避免把 DROP INDEX 认成 DROP COLUMN
var alterDropReserved = map[string]struct{}{
	"index": {}, "key": {}, "primary": {}, "foreign": {}, "constraint": {},
	"check": {}, "partition": {}, "default": {}, "tablespace": {}, "trigger": {},
}

// applyStatementFacts 从语句原文中提取「影响哪些对象/列」「引用了哪些列」
func applyStatementFacts(st *OrderStatement) {
	text := stripLeadingSQLComments(st.Text)
	switch {
	case createTableRe.MatchString(text):
		st.Kind = stmtKindCreateTable
		st.Table = createTableRe.FindStringSubmatch(text)[1]
		st.CreateColumns = createTableColumnDefs(text)
	case dropTableRe.MatchString(text):
		st.Kind = stmtKindDropTable
		st.Table = dropTableRe.FindStringSubmatch(text)[1]
	case truncateRe.MatchString(text):
		st.Kind = stmtKindTruncateTable
		st.Table = truncateRe.FindStringSubmatch(text)[1]
	case renameTableRe.MatchString(text):
		m := renameTableRe.FindStringSubmatch(text)
		st.Kind = stmtKindRenameTable
		st.Table = m[1]
		st.RenameTo = m[2]
	case alterTableRe.MatchString(text):
		loc := alterTableRe.FindStringSubmatchIndex(text)
		st.Kind = stmtKindAlterTable
		st.Table = text[loc[2]:loc[3]]
		body := text[loc[1]:]
		if m := alterRenameRe.FindStringSubmatch(body); m != nil {
			// ALTER TABLE a RENAME TO b 本质是改名，按改名处理（旧名随后不可用）
			st.Kind = stmtKindRenameTable
			st.RenameTo = m[1]
			break
		}
		applyAlterBodyFacts(st, body)
	case createDBRe.MatchString(text):
		st.Kind = stmtKindCreateDatabase
		st.Table = createDBRe.FindStringSubmatch(text)[1]
	case dropDBRe.MatchString(text):
		st.Kind = stmtKindDropDatabase
		st.Table = dropDBRe.FindStringSubmatch(text)[1]
	case createIndexRe.MatchString(text):
		st.Kind = stmtKindCreateIndex
		st.Table = createIndexRe.FindStringSubmatch(text)[1]
	case insertRe.MatchString(text):
		m := insertRe.FindStringSubmatch(text)
		st.Kind = stmtKindInsert
		st.Table = m[1]
		st.RefColumns = parseColumnList(m[2])
		st.InsertValueCounts = insertValueTupleSizes(text)
	case insertBareRe.MatchString(text):
		// INSERT ... VALUES 未显式列出列名：无法判定引用了哪些列
		st.Kind = stmtKindInsert
		st.Table = insertBareRe.FindStringSubmatch(text)[1]
		st.InsertValueCounts = insertValueTupleSizes(text)
	case updateRe.MatchString(text):
		m := updateRe.FindStringSubmatch(text)
		st.Kind = stmtKindUpdate
		st.Table = m[1]
		st.RefColumns = parseSetColumns(m[2])
	case deleteRe.MatchString(text):
		st.Kind = stmtKindDelete
		st.Table = deleteRe.FindStringSubmatch(text)[1]
	}
	if st.SQLType == orderSQLTypeDML && !st.Kind.isDML() {
		// 兜底：DML 但动作没识别出来（如 PG 的 MERGE），只做对象级判定
		st.Kind = stmtKindOther
		st.Table = ""
	}
}

// applyAlterBodyFacts 解析 ALTER TABLE 的子句：加列 / 删列 / 改列名
func applyAlterBodyFacts(st *OrderStatement, body string) {
	for _, seg := range splitTopLevel(body, ',') {
		st.AddColumns = append(st.AddColumns, alterAddColumnsOf(seg)...)
	}
	for _, m := range alterDropColRe.FindAllStringSubmatch(body, -1) {
		name := strings.ToLower(strings.Trim(m[1], "`\" "))
		if _, reserved := alterDropReserved[name]; reserved {
			continue
		}
		st.DropColumns = append(st.DropColumns, name)
	}
	for _, re := range []*regexp.Regexp{alterRenameColRe, alterChangeColRe, alterAlterColRe} {
		for _, m := range re.FindAllStringSubmatch(body, -1) {
			oldName := strings.ToLower(strings.Trim(m[1], "`\" "))
			newName := strings.ToLower(strings.Trim(m[2], "`\" "))
			if oldName == "" || newName == "" || oldName == newName {
				continue
			}
			if st.RenameColumns == nil {
				st.RenameColumns = map[string]string{}
			}
			st.RenameColumns[oldName] = newName
		}
	}
}

// ---------------------------------------------------------------------------
// 括号配平与建表列提取（纯静态，MySQL / PG 通用）
// ---------------------------------------------------------------------------

// splitTopLevel 按分隔符切分，忽略括号内与引号（' " `）内的分隔符
func splitTopLevel(s string, sep byte) []string {
	var parts []string
	depth := 0
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if c == sep && depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
}

// matchingCloseParen 返回与 s[open] 配平的 ')' 下标；open 越界或无配平时返回 -1
func matchingCloseParen(s string, open int) int {
	if open < 0 || open >= len(s) || s[open] != '(' {
		return -1
	}
	depth := 0
	var quote byte
	for i := open; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
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

// firstIdentOf 取片段里的第一个标识符（去反引号/双引号，转小写）
func firstIdentOf(seg string) string {
	m := colIdentRe.FindStringSubmatch(seg)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}

// createConstraintStarters 建表列定义段里这些开头不是「列」，是表级约束
var createConstraintStarters = map[string]struct{}{
	"primary": {}, "unique": {}, "key": {}, "index": {}, "constraint": {},
	"foreign": {}, "check": {}, "fulltext": {}, "spatial": {}, "period": {}, "exclude": {},
}

// alterAddReserved ALTER 里 ADD 后面跟这些词时不是「加列」
var alterAddReserved = map[string]struct{}{
	"index": {}, "key": {}, "unique": {}, "primary": {}, "foreign": {},
	"constraint": {}, "fulltext": {}, "spatial": {}, "check": {}, "partition": {},
}

// createTableColumnDefs 提取 CREATE TABLE 定义的列名。
// 仅当表名后紧跟「(」时提取；CREATE ... AS SELECT / LIKE 返回 nil（列集无法静态确定）。
// 列定义段开头是表级约束（PRIMARY KEY / UNIQUE / KEY / CONSTRAINT...）的跳过。
func createTableColumnDefs(text string) []string {
	loc := createTableRe.FindStringSubmatchIndex(text)
	if loc == nil {
		return nil
	}
	rest := strings.TrimLeft(text[loc[1]:], " \t\r\n")
	if !strings.HasPrefix(rest, "(") {
		return nil
	}
	closeIdx := matchingCloseParen(rest, 0)
	if closeIdx < 0 {
		return nil
	}
	var cols []string
	for _, seg := range splitTopLevel(rest[1:closeIdx], ',') {
		name := firstIdentOf(seg)
		if name == "" {
			continue
		}
		if _, isConstraint := createConstraintStarters[name]; isConstraint {
			continue
		}
		cols = append(cols, name)
	}
	return cols
}

// alterAddColumnsOf 从 ALTER TABLE 的一个逗号分段里提取新增列名。
// 支持 `ADD COLUMN name type` / `ADD name type` / `ADD COLUMN (a int, b int)`；
// ADD INDEX / ADD CONSTRAINT 等非加列子句返回 nil。
func alterAddColumnsOf(seg string) []string {
	if m := alterAddParenRe.FindStringSubmatch(seg); m != nil {
		var cols []string
		for _, part := range splitTopLevel(m[1], ',') {
			name := firstIdentOf(part)
			if name == "" {
				continue
			}
			if _, isConstraint := createConstraintStarters[name]; isConstraint {
				continue
			}
			cols = append(cols, name)
		}
		return cols
	}
	m := alterAddSingleRe.FindStringSubmatch(seg)
	if m == nil {
		return nil
	}
	name := strings.ToLower(strings.Trim(m[1], "`\" "))
	if name == "" {
		return nil
	}
	if _, reserved := alterAddReserved[name]; reserved {
		return nil
	}
	return []string{name}
}

// insertValueTupleSizes 解析 INSERT ... VALUES 每行的值个数（MySQL / PG 通用）。
// 顶层括号组为一行，组内按顶层逗号计数；`VALUES ()` 记 0（合法的全默认值插入）。
// 找不到 VALUES（如 INSERT ... SELECT）返回 nil。
func insertValueTupleSizes(text string) []int {
	loc := valuesRe.FindStringIndex(text)
	if loc == nil {
		return nil
	}
	rest := text[loc[1]-1:] // 从 '(' 开始
	var sizes []int
	for i := 0; i < len(rest); {
		// 跳过行间空白（含逗号之后、下一行之前的空格换行）
		for i < len(rest) {
			if rest[i] == ' ' || rest[i] == '\t' || rest[i] == '\r' || rest[i] == '\n' {
				i++
				continue
			}
			break
		}
		if i >= len(rest) || rest[i] != '(' {
			break
		}
		closeIdx := matchingCloseParen(rest, i)
		if closeIdx < 0 {
			break
		}
		content := rest[i+1 : closeIdx]
		if strings.TrimSpace(content) == "" {
			sizes = append(sizes, 0)
		} else {
			sizes = append(sizes, len(splitTopLevel(content, ',')))
		}
		i = closeIdx + 1
		// 行间分隔符：允许一个逗号；遇到其它内容（on duplicate / select 等）即结束
		for i < len(rest) {
			if rest[i] == ' ' || rest[i] == '\t' || rest[i] == '\r' || rest[i] == '\n' {
				i++
				continue
			}
			break
		}
		if i < len(rest) && rest[i] == ',' {
			i++
		} else {
			break
		}
	}
	return sizes
}

// parseColumnList 解析 INSERT 的列清单
func parseColumnList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var cols []string
	for _, part := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.Trim(strings.TrimSpace(part), "`\" "))
		if name == "" {
			continue
		}
		cols = append(cols, name)
	}
	return cols
}

// parseSetColumns 解析 UPDATE 的 SET 子句，取出被赋值的列名
func parseSetColumns(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var cols []string
	for _, part := range strings.Split(raw, ",") {
		segment := strings.TrimSpace(part)
		idx := strings.Index(segment, "=")
		if idx <= 0 {
			continue
		}
		name := strings.ToLower(strings.Trim(strings.TrimSpace(segment[:idx]), "`\" "))
		if name == "" {
			continue
		}
		cols = append(cols, name)
	}
	return cols
}

// stripLeadingSQLComments 去掉前导注释与空白，保证 ^ 锚点正则能命中语句头
func stripLeadingSQLComments(text string) string {
	return strings.TrimSpace(sqlsplit.StripLeadingComments(text))
}

// ---------------------------------------------------------------------------
// 混合工单静态冲突分析
// ---------------------------------------------------------------------------

// OrderPlanConflict 混合工单内的一条冲突结论
type OrderPlanConflict struct {
	// Seq 触发问题的语句序号
	Seq int
	// Line 触发问题的语句所在行
	Line int
	// Level must（执行必然失败，阻断提交）/ suggest（高风险但不阻断）
	Level string
	// Category 错误类别，复用审核引擎的类别口径
	Category string
	// Message 结论
	Message string
	// Object 出错对象
	Object string
	// Reason 执行后果
	Reason string
	// Advice 修复建议
	Advice string
}

// analyzeOrderConflicts 检出「必然失败」的 DDL / DML 顺序写法。
//
// 只覆盖能静态确定的三类：
//  1. 表被 DROP / RENAME 之后，同表再次被 DML 或索引操作引用（执行必然失败）
//  2. 列被 DROP COLUMN / RENAME COLUMN 之后，同表 DML 仍引用旧列名（执行必然失败）
//  3. TRUNCATE 之前对同表做过 DML，那些变更会被清空（数据丢失，不阻断）
//
// 不做风格建议，也不推断跨表外键、视图依赖等需要读库才能确认的关系。
func analyzeOrderConflicts(plan OrderPlan) []OrderPlanConflict {
	type tableState struct {
		droppedAt      int
		droppedBy      string
		renamedTo      string
		droppedColumns map[string]int
		renamedColumns map[string]int
		// columns 工单内建表语句定义的列集（列名 → 建表语句序号）。
		// nil 表示列集未知（表在库里已存在、或建表写法无法静态确定列集）
		columns    map[string]int
		createdSeq int
	}
	states := map[string]*tableState{}
	stateOf := func(name string) *tableState {
		if name == "" {
			return nil
		}
		if s, ok := states[name]; ok {
			return s
		}
		s := &tableState{droppedColumns: map[string]int{}, renamedColumns: map[string]int{}}
		states[name] = s
		return s
	}
	dmlSeen := map[string]int{}

	var conflicts []OrderPlanConflict
	for _, st := range plan.Statements {
		table := identTail(st.Table)

		// ① 引用前先判定对象可用性
		if table != "" && (st.Kind.isDML() || st.Kind == stmtKindCreateIndex) {
			if s := states[table]; s != nil && s.droppedAt > 0 && s.droppedAt < st.Seq {
				how := s.droppedBy
				if s.renamedTo != "" {
					how = fmt.Sprintf("被 RENAME 为 `%s`", s.renamedTo)
				}
				conflicts = append(conflicts, OrderPlanConflict{
					Seq:      st.Seq,
					Line:     st.Line,
					Level:    "must",
					Category: controllers.CategoryObjectMissing,
					Object:   table,
					Message: fmt.Sprintf("第 %d 条语句引用了表`%s`，但该表已在第 %d 条语句%s，执行必然失败",
						st.Seq, table, s.droppedAt, how),
					Reason: fmt.Sprintf("表`%s`在第 %d 条语句后已不存在，本条语句下发到数据库会直接报「表不存在」", table, s.droppedAt),
					Advice: "① 把对该表的操作移到 DROP/RENAME 之前；② 若确需重建，请在本条语句之前用 CREATE TABLE 重新创建同名表；③ 若这是两件互不相关的变更，请拆成两个工单",
				})
			}
		}

		// ② 列级引用判定（仅 INSERT 列清单 / UPDATE 的 SET 列可静态判定）
		if s := states[table]; s != nil && len(st.RefColumns) > 0 {
			for _, col := range st.RefColumns {
				if seq, ok := s.droppedColumns[col]; ok && seq < st.Seq {
					conflicts = append(conflicts, OrderPlanConflict{
						Seq:      st.Seq,
						Line:     st.Line,
						Level:    "must",
						Category: controllers.CategoryObjectMissing,
						Object:   fmt.Sprintf("%s.%s", table, col),
						Message: fmt.Sprintf("第 %d 条语句引用了表`%s`的列`%s`，但该列已在第 %d 条语句被 DROP COLUMN 删除，执行必然失败",
							st.Seq, table, col, seq),
						Reason: "列已不存在，本条语句下发到数据库会直接报「字段不存在」",
						Advice: "① 把该 DML 移到 DROP COLUMN 之前；② 或改用新列名/删除对该列的引用",
					})
					continue
				}
				if seq, ok := s.renamedColumns[col]; ok && seq < st.Seq {
					conflicts = append(conflicts, OrderPlanConflict{
						Seq:      st.Seq,
						Line:     st.Line,
						Level:    "must",
						Category: controllers.CategoryObjectMissing,
						Object:   fmt.Sprintf("%s.%s", table, col),
						Message: fmt.Sprintf("第 %d 条语句引用了表`%s`的列`%s`，但该列已在第 %d 条语句被重命名，执行必然失败",
							st.Seq, table, col, seq),
						Reason: "旧列名已不存在，本条语句下发到数据库会直接报「字段不存在」",
						Advice: "① 把该 DML 移到改列名之前；② 或改用改名后的列名",
					})
					continue
				}
				// ②-b 工单内建表语句定义过的表：引用列必须在建表（或后续加列）里出现过。
				// 典型场景：`create table t(id int); insert into t(name)...` —— 审核引擎
				// 只对照库的当前状态，看不到「表还没建」，所以这里补上静态校验
				if _, ok := s.columns[col]; !ok && len(s.columns) > 0 {
					conflicts = append(conflicts, OrderPlanConflict{
						Seq:      st.Seq,
						Line:     st.Line,
						Level:    "must",
						Category: controllers.CategoryObjectMissing,
						Object:   fmt.Sprintf("%s.%s", table, col),
						Message: fmt.Sprintf("第 %d 条语句引用了表`%s`的字段`%s`，但工单内第 %d 条建表语句未定义该字段，执行必然失败",
							st.Seq, table, col, s.createdSeq),
						Reason: fmt.Sprintf("建表语句里没有字段`%s`，本条语句下发到数据库会直接报「Unknown column」", col),
						Advice: "① 核对字段拼写；② 若字段需要后建，请在本条语句之前先 ALTER TABLE 添加该字段；③ 或在建表语句中补上该字段定义",
					})
				}
			}
		}

		// ②-c VALUES 值个数与字段数比对（规则 insert_stmt_columns_match_values 的计划内兜底）。
		// 已有表由审核引擎比对真实表结构；这里只兜「表是本工单建的」——引擎读不到
		// 库里还不存在的表结构会整体跳过。有字段列表时与列表长度比对，无列表时与建表列集比对。
		if s := states[table]; s != nil && len(st.InsertValueCounts) > 0 && len(s.columns) > 0 {
			want, basis := 0, ""
			if len(st.RefColumns) > 0 {
				want, basis = len(st.RefColumns), "字段列表"
			} else {
				want, basis = len(s.columns), "建表语句"
			}
			for i, got := range st.InsertValueCounts {
				// 无字段列表时 `VALUES ()` 是合法的全默认值插入；有列表时值数必须一致
				if got == want || (got == 0 && len(st.RefColumns) == 0) {
					continue
				}
				conflicts = append(conflicts, OrderPlanConflict{
					Seq:      st.Seq,
					Line:     st.Line,
					Level:    "must",
					Category: controllers.CategoryObjectMissing,
					Object:   table,
					Message: fmt.Sprintf("第 %d 条语句为表`%s`的%s（%d 个字段）提供了 %d 个值（第 %d 行），执行必然失败",
						st.Seq, table, basis, want, got, i+1),
					Reason: "字段与值的数量不匹配，本条语句下发到数据库会直接报「Column count doesn't match value count」（Error 1136）",
					Advice: "① 使 VALUES 的值个数与字段个数一一对应；② 或按目标表字段补全字段列表",
				})
				break
			}
		}

		// ③ 更新对象状态
		switch st.Kind {
		case stmtKindCreateTable:
			// 重新建表：对象恢复可用，结构变更记录一并清空
			states[table] = &tableState{droppedColumns: map[string]int{}, renamedColumns: map[string]int{}}
			if len(st.CreateColumns) > 0 {
				cols := make(map[string]int, len(st.CreateColumns))
				for _, c := range st.CreateColumns {
					cols[c] = st.Seq
				}
				states[table].columns = cols
				states[table].createdSeq = st.Seq
			}
		case stmtKindDropTable:
			s := stateOf(table)
			s.droppedAt = st.Seq
			s.droppedBy = "被 DROP TABLE 删除"
			s.renamedTo = ""
			s.droppedColumns = map[string]int{}
			s.renamedColumns = map[string]int{}
		case stmtKindDropDatabase:
			// 库级删除影响该库下所有表：清空全部状态并记住删除点
			for _, s := range states {
				s.droppedAt = st.Seq
				s.droppedBy = "所在库被 DROP DATABASE 删除"
			}
		case stmtKindRenameTable:
			s := stateOf(table)
			s.droppedAt = st.Seq
			s.droppedBy = "被 RENAME"
			s.renamedTo = identTail(st.RenameTo)
			s.droppedColumns = map[string]int{}
			s.renamedColumns = map[string]int{}
			// 新名字从这一刻起可用
			states[identTail(st.RenameTo)] = &tableState{droppedColumns: map[string]int{}, renamedColumns: map[string]int{}}
		case stmtKindTruncateTable:
			if seq, ok := dmlSeen[table]; ok && seq < st.Seq {
				conflicts = append(conflicts, OrderPlanConflict{
					Seq:      st.Seq,
					Line:     st.Line,
					Level:    "suggest",
					Category: controllers.CategoryRuleViolation,
					Object:   table,
					Message: fmt.Sprintf("第 %d 条语句对表`%s`的数据变更，会被第 %d 条语句的 TRUNCATE 清空",
						seq, table, st.Seq),
					Reason: "TRUNCATE 会删除表中全部数据，前面那次变更不会保留",
					Advice: "① 把 TRUNCATE 移到所有 DML 之前；② 若确实需要清空后重灌，请确认前面的变更可以丢弃",
				})
			}
		case stmtKindAlterTable:
			s := stateOf(table)
			for _, col := range st.DropColumns {
				s.droppedColumns[col] = st.Seq
				delete(s.columns, col)
			}
			for _, col := range st.AddColumns {
				if s.columns != nil {
					s.columns[col] = st.Seq
				}
			}
			for oldName, newName := range st.RenameColumns {
				s.renamedColumns[oldName] = st.Seq
				if seq, ok := s.columns[oldName]; ok {
					delete(s.columns, oldName)
					s.columns[newName] = seq
				}
			}
		}

		if st.Kind.isDML() && table != "" {
			if _, ok := dmlSeen[table]; !ok {
				dmlSeen[table] = st.Seq
			}
		}
	}
	return conflicts
}

// identTail 取标识符最后一段并转小写，用于跨语句比较同一张表。
// 兼容 `db`.`t` / "public"."t" / db.t / t 四种写法。
func identTail(name string) string {
	v := strings.TrimSpace(name)
	if v == "" {
		return ""
	}
	if idx := strings.LastIndex(v, "."); idx >= 0 {
		v = strings.TrimSpace(v[idx+1:])
	}
	v = strings.Trim(v, "`\"[] ")
	return strings.ToLower(v)
}

// ---------------------------------------------------------------------------
// 与预审结果、工单类型校验的对接
// ---------------------------------------------------------------------------

// reconcileOrderSQLType 调和「客户端传入的 sql_type」与「按内容推断出的类型」。
//
// 内容是唯一权威：推断为 MIXED 时，客户端按单一类型提交（老前端不传或传 DDL/DML）
// 一律采用 MIXED，不再报「类型与内容不匹配」；其余不一致仍按错误处理，
// 否则可以靠改请求参数把 DML 冒充成只读或反向冒充。
func reconcileOrderSQLType(provided string, inferred commonModels.EnumType) (commonModels.EnumType, error) {
	given := strings.ToUpper(strings.TrimSpace(provided))
	got := commonModels.EnumType(strings.ToUpper(strings.TrimSpace(string(inferred))))
	if given == "" {
		return got, nil
	}
	if commonModels.EnumType(given) == got {
		return got, nil
	}
	if got == commonModels.EnumType(orderSQLTypeMixed) &&
		(given == orderSQLTypeDDL || given == orderSQLTypeDML) {
		return got, nil
	}
	return "", fmt.Errorf("工单SQL类型与SQL内容不匹配")
}

// appendOrderPlanFindings 把语句计划的两类结论并入预审结果：
//
//  1. 剔除「本工单自己会先建出来」的对象被误判为不存在的问题项；
//  2. 追加混合工单的顺序冲突结论。
//
// 并入而非另开接口：结论与规范问题项走同一套展示与阻断口径
// （items 里的 level 决定是否阻断提交），前端无需改动。
func appendOrderPlanFindings(result map[string]any, plan OrderPlan) {
	if result == nil {
		return
	}
	items := suppressSelfCreatedObjectMissing(preInspectItemsOf(result["items"]), plan)
	for _, c := range analyzeOrderConflicts(plan) {
		items = append(items, preInspectItem{
			Line:     c.Line,
			Level:    c.Level,
			Message:  c.Message,
			Query:    statementTextOfSeq(plan, c.Seq),
			Category: c.Category,
			Position: c.Object,
			Object:   c.Object,
			Reason:   c.Reason,
			Advice:   c.Advice,
		})
	}
	mustCount, suggestCount := 0, 0
	for _, it := range items {
		switch it.Level {
		case "must":
			mustCount++
		case "suggest":
			suggestCount++
		}
	}
	status := "passed"
	if mustCount > 0 {
		status = "must_fix"
	} else if suggestCount > 0 {
		status = "suggest"
	}
	result["items"] = items
	result["must_count"] = mustCount
	result["suggest_count"] = suggestCount
	result["status"] = status
	if plan.SQLType == orderSQLTypeMixed {
		result["sql_type"] = orderSQLTypeMixed
		result["ddl_count"] = plan.DDLCount
		result["dml_count"] = plan.DMLCount
	}
}

// suppressSelfCreatedObjectMissing 剔除「对象在本工单内稍后才被创建」的误报。
//
// 审核引擎是按**目标库当前真实状态**校验的：工单里第一条 `CREATE TABLE t`、
// 第二条 `ALTER TABLE t ...`，引擎看到库里还没有 t，就会给第二条判
// 「对象不存在（must）」——这条结论在整单执行时并不成立（执行到第二条时表已经建好了）。
// 对混合工单尤其致命：每一条 DML 都会被自己的建表语句"判死"，工单永远提交不了。
//
// 判定条件（保守）：
//   - 结论文本或对象字段能定位到具体对象名；
//   - 该对象在计划内被 CREATE TABLE / CREATE DATABASE 创建，且创建语句的行号不晚于问题行；
//   - 结论本身是「对象不存在」语义（object_missing 类别，或命中 1146/42P01 的对象不存在文案）。
//
// 任何一条不满足就保留原结论——宁可多报，也不要把真实问题压掉。
func suppressSelfCreatedObjectMissing(items []preInspectItem, plan OrderPlan) []preInspectItem {
	if len(items) == 0 {
		return items
	}
	createdAt := map[string]int{}
	for _, st := range plan.Statements {
		if st.Kind != stmtKindCreateTable && st.Kind != stmtKindCreateDatabase {
			continue
		}
		if name := identTail(st.Table); name != "" {
			createdAt[name] = st.Line
		}
	}
	if len(createdAt) == 0 {
		return items
	}
	kept := make([]preInspectItem, 0, len(items))
	for _, it := range items {
		if !isObjectMissingFinding(it) {
			kept = append(kept, it)
			continue
		}
		if createdEarlier := matchedCreatedObject(it, createdAt); createdEarlier {
			continue
		}
		kept = append(kept, it)
	}
	return kept
}

// isObjectMissingFinding 判断问题项是否为「对象不存在」语义
func isObjectMissingFinding(item preInspectItem) bool {
	if item.Category == controllers.CategoryObjectMissing {
		return true
	}
	msg := strings.ToLower(item.Message)
	for _, kw := range []string{"doesn't exist", "does not exist", "不存在", "42p01", "1146"} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// matchedCreatedObject 判断问题项指向的对象是否属于「本工单内已先行创建」的对象
func matchedCreatedObject(item preInspectItem, createdAt map[string]int) bool {
	for _, name := range objectNamesOf(item) {
		if line, ok := createdAt[name]; ok && line <= item.Line {
			return true
		}
	}
	return false
}

// quotedNameRe 匹配结论文案里的库表/字段标识符（'db.t'、`t`、"t"）
var quotedNameRe = regexp.MustCompile("`([^`]+)`|'([^']+)'|\"([^\"]+)\"")

// objectNamesOf 提取问题项可能指向的对象名（去库前缀、小写）
func objectNamesOf(item preInspectItem) []string {
	var names []string
	add := func(raw string) {
		if name := identTail(raw); name != "" {
			names = append(names, name)
		}
	}
	add(item.Object)
	for _, m := range quotedNameRe.FindAllStringSubmatch(item.Message, -1) {
		for i := 1; i < len(m); i++ {
			if m[i] != "" {
				add(m[i])
			}
		}
	}
	return names
}

// statementTextOfSeq 取指定序号的语句原文（用于预审结果里的语句定位展示）
func statementTextOfSeq(plan OrderPlan, seq int) string {
	for _, st := range plan.Statements {
		if st.Seq == seq {
			return strings.TrimSpace(st.Text)
		}
	}
	return ""
}

// preInspectItemsOf 兼容两种 items 承载形态：
// 实时预审返回的是 []preInspectItem（结构体），经 JSON 往返后是 []any。
func preInspectItemsOf(raw any) []preInspectItem {
	switch items := raw.(type) {
	case []preInspectItem:
		return items
	case *[]preInspectItem:
		if items == nil {
			return nil
		}
		return *items
	case []any:
		out := make([]preInspectItem, 0, len(items))
		for _, it := range items {
			switch v := it.(type) {
			case preInspectItem:
				out = append(out, v)
			case map[string]any:
				out = append(out, preInspectItem{
					Line:     intField(v, "line"),
					Level:    stringField(v, "level"),
					Message:  stringField(v, "message"),
					Query:    stringField(v, "query"),
					RuleKey:  stringField(v, "rule_key"),
					RuleName: stringField(v, "rule_name"),
					Category: stringField(v, "category"),
					Position: stringField(v, "position"),
					Object:   stringField(v, "object"),
					Reason:   stringField(v, "reason"),
					Advice:   stringField(v, "advice"),
				})
			}
		}
		return out
	default:
		return nil
	}
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func intField(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

// orderSQLTypeLabel 工单类型的中文展示文案（通知、日志、页面共用口径）
func orderSQLTypeLabel(sqlType string) string {
	switch strings.ToUpper(strings.TrimSpace(sqlType)) {
	case orderSQLTypeMixed:
		return "DDL + DML 混合"
	case orderSQLTypeDDL:
		return "DDL"
	case orderSQLTypeDML:
		return "DML"
	case orderSQLTypeExport:
		return "EXPORT"
	default:
		return sqlType
	}
}
