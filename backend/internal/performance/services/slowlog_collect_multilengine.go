package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/internal/global"
	performanceModels "github.com/simajiong/jaysql/internal/performance/models"
	"github.com/simajiong/jaysql/pkg/secret"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ============================================================================
// PostgreSQL / Oracle 的采集实现
//
// 采集策略分两类：
//   文件类（MySQL / PostgreSQL）：SSH 登录数据库主机 → 按 inode + offset 增量读
//                                → 交给对应解析器 → 入库
//   采样类（Oracle）            ：直接查 V$SQL → 与上次采样做差分 → 入库
//
// 两类共用同一个采集器行（insight_performance_slowlog_collectors）记录状态，
// 因此前端的状态列与前提条件面板对三种引擎是同一套逻辑。
// ============================================================================

// chunkParser 把一段日志文本解析成若干慢日志事件。
type chunkParser func(content string, baseOffset int64) ([]parsedSlowLogEvent, string, error)

// collectPostgresSlowLogForInstance 采集 PG 服务端日志中的慢 SQL。
//
// 与 MySQL 版本唯一的区别是解析器：PG 没有独立慢日志文件，慢 SQL 混在服务日志里，
// 靠 `LOG:  duration: ... ms  statement: ...` 这个模式识别；
// 而 MySQL 是 `# Query_time: ...` 的块结构。两者无法共用解析器。
func collectPostgresSlowLogForInstance(instance *commonModels.InsightInstances,
	collector *performanceModels.InsightPerformanceSlowLogCollectors) {
	// 采集前先问服务端「现在写的是哪个日志文件」：PG 默认 log_filename 带日期，
	// 跨天会换文件，路径不刷新就会一直读昨天的文件（表现为「无新增」）。
	refreshPostgresLogPath(instance, collector)
	collectFileSlowLogForInstance(instance, collector, "PostgreSQL", parsePostgresSlowLogChunk)
}

// pgLogPathRefreshInterval 控制刷新当前日志文件路径的最小间隔。
// 采集循环 20s 一轮，查一次 pg_current_logfile() 虽是本地函数，
// 但会额外建一次数据库连接，一分钟一次足够跟上每天一次的轮转。
const (
	pgLogPathRefreshInterval = time.Minute
	pgLogPathQueryTimeout    = 3 * time.Second
)

// pgLogPathRefreshAt 记录各实例上次刷新路径的时间（进程内，重启即失效）。
var pgLogPathRefreshAt sync.Map

// refreshPostgresLogPath 重新解析 PG 当前日志文件的绝对路径并同步到采集器。
//
// 只在「路径变了」或「还没路径」时写库：
//   - 换文件必须把 read_offset 归零、pending_buffer 清空，
//     否则会拿旧文件的偏移去截新文件，丢掉开头一整段；
//   - 查询失败（网络抖动、权限临时异常）时保留原路径继续采集，不阻断链路。
func refreshPostgresLogPath(instance *commonModels.InsightInstances,
	collector *performanceModels.InsightPerformanceSlowLogCollectors) {
	if instance == nil || collector == nil {
		return
	}
	instanceID := strings.TrimSpace(instance.InstanceID)
	if instanceID == "" {
		return
	}
	// 人工指定的路径优先级最高，不再被探测覆盖
	if readSlowLogPathOverride(instance) != "" {
		return
	}
	if last, ok := pgLogPathRefreshAt.Load(instanceID); ok {
		if at, _ := last.(time.Time); time.Since(at) < pgLogPathRefreshInterval {
			return
		}
	}
	pgLogPathRefreshAt.Store(instanceID, time.Now())

	path := queryPostgresCurrentLogFile(instance)
	if path == "" || path == strings.TrimSpace(collector.LogPath) {
		return
	}

	collector.LogPath = path
	collector.ResolvedLogPath = path
	_ = global.App.DB.Model(collector).Updates(map[string]any{
		"log_path":          path,
		"resolved_log_path": path,
		"read_offset":       int64(0),
		"pending_buffer":    "",
		"file_inode":        "",
	}).Error
}

