package services

// ---------------------------------------------------------------------------
// 数据访问申请入口的取数接口
//
// 与 das/resources 系列的区别：那些接口按「申请人已有授权」过滤，
// 而申请入口恰恰是用户还没有权限时用的——如果也按权限过滤，
// 用户在页面上连目标数据源都看不到，申请流程就死在了第一步。
// 因此这里只做最小暴露：
//   实例 —— 名称/地址/类型等脱敏元数据，不含凭据；
//   库   —— 元数据表 insight_instance_schemas，静态数据；
//   表   —— 服务端连实例实时拉表名清单（只回表名，不回任何列/数据）。
// 申请人真正拿到数据仍然必须经过「审批通过 → 写授权表」这一道闸。
// ---------------------------------------------------------------------------

import (
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/common/tasks"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"

	"github.com/gin-gonic/gin"
)

// ListAccessInstanceOptionsService 申请入口：数据源下拉
type ListAccessInstanceOptionsService struct {
	*forms.AccessInstanceOptionsForm
}

func (s *ListAccessInstanceOptionsService) Run() ([]map[string]any, error) {
	rows := []struct {
		InstanceID string
		Remark     string
		Hostname   string
		Port       int
		DBType     string
	}{}
	tx := global.App.DB.Table("insight_instances").
		Select("instance_id, ifnull(remark,'') as remark, ifnull(hostname,'') as hostname, ifnull(port,0) as port, ifnull(db_type,'') as db_type")
	if v := strings.TrimSpace(s.Search); v != "" {
		like := "%" + v + "%"
		tx = tx.Where("remark like ? or hostname like ? or db_type like ?", like, like, like)
	}
	if err := tx.Order("remark asc, hostname asc, port asc").Limit(200).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		name := strings.TrimSpace(r.Remark)
		addr := fmt.Sprintf("%s:%d", r.Hostname, r.Port)
		if name == "" {
			name = addr
		}
		out = append(out, map[string]any{
			"instance_id":   r.InstanceID,
			"name":          name,
			"addr":          addr,
			"db_type":       r.DBType,
			"instance_name": addr,
			// has_schema_level 决定申请页「申请范围」是三项还是四项（实例/库/模式/表）。
			// 由服务端下发而不是前端按 db_type 自己 switch，避免两边口径漂移。
			"has_schema_level": HasSchemaLevel(r.DBType),
		})
	}
	return out, nil
}

// accessSchemaOption 库/Schema 下拉项
type accessSchemaOption struct {
	InstanceID   string `json:"instance_id"`
	Schema       string `json:"schema"`
	DisplayName  string `json:"display_name"`
	ResourceType string `json:"resource_type"`
	ParentPath   string `json:"parent_path"`
}

// ListAccessSchemaOptionsService 申请入口：库/Schema 下拉（元数据静态查询）
type ListAccessSchemaOptionsService struct {
	*forms.AccessSchemaOptionsForm
}

func (s *ListAccessSchemaOptionsService) Run() ([]accessSchemaOption, error) {
	// 读时惰性刷新：申请页选择的库来自元数据快照，快照过期时异步补刷一次。
	// 本次请求仍返回现有快照 —— 申请入口绝不能因为一次远端连接慢而卡住。
	tasks.EnsureFresh(s.InstanceID)
	rows := []accessSchemaOption{}
	err := global.App.DB.Table("insight_instance_schemas").
		Select("instance_id, `schema`, ifnull(display_name,'') as display_name, ifnull(resource_type,'') as resource_type, ifnull(parent_path,'') as parent_path").
		Where("instance_id = ? AND is_deleted = 0", strings.TrimSpace(s.InstanceID)).
		Order("`schema` asc").
		Find(&rows).Error
	return rows, err
}

// accessTableOption 表下拉项
type accessTableOption struct {
	Name    string `json:"name"`
	Comment string `json:"comment"`
}

// ListAccessTableOptionsService 申请入口：表下拉（服务端连实例实时拉取）
type ListAccessTableOptionsService struct {
	*forms.AccessTableOptionsForm
	C *gin.Context
}

func (s *ListAccessTableOptionsService) Run() ([]accessTableOption, error) {
	instanceID := strings.TrimSpace(s.InstanceID)
	schemaPath := strings.TrimSpace(s.Schema)

	var inst struct {
		Hostname string
		Port     int
		User     string
		Password string
		DBType   string
	}
	if err := global.App.DB.Table("insight_instances").
		Select("ifnull(hostname,'') as hostname, ifnull(port,0) as port, ifnull(`user`,'') as `user`, ifnull(password,'') as password, ifnull(db_type,'') as db_type").
		Where("instance_id = ?", instanceID).Take(&inst).Error; err != nil {
		return nil, fmt.Errorf("数据源不存在或已删除")
	}
	plain, err := secret.Decrypt(inst.Password)
	if err != nil {
		return nil, fmt.Errorf("数据源凭据解密失败")
	}
	db := dao.DB{
		User:     inst.User,
		Password: plain,
		Host:     inst.Hostname,
		Port:     inst.Port,
		Ctx:      s.C.Request.Context(),
	}

	// MySQL 家族：schema 即库名；PG 家族：schema 路径形如 "database.schema"
	// （与资源树/授权表的 schema_name 口径一致），拆出实际 database 与 schema。
	var query string
	switch {
	case IsMySQLFamily(inst.DBType):
		query = fmt.Sprintf(
			"SELECT table_name AS name, ifnull(table_comment,'') AS comment FROM information_schema.tables WHERE table_schema = '%s' AND table_type = 'BASE TABLE' ORDER BY table_name",
			strings.ReplaceAll(schemaPath, "'", "''"),
		)
	case IsPostgresFamily(inst.DBType):
		parts := strings.SplitN(schemaPath, ".", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("PostgreSQL 需要选择具体的库与 Schema 后才能列出表")
		}
		query = fmt.Sprintf(
			"SELECT c.relname AS name, ifnull(obj_description(c.oid),'') AS comment FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = '%s' AND c.relkind = 'r' ORDER BY c.relname",
			strings.ReplaceAll(parts[1], "'", "''"),
		)
	default:
		return nil, fmt.Errorf("该数据库类型暂不支持表级申请，请选择实例级或库级")
	}

	_, data, err := db.Query(query)
	if err != nil {
		global.App.Log.Error(fmt.Sprintf("access table options query failed: %v", err))
		return nil, fmt.Errorf("获取表清单失败，请检查数据源连接状态")
	}
	out := make([]accessTableOption, 0, len(*data))
	for _, row := range *data {
		name, _ := row["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		comment, _ := row["comment"].(string)
		out = append(out, accessTableOption{Name: name, Comment: comment})
	}
	return out, nil
}
