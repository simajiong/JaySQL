package checker

// SQL Server 引擎的「对象存在性 / 对象冲突 / 元信息」规则
//
// 与 PostgreSQL 引擎同构：抽出 sqlServerMetaResolver 接口，生产实现走 sys.* 目录视图，
// 单测注入桩实现。这样「必须连库才能判定」的规则可以脱离真实库做单测，
// 且连库失败时一律跳过（宁可漏报，绝不误报）。
//
// sys.* 目录视图的选取理由：
//   - sys.objects / sys.schemas / sys.columns / sys.indexes 是 SQL Server 官方元数据视图，
//     权限要求低（public 角色即可读），且只需要 SELECT 权限；
//   - 不用 INFORMATION_SCHEMA：它不覆盖扩展属性（表/列注释的唯一载体），也不区分聚集/非聚集索引。
//
// 注释口径（与 MySQL/Oracle 的 COMMENT 语义对齐）：
//   SQL Server 没有 COMMENT 语法，表/列注释落在扩展属性 MS_Description 上
//   （class=1，minor_id=0 表示表级，minor_id>0 表示列级）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
)

// sqlServerMetaResolver 元信息探针：生产实现走 sys.* 目录视图，测试可注入桩实现。
type sqlServerMetaResolver interface {
	TableExists(name sqlServerName) (bool, error)
	ColumnExists(name sqlServerName, column string) (bool, error)
	IndexExists(name sqlServerName, index string) (bool, error)
	TableColumnCount(name sqlServerName) (int, error)
	IndexCount(name sqlServerName) (int, error)
	PrimaryKeyColumns(name sqlServerName) ([]string, error)
	UniqueIndexColumns(name sqlServerName) ([]string, error)
	ColumnNullable(name sqlServerName, column string) (bool, error)
	TableCommented(name sqlServerName) (bool, error)
	ColumnCommented(name sqlServerName, column string) (bool, error)
	IndexedLeadingColumns(name sqlServerName) ([]string, error)
	CountWhere(name sqlServerName, where string) (int, error)
}

// sqlServerDBResolver 生产实现：复用上下文里的连接信息按 sys.* 查询。
type sqlServerDBResolver struct {
	ctx *sqlServerInspectContext
}

// sqlServerSchemaCond 生成 sys.schemas 的模式过滤条件
func sqlServerSchemaCond(schema string) string {
	if s := strings.TrimSpace(schema); s != "" {
		return fmt.Sprintf(" AND s.name = %s", sqlQuoteLiteral(s))
	}
	return ""
}

func (r sqlServerDBResolver) TableExists(name sqlServerName) (bool, error) {
	q := fmt.Sprintf(
		"SELECT COUNT(*) AS count FROM sys.objects o JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"WHERE o.name = %s AND o.type IN ('U','V')%s",
		sqlQuoteLiteral(name.Object), sqlServerSchemaCond(name.EffectiveSchema()))
	n, err := r.ctx.countQuery(q)
	return n > 0, err
}

func (r sqlServerDBResolver) ColumnExists(name sqlServerName, column string) (bool, error) {
	q := fmt.Sprintf(
		"SELECT COUNT(*) AS count FROM sys.columns c "+
			"JOIN sys.objects o ON o.object_id = c.object_id "+
			"JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"WHERE o.name = %s AND c.name = %s%s",
		sqlQuoteLiteral(name.Object), sqlQuoteLiteral(column), sqlServerSchemaCond(name.EffectiveSchema()))
	n, err := r.ctx.countQuery(q)
	return n > 0, err
}

func (r sqlServerDBResolver) IndexExists(name sqlServerName, index string) (bool, error) {
	q := fmt.Sprintf(
		"SELECT COUNT(*) AS count FROM sys.indexes i "+
			"JOIN sys.objects o ON o.object_id = i.object_id "+
			"JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"WHERE o.name = %s AND i.name = %s AND i.index_id > 0%s",
		sqlQuoteLiteral(name.Object), sqlQuoteLiteral(index), sqlServerSchemaCond(name.EffectiveSchema()))
	n, err := r.ctx.countQuery(q)
	return n > 0, err
}

