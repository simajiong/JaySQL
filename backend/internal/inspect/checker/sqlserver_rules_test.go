package checker

// SQL Server 引擎单测
//
// 覆盖三类判定：
//  1. 纯语法类（不连库）：建表/改表/索引/DML 的规范检查；
//  2. 准入闸门类：CTAS（SELECT ... INTO）、视图、存储过程/函数/触发器；
//  3. 对象存在性类：注入 sqlServerStubMeta 桩实现，验证 rule_key 与结论类别。
//
// 不连库时（meta 返回 err）所有依赖元信息的规则必须静默跳过 —— 这是 PG/Oracle
// 引擎共同的「宁可漏报，绝不误报」约定，同样在这里守住。

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
)

// sqlServerProdLikeParams 模拟「生产环境规范」映射出的审核参数（结构与 DML 类规则全开）
func sqlServerProdLikeParams() config.InspectParams {
	p := config.DefaultInspectParams()
	p.CHECK_TABLE_PRIMARY_KEY = true
	p.CHECK_COLUMN_COMMENT = true
	p.CHECK_TABLE_COMMENT = true
	p.CHECK_COLUMN_DEFAULT_VALUE = true
	p.ENABLE_COLUMN_NOT_NULL = true
	p.CHECK_TABLE_IDENTIFIER = true
	p.CHECK_COLUMN_IDENTIFIER = true
	p.CHECK_IDENTIFER_KEYWORD = true
	p.CHECK_PRIMARYKEY_USE_BIGINT = true
	p.PRIMARYKEY_SUPPORT_TYPES = []string{"bigint"}
	p.PRIMARYKEY_MAX_KEY_PARTS = 3
	p.SECONDARY_INDEX_MAX_KEY_PARTS = 5
	p.MAX_INDEX_KEYS = 8
	p.COLUMN_MAX_CHAR_LENGTH = 64
	p.MAX_VARCHAR_LENGTH = 1024
	p.CHECK_UNIQ_INDEX_PREFIX = true
	p.CHECK_SECONDARY_INDEX_PREFIX = true
	p.CHECK_INDEX_NAME_REQUIRED = true
	p.UNQI_INDEX_PREFIX = "UK_"
	p.SECONDARY_INDEX_PREFIX = "IDX_"
	p.ENABLE_FOREIGN_KEY = false
	p.ENABLE_CREATE_VIEW = false
	p.ENABLE_CREATE_TABLE_AS = false
	p.CHECK_TABLE_AUTOINCREMENT_INIT_VALUE = true
	p.TABLE_AUTOINCREMENT_INIT_VALUE = 1
	p.CHECK_AUTOINCREMENT_COL_NAME_IS_ID = true
	p.CHECK_PRIMARYKEY_USE_AUTO_INCREMENT = true
	p.CHECK_COLUMN_FORBIDDEN_TYPES = true
	p.COLUMN_FORBIDDEN_TYPES = []string{"text", "ntext", "image", "sql_variant", "timestamp", "money", "smallmoney"}
	p.CHECK_COLUMN_COLLATION = true
	p.MAX_TABLE_COLUMNS = 100
	p.TABLE_REQUIRED_COLUMNS = []string{"create_time", "update_time"}
	p.ENABLE_REDUNDANT_INDEX = false
	p.ENABLE_MERGE_ALTER_TABLE = true
	p.DML_MUST_HAVE_WHERE = true
	p.DML_DISABLE_SUBQUERY = true
	p.DML_DISABLE_ORDERBY = true
	p.CHECK_DML_JOIN_WITH_ON = true
	p.MAX_UPDATE_DELETE_JOIN_TABLES = 2
	p.DML_MAX_LIMIT_ROWS = 100
	p.MAX_WHERE_IN_ELEMENTS = 100
	p.MAX_INSERT_ROWS = 100
	p.CHECK_INSERT_SPECIFY_COLUMNS = true
	p.CHECK_INSERT_COLUMN_NO_DUP = true
	p.CHECK_INSERT_COLUMNS_MATCH_VALUES = true
	p.CHECK_INSERT_AVOID_SYSDATE = true
	p.CHECK_UPDATE_SET_SEPARATOR = true
	p.CHECK_UPDATE_MULTI_TABLE_COLUMN_PREFIX = true
	p.CHECK_UPDATE_UPDATES_PK = true
	p.CHECK_UPDATE_UPDATES_UNIQUE_KEY = true
	p.UPDATE_TIME_COLUMNS = "update_time"
	p.AVOID_UPDATE_COLUMNS = "create_time"
	p.CHECK_WHERE_LEADING_WILDCARD = true
	p.CHECK_WHERE_NOT_IN_NOT_LIKE = true
	p.CHECK_WHERE_OR_CONDITION = true
	p.CHECK_WHERE_LIKE_WITHOUT_WILDCARD = true
	p.CHECK_WHERE_INDEX_COL_CALC = true
	p.CHECK_WHERE_IMPLICIT_CAST = true
	p.CHECK_DELETE_TABLE_EXISTS = true
	p.CHECK_INSERT_OBJECT_EXISTS = true
	p.CHECK_INSERT_NOT_NULL_COLUMN_NULL = true
	p.CHECK_DML_MUST_USE_INDEX = true
	p.MAX_AFFECTED_ROWS = 10000
	return p
}

