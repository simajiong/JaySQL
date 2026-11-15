package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/parser"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"

	"github.com/gin-gonic/gin"
)

// SQL 工作台「对象树」（Schema Browser）的数据源。
//
// 设计要点：
//   1. 一个接口按 scope 返回「某一层的 children」，前端 a-tree 懒加载逐层取数；
//   2. 分组节点即使为空也返回（count=0），保证 DBeaver 式的固定分组骨架；
//   3. 权限口径与 GetTablesService 完全一致：先过 schema 级权限校验，查不到就报错，
//      不额外放行；
//   4. 引擎覆盖按「信息可得性」降级：MySQL 系六组全量；PG / SQLServer / Oracle
//      覆盖 tables/views/procedures/triggers/functions；ClickHouse 只覆盖
//      tables/views/columns；不支持的分组一律 count=0，绝不让前端出现空骨架。

// BrowserNode 对象树的统一节点结构。
type BrowserNode struct {
	// kind: group / table / view / procedure / function / trigger / event / column / index
	Kind string `json:"kind"`
	// Name 对象名或分组 key（group 节点时为 tables/views/... ）
	Name string `json:"name"`
	// Label 展示名（group 节点为 "Tables" 等，其余与 Name 相同）
	Label string `json:"label"`
	// IsLeaf true = 不可再展开
	IsLeaf bool `json:"is_leaf"`
	// Count 仅 group 节点使用：分组下的对象数（含 0）
	Count int64 `json:"count"`
	// Attrs 叶子属性：column_type / is_primary / unique / columns / timing / event ...
	Attrs map[string]any `json:"attrs,omitempty"`
}

var browserDatabaseGroupKeys = []string{"tables", "views", "procedures", "triggers", "events", "functions"}
var browserTableGroupKeys = []string{"columns", "indexes", "table-triggers"}

var browserGroupLabels = map[string]string{
	"tables":         "Tables",
	"views":          "Views",
	"procedures":     "Procedures",
	"triggers":       "Triggers",
	"events":         "Events",
	"functions":      "Functions",
	"columns":        "Columns",
	"indexes":        "Indexes",
	"table-triggers": "Triggers",
}

type SchemaBrowserService struct {
	*forms.SchemaBrowserForm
	C        *gin.Context
	Username string
}

func (s *SchemaBrowserService) parserUUID() (id string, err error) {
	id = strings.TrimSpace(s.InstanceID)
	if id == "" {
		return "", fmt.Errorf("instance id is empty")
	}
	return id, nil
}

// validatePerms 与 GetTablesService 同口径：没有该库权限一律拒绝。
func (s *SchemaBrowserService) validatePerms(uuid string) error {
	tables := []parser.Table{{Schema: s.Schema}}
	checker := CheckUserPerm{
		UserName:   s.Username,
		InstanceID: uuid,
		Tables:     tables,
	}
	return checker.HasSchemaPerms()
}

// schemaBrowserTarget 归一后的取数目标：家族 + 已拆分的库/模式名。
type schemaBrowserTarget struct {
	family       string
	cfg          *InstanceCfg
	databaseName string
	schemaName   string
	ctx          context.Context
}

func (s *SchemaBrowserService) Run() (responseData []BrowserNode, err error) {
	id, err := s.parserUUID()
	if err != nil {
		return nil, err
	}
	if err = s.validatePerms(id); err != nil {
		return nil, err
	}
	var cfg InstanceCfg
	global.App.DB.Table("insight_instances").Where("`instance_id`=?", s.InstanceID).Take(&cfg)
	if cfg.Hostname == "" {
		return nil, fmt.Errorf("实例不存在")
	}
	plainPassword, err := secret.Decrypt(cfg.Password)
	if err != nil {
		return nil, err
	}
	cfg.PlainPassword = plainPassword

	// 与 GetTablesService 保持同一套资源路径约定：
	// MySQL 系 / ClickHouse / Oracle 的 schema 参数就是库名；
	// PostgreSQL / SQLServer 是 "库.模式" 路径，这里负责拆分。
	target := &schemaBrowserTarget{cfg: &cfg, ctx: s.C.Request.Context()}
	dbType := normalizeDBType(cfg.DbType)
	switch {
	case IsMySQLFamilyCfg(cfg.DbType, cfg.ExtraConfig):
		target.family = "mysql"
		target.schemaName = s.Schema
		target.databaseName = s.Schema
	case dbType == "clickhouse":
		target.family = "clickhouse"
		target.schemaName = s.Schema
		target.databaseName = s.Schema
	case IsOracleFamilyCfg(cfg.DbType, cfg.ExtraConfig):
		target.family = "oracle"
		target.schemaName = s.Schema
	case IsPostgresFamilyCfg(cfg.DbType, cfg.ExtraConfig):
		parts := strings.SplitN(s.Schema, ".", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("PostgreSQL 资源路径不合法")
		}
		target.family = "postgresql"
		target.databaseName = parts[0]
		target.schemaName = parts[1]
	case dbType == "sqlserver":
		parts := strings.SplitN(s.Schema, ".", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("SQLServer 资源路径不合法")
		}
		target.family = "sqlserver"
		target.databaseName = parts[0]
		target.schemaName = parts[1]
	default:
		target.family = "unsupported"
	}

	// table 级 scope 必须带表名
	if (s.Scope == "table-groups" || s.Scope == "table-group") && strings.TrimSpace(s.Table) == "" {
		return nil, fmt.Errorf("table 参数不能为空")
	}

	switch s.Scope {
	case "database-groups":
		return target.databaseGroups()
	case "group":
		if !isBrowserDatabaseGroup(s.Group) {
			return nil, fmt.Errorf("不支持的分组：%s", s.Group)
		}
		return target.groupObjects(s.Group)
	case "table-groups":
		return target.tableGroups(s.Table)
	case "table-group":
		if !isBrowserTableGroup(s.Group) {
			return nil, fmt.Errorf("不支持的分组：%s", s.Group)
		}
		return target.tableGroupObjects(s.Table, s.Group)
	}
	return nil, fmt.Errorf("不支持的 scope：%s", s.Scope)
}

