package checker

import (
	"fmt"
	"sort"
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"

	"github.com/simajiong/jaysql/pkg/sqlsplit"
	"github.com/simajiong/jaysql/pkg/utils"
)

// PG 规范审核引擎
//
// 判定来源与 MySQL 引擎完全一致：实例绑定的 SQL 开发规范（经 BuildInspectParamsForInstance
// 映射为 config.InspectParams）。差别只在实现方式——PG 语句不使用 TiDB AST，
// 而是基于语法感知拆分 + 轻量文本解析提取表/列/索引/约束信息后逐条判定。
//
// 规则 key 使用「去掉数据库前缀的后缀」形式（与 MySQL 引擎一致），
// 由 orders/services 的 resolveInspectSeverity 关联回规范中的具体规则级别：
// must → 阻断提交审批；suggest → 提示但放行；off → 规则不检查（参数为默认关闭态）。

// pgInspectContext 单次预审的上下文
type pgInspectContext struct {
	params  config.InspectParams
	sqlText string
	db      *dao.DB // 可能为 nil（不连库时跳过依赖元信息的规则）
	schema  string

	// 工单内 COMMENT ON 收集：table -> column -> true（column 为空表示表注释）
	colComments map[string]map[string]bool
	// 工单内新增索引：table -> 索引列表
	indexByTable map[string][]*pgIndexDef
	// 元信息探针：nil 时走真实库查询；单测可注入桩实现（见 pg_object_rules.go）
	meta pgMetaResolver
}

// pgSummary 构造一条审核项
func pgSummary(ruleKey, format string, args ...any) controllers.SummaryItem {
	return controllers.SummaryItem{
		Level:   LevelWarn,
		RuleKey: ruleKey,
		Message: fmt.Sprintf(format, args...),
	}
}

// pgObjectMissing 构造「执行必然失败」的对象缺失结论。
// 带 category / object / advice，前端可渲染四要素卡片；MySQL 侧对应 WarnFatal。
func pgObjectMissing(ruleKey, object, advice, format string, args ...any) controllers.SummaryItem {
	return controllers.SummaryItem{
		Level:    LevelWarn,
		RuleKey:  ruleKey,
		Category: controllers.CategoryObjectMissing,
		Object:   object,
		Advice:   advice,
		Message:  fmt.Sprintf(format, args...),
	}
}

// runPGInspect PG 工单规范预审入口
func (s *SyntaxInspectService) runPGInspect() ([]ReturnData, error) {
	stmts, types, err := sqlsplit.SplitTyped(s.SqlText)
	if err != nil {
		return nil, err
	}
	ctx := &pgInspectContext{
		params:       s.InspectParams,
		sqlText:      s.SqlText,
		db:           s.DB,
		schema:       s.InspectSchema,
		colComments:  map[string]map[string]bool{},
		indexByTable: map[string][]*pgIndexDef{},
		meta:         s.pgMeta,
	}
	// 语句文本归一：展示用原文（clean），解析用折叠空白的文本（parsed）。
	// 引擎全部规则都是文本匹配实现，不归一会让 `create  table  t(...)` 这类多空白写法
	// 解析失败、整条语句零结论（见 collapseSQLWhitespace 注释）。
	cleans := make([]string, len(stmts))
	parsed := make([]string, len(stmts))
	for i, stmt := range stmts {
		cleans[i] = strings.TrimSpace(sqlsplit.StripLeadingComments(stmt))
		parsed[i] = collapseSQLWhitespace(cleans[i])
	}
	ctx.collect(parsed)

	returnData := make([]ReturnData, 0, len(stmts))
	// 同表多条 ALTER：与 MySQL 引擎一致，在整段 SQL 层面汇总后给出合并建议
	var alterTables []string
	for i := range stmts {
		// 与 MySQL 引擎口径一致：Query 展示语句本体（不含前导注释），行号指向语句起始行
		clean := cleans[i]
		data := ReturnData{
			FingerId: "",
			Query:    clean,
			Type:     types[i],
			Line:     s.computeLine(clean),
		}
		if act, ok := parseAlterTableActions(parsed[i]); ok && strings.TrimSpace(act.Table) != "" {
			// 归并键：显式限定了「非工单 schema」时按 `schema.table` 区分（不同模式下的
			// 同名表是不同的表，混在一起会给出错误的合并建议）；限定到工单 schema 或
			// 未限定 schema 时一律用表名，避免 `public.t` 与 `t` 被当成两张表而漏报。
			key := act.Table
			if act.Schema != "" && !strings.EqualFold(act.Schema, ctx.schema) {
				key = act.Schema + "." + act.Table
			}
			alterTables = append(alterTables, key)
		}
		data.Summary = ctx.inspectStatement(parsed[i], types[i])
		returnData = append(returnData, data)
	}
	if data := ctx.mergeAlterSummary(alterTables); len(data.Summary) > 0 {
		returnData = append(returnData, data)
	}
	return returnData, nil
}

// mergeAlterSummary 同一张表出现多条 ALTER 语句时提示合并（规则 merge_multiple_alter_stmt_same_table）。
//
// PG 同样成立：每条 ALTER TABLE 都要单独获取 ACCESS EXCLUSIVE 锁并按需重写表，
// 合并成一条可显著缩短持锁时间、只重写一次。
func (c *pgInspectContext) mergeAlterSummary(tables []string) ReturnData {
	data := ReturnData{FingerId: utils.GenerateSimpleRandomString(16)}
	if !c.params.ENABLE_MERGE_ALTER_TABLE || len(tables) < 2 {
		return data
	}
	ok, repeated := utils.IsRepeat(tables)
	if !ok {
		return data
	}
	for _, table := range repeated {
		data.Summary = append(data.Summary, pgSummary("merge_multiple_alter_stmt_same_table",
			"[PostgreSQL数据库]表`%s`的多条ALTER操作，请合并为一条ALTER语句", table))
	}
	return data
}

