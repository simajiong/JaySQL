<template>
  <div class="task-page">
    <a-card class="task-header-card" :bordered="false">
      <div class="task-header">
        <div>
          <div class="task-title">执行任务</div>
        </div>
        <a-space wrap>
          <a-button type="primary" :loading="uiState.executeLoading" @click="openExecuteModal">
            <PlayCircleOutlined />
            执行
          </a-button>
          <a-button @click="router.push({ name: 'orders.detail', params: { order_id: orderID } })">
            返回工单
          </a-button>
        </a-space>
      </div>
    </a-card>

    <!-- 执行计划提示：定时 / 手动模式下的状态与策略（立即执行时无提示） -->
    <a-alert
      v-if="executionPlan"
      class="task-plan-alert"
      :type="executionPlan.alertType"
      show-icon
      :message="executionPlan.title"
      :description="executionPlan.description"
    />

    <a-card title="执行结果" class="task-table-card">
      <a-space class="task-toolbar" size="middle" wrap>
        <a-select
          v-model:value="uiData.progress"
          :options="progressOptions"
          allow-clear
          style="width: 140px"
          placeholder="任务进度"
          @change="fetchData"
        />
        <a-input-search
          v-model:value="uiData.searchValue"
          placeholder="请输入 SQL 文本"
          style="width: 260px"
          allow-clear
          @search="handleSearch"
        />
      </a-space>

      <div class="task-table-wrapper">
        <a-table
          size="middle"
          :columns="uiData.tableColumns"
          :row-key="(record) => record.task_id"
          :data-source="uiData.tableData"
          :pagination="pagination"
          :loading="uiState.loading"
          :row-class-name="taskRowClass"
          @change="handleTableChange"
          :scroll="{ x: 1200 }"
        >
          <template #bodyCell="{ column, record, index }">
            <template v-if="column.key === 'index'">
              {{ index + 1 }}
            </template>

            <template v-else-if="column.key === 'progress'">
              <a-tag :color="getTaskStatusMeta(record.progress).color">
                {{ getTaskStatusMeta(record.progress).text }}
              </a-tag>
            </template>

            <template v-else-if="column.key === 'sql'">
              <a-tooltip v-if="rowTargetName(record)" :title="`属于对象 ${rowTargetName(record)} 的建表链任务`">
                <span class="task-target-tag">{{ rowTargetName(record) }}</span>
              </a-tooltip>
              <a class="sql-link" @click="showSqlDetail(record)">
                {{ record.sql }}
              </a>
            </template>

            <template v-else-if="column.key === 'started_at'">
              <span>{{ getTaskStartTime(record) }}</span>
            </template>

            <template v-else-if="column.key === 'finished_at'">
              <span>{{ getTaskEndTime(record) }}</span>
            </template>

            <template v-else-if="column.key === 'result'">
              <span>{{ getExecutionDetail(record) }}</span>
            </template>

            <template v-else-if="column.key === 'backup'">
              <span>{{ getBackupStatus(record) }}</span>
            </template>

            <template v-else-if="column.key === 'action'">
              <a-space>
                <a class="action-link" @click="showTaskResult(record)">
                  <FileSearchOutlined />
                  详情
                </a>
                <a v-if="record.progress !== 'COMPLETED'" class="action-link" @click="executeSingleTask(record)">
                  <PlayCircleOutlined />
                  单条执行
                </a>
              </a-space>
            </template>
          </template>
        </a-table>
      </div>
    </a-card>

    <a-modal v-model:open="uiState.sqlPreviewOpen" title="SQL 语句" width="55%" :footer="null" @cancel="handleSqlPreviewCancel">
      <highlightjs language="sql" :code="uiData.sql" />
    </a-modal>

    <a-modal :open="uiState.executeModalOpen" title="执行 SQL 任务" @cancel="closeExecuteModal">
      <a-form ref="executeFormRef" :model="executeForm" layout="vertical">
        <a-form-item label="错误处理" name="error_policy" :rules="[{ required: true, message: '请选择错误处理方式' }]">
          <a-radio-group v-model:value="executeForm.error_policy">
            <a-radio value="STOP">执行出错后，终止任务</a-radio>
            <a-radio value="CONTINUE">执行出错后，继续任务</a-radio>
            <a-radio value="ROLLBACK">执行出错后，回滚任务</a-radio>
          </a-radio-group>
        </a-form-item>

        <a-form-item label="备份失败处理" name="backup_policy" :rules="[{ required: true, message: '请选择备份失败处理方式' }]">
          <a-radio-group v-model:value="executeForm.backup_policy">
            <a-radio value="CONTINUE">备份失败后，继续任务</a-radio>
            <a-radio value="STOP">备份失败后，停止任务</a-radio>
          </a-radio-group>
        </a-form-item>

        <a-form-item label="执行方式" name="execution_mode" :rules="[{ required: true, message: '请选择执行方式' }]">
          <a-radio-group v-model:value="executeForm.execution_mode">
            <a-radio value="IMMEDIATE">立即执行</a-radio>
            <a-radio value="SCHEDULED">定时执行</a-radio>
            <a-radio value="MANUAL">手动执行</a-radio>
          </a-radio-group>
        </a-form-item>

        <a-form-item
          v-if="executeForm.execution_mode === 'SCHEDULED'"
          label="定时执行时间"
          name="scheduled_at"
          :rules="[{ required: true, message: '请选择定时执行时间' }]"
        >
          <a-date-picker
            v-model:value="uiData.scheduledAt"
            show-time
            value-format="YYYY-MM-DD HH:mm:ss"
            :disabled-date="disabledScheduledDate"
            class="full-width"
          />
          <div class="scheduled-hint">到点后由平台调度器自动触发（每分钟扫描一次），无需人工干预。</div>
        </a-form-item>
      </a-form>

      <template #footer>
        <a-button @click="closeExecuteModal">取消</a-button>
        <a-button type="primary" :loading="uiState.executeLoading" @click="submitExecute">
          确定
        </a-button>
      </template>
    </a-modal>

    <TaskResult
      :open="uiState.taskResultOpen"
      v-model:modelValue="taskResultData"
      @update:open="uiState.taskResultOpen = $event"
    />
  </div>
