package services

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/datasec/forms"
	datasecModels "github.com/simajiong/jaysql/internal/datasec/models"
)

// ---------- 脱敏算法 ----------

func paramsFromJSON(t *testing.T, raw string) maskParams {
	t.Helper()
	return parseMaskParams(json.RawMessage(raw))
}

func TestApplyMaskAlgorithmFull(t *testing.T) {
	got := ApplyMaskAlgorithm(MaskAlgoFull, maskParams{}, "13812345678")
	if got != "***********" {
		t.Fatalf("全遮蔽结果不符: %q", got)
	}
	// 空值原样返回，不产生多余字符
	if got := ApplyMaskAlgorithm(MaskAlgoFull, maskParams{}, ""); got != "" {
		t.Fatalf("空串应原样返回，got %q", got)
	}
}

func TestApplyMaskAlgorithmKeep(t *testing.T) {
	p := paramsFromJSON(t, `{"keep_head":3,"keep_tail":4}`)
	if got := ApplyMaskAlgorithm(MaskAlgoHeadTail, p, "13812345678"); got != "138****5678" {
		t.Fatalf("保留前3后4不符: %q", got)
	}
	p2 := paramsFromJSON(t, `{"keep_tail":4}`)
	if got := ApplyMaskAlgorithm(MaskAlgoTail, p2, "6222020200112345678"); got != "***************5678" {
		t.Fatalf("保留后4不符: %q", got)
	}
	// 保留位数 >= 总长时必须全遮蔽，不能明文回显
	p3 := paramsFromJSON(t, `{"keep_head":3,"keep_tail":4}`)
	if got := ApplyMaskAlgorithm(MaskAlgoHeadTail, p3, "12345"); got != "*****" {
		t.Fatalf("短值应全遮蔽: %q", got)
	}
}

func TestApplyMaskAlgorithmEmailNameAddress(t *testing.T) {
	if got := ApplyMaskAlgorithm(MaskAlgoEmail, maskParams{}, "zhangsan@example.com"); got != "z*******@example.com" {
		t.Fatalf("邮箱脱敏不符: %q", got)
	}
	if got := ApplyMaskAlgorithm(MaskAlgoName, maskParams{}, "张三丰"); got != "张**" {
		t.Fatalf("姓名脱敏不符: %q", got)
	}
	got := ApplyMaskAlgorithm(MaskAlgoAddress, maskParams{}, "浙江省杭州市西湖区文一西路100号")
	if !strings.HasPrefix(got, "浙江省杭州市西湖区") || strings.Contains(got, "文一西路") {
		t.Fatalf("地址脱敏不符: %q", got)
	}
}

func TestApplyMaskAlgorithmHash(t *testing.T) {
	a := ApplyMaskAlgorithm(MaskAlgoHash, paramsFromJSON(t, `{"hash_len":16}`), "13812345678")
	b := ApplyMaskAlgorithm(MaskAlgoHash, paramsFromJSON(t, `{"hash_len":16}`), "13812345678")
	if a != b || len(a) != 16 {
		t.Fatalf("哈希脱敏应同值同串且截断到16位: %q vs %q", a, b)
	}
}

func TestApplyMaskAlgorithmUnknownFallback(t *testing.T) {
	// 未知算法按全遮蔽兜底：宁可脱多，不可漏脱
	got := ApplyMaskAlgorithm("not_exist", maskParams{}, "abc")
	if got != "***" {
		t.Fatalf("未知算法应全遮蔽: %q", got)
	}
}

// ---------- 明文工单范围判定 ----------

func TestNormalizeGrantScope(t *testing.T) {
	cases := []struct {
		schema, table, column string
		ok                    bool
	}{
		{"", "", "", true},          // 整实例
		{"db", "", "", true},        // 整库
		{"db", "t1", "", true},      // 整表
		{"db", "t1", "c1", true},    // 单列
		{"", "t1", "", false},       // 有表无库
		{"", "", "c1", false},       // 有列无表
		{"db", "", "c1", false},     // 有列无表
	}
	for i, c := range cases {
		err := NormalizeGrantScope(c.schema, c.table, c.column)
		if (err == nil) != c.ok {
			t.Fatalf("case %d (%+v) 期望 ok=%v，实际 err=%v", i, c, c.ok, err)
		}
	}
}

// ---------- 敏感列判定器 ----------

func strPtr(v string) *scanVerdict { return &scanVerdict{DataType: v} }

func TestDetectorPhone(t *testing.T) {
	samples := []string{"13812345678", "15987652210", "18623459042", "", "13812345678"}
	v := detectSensitiveType("mobile", "手机号", samples)
	if v == nil || v.DataType != DataTypePhone {
		t.Fatalf("手机号列应判定为 phone，got %+v", v)
	}
	if v.Confidence < 90 {
		t.Fatalf("手机号置信度应>=90: %d", v.Confidence)
	}
	_ = strPtr
}

