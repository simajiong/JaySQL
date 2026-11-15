package models

type BuiltinRoleDefinition struct {
	Code            string
	Name            string
	Description     string
	PermissionCodes []string
	IsSuperuser     bool
}

const (
	BuiltinRoleCodeMember         = "member"
	BuiltinRoleCodeAdmin          = "admin"
	BuiltinRoleCodeGlobalReadonly = "global_readonly"
)

var builtinRoleDefinitions = []BuiltinRoleDefinition{
	{
		Code:        BuiltinRoleCodeMember,
		Name:        "普通成员",
		Description: "日常使用者：拥有 SQL 查询页面访问权限（具体实例需单独授权）、新建工单权限（审批和执行需额外授权），可查看自己提交、被抄送、自己审批、自己执行的工单，可为自己发起数据访问权限申请。",
		PermissionCodes: []string{
			"sql.console.access",
			"sql.console.execute",
			"sql.favorite.view",
			"sql.favorite.manage",
			"sql.history.view",
			"order.view",
			"order.submit",
			// 数据访问申请：普通成员是这套流程的起点用户，没有 apply 就无法发起申请
			"das.access.apply",
			// 数据源管理（2026-10-25）：普通成员默认可查看数据源与参数。
			// 注意 member 是「可编辑内置角色」：此默认集只写进新建库，存量库不回填
			// （尊重管理员在 UI 上对 member 权限集的调整，也符合「存量不补授权」的数据兼容口径）。
			"datasource.instance.view",
			"datasource.param.view",
			// 数据安全（脱敏）：普通成员默认可发起明文申请（审批需要另授）
			"datasec.plain.request",
		},
	},
	{
		Code:        BuiltinRoleCodeAdmin,
		Name:        "系统管理员",
		Description: "平台系统管理员：拥有所有功能和数据权限，负责用户、角色、数据源、工单、备份、监控、审计等所有模块的配置与管理。",
		PermissionCodes: []string{
			"system.access",
			"system.user.view",
			"system.user.manage",
			"system.role.view",
			"system.role.manage",
			"system.permission.view",
			"system.environment.view",
			"system.environment.manage",
			"system.instance.view",
			"system.instance.manage",
			"system.instance.test",
			"system.instance.sync_meta",
			"system.inspect.view",
			"system.inspect.manage",
			"system.das.view",
			"system.das.manage",
			"system.approval_flow.view",
			"system.approval_flow.manage",
			"sql.console.access",
			"sql.console.execute",
			"sql.favorite.view",
			"sql.favorite.manage",
			"sql.history.view",
			"order.view",
			"order.submit",
			"order.approve",
			"order.execute",
			"order.manage",
			// 数据访问申请：管理员既发起也审批，还要能查看全部申请记录
			"das.access.apply",
			"das.access.approve",
			"das.access.view",
			"audit.log.view",
			"audit.log.export",
			"audit.policy.view",
			"audit.policy.manage",
			"backup.task.view",
			"backup.task.manage",
			"backup.set.view",
			"backup.set.download",
			"backup.restore.view",
			"backup.restore.execute",
			"monitor.view",
			"monitor.manage",
			"performance.view",
			"performance.manage",
			"performance.param.modify",
			// 数据源管理（2026-10-25）：系统管理员默认拥有该组全部权限
			// （admin 是代码严格对齐的内置角色，启动时自动同步，存量库同样生效）。
			"datasource.instance.view",
			"datasource.instance.manage",
			"datasource.instance.ssh",
			"datasource.param.view",
			"datasource.param.manage",
			"datasource.compare.view",
			"system.ai.view",
			"system.ai.manage",
			"ai.chat.use",
			"system.feedback.view",
			"system.feedback.manage",
			"system.announcement.view",
			"system.announcement.manage",
			"system.hotarticle.view",
			"system.hotarticle.manage",
			// 数据安全（脱敏）：管理员全量
			"datasec.rule.view",
			"datasec.rule.manage",
			"datasec.plain.request",
			"datasec.plain.approve",
		},
		IsSuperuser: true,
	},
	{
		Code:        BuiltinRoleCodeGlobalReadonly,
		Name:        "全局只读",
		Description: "全局只读账号：拥有 SQL 查询页面访问权限（具体实例需单独授权），其他所有模块全局可读，不允许任何修改、写入、删除、执行、审批等写操作。",
		PermissionCodes: []string{
			"sql.console.access",
			"sql.console.execute",
			"sql.favorite.view",
			"sql.history.view",
			"order.view",
			"system.access",
			"system.user.view",
			"system.role.view",
			"system.permission.view",
			"system.environment.view",
			"system.instance.view",
			"system.inspect.view",
			"system.das.view",
			"system.approval_flow.view",
			// 数据访问申请：只读账号可审计查看，不能发起也不能审批
			"das.access.view",
			"audit.log.view",
			"audit.policy.view",
			"backup.task.view",
			"backup.set.view",
			"backup.restore.view",
			"monitor.view",
			"performance.view",
			"system.ai.view",
			"ai.chat.use",
			"system.feedback.view",
			"system.announcement.view",
			"system.hotarticle.view",
			// 数据安全（脱敏）：只读账号可看配置，不可改、不可审批
			"datasec.rule.view",
		},
	},
}

func BuiltinRoleDefinitions() []BuiltinRoleDefinition {
	result := make([]BuiltinRoleDefinition, 0, len(builtinRoleDefinitions))
	for _, item := range builtinRoleDefinitions {
		copied := item
		copied.PermissionCodes = append([]string(nil), item.PermissionCodes...)
		result = append(result, copied)
	}
	return result
}

func IsBuiltinRoleCode(code string) bool {
	for _, item := range builtinRoleDefinitions {
		if item.Code == code {
			return true
		}
	}
	return false
}