// collect 预处理：收集 COMMENT ON 与 CREATE INDEX，供后续规则引用
func (c *pgInspectContext) collect(stmts []string) {
	for _, stmt := range stmts {
		head := sqlsplit.NormalizeLeading(stmt)
		switch {
		case strings.HasPrefix(head, "comment on"):
			table, column, ok := parseCommentOn(stmt)
			if !ok {
				continue
			}
			key := normalizePGIdent(table)
			if c.colComments[key] == nil {
				c.colComments[key] = map[string]bool{}
			}
			c.colComments[key][normalizePGIdent(column)] = true
		case strings.HasPrefix(head, "create") && (strings.Contains(head, "index") || strings.Contains(head, "unique")):
			def, ok := parseCreateIndex(stmt)
			if !ok {
				continue
			}
			key := normalizePGIdent(def.Table)
			c.indexByTable[key] = append(c.indexByTable[key], def)
		}
	}
}

// inspectStatement 对单条语句执行全部适用规则
func (c *pgInspectContext) inspectStatement(stmt string, sqlType string) []controllers.SummaryItem {
	head := sqlsplit.NormalizeLeading(stmt)
	switch {
	case strings.HasPrefix(head, "create table"):
		// 准入闸门：CTAS / CTL 属「整类不允许执行」，命中即给出语句级结论，不再进入规则链
		// （它们的表结构继承自 SELECT/源表，后续规则只能产出无法满足的伪结论）
		if rejected := c.gateCreateTable(stmt); len(rejected) > 0 {
			return rejected
		}
		return c.inspectCreateTable(stmt)
	case strings.HasPrefix(head, "alter table"):
		return c.inspectAlterTable(stmt)
	case strings.HasPrefix(head, "create") && strings.Contains(head, "index"):
		return c.inspectCreateIndex(stmt)
	case strings.HasPrefix(head, "drop index"), strings.HasPrefix(head, "alter index"):
		// 索引对象的存在性/冲突（PG 的改索引名是 ALTER INDEX ... RENAME TO，
		// 与 MySQL 的 ALTER TABLE ... RENAME INDEX 语法不同，规则语义一致）
		return c.inspectIndexObject(stmt)
	case isPGViewStatement(head):
		// 同属准入闸门：平台禁止视图时直接给语句级结论
		return c.inspectView(stmt)
	case sqlType == "DML":
		return c.inspectDML(stmt)
	default:
		return nil
	}
}

// isPGViewStatement 判断是否为视图相关语句（含 MATERIALIZED VIEW）
func isPGViewStatement(head string) bool {
	_, _, ok := parseViewStatement(head)
	return ok
}

// ---------- CREATE TABLE ----------

