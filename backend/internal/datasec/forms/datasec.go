package forms

import "github.com/simajiong/jaysql/pkg/pagination"

// ---------- 脱敏规则 ----------

type ListRulesForm struct {
	PaginationQ pagination.Pagination `form:"-" json:"-"`
	Search      string                `form:"search" json:"search"`
	DataType    string                `form:"data_type" json:"data_type"`
}

type SaveRuleForm struct {
	ID        uint64 `form:"id" json:"id" binding:"required"`
	// phone/id_card/bank_card/email/address/name/custom
	DataType  string `form:"data_type" json:"data_type" binding:"required"`
	Name      string `form:"name" json:"name" binding:"required,min=2,max=64"`
	// full/head_tail/tail/head/email/address/name/hash
	Algorithm string `form:"algorithm" json:"algorithm" binding:"required"`
	// {"keep_head":3,"keep_tail":4} / {"hash_len":16}
	Params string `form:"params" json:"params"`
	Enabled bool  `form:"enabled" json:"enabled"`
	Remark  string `form:"remark" json:"remark" binding:"max=255"`
}

type IDForm struct {
	ID uint64 `form:"id" json:"id" uri:"id" binding:"required"`
}

// ---------- 列配置 ----------

type ListColumnsForm struct {
	PaginationQ pagination.Pagination `form:"-" json:"-"`
	InstanceID  string                `form:"instance_id" json:"instance_id"`
	SchemaName  string                `form:"schema_name" json:"schema_name"`
	TableName   string                `form:"table_name" json:"table_name"`
	Status      string                `form:"status" json:"status"` // enabled/suggested
}

type SaveColumnForm struct {
	ID         uint64 `form:"id" json:"id"`
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	SchemaName string `form:"schema_name" json:"schema_name"`
	TableName  string `form:"table_name" json:"table_name"`
	ColumnName string `form:"column_name" json:"column_name" binding:"required,max=64"`
	RuleID     uint64 `form:"rule_id" json:"rule_id" binding:"required"`
}

type ColumnStatusForm struct {
	ID     uint64 `form:"id" json:"id" uri:"id" binding:"required"`
	Status string `form:"status" json:"status" binding:"required"`
}

// ---------- 库/表/列级联选项 ----------
// 供「绑定列」与「申请明文」弹窗做选择器取数。与 SQL 工作台的 /das/schemas 系列不同：
// 那条链路按调用者已有数据授权过滤，而配脱敏规则的 DBA 未必有目标库的查询授权。

type SchemaOptionsForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
}

type TableOptionsForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	// MySQL 系传库名；PG 系传 "库.schema"
	SchemaName string `form:"schema_name" json:"schema_name" binding:"required,max=128"`
}

type ColumnOptionsForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	SchemaName string `form:"schema_name" json:"schema_name" binding:"required,max=128"`
	TableName  string `form:"table_name" json:"table_name" binding:"required,max=128"`
}

// ---------- 自动发现 ----------

type ScanTableForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	// MySQL 系传库名；PG 系传 "库.schema" 或仅库名（默认 public）
	SchemaName string `form:"schema_name" json:"schema_name" binding:"required,max=128"`
	// 英文逗号分隔的表名列表
	// 上限按「表名 64 字符 + 分隔符」估算，需容纳全选整库的场景（512 字符只够十几张表）
	TableNames string `form:"table_names" json:"table_names" binding:"required,max=65535"`
}

// ---------- 明文工单 ----------

type ListGrantsForm struct {
	PaginationQ pagination.Pagination `form:"-" json:"-"`
	Scope       string                `form:"scope" json:"scope"` // all=全部（需审批权限），缺省=我的
	Status      string                `form:"status" json:"status"`
}

type CreateGrantForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	SchemaName string `form:"schema_name" json:"schema_name" binding:"max=64"`
	TableName  string `form:"table_name" json:"table_name" binding:"max=64"`
	ColumnName string `form:"column_name" json:"column_name" binding:"max=64"`
	Reason     string `form:"reason" json:"reason" binding:"required,min=5,max=255"`
	// 有效时长（小时），1-24
	Hours int `form:"hours" json:"hours" binding:"required,min=1,max=24"`
}

type DecideGrantForm struct {
	ID     uint64 `form:"id" json:"id" uri:"id" binding:"required"`
	Action string `form:"action" json:"action" binding:"required,oneof=approve reject revoke"`
	Remark string `form:"remark" json:"remark" binding:"max=255"`
}
