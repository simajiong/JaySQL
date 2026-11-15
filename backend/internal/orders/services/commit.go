package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/pagination"
	"github.com/simajiong/jaysql/pkg/parser"
	"github.com/simajiong/jaysql/pkg/secret"
	"github.com/simajiong/jaysql/pkg/sqlsplit"
	"github.com/simajiong/jaysql/pkg/utils"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	commonTasks "github.com/simajiong/jaysql/internal/common/tasks"
	dasServices "github.com/simajiong/jaysql/internal/das/services"
	"github.com/simajiong/jaysql/internal/inspect/checker"
	"github.com/simajiong/jaysql/internal/orders/forms"
	"github.com/simajiong/jaysql/internal/orders/models"
	orderScopes "github.com/simajiong/jaysql/internal/orders/scopes"
	usersModels "github.com/simajiong/jaysql/internal/users/models"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// 获取环境
type GetOrderEnvironmentsService struct {
	C *gin.Context
}

func (s *GetOrderEnvironmentsService) Run() ([]commonModels.InsightInstanceEnvironments, error) {
	var results []commonModels.InsightInstanceEnvironments
	global.App.DB.Table("`insight_instance_environments` a").
		Select("a.`name`, a.id").
		Scan(&results)
	return results, nil
}

// 获取指定环境的实例
type GetOrderInstancesService struct {
	*forms.GetOrderInstancesForm
	C        *gin.Context
	Username string
}

type OrderDataSourceItem struct {
	ID               uint64                 `json:"id"`
	InstanceID       string                 `json:"instance_id"`
	Remark           string                 `json:"remark"`
	Hostname         string                 `json:"hostname"`
	Port             int                    `json:"port"`
	DBType           commonModels.EnumType  `json:"db_type"`
	Environment      int                    `json:"environment"`
	EnvironmentName  string                 `json:"environment_name"`
	LastCheckedAt    commonModels.LocalTime `json:"last_checked_at"`
	LastCheckStatus  string                 `json:"last_check_status"`
	LastCheckMessage string                 `json:"last_check_message"`
}

type GetOrderDataSourcesService struct {
	*forms.GetOrderDataSourcesForm
	C        *gin.Context
	Username string
}

// 【2026-10-24 功能权限口径】工单的候选列表不再按数据访问授权收敛。
//
// 这是既有决策的收尾，不是新规则：2026-10-23 起工单执行链路已不做数据授权校验
// （变更/导出由工单审批覆盖，见 execute_tasks.go 与 syntax.go），
// 项目既定口径是「数据授权只约束 das 查询接口」（das/services/access_action_scope.go）。
//
// 落到工单上就是：功能权限 order.submit 决定能不能提交，数据授权
// （insight_das_data_grants）不参与。此前候选列表仍按授权过滤，
// 造成「提交接口放行、下拉里却选不到」的自相矛盾，现在补齐。
// 与性能分析（performance.view，2026-10-24 同批解耦）口径一致。
func (s *GetOrderDataSourcesService) Run() (responseData interface{}, total int64, err error) {
	var dataSources []OrderDataSourceItem
	tx := global.App.DB.Table("insight_instances a").
		Select("a.id, a.instance_id, a.remark, a.hostname, a.port, a.db_type, a.environment, b.name as environment_name, a.last_checked_at, a.last_check_status, a.last_check_message").
		Joins("left join insight_instance_environments b on a.environment = b.id")
	// 工单数据源候选：直接引用工单支持的库类型白名单（scopes.SupportedOrderDbTypes），
	// 不再本地另写一份。历史 bug：这里漏了 Kingbase，金仓实例在「新建工单 → 数据源」
	// 下拉里显示「暂无数据」，而工单列表侧的白名单也各少一份，导致建好的单凭空消失。
	tx = tx.Where("a.db_type in ?", orderScopes.SupportedOrderDbTypes)
	tx = tx.Order("b.name asc, a.db_type asc, a.remark asc, a.hostname asc, a.port asc")
	total = pagination.Pager(&s.PaginationQ, tx, &dataSources)
	return &dataSources, total, nil
}