func (c *pgInspectContext) inspectCreateTable(stmt string) []controllers.SummaryItem {
	def, ok := parseCreateTable(stmt)
	if !ok {
		return nil
	}
	var items []controllers.SummaryItem
	table := def.Ident.Name

	// 表名规范：大小写要求由「限制表名大小写」规则的 name_case 参数决定，默认小写
	if c.params.CHECK_TABLE_IDENTIFIER {
		pattern := pgNamePattern
		caseText := "小写"
		if strings.EqualFold(strings.TrimSpace(c.params.TABLE_NAME_CASE), "upper") {
			pattern = pgUpperNamePattern
			caseText = "大写"
		}
		if !pattern.MatchString(table) {
			items = append(items, pgSummary("limit_tbl_name_case",
				"表`%s`命名不符合要求：必须为%s，仅允许匹配正则`%s`", table, caseText, pattern.String()))
		}
	}
	if c.params.CHECK_IDENTIFER_KEYWORD {
		// 与 MySQL 引擎口径一致：关键字范围来自规范配置（未配置回退默认全集），
		// 判定大小写不敏感（PG 未加引号的标识符本身不区分大小写）
		if _, hit := c.params.IdentifierKeywordSet()[strings.ToUpper(table)]; hit {
			items = append(items, pgSummary("tbl_name_no_keyword", "表`%s`命名不允许使用关键字", table))
		}
	}

	// 执行前置：表已存在。
	// 与 MySQL 引擎一致地归级到 create_tbl_must_not_exist，并标记为「对象冲突」：
	// 该场景下 CREATE TABLE 执行必然失败（PG 42P07），不能按规范默认级别降级成「建议改进」。
	// 建表语句自带 `schema.t` 时按该 schema 探测（未限定时走工单 InspectSchema）。
	if exists, err := c.metaResolverFor(def.Ident.Schema).TableExists(table); err == nil && exists {
		items = append(items, controllers.SummaryItem{
			Level:    LevelWarn,
			RuleKey:  "create_tbl_must_not_exist",
			Category: controllers.CategoryObjectConflict,
			Object:   table,
			Advice:   "① 更换表名（如 `" + table + "_new`）；② 需要重复执行请使用 `CREATE TABLE IF NOT EXISTS`；③ 确需覆盖请先单独提交 `DROP TABLE` 工单（数据将丢失，需另行评估）",
			Message:  fmt.Sprintf("表或视图`%s`已存在", table),
		})
	}

	// 主键
	items = append(items, c.checkPrimaryKey(table, def.PrimaryKeys, def.Columns)...)

	// 列级规则
	for _, col := range def.Columns {
		items = append(items, c.checkColumn(table, col, false)...)
	}

	// 表字段数量上限（规则 limit_tbl_col_的count，0 表示不限制）
	if c.params.MAX_TABLE_COLUMNS > 0 && len(def.Columns) > c.params.MAX_TABLE_COLUMNS {
		items = append(items, pgSummary("limit_table_column_count",
			"表`%s`字段数量超出限制（max = %d，当前 %d），请拆分表或精简字段",
			table, c.params.MAX_TABLE_COLUMNS, len(def.Columns)))
	}

	// 表必须包含的列（规则 tbl_需要include_某些col，列表为空表示规则未开启）
	if len(c.params.TABLE_REQUIRED_COLUMNS) > 0 {
		have := make(map[string]bool, len(def.Columns))
		for _, col := range def.Columns {
			have[strings.ToLower(normalizePGIdent(col.Name))] = true
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
		if len(missing) > 0 {
			items = append(items, pgSummary("table_must_include_required_columns",
				"表`%s`缺少必需的列：%s", table, strings.Join(missing, ", ")))
		}
	}

	// 列注释：新建表场景要求工单内为每列提供 COMMENT ON COLUMN
	items = append(items, c.checkColumnComments(table, def.Columns)...)

	// 表注释：PG 无内联 COMMENT 语法，表注释由独立的 COMMENT ON TABLE 语句提供；
	// 判定口径与列注释一致（工单内提供即视为满足），表已存在时连库以实际注释兜底
	items = append(items, c.checkTableComment(table)...)

	// 外键
	if def.HasForeignKey && !c.params.ENABLE_FOREIGN_KEY {
		items = append(items, pgSummary("limit_tbl_no_use_fk", "表`%s`禁止定义外键", table))
	}

	// 表内唯一约束命名（判定口径与 CREATE UNIQUE INDEX 完全一致）
	for _, name := range def.UniqueNames {
		if name == "" {
			continue
		}
		if c.params.CHECK_UNIQ_INDEX_PREFIX {
			prefix := c.uniqueIndexPrefix()
			if !utils.MatchIndexName(name, c.params.UNIQ_INDEX_REGEX, prefix) {
				items = append(items, pgSummary("limit_uniq_idx_name_fmt",
					"唯一索引`%s`命名不符合要求，必须%s（表`%s`）",
					name, utils.IndexNameRequirementText(c.params.UNIQ_INDEX_REGEX, prefix), table))
			}
		}
	}

	// 索引数量
	items = append(items, c.checkIndexCount(table)...)
	return items
}

// ---------- ALTER TABLE ----------

func (c *pgInspectContext) inspectAlterTable(stmt string) []controllers.SummaryItem {
	var items []controllers.SummaryItem

	// 对象存在性/冲突（目标表、DROP/ALTER 列、改名列、改名表）：与 ADD 解析相互独立，
	// 因此不能因为「本语句没有 ADD 子句」就整条跳过。
	items = append(items, c.checkAlterTableObjects(stmt)...)

	// ADD 相关规则（列定义 / 主键 / 外键）：解析不到 ADD 子句时跳过
	add, ok := parseAlterTableAdd(stmt)
	if !ok {
		return items
	}
	if len(add.Cols) > 0 {
		def := &pgTableDef{Columns: add.Cols}
		for _, col := range add.Cols {
			items = append(items, c.checkColumn(add.Table, col, true)...)
		}
		// 新增列同样需要注释
		items = append(items, c.checkColumnComments(add.Table, def.Columns)...)
	}
	if len(add.PK) > 0 {
		items = append(items, c.checkPrimaryKeyType(add.Table, add.PK, nil)...)
	}
	if add.FK && !c.params.ENABLE_FOREIGN_KEY {
		items = append(items, pgSummary("limit_tbl_no_use_fk", "表`%s`禁止定义外键", add.Table))
	}
	return items
}

// ---------- CREATE INDEX ----------

func (c *pgInspectContext) inspectCreateIndex(stmt string) []controllers.SummaryItem {
	def, ok := parseCreateIndex(stmt)
	if !ok {
		return nil
	}
	var items []controllers.SummaryItem
	table := def.Table

	// 索引必须显式命名：仅在「索引需要设置名称」规则开启时检查（CHECK_INDEX_NAME_REQUIRED）；
	// 未命名且规则关闭时跳过命名类检查（无名称可做前缀/正则比对）
	if c.params.CHECK_INDEX_NAME_REQUIRED && (!def.Explicit || def.Name == "") {
		items = append(items, pgSummary("index_must_have_name", "索引必须显式命名（表`%s`）", table))
	} else if def.Name != "" && def.Unique {
		// 唯一索引命名：规范配了正则则以「完整匹配正则」为准，未配才回退前缀。
		// 判定口径与 MySQL 引擎共用 utils.MatchIndexName，避免两边实现漂移。
		if c.params.CHECK_UNIQ_INDEX_PREFIX {
			prefix := c.uniqueIndexPrefix()
			if !utils.MatchIndexName(def.Name, c.params.UNIQ_INDEX_REGEX, prefix) {
				items = append(items, pgSummary("limit_uniq_idx_name_fmt",
					"唯一索引`%s`命名不符合要求，必须%s（表`%s`）",
					def.Name, utils.IndexNameRequirementText(c.params.UNIQ_INDEX_REGEX, prefix), table))
			}
		}
	} else if def.Name != "" && c.params.CHECK_SECONDARY_INDEX_PREFIX {
		prefix := c.secondaryIndexPrefix()
		if !utils.MatchIndexName(def.Name, c.params.SECONDARY_INDEX_REGEX, prefix) {
			items = append(items, pgSummary("limit_normal_idx_name_fmt",
				"普通索引`%s`命名不符合要求，必须%s（表`%s`）",
				def.Name, utils.IndexNameRequirementText(c.params.SECONDARY_INDEX_REGEX, prefix), table))
		}
	}

	if c.params.SECONDARY_INDEX_MAX_KEY_PARTS > 0 && len(def.Columns) > c.params.SECONDARY_INDEX_MAX_KEY_PARTS {
		items = append(items, pgSummary("limit_single_index_column_count",
			"索引`%s`包含的列数超出限制（max = %d，当前 %d）（表`%s`）",
			def.Name, c.params.SECONDARY_INDEX_MAX_KEY_PARTS, len(def.Columns), table))
	}

	items = append(items, c.checkIndexCount(table)...)
	items = append(items, c.checkRedundantIndexes(def)...)
	return items
}

// checkRedundantIndexes 冗余索引检查（规则 limit_tbl_redundant_idx）。
// 仅在「限制冗余索引」规则开启时执行（ENABLE_REDUNDANT_INDEX=false 表示不允许冗余）。
// 判定口径与 MySQL 引擎对齐：
//   - 重复索引：两索引列组合完全相同；
//   - 前缀覆盖：已有索引的列组合是当前索引的最左前缀（如 (a) 被 (a,b) 覆盖）。
//
// PG 无 CREATE TABLE 内联索引语法，索引均来自工单内的 CREATE INDEX / CREATE UNIQUE INDEX
// 语句（collect 阶段已按表聚合到 indexByTable），因此是「同一工单内」的比对，
// 不涉及数据库已有索引（避免预审阶段连库）。
// 去重口径：每对冗余只由「更宽」一侧的语句报告一次；列组合完全相同时由位置靠后的语句报告。
func (c *pgInspectContext) checkRedundantIndexes(cur *pgIndexDef) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	if c.params.ENABLE_REDUNDANT_INDEX {
		return items
	}
	key := normalizePGIdent(cur.Table)
	group := c.indexByTable[key]
	// 当前语句的 def 是新解析出来的对象，需按值定位它在收集列表中的位置（用于去重）
	curPos := -1
	for i, def := range group {
		if pgSameIndexDef(def, cur) {
			curPos = i
			break
		}
	}
	if curPos < 0 {
		return items // 防御：当前语句的索引不在收集结果中，跳过
	}
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
			// other 是 cur 的最左前缀：由 cur（更宽）一侧报告
		case len(otherCols) == len(curCols) && pgColsEqual(curCols, otherCols) && i > curPos:
			// 重复索引：两语句各会比对一次，只由位置靠后的一方报告
		default:
			continue
		}
		items = append(items, pgSummary("limit_tbl_redundant_idx",
			"表`%s`发现了冗余索引，冗余索引的字段组合为%s/%s",
			other.Table, pgIndexLabel(cur), pgIndexLabel(other)))
	}
	return items
}