// queryPostgresCurrentLogFile 连接实例取当前日志文件的绝对路径。
//
// 三个值一起查，避免多开一次连接；任一步失败都返回空串，
// 由调用方保留原路径继续采集。
func queryPostgresCurrentLogFile(instance *commonModels.InsightInstances) string {
	password := strings.TrimSpace(string(instance.Password))
	if password != "" {
		if plain, err := secret.Decrypt(password); err == nil && strings.TrimSpace(plain) != "" {
			password = strings.TrimSpace(plain)
		}
	}
	user := strings.TrimSpace(string(instance.User))
	if user == "" || password == "" {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), pgLogPathQueryTimeout)
	defer cancel()

	db := dao.PostgresDB{
		User:     user,
		Password: password,
		Host:     strings.TrimSpace(instance.Hostname),
		Port:     instance.Port,
		Database: pgConnectDatabase(instance),
		Ctx:      ctx,
	}
	_, rows, err := db.Query(`SELECT pg_current_logfile() AS current_logfile,
       current_setting('log_directory') AS log_directory,
       current_setting('data_directory') AS data_directory`)
	if err != nil || rows == nil || len(*rows) == 0 {
		return ""
	}

	currentLogFile, logDir, dataDir := "", "", ""
	for column, raw := range (*rows)[0] {
		value := strings.TrimSpace(stringifyVariableValue(raw))
		if value == "<nil>" {
			value = ""
		}
		switch strings.ToLower(strings.TrimSpace(column)) {
		case "current_logfile":
			currentLogFile = value
		case "log_directory":
			logDir = value
		case "data_directory":
			dataDir = value
		}
	}
	return joinPGLogPath(dataDir, logDir, currentLogFile)
}

