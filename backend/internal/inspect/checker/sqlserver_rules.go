package checker

// SQL Server 规范审核引擎
//
// 判定来源与其它引擎完全一致：实例绑定的 SQL 开发规范（经 BuildInspectParamsForInstance
// 映射为 config.InspectParams）。差别只在实现方式 —— T-SQL 与 TiDB parser 完全不兼容，
// 因此与 PostgreSQL/Oracle 同样采用「语法感知拆分 + 轻量文本解析」。
//
// 规则 key 使用「去掉数据库前缀的后缀」形式（与 MySQL 引擎一致），由 orders/services 的
// resolveInspectSeverity 关联回规范中的具体规则级别：
// must → 阻断提交审批；suggest → 提示但放行；off → 规则不检查（参数为默认关闭态）。
//
// T-SQL 与 MySQL/PG 的语义差异（引擎侧的关键适配点，逐条对应到规范规则）：
//
//	规范规则                           T-SQL 写法                     MySQL/PG 写法
//	limit_auto_inc_col_name_is_id      IDENTITY 列                    AUTO_INCREMENT / IDENTITY
//	limit_create_table_auto_inc_init   IDENTITY(seed, increment)      AUTO_INCREMENT=n / START WITH
//	limit_char/varchar_type_col_len    CHAR/NCHAR、VARCHAR/NVARCHAR   CHAR / VARCHAR
//	                                   VARCHAR(MAX) 视为超长
//	no_column_collate                  列级 COLLATE                   MySQL 列级 COLLATE
//	limit_col_must_have_comment        sp_addextendedproperty        内联 COMMENT / COMMENT ON
//	limit_tbl_must_comment             sp_addextendedproperty        同上
//	disable_create_table_as            SELECT ... INTO                CREATE TABLE ... AS SELECT
//	limit_update_delete_data_size      TOP (n)                        LIMIT n
//	insert_stmt_avoid_sysdate_func     GETDATE()/SYSDATETIME()        SYSDATE()/now()
//	obj_* / create_tbl_must_not_exist  sys.objects / sys.columns / sys.indexes（错误号 208/2714/3701）

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"

	"github.com/simajiong/jaysql/pkg/sqlsplit"
	"github.com/simajiong/jaysql/pkg/utils"
)

// sqlServerInspectContext 单次预审的上下文
type sqlServerInspectContext struct {
	params  config.InspectParams
	sqlText string
	db      *dao.DB // 可能为 nil（不连库时跳过依赖元信息的规则）
	schema  string  // 工单目标 schema，空则按 dbo

	// 工单内扩展属性（表/列注释）：tableKey -> columnKey -> true（"" 表示表级注释）
	comments map[string]map[string]bool
	// 工单内新增索引：tableKey -> 索引列表
	indexByTable map[string][]*sqlServerIndex
	// 工单内已有注释的表/列（来自 CREATE TABLE 之外的途径）
	meta sqlServerMetaResolver
}

// sqlServerSummary 构造一条审核项
func sqlServerSummary(ruleKey, format string, args ...any) controllers.SummaryItem {
	return controllers.SummaryItem{
		Level:   LevelWarn,
		RuleKey: ruleKey,
		Message: fmt.Sprintf(format, args...),
	}
}

// sqlServerObjectMissing 构造「执行必然失败」的对象缺失结论
func sqlServerObjectMissing(ruleKey, object, advice, format string, args ...any) controllers.SummaryItem {
	return controllers.SummaryItem{
		Level:    LevelWarn,
		RuleKey:  ruleKey,
		Category: controllers.CategoryObjectMissing,
		Object:   object,
		Advice:   advice,
		Message:  fmt.Sprintf(format, args...),
	}
}

// runSQLServerInspect SQL Server 工单规范预审入口
func (s *SyntaxInspectService) runSQLServerInspect() ([]ReturnData, error) {
	// GO 是客户端批处理分隔符，不是 T-SQL 语句：先剥离，否则会和后续语句粘成一条
	text := stripSQLServerGoBatch(s.SqlText)
	stmts, types, err := sqlsplit.SplitTypedExtra(text, sqlServerExtraDDLPrefixes)
	if err != nil {
		return nil, err
	}
	ctx := &sqlServerInspectContext{
		params:       s.InspectParams,
		sqlText:      text,
		db:           s.DB,
		schema:       s.InspectSchema,
		comments:     map[string]map[string]bool{},
		indexByTable: map[string][]*sqlServerIndex{},
		meta:         s.sqlserverMeta,
	}
	// 语句文本归一：展示用原文，解析用折叠空白的副本（理由同 PG 引擎，见 sql_whitespace.go）
	cleans := make([]string, len(stmts))
	parsed := make([]string, len(stmts))
	for i, stmt := range stmts {
		cleans[i] = strings.TrimSpace(sqlsplit.StripLeadingComments(stmt))
		parsed[i] = collapseSQLWhitespace(cleans[i])
	}
	ctx.collect(parsed)

	returnData := make([]ReturnData, 0, len(stmts))
	var alterTables []string
	for i := range stmts {
		clean := cleans[i]
		data := ReturnData{
			FingerId: "",
			Query:    clean,
			Type:     types[i],
			Line:     s.computeLine(clean),
		}
		if act, ok := parseSQLServerAlterTable(parsed[i]); ok && strings.TrimSpace(act.Name.Object) != "" {
			alterTables = append(alterTables, act.Name.Key())
		}
		data.Summary = ctx.inspectStatement(parsed[i], types[i])
		returnData = append(returnData, data)
	}
	if data := ctx.mergeAlterSummary(alterTables); len(data.Summary) > 0 {
		returnData = append(returnData, data)
	}
	return returnData, nil
}

// mergeAlterSummary 同一张表出现多条 ALTER 语句时提示合并。
// SQL Server 每条 ALTER TABLE 都要申请 Sch-M 架构修改锁，合并可显著缩短阻塞窗口。
func (c *sqlServerInspectContext) mergeAlterSummary(tables []string) ReturnData {
	data := ReturnData{FingerId: utils.GenerateSimpleRandomString(16)}
	if !c.params.ENABLE_MERGE_ALTER_TABLE || len(tables) < 2 {
		return data
	}
	ok, repeated := utils.IsRepeat(tables)
	if !ok {
		return data
	}
	for _, table := range repeated {
		data.Summary = append(data.Summary, sqlServerSummary("merge_multiple_alter_stmt_same_table",
			"[SQLServer数据库]表`%s`的多条ALTER操作，请合并为一条ALTER语句", table))
	}
	return data
}

// collect 预处理：收集扩展属性注释与 CREATE INDEX，供后续规则引用
func (c *sqlServerInspectContext) collect(stmts []string) {
	for _, stmt := range stmts {
		head := sqlsplit.NormalizeLeading(stmt)
		switch {
		case strings.Contains(head, "sp_addextendedproperty"), strings.Contains(head, "sp_updateextendedproperty"):
			props, ok := parseSQLServerExtendedProperty(stmt)
			if !ok {
				continue
			}
			for _, p := range props {
				key := p.Name.Key()
				if c.comments[key] == nil {
					c.comments[key] = map[string]bool{}
				}
				c.comments[key][strings.ToLower(unquoteSQLServerIdent(p.Column))] = true
			}
		case strings.HasPrefix(head, "create") && sqlServerObjectKind(head) == "index":
			def, ok := parseSQLServerCreateIndex(stmt)
			if !ok {
				continue
			}
			c.indexByTable[def.Table.Key()] = append(c.indexByTable[def.Table.Key()], def)
		}
	}
}

