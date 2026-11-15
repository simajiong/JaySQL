package execute

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/microsoft/go-mssqldb"
	go_ora "github.com/sijms/go-ora/v2"
	"github.com/simajiong/jaysql/internal/common/sqlserverdsn"
	"github.com/simajiong/jaysql/internal/orders/api/base"
	"github.com/simajiong/jaysql/internal/orders/api/postgres"
	"github.com/simajiong/jaysql/pkg/sqlsplit"
	"github.com/simajiong/jaysql/pkg/utils"
)

type PostgresExecutor struct {
	*base.DBConfig
}

type SQLServerExecutor struct {
	*base.DBConfig
}

type OracleExecutor struct {
	*base.DBConfig
}

type postgresExtraConfig struct {
	SSLMode string `json:"sslmode"`
}

type oracleExtraConfig struct {
	ServiceName string `json:"service_name"`
}

// parseDatabaseAndSchema 拆分「database.schema」资源路径。
// 仅传 database 单段时视为未选 schema，返回空 schema（连接后使用数据库默认 search_path），不再报错阻断执行。
func parseDatabaseAndSchema(resourcePath string) (string, string, error) {
	parts := strings.SplitN(strings.TrimSpace(resourcePath), ".", 2)
	if len(parts) != 2 {
		return parts[0], "", nil
	}
	return parts[0], parts[1], nil
}

func (m *PostgresExecutor) Run() (data base.ReturnData, err error) {
	if m.SQLType == "EXPORT" {
		data.Error = "PostgreSQL 暂不支持导出工单"
		return data, errors.New(data.Error)
	}
	// DML 走独立执行器：除执行本身外，还负责按条件生成精确回滚 SQL
	// （逻辑解码方案，见 internal/orders/api/postgres 包注释）。DDL 无行级回滚，
	// 维持本函数内直接执行。
	if m.SQLType == "DML" {
		execute := postgres.ExecutePostgresDML{DBConfig: m.DBConfig}
		return execute.Run()
	}
	logger := base.NewExecuteLogger()
	publisher := base.NewPublisher()
	logFn := func(msg string) {
		cleaned := strings.TrimSpace(msg)
		formatted := logger.Add(cleaned) + "\n"
		publisher.Publish(m.OrderID, m.TaskID, utils.RenderLogStream, formatted)
	}
	databaseName, schemaName, err := parseDatabaseAndSchema(m.Schema)
	if err != nil {
		data.Error = err.Error()
		return data, err
	}
	cfg := postgresExtraConfig{}
	if len(m.ExtraConfig) > 0 {
		_ = json.Unmarshal(m.ExtraConfig, &cfg)
	}
	sslMode := cfg.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	connString := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s", m.UserName, m.Password, m.Hostname, m.Port, databaseName, sslMode)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, connString)
	if err != nil {
		logFn(fmt.Sprintf("访问数据库(%s:%d)失败，错误：%s", m.Hostname, m.Port, err.Error()))
		data.ExecuteLog = logger.String()
		data.Error = err.Error()
		return data, err
	}
	defer conn.Close(ctx)
	logFn(fmt.Sprintf("访问数据库(%s:%d)成功", m.Hostname, m.Port))
	if schemaName != "" {
		escaped := `"` + strings.ReplaceAll(schemaName, `"`, `""`) + `"`
		if _, err = conn.Exec(ctx, fmt.Sprintf("set search_path to %s", escaped)); err != nil {
			logFn(fmt.Sprintf("设置search_path失败，错误：%s", err.Error()))
			data.ExecuteLog = logger.String()
			data.Error = err.Error()
			return data, err
		}
		logFn(fmt.Sprintf("已设置search_path为 %s", escaped))
	}
	pid := conn.PgConn().PID()
	logFn(fmt.Sprintf("数据库Backend PID：%d", pid))
	parts := sqlsplit.Split(m.SQL)
	if len(parts) == 0 {
		parts = []string{m.SQL}
	}
	validParts := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			validParts = append(validParts, p)
		}
	}
	if len(validParts) == 0 {
		data.ExecuteLog = logger.String()
		data.Error = "没有可执行的SQL语句"
		return data, errors.New(data.Error)
	}
	startTime := time.Now()
	var totalAffected int64
	for idx, one := range validParts {
		logFn(fmt.Sprintf("开始执行第 %d/%d 条SQL", idx+1, len(validParts)))
		startT := time.Now()
		tag, execErr := conn.Exec(ctx, one)
		cost := utils.HumanfriendlyTimeUnit(time.Since(startT))
		if execErr != nil {
			logFn(fmt.Sprintf("第 %d/%d 条SQL执行失败，错误：%s，耗时：%s", idx+1, len(validParts), execErr.Error(), cost))
			data.ExecuteLog = logger.String()
			data.Error = execErr.Error()
			data.AffectedRows = totalAffected
			return data, execErr
		}
		n := tag.RowsAffected()
		totalAffected += n
		logFn(fmt.Sprintf("第 %d/%d 条SQL执行成功，影响行数%d，耗时：%s", idx+1, len(validParts), n, cost))
	}
	executeCostTime := utils.HumanfriendlyTimeUnit(time.Since(startTime))
	logFn(fmt.Sprintf("SQL全部执行成功，累计影响行数%d，总耗时：%s", totalAffected, executeCostTime))
	data.ExecuteLog = logger.String()
	data.AffectedRows = totalAffected
	data.ExecuteCostTime = executeCostTime
	return data, nil
}

