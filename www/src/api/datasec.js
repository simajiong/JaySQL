import { get, post, put, del } from '@/utils/request'

// 数据安全（脱敏）：规则 / 列配置 / 自动发现 / 明文工单
// 后端模块：backend/internal/datasec，路由前缀 /api/v1/datasec

// ---------- 脱敏规则 ----------
export const getDataSecRulesApi = (params, config = {}) => get('/api/v1/datasec/rules', params, config)
export const createDataSecRuleApi = (data) => post('/api/v1/datasec/rules', data)
export const updateDataSecRuleApi = (id, data) => put(`/api/v1/datasec/rules/${id}`, data)
export const deleteDataSecRuleApi = (id) => del(`/api/v1/datasec/rules/${id}`)

// ---------- 列配置 ----------
export const getDataSecColumnsApi = (params, config = {}) => get('/api/v1/datasec/columns', params, config)
export const saveDataSecColumnApi = (data) => post('/api/v1/datasec/columns', data)
export const updateDataSecColumnStatusApi = (id, status) => post(`/api/v1/datasec/columns/${id}/status`, { status })
export const deleteDataSecColumnApi = (id) => del(`/api/v1/datasec/columns/${id}`)

// ---------- 库/表/列级联选项 ----------
// 独立于 /das/schemas（那条按调用者数据授权过滤，配规则的 DBA 未必有目标库授权）
export const getDataSecSchemaOptionsApi = (params, config = {}) => get('/api/v1/datasec/schema-options', params, config)
export const getDataSecTableOptionsApi = (params, config = {}) => get('/api/v1/datasec/table-options', params, config)
export const getDataSecColumnOptionsApi = (params, config = {}) => get('/api/v1/datasec/column-options', params, config)

// ---------- 自动发现 ----------
export const scanDataSecTableApi = (data) => post('/api/v1/datasec/scan', data)

// ---------- 明文工单 ----------
export const getDataSecGrantsApi = (params, config = {}) => get('/api/v1/datasec/grants', params, config)
export const createDataSecGrantApi = (data) => post('/api/v1/datasec/grants', data)
export const decideDataSecGrantApi = (id, data) => post(`/api/v1/datasec/grants/${id}/decide`, data)
