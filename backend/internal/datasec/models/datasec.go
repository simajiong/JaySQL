package models

import (
	"github.com/simajiong/jaysql/internal/common/models"
	"gorm.io/datatypes"
)

// 数据安全（脱敏）模块三张核心表：
//   1. 规则表：敏感类型 + 脱敏算法的映射定义（可复用，列配置引用规则）；
//   2. 列配置表：实例 + 库 + 表 + 列 → 绑定规则，是查询链路脱敏的执行依据；
//   3. 明文工单表：限时明文访问授权，到期自动失效。

// InsightDataSecRules 脱敏规则：定义「某类敏感数据怎么脱」。
// 规则与具体列解耦：同一条「手机号-保留前3后4」规则可以绑到任意实例的任意列上。
type InsightDataSecRules struct {
	*models.Model
	// 敏感数据类型：phone/id_card/bank_card/email/address/name/custom
	DataType models.EnumType `gorm:"type:varchar(32);not null;comment:敏感数据类型" json:"data_type"`
	// 规则名称，唯一。内置规则用「类型-算法」命名，方便扫描建议直接引用。
	Name string `gorm:"type:varchar(64);not null;default:'';uniqueIndex:uniq_datasec_rule_name;comment:规则名称" json:"name"`
	// 脱敏算法：full/head_tail/tail/head/email/address/name/hash
	Algorithm models.EnumType `gorm:"type:varchar(32);not null;default:'full';comment:脱敏算法" json:"algorithm"`
	// 算法参数 JSON：head_tail/tail/head 用 keep_head / keep_tail；hash 用 hash_len
	Params datatypes.JSON `gorm:"comment:算法参数JSON" json:"params"`
	// 1=启用 0=停用（停用后引用该规则的列不再脱敏）
	Enabled int `gorm:"type:int;not null;default:1;comment:是否启用 1是 0否" json:"enabled"`
	Remark  string `gorm:"type:varchar(255);not null;default:'';comment:备注" json:"remark"`
	// 内置规则（seed 生成）不可删除，只能停用，避免扫描建议悬空
	BuiltIn int `gorm:"type:int;not null;default:0;comment:是否内置 1是 0否" json:"built_in"`
	CreatedBy string `gorm:"type:varchar(32);not null;default:'';comment:创建人" json:"created_by"`
}

func (InsightDataSecRules) TableName() string { return "insight_datasec_rules" }

// InsightDataSecColumns 列配置：脱敏的执行依据。
// InstanceID 用业务字符串键（char(15)，与 das/backup 表单口径一致），
// 匹配时按「列名」归一（大小写不敏感），不区分具体来源表，详见 services/mask.go。
type InsightDataSecColumns struct {
	*models.Model
	InstanceID string `gorm:"type:char(15);not null;default:'';uniqueIndex:uniq_datasec_col,priority:1;comment:实例业务ID" json:"instance_id"`
	SchemaName string `gorm:"type:varchar(64);not null;default:'';uniqueIndex:uniq_datasec_col,priority:2;comment:库名/schema" json:"schema_name"`
	TblName    string `gorm:"column:table_name;type:varchar(64);not null;default:'';uniqueIndex:uniq_datasec_col,priority:3;comment:表名" json:"table_name"`
	ColumnName string `gorm:"type:varchar(64);not null;default:'';uniqueIndex:uniq_datasec_col,priority:4;comment:列名" json:"column_name"`
	RuleID     uint64 `gorm:"type:bigint;not null;default:0;comment:关联规则ID" json:"rule_id"`
	// enabled=已生效（查询时脱敏）；suggested=扫描建议（待人工确认）。
	// 同一列只有一行记录，扫描建议覆盖确认后原地转 enabled。
	Status string `gorm:"type:varchar(16);not null;default:'enabled';comment:状态 enabled/suggested" json:"status"`
	// manual=手工配置；scan=自动发现
	Source string `gorm:"type:varchar(16);not null;default:'manual';comment:来源 manual/scan" json:"source"`
	// 自动发现的置信度 0-100，手工配置为 100
	Confidence int    `gorm:"type:int;not null;default:100;comment:置信度0-100" json:"confidence"`
	SampleHit  string `gorm:"type:varchar(128);not null;default:'';comment:判定依据" json:"sample_hit"`
	CreatedBy  string `gorm:"type:varchar(32);not null;default:'';comment:创建人" json:"created_by"`
}

func (InsightDataSecColumns) TableName() string { return "insight_datasec_columns" }

// InsightDataSecPlainGrants 明文访问工单：脱敏是默认，明文是限时特例。
type InsightDataSecPlainGrants struct {
	*models.Model
	GrantNo    string `gorm:"type:char(24);not null;default:'';uniqueIndex:uniq_datasec_grant_no;comment:工单编号" json:"grant_no"`
	InstanceID string `gorm:"type:char(15);not null;default:'';index:idx_datasec_grant_instance;comment:实例业务ID" json:"instance_id"`
	// 三级范围允许逐级留空：列空=整表，表空=整库，库空=整实例
	SchemaName string `gorm:"type:varchar(64);not null;default:'';comment:库名(空=整实例)" json:"schema_name"`
	TblName    string `gorm:"column:table_name;type:varchar(64);not null;default:'';comment:表名(空=整库)" json:"table_name"`
	ColumnName string `gorm:"type:varchar(64);not null;default:'';comment:列名(空=整表)" json:"column_name"`
	Username   string `gorm:"type:varchar(32);not null;default:'';index:idx_datasec_grant_user;comment:申请人" json:"username"`
	Reason     string `gorm:"type:varchar(255);not null;default:'';comment:申请事由" json:"reason"`
	// pending/approved/rejected/expired/revoked
	Status string `gorm:"type:varchar(16);not null;default:'pending';index:idx_datasec_grant_status;comment:状态" json:"status"`
	Approver      string                `gorm:"type:varchar(32);not null;default:'';comment:审批人" json:"approver"`
	ApproveRemark string                `gorm:"type:varchar(255);not null;default:'';comment:审批意见" json:"approve_remark"`
	ExpiresAt     *models.LocalTime     `gorm:"comment:过期时间" json:"expires_at"`
	ApprovedAt    *models.LocalTime     `gorm:"comment:审批时间" json:"approved_at"`
}

func (InsightDataSecPlainGrants) TableName() string { return "insight_datasec_plain_grants" }
