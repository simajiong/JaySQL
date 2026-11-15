package checker

// SQL Server 引擎的「语句准入闸门」
//
// 与 MySQL / PostgreSQL 引擎同一层语义：判定「这类语句能不能执行」，命中即给出
// **语句级**结论，不再进入规则链 —— CTAS 与视图的表结构来自别处，
// 字段/索引/约束规范既无法满足，也不是用户该处理的问题。
//
//	语句                          规范规则（rule_key 后缀）      引擎参数                 引擎默认
//	SELECT ... INTO t             disable_create_table_as         ENABLE_CREATE_TABLE_AS   false（禁止）
//	CREATE/ALTER/DROP VIEW        disable_view_related_op         ENABLE_CREATE_VIEW       true（允许）
//
// 与 MySQL/PG 的语法差异：
//   - CTAS：T-SQL 的等价写法是 `SELECT ... INTO 新表 FROM ...`（不是 CREATE TABLE ... AS SELECT），
//     两者语义完全相同（表结构来自 SELECT），归到同一条规则；
//   - 视图：T-SQL 还支持 `CREATE OR ALTER VIEW`，一并纳入「禁止视图相关操作」。

import (
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
)

// gateCreateTable 判定 `CREATE TABLE ... AS SELECT`（T-SQL 极少见但 Azure Synapse 支持）。
func (c *sqlServerInspectContext) gateCreateTable(stmt string) []controllers.SummaryItem {
	if c.params.ENABLE_CREATE_TABLE_AS {
		return nil
	}
	clean := stripPGStringsAndComments(stmt)
	lower := strings.ToLower(clean)
	if indexOfKeyword(lower, " as ") < 0 && indexOfKeyword(lower, "as select") < 0 {
		return nil
	}
	name, _, ok := trimSQLServerCreateTablePrefix(clean)
	if !ok {
		return nil
	}
	return []controllers.SummaryItem{controllers.ForbiddenStatement("disable_create_table_as", name.Object,
		"① 改为两步执行：先 `CREATE TABLE 表名 (列定义…)`，再 `INSERT INTO 表名 SELECT …`；"+
			"② 若确需一次性建表并灌数据，请联系 DBA 在实例绑定的「SQL 开发规范」中关闭「禁止 CREATE TABLE ... AS SELECT」规则",
		fmt.Sprintf("语句不允许执行：`CREATE TABLE ... AS SELECT`（表`%s`），该语法会绕过字段/索引/约束等建表规范", name.Object))}
}

// gateSelectInto 判定 `SELECT ... INTO 新表 FROM ...`（T-SQL 的 CTAS）。
//
// 单独成闸门的原因：这条语句的语句类型是 EXPORT（以 SELECT 开头），不经过 CREATE TABLE 分支，
// 但它确实在库里建了一张新表，必须与 CREATE TABLE AS SELECT 同等对待。
func (c *sqlServerInspectContext) gateSelectInto(stmt string) []controllers.SummaryItem {
	if c.params.ENABLE_CREATE_TABLE_AS {
		return nil
	}
	name, ok := sqlServerSelectIntoTable(stmt)
	if !ok {
		return nil
	}
	return []controllers.SummaryItem{controllers.ForbiddenStatement("disable_create_table_as", name.Object,
		"① 改为两步执行：先 `CREATE TABLE 表名 (列定义…)`，再 `INSERT INTO 表名 SELECT …`；"+
			"② 若确需一次性建表并灌数据，请联系 DBA 在实例绑定的「SQL 开发规范」中关闭「禁止 CREATE TABLE ... AS SELECT」规则",
		fmt.Sprintf("语句不允许执行：`SELECT ... INTO`（新表`%s`），该语法会绕过字段/索引/约束等建表规范", name.Object))}
}

// inspectView 视图相关操作（CREATE / CREATE OR ALTER / ALTER / DROP VIEW）。
func (c *sqlServerInspectContext) inspectView(stmt string) []controllers.SummaryItem {
	op, view, ok := parseSQLServerViewStatement(stmt)
	if !ok {
		return nil
	}
	object := view.Object
	desc := fmt.Sprintf("`%s`", op)
	if view.Object != "" {
		desc = fmt.Sprintf("`%s`（视图`%s`）", op, view.Object)
	}
	if c.params.ENABLE_CREATE_VIEW {
		// 视图允许时仍有一项结构性判定：目标视图已存在（CREATE VIEW 会失败，错误号 2714）
		if op == "DROP VIEW" || view.Object == "" {
			return nil
		}
		if strings.HasPrefix(op, "CREATE") {
			if exists, err := c.metaResolver().TableExists(view); err == nil && exists {
				return []controllers.SummaryItem{sqlServerObjectConflict("obj_view_must_not_exist", object,
					"① 改用 `CREATE OR ALTER VIEW`（幂等）；② 或先单独提交 `DROP VIEW` 工单再创建",
					"视图`%s`已存在，`CREATE VIEW` 执行必然失败", view.Object)}
			}
		}
		return nil
	}
	return []controllers.SummaryItem{controllers.ForbiddenStatement("disable_view_related_op", object,
		"① 改为在应用层封装查询，或使用普通表承载数据；"+
			"② 若确需视图，请联系 DBA 在实例绑定的「SQL 开发规范」中关闭「禁止视图相关操作」规则",
		fmt.Sprintf("语句不允许执行：%s，平台规范禁止使用视图对象", desc))}
}
