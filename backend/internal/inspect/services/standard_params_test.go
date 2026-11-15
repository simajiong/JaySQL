package services

import (
	"encoding/json"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/models"
)

// TestDefaultInspectParamsNoRuleNoCheck 规范中不存在对应规则时，检查/限制类参数必须处于关闭状态
func TestDefaultInspectParamsNoRuleNoCheck(t *testing.T) {
	p := config.DefaultInspectParams()
	if p.ENABLE_COLUMN_NOT_NULL {
		t.Errorf("ENABLE_COLUMN_NOT_NULL should be disabled by default")
	}
	if p.CHECK_TABLE_AUDIT_TYPE_COLUMNS {
		t.Errorf("CHECK_TABLE_AUDIT_TYPE_COLUMNS should be disabled by default (no rule in catalog)")
	}
	if p.CHECK_TABLE_PRIMARY_KEY {
		t.Errorf("CHECK_TABLE_PRIMARY_KEY should be disabled by default")
	}
	if p.MAX_TABLE_NAME_LENGTH != 0 || p.MAX_VARCHAR_LENGTH != 0 || p.MAX_AFFECTED_ROWS != 0 {
		t.Errorf("numeric limits should be 0 (unlimited) by default")
	}
	if len(p.INNODB_ROW_FORMAT) != 0 {
		t.Errorf("INNODB_ROW_FORMAT should be empty by default")
	}
}

