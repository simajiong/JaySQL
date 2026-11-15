package services

import (
	"encoding/json"
	"fmt"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/models"
	"github.com/simajiong/jaysql/internal/das/parser"
	"github.com/simajiong/jaysql/internal/global"
	"gorm.io/datatypes"
)

type ExecuteOracleQueryService struct {
	*forms.ExecuteOracleQueryForm
	C        *gin.Context
	Username string
}

func (s *ExecuteOracleQueryService) Run() (ResponseData, error) {
	var responseData ResponseData = ResponseData{SQLText: s.SQLText}

	normalized, err := validateSingleSelectOnly(s.SQLText)
	if err != nil {
		return responseData, err
	}

	params, _ := json.Marshal(map[string]interface{}{})
	record := &models.InsightDASRecords{
		Username:  s.Username,
		Schema:    s.Schema,
		Sqltext:   normalized,
		RequestID: requestid.Get(s.C),
		Params:    datatypes.JSON(params),
	}
	global.App.DB.Create(&record)

	uuid, err := ParserUUID(s.InstanceID)
	if err != nil {
		return responseData, err
	}
	global.App.DB.Model(&models.InsightDASRecords{}).
		Where("request_id=? and username=?", requestid.Get(s.C), s.Username).
		Update("InstanceID", uuid)

	// 先取配置：含 OceanBase Oracle 模式的实例同样走本通道，家族判定需要 extra_config。
	instanceCfg, err := GetDBConfig(s.InstanceID)
	if err != nil {
		return responseData, err
	}
	dbType := string(instanceCfg.DbType)
	if !IsOracleFamilyCfg(dbType, instanceCfg.ExtraConfig) {
		return responseData, fmt.Errorf("当前接口仅支持Oracle（含 OceanBase Oracle 模式），当前DB类型为%s", dbType)
	}

	// 按实际家族注入行数上限：OceanBase Oracle 模式同样需要 ROWNUM 包裹，
	// 直接传 "OceanBase" 会落到 applyQueryLimit 的 default 分支而失去限制。
	normalized, err = applyQueryLimit(resolveFamily(dbType, instanceCfg.ExtraConfig), normalized)
	if err != nil {
		return responseData, err
	}
	s.SQLText = normalized
	responseData.SQLText = normalized

	checker := CheckUserPerm{
		UserName:   s.Username,
		InstanceID: uuid,
		Tables:     []parser.Table{{Schema: s.Schema}},
	}
	if err := checker.HasSchemaPerms(); err != nil {
		return responseData, err
	}

	var executeApi ExecuteApi = OracleExecuteApi{ExecuteOracleQueryForm: s.ExecuteOracleQueryForm, Ctx: s.C.Request.Context()}
	columns, data, duration, err := CalculateDuration(instanceCfg, executeApi.Execute)
	if err != nil {
		return responseData, err
	}
	global.App.DB.Model(&models.InsightDASRecords{}).
		Where("request_id=? and username=?", requestid.Get(s.C), s.Username).
		Updates(map[string]any{"ReturnRows": len(*data), "Duration": duration})

	responseData.Duration = fmt.Sprintf("%dms", duration)
	responseData.Data = data
	responseData.Columns = columns
	// 写后失效：Oracle 的 schema 即用户，CREATE/DROP USER 会改变 schema 清单。
	NotifySchemaInventoryChanged(s.InstanceID, s.SQLText)
	return responseData, nil
}
