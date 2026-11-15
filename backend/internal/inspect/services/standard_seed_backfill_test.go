package services

import (
	"encoding/json"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/simajiong/jaysql/internal/inspect/models"
)

// openSQLite 打开内存库并手写建表（AutoMigrate 会生成 MySQL enum DDL，sqlite 不认）。
func openSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开 sqlite 失败: %v", err)
	}
	ddl := []string{
		`CREATE TABLE insight_sql_standards (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME, updated_at DATETIME, deleted_at DATETIME,
			name VARCHAR(128) NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			is_default BOOLEAN NOT NULL DEFAULT FALSE,
			env_type VARCHAR(16) NOT NULL DEFAULT 'dev',
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_by VARCHAR(64) NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE insight_sql_standard_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME, updated_at DATETIME, deleted_at DATETIME,
			standard_id INTEGER NOT NULL,
			rule_key VARCHAR(128) NOT NULL,
			category VARCHAR(32) NOT NULL,
			rule_type VARCHAR(32) NOT NULL DEFAULT '',
			db_type VARCHAR(32) NOT NULL,
			name VARCHAR(256) NOT NULL,
			description TEXT NOT NULL,
			base_level VARCHAR(16) NOT NULL DEFAULT 'suggest',
			level VARCHAR(16) NOT NULL DEFAULT 'off',
			params JSON,
			sort_order INTEGER NOT NULL DEFAULT 0,
			implemented BOOLEAN NOT NULL DEFAULT FALSE
		)`,
	}
	for _, s := range ddl {
		if err := db.Exec(s).Error; err != nil {
			t.Fatalf("建表失败: %v\nSQL: %s", err, s)
		}
	}
	return db
}

// 存量规范目录对账补齐（ensureCatalogMissingRules）的集成测试。
//
// 复现生产问题：老环境初始化规范时目录还没有某条规则（如 postgres 的
// limit_update_delete_affected_rows），后来代码加了种子，但播种逻辑
// 「规范有规则就整体跳过」，导致老规范永远缺新行——规范页看不到新库/新规则。
func TestEnsureCatalogMissingRules(t *testing.T) {
	db := openSQLite(t)

	dev := models.InsightSQLStandard{Name: "默认开发环境规范", EnvType: "dev", IsDefault: true, Enabled: true}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("创建规范失败: %v", err)
	}

	// 模拟老环境：规范里只有 2 条旧规则（级别被管理员改过，必须保住）
	old := []models.InsightSQLStandardRule{
		{StandardID: dev.ID, RuleKey: "mysql.struct.table_must_have_pk", Category: "structure",
			RuleType: "表", DbType: "mysql", Name: "表必须有主键", BaseLevel: "must", Level: "off"},
		{StandardID: dev.ID, RuleKey: "postgres.struct.table_must_have_pk", Category: "structure",
			RuleType: "表", DbType: "postgres", Name: "表必须有主键", BaseLevel: "must", Level: "must"},
	}
	if err := db.Create(&old).Error; err != nil {
		t.Fatalf("写入旧规则失败: %v", err)
	}

	catalog := defaultStandardRules()
	if len(catalog) == 0 {
		t.Fatal("规则目录为空，测试前提不成立")
	}

	if err := ensureCatalogMissingRules(db); err != nil {
		t.Fatalf("补齐失败: %v", err)
	}

	// 断言 1：规范行数 == 目录条数（缺的全补上，已有的不重复）
	var count int64
	db.Model(&models.InsightSQLStandardRule{}).Where("standard_id = ?", dev.ID).Count(&count)
	if count != int64(len(catalog)) {
		t.Errorf("补齐后规则行数 = %d, 期望目录条数 %d", count, len(catalog))
	}

	// 断言 2：生产问题主角 —— postgres 影响行数规则已补进老规范
	var pgAffected models.InsightSQLStandardRule
	if err := db.Where("standard_id = ? AND rule_key = ?", dev.ID,
		"postgres.query_update.limit_update_delete_affected_rows").Take(&pgAffected).Error; err != nil {
		t.Fatalf("老规范仍未补到 postgres 影响行数规则: %v", err)
	}
	if pgAffected.Level == "" || pgAffected.BaseLevel == "" {
		t.Errorf("补进行缺少级别: base_level=%q level=%q", pgAffected.BaseLevel, pgAffected.Level)
	}

	// 断言 3：管理员改过的旧行不被覆盖
	var pkRule models.InsightSQLStandardRule
	if err := db.Where("standard_id = ? AND rule_key = ?", dev.ID, "mysql.struct.table_must_have_pk").Take(&pkRule).Error; err != nil {
		t.Fatalf("查询旧规则失败: %v", err)
	}
	if pkRule.Level != "off" {
		t.Errorf("已有规则级别被覆盖: level=%q, 期望保住 off", pkRule.Level)
	}

	// 断言 4：幂等 —— 再跑一次行数不变
	if err := ensureCatalogMissingRules(db); err != nil {
		t.Fatalf("二次补齐失败: %v", err)
	}
	db.Model(&models.InsightSQLStandardRule{}).Where("standard_id = ?", dev.ID).Count(&count)
	if count != int64(len(catalog)) {
		t.Errorf("二次补齐后行数 = %d, 期望仍为 %d（幂等被破坏）", count, len(catalog))
	}
}