// inspectStatement 对单条语句执行全部适用规则
func (c *sqlServerInspectContext) inspectStatement(stmt string, sqlType string) []controllers.SummaryItem {
	head := sqlsplit.NormalizeLeading(stmt)
	kind := sqlServerObjectKind(head)
	switch {
	case strings.HasPrefix(head, "create table"):
		// 准入闸门：CTAS（CREATE TABLE ... AS SELECT）属「整类不允许执行」
		if rejected := c.gateCreateTable(stmt); len(rejected) > 0 {
			return rejected
		}
		return c.inspectCreateTable(stmt)
	case strings.HasPrefix(head, "alter table"):
		return c.inspectAlterTable(stmt)
	case kind == "index":
		return c.inspectCreateIndex(stmt)
	case strings.HasPrefix(head, "drop index"), strings.HasPrefix(head, "alter index"):
		return c.inspectIndexObject(stmt)
	case kind == "view":
		return c.inspectView(stmt)
	case kind == "procedure", kind == "function", kind == "trigger":
		return c.inspectRoutineDDL(stmt, kind)
	case strings.Contains(head, "sp_rename"):
		return c.inspectSpRename(stmt)
	case strings.Contains(head, "sp_addextendedproperty"), strings.Contains(head, "sp_updateextendedproperty"):
		// 注释写入语句本身不产生违规项（规则结论在建表/改表侧给出）
		return nil
	case strings.HasPrefix(head, "select"), strings.HasPrefix(head, "with"):
		// T-SQL 的 CTAS 是 SELECT ... INTO：属准入闸门
		return c.gateSelectInto(stmt)
	case sqlType == "DML":
		return c.inspectDML(stmt)
	default:
		return nil
	}
}

// ---------- CREATE TABLE ----------

func (c *sqlServerInspectContext) inspectCreateTable(stmt string) []controllers.SummaryItem {
	def, ok := parseSQLServerCreateTable(stmt)
	if !ok {
		return nil
	}
	var items []controllers.SummaryItem
	table := def.Name.Object

	// 表名规范
	if c.params.CHECK_TABLE_IDENTIFIER {
		pattern, caseText := sqlServerNamePattern(c.params.TABLE_NAME_CASE)
		if !pattern.MatchString(table) {
			items = append(items, sqlServerSummary("limit_tbl_name_case",
				"表`%s`命名不符合要求：必须为%s，仅允许匹配正则`%s`", table, caseText, pattern.String()))
		}
	}
	if c.params.CHECK_IDENTIFER_KEYWORD {
		if _, hit := c.params.IdentifierKeywordSetFor("sqlserver")[strings.ToUpper(table)]; hit {
			items = append(items, sqlServerSummary("tbl_name_no_keyword", "表`%s`命名不允许使用关键字", table))
		}
	}

	// 执行前置：表已存在（错误号 2714）
	if exists, err := c.metaResolver().TableExists(def.Name); err == nil && exists {
		items = append(items, sqlServerObjectConflict("create_tbl_must_not_exist", table,
			"① 更换表名（如 `"+table+"_new`）；② 需要重复执行请改用 `IF OBJECT_ID(N'表名', N'U') IS NULL` 包裹；③ 确需覆盖请先单独提交 `DROP TABLE` 工单（数据将丢失，需另行评估）",
			"表或视图`%s`已存在", table))
	}

	// 主键
	items = append(items, c.checkPrimaryKey(def)...)

	// 列级规则
	for _, col := range def.Columns {
		items = append(items, c.checkColumn(def.Name, col)...)
	}

	// 自增列（IDENTITY）规则
	items = append(items, c.checkIdentity(def)...)

	// 表字段数量上限
	if c.params.MAX_TABLE_COLUMNS > 0 && len(def.Columns) > c.params.MAX_TABLE_COLUMNS {
		items = append(items, sqlServerSummary("limit_table_column_count",
			"表`%s`字段数量超出限制（max = %d，当前 %d），请拆分表或精简字段",
			table, c.params.MAX_TABLE_COLUMNS, len(def.Columns)))
	}

	// 表必须包含的列
	items = append(items, c.checkRequiredColumns(def)...)

	// 列注释 / 表注释
	items = append(items, c.checkColumnComments(def)...)
	items = append(items, c.checkTableComment(def.Name)...)

	// 外键
	if def.HasForeignKey && !c.params.ENABLE_FOREIGN_KEY {
		items = append(items, sqlServerSummary("limit_tbl_no_use_fk", "表`%s`禁止定义外键", table))
	}

	// 表内唯一约束命名（口径与 CREATE UNIQUE INDEX 一致）
	for _, name := range def.UniqueNames {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if c.params.CHECK_UNIQ_INDEX_PREFIX {
			prefix := c.uniqueIndexPrefix()
			if !utils.MatchIndexName(name, c.params.UNIQ_INDEX_REGEX, prefix) {
				items = append(items, sqlServerSummary("limit_uniq_idx_name_fmt",
					"唯一索引`%s`命名不符合要求，必须%s（表`%s`）",
					name, utils.IndexNameRequirementText(c.params.UNIQ_INDEX_REGEX, prefix), table))
			}
		}
	}

	// 工单内表内联索引计入索引数量与冗余判定
	c.indexByTable[def.Name.Key()] = append(c.indexByTable[def.Name.Key()], def.UniqueIndexes...)
	items = append(items, c.checkIndexCount(def.Name)...)
	return items
}

// ---------- 列 ----------

// checkColumn 列级规则
func (c *sqlServerInspectContext) checkColumn(table sqlServerName, col sqlServerColumn) []controllers.SummaryItem {
	name := col.Name
	tableName := table.Object
	var items []controllers.SummaryItem

	if col.IsComputed {
		// 计算列没有类型声明，也不参与默认值/注释/长度类规则
		return items
	}

	if c.params.CHECK_COLUMN_IDENTIFIER {
		pattern, caseText := sqlServerNamePattern(c.params.COLUMN_NAME_CASE)
		if !pattern.MatchString(name) {
			items = append(items, sqlServerSummary("limit_col_name_case",
				"列`%s`命名不符合要求：必须为%s，仅允许匹配正则`%s`（表`%s`）", name, caseText, pattern.String(), tableName))
		}
	}
	if c.params.CHECK_IDENTIFER_KEYWORD {
		if _, hit := c.params.IdentifierKeywordSetFor("sqlserver")[strings.ToUpper(name)]; hit {
			items = append(items, sqlServerSummary("col_name_no_keyword", "列`%s`命名不允许使用关键字（表`%s`）", name, tableName))
		}
	}

	// CHAR / NCHAR 长度
	if c.params.COLUMN_MAX_CHAR_LENGTH > 0 && isSQLServerCharType(col.Type) && col.Len > c.params.COLUMN_MAX_CHAR_LENGTH {
		items = append(items, sqlServerSummary("limit_char_type_col_len",
			"列`%s`长度太大（max = %d），当前char长度为%d，请调整长度或改用varchar类型（表`%s`）",
			name, c.params.COLUMN_MAX_CHAR_LENGTH, col.Len, tableName))
	}
	// VARCHAR / NVARCHAR 长度（MAX 视为超限）
	if c.params.MAX_VARCHAR_LENGTH > 0 && isSQLServerVarcharType(col.Type) {
		if col.IsMax {
			items = append(items, sqlServerSummary("limit_varchar_type_col_len",
				"列`%s`长度太大（max = %d），当前使用%s(MAX)，请改用NVARCHAR(%d)或拆分子表（表`%s`）",
				name, c.params.MAX_VARCHAR_LENGTH, strings.ToUpper(col.Type), c.params.MAX_VARCHAR_LENGTH, tableName))
		} else if col.Len > c.params.MAX_VARCHAR_LENGTH {
			items = append(items, sqlServerSummary("limit_varchar_type_col_len",
				"列`%s`长度太大（max = %d），当前varchar长度为%d（表`%s`）",
				name, c.params.MAX_VARCHAR_LENGTH, col.Len, tableName))
		}
	}

	if c.params.ENABLE_COLUMN_NOT_NULL && !col.NotNull && !col.IsIdentity && !sqlServerAllowsNullType(col.Type, col.IsMax) {
		items = append(items, sqlServerSummary("limit_col_not_null", "列`%s`必须定义为`NOT NULL`（表`%s`）", name, tableName))
	}
	if c.params.CHECK_COLUMN_DEFAULT_VALUE && !col.HasDefault && !sqlServerCannotHaveDefaultType(col.Type, col.IsMax) {
		items = append(items, sqlServerSummary("limit_col_must_have_default", "列`%s`需要设置一个默认值（表`%s`）", name, tableName))
	}
	if c.params.CHECK_COLUMN_FORBIDDEN_TYPES {
		if c.params.IsForbiddenSQLServerType(col.Type) {
			items = append(items, sqlServerSummary("limit_col_no_partial_data_type",
				"列`%s`不允许定义%s类型（表`%s`）", name, strings.ToUpper(col.Type), tableName))
		}
	}
	if c.params.CHECK_COLUMN_COLLATION && col.HasCollate {
		items = append(items, sqlServerSummary("no_column_collate",
			"列`%s`不允许显式设置COLLATE（当前%s），请统一跟随库默认排序规则（表`%s`）",
			name, col.CollateName, tableName))
	}
	return items
}