func isBrowserDatabaseGroup(g string) bool {
	for _, key := range browserDatabaseGroupKeys {
		if key == g {
			return true
		}
	}
	return false
}

func isBrowserTableGroup(g string) bool {
	for _, key := range browserTableGroupKeys {
		if key == g {
			return true
		}
	}
	return false
}

func newBrowserGroupNode(group string, count int64) BrowserNode {
	return BrowserNode{Kind: "group", Name: group, Label: browserGroupLabels[group], Count: count}
}

func browserNameNodes(kind string, leaf bool, rows *[]map[string]interface{}) []BrowserNode {
	nodes := make([]BrowserNode, 0, len(*rows))
	for _, row := range *rows {
		name := firstStringColumn(row)
		if name == "" {
			continue
		}
		nodes = append(nodes, BrowserNode{Kind: kind, Name: name, Label: name, IsLeaf: leaf})
	}
	return nodes
}

// firstStringColumn 取第一列的字符串值，屏蔽各引擎返回的大小写差异。
func firstStringColumn(row map[string]interface{}) string {
	for _, key := range []string{"name", "n", "column_name", "index_name", "trigger_name", "routine_name", "event_name", "object_name"} {
		if v, ok := row[key]; ok {
			if str, ok := v.(string); ok {
				return strings.TrimSpace(str)
			}
		}
	}
	// 兜底：取任意一个 string 值（单列查询场景）
	for _, v := range row {
		if str, ok := v.(string); ok {
			return strings.TrimSpace(str)
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// 库级分组
// ---------------------------------------------------------------------------

func (t *schemaBrowserTarget) databaseGroups() ([]BrowserNode, error) {
	counts := map[string]int64{}
	switch t.family {
	case "mysql":
		type myGroup struct {
			key string
			sql string
		}
		groups := []myGroup{
			// MySQL 8 起 information_schema 的对象 table_type 是 'SYSTEM VIEW'（5.7 时代是 BASE TABLE），
			// 只匹配 BASE TABLE 会让系统库的 tables 分组恒为空，与 Navicat/DBeaver 的展示口径一致纳入 tables
			{"tables", fmt.Sprintf(`SELECT table_name AS name FROM information_schema.tables WHERE table_schema=%s AND table_type IN ('BASE TABLE', 'SYSTEM VIEW') ORDER BY table_name`, quoteStringLiteral(t.schemaName))},
			{"views", fmt.Sprintf(`SELECT table_name AS name FROM information_schema.tables WHERE table_schema=%s AND table_type='VIEW' ORDER BY table_name`, quoteStringLiteral(t.schemaName))},
			{"procedures", fmt.Sprintf(`SELECT routine_name AS name FROM information_schema.routines WHERE routine_schema=%s AND routine_type='PROCEDURE' ORDER BY routine_name`, quoteStringLiteral(t.schemaName))},
			{"functions", fmt.Sprintf(`SELECT routine_name AS name FROM information_schema.routines WHERE routine_schema=%s AND routine_type='FUNCTION' ORDER BY routine_name`, quoteStringLiteral(t.schemaName))},
			{"triggers", fmt.Sprintf(`SELECT trigger_name AS name FROM information_schema.triggers WHERE trigger_schema=%s ORDER BY trigger_name`, quoteStringLiteral(t.schemaName))},
			{"events", fmt.Sprintf(`SELECT event_name AS name FROM information_schema.events WHERE event_schema=%s ORDER BY event_name`, quoteStringLiteral(t.schemaName))},
		}
		for _, g := range groups {
			rows := mustRows(t.myQuery(g.sql))
			counts[g.key] = int64(len(*rows))
		}
	case "postgresql", "sqlserver", "oracle":
		for _, key := range browserDatabaseGroupKeys {
			nodes, err := t.groupObjects(key)
			if err != nil {
				return nil, err
			}
			counts[key] = int64(len(nodes))
		}
	case "clickhouse":
		for _, key := range []string{"tables", "views"} {
			nodes, err := t.groupObjects(key)
			if err != nil {
				return nil, err
			}
			counts[key] = int64(len(nodes))
		}
	}
	// 分组即使为空也固定返回，保持骨架稳定
	nodes := make([]BrowserNode, 0, len(browserDatabaseGroupKeys))
	for _, key := range browserDatabaseGroupKeys {
		nodes = append(nodes, newBrowserGroupNode(key, counts[key]))
	}
	return nodes, nil
}

// ---------------------------------------------------------------------------
// 分组对象清单
// ---------------------------------------------------------------------------

func (t *schemaBrowserTarget) groupObjects(group string) ([]BrowserNode, error) {
	switch t.family {
	case "mysql":
		return t.myGroupObjects(group)
	case "postgresql":
		return t.pgGroupObjects(group)
	case "sqlserver":
		return t.mssqlGroupObjects(group)
	case "oracle":
		return t.oracleGroupObjects(group)
	case "clickhouse":
		return t.chGroupObjects(group)
	}
	return []BrowserNode{}, nil
}

func (t *schemaBrowserTarget) myQuery(query string) (*[]map[string]interface{}, error) {
	db := dao.DB{
		User:     t.cfg.User,
		Password: t.cfg.PlainPassword,
		Host:     t.cfg.Hostname,
		Port:     t.cfg.Port,
		Database: t.databaseName,
		Params:   map[string]string{"group_concat_max_len": "4194304"},
		Ctx:      t.ctx,
	}
	_, rows, err := db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return nil, err
	}
	return rows, nil
}

func (t *schemaBrowserTarget) myGroupObjects(group string) ([]BrowserNode, error) {
	schema := quoteStringLiteral(t.schemaName)
	switch group {
	case "tables":
		// 与 databaseGroups 的 tables 计数同一口径：BASE TABLE + SYSTEM VIEW（见上方注释）
		return browserNameNodes("table", false, mustRows(t.myQuery(fmt.Sprintf(`SELECT table_name AS name FROM information_schema.tables WHERE table_schema=%s AND table_type IN ('BASE TABLE', 'SYSTEM VIEW') ORDER BY table_name`, schema)))), nil
	case "views":
		return browserNameNodes("view", false, mustRows(t.myQuery(fmt.Sprintf(`SELECT table_name AS name FROM information_schema.tables WHERE table_schema=%s AND table_type='VIEW' ORDER BY table_name`, schema)))), nil
	case "procedures":
		return browserNameNodes("procedure", true, mustRows(t.myQuery(fmt.Sprintf(`SELECT routine_name AS name FROM information_schema.routines WHERE routine_schema=%s AND routine_type='PROCEDURE' ORDER BY routine_name`, schema)))), nil
	case "functions":
		return browserNameNodes("function", true, mustRows(t.myQuery(fmt.Sprintf(`SELECT routine_name AS name FROM information_schema.routines WHERE routine_schema=%s AND routine_type='FUNCTION' ORDER BY routine_name`, schema)))), nil
	case "triggers":
		return browserNameNodes("trigger", true, mustRows(t.myQuery(fmt.Sprintf(`SELECT trigger_name AS name FROM information_schema.triggers WHERE trigger_schema=%s ORDER BY trigger_name`, schema)))), nil
	case "events":
		return browserNameNodes("event", true, mustRows(t.myQuery(fmt.Sprintf(`SELECT event_name AS name FROM information_schema.events WHERE event_schema=%s ORDER BY event_name`, schema)))), nil
	}
	return []BrowserNode{}, nil
}

func (t *schemaBrowserTarget) pgDB() (*dao.PostgresDB, error) {
	extra := postgresExtraConfig{}
	if len(t.cfg.ExtraConfig) > 0 {
		_ = json.Unmarshal(t.cfg.ExtraConfig, &extra)
	}
	return &dao.PostgresDB{
		User:     t.cfg.User,
		Password: t.cfg.PlainPassword,
		Host:     t.cfg.Hostname,
		Port:     t.cfg.Port,
		Database: t.databaseName,
		SSLMode:  extra.SSLMode,
		Ctx:      t.ctx,
	}, nil
}

func (t *schemaBrowserTarget) pgQuery(query string) (*[]map[string]interface{}, error) {
	db, err := t.pgDB()
	if err != nil {
		return nil, err
	}
	_, rows, err := db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return nil, err
	}
	return rows, nil
}

func (t *schemaBrowserTarget) pgGroupObjects(group string) ([]BrowserNode, error) {
	schema := quoteStringLiteral(t.schemaName)
	switch group {
	case "tables":
		return browserNameNodes("table", false, mustRows(t.pgQuery(fmt.Sprintf(`SELECT table_name AS name FROM information_schema.tables WHERE table_schema=%s AND table_type='BASE TABLE' ORDER BY table_name`, schema)))), nil
	case "views":
		// 视图 + 物化视图统一归到 Views 分组（information_schema.columns 不含物化视图，用 pg_class 兜住）
		return browserNameNodes("view", false, mustRows(t.pgQuery(fmt.Sprintf(`SELECT c.relname AS name FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname=%s AND c.relkind IN ('v','m') ORDER BY c.relname`, schema)))), nil
	case "procedures":
		return browserNameNodes("procedure", true, mustRows(t.pgQuery(fmt.Sprintf(`SELECT routine_name AS name FROM information_schema.routines WHERE routine_schema=%s AND routine_type='PROCEDURE' ORDER BY routine_name`, schema)))), nil
	case "functions":
		return browserNameNodes("function", true, mustRows(t.pgQuery(fmt.Sprintf(`SELECT routine_name AS name FROM information_schema.routines WHERE routine_schema=%s AND routine_type='FUNCTION' ORDER BY routine_name`, schema)))), nil
	case "triggers":
		return browserNameNodes("trigger", true, mustRows(t.pgQuery(fmt.Sprintf(`SELECT DISTINCT trigger_name AS name FROM information_schema.triggers WHERE trigger_schema=%s ORDER BY trigger_name`, schema)))), nil
	}
	return []BrowserNode{}, nil
}

func (t *schemaBrowserTarget) mssqlQuery(query string) (*[]map[string]interface{}, error) {
	db := &dao.SQLServerDB{
		User:     t.cfg.User,
		Password: t.cfg.PlainPassword,
		Host:     t.cfg.Hostname,
		Port:     t.cfg.Port,
		Database: t.databaseName,
		Ctx:      t.ctx,
	}
	_, rows, err := db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return nil, err
	}
	return rows, nil
}

