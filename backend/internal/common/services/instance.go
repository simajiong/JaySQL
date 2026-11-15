package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/pagination"
	"github.com/simajiong/jaysql/pkg/secret"
	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/common/dbfamily"
	"github.com/simajiong/jaysql/internal/common/forms"
	"github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/common/tasks"

	dasModels "github.com/simajiong/jaysql/internal/das/models"
	dasServices "github.com/simajiong/jaysql/internal/das/services"
	inspectModels "github.com/simajiong/jaysql/internal/inspect/models"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	perfModels "github.com/simajiong/jaysql/internal/performance/models"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type AdminGetInstancesServices struct {
	*forms.AdminGetInstancesForm
	C *gin.Context
}

func (s *AdminGetInstancesServices) Run() (responseData interface{}, total int64, err error) {
	type DBConfig struct {
		models.InsightInstances
		EnvironmentName string `json:"environment_name"`
		DataSourceID    string `json:"data_source_id"`
	}
	var dbs []DBConfig
	tx := global.App.DB.Select(`a.id,a.instance_id,a.hostname,a.port,a.user,a.use_type,a.db_type,a.extra_config,b.id as environment,
							b.name as environment_name, a.remark, a.created_by, a.last_checked_at, a.last_check_status, a.last_check_message, a.created_at, a.updated_at,
							a.deploy_provider, a.cloud_vendor, a.ha_mode, a.disk_type, a.db_version`).
		Table("insight_instances a").
		Joins("left join insight_instance_environments b on a.environment=b.id").
		Order("a.created_at desc, a.id desc")
	// 搜索
	if s.Search != "" {
		like := "%" + s.Search + "%"
		tx = tx.Where("a.hostname like ? or a.remark like ? or a.instance_id like ? or a.user like ? or a.created_by like ?", like, like, like, like, like)
	}
	if s.Environment != "" {
		tx = tx.Where("CAST(b.id as char)=? or b.name=?", s.Environment, s.Environment)
	}
	if s.DbType != "" {
		tx = tx.Where("a.db_type=?", s.DbType)
	}
	if s.Status != "" {
		tx = tx.Where("a.last_check_status=?", s.Status)
	}
	total = pagination.Pager(&s.PaginationQ, tx, &dbs)
	for i := range dbs {
		// extra_config 里的 SSH 凭据（明文或密文）都不该出接口，统一打码。
		// 前端编辑态本来就不回显这两项（留空=不修改），脱敏不影响任何交互。
		if len(dbs[i].ExtraConfig) > 0 {
			if masked, maskErr := secret.MaskJSON(dbs[i].ExtraConfig, instanceSecretJSONPaths); maskErr == nil {
				dbs[i].ExtraConfig = masked
			}
		}
		rawID := strings.TrimSpace(dbs[i].InstanceID)
		if utils.IsValidBizID(rawID) {
			dbs[i].DataSourceID = rawID
		} else if rawID == "" {
			dbs[i].DataSourceID = "ds-000000000000"
		} else {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(rawID, "-", ""), " ", ""))
			if len(normalized) >= 12 {
				dbs[i].DataSourceID = "ds-" + normalized[:12]
			} else {
				dbs[i].DataSourceID = "ds-" + normalized + strings.Repeat("0", 12-len(normalized))
			}
		}
	}
	return &dbs, total, nil
}

type AdminCreateInstancesService struct {
	*forms.AdminCreateInstancesForm
	C *gin.Context
}

var (
	hostnameLabelRE = regexp.MustCompile(`^[A-Za-z0-9-]{1,63}$`)
	ipLikeHostRE    = regexp.MustCompile(`^[0-9.]+$`)

	// instanceSecretJSONPaths 是数据源扩展参数里需要加密/脱敏的敏感路径。
	// 与 secret 默认清单保持一致，避免"加密了 A 路径、脱敏了 B 路径"的漂移。
	instanceSecretJSONPaths = []string{"$.ssh_password", "$.ssh_private_key"}
)

func validateInstanceHostname(hostname string) error {
	host := strings.TrimSpace(hostname)
	if host == "" {
		return fmt.Errorf("主机名或IP不能为空")
	}
	if strings.ContainsAny(host, " \r\n\t") {
		return fmt.Errorf("主机名或IP格式不合法（%q）", hostname)
	}
	if ip := net.ParseIP(host); ip != nil {
		return nil
	}
	// 纯数字+点的字符串按“IP字面量”处理；若 ParseIP 失败则明确判定非法，
	// 避免 192.1681.1.99 这类看似 IP 的错误地址被当作普通 hostname 接收。
	if ipLikeHostRE.MatchString(host) {
		return fmt.Errorf("IP地址格式不合法（%q）", hostname)
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !hostnameLabelRE.MatchString(label) {
			return fmt.Errorf("主机名或IP格式不合法（%q）", hostname)
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("主机名或IP格式不合法（%q）", hostname)
		}
	}
	return nil
}

func (s *AdminCreateInstancesService) Run() (*models.InsightInstances, error) {
	if err := validateInstanceHostname(s.Hostname); err != nil {
		return nil, err
	}
	var existing models.InsightInstances
	check := global.App.DB.Table("insight_instances").Where("hostname=? and port=?", s.Hostname, s.Port).Limit(1).Take(&existing)
	if check.Error == nil {
		return nil, fmt.Errorf("该实例已存在，请不要重复配置")
	}
	if check.Error != nil && !errors.Is(check.Error, gorm.ErrRecordNotFound) {
		return nil, check.Error
	}

	// 加密密码
	encryptPassword, err := secret.Encrypt(s.Password)
	if err != nil {
		return nil, err
	}
	extraConfig, err := normalizeInstanceExtraConfig(string(s.DbType), s.ExtraConfig, "create")
	if err != nil {
		return nil, err
	}
	// 新增记录
	db := models.InsightInstances{
		Hostname:        s.Hostname,
		Port:            s.Port,
		User:            s.User,
		Password:        encryptPassword,
		DbType:          s.DbType,
		ExtraConfig:     extraConfig,
		Environment:     s.Environment,
		Remark:          s.Remark,
		InstanceID:      utils.GenerateInstanceID(),
		UseType:         models.EnumType("查询"),
		CreatedBy:       currentAuditUsername(s.C),
		LastCheckStatus: "unknown",
		// 录入型画像字段：部署形态 / 云厂商 / 高可用模式
		DeployProvider: strings.TrimSpace(s.DeployProvider),
		CloudVendor:    strings.TrimSpace(s.CloudVendor),
		HaMode:         strings.TrimSpace(s.HaMode),
		DiskType:       strings.TrimSpace(s.DiskType),
	}
	// 新增记录：创建与「继承所属环境的 SQL 开发规范」在同一事务内完成，
	// 保证数据源一旦落入某环境，其规范绑定必然同步落库（不会出现「有环境无规范」）。
	err = global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&db).Error; err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
				return fmt.Errorf("该实例已存在，请不要重复配置")
			}
			return err
		}
		if err := syncInstanceStandardBinding(tx, db.InstanceID, db.Environment); err != nil {
			return err
		}
		// 【2026-10-15 收口】数据源对其他所有人默认不可见，必须经显式授权后才可访问；
		// 但创建人自己加的数据源自己不能选违背直觉，故在同一事务内给创建人
		// 自动写一条实例级授权（见 das/services/data_grant_auto.go）。
		if err := dasServices.AutoGrantInstanceToCreator(tx, db.InstanceID, db.CreatedBy); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// 新建即拉一次库清单：否则新数据源在工单提交、授权申请、资源树里是一张空下拉，
	// 用户只能干等定时任务（历史上最坏要 5 分钟）。这里异步触发，
	// 保存接口不会被目标库的连接耗时拖住；连不上时失败原因会落进
	// insight_instance_meta_state，页面据此提示而不是静默显示空列表。
	tasks.RefreshInstanceMetaAsync(db.InstanceID)
	return &db, nil
}

type AdminUpdateInstancesService struct {
	*forms.AdminUpdateInstancesForm
	C  *gin.Context
	ID uint64
}

