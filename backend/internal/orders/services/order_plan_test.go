package services

import (
	"strings"
	"testing"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/inspect/controllers"
)

// TestBuildOrderPlanPureTypes 固化「单一类型工单仍按原类型落库」，
// 保证混合能力上线后不改变存量 DDL / DML 工单的行为。
func TestBuildOrderPlanPureTypes(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
		ddl     int
		dml     int
	}{
		{
			name:    "纯DDL",
			content: "CREATE TABLE t_plan_a (id bigint NOT NULL PRIMARY KEY);\nALTER TABLE t_plan_a ADD COLUMN name varchar(32) NOT NULL DEFAULT '';",
			want:    orderSQLTypeDDL,
			ddl:     2,
		},
		{
			name:    "纯DML",
			content: "INSERT INTO t_plan_a (id) VALUES (1);\nUPDATE t_plan_a SET id = 2 WHERE id = 1;",
			want:    orderSQLTypeDML,
			dml:     2,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, err := BuildOrderPlan(c.content, "MySQL")
			if err != nil {
				t.Fatalf("BuildOrderPlan error: %v", err)
			}
			if plan.SQLType != c.want {
				t.Fatalf("SQLType = %q, want %q", plan.SQLType, c.want)
			}
			if plan.DDLCount != c.ddl || plan.DMLCount != c.dml {
				t.Fatalf("counts = ddl:%d dml:%d, want ddl:%d dml:%d", plan.DDLCount, plan.DMLCount, c.ddl, c.dml)
			}
		})
	}
}

// TestBuildOrderPlanMixed 混合工单：整单为 MIXED，且每条语句保留自己的类型。
// 这里同时挡住一个历史 bug：MySQL 路径按整段内容首个关键字判断类型，
// `CREATE TABLE ...; INSERT ...` 曾被整单判成 DDL，后半段被静默忽略。
func TestBuildOrderPlanMixed(t *testing.T) {
	content := strings.Join([]string{
		"CREATE TABLE t_plan_b (id bigint NOT NULL PRIMARY KEY);",
		"INSERT INTO t_plan_b (id) VALUES (1);",
		"ALTER TABLE t_plan_b ADD COLUMN name varchar(32) NOT NULL DEFAULT '';",
		"UPDATE t_plan_b SET name = 'x' WHERE id = 1;",
	}, "\n")

	plan, err := BuildOrderPlan(content, "MySQL")
	if err != nil {
		t.Fatalf("BuildOrderPlan error: %v", err)
	}
	if plan.SQLType != orderSQLTypeMixed {
		t.Fatalf("SQLType = %q, want MIXED", plan.SQLType)
	}
	if plan.DDLCount != 2 || plan.DMLCount != 2 {
		t.Fatalf("counts = ddl:%d dml:%d, want ddl:2 dml:2", plan.DDLCount, plan.DMLCount)
	}
	want := []string{orderSQLTypeDDL, orderSQLTypeDML, orderSQLTypeDDL, orderSQLTypeDML}
	if len(plan.Statements) != len(want) {
		t.Fatalf("statements = %d, want %d", len(plan.Statements), len(want))
	}
	for i, st := range plan.Statements {
		if st.SQLType != want[i] {
			t.Fatalf("statement[%d].SQLType = %q, want %q (sql=%s)", i, st.SQLType, want[i], st.Text)
		}
		if st.Seq != i+1 {
			t.Fatalf("statement[%d].Seq = %d, want %d", i, st.Seq, i+1)
		}
		if st.Line != i+1 {
			t.Fatalf("statement[%d].Line = %d, want %d", i, st.Line, i+1)
		}
	}
}

// TestBuildOrderPlanRejectsExportMixed 查询/导出语句不能与 DDL/DML 混在同一工单：
// 导出走文件流、不落库，混排没有执行语义。
func TestBuildOrderPlanRejectsExportMixed(t *testing.T) {
	content := "CREATE TABLE t_plan_c (id bigint NOT NULL PRIMARY KEY);\nSELECT * FROM t_plan_c;"
	_, err := BuildOrderPlan(content, "MySQL")
	if err == nil {
		t.Fatal("期望报错，实际通过")
	}
	if !strings.Contains(err.Error(), "混在同一个工单") {
		t.Fatalf("错误信息不符合预期: %v", err)
	}
}