func (t *schemaBrowserTarget) mssqlGroupObjects(group string) ([]BrowserNode, error) {
	schema := quoteStringLiteral(t.schemaName)
	// 统一走 sys.objects + sys.schemas，按 type 分支
	typeMap := map[string]string{
		"tables":     "'U'",
		"views":      "'V'",
		"procedures": "'P'",
		"functions":  "'FN','IF','TF'",
		"triggers":   "'TR'",
	}
	if types, ok := typeMap[group]; ok {
		cond := ""
		if group == "triggers" {
			cond = " AND o.is_ms_shipped = 0"
		}
		query := fmt.Sprintf(`SELECT o.name AS name FROM sys.objects o JOIN sys.schemas s ON o.schema_id = s.schema_id WHERE s.name = %s AND o.type IN (%s)%s ORDER BY o.name`, schema, types, cond)
		return browserNameNodes(groupToKind(group), groupIsLeaf(group), mustRows(t.mssqlQuery(query))), nil
	}
	return []BrowserNode{}, nil
}

func (t *schemaBrowserTarget) oracleQuery(query string) (*[]map[string]interface{}, error) {
	extra := oracleTableExtraConfig{}
	if len(t.cfg.ExtraConfig) > 0 {
		_ = json.Unmarshal(t.cfg.ExtraConfig, &extra)
	}
	db := &dao.OracleDB{
		User:          t.cfg.User,
		Password:      t.cfg.PlainPassword,
		Host:          t.cfg.Hostname,
		Port:          t.cfg.Port,
		ServiceName:   extra.ServiceName,
		CurrentSchema: t.schemaName,
		Ctx:           t.ctx,
	}
	_, rows, err := db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return nil, err
	}
	return rows, nil
}

