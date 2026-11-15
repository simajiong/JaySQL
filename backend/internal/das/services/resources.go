package services

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/common/tasks"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"
	"gorm.io/gorm"
)

type accessibleInstanceResult struct {
	InstanceID string `json:"instance_id"`
	DbType     string `json:"db_type"`
	// DbFamily 解析后的引擎家族（小写）。前端据此决定按 MySQL 还是 Oracle 通道渲染/派发，
	// 对 OceanBase 尤其必要：db_type 只有 "OceanBase"，模式信息在 extra_config 里。
	// 非 OceanBase 类型时与 db_type 的小写形式一致。
	DbFamily         string `json:"db_family"`
	Hostname         string `json:"hostname"`
	Port             int    `json:"port"`
	Remark           string `json:"remark"`
	EnvironmentName  string `json:"environment_name"`
	EnvironmentColor string `json:"environment_color"`
	LastCheckStatus  string `json:"last_check_status"`
	LastCheckMessage string `json:"last_check_message"`
	// ExtraConfig 只用于本地解析家族，不对外暴露（json:"-"）。
	ExtraConfig []byte `gorm:"column:extra_config" json:"-"`
}

type resourceListResult struct {
	InstanceID   string `json:"instance_id"`
	Schema       string `json:"schema"`
	ResourcePath string `json:"resource_path"`
	ParentPath   string `json:"parent_path"`
	ResourceType string `json:"resource_type"`
	DatabaseName string `json:"database_name"`
	SchemaName   string `json:"schema_name"`
	DisplayName  string `json:"display_name"`
	DbType       string `json:"db_type"`
	DbFamily     string `json:"db_family"`
	Hostname     string `json:"hostname"`
	Port         int    `json:"port"`
	Remark       string `json:"remark"`
	IsDeleted    bool   `json:"is_deleted"`
	// SizeBytes 库 / Schema 的数据量（字节）。nil = 该引擎暂不支持统计，前端不展示。
	SizeBytes *int64 `json:"size_bytes"`
}

func trimDatabasePrefix(text, databaseName string) string {
	value := strings.TrimSpace(text)
	database := strings.TrimSpace(databaseName)
	if value == "" || database == "" {
		return value
	}
	separators := []string{" / ", "/", ".", " · ", ": "}
	for _, separator := range separators {
		prefix := database + separator
		if strings.HasPrefix(value, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(value, prefix))
		}
	}
	return value
}

func deriveDatabaseAndSchemaNames(resourcePath, parentPath, displayName string) (string, string, string) {
	path := strings.TrimSpace(resourcePath)
	databaseName := strings.TrimSpace(parentPath)
	schemaName := strings.TrimSpace(displayName)

	if databaseName != "" {
		schemaName = trimDatabasePrefix(schemaName, databaseName)
	}
	if schemaName == "" || schemaName == path {
		schemaName = trimDatabasePrefix(path, databaseName)
	}
	if schemaName == "" || schemaName == path {
		parts := strings.SplitN(path, ".", 2)
		if len(parts) == 2 {
			if databaseName == "" {
				databaseName = strings.TrimSpace(parts[0])
			}
			schemaName = strings.TrimSpace(parts[1])
		}
	}
	if databaseName == "" && path != "" && schemaName != "" && schemaName != path {
		separators := []string{" / ", "/", ".", " · ", ": "}
		for _, separator := range separators {
			prefix := separator + schemaName
			if strings.HasSuffix(path, prefix) {
				databaseName = strings.TrimSpace(strings.TrimSuffix(path, prefix))
				break
			}
		}
	}
	if schemaName == "" {
		schemaName = path
	}
	return databaseName, schemaName, schemaName
}

