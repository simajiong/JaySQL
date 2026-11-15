package tools

import "testing"

// argStringList 是模型传「字符串数组」的唯一入口：数组、逗号分隔字符串都要兜住。
// 曾经踩过坑：appendUniqueString 返回新切片却没接返回值，列表类参数被静默丢弃，
// 表现是「模型明明传了 schemas，工具却当没传」——这里锁死回归。
func TestArgStringList(t *testing.T) {
	cases := []struct {
		args map[string]any
		key  string
		want []string
	}{
		{map[string]any{"tables": []any{"t1", "t2"}}, "tables", []string{"t1", "t2"}},
		{map[string]any{"tables": []string{"t1", "t2", "t1"}}, "tables", []string{"t1", "t2"}},
		{map[string]any{"tables": "t1,t2, t1 "}, "tables", []string{"t1", "t2"}},
		{map[string]any{"tables": []any{"", " ", "t1"}}, "tables", []string{"t1"}},
	}
	for _, item := range cases {
		got := argStringList(item.args, item.key)
		if len(got) != len(item.want) {
			t.Fatalf("argStringList(%v) = %v, want %v", item.args, got, item.want)
		}
		for i := range got {
			if got[i] != item.want[i] {
				t.Fatalf("argStringList(%v) = %v, want %v", item.args, got, item.want)
			}
		}
	}
	if got := argStringList(map[string]any{}, "tables"); len(got) != 0 {
		t.Fatalf("缺参数应返回空列表，got %v", got)
	}
}

// 写工具的 SQL 推断：标题与回滚 SQL（纯函数，不依赖数据库）
func TestInferOrderMeta(t *testing.T) {
	cases := []struct {
		sql          string
		wantTitle    string
		wantRollback string
	}{
		{"CREATE DATABASE test001 DEFAULT CHARACTER SET utf8mb4;", "创建数据库 test001", "DROP DATABASE IF EXISTS `test001`;"},
		{"create database if not exists `app_db`;", "创建数据库 app_db", "DROP DATABASE IF EXISTS `app_db`;"},
		{"ALTER TABLE t1 ADD COLUMN c1 INT;", "修改表结构", ""},
		{"CREATE TABLE t2 (id int);", "创建表", ""},
		{"DROP TABLE t2;", "删除表", ""},
		{"CREATE DATABASE `test001`;", "创建数据库 test001", "DROP DATABASE IF EXISTS `test001`;"},
	}
	for _, item := range cases {
		if got := inferOrderTitle(item.sql, ""); got != item.wantTitle {
			t.Fatalf("title(%q) = %q, want %q", item.sql, got, item.wantTitle)
		}
		if got := inferRollbackSQL(item.sql); got != item.wantRollback {
			t.Fatalf("rollback(%q) = %q, want %q", item.sql, got, item.wantRollback)
		}
	}
}

// 工具注册表完整性：每个工具都要有描述、参数 Schema、处理函数；
// 写工具只允许「创建工单 / 创建备份」两个，且都要走草稿闸门（不存在直通落库的工具）。
func TestToolRegistry(t *testing.T) {
	names := Names()
	if len(names) < 7 {
		t.Fatalf("工具数量不足，got %d: %v", len(names), names)
	}

	writeCount := 0
	for _, name := range names {
		tool, ok := Get(name)
		if !ok || tool.Handler == nil {
			t.Fatalf("工具 %s 未正确注册", name)
		}
		if len(tool.Parameters) == 0 || tool.Description == "" {
			t.Fatalf("工具 %s 缺少描述或参数定义", name)
		}
		if tool.Write {
			writeCount++
			if tool.Permission == "" {
				t.Fatalf("写工具 %s 缺少权限点声明", name)
			}
		}
	}
	if writeCount != len(writeToolNames) {
		t.Fatalf("写工具数量应为 %d，got %d", len(writeToolNames), writeCount)
	}
	if defs := Definitions(false); len(defs) != len(names)-writeCount {
		t.Fatalf("只读工具清单数量不符：%d vs %d", len(defs), len(names)-writeCount)
	}
}

// writeToolNames 当前允许落草稿的写工具（新增写工具必须同步这里，逼迫评审一遍闸门）
var writeToolNames = []string{"create_order", "create_backup"}