// oracleCatalogRows Oracle 字典可见性降级（与 objectmeta.go objectBaseOracle 同一口径）：
// 先查 all_*（只显示当前用户有权限的对象），结果为空时降级 dba_*（DBA 账号可覆盖
// 全部 schema）。dba_* 无权限报错（ORA-00942）不上抛——非 DBA 账号本就只能看
// all_* 视野，保留 all_* 的结果返回。
func (t *schemaBrowserTarget) oracleCatalogRows(build func(catalog string) string) (*[]map[string]interface{}, error) {
	rows, err := t.oracleQuery(build("all"))
	if err != nil {
		return nil, err
	}
	if rows == nil || len(*rows) == 0 {
		if dbaRows, dbaErr := t.oracleQuery(build("dba")); dbaErr == nil {
			return dbaRows, nil
		}
	}
	return rows, nil
}

func (t *schemaBrowserTarget) oracleGroupObjects(group string) ([]BrowserNode, error) {
	owner := quoteStringLiteral(strings.ToUpper(t.schemaName))
	// 各分组都走 all_* → dba_* 降级：2026-11-12 生产实锤，DBA 特权账号能从
	// dba_users/dba_segments 看到 HR 的用户与段（左侧模式树连大小都显示 4.8MB），
	// 但未被显式授权 HR 的表时，all_objects 对 owner='HR' 一行都拿不到，
	// Tables 分组被伪装成「该库下暂无表」（DBA1 同症状）。
	// 错误一律上抛（all_* 查询报错就该让用户看到「加载失败」，而不是空列表）。
	switch group {
	case "tables":
		rows, err := t.oracleCatalogRows(func(catalog string) string {
			return fmt.Sprintf(`SELECT object_name AS name FROM %s_objects WHERE owner = %s AND object_type = 'TABLE' ORDER BY object_name`, catalog, owner)
		})
		if err != nil {
			return nil, err
		}
		return browserNameNodes("table", false, rows), nil
	case "views":
		// 视图 + 物化视图统一归到 Views 分组
		rows, err := t.oracleCatalogRows(func(catalog string) string {
			return fmt.Sprintf(`SELECT view_name AS name FROM %s_views WHERE owner = %s UNION ALL SELECT mview_name AS name FROM %s_mviews WHERE owner = %s ORDER BY name`, catalog, owner, catalog, owner)
		})
		if err != nil {
			return nil, err
		}
		return browserNameNodes("view", false, rows), nil
	case "procedures":
		rows, err := t.oracleCatalogRows(func(catalog string) string {
			return fmt.Sprintf(`SELECT object_name AS name FROM %s_objects WHERE owner = %s AND object_type = 'PROCEDURE' ORDER BY object_name`, catalog, owner)
		})
		if err != nil {
			return nil, err
		}
		return browserNameNodes("procedure", true, rows), nil
	case "functions":
		rows, err := t.oracleCatalogRows(func(catalog string) string {
			return fmt.Sprintf(`SELECT object_name AS name FROM %s_objects WHERE owner = %s AND object_type = 'FUNCTION' ORDER BY object_name`, catalog, owner)
		})
		if err != nil {
			return nil, err
		}
		return browserNameNodes("function", true, rows), nil
	case "triggers":
		rows, err := t.oracleCatalogRows(func(catalog string) string {
			return fmt.Sprintf(`SELECT trigger_name AS name FROM %s_triggers WHERE owner = %s ORDER BY trigger_name`, catalog, owner)
		})
		if err != nil {
			return nil, err
		}
		return browserNameNodes("trigger", true, rows), nil
	}
	return []BrowserNode{}, nil
}