// collectFileSlowLogForInstance 文件类引擎的通用采集流程。
//
// 抽出来的原因：MySQL 和 PG 的采集流程九成相同（连 SSH → 取 inode/offset →
// 增量读 → 落 pending_buffer），只有解析器不同。之前硬绑 MySQL，
// 新增引擎就得复制一整段；现在新引擎只要提供一个解析器。
func collectFileSlowLogForInstance(instance *commonModels.InsightInstances,
	collector *performanceModels.InsightPerformanceSlowLogCollectors,
	dbType string, parser chunkParser) {
	if instance == nil || collector == nil {
		return
	}

	startedAt := time.Now()
	_ = global.App.DB.Model(collector).Updates(map[string]any{
		"collect_status":       slowLogCollectStatusRunning,
		"last_collect_at":      commonModels.LocalTime(startedAt),
		"last_error":           "",
		"last_collect_message": "",
	}).Error

	sshUser, sshPassword, sshPrivateKey, sshPort, ok, reason := resolveSSHCredentialsFromPerformanceInstance(instance)
	if !ok {
		markCollectorFailureWithCode(collector, fmt.Sprintf("缺少 SSH 凭据：%s", reason),
			"", failEngineNoCredential)
		return
	}

	client, err := openSSHClient(strings.TrimSpace(instance.Hostname), sshPort, sshUser, sshPassword, sshPrivateKey)
	if err != nil {
		markCollectorFailureWithCode(collector, fmt.Sprintf("SSH 连接失败：%v", err),
			"", failEngineSSHFailed)
		return
	}
	defer client.Close()

	postgres := strings.EqualFold(strings.TrimSpace(dbType), "PostgreSQL") || strings.EqualFold(strings.TrimSpace(dbType), "Postgres")
	remoteState, err := inspectRemoteSlowLog(client, collector.LogPath, collector.ReadOffset, collector.FileInode, postgres)
	if err != nil {
		if errors.Is(err, errPGLogPathUnresolved) {
			// 探测没拿到 pg_current_logfile() 时的明确结论：
			// 属于参数/权限层面未就绪，不是「文件被删了」，不该报成故障红字。
			markCollectorFailureWithCode(collector,
				"未能定位 PostgreSQL 当前日志文件：pg_current_logfile() 无返回，请检查 log_directory 配置与账号的 pg_monitor 角色，或在实例配置中手动指定慢日志路径",
				"", failPGLogPathUnresolved)
			return
		}
		if isSlowLogNotFoundError(err) {
			markCollectorFailureWithCode(collector, fmt.Sprintf("慢日志文件不可读：%v", err),
				capabilityFileMissing, failEngineFileMissing)
			return
		}
		markCollectorFailureWithCode(collector, fmt.Sprintf("读取慢日志状态失败：%v", err),
			"", failEngineSSHFailed)
		return
	}

	prevOffset := collector.ReadOffset
	if remoteState.Offset != collector.ReadOffset {
		collector.PendingBuffer = ""
	}
	if remoteState.Offset == collector.ReadOffset && remoteState.Tail == "" {
		readOK := map[string]any{
			"file_inode":           remoteState.Inode,
			"file_size":            remoteState.Size,
			"read_offset":          remoteState.Offset,
			"collect_status":       slowLogCollectStatusIdle,
			"last_collect_message": "无新增慢日志",
			"last_error":           "",
			"failure_code":         "",
			"last_notified_error":  "",
		}
		if strings.TrimSpace(collector.CapabilityState) == capabilityFileMissing {
			readOK["capability_state"] = capabilityEnabledFile
			collector.CapabilityState = capabilityEnabledFile
		}
		_ = global.App.DB.Model(collector).Updates(readOK).Error
		return
	}

	content := collector.PendingBuffer + remoteState.Tail
	// PG：本轮被跳过的协议阶段行数（parse/bind），仅用于采集消息。
	// 让用户能把「日志里 duration 涨了几百条」与「页面只多几条」对上账。
	protocolSkipped := 0
	if postgres {
		protocolSkipped = len(pgProtocolPhaseRegexp.FindAllStringIndex(content, -1))
	}
	events, rest, err := parser(content, remoteState.Offset-int64(len(remoteState.Tail)))
	if err != nil {
		markCollectorFailureWithCode(collector, fmt.Sprintf("解析慢日志失败：%v", err),
			"", failEngineParseFailed)
		return
	}

	inserted := 0
	var lastEventTime time.Time
	for _, event := range events {
		model := performanceModels.InsightPerformanceSlowLogEntries{
			InstanceID:     strings.TrimSpace(instance.InstanceID),
			DBType:         commonModels.EnumType(dbType),
			LogPath:        collector.LogPath,
			FileInode:      remoteState.Inode,
			OffsetStart:    event.OffsetStart,
			OffsetEnd:      event.OffsetEnd,
			EventTime:      commonModels.LocalTime(event.EventTime),
			CollectTime:    commonModels.LocalTime(time.Now()),
			Username:       event.Username,
			ClientHost:     event.ClientHost,
			SchemaName:     event.SchemaName,
			QueryTimeMS:    event.QueryTimeMS,
			LockTimeMS:     event.LockTimeMS,
			RowsSent:       event.RowsSent,
			RowsExamined:   event.RowsExamined,
			ThreadID:       event.ThreadID,
			SQLText:        event.SQLText,
			SQLFingerprint: event.SQLFingerprint,
			SQLDigest:      event.SQLDigest,
			ParseStatus:    event.ParseStatus,
			ParseError:     event.ParseError,
			RawText:        event.RawText,
			ExecCount:      1,
		}
		model.EventKey = slowLogEventKey(collector.InstanceID, remoteState.Inode, event.OffsetStart)

		tx := global.App.DB.Where("event_key = ?", model.EventKey).FirstOrCreate(&model)
		if tx.Error != nil {
			markCollectorFailureWithCode(collector, fmt.Sprintf("入库慢日志失败：%v", tx.Error),
				"", failEnginePersistFailed)
			return
		}
		if tx.RowsAffected > 0 {
			inserted++
		}
		if !event.EventTime.IsZero() && event.EventTime.After(lastEventTime) {
			lastEventTime = event.EventTime
		}
	}

	collectMessage := fmt.Sprintf("采集成功：新增 %d 条，偏移 %d -> %d", inserted, prevOffset, remoteState.Offset)
	if protocolSkipped > 0 {
		// 不写「跳过」就少一半信息：用户会以为平台漏采了
		collectMessage = fmt.Sprintf(
			"采集成功：新增 %d 条，跳过 %d 条协议阶段记录（parse/bind，非独立执行，不计入慢SQL），偏移 %d -> %d",
			inserted, protocolSkipped, prevOffset, remoteState.Offset)
	}

	updates := map[string]any{
		"file_inode":           remoteState.Inode,
		"file_size":            remoteState.Size,
		"read_offset":          remoteState.Offset,
		"pending_buffer":       rest,
		"collect_status":       slowLogCollectStatusIdle,
		"last_collect_message": collectMessage,
		"last_success_at":      commonModels.LocalTime(time.Now()),
		"last_error":           "",
		"failure_code":         "",
		"last_notified_error":  "",
	}
	if strings.TrimSpace(collector.CapabilityState) == capabilityFileMissing {
		updates["capability_state"] = capabilityEnabledFile
		collector.CapabilityState = capabilityEnabledFile
	}
	if strings.TrimSpace(remoteState.ResolvedPath) != "" &&
		strings.TrimSpace(remoteState.ResolvedPath) != strings.TrimSpace(collector.LogPath) {
		updates["log_path"] = strings.TrimSpace(remoteState.ResolvedPath)
		collector.LogPath = strings.TrimSpace(remoteState.ResolvedPath)
	}
	if !lastEventTime.IsZero() {
		updates["last_event_at"] = commonModels.LocalTime(lastEventTime)
	}
	_ = global.App.DB.Model(collector).Updates(updates).Error
}

// ---------- PostgreSQL 日志解析 ----------