// pgIndexLabel 索引展示标签：有名字用名字，匿名的用 (匿名) 占位
func pgIndexLabel(def *pgIndexDef) string {
	name := def.Name
	if strings.TrimSpace(name) == "" {
		name = "(匿名)"
	}
	return fmt.Sprintf("%s(%s)", name, strings.Join(pgNormCols(def.Columns), ","))
}

// pgNormCols 归一化索引列名（统一小写，去除空白）
func pgNormCols(cols []string) []string {
	out := make([]string, 0, len(cols))
	for _, col := range cols {
		out = append(out, strings.ToLower(strings.TrimSpace(col)))
	}
	return out
}

func pgColsPrefix(long, prefix []string) bool {
	for i, c := range prefix {
		if long[i] != c {
			return false
		}
	}
	return true
}

func pgColsEqual(a, b []string) bool {
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

// pgSameIndexDef 按值判断两个索引定义是否指向同一索引（表名/名称/唯一性/列组合一致）
func pgSameIndexDef(a, b *pgIndexDef) bool {
	if a == nil || b == nil {
		return false
	}
	return normalizePGIdent(a.Table) == normalizePGIdent(b.Table) &&
		strings.EqualFold(strings.TrimSpace(a.Name), strings.TrimSpace(b.Name)) &&
		a.Unique == b.Unique &&
		pgColsEqual(pgNormCols(a.Columns), pgNormCols(b.Columns))
}

// ---------- 视图 ----------

// 视图相关操作（禁止视图 / 允许视图）的判定已上移到语句准入闸门，见 pg_gate.go: inspectView。
// 保留空实现是为了让「视图存在性、依赖表存在性」等后续规则有落点；
// 目前 PG 侧尚无此类规则，因此不做额外判定。

// ---------- 主键 ----------

func (c *pgInspectContext) checkPrimaryKey(table string, keys []string, cols []pgColumn) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	if c.params.CHECK_TABLE_PRIMARY_KEY {
		if len(keys) == 0 {
			items = append(items, pgSummary("table_must_have_pk",
				"表`%s`必须定义主键（建议使用 bigint GENERATED ALWAYS AS IDENTITY）", table))
		}
		if len(keys) > 1 {
			items = append(items, pgSummary("table_must_have_pk", "表`%s`主键定义异常：只能定义一个主键", table))
		}
	}
	return append(items, c.checkPrimaryKeyType(table, keys, cols)...)
}

// checkPrimaryKeyType 主键列类型规则（白名单可配置，默认 bigint、int）
func (c *pgInspectContext) checkPrimaryKeyType(table string, keys []string, cols []pgColumn) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	allowed := c.params.PRIMARYKEY_SUPPORT_TYPES
	// 类型白名单校验：规则开启且配置了白名单才逐列比对；**未配置（空）表示不限制类型**
	if c.params.CHECK_PRIMARYKEY_USE_BIGINT && len(allowed) > 0 {
		colTypes := map[string]string{}
		for _, col := range cols {
			colTypes[normalizePGIdent(col.Name)] = col.Type
		}
		for _, key := range keys {
			typ, ok := colTypes[normalizePGIdent(key)]
			if !ok {
				continue // 列定义不在同一条语句中（如 ALTER 单独加主键），无法判定，不报
			}
			if !pgTypeMatches(typ, allowed) {
				items = append(items, pgSummary("limit_pk_col_type",
					"主键`%s`类型不符合要求，仅允许使用%s（表`%s`）", key, strings.Join(allowed, ", "), table))
			}
		}
	}
	if c.params.PRIMARYKEY_MAX_KEY_PARTS > 0 && len(keys) > c.params.PRIMARYKEY_MAX_KEY_PARTS {
		items = append(items, pgSummary("limit_pk_column_count",
			"主键包含的列数超出限制（max = %d，当前 %d）（表`%s`）",
			c.params.PRIMARYKEY_MAX_KEY_PARTS, len(keys), table))
	}
	return items
}

// pgTypeAliases PG 类型别名 → 归一化名称（对齐白名单里的 MySQL 风格类型名）
var pgTypeAliases = map[string]string{
	"bigint": "bigint", "int8": "bigint", "bigserial": "bigint", "serial8": "bigint",
	"int": "int", "integer": "int", "int4": "int", "serial": "int", "serial4": "int",
	"smallint": "smallint", "int2": "smallint", "smallserial": "smallint", "serial2": "smallint",
	"numeric": "decimal", "decimal": "decimal",
	"real": "float", "float4": "float",
	"double precision": "double", "float8": "double",
	"varchar": "varchar", "character varying": "varchar",
	"char": "char", "character": "char",
	"text": "text", "date": "date", "time": "time",
	"timestamp": "timestamp", "timestamptz": "timestamp",
	"boolean": "bool", "bool": "bool",
	"json": "json", "jsonb": "json", "uuid": "uuid", "bytea": "bytea",
}

