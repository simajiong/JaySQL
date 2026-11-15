package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	aiServices "github.com/simajiong/jaysql/internal/ai/services"
	backupServices "github.com/simajiong/jaysql/internal/backup/services"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/eventbus"
	"github.com/simajiong/jaysql/pkg/lock"
	"github.com/simajiong/jaysql/pkg/secret"

	aiModels "github.com/simajiong/jaysql/internal/ai/models"
	announcementModels "github.com/simajiong/jaysql/internal/announcement/models"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	commonServices "github.com/simajiong/jaysql/internal/common/services"
	dasModels "github.com/simajiong/jaysql/internal/das/models"
	datasecModels "github.com/simajiong/jaysql/internal/datasec/models"
	datasecServices "github.com/simajiong/jaysql/internal/datasec/services"
	feedbackModels "github.com/simajiong/jaysql/internal/feedback/models"
	hotArticleModels "github.com/simajiong/jaysql/internal/hotarticle/models"
	hotArticleServices "github.com/simajiong/jaysql/internal/hotarticle/services"
	inspectModels "github.com/simajiong/jaysql/internal/inspect/models"
	inspectServices "github.com/simajiong/jaysql/internal/inspect/services"
	monitorServices "github.com/simajiong/jaysql/internal/monitor/services"
	ordersModels "github.com/simajiong/jaysql/internal/orders/models"
	performanceModels "github.com/simajiong/jaysql/internal/performance/models"
	performanceServices "github.com/simajiong/jaysql/internal/performance/services"
	usersModels "github.com/simajiong/jaysql/internal/users/models"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/dbresolver"
)

// 初始化进程内的发布/订阅总线（替代原先的 Redis Pub/Sub）。
//
// 这里刻意不做任何外部连接：总线是纯内存结构，创建即就绪，不存在连不上、
// 也不需要健康检查。原先 InitializeRedis 在连接失败时直接 panic，
// 一个与核心业务无关的外部组件却能阻止服务启动，这个耦合已移除。
func InitializeEventBus() *eventbus.Bus {
	return eventbus.New(eventbus.DefaultBufferSize)
}

// 初始化进程内的工单执行锁（替代原先的 Redis SetNX 锁）。
//
// 传入的 TTL 是兜底租约：正常执行路径会在 defer 里释放，
// 只有异常路径（进程崩溃、分支提前返回）才依赖它自动失效。
func InitializeLocks() *lock.Manager {
	return lock.New(lock.DefaultTTL)
}

func InitializeDB() *gorm.DB {
	switch global.App.Config.Database.Driver {
	case "mysql":
		return initializeMySQLGorm()
	default:
		return initializeMySQLGorm()
	}
}

// 初始化MySQL
func initializeMySQLGorm() *gorm.DB {
	config := global.App.Config.Database
	if config.Database == "" {
		return nil
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=True&loc=Local", config.UserName, config.Password, config.Host, config.Port, config.Database, config.Charset)
	gormLogger := logger.Default.LogMode(logger.Warn)
	if db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: gormLogger}); err != nil {
		global.App.Log.Error("mysql connect failed, err:", err.Error())
		panic(err.Error())
	} else {
		if err := db.Use(
			dbresolver.Register(dbresolver.Config{ /* xxx */ }).
				SetConnMaxIdleTime(time.Duration(config.ConnMaxIdleTime) * time.Second).
				SetConnMaxLifetime(time.Duration(config.ConnMaxLifetime) * time.Second).
				SetMaxIdleConns(config.MaxIdleConns).
				SetMaxOpenConns(config.MaxOpenConns),
		); err != nil {
			global.App.Log.Error("mysql set conn params failed, err:", err.Error())
			panic(err.Error())
		}
		// 凭据透明拦截插件：必须在任何业务查询之前注册，否则会漏掉启动期的读取。
		//
		// 这里刻意保持默认 ModeOff：加解密仍由各业务显式调用（L1），插件只作为
		// 后续迁移阶段的开关。等存量数据迁移完成、清单验证无误后，
		// 改成 secret.Install(db, secret.WithTransparentMode(secret.ModeOn)) 即可全量启用。
		if err := secret.Install(db); err != nil {
			global.App.Log.Warnf("secret: 注册透明拦截插件失败（已降级为显式调用）: %v", err)
		}

		// 初始化表
		initializeTables(db)

		// 敏感字段清单校验：表没建 / 列改名时能立刻发现，而不是等线上出现明文。
		// 只告警不阻断：insight_cloud_accounts 等表在部分环境未创建属于已知情况。
		if err := secret.Validate(db); err != nil {
			global.App.Log.Warnf("secret: 敏感字段清单校验未通过，请核对表结构: %v", err)
		}
		// 初始化用户允许的操作
		initializeAllowedOperations(db)
		// 初始化默认 SQL 开发规范（开发/生产）及其规则
		initializeDefaultSQLStandards(db)
		// 初始化默认环境（开发/生产），并绑定各自环境的默认规范
		initializeDefaultEnvironments(db)
		// 初始化首页「相关热文」默认条目（仅表内无记录时播种）
		initializeDefaultHotArticles(db)
		// 初始化 RBAC 权限点
		initializeRBACPermissions(db)
		// 退休已下线模块的权限点（旧「消息通知」），清理存量库里的孤儿行
		retireNotifyPermissions(db)
		// 初始化内置角色（member / admin / global_readonly）
		initializeBuiltinRoles(db)
		// 初始化系统管理员
		initializeAdminUser(db)
		// 初始化通知配置
		initializeNotifySettings(db)
		// 初始化内置大模型厂商模板（幂等播种，不覆盖运营侧调整）
		if err := aiServices.SeedBuiltinVendorTemplates(db); err != nil {
			global.App.Log.Warnf("seed builtin llm vendor templates failed: %v", err)
		}
		// 初始化系统参数
		initializeSystemParams(db)
		return db
	}
}