func TestDetectorIDCardChecksum(t *testing.T) {
	// 110101199003077539 校验位按 GB 11643 计算为 9；造一个校验位错的样本，命中率掉到 90% 以下应不判定
	good := "110101199003077539"
	if err := verifyIDCardChecksum(good); err != nil {
		t.Fatalf("样本本身应通过校验位验算: %v", err)
	}
	bad := "110101199003077538"
	samples := []string{good, good, good, good, bad, good, good, good, good, good}
	if v := detectSensitiveType("id_card", "身份证号", samples); v == nil || v.DataType != DataTypeIDCard {
		t.Fatalf("90%% 命中应判定为 id_card，got %+v", v)
	}
	// 全部校验位错误 → 不自动生效，但列名特征明确时降级为「建议级」
	badSamples := []string{bad, bad, bad, bad, bad, bad, bad, bad, bad, bad}
	if v := detectSensitiveType("id_card", "身份证号", badSamples); v == nil ||
		v.DataType != DataTypeIDCard || v.Confidence >= 90 {
		t.Fatalf("校验位全错但列名明确时应给建议级(<90): %+v", v)
	}
	// 列名没有任何特征 + 校验位全错 → 不判定（防止把普通 18 位数字误判成身份证）
	if v := detectSensitiveType("biz_ref", "", badSamples); v != nil {
		t.Fatalf("列名无特征且校验位不成立时不应判定: %+v", v)
	}
}

func TestDetectorMetaHintFallback(t *testing.T) {
	// 真实库常态：测试数据/脱敏数据的校验位天然不成立，靠列名兜底给建议
	cases := []struct {
		col, comment, wantType string
		samples                []string
	}{
		{"id_card", "身份证号", DataTypeIDCard, []string{"110101199003077539", "330106199211044621"}},
		{"bank_card", "银行卡号", DataTypeBankCard, []string{"6222020200112345", "6217000010012346"}},
		{"id_card_no", "", DataTypeIDCard, []string{"110101********1234"}},       // 已脱敏
		{"bank_card_no", "", DataTypeBankCard, []string{"622202******2345"}},      // 已脱敏
		{"idcard", "", DataTypeIDCard, nil},                                       // 全 NULL 也要给建议
	}
	for _, c := range cases {
		v := detectSensitiveType(c.col, c.comment, c.samples)
		if v == nil || v.DataType != c.wantType {
			t.Fatalf("%s 应给出 %s 建议，got %+v", c.col, c.wantType, v)
		}
		if v.Confidence >= 90 || v.Confidence < 60 {
			t.Fatalf("%s 兜底建议置信度应在 [60,89]，got %d", c.col, v.Confidence)
		}
		t.Logf("%-14s -> %s %d%% | %s", c.col, v.DataType, v.Confidence, v.SampleHit)
	}

	// 短词误伤回归：timestamp 含 mp、hotel 含 tel，去掉这两个 key 后都不该命中 phone
	if v := detectSensitiveType("create_timestamp", "", []string{"2026-11-12 13:39:09"}); v != nil {
		t.Fatalf("timestamp 列不应命中手机号: %+v", v)
	}
	if v := detectSensitiveType("hotel_name", "", []string{"如家酒店"}); v != nil && v.DataType == DataTypePhone {
		t.Fatalf("hotel_name 不应命中手机号: %+v", v)
	}
	// bank_name（开户银行）不是卡号，裸 bank 特征词会把它拉进建议级噪音
	if v := detectSensitiveType("bank_name", "开户银行", []string{"中国工商银行", "招商银行"}); v != nil && v.DataType == DataTypeBankCard {
		t.Fatalf("bank_name 不应命中银行卡: %+v", v)
	}
	// 内容真实匹配优先于列名猜测：列名叫 phone 但存的全是身份证号，应按内容判
	if v := detectSensitiveType("phone", "", []string{"33010619921104462X", "440304198706122545"}); v == nil ||
		v.DataType != DataTypeIDCard || v.Confidence < 90 {
		t.Fatalf("内容匹配应优先于列名，got %+v", v)
	}
	// 列名猜错、内容只部分命中：类型按内容纠偏，置信度仍压到建议级
	if v := detectSensitiveType("phone", "", []string{"33010619921104462X", "110101199003077538"}); v == nil ||
		v.DataType != DataTypeIDCard || v.Confidence >= 90 {
		t.Fatalf("列名与内容冲突时应按内容给建议级, got %+v", v)
	}
}

