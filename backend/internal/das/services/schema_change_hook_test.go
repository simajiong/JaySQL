package services

import "testing"

// IsSchemaInventoryDDL 的判定直接决定「建库后能不能秒级看到」：
// 漏判 → 用户还是得等定时任务；误判 → 每条建表语句都触发一次全实例元数据同步。
func TestIsSchemaInventoryDDL(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		{name: "MySQL 建库", sql: "CREATE DATABASE demo", want: true},
		{name: "MySQL 建库带 IF NOT EXISTS", sql: "create database if not exists `demo` default charset utf8mb4", want: true},
		{name: "MySQL 删库", sql: "DROP DATABASE demo", want: true},
		{name: "MySQL CREATE SCHEMA 是库别名", sql: "CREATE SCHEMA demo", want: true},
		{name: "PG 建 schema", sql: "create schema if not exists app", want: true},
		{name: "PG 建库", sql: "CREATE DATABASE demo WITH ENCODING 'UTF8'", want: true},
		{name: "SQLServer 建 schema", sql: "CREATE SCHEMA sales", want: true},
		{name: "SQLServer 改库名", sql: "sp_renamedb 'old', 'new'", want: true},
		{name: "Oracle 建用户即建 schema", sql: "CREATE USER hongjie IDENTIFIED BY \"pwd\"", want: true},
		{name: "带行注释前缀", sql: "-- 新建测试库\nCREATE DATABASE demo", want: true},
		{name: "带块注释前缀", sql: "/* ticket:1234 */\ndrop database demo", want: true},
		{name: "带井号注释前缀", sql: "# mysql 风格注释\nCREATE DATABASE demo", want: true},
		{name: "前导空白与换行", sql: "\n\t  CREATE DATABASE demo", want: true},
		{name: "建表不触发", sql: "CREATE TABLE demo.t (id int)", want: false},
		{name: "改表不触发", sql: "ALTER TABLE demo.t ADD COLUMN c int", want: false},
		{name: "建索引不触发", sql: "CREATE INDEX idx_a ON demo.t(a)", want: false},
		{name: "查询不触发", sql: "SELECT * FROM demo.t", want: false},
		{name: "空语句", sql: "   ", want: false},
		{name: "只有注释", sql: "-- nothing here", want: false},
		{name: "模糊词不误判", sql: "CREATE DATABASE_LIKE_THING", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSchemaInventoryDDL(tc.sql); got != tc.want {
				t.Fatalf("IsSchemaInventoryDDL(%q) = %v, want %v", tc.sql, got, tc.want)
			}
		})
	}
}
