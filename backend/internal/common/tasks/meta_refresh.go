package tasks

// 库/schema 元数据快照的刷新器。
//
// 为什么要它
//   改造前 insight_instance_schemas 只由 cron（默认 */5 * * * *）全量同步，
//   链路上没有任何「写后失效」或「读时校验」，于是新建库之后要在工单提交、
//   授权申请、资源树、已授权库列表里看到它，最坏得等满 5 分钟。
//   把刷新周期调小是错的方向——那是全局全量扫，实例越多越慢，而且仍然有延迟。
//
// 现在改成三条触发路径（本文件是它们共用的执行体）：
//   L1 写后失效：平台内建库 / 工单执行 / 实例保存成功后立刻异步刷新（秒级）
//   L2 读时惰性：读接口发现快照超过 TTL，异步补一次，本次仍返回旧快照
//   L3 定时对账：cron 降频兜底「平台完全没感知到的变更」（DBA 直连客户端建库）
//
// 三条路径都必须满足两个约束，否则会把页面拖垮：
//   1) 绝不阻塞请求：刷新一律丢到后台 goroutine，读接口只读现有快照；
//   2) 同一实例同一时刻只跑一次：SQLServer 一次同步要按库逐个开连接查
//      sys.schemas，四个页面同时打开就会变成四次全量同步。用 singleflight 去重。

import (
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// metaStateNever 从未同步过。与 failed 区分开：前者是「还没试」，
	// 后者是「试了但失败」，页面提示语完全不同。
	metaStateNever   = "never"
	metaStateSuccess = "success"
	metaStateFailed  = "failed"

	// defaultMetaRefreshTTL 读时惰性刷新的默认节流窗口。
	// 取 60s 的取舍：既能把「DBA 用客户端直接建库」这类平台无感知的变更
	// 收敛到一分钟内，又不会让高频访问的页面变成持续压测目标库。
	defaultMetaRefreshTTL = 60 * time.Second

	// metaSyncingStaleAfter 「同步中」标记的失效阈值。
	// 进程被 kill 时 syncing_since 会留在表里，超过这个时长就当脏标记忽略，
	// 否则该实例会被永久判定为「正在同步」而再也触发不了刷新。
	metaSyncingStaleAfter = 5 * time.Minute

	// metaStateErrorMaxLen 失败原因入库前的截断长度，与列宽保持一致。
	metaStateErrorMaxLen = 512
)

// metaRefreshGroup 按 instance_id 去重：同一实例并发触发只真正跑一次，
// 其余调用者共享同一次结果。
var metaRefreshGroup singleflight.Group

// MetaFreshness 描述某个实例快照的新鲜度，随读接口返回给前端。
//
// 前端据此决定提示语：
//   - syncing=true            → 「库列表同步中，稍后自动刷新」
//   - last_status=never        → 「尚未同步过，点刷新立即获取」
//   - last_status=failed       → 「同步失败：<last_error>」
//   - 其他                      → 正常展示列表
type MetaFreshness struct {
	InstanceID   string     `json:"instance_id"`
	LastSyncedAt *time.Time `json:"last_synced_at"`
	LastStatus   string     `json:"last_status"`
	LastError    string     `json:"last_error,omitempty"`
	Syncing      bool       `json:"syncing"`
	Stale        bool       `json:"stale"`
	ResourceCnt  int        `json:"resource_count"`
}

// metaRefreshTTL 读配置里的惰性刷新窗口。
//
// 语义：
//   - 未配置（0）  → 默认 60s
//   - 负数          → 关闭读时惰性刷新（只保留写后失效 + 定时对账）
//   - 正数          → 按配置秒数执行
func metaRefreshTTL() time.Duration {
	seconds := global.App.Config.Crontab.MetaRefreshTTL
	switch {
	case seconds < 0:
		return 0
	case seconds == 0:
		return defaultMetaRefreshTTL
	default:
		return time.Duration(seconds) * time.Second
	}
}

// EnsureFresh 读路径的惰性刷新入口：快照超过 TTL 时**异步**补一次，
// 立即返回当前状态，绝不等待同步完成。
//
// 调用约定：任何读库里/schema 清单的接口，在查询 insight_instance_schemas
// 之前调一次即可；返回值可以直接带给前端做同步态提示。
// 它自身不会返回错误——刷新失败只影响下一次读数，不应让用户的列表查询失败。
func EnsureFresh(instanceID string) MetaFreshness {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return MetaFreshness{LastStatus: metaStateNever}
	}
	now := time.Now()
	state, found := readMetaState(instanceID)
	freshness := buildFreshness(instanceID, state, found, now)

	ttl := metaRefreshTTL()
	if ttl > 0 && needMetaRefresh(state, found, now, ttl) {
		RefreshInstanceMetaAsync(instanceID)
		// 返回 syncing=true 让页面立刻切到「同步中」，而不是等下一次请求才发现。
		freshness.Syncing = true
	}
	return freshness
}

