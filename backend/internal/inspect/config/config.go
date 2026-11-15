package config

import "strings"

type DisableTablesAudit struct {
	DB     string   // 库名
	Tables []string // 表名
	Reason string   // 原因
}

type InspectParams struct {
	// TABLE
	MAX_TABLE_NAME_LENGTH                int                 // 表名的长度
	CHECK_TABLE_COMMENT                  bool                // 检查表是否有注释
	TABLE_COMMENT_LENGTH                 int                 // 表的注释长度
	CHECK_TABLE_IDENTIFIER               bool                // 表名基础字符集校验[a-zA-Z0-9_]（规则 limit_tbl_name_case）
	CHECK_COLUMN_IDENTIFIER              bool                // 列名基础字符集校验[a-zA-Z0-9_]（规则 limit_col_name_case）
	CHECK_INDEX_IDENTIFIER               bool                // 索引名基础字符集校验[a-zA-Z0-9_]（规则 limit_uniq_idx_name_fmt / limit_normal_idx_name_fmt / index_must_have_name）
	COLUMN_NAME_CASE                     string              // 列名大小写要求：lower/upper（规则 limit_col_name_case 配置，默认 lower；仅列名检查消费）
	TABLE_NAME_CASE                      string              // 表名大小写要求：lower/upper（规则 limit_tbl_name_case 配置，默认 lower；仅表名检查消费）
	CHECK_IDENTIFER_KEYWORD              bool                // 对象名是否可以使用关键字
	IDENTIFIER_KEYWORDS                  []string            // 关键字检查使用的关键字列表（规则 col_name_no_是keyword 配置；nil=默认全集，[]=不拦截任何关键字）
	CHECK_TABLE_CHARSET                  bool                // 是否检查表的字符集和排序规则
	TABLE_SUPPORT_CHARSET                []map[string]string // 表支持的字符集
	CHECK_TABLE_COLLATION                bool                // 是否检查表的排序规则（规则 limit_tbl_collation）
	TABLE_SUPPORT_COLLATION              []string            // 表支持的排序规则白名单（小写，规则 limit_tbl_collation）
	TABLE_REQUIRED_COLUMNS               []string            // 表必须包含的列（小写，规则 tbl_需要include_某些col；空表示不检查）
	CHECK_DATABASE_CHARSET               bool                // 是否检查库的字符集（规则 limit_库的charset）
	DATABASE_SUPPORT_CHARSET             []map[string]string // 库支持的字符集
	CHECK_TABLE_ENGINE                   bool                // 是否检查表的存储引擎
	TABLE_SUPPORT_ENGINE                 []string            // 表支持的存储引擎
	ENABLE_PARTITION_TABLE               bool                // 是否启用分区表
	CHECK_TABLE_PRIMARY_KEY              bool                // 检查表是否有主键
	CHECK_TABLE_AUDIT_TYPE_COLUMNS       bool                // 启用审计类型的字段(col1 datetime DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP && col2 datetime DEFAULT CURRENT_TIMESTAMP)
	ENABLE_CREATE_TABLE_AS               bool                // 是否允许create table as语法
	ENABLE_CREATE_TABLE_LIKE             bool                // 是否允许create table like语法
	ENABLE_FOREIGN_KEY                   bool                // 是否启用外键
	CHECK_TABLE_AUTOINCREMENT_INIT_VALUE bool                // 检查建表是自增列初始值是否为1
	TABLE_AUTOINCREMENT_INIT_VALUE       int                 // 建表自增初始值上限（规则 limit_建tbl_auto_inc_init，默认1）
	CHECK_AUTOINCREMENT_COL_NAME_IS_ID   bool                // 限制自增列名字为 ID（规则 limit_auto_inc_col_name_字is_id）
	CHECK_AUTOINCREMENT_COL_UNSIGNED     bool                // 限制自增列为无符号（规则 limit_auto_inc_col_is_unsigned）
	ENABLE_CREATE_VIEW                   bool                // 是否支持创建和使用视图
	// 对象类能力开关（SQL Server 段规则消费：disable_procedure_related_op 等）。
	// 默认 true=允许；规范中开启对应「禁止…操作」规则时置为 false。
	// 之所以只由 SQL Server 引擎读取：MySQL/PG/Oracle 段从未实现这三条规则的判定，
	// 加进来只是为了让新数据库类型能真正落地「禁止存储过程/函数/触发器」规范。
	ENABLE_PROCEDURE_OBJECT bool
	ENABLE_FUNCTION_OBJECT  bool
	ENABLE_TRIGGER_OBJECT   bool
	INNODB_ROW_FORMAT       []string // InnoDB表支持的行格式
	MAX_TABLE_COLUMNS       int      // 表最多允许定义的列数（规则 limit_tbl_col_的count）
	// COLUMN
	MAX_COLUMN_NAME_LENGTH               int      // 列名的长度
	CHECK_COLUMN_CHARSET                 bool     // 是否检查列的字符集（规则 no_set_col_的charset：不能显式设置列的字符集）
	CHECK_COLUMN_COLLATION               bool     // 是否检查列的校对集（规则 no_set_col_的collate：不能显式设置列的校对集）
	DISABLE_COLUMN_ZEROFILL              bool     // 是否禁止列使用 ZEROFILL 属性（规则 col_禁用_zerofill_属性）
	CHECK_COLUMN_COMMENT                 bool     // 是否检查列的注释
	COLUMN_MAX_CHAR_LENGTH               int      // char长度大于N的时候需要改为varchar
	MAX_VARCHAR_LENGTH                   int      // 最大允许定义的varchar长度
	ENABLE_COLUMN_BLOB_TYPE              bool     // 是否允许列的类型为BLOB/TEXT
	ENABLE_COLUMN_JSON_TYPE              bool     // 是否允许列的类型为JSON
	ENABLE_COLUMN_BIT_TYPE               bool     // 是否允许列的类型为BIT
	ENABLE_COLUMN_TIMESTAMP_TYPE         bool     // 是否允许列的类型为TIMESTAMP
	CHECK_PRIMARYKEY_USE_BIGINT          bool     // 主键列类型是否启用白名单校验
	CHECK_PRIMARYKEY_USE_AUTO_INCREMENT  bool     // 主键是否定义为自增
	PRIMARYKEY_SUPPORT_TYPES             []string // 主键列类型白名单（规则 limit_pk_col_type 配置，空则回退 bigint/int）
	ENABLE_COLUMN_NOT_NULL               bool     // 是否允许列定义为NOT NULL
	ENABLE_COLUMN_TIME_NULL              bool     // 是否允许时间类型设置为NULL
	CHECK_COLUMN_DEFAULT_VALUE           bool     // 列必须要有默认值
	CHECK_COLUMN_FLOAT_DOUBLE            bool     // 将float/double转成int/bigint/decimal等
	CHECK_COLUMN_FORBIDDEN_TYPES         bool     // 是否限制列不能使用部分数据类型
	COLUMN_FORBIDDEN_TYPES               []string // 禁止使用的数据类型列表（小写），规则 limit_col_no_use_partial_数据type
	ENABLE_COLUMN_TYPE_CHANGE            bool     // 是否允许变更列类型
	ENABLE_COLUMN_TYPE_CHANGE_COMPATIBLE bool     // 允许tinyint-> int、int->bigint、char->varchar等
	ENABLE_COLUMN_CHANGE_COLUMN_NAME     bool     // 是否允许CHANGE修改列名操作
	// INDEX
	CHECK_UNIQ_INDEX_PREFIX       bool   // 是否检查唯一索引前缀,如唯一索引必须以uniq_为前缀
	CHECK_INDEX_NAME_REQUIRED     bool   // 索引必须显式命名（规则 idx_需要set_name）
	CHECK_SECONDARY_INDEX_PREFIX  bool   // 是否检查二级索引前缀,如普通索引必须以idx_为前缀
	CHECK_FULLTEXT_INDEX_PREFIX   bool   // 是否检查全文索引前缀,如全文索引必须以full_为前缀
	UNQI_INDEX_PREFIX             string // 定义唯一索引前缀，不区分大小写
	UNIQ_INDEX_REGEX              string // 定义唯一索引名称必须匹配的正则表达式（规则 limit_uniq_idx_name_fmt 配置，默认 uk_\w+）
	SECONDARY_INDEX_PREFIX        string // 定义二级索引前缀，不区分大小写
	SECONDARY_INDEX_REGEX         string // 定义普通（二级）索引名称必须匹配的正则表达式（规则 limit_normal_idx_name_fmt 配置，未配则为空 => 回退前缀）
	FULLTEXT_INDEX_PREFIX         string // 定义全文索引前缀，不区分大小写
	SECONDARY_INDEX_MAX_KEY_PARTS int    // 组成二级索引的列数不能超过指定的个数,包括唯一索引
	PRIMARYKEY_MAX_KEY_PARTS      int    // 组成主键索引的列数不能超过指定的个数
	MAX_INDEX_KEYS                int    // 最多有N个索引,包括唯一索引/二级索引
	ENABLE_INDEX_RENAME           bool   // 是否允许rename索引名
	ENABLE_REDUNDANT_INDEX        bool   // 是否允许冗余索引
	// ALTER
	ENABLE_DROP_COLS         bool // 是否允许DROP列
	ENABLE_DROP_INDEXES      bool // 是否允许DROP索引
	ENABLE_DROP_PRIMARYKEY   bool // 是否允许DROP主键
	ENABLE_DROP_TABLE        bool // 是否允许DROP TABLE
	ENABLE_TRUNCATE_TABLE    bool // 是否允许TRUNCATE TABLE
	ENABLE_RENAME_TABLE_NAME bool // 是否允许rename表名
	// 同一张表的多条 ALTER 是否合并为一条（MySQL / PostgreSQL 引擎共用：
	// 规则 mysql.struct.merge_multiple_alter_stmt_same_table 与 postgres 版都映射到它）
	ENABLE_MERGE_ALTER_TABLE      bool
	ENABLE_TIDB_MERGE_ALTER_TABLE bool // TiDB同一个表的多个ALTER是否合并为单条语句
	// DML
	DML_MUST_HAVE_WHERE bool // DML语句必须有where条件
	// UPDATE/DELETE 语句必须使用索引（规则 update_delete_stmt_must_use_idx）。
	// 开启后由 EXPLAIN 输出判定：任一被访问表未使用索引（key 为空）即告警；
	// TiDB / PG 的 EXPLAIN 输出没有等价列，判定会自动跳过，不会误报。
	CHECK_DML_MUST_USE_INDEX bool
	DML_DISABLE_LIMIT        bool   // DML语句中不允许有LIMIT
	DML_DISABLE_ORDERBY      bool   // DML语句中不允许有orderby
	DML_DISABLE_SUBQUERY     bool   // DML语句不能有子查询
	CHECK_DML_JOIN_WITH_ON   bool   // DML的JOIN语句必须有ON语句
	EXPLAIN_RULE             string // explain判断受影响行数时使用的规则("first", "max")。 "first": 使用第一行的explain结果作为受影响行数, "max": 使用explain结果中的最大值作为受影响行数
	MAX_AFFECTED_ROWS        int    // 最大影响行数，默认100
	MAX_INSERT_ROWS          int    // 一次最多允许insert的行, eg: insert into tbl(col,...) values(row1), (row2)...
	// UPDATE/DELETE 语句多表关联的最大表数（含主表）；0=不限制（规则关闭时的取值）
	MAX_UPDATE_DELETE_JOIN_TABLES int
	// UPDATE/DELETE 语句 LIMIT 允许的最大行数；0=不限制（规则关闭时的取值）
	DML_MAX_LIMIT_ROWS int
	// UPDATE 语句建议同步更新的「修改时间」列（逗号分隔）；空=不检查（规则关闭时的取值）
	UPDATE_TIME_COLUMNS string
	// UPDATE 语句不建议更新的列（规则 update_stmt_avoid_update_create_time_column，逗号分隔）。
	// 创建时间属于数据元信息、具备不可变更属性，更新会破坏审计追溯能力；
	// 默认 create_time，可配置为 gmt_create / created_at 等。空=不检查（规则关闭时的取值）。
	AVOID_UPDATE_COLUMNS string
	// WHERE 条件中 IN 子句允许的最大元素个数；0=不限制（规则关闭时的取值）
	MAX_WHERE_IN_ELEMENTS int
	// UPDATE 语句 SET 多个列之间的分隔符检查：多列之间用 AND 分隔属非法语法（规则 update_stmt_set_separator_check）。
	// 引擎按 AST 判定，合法的布尔表达式 `SET a = (b>1 AND c<2)` 不会误报。
	CHECK_UPDATE_SET_SEPARATOR bool
	// UPDATE 多表时 SET 的列是否必须指定表前缀（规则 update_multi_table_column_prefix_check）。
	// 多表 UPDATE 中列名可能歧义，要求写成 `t.col = ...`。
	CHECK_UPDATE_MULTI_TABLE_COLUMN_PREFIX bool
	// UPDATE 语句检测是否更新了主键（规则 update_stmt_check_updates_pk）。
	// 依赖目标表的表结构（SHOW CREATE TABLE）；表结构不可读时跳过，不误报。
	CHECK_UPDATE_UPDATES_PK bool
	// UPDATE 语句检测是否更新了唯一键（规则 update_stmt_check_updates_unique_key）。
	// 依赖目标表的表结构（SHOW CREATE TABLE）；表结构不可读时跳过，不误报。主键由
	// CHECK_UPDATE_UPDATES_PK 单独覆盖，这里不重复告警。
	CHECK_UPDATE_UPDATES_UNIQUE_KEY bool
	// 表存在性（规则 delete_stmt_check_table_exists）：MySQL 走 DESC、PostgreSQL 走 information_schema。
	CHECK_DELETE_TABLE_EXISTS bool
	// INSERT 目标对象存在性（规则 insert_stmt_check_column_exists）：
	// 表存在性 MySQL 走 DESC、PostgreSQL 走 information_schema；字段存在性依赖
	// SHOW CREATE TABLE，仅 MySQL 可用（PG 读不到表结构时只判表）。
	CHECK_INSERT_OBJECT_EXISTS bool
	// WHERE 条件中不建议使用前通配符查找：LIKE '%xxx'（规则 where_avoid_leading_wildcard）
	CHECK_WHERE_LEADING_WILDCARD bool
	// WHERE 条件中不建议索引字段包含数学运算或函数运算（规则 where_avoid_index_col_arithmetic_or_func）。
	// 依赖目标表的表结构识别索引列；表结构不可读时跳过，不误报。
	CHECK_WHERE_INDEX_COL_CALC bool
	// WHERE 条件中不建议使用反向查询 NOT IN / NOT LIKE（规则 where_avoid_not_in_not_like）
	CHECK_WHERE_NOT_IN_NOT_LIKE bool
	// WHERE 条件中检测是否通过 OR 操作符连接过滤条件（规则 where_or_condition_check）
	CHECK_WHERE_OR_CONDITION bool
	// WHERE 条件中检测字段是否存在隐式类型转换（规则 where_implicit_type_cast_check）。
	// 依赖目标表的表结构识别列类型；表结构不可读时跳过，不误报。
	CHECK_WHERE_IMPLICIT_CAST bool
	// WHERE 条件中检测没有通配符的 LIKE 语句（规则 where_like_without_wildcard）
	CHECK_WHERE_LIKE_WITHOUT_WILDCARD bool
	// INSERT 语句字段名不能重复（规则 insert_stmt_col_name_no_dup）
	CHECK_INSERT_COLUMN_NO_DUP bool
	// INSERT 语句建议指定字段列表（规则 insert_stmt_specify_columns）
	CHECK_INSERT_SPECIFY_COLUMNS bool
	// INSERT 语句字段列表和值列表要匹配（规则 insert_stmt_columns_match_values）。
	// 有字段列表时与列表长度比对；无字段列表时与真实表列数比对（表结构不可读时跳过）。
	CHECK_INSERT_COLUMNS_MATCH_VALUES bool
	// INSERT 语句不能为 NOT NULL 列插入 NULL 值（规则 insert_stmt_not_null_column_null_value）。
	// 依赖目标表的表结构（SHOW CREATE TABLE）；表结构不可读时跳过，不误报。
	CHECK_INSERT_NOT_NULL_COLUMN_NULL bool
	// INSERT 语句不建议使用 SYSDATE() 函数（规则 insert_stmt_avoid_sysdate_func）。
	// SYSDATE() 返回的是执行时刻的时间，非语句开始时间，会导致主从数据不一致。
	CHECK_INSERT_AVOID_SYSDATE bool
	DISABLE_REPLACE            bool // 是否禁用replace语句
	DISABLE_INSERT_INTO_SELECT bool // 是否禁用insert/replace into select语法
	DISABLE_ON_DUPLICATE       bool // 是否禁止insert on duplicate语法
}

