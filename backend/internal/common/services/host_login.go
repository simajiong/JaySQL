package services

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	jwt "github.com/appleboy/gin-jwt/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/simajiong/jaysql/internal/common/forms"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"
	"golang.org/x/crypto/ssh"
)

// ---------------------------------------------------------------------------
// 主机登录（数据源总览 → 操作列「主机登录」）
//
// 会话是标准 SSH PTY，与发行版无关；真正需要平台侧处理的是三件事：
//   1. 认证方式：Ubuntu / UOS / 银河麒麟默认禁止 root 密码登录，等保镜像还会禁用密码认证，
//      此时必须由错误码引导用户改用私钥，而不是笼统报「认证失败」；
//   2. 主机密钥：做 TOFU（指纹由浏览器侧持久化，避免为存指纹单独建表），
//      指纹变化时阻断建连，防中间人；
//   3. 国密：国产 OS 等保镜像可能启用国密 OpenSSH（SM2/SM4），
//      golang.org/x/crypto/ssh 不支持，只能明确报 ALGORITHM_MISMATCH 并引导走 agent 通道；
//   4. Windows（SQL Server 的高频部署形态，Oracle on Windows 同样存在）：
//      OpenSSH for Windows 的 pty-req 依赖 conpty，老版本或受限镜像上会直接拒绝，
//      此时降级为「无 PTY 的 shell」仍可拿到可用的 cmd / PowerShell 命令行，
//      代价是窗口尺寸不同步、vi / top 这类全屏程序不可用；
//      另外中文 Windows 的 cmd 默认是 GBK（936）代码页，UTF-8 终端里中文会乱码，需要提示而不是猜。
// ---------------------------------------------------------------------------

// 支持范围与实例表单「主机 SSH」区块（InstanceFormModal.showsSSHSection）保持同一口径：
// 表单不提供该区块的引擎，总览里就不给出入口，否则用户点进去只会看到「不支持」。
//
// 落库侧还有一处必须同步：normalizeInstanceExtraConfig 只有调用了
// mergeSSHFieldsIntoExtraConfig 的 db_type 才会把 ssh_* 写进 extra_config ——
// 白名单放行而落库没放行的话，用户在表单里填完凭据保存后会「什么都没存下来」。
var hostLoginSupportedDBTypes = map[string]bool{
	"mysql":      true,
	"postgresql": true,
	"kingbase":   true,
	"tidb":       true,
	"doris":      true,
	"oracle":     true,
	"sqlserver":  true,
}

// 列表接口用的阻断码（前端按 note 直接展示，不再 switch）
const (
	HostLoginBlockNone                 = ""
	HostLoginBlockCloudManaged         = "CLOUD_MANAGED"
	HostLoginBlockEngineUnsupported    = "ENGINE_UNSUPPORTED"
	HostLoginBlockNoCredential         = "NO_CREDENTIAL"
	HostLoginBlockCredentialIncomplete = "CREDENTIAL_INCOMPLETE"

	// 连接类失败码
	HostLoginErrAuthFailed           = "AUTH_FAILED"
	HostLoginErrKeyInvalid           = "KEY_INVALID"
	HostLoginErrAuthMethodUnavail    = "AUTH_METHOD_UNAVAILABLE"
	HostLoginErrAlgorithmMismatch    = "ALGORITHM_MISMATCH"
	HostLoginErrConnectTimeout       = "CONNECT_TIMEOUT"
	HostLoginErrConnRefused          = "CONN_REFUSED"
	HostLoginErrNetworkUnreachable   = "NETWORK_UNREACHABLE"
	HostLoginErrHostKeyChanged       = "HOST_KEY_CHANGED"
	HostLoginErrSessionLimit         = "SESSION_LIMIT"
	HostLoginErrNoPermission         = "NO_PERMISSION"
	HostLoginErrNoCredential         = "NO_CREDENTIAL"
	HostLoginErrCredentialIncomplete = "CREDENTIAL_INCOMPLETE"
)