func (s *GetOrderInstancesService) Run() (responseData interface{}, total int64, err error) {
	var instances []commonModels.InsightInstances
	tx := global.App.DB.Table("insight_instances").
		Where("environment=? and db_type=?", s.ID, s.DbType)
	total = pagination.Pager(&s.PaginationQ, tx, &instances)
	return &instances, total, nil
}

// 获取指定实例的Schemas
type GetOrderSchemasService struct {
	*forms.GetOrderSchemasForm
	C        *gin.Context
	Username string
}

func (s *GetOrderSchemasService) Run() (responseData interface{}, total int64, err error) {
	// 口径（2026-10-24 定稿）：
	//   - 能不能提交工单由功能权限 order.submit 决定，数据授权不参与（见 GetOrderDataSourcesService）；
	//   - 但系统库（information_schema / mysql / sys / pg_catalog …）不进入工单视野：
	//     下拉对非 admin 隐藏，与提交校验（rejectOrderSystemTarget）同一口径。
	// 过滤必须下推到 SQL：分页计数若不含过滤，会出现「total 100 条、翻页全是空页」。
	//
	// 2026-11-09 读时惰性刷新：建库成功后平台会主动失效该实例快照，
	// 但「DBA 用客户端直接建库」这类变更平台无感知，所以读之前补一道 TTL 校验。
	// 这里只是异步触发，不等待结果，接口耗时不受影响。
	commonTasks.EnsureFresh(s.InstanceID)
	var roles []commonModels.InsightInstanceSchemas
	tx := global.App.DB.Table("insight_instance_schemas").Where("instance_id=? and is_deleted=0", s.InstanceID)
	if s.ParentPath != "" {
		tx = tx.Where("parent_path=?", s.ParentPath)
	} else {
		tx = tx.Where("parent_path=''")
	}
	if s.ResourceType != "" {
		tx = tx.Where("resource_type=?", s.ResourceType)
	}
	if dbType, dbErr := orderInstanceDBType(s.InstanceID); dbErr == nil &&
		!dasServices.HasBuiltinAdminRoleByUsername(s.Username) {
		tx = applySystemSchemaExclusion(tx, dbType)
	}
	tx = tx.Order("display_name asc, `schema` asc")
	total = pagination.Pager(&s.PaginationQ, tx, &roles)
	return &roles, total, nil
}

// orderInstanceDBType 取实例的 db_type：系统库清单按引擎家族取（MySQL/PG/Oracle/TiDB 各不同）。
func orderInstanceDBType(instanceID string) (string, error) {
	var row struct {
		DbType string
	}
	if err := global.App.DB.Table("insight_instances").
		Select("db_type").Where("instance_id=?", instanceID).Take(&row).Error; err != nil {
		return "", err
	}
	return row.DbType, nil
}

// applySystemSchemaExclusion 把系统库过滤下推为 SQL 条件（名称 NOT IN + 前缀/后缀 NOT LIKE）。
// 一律小写比较：清单存小写，Oracle 的 SYS/SYSTEM 等大写账号靠 LOWER 归一命中。
func applySystemSchemaExclusion(tx *gorm.DB, dbType string) *gorm.DB {
	names, prefixes, suffixes := dasServices.SystemSchemaFilterTerms(dbType)
	expr := "LOWER(TRIM(`schema`))"
	if len(names) > 0 {
		tx = tx.Where(expr+" NOT IN ?", names)
	}
	for _, prefix := range prefixes {
		tx = tx.Where(expr+" NOT LIKE ?", prefix+"%")
	}
	for _, suffix := range suffixes {
		tx = tx.Where(expr+" NOT LIKE ?", "%"+suffix)
	}
	return tx
}

// 获取审核/复核/抄送人
type GetOrderUsersService struct {
	*forms.GetOrderUsersForm
	C *gin.Context
}

