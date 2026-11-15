import { del, get, post, put } from "@/utils/request"

const DAS_SILENT_RESOURCE_CONFIG = {
  skipGlobalMessageError: true,
  silentBusinessError: true,
  errorCategory: 'connection',
  errorFriendlyText: '拉取资源列表失败，请检查实例连接状态',
}

const DAS_SILENT_SCHEMA_TABLES_CONFIG = {
  skipGlobalMessageError: true,
  silentBusinessError: true,
  errorCategory: 'connection',
  errorFriendlyText: '加载表元数据失败，请检查实例连接状态',
}

export const GetSchemasApi = (params) => get('/api/v1/das/schemas', params)
export const GetInstancesApi = (params) => get('/api/v1/das/instances', params)
// 库清单同步状态 / 手动刷新（工单提交、授权申请、SQL 工作台共用）。
// 这些接口只触发一次只读元数据采集，不返回任何数据内容，
// 所以工单提交人（order.submit）与权限申请人（das.access.apply）也有权限调用。
export const GetInstanceMetaStatusApi = (instanceId) =>
  get(`/api/v1/das/instances/${instanceId}/meta-status`, {}, { skipGlobalMessageError: true, silentBusinessError: true })
export const RefreshInstanceMetaApi = (instanceId) =>
  post(`/api/v1/das/instances/${instanceId}/refresh-meta`, {}, { skipGlobalMessageError: true, silentBusinessError: true })
export const GetResourcesApi = (params, config = {}) => get('/api/v1/das/resources', params, { ...DAS_SILENT_RESOURCE_CONFIG, ...(config || {}) })
export const GetSchemaTablesApi = (params, config = {}) => get('/api/v1/das/schema/tables', params, { ...DAS_SILENT_SCHEMA_TABLES_CONFIG, ...(config || {}) })
// SQL 工作台「对象树」（Schema Browser）逐层懒加载取数
export const GetSchemaBrowserChildrenApi = (params, config = {}) =>
  get('/api/v1/das/schema-browser/children', params, {
    skipGlobalMessageError: true,
    silentBusinessError: true,
    errorCategory: 'connection',
    errorFriendlyText: '加载对象树节点失败，请检查实例连接状态',
    ...(config || {}),
  })
export const GetHistoryApi = (params) => get('/api/v1/das/history', params)
// 数据访问申请入口取数：申请人未必有授权，三个接口都做最小暴露（不含任何凭据/数据）
export const GetAccessInstanceOptionsApi = (params) => get('/api/v1/das/access-instance-options', params)
export const GetAccessSchemaOptionsApi = (params) => get('/api/v1/das/access-schema-options', params)
export const GetAccessTableOptionsApi = (params) => get('/api/v1/das/access-table-options', params)
export const GetPermittedTablesBySchemaApi = (params, config = {}) => get('/api/v1/das/schema/grants', params, { ...DAS_SILENT_RESOURCE_CONFIG, ...(config || {}) })
export const ExecuteMySQLQueryApi = (params, config = {}) => post('/api/v1/das/query/mysql', params, config)
export const ExplainMySQLQueryApi = (params, config = {}) => post('/api/v1/das/query/mysql/explain', params, config)
export const ExecuteClickHouseQueryApi = (params, config = {}) => post('/api/v1/das/query/clickhouse', params, config)
export const ExecutePostgresQueryApi = (params, config = {}) =>
  post('/api/v1/das/query/postgresql', params, config)
export const ExplainPostgresQueryApi = (params, config = {}) =>
  post('/api/v1/das/query/postgresql/explain', params, config)
export const ExecuteSQLServerQueryApi = (params, config = {}) =>
  post('/api/v1/das/query/sqlserver', params, config)
export const ExecuteOracleQueryApi = (params, config = {}) => post('/api/v1/das/query/oracle', params, config)
export const GetTableInfoApi = (params) => get('/api/v1/das/table-info', params)
export const GetDBDictApi = (params) => get('/api/v1/das/dbdict', params)
export const CreateFavoritesApi = (data) => post('/api/v1/das/favorites', data)
export const GetFavoritesApi = (params) => get('/api/v1/das/favorites', params)
export const UpdateFavoritesApi = (data) => put(`/api/v1/das/favorites/${data.id}`, data)
export const DeleteFavoritesApi = (data) => del(`/api/v1/das/favorites/${data.id}`, data)

const DAS_OBJECT_SEARCH_CONFIG = {
  skipGlobalMessageError: true,
  silentBusinessError: true,
  errorCategory: 'search',
  errorFriendlyText: '对象搜索服务异常，请稍后再试',
}

export const GetObjectsFlatIndexApi = (params, config = {}) =>
  get('/api/v1/das/objects/flat-index', params, { ...DAS_OBJECT_SEARCH_CONFIG, ...(config || {}) })
export const SearchObjectsApi = (params, config = {}) =>
  get('/api/v1/das/objects/search', params, { ...DAS_OBJECT_SEARCH_CONFIG, ...(config || {}) })
export const ReportObjectSearchAuditApi = (data, config = {}) =>
  post('/api/v1/das/objects/search-audit', data || {}, {
    ...DAS_OBJECT_SEARCH_CONFIG,
    ...(config || {}),
    timeout: 3000,
  })

// ---------------------------------------------------------------------------
// 数据访问权限申请
// 申请人直选「数据源 + 库/表 + 操作」，提交后进入审批流，与 SQL 工单无关。
// 申请入口的三个取数接口不走授权过滤（申请人本来就还没有权限）。
// ---------------------------------------------------------------------------
export const GetAccessRequestsApi = (params) => get('/api/v1/das/access-requests', params)
export const GetAccessRequestDetailApi = (params) =>
  get(`/api/v1/das/access-requests/${params.request_no}`)
export const CreateAccessRequestApi = (data) => post('/api/v1/das/access-requests', data)
export const ApproveAccessRequestApi = (data) =>
  post(`/api/v1/das/access-requests/${data.request_no}/approve`, data)
export const RejectAccessRequestApi = (data) =>
  post(`/api/v1/das/access-requests/${data.request_no}/reject`, data)
export const WithdrawAccessRequestApi = (data) =>
  post(`/api/v1/das/access-requests/${data.request_no}/withdraw`, data)
// 转交 / 终止 / 执行授权：审批与落地分两步，审批通过只推进到 wait_grant
export const ForwardAccessRequestApi = (data) =>
  post(`/api/v1/das/access-requests/${data.request_no}/forward`, data)
export const TerminateAccessRequestApi = (data) =>
  post(`/api/v1/das/access-requests/${data.request_no}/terminate`, data)
export const GrantAccessRequestApi = (data) =>
  post(`/api/v1/das/access-requests/${data.request_no}/grant`, data)
// 审批人候选：申请页选人、详情页转交共用（只返回持有 das.access.approve 的账号）
export const GetAccessApproverOptionsApi = (params) =>
  get('/api/v1/das/access-approver-options', params)