// TestApplyStandardRuleSwitch 规则开启/关闭决定引擎参数的开与关
func TestApplyStandardRuleSwitch(t *testing.T) {
	cases := []struct {
		name     string
		ruleKey  string
		level    string
		params   string
		check    func(p config.InspectParams) bool
		expected bool
	}{
		{
			name: "不可空规则开启=>启用NOT NULL检查", ruleKey: "mysql.struct.limit_col_not_null", level: "must",
			check: func(p config.InspectParams) bool { return p.ENABLE_COLUMN_NOT_NULL }, expected: true,
		},
		{
			name: "不可空规则关闭=>关闭NOT NULL检查", ruleKey: "mysql.struct.limit_col_not_null", level: "off",
			check: func(p config.InspectParams) bool { return p.ENABLE_COLUMN_NOT_NULL }, expected: false,
		},
		{
			name: "主键规则开启", ruleKey: "mysql.struct.table_must_have_pk", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_TABLE_PRIMARY_KEY }, expected: true,
		},
		{
			name: "禁止外键规则开启=>禁用外键", ruleKey: "mysql.struct.limit_tbl_no_use_fk", level: "suggest",
			check: func(p config.InspectParams) bool { return p.ENABLE_FOREIGN_KEY }, expected: false,
		},
		{
			name: "PG表注释规则开启=>启用表注释检查", ruleKey: "postgres.struct.limit_tbl_must_comment", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_TABLE_COMMENT }, expected: true,
		},
		{
			name: "PG表注释规则关闭=>关闭表注释检查", ruleKey: "postgres.struct.limit_tbl_must_comment", level: "off",
			check: func(p config.InspectParams) bool { return p.CHECK_TABLE_COMMENT }, expected: false,
		},
		{
			name: "INSERT 表/字段存在性规则开启", ruleKey: "mysql.query_update.insert_stmt_check_column_exists", level: "suggest",
			check: func(p config.InspectParams) bool { return p.CHECK_INSERT_OBJECT_EXISTS }, expected: true,
		},
		{
			name: "PG INSERT 表/字段存在性规则开启", ruleKey: "postgres.query_update.insert_stmt_check_column_exists", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_INSERT_OBJECT_EXISTS }, expected: true,
		},
		{
			name: "INSERT 表/字段存在性规则关闭", ruleKey: "mysql.query_update.insert_stmt_check_column_exists", level: "off",
			check: func(p config.InspectParams) bool { return p.CHECK_INSERT_OBJECT_EXISTS }, expected: false,
		},
		{
			name: "禁止外键规则关闭=>允许外键", ruleKey: "mysql.struct.limit_tbl_no_use_fk", level: "off",
			check: func(p config.InspectParams) bool { return p.ENABLE_FOREIGN_KEY }, expected: true,
		},
		{
			name: "varchar 长度规则开启且配置阈值", ruleKey: "mysql.struct.limit_varchar_type_col_len", level: "suggest", params: `{"max_length": 500}`,
			check: func(p config.InspectParams) bool { return p.MAX_VARCHAR_LENGTH == 500 }, expected: true,
		},
		{
			name: "varchar 长度规则开启但未配置阈值=>取引擎默认", ruleKey: "mysql.struct.limit_varchar_type_col_len", level: "suggest",
			check: func(p config.InspectParams) bool { return p.MAX_VARCHAR_LENGTH == defaultMaxVarcharLength }, expected: true,
		},
		{
			name: "varchar 长度规则关闭=>不限制", ruleKey: "mysql.struct.limit_varchar_type_col_len", level: "off",
			check: func(p config.InspectParams) bool { return p.MAX_VARCHAR_LENGTH == 0 }, expected: true,
		},
		{
			name: "索引数量规则开启=>取引擎默认阈值", ruleKey: "mysql.struct.limit_tbl_idx_count", level: "suggest",
			check: func(p config.InspectParams) bool { return p.MAX_INDEX_KEYS == defaultMaxIndexKeys }, expected: true,
		},
		{
			name: "自增初始值规则开启且配置阈值", ruleKey: "mysql.struct.limit_create_table_auto_inc_init", level: "must", params: `{"auto_inc_init_value": 100}`,
			check: func(p config.InspectParams) bool { return p.TABLE_AUTOINCREMENT_INIT_VALUE == 100 }, expected: true,
		},
		{
			name: "自增初始值规则开启但未配置阈值=>取引擎默认1", ruleKey: "mysql.struct.limit_create_table_auto_inc_init", level: "must",
			check: func(p config.InspectParams) bool { return p.TABLE_AUTOINCREMENT_INIT_VALUE == defaultAutoIncInitValue }, expected: true,
		},
		{
			name: "自增初始值规则关闭=>不限制", ruleKey: "mysql.struct.limit_create_table_auto_inc_init", level: "off",
			check: func(p config.InspectParams) bool { return p.TABLE_AUTOINCREMENT_INIT_VALUE == 0 }, expected: true,
		},
		{
			name: "字段名大小写规则开启且配置大写", ruleKey: "mysql.struct.limit_col_name_case", level: "must", params: `{"name_case": "upper"}`,
			check: func(p config.InspectParams) bool { return p.COLUMN_NAME_CASE == "upper" }, expected: true,
		},
		{
			name: "字段名大小写规则开启未配置=>默认小写", ruleKey: "mysql.struct.limit_col_name_case", level: "must",
			check: func(p config.InspectParams) bool { return p.COLUMN_NAME_CASE == "lower" }, expected: true,
		},
		{
			name: "字段名大小写规则开启配置非法值=>回退小写", ruleKey: "postgres.struct.limit_col_name_case", level: "must", params: `{"name_case": "mixed"}`,
			check: func(p config.InspectParams) bool { return p.COLUMN_NAME_CASE == "lower" }, expected: true,
		},
		{
			name: "字段名大小写规则关闭=>小写（检查不生效）", ruleKey: "mysql.struct.limit_col_name_case", level: "off",
			check: func(p config.InspectParams) bool { return !p.CHECK_COLUMN_IDENTIFIER && p.COLUMN_NAME_CASE == "lower" }, expected: true,
		},
		{
			name: "表名大小写规则开启未配置=>默认小写（独立于列名参数）", ruleKey: "mysql.struct.limit_tbl_name_case", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_TABLE_IDENTIFIER && p.TABLE_NAME_CASE == "lower" }, expected: true,
		},
		{
			name: "表名大小写规则开启且配置大写", ruleKey: "postgres.struct.limit_tbl_name_case", level: "must", params: `{"name_case": "upper"}`,
			check: func(p config.InspectParams) bool { return p.TABLE_NAME_CASE == "upper" }, expected: true,
		},
		{
			name: "必含列规则开启未配置=>回退默认列(create_time,update_time)", ruleKey: "mysql.struct.table_must_include_required_columns", level: "must",
			check: func(p config.InspectParams) bool {
				return len(p.TABLE_REQUIRED_COLUMNS) == 2 && p.TABLE_REQUIRED_COLUMNS[0] == "create_time" && p.TABLE_REQUIRED_COLUMNS[1] == "update_time"
			}, expected: true,
		},
		{
			name: "表排序规则规则开启未配置=>回退默认白名单", ruleKey: "mysql.struct.limit_tbl_collation", level: "must",
			check: func(p config.InspectParams) bool {
				return p.CHECK_TABLE_COLLATION && len(p.TABLE_SUPPORT_COLLATION) == 4
			}, expected: true,
		},
		{
			name: "表排序规则规则开启且配置白名单", ruleKey: "mysql.struct.limit_tbl_collation", level: "must", params: `{"collations": ["utf8mb4_0900_ai_ci"]}`,
			check: func(p config.InspectParams) bool {
				return p.CHECK_TABLE_COLLATION && len(p.TABLE_SUPPORT_COLLATION) == 1 && p.TABLE_SUPPORT_COLLATION[0] == "utf8mb4_0900_ai_ci"
			}, expected: true,
		},
		{
			name: "表排序规则规则关闭=>白名单清空", ruleKey: "mysql.struct.limit_tbl_collation", level: "off",
			check: func(p config.InspectParams) bool {
				return !p.CHECK_TABLE_COLLATION && p.TABLE_SUPPORT_COLLATION == nil
			}, expected: true,
		},
		{
			name: "必含列规则开启未配置=>回退默认列", ruleKey: "mysql.struct.table_must_include_required_columns", level: "must",
			check: func(p config.InspectParams) bool {
				return len(p.TABLE_REQUIRED_COLUMNS) == 2 && p.TABLE_REQUIRED_COLUMNS[0] == "create_time"
			}, expected: true,
		},
		{
			name: "必含列规则开启且配置列列表", ruleKey: "postgres.struct.table_must_include_required_columns", level: "must", params: `{"columns": ["id", "created_at"]}`,
			check: func(p config.InspectParams) bool {
				return len(p.TABLE_REQUIRED_COLUMNS) == 2 && p.TABLE_REQUIRED_COLUMNS[1] == "created_at"
			}, expected: true,
		},
		{
			name: "必含列规则配置空数组=>开启但不要求任何列", ruleKey: "mysql.struct.table_must_include_required_columns", level: "must", params: `{"columns": []}`,
			check: func(p config.InspectParams) bool {
				return p.TABLE_REQUIRED_COLUMNS != nil && len(p.TABLE_REQUIRED_COLUMNS) == 0
			}, expected: true,
		},
		{
			name: "必含列规则关闭=>不检查", ruleKey: "mysql.struct.table_must_include_required_columns", level: "off",
			check: func(p config.InspectParams) bool { return p.TABLE_REQUIRED_COLUMNS == nil }, expected: true,
		},
		{
			name: "索引命名规则开启=>显式命名检查联动开启", ruleKey: "mysql.struct.index_must_have_name", level: "must",
			check: func(p config.InspectParams) bool {
				return p.CHECK_INDEX_NAME_REQUIRED && p.CHECK_UNIQ_INDEX_PREFIX && p.CHECK_SECONDARY_INDEX_PREFIX
			}, expected: true,
		},
		{
			name: "索引命名规则关闭=>显式命名检查关闭", ruleKey: "postgres.struct.index_must_have_name", level: "off",
			check: func(p config.InspectParams) bool { return !p.CHECK_INDEX_NAME_REQUIRED }, expected: true,
		},
		{
			name: "多条ALTER合并规则开启=>合并检查开启", ruleKey: "mysql.struct.merge_multiple_alter_stmt_same_table", level: "suggest",
			check: func(p config.InspectParams) bool { return p.ENABLE_MERGE_ALTER_TABLE }, expected: true,
		},
		{
			name: "多条ALTER合并规则关闭=>合并检查关闭", ruleKey: "mysql.struct.merge_multiple_alter_stmt_same_table", level: "off",
			check: func(p config.InspectParams) bool { return !p.ENABLE_MERGE_ALTER_TABLE }, expected: true,
		},
		{
			name: "普通索引名格式规则开启未配正则=>回退默认idx1_\\w+", ruleKey: "mysql.struct.limit_normal_idx_name_fmt", level: "suggest",
			check: func(p config.InspectParams) bool {
				return p.CHECK_SECONDARY_INDEX_PREFIX && p.SECONDARY_INDEX_REGEX == "idx1_\\w+"
			}, expected: true,
		},
		{
			name: "普通索引名格式规则开启且配置正则", ruleKey: "postgres.struct.limit_normal_idx_name_fmt", level: "suggest", params: `{"regex": "^idx_"}`,
			check: func(p config.InspectParams) bool { return p.SECONDARY_INDEX_REGEX == "^idx_" }, expected: true,
		},
		{
			name: "普通索引名格式规则配置非法正则=>回退默认", ruleKey: "mysql.struct.limit_normal_idx_name_fmt", level: "suggest", params: `{"regex": "idx_["}`,
			check: func(p config.InspectParams) bool { return p.SECONDARY_INDEX_REGEX == "idx1_\\w+" }, expected: true,
		},
		{
			name: "普通索引名格式规则关闭=>正则清空", ruleKey: "mysql.struct.limit_normal_idx_name_fmt", level: "off",
			check: func(p config.InspectParams) bool {
				return !p.CHECK_SECONDARY_INDEX_PREFIX && p.SECONDARY_INDEX_REGEX == ""
			}, expected: true,
		},
		{
			name: "主键列类型规则开启未配置=>白名单为空(不限制类型)", ruleKey: "mysql.struct.limit_pk_col_type", level: "must",
			check: func(p config.InspectParams) bool {
				return p.CHECK_PRIMARYKEY_USE_BIGINT && len(p.PRIMARYKEY_SUPPORT_TYPES) == 0
			}, expected: true,
		},
		{
			name: "主键列类型规则开启且配置白名单", ruleKey: "postgres.struct.limit_pk_col_type", level: "must", params: `{"pk_types": ["bigint"]}`,
			check: func(p config.InspectParams) bool {
				return len(p.PRIMARYKEY_SUPPORT_TYPES) == 1 && p.PRIMARYKEY_SUPPORT_TYPES[0] == "bigint"
			}, expected: true,
		},
		{
			name: "主键列类型规则关闭=>白名单清空", ruleKey: "mysql.struct.limit_pk_col_type", level: "off",
			check: func(p config.InspectParams) bool {
				return !p.CHECK_PRIMARYKEY_USE_BIGINT && p.PRIMARYKEY_SUPPORT_TYPES == nil
			}, expected: true,
		},
		{
			name: "单索引列数规则开启未配置=>回退默认2", ruleKey: "mysql.struct.limit_single_index_column_count", level: "must",
			check: func(p config.InspectParams) bool {
				return p.SECONDARY_INDEX_MAX_KEY_PARTS == defaultSecondaryKeyParts && defaultSecondaryKeyParts == 2
			}, expected: true,
		},
		{
			name: "表字符集规则开启未配置=>回退默认utf8mb4/utf8", ruleKey: "mysql.struct.limit_tbl_charset", level: "must",
			check: func(p config.InspectParams) bool {
				return p.CHECK_TABLE_CHARSET && len(p.TABLE_SUPPORT_CHARSET) == 2 &&
					p.TABLE_SUPPORT_CHARSET[0]["charset"] == "utf8mb4" && p.TABLE_SUPPORT_CHARSET[1]["charset"] == "utf8"
			}, expected: true,
		},
		{
			name: "影响行数规则开启未配置=>回退默认10000", ruleKey: "mysql.query_update.limit_update_delete_affected_rows", level: "must",
			check: func(p config.InspectParams) bool {
				return p.MAX_AFFECTED_ROWS == defaultMaxAffectedRows && defaultMaxAffectedRows == 10000
			}, expected: true,
		},
		{
			name: "影响行数规则开启且配置阈值", ruleKey: "mysql.query_update.limit_update_delete_affected_rows", level: "must", params: `{"max_affected_rows": 50000}`,
			check: func(p config.InspectParams) bool { return p.MAX_AFFECTED_ROWS == 50000 }, expected: true,
		},
		{
			name: "影响行数规则关闭=>不限制", ruleKey: "mysql.query_update.limit_update_delete_affected_rows", level: "off",
			check: func(p config.InspectParams) bool { return p.MAX_AFFECTED_ROWS == 0 }, expected: true,
		},
		{
			name: "PG影响行数规则开启且配置阈值", ruleKey: "postgres.query_update.limit_update_delete_affected_rows", level: "must", params: `{"max_affected_rows": 20000}`,
			check: func(p config.InspectParams) bool { return p.MAX_AFFECTED_ROWS == 20000 }, expected: true,
		},
		{
			name: "PG影响行数规则开启未配置=>回退默认10000", ruleKey: "postgres.query_update.limit_update_delete_affected_rows", level: "suggest",
			check: func(p config.InspectParams) bool { return p.MAX_AFFECTED_ROWS == defaultMaxAffectedRows }, expected: true,
		},
		{
			name: "PG影响行数规则关闭=>不限制", ruleKey: "postgres.query_update.limit_update_delete_affected_rows", level: "off",
			check: func(p config.InspectParams) bool { return p.MAX_AFFECTED_ROWS == 0 }, expected: true,
		},
		{
			name: "PG LIMIT行数规则开启且配置阈值", ruleKey: "postgres.query_update.limit_update_delete_data_size", level: "suggest", params: `{"max_limit_rows": 500}`,
			check: func(p config.InspectParams) bool { return p.DML_MAX_LIMIT_ROWS == 500 }, expected: true,
		},
		{
			name: "PG LIMIT行数规则关闭=>不限制", ruleKey: "postgres.query_update.limit_update_delete_data_size", level: "off",
			check: func(p config.InspectParams) bool { return p.DML_MAX_LIMIT_ROWS == 0 }, expected: true,
		},
		{
			name: "INSERT行数规则开启未配置=>回退默认1000", ruleKey: "postgres.query_update.limit_insert_values_row_count", level: "suggest",
			check: func(p config.InspectParams) bool {
				return p.MAX_INSERT_ROWS == defaultMaxInsertRows && defaultMaxInsertRows == 1000
			}, expected: true,
		},
		{
			name: "INSERT行数规则开启且配置阈值", ruleKey: "mysql.query_update.limit_insert_values_row_count", level: "suggest", params: `{"max_insert_rows": 500}`,
			check: func(p config.InspectParams) bool { return p.MAX_INSERT_ROWS == 500 }, expected: true,
		},
		{
			name: "INSERT行数规则关闭=>不限制", ruleKey: "postgres.query_update.limit_insert_values_row_count", level: "off",
			check: func(p config.InspectParams) bool { return p.MAX_INSERT_ROWS == 0 }, expected: true,
		},
		{
			name: "多表关联规则开启未配置=>回退默认5", ruleKey: "mysql.query_update.limit_update_delete_join_table_count", level: "suggest",
			check: func(p config.InspectParams) bool {
				return p.MAX_UPDATE_DELETE_JOIN_TABLES == defaultDMLJoinTables && defaultDMLJoinTables == 5
			}, expected: true,
		},
		{
			name: "多表关联规则开启且配置阈值", ruleKey: "postgres.query_update.limit_update_delete_join_table_count", level: "must", params: `{"max_join_tables": 3}`,
			check: func(p config.InspectParams) bool { return p.MAX_UPDATE_DELETE_JOIN_TABLES == 3 }, expected: true,
		},
		{
			name: "多表关联规则关闭=>不限制", ruleKey: "mysql.query_update.limit_update_delete_join_table_count", level: "off",
			check: func(p config.InspectParams) bool { return p.MAX_UPDATE_DELETE_JOIN_TABLES == 0 }, expected: true,
		},
		{
			name: "LIMIT数据量规则开启未配置=>回退默认5", ruleKey: "mysql.query_update.limit_update_delete_data_size", level: "suggest",
			check: func(p config.InspectParams) bool {
				return p.DML_MAX_LIMIT_ROWS == defaultDMLLimitRows && defaultDMLLimitRows == 5 && !p.DML_DISABLE_LIMIT
			}, expected: true,
		},
		{
			name: "LIMIT数据量规则开启且配置阈值", ruleKey: "mysql.query_update.limit_update_delete_data_size", level: "must", params: `{"max_limit_rows": 100}`,
			check: func(p config.InspectParams) bool { return p.DML_MAX_LIMIT_ROWS == 100 }, expected: true,
		},
		{
			name: "LIMIT数据量规则关闭=>不限制", ruleKey: "mysql.query_update.limit_update_delete_data_size", level: "off",
			check: func(p config.InspectParams) bool { return p.DML_MAX_LIMIT_ROWS == 0 }, expected: true,
		},
		{
			name: "修改时间列规则开启未配置=>回退默认update_time", ruleKey: "mysql.query_update.update_stmt_suggest_update_time_column", level: "suggest",
			check: func(p config.InspectParams) bool {
				return p.UPDATE_TIME_COLUMNS == defaultUpdateTimeColumns && defaultUpdateTimeColumns == "update_time"
			}, expected: true,
		},
		{
			name: "修改时间列规则开启且配置候选列", ruleKey: "postgres.query_update.update_stmt_suggest_update_time_column", level: "suggest", params: `{"update_time_columns": "update_time,modified_at"}`,
			check: func(p config.InspectParams) bool { return p.UPDATE_TIME_COLUMNS == "update_time,modified_at" }, expected: true,
		},
		{
			name: "修改时间列规则关闭=>不检查", ruleKey: "mysql.query_update.update_stmt_suggest_update_time_column", level: "off",
			check: func(p config.InspectParams) bool { return p.UPDATE_TIME_COLUMNS == "" }, expected: true,
		},
		{
			name: "IN元素个数规则开启未配置=>回退默认1000", ruleKey: "mysql.query_update.limit_where_in_element_count", level: "suggest",
			check: func(p config.InspectParams) bool {
				return p.MAX_WHERE_IN_ELEMENTS == defaultMaxWhereInElements && defaultMaxWhereInElements == 1000
			}, expected: true,
		},
		{
			name: "IN元素个数规则开启且配置阈值", ruleKey: "postgres.query_update.limit_where_in_element_count", level: "must", params: `{"max_in_elements": 500}`,
			check: func(p config.InspectParams) bool { return p.MAX_WHERE_IN_ELEMENTS == 500 }, expected: true,
		},
		{
			name: "IN元素个数规则关闭=>不限制", ruleKey: "mysql.query_update.limit_where_in_element_count", level: "off",
			check: func(p config.InspectParams) bool { return p.MAX_WHERE_IN_ELEMENTS == 0 }, expected: true,
		},
		// UPDATE/DELETE 必须使用索引（update_delete_stmt_must_use_idx）：布尔开关型规则
		{
			name: "必须使用索引规则开启=>检查打开", ruleKey: "mysql.query_update.update_delete_stmt_must_use_idx", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_DML_MUST_USE_INDEX }, expected: true,
		},
		{
			name: "必须使用索引规则建议级=>检查同样打开", ruleKey: "mysql.query_update.update_delete_stmt_must_use_idx", level: "suggest",
			check: func(p config.InspectParams) bool { return p.CHECK_DML_MUST_USE_INDEX }, expected: true,
		},
		{
			name: "必须使用索引规则关闭=>不检查", ruleKey: "mysql.query_update.update_delete_stmt_must_use_idx", level: "off",
			check: func(p config.InspectParams) bool { return !p.CHECK_DML_MUST_USE_INDEX }, expected: true,
		},
		// INSERT / UPDATE 语法语义类规则（2026-09-29 新增实现）：布尔开关型
		{
			name: "INSERT字段重复规则开启", ruleKey: "mysql.query_update.insert_stmt_col_name_no_dup", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_INSERT_COLUMN_NO_DUP }, expected: true,
		},
		{
			name: "INSERT字段重复规则关闭", ruleKey: "mysql.query_update.insert_stmt_col_name_no_dup", level: "off",
			check: func(p config.InspectParams) bool { return !p.CHECK_INSERT_COLUMN_NO_DUP }, expected: true,
		},
		{
			name: "INSERT指定字段列表规则开启", ruleKey: "mysql.query_update.insert_stmt_specify_columns", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_INSERT_SPECIFY_COLUMNS }, expected: true,
		},
		{
			name: "INSERT指定字段列表规则关闭", ruleKey: "mysql.query_update.insert_stmt_specify_columns", level: "off",
			check: func(p config.InspectParams) bool { return !p.CHECK_INSERT_SPECIFY_COLUMNS }, expected: true,
		},
		{
			name: "INSERT为NOT NULL列插入NULL规则开启", ruleKey: "mysql.query_update.insert_stmt_not_null_column_null_value", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_INSERT_NOT_NULL_COLUMN_NULL }, expected: true,
		},
		{
			name: "INSERT禁用SYSDATE规则开启", ruleKey: "mysql.query_update.insert_stmt_avoid_sysdate_func", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_INSERT_AVOID_SYSDATE }, expected: true,
		},
		{
			name: "INSERT字段值匹配规则开启", ruleKey: "mysql.query_update.insert_stmt_columns_match_values", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_INSERT_COLUMNS_MATCH_VALUES }, expected: true,
		},
		{
			name: "INSERT字段值匹配规则关闭", ruleKey: "mysql.query_update.insert_stmt_columns_match_values", level: "off",
			check: func(p config.InspectParams) bool { return !p.CHECK_INSERT_COLUMNS_MATCH_VALUES }, expected: true,
		},
		{
			name: "UPDATE多表前缀规则开启", ruleKey: "mysql.query_update.update_multi_table_column_prefix_check", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_UPDATE_MULTI_TABLE_COLUMN_PREFIX }, expected: true,
		},
		{
			name: "UPDATE更新主键规则开启", ruleKey: "mysql.query_update.update_stmt_check_updates_pk", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_UPDATE_UPDATES_PK }, expected: true,
		},
		{
			name: "UPDATE的SET分隔符规则开启", ruleKey: "mysql.query_update.update_stmt_set_separator_check", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_UPDATE_SET_SEPARATOR }, expected: true,
		},
		{
			name: "PG的SET分隔符规则开启", ruleKey: "postgres.query_update.update_stmt_set_separator_check", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_UPDATE_SET_SEPARATOR }, expected: true,
		},
		{
			name: "UPDATE更新唯一键规则开启", ruleKey: "mysql.query_update.update_stmt_check_updates_unique_key", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_UPDATE_UPDATES_UNIQUE_KEY }, expected: true,
		},
		{
			name: "UPDATE更新唯一键规则关闭", ruleKey: "mysql.query_update.update_stmt_check_updates_unique_key", level: "off",
			check: func(p config.InspectParams) bool { return !p.CHECK_UPDATE_UPDATES_UNIQUE_KEY }, expected: true,
		},
		{
			name: "WHERE前通配符规则开启", ruleKey: "mysql.query_update.where_avoid_leading_wildcard", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_WHERE_LEADING_WILDCARD }, expected: true,
		},
		{
			name: "WHERE索引列运算规则开启", ruleKey: "mysql.query_update.where_avoid_index_col_arithmetic_or_func", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_WHERE_INDEX_COL_CALC }, expected: true,
		},
		{
			name: "WHERE反向查询规则关闭", ruleKey: "mysql.query_update.where_avoid_not_in_not_like", level: "off",
			check: func(p config.InspectParams) bool { return !p.CHECK_WHERE_NOT_IN_NOT_LIKE }, expected: true,
		},
		{
			name: "WHERE的OR规则开启", ruleKey: "mysql.query_update.where_or_condition_check", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_WHERE_OR_CONDITION }, expected: true,
		},
		{
			name: "WHERE隐式转换规则开启", ruleKey: "mysql.query_update.where_implicit_type_cast_check", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_WHERE_IMPLICIT_CAST }, expected: true,
		},
		{
			name: "WHERE无通配符LIKE规则开启", ruleKey: "mysql.query_update.where_like_without_wildcard", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_WHERE_LIKE_WITHOUT_WILDCARD }, expected: true,
		},
		{
			name: "PG的WHERE前通配符规则开启", ruleKey: "postgres.query_update.where_avoid_leading_wildcard", level: "must",
			check: func(p config.InspectParams) bool { return p.CHECK_WHERE_LEADING_WILDCARD }, expected: true,
		},
	}

	for _, c := range cases {
		params := config.DefaultInspectParams()
		rule := models.InsightSQLStandardRule{RuleKey: c.ruleKey, Level: c.level}
		if c.params != "" {
			rule.Params = json.RawMessage(c.params)
		}
		applyStandardRule(&params, rule)
		if got := c.check(params); got != c.expected {
			t.Errorf("%s: got=%v want=%v", c.name, got, c.expected)
		}
	}
}

