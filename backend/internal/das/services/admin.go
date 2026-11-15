package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/pagination"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/common/tasks"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/models"
	"github.com/simajiong/jaysql/pkg/secret"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AdminGetSchemasGrantService struct {
	*forms.AdminSchemasGrantForm
	C *gin.Context
}

func (s *AdminGetSchemasGrantService) Run() (responseData any, total int64, err error) {
	type result struct {
		ID                       uint64                  `json:"id"`
		CreatedAt                commonModels.LocalTime  `json:"created_at"`
		UpdatedAt                commonModels.LocalTime  `json:"updated_at"`
		TableMaxUpdatedAt        *commonModels.LocalTime `json:"table_max_updated_at"`
		PrincipalEarliestCreated *commonModels.LocalTime `json:"principal_earliest_created_at"`
		PrincipalType            string                  `json:"principal_type"`
		PrincipalID              uint64                  `json:"principal_id"`
		PrincipalName            string                  `json:"principal_name"`
		Username                 string                  `json:"username"`
		Schema                   string                  `json:"schema"`
		ResourceType             string                  `json:"resource_type"`
		InstanceID               string                  `json:"instance_id"`
		Hostname                 string                  `json:"hostname"`
		Port                     int                     `json:"port"`
		DBType                   string                  `json:"db_type"`
		Environment              string                  `json:"environment"`
		Remark                   string                  `json:"remark"`
		ParentPath               string                  `json:"parent_path"`
		DatabaseName             string                  `json:"database_name"`
		SchemaName               string                  `json:"schema_name"`
		DisplayName              string                  `json:"display_name"`
		PrincipalDeletedHint     string                  `json:"principal_deleted_hint"`
	}
	var schemaPerms []result
	tx := global.App.DB.Select(`
			a.id,
			a.principal_type,
			a.principal_id,
			a.principal_name,
			a.username,
			a.schema,
			a.resource_type,
			a.instance_id,
			a.created_at,
			a.updated_at,
			tp.table_max_updated_at,
			pc.earliest_created_at as principal_earliest_created_at,
			b.hostname,
			b.port,
			b.db_type,
			c.name as environment,
			b.remark,
			s.parent_path,
			case
				when a.resource_type = 'instance' then ifnull(b.remark, concat(b.hostname, ':', b.port))
				else ifnull(s.display_name, a.schema)
			end as display_name
		`).
		Table("insight_das_schema_perms a").
		Joins("left join insight_instances b on a.instance_id=b.instance_id").
		Joins("left join insight_instance_schemas s on a.instance_id=s.instance_id and a.schema=s.schema and a.resource_type=s.resource_type").
		Joins("left join insight_instance_environments c on b.environment=c.id").
		Joins(`left join (
			select principal_type, principal_id, instance_id, ` + "`schema`" + `, resource_type, max(updated_at) as table_max_updated_at
			from insight_das_table_perms
			group by principal_type, principal_id, instance_id, ` + "`schema`" + `, resource_type
		) tp on a.principal_type=tp.principal_type and a.principal_id=tp.principal_id and a.instance_id=tp.instance_id and a.` + "`schema`" + `=tp.` + "`schema`" + ` and a.resource_type=tp.resource_type`).
		Joins(`left join (
			select principal_type, principal_id, min(created_at) as earliest_created_at
			from insight_das_schema_perms
			group by principal_type, principal_id
		) pc on a.principal_type=pc.principal_type and a.principal_id=pc.principal_id`).
		Order("a.updated_at desc")
	// 搜索
	if s.Search != "" {
		tx = tx.Where("b.`hostname` like ? or b.`remark` like ? or a.`instance_id` like ? or a.principal_name like ? or a.schema like ? or a.resource_type like ?", "%"+s.Search+"%", "%"+s.Search+"%", "%"+s.Search+"%", "%"+s.Search+"%", "%"+s.Search+"%", "%"+s.Search+"%")
	}
	if s.Environment != "" {
		tx = tx.Where("c.name = ?", s.Environment)
	}
	total = pagination.Pager(&s.PaginationQ, tx, &schemaPerms)
	for i := range schemaPerms {
		if IsPostgresFamily(schemaPerms[i].DBType) || strings.EqualFold(schemaPerms[i].DBType, "sqlserver") {
			databaseName, schemaName, displayName := deriveDatabaseAndSchemaNames(schemaPerms[i].Schema, schemaPerms[i].ParentPath, schemaPerms[i].DisplayName)
			schemaPerms[i].DatabaseName = databaseName
			schemaPerms[i].SchemaName = schemaName
			if schemaPerms[i].ResourceType == "schema" {
				schemaPerms[i].DisplayName = displayName
			}
		}
	}
	// 给每条授权主体打"已删除/已停用/不存在"标记（方便前端显示灰标）
	if len(schemaPerms) > 0 {
		userUIDs := make([]uint64, 0, len(schemaPerms))
		roleIDs := make([]uint64, 0, len(schemaPerms))
		for _, item := range schemaPerms {
			if normalizeDASPrincipalType(item.PrincipalType) == dasPrincipalUser {
				userUIDs = append(userUIDs, item.PrincipalID)
			} else {
				roleIDs = append(roleIDs, item.PrincipalID)
			}
		}
		userStatus := make(map[uint64]string, len(userUIDs))
		if len(userUIDs) > 0 {
			type userRow struct {
				Uid       uint64
				IsActive  bool
				IsDeleted bool
			}
			var users []userRow
			_ = global.App.DB.Table("insight_users").Select("uid, is_active, is_deleted").Where("uid in ?", userUIDs).Scan(&users).Error
			for _, u := range users {
				switch {
				case u.IsDeleted:
					userStatus[u.Uid] = "已删除"
				case !u.IsActive:
					userStatus[u.Uid] = "已停用"
				}
			}
			// 收集仍存在（至少有一条）的 UID 集合，用于判断"查不到=主体已不存在"
			existingUIDs := make(map[uint64]struct{}, len(users))
			for _, u := range users {
				existingUIDs[u.Uid] = struct{}{}
			}
			for _, uid := range userUIDs {
				if _, ok := existingUIDs[uid]; !ok {
					userStatus[uid] = "主体已不存在"
				}
			}
		}
		roleStatus := make(map[uint64]string, len(roleIDs))
		if len(roleIDs) > 0 {
			type roleRow struct {
				Id        uint64
				IsDeleted bool
			}
			var roles []roleRow
			_ = global.App.DB.Table("insight_roles").Select("id, is_deleted").Where("id in ?", roleIDs).Scan(&roles).Error
			existingRoleIDs := make(map[uint64]struct{}, len(roles))
			for _, r := range roles {
				existingRoleIDs[r.Id] = struct{}{}
				if r.IsDeleted {
					roleStatus[r.Id] = "已删除"
				}
			}
			for _, rid := range roleIDs {
				if _, ok := existingRoleIDs[rid]; !ok {
					roleStatus[rid] = "角色已不存在"
				}
			}
		}
		for i := range schemaPerms {
			ptype := normalizeDASPrincipalType(schemaPerms[i].PrincipalType)
			if ptype == dasPrincipalUser {
				if hint, ok := userStatus[schemaPerms[i].PrincipalID]; ok {
					schemaPerms[i].PrincipalDeletedHint = hint
				}
			} else {
				if hint, ok := roleStatus[schemaPerms[i].PrincipalID]; ok {
					schemaPerms[i].PrincipalDeletedHint = hint
				}
			}
		}
	}
	return &schemaPerms, total, nil
}

