package bootstrap

import (
	"time"

	backupServices "github.com/simajiong/jaysql/internal/backup/services"
	"github.com/simajiong/jaysql/internal/global"

	commonServices "github.com/simajiong/jaysql/internal/common/services"
	commonTasks "github.com/simajiong/jaysql/internal/common/tasks"
	dasTasks "github.com/simajiong/jaysql/internal/das/tasks"
	monitorServices "github.com/simajiong/jaysql/internal/monitor/services"
	ordersTasks "github.com/simajiong/jaysql/internal/orders/tasks"
	performanceServices "github.com/simajiong/jaysql/internal/performance/services"

	"github.com/robfig/cron/v3"
)

func InitializeCron() {
	global.App.Cron = cron.New()

	go func() {
		// 库/schema 元数据全量对账。默认 */30，只负责「平台无感知的变更」
		// （DBA 直连客户端建库）。平台内建库走写后失效 + 读时惰性，秒级可见，
		// 见 common/tasks/meta_refresh.go 的文件头说明。
		_, err := global.App.Cron.AddFunc(global.App.Config.Crontab.SyncDBMetas, func() {
			global.App.Log.Info("Run SyncDBMeta At:", time.Now())
			commonTasks.SyncDBMeta()
		})
		if err != nil {
			global.App.Log.Error(err)
		}

		_, err = global.App.Cron.AddFunc("*/5 * * * *", func() {
			global.App.Log.Info("Run KillTiDBQuery At:", time.Now())
			kill := dasTasks.KillTiDBQuery{}
			kill.Run()
		})
		if err != nil {
			global.App.Log.Error(err)
		}

		// 细粒度数据授权：把已过失效时间但仍标记 active 的授权收敛为 expired，
		// 保证列表口径与判定口径一致。
		dasTasks.RegisterDataGrantCronJobs()

		backupServices.RegisterBackupTaskCronJobs()
		performanceServices.RegisterSlowLogCollectCronJobs()
		performanceServices.RegisterSlowLogMaintenanceCronJobs()
		// 实例连通性探活：此前只在实例页面的浏览器定时器里跑，没人开页面就不探测，
		// 而首页「实例连接异常」风险口径直接读 last_check_status —— 必须后台常驻。
		commonServices.RegisterInstanceProbeCronJobs()
		// 工单「定时执行」调度：扫描 execution_mode=SCHEDULED 且已到期的工单并触发执行。
		// 没有这个任务，弹窗里的「定时执行」就只是落库一行计划、永远不会被执行。
		ordersTasks.RegisterOrderCronJobs()
		// 监控采集/告警评估/数据卫生三个后台任务。
		// 这是本次改造的根：此前 monitor 全平台没有任何定时任务，采集挂在页面请求上，
		// 结果是"没人打开页面就没有数据、没有告警"。
		monitorServices.RegisterMonitorCronJobs()
		global.App.Cron.Start()
		defer global.App.Cron.Stop()
		select {}
	}()
}