// checkIdentity 自增列（IDENTITY）规则
func (c *sqlServerInspectContext) checkIdentity(def *sqlServerTable) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	for _, col := range def.Columns {
		if !col.IsIdentity {
			continue
		}
		if c.params.CHECK_AUTOINCREMENT_COL_NAME_IS_ID && !strings.EqualFold(col.Name, "id") {
			items = append(items, sqlServerSummary("limit_auto_inc_col_name_is_id",
				"表`%s`的自增列名为`%s`，规范要求自增列名必须为`ID`", def.Name.Object, col.Name))
		}
		if c.params.CHECK_TABLE_AUTOINCREMENT_INIT_VALUE && col.IdentitySeed > 1 {
			max := c.params.TABLE_AUTOINCREMENT_INIT_VALUE
			if max <= 0 {
				max = 1
			}
			if col.IdentitySeed > max {
				items = append(items, sqlServerSummary("limit_create_table_auto_inc_init",
					"表`%s`自增列`%s`的初始值（IDENTITY seed）为 %d，超过上限 %d",
					def.Name.Object, col.Name, col.IdentitySeed, max))
			}
		}
	}
	return items
}

// checkRequiredColumns 表必须包含指定列
func (c *sqlServerInspectContext) checkRequiredColumns(def *sqlServerTable) []controllers.SummaryItem {
	if len(c.params.TABLE_REQUIRED_COLUMNS) == 0 {
		return nil
	}
	have := make(map[string]bool, len(def.Columns))
	for _, col := range def.Columns {
		have[strings.ToLower(col.Name)] = true
	}
	var missing []string
	for _, need := range c.params.TABLE_REQUIRED_COLUMNS {
		name := strings.ToLower(strings.TrimSpace(need))
		if name == "" {
			continue
		}
		if !have[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []controllers.SummaryItem{sqlServerSummary("table_must_include_required_columns",
		"表`%s`缺少必需的列：%s", def.Name.Object, strings.Join(missing, ", "))}
}

// checkColumnComments 列注释规则。
//
// SQL Server 没有内联 COMMENT，注释只能通过 sp_addextendedproperty 写入扩展属性：
//   - 工单内提供了该列的扩展属性 => 满足；
//   - 未提供时连库看 sys.extended_properties 是否已有 MS_Description => 满足；
//   - 两者都不满足 => 报告；连库失败按未注释处理（与 PG 口径一致）。
func (c *sqlServerInspectContext) checkColumnComments(def *sqlServerTable) []controllers.SummaryItem {
	if !c.params.CHECK_COLUMN_COMMENT || len(def.Columns) == 0 {
		return nil
	}
	inTicket := c.comments[def.Name.Key()]
	meta := c.metaResolver()
	var items []controllers.SummaryItem
	for _, col := range def.Columns {
		if col.IsComputed {
			continue
		}
		if inTicket != nil && inTicket[strings.ToLower(col.Name)] {
			continue
		}
		if has, err := meta.ColumnCommented(def.Name, col.Name); err == nil && has {
			continue
		}
		items = append(items, sqlServerSummary("limit_col_must_have_comment",
			"列`%s`缺少注释（表`%s`，建议用 sp_addextendedproperty 补充 MS_Description 扩展属性）",
			col.Name, def.Name.Object))
	}
	return items
}

// checkTableComment 表注释规则（判定口径与列注释一致）
func (c *sqlServerInspectContext) checkTableComment(table sqlServerName) []controllers.SummaryItem {
	if !c.params.CHECK_TABLE_COMMENT {
		return nil
	}
	if inTicket := c.comments[table.Key()]; inTicket != nil && inTicket[""] {
		return nil
	}
	if has, err := c.metaResolver().TableCommented(table); err == nil && has {
		return nil
	}
	return []controllers.SummaryItem{sqlServerSummary("limit_tbl_must_comment",
		"表`%s`缺少注释（建议用 sp_addextendedproperty 补充 MS_Description 扩展属性）",
		table.Object)}
}

// ---------- 主键 ----------

func (c *sqlServerInspectContext) checkPrimaryKey(def *sqlServerTable) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	if c.params.CHECK_TABLE_PRIMARY_KEY {
		if len(def.PrimaryKeys) == 0 {
			items = append(items, sqlServerSummary("table_must_have_pk",
				"表`%s`必须定义主键（建议 `id BIGINT IDENTITY(1,1) PRIMARY KEY`），否则退化为堆表", def.Name.Object))
		}
		if len(def.PrimaryKeys) > 1 {
			items = append(items, sqlServerSummary("table_must_have_pk",
				"表`%s`主键定义异常：只能定义一个主键约束", def.Name.Object))
		}
	}
	return append(items, c.checkPrimaryKeyDetail(def)...)
}

// checkPrimaryKeyDetail 主键列类型 / 列数 / 自增
func (c *sqlServerInspectContext) checkPrimaryKeyDetail(def *sqlServerTable) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	table := def.Name.Object
	allowed := c.params.PRIMARYKEY_SUPPORT_TYPES
	colByKey := make(map[string]sqlServerColumn, len(def.Columns))
	for _, col := range def.Columns {
		colByKey[strings.ToLower(col.Name)] = col
	}
	if c.params.CHECK_PRIMARYKEY_USE_BIGINT && len(allowed) > 0 {
		for _, key := range def.PrimaryKeys {
			col, ok := colByKey[strings.ToLower(key)]
			if !ok {
				continue // 列定义不在同一条语句中，无法判定，不报
			}
			if !sqlServerTypeMatches(col.Type, allowed) {
				items = append(items, sqlServerSummary("limit_pk_col_type",
					"主键`%s`类型不符合要求，仅允许使用%s（表`%s`）", key, strings.Join(allowed, ", "), table))
			}
		}
	}
	if c.params.CHECK_PRIMARYKEY_USE_AUTO_INCREMENT && len(def.PrimaryKeys) == 1 {
		col, ok := colByKey[strings.ToLower(def.PrimaryKeys[0])]
		if ok && !col.IsIdentity {
			items = append(items, sqlServerSummary("limit_pk_col_must_auto_inc",
				"主键`%s`必须定义为自增列（IDENTITY）（表`%s`）", def.PrimaryKeys[0], table))
		}
	}
	if c.params.PRIMARYKEY_MAX_KEY_PARTS > 0 && len(def.PrimaryKeys) > c.params.PRIMARYKEY_MAX_KEY_PARTS {
		items = append(items, sqlServerSummary("limit_pk_column_count",
			"主键包含的列数超出限制（max = %d，当前 %d）（表`%s`）",
			c.params.PRIMARYKEY_MAX_KEY_PARTS, len(def.PrimaryKeys), table))
	}
	return items
}

// sqlServerTypeAliases SQL Server 类型同义词 → 白名单里的规范名
var sqlServerTypeAliases = map[string]string{
	"integer":                    "int",
	"numeric":                    "decimal",
	"dec":                        "decimal",
	"character":                  "char",
	"char varying":               "varchar",
	"national char":              "nchar",
	"national character":         "nchar",
	"national character varying": "nvarchar",
	"double precision":           "float",
	"rowversion":                 "timestamp",
	"binary varying":             "varbinary",
}

// sqlServerTypeMatches 判断列类型是否命中白名单（先做同义词归一化再比对，大小写不敏感）
func sqlServerTypeMatches(typ string, allowed []string) bool {
	t := strings.ToLower(strings.TrimSpace(typ))
	if canon, ok := sqlServerTypeAliases[t]; ok {
		t = canon
	}
	for _, name := range allowed {
		n := strings.ToLower(strings.TrimSpace(name))
		if n == "" {
			continue
		}
		if canon, ok := sqlServerTypeAliases[n]; ok {
			n = canon
		}
		if n == t {
			return true
		}
	}
	return false
}

// isSQLServerCharType 定长字符类型（CHAR / NCHAR）
func isSQLServerCharType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "char", "nchar":
		return true
	default:
		return false
	}
}

// isSQLServerVarcharType 变长字符类型（VARCHAR / NVARCHAR）
func isSQLServerVarcharType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "varchar", "nvarchar":
		return true
	default:
		return false
	}
}

