# SQLServer 数据库的 SQL 开发规范支持说明

## 文档目的

本文说明在**不改动既有四个数据库（MySQL / TiDB / PostgreSQL / Oracle）行为**的前提下，
为 SQLServer 数据库补齐 SQL 开发规范（规则目录 + 审核引擎 + 参数映射 + 前端展示）的范围与落点。

阅读顺序建议：先看「一、现有规范结构」，再看「二、SQLServer 规则范围」，
「四、需要调整或扩展的条目」是本次改动的完整清单。

---

## 一、现有规范的结构（改动前基线）

### 1.1 目录规模

| 数据库 | 规则条数 |
|---|---|
| MySQL | 82 |
| TiDB | 82 |
| PostgreSQL | 58 |
| Oracle | 57 |
| **合计** | **279** |

改动后合计 **351** 条（MySQL 82 / TiDB 82 / PostgreSQL 58 / Oracle 57 / **SQLServer 72**）。

### 1.2 规则的数据结构

一条规范规则 = `insight_sql_standard_rules` 一行，字段与含义：

| 字段 | 含义 | 说明 |
|---|---|---|
| `rule_key` | 规则唯一键 | 形如 `{db}.{struct|query_update}.{能力项}`，引擎产出的 `rule_key` 是**后缀**，靠它回查级别 |
| `category` | 大类 | `structure`（结构/DDL）/ `query_update`（DML） |
| `db_type` | 适用数据库 | `mysql` / `tidb` / `postgres` / `oracle` / **`sqlserver`** / `all` |
| `rule_type` | 规则类型（页面「类型」列） | 表 / 列 / 索引 / 库 / 对象 / 更新 / 写入 / 删除 / 查询 |
| `name` / `description` | 规则名称 / 规则描述 | 页面展示与命中说明 |
| `base_level` | 基线级别 | `must`（必须改进）/ `suggest`（建议改进） |
| `level` | 生效级别 | `must` / `suggest` / `off`（关闭） |
| `params` | 规则参数（JSON） | 如 `{"max_length": 1024}`，由 `applyStandardRule` 落到引擎开关/threshold |
| `sort_order` / `implemented` | 排序 / 是否已实现 | |
| `standard_id` | 归属规范 | 一个规范一套规则，实例按环境绑定规范 |

### 1.3 三个必须一致的口径（本轮的红线）

1. **目录 ↔ 引擎**：引擎产出的每个 `rule_key` 后缀都必须在目录里有对应规则，否则预审结论会走
   「致命类别兜底」挂到别的规则上（张冠李戴）；反过来目录里有而引擎不产出的就是「幽灵规则」
   （页面能配、永不生效）。两端由守门测试锁定。
2. **级别 ↔ 同级**：同一能力项在不同数据库上的 `base_level` 必须一致，否则同一段 SQL 换个库
   结论就变（Oracle 轮踩过这个坑）。
3. **参数 ↔ 参数名**：`applyStandardRule` 里未映射的规则键 = 可配置但永不触发，因此每条
   SQLServer 规则都必须落到一个真实的引擎参数上。

---

## 二、SQLServer 的规则范围

### 2.1 覆盖判定原则

以 MySQL 的 82 个**能力项**（`rule_key` 后缀）为全集逐条判断，收录条件为：

1. **语义在 T-SQL 里存在**（等价语法或等价替代物存在）；
2. **引擎可判定**（纯文本可判，或能用 `sys.*` 数据字典判定）。

两条都不满足的一律不收录——收录了就是幽灵规则。

结果：**82 − 10 = 72 条**，其中 `structure` 42 条、`query_update` 30 条。

### 2.2 不收录的 10 条及原因

| 能力项 | 不收录原因 |
|---|---|
| `limit_auto_inc_col_is_unsigned` | T-SQL 没有 `UNSIGNED` 属性（用 `tinyint/smallint/int/bigint` 直接选宽度） |
| `limit_table_storage_engine` | SQL Server 没有可选的存储引擎（只有堆表/聚集索引两种组织方式） |
| `limit_database_charset` | SQL Server 没有库级字符集（只有排序规则 collation，且库级默认即可） |
| `no_column_charset` | 列上没有字符集，等价的「列级排序规则」由 `no_column_collate` 覆盖 |
| `limit_tbl_charset` / `limit_tbl_collation` | 表级没有字符集；表级排序规则无独立检查价值（列级已覆盖） |
| `tbl_charset_change_use_convert_syntax` | 依赖 `ALTER TABLE ... CONVERT TO CHARACTER SET`，T-SQL 无此语法 |
| `column_zerofill_forbidden` | T-SQL 没有 `ZEROFILL` |
| `disable_event_related_op` | SQL Server 没有 MySQL 的 EVENT 调度器（等价物是 SQL Agent Job，不在 DDL 审核范围内） |
| `disable_create_table_like` | T-SQL 不支持 `CREATE TABLE ... LIKE`，写法本身就不存在（CTAS 由 `disable_create_table_as` 覆盖 `SELECT ... INTO`） |

