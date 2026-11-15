-- =============================================================================
-- 补种「SQLServer SQL 规范规则」（存量规范专用，共 72 条 = 结构 42 + DML 30）
--
-- 背景：
--   SQLServer 工单此前完全没有 SQL 规范可配：检查引擎 Run() 只分发 MySQL/PG/Oracle，
--   规范目录里也只有 mysql/tidb/postgres/oracle 四套规则。本次补齐后：
--     · 引擎（checker/sqlserver_parse.go / sqlserver_rules.go / sqlserver_dml.go /
--       sqlserver_gate.go / sqlserver_object_rules.go）按「语法感知拆分 + T-SQL 文本解析」
--       实现判定，连库规则走 sys.objects / sys.columns / sys.indexes / sys.extended_properties
--       （github.com/go-mssqldb 驱动）；
--     · 标准播种逻辑（InitializeDefaultSQLStandards）对已有规范是「有规则就跳过」，
--       存量环境必须跑本脚本，规则才会出现在规范页面并可配置级别。
--
-- 为什么必须整目录补种（而不是只补缺失的）：
--   SQLServer 引擎产出的每个 rule_key 都必须在规范里有对应规则，否则预审结论会走
--   「致命类别兜底」，被挂到同致命类别里任意一条规则上（PG/Oracle 轮实测踩坑）。
--   services/standard_seed_sqlserver_test.go 已把「引擎产出键 == 种子目录键」锁成红线。
--
-- 未收录的 10 条 MySQL 专属能力项（SQLServer 上不存在对应语义，收录即幽灵规则）：
--   limit_auto_inc_col_is_unsigned / limit_table_storage_engine / limit_database_charset /
--   no_column_charset / limit_tbl_charset / limit_tbl_collation /
--   tbl_charset_change_use_convert_syntax / column_zerofill_forbidden /
--   disable_event_related_op / disable_create_table_like
--   （SQLServer 没有 unsigned / zerofill / 存储引擎 / 字符集，只有排序规则 collation；
--    「列字符集」由 no_column_collate 覆盖，「不支持 CREATE TABLE ... LIKE」不适用）
--
-- SQLServer 特有口径：
--   · 标识符默认不区分大小写（取决于排序规则）⇒ 大小写两条规则默认 must 但按 lower/upper
--     参数判定，规范里可配 name_case；
--   · 表/列注释没有内联 COMMENT 语法，唯一载体是扩展属性，需同工单内提供
--     EXEC sp_addextendedproperty 'MS_Description', ... 语句；
--   · 自增列是 IDENTITY(seed, increment)，自增初值对应 IDENTITY 的 seed；
--   · 没有 UPDATE/DELETE 的 LIMIT/ORDER BY 语法，限量语法只有 TOP (n)；
--   · 只有排序规则（collation），没有列级字符集；
--   · 存储过程/自定义函数/触发器默认允许，由三条 disable_*_related_op 规则独立控制。
--
-- 安全：
--   STEP 1 只读预览；STEP 2 为写入，整段可重复执行（NOT EXISTS 幂等）。
-- =============================================================================

-- ========================== STEP 1：只读预览 ==========================

-- 统一客户端字符集，避免用 latin1 默认的客户端执行时中文规则名/描述被写坏
SET NAMES utf8mb4;

-- 1.1 当前规范列表
SELECT id, name, env_type, enabled FROM insight_sql_standards ORDER BY id;