// 获取指定环境的实例
type AdminGetInstancesListService struct {
	*forms.AdminGetInstancesListForm
	C *gin.Context
}

func (s *AdminGetInstancesListService) Run() (responseData any, total int64, err error) {
	var configs []commonModels.InsightInstances
	tx := global.App.DB.Table("insight_instances").Where("environment=? and db_type=?", s.ID, s.DbType)
	total = pagination.Pager(&s.PaginationQ, tx, &configs)
	return &configs, total, nil
}

type AdminGetSchemasListService struct {
	*forms.AdminGetSchemasListForm
	C *gin.Context
}

func (s *AdminGetSchemasListService) Run() (responseData any, total int64, err error) {
	type schemaListItem struct {
		InstanceID   string `json:"instance_id"`
		Schema       string `json:"schema"`
		ParentPath   string `json:"parent_path"`
		ResourceType string `json:"resource_type"`
		DisplayName  string `json:"display_name"`
		IsDeleted    bool   `json:"is_deleted"`
		DBType       string `json:"db_type"`
		DatabaseName string `json:"database_name"`
		SchemaName   string `json:"schema_name"`
	}
	var roles []schemaListItem
	// 读时惰性刷新：授权管理页的库清单同样来自元数据快照，
	// 新建库后若快照已过期，这里异步补刷一次，下次展开即可见。
	tasks.EnsureFresh(s.InstanceID)
	tx := global.App.DB.Table("insight_instance_schemas a").
		Select("a.instance_id, a.schema, a.parent_path, a.resource_type, a.display_name, a.is_deleted, i.db_type").
		Joins("join insight_instances i on a.instance_id = i.instance_id").
		Where("a.instance_id=? and a.is_deleted=0", s.InstanceID)
	if s.ParentPath != "" {
		tx = tx.Where("a.parent_path=?", s.ParentPath)
	} else {
		tx = tx.Where("a.parent_path=''")
	}
	if s.ResourceType != "" {
		tx = tx.Where("a.resource_type=?", s.ResourceType)
	}
	tx = tx.Order("a.display_name asc, a.`schema` asc")
	total = pagination.Pager(&s.PaginationQ, tx, &roles)
	for i := range roles {
		if IsPostgresFamily(roles[i].DBType) || strings.EqualFold(roles[i].DBType, "sqlserver") {
			databaseName, schemaName, displayName := deriveDatabaseAndSchemaNames(roles[i].Schema, roles[i].ParentPath, roles[i].DisplayName)
			roles[i].DatabaseName = databaseName
			roles[i].SchemaName = schemaName
			if roles[i].ResourceType == "schema" {
				roles[i].DisplayName = displayName
			}
		}
	}
	return &roles, total, nil
}

