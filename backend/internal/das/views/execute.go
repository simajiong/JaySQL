package views

import (
	commonServices "github.com/simajiong/jaysql/internal/common/services"
	datasecServices "github.com/simajiong/jaysql/internal/datasec/services"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/response"

	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/models"
	"github.com/simajiong/jaysql/internal/das/services"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
)

// applyDataSecMasking 查询结果脱敏（数据安全模块统一挂钩）：
// 在 5 个执行视图的成功分支、response.Success 之前调用。
// 任何内部错误都不影响查询返回（脱敏是增强能力，配置拉不到时按无配置处理）；
// 命中规则时在响应里带上 masked_columns / plain_grant_no，供前端提示与审计关联。
func applyDataSecMasking(instanceID, username string, returnData *services.ResponseData) {
	masked, grantNo := datasecServices.MaskQueryResult(instanceID, username, returnData.Columns, returnData.Data)
	if len(masked) > 0 {
		returnData.MaskedColumns = masked
		returnData.PlainGrantNo = grantNo
	}
}

// 执行MySQL/TiDB查询
func ExecuteMySQLQueryView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form *forms.ExecuteMySQLQueryForm = &forms.ExecuteMySQLQueryForm{}
	var RequestID string = requestid.Get(c)

	if err := c.ShouldBind(&form); err == nil {
		service := services.ExecuteMySQLQueryService{
			ExecuteMySQLQueryForm: form,
			C:                     c,
			Username:              username,
		}
		returnData, err := service.Run()
		if err != nil {
			// 更新数据库记录
			global.App.DB.Model(&models.InsightDASRecords{}).
				Where("request_id=? and username=?", RequestID, username).
				Updates(map[string]any{"error_msg": err.Error(), "is_finish": true})
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_query",
				ModuleName:     "SQL 查询",
				ActionCode:     "execute",
				ActionName:     "执行 SQL",
				ActionResult:   "fail",
				ResourceType:   "schema",
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        "执行 SQL 查询失败",
				ErrorMessage:   err.Error(),
				RequestPayload: form,
				SQLText:        form.SQLText,
			})
			response.Fail(c, err.Error())
		} else {
			// 更新数据库记录
			global.App.DB.Model(&models.InsightDASRecords{}).
				Where("request_id=? and username=?", RequestID, username).
				Updates(map[string]any{"is_finish": true})
			rows := 0
			if returnData.Data != nil {
				rows = len(*returnData.Data)
			}
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_query",
				ModuleName:     "SQL 查询",
				ActionCode:     "execute",
				ActionName:     "执行 SQL",
				ActionResult:   "success",
				ResourceType:   "schema",
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        "执行 SQL 查询，返回结果集",
				RequestPayload: map[string]any{"instance_id": form.InstanceID, "schema": form.Schema},
				AfterData:      map[string]any{"query_id": returnData.QueryID, "duration": returnData.Duration, "rows": rows},
				SQLText:        returnData.SQLText,
			})
			applyDataSecMasking(form.InstanceID, username, &returnData)
			response.Success(c, returnData, "success")
		}
	} else {
		// 有效性验证不通过，更新数据库记录
		global.App.DB.Model(&models.InsightDASRecords{}).
			Where("request_id=? and username=?", RequestID, username).
			Updates(map[string]any{"error_msg": err.Error(), "is_finish": true})
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:     username,
			AuditLevel:   "normal",
			ModuleCode:   "sql_query",
			ModuleName:   "SQL 查询",
			ActionCode:   "execute",
			ActionName:   "执行 SQL",
			ActionResult: "fail",
			Summary:      "执行 SQL 查询失败",
			ErrorMessage: err.Error(),
		})
		response.ValidateFail(c, err.Error())
	}
}