// normalizeForbiddenTypes 把禁用类型列表统一归一化为小写集合，便于快速查找
func normalizeForbiddenTypes(types []string) map[string]struct{} {
	set := make(map[string]struct{}, len(types))
	for _, t := range types {
		s := strings.ToLower(strings.TrimSpace(t))
		if s != "" {
			set[s] = struct{}{}
		}
	}
	return set
}

// IsForbiddenMySQLType 判断给定的 MySQL 类型名是否被当前规范禁用
func (p *InspectParams) IsForbiddenMySQLType(tpName string) bool {
	if !p.CHECK_COLUMN_FORBIDDEN_TYPES || len(p.COLUMN_FORBIDDEN_TYPES) == 0 {
		return false
	}
	set := normalizeForbiddenTypes(p.COLUMN_FORBIDDEN_TYPES)
	_, ok := set[strings.ToLower(strings.TrimSpace(tpName))]
	return ok
}

// IsForbiddenPGType 判断给定的 PostgreSQL 类型名（含常见别名）是否被当前规范禁用
func (p *InspectParams) IsForbiddenPGType(typeName string) bool {
	if !p.CHECK_COLUMN_FORBIDDEN_TYPES || len(p.COLUMN_FORBIDDEN_TYPES) == 0 {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(typeName))
	aliases := map[string]string{
		"character varying": "varchar",
		"character":         "char",
		"bpchar":            "char",
		"int8":              "bigint",
		"int4":              "int",
		"integer":           "int",
		"int2":              "smallint",
		"float8":            "double",
		"float4":            "real",
		"bool":              "boolean",
	}
	if canon, ok := aliases[normalized]; ok {
		normalized = canon
	}
	set := normalizeForbiddenTypes(p.COLUMN_FORBIDDEN_TYPES)
	_, ok := set[normalized]
	return ok
}

