package forms

import (
	"github.com/simajiong/jaysql/pkg/pagination"

	"github.com/simajiong/jaysql/internal/common/models"
)

type AdminGetInstancesForm struct {
	PaginationQ pagination.Pagination
	Search      string `form:"search"`
	Environment string `form:"environment"`
	DbType      string `form:"db_type"`
	Status      string `form:"status"`
}

type AdminCreateInstancesForm struct {
	Hostname    string          `form:"hostname"  json:"hostname" binding:"required,min=2,max=128"`
	Port        int             `form:"port"  json:"port" binding:"required"`
	User        string          `form:"user"  json:"user" binding:"required,min=1,max=128"`
	Password    string          `form:"password"  json:"password" binding:"required,min=1,max=256"`
	DbType      models.EnumType `form:"db_type"  json:"db_type" binding:"required,oneof=MySQL TiDB Doris ClickHouse PostgreSQL Kingbase MongoDB Oracle SQLServer OceanBase"`
	ExtraConfig map[string]any  `form:"extra_config" json:"extra_config"`
	Environment int             `form:"environment"  json:"environment" binding:"required"`
	Remark      string          `form:"remark"  json:"remark" binding:"required,min=2,max=256"`

	// ---- 实例画像：只能由运维录入的产品事实 ----
	// 部署形态与高可用模式从数据库侧探测不出来（实例不知道自己跑在谁家的机器上、
	// 也不知道自己归哪个 HA 组件管），只能录入。留空不报错 —— 页面按"未录入 + 原因"展示。
	DeployProvider string `form:"deploy_provider" json:"deploy_provider" binding:"omitempty,oneof=self_built cloud"`
	CloudVendor    string `form:"cloud_vendor" json:"cloud_vendor" binding:"omitempty,max=64"`
	HaMode         string `form:"ha_mode" json:"ha_mode" binding:"omitempty,max=64"`
	// DiskType 磁盘类型（SSD / HDD / ESSD PL1 …）：磁盘延迟阈值的判读依据，同样只能录入
	DiskType string `form:"disk_type" json:"disk_type" binding:"omitempty,max=32"`
}

type AdminUpdateInstancesForm struct {
	Hostname string `form:"hostname"  json:"hostname" binding:"required,min=2,max=128"`
	Port     int    `form:"port"  json:"port" binding:"required"`
	User     string `form:"user"  json:"user" binding:"required,min=1,max=128"`
	// 编辑时 password 允许不传/为空：不修改原密码；当传入非空值时才会更新
	Password    string          `form:"password"  json:"password" binding:"omitempty,min=1,max=256"`
	DbType      models.EnumType `form:"db_type"  json:"db_type" binding:"required,oneof=MySQL TiDB Doris ClickHouse PostgreSQL Kingbase MongoDB Oracle SQLServer OceanBase"`
	ExtraConfig map[string]any  `form:"extra_config" json:"extra_config"`
	Environment int             `form:"environment"  json:"environment" binding:"required"`
	Remark      string          `form:"remark"  json:"remark" binding:"required,min=2,max=256"`

	// 与创建保持一致：这四个是录入型画像字段，留空表示不修改
	DeployProvider string `form:"deploy_provider" json:"deploy_provider" binding:"omitempty,oneof=self_built cloud"`
	CloudVendor    string `form:"cloud_vendor" json:"cloud_vendor" binding:"omitempty,max=64"`
	HaMode         string `form:"ha_mode" json:"ha_mode" binding:"omitempty,max=64"`
	DiskType       string `form:"disk_type" json:"disk_type" binding:"omitempty,max=32"`
}

// AdminUpdateInstanceRemarkForm 数据源名称（remark）行内单独改名。
// 刻意不走全量更新接口：那需要回传 hostname/port/extra_config 全套，
// 列表行内快速改名漏传任何一个字段都会把连接信息冲掉，风险不对称。
type AdminUpdateInstanceRemarkForm struct {
	Remark string `form:"remark"  json:"remark" binding:"required,min=2,max=256"`
}

type AdminBatchImportInstancesForm struct {
	Format          string                   `form:"format" json:"format" binding:"required,oneof=csv markdown auto"`
	FileName        string                   `form:"file_name" json:"file_name"`
	Content         string                   `form:"content" json:"content" binding:"required,min=1,max=5242880"`
	DuplicatePolicy string                   `form:"duplicate_policy" json:"duplicate_policy" binding:"required,oneof=skip update abort"`
	Rows            []map[string]interface{} `form:"rows" json:"rows"`
	BatchID         string                   `form:"batch_id" json:"batch_id" binding:"max=64"`
}