// 解释MySQL/TiDB查询
func ExplainMySQLQueryView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form *forms.ExecuteMySQLQueryForm = &forms.ExecuteMySQLQueryForm{}
	var RequestID string = requestid.Get(c)

	if err := c.ShouldBind(&form); err == nil {
		service := services.ExplainMySQLQueryService{
			ExecuteMySQLQueryForm: form,
			C:                     c,
			Username:              username,
		}
		returnData, err := service.Run()
		if err != nil {
			global.App.DB.Model(&models.InsightDASRecords{}).
				Where("request_id=? and username=?", RequestID, username).
				Updates(map[string]any{"error_msg": err.Error(), "is_finish": true})
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_query",
				ModuleName:     "SQL 查询",
				ActionCode:     "explain",
				ActionName:     "解释 SQL",
				ActionResult:   "fail",
				ResourceType:   "schema",
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        "生成执行计划失败",
				ErrorMessage:   err.Error(),
				RequestPayload: form,
				SQLText:        form.SQLText,
			})
			response.Fail(c, err.Error())
		} else {
			global.App.DB.Model(&models.InsightDASRecords{}).
				Where("request_id=? and username=?", RequestID, username).
				Updates(map[string]any{"is_finish": true})
			rows := 0
			if returnData.Data != nil {
				rows = len(*returnData.Data)
			}
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_query",
				ModuleName:     "SQL 查询",
				ActionCode:     "explain",
				ActionName:     "解释 SQL",
				ActionResult:   "success",
				ResourceType:   "schema",
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        "生成 SQL 执行计划",
				RequestPayload: map[string]any{"instance_id": form.InstanceID, "schema": form.Schema},
				AfterData:      map[string]any{"query_id": returnData.QueryID, "duration": returnData.Duration, "rows": rows},
				SQLText:        returnData.SQLText,
			})
			response.Success(c, returnData, "success")
		}
	} else {
		global.App.DB.Model(&models.InsightDASRecords{}).
			Where("request_id=? and username=?", RequestID, username).
			Updates(map[string]any{"error_msg": err.Error(), "is_finish": true})
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:     username,
			AuditLevel:   "normal",
			ModuleCode:   "sql_query",
			ModuleName:   "SQL 查询",
			ActionCode:   "explain",
			ActionName:   "解释 SQL",
			ActionResult: "fail",
			Summary:      "生成执行计划失败",
			ErrorMessage: err.Error(),
		})
		response.ValidateFail(c, err.Error())
	}
}

// 执行ClickHouse查询
func ExecuteClickHouseQueryView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form *forms.ExecuteClickHouseQueryForm = &forms.ExecuteClickHouseQueryForm{}
	var RequestID string = requestid.Get(c)

	if err := c.ShouldBind(&form); err == nil {
		service := services.ExecuteClickHouseQueryService{
			ExecuteClickHouseQueryForm: form,
			C:                          c,
			Username:                   username,
		}
		returnData, err := service.Run()
		if err != nil {
			// 更新数据库记录
			global.App.DB.Model(&models.InsightDASRecords{}).
				Where("request_id=? and username=?", RequestID, username).
				Updates(map[string]any{"error_msg": err.Error(), "is_finish": true})
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_query",
				ModuleName:     "SQL 查询",
				ActionCode:     "execute",
				ActionName:     "执行 SQL",
				ActionResult:   "fail",
				ResourceType:   "schema",
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        "执行 SQL 查询失败",
				ErrorMessage:   err.Error(),
				RequestPayload: form,
				SQLText:        form.SQLText,
			})
			response.Fail(c, err.Error())
		} else {
			// 更新数据库记录
			global.App.DB.Model(&models.InsightDASRecords{}).
				Where("request_id=? and username=?", RequestID, username).
				Updates(map[string]any{"is_finish": true})
			rows := 0
			if returnData.Data != nil {
				rows = len(*returnData.Data)
			}
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_query",
				ModuleName:     "SQL 查询",
				ActionCode:     "execute",
				ActionName:     "执行 SQL",
				ActionResult:   "success",
				ResourceType:   "schema",
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        "执行 SQL 查询，返回结果集",
				RequestPayload: map[string]any{"instance_id": form.InstanceID, "schema": form.Schema},
				AfterData:      map[string]any{"query_id": returnData.QueryID, "duration": returnData.Duration, "rows": rows},
				SQLText:        returnData.SQLText,
			})
			applyDataSecMasking(form.InstanceID, username, &returnData)
			response.Success(c, returnData, "success")
		}
	} else {
		// 有效性验证不通过，更新数据库记录
		global.App.DB.Model(&models.InsightDASRecords{}).
			Where("request_id=? and username=?", RequestID, username).
			Updates(map[string]any{"error_msg": err.Error(), "is_finish": true})
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:     username,
			AuditLevel:   "normal",
			ModuleCode:   "sql_query",
			ModuleName:   "SQL 查询",
			ActionCode:   "execute",
			ActionName:   "执行 SQL",
			ActionResult: "fail",
			Summary:      "执行 SQL 查询失败",
			ErrorMessage: err.Error(),
		})
		response.ValidateFail(c, err.Error())
	}
}

