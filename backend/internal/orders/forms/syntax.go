package forms

import (
	"github.com/simajiong/jaysql/internal/common/models"
)

type InspectOrderSyntaxForm struct {
	// 与 CreateOrderForm.DBType 同口径：SQLServer 规范预审走专用文本解析引擎
	DBType     models.EnumType `form:"db_type" json:"db_type" binding:"omitempty,oneof=MySQL PostgreSQL Kingbase Oracle TiDB Doris SQLServer MSSQL"`
	SQLType    models.EnumType `form:"sql_type" json:"sql_type" binding:"omitempty,oneof=DML DDL MIXED"`
	InstanceID string          `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Database   string          `form:"database" json:"database" binding:"max=1024"`
	Schema     string          `form:"schema" json:"schema" binding:"max=1024"`
	Content    string          `form:"content" json:"content" binding:"required"`
}