func hydrateStructuredResourceFields(item *resourceListResult) {
	if item == nil {
		return
	}
	item.ResourcePath = item.Schema

	displayName := strings.TrimSpace(item.DisplayName)
	// 优先用已解析的家族；缺失时回落到 db_type（OceanBase 会按 MySQL 模式处理）。
	family := strings.TrimSpace(item.DbFamily)
	if family == "" {
		family = normalizeDBType(item.DbType)
	}

	switch {
	case family == "postgresql" || family == "sqlserver":
		if item.ResourceType == "database" {
			item.DatabaseName = strings.TrimSpace(item.Schema)
			item.SchemaName = ""
			if displayName == "" {
				displayName = item.DatabaseName
			}
		} else if item.ResourceType == "schema" {
			databaseName, schemaName, normalizedDisplayName := deriveDatabaseAndSchemaNames(item.Schema, item.ParentPath, displayName)
			item.DatabaseName = databaseName
			item.SchemaName = schemaName
			displayName = normalizedDisplayName
		}
	case family == "oracle":
		item.SchemaName = strings.TrimSpace(item.Schema)
		if displayName == "" {
			displayName = item.SchemaName
		}
	default:
		item.DatabaseName = strings.TrimSpace(item.Schema)
		if displayName == "" {
			displayName = item.DatabaseName
		}
	}

	item.DisplayName = displayName
}

type GetAccessibleInstancesService struct {
	C        *gin.Context
	Username string
}

func (s *GetAccessibleInstancesService) Run() ([]accessibleInstanceResult, error) {
	scope, err := resolveDASActorScope(s.Username)
	if err != nil {
		return nil, err
	}
	// 【2026-10-15 收口】不再按超管身份跳过过滤，admin 同样只在被授权的实例上可见。
	if !dataGrantEnabled() {
		// 迁移前兼容路径：旧表显式授权行（不再做任何隐式补齐）
		ensureSelfServiceGrantsForScope(scope)
	}
	var results []accessibleInstanceResult
	tx := global.App.DB.Table("insight_instances i").
		Select(`
			i.instance_id,
			i.db_type,
			i.extra_config,
			i.hostname,
			i.port,
			i.remark,
			ifnull(env.name, '') as environment_name,
			ifnull(env.color, '') as environment_color,
			ifnull(i.last_check_status, 'unknown') as last_check_status,
			ifnull(i.last_check_message, '') as last_check_message
		`).
		Joins("left join insight_instance_environments env on env.id = i.environment")
	if dataGrantEnabled() {
		// 新模型：只按 instance_id 过滤（实例上有一条有效授权即可见），
		// 不再 join 旧授权表——否则只有新表授权、旧表无行的用户会一条都看不到。
		tx = applyDataGrantInstanceVisibilityFilter(tx, "i", scope)
	} else {
		tx = tx.Joins("join insight_das_schema_perms p on p.instance_id = i.instance_id")
		tx = applyPrincipalFilter(tx, "p", scope)
	}
	err = tx.Distinct().
		Order("i.remark asc, i.hostname asc, i.port asc").
		Scan(&results).Error
	// db_family 是给前端做「按家族渲染/派发」用的派生字段：
	// OceanBase 的 db_type 无法区分 MySQL / Oracle 模式，家族必须由后端算好再下发，
	// 避免前端各页面重复解析 extra_config。
	for i := range results {
		results[i].DbFamily = resolveFamily(results[i].DbType, results[i].ExtraConfig)
	}
	return results, err
}

// filterSystemSchemaResources 按系统库可见性规则过滤资源树结果（原地压缩，不额外分配底层数组）。
//
// gate 为 nil（admin / 未知主体）时原样返回，不做任何收敛。
func filterSystemSchemaResources(gate *systemSchemaGate, items []resourceListResult) []resourceListResult {
	if gate == nil || len(items) == 0 {
		return items
	}
	kept := items[:0]
	for i := range items {
		if gate.hidden(items[i].InstanceID, items[i].DbType, items[i].Schema) {
			continue
		}
		kept = append(kept, items[i])
	}
	return kept
}

type GetResourcesService struct {
	*forms.GetResourcesForm
	C        *gin.Context
	Username string
}

func (s *GetResourcesService) getInstanceFamily() (string, error) {
	type instanceType struct {
		DbType      string `json:"db_type"`
		ExtraConfig []byte `gorm:"column:extra_config" json:"-"`
	}
	var result instanceType
	err := global.App.DB.Table("insight_instances").
		Select("db_type, extra_config").
		Where("instance_id = ?", s.InstanceID).
		Take(&result).Error
	if err != nil {
		return "", err
	}
	return resolveFamily(result.DbType, result.ExtraConfig), nil
}

func (s *GetResourcesService) baseQuery() *gorm.DB {
	return global.App.DB.Table("insight_instance_schemas m").
		Select("m.instance_id, m.schema, m.parent_path, m.resource_type, m.display_name, i.db_type, i.hostname, i.port, i.remark, m.is_deleted").
		Joins("join insight_instances i on m.instance_id = i.instance_id").
		Where("m.instance_id = ? and m.is_deleted = 0", s.InstanceID)
}

