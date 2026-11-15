package services

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/pkg/secret"
)

// ============================================================================
// PostgreSQL 慢日志采集能力探测
//
// 与 MySQL 的根本差异（决定了探测口径完全不同）：
//   - **PG 没有"慢日志文件"这一概念**。慢 SQL 只是服务端日志里的一行
//     `LOG:  duration: 1234.567 ms  statement: ...`，
//     它和错误日志、连接日志写在同一个文件里。
//   - 因此"慢日志是否开启"这件事，在 PG 上要拆成三个独立前提：
//       ① 日志进了文件吗？        → logging_collector = on
//       ② 多慢才算慢？            → log_min_duration_statement > 0
//       ③ 日志行里有用户和库吗？  → log_line_prefix 含 %u 和 %d
//     三条任一不满足，采集结果都是"数据不完整或采不到"，但**修复动作各不相同**。
//     如果只给一句"采集失败"，用户根本不知道该动哪一项——这就是要结构化失败码的原因。
//
// 关于 pg_stat_statements：它给的是 SQL 维度的**累计聚合**（调用次数/总耗时/共享块命中），
// 适合做 Top-N 排行，但拿不到"某一次执行的具体耗时"和客户端信息，因此**不作为一期数据源**，
// 只在前提条件里作为"可选增强"列出。
// ============================================================================

// pgSettingsQuery 一次性取回判定所需的全部参数，避免连开多次连接。
//
// 为什么用 pg_settings 而不是逐条 SHOW：探测是每 10 分钟对所有实例跑一遍的后台动作，
// RTT 成本会被实例数放大；而且 pg_settings 的 setting 列统一是文本，判定逻辑更一致。
const pgSettingsQuery = `
SELECT name, setting
FROM pg_settings
WHERE name IN (
    'server_version_num',
    'logging_collector',
    'log_min_duration_statement',
    'log_line_prefix',
    'log_destination',
    'data_directory',
    'log_directory'
)`

// pgMinSupportedVersionNum PG 10 对应的 server_version_num。
// 低于 10 没有 pg_current_logfile()，平台无法可靠定位当前活跃日志文件。
const pgMinSupportedVersionNum = 100000

// postgresProbeResult PG 探测的归一化结果。
type postgresProbeResult struct {
	VersionNum     int
	ServerVersion  string
	CollectorOn    bool
	DurationMS     int
	LinePrefix     string
	HasUserDB      bool
	Destinations   []string
	DataDir        string
	LogDir         string
	CurrentLogFile string
	FailureCode    string
	FailureDetail  string
}

