/**
 * 监控模块「前端对外开放」的数据源白名单
 *
 * 背景：后端对 MySQL / PostgreSQL / TiDB / Doris / ClickHouse / SQLServer / Oracle
 * 都保留了类型定义、连接校验、解析逻辑与模板存储结构（engineCapabilities），
 * 但监控目前只有 MySQL / PostgreSQL / Oracle / SQL Server 四套经过核对的采集实现、
 * 展示目录与默认模板，其余类型（TiDB / Doris / ClickHouse）开放出去会出现
 * "能选但指标是空的"。所以在这里收窄前端可选范围，
 * 后端能力原样保留，后续按需放开即可。
 *
 * 适用范围（统一走本文件，业务组件不再各自硬编码类型）：
 *   - 监控配置页顶部「全部类型」筛选下拉
 *   - 监控明细页顶部「数据库类型」筛选下拉
 *   - 指标模板列表筛选下拉 / 新建模板的「数据库类型」下拉与提交校验
 *
 * ── 后续如何扩充 ────────────────────────────────────────────────
 * 1. 确认后端已具备该类型的能力：
 *    backend/internal/monitor/services/monitor_metrics.go 的 engineCapabilities
 *    中存在该 key，且 Implemented = true。
 * 2. 确认该类型有对应的采集实现、展示目录与默认模板：
 *    monitor_display_layout.go（展示目录）+ monitor_templates.go（默认模板播种）。
 * 3. 只改本文件两处：
 *    - MONITOR_DB_TYPE_WHITELIST 增加该 key（小写，与后端 engineCapabilities.Key 一致）
 *    - DISPLAY_NAME 增加 key → 展示名（用于校验提示文案）
 *    前端所有类型下拉、图标、校验提示会自动跟随，无需改业务组件。
 * ────────────────────────────────────────────────────────────────
 *
 * 注意：这里只控制「新建/选择」入口，不控制后端数据来源 ——
 *   - 后端 API 仍保留全部类型（未来要放开时不必重新实现），前端只是不展示；
 *   - 指标模板列表按白名单过滤，其它类型的模板不再显示；
 *   - 已注册的监控对象仍会照常列出，避免造成已有实例看不见的兼容问题。
 */
import { dbTypeLogo } from '@/utils/dbTypeLogo'

/** 允许在监控模块中新建/筛选的数据源 key（与后端 engineCapabilities.Key 对齐，小写） */
// kingbase：后端 engineCapabilities 已注册（Implemented=true），采集复用 PG 采集器，
// 展示目录 displayLayouts 整目录复用 PostgreSQL，因此可以对外开放。
export const MONITOR_DB_TYPE_WHITELIST = ['mysql', 'postgresql', 'kingbase', 'oracle', 'sqlserver', 'tidb']

/** 白名单 key → 展示名，仅用于提示文案 */
const DISPLAY_NAME = {
  mysql: 'MySQL',
  postgresql: 'PostgreSQL',
  kingbase: 'Kingbase',
  oracle: 'Oracle',
  sqlserver: 'SQL Server',
  tidb: 'TiDB',
}

const normalize = (value) => String(value == null ? '' : value).trim().toLowerCase()

/** 单个值（key 或展示名）是否在白名单内 */
export const isMonitorDbTypeSupported = (value) =>
  MONITOR_DB_TYPE_WHITELIST.includes(normalize(value))

/**
 * 下拉项是否在白名单内。
 * 不同接口给的 value 形态不一致：/monitor/templates 与监控筛选的 db_types 用 key（mysql），
 * 引擎能力表派生出来的选项用展示名（MySQL），所以 value / label 两个字段都判一次。
 */
export const isMonitorDbTypeOptionSupported = (option) =>
  isMonitorDbTypeSupported(option?.value) || isMonitorDbTypeSupported(option?.label)

/** 过滤下拉项，只保留白名单内的类型 */
export const filterMonitorDbTypeOptions = (options) =>
  (options || []).filter(isMonitorDbTypeOptionSupported)

/** 校验提示文案，如 "MySQL / PostgreSQL" —— 随白名单自动变化，避免多处硬编码 */
export const monitorDbTypeHint = () =>
  MONITOR_DB_TYPE_WHITELIST.map((key) => DISPLAY_NAME[key] || key).join(' / ')

/** 给下拉项补上品牌 Logo 字段，供 a-select 的 #option 插槽渲染 */
export const decorateMonitorDbTypeOptions = (options) =>
  filterMonitorDbTypeOptions(options).map((item) => ({
    ...item,
    logo: dbTypeLogo(item.value ?? item.label),
  }))