const (
	// 默认 SSH 端口，与实例表单 applyTypeDefaults 的默认值一致
	defaultHostLoginSSHPort = 22
	// 预检与建连超时：预检不交互，10 秒足够；超时归类 CONNECT_TIMEOUT
	hostLoginDialTimeout = 10 * time.Second
	// 一次性令牌有效期：拿到令牌后必须立刻建 WS，超时未完成即作废
	hostLoginTokenTTL = 60 * time.Second
	// 会话上限
	hostLoginMaxPerInstance = 3
	hostLoginMaxPerUser     = 5
	// 生命周期
	hostLoginIdleTimeout  = 30 * time.Minute
	hostLoginTotalTimeout = 2 * time.Hour
	// 终端类型：回退链 xterm-256color → xterm → vt100，精简镜像没有 256color 的 terminfo
	hostLoginTermPrimary   = "xterm-256color"
	hostLoginTermFallback1 = "xterm"
	hostLoginTermFallback2 = "vt100"
)

var hostLoginBlockNotes = map[string]string{
	HostLoginBlockCloudManaged:         "云数据库由云厂商托管，不提供主机 shell，请通过云控制台或数据库连接排查问题。",
	HostLoginBlockEngineUnsupported:    "该引擎暂未开放主机登录（当前支持 MySQL / PostgreSQL / TiDB / Doris / Oracle / SQL Server）。",
	HostLoginBlockNoCredential:         "该实例尚未配置主机 SSH 凭据，无法登录主机。",
	HostLoginBlockCredentialIncomplete: "已配置 SSH 用户名，但密码与私钥均为空，二者需填写其一。",
}

// hostLoginServerLooksWindows 依据 SSH 服务端自报标识粗判 Windows。
// Windows OpenSSH 会自报形如 "SSH-2.0-OpenSSH_for_Windows_8.1"。
// 只用于给前端挑提示文案（代码页 / 换行 / 无 PTY 的交互退化），不参与鉴权与能力判定：
// 标识可被管理员改写，判错最多是提示不对，不会导致连不上。
func hostLoginServerLooksWindows(serverVersion string) bool {
	v := strings.ToLower(strings.TrimSpace(serverVersion))
	return strings.Contains(v, "windows") || strings.Contains(v, "win32")
}

// ---------------------------------------------------------------------------
// 能力判定（列表接口用，不解密、不连主机）
// ---------------------------------------------------------------------------

// ClassifyHostLoginBlock 只看元信息就能判定的部分。返回的 user 用于悬浮展示，
// 密码与私钥永远不出现在返回值里。
func ClassifyHostLoginBlock(dbType, deployProvider string, cfg map[string]any) (code, note, user string, ready bool) {
	normalized := strings.ToLower(strings.TrimSpace(dbType))
	if strings.EqualFold(strings.TrimSpace(deployProvider), "cloud") {
		return HostLoginBlockCloudManaged, hostLoginBlockNotes[HostLoginBlockCloudManaged], "", false
	}
	if !hostLoginSupportedDBTypes[normalized] {
		return HostLoginBlockEngineUnsupported, hostLoginBlockNotes[HostLoginBlockEngineUnsupported], "", false
	}
	sshUser := getStringConfig(cfg, "ssh_user", "")
	if strings.TrimSpace(sshUser) == "" {
		return HostLoginBlockNoCredential, hostLoginBlockNotes[HostLoginBlockNoCredential], "", false
	}
	// extra_config 里存的是密文，这里只看「有没有配」，不解密（列表几十行，没必要）
	if strings.TrimSpace(getStringConfig(cfg, "ssh_password", "")) == "" &&
		strings.TrimSpace(getStringConfig(cfg, "ssh_private_key", "")) == "" {
		return HostLoginBlockCredentialIncomplete, hostLoginBlockNotes[HostLoginBlockCredentialIncomplete], sshUser, false
	}
	return HostLoginBlockNone, "", sshUser, true
}

// ---------------------------------------------------------------------------
// 凭据解析（precheck / 建会话时用，需要明文）
// ---------------------------------------------------------------------------

type HostLoginTarget struct {
	InstanceID     string
	Name           string
	Hostname       string
	SSHPort        int
	SSHUser        string
	Password       string // 解密后的明文，仅存活在本次请求的内存里
	PrivateKey     string
	AuthMode       string // password | key
	DbType         string
	DeployProvider string
}