// pgTypeMatches 判断 PG 列类型是否命中白名单（先做别名归一化再比对，大小写不敏感）
func pgTypeMatches(typ string, allowed []string) bool {
	t := strings.ToLower(strings.TrimSpace(typ))
	if canonical, ok := pgTypeAliases[t]; ok {
		t = canonical
	}
	for _, name := range allowed {
		n := strings.ToLower(strings.TrimSpace(name))
		if n == "" {
			continue
		}
		if n == t {
			return true
		}
	}
	return false
}

// ---------- 列 ----------

// checkColumn 列级规则；isAddCol 表示该列来自 ALTER TABLE ADD COLUMN
func (c *pgInspectContext) checkColumn(table string, col pgColumn, isAddCol bool) []controllers.SummaryItem {
	var items []controllers.SummaryItem
	name := col.Name

	if c.params.CHECK_COLUMN_IDENTIFIER {
		// 列名大小写要求由「限制字段名大小写」规则的 name_case 参数决定，默认小写；
		// 文案回显当前生效的大小写要求与正则，便于确认配置是否生效
		pattern := pgNamePattern
		caseText := "小写"
		if strings.EqualFold(strings.TrimSpace(c.params.COLUMN_NAME_CASE), "upper") {
			pattern = pgUpperNamePattern
			caseText = "大写"
		}
		if !pattern.MatchString(name) {
			items = append(items, pgSummary("limit_col_name_case",
				"列`%s`命名不符合要求：必须为%s，仅允许匹配正则`%s`（表`%s`）", name, caseText, pattern.String(), table))
		}
	}
	if c.params.CHECK_IDENTIFER_KEYWORD {
		// 与 MySQL 引擎口径一致：关键字范围来自规范配置（未配置回退默认全集）
		if _, hit := c.params.IdentifierKeywordSet()[strings.ToUpper(name)]; hit {
			items = append(items, pgSummary("col_name_no_keyword", "列`%s`命名不允许使用关键字（表`%s`）", name, table))
		}
	}
	if c.params.COLUMN_MAX_CHAR_LENGTH > 0 && isPGCharType(col.Type) && col.Len > c.params.COLUMN_MAX_CHAR_LENGTH {
		items = append(items, pgSummary("limit_char_type_col_len",
			"列`%s`长度太大（max = %d），当前char长度为%d，请调整长度或改用varchar类型（表`%s`）",
			name, c.params.COLUMN_MAX_CHAR_LENGTH, col.Len, table))
	}
	if c.params.MAX_VARCHAR_LENGTH > 0 && isPGVarcharType(col.Type) && col.Len > c.params.MAX_VARCHAR_LENGTH {
		items = append(items, pgSummary("limit_varchar_type_col_len",
			"列`%s`长度太大（max = %d），当前varchar长度为%d，请使用text类型替代（表`%s`）",
			name, c.params.MAX_VARCHAR_LENGTH, col.Len, table))
	}
	if c.params.ENABLE_COLUMN_NOT_NULL && !col.NotNull && !col.IsIdentity && !pgAllowsNullType(col.Type) {
		items = append(items, pgSummary("limit_col_not_null", "列`%s`必须定义为`NOT NULL`（表`%s`）", name, table))
	}
	if c.params.CHECK_COLUMN_DEFAULT_VALUE && !col.HasDefault && !pgCannotHaveDefaultType(col.Type) {
		items = append(items, pgSummary("limit_col_must_have_default", "列`%s`需要设置一个默认值（表`%s`）", name, table))
	}
	if c.params.CHECK_COLUMN_FORBIDDEN_TYPES {
		if c.params.IsForbiddenPGType(col.Type) {
			items = append(items, pgSummary("limit_col_no_partial_data_type",
				"列`%s`不允许定义%s类型（表`%s`）", name, strings.ToUpper(col.Type), table))
		}
	}
	_ = isAddCol
	return items
}

// checkColumnComments 列注释规则：
// PG 没有内联 COMMENT 语法，注释由独立的 COMMENT ON COLUMN 语句提供，
// 因此判定「工单内是否提供了覆盖全部列的 COMMENT ON COLUMN」（执行后注释即落库）。
func (c *pgInspectContext) checkColumnComments(table string, cols []pgColumn) []controllers.SummaryItem {
	if !c.params.CHECK_COLUMN_COMMENT || len(cols) == 0 {
		return nil
	}
	commented := c.colComments[normalizePGIdent(table)]
	var items []controllers.SummaryItem
	for _, col := range cols {
		if commented != nil && commented[normalizePGIdent(col.Name)] {
			continue
		}
		items = append(items, pgSummary("limit_col_must_have_comment", "列`%s`必须要有注释（表`%s`）", col.Name, table))
	}
	return items
}

// checkTableComments 表注释规则（limit_tbl_must_comment）：
// 与列注释判定口径一致——PG 的表注释由独立的 COMMENT ON TABLE 语句提供，
// collect 阶段已将其记入 colComments[table][""]（column 为空表示表注释）。
// 工单内未提供时，若表已存在则连库查询 pg_description 兜底（失败静默跳过，按未注释处理）。
func (c *pgInspectContext) checkTableComment(table string) []controllers.SummaryItem {
	if !c.params.CHECK_TABLE_COMMENT {
		return nil
	}
	if commented := c.colComments[normalizePGIdent(table)]; commented != nil && commented[""] {
		return nil
	}
	// 表已存在且库中已有注释（COMMENT ON 先于建表语句提交等场景），视为满足
	if exists, err := c.tableExists(table); err == nil && exists {
		if has, err := c.tableHasCommentInDB(table); err == nil && has {
			return nil
		}
	}
	return []controllers.SummaryItem{
		pgSummary("limit_tbl_must_comment", "表`%s`必须要有注释", table),
	}
}

