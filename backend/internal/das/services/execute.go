package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/global"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/pkg/secret"

	"gorm.io/gorm"
)

// 获取DB配置
func GetDBConfig(instance_id string) (instanceCfg commonModels.InsightInstances, err error) {
	r := global.App.DB.Table("`insight_instances` a").
		Select("a.`hostname`, a.`port`, a.`User`, a.`Password`, a.`db_type`, a.`extra_config`").
		Where("a.instance_id=?", instance_id).
		Take(&instanceCfg)
	// 判断记录是否存在
	if errors.Is(r.Error, gorm.ErrRecordNotFound) {
		return instanceCfg, fmt.Errorf("指定DB配置的记录不存在，错误的信息:%s", r.Error.Error())
	}
	return instanceCfg, nil
}

// 获取DB类型
func GetDbType(instance_id string) (string, error) {
	type dbTypeResult struct {
		DbType string `json:"db_type"`
	}
	var result dbTypeResult
	r := global.App.DB.Table("`insight_instances` a").
		Select("a.`db_type`").
		Where("a.instance_id=?", instance_id).
		Take(&result)
	// 判断记录是否存在
	if errors.Is(r.Error, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("指定DB配置的记录不存在，错误信息:%s", r.Error.Error())
	}
	return result.DbType, nil
}

// 解析UUID
func ParserUUID(instance_id string) (id string, err error) {
	id = strings.TrimSpace(instance_id)
	if id == "" {
		return "", fmt.Errorf("instance id is empty")
	}
	return id, nil
}

// 计算延时
func CalculateDuration(instanceCfg commonModels.InsightInstances, callback func(instanceCfg commonModels.InsightInstances) (*[]string, *[]map[string]interface{}, error)) (*[]string, *[]map[string]interface{}, int64, error) {
	startTime := time.Now()
	columns, data, err := callback(instanceCfg)
	endTime := time.Now()
	return columns, data, int64(endTime.Sub(startTime) / time.Millisecond), err
}

// 响应data
type ResponseData struct {
	SQLText  string                    `json:"sqltext"`
	Duration string                    `json:"duration"`
	QueryID  string                    `json:"query_id"`
	Data     *[]map[string]interface{} `json:"data"`
	Columns  *[]string                 `json:"columns"`
	// 数据安全脱敏挂钩（views 层填充）：本次结果被脱敏的列名与引用的明文工单号。
	// 未命中脱敏规则时为空，不改变既有前端消费逻辑。
	MaskedColumns []string `json:"masked_columns,omitempty"`
	PlainGrantNo  string   `json:"plain_grant_no,omitempty"`
}

// 执行接口
type ExecuteApi interface {
	Execute(instanceCfg commonModels.InsightInstances) (*[]string, *[]map[string]interface{}, error)
}

type ClickHouseExecuteApi struct {
	*forms.ExecuteClickHouseQueryForm
	Ctx context.Context
}

func (m ClickHouseExecuteApi) Execute(instanceCfg commonModels.InsightInstances) (*[]string, *[]map[string]interface{}, error) {
	// 解密密码
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, nil, err
	}

	db := dao.ClickhouseDB{
		User:     instanceCfg.User,
		Password: plainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Database: m.Schema,
		Settings: m.Params,
		Ctx:      m.Ctx,
	}
	columns, data, err := db.Query(m.SQLText)
	if err != nil {
		return nil, nil, err
	}
	return columns, data, nil
}

type MySQLExecuteApi struct {
	*forms.ExecuteMySQLQueryForm
	Ctx context.Context
}

func (m MySQLExecuteApi) Execute(instanceCfg commonModels.InsightInstances) (*[]string, *[]map[string]interface{}, error) {
	// 解密密码
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, nil, err
	}

	db := dao.DB{
		User:     instanceCfg.User,
		Password: plainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Database: m.Schema,
		Params:   m.Params,
		Ctx:      m.Ctx,
	}
	columns, data, err := db.Query(m.SQLText)
	if err != nil {
		return nil, nil, err
	}
	return columns, data, nil
}

type postgresExecuteExtraConfig struct {
	SSLMode string `json:"sslmode"`
}

type PostgresExecuteApi struct {
	*forms.ExecutePostgresQueryForm
	Ctx context.Context
}

func (m PostgresExecuteApi) Execute(instanceCfg commonModels.InsightInstances) (*[]string, *[]map[string]interface{}, error) {
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, nil, err
	}

	schemaPath := strings.TrimSpace(m.Schema)
	databaseName := ""
	schemaName := ""
	if schemaPath != "" {
		parts := strings.SplitN(schemaPath, ".", 2)
		if len(parts) == 2 {
			databaseName = parts[0]
			schemaName = parts[1]
		} else {
			databaseName = schemaPath
		}
	}

	cfg := postgresExecuteExtraConfig{}
	if len(instanceCfg.ExtraConfig) > 0 {
		_ = json.Unmarshal(instanceCfg.ExtraConfig, &cfg)
	}

	db := dao.PostgresDB{
		User:     instanceCfg.User,
		Password: plainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Database: databaseName,
		SSLMode:  cfg.SSLMode,
		Ctx:      m.Ctx,
	}
	if schemaName != "" {
		return db.QueryWithSearchPath(schemaName, m.SQLText)
	}
	return db.Query(m.SQLText)
}

type SQLServerExecuteApi struct {
	*forms.ExecuteSQLServerQueryForm
	Ctx context.Context
}

func (m SQLServerExecuteApi) Execute(instanceCfg commonModels.InsightInstances) (*[]string, *[]map[string]interface{}, error) {
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, nil, err
	}

	schemaPath := strings.TrimSpace(m.Schema)
	databaseName := ""
	if schemaPath != "" {
		parts := strings.SplitN(schemaPath, ".", 2)
		databaseName = parts[0]
	}

	db := dao.SQLServerDB{
		User:     instanceCfg.User,
		Password: plainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Database: databaseName,
		Ctx:      m.Ctx,
	}
	return db.Query(m.SQLText)
}

type oracleExtraConfig struct {
	ServiceName string `json:"service_name"`
}

type OracleExecuteApi struct {
	*forms.ExecuteOracleQueryForm
	Ctx context.Context
}

func (m OracleExecuteApi) Execute(instanceCfg commonModels.InsightInstances) (*[]string, *[]map[string]interface{}, error) {
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, nil, err
	}

	cfg := oracleExtraConfig{}
	if len(instanceCfg.ExtraConfig) > 0 {
		_ = json.Unmarshal(instanceCfg.ExtraConfig, &cfg)
	}

	db := dao.OracleDB{
		User:          instanceCfg.User,
		Password:      plainPassword,
		Host:          instanceCfg.Hostname,
		Port:          instanceCfg.Port,
		ServiceName:   cfg.ServiceName,
		CurrentSchema: m.Schema,
		Ctx:           m.Ctx,
	}
	return db.Query(m.SQLText)
}
