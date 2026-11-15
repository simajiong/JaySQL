// Package scopes 收敛工单列表「scope（作用域）」的 WHERE 判定表达式。
//
// 背景：同一套作用域过滤条件目前被两处各自拼装——
//   - 工单列表页：internal/orders/services/list.go 的 GetOrderListServices.Run
//   - 首页「我的资源 · SQL工单」卡：internal/common/services/home_dashboard.go 的
//     orderListAlignedBaseQuery
//
// 两份实现必须逐字符一致，否则会出现「首页卡片 N 条 / 点进去列表 M 条」的口径分裂。
// 之所以不把判定放进 internal/orders/services：该包下的 operate.go 反向依赖
// internal/common/services，而 common/services 需要复用本判定，放回去会形成 import 循环。
// 因此独立成只依赖标准库的叶子包，两边共同引用。
package scopes

import "strings"

// Scope 是工单列表作用域字符串。
type Scope = string

// 工单列表支持的作用域标识。
const (
	// ScopeAll 全量视图。仅有 order.manage 权限或超级管理员能看到真正的全量；
	// 其余用户在后端退化为「与我相关」，因此前端不应向这类用户暴露该选项（见 #5 修复）。
	ScopeAll Scope = "ALL"
	// ScopeMyRelated 「与我相关」：提交、执行、被抄送、参与审批，或历史上操作过该工单。
	ScopeMyRelated Scope = "MY_RELATED"
	// ScopeMySubmitted 我提交的工单。
	ScopeMySubmitted Scope = "MY_SUBMITTED"
	// ScopeMyCC 抄送给我的工单。
	ScopeMyCC Scope = "MY_CC"
	// ScopePendingMyApproval 待我审批。
	ScopePendingMyApproval Scope = "PENDING_MY_APPROVAL"
	// ScopeMyApproved 我已审批（通过或驳回）。
	ScopeMyApproved Scope = "MY_APPROVED"
	// ScopePendingMyExecution 待我执行。
	ScopePendingMyExecution Scope = "PENDING_MY_EXECUTION"
	// ScopeMyExecuted 我已执行（成功或失败）。
	ScopeMyExecuted Scope = "MY_EXECUTED"
)

// RelatedConditionSQL 「与我相关」的 WHERE 片段。
//
// 判定为四类角色的并集（天然去重，禁止分角色相加计数）：
//  1. 提交者 a.applicant
//  2. 执行者 a.executor
//  3. 抄送人 a.cc（JSON 数组）
//  4. 审批相关人 insight_approval_records.approver
//
// 外加第五条兜底：历史操作人 insight_order_logs.username。
// 兜底原因：第 1~4 条的字段是「当前值」，一旦被改写，用户就会从「与我相关」里凭空消失。
// 典型场景——
//   - 审批人 A 把审批转交给 B（operate.go 转交动作）后，approval_records.approver 变成 B，A 不再命中第 4 条；
//   - 工单被驳回后申请人编辑内容重新提交，审批人被重新指派，原审批人不再命中第 4 条。
//
// 这两个场景下 A 确实参与过该工单，只是「当前值」里已经没有他。操作日志是只追加的，
// 用 l2.username 兜底可让这类工单稳定留在 A 的「与我相关」列表里，不再忽隐忽现。
//
// 注意：此处刻意不使用 l2.msg 做关键词匹配（日志文案会变），只认「谁操作过」这个事实。
const RelatedConditionSQL = `(
	a.applicant = ?
	OR a.executor = ?
	OR JSON_CONTAINS(IFNULL(a.cc, JSON_ARRAY()), JSON_QUOTE(?), '$')
	OR EXISTS (
		SELECT 1 FROM insight_approval_records ar2
		WHERE ar2.order_id = a.order_id AND ar2.approver = ?
	)
	OR EXISTS (
		SELECT 1 FROM insight_order_logs l2
		WHERE l2.order_id = a.order_id AND l2.username = ?
	)
)`

// RelatedCondition 返回「与我相关」的 WHERE 条件与对应参数。
// 参数按占位符顺序共 5 个，均为同一 username。
//
// username 为空时返回恒假条件：没有身份就谈不上「与我相关」，
// 同时避免空串恰好命中 insight_order_logs 中 username 为空的脏数据行而放大可见范围。
func RelatedCondition(username string) (string, []any) {
	u := strings.TrimSpace(username)
	if u == "" {
		return "1 = 0", nil
	}
	return RelatedConditionSQL, []any{u, u, u, u, u}
}

// SupportedOrderDbTypes 工单列表展示的库类型白名单 = 工单执行链支持的范围。
//
// 列表页（list.go）与首页「我的资源 · SQL工单」卡（home_dashboard.go）共用，
// 两处口径必须一致——否则会出现「首页卡片 N 条 / 列表 M 条」的分裂。
// 历史 bug：白名单曾硬编码 MySQL/PostgreSQL，Oracle/TiDB/Doris 工单能创建、
// 能在详情页看到，却在列表里凭空消失。
// 2026-11-11 补 Kingbase / SQLServer / MSSQL：金仓与 SQL Server 早已在
// validateOrderSQLTypeSupport 与工单数据源下拉里放行，但这张白名单没跟上，
// 结果是「能建单、能执行，列表页查不到」以及「数据源下拉里选不到实例」。
// 现在候选下拉（commit.go）也直接引用本变量，避免第三份拷贝再漂移。
var SupportedOrderDbTypes = []string{"MySQL", "PostgreSQL", "Kingbase", "Oracle", "TiDB", "Doris", "SQLServer", "MSSQL"}

// SupportsOrderDbType 判断给定 db_type 是否在工单支持范围内。
// 供审核/预审/执行链复用，避免在多处各写一份 switch（历史 bug：三份清单先后漏配）。
func SupportsOrderDbType(dbType string) bool {
	target := strings.TrimSpace(dbType)
	for _, item := range SupportedOrderDbTypes {
		if strings.EqualFold(item, target) {
			return true
		}
	}
	return false
}
