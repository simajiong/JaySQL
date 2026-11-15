-- =============================================================================
-- 补种「PostgreSQL UPDATE/DELETE 影响行数 / LIMIT 行数」两条规则（存量规范专用）
--
-- 背景：
--   PG 引擎此前只有 limit_update_delete_join_table_count 一条 DML 量化规则。
--   本次补齐另外两条（与 mysql/tidb/oracle 目录对齐）：
--     · postgres.query_update.limit_update_delete_affected_rows
--       引擎按「对同 WHERE 条件做 COUNT(*)」估算影响行数（pg_rules.go pgCountWhere）；
--     · postgres.query_update.limit_update_delete_data_size
--       引擎解析 LIMIT 常量行数超阈值告警（pg_dml.go LimitRows）。
--   standards.go 的参数映射两个 case 均已加 postgres 前缀，规则存在即可生效。
--
-- 安全：整段可重复执行（NOT EXISTS 幂等）；STEP 1 只读预览。
-- =============================================================================

-- ========================== STEP 1：只读预览 ==========================

SELECT s.id AS standard_id, s.name AS standard_name,
       r.rule_key,
       (SELECT COUNT(*) FROM insight_sql_standard_rules x
         WHERE x.standard_id = s.id AND x.rule_key = r.rule_key) AS already_seeded
FROM insight_sql_standards s
JOIN (
  SELECT 'postgres.query_update.limit_update_delete_affected_rows' AS rule_key
  UNION ALL SELECT 'postgres.query_update.limit_update_delete_data_size'
) r;

-- ========================== STEP 2：写入（幂等） ==========================

INSERT INTO insight_sql_standard_rules
  (created_at, updated_at, standard_id, rule_key, category, db_type, name, description,
   rule_type, base_level, level, params, sort_order, implemented)
SELECT
  NOW(3), NOW(3), s.id, r.rule_key, r.category, 'postgres', r.name, r.description,
  r.rule_type, r.base_level, r.base_level, r.params, r.sort_order, 1
FROM insight_sql_standards s
JOIN (
  SELECT 'postgres.query_update.limit_update_delete_affected_rows' AS rule_key,
         'query_update' AS category, '更新' AS rule_type,
         'UPDATE/DELETE 单语句影响行数' AS name,
         '大批量更新删除会拖长事务时间、占用大量存储并加剧锁竞争，回滚代价高' AS description,
         'suggest' AS base_level, '{"max_affected_rows":10000}' AS params, 115 AS sort_order
  UNION ALL
  SELECT 'postgres.query_update.limit_update_delete_data_size',
         'query_update', '更新',
         'UPDATE/DELETE 语句 LIMIT 的行数',
         '限制单次数据量可缩短锁持有时间，降低对性能的影响',
         'suggest', '{"max_limit_rows":5}', 115
) r
WHERE NOT EXISTS (
  SELECT 1 FROM insight_sql_standard_rules x
   WHERE x.standard_id = s.id AND x.rule_key = r.rule_key
);
