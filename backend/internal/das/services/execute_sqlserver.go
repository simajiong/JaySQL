package services

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/models"
	"github.com/simajiong/jaysql/internal/das/parser"
	"github.com/simajiong/jaysql/internal/global"
	"gorm.io/datatypes"
)

type ExecuteSQLServerQueryService struct {
	*forms.ExecuteSQLServerQueryForm
	C        *gin.Context
	Username string
}

func (s *ExecuteSQLServerQueryService) Run() (ResponseData, error) {
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

	dbType, err := GetDbType(s.InstanceID)
	if err != nil {
		return responseData, err
	}
	if !strings.EqualFold(dbType, "sqlserver") {
		return responseData, fmt.Errorf("当前接口仅支持SQLServer，当前DB类型为%s", dbType)
	}

	normalized, err = applyQueryLimit(dbType, normalized)
	if err != nil {
		return responseData, err
	}
	s.SQLText = normalized
	responseData.SQLText = normalized

	instanceCfg, err := GetDBConfig(s.InstanceID)
	if err != nil {
		return responseData, err
	}

	checker := CheckUserPerm{
		UserName:   s.Username,
		InstanceID: uuid,
		Tables:     []parser.Table{{Schema: s.Schema}},
	}
	if err := checker.HasSchemaPerms(); err != nil {
		return responseData, err
	}

	var executeApi ExecuteApi = SQLServerExecuteApi{ExecuteSQLServerQueryForm: s.ExecuteSQLServerQueryForm, Ctx: s.C.Request.Context()}
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
	// 写后失效：CREATE/DROP DATABASE|SCHEMA 成功后立即重建该实例快照。
	// SQLServer 一次同步要按库逐个连 sys.schemas，比其它引擎慢，这里尤其不能等定时任务。
	NotifySchemaInventoryChanged(s.InstanceID, s.SQLText)
	return responseData, nil
}