</template>

<script setup>
import { executeTaskApi, executebatchTasksApi, getOrderDetailApi, getOrderTasksApi } from '@/api/order'
import { TASK_PROGRESS_OPTIONS, getTaskStatusMeta } from '@/views/orders/shared/orderStatusMeta'
import { resolveStatementGroup } from '@/views/orders/shared/statementGroups'
import { FileSearchOutlined, PlayCircleOutlined } from '@ant-design/icons-vue'
import { useIntervalFn, useThrottleFn } from '@vueuse/core'
import { message } from 'ant-design-vue'
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import TaskResult from './TaskResult.vue'

const route = useRoute()
const router = useRouter()
const orderID = route.params.order_id
const progressOptions = TASK_PROGRESS_OPTIONS
const taskResultData = ref({})
const executeFormRef = ref()

/* 工单执行计划（执行方式 / 策略）：用于展示「定时执行」「手动执行」的当前状态。
   历史问题：执行方式此前只停留在弹窗里、服务端不消费，三种选择行为一致；
   现在定时/手动会落库，页面必须把这个状态显示出来，否则用户以为「点了没反应」。*/
const orderInfo = ref({})
const ERROR_POLICY_LABEL = {
  STOP: '执行出错后终止任务',
  CONTINUE: '执行出错后继续任务',
  ROLLBACK: '执行出错后回滚任务',
}
const BACKUP_POLICY_LABEL = {
  CONTINUE: '备份失败后继续任务',
  STOP: '备份失败后停止任务',
}

const policySummary = computed(() => {
  const config = orderInfo.value?.execution_config || {}
  const errorLabel = ERROR_POLICY_LABEL[config.error_policy] || ERROR_POLICY_LABEL.STOP
  const backupLabel = BACKUP_POLICY_LABEL[config.backup_policy] || BACKUP_POLICY_LABEL.STOP
  return `错误处理「${errorLabel}」· 备份失败处理「${backupLabel}」`
})

// 已结束的工单不再提示执行计划
const TERMINAL_ORDER_PROGRESS = ['COMPLETED', 'TERMINATED', 'WITHDRAWN', 'REJECTED']