type AdminGetTablesListService struct {
	*forms.AdminGetTablesListForm
	C *gin.Context
}

// 获取MySQL/TiDB的表信息
func (g *AdminGetTablesListService) getMySQLMetaData(r *InstanceCfg) (data *[]map[string]any, err error) {
	if err := validateIdentifier(r.Schema, "schema"); err != nil {
		return nil, err
	}
	var query string = fmt.Sprintf(`
		select 
			table_name as table_name
		from 
			information_schema.tables
			where 
				table_schema=%s and table_name not regexp '^_(.*)[_ghc|_gho|_del]$'
			`, quoteStringLiteral(r.Schema))

	db := dao.DB{
		User:     r.User,
		Password: r.PlainPassword,
		Host:     r.Hostname,
		Port:     r.Port,
		Params:   map[string]string{"group_concat_max_len": "4194304"},
		Ctx:      g.C.Request.Context(),
	}
	_, data, err = db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return data, err
	}
	return data, nil
}

// 获取ClickHouse的表信息
func (g *AdminGetTablesListService) getClickHouseMetaData(r *InstanceCfg) (data *[]map[string]any, err error) {
	if err := validateIdentifier(r.Schema, "schema"); err != nil {
		return nil, err
	}
	var query string = fmt.Sprintf(`
	select 
		name as table_name
	from 
		system.tables 
		where 
			(database = %s)
		`, quoteStringLiteral(r.Schema))

	db := dao.ClickhouseDB{
		User:     r.User,
		Password: r.PlainPassword,
		Host:     r.Hostname,
		Port:     r.Port,
		Ctx:      g.C.Request.Context(),
	}
	_, data, err = db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return data, err
	}
	return data, nil
}

type pgExtraConfig struct {
	Database string `json:"database"`
	SSLMode  string `json:"sslmode"`
}

type oracleAdminExtraConfig struct {
	ServiceName string `json:"service_name"`
}

