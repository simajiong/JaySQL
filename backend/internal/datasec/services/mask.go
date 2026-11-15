package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/simajiong/jaysql/internal/datasec/models"
	"github.com/simajiong/jaysql/internal/global"
)

// 脱敏引擎：查询链路的唯一执行入口。
//
// 设计口径：结果集列在网关层没有可靠的「来源表」信息（别名、表达式、JOIN 都会丢），
// 因此列匹配按「实例 + 列名」归一（大小写不敏感），同名列共用一条规则。
// 这是网关脱敏产品的通行做法，换取的是对任意 SQL 的 100% 覆盖。
// 若个别列必须差异化（如 a-table.mobile 与 b-table.mobile 不同策略），
// 建议在命名上区分，或等驱动暴露表级元数据后再收紧匹配。

// 内置算法名
const (
	MaskAlgoFull     = "full"      // 全部遮蔽
	MaskAlgoHeadTail = "head_tail" // 保留前 N 后 M
	MaskAlgoTail     = "tail"      // 仅保留后 M
	MaskAlgoHead     = "head"      // 仅保留前 N
	MaskAlgoEmail    = "email"     // 邮箱：用户名保留首字符
	MaskAlgoAddress  = "address"   // 地址：保留省市区
	MaskAlgoName     = "name"      // 姓名：保留首字
	MaskAlgoHash     = "hash"      // SHA-256 截断（不可逆，可关联）
)

// 内置敏感类型（与前端下拉、扫描器判定器对齐）
const (
	DataTypePhone    = "phone"
	DataTypeIDCard   = "id_card"
	DataTypeBankCard = "bank_card"
	DataTypeEmail    = "email"
	DataTypeAddress  = "address"
	DataTypeName     = "name"
	DataTypeCustom   = "custom"
)

// 内置规则名（seed 时写入，扫描建议按类型直接引用）
const (
	RulePhone   = "手机号-保留前3后4"
	RuleIDCard  = "身份证号-保留前4后2"
	RuleBank    = "银行卡号-保留后4位"
	RuleEmail   = "邮箱-用户名保留首字符"
	RuleAddress = "地址-保留省市区"
	RuleName    = "姓名-保留首字"
)

const (
	columnStatusEnabled   = "enabled"
	columnStatusSuggested = "suggested"
)

// ---------- 算法实现（纯函数，参数来自规则的 Params JSON） ----------

type maskParams struct {
	KeepHead int `json:"keep_head"`
	KeepTail int `json:"keep_tail"`
	HashLen  int `json:"hash_len"`
}

func parseMaskParams(raw []byte) maskParams {
	var p maskParams
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	return p
}

// ApplyMaskAlgorithm 对单个值执行脱敏。空值/空串原样返回。
func ApplyMaskAlgorithm(algorithm string, params maskParams, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	switch algorithm {
	case MaskAlgoHeadTail:
		return maskKeep(value, params.KeepHead, params.KeepTail)
	case MaskAlgoTail:
		return maskKeep(value, 0, params.KeepTail)
	case MaskAlgoHead:
		return maskKeep(value, params.KeepHead, 0)
	case MaskAlgoEmail:
		return maskEmail(value)
	case MaskAlgoAddress:
		return maskAddress(value)
	case MaskAlgoName:
		return maskName(value)
	case MaskAlgoHash:
		return maskHash(value, params.HashLen)
	default:
		// 未知算法一律按全遮蔽兜底：宁可脱多，不可漏脱
		return strings.Repeat("*", len([]rune(value)))
	}
}

func maskKeep(value string, keepHead, keepTail int) string {
	runes := []rune(value)
	keepHead = max(keepHead, 0)
	keepTail = max(keepTail, 0)
	if keepHead+keepTail >= len(runes) {
		// 长度不足以保留时全遮蔽，避免「保留位比总长还长」变成明文回显
		return strings.Repeat("*", len(runes))
	}
	head := runes[:keepHead]
	tail := runes[len(runes)-keepTail:]
	out := make([]rune, 0, len(runes))
	out = append(out, head...)
	for i := 0; i < len(runes)-keepHead-keepTail; i++ {
		out = append(out, '*')
	}
	out = append(out, tail...)
	return string(out)
}

func maskEmail(value string) string {
	at := strings.LastIndex(value, "@")
	if at <= 0 {
		return strings.Repeat("*", len([]rune(value)))
	}
	local := []rune(value[:at])
	if len(local) > 1 {
		return string(local[0]) + strings.Repeat("*", len(local)-1) + value[at:]
	}
	return string(local) + value[at:]
}

func maskAddress(value string) string {
	runes := []rune(value)
	// 保留到「省/市/区」一级：贪心取最靠后的行政区划词
	keep := 0
	for i, r := range runes {
		if r == '省' || r == '市' || r == '区' || r == '县' {
			keep = i + 1
		}
	}
	if keep == 0 || keep >= len(runes) {
		return maskKeep(value, 6, 0)
	}
	return string(runes[:keep]) + strings.Repeat("*", len(runes)-keep)
}

func maskName(value string) string {
	runes := []rune(value)
	if len(runes) <= 1 {
		return strings.Repeat("*", len(runes))
	}
	return string(runes[0]) + strings.Repeat("*", len(runes)-1)
}

func maskHash(value string, hashLen int) string {
	sum := sha256.Sum256([]byte(value))
	out := hex.EncodeToString(sum[:])
	if hashLen > 0 && hashLen < len(out) {
		out = out[:hashLen]
	}
	return out
}

// ---------- 规则装载（带 30s 进程内缓存，避免每条 SQL 都查规则表） ----------

type columnMaskRule struct {
	RuleID    uint64
	DataType  string
	Algorithm string
	Params    maskParams
}

