package checker

// PG 规则实测用例集：34 条 DDL（structure）+ 27 条 DML（query_update）。
//
// 用例口径：
//   - 每条规则至少一条「违规语句」，期望命中该 rule key；
//   - 规范中 level=off 的规则保留违规语句，期望「不命中」——证明确实被关闭而不是缺失；
//   - SQL 不真正执行（预审链路只做静态分析 + 只读元信息查询），
//     但 fixture 必须真实存在，否则依赖元信息的规则会静默跳过。

import (
	"fmt"
	"strings"
)

// bulkInsertSQL 生成 N 行 VALUES 的 INSERT（规则 limit_insert_values_row_count 用）
func bulkInsertSQL(rows int) string {
	var sb strings.Builder
	sb.WriteString("INSERT INTO t_pg_rule_user (id, name) VALUES ")
	for i := 1; i <= rows; i++ {
		if i > 1 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "(%d, 'b%d')", 100000+i, i)
	}
	sb.WriteString(";")
	return sb.String()
}

// wideTableSQL 生成 51 列表（规则 limit_table_column_count 默认上限 50）
func wideTableSQL(cols int) string {
	var sb strings.Builder
	sb.WriteString("CREATE TABLE t_pg_rule_wide (\n  id bigint NOT NULL,\n")
	for i := 1; i < cols; i++ {
		fmt.Fprintf(&sb, "  c%d integer NOT NULL DEFAULT 0,\n", i)
	}
	sb.WriteString("  create_time timestamp NOT NULL DEFAULT now(),\n")
	sb.WriteString("  update_time timestamp,\n  PRIMARY KEY (id)\n);")
	return sb.String()
}