func (g *AdminGetTablesListService) getPostgresMetaData(r *InstanceCfg) (data *[]map[string]any, err error) {
	parts := strings.SplitN(r.Schema, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("PostgreSQL 资源路径不合法")
	}
	databaseName := parts[0]
	schemaName := parts[1]
	if err := validateIdentifier(databaseName, "database"); err != nil {
		return nil, err
	}
	if err := validateIdentifier(schemaName, "schema"); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
		select
			table_name as table_name
		from
			information_schema.tables
		where
			table_schema=%s and table_type='BASE TABLE'
		order by table_name
	`, quoteStringLiteral(schemaName))

	cfg := pgExtraConfig{}
	if len(r.ExtraConfig) > 0 {
		_ = json.Unmarshal(r.ExtraConfig, &cfg)
	}
	db := dao.PostgresDB{
		User:     r.User,
		Password: r.PlainPassword,
		Host:     r.Hostname,
		Port:     r.Port,
		Database: databaseName,
		SSLMode:  cfg.SSLMode,
		Ctx:      g.C.Request.Context(),
	}
	_, rows, err := db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return rows, err
	}
	return rows, nil
}

func (g *AdminGetTablesListService) getSQLServerMetaData(r *InstanceCfg) (data *[]map[string]any, err error) {
	parts := strings.SplitN(r.Schema, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("SQLServer 资源路径不合法")
	}
	databaseName := parts[0]
	schemaName := parts[1]
	if err := validateIdentifier(databaseName, "database"); err != nil {
		return nil, err
	}
	if err := validateIdentifier(schemaName, "schema"); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
		select
			table_name as table_name
		from
			information_schema.tables
		where
			table_schema=%s and table_type='BASE TABLE'
		order by table_name
	`, quoteStringLiteral(schemaName))

	db := dao.SQLServerDB{
		User:     r.User,
		Password: r.PlainPassword,
		Host:     r.Hostname,
		Port:     r.Port,
		Database: databaseName,
		Ctx:      g.C.Request.Context(),
	}
	_, rows, err := db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return rows, err
	}
	return rows, nil
}

func (g *AdminGetTablesListService) getOracleMetaData(r *InstanceCfg) (data *[]map[string]any, err error) {
	schemaName := strings.TrimSpace(r.Schema)
	if schemaName == "" {
		return nil, fmt.Errorf("Oracle Schema不能为空")
	}
	cfg := oracleAdminExtraConfig{}
	if len(r.ExtraConfig) > 0 {
		_ = json.Unmarshal(r.ExtraConfig, &cfg)
	}
	db := dao.OracleDB{
		User:          r.User,
		Password:      r.PlainPassword,
		Host:          r.Hostname,
		Port:          r.Port,
		ServiceName:   cfg.ServiceName,
		CurrentSchema: schemaName,
		Ctx:           g.C.Request.Context(),
	}
	query := fmt.Sprintf(`
		select
			table_name as table_name
		from
			all_tables
		where
			owner = %s
		order by table_name
	`, quoteStringLiteral(strings.ToUpper(schemaName)))
	_, rows, err := db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
		return rows, err
	}
	return rows, nil
}

func (s *AdminGetTablesListService) Run() (responseData *[]map[string]any, err error) {
	var cfg InstanceCfg
	global.App.DB.Table("insight_instances").Where("`instance_id`=?", s.InstanceID).Take(&cfg)

	plainPassword, err := secret.Decrypt(cfg.Password)
	if err != nil {
		return nil, err
	}
	cfg.PlainPassword = plainPassword
	cfg.Schema = s.Schema
	// 产品特有实现优先，再按家族兜底；OceanBase 不单列分支 ——
	// MySQL 模式复用 getMySQLMetaData，Oracle 模式复用 getOracleMetaData。
	productType := normalizeDBType(cfg.DbType)
	if productType == "clickhouse" {
		return s.getClickHouseMetaData(&cfg)
	}
	if productType == "sqlserver" {
		return s.getSQLServerMetaData(&cfg)
	}
	if IsMySQLFamilyCfg(cfg.DbType, cfg.ExtraConfig) {
		return s.getMySQLMetaData(&cfg)
	}
	if IsPostgresFamilyCfg(cfg.DbType, cfg.ExtraConfig) {
		return s.getPostgresMetaData(&cfg)
	}
	if IsOracleFamilyCfg(cfg.DbType, cfg.ExtraConfig) {
		return s.getOracleMetaData(&cfg)
	}

	return responseData, fmt.Errorf("当前数据库类型暂不支持表权限元数据获取: %s", cfg.DbType)
}