func resolvePermCodes(scope string) []string {
	switch scope {
	case "approver":
		return []string{"order.approve", "order.manage"}
	case "executor":
		return []string{"order.execute", "order.manage"}
	default:
		return nil
	}
}

func (s *GetOrderUsersService) Run() (responseData interface{}, total int64, err error) {
	var roles []usersModels.InsightUsers
	tx := global.App.DB.Table("insight_users u").Where("u.is_active=1 AND IFNULL(u.is_deleted,0)=0")

	if codes := resolvePermCodes(s.PermScope); len(codes) > 0 {
		subQuery := global.App.DB.Table("insight_permissions p").
			Select("1").
			Where("p.is_enabled = 1 AND p.code IN ?", codes).
			Where(`(
				EXISTS (
					SELECT 1 FROM insight_user_permissions up
					WHERE up.uid = u.uid AND up.permission_id = p.id
				)
				OR EXISTS (
					SELECT 1
					FROM insight_user_roles ur
					JOIN insight_roles r ON ur.role_id = r.id
					JOIN insight_role_permissions rp ON ur.role_id = rp.role_id
					WHERE ur.uid = u.uid AND r.is_deleted = 0 AND rp.permission_id = p.id
				)
			)`)
		tx = tx.Where("u.is_superuser = 1 OR EXISTS (?)", subQuery)
	}

	total = pagination.Pager(&s.PaginationQ, tx, &roles)
	return &roles, total, nil
}

// 提交工单
type CreateOrderService struct {
	*forms.CreateOrderForm
	C        *gin.Context
	Username string
	Audit    *parser.TiStmt
}

// isPgLikeDB schema 与 database 分离的库类型（PG / 金仓 / SQL Server）。
// 注意别只按 parser 口径理解：inspectConnTarget / operate / execute_tasks
// 都靠它区分「库字段、schema 字段是否独立维护」。
func isPgLikeDB(dbType string) bool {
	switch strings.ToLower(strings.TrimSpace(dbType)) {
	case "postgresql", "postgres", "kingbase", "kingbasees", "sqlserver":
		return true
	default:
		return false
	}
}

// isSQLServerDB SQL Server（含别名 MSSQL）走独立拆分链路：
// T-SQL 方言不适用 TiDB parser，且需剥离 GO 批处理分隔符（order_plan.go）
func isSQLServerDB(dbType string) bool {
	switch strings.ToLower(strings.TrimSpace(dbType)) {
	case "sqlserver", "mssql":
		return true
	default:
		return false
	}
}

// rejectOrderSystemTarget 工单目标不得指向系统库/schema（非管理员）。
//
// 为什么不做「显式授权豁免」：工单下拉（GetOrderSchemasService）对非 admin
// 无条件隐藏系统库，这里必须同一口径——两处都对齐「非 admin = 不可见 + 不可提交」，
// 否则又会出现「看得到、提交不了」的半套规则。管理员（admin 角色）放行。
// 已知限制：只校验表单的目标库/schema 字段；SQL 文本里内嵌的 USE 系统库、
// CREATE DATABASE 系统库名，由审批链与 SQL 审核规范兜底，不在本次拦截范围。
func (s *CreateOrderService) rejectOrderSystemTarget(dbType string) error {
	if dasServices.HasBuiltinAdminRoleByUsername(s.Username) {
		return nil
	}
	if s.Database != "" && dasServices.IsSystemSchema(dbType, s.Database) {
		return fmt.Errorf("目标库 `%s` 是系统库，不允许通过工单变更；如确需变更系统库，请联系管理员处理", s.Database)
	}
	if s.Schema != "" && s.Schema != s.Database && dasServices.IsSystemSchemaPath(dbType, s.Schema) {
		return fmt.Errorf("目标 schema `%s` 是系统 schema，不允许通过工单变更；如确需变更系统库，请联系管理员处理", s.Schema)
	}
	return nil
}

var leadingSQLCommentPattern = regexp.MustCompile(`(?is)\A(?:\s|--[^\n]*(?:\n|$)|/\*.*?\*/)*`)

