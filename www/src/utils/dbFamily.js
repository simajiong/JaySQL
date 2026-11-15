/**
 * 数据库类型 → 引擎家族 的前端唯一判定入口。
 *
 * 与后端 backend/internal/common/dbfamily 逐项对齐，理由相同：
 *   - 同一个 db_type（尤其 OceanBase）在不同页面都要判一次「它该走 MySQL 还是 Oracle 通道」，
 *     历史上每个页面各写一份 switch，新增一种类型就得满地找；
 *   - OceanBase 一个 db_type 对应两种租户模式，只靠 db_type 字符串判不出来，
 *     必须结合 extra_config.oceanbase_mode。
 *
 * 取值来源优先级：
 *   1) 后端 /api/v1/das/instances 下发的派生字段 db_family（权威，后端已解析过 extra_config）；
 *   2) 本地按 db_type + extra_config.oceanbase_mode 推导（口径与后端一致）。
 * 见 resolveDbFamily / resolveInstanceDbFamily。
 */

export const FAMILY_MYSQL = 'mysql'
export const FAMILY_POSTGRES = 'postgresql'
export const FAMILY_ORACLE = 'oracle'
export const FAMILY_CLICKHOUSE = 'clickhouse'
export const FAMILY_SQLSERVER = 'sqlserver'
export const FAMILY_MONGODB = 'mongodb'

/** 写入 extra_config.oceanbase_mode 的规范值。 */
export const OCEANBASE_MYSQL = 'mysql'
export const OCEANBASE_ORACLE = 'oracle'

/**
 * OceanBase 模式的等价写法。只列 Oracle 侧：
 * 识别不出来的一律回落 MySQL，与后端 dbfamily.OceanBaseMode 的兜底口径一致。
 */
const OCEANBASE_ORACLE_MODE_ALIASES = [
  'oracle',
  'oracle_mode',
  'oracle-mode',
  'oboracle',
  'ob_oracle',
  'oceanbase_oracle',
  'oceanbase-oracle',
]

/** 归一化数据库类型字符串：去空白 + 转小写。 */
export const normalizeDbType = (value) => String(value ?? '').trim().toLowerCase()

/** 是否为 OceanBase（大小写/空白不敏感）。 */
export const isOceanBaseType = (dbType) => normalizeDbType(dbType) === 'oceanbase'

/** 归一化 OceanBase 租户模式：只返回 'mysql' / 'oracle'，缺省或非法一律 'mysql'。 */
export const normalizeOceanBaseMode = (raw) =>
  OCEANBASE_ORACLE_MODE_ALIASES.includes(normalizeDbType(raw)) ? OCEANBASE_ORACLE : OCEANBASE_MYSQL

/**
 * 解析 extra_config。后端可能下发 JSON 字符串（历史行为）或已解析的对象；
 * 任何异常输入都返回空对象，绝不抛出。
 */
