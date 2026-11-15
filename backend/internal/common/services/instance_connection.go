package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"

	commonModels "github.com/simajiong/jaysql/internal/common/models"

	"github.com/simajiong/jaysql/internal/common/dbfamily"
	"github.com/simajiong/jaysql/internal/common/sqlserverdsn"
	_ "github.com/microsoft/go-mssqldb"
	go_ora "github.com/sijms/go-ora/v2"
)

type AdminTestInstanceConnectionService struct {
	C  context.Context
	ID uint64
}

func (s *AdminTestInstanceConnectionService) Run() (*ConnectionTestReport, error) {
	// 手动「测试」走完整体检（连通性 + SSH + 采集权限），与 ProbeInstanceConnection 共用同一实现，
	// 保证手动测试与后台探活不会出现两套判定口径。
	return ProbeInstanceConnection(s.C, s.ID, ProbeModeFull)
}

// ProbeMode 控制实例连接测试的深度。
//
//   - ProbeModeFull（默认、零值之外的显式选择）：连通性 + 主机 SSH 探测 + 采集权限体检。
//     纳管/编辑实例与手动点「测试」时使用，结论要能顺带告诉用户备份通道与权限缺口。
//   - ProbeModeLightweight：只做数据库连通性（建连 + 一次 select 1）。
//     后台定时探活使用 —— 它每分钟都要对一批实例跑一遍，如果再带上 SSH dial
//     与多条只读 SQL 的权限体检，纯属浪费，还会把"探活"这件事的耗时放大一个量级。
type ProbeMode string

const (
	ProbeModeFull        ProbeMode = "full"
	ProbeModeLightweight ProbeMode = "lightweight"
)

// ProbeInstanceConnection 实例连接探测的**唯一实现**：加载实例 → 解密密码 → 连接测试 → 回写状态。
//
// 手动「测试」（AdminTestInstanceConnectionService）与后台定时探活都走这里，
// 差别只有 mode：前者 full，后者 lightweight。任何新的调用方都不应再自行拼装这套流程。
//
// 无论成功失败都会回写 last_checked_at / last_check_status / last_check_message，
// 因为首页「实例连接异常」风险口径与列表状态列都直接读这三个字段。
func ProbeInstanceConnection(ctx context.Context, id uint64, mode ProbeMode) (*ConnectionTestReport, error) {
	var instance commonModels.InsightInstances
	err := global.App.DB.Table("insight_instances").Where("id=?", id).Take(&instance).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("实例不存在")
		}
		return nil, err
	}

	decrypted, err := secret.Decrypt(instance.Password)
	if err != nil {
		// 解密失败也要回写：否则这个实例会永远停在旧状态上，且用户不知道原因。
		updateInstanceConnectionStatus(id, "error", "实例密码解密失败")
		return nil, fmt.Errorf("实例密码解密失败")
	}

	report, err := testConnectionMode(
		ctx,
		mode,
		string(instance.DbType),
		instance.Hostname,
		instance.Port,
		instance.User,
		decrypted,
		instance.ExtraConfig,
	)
	if err != nil {
		updateInstanceConnectionStatus(id, "error", err.Error())
		return nil, err
	}
	updateInstanceConnectionStatus(id, "success", connectionSuccessMessageForMode(report, mode))
	return report, nil
}

type AdminTestInstanceConnectionByPayloadService struct {
	C           context.Context
	DbType      string
	Hostname    string
	Port        int
	User        string
	Password    string
	ExtraConfig map[string]any
}

func (s *AdminTestInstanceConnectionByPayloadService) Run() (*ConnectionTestReport, error) {
	extraConfig, err := marshalExtraConfig(s.ExtraConfig)
	if err != nil {
		return nil, err
	}
	return testConnection(
		s.C,
		s.DbType,
		s.Hostname,
		s.Port,
		s.User,
		s.Password,
		extraConfig,
	)
}

// privilegeCheckTimeout 体检阶段的独立超时。
//
// 与建立连接的 4 秒分开：连接失败（端口不通/认证被拒）是快速失败，
// 4 秒足够；而体检要跑多条只读 SQL，用 4 秒会把正常实例误判成失败。
// 两者分开后，"不可达"依然秒回，体检慢一点也只发生在已连上的实例上。
const privilegeCheckTimeout = 8 * time.Second

