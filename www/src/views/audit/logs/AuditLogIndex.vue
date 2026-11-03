<template>
  <div class="gi-page-shell audit-log-page">
    <a-card class="gi-page-section audit-filter-card" :bordered="false">
      <div class="audit-filter">
        <!-- 顶部常驻行：预设时间 → 时间范围 → 查询 → 重置 → 收起/展开（整体靠右对齐） -->
        <div class="filter-main-row">
          <div class="preset-btn-group">
            <a-button
              v-for="preset in timePresets"
              :key="preset.key"
              :type="activePreset === preset.key ? 'primary' : 'default'"
              @click="applyPreset(preset.key)"
            >
              {{ preset.label }}
            </a-button>
          </div>

          <a-range-picker
            v-model:value="filterForm.timeRange"
            show-time
            value-format="YYYY-MM-DD HH:mm:ss"
            format="YYYY-MM-DD HH:mm:ss"
            class="filter-control filter-control--range"
          />

          <div class="filter-action-group">
            <a-button type="primary" @click="handleSearch">查询</a-button>
            <a-button @click="handleReset">重置</a-button>
            <a-tooltip :title="uiState.moreExpanded ? '收起更多筛选' : '展开更多筛选'">
              <a-button
                type="text"
                class="more-toggle-btn"
                :class="{ 'more-toggle-btn--active': uiState.moreExpanded }"
                @click="toggleMore()"
              >
                <template #icon>
                  <DownOutlined v-if="!uiState.moreExpanded" />
                  <UpOutlined v-else />
                </template>
                {{ uiState.moreExpanded ? '收起' : '展开' }}
              </a-button>
            </a-tooltip>
          </div>
        </div>

        <!-- 展开区：操作用户 / 审计等级 / 所属模块 / 操作结果 为半宽，关键字占满剩余半行 -->
        <div v-show="uiState.moreExpanded" class="filter-more-row">
          <a-input
            class="filter-control filter-control--half"
            v-model:value="filterForm.operatorUsername"
            allow-clear
            placeholder="操作用户"
          />
          <a-select
            class="filter-control filter-control--half"
            v-model:value="filterForm.auditLevel"
            allow-clear
            placeholder="审计等级"
            :options="auditLevelOptions"
          />
          <a-select
            class="filter-control filter-control--half"
            v-model:value="filterForm.moduleCode"
            allow-clear
            placeholder="所属模块"
            :options="moduleOptions"
          />
          <a-select
            class="filter-control filter-control--half"
            v-model:value="filterForm.actionResult"
            allow-clear
            placeholder="操作结果"
            :options="resultOptions"
          />
          <a-input
            class="filter-control filter-control--keyword"
            v-model:value="filterForm.keyword"
            allow-clear
            placeholder="搜索操作类型、摘要、资源名称"
          />
        </div>
      </div>
    </a-card>

    <a-card class="gi-page-section audit-log-table-card" :bordered="false">
      <div class="audit-log-table-wrapper" ref="tableWrapperRef">
        <a-table
          row-key="audit_id"
          :columns="columns"
          :data-source="tableData"
          :loading="tableLoading"
          :pagination="paginationConfig"
          :locale="{ emptyText: '暂无符合条件的审计日志' }"
          :scroll="{ y: scrollY }"
        >
          <template #bodyCell="{ column, record, text }">
            <template v-if="column.key === 'operated_at'">
              <span class="ellipsis-text" :title="record.operated_at">{{ record.operated_at || '-' }}</span>
            </template>
            <template v-else-if="column.key === 'audit_level'">
              {{ getAuditLevelLabel(record.audit_level) }}
            </template>
            <template v-else-if="column.key === 'action_result'">
              {{ getResultLabel(record.action_result) }}
            </template>
            <template v-else-if="column.key === 'summary'">
              <span class="ellipsis-text" :title="record.summary">{{ record.summary || '-' }}</span>
            </template>
            <template v-else-if="column.key === 'actions'">
              <span class="gi-table-actions">
                <a-button type="link" size="small" @click="openDetail(record)">查看详情</a-button>
              </span>
            </template>
            <template v-else>
              {{ text || '-' }}
            </template>
          </template>
        </a-table>
      </div>
    </a-card>

    <a-drawer v-model:open="detailState.open" title="审计详情" width="760" destroy-on-close>
      <a-spin :spinning="detailState.loading">
        <template v-if="detailState.record">
          <a-space direction="vertical" class="detail-layout" :size="16">
            <a-card size="small" title="基本信息">
              <a-descriptions :column="2" size="small" bordered>
                <a-descriptions-item label="审计 ID">{{ detailState.record.audit_id }}</a-descriptions-item>
                <a-descriptions-item label="请求 ID">{{ detailState.record.request_id || '-' }}</a-descriptions-item>
                <a-descriptions-item label="操作时间">{{ detailState.record.operated_at }}</a-descriptions-item>
                <a-descriptions-item label="操作用户">
                  {{ detailState.record.operator_username || '-' }} / {{ detailState.record.operator_name || '-' }}
                </a-descriptions-item>
                <a-descriptions-item label="审计等级">{{ getAuditLevelLabel(detailState.record.audit_level) }}</a-descriptions-item>
                <a-descriptions-item label="操作结果">{{ getResultLabel(detailState.record.action_result) }}</a-descriptions-item>
                <a-descriptions-item label="所属模块">{{ detailState.record.module_name || '-' }}</a-descriptions-item>
                <a-descriptions-item label="操作类型">{{ detailState.record.action_name || '-' }}</a-descriptions-item>
                <a-descriptions-item label="用户 IP">{{ detailState.record.operator_ip || '-' }}</a-descriptions-item>
                <a-descriptions-item label="终端信息">{{ detailState.record.operator_terminal || '-' }}</a-descriptions-item>
              </a-descriptions>
            </a-card>

            <a-card size="small" title="对象信息">
              <a-descriptions :column="2" size="small" bordered>
                <a-descriptions-item label="对象类型">{{ detailState.record.resource_type || '-' }}</a-descriptions-item>
                <a-descriptions-item label="对象名称">{{ detailState.record.resource_name || '-' }}</a-descriptions-item>
                <a-descriptions-item label="对象 ID">{{ detailState.record.resource_id || '-' }}</a-descriptions-item>
                <a-descriptions-item label="关联工单">{{ detailState.record.related_order_id || '-' }}</a-descriptions-item>
              </a-descriptions>
            </a-card>

            <a-card size="small" title="操作详情">
              <div class="detail-block">
                <div class="detail-block-title">操作摘要</div>
                <div class="detail-block-text">{{ detailState.record.summary || '-' }}</div>
              </div>
              <div class="detail-block">
                <div class="detail-block-title">请求参数</div>
                <pre class="detail-code">{{ prettyJson(detailState.record.request_payload) }}</pre>
              </div>
              <div class="detail-block">
                <div class="detail-block-title">变更前内容</div>
                <pre class="detail-code">{{ prettyJson(detailState.record.before_data) }}</pre>
              </div>
              <div class="detail-block">
                <div class="detail-block-title">变更后内容</div>
                <pre class="detail-code">{{ prettyJson(detailState.record.after_data) }}</pre>
              </div>
              <div class="detail-block">
                <div class="detail-block-title">SQL 内容</div>
                <pre class="detail-code">{{ detailState.record.sql_text || '-' }}</pre>
              </div>
              <div class="detail-block">
                <div class="detail-block-title">错误信息</div>
                <pre class="detail-code">{{ detailState.record.error_message || '-' }}</pre>
              </div>
            </a-card>
          </a-space>
        </template>
      </a-spin>
    </a-drawer>
  </div>
