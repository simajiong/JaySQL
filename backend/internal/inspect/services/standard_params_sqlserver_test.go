package services

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/models"
)

// SQLServer 规范项 → 引擎参数的映射守门测试
//
// SQL Server 的 72 条规范里有 69 条与 MySQL 能力项同名，applyStandardRule 通过
// sqlServerRuleKeyAlias 复用 mysql.* 的 switch 分支（避免 50+ 个 case 复制一份）。
// 这里锁住三件事：
//  1. 别名映射真的生效（sqlserver.* 与 mysql.* 落到同一个参数开关上）；
//  2. SQL Server 独有的三条「对象开关」规则单独映射，不受别名影响；
//  3. 禁用类型清单按库分派，SQL Server 拿到的不是 MySQL 那份（double/float/enum…）。

func applyRuleForTest(t *testing.T, ruleKey, dbType, level, params string) config.InspectParams {
	t.Helper()
	p := config.DefaultInspectParams()
	rule := models.InsightSQLStandardRule{
		RuleKey: ruleKey,
		DbType:  dbType,
		Level:   level,
		Params:  []byte(params),
	}
	applyStandardRule(&p, rule)
	return p
}

// TestApplyStandardRuleSQLServerAlias sqlserver.* 与 mysql.* 落到同一参数
func TestApplyStandardRuleSQLServerAlias(t *testing.T) {
	cases := []struct {
		name    string
		ruleKey string
		level   string
		check   func(p config.InspectParams) bool
		want    bool
	}{
		{"列不可空开启", "sqlserver.struct.limit_col_not_null", "must",
			func(p config.InspectParams) bool { return p.ENABLE_COLUMN_NOT_NULL }, true},
		{"列不可空关闭", "sqlserver.struct.limit_col_not_null", "off",
			func(p config.InspectParams) bool { return p.ENABLE_COLUMN_NOT_NULL }, false},
		{"主键规则开启", "sqlserver.struct.table_must_have_pk", "must",
			func(p config.InspectParams) bool { return p.CHECK_TABLE_PRIMARY_KEY }, true},
		{"表注释规则开启", "sqlserver.struct.limit_tbl_must_comment", "must",
			func(p config.InspectParams) bool { return p.CHECK_TABLE_COMMENT }, true},
		{"禁止外键开启=>不允许外键", "sqlserver.struct.limit_tbl_no_use_fk", "suggest",
			func(p config.InspectParams) bool { return p.ENABLE_FOREIGN_KEY }, false},
		{"索引数量限制开启", "sqlserver.struct.limit_tbl_idx_count", "suggest",
			func(p config.InspectParams) bool { return p.MAX_INDEX_KEYS > 0 }, true},
		{"WHERE 必填开启", "sqlserver.query_update.update_delete_stmt_suggest_where", "must",
			func(p config.InspectParams) bool { return p.DML_MUST_HAVE_WHERE }, true},
		{"INSERT 字段列表开启", "sqlserver.query_update.insert_stmt_specify_columns", "must",
			func(p config.InspectParams) bool { return p.CHECK_INSERT_SPECIFY_COLUMNS }, true},
		{"影响行数限制开启", "sqlserver.query_update.limit_update_delete_affected_rows", "must",
			func(p config.InspectParams) bool { return p.MAX_AFFECTED_ROWS > 0 }, true},
	}
	for _, c := range cases {
		p := applyRuleForTest(t, c.ruleKey, "sqlserver", c.level, "{}")
		if got := c.check(p); got != c.want {
			t.Errorf("%s：%s(level=%s) 期望 %v，实际 %v", c.name, c.ruleKey, c.level, c.want, got)
		}
	}
}

// TestApplyStandardRuleSQLServerExclusive SQL Server 独有的对象开关规则
func TestApplyStandardRuleSQLServerExclusive(t *testing.T) {
	cases := []struct {
		ruleKey string
		check   func(p config.InspectParams) bool
	}{
		{"sqlserver.struct.disable_proc_related_op", func(p config.InspectParams) bool { return p.ENABLE_PROCEDURE_OBJECT }},
		{"sqlserver.struct.disable_custom_function_related_op", func(p config.InspectParams) bool { return p.ENABLE_FUNCTION_OBJECT }},
		{"sqlserver.struct.disable_trigger_related_op", func(p config.InspectParams) bool { return p.ENABLE_TRIGGER_OBJECT }},
	}
	for _, c := range cases {
		// 规则=必须改进（禁止）→ 对应对象能力关闭
		if p := applyRuleForTest(t, c.ruleKey, "sqlserver", "must", "{}"); c.check(p) {
			t.Errorf("%s 处于 must（禁止）时对象开关应为 false", c.ruleKey)
		}
		// 规则=关闭 → 能力恢复（允许）
		if p := applyRuleForTest(t, c.ruleKey, "sqlserver", "off", "{}"); !c.check(p) {
			t.Errorf("%s 处于 off 时对象开关应为 true", c.ruleKey)
		}
	}
}

// TestApplyStandardRuleSQLServerForbiddenTypes 禁用类型清单按库分派
func TestApplyStandardRuleSQLServerForbiddenTypes(t *testing.T) {
	p := applyRuleForTest(t, "sqlserver.struct.limit_col_no_partial_data_type", "sqlserver", "must", "{}")
	if len(p.COLUMN_FORBIDDEN_TYPES) == 0 {
		t.Fatalf("SQL Server 未配置默认禁用类型")
	}
	got := make(map[string]bool, len(p.COLUMN_FORBIDDEN_TYPES))
	for _, tp := range p.COLUMN_FORBIDDEN_TYPES {
		got[strings.ToLower(tp)] = true
	}
	for _, want := range []string{"text", "ntext", "image"} {
		if !got[want] {
			t.Errorf("SQL Server 默认禁用类型应包含 %q，实际 %v", want, p.COLUMN_FORBIDDEN_TYPES)
		}
	}
	// 不能把 MySQL 的清单搬过来（double/float 在 SQL Server 里是合法且常用的类型）
	for _, bad := range []string{"double", "float", "enum", "set"} {
		if got[bad] {
			t.Errorf("SQL Server 默认禁用类型不应包含 MySQL 专属项 %q", bad)
		}
	}
}