// runSQLServer 执行 SQL Server 引擎预审
func runSQLServer(t *testing.T, sql string, params config.InspectParams) []ReturnData {
	t.Helper()
	svc := &SyntaxInspectService{
		DbType:        "SQLServer",
		SqlText:       sql,
		InspectParams: params,
	}
	data, err := svc.runSQLServerInspect()
	if err != nil {
		t.Fatalf("SQL Server 预审执行失败: %v", err)
	}
	return data
}

// runSQLServerWithMeta 注入元信息桩实现
func runSQLServerWithMeta(t *testing.T, sql string, params config.InspectParams, meta *sqlServerStubMeta) []ReturnData {
	t.Helper()
	svc := &SyntaxInspectService{
		DbType:        "SQLServer",
		SqlText:       sql,
		InspectParams: params,
		sqlserverMeta: meta,
	}
	data, err := svc.runSQLServerInspect()
	if err != nil {
		t.Fatalf("SQL Server 预审执行失败: %v", err)
	}
	return data
}

// sqlServerStubMeta sqlServerMetaResolver 的桩实现。
//
// 默认语义（贴近真实库的"干净"状态）：
//   - 表一律不存在、索引一律存在、列一律存在且允许 NULL、无注释；
//   - failAll=true 时全部返回错误，等价于"连不上库"。
type sqlServerStubMeta struct {
	tables       map[string]bool
	columns      map[string]bool
	indexes      map[string]bool
	colTypes     map[string]string
	pk           map[string][]string
	unique       map[string][]string
	leading      map[string][]string
	tableComment map[string]bool
	colComment   map[string]bool
	countWhere   map[string]int
	idxCount     map[string]int
	failAll      bool
}

func (m *sqlServerStubMeta) TableExists(name sqlServerName) (bool, error) {
	if m.failAll {
		return false, errStubUnavailable
	}
	return m.tables[name.Key()], nil
}

func (m *sqlServerStubMeta) ColumnExists(name sqlServerName, column string) (bool, error) {
	if m.failAll {
		return false, errStubUnavailable
	}
	key := name.Key() + "." + strings.ToLower(column)
	if v, ok := m.columns[key]; ok {
		return v, nil
	}
	return true, nil // 默认认为存在，避免无关用例被"列不存在"噪音淹没
}

func (m *sqlServerStubMeta) IndexExists(name sqlServerName, index string) (bool, error) {
	if m.failAll {
		return false, errStubUnavailable
	}
	key := name.Key() + "." + strings.ToLower(index)
	if v, ok := m.indexes[key]; ok {
		return v, nil
	}
	return true, nil
}

func (m *sqlServerStubMeta) TableColumnCount(name sqlServerName) (int, error) {
	if m.failAll {
		return 0, errStubUnavailable
	}
	return 0, nil
}

func (m *sqlServerStubMeta) IndexCount(name sqlServerName) (int, error) {
	if m.failAll {
		return 0, errStubUnavailable
	}
	return m.idxCount[name.Key()], nil
}

func (m *sqlServerStubMeta) PrimaryKeyColumns(name sqlServerName) ([]string, error) {
	if m.failAll {
		return nil, errStubUnavailable
	}
	return m.pk[name.Key()], nil
}

func (m *sqlServerStubMeta) UniqueIndexColumns(name sqlServerName) ([]string, error) {
	if m.failAll {
		return nil, errStubUnavailable
	}
	return m.unique[name.Key()], nil
}

func (m *sqlServerStubMeta) ColumnNullable(name sqlServerName, column string) (bool, error) {
	if m.failAll {
		return true, errStubUnavailable
	}
	return true, nil
}

func (m *sqlServerStubMeta) TableCommented(name sqlServerName) (bool, error) {
	if m.failAll {
		return false, errStubUnavailable
	}
	return m.tableComment[name.Key()], nil
}

func (m *sqlServerStubMeta) ColumnCommented(name sqlServerName, column string) (bool, error) {
	if m.failAll {
		return false, errStubUnavailable
	}
	return m.colComment[name.Key()+"."+strings.ToLower(column)], nil
}

func (m *sqlServerStubMeta) IndexedLeadingColumns(name sqlServerName) ([]string, error) {
	if m.failAll {
		return nil, errStubUnavailable
	}
	return m.leading[name.Key()], nil
}

func (m *sqlServerStubMeta) CountWhere(name sqlServerName, where string) (int, error) {
	if m.failAll {
		return 0, errStubUnavailable
	}
	return m.countWhere[name.Key()], nil
}

var errStubUnavailable = stubError("库连接不可用（桩实现）")

type stubError string

func (e stubError) Error() string { return string(e) }

// ---------- 建表 ----------

const sqlServerBadCreateTable = `CREATE TABLE BadOrder (
	OrderNo VARCHAR(500) NOT NULL,
	wrongName INT NOT NULL,
	note TEXT NULL,
	amount MONEY NULL,
	ver TIMESTAMP NULL,
	amount_copy AS (amount + 1),
	CONSTRAINT PK_BadOrder PRIMARY KEY (OrderNo)
)`

