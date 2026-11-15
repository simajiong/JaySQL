package services

// ---------------------------------------------------------------------------
// 库 / 表 / 列级联选项（绑定列弹窗、申请明文弹窗共用）
//
// 为什么不复用 SQL 工作台的 /api/v1/das/schemas 系列：
//   那条链路按「调用者已有数据授权」（insight_das_schema_perms / data_grants）过滤，
//   2026-10-15 收口后连超管也不放宽。而配脱敏规则的通常是 DBA/管理员，
//   对目标库未必有查询授权 —— 用授权表取数就会出现「选了实例，库下拉一片空白」。
//
// 因此这里按「配置面」取数：
//   库   —— 实例元数据快照 insight_instance_schemas（静态、零连接）；
//   表   —— 连实例实时读 information_schema / pg_class（只回表名与注释）；
//   列   —— 连实例实时读列元数据（复用自动发现同一套列读取函数）。
// 全程不读业务数据，也不返回任何数据值。
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/common/tasks"
	"github.com/simajiong/jaysql/internal/datasec/forms"
	dasDao "github.com/simajiong/jaysql/internal/das/dao"
	dasServices "github.com/simajiong/jaysql/internal/das/services"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"
)

type schemaOption struct {
	Schema       string `json:"schema"`
	DisplayName  string `json:"display_name"`
	ResourceType string `json:"resource_type"`
}

// ListSchemaOptionsService 库 / Schema 下拉（读实例元数据快照）
type ListSchemaOptionsService struct {
	*forms.SchemaOptionsForm
}

func (s *ListSchemaOptionsService) Run() ([]schemaOption, error) {
	instanceID := strings.TrimSpace(s.InstanceID)
	// 快照过期时异步补刷一次；本次请求仍返回现有快照，不阻塞在远端连接上。
	tasks.EnsureFresh(instanceID)
	rows := make([]schemaOption, 0, 32)
	err := global.App.DB.Table("insight_instance_schemas").
		Select("`schema`, ifnull(display_name,'') as display_name, ifnull(resource_type,'') as resource_type").
		Where("instance_id = ? and is_deleted = 0", instanceID).
		Order("`schema` asc").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	// 剔除引擎自带系统库（information_schema / mysql / sys / pg_catalog…）：
	// 脱敏配置的对象是业务表，系统库放进来只会干扰选择。
	// 判定复用 das 的同一套口径，避免两个模块各写一份规则而漂移。
	if dbType, typeErr := dasServices.GetDbType(instanceID); typeErr == nil && dbType != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if dasServices.IsSystemSchemaPath(dbType, r.Schema) {
				continue
			}
			filtered = append(filtered, r)
		}
		rows = filtered
	}
	return rows, nil
}

type tableOption struct {
	Name    string `json:"name"`
	Comment string `json:"comment"`
}

// ListTableOptionsService 表下拉（连实例实时读取，只回表名与注释）
type ListTableOptionsService struct {
	*forms.TableOptionsForm
	C *gin.Context
}

func (s *ListTableOptionsService) Run() ([]tableOption, error) {
	instanceID := strings.TrimSpace(s.InstanceID)
	schemaPath := strings.TrimSpace(s.SchemaName)
	if schemaPath == "" {
		return nil, fmt.Errorf("请先选择库")
	}
	cfg, err := dasServices.GetDBConfig(instanceID)
	if err != nil {
		return nil, err
	}
	plain, err := secret.Decrypt(cfg.Password)
	if err != nil {
		return nil, fmt.Errorf("数据源凭据解密失败")
	}
	switch {
	case dasServices.IsMySQLFamily(string(cfg.DbType)):
		return listMySQLTables(s.C, cfg, plain, schemaPath)
	case dasServices.IsPostgresFamily(string(cfg.DbType)):
		return listPostgresTables(s.C, cfg, plain, schemaPath)
	case dasServices.IsOracleFamilyCfg(string(cfg.DbType), cfg.ExtraConfig):
		return listOracleTables(s.C, cfg, plain, schemaPath)
	default:
		return nil, fmt.Errorf("%s 暂不支持表级选择，可手工输入表名", cfg.DbType)
	}
}