func normalizeLeadingSQL(content string) string {
	return strings.ToLower(strings.TrimSpace(leadingSQLCommentPattern.ReplaceAllString(content, "")))
}

type orderTargetMode string

const (
	orderTargetNone     orderTargetMode = "none"
	orderTargetDatabase orderTargetMode = "database"
	orderTargetSchema   orderTargetMode = "schema"
)

func detectOrderTargetMode(content string, dbType string) (orderTargetMode, error) {
	// PG / Oracle 类数据库使用语法感知拆分逐条判断（TiDB parser 无法解析 IDENTITY /
	// COMMENT ON / NUMBER 等非 MySQL 语法）；
	// 注意必须逐条判断而不能用整段前缀快速路径，否则「CREATE SCHEMA; CREATE TABLE」混用无法拦截
	if isPgLikeDB(dbType) || isOracleDB(dbType) {
		stmts, _, err := sqlsplit.SplitTyped(content)
		if err != nil {
			return "", err
		}
		mode := orderTargetSchema
		for _, stmt := range stmts {
			current := orderTargetSchema
			head := sqlsplit.NormalizeLeading(stmt)
			switch {
			case strings.HasPrefix(head, "create database"):
				current = orderTargetNone
			case strings.HasPrefix(head, "create schema") && isPgLikeDB(dbType):
				// PG 的 schema 在工单口径下对应「库层级」目标；
				// Oracle 的 CREATE SCHEMA 是组合授权语句、不是建 schema，按 schema 层级处理
				current = orderTargetDatabase
			}
			if mode == orderTargetSchema {
				mode = current
				continue
			}
			if mode != current {
				return "", fmt.Errorf("当前工单包含不同目标层级的SQL，请拆分后再提交")
			}
		}
		return mode, nil
	}
	normalized := normalizeLeadingSQL(content)
	switch {
	case strings.HasPrefix(normalized, "create database"):
		return orderTargetNone, nil
	case strings.HasPrefix(normalized, "create schema"):
		return orderTargetNone, nil
	}
	stmts, err := parser.NewParse(content, "", "")
	if err != nil {
		return "", err
	}
	if len(stmts) == 0 {
		return "", fmt.Errorf("SQL内容不能为空")
	}
	mode := orderTargetSchema
	for _, stmt := range stmts {
		current := orderTargetSchema
		switch stmt.(type) {
		case *ast.CreateDatabaseStmt:
			original := strings.ToLower(strings.TrimSpace(stmt.OriginalText()))
			if strings.HasPrefix(original, "create schema") && isPgLikeDB(dbType) {
				current = orderTargetDatabase
			} else {
				current = orderTargetNone
			}
		}
		if mode == orderTargetSchema {
			mode = current
			continue
		}
		if mode != current {
			return "", fmt.Errorf("当前工单包含不同目标层级的SQL，请拆分后再提交")
		}
	}
	return mode, nil
}

func resolveOrderTargetResource(dbType string, database string, schema string, mode orderTargetMode) (string, error) {
	switch mode {
	case orderTargetNone:
		return "", nil
	case orderTargetDatabase:
		if strings.TrimSpace(database) == "" {
			return "", fmt.Errorf("当前SQL需先选择数据库")
		}
		return strings.TrimSpace(database), nil
	default:
		if isOracleDB(dbType) {
			// Oracle 目标是 schema（CURRENT_SCHEMA 语义），无独立的 database 层级
			if strings.TrimSpace(schema) == "" {
				return "", fmt.Errorf("当前SQL需先选择Schema")
			}
			return strings.TrimSpace(schema), nil
		}
		if isPgLikeDB(dbType) {
			if strings.TrimSpace(database) == "" {
				return "", fmt.Errorf("当前SQL需先选择数据库")
			}
			if strings.TrimSpace(schema) == "" {
				return "", fmt.Errorf("当前SQL需先选择Schema")
			}
			return strings.TrimSpace(schema), nil
		}
		if strings.TrimSpace(database) == "" {
			return "", fmt.Errorf("当前SQL需先选择数据库")
		}
		return strings.TrimSpace(database), nil
	}
}

