package services

import (
	"strings"
	"testing"
)

// 回归背景：Oracle 版本一直显示「Oracle <nil>」。
// 根因是 Oracle 把不带引号的列别名折叠成大写（AS db_version → DB_VERSION），
// 而采集侧按小写 key 取值 → 拿到 nil → toString(nil) = "<nil>" → 原样写进
// insight_instances.db_version。MySQL/PG 列名本来就是小写，所以只有 Oracle 中招。
//
// 这组测试锁住三件事：别名必须带引号、取值大小写不敏感、NULL 哨兵不得入库。

func TestOracleProfileFactsSQLKeepsQuotedAliases(t *testing.T) {
	queries := []struct{ name, sql string }{
		{"oracleProfileFactsSQL", oracleProfileFactsSQL},
		{"oracleProfileFactsLegacySQL", oracleProfileFactsLegacySQL},
	}
	for _, q := range queries {
		for _, alias := range []string{"db_version", "db_role", "uptime_seconds"} {
			if !strings.Contains(q.sql, `"`+alias+`"`) {
				t.Errorf("%s 缺少带引号的别名 %q —— Oracle 会把它折叠成大写，导致取值为 nil", q.name, alias)
			}
		}
	}
}

func TestOracleProfileFactsSQLPrefersVersionFull(t *testing.T) {
	// version 只给主版本（19c 一律 19.0.0.0.0），version_full 才带 Release Update（19.3.0.0.0）。
	// 页面要能区分补丁级别，所以主 SQL 必须用 version_full。
	if !strings.Contains(oracleProfileFactsSQL, "version_full") {
		t.Errorf("oracleProfileFactsSQL 必须使用 version_full，否则页面只能显示主版本（19.0.0.0.0）")
	}
	// 回退 SQL 面向 12.1 及更早（没有 version_full 列），不能再用 version_full。
	if strings.Contains(oracleProfileFactsLegacySQL, "version_full") {
		t.Errorf("oracleProfileFactsLegacySQL 是给 12.1 及更早用的，不得出现 version_full")
	}
}

func TestOracleProfileValueIsCaseInsensitive(t *testing.T) {
	// 带引号别名时驱动返回小写；万一有人改回不带引号，Oracle 返回大写。
	// 两种形态都必须取得到，这是最后一道兜底。
	cases := []struct {
		name string
		item map[string]any
	}{
		{"大写 key（Oracle 不带引号别名）", map[string]any{"DB_VERSION": "19.0.0.0.0", "DB_ROLE": "PRIMARY"}},
		{"小写 key（带引号别名）", map[string]any{"db_version": "19.0.0.0.0", "db_role": "PRIMARY"}},
		{"混合大小写", map[string]any{"Db_Version": "19.0.0.0.0", "Db_Role": "PRIMARY"}},
	}
	for _, c := range cases {
		if got := toString(oracleProfileValue(c.item, "db_version")); got != "19.0.0.0.0" {
			t.Errorf("%s: 取 db_version 得到 %q，期望 19.0.0.0.0", c.name, got)
		}
		if got := toString(oracleProfileValue(c.item, "db_role")); got != "PRIMARY" {
			t.Errorf("%s: 取 db_role 得到 %q，期望 PRIMARY", c.name, got)
		}
	}

	// 列不存在时返回 nil（交给 normalizeDbVersion 过滤），不能 panic
	if got := oracleProfileValue(map[string]any{}, "db_version"); got != nil {
		t.Errorf("缺失列应返回 nil，实际 %v", got)
	}
}

func TestNormalizeDbVersionDropsNullSentinels(t *testing.T) {
	// fmt.Sprintf("%v", nil) 的结果就是 "<nil>"，它首字符不是数字，
	// 会绕过版本号解析被原样返回 —— 必须在这里拦成空串。
	for _, raw := range []string{"<nil>", "<NULL>", "NULL", "nil", "None", "  <nil>  "} {
		if got := normalizeDbVersion(raw); got != "" {
			t.Errorf("normalizeDbVersion(%q) = %q，期望空串（NULL 哨兵不得入库）", raw, got)
		}
	}
}

func TestNormalizeDbVersionKeepsRealVersions(t *testing.T) {
	cases := map[string]string{
		"19.0.0.0.0": "19.0.0.0.0",
		"8.0.34-log": "8.0.34",
		// TiDB：前导段是 MySQL 兼容版本号，必须取 -v 尾段的真实 TiDB 版本，
		// 否则页面上显示 "TiDB 8.0.11"，用户会误以为是 MySQL 8.0.11。
		// 版本串不带引擎名 —— 展示处已经拼了 db_type，再带 -TiDB 就成了
		// 「TiDB 8.0.11-TiDB」。
		"8.0.11-TiDB-v8.5.1": "8.5.1",
		"5.7.25-TiDB-v7.5.0": "7.5.0",
		// 老版本无 -v 尾段："8.0.11-TiDB" 的前导段本身就是 TiDB 版本。
		"5.7.25-TiDB":                        "5.7.25",
		"8.0.11-TiDB":                        "8.0.11",
		"PostgreSQL 15.3 on x86_64-pc-linux": "15.3",
		"16.15":                              "16.15",
		// SQL Server：SERVERPROPERTY('ProductVersion') 是四段式数字。
		"16.0.4165.4": "16.0.4165.4",
	}
	for raw, want := range cases {
		if got := normalizeDbVersion(raw); got != want {
			t.Errorf("normalizeDbVersion(%q) = %q，期望 %q", raw, got, want)
		}
	}
}
