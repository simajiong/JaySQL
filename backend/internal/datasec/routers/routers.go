package routers

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/datasec/views"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/middleware"
)

func Routers(r *gin.Engine) {
	api := r.Group("/api/v1/datasec")
	api.Use(global.App.JWT.MiddlewareFunc())

	// 脱敏规则
	api.GET("/rules", middleware.RequirePermission("datasec.rule.view"), views.ListRulesView)
	api.POST("/rules", middleware.RequirePermission("datasec.rule.manage"), views.CreateRuleView)
	api.PUT("/rules/:id", middleware.RequirePermission("datasec.rule.manage"), views.UpdateRuleView)
	api.DELETE("/rules/:id", middleware.RequirePermission("datasec.rule.manage"), views.DeleteRuleView)

	// 列配置（库/表/列 → 规则绑定）
	api.GET("/columns", middleware.RequirePermission("datasec.rule.view"), views.ListColumnsView)
	api.POST("/columns", middleware.RequirePermission("datasec.rule.manage"), views.SaveColumnView)
	api.POST("/columns/:id/status", middleware.RequirePermission("datasec.rule.manage"), views.UpdateColumnStatusView)
	api.DELETE("/columns/:id", middleware.RequirePermission("datasec.rule.manage"), views.DeleteColumnView)

	// 库/表/列级联选项：读元数据 + 连实例只读表结构，不按数据授权过滤
	// （配置面取数，见 services/options.go 顶部说明）。
	// 权限用「任一」：配规则的看 datasec.rule.view，只申请明文的成员看 datasec.plain.request，
	// 两个弹窗都要用这组接口，缺一个就会让另一类用户拿到 403。
	api.GET("/schema-options", middleware.RequireAnyPermission("datasec.rule.view", "datasec.plain.request"), views.ListSchemaOptionsView)
	api.GET("/table-options", middleware.RequireAnyPermission("datasec.rule.view", "datasec.plain.request"), views.ListTableOptionsView)
	api.GET("/column-options", middleware.RequireAnyPermission("datasec.rule.view", "datasec.plain.request"), views.ListColumnOptionsView)

	// 敏感列自动发现（按需抽样扫描）
	api.POST("/scan", middleware.RequirePermission("datasec.rule.manage"), views.ScanTableView)

	// 明文访问工单
	api.GET("/grants", middleware.RequirePermission("datasec.plain.request"), views.ListGrantsView)
	api.POST("/grants", middleware.RequirePermission("datasec.plain.request"), views.CreateGrantView)
	api.POST("/grants/:id/decide", middleware.RequirePermission("datasec.plain.approve"), views.DecideGrantView)
}