const executionPlan = computed(() => {
  const progress = orderInfo.value?.progress
  const mode = orderInfo.value?.execution_mode
  if (!progress || TERMINAL_ORDER_PROGRESS.includes(progress)) return null
  // 定时执行：调度器只扫描 APPROVED / FAILED 的到期工单，只有这两个状态下提示才有意义
  if (mode === 'SCHEDULED' && (progress === 'APPROVED' || progress === 'FAILED') && orderInfo.value?.scheduled_at) {
    return {
      alertType: 'warning',
      title: `已排定定时执行：${orderInfo.value.scheduled_at}`,
      description: `到期后由平台调度器自动触发，无需人工干预。执行策略：${policySummary.value}。如需立刻执行，点右上角「执行」并选择「立即执行」覆盖本次计划。`,
    }
  }
  // 手动执行：部分任务执行后工单会显示「执行中」，此时仍要保留提示，
  // 否则用户会以为剩余任务已经在自动跑了
  if (mode === 'MANUAL') {
    const partial = progress === 'EXECUTING'
    return {
      alertType: 'info',
      title: partial ? '手动执行模式：部分任务已执行，其余仍需手动触发' : '手动执行模式：整单不会自动执行',
      description: `请在下方任务列表逐条点击「单条执行」触发。执行策略：${policySummary.value}。`,
    }
  }
  return null
})

// 定时执行时间不允许选过去（服务端同样做校验，这里给即时反馈）。
// antd 4 的 date-picker 传进来的是 dayjs 对象，这里兼容 dayjs / Date 两种形态。
const disabledScheduledDate = (current) => {
  if (!current) return false
  const todayStart = new Date(new Date().setHours(0, 0, 0, 0)).getTime()
  const value = typeof current.toDate === 'function' ? current.toDate() : new Date(current)
  return value.getTime() < todayStart
}

const fetchOrderInfo = async () => {
  const res = await getOrderDetailApi({ order_id: orderID }).catch(() => null)
  orderInfo.value = res?.data || {}
}

useIntervalFn(
  () => {
    fetchData()
    // 同步刷新工单执行计划：定时工单到期被执行后（progress 变为 EXECUTING/COMPLETED），
    // 计划提示应自动消失，无需用户刷新页面
    fetchOrderInfo()
  },
  10000,
  { immediate: true },
)

const uiState = reactive({
  loading: false,
  sqlPreviewOpen: false,
  taskResultOpen: false,
  executeModalOpen: false,
  executeLoading: false,
})

const uiData = reactive({
  searchValue: '',
  progress: null,
  sql: '',
  scheduledAt: '',
  tableData: [],
  tableColumns: [
    { title: '行号', key: 'index', width: 72, fixed: 'left' },
    // 执行详情列宽减半（原为自适应剩余宽度），释放的宽度全部分配给 SQL 文本列
    { title: 'SQL文本', dataIndex: 'sql', key: 'sql', ellipsis: true, width: 440 },
    // 任务级真实执行时间（started_at/finished_at 由执行循环随进度流转写入）：
    // created_at 是任务生成时刻，updated_at 是行最后更新时刻，拿它们当开始/结束
    // 时间会让待执行任务显示成「开始=结束」的假象
    { title: '开始时间', dataIndex: 'started_at', key: 'started_at', width: 180 },
    { title: '结束时间', dataIndex: 'finished_at', key: 'finished_at', width: 180 },
    { title: '执行状态', dataIndex: 'progress', key: 'progress', width: 120 },
    { title: '执行详情', key: 'result', ellipsis: true, width: 240 },
    { title: '备份状态', key: 'backup', width: 120 },
    { title: '操作', key: 'action', width: 160, fixed: 'right' },
  ],
})

const executeForm = reactive({
  error_policy: 'STOP',
  backup_policy: 'STOP',
  execution_mode: 'IMMEDIATE',
  scheduled_at: '',
})

const pagination = reactive({
  current: 1,
  pageSize: 10,
  total: 0,
  pageSizeOptions: ['10', '20', '50', '100'],
  showSizeChanger: true,
})

const parseResult = (record) => {
  if (!record?.result) return {}
  if (typeof record.result === 'object') return record.result
  try {
    return JSON.parse(record.result)
  } catch {
    return {}
  }
}

// 执行详情必须与执行状态同口径：默认文案由 progress 决定，
// 只有任务真的到了终态才能显示「执行成功」，避免待执行任务
// 在 result 为空时被兜底文案误标成「执行成功」
const getExecutionDetail = (record) => {
  const result = parseResult(record)
  const detail = result.error || result.execute_log
  if (detail) return detail
  if (record.progress === 'COMPLETED') return '执行成功'
  if (record.progress === 'FAILED') return '执行失败'
  if (record.progress === 'EXECUTING') return '执行中…'
  return '-'
}

