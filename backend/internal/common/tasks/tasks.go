package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/internal/common/dbfamily"
	"github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/common/sqlserverdsn"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/pkg/secret"

	go_ora "github.com/sijms/go-ora/v2"
	"gorm.io/gorm"
)

// 【2026-10-19】同步层不再按「是不是系统库」做过滤，只负责把元数据照实搬回来。
//
// 之前这里把 information_schema / mysql / performance_schema 直接挡在同步之外，
// 后果是 insight_instance_schemas 里根本没有这几行——admin 就算被授予了全实例
// 权限也永远看不到它们，查锁、会话、慢查询、权限这些排障场景无从下手。
// （顺带一提：漏掉 sys 只是让树上孤零零挂一个系统库、看着像故障，本质同源。）
//
// 「该不该显示」已经统一收口在可见性层 das/services/system_schema.go 的
// systemSchemaGate：非 admin 主体过滤掉全部系统库，admin 照旧可见，显式授权单个
// 库（哪怕就是系统库）不受影响。同步层再过滤一遍等于替可见性层做决定，而且会让
// admin 的「全实例权限」名不副实。
//
// 这里只保留两类与「系统库」无关的排除：
//   1. 会话级临时对象（PG 的 pg_toast* / pg_temp_*）：每次连接都在变，同步进来
//      只会制造噪音和 is_deleted 抖动；
//   2. 不可连接 / 每次重建的对象（PG 模板库已被 datistemplate / datallowconn
//      天然排除；SQLServer 的 tempdb 每次重启重建，同步它没有意义）。

// MySQL 系（mysql / tidb / doris，以及走 MySQL 协议的 mariadb / polardb /
// oceanbase-mysql 等）与 ClickHouse 共用下面两条查询。
var mysqlQuery string = `
	SELECT 
		SCHEMA_NAME AS TABLE_SCHEMA
	FROM 
		INFORMATION_SCHEMA.SCHEMATA
`

var clickhouseQuery string = `
	SELECT 
		name AS TABLE_SCHEMA
	FROM 
		system.databases
`

var postgresDatabaseQuery string = `
	SELECT datname AS database_name
	FROM pg_database
	WHERE datistemplate = false
	  AND datallowconn = true
	ORDER BY datname
`

// information_schema / pg_catalog 不再排除：它们是排障刚需，可见性由 gate 收口。
var postgresSchemaQuery string = `
	SELECT schema_name
	FROM information_schema.schemata
	WHERE schema_name NOT LIKE 'pg_toast%'
	  AND schema_name NOT LIKE 'pg_temp_%'
	ORDER BY schema_name
`

var sqlserverDatabaseQuery string = `
	SELECT name AS database_name
	FROM sys.databases
	WHERE name NOT IN ('tempdb')
	ORDER BY name
`

// INFORMATION_SCHEMA / sys 不再排除，理由同上；guest 不是真实 schema，保留排除。
var sqlserverSchemaQuery string = `
	SELECT name AS schema_name
	FROM sys.schemas
	WHERE name NOT IN ('guest')
	ORDER BY name
`

var oracleSchemaQuery string = `
	SELECT username AS schema_name
	FROM all_users
	ORDER BY username
`

type instanceExtraConfig struct {
	Database   string `json:"database"`
	SSLMode    string `json:"sslmode"`
	AuthSource string `json:"auth_source"`
	Service    string `json:"service_name"`
}

type schemaResource struct {
	Path         string
	ParentPath   string
	ResourceType string
	DisplayName  string
}

func parseInstanceExtraConfig(raw []byte) instanceExtraConfig {
	var cfg instanceExtraConfig
	if len(raw) == 0 {
		return cfg
	}
	_ = json.Unmarshal(raw, &cfg)
	return cfg
}

func buildFlatResources(data *[]map[string]interface{}, resourceType string) []schemaResource {
	resources := make([]schemaResource, 0)
	for _, row := range *data {
		name, _ := row["TABLE_SCHEMA"].(string)
		if name == "" {
			continue
		}
		resources = append(resources, schemaResource{
			Path:         name,
			ParentPath:   "",
			ResourceType: resourceType,
			DisplayName:  name,
		})
	}
	return resources
}

func syncSchemaResources(instanceID string, resources []schemaResource) {
	seen := map[string]struct{}{}
	for _, resource := range resources {
		seen[resource.Path] = struct{}{}
		var current models.InsightInstanceSchemas
		result := global.App.DB.Table("insight_instance_schemas").
			Where("instance_id=? and `schema`=?", instanceID, resource.Path).
			First(&current)
		if result.Error != nil {
			if result.Error == gorm.ErrRecordNotFound {
				global.App.DB.Create(&models.InsightInstanceSchemas{
					InstanceID:   instanceID,
					Schema:       resource.Path,
					ParentPath:   resource.ParentPath,
					ResourceType: resource.ResourceType,
					DisplayName:  resource.DisplayName,
					IsDeleted:    false,
				})
			}
			continue
		}
		global.App.DB.Model(&models.InsightInstanceSchemas{}).
			Where("instance_id=? and `schema`=?", instanceID, resource.Path).
			Updates(map[string]interface{}{
				"parent_path":   resource.ParentPath,
				"resource_type": resource.ResourceType,
				"display_name":  resource.DisplayName,
				"is_deleted":    false,
			})
	}

	var localResources []models.InsightInstanceSchemas
	global.App.DB.Table("insight_instance_schemas").Where("instance_id=?", instanceID).Find(&localResources)
	for _, local := range localResources {
		if _, ok := seen[local.Schema]; ok {
			continue
		}
		global.App.DB.Model(&models.InsightInstanceSchemas{}).
			Where("instance_id=? and `schema`=?", instanceID, local.Schema).
			Update("is_deleted", true)
	}
}