func (s *AdminUpdateInstancesService) Run() error {
	if err := validateInstanceHostname(s.Hostname); err != nil {
		return err
	}
	var existing models.InsightInstances
	check := global.App.DB.Table("insight_instances").
		Where("hostname=? and port=? and id<>?", s.Hostname, s.Port, s.ID).
		Limit(1).
		Take(&existing)
	if check.Error == nil {
		return fmt.Errorf("该实例已存在，请不要重复配置")
	}
	if check.Error != nil && !errors.Is(check.Error, gorm.ErrRecordNotFound) {
		return check.Error
	}

	var target models.InsightInstances
	if err := global.App.DB.Table("insight_instances").Where("id = ?", s.ID).Take(&target).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("实例不存在或已被删除")
		}
		return err
	}
	if s.ExtraConfig == nil {
		s.ExtraConfig = map[string]any{}
	}
	if len(target.ExtraConfig) > 0 {
		var oldCfg map[string]any
		if json.Unmarshal(target.ExtraConfig, &oldCfg) == nil {
			mergeKeepOld := func(key string) {
				newVal, hasNew := s.ExtraConfig[key]
				if !hasNew || newVal == nil || strings.TrimSpace(fmt.Sprintf("%v", newVal)) == "" {
					if oldVal, ok := oldCfg[key]; ok {
						s.ExtraConfig[key] = oldVal
					}
				}
			}
			mergeKeepOld("ssh_user")
			mergeKeepOld("ssh_port")
			mergeKeepOld("ssh_password")
			mergeKeepOld("ssh_private_key")
			mergeKeepOld("database")
			mergeKeepOld("sslmode")
			mergeKeepOld("auth_source")
			mergeKeepOld("service_name")
			// OceanBase 租户模式：编辑态前端会回填旧值，这里兜底保留，
			// 避免「只改密码」这类提交把模式冲掉导致下次连接走错通道。
			mergeKeepOld("oceanbase_mode")
		}
	}

	updates := map[string]interface{}{
		"hostname":    s.Hostname,
		"port":        s.Port,
		"user":        s.User,
		"db_type":     s.DbType,
		"environment": s.Environment,
		"remark":      s.Remark,
	}
	// 录入型画像字段随编辑一起保存（清空即视为"未录入"）
	updates["deploy_provider"] = strings.TrimSpace(s.DeployProvider)
	updates["cloud_vendor"] = strings.TrimSpace(s.CloudVendor)
	updates["ha_mode"] = strings.TrimSpace(s.HaMode)
	updates["disk_type"] = strings.TrimSpace(s.DiskType)
	extraConfig, err := normalizeInstanceExtraConfig(string(s.DbType), s.ExtraConfig, "update")
	if err != nil {
		return err
	}
	updates["extra_config"] = extraConfig

	// 仅当传入非空 password 时才更新密码
	if s.Password != "" {
		encryptPassword, err := secret.Encrypt(s.Password)
		if err != nil {
			return err
		}
		updates["password"] = encryptPassword
	}

	// 更新记录 + 对齐规范继承（同一事务）：
	// 数据源只要归属于某个环境，就必须继承该环境的开发规范；改环境后同步改写绑定。
	err = global.App.DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.InsightInstances{}).Where("id=?", s.ID).Updates(updates)
		if result.Error != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(result.Error, &mysqlErr) && mysqlErr.Number == 1062 {
				return fmt.Errorf("该实例已存在，请不要重复配置")
			}
			return result.Error
		}
		return syncInstanceStandardBinding(tx, target.InstanceID, s.Environment)
	})
	if err != nil {
		return err
	}
	// 连接信息（主机/端口/账号/类型/库名）任何一项变化都可能落到另一套库清单上，
	// 这里不做字段级比对，直接异步重拉一次——单实例的代价远小于「改了地址还显示旧库」。
	tasks.RefreshInstanceMetaAsync(target.InstanceID)
	return nil
}

// AdminUpdateInstanceRemarkService 行内修改数据源名称（remark）。
// 只动 remark 一列：不校验 hostname/port 重复（没变），不触碰 extra_config 与密码，
// 避免行内快速改名把连接信息、SSH 配置一并覆盖。
type AdminUpdateInstanceRemarkService struct {
	C      *gin.Context
	ID     uint64
	Remark string
}

func (s *AdminUpdateInstanceRemarkService) Run() error {
	var target models.InsightInstances
	if err := global.App.DB.Table("insight_instances").Where("id = ?", s.ID).Take(&target).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("实例不存在或已被删除")
		}
		return err
	}
	remark := strings.TrimSpace(s.Remark)
	if len([]rune(remark)) < 2 {
		return fmt.Errorf("数据源名称至少 2 个字符")
	}
	if len([]rune(remark)) > 256 {
		return fmt.Errorf("数据源名称不能超过 256 个字符")
	}
	return global.App.DB.Table("insight_instances").Where("id = ?", s.ID).Update("remark", remark).Error
}

