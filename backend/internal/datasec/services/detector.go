package services

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// 敏感列判定器：纯函数，输入样本值集合，输出判定结果。
// 三级判定中的第 2 级（抽样内容校验）在这里实现：
//   - 强校验位类（身份证 GB 11643、银行卡 Luhn）做真验算，杜绝纯数字误报；
//   - 无校验位的类型（地址、姓名）只做加权建议，置信度不超过 89，强制人工确认。

const (
	scanHitThreshold = 0.90 // 抽样命中率达标线
	// 建议级置信度上限：不到「自动建议」线，必须人工确认
	suggestionConfidenceCap = 89
	// 列名未指向该类型时，内容部分命中达到该比例才敢下建议（随机数字通过校验位的概率约 10%）
	partialMatchFloor = 0.30
)

type scanVerdict struct {
	DataType   string
	Confidence int
	SampleHit  string
}

// checksumType 强校验类类型的判定规则（内容校验函数 + 列名/注释特征词）。
type checksumType struct {
	dataType    string
	match       func(string) bool
	nameKeys    string
	commentKeys string
}

// 顺序即优先级：内容判定阶段任一类型达标即返回；元数据兜底阶段按同一顺序取第一个命中。
// 列名特征词刻意避开三类宽词，它们会把无关列拉进建议级噪音：
//   - mp / tel 这类短词：误伤 timestamp、hotel
//   - 裸 bank：银行的「名称/分行/logo」都不是卡号，要求 bank_card|bank_no 这类卡号语义
var checksumTypes = []checksumType{
	{DataTypePhone, phoneMatcher, "mobile|phone|msisdn|tel_no|telephone", "手机|电话"},
	{DataTypeIDCard, idCardMatcher, "id_card|idcard|identity|sfz|id_no", "身份证|证件"},
	{DataTypeBankCard, bankCardMatcher, "bank_card|bankcard|bank_no|card_no|cardnum|card_number", "银行卡|卡号"},
	{DataTypeEmail, emailMatcher, "email|mail", "邮箱|邮件"},
}

// detectSensitiveType 对一列的样本值做类型判定。返回 nil 表示未命中任何类型。
//
// 两阶段判定，避免「列名猜错了类型」压过「内容真实匹配」：
//  1. 内容判定：4 个强校验类型逐个验算，命中率 ≥ 90% 才判定为该类型（置信度 90+）；
//  2. 元数据兜底：内容全不达标但列名/注释特征明确 → 降级为「建议级」（≤89，强制人工确认）。
//
// 第 2 阶段不能省：测试库随手编的号、生产库已脱敏的号（110101********1234）校验位天然不成立，
// 只认内容会让「列名明明叫 id_card」的列完全扫不出来，用户无从判断是漏扫还是没配规则。
func detectSensitiveType(columnName, columnComment string, samples []string) *scanVerdict {
	nameHint := strings.ToLower(columnName)
	commentHint := strings.ToLower(columnComment)

	// 第 1 阶段：内容为准
	for _, ct := range checksumTypes {
		metaHit := hasMetaHint(nameHint, commentHint, ct.nameKeys, ct.commentKeys)
		if v := detectByChecksum(samples, ct.match, ct.dataType, metaHit); v != nil {
			return v
		}
	}
	// 第 2 阶段：内容都没到自动建议线，退化为元数据特征建议。
	// 若内容对另一类型的部分命中明显（≥30%），说明列名猜错了——列名可以是 phone 而库里存的是身份证，
	// 此时按内容类型给建议；纯随机数字偶然通过 Luhn/校验位的概率约 10%，30% 门槛能挡住这类误报。
	metaType := firstMetaHintType(nameHint, commentHint)
	contentType, contentRate := bestPartialMatch(samples)
	switch {
	case contentType != nil && contentType != metaType && contentRate >= partialMatchFloor:
		return metaHintFallback(samples, contentType.match, contentType.dataType, true)
	case metaType != nil:
		return metaHintFallback(samples, metaType.match, metaType.dataType, false)
	}

	// 建议类：地址 / 姓名，无强校验位，只出建议
	if isAddressColumn(nameHint, commentHint) && hasNonEmptySamples(samples) {
		return &scanVerdict{DataType: DataTypeAddress, Confidence: 72, SampleHit: "列名/注释特征 + 样本含行政区划词，需人工确认"}
	}
	if isNameColumn(nameHint, commentHint) && hasNonEmptySamples(samples) {
		return &scanVerdict{DataType: DataTypeName, Confidence: 70, SampleHit: "列名/注释特征 + 样本为短中文串，需人工确认"}
	}
	return nil
}

// firstMetaHintType 按优先级返回第一个被列名/注释命中的类型。
func firstMetaHintType(nameHint, commentHint string) *checksumType {
	for i := range checksumTypes {
		ct := &checksumTypes[i]
		if hasMetaHint(nameHint, commentHint, ct.nameKeys, ct.commentKeys) {
			return ct
		}
	}
	return nil
}

// bestPartialMatch 返回内容命中率最高的类型（只看有命中的），用于纠正列名猜错的类型。
func bestPartialMatch(samples []string) (*checksumType, float64) {
	var best *checksumType
	bestRate := 0.0
	for i := range checksumTypes {
		ct := &checksumTypes[i]
		total, hit := countMatches(samples, ct.match)
		if total == 0 || hit == 0 {
			continue
		}
		if rate := float64(hit) / float64(total); rate > bestRate {
			bestRate, best = rate, ct
		}
	}
	return best, bestRate
}