// TestAnalyzeOrderConflicts 混合工单的静态冲突分析：
// 只报「必然失败」与「数据会被清空」两类，合法写法一条都不许报。
func TestAnalyzeOrderConflicts(t *testing.T) {
	cases := []struct {
		name         string
		content      string
		wantCount    int
		wantLevel    string
		wantCategory string
		wantInMsg    string
	}{
		{
			name:      "建表后灌数据_合法",
			content:   "CREATE TABLE t_x (id bigint NOT NULL PRIMARY KEY, name varchar(32));\nINSERT INTO t_x (id, name) VALUES (1, 'a');",
			wantCount: 0,
		},
		{
			name:      "加列后更新_合法",
			content:   "ALTER TABLE t_x ADD COLUMN age int NOT NULL DEFAULT 0;\nUPDATE t_x SET age = 18 WHERE id = 1;",
			wantCount: 0,
		},
		{
			name:      "先清空再重灌_合法",
			content:   "TRUNCATE TABLE t_x;\nINSERT INTO t_x (id) VALUES (1);",
			wantCount: 0,
		},
		{
			name:      "删表后插入同表_必然失败",
			content:   "DROP TABLE t_x;\nINSERT INTO t_x (id) VALUES (1);",
			wantCount: 1, wantLevel: "must", wantCategory: controllers.CategoryObjectMissing, wantInMsg: "执行必然失败",
		},
		{
			name:      "改名后仍用旧表名_必然失败",
			content:   "RENAME TABLE t_x TO t_y;\nDELETE FROM t_x WHERE id = 1;",
			wantCount: 1, wantLevel: "must", wantCategory: controllers.CategoryObjectMissing, wantInMsg: "被 RENAME",
		},
		{
			name:      "删列后仍引用该列_必然失败",
			content:   "ALTER TABLE t_x DROP COLUMN name;\nUPDATE t_x SET name = 'b' WHERE id = 1;",
			wantCount: 1, wantLevel: "must", wantCategory: controllers.CategoryObjectMissing, wantInMsg: "DROP COLUMN",
		},
		{
			name:      "改列名后仍用旧列名_必然失败",
			content:   "ALTER TABLE t_x CHANGE COLUMN name nick varchar(32);\nINSERT INTO t_x (id, name) VALUES (1, 'a');",
			wantCount: 1, wantLevel: "must", wantCategory: controllers.CategoryObjectMissing, wantInMsg: "被重命名",
		},
		{
			name:      "先变更再清空_数据被丢弃",
			content:   "INSERT INTO t_x (id) VALUES (1);\nTRUNCATE TABLE t_x;",
			wantCount: 1, wantLevel: "suggest", wantCategory: controllers.CategoryRuleViolation, wantInMsg: "TRUNCATE 清空",
		},
		{
			name:      "删表后重建再插入_合法",
			content:   "DROP TABLE t_x;\nCREATE TABLE t_x (id bigint NOT NULL PRIMARY KEY);\nINSERT INTO t_x (id) VALUES (1);",
			wantCount: 0,
		},
		{
			name:      "删索引不算删列_合法",
			content:   "ALTER TABLE t_x DROP INDEX idx_name;\nINSERT INTO t_x (id) VALUES (1);",
			wantCount: 0,
		},
		{
			// 用户真实案例：create table t(id int); insert into t(name)...
			// 审核引擎只对照库的当前状态（表还没建），由计划内列校验兜底
			name:      "建表后插入未定义字段_必然失败",
			content:   "create table t_2026_1019(id int);\ninsert into t_2026_1019(name)values('10');",
			wantCount: 1, wantLevel: "must", wantCategory: controllers.CategoryObjectMissing, wantInMsg: "未定义该字段",
		},
		{
			name:      "建表后加列再插入_合法",
			content:   "CREATE TABLE t_x (id bigint PRIMARY KEY);\nALTER TABLE t_x ADD COLUMN name varchar(32) NOT NULL DEFAULT '';\nINSERT INTO t_x (name) VALUES ('a');",
			wantCount: 0,
		},
		{
			name:      "建表后无列清单插入_不校验",
			content:   "CREATE TABLE t_x (id bigint PRIMARY KEY);\nINSERT INTO t_x VALUES (1);",
			wantCount: 0,
		},
		{
			name:      "ADD INDEX 不算加列",
			content:   "CREATE TABLE t_x (id bigint PRIMARY KEY);\nALTER TABLE t_x ADD INDEX idx_name (name);\nINSERT INTO t_x (name) VALUES ('a');",
			wantCount: 1, wantLevel: "must", wantCategory: controllers.CategoryObjectMissing, wantInMsg: "未定义该字段",
		},
		{
			name:      "AS SELECT 建表列集未知_不校验",
			content:   "CREATE TABLE t_x AS SELECT id, name FROM other_t;\nINSERT INTO t_x (whatever) VALUES (1);",
			wantCount: 0,
		},
		{
			name:      "UPDATE 引用未定义字段_必然失败",
			content:   "CREATE TABLE t_x (id bigint PRIMARY KEY);\nUPDATE t_x SET name = 'x' WHERE id = 1;",
			wantCount: 1, wantLevel: "must", wantCategory: controllers.CategoryObjectMissing, wantInMsg: "未定义该字段",
		},
		{
			name:      "默认值含逗号与嵌套括号_合法",
			content:   "CREATE TABLE t_x (id bigint, name varchar(32) DEFAULT 'a,b', extra decimal(10,2));\nINSERT INTO t_x (name, extra) VALUES ('a', 1);",
			wantCount: 0,
		},
		{
			name:      "建表含表级约束段_合法",
			content:   "CREATE TABLE t_x (id bigint, PRIMARY KEY (id), name varchar(32), UNIQUE KEY (name));\nINSERT INTO t_x (name) VALUES ('a');",
			wantCount: 0,
		},
		{
			name:      "改列名后用新列名_合法",
			content:   "CREATE TABLE t_x (id bigint, name varchar(32));\nALTER TABLE t_x CHANGE COLUMN name nick varchar(32);\nINSERT INTO t_x (nick) VALUES ('a');",
			wantCount: 0,
		},
		{
			name:      "PG 建表后插入未定义字段_必然失败",
			content:   "create table t_x (id int);\ninsert into t_x (name) values ('a');",
			wantCount: 1, wantLevel: "must", wantCategory: controllers.CategoryObjectMissing, wantInMsg: "未定义该字段",
		},
		{
			// 用户真实案例：裸 INSERT 无字段列表，1 个值 vs 建表 2 个字段
			name:      "建表后裸插入值数不足_必然失败",
			content:   "create table t_2026_1019_01(id int,name char(10));\ninsert into t_2026_1019_01 values('10');",
			wantCount: 1, wantLevel: "must", wantCategory: controllers.CategoryObjectMissing, wantInMsg: "提供了 1 个值",
		},
		{
			name:      "建表后显式列表值数不足_必然失败",
			content:   "CREATE TABLE t_x (id bigint, name varchar(32));\nINSERT INTO t_x (id, name) VALUES (1);",
			wantCount: 1, wantLevel: "must", wantCategory: controllers.CategoryObjectMissing, wantInMsg: "提供了 1 个值",
		},
		{
			name:      "建表后显式列表值数匹配_合法",
			content:   "CREATE TABLE t_x (id bigint, name varchar(32));\nINSERT INTO t_x (id, name) VALUES (1, 'a');",
			wantCount: 0,
		},
		{
			name:      "建表后裸插入多行其一值数不符_必然失败",
			content:   "CREATE TABLE t_x (id bigint, name varchar(32));\nINSERT INTO t_x VALUES (1, 'a'), (3);",
			wantCount: 1, wantLevel: "must", wantCategory: controllers.CategoryObjectMissing, wantInMsg: "第 2 行",
		},
		{
			name:      "裸插入空值组_合法",
			content:   "CREATE TABLE t_x (id bigint, name varchar(32));\nINSERT INTO t_x VALUES ();",
			wantCount: 0,
		},
		{
			name:      "INSERT SELECT 无法判定值数_不校验",
			content:   "CREATE TABLE t_x (id bigint, name varchar(32));\nINSERT INTO t_x SELECT id, name FROM other_t;",
			wantCount: 0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, err := BuildOrderPlan(c.content, "MySQL")
			if err != nil {
				t.Fatalf("BuildOrderPlan error: %v", err)
			}
			conflicts := analyzeOrderConflicts(plan)
			if len(conflicts) != c.wantCount {
				t.Fatalf("conflicts = %d, want %d: %+v", len(conflicts), c.wantCount, conflicts)
			}
			if c.wantCount == 0 {
				return
			}
			got := conflicts[0]
			if got.Level != c.wantLevel {
				t.Fatalf("Level = %q, want %q", got.Level, c.wantLevel)
			}
			if got.Category != c.wantCategory {
				t.Fatalf("Category = %q, want %q", got.Category, c.wantCategory)
			}
			if !strings.Contains(got.Message, c.wantInMsg) {
				t.Fatalf("Message = %q, 期望包含 %q", got.Message, c.wantInMsg)
			}
			if got.Advice == "" || got.Reason == "" {
				t.Fatalf("冲突结论必须带原因与建议: %+v", got)
			}
		})
	}
}

