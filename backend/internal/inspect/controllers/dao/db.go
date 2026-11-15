package dao

import (
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"

	"github.com/simajiong/jaysql/internal/common/sqlserverdsn"
)

// DB Struct
type DB struct {
	User     string
	Password string
	Host     string
	Port     int
	Database string
	Schema   string // PG 专用：目标 schema；为空时用 current_schema()
	DbType   string // mysql / postgres；为空按 mysql 处理，保持既有调用方兼容
}

func (d *DB) isPostgres() bool {
	switch strings.ToLower(strings.TrimSpace(d.DbType)) {
	case "postgres", "postgresql", "pg",
		// 金仓兼容 PG 协议：pgx 驱动、url 形式的 DSN、当前库/字符集查询全部通用
		"kingbase", "kingbasees":
		return true
	}
	return false
}

// isSQLServer 判断是否按 SQL Server 协议连接（实例表存的字面值是 `SQLServer`）。
func (d *DB) isSQLServer() bool {
	switch strings.ToLower(strings.TrimSpace(d.DbType)) {
	case "sqlserver", "sql server", "mssql":
		return true
	}
	return false
}

// Open connection for db
func (d *DB) Open() (*sql.DB, error) {
	if d.isPostgres() {
		return d.openPostgres()
	}
	if d.isSQLServer() {
		return d.openSQLServer()
	}
	return d.openMySQL()
}

// openSQLServer 建立 SQL Server 连接。
//
// DSN 统一由 sqlserverdsn 构建：必须带 tlsmin=1.0，否则 SQL Server 2012 等只支持 TLS 1.0
// 的老版本会握手失败（tls: server selected unsupported protocol version 301）。
// 超时口径与 MySQL/PG 一致：连接 3s；语句级超时由参数 context 控制，这里不额外设置。
func (d *DB) openSQLServer() (*sql.DB, error) {
	dsn := sqlserverdsn.DSN(d.User, d.Password, d.Host, d.Port, d.Database)
	db, err := sql.Open("sqlserver", dsn)
	db.SetMaxOpenConns(1)
	return db, err
}

func (d *DB) openMySQL() (*sql.DB, error) {
	config := mysql.Config{
		User:                 d.User,
		Passwd:               d.Password,
		Addr:                 fmt.Sprintf("%s:%d", d.Host, d.Port),
		Net:                  "tcp",
		DBName:               d.Database,
		AllowNativePasswords: true,
		Timeout:              3000 * time.Millisecond,
		ReadTimeout:          3000 * time.Millisecond,
		WriteTimeout:         3000 * time.Millisecond,
	}

	DSN := config.FormatDSN()
	db, err := sql.Open("mysql", DSN)
	db.SetMaxOpenConns(1)
	return db, err
}

func (d *DB) openPostgres() (*sql.DB, error) {
	// 与 MySQL 侧口径一致：连接 3s 超时、语句 3s 超时（statement_timeout 单位 ms）
	pgURL := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(d.User, d.Password),
		Host:     fmt.Sprintf("%s:%d", d.Host, d.Port),
		Path:     "/" + d.Database,
		RawQuery: "sslmode=disable&connect_timeout=3&statement_timeout=3000&application_name=goinsight-inspect",
	}
	db, err := sql.Open("pgx", pgURL.String())
	db.SetMaxOpenConns(1)
	return db, err
}

// Executes a query without returning any rows.
func (d *DB) Execute(query string) error {
	db, err := d.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(query)
	return err
}

// Query
func (d *DB) Query(query string) (*[]map[string]interface{}, error) {
	db, err := d.Open()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	// 执行查询
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	// 获取列名
	columns, error := rows.Columns()
	if error != nil {
		return nil, error
	}
	// Make a slice
	vals := make([]interface{}, len(columns))
	for i := range columns {
		vals[i] = new(sql.RawBytes)
	}
	// Fetch rows
	result := make([]map[string]interface{}, 0)
	for rows.Next() {
		if err := rows.Scan(vals...); err != nil {
			return nil, err
		}
		// var value string
		vmap := make(map[string]interface{}, len(vals))
		for i, c := range vals {
			// 类型断言
			switch v := c.(type) {
			case *sql.RawBytes:
				if *v == nil {
					// nil在前端解析的是null，符合预期，也可以直接return nil
					vmap[columns[i]] = "NULL"
				} else {
					vmap[columns[i]] = string(*v)
				}
			}
		}
		result = append(result, vmap)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}

	// Rows.Err will report the last error encountered by Rows.Scan.
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetDBVars retrieves database variables.
func GetDBVars(db *DB) (map[string]string, error) {
	if db.isPostgres() {
		return getPostgresDBVars(db)
	}
	return getMySQLDBVars(db)
}

// getPostgresDBVars 获取 PostgreSQL 实例的版本与字符集，映射到与 MySQL 相同的 kv 键，
// 保证下游检查（dbVersion 判断、字符集解析）无需感知数据库类型。
// largePrefix / innodbDefaultRowFormat 是 InnoDB 专属概念，PG 固定给默认值。
func getPostgresDBVars(db *DB) (map[string]string, error) {
	result, err := db.Query(`SELECT version() AS version, current_setting('server_encoding') AS encoding`)
	if err != nil {
		return nil, err
	}

	data := map[string]string{
		"dbVersion":              "",
		"dbCharset":              "utf8",
		"largePrefix":            "OFF",
		"innodbDefaultRowFormat": "dynamic",
	}
	for _, row := range *result {
		if v, ok := row["version"].(string); ok {
			data["dbVersion"] = v
		}
		if v, ok := row["encoding"].(string); ok && v != "" {
			data["dbCharset"] = strings.ToLower(v)
		}
	}
	return data, nil
}

func getMySQLDBVars(db *DB) (map[string]string, error) {
	result, err := db.Query(`SHOW VARIABLES WHERE Variable_name IN ('innodb_large_prefix','version','character_set_database','innodb_default_row_format')`)
	if err != nil {
		return nil, err
	}

	var data map[string]string = map[string]string{
		"dbVersion":              "",
		"dbCharset":              "utf8",
		"largePrefix":            "OFF",
		"innodbDefaultRowFormat": "dynamic",
	}

	// [map[Value:utf8 Variable_name:character_set_database] map[Value:5.7.35-log Variable_name:version]]
	for _, row := range *result {
		variableName, ok := row["Variable_name"].(string)
		if !ok {
			return nil, fmt.Errorf("Variable_name类型意外")
		}

		value, ok := row["Value"].(string)
		if !ok {
			return nil, fmt.Errorf("行中Value类型意外")
		}

		switch variableName {
		case "version":
			data["dbVersion"] = value
		case "character_set_database":
			data["dbCharset"] = value
		case "innodb_large_prefix":
			switch value {
			case "0":
				data["largePrefix"] = "OFF"
			case "1":
				data["largePrefix"] = "ON"
			default:
				data["largePrefix"] = strings.ToUpper(value)
			}
		case "innodb_default_row_format":
			data["innodbDefaultRowFormat"] = value
		}
	}
	return data, nil
}

// countRows 从查询结果中取出 COUNT(*) 的值（列名固定为 count）
func countRows(result *[]map[string]interface{}) int {
	for _, row := range *result {
		if v, ok := row["count"].(string); ok {
			count, _ := strconv.Atoi(v)
			return count
		}
	}
	return 0
}