func buildSQLServerDSN(user string, password string, hostname string, port int, database string) string {
	if database == "" {
		// 保持历史语义：备份链路始终显式指定 master，而非登录默认库
		database = "master"
	}
	return sqlserverdsn.DSN(user, password, hostname, port, database)
}

func scanSingleColumn(ctx context.Context, db *sql.DB, query string) ([]string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		result = append(result, value)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return result, nil
}

func SyncDBMetaForInstance(row models.InsightInstances) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	plainPassword, err := secret.Decrypt(row.Password)
	if err != nil {
		return fmt.Errorf("密码解密失败：%s", err.Error())
	}

	extraCfg := parseInstanceExtraConfig(row.ExtraConfig)

	resources := make([]schemaResource, 0)
	switch strings.ToLower(string(row.DbType)) {
	case "mysql", "tidb", "doris":
		db := dao.DB{
			User:     row.User,
			Password: plainPassword,
			Host:     row.Hostname,
			Port:     row.Port,
			Params:   map[string]string{"group_concat_max_len": "67108864"},
			Ctx:      ctx,
		}
		_, data, err := db.Query(mysqlQuery)
		if err != nil {
			return err
		}
		resources = buildFlatResources(data, "database")
	case "clickhouse":
		db := dao.ClickhouseDB{
			User:     row.User,
			Password: plainPassword,
			Host:     row.Hostname,
			Port:     row.Port,
			Ctx:      ctx,
		}
		_, data, err := db.Query(clickhouseQuery)
		if err != nil {
			return err
		}
		resources = buildFlatResources(data, "database")
	case "postgresql", "kingbase", "kingbasees":
		// 金仓 KingbaseES 的 pg_database / information_schema.schemata 与 PG 同构，
		// 库/schema 两级资源可直接复用同一组字典查询；只有默认连接库不同。
		adminDB := extraCfg.Database
		if adminDB == "" {
			adminDB = "postgres"
			if strings.HasPrefix(strings.ToLower(string(row.DbType)), "kingbase") {
				adminDB = "test"
			}
		}
		db := dao.PostgresDB{
			User:     row.User,
			Password: plainPassword,
			Host:     row.Hostname,
			Port:     row.Port,
			Database: adminDB,
			SSLMode:  extraCfg.SSLMode,
			Ctx:      ctx,
		}
		_, data, err := db.Query(postgresDatabaseQuery)
		if err != nil {
			return err
		}
		for _, d := range *data {
			databaseName, _ := d["database_name"].(string)
			if databaseName == "" {
				continue
			}
			resources = append(resources, schemaResource{
				Path:         databaseName,
				ParentPath:   "",
				ResourceType: "database",
				DisplayName:  databaseName,
			})
			db.Database = databaseName
			_, schemaRows, queryErr := db.Query(postgresSchemaQuery)
			if queryErr != nil {
				global.App.Log.Error(fmt.Sprintf("从主机%s:%d同步PG Schema失败，database=%s，错误信息：%s", row.Hostname, row.Port, databaseName, queryErr.Error()))
				continue
			}
			for _, schemaRow := range *schemaRows {
				schemaName, _ := schemaRow["schema_name"].(string)
				if schemaName == "" {
					continue
				}
				path := fmt.Sprintf("%s.%s", databaseName, schemaName)
				resources = append(resources, schemaResource{
					Path:         path,
					ParentPath:   databaseName,
					ResourceType: "schema",
					DisplayName:  schemaName,
				})
			}
		}
	case "sqlserver":
		adminDB := extraCfg.Database
		if adminDB == "" {
			adminDB = "master"
		}
		adminConn, err := sql.Open("sqlserver", buildSQLServerDSN(row.User, plainPassword, row.Hostname, row.Port, adminDB))
		if err != nil {
			return err
		}
		defer adminConn.Close()
		dbNames, err := scanSingleColumn(ctx, adminConn, sqlserverDatabaseQuery)
		if err != nil {
			return err
		}
		for _, databaseName := range dbNames {
			resources = append(resources, schemaResource{
				Path:         databaseName,
				ParentPath:   "",
				ResourceType: "database",
				DisplayName:  databaseName,
			})
			conn, err := sql.Open("sqlserver", buildSQLServerDSN(row.User, plainPassword, row.Hostname, row.Port, databaseName))
			if err != nil {
				global.App.Log.Error(fmt.Sprintf("从主机%s:%d同步SQLServer Schema失败，database=%s，错误信息：%s", row.Hostname, row.Port, databaseName, err.Error()))
				continue
			}
			schemaNames, qErr := scanSingleColumn(ctx, conn, sqlserverSchemaQuery)
			_ = conn.Close()
			if qErr != nil {
				global.App.Log.Error(fmt.Sprintf("从主机%s:%d同步SQLServer Schema失败，database=%s，错误信息：%s", row.Hostname, row.Port, databaseName, qErr.Error()))
				continue
			}
			for _, schemaName := range schemaNames {
				path := fmt.Sprintf("%s.%s", databaseName, schemaName)
				resources = append(resources, schemaResource{
					Path:         path,
					ParentPath:   databaseName,
					ResourceType: "schema",
					DisplayName:  schemaName,
				})
			}
		}
	case "oracle":
		serviceName := extraCfg.Service
		if serviceName == "" {
			return fmt.Errorf("Oracle 服务名不能为空")
		}
		dsn := go_ora.BuildUrl(row.Hostname, row.Port, serviceName, row.User, plainPassword, nil)
		conn, err := sql.Open("oracle", dsn)
		if err != nil {
			return err
		}
		defer conn.Close()
		schemaNames, err := scanSingleColumn(ctx, conn, oracleSchemaQuery)
		if err != nil {
			return err
		}
		for _, schemaName := range schemaNames {
			resources = append(resources, schemaResource{
				Path:         schemaName,
				ParentPath:   "",
				ResourceType: "schema",
				DisplayName:  schemaName,
			})
		}
	case "oceanbase":
		/* OceanBase 按租户模式复用 MySQL / Oracle 两套枚举口径：
		     - Oracle 模式：schema = 用户（all_users），与 Oracle 一致；
		     - MySQL 模式：database = information_schema.schemata，与 MySQL 一致。
		   模式取自 extra_config.oceanbase_mode，缺省 MySQL 模式。 */
		if dbfamily.OceanBaseOracleMode(row.ExtraConfig) {
			serviceName := extraCfg.Service
			if serviceName == "" {
				return fmt.Errorf("OceanBase（Oracle 模式）服务名不能为空")
			}
			dsn := go_ora.BuildUrl(row.Hostname, row.Port, serviceName, row.User, plainPassword, nil)
			conn, err := sql.Open("oracle", dsn)
			if err != nil {
				return err
			}
			defer conn.Close()
			schemaNames, err := scanSingleColumn(ctx, conn, oracleSchemaQuery)
			if err != nil {
				return err
			}
			for _, schemaName := range schemaNames {
				resources = append(resources, schemaResource{
					Path:         schemaName,
					ParentPath:   "",
					ResourceType: "schema",
					DisplayName:  schemaName,
				})
			}
			break
		}
		db := dao.DB{
			User:     row.User,
			Password: plainPassword,
			Host:     row.Hostname,
			Port:     row.Port,
			Params:   map[string]string{"group_concat_max_len": "67108864"},
			Ctx:      ctx,
		}
		_, data, err := db.Query(mysqlQuery)
		if err != nil {
			return err
		}
		resources = buildFlatResources(data, "database")
	default:
		return fmt.Errorf("不支持的数据库类型：%s", row.DbType)
	}

	if len(resources) == 0 {
		return errors.New("未同步到任何资源，请检查账号权限或连接参数")
	}

	syncSchemaResources(row.InstanceID, resources)
	return nil
}

