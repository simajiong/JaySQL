package config

// DefaultInspectParams 返回审核引擎参数的内置默认值（基线值）。
//
// 注意：这里的取值**不是**审核参数的判定条件，而是引擎参数的初始状态。
// 审核参数的最终取值只由实例绑定的 SQL 开发规范决定，判定维度只有四个：
//  1. 规范中是否存在该参数对应的规则（按 rule_key 映射）
//  2. 规则的级别是什么（must/suggest 为开启，off 为关闭）
//  3. 参数是否开启（level != off）
//  4. 参数是否属于当前数据源（rule.db_type == all 或 == 实例的 db_type）
//
// 默认值遵循两条约定：
//   - 限制/检查类参数（CHECK_*、DISABLE_*、*_MAX_*、*_KEY_PARTS 等）：默认关闭或 0/空，
//     表示“不检查 / 不限制”，只有规范中存在且开启的规则才会将其打开；
//     数值阈值 0、字符串列表空均表示不限制（引擎侧已加 >0 / len>0 守卫）。
//   - 能力类参数（是否允许某种语法或操作，规范规则不覆盖）：沿用引擎既有默认行为。
func DefaultInspectParams() InspectParams {
	return InspectParams{
		// TABLE
		MAX_TABLE_NAME_LENGTH: 0,     // 0 表示不限制
		CHECK_TABLE_COMMENT:   false, // 规则：limit_tbl_must_comment
		TABLE_COMMENT_LENGTH:  0,     // 0 表示不限制
		// 标识符基础字符集校验：表名/列名/索引名各自独立开关。
		// 历史版本三者共用一个 CHECK_IDENTIFIER，由「表名大小写」「列名大小写」两条规则写入，
		// 导致一条规则关闭后检查仍被执行（被另一条规则打开），且互相覆盖。现按检查对象拆分。
		CHECK_TABLE_IDENTIFIER:               false,   // 规则：limit_tbl_name_case
		CHECK_COLUMN_IDENTIFIER:              false,   // 规则：limit_col_name_case
		CHECK_INDEX_IDENTIFIER:               false,   // 规则：limit_uniq_idx_name_fmt / limit_normal_idx_name_fmt / index_must_have_name
		COLUMN_NAME_CASE:                     "lower", // 列名大小写要求，规则 limit_col_name_case 启用时由参数 name_case 决定（默认小写）
		TABLE_NAME_CASE:                      "lower", // 表名大小写要求，规则 limit_tbl_name_case 启用时由参数 name_case 决定（默认小写）
		CHECK_IDENTIFER_KEYWORD:              false,   // 规则：tbl_name_no_是keyword / col_name_no_是keyword
		CHECK_TABLE_CHARSET:                  false,   // 规则：limit_tbl_charset
		CHECK_TABLE_COLLATION:                false,   // 规则：limit_tbl_collation
		TABLE_SUPPORT_COLLATION:              nil,     // 规则：limit_tbl_collation；nil 表示未配置，规则启用时回退默认白名单
		TABLE_REQUIRED_COLUMNS:               nil,     // 规则：tbl_需要include_某些col；nil 表示不检查，规则启用且未配置时回退默认列
		TABLE_SUPPORT_CHARSET:                defaultTableSupportCharset(),
		CHECK_DATABASE_CHARSET:               false, // 规则：limit_库的charset
		DATABASE_SUPPORT_CHARSET:             nil,   // 规则：limit_库的charset；nil 表示未配置
		CHECK_TABLE_ENGINE:                   false, // 规则：limit_tbl_存储engine
		TABLE_SUPPORT_ENGINE:                 []string{"InnoDB", "MyISAM"},
		ENABLE_PARTITION_TABLE:               false, // 能力类：不允许分区表
		CHECK_TABLE_PRIMARY_KEY:              false, // 规则：tbl_要有pk
		CHECK_TABLE_AUDIT_TYPE_COLUMNS:       false, // 规范目录中无对应规则，默认不检查
		ENABLE_CREATE_TABLE_AS:               false, // 规则：disable_create_table_as（规则开启=禁止；规则关闭才置 true，故默认必须是禁止）
		ENABLE_CREATE_TABLE_LIKE:             false, // 规则：disable_create_table_like（口径同 CTAS）
		ENABLE_FOREIGN_KEY:                   true,  // 规则：limit_tbl_no_use_fk（开启时置为 false）
		CHECK_TABLE_AUTOINCREMENT_INIT_VALUE: false, // 规则：limit_建tbl_auto_inc_init
		TABLE_AUTOINCREMENT_INIT_VALUE:       0,     // 0 表示未配置，规则启用时取引擎默认 1
		ENABLE_CREATE_VIEW:                   true,  // 规则：disable_view_related_op（开启时置为 false）
		ENABLE_PROCEDURE_OBJECT:              true,  // 规则：sqlserver.struct.disable_proc_related_op（开启时置为 false）
		ENABLE_FUNCTION_OBJECT:               true,  // 规则：sqlserver.struct.disable_custom_function_related_op
		ENABLE_TRIGGER_OBJECT:                true,  // 规则：sqlserver.struct.disable_trigger_related_op
		INNODB_ROW_FORMAT:                    nil,   // 空表示不检查
		MAX_TABLE_COLUMNS:                    0,     // 规则：limit_tbl_col_的count，0 表示不限制
		// COLUMN
		MAX_COLUMN_NAME_LENGTH:               0,     // 0 表示不限制
		CHECK_COLUMN_CHARSET:                 false, // 规则：no_set_col_的charset
		CHECK_COLUMN_COLLATION:               false, // 规则：no_set_col_的collate
		DISABLE_COLUMN_ZEROFILL:              false, // 规则：col_禁用_zerofill_属性
		CHECK_COLUMN_COMMENT:                 false, // 规则：limit_col_要有comment
		COLUMN_MAX_CHAR_LENGTH:               0,     // 规则：limit_char_type_col_len
		MAX_VARCHAR_LENGTH:                   0,     // 规则：limit_varchar_type_col_len
		ENABLE_COLUMN_BLOB_TYPE:              true,  // 能力类
		ENABLE_COLUMN_JSON_TYPE:              true,  // 能力类
		ENABLE_COLUMN_BIT_TYPE:               true,  // 能力类
		ENABLE_COLUMN_TIMESTAMP_TYPE:         false, // 能力类
		CHECK_PRIMARYKEY_USE_BIGINT:          false, // 规则：limit_pk_col_type
		CHECK_PRIMARYKEY_USE_AUTO_INCREMENT:  false, // 规则：limit_pk_col_must_auto_inc
		PRIMARYKEY_SUPPORT_TYPES:             nil,   // 规则：limit_pk_col_type（规则开启但未配置时引擎回退 bigint/int）
		ENABLE_COLUMN_NOT_NULL:               false, // 规则：limit_col_不可空_not_null
		ENABLE_COLUMN_TIME_NULL:              true,  // 能力类：允许时间类型列可为 NULL
		CHECK_COLUMN_DEFAULT_VALUE:           false, // 规则：limit_col_要有default
		CHECK_COLUMN_FLOAT_DOUBLE:            false, // 规范目录中无对应规则，默认不检查
		CHECK_COLUMN_FORBIDDEN_TYPES:         false, // 规则：limit_col_no_use_partial_数据type
		COLUMN_FORBIDDEN_TYPES:               nil,   // 规则：limit_col_no_use_partial_数据type；nil 表示未配置，空数组表示不限制任何类型
		ENABLE_COLUMN_TYPE_CHANGE:            false, // 能力类
		ENABLE_COLUMN_TYPE_CHANGE_COMPATIBLE: true,  // 能力类
		ENABLE_COLUMN_CHANGE_COLUMN_NAME:     false, // 能力类
		// INDEX
		CHECK_UNIQ_INDEX_PREFIX:       false, // 规则：limit_uniq_idx_name_fmt
		CHECK_INDEX_NAME_REQUIRED:     false, // 规则：idx_需要set_name
		CHECK_SECONDARY_INDEX_PREFIX:  false, // 规则：limit_normal_idx_name_fmt
		CHECK_FULLTEXT_INDEX_PREFIX:   false, // 规范目录中无对应规则，默认不检查
		UNQI_INDEX_PREFIX:             "UNIQ_",
		UNIQ_INDEX_REGEX:              "", // 规则：limit_uniq_idx_name_fmt；空表示规范未配置正则，引擎回退前缀检查
		SECONDARY_INDEX_PREFIX:        "IDX_",
		SECONDARY_INDEX_REGEX:         "", // 规则：limit_normal_idx_name_fmt；空表示规范未配置正则，引擎回退前缀检查
		FULLTEXT_INDEX_PREFIX:         "FULL_",
		SECONDARY_INDEX_MAX_KEY_PARTS: 0,     // 规则：limit_单个idx_include_col_的个数
		PRIMARYKEY_MAX_KEY_PARTS:      0,     // 规则：limit_pk_include_col_的个数
		MAX_INDEX_KEYS:                0,     // 规则：limit_tbl_idx_count
		ENABLE_INDEX_RENAME:           false, // 能力类
		// true=允许冗余索引（跳过冗余索引检查）。该参数当前没有任何规范规则映射，
		// 必须默认 true，否则冗余索引检查会脱离规则开关恒开（曾导致规范全关仍报冗余索引）。
		ENABLE_REDUNDANT_INDEX: true, // 能力类：允许冗余索引（检查默认关闭）
		// ALTER
		ENABLE_DROP_COLS:              true,  // 能力类
		ENABLE_DROP_INDEXES:           true,  // 能力类
		ENABLE_DROP_PRIMARYKEY:        false, // 能力类
		ENABLE_DROP_TABLE:             true,  // 能力类
		ENABLE_TRUNCATE_TABLE:         true,  // 能力类
		ENABLE_RENAME_TABLE_NAME:      false, // 能力类
		ENABLE_MERGE_ALTER_TABLE:      true,  // 能力类
		ENABLE_TIDB_MERGE_ALTER_TABLE: false, // 能力类
		// DML
		DML_MUST_HAVE_WHERE:           false, // 规则：update_delete_stmt_suggest_指定_where_where
		CHECK_DML_MUST_USE_INDEX:      false, // 规则：update_delete_stmt_must_use_idx（默认关闭，规则启用才检查）
		DML_DISABLE_LIMIT:             false, // 规则：limit_update_delete_stmt_limit_数据量大小
		DML_DISABLE_ORDERBY:           false, // 规则：update_delete_stmt_no_有_order_by_子句
		DML_DISABLE_SUBQUERY:          false, // 规则：update_delete_stmt_detect_where_where_是否include_子sel
		CHECK_DML_JOIN_WITH_ON:        false, // 规则：update_delete_stmt_detect_多tbl_join_syntax_是否完整...
		EXPLAIN_RULE:                  "first",
		MAX_AFFECTED_ROWS:             0,  // 规则：limit_update_delete_单个stmt_总影响行数
		MAX_INSERT_ROWS:               0,  // 规则：limit_insert_stmt_一条_insert_values_的总行数
		MAX_UPDATE_DELETE_JOIN_TABLES: 0,  // 规则：limit_update_delete_join_table_count（0=不限制）
		DML_MAX_LIMIT_ROWS:            0,  // 规则：limit_update_delete_data_size（0=不限制）
		UPDATE_TIME_COLUMNS:           "", // 规则：update_stmt_suggest_update_time_column（空=不检查）
		AVOID_UPDATE_COLUMNS:          "", // 规则：update_stmt_avoid_update_create_time_column（空=不检查）
		MAX_WHERE_IN_ELEMENTS:         0,  // 规则：limit_where_in_element_count（0=不限制）
		// UPDATE 语句相关（以下均为「检查类」，默认关闭，规则启用才检查）
		CHECK_UPDATE_SET_SEPARATOR:             false, // 规则：update_stmt_set_separator_check
		CHECK_UPDATE_MULTI_TABLE_COLUMN_PREFIX: false, // 规则：update_multi_table_column_prefix_check
		CHECK_UPDATE_UPDATES_PK:                false, // 规则：update_stmt_check_updates_pk
		CHECK_UPDATE_UPDATES_UNIQUE_KEY:        false, // 规则：update_stmt_check_updates_unique_key
		CHECK_DELETE_TABLE_EXISTS:              false, // 规则：delete_stmt_check_table_exists
		CHECK_INSERT_OBJECT_EXISTS:             false, // 规则：insert_stmt_check_column_exists
		CHECK_INSERT_COLUMNS_MATCH_VALUES:      false, // 规则：insert_stmt_columns_match_values
		// WHERE 条件类（同上，默认关闭）
		CHECK_WHERE_LEADING_WILDCARD:      false, // 规则：where_avoid_leading_wildcard
		CHECK_WHERE_INDEX_COL_CALC:        false, // 规则：where_avoid_index_col_arithmetic_or_func
		CHECK_WHERE_NOT_IN_NOT_LIKE:       false, // 规则：where_avoid_not_in_not_like
		CHECK_WHERE_OR_CONDITION:          false, // 规则：where_or_condition_check
		CHECK_WHERE_IMPLICIT_CAST:         false, // 规则：where_implicit_type_cast_check
		CHECK_WHERE_LIKE_WITHOUT_WILDCARD: false, // 规则：where_like_without_wildcard
		// INSERT 语句相关（同上，默认关闭）
		CHECK_INSERT_COLUMN_NO_DUP:        false, // 规则：insert_stmt_col_name_no_dup
		CHECK_INSERT_SPECIFY_COLUMNS:      false, // 规则：insert_stmt_specify_columns
		CHECK_INSERT_NOT_NULL_COLUMN_NULL: false, // 规则：insert_stmt_not_null_column_null_value
		CHECK_INSERT_AVOID_SYSDATE:        false, // 规则：insert_stmt_avoid_sysdate_func
		DISABLE_REPLACE:                   false,
		DISABLE_INSERT_INTO_SELECT:        false,
		DISABLE_ON_DUPLICATE:              false,
	}
}

// defaultTableSupportCharset 表支持的字符集（规则未配置参数时的取值）
func defaultTableSupportCharset() []map[string]string {
	return []map[string]string{
		{"charset": "utf8", "recommend": "utf8_general_ci"},
		{"charset": "utf8mb4", "recommend": "utf8mb4_general_ci"},
	}
}