// ResolveHostLoginTarget 取实例 + 解析 extra_config 的 ssh_* 并解密。
// 返回的 blockCode 非空表示不该建连（能力或凭据层面），调用方直接按它分流转前端。
func ResolveHostLoginTarget(instanceID string) (*HostLoginTarget, string, error) {
	db := global.App.DB
	var row struct {
		ID             uint64
		InstanceID     string
		Name           string
		Hostname       string
		DbType         string
		DeployProvider string
		ExtraConfig    []byte
	}
	if err := db.Table("insight_instances").
		Select("id, instance_id, IFNULL(remark,'') AS name, hostname, db_type, "+
			"IFNULL(deploy_provider,'') AS deploy_provider, extra_config").
		Where("instance_id = ?", strings.TrimSpace(instanceID)).
		Scan(&row).Error; err != nil {
		return nil, "", fmt.Errorf("查询实例失败: %w", err)
	}
	if row.InstanceID == "" {
		return nil, HostLoginErrNoCredential, fmt.Errorf("实例不存在：%s", instanceID)
	}

	cfg, err := parseExtraConfig(row.ExtraConfig)
	if err != nil {
		return nil, "", err
	}
	blockCode, _, sshUser, _ := ClassifyHostLoginBlock(row.DbType, row.DeployProvider, cfg)
	if blockCode != HostLoginBlockNone {
		return nil, blockCode, nil
	}

	target := &HostLoginTarget{
		InstanceID:     row.InstanceID,
		Name:           strings.TrimSpace(row.Name),
		Hostname:       strings.TrimSpace(row.Hostname),
		SSHPort:        defaultHostLoginSSHPort,
		SSHUser:        strings.TrimSpace(sshUser),
		DbType:         strings.TrimSpace(row.DbType),
		DeployProvider: strings.TrimSpace(row.DeployProvider),
	}
	if portText := getStringConfig(cfg, "ssh_port", ""); strings.TrimSpace(portText) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(portText)); parseErr == nil && parsed > 0 && parsed < 65536 {
			target.SSHPort = parsed
		}
	}
	// extra_config 里的 ssh 凭据是密文入库的（与备份侧同一套 secret 幂等解密：
	// 密文还原明文，历史明文原样返回）
	password := strings.TrimSpace(getStringConfig(cfg, "ssh_password", ""))
	privateKey := strings.TrimSpace(getStringConfig(cfg, "ssh_private_key", ""))
	if decrypted, decErr := secret.Decrypt(password); decErr == nil {
		password = strings.TrimSpace(decrypted)
	}
	if decrypted, decErr := secret.Decrypt(privateKey); decErr == nil {
		privateKey = strings.TrimSpace(decrypted)
	}
	target.PrivateKey = privateKey
	target.Password = password
	// 两种凭据都配了时优先私钥：私钥更安全，且部分主机禁密码认证
	if privateKey != "" {
		target.AuthMode = "key"
	} else {
		target.AuthMode = "password"
	}
	return target, HostLoginBlockNone, nil
}

// ---------------------------------------------------------------------------
// precheck
// ---------------------------------------------------------------------------

type HostLoginPrecheckService struct {
	*forms.HostLoginPrecheckForm
	C *gin.Context
}

type HostLoginPrecheckResult struct {
	OK                 bool   `json:"ok"`
	Code               string `json:"code"`
	Message            string `json:"message"`
	SSHUser            string `json:"ssh_user"`
	SSHHost            string `json:"ssh_host"`
	SSHPort            int    `json:"ssh_port"`
	AuthMode           string `json:"auth_mode"`
	HostKeyFingerprint string `json:"host_key_fingerprint"`
	ActiveSessions     int    `json:"active_sessions"`
}