</template>

<script setup>
defineOptions({ name: 'AuditLogIndexView' })

import { DownOutlined, UpOutlined } from '@ant-design/icons-vue'
import { computed, inject, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { getAuditLogDetailApi, getAuditLogsApi } from '@/api/audit'

const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)
const tableWrapperRef = ref(null)
const scrollY = ref(380)

const updateScrollY = () => {
  if (tableWrapperRef.value) {
    const wrapper = tableWrapperRef.value
    const card = wrapper.closest?.('.audit-log-table-card')
    if (card) {
      const headerHeight = card.querySelector('.ant-card-head')?.offsetHeight ?? 0
      const bodyPadTop = 16
      const paginationEl = card.querySelector('.ant-spin-container > ul.ant-pagination, .ant-spin-container > div.ant-pagination')
      const paginationHeight = paginationEl?.offsetHeight ?? 60
      const extra = 42
      const available = card.clientHeight - headerHeight - bodyPadTop - paginationHeight - extra
      scrollY.value = available > 280 ? available : 280
    }
  }
}

const pad = (value) => String(value).padStart(2, '0')
const formatDateTime = (date) => {
  const value = date instanceof Date ? date : new Date(date)
  return `${value.getFullYear()}-${pad(value.getMonth() + 1)}-${pad(value.getDate())} ${pad(value.getHours())}:${pad(value.getMinutes())}:${pad(value.getSeconds())}`
}
const startOfDay = (date) => {
  const value = new Date(date)
  value.setHours(0, 0, 0, 0)
  return value
}
const endOfDay = (date) => {
  const value = new Date(date)
  value.setHours(23, 59, 59, 999)
  return value
}
const shiftDays = (baseDate, days) => {
  const value = new Date(baseDate)
  value.setDate(value.getDate() + days)
  return value
}

