package services

import (
	"fmt"
	"strings"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	commonModels "github.com/simajiong/jaysql/internal/common/models"

	"github.com/gin-gonic/gin"
)

type GetBackupObjectsService struct {
	*backupForms.GetBackupObjectsForm
	C *gin.Context
}

type backupObjectNode struct {
	Key      string `json:"key"`
	Title    string `json:"title"`
	IsLeaf   bool   `json:"isLeaf"`
	NodeKind string `json:"node_kind"`
	Database string `json:"database"`
	Schema   string `json:"schema"`
}

func (s *GetBackupObjectsService) Run() (interface{}, error) {
	instance, err := loadInstanceByID(s.InstanceID)
	if err != nil {
		return nil, err
	}
	dbType := string(instance.DbType)
	level := strings.TrimSpace(s.Level)

	// PostgreSQL 与 KingbaseES 的对象树（库 → schema → 表）一致，复用同一套查询；
	// 金仓走 PG 协议，information_schema / pg_catalog 均可用。
	switch {
	case strings.EqualFold(strings.TrimSpace(dbType), "MySQL"):
		return s.runMySQL(instance, level)
	case isPostgresLogicalFamily(dbType):
		return s.runPostgres(instance, level)
	default:
		return nil, fmt.Errorf("仅支持 MySQL/PostgreSQL/Kingbase")
	}
}

func (s *GetBackupObjectsService) runMySQL(instance *commonModels.InsightInstances, level string) (interface{}, error) {
	switch level {
	case "databases":
		dbs, err := listMySQLDatabases(instance.Hostname, instance.Port, instance.User, instance.Password)
		if err != nil {
			return nil, err
		}
		nodes := make([]backupObjectNode, 0, len(dbs))
		for _, db := range dbs {
			nodes = append(nodes, backupObjectNode{
				Key:      "db:" + db,
				Title:    db,
				IsLeaf:   false,
				NodeKind: "database",
				Database: db,
			})
		}
		return nodes, nil
	case "tables":
		if err := validateIdent(s.Database, "库名"); err != nil {
			return nil, err
		}
		tables, err := listMySQLTables(instance.Hostname, instance.Port, instance.User, instance.Password, s.Database)
		if err != nil {
			return nil, err
		}
		nodes := make([]backupObjectNode, 0, len(tables))
		for _, t := range tables {
			nodes = append(nodes, backupObjectNode{
				Key:      "db:" + s.Database + "/table:" + t,
				Title:    t,
				IsLeaf:   true,
				NodeKind: "table",
				Database: s.Database,
			})
		}
		return nodes, nil
	default:
		return nil, fmt.Errorf("level 参数非法")
	}
}

func (s *GetBackupObjectsService) runPostgres(instance *commonModels.InsightInstances, level string) (interface{}, error) {
	extra := parseExtraConfig(instance.ExtraConfig)
	sslmode := extra["sslmode"]
	defaultDB := extra["database"]
	if defaultDB == "" {
		defaultDB = "postgres"
	}

	switch level {
	case "databases":
		dbs, err := listPostgresDatabases(instance.Hostname, instance.Port, instance.User, instance.Password, defaultDB, sslmode)
		if err != nil {
			return nil, err
		}
		nodes := make([]backupObjectNode, 0, len(dbs))
		for _, db := range dbs {
			nodes = append(nodes, backupObjectNode{
				Key:      "db:" + db,
				Title:    db,
				IsLeaf:   false,
				NodeKind: "database",
				Database: db,
			})
		}
		return nodes, nil
	case "schemas":
		if err := validateIdent(s.Database, "数据库名"); err != nil {
			return nil, err
		}
		schemas, err := listPostgresSchemas(instance.Hostname, instance.Port, instance.User, instance.Password, s.Database, sslmode)
		if err != nil {
			return nil, err
		}
		nodes := make([]backupObjectNode, 0, len(schemas))
		for _, sc := range schemas {
			nodes = append(nodes, backupObjectNode{
				Key:      "db:" + s.Database + "/schema:" + sc,
				Title:    sc,
				IsLeaf:   false,
				NodeKind: "schema",
				Database: s.Database,
				Schema:   sc,
			})
		}
		return nodes, nil
	case "tables":
		if err := validateIdent(s.Database, "数据库名"); err != nil {
			return nil, err
		}
		if err := validateIdent(s.Schema, "Schema"); err != nil {
			return nil, err
		}
		tables, err := listPostgresTables(instance.Hostname, instance.Port, instance.User, instance.Password, s.Database, sslmode, s.Schema)
		if err != nil {
			return nil, err
		}
		nodes := make([]backupObjectNode, 0, len(tables))
		for _, t := range tables {
			nodes = append(nodes, backupObjectNode{
				Key:      "db:" + s.Database + "/schema:" + s.Schema + "/table:" + t,
				Title:    t,
				IsLeaf:   true,
				NodeKind: "table",
				Database: s.Database,
				Schema:   s.Schema,
			})
		}
		return nodes, nil
	default:
		return nil, fmt.Errorf("level 参数非法")
	}
}