func ExecutePostgresQueryView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form *forms.ExecutePostgresQueryForm = &forms.ExecutePostgresQueryForm{}
	var RequestID string = requestid.Get(c)

	if err := c.ShouldBind(&form); err == nil {
		service := services.ExecutePostgresQueryService{
			ExecutePostgresQueryForm: form,
			C:                        c,
			Username:                 username,
		}
		returnData, err := service.Run()
		if err != nil {
			global.App.DB.Model(&models.InsightDASRecords{}).
				Where("request_id=? and username=?", RequestID, username).
				Updates(map[string]any{"error_msg": err.Error(), "is_finish": true})
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_query",
				ModuleName:     "SQL 查询",
				ActionCode:     "execute",
				ActionName:     "执行 SQL",
				ActionResult:   "fail",
				ResourceType:   "schema",
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        "执行 SQL 查询失败",
				ErrorMessage:   err.Error(),
				RequestPayload: form,
				SQLText:        form.SQLText,
			})
			response.Fail(c, err.Error())
		} else {
			global.App.DB.Model(&models.InsightDASRecords{}).
				Where("request_id=? and username=?", RequestID, username).
				Updates(map[string]any{"is_finish": true})
			rows := 0
			if returnData.Data != nil {
				rows = len(*returnData.Data)
			}
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_query",
				ModuleName:     "SQL 查询",
				ActionCode:     "execute",
				ActionName:     "执行 SQL",
				ActionResult:   "success",
				ResourceType:   "schema",
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        "执行 SQL 查询，返回结果集",
				RequestPayload: map[string]any{"instance_id": form.InstanceID, "schema": form.Schema},
				AfterData:      map[string]any{"query_id": returnData.QueryID, "duration": returnData.Duration, "rows": rows},
				SQLText:        returnData.SQLText,
			})
			applyDataSecMasking(form.InstanceID, username, &returnData)
			response.Success(c, returnData, "success")
		}
	} else {
		global.App.DB.Model(&models.InsightDASRecords{}).
			Where("request_id=? and username=?", RequestID, username).
			Updates(map[string]any{"error_msg": err.Error(), "is_finish": true})
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:     username,
			AuditLevel:   "normal",
			ModuleCode:   "sql_query",
			ModuleName:   "SQL 查询",
			ActionCode:   "execute",
			ActionName:   "执行 SQL",
			ActionResult: "fail",
			Summary:      "执行 SQL 查询失败",
			ErrorMessage: err.Error(),
		})
		response.ValidateFail(c, err.Error())
	}
}

func ExplainPostgresQueryView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form *forms.ExecutePostgresQueryForm = &forms.ExecutePostgresQueryForm{}
	var RequestID string = requestid.Get(c)

	if err := c.ShouldBind(&form); err == nil {
		service := services.ExplainPostgresQueryService{
			ExecutePostgresQueryForm: form,
			C:                        c,
			Username:                 username,
		}
		returnData, err := service.Run()
		if err != nil {
			global.App.DB.Model(&models.InsightDASRecords{}).
				Where("request_id=? and username=?", RequestID, username).
				Updates(map[string]any{"error_msg": err.Error(), "is_finish": true})
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_query",
				ModuleName:     "SQL 查询",
				ActionCode:     "explain",
				ActionName:     "解释 SQL",
				ActionResult:   "fail",
				ResourceType:   "schema",
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        "生成执行计划失败",
				ErrorMessage:   err.Error(),
				RequestPayload: form,
				SQLText:        form.SQLText,
			})
			response.Fail(c, err.Error())
		} else {
			global.App.DB.Model(&models.InsightDASRecords{}).
				Where("request_id=? and username=?", RequestID, username).
				Updates(map[string]any{"is_finish": true})
			rows := 0
			if returnData.Data != nil {
				rows = len(*returnData.Data)
			}
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_query",
				ModuleName:     "SQL 查询",
				ActionCode:     "explain",
				ActionName:     "解释 SQL",
				ActionResult:   "success",
				ResourceType:   "schema",
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        "生成 SQL 执行计划",
				RequestPayload: map[string]any{"instance_id": form.InstanceID, "schema": form.Schema},
				AfterData:      map[string]any{"duration": returnData.Duration, "rows": rows},
				SQLText:        returnData.SQLText,
			})
			response.Success(c, returnData, "success")
		}
	} else {
		global.App.DB.Model(&models.InsightDASRecords{}).
			Where("request_id=? and username=?", RequestID, username).
			Updates(map[string]any{"error_msg": err.Error(), "is_finish": true})
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:     username,
			AuditLevel:   "normal",
			ModuleCode:   "sql_query",
			ModuleName:   "SQL 查询",
			ActionCode:   "explain",
			ActionName:   "解释 SQL",
			ActionResult: "fail",
			Summary:      "生成执行计划失败",
			ErrorMessage: err.Error(),
		})
		response.ValidateFail(c, err.Error())
	}
}