const timePresets = [
  { key: 'today', label: '今日' },
  { key: 'yesterday', label: '昨日' },
  { key: 'last7', label: '近 7 天' },
  { key: 'last30', label: '近 30 天' },
]

const getPresetRange = (presetKey) => {
  const now = new Date()
  if (presetKey === 'today') return [formatDateTime(startOfDay(now)), formatDateTime(now)]
  if (presetKey === 'yesterday') {
    const yesterday = shiftDays(now, -1)
    return [formatDateTime(startOfDay(yesterday)), formatDateTime(endOfDay(yesterday))]
  }
  if (presetKey === 'last30') return [formatDateTime(startOfDay(shiftDays(now, -29))), formatDateTime(now)]
  return [formatDateTime(startOfDay(shiftDays(now, -6))), formatDateTime(now)]
}

const createDefaultFilterForm = () => ({
  timeRange: getPresetRange('last7'),
  operatorUsername: '',
  auditLevel: undefined,
  moduleCode: undefined,
  keyword: '',
  actionResult: undefined,
})

const auditLevelOptions = [
  { label: '普通操作', value: 'normal' },
  { label: '高危操作', value: 'high' },
]

const resultOptions = [
  { label: '成功', value: 'success' },
  { label: '失败', value: 'fail' },
  { label: '被拒绝', value: 'rejected' },
  { label: '已取消', value: 'cancelled' },
]

const moduleOptions = [
  { label: '数据源管理', value: 'datasource' },
  { label: '数据授权', value: 'das_grant' },
  { label: 'SQL 查询', value: 'sql_query' },
  { label: 'SQL 收藏', value: 'sql_favorite' },
  { label: '数据库工单', value: 'db_order' },
  { label: '备份节点', value: 'backup_node' },
  { label: '备份任务', value: 'backup_task' },
  { label: '备份集', value: 'backup_set' },
  { label: '备份恢复', value: 'backup_restore' },
]

/* 列宽：删除【操作类型】列后，剩余 7 列平均分配 100% 宽度（≈14.29%/列），各列分布均匀。
   用百分比而非固定 px，保证表格在任何容器宽度下比例一致、不出现横向滚动。 */
const COL_WIDTH = `${(100 / 7).toFixed(2)}%`