func updateInstanceConnectionStatus(id uint64, status, message string) {
	updates := map[string]any{
		"last_checked_at":    models.LocalTime(time.Now()),
		"last_check_status":  strings.TrimSpace(status),
		"last_check_message": strings.TrimSpace(message),
	}
	if updates["last_check_status"] == "" {
		updates["last_check_status"] = "unknown"
	}
	if err := global.App.DB.Model(&models.InsightInstances{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		global.App.Log.Warnf("update instance connection status failed: %v", err)
	}
}

func normalizeInstanceExtraConfig(dbType string, raw map[string]any, mode string) (datatypes.JSON, error) {
	cfg := map[string]any{}
	readString := func(key string) string {
		if raw == nil {
			return ""
		}
		v, ok := raw[key]
		if !ok || v == nil {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}

	switch dbType {
	case "PostgreSQL", "Kingbase":
		// 两者 extra_config 结构一致，只有默认库不同：PG 是 postgres，金仓初始化后自带 test。
		// 默认库必须落库，否则连接测试与元数据同步会各自兜底成不同值。
		database := readString("database")
		if database == "" {
			database = "postgres"
			if dbType == "Kingbase" {
				database = "test"
			}
		}
		cfg["database"] = database
		sslmode := readString("sslmode")
		if sslmode == "" {
			sslmode = "disable"
		}
		cfg["sslmode"] = sslmode
		if err := mergeSSHFieldsIntoExtraConfig(cfg, readString, raw); err != nil {
			return nil, err
		}
	case "MySQL", "TiDB", "Doris":
		if err := mergeSSHFieldsIntoExtraConfig(cfg, readString, raw); err != nil {
			return nil, err
		}
	case "MongoDB":
		authSource := readString("auth_source")
		if authSource == "" {
			authSource = "admin"
		}
		cfg["auth_source"] = authSource
	case "Oracle":
		serviceName := readString("service_name")
		if serviceName == "" {
			return nil, fmt.Errorf("Oracle 服务名不能为空")
		}
		cfg["service_name"] = serviceName
		// Oracle 主机（Linux / AIX / Windows 都有）支持主机登录，SSH 凭据必须落库
		if err := mergeSSHFieldsIntoExtraConfig(cfg, readString, raw); err != nil {
			return nil, err
		}
	case "OceanBase":
		/* OceanBase 一个 db_type 对应两种租户模式，模式必须落库，
		   否则后续「连接测试 / 元数据同步 / SQL 工作台」都判不出该走 MySQL 还是 Oracle 通道。
		   口径（与前端「租户模式」单选一致）：
		     - oceanbase_mode 归一化后只允许 mysql / oracle，缺省回落 mysql；
		     - oracle 模式必须有 service_name（OceanBase 的 service name = 租户名）；
		     - mysql 模式可选 database（默认库），留空表示不指定。 */
		mode := dbfamily.NormalizeMode(readString("oceanbase_mode"))
		if mode == "" {
			mode = dbfamily.OceanBaseMySQL
		}
		cfg["oceanbase_mode"] = mode
		if mode == dbfamily.OceanBaseOracle {
			serviceName := readString("service_name")
			if serviceName == "" {
				return nil, fmt.Errorf("OceanBase（Oracle 模式）服务名不能为空，请填写目标租户名")
			}
			cfg["service_name"] = serviceName
		} else if database := strings.TrimSpace(readString("database")); database != "" {
			cfg["database"] = database
		}
	case "SQLServer":
		// 必须 trim：表单里敲几个空格也算"填了"，不 trim 会把 "   " 当库名落库，
		// 连接时 sqlserver 驱动会把库名原样带进 DSN，登录直接失败。
		database := strings.TrimSpace(readString("database"))
		if database == "" {
			database = "master"
		}
		cfg["database"] = database
		// SQL Server 多跑在 Windows 主机上，主机登录走 OpenSSH for Windows，凭据同样要落库
		if err := mergeSSHFieldsIntoExtraConfig(cfg, readString, raw); err != nil {
			return nil, err
		}
	default:
		// 保持现有类型兼容，不写额外参数
	}

	// 主机 SSH 是「选填」：监控采集全链路不使用 SSH（服务端直连数据库 + Prometheus 拉主机指标），
	// 只有备份/恢复与主机侧操作需要它。
	//
	// 变更说明（为什么删掉这里的硬报错）：此前新建态对 MySQL/PG/TiDB/Doris 强制要求
	// ssh_user + ssh_port + 凭据「三件套」，而前端早已把该区块标为「选填」并放开了必填，
	// 后端没跟上 —— 直接后果就是「整块留空点确定保存不了」，且报错来自服务端、用户在
	// 表单里看不到任何字段级提示，观感是「点了没反应」。
	// 现在改为只记一条 Warn：半截配置（例如填了 ssh_user 却没凭据）不再拦截保存，
	// 缺陷由 instance_privilege_check.go 的 probeOptionalSSHForBackup 与备份侧预检给出明确提示
	// （语义：不影响监控采集，备份任务会在凭据校验环节报错）。
	if strings.EqualFold(strings.TrimSpace(mode), "create") && hasIncompleteSSHCredential(cfg) {
		global.App.Log.Warnf("新建 %s 实例的主机 SSH 信息不完整（已按选填放行）：监控采集不受影响；"+
			"使用备份/恢复前需在数据源扩展参数中补全 ssh_user 与 ssh_password（或 ssh_private_key）",
			strings.TrimSpace(dbType))
	}

	if len(cfg) == 0 {
		return nil, nil
	}
	buf, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(buf), nil
}

// hasIncompleteSSHCredential 判断主机 SSH 是否处于「填了一半」的状态：
// 填了 ssh_user / ssh_password / ssh_private_key 中任意一个，却缺少可用的登录凭据。
//
// 注意口径：
//   - 三项全空 = 「未启用 SSH」（选填整块留空），不算不完整，也不应拦截保存；
//   - 只有 ssh_port 不留空不算启用 SSH —— 端口本身在表单里有默认值 22，
//     仅凭端口无法判断用户是否真的想开 SSH，因此不参与判断。
func hasIncompleteSSHCredential(cfg map[string]any) bool {
	readStr := func(key string) string {
		v, ok := cfg[key]
		if !ok || v == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	sshUser := readStr("ssh_user")
	sshPassword := readStr("ssh_password")
	sshPrivateKey := readStr("ssh_private_key")
	if sshUser == "" && sshPassword == "" && sshPrivateKey == "" {
		return false
	}
	if sshUser == "" {
		return true
	}
	return sshPassword == "" && sshPrivateKey == ""
}

func mergeSSHFieldsIntoExtraConfig(cfg map[string]any, readString func(key string) string, raw map[string]any) error {
	const (
		maxSSHUser       = 128
		maxSSHPassword   = 1024
		maxSSHPrivateKey = 8192
	)
	sshUser := strings.TrimSpace(readString("ssh_user"))
	if sshUser != "" {
		if len(sshUser) > maxSSHUser {
			return fmt.Errorf("SSH 用户长度不能超过 %d", maxSSHUser)
		}
		if strings.ContainsAny(sshUser, "\r\n\t") {
			return fmt.Errorf("SSH 用户不能包含换行或制表符")
		}
		cfg["ssh_user"] = sshUser
	}
	rawSSHPort := readString("ssh_port")
	if strings.TrimSpace(rawSSHPort) != "" {
		port64, err := strconv.ParseInt(strings.TrimSpace(rawSSHPort), 10, 64)
		if err != nil {
			// 允许 number 类型从 raw 重新取一次
			var fromRaw bool
			if raw != nil {
				if v, ok := raw["ssh_port"]; ok {
					switch typed := v.(type) {
					case float64:
						port64 = int64(typed)
						fromRaw = true
					case float32:
						port64 = int64(typed)
						fromRaw = true
					case int64:
						port64 = typed
						fromRaw = true
					case int:
						port64 = int64(typed)
						fromRaw = true
					}
				}
			}
			if !fromRaw {
				return fmt.Errorf("SSH 端口号必须为整数")
			}
		}
		if port64 < 1 || port64 > 65535 {
			return fmt.Errorf("SSH 端口号范围为 1-65535")
		}
		cfg["ssh_port"] = int(port64)
	}
	// SSH 凭据入库前必须加密。
	//
	// 历史问题：extra_config 是 JSON 列，这里的 ssh_password / ssh_private_key 此前直接以明文写入，
	// 而 insight_instances.password 一直是加密的 —— 同一张表里两种口径，靠"记得调 Encrypt"维持，
	// 漏了也不报错。现在统一走 secret.Encrypt：
	//   - 幂等：传入已是密文的值（例如编辑时回传库里的原值）原样保留，不会双重加密；
	//   - 兼容：读取侧 secret.Decrypt 遇到历史明文会原样返回，所以存量数据无需先迁移再发版。
	sshPassword := readString("ssh_password")
	if sshPassword != "" {
		if len(sshPassword) > maxSSHPassword {
			return fmt.Errorf("SSH 密码不能超过 %d 个字符", maxSSHPassword)
		}
		encrypted, err := secret.Encrypt(sshPassword)
		if err != nil {
			return fmt.Errorf("SSH 密码加密失败: %w", err)
		}
		// 幂等：编辑态回传库里的密文时原样保留，不会双重加密
		cfg["ssh_password"] = encrypted
	}
	sshPrivateKey := strings.TrimSpace(readString("ssh_private_key"))
	if sshPrivateKey != "" {
		if len(sshPrivateKey) > maxSSHPrivateKey {
			return fmt.Errorf("SSH 私钥长度不能超过 %d 个字符", maxSSHPrivateKey)
		}
		encrypted, err := secret.Encrypt(sshPrivateKey)
		if err != nil {
			return fmt.Errorf("SSH 私钥加密失败: %w", err)
		}
		cfg["ssh_private_key"] = encrypted
	}
	return nil
}

type AdminDeleteInstanceConfigsService struct {
	C  *gin.Context
	ID uint64
	// Summary 记录本次实际清理掉的行数，供视图层写进审计（成功后才有值）。
	Summary InstanceCleanupSummary
}

// InstanceBlockingResource 描述一条「阻止删除数据源」的关联资源。
type InstanceBlockingResource struct {
	Kind     string `json:"kind"`               // backup_task / backup_run / restore_run
	ID       string `json:"id"`                 // 备份任务业务号 bk-xxx / 执行记录主键
	Name     string `json:"name"`               // 展示名（任务名，缺失时回落 ID）
	Status   string `json:"status,omitempty"`   // 执行状态（任务条目为空）
	Active   bool   `json:"active"`             // 是否处于执行中（PENDING/RUNNING/STOP_REQUESTED）
	Relation string `json:"relation,omitempty"` // 恢复记录：source（源实例）/ target（目标实例）
}

// InstanceInUseError 数据源仍被备份/恢复任务引用时的删除拦截。
//
// 视图层用 errors.As 判定后走 response.FailWithData，
// 前端据此弹「无法删除」窗口并给出「去删除备份任务」的跳转入口。
type InstanceInUseError struct {
	ID          uint64                     `json:"id"`          // insight_instances 主键（前端跳转按主键过滤）
	InstanceID  string                     `json:"instance_id"` // 业务实例号
	HostPort    string                     `json:"host_port"`
	BackupTasks []InstanceBlockingResource `json:"backup_tasks"`
	RestoreRuns []InstanceBlockingResource `json:"restore_runs"`
	ActiveItems []InstanceBlockingResource `json:"active_items"`
}

// ActiveBlockers 汇总所有「执行中」的条目：在途备份执行 + 在途恢复记录。
// 恢复记录同时也在 RestoreRuns 里（带 status/active），这里只做聚合计数，不再重复落库。
func (e *InstanceInUseError) ActiveBlockers() []InstanceBlockingResource {
	if e == nil {
		return nil
	}
	active := make([]InstanceBlockingResource, 0, len(e.ActiveItems)+len(e.RestoreRuns))
	active = append(active, e.ActiveItems...)
	for _, item := range e.RestoreRuns {
		if item.Active {
			active = append(active, item)
		}
	}
	return active
}

func (e *InstanceInUseError) Error() string {
	if e == nil {
		return "数据源仍被关联资源占用，无法删除"
	}
	parts := make([]string, 0, 3)
	if active := e.ActiveBlockers(); len(active) > 0 {
		parts = append(parts, fmt.Sprintf("存在 %d 项执行中的备份/恢复，无法删除，请先终止或等待其结束：%s",
			len(active), describeBlockingResources(active)))
	}
	if n := len(e.BackupTasks); n > 0 {
		parts = append(parts, fmt.Sprintf("关联 %d 个备份任务，无法删除，请先修改或删除对应任务后再操作：%s",
			n, describeBlockingResources(e.BackupTasks)))
	}
	if n := len(e.RestoreRuns); n > 0 {
		parts = append(parts, fmt.Sprintf("关联 %d 条恢复记录，无法删除，请先处理对应恢复记录：%s",
			n, describeBlockingResources(e.RestoreRuns)))
	}
	if len(parts) == 0 {
		return "数据源仍被关联资源占用，无法删除"
	}
	return "该数据源" + strings.Join(parts, "；")
}

// HasBlocker 是否真的有阻塞项（无阻塞时不应把错误抛给调用方）。
func (e *InstanceInUseError) HasBlocker() bool {
	if e == nil {
		return false
	}
	return len(e.BackupTasks) > 0 || len(e.RestoreRuns) > 0 || len(e.ActiveItems) > 0
}

// 阻塞项最多回显几个：错误文案与弹窗都只列前若干条，避免长任务名把日志/提示撑爆。
const instanceDeleteBlockerDisplayLimit = 5

func describeBlockingResources(items []InstanceBlockingResource) string {
	if len(items) == 0 {
		return ""
	}
	shown := items
	truncated := 0
	if len(items) > instanceDeleteBlockerDisplayLimit {
		shown = items[:instanceDeleteBlockerDisplayLimit]
		truncated = len(items) - instanceDeleteBlockerDisplayLimit
	}
	labels := make([]string, 0, len(shown)+1)
	for _, item := range shown {
		label := strings.TrimSpace(item.Name)
		if label == "" {
			label = item.ID
		}
		if item.ID != "" && item.ID != label {
			label = fmt.Sprintf("%s（%s）", label, item.ID)
		}
		if item.Status != "" {
			label = fmt.Sprintf("%s[%s]", label, item.Status)
		}
		labels = append(labels, label)
	}
	text := strings.Join(labels, "、")
	if truncated > 0 {
		text = fmt.Sprintf("%s 等 %d 项", text, len(items))
	}
	return text
}

// instanceDeleteActiveRunStatuses 判定「执行还挂在队列里」的状态集合。
//
// 以「删除备份任务」的收口语义（PENDING / RUNNING / STOP_REQUESTED）为基线，
// 再补上备份执行记录真实存在的两个存活态：
//   - PENDING_PREPARE：已受理、正在做执行前准备；
//   - STOPPING：正在停，尚未真正落地。
//
// 这两态若不算「执行中」，就会出现「以为没在跑、删了数据源」的窗口；
// 宁可挡住让人先去处理，也不要放行一个还在准备/收尾的实例删除。
var instanceDeleteActiveRunStatuses = []string{
	"PENDING", "PENDING_PREPARE", "RUNNING", "STOPPING", "STOP_REQUESTED",
}

// collectInstanceDeleteBlockers 汇总指向该实例的备份任务与恢复记录。
//
// 口径上有两处必须照抄注意：
//  1. 备份域的关联列存的是 insight_instances.id（主键），不是业务号 instance_id
//     —— 这也是前端跳转要带主键的原因。
//  2. insight_backup_tasks 是软删除模型：查「是否还有任务」必须显式带 deleted_at IS NULL，
//     否则历史软删记录会让数据源永远删不掉（现网已有大量这种历史行）。
func collectInstanceDeleteBlockers(tx *gorm.DB, instancePK uint64, instanceID string, hostPort string) (*InstanceInUseError, error) {
	blocking := &InstanceInUseError{ID: instancePK, InstanceID: instanceID, HostPort: hostPort}
	if instancePK == 0 {
		return blocking, nil
	}

	var tasks []struct {
		TaskID string
		Name   string
	}
	if err := tx.Table("insight_backup_tasks").
		Select("task_id, name").
		Where("source_instance_id = ? AND deleted_at IS NULL", instancePK).
		Order("id desc").
		Limit(200).
		Scan(&tasks).Error; err != nil {
		return nil, fmt.Errorf("检查备份任务占用失败: %w", err)
	}
	for _, task := range tasks {
		name := strings.TrimSpace(task.Name)
		if name == "" {
			name = strings.TrimSpace(task.TaskID)
		}
		blocking.BackupTasks = append(blocking.BackupTasks, InstanceBlockingResource{
			Kind: "backup_task",
			ID:   strings.TrimSpace(task.TaskID),
			Name: name,
		})
	}

	// 在途的备份执行记录：任务可能已被改名/删除，但记录还在跑，一样要挡住。
	var runs []struct {
		ID     uint64
		TaskID string
		Status string
	}
	if err := tx.Table("insight_backup_runs").
		Select("id, task_id, status").
		Where("source_instance_id = ? AND status IN ?", instancePK, instanceDeleteActiveRunStatuses).
		Order("id desc").
		Limit(200).
		Scan(&runs).Error; err != nil {
		return nil, fmt.Errorf("检查备份执行记录失败: %w", err)
	}
	for _, run := range runs {
		blocking.ActiveItems = append(blocking.ActiveItems, InstanceBlockingResource{
			Kind:   "backup_run",
			ID:     strconv.FormatUint(run.ID, 10),
			Name:   strings.TrimSpace(run.TaskID),
			Status: strings.TrimSpace(run.Status),
			Active: true,
		})
	}

	// 恢复记录：源实例或目标实例命中都算占用（恢复记录是历史凭证，被删的实例会让详情断链）。
	var restores []struct {
		ID               uint64
		Name             string
		Status           string
		SourceInstanceID uint64
		TargetInstanceID uint64
	}
	if err := tx.Table("insight_restore_runs").
		Select("id, name, status, source_instance_id, target_instance_id").
		Where("source_instance_id = ? OR target_instance_id = ?", instancePK, instancePK).
		Order("id desc").
		Limit(200).
		Scan(&restores).Error; err != nil {
		return nil, fmt.Errorf("检查恢复记录占用失败: %w", err)
	}
	for _, run := range restores {
		relation := "source"
		if run.TargetInstanceID == instancePK && run.SourceInstanceID != instancePK {
			relation = "target"
		}
		name := strings.TrimSpace(run.Name)
		if name == "" {
			name = fmt.Sprintf("恢复记录 #%d", run.ID)
		}
		item := InstanceBlockingResource{
			Kind:     "restore_run",
			ID:       strconv.FormatUint(run.ID, 10),
			Name:     name,
			Status:   strings.TrimSpace(run.Status),
			Relation: relation,
			// 在途标记由 Error()/ActiveBlockers() 聚合，这里不重复塞进 ActiveItems，
			// 否则同一条恢复记录会在弹窗里出现两次。
			Active: isInstanceDeleteActiveRunStatus(run.Status),
		}
		blocking.RestoreRuns = append(blocking.RestoreRuns, item)
	}

	return blocking, nil
}

func isInstanceDeleteActiveRunStatus(status string) bool {
	target := strings.TrimSpace(strings.ToUpper(status))
	for _, item := range instanceDeleteActiveRunStatuses {
		if item == target {
			return true
		}
	}
	return false
}

// InstanceCleanupSummary 记录删除数据源时各子表的实际清理行数。
// 权限回收是「连带副作用」，单独露出来便于审计核对"删实例到底回收了多少授权"。
type InstanceCleanupSummary struct {
	SQLStandardBindings   int64 `json:"sql_standard_bindings"`
	InstanceSchemas       int64 `json:"instance_schemas"`
	DasSchemaPerms        int64 `json:"das_schema_perms"`
	DasTablePerms         int64 `json:"das_table_perms"`
	DasDataGrants         int64 `json:"das_data_grants"`
	MonitorTargetConfigs  int64 `json:"monitor_target_configs"`
	SlowLogCollectors     int64 `json:"slowlog_collectors"`
	InspectInstanceParams int64 `json:"inspect_instance_params"`
}

// RevokedPermissions 本次自动回收的访问权限行数。
//
// 口径说明：新模型的 insight_das_data_grants 是**当前唯一判权依据**，
// 删实例时必须一并清掉，否则会留下「指向已删实例的孤儿授权」——
// 它不会再被任何可见性过滤命中（实例已不存在），但会永久污染授权表与审计对账。
func (s InstanceCleanupSummary) RevokedPermissions() int64 {
	return s.DasSchemaPerms + s.DasTablePerms + s.DasDataGrants
}

// deleteInstanceRelatedConfigs 删除数据源时，同步清理「外键挂在子表侧」的实例级配置。
//
// 必须先分清两类关联，它们的清理方式天然不同：
//  1. 环境关联：外键列是 insight_instances.environment，外键在**实例表自己这一侧**。
//     删掉实例行，关联关系就随行消失，不需要任何额外清理 —— 这正是
//     「环境管理里关联的条目会同步消失」的原因，它不是级联删除，是主记录没了。
//  2. 规范绑定等：外键列是 insight_instance_sql_standard_bindings.instance_id
//     这类列，外键在**子表这一侧**，实例行删了子表行还在，就变成孤儿数据。
//
// 元库没有任何物理外键（information_schema 里 FOREIGN KEY 数量为 0），
// 模型上也没有 constraint:OnDelete 标签，所以第 2 类只能靠服务层显式清理。
// 不清理的直接后果：「SQL规范」页的 instance_count 与实例列表会把已删除的
// 实例继续算进来，和「数据源管理」「环境管理」对不上。
//
// 一致性口径与 AdminDeleteEnvironmentService / AdminSQLStandardDeleteService 保持一致：
// 同一事务内「先清子表、再删主表」，任一步失败整体回滚。
//
// 返回各表实际清理行数：其中 DasSchemaPerms / DasTablePerms 就是
// 「用户对该实例的访问权限」，删除数据源时随实例一并自动回收，
// 调用方把它写进审计，便于事后核对"回收了多少授权"。
func deleteInstanceRelatedConfigs(tx *gorm.DB, instanceID string) (InstanceCleanupSummary, error) {
	var summary InstanceCleanupSummary
	instanceID = strings.TrimSpace(instanceID)
	// 空 instance_id 时直接返回，绝不降级成 "where instance_id = ''" 之类的兜底清理：
	// 宁可留下孤儿等人工处理，也不能因为一次异常数据把其它实例的关联配置误删掉。
	if instanceID == "" {
		return summary, nil
	}

	// 1) SQL 规范绑定：本次不一致问题的直接来源
	result := tx.Where("instance_id = ?", instanceID).
		Delete(&inspectModels.InsightInstanceSQLStandardBinding{})
	if result.Error != nil {
		return summary, fmt.Errorf("清理SQL规范绑定失败: %w", result.Error)
	}
	summary.SQLStandardBindings = result.RowsAffected

	// 2) 实例库表元数据缓存
	result = tx.Where("instance_id = ?", instanceID).
		Delete(&models.InsightInstanceSchemas{})
	if result.Error != nil {
		return summary, fmt.Errorf("清理实例库表元数据失败: %w", result.Error)
	}
	summary.InstanceSchemas = result.RowsAffected

	// 3) DAS 库权限 / 表权限（即"用户访问权限"）
	result = tx.Where("instance_id = ?", instanceID).
		Delete(&dasModels.InsightDasSchemaPerms{})
	if result.Error != nil {
		return summary, fmt.Errorf("清理DAS库权限失败: %w", result.Error)
	}
	summary.DasSchemaPerms = result.RowsAffected

	result = tx.Where("instance_id = ?", instanceID).
		Delete(&dasModels.InsightDasTablePerms{})
	if result.Error != nil {
		return summary, fmt.Errorf("清理DAS表权限失败: %w", result.Error)
	}
	summary.DasTablePerms = result.RowsAffected

	// 3.3) 细粒度数据授权（insight_das_data_grants，当前唯一判权依据）
	//
	// 【2026-10-15 补齐】这张表是细粒度授权改造时新增的，但当时漏了级联清理，
	// 导致删除数据源后授权行残留成孤儿。新增任何以 instance_id 关联的子表时，
	// 必须同步在这里补清理——元库没有物理外键，不会有人兜底。
	//
	// 审计流水 insight_das_grant_audit 按约定**不随实例删除**（流水类保留作证据），
	// 它自带 grant_no / 主体 / 资源描述，删了授权行也能追溯。
	result = tx.Where("instance_id = ?", instanceID).
		Delete(&dasModels.InsightDasDataGrant{})
	if result.Error != nil {
		return summary, fmt.Errorf("清理细粒度数据授权失败: %w", result.Error)
	}
	summary.DasDataGrants = result.RowsAffected

	// 4) 监控采集配置（含采集策略缓存列）
	result = tx.Where("instance_id = ?", instanceID).
		Delete(&monitorModels.InsightMonitorTargetConfigs{})
	if result.Error != nil {
		return summary, fmt.Errorf("清理监控采集配置失败: %w", result.Error)
	}
	summary.MonitorTargetConfigs = result.RowsAffected

	// 5) 慢日志采集器
	result = tx.Where("instance_id = ?", instanceID).
		Delete(&perfModels.InsightPerformanceSlowLogCollectors{})
	if result.Error != nil {
		return summary, fmt.Errorf("清理慢日志采集器失败: %w", result.Error)
	}
	summary.SlowLogCollectors = result.RowsAffected

	// 6) 审核实例参数：该表无 GORM 模型（遗留表），用表名直删
	result = tx.Exec("DELETE FROM insight_inspect_instance_params WHERE instance_id = ?", instanceID)
	if result.Error != nil {
		return summary, fmt.Errorf("清理审核实例参数失败: %w", result.Error)
	}
	summary.InspectInstanceParams = result.RowsAffected

	return summary, nil
}

func (s *AdminDeleteInstanceConfigsService) Run() error {
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		// 先定位主记录并拿到业务 instance_id（char(15)），后续子表清理全部以它为条件。
		// 查不到就直接失败，避免拿着空 ID 去删子表。
		var instance models.InsightInstances
		if err := tx.Select("id", "instance_id", "hostname", "port").Where("id = ?", s.ID).First(&instance).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("数据源不存在或已被删除")
			}
			return err
		}

		// 前置校验：备份任务 / 恢复记录 / 在途执行还引用着这个实例时，拒绝删除。
		//
		// 为什么必须"先拦后删"而不是顺带清理：
		// 备份任务是用户资产（含调度计划、产物目录、保留期），恢复记录是不可改写的执行凭证，
		// 由一次"删数据源"的点击把它们一并抹掉，破坏性被放大了好几倍；
		// 反过来不拦又会留下"实例已删、任务还在"的脏状态（调度报 instance_missing、列表数据源为空）。
		// 因此这里只负责挡住，清理动作留给用户在自己确认过的入口里做。
		blocking, err := collectInstanceDeleteBlockers(
			tx,
			instance.ID,
			instance.InstanceID,
			fmt.Sprintf("%s:%d", strings.TrimSpace(instance.Hostname), instance.Port),
		)
		if err != nil {
			return err
		}
		if blocking.HasBlocker() {
			return blocking
		}

		// 先清子表：这些表的外键在子表侧，不清理就会留下孤儿。
		// 其中库/表权限行即"该实例的用户访问权限"，在此随实例一并自动回收；
		// 回收是终态：DAS 的自愈补写都 INNER 关联 insight_instances，实例行没了就不会再补回来。
		summary, err := deleteInstanceRelatedConfigs(tx, instance.InstanceID)
		if err != nil {
			return err
		}

		// 再删主表。RowsAffected == 0 说明并发下已被删除，
		// 此时返回错误让事务回滚，避免「主记录没删掉、配置却先被清了」的半截状态。
		result := tx.Where("id = ?", s.ID).Delete(&models.InsightInstances{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("数据源不存在或已被删除")
		}
		s.Summary = summary
		return nil
	})
}

