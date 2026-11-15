package checker

// 「UPDATE/DELETE 单语句影响行数」（limit_update_delete_affected_rows）的 PG 引擎单测。
//
// 此前该规则的 COUNT(*) 直连 c.db、从未被单测覆盖（真实验证只能靠 LIVE_PG_MATRIX=1 的
// 实连矩阵，本地无 PG 时永远 SKIP）。现在 COUNT 走 pgMetaResolver 桩接口（与 Oracle/
// SQLServer 的 CountWhere 同构），用桩注入行数即可覆盖关键路径：
//
// 超限命中 / 阈值内不命中 / 连库失败静默跳过（不误报）/ 无 WHERE 不触发 / 规则关闭不触发。

import (
	"errors"
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
)

func pgAffectedRowsParams(max int) config.InspectParams {
	p := config.DefaultInspectParams()
	p.MAX_AFFECTED_ROWS = max
	return p
}

// pgAffectedRowsMessages 取影响行数规则的提示文本（未命中返回空）
func pgAffectedRowsMessages(t *testing.T, data []ReturnData) []string {
	t.Helper()
	var msgs []string
	for _, row := range data {
		for _, sum := range row.Summary {
			if sum.RuleKey == "limit_update_delete_affected_rows" {
				msgs = append(msgs, sum.Message)
			}
		}
	}
	return msgs
}

// TestPGAffectedRowsHit 超限命中：桩返回 100 行、阈值 10 → 产出规则项
func TestPGAffectedRowsHit(t *testing.T) {
	meta := newStubPGMeta()
	meta.countValue = 100

	msgs := pgAffectedRowsMessages(t, runPGWithMeta(t, `UPDATE t_user SET status = 2 WHERE status = 0;`, pgAffectedRowsParams(10), meta))

	if len(msgs) == 0 {
		t.Fatal("影响行数 100 超过阈值 10 应命中 limit_update_delete_affected_rows，实际无输出")
	}
	if !strings.Contains(msgs[0], "100") {
		t.Errorf("提示应包含实际行数 100，实际: %s", msgs[0])
	}
	// 下推校验：表名与 WHERE 原文必须原样传给 COUNT
	if len(meta.countCalls) != 1 {
		t.Fatalf("COUNT 应被调用 1 次，实际 %d 次", len(meta.countCalls))
	}
	call := meta.countCalls[0]
	if call.table != "t_user" {
		t.Errorf("COUNT 表名应为 t_user，实际 %q", call.table)
	}
	if !strings.Contains(call.where, "status = 0") {
		t.Errorf("COUNT WHERE 原文应包含 status = 0，实际 %q", call.where)
	}
}

// TestPGAffectedRowsWithinLimit 阈值内不命中
func TestPGAffectedRowsWithinLimit(t *testing.T) {
	meta := newStubPGMeta()
	meta.countValue = 8

	if msgs := pgAffectedRowsMessages(t, runPGWithMeta(t, `UPDATE t_user SET status = 2 WHERE status = 0;`, pgAffectedRowsParams(10), meta)); len(msgs) != 0 {
		t.Errorf("影响行数 8 未超阈值 10 不应命中，实际: %v", msgs)
	}
}

// TestPGAffectedRowsDBErrorSkip 连库失败静默跳过：宁可漏报、绝不误报
func TestPGAffectedRowsDBErrorSkip(t *testing.T) {
	meta := newStubPGMeta()
	meta.countValue = 100
	meta.countErr = errors.New("连接失败")

	if msgs := pgAffectedRowsMessages(t, runPGWithMeta(t, `UPDATE t_user SET status = 2 WHERE status = 0;`, pgAffectedRowsParams(10), meta)); len(msgs) != 0 {
		t.Errorf("COUNT 失败应跳过（不误报），实际: %v", msgs)
	}
}

// TestPGAffectedRowsNoWhere 无 WHERE 不触发（由「必须包含 WHERE」规则单独拦截）
func TestPGAffectedRowsNoWhere(t *testing.T) {
	meta := newStubPGMeta()
	meta.countValue = 100

	runPGWithMeta(t, `UPDATE t_user SET status = 2;`, pgAffectedRowsParams(10), meta)

	if len(meta.countCalls) != 0 {
		t.Errorf("无 WHERE 时不应发起 COUNT，实际调用 %d 次", len(meta.countCalls))
	}
}

// TestPGAffectedRowsDisabled 规则关闭（MAX_AFFECTED_ROWS=0）不触发
func TestPGAffectedRowsDisabled(t *testing.T) {
	meta := newStubPGMeta()
	meta.countValue = 100

	runPGWithMeta(t, `UPDATE t_user SET status = 2 WHERE status = 0;`, pgAffectedRowsParams(0), meta)

	if len(meta.countCalls) != 0 {
		t.Errorf("规则关闭时不应发起 COUNT，实际调用 %d 次", len(meta.countCalls))
	}
}

// TestPGAffectedRowsDelete DELETE 同样覆盖
func TestPGAffectedRowsDelete(t *testing.T) {
	meta := newStubPGMeta()
	meta.countValue = 500

	msgs := pgAffectedRowsMessages(t, runPGWithMeta(t, `DELETE FROM t_user WHERE created_at < '2020-01-01';`, pgAffectedRowsParams(100), meta))

	if len(msgs) == 0 {
		t.Fatal("DELETE 超限应命中规则")
	}
	if !strings.Contains(strings.ToUpper(msgs[0]), "DELETE") {
		t.Errorf("提示应标识 DELETE 类型，实际: %s", msgs[0])
	}
}
