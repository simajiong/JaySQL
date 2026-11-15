package dao

import (
	"errors"
	"fmt"

	"github.com/simajiong/jaysql/pkg/kv"
	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/inspect/controllers/parser"

	mysqlapi "github.com/go-sql-driver/mysql"
)

// ShowCreateTable retrieves the structure of the specified table.
func ShowCreateTable(table string, db *DB, kv *kv.KVCache) (data interface{}, err error) {
	if err := validateIdentifier(table, "table"); err != nil {
		return nil, err
	}
	if db.isPostgres() {
		// PG 没有 SHOW CREATE TABLE：依赖既有表结构比对的规则（ALTER 改列/索引、INSERT 列校验等）
		// 在 PG 下降级跳过。调用方对该错误只 Warn 不中断，不影响其余规则检查。
		return nil, fmt.Errorf("PostgreSQL 暂不支持读取表结构进行比对检查，该项规则已跳过")
	}
	if db.isSQLServer() {
		// SQL Server 同样没有 SHOW CREATE TABLE（等价能力是脚本化生成 DDL，成本与语义都不同）：
		// SQL Server 引擎走 sys.* 元信息探针做存在性/数量判定，不依赖表结构反解。
		return nil, fmt.Errorf("SQL Server 暂不支持读取表结构进行比对检查，该项规则已跳过")
	}
	// Return table structure from cache if available
	data = kv.Get(table)
	if data != nil {
		return data, nil
	}
	query := fmt.Sprintf("SHOW CREATE TABLE `%s`", table)
	result, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	var createStatement string
	for _, sql := range *result {
		// Table
		if _, ok := sql["Create Table"]; ok {
			createStatement = sql["Create Table"].(string)
		}
		// View
		if _, ok := sql["Create View"]; ok {
			createStatement = sql["Create View"].(string)
		}
	}

	var warns []error
	data, warns, err = parser.NewParse(createStatement, "", "")
	if len(warns) > 0 {
		return nil, fmt.Errorf("解析警告: %s", utils.ErrsJoin("; ", warns))
	}
	if err != nil {
		return nil, fmt.Errorf("SQL语法解析错误: %s", err.Error())
	}
	kv.Put(table, data)
	return data, nil
}

// CheckIfTableExists checks if the specified table exists in the current database.
func CheckIfTableExists(table string, db *DB) (string, error) {
	if err := validateIdentifier(table, "table"); err != nil {
		return "", err
	}
	if db.isPostgres() {
		// PG 按 schema 限定判断表/视图是否存在，语义与 MySQL 的 DESC 探测一致；
		// schema 为空时退化为 current_schema()
		schemaCond := "table_schema = current_schema()"
		if db.Schema != "" {
			schemaCond = fmt.Sprintf("table_schema = '%s'", db.Schema)
		}
		result, err := db.Query(fmt.Sprintf("SELECT COUNT(*) AS count FROM information_schema.tables WHERE %s AND table_name = '%s'", schemaCond, table))
		if err != nil {
			return fmt.Sprintf("执行SQL失败, 主机: %s:%d, 错误: %s", db.Host, db.Port, err.Error()), err
		}
		if countRows(result) == 0 {
			return fmt.Sprintf("表或视图`%s`不存在", table), errors.New("error")
		}
		return fmt.Sprintf("表或视图`%s`已存在", table), nil
	}
	err := db.Execute(fmt.Sprintf("DESC `%s`", table))
	if me, ok := err.(*mysqlapi.MySQLError); ok {
		switch me.Number {
		case 1146:
			// Table does not exist
			return fmt.Sprintf("表或视图`%s`不存在", table), err
		case 1045:
			return fmt.Sprintf("无法访问目标数据库 %s:%d, %s", db.Host, db.Port, err.Error()), err
		}
	}
	return fmt.Sprintf("表或视图`%s`已存在", table), nil
}

// CheckIfDatabaseExists checks if the specified database exists.
func CheckIfDatabaseExists(database string, db *DB) (string, error) {
	if err := validateIdentifier(database, "database"); err != nil {
		return "", err
	}
	if db.isPostgres() {
		// PG 语义适配：MySQL 的 CREATE DATABASE 在 PG 对应 CREATE SCHEMA；
		// 目标名命中 schema 或 database 任一即视为已存在
		result, err := db.Query(fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='%s') + (SELECT COUNT(*) FROM pg_database WHERE datname='%s') AS count", database, database))
		if err != nil {
			return fmt.Sprintf("执行SQL失败, 主机: %s:%d, 错误: %s", db.Host, db.Port, err.Error()), err
		}
		if countRows(result) == 0 {
			return fmt.Sprintf("数据库`%s`不存在", database), errors.New("error")
		}
		return fmt.Sprintf("数据库`%s`已存在", database), nil
	}
	// Query the information_schema.schemata to check if the database exists
	result, err := db.Query(fmt.Sprintf("SELECT COUNT(*) as count FROM information_schema.schemata WHERE schema_name='%s'", database))
	if err != nil {
		return fmt.Sprintf("执行SQL失败, 主机: %s:%d, 错误: %s", db.Host, db.Port, err.Error()), err
	}
	if countRows(result) == 0 {
		// Database does not exist
		return fmt.Sprintf("数据库`%s`不存在", database), errors.New("error")
	}
	// Database exists
	return fmt.Sprintf("数据库`%s`已存在", database), nil
}

// CheckIfTableExistsCrossDB checks if the specified table exists across databases.
func CheckIfTableExistsCrossDB(table string, db *DB) (string, error) {
	if err := validateIdentifier(table, "table"); err != nil {
		return "", err
	}
	// Check if the table exists using information_schema.tables, suitable for cross-database checks
	// PG 下 information_schema.tables 同样可用（覆盖所有 schema，语义为跨库探测）
	result, err := db.Query(fmt.Sprintf("SELECT COUNT(*) AS count FROM information_schema.tables WHERE table_name='%s'", table))
	if err != nil {
		return fmt.Sprintf("执行SQL失败, 主机: %s:%d, 错误: %s", db.Host, db.Port, err.Error()), err
	}
	if countRows(result) == 0 {
		// Table does not exist
		return fmt.Sprintf("表或视图`%s`不存在", table), errors.New("error")
	}
	// Table exists
	return fmt.Sprintf("表或视图`%s`已存在", table), nil
}