// Run 预检只验证「能不能建连」，不建立交互式会话（不申请 PTY、不挂 shell），
// 因此失败也不落审计 —— 页面每开一次就写一条噪声日志没有意义。
func (s *HostLoginPrecheckService) Run() (*HostLoginPrecheckResult, error) {
	instanceID := strings.TrimSpace(s.InstanceID)
	target, blockCode, err := ResolveHostLoginTarget(instanceID)
	if err != nil {
		return nil, err
	}
	if blockCode != HostLoginBlockNone {
		return &HostLoginPrecheckResult{
			OK:      false,
			Code:    blockCode,
			Message: hostLoginBlockNotes[blockCode],
		}, nil
	}

	active := hostLoginSessions.countByInstance(instanceID)
	result := &HostLoginPrecheckResult{
		SSHUser:        target.SSHUser,
		SSHHost:        target.Hostname,
		SSHPort:        target.SSHPort,
		AuthMode:       target.AuthMode,
		ActiveSessions: active,
	}
	if active >= hostLoginMaxPerInstance {
		result.OK = false
		result.Code = HostLoginErrSessionLimit
		result.Message = fmt.Sprintf("该实例的终端会话已达上限（%d 个），请先关闭其他终端。", hostLoginMaxPerInstance)
		return result, nil
	}

	client, fingerprint, dialErr := dialHostLoginSSH(target)
	if client != nil {
		defer client.Close()
	}
	result.HostKeyFingerprint = fingerprint
	if dialErr != nil {
		result.OK = false
		result.Code = dialErr.code
		result.Message = dialErr.message
		return result, nil
	}
	result.OK = true
	return result, nil
}

// ---------------------------------------------------------------------------
// 会话
// ---------------------------------------------------------------------------

type HostLoginSession struct {
	ID         string
	Token      string
	Username   string // 操作者（JWT 里的用户名）
	InstanceID string
	Label      string // user@host:port，用于审计与页面展示
	// 浏览器侧持久化的主机密钥指纹（TOFU）。为空表示首次登录，本次连上后由页面记录；
	// 非空则 WS 握手拨号后比对，不一致直接断连——指纹校验必须发生在 PTY 建立之前。
	ExpectedFingerprint string

	CreatedAt  time.Time
	LastActive time.Time

	WS       *websocket.Conn
	SSHPty   *HostLoginPty
	closeCh  chan struct{}
	closeOne sync.Once
	mu       sync.Mutex
}

func (s *HostLoginSession) touch() {
	s.mu.Lock()
	s.LastActive = time.Now()
	s.mu.Unlock()
}

func (s *HostLoginSession) close() {
	s.closeOne.Do(func() {
		close(s.closeCh)
		if s.SSHPty != nil {
			s.SSHPty.Close()
		}
	})
}

// HostLoginSessionManager 会话登记簿。放在内存里而不是数据库：会话是短生命周期的临时对象，
// 进程重启后旧会话本来就不可能继续存活（底层 TCP 已断），持久化只会留下垃圾数据。
type HostLoginSessionManager struct {
	mu       sync.Mutex
	sessions map[string]*HostLoginSession
}

var hostLoginSessions = &HostLoginSessionManager{sessions: make(map[string]*HostLoginSession)}

func (m *HostLoginSessionManager) add(s *HostLoginSession) {
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
}