// pgDurationLineRegexp 匹配 PG text 格式的慢 SQL 日志行。
//
// 典型行（log_line_prefix = '%m [%p] %u@%d '）：
//
//	2026-10-23 12:03:41.512 CST [28471] app_user@app_db 10.0.0.7 LOG:  duration: 1234.567 ms  statement: SELECT ...
//
// 捕获组：1=行首前缀（log_line_prefix 渲染结果） 2=耗时(ms) 3=语句类型 4=SQL
//
// ⚠️ 这里踩过两个很隐蔽的坑：
//
// ① 旧版把「时间戳 + [pid] + user@db + LOG:」写成一条**严格相邻**的完整正则，
// 于是只要 log_line_prefix 不是它期望的那一种布局，整行就失配。而探测阶段只校验了
// prefix 含 %u 和 %d 就判 ENABLED_FILE，采集器一路读文件、一路"采集成功：新增 0 条"，
// 页面上干干净净 0 条慢 SQL，不报任何错。现在改为只锚定 PG 自身固定不变的
// `LOG:  duration: ... ms  statement|execute ...:` 结构，前缀字段尽力提取、缺失不丢整条。
//
// ② `LOG:` 的**前置空白不可强制要求**。log_line_prefix 结尾没写空格时（例如
// `%m [%p] %q%u@%d,app=%a,client=%h`），渲染结果是 `client=192.168.1.3LOG:  duration:`，
// IP 与 `LOG:` 直接相连。用 `\s+LOG:` 会把这台实例的每一行都判为失配。
//
// ③ `statement` 之外还必须认 `execute`。走扩展协议（JDBC / pgx / Npgsql 等
// prepared statement）时 PG 输出的是 `duration: X ms  execute <name>: SQL`，
// 根本不会出现 `statement:`；只认 statement 等于把这类应用的慢 SQL 全部漏掉。
// `parse` / `bind` 行虽也带 duration，但那是解析与绑定阶段耗时，不计入慢 SQL。
var pgDurationLineRegexp = regexp.MustCompile(
	`^(.*?)\s*LOG:\s+duration:\s+([0-9.]+)\s+ms\s+(statement|execute)(?:\s+[^:]*)?:\s*(.*)$`)

// pgNewLineRegexp 判定一行是否是新日志行的开始（用于拼接被换行截断的 SQL）。
//
// 除了「以 YYYY-MM-DD 开头」这条原判据，再补一条「行内含 duration 锚点」：
// 有些部署把 log_line_prefix 配成不带时间戳（或时间戳在方括号里），
// 少了这条，多行 SQL 会被当成续行粘到上一条记录上，整条指纹就错了。
var pgNewLineRegexp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} `)

// pgNewLineAnchoredRegexp 行内含 `LOG:  duration:` 也算新日志行的开始。
// 同样不能用 `\s+LOG:`（前缀末尾无空格时 LOG: 会紧贴前一个字段）。
var pgNewLineAnchoredRegexp = regexp.MustCompile(`LOG:\s+duration:\s+[0-9.]+\s+ms`)

// pgProtocolPhaseRegexp 扩展协议的准备/绑定阶段行（parse/bind/describe/close）。
//
// 这些行与 execute 属于**同一次查询的不同阶段**，不是独立的慢SQL执行：
// parse 含语法分析与计划生成，常 >1ms 而被记录；execute 往往 <1ms 不记录。
// 于是一条业务 SQL 可能在日志里留下 parse + bind 两条 duration，execute 一条都没有。
// 若计入慢SQL数量，同一条 SQL 会被重复算 2~3 次，还混进纯计划开销。
//
// 解析时跳过它们，但把条数透出到采集消息（见 collectFileSlowLogForInstance）：
// 否则用户 grep duration 看到上千条、页面只多几十条，会误判成平台漏采——
// 这正是「日志涨了几百条、页面才 51 条」的成因。
var pgProtocolPhaseRegexp = regexp.MustCompile(`(?m)LOG:\s+duration:\s+[0-9.]+\s+ms\s+(?:parse|bind|describe|close)\b`)

// pgTimestampHeadRegexp 从前缀里取开头的日志时间戳。
//
// 时区缩写（CST/UTC 等）必须一起捕获：早期版本只截到毫秒就停，
// 交给 parsePGTimestamp 后走「无时区 → UTC」的兜底路径，而 PG 的 %m
// 输出的是 log_timezone 的墙上时钟（如 Asia/Shanghai 渲染成 CST），
// 结果 event_time 全部超前 8 小时、落在页面 24h 统计窗口之外——
// 采集器报成功、实例有慢SQL、页面数量却是 0。缩写捕获不到时
// （个别时区渲染成 +08 这类数字形态）由 parsePGTimestamp 按本地时区兜底。
var pgTimestampHeadRegexp = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?(?:\s+[A-Za-z]{2,5})?)`)

