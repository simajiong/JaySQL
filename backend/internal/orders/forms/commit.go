package forms

import (
	"github.com/simajiong/jaysql/pkg/pagination"

	"github.com/simajiong/jaysql/internal/common/models"
)

type GetOrderInstancesForm struct {
	PaginationQ pagination.Pagination
	ID          uint64 `form:"id"  json:"id" binding:"required"`
	DbType      string `form:"db_type" json:"db_type" binding:"required,oneof=MySQL Oracle SQLServer TiDB Doris PostgreSQL Kingbase ClickHouse"`
}

type GetOrderDataSourcesForm struct {
	PaginationQ pagination.Pagination
}

type GetOrderSchemasForm struct {
	PaginationQ  pagination.Pagination
	InstanceID   string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	ParentPath   string `form:"parent_path" json:"parent_path"`
	ResourceType string `form:"resource_type" json:"resource_type"`
}

type GetOrderUsersForm struct {
	PaginationQ pagination.Pagination
	PermScope   string `form:"perm_scope" json:"perm_scope" binding:"omitempty,oneof=approver executor"`
}

type CreateOrderForm struct {
	Title  string          `form:"title" json:"title" binding:"required,min=3,max=196"`
	Remark string          `form:"remark" json:"remark" binding:"max=1024"`
	// 与 validateOrderSQLTypeSupport / GetOrderDataSourcesService / execute 分发同口径：
	// SQLServer（含别名 MSSQL）工单链路（审核/执行器）已实现，这里必须放行，否则
	// 「下拉能选 SQLServer 实例、提交却被 binding 拦截」。
	DBType models.EnumType `form:"db_type" json:"db_type" binding:"omitempty,oneof=MySQL PostgreSQL Kingbase Oracle TiDB Doris SQLServer MSSQL"`
	// MIXED：同一工单内 DDL 与 DML 并存（类型由后端按内容推断，前端可不传）
	SQLType               models.EnumType `form:"sql_type" json:"sql_type" binding:"omitempty,oneof=DML DDL MIXED"`
	Environment           int             `form:"environment" json:"environment"`
	InstanceID            string          `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Database              string          `form:"database" json:"database" binding:"max=1024"`
	Schema                string          `form:"schema" json:"schema" binding:"max=1024"`
	Executor              string          `form:"executor" json:"executor" binding:"required,max=32"`
	EstimatedAffectedRows int64           `form:"estimated_affected_rows" json:"estimated_affected_rows" binding:"gte=0"`
	RollbackSQL           string          `form:"rollback_sql" json:"rollback_sql"`
	CC                    []string        `form:"cc" json:"cc"`
	Content               string          `form:"content" json:"content" binding:"required"`
	ExportFileFormat      models.EnumType `form:"export_file_format" json:"export_file_format" binding:"omitempty,oneof=XLSX CSV"`
}