// 从用户定义的远程数据库实例同步库信息
//
// 2026-11-09 起它只是三条刷新路径里的「定时对账」一条（见 meta_refresh.go）：
// 平台内建库已由写后失效 + 读时惰性刷新覆盖，这里降到 */30 兜底
// 「平台完全没感知到的变更」（DBA 用客户端直连建库）。
// 走 RefreshInstanceMeta 而不是直接调 SyncDBMetaForInstance，是为了把同步状态
// 一并落进 insight_instance_meta_state —— 否则页面会认为这条实例「从未同步过」，
// 每次打开都触发一次多余的惰性刷新。
func SyncDBMeta() {
	// 获取数据库配置列表
	var records []models.InsightInstances
	global.App.DB.Table("insight_instances").Scan(&records)
	// 启动4个并发
	var wg sync.WaitGroup
	ch := make(chan struct{}, 4)
	for _, row := range records {
		ch <- struct{}{}
		wg.Add(1)
		// 获取目标数据库的库信息
		go func(row models.InsightInstances) {
			defer func() {
				<-ch
				wg.Done()
			}()

			if _, err := RefreshInstanceMeta(row.InstanceID); err != nil {
				global.App.Log.Error(fmt.Sprintf("从主机%s:%d同步元数据失败，错误信息：%s", row.Hostname, row.Port, err.Error()))
			}
		}(row)
	}
	wg.Wait()
}
