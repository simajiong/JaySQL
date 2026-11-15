package services

import (
	"testing"

	orderScopes "github.com/simajiong/jaysql/internal/orders/scopes"
)

// 工单链路「数据库类型支持范围」守门测试。
//
// 历史 bug（2026-11-11）：同一份支持清单被抄了三份——工单列表白名单
// （scopes.SupportedOrderDbTypes）、数据源下拉（commit.go）、审核/执行判定
// （db_support.go）。给金仓接入时只改了最后一份，结果：
//   - 新建工单页「数据源」选 Kingbase 后，实例下拉显示「暂无数据」；
//   - 建好的金仓工单在列表页查不到。
//
// 现在三处统一引用 scopes.SupportedOrderDbTypes，本测试把清单内容与
// 「金仓必须被支持」锁死，避免再次漏配。
func TestOrderSupportedDbTypesIncludeKingbase(t *testing.T) {
	required := []string{"MySQL", "PostgreSQL", "Kingbase", "Oracle", "TiDB", "Doris", "SQLServer"}
	for _, db := range required {
		if !orderScopes.SupportsOrderDbType(db) {
			t.Errorf("工单支持清单缺少 %q：该类型的实例会在数据源下拉里选不到、建好的工单也会从列表消失", db)
		}
	}

	if err := validateOrderSQLTypeSupport("Kingbase", "DDL"); err != nil {
		t.Errorf("validateOrderSQLTypeSupport(Kingbase, DDL) 返回错误：%v", err)
	}
	if err := validateOrderSQLTypeSupport("Kingbase", "DML"); err != nil {
		t.Errorf("validateOrderSQLTypeSupport(Kingbase, DML) 返回错误：%v", err)
	}
	if !supportsDeepInspect("Kingbase") {
		t.Error("supportsDeepInspect(Kingbase) = false：金仓会跳过规范预审")
	}

	// 反向：不支持的类型必须继续被拦（白名单不能被放宽成「全放行」）
	if err := validateOrderSQLTypeSupport("MongoDB", "DDL"); err == nil {
		t.Error("validateOrderSQLTypeSupport(MongoDB, DDL) 应该返回错误")
	}
	if supportsDeepInspect("MongoDB") {
		t.Error("supportsDeepInspect(MongoDB) 应该为 false")
	}
}

// TestKingbaseOrderIsPgLike 金仓必须走 PG 的「库/schema 两级」链路：
// 库字段与 schema 字段独立维护、连接目标取 database、系统库过滤按 PG 口径。
func TestKingbaseOrderIsPgLike(t *testing.T) {
	if !isPgLikeDB("Kingbase") || !isPgLikeDB("KingbaseES") {
		t.Fatal("isPgLikeDB 未包含金仓：库/schema 拆分、连接目标解析都会走错分支")
	}
	connDB, inspectSchema := inspectConnTarget("Kingbase", "test", "test.public")
	if connDB != "test" || inspectSchema != "public" {
		t.Errorf("inspectConnTarget(Kingbase) = (%q,%q)，期望 (test,public)", connDB, inspectSchema)
	}
	if tidbParserCompatibleDB("Kingbase") {
		t.Error("金仓不能被当成 TiDB parser 兼容类型：PG 语法会被误判为解析错误")
	}
}