// 初始化系统表
func initializeTables(db *gorm.DB) {
	backupServices.EnsureBackupTaskSchema(db)
	// 监控表此前不在 AutoMigrate 列表里（表是外部创建的）。这里同步迁移，
	// 保证 HTTP 服务开始接收请求前，监控相关表与新增列已经就位。
	monitorServices.EnsureMonitorSchema(db)
	ensureRBACColumns(db)
	ensureDASPrincipalColumns(db)
	err := db.Set("gorm:table_options", "ENGINE=InnoDB CHARSET=utf8mb4").AutoMigrate(
		// 用户
		&usersModels.InsightUsers{},
		&usersModels.InsightRoles{},
		&usersModels.InsightPermissions{},
		&usersModels.InsightRolePermissions{},
		&usersModels.InsightUserRoles{},
		&usersModels.InsightUserPermissions{},
		// common
		&commonModels.InsightInstanceEnvironments{},
		&commonModels.InsightInstances{},
		&commonModels.InsightInstanceSchemas{},
		// 元数据快照刷新状态（一实例一行）：刷新器的节流依据与页面同步状态来源
		&commonModels.InsightInstanceMetaState{},
		&commonModels.InsightRiskDismissed{},
		&commonModels.InsightNotifySettings{},
		&commonModels.InsightSystemParam{},
		&commonModels.InsightAuditLogs{},
		// inspect
		&inspectModels.InsightSQLStandard{},
		&inspectModels.InsightSQLStandardRule{},
		&inspectModels.InsightInstanceSQLStandardBinding{},
		// das
		&dasModels.InsightDasSchemaPerms{},
		&dasModels.InsightDasTablePerms{},
		&dasModels.InsightDASRecords{},
		&dasModels.InsightDASOperations{},
		&dasModels.InsightDASFavorites{},
		// 细粒度数据授权（新模型）：显式授权 + 变更审计流水
		&dasModels.InsightDasDataGrant{},
		&dasModels.InsightDasGrantAudit{},
		// 数据访问权限申请：申请单 + 流转事件流水
		&dasModels.InsightDasAccessRequest{},
		&dasModels.InsightDasAccessRequestEvent{},
		// orders
		&ordersModels.InsightOrderRecords{},
		&ordersModels.InsightOrderTasks{},
		&ordersModels.InsightOrderMessages{},
		&ordersModels.InsightApprovalFlows{},
		&ordersModels.InsightApprovalRecords{},
		&ordersModels.InsightApprovalFlowUsers{},
		&ordersModels.InsightOrderLogs{},
		&ordersModels.InsightOrderPreInspections{},
		// 钉钉审批一次性令牌（工单审批卡片回调的防重放凭据）
		&ordersModels.InsightOrderApprovalTokens{},
		&performanceModels.InsightPerformanceSnapshotParams{},
		&performanceModels.InsightPerformanceSlowLogCollectors{},
		&performanceModels.InsightPerformanceSlowLogEntries{},
		// Oracle V$SQL 采样基线（累计值差分用）
		&performanceModels.InsightPerformanceSlowLogSamples{},
		// 参数修改历史（参数当前值实时查目标实例，不落平台库）
		&performanceModels.InsightPerformanceParamChanges{},
		&backupModels.InsightBackupTasks{},
		&backupModels.InsightBackupRuns{},
		&backupModels.InsightBackupSets{},
		&backupModels.InsightBackupNodes{},
		&backupModels.InsightBackupStorageTargets{},
		&backupModels.InsightRestoreRuns{},
		&backupModels.InsightRestoreRunItems{},
		// 大模型配置接入模块
		&aiModels.InsightLlmProviders{},
		&aiModels.InsightLlmModels{},
		&aiModels.InsightLlmVendorTemplates{},
		&aiModels.InsightLlmCallLogs{},
		// 用户反馈
		&feedbackModels.InsightFeedback{},
		// 系统公告
		&announcementModels.InsightAnnouncement{},
		// 首页相关热文（管理员可配置）
		&hotArticleModels.InsightHotArticle{},
		// 数据安全（脱敏）：规则 / 列配置 / 明文工单
		&datasecModels.InsightDataSecRules{},
		&datasecModels.InsightDataSecColumns{},
		&datasecModels.InsightDataSecPlainGrants{},
	)
	if err != nil {
		global.App.Log.Fatal("migrate table failed", err.Error())
	}
	ensureOrderTableColumns(db)
	// 慢日志的 event_key 唯一索引依赖存量数据回填，不能交给 AutoMigrate，
	// 必须与 monitor 的 EnsureMonitorSchema 一样在表结构就位后单独补。
	performanceServices.EnsureSlowLogSchema(db)
	// 内置脱敏规则幂等 seed（6 条，已存在则跳过）
	datasecServices.EnsureBuiltinRules(db)
	logDataGrantMigrationState(db)
}

// logDataGrantMigrationState 启动自检：把数据授权模型的启用状态写进日志。
//
// 改造后「数据可见性」的唯一权威是 insight_das_data_grants，而它是否被采用
// 取决于「存量迁移有没有跑过」（见 das/services/data_grant.go:dataGrantEnabled）。
// 这条日志让运维一眼确认当前处于哪个阶段，避免出现「代码已发布但迁移没跑，
// 大家都在用旧表」这种静默状态。
func logDataGrantMigrationState(db *gorm.DB) {
	var grantCount, legacyCount int64
	if err := db.Table("insight_das_data_grants").Count(&grantCount).Error; err != nil {
		global.App.Log.Warnf("data grant self-check failed: %v", err)
		return
	}
	_ = db.Table("insight_das_schema_perms").Count(&legacyCount).Error

	if grantCount > 0 {
		global.App.Log.Infof("数据授权：已启用细粒度显式授权模型（授权记录 %d 条；旧表 %d 行已不参与判定）", grantCount, legacyCount)
		return
	}
	global.App.Log.Warnf("数据授权：尚未启用细粒度显式授权模型（新表为空，当前回退到旧表显式授权）。如已确认升级，请执行 backend/sql/20261015_data_grant_fine_grained.sql 的 STEP 2/3")
}

func ensureOrderTableColumns(db *gorm.DB) {
	statements := []string{
		"ALTER TABLE insight_order_records MODIFY COLUMN db_type varchar(32) NOT NULL DEFAULT 'MySQL' COMMENT 'DB类型'",
		"ALTER TABLE insight_order_records MODIFY COLUMN `schema` varchar(255) NOT NULL DEFAULT '' COMMENT '资源路径'",
		"ALTER TABLE insight_order_tasks MODIFY COLUMN db_type varchar(32) NOT NULL DEFAULT 'MySQL' COMMENT 'DB类型'",
		// sql_type 从 ENUM 放宽到 varchar：混合工单需要写入 MIXED，而 ENUM 增枚举值
		// 无法被 AutoMigrate 感知（枚举值差异不体现在列类型名上），漏改会让 MIXED
		// 在严格模式下写入失败 / 非严格模式下被截断成空串。
		"ALTER TABLE insight_order_records MODIFY COLUMN sql_type varchar(16) NOT NULL DEFAULT 'DML' COMMENT 'SQL类型(DDL/DML/EXPORT/MIXED)'",
		"ALTER TABLE insight_order_tasks MODIFY COLUMN sql_type varchar(16) NOT NULL DEFAULT 'DML' COMMENT 'SQL类型(DDL/DML/EXPORT)'",
	}
	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			global.App.Log.Warnf("adjust order table columns failed: %v, sql: %s", err, stmt)
		}
	}
	// 混合工单靠任务序号还原语句书写顺序：存量任务 seq=0，排序时回落到主键
	if !db.Migrator().HasColumn(&ordersModels.InsightOrderTasks{}, "seq") {
		if err := db.Exec("ALTER TABLE insight_order_tasks ADD COLUMN seq int NOT NULL DEFAULT 0 COMMENT '工单内执行序号'").Error; err != nil {
			global.App.Log.Warnf("add order tasks seq column failed: %v", err)
		}
	}
}

func ensureRBACColumns(db *gorm.DB) {
	if !db.Migrator().HasTable(&usersModels.InsightRoles{}) {
		return
	}

	statements := []string{}
	if !db.Migrator().HasColumn(&usersModels.InsightRoles{}, "code") {
		statements = append(statements, "ALTER TABLE insight_roles ADD COLUMN code varchar(64) NOT NULL DEFAULT '' COMMENT '角色编码'")
	}
	if !db.Migrator().HasColumn(&usersModels.InsightRoles{}, "is_builtin") {
		statements = append(statements, "ALTER TABLE insight_roles ADD COLUMN is_builtin boolean NOT NULL DEFAULT false COMMENT '是否内置角色'")
	}
	if !db.Migrator().HasColumn(&usersModels.InsightRoles{}, "is_deleted") {
		statements = append(statements, "ALTER TABLE insight_roles ADD COLUMN is_deleted boolean NOT NULL DEFAULT false COMMENT '软删除标记'")
	}
	if !db.Migrator().HasColumn(&usersModels.InsightUsers{}, "is_deleted") {
		statements = append(statements, "ALTER TABLE insight_users ADD COLUMN is_deleted boolean NOT NULL DEFAULT false COMMENT '软删除标记'")
	}
	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			global.App.Log.Warnf("adjust rbac table columns failed: %v, sql: %s", err, stmt)
		}
	}

	repairStatements := []string{
		"UPDATE insight_roles SET code = CONCAT('role_', id) WHERE code IS NULL OR TRIM(code) = ''",
	}
	for _, stmt := range repairStatements {
		if err := db.Exec(stmt).Error; err != nil {
			global.App.Log.Warnf("adjust rbac table columns failed: %v, sql: %s", err, stmt)
		}
	}

	if !db.Migrator().HasIndex(&usersModels.InsightRoles{}, "uniq_code") {
		if err := db.Exec("ALTER TABLE insight_roles ADD UNIQUE INDEX uniq_code (code)").Error; err != nil {
			global.App.Log.Warnf("adjust rbac table columns failed: %v, sql: %s", err, "ALTER TABLE insight_roles ADD UNIQUE INDEX uniq_code (code)")
		}
	}
}