### 2.3 SQLServer 独有的 3 条（其他库没有对应能力项）

| 规则键 | 名称 | 为什么单独建规则 |
|---|---|---|
| `sqlserver.struct.disable_proc_related_op` | 禁止存储过程相关操作 | T-SQL 的存储过程是一等对象，DDL 工单里很常见，需要独立开关 |
| `sqlserver.struct.disable_custom_function_related_op` | 禁止自定义函数相关操作 | 同上（标量/内联表值/多语句表值函数） |
| `sqlserver.struct.disable_trigger_related_op` | 禁止触发器相关操作 | 同上；触发器对 DML 有隐式副作用，需要单独管控 |

三条规则的语义是「级别 = 必须改进 ⇒ 禁止；级别 = 关闭 ⇒ 允许」，与 MySQL 的
`disable_view_related_op` 同一套「能力开关型」语义，因此引擎侧映射为三个能力开关参数。

---

## 三、SQLServer 规则清单（72 条）

### 3.1 结构类（`sqlserver.struct.*`，42 条）

| # | 规则键（去 `sqlserver.` 前缀） | 规则名称 | 规则类型 | 基线级别 | 规则描述（T-SQL 口径） |
|---|---|---|---|---|---|
| 1 | `struct.limit_auto_inc_col_name_is_id` | 限制自增列名为 ID | 列 | 必须改进 | 限制自增列名（IDENTITY 列）为 ID |
| 2 | `struct.limit_create_table_auto_inc_init` | 限制建表自增列初始值 | 表 | 必须改进 | 限制建表自增初始值（IDENTITY(seed, increment) 的 seed） |
| 3 | `struct.limit_char_type_col_len` | 限制 CHAR 类型字段的长度 | 列 | 必须改进 | 限制 CHAR / NCHAR 类型字段长度（长度按字符计，非字节） |
| 4 | `struct.limit_col_name_case` | 限制字段名的大小写 | 列 | 必须改进 | 限制字段名大小写（SQL Server 默认排序规则下标识符大小写不敏感，此处约束命名风格） |
| 5 | `struct.col_name_no_keyword` | 限制字段名不能是关键字 | 列 | 必须改进 | 字段名不能是关键字（T-SQL 保留字） |
| 6 | `struct.limit_col_must_have_comment` | 限制列必须有注释 | 列 | 必须改进 | 列要有注释（SQL Server 无内联 COMMENT 语法，需 sp_addextendedproperty 扩展属性） |
| 7 | `struct.limit_varchar_type_col_len` | 限制 VARCHAR 类型字段的长度 | 列 | 必须改进 | 限制 VARCHAR / NVARCHAR 类型字段长度（MAX 视为超限） |
| 8 | `struct.limit_col_must_have_default` | 限制列必须有默认值 | 列 | 建议改进 | 除 TEXT/NTEXT/IMAGE/XML/VARBINARY(MAX)/VARCHAR(MAX)/NVARCHAR(MAX) 外，列需有默认值 |
| 9 | `struct.limit_col_not_null` | 限制列不可空（NOT NULL） | 列 | 建议改进 | 除 TEXT/NTEXT/IMAGE/XML/(MAX) 系列外，列不可空（NOT NULL） |
| 10 | `struct.limit_col_no_partial_data_type` | 限制列不能使用指定的数据类型 | 列 | 建议改进 | 建表或改表时禁止使用指定数据类型（TEXT/NTEXT/IMAGE 已废弃，MONEY/SQL_VARIANT/TIMESTAMP 语义易误用） |
| 11 | `struct.no_column_collate` | 限制列不能设置校对集 | 列 | 建议改进 | 不能显式设置列级 COLLATE（统一跟随库默认排序规则，避免同表列排序规则混用） |
| 12 | `struct.limit_table_column_count` | 限制表字段数量上限 | 表 | 必须改进 | 限制表字段的数量（SQL Server 硬上限 1024 列） |
| 13 | `struct.limit_tbl_must_comment` | 限制表必须有注释 | 表 | 必须改进 | 表备注便于使用者快速了解业务（需 sp_addextendedproperty 扩展属性） |
| 14 | `struct.table_must_have_pk` | 表必须有主键 | 表 | 必须改进 | 表要有主键（否则退化为堆表，更新删除与复制链路性能显著劣化） |
| 15 | `struct.merge_multiple_alter_stmt_same_table` | 同一张表的多条 ALTER 语句建议合为一条 | 表 | 建议改进 | 建议将同一张表的多条 DDL 合并，避免多次表锁与重复扫描 |
| 16 | `struct.limit_tbl_name_case` | 限制表名的大小写 | 表 | 建议改进 | 限制表名大小写（SQL Server 默认排序规则下标识符大小写不敏感，此处约束命名风格） |
| 17 | `struct.tbl_name_no_keyword` | 限制表名不能是关键字 | 表 | 建议改进 | 表名不能是关键字（T-SQL 保留字） |
| 18 | `struct.limit_tbl_no_use_fk` | 限制表不能使用外键 | 表 | 建议改进 | 表不能使用外键 |
| 19 | `struct.table_must_include_required_columns` | 限制表必须包含指定列 | 表 | 建议改进 | 表需要包含某些列 |
| 20 | `struct.create_tbl_must_not_exist` | 限制建表的目标表不能已存在 | 表 | 必须改进 | 目标表已存在时 CREATE TABLE 执行必然失败（错误号 2714） |
| 21 | `struct.obj_tbl_must_exist` | 限制操作的目标表必须存在 | 表 | 必须改进 | 表不存在时 ALTER/DROP/TRUNCATE 执行必然失败（错误号 208） |
| 22 | `struct.obj_tbl_must_not_exist` | 限制改名的目标表名不能已存在 | 表 | 必须改进 | sp_rename 的新表名已存在会产生冲突，执行必然失败 |
| 23 | `struct.obj_col_must_exist` | 限制操作的目标列必须存在 | 列 | 必须改进 | 列不存在时 ALTER COLUMN / DROP COLUMN 执行必然失败（错误号 4924） |
| 24 | `struct.obj_col_must_not_exist` | 限制改名的目标列名不能已存在 | 列 | 必须改进 | sp_rename 的新列名已存在会产生列冲突，执行必然失败 |
| 25 | `struct.limit_uniq_idx_name_fmt` | 限制唯一索引命名格式 | 索引 | 必须改进 | 限制唯一索引名格式 |
| 26 | `struct.index_name_no_duplicate` | 限制索引名称不能重复 | 索引 | 建议改进 | 单表内索引（普通/唯一/外键）名称不允许重复 |
| 27 | `struct.limit_tbl_idx_count` | 限制表的索引数量 | 索引 | 建议改进 | 表上允许创建的最大索引数量 |
| 28 | `struct.index_must_have_name` | 限制索引必须设置名称 | 索引 | 建议改进 | 限制索引要有名称 |
| 29 | `struct.limit_normal_idx_name_fmt` | 限制普通索引命名格式 | 索引 | 建议改进 | 限制普通索引名格式 |
| 30 | `struct.limit_pk_col_must_auto_inc` | 限制主键列必须自增 | 索引 | 建议改进 | 限制主键列必须自增（IDENTITY 列） |
| 31 | `struct.limit_pk_column_count` | 限制主键包含的列数 | 索引 | 建议改进 | 限制主键包含列的个数 |
| 32 | `struct.limit_pk_col_type` | 限制主键列的数据类型 | 索引 | 建议改进 | 限制主键列类型 |
| 33 | `struct.limit_single_index_column_count` | 限制单个索引包含的列数 | 索引 | 建议改进 | 限制单个索引包含列的个数（键列，不含 INCLUDE 列） |
| 34 | `struct.limit_tbl_redundant_idx` | 限制冗余索引 | 索引 | 建议改进 | 不允许创建冗余索引（同列组合重复或已被最左前缀覆盖） |
| 35 | `struct.obj_idx_must_exist` | 限制操作的目标索引必须存在 | 索引 | 必须改进 | 索引不存在时 DROP INDEX 执行必然失败（错误号 3701） |
| 36 | `struct.obj_idx_must_not_exist` | 限制改名的目标索引名不能已存在 | 索引 | 必须改进 | sp_rename 的新索引名已存在会产生冲突，执行必然失败 |
| 37 | `struct.disable_custom_function_related_op` | 禁止自定义函数相关操作 | 对象 | 建议改进 | 禁止 CREATE/ALTER/DROP FUNCTION 变更（含标量函数与内联表值函数） |
| 38 | `struct.disable_proc_related_op` | 禁止存储过程相关操作 | 对象 | 建议改进 | 禁止 CREATE/ALTER/DROP PROCEDURE 变更 |
| 39 | `struct.disable_trigger_related_op` | 禁止触发器相关操作 | 对象 | 建议改进 | 禁止 CREATE/ALTER/DROP TRIGGER 变更 |
| 40 | `struct.disable_view_related_op` | 禁止视图相关操作 | 对象 | 建议改进 | 禁止视图的创建、更新、删除等变更 |
| 41 | `struct.disable_create_table_as` | 禁止 CREATE TABLE ... AS SELECT | 对象 | 建议改进 | 禁止使用 CTAS 语法建表（T-SQL 写法为 SELECT ... INTO）：表结构来自 SELECT，字段/索引/约束等建表规范无法逐项校验 |
| 42 | `struct.obj_view_must_not_exist` | 限制创建视图的目标视图不能已存在 | 对象 | 必须改进 | 视图已存在时 CREATE VIEW 执行必然失败，建议改用 CREATE OR ALTER VIEW |