// testConnection 测试数据库连接是否可用，并回报 SSH 与采集权限体检结论。
//
// 判定口径（2026-09 修订）：
//   - 数据库连通性是硬性门槛，失败即失败；
//   - 主机 SSH 是**软性**提示：监控采集全程不使用 SSH（服务端直连 + Prometheus 拉主机），
//     只影响备份/恢复。此前把 SSH 失败等同于连接失败，逼得"只想接入监控"的用户
//     必须先开通主机 SSH，这次改为提示；
//   - 采集权限体检同样不阻断，但会明确列出缺口与修复语句。
func testConnection(ctx context.Context, dbType string, hostname string, port int, user string, password string, extraConfig []byte) (*ConnectionTestReport, error) {
	return testConnectionMode(ctx, ProbeModeFull, dbType, hostname, port, user, password, extraConfig)
}

func testConnectionMode(ctx context.Context, mode ProbeMode, dbType string, hostname string, port int, user string, password string, extraConfig []byte) (*ConnectionTestReport, error) {
	childCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	cfg, err := parseExtraConfig(extraConfig)
	if err != nil {
		return nil, err
	}
	report := newConnectionTestReport()
	// 外围体检（SSH 探测 + 采集权限）只在 full 模式跑，见 ProbeMode 的说明。
	probePeripherals := mode != ProbeModeLightweight

	switch strings.ToLower(dbType) {
	case "mysql", "tidb", "doris":
		db := dao.DB{
			User:     user,
			Password: password,
			Host:     hostname,
			Port:     port,
			Database: "",
			Params: map[string]string{
				"charset": "utf8mb4",
			},
			Ctx: childCtx,
		}
		_, _, err := db.Query("select 1")
		if err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1045 {
				return nil, fmt.Errorf("连接失败：账号或密码错误")
			}
			return nil, fmt.Errorf("连接失败：%s", err.Error())
		}
		if probePeripherals {
			report.SSHStatus, report.SSHMessage = probeOptionalSSHForBackup(hostname, cfg)
		}
		if probePeripherals {
			checkCtx, cancelCheck := context.WithTimeout(ctx, privilegeCheckTimeout)
			defer cancelCheck()
			probe := db
			probe.Ctx = checkCtx
			for _, item := range checkMySQLProtocolPrivileges(probe, user) {
				report.add(item)
			}
		}
		return report, nil
	case "clickhouse":
		ck := dao.ClickhouseDB{
			User:     user,
			Password: password,
			Host:     hostname,
			Port:     port,
			Database: "default",
			Settings: map[string]interface{}{},
			Ctx:      childCtx,
		}
		if _, _, err := ck.Query("select 1"); err != nil {
			return nil, fmt.Errorf("连接失败：%s", err.Error())
		}
		if probePeripherals {
			report.SSHStatus, report.SSHMessage = probeOptionalSSHForBackup(hostname, cfg)
		}
		return report, nil
	case "postgresql", "kingbase", "kingbasees":
		// 金仓 KingbaseES 兼容 PG 协议：DSN、pgx 驱动、select 1 探测、权限体检全部同一套。
		// 默认库不同（金仓初始化自带 test），因此不能简单复用 getStringConfig 的 postgres 兜底。
		defaultDB := "postgres"
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(dbType)), "kingbase") {
			defaultDB = "test"
		}
		database := getStringConfig(cfg, "database", defaultDB)
		sslMode := getStringConfig(cfg, "sslmode", "disable")
		connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
			url.QueryEscape(user),
			url.QueryEscape(password),
			hostname,
			port,
			url.PathEscape(database),
			url.QueryEscape(sslMode),
		)
		conn, err := pgx.Connect(childCtx, connStr)
		if err != nil {
			return nil, fmt.Errorf("连接失败：%s", err.Error())
		}
		// childCtx 到期后 conn.Close 会失败并泄漏连接，这里用不受本次超时约束的上下文收尾。
		defer conn.Close(context.Background())
		var ping int
		if err := conn.QueryRow(childCtx, "select 1").Scan(&ping); err != nil {
			return nil, fmt.Errorf("连接失败：%s", err.Error())
		}
		if probePeripherals {
			report.SSHStatus, report.SSHMessage = probeOptionalSSHForBackup(hostname, cfg)
		}
		if probePeripherals {
			checkCtx, cancelCheck := context.WithTimeout(ctx, privilegeCheckTimeout)
			defer cancelCheck()
			for _, item := range checkPostgresPrivileges(checkCtx, conn, user) {
				report.add(item)
			}
		}
		return report, nil
	case "sqlserver":
		dsn := sqlserverdsn.DSN(user, password, hostname, port, getStringConfig(cfg, "database", "master"))
		db, err := sql.Open("sqlserver", dsn)
		if err != nil {
			return nil, fmt.Errorf("连接失败：%s", err.Error())
		}
		defer db.Close()
		if err := db.PingContext(childCtx); err != nil {
			return nil, fmt.Errorf("连接失败：%s", err.Error())
		}
		if probePeripherals {
			report.SSHStatus, report.SSHMessage = probeOptionalSSHForBackup(hostname, cfg)
		}
		return report, nil
	case "mongodb":
		authSource := getStringConfig(cfg, "auth_source", "admin")
		mongoURI := fmt.Sprintf("mongodb://%s:%s@%s:%d/?authSource=%s",
			url.QueryEscape(user),
			url.QueryEscape(password),
			hostname,
			port,
			url.QueryEscape(authSource),
		)
		client, err := mongo.Connect(childCtx, options.Client().ApplyURI(mongoURI))
		if err != nil {
			return nil, fmt.Errorf("连接失败：%s", err.Error())
		}
		defer func() {
			_ = client.Disconnect(context.Background())
		}()
		if err := client.Ping(childCtx, nil); err != nil {
			return nil, fmt.Errorf("连接失败：%s", err.Error())
		}
		if probePeripherals {
			report.SSHStatus, report.SSHMessage = probeOptionalSSHForBackup(hostname, cfg)
		}
		return report, nil
	case "oracle":
		serviceName := getStringConfig(cfg, "service_name", "")
		if serviceName == "" {
			return nil, fmt.Errorf("连接失败：Oracle 服务名不能为空")
		}
		dsn := go_ora.BuildUrl(hostname, port, serviceName, user, password, nil)
		db, err := sql.Open("oracle", dsn)
		if err != nil {
			return nil, fmt.Errorf("连接失败：%s", err.Error())
		}
		defer db.Close()
		if err := db.PingContext(childCtx); err != nil {
			return nil, fmt.Errorf("连接失败：%s", err.Error())
		}
		if probePeripherals {
			report.SSHStatus, report.SSHMessage = probeOptionalSSHForBackup(hostname, cfg)
		}
		return report, nil
	case "oceanbase":
		/* OceanBase 一个 db_type 对应两种租户模式，走哪条通道由 extra_config.oceanbase_mode 决定：
		   - Oracle 模式：与 Oracle 一致，service_name（= 租户名）必填，用 Oracle 驱动；
		   - MySQL 模式：与 MySQL 一致，复用 MySQL 协议 + 协议权限体检。
		 缺省按 MySQL 模式处理，与 dbfamily.OceanBaseMode 的兜底口径保持一致。 */
		if dbfamily.OceanBaseOracleMode(extraConfig) {
			serviceName := getStringConfig(cfg, "service_name", "")
			if serviceName == "" {
				return nil, fmt.Errorf("连接失败：OceanBase（Oracle 模式）服务名不能为空")
			}
			dsn := go_ora.BuildUrl(hostname, port, serviceName, user, password, nil)
			db, err := sql.Open("oracle", dsn)
			if err != nil {
				return nil, fmt.Errorf("连接失败：%s", err.Error())
			}
			defer db.Close()
			if err := db.PingContext(childCtx); err != nil {
				return nil, fmt.Errorf("连接失败：%s", err.Error())
			}
			if probePeripherals {
				report.SSHStatus, report.SSHMessage = probeOptionalSSHForBackup(hostname, cfg)
			}
			return report, nil
		}
		db := dao.DB{
			User:     user,
			Password: password,
			Host:     hostname,
			Port:     port,
			Database: "",
			Params: map[string]string{
				"charset": "utf8mb4",
			},
			Ctx: childCtx,
		}
		_, _, err := db.Query("select 1")
		if err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1045 {
				return nil, fmt.Errorf("连接失败：账号或密码错误")
			}
			return nil, fmt.Errorf("连接失败：%s", err.Error())
		}
		if probePeripherals {
			report.SSHStatus, report.SSHMessage = probeOptionalSSHForBackup(hostname, cfg)
		}
		if probePeripherals {
			checkCtx, cancelCheck := context.WithTimeout(ctx, privilegeCheckTimeout)
			defer cancelCheck()
			probe := db
			probe.Ctx = checkCtx
			for _, item := range checkMySQLProtocolPrivileges(probe, user) {
				report.add(item)
			}
		}
		return report, nil
	default:
		return nil, fmt.Errorf("暂不支持该数据库类型的连接测试")
	}
}

