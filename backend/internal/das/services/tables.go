package services

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/parser"
	"github.com/simajiong/jaysql/pkg/secret"

	"github.com/gin-gonic/gin"
)

type InstanceCfg struct {
	Hostname      string
	Port          int
	User          string
	Password      string
	PlainPassword string
	Schema        string
	DbType        string
	ExtraConfig   []byte
}

type GetTablesService struct {
	*forms.GetTablesForm
	C        *gin.Context
	Username string
}

func (s *GetTablesService) parserUUID() (id string, err error) {
	id = strings.TrimSpace(s.InstanceID)
	if id == "" {
		return "", fmt.Errorf("instance id is empty")
	}
	return id, nil
}

// 验证用户是否有指定schema的权限
func (s *GetTablesService) validatePerms(uuid string) error {
	// 检查库表权限
	var tables []parser.Table
	tables = append(tables, parser.Table{Schema: s.Schema})
	checker := CheckUserPerm{
		UserName:   s.Username,
		InstanceID: uuid,
		Tables:     tables,
	}
	if err := checker.HasSchemaPerms(); err != nil {
		return err
	}
	return nil
}

// 获取MySQL/TiDB的表&字段信息
func (s *GetTablesService) getMySQLMetaData(instanceCfg *InstanceCfg) (data *[]map[string]interface{}, err error) {
	/*
		table_name: test
		table_schema: pmm
		columns: id$$int(10) unsigned$$@@created_at$$datetime(3)$$创建时间@@updated_at$$datetime(3)$$更新时间
	*/
	var query string = fmt.Sprintf(`
		SELECT
			c.table_schema as table_schema,
			c.table_name as table_name,
			group_concat(concat(c.column_name, '$$', c.column_type, "$$", c.column_comment) SEPARATOR '@@') as columns
		from
			information_schema.columns c
			join information_schema.tables t
				on t.table_schema = c.table_schema and t.table_name = c.table_name and t.table_type in ('BASE TABLE', 'SYSTEM VIEW')
		where
			c.table_schema='%s' and c.table_name not regexp '^_(.*)[_ghc|_gho|_del]$'
		group by
			c.table_schema, c.table_name order by c.table_name
		`, instanceCfg.Schema)
	db := dao.DB{
		User:     instanceCfg.User,
		Password: instanceCfg.PlainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Params:   map[string]string{"group_concat_max_len": "4194304"},
		Ctx:      s.C.Request.Context(),
	}
	_, data, err = db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return data, err
	}

	var tablesQuery string = fmt.Sprintf(`
		SELECT
			table_name as table_name,
			table_rows as table_rows,
			CEIL(IFNULL(data_length, 0) / 1024) as data_length_kb,
			engine as engine,
			update_time as update_time,
			table_comment as table_comment
		FROM information_schema.tables
		WHERE table_schema = '%s' and table_name not regexp '^_(.*)[_ghc|_gho|_del]$'
		ORDER BY table_name
	`, instanceCfg.Schema)

	_, tableRows, err := db.Query(tablesQuery)
	if err != nil {
		global.App.Log.Error(err.Error())
		return data, err
	}

	tableMetaByName := make(map[string]map[string]interface{})
	for _, row := range *tableRows {
		tableName, ok := row["table_name"].(string)
		if !ok || tableName == "" {
			continue
		}
		tableMetaByName[tableName] = row
	}

	for _, row := range *data {
		tableName, ok := row["table_name"].(string)
		if !ok || tableName == "" {
			continue
		}
		meta, ok := tableMetaByName[tableName]
		if !ok {
			continue
		}
		row["table_rows"] = meta["table_rows"]
		row["data_length_kb"] = meta["data_length_kb"]
		row["engine"] = meta["engine"]
		row["update_time"] = meta["update_time"]
		row["table_comment"] = meta["table_comment"]
	}

	return data, nil
}

func shouldSkipGhostTable(tableName string) bool {
	name := strings.TrimSpace(tableName)
	if !strings.HasPrefix(name, "_") {
		return false
	}
	return strings.HasSuffix(name, "_ghc") || strings.HasSuffix(name, "_gho") || strings.HasSuffix(name, "_del")
}

func getStringField(row map[string]interface{}, key string) string {
	value, ok := row[key]
	if !ok || value == nil {
		return ""
	}
	return fmt.Sprintf("%v", value)
}