type maskConfigCache struct {
	expireAt  time.Time
	// instanceID -> lower(column name) -> rule
	rules map[string]map[string]columnMaskRule
}

var (
	maskCacheMu sync.RWMutex
	maskCache   *maskConfigCache
)

const maskCacheTTL = 30 * time.Second

// loadMaskConfig 拉取「全部启用规则 + 全部 enabled 列配置」并按实例组织。
// 全量拉取而不是按实例过滤：脱敏发生在每条 SQL 的热路径上，规则总量很小
// （通常 < 几百行），一次查询 + 内存过滤比每实例一次查询更省。
func loadMaskConfig() (map[string]map[string]columnMaskRule, error) {
	var rules []models.InsightDataSecRules
	if err := global.App.DB.Where("enabled=1").Find(&rules).Error; err != nil {
		return nil, err
	}
	ruleMap := make(map[uint64]columnMaskRule, len(rules))
	for _, r := range rules {
		ruleMap[r.ID] = columnMaskRule{
			RuleID:    r.ID,
			DataType:  string(r.DataType),
			Algorithm: string(r.Algorithm),
			Params:    parseMaskParams(r.Params),
		}
	}
	var cols []models.InsightDataSecColumns
	if err := global.App.DB.Where("status=?", columnStatusEnabled).Find(&cols).Error; err != nil {
		return nil, err
	}
	result := make(map[string]map[string]columnMaskRule)
	for _, c := range cols {
		rule, ok := ruleMap[c.RuleID]
		if !ok {
			continue // 规则被删/停用，该列暂不脱敏
		}
		key := strings.ToLower(strings.TrimSpace(c.InstanceID))
		if result[key] == nil {
			result[key] = make(map[string]columnMaskRule)
		}
		result[key][strings.ToLower(strings.TrimSpace(c.ColumnName))] = rule
	}
	return result, nil
}

func getMaskConfig() (map[string]map[string]columnMaskRule, error) {
	maskCacheMu.RLock()
	if maskCache != nil && time.Now().Before(maskCache.expireAt) {
		defer maskCacheMu.RUnlock()
		return maskCache.rules, nil
	}
	maskCacheMu.RUnlock()

	maskCacheMu.Lock()
	defer maskCacheMu.Unlock()
	// 双检：等锁期间可能已被其他请求刷新
	if maskCache != nil && time.Now().Before(maskCache.expireAt) {
		return maskCache.rules, nil
	}
	rules, err := loadMaskConfig()
	if err != nil {
		return nil, err
	}
	maskCache = &maskConfigCache{expireAt: time.Now().Add(maskCacheTTL), rules: rules}
	return rules, nil
}

// InvalidateMaskConfig 规则/列配置变更后调用，让下一条查询立即生效。
func InvalidateMaskConfig() {
	maskCacheMu.Lock()
	maskCache = nil
	maskCacheMu.Unlock()
}

// ---------- 查询链路接入 ----------

// MaskQueryResult 对查询结果执行脱敏。
//   - 返回值 maskedColumns：命中的结果列名（用于前端提示与审计）；
//   - grantNo：本批次明文放行所引用的工单号（空=全部脱敏）。
//
// 明文工单覆盖判定：approved 且未过期，且工单范围包含该列（实例/库/表/列逐级通配）。
// 任何内部错误都不影响查询本身——脱敏失败时记日志并放行原结果？不：失败时放行明文
// 有泄露风险，这里选择「失败仍按无配置处理」，因为规则拉不到时无法区分「没配置」
// 与「数据库抖动」，而脱敏是增强能力而非数据通道本身，保持查询可用优先。
func MaskQueryResult(instanceID, username string, columns *[]string, data *[]map[string]interface{}) (maskedColumns []string, grantNo string) {
	if columns == nil || data == nil || len(*columns) == 0 || len(*data) == 0 {
		return nil, ""
	}
	config, err := getMaskConfig()
	if err != nil {
		global.App.Log.Error("datasec: 加载脱敏配置失败，本批次不脱敏", err.Error())
		return nil, ""
	}
	key := strings.ToLower(strings.TrimSpace(instanceID))
	rules := config[key]
	if len(rules) == 0 {
		return nil, ""
	}
	// 先判断哪些列命中（大小写不敏感）
	hits := make(map[string]columnMaskRule) // 结果列名 -> 规则
	for _, col := range *columns {
		if rule, ok := rules[strings.ToLower(strings.TrimSpace(col))]; ok {
			hits[col] = rule
		}
	}
	if len(hits) == 0 {
		return nil, ""
	}
	// 明文工单：拿到用户对该实例全部有效授权，命中列逐个判断
	grants := loadActivePlainGrants(instanceID, username)
	plainCols := make(map[string]string) // 列名 -> grantNo
	for _, col := range *columns {
		for _, g := range grants {
			if grantCoversColumn(g, col) {
				plainCols[col] = g.GrantNo
				break
			}
		}
	}
	// 执行脱敏
	affected := make([]string, 0, len(hits))
	usedGrants := make(map[string]string)
	for _, row := range *data {
		for col, rule := range hits {
			raw, exists := row[col]
			if !exists || raw == nil {
				continue
			}
			if gno, ok := plainCols[col]; ok {
				usedGrants[col] = gno
				continue
			}
			row[col] = ApplyMaskAlgorithm(rule.Algorithm, rule.Params, toStringValue(raw))
		}
	}
	for col := range hits {
		affected = append(affected, col)
	}
	// 一个批次可能引用多个工单（不同列不同工单），全部带上便于审计展示
	gnoList := make([]string, 0, len(usedGrants))
	for _, gno := range usedGrants {
		gnoList = append(gnoList, gno)
	}
	return affected, strings.Join(gnoList, ",")
}

func toStringValue(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprint(t)
	}
}