func (s *GetResourcesService) Run() ([]resourceListResult, error) {
	scope, err := resolveDASActorScope(s.Username)
	if err != nil {
		return nil, err
	}
	family, err := s.getInstanceFamily()
	if err != nil {
		return nil, err
	}
	instanceUUID := strings.TrimSpace(s.InstanceID)
	// 读时惰性刷新：资源树是「新建库后最容易发现看不到」的入口，
	// 快照过期时在这里异步补刷（不阻塞本次请求，只影响下一次展开）。
	tasks.EnsureFresh(instanceUUID)
	instanceGranted, err := hasDASInstanceGrant(scope, instanceUUID)
	if err != nil {
		return nil, err
	}

	tx := s.baseQuery()
	if s.ParentPath != "" {
		tx = tx.Where("m.parent_path = ?", s.ParentPath)
	} else {
		tx = tx.Where("m.parent_path = ''")
	}
	tx = tx.Where("m.resource_type = ?", s.ResourceType)

	if !instanceGranted {
		if dataGrantEnabled() {
			// 新模型：直接用显式授权做 EXISTS 过滤（实例级/库级/表级都能覆盖到），
			// 不再按数据库家族 join 不同的旧授权表。
			tx = applyDataGrantVisibilityFilter(tx, "m", scope)
		} else {
			// 迁移前兼容：授权过滤按「家族」分支（OceanBase 已由 resolveFamily 落到 mysql / oracle，
			// 因此这里不需要再单独为 OceanBase 开分支）。
			switch {
			case family == "mysql", family == "clickhouse", family == "oracle":
				tx = tx.Joins("join insight_das_schema_perms p on p.instance_id = m.instance_id and p.schema = m.schema and p.resource_type = m.resource_type")
				tx = applyPrincipalFilter(tx, "p", scope)
			case family == "postgresql", family == "sqlserver":
				if s.ResourceType == "database" {
					tx = tx.Joins("join insight_instance_schemas c on c.instance_id = m.instance_id and c.parent_path = m.schema and c.resource_type = 'schema' and c.is_deleted = 0").
						Joins("join insight_das_schema_perms p on p.instance_id = c.instance_id and p.schema = c.schema and p.resource_type = c.resource_type")
					tx = applyPrincipalFilter(tx, "p", scope)
				} else {
					tx = tx.Joins("join insight_das_schema_perms p on p.instance_id = m.instance_id and p.schema = m.schema and p.resource_type = m.resource_type")
					tx = applyPrincipalFilter(tx, "p", scope)
				}
			default:
				return nil, fmt.Errorf("当前数据库类型暂不支持资源分层查询: %s", family)
			}
		}
	}

	var results []resourceListResult
	err = tx.Distinct().
		Order("m.display_name asc, m.`schema` asc").
		Scan(&results).Error
	// 【2026-10-15】全实例授权 + 非 admin：把引擎自带系统库从可见列表里剔除。
	// 放在结果侧而不是 SQL 里，是因为「要不要剔除」取决于引擎家族，
	// 而家族对 OceanBase 还要结合租户模式——SQL 里表达不出来。
	if len(results) > 0 {
		results = filterSystemSchemaResources(newSystemSchemaGate(scope), results)
	}
	for i := range results {
		// 家族统一在此注入：hydrateStructuredResourceFields 依赖它决定 database / schema 归属，
		// 避免它在只有 db_type 的情况下把 OceanBase Oracle 模式错认成 MySQL 风格。
		if results[i].DbFamily == "" {
			results[i].DbFamily = family
		}
		hydrateStructuredResourceFields(&results[i])
	}
	// 体量数据：按引擎能力尽力取，失败或无能力时保持 nil（前端不渲染大小）
	if sizes := loadResourceSizes(s.C.Request.Context(), instanceUUID, family, results); !sizes.empty() {
		for i := range results {
			var (
				value int64
				ok    bool
			)
			// 库级与模式级分开取：PostgreSQL 的库名与「库.模式」在同一张表里，
			// 混在一个 map 里会撞键。
			if results[i].ResourceType == "database" {
				value, ok = sizes.databaseSize(results[i].Schema)
			} else {
				value, ok = sizes.schemaSize(&results[i])
			}
			if ok {
				size := value
				results[i].SizeBytes = &size
			}
		}
	}
	return results, err
}