// pgPidRegexp 从前缀里取进程号（%p 通常渲染成 [12345]）。
var pgPidRegexp = regexp.MustCompile(`\[(\d+)\]`)

// pgUserDBAtRegexp 从前缀里取 `user@db` 形态（%u@%d）。
//
// 库名必须以 `,` `]` 或空白为界：log_line_prefix 里 `%d` 后面常紧跟 `,app=%a`，
// 用 `[^\s\[\]]+` 会把库名吃成 `postgres,app=`。
var pgUserDBAtRegexp = regexp.MustCompile(`([^,\s@\[\]]+)@([^,\s\[\]]+)`)

// pgUserDBKvRegexp 从前缀里取 `user=xxx,db=yyy` 形态（Patroni/CKP 等常用）。
var pgUserDBKvRegexp = regexp.MustCompile(`(?i)\buser=([^,\s]+)[,\s]+\bdb=([^,\s]+)`)

// pgClientKvRegexp 从前缀里取 `client=xxx` 形态。
var pgClientKvRegexp = regexp.MustCompile(`(?i)\bclient=([^,\s]+)`)

// pgClientIPRegexp 从前缀里取裸 IPv4 地址。
var pgClientIPRegexp = regexp.MustCompile(`\b(\d{1,3}(?:\.\d{1,3}){3})\b`)

// isNewPGLogLine 判定 raw[j] 是否开启了一条新的日志行。
func isNewPGLogLine(line string) bool {
	return pgNewLineRegexp.MatchString(line) || pgNewLineAnchoredRegexp.MatchString(line)
}

// extractPGLogPrefixFields 从日志行前缀里尽力提取进程号 / 用户 / 库 / 客户端地址。
//
// 设计口径：前缀是**尽力而为**的补充信息，提取不到就留空字符串，
// 绝不能因此丢弃整条慢 SQL —— 耗时与 SQL 文本才是分析的主体。
func extractPGLogPrefixFields(head string) (pid string, username string, schema string, client string) {
	if m := pgUserDBAtRegexp.FindStringSubmatch(head); len(m) == 3 {
		username, schema = strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	}
	if username == "" || schema == "" {
		if m := pgUserDBKvRegexp.FindStringSubmatch(head); len(m) == 3 {
			username, schema = strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
		}
	}
	if m := pgPidRegexp.FindStringSubmatch(head); len(m) == 2 {
		pid = strings.TrimSpace(m[1])
	}
	if m := pgClientKvRegexp.FindStringSubmatch(head); len(m) == 2 {
		client = strings.TrimSpace(m[1])
	} else if m := pgClientIPRegexp.FindStringSubmatch(head); len(m) == 2 {
		client = strings.TrimSpace(m[1])
	}
	return
}

// pgTimestampLayouts PG 日志时间戳格式。
var pgTimestampLayouts = []string{
	"2006-01-02 15:04:05.000 MST",
	"2006-01-02 15:04:05 MST",
	"2006-01-02 15:04:05.000",
	"2006-01-02 15:04:05",
}