// sqlServerAllowsNullType 允许为 NULL 的类型（对齐 MySQL/PG 引擎的 text/json/时间类型豁免口径）
func sqlServerAllowsNullType(t string, isMax bool) bool {
	if isMax {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "text", "ntext", "image", "xml", "sql_variant", "geography", "geometry", "hierarchyid",
		"datetime", "datetime2", "smalldatetime", "datetimeoffset", "date", "time", "timestamp":
		return true
	default:
		return false
	}
}

// sqlServerCannotHaveDefaultType 不能设默认值的类型（对齐 MySQL/PG 的 BLOB/TEXT/JSON 例外）
func sqlServerCannotHaveDefaultType(t string, isMax bool) bool {
	if isMax {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "text", "ntext", "image", "xml", "sql_variant", "timestamp", "geography", "geometry", "hierarchyid":
		return true
	default:
		return false
	}
}

// sqlServerNamePattern 按规范里的 name_case 取表名/列名正则
func sqlServerNamePattern(nameCase string) (*regexp.Regexp, string) {
	if strings.EqualFold(strings.TrimSpace(nameCase), "upper") {
		return sqlServerUpperNamePattern, "大写"
	}
	return sqlServerLowerNamePattern, "小写"
}

// ---------- ALTER TABLE ----------

func (c *sqlServerInspectContext) inspectAlterTable(stmt string) []controllers.SummaryItem {
	act, ok := parseSQLServerAlterTable(stmt)
	if !ok {
		return nil
	}
	var items []controllers.SummaryItem
	meta := c.metaResolver()
	table := act.Name.Object

	// 目标表存在性（错误号 208）。确认不存在时不再逐列判定，由表级结论代表。
	tableExists := false
	if exists, err := meta.TableExists(act.Name); err == nil {
		tableExists = exists
		if !exists {
			items = append(items, sqlServerObjectMissing("obj_tbl_must_exist", table,
				"① 核对表名拼写与 schema（SQL Server 默认 schema 是 dbo）；② 若该表应由本工单创建，请调整语句顺序；③ 若确认存在仍被报缺失，请先同步实例元数据再重新检查",
				"表`%s`不存在，无法执行 `ALTER TABLE`", table))
		}
	}
	if !tableExists {
		return items
	}

	for _, col := range act.DropColumns {
		if exists, err := meta.ColumnExists(act.Name, col); err == nil && !exists {
			items = append(items, sqlServerObjectMissing("obj_col_must_exist", col,
				"① 核对列名拼写；② 该列可能已被其他工单删除，请先同步实例元数据再重新检查",
				"表`%s`中要 DROP 的列`%s`不存在", table, col))
		}
	}
	for _, col := range act.AlterColumns {
		if exists, err := meta.ColumnExists(act.Name, col); err == nil && !exists {
			items = append(items, sqlServerObjectMissing("obj_col_must_exist", col,
				"① 核对列名拼写；② 若是新增列请改用 `ADD 列名 类型`；③ 该列可能已被其他工单删除",
				"列`%s`不存在（表`%s`）", col, table))
		}
	}
	for _, idx := range act.DropIndexes {
		if exists, err := meta.IndexExists(act.Name, idx); err == nil && !exists {
			items = append(items, sqlServerObjectMissing("obj_idx_must_exist", idx,
				"① 核对索引名拼写；② SQL Server 的 `DROP INDEX` 必须带 `ON 表名`；③ 需要幂等删除请改用 `IF EXISTS`",
				"索引`%s`不存在于表`%s`（无法执行 `DROP INDEX`）", idx, table))
		}
	}

	// 新增列的列级规则（含注释）
	if len(act.AddColumns) > 0 {
		for _, col := range act.AddColumns {
			items = append(items, c.checkColumn(act.Name, col)...)
		}
		items = append(items, c.checkIdentity(&sqlServerTable{Name: act.Name, Columns: act.AddColumns})...)
		items = append(items, c.checkColumnComments(&sqlServerTable{Name: act.Name, Columns: act.AddColumns})...)
	}
	// 新增主键：列类型只能靠元信息判定，这里只做列数校验
	if len(act.AddPK) > 0 && c.params.PRIMARYKEY_MAX_KEY_PARTS > 0 && len(act.AddPK) > c.params.PRIMARYKEY_MAX_KEY_PARTS {
		items = append(items, sqlServerSummary("limit_pk_column_count",
			"主键包含的列数超出限制（max = %d，当前 %d）（表`%s`）",
			c.params.PRIMARYKEY_MAX_KEY_PARTS, len(act.AddPK), table))
	}
	if act.AddFK && !c.params.ENABLE_FOREIGN_KEY {
		items = append(items, sqlServerSummary("limit_tbl_no_use_fk", "表`%s`禁止定义外键", table))
	}
	// 新增唯一约束命名
	if strings.TrimSpace(act.AddUniqueName) != "" && len(act.AddUnique) > 0 && c.params.CHECK_UNIQ_INDEX_PREFIX {
		prefix := c.uniqueIndexPrefix()
		if !utils.MatchIndexName(act.AddUniqueName, c.params.UNIQ_INDEX_REGEX, prefix) {
			items = append(items, sqlServerSummary("limit_uniq_idx_name_fmt",
				"唯一索引`%s`命名不符合要求，必须%s（表`%s`）",
				act.AddUniqueName, utils.IndexNameRequirementText(c.params.UNIQ_INDEX_REGEX, prefix), table))
		}
	}
	return items
}

// ---------- 索引 ----------

func (c *sqlServerInspectContext) inspectCreateIndex(stmt string) []controllers.SummaryItem {
	def, ok := parseSQLServerCreateIndex(stmt)
	if !ok {
		return nil
	}
	var items []controllers.SummaryItem
	table := def.Table.Object

	if c.params.CHECK_INDEX_NAME_REQUIRED && (!def.Explicit || def.Name == "") {
		items = append(items, sqlServerSummary("index_must_have_name", "索引必须显式命名（表`%s`）", table))
	} else if def.Name != "" && def.Unique {
		if c.params.CHECK_UNIQ_INDEX_PREFIX {
			prefix := c.uniqueIndexPrefix()
			if !utils.MatchIndexName(def.Name, c.params.UNIQ_INDEX_REGEX, prefix) {
				items = append(items, sqlServerSummary("limit_uniq_idx_name_fmt",
					"唯一索引`%s`命名不符合要求，必须%s（表`%s`）",
					def.Name, utils.IndexNameRequirementText(c.params.UNIQ_INDEX_REGEX, prefix), table))
			}
		}
	} else if def.Name != "" && c.params.CHECK_SECONDARY_INDEX_PREFIX {
		prefix := c.secondaryIndexPrefix()
		if !utils.MatchIndexName(def.Name, c.params.SECONDARY_INDEX_REGEX, prefix) {
			items = append(items, sqlServerSummary("limit_normal_idx_name_fmt",
				"普通索引`%s`命名不符合要求，必须%s（表`%s`）",
				def.Name, utils.IndexNameRequirementText(c.params.SECONDARY_INDEX_REGEX, prefix), table))
		}
	}

	if c.params.SECONDARY_INDEX_MAX_KEY_PARTS > 0 && len(def.Columns) > c.params.SECONDARY_INDEX_MAX_KEY_PARTS {
		items = append(items, sqlServerSummary("limit_single_index_column_count",
			"索引`%s`包含的列数超出限制（max = %d，当前 %d）（表`%s`）",
			def.Name, c.params.SECONDARY_INDEX_MAX_KEY_PARTS, len(def.Columns), table))
	}

	// 同表内索引重名（SQL Server 的索引名在表内唯一，跨表可重复）
	items = append(items, c.checkIndexNameDuplicate(def)...)
	items = append(items, c.checkIndexCount(def.Table)...)
	items = append(items, c.checkRedundantIndexes(def)...)
	return items
}

// checkIndexNameDuplicate 单表内索引名不允许重复（规则 index_name_no_duplicate）。
// 与 MySQL 口径一致：只比同一张表内、同一工单里的索引名。
func (c *sqlServerInspectContext) checkIndexNameDuplicate(cur *sqlServerIndex) []controllers.SummaryItem {
	if strings.TrimSpace(cur.Name) == "" {
		return nil
	}
	var items []controllers.SummaryItem
	group := c.indexByTable[cur.Table.Key()]
	seen := 0
	for _, other := range group {
		if !strings.EqualFold(strings.TrimSpace(other.Name), strings.TrimSpace(cur.Name)) {
			continue
		}
		seen++
	}
	if seen > 1 {
		items = append(items, sqlServerSummary("index_name_no_duplicate",
			"表`%s`内索引名`%s`重复定义，索引名在单表内必须唯一", cur.Table.Object, cur.Name))
	}
	return items
}

