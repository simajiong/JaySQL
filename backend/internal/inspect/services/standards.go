package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	jwt "github.com/appleboy/gin-jwt/v2"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/simajiong/jaysql/internal/inspect/forms"
	"github.com/simajiong/jaysql/internal/inspect/models"
	"github.com/simajiong/jaysql/pkg/pagination"
)

// AdminSQLStandardListService 规范列表
type AdminSQLStandardListService struct {
	*forms.AdminSQLStandardListForm
	C *gin.Context
}

func (s *AdminSQLStandardListService) Run() (responseData any, total int64, err error) {
	var standards []models.InsightSQLStandard
	tx := global.App.DB.Model(&models.InsightSQLStandard{})
	if s.Search != "" {
		tx = tx.Where("`name` like ? or `description` like ?", "%"+s.Search+"%", "%"+s.Search+"%")
	}
	total = pagination.Pager(&s.PaginationQ, tx.Order("sort_order asc, id asc"), &standards)

	// 统计每个规范关联的实例数
	standardIDs := make([]uint64, 0, len(standards))
	for _, st := range standards {
		standardIDs = append(standardIDs, st.ID)
	}
	var instanceCounts []struct {
		StandardID uint64
		Count      int64
	}
	if len(standardIDs) > 0 {
		global.App.DB.Model(&models.InsightInstanceSQLStandardBinding{}).
			Select("standard_id, count(*) as count").
			Where("standard_id in ?", standardIDs).
			Group("standard_id").
			Scan(&instanceCounts)
	}
	countMap := make(map[uint64]int64)
	for _, ic := range instanceCounts {
		countMap[ic.StandardID] = ic.Count
	}

	// 统计每条规范下的规则总数，以及可用于提交工单审核的有效规则数
	// 提交工单流程支持条件：规范已启用，且至少包含一条 level in ('must','suggest') 且 implemented=true 的规则
	type ruleAgg struct {
		StandardID        uint64
		Total             int64
		ActiveImplemented int64
	}
	var ruleAggs []ruleAgg
	if len(standardIDs) > 0 {
		err := global.App.DB.Model(&models.InsightSQLStandardRule{}).
			Select("standard_id, count(*) as total, sum(case when level in ('must','suggest') and implemented = 1 then 1 else 0 end) as active_implemented").
			Where("standard_id in ?", standardIDs).
			Group("standard_id").
			Scan(&ruleAggs).Error
		if err != nil {
			return nil, total, err
		}
	}
	ruleTotalMap := make(map[uint64]int64, len(ruleAggs))
	ruleActiveMap := make(map[uint64]int64, len(ruleAggs))
	for _, ra := range ruleAggs {
		ruleTotalMap[ra.StandardID] = ra.Total
		ruleActiveMap[ra.StandardID] = ra.ActiveImplemented
	}

	result := make([]map[string]any, 0, len(standards))
	for _, st := range standards {
		activeImplemented := ruleActiveMap[st.ID]
		result = append(result, map[string]any{
			"id":                       st.ID,
			"name":                     st.Name,
			"description":              st.Description,
			"is_default":               st.IsDefault,
			"env_type":                 st.EnvType,
			"enabled":                  st.Enabled,
			"sort_order":               st.SortOrder,
			"created_at":               st.CreatedAt,
			"created_by":               st.CreatedBy,
			"instance_count":           countMap[st.ID],
			"rule_count":               ruleTotalMap[st.ID],
			"active_implemented_count": activeImplemented,
			"supported":                st.Enabled && activeImplemented > 0,
		})
	}
	return result, total, nil
}

// AdminSQLStandardDetailService 规范详情+规则
type AdminSQLStandardDetailService struct {
	C  *gin.Context
	ID uint64
}

func (s *AdminSQLStandardDetailService) Run() (map[string]any, error) {
	var standard models.InsightSQLStandard
	if err := global.App.DB.First(&standard, s.ID).Error; err != nil {
		return nil, err
	}
	var rules []models.InsightSQLStandardRule
	if err := global.App.DB.Model(&models.InsightSQLStandardRule{}).
		Where("standard_id = ?", s.ID).
		Order("category asc, sort_order asc, id asc").
		Scan(&rules).Error; err != nil {
		return nil, err
	}
	return map[string]any{
		"id":          standard.ID,
		"name":        standard.Name,
		"description": standard.Description,
		"is_default":  standard.IsDefault,
		"env_type":    standard.EnvType,
		"enabled":     standard.Enabled,
		"sort_order":  standard.SortOrder,
		"rules":       rules,
	}, nil
}

// AdminSQLStandardByInstanceService 根据实例 ID 查询其绑定的 SQL 开发规范
// 用于工单详情页「查看规范」链接：实例切换时实时同步，无绑定则返回错误，前端据此隐藏链接。
type AdminSQLStandardByInstanceService struct {
	C          *gin.Context
	InstanceID string
}

func (s *AdminSQLStandardByInstanceService) Run() (map[string]any, error) {
	if strings.TrimSpace(s.InstanceID) == "" {
		return nil, fmt.Errorf("实例 ID 不能为空")
	}

	var binding models.InsightInstanceSQLStandardBinding
	if err := global.App.DB.Model(&models.InsightInstanceSQLStandardBinding{}).
		Where("instance_id = ?", s.InstanceID).
		Take(&binding).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("该实例未配置开发规范")
		}
		return nil, err
	}

	var standard models.InsightSQLStandard
	if err := global.App.DB.First(&standard, binding.StandardID).Error; err != nil {
		return nil, err
	}

	return map[string]any{
		"id":          standard.ID,
		"name":        standard.Name,
		"description": standard.Description,
		"is_default":  standard.IsDefault,
		"env_type":    standard.EnvType,
		"enabled":     standard.Enabled,
		"sort_order":  standard.SortOrder,
	}, nil
}

// AdminSQLStandardSaveService 保存规范基础信息
type AdminSQLStandardSaveService struct {
	*forms.AdminSQLStandardSaveForm
	C  *gin.Context
	ID uint64
}

func (s *AdminSQLStandardSaveService) Run() error {
	data := map[string]any{
		"name":        s.Name,
		"description": s.Description,
		"enabled":     s.Enabled,
	}
	if s.ID == 0 {
		// 新建自定义规范
		standard := models.InsightSQLStandard{
			Name:        s.Name,
			Description: s.Description,
			EnvType:     "custom",
			Enabled:     s.Enabled,
			CreatedBy:   currentUsername(s.C),
		}
		if err := global.App.DB.Create(&standard).Error; err != nil {
			return err
		}
		// 默认复制生产环境规范的所有规则作为模板
		return copyRulesFromDefault(standard.ID)
	}
	return global.App.DB.Model(&models.InsightSQLStandard{}).Where("id=?", s.ID).Updates(data).Error
}

// AdminSQLStandardRuleUpdateService 更新单条规则的处理级别/参数。
// 作用域严格限定在传入的 rule_id 这一条记录：
//   - 不联动修改同规范下的其它规则（包括同名/同能力项的跨数据库规则）
//   - 不回写 base_level（基础级别是规则目录的原始值，不允许被运行期编辑覆盖）
type AdminSQLStandardRuleUpdateService struct {
	*forms.AdminSQLStandardRuleUpdateForm
	C *gin.Context
}

func (s *AdminSQLStandardRuleUpdateService) Run() (map[string]any, error) {
	if s.RuleID == 0 {
		return nil, fmt.Errorf("规则ID不能为空")
	}
	// 先确认规则存在，避免静默更新 0 行导致前端误报成功
	var rule models.InsightSQLStandardRule
	if err := global.App.DB.First(&rule, s.RuleID).Error; err != nil {
		return nil, fmt.Errorf("规则不存在或已被删除")
	}
	params := s.Params
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	// 主键列类型规则：校验 pk_types 必须落在该数据库的可选值字典内，
	// 防止写入引擎无法识别的类型名（配置看似生效，实际所有主键都会误报不合规）
	if strings.HasSuffix(rule.RuleKey, "limit_pk_col_type") {
		if err := validatePrimaryKeyTypes(params); err != nil {
			return nil, err
		}
	}
	// 只更新 level / params / description 三个字段，避免误伤 base_level、rule_key、sort_order 等元数据
	description := strings.TrimSpace(s.Description)
	if len(description) > 512 {
		description = strings.TrimSpace(description[:512])
	}
	tx := global.App.DB.Model(&models.InsightSQLStandardRule{}).
		Where("id = ?", s.RuleID).
		Select("level", "params", "description").
		Updates(map[string]any{
			"level":       s.Level,
			"params":      params,
			"description": description,
		})
	if tx.Error != nil {
		return nil, tx.Error
	}
	if tx.RowsAffected == 0 {
		return nil, fmt.Errorf("规则级别未更新，请刷新后重试")
	}
	// 回读最新值，供前端核对（保证页面展示与数据库完全一致）
	var updated models.InsightSQLStandardRule
	if err := global.App.DB.First(&updated, s.RuleID).Error; err != nil {
		return nil, err
	}
	return map[string]any{
		"id":          updated.ID,
		"level":       updated.Level,
		"params":      updated.Params,
		"description": updated.Description,
	}, nil
}