// inferOrderSQLType 按内容推断整单 SQL 类型。
//
// 实现收敛到 BuildOrderPlan（逐条语句定型后归约），不再按整段内容做前缀判断——
// 历史实现在 MySQL 路径上先看整段内容的第一个关键字，遇到
// `CREATE TABLE ...; INSERT ...` 会直接判成 DDL 并把后半段静默忽略；
// 一旦两种类型都被识别出来又报「不允许混用」。现在统一返回 DDL / DML / EXPORT / MIXED。
func inferOrderSQLType(content string, dbType string) (commonModels.EnumType, error) {
	plan, err := BuildOrderPlan(content, dbType)
	if err != nil {
		return "", err
	}
	return commonModels.EnumType(plan.SQLType), nil
}

func (s *CreateOrderService) validateExecutor() error {
	var count int64
	if err := global.App.DB.Table("insight_users").
		Where("username = ? AND is_active = 1", s.Executor).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("执行人不存在或未启用")
	}
	return nil
}

// 审核SQL
func (s *CreateOrderService) inspectSQL(instanceCfg commonModels.InsightInstances) ([]checker.ReturnData, error) {
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}
	connDB, inspectSchema := inspectConnTarget(string(s.DBType), s.Database, s.Schema)
	inspect := checker.SyntaxInspectService{
		C:             s.C,
		InstanceID:    instanceCfg.InstanceID,
		DbUser:        instanceCfg.User,
		DbPassword:    plainPassword,
		DbHost:        instanceCfg.Hostname,
		DbPort:        instanceCfg.Port,
		DbType:        string(s.DBType),
		DBSchema:      connDB,
		InspectSchema: inspectSchema,
		// Oracle 元信息探测需要服务名（extra_config.service_name）；其它库类型不消费该字段
		ServiceName: oracleServiceName(instanceCfg.ExtraConfig),
		Username:    s.Username,
		SqlText:     s.Content,
	}
	return inspect.Run()
}