// dev/prod 级别转换口径：生产 suggest→must，开发 must→suggest，其余原样。
func TestSeedLevelForEnv(t *testing.T) {
	cases := []struct {
		base, env, want string
	}{
		{"suggest", "prod", "must"},
		{"must", "prod", "must"},
		{"must", "dev", "suggest"},
		{"suggest", "dev", "suggest"},
		{"off", "prod", "off"},
		{"off", "dev", "off"},
	}
	for _, c := range cases {
		if got := seedLevelForEnv(c.base, c.env); got != c.want {
			t.Errorf("seedLevelForEnv(%q, %q) = %q, want %q", c.base, c.env, got, c.want)
		}
	}
}

// 全链路：InitializeDefaultSQLStandards 在「规范已存在且只有部分规则」时
// 也会补齐目录缺口（这是生产环境的实际触发路径）。
func TestInitializeBackfillsExistingStandard(t *testing.T) {
	db := openSQLite(t)

	// 老环境只有 dev 规范（无 prod），且只有一条旧规则
	dev := models.InsightSQLStandard{Name: "默认开发环境规范", EnvType: "dev", IsDefault: true, Enabled: true}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("创建规范失败: %v", err)
	}
	oldRule := models.InsightSQLStandardRule{StandardID: dev.ID, RuleKey: "mysql.struct.table_must_have_pk",
		Category: "structure", RuleType: "表", DbType: "mysql", Name: "表必须有主键",
		BaseLevel: "must", Level: "off"}
	if err := db.Create(&oldRule).Error; err != nil {
		t.Fatalf("写入旧规则失败: %v", err)
	}

	if err := InitializeDefaultSQLStandards(db); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	// dev 规范补齐到目录全集
	var devCount int64
	db.Model(&models.InsightSQLStandardRule{}).Where("standard_id = ?", dev.ID).Count(&devCount)
	if devCount != int64(len(defaultStandardRules())) {
		t.Errorf("存量 dev 规范补齐后行数 = %d, 期望 %d", devCount, len(defaultStandardRules()))
	}

	// 缺失的 prod 规范被新建并全量播种（prod 口径：suggest→must）
	var prod models.InsightSQLStandard
	if err := db.Where("env_type = ?", "prod").Take(&prod).Error; err != nil {
		t.Fatalf("prod 规范未被创建: %v", err)
	}
	var prodAffected models.InsightSQLStandardRule
	if err := db.Where("standard_id = ? AND rule_key = ?", prod.ID,
		"postgres.query_update.limit_update_delete_affected_rows").Take(&prodAffected).Error; err != nil {
		t.Fatalf("prod 规范缺影响行数规则: %v", err)
	}

	// 金仓镜像行存在
	var kbCount int64
	db.Model(&models.InsightSQLStandardRule{}).Where("standard_id = ? AND db_type = ?", dev.ID, "kingbase").Count(&kbCount)
	if kbCount == 0 {
		t.Error("存量 dev 规范未补到 kingbase 段")
	}

	// 旧规则级别保住
	var pkRule models.InsightSQLStandardRule
	if err := db.Where("standard_id = ? AND rule_key = ?", dev.ID, "mysql.struct.table_must_have_pk").Take(&pkRule).Error; err != nil {
		t.Fatalf("查询旧规则失败: %v", err)
	}
	if pkRule.Level != "off" {
		t.Errorf("已有规则级别被覆盖: %q", pkRule.Level)
	}

	// 二次初始化幂等
	if err := InitializeDefaultSQLStandards(db); err != nil {
		t.Fatalf("二次初始化失败: %v", err)
	}
	var total int64
	db.Model(&models.InsightSQLStandardRule{}).Count(&total)
	var want int64 = int64(2 * len(defaultStandardRules()))
	if total != want {
		t.Errorf("二次初始化后总行数 = %d, 期望 %d（幂等被破坏）", total, want)
	}
}

// 防止 catalog 里出现重复 rule_key（对账用 map 去重，目录重复会静默漏补）。
func TestCatalogRuleKeysUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range defaultStandardRules() {
		if seen[r.RuleKey] {
			t.Errorf("目录中 rule_key 重复: %s", r.RuleKey)
		}
		seen[r.RuleKey] = true
	}
	if len(seen) == 0 {
		t.Fatal("目录为空")
	}
}

// params 落库前保持合法 JSON（sqlite json1 不可用时人工校验）。
func TestCatalogParamsValidJSON(t *testing.T) {
	for _, r := range defaultStandardRules() {
		if len(r.Params) > 0 && !json.Valid(r.Params) {
			t.Errorf("%s 的 params 非法 JSON: %s", r.RuleKey, r.Params)
		}
	}
}