### 3.2 DML 类（`sqlserver.query_update.*`，30 条）

| # | 规则键（去 `sqlserver.` 前缀） | 规则名称 | 规则类型 | 基线级别 | 规则描述（T-SQL 口径） |
|---|---|---|---|---|---|
| 1 | `query_update.limit_update_delete_affected_rows` | 限制 UPDATE/DELETE 单语句影响行数 | 更新 | 必须改进 | 大批量更新/删除会暴涨事务日志、长时间锁大量数据，且 SQL Server 锁升级后回滚代价极高 |
| 2 | `query_update.delete_stmt_check_table_exists` | DELETE 语句检测表是否存在 | 删除 | 建议改进 | 检查 DELETE 涉及的表是否存在 |
| 3 | `query_update.limit_update_delete_join_table_count` | 限制 UPDATE/DELETE 语句关联表的数量 | 更新 | 建议改进 | 多表关联易致性能下降与锁竞争（T-SQL 的关联表写在 FROM 子句里） |
| 4 | `query_update.update_delete_stmt_join_on_check` | UPDATE/DELETE 语句检测多表关联语法是否完整（JOIN 遗漏 ON 子句） | 更新 | 建议改进 | 缺少 ON 子句会产生笛卡尔积，可能误更新/删除大量数据 |
| 5 | `query_update.limit_update_delete_data_size` | 限制 UPDATE/DELETE 语句 LIMIT 的行数 | 更新 | 建议改进 | 限制单次数据量可缩短锁持有时间（T-SQL 无 LIMIT，等价写法为 TOP (n)） |
| 6 | `query_update.update_delete_stmt_suggest_where` | UPDATE/DELETE 语句建议指定 WHERE 条件 | 更新 | 建议改进 | 缺少 WHERE 会全表更新/删除，风险高；WHERE 可走索引减少数据量 |
| 7 | `query_update.update_delete_stmt_must_use_idx` | UPDATE/DELETE 语句必须使用索引 | 更新 | 建议改进 | WHERE 条件必须命中已有索引的首列，否则表扫描 + 锁升级 |
| 8 | `query_update.update_delete_stmt_no_order_by_clause` | UPDATE/DELETE 语句不能有 ORDER BY 子句 | 更新 | 建议改进 | 排序对 UPDATE/DELETE 无意义（T-SQL 里 ORDER BY 只允许配合 TOP 使用） |
| 9 | `query_update.update_delete_stmt_where_subquery_check` | UPDATE/DELETE 语句检测 WHERE 条件是否包含子查询 | 更新 | 建议改进 | 子查询执行计划复杂、开销大，且拉长锁持有时间 |
| 10 | `query_update.update_multi_table_column_prefix_check` | UPDATE 多表时检测 SET 的列是否指定表前缀 | 更新 | 建议改进 | 多表存在同名字段时，不指定表前缀易产生歧义 |
| 11 | `query_update.update_stmt_avoid_update_create_time_column` | UPDATE 语句不建议更新表上的“创建时间”列 | 更新 | 建议改进 | 创建时间属数据元信息，更新会破坏审计追溯 |
| 12 | `query_update.update_stmt_check_updates_pk` | UPDATE 语句检测是否更新了主键 | 更新 | 建议改进 | 更新主键会触发聚集索引重建并可能引发级联更新，影响性能 |
| 13 | `query_update.update_stmt_suggest_update_time_column` | UPDATE 语句建议更新表上的“修改时间”列 | 更新 | 建议改进 | 修改时间便于审计、变更追踪与增量同步 |
| 14 | `query_update.update_stmt_set_separator_check` | UPDATE 语句检测 SET 多个列之间的分隔符（AND 非法） | 更新 | 建议改进 | AND 是逻辑运算符，不能用作 SET 分隔符，否则语法异常 |
| 15 | `query_update.update_stmt_check_column_exists` | UPDATE 语句检测表/字段是否存在 | 更新 | 建议改进 | UPDATE 语句的目标表、字段不存在时执行必然失败（错误号 208 / 207） |
| 16 | `query_update.update_stmt_check_updates_unique_key` | UPDATE 语句检测是否更新了唯一键 | 更新 | 建议改进 | 更新唯一键会破坏外键关联与业务逻辑，易致数据不一致 |
| 17 | `query_update.insert_stmt_col_name_no_dup` | INSERT 语句字段名不能重复 | 写入 | 建议改进 | 字段名重复会导致歧义并使 SQL 执行失败（错误号 264） |
| 18 | `query_update.insert_stmt_columns_match_values` | INSERT 语句字段列表与值列表要匹配 | 写入 | 建议改进 | 字段与值数量不一致会直接报错（错误号 213），显式列表时逐行比对，无列表时与表结构比对 |
| 19 | `query_update.insert_stmt_specify_columns` | INSERT 语句建议指定字段列表 | 写入 | 建议改进 | 显式指定字段列表更清晰，且表结构变更时语句不易失效 |
| 20 | `query_update.insert_stmt_not_null_column_null_value` | INSERT 语句不能为 NOT NULL 列插入 NULL 值 | 写入 | 建议改进 | NOT NULL 列必须给值，否则执行失败并易破坏数据完整性（错误号 515） |
| 21 | `query_update.insert_stmt_avoid_sysdate_func` | INSERT 语句不建议使用 SYSDATE() 函数 | 写入 | 建议改进 | INSERT 显式写入 GETDATE()/SYSDATETIME() 等易变时间函数，会让数据值随执行时刻漂移、不便复现与比对 |
| 22 | `query_update.limit_insert_values_row_count` | 限制 INSERT 语句单条 VALUES 的行数 | 写入 | 建议改进 | 大批量插入会暴涨事务日志、占用大量内存并加剧锁竞争（T-SQL 单条 VALUES 上限 1000 行） |
| 23 | `query_update.insert_stmt_check_column_exists` | INSERT 语句检测目标表/字段是否存在 | 写入 | 建议改进 | 目标表或字段不存在时执行必然失败（错误号 208 / 207），提交前必须拦截 |
| 24 | `query_update.limit_where_in_element_count` | 限制 WHERE 条件中 IN 子句的元素个数 | 查询 | 建议改进 | IN 元素过多难以命中索引，易触发全表扫描 |
| 25 | `query_update.where_avoid_leading_wildcard` | WHERE 条件中不建议使用前通配符查找 | 查询 | 建议改进 | 前置 % 无法走前缀索引，必然全表扫描 |
| 26 | `query_update.where_avoid_index_col_arithmetic_or_func` | WHERE 条件中不建议索引字段包含数学运算或函数运算 | 查询 | 建议改进 | 索引字段做函数/运算会失效，触发全表扫描 |
| 27 | `query_update.where_avoid_not_in_not_like` | WHERE 条件中不建议使用反向查询（NOT IN/NOT LIKE） | 查询 | 建议改进 | NOT 类反向条件难命中索引，多为全表扫描 |
| 28 | `query_update.where_or_condition_check` | WHERE 条件中检测是否使用 OR 连接过滤条件 | 查询 | 建议改进 | OR 易致索引失效走全表扫描，条件恒真还有误操作风险 |
| 29 | `query_update.where_implicit_type_cast_check` | WHERE 条件中检测字段是否存在隐式类型转换 | 查询 | 建议改进 | 隐式类型转换（如 VARCHAR 列与数值/日期比较）易致索引失效全表扫描，还可能比对出错 |
| 30 | `query_update.where_like_without_wildcard` | WHERE 条件中检测 LIKE 语句是否缺少通配符 | 查询 | 建议改进 | 无通配符的 LIKE 等价于等值查询，用 = 更清晰且更易走索引 |