func TestSQLServerCreateTableRules(t *testing.T) {
	data := runSQLServer(t, sqlServerBadCreateTable, sqlServerProdLikeParams())
	keys := collectRuleKeys(data)

	// 表名大写不符合小写要求
	if !hasRuleKey(keys, "limit_tbl_name_case") {
		t.Errorf("表名 BadOrder 应命中 limit_tbl_name_case，实际: %v", keys)
	}
	// 列名 wrongName 含大写
	if !hasRuleKey(keys, "limit_col_name_case") {
		t.Errorf("列名 wrongName 应命中 limit_col_name_case，实际: %v", keys)
	}
	// VARCHAR(500) 超上限 1024 不报，但列 orderNo 无注释 => limit_col_must_have_comment
	if !hasRuleKey(keys, "limit_col_must_have_comment") {
		t.Errorf("未提供扩展属性注释应命中 limit_col_must_have_comment，实际: %v", keys)
	}
	if !hasRuleKey(keys, "limit_tbl_must_comment") {
		t.Errorf("未提供表注释应命中 limit_tbl_must_comment，实际: %v", keys)
	}
	// TEXT / MONEY / TIMESTAMP 属禁用类型
	if !hasRuleKey(keys, "limit_col_no_partial_data_type") {
		t.Errorf("TEXT/MONEY/TIMESTAMP 应命中 limit_col_no_partial_data_type，实际: %v", keys)
	}
	// 主键列类型 VARCHAR 不在白名单 bigint
	if !hasRuleKey(keys, "limit_pk_col_type") {
		t.Errorf("主键列类型 VARCHAR 应命中 limit_pk_col_type，实际: %v", keys)
	}
	// 主键列非 IDENTITY
	if !hasRuleKey(keys, "limit_pk_col_must_auto_inc") {
		t.Errorf("主键非 IDENTITY 应命中 limit_pk_col_must_auto_inc，实际: %v", keys)
	}
	// 缺少必需的 create_time / update_time
	if !hasRuleKey(keys, "table_must_include_required_columns") {
		t.Errorf("缺少必需列应命中 table_must_include_required_columns，实际: %v", keys)
	}
	// 计算列不参与类型/注释判定（amount_copy 不应产生 no_column_collate 之外的噪音）
	if hasRuleKey(keys, "table_must_have_pk") {
		t.Errorf("已定义主键不应命中 table_must_have_pk，实际: %v", keys)
	}
}

func TestSQLServerGoodCreateTableNoViolation(t *testing.T) {
	params := config.DefaultInspectParams()
	// 只打开"结构完整性"类规则，验证干净写法不产生噪音
	params.CHECK_TABLE_PRIMARY_KEY = true
	params.CHECK_PRIMARYKEY_USE_BIGINT = true
	params.PRIMARYKEY_SUPPORT_TYPES = []string{"bigint"}
	sql := `CREATE TABLE dbo.t_order (
	id BIGINT IDENTITY(1,1) NOT NULL PRIMARY KEY,
	order_no NVARCHAR(64) NOT NULL,
	create_time DATETIME2 NOT NULL DEFAULT (getdate()),
	update_time DATETIME2 NOT NULL DEFAULT (getdate())
)`
	keys := collectRuleKeys(runSQLServer(t, sql, params))
	if len(keys) != 0 {
		t.Fatalf("标准写法不应产生任何违规项，实际: %v", keys)
	}
}

func TestSQLServerVarcharMaxExceedsLimit(t *testing.T) {
	params := config.DefaultInspectParams()
	params.MAX_VARCHAR_LENGTH = 1024
	params.COLUMN_MAX_CHAR_LENGTH = 64
	sql := `CREATE TABLE t1 (
	a1 NVARCHAR(MAX) NULL,
	a2 VARCHAR(2000) NULL,
	a3 NCHAR(80) NULL,
	a4 NVARCHAR(50) NULL
)`
	keys := collectRuleKeys(runSQLServer(t, sql, params))
	if !hasRuleKey(keys, "limit_varchar_type_col_len") {
		t.Fatalf("NVARCHAR(MAX)/VARCHAR(2000) 应命中 limit_varchar_type_col_len，实际: %v", keys)
	}
	if !hasRuleKey(keys, "limit_char_type_col_len") {
		t.Fatalf("NCHAR(80) 应命中 limit_char_type_col_len，实际: %v", keys)
	}
}

func TestSQLServerIdentityRules(t *testing.T) {
	params := config.DefaultInspectParams()
	params.CHECK_AUTOINCREMENT_COL_NAME_IS_ID = true
	params.CHECK_TABLE_AUTOINCREMENT_INIT_VALUE = true
	params.TABLE_AUTOINCREMENT_INIT_VALUE = 1
	sql := `CREATE TABLE t_identity (
	order_id BIGINT IDENTITY(1000, 1) NOT NULL PRIMARY KEY
)`
	keys := collectRuleKeys(runSQLServer(t, sql, params))
	if !hasRuleKey(keys, "limit_auto_inc_col_name_is_id") {
		t.Fatalf("IDENTITY 列名 order_id 应命中 limit_auto_inc_col_name_is_id，实际: %v", keys)
	}
	if !hasRuleKey(keys, "limit_create_table_auto_inc_init") {
		t.Fatalf("IDENTITY(1000,1) 应命中 limit_create_table_auto_inc_init，实际: %v", keys)
	}
}