// TestAppendOrderPlanFindings 冲突结论并入预审结果后，状态与计数必须同步刷新，
// 否则「页面显示 must 问题、提交却放行」会重新出现。
func TestAppendOrderPlanFindings(t *testing.T) {
	plan, err := BuildOrderPlan("DROP TABLE t_x;\nINSERT INTO t_x (id) VALUES (1);", "MySQL")
	if err != nil {
		t.Fatalf("BuildOrderPlan error: %v", err)
	}
	result := map[string]any{
		"status":        "passed",
		"must_count":    0,
		"suggest_count": 0,
		"items":         []preInspectItem{},
	}
	appendOrderPlanFindings(result, plan)

	if got := result["status"]; got != "must_fix" {
		t.Fatalf("status = %v, want must_fix", got)
	}
	if got := result["must_count"]; got != 1 {
		t.Fatalf("must_count = %v, want 1", got)
	}
	if got := result["sql_type"]; got != orderSQLTypeMixed {
		t.Fatalf("sql_type = %v, want MIXED", got)
	}
	items := preInspectItemsOf(result["items"])
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].Category != controllers.CategoryObjectMissing {
		t.Fatalf("category = %q, want object_missing", items[0].Category)
	}
	if items[0].Line != 2 {
		t.Fatalf("line = %d, want 2", items[0].Line)
	}
}