---

## 四、需要调整或扩展的规则条目（完整清单）

### 4.1 新增：规则目录（72 条）

- `backend/internal/inspect/services/standard_seed_sqlserver.go`（新文件）承载 72 条规则；
  `standard_seed.go` 的 `defaultStandardRules()` 末尾追加 `append(rules, sqlserverStandardRules()...)`。
- 结构完全复用既有字段，`rule_key` 前缀 `sqlserver.`，`db_type = "sqlserver"`。
- **存量规范**需要跑 `backend/sql/20261108_seed_sqlserver_rules.sql`（幂等补种，先备份）。

### 4.2 扩展：规则参数映射（`applyStandardRule`）

- 新增 `sqlServerRuleKeyAlias()`：把 `sqlserver.xxx` 归一成 `mysql.xxx` 再进原有 switch。
  69 条规则因此**复用** MySQL 的参数映射分支，无需复制 50+ 个 case。
- 新增 `applySQLServerExclusiveRule()`：先处理 3 条 SQLServer 独有开关型规则
  （`ENABLE_PROCEDURE_OBJECT` / `ENABLE_FUNCTION_OBJECT` / `ENABLE_TRIGGER_OBJECT`）。
- 「列不能使用指定数据类型」的默认清单按库分派扩展：
  `sqlserver` → `text / ntext / image / sql_variant / timestamp / money / smallmoney`
  （不再落到 MySQL 的 `double / float / enum / bit / set`）。