func ensureDASPrincipalColumns(db *gorm.DB) {
	if db.Migrator().HasTable(&dasModels.InsightDasSchemaPerms{}) {
		statements := []string{}
		if !db.Migrator().HasColumn(&dasModels.InsightDasSchemaPerms{}, "principal_type") {
			statements = append(statements, "ALTER TABLE insight_das_schema_perms ADD COLUMN principal_type varchar(16) NOT NULL DEFAULT 'user' COMMENT '授权主体类型'")
		}
		if !db.Migrator().HasColumn(&dasModels.InsightDasSchemaPerms{}, "principal_id") {
			statements = append(statements, "ALTER TABLE insight_das_schema_perms ADD COLUMN principal_id bigint NOT NULL DEFAULT 0 COMMENT '授权主体ID'")
		}
		if !db.Migrator().HasColumn(&dasModels.InsightDasSchemaPerms{}, "principal_name") {
			statements = append(statements, "ALTER TABLE insight_das_schema_perms ADD COLUMN principal_name varchar(128) NOT NULL DEFAULT '' COMMENT '授权主体名称快照'")
		}
		for _, stmt := range statements {
			if err := db.Exec(stmt).Error; err != nil {
				global.App.Log.Warnf("adjust das schema perm columns failed: %v, sql: %s", err, stmt)
			}
		}
		repairs := []string{
			"UPDATE insight_das_schema_perms p JOIN insight_users u ON p.username = u.username SET p.principal_type='user', p.principal_id=u.uid, p.principal_name=u.username WHERE p.principal_id = 0 AND p.username <> ''",
			"UPDATE insight_das_schema_perms SET principal_type='user' WHERE principal_type IS NULL OR TRIM(principal_type) = ''",
			"UPDATE insight_das_schema_perms SET principal_name=username WHERE principal_name = '' AND username <> ''",
		}
		for _, stmt := range repairs {
			if err := db.Exec(stmt).Error; err != nil {
				global.App.Log.Warnf("repair das schema perm data failed: %v, sql: %s", err, stmt)
			}
		}
		if !db.Migrator().HasIndex(&dasModels.InsightDasSchemaPerms{}, "uniq_schema_principal") {
			if err := db.Exec("ALTER TABLE insight_das_schema_perms ADD UNIQUE INDEX uniq_schema_principal (principal_type, principal_id, `schema`, instance_id)").Error; err != nil {
				global.App.Log.Warnf("adjust das schema perm index failed: %v", err)
			}
		}
	}

	if db.Migrator().HasTable(&dasModels.InsightDasTablePerms{}) {
		statements := []string{}
		if !db.Migrator().HasColumn(&dasModels.InsightDasTablePerms{}, "principal_type") {
			statements = append(statements, "ALTER TABLE insight_das_table_perms ADD COLUMN principal_type varchar(16) NOT NULL DEFAULT 'user' COMMENT '授权主体类型'")
		}
		if !db.Migrator().HasColumn(&dasModels.InsightDasTablePerms{}, "principal_id") {
			statements = append(statements, "ALTER TABLE insight_das_table_perms ADD COLUMN principal_id bigint NOT NULL DEFAULT 0 COMMENT '授权主体ID'")
		}
		if !db.Migrator().HasColumn(&dasModels.InsightDasTablePerms{}, "principal_name") {
			statements = append(statements, "ALTER TABLE insight_das_table_perms ADD COLUMN principal_name varchar(128) NOT NULL DEFAULT '' COMMENT '授权主体名称快照'")
		}
		for _, stmt := range statements {
			if err := db.Exec(stmt).Error; err != nil {
				global.App.Log.Warnf("adjust das table perm columns failed: %v, sql: %s", err, stmt)
			}
		}
		repairs := []string{
			"UPDATE insight_das_table_perms p JOIN insight_users u ON p.username = u.username SET p.principal_type='user', p.principal_id=u.uid, p.principal_name=u.username WHERE p.principal_id = 0 AND p.username <> ''",
			"UPDATE insight_das_table_perms SET principal_type='user' WHERE principal_type IS NULL OR TRIM(principal_type) = ''",
			"UPDATE insight_das_table_perms SET principal_name=username WHERE principal_name = '' AND username <> ''",
		}
		for _, stmt := range repairs {
			if err := db.Exec(stmt).Error; err != nil {
				global.App.Log.Warnf("repair das table perm data failed: %v, sql: %s", err, stmt)
			}
		}
		if !db.Migrator().HasIndex(&dasModels.InsightDasTablePerms{}, "uniq_table_principal") {
			if err := db.Exec("ALTER TABLE insight_das_table_perms ADD UNIQUE INDEX uniq_table_principal (principal_type, principal_id, `schema`, `table`, instance_id)").Error; err != nil {
				global.App.Log.Warnf("adjust das table perm index failed: %v", err)
			}
		}
	}
}

// defaultAdminPasswordHash 是默认密码 Admin@2026 的 bcrypt 哈希（cost=10）
const defaultAdminPasswordHash = "$2a$10$sHXyrEaa0/pk.o6hKtK1VuHhPQBI6./e4oP6p.R0Y1nnIXZgRPgqW"

// 初始化系统管理员
func initializeAdminUser(db *gorm.DB) {
	var user usersModels.InsightUsers
	err := db.Where("username = ?", "admin").First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		user = usersModels.InsightUsers{
			Username:    "admin",
			Password:    defaultAdminPasswordHash,
			Email:       "admin@example.com",
			Mobile:      "",
			AvatarFile:  "/static/avatar2.jpg",
			IsSuperuser: true,
			IsActive:    true,
			IsStaff:     false,
			IsDeleted:   false,
		}
		if err := db.Create(&user).Error; err != nil {
			global.App.Log.Warnf("seed admin user failed: %v", err)
			return
		}
		global.App.Log.Infof("init: 默认管理员 admin 已创建，默认密码 Admin@2026，请登录后及时修改")
	} else if err != nil {
		global.App.Log.Warnf("load admin user failed: %v", err)
		return
	} else {
		// admin 已存在：强制修复状态字段，防止被误禁用/软删除
		repair := map[string]interface{}{}
		if user.IsDeleted {
			repair["is_deleted"] = false
		}
		if !user.IsActive {
			repair["is_active"] = true
		}
		if !user.IsSuperuser {
			repair["is_superuser"] = true
		}
		if strings.TrimSpace(user.Email) == "" {
			repair["email"] = "admin@example.com"
		}
		if len(repair) > 0 {
			if err := db.Model(&usersModels.InsightUsers{}).Where("uid = ?", user.Uid).Updates(repair).Error; err != nil {
				global.App.Log.Warnf("repair admin user status failed: %v", err)
			} else {
				global.App.Log.Infof("init: 已自动修复管理员 admin 的状态字段")
			}
		}
	}

	// 给 admin 绑定 admin 内置角色
	var adminRole usersModels.InsightRoles
	if err := db.Where("code = ? AND is_builtin = ? AND is_deleted = ?",
		usersModels.BuiltinRoleCodeAdmin, true, false).First(&adminRole).Error; err == nil {
		var bind usersModels.InsightUserRoles
		err := db.Where("uid = ? AND role_id = ?", user.Uid, adminRole.ID).First(&bind).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := db.Create(&usersModels.InsightUserRoles{UID: user.Uid, RoleID: adminRole.ID}).Error; err != nil {
				global.App.Log.Warnf("bind admin role to admin user failed: %v", err)
			} else {
				global.App.Log.Infof("init: 已给管理员 admin 绑定系统管理员角色")
			}
		}
	}
}