const columns = [
  { title: '操作时间', dataIndex: 'operated_at', key: 'operated_at', width: COL_WIDTH, ellipsis: true },
  { title: '操作用户', dataIndex: 'operator_username', key: 'operator_username', width: COL_WIDTH },
  { title: '审计等级', dataIndex: 'audit_level', key: 'audit_level', width: COL_WIDTH },
  { title: '所属模块', dataIndex: 'module_name', key: 'module_name', width: COL_WIDTH },
  { title: '操作结果', dataIndex: 'action_result', key: 'action_result', width: COL_WIDTH },
  { title: '操作摘要', dataIndex: 'summary', key: 'summary', width: COL_WIDTH, ellipsis: true },
  { title: '操作', key: 'actions', width: COL_WIDTH, fixed: 'right' },
]

const filterForm = reactive(createDefaultFilterForm())
const activePreset = ref('last7')
const tableData = ref([])
const tableLoading = ref(false)
const total = ref(0)
const pagination = reactive({
  current: 1,
  pageSize: 10,
})
const detailState = reactive({
  open: false,
  loading: false,
  record: null,
})
const uiState = reactive({
  moreExpanded: false,
})

const toggleMore = (expanded) => {
  uiState.moreExpanded = typeof expanded === 'boolean' ? expanded : !uiState.moreExpanded
  nextTick(() => updateScrollY())
}

const buildQueryParams = () => ({
  start_time: filterForm.timeRange?.[0] || '',
  end_time: filterForm.timeRange?.[1] || '',
  operator_username: filterForm.operatorUsername || '',
  audit_level: filterForm.auditLevel || '',
  module_code: filterForm.moduleCode || '',
  keyword: filterForm.keyword.trim(),
  action_result: filterForm.actionResult || '',
  page: pagination.current,
  page_size: pagination.pageSize,
  is_page: true,
})

const fetchLogs = async () => {
  tableLoading.value = true
  try {
    const res = await getAuditLogsApi(buildQueryParams())
    tableData.value = Array.isArray(res?.data) ? res.data : []
    total.value = Number(res?.total || 0)
  } finally {
    tableLoading.value = false
  }
}

const openDetail = async (record) => {
  detailState.open = true
  detailState.loading = true
  detailState.record = null
  try {
    const res = await getAuditLogDetailApi(record.audit_id)
    detailState.record = res?.data || null
  } finally {
    detailState.loading = false
  }
}

const handleSearch = () => {
  pagination.current = 1
  fetchLogs()
}

const handleReset = () => {
  Object.assign(filterForm, createDefaultFilterForm())
  activePreset.value = 'last7'
  pagination.current = 1
  fetchLogs()
}

const applyPreset = (presetKey) => {
  activePreset.value = presetKey
  filterForm.timeRange = getPresetRange(presetKey)
  handleSearch()
}

const paginationConfig = computed(() => ({
  current: pagination.current,
  pageSize: pagination.pageSize,
  total: total.value,
  showSizeChanger: true,
  showLessItems: true,
  pageSizeOptions: ['10', '20', '50'],
  showTotal: (value) => `共 ${value} 条`,
  onChange: (page, pageSize) => {
    pagination.current = page
    pagination.pageSize = pageSize
    fetchLogs()
  },
  onShowSizeChange: (page, pageSize) => {
    pagination.current = page
    pagination.pageSize = pageSize
    fetchLogs()
  },
}))

const getAuditLevelLabel = (value) => (value === 'high' ? '高危操作' : '普通操作')
const getResultLabel = (value) => {
  if (value === 'success') return '成功'
  if (value === 'fail') return '失败'
  if (value === 'rejected') return '被拒绝'
  if (value === 'cancelled') return '已取消'
  return value || '-'
}

const prettyJson = (value) => {
  if (!value) return '-'
  if (typeof value === 'string') {
    try {
      return JSON.stringify(JSON.parse(value), null, 2)
    } catch {
      return value
    }
  }
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

onMounted(() => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(true)
  }
  nextTick(() => {
    updateScrollY()
    window.addEventListener('resize', updateScrollY)
  })
  fetchLogs()
})