- `normalizeRuleDbType()` 扩展：`sqlserver / sql server / mssql / microsoft sql server` → `sqlserver`。

### 4.3 扩展：类型字典（`config` 包）

| 新增项 | 用途 |
|---|---|
| `IsForbiddenSQLServerType()` | 禁用类型判定，含同义词归一（`integer→int`、`national char→nchar`、`double precision→float`、`rowversion→timestamp` 等） |
| `SQLServerPrimaryKeyTypes` | 主键列可选取值；**不含** MAX 变体与 `text/ntext/image/xml/sql_variant/地理类型`（它们不能做键） |
| `PrimaryKeyTypesByDB["sqlserver"]`、`"mssql"` | 让「主键列类型」规则按库取白名单 |
| `IdentifierKeywordSetFor(dbType)` + `DefaultSQLServerExtraKeywords` | 追加 `TOP/OUTPUT/PIVOT/MERGE/IDENTITY/NOLOCK/...` 等 T-SQL 关键字，**只对 sqlserver 生效** |
| `ENABLE_PROCEDURE_OBJECT` / `ENABLE_FUNCTION_OBJECT` / `ENABLE_TRIGGER_OBJECT` | 三条独有规则的能力开关，默认 `true`（允许），对既有库无影响 |

### 4.4 扩展：审核引擎（新增 5 个文件）

