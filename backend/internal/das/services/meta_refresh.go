package services

// 「刷新库列表」入口的服务层。
//
// 为什么要有它
//   元数据快照默认由三条路径自动维护（写后失效 / 读时惰性 / 定时对账），
//   但总会有「我就是想现在看到」的时刻：DBA 刚用客户端建了个库，
//   或者同步失败后想重试一次。管理员侧的 sync-meta 接口要求
//   system.instance.sync_meta 权限，工单提交人、申请人、普通查询用户都点不到，
//   所以这里补一个只做「重新拉一次库清单」的轻量入口。
//
// 安全边界
//   它不返回任何数据内容，只返回同步状态（上次成功时间 / 是否同步中 / 失败原因），
//   真正的数据可见性仍由 insight_das_data_grants 决定。因此这里不做授权过滤
//   ——否则「申请权限之前」的页面上就会点不动刷新按钮，而这恰恰是最需要它的场景。
//   代价是任何持有 sql.console.access 的用户都能触发一次只读元数据采集，
//   用「每实例 10 秒一次」的进程内节流把这个代价限制住。

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/simajiong/jaysql/internal/common/tasks"
	"github.com/simajiong/jaysql/internal/global"
)

const (
	// metaRefreshMinInterval 同一实例两次手动刷新之间的最小间隔。
	// 一次 SQLServer 同步要按库逐个连 sys.schemas，没有节流的话
	// 一个用户连点几下就能把目标库的连接数打上去。
	metaRefreshMinInterval = 10 * time.Second
)

// metaRefreshThrottle 进程内节流表：instance_id → 上次触发时间。
// 多实例部署下不跨进程共享，但本接口是「用户主动点一下」的低频操作，
// 单进程节流已经足够挡住连点，不值得为它引入一次 Redis/DB 往返。
var metaRefreshThrottle sync.Map

// InstanceMetaStatusService 读取某实例的元数据同步状态（只读，不触发刷新）。
type InstanceMetaStatusService struct {
	InstanceID string
}

func (s *InstanceMetaStatusService) Run() (tasks.MetaFreshness, error) {
	instanceID, err := normalizeMetaInstanceID(s.InstanceID)
	if err != nil {
		return tasks.MetaFreshness{}, err
	}
	return tasks.ReadMetaFreshness(instanceID), nil
}

// RefreshInstanceMetaService 手动刷新某实例的库清单（同步执行，等结果）。
type RefreshInstanceMetaService struct {
	InstanceID string
	Username   string
}

func (s *RefreshInstanceMetaService) Run() (tasks.MetaFreshness, error) {
	instanceID, err := normalizeMetaInstanceID(s.InstanceID)
	if err != nil {
		return tasks.MetaFreshness{}, err
	}
	if !allowInstanceMetaRefresh(instanceID) {
		return tasks.ReadMetaFreshness(instanceID), fmt.Errorf("刷新过于频繁，请 %d 秒后再试", int(metaRefreshMinInterval.Seconds()))
	}
	global.App.Log.Info(fmt.Sprintf("用户 %s 手动刷新实例 %s 的库清单", strings.TrimSpace(s.Username), instanceID))
	return tasks.RefreshInstanceMeta(instanceID)
}

// normalizeMetaInstanceID 校验实例存在，并返回规整后的 instance_id。
//
// 不在这里做数据授权校验，原因见文件头注释：申请入口面向的正是还没有授权的用户。
func normalizeMetaInstanceID(raw string) (string, error) {
	instanceID := strings.TrimSpace(raw)
	if instanceID == "" {
		return "", fmt.Errorf("数据源ID不能为空")
	}
	var count int64
	if err := global.App.DB.Table("insight_instances").
		Where("instance_id = ?", instanceID).
		Count(&count).Error; err != nil {
		return "", fmt.Errorf("查询数据源失败")
	}
	if count == 0 {
		return "", fmt.Errorf("数据源不存在或已删除")
	}
	return instanceID, nil
}

// allowInstanceMetaRefresh 进程内节流：同一实例在窗口内只放行一次。
func allowInstanceMetaRefresh(instanceID string) bool {
	now := time.Now()
	if last, ok := metaRefreshThrottle.Load(instanceID); ok {
		if lastAt, valid := last.(time.Time); valid && now.Sub(lastAt) < metaRefreshMinInterval {
			return false
		}
	}
	metaRefreshThrottle.Store(instanceID, now)
	return true
}
