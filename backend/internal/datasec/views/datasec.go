package views

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/datasec/forms"
	"github.com/simajiong/jaysql/internal/datasec/services"
	"github.com/simajiong/jaysql/pkg/response"
)

// ---------- 脱敏规则 ----------

func ListRulesView(c *gin.Context) {
	form := &forms.ListRulesForm{}
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ListRulesService{ListRulesForm: form, C: c}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

func CreateRuleView(c *gin.Context) {
	form := &forms.SaveRuleForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CreateRuleService{SaveRuleForm: form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "创建成功")
}

func UpdateRuleView(c *gin.Context) {
	form := &forms.SaveRuleForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.UpdateRuleService{SaveRuleForm: form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "更新成功")
}

func DeleteRuleView(c *gin.Context) {
	form := &forms.IDForm{}
	if err := c.ShouldBindUri(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.DeleteRuleService{IDForm: form, C: c}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "删除成功")
}

// ---------- 列配置 ----------

func ListColumnsView(c *gin.Context) {
	form := &forms.ListColumnsForm{}
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ListColumnsService{ListColumnsForm: form, C: c}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

func SaveColumnView(c *gin.Context) {
	form := &forms.SaveColumnForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.SaveColumnService{SaveColumnForm: form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "保存成功")
}

func UpdateColumnStatusView(c *gin.Context) {
	form := &forms.ColumnStatusForm{}
	if err := c.ShouldBindUri(form); err != nil || form.Status == "" {
		if err2 := c.ShouldBindJSON(form); err2 != nil {
			response.ValidateFail(c, err.Error())
			return
		}
	}
	service := services.UpdateColumnStatusService{ColumnStatusForm: form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "状态已更新")
}

func DeleteColumnView(c *gin.Context) {
	form := &forms.IDForm{}
	if err := c.ShouldBindUri(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.DeleteColumnService{IDForm: form, C: c}
	if err := service.Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "删除成功")
}

// ---------- 库/表/列级联选项 ----------

func ListSchemaOptionsView(c *gin.Context) {
	form := &forms.SchemaOptionsForm{}
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ListSchemaOptionsService{SchemaOptionsForm: form}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func ListTableOptionsView(c *gin.Context) {
	form := &forms.TableOptionsForm{}
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ListTableOptionsService{TableOptionsForm: form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

func ListColumnOptionsView(c *gin.Context) {
	form := &forms.ColumnOptionsForm{}
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ListColumnOptionsService{ColumnOptionsForm: form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// ---------- 自动发现 ----------

func ScanTableView(c *gin.Context) {
	form := &forms.ScanTableForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ScanTableService{ScanTableForm: form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "扫描完成")
}

// ---------- 明文工单 ----------

func ListGrantsView(c *gin.Context) {
	form := &forms.ListGrantsForm{}
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.ListGrantsService{ListGrantsForm: form, C: c}
	data, total, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, data)
}

func CreateGrantView(c *gin.Context) {
	form := &forms.CreateGrantForm{}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.CreateGrantService{CreateGrantForm: form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "申请已提交，等待审批")
}

func DecideGrantView(c *gin.Context) {
	form := &forms.DecideGrantForm{}
	if err := c.ShouldBindUri(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	if err := c.ShouldBindJSON(form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	service := services.DecideGrantService{DecideGrantForm: form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "操作成功")
}
