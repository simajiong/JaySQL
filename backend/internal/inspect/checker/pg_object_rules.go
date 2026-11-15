package checker

// PG 引擎的「对象存在性 / 对象冲突」规则
//
// 与 MySQL 引擎一一对应（rule_key 后缀一致，判级与规范配置完全复用，
// 差异只在语法识别方式：MySQL 走 TiDB AST，PG 走语法感知的文本解析）：
//
//	MySQL 实现位置                               规则（rule_key 后缀）   PG 语句
//	logics.LogicAlterTableIsExist                obj_tbl_must_exist      ALTER TABLE
//	logics.LogicAlterTableDropColsOrIndexes      obj_col_must_exist      DROP COLUMN
//	logics.LogicAlterTableModifyColOptions       obj_col_must_exist      ALTER COLUMN ... TYPE/SET/DROP
//	logics.LogicAlterTableChangeColOptions       obj_col_must_exist      RENAME COLUMN（旧列名）
//	logics.LogicAlterTableRenameIndex            obj_idx_must_exist      DROP INDEX / ALTER INDEX
//	logics.LogicAlterTableRenameIndex            obj_idx_must_not_exist  ALTER INDEX ... RENAME TO
//	logics.LogicAlterTableChangeColOptions       obj_col_must_not_exist  RENAME COLUMN（新列名）
//	logics.LogicAlterTableRenameTblName          obj_tbl_must_not_exist  ALTER TABLE ... RENAME TO
//
// 判定原则与 PG 引擎其余规则一致：
//  1. 查询异常（连不上库 / 权限不足 / 元信息查不到）一律跳过，绝不猜测；
//  2. 语句自带 `IF EXISTS` 时目标对象允许不存在，不产出结论 ——
//     否则会把「能正常执行」的语句报成「执行必然失败」，这是比漏报更糟的误报；
//  3. 表已确认不存在时不再逐列判定（那只会衍生一串噪音），由表级结论代表。

import (
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
)

// pgMetaResolver 元信息探针：生产实现走 information_schema / pg_catalog，测试可注入桩实现。
//
// 抽出接口而不是直接调 c.db，是为了让「对象存在性」这组规则可以脱离真实库做单测
// （PG 引擎其余规则同样遵循「不连库就不判定」的约定，见 pg_rules_test.go）。
type pgMetaResolver interface {
	TableExists(table string) (bool, error)
	ColumnExists(table, column string) (bool, error)
	IndexExists(index string) (bool, error)
	// CountWhere 对目标表按 WHERE 原文做 COUNT(*)，估算 UPDATE/DELETE 影响行数
	// （规则 limit_update_delete_affected_rows，与 Oracle/SQLServer 的 CountWhere 同构）。
	// schema 为空表示按连接的 search_path 处理。
	CountWhere(table, schema, whereClause string) (int64, error)
}

// pgDBResolver 生产实现：复用 pgInspectContext 已有的连接与 schema 处理。
//
// schema 非空表示语句里显式限定了模式（`ALTER TABLE sch.t ...` / `DROP INDEX sch.idx`）：
// 此时必须按该模式查询，否则会落到工单 InspectSchema，把「另一个模式里的同名对象」
// 判成不存在（漏/误报都对不上用户的实际执行结果）。
type pgDBResolver struct {
	ctx    *pgInspectContext
	schema string
}

// TableExists 表/视图存在性判定。
// 先走 information_schema（大小写不敏感，覆盖正常业务表），查不到再用 to_regclass 复核：
// information_schema 不覆盖系统目录表（pg_class / pg_stat_* 等），只查它会把这些真实存在的表
// 误报成「不存在」—— 与 insertTargetTableExists 的处理口径一致（宁可漏报，绝不误报）。
func (r pgDBResolver) TableExists(table string) (bool, error) {
	exists, err := r.ctx.tableExistsIn(r.schema, table)
	if err != nil || exists {
		return exists, err
	}
	return r.ctx.toRegclassExistsIn(r.schema, table)
}

func (r pgDBResolver) ColumnExists(table, column string) (bool, error) {
	return r.ctx.columnExistsIn(r.schema, table, column)
}

func (r pgDBResolver) IndexExists(index string) (bool, error) {
	return r.ctx.indexExistsIn(r.schema, index)
}

// CountWhere 影响行数估算：对同条件做 COUNT(*)。
// schema 以语句解析结果为准（res.TableSchema），语句未限定时按 search_path 处理；
// 不使用 r.schema 兜底，与原 pgCountWhere 行为保持一致。
func (r pgDBResolver) CountWhere(table, schema, whereClause string) (int64, error) {
	return r.ctx.pgCountWhere(table, schema, whereClause)
}

// metaResolver 取当前上下文的元信息探针（未注入桩实现时走真实库查询）。
func (c *pgInspectContext) metaResolver() pgMetaResolver {
	if c.meta != nil {
		return c.meta
	}
	return pgDBResolver{ctx: c}
}