func isPGCharType(t string) bool {
	switch strings.ToLower(t) {
	case "char", "bpchar", "character":
		return true
	default:
		return false
	}
}

func isPGVarcharType(t string) bool {
	switch strings.ToLower(t) {
	case "varchar", "character varying":
		return true
	default:
		return false
	}
}

// pgAllowsNullType 允许为 NULL 的类型（与 MySQL 引擎的 blob/json 与时间类型豁免口径对齐）
func pgAllowsNullType(t string) bool {
	switch strings.ToLower(t) {
	case "text", "bytea", "json", "jsonb", "xml",
		"timestamp", "timestamptz", "timestamp with time zone", "timestamp without time zone",
		"date", "time", "timetz", "time with time zone", "time without time zone", "interval":
		return true
	default:
		return false
	}
}

// pgCannotHaveDefaultType 不能设默认值的类型（对齐 MySQL 引擎的 BLOB/TEXT/JSON 例外）
func pgCannotHaveDefaultType(t string) bool {
	switch strings.ToLower(t) {
	case "text", "bytea", "json", "jsonb", "xml":
		return true
	default:
		return false
	}
}

// ---------- 索引数量 ----------

func (c *pgInspectContext) checkIndexCount(table string) []controllers.SummaryItem {
	if c.params.MAX_INDEX_KEYS <= 0 {
		return nil
	}
	count := len(c.indexByTable[normalizePGIdent(table)])
	if existing, err := c.countExistingIndexes(table); err == nil {
		count += existing
	}
	if count > c.params.MAX_INDEX_KEYS {
		return []controllers.SummaryItem{pgSummary("limit_tbl_idx_count",
			"表`%s`索引数量超出限制（max = %d，当前 %d）", table, c.params.MAX_INDEX_KEYS, count)}
	}
	return nil
}

func (c *pgInspectContext) uniqueIndexPrefix() string {
	if p := strings.TrimSpace(c.params.UNQI_INDEX_PREFIX); p != "" {
		return p
	}
	return "UNIQ_"
}

func (c *pgInspectContext) secondaryIndexPrefix() string {
	if p := strings.TrimSpace(c.params.SECONDARY_INDEX_PREFIX); p != "" {
		return p
	}
	return "IDX_"
}

// ---------- DML ----------