export const parseExtraConfig = (raw) => {
  if (!raw) return {}
  if (typeof raw === 'object') return raw
  if (typeof raw !== 'string') return {}
  try {
    const parsed = JSON.parse(raw)
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

/** 从 extra_config 取 OceanBase 租户模式（缺省 mysql）。 */
export const oceanBaseMode = (extraConfig) =>
  normalizeOceanBaseMode(parseExtraConfig(extraConfig).oceanbase_mode)

/** 该 OceanBase 实例是否处于 Oracle 模式。非 OceanBase 类型一律 false。 */
export const isOceanBaseOracleMode = (extraConfig) => oceanBaseMode(extraConfig) === OCEANBASE_ORACLE

/**
 * 返回数据库类型实际对应的引擎家族（小写）。
 * 未知类型原样返回小写字符串，便于在提示文案里回显用户填的类型。
 */
export const resolveDbFamily = (dbType, extraConfig) => {
  switch (normalizeDbType(dbType)) {
    case 'mysql':
    case 'tidb':
    case 'doris':
    case 'mariadb':
    // percona：MySQL 分支，历史上只有 ConsoleIndex 认它，这里统一收口
    case 'percona':
    case 'polardb':
    case 'polardb-mysql':
    case 'polardbmysql':
      return FAMILY_MYSQL
    case 'postgresql':
    case 'postgres':
    case 'pg':
    // Kingbase（金仓）兼容 PostgreSQL 协议与方言，连接/元数据/执行全部复用 PG 通道；
    // 这里归一到 postgres 家族后，各页面的「PG 家族」判定自动生效，不必逐页加判断。
    case 'kingbase':
    case 'kingbasees':
    case 'polar-postgres':
    case 'polardb-pg':
    case 'polardb-postgresql':
    case 'polardbpg':
      return FAMILY_POSTGRES
    case 'oracle':
      return FAMILY_ORACLE
    case 'clickhouse':
      return FAMILY_CLICKHOUSE
    case 'sqlserver':
      return FAMILY_SQLSERVER
    case 'mongodb':
      return FAMILY_MONGODB
    default:
      break
  }
  if (isOceanBaseType(dbType)) {
    return isOceanBaseOracleMode(extraConfig) ? FAMILY_ORACLE : FAMILY_MYSQL
  }
  return normalizeDbType(dbType)
}

/**
 * 针对「实例对象」的家族判定（前端最常用的入口）。
 * 优先采用后端下发的 db_family（已是权威结论），缺失时本地按同一口径推导。
 */
export const resolveInstanceDbFamily = (instance) => {
  if (!instance) return ''
  const derived = normalizeDbType(instance.db_family)
  if (derived) return derived
  return resolveDbFamily(instance.db_type, instance.extra_config)
}

/**
 * 各数据库类型的规范展示名（统一大小写/写法，避免 "sqlserver" / "postgresql" 直接透出）。
 * 键为归一化后的小写类型名。
 */
const DB_TYPE_DISPLAY_NAMES = {
  mysql: 'MySQL',
  tidb: 'TiDB',
  doris: 'Doris',
  mariadb: 'MariaDB',
  polardb: 'PolarDB',
  'polardb-mysql': 'PolarDB MySQL',
  polardbmysql: 'PolarDB MySQL',
  postgresql: 'PostgreSQL',
  postgres: 'PostgreSQL',
  kingbase: 'Kingbase',
  kingbasees: 'Kingbase',
  'polardb-pg': 'PolarDB PG',
  'polardb-postgresql': 'PolarDB PG',
  polardbpg: 'PolarDB PG',
  oracle: 'Oracle',
  clickhouse: 'ClickHouse',
  sqlserver: 'SQL Server',
  mongodb: 'MongoDB',
}

/**
 * 在「实例上下文」里解析家族。
 *
 * 为什么需要它：OceanBase 的租户模式只存在于实例配置（db_family / extra_config.oceanbase_mode）里，
 * 光给一个 db_type 字符串 "OceanBase" 是判不出走 MySQL 还是 Oracle 通道的，
 * 只能按 MySQL 模式兜底 —— 那对 Oracle 模式租户是错的（引号、rownum、执行端点都会跑偏）。
 * 因此凡是「可能拿到 OceanBase 原始类型」的调用点，都应把实例元数据一并传进来。
 *
 * 入参可以是 db_type、也可以是家族值本身（幂等）。
 */
export const resolveFamilyInContext = (dbType, instanceMeta) => {
  const raw = normalizeDbType(dbType)
  if (!isOceanBaseType(raw)) return resolveDbFamily(raw)
  return resolveInstanceDbFamily(instanceMeta) || resolveDbFamily(raw)
}

/**
 * 实例展示用类型名：OceanBase 带出租户模式，其余类型统一为规范写法。
 * 同为「OceanBase」的两条记录实际走完全不同的通道，列表/详情里必须能区分；
 * 刻意保持紧凑（不带「模式」二字），以便与版本号拼接后仍能放进表格列。
 */
export const dbTypeDisplayName = (dbType, extraConfig) => {
  const raw = String(dbType ?? '').trim()
  if (!raw) return '-'
  if (isOceanBaseType(raw)) {
    return oceanBaseMode(extraConfig) === OCEANBASE_ORACLE ? 'OceanBase (Oracle)' : 'OceanBase (MySQL)'
  }
  return DB_TYPE_DISPLAY_NAMES[normalizeDbType(raw)] || raw
}