func (m *HostLoginSessionManager) remove(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

func (m *HostLoginSessionManager) get(id string) (*HostLoginSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

// take 一次性取用：令牌校验通过后立刻作废，防止重放。
// 这也是终端页刷新后不能自动重连的原因 —— 刷新时令牌早已作废。
func (m *HostLoginSessionManager) take(id, token string) (*HostLoginSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || s.Token == "" || s.Token != token {
		return nil, false
	}
	s.Token = ""
	return s, true
}

func (m *HostLoginSessionManager) countByInstance(instanceID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.sessions {
		if s.InstanceID == instanceID {
			n++
		}
	}
	return n
}

func (m *HostLoginSessionManager) countByUser(username string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.sessions {
		if s.Username == username {
			n++
		}
	}
	return n
}

func (m *HostLoginSessionManager) listByUser(username string) []*HostLoginSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*HostLoginSession, 0, 4)
	for _, s := range m.sessions {
		if s.Username == username {
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------------------

type CreateHostLoginSessionService struct {
	*forms.CreateHostLoginSessionForm
	C *gin.Context
}

type HostLoginSessionTicket struct {
	SessionID          string `json:"session_id"`
	WSURL              string `json:"ws_url"`
	Token              string `json:"token"`
	ExpiresAt          string `json:"expires_at"`
	OK                 bool   `json:"ok"`
	Code               string `json:"code"`
	Message            string `json:"message"`
	SSHUser            string `json:"ssh_user"`
	SSHHost            string `json:"ssh_host"`
	SSHPort            int    `json:"ssh_port"`
	HostKeyFingerprint string `json:"host_key_fingerprint"`
}

// Run 只签发票据，不建 SSH 连接：真正的建连发生在 WS 握手里。
// 这样令牌过期（用户迟迟不连）时不需要收拾半开的 SSH 会话。
func (s *CreateHostLoginSessionService) Run() (*HostLoginSessionTicket, error) {
	instanceID := strings.TrimSpace(s.InstanceID)
	target, blockCode, err := ResolveHostLoginTarget(instanceID)
	if err != nil {
		return nil, err
	}
	username := CurrentHostLoginUsername(s.C)
	ticket := &HostLoginSessionTicket{SessionID: newHostLoginSessionID()}
	if blockCode != HostLoginBlockNone {
		ticket.Code = blockCode
		ticket.Message = hostLoginBlockNotes[blockCode]
		TryWriteAuditLog(s.C, AuditLogEvent{
			Username:     username,
			AuditLevel:   "WARNING",
			ModuleCode:   "datasource",
			ModuleName:   "数据源管理",
			ActionCode:   "datasource.host_login",
			ActionName:   "主机登录",
			ActionResult: "rejected",
			ResourceType: "instance",
			ResourceID:   instanceID,
			Summary:      fmt.Sprintf("主机登录被拒绝：%s", ticket.Message),
		})
		return ticket, nil
	}

	if hostLoginSessions.countByInstance(instanceID) >= hostLoginMaxPerInstance {
		ticket.Code = HostLoginErrSessionLimit
		ticket.Message = fmt.Sprintf("该实例的终端会话已达上限（%d 个），请先关闭其他终端。", hostLoginMaxPerInstance)
		return ticket, nil
	}
	if hostLoginSessions.countByUser(username) >= hostLoginMaxPerUser {
		ticket.Code = HostLoginErrSessionLimit
		ticket.Message = fmt.Sprintf("您的终端会话已达上限（%d 个），请先关闭其他终端。", hostLoginMaxPerUser)
		return ticket, nil
	}

	// 指纹不在签发阶段校验：那时拨一次 SSH 只为拿指纹，WS 握手还要再拨一次，
	// 等于每次开终端都要连三下主机。改由 WS 握手时比对，阻断点仍在 PTY 建立之前。
	session := &HostLoginSession{
		ID:                  ticket.SessionID,
		Token:               newHostLoginToken(),
		Username:            username,
		InstanceID:          instanceID,
		Label:               fmt.Sprintf("%s@%s:%d", target.SSHUser, target.Hostname, target.SSHPort),
		ExpectedFingerprint: strings.TrimSpace(s.HostKeyFingerprint),
		CreatedAt:           time.Now(),
		LastActive:          time.Now(),
		closeCh:             make(chan struct{}),
	}
	// 目标信息随会话带走，WS 握手时直接用，避免二次解析与二次解密
	hostLoginTargets.Store(session.ID, target)
	hostLoginSessions.add(session)

	// 令牌 60 秒内没有用来建 WS 就作废，避免留下一批永远不会被连上的空会话
	go func() {
		timer := time.NewTimer(hostLoginTokenTTL)
		defer timer.Stop()
		select {
		case <-session.closeCh:
			return
		case <-timer.C:
			if s, ok := hostLoginSessions.get(session.ID); ok && s.SSHPty == nil {
				s.close()
				hostLoginSessions.remove(session.ID)
				hostLoginTargets.Delete(session.ID)
			}
		}
	}()

	ticket.OK = true
	ticket.Token = session.Token
	ticket.WSURL = fmt.Sprintf("/api/v1/datasource/host-login/ws/%s", session.ID)
	ticket.ExpiresAt = time.Now().Add(hostLoginTokenTTL).Format("2006-01-02 15:04:05")
	ticket.SSHUser = target.SSHUser
	ticket.SSHHost = target.Hostname
	ticket.SSHPort = target.SSHPort
	return ticket, nil
}

// ---------------------------------------------------------------------------
// SSH 拨号
// ---------------------------------------------------------------------------

type hostLoginDialError struct {
	code    string
	message string
	detail  string
}

// dialHostLoginSSH 拨号并返回主机密钥指纹。
// expectedFingerprint 为空表示首次连接（接受并记录指纹，TOFU）。
func dialHostLoginSSH(target *HostLoginTarget) (*ssh.Client, string, *hostLoginDialError) {
	authMethods := make([]ssh.AuthMethod, 0, 3)
	hasKey, hasPwd := false, false
	if strings.TrimSpace(target.PrivateKey) != "" {
		signer, err := ssh.ParsePrivateKey([]byte(target.PrivateKey))
		if err != nil {
			return nil, "", &hostLoginDialError{
				code:    HostLoginErrKeyInvalid,
				message: "SSH 私钥格式无效或私钥已加密（不支持带 passphrase 的私钥）。",
				detail:  err.Error(),
			}
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
		hasKey = true
	}
	if strings.TrimSpace(target.Password) != "" {
		authMethods = append(authMethods, ssh.Password(target.Password))
		// 部分主机（尤其等保加固镜像）只开 keyboard-interactive，
		// 不带这个会在 PasswordAuthentication 关闭时直接协商失败
		authMethods = append(authMethods, ssh.KeyboardInteractive(
			func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, 0, len(questions))
				for range questions {
					answers = append(answers, target.Password)
				}
				return answers, nil
			}))
		hasPwd = true
	}
	if len(authMethods) == 0 {
		return nil, "", &hostLoginDialError{
			code:    HostLoginErrCredentialIncomplete,
			message: hostLoginBlockNotes[HostLoginBlockCredentialIncomplete],
		}
	}

	fingerprint := ""
	config := &ssh.ClientConfig{
		User: strings.TrimSpace(target.SSHUser),
		Auth: authMethods,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			fingerprint = ssh.FingerprintSHA256(key)
			return nil
		},
		Timeout: hostLoginDialTimeout,
	}
	addr := fmt.Sprintf("%s:%d", strings.TrimSpace(target.Hostname), target.SSHPort)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fingerprint, &hostLoginDialError{
			code:    classifyHostLoginDialError(err, hasKey, hasPwd),
			message: hostLoginDialMessage(err, target, hasKey, hasPwd),
			detail:  err.Error(),
		}
	}
	return client, fingerprint, nil
}