| 文件 | 职责 |
|---|---|
| `checker/sqlserver_parse.go` | T-SQL 文本解析：`[方括号]` 标识符、`IDENTITY(seed,inc)`、`NVARCHAR(MAX)`、`CREATE INDEX`、`ALTER TABLE` 多动作、`sp_addextendedproperty`、`sp_rename`、`GO` 批处理 |
| `checker/sqlserver_rules.go` | 规则主体：建表/改表/索引/DML 的逐条判定与结论文案 |
| `checker/sqlserver_dml.go` | DML 结构分析（`TOP (n)`→限量、别名→真实表名还原、WHERE 子句抽取） |
| `checker/sqlserver_gate.go` | 准入闸门：`SELECT ... INTO`（CTAS）、视图 |
| `checker/sqlserver_object_rules.go` | 连库判定：`sys.objects / sys.columns / sys.indexes / sys.extended_properties` |

`checker/check.go` 的 `Run()` 新增分发：
`DbType ∈ {SQLServer, MSSQL}` → `runSQLServerInspect()`。

### 4.5 扩展：数据访问层

- `controllers/dao/db.go`：新增 `isSQLServer()` / `openSQLServer()`（驱动 `github.com/microsoft/go-mssqldb`，
  DSN 由 `internal/common/sqlserverdsn` 统一构造，已含 `tlsmin=1.0` 以兼容 SQL Server 2012）。