func pgLiveCases() []liveCase {
	hit := "命中"
	miss := "不命中（规则 level=off）"

	cases := []liveCase{
		// ==================== DDL：34 条 ====================
		{
			Key: "disable_create_table_like", Category: "structure", Name: "禁止 CREATE TABLE ... LIKE",
			SQL:    `CREATE TABLE t_pg_rule_like_new (LIKE t_pg_rule_user INCLUDING ALL);`,
			Expect: true, ExpectNote: hit,
			Note: "PG 用括号内 LIKE 子句表达 CTL，结构继承源表",
		},
		{
			Key: "disable_create_table_as", Category: "structure", Name: "禁止 CREATE TABLE ... AS SELECT",
			SQL:    `CREATE TABLE t_pg_rule_ctas_new AS SELECT id, name FROM t_pg_rule_user;`,
			Expect: true, ExpectNote: hit,
			Note: "CTAS 绕过字段/索引/约束规范",
		},
		{
			Key: "merge_multiple_alter_stmt_same_table", Category: "structure", Name: "同一张表多条 ALTER 建议合并",
			SQL: `ALTER TABLE t_pg_rule_multi ADD COLUMN d integer;
ALTER TABLE t_pg_rule_multi ADD COLUMN e integer;`,
			Expect: true, ExpectNote: hit,
			Note: "两条 ALTER 同一表 → 合并建议（整段 SQL 层面汇总）",
		},
		{
			Key: "limit_char_type_col_len", Category: "structure", Name: "CHAR 长度上限",
			SQL:    `CREATE TABLE t_pg_rule_char (id bigint NOT NULL, code char(100) NOT NULL DEFAULT '', PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "规范 max_length=64，char(100) 超限",
		},
		{
			Key: "limit_col_name_case", Category: "structure", Name: "字段名大小写",
			SQL:    `CREATE TABLE t_pg_rule_case (id bigint NOT NULL, UserName varchar(20) NOT NULL DEFAULT '', PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "规范要求小写，UserName 大写",
		},
		{
			Key: "col_name_no_keyword", Category: "structure", Name: "字段名不能是关键字",
			SQL:    `CREATE TABLE t_pg_rule_kw (id bigint NOT NULL, user varchar(20) NOT NULL DEFAULT '', PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "user 在 PG 关键字集合内",
		},
		{
			Key: "limit_col_must_have_comment", Category: "structure", Name: "列必须有注释",
			SQL:    `CREATE TABLE t_pg_rule_colcomment (id bigint NOT NULL, name varchar(20) NOT NULL DEFAULT '', PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "PG 列注释靠 COMMENT ON COLUMN，工单未提供即命中",
		},
		{
			Key: "limit_table_column_count", Category: "structure", Name: "表字段数量上限",
			SQL:    wideTableSQL(51),
			Expect: true, ExpectNote: hit,
			Note: "默认上限 50，构造 51 列",
		},
		{
			Key: "table_must_have_pk", Category: "structure", Name: "表必须有主键",
			SQL:    `CREATE TABLE t_pg_rule_nopk (id bigint NOT NULL, name varchar(20) NOT NULL DEFAULT '');`,
			Expect: true, ExpectNote: hit,
			Note: "无 PRIMARY KEY",
		},
		{
			Key: "limit_uniq_idx_name_fmt", Category: "structure", Name: "唯一索引命名格式",
			SQL:    `CREATE TABLE t_pg_rule_ukname (id bigint NOT NULL, code varchar(20) NOT NULL DEFAULT '', create_time timestamp NOT NULL DEFAULT now(), update_time timestamp, CONSTRAINT unique_bad_name UNIQUE (code), PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "默认唯一索引正则 uk_\\w+，unique_bad_name 不匹配",
		},
		{
			Key: "limit_varchar_type_col_len", Category: "structure", Name: "VARCHAR 长度上限",
			SQL:    `CREATE TABLE t_pg_rule_varchar (id bigint NOT NULL, memo varchar(20000) NOT NULL DEFAULT '', PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "规范 max_length=16383，varchar(20000) 超限",
		},
		{
			Key: "limit_col_must_have_default", Category: "structure", Name: "列必须有默认值",
			SQL:    `CREATE TABLE t_pg_rule_default (id bigint NOT NULL, flag integer NOT NULL, PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "flag 无 DEFAULT",
		},
		{
			Key: "limit_col_not_null", Category: "structure", Name: "列不可空",
			SQL:    `CREATE TABLE t_pg_rule_notnull (id bigint NOT NULL, remark varchar(50) DEFAULT '', PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "remark 未声明 NOT NULL（varchar 不在豁免类型内）",
		},
		{
			Key: "limit_col_no_partial_data_type", Category: "structure", Name: "列不能使用指定数据类型",
			SQL:    `CREATE TABLE t_pg_rule_type (id bigint NOT NULL, rate double precision NOT NULL DEFAULT 0, PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "禁用类型含 double；PG 的 double precision 归一化为 double",
		},
		{
			Key: "disable_view_related_op", Category: "structure", Name: "禁止视图相关操作",
			SQL:    `CREATE VIEW v_pg_rule_probe AS SELECT id FROM t_pg_rule_user;`,
			Expect: true, ExpectNote: hit,
			Note: "规范级别 suggest=开启 → 视图被闸门拦截",
		},
		{
			Key: "limit_tbl_idx_count", Category: "structure", Name: "表的索引数量上限",
			SQL:    `CREATE INDEX idx_pg_multi_c ON t_pg_rule_multi (c);`,
			Expect: true, ExpectNote: hit,
			Note: "规范 max_count=1，t_pg_rule_multi 已有 3 条索引 + 本语句 1 条",
		},
		{
			Key: "index_must_have_name", Category: "structure", Name: "索引必须设置名称",
			SQL:    `CREATE INDEX ON t_pg_rule_user (status);`,
			Expect: true, ExpectNote: hit,
			Note: "PG 允许匿名索引（自动命名），规范要求显式命名",
		},
		{
			Key: "limit_normal_idx_name_fmt", Category: "structure", Name: "普通索引命名格式",
			SQL:    `CREATE INDEX idx_bad_name ON t_pg_rule_user (status);`,
			Expect: true, ExpectNote: hit,
			Note: "规范正则 idx1_\\w+，idx_bad_name 不匹配",
		},
		{
			Key: "limit_pk_column_count", Category: "structure", Name: "主键包含的列数",
			SQL:    `CREATE TABLE t_pg_rule_pkcols (id bigint NOT NULL, tenant_id bigint NOT NULL, PRIMARY KEY (id, tenant_id));`,
			Expect: true, ExpectNote: hit,
			Note: "规范 max_key_parts=1，复合主键 2 列",
		},
		{
			Key: "limit_tbl_redundant_idx", Category: "structure", Name: "限制冗余索引",
			SQL: `CREATE INDEX idx_pg_dup_a ON t_pg_rule_multi (a);
CREATE INDEX idx_pg_dup_ab ON t_pg_rule_multi (a, b);`,
			Expect: true, ExpectNote: hit,
			Note: "(a) 是 (a,b) 的最左前缀 → 由更宽的一侧报出",
		},
		{
			Key: "limit_pk_col_type", Category: "structure", Name: "主键列的数据类型",
			SQL:    `CREATE TABLE t_pg_rule_pktype (id varchar(20) NOT NULL, PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "规范 pk_types=bigint,int，varchar 主键违规",
		},
		{
			Key: "limit_single_index_column_count", Category: "structure", Name: "单个索引包含的列数",
			SQL:    `CREATE INDEX idx_pg_three ON t_pg_rule_multi (a, b, c);`,
			Expect: true, ExpectNote: hit,
			Note: "规范 max_key_parts=2，索引含 3 列",
		},
		{
			Key: "limit_tbl_name_case", Category: "structure", Name: "表名的大小写",
			SQL:    `CREATE TABLE T_PG_RULE_UPPER (id bigint NOT NULL, create_time timestamp NOT NULL DEFAULT now(), update_time timestamp, PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "规范 name_case=lower",
		},
		{
			Key: "tbl_name_no_keyword", Category: "structure", Name: "表名不能是关键字",
			SQL:    `CREATE TABLE user (id bigint NOT NULL, PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "user 在关键字集合内",
		},
		{
			Key: "limit_tbl_no_use_fk", Category: "structure", Name: "限制表不能使用外键",
			SQL:    `CREATE TABLE t_pg_rule_fk (id bigint NOT NULL, dept_id bigint NOT NULL DEFAULT 0, PRIMARY KEY (id), CONSTRAINT fk_rule_dept FOREIGN KEY (dept_id) REFERENCES t_pg_rule_dept (id));`,
			Expect: true, ExpectNote: hit,
			Note: "规范未开启外键 → 命中",
		},
		{
			Key: "table_must_include_required_columns", Category: "structure", Name: "表必须包含指定列",
			SQL:    `CREATE TABLE t_pg_rule_reqcol (id bigint NOT NULL, name varchar(20) NOT NULL DEFAULT '', PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "规范要求包含 create_time、update_time，缺失",
		},
		{
			Key: "limit_tbl_must_comment", Category: "structure", Name: "表必须有注释",
			SQL:    `CREATE TABLE t_pg_rule_nocomment (id bigint NOT NULL, create_time timestamp NOT NULL DEFAULT now(), update_time timestamp, PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "工单未提供 COMMENT ON TABLE，且表在库中不存在",
		},
		{
			Key: "create_tbl_must_not_exist", Category: "structure", Name: "建表目标表不能已存在",
			SQL:    `CREATE TABLE t_pg_rule_user (id bigint NOT NULL, PRIMARY KEY (id));`,
			Expect: true, ExpectNote: hit,
			Note: "t_pg_rule_user 为 fixture，已存在",
		},
		{
			Key: "obj_tbl_must_exist", Category: "structure", Name: "操作的目标表必须存在",
			SQL:    `ALTER TABLE t_pg_rule_no_such ADD COLUMN x integer;`,
			Expect: true, ExpectNote: hit,
			Note: "表不存在 → ALTER 必然失败（PG 42P01）",
		},
		{
			Key: "obj_col_must_exist", Category: "structure", Name: "操作的目标列必须存在",
			SQL:    `ALTER TABLE t_pg_rule_user DROP COLUMN no_such_col;`,
			Expect: true, ExpectNote: hit,
			Note: "DROP COLUMN 的列不存在（PG 42703）",
		},
		{
			Key: "obj_idx_must_exist", Category: "structure", Name: "操作的目标索引必须存在",
			SQL:    `DROP INDEX idx_pg_no_such;`,
			Expect: true, ExpectNote: hit,
			Note: "索引不存在（PG 42704）",
		},
		{
			Key: "obj_col_must_not_exist", Category: "structure", Name: "改名的目标列名不能已存在",
			SQL:    `ALTER TABLE t_pg_rule_user RENAME COLUMN name TO email;`,
			Expect: true, ExpectNote: hit,
			Note: "email 已是现有列 → 改名冲突（PG 42701）",
		},
		{
			Key: "obj_idx_must_not_exist", Category: "structure", Name: "改名的目标索引名不能已存在",
			SQL:    `ALTER INDEX idx_pg_multi_a RENAME TO idx_pg_multi_ab;`,
			Expect: true, ExpectNote: hit,
			Note: "目标索引名已存在（PG 42710）",
		},
		{
			Key: "obj_tbl_must_not_exist", Category: "structure", Name: "改名的目标表名不能已存在",
			SQL:    `ALTER TABLE t_pg_rule_user RENAME TO t_pg_rule_dept;`,
			Expect: true, ExpectNote: hit,
			Note: "目标表名已存在（PG 42P07）",
		},

		// ==================== DML：27 条 ====================
		{
			Key: "delete_stmt_check_table_exists", Category: "query_update", Name: "DELETE 语句检测表是否存在",
			SQL:    `DELETE FROM t_pg_rule_no_such WHERE id = 1;`,
			Expect: true, ExpectNote: hit,
			Note: "DELETE 目标表不存在（PG 42P01）",
		},
		{
			Key: "limit_update_delete_join_table_count", Category: "query_update", Name: "UPDATE/DELETE 关联表数量",
			SQL:    `UPDATE t_pg_rule_user u SET status = 1 FROM t_pg_rule_dept d, t_pg_rule_multi m WHERE u.id = d.id AND d.id = m.id;`,
			Expect: false, ExpectNote: miss,
			Note: "dev 规范该规则 level=off，构造 3 表关联也应不触发",
		},
		{
			Key: "limit_update_delete_data_size", Category: "query_update", Name: "UPDATE/DELETE LIMIT 行数上限",
			SQL:    `UPDATE t_pg_rule_user SET status = 1 WHERE id < 50 LIMIT 10;`,
			Expect: true, ExpectNote: hit,
			Note: "规范 max_limit_rows=5，LIMIT 10 超限",
		},
		{
			Key: "limit_update_delete_affected_rows", Category: "query_update", Name: "UPDATE/DELETE 单语句影响行数",
			SQL:    `UPDATE t_pg_rule_user SET status = 2 WHERE status = 0;`,
			Expect: true, ExpectNote: hit,
			Note: "规范 max_affected_rows=10；按 WHERE 做 COUNT(*) 估算约 100 行",
		},
		{
			Key: "update_delete_stmt_join_on_check", Category: "query_update", Name: "多表关联缺 ON 子句",
			SQL:    `UPDATE t_pg_rule_user u JOIN t_pg_rule_dept d SET u.status = 1 WHERE u.id = 1;`,
			Expect: true, ExpectNote: hit,
			Note: "JOIN 后未出现 ON 即命中（笛卡尔积风险）",
		},
		{
			Key: "update_delete_stmt_suggest_where", Category: "query_update", Name: "UPDATE/DELETE 必须指定 WHERE",
			SQL:    `DELETE FROM t_pg_rule_user;`,
			Expect: true, ExpectNote: hit,
			Note: "无 WHERE 条件",
		},
		{
			Key: "insert_stmt_col_name_no_dup", Category: "query_update", Name: "INSERT 字段名不能重复",
			SQL:    `INSERT INTO t_pg_rule_user (id, name, name) VALUES (900001, 'a', 'b');`,
			Expect: true, ExpectNote: hit,
			Note: "字段列表 name 重复",
		},
		{
			Key: "insert_stmt_columns_match_values", Category: "query_update", Name: "INSERT 字段与值数量匹配",
			SQL:    `INSERT INTO t_pg_rule_user (id, name, status) VALUES (900002, 'a');`,
			Expect: true, ExpectNote: hit,
			Note: "3 个字段 1 个值",
		},
		{
			Key: "insert_stmt_specify_columns", Category: "query_update", Name: "INSERT 建议指定字段列表",
			SQL:    `INSERT INTO t_pg_rule_user VALUES (900003, 'a', 'e', 0, 'c900003', now(), now());`,
			Expect: true, ExpectNote: hit,
			Note: "未指定字段列表，表结构变更后即错位",
		},
		{
			Key: "insert_stmt_not_null_column_null_value", Category: "query_update", Name: "NOT NULL 列插入 NULL",
			SQL:    `INSERT INTO t_pg_rule_user (id, name) VALUES (900004, NULL);`,
			Expect: true, ExpectNote: hit,
			Note: "name 为 NOT NULL 列，插入 NULL 必失败（PG 23502）",
		},
		{
			Key: "insert_stmt_avoid_sysdate_func", Category: "query_update", Name: "INSERT 不建议使用 SYSDATE()",
			SQL:    `INSERT INTO t_pg_rule_user (id, create_time) VALUES (900005, SYSDATE());`,
			Expect: true, ExpectNote: hit,
			Note: "SYSDATE() 取执行时刻，主从/重放不一致",
		},
		{
			Key: "limit_insert_values_row_count", Category: "query_update", Name: "INSERT 单条 VALUES 行数上限",
			SQL:    bulkInsertSQL(1001),
			Expect: true, ExpectNote: hit,
			Note: "规范 max_insert_rows=1000，构造 1001 行",
		},
		{
			Key: "insert_stmt_check_column_exists", Category: "query_update", Name: "INSERT 检测表是否存在",
			SQL:    `INSERT INTO t_pg_rule_no_such (id) VALUES (1);`,
			Expect: true, ExpectNote: hit,
			Note: "目标表不存在",
		},
		{
			Key: "update_multi_table_column_prefix_check", Category: "query_update", Name: "多表 UPDATE 的 SET 列需带表前缀",
			SQL:    `UPDATE t_pg_rule_user u SET status = 1 FROM t_pg_rule_dept d WHERE u.id = d.id;`,
			Expect: true, ExpectNote: hit,
			Note: "多表 UPDATE 中 SET status 未带前缀，歧义风险",
		},
		{
			Key: "update_stmt_avoid_update_create_time_column", Category: "query_update", Name: "UPDATE 不建议更新创建时间列",
			SQL:    `UPDATE t_pg_rule_user SET name = 'x', create_time = now() WHERE id = 1;`,
			Expect: true, ExpectNote: hit,
			Note: "SET 中出现 create_time",
		},
		{
			Key: "update_stmt_check_updates_pk", Category: "query_update", Name: "UPDATE 检测是否更新主键",
			SQL:    `UPDATE t_pg_rule_user SET id = 2 WHERE id = 1;`,
			Expect: true, ExpectNote: hit,
			Note: "SET id 命中主键列",
		},
		{
			Key: "update_stmt_suggest_update_time_column", Category: "query_update", Name: "UPDATE 建议同步更新时间列",
			SQL:    `UPDATE t_pg_rule_user SET name = 'y' WHERE id = 1;`,
			Expect: true, ExpectNote: hit,
			Note: "规范 update_time_columns=update_time，SET 未包含",
		},
		{
			Key: "update_stmt_set_separator_check", Category: "query_update", Name: "SET 多列之间用 AND 分隔",
			SQL:    `UPDATE t_pg_rule_user SET name = 'z' AND status = 1 WHERE id = 1;`,
			Expect: true, ExpectNote: hit,
			Note: "多列之间用 AND（非法语法）",
		},
		{
			Key: "update_stmt_check_column_exists", Category: "query_update", Name: "UPDATE 检测表/字段是否存在",
			SQL:    `UPDATE t_pg_rule_user SET no_such_col = 1 WHERE id = 1;`,
			Expect: true, ExpectNote: hit,
			Note: "SET 列不存在（PG 42703）",
		},
		{
			Key: "update_stmt_check_updates_unique_key", Category: "query_update", Name: "UPDATE 检测是否更新唯一键",
			SQL:    `UPDATE t_pg_rule_user SET uq_code = 'zz' WHERE id = 1;`,
			Expect: true, ExpectNote: hit,
			Note: "uq_code 上有唯一约束 uk_pg_rule_user_code",
		},
		{
			Key: "update_delete_stmt_where_subquery_check", Category: "query_update", Name: "WHERE 不建议使用子查询",
			SQL:    `DELETE FROM t_pg_rule_user WHERE id IN (SELECT id FROM t_pg_rule_dept);`,
			Expect: true, ExpectNote: hit,
			Note: "WHERE 内嵌子查询",
		},
		{
			Key: "limit_where_in_element_count", Category: "query_update", Name: "WHERE IN 元素个数上限",
			SQL:    `UPDATE t_pg_rule_user SET status = 3 WHERE id IN (1, 2, 3);`,
			Expect: true, ExpectNote: hit,
			Note: "规范 max_in_elements=2，3 个元素超限",
		},
		{
			Key: "where_avoid_leading_wildcard", Category: "query_update", Name: "不建议前通配符查找",
			SQL:    `UPDATE t_pg_rule_user SET status = 1 WHERE name LIKE '%abc';`,
			Expect: true, ExpectNote: hit,
			Note: "LIKE 以 % 开头，索引失效",
		},
		{
			Key: "where_avoid_index_col_arithmetic_or_func", Category: "query_update", Name: "索引列不做运算/函数",
			SQL:    `UPDATE t_pg_rule_user SET status = 1 WHERE id + 1 = 5;`,
			Expect: true, ExpectNote: hit,
			Note: "id 为主键索引列，WHERE 中参与算术运算",
		},
		{
			Key: "where_avoid_not_in_not_like", Category: "query_update", Name: "不建议 NOT IN / NOT LIKE",
			SQL:    `DELETE FROM t_pg_rule_user WHERE id NOT IN (1, 2);`,
			Expect: true, ExpectNote: hit,
			Note: "反向查询无法有效使用索引",
		},
		{
			Key: "where_or_condition_check", Category: "query_update", Name: "WHERE 使用 OR 连接条件",
			SQL:    `UPDATE t_pg_rule_user SET status = 1 WHERE id = 1 OR status = 1;`,
			Expect: true, ExpectNote: hit,
			Note: "OR 导致索引可能失效/结果集放大",
		},
		{
			Key: "where_like_without_wildcard", Category: "query_update", Name: "LIKE 缺少通配符",
			SQL:    `UPDATE t_pg_rule_user SET status = 1 WHERE name LIKE 'abc';`,
			Expect: true, ExpectNote: hit,
			Note: "LIKE 无 % / _，等价于等值但写法误导",
		},
	}
	return cases
}