-- 1.2 每个规范缺失多少条 SQLServer 规则（补种前）
SELECT s.id AS standard_id, s.name AS standard_name, COUNT(*) AS missing_count
FROM insight_sql_standards s
JOIN (
  SELECT 'sqlserver.struct.limit_auto_inc_col_name_is_id' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_create_table_auto_inc_init' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_char_type_col_len' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_col_name_case' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.col_name_no_keyword' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_col_must_have_comment' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_varchar_type_col_len' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_col_must_have_default' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_col_not_null' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_col_no_partial_data_type' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.no_column_collate' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_table_column_count' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_tbl_must_comment' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.table_must_have_pk' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.merge_multiple_alter_stmt_same_table' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_tbl_name_case' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.tbl_name_no_keyword' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_tbl_no_use_fk' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.table_must_include_required_columns' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.create_tbl_must_not_exist' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.obj_tbl_must_exist' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.obj_tbl_must_not_exist' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.obj_col_must_exist' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.obj_col_must_not_exist' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_uniq_idx_name_fmt' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.index_name_no_duplicate' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_tbl_idx_count' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.index_must_have_name' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_normal_idx_name_fmt' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_pk_col_must_auto_inc' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_pk_column_count' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_pk_col_type' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_single_index_column_count' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.limit_tbl_redundant_idx' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.obj_idx_must_exist' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.obj_idx_must_not_exist' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.disable_custom_function_related_op' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.disable_proc_related_op' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.disable_trigger_related_op' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.disable_view_related_op' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.disable_create_table_as' AS rule_key
  UNION ALL SELECT 'sqlserver.struct.obj_view_must_not_exist' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.limit_update_delete_affected_rows' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.delete_stmt_check_table_exists' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.limit_update_delete_join_table_count' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.update_delete_stmt_join_on_check' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.limit_update_delete_data_size' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.update_delete_stmt_suggest_where' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.update_delete_stmt_must_use_idx' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.update_delete_stmt_no_order_by_clause' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.update_delete_stmt_where_subquery_check' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.update_multi_table_column_prefix_check' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.update_stmt_avoid_update_create_time_column' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.update_stmt_check_updates_pk' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.update_stmt_suggest_update_time_column' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.update_stmt_set_separator_check' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.update_stmt_check_column_exists' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.update_stmt_check_updates_unique_key' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.insert_stmt_col_name_no_dup' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.insert_stmt_columns_match_values' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.insert_stmt_specify_columns' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.insert_stmt_not_null_column_null_value' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.insert_stmt_avoid_sysdate_func' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.limit_insert_values_row_count' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.insert_stmt_check_column_exists' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.limit_where_in_element_count' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.where_avoid_leading_wildcard' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.where_avoid_index_col_arithmetic_or_func' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.where_avoid_not_in_not_like' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.where_or_condition_check' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.where_implicit_type_cast_check' AS rule_key
  UNION ALL SELECT 'sqlserver.query_update.where_like_without_wildcard' AS rule_key
) r ON 1 = 1
LEFT JOIN insight_sql_standard_rules x
  ON x.standard_id = s.id AND x.rule_key = r.rule_key
WHERE x.id IS NULL
GROUP BY s.id, s.name
ORDER BY s.id;

-- ========================== STEP 2：执行补种 ==========================
-- 执行前先备份（幂等，重复执行不会重复插入）：
CREATE TABLE IF NOT EXISTS insight_sql_standard_rules_bak_20261108
  AS SELECT * FROM insight_sql_standard_rules;

INSERT INTO insight_sql_standard_rules
  (created_at, updated_at, standard_id, rule_key, category, db_type, name, description,
   rule_type, level, base_level, params, sort_order, implemented)
SELECT
  NOW(3), NOW(3), s.id, r.rule_key, r.category, 'sqlserver', r.name, r.description,
  r.rule_type, r.level,
  -- base_level 与 MySQL 规则目录基线对齐（注释类规则目录基线是 must，默认级别放宽为 suggest）；
  -- base_level 是目录原始值，不允许被规范页的运行期编辑覆盖
  CASE WHEN r.rule_key IN ('sqlserver.struct.limit_col_must_have_comment',
                           'sqlserver.struct.limit_tbl_must_comment')
       THEN 'must' ELSE r.level END,
  r.params, r.sort_order, 1