- `controllers/dao/dao.go`：`ShowCreateTable` 对 SQL Server 返回「不支持读取表结构比对」，
  依赖该能力的规则静默跳过（与 PG 一致，宁可漏报不误报）。

### 4.6 非破坏性改造（关键：不影响既有库）

| 位置 | 原做法 | 现做法 | 为什么不直接改 |
|---|---|---|---|
| `pkg/sqlsplit` | `SplitTyped(text)` 内置 DDL 前缀表 | 新增 `SplitTypedExtra(text, extraDDLPrefixes)`，`SplitTyped` 行为不变 | 直接往原表塞 `EXEC` 会改变 MySQL/PG/Oracle 的分句结果 |
| 关键字清单 | `DefaultIdentifierKeywords()` 是 MySQL+PG 并集 | 新增 `IdentifierKeywordSetFor(dbType)`，仍只在 sqlserver 分支追加 | 直接并进基础清单会让既有四个库「忽然多拦一批关键字」 |
| 参数默认值 | —— | 新增三个开关默认 `true` | 默认值即「允许」，对其他库的引擎毫无影响（没有引擎读这三个字段） |

### 4.7 打通工单链路（否则规范永远跑不到 SQLServer 上）

规范预审发生在工单创建阶段，而此前 SQLServer 工单被三处硬门拦着：

| 位置 | 改动 |
|---|---|
| `orders/services/db_support.go` `supportsDeepInspect` | 允许 `SQLServer` / `MSSQL` 进入深度审核 |
| `orders/services/db_support.go` `validateOrderSQLTypeSupport` | 允许创建 SQLServer 工单（执行器 `SQLServerExecutor` 早已存在） |
| `orders/services/commit.go` 数据源候选 | 下拉里加入 `SQLServer` / `MSSQL` 实例 |

三处都是**纯增量**（只放行新类型），不改变既有类型的任何判定。

### 4.8 前端

| 文件 | 改动 |
|---|---|
| `www/src/views/admin/system/sql-standards/RuleTable.vue` | `DB_LABELS` / `DB_LOGO` / `DB_ORDER` 加 `sqlserver`；新增 `SQLSERVER_TYPE_OPTIONS`（T-SQL 类型体系）、`SQLSERVER_FORBIDDEN_TYPE_DEFAULTS`、`SQLSERVER_PK_COL_TYPE_DEFAULT`；`defaultForbiddenTypes()` / `defaultPkColTypes()` 按库分派 |
| `www/src/views/admin/system/sql-standards/SQLStandardDetail.vue` | `DB_LABELS` / `DB_ORDER` 加 `sqlserver` |

图标 `src/assets/logo/sqlserver.png` 与 `utils/dbTypeLogo.js` 的 `SQLSERVER` 映射此前已存在，直接复用。

---

## 五、与现有规范结构的一致性

| 一致性要求 | 落地方式 |
|---|---|
| 规则键命名一致 | `sqlserver.struct.*` / `sqlserver.query_update.*`，后缀与 MySQL 同名能力项**逐字相同** |
| 字段/枚举一致 | `category`、`rule_type`、`level`、`base_level`、`params` 语义与取值集合完全沿用 |
| 级别口径一致 | 由 `TestSQLServerSeedLevelAlignedWithMySQL` 锁定：同能力项基线级别必须与 MySQL 相同 |
| 结论级别回查一致 | 引擎只产出后缀 key，`orders/services` 按「实例 → 规范 → 该库规则」回查级别，无需改判定逻辑 |
| 参数映射一致 | 走 `mysql.*` 别名分支，阈值键名（`max_length` / `max_affected_rows` / `types` …）完全相同 |
| 连库失败策略一致 | 改用 `sys.*` 数据字典；连不上/无权限时一律跳过，绝不误报 |

---

## 六、验证记录

### 6.1 编译与静态检查

```
go build ./...                      # 通过
go vet ./internal/inspect/... ./internal/orders/...   # 通过
gofmt -l（本次新增/修改文件）        # 无输出
npx vite build                      # 通过
```

### 6.2 单元测试