const (
	DefaultFlatIndexMaxRows = 20000
	DefaultSearchLimit      = 200
)

type FlatObject struct {
	Type         string `json:"type"`
	DatabaseName string `json:"database_name"`
	SchemaName   string `json:"schema_name"`
	TableName    string `json:"table_name"`
	DisplayName  string `json:"display_name"`
	Remark       string `json:"remark"`
	TableComment string `json:"table_comment"`
	TableRows    any    `json:"table_rows"`
	DataLengthKB any    `json:"data_length_kb"`
	Engine       string `json:"engine"`
	UpdateTime   string `json:"update_time"`
	ResourcePath string `json:"resource_path"`
	SchemaPath   string `json:"schema_path"`
}

type FlatIndexResult struct {
	TooLarge   bool         `json:"too_large"`
	TotalCount int          `json:"total_count"`
	Items      []FlatObject `json:"items"`
}

type SearchHitResult struct {
	Total int          `json:"total"`
	Items []FlatObject `json:"items"`
}

type GetObjectsFlatIndexService struct {
	*forms.GetObjectsFlatIndexForm
	C        *gin.Context
	Username string
}

func (s *GetObjectsFlatIndexService) getInstanceFamily() (string, error) {
	type instanceType struct {
		DbType      string `json:"db_type"`
		ExtraConfig []byte `gorm:"column:extra_config" json:"-"`
	}
	var result instanceType
	err := global.App.DB.Table("insight_instances").
		Select("db_type, extra_config").
		Where("instance_id = ?", s.InstanceID).
		Take(&result).Error
	if err != nil {
		return "", err
	}
	return resolveFamily(result.DbType, result.ExtraConfig), nil
}

func (s *GetObjectsFlatIndexService) maxRows() int {
	if s.MaxRows <= 0 {
		return DefaultFlatIndexMaxRows
	}
	return s.MaxRows
}

type schemaUnit struct {
	SchemaPath   string
	DatabaseName string
	SchemaName   string
	DbType       string
}

func (s *GetObjectsFlatIndexService) listAccessibleSchemas() ([]schemaUnit, error) {
	_, err := resolveDASActorScope(s.Username)
	if err != nil {
		return nil, err
	}
	family, err := s.getInstanceFamily()
	if err != nil {
		return nil, err
	}

	databaseSvc := GetResourcesService{
		GetResourcesForm: &forms.GetResourcesForm{
			InstanceID:   s.InstanceID,
			ResourceType: "database",
		},
		C:        s.C,
		Username: s.Username,
	}
	databases, err := databaseSvc.Run()
	if err != nil {
		return nil, err
	}

	units := make([]schemaUnit, 0, len(databases))

	switch {
	case family == "postgresql" || family == "sqlserver":
		schemaSvc := GetResourcesService{
			GetResourcesForm: &forms.GetResourcesForm{
				InstanceID:   s.InstanceID,
				ResourceType: "schema",
			},
			C:        s.C,
			Username: s.Username,
		}
		schemas, err := schemaSvc.Run()
		if err != nil {
			return nil, err
		}
		for _, sch := range schemas {
			hydrateStructuredResourceFields(&sch)
			units = append(units, schemaUnit{
				SchemaPath:   sch.Schema,
				DatabaseName: sch.DatabaseName,
				SchemaName:   sch.SchemaName,
				DbType:       sch.DbType,
			})
		}
	default:
		for _, db := range databases {
			hydrateStructuredResourceFields(&db)
			units = append(units, schemaUnit{
				SchemaPath:   db.Schema,
				DatabaseName: db.DatabaseName,
				SchemaName:   "",
				DbType:       db.DbType,
			})
		}
	}
	return units, nil
}

type flatIndexCollector struct {
	items      []FlatObject
	totalCount int
	tooLarge   bool
	maxRows    int
}

func (c *flatIndexCollector) add(obj FlatObject) (stop bool) {
	c.totalCount++
	if c.maxRows > 0 && c.totalCount > c.maxRows {
		c.tooLarge = true
		c.items = nil
		return true
	}
	if !c.tooLarge {
		c.items = append(c.items, obj)
	}
	return false
}