func (t *schemaBrowserTarget) chQuery(query string) (*[]map[string]interface{}, error) {
	db := &dao.ClickhouseDB{
		User:     t.cfg.User,
		Password: t.cfg.PlainPassword,
		Host:     t.cfg.Hostname,
		Port:     t.cfg.Port,
		Ctx:      t.ctx,
	}
	_, rows, err := db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return nil, err
	}
	return rows, nil
}

func (t *schemaBrowserTarget) chGroupObjects(group string) ([]BrowserNode, error) {
	database := quoteStringLiteral(t.schemaName)
	switch group {
	case "tables":
		return browserNameNodes("table", false, mustRows(t.chQuery(fmt.Sprintf(`SELECT name AS name FROM system.tables WHERE database = %s AND engine NOT LIKE '%%View' ORDER BY name`, database)))), nil
	case "views":
		return browserNameNodes("view", false, mustRows(t.chQuery(fmt.Sprintf(`SELECT name AS name FROM system.tables WHERE database = %s AND engine LIKE '%%View' ORDER BY name`, database)))), nil
	}
	return []BrowserNode{}, nil
}

func groupToKind(group string) string {
	switch group {
	case "tables":
		return "table"
	case "views":
		return "view"
	case "procedures":
		return "procedure"
	case "functions":
		return "function"
	case "triggers":
		return "trigger"
	case "events":
		return "event"
	}
	return group
}

func groupIsLeaf(group string) bool {
	return group != "tables" && group != "views"
}

func mustRows(rows *[]map[string]interface{}, err error) *[]map[string]interface{} {
	if err != nil {
		return &[]map[string]interface{}{}
	}
	return rows
}

// ---------------------------------------------------------------------------
// 表级分组
// ---------------------------------------------------------------------------

func (t *schemaBrowserTarget) tableGroups(table string) ([]BrowserNode, error) {
	counts := map[string]int64{}
	for _, key := range browserTableGroupKeys {
		nodes, err := t.tableGroupObjects(table, key)
		if err != nil {
			return nil, err
		}
		counts[key] = int64(len(nodes))
	}
	nodes := make([]BrowserNode, 0, len(browserTableGroupKeys))
	for _, key := range browserTableGroupKeys {
		nodes = append(nodes, newBrowserGroupNode(key, counts[key]))
	}
	return nodes, nil
}