func (s *CreateOrderService) Run() (string, error) {
	inferredSQLType, err := inferOrderSQLType(s.Content, string(s.DBType))
	if err != nil {
		return "", err
	}
	// 内容为准：推断为 MIXED 时接受客户端按单一类型提交，其余不一致仍拦截
	if s.SQLType, err = reconcileOrderSQLType(string(s.SQLType), inferredSQLType); err != nil {
		return "", err
	}

	if err := s.validateExecutor(); err != nil {
		return "", err
	}
	// 判断SQL条数：TiDB parser 只认 MySQL 方言（MySQL/TiDB/Doris）；其余引擎
	// （PostgreSQL/SQLServer/Oracle 等）用 parser 必然解析失败、会误拦正常工单，
	// 统一改用语法感知拆分计数 —— 语法/规范问题交给保存后的规范预审报告
	if tidbParserCompatibleDB(string(s.DBType)) {
		if err = parser.CheckMaxAllowedSQLNums(s.Content); err != nil {
			return "", err
		}
	} else if n := len(sqlsplit.Split(s.Content)); n > 2048 {
		return "", fmt.Errorf("单次最大允许提交2048条SQL，当前SQL语句条数为%d", n)
	}
	// 获取实例配置
	var config commonModels.InsightInstances
	global.App.DB.Table("`insight_instances`").
		Where("instance_id=?", s.InstanceID).
		First(&config)
	if config.InstanceID == "" {
		return "", fmt.Errorf("未找到对应数据源")
	}
	s.DBType = config.DbType
	s.Environment = config.Environment
	if err := validateOrderSQLTypeSupport(string(s.DBType), string(s.SQLType)); err != nil {
		return "", err
	}
	targetMode, err := detectOrderTargetMode(s.Content, string(s.DBType))
	if err != nil {
		return "", err
	}
	// 这里不再校验目标库/表的数据授权：变更(DML/DDL)与导出(EXPORT)由工单自身的
	// 「提交 → 审批 → 执行」流程覆盖，数据访问授权只约束查询接口
	// （口径见 das/services/access_action_scope.go）。
	// 以前按 sqlType 推导 dml/export 再查授权，会让「工单已批但没单独申请数据权限」
	// 卡在提交或执行，等于同一件事审两遍。
	s.Database = strings.TrimSpace(s.Database)
	s.Schema = strings.TrimSpace(s.Schema)
	if targetMode == orderTargetNone {
		s.Database = ""
		s.Schema = ""
	}
	if !isPgLikeDB(string(s.DBType)) && s.Database != "" {
		// MySQL 类实例仅维护一个库字段，统一把 schema 与 database 对齐，避免后续执行/展示口径不一致。
		s.Schema = s.Database
	}
	// 系统库校验（2026-10-24）：非管理员的工单目标不得指向系统库/schema。
	// 下拉里已经看不到（GetOrderSchemasService 同口径过滤），这里是防 API 直调的
	// 后端强校验；管理员放行——DBA 变更系统库是合法运维操作，走管理员自己的审批链。
	if err := s.rejectOrderSystemTarget(string(s.DBType)); err != nil {
		return "", err
	}

	// 规范类/语法类问题不在创建阶段拦截或提示，统一移至工单详情页的「规范预审」阶段展示；
	// 创建流程只做基础校验（必填、SQL 类型、目标资源与权限），保证提交照常继续。
	instance_id := strings.TrimSpace(s.InstanceID)
	if instance_id == "" {
		return "", fmt.Errorf("数据源ID不能为空")
	}
	// 抄送人
	ccData, err := json.Marshal(s.CC)
	if err != nil {
		return "", err
	}
	cc := datatypes.JSON(ccData)
	// 生成工单ID
	orderID := utils.GenerateOrderID()
	// 获取工单环境名称
	var env commonModels.InsightInstanceEnvironments
	global.App.DB.Table("`insight_instance_environments` a").
		Select("a.`name`, a.id").
		Where("a.id=?", s.Environment).
		Take(&env)
	// 创建工单
	record := models.InsightOrderRecords{
		Title:                 s.Title,
		OrderID:               orderID,
		Remark:                s.Remark,
		SQLType:               s.SQLType,
		DBType:                s.DBType,
		Environment:           env.Name,
		InstanceID:            instance_id,
		Database:              s.Database,
		Schema:                s.Schema,
		Applicant:             s.Username,
		Organization:          "",
		Executor:              s.Executor,
		CC:                    cc,
		Content:               s.Content,
		RollbackSQL:           s.RollbackSQL,
		EstimatedAffectedRows: s.EstimatedAffectedRows,
		ExportFileFormat:      s.ExportFileFormat,
		Progress:              "WAIT_PRECHECK",
	}
	err = global.App.DB.Transaction(func(tx *gorm.DB) error {
		// 插入工单记录
		if err := tx.Model(&models.InsightOrderRecords{}).Create(&record).Error; err != nil {
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
		// 记录操作日志
		if err := WriteOrderLog(tx, orderID, s.Username, fmt.Sprintf("用户%s创建了工单，已进入规范预审阶段", s.Username)); err != nil {
			global.App.Log.Error("CreateOrderService.Run error:", err.Error())
			return err
		}
		return nil
	})

	if err != nil {
		return "", err
	}

	// 事务提交成功后，重新查询最新记录
	var latestRecord models.InsightOrderRecords
	if err := global.App.DB.Where("order_id = ?", orderID).Take(&latestRecord).Error; err != nil {
		global.App.Log.Error("查询工单记录失败:", err)
		return "", err
	}

	// 生成规范预审快照：固定「提交时刻」的预审口径。
	// 生成失败只记录日志 + 落一条 failed 快照，不阻断工单创建。
	SaveOrderPreInspectSnapshotQuietly(&latestRecord, s.C)

	return orderID, nil
}