func ExecuteSQLServerQueryView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form *forms.ExecuteSQLServerQueryForm = &forms.ExecuteSQLServerQueryForm{}
	var RequestID string = requestid.Get(c)

	if err := c.ShouldBind(&form); err == nil {
		service := services.ExecuteSQLServerQueryService{
			ExecuteSQLServerQueryForm: form,
			C:                         c,
			Username:                  username,
		}
		returnData, err := service.Run()
		if err != nil {
			global.App.DB.Model(&models.InsightDASRecords{}).
				Where("request_id=? and username=?", RequestID, username).
				Updates(map[string]any{"error_msg": err.Error(), "is_finish": true})
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_query",
				ModuleName:     "SQL 查询",
				ActionCode:     "execute",
				ActionName:     "执行 SQL",
				ActionResult:   "fail",
				ResourceType:   "schema",
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        "执行 SQL 查询失败",
				ErrorMessage:   err.Error(),
				RequestPayload: form,
				SQLText:        form.SQLText,
			})
			response.Fail(c, err.Error())
		} else {
			global.App.DB.Model(&models.InsightDASRecords{}).
				Where("request_id=? and username=?", RequestID, username).
				Updates(map[string]any{"is_finish": true})
			rows := 0
			if returnData.Data != nil {
				rows = len(*returnData.Data)
			}
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_query",
				ModuleName:     "SQL 查询",
				ActionCode:     "execute",
				ActionName:     "执行 SQL",
				ActionResult:   "success",
				ResourceType:   "schema",
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        "执行 SQL 查询，返回结果集",
				RequestPayload: map[string]any{"instance_id": form.InstanceID, "schema": form.Schema},
				AfterData:      map[string]any{"query_id": returnData.QueryID, "duration": returnData.Duration, "rows": rows},
				SQLText:        returnData.SQLText,
			})
			applyDataSecMasking(form.InstanceID, username, &returnData)
			response.Success(c, returnData, "success")
		}
	} else {
		global.App.DB.Model(&models.InsightDASRecords{}).
			Where("request_id=? and username=?", RequestID, username).
			Updates(map[string]any{"error_msg": err.Error(), "is_finish": true})
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:     username,
			AuditLevel:   "normal",
			ModuleCode:   "sql_query",
			ModuleName:   "SQL 查询",
			ActionCode:   "execute",
			ActionName:   "执行 SQL",
			ActionResult: "fail",
			Summary:      "执行 SQL 查询失败",
			ErrorMessage: err.Error(),
		})
		response.ValidateFail(c, err.Error())
	}
}

func ExecuteOracleQueryView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form *forms.ExecuteOracleQueryForm = &forms.ExecuteOracleQueryForm{}
	var RequestID string = requestid.Get(c)

	if err := c.ShouldBind(&form); err == nil {
		service := services.ExecuteOracleQueryService{
			ExecuteOracleQueryForm: form,
			C:                      c,
			Username:               username,
		}
		returnData, err := service.Run()
		if err != nil {
			global.App.DB.Model(&models.InsightDASRecords{}).
				Where("request_id=? and username=?", RequestID, username).
				Updates(map[string]any{"error_msg": err.Error(), "is_finish": true})
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_query",
				ModuleName:     "SQL 查询",
				ActionCode:     "execute",
				ActionName:     "执行 SQL",
				ActionResult:   "fail",
				ResourceType:   "schema",
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        "执行 SQL 查询失败",
				ErrorMessage:   err.Error(),
				RequestPayload: form,
				SQLText:        form.SQLText,
			})
			response.Fail(c, err.Error())
		} else {
			global.App.DB.Model(&models.InsightDASRecords{}).
				Where("request_id=? and username=?", RequestID, username).
				Updates(map[string]any{"is_finish": true})
			rows := 0
			if returnData.Data != nil {
				rows = len(*returnData.Data)
			}
			commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
				Username:       username,
				AuditLevel:     "normal",
				ModuleCode:     "sql_query",
				ModuleName:     "SQL 查询",
				ActionCode:     "execute",
				ActionName:     "执行 SQL",
				ActionResult:   "success",
				ResourceType:   "schema",
				ResourceID:     form.InstanceID,
				ResourceName:   form.Schema,
				Summary:        "执行 SQL 查询，返回结果集",
				RequestPayload: map[string]any{"instance_id": form.InstanceID, "schema": form.Schema},
				AfterData:      map[string]any{"query_id": returnData.QueryID, "duration": returnData.Duration, "rows": rows},
				SQLText:        returnData.SQLText,
			})
			applyDataSecMasking(form.InstanceID, username, &returnData)
			response.Success(c, returnData, "success")
		}
	} else {
		global.App.DB.Model(&models.InsightDASRecords{}).
			Where("request_id=? and username=?", RequestID, username).
			Updates(map[string]any{"error_msg": err.Error(), "is_finish": true})
		commonServices.TryWriteAuditLog(c, commonServices.AuditLogEvent{
			Username:     username,
			AuditLevel:   "normal",
			ModuleCode:   "sql_query",
			ModuleName:   "SQL 查询",
			ActionCode:   "execute",
			ActionName:   "执行 SQL",
			ActionResult: "fail",
			Summary:      "执行 SQL 查询失败",
			ErrorMessage: err.Error(),
		})
		response.ValidateFail(c, err.Error())
	}
}