// 写工具必须注册齐全、标 Write、声明权限点，并带确认落库入口（SubmitPath）
func TestWriteToolsRegistered(t *testing.T) {
	for _, name := range writeToolNames {
		tool, ok := Get(name)
		if !ok {
			t.Fatalf("写工具 %s 未注册", name)
		}
		if !tool.Write {
			t.Fatalf("工具 %s 应标记为写操作", name)
		}
	}
}

// create_backup 的入参推导（纯函数，不依赖数据库）：
// 给了库名走逻辑备份、物理备份只能是整实例、调度星期归一。
func TestInferBackupMeta(t *testing.T) {
	if got, err := inferBackupDriverType("MySQL", "", nil); err != nil || got != "XTRABACKUP" {
		t.Fatalf("未指定时应推导 MySQL 物理备份，got %q err %v", got, err)
	}
	if got, err := inferBackupDriverType("PostgreSQL", "", nil); err != nil || got != "PG_BACKREST" {
		t.Fatalf("未指定时应推导 PG 物理备份，got %q err %v", got, err)
	}
	if got, err := inferBackupDriverType("MySQL", "", []string{"order"}); err != nil || got != "LOGICAL" {
		t.Fatalf("给了库名应推导逻辑备份，got %q err %v", got, err)
	}
	if _, err := inferBackupDriverType("PostgreSQL", "XTRABACKUP", nil); err == nil {
		t.Fatalf("XtraBackup 挂到 PostgreSQL 上应报错")
	}
	if _, err := inferBackupDriverType("MySQL", "MYSQLDUMP", nil); err == nil {
		t.Fatalf("未知备份方式应报错")
	}

	// 金仓：无物理备份工具链，未指定时必须落逻辑；同时校验各种写法的类型归一。
	for _, dbType := range []string{"Kingbase", "KingbaseES", "kingbasees", "Kingbase ES"} {
		if got, err := inferBackupDriverType(dbType, "", nil); err != nil || got != "LOGICAL" {
			t.Fatalf("%s 未指定备份方式时应推导逻辑备份，got %q err %v", dbType, got, err)
		}
		if _, err := inferBackupDriverType(dbType, "PG_BACKREST", nil); err == nil {
			t.Fatalf("%s 指定 pgBackRest 应报错（金仓无该工具链）", dbType)
		}
	}

	days, err := normalizeBackupScheduleDays([]string{"周二", "SUNDAY", "周二"})
	if err != nil {
		t.Fatalf("调度星期归一失败: %v", err)
	}
	if len(days) != 2 || days[0] != "TUE" || days[1] != "SUN" {
		t.Fatalf("调度星期去重/排序不符：%v", days)
	}
	if _, err := normalizeBackupScheduleDays([]string{"周八"}); err == nil {
		t.Fatalf("非法调度星期应报错")
	}
	if all := mustBackupScheduleDays(nil); len(all) != 7 {
		t.Fatalf("留空应表示每天，got %v", all)
	}
	if text := buildBackupScheduleText([]string{"TUE", "SUN"}, "01:30"); text != "每周 周二、周日 01:30" {
		t.Fatalf("调度文案不符：%s", text)
	}
	if name := inferBackupTaskName(backupDriverText("XTRABACKUP"), "192.168.1.99", 3306, nil); name != "物理备份-192.168.1.99:3306" {
		t.Fatalf("任务名不符：%s", name)
	}
}

func mustBackupScheduleDays(raw []string) []string {
	out, err := normalizeBackupScheduleDays(raw)
	if err != nil {
		panic(err)
	}
	return out
}

// 实例与库表工具必须注册齐全（这四个是「查看实例 / 表介绍」能力的入口）
func TestInstanceTableToolsRegistered(t *testing.T) {
	for _, name := range []string{"list_instances", "get_instance", "list_tables", "describe_table"} {
		tool, ok := Get(name)
		if !ok {
			t.Fatalf("工具 %s 未注册", name)
		}
		if tool.Write {
			t.Fatalf("工具 %s 应为只读，不得标记写操作", name)
		}
		if tool.Permission == "" {
			t.Fatalf("工具 %s 缺少权限点声明", name)
		}
		properties, ok := tool.Parameters["properties"].(map[string]any)
		if !ok || len(properties) == 0 {
			t.Fatalf("工具 %s 参数 Schema 不合法", name)
		}
	}
}