// checkRedundantIndexes 冗余索引检查（规则 limit_tbl_redundant_idx）。
// 判定口径与 MySQL/PG 引擎一致：重复索引（列组合相同）或前缀覆盖（已有索引是当前索引的最左前缀）。
func (c *sqlServerInspectContext) checkRedundantIndexes(cur *sqlServerIndex) []controllers.SummaryItem {
	if c.params.ENABLE_REDUNDANT_INDEX {
		return nil
	}
	group := c.indexByTable[cur.Table.Key()]
	curPos := -1
	for i, def := range group {
		if pgSameIndexDef(def.pgLike(), cur.pgLike()) {
			curPos = i
			break
		}
	}
	if curPos < 0 {
		return nil
	}
	var items []controllers.SummaryItem
	curCols := pgNormCols(cur.Columns)
	for i, other := range group {
		if i == curPos {
			continue
		}
		otherCols := pgNormCols(other.Columns)
		if len(otherCols) == 0 || len(curCols) == 0 {
			continue
		}
		switch {
		case len(otherCols) < len(curCols) && pgColsPrefix(curCols, otherCols):
			// other 是 cur 的最左前缀，由更宽的一侧报告
		case len(otherCols) == len(curCols) && pgColsEqual(curCols, otherCols) && i > curPos:
			// 重复索引：只由位置靠后的一方报告
		default:
			continue
		}
		items = append(items, sqlServerSummary("limit_tbl_redundant_idx",
			"表`%s`发现了冗余索引，冗余索引的字段组合为%s/%s",
			other.Table.Object, sqlServerIndexLabel(cur), sqlServerIndexLabel(other)))
	}
	return items
}

// pgLike 转成 PG 引擎的索引结构，复用其冗余判定与比较工具（避免两套实现漂移）
func (i *sqlServerIndex) pgLike() *pgIndexDef {
	if i == nil {
		return nil
	}
	return &pgIndexDef{
		Name:     i.Name,
		Table:    i.Table.Object,
		Columns:  i.Columns,
		Unique:   i.Unique,
		Explicit: i.Explicit,
	}
}

// sqlServerIndexLabel 索引展示标签
func sqlServerIndexLabel(def *sqlServerIndex) string {
	name := def.Name
	if strings.TrimSpace(name) == "" {
		name = "(匿名)"
	}
	return fmt.Sprintf("%s(%s)", name, strings.Join(pgNormCols(def.Columns), ","))
}

// checkIndexCount 索引数量上限（工单内新增 + 库中已有）
func (c *sqlServerInspectContext) checkIndexCount(table sqlServerName) []controllers.SummaryItem {
	if c.params.MAX_INDEX_KEYS <= 0 {
		return nil
	}
	count := len(c.indexByTable[table.Key()])
	if existing, err := c.metaResolver().IndexCount(table); err == nil {
		count += existing
	}
	if count > c.params.MAX_INDEX_KEYS {
		return []controllers.SummaryItem{sqlServerSummary("limit_tbl_idx_count",
			"表`%s`索引数量超出限制（max = %d，当前 %d）", table.Object, c.params.MAX_INDEX_KEYS, count)}
	}
	return nil
}

// inspectIndexObject DROP INDEX / ALTER INDEX 上的索引存在性判定。
//
// T-SQL 的 DROP INDEX 必须写 `DROP INDEX 索引名 ON 表名`；ALTER INDEX 只有
// REBUILD/REORGANIZE/DISABLE（改名走 sp_rename），因此这里只判存在性。
func (c *sqlServerInspectContext) inspectIndexObject(stmt string) []controllers.SummaryItem {
	clean := stripPGStringsAndComments(stmt)
	lower := strings.ToLower(strings.TrimSpace(clean))
	var rest string
	switch {
	case strings.HasPrefix(lower, "drop index"):
		rest = strings.TrimSpace(clean[len("drop index"):])
	case strings.HasPrefix(lower, "alter index"):
		rest = strings.TrimSpace(clean[len("alter index"):])
	default:
		return nil
	}
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(rest), "if exists"), ""))
	// 索引名与表名：`idx ON [schema.]t`
	nameToken := firstSQLServerToken(rest)
	// 上一行可能把小写化的文本传进来，这里重新从原文取
	origRest := rest
	if strings.HasPrefix(lower, "drop index") {
		origRest = strings.TrimSpace(clean[len("drop index"):])
	} else {
		origRest = strings.TrimSpace(clean[len("alter index"):])
	}
	lowerOrig := strings.ToLower(origRest)
	if strings.HasPrefix(lowerOrig, "if exists") {
		origRest = strings.TrimSpace(origRest[len("if exists"):])
	}
	nameToken = firstSQLServerToken(origRest)
	if nameToken == "" {
		return nil
	}
	indexName := unquoteSQLServerIdent(nameToken)
	remaining := strings.TrimSpace(origRest[len(nameToken):])
	if !strings.HasPrefix(strings.ToLower(remaining), "on") {
		return nil
	}
	remaining = strings.TrimSpace(remaining[len("on"):])
	tableName, ok := parseSQLServerName(firstSQLServerToken(remaining))
	if !ok {
		return nil
	}
	if exists, err := c.metaResolver().IndexExists(tableName, indexName); err == nil && !exists {
		return []controllers.SummaryItem{sqlServerObjectMissing("obj_idx_must_exist", indexName,
			"① 核对索引名与表名拼写；② 该索引可能已被其他工单删除，请先同步实例元数据再重新检查",
			"索引`%s`不存在于表`%s`", indexName, tableName.Object)}
	}
	return nil
}

func (c *sqlServerInspectContext) uniqueIndexPrefix() string {
	if p := strings.TrimSpace(c.params.UNQI_INDEX_PREFIX); p != "" {
		return p
	}
	return "UK_"
}

func (c *sqlServerInspectContext) secondaryIndexPrefix() string {
	if p := strings.TrimSpace(c.params.SECONDARY_INDEX_PREFIX); p != "" {
		return p
	}
	return "IDX_"
}

// ---------- sp_rename ----------

// inspectSpRename 改名动作的对象存在性/冲突判定（T-SQL 没有 RENAME 语法）。
func (c *sqlServerInspectContext) inspectSpRename(stmt string) []controllers.SummaryItem {
	ren, ok := parseSQLServerSpRename(stmt)
	if !ok {
		return nil
	}
	meta := c.metaResolver()
	var items []controllers.SummaryItem
	switch ren.Kind {
	case "TABLE":
		oldName := sqlServerName{Schema: ren.Table.Schema, Object: ren.OldName}
		if exists, err := meta.TableExists(oldName); err == nil && !exists {
			items = append(items, sqlServerObjectMissing("obj_tbl_must_exist", ren.OldName,
				"① 核对原表名拼写；② 该表可能已被其他工单删除或改名，请先同步实例元数据再重新检查",
				"原表`%s`不存在，无法重命名为`%s`", ren.OldName, ren.NewName))
		}
		newName := sqlServerName{Schema: ren.Table.Schema, Object: ren.NewName}
		if exists, err := meta.TableExists(newName); err == nil && exists {
			items = append(items, sqlServerObjectConflict("obj_tbl_must_not_exist", ren.NewName,
				"① 更换新表名；② 若目标就是覆盖，请先单独提交 DROP TABLE 工单（数据将丢失，需另行评估）",
				"目标表名`%s`已存在，无法将表`%s`重命名为该名称", ren.NewName, ren.OldName))
		}
	case "COLUMN":
		if exists, err := meta.ColumnExists(ren.Table, ren.OldName); err == nil && !exists {
			items = append(items, sqlServerObjectMissing("obj_col_must_exist", ren.OldName,
				"① 核对原列名拼写；② 该列可能已被其他工单删除或改名",
				"表`%s`中原列`%s`不存在，无法重命名为`%s`", ren.Table.Object, ren.OldName, ren.NewName))
		}
		if !strings.EqualFold(ren.OldName, ren.NewName) {
			if exists, err := meta.ColumnExists(ren.Table, ren.NewName); err == nil && exists {
				items = append(items, sqlServerObjectConflict("obj_col_must_not_exist", ren.NewName,
					"① 换一个新列名；② 若目标就是覆盖，请先单独提交删除/改名工单，避免改名产生列冲突",
					"表`%s`中新列`%s`已存在，`sp_rename` 会导致列名冲突", ren.Table.Object, ren.NewName))
			}
		}
	case "INDEX":
		if exists, err := meta.IndexExists(ren.Table, ren.OldName); err == nil && !exists {
			items = append(items, sqlServerObjectMissing("obj_idx_must_exist", ren.OldName,
				"① 核对索引名与表名拼写；② 该索引可能已被其他工单删除",
				"索引`%s`不存在（表`%s`），无法重命名为`%s`", ren.OldName, ren.Table.Object, ren.NewName))
		}
		if exists, err := meta.IndexExists(ren.Table, ren.NewName); err == nil && exists {
			items = append(items, sqlServerObjectConflict("obj_idx_must_not_exist", ren.NewName,
				"① 换一个新索引名；② 若目标就是覆盖，请先单独提交 DROP INDEX 工单，避免重命名冲突",
				"新索引名`%s`已存在（表`%s`），无法把索引`%s`重命名为该名称",
				ren.NewName, ren.Table.Object, ren.OldName))
		}
	}
	return items
}

