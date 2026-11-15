package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// 金仓的 extra_config 与 PostgreSQL 同构，只有默认库不同（金仓初始化自带 test）。
// 默认库必须落库：否则连接测试、元数据同步、监控采集三处各自兜底成不同值，
// 表现是「能保存、但读不到库清单」。
func TestNormalizeInstanceExtraConfigKingbase(t *testing.T) {
	cases := []struct {
		name     string
		raw      map[string]any
		wantDB   string
		wantSSL  string
		wantKeys bool
	}{
		{name: "空配置用金仓默认库", raw: nil, wantDB: "test", wantSSL: "disable", wantKeys: true},
		{name: "显式库名保留", raw: map[string]any{"database": "bizdb"}, wantDB: "bizdb", wantSSL: "disable", wantKeys: true},
		{name: "ssl 显式开启保留", raw: map[string]any{"sslmode": "require"}, wantDB: "test", wantSSL: "require", wantKeys: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := normalizeInstanceExtraConfig("Kingbase", c.raw, "create")
			if err != nil {
				t.Fatalf("normalizeInstanceExtraConfig(Kingbase) 报错：%v", err)
			}
			var cfg map[string]any
			if err := json.Unmarshal(got, &cfg); err != nil {
				t.Fatalf("extra_config 不是合法 JSON：%v", err)
			}
			if db, _ := cfg["database"].(string); db != c.wantDB {
				t.Errorf("database = %q, want %q", db, c.wantDB)
			}
			if ssl, _ := cfg["sslmode"].(string); ssl != c.wantSSL {
				t.Errorf("sslmode = %q, want %q", ssl, c.wantSSL)
			}
		})
	}
}

// PG 的默认库不能被金仓的改动带偏：两条分支共用同一个 case，最容易在这里回归。
func TestNormalizeInstanceExtraConfigPostgresKeepsDefault(t *testing.T) {
	got, err := normalizeInstanceExtraConfig("PostgreSQL", nil, "create")
	if err != nil {
		t.Fatalf("normalizeInstanceExtraConfig(PostgreSQL) 报错：%v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(got, &cfg); err != nil {
		t.Fatalf("extra_config 不是合法 JSON：%v", err)
	}
	if db, _ := cfg["database"].(string); db != "postgres" {
		t.Errorf("PostgreSQL 默认库应仍为 postgres，实际 %q", db)
	}
}

// 连接测试必须把 kingbase 分派到 PG 分支。
// 判定方式：不可达地址应该报「连接失败」；若走到 switch 的 default 分支，
// 报的会是「暂不支持该数据库类型的连接测试」——两者不会混淆。
func TestTestConnectionModeKingbaseUsesPostgresChannel(t *testing.T) {
	_, err := testConnectionMode(context.Background(), ProbeModeLightweight,
		"Kingbase", "127.0.0.1", 54321, "system", "whatever", nil)
	if err == nil {
		t.Fatal("连不存在的金仓端口应返回错误")
	}
	msg := err.Error()
	if strings.Contains(msg, "暂不支持") {
		t.Fatalf("kingbase 未分派到 PG 分支，实际错误：%s", msg)
	}
	if !strings.Contains(msg, "连接失败") {
		t.Fatalf("期望 PG 通道的「连接失败」错误，实际：%s", msg)
	}
}

// 反例：真正不支持的类型仍应落到 default 分支，证明上面的断言不是恒真。
func TestTestConnectionModeUnknownTypeStillRejected(t *testing.T) {
	_, err := testConnectionMode(context.Background(), ProbeModeLightweight,
		"Greenplum", "127.0.0.1", 54321, "system", "whatever", nil)
	if err == nil {
		t.Fatal("未知类型应返回错误")
	}
	if !strings.Contains(err.Error(), "暂不支持") {
		t.Fatalf("未知类型应报「暂不支持」，实际：%s", err.Error())
	}
}