// TestBuildInspectParamsRuleOrder 同一参数被多条规则映射时：全部关闭才会关闭，任一开启即开启
func TestBuildInspectParamsRuleOrder(t *testing.T) {
	rules := []models.InsightSQLStandardRule{
		{RuleKey: "mysql.struct.limit_col_must_have_comment", Level: "off"},
		{RuleKey: "mysql.struct.limit_col_not_null", Level: "off"},
		{RuleKey: "mysql.struct.table_must_have_pk", Level: "must"},
	}
	params := config.DefaultInspectParams()
	for _, rule := range rules {
		if rule.Level == "off" {
			applyStandardRule(&params, rule)
		}
	}
	for _, rule := range rules {
		if rule.Level != "off" {
			applyStandardRule(&params, rule)
		}
	}
	if params.CHECK_COLUMN_COMMENT {
		t.Errorf("CHECK_COLUMN_COMMENT should stay disabled when its rule is off")
	}
	if params.ENABLE_COLUMN_NOT_NULL {
		t.Errorf("ENABLE_COLUMN_NOT_NULL should stay disabled when its rule is off")
	}
	if !params.CHECK_TABLE_PRIMARY_KEY {
		t.Errorf("CHECK_TABLE_PRIMARY_KEY should be enabled when its rule is must")
	}
}

