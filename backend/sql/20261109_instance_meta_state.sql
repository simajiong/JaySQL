-- =============================================================================
-- 数据源库元数据刷新状态表
--
-- 背景
--   改造前 insight_instance_schemas 只由 cron（默认 */5 * * * *）全量刷，
--   没有任何「写后失效」或「读时校验」，因此新建库后最坏要等 5 分钟
--   才能在工单提交 / 授权申请 / 资源树 / 已授权库列表里看到。
--
--   改造后刷新改为三条路径（见 internal/common/tasks/meta_refresh.go）：
--     L1 写后失效：平台内建库/工单执行/实例保存成功后立刻异步刷新
--     L2 读时惰性：快照超过 TTL（默认 60s）时异步触发，本次仍返回旧快照
--     L3 定时对账：sync_db_metas 降频到 */30，只兜底平台无感知的变更
--   本表承载刷新状态：节流依据（last_attempt_at）、页面同步态（syncing_since）、
--   排障信息（last_status / last_error / resource_count）。
--
-- 执行说明
--   * 本表已加入 internal/bootstrap/db.go 的 AutoMigrate 列表，服务启动会自动建表；
--     本脚本供「服务账号无 DDL 权限」或需要提前预建的环境手工执行。
--   * 幂等：IF NOT EXISTS，可重复执行。
--   * 回滚见 20261109_instance_meta_state_rollback.sql。
--
-- 用法：mysql --default-character-set=utf8mb4 -u<user> -p<pass> <db> < 本文件
-- =============================================================================

SET NAMES utf8mb4;

CREATE TABLE IF NOT EXISTS `insight_instance_meta_state` (
  `instance_id`     CHAR(15)     NOT NULL                          COMMENT '数据源业务ID',
  `last_synced_at`  DATETIME(3)  NULL                              COMMENT '最近一次同步成功时间',
  `last_attempt_at` DATETIME(3)  NULL                              COMMENT '最近一次发起同步时间',
  `last_status`     VARCHAR(16)  NOT NULL DEFAULT 'never'          COMMENT '最近一次同步结果 never/success/failed',
  `last_error`      VARCHAR(512) NOT NULL DEFAULT ''               COMMENT '最近一次失败原因',
  `resource_count`  INT          NOT NULL DEFAULT 0                COMMENT '本次同步到的库或模式数量',
  `syncing_since`   DATETIME(3)  NULL                              COMMENT '本轮同步开始时间，非空表示正在同步',
  `created_at`      DATETIME(3)  NULL                              COMMENT '创建时间',
  `updated_at`      DATETIME(3)  NULL                              COMMENT '更新时间',
  PRIMARY KEY (`instance_id`),
  KEY `idx_meta_state_last_attempt` (`last_attempt_at`),
  KEY `idx_meta_state_updated_at` (`updated_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='数据源库元数据刷新状态';

-- ============================ 复核 ============================
-- 期望：表存在、列齐全、主键为 instance_id
SELECT
  COLUMN_NAME      AS `列名`,
  COLUMN_TYPE      AS `类型`,
  IS_NULLABLE      AS `可空`,
  COLUMN_DEFAULT   AS `默认值`,
  COLUMN_COMMENT   AS `注释`
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE()
  AND TABLE_NAME   = 'insight_instance_meta_state'
ORDER BY ORDINAL_POSITION;