func (r sqlServerDBResolver) TableColumnCount(name sqlServerName) (int, error) {
	q := fmt.Sprintf(
		"SELECT COUNT(*) AS count FROM sys.columns c "+
			"JOIN sys.objects o ON o.object_id = c.object_id "+
			"JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"WHERE o.name = %s%s",
		sqlQuoteLiteral(name.Object), sqlServerSchemaCond(name.EffectiveSchema()))
	return r.ctx.countQuery(q)
}

func (r sqlServerDBResolver) IndexCount(name sqlServerName) (int, error) {
	q := fmt.Sprintf(
		"SELECT COUNT(*) AS count FROM sys.indexes i "+
			"JOIN sys.objects o ON o.object_id = i.object_id "+
			"JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"WHERE o.name = %s AND i.index_id > 0 AND i.is_hypothetical = 0%s",
		sqlQuoteLiteral(name.Object), sqlServerSchemaCond(name.EffectiveSchema()))
	return r.ctx.countQuery(q)
}

func (r sqlServerDBResolver) PrimaryKeyColumns(name sqlServerName) ([]string, error) {
	q := fmt.Sprintf(
		"SELECT c.name AS name FROM sys.indexes i "+
			"JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id "+
			"JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id "+
			"JOIN sys.objects o ON o.object_id = i.object_id "+
			"JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"WHERE o.name = %s AND i.is_primary_key = 1%s ORDER BY ic.key_ordinal",
		sqlQuoteLiteral(name.Object), sqlServerSchemaCond(name.EffectiveSchema()))
	return r.ctx.queryStrings(q, "name")
}

func (r sqlServerDBResolver) UniqueIndexColumns(name sqlServerName) ([]string, error) {
	q := fmt.Sprintf(
		"SELECT DISTINCT c.name AS name FROM sys.indexes i "+
			"JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id "+
			"JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id "+
			"JOIN sys.objects o ON o.object_id = i.object_id "+
			"JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"WHERE o.name = %s AND i.is_unique = 1 AND i.is_primary_key = 0%s",
		sqlQuoteLiteral(name.Object), sqlServerSchemaCond(name.EffectiveSchema()))
	return r.ctx.queryStrings(q, "name")
}

func (r sqlServerDBResolver) ColumnNullable(name sqlServerName, column string) (bool, error) {
	q := fmt.Sprintf(
		"SELECT COUNT(*) AS count FROM sys.columns c "+
			"JOIN sys.objects o ON o.object_id = c.object_id "+
			"JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"WHERE o.name = %s AND c.name = %s AND c.is_nullable = 1%s",
		sqlQuoteLiteral(name.Object), sqlQuoteLiteral(column), sqlServerSchemaCond(name.EffectiveSchema()))
	n, err := r.ctx.countQuery(q)
	return n > 0, err
}

func (r sqlServerDBResolver) TableCommented(name sqlServerName) (bool, error) {
	q := fmt.Sprintf(
		"SELECT COUNT(*) AS count FROM sys.extended_properties ep "+
			"JOIN sys.objects o ON o.object_id = ep.major_id "+
			"JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"WHERE o.name = %s AND ep.class = 1 AND ep.minor_id = 0 AND ep.name = 'MS_Description' "+
			"AND ep.value IS NOT NULL AND CONVERT(NVARCHAR(4000), ep.value) <> ''%s",
		sqlQuoteLiteral(name.Object), sqlServerSchemaCond(name.EffectiveSchema()))
	n, err := r.ctx.countQuery(q)
	return n > 0, err
}