// parsePostgresSlowLogChunk 解析一段 PG 服务端日志，提取慢 SQL。
//
// 识别完全依赖 `LOG:  duration: ... ms  statement: ...` 这个固定结构；
// 前缀字段（进程号 / 用户 / 库 / 客户端）由 extractPGLogPrefixFields 尽力提取，
// 缺失不影响成条。
func parsePostgresSlowLogChunk(content string, baseOffset int64) ([]parsedSlowLogEvent, string, error) {
	text := strings.ReplaceAll(content, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil, "", nil
	}

	raw := strings.SplitAfter(text, "\n")
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}

	events := make([]parsedSlowLogEvent, 0, 8)
	consumed := int64(0)
	i := 0
	for i < len(raw) {
		line := strings.TrimSuffix(raw[i], "\n")
		matches := pgDurationLineRegexp.FindStringSubmatch(line)
		// 组：1=前缀 2=耗时(ms) 3=语句类型(statement|execute) 4=SQL
		//
		// ⚠️ 这里**不能**因为 matches[4] 为空就跳过整条。走扩展协议时 PG 输出的是
		// `duration: 3.099 ms  execute stmtcache_378: ` 后面直接换行，语句正文全在续行里。
		// 早期要求首行 SQL 非空，实测 27 条 duration 行里有 22 条被这一条判据丢掉。
		// 正确做法：先收下这条事件，把续行拼完再判空。
		if len(matches) < 5 {
			consumed += int64(len(raw[i]))
			i++
			continue
		}

		sqlText := strings.TrimSpace(matches[4])
		used := int64(len(raw[i]))
		rawBlock := []string{line}
		j := i + 1
		// 续行：SQL 内部含换行时，PG 会原样输出后续行，直到下一个日志行开头
		for j < len(raw) && !isNewPGLogLine(raw[j]) {
			rawBlock = append(rawBlock, strings.TrimSuffix(raw[j], "\n"))
			sqlText += " " + strings.TrimSpace(raw[j])
			used += int64(len(raw[j]))
			j++
		}
		sqlText = strings.TrimSpace(sqlText)
		if sqlText == "" {
			// 语句名之后既无正文也无续行：不是可分析的慢 SQL，只推进偏移
			consumed += used
			i = j
			continue
		}
		if len(sqlText) > 4000 {
			sqlText = sqlText[:4000]
		}

		durationMS := 0
		if parsed, err := strconv.ParseFloat(matches[2], 64); err == nil {
			durationMS = int(parsed)
		}

		pid, username, schemaName, clientHost := extractPGLogPrefixFields(matches[1])
		eventTime := time.Time{}
		if timestamp := pgTimestampHeadRegexp.FindStringSubmatch(matches[1]); timestamp != nil {
			eventTime = parsePGTimestamp(timestamp[1])
		}
		if eventTime.IsZero() {
			if timestamp := pgTimestampHeadRegexp.FindStringSubmatch(line); timestamp != nil {
				eventTime = parsePGTimestamp(timestamp[1])
			}
		}
		if eventTime.IsZero() {
			eventTime = time.Now()
		}

		event := parsedSlowLogEvent{
			OffsetStart: baseOffset + consumed,
			OffsetEnd:   baseOffset + consumed + used,
			EventTime:   eventTime,
			Username:    username,
			SchemaName:  schemaName,
			ClientHost:  clientHost,
			ThreadID:    pid,
			SQLText:     sqlText,
			QueryTimeMS: durationMS,
			ParseStatus: slowLogParseStatusParsed,
			RawText:     strings.Join(rawBlock, "\n"),
		}
		event.SQLFingerprint = normalizeSQLFingerprint(sqlText)
		sum := sha256.Sum256([]byte(event.SQLFingerprint))
		event.SQLDigest = hex.EncodeToString(sum[:])
		events = append(events, event)

		consumed += used
		i = j
	}

	rest := ""
	if !strings.HasSuffix(text, "\n") {
		idx := strings.LastIndex(text, "\n")
		rest = text[idx+1:]
	}
	return events, rest, nil
}

// ---------- Oracle V$SQL 采样 ----------

const (
	// oracleSampleTimeout 单次采样超时：库缓存大时 v$sql 全扫可能较慢，
	// 但不能无限等，否则会拖住整个 cron（采集是串行跑的）。
	oracleSampleTimeout = 30 * time.Second
	// oracleDefaultSlowThresholdMS Oracle 没有 long_query_time 之类的服务端阈值，
	// 只能由平台侧在入库前过滤。缺省 1000ms，可用 extra_config.slow_log_threshold_ms 覆盖。
	oracleDefaultSlowThresholdMS = 1000
)

// oracleSlowLogThresholdMS 读取该实例的「算慢」阈值（毫秒）。
func oracleSlowLogThresholdMS(extra datatypes.JSON) int {
	text, ok := stringFromExtraConfig(extra, "slow_log_threshold_ms")
	if !ok {
		return oracleDefaultSlowThresholdMS
	}
	value, err := strconv.Atoi(text)
	if err != nil || value <= 0 {
		return oracleDefaultSlowThresholdMS
	}
	return value
}