func TestSQLServerColumnCollateForbidden(t *testing.T) {
	params := config.DefaultInspectParams()
	params.CHECK_COLUMN_COLLATION = true
	sql := `CREATE TABLE t_collate (
	name NVARCHAR(50) COLLATE Latin1_General_CI_AS NULL
)`
	keys := collectRuleKeys(runSQLServer(t, sql, params))
	if !hasRuleKey(keys, "no_column_collate") {
		t.Fatalf("列级 COLLATE 应命中 no_column_collate，实际: %v", keys)
	}
}

func TestSQLServerBracketIdentifiers(t *testing.T) {
	params := config.DefaultInspectParams()
	params.CHECK_COLUMN_IDENTIFIER = true
	params.COLUMN_NAME_CASE = "lower"
	sql := `CREATE TABLE [dbo].[Order Header] (
	[Id] BIGINT NOT NULL,
	[Order No] NVARCHAR(20) NULL,
	CONSTRAINT [PK_Order] PRIMARY KEY ([Id])
)`
	keys := collectRuleKeys(runSQLServer(t, sql, params))
	// [Id] / [Order No] 都不满足小写命名要求（含大写字母或空格）
	if !hasRuleKey(keys, "limit_col_name_case") {
		t.Fatalf("方括号标识符应被正确解析并命中 limit_col_name_case，实际: %v", keys)
	}
}

// ---------- 注释（扩展属性） ----------

func TestSQLServerCommentsViaExtendedProperty(t *testing.T) {
	params := config.DefaultInspectParams()
	params.CHECK_COLUMN_COMMENT = true
	params.CHECK_TABLE_COMMENT = true
	sql := `CREATE TABLE dbo.t_user (
	id BIGINT NOT NULL PRIMARY KEY,
	name NVARCHAR(50) NOT NULL
);
EXEC sp_addextendedproperty 'MS_Description', '用户表', 'SCHEMA', 'dbo', 'TABLE', 't_user';
EXEC sp_addextendedproperty 'MS_Description', '主键', 'SCHEMA', 'dbo', 'TABLE', 't_user', 'COLUMN', 'id';
EXEC sp_addextendedproperty @name = N'MS_Description', @value = N'姓名',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE', @level1name = N't_user',
     @level2type = N'COLUMN', @level2name = N'name';`
	keys := collectRuleKeys(runSQLServer(t, sql, params))
	if hasRuleKey(keys, "limit_col_must_have_comment") {
		t.Fatalf("两列都提供了扩展属性注释，不应命中 limit_col_must_have_comment，实际: %v", keys)
	}
	if hasRuleKey(keys, "limit_tbl_must_comment") {
		t.Fatalf("提供了表级扩展属性，不应命中 limit_tbl_must_comment，实际: %v", keys)
	}
}

func TestSQLServerTableCommentFromDB(t *testing.T) {
	params := config.DefaultInspectParams()
	params.CHECK_TABLE_COMMENT = true
	params.CHECK_COLUMN_COMMENT = false
	meta := &sqlServerStubMeta{tableComment: map[string]bool{"t_user": true}}
	keys := collectRuleKeys(runSQLServerWithMeta(t, `CREATE TABLE dbo.t_user (id BIGINT NOT NULL PRIMARY KEY)`, params, meta))
	if hasRuleKey(keys, "limit_tbl_must_comment") {
		t.Fatalf("库中已有 MS_Description 时不应命中 limit_tbl_must_comment，实际: %v", keys)
	}
}

// ---------- 索引 ----------

func TestSQLServerIndexRules(t *testing.T) {
	params := config.DefaultInspectParams()
	params.CHECK_UNIQ_INDEX_PREFIX = true
	params.CHECK_SECONDARY_INDEX_PREFIX = true
	params.UNQI_INDEX_PREFIX = "UK_"
	params.SECONDARY_INDEX_PREFIX = "IDX_"
	params.SECONDARY_INDEX_MAX_KEY_PARTS = 2
	params.ENABLE_REDUNDANT_INDEX = false
	sql := `CREATE TABLE dbo.t_idx (id BIGINT NOT NULL PRIMARY KEY, a INT NULL, b INT NULL, c INT NULL);
CREATE UNIQUE INDEX wrong_uk ON dbo.t_idx (a);
CREATE INDEX idx_ab ON dbo.t_idx (a, b, c);
CREATE INDEX idx_a ON dbo.t_idx (a);`
	keys := collectRuleKeys(runSQLServer(t, sql, params))
	if !hasRuleKey(keys, "limit_uniq_idx_name_fmt") {
		t.Errorf("唯一索引 wrong_uk 应命中 limit_uniq_idx_name_fmt，实际: %v", keys)
	}
	if !hasRuleKey(keys, "limit_single_index_column_count") {
		t.Errorf("(a,b,c) 超过 2 列应命中 limit_single_index_column_count，实际: %v", keys)
	}
	if !hasRuleKey(keys, "limit_tbl_redundant_idx") {
		t.Errorf("idx_a 被 idx_ab 覆盖应命中 limit_tbl_redundant_idx，实际: %v", keys)
	}
}