const getBackupStatus = (record) => {
  // 未执行的任务没有备份动作可言，显示 '-' 而不是误标成「已跳过」
  if (record.progress === 'PENDING') return '-'
  const result = parseResult(record)
  if (result.backup_status) return result.backup_status
  if (result.backup_cost_time) return '备份成功'
  return '已跳过'
}

// 时间列：新数据用任务级 started_at/finished_at；存量任务两列为空，
// 已执行的老任务回退 updated_at（终态写入时恰好是结束时刻），
// 未执行的老任务显示 '-'
const getTaskStartTime = (record) => record.started_at || record.created_at || '-'
const getTaskEndTime = (record) => {
  if (record.finished_at) return record.finished_at
  if (record.progress === 'COMPLETED' || record.progress === 'FAILED') return record.updated_at || '-'
  return '-'
}

/* 对象归属标注：Oracle 一条建表链（CREATE TABLE + COMMENT ON × N + 索引）会被拆成
   多条任务，逐条平铺后看不出结构。这里给链内任务标注所属对象名，并在组首行
   （CREATE TABLE）加分隔线，让「一张表一段」一眼可辨。
   不做折叠：任务列表是服务端分页 + 10s 轮询刷新，折叠会跨页断裂且状态反复重置；
   完整的组级折叠需后端按组分页，留待后续迭代。 */
const rowTargetName = (record) => resolveStatementGroup(record?.sql)?.name || ''

const taskRowClass = (record) => {
  const target = resolveStatementGroup(record?.sql)
  return target?.kind === 'create_table' ? 'task-row-group-start' : ''
}

const handleSearch = (value) => {
  uiData.searchValue = value
  pagination.current = 1
  fetchData()
}

const handleTableChange = (pager) => {
  pagination.current = pager.current
  pagination.pageSize = pager.pageSize
  fetchData()
}

const fetchData = async () => {
  uiState.loading = true
  const params = {
    page_size: pagination.pageSize,
    page: pagination.current,
    is_page: true,
    search: uiData.searchValue,
    progress: uiData.progress,
    order_id: orderID,
  }
  const res = await getOrderTasksApi(params).catch(() => null)
  if (res) {
    pagination.total = res.total
    uiData.tableData = res.data || []
  }
  uiState.loading = false
}

const showSqlDetail = (record) => {
  uiData.sql = record.sql || ''
  uiState.sqlPreviewOpen = true
}

const handleSqlPreviewCancel = () => {
  uiState.sqlPreviewOpen = false
}

const showTaskResult = (record) => {
  taskResultData.value = {
    ...record,
    result: parseResult(record),
  }
  uiState.taskResultOpen = true
}

const openExecuteModal = () => {
  uiState.executeModalOpen = true
}

const closeExecuteModal = () => {
  uiState.executeModalOpen = false
  executeFormRef.value?.clearValidate?.()
}

const buildExecutePayload = () => ({
  order_id: orderID,
  error_policy: executeForm.error_policy,
  backup_policy: executeForm.backup_policy,
  execution_mode: executeForm.execution_mode,
  scheduled_at: executeForm.execution_mode === 'SCHEDULED' ? uiData.scheduledAt : '',
})

// 单条执行固定按「立即执行」提交。
// 弹窗里的执行方式可能残留未提交的 SCHEDULED 选择，若沿用会出现
// 「点了单条执行却只是登记了定时计划、任务没动」的错觉。
const buildSingleTaskPayload = (taskId) => ({
  order_id: orderID,
  task_id: taskId,
  error_policy: executeForm.error_policy,
  backup_policy: executeForm.backup_policy,
  execution_mode: 'IMMEDIATE',
  scheduled_at: '',
})

const executeSingleTask = useThrottleFn(async (record) => {
  uiState.executeLoading = true
  try {
    const res = await executeTaskApi(buildSingleTaskPayload(record.task_id)).catch(() => null)
    if (!res) return
    message.success('任务已开始执行')
    fetchData()
    fetchOrderInfo()
  } finally {
    uiState.executeLoading = false
  }
})

