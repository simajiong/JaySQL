package services

import (
	"strings"
	"testing"
)

// SQLServer 种子一致性守门测试（与 Oracle/PG 同一套红线）。
//
//  1. TestSQLServerSeedCoversEngineKeys：SQL Server 引擎产出的每个 rule_key 后缀
//     （checker/sqlserver_*.go）都必须在种子目录里有对应规则，否则预审结论会挂到
//     同致命类别的任意规则上（张冠李戴）。
//  2. TestSQLServerSeedNoGhostRules：种子里全部 sqlserver.* 规则都必须是引擎确实
//     会产出的 key，否则规范页面照常显示、引擎永不触发（幽灵规则）。
//  3. TestSQLServerSeedRuleTypeValid：RuleType 必须是合法取值（前端按它做筛选）。
//  4. TestSQLServerSeedLevelAlignedWithMySQL：基线严重级别必须与 MySQL 对齐，
//     避免同一个规范项在不同库上给出不同等级。

// sqlServerEngineEmittedKeys SQL Server 引擎产出的 rule_key 后缀全集
// （来源：checker/sqlserver_gate.go / sqlserver_rules.go / sqlserver_dml.go / sqlserver_object_rules.go）。
var sqlServerEngineEmittedKeys = []string{
	// 准入闸门
	"disable_create_table_as", // CREATE TABLE AS / SELECT ... INTO
	"disable_view_related_op",
	"disable_proc_related_op",
	"disable_custom_function_related_op",
	"disable_trigger_related_op",
	// 对象存在性（结构侧）
	"create_tbl_must_not_exist",
	"obj_tbl_must_exist",
	"obj_tbl_must_not_exist",
	"obj_col_must_exist",
	"obj_col_must_not_exist",
	"obj_idx_must_exist",
	"obj_idx_must_not_exist",
	"obj_view_must_not_exist",
	// 结构：表
	"table_must_have_pk",
	"limit_tbl_must_comment",
	"limit_table_column_count",
	"table_must_include_required_columns",
	"limit_tbl_name_case",
	"tbl_name_no_keyword",
	"limit_tbl_no_use_fk",
	"merge_multiple_alter_stmt_same_table",
	// 结构：列
	"limit_auto_inc_col_name_is_id",
	"limit_create_table_auto_inc_init",
	"limit_char_type_col_len",
	"limit_varchar_type_col_len",
	"limit_col_name_case",
	"col_name_no_keyword",
	"limit_col_must_have_comment",
	"limit_col_must_have_default",
	"limit_col_not_null",
	"limit_col_no_partial_data_type",
	"no_column_collate",
	// 结构：索引 / 主键
	"limit_uniq_idx_name_fmt",
	"limit_normal_idx_name_fmt",
	"index_must_have_name",
	"index_name_no_duplicate",
	"limit_pk_col_must_auto_inc",
	"limit_pk_column_count",
	"limit_pk_col_type",
	"limit_single_index_column_count",
	"limit_tbl_idx_count",
	"limit_tbl_redundant_idx",
	// DML：通用
	"limit_update_delete_affected_rows",
	"delete_stmt_check_table_exists",
	"limit_update_delete_join_table_count",
	"update_delete_stmt_join_on_check",
	"limit_update_delete_data_size",
	"update_delete_stmt_suggest_where",
	"update_delete_stmt_must_use_idx",
	"update_delete_stmt_no_order_by_clause",
	"update_delete_stmt_where_subquery_check",
	"limit_where_in_element_count",
	// DML：UPDATE
	"update_multi_table_column_prefix_check",
	"update_stmt_avoid_update_create_time_column",
	"update_stmt_check_updates_pk",
	"update_stmt_check_updates_unique_key",
	"update_stmt_suggest_update_time_column",
	"update_stmt_set_separator_check",
	"update_stmt_check_column_exists",
	// DML：INSERT
	"insert_stmt_col_name_no_dup",
	"insert_stmt_columns_match_values",
	"insert_stmt_specify_columns",
	"insert_stmt_not_null_column_null_value",
	"insert_stmt_avoid_sysdate_func",
	"insert_stmt_check_column_exists",
	"limit_insert_values_row_count",
	// DML：WHERE 条件
	"where_avoid_leading_wildcard",
	"where_avoid_index_col_arithmetic_or_func",
	"where_avoid_not_in_not_like",
	"where_or_condition_check",
	"where_implicit_type_cast_check",
	"where_like_without_wildcard",
}