func listMySQLTables(c *gin.Context, cfg models.InsightInstances, plainPassword, schema string) ([]tableOption, error) {
	if !validIdentifier(schema) {
		return nil, fmt.Errorf("库名 %s 含非法字符", schema)
	}
	db := dasDao.DB{User: cfg.User, Password: plainPassword, Host: cfg.Hostname, Port: cfg.Port, Ctx: c.Request.Context()}
	sqlDB, err := db.Open()
	if err != nil {
		return nil, fmt.Errorf("连接实例失败: %w", err)
	}
	defer sqlDB.Close()
	rows, err := sqlDB.Query(
		"SELECT TABLE_NAME, IFNULL(TABLE_COMMENT,'') FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_TYPE='BASE TABLE' ORDER BY TABLE_NAME",
		schema)
	if err != nil {
		return nil, fmt.Errorf("读取表清单失败: %w", err)
	}
	defer rows.Close()
	out := make([]tableOption, 0, 32)
	for rows.Next() {
		var name, comment string
		if err := rows.Scan(&name, &comment); err != nil {
			return nil, err
		}
		out = append(out, tableOption{Name: name, Comment: comment})
	}
	return out, rows.Err()
}

// listOracleTables Oracle 表清单（owner = 大写 schema）。
// 用 all_objects 而非 all_tables：DBA 特权账号未被显式授权目标 schema 时
// all_tables 拿不到行（见 das/services/schema_browser.go 同款回归）；
// all_* 为空时降级 dba_*（连接账号有 DBA 权限的场景）。
func listOracleTables(c *gin.Context, cfg models.InsightInstances, plainPassword, schema string) ([]tableOption, error) {
	owner := strings.ToUpper(strings.TrimSpace(schema))
	if !validIdentifier(owner) {
		return nil, fmt.Errorf("Schema 名 %s 含非法字符", owner)
	}
	extra := oracleScanExtra{}
	if len(cfg.ExtraConfig) > 0 {
		_ = json.Unmarshal(cfg.ExtraConfig, &extra)
	}
	db := dasDao.OracleDB{
		User: cfg.User, Password: plainPassword, Host: cfg.Hostname, Port: cfg.Port,
		ServiceName: extra.ServiceName, Ctx: c.Request.Context(),
	}
	fetch := func(catalog string) (*[]string, *[]map[string]interface{}, error) {
		// 别名不能用 comment（Oracle 保留字，ORA-00923），用 tab_comment
		query := fmt.Sprintf(`SELECT o.object_name AS name, COALESCE(cc.comments, '') AS tab_comment
			FROM %s_objects o
			LEFT JOIN %s_tab_comments cc
			  ON cc.owner = o.owner AND cc.table_name = o.object_name
			WHERE o.owner = '%s' AND o.object_type = 'TABLE'
			ORDER BY o.object_name`, catalog, catalog, owner)
		return db.Query(query)
	}
	_, data, err := fetch("all")
	if err == nil && (data == nil || len(*data) == 0) {
		if _, dbaData, dbaErr := fetch("dba"); dbaErr == nil {
			data = dbaData
		}
	}
	if err != nil {
		return nil, fmt.Errorf("读取表清单失败: %w", err)
	}
	out := make([]tableOption, 0, len(*data))
	for _, row := range *data {
		name := oracleRowString(row, "name")
		if strings.TrimSpace(name) == "" {
			continue
		}
		out = append(out, tableOption{Name: name, Comment: oracleRowString(row, "tab_comment")})
	}
	return out, nil
}

func listPostgresTables(c *gin.Context, cfg models.InsightInstances, plainPassword, schemaPath string) ([]tableOption, error) {
	databaseName, schemaName := splitSchemaPath(schemaPath)
	if !validIdentifier(databaseName) || !validIdentifier(schemaName) {
		return nil, fmt.Errorf("库/schema 名含非法字符")
	}
	// schemaName 已过白名单校验，内联安全（pgx 的 Query 不接受占位参数）
	query := fmt.Sprintf(
		`SELECT c.relname, COALESCE(obj_description(c.oid), '')
		 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = '%s' AND c.relkind = 'r' ORDER BY c.relname`, schemaName)
	_, data, err := buildPostgresDB(c, cfg, plainPassword, databaseName).Query(query)
	if err != nil {
		return nil, fmt.Errorf("读取表清单失败: %w", err)
	}
	out := make([]tableOption, 0, len(*data))
	for _, row := range *data {
		name := toStringValue(row["relname"])
		if strings.TrimSpace(name) == "" {
			continue
		}
		out = append(out, tableOption{Name: name, Comment: toStringValue(row["coalesce"])})
	}
	return out, nil
}

type columnOption struct {
	Name     string `json:"name"`
	Comment  string `json:"comment"`
	DataType string `json:"data_type"`
}

