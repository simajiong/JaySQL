package execute

import (
	"errors"
	"fmt"

	"github.com/simajiong/jaysql/internal/orders/api/base"
	"github.com/simajiong/jaysql/internal/orders/api/mysql"
	"github.com/simajiong/jaysql/internal/orders/api/tidb"
)

type Executor interface {
	Run() (base.ReturnData, error)
}

type ExecuteSQLAPI struct {
	*base.DBConfig
	Executor
}

type UnsupportedExecutor struct {
	*base.DBConfig
}

func (e *ExecuteSQLAPI) Run() (data base.ReturnData, err error) {
	data, err = e.Executor.Run()
	if err != nil {
		data.Error = err.Error()
	}
	return
}

func NewExecuteSQLAPI(config *base.DBConfig) *ExecuteSQLAPI {
	switch config.DBType {
	case "MySQL":
		return &ExecuteSQLAPI{config, &MySQLExecutor{config}}
	case "Doris":
		return &ExecuteSQLAPI{config, &MySQLExecutor{config}}
	case "TiDB":
		return &ExecuteSQLAPI{config, &TiDBExecutor{config}}
	case "PostgreSQL":
		return &ExecuteSQLAPI{config, &PostgresExecutor{config}}
	case "Kingbase":
		// 金仓兼容 PG 协议，执行器直接复用 PostgresExecutor（DSN/事务/结果集处理完全一致）
		return &ExecuteSQLAPI{config, &PostgresExecutor{config}}
	case "SQLServer":
		return &ExecuteSQLAPI{config, &SQLServerExecutor{config}}
	case "Oracle":
		return &ExecuteSQLAPI{config, &OracleExecutor{config}}
	case "ClickHouse":
		return &ExecuteSQLAPI{config, &UnsupportedExecutor{config}}
	}
	return &ExecuteSQLAPI{config, &UnsupportedExecutor{config}}
}

// MySQL
type MySQLExecutor struct {
	*base.DBConfig
}

func (m *MySQLExecutor) Run() (data base.ReturnData, err error) {
	switch m.SQLType {
	case "DML":
		execute := mysql.ExecuteMySQLDML{DBConfig: m.DBConfig}
		return execute.Run()
	case "DDL":
		execute := mysql.ExecuteMySQLDDL{DBConfig: m.DBConfig}
		return execute.Run()
	case "EXPORT":
		execute := mysql.ExecuteMySQLExportToFile{DBConfig: m.DBConfig}
		return execute.Run()
	default:
		data.Error = fmt.Sprintf("不支持的SQL类型：%s", m.SQLType)
		err = errors.New(data.Error)
		return
	}
}

// TiDBExecutor 结构体用于执行TiDB数据库操作
type TiDBExecutor struct {
	// 继承自 DBConfig 结构体，包含数据库连接信息
	*base.DBConfig
}

// Run 方法根据 SQL 类型执行 TiDB 操作并返回结果和错误信息
func (m *TiDBExecutor) Run() (data base.ReturnData, err error) {
	switch m.SQLType {
	case "DML":
		execute := tidb.ExecuteTiDBDML{DBConfig: m.DBConfig}
		return execute.Run()
	case "DDL":
		execute := tidb.ExecuteTiDBDDL{DBConfig: m.DBConfig}
		return execute.Run()
	case "EXPORT":
		execute := tidb.ExecuteTiDBExportToFile{DBConfig: m.DBConfig}
		return execute.Run()
	default:
		data.Error = fmt.Sprintf("不支持的SQL类型：%s", m.SQLType)
		err = errors.New(data.Error)
		return
	}
}

func (m *UnsupportedExecutor) Run() (data base.ReturnData, err error) {
	data.Error = fmt.Sprintf("当前数据库类型暂不支持工单执行：%s", m.DBType)
	err = errors.New(data.Error)
	return
}