func (r sqlServerDBResolver) ColumnCommented(name sqlServerName, column string) (bool, error) {
	q := fmt.Sprintf(
		"SELECT COUNT(*) AS count FROM sys.extended_properties ep "+
			"JOIN sys.objects o ON o.object_id = ep.major_id "+
			"JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"JOIN sys.columns c ON c.object_id = ep.major_id AND c.column_id = ep.minor_id "+
			"WHERE o.name = %s AND c.name = %s AND ep.class = 1 AND ep.name = 'MS_Description' "+
			"AND ep.value IS NOT NULL AND CONVERT(NVARCHAR(4000), ep.value) <> ''%s",
		sqlQuoteLiteral(name.Object), sqlQuoteLiteral(column), sqlServerSchemaCond(name.EffectiveSchema()))
	n, err := r.ctx.countQuery(q)
	return n > 0, err
}

func (r sqlServerDBResolver) IndexedLeadingColumns(name sqlServerName) ([]string, error) {
	q := fmt.Sprintf(
		"SELECT DISTINCT c.name AS name FROM sys.indexes i "+
			"JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id "+
			"JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id "+
			"JOIN sys.objects o ON o.object_id = i.object_id "+
			"JOIN sys.schemas s ON s.schema_id = o.schema_id "+
			"WHERE o.name = %s AND i.index_id > 0 AND ic.key_ordinal = 1%s",
		sqlQuoteLiteral(name.Object), sqlServerSchemaCond(name.EffectiveSchema()))
	return r.ctx.queryStrings(q, "name")
}

// CountWhere 对目标表按给定 WHERE 条件做 COUNT(*)，用于估算 UPDATE/DELETE 影响行数。
func (r sqlServerDBResolver) CountWhere(name sqlServerName, where string) (int, error) {
	where = strings.TrimSpace(where)
	if where == "" {
		return 0, fmt.Errorf("WHERE 条件为空")
	}
	q := fmt.Sprintf("SELECT COUNT_BIG(*) AS count FROM [%s].[%s] WHERE %s",
		escapeSQLServerBracket(name.EffectiveSchema()), escapeSQLServerBracket(name.Object), where)
	return r.ctx.countQuery(q)
}

// escapeSQLServerBracket 转义方括号标识符里的 `]`（T-SQL 用 `]]` 表示）。
func escapeSQLServerBracket(s string) string {
	return strings.ReplaceAll(s, "]", "]]")
}

// metaResolver 取当前上下文的元信息探针（未注入桩实现时走真实库查询）。
func (c *sqlServerInspectContext) metaResolver() sqlServerMetaResolver {
	if c.meta != nil {
		return c.meta
	}
	return sqlServerDBResolver{ctx: c}
}

// sqlServerObjectConflict 构造「目标对象已存在」的冲突结论（与 pgObjectConflict 同构）。
func sqlServerObjectConflict(ruleKey, object, advice, format string, args ...any) controllers.SummaryItem {
	return controllers.SummaryItem{
		Level:    LevelWarn,
		RuleKey:  ruleKey,
		Category: controllers.CategoryObjectConflict,
		Object:   object,
		Advice:   advice,
		Message:  fmt.Sprintf(format, args...),
	}
}

// ---------- 元信息探测（连库，失败即跳过，不阻断预审） ----------

// countQuery 执行 COUNT 查询并取出结果（列名固定 count）。
func (c *sqlServerInspectContext) countQuery(query string) (int, error) {
	if c.db == nil {
		return 0, fmt.Errorf("未初始化数据库连接")
	}
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
	if v, ok := rows[0]["count"]; ok {
		if s, ok := v.(string); ok {
			return atoiSafe(strings.TrimSpace(s)), nil
		}
	}
	// 结果列名大小写随驱动返回，兜底取唯一的数值列
	keys := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := rows[0][k].(string); ok {
			return atoiSafe(strings.TrimSpace(s)), nil
		}
	}
	return 0, nil
}

// queryStrings 取结果集某一列的字符串值（列名大小写不敏感）。
func (c *sqlServerInspectContext) queryStrings(query, column string) ([]string, error) {
	if c.db == nil {
		return nil, fmt.Errorf("未初始化数据库连接")
	}
	result, err := c.db.Query(query)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	out := make([]string, 0, len(*result))
	for _, row := range *result {
		for k, v := range row {
			if !strings.EqualFold(k, column) {
				continue
			}
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	return out, nil
}