// TestApplyStandardRuleKeywordParamsNoClobber 表名/字段名两条关键字规则共用 IDENTIFIER_KEYWORDS 参数：
// 后应用的规则未配置 keywords 时不得清掉先应用规则已写入的配置（2026-09-29 修复）。
func TestApplyStandardRuleKeywordParamsNoClobber(t *testing.T) {
	mk := func(key string, level string, params string) models.InsightSQLStandardRule {
		return models.InsightSQLStandardRule{RuleKey: key, Level: level, Params: json.RawMessage(params)}
	}
	// 场景1：col 规则配置了列表，tbl 规则未配置（后应用）→ 配置必须保留
	p := config.DefaultInspectParams()
	applyStandardRule(&p, mk("mysql.struct.col_name_no_keyword", "must", `{"keywords": ["SELECT", "USER"]}`))
	applyStandardRule(&p, mk("mysql.struct.tbl_name_no_keyword", "must", `{}`))
	if !p.CHECK_IDENTIFER_KEYWORD || len(p.IDENTIFIER_KEYWORDS) != 2 {
		t.Fatalf("col 配置的关键字列表被 tbl 规则覆盖: check=%v kws=%v", p.CHECK_IDENTIFER_KEYWORD, p.IDENTIFIER_KEYWORDS)
	}
	// 场景2：全部关闭 → 列表清空（引擎回退默认全集）
	p2 := config.DefaultInspectParams()
	applyStandardRule(&p2, mk("mysql.struct.col_name_no_keyword", "off", `{}`))
	applyStandardRule(&p2, mk("postgres.struct.tbl_name_no_keyword", "off", `{}`))
	if p2.CHECK_IDENTIFER_KEYWORD || p2.IDENTIFIER_KEYWORDS != nil {
		t.Fatalf("关闭后参数应复位: check=%v kws=%v", p2.CHECK_IDENTIFER_KEYWORD, p2.IDENTIFIER_KEYWORDS)
	}
}