// inspectDML 单条 DML 语句的规则链
func (c *pgInspectContext) inspectDML(stmt string) []controllers.SummaryItem {
	res := analyzePGDML(stmt)
	if !res.OK {
		return nil
	}
	var items []controllers.SummaryItem
	table := res.Table

	switch res.Kind {
	case "update", "delete":
		if c.params.DML_MUST_HAVE_WHERE && !res.HasWhere {
			items = append(items, pgSummary("update_delete_stmt_suggest_where",
				"%s 语句必须指定 WHERE 条件（表`%s`）", strings.ToUpper(res.Kind), table))
		}
		if c.params.DML_DISABLE_SUBQUERY && res.WhereSubquery {
			items = append(items, pgSummary("update_delete_stmt_where_subquery_check",
				"WHERE 条件中不允许使用子查询（表`%s`）", table))
		}
		if c.params.CHECK_DML_JOIN_WITH_ON && res.JoinMissingOn {
			items = append(items, pgSummary("update_delete_stmt_join_on_check",
				"JOIN 语句必须包含 ON 子句（表`%s`）", table))
		}
		if c.params.DML_DISABLE_LIMIT && strings.Contains(strings.ToLower(stripPGStringsAndComments(stmt)), " limit ") {
			items = append(items, pgSummary("limit_update_delete_data_size",
				"UPDATE/DELETE 语句不允许使用 LIMIT（表`%s`）", table))
		}
		// 多表关联数量：超过 MAX_UPDATE_DELETE_JOIN_TABLES 才告警（规则 limit_update_delete_join_table_count）
		if c.params.MAX_UPDATE_DELETE_JOIN_TABLES > 0 && res.JoinTableCount > c.params.MAX_UPDATE_DELETE_JOIN_TABLES {
			items = append(items, pgSummary("limit_update_delete_join_table_count",
				"%s 语句关联表数过多：最多允许 %d 张表（含主表），当前 %d 张（表`%s`）",
				strings.ToUpper(res.Kind), c.params.MAX_UPDATE_DELETE_JOIN_TABLES, res.JoinTableCount, table))
		}
		// LIMIT 数据量：行数超过 DML_MAX_LIMIT_ROWS 才告警（规则 limit_update_delete_data_size）
		if c.params.DML_MAX_LIMIT_ROWS > 0 && res.LimitRows > c.params.DML_MAX_LIMIT_ROWS {
			items = append(items, pgSummary("limit_update_delete_data_size",
				"%s 语句 LIMIT 行数超过上限：最多允许 %d 行，当前 %d 行（表`%s`）",
				strings.ToUpper(res.Kind), c.params.DML_MAX_LIMIT_ROWS, res.LimitRows, table))
		}
		// WHERE 条件 IN 元素个数（规则 limit_where_in_element_count）
		if c.params.MAX_WHERE_IN_ELEMENTS > 0 && res.MaxInElements > c.params.MAX_WHERE_IN_ELEMENTS {
			items = append(items, pgSummary("limit_where_in_element_count",
				"WHERE 条件中 `IN` 子句元素过多：最多允许 %d 个，当前 %d 个（表`%s`）",
				c.params.MAX_WHERE_IN_ELEMENTS, res.MaxInElements, table))
		}
		// 影响行数估算：对同条件做 COUNT(*)（规则 limit_update_delete_affected_rows）。
		// 连库失败 / 无 WHERE（无 WHERE 已被上一条规则拦截）时跳过，与 Oracle/SQLServer 同口径。
		// 走 metaResolver 桩接口：单测可注入计数桩覆盖命中/跳过两条路径（此前直连 c.db 从未被单测验证）。
		if c.params.MAX_AFFECTED_ROWS > 0 && res.HasWhere && strings.TrimSpace(res.WhereClause) != "" &&
			strings.TrimSpace(res.Table) != "" {
			if n, err := c.metaResolverFor(res.TableSchema).CountWhere(res.Table, res.TableSchema, res.WhereClause); err == nil && n > int64(c.params.MAX_AFFECTED_ROWS) {
				items = append(items, pgSummary("limit_update_delete_affected_rows",
					"%s 语句影响行数（约 %d）超过上限 %d（表`%s`），请缩小 WHERE 条件范围或拆分批次",
					strings.ToUpper(res.Kind), n, c.params.MAX_AFFECTED_ROWS, res.Table))
			}
		}
		// UPDATE 建议同步更新「修改时间」列（规则 update_stmt_suggest_update_time_column）
		if res.Kind == "update" && strings.TrimSpace(c.params.UPDATE_TIME_COLUMNS) != "" {
			updated := false
			for _, col := range res.UpdateSetColumns {
				for _, want := range strings.Split(strings.ToLower(c.params.UPDATE_TIME_COLUMNS), ",") {
					if strings.TrimSpace(want) != "" && col == strings.TrimSpace(want) {
						updated = true
					}
				}
			}
			if !updated {
				items = append(items, pgSummary("update_stmt_suggest_update_time_column",
					"UPDATE 语句建议同步更新表上的修改时间列（%s）（表`%s`）", c.params.UPDATE_TIME_COLUMNS, table))
			}
		}
		// UPDATE 不建议更新「创建时间」列（规则 update_stmt_avoid_update_create_time_column）
		if res.Kind == "update" && strings.TrimSpace(c.params.AVOID_UPDATE_COLUMNS) != "" {
			for _, col := range res.UpdateSetColumns {
				// SET 列带表前缀（如 t.create_time）时按后缀匹配
				if i := strings.LastIndex(col, "."); i >= 0 {
					col = col[i+1:]
				}
				for _, avoid := range strings.Split(strings.ToLower(c.params.AVOID_UPDATE_COLUMNS), ",") {
					if strings.TrimSpace(avoid) != "" && strings.ToLower(col) == strings.TrimSpace(avoid) {
						items = append(items, pgSummary("update_stmt_avoid_update_create_time_column",
							"UPDATE 语句不建议更新表上的创建时间列（%s）：创建时间属于数据元信息、具备不可变更属性，更新会破坏数据审计追溯能力（表`%s`）",
							strings.TrimSpace(avoid), table))
					}
				}
			}
		}
	case "insert":
		if c.params.MAX_INSERT_ROWS > 0 && res.InsertValueRows > c.params.MAX_INSERT_ROWS {
			items = append(items, pgSummary("limit_insert_values_row_count",
				"单条 INSERT 语句插入行数超出限制（max = %d，当前 %d）（表`%s`）",
				c.params.MAX_INSERT_ROWS, res.InsertValueRows, table))
		}
		// 目标表是否存在（规则 insert_stmt_check_column_exists）。
		// PG 侧只判表：字段存在性依赖表结构，PG 走 information_schema 的成本与语义
		// 与 MySQL 的 SHOW CREATE TABLE 不同，暂不纳入（宁可少报，不做猜测式误报）。
		// 探测异常（连不上库 / 权限不足）时 err != nil，直接跳过。
		if c.params.CHECK_INSERT_OBJECT_EXISTS && strings.TrimSpace(table) != "" {
			if exists, err := c.insertTargetTableExists(table); err == nil && !exists {
				items = append(items, pgObjectMissing("insert_stmt_check_column_exists", table,
					"① 核对表名拼写与大小写（PG 中未加引号的标识符会被折叠为小写）；② 确认 schema 是否正确；③ 若确认存在仍被报缺失，请先同步实例元数据再重新检查",
					"表`%s`不存在", table))
			}
		}
		// 字段列表和值列表数量要匹配（规则 insert_stmt_columns_match_values）。
		// 有字段列表：值个数与字段个数逐行比对；无字段列表：与真实表列数比对
		// （表不存在 / 查询异常时跳过，由「表是否存在」规则兜底）。`VALUES ()` 合法，跳过。
		if c.params.CHECK_INSERT_COLUMNS_MATCH_VALUES && len(res.InsertValueCols) > 0 {
			if res.InsertExplicitCols > 0 {
				for i, got := range res.InsertValueCols {
					if got == res.InsertExplicitCols {
						continue
					}
					items = append(items, pgSummary("insert_stmt_columns_match_values",
						"INSERT 语句字段列表有 %d 个字段，但第 %d 行 VALUES 提供了 %d 个值，数量不匹配（表`%s`）",
						res.InsertExplicitCols, i+1, got, table))
					break
				}
			} else if cnt, err := c.pgTableColumnCount(table); err == nil && cnt > 0 {
				for i, got := range res.InsertValueCols {
					if got == cnt || got == 0 {
						continue
					}
					items = append(items, pgSummary("insert_stmt_columns_match_values",
						"INSERT 语句未指定字段列表，表`%s`有 %d 个字段，但第 %d 行 VALUES 提供了 %d 个值，数量不匹配",
						table, cnt, i+1, got))
					break
				}
			}
		}
	}
	// 补齐的 15 条 DML 规则（WHERE 类 / SET 分隔符 / 多表前缀 / 主键与唯一键 /
	// 列存在性 / INSERT 字段重复、未指定字段列表、NOT NULL 插 NULL、SYSDATE() /
	// DELETE 表存在性）。传原始语句：LIKE 字面量需要保留引号内内容。
	items = append(items, c.inspectPGDMLTextRules(stmt, stripPGStringsAndComments(stmt), res)...)
	return items
}

// ---------- 元信息探测（连库，失败即跳过，不阻断预审） ----------

