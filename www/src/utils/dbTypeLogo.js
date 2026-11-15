/**
 * 数据库类型 → 品牌图标映射
 *
 * 抽成共享工具的原因：数据备份 / 数据恢复 / 备份集等页面都需要展示数据库图标，
 * 之前各页面各写一份 switch，容易出现图标尺寸与风格不一致。统一从这里取。
 *
 * 用法：<img class="db-logo" :src="dbTypeLogo(record.db_type)" :alt="record.db_type" />
 * 尺寸全站统一 20px（2026-10-20 统一口径）：各页面用 .db-logo / .ds-logo / .data-source-logo /
 * .db-type-icon / .db-logo-img 等类名各自声明，但宽高一律 20×20，与文字间距由各页面自定。
 */
import mysqlLogo from '@/assets/logo/mysql.png'
import pgLogo from '@/assets/logo/postgresql.png'
import oracleLogo from '@/assets/logo/oracle.png'
import sqlserverLogo from '@/assets/logo/sqlserver.png'
import tidbLogo from '@/assets/logo/tidb.png'
import kingbaseLogo from '@/assets/logo/kingbase.png'
import oceanbaseLogo from '@/assets/logo/oceanbase.png'
import clickhouseLogo from '@/assets/logo/clickhouse.svg'
import mongodbLogo from '@/assets/logo/mongodb.svg'
import dorisLogo from '@/assets/logo/doris.svg'

/** 键统一为大写数据库类型，值对应 assets/logo 下的品牌图标 */
const LOGO_BY_DB_TYPE = Object.freeze({
  MYSQL: mysqlLogo,
  POSTGRESQL: pgLogo,
  ORACLE: oracleLogo,
  SQLSERVER: sqlserverLogo,
  TIDB: tidbLogo,
  // 金仓（Kingbase）：兼容 PostgreSQL 协议，但品牌独立，图标单独收录
  KINGBASE: kingbaseLogo,
  OCEANBASE: oceanbaseLogo,
  CLICKHOUSE: clickhouseLogo,
  MONGODB: mongodbLogo,
  DORIS: dorisLogo,
})

/**
 * 根据数据库类型获取品牌图标地址
 * @param {string} dbType 数据库类型，大小写不敏感（如 MySQL / postgresql / TiDB）
 * @returns {string} 图标资源地址；未知类型回落 MySQL 图标，与数据恢复页原有表现保持一致
 */
export const dbTypeLogo = (dbType) => {
  let key = String(dbType == null ? '' : dbType)
    .trim()
    .toUpperCase()
  // KingbaseES 是金仓的完整产品名，图标与 Kingbase 共用，避免落回 MySQL 图标
  if (key === 'KINGBASEES') key = 'KINGBASE'
  return LOGO_BY_DB_TYPE[key] || mysqlLogo
}

/**
 * 判断数据库类型是否有品牌图标（未收录类型返回 false，由调用方决定兜底展示）
 * @param {string} dbType 数据库类型，大小写不敏感
 * @returns {boolean}
 */
export const hasDbTypeLogo = (dbType) => {
  const key = String(dbType == null ? '' : dbType)
    .trim()
    .toUpperCase()
  return Object.prototype.hasOwnProperty.call(LOGO_BY_DB_TYPE, key)
}

export default dbTypeLogo