type rbacPermissionSeed struct {
	Code           string
	Name           string
	Module         string
	Resource       string
	Action         string
	PermissionType string
	Description    string
}

func initializeRBACPermissions(db *gorm.DB) {
	permissions := []rbacPermissionSeed{
		{"system.access", "访问系统管理", "system", "admin", "access", "page", "进入系统管理模块"},
		{"system.user.view", "查看用户", "system", "user", "view", "page", "查看用户管理页面和列表"},
		{"system.user.manage", "管理用户", "system", "user", "manage", "action", "新增编辑删除用户及重置密码"},
		{"system.role.view", "查看角色", "system", "role", "view", "page", "查看角色管理页面和列表"},
		{"system.role.manage", "管理角色", "system", "role", "manage", "action", "新增编辑删除角色及配置权限"},
		{"system.permission.view", "查看权限点", "system", "permission", "view", "page", "查看权限点列表"},
		{"system.environment.view", "查看环境", "system", "environment", "view", "page", "查看环境管理"},
		{"system.environment.manage", "管理环境", "system", "environment", "manage", "action", "管理环境配置"},
		{"system.instance.view", "查看实例", "system", "instance", "view", "page", "查看实例配置"},
		{"system.instance.manage", "管理实例", "system", "instance", "manage", "action", "新增编辑删除实例"},
		{"system.instance.test", "测试实例连接", "system", "instance", "test", "action", "执行实例测试连接"},
		{"system.instance.sync_meta", "同步实例元数据", "system", "instance", "sync_meta", "action", "执行实例元数据同步"},
		{"system.inspect.view", "查看SQL规范", "system", "inspect", "view", "page", "查看SQL规范"},
		{"system.inspect.manage", "管理SQL规范", "system", "inspect", "manage", "action", "管理SQL规范"},
		{"system.das.view", "查看数据访问", "system", "das", "view", "page", "查看数据访问授权"},
		{"system.das.manage", "管理数据访问", "system", "das", "manage", "action", "维护数据访问授权"},
		{"das.access.apply", "申请数据访问", "das", "access", "apply", "action", "针对具体工单发起数据访问权限申请"},
		{"das.access.approve", "审批数据访问申请", "das", "access", "approve", "action", "审批数据访问权限申请并落地授权"},
		{"das.access.view", "查看数据访问申请", "das", "access", "view", "page", "查看全部数据访问权限申请与审批记录"},
		// 2026-10-25 新增「数据源管理」权限组（module=datasource）：用户编辑弹窗独立 Tab 展示。
		// 数据兼容口径：存量库不回填、不初始化授权，新增权限默认不授予，仅对新产生的授权生效
		// （admin 为代码严格对齐的内置角色，启动时按 BuiltinRoleDefinitions 自动同步，属既有行为）。
		{"datasource.instance.view", "查看数据源", "datasource", "instance", "view", "page", "查看数据源管理、数据源总览与实例清单"},
		{"datasource.instance.manage", "管理数据源", "datasource", "instance", "manage", "action", "新增编辑删除数据源（实例）配置"},
		{"datasource.instance.ssh", "SSH 主机登录", "datasource", "instance", "ssh", "action", "通过主机终端 SSH 登录数据库服务器"},
		{"datasource.param.view", "参数查看", "datasource", "param", "view", "page", "查看数据库参数配置"},
		{"datasource.param.manage", "参数管理", "datasource", "param", "manage", "action", "修改数据库参数并留存历史（高危参数需二次确认）"},
		{"datasource.compare.view", "多实例对比", "datasource", "compare", "view", "page", "使用多实例参数对比视图"},
		{"system.approval_flow.view", "查看审批流", "system", "approval_flow", "view", "page", "查看审批流配置"},
		{"system.approval_flow.manage", "管理审批流", "system", "approval_flow", "manage", "action", "维护审批流配置"},
		{"sql.console.access", "访问 SQL 查询", "sql", "console", "access", "page", "进入 SQL 查询页面"},
		{"sql.console.execute", "执行 SQL 查询", "sql", "console", "execute", "action", "执行 SQL 查询语句"},
		{"sql.favorite.view", "查看 SQL 收藏", "sql", "favorite", "view", "page", "查看 SQL 收藏"},
		{"sql.favorite.manage", "管理 SQL 收藏", "sql", "favorite", "manage", "action", "新增编辑删除 SQL 收藏"},
		{"sql.history.view", "查看 SQL 历史", "sql", "history", "view", "page", "查看 SQL 历史"},
		{"order.view", "查看工单", "order", "order", "view", "page", "查看工单列表和详情"},
		{"order.submit", "提交工单", "order", "order", "submit", "action", "新建并提交工单"},
		{"order.approve", "审批工单", "order", "order", "approve", "action", "审批和审核工单"},
		{"order.execute", "执行工单", "order", "order", "execute", "action", "执行工单任务"},
		{"order.manage", "管理工单", "order", "order", "manage", "action", "终止/关闭/强制流转工单等管理操作"},
		{"audit.log.view", "查看审计日志", "audit", "log", "view", "page", "查看安全审计日志"},
		{"audit.log.export", "导出审计日志", "audit", "log", "export", "action", "导出审计日志"},
		{"audit.policy.view", "查看审计策略", "audit", "policy", "view", "page", "查看审计策略配置"},
		{"audit.policy.manage", "管理审计策略", "audit", "policy", "manage", "action", "编辑审计策略配置"},
		{"backup.task.view", "查看备份任务", "backup", "task", "view", "page", "查看备份任务列表和详情"},
		{"backup.task.manage", "管理备份任务", "backup", "task", "manage", "action", "创建编辑执行备份任务"},
		{"backup.set.view", "查看备份集", "backup", "set", "view", "page", "查看备份集列表"},
		{"backup.set.download", "下载备份集", "backup", "set", "download", "action", "下载备份集文件"},
		{"backup.restore.view", "查看恢复任务", "backup", "restore", "view", "page", "查看恢复任务列表和详情"},
		{"backup.restore.execute", "执行恢复任务", "backup", "restore", "execute", "action", "发起并执行恢复任务"},
		{"monitor.view", "查看监控告警", "monitor", "monitor", "view", "page", "查看监控告警任务和状态"},
		{"monitor.manage", "管理监控告警", "monitor", "monitor", "manage", "action", "创建编辑删除监控告警任务"},
		{"performance.view", "查看性能分析", "performance", "performance", "view", "page", "查看性能分析概览与慢SQL分析"},
		{"performance.manage", "管理性能", "performance", "snapshot_param", "manage", "action", "在查看性能分析基础上，额外允许生成负载快照并维护快照报告参数"},
		{"performance.param.modify", "修改数据库参数", "performance", "param", "modify", "action", "对实例执行参数修改并留存历史（高危参数需二次确认）"},
		{"system.ai.view", "查看 AI 能力配置", "system", "ai", "view", "page", "查看大模型接入配置与模型清单"},
		{"system.ai.manage", "管理 AI 能力配置", "system", "ai", "manage", "action", "接入/编辑/启停大模型服务商与厂商模板"},
		{"ai.chat.use", "使用 AI 助手", "ai", "chat", "use", "action", "在工作台 AI 助手发起对话与配置请求"},
		{"system.feedback.view", "查看反馈管理", "system", "feedback", "view", "page", "查看用户反馈列表与详情"},
		{"system.feedback.manage", "管理反馈", "system", "feedback", "manage", "action", "处理用户反馈：更新状态与备注、删除反馈"},
		{"system.announcement.view", "查看公告管理", "system", "announcement", "view", "page", "查看系统公告列表"},
		{"system.announcement.manage", "管理公告", "system", "announcement", "manage", "action", "新增编辑删除公告及启停公告"},
		{"system.hotarticle.view", "查看热文管理", "system", "hotarticle", "view", "page", "查看首页相关热文配置列表"},
		{"system.hotarticle.manage", "管理热文", "system", "hotarticle", "manage", "action", "新增编辑删除首页热文、启停与调整排序"},
		// 数据安全（脱敏）：规则配置 / 列配置与自动发现 / 明文工单
		{"datasec.rule.view", "查看脱敏配置", "datasec", "rule", "view", "page", "查看脱敏规则与库表列配置"},
		{"datasec.rule.manage", "管理脱敏配置", "datasec", "rule", "manage", "action", "维护脱敏规则、绑定列与触发敏感列扫描"},
		{"datasec.plain.request", "申请明文访问", "datasec", "plain", "request", "action", "发起明文访问工单申请并查看自己的申请记录"},
		{"datasec.plain.approve", "审批明文访问", "datasec", "plain", "approve", "action", "审批/驳回明文工单、收回已生效授权"},
	}

	for _, item := range permissions {
		var permission usersModels.InsightPermissions
		err := db.Where("code = ?", item.Code).First(&permission).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			permission = usersModels.InsightPermissions{
				Code:           item.Code,
				Name:           item.Name,
				Module:         item.Module,
				Resource:       item.Resource,
				Action:         item.Action,
				PermissionType: item.PermissionType,
				Description:    item.Description,
				IsBuiltin:      true,
				IsEnabled:      true,
			}
			if err := db.Create(&permission).Error; err != nil {
				global.App.Log.Warnf("seed permission failed: %v, code: %s", err, item.Code)
			}
			continue
		}
		if err != nil {
			global.App.Log.Warnf("load permission failed: %v, code: %s", err, item.Code)
			continue
		}
		updates := map[string]interface{}{
			"name":            item.Name,
			"module":          item.Module,
			"resource":        item.Resource,
			"action":          item.Action,
			"permission_type": item.PermissionType,
			"description":     item.Description,
			"is_builtin":      true,
			"is_enabled":      true,
		}
		if err := db.Model(&usersModels.InsightPermissions{}).Where("id = ?", permission.ID).Updates(updates).Error; err != nil {
			global.App.Log.Warnf("update permission seed failed: %v, code: %s", err, item.Code)
		}
	}
}