FROM insight_sql_standards s
CROSS JOIN (
  -- ---------- 结构：DDL（42 条） ----------
  -- 注意：UNION ALL 派生表的列名只取自【第一条 SELECT】的别名，
  -- 这里必须显式 AS，否则外层 r.rule_key / r.category / ... 全部无法解析。
  -- 原因：MySQL 把首行裸字面量当列名，会直接报 Error 1054 Unknown column 'r.rule_key'。
  SELECT 'sqlserver.struct.limit_auto_inc_col_name_is_id' AS rule_key,
         'structure'    AS category,
         '限制自增列名为 ID' AS name,
         '限制自增列名（IDENTITY 列）为 ID' AS description,
         '列'           AS rule_type,
         'must'         AS level,
         '{}'           AS params,
         0              AS sort_order
  UNION ALL SELECT 'sqlserver.struct.limit_create_table_auto_inc_init', 'structure', '限制建表自增列初始值',
         '限制建表自增初始值（IDENTITY(seed, increment) 的 seed）', '表', 'must', '{}', 2
  UNION ALL SELECT 'sqlserver.struct.limit_char_type_col_len', 'structure', '限制 CHAR 类型字段的长度',
         '限制 CHAR / NCHAR 类型字段长度（长度按字符计，非字节）', '列', 'must', '{"max_length": 64}', 3
  UNION ALL SELECT 'sqlserver.struct.limit_col_name_case', 'structure', '限制字段名的大小写',
         '限制字段名大小写（SQL Server 默认排序规则下标识符大小写不敏感，此处约束命名风格）', '列', 'must', '{}', 4
  UNION ALL SELECT 'sqlserver.struct.col_name_no_keyword', 'structure', '限制字段名不能是关键字',
         '字段名不能是关键字（T-SQL 保留字）', '列', 'must', '{}', 5
  UNION ALL SELECT 'sqlserver.struct.limit_col_must_have_comment', 'structure', '限制列必须有注释',
         '列要有注释（SQL Server 无内联 COMMENT 语法，需 sp_addextendedproperty 扩展属性）', '列', 'suggest', '{}', 6
  UNION ALL SELECT 'sqlserver.struct.limit_varchar_type_col_len', 'structure', '限制 VARCHAR 类型字段的长度',
         '限制 VARCHAR / NVARCHAR 类型字段长度（MAX 视为超限）', '列', 'must', '{"max_length": 1024}', 13
  UNION ALL SELECT 'sqlserver.struct.limit_col_must_have_default', 'structure', '限制列必须有默认值',
         '除 TEXT/NTEXT/IMAGE/XML/VARBINARY(MAX)/VARCHAR(MAX)/NVARCHAR(MAX) 外，列需有默认值', '列', 'suggest', '{}', 14
  UNION ALL SELECT 'sqlserver.struct.limit_col_not_null', 'structure', '限制列不可空（NOT NULL）',
         '除 TEXT/NTEXT/IMAGE/XML/(MAX) 系列外，列不可空（NOT NULL）', '列', 'suggest', '{}', 15
  UNION ALL SELECT 'sqlserver.struct.limit_col_no_partial_data_type', 'structure', '限制列不能使用指定的数据类型',
         '建表或改表时禁止使用指定数据类型（TEXT/NTEXT/IMAGE 已废弃，MONEY/SQL_VARIANT/TIMESTAMP 语义易误用）', '列', 'suggest', '{"types": ["text", "ntext", "image", "sql_variant", "timestamp", "money", "smallmoney"]}', 16
  UNION ALL SELECT 'sqlserver.struct.no_column_collate', 'structure', '限制列不能设置校对集',
         '不能显式设置列级 COLLATE（统一跟随库默认排序规则，避免同表列排序规则混用）', '列', 'suggest', '{}', 19
  UNION ALL SELECT 'sqlserver.struct.limit_table_column_count', 'structure', '限制表字段数量上限',
         '限制表字段的数量（SQL Server 硬上限 1024 列）', '表', 'must', '{}', 7
  UNION ALL SELECT 'sqlserver.struct.limit_tbl_must_comment', 'structure', '限制表必须有注释',
         '表备注便于使用者快速了解业务（需 sp_addextendedproperty 扩展属性）', '表', 'suggest', '{}', 8
  UNION ALL SELECT 'sqlserver.struct.table_must_have_pk', 'structure', '表必须有主键',
         '表要有主键（否则退化为堆表，更新删除与复制链路性能显著劣化）', '表', 'must', '{}', 9
  UNION ALL SELECT 'sqlserver.struct.merge_multiple_alter_stmt_same_table', 'structure', '同一张表的多条 ALTER 语句建议合为一条',
         '建议将同一张表的多条 DDL 合并，避免多次表锁与重复扫描', '表', 'suggest', '{}', 36
  UNION ALL SELECT 'sqlserver.struct.limit_tbl_name_case', 'structure', '限制表名的大小写',
         '限制表名大小写（SQL Server 默认排序规则下标识符大小写不敏感，此处约束命名风格）', '表', 'suggest', '{}', 40
  UNION ALL SELECT 'sqlserver.struct.tbl_name_no_keyword', 'structure', '限制表名不能是关键字',
         '表名不能是关键字（T-SQL 保留字）', '表', 'suggest', '{}', 41
  UNION ALL SELECT 'sqlserver.struct.limit_tbl_no_use_fk', 'structure', '限制表不能使用外键',
         '表不能使用外键', '表', 'suggest', '{}', 42
  UNION ALL SELECT 'sqlserver.struct.table_must_include_required_columns', 'structure', '限制表必须包含指定列',
         '表需要包含某些列', '表', 'suggest', '{}', 43
  UNION ALL SELECT 'sqlserver.struct.create_tbl_must_not_exist', 'structure', '限制建表的目标表不能已存在',
         '目标表已存在时 CREATE TABLE 执行必然失败（错误号 2714）', '表', 'must', '{}', 155
  UNION ALL SELECT 'sqlserver.struct.obj_tbl_must_exist', 'structure', '限制操作的目标表必须存在',
         '表不存在时 ALTER/DROP/TRUNCATE 执行必然失败（错误号 208）', '表', 'must', '{}', 156
  UNION ALL SELECT 'sqlserver.struct.obj_tbl_must_not_exist', 'structure', '限制改名的目标表名不能已存在',
         'sp_rename 的新表名已存在会产生冲突，执行必然失败', '表', 'must', '{}', 161
  UNION ALL SELECT 'sqlserver.struct.obj_col_must_exist', 'structure', '限制操作的目标列必须存在',
         '列不存在时 ALTER COLUMN / DROP COLUMN 执行必然失败（错误号 4924）', '列', 'must', '{}', 157
  UNION ALL SELECT 'sqlserver.struct.obj_col_must_not_exist', 'structure', '限制改名的目标列名不能已存在',
         'sp_rename 的新列名已存在会产生列冲突，执行必然失败', '列', 'must', '{}', 159
  UNION ALL SELECT 'sqlserver.struct.limit_uniq_idx_name_fmt', 'structure', '限制唯一索引命名格式',
         '限制唯一索引名格式', '索引', 'must', '{}', 12
  UNION ALL SELECT 'sqlserver.struct.index_name_no_duplicate', 'structure', '限制索引名称不能重复',
         '单表内索引（普通/唯一/外键）名称不允许重复', '索引', 'suggest', '{}', 27
  UNION ALL SELECT 'sqlserver.struct.limit_tbl_idx_count', 'structure', '限制表的索引数量',
         '表上允许创建的最大索引数量', '索引', 'suggest', '{}', 28
  UNION ALL SELECT 'sqlserver.struct.index_must_have_name', 'structure', '限制索引必须设置名称',
         '限制索引要有名称', '索引', 'suggest', '{}', 29
  UNION ALL SELECT 'sqlserver.struct.limit_normal_idx_name_fmt', 'structure', '限制普通索引命名格式',
         '限制普通索引名格式', '索引', 'suggest', '{}', 31
  UNION ALL SELECT 'sqlserver.struct.limit_pk_col_must_auto_inc', 'structure', '限制主键列必须自增',
         '限制主键列必须自增（IDENTITY 列）', '索引', 'suggest', '{}', 32
  UNION ALL SELECT 'sqlserver.struct.limit_pk_column_count', 'structure', '限制主键包含的列数',
         '限制主键包含列的个数', '索引', 'suggest', '{}', 33
  UNION ALL SELECT 'sqlserver.struct.limit_pk_col_type', 'structure', '限制主键列的数据类型',
         '限制主键列类型', '索引', 'suggest', '{}', 34
  UNION ALL SELECT 'sqlserver.struct.limit_single_index_column_count', 'structure', '限制单个索引包含的列数',
         '限制单个索引包含列的个数（键列，不含 INCLUDE 列）', '索引', 'suggest', '{}', 35
  UNION ALL SELECT 'sqlserver.struct.limit_tbl_redundant_idx', 'structure', '限制冗余索引',
         '不允许创建冗余索引（同列组合重复或已被最左前缀覆盖）', '索引', 'suggest', '{}', 37
  UNION ALL SELECT 'sqlserver.struct.obj_idx_must_exist', 'structure', '限制操作的目标索引必须存在',
         '索引不存在时 DROP INDEX 执行必然失败（错误号 3701）', '索引', 'must', '{}', 158
  UNION ALL SELECT 'sqlserver.struct.obj_idx_must_not_exist', 'structure', '限制改名的目标索引名不能已存在',
         'sp_rename 的新索引名已存在会产生冲突，执行必然失败', '索引', 'must', '{}', 160
  UNION ALL SELECT 'sqlserver.struct.disable_custom_function_related_op', 'structure', '禁止自定义函数相关操作',
         '禁止 CREATE/ALTER/DROP FUNCTION 变更（含标量函数与内联表值函数）', '对象', 'suggest', '{}', 22
  UNION ALL SELECT 'sqlserver.struct.disable_proc_related_op', 'structure', '禁止存储过程相关操作',
         '禁止 CREATE/ALTER/DROP PROCEDURE 变更', '对象', 'suggest', '{}', 23
  UNION ALL SELECT 'sqlserver.struct.disable_trigger_related_op', 'structure', '禁止触发器相关操作',
         '禁止 CREATE/ALTER/DROP TRIGGER 变更', '对象', 'suggest', '{}', 24
  UNION ALL SELECT 'sqlserver.struct.disable_view_related_op', 'structure', '禁止视图相关操作',
         '禁止视图的创建、更新、删除等变更', '对象', 'suggest', '{}', 25
  UNION ALL SELECT 'sqlserver.struct.disable_create_table_as', 'structure', '禁止 CREATE TABLE ... AS SELECT',
         '禁止使用 CTAS 语法建表（T-SQL 写法为 SELECT ... INTO）：表结构来自 SELECT，字段/索引/约束等建表规范无法逐项校验', '对象', 'suggest', '{}', 25
  UNION ALL SELECT 'sqlserver.struct.obj_view_must_not_exist', 'structure', '限制创建视图的目标视图不能已存在',
         '视图已存在时 CREATE VIEW 执行必然失败，建议改用 CREATE OR ALTER VIEW', '对象', 'must', '{}', 162
  -- ---------- DML（30 条） ----------
  UNION ALL SELECT 'sqlserver.query_update.limit_update_delete_affected_rows', 'query_update', '限制 UPDATE/DELETE 单语句影响行数',
         '大批量更新/删除会暴涨事务日志、长时间锁大量数据，且 SQL Server 锁升级后回滚代价极高', '更新', 'must', '{"max_affected_rows": 10000}', 44
  UNION ALL SELECT 'sqlserver.query_update.delete_stmt_check_table_exists', 'query_update', 'DELETE 语句检测表是否存在',
         '检查 DELETE 涉及的表是否存在', '删除', 'suggest', '{}', 45
  UNION ALL SELECT 'sqlserver.query_update.limit_update_delete_join_table_count', 'query_update', '限制 UPDATE/DELETE 语句关联表的数量',
         '多表关联易致性能下降与锁竞争（T-SQL 的关联表写在 FROM 子句里）', '更新', 'suggest', '{"max_join_tables": 5}', 46
  UNION ALL SELECT 'sqlserver.query_update.update_delete_stmt_join_on_check', 'query_update', 'UPDATE/DELETE 语句检测多表关联语法是否完整（JOIN 遗漏 ON 子句）',
         '缺少 ON 子句会产生笛卡尔积，可能误更新/删除大量数据', '更新', 'suggest', '{}', 47
  UNION ALL SELECT 'sqlserver.query_update.limit_update_delete_data_size', 'query_update', '限制 UPDATE/DELETE 语句 LIMIT 的行数',
         '限制单次数据量可缩短锁持有时间（T-SQL 无 LIMIT，等价写法为 TOP (n)）', '更新', 'suggest', '{"max_limit_rows": 5}', 48
  UNION ALL SELECT 'sqlserver.query_update.update_delete_stmt_suggest_where', 'query_update', 'UPDATE/DELETE 语句建议指定 WHERE 条件',
         '缺少 WHERE 会全表更新/删除，风险高；WHERE 可走索引减少数据量', '更新', 'suggest', '{}', 49
  UNION ALL SELECT 'sqlserver.query_update.update_delete_stmt_must_use_idx', 'query_update', 'UPDATE/DELETE 语句必须使用索引',
         'WHERE 条件必须命中已有索引的首列，否则表扫描 + 锁升级', '更新', 'suggest', '{}', 50
  UNION ALL SELECT 'sqlserver.query_update.update_delete_stmt_no_order_by_clause', 'query_update', 'UPDATE/DELETE 语句不能有 ORDER BY 子句',
         '排序对 UPDATE/DELETE 无意义（T-SQL 里 ORDER BY 只允许配合 TOP 使用）', '更新', 'suggest', '{}', 51
  UNION ALL SELECT 'sqlserver.query_update.update_delete_stmt_where_subquery_check', 'query_update', 'UPDATE/DELETE 语句检测 WHERE 条件是否包含子查询',
         '子查询执行计划复杂、开销大，且拉长锁持有时间', '更新', 'suggest', '{}', 82
  UNION ALL SELECT 'sqlserver.query_update.update_multi_table_column_prefix_check', 'query_update', 'UPDATE 多表时检测 SET 的列是否指定表前缀',
         '多表存在同名字段时，不指定表前缀易产生歧义', '更新', 'suggest', '{}', 75
  UNION ALL SELECT 'sqlserver.query_update.update_stmt_avoid_update_create_time_column', 'query_update', 'UPDATE 语句不建议更新表上的“创建时间”列',
         '创建时间属数据元信息，更新会破坏审计追溯', '更新', 'suggest', '{"create_time_columns": "create_time"}', 76
  UNION ALL SELECT 'sqlserver.query_update.update_stmt_check_updates_pk', 'query_update', 'UPDATE 语句检测是否更新了主键',
         '更新主键会触发聚集索引重建并可能引发级联更新，影响性能', '更新', 'suggest', '{}', 77
  UNION ALL SELECT 'sqlserver.query_update.update_stmt_suggest_update_time_column', 'query_update', 'UPDATE 语句建议更新表上的“修改时间”列',
         '修改时间便于审计、变更追踪与增量同步', '更新', 'suggest', '{"update_time_columns": "update_time"}', 78
  UNION ALL SELECT 'sqlserver.query_update.update_stmt_set_separator_check', 'query_update', 'UPDATE 语句检测 SET 多个列之间的分隔符（AND 非法）',
         'AND 是逻辑运算符，不能用作 SET 分隔符，否则语法异常', '更新', 'suggest', '{}', 79
  UNION ALL SELECT 'sqlserver.query_update.update_stmt_check_column_exists', 'query_update', 'UPDATE 语句检测表/字段是否存在',
         'UPDATE 语句的目标表、字段不存在时执行必然失败（错误号 208 / 207）', '更新', 'suggest', '{}', 80
  UNION ALL SELECT 'sqlserver.query_update.update_stmt_check_updates_unique_key', 'query_update', 'UPDATE 语句检测是否更新了唯一键',
         '更新唯一键会破坏外键关联与业务逻辑，易致数据不一致', '更新', 'suggest', '{}', 81
  UNION ALL SELECT 'sqlserver.query_update.insert_stmt_col_name_no_dup', 'query_update', 'INSERT 语句字段名不能重复',
         '字段名重复会导致歧义并使 SQL 执行失败（错误号 264）', '写入', 'suggest', '{}', 53
  UNION ALL SELECT 'sqlserver.query_update.insert_stmt_columns_match_values', 'query_update', 'INSERT 语句字段列表与值列表要匹配',
         '字段与值数量不一致会直接报错（错误号 213），显式列表时逐行比对，无列表时与表结构比对', '写入', 'suggest', '{}', 54
  UNION ALL SELECT 'sqlserver.query_update.insert_stmt_specify_columns', 'query_update', 'INSERT 语句建议指定字段列表',
         '显式指定字段列表更清晰，且表结构变更时语句不易失效', '写入', 'suggest', '{}', 55
  UNION ALL SELECT 'sqlserver.query_update.insert_stmt_not_null_column_null_value', 'query_update', 'INSERT 语句不能为 NOT NULL 列插入 NULL 值',
         'NOT NULL 列必须给值，否则执行失败并易破坏数据完整性（错误号 515）', '写入', 'suggest', '{}', 56
  UNION ALL SELECT 'sqlserver.query_update.insert_stmt_avoid_sysdate_func', 'query_update', 'INSERT 语句不建议使用 SYSDATE() 函数',
         'INSERT 显式写入 GETDATE()/SYSDATETIME() 等易变时间函数，会让数据值随执行时刻漂移、不便复现与比对', '写入', 'suggest', '{}', 57
  UNION ALL SELECT 'sqlserver.query_update.limit_insert_values_row_count', 'query_update', '限制 INSERT 语句单条 VALUES 的行数',
         '大批量插入会暴涨事务日志、占用大量内存并加剧锁竞争（T-SQL 单条 VALUES 上限 1000 行）', '写入', 'suggest', '{"max_insert_rows": 1000}', 58
  UNION ALL SELECT 'sqlserver.query_update.insert_stmt_check_column_exists', 'query_update', 'INSERT 语句检测目标表/字段是否存在',
         '目标表或字段不存在时执行必然失败（错误号 208 / 207），提交前必须拦截', '写入', 'suggest', '{}', 59
  UNION ALL SELECT 'sqlserver.query_update.limit_where_in_element_count', 'query_update', '限制 WHERE 条件中 IN 子句的元素个数',
         'IN 元素过多难以命中索引，易触发全表扫描', '查询', 'suggest', '{"max_in_elements": 1000}', 83
  UNION ALL SELECT 'sqlserver.query_update.where_avoid_leading_wildcard', 'query_update', 'WHERE 条件中不建议使用前通配符查找',
         '前置 % 无法走前缀索引，必然全表扫描', '查询', 'suggest', '{}', 84
  UNION ALL SELECT 'sqlserver.query_update.where_avoid_index_col_arithmetic_or_func', 'query_update', 'WHERE 条件中不建议索引字段包含数学运算或函数运算',
         '索引字段做函数/运算会失效，触发全表扫描', '查询', 'suggest', '{}', 85
  UNION ALL SELECT 'sqlserver.query_update.where_avoid_not_in_not_like', 'query_update', 'WHERE 条件中不建议使用反向查询（NOT IN/NOT LIKE）',
         'NOT 类反向条件难命中索引，多为全表扫描', '查询', 'suggest', '{}', 86
  UNION ALL SELECT 'sqlserver.query_update.where_or_condition_check', 'query_update', 'WHERE 条件中检测是否使用 OR 连接过滤条件',
         'OR 易致索引失效走全表扫描，条件恒真还有误操作风险', '查询', 'suggest', '{}', 87
  UNION ALL SELECT 'sqlserver.query_update.where_implicit_type_cast_check', 'query_update', 'WHERE 条件中检测字段是否存在隐式类型转换',
         '隐式类型转换（如 VARCHAR 列与数值/日期比较）易致索引失效全表扫描，还可能比对出错', '查询', 'suggest', '{}', 88
  UNION ALL SELECT 'sqlserver.query_update.where_like_without_wildcard', 'query_update', 'WHERE 条件中检测 LIKE 语句是否缺少通配符',
         '无通配符的 LIKE 等价于等值查询，用 = 更清晰且更易走索引', '查询', 'suggest', '{}', 89
) r
WHERE NOT EXISTS (
  SELECT 1 FROM insight_sql_standard_rules x
  WHERE x.standard_id = s.id AND x.rule_key = r.rule_key
);