// ---------- 存储过程 / 函数 / 触发器 ----------

// inspectRoutineDDL CREATE/ALTER/DROP PROCEDURE|FUNCTION|TRIGGER 的对象类规则。
func (c *sqlServerInspectContext) inspectRoutineDDL(stmt string, kind string) []controllers.SummaryItem {
	var ruleKey, label, advice string
	var allowed bool
	switch kind {
	case "procedure":
		ruleKey, label, allowed = "disable_proc_related_op", "存储过程", c.params.ENABLE_PROCEDURE_OBJECT
		advice = "若确需存储过程，请联系 DBA 在实例绑定的「SQL 开发规范」中关闭「禁止存储过程相关操作」规则"
	case "function":
		ruleKey, label, allowed = "disable_custom_function_related_op", "自定义函数", c.params.ENABLE_FUNCTION_OBJECT
		advice = "若确需自定义函数，请联系 DBA 在实例绑定的「SQL 开发规范」中关闭「禁止自定义函数相关操作」规则"
	case "trigger":
		ruleKey, label, allowed = "disable_trigger_related_op", "触发器", c.params.ENABLE_TRIGGER_OBJECT
		advice = "若确需触发器，请联系 DBA 在实例绑定的「SQL 开发规范」中关闭「禁止触发器相关操作」规则"
	default:
		return nil
	}
	if allowed {
		return nil
	}
	head := sqlsplit.NormalizeLeading(stmt)
	object := firstSQLServerToken(strings.TrimSpace(
		strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(head, "create or alter "), "create "), "alter "), "drop ")))
	// 从原文取对象名（head 已被小写化）
	obj := sqlServerRoutineName(stmt)
	desc := fmt.Sprintf("`%s`", label)
	if obj != "" {
		object = obj
		desc = fmt.Sprintf("`%s`（名字`%s`）", label, obj)
	}
	return []controllers.SummaryItem{controllers.ForbiddenStatement(ruleKey, object, advice,
		fmt.Sprintf("语句不允许执行：变更%s，平台规范禁止该类对象", desc))}
}

// sqlServerRoutineName 从 CREATE/ALTER/DROP PROCEDURE|FUNCTION|TRIGGER 语句里取对象名
func sqlServerRoutineName(stmt string) string {
	clean := stripPGStringsAndComments(stmt)
	lower := strings.ToLower(strings.TrimSpace(clean))
	for _, prefix := range []string{"create or alter ", "create ", "alter ", "drop "} {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		rest := strings.TrimSpace(clean[len(prefix):])
		lowerRest := strings.ToLower(rest)
		for _, kw := range []string{"procedure", "proc", "function", "trigger"} {
			if !strings.HasPrefix(lowerRest, kw) {
				continue
			}
			rest = strings.TrimSpace(rest[len(kw):])
			if strings.HasPrefix(strings.ToLower(rest), "if exists") {
				rest = strings.TrimSpace(rest[len("if exists"):])
			}
			return unquoteSQLServerIdent(firstSQLServerToken(rest))
		}
	}
	return ""
}

// ---------- DML ----------

func (c *sqlServerInspectContext) inspectDML(stmt string) []controllers.SummaryItem {
	clean := stripPGStringsAndComments(stmt)
	res := analyzeSQLServerDML(stmt)
	if !res.OK {
		return nil
	}
	var items []controllers.SummaryItem
	tableName := sqlServerName{}
	if res.Table != "" {
		tableName, _ = parseSQLServerName(res.Table)
	}
	table := tableName.Object
	if table == "" {
		table = res.Table
	}

	switch res.Kind {
	case "update", "delete":
		if c.params.DML_MUST_HAVE_WHERE && !res.HasWhere {
			items = append(items, sqlServerSummary("update_delete_stmt_suggest_where",
				"%s 语句必须指定 WHERE 条件（表`%s`）", strings.ToUpper(res.Kind), table))
		}
		if c.params.DML_DISABLE_SUBQUERY && res.WhereSubquery {
			items = append(items, sqlServerSummary("update_delete_stmt_where_subquery_check",
				"WHERE 条件中不允许使用子查询（表`%s`）", table))
		}
		if c.params.CHECK_DML_JOIN_WITH_ON && res.JoinMissingOn {
			items = append(items, sqlServerSummary("update_delete_stmt_join_on_check",
				"JOIN 语句必须包含 ON 子句（表`%s`）", table))
		}
		// T-SQL 的 UPDATE/DELETE 语法里根本没有 ORDER BY（TOP 也不能配合 ORDER BY），
		// 出现即语法错误或依赖非确定性顺序，一律提示。
		if c.params.DML_DISABLE_ORDERBY && indexOfKeyword(clean, "order by") >= 0 {
			items = append(items, sqlServerSummary("update_delete_stmt_no_order_by_clause",
				"%s 语句不允许使用 ORDER BY（T-SQL 的 UPDATE/DELETE 不支持排序子句，需限量请用 TOP 或子查询）（表`%s`）",
				strings.ToUpper(res.Kind), table))
		}
		// DELETE 目标表存在性（其他引擎由 controllers/logics 的 AST 遍历层负责，
		// SQL Server 走文本引擎，必须在这里自己判）
		if res.Kind == "delete" && c.params.CHECK_DELETE_TABLE_EXISTS && strings.TrimSpace(table) != "" {
			if exists, err := c.metaResolver().TableExists(tableName); err == nil && !exists {
				items = append(items, sqlServerObjectMissing("delete_stmt_check_table_exists", table,
					"① 核对表名拼写与 schema（SQL Server 默认 schema 是 dbo）；② 是否选错了数据库；③ 若确认存在仍被报缺失，请先同步实例元数据再重新检查",
					"表`%s`不存在", table))
			}
		}
		if c.params.MAX_UPDATE_DELETE_JOIN_TABLES > 0 && res.JoinTableCount > c.params.MAX_UPDATE_DELETE_JOIN_TABLES {
			items = append(items, sqlServerSummary("limit_update_delete_join_table_count",
				"%s 语句关联表数过多：最多允许 %d 张表（含主表），当前 %d 张（表`%s`）",
				strings.ToUpper(res.Kind), c.params.MAX_UPDATE_DELETE_JOIN_TABLES, res.JoinTableCount, table))
		}
		if c.params.DML_MAX_LIMIT_ROWS > 0 && res.LimitRows > c.params.DML_MAX_LIMIT_ROWS {
			items = append(items, sqlServerSummary("limit_update_delete_data_size",
				"%s 语句 TOP 行数超过上限：最多允许 %d 行，当前 %d 行（表`%s`）",
				strings.ToUpper(res.Kind), c.params.DML_MAX_LIMIT_ROWS, res.LimitRows, table))
		}
		if c.params.MAX_WHERE_IN_ELEMENTS > 0 && res.MaxInElements > c.params.MAX_WHERE_IN_ELEMENTS {
			items = append(items, sqlServerSummary("limit_where_in_element_count",
				"WHERE 条件中 `IN` 子句元素过多：最多允许 %d 个，当前 %d 个（表`%s`）",
				c.params.MAX_WHERE_IN_ELEMENTS, res.MaxInElements, table))
		}
		// WHERE 条件类规则
		items = append(items, c.checkWhereRules(stmt, clean, tableName, res.WhereClause)...)
		// 索引使用与影响行数（连库，失败即跳过）
		items = append(items, c.checkWhereUsesIndex(tableName, res.WhereClause, table)...)
		items = append(items, c.checkAffectedRows(tableName, res.WhereClause, res.Kind, table)...)
		if res.Kind == "update" {
			items = append(items, c.inspectUpdateOnly(tableName, res, clean)...)
		}
	case "insert":
		items = append(items, c.inspectInsert(clean, res, tableName)...)
	}
	return items
}

