package services

import (
	"strings"
	"testing"
)

func TestLogicalToolNameMapping(t *testing.T) {
	cases := []struct {
		dbType        string
		wantDump      string
		wantRestore   string
		wantQuery     string
	}{
		{"PostgreSQL", "pg_dump", "pg_restore", "psql"},
		{"postgresql", "pg_dump", "pg_restore", "psql"},
		{"Kingbase", "sys_dump", "sys_restore", "ksql"},
		{"KingbaseES", "sys_dump", "sys_restore", "ksql"},
		{"kingbase es", "sys_dump", "sys_restore", "ksql"},
	}
	for _, c := range cases {
		if got := logicalToolName(c.dbType, logicalToolRoleDump); got != c.wantDump {
			t.Fatalf("%s dump 工具名 = %s, 期望 %s", c.dbType, got, c.wantDump)
		}
		if got := logicalToolName(c.dbType, logicalToolRoleRestore); got != c.wantRestore {
			t.Fatalf("%s restore 工具名 = %s, 期望 %s", c.dbType, got, c.wantRestore)
		}
		if got := logicalToolName(c.dbType, logicalToolRoleQuery); got != c.wantQuery {
			t.Fatalf("%s query 工具名 = %s, 期望 %s", c.dbType, got, c.wantQuery)
		}
	}
}

func TestIsPostgresLogicalFamily(t *testing.T) {
	for _, dbType := range []string{"PostgreSQL", "postgresql", "Kingbase", "KingbaseES", "kingbase-es"} {
		if !isPostgresLogicalFamily(dbType) {
			t.Fatalf("%s 应归入 PG 逻辑通道", dbType)
		}
	}
	for _, dbType := range []string{"MySQL", "Oracle", "TiDB", "SQLServer", ""} {
		if isPostgresLogicalFamily(dbType) {
			t.Fatalf("%s 不应归入 PG 逻辑通道", dbType)
		}
	}
}

func TestLogicalDefaultsDivergePerFamily(t *testing.T) {
	if got := logicalDefaultDatabase("PostgreSQL"); got != "postgres" {
		t.Fatalf("PG 默认库 = %s, 期望 postgres", got)
	}
	// 金仓 V8 装机自带 test / kingbase / security，test 是默认库
	if got := logicalDefaultDatabase("Kingbase"); got != "test" {
		t.Fatalf("金仓默认库 = %s, 期望 test", got)
	}
	if got := logicalDefaultOSUser("Kingbase"); got != "kingbase" {
		t.Fatalf("金仓 OS 用户 = %s, 期望 kingbase", got)
	}
	if got := logicalDefaultOSUser("PostgreSQL"); got != "postgres" {
		t.Fatalf("PG OS 用户 = %s, 期望 postgres", got)
	}
}

// 金仓版本串（V009R001C010）没有 PG major 概念，必须跳过客户端 major 强校验，
// 否则可用环境会被判成「客户端版本不兼容」。
func TestKingbaseSkipsPostgresMajorValidation(t *testing.T) {
	if requiresLogicalExactMajor("Kingbase", "sys_dump") {
		t.Fatal("金仓不应要求客户端 major 与服务端一致")
	}
	if !requiresLogicalExactMajor("PostgreSQL", "pg_dump") {
		t.Fatal("PostgreSQL 仍应保留 major 强校验")
	}
	if got := logicalServerVersionForTool("Kingbase", "KingbaseES V009R001C010"); got != "" {
		t.Fatalf("金仓打工具版本时应清空服务端版本，实际 %s", got)
	}
	if got := logicalServerVersionForTool("PostgreSQL", "16.2"); got != "16.2" {
		t.Fatalf("PG 应原样透传版本，实际 %s", got)
	}
}

// 探测脚本必须真的去找 sys_dump，并且带上金仓装机目录；
// 否则运维把 bin 加进 PATH 之前，预检查会一直报「未检测到逻辑备份工具」。
func TestKingbaseProbeScriptLooksForSysDump(t *testing.T) {
	script := buildLogicalToolResolveProbeScript("Kingbase", "KingbaseES V009R001C010", "")
	if !strings.Contains(script, "sys_dump") {
		t.Fatal("金仓探测脚本应查找 sys_dump")
	}
	if strings.Contains(script, "TOOL_NAME=pg_dump") {
		t.Fatal("金仓探测脚本不应回落到 pg_dump")
	}
	if !strings.Contains(script, "/opt/Kingbase/ES/V8/Server/bin") {
		t.Fatal("金仓探测脚本应包含默认装机目录")
	}
}

func TestPostgresProbeScriptUnchanged(t *testing.T) {
	script := buildLogicalToolResolveProbeScript("PostgreSQL", "16.2", "")
	// 赋值语句统一走 shellQuote，可能带单引号；两种形态都算命中
	if !strings.Contains(script, "TOOL_NAME=pg_dump") && !strings.Contains(script, "TOOL_NAME='pg_dump'") {
		t.Fatalf("PG 探测脚本仍应使用 pg_dump，实际:\n%s", script)
	}
	if strings.Contains(script, "sys_dump") {
		t.Fatal("PG 探测脚本不应包含金仓工具名")
	}
}

// 金仓的对象模型与 PG 一致（库 → schema → 表），必须复用同一套解析，
// 否则自定义备份会退化成「只备份整库」。
func TestKingbaseReusesPostgresObjectSpec(t *testing.T) {
	pg, err := normalizeObjectSpec("PostgreSQL", "LOGICAL", "ALL", "", "FULL", nil)
	if err != nil {
		t.Fatalf("PG 解析失败: %v", err)
	}
	kb, err := normalizeObjectSpec("Kingbase", "LOGICAL", "ALL", "", "FULL", nil)
	if err != nil {
		t.Fatalf("金仓解析失败: %v", err)
	}
	if len(kb.Jobs) != len(pg.Jobs) {
		t.Fatalf("金仓 jobs 数 %d 与 PG %d 不一致", len(kb.Jobs), len(pg.Jobs))
	}

	spec := map[string]any{
		"databases": []map[string]any{
			{"name": "test", "schemas": []string{"public"}, "tables": []map[string]any{}},
		},
	}
	kbCustom, err := normalizeObjectSpec("Kingbase", "LOGICAL", "CUSTOM", "", "FULL", spec)
	if err != nil {
		t.Fatalf("金仓自定义解析失败: %v", err)
	}
	if len(kbCustom.Jobs) != 1 || kbCustom.Jobs[0].DBName != "test" {
		t.Fatalf("金仓自定义对象解析错误: %+v", kbCustom.Jobs)
	}
	if len(kbCustom.Jobs[0].PgSchemas) != 1 || kbCustom.Jobs[0].PgSchemas[0] != "public" {
		t.Fatalf("金仓 schema 未透传: %+v", kbCustom.Jobs[0].PgSchemas)
	}
}

// 备份产物打包策略：金仓与 PG 一样是多库 zip，不能落成裸 .dump 单文件。
func TestKingbaseArtifactPlanUsesZip(t *testing.T) {
	plan, err := PlanBackupArtifact("Kingbase", "LOGICAL", "ALL", "", "FULL", nil, nil)
	if err != nil {
		t.Fatalf("产物计划失败: %v", err)
	}
	if plan.ArtifactName != "backup.zip" {
		t.Fatalf("金仓产物名 = %s, 期望 backup.zip", plan.ArtifactName)
	}
	if plan.StreamCompression != ArtifactCompressionZip {
		t.Fatalf("金仓压缩方式 = %s, 期望 zip", plan.StreamCompression)
	}
}