// classifyHostLoginDialError 把 Go 的底层错误翻译成前端可分支的失败码。
// 分类顺序有讲究：认证类必须先于网络类判断，否则 auth 失败也常被报成超时。
func classifyHostLoginDialError(err error, hasKey, hasPwd bool) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "unable to authenticate"), strings.Contains(msg, "no supported methods remain"):
		if hasPwd && !hasKey {
			return HostLoginErrAuthFailed
		}
		return HostLoginErrKeyInvalid
	case strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "deadline exceeded"):
		return HostLoginErrConnectTimeout
	case strings.Contains(msg, "connection refused"):
		return HostLoginErrConnRefused
	case strings.Contains(msg, "no route to host"), strings.Contains(msg, "network is unreachable"),
		strings.Contains(msg, "no such host"):
		return HostLoginErrNetworkUnreachable
	case strings.Contains(msg, "handshake failed"), strings.Contains(msg, "no common algorithm"):
		// 老 OpenSSH（算法协商不上）与国密都无法在 x/crypto/ssh 上工作，统一归到这一类
		return HostLoginErrAlgorithmMismatch
	default:
		return HostLoginErrConnectTimeout
	}
}

func hostLoginDialMessage(err error, target *HostLoginTarget, hasKey, hasPwd bool) string {
	addr := fmt.Sprintf("%s:%d", strings.TrimSpace(target.Hostname), target.SSHPort)
	switch classifyHostLoginDialError(err, hasKey, hasPwd) {
	case HostLoginErrAuthFailed:
		message := "主机认证失败：用户名或密码不正确。"
		// Windows 常见误配：拿数据库账号当 SSH 账号（root 在 Windows 上不存在）
		if hostLoginTargetOnWindowsLike(target) {
			message += "Windows 主机请使用系统账号（如 Administrator），数据库账号（如 sa）不能用于 SSH 登录。"
		}
		return message
	case HostLoginErrKeyInvalid:
		return "SSH 私钥认证失败：私钥与主机上的公钥不匹配。"
	case HostLoginErrConnectTimeout:
		return fmt.Sprintf("连接主机超时（%d 秒）：%s 未响应。", int(hostLoginDialTimeout.Seconds()), addr) +
			hostLoginWindowsHint(target)
	case HostLoginErrConnRefused:
		return fmt.Sprintf("主机拒绝连接（Connection refused）：%s 上的 SSH 服务未启动或端口不正确。", addr) +
			hostLoginWindowsHint(target)
	case HostLoginErrNetworkUnreachable:
		return fmt.Sprintf("网络不可达：服务端到 %s 不通，请检查安全组与防火墙。", addr)
	case HostLoginErrAlgorithmMismatch:
		return "SSH 算法协商失败：主机可能使用国密算法或 OpenSSH 版本过旧。"
	default:
		return "连接主机失败。"
	}
}