-- ========================== STEP 3：复核 ==========================

-- 3.1 补种后每个规范应各持有 72 条
SELECT standard_id, COUNT(*) AS rule_count
FROM insight_sql_standard_rules
WHERE rule_key LIKE 'sqlserver.%'
GROUP BY standard_id;

-- 3.2 明细核对（类别分布：structure 42 / query_update 30）
SELECT standard_id, category, COUNT(*) AS cnt
FROM insight_sql_standard_rules
WHERE rule_key LIKE 'sqlserver.%'
GROUP BY standard_id, category
ORDER BY standard_id, category;

-- 3.3 规则类型（rule_type）不允许为空——「规则类型」列的唯一权威来源是库列
SELECT id, rule_key, rule_type
FROM insight_sql_standard_rules
WHERE rule_key LIKE 'sqlserver.%' AND (rule_type IS NULL OR rule_type = '');

-- 3.4 级别分布（应与 MySQL 同能力项一致）
SELECT level, COUNT(*) AS cnt
FROM insight_sql_standard_rules
WHERE rule_key LIKE 'sqlserver.%'
GROUP BY level ORDER BY level;

-- 3.5 需要关闭某条规则时按需改级别：
-- UPDATE insight_sql_standard_rules SET level='off'
--  WHERE rule_key='sqlserver.struct.obj_idx_must_exist' AND standard_id=1;