// 限制冗余索引（limit_tbl_redundant_idx）：规则开启 => 不允许冗余（引擎执行检查）；
// 规则关闭 => 允许冗余（跳过检查）。参数 ENABLE_REDUNDANT_INDEX 语义为「允许冗余」，
// 与规则开关方向相反，极易改错，此处锁定口径。
func TestApplyStandardRuleRedundantIndex(t *testing.T) {
	mk := func(key, level string) models.InsightSQLStandardRule {
		return models.InsightSQLStandardRule{RuleKey: key, Level: level, Params: json.RawMessage(`{}`)}
	}
	for _, key := range []string{"mysql.struct.limit_tbl_redundant_idx", "postgres.struct.limit_tbl_redundant_idx"} {
		// 开启（must / suggest 均视为开启）→ 不允许冗余 → 检查执行
		for _, level := range []string{"must", "suggest"} {
			p := config.DefaultInspectParams()
			applyStandardRule(&p, mk(key, level))
			if p.ENABLE_REDUNDANT_INDEX {
				t.Errorf("%s level=%s 应不允许冗余（ENABLE_REDUNDANT_INDEX=false），实际 true", key, level)
			}
		}
		// 关闭 → 允许冗余 → 跳过检查
		p := config.DefaultInspectParams()
		applyStandardRule(&p, mk(key, "off"))
		if !p.ENABLE_REDUNDANT_INDEX {
			t.Errorf("%s level=off 应允许冗余（ENABLE_REDUNDANT_INDEX=true），实际 false", key)
		}
	}
}