func (t *schemaBrowserTarget) tableGroupObjects(table, group string) ([]BrowserNode, error) {
	switch t.family {
	case "mysql":
		return t.myTableGroupObjects(table, group)
	case "postgresql":
		return t.pgTableGroupObjects(table, group)
	case "sqlserver":
		return t.mssqlTableGroupObjects(table, group)
	case "oracle":
		return t.oracleTableGroupObjects(table, group)
	case "clickhouse":
		if group == "columns" {
			return t.chTableGroupObjects(table)
		}
	}
	return []BrowserNode{}, nil
}

func (t *schemaBrowserTarget) myTableGroupObjects(table, group string) ([]BrowserNode, error) {
	schema := quoteStringLiteral(t.schemaName)
	tbl := quoteStringLiteral(table)
	switch group {
	case "columns":
		rows := mustRows(t.myQuery(fmt.Sprintf(`
			SELECT column_name AS name, column_type AS column_type, column_key AS column_key,
			       is_nullable AS is_nullable, column_comment AS column_comment
			FROM information_schema.columns
			WHERE table_schema = %s AND table_name = %s
			ORDER BY ordinal_position`, schema, tbl)))
		nodes := make([]BrowserNode, 0, len(*rows))
		for _, row := range *rows {
			name := firstStringColumn(row)
			if name == "" {
				continue
			}
			nodes = append(nodes, BrowserNode{
				Kind: "column", Name: name, Label: name, IsLeaf: true,
				Attrs: map[string]any{
					"column_type": stringField(row, "column_type"),
					"is_primary":  stringField(row, "column_key") == "PRI",
					"nullable":    stringField(row, "is_nullable") == "YES",
					"comment":     stringField(row, "column_comment"),
				},
			})
		}
		return nodes, nil
	case "indexes":
		rows := mustRows(t.myQuery(fmt.Sprintf(`
			SELECT index_name AS name, MAX(non_unique) AS non_unique,
			       GROUP_CONCAT(column_name ORDER BY seq_in_index) AS columns
			FROM information_schema.statistics
			WHERE table_schema = %s AND table_name = %s AND index_name IS NOT NULL
			GROUP BY index_name ORDER BY index_name`, schema, tbl)))
		nodes := make([]BrowserNode, 0, len(*rows))
		for _, row := range *rows {
			name := firstStringColumn(row)
			if name == "" {
				continue
			}
			nodes = append(nodes, BrowserNode{
				Kind: "index", Name: name, Label: name, IsLeaf: true,
				Attrs: map[string]any{
					"unique":  stringField(row, "non_unique") == "0",
					"columns": stringField(row, "columns"),
				},
			})
		}
		return nodes, nil
	case "table-triggers":
		rows := mustRows(t.myQuery(fmt.Sprintf(`
			SELECT trigger_name AS name, action_timing AS action_timing, event_manipulation AS event_manipulation
			FROM information_schema.triggers
			WHERE trigger_schema = %s AND event_object_table = %s
			ORDER BY trigger_name`, schema, tbl)))
		nodes := make([]BrowserNode, 0, len(*rows))
		for _, row := range *rows {
			name := firstStringColumn(row)
			if name == "" {
				continue
			}
			nodes = append(nodes, BrowserNode{
				Kind: "trigger", Name: name, Label: name, IsLeaf: true,
				Attrs: map[string]any{
					"timing": stringField(row, "action_timing"),
					"event":  stringField(row, "event_manipulation"),
				},
			})
		}
		return nodes, nil
	}
	return []BrowserNode{}, nil
}