// ListColumnOptionsService 列下拉（连实例实时读取列元数据，复用自动发现的列读取函数）
type ListColumnOptionsService struct {
	*forms.ColumnOptionsForm
	C *gin.Context
}

func (s *ListColumnOptionsService) Run() ([]columnOption, error) {
	instanceID := strings.TrimSpace(s.InstanceID)
	schemaPath := strings.TrimSpace(s.SchemaName)
	table := strings.TrimSpace(s.TableName)
	if schemaPath == "" || table == "" {
		return nil, fmt.Errorf("请先选择库与表")
	}
	cfg, err := dasServices.GetDBConfig(instanceID)
	if err != nil {
		return nil, err
	}
	plain, err := secret.Decrypt(cfg.Password)
	if err != nil {
		return nil, fmt.Errorf("数据源凭据解密失败")
	}
	switch {
	case dasServices.IsMySQLFamily(string(cfg.DbType)):
		if !validIdentifier(schemaPath) || !validIdentifier(table) {
			return nil, fmt.Errorf("库名或表名含非法字符")
		}
		db := dasDao.DB{User: cfg.User, Password: plain, Host: cfg.Hostname, Port: cfg.Port, Ctx: s.C.Request.Context()}
		sqlDB, err := db.Open()
		if err != nil {
			return nil, fmt.Errorf("连接实例失败: %w", err)
		}
		defer sqlDB.Close()
		metas, err := mysqlColumns(sqlDB, schemaPath, table)
		if err != nil {
			return nil, fmt.Errorf("读取列元数据失败: %w", err)
		}
		return toColumnOptions(metas), nil
	case dasServices.IsPostgresFamily(string(cfg.DbType)):
		databaseName, schemaName := splitSchemaPath(schemaPath)
		if !validIdentifier(databaseName) || !validIdentifier(schemaName) || !validIdentifier(table) {
			return nil, fmt.Errorf("库/schema/表名含非法字符")
		}
		metas, err := postgresColumns(*buildPostgresDB(s.C, cfg, plain, databaseName), schemaName, table)
		if err != nil {
			return nil, fmt.Errorf("读取列元数据失败: %w", err)
		}
		return toColumnOptions(metas), nil
	case dasServices.IsOracleFamilyCfg(string(cfg.DbType), cfg.ExtraConfig):
		owner := strings.ToUpper(strings.TrimSpace(schemaPath))
		upperTable := strings.ToUpper(strings.TrimSpace(table))
		if !validIdentifier(owner) || !validIdentifier(upperTable) {
			return nil, fmt.Errorf("Schema/表名含非法字符")
		}
		extra := oracleScanExtra{}
		if len(cfg.ExtraConfig) > 0 {
			_ = json.Unmarshal(cfg.ExtraConfig, &extra)
		}
		db := dasDao.OracleDB{
			User: cfg.User, Password: plain, Host: cfg.Hostname, Port: cfg.Port,
			ServiceName: extra.ServiceName, CurrentSchema: owner, Ctx: s.C.Request.Context(),
		}
		metas, err := oracleColumns(db, owner, upperTable)
		if err != nil {
			return nil, fmt.Errorf("读取列元数据失败: %w", err)
		}
		return toColumnOptions(metas), nil
	default:
		return nil, fmt.Errorf("%s 暂不支持列级选择，可手工输入列名", cfg.DbType)
	}
}

func toColumnOptions(metas []scanColumnMeta) []columnOption {
	out := make([]columnOption, 0, len(metas))
	for _, m := range metas {
		out = append(out, columnOption{Name: m.Name, Comment: m.Comment})
	}
	return out
}

// splitSchemaPath 把 "库.schema" 拆成两段；只有库名时 schema 默认 public
// （与 SQL 查询 / 自动发现的口径保持一致）。
func splitSchemaPath(schemaPath string) (databaseName, schemaName string) {
	if parts := strings.SplitN(schemaPath, ".", 2); len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1]
	}
	return schemaPath, "public"
}

func buildPostgresDB(c *gin.Context, cfg models.InsightInstances, plainPassword, databaseName string) *dasDao.PostgresDB {
	extra := postgresExtra{}
	if len(cfg.ExtraConfig) > 0 {
		_ = json.Unmarshal(cfg.ExtraConfig, &extra)
	}
	return &dasDao.PostgresDB{
		User: cfg.User, Password: plainPassword, Host: cfg.Hostname, Port: cfg.Port,
		Database: databaseName, SSLMode: extra.SSLMode, Ctx: c.Request.Context(),
	}
}