// IsForbiddenSQLServerType 判断给定的 SQL Server 类型名（含常见同义词）是否被当前规范禁用。
//
// 与 MySQL/PG 的口径差异：
//   - T-SQL 的类型同义词（integer/int、numeric/decimal、character/char）归一到同一规范名，
//     否则用户配了 decimal 却写 numeric 就会漏判；
//   - 长度/精度后缀在解析层已剥掉（VARCHAR(50) → varchar），这里只处理类型名本身；
//   - `rowversion` 与 `timestamp` 在 SQL Server 里是同一个类型（二进制版本号，不是时间戳），
//     统一归一到 timestamp，避免用户配了 timestamp 却漏掉 rowversion 写法。
func (p *InspectParams) IsForbiddenSQLServerType(typeName string) bool {
	if !p.CHECK_COLUMN_FORBIDDEN_TYPES || len(p.COLUMN_FORBIDDEN_TYPES) == 0 {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(typeName))
	aliases := map[string]string{
		"integer":            "int",
		"numeric":            "decimal",
		"dec":                "decimal",
		"character":          "char",
		"char varying":       "varchar",
		"national char":      "nchar",
		"national character": "nchar",
		"double precision":   "float",
		"rowversion":         "timestamp",
		"binary varying":     "varbinary",
	}
	if canon, ok := aliases[normalized]; ok {
		normalized = canon
	}
	set := normalizeForbiddenTypes(p.COLUMN_FORBIDDEN_TYPES)
	_, ok := set[normalized]
	return ok
}

