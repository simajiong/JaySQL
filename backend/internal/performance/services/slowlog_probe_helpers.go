package services

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/pkg/secret"
	"gorm.io/datatypes"
)

// ============================================================================
// 多引擎探测的公共读取工具
//
// 为什么要单独一组取值函数：MySQL / PG / Oracle 三个驱动返回的列名大小写规则不同
//   - MySQL / pgx：返回建表/查询时的原始列名（通常小写）
//   - Oracle：不带双引号的别名被折叠成大写，带双引号才保留原样
//
// 探测侧统一走「大小写不敏感」取值，这样即使以后有人改了别名写法（去掉引号），
// 也不会再出现按小写 key 取不到值、把字符串 "<nil>" 写进库里的问题。
// ============================================================================

// pgText 从结果行里取字符串，列名大小写不敏感；取不到返回空串。
func pgText(row map[string]any, name string) string {
	for column, raw := range row {
		if !strings.EqualFold(strings.TrimSpace(column), name) {
			continue
		}
		text := strings.TrimSpace(fmt.Sprintf("%v", raw))
		if text == "<nil>" || text == "<null>" || text == "%!v(PANIC=...)" {
			return ""
		}
		return text
	}
	return ""
}

// pgInt 从结果行里取整数；解析失败返回 0。
func pgInt(row map[string]any, name string) int64 {
	text := pgText(row, name)
	if text == "" {
		return 0
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0
	}
	return int64(value)
}

// pgTime 解析 Oracle / PG 的时间文本。
// Oracle 的 DATE 经驱动返回可能是 time.Time 或 "2026-10-23 12:03:41" 文本，两者都要能处理。
func pgTime(row map[string]any, name string) time.Time {
	for column, raw := range row {
		if !strings.EqualFold(strings.TrimSpace(column), name) {
			continue
		}
		if typed, ok := raw.(time.Time); ok {
			return typed
		}
		break
	}
	text := pgText(row, name)
	if text == "" {
		return time.Time{}
	}
	layouts := []string{
		"2006-01-02 15:04:05.999999999 -07:00 MST",
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// parsePGTimestamp 解析 PG 日志行里的时间戳文本。
//
// 之所以要自己排 layout 顺序：log_line_prefix 的 `%m` 是否带时区取决于服务端
// log_timezone，带毫秒与否取决于 `%m` 的实现版本。
//
// 必须用 ParseInLocation（按本机时区兜底），不能裸用 time.Parse：
//   - 文本不带时区时，time.Parse 会按 UTC 解释——PG 的 %m 输出的是
//     log_timezone 的墙上时钟，生产上与采集器同时区，当 UTC 解析后
//     入库再转本地就整体 +8h，事件时间全部落在「未来」，页面 24h
//     统计窗口（event_time <= now）一条都圈不进来，慢SQL数量恒为 0。
//   - 文本带时区缩写（CST/UTC 等）时，ParseInLocation 会优先用本机
//     时区解释该缩写：Asia/Shanghai 下 CST 正确解析为 +8，UTC 正确
//     解析为 0 偏移。个别时区把缩写解析不出来时（Go 对未知缩写记 0
//     偏移），误差与不带时区的兜底路径一致，可接受。
//
// 全部 layout 失败时返回零值，由调用方回退到当前时间——慢日志的时间精度到秒就够用。
func parsePGTimestamp(text string) time.Time {
	text = strings.TrimSpace(text)
	if text == "" {
		return time.Time{}
	}
	for _, layout := range pgTimestampLayouts {
		if parsed, err := time.ParseInLocation(layout, text, time.Local); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// oracleFirstNonEmpty 取第一个非空值，用于版本等值回退。
func oracleFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// instancePlainPassword 解密实例密码；解密失败时返回空串（由调用方归因）。
func instancePlainPassword(instance *commonModels.InsightInstances) string {
	if instance == nil {
		return ""
	}
	raw := strings.TrimSpace(instance.Password)
	if raw == "" {
		return ""
	}
	plain, err := secret.Decrypt(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(plain)
}

// pgConnectDatabase PG 系（PostgreSQL / Kingbase）连接库名。
//
// 唯一权威是 extra_config.database —— 与 SQL 工作台、监控指标、画像采集同源。
// 2026-11-11 修复：此前误用 organization_key（"搜索路径"字段，正常为空），
// 回退到 "postgres" 后，金仓这类没有 postgres 库的发行版（KES 默认 test）
// 全部连不上 —— 慢日志探测、参数管理、日志路径查询全部失效。
// organization_key 保留为第二优先级兼容历史录入，最后才回退 postgres。
func pgConnectDatabase(instance *commonModels.InsightInstances) string {
	if db, ok := stringFromExtraConfig(instance.ExtraConfig, "database"); ok {
		return db
	}
	if org := strings.TrimSpace(instance.OrganizationKey); org != "" {
		return org
	}
	return "postgres"
}

// stringFromExtraConfig 从 extra_config JSON 里取字符串配置。
func stringFromExtraConfig(extra datatypes.JSON, key string) (string, bool) {
	if len(extra) == 0 {
		return "", false
	}
	var cfg map[string]any
	if err := json.Unmarshal(extra, &cfg); err != nil {
		return "", false
	}
	raw, ok := cfg[key]
	if !ok || raw == nil {
		return "", false
	}
	switch typed := raw.(type) {
	case string:
		return strings.TrimSpace(typed), strings.TrimSpace(typed) != ""
	case float64:
		value := strconv.FormatFloat(typed, 'f', -1, 64)
		return value, true
	case bool:
		value := strconv.FormatBool(typed)
		return value, true
	default:
		text := strings.TrimSpace(fmt.Sprintf("%v", raw))
		return text, text != ""
	}
}

// boolFromExtraConfig 从 extra_config 里取布尔配置；缺失返回 fallback。
func boolFromExtraConfig(extra datatypes.JSON, key string, fallback bool) bool {
	if len(extra) == 0 {
		return fallback
	}
	var cfg map[string]any
	if err := json.Unmarshal(extra, &cfg); err != nil {
		return fallback
	}
	raw, ok := cfg[key]
	if !ok || raw == nil {
		return fallback
	}
	switch typed := raw.(type) {
	case bool:
		return typed
	case string:
		text := strings.TrimSpace(strings.ToLower(typed))
		if text == "" {
			return fallback
		}
		return text != "false" && text != "0" && text != "off" && text != "no"
	case float64:
		return int(typed) != 0
	}
	return fallback
}

// oracleStatsLevelsAll 探测默认值占位：未查到 statistics_level 时不判定为 BASIC。
const oracleStatsLevelsAll = "TYPICAL"

// oracleFirstSampleWindow 首次采样回溯窗口：Oracle 的 V$SQL 是累计视图，
// 首次无法确定基线，取最近一段时间的活跃 SQL 作为起点。
const oracleFirstSampleWindow = 10 * time.Minute

// oracleTimeLiteral 生成 Oracle 时间字面量。
func oracleTimeLiteral(t time.Time) string {
	return "TO_DATE('" + t.Format("2006-01-02 15:04:05") + "', 'YYYY-MM-DD HH24:MI:SS')"
}

// serviceNameFromExtra 从 extra_config 取 service_name。
func serviceNameFromExtra(extra datatypes.JSON) string {
	value, _ := stringFromExtraConfig(extra, "service_name")
	return value
}