func attachTableMeta(rows *[]map[string]interface{}, tableRows *[]map[string]interface{}) *[]map[string]interface{} {
	tableMetaByName := make(map[string]map[string]interface{})
	for _, row := range *tableRows {
		tableName, ok := row["table_name"].(string)
		if !ok || tableName == "" {
			continue
		}
		tableMetaByName[tableName] = row
	}

	for _, row := range *rows {
		tableName, ok := row["table_name"].(string)
		if !ok || tableName == "" {
			continue
		}
		meta, ok := tableMetaByName[tableName]
		if !ok {
			continue
		}
		row["table_rows"] = meta["table_rows"]
		row["data_length_kb"] = meta["data_length_kb"]
		row["engine"] = meta["engine"]
		row["update_time"] = meta["update_time"]
		row["table_comment"] = meta["table_comment"]
	}
	return rows
}

// 获取Doris的表&字段信息
func (s *GetTablesService) getDorisMetaData(instanceCfg *InstanceCfg) (data *[]map[string]interface{}, err error) {
	var query string = fmt.Sprintf(`
		SELECT
			c.table_schema as table_schema,
			c.table_name as table_name,
			c.column_name as column_name,
			c.column_type as column_type,
			c.column_comment as column_comment
		FROM
			information_schema.columns c
			JOIN information_schema.tables t
				ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
		WHERE
			c.table_schema='%s'
		ORDER BY
			c.table_name, c.ordinal_position
	`, instanceCfg.Schema)
	db := dao.DB{
		User:     instanceCfg.User,
		Password: instanceCfg.PlainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Ctx:      s.C.Request.Context(),
	}
	_, columnRows, err := db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return data, err
	}

	groupedRows := make([]map[string]interface{}, 0)
	tableIndex := make(map[string]int)
	for _, row := range *columnRows {
		tableName := getStringField(row, "table_name")
		if tableName == "" || shouldSkipGhostTable(tableName) {
			continue
		}
		tableSchema := getStringField(row, "table_schema")
		columnExpr := fmt.Sprintf(
			"%s$$%s$$%s",
			getStringField(row, "column_name"),
			getStringField(row, "column_type"),
			getStringField(row, "column_comment"),
		)

		if idx, ok := tableIndex[tableName]; ok {
			groupedRows[idx]["columns"] = groupedRows[idx]["columns"].(string) + "@@" + columnExpr
			continue
		}

		tableIndex[tableName] = len(groupedRows)
		groupedRows = append(groupedRows, map[string]interface{}{
			"table_schema": tableSchema,
			"table_name":   tableName,
			"columns":      columnExpr,
		})
	}

	var tablesQuery string = fmt.Sprintf(`
		SELECT
			table_name as table_name,
			table_rows as table_rows,
			CEIL(IFNULL(data_length, 0) / 1024) as data_length_kb,
			engine as engine,
			update_time as update_time,
			table_comment as table_comment
		FROM information_schema.tables
		WHERE table_schema = '%s' AND table_type = 'BASE TABLE'
		ORDER BY table_name
	`, instanceCfg.Schema)

	_, tableRows, err := db.Query(tablesQuery)
	if err != nil {
		global.App.Log.Error(err.Error())
		return data, err
	}

	filteredTableRows := make([]map[string]interface{}, 0, len(*tableRows))
	for _, row := range *tableRows {
		tableName := getStringField(row, "table_name")
		if tableName == "" || shouldSkipGhostTable(tableName) {
			continue
		}
		filteredTableRows = append(filteredTableRows, row)
	}

	result := groupedRows
	return attachTableMeta(&result, &filteredTableRows), nil
}

// 获取ClickHouse的表&字段信息
func (s *GetTablesService) getClickHouseMetaData(instanceCfg *InstanceCfg) (data *[]map[string]interface{}, err error) {
	/*
		table_name: test
		table_schema: pmm
		columns: I_ID#bigint(18),USER_ID#bigint(20),D_CREATED_AT#datetime
	*/
	var query string = fmt.Sprintf(`
		SELECT
			database as table_schema,
			table as table_name,
			columns
		FROM
		(
			SELECT
				database,
				table,
				groupArray(concat_col) AS col_array,
				arrayStringConcat(col_array, '@@') AS columns
			FROM
				(
					SELECT
						c.database,
						c.table,
						concat(c.name, '$$', c.type, '$$', c.comment) AS concat_col,
						c.name AS column
					FROM
						system.columns c
					JOIN system.tables t
						ON t.database = c.database AND t.name = c.table
					WHERE
						(c.database = '%s') AND (t.engine NOT LIKE '%%View%%')
				)
			GROUP BY
				database,
				table
		)
		ORDER BY table ASC
	`, instanceCfg.Schema)

	db := dao.ClickhouseDB{
		User:     instanceCfg.User,
		Password: instanceCfg.PlainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Ctx:      s.C.Request.Context(),
	}
	_, data, err = db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return data, err
	}
	return data, nil
}

