// Kingbase 走 PG 通道（慢日志采集、参数管理均复用 PG 实现）
export const SUPPORTED_DB_TYPES = ['MySQL', 'PostgreSQL', 'Kingbase']

export const formatNumber = (value) => {
  const num = Number(value || 0)
  if (!Number.isFinite(num)) return '0'
  return new Intl.NumberFormat('zh-CN').format(num)
}

export const formatDuration = (value) => {
  const num = Number(value || 0)
  if (!Number.isFinite(num) || num <= 0) return '0 ms'
  if (num >= 1000) return `${(num / 1000).toFixed(num >= 10000 ? 1 : 2)} s`
  return `${Math.round(num)} ms`
}

export const buildInstanceLabel = (item = {}) => {
  const name = item.remark || item.instance_id || '-'
  const target = item.hostname && item.port ? `${item.hostname}:${item.port}` : ''
  return target ? `${name}（${target}）` : name
}

export const yesNoText = (value) => (value ? '是' : '否')

export const scopeTypeText = (value) => {
  if (value === 'instance') return '实例级'
  return '全局'
}

// 截断工具收敛到全局 utils/text.js，这里保留原导出，避免各调用方改 import
export { clipText } from '@/utils/text'

export const sqlPreview = (sqlText = '', max = 120) => {
  const text = String(sqlText || '').replace(/\s+/g, ' ').trim()
  if (!text) return '-'
  return text.length > max ? `${text.slice(0, max)}...` : text
}
