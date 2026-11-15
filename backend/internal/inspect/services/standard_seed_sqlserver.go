package services

import (
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/models"
)

// SQLServer 规范规则目录
//
// 设计原则（与 mysql / postgres / oracle / tidb 段保持一致，便于跨库比对）：
//  1. rule_key 采用 `sqlserver.{struct|query_update}.<能力后缀>`，能力后缀沿用既有
//     82 个后缀中「在 SQL Server 上有等价语义」的部分，前端能力项表格据此跨库合并成一行；
//  2. BaseLevel / SortOrder 与 MySQL 段同后缀逐项对齐 —— 同一能力项在不同数据库上的
//     默认级别必须一致，否则用户切换数据库会看到级别漂移；
//  3. Name 沿用既有文案（前端按名称做运营检索），Description 改写为 SQL Server 口径，
//     把「MySQL 语法 → T-SQL 语法」的差异写清楚（IDENTITY / 扩展属性 / NVARCHAR / TOP）；
//  4. 只收录引擎确实会产出的 key —— 目录里存在但引擎不产出的规则是「幽灵规则」
//     （页面能配级别、永远不触发），由 standard_seed_sqlserver_test.go 守门。
//
// 未收录的 10 条 MySQL/TiDB 规则（SQL Server 无对应语义，收录会成为幽灵规则）：
//
//	limit_auto_inc_col_is_unsigned          SQL Server 无 UNSIGNED 属性
//	limit_table_storage_engine              SQL Server 无存储引擎概念（只有文件组）
//	limit_database_charset                  库级是排序规则（collation），无字符集
//	no_column_charset                       无列级字符集语法（由 VARCHAR/NVARCHAR 决定）
//	limit_tbl_charset                       表级无字符集语法
//	limit_tbl_collation                     表级无 collation 语法（只有列级）
//	tbl_charset_change_use_convert_syntax   无 ALTER TABLE ... CONVERT TO CHARACTER SET
//	column_zerofill_forbidden               无 ZEROFILL 属性
//	disable_event_related_op                无 EVENT 对象（MySQL 事件调度器）
//	disable_create_table_like               无 CREATE TABLE ... LIKE（等价能力是 SELECT INTO，
//	                                        已归入 disable_create_table_as）
func sqlserverStandardRules() []models.InsightSQLStandardRule {
	seeds := []sqlserverSeedRule{
		// ---------- 结构：列 ----------
		{
			key: "struct.limit_auto_inc_col_name_is_id", ruleType: "列", level: "must", sort: 0,
			name: "限制自增列名为 ID",
			desc: "限制自增列名（IDENTITY 列）为 ID",
		},
		{
			key: "struct.limit_create_table_auto_inc_init", ruleType: "表", level: "must", sort: 2,
			name: "限制建表自增列初始值",
			desc: "限制建表自增初始值（IDENTITY(seed, increment) 的 seed）",
		},
		{
			key: "struct.limit_char_type_col_len", ruleType: "列", level: "must", sort: 3,
			name:   "限制 CHAR 类型字段的长度",
			desc:   "限制 CHAR / NCHAR 类型字段长度（长度按字符计，非字节）",
			params: `{"max_length": 64}`,
		},
		{
			key: "struct.limit_col_name_case", ruleType: "列", level: "must", sort: 4,
			name: "限制字段名的大小写",
			desc: "限制字段名大小写（SQL Server 默认排序规则下标识符大小写不敏感，此处约束命名风格）",
		},
		{
			key: "struct.col_name_no_keyword", ruleType: "列", level: "must", sort: 5,
			name: "限制字段名不能是关键字",
			desc: "字段名不能是关键字（T-SQL 保留字）",
		},
		{
			// 注释类规则：目录基线与 MySQL 对齐保持 must（TestSQLServerSeedLevelAlignedWithMySQL），
			// 但默认级别放宽为 suggest —— SQL Server 注释需 sp_addextendedproperty，成本高于内联 COMMENT，
			// 强制拦截对存量表/快速建表场景过重，与其他引擎在注释规则上的实际口径一致。
			key: "struct.limit_col_must_have_comment", ruleType: "列", level: "suggest", base: "must", sort: 6,
			name: "限制列必须有注释",
			desc: "列要有注释（SQL Server 无内联 COMMENT 语法，需 sp_addextendedproperty 扩展属性）",
		},
		{
			key: "struct.limit_varchar_type_col_len", ruleType: "列", level: "must", sort: 13,
			name:   "限制 VARCHAR 类型字段的长度",
			desc:   "限制 VARCHAR / NVARCHAR 类型字段长度（MAX 视为超限）",
			params: `{"max_length": 1024}`,
		},
		{
			key: "struct.limit_col_must_have_default", ruleType: "列", level: "suggest", sort: 14,
			name: "限制列必须有默认值",
			desc: "除 TEXT/NTEXT/IMAGE/XML/VARBINARY(MAX)/VARCHAR(MAX)/NVARCHAR(MAX) 外，列需有默认值",
		},
		{
			key: "struct.limit_col_not_null", ruleType: "列", level: "suggest", sort: 15,
			name: "限制列不可空（NOT NULL）",
			desc: "除 TEXT/NTEXT/IMAGE/XML/(MAX) 系列外，列不可空（NOT NULL）",
		},
		{
			key: "struct.limit_col_no_partial_data_type", ruleType: "列", level: "suggest", sort: 16,
			name:   "限制列不能使用指定的数据类型",
			desc:   "建表或改表时禁止使用指定数据类型（TEXT/NTEXT/IMAGE 已废弃，MONEY/SQL_VARIANT/TIMESTAMP 语义易误用）",
			params: `{"types": ["text", "ntext", "image", "sql_variant", "timestamp", "money", "smallmoney"]}`,
		},
		{
			key: "struct.no_column_collate", ruleType: "列", level: "suggest", sort: 19,
			name: "限制列不能设置校对集",
			desc: "不能显式设置列级 COLLATE（统一跟随库默认排序规则，避免同表列排序规则混用）",
		},

		// ---------- 结构：表 ----------
		{
			key: "struct.limit_table_column_count", ruleType: "表", level: "must", sort: 7,
			name: "限制表字段数量上限",
			desc: "限制表字段的数量（SQL Server 硬上限 1024 列）",
		},
		{
			// 与列注释规则同口径：基线 must、默认 suggest，理由见上方 limit_col_must_have_comment
			key: "struct.limit_tbl_must_comment", ruleType: "表", level: "suggest", base: "must", sort: 8,
			name: "限制表必须有注释",
			desc: "表备注便于使用者快速了解业务（需 sp_addextendedproperty 扩展属性）",
		},
		{
			key: "struct.table_must_have_pk", ruleType: "表", level: "must", sort: 9,
			name: "表必须有主键",
			desc: "表要有主键（否则退化为堆表，更新删除与复制链路性能显著劣化）",
		},
		{
			key: "struct.merge_multiple_alter_stmt_same_table", ruleType: "表", level: "suggest", sort: 36,
			name: "同一张表的多条 ALTER 语句建议合为一条",
			desc: "建议将同一张表的多条 DDL 合并，避免多次表锁与重复扫描",
		},
		{
			key: "struct.limit_tbl_name_case", ruleType: "表", level: "suggest", sort: 40,
			name: "限制表名的大小写",
			desc: "限制表名大小写（SQL Server 默认排序规则下标识符大小写不敏感，此处约束命名风格）",
		},
		{
			key: "struct.tbl_name_no_keyword", ruleType: "表", level: "suggest", sort: 41,
			name: "限制表名不能是关键字",
			desc: "表名不能是关键字（T-SQL 保留字）",
		},
		{
			key: "struct.limit_tbl_no_use_fk", ruleType: "表", level: "suggest", sort: 42,
			name: "限制表不能使用外键",
			desc: "表不能使用外键",
		},
		{
			key: "struct.table_must_include_required_columns", ruleType: "表", level: "suggest", sort: 43,
			name: "限制表必须包含指定列",
			desc: "表需要包含某些列",
		},
		{
			key: "struct.create_tbl_must_not_exist", ruleType: "表", level: "must", sort: 155,
			name: "限制建表的目标表不能已存在",
			desc: "目标表已存在时 CREATE TABLE 执行必然失败（错误号 2714）",
		},
		{
			key: "struct.obj_tbl_must_exist", ruleType: "表", level: "must", sort: 156,
			name: "限制操作的目标表必须存在",
			desc: "表不存在时 ALTER/DROP/TRUNCATE 执行必然失败（错误号 208）",
		},
		{
			key: "struct.obj_tbl_must_not_exist", ruleType: "表", level: "must", sort: 161,
			name: "限制改名的目标表名不能已存在",
			desc: "sp_rename 的新表名已存在会产生冲突，执行必然失败",
		},

		// ---------- 结构：列对象 ----------
		{
			key: "struct.obj_col_must_exist", ruleType: "列", level: "must", sort: 157,
			name: "限制操作的目标列必须存在",
			desc: "列不存在时 ALTER COLUMN / DROP COLUMN 执行必然失败（错误号 4924）",
		},
		{
			key: "struct.obj_col_must_not_exist", ruleType: "列", level: "must", sort: 159,
			name: "限制改名的目标列名不能已存在",
			desc: "sp_rename 的新列名已存在会产生列冲突，执行必然失败",
		},

		// ---------- 结构：索引 ----------
		{
			key: "struct.limit_uniq_idx_name_fmt", ruleType: "索引", level: "must", sort: 12,
			name: "限制唯一索引命名格式",
			desc: "限制唯一索引名格式",
		},
		{
			key: "struct.index_name_no_duplicate", ruleType: "索引", level: "suggest", sort: 27,
			name: "限制索引名称不能重复",
			desc: "单表内索引（普通/唯一/外键）名称不允许重复",
		},
		{
			key: "struct.limit_tbl_idx_count", ruleType: "索引", level: "suggest", sort: 28,
			name: "限制表的索引数量",
			desc: "表上允许创建的最大索引数量",
		},
		{
			key: "struct.index_must_have_name", ruleType: "索引", level: "suggest", sort: 29,
			name: "限制索引必须设置名称",
			desc: "限制索引要有名称",
		},
		{
			key: "struct.limit_normal_idx_name_fmt", ruleType: "索引", level: "suggest", sort: 31,
			name: "限制普通索引命名格式",
			desc: "限制普通索引名格式",
		},
		{
			key: "struct.limit_pk_col_must_auto_inc", ruleType: "索引", level: "suggest", sort: 32,
			name: "限制主键列必须自增",
			desc: "限制主键列必须自增（IDENTITY 列）",
		},
		{
			key: "struct.limit_pk_column_count", ruleType: "索引", level: "suggest", sort: 33,
			name: "限制主键包含的列数",
			desc: "限制主键包含列的个数",
		},
		{
			key: "struct.limit_pk_col_type", ruleType: "索引", level: "suggest", sort: 34,
			name: "限制主键列的数据类型",
			desc: "限制主键列类型",
		},
		{
			key: "struct.limit_single_index_column_count", ruleType: "索引", level: "suggest", sort: 35,
			name: "限制单个索引包含的列数",
			desc: "限制单个索引包含列的个数（键列，不含 INCLUDE 列）",
		},
		{
			key: "struct.limit_tbl_redundant_idx", ruleType: "索引", level: "suggest", sort: 37,
			name: "限制冗余索引",
			desc: "不允许创建冗余索引（同列组合重复或已被最左前缀覆盖）",
		},
		{
			key: "struct.obj_idx_must_exist", ruleType: "索引", level: "must", sort: 158,
			name: "限制操作的目标索引必须存在",
			desc: "索引不存在时 DROP INDEX 执行必然失败（错误号 3701）",
		},
		{
			key: "struct.obj_idx_must_not_exist", ruleType: "索引", level: "must", sort: 160,
			name: "限制改名的目标索引名不能已存在",
			desc: "sp_rename 的新索引名已存在会产生冲突，执行必然失败",
		},

		// ---------- 结构：数据库对象 ----------
		{
			key: "struct.disable_custom_function_related_op", ruleType: "对象", level: "suggest", sort: 22,
			name: "禁止自定义函数相关操作",
			desc: "禁止 CREATE/ALTER/DROP FUNCTION 变更（含标量函数与内联表值函数）",
		},
		{
			key: "struct.disable_proc_related_op", ruleType: "对象", level: "suggest", sort: 23,
			name: "禁止存储过程相关操作",
			desc: "禁止 CREATE/ALTER/DROP PROCEDURE 变更",
		},
		{
			key: "struct.disable_trigger_related_op", ruleType: "对象", level: "suggest", sort: 24,
			name: "禁止触发器相关操作",
			desc: "禁止 CREATE/ALTER/DROP TRIGGER 变更",
		},
		{
			key: "struct.disable_view_related_op", ruleType: "对象", level: "suggest", sort: 25,
			name: "禁止视图相关操作",
			desc: "禁止视图的创建、更新、删除等变更",
		},
		{
			key: "struct.disable_create_table_as", ruleType: "对象", level: "suggest", sort: 25,
			name: "禁止 CREATE TABLE ... AS SELECT",
			desc: "禁止使用 CTAS 语法建表（T-SQL 写法为 SELECT ... INTO）：表结构来自 SELECT，字段/索引/约束等建表规范无法逐项校验",
		},
		{
			key: "struct.obj_view_must_not_exist", ruleType: "对象", level: "must", sort: 162,
			name: "限制创建视图的目标视图不能已存在",
			desc: "视图已存在时 CREATE VIEW 执行必然失败，建议改用 CREATE OR ALTER VIEW",
		},

		// ---------- 查询 / 更新：UPDATE · DELETE ----------
		{
			key: "query_update.limit_update_delete_affected_rows", ruleType: "更新", level: "must", sort: 44,
			name:   "限制 UPDATE/DELETE 单语句影响行数",
			desc:   "大批量更新/删除会暴涨事务日志、长时间锁大量数据，且 SQL Server 锁升级后回滚代价极高",
			params: `{"max_affected_rows": 10000}`,
		},
		{
			key: "query_update.delete_stmt_check_table_exists", ruleType: "删除", level: "suggest", sort: 45,
			name: "DELETE 语句检测表是否存在",
			desc: "检查 DELETE 涉及的表是否存在",
		},
		{
			key: "query_update.limit_update_delete_join_table_count", ruleType: "更新", level: "suggest", sort: 46,
			name:   "限制 UPDATE/DELETE 语句关联表的数量",
			desc:   "多表关联易致性能下降与锁竞争（T-SQL 的关联表写在 FROM 子句里）",
			params: `{"max_join_tables": 5}`,
		},
		{
			key: "query_update.update_delete_stmt_join_on_check", ruleType: "更新", level: "suggest", sort: 47,
			name: "UPDATE/DELETE 语句检测多表关联语法是否完整（JOIN 遗漏 ON 子句）",
			desc: "缺少 ON 子句会产生笛卡尔积，可能误更新/删除大量数据",
		},
		{
			key: "query_update.limit_update_delete_data_size", ruleType: "更新", level: "suggest", sort: 48,
			name:   "限制 UPDATE/DELETE 语句 LIMIT 的行数",
			desc:   "限制单次数据量可缩短锁持有时间（T-SQL 无 LIMIT，等价写法为 TOP (n)）",
			params: `{"max_limit_rows": 5}`,
		},
		{
			key: "query_update.update_delete_stmt_suggest_where", ruleType: "更新", level: "suggest", sort: 49,
			name: "UPDATE/DELETE 语句建议指定 WHERE 条件",
			desc: "缺少 WHERE 会全表更新/删除，风险高；WHERE 可走索引减少数据量",
		},
		{
			key: "query_update.update_delete_stmt_must_use_idx", ruleType: "更新", level: "suggest", sort: 50,
			name: "UPDATE/DELETE 语句必须使用索引",
			desc: "WHERE 条件必须命中已有索引的首列，否则表扫描 + 锁升级",
		},
		{
			key: "query_update.update_delete_stmt_no_order_by_clause", ruleType: "更新", level: "suggest", sort: 51,
			name: "UPDATE/DELETE 语句不能有 ORDER BY 子句",
			desc: "排序对 UPDATE/DELETE 无意义（T-SQL 里 ORDER BY 只允许配合 TOP 使用）",
		},
		{
			key: "query_update.update_delete_stmt_where_subquery_check", ruleType: "更新", level: "suggest", sort: 82,
			name: "UPDATE/DELETE 语句检测 WHERE 条件是否包含子查询",
			desc: "子查询执行计划复杂、开销大，且拉长锁持有时间",
		},
		{
			key: "query_update.update_multi_table_column_prefix_check", ruleType: "更新", level: "suggest", sort: 75,
			name: "UPDATE 多表时检测 SET 的列是否指定表前缀",
			desc: "多表存在同名字段时，不指定表前缀易产生歧义",
		},
		{
			key: "query_update.update_stmt_avoid_update_create_time_column", ruleType: "更新", level: "suggest", sort: 76,
			name:   "UPDATE 语句不建议更新表上的“创建时间”列",
			desc:   "创建时间属数据元信息，更新会破坏审计追溯",
			params: `{"create_time_columns": "create_time"}`,
		},
		{
			key: "query_update.update_stmt_check_updates_pk", ruleType: "更新", level: "suggest", sort: 77,
			name: "UPDATE 语句检测是否更新了主键",
			desc: "更新主键会触发聚集索引重建并可能引发级联更新，影响性能",
		},
		{
			key: "query_update.update_stmt_suggest_update_time_column", ruleType: "更新", level: "suggest", sort: 78,
			name:   "UPDATE 语句建议更新表上的“修改时间”列",
			desc:   "修改时间便于审计、变更追踪与增量同步",
			params: `{"update_time_columns": "update_time"}`,
		},
		{
			key: "query_update.update_stmt_set_separator_check", ruleType: "更新", level: "suggest", sort: 79,
			name: "UPDATE 语句检测 SET 多个列之间的分隔符（AND 非法）",
			desc: "AND 是逻辑运算符，不能用作 SET 分隔符，否则语法异常",
		},
		{
			key: "query_update.update_stmt_check_column_exists", ruleType: "更新", level: "suggest", sort: 80,
			name: "UPDATE 语句检测表/字段是否存在",
			desc: "UPDATE 语句的目标表、字段不存在时执行必然失败（错误号 208 / 207）",
		},
		{
			key: "query_update.update_stmt_check_updates_unique_key", ruleType: "更新", level: "suggest", sort: 81,
			name: "UPDATE 语句检测是否更新了唯一键",
			desc: "更新唯一键会破坏外键关联与业务逻辑，易致数据不一致",
		},

		// ---------- 查询 / 更新：INSERT ----------
		{
			key: "query_update.insert_stmt_col_name_no_dup", ruleType: "写入", level: "suggest", sort: 53,
			name: "INSERT 语句字段名不能重复",
			desc: "字段名重复会导致歧义并使 SQL 执行失败（错误号 264）",
		},
		{
			key: "query_update.insert_stmt_columns_match_values", ruleType: "写入", level: "suggest", sort: 54,
			name: "INSERT 语句字段列表与值列表要匹配",
			desc: "字段与值数量不一致会直接报错（错误号 213），显式列表时逐行比对，无列表时与表结构比对",
		},
		{
			key: "query_update.insert_stmt_specify_columns", ruleType: "写入", level: "suggest", sort: 55,
			name: "INSERT 语句建议指定字段列表",
			desc: "显式指定字段列表更清晰，且表结构变更时语句不易失效",
		},
		{
			key: "query_update.insert_stmt_not_null_column_null_value", ruleType: "写入", level: "suggest", sort: 56,
			name: "INSERT 语句不能为 NOT NULL 列插入 NULL 值",
			desc: "NOT NULL 列必须给值，否则执行失败并易破坏数据完整性（错误号 515）",
		},
		{
			key: "query_update.insert_stmt_avoid_sysdate_func", ruleType: "写入", level: "suggest", sort: 57,
			name: "INSERT 语句不建议使用 SYSDATE() 函数",
			desc: "INSERT 显式写入 GETDATE()/SYSDATETIME() 等易变时间函数，会让数据值随执行时刻漂移、不便复现与比对",
		},
		{
			key: "query_update.limit_insert_values_row_count", ruleType: "写入", level: "suggest", sort: 58,
			name:   "限制 INSERT 语句单条 VALUES 的行数",
			desc:   "大批量插入会暴涨事务日志、占用大量内存并加剧锁竞争（T-SQL 单条 VALUES 上限 1000 行）",
			params: `{"max_insert_rows": 1000}`,
		},
		{
			key: "query_update.insert_stmt_check_column_exists", ruleType: "写入", level: "suggest", sort: 59,
			name: "INSERT 语句检测目标表/字段是否存在",
			desc: "目标表或字段不存在时执行必然失败（错误号 208 / 207），提交前必须拦截",
		},

		// ---------- 查询：WHERE 条件 ----------
		{
			key: "query_update.limit_where_in_element_count", ruleType: "查询", level: "suggest", sort: 83,
			name:   "限制 WHERE 条件中 IN 子句的元素个数",
			desc:   "IN 元素过多难以命中索引，易触发全表扫描",
			params: `{"max_in_elements": 1000}`,
		},
		{
			key: "query_update.where_avoid_leading_wildcard", ruleType: "查询", level: "suggest", sort: 84,
			name: "WHERE 条件中不建议使用前通配符查找",
			desc: "前置 % 无法走前缀索引，必然全表扫描",
		},
		{
			key: "query_update.where_avoid_index_col_arithmetic_or_func", ruleType: "查询", level: "suggest", sort: 85,
			name: "WHERE 条件中不建议索引字段包含数学运算或函数运算",
			desc: "索引字段做函数/运算会失效，触发全表扫描",
		},
		{
			key: "query_update.where_avoid_not_in_not_like", ruleType: "查询", level: "suggest", sort: 86,
			name: "WHERE 条件中不建议使用反向查询（NOT IN/NOT LIKE）",
			desc: "NOT 类反向条件难命中索引，多为全表扫描",
		},
		{
			key: "query_update.where_or_condition_check", ruleType: "查询", level: "suggest", sort: 87,
			name: "WHERE 条件中检测是否使用 OR 连接过滤条件",
			desc: "OR 易致索引失效走全表扫描，条件恒真还有误操作风险",
		},
		{
			key: "query_update.where_implicit_type_cast_check", ruleType: "查询", level: "suggest", sort: 88,
			name: "WHERE 条件中检测字段是否存在隐式类型转换",
			desc: "隐式类型转换（如 VARCHAR 列与数值/日期比较）易致索引失效全表扫描，还可能比对出错",
		},
		{
			key: "query_update.where_like_without_wildcard", ruleType: "查询", level: "suggest", sort: 89,
			name: "WHERE 条件中检测 LIKE 语句是否缺少通配符",
			desc: "无通配符的 LIKE 等价于等值查询，用 = 更清晰且更易走索引",
		},
	}

	out := make([]models.InsightSQLStandardRule, 0, len(seeds))
	for _, s := range seeds {
		category := "structure"
		if strings.HasPrefix(s.key, "query_update.") {
			category = "query_update"
		}
		params := s.params
		if strings.TrimSpace(params) == "" {
			params = `{}`
		}
		// 基线级别默认与默认级别同源；个别规则需与 MySQL 目录基线对齐但默认放宽时，
		// 显式指定 base（level 可被运行期编辑，base 是目录原始值，不允许被编辑覆盖）
		baseLevel := s.level
		if strings.TrimSpace(s.base) != "" {
			baseLevel = s.base
		}
		out = append(out, models.InsightSQLStandardRule{
			RuleKey:     "sqlserver." + s.key,
			Category:    category,
			RuleType:    s.ruleType,
			DbType:      "sqlserver",
			Name:        s.name,
			Description: s.desc,
			BaseLevel:   baseLevel,
			Level:       s.level,
			Params:      []byte(params),
			SortOrder:   s.sort,
			Implemented: true,
		})
	}
	return out
}

// sqlserverSeedRule 规则目录的紧凑写法：Category / DbType / Implemented 由构造逻辑补齐，
// Level 与 BaseLevel 同源（种子目录的默认级别就是基线级别）。
type sqlserverSeedRule struct {
	key      string // 去掉 "sqlserver." 前缀后的后缀：struct.xxx / query_update.xxx
	ruleType string // 规则类型：表/列/索引/库/对象/更新/写入/删除/查询
	level    string // must / suggest：种子的默认级别
	base     string // must / suggest：目录基线级别；空表示与 level 同源（基线需与 MySQL 对齐时显式指定）
	sort     int
	name     string
	desc     string
	params   string // 规则参数 JSON；空表示 `{}`
}