type postgresExtraConfig struct {
	SSLMode string `json:"sslmode"`
}

// postgresTablesQuery 构造 PG / KingbaseES 共用的「表清单 + 列拼接」SQL。
//
// 方言兼容约束（金仓实例展开 Tables 报「加载对象列表失败」的根因）：
//   - 不能用 concat(a,'$$',b,'$$',c)：KingbaseES 的 sys.concat 只有
//     concat(text, text) 定长两参重载（PG 是 variadic "any"），多参调用直接报
//     function sys.concat(...) does not exist；
//   - 改用 || 拼接：PG 与金仓行为一致，NULL 已由 coalesce 兜住，不存在传染；
//   - information_schema 的 column_name / data_type 是域类型（sql_identifier /
//     character_data），显式 ::text 让金仓能匹配到字符串算子。
func postgresTablesQuery(schemaName string) string {
	return fmt.Sprintf(`
		SELECT 
			cols.table_schema as table_schema,
			cols.table_name as table_name,
			string_agg(
				cols.column_name::text || '$$' || cols.data_type::text || '$$' || coalesce(col_description(cls.oid, cols.ordinal_position), ''),
				'@@' order by cols.ordinal_position
			) as columns,
			GREATEST(cls.reltuples::bigint, 0) as table_rows,
			CEIL(pg_total_relation_size(cls.oid) / 1024.0)::bigint as data_length_kb,
			'' as engine,
			'' as update_time,
			coalesce(obj_description(cls.oid, 'pg_class'), '') as table_comment
		FROM 
			information_schema.columns cols
			JOIN pg_namespace nsp ON nsp.nspname = cols.table_schema
			JOIN pg_class cls ON cls.relnamespace = nsp.oid AND cls.relname = cols.table_name
		WHERE 
			cols.table_schema=%s
			AND cls.relkind IN ('r', 'p', 'm', 'f')
		GROUP BY 
			cols.table_schema, cols.table_name, cls.oid, cls.reltuples
		ORDER BY cols.table_name
	`, quoteStringLiteral(schemaName))
}

func (s *GetTablesService) getPostgresMetaData(instanceCfg *InstanceCfg) (data *[]map[string]interface{}, err error) {
	parts := strings.SplitN(instanceCfg.Schema, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("PostgreSQL 资源路径不合法")
	}
	databaseName := parts[0]
	schemaName := parts[1]

	query := postgresTablesQuery(schemaName)

	cfg := postgresExtraConfig{}
	if len(instanceCfg.ExtraConfig) > 0 {
		_ = json.Unmarshal(instanceCfg.ExtraConfig, &cfg)
	}
	db := dao.PostgresDB{
		User:     instanceCfg.User,
		Password: instanceCfg.PlainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Database: databaseName,
		SSLMode:  cfg.SSLMode,
		Ctx:      s.C.Request.Context(),
	}
	_, rows, err := db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return rows, err
	}
	result := make([]map[string]interface{}, 0, len(*rows))
	for _, row := range *rows {
		result = append(result, map[string]interface{}{
			"table_schema":   row["table_schema"],
			"table_name":     row["table_name"],
			"columns":        row["columns"],
			"table_rows":     row["table_rows"],
			"data_length_kb": row["data_length_kb"],
			"engine":         row["engine"],
			"update_time":    row["update_time"],
			"table_comment":  row["table_comment"],
		})
	}
	return &result, nil
}