// TestAppendOrderPlanFindingsNoConflictKeepsStatus 合法混合工单不得产生任何噪音提示。
func TestAppendOrderPlanFindingsNoConflictKeepsStatus(t *testing.T) {
	plan, err := BuildOrderPlan("CREATE TABLE t_x (id bigint NOT NULL PRIMARY KEY);\nINSERT INTO t_x (id) VALUES (1);", "MySQL")
	if err != nil {
		t.Fatalf("BuildOrderPlan error: %v", err)
	}
	result := map[string]any{
		"status":        "passed",
		"must_count":    0,
		"suggest_count": 0,
		"items":         []preInspectItem{},
	}
	appendOrderPlanFindings(result, plan)
	if got := result["status"]; got != "passed" {
		t.Fatalf("status = %v, want passed", got)
	}
	if items := preInspectItemsOf(result["items"]); len(items) != 0 {
		t.Fatalf("items = %d, want 0", len(items))
	}
}

// TestReconcileOrderSQLType 内容为准：混合内容接受客户端按单一类型提交，其余不一致仍拦截。
func TestReconcileOrderSQLType(t *testing.T) {
	cases := []struct {
		provided string
		inferred string
		want     string
		wantErr  bool
	}{
		{"", orderSQLTypeMixed, orderSQLTypeMixed, false},
		{"DDL", orderSQLTypeMixed, orderSQLTypeMixed, false},
		{"DML", orderSQLTypeMixed, orderSQLTypeMixed, false},
		{"MIXED", orderSQLTypeMixed, orderSQLTypeMixed, false},
		{"", orderSQLTypeDDL, orderSQLTypeDDL, false},
		{"DDL", orderSQLTypeDDL, orderSQLTypeDDL, false},
		{"DML", orderSQLTypeDDL, "", true},
		{"DDL", orderSQLTypeDML, "", true},
		{"EXPORT", orderSQLTypeDML, "", true},
	}
	for _, c := range cases {
		got, err := reconcileOrderSQLType(c.provided, commonModels.EnumType(c.inferred))
		if c.wantErr {
			if err == nil {
				t.Fatalf("provided=%q inferred=%q 期望报错，实际通过", c.provided, c.inferred)
			}
			continue
		}
		if err != nil {
			t.Fatalf("provided=%q inferred=%q 报错: %v", c.provided, c.inferred, err)
		}
		if string(got) != c.want {
			t.Fatalf("provided=%q inferred=%q got=%q, want %q", c.provided, c.inferred, got, c.want)
		}
	}
}

