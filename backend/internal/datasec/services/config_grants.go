package services

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/datasec/forms"
	datasecModels "github.com/simajiong/jaysql/internal/datasec/models"
	"github.com/simajiong/jaysql/internal/global"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ---------- 脱敏规则 CRUD ----------

type ListRulesService struct {
	*forms.ListRulesForm
	C *gin.Context
}

func (s *ListRulesService) Run() (interface{}, int64, error) {
	var list []datasecModels.InsightDataSecRules
	tx := global.App.DB.Model(&datasecModels.InsightDataSecRules{})
	if keyword := strings.TrimSpace(s.Search); keyword != "" {
		tx = tx.Where("name LIKE ? OR remark LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if dataType := strings.TrimSpace(s.DataType); dataType != "" {
		tx = tx.Where("data_type=?", dataType)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := tx.Order("built_in DESC, id ASC").Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

type CreateRuleService struct {
	*forms.SaveRuleForm
	C *gin.Context
}

func (s *CreateRuleService) Run() (interface{}, error) {
	rule, err := buildRuleFromForm(s.SaveRuleForm)
	if err != nil {
		return nil, err
	}
	rule.CreatedBy = currentUsername(s.C)
	if err := global.App.DB.Create(rule).Error; err != nil {
		if strings.Contains(err.Error(), "uniq_datasec_rule_name") {
			return nil, fmt.Errorf("规则名称 %s 已存在", rule.Name)
		}
		return nil, err
	}
	InvalidateMaskConfig()
	return rule, nil
}

type UpdateRuleService struct {
	*forms.SaveRuleForm
	C *gin.Context
}

func (s *UpdateRuleService) Run() (interface{}, error) {
	var rule datasecModels.InsightDataSecRules
	if err := global.App.DB.First(&rule, s.ID).Error; err != nil {
		return nil, fmt.Errorf("规则不存在或已被删除")
	}
	updates, err := buildRuleFromForm(s.SaveRuleForm)
	if err != nil {
		return nil, err
	}
	rule.DataType = updates.DataType
	rule.Name = updates.Name
	rule.Algorithm = updates.Algorithm
	rule.Params = updates.Params
	rule.Enabled = updates.Enabled
	rule.Remark = updates.Remark
	if err := global.App.DB.Save(&rule).Error; err != nil {
		return nil, err
	}
	InvalidateMaskConfig()
	return &rule, nil
}

func buildRuleFromForm(s *forms.SaveRuleForm) (*datasecModels.InsightDataSecRules, error) {
	algorithm := strings.TrimSpace(s.Algorithm)
	switch algorithm {
	case MaskAlgoFull, MaskAlgoHeadTail, MaskAlgoTail, MaskAlgoHead,
		MaskAlgoEmail, MaskAlgoAddress, MaskAlgoName, MaskAlgoHash:
	default:
		return nil, fmt.Errorf("不支持的脱敏算法 %s", algorithm)
	}
	dataType := strings.TrimSpace(s.DataType)
	switch dataType {
	case DataTypePhone, DataTypeIDCard, DataTypeBankCard, DataTypeEmail,
		DataTypeAddress, DataTypeName, DataTypeCustom:
	default:
		return nil, fmt.Errorf("不支持的敏感类型 %s", dataType)
	}
	// 区间类算法必须带参数：缺省按该类型行业惯例兜底
	paramsJSON := datatypes.JSON("{}")
	if algorithm == MaskAlgoHeadTail || algorithm == MaskAlgoTail || algorithm == MaskAlgoHead || algorithm == MaskAlgoHash {
		p := parseMaskParams(datatypes.JSON(s.Params))
		if s.Params != "" {
			_ = json.Unmarshal([]byte(s.Params), &p)
		}
		if p.KeepHead == 0 && p.KeepTail == 0 && algorithm != MaskAlgoHash {
			switch dataType {
			case DataTypePhone:
				p.KeepHead, p.KeepTail = 3, 4
			case DataTypeIDCard:
				p.KeepHead, p.KeepTail = 4, 2
			case DataTypeBankCard:
				p.KeepTail = 4
			default:
				p.KeepHead = 1
			}
		}
		raw, _ := json.Marshal(p)
		paramsJSON = datatypes.JSON(raw)
	}
	enabled := 1
	if !s.Enabled {
		enabled = 0
	}
	return &datasecModels.InsightDataSecRules{
		DataType:  commonModels.EnumType(dataType),
		Name:      strings.TrimSpace(s.Name),
		Algorithm: commonModels.EnumType(algorithm),
		Params:    paramsJSON,
		Enabled:   enabled,
		Remark:    strings.TrimSpace(s.Remark),
	}, nil
}

type DeleteRuleService struct {
	*forms.IDForm
	C *gin.Context
}

func (s *DeleteRuleService) Run() error {
	var rule datasecModels.InsightDataSecRules
	if err := global.App.DB.First(&rule, s.ID).Error; err != nil {
		return fmt.Errorf("规则不存在或已被删除")
	}
	if rule.BuiltIn == 1 {
		return fmt.Errorf("内置规则不可删除，可改为停用")
	}
	var count int64
	global.App.DB.Model(&datasecModels.InsightDataSecColumns{}).Where("rule_id=?", s.ID).Count(&count)
	if count > 0 {
		return fmt.Errorf("该规则已被 %d 个列配置引用，请先解除绑定", count)
	}
	if err := global.App.DB.Delete(&rule).Error; err != nil {
		return err
	}
	InvalidateMaskConfig()
	return nil
}

// EnsureBuiltinRules 幂等写入 6 条内置规则（bootstrap 调用，不覆盖已有修改）。
func EnsureBuiltinRules(db *gorm.DB) {
	builtins := []datasecModels.InsightDataSecRules{
		{DataType: commonModels.EnumType(DataTypePhone), Name: RulePhone, Algorithm: commonModels.EnumType(MaskAlgoHeadTail), Params: datatypes.JSON(`{"keep_head":3,"keep_tail":4}`), Enabled: 1, BuiltIn: 1, Remark: "手机号行业标准脱敏"},
		{DataType: commonModels.EnumType(DataTypeIDCard), Name: RuleIDCard, Algorithm: commonModels.EnumType(MaskAlgoHeadTail), Params: datatypes.JSON(`{"keep_head":4,"keep_tail":2}`), Enabled: 1, BuiltIn: 1, Remark: "身份证号保留省市与校验位"},
		{DataType: commonModels.EnumType(DataTypeBankCard), Name: RuleBank, Algorithm: commonModels.EnumType(MaskAlgoTail), Params: datatypes.JSON(`{"keep_tail":4}`), Enabled: 1, BuiltIn: 1, Remark: "银行卡号保留后4位供核对"},
		{DataType: commonModels.EnumType(DataTypeEmail), Name: RuleEmail, Algorithm: commonModels.EnumType(MaskAlgoEmail), Enabled: 1, BuiltIn: 1, Remark: "邮箱用户名保留首字符"},
		{DataType: commonModels.EnumType(DataTypeAddress), Name: RuleAddress, Algorithm: commonModels.EnumType(MaskAlgoAddress), Enabled: 1, BuiltIn: 1, Remark: "地址保留省市区"},
		{DataType: commonModels.EnumType(DataTypeName), Name: RuleName, Algorithm: commonModels.EnumType(MaskAlgoName), Enabled: 1, BuiltIn: 1, Remark: "中文姓名保留首字"},
	}
	for i := range builtins {
		var count int64
		db.Model(&datasecModels.InsightDataSecRules{}).Where("name=?", builtins[i].Name).Count(&count)
		if count == 0 {
			db.Create(&builtins[i])
		}
	}
}

// ---------- 列配置 CRUD ----------

type ListColumnsService struct {
	*forms.ListColumnsForm
	C *gin.Context
}

func (s *ListColumnsService) Run() (interface{}, int64, error) {
	var list []datasecModels.InsightDataSecColumns
	tx := global.App.DB.Model(&datasecModels.InsightDataSecColumns{})
	if s.InstanceID != "" {
		tx = tx.Where("instance_id=?", strings.TrimSpace(s.InstanceID))
	}
	if s.SchemaName != "" {
		tx = tx.Where("schema_name=?", strings.TrimSpace(s.SchemaName))
	}
	if s.TableName != "" {
		tx = tx.Where("table_name=?", strings.TrimSpace(s.TableName))
	}
	if s.Status != "" {
		tx = tx.Where("status=?", strings.TrimSpace(s.Status))
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := tx.Order("instance_id, schema_name, table_name, column_name").Limit(500).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

type SaveColumnService struct {
	*forms.SaveColumnForm
	C *gin.Context
}

func (s *SaveColumnService) Run() (interface{}, error) {
	var rule datasecModels.InsightDataSecRules
	if err := global.App.DB.Where("enabled=1").First(&rule, s.RuleID).Error; err != nil {
		return nil, fmt.Errorf("规则不存在或已停用，请先在「脱敏规则」中启用")
	}
	var ruleCount int64
	global.App.DB.Model(&datasecModels.InsightDataSecRules{}).Where("id=? and enabled=1", s.RuleID).Count(&ruleCount)
	if ruleCount == 0 {
		return nil, fmt.Errorf("规则已停用，不能绑定新列")
	}
	col := datasecModels.InsightDataSecColumns{
		InstanceID: strings.TrimSpace(s.InstanceID),
		SchemaName: strings.TrimSpace(s.SchemaName),
		TblName:    strings.TrimSpace(s.TableName),
		ColumnName: strings.TrimSpace(s.ColumnName),
		RuleID:     s.RuleID,
		Status:     columnStatusEnabled,
		Source:     "manual",
		Confidence: 100,
		CreatedBy:  currentUsername(s.C),
	}
	if col.InstanceID == "" || col.ColumnName == "" {
		return nil, fmt.Errorf("实例与列名必填")
	}
	// 同列已存在（含扫描建议）则原地更新，不产生重复行
	var existing datasecModels.InsightDataSecColumns
	err := global.App.DB.Where("instance_id=? and schema_name=? and table_name=? and column_name=?",
		col.InstanceID, col.SchemaName, col.TableName, col.ColumnName).First(&existing).Error
	if err == nil {
		existing.RuleID = col.RuleID
		existing.Status = columnStatusEnabled
		existing.Source = "manual"
		existing.Confidence = 100
		if err := global.App.DB.Save(&existing).Error; err != nil {
			return nil, err
		}
		InvalidateMaskConfig()
		return &existing, nil
	}
	if err := global.App.DB.Create(&col).Error; err != nil {
		return nil, err
	}
	InvalidateMaskConfig()
	return &col, nil
}

type UpdateColumnStatusService struct {
	*forms.ColumnStatusForm
	C *gin.Context
}

// Run 启用 / 停用 / 采纳扫描建议 共用：按主键改状态。
func (s *UpdateColumnStatusService) Run() (interface{}, error) {
	var col datasecModels.InsightDataSecColumns
	if err := global.App.DB.First(&col, s.ID).Error; err != nil {
		return nil, fmt.Errorf("列配置不存在或已被删除")
	}
	status := strings.TrimSpace(s.Status)
	switch status {
	case columnStatusEnabled, columnStatusSuggested, "disabled":
	default:
		return nil, fmt.Errorf("非法状态 %s", status)
	}
	col.Status = status
	if err := global.App.DB.Save(&col).Error; err != nil {
		return nil, err
	}
	InvalidateMaskConfig()
	return &col, nil
}

type DeleteColumnService struct {
	*forms.IDForm
	C *gin.Context
}

func (s *DeleteColumnService) Run() error {
	var col datasecModels.InsightDataSecColumns
	if err := global.App.DB.First(&col, s.ID).Error; err != nil {
		return fmt.Errorf("列配置不存在或已被删除")
	}
	if err := global.App.DB.Delete(&col).Error; err != nil {
		return err
	}
	InvalidateMaskConfig()
	return nil
}

// ---------- 明文工单 ----------

type CreateGrantService struct {
	*forms.CreateGrantForm
	C *gin.Context
}

func (s *CreateGrantService) Run() (interface{}, error) {
	username := currentUsername(s.C)
	if err := NormalizeGrantScope(s.SchemaName, s.TableName, s.ColumnName); err != nil {
		return nil, err
	}
	if s.Hours <= 0 || s.Hours > maxGrantHours {
		return nil, fmt.Errorf("有效时长必须在 1-%d 小时之间", maxGrantHours)
	}
	// 同人同范围存在待审批工单时不重复创建
	var dup int64
	global.App.DB.Model(&datasecModels.InsightDataSecPlainGrants{}).
		Where("instance_id=? and username=? and schema_name=? and table_name=? and column_name=? and status=?",
			s.InstanceID, username, strings.TrimSpace(s.SchemaName), strings.TrimSpace(s.TableName), strings.TrimSpace(s.ColumnName), GrantStatusPending).
		Count(&dup)
	if dup > 0 {
		return nil, fmt.Errorf("已存在相同范围的待审批申请，请等待审批结果")
	}
	now := time.Now()
	expiresAt := commonModels.LocalTime(now.Add(time.Duration(s.Hours) * time.Hour))
	grant := datasecModels.InsightDataSecPlainGrants{
		GrantNo:    generateGrantNo(),
		InstanceID: strings.TrimSpace(s.InstanceID),
		SchemaName: strings.TrimSpace(s.SchemaName),
		TblName:    strings.TrimSpace(s.TableName),
		ColumnName: strings.TrimSpace(s.ColumnName),
		Username:   username,
		Reason:     strings.TrimSpace(s.Reason),
		Status:     GrantStatusPending,
		ExpiresAt:  &expiresAt,
	}
	if err := global.App.DB.Create(&grant).Error; err != nil {
		return nil, err
	}
	return &grant, nil
}

func generateGrantNo() string {
	return fmt.Sprintf("T-%s-%04d", time.Now().Format("20060102"), time.Now().UnixNano()%10000)
}

type ListGrantsService struct {
	*forms.ListGrantsForm
	C *gin.Context
}

func (s *ListGrantsService) Run() (interface{}, int64, error) {
	var list []datasecModels.InsightDataSecPlainGrants
	tx := global.App.DB.Model(&datasecModels.InsightDataSecPlainGrants{})
	// scope=all 需要审批权限（路由层已控），否则只看自己的
	if s.Scope != "all" {
		tx = tx.Where("username=?", currentUsername(s.C))
	}
	if s.Status != "" {
		tx = tx.Where("status=?", strings.TrimSpace(s.Status))
	}
	// 惰性过期：已批准但过了 expires_at 的记录刷成 expired（只影响展示口径）
	global.App.DB.Model(&datasecModels.InsightDataSecPlainGrants{}).
		Where("status=? and expires_at<=?", GrantStatusApproved, time.Now()).
		Update("status", GrantStatusExpired)
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := tx.Order("id DESC").Limit(200).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

type DecideGrantService struct {
	*forms.DecideGrantForm
	C *gin.Context
}

func (s *DecideGrantService) Run() (interface{}, error) {
	var grant datasecModels.InsightDataSecPlainGrants
	if err := global.App.DB.First(&grant, s.ID).Error; err != nil {
		return nil, fmt.Errorf("工单不存在或已被删除")
	}
	username := currentUsername(s.C)
	if grant.Username == username {
		return nil, fmt.Errorf("不能审批自己发起的明文申请")
	}
	now := commonModels.LocalTime(time.Now())
	switch s.Action {
	case "approve":
		if grant.Status != GrantStatusPending {
			return nil, fmt.Errorf("工单当前状态为 %s，不能审批", grant.Status)
		}
		grant.Status = GrantStatusApproved
		grant.Approver = username
		grant.ApproveRemark = strings.TrimSpace(s.Remark)
		grant.ApprovedAt = &now
	case "reject":
		if grant.Status != GrantStatusPending {
			return nil, fmt.Errorf("工单当前状态为 %s，不能驳回", grant.Status)
		}
		grant.Status = GrantStatusRejected
		grant.Approver = username
		grant.ApproveRemark = strings.TrimSpace(s.Remark)
	case "revoke":
		if grant.Status != GrantStatusApproved {
			return nil, fmt.Errorf("只有已批准的工单可以收回")
		}
		grant.Status = GrantStatusRevoked
		grant.Approver = username
		grant.ApproveRemark = strings.TrimSpace(s.Remark)
	default:
		return nil, fmt.Errorf("不支持的操作 %s", s.Action)
	}
	if err := global.App.DB.Save(&grant).Error; err != nil {
		return nil, err
	}
	return &grant, nil
}