func (s *GetTablesService) getSQLServerMetaData(instanceCfg *InstanceCfg) (data *[]map[string]interface{}, err error) {
	parts := strings.SplitN(instanceCfg.Schema, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("SQLServer 资源路径不合法")
	}
	databaseName := parts[0]
	schemaName := parts[1]

	db := dao.SQLServerDB{
		User:     instanceCfg.User,
		Password: instanceCfg.PlainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Database: databaseName,
		Ctx:      s.C.Request.Context(),
	}

	query := fmt.Sprintf(`
		SELECT
			c.table_schema as table_schema,
			c.table_name as table_name,
			c.column_name as column_name,
			c.data_type as data_type,
			c.ordinal_position as ordinal_position
		FROM
			information_schema.columns c
			JOIN information_schema.tables t
				ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
		WHERE
			c.table_schema=%s
		ORDER BY c.table_name, c.ordinal_position
	`, quoteStringLiteral(schemaName))

	_, rows, err := db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return nil, err
	}

	type tableKey struct {
		Schema string
		Table  string
	}
	type col struct {
		Name    string
		Type    string
		Comment string
	}
	colsByTable := make(map[tableKey][]col)
	order := make([]tableKey, 0)
	seen := make(map[tableKey]struct{})
	for _, row := range *rows {
		ts, _ := row["table_schema"].(string)
		tn, _ := row["table_name"].(string)
		cn, _ := row["column_name"].(string)
		dt, _ := row["data_type"].(string)
		if ts == "" || tn == "" || cn == "" {
			continue
		}
		key := tableKey{Schema: ts, Table: tn}
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			order = append(order, key)
		}
		colsByTable[key] = append(colsByTable[key], col{Name: cn, Type: dt, Comment: ""})
	}

	result := make([]map[string]interface{}, 0, len(order))
	for _, key := range order {
		colParts := make([]string, 0, len(colsByTable[key]))
		for _, c := range colsByTable[key] {
			colParts = append(colParts, fmt.Sprintf("%s$$%s$$%s", c.Name, c.Type, c.Comment))
		}
		result = append(result, map[string]interface{}{
			"table_schema": key.Schema,
			"table_name":   key.Table,
			"columns":      strings.Join(colParts, "@@"),
		})
	}
	return &result, nil
}

type oracleTableExtraConfig struct {
	ServiceName string `json:"service_name"`
}

// oracleTablesQuery 生成 Oracle/金仓 Oracle 模式的「schema 表 + 列元信息」查询。
//
// 回归背景（2026-11-12）：原 SQL 是一个重构残壳——内联子查询没有别名、外层还引用
// 了不存在的 cc 前缀（cc.comments），Oracle 直接抛 ORA-00904，前端表现为
// 「加载 "HR" 对象列表失败」。现简化为单层查询：*_tab_columns 主表 +
// *_objects 过滤（object_type='TABLE'，排除视图/物化视图）+ *_col_comments
// 左连接取列注释，输出列名 table_schema/table_name/columns 与 MySQL/PG 版本口径一致。
//
// catalog 取 "all" 或 "dba"：Oracle 的 all_* 字典只显示「当前用户有权限的对象」，
// DBA 特权账号未被显式授权 HR 的表时 all_* 一行都拿不到（dba_segments/all_users
// 却都可见），调用方需先查 all 再按空结果降级 dba（见 getOracleMetaData）。
func oracleTablesQuery(catalog string, schema string) string {
	return fmt.Sprintf(`
		SELECT
			c.owner AS table_schema,
			c.table_name AS table_name,
			c.column_name AS column_name,
			c.data_type AS data_type,
			c.column_id AS ordinal_position,
			cc.comments AS column_comment
		FROM %s_tab_columns c
		JOIN %s_objects o
			ON o.owner = c.owner AND o.object_name = c.table_name AND o.object_type = 'TABLE'
		LEFT JOIN %s_col_comments cc
			ON c.owner = cc.owner
			AND c.table_name = cc.table_name
			AND c.column_name = cc.column_name
		WHERE c.owner = %s
		ORDER BY c.table_name, c.column_id
	`, catalog, catalog, catalog, quoteStringLiteral(schema))
}