// hostLoginTargetOnWindowsLike 该引擎的高频部署形态是 Windows 主机（SQL Server 几乎必是，
// Oracle 也常见），用于在连接/认证失败时给出针对性提示。
func hostLoginTargetOnWindowsLike(target *HostLoginTarget) bool {
	if target == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(target.DbType)) {
	case "sqlserver", "oracle":
		return true
	}
	return false
}

// hostLoginWindowsHint 连接类失败（超时/拒绝）时，对 Windows 高频引擎追加
// 「主机侧需要先装 OpenSSH Server」的引导——Windows 默认没有 SSH 服务，
// 这不是平台或凭据的问题，主机装完服务后无需任何平台侧改动即可登录。
func hostLoginWindowsHint(target *HostLoginTarget) string {
	if !hostLoginTargetOnWindowsLike(target) {
		return ""
	}
	return "若该实例部署在 Windows 主机上，需先在主机上安装并启动 OpenSSH Server" +
		"（管理员 PowerShell：Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0，再执行 Start-Service sshd）后重试。"
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

// CurrentHostLoginUsername 与 response.extractUserName 同口径：JWT claim 的 id 就是用户名。
// 导出是为了让 view 层（关闭会话、列会话）也能拿到操作者，避免各写一份取值逻辑。
func CurrentHostLoginUsername(c *gin.Context) string {
	if c == nil {
		return ""
	}
	raw, ok := jwt.ExtractClaims(c)["id"]
	if !ok {
		return ""
	}
	username, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(username)
}

// 会话 ID 与令牌都用 UUID：会话 ID 要在 URL 里出现，令牌要不可猜
func newHostLoginSessionID() string { return strings.ReplaceAll(uuid.NewString(), "-", "") }
func newHostLoginToken() string     { return strings.ReplaceAll(uuid.NewString(), "-", "") }

// 会话 → 目标 的旁路映射：WS 握手时要用目标信息，但不想把它塞进 session 结构体
// （目标含明文口令，生命周期应尽可能短）
var hostLoginTargets = &hostLoginTargetStore{items: make(map[string]*HostLoginTarget)}

type hostLoginTargetStore struct {
	mu    sync.Mutex
	items map[string]*HostLoginTarget
}

func (s *hostLoginTargetStore) Store(id string, t *HostLoginTarget) {
	s.mu.Lock()
	s.items[id] = t
	s.mu.Unlock()
}

func (s *hostLoginTargetStore) Load(id string) (*HostLoginTarget, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.items[id]
	return t, ok
}

func (s *hostLoginTargetStore) Delete(id string) {
	s.mu.Lock()
	delete(s.items, id)
	s.mu.Unlock()
}

// 审计用的请求载荷：只记连接目标，绝不记凭据
func hostLoginRequestPayload(target *HostLoginTarget) map[string]any {
	if target == nil {
		return nil
	}
	return map[string]any{
		"instance_id": target.InstanceID,
		"ssh_host":    target.Hostname,
		"ssh_port":    target.SSHPort,
		"ssh_user":    target.SSHUser,
		"auth_mode":   target.AuthMode,
	}
}