// insertTargetTableExists INSERT 目标表存在性判定（规则 insert_stmt_check_column_exists）
//
// 比通用 tableExists 更宽容：information_schema.tables 不覆盖系统目录表
// （pg_class / pg_stat_* 等），只查它会把这些真实存在的表误报成「不存在」。
// 因此再补一次 to_regclass 复核（带 schema 与裸表名各试一次，覆盖 search_path）。
// 复核也查不到才判不存在；查询异常一律返回 err，由调用方跳过。
func (c *pgInspectContext) insertTargetTableExists(table string) (bool, error) {
	exists, err := c.tableExists(table)
	if err != nil || exists {
		return exists, err
	}
	if c.db == nil {
		return false, fmt.Errorf("未初始化数据库连接")
	}
	qualified := sqlQuoteLiteral(table)
	if s := strings.TrimSpace(c.schema); s != "" {
		qualified = sqlQuoteLiteral(s + "." + table)
	}
	count, err := c.countQuery(fmt.Sprintf(
		"SELECT CASE WHEN to_regclass(%s) IS NOT NULL OR to_regclass(%s) IS NOT NULL THEN 1 ELSE 0 END AS count",
		qualified, sqlQuoteLiteral(table)))
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (c *pgInspectContext) tableExists(table string) (bool, error) {
	return c.tableExistsIn(c.schema, table)
}

// tableExistsIn 判断指定 schema 下是否存在该表/视图。
// schema 为空时按 current_schema() 判定（与 PG 未限定标识符的解析规则一致）。
func (c *pgInspectContext) tableExistsIn(schema, table string) (bool, error) {
	if c.db == nil {
		return false, fmt.Errorf("未初始化数据库连接")
	}
	schemaCond := "current_schema()"
	if s := strings.TrimSpace(schema); s != "" {
		schemaCond = sqlQuoteLiteral(s)
	}
	query := fmt.Sprintf(
		"SELECT COUNT(*) AS count FROM information_schema.tables WHERE table_schema = %s AND table_name = %s",
		schemaCond, sqlQuoteLiteral(table))
	count, err := c.countQuery(query)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// pgTableColumnCount 返回目标表在 information_schema.columns 里的列数
// （规则 insert_stmt_columns_match_values 无字段列表时比对真实表结构）。
// 表不存在返回 0；查询异常返回 err，由调用方跳过、不误报。
func (c *pgInspectContext) pgTableColumnCount(table string) (int, error) {
	if c.db == nil {
		return 0, fmt.Errorf("未初始化数据库连接")
	}
	schemaCond := "current_schema()"
	if s := strings.TrimSpace(c.schema); s != "" {
		schemaCond = sqlQuoteLiteral(s)
	}
	query := fmt.Sprintf(
		"SELECT COUNT(*) AS count FROM information_schema.columns WHERE table_schema = %s AND table_name = %s",
		schemaCond, sqlQuoteLiteral(table))
	return c.countQuery(query)
}

func (c *pgInspectContext) countExistingIndexes(table string) (int, error) {
	if c.db == nil {
		return 0, fmt.Errorf("未初始化数据库连接")
	}
	schemaCond := "current_schema()"
	if s := strings.TrimSpace(c.schema); s != "" {
		schemaCond = sqlQuoteLiteral(s)
	}
	query := fmt.Sprintf(
		"SELECT COUNT(*) AS count FROM pg_indexes WHERE schemaname = %s AND tablename = %s",
		schemaCond, sqlQuoteLiteral(table))
	return c.countQuery(query)
}

// tableHasCommentInDB 连库查询表是否已有注释（COMMENT ON TABLE 落在 pg_description，objsubid=0）
func (c *pgInspectContext) tableHasCommentInDB(table string) (bool, error) {
	if c.db == nil {
		return false, fmt.Errorf("未初始化数据库连接")
	}
	schemaCond := "current_schema()"
	if s := strings.TrimSpace(c.schema); s != "" {
		schemaCond = sqlQuoteLiteral(s)
	}
	query := fmt.Sprintf(
		"SELECT COUNT(*) AS count FROM pg_description d "+
			"JOIN pg_class cl ON cl.oid = d.objoid "+
			"JOIN pg_namespace n ON n.oid = cl.relnamespace "+
			"WHERE n.nspname = %s AND cl.relname = %s AND d.objsubid = 0 "+
			"AND d.description IS NOT NULL AND d.description <> ''",
		schemaCond, sqlQuoteLiteral(table))
	count, err := c.countQuery(query)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// pgCountWhere 对目标表按给定 WHERE 条件做 COUNT(*)，估算 UPDATE/DELETE 影响行数
// （规则 limit_update_delete_affected_rows，与 Oracle/SQLServer 的 CountWhere 同口径）。
// 表名/schema 来自语句解析结果（parsePGQualifiedName），双引号包裹并转义后拼接；
// WHERE 条件原文来自用户语句本身，在只读预审连接上执行。查询失败由调用方跳过、不误报。
func (c *pgInspectContext) pgCountWhere(table, schema, whereClause string) (int64, error) {
	if c.db == nil {
		return 0, fmt.Errorf("未初始化数据库连接")
	}
	qualified := pgQuoteIdent(table)
	if s := strings.TrimSpace(schema); s != "" {
		qualified = pgQuoteIdent(s) + "." + qualified
	}
	n, err := c.countQuery(fmt.Sprintf("SELECT COUNT(*) AS count FROM %s WHERE %s", qualified, whereClause))
	if err != nil {
		return 0, err
	}
	return int64(n), nil
}

// pgQuoteIdent PG 标识符引用：双引号包裹并转义内部双引号
func pgQuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(s), `"`, `""`) + `"`
}

// countQuery 执行 COUNT 查询并取出结果（列名固定 count）
func (c *pgInspectContext) countQuery(query string) (int, error) {
	result, err := c.db.Query(query)
	if err != nil {
		return 0, err
	}
	if result == nil {
		return 0, nil
	}
	rows := *result
	if len(rows) == 0 {
		return 0, nil
	}
	// 结果可能有多列，取 count 列（缺失时取唯一的数值列）
	keys := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if v, ok := rows[0]["count"]; ok {
		if s, ok := v.(string); ok {
			return atoiSafe(strings.TrimSpace(s)), nil
		}
	}
	for _, k := range keys {
		if s, ok := rows[0][k].(string); ok {
			return atoiSafe(strings.TrimSpace(s)), nil
		}
	}
	return 0, nil
}

// sqlQuoteLiteral 转义字符串字面量（防注入：单引号翻倍）
func sqlQuoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