// collectOracleSlowLogForInstance Oracle 慢 SQL 采集主流程（V$SQL 增量采样）。
//
// 流程：取上次累计基线 → 查 v$sql 抓窗口内活跃 SQL → 与基线做差 →
//
//	过滤「平均耗时低于阈值」的 SQL → 落 entries → 回写基线。
//
// 与文件类采集最大的不同：这里没有 SSH、没有 inode、没有 offset。
// 幂等靠 event_key（VSQL|instance|sql_id|plan_hash|采样时刻），
// 同一轮重复执行不会产生重复行。
func collectOracleSlowLogForInstance(instance *commonModels.InsightInstances,
	collector *performanceModels.InsightPerformanceSlowLogCollectors) {
	if instance == nil || collector == nil {
		return
	}
	instanceID := strings.TrimSpace(instance.InstanceID)
	startedAt := time.Now()
	_ = global.App.DB.Model(collector).Updates(map[string]any{
		"collect_status":       slowLogCollectStatusRunning,
		"last_collect_at":      commonModels.LocalTime(startedAt),
		"last_error":           "",
		"last_collect_message": "",
	}).Error

	password := instancePlainPassword(instance)
	if password == "" {
		markCollectorFailureWithCode(collector,
			"Oracle 实例密码缺失或解密失败，无法建立采样连接",
			capabilityProbeFailed, failOracleNoCredential)
		return
	}
	if strings.TrimSpace(serviceNameFromExtra(instance.ExtraConfig)) == "" {
		markCollectorFailureWithCode(collector,
			"实例扩展参数未配置 service_name，无法建立 Oracle 连接",
			capabilityProbeFailed, failOracleNoCredential)
		return
	}

	baseline := loadOracleSampleBaseline(instanceID)
	ctx, cancel := context.WithTimeout(context.Background(), oracleSampleTimeout)
	defer cancel()

	samples, sampleAt, err := collectOracleSlowLogSample(ctx, instance, password, baseline.since, oracleMaxSamples)
	if err != nil {
		markCollectorFailureWithCode(collector,
			fmt.Sprintf("Oracle V$SQL 采样失败：%v", err),
			capabilityProbeFailed, failOracleSampleFailed)
		return
	}

	thresholdMS := oracleSlowLogThresholdMS(instance.ExtraConfig)
	inserted := 0
	skippedNotSlow := 0
	var lastEventTime time.Time
	for _, item := range samples {
		previous, existed := baseline.rows[item.SQLID+":"+item.PlanHash]
		execDelta, elapsedDelta := item.Executions, item.ElapsedUS
		if existed {
			// 累计值回退说明共享池老化或实例重启过，本轮按全量计，
			// 宁可多记一条也不要算出负数耗时。
			if item.Executions >= previous.ExecutionsTotal {
				execDelta = item.Executions - previous.ExecutionsTotal
			}
			if item.ElapsedUS >= previous.ElapsedUSTotal {
				elapsedDelta = item.ElapsedUS - previous.ElapsedUSTotal
			}
		}
		if execDelta <= 0 {
			continue
		}
		averageMS := int(elapsedDelta / execDelta / 1000)
		if averageMS < thresholdMS {
			skippedNotSlow++
			continue
		}

		sqlText := item.SQLText
		if len(sqlText) > 4000 {
			sqlText = sqlText[:4000]
		}
		fingerprint := normalizeSQLFingerprint(sqlText)
		digest := sha256.Sum256([]byte(fingerprint))
		eventTime := item.LastActive
		if eventTime.IsZero() {
			eventTime = sampleAt
		}

		entry := performanceModels.InsightPerformanceSlowLogEntries{
			InstanceID:     instanceID,
			DBType:         commonModels.EnumType(strings.TrimSpace(string(instance.DbType))),
			LogPath:        oracleSlowLogSource,
			FileInode:      oracleSlowLogKeyPrefix + item.SQLID,
			EventTime:      commonModels.LocalTime(eventTime),
			CollectTime:    commonModels.LocalTime(time.Now()),
			SchemaName:     item.SchemaName,
			QueryTimeMS:    averageMS,
			RowsExamined:   int(item.RowsProcessed),
			ThreadID:       item.Module,
			SQLText:        sqlText,
			SQLFingerprint: fingerprint,
			SQLDigest:      hex.EncodeToString(digest[:]),
			ParseStatus:    slowLogParseStatusParsed,
			RawText:        sqlText,
			SampleTime:     commonModels.LocalTime(sampleAt),
			ExecCount:      execDelta,
		}
		entry.EventKey = slowLogEventKey(oracleSlowLogKeyPrefix, instanceID, item.SQLID,
			item.PlanHash, sampleAt.Unix())

		tx := global.App.DB.Where("event_key = ?", entry.EventKey).FirstOrCreate(&entry)
		if tx.Error != nil {
			markCollectorFailureWithCode(collector,
				fmt.Sprintf("Oracle 慢 SQL 入库失败：%v", tx.Error),
				capabilityProbeFailed, failOraclePersistFailed)
			return
		}
		if tx.RowsAffected > 0 {
			inserted++
		}
		if eventTime.After(lastEventTime) {
			lastEventTime = eventTime
		}
	}

	// 基线必须在入库之后写：入库失败时基线不动，下一轮能重新差分，不会漏数据
	if err := upsertOracleSampleBaseline(instanceID, samples); err != nil {
		markCollectorFailureWithCode(collector,
			fmt.Sprintf("Oracle 采样基线写入失败：%v", err),
			capabilityProbeFailed, failOraclePersistFailed)
		return
	}

	updates := map[string]any{
		"file_size":      int64(len(samples)),
		"collect_status": slowLogCollectStatusIdle,
		"last_collect_message": fmt.Sprintf("采样成功：扫描 %d 条，新增 %d 条，未达阈值 %d 条（阈值 %dms）",
			len(samples), inserted, skippedNotSlow, thresholdMS),
		"last_success_at":     commonModels.LocalTime(time.Now()),
		"last_error":          "",
		"failure_code":        "",
		"last_notified_error": "",
		"source":              oracleSlowLogSource,
	}
	if !lastEventTime.IsZero() {
		updates["last_event_at"] = commonModels.LocalTime(lastEventTime)
	}
	_ = global.App.DB.Model(collector).Updates(updates).Error
}