// 主键列类型白名单（规则 limit_pk_col_type 的 pk_types）的多库字典。
//
// 历史坑：这份字典曾经只有 MySQL 体系类型，导致 PostgreSQL 的 uuid/bigserial/jsonb、
// Oracle 的 number/varchar2/long raw 在前端选不到、后端还会被判为「不在可选范围内」——
// 页面表现为「这条规则怎么都配不了」。因此按数据库家族拆分维护，与前端
// RuleTable.vue 的 TYPE_OPTIONS_BY_DB 逐项保持一致。

// MySQLPrimaryKeyTypes MySQL / TiDB 主键列类型可选值
var MySQLPrimaryKeyTypes = []string{
	"tinyint", "smallint", "mediumint", "int", "integer", "bigint", "serial",
	"decimal", "numeric", "float", "double", "real",
	"char", "varchar", "text", "tinytext", "mediumtext", "longtext",
	"binary", "varbinary", "blob", "tinyblob", "mediumblob", "longblob",
	"date", "time", "datetime", "timestamp", "year",
	"bool", "boolean", "enum", "set", "bit", "json",
	"geometry", "point", "linestring", "polygon",
}

// PostgreSQLPrimaryKeyTypes PostgreSQL 主键列类型可选值
var PostgreSQLPrimaryKeyTypes = []string{
	"smallserial", "serial", "bigserial",
	"smallint", "integer", "bigint", "int", "int2", "int4", "int8",
	"decimal", "numeric", "real", "double precision", "float4", "float8", "money",
	"char", "character", "varchar", "character varying", "text", "citext",
	"bytea", "boolean", "bool", "uuid", "xml", "json", "jsonb",
	"date", "time", "timetz", "timestamp", "timestamptz", "interval",
	"inet", "cidr", "macaddr", "varbit",
}

