package dbfamily

import "testing"

func TestEffectiveFamily(t *testing.T) {
	tests := []struct {
		name        string
		dbType      string
		extraConfig string
		want        string
	}{
		// 既有类型：必须与历史 IsMySQLFamily / IsPostgresFamily 口径逐字符一致，
		// 这是把老调用点切到本包的安全不变量。
		{name: "mysql", dbType: "MySQL", want: FamilyMySQL},
		{name: "tidb lower", dbType: "tidb", want: FamilyMySQL},
		{name: "doris mixed", dbType: "Doris", want: FamilyMySQL},
		{name: "mariadb", dbType: "mariadb", want: FamilyMySQL},
		{name: "polardb mysql", dbType: "polardb-mysql", want: FamilyMySQL},
		{name: "postgresql", dbType: "PostgreSQL", want: FamilyPostgres},
		{name: "postgres alias", dbType: "postgres", want: FamilyPostgres},
		{name: "polar pg", dbType: "polardb-pg", want: FamilyPostgres},
		// 金仓：兼容 PG 协议，归 postgres 家族（连接/元数据/执行/审核全部复用 PG 通道）
		{name: "kingbase", dbType: "Kingbase", want: FamilyPostgres},
		{name: "kingbase lower", dbType: "kingbase", want: FamilyPostgres},
		{name: "kingbasees", dbType: "KingbaseES", want: FamilyPostgres},
		{name: "oracle", dbType: "Oracle", want: FamilyOracle},
		{name: "clickhouse", dbType: "ClickHouse", want: FamilyClickHouse},
		{name: "sqlserver", dbType: "SQLServer", want: FamilySQLServer},
		{name: "mongodb", dbType: "MongoDB", want: FamilyMongoDB},
		{name: "unknown passthrough", dbType: "Greenplum", want: "greenplum"},
		{name: "wrapped spaces", dbType: "  MySQL  ", want: FamilyMySQL},

		// OceanBase：模式决定家族，缺省回落 MySQL。
		{name: "ob default(no cfg)", dbType: "OceanBase", want: FamilyMySQL},
		{name: "ob empty cfg", dbType: "OceanBase", extraConfig: `{}`, want: FamilyMySQL},
		{name: "ob mysql", dbType: "OceanBase", extraConfig: `{"oceanbase_mode":"mysql"}`, want: FamilyMySQL},
		{name: "ob oracle", dbType: "OceanBase", extraConfig: `{"oceanbase_mode":"oracle"}`, want: FamilyOracle},
		{name: "ob oracle upper", dbType: "OceanBase", extraConfig: `{"oceanbase_mode":"Oracle"}`, want: FamilyOracle},
		{name: "ob oracle alias", dbType: "OceanBase", extraConfig: `{"oceanbase_mode":"oceanbase-oracle"}`, want: FamilyOracle},
		{name: "ob invalid mode falls back mysql", dbType: "OceanBase", extraConfig: `{"oceanbase_mode":"weird"}`, want: FamilyMySQL},
		{name: "ob broken json falls back mysql", dbType: "OceanBase", extraConfig: `{not-json`, want: FamilyMySQL},
		{name: "ob mode null", dbType: "OceanBase", extraConfig: `{"oceanbase_mode":null}`, want: FamilyMySQL},
		{name: "ob lower case type", dbType: "oceanbase", extraConfig: `{"oceanbase_mode":"oracle"}`, want: FamilyOracle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg []byte
			if tt.extraConfig != "" {
				cfg = []byte(tt.extraConfig)
			}
			if got := EffectiveFamily(tt.dbType, cfg); got != tt.want {
				t.Fatalf("EffectiveFamily(%q, %q) = %q, want %q", tt.dbType, tt.extraConfig, got, tt.want)
			}
		})
	}
}

func TestOceanBasePredicates(t *testing.T) {
	if !IsOceanBase("OceanBase") || !IsOceanBase("  oceanbase ") {
		t.Fatal("IsOceanBase 应大小写/空白不敏感")
	}
	if IsOceanBase("MySQL") || IsOceanBase("Oracle") {
		t.Fatal("IsOceanBase 不应把 MySQL/Oracle 判成 OceanBase")
	}

	oracleCfg := []byte(`{"oceanbase_mode":"oracle"}`)
	if !IsOracle("OceanBase", oracleCfg) || IsMySQL("OceanBase", oracleCfg) {
		t.Fatal("OceanBase Oracle 模式应判为 oracle 家族")
	}
	if !IsMySQL("OceanBase", []byte(`{"oceanbase_mode":"mysql"}`)) {
		t.Fatal("OceanBase MySQL 模式应判为 mysql 家族")
	}
	// MySQL 模式不因判定函数而误入 Oracle 分支
	if IsOracle("OceanBase", nil) || IsPostgres("OceanBase", nil) {
		t.Fatal("缺省模式的 OceanBase 不应判为 oracle / postgresql")
	}
}

func TestNormalizeMode(t *testing.T) {
	cases := map[string]string{
		"mysql":             OceanBaseMySQL,
		"MySQL":             OceanBaseMySQL,
		" oceanbase_mysql ": OceanBaseMySQL,
		"mysql_mode":        OceanBaseMySQL,
		"oracle":            OceanBaseOracle,
		"Oracle":            OceanBaseOracle,
		"ob-oracle":         "", // 未登记的别名不猜测，交调用方兜底
		"":                  "",
	}
	for raw, want := range cases {
		if got := NormalizeMode(raw); got != want {
			t.Fatalf("NormalizeMode(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestOceanBaseModeDefault(t *testing.T) {
	// 未配置 / 无法解析一律回落 MySQL，绝不返回空串 —— 否则下游 switch 会漏分支。
	for _, cfg := range [][]byte{nil, {}, []byte(`{}`), []byte(`{"oceanbase_mode":""}`), []byte(`garbage`)} {
		if got := OceanBaseMode(cfg); got != OceanBaseMySQL {
			t.Fatalf("OceanBaseMode(%q) = %q, want %q", string(cfg), got, OceanBaseMySQL)
		}
	}
}