// sqlServerRuleSuffix 把 sqlserver.struct.xxx / sqlserver.query_update.xxx 归一成 xxx
func sqlServerRuleSuffix(ruleKey string) string {
	key := strings.TrimPrefix(ruleKey, "sqlserver.")
	key = strings.TrimPrefix(key, "struct.")
	key = strings.TrimPrefix(key, "query_update.")
	return key
}

func TestSQLServerSeedCoversEngineKeys(t *testing.T) {
	idx := make(map[string]bool)
	for _, r := range defaultStandardRules() {
		if strings.HasPrefix(r.RuleKey, "sqlserver.") {
			idx[sqlServerRuleSuffix(r.RuleKey)] = true
		}
	}
	for _, key := range sqlServerEngineEmittedKeys {
		if !idx[key] {
			t.Errorf("引擎产出 key %q 不在 SQLServer 种子目录中：预审结论会走致命类别兜底（张冠李戴），请补种子", key)
		}
	}
}

func TestSQLServerSeedNoGhostRules(t *testing.T) {
	emitted := make(map[string]bool, len(sqlServerEngineEmittedKeys))
	for _, key := range sqlServerEngineEmittedKeys {
		emitted[key] = true
	}
	for _, r := range defaultStandardRules() {
		if !strings.HasPrefix(r.RuleKey, "sqlserver.") {
			continue
		}
		if !emitted[sqlServerRuleSuffix(r.RuleKey)] {
			t.Errorf("种子里的 %q 不是引擎产出的 key：属于幽灵规则（页面可配但永不触发），请删除或实现引擎", r.RuleKey)
		}
	}
}

// TestSQLServerSeedRuleTypeValid 种子里的 sqlserver 规则类型必须是合法取值
func TestSQLServerSeedRuleTypeValid(t *testing.T) {
	for _, r := range defaultStandardRules() {
		if !strings.HasPrefix(r.RuleKey, "sqlserver.") {
			continue
		}
		if !isAllowedRuleType(r.RuleType) {
			t.Errorf("种子 %q 的 RuleType %q 非法", r.RuleKey, r.RuleType)
		}
	}
}

// TestSQLServerSeedLevelAlignedWithMySQL 同一能力项在 MySQL 与 SQLServer 上的基线级别必须一致。
//
// 用户在规范页看到的是「表必须有主键 = 必须改进」这类统一口径，若某个库的种子把
// 同一项写成别的级别，切库后同一段 SQL 的结论会前后不一致（Oracle 轮踩过这个坑）。
func TestSQLServerSeedLevelAlignedWithMySQL(t *testing.T) {
	mysqlLevel := make(map[string]string)
	for _, r := range defaultStandardRules() {
		if strings.HasPrefix(r.RuleKey, "mysql.") {
			mysqlLevel[sqlServerRuleSuffix(r.RuleKey)] = r.BaseLevel
		}
	}
	for _, r := range defaultStandardRules() {
		if !strings.HasPrefix(r.RuleKey, "sqlserver.") {
			continue
		}
		suffix := sqlServerRuleSuffix(r.RuleKey)
		want, ok := mysqlLevel[suffix]
		if !ok {
			continue // SQLServer 独有能力项（如存储过程/触发器开关）没有 MySQL 对应项
		}
		if r.BaseLevel != want {
			t.Errorf("%q 的基线级别 %q 与 MySQL 的 %q 不一致", r.RuleKey, r.BaseLevel, want)
		}
	}
}