func (t *schemaBrowserTarget) pgTableGroupObjects(table, group string) ([]BrowserNode, error) {
	schema := quoteStringLiteral(t.schemaName)
	tbl := quoteStringLiteral(table)
	switch group {
	case "columns":
		rows := mustRows(t.pgQuery(fmt.Sprintf(`
			SELECT column_name AS name, data_type AS data_type, is_nullable AS is_nullable
			FROM information_schema.columns
			WHERE table_schema = %s AND table_name = %s
			ORDER BY ordinal_position`, schema, tbl)))
		pkRows := mustRows(t.pgQuery(fmt.Sprintf(`
			SELECT kcu.column_name AS name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu
			  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
			WHERE tc.constraint_type = 'PRIMARY KEY' AND tc.table_schema = %s AND tc.table_name = %s`, schema, tbl)))
		pkSet := map[string]bool{}
		for _, row := range *pkRows {
			pkSet[firstStringColumn(row)] = true
		}
		nodes := make([]BrowserNode, 0, len(*rows))
		for _, row := range *rows {
			name := firstStringColumn(row)
			if name == "" {
				continue
			}
			nodes = append(nodes, BrowserNode{
				Kind: "column", Name: name, Label: name, IsLeaf: true,
				Attrs: map[string]any{
					"column_type": stringField(row, "data_type"),
					"is_primary":  pkSet[name],
					"nullable":    stringField(row, "is_nullable") == "YES",
				},
			})
		}
		return nodes, nil
	case "indexes":
		rows := mustRows(t.pgQuery(fmt.Sprintf(`
			SELECT indexname AS name, indexdef FROM pg_indexes
			WHERE schemaname = %s AND tablename = %s ORDER BY indexname`, schema, tbl)))
		nodes := make([]BrowserNode, 0, len(*rows))
		for _, row := range *rows {
			name := firstStringColumn(row)
			if name == "" {
				continue
			}
			def := stringField(row, "indexdef")
			nodes = append(nodes, BrowserNode{
				Kind: "index", Name: name, Label: name, IsLeaf: true,
				Attrs: map[string]any{
					"unique":     strings.Contains(strings.ToUpper(def), "UNIQUE"),
					"definition": def,
				},
			})
		}
		return nodes, nil
	case "table-triggers":
		rows := mustRows(t.pgQuery(fmt.Sprintf(`
			SELECT DISTINCT trigger_name AS name, action_timing, event_manipulation
			FROM information_schema.triggers
			WHERE trigger_schema = %s AND event_object_table = %s
			ORDER BY trigger_name`, schema, tbl)))
		nodes := make([]BrowserNode, 0, len(*rows))
		for _, row := range *rows {
			name := firstStringColumn(row)
			if name == "" {
				continue
			}
			nodes = append(nodes, BrowserNode{
				Kind: "trigger", Name: name, Label: name, IsLeaf: true,
				Attrs: map[string]any{
					"timing": stringField(row, "action_timing"),
					"event":  stringField(row, "event_manipulation"),
				},
			})
		}
		return nodes, nil
	}
	return []BrowserNode{}, nil
}

func (t *schemaBrowserTarget) mssqlTableGroupObjects(table, group string) ([]BrowserNode, error) {
	schema := quoteStringLiteral(t.schemaName)
	tbl := quoteStringLiteral(table)
	switch group {
	case "columns":
		rows := mustRows(t.mssqlQuery(fmt.Sprintf(`
			SELECT column_name AS name, data_type AS data_type, is_nullable AS is_nullable
			FROM information_schema.columns
			WHERE table_schema = %s AND table_name = %s
			ORDER BY ordinal_position`, schema, tbl)))
		pkRows := mustRows(t.mssqlQuery(fmt.Sprintf(`
			SELECT kcu.column_name AS name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu
			  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
			WHERE tc.constraint_type = 'PRIMARY KEY' AND tc.table_schema = %s AND tc.table_name = %s`, schema, tbl)))
		pkSet := map[string]bool{}
		for _, row := range *pkRows {
			pkSet[firstStringColumn(row)] = true
		}
		nodes := make([]BrowserNode, 0, len(*rows))
		for _, row := range *rows {
			name := firstStringColumn(row)
			if name == "" {
				continue
			}
			nodes = append(nodes, BrowserNode{
				Kind: "column", Name: name, Label: name, IsLeaf: true,
				Attrs: map[string]any{
					"column_type": stringField(row, "data_type"),
					"is_primary":  pkSet[name],
					"nullable":    stringField(row, "is_nullable") == "YES",
				},
			})
		}
		return nodes, nil
	case "indexes":
		// STRING_AGG 需要 SQLServer 2017+；更老版本请改用 FOR XML PATH 写法
		rows := mustRows(t.mssqlQuery(fmt.Sprintf(`
			SELECT i.name AS name, i.is_unique,
			       STRING_AGG(c.name, ',') WITHIN GROUP (ORDER BY ic.key_ordinal) AS columns
			FROM sys.indexes i
			JOIN sys.index_columns ic ON i.object_id = ic.object_id AND i.index_id = ic.index_id
			JOIN sys.columns c ON ic.object_id = c.object_id AND ic.column_id = c.column_id
			JOIN sys.objects o ON i.object_id = o.object_id
			JOIN sys.schemas s ON o.schema_id = s.schema_id
			WHERE s.name = %s AND o.name = %s AND i.name IS NOT NULL
			GROUP BY i.name, i.is_unique ORDER BY i.name`, schema, tbl)))
		nodes := make([]BrowserNode, 0, len(*rows))
		for _, row := range *rows {
			name := firstStringColumn(row)
			if name == "" {
				continue
			}
			nodes = append(nodes, BrowserNode{
				Kind: "index", Name: name, Label: name, IsLeaf: true,
				Attrs: map[string]any{
					"unique":  stringField(row, "is_unique") == "1" || stringField(row, "is_unique") == "true",
					"columns": stringField(row, "columns"),
				},
			})
		}
		return nodes, nil
	case "table-triggers":
		rows := mustRows(t.mssqlQuery(fmt.Sprintf(`
			SELECT o.name AS name, CASE WHEN t.is_disabled = 1 THEN 'DISABLED' ELSE 'ENABLED' END AS status
			FROM sys.triggers t
			JOIN sys.objects o ON t.object_id = o.object_id
			JOIN sys.objects p ON t.parent_id = p.object_id
			JOIN sys.schemas s ON o.schema_id = s.schema_id
			WHERE s.name = %s AND p.name = %s
			ORDER BY o.name`, schema, tbl)))
		nodes := make([]BrowserNode, 0, len(*rows))
		for _, row := range *rows {
			name := firstStringColumn(row)
			if name == "" {
				continue
			}
			nodes = append(nodes, BrowserNode{
				Kind: "trigger", Name: name, Label: name, IsLeaf: true,
				Attrs: map[string]any{"status": stringField(row, "status")},
			})
		}
		return nodes, nil
	}
	return []BrowserNode{}, nil
}