// retiredPermissionCodes 已下线权限点：旧「系统管理 → 消息通知」页及其接口已整条删除，
// 权限点随之退休。这里做一次性幂等清理，避免存量库的权限树里继续出现「通知配置」分组
// （这些行不会再被任何角色或接口引用，留着只会让管理员困惑）。
var retiredPermissionCodes = []string{
	"system.notify.view",
	"system.notify.manage",
	"system.notify.test",
}

// retireNotifyPermissions 删除已下线的通知权限点及其角色/用户绑定。
//
// 顺序必须是「先解绑、再删权限点」：绑定表里留着指向已删权限的 id，
// 会让角色/用户权限页出现查不到权限点的幽灵条目。
func retireNotifyPermissions(db *gorm.DB) {
	var ids []uint64
	if err := db.Model(&usersModels.InsightPermissions{}).
		Where("code IN ?", retiredPermissionCodes).
		Pluck("id", &ids).Error; err != nil {
		global.App.Log.Warnf("load retired permissions failed: %v", err)
		return
	}
	if len(ids) == 0 {
		return
	}

	if err := db.Where("permission_id IN ?", ids).Delete(&usersModels.InsightRolePermissions{}).Error; err != nil {
		global.App.Log.Warnf("unbind retired role permissions failed: %v", err)
		return
	}
	if err := db.Where("permission_id IN ?", ids).Delete(&usersModels.InsightUserPermissions{}).Error; err != nil {
		global.App.Log.Warnf("unbind retired user permissions failed: %v", err)
		return
	}
	if err := db.Where("id IN ?", ids).Delete(&usersModels.InsightPermissions{}).Error; err != nil {
		global.App.Log.Warnf("delete retired permissions failed: %v", err)
		return
	}
	global.App.Log.Infof("已清理 %d 个已下线的通知配置权限点", len(ids))
}