// connectionSuccessMessage 组装写回 last_check_message 的结论摘要。
//
// 摘要必须自解释：前端 InstanceList 直接把这段文本放进 tooltip 和详情弹窗，
// 用户看到的"连接成功"要能顺带看到 SSH 状态与权限缺口，否则等于没有体检。
func connectionSuccessMessage(report *ConnectionTestReport) string {
	if report == nil {
		return "数据库连接正常"
	}
	parts := []string{"数据库连接正常"}
	switch report.SSHStatus {
	case sshStatusOK:
		parts = append(parts, "主机 SSH 可用")
	case sshStatusFailed:
		parts = append(parts, "主机 SSH 不可用（不影响监控采集，备份/恢复需要）："+report.SSHMessage)
	default:
		parts = append(parts, "未配置主机 SSH（不影响监控采集，备份/恢复需要）")
	}
	failed := make([]string, 0, len(report.Privileges))
	for _, item := range report.Privileges {
		if item.Status == PrivilegeStatusFail {
			failed = append(failed, item.Name)
		}
	}
	if len(failed) > 0 {
		parts = append(parts, fmt.Sprintf("采集权限缺口 %d 项：%s", len(failed), strings.Join(failed, "、")))
	}
	return strings.Join(parts, "；")
}

// connectionSuccessMessageForMode 按探测深度挑选写回 last_check_message 的摘要。
//
// 轻量探活必须**另起一句**，不能复用 connectionSuccessMessage：后者会拼上 SSH 与权限结论，
// 而 lightweight 根本没跑这两项体检，SSHStatus 为空会走到 default 分支，
// 从而在库里写下"未配置主机 SSH"——那是彻头彻尾的假结论，会覆盖掉用户上次完整体检的真实结果。
func connectionSuccessMessageForMode(report *ConnectionTestReport, mode ProbeMode) string {
	if mode == ProbeModeLightweight {
		return "数据库连接正常（后台探活）"
	}
	return connectionSuccessMessage(report)
}