func (m *SQLServerExecutor) Run() (data base.ReturnData, err error) {
	if m.SQLType == "EXPORT" {
		data.Error = "SQLServer 暂不支持导出工单"
		return data, errors.New(data.Error)
	}
	logger := base.NewExecuteLogger()
	publisher := base.NewPublisher()
	logFn := func(msg string) {
		cleaned := strings.TrimSpace(msg)
		formatted := logger.Add(cleaned) + "\n"
		publisher.Publish(m.OrderID, m.TaskID, utils.RenderLogStream, formatted)
	}
	databaseName, _, err := parseDatabaseAndSchema(m.Schema)
	if err != nil {
		data.Error = err.Error()
		return data, err
	}
	dsn := sqlserverdsn.DSN(m.UserName, m.Password, m.Hostname, int(m.Port), databaseName)
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		data.Error = err.Error()
		return data, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	// 分号拆句 + 逐条执行，避免多语句整包塞 ExecContext 报错
	parts := sqlsplit.Split(m.SQL)
	if len(parts) == 0 {
		parts = []string{m.SQL}
	}
	validParts := make([]string, 0, len(parts))
	for _, p := range parts {
		cleaned := strings.TrimSpace(p)
		cleaned = strings.TrimSuffix(cleaned, ";")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned != "" {
			validParts = append(validParts, cleaned)
		}
	}
	if len(validParts) == 0 {
		data.Error = "没有可执行的SQL语句"
		return data, errors.New(data.Error)
	}
	ctx := context.Background()
	startTime := time.Now()
	var totalAffected int64
	for idx, one := range validParts {
		logFn(fmt.Sprintf("开始执行第 %d/%d 条SQL", idx+1, len(validParts)))
		startT := time.Now()
		result, execErr := db.ExecContext(ctx, one)
		cost := utils.HumanfriendlyTimeUnit(time.Since(startT))
		if execErr != nil {
			logFn(fmt.Sprintf("第 %d/%d 条SQL执行失败，错误：%s，耗时：%s", idx+1, len(validParts), execErr.Error(), cost))
			data.ExecuteLog = logger.String()
			data.Error = execErr.Error()
			data.AffectedRows = totalAffected
			return data, execErr
		}
		n, _ := result.RowsAffected()
		totalAffected += n
		logFn(fmt.Sprintf("第 %d/%d 条SQL执行成功，影响行数%d，耗时：%s", idx+1, len(validParts), n, cost))
	}
	executeCostTime := utils.HumanfriendlyTimeUnit(time.Since(startTime))
	logFn(fmt.Sprintf("SQL全部执行成功，累计影响行数%d，总耗时：%s", totalAffected, executeCostTime))
	data.ExecuteLog = logger.String()
	data.AffectedRows = totalAffected
	data.ExecuteCostTime = executeCostTime
	return data, nil
}