// 初始化用户允许的操作
func initializeAllowedOperations(db *gorm.DB) {
	var ops []map[string]any = []map[string]any{
		{"name": "SELECT", "is_enable": true, "remark": ""},
		{"name": "UNION", "is_enable": true, "remark": ""},
		{"name": "Use", "is_enable": true, "remark": ""},
		{"name": "Desc", "is_enable": true, "remark": ""},
		{"name": "ExplainSelect", "is_enable": true, "remark": ""},
		{"name": "ExplainDelete", "is_enable": true, "remark": ""},
		{"name": "ExplainInsert", "is_enable": true, "remark": ""},
		{"name": "ExplainUpdate", "is_enable": true, "remark": ""},
		{"name": "ExplainUnion", "is_enable": true, "remark": ""},
		{"name": "ExplainFor", "is_enable": true, "remark": "ExplainForStmt is a statement to provite information about how is SQL statement executeing in connection #ConnectionID"},
		{"name": "ShowEngines", "is_enable": false, "remark": ""},
		{"name": "ShowDatabases", "is_enable": false, "remark": ""},
		{"name": "ShowTables", "is_enable": false, "remark": ""},
		{"name": "ShowTableStatus", "is_enable": false, "remark": ""},
		{"name": "ShowColumns", "is_enable": false, "remark": ""},
		{"name": "ShowWarnings", "is_enable": false, "remark": ""},
		{"name": "ShowCharset", "is_enable": false, "remark": ""},
		{"name": "ShowVariables", "is_enable": false, "remark": ""},
		{"name": "ShowStatus", "is_enable": false, "remark": ""},
		{"name": "ShowCollation", "is_enable": false, "remark": ""},
		{"name": "ShowCreateTable", "is_enable": false, "remark": ""},
		{"name": "ShowCreateView", "is_enable": false, "remark": ""},
		{"name": "ShowCreateUser", "is_enable": false, "remark": ""},
		{"name": "ShowCreateSequence", "is_enable": false, "remark": ""},
		{"name": "ShowCreatePlacementPolicy", "is_enable": false, "remark": ""},
		{"name": "ShowGrants", "is_enable": false, "remark": ""},
		{"name": "ShowTriggers", "is_enable": false, "remark": ""},
		{"name": "ShowProcedureStatus", "is_enable": false, "remark": ""},
		{"name": "ShowIndex", "is_enable": false, "remark": ""},
		{"name": "ShowProcessList", "is_enable": false, "remark": ""},
		{"name": "ShowCreateDatabase", "is_enable": false, "remark": ""},
		{"name": "ShowConfig", "is_enable": false, "remark": ""},
		{"name": "ShowEvents", "is_enable": false, "remark": ""},
		{"name": "ShowStatsExtended", "is_enable": false, "remark": ""},
		{"name": "ShowStatsMeta", "is_enable": false, "remark": ""},
		{"name": "ShowStatsHistograms", "is_enable": false, "remark": ""},
		{"name": "ShowStatsTopN", "is_enable": false, "remark": ""},
		{"name": "ShowStatsBuckets", "is_enable": false, "remark": ""},
		{"name": "ShowStatsHealthy", "is_enable": false, "remark": ""},
		{"name": "ShowStatsLocked", "is_enable": false, "remark": ""},
		{"name": "ShowHistogramsInFlight", "is_enable": false, "remark": ""},
		{"name": "ShowColumnStatsUsage", "is_enable": false, "remark": ""},
		{"name": "ShowPlugins", "is_enable": false, "remark": ""},
		{"name": "ShowProfile", "is_enable": false, "remark": ""},
		{"name": "ShowProfiles", "is_enable": false, "remark": ""},
		{"name": "ShowMasterStatus", "is_enable": false, "remark": ""},
		{"name": "ShowPrivileges", "is_enable": false, "remark": ""},
		{"name": "ShowErrors", "is_enable": false, "remark": ""},
		{"name": "ShowBindings", "is_enable": false, "remark": ""},
		{"name": "ShowBindingCacheStatus", "is_enable": false, "remark": ""},
		{"name": "ShowPumpStatus", "is_enable": false, "remark": ""},
		{"name": "ShowDrainerStatus", "is_enable": false, "remark": ""},
		{"name": "ShowOpenTables", "is_enable": false, "remark": ""},
		{"name": "ShowAnalyzeStatus", "is_enable": false, "remark": ""},
		{"name": "ShowRegions", "is_enable": false, "remark": ""},
		{"name": "ShowBuiltins", "is_enable": false, "remark": ""},
		{"name": "ShowTableNextRowId", "is_enable": false, "remark": ""},
		{"name": "ShowBackups", "is_enable": false, "remark": ""},
		{"name": "ShowRestores", "is_enable": false, "remark": ""},
		{"name": "ShowPlacement", "is_enable": false, "remark": ""},
		{"name": "ShowPlacementForDatabase", "is_enable": false, "remark": ""},
		{"name": "ShowPlacementForTable", "is_enable": false, "remark": ""},
		{"name": "ShowPlacementForPartition", "is_enable": false, "remark": ""},
		{"name": "ShowPlacementLabels", "is_enable": false, "remark": ""},
		{"name": "ShowSessionStates", "is_enable": false, "remark": ""},
	}
	for _, i := range ops {
		var allowedOperations dasModels.InsightDASOperations
		_ = db.FirstOrCreate(&allowedOperations, dasModels.InsightDASOperations{
			Name:     i["name"].(string),
			IsEnable: i["is_enable"].(bool),
			Remark:   i["remark"].(string),
		})
	}
}

// 初始化默认 SQL 开发规范（开发/生产）及其规则
func initializeDefaultSQLStandards(db *gorm.DB) {
	if err := inspectServices.InitializeDefaultSQLStandards(db); err != nil {
		global.App.Log.Warnf("initialize default SQL standards failed: %v", err)
		return
	}
	// 存量规则的 rule_type 回填：类型列上线前建的规则一律为空（页面曾整列显示「待补充」）。
	// 已经合法的值不动，所以每次启动跑都幂等；首次部署会看到 affected 数量。
	affected, err := inspectServices.BackfillSQLStandardRuleTypes(db)
	if err != nil {
		global.App.Log.Warnf("backfill SQL standard rule_type failed: %v", err)
		return
	}
	if affected > 0 {
		global.App.Log.Infof("backfill SQL standard rule_type: %d rules updated", affected)
	}
}

// 初始化默认环境（开发/生产）。
// 仅在环境表为空时播种：分别绑定对应环境类型的默认 SQL 规范，保证「数据源 → 环境 → 规范」链路开箱可用。
func initializeDefaultEnvironments(db *gorm.DB) {
	if err := commonServices.InitializeDefaultEnvironments(db); err != nil {
		global.App.Log.Warnf("initialize default environments failed: %v", err)
	}
}

// 初始化首页「相关热文」默认条目。
// 仅在热文表为空时播种，与改造前的静态内容一致：新装环境首页不会空着。
// 管理员登录后可在【系统管理 → 系统配置 → 热文管理】里自由编辑/停用/删除；
// 一旦表内有记录就不再播种，保证「管理员主动清空」不会被下次启动复活。
func initializeDefaultHotArticles(db *gorm.DB) {
	if err := hotArticleServices.InitializeDefaultHotArticles(db); err != nil {
		global.App.Log.Warnf("initialize default hot articles failed: %v", err)
	}
}

// 初始化平台默认通知配置（优先使用DB，首次则写入默认配置）
func initializeNotifySettings(db *gorm.DB) {
	const notifyConfigKeyDefault = "default"

	var record commonModels.InsightNotifySettings
	err := db.Where("config_key = ?", notifyConfigKeyDefault).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		record = commonModels.InsightNotifySettings{
			ConfigKey:    notifyConfigKeyDefault,
			NoticeURL:    defaultNoticeURL(),
			MailEnable:   false,
			MailUsername: "",
			MailPassword: "",
			MailHost:     "",
			MailPort:     465,
		}
		if err := db.Create(&record).Error; err != nil {
			global.App.Log.Fatal("seed notify settings failed", err.Error())
		}
		return
	}
	if err != nil {
		global.App.Log.Fatal("load notify settings failed", err.Error())
	}
}

func defaultNoticeURL() string {
	address := strings.TrimSpace(global.App.Config.App.ListenAddress)
	if address == "" {
		return ""
	}
	if strings.HasPrefix(address, "http://") || strings.HasPrefix(address, "https://") {
		return strings.TrimRight(address, "/")
	}
	return "http://" + strings.TrimRight(address, "/")
}

func initializeSystemParams(db *gorm.DB) {
	type seedParam struct {
		Title       string
		ConfigKey   string
		ConfigValue string
		ConfigType  string
		Description string
	}
	buildJSON := func(v any) string {
		data, _ := json.Marshal(v)
		return string(data)
	}
	seeds := []seedParam{
		{
			Title:       "允许直接执行 DDL 语句",
			ConfigKey:   "allow_sql_execute_ddl",
			ConfigValue: buildJSON(true),
			ConfigType:  "boolean",
			Description: "SQL 工作台是否允许用户直接运行 CREATE/ALTER/DROP/TRUNCATE 等 DDL",
		},
		{
			Title:       "允许直接执行 DML 语句",
			ConfigKey:   "allow_sql_execute_dml",
			ConfigValue: buildJSON(true),
			ConfigType:  "boolean",
			Description: "SQL 工作台是否允许用户直接运行 INSERT/UPDATE/DELETE/REPLACE 等 DML",
		},
		{
			Title:       "系统支持的数据库类型",
			ConfigKey:   "supported_db_types",
			ConfigValue: buildJSON([]string{"MySQL", "Oracle", "SQLServer", "TiDB", "Doris", "PostgreSQL", "ClickHouse"}),
			ConfigType:  "array",
			Description: "全局枚举白名单，实例/工单/备份等页面只允许从这里选择 DB 类型",
		},
		{
			Title:       "导出策略",
			ConfigKey:   "export_policy",
			ConfigValue: buildJSON(map[string]any{"mode": "allow_roles", "role_codes": []string{}}),
			ConfigType:  "object",
			Description: "页面导出行为：允许指定角色直接下载，或任何角色都走工单审批流程",
		},
		{
			Title:       "超级管理员绕过 DAS 访问权限",
			ConfigKey:   "das_allow_superuser_bypass",
			ConfigValue: buildJSON(false),
			ConfigType:  "boolean",
			Description: "若关闭，superuser/admin 同样需在数据访问页面配置授权后才能访问实例/库/表并执行 SQL",
		},
	}
	for _, seed := range seeds {
		var existing commonModels.InsightSystemParam
		err := db.Where("config_key = ?", seed.ConfigKey).First(&existing).Error
		if err == nil {
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			global.App.Log.Warnf("load system param %s failed: %v", seed.ConfigKey, err)
			continue
		}
		record := commonModels.InsightSystemParam{
			Title:       seed.Title,
			ConfigKey:   seed.ConfigKey,
			ConfigValue: seed.ConfigValue,
			ConfigType:  seed.ConfigType,
			Description: seed.Description,
		}
		if err := db.Create(&record).Error; err != nil {
			global.App.Log.Fatalf("seed system param %s failed: %v", seed.ConfigKey, err.Error())
		}
	}
}