// parsePositivePort 解析 1-65535 的端口文本。
func parsePositivePort(text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, fmt.Errorf("端口不能为空")
	}
	var port int
	if _, err := fmt.Sscanf(text, "%d", &port); err != nil {
		return 0, fmt.Errorf("端口必须为整数")
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("端口范围必须在 1-65535")
	}
	return port, nil
}

func dialInstanceSSH(host string, port int, user, password, privateKey string) error {
	authMethods := make([]ssh.AuthMethod, 0, 3)
	hasKey := false
	hasPwd := false
	if strings.TrimSpace(privateKey) != "" {
		signer, err := ssh.ParsePrivateKey([]byte(privateKey))
		if err != nil {
			return fmt.Errorf("SSH 私钥解析失败: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
		hasKey = true
	}
	if strings.TrimSpace(password) != "" {
		authMethods = append(authMethods, ssh.Password(password))
		authMethods = append(authMethods, ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
			answers := make([]string, 0, len(questions))
			for range questions {
				answers = append(answers, password)
			}
			return answers, nil
		}))
		hasPwd = true
	}
	if len(authMethods) == 0 {
		return fmt.Errorf("SSH 认证信息为空")
	}
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", strings.TrimSpace(host), port), &ssh.ClientConfig{
		User:            strings.TrimSpace(user),
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("SSH 登录 %s@%s:%d 失败: %w（已尝试认证：私钥=%v, 密码/交互=%v）", strings.TrimSpace(user), strings.TrimSpace(host), port, err, hasKey, hasPwd)
	}
	_ = client.Close()
	return nil
}

func parseExtraConfig(extraConfig []byte) (map[string]any, error) {
	if len(extraConfig) == 0 {
		return map[string]any{}, nil
	}
	var cfg map[string]any
	if err := json.Unmarshal(extraConfig, &cfg); err != nil {
		return nil, fmt.Errorf("扩展连接参数格式错误")
	}
	return cfg, nil
}

func getStringConfig(cfg map[string]any, key string, fallback string) string {
	if cfg == nil {
		return fallback
	}
	value, ok := cfg[key]
	if !ok || value == nil {
		return fallback
	}
	text := strings.TrimSpace(fmt.Sprintf("%v", value))
	if text == "" {
		return fallback
	}
	return text
}

func marshalExtraConfig(raw map[string]any) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("扩展连接参数格式错误")
	}
	return buf, nil
}