// EnsureFreshOrGet 仅读取状态、不触发刷新。
// 用于那些「已经确定要展示旧快照」的场景（例如已授权库列表的分页第二页），
// 避免翻页过程中反复触发刷新。
func ReadMetaFreshness(instanceID string) MetaFreshness {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return MetaFreshness{LastStatus: metaStateNever}
	}
	now := time.Now()
	state, found := readMetaState(instanceID)
	return buildFreshness(instanceID, state, found, now)
}

// RefreshInstanceMetaAsync 异步触发一次刷新，忽略结果。
//
// 语义是「保证在这次调用之后至少有一次同步开始过」：
// 如果调用瞬间正好有一次同步在跑（写操作与后台刷新撞车），singleflight 会
// 让本次调用挂到那一次上——而那次可能读的是写之前的元数据。所以这里补一次
// 校验：若同步完成时间仍早于本次调用时间，再跑一轮。最多两轮，不做无限重试。
func RefreshInstanceMetaAsync(instanceID string) {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return
	}
	startedAt := time.Now()
	go func() {
		defer func() {
			// 后台 goroutine 里必须自己兜 panic：这里出问题不能带走整个进程。
			if r := recover(); r != nil {
				global.App.Log.Error(fmt.Sprintf("刷新实例 %s 元数据时发生异常：%v", instanceID, r))
			}
		}()
		for attempt := 0; attempt < 2; attempt++ {
			_, _, _ = metaRefreshGroup.Do(instanceID, func() (interface{}, error) {
				return nil, runMetaRefresh(instanceID)
			})
			if syncedAfter(instanceID, startedAt) {
				return
			}
		}
	}()
}

// RefreshInstanceMeta 同步刷新并返回最新状态。
//
// 只给「手动刷新」接口用——用户在页面上点了刷新，理应看到明确的成功/失败，
// 而不是再猜一次。所有业务读路径一律走 EnsureFresh / RefreshInstanceMetaAsync。
func RefreshInstanceMeta(instanceID string) (MetaFreshness, error) {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return MetaFreshness{LastStatus: metaStateNever}, fmt.Errorf("数据源ID不能为空")
	}
	// 注意取值顺序：必须「先刷新、再读状态」。
	// 反过来写会把刷新前的旧状态当成结果返回，调用方看到的永远是 never，
	// 前端就会一直提示「尚未同步」——即便这次已经同步成功了。
	v, err, _ := metaRefreshGroup.Do(instanceID, func() (interface{}, error) {
		syncErr := runMetaRefresh(instanceID)
		return readMetaFreshnessWithError(instanceID), syncErr
	})
	freshness, _ := v.(MetaFreshness)
	return freshness, err
}

// runMetaRefresh 真正执行一次同步 + 状态落库。调用方负责 singleflight 去重。
func runMetaRefresh(instanceID string) error {
	start := time.Now()

	var instance models.InsightInstances
	if err := global.App.DB.Table("insight_instances").
		Where("instance_id = ?", instanceID).
		Take(&instance).Error; err != nil {
		// 实例已删除：清掉状态行，避免留下指向不存在实例的孤儿。
		global.App.DB.Where("instance_id = ?", instanceID).Delete(&models.InsightInstanceMetaState{})
		return fmt.Errorf("数据源不存在或已删除")
	}

	// 先落「同步中」，让并发进来的读请求看到真实状态而不是又触发一次。
	markMetaState(instanceID, map[string]interface{}{
		"syncing_since":   models.LocalTime(start),
		"last_attempt_at": models.LocalTime(start),
	})

	syncErr := SyncDBMetaForInstance(instance)
	resourceCount := countSchemaResources(instanceID)
	finishedAt := time.Now()

	if syncErr != nil {
		markMetaState(instanceID, map[string]interface{}{
			"syncing_since":  nil,
			"last_status":    metaStateFailed,
			"last_error":     truncateMetaError(syncErr.Error()),
			"resource_count": resourceCount,
		})
		global.App.Log.Error(fmt.Sprintf(
			"刷新实例 %s 元数据失败（耗时 %s）：%s", instanceID, finishedAt.Sub(start).Round(time.Millisecond), syncErr.Error()))
		return syncErr
	}

	markMetaState(instanceID, map[string]interface{}{
		"syncing_since":  nil,
		"last_status":    metaStateSuccess,
		"last_error":     "",
		"last_synced_at": models.LocalTime(finishedAt),
		"resource_count": resourceCount,
	})
	global.App.Log.Info(fmt.Sprintf(
		"刷新实例 %s 元数据成功：%d 个库/模式，耗时 %s", instanceID, resourceCount, finishedAt.Sub(start).Round(time.Millisecond)))
	return nil
}