| 用例集 | 条数 | 结果 |
|---|---|---|
| `TestSQLServer*`（引擎判定） | 30 | PASS |
| `TestSQLServerSeedCoversEngineKeys` / `NoGhostRules` / `RuleTypeValid` / `LevelAlignedWithMySQL` | 4 | PASS |
| `TestApplyStandardRuleSQLServerAlias` / `Exclusive` / `ForbiddenTypes` | 3 | PASS |
| `TestNormalizeRuleDbTypeCoversAllInstanceTypes`（含 SQLServer/MSSQL） | 1 | PASS |
| `go test ./internal/... ./pkg/...` 全量 | — | 全部 ok |

### 6.3 端到端样例（真实引擎输出）

输入（故意写坏的一段 T-SQL）：

```sql
CREATE TABLE dbo.T_Order (
  ID int IDENTITY(1,1) NOT NULL,
  [order_no] varchar(max) NULL,
  amount money NULL,
  remark text NULL,
  status int NULL,
  create_time datetime NOT NULL DEFAULT getdate()
);
CREATE INDEX idx_bad ON dbo.T_Order (order_no, amount, status);
UPDATE o SET status = 1, remark = 'x' AND amount = 1 FROM dbo.T_Order o JOIN dbo.T_User u ON u.id = o.user_id WHERE o.status = 1 OR o.remark LIKE '%abc';
DELETE FROM dbo.T_Order WHERE id = 1 ORDER BY id;
INSERT INTO dbo.T_Order VALUES (1, 'a');
SELECT * INTO dbo.T_Order_bak FROM dbo.T_Order;
EXEC sp_addextendedproperty 'MS_Description', N'订单表', 'SCHEMA', 'dbo', 'TABLE', 'T_Order';
```

引擎结论（节选，级别由规范级别回查得到）：

```
limit_tbl_name_case                 表`T_Order`命名不符合要求：必须为小写 …
table_must_have_pk                  表`T_Order`必须定义主键（建议 `id BIGINT IDENTITY(1,1) PRIMARY KEY`），否则退化为堆表
limit_varchar_type_col_len          列`order_no`长度太大（max = 1024），当前使用VARCHAR(MAX) …
limit_col_no_partial_data_type      列`amount`不允许定义MONEY类型（表`T_Order`）
limit_col_no_partial_data_type      列`remark`不允许定义TEXT类型（表`T_Order`）
limit_col_must_have_comment         列`ID`必须要有注释：SQL Server 需用 `EXEC sp_addextendedproperty 'MS_Description', …` 补充
where_avoid_leading_wildcard        WHERE 条件中 `LIKE` 使用了前通配符（%xxx）…（表`T_Order`）
where_or_condition_check            WHERE 条件中使用 `OR` 连接过滤条件 …（表`T_Order`）
update_multi_table_column_prefix_check  多表 UPDATE 的 SET 列`status`未指定表前缀，T-SQL 下会因列名歧义报错 209
update_stmt_set_separator_check     UPDATE 语句 SET 子句多个列之间应使用逗号分隔，不允许使用 AND
update_delete_stmt_no_order_by_clause   DELETE 语句不允许使用 ORDER BY（T-SQL 不支持排序子句）
insert_stmt_specify_columns         INSERT 语句建议显式指定字段列表
disable_create_table_as             语句不允许执行：`SELECT ... INTO`（新表`T_Order_bak`）
```

注：`EXEC sp_addextendedproperty` 被正确识别为「表注释」，未产生注释缺失类误报。

---

## 七、上线步骤

1. 部署新版本（后端 + 前端）。
2. **存量环境**执行 `backend/sql/20261108_seed_sqlserver_rules.sql`：
   - STEP 1 只读预览（每个规范缺多少条）；
   - STEP 2 备份 + 幂等插入（`NOT EXISTS`，可重复执行）；
   - STEP 3 复核（应为每规范 72 条，`structure` 42 / `query_update` 30）。
3. 规范页面确认 SQLServer 图标与新规则可见，按需调整级别（`level = off` 即关闭）。
4. SQLServer 实例创建 DDL/DML 工单，提交时应看到规范预审结论。

## 八、已知限制

- SQL Server 走**文本解析**而非官方 AST 解析器：复杂动态 SQL（`EXEC(@sql)` 拼接）、跨库
  四段命名（`server.db.schema.table`）按保守口径处理，宁可漏报不误报。
- 连库类规则（对象存在性、影响行数估算、索引使用）依赖实例连通性；连不上时静默跳过。
- `Read uncommitted` / 事务隔离级别等运行期语义不在本规范范围内。