// OraclePrimaryKeyTypes Oracle 主键列类型可选值
var OraclePrimaryKeyTypes = []string{
	"number", "integer", "int", "float", "binary_float", "binary_double",
	"char", "nchar", "varchar", "varchar2", "nvarchar2",
	"long", "long raw", "raw", "clob", "nclob", "bfile", "blob",
	"date", "timestamp", "timestamp with time zone", "timestamp with local time zone",
	"interval", "rowid", "xmltype",
}

// SQLServerPrimaryKeyTypes SQL Server 主键列类型可选值。
//
// 口径：sys.types 里允许出现在主键中的类型。T-SQL 与 MySQL/Oracle 的两处关键差异：
//   - 没有 unsigned / zerofill 概念，整数类型没有无符号变体；
//   - 字符类型按「字符」计长（VARCHAR 受排序规则影响，NVARCHAR 固定 Unicode），
//     因此不列 MAX 变体 —— 它们不能做主键。
//
// 刻意不收录 TEXT/NTEXT/IMAGE/XML/SQL_VARIANT/GEOGRAPHY/GEOMETRY/HIERARCHYID：
// 这些类型不能建主键，列进来只会让用户配出一条永远无法满足的规则。
var SQLServerPrimaryKeyTypes = []string{
	"tinyint", "smallint", "int", "bigint", "bit",
	"decimal", "numeric", "money", "smallmoney", "float", "real",
	"char", "varchar", "nchar", "nvarchar",
	"binary", "varbinary",
	"date", "time", "datetime", "datetime2", "smalldatetime", "datetimeoffset",
	"uniqueidentifier", "rowversion", "timestamp",
	"sysname",
}