// TestBuildOrderTaskSpecsMixed 任务拆分：每条任务带自己的类型与书写顺序。
func TestBuildOrderTaskSpecsMixed(t *testing.T) {
	content := "CREATE TABLE t_q (id bigint NOT NULL PRIMARY KEY);\nINSERT INTO t_q (id) VALUES (1);\nDROP TABLE t_q;"
	specs, err := buildOrderTaskSpecs(content, "MySQL", orderSQLTypeMixed)
	if err != nil {
		t.Fatalf("buildOrderTaskSpecs error: %v", err)
	}
	want := []string{orderSQLTypeDDL, orderSQLTypeDML, orderSQLTypeDDL}
	if len(specs) != len(want) {
		t.Fatalf("specs = %d, want %d", len(specs), len(want))
	}
	for i, spec := range specs {
		if spec.SQLType != want[i] {
			t.Fatalf("spec[%d].SQLType = %q, want %q", i, spec.SQLType, want[i])
		}
		if strings.TrimSpace(spec.SQL) == "" {
			t.Fatalf("spec[%d].SQL 为空", i)
		}
	}
}

// TestSuppressSelfCreatedObjectMissing 本工单内先行 CREATE 的对象，
// 后续语句不应被判「对象不存在」——否则混合工单每条 DML 都会被自己的建表语句判死。
func TestSuppressSelfCreatedObjectMissing(t *testing.T) {
	plan, err := BuildOrderPlan(strings.Join([]string{
		"CREATE TABLE t_new (id bigint NOT NULL PRIMARY KEY);",
		"ALTER TABLE t_new ADD COLUMN name varchar(32);",
		"INSERT INTO t_new (id) VALUES (1);",
	}, "\n"), "MySQL")
	if err != nil {
		t.Fatalf("BuildOrderPlan error: %v", err)
	}
	items := []preInspectItem{
		{Line: 2, Level: "must", Category: controllers.CategoryObjectMissing, Object: "t_new", Message: "表或视图`t_new`不存在"},
		{Line: 2, Level: "suggest", Category: controllers.CategoryRuleViolation, Message: "Error 1146 (42S02): Table 'jaysql.t_new' doesn't exist"},
		{Line: 3, Level: "suggest", Category: controllers.CategoryRuleViolation, Message: "Error 1146 (42S02): Table 'jaysql.t_new' doesn't exist"},
	}
	kept := suppressSelfCreatedObjectMissing(items, plan)
	if len(kept) != 0 {
		t.Fatalf("应全部剔除，实际保留 %d 条: %+v", len(kept), kept)
	}
}

// TestSuppressSelfCreatedObjectMissingKeepsOthers 只压「自建对象」的误报，
// 其它对象不存在、其它类别的结论一律保留。
func TestSuppressSelfCreatedObjectMissingKeepsOthers(t *testing.T) {
	plan, err := BuildOrderPlan(strings.Join([]string{
		"CREATE TABLE t_new (id bigint NOT NULL PRIMARY KEY);",
		"INSERT INTO t_new (id) VALUES (1);",
	}, "\n"), "MySQL")
	if err != nil {
		t.Fatalf("BuildOrderPlan error: %v", err)
	}
	items := []preInspectItem{
		// 另一个表不存在：不是本工单创建的，必须保留
		{Line: 2, Level: "must", Category: controllers.CategoryObjectMissing, Object: "t_other", Message: "表或视图`t_other`不存在"},
		// 表存在性问题以外的规范问题：必须保留
		{Line: 1, Level: "suggest", Category: controllers.CategoryRuleViolation, Message: "表`t_new`必须有注释"},
	}
	kept := suppressSelfCreatedObjectMissing(items, plan)
	if len(kept) != 2 {
		t.Fatalf("应保留 2 条，实际 %d 条: %+v", len(kept), kept)
	}
}