const submitExecute = useThrottleFn(async () => {
  executeForm.scheduled_at = uiData.scheduledAt
  await executeFormRef.value?.validate?.()

  // 定时执行必须晚于当前时间：服务端会再次校验，这里提前给出明确提示
  if (executeForm.execution_mode === 'SCHEDULED') {
    const planned = new Date(String(uiData.scheduledAt || '').replace(/-/g, '/'))
    if (Number.isNaN(planned.getTime()) || planned.getTime() <= Date.now()) {
      message.warning('定时执行时间必须晚于当前时间')
      return
    }
  }

  uiState.executeLoading = true
  try {
    const res = await executebatchTasksApi(buildExecutePayload()).catch(() => null)
    if (!res) return
    if (executeForm.execution_mode === 'SCHEDULED') {
      message.success(`已登记为定时执行：${uiData.scheduledAt}，到期自动触发`)
    } else if (executeForm.execution_mode === 'MANUAL') {
      message.success('已登记为手动执行，请在任务列表逐条点「单条执行」触发')
    } else {
      message.success('执行任务已提交')
    }
    closeExecuteModal()
    fetchData()
    fetchOrderInfo()
  } finally {
    uiState.executeLoading = false
  }
})

onMounted(() => {
  fetchData()
  fetchOrderInfo()
})
</script>

<style scoped>
.task-page {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.task-header-card :deep(.ant-card-body) {
  padding: 18px 20px;
}

/* 执行结果卡片：搜索框整体向上移 5px —— 减少 card-body 顶部 padding 5px（AntD 默认 24px → 19px），
   保持工具栏下方 margin-bottom=12px 不变，避免搜索框下边框被表格挡住 */
.task-table-card :deep(.ant-card-body) {
  padding-top: 19px;
}

.task-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.task-title {
  font-size: 18px;
  font-weight: 600;
  color: #1f2329;
}

.task-subtitle {
  margin-top: 4px;
  color: #8c8c8c;
  font-size: 13px;
}

.task-toolbar {
  /* 两个方框保持原位置不动，不调整这里的间距 */
  margin-bottom: 20px;
  margin-top: 0;
}

.task-table-wrapper {
  /* 表格再下移 6px：从 14px → 20px，累计下移 16px（4+10+6） */
  margin-top: 20px;
}

.task-table-wrapper :deep(.ant-table) {
  margin-top: 0;
}

.sql-link {
  display: inline-block;
  max-width: calc(100% - 40px);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  /* antd 的 a 走 colorLink 不吃主题色，这里显式覆盖为主题色 */
  color: var(--gi-color-primary);
  vertical-align: middle;
}

/* 建表链对象标注：同一张表的 CREATE TABLE / COMMENT ON / 索引任务共用同一标注 */
.task-target-tag {
  display: inline-block;
  max-width: 120px;
  margin-right: 6px;
  padding: 0 6px;
  border-radius: 4px;
  background: #f2f3f5;
  color: #5f5e5a;
  font-size: 12px;
  line-height: 18px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}

/* 建表链组首行（CREATE TABLE）：与上一张表的任务在视觉上分段 */
.task-table-wrapper :deep(tr.task-row-group-start > td) {
  border-top: 2px solid #e5e6eb;
}

.sql-link:hover {
  color: var(--gi-color-primary-hover);
}

.action-link {
  /* 操作列链接统一主题色（图标随 currentColor 继承） */
  color: var(--gi-color-primary);
}

.action-link:hover {
  color: var(--gi-color-primary-hover);
}

.full-width {
  width: 100%;
}

/* 执行计划提示条：与上下卡片保持同样的间距节奏 */
.task-plan-alert {
  margin: 0;
}

.scheduled-hint {
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.6;
  color: #8c8c8c;
}

@media (max-width: 992px) {
  .task-header {
    flex-direction: column;
    align-items: flex-start;
  }
}

@media (max-width: 768px) {
  .task-toolbar {
    margin-bottom: 12px;
    width: 100%;
  }

  .task-toolbar :deep(.ant-space-item) {
    width: 100%;
  }

  .task-toolbar :deep(.ant-select),
  .task-toolbar :deep(.ant-input-search) {
    width: 100% !important;
  }

  .task-page {
    gap: 8px;
  }

  .task-header-card :deep(.ant-card-body) {
    padding: 12px 14px;
  }

  .action-link {
    display: inline-flex;
    align-items: center;
    padding: 4px 0;
  }
}
</style>