// resolvePreservedFirstAt 计算一个授权主体（用户/角色）的「第一次授权时间」。
//
// 优先级：
//  1. 前端通过 preserve_first_granted_at 显式传回来的历史最早时间（用于「先删除老记录再新建」的编辑场景）
//  2. 现存 insight_das_schema_perms 中该主体的 MIN(created_at)
//  3. 以上都没有 → 返回当前 now（新主体，第一次授权）
//
// 解析失败或无效（0001-01-01 等） → 自动降级到下一级。
func resolvePreservedFirstAt(tx *gorm.DB, principalType string, principalID uint64, payloadEarliest *string, now commonModels.LocalTime) commonModels.LocalTime {
	if payloadEarliest != nil && *payloadEarliest != "" {
		if parsed, ok := tryParseLocalTime(*payloadEarliest); ok {
			return parsed
		}
	}
	var existingMin *commonModels.LocalTime
	if err := tx.Model(&models.InsightDasSchemaPerms{}).
		Where("principal_type=? and principal_id=?", principalType, principalID).
		Select("min(created_at)").
		Scan(&existingMin).Error; err == nil && existingMin != nil && !time.Time(*existingMin).IsZero() {
		return *existingMin
	}
	return now
}

// tryParseLocalTime 尝试从一个前端字符串（支持 2006-01-02 15:04:05 / 2006-01-02T15:04:05Z / 带时区偏移 等常见格式）解析为 LocalTime
func tryParseLocalTime(raw string) (commonModels.LocalTime, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return commonModels.LocalTime{}, false
	}
	patterns := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02 15:04:05.000000",
		"2006-01-02",
	}
	for _, layout := range patterns {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			y := t.Year()
			if y <= 1 || y >= 9999 {
				continue
			}
			return commonModels.LocalTime(t), true
		}
	}
	return commonModels.LocalTime{}, false
}

// applyCreatedAtToSchemaPerm 强制把 insight_das_schema_perms 记录的 CreatedAt 设置为 firstAt（同时兼容内部 *models.Model 指针是否已初始化）
func applyCreatedAtToSchemaPerm(rec *models.InsightDasSchemaPerms, firstAt commonModels.LocalTime) {
	if rec == nil {
		return
	}
	// Model 字段是嵌套指针，先确保它有对象可写
	if rec.Model == nil {
		rec.Model = &commonModels.Model{}
	}
	rec.Model.CreatedAt = firstAt
	rec.CreatedAt = firstAt
}

// applyCreatedAtToTablePerm 对 insight_das_table_perms 做同样的 created_at 强制赋值
func applyCreatedAtToTablePerm(rec *models.InsightDasTablePerms, firstAt commonModels.LocalTime) {
	if rec == nil {
		return
	}
	if rec.Model == nil {
		rec.Model = &commonModels.Model{}
	}
	rec.Model.CreatedAt = firstAt
	rec.CreatedAt = firstAt
}

type AdminCreateSchemasGrantService struct {
	*forms.AdminCreateSchemasGrantForm
	C *gin.Context
}

type adminSchemaGrantItem struct {
	Schema       string
	ResourceType string
	Tables       []string
	Rule         commonModels.EnumType
}

func (s *AdminCreateSchemasGrantService) normalizeInstanceIDs() ([]string, error) {
	rawIDs := make([]string, 0, len(s.InstanceIDs)+1)
	if len(s.InstanceIDs) > 0 {
		rawIDs = append(rawIDs, s.InstanceIDs...)
	} else if strings.TrimSpace(s.InstanceID) != "" {
		rawIDs = append(rawIDs, s.InstanceID)
	}
	if len(rawIDs) == 0 {
		return nil, fmt.Errorf("实例不能为空")
	}
	instanceIDs := make([]string, 0, len(rawIDs))
	seen := make(map[string]struct{}, len(rawIDs))
	for _, rawID := range rawIDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		instanceIDs = append(instanceIDs, id)
	}
	return instanceIDs, nil
}

