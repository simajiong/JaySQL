package tasks

import (
	"net"
	"os"
	"strconv"
	"testing"

	"github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 集成测试：用真实 MySQL 跑一遍「同步 → 状态落库 → 快照可读」的完整链路。
//
// 单测只能覆盖纯逻辑（TTL 判定、状态翻译），而这条链路的风险恰恰在 SQL 侧：
// markMetaState 的 ON DUPLICATE KEY UPDATE、SyncDBMetaForInstance 写
// insight_instance_schemas、以及状态表与实例表的主键口径。
//
// 不设 DSN 时整组跳过，CI 与本地默认跑不受影响：
//
//	JAYSQL_IT_MYSQL_DSN='root:root1234@tcp(127.0.0.1:3306)/jaysql_it?charset=utf8mb4&parseTime=True&loc=Local' \
//	  go test ./internal/common/tasks -run TestRefreshInstanceMetaIntegration -v
const envIntegrationDSN = "JAYSQL_IT_MYSQL_DSN"

const integrationInstanceID = "ds-itmetaref001"

func TestRefreshInstanceMetaIntegration(t *testing.T) {
	dsn := os.Getenv(envIntegrationDSN)
	if dsn == "" {
		t.Skipf("未设置 %s，跳过集成测试", envIntegrationDSN)
	}
	dsnCfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("解析 DSN 失败：%v", err)
	}

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("连接测试库失败：%v", err)
	}
	if err := db.AutoMigrate(
		&models.InsightInstances{},
		&models.InsightInstanceSchemas{},
		&models.InsightInstanceMetaState{},
	); err != nil {
		t.Fatalf("建表失败：%v", err)
	}

	originalDB, originalLog := global.App.DB, global.App.Log
	t.Cleanup(func() {
		db.Exec("DELETE FROM insight_instance_schemas WHERE instance_id = ?", integrationInstanceID)
		db.Exec("DELETE FROM insight_instance_meta_state WHERE instance_id = ?", integrationInstanceID)
		db.Exec("DELETE FROM insight_instances WHERE instance_id = ?", integrationInstanceID)
		global.App.DB, global.App.Log = originalDB, originalLog
	})
	global.App.DB = db
	quietLog := logrus.New()
	quietLog.SetLevel(logrus.ErrorLevel)
	global.App.Log = quietLog

	host, portText, err := net.SplitHostPort(dsnCfg.Addr)
	if err != nil {
		t.Fatalf("解析测试库地址失败：%v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("解析测试库端口失败：%v", err)
	}
	// 目标实例就用测试库自己：同步会把它现有的库都采回来，
	// 断言只要求「采到了东西且状态为 success」，不依赖具体库名。
	// 密码必须走 secret.Encrypt（SyncDBMetaForInstance 会 Decrypt），
	// 而加密需要密钥就绪 —— 生产由启动流程加载，测试里显式 LoadKey 一次。
	originalKey := global.App.Config.App.SECRET_KEY
	global.App.Config.App.SECRET_KEY = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { global.App.Config.App.SECRET_KEY = originalKey })
	if err := secret.LoadKey(); err != nil {
		t.Fatalf("加载加密密钥失败：%v", err)
	}
	encrypted, err := secret.Encrypt(dsnCfg.Passwd)
	if err != nil {
		t.Fatalf("密码加密失败：%v", err)
	}
	instance := models.InsightInstances{
		InstanceID: integrationInstanceID,
		Hostname:   host,
		Port:       port,
		User:       dsnCfg.User,
		Password:   encrypted,
		DbType:     "MySQL",
		Remark:     "集成测试实例",
	}
	db.Exec("DELETE FROM insight_instances WHERE instance_id = ?", instance.InstanceID)
	if err := db.Create(&instance).Error; err != nil {
		t.Fatalf("写入测试实例失败：%v", err)
	}

	// 第一次刷新：必须成功，并把资源条数写进状态表。
	freshness, err := RefreshInstanceMeta(instance.InstanceID)
	if err != nil {
		t.Fatalf("刷新元数据失败：%v", err)
	}
	if freshness.LastStatus != metaStateSuccess {
		t.Fatalf("期望同步成功，实际状态 %q（错误：%s）", freshness.LastStatus, freshness.LastError)
	}
	if freshness.LastSyncedAt == nil {
		t.Fatal("同步成功后 last_synced_at 不能为空")
	}

	var schemaCount int64
	db.Table("insight_instance_schemas").
		Where("instance_id = ? AND is_deleted = 0", instance.InstanceID).
		Count(&schemaCount)
	if schemaCount == 0 {
		t.Fatal("同步成功后应至少采到一个库")
	}
	if freshness.ResourceCnt != int(schemaCount) {
		t.Fatalf("状态表资源条数 %d 与实际快照 %d 不一致", freshness.ResourceCnt, schemaCount)
	}

	// 刚同步完的实例不该被判为 stale，也不该再触发刷新。
	latest := ReadMetaFreshness(instance.InstanceID)
	if latest.Stale || latest.Syncing {
		t.Fatalf("刚同步完不该 stale/syncing，实际 %+v", latest)
	}

	// 幂等：再刷一次不能把状态表刷出第二行（主键 upsert 生效）。
	if _, err := RefreshInstanceMeta(instance.InstanceID); err != nil {
		t.Fatalf("二次刷新失败：%v", err)
	}
	var stateRows int64
	db.Table("insight_instance_meta_state").
		Where("instance_id = ?", instance.InstanceID).
		Count(&stateRows)
	if stateRows != 1 {
		t.Fatalf("状态表应为一实例一行，实际 %d 行", stateRows)
	}

	// 失败路径：改成连不通的端口，状态应转为 failed 且原因非空，
	// 同时保留上一次成功时间（否则页面会把「同步失败」显示成「从未同步」）。
	db.Model(&models.InsightInstances{}).
		Where("instance_id = ?", instance.InstanceID).
		Update("port", 1)
	if _, err := RefreshInstanceMeta(instance.InstanceID); err == nil {
		t.Fatal("连接到不存在的端口时应当返回错误")
	}
	failed := ReadMetaFreshness(instance.InstanceID)
	if failed.LastStatus != metaStateFailed || failed.LastError == "" {
		t.Fatalf("失败后状态应记录 failed + 原因，实际 %+v", failed)
	}
	if failed.LastSyncedAt == nil {
		t.Fatal("同步失败不应清掉上一次成功时间")
	}
}