// inspectInsert INSERT 相关规则
func (c *sqlServerInspectContext) inspectInsert(clean string, res sqlServerDMLAnalysis, table sqlServerName) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	name := table.Object
	if name == "" {
		name = res.Table
	}
	if c.params.MAX_INSERT_ROWS > 0 && res.InsertValueRows > c.params.MAX_INSERT_ROWS {
		items = append(items, sqlServerSummary("limit_insert_values_row_count",
			"单条 INSERT 语句插入行数超出限制（max = %d，当前 %d）（表`%s`）",
			c.params.MAX_INSERT_ROWS, res.InsertValueRows, name))
	}
	cols := sqlServerInsertColumns(clean)
	if c.params.CHECK_INSERT_SPECIFY_COLUMNS && len(cols) == 0 {
		items = append(items, sqlServerSummary("insert_stmt_specify_columns",
			"INSERT 语句建议显式指定字段列表（表`%s`），避免表结构变更后语句失效", name))
	}
	if c.params.CHECK_INSERT_COLUMN_NO_DUP && len(cols) > 0 {
		seen := map[string]bool{}
		for _, col := range cols {
			key := strings.ToLower(col)
			if seen[key] {
				items = append(items, sqlServerSummary("insert_stmt_col_name_no_dup",
					"INSERT 语句字段`%s`重复定义（表`%s`）", col, name))
			}
			seen[key] = true
		}
	}
	// 字段与值数量匹配（显式字段数用 sqlServerInsertColumns 的自解析结果：
	// pgInsertShapes 要求 `into` 后紧跟左括号，`INSERT INTO t (a)` 中间的空格会让它算成 0）
	if c.params.CHECK_INSERT_COLUMNS_MATCH_VALUES && len(res.InsertValueCols) > 0 {
		if len(cols) > 0 {
			for i, got := range res.InsertValueCols {
				if got == len(cols) {
					continue
				}
				items = append(items, sqlServerSummary("insert_stmt_columns_match_values",
					"INSERT 语句字段列表有 %d 个字段，但第 %d 行 VALUES 提供了 %d 个值，数量不匹配（表`%s`）",
					len(cols), i+1, got, name))
				break
			}
		} else if cnt, err := c.metaResolver().TableColumnCount(table); err == nil && cnt > 0 {
			for i, got := range res.InsertValueCols {
				if got == cnt || got == 0 {
					continue
				}
				items = append(items, sqlServerSummary("insert_stmt_columns_match_values",
					"INSERT 语句未指定字段列表，表`%s`有 %d 个字段，但第 %d 行 VALUES 提供了 %d 个值，数量不匹配",
					name, cnt, i+1, got))
				break
			}
		}
	}
	// 易变时间函数
	if c.params.CHECK_INSERT_AVOID_SYSDATE && sqlServerInsertUsesVolatileTime(clean) {
		items = append(items, sqlServerSummary("insert_stmt_avoid_sysdate_func",
			"INSERT 语句不建议在 VALUES 中写入 GETDATE()/SYSDATETIME() 等易变时间函数（表`%s`），"+
				"时间字段请交由默认值（DEFAULT）或应用层赋值，便于数据复现与比对", name))
	}
	// 目标表/字段存在性
	if c.params.CHECK_INSERT_OBJECT_EXISTS && strings.TrimSpace(name) != "" {
		if exists, err := c.metaResolver().TableExists(table); err == nil && !exists {
			items = append(items, sqlServerObjectMissing("insert_stmt_check_column_exists", name,
				"① 核对表名拼写与 schema（SQL Server 默认 schema 是 dbo）；② 是否选错了数据库；③ 若确认存在仍被报缺失，请先同步实例元数据再重新检查",
				"表`%s`不存在", name))
		} else if err == nil && exists {
			for _, col := range cols {
				if ok, err := c.metaResolver().ColumnExists(table, col); err == nil && !ok {
					items = append(items, sqlServerObjectMissing("insert_stmt_check_column_exists", col,
						"① 核对列名拼写；② 该列可能已被其他工单删除，请先同步实例元数据再重新检查",
						"表`%s`中字段`%s`不存在", name, col))
				}
			}
			// NOT NULL 列不可插入 NULL
			if c.params.CHECK_INSERT_NOT_NULL_COLUMN_NULL {
				for _, col := range sqlServerInsertNullColumns(clean, cols) {
					nullable, err := c.metaResolver().ColumnNullable(table, col)
					if err != nil || nullable {
						continue
					}
					items = append(items, sqlServerSummary("insert_stmt_not_null_column_null_value",
						"字段`%s`不允许为 NULL，表`%s`中该列是 NOT NULL", col, name))
				}
			}
		}
	}
	return items
}

// inspectUpdateOnly 仅 UPDATE 执行的规则
func (c *sqlServerInspectContext) inspectUpdateOnly(table sqlServerName, res sqlServerDMLAnalysis, clean string) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	name := table.Object
	if name == "" {
		name = res.Table
	}
	setCols := sqlServerUpdateSetColumns(clean)

	// 多表 UPDATE 的 SET 列必须带表前缀
	if c.params.CHECK_UPDATE_MULTI_TABLE_COLUMN_PREFIX && res.JoinTableCount > 1 {
		for _, col := range setCols {
			if !col.Prefixed {
				items = append(items, sqlServerSummary("update_multi_table_column_prefix_check",
					"多表 UPDATE 的 SET 列`%s`未指定表前缀，T-SQL 下会因列名歧义报错 209（表`%s`）", col.Raw, name))
			}
		}
	}
	// 建议同步更新「修改时间」列
	if strings.TrimSpace(c.params.UPDATE_TIME_COLUMNS) != "" {
		updated := false
		for _, col := range setCols {
			for _, want := range strings.Split(strings.ToLower(c.params.UPDATE_TIME_COLUMNS), ",") {
				if strings.TrimSpace(want) != "" && col.Bare == strings.TrimSpace(want) {
					updated = true
				}
			}
		}
		if !updated {
			items = append(items, sqlServerSummary("update_stmt_suggest_update_time_column",
				"UPDATE 语句建议同步更新表上的修改时间列（%s）（表`%s`）", c.params.UPDATE_TIME_COLUMNS, name))
		}
	}
	// 不建议更新「创建时间」列
	if strings.TrimSpace(c.params.AVOID_UPDATE_COLUMNS) != "" {
		for _, col := range setCols {
			for _, avoid := range strings.Split(strings.ToLower(c.params.AVOID_UPDATE_COLUMNS), ",") {
				if strings.TrimSpace(avoid) != "" && col.Bare == strings.TrimSpace(avoid) {
					items = append(items, sqlServerSummary("update_stmt_avoid_update_create_time_column",
						"UPDATE 语句不建议更新表上的创建时间列（%s）：创建时间属于数据元信息、具备不可变更属性，更新会破坏数据审计追溯能力（表`%s`）",
						strings.TrimSpace(avoid), name))
				}
			}
		}
	}
	// SET 分隔符误用 AND
	if c.params.CHECK_UPDATE_SET_SEPARATOR {
		if setIdx := indexOfKeyword(clean, "set"); setIdx >= 0 {
			after := clean[setIdx+len("set"):]
			if whereIdx := indexOfKeyword(after, "where"); whereIdx >= 0 {
				after = after[:whereIdx]
			}
			if fromIdx := indexOfKeyword(after, "from"); fromIdx >= 0 {
				after = after[:fromIdx]
			}
			if sqlServerTopLevelKeyword(after, " and ") {
				items = append(items, sqlServerSummary("update_stmt_set_separator_check",
					"UPDATE 语句 SET 子句多个列之间应使用逗号分隔，不允许使用 AND（表`%s`）", name))
			}
		}
	}
	// 更新主键 / 唯一键
	if c.params.CHECK_UPDATE_UPDATES_PK {
		if pk, err := c.metaResolver().PrimaryKeyColumns(table); err == nil {
			for _, col := range setCols {
				for _, key := range pk {
					if strings.EqualFold(col.Bare, key) {
						items = append(items, sqlServerSummary("update_stmt_check_updates_pk",
							"UPDATE 语句更新了主键列`%s`，会触发聚集索引重建并可能引发级联更新（表`%s`）", key, name))
					}
				}
			}
		}
	}
	if c.params.CHECK_UPDATE_UPDATES_UNIQUE_KEY {
		if uniq, err := c.metaResolver().UniqueIndexColumns(table); err == nil {
			for _, col := range setCols {
				for _, key := range uniq {
					if strings.EqualFold(col.Bare, key) {
						items = append(items, sqlServerSummary("update_stmt_check_updates_unique_key",
							"UPDATE 语句更新了唯一键列`%s`，会破坏外键关联与业务唯一性（表`%s`）", key, name))
					}
				}
			}
		}
	}
	// 目标表 / 字段存在性
	if c.params.CHECK_INSERT_OBJECT_EXISTS && len(setCols) > 0 && strings.TrimSpace(name) != "" {
		if exists, err := c.metaResolver().TableExists(table); err == nil && !exists {
			items = append(items, sqlServerObjectMissing("update_stmt_check_column_exists", name,
				"① 核对表名拼写与 schema；② 是否选错了数据库；③ 若确认存在仍被报缺失，请先同步实例元数据再重新检查",
				"表`%s`不存在", name))
		} else if err == nil && exists {
			for _, col := range setCols {
				if ok, err := c.metaResolver().ColumnExists(table, col.Bare); err == nil && !ok {
					items = append(items, sqlServerObjectMissing("update_stmt_check_column_exists", col.Bare,
						"① 核对列名拼写；② 该列可能已被其他工单删除",
						"表`%s`中字段`%s`不存在", name, col.Bare))
				}
			}
		}
	}
	return items
}