// PrimaryKeyTypesByDB 按数据库类型索引的主键列类型白名单
var PrimaryKeyTypesByDB = map[string][]string{
	"mysql":      MySQLPrimaryKeyTypes,
	"tidb":       MySQLPrimaryKeyTypes,
	"doris":      MySQLPrimaryKeyTypes,
	"postgres":   PostgreSQLPrimaryKeyTypes,
	"postgresql": PostgreSQLPrimaryKeyTypes,
	"oracle":     OraclePrimaryKeyTypes,
	"sqlserver":  SQLServerPrimaryKeyTypes,
	"mssql":      SQLServerPrimaryKeyTypes,
}

// PrimaryKeySupportedTypes 全部可选值的并集（规则未标注数据库 / db_type=all 时使用）。
// 与前端「编辑多个数据库的规则时取并集」的行为保持一致。
var PrimaryKeySupportedTypes = func() []string {
	out := make([]string, 0, 128)
	seen := make(map[string]struct{}, 128)
	for _, group := range [][]string{MySQLPrimaryKeyTypes, PostgreSQLPrimaryKeyTypes, OraclePrimaryKeyTypes, SQLServerPrimaryKeyTypes} {
		for _, t := range group {
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}()

// PrimaryKeyTypesForDB 返回指定数据库的主键列类型可选值；未知数据库回退并集。
// 注意：只用于前端下拉分组/提示口径，**保存校验一律用并集**——同一能力项跨库合并成一行，
// 抽屉里选中的类型会同时写入所有勾选的库，按单库严格校验会把正常保存误拦。
func PrimaryKeyTypesForDB(dbType string) []string {
	key := strings.ToLower(strings.TrimSpace(dbType))
	if list, ok := PrimaryKeyTypesByDB[key]; ok {
		return list
	}
	return PrimaryKeySupportedTypes
}

// IsSupportedPrimaryKeyType 判断类型名是否属于主键列类型可选值（大小写不敏感、忽略首尾空格）。
// 口径 = 三个数据库家族类型的并集，与前端 unionTypeOptions 完全一致。
func IsSupportedPrimaryKeyType(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return true
	}
	for _, t := range PrimaryKeySupportedTypes {
		if t == n {
			return true
		}
	}
	return false
}

// PrimaryKeySupportedTypesText 全部可选值的可读列表（错误提示用）
func PrimaryKeySupportedTypesText() string {
	return strings.Join(PrimaryKeySupportedTypes, ", ")
}