func (s *AdminCreateSchemasGrantService) normalizeGrantItems() ([]adminSchemaGrantItem, error) {
	items := make([]adminSchemaGrantItem, 0)
	appendItem := func(schema, resourceType string, tables []string, rule string) error {
		schema = strings.TrimSpace(schema)
		resourceType = strings.TrimSpace(resourceType)
		if schema == "" {
			return fmt.Errorf("资源不能为空")
		}
		if resourceType == "" {
			return fmt.Errorf("资源类型不能为空")
		}
		enumRule := commonModels.EnumType("allow")
		if strings.EqualFold(rule, "deny") {
			enumRule = commonModels.EnumType("deny")
		}
		normalizedTables := make([]string, 0, len(tables))
		seen := make(map[string]struct{}, len(tables))
		for _, table := range tables {
			name := strings.TrimSpace(table)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			normalizedTables = append(normalizedTables, name)
		}
		if len(normalizedTables) == 0 {
			enumRule = commonModels.EnumType("allow")
		}
		items = append(items, adminSchemaGrantItem{
			Schema:       schema,
			ResourceType: resourceType,
			Tables:       normalizedTables,
			Rule:         enumRule,
		})
		return nil
	}

	if len(s.GrantItems) > 0 {
		for _, item := range s.GrantItems {
			if err := appendItem(item.Schema, item.ResourceType, item.Tables, item.Rule); err != nil {
				return nil, err
			}
		}
		return items, nil
	}

	if err := appendItem(s.Schema, s.ResourceType, s.Tables, s.Rule); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *AdminCreateSchemasGrantService) Run() (err error) {
	instanceIDs, err := s.normalizeInstanceIDs()
	if err != nil {
		return err
	}
	principalType := normalizeDASPrincipalType(s.PrincipalType)
	grantItems, err := s.normalizeGrantItems()
	if err != nil {
		return err
	}
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		compatKey, principalName, err := resolveGrantPrincipalSnapshot(tx, principalType, s.PrincipalID)
		if err != nil {
			return err
		}
		now := commonModels.LocalTime(time.Now())
		// 固定「第一次授权时间」：
		//   优先级 1：前端 preserve_first_granted_at 带回来的历史最早时间（先删后插场景）
		//   优先级 2：现存主体 MIN(created_at)（编辑时还剩记录的场景）
		//   优先级 3：now（真正的第一次授权）
		preservedFirstAt := resolvePreservedFirstAt(tx, principalType, s.PrincipalID, s.PreserveFirstGranted, now)
		touchAffectedSchemaPermIDs := make([]uint64, 0, len(instanceIDs)*len(grantItems))
		for _, instanceID := range instanceIDs {
			for _, item := range grantItems {
				schemaRecord := models.InsightDasSchemaPerms{
					PrincipalType: principalType,
					PrincipalID:   s.PrincipalID,
					PrincipalName: principalName,
					Username:      compatKey,
					Schema:        item.Schema,
					ResourceType:  item.ResourceType,
					InstanceID:    instanceID,
				}
				// 强制让新建记录的 created_at 继承主体最早授权时间（避免编辑=先删后插，创建时间刷新成新的）
				applyCreatedAtToSchemaPerm(&schemaRecord, preservedFirstAt)

				tableRecords := make([]models.InsightDasTablePerms, 0, len(item.Tables))
				for _, table := range item.Tables {
					tr := models.InsightDasTablePerms{
						PrincipalType: principalType,
						PrincipalID:   s.PrincipalID,
						PrincipalName: principalName,
						Username:      compatKey,
						Schema:        item.Schema,
						ResourceType:  item.ResourceType,
						InstanceID:    instanceID,
						Table:         table,
						Rule:          item.Rule,
					}
					applyCreatedAtToTablePerm(&tr, preservedFirstAt)
					tableRecords = append(tableRecords, tr)
				}

				var existingSchemaPerm models.InsightDasSchemaPerms
				schemaExists := false
				if err := tx.Model(&models.InsightDasSchemaPerms{}).
					Where("principal_type=? and principal_id=? and instance_id=? and `schema`=? and resource_type=?", principalType, s.PrincipalID, instanceID, item.Schema, item.ResourceType).
					Take(&existingSchemaPerm).Error; err == nil {
					schemaExists = true
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}

				if !schemaExists {
					if err := tx.Model(&models.InsightDasSchemaPerms{}).Create(&schemaRecord).Error; err != nil {
						var mysqlErr *mysql.MySQLError
						if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
							schemaExists = true
						} else {
							global.App.Log.Error(err)
							return err
						}
					} else {
						touchAffectedSchemaPermIDs = append(touchAffectedSchemaPermIDs, schemaRecord.ID)
					}
				}
				if schemaExists && existingSchemaPerm.ID > 0 {
					touchAffectedSchemaPermIDs = append(touchAffectedSchemaPermIDs, existingSchemaPerm.ID)
				}

				if len(tableRecords) == 0 {
					continue
				}

				var existingTableCount int64
				if err := tx.Model(&models.InsightDasTablePerms{}).
					Where("principal_type=? and principal_id=? and instance_id=? and `schema`=? and resource_type=?", principalType, s.PrincipalID, instanceID, item.Schema, item.ResourceType).
					Count(&existingTableCount).Error; err != nil {
					return err
				}

				if schemaExists && existingTableCount == 0 && item.Rule == "allow" {
					return fmt.Errorf("资源 `%s` 已存在全量查询授权，无需再配置白名单", item.Schema)
				}

				if len(tableRecords) > 0 {
					tableConflictColumns := []string{"principal_type", "principal_id", "instance_id", "`schema`", "resource_type", "`table`"}
					for i := range tableRecords {
						tableRecords[i].UpdatedAt = now
						if tableRecords[i].Model != nil {
							tableRecords[i].Model.UpdatedAt = now
						}
					}
					if err := tx.Clauses(clause.OnConflict{
						Columns:   make([]clause.Column, 0, len(tableConflictColumns)),
						DoNothing: true,
					}).
						Model(&models.InsightDasTablePerms{}).
						CreateInBatches(&tableRecords, len(tableRecords)).Error; err != nil {
						global.App.Log.Error(err)
						return err
					}
				}
			}
		}
		if len(touchAffectedSchemaPermIDs) > 0 {
			idSet := make(map[uint64]struct{}, len(touchAffectedSchemaPermIDs))
			deduped := make([]uint64, 0, len(touchAffectedSchemaPermIDs))
			for _, id := range touchAffectedSchemaPermIDs {
				if _, ok := idSet[id]; ok {
					continue
				}
				idSet[id] = struct{}{}
				deduped = append(deduped, id)
			}
			if err := tx.Model(&models.InsightDasSchemaPerms{}).
				Where("id in ?", deduped).
				UpdateColumn("updated_at", now).Error; err != nil {
				global.App.Log.Error(err)
				return err
			}
		}
		// 旧管理页的授权写入必须同步到新授权表，否则判定侧（只认新表）会看不到，
		// 表现为「界面上授权成功但用户依然访问不了」。
		if dataGrantEnabled() {
			operator := ""
			if s.C != nil {
				operator = s.C.GetString("username")
			}
			if err := ReconcileLegacyGrantsForPrincipal(tx, principalType, s.PrincipalID, operator); err != nil {
				global.App.Log.Error(err)
				return err
			}
		}
		return nil
	})
}

