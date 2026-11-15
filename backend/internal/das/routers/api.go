package routers

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/das/views"
	"github.com/simajiong/jaysql/middleware"
)

func RegisterDasRoutes(das *gin.RouterGroup) {
	das.GET("environments", middleware.RequirePermission("sql.console.access"), views.GetEnvironmentsView)
	das.GET("instances", middleware.RequirePermission("sql.console.access"), views.GetInstancesView)
	// 库清单同步状态 / 手动刷新。
	// 刷新动作本身不返回任何数据内容，只触发一次只读元数据采集。
	// 权限放得比「浏览库表」宽是有意的：工单提交人（order.submit）与
	// 权限申请人（das.access.apply）都需要「刚建的库马上能选到」，
	// 而他们往往没有 sql.console.access —— 只卡死在这一个权限点上，
	// 刷新按钮对他们就是个摆设。数据可见性仍由授权表决定，与此无关。
	metaAccess := middleware.RequireAnyPermission("sql.console.access", "order.submit", "das.access.apply", "system.das.view")
	das.GET("instances/:id/meta-status", metaAccess, views.GetInstanceMetaStatusView)
	das.POST("instances/:id/refresh-meta", metaAccess, views.RefreshInstanceMetaView)
	das.GET("resources", middleware.RequirePermission("sql.console.access"), views.GetResourcesView)
	das.GET("schemas", middleware.RequirePermission("sql.console.access"), views.GetSchemasView)
	das.GET("schema/tables", middleware.RequirePermission("sql.console.access"), views.GetTablesView)
	das.GET("schema/grants", middleware.RequirePermission("sql.console.access"), views.GetUserGrantsView)
	das.POST("query/mysql", middleware.RequirePermission("sql.console.execute"), views.ExecuteMySQLQueryView)
	das.POST("query/mysql/explain", middleware.RequirePermission("sql.console.execute"), views.ExplainMySQLQueryView)
	das.POST("query/clickhouse", middleware.RequirePermission("sql.console.execute"), views.ExecuteClickHouseQueryView)
	das.POST("query/postgresql", middleware.RequirePermission("sql.console.execute"), views.ExecutePostgresQueryView)
	das.POST("query/postgresql/explain", middleware.RequirePermission("sql.console.execute"), views.ExplainPostgresQueryView)
	das.POST("query/sqlserver", middleware.RequirePermission("sql.console.execute"), views.ExecuteSQLServerQueryView)
	das.POST("query/oracle", middleware.RequirePermission("sql.console.execute"), views.ExecuteOracleQueryView)
	das.GET("table-info", middleware.RequirePermission("sql.console.access"), views.GetTableInfoView)
	das.GET("schema-browser/children", middleware.RequirePermission("sql.console.access"), views.GetSchemaBrowserChildrenView)
	das.GET("dbdict", middleware.RequirePermission("sql.console.access"), views.GetDbDictView)
	das.GET("history", middleware.RequirePermission("sql.history.view"), views.GetHistoryView)
	das.GET("favorites", middleware.RequirePermission("sql.favorite.view"), views.GetFavoritesView)
	das.POST("favorites", middleware.RequirePermission("sql.favorite.manage"), views.CreateFavoritesView)
	das.PUT("favorites/:id", middleware.RequirePermission("sql.favorite.manage"), views.UpdateFavoritesView)
	das.DELETE("favorites/:id", middleware.RequirePermission("sql.favorite.manage"), views.DeleteFavoritesView)

	das.GET("objects/flat-index", middleware.RequirePermission("sql.console.access"), views.GetObjectsFlatIndexView)
	das.GET("objects/search", middleware.RequirePermission("sql.console.access"), views.SearchObjectsView)
	das.POST("objects/search-audit", middleware.RequirePermission("sql.console.access"), views.ReportObjectSearchAuditView)

	// 数据授权自助：持有「数据访问」管理权限的用户可为自己发起并立即生效，
	// 全程写 insight_das_grant_audit 流水。主体在服务层被强制改写为本人。
	das.GET("data-grants/mine", middleware.RequirePermission("system.das.manage"), views.GetMyDataGrantsView)
	das.POST("data-grants/self", middleware.RequirePermission("system.das.manage"), views.CreateSelfDataGrantView)

	// 数据访问权限申请：申请人直选「数据源 + 库/表」，提交后进入审批流。
	// 审批通过后进入「待执行授权」，由执行人执行授权才在事务内写授权表
	// （insight_das_data_grants），全程不依赖 SQL 工单。
	//
	// 只受理查询权限：变更(DML/DDL)与导出(EXPORT)由 SQL 工单自身的审批流程覆盖，
	// 不在这里重复申请（见 services/access_action_scope.go）。
	//
	// 列表/详情用 RequireAnyPermission：申请人、审批人、审计视角三种角色
	// 走的是同一个读接口，具体数据范围在视图层按权限点收敛。
	accessRead := middleware.RequireAnyPermission("das.access.apply", "das.access.approve", "das.access.view")
	das.GET("access-requests", accessRead, views.ListAccessRequestsView)
	das.GET("access-requests/:request_no", accessRead, views.GetAccessRequestDetailView)
	das.POST("access-requests", middleware.RequirePermission("das.access.apply"), views.CreateAccessRequestView)
	das.POST("access-requests/:request_no/approve", middleware.RequirePermission("das.access.approve"), views.ApproveAccessRequestView)
	das.POST("access-requests/:request_no/reject", middleware.RequirePermission("das.access.approve"), views.RejectAccessRequestView)
	das.POST("access-requests/:request_no/forward", middleware.RequirePermission("das.access.approve"), views.ForwardAccessRequestView)
	das.POST("access-requests/:request_no/grant", middleware.RequirePermission("das.access.approve"), views.GrantAccessRequestView)
	das.POST("access-requests/:request_no/withdraw", middleware.RequirePermission("das.access.apply"), views.WithdrawAccessRequestView)
	// 终止：申请人（apply）或管理员（approve 兜住），归属判定在服务层
	das.POST("access-requests/:request_no/terminate", accessRead, views.TerminateAccessRequestView)

	// 审批人候选：申请页选人 / 详情页转交共用
	das.GET("access-approver-options", middleware.RequirePermission("das.access.apply"), views.ListAccessApproverOptionsView)

	// 申请入口取数：申请人是「还没有权限」的用户，不能按授权过滤，
	// 三个接口只做最小暴露（脱敏元数据 / 静态库清单 / 服务端表名清单）
	das.GET("access-instance-options", middleware.RequirePermission("das.access.apply"), views.ListAccessInstanceOptionsView)
	das.GET("access-schema-options", middleware.RequirePermission("das.access.apply"), views.ListAccessSchemaOptionsView)
	das.GET("access-table-options", middleware.RequirePermission("das.access.apply"), views.ListAccessTableOptionsView)
}