func (s *GetObjectsFlatIndexService) Run() (*FlatIndexResult, error) {
	maxRows := s.maxRows()
	collector := &flatIndexCollector{maxRows: maxRows, items: make([]FlatObject, 0, maxRows)}

	units, err := s.listAccessibleSchemas()
	if err != nil {
		return nil, err
	}

	var instanceCfg InstanceCfg
	err = global.App.DB.Table("insight_instances").Where("instance_id=?", s.InstanceID).Take(&instanceCfg).Error
	if err != nil {
		return nil, err
	}
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}
	instanceCfg.PlainPassword = plainPassword

	databaseSeen := make(map[string]struct{})
	schemaSeen := make(map[string]struct{})
	instanceUUID := strings.TrimSpace(s.InstanceID)

	for _, unit := range units {
		if collector.tooLarge {
			break
		}
		if unit.DatabaseName != "" {
			if _, ok := databaseSeen[unit.DatabaseName]; !ok {
				databaseSeen[unit.DatabaseName] = struct{}{}
				if collector.add(FlatObject{
					Type:         "database",
					DatabaseName: unit.DatabaseName,
					DisplayName:  unit.DatabaseName,
					ResourcePath: unit.DatabaseName,
					SchemaPath:   "",
				}) {
					break
				}
			}
		}
		if unit.SchemaName != "" {
			key := unit.DatabaseName + "|" + unit.SchemaName
			if _, ok := schemaSeen[key]; !ok {
				schemaSeen[key] = struct{}{}
				if collector.add(FlatObject{
					Type:         "schema",
					DatabaseName: unit.DatabaseName,
					SchemaName:   unit.SchemaName,
					DisplayName:  unit.SchemaName,
					ResourcePath: unit.SchemaPath,
					SchemaPath:   unit.SchemaPath,
				}) {
					break
				}
			}
		}

		tblSvc := GetTablesService{
			GetTablesForm: &forms.GetTablesForm{
				InstanceID: s.InstanceID,
				Schema:     unit.SchemaPath,
			},
			C:        s.C,
			Username: s.Username,
		}
		if err = tblSvc.validatePerms(instanceUUID); err != nil {
			continue
		}
		rows, err := tblSvc.Run()
		if err != nil || rows == nil {
			continue
		}
		for _, row := range *rows {
			if collector.tooLarge {
				break
			}
			tableName := getStringField(row, "table_name")
			if tableName == "" || shouldSkipGhostTable(tableName) {
				continue
			}
			tableComment := getStringField(row, "table_comment")
			engine := getStringField(row, "engine")
			updateTime := getStringField(row, "update_time")
			if collector.add(FlatObject{
				Type:         "table",
				DatabaseName: unit.DatabaseName,
				SchemaName:   unit.SchemaName,
				TableName:    tableName,
				DisplayName:  tableName,
				TableComment: tableComment,
				TableRows:    row["table_rows"],
				DataLengthKB: row["data_length_kb"],
				Engine:       engine,
				UpdateTime:   updateTime,
				ResourcePath: unit.SchemaPath + "/" + tableName,
				SchemaPath:   unit.SchemaPath,
			}) {
				break
			}
		}
	}

	return &FlatIndexResult{
		TooLarge:   collector.tooLarge,
		TotalCount: collector.totalCount,
		Items:      collector.items,
	}, nil
}

type SearchObjectsService struct {
	*forms.SearchObjectsForm
	C        *gin.Context
	Username string
}

func (s *SearchObjectsService) limit() int {
	if s.Limit <= 0 {
		return DefaultSearchLimit
	}
	return s.Limit
}

func matchesKeyword(obj *FlatObject, keyword string) bool {
	kw := strings.ToLower(keyword)
	if kw == "" {
		return false
	}
	dbName := strings.ToLower(obj.DatabaseName)
	scName := strings.ToLower(obj.SchemaName)
	tbName := strings.ToLower(obj.TableName)
	dispName := strings.ToLower(obj.DisplayName)
	isDispDistinct := dispName != dbName && dispName != scName && dispName != tbName
	return (dbName != "" && strings.Contains(dbName, kw)) ||
		(scName != "" && strings.Contains(scName, kw)) ||
		(tbName != "" && strings.Contains(tbName, kw)) ||
		(isDispDistinct && dispName != "" && strings.Contains(dispName, kw))
}