type AdminGetTablesGrantService struct {
	*forms.AdminGetTablesGrantForm
	C *gin.Context
}

func (s *AdminGetTablesGrantService) Run() (responseData any, total int64, err error) {
	var tables []models.InsightDasTablePerms
	tx := global.App.DB.Model(&models.InsightDasTablePerms{}).
		Where("principal_type=? and principal_id=? and instance_id=? and `schema`=?", normalizeDASPrincipalType(s.PrincipalType), s.PrincipalID, s.InstanceID, s.Schema).
		Order("created_at asc")
	// 搜索
	if s.Search != "" {
		tx = tx.Where("`table` like ? or `rule` like ?", "%"+s.Search+"%", "%"+s.Search+"%")
	}
	total = pagination.Pager(&s.PaginationQ, tx, &tables)
	return &tables, total, nil
}

type AdminCreateTablesGrantService struct {
	*forms.AdminCreateTablesGrantForm
	C *gin.Context
}

func (s *AdminCreateTablesGrantService) Run() (err error) {
	instance_id := strings.TrimSpace(s.InstanceID)
	if instance_id == "" {
		return fmt.Errorf("instance id is empty")
	}
	principalType := normalizeDASPrincipalType(s.PrincipalType)
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		compatKey, principalName, err := resolveGrantPrincipalSnapshot(tx, principalType, s.PrincipalID)
		if err != nil {
			return err
		}
		now := commonModels.LocalTime(time.Now())
		preservedFirstAt := resolvePreservedFirstAt(tx, principalType, s.PrincipalID, s.PreserveFirstGranted, now)
		tableRecords := make([]models.InsightDasTablePerms, 0, len(s.Tables))
		for _, table := range s.Tables {
			tr := models.InsightDasTablePerms{
				PrincipalType: principalType,
				PrincipalID:   s.PrincipalID,
				PrincipalName: principalName,
				Username:      compatKey,
				Schema:        s.Schema,
				ResourceType:  s.ResourceType,
				InstanceID:    instance_id,
				Table:         table,
				Rule:          commonModels.EnumType(s.Rule),
			}
			applyCreatedAtToTablePerm(&tr, preservedFirstAt)
			tableRecords = append(tableRecords, tr)
		}
		if err := tx.Model(&models.InsightDasTablePerms{}).CreateInBatches(&tableRecords, len(tableRecords)).Error; err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) {
				switch mysqlErr.Number {
				case 1062:
					return fmt.Errorf("记录已存在，错误:%s", err.Error())
				}
			}
			global.App.Log.Error(err)
			return err
		}
		return nil
	})
}

