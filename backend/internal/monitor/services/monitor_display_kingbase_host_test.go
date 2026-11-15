package services

import "testing"

// 回归：金仓实例的监控明细只剩「数据库指标」，没有「基础指标」。
//
// 根因有两层，这个测试锁后端这一层：
//  1. 金仓的展示目录整目录复用 PostgreSQL（host + engine 两个 Tab），
//     `displayLayoutCatalogItems("kingbase")` 必须真的吐出 host 层指标 ——
//     模板勾选弹窗（layout=1）拿到的就是这个清单；漏了主机层，用户在模板里
//     根本勾不到 res.*，明细页的「基础指标」Tab 随后被模板门控整页剪掉。
//  2. 前端 ConfigView.vue 的 TEMPLATE_HOST_DB_TYPES / LAYOUT_ENGINE_PICKER
//     也必须包含 kingbase，两边口径必须一致（后端这里守住底线）。
func TestDisplayLayoutCatalogItemsCoversKingbaseHostLayer(t *testing.T) {
	items := displayLayoutCatalogItems("kingbase")
	if len(items) == 0 {
		t.Fatal("金仓应有展示目录指标清单，实际为空")
	}

	var host, engine int
	var hasResCPU bool
	for _, item := range items {
		switch item.Layer {
		case LayerHost:
			host++
			if item.MetricKey == MetricResCPU {
				hasResCPU = true
			}
		case LayerEngine:
			engine++
		}
	}
	if host == 0 {
		t.Fatal("金仓展示目录必须包含主机层（基础指标）指标，实际 0 项")
	}
	if !hasResCPU {
		t.Fatalf("金仓主机层应包含 %s（CPU 使用率）", MetricResCPU)
	}
	if engine == 0 {
		t.Fatal("金仓展示目录必须包含数据库层指标，实际 0 项")
	}

	// 与 PostgreSQL 同源同口径：金仓清单的行数应和 PG 完全一致（整目录复用）。
	pg := displayLayoutCatalogItems("postgresql")
	if len(pg) != len(items) {
		t.Fatalf("金仓应整目录复用 PostgreSQL：kingbase=%d 项，postgresql=%d 项", len(items), len(pg))
	}
}

// 反向确认：hostMetricsInTemplate 认 kingbase，模板门控与勾选界面才不会各说各话。
func TestKingbaseJoinsHostTemplateGovernance(t *testing.T) {
	for _, engine := range []string{"kingbase", "Kingbase", "kingbasees"} {
		if !hostMetricsInTemplate(engine) {
			t.Fatalf("%q 应纳入主机层模板治理", engine)
		}
	}
}