// checkWhereRules WHERE 条件类规则（文本级判定）
func (c *sqlServerInspectContext) checkWhereRules(stmt, clean string, table sqlServerName, where string) []controllers.SummaryItem {
	if strings.TrimSpace(where) == "" {
		return nil
	}
	whereClean := stripPGStringsAndComments(where)
	whereLower := strings.ToLower(whereClean)
	var items []controllers.SummaryItem
	name := table.Object

	if c.params.CHECK_WHERE_LEADING_WILDCARD && sqlServerHasLeadingWildcard(where) {
		items = append(items, sqlServerSummary("where_avoid_leading_wildcard",
			"WHERE 条件中 `LIKE` 使用了前通配符（%%xxx），无法命中前缀索引，必然全表扫描（表`%s`）", name))
	}
	if c.params.CHECK_WHERE_NOT_IN_NOT_LIKE &&
		(indexOfKeyword(whereLower, "not in") >= 0 || indexOfKeyword(whereLower, "not like") >= 0) {
		items = append(items, sqlServerSummary("where_avoid_not_in_not_like",
			"WHERE 条件中不建议使用 `NOT IN` / `NOT LIKE` 反向查询，难命中索引且易踩 NULL 语义（表`%s`）", name))
	}
	if c.params.CHECK_WHERE_OR_CONDITION && sqlServerTopLevelKeyword(whereLower, " or ") {
		items = append(items, sqlServerSummary("where_or_condition_check",
			"WHERE 条件中使用 `OR` 连接过滤条件，易致索引失效走全表扫描；条件恒真还有误操作风险（表`%s`）", name))
	}
	if c.params.CHECK_WHERE_LIKE_WITHOUT_WILDCARD && sqlServerHasLikeWithoutWildcard(where) {
		items = append(items, sqlServerSummary("where_like_without_wildcard",
			"WHERE 条件中 `LIKE` 缺少通配符，等价于等值查询，建议改用 `=`（表`%s`）", name))
	}
	if c.params.CHECK_WHERE_INDEX_COL_CALC {
		if cols, err := c.metaResolver().IndexedLeadingColumns(table); err == nil {
			for _, col := range cols {
				if sqlServerHasColumnCalc(whereClean, col) {
					items = append(items, sqlServerSummary("where_avoid_index_col_arithmetic_or_func",
						"WHERE 条件中索引列`%s`参与了函数或数学运算，会导致索引失效（表`%s`）", col, name))
				}
			}
		}
	}
	if c.params.CHECK_WHERE_IMPLICIT_CAST {
		if types, err := c.sqlServerColumnTypes(table); err == nil {
			for col, typ := range types {
				if sqlServerHasImplicitCast(whereClean, col, typ) {
					items = append(items, sqlServerSummary("where_implicit_type_cast_check",
						"WHERE 条件中字段`%s`（%s）存在隐式类型转换，易致索引失效且比对结果可能出错（表`%s`）",
						col, strings.ToUpper(typ), name))
				}
			}
		}
	}
	return items
}

// checkWhereUsesIndex UPDATE/DELETE 必须使用索引（规则 update_delete_stmt_must_use_idx）。
// 判定口径：WHERE 里出现的列只要有一列是某索引的首列，即视为使用了索引；
// 连库失败或无 WHERE 时跳过（不误报）。
func (c *sqlServerInspectContext) checkWhereUsesIndex(table sqlServerName, where string, displayName string) []controllers.SummaryItem {
	if !c.params.CHECK_DML_MUST_USE_INDEX || strings.TrimSpace(where) == "" || strings.TrimSpace(displayName) == "" {
		return nil
	}
	leading, err := c.metaResolver().IndexedLeadingColumns(table)
	if err != nil || len(leading) == 0 {
		return nil
	}
	whereLower := strings.ToLower(stripPGStringsAndComments(where))
	for _, col := range leading {
		if indexOfKeyword(whereLower, strings.ToLower(col)) >= 0 {
			return nil
		}
	}
	return []controllers.SummaryItem{sqlServerSummary("update_delete_stmt_must_use_idx",
		"UPDATE/DELETE 语句的 WHERE 条件未命中任何索引的首列（现有索引首列：%s），将退化为表扫描（表`%s`）",
		strings.Join(leading, ", "), displayName)}
}

// checkAffectedRows 影响行数估算：对同条件做 COUNT_BIG(*)（规则 limit_update_delete_affected_rows）。
func (c *sqlServerInspectContext) checkAffectedRows(table sqlServerName, where, kind, displayName string) []controllers.SummaryItem {
	if c.params.MAX_AFFECTED_ROWS <= 0 || strings.TrimSpace(where) == "" || strings.TrimSpace(displayName) == "" {
		return nil
	}
	n, err := c.metaResolver().CountWhere(table, where)
	if err != nil || n <= c.params.MAX_AFFECTED_ROWS {
		return nil
	}
	return []controllers.SummaryItem{sqlServerSummary("limit_update_delete_affected_rows",
		"%s 语句影响行数（约 %d）超过上限 %d（表`%s`），请缩小 WHERE 条件范围或拆分批次",
		strings.ToUpper(kind), n, c.params.MAX_AFFECTED_ROWS, displayName)}
}

// sqlServerColumnTypes 取目标表列类型（隐式类型转换判定用；连库失败返回 err）
func (c *sqlServerInspectContext) sqlServerColumnTypes(table sqlServerName) (map[string]string, error) {
	if c.db == nil {
		return nil, fmt.Errorf("未初始化数据库连接")
	}
	q := fmt.Sprintf(
		"SELECT c.name AS name, t.name AS type FROM sys.columns c "+
			"JOIN sys.types t ON t.user_type_id = c.user_type_id "+
			"JOIN sys.objects o ON o.object_id = c.object_id "+
			"JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"WHERE o.name = %s%s",
		sqlQuoteLiteral(table.Object), sqlServerSchemaCond(table.EffectiveSchema()))
	result, err := c.db.Query(q)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if result == nil {
		return out, nil
	}
	for _, row := range *result {
		var name, typ string
		for k, v := range row {
			s, _ := v.(string)
			switch {
			case strings.EqualFold(k, "name"):
				name = s
			case strings.EqualFold(k, "type"):
				typ = s
			}
		}
		if name != "" {
			out[name] = strings.ToLower(typ)
		}
	}
	return out, nil
}