// metaHintFallback 元数据兜底：判成某类敏感数据，但样本命中率没到自动建议线。
// byContent=true 表示类型是「内容部分命中」推出来的（列名没指向它），依据文案要区分开，
// 否则用户看到「列名特征匹配」会疑惑——列名明明不叫这个。
// 置信度封顶 89，永不进入「自动生效」区间：机器只能猜，最终由人拍板。
func metaHintFallback(samples []string, match func(string) bool, dataType string, byContent bool) *scanVerdict {
	total, hit := countMatches(samples, match)
	if total == 0 {
		return &scanVerdict{
			DataType:   dataType,
			Confidence: 60,
			SampleHit:  "仅列名/注释特征匹配，样本为空或全为 NULL，需人工确认",
		}
	}
	confidence := 60 + hit*25/maxInt(total, 1) // 60~85，始终低于自动建议线
	if confidence > suggestionConfidenceCap {
		confidence = suggestionConfidenceCap
	}
	basis := fmt.Sprintf("列名/注释特征匹配，抽样 %d 条仅 %d 条通过校验位（疑似脱敏或测试数据），需人工确认", total, hit)
	if byContent {
		basis = fmt.Sprintf("抽样 %d 条中 %d 条通过校验位（未达 90%%，列名未指向该类型），需人工确认", total, hit)
	}
	return &scanVerdict{DataType: dataType, Confidence: confidence, SampleHit: basis}
}

// countMatches 统计非空样本总数与命中数（空值与 NULL 不参与命中率计算）。
func countMatches(samples []string, match func(string) bool) (total, hit int) {
	for _, s := range samples {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		total++
		if match(s) {
			hit++
		}
	}
	return total, hit
}

// detectByChecksum 强校验类判定：样本命中率 ≥ 90% 才判定为该类型。
// metaHit 表示列名/注释也带类型特征，命中时置信度加 2（内容是主依据，元数据只加分）。
func detectByChecksum(samples []string, match func(string) bool, dataType string, metaHit bool) *scanVerdict {
	total, hit := countMatches(samples, match)
	if total == 0 || float64(hit)/float64(total) < scanHitThreshold {
		return nil
	}
	confidence := 90 + hit*10/maxInt(total, 1)
	if metaHit {
		confidence += 2
	}
	if confidence > 98 {
		confidence = 98
	}
	basis := fmt.Sprintf("抽样 %d/%d 命中", hit, total)
	if metaHit {
		basis += " + 列名/注释特征"
	}
	return &scanVerdict{DataType: dataType, Confidence: confidence, SampleHit: basis}
}

// hasMetaHint 列名或注释是否包含类型特征词（| 分隔）。
func hasMetaHint(name, comment, nameKeys, commentKeys string) bool {
	return containsAny(name, strings.Split(nameKeys, "|")) ||
		containsAny(comment, strings.Split(commentKeys, "|"))
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if n != "" && strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

func hasNonEmptySamples(samples []string) bool {
	for _, s := range samples {
		if strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

func isAddressColumn(name, comment string) bool {
	for _, k := range []string{"address", "addr", "location"} {
		if strings.Contains(name, k) {
			return true
		}
	}
	return strings.Contains(comment, "地址") || strings.Contains(comment, "住址")
}

// 这些 _name 后缀列装的是机构/产品/文件等名称，不是人名，列进来只会制造噪音。
var nonPersonNameKeys = []string{
	"bank", "hotel", "company", "corp", "brand", "product", "goods", "file", "table",
	"column", "schema", "database", "city", "province", "dept", "org", "store", "shop",
	"app", "host", "server", "group", "role", "menu", "channel", "source", "type",
}

func isNameColumn(name, comment string) bool {
	// 避免把 username（账号）误判成姓名：排除含 user/account 的列名
	if strings.Contains(name, "user") || strings.Contains(name, "account") {
		return false
	}
	if containsAny(name, nonPersonNameKeys) {
		return false
	}
	if name == "name" || strings.HasSuffix(name, "_name") || name == "xm" || name == "xingming" {
		return true
	}
	return strings.Contains(comment, "姓名")
}

var (
	phoneMatcher  = regexp.MustCompile(`^1[3-9]\d{9}$`).MatchString
	emailMatcher  = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`).MatchString
	idCardMatcher = func(v string) bool {
		if len(v) != 18 {
			return false
		}
		for i := 0; i < 17; i++ {
			if v[i] < '0' || v[i] > '9' {
				return false
			}
		}
		last := unicode.ToUpper(rune(v[17]))
		if last != 'X' && (last < '0' || last > '9') {
			return false
		}
		return verifyIDCardChecksum(v) == nil
	}
	bankCardMatcher = func(v string) bool {
		if len(v) < 13 || len(v) > 19 {
			return false
		}
		for _, r := range v {
			if r < '0' || r > '9' {
				return false
			}
		}
		return verifyLuhn(v)
	}
)

// verifyIDCardChecksum GB 11643 校验位验算。
// 加权因子 [7,9,10,5,8,4,2,1,6,3,7,9,10,5,8,4,2]，模 11 映射校验码 [1,0,X,9,8,7,6,5,4,3,2]。
func verifyIDCardChecksum(id string) error {
	weights := []int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	codes := "10X98765432"
	sum := 0
	for i := 0; i < 17; i++ {
		sum += int(id[i]-'0') * weights[i]
	}
	expect := codes[sum%11]
	if !strings.EqualFold(string(id[17]), string(expect)) {
		return fmt.Errorf("身份证校验位不符：期望 %s 实际 %s", string(expect), string(id[17]))
	}
	return nil
}

// verifyLuhn 银行卡 Luhn 算法验算。
func verifyLuhn(number string) bool {
	sum := 0
	alt := false
	for i := len(number) - 1; i >= 0; i-- {
		d := int(number[i] - '0')
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