// oracleSampleBaseline 上一次采样的累计值快照。
type oracleSampleBaseline struct {
	// since 是增量查询的下界；首次采样时为窗口起点
	since time.Time
	// rows 的 key 为 `sql_id:plan_hash_value`
	rows map[string]performanceModels.InsightPerformanceSlowLogSamples
}

// loadOracleSampleBaseline 读取某实例上一次采样的累计值快照。
//
// 注意只取最近一轮（MAX(sample_time) 那一批），而不是全部历史：
// 差分必须和"紧邻的上一轮"比，和历史任意一轮比都会得到错误的增量。
func loadOracleSampleBaseline(instanceID string) oracleSampleBaseline {
	baseline := oracleSampleBaseline{rows: map[string]performanceModels.InsightPerformanceSlowLogSamples{}}
	if global.App.DB == nil || strings.TrimSpace(instanceID) == "" {
		baseline.since = time.Now()
		return baseline
	}

	var latest time.Time
	var latestRow performanceModels.InsightPerformanceSlowLogSamples
	if err := global.App.DB.Where("instance_id = ?", instanceID).
		Order("sample_time desc").Take(&latestRow).Error; err == nil {
		latest = time.Time(latestRow.SampleTime)
	}

	var rows []performanceModels.InsightPerformanceSlowLogSamples
	query := global.App.DB.Where("instance_id = ?", instanceID)
	if !latest.IsZero() {
		query = query.Where("sample_time = ?", commonModels.LocalTime(latest))
	}
	if err := query.Find(&rows).Error; err != nil {
		baseline.since = time.Now()
		return baseline
	}

	for _, row := range rows {
		baseline.rows[row.SQLID+":"+row.PlanHashValue] = row
	}
	if latest.IsZero() {
		baseline.since = time.Now()
	} else {
		baseline.since = latest
	}
	return baseline
}

// upsertOracleSampleBaseline 写入本轮采样得到的累计值。
func upsertOracleSampleBaseline(instanceID string, items []oracleVSQLSample) error {
	if global.App.DB == nil || len(items) == 0 {
		return nil
	}
	now := commonModels.LocalTime(time.Now())
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		for _, item := range items {
			row := performanceModels.InsightPerformanceSlowLogSamples{
				InstanceID:         instanceID,
				SQLID:              item.SQLID,
				PlanHashValue:      item.PlanHash,
				ParsingSchemaName:  item.SchemaName,
				ExecutionsTotal:    item.Executions,
				ElapsedUSTotal:     item.ElapsedUS,
				CPUUSTotal:         item.CPUUS,
				BufferGetsTotal:    item.BufferGets,
				DiskReadsTotal:     item.DiskReads,
				RowsProcessedTotal: item.RowsProcessed,
				LastActiveTime:     commonModels.LocalTime(item.LastActive),
				SampleTime:         now,
			}
			row.Model = &commonModels.Model{}
			cond := performanceModels.InsightPerformanceSlowLogSamples{
				InstanceID: instanceID, SQLID: item.SQLID, PlanHashValue: item.PlanHash,
			}
			cond.Model = &commonModels.Model{}
			if err := tx.Where("instance_id = ? AND sql_id = ? AND plan_hash_value = ?",
				instanceID, item.SQLID, item.PlanHash).
				Assign(row).FirstOrCreate(&cond).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------- 事件身份 ----------

// slowLogEventKey 生成跨引擎统一的事件幂等键。
//
// 文件类：inode + 起始偏移，天然唯一且可复现（重放同一段日志得到同一个键）；
// 采样类：sql_id + plan_hash + 采样时刻，保证同一轮同一 SQL 只写一条。
// 长度控制在 160 字符内以便建唯一索引。
func slowLogEventKey(parts ...any) string {
	raw := make([]string, 0, len(parts))
	for _, part := range parts {
		raw = append(raw, fmt.Sprintf("%v", part))
	}
	key := strings.Join(raw, "|")
	if len(key) > 150 {
		sum := sha256.Sum256([]byte(key))
		key = key[:100] + "|" + hex.EncodeToString(sum[:])[:40]
	}
	return key
}

// oracleSlowLogKeyPrefix 是 Oracle 采样事件在 file_inode 列使用的前缀。
// 保留该列可读性：一眼能看出这条记录来自采样而非文件。
const oracleSlowLogKeyPrefix = "VSQL:"