onBeforeUnmount(() => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(false)
  }
  window.removeEventListener('resize', updateScrollY)
})
</script>

<style scoped>
.audit-log-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  gap: var(--gi-spacing-md);
  min-height: 0;
}

.audit-log-page > .gi-page-section {
  flex-shrink: 0;
}

.audit-log-table-card {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.audit-log-table-card :deep(.ant-card-body) {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  min-height: 0;
  padding: 16px 16px 6px 16px;
}

.audit-log-table-wrapper {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.audit-log-table-wrapper :deep(.ant-table-wrapper),
.audit-log-table-wrapper :deep(.ant-spin-nested-loading),
.audit-log-table-wrapper :deep(.ant-spin-container) {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}
.audit-log-table-wrapper :deep(.ant-spin-container > ul.ant-pagination),
.audit-log-table-wrapper :deep(.ant-spin-container > div.ant-pagination) {
  flex: 0 0 auto;
  margin: 4px 0 0;
  padding: 2px 0 2px;
}

.audit-filter-card :deep(.ant-card-body) {
  padding: 14px 20px 12px;
}

/* 表头：与备份模块等主表格页统一风格——浅底、轻量、单线分隔
   左右 padding 与表体单元格(16px)一致，保证表头与内容文字对齐
   字号 14px 为操作日志页统一要求，padding / line-height 与之匹配 */
.audit-log-table-card :deep(.ant-table-thead > tr > th) {
  padding: 12px 16px;
  background: #f8fbfd;
  color: #38505d;
  font-weight: 600;
  font-size: 14px;
  line-height: 22px;
  border-bottom: 1px solid #e7edf1;
}

/* 固定列（操作列）表头同底色，避免横向滚动时出现透明断层 */
.audit-log-table-card :deep(.ant-table-thead > tr > th.ant-table-cell-fix-right) {
  background: #f8fbfd;
}

.audit-filter {
  display: flex;
  flex-direction: column;
  gap: var(--gi-spacing-ssm);
  width: 100%;
  min-width: 0;
}

.filter-main-row {
  display: flex;
  align-items: center;
  /* 顶部常驻控件整体靠左对齐 */
  justify-content: flex-start;
  gap: var(--gi-spacing-sm);
  width: 100%;
  min-width: 0;
}

/* 预设时间按钮组：
   这里不用 a-space —— a-space 作为 flex item 实测会相对同行控件下移 4px
   （top 95 / bottom 127，而其它控件是 91 / 123），导致整行视觉上高低不齐。
   换成自带 inline-flex 容器后，高度与基线同其它控件完全一致。 */
.preset-btn-group {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: var(--gi-spacing-sm);
}

/* 统一筛选区所有控件（输入框 / 下拉框 / 时间选择器 / 按钮）的视觉基线：
   高度 32px、圆角 6px，与项目 tokens 对齐，避免各控件高低不齐、圆角不一。
   注意：只能约束外层容器（affix-wrapper / picker / selector / btn），
   直接给内部 input.ant-input 设 height 会破坏 affix-wrapper 的高度计算（实测会撑到 50px）。 */
.audit-filter :deep(.ant-input-affix-wrapper),
.audit-filter :deep(.ant-picker),
.audit-filter :deep(.ant-select-selector),
.audit-filter :deep(.ant-btn) {
  height: 32px;
  border-radius: var(--gi-radius-sm);
}

.audit-filter :deep(.ant-btn) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.audit-filter :deep(.ant-select-selector) {
  align-items: center;
}

/* 展开区与顶部行复用同一套控件宽度约束，保证展开后不会突然变宽/变窄 */
.filter-more-row :deep(.ant-select),
.filter-more-row :deep(.ant-input) {
  width: 100%;
}

.filter-control {
  min-width: 0;
}

/* 时间范围：两个「YYYY-MM-DD HH:mm:ss」+ 分隔符 + 图标需要约 380px 才能完整铺开，
   之前 340px 且允许收缩（flex-shrink:1）会把日期时间挤到换行/截断。
   这里固定 400px 且不参与收缩（flex-grow/shrink 均为 0），
   同行其它控件仍按自身尺寸排布，不会被时间框抢走宽度。 */
.filter-control--range {
  flex: 0 0 400px;
}

.filter-action-group {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: var(--gi-spacing-sm);
}

.more-toggle-btn {
  flex-shrink: 0;
  color: var(--gi-color-primary);
  font-weight: 500;
  padding: 4px 10px;
}

.more-toggle-btn--active {
  background: #F0FDFA;
}

/* 展开区用 24 列网格（8 与 6 的最小公倍数，便于同时表达 1/8 与 1/6）：
   - 操作用户 / 审计等级 / 所属模块 / 操作结果：各 span 3（= 行宽 1/8）
   - 关键字：span 4（= 行宽 1/6），相当于改造前 1/2 行宽的三分之一
   剩余 1/3 行宽留白，不再强行撑满整行。 */
.filter-more-row {
  padding-top: var(--gi-spacing-ssm);
  border-top: 1px dashed #EBEEF2;
  display: grid;
  grid-template-columns: repeat(24, minmax(0, 1fr));
  gap: var(--gi-spacing-sm);
  align-items: center;
}

.filter-control--half {
  grid-column: span 3;
}

.filter-control--keyword {
  grid-column: span 4;
}

.ellipsis-text,
.mono-text {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.mono-text {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
}

.detail-layout {
  width: 100%;
}

.detail-block + .detail-block {
  margin-top: 12px;
}

.detail-block-title {
  margin-bottom: 8px;
  font-size: 12px;
  font-weight: 600;
  color: #52606d;
}

.detail-block-text {
  font-size: 13px;
  line-height: 1.7;
  color: #1f2d38;
}

.detail-code {
  margin: 0;
  padding: 10px 12px;
  border-radius: 8px;
  background: #f7f9fb;
  border: 1px solid #edf1f5;
  color: #1f2d38;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}

/* 中等屏幕：24 列过窄，收成 12 列——半宽项各占 1/4 行宽，关键字独占整行 */
@media (max-width: 1199px) {
  .filter-more-row {
    grid-template-columns: repeat(12, minmax(0, 1fr));
  }
  .filter-control--half {
    grid-column: span 3;
  }
  .filter-control--keyword {
    grid-column: span 12;
  }
  /* 中等屏幕下单排不下全部常驻控件，允许换行，避免控件被压扁 */
  .filter-main-row {
    flex-wrap: wrap;
  }
}

@media (max-width: 767px) {
  .filter-main-row {
    flex-wrap: wrap;
  }
  .filter-control--range {
    flex: 1 1 100%;
    max-width: 100%;
  }
  .filter-more-row {
    grid-template-columns: minmax(0, 1fr);
  }
  /* 单列网格下必须显式收敛 span，否则 grid-column: span 4 会撑出隐式列 */
  .filter-control--half,
  .filter-control--keyword {
    grid-column: span 1;
  }
}

@media (max-width: 768px) {
  /* 7 列均分 100%，窄屏每列只有 53px 且没有横向滚动 */
  .audit-log-table-wrapper :deep(.ant-table-content) {
    overflow-x: auto;
  }

  .audit-log-table-wrapper :deep(.ant-table) {
    min-width: 980px;
  }

  .audit-log-table-card :deep(.ant-table-thead > tr > th),
  .audit-log-table-card :deep(.ant-table-tbody > tr > td) {
    padding: 10px 8px;
    font-size: 13px;
  }

  .preset-btn-group,
  .filter-action-group {
    flex-wrap: wrap;
    width: 100%;
  }

  .filter-action-group :deep(.ant-btn) {
    flex: 1 1 auto;
  }

  .detail-code {
    font-size: 11px;
    padding: 8px 10px;
  }
}
</style>
