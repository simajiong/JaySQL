package forms

import "github.com/simajiong/jaysql/pkg/pagination"

type AdminSchemasGrantForm struct {
	PaginationQ pagination.Pagination
	Search      string `form:"search"`
	Environment string `form:"environment"`
}

type AdminGetInstancesListForm struct {
	PaginationQ pagination.Pagination
	ID          uint64 `form:"id"  json:"id" binding:"required"`
	DbType      string `form:"db_type" json:"db_type" binding:"required,oneof=MySQL Oracle SQLServer TiDB Doris PostgreSQL Kingbase ClickHouse OceanBase"`
}

type AdminGetSchemasListForm struct {
	PaginationQ  pagination.Pagination
	InstanceID   string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	ParentPath   string `form:"parent_path" json:"parent_path"`
	ResourceType string `form:"resource_type" json:"resource_type"`
}

type AdminGetTablesListForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Schema     string `form:"schema" json:"schema" binding:"required"`
}

type AdminCreateSchemasGrantForm struct {
	PrincipalType        string                       `form:"principal_type" json:"principal_type" binding:"required,oneof=user role"`
	PrincipalID          uint64                       `form:"principal_id" json:"principal_id" binding:"required"`
	InstanceID           string                       `form:"instance_id" json:"instance_id" binding:"omitempty,min=3,max=15"`
	InstanceIDs          []string                     `form:"instance_ids" json:"instance_ids" binding:"omitempty,dive,min=3,max=15"`
	Schema               string                       `form:"schema" json:"schema"`
	ResourceType         string                       `form:"resource_type" json:"resource_type"`
	Tables               []string                     `form:"tables" json:"tables"`
	Rule                 string                       `form:"rule" json:"rule"`
	GrantItems           []AdminCreateSchemaGrantItem `form:"grant_items" json:"grant_items"`
	PreserveFirstGranted *string                      `form:"preserve_first_granted_at" json:"preserve_first_granted_at" binding:"omitempty"`
}

type AdminCreateSchemaGrantItem struct {
	Schema       string   `form:"schema" json:"schema"`
	ResourceType string   `form:"resource_type" json:"resource_type"`
	Tables       []string `form:"tables" json:"tables"`
	Rule         string   `form:"rule" json:"rule"`
}

type AdminGetTablesGrantForm struct {
	PaginationQ   pagination.Pagination
	Search        string `form:"search"`
	PrincipalType string `form:"principal_type" json:"principal_type" binding:"required,oneof=user role"`
	PrincipalID   uint64 `form:"principal_id" json:"principal_id" binding:"required"`
	InstanceID    string `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Schema        string `form:"schema" json:"schema" binding:"required"`
}

type AdminCreateTablesGrantForm struct {
	PrincipalType        string   `form:"principal_type" json:"principal_type" binding:"required,oneof=user role"`
	PrincipalID          uint64   `form:"principal_id" json:"principal_id" binding:"required"`
	InstanceID           string   `form:"instance_id" json:"instance_id" binding:"required,min=3,max=15"`
	Schema               string   `form:"schema" json:"schema" binding:"required"`
	ResourceType         string   `form:"resource_type" json:"resource_type" binding:"required"`
	Tables               []string `form:"tables" json:"tables"`
	Rule                 string   `form:"rule" json:"rule" binding:"required"`
	PreserveFirstGranted *string  `form:"preserve_first_granted_at" json:"preserve_first_granted_at" binding:"omitempty"`
}