type BatchImportInstanceRowResult struct {
	Line     int                    `json:"line"`
	Remark   string                 `json:"remark"`
	Key      string                 `json:"key"`
	Status   string                 `json:"status"`
	Reason   string                 `json:"reason,omitempty"`
	ID       uint64                 `json:"id,omitempty"`
	Instance string                 `json:"instance_id,omitempty"`
	Payload  map[string]interface{} `json:"payload,omitempty"`
}

type AdminBatchImportInstancesService struct {
	*forms.AdminBatchImportInstancesForm
	C *gin.Context
}

func (s *AdminBatchImportInstancesService) Run() (map[string]interface{}, error) {
	rows, err := s.prepareRows()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("未解析到任何有效的实例行")
	}
	if len(rows) > 500 {
		return nil, fmt.Errorf("单次批量导入最多允许 500 行，当前 %d 行", len(rows))
	}

	var allEnvs []struct {
		ID   int
		Name string
	}
	if err := global.App.DB.Table("insight_instance_environments").Select("id, name").Scan(&allEnvs).Error; err != nil {
		return nil, err
	}
	envByName := map[string]int{}
	envByID := map[int]string{}
	for _, e := range allEnvs {
		envByName[strings.ToLower(strings.TrimSpace(e.Name))] = e.ID
		envByID[e.ID] = e.Name
	}
	_ = envByID

	dbTypeWhitelist := map[string]bool{
		"MySQL":      true,
		"PostgreSQL": true,
	}

	var existingList []struct {
		Hostname string
		Port     int
		ID       uint64
	}
	if err := global.App.DB.Table("insight_instances").Select("hostname, port, id").Scan(&existingList).Error; err != nil {
		return nil, err
	}
	existsByKey := map[string]uint64{}
	for _, it := range existingList {
		existsByKey[fmt.Sprintf("%s:%d", strings.ToLower(it.Hostname), it.Port)] = it.ID
	}

	results := make([]BatchImportInstanceRowResult, 0, len(rows))
	successCount, skippedCount, failedCount := 0, 0, 0

	policy := strings.TrimSpace(s.DuplicatePolicy)
	if policy == "" {
		policy = "skip"
	}

	for i, raw := range rows {
		lineNo := i + 2
		payload, convErr := s.normalizeRow(raw)
		result := BatchImportInstanceRowResult{
			Line:    lineNo,
			Remark:  stringValue(payload, "remark"),
			Key:     fmt.Sprintf("%s:%v", stringValue(payload, "hostname"), rawValue(payload, "port")),
			Status:  "failed",
			Payload: raw,
		}
		if convErr != nil {
			result.Reason = convErr.Error()
			failedCount++
			results = append(results, result)
			continue
		}

		envName := strings.ToLower(strings.TrimSpace(stringValue(payload, "environment_name")))
		if envName == "" {
			envIDRaw := rawValue(payload, "environment")
			switch v := envIDRaw.(type) {
			case float64:
				envID := int(v)
				if _, ok := envByID[envID]; ok {
					payload["environment"] = envID
				}
			case int:
				if _, ok := envByID[v]; ok {
					payload["environment"] = v
				}
			}
		} else {
			id, ok := envByName[envName]
			if !ok {
				result.Reason = fmt.Sprintf("环境「%s」不存在或未启用", stringValue(payload, "environment_name"))
				failedCount++
				results = append(results, result)
				continue
			}
			payload["environment"] = id
		}

		if _, ok := payload["environment"]; !ok {
			result.Reason = "缺少环境字段或环境名称无法匹配"
			failedCount++
			results = append(results, result)
			continue
		}

		dbType := strings.TrimSpace(stringValue(payload, "db_type"))
		if !dbTypeWhitelist[dbType] {
			result.Reason = fmt.Sprintf("当前批量导入仅支持 MySQL / PostgreSQL，不支持「%s」", dbType)
			failedCount++
			results = append(results, result)
			continue
		}

		hostname := strings.TrimSpace(stringValue(payload, "hostname"))
		port := 0
		switch v := rawValue(payload, "port").(type) {
		case float64:
			port = int(v)
		case int:
			port = v
		case string:
			if num, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				port = num
			}
		}
		if hostname == "" || port < 1 || port > 65535 {
			result.Reason = "连接地址（主机/端口）格式非法"
			failedCount++
			results = append(results, result)
			continue
		}
		key := fmt.Sprintf("%s:%d", strings.ToLower(hostname), port)
		result.Key = key
		result.Remark = stringValue(payload, "remark")

		if existingID, dup := existsByKey[key]; dup {
			if policy == "skip" {
				result.Status = "skipped"
				result.Reason = "hostname + port 已存在，按策略跳过"
				result.ID = existingID
				skippedCount++
				results = append(results, result)
				continue
			}
			if policy == "abort" {
				return nil, fmt.Errorf("检测到重复实例 %s，按 abort 策略已终止整批导入", key)
			}
			if policy == "update" {
				updated, updateErr := s.updateExisting(existingID, payload)
				if updateErr != nil {
					result.Reason = updateErr.Error()
					failedCount++
					results = append(results, result)
					continue
				}
				result.Status = "success"
				result.ID = existingID
				result.Instance = updated.InstanceID
				result.Reason = "原记录已存在，按策略覆盖更新"
				successCount++
				results = append(results, result)
				continue
			}
		}

		created, createErr := s.createNew(payload)
		if createErr != nil {
			result.Reason = createErr.Error()
			failedCount++
			results = append(results, result)
			continue
		}
		result.Status = "success"
		result.ID = created.ID
		result.Instance = created.InstanceID
		existsByKey[key] = created.ID
		successCount++
		results = append(results, result)
	}

	return map[string]interface{}{
		"batch_id": s.BatchID,
		"total":    len(rows),
		"success":  successCount,
		"skipped":  skippedCount,
		"failed":   failedCount,
		"rows":     results,
	}, nil
}