// needMetaRefresh 判断是否需要触发一次后台刷新。
func needMetaRefresh(state models.InsightInstanceMetaState, found bool, now time.Time, ttl time.Duration) bool {
	// 从未同步过：必须刷，否则页面永远是空列表。
	if !found || state.LastStatus == metaStateNever {
		return true
	}
	// 正在同步且标记没过期：让它跑完，否则会对同一个实例叠加多次同步。
	if state.SyncingSince != nil {
		if now.Sub(time.Time(*state.SyncingSince)) < metaSyncingStaleAfter {
			return false
		}
	}
	// 距上次「发起」还不到 TTL：不重复触发。
	// 注意这里用 last_attempt_at 而不是 last_synced_at——同步失败也要有节流，
	// 否则一个连不上的实例会被每次页面请求反复重试，把连接超时堆到日志里。
	if state.LastAttemptAt != nil && now.Sub(time.Time(*state.LastAttemptAt)) < ttl {
		return false
	}
	return true
}

// buildFreshness 把状态行翻译成对外结构。
func buildFreshness(instanceID string, state models.InsightInstanceMetaState, found bool, now time.Time) MetaFreshness {
	freshness := MetaFreshness{
		InstanceID:  instanceID,
		LastStatus:  metaStateNever,
		ResourceCnt: 0,
	}
	if !found {
		freshness.Stale = true
		return freshness
	}
	freshness.LastStatus = state.LastStatus
	if freshness.LastStatus == "" {
		freshness.LastStatus = metaStateNever
	}
	freshness.LastError = state.LastError
	freshness.ResourceCnt = state.ResourceCount
	if state.LastSyncedAt != nil {
		syncedAt := time.Time(*state.LastSyncedAt)
		freshness.LastSyncedAt = &syncedAt
	}
	if state.SyncingSince != nil && now.Sub(time.Time(*state.SyncingSince)) < metaSyncingStaleAfter {
		freshness.Syncing = true
	}
	ttl := metaRefreshTTL()
	switch {
	case freshness.LastSyncedAt == nil:
		freshness.Stale = true
	case ttl <= 0:
		// 关闭了惰性刷新：只有从没同步过才算 stale，避免页面一直挂提示。
		freshness.Stale = false
	default:
		freshness.Stale = now.Sub(*freshness.LastSyncedAt) >= ttl
	}
	return freshness
}

func readMetaState(instanceID string) (models.InsightInstanceMetaState, bool) {
	var state models.InsightInstanceMetaState
	err := global.App.DB.Table("insight_instance_meta_state").
		Where("instance_id = ?", instanceID).Take(&state).Error
	if err != nil {
		return models.InsightInstanceMetaState{}, false
	}
	return state, true
}

func readMetaFreshnessWithError(instanceID string) MetaFreshness {
	state, found := readMetaState(instanceID)
	return buildFreshness(instanceID, state, found, time.Now())
}

// syncedAfter 判断该实例最近一次成功同步是否发生在 given 之后。
func syncedAfter(instanceID string, given time.Time) bool {
	state, found := readMetaState(instanceID)
	if !found || state.LastSyncedAt == nil {
		return false
	}
	return !time.Time(*state.LastSyncedAt).Before(given)
}

// markMetaState 幂等写入状态：行不存在则插入，存在则只更新传入的列。
//
// 为什么用 ON DUPLICATE KEY UPDATE 而不是「先 UPDATE 再判 RowsAffected 补 INSERT」：
// 后者在并发下会撞主键重复，而单飞只保证同进程同实例串行，手动刷新与后台刷新
// 仍可能落在不同 goroutine 上。交给数据库做原子 upsert 最省心。
func markMetaState(instanceID string, updates map[string]interface{}) {
	if len(updates) == 0 {
		return
	}
	updates["updated_at"] = time.Now()
	row := models.InsightInstanceMetaState{
		InstanceID: instanceID,
		LastStatus: metaStateNever,
	}
	err := global.App.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "instance_id"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(&row).Error
	if err != nil {
		// 状态写入失败不应影响同步本身：同步已经改了 insight_instance_schemas，
		// 业务上该看到的东西已经能看到，这里只丢掉了「同步时间/失败原因」这类观测信息。
		global.App.Log.Warnf("写入实例 %s 元数据同步状态失败：%v", instanceID, err)
	}
}

// countSchemaResources 统计当前快照里有效资源的条数。
func countSchemaResources(instanceID string) int {
	var count int64
	if err := global.App.DB.Table("insight_instance_schemas").
		Where("instance_id = ? AND is_deleted = 0", instanceID).
		Count(&count).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			global.App.Log.Warnf("统计实例 %s 元数据条数失败：%v", instanceID, err)
		}
		return 0
	}
	return int(count)
}

func truncateMetaError(message string) string {
	message = strings.TrimSpace(message)
	runes := []rune(message)
	if len(runes) <= metaStateErrorMaxLen {
		return message
	}
	return string(runes[:metaStateErrorMaxLen])
}