// metaResolverFor 取「按语句显式 schema」的元信息探针。
// schema 为空时等价于 metaResolver()（按工单 InspectSchema / 当前 search_path 处理）。
func (c *pgInspectContext) metaResolverFor(schema string) pgMetaResolver {
	if c.meta != nil {
		return c.meta
	}
	if strings.TrimSpace(schema) == "" {
		return pgDBResolver{ctx: c}
	}
	return pgDBResolver{ctx: c, schema: schema}
}

// pgObjectConflict 构造「目标对象已存在」的冲突结论。
// 带 category / object / advice，前端可渲染四要素卡片；MySQL 侧对应 WarnFatal。
func pgObjectConflict(ruleKey, object, advice, format string, args ...any) controllers.SummaryItem {
	return controllers.SummaryItem{
		Level:    LevelWarn,
		RuleKey:  ruleKey,
		Category: controllers.CategoryObjectConflict,
		Object:   object,
		Advice:   advice,
		Message:  fmt.Sprintf(format, args...),
	}
}

// checkAlterTableObjects ALTER TABLE 上的对象存在性/冲突判定。
func (c *pgInspectContext) checkAlterTableObjects(stmt string) []controllers.SummaryItem {
	act, ok := parseAlterTableActions(stmt)
	if !ok || strings.TrimSpace(act.Table) == "" {
		return nil
	}
	var items []controllers.SummaryItem
	// 语句显式限定了 schema 时按该 schema 探测，否则落到工单 InspectSchema（metaResolverFor 内处理）
	meta := c.metaResolverFor(act.Schema)

	// 1) 目标表必须存在（PG 42P01）。
	// 与 MySQL 一致：这条结论不阻断后续判定，但表确实不存在时列级判定没有意义 ——
	// 只在「确认存在」时继续，避免一条缺失结论衍生出一串重复的列缺失噪音。
	tableExists := false
	if exists, err := meta.TableExists(act.Table); err == nil {
		tableExists = exists
		if !exists && !act.IfExists {
			items = append(items, pgObjectMissing("obj_tbl_must_exist", act.Table,
				"① 核对表名拼写与大小写（PG 中未加引号的标识符会被折叠为小写）；② 确认是否选错了模式（schema）；③ 若该表应由本工单创建，请调整语句顺序；④ 若确认存在仍被报缺失，请先同步实例元数据再重新检查",
				"表`%s`不存在，无法执行 `ALTER TABLE`", act.Table))
		}
	}
	if !tableExists {
		return items
	}

	// 2) DROP COLUMN：列必须存在（PG 42703）
	for _, col := range act.DropColumns {
		if exists, err := meta.ColumnExists(act.Table, col); err == nil && !exists {
			items = append(items, pgObjectMissing("obj_col_must_exist", col,
				"① 核对列名拼写与大小写；② 该列可能已被其他工单删除，请先同步实例元数据再重新检查；③ 需要幂等删除请改用 `DROP COLUMN IF EXISTS`",
				"表`%s`中要 DROP 的列`%s`不存在", act.Table, col))
		}
	}

	// 3) ALTER COLUMN（改类型 / SET NOT NULL / SET DEFAULT / DROP DEFAULT 等）：列必须存在
	for _, col := range act.AlterColumns {
		if exists, err := meta.ColumnExists(act.Table, col); err == nil && !exists {
			items = append(items, pgObjectMissing("obj_col_must_exist", col,
				"① 核对列名拼写与大小写；② 若是新增列请改用 ADD COLUMN；③ 该列可能已被其他工单删除，请先同步实例元数据再重新检查",
				"列`%s`不存在（表`%s`）", col, act.Table))
		}
	}

	// 4) RENAME COLUMN：旧列名必须存在，新列名不能已存在（PG 42701 duplicate column）
	for _, pair := range act.RenameColumns {
		if exists, err := meta.ColumnExists(act.Table, pair.Old); err == nil && !exists {
			items = append(items, pgObjectMissing("obj_col_must_exist", pair.Old,
				"① 核对原列名拼写与大小写；② 该列可能已被其他工单删除或改名，请先同步实例元数据再重新检查",
				"表`%s`中原列`%s`不存在，无法重命名为`%s`", act.Table, pair.Old, pair.New))
		}
		if strings.EqualFold(pair.Old, pair.New) {
			continue // 同名（PG 会直接报错但无冲突语义），不重复告警
		}
		if exists, err := meta.ColumnExists(act.Table, pair.New); err == nil && exists {
			items = append(items, pgObjectConflict("obj_col_must_not_exist", pair.New,
				"① 换一个新列名；② 若目标就是覆盖，请先单独提交删除/改名的工单，避免改名产生列冲突",
				"表`%s`中新列`%s`已存在，`RENAME COLUMN` 会导致列名冲突", act.Table, pair.New))
		}
	}

	// 5) RENAME TO：新表名不能已存在（PG 42P07）
	if act.RenameTable != "" {
		if exists, err := meta.TableExists(act.RenameTable); err == nil && exists {
			items = append(items, pgObjectConflict("obj_tbl_must_not_exist", act.RenameTable,
				"① 更换新表名；② 若目标就是覆盖，请先单独提交 DROP TABLE 工单（数据将丢失，需另行评估）",
				"目标表名`%s`已存在，无法将表`%s`重命名为该名称", act.RenameTable, act.Table))
		}
	}
	return items
}