// backfillMemberAccessPermission 给「普通成员」角色补上数据访问申请入口权限。
//
// 为什么需要单独回填：member 是「可编辑内置角色」，initializeBuiltinRoles 对已存在的
// member 不覆盖权限集（尊重管理员在 UI 上的调整），所以新增的 das.access.apply
// 只会写进新建库，存量库里普通用户直接 403，访问申请页等于不可用。
//
// 幂等口径：仅当 member 当前完全没有 das.access.* 授权时才补一条 apply，
// 补过一次之后管理员再怎么调整（包括删掉）都不会被反复覆盖。
func backfillMemberAccessPermission(db *gorm.DB, permCodeIDMap map[string]uint64) {
	applyID := permCodeIDMap["das.access.apply"]
	if applyID == 0 {
		return
	}
	var member usersModels.InsightRoles
	if err := db.Where("code = ? AND is_deleted = ?", usersModels.BuiltinRoleCodeMember, false).
		First(&member).Error; err != nil {
		return
	}
	var accessPermIDs []uint64
	if err := db.Model(&usersModels.InsightPermissions{}).
		Where("code LIKE ?", "das.access.%").
		Pluck("id", &accessPermIDs).Error; err != nil {
		global.App.Log.Warnf("load das.access permissions failed: %v", err)
		return
	}
	if len(accessPermIDs) == 0 {
		return
	}
	var bound int64
	if err := db.Model(&usersModels.InsightRolePermissions{}).
		Where("role_id = ? AND permission_id IN ?", member.ID, accessPermIDs).
		Count(&bound).Error; err != nil {
		global.App.Log.Warnf("count member das.access bindings failed: %v", err)
		return
	}
	if bound > 0 {
		return
	}
	if err := db.Create(&usersModels.InsightRolePermissions{RoleID: member.ID, PermissionID: applyID}).Error; err != nil {
		global.App.Log.Warnf("backfill member das.access.apply failed: %v", err)
		return
	}
	global.App.Log.Infof("backfill: 普通成员角色已补充 das.access.apply 权限")
}