// TestApplyStandardRuleAvoidUpdateColumns UPDATE 不建议更新「创建时间」列的参数装配。
//
// 列名由规范显式配置（create_time_columns），未配置时回退默认 create_time；规则关闭 => 空（不检查）。
// 说明：INSERT 字段值匹配 / INSERT 表字段存在 / UPDATE 表字段存在 三条规则已下线，对应用例已删除。
func TestApplyStandardRuleAvoidUpdateColumns(t *testing.T) {
	mk := func(key, level, params string) models.InsightSQLStandardRule {
		return models.InsightSQLStandardRule{RuleKey: key, Level: level, Params: json.RawMessage(params)}
	}
	for _, key := range []string{
		"mysql.query_update.update_stmt_avoid_update_create_time_column",
		"postgres.query_update.update_stmt_avoid_update_create_time_column",
	} {
		// 开启且未配置 => 回退默认 create_time
		p := config.DefaultInspectParams()
		applyStandardRule(&p, mk(key, "must", `{}`))
		if p.AVOID_UPDATE_COLUMNS != "create_time" {
			t.Errorf("%s 未配置列时应回退 create_time，实际 %q", key, p.AVOID_UPDATE_COLUMNS)
		}
		// 开启且配置了自定义列 => 取配置值
		p = config.DefaultInspectParams()
		applyStandardRule(&p, mk(key, "suggest", `{"create_time_columns":"gmt_create,created_at"}`))
		if p.AVOID_UPDATE_COLUMNS != "gmt_create,created_at" {
			t.Errorf("%s 应取配置的列，实际 %q", key, p.AVOID_UPDATE_COLUMNS)
		}
		// 关闭 => 空（不检查）
		p = config.DefaultInspectParams()
		applyStandardRule(&p, mk(key, "off", `{"create_time_columns":"gmt_create"}`))
		if p.AVOID_UPDATE_COLUMNS != "" {
			t.Errorf("%s level=off 应不检查，实际 %q", key, p.AVOID_UPDATE_COLUMNS)
		}
	}
}