func (s *SearchObjectsService) Run() (*SearchHitResult, error) {
	flatSvc := GetObjectsFlatIndexService{
		GetObjectsFlatIndexForm: &forms.GetObjectsFlatIndexForm{
			InstanceID: s.InstanceID,
			MaxRows:    DefaultFlatIndexMaxRows,
		},
		C:        s.C,
		Username: s.Username,
	}
	index, err := flatSvc.Run()
	if err != nil {
		return nil, err
	}

	out := make([]FlatObject, 0, s.limit())
	total := 0
	if !index.TooLarge {
		for i := range index.Items {
			if matchesKeyword(&index.Items[i], s.Query) {
				total++
				if len(out) < cap(out) {
					out = append(out, index.Items[i])
				}
			}
		}
		return &SearchHitResult{Total: total, Items: out}, nil
	}

	units, err := flatSvc.listAccessibleSchemas()
	if err != nil {
		return nil, err
	}
	var instanceCfg InstanceCfg
	if err = global.App.DB.Table("insight_instances").Where("instance_id=?", s.InstanceID).Take(&instanceCfg).Error; err != nil {
		return nil, err
	}
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}
	instanceCfg.PlainPassword = plainPassword
	instanceUUID := strings.TrimSpace(s.InstanceID)
	limit := s.limit()

	collectTotal := 0
	databaseSeen := make(map[string]struct{})
	schemaSeen := make(map[string]struct{})

	checkDatabaseAndSchema := func(unit schemaUnit) (full bool) {
		if unit.DatabaseName != "" {
			if _, ok := databaseSeen[unit.DatabaseName]; !ok {
				obj := FlatObject{
					Type:         "database",
					DatabaseName: unit.DatabaseName,
					DisplayName:  unit.DatabaseName,
					ResourcePath: unit.DatabaseName,
				}
				if matchesKeyword(&obj, s.Query) {
					collectTotal++
					databaseSeen[unit.DatabaseName] = struct{}{}
					if len(out) < limit {
						out = append(out, obj)
					}
				} else {
					databaseSeen[unit.DatabaseName] = struct{}{}
				}
			}
		}
		if unit.SchemaName != "" {
			key := unit.DatabaseName + "|" + unit.SchemaName
			if _, ok := schemaSeen[key]; !ok {
				obj := FlatObject{
					Type:         "schema",
					DatabaseName: unit.DatabaseName,
					SchemaName:   unit.SchemaName,
					DisplayName:  unit.SchemaName,
					ResourcePath: unit.SchemaPath,
					SchemaPath:   unit.SchemaPath,
				}
				if matchesKeyword(&obj, s.Query) {
					collectTotal++
					schemaSeen[key] = struct{}{}
					if len(out) < limit {
						out = append(out, obj)
					}
				} else {
					schemaSeen[key] = struct{}{}
				}
			}
		}
		return len(out) >= limit
	}

	for _, unit := range units {
		if len(out) >= limit {
			break
		}
		if checkDatabaseAndSchema(unit) {
			continue
		}
		tblSvc := GetTablesService{
			GetTablesForm: &forms.GetTablesForm{InstanceID: s.InstanceID, Schema: unit.SchemaPath},
			C:             s.C,
			Username:      s.Username,
		}
		if err = tblSvc.validatePerms(instanceUUID); err != nil {
			continue
		}
		rows, err := tblSvc.Run()
		if err != nil || rows == nil {
			continue
		}
		for _, row := range *rows {
			if len(out) >= limit && collectTotal >= limit {
				break
			}
			tableName := getStringField(row, "table_name")
			if tableName == "" || shouldSkipGhostTable(tableName) {
				continue
			}
			obj := FlatObject{
				Type:         "table",
				DatabaseName: unit.DatabaseName,
				SchemaName:   unit.SchemaName,
				TableName:    tableName,
				DisplayName:  tableName,
				TableComment: getStringField(row, "table_comment"),
				TableRows:    row["table_rows"],
				DataLengthKB: row["data_length_kb"],
				Engine:       getStringField(row, "engine"),
				UpdateTime:   getStringField(row, "update_time"),
				ResourcePath: unit.SchemaPath + "/" + tableName,
				SchemaPath:   unit.SchemaPath,
			}
			if matchesKeyword(&obj, s.Query) {
				collectTotal++
				if len(out) < limit {
					out = append(out, obj)
				}
			}
		}
	}
	return &SearchHitResult{Total: collectTotal, Items: out}, nil
}