func initializeBuiltinRoles(db *gorm.DB) {
	definitions := usersModels.BuiltinRoleDefinitions()
	permCodeIDMap := make(map[string]uint64)
	{
		type permRow struct {
			ID   uint64
			Code string
		}
		var rows []permRow
		if err := db.Table("insight_permissions").Select("id, code").Where("is_enabled = ?", true).Scan(&rows).Error; err != nil {
			global.App.Log.Warnf("load permissions for builtin roles failed: %v", err)
			return
		}
		for _, row := range rows {
			permCodeIDMap[row.Code] = row.ID
		}
	}

	// 『不可编辑、权限严格跟随代码走的内置角色』——系统管理员/全局只读（System Principal 硬编码入口），
	// 启动时会强制把 insight_role_permissions 关联和代码对齐（DELETE多余 + INSERT缺少），保证和代码里
	// BuiltinRoleDefinitions 的内置权限集严格一致。
	// 『可编辑内置角色』——普通成员 member / 只读 viewer：只在首次创建角色时写一次默认权限，
	// 后续若管理员在 UI 上直接增删 member 的权限、改名称/改描述，我们不再覆盖，保证"编辑后真正生效"。
	readonlyBuiltinCodes := map[string]struct{}{
		usersModels.BuiltinRoleCodeAdmin:          {},
		usersModels.BuiltinRoleCodeGlobalReadonly: {},
	}

	for _, def := range definitions {
		isReadonlyBuiltin := false
		if _, hit := readonlyBuiltinCodes[def.Code]; hit {
			isReadonlyBuiltin = true
		}

		var role usersModels.InsightRoles
		err := db.Where("code = ? AND is_deleted = ?", def.Code, false).First(&role).Error
		roleCreated := false
		if errors.Is(err, gorm.ErrRecordNotFound) {
			role = usersModels.InsightRoles{
				Code:        def.Code,
				Name:        def.Name,
				Description: def.Description,
				IsBuiltin:   true,
				IsDeleted:   false,
			}
			if err := db.Create(&role).Error; err != nil {
				global.App.Log.Warnf("seed builtin role `%s` failed: %v", def.Code, err)
				continue
			}
			roleCreated = true
		} else if err != nil {
			global.App.Log.Warnf("load builtin role `%s` failed: %v", def.Code, err)
			continue
		} else {
			// 描述/名称：仅当是"不可编辑内置角色"时，启动时强同步成代码里的默认值
			// （如果允许用户改 member 的名称/描述，这里就不能覆盖）
			if isReadonlyBuiltin && (role.Name != def.Name || role.Description != def.Description) {
				updates := map[string]interface{}{"name": def.Name, "description": def.Description}
				if err := db.Model(&role).Updates(updates).Error; err != nil {
					global.App.Log.Warnf("update builtin role `%s` meta failed: %v", def.Code, err)
				}
			}
		}

		permIDs := make([]uint64, 0, len(def.PermissionCodes))
		for _, code := range def.PermissionCodes {
			if id, ok := permCodeIDMap[code]; ok && id > 0 {
				permIDs = append(permIDs, id)
			}
		}

		if isReadonlyBuiltin || roleCreated {
			syncBuiltinRolePermissions(db, role.ID, permIDs)
		}
		// else: 可编辑内置角色（member/viewer）且角色已存在：不再同步权限，让管理员在 UI 上手动改的 role_permissions
		// 真正保留，不会每次启动被覆盖 —— 满足「编辑后要真正生效对应权限」的需求。
	}

	// 历史回填：数据访问申请入口权限。
	// member 属于「可编辑内置角色」，上面的循环对已存在的 member 不会覆盖权限集，
	// 而 das.access.* 是后加的权限点 ⇒ 存量库的 member 一条都没有，
	// 普通用户连「访问申请」页都打不开（403 缺少权限: das.access.apply）。
	backfillMemberAccessPermission(db, permCodeIDMap)

	// ========================================
	// 历史数据迁移：旧的内置角色（developer/operator/manager/system_admin/backup_viewer/audit_viewer）
	// 1) 软删除这些旧角色（is_builtin=true 置 is_deleted=true），并清关联
	// 2) admin 用户：如果绑定了旧 system_admin，迁移绑定到新的 admin(系统管理员) 角色
	// ========================================
	legacyBuiltinCodes := []string{"developer", "operator", "manager", "system_admin", "backup_viewer", "audit_viewer"}
	var legacyRoles []usersModels.InsightRoles
	if err := db.Where("code in ? AND is_deleted = ?", legacyBuiltinCodes, false).Find(&legacyRoles).Error; err != nil {
		global.App.Log.Warnf("load legacy builtin roles failed: %v", err)
	}
	for _, lr := range legacyRoles {
		legacyRoleID := lr.ID
		// 软删除旧内置角色
		if err := db.Model(&usersModels.InsightRoles{}).Where("id = ?", legacyRoleID).
			Update("is_deleted", true).Error; err != nil {
			global.App.Log.Warnf("soft delete legacy role (id=%d, code=%s) failed: %v", legacyRoleID, lr.Code, err)
			continue
		}
		// 清理旧角色的角色-权限关联
		if err := db.Where("role_id = ?", legacyRoleID).Delete(&usersModels.InsightRolePermissions{}).Error; err != nil {
			global.App.Log.Warnf("clean legacy role_permissions (role=%d, code=%s) failed: %v", legacyRoleID, lr.Code, err)
		}
		global.App.Log.Infof("migrate: legacy builtin role `%s` soft-deleted", lr.Code)
	}

	// 迁移 admin 用户角色绑定：旧 system_admin → 新 admin
	var adminUser usersModels.InsightUsers
	if err := db.Where("username = ? AND is_deleted = ?", "admin", false).First(&adminUser).Error; err == nil {
		var newAdminRole usersModels.InsightRoles
		if err := db.Where("code = ? AND is_deleted = ?", usersModels.BuiltinRoleCodeAdmin, false).
			First(&newAdminRole).Error; err == nil {
			// 1) 清掉 admin 用户所有旧 legacyBuiltinCodes 角色的绑定
			var legacyIDs []uint64
			for _, lr := range legacyRoles {
				legacyIDs = append(legacyIDs, lr.ID)
			}
			if len(legacyIDs) > 0 {
				if err := db.Where("uid = ? AND role_id in ?", adminUser.Uid, legacyIDs).
					Delete(&usersModels.InsightUserRoles{}).Error; err != nil {
					global.App.Log.Warnf("clean admin user legacy bindings failed: %v", err)
				}
			}
			// 2) 绑定新的 admin(系统管理员) 角色（若不存在）
			var existingBind usersModels.InsightUserRoles
			err2 := db.Where("uid = ? AND role_id = ?", adminUser.Uid, newAdminRole.ID).First(&existingBind).Error
			if errors.Is(err2, gorm.ErrRecordNotFound) {
				if err := db.Create(&usersModels.InsightUserRoles{
					UID:    adminUser.Uid,
					RoleID: newAdminRole.ID,
				}).Error; err != nil {
					global.App.Log.Warnf("bind admin user to new admin role failed: %v", err)
				} else {
					global.App.Log.Infof("migrate: admin user re-bound to new `admin(系统管理员)` role")
				}
			}
			// 3) admin 用户强制 is_superuser=true（新模型逻辑）
			if !adminUser.IsSuperuser {
				db.Model(&usersModels.InsightUsers{}).Where("uid = ?", adminUser.Uid).Update("is_superuser", true)
			}
		}
	}

	// 迁移所有普通用户：若绑定的角色全为已软删的 legacy 角色（developer / operator / ...）→ 自动换绑到新的 member（普通成员）角色
	// 防止像 hongj 这种用户登录后，所有权限都查不到 → 报 5 条「无权限访问」
	var newMemberRole usersModels.InsightRoles
	if err := db.Where("code = ? AND is_deleted = ?", usersModels.BuiltinRoleCodeMember, false).
		First(&newMemberRole).Error; err == nil && newMemberRole.ID > 0 {
		type userRoleBinding struct {
			UID      uint64
			RoleID   uint64
			IsDelete bool
		}
		var allBindings []userRoleBinding
		if err := db.Table("insight_user_roles ur").
			Select("ur.uid, ur.role_id, r.is_deleted").
			Joins("join insight_roles r on r.id = ur.role_id").
			Scan(&allBindings).Error; err == nil {
			legacyRoleIDSet := make(map[uint64]struct{}, len(legacyRoles))
			for _, lr := range legacyRoles {
				legacyRoleIDSet[lr.ID] = struct{}{}
			}
			// perUID 聚合：每个用户的有效（未软删）角色数
			perUID := make(map[uint64]struct {
				validRoleCount   int
				bindingIsAllOld  bool
				hasMemberAlready bool
			})
			for _, b := range allBindings {
				info := perUID[b.UID]
				// 是否是旧 legacy 的 role id（不管软删状态，只要是旧6角色就算"应该被替换"）
				_, isLegacyID := legacyRoleIDSet[b.RoleID]
				if b.RoleID == newMemberRole.ID {
					info.hasMemberAlready = true
				}
				if !b.IsDelete && !isLegacyID && b.RoleID != newMemberRole.ID {
					info.validRoleCount++
				}
				perUID[b.UID] = info
			}
			// 清掉所有用户对旧6角色的 ur 绑定，替换成 member
			legacyRoleIDsForQuery := make([]uint64, 0, len(legacyRoles))
			for _, lr := range legacyRoles {
				legacyRoleIDsForQuery = append(legacyRoleIDsForQuery, lr.ID)
			}
			if len(legacyRoleIDsForQuery) > 0 {
				if err := db.Where("role_id in ?", legacyRoleIDsForQuery).
					Delete(&usersModels.InsightUserRoles{}).Error; err != nil {
					global.App.Log.Warnf("clean old legacy user_role bindings failed: %v", err)
				} else {
					global.App.Log.Infof("migrate: cleaned up %d legacy role user bindings", len(legacyRoleIDsForQuery))
				}
			}
			// 对所有 "validRoleCount=0 且 还没 member 绑定"的用户，补 member
			migratedCount := 0
			for uid, info := range perUID {
				if uid == adminUser.Uid {
					continue
				}
				if info.validRoleCount == 0 && !info.hasMemberAlready {
					if err := db.Create(&usersModels.InsightUserRoles{
						UID:    uid,
						RoleID: newMemberRole.ID,
					}).Error; err == nil {
						migratedCount++
					}
				}
			}
			if migratedCount > 0 {
				global.App.Log.Infof("migrate: assigned %d users to default member role (legacy role clean-up)", migratedCount)
			}
		}
	}
}

func syncBuiltinRolePermissions(db *gorm.DB, roleID uint64, permIDs []uint64) {
	type rp struct {
		RoleID       uint64
		PermissionID uint64
	}
	var existing []rp
	if err := db.Table("insight_role_permissions").Select("role_id, permission_id").Where("role_id = ?", roleID).Scan(&existing).Error; err != nil {
		global.App.Log.Warnf("load existing role_permissions for role %d failed: %v", roleID, err)
		return
	}
	existingSet := make(map[uint64]struct{}, len(existing))
	for _, e := range existing {
		existingSet[e.PermissionID] = struct{}{}
	}
	wantSet := make(map[uint64]struct{}, len(permIDs))
	for _, id := range permIDs {
		wantSet[id] = struct{}{}
		if _, ok := existingSet[id]; !ok {
			if err := db.Create(&usersModels.InsightRolePermissions{RoleID: roleID, PermissionID: id}).Error; err != nil {
				global.App.Log.Warnf("insert role_permission (role=%d, perm=%d) failed: %v", roleID, id, err)
			}
		}
	}
	for id := range existingSet {
		if _, ok := wantSet[id]; !ok {
			if err := db.Where("role_id = ? AND permission_id = ?", roleID, id).Delete(&usersModels.InsightRolePermissions{}).Error; err != nil {
				global.App.Log.Warnf("delete role_permission (role=%d, perm=%d) failed: %v", roleID, id, err)
			}
		}
	}
}