type AdminDeleteSchemasGrantService struct {
	C  *gin.Context
	ID uint32
}

func (s *AdminDeleteSchemasGrantService) Run() error {
	var schemaPerms models.InsightDasSchemaPerms
	tx := global.App.DB.Model(&models.InsightDasSchemaPerms{}).
		Where("id=?", s.ID).
		Take(&schemaPerms)
	if tx.RowsAffected == 0 {
		return errors.New("删除失败，影响行数为0")
	}
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id=?", s.ID).
			Delete(&models.InsightDasSchemaPerms{}).Error; err != nil {
			global.App.Log.Error(err)
			return err
		}
		if err := tx.Where("principal_type=? and principal_id=? and instance_id=? AND `schema`=?", schemaPerms.PrincipalType, schemaPerms.PrincipalID, schemaPerms.InstanceID, schemaPerms.Schema).
			Delete(&models.InsightDasTablePerms{}).Error; err != nil {
			global.App.Log.Error(err)
			return err
		}
		// 同步撤销新授权表里对应的记录（判定侧只认新表）
		if dataGrantEnabled() {
			operator := ""
			if s.C != nil {
				operator = s.C.GetString("username")
			}
			if err := ReconcileLegacyGrantsForPrincipal(tx, schemaPerms.PrincipalType, schemaPerms.PrincipalID, operator); err != nil {
				global.App.Log.Error(err)
				return err
			}
		}
		return nil
	})
}

type AdminDeleteTablesGrantService struct {
	C  *gin.Context
	ID uint32
}

func (s *AdminDeleteTablesGrantService) Run() error {
	var tablePerm models.InsightDasTablePerms
	if err := global.App.DB.Model(&models.InsightDasTablePerms{}).Where("id=?", s.ID).Take(&tablePerm).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("删除失败，记录不存在")
		}
		return err
	}
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id=?", s.ID).Delete(&models.InsightDasTablePerms{}).Error; err != nil {
			global.App.Log.Error(err)
			return err
		}
		// 同步撤销新授权表里对应的记录（判定侧只认新表）
		if dataGrantEnabled() {
			operator := ""
			if s.C != nil {
				operator = s.C.GetString("username")
			}
			if err := ReconcileLegacyGrantsForPrincipal(tx, tablePerm.PrincipalType, tablePerm.PrincipalID, operator); err != nil {
				global.App.Log.Error(err)
				return err
			}
		}
		return nil
	})
}