func TestDetectorBankCardLuhn(t *testing.T) {
	// 7992739871300 是 Luhn 通过的 13 位构造卡号；先自检算法
	okNum := "7992739871300"
	if !verifyLuhn(okNum) {
		t.Fatalf("Luhn 自检失败: %s", okNum)
	}
	badNum := "7992739871301"
	if verifyLuhn(badNum) {
		t.Fatalf("Luhn 应拒绝 %s", badNum)
	}
	samples := []string{}
	for i := 0; i < 10; i++ {
		samples = append(samples, okNum)
	}
	if v := detectSensitiveType("bank_card_no", "银行卡号", samples); v == nil || v.DataType != DataTypeBankCard {
		t.Fatalf("银行卡列应判定为 bank_card，got %+v", v)
	}
}

func TestDetectorSuggestionCap(t *testing.T) {
	// 地址/姓名是无强校验位的建议类：置信度必须低于自动建议线，强制人工确认
	v := detectSensitiveType("receiver_address", "收货地址", []string{"浙江省杭州市西湖区文一西路100号", "上海市浦东新区张江路1号"})
	if v == nil || v.DataType != DataTypeAddress || v.Confidence >= 90 {
		t.Fatalf("地址应判定为建议级(<90): %+v", v)
	}
	// username 不能被误判成姓名
	if v := detectSensitiveType("username", "登录账号", []string{"zhangsan", "lisi"}); v != nil {
		t.Fatalf("username 不应判定为姓名: %+v", v)
	}
	// 机构/产品/文件类 _name 列不是人名
	for _, col := range []string{"bank_name", "hotel_name", "company_name", "file_name", "role_name"} {
		if v := detectSensitiveType(col, "", []string{"中国工商银行", "如家酒店"}); v != nil && v.DataType == DataTypeName {
			t.Fatalf("%s 不应判定为姓名: %+v", col, v)
		}
	}
	// 真实的人名列仍要命中
	for _, col := range []string{"name", "real_name", "contact_name", "person_name"} {
		if v := detectSensitiveType(col, "", []string{"张三", "李四"}); v == nil || v.DataType != DataTypeName {
			t.Fatalf("%s 应判定为姓名: %+v", col, v)
		}
	}
}

func TestDetectByChecksumThreshold(t *testing.T) {
	// 命中率不足 90% 不判定
	samples := []string{"13812345678", "not-a-phone", "13812345678", "13812345678", "13812345678",
		"13812345678", "13812345678", "13812345678", "13812345678", "13812345678"}
	// 9/10 = 90% 恰好达标；换成 8/10 应不判定
	samples[9] = "not-a-phone"
	if v := detectByChecksum(samples, phoneMatcher, DataTypePhone, true); v != nil {
		t.Fatalf("命中率 80%% 不应判定: %+v", v)
	}
	samples[9] = "13812345678"
	if v := detectByChecksum(samples, phoneMatcher, DataTypePhone, true); v == nil {
		t.Fatalf("命中率 90%% 应判定")
	}
}

func TestBaseColumnName(t *testing.T) {
	candidates := []string{"mobile", "id_card"}
	if got := baseColumnName("mobile_6f1a2b3c[别名]", candidates); got != "mobile" {
		t.Fatalf("重列名应归一回 mobile: %q", got)
	}
	if got := baseColumnName("id_card", candidates); got != "id_card" {
		t.Fatalf("正常列名应原样返回: %q", got)
	}
	// 前缀重叠时取最长候选：id_card 与 id_card_no 场景
	if got := baseColumnName("id_card_no_xxx", []string{"id_card", "id_card_no"}); got != "id_card_no" {
		t.Fatalf("应取最长前缀候选: %q", got)
	}
}

// ---------- 表单模型冒烟（保证 SaveRuleForm.toModel 编译契约） ----------

func TestSaveRuleFormToModel(t *testing.T) {
	svc := &forms.SaveRuleForm{
		DataType: DataTypePhone, Name: "测试手机号", Algorithm: MaskAlgoHeadTail,
		Params: "", Enabled: true,
	}
	m, err := buildRuleFromForm(svc)
	if err != nil {
		t.Fatalf("合法表单不应报错: %v", err)
	}
	p := parseMaskParams(m.Params)
	// 未传参数时按类型惯例兜底：手机号 3/4
	if p.KeepHead != 3 || p.KeepTail != 4 {
		t.Fatalf("手机号缺省参数应为 3/4，got %+v", p)
	}
	svc.Algorithm = "unknown_algo"
	if _, err := buildRuleFromForm(svc); err == nil {
		t.Fatalf("未知算法应报错")
	}
	_ = datasecModels.InsightDataSecRules{}
}