func (m *OracleExecutor) Run() (data base.ReturnData, err error) {
	if m.SQLType == "EXPORT" {
		data.Error = "Oracle 暂不支持导出工单"
		return data, errors.New(data.Error)
	}
	logger := base.NewExecuteLogger()
	publisher := base.NewPublisher()
	logFn := func(msg string) {
		cleaned := strings.TrimSpace(msg)
		formatted := logger.Add(cleaned) + "\n"
		publisher.Publish(m.OrderID, m.TaskID, utils.RenderLogStream, formatted)
	}
	cfg := oracleExtraConfig{}
	if len(m.ExtraConfig) > 0 {
		_ = json.Unmarshal(m.ExtraConfig, &cfg)
	}
	if cfg.ServiceName == "" {
		data.Error = "Oracle 服务名不能为空"
		return data, errors.New(data.Error)
	}
	dsn := go_ora.BuildUrl(m.Hostname, int(m.Port), cfg.ServiceName, m.UserName, m.Password, nil)
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		data.Error = err.Error()
		return data, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	ctx := context.Background()
	if strings.TrimSpace(m.Schema) != "" {
		if _, err = db.ExecContext(ctx, fmt.Sprintf("ALTER SESSION SET CURRENT_SCHEMA = %s", strings.ToUpper(strings.TrimSpace(m.Schema)))); err != nil {
			data.Error = err.Error()
			return data, err
		}
		logFn(fmt.Sprintf("已设置 CURRENT_SCHEMA = %s", strings.ToUpper(strings.TrimSpace(m.Schema))))
	}

	// 分号拆句 + 逐条执行（Oracle 语句末尾分号不是所有场景都支持，TrimSuffix 去掉）
	parts := sqlsplit.Split(m.SQL)
	if len(parts) == 0 {
		parts = []string{m.SQL}
	}
	validParts := make([]string, 0, len(parts))
	for _, p := range parts {
		cleaned := strings.TrimSpace(p)
		cleaned = strings.TrimSuffix(cleaned, ";")
		cleaned = strings.TrimSuffix(cleaned, "/")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned != "" {
			validParts = append(validParts, cleaned)
		}
	}
	if len(validParts) == 0 {
		data.Error = "没有可执行的SQL语句"
		return data, errors.New(data.Error)
	}
	startTime := time.Now()
	var totalAffected int64
	for idx, one := range validParts {
		logFn(fmt.Sprintf("开始执行第 %d/%d 条SQL", idx+1, len(validParts)))
		startT := time.Now()
		result, execErr := db.ExecContext(ctx, one)
		cost := utils.HumanfriendlyTimeUnit(time.Since(startT))
		if execErr != nil {
			logFn(fmt.Sprintf("第 %d/%d 条SQL执行失败，错误：%s，耗时：%s", idx+1, len(validParts), execErr.Error(), cost))
			data.ExecuteLog = logger.String()
			data.Error = execErr.Error()
			data.AffectedRows = totalAffected
			return data, execErr
		}
		n, _ := result.RowsAffected()
		totalAffected += n
		logFn(fmt.Sprintf("第 %d/%d 条SQL执行成功，影响行数%d，耗时：%s", idx+1, len(validParts), n, cost))
	}
	executeCostTime := utils.HumanfriendlyTimeUnit(time.Since(startTime))
	logFn(fmt.Sprintf("SQL全部执行成功，累计影响行数%d，总耗时：%s", totalAffected, executeCostTime))
	data.ExecuteLog = logger.String()
	data.AffectedRows = totalAffected
	data.ExecuteCostTime = executeCostTime
	return data, nil
}