func (t *schemaBrowserTarget) oracleTableGroupObjects(table, group string) ([]BrowserNode, error) {
	owner := quoteStringLiteral(strings.ToUpper(t.schemaName))
	tbl := quoteStringLiteral(strings.ToUpper(table))
	switch group {
	case "columns":
		rows := mustRows(t.oracleQuery(fmt.Sprintf(`
			SELECT column_name AS name, data_type AS data_type, nullable AS nullable
			FROM all_tab_columns
			WHERE owner = %s AND table_name = %s
			ORDER BY column_id`, owner, tbl)))
		pkRows := mustRows(t.oracleQuery(fmt.Sprintf(`
			SELECT cc.column_name AS name
			FROM all_constraints c
			JOIN all_cons_columns cc ON c.owner = cc.owner AND c.constraint_name = cc.constraint_name
			WHERE c.constraint_type = 'P' AND c.owner = %s AND c.table_name = %s`, owner, tbl)))
		pkSet := map[string]bool{}
		for _, row := range *pkRows {
			pkSet[firstStringColumn(row)] = true
		}
		nodes := make([]BrowserNode, 0, len(*rows))
		for _, row := range *rows {
			name := firstStringColumn(row)
			if name == "" {
				continue
			}
			nodes = append(nodes, BrowserNode{
				Kind: "column", Name: name, Label: name, IsLeaf: true,
				Attrs: map[string]any{
					"column_type": stringField(row, "data_type"),
					"is_primary":  pkSet[name],
					"nullable":    stringField(row, "nullable") == "Y",
				},
			})
		}
		return nodes, nil
	case "indexes":
		rows := mustRows(t.oracleQuery(fmt.Sprintf(`
			SELECT index_name AS name, uniqueness AS uniqueness FROM all_indexes
			WHERE owner = %s AND table_name = %s ORDER BY index_name`, owner, tbl)))
		nodes := make([]BrowserNode, 0, len(*rows))
		for _, row := range *rows {
			name := firstStringColumn(row)
			if name == "" {
				continue
			}
			nodes = append(nodes, BrowserNode{
				Kind: "index", Name: name, Label: name, IsLeaf: true,
				Attrs: map[string]any{"unique": stringField(row, "uniqueness") == "UNIQUE"},
			})
		}
		return nodes, nil
	case "table-triggers":
		rows := mustRows(t.oracleQuery(fmt.Sprintf(`
			SELECT trigger_name AS name, trigger_type AS timing, triggering_event AS event
			FROM all_triggers
			WHERE owner = %s AND table_name = %s ORDER BY trigger_name`, owner, tbl)))
		nodes := make([]BrowserNode, 0, len(*rows))
		for _, row := range *rows {
			name := firstStringColumn(row)
			if name == "" {
				continue
			}
			nodes = append(nodes, BrowserNode{
				Kind: "trigger", Name: name, Label: name, IsLeaf: true,
				Attrs: map[string]any{
					"timing": stringField(row, "timing"),
					"event":  stringField(row, "event"),
				},
			})
		}
		return nodes, nil
	}
	return []BrowserNode{}, nil
}

func (t *schemaBrowserTarget) chTableGroupObjects(table string) ([]BrowserNode, error) {
	database := quoteStringLiteral(t.schemaName)
	tbl := quoteStringLiteral(table)
	rows := mustRows(t.chQuery(fmt.Sprintf(`
		SELECT name AS name, type AS column_type FROM system.columns
		WHERE database = %s AND table = %s ORDER BY position`, database, tbl)))
	nodes := make([]BrowserNode, 0, len(*rows))
	for _, row := range *rows {
		name := firstStringColumn(row)
		if name == "" {
			continue
		}
		nodes = append(nodes, BrowserNode{
			Kind: "column", Name: name, Label: name, IsLeaf: true,
			Attrs: map[string]any{"column_type": stringField(row, "column_type")},
		})
	}
	return nodes, nil
}

func stringField(row map[string]interface{}, key string) string {
	if v, ok := row[key]; ok {
		switch val := v.(type) {
		case string:
			return val
		case []byte:
			return string(val)
		case nil:
			return ""
		default:
			return fmt.Sprintf("%v", val)
		}
	}
	return ""
}
