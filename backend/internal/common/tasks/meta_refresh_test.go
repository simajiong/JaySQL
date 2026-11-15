package tasks

import (
	"strings"
	"testing"
	"time"

	"github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
)

func TestMetaRefreshTTLConfig(t *testing.T) {
	original := global.App.Config.Crontab.MetaRefreshTTL
	t.Cleanup(func() { global.App.Config.Crontab.MetaRefreshTTL = original })

	cases := []struct {
		name     string
		config   int
		expected time.Duration
	}{
		{name: "未配置回落默认六十秒", config: 0, expected: defaultMetaRefreshTTL},
		{name: "正数按配置秒数", config: 15, expected: 15 * time.Second},
		{name: "负数关闭惰性刷新", config: -1, expected: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			global.App.Config.Crontab.MetaRefreshTTL = tc.config
			if got := metaRefreshTTL(); got != tc.expected {
				t.Fatalf("metaRefreshTTL() = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestNeedMetaRefresh(t *testing.T) {
	now := time.Now()
	ttl := 60 * time.Second
	ago := func(d time.Duration) *models.LocalTime {
		v := models.LocalTime(now.Add(-d))
		return &v
	}

	cases := []struct {
		name  string
		state models.InsightInstanceMetaState
		found bool
		want  bool
	}{
		{
			name:  "从未同步过必须刷",
			found: false,
			want:  true,
		},
		{
			name:  "状态行存在但状态是 never 也要刷",
			found: true,
			state: models.InsightInstanceMetaState{LastStatus: metaStateNever},
			want:  true,
		},
		{
			name:  "正在同步且标记未过期时不重复触发",
			found: true,
			state: models.InsightInstanceMetaState{LastStatus: metaStateSuccess, SyncingSince: ago(10 * time.Second)},
			want:  false,
		},
		{
			name:  "同步中标记已过期（进程异常退出）应重新触发",
			found: true,
			state: models.InsightInstanceMetaState{LastStatus: metaStateFailed, SyncingSince: ago(metaSyncingStaleAfter + time.Minute)},
			want:  true,
		},
		{
			name:  "距上次发起不足 TTL 不触发",
			found: true,
			state: models.InsightInstanceMetaState{LastStatus: metaStateSuccess, LastAttemptAt: ago(10 * time.Second)},
			want:  false,
		},
		{
			name:  "距上次发起超过 TTL 触发",
			found: true,
			state: models.InsightInstanceMetaState{LastStatus: metaStateSuccess, LastAttemptAt: ago(2 * time.Minute)},
			want:  true,
		},
		{
			name:  "同步失败同样受 TTL 节流",
			found: true,
			state: models.InsightInstanceMetaState{LastStatus: metaStateFailed, LastAttemptAt: ago(5 * time.Second)},
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needMetaRefresh(tc.state, tc.found, now, ttl); got != tc.want {
				t.Fatalf("needMetaRefresh() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildFreshness(t *testing.T) {
	original := global.App.Config.Crontab.MetaRefreshTTL
	t.Cleanup(func() { global.App.Config.Crontab.MetaRefreshTTL = original })
	global.App.Config.Crontab.MetaRefreshTTL = 60

	now := time.Now()

	t.Run("无状态行判为 stale 且状态 never", func(t *testing.T) {
		got := buildFreshness("ds-1", models.InsightInstanceMetaState{}, false, now)
		if got.LastStatus != metaStateNever || !got.Stale || got.Syncing {
			t.Fatalf("unexpected freshness: %+v", got)
		}
	})

	t.Run("刚同步成功既不 stale 也不 syncing", func(t *testing.T) {
		syncedAt := models.LocalTime(now.Add(-5 * time.Second))
		got := buildFreshness("ds-1", models.InsightInstanceMetaState{
			LastStatus:    metaStateSuccess,
			LastSyncedAt:  &syncedAt,
			ResourceCount: 3,
		}, true, now)
		if got.Stale || got.Syncing || got.LastSyncedAt == nil || got.ResourceCnt != 3 {
			t.Fatalf("unexpected freshness: %+v", got)
		}
	})

	t.Run("超过 TTL 判为 stale", func(t *testing.T) {
		syncedAt := models.LocalTime(now.Add(-2 * time.Minute))
		got := buildFreshness("ds-1", models.InsightInstanceMetaState{
			LastStatus:   metaStateSuccess,
			LastSyncedAt: &syncedAt,
		}, true, now)
		if !got.Stale {
			t.Fatalf("expected stale, got %+v", got)
		}
	})

	t.Run("同步中标记有效时 syncing 为真", func(t *testing.T) {
		syncingSince := models.LocalTime(now.Add(-3 * time.Second))
		got := buildFreshness("ds-1", models.InsightInstanceMetaState{
			LastStatus:   metaStateSuccess,
			SyncingSince: &syncingSince,
		}, true, now)
		if !got.Syncing {
			t.Fatalf("expected syncing, got %+v", got)
		}
	})

	t.Run("同步中标记过期视为脏标记", func(t *testing.T) {
		syncingSince := models.LocalTime(now.Add(-metaSyncingStaleAfter - time.Minute))
		got := buildFreshness("ds-1", models.InsightInstanceMetaState{SyncingSince: &syncingSince}, true, now)
		if got.Syncing {
			t.Fatalf("stale syncing flag should be ignored, got %+v", got)
		}
	})
}

func TestTruncateMetaError(t *testing.T) {
	short := "连接超时"
	if got := truncateMetaError("  " + short + "  "); got != short {
		t.Fatalf("truncateMetaError() = %q, want %q", got, short)
	}

	long := strings.Repeat("错", metaStateErrorMaxLen+50)
	got := truncateMetaError(long)
	if len([]rune(got)) != metaStateErrorMaxLen {
		t.Fatalf("expected %d runes, got %d", metaStateErrorMaxLen, len([]rune(got)))
	}
}
