package views

import (
	"github.com/simajiong/jaysql/internal/das/services"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/gin-gonic/gin"
)

// GetInstanceMetaStatusView 读取某数据源的库清单同步状态。
//
// 前端在「库/模式」下拉旁边用它决定提示语：
// syncing → 「同步中」、last_status=never → 「尚未同步」、
// failed → 「同步失败：<原因>」、其余按 last_synced_at 显示时间。
func GetInstanceMetaStatusView(c *gin.Context) {
	service := services.InstanceMetaStatusService{InstanceID: c.Param("id")}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// RefreshInstanceMetaView 手动刷新某数据源的库清单。
//
// 同步执行并返回最新状态：用户点了按钮，就该看到明确的成功或失败，
// 而不是再靠刷新页面去猜。单实例节流在服务层（每 10 秒一次）。
func RefreshInstanceMetaView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	service := services.RefreshInstanceMetaService{
		InstanceID: c.Param("id"),
		Username:   username,
	}
	data, err := service.Run()
	if err != nil {
		// 失败也把状态带出去：前端据此展示「上次同步失败：xxx」，
		// 而不是只弹一个 toast 就把原因丢掉。
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "刷新成功")
}