// inspectIndexObject DROP INDEX / ALTER INDEX 上的索引存在性/冲突判定。
func (c *pgInspectContext) inspectIndexObject(stmt string) []controllers.SummaryItem {
	op, ok := parseIndexObjectOp(stmt)
	if !ok || len(op.Names) == 0 {
		return nil
	}
	meta := c.metaResolverFor(op.Schema)
	var items []controllers.SummaryItem

	// 1) 目标索引必须存在（PG 42704 undefined object）
	if !op.IfExists {
		for _, name := range op.Names {
			if exists, err := meta.IndexExists(name); err == nil && !exists {
				items = append(items, pgObjectMissing("obj_idx_must_exist", name,
					"① 核对索引名拼写与大小写；② 该索引可能已被其他工单删除，请先同步实例元数据再重新检查；③ 需要幂等删除请改用 `DROP INDEX IF EXISTS`",
					"索引`%s`不存在，无法执行 `%s`", name, op.Op))
			}
		}
	}

	// 2) 改名目标不能已存在（PG 42710 duplicate object）
	if op.RenameTo != "" && len(op.Names) == 1 {
		if exists, err := meta.IndexExists(op.RenameTo); err == nil && exists {
			items = append(items, pgObjectConflict("obj_idx_must_not_exist", op.RenameTo,
				"① 换一个新索引名；② 若目标就是覆盖，请先单独提交 DROP INDEX 工单，避免重命名冲突",
				"新索引名`%s`已存在，无法把索引`%s`重命名为该名称", op.RenameTo, op.Names[0]))
		}
	}
	return items
}

// ---------- 元信息探测（连库，失败即跳过，不阻断预审） ----------

// pgSchemaCondFor 返回 ` AND n.nspname = '<schema>'` 形式的模式过滤。
// schema 为空时不加条件，交给 pg_catalog 按全模式匹配 —— 宁可漏报也不误报。
func pgSchemaCondFor(schema string) string {
	if s := strings.TrimSpace(schema); s != "" {
		return fmt.Sprintf(" AND n.nspname = %s", sqlQuoteLiteral(s))
	}
	return ""
}

// toRegclassExistsIn 用 to_regclass 复核对象是否存在（同时尝试 schema 限定名与裸表名，
// 覆盖 search_path 生效的情况；不存在的对象返回 NULL 而不是报错）。
func (c *pgInspectContext) toRegclassExistsIn(schema, table string) (bool, error) {
	if c.db == nil {
		return false, fmt.Errorf("未初始化数据库连接")
	}
	qualified := sqlQuoteLiteral(table)
	if s := strings.TrimSpace(schema); s != "" {
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

// columnExistsIn 判断表上是否存在指定列。
//
// 走 pg_attribute 而不是 information_schema.columns：后者不覆盖系统目录表，
// 对 `ALTER TABLE pg_class DROP COLUMN relname` 这类语句会误报「列不存在」。
// 列名比较大小写不敏感（PG 未加引号的标识符会折叠为小写），已删除列（attisdropped）跳过。
func (c *pgInspectContext) columnExistsIn(schema, table, column string) (bool, error) {
	if c.db == nil {
		return false, fmt.Errorf("未初始化数据库连接")
	}
	query := fmt.Sprintf(
		"SELECT COUNT(*) AS count FROM pg_attribute a "+
			"JOIN pg_class c ON c.oid = a.attrelid "+
			"JOIN pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE lower(c.relname) = lower(%s) AND a.attnum > 0 AND NOT a.attisdropped "+
			"AND lower(a.attname) = lower(%s)%s",
		sqlQuoteLiteral(table), sqlQuoteLiteral(column), pgSchemaCondFor(schema))
	count, err := c.countQuery(query)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// indexExistsIn 判断是否存在指定索引（索引名在 schema 内唯一；relkind i/I 分别对应普通索引与分区索引）
func (c *pgInspectContext) indexExistsIn(schema, index string) (bool, error) {
	if c.db == nil {
		return false, fmt.Errorf("未初始化数据库连接")
	}
	query := fmt.Sprintf(
		"SELECT COUNT(*) AS count FROM pg_class c "+
			"JOIN pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE c.relkind IN ('i','I') AND lower(c.relname) = lower(%s)%s",
		sqlQuoteLiteral(index), pgSchemaCondFor(schema))
	count, err := c.countQuery(query)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