// validatePrimaryKeyTypes 校验「限制主键列类型」规则参数 pk_types 的合法性。
// 空白名单合法（表示不限制类型）；非空时每一项都必须属于可选值字典。
// 字典为 MySQL + PostgreSQL + Oracle 三套类型的并集：同一能力项跨库合并为一行，
// 抽屉里选中的类型会同时写入所有勾选的库，因此不能按单库严格校验（否则正常保存会被误拦）。
func validatePrimaryKeyTypes(params json.RawMessage) error {
	if len(params) == 0 {
		return nil
	}
	var p struct {
		PKTypes []string `json:"pk_types"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return fmt.Errorf("规则参数格式错误：%v", err)
	}
	var invalid []string
	for _, t := range p.PKTypes {
		if strings.TrimSpace(t) == "" {
			continue
		}
		if !config.IsSupportedPrimaryKeyType(t) {
			invalid = append(invalid, t)
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("主键列类型 %s 不在可选范围内，可选值：%s",
			strings.Join(invalid, "、"), config.PrimaryKeySupportedTypesText())
	}
	return nil
}

// AdminSQLStandardDeleteService 删除规范（仅自定义）
type AdminSQLStandardDeleteService struct {
	C  *gin.Context
	ID uint64
}

func (s *AdminSQLStandardDeleteService) Run() error {
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? and is_default = ?", s.ID, false).Delete(&models.InsightSQLStandard{}).Error; err != nil {
			return err
		}
		if err := tx.Where("standard_id = ?", s.ID).Delete(&models.InsightSQLStandardRule{}).Error; err != nil {
			return err
		}
		if err := tx.Where("standard_id = ?", s.ID).Delete(&models.InsightInstanceSQLStandardBinding{}).Error; err != nil {
			return err
		}
		return nil
	})
}

// AdminSQLStandardInstanceListService 规范下关联的实例
type AdminSQLStandardInstanceListService struct {
	*forms.AdminSQLStandardInstanceListForm
	C *gin.Context
}

func (s *AdminSQLStandardInstanceListService) Run() (responseData any, total int64, err error) {
	var bindings []models.InsightInstanceSQLStandardBinding
	tx := global.App.DB.Model(&models.InsightInstanceSQLStandardBinding{}).Where("standard_id = ?", s.StandardID)
	total = pagination.Pager(&s.PaginationQ, tx.Order("id desc"), &bindings)

	instanceIDs := make([]string, 0, len(bindings))
	for _, b := range bindings {
		instanceIDs = append(instanceIDs, b.InstanceID)
	}
	type InstanceInfo struct {
		InstanceID string `json:"instance_id"`
		Hostname   string `json:"hostname"`
		Port       int    `json:"port"`
		DbType     string `json:"db_type"`
	}
	var instances []InstanceInfo
	if len(instanceIDs) > 0 {
		global.App.DB.Table("insight_instances").
			Select("instance_id, hostname, port, db_type").
			Where("instance_id in ?", instanceIDs).
			Scan(&instances)
	}
	instanceMap := make(map[string]InstanceInfo)
	for _, ins := range instances {
		instanceMap[ins.InstanceID] = ins
	}

	result := make([]map[string]any, 0, len(bindings))
	for _, b := range bindings {
		info := instanceMap[b.InstanceID]
		result = append(result, map[string]any{
			"id":          b.ID,
			"instance_id": b.InstanceID,
			"hostname":    info.Hostname,
			"port":        info.Port,
			"db_type":     info.DbType,
			"created_at":  b.CreatedAt,
		})
	}
	return result, total, nil
}

// AdminSQLStandardBindInstanceService 绑定实例到规范
type AdminSQLStandardBindInstanceService struct {
	*forms.AdminSQLStandardBindInstanceForm
	C *gin.Context
}

func (s *AdminSQLStandardBindInstanceService) Run() error {
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		// 每个实例只能绑定一个规范：先删除该实例的其它绑定
		if err := tx.Where("instance_id = ?", s.InstanceID).Delete(&models.InsightInstanceSQLStandardBinding{}).Error; err != nil {
			return err
		}
		return tx.Create(&models.InsightInstanceSQLStandardBinding{
			InstanceID: s.InstanceID,
			StandardID: s.StandardID,
		}).Error
	})
}

// AdminSQLStandardUnbindInstanceService 解除绑定
type AdminSQLStandardUnbindInstanceService struct {
	C  *gin.Context
	ID uint64
}

func (s *AdminSQLStandardUnbindInstanceService) Run() error {
	return global.App.DB.Delete(&models.InsightInstanceSQLStandardBinding{}, s.ID).Error
}

// AdminSQLStandardAvailableInstancesService 可被绑定到该规范的实例
type AdminSQLStandardAvailableInstancesService struct {
	*forms.AdminSQLStandardAvailableInstancesForm
	C *gin.Context
}

func (s *AdminSQLStandardAvailableInstancesService) Run() (responseData any, total int64, err error) {
	// 排除已经绑定到其它规范的实例
	var boundInstanceIDs []string
	global.App.DB.Model(&models.InsightInstanceSQLStandardBinding{}).
		Where("standard_id != ?", s.StandardID).
		Pluck("instance_id", &boundInstanceIDs)

	type InstanceInfo struct {
		InstanceID string `json:"instance_id"`
		Hostname   string `json:"hostname"`
		Port       int    `json:"port"`
		DbType     string `json:"db_type"`
	}
	var instances []InstanceInfo
	tx := global.App.DB.Table("insight_instances").
		Select("instance_id, hostname, port, db_type").
		// 可绑定规范的实例类型 = 工单审核引擎支持范围（MySQL/TiDB/Doris 共用 MySQL 引擎，
		// Kingbase 共用 PG 引擎）。漏掉某一类会让该类型的实例在「绑定实例」里根本选不到，
		// 表现为「规范建好了但绑不上」——金仓此前就是这一种情况。
		Where("db_type in ?", []string{"MySQL", "PostgreSQL", "Kingbase", "Oracle", "TiDB", "Doris"})
	if len(boundInstanceIDs) > 0 {
		tx = tx.Where("instance_id not in ?", boundInstanceIDs)
	}
	if s.Search != "" {
		tx = tx.Where("hostname like ? or instance_id like ?", "%"+s.Search+"%", "%"+s.Search+"%")
	}
	total = pagination.Pager(&s.PaginationQ, tx.Order("hostname asc"), &instances)
	return instances, total, nil
}

// copyRulesFromDefault 新建规范时从默认生产规范复制规则作为初始模板
func copyRulesFromDefault(standardID uint64) error {
	var prod models.InsightSQLStandard
	if err := global.App.DB.Where("env_type = ?", "prod").First(&prod).Error; err != nil {
		// 如果生产环境规范不存在，复制开发规范
		if err := global.App.DB.Where("env_type = ?", "dev").First(&prod).Error; err != nil {
			return nil
		}
	}
	var rules []models.InsightSQLStandardRule
	if err := global.App.DB.Where("standard_id = ?", prod.ID).Find(&rules).Error; err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}
	newRules := make([]models.InsightSQLStandardRule, 0, len(rules))
	for _, r := range rules {
		baseLevel := r.BaseLevel
		if baseLevel == "" {
			baseLevel = r.Level
		}
		newRules = append(newRules, models.InsightSQLStandardRule{
			StandardID: standardID,
			RuleKey:    r.RuleKey,
			Category:   r.Category,
			// 规则类型必须随模板一起复制：漏掉它会让新规范的「规则类型」列整列为空
			RuleType:    NormalizeRuleType(r.RuleKey, r.Name, r.RuleType),
			DbType:      r.DbType,
			Name:        r.Name,
			Description: r.Description,
			BaseLevel:   baseLevel,
			Level:       r.Level,
			Params:      r.Params,
			SortOrder:   r.SortOrder,
			Implemented: r.Implemented,
		})
	}
	return global.App.DB.CreateInBatches(&newRules, 100).Error
}

// InitializeDefaultSQLStandards 初始化默认规范和规则（首次运行或规范缺失时）
func InitializeDefaultSQLStandards(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		standards := []models.InsightSQLStandard{
			{Name: "默认开发环境规范", Description: "默认开发环境 SQL 开发规范，相对宽松", IsDefault: true, EnvType: "dev", Enabled: true, SortOrder: 1},
			{Name: "默认生产环境规范", Description: "默认生产环境 SQL 开发规范，更加严格", IsDefault: true, EnvType: "prod", Enabled: true, SortOrder: 2},
		}

		var devStandard, prodStandard models.InsightSQLStandard
		for _, st := range standards {
			var existing models.InsightSQLStandard
			err := tx.Where("env_type = ?", st.EnvType).First(&existing).Error
			if err == gorm.ErrRecordNotFound {
				if err := tx.Create(&st).Error; err != nil {
					return err
				}
				existing = st
			} else if err != nil {
				return err
			}
			if st.EnvType == "dev" {
				devStandard = existing
			} else {
				prodStandard = existing
			}
		}

		// 如果任一默认规范还没有规则，则写入全部规则
		for _, st := range []models.InsightSQLStandard{devStandard, prodStandard} {
			if st.ID == 0 {
				continue
			}
			var count int64
			tx.Model(&models.InsightSQLStandardRule{}).Where("standard_id = ?", st.ID).Count(&count)
			if count > 0 {
				continue
			}
			rules := defaultStandardRules()
			newRules := make([]models.InsightSQLStandardRule, 0, len(rules))
			for _, r := range rules {
				baseLevel := r.BaseLevel
				if baseLevel == "" {
					baseLevel = r.Level
				}
				level := seedLevelForEnv(baseLevel, st.EnvType)
				newRules = append(newRules, models.InsightSQLStandardRule{
					StandardID: st.ID,
					RuleKey:    r.RuleKey,
					Category:   r.Category,
					// 规则类型随种子一起落库：页面的「规则类型」列直接读这一列，不做事后推断
					RuleType:    NormalizeRuleType(r.RuleKey, r.Name, r.RuleType),
					DbType:      r.DbType,
					Name:        r.Name,
					Description: r.Description,
					BaseLevel:   baseLevel,
					Level:       level,
					Params:      r.Params,
					SortOrder:   r.SortOrder,
					Implemented: r.Implemented,
				})
			}
			if err := tx.CreateInBatches(&newRules, 100).Error; err != nil {
				return err
			}
		}
		// 存量规范补种金仓段：上面「有规则就跳过」的逻辑会让老环境永远拿不到
		// kingbase.* 条目（表现为规范页看不到金仓、金仓实例只能靠 postgres 段兜底）。
		// 这里做一次幂等补齐，只补「缺金仓段」的规范，不动任何既有条目的级别与参数。
		if err := ensureKingbaseRules(tx); err != nil {
			return err
		}
		// 通用目录对账：金仓镜像依赖「库内 PG 段完整」，而老环境的 PG 段本身可能缺
		// 后来新增的规则（如 postgres 的「影响行数」），SQLServer 等靠一次性补种脚本
		// 才进入老规范。这里按 rule_key 对目录做最终对账，兜住所有历史缺口。
		return ensureCatalogMissingRules(tx)
	})
}

// seedLevelForEnv 按规范环境推导种子条目的落地级别（与播种逻辑同口径）：
// 生产更严格（suggest→must），开发更宽松（must→suggest）。
func seedLevelForEnv(baseLevel, envType string) string {
	if envType == "prod" {
		if baseLevel == "suggest" {
			return "must"
		}
		return baseLevel
	}
	if baseLevel == "must" {
		return "suggest"
	}
	return baseLevel
}

// ensureCatalogMissingRules 为已存在的规范补齐规则目录中缺失的条目（幂等）。
//
// 背景：InitializeDefaultSQLStandards 只在规范「一条规则都没有」时全量播种，
// 此后目录新增的规则（新数据库类型、新能力项）永远不会进入老规范——表现为
// 老环境升级后规范页看不到新库/新规则。这里按 rule_key 对账：只补缺失行，
// 绝不更新已有行（级别/参数保住管理员的自定义调整）。规则行没有删除入口，
// 所以「管理员有意删掉某条」的场景不存在，重启补齐是安全的。
//
// 执行顺序上放在 ensureKingbaseRules 之后：金仓段优先以「库内 PG 段」为模板
// （继承管理员对 PG 的级别/参数调整），目录对账只兜两者都没覆盖到的缺口。
func ensureCatalogMissingRules(tx *gorm.DB) error {
	var standards []models.InsightSQLStandard
	if err := tx.Find(&standards).Error; err != nil {
		return err
	}
	catalog := defaultStandardRules()
	for _, st := range standards {
		var existingKeys []string
		if err := tx.Model(&models.InsightSQLStandardRule{}).
			Where("standard_id = ?", st.ID).
			Pluck("rule_key", &existingKeys).Error; err != nil {
			return err
		}
		if len(existingKeys) == 0 {
			continue // 空规范由上面的播种逻辑负责，避免两边同时写入
		}
		have := make(map[string]struct{}, len(existingKeys))
		for _, k := range existingKeys {
			have[k] = struct{}{}
		}
		missing := make([]models.InsightSQLStandardRule, 0, len(catalog))
		for _, r := range catalog {
			if _, ok := have[r.RuleKey]; ok {
				continue
			}
			baseLevel := r.BaseLevel
			if baseLevel == "" {
				baseLevel = r.Level
			}
			missing = append(missing, models.InsightSQLStandardRule{
				StandardID:  st.ID,
				RuleKey:     r.RuleKey,
				Category:    r.Category,
				RuleType:    NormalizeRuleType(r.RuleKey, r.Name, r.RuleType),
				DbType:      r.DbType,
				Name:        r.Name,
				Description: r.Description,
				BaseLevel:   baseLevel,
				Level:       seedLevelForEnv(baseLevel, st.EnvType),
				Params:      r.Params,
				SortOrder:   r.SortOrder,
				Implemented: r.Implemented,
			})
		}
		if len(missing) > 0 {
			if err := tx.CreateInBatches(&missing, 100).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// ensureKingbaseRules 为已有规范补齐金仓规则段（幂等）。
//
// 以该规范自己的 postgres 段为模板：级别、参数、排序完全跟随（金仓与 PG 同一套判定），
// 分类/类型/名称/描述逐字段复制。已经存在 kingbase.* 条目的规范直接跳过，
// 因此重复启动/多次执行不会产生重复规则，也不会覆盖管理员对金仓段的独立调整。
func ensureKingbaseRules(tx *gorm.DB) error {
	var standardIDs []uint64
	if err := tx.Model(&models.InsightSQLStandard{}).Pluck("id", &standardIDs).Error; err != nil {
		return err
	}
	for _, standardID := range standardIDs {
		var total, kingbaseCount int64
		tx.Model(&models.InsightSQLStandardRule{}).Where("standard_id = ?", standardID).Count(&total)
		if total == 0 {
			continue // 空规范交给上面的播种逻辑，避免两边同时写入
		}
		tx.Model(&models.InsightSQLStandardRule{}).
			Where("standard_id = ? AND db_type = ?", standardID, "kingbase").
			Count(&kingbaseCount)
		if kingbaseCount > 0 {
			continue
		}
		var pgRules []models.InsightSQLStandardRule
		if err := tx.Where("standard_id = ? AND db_type = ?", standardID, "postgres").Find(&pgRules).Error; err != nil {
			return err
		}
		if len(pgRules) == 0 {
			continue // 该规范的 PG 段被整体移除，无从镜像
		}
		cloned := kingbaseRulesFromPostgres(pgRules)
		rows := make([]models.InsightSQLStandardRule, 0, len(cloned))
		for _, r := range cloned {
			// 显式构造新记录：不能复用模板行的 Model（ID 非空会被当成更新，
			// 而且多个标准共用同一个指针会互相覆盖）。
			// Level / BaseLevel / Params 沿用模板：金仓段初始值与 PG 段保持一致，
			// 之后可在规范页独立调整（applyStandardRule 把 kingbase.* 映射到同一组引擎参数）。
			rows = append(rows, models.InsightSQLStandardRule{
				StandardID:  standardID,
				RuleKey:     r.RuleKey,
				Category:    r.Category,
				RuleType:    NormalizeRuleType(r.RuleKey, r.Name, r.RuleType),
				DbType:      "kingbase",
				Name:        r.Name,
				Description: r.Description,
				BaseLevel:   r.BaseLevel,
				Level:       r.Level,
				Params:      r.Params,
				SortOrder:   r.SortOrder,
				Implemented: r.Implemented,
			})
		}
		if err := tx.CreateInBatches(&rows, 100).Error; err != nil {
			return err
		}
	}
	return nil
}

// BuildInspectParamsForInstance 根据实例绑定的 SQL 开发规范生成 InspectParams。
//
// 审核参数的唯一来源是「实例绑定的规范」中的规则，判定维度只有四个：
//  1. 规范中是否存在该参数对应的规则（按 rule_key 映射，见 applyStandardRule）
//  2. 规则的级别是什么（must/suggest 视为开启，off 视为关闭）
//  3. 参数是否开启（level != off）
//  4. 参数是否属于当前数据源（rule.db_type == all 或 == 实例的 db_type）
//
// 不存在对应规则、规则被关闭或数据源不匹配时，参数保持引擎内置默认值
// （见 config.DefaultInspectParams：检查/限制类默认关闭，能力类沿用引擎默认行为）。
func BuildInspectParamsForInstance(instanceID string) (config.InspectParams, error) {
	// 基线：引擎内置默认值
	params := config.DefaultInspectParams()

	standardID, dbType, ok := resolveInstanceStandard(instanceID)
	if !ok {
		// 未绑定规范 / 规范未启用，全部参数保持引擎内置默认值
		return params, nil
	}

	rules := loadStandardRules(standardID, dbType)

	// 先应用关闭的规则（off），再应用开启的规则（must/suggest）。
	// 这样同一个引擎参数被多条规则映射时，“任一开启的规则”都能生效，
	// 而全部相关规则都关闭时参数回到关闭状态。
	for _, rule := range rules {
		if rule.Level == "off" {
			applyStandardRule(&params, rule)
		}
	}
	for _, rule := range rules {
		if rule.Level != "off" {
			applyStandardRule(&params, rule)
		}
	}
	return params, nil
}

// resolveInstanceStandard 返回实例绑定的「已启用」规范 ID 与实例的数据源类型。
// 返回 ok=false 表示未绑定规范或规范未启用，此时审核参数应全部使用引擎内置默认值。
func resolveInstanceStandard(instanceID string) (standardID uint64, dbType string, ok bool) {
	var binding models.InsightInstanceSQLStandardBinding
	if err := global.App.DB.Where("instance_id = ?", instanceID).First(&binding).Error; err != nil {
		return 0, "", false
	}

	var standard models.InsightSQLStandard
	if err := global.App.DB.First(&standard, binding.StandardID).Error; err != nil {
		return 0, "", false
	}
	if !standard.Enabled {
		return 0, "", false
	}

	var instance struct {
		DbType string `json:"db_type"`
	}
	global.App.DB.Table("insight_instances").Where("instance_id = ?", instanceID).Select("db_type").Scan(&instance)

	return binding.StandardID, normalizeRuleDbType(instance.DbType), true
}

// normalizeRuleDbType 把实例的数据源类型归一化为规范规则使用的类型标识。
// 实例表存的是 MySQL/PostgreSQL/TiDB/Doris/SQLServer，规范规则目录用的是
// mysql/postgres/oracle/tidb/sqlserver。
// 归一化属于「参数是否属于当前数据源」这一判定维度的一部分。
func normalizeRuleDbType(dbType string) string {
	switch strings.ToLower(strings.TrimSpace(dbType)) {
	case "postgresql", "pg":
		return "postgres"
	case "kingbase", "kingbasees", "kingbase es", "kingbase-es":
		// 金仓兼容 PG 方言：引擎复用 PG 解析链，规范目录单独成段（standard_seed_kingbase.go）
		// 以便在规范页里独立配置。存量库若还没补种 kingbase 规则，loadStandardRules 会
		// 自动回退到 postgres 段（见该函数注释），不会出现「绑了规范却零规则」。
		return "kingbase"
	case "tidb":
		// TiDB 有独立规范目录（db_type=tidb，逐条镜像 mysql 段），便于按数据库类型单独配置
		return "tidb"
	case "doris":
		// Doris 暂无独立目录，复用 mysql 规则（MySQL 协议 + MySQL 方言）
		return "mysql"
	case "sqlserver", "sql server", "mssql", "microsoft sql server":
		// 实例表里存的字面值是 `SQLServer`；页面/SQL 里也出现过 `SQL Server`、`MSSQL` 等写法，
		// 一并归一，否则这些实例会拿不到 SQLServer 段规则（表现为「规范配了但不生效」）。
		return "sqlserver"
	default:
		return strings.ToLower(strings.TrimSpace(dbType))
	}
}

// loadStandardRules 加载规范下的全部规则（四个维度中的「是否存在」与「是否属于当前数据源」）。
// 这里不做任何其它过滤：既不看 level（级别/开关在 applyStandardRule 中处理），
// 也不看 implemented（是否为规范目录内的有效规则与引擎能力无关，属历史遗留判断）。
func loadStandardRules(standardID uint64, dbType string) []models.InsightSQLStandardRule {
	var dbRules []models.InsightSQLStandardRule
	if err := global.App.DB.Where("standard_id = ?", standardID).Find(&dbRules).Error; err != nil {
		return nil
	}
	dbTypeLower := strings.ToLower(dbType)
	rules := filterRulesByDbType(dbRules, dbTypeLower)
	// 金仓兜底：规范里还没有 kingbase 段（存量库未补种、或规范是金仓上量之前创建的）时，
	// 退回 postgres 段。金仓与 PG 是同一套判定语义，回退只会「按 PG 规则审」，
	// 不会像零规则那样静默退化成引擎默认参数（那才是真正难查的「规范绑了不生效」）。
	if len(rules) == 0 && strings.EqualFold(dbTypeLower, "kingbase") {
		rules = filterRulesByDbType(dbRules, "postgres")
	}
	return rules
}

// filterRulesByDbType 按「all 通配 + 指定库」筛出规则，保持库内原顺序。
func filterRulesByDbType(all []models.InsightSQLStandardRule, dbTypeLower string) []models.InsightSQLStandardRule {
	rules := make([]models.InsightSQLStandardRule, 0, len(all))
	for _, rule := range all {
		if rule.DbType != "all" && rule.DbType != dbTypeLower {
			continue
		}
		rules = append(rules, rule)
	}
	return rules
}

// loadInstanceStandardRules 加载实例绑定规范中「属于当前数据源」的全部规则。
// 返回值：(规则列表, 是否成功取到已启用的规范)。取不到规范时 ok=false，调用方需按无规范处理。
func loadInstanceStandardRules(instanceID string) ([]models.InsightSQLStandardRule, bool) {
	standardID, dbType, ok := resolveInstanceStandard(instanceID)
	if !ok {
		return nil, false
	}
	return loadStandardRules(standardID, dbType), true
}

// StandardRuleLevel 规范中单条规则的级别信息，用于把审核消息精确关联回具体规则
type StandardRuleLevel struct {
	RuleKey     string `json:"rule_key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Level       string `json:"level"`
}

// GetInstanceStandardRuleLevels 返回实例绑定规范中「已开启」规则的级别明细。
// 规则级别是工单预审/提交时的唯一数据源：只要规范中开启了该规则，
// 引擎输出的对应消息就必须按此级别展示；关闭（off）的规则不参与级别判定。
// 返回 nil 表示未绑定规范 / 规范禁用，调用方不应据此推断级别。
func GetInstanceStandardRuleLevels(instanceID string) []StandardRuleLevel {
	// 复用一次性加载的快照：规则级别与规则版本同源，避免多次读库之间规则被改导致不自洽
	return LoadInstanceStandardSnapshot(instanceID).Rules
}

// GetInstanceStandardDefaultSeverity 返回规范中启用规则的默认（占优）级别。
// 用于审核消息无法精确匹配到具体规则时的兜底，该兜底仍来自规范定义：
//   - 取启用规则中数量占优的级别
//   - 数量相同时取 suggest（宽松优先，避免个别必须改进规则把全部问题升级为必须）
//   - 未绑定规范 / 无启用规则时返回空字符串，由调用方继续兜底
func GetInstanceStandardDefaultSeverity(instanceID string) string {
	// 同样走一次性加载的快照，保证与规则级别/版本取自同一份规则
	return LoadInstanceStandardSnapshot(instanceID).DefaultSeverity
}

// currentUsername 从 JWT claims 中取当前登录用户名
func currentUsername(c *gin.Context) string {
	if c == nil {
		return ""
	}
	claims := jwt.ExtractClaims(c)
	raw, ok := claims["id"]
	if !ok {
		return ""
	}
	username, ok := raw.(string)
	if !ok {
		return ""
	}
	return username
}

// 规则参数未配置阈值时使用的取值，保持与引擎历史默认行为一致
const (
	defaultMaxCharLength      = 64
	defaultMaxVarcharLength   = 1024
	defaultAutoIncInitValue   = 1
	defaultMaxIndexKeys       = 5
	defaultPrimaryKeyKeyParts = 2
	defaultSecondaryKeyParts  = 2
	defaultMaxAffectedRows    = 10000
	defaultMaxInsertRows      = 1000
	// defaultDMLJoinTables UPDATE/DELETE 多表关联数量的默认上限（规则未配置 max_join_tables 时）
	defaultDMLJoinTables = 5
	// defaultDMLLimitRows UPDATE/DELETE 语句 LIMIT 行数的默认上限（规则未配置 max_limit_rows 时）
	defaultDMLLimitRows = 5
	// defaultUpdateTimeColumns UPDATE 建议更新的「修改时间」列默认取值（规则未配置 update_time_columns 时）
	defaultUpdateTimeColumns = "update_time"
	// defaultAvoidUpdateColumns UPDATE 不建议更新的「创建时间」列默认取值
	//（规则 update_stmt_avoid_update_create_time_column 未配置 create_time_columns 时）
	defaultAvoidUpdateColumns = "create_time"
	// defaultMaxWhereInElements WHERE 条件中 IN 子句元素个数的默认上限（规则未配置 max_in_elements 时）
	defaultMaxWhereInElements = 1000
	defaultMaxTableColumns    = 50
	defaultUniqueIndexPrefix  = "UNIQ_"
	defaultSecondaryIdxPrefix = "IDX_"
	defaultUniqIndexRegex     = "uk_\\w+"
	// defaultNormalIdxRegex 普通索引名称正则的默认取值（规则 limit_normal_idx_name_fmt
	// 未配置 regex 或配置非法时回退）。与唯一索引同口径：正则优先、完整匹配。
	defaultNormalIdxRegex = "idx1_\\w+"
	// defaultTableCharsets 表字符集白名单的默认取值（规则 limit_tbl_charset 未配置 charsets 时）
	defaultTableCharsets = "utf8mb4,utf8"
	// defaultTableCollations 表排序规则白名单的默认取值（规则 limit_tbl_collation 未配置 collations 时）
	defaultTableCollations = "utf8mb4_general_ci,utf8_general_ci,utf8mb4_bin,utf8_bin"
	// defaultRequiredColumns 表必须包含列的默认取值（规则 tbl_需要include_某些col 未配置 columns 时）
	defaultRequiredColumns = "create_time,update_time"
)

// standardRuleParams 规范中为规则配置的参数（阈值/枚举值）
type standardRuleParams struct {
	MaxLength        int    `json:"max_length"`
	MaxCount         int    `json:"max_count"`
	MaxKeyParts      int    `json:"max_key_parts"`
	MaxAffectedRows  int    `json:"max_affected_rows"`
	MaxInsertRows    int    `json:"max_insert_rows"`
	MaxColumns       int    `json:"max_column_count"`
	AutoIncInitValue int    `json:"auto_inc_init_value"`
	NameCase         string `json:"name_case"`
	Prefix           string `json:"prefix"`
	// MaxJoinTables UPDATE/DELETE 语句多表关联数量的上限（规则 limit_update_delete_join_table_count 配置）。
	MaxJoinTables int `json:"max_join_tables"`
	// LimitMaxRows UPDATE/DELETE 语句 LIMIT 行数上限（规则 limit_update_delete_data_size 配置）。
	LimitMaxRows int `json:"max_limit_rows"`
	// MaxInElements WHERE 条件中 IN 子句元素个数上限（规则 limit_where_in_element_count 配置）。
	MaxInElements int `json:"max_in_elements"`
	// UpdateTimeColumns UPDATE 建议同步更新的「修改时间」列（规则 update_stmt_suggest_update_time_column 配置，
	// 逗号分隔多个候选列，如 update_time/modified_at）。
	UpdateTimeColumns string `json:"update_time_columns"`
	// AvoidUpdateColumns UPDATE 不建议更新的列（规则 update_stmt_avoid_update_create_time_column 配置，
	// 逗号分隔多个列，如 create_time/gmt_create）。
	AvoidUpdateColumns string `json:"create_time_columns"`
	// Regex 唯一索引名称必须匹配的正则表达式（规则 limit_uniq_idx_name_fmt 配置）。
	// 留空视为未修改，回退默认表达式 defaultUniqIndexRegex。
	Regex    string   `json:"regex"`
	Engines  []string `json:"engines"`
	Charsets []string `json:"charsets"`
	// Keywords 关键字检查的关键字列表（规则 col_name_no_是keyword / tbl_name_no_是keyword 配置）。
	// JSON 中缺省/null → nil（未配置，引擎使用默认全集）；[] → 空集（规则开启但不拦截任何关键字）。
	Keywords []string `json:"keywords"`
	// ForbiddenTypes 禁止使用的数据类型列表（规则 limit_col_no_use_partial_数据type 配置）。
	// JSON 中缺省/null → nil（未配置，规则开启后回退默认列表）；[] → 空集（规则开启但不限制任何类型）。
	ForbiddenTypes []string `json:"types"`
	// PKTypes 主键列类型白名单（规则 limit_pk_col_type 配置）。
	// JSON 中缺省/null → 引擎回退默认白名单（bigint、int）；配置后主键列类型必须在白名单内。
	PKTypes []string `json:"pk_types"`
	// Collations 表排序规则白名单（规则 limit_tbl_collation 配置）。
	// JSON 中缺省/null → nil（未配置，规则开启后回退默认白名单）；[] → 空集（规则开启但不限制任何排序规则）。
	Collations []string `json:"collations"`
	// Columns 表必须包含的列（规则 tbl_需要include_某些col 配置）。
	// JSON 中缺省/null → nil（未配置，规则开启后回退默认列）；[] → 空集（规则开启但不要求任何列）。
	Columns []string `json:"columns"`
}

func parseStandardRuleParams(raw json.RawMessage) standardRuleParams {
	var p standardRuleParams
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	return p
}

// firstPositive 规则配置了阈值则取规则值，否则取引擎默认阈值
func firstPositive(ruleValue, defaultValue int) int {
	if ruleValue > 0 {
		return ruleValue
	}
	return defaultValue
}

// normalizeRegex 规范配置了正则表达式则取规则值，否则取引擎默认表达式。
// 规则值为空或非法（无法编译）时一律回退默认值，避免引擎侧匹配时 panic。
func normalizeRegex(ruleValue, defaultValue string) string {
	v := strings.TrimSpace(ruleValue)
	if v == "" {
		return defaultValue
	}
	if _, err := regexp.Compile(v); err != nil {
		return defaultValue
	}
	return v
}

// charsetsToSupportCharset 把规则配置的字符集列表转换为引擎需要的结构
func charsetsToSupportCharset(charsets []string) []map[string]string {
	result := make([]map[string]string, 0, len(charsets))
	for _, c := range charsets {
		result = append(result, map[string]string{"charset": c, "recommend": ""})
	}
	return result
}

// applyStandardRule 把规范中的一条规则映射到审核引擎参数。
//
// 四个判定维度在这里落地的是「级别」与「是否开启」：level == off 表示关闭，
// must/suggest 表示开启；规则不存在或数据源不匹配时该规则压根不会被加载（见 loadStandardRules），
// 对应参数保持 config.DefaultInspectParams 的内置默认值。
//
// SQLServer / Kingbase 通过 standardRuleKeyAlias 复用 mysql.* / postgres.* 的参数映射分支：
// 72 条规则与 MySQL 段是「同一能力项、同一参数语义」，只是语法实现不同（引擎不同），
// 因此不需要为每条规则再加一份 `sqlserver.*` case —— 那会让 50 多个 case 行成倍膨胀，
// 且两边一旦改漏就会静默失效。数据库专属差异（如禁用类型默认清单）由 rule.DbType 分支补齐。
// standardRuleKeyAlias 把 `sqlserver.*` 规则键归一到 `mysql.*` 同一能力项的键上。
//
// 归一化让 SQL Server 段直接复用 applyStandardRule 里既有的 50 多个 case 分支：
// 两边是同一能力项、同一引擎参数语义，只有「引擎怎么判定」不同。好处是
// MySQL 段新增/调整一条规则的参数映射时 SQL Server 自动同步，不会因为漏改一处分行而静默失效。
//
// 归一化只做前缀替换，不改后缀：`sqlserver.struct.limit_tbl_name_case` →
// `mysql.struct.limit_tbl_name_case`。SQL Server 目录里刻意不收录 MySQL/TiDB 独占的
// 能力项（UNSIGNED / 存储引擎 / 字符集 / ZEROFILL / EVENT / CTL），因此不会出现
// 「归一后命中一个 SQL Server 上不适用的分支」。
func standardRuleKeyAlias(ruleKey string) string {
	if strings.HasPrefix(ruleKey, "sqlserver.") {
		return "mysql." + strings.TrimPrefix(ruleKey, "sqlserver.")
	}
	// 金仓段是 postgres 段的镜像（同一能力项、同一参数语义、同一套引擎），
	// 别名到 postgres.* 复用既有 60 多个 case 分支：PG 段新增/调整参数映射时
	// 金仓自动同步，不会因为漏写一条 kingbase.* case 而静默失效。
	if strings.HasPrefix(ruleKey, "kingbase.") {
		return "postgres." + strings.TrimPrefix(ruleKey, "kingbase.")
	}
	return ruleKey
}

// applySQLServerExclusiveRule 映射 SQLServer 段独有的能力类规则，返回是否已处理。
//
// 这三条规则在既有四个数据库的段里只有目录条目、引擎从不判定（页面能配级别但永不触发），
// 归一化到 mysql.* 会命中不存在的 case 而静默失效。SQL Server 引擎实现了它们，
// 因此在这里单独映射到独立参数，语义与其他「禁止…操作」规则保持一致：规则开启 => 禁止。
func applySQLServerExclusiveRule(params *config.InspectParams, ruleKey string, on bool) bool {
	switch ruleKey {
	case "sqlserver.struct.disable_proc_related_op":
		params.ENABLE_PROCEDURE_OBJECT = !on
	case "sqlserver.struct.disable_custom_function_related_op":
		params.ENABLE_FUNCTION_OBJECT = !on
	case "sqlserver.struct.disable_trigger_related_op":
		params.ENABLE_TRIGGER_OBJECT = !on
	default:
		return false
	}
	return true
}

func applyStandardRule(params *config.InspectParams, rule models.InsightSQLStandardRule) {
	on := rule.Level != "off" // 级别 + 是否开启
	rp := parseStandardRuleParams(rule.Params)

	// SQLServer 独有能力项先处理：这些规则在 MySQL/TiDB/PG/Oracle 段里只有目录、没有引擎判定，
	// 归一化到 mysql.* 会命中不存在的 case（静默失效），所以单独映射。
	if applySQLServerExclusiveRule(params, rule.RuleKey, on) {
		return
	}

	switch standardRuleKeyAlias(rule.RuleKey) {
	// ---------- 表 ----------
	case "mysql.struct.table_must_have_pk", "tidb.struct.table_must_have_pk", "postgres.struct.table_must_have_pk", "oracle.struct.table_must_have_pk":
		params.CHECK_TABLE_PRIMARY_KEY = on
	case "mysql.struct.limit_tbl_must_comment", "tidb.struct.limit_tbl_must_comment", "postgres.struct.limit_tbl_must_comment", "oracle.struct.limit_tbl_must_comment":
		params.CHECK_TABLE_COMMENT = on
	case "mysql.struct.limit_auto_inc_col_name_is_id", "tidb.struct.limit_auto_inc_col_name_is_id", "oracle.struct.limit_auto_inc_col_name_is_id":
		params.CHECK_AUTOINCREMENT_COL_NAME_IS_ID = on
	case "mysql.struct.limit_auto_inc_col_is_unsigned", "tidb.struct.limit_auto_inc_col_is_unsigned":
		params.CHECK_AUTOINCREMENT_COL_UNSIGNED = on
	case "mysql.struct.limit_create_table_auto_inc_init", "tidb.struct.limit_create_table_auto_inc_init", "oracle.struct.limit_create_table_auto_inc_init":
		params.CHECK_TABLE_AUTOINCREMENT_INIT_VALUE = on
		if on {
			params.TABLE_AUTOINCREMENT_INIT_VALUE = firstPositive(rp.AutoIncInitValue, defaultAutoIncInitValue)
			// 自增列字段名大小写：MySQL/PG 默认小写、可配置为大写（params.name_case）；
			// Oracle 未加引号标识符折叠为大写，默认大写、可配置为小写。
			switch {
			case strings.EqualFold(strings.TrimSpace(rp.NameCase), "upper"):
				params.COLUMN_NAME_CASE = "upper"
			case strings.EqualFold(strings.TrimSpace(rp.NameCase), "lower"):
				params.COLUMN_NAME_CASE = "lower"
			case strings.EqualFold(rule.DbType, "oracle"):
				params.COLUMN_NAME_CASE = "upper"
			default:
				params.COLUMN_NAME_CASE = "lower"
			}
		} else {
			params.TABLE_AUTOINCREMENT_INIT_VALUE = 0
		}
	case "mysql.struct.limit_table_column_count", "tidb.struct.limit_table_column_count", "postgres.struct.limit_table_column_count", "oracle.struct.limit_table_column_count":
		if on {
			params.MAX_TABLE_COLUMNS = firstPositive(rp.MaxColumns, defaultMaxTableColumns)
		} else {
			params.MAX_TABLE_COLUMNS = 0
		}
	case "mysql.struct.limit_table_storage_engine", "tidb.struct.limit_table_storage_engine":
		params.CHECK_TABLE_ENGINE = on
		if on && len(rp.Engines) > 0 {
			params.TABLE_SUPPORT_ENGINE = rp.Engines
		}
	case "mysql.struct.limit_tbl_charset", "tidb.struct.limit_tbl_charset":
		params.CHECK_TABLE_CHARSET = on
		if on {
			// 表字符集白名单：未配置时回退默认列表（utf8mb4、utf8）
			if len(rp.Charsets) > 0 {
				params.TABLE_SUPPORT_CHARSET = charsetsToSupportCharset(rp.Charsets)
			} else {
				params.TABLE_SUPPORT_CHARSET = charsetsToSupportCharset(strings.Split(defaultTableCharsets, ","))
			}
		} else {
			params.TABLE_SUPPORT_CHARSET = nil
		}
	case "mysql.struct.limit_tbl_collation", "tidb.struct.limit_tbl_collation":
		// 表排序规则白名单：规则开启后显式指定的 COLLATE 必须在白名单内。
		// 未配置白名单时回退默认列表（utf8mb4 通用排序规则）。
		params.CHECK_TABLE_COLLATION = on
		if on {
			if rp.Collations != nil {
				params.TABLE_SUPPORT_COLLATION = rp.Collations
			} else {
				params.TABLE_SUPPORT_COLLATION = strings.Split(defaultTableCollations, ",")
			}
		} else {
			params.TABLE_SUPPORT_COLLATION = nil
		}
	case "mysql.struct.table_must_include_required_columns", "tidb.struct.table_must_include_required_columns", "postgres.struct.table_must_include_required_columns",
		"oracle.struct.table_must_include_required_columns":
		// 表必须包含的列：规则开启后建表语句必须包含配置的列（未配置时回退默认列）。
		params.TABLE_REQUIRED_COLUMNS = nil
		if on {
			if rp.Columns != nil {
				params.TABLE_REQUIRED_COLUMNS = rp.Columns
			} else {
				params.TABLE_REQUIRED_COLUMNS = strings.Split(defaultRequiredColumns, ",")
			}
		}
	case "mysql.struct.limit_database_charset", "tidb.struct.limit_database_charset":
		params.CHECK_DATABASE_CHARSET = on
		if on {
			charsets := rp.Charsets
			if len(charsets) == 0 {
				// 规则开启但未配置字符集列表时，回退到默认列表
				charsets = []string{"utf8mb4", "utf8"}
			}
			params.DATABASE_SUPPORT_CHARSET = charsetsToSupportCharset(charsets)
		}
	case "mysql.struct.limit_tbl_name_case", "tidb.struct.limit_tbl_name_case", "postgres.struct.limit_tbl_name_case", "oracle.struct.limit_tbl_name_case":
		// 表名字符集校验开关独立于列名（历史共用 CHECK_IDENTIFIER，导致一条规则关闭后检查仍被执行）
		params.CHECK_TABLE_IDENTIFIER = on
		// 表名大小写要求：MySQL/PG 默认小写、可配置为大写（params.name_case）；
		// Oracle 未加引号标识符折叠为大写，默认大写、可配置为小写。
		// 仅表名检查消费该参数；与「限制字段名大小写」的 COLUMN_NAME_CASE 相互独立。
		if on {
			switch {
			case strings.EqualFold(strings.TrimSpace(rp.NameCase), "upper"):
				params.TABLE_NAME_CASE = "upper"
			case strings.EqualFold(strings.TrimSpace(rp.NameCase), "lower"):
				params.TABLE_NAME_CASE = "lower"
			case strings.EqualFold(rule.DbType, "oracle"):
				params.TABLE_NAME_CASE = "upper"
			default:
				params.TABLE_NAME_CASE = "lower"
			}
		}
	case "mysql.struct.limit_col_name_case", "tidb.struct.limit_col_name_case", "postgres.struct.limit_col_name_case", "oracle.struct.limit_col_name_case":
		// 列名字符集校验开关独立于表名：规则关闭时必须真正跳过列名检查
		params.CHECK_COLUMN_IDENTIFIER = on
		// 「限制字段名大小写」的大小写要求：MySQL/PG 默认小写、可配置为大写（params.name_case）；
		// Oracle 未加引号标识符折叠为大写，默认大写、可配置为小写。
		// 仅列名检查消费该参数（表名/索引名的约束范围不含此配置）。
		if on {
			switch {
			case strings.EqualFold(strings.TrimSpace(rp.NameCase), "upper"):
				params.COLUMN_NAME_CASE = "upper"
			case strings.EqualFold(strings.TrimSpace(rp.NameCase), "lower"):
				params.COLUMN_NAME_CASE = "lower"
			case strings.EqualFold(rule.DbType, "oracle"):
				params.COLUMN_NAME_CASE = "upper"
			default:
				params.COLUMN_NAME_CASE = "lower"
			}
		} else {
			params.COLUMN_NAME_CASE = "lower"
		}
	case "mysql.struct.tbl_name_no_keyword", "tidb.struct.tbl_name_no_keyword", "postgres.struct.tbl_name_no_keyword", "oracle.struct.tbl_name_no_keyword",
		"mysql.struct.col_name_no_keyword", "tidb.struct.col_name_no_keyword", "postgres.struct.col_name_no_keyword", "oracle.struct.col_name_no_keyword":
		params.CHECK_IDENTIFER_KEYWORD = on
		// 两条规则（表名/字段名）共用 IDENTIFIER_KEYWORDS 参数：
		//   - 开启且配置了 keywords：使用配置值（后应用的规则若未配置，不得清掉先应用规则已写入的列表）
		//   - 开启但未配置：不覆盖现有值，nil 时引擎回退默认全集
		//   - 关闭：清空（回退默认全集，等价于"不按自定义列表拦截"）
		if on && rp.Keywords != nil {
			params.IDENTIFIER_KEYWORDS = rp.Keywords
		} else if !on {
			params.IDENTIFIER_KEYWORDS = nil
		}

	// ---------- 列 ----------
	case "mysql.struct.limit_col_must_have_comment", "tidb.struct.limit_col_must_have_comment", "postgres.struct.limit_col_must_have_comment", "oracle.struct.limit_col_must_have_comment":
		params.CHECK_COLUMN_COMMENT = on
	case "mysql.struct.limit_col_must_have_default", "tidb.struct.limit_col_must_have_default", "postgres.struct.limit_col_must_have_default", "oracle.struct.limit_col_must_have_default":
		params.CHECK_COLUMN_DEFAULT_VALUE = on
	case "mysql.struct.limit_col_not_null", "tidb.struct.limit_col_not_null", "postgres.struct.limit_col_not_null", "oracle.struct.limit_col_not_null":
		// 规则含义：限制列不可空 => 启用 NOT NULL 检查
		params.ENABLE_COLUMN_NOT_NULL = on
	case "mysql.struct.no_column_charset", "tidb.struct.no_column_charset":
		params.CHECK_COLUMN_CHARSET = on
	case "mysql.struct.no_column_collate", "tidb.struct.no_column_collate":
		params.CHECK_COLUMN_COLLATION = on
	case "mysql.struct.column_zerofill_forbidden", "tidb.struct.column_zerofill_forbidden":
		params.DISABLE_COLUMN_ZEROFILL = on
	case "mysql.struct.limit_char_type_col_len", "tidb.struct.limit_char_type_col_len", "postgres.struct.limit_char_type_col_len", "oracle.struct.limit_char_type_col_len":
		if on {
			params.COLUMN_MAX_CHAR_LENGTH = firstPositive(rp.MaxLength, defaultMaxCharLength)
		} else {
			params.COLUMN_MAX_CHAR_LENGTH = 0
		}
	case "mysql.struct.limit_varchar_type_col_len", "tidb.struct.limit_varchar_type_col_len", "postgres.struct.limit_varchar_type_col_len", "oracle.struct.limit_varchar_type_col_len":
		if on {
			params.MAX_VARCHAR_LENGTH = firstPositive(rp.MaxLength, defaultMaxVarcharLength)
		} else {
			params.MAX_VARCHAR_LENGTH = 0
		}
	case "mysql.struct.limit_col_no_partial_data_type", "tidb.struct.limit_col_no_partial_data_type", "postgres.struct.limit_col_no_partial_data_type", "oracle.struct.limit_col_no_partial_data_type":
		params.CHECK_COLUMN_FORBIDDEN_TYPES = on
		if on {
			params.COLUMN_FORBIDDEN_TYPES = rp.ForbiddenTypes
			if params.COLUMN_FORBIDDEN_TYPES == nil {
				// 规则开启但未配置类型列表时，回退到默认列表：
				// Oracle 无 enum/bit/set，回退其淘汰类型；SQL Server 回退已废弃/易误用类型。
				switch {
				case strings.EqualFold(rule.DbType, "oracle"):
					params.COLUMN_FORBIDDEN_TYPES = []string{"long", "long raw"}
				case strings.EqualFold(rule.DbType, "sqlserver"):
					params.COLUMN_FORBIDDEN_TYPES = []string{"text", "ntext", "image", "sql_variant", "timestamp", "money", "smallmoney"}
				default:
					params.COLUMN_FORBIDDEN_TYPES = []string{"double", "float", "enum", "bit", "set"}
				}
			}
		} else {
			params.COLUMN_FORBIDDEN_TYPES = nil
		}

	// ---------- 主键 / 索引 ----------
	case "mysql.struct.limit_pk_col_type", "tidb.struct.limit_pk_col_type", "postgres.struct.limit_pk_col_type", "oracle.struct.limit_pk_col_type":
		params.CHECK_PRIMARYKEY_USE_BIGINT = on
		if on {
			// 主键列类型白名单：完全按用户配置；未配置(空) => 不限制类型
			params.PRIMARYKEY_SUPPORT_TYPES = rp.PKTypes
		} else {
			params.PRIMARYKEY_SUPPORT_TYPES = nil
		}
	case "mysql.struct.limit_pk_col_must_auto_inc", "tidb.struct.limit_pk_col_must_auto_inc", "oracle.struct.limit_pk_col_must_auto_inc":
		params.CHECK_PRIMARYKEY_USE_AUTO_INCREMENT = on
	case "mysql.struct.limit_pk_column_count", "tidb.struct.limit_pk_column_count", "postgres.struct.limit_pk_column_count", "oracle.struct.limit_pk_column_count":
		if on {
			params.PRIMARYKEY_MAX_KEY_PARTS = firstPositive(rp.MaxKeyParts, defaultPrimaryKeyKeyParts)
		} else {
			params.PRIMARYKEY_MAX_KEY_PARTS = 0
		}
	case "mysql.struct.limit_single_index_column_count", "tidb.struct.limit_single_index_column_count", "postgres.struct.limit_single_index_column_count", "oracle.struct.limit_single_index_column_count":
		if on {
			params.SECONDARY_INDEX_MAX_KEY_PARTS = firstPositive(rp.MaxKeyParts, defaultSecondaryKeyParts)
		} else {
			params.SECONDARY_INDEX_MAX_KEY_PARTS = 0
		}
	case "mysql.struct.limit_tbl_idx_count", "tidb.struct.limit_tbl_idx_count", "postgres.struct.limit_tbl_idx_count", "oracle.struct.limit_tbl_idx_count":
		if on {
			params.MAX_INDEX_KEYS = firstPositive(rp.MaxCount, defaultMaxIndexKeys)
		} else {
			params.MAX_INDEX_KEYS = 0
		}
	case "mysql.struct.limit_tbl_redundant_idx", "tidb.struct.limit_tbl_redundant_idx", "postgres.struct.limit_tbl_redundant_idx", "oracle.struct.limit_tbl_redundant_idx":
		// 限制冗余索引：规则开启 => 不允许冗余（ENABLE_REDUNDANT_INDEX=false，引擎执行检查）；
		// 规则关闭/未配置 => true（允许冗余，跳过检查）。该参数默认 true，详见 defaults.go。
		params.ENABLE_REDUNDANT_INDEX = !on
	case "mysql.struct.limit_uniq_idx_name_fmt", "tidb.struct.limit_uniq_idx_name_fmt", "postgres.struct.limit_uniq_idx_name_fmt", "oracle.struct.limit_uniq_idx_name_fmt":
		params.CHECK_UNIQ_INDEX_PREFIX = on
		// 索引名基础字符集校验跟随「索引名格式」规则（历史由「表名/列名大小写」规则误开）
		params.CHECK_INDEX_IDENTIFIER = on
		if on {
			params.UNQI_INDEX_PREFIX = rp.Prefix
			if params.UNQI_INDEX_PREFIX == "" {
				params.UNQI_INDEX_PREFIX = defaultUniqueIndexPrefix
			}
			// 唯一索引名称正则表达式：留空视为未修改，回退默认表达式；
			// 非法表达式同样回退默认值，避免引擎侧编译 panic。
			params.UNIQ_INDEX_REGEX = normalizeRegex(rp.Regex, defaultUniqIndexRegex)
		} else {
			params.UNIQ_INDEX_REGEX = ""
		}
	case "mysql.struct.limit_normal_idx_name_fmt", "tidb.struct.limit_normal_idx_name_fmt", "postgres.struct.limit_normal_idx_name_fmt", "oracle.struct.limit_normal_idx_name_fmt":
		params.CHECK_SECONDARY_INDEX_PREFIX = on
		params.CHECK_INDEX_IDENTIFIER = on
		if on {
			params.SECONDARY_INDEX_PREFIX = rp.Prefix
			if params.SECONDARY_INDEX_PREFIX == "" {
				params.SECONDARY_INDEX_PREFIX = defaultSecondaryIdxPrefix
			}
			// 普通索引名称正则表达式：与唯一索引同口径——配了正则以正则为准（完整匹配），
			// 未配（为空）或配置非法时回退默认表达式 defaultNormalIdxRegex（idx1_\w+），
			// 不再回退前缀检查。两套引擎共用 utils.MatchIndexName。
			params.SECONDARY_INDEX_REGEX = normalizeRegex(rp.Regex, defaultNormalIdxRegex)
		} else {
			params.SECONDARY_INDEX_REGEX = ""
		}
	case "mysql.struct.index_must_have_name", "tidb.struct.index_must_have_name", "postgres.struct.index_must_have_name", "oracle.struct.index_must_have_name":
		// 索引必须显式命名：引擎侧通过前缀检查实现（未命名时前缀检查会报错）；
		// CHECK_INDEX_NAME_REQUIRED 供 PG 引擎显式拦截未命名索引（与规则开关联动）。
		params.CHECK_UNIQ_INDEX_PREFIX = on
		params.CHECK_SECONDARY_INDEX_PREFIX = on
		params.CHECK_INDEX_NAME_REQUIRED = on
		params.CHECK_INDEX_IDENTIFIER = on

	// ---------- 表约束 / 数据库对象 ----------
	case "mysql.struct.limit_tbl_no_use_fk", "tidb.struct.limit_tbl_no_use_fk", "postgres.struct.limit_tbl_no_use_fk", "oracle.struct.limit_tbl_no_use_fk":
		// 规则开启 => 禁止外键
		params.ENABLE_FOREIGN_KEY = !on
	case "mysql.struct.merge_multiple_alter_stmt_same_table", "tidb.struct.merge_multiple_alter_stmt_same_table",
		"postgres.struct.merge_multiple_alter_stmt_same_table",
		"oracle.struct.merge_multiple_alter_stmt_same_table":
		// 同一张表的多条 ALTER 建议合并为一条：规则开关直接控制引擎的合并检查（两套引擎共用该参数）。
		// 注意引擎默认值为 true（保持历史行为），规则关闭（off）时显式关闭检查。
		params.ENABLE_MERGE_ALTER_TABLE = on
	case "mysql.struct.disable_view_related_op", "tidb.struct.disable_view_related_op", "postgres.struct.disable_view_related_op", "oracle.struct.disable_view_related_op":
		// 规则开启 => 禁止创建/使用视图
		params.ENABLE_CREATE_VIEW = !on
	case "mysql.struct.disable_create_table_as", "tidb.struct.disable_create_table_as", "postgres.struct.disable_create_table_as", "oracle.struct.disable_create_table_as":
		// 规则开启 => 禁止 CTAS。判定在语句准入闸门（MySQL: checker.statementAdmissionGate；
		// PG: checker.pg_gate.go gateCreateTable）。
		// 与视图规则的关键差异：ENABLE_CREATE_TABLE_AS 是**能力类**参数，引擎默认值为 false（禁止），
		// 因此「规范里没有这条规则」的存量环境仍然默认禁止（安全侧）——必须靠补种脚本
		// 把规则补进规范，用户才能在页面上把它关掉（见 backend/sql/2026xxxx_seed_*.sql）。
		params.ENABLE_CREATE_TABLE_AS = !on
	case "mysql.struct.disable_create_table_like", "tidb.struct.disable_create_table_like", "postgres.struct.disable_create_table_like", "oracle.struct.disable_create_table_like":
		// 规则开启 => 禁止 CTL，同 disable_create_table_as 的能力类默认值口径。
		params.ENABLE_CREATE_TABLE_LIKE = !on

	// ---------- DML ----------
	case "mysql.query_update.update_delete_stmt_suggest_where", "tidb.query_update.update_delete_stmt_suggest_where",
		"postgres.query_update.update_delete_stmt_suggest_where",
		"oracle.query_update.update_delete_stmt_suggest_where":
		params.DML_MUST_HAVE_WHERE = on
	case "mysql.query_update.update_delete_stmt_must_use_idx", "tidb.query_update.update_delete_stmt_must_use_idx":
		// UPDATE/DELETE 必须使用索引：开启后由 EXPLAIN 输出判定。
		// 该规则目录里只有 mysql 版本（PostgreSQL 无对应项），且 TiDB 的 EXPLAIN
		// 没有 type/key 列，判定会在 process.Explain.GetIndexUsage 内自动跳过。
		params.CHECK_DML_MUST_USE_INDEX = on
	case "mysql.query_update.update_delete_stmt_no_order_by_clause", "tidb.query_update.update_delete_stmt_no_order_by_clause":
		params.DML_DISABLE_ORDERBY = on
	case "mysql.query_update.update_delete_stmt_where_subquery_check", "tidb.query_update.update_delete_stmt_where_subquery_check",
		"postgres.query_update.update_delete_stmt_where_subquery_check":
		params.DML_DISABLE_SUBQUERY = on
	case "mysql.query_update.limit_update_delete_data_size", "tidb.query_update.limit_update_delete_data_size",
		"postgres.query_update.limit_update_delete_data_size",
		"oracle.query_update.limit_update_delete_data_size":
		// 语义（2026-09-29 调整）：由「禁止任何 LIMIT」改为「LIMIT 行数超过阈值才告警」。
		// 规则开启 => DML_MAX_LIMIT_ROWS = 配置值（默认 5）；规则关闭 => 0（不限制）。
		// DML_DISABLE_LIMIT 不再由该规则设置（历史参数保留，避免影响其它引用点）。
		// Oracle/PG 的 UPDATE/DELETE 语法本身不支持 LIMIT（直接写会报语法错），
		// 引擎侧按「出现 LIMIT 即报」或 CTE 包裹限量写法的行数解析各自落地。
		if on {
			params.DML_MAX_LIMIT_ROWS = firstPositive(rp.LimitMaxRows, defaultDMLLimitRows)
		} else {
			params.DML_MAX_LIMIT_ROWS = 0
		}
	case "mysql.query_update.limit_update_delete_join_table_count", "tidb.query_update.limit_update_delete_join_table_count",
		"postgres.query_update.limit_update_delete_join_table_count",
		"oracle.query_update.limit_update_delete_join_table_count":
		// 规则开启 => MAX_UPDATE_DELETE_JOIN_TABLES = 配置值（默认 5，含主表）；关闭 => 0（不限制）
		if on {
			params.MAX_UPDATE_DELETE_JOIN_TABLES = firstPositive(rp.MaxJoinTables, defaultDMLJoinTables)
		} else {
			params.MAX_UPDATE_DELETE_JOIN_TABLES = 0
		}
	case "mysql.query_update.update_stmt_suggest_update_time_column", "tidb.query_update.update_stmt_suggest_update_time_column",
		"postgres.query_update.update_stmt_suggest_update_time_column",
		"oracle.query_update.update_stmt_suggest_update_time_column":
		// 规则开启 => UPDATE_TIME_COLUMNS = 配置的列（默认 update_time）；关闭 => 空（不检查）
		if on {
			cols := strings.TrimSpace(rp.UpdateTimeColumns)
			if cols == "" {
				cols = defaultUpdateTimeColumns
			}
			params.UPDATE_TIME_COLUMNS = cols
		} else {
			params.UPDATE_TIME_COLUMNS = ""
		}
	case "mysql.query_update.update_stmt_avoid_update_create_time_column", "tidb.query_update.update_stmt_avoid_update_create_time_column",
		"postgres.query_update.update_stmt_avoid_update_create_time_column",
		"oracle.query_update.update_stmt_avoid_update_create_time_column":
		// 规则开启 => AVOID_UPDATE_COLUMNS = 配置的列（默认 create_time）；关闭 => 空（不检查）
		if on {
			cols := strings.TrimSpace(rp.AvoidUpdateColumns)
			if cols == "" {
				cols = defaultAvoidUpdateColumns
			}
			params.AVOID_UPDATE_COLUMNS = cols
		} else {
			params.AVOID_UPDATE_COLUMNS = ""
		}
	case "mysql.query_update.limit_where_in_element_count", "tidb.query_update.limit_where_in_element_count",
		"postgres.query_update.limit_where_in_element_count",
		"oracle.query_update.limit_where_in_element_count":
		// 规则开启 => MAX_WHERE_IN_ELEMENTS = 配置值（默认 1000）；关闭 => 0（不限制）
		if on {
			params.MAX_WHERE_IN_ELEMENTS = firstPositive(rp.MaxInElements, defaultMaxWhereInElements)
		} else {
			params.MAX_WHERE_IN_ELEMENTS = 0
		}
	case "mysql.query_update.update_delete_stmt_join_on_check", "tidb.query_update.update_delete_stmt_join_on_check",
		"postgres.query_update.update_delete_stmt_join_on_check",
		"oracle.query_update.update_delete_stmt_join_on_check":
		params.CHECK_DML_JOIN_WITH_ON = on
	case "mysql.query_update.limit_update_delete_affected_rows", "tidb.query_update.limit_update_delete_affected_rows",
		"postgres.query_update.limit_update_delete_affected_rows",
		"oracle.query_update.limit_update_delete_affected_rows":
		if on {
			params.MAX_AFFECTED_ROWS = firstPositive(rp.MaxAffectedRows, defaultMaxAffectedRows)
		} else {
			params.MAX_AFFECTED_ROWS = 0
		}
	case "mysql.query_update.limit_insert_values_row_count", "tidb.query_update.limit_insert_values_row_count",
		"postgres.query_update.limit_insert_values_row_count",
		"oracle.query_update.limit_insert_values_row_count":
		if on {
			params.MAX_INSERT_ROWS = firstPositive(rp.MaxInsertRows, defaultMaxInsertRows)
		} else {
			params.MAX_INSERT_ROWS = 0
		}

	// ---------- DML：语法/语义类检查（2026-09-29 新增实现） ----------
	// 这些规则此前只在目录里存在、引擎没有对应检查，页面能配级别但永不生效。
	// 现在按「规范中存在且级别 != off => 打开开关」的同一口径映射到引擎参数。
	case "mysql.query_update.update_stmt_set_separator_check", "tidb.query_update.update_stmt_set_separator_check",
		"postgres.query_update.update_stmt_set_separator_check",
		"oracle.query_update.update_stmt_set_separator_check":
		params.CHECK_UPDATE_SET_SEPARATOR = on
	case "mysql.query_update.update_multi_table_column_prefix_check", "tidb.query_update.update_multi_table_column_prefix_check",
		"postgres.query_update.update_multi_table_column_prefix_check",
		"oracle.query_update.update_multi_table_column_prefix_check":
		params.CHECK_UPDATE_MULTI_TABLE_COLUMN_PREFIX = on
	case "mysql.query_update.update_stmt_check_updates_pk", "tidb.query_update.update_stmt_check_updates_pk",
		"postgres.query_update.update_stmt_check_updates_pk",
		"oracle.query_update.update_stmt_check_updates_pk":
		params.CHECK_UPDATE_UPDATES_PK = on
	case "mysql.query_update.update_stmt_check_updates_unique_key", "tidb.query_update.update_stmt_check_updates_unique_key",
		"postgres.query_update.update_stmt_check_updates_unique_key":
		params.CHECK_UPDATE_UPDATES_UNIQUE_KEY = on
	case "mysql.query_update.delete_stmt_check_table_exists", "tidb.query_update.delete_stmt_check_table_exists",
		"postgres.query_update.delete_stmt_check_table_exists",
		"oracle.query_update.delete_stmt_check_table_exists":
		params.CHECK_DELETE_TABLE_EXISTS = on
	case "mysql.query_update.insert_stmt_check_column_exists", "tidb.query_update.insert_stmt_check_column_exists",
		"postgres.query_update.insert_stmt_check_column_exists",
		"oracle.query_update.insert_stmt_check_column_exists",
		"oracle.query_update.update_stmt_check_column_exists":
		// Oracle 的 update_stmt_check_column_exists 与 INSERT 的目标表/字段存在性
		// 共用 CHECK_INSERT_OBJECT_EXISTS 开关（同一存在性检查能力族）。
		params.CHECK_INSERT_OBJECT_EXISTS = on
	case "mysql.query_update.insert_stmt_columns_match_values", "tidb.query_update.insert_stmt_columns_match_values",
		"postgres.query_update.insert_stmt_columns_match_values",
		"oracle.query_update.insert_stmt_columns_match_values":
		params.CHECK_INSERT_COLUMNS_MATCH_VALUES = on
	// WHERE 条件类规则（2026-09-29 新增实现）
	case "mysql.query_update.where_avoid_leading_wildcard", "tidb.query_update.where_avoid_leading_wildcard",
		"postgres.query_update.where_avoid_leading_wildcard":
		params.CHECK_WHERE_LEADING_WILDCARD = on
	case "mysql.query_update.where_avoid_index_col_arithmetic_or_func", "tidb.query_update.where_avoid_index_col_arithmetic_or_func",
		"postgres.query_update.where_avoid_index_col_arithmetic_or_func":
		params.CHECK_WHERE_INDEX_COL_CALC = on
	case "mysql.query_update.where_avoid_not_in_not_like", "tidb.query_update.where_avoid_not_in_not_like",
		"postgres.query_update.where_avoid_not_in_not_like":
		params.CHECK_WHERE_NOT_IN_NOT_LIKE = on
	case "mysql.query_update.where_or_condition_check", "tidb.query_update.where_or_condition_check",
		"postgres.query_update.where_or_condition_check":
		params.CHECK_WHERE_OR_CONDITION = on
	case "mysql.query_update.where_implicit_type_cast_check", "tidb.query_update.where_implicit_type_cast_check":
		// PostgreSQL 目录中没有这条规则（PG 有独立的类型系统，不做等价判定）
		params.CHECK_WHERE_IMPLICIT_CAST = on
	case "mysql.query_update.where_like_without_wildcard", "tidb.query_update.where_like_without_wildcard",
		"postgres.query_update.where_like_without_wildcard":
		params.CHECK_WHERE_LIKE_WITHOUT_WILDCARD = on
	case "mysql.query_update.insert_stmt_col_name_no_dup", "tidb.query_update.insert_stmt_col_name_no_dup",
		"postgres.query_update.insert_stmt_col_name_no_dup",
		"oracle.query_update.insert_stmt_col_name_no_dup":
		params.CHECK_INSERT_COLUMN_NO_DUP = on
	case "mysql.query_update.insert_stmt_specify_columns", "tidb.query_update.insert_stmt_specify_columns",
		"postgres.query_update.insert_stmt_specify_columns",
		"oracle.query_update.insert_stmt_specify_columns":
		params.CHECK_INSERT_SPECIFY_COLUMNS = on
	case "mysql.query_update.insert_stmt_not_null_column_null_value", "tidb.query_update.insert_stmt_not_null_column_null_value",
		"postgres.query_update.insert_stmt_not_null_column_null_value",
		"oracle.query_update.insert_stmt_not_null_column_null_value":
		params.CHECK_INSERT_NOT_NULL_COLUMN_NULL = on
	case "mysql.query_update.insert_stmt_avoid_sysdate_func", "tidb.query_update.insert_stmt_avoid_sysdate_func",
		"postgres.query_update.insert_stmt_avoid_sysdate_func",
		"oracle.query_update.insert_stmt_avoid_sysdate_func":
		params.CHECK_INSERT_AVOID_SYSDATE = on
	}
}