func (s *AdminBatchImportInstancesService) prepareRows() ([]map[string]interface{}, error) {
	if s.Rows != nil && len(s.Rows) > 0 {
		return s.Rows, nil
	}
	format := strings.ToLower(strings.TrimSpace(s.Format))
	fileName := strings.ToLower(strings.TrimSpace(s.FileName))
	content := s.Content
	if format == "auto" {
		if strings.HasPrefix(strings.TrimSpace(content), "<") && strings.HasSuffix(fileName, ".xml") {
			return nil, fmt.Errorf("当前批量导入暂不支持 Navicat XML 格式，请使用 CSV 或 Markdown")
		}
		if strings.HasSuffix(fileName, ".csv") {
			format = "csv"
		} else if strings.HasSuffix(fileName, ".md") || strings.HasSuffix(fileName, ".markdown") {
			format = "markdown"
		} else if strings.ContainsAny(strings.TrimSpace(content), "|") && strings.Contains(strings.TrimSpace(content), "---") {
			format = "markdown"
		} else {
			format = "csv"
		}
	}
	switch format {
	case "csv":
		return parseCSV(content)
	case "markdown":
		return parseMarkdown(content)
	default:
		return nil, fmt.Errorf("不支持的格式: %s", format)
	}
}

func (s *AdminBatchImportInstancesService) normalizeRow(raw map[string]interface{}) (map[string]interface{}, error) {
	out := map[string]interface{}{}
	for k, v := range raw {
		out[strings.TrimSpace(k)] = v
	}

	remark := firstNonEmptyString(out, "数据源名称", "remark", "name", "名称")
	envName := firstNonEmptyString(out, "环境", "environment", "环境名称", "env")
	dbTypeRaw := strings.TrimSpace(firstNonEmptyString(out, "数据库类型", "db_type", "dbType", "类型"))
	connAddr := firstNonEmptyString(out, "连接地址", "connection_address", "address", "地址", "hostport")
	user := firstNonEmptyString(out, "登录账号", "user", "username", "账号", "用户名")
	password := firstNonEmptyString(out, "密码", "password", "passwd", "pwd")
	database := firstNonEmptyString(out, "默认数据库", "database", "db")
	sslMode := firstNonEmptyString(out, "SSL模式", "ssl_mode", "sslmode", "ssl")
	_ = sslMode

	remark = strings.TrimSpace(remark)
	envName = strings.TrimSpace(envName)
	user = strings.TrimSpace(user)
	password = strings.TrimSpace(password)
	database = strings.TrimSpace(database)

	dbType := normalizeDbType(dbTypeRaw)
	if dbType == "" {
		return nil, fmt.Errorf("数据库类型字段缺失或无法识别（%q）", dbTypeRaw)
	}
	hostname := ""
	port := 0
	if connAddr != "" {
		rawTrim := strings.TrimSpace(connAddr)
		idx := strings.LastIndex(rawTrim, ":")
		if idx <= 0 || idx == len(rawTrim)-1 {
			return nil, fmt.Errorf("连接地址格式错误，应为 host:port（%q）", connAddr)
		}
		hostname = strings.TrimSpace(rawTrim[:idx])
		portStr := strings.TrimSpace(rawTrim[idx+1:])
		num, err := strconv.Atoi(portStr)
		if err != nil || num < 1 || num > 65535 {
			return nil, fmt.Errorf("连接地址端口非法（%q）", connAddr)
		}
		port = num
	} else {
		hostname = strings.TrimSpace(firstNonEmptyString(out, "主机", "hostname", "host", "主机名", "ip"))
		portStr := firstNonEmptyString(out, "端口", "port")
		if hostname == "" || portStr == "" {
			return nil, fmt.Errorf("缺少连接地址字段")
		}
		num, err := strconv.Atoi(strings.TrimSpace(portStr))
		if err != nil || num < 1 || num > 65535 {
			return nil, fmt.Errorf("端口号非法")
		}
		port = num
	}

	if err := validateInstanceHostname(hostname); err != nil {
		return nil, err
	}

	if remark == "" {
		return nil, fmt.Errorf("数据源名称不能为空")
	}
	if len(remark) < 3 || len(remark) > 256 {
		return nil, fmt.Errorf("数据源名称长度需在 3 到 256 个字符之间")
	}
	if user == "" || len(user) > 128 {
		return nil, fmt.Errorf("登录账号长度需在 1 到 128 个字符之间")
	}
	if strings.ContainsAny(user, "\r\n\t") {
		return nil, fmt.Errorf("登录账号不能包含换行或制表符")
	}
	if password == "" || len(password) > 256 {
		return nil, fmt.Errorf("密码长度需在 1 到 256 个字符之间")
	}
	if strings.ContainsAny(password, "\r\n\t") {
		return nil, fmt.Errorf("密码不能包含换行或制表符")
	}
	if envName == "" {
		envIDRaw := rawValue(out, "environment_id")
		if envIDRaw == nil {
			switch v := rawValue(out, "environment").(type) {
			case float64, int, int64:
				out["environment"] = v
				envIDRaw = v
			}
		}
		if envIDRaw == nil {
			return nil, fmt.Errorf("环境字段不能为空（需填写环境名称或环境ID）")
		}
	}

	out["remark"] = remark
	out["environment_name"] = envName
	out["db_type"] = dbType
	out["hostname"] = hostname
	out["port"] = port
	out["user"] = user
	out["password"] = password
	extra := map[string]interface{}{}
	if dbType == "PostgreSQL" {
		if database != "" {
			if len(database) > 128 {
				return nil, fmt.Errorf("默认数据库不能超过 128 个字符")
			}
			extra["database"] = database
		} else {
			extra["database"] = "postgres"
		}
		sslVal := strings.TrimSpace(sslMode)
		if sslVal == "" {
			sslVal = "disable"
		}
		extra["sslmode"] = sslVal
	} else if dbType == "MySQL" {
		if database != "" {
			if len(database) > 128 {
				return nil, fmt.Errorf("默认数据库不能超过 128 个字符")
			}
			extra["database"] = database
		}
	}
	out["extra_config"] = extra

	return out, nil
}