// TestSuppressSelfCreatedObjectMissingLaterCreate CREATE 晚于引用语句时不成立，
// 此时对象确实还不存在，结论必须保留。
func TestSuppressSelfCreatedObjectMissingLaterCreate(t *testing.T) {
	plan, err := BuildOrderPlan(strings.Join([]string{
		"INSERT INTO t_late (id) VALUES (1);",
		"CREATE TABLE t_late (id bigint NOT NULL PRIMARY KEY);",
	}, "\n"), "MySQL")
	if err != nil {
		t.Fatalf("BuildOrderPlan error: %v", err)
	}
	items := []preInspectItem{
		{Line: 1, Level: "must", Category: controllers.CategoryObjectMissing, Object: "t_late", Message: "表或视图`t_late`不存在"},
	}
	if kept := suppressSelfCreatedObjectMissing(items, plan); len(kept) != 1 {
		t.Fatalf("应保留 1 条，实际 %d 条", len(kept))
	}
}

// TestOrderSQLTypeLabel 通知/日志里的类型展示口径。
func TestOrderSQLTypeLabel(t *testing.T) {
	if got := orderSQLTypeLabel("MIXED"); got != "DDL + DML 混合" {
		t.Fatalf("MIXED label = %q", got)
	}
	if got := orderSQLTypeLabel("DDL"); got != "DDL" {
		t.Fatalf("DDL label = %q", got)
	}
}

// TestBuildOrderPlanSQLServerGoBatch 用户真实场景：T-SQL 脚本带 GO 批处理分隔符
// 与 EXEC sp_addextendedproperty。GO 不是 T-SQL 语句（SSMS/sqlcmd 客户端分隔符），
// 提交链路必须先剥离；EXEC 系列按 DDL 归类（与预审链路同口径）。
func TestBuildOrderPlanSQLServerGoBatch(t *testing.T) {
	content := `-- 1. 创建表结构
CREATE TABLE [dbo].[EmployeeInfo] (
    [Id] INT IDENTITY(1,1) NOT NULL,
    [EmpNo] VARCHAR(20) NOT NULL,
    CONSTRAINT [PK_EmployeeInfo] PRIMARY KEY CLUSTERED ([Id] ASC)
);
GO

-- 2. 为表和字段添加备注 (使用系统存储过程)
EXEC sp_addextendedproperty
    @name = N'MS_Description',
    @value = N'公司员工基本信息表',
    @level0type = N'SCHEMA', @level0name = N'dbo',
    @level1type = N'TABLE',  @level1name = N'EmployeeInfo';
GO`

	plan, err := BuildOrderPlan(content, "SQLServer")
	if err != nil {
		t.Fatalf("BuildOrderPlan error: %v", err)
	}
	if len(plan.Statements) != 2 {
		for i, st := range plan.Statements {
			t.Logf("stmt %d: type=%s head=%q", i+1, st.SQLType, st.Text[:min(40, len(st.Text))])
		}
		t.Fatalf("应拆出 2 条语句，实际 %d 条", len(plan.Statements))
	}
	if plan.Statements[0].SQLType != "DDL" || plan.Statements[1].SQLType != "DDL" {
		t.Fatalf("CREATE 与 EXEC 都应归为 DDL，实际 %s / %s",
			plan.Statements[0].SQLType, plan.Statements[1].SQLType)
	}
	// GO 不应残留在任何语句里
	for _, st := range plan.Statements {
		if strings.EqualFold(strings.TrimSpace(strings.TrimLeft(st.Text, " \t\r\n-;")), "GO") {
			t.Fatalf("GO 残留：%q", st.Text)
		}
	}
}

// TestBuildOrderPlanSQLServerGoN 独立验证 `GO n`（重复执行次数）写法被剥离，
// 且纯 DDL 工单类型归纳不受影响。
func TestBuildOrderPlanSQLServerGoN(t *testing.T) {
	content := `INSERT INTO [dbo].[t_go] ([Id]) VALUES (1);
GO 5`
	plan, err := BuildOrderPlan(content, "SQLServer")
	if err != nil {
		t.Fatalf("BuildOrderPlan error: %v", err)
	}
	if len(plan.Statements) != 1 || plan.Statements[0].SQLType != "DML" {
		t.Fatalf("应拆出 1 条 DML，实际 %d 条 / %v", len(plan.Statements), plan.Statements)
	}
}