func TestSQLServerIndexMustHaveName(t *testing.T) {
	params := config.DefaultInspectParams()
	params.CHECK_INDEX_NAME_REQUIRED = true
	params.CHECK_SECONDARY_INDEX_PREFIX = true
	sql := `CREATE TABLE dbo.t_idx2 (id BIGINT NOT NULL PRIMARY KEY, a INT NULL);
CREATE INDEX ON dbo.t_idx2 (a);`
	// CREATE INDEX 不带名字在 T-SQL 里非法，解析会失败 -> 不应崩溃
	if _, err := (&SyntaxInspectService{DbType: "SQLServer", SqlText: sql, InspectParams: params}).runSQLServerInspect(); err != nil {
		t.Fatalf("无法解析的语句不应让整段预审失败: %v", err)
	}
}

// ---------- 准入闸门 ----------

func TestSQLServerSelectIntoForbidden(t *testing.T) {
	params := config.DefaultInspectParams()
	params.ENABLE_CREATE_TABLE_AS = false
	sql := `SELECT id, name INTO dbo.t_new FROM dbo.t_old WHERE id > 0;`
	data := runSQLServer(t, sql, params)
	keys := collectRuleKeys(data)
	if !hasRuleKey(keys, "disable_create_table_as") {
		t.Fatalf("SELECT ... INTO 应命中 disable_create_table_as，实际: %v", keys)
	}
	// 命中闸门后应给出语句级结论（带 category）
	found := false
	for _, row := range data {
		for _, s := range row.Summary {
			if s.RuleKey == "disable_create_table_as" && s.Category != "" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("闸门结论应带上 category（语句级），实际未携带")
	}
}

func TestSQLServerSelectIntoAllowed(t *testing.T) {
	params := config.DefaultInspectParams()
	params.ENABLE_CREATE_TABLE_AS = true
	keys := collectRuleKeys(runSQLServer(t, `SELECT id INTO t_new FROM dbo.t_old`, params))
	if hasRuleKey(keys, "disable_create_table_as") {
		t.Fatalf("规范允许 CTAS 时不应命中 disable_create_table_as，实际: %v", keys)
	}
}

func TestSQLServerViewGate(t *testing.T) {
	params := config.DefaultInspectParams()
	params.ENABLE_CREATE_VIEW = false
	sql := `CREATE VIEW dbo.v_order AS SELECT id FROM dbo.t_order;
CREATE OR ALTER VIEW dbo.v_order AS SELECT id FROM dbo.t_order;
DROP VIEW dbo.v_order;`
	data := runSQLServer(t, sql, params)
	count := 0
	for _, row := range data {
		for _, s := range row.Summary {
			if s.RuleKey == "disable_view_related_op" {
				count++
			}
		}
	}
	if count != 3 {
		t.Fatalf("三条视图语句都应命中 disable_view_related_op，实际命中 %d 条", count)
	}
}

func TestSQLServerRoutineGates(t *testing.T) {
	params := config.DefaultInspectParams()
	params.ENABLE_PROCEDURE_OBJECT = false
	params.ENABLE_FUNCTION_OBJECT = false
	params.ENABLE_TRIGGER_OBJECT = false
	sql := `CREATE PROCEDURE dbo.p_demo AS SELECT 1;
CREATE FUNCTION dbo.f_demo() RETURNS INT AS BEGIN RETURN 1 END;
CREATE TRIGGER dbo.trg_demo ON dbo.t_demo AFTER INSERT AS SELECT 1;`
	keys := collectRuleKeys(runSQLServer(t, sql, params))
	for _, want := range []string{"disable_proc_related_op", "disable_custom_function_related_op", "disable_trigger_related_op"} {
		if !hasRuleKey(keys, want) {
			t.Errorf("应命中 %s，实际: %v", want, keys)
		}
	}
}

func TestSQLServerRoutineAllowedByDefault(t *testing.T) {
	keys := collectRuleKeys(runSQLServer(t, `CREATE PROCEDURE dbo.p_demo AS SELECT 1`, config.DefaultInspectParams()))
	for _, k := range keys {
		if strings.HasPrefix(k, "disable_") {
			t.Fatalf("默认允许存储过程，不应命中 %s", k)
		}
	}
}

// ---------- DML ----------

func TestSQLServerDMLWhereAndTopRules(t *testing.T) {
	params := config.DefaultInspectParams()
	params.DML_MUST_HAVE_WHERE = true
	params.DML_MAX_LIMIT_ROWS = 100
	params.DML_DISABLE_ORDERBY = true
	params.MAX_WHERE_IN_ELEMENTS = 3
	sql := `DELETE FROM dbo.t_order;
UPDATE TOP (500) dbo.t_order SET status = 1 WHERE id > 0;
DELETE TOP (10) FROM dbo.t_order WHERE id IN (1,2,3,4,5);
DELETE TOP (10) FROM dbo.t_order WHERE id = 1 ORDER BY id;`
	keys := collectRuleKeys(runSQLServer(t, sql, params))
	if !hasRuleKey(keys, "update_delete_stmt_suggest_where") {
		t.Errorf("无 WHERE 的 DELETE 应命中 update_delete_stmt_suggest_where，实际: %v", keys)
	}
	if !hasRuleKey(keys, "limit_update_delete_data_size") {
		t.Errorf("TOP (500) 超过 100 应命中 limit_update_delete_data_size，实际: %v", keys)
	}
	if !hasRuleKey(keys, "limit_where_in_element_count") {
		t.Errorf("IN 5 个元素超过 3 应命中 limit_where_in_element_count，实际: %v", keys)
	}
	if !hasRuleKey(keys, "update_delete_stmt_no_order_by_clause") {
		t.Errorf("DELETE TOP (10) ... ORDER BY 应命中 update_delete_stmt_no_order_by_clause，实际: %v", keys)
	}
}

func TestSQLServerWhereConditionRules(t *testing.T) {
	params := config.DefaultInspectParams()
	params.CHECK_WHERE_LEADING_WILDCARD = true
	params.CHECK_WHERE_NOT_IN_NOT_LIKE = true
	params.CHECK_WHERE_OR_CONDITION = true
	params.CHECK_WHERE_LIKE_WITHOUT_WILDCARD = true
	params.DML_MUST_HAVE_WHERE = true
	sql := `UPDATE dbo.t_order SET status = 1
WHERE name LIKE '%abc' OR code NOT IN ('a','b') OR tag LIKE 'abc';`
	keys := collectRuleKeys(runSQLServer(t, sql, params))
	for _, want := range []string{
		"where_avoid_leading_wildcard",
		"where_avoid_not_in_not_like",
		"where_or_condition_check",
		"where_like_without_wildcard",
	} {
		if !hasRuleKey(keys, want) {
			t.Errorf("应命中 %s，实际: %v", want, keys)
		}
	}
}

func TestSQLServerUpdateOnlyRules(t *testing.T) {
	params := config.DefaultInspectParams()
	params.CHECK_UPDATE_SET_SEPARATOR = true
	params.CHECK_UPDATE_MULTI_TABLE_COLUMN_PREFIX = true
	params.CHECK_UPDATE_UPDATES_PK = true
	params.CHECK_UPDATE_UPDATES_UNIQUE_KEY = true
	params.AVOID_UPDATE_COLUMNS = "create_time"
	params.UPDATE_TIME_COLUMNS = "update_time"
	params.MAX_UPDATE_DELETE_JOIN_TABLES = 5
	meta := &sqlServerStubMeta{
		pk:      map[string][]string{"t_order": {"id"}},
		unique:  map[string][]string{"t_order": {"order_no"}},
		tables:  map[string]bool{"t_order": true},
		columns: map[string]bool{},
	}
	// 多表 UPDATE：SET 里 status 未带表前缀（T-SQL 会报 209 列名歧义）
	sql := `UPDATE o SET status = 1, o.create_time = getdate() FROM dbo.t_order o JOIN dbo.t_user u ON u.id = o.user_id WHERE o.id = 1;
UPDATE dbo.t_order SET id = 2, order_no = 'X' WHERE id = 1;
UPDATE dbo.t_order SET status = 1 AND remark = 'x' WHERE id = 1;`
	keys := collectRuleKeys(runSQLServerWithMeta(t, sql, params, meta))
	for _, want := range []string{
		"update_multi_table_column_prefix_check",
		"update_stmt_check_updates_pk",
		"update_stmt_check_updates_unique_key",
		"update_stmt_avoid_update_create_time_column",
		"update_stmt_set_separator_check",
	} {
		if !hasRuleKey(keys, want) {
			t.Errorf("应命中 %s，实际: %v", want, keys)
		}
	}
}

// TestSQLServerUpdateTargetAliasResolved 多表 UPDATE 的目标是别名时，结论与探测都应指向真实表名
//
// T-SQL 多表更新写成 `UPDATE o SET ... FROM dbo.t_order o JOIN ...`：
// 若直接把 `o` 当表名，提示文案会写成「表`o`」，sys.objects 探测也永远查不到。
func TestSQLServerUpdateTargetAliasResolved(t *testing.T) {
	params := config.DefaultInspectParams()
	params.CHECK_UPDATE_SET_SEPARATOR = true
	params.CHECK_INSERT_OBJECT_EXISTS = true
	meta := &sqlServerStubMeta{
		tables:  map[string]bool{"t_order": true},
		columns: map[string]bool{},
	}
	data := runSQLServerWithMeta(t,
		`UPDATE o SET status = 1 AND remark = 'x' FROM dbo.t_order o JOIN dbo.t_user u ON u.id = o.user_id WHERE o.id = 1`,
		params, meta)
	found := false
	for _, row := range data {
		for _, s := range row.Summary {
			if s.RuleKey != "update_stmt_set_separator_check" {
				continue
			}
			found = true
			if !strings.Contains(s.Message, "t_order") {
				t.Fatalf("结论应指向真实表名 t_order，实际文案: %s", s.Message)
			}
			if strings.Contains(s.Message, "`o`") {
				t.Fatalf("结论不应把别名 o 当表名: %s", s.Message)
			}
		}
	}
	if !found {
		t.Fatalf("未命中 update_stmt_set_separator_check")
	}
	// 别名还原后，表存在性探测不应误报（t_order 在桩里是存在的）
	if hasRuleKey(collectRuleKeys(data), "update_stmt_check_column_exists") {
		t.Fatalf("别名已还原为真实表名 t_order，不应报表/列不存在")
	}
}

func TestSQLServerInsertRules(t *testing.T) {
	params := config.DefaultInspectParams()
	params.CHECK_INSERT_SPECIFY_COLUMNS = true
	params.CHECK_INSERT_COLUMN_NO_DUP = true
	params.CHECK_INSERT_COLUMNS_MATCH_VALUES = true
	params.CHECK_INSERT_AVOID_SYSDATE = true
	params.MAX_INSERT_ROWS = 2
	params.CHECK_INSERT_OBJECT_EXISTS = false
	sql := `INSERT INTO dbo.t_order VALUES (1, 'a');
INSERT INTO dbo.t_order (id, id) VALUES (1, 2);
INSERT INTO dbo.t_order (id, name) VALUES (1);
INSERT INTO dbo.t_order (id, create_time) VALUES (1, GETDATE()), (2, GETDATE()), (3, GETDATE());`
	keys := collectRuleKeys(runSQLServer(t, sql, params))
	for _, want := range []string{
		"insert_stmt_specify_columns",
		"insert_stmt_col_name_no_dup",
		"insert_stmt_columns_match_values",
		"insert_stmt_avoid_sysdate_func",
		"limit_insert_values_row_count",
	} {
		if !hasRuleKey(keys, want) {
			t.Errorf("应命中 %s，实际: %v", want, keys)
		}
	}
}

func TestSQLServerInsertObjectExists(t *testing.T) {
	params := config.DefaultInspectParams()
	params.CHECK_INSERT_OBJECT_EXISTS = true
	meta := &sqlServerStubMeta{
		tables:  map[string]bool{"t_order": false},
		columns: map[string]bool{},
	}
	keys := collectRuleKeys(runSQLServerWithMeta(t,
		`INSERT INTO dbo.t_order (id, name) VALUES (1, 'a')`, params, meta))
	if !hasRuleKey(keys, "insert_stmt_check_column_exists") {
		t.Fatalf("目标表不存在应命中 insert_stmt_check_column_exists，实际: %v", keys)
	}
}

func TestSQLServerDeleteTableExists(t *testing.T) {
	params := config.DefaultInspectParams()
	params.DML_MUST_HAVE_WHERE = false
	params.CHECK_DELETE_TABLE_EXISTS = true
	meta := &sqlServerStubMeta{tables: map[string]bool{"t_order": false}}
	keys := collectRuleKeys(runSQLServerWithMeta(t,
		`DELETE FROM dbo.t_order WHERE id = 1`, params, meta))
	if hasRuleKey(keys, "update_delete_stmt_suggest_where") {
		// 该参数已关闭，不应命中
		t.Errorf("DML_MUST_HAVE_WHERE 关闭时不应命中 update_delete_stmt_suggest_where，实际: %v", keys)
	}
	if !hasRuleKey(keys, "delete_stmt_check_table_exists") {
		t.Errorf("DELETE 目标表不存在应命中 delete_stmt_check_table_exists，实际: %v", keys)
	}
}

// ---------- 对象存在性 ----------

func TestSQLServerCreateTableExists(t *testing.T) {
	params := config.DefaultInspectParams()
	params.CHECK_TABLE_PRIMARY_KEY = false
	meta := &sqlServerStubMeta{tables: map[string]bool{"t_order": true}, columns: map[string]bool{}}
	data := runSQLServerWithMeta(t, `CREATE TABLE dbo.t_order (id BIGINT NOT NULL)`, params, meta)
	keys := collectRuleKeys(data)
	if !hasRuleKey(keys, "create_tbl_must_not_exist") {
		t.Fatalf("表已存在应命中 create_tbl_must_not_exist，实际: %v", keys)
	}
	for _, row := range data {
		for _, s := range row.Summary {
			if s.RuleKey == "create_tbl_must_not_exist" && s.Category == "" {
				t.Fatalf("create_tbl_must_not_exist 应带对象冲突类别")
			}
		}
	}
}

func TestSQLServerAlterTableObjectRules(t *testing.T) {
	params := config.DefaultInspectParams()
	meta := &sqlServerStubMeta{
		tables:  map[string]bool{"t_order": true},
		columns: map[string]bool{"t_order.ghost_col": false},
		indexes: map[string]bool{"t_order.idx_missing": false},
	}
	sql := `ALTER TABLE dbo.t_order DROP COLUMN ghost_col;
ALTER TABLE dbo.t_order ALTER COLUMN ghost_col NVARCHAR(10) NULL;
ALTER TABLE dbo.t_order DROP INDEX idx_missing;
ALTER TABLE dbo.t_not_exist ADD COLUMN a INT NULL;`
	keys := collectRuleKeys(runSQLServerWithMeta(t, sql, params, meta))
	if !hasRuleKey(keys, "obj_col_must_exist") {
		t.Errorf("DROP/ALTER 不存在的列应命中 obj_col_must_exist，实际: %v", keys)
	}
	if !hasRuleKey(keys, "obj_idx_must_exist") {
		t.Errorf("DROP 不存在的索引应命中 obj_idx_must_exist，实际: %v", keys)
	}
	if !hasRuleKey(keys, "obj_tbl_must_exist") {
		t.Errorf("ALTER 不存在的表应命中 obj_tbl_must_exist，实际: %v", keys)
	}
}

func TestSQLServerSpRenameRules(t *testing.T) {
	params := config.DefaultInspectParams()
	meta := &sqlServerStubMeta{
		tables:  map[string]bool{"t_order": true, "t_order_new": true},
		columns: map[string]bool{"t_order.new_col": true},
		indexes: map[string]bool{"t_order.idx_new": true},
	}
	sql := `EXEC sp_rename 'dbo.t_order', 't_order_new';
EXEC sp_rename 'dbo.t_order.old_col', 'new_col', 'COLUMN';
EXEC sp_rename 'dbo.t_order.idx_old', 'idx_new', 'INDEX';`
	keys := collectRuleKeys(runSQLServerWithMeta(t, sql, params, meta))
	if !hasRuleKey(keys, "obj_tbl_must_not_exist") {
		t.Errorf("改名目标表已存在应命中 obj_tbl_must_not_exist，实际: %v", keys)
	}
	if !hasRuleKey(keys, "obj_col_must_not_exist") {
		t.Errorf("改名目标列已存在应命中 obj_col_must_not_exist，实际: %v", keys)
	}
	if !hasRuleKey(keys, "obj_idx_must_not_exist") {
		t.Errorf("改名目标索引已存在应命中 obj_idx_must_not_exist，实际: %v", keys)
	}
}

func TestSQLServerViewExists(t *testing.T) {
	params := config.DefaultInspectParams()
	params.ENABLE_CREATE_VIEW = true
	meta := &sqlServerStubMeta{tables: map[string]bool{"v_order": true}, columns: map[string]bool{}}
	keys := collectRuleKeys(runSQLServerWithMeta(t,
		`CREATE VIEW dbo.v_order AS SELECT 1 AS a`, params, meta))
	if !hasRuleKey(keys, "obj_view_must_not_exist") {
		t.Fatalf("视图已存在应命中 obj_view_must_not_exist，实际: %v", keys)
	}
}

// ---------- 连库失败时一律跳过 ----------

func TestSQLServerMetaUnavailableSkipsObjectRules(t *testing.T) {
	params := sqlServerProdLikeParams()
	meta := &sqlServerStubMeta{failAll: true}
	data := runSQLServerWithMeta(t, `CREATE TABLE dbo.t_order (id BIGINT NOT NULL PRIMARY KEY)`, params, meta)
	keys := collectRuleKeys(data)
	for _, k := range []string{"create_tbl_must_not_exist", "obj_tbl_must_exist", "insert_stmt_check_column_exists"} {
		if hasRuleKey(keys, k) {
			t.Fatalf("元信息不可用时不应产出 %s（宁可漏报，绝不误报），实际: %v", k, keys)
		}
	}
}

// ---------- GO 批处理与合并 ALTER ----------

func TestSQLServerGoBatchSeparator(t *testing.T) {
	params := config.DefaultInspectParams()
	params.ENABLE_MERGE_ALTER_TABLE = true
	sql := "CREATE TABLE dbo.t1 (id BIGINT NOT NULL)\nGO\nALTER TABLE dbo.t1 ADD a INT NULL\nGO\nALTER TABLE dbo.t1 ADD b INT NULL\nGO"
	keys := collectRuleKeys(runSQLServer(t, sql, params))
	if !hasRuleKey(keys, "merge_multiple_alter_stmt_same_table") {
		t.Fatalf("GO 分隔后应正确识别两条 ALTER 并命中合并建议，实际: %v", keys)
	}
}

func TestSQLServerMergeAlterSameTable(t *testing.T) {
	params := config.DefaultInspectParams()
	params.ENABLE_MERGE_ALTER_TABLE = true
	sql := `ALTER TABLE dbo.t1 ADD a INT NULL;
ALTER TABLE dbo.t1 ADD b INT NULL;`
	keys := collectRuleKeys(runSQLServer(t, sql, params))
	if !hasRuleKey(keys, "merge_multiple_alter_stmt_same_table") {
		t.Fatalf("同表两条 ALTER 应命中 merge_multiple_alter_stmt_same_table，实际: %v", keys)
	}
}

// ---------- 索引使用与影响行数（连库判定） ----------

func TestSQLServerMustUseIndexAndAffectedRows(t *testing.T) {
	params := config.DefaultInspectParams()
	params.CHECK_DML_MUST_USE_INDEX = true
	params.MAX_AFFECTED_ROWS = 100
	meta := &sqlServerStubMeta{
		leading:    map[string][]string{"t_order": {"id"}},
		countWhere: map[string]int{"t_order": 5000},
		tables:     map[string]bool{"t_order": true},
		columns:    map[string]bool{},
	}
	keys := collectRuleKeys(runSQLServerWithMeta(t,
		`UPDATE dbo.t_order SET status = 1 WHERE name = 'x'`, params, meta))
	if !hasRuleKey(keys, "update_delete_stmt_must_use_idx") {
		t.Errorf("WHERE 未命中索引首列应命中 update_delete_stmt_must_use_idx，实际: %v", keys)
	}
	if !hasRuleKey(keys, "limit_update_delete_affected_rows") {
		t.Errorf("影响行数 5000 超过 100 应命中 limit_update_delete_affected_rows，实际: %v", keys)
	}
}