func (s *AdminBatchImportInstancesService) createNew(payload map[string]interface{}) (*models.InsightInstances, error) {
	envID, _ := rawValue(payload, "environment").(int)
	extra, _ := rawValue(payload, "extra_config").(map[string]interface{})
	form := &forms.AdminCreateInstancesForm{
		Hostname:    stringValue(payload, "hostname"),
		Port:        asInt(rawValue(payload, "port")),
		User:        stringValue(payload, "user"),
		Password:    stringValue(payload, "password"),
		DbType:      models.EnumType(stringValue(payload, "db_type")),
		ExtraConfig: extra,
		Environment: envID,
		Remark:      stringValue(payload, "remark"),
	}
	binderRaw := map[string]interface{}{
		"hostname":     form.Hostname,
		"port":         form.Port,
		"user":         form.User,
		"password":     form.Password,
		"db_type":      string(form.DbType),
		"extra_config": form.ExtraConfig,
		"environment":  form.Environment,
		"remark":       form.Remark,
	}
	if err := bindingMap(form, binderRaw); err != nil {
		return nil, fmt.Errorf("字段校验失败: %v", err)
	}
	svc := AdminCreateInstancesService{
		AdminCreateInstancesForm: form,
		C:                        s.C,
	}
	created, err := svc.Run()
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (s *AdminBatchImportInstancesService) updateExisting(id uint64, payload map[string]interface{}) (*models.InsightInstances, error) {
	envID, _ := rawValue(payload, "environment").(int)
	extra, _ := rawValue(payload, "extra_config").(map[string]interface{})
	password := stringValue(payload, "password")
	form := &forms.AdminUpdateInstancesForm{
		Hostname:    stringValue(payload, "hostname"),
		Port:        asInt(rawValue(payload, "port")),
		User:        stringValue(payload, "user"),
		Password:    password,
		DbType:      models.EnumType(stringValue(payload, "db_type")),
		ExtraConfig: extra,
		Environment: envID,
		Remark:      stringValue(payload, "remark"),
	}
	binderRaw := map[string]interface{}{
		"hostname":     form.Hostname,
		"port":         form.Port,
		"user":         form.User,
		"password":     form.Password,
		"db_type":      string(form.DbType),
		"extra_config": form.ExtraConfig,
		"environment":  form.Environment,
		"remark":       form.Remark,
	}
	if err := bindingMap(form, binderRaw); err != nil {
		return nil, fmt.Errorf("字段校验失败: %v", err)
	}
	svc := AdminUpdateInstancesService{
		AdminUpdateInstancesForm: form,
		C:                        s.C,
		ID:                       id,
	}
	if err := svc.Run(); err != nil {
		return nil, err
	}
	var row models.InsightInstances
	if err := global.App.DB.Where("id=?", id).Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func bindingMap(form interface{}, raw map[string]interface{}) error {
	_ = form
	return validateRawByBindingTag(form, raw)
}

func validateRawByBindingTag(form interface{}, raw map[string]interface{}) error {
	if raw["hostname"] == nil || strings.TrimSpace(fmt.Sprintf("%v", raw["hostname"])) == "" {
		return fmt.Errorf("hostname 不能为空")
	}
	portRaw := raw["port"]
	if portRaw == nil {
		return fmt.Errorf("port 不能为空")
	}
	port := asInt(portRaw)
	if port < 1 || port > 65535 {
		return fmt.Errorf("port 非法")
	}
	userRaw := raw["user"]
	if userRaw == nil || strings.TrimSpace(fmt.Sprintf("%v", userRaw)) == "" {
		return fmt.Errorf("user 不能为空")
	}
	passwordEmpty := raw["password"] == nil || strings.TrimSpace(fmt.Sprintf("%v", raw["password"])) == ""
	isUpdate := false
	switch form.(type) {
	case *forms.AdminUpdateInstancesForm:
		isUpdate = true
	}
	if passwordEmpty && !isUpdate {
		return fmt.Errorf("password 不能为空")
	}
	if raw["db_type"] == nil || strings.TrimSpace(fmt.Sprintf("%v", raw["db_type"])) == "" {
		return fmt.Errorf("db_type 不能为空")
	}
	if raw["environment"] == nil {
		return fmt.Errorf("environment 不能为空")
	}
	remark := strings.TrimSpace(fmt.Sprintf("%v", raw["remark"]))
	if len(remark) < 2 || len(remark) > 256 {
		return fmt.Errorf("remark 长度需在 2 到 256 个字符之间")
	}
	return nil
}

func parseCSV(content string) ([]map[string]interface{}, error) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n"))
	if trimmed == "" {
		return nil, fmt.Errorf("CSV 内容为空")
	}
	lines := strings.Split(trimmed, "\n")
	var dataLines []string
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		dataLines = append(dataLines, line)
	}
	if len(dataLines) < 2 {
		return nil, fmt.Errorf("CSV 至少需要表头+1行数据")
	}
	headers := splitCSVLine(dataLines[0])
	for i := range headers {
		headers[i] = strings.TrimSpace(headers[i])
	}
	rows := make([]map[string]interface{}, 0, len(dataLines)-1)
	for i := 1; i < len(dataLines); i++ {
		cells := splitCSVLine(dataLines[i])
		row := map[string]interface{}{}
		for j, h := range headers {
			if h == "" {
				continue
			}
			if j < len(cells) {
				row[h] = strings.TrimSpace(cells[j])
			} else {
				row[h] = ""
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func splitCSVLine(line string) []string {
	var res []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch == '"' {
			if inQuote && i+1 < len(line) && line[i+1] == '"' {
				cur.WriteByte('"')
				i++
				continue
			}
			inQuote = !inQuote
			continue
		}
		if ch == ',' && !inQuote {
			res = append(res, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	res = append(res, cur.String())
	return res
}

var requiredMarkdownHeaders = []string{"数据源名称", "环境", "数据库类型", "连接地址", "登录账号", "密码"}
var requiredMarkdownAliases = map[string][]string{
	"remark":      {"数据源名称", "remark", "name", "名称"},
	"environment": {"环境", "environment", "env", "环境名称"},
	"db_type":     {"数据库类型", "db_type", "dbtype", "类型"},
	"conn":        {"连接地址", "connection_address", "address", "地址", "hostport"},
	"user":        {"登录账号", "user", "username", "账号", "用户名"},
	"password":    {"密码", "password", "passwd", "pwd"},
}

func isMarkdownSeparatorCells(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	dash := 0
	for _, c := range cells {
		s := strings.TrimSpace(c)
		if s == "" {
			continue
		}
		normalized := strings.ReplaceAll(s, " ", "")
		if matched, _ := regexp.MatchString(`^:?-{3,}:?$`, normalized); matched {
			dash++
		}
	}
	return dash*2 >= len(cells)
}

func pickMarkdownDataTable(blocks [][]string) []string {
	if len(blocks) == 0 {
		return nil
	}
	for _, block := range blocks {
		if len(block) < 3 {
			continue
		}
		headers := splitMarkdownCells(block[0])
		sepCells := splitMarkdownCells(block[1])
		if !isMarkdownSeparatorCells(sepCells) {
			continue
		}
		headerSet := map[string]bool{}
		for _, h := range headers {
			headerSet[strings.ToLower(strings.TrimSpace(h))] = true
		}
		direct := 0
		for _, h := range requiredMarkdownHeaders {
			if headerSet[strings.ToLower(h)] {
				direct++
			}
		}
		if direct >= 4 {
			return block
		}
		aliasHits := 0
		for _, aliases := range requiredMarkdownAliases {
			found := false
			for _, a := range aliases {
				if headerSet[strings.ToLower(a)] {
					found = true
					break
				}
			}
			if found {
				aliasHits++
			}
		}
		if aliasHits >= 5 {
			return block
		}
	}
	return blocks[len(blocks)-1]
}

func parseMarkdown(content string) ([]map[string]interface{}, error) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n"))
	if trimmed == "" {
		return nil, fmt.Errorf("Markdown 内容为空")
	}
	rawLines := strings.Split(trimmed, "\n")
	var blocks [][]string
	var buf []string
	for _, line := range rawLines {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "|") {
			if buf == nil {
				buf = []string{}
			}
			buf = append(buf, strings.Trim(l, "|"))
		} else {
			if len(buf) >= 3 {
				blocks = append(blocks, buf)
			}
			buf = nil
		}
	}
	if len(buf) >= 3 {
		blocks = append(blocks, buf)
	}
	block := pickMarkdownDataTable(blocks)
	if block == nil || len(block) < 3 {
		return nil, fmt.Errorf("Markdown 表格解析失败：未找到含（数据源名称/环境/数据库类型/连接地址/登录账号/密码）列的数据表格，请参考 MD 模板的第二节【导入数据】表格格式。")
	}
	headers := splitMarkdownCells(block[0])
	separator := splitMarkdownCells(block[1])
	if !isMarkdownSeparatorCells(separator) {
		return nil, fmt.Errorf("Markdown 表头分隔线格式错误，下一行应为 | --- | --- |（可含冒号左右对齐）")
	}
	if len(headers) != len(separator) && len(headers) > len(separator) {
		return nil, fmt.Errorf("Markdown 表头与分隔线列数不一致")
	}
	for i := range headers {
		headers[i] = strings.TrimSpace(headers[i])
	}
	rows := make([]map[string]interface{}, 0, len(block)-2)
	for i := 2; i < len(block); i++ {
		cells := splitMarkdownCells(block[i])
		if isEmptySliceMap(cells) {
			continue
		}
		row := map[string]interface{}{}
		for j, h := range headers {
			if h == "" {
				continue
			}
			if j < len(cells) {
				row[h] = strings.TrimSpace(cells[j])
			} else {
				row[h] = ""
			}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("Markdown 表格无有效数据行")
	}
	return rows, nil
}

func isEmptySliceMap(cells []string) bool {
	for _, c := range cells {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

func splitMarkdownCells(line string) []string {
	parts := strings.Split(line, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func isEmptyRow(row map[string]interface{}) bool {
	for _, v := range row {
		if strings.TrimSpace(fmt.Sprintf("%v", v)) != "" {
			return false
		}
	}
	return true
}

func normalizeDbType(raw string) string {
	r := strings.ToLower(strings.TrimSpace(raw))
	switch r {
	case "mysql", "mysqld", "mariadb", "tidb":
		return "MySQL"
	case "postgresql", "postgres", "pg":
		return "PostgreSQL"
	}
	return ""
}

func rawValue(m map[string]interface{}, key string) interface{} {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	return v
}

func stringValue(m map[string]interface{}, key string) string {
	v := rawValue(m, key)
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func asInt(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err == nil {
			return n
		}
	}
	return 0
}

func firstNonEmptyString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		s := stringValue(m, k)
		if s != "" {
			return s
		}
	}
	return ""
}