// probePostgresSlowLogCapability 探测 PG 实例的慢日志可采集性。
func probePostgresSlowLogCapability(instance *commonModels.InsightInstances) *slowLogCapability {
	instanceID := strings.TrimSpace(instance.InstanceID)
	user := strings.TrimSpace(instance.User)

	password := strings.TrimSpace(instance.Password)
	if password != "" {
		if plain, err := secret.Decrypt(password); err == nil && strings.TrimSpace(plain) != "" {
			password = strings.TrimSpace(plain)
		}
	}
	if user == "" || password == "" {
		return pgCapability(instanceID, &postgresProbeResult{
			FailureCode:   failPGNoCredential,
			FailureDetail: "未配置可用的实例账号或密码，无法探测服务端日志配置",
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), slowLogProbeTimeout)
	defer cancel()

	db := dao.PostgresDB{
		User:     user,
		Password: password,
		Host:     strings.TrimSpace(instance.Hostname),
		Port:     instance.Port,
		Database: pgConnectDatabase(instance),
		Ctx:      ctx,
	}

	_, rows, err := db.Query(pgSettingsQuery)
	if err != nil {
		return pgCapability(instanceID, &postgresProbeResult{
			FailureCode:   failPGConnectFailed,
			FailureDetail: fmt.Sprintf("连接实例查询日志参数失败：%v", err),
		})
	}

	settings := parsePGSettings(rows)

	// 版本：低于 10 直接判定不支持，避免后续所有判定的口径都不可靠
	versionNum := 0
	if raw, ok := settings["server_version_num"]; ok {
		versionNum, _ = strconv.Atoi(raw)
	}
	if versionNum > 0 && versionNum < pgMinSupportedVersionNum {
		return pgCapability(instanceID, &postgresProbeResult{
			VersionNum:    versionNum,
			FailureCode:   failPGVersionUnsupported,
			FailureDetail: fmt.Sprintf("PostgreSQL 版本过低（server_version_num=%d），低于 10 无 pg_current_logfile()，无法自动定位当前日志文件", versionNum),
		})
	}

	// 当前活跃日志文件：需要 pg_monitor 角色，拿不到就明确归因到权限
	currentLogFile := ""
	if _, rows2, err2 := db.Query("SELECT pg_current_logfile()"); err2 == nil && rows2 != nil && len(*rows2) > 0 {
		for _, raw := range (*rows2)[0] {
			if text := strings.TrimSpace(fmt.Sprintf("%v", raw)); text != "" && text != "<nil>" {
				currentLogFile = text
			}
		}
	} else if err2 != nil {
		low := strings.ToLower(err2.Error())
		if strings.Contains(low, "permission denied") || strings.Contains(low, "must be superuser") ||
			strings.Contains(low, "executing function") {
			return pgCapability(instanceID, &postgresProbeResult{
				VersionNum:    versionNum,
				FailureCode:   failPGNoMonitorRole,
				FailureDetail: "账号缺少 pg_monitor 角色，无法读取当前日志文件位置",
			})
		}
	}

	durationMS := 0
	if raw, ok := settings["log_min_duration_statement"]; ok {
		durationMS, _ = strconv.Atoi(raw)
	}

	prefix := settings["log_line_prefix"]
	hasUserDB := strings.Contains(prefix, "%u") && strings.Contains(prefix, "%d")

	result := &postgresProbeResult{
		VersionNum:     versionNum,
		ServerVersion:  pgVersionText(versionNum),
		CollectorOn:    strings.EqualFold(settings["logging_collector"], "on"),
		DurationMS:     durationMS,
		LinePrefix:     prefix,
		HasUserDB:      hasUserDB,
		Destinations:   splitPGDestinations(settings["log_destination"]),
		DataDir:        strings.TrimSpace(settings["data_directory"]),
		LogDir:         strings.TrimSpace(settings["log_directory"]),
		CurrentLogFile: currentLogFile,
	}

	// 按「后果严重度」排序判定：先判连不上、再判没开、再判没文件、最后判信息不全。
	switch {
	case !result.CollectorOn:
		result.FailureCode = failPGCollectorOff
		result.FailureDetail = "logging_collector 为 off，日志只输出到 stderr（被 systemd/journald 接管），平台无法按文件采集"
	case result.DurationMS < 0:
		result.FailureCode = failPGMinDurationDisabled
		result.FailureDetail = "log_min_duration_statement 为 -1，服务端不记录任何慢SQL"
	case result.DurationMS == 0:
		result.FailureCode = failPGMinDurationZero
		result.FailureDetail = "log_min_duration_statement 为 0，会记录全部语句，日志量暴涨且无分析价值"
	case !result.HasUserDB:
		result.FailureCode = failPGPrefixMissingUserDB
		result.FailureDetail = fmt.Sprintf("log_line_prefix 缺少 %%u 或 %%d（当前：%s），采上来的记录没有执行用户和库名", prefix)
	}

	return pgCapability(instanceID, result)
}

// pgCapability 把 PG 探测结果映射成统一的采集能力结构。
//
// FieldCapable / SlowLogEnabled 沿用 MySQL 那套语义：
// 只有「日志进了文件」且「慢SQL阈值合理」才算可采集，
// 这样采集门控 slowLogCollectable() 与五态展示逻辑一行都不用改。
func pgCapability(instanceID string, r *postgresProbeResult) *slowLogCapability {
	capability := &slowLogCapability{
		InstanceID:      instanceID,
		ProbeStatus:     probeStatusOK,
		ServerVersion:   r.ServerVersion,
		SlowLogEnabled:  r.CollectorOn && r.DurationMS > 0,
		LogOutputMode:   strings.Join(r.Destinations, ","),
		FileCapable:     r.CollectorOn,
		TableCapable:    false,
		RawLogFile:      r.CurrentLogFile,
		Datadir:         r.DataDir,
		ResolvedPath:    resolvePGSlowLogPath(r),
		LongQueryTimeMS: r.DurationMS,
		ProbedAt:        time.Now(),
		FailureCode:     r.FailureCode,
	}
	capability.ProbeError = r.FailureDetail
	if strings.TrimSpace(r.FailureCode) != "" {
		capability.ProbeStatus = probeStatusUnsupported
		capability.State = capabilityUnsupported
		return capability
	}
	capability.State = decideSlowLogCapabilityState(capability)
	return capability
}

// resolvePGSlowLogPath 按 PG 的规则定出日志文件绝对路径。
//
// 优先级：pg_current_logfile()（最准，跟随轮转）> log_directory 拼 log_filename。
//
// ⚠️ 这里踩过一个坑，值得写清楚：pg_current_logfile() 返回的是
// 「log_directory + 文件名」的路径（官方文档：路径包含 log_directory 目录和单独的日志文件名），
// 但当 log_directory 是相对路径（默认 'log'）时，返回的仍是**相对路径**，
// 基准是 data_directory —— 例如 `log/postgresql-2026-10-23_000000.log`。
//
// 早前版本把它直接当绝对路径落库，SSH 端 `test -f log/postgresql-xxx.log`
// 在登录用户的 home 下自然不存在，采集报 FILE_MISSING，
// 前端归一成 LOG_FILE_UNREADABLE「慢日志文件缺失或不可读」——
// 而服务端其实天天在写这个文件。这就是必须用 data_directory 展开的原因。
func resolvePGSlowLogPath(r *postgresProbeResult) string {
	if r == nil {
		return ""
	}
	return joinPGLogPath(r.DataDir, r.LogDir, r.CurrentLogFile)
}

// joinPGLogPath 把 (data_directory, log_directory, pg_current_logfile() 返回值) 拼成绝对路径。
//
// 要兼容三种返回形态：
//   - 绝对路径（log_directory 配成绝对路径，或托管 PG）→ 直接用；
//   - `log/postgresql-xxx.log`（默认配置，已含 log_directory 前缀）→ 只补 data_directory，
//     否则会拼出 `.../data/log/log/xxx.log`；
//   - `postgresql-xxx.log`（只给文件名）→ 补 log_directory 再补 data_directory。
//
// 拿不到文件名时返回空串，由采集流程报「未能定位当前日志文件」——
// 只拿到目录时不能当文件用：`test -f <目录>` 必然失败，又会误报「文件缺失」。
func joinPGLogPath(dataDir, logDir, currentLogFile string) string {
	dataDir = strings.TrimSpace(dataDir)
	logDir = strings.TrimSpace(logDir)
	currentLogFile = strings.TrimSpace(currentLogFile)

	if currentLogFile == "" {
		return ""
	}
	if filepath.IsAbs(currentLogFile) {
		return filepath.Clean(currentLogFile)
	}

	// log_directory 自身也可能是相对路径（默认 'log'），相对的是 data_directory
	dir := logDir
	if dir != "" && !filepath.IsAbs(dir) && dataDir != "" {
		dir = filepath.Join(dataDir, dir)
	}

	// 返回值已带 log_directory 前缀 → 只需补 data_directory
	cleanLogDir := filepath.Clean(logDir)
	hasLogDirPrefix := logDir != "" &&
		(cleanLogDir == currentLogFile || strings.HasPrefix(currentLogFile, cleanLogDir+string(filepath.Separator)))
	if hasLogDirPrefix {
		if dataDir == "" {
			return filepath.Clean(currentLogFile)
		}
		return filepath.Clean(filepath.Join(dataDir, currentLogFile))
	}

	if dir == "" {
		return filepath.Clean(currentLogFile)
	}
	return filepath.Clean(filepath.Join(dir, currentLogFile))
}

// parsePGSettings 把 pg_settings 的结果行转成 name -> setting。
// 列名为 PG 返回的原始列名（小写），这里做大小写不敏感匹配，
// 避免不同 pgx 版本返回的列名大小写不一致导致取不到值。
func parsePGSettings(rows *[]map[string]interface{}) map[string]string {
	out := map[string]string{}
	if rows == nil {
		return out
	}
	for _, row := range *rows {
		name, setting := "", ""
		for column, raw := range row {
			switch strings.ToUpper(strings.TrimSpace(column)) {
			case "NAME":
				name = stringifyVariableValue(raw)
			case "SETTING":
				setting = stringifyVariableValue(raw)
			}
		}
		if name == "" {
			continue
		}
		out[strings.ToLower(name)] = setting
	}
	return out
}

// pgVersionText 把 server_version_num（如 100023 / 160002）翻成可读版本号。
// PG 官方约定：9.6 及以前是 90600 形态，10 起是 100000 形态。
func pgVersionText(num int) string {
	if num <= 0 {
		return ""
	}
	major := num / 10000
	minor := (num % 10000) / 100
	patch := num % 100
	if major < 10 {
		return fmt.Sprintf("%d.%d.%d", major, minor, patch)
	}
	return fmt.Sprintf("%d.%d", major, patch)
}

// splitPGDestinations 拆分 log_destination（如 'stderr,csvlog'）。
func splitPGDestinations(raw string) []string {
	parts := strings.Split(strings.TrimSpace(raw), ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if text := strings.TrimSpace(strings.ToLower(part)); text != "" {
			out = append(out, text)
		}
	}
	return out
}