func (s *GetTablesService) getOracleMetaData(instanceCfg *InstanceCfg) (data *[]map[string]interface{}, err error) {
	schemaName := strings.TrimSpace(instanceCfg.Schema)
	if schemaName == "" {
		return nil, fmt.Errorf("Oracle Schema不能为空")
	}
	cfg := oracleTableExtraConfig{}
	if len(instanceCfg.ExtraConfig) > 0 {
		_ = json.Unmarshal(instanceCfg.ExtraConfig, &cfg)
	}
	db := dao.OracleDB{
		User:          instanceCfg.User,
		Password:      instanceCfg.PlainPassword,
		Host:          instanceCfg.Hostname,
		Port:          instanceCfg.Port,
		ServiceName:   cfg.ServiceName,
		CurrentSchema: schemaName,
		Ctx:           s.C.Request.Context(),
	}

	// Oracle 字典可见性降级：先查 all_*（受授权限制），为空时降级 dba_*
	// （DBA 账号可覆盖全部 schema）；dba_* 无权限报错不上抛，保留 all_* 的
	// 空结果。与 schema_browser.go oracleCatalogRows 同一口径。
	upperSchema := strings.ToUpper(schemaName)
	_, rows, err := db.Query(oracleTablesQuery("all", upperSchema))
	if err == nil && (rows == nil || len(*rows) == 0) {
		if _, dbaRows, dbaErr := db.Query(oracleTablesQuery("dba", upperSchema)); dbaErr == nil {
			rows = dbaRows
		}
	}
	if err != nil {
		global.App.Log.Error(err.Error())
		return nil, err
	}

	type tableKey struct {
		Schema string
		Table  string
	}
	type col struct {
		Name    string
		Type    string
		Comment string
	}
	colsByTable := make(map[tableKey][]col)
	order := make([]tableKey, 0)
	seen := make(map[tableKey]struct{})
	for _, row := range *rows {
		ts, _ := row["table_schema"].(string)
		tn, _ := row["table_name"].(string)
		cn, _ := row["column_name"].(string)
		dt, _ := row["data_type"].(string)
		cc, _ := row["column_comment"].(string)
		if ts == "" || tn == "" || cn == "" {
			continue
		}
		key := tableKey{Schema: ts, Table: tn}
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			order = append(order, key)
		}
		colsByTable[key] = append(colsByTable[key], col{Name: cn, Type: dt, Comment: cc})
	}

	result := make([]map[string]interface{}, 0, len(order))
	for _, key := range order {
		colParts := make([]string, 0, len(colsByTable[key]))
		for _, c := range colsByTable[key] {
			colParts = append(colParts, fmt.Sprintf("%s$$%s$$%s", c.Name, c.Type, c.Comment))
		}
		result = append(result, map[string]interface{}{
			"table_schema": key.Schema,
			"table_name":   key.Table,
			"columns":      strings.Join(colParts, "@@"),
		})
	}
	return &result, nil
}

func (s *GetTablesService) Run() (responseData *[]map[string]interface{}, err error) {
	// 解析UUID
	uuid, err := s.parserUUID()
	if err != nil {
		return responseData, err
	}
	// 验证用户是否有指定schema的权限
	if err = s.validatePerms(uuid); err != nil {
		return responseData, err
	}
	var cfg InstanceCfg
	global.App.DB.Table("insight_instances").Where("`instance_id`=?", s.InstanceID).Take(&cfg)
	// 解密密码
	plainPassword, err := secret.Decrypt(cfg.Password)
	if err != nil {
		return nil, err
	}
	cfg.PlainPassword = plainPassword
	cfg.Schema = s.Schema
	// 先按「产品特有的采集实现」精确匹配（Doris / ClickHouse / SQLServer 各有独立 SQL），
	// 再按「家族」兜底 mysql / postgresql / oracle。
	// OceanBase 不单列分支：MySQL 模式落到 IsMySQLFamilyCfg，Oracle 模式落到 IsOracleFamilyCfg，
	// 复用的是同一套采集实现，行为与原生 MySQL / Oracle 一致。
	dbType := normalizeDBType(cfg.DbType)
	if dbType == "doris" {
		return s.getDorisMetaData(&cfg)
	}
	if dbType == "clickhouse" {
		return s.getClickHouseMetaData(&cfg)
	}
	if dbType == "sqlserver" {
		return s.getSQLServerMetaData(&cfg)
	}
	if IsMySQLFamilyCfg(cfg.DbType, cfg.ExtraConfig) {
		return s.getMySQLMetaData(&cfg)
	}
	if IsPostgresFamilyCfg(cfg.DbType, cfg.ExtraConfig) {
		return s.getPostgresMetaData(&cfg)
	}
	if IsOracleFamilyCfg(cfg.DbType, cfg.ExtraConfig) {
		return s.getOracleMetaData(&cfg)
	}

	return responseData, nil
}
