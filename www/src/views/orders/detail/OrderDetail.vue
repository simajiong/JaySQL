<template>
  <div class="order-detail-page gi-page-shell">
    <a-card class="detail-top-card" :bordered="false">
      <div class="steps-shell">
        <a-steps :current="currentStep" size="small">
          <a-step title="提交任务" />
          <a-step title="规范预审">
            <template #description>
              <div
                v-if="standardLinkVisible"
                class="order-step-standard-hint"
              >
                <a
                  :href="standardDetailHref"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="order-step-standard-link"
                >
                  查阅规范
                </a>
              </div>
            </template>
          </a-step>
          <a-step title="提交审批" />
          <a-step title="执行任务" />
          <a-step title="完成" />
        </a-steps>
      </div>

      <div class="detail-header">
        <div class="detail-title-block">
          <header-content :order-detail="orderDetail" compact />
        </div>
        <div class="header-action-slot">
          <header-extra :order-detail="orderDetail" :inspect-data="preInspectState" @refresh="refresh" />
        </div>
      </div>
    </a-card>

    <a-card
      size="small"
      class="detail-section-card preinspect-card"
    >
      <a-spin :spinning="preInspectState.loading">
        <a-tabs v-model:activeKey="preInspectTab" class="preinspect-tabs">
          <!-- Tab：执行结果（执行场景时置首），每条 SQL 一行执行记录 -->
          <a-tab-pane v-if="hasExecutionTasks" key="exec" tab="执行结果">
            <a-table
              row-key="task_id"
              size="small"
              :columns="execResultColumns"
              :data-source="orderTasks"
              :pagination="execResultPagination"
            >
              <template #bodyCell="{ column, record, index }">
                <template v-if="column.key === 'seq'">
                  {{ index + 1 }}
                </template>
                <template v-else-if="column.key === 'sql'">
                  <div class="exec-sql-wrap">
                    <span class="exec-sql-text" :title="record.sql">{{ record.sql }}</span>
                    <!-- 原「详情」列的结果摘要并入 SQL 下方副行（tooltip 保留完整 execute_log），数据不丢失 -->
                    <span
                      v-if="taskDetail(record)"
                      class="exec-sql-summary"
                      :class="{ 'is-failed': taskFailed(record) }"
                      :title="taskDetailFull(record)"
                    >
                      {{ taskDetail(record) }}
                    </span>
                  </div>
                </template>
                <template v-else-if="column.key === 'start_time'">
                  {{ taskStartEnd(record).start || '-' }}
                </template>
                <template v-else-if="column.key === 'end_time'">
                  {{ taskStartEnd(record).end || '-' }}
                </template>
                <template v-else-if="column.key === 'backup'">
                  <!-- 备份状态：与任务页同口径（backup_status 文案，缺失时回退 backup_cost_time） -->
                  <span :class="['exec-backup-status', backupStatusClass(record)]">
                    {{ backupStatusText(record) || '-' }}
                  </span>
                </template>
                <template v-else-if="column.key === 'action'">
                  <a-space size="middle">
                    <!-- 失败任务：红色「失败原因」入口替代原详情列的失败标签，点击打开失败抽屉 -->
                    <a v-if="taskFailed(record)" class="exec-failure-link" @click="openFailureDrawer(record)">
                      失败原因
                    </a>
                    <a class="exec-detail-link" @click="showTaskResult(record)">详情</a>
                    <!-- 回滚 SQL 入口：仅备份成功（有回滚 SQL）的任务渲染，避免死链接 -->
                    <a v-if="taskRollbackSql(record)" class="exec-rollback-link" @click="showTaskResult(record)">
                      回滚 SQL
                    </a>
                  </a-space>
                </template>
              </template>
            </a-table>
          </a-tab-pane>

          <!-- Tab：规范预审，每条完整 SQL 一行（读取提交时的预审快照） -->
          <a-tab-pane key="review" tab="规范预审">
            <div v-if="preInspectState.error" class="preinspect-error">
              <!-- 语法错误由后端作为结构化问题项返回（category=syntax_error），
                   走到这里的一定是真实的系统错误（连不上库/权限不足/快照读取失败），
                   不能再把用户的 SQL 错误说成「快照读取失败」。 -->
              <a-alert type="error" show-icon message="规范预审执行失败" :description="preInspectState.error" />
            </div>
            <template v-else-if="preInspectState.data">
              <!-- 致命错误汇总：语法错误 / 对象已存在 / 对象不存在 —— 执行必然失败，置顶提示 -->
              <div v-if="fatalIssueSummary" class="preinspect-fatal-alert">
                <a-alert type="error" show-icon :message="fatalIssueSummary.title" :description="fatalIssueSummary.description" />
              </div>
              <a-table
                v-if="!preInspectSnapshotMissing"
                ref="preInspectTableRef"
                row-key="seq"
                size="small"
                table-layout="fixed"
                :columns="preInspectColumns"
                :data-source="preInspectDisplayRows"
                :pagination="false"
                :show-expand-column="false"
                :expanded-row-keys="preInspectExpandedKeys"
                :custom-row="preInspectRowProps"
                @expanded-rows-change="(keys) => (preInspectExpandedKeys = keys)"
              >
                <template #bodyCell="{ column, record }">
                  <template v-if="column.key === 'seq'">
                    <span v-if="record.isGroupHeader" class="preinspect-group-seq">
                      组 {{ groupOrderIndex.get(record.groupKey) }}
                    </span>
                    <template v-else>{{ displaySeqOf(record.seq) }}</template>
                  </template>
                  <template v-if="column.key === 'sql'">
                    <template v-if="record.isGroupHeader">
                      <span class="preinspect-group-head" @click.stop="toggleGroupCollapse(record.groupKey)">
                        <CaretRightOutlined :class="['preinspect-group-caret', { 'is-open': !record.collapsed }]" />
                        <span class="preinspect-group-name">{{ record.group.name }}</span>
                        <span class="preinspect-group-summary">
                          {{ record.group.summary }} · {{ record.group.seqs.length }} 条语句
                        </span>
                      </span>
                    </template>
                    <template v-else>
                      <div class="preinspect-sql-cell">
                        <div class="preinspect-sql-main" :title="record.query">
                          <div
                            v-for="(comment, ci) in statementComments(record.query)"
                            :key="ci"
                            class="preinspect-sql-comment"
                          >
                            {{ comment }}
                          </div>
                          <span :class="['preinspect-sql-text', { 'is-member': record.isGroupMember }]">
                            {{ truncateSql(statementCode(record.query)) }}
                          </span>
                        </div>
                        <a-tooltip v-if="rowGroupHintCount(record)" :title="rowGroupHintTitle(record)">
                          <span class="preinspect-group-badge">组 {{ rowGroupHintCount(record) }}</span>
                        </a-tooltip>
                        <a-tooltip title="复制完整 SQL">
                          <CopyOutlined class="preinspect-copy-icon" @click.stop="copyTextToClipboard(record.query)" />
                        </a-tooltip>
                      </div>
                    </template>
                  </template>
                  <template v-else-if="column.key === 'audit'">
                    <span class="preinspect-audit-group">
                      <a-tooltip :title="auditTooltip(record.must.length, '必须改进')">
                        <span
                          :class="['preinspect-audit-chip', { 'is-empty': !record.must.length, 'is-active': isRowExpanded(record) }]"
                          @click.stop="toggleRowExpand(record)"
                        >
                          <CloseCircleFilled class="preinspect-audit-icon preinspect-audit-icon--must" />
                          <span class="preinspect-audit-num">{{ record.must.length }}</span>
                        </span>
                      </a-tooltip>
                      <a-tooltip :title="auditTooltip(record.suggest.length, '建议改进')">
                        <span
                          :class="['preinspect-audit-chip', { 'is-empty': !record.suggest.length, 'is-active': isRowExpanded(record) }]"
                          @click.stop="toggleRowExpand(record)"
                        >
                          <WarningFilled class="preinspect-audit-icon preinspect-audit-icon--suggest" />
                          <span class="preinspect-audit-num">{{ record.suggest.length }}</span>
                        </span>
                      </a-tooltip>
                    </span>
                  </template>
                  <template v-else-if="column.key === 'result'">
                    <span
                      class="preinspect-result-tag"
                      :class="`preinspect-result-tag--${rowAuditResult(record).key}`"
                    >
                      <CheckCircleFilled
                        v-if="rowAuditResult(record).key === 'passed'"
                        class="preinspect-result-icon"
                      />
                      <WarningFilled
                        v-else-if="rowAuditResult(record).key === 'warn'"
                        class="preinspect-result-icon"
                      />
                      <CloseCircleFilled v-else class="preinspect-result-icon" />
                      {{ rowAuditResult(record).label }}
                    </span>
                  </template>
                </template>
                <template #expandedRowRender="{ record }">
                  <div class="preinspect-expand-body">
                    <div
                      v-for="(item, idx) in expandedItems(record)"
                      :key="idx"
                      class="preinspect-expand-line"
                    >
                      <span class="preinspect-expand-index">{{ idx + 1 }}).</span>
                      <span
                        :class="[
                          'preinspect-expand-level',
                          item.level === 'must' ? 'preinspect-expand-level--must' : 'preinspect-expand-level--suggest',
                        ]"
                      >
                        {{ item.level === 'must' ? '必须改进' : '建议改进' }}
                      </span>
                      <span class="preinspect-expand-separator">：</span>
                      <span class="preinspect-expand-text" v-html="formatAuditText(item)"></span>
                    </div>
                  </div>
                </template>
              </a-table>

              <!-- 跨语句提示：不挂在任何单条语句上的组级规则结论（如「多条 ALTER 建议合并」） -->
              <div
                v-if="!preInspectSnapshotMissing && groupHints.list.length"
                :class="['preinspect-group-hint', { 'is-must': groupHints.mustCount > 0, 'is-open': groupHintOpen }]"
              >
                <div class="preinspect-group-hint-head" @click="toggleGroupHint">
                  <span class="preinspect-group-hint-title">跨语句提示</span>
                  <span class="preinspect-group-hint-toggle">{{ groupHintOpen ? '收起' : '展开' }}</span>
                </div>
                <div v-show="groupHintOpen" class="preinspect-group-hint-body">
                  <div v-for="hint in groupHints.list" :key="hint.key" class="preinspect-hint-item">
                    <div class="preinspect-hint-msg">
                      <span :class="['preinspect-hint-level', `preinspect-hint-level--${hint.level}`]">
                        {{ hint.level === 'must' ? '必须改进' : '建议改进' }}
                      </span>
                      <span class="preinspect-hint-text" v-html="renderInlineCode(hint.message)"></span>
                      <span v-if="hint.occurrences > 1" class="preinspect-hint-times">×{{ hint.occurrences }}</span>
                      <span v-if="hint.ruleName" class="preinspect-hint-rule">规则：{{ hint.ruleName }}</span>
                    </div>
                    <div class="preinspect-hint-meta">
                      <span class="preinspect-hint-meta-label">涉及语句：</span>
                      <template v-if="hint.relatedSeqs.length">
                        <a-tooltip
                          v-for="seq in hint.relatedSeqs"
                          :key="seq"
                          :title="`点击定位到第 ${displaySeqOf(seq)} 条语句`"
                        >
                          <span class="preinspect-hint-rel" @click="locateStatement(seq)">#{{ displaySeqOf(seq) }}</span>
                        </a-tooltip>
                      </template>
                      <span v-else class="preinspect-hint-meta-empty">未匹配到具体语句</span>
                    </div>
                  </div>
                </div>
              </div>
              <a-empty v-if="preInspectSnapshotMissing" :description="preInspectEmptyText" />
            </template>
            <a-empty v-else description="暂无预审数据" />
          </a-tab-pane>

          <!-- Tab：SQL 内容，完整展示工单提交时的 SQL -->
          <a-tab-pane key="sql" tab="SQL 内容">
            <div class="sql-text-box">
              <div class="sql-text-toolbar">
                <span class="sql-text-meta">{{ selectedSqlMeta }}</span>
                <span class="sql-text-actions">
                  <a-tooltip title="复制完整 SQL">
                    <CopyOutlined class="sql-text-action-icon" @click="copyTextToClipboard(selectedSql)" />
                  </a-tooltip>
                  <a-tooltip title="查看详情">
                    <EyeOutlined class="sql-text-action-icon" @click="openSqlDetailModal" />
                  </a-tooltip>
                </span>
              </div>
              <div class="task-sql-editor">
                <CodeMirror ref="tabSqlRef" :initVal="selectedSql" height="240px" />
              </div>
            </div>
          </a-tab-pane>
        </a-tabs>
      </a-spin>
    </a-card>

    <a-card size="small" title="操作历史" class="detail-section-card">
      <a-table
        row-key="id"
        size="small"
        :columns="historyColumns"
        :data-source="orderLogs"
        :pagination="false"
        :scroll="{ x: 760 }"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'action'">
            <span class="history-action-text">{{ logActionMeta(record.msg, orderDetail.progress).label }}</span>
          </template>
        </template>
      </a-table>
    </a-card>

    <!-- SQL 详情弹窗：行号+高亮只读展示，支持换行开关与复制 -->
    <a-modal
      v-model:open="sqlDetailModalOpen"
      :width="'72%'"
      :footer="null"
      centered
      wrap-class-name="sql-detail-modal"
    >
      <div class="sql-modal-toolbar">
        <span class="sql-modal-meta">{{ selectedSqlMeta }}</span>
        <span class="sql-modal-actions">
          <a class="sql-modal-wrap-toggle" @click="sqlWrapEnabled = !sqlWrapEnabled">
            {{ sqlWrapEnabled ? '关闭换行' : '开启换行' }}
          </a>
          <a-tooltip title="复制完整 SQL">
            <CopyOutlined class="sql-modal-copy-icon" @click="copyTextToClipboard(selectedSql)" />
          </a-tooltip>
        </span>
      </div>
      <div :class="['sql-modal-editor', { 'is-wrap': sqlWrapEnabled }]">
        <CodeMirror ref="modalSqlRef" :initVal="selectedSql" height="58vh" />
      </div>
      <div class="sql-modal-footer">
        <a-button @click="sqlDetailModalOpen = false">关 闭</a-button>
      </div>
    </a-modal>

    <!-- 任务详情弹窗：复用任务页同款 TaskResult（SQL 全文/执行信息/执行日志/回滚 SQL），
         让「执行成功想回滚」的用户在工单详情页就能拿到回滚 SQL —— 任务页路由是隐藏的，
         已完成的工单此前没有任何可点击入口能到达它 -->
    <TaskResult :open="taskResultOpen" v-model:modelValue="taskResultData" @update:open="taskResultOpen = $event" />

    <!-- 执行失败详情抽屉：结构化展示失败原因，并给出真正可行的恢复出口 -->
    <a-drawer
      v-model:open="failureDrawerOpen"
      title="执行失败详情"
      placement="right"
      :width="620"
    >
      <template v-if="currentFailure">
        <inspect-error-card class="failure-drawer-card" :item="currentFailureItem" />

        <div class="failure-drawer-section">
          <div class="failure-drawer-section-title">失败语句</div>
          <pre class="failure-drawer-sql">{{ currentFailure.sql || '-' }}</pre>
        </div>

        <div class="failure-drawer-section">
          <div class="failure-drawer-section-title">
            完整错误信息
            <a class="failure-drawer-copy" @click="copyTextToClipboard(currentFailureRawError)">复制</a>
          </div>
          <pre class="failure-drawer-raw">{{ currentFailureRawError || '-' }}</pre>
        </div>

        <a-alert
          class="failure-drawer-alert"
          type="warning"
          show-icon
          message="执行失败的工单不支持原地修改 SQL"
          description="「重新执行」会用同一段 SQL 重跑，未修正前必然再次失败。请复制为新工单，改好 SQL 后重新走审批流程。"
        />
      </template>

      <template #footer>
        <div class="failure-drawer-footer">
          <a-button @click="failureDrawerOpen = false">关 闭</a-button>
          <a-button type="primary" @click="handleCopyAsNewOrder">复制为新工单</a-button>
        </div>
      </template>
    </a-drawer>
  </div>
</template>

<script setup>
import { getSQLStandardByInstanceApi } from '@/api/admin'
import { getOrderDetailApi, getOrderLogsApi, getOrderPreInspectSnapshotApi, getOrderTasksApi } from '@/api/order'
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useOrderCreatePrefillStore } from '@/store/prefill'
import { message } from 'ant-design-vue'
import { CaretRightOutlined, CheckCircleFilled, CloseCircleFilled, CopyOutlined, EyeOutlined, WarningFilled } from '@ant-design/icons-vue'
import { buildStatementGroups, groupsAreUseful } from '../shared/statementGroups'
import CodeMirror from '@/components/edit/Codemirror.vue'
import TaskResult from '../tasks/TaskResult.vue'
import HeaderContent from './HeaderContent.vue'
import HeaderExtra from './HeaderExtra.vue'

const route = useRoute()
const router = useRouter()
const prefillStore = useOrderCreatePrefillStore()
const orderId = route.params.order_id
const orderDetail = ref({})
const orderLogs = ref([])
const orderTasks = ref([])

const historyColumns = [
  {
    title: '时间',
    dataIndex: 'created_at',
    key: 'created_at',
    width: 190,
  },
  {
    title: '操作人',
    dataIndex: 'username',
    key: 'username',
    width: 140,
  },
  {
    title: '操作项',
    key: 'action',
    width: 110,
  },
  {
    title: '内容',
    dataIndex: 'msg',
    key: 'msg',
  },
]

/* 操作项：日志 msg 无类型字段，按后端固定文案模板关键字归类 */
const LOG_ACTION_DEFS = [
  { match: ['创建了工单', '保存了草稿'], label: '提交申请', color: 'blue' },
  { match: ['提交工单审批'], label: '提交审批', color: 'cyan' },
  { match: ['提交执行'], label: '提交执行', color: 'default' },
  { match: ['开始执行'], label: '开始执行', color: 'default' },
  { match: ['审批通过'], label: '审批通过', color: 'green' },
  { match: ['审批驳回'], label: '审批不通过', color: 'red' },
  { match: ['执行任务失败', '更改工单状态为已失败', '执行失败'], label: '执行失败', color: 'red' },
  { match: ['更改工单状态为已完成', '执行成功'], label: '执行成功', color: 'green' },
  { match: ['撤回了工单'], label: '已撤回', color: 'orange' },
  { match: ['编辑了工单内容'], label: '提交申请', color: 'blue' },
  { match: ['撤销了工单'], label: '已撤回', color: 'orange' },
  { match: ['终止了工单'], label: '终止', color: 'orange' },
  { match: ['转交'], label: '转交', color: 'purple' },
  { match: ['修改工单名称'], label: '修改名称', color: 'default' },
]

const logActionMeta = (msg, progress) => {
  const text = String(msg || '')
  const hit = LOG_ACTION_DEFS.find((def) => def.match.some((m) => text.includes(m)))
  const meta = hit || { label: '其他' }
  // 已终止工单：撤销与终止殊途同归（progress 均为 TERMINATED），操作项统一明确为「终止」。
  // 注意「撤回了工单」（新撤回语义，progress=WITHDRAWN）不受此规则影响。
  if (String(progress || '').toUpperCase() === 'TERMINATED' && meta.label === '已撤回') {
    return { label: '终止' }
  }
  return meta
}

const currentStep = computed(() => {
  switch (orderDetail.value.progress) {
    case 'WAIT_PRECHECK':
      return 1
    case 'PENDING':
      return 2
    case 'APPROVED':
      return 3
    case 'EXECUTING':
      return 3
    case 'FAILED':
    case 'COMPLETED':
    case 'TERMINATED':
      return 4
    case 'REJECTED':
      return 1
    case 'WITHDRAWN':
      return 1
    default:
      return 0
  }
})

/* ===== 步骤条「查阅规范」链接 =====
   根据当前工单所选实例，实时查询其绑定的 SQL 开发规范；
   无实例/无绑定/无查看权限时隐藏链接，避免展示错误信息。 */
const instanceStandard = ref(null)
const loadingInstanceStandard = ref(false)

const standardLinkVisible = computed(() => {
  return !!instanceStandard.value?.id && !!orderDetail.value?.instance_id
})

const standardDetailHref = computed(() => {
  const id = instanceStandard.value?.id
  return id ? `/datasource/sql-standards/${id}` : ''
})

const loadInstanceStandard = async () => {
  const instanceID = orderDetail.value?.instance_id
  if (!instanceID) {
    instanceStandard.value = null
    return
  }
  loadingInstanceStandard.value = true
  try {
    const res = await getSQLStandardByInstanceApi(instanceID)
    if (res?.data?.id) {
      instanceStandard.value = res.data
    } else {
      instanceStandard.value = null
    }
  } catch (e) {
    // 未绑定规范或无查看权限时静默隐藏，不打扰用户
    instanceStandard.value = null
  } finally {
    loadingInstanceStandard.value = false
  }
}

watch(() => orderDetail.value?.instance_id, loadInstanceStandard, { immediate: true })

/* ===== 规范预审（读取工单提交时的快照）=====
   规范预审是实时检查：部分规则（如 ALTER ADD COLUMN 的重复列判定）会即时读取目标库
   表结构。工单执行成功后 DDL 已落库，重跑预审必然把「刚加上的列」判成重复列，从而在
   成功工单上出现「必须改进」的事后误报。
   因此详情页统一读取预审快照：
   - 待预审 / 被驳回：快照缺失、SQL 变化或上次生成失败时，由后端按需重建；
   - 审批中/审批通过/执行中：只读快照；存量工单无快照时由后端回填一次（DDL 未执行，结果有效）；
   - 执行成功/执行失败/已终止：只读快照；存量工单无快照时回填一次，
     并排除由本工单执行引起的事后误报（如执行成功工单的 ADD COLUMN 被判成重复列）；
   - 提交审批闸门处会把当次预审结果落库，通过闸门后全流程不再执行规范检查。 */
const preInspectState = reactive({
  loading: false,
  error: '',
  data: null,
})

const loadPreInspectSnapshot = async () => {
  const detail = orderDetail.value || {}
  if (!detail.order_id) {
    preInspectState.data = null
    preInspectState.error = ''
    return
  }
  preInspectState.loading = true
  preInspectState.error = ''
  const res = await getOrderPreInspectSnapshotApi(
    { order_id: detail.order_id },
    { silentBusinessError: true, silentHttpError: true },
  ).catch((err) => {
    // 语法错误已改由后端结构化返回（category=syntax_error），不再走这里；
    // 能走到这里的都是真实的系统错误，文案要说清是「执行失败」而不是「快照缺失」
    preInspectState.error = String(err?.message || err?.data?.message || '').trim() || '规范预审执行失败，请点击「重新检查」重试'
    return null
  })
  preInspectState.loading = false
  preInspectState.data = res ? (res.data || null) : null
}

// 快照缺失/生成失败时不做「全部通过」的假展示，改为中性空态
const preInspectSnapshotMissing = computed(() => {
  const data = preInspectState.data
  if (!data) return true
  if (!data.snapshot) return true
  return data.snapshot_status === 'failed'
})

const preInspectEmptyText = computed(() => {
  const data = preInspectState.data || {}
  if (data.message) return data.message
  if (!data.snapshot) return '该工单未生成规范预审快照'
  return '暂无预审数据'
})

/* ===== 预审双 Tab 交互 =====
   Tab1 规范预审：按「完整 SQL」聚合预审问题，每条 SQL 一行；
        点红叉/黄灯展开查看对应级别的审核信息，点击行选中该 SQL。
   Tab2 SQL 文本：完整展示当前选中的 SQL，支持复制与详情弹窗。 */
const preInspectTab = ref('review')
const selectedSeq = ref(1)
const preInspectExpandedKeys = ref([])

const preInspectColumns = [
  { title: '序号', dataIndex: 'seq', key: 'seq', width: 64 },
  // 注意：此列禁用 antd 的 ellipsis（td 整体 overflow:hidden 会把行尾复制图标一起裁掉），
  // 截断由 .preinspect-sql-cell 内部的 flex + 自定义 ellipsis 完成
  { title: 'SQL 语句', dataIndex: 'query', key: 'sql' },
  { title: '审核信息', key: 'audit', width: '20%' },
  { title: '审核结果', key: 'result', width: 96 },
  // 空占位列：把「审核信息 / 审核结果」整体左移表宽的 5%
  { title: '', key: 'spacer', width: '5%' },
]

/* ===== 致命错误（执行必然失败）的分类汇总 =====
   语法错误 / 对象已存在 / 对象不存在 / 语句不允许执行 都会让 SQL 无法落地执行。
   它们在预审判级里已是「必须改进」（会阻断提交审批），这里额外置顶汇总，
   避免用户看到一堆分散的行内提示却不知道「为什么不能提交」。 */
const FATAL_CATEGORY_LABELS = {
  syntax_error: 'SQL 语法错误',
  object_conflict: '目标对象已存在',
  object_missing: '目标对象不存在',
  forbidden_statement: '语句不允许执行',
}

const fatalIssueSummary = computed(() => {
  const items = preInspectState.data?.items || []
  const counts = {}
  items.forEach((item) => {
    const category = String(item?.category || '')
    if (!FATAL_CATEGORY_LABELS[category]) return
    counts[category] = (counts[category] || 0) + 1
  })
  const keys = Object.keys(counts)
  if (!keys.length) return null
  const parts = keys.map((key) => `${FATAL_CATEGORY_LABELS[key]} ${counts[key]} 处`)
  return {
    title: `检测到 ${parts.join('、')}，执行将失败`,
    description: '这些问题会导致 SQL 在执行阶段直接报错，请先修正后点击「重新检查」，再提交审批。',
  }
})

/* 按行审核结论：must（含语法错误，后端统一归级）→ 不通过；仅建议 → 有告警；全无 → 通过 */
const rowAuditResult = (record) => {
  if (record.must.length > 0) return { key: 'fail', label: '不通过' }
  if (record.suggest.length > 0) return { key: 'warn', label: '有告警' }
  return { key: 'passed', label: '通过' }
}

const truncateSql = (sql) => {
  const text = String(sql || '')
  return text.length > 120 ? `${text.slice(0, 120)}...` : text
}

/* 语句展示拆分：把前导注释行与 SQL 正文分开展示。
   注释是给人看的合规说明（如「-- 【合规】建表…」），和正文挤在同一格里会把
   语句开头挤没；拆出后注释弱化置顶，正文另起一行保持可读。
   只拆「整行注释在前」的形态；单行内联的「-- 注释 CREATE TABLE …」（注释和
   语句在同一行、无法可靠切分）保持原样整行展示。 */
const splitStatementParts = (sql) => {
  const lines = String(sql || '').split('\n')
  const firstCodeIdx = lines.findIndex((line) => line.trim() && !/^\s*--/.test(line))
  if (firstCodeIdx <= 0) return { comments: [], code: String(sql || '').trim() }
  const comments = lines
    .slice(0, firstCodeIdx)
    .map((line) => line.trim())
    .filter(Boolean)
  return { comments, code: lines.slice(firstCodeIdx).join('\n').trim() }
}
const statementComments = (sql) => splitStatementParts(sql).comments
const statementCode = (sql) => splitStatementParts(sql).code

// 跨语句提示：query 为空的摘要（如「多条 ALTER 建议合并」）评价的是「语句组」，
// 不属于任何一条 SQL，单独渲染为表尾卡片，避免出现「序号 +1 但 SQL 列为空」的错位行。
const isGroupHintItem = (item) => !String(item?.query || '').trim()

/* 把工单 SQL 切成「一条语句 = 一段文本 + 起止行号」。
   切分口径与后端预审一致：注释行、空行归入其后第一条语句（即「上一条语句结束行的下一行」起算），
   末段没有分号时独立成条。只有切分口径一致，才能用 item.line 把告警精确对回语句。
   例外：末尾的纯注释段（如 `;--asd`）归并进上一条语句——后端任务拆分本就丢弃纯注释段，
   独立成条只会产生一行「通过」的噪音行；归并后注释仍独占一行、跟在语句之后。 */
const splitSqlStatements = (content) => {
  const text = String(content || '').replace(/\r\n?/g, '\n')
  if (!text.trim()) return []
  const lines = text.split('\n')
  const result = []
  let start = 0
  let buffer = []
  lines.forEach((line, idx) => {
    buffer.push(line)
    const isCommentLine = /^\s*--/.test(line)
    // 语句终止：分号结尾（允许后跟注释），注释行/空行不构成终止
    const isTerminator = !isCommentLine && /;\s*(?:--.*)?$/.test(line)
    if (!isTerminator && idx !== lines.length - 1) return
    const raw = buffer.join('\n').trim()
    if (raw) {
      result.push({ query: raw, startLine: start + 1, endLine: idx + 1 })
    }
    start = idx + 1
    buffer = []
  })
  // 尾段纯注释归并进上一条语句（见函数头注释）；仅剩一条时不合并（整段就是注释）
  while (result.length > 1 && isPureCommentSegment(result[result.length - 1].query)) {
    const tail = result.pop()
    const prev = result[result.length - 1]
    prev.query = `${prev.query}\n${tail.query}`
    prev.endLine = tail.endLine
  }
  return result
}

// 判断一段文本是否为「纯注释/空白」段（不含任何可执行代码）：反复剥离
// 行注释（-- 至行尾）与块注释（/* ... */）后无剩余内容即为纯注释
const isPureCommentSegment = (text) => {
  let s = String(text || '')
  for (;;) {
    s = s.replace(/^\s+/, '')
    if (s.startsWith('--')) {
      const idx = s.indexOf('\n')
      s = idx >= 0 ? s.slice(idx + 1) : ''
      continue
    }
    if (s.startsWith('/*')) {
      const idx = s.indexOf('*/')
      s = idx >= 0 ? s.slice(idx + 2) : ''
      continue
    }
    break
  }
  return !s.trim()
}

// 文本归一化：剔除注释、折叠空白、小写——仅用于「行号配不上」时把告警对回语句
const normalizeSqlText = (sql) =>
  String(sql || '')
    .split('\n')
    .map((line) => line.replace(/--.*$/, ''))
    .join(' ')
    .replace(/\s+/g, ' ')
    .trim()
    .toLowerCase()

/* 预审行 = 提交的**全部语句**（不再只列命中规则的语句）：
   - 主口径：按 item.line 落在语句行区间内归属（与后端切分口径一一对应）；
   - 兜底口径：行号对不上时按归一化 SQL 文本配位（顺序配位，容忍重复文本）；
   - 两条口径都配不上的告警（如内容已被改写）补列到表尾，绝不静默丢弃。 */
const preInspectRows = computed(() => {
  const items = (preInspectState.data?.items || []).filter((item) => !isGroupHintItem(item))
  const statements = splitSqlStatements(orderDetail.value?.content || '')

  // 内容不可用（被清空等）：退回只展示告警项，保证信息不丢
  if (!statements.length) {
    return items.map((item, index) => ({
      seq: index + 1,
      query: String(item.query || '').trim(),
      must: item.level === 'suggest' ? [] : [item],
      suggest: item.level === 'suggest' ? [item] : [],
    }))
  }

  const rows = statements.map((statement, index) => ({
    seq: index + 1,
    query: statement.query,
    must: [],
    suggest: [],
    startLine: statement.startLine,
    endLine: statement.endLine,
    hasItem: false,
  }))

  const fill = (row, item) => {
    if (item.level === 'suggest') {
      row.suggest.push(item)
    } else {
      row.must.push(item)
    }
    row.hasItem = true
  }

  const placed = new Set()
  items.forEach((item, idx) => {
    const line = Number(item?.line)
    if (!Number.isFinite(line)) return
    const row = rows.find((r) => line >= r.startLine && line <= r.endLine)
    if (!row) return
    fill(row, item)
    placed.add(idx)
  })
  const leftovers = []
  items.forEach((item, idx) => {
    if (placed.has(idx)) return
    const target = normalizeSqlText(item.query)
    const row = rows.find((r) => !r.hasItem && normalizeSqlText(r.query) === target)
    if (row) {
      fill(row, item)
      placed.add(idx)
      return
    }
    leftovers.push(item)
  })
  leftovers.forEach((item) => {
    rows.push({
      seq: rows.length + 1,
      query: String(item.query || '').trim(),
      must: item.level === 'suggest' ? [] : [item],
      suggest: item.level === 'suggest' ? [item] : [],
      hasItem: true,
    })
  })

  // startLine 不再单独展示（「行号」列已删），仍保留用于把告警对回语句文本的具体行
  return rows.map(({ endLine, hasItem, ...row }) => row)
})

// 展开显示该行全部审核信息：必须修改 + 建议优化
const expandedItems = (record) => [...record.must, ...record.suggest]

/* 审核信息按行内代码渲染：后端消息沿用 markdown 风格的反引号标注对象名
   （如 表 `ORDER1`、列 `is_valid`），纯文本直出会把反引号原样露出来。
   先整体 HTML 转义再替换，保证消息内容不会被当成 HTML 执行；
   未成对的反引号不替换（正则只匹配成对片段）。 */
const renderInlineCode = (text) => {
  const escaped = String(text ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
  return escaped.replace(/`([^`]*)`/g, '<code class="preinspect-inline-code">$1</code>')
}

// 展开行的审核文案：【消息 + 规则名】，反引号部分渲染为行内代码
const formatAuditText = (item) => {
  const msg = renderInlineCode(item.message || '')
  const rule = item.rule_name ? '规则：' + renderInlineCode(item.rule_name) : ''
  return `【${msg}${rule}】`
}

/* ===== 语句分组展示 =====
   Oracle 没有列注释内联语法（COMMENT ON 只能是独立语句），一条规范建表天然膨胀成
   1+N 条语句；10 张表就是 60+ 行扁平列表。行业通行做法（Liquibase changeSet /
   DMS 变更工单）是展示按目标对象聚合、执行仍逐条 —— 这里把同一条建表及其伴生的
   COMMENT ON / CREATE INDEX / ALTER 归入一张对象卡片，默认折叠。
   只影响展示层：后端拆分、seq、执行与回滚粒度全部不变。 */
const statementGroups = computed(() => {
  const groups = buildStatementGroups(preInspectRows.value)
  return groupsAreUseful(preInspectRows.value, groups) ? groups : null
})

// 折叠的组（默认全部折叠：组头即摘要，一眼可扫；点击组头展开逐条明细）
const collapsedGroupKeys = ref(new Set())
const toggleGroupCollapse = (key) => {
  const next = new Set(collapsedGroupKeys.value)
  if (next.has(key)) {
    next.delete(key)
  } else {
    next.add(key)
  }
  collapsedGroupKeys.value = next
}

/* 分组默认收起：组集合出现新组键时自动加入折叠集合（切工单/重新检查后同样生效）。
   展开/收起只增删本集合，不会触发 statementGroups 重算，不会被这里重新折叠。 */
watch(statementGroups, (groups) => {
  if (!groups || !groups.size) return
  let changed = false
  const next = new Set(collapsedGroupKeys.value)
  groups.forEach((_, key) => {
    if (!next.has(key)) {
      next.add(key)
      changed = true
    }
  })
  if (changed) collapsedGroupKeys.value = next
})

// 组序号（组头「组 N」标签）
const groupOrderIndex = computed(() => {
  const index = new Map()
  if (statementGroups.value) {
    Array.from(statementGroups.value.keys()).forEach((key, idx) => index.set(key, idx + 1))
  }
  return index
})

/* 组内展示序号：分组后每组内从 1 连续编号（1、2、3…不跳号）。
   全局 seq 仍是内部主键（row-key / 定位 / 复制 / 告警对位都用它），
   序号列与跨语句提示 chip 展示时统一换算成组内序号，展开/收起不影响编号。 */
const seqDisplayNumbers = computed(() => {
  const map = new Map()
  const groups = statementGroups.value
  if (!groups) return map
  groups.forEach((group) => {
    group.seqs.forEach((seq, idx) => map.set(seq, idx + 1))
  })
  return map
})
const displaySeqOf = (seq) => seqDisplayNumbers.value.get(seq) ?? seq

/* 展示行 = 组头行（含组内 must/suggest 聚合）+ 未折叠组的成员行 + 未分组行。
   组头行 seq 用字符串前缀避开与真实语句 seq 冲突（row-key / data-row-seq 仍唯一）。
   每个「组」作为一个整体块渲染（块位置 = 组内最小 seq）：组头与组内成员始终相邻，
   不再被语句顺序上插在中间的其他组隔开；未分组行按自身 seq 排位。 */
const preInspectDisplayRows = computed(() => {
  const groups = statementGroups.value
  if (!groups) return preInspectRows.value
  const rowBySeq = new Map()
  preInspectRows.value.forEach((row) => rowBySeq.set(row.seq, row))

  const units = []
  const consumed = new Set()
  groups.forEach((group, key) => {
    const collapsed = collapsedGroupKeys.value.has(key)
    const must = []
    const suggest = []
    group.seqs.forEach((seq) => {
      const member = rowBySeq.get(seq)
      if (member) {
        must.push(...member.must)
        suggest.push(...member.suggest)
      }
    })
    const header = {
      seq: `group-${key}`,
      isGroupHeader: true,
      groupKey: key,
      group,
      collapsed,
      query: '',
      must,
      suggest,
    }
    const memberRows = collapsed
      ? []
      : group.seqs.map((seq) => ({ ...rowBySeq.get(seq), isGroupMember: true, groupKey: key }))
    group.seqs.forEach((seq) => consumed.add(seq))
    units.push({ pos: Math.min(...group.seqs), rows: [header, ...memberRows] })
  })
  preInspectRows.value.forEach((row) => {
    if (!consumed.has(row.seq)) units.push({ pos: row.seq, rows: [row] })
  })
  units.sort((a, b) => a.pos - b.pos)
  const out = []
  units.forEach((unit) => out.push(...unit.rows))
  return out
})

/* ===== 跨语句提示（query 为空的组级规则结论）=====
   展示：表尾卡片，与语句行分层，默认收起（含必须改进项时默认展开）。
   关联：解析文案中的表名匹配语句行 → 「涉及语句 #N」chip，点击定位并高亮；语句行加「组 N」角标。
   操作：展开/收起 · 点击 #N 定位语句。
   注：不提供「忽略」——预审是提交闸门，隐藏必须改进项会造成「页面看似通过、提交被拦」的矛盾。 */

// 已知的跨语句规则：优先用后端下发的 rule_key，缺失时按文案兜底匹配
// （规则名与描述与规范种子 standard_seed.go:495 保持一致）
const GROUP_HINT_RULES = [
  {
    key: 'merge_multiple_alter_stmt_same_table',
    name: '同一张表的多条 ALTER 语句建议合为一条',
    test: (message) => /多条\s*ALTER\s*操作|请合并为一条\s*ALTER/i.test(message),
  },
]

const matchGroupHintRule = (item) => {
  const key = String(item?.rule_key || '').replace(/^(mysql|postgres)\./, '')
  if (key) {
    const byKey = GROUP_HINT_RULES.find((rule) => rule.key === key)
    if (byKey) return byKey
  }
  const text = String(item?.message || '')
  return GROUP_HINT_RULES.find((rule) => rule.test(text)) || null
}

// 从摘要文案中解析涉及的表名（如「表`t_order`的多条ALTER操作」）
const extractHintTables = (message) => {
  const text = String(message || '')
  const names = []
  const re = /表\s*`?([A-Za-z0-9_$]+(?:\.[A-Za-z0-9_$]+)?)`?/g
  let matched = re.exec(text)
  while (matched) {
    if (matched[1]) names.push(matched[1].toLowerCase())
    matched = re.exec(text)
  }
  return Array.from(new Set(names))
}

const escapeRegExp = (text) => String(text).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

// 语句是否操作了某张表（按标识符边界匹配，避免 t_order 命中 t_order_item）
const sqlTouchesTable = (sql, table) => {
  const text = String(sql || '').toLowerCase()
  if (!text || !table) return false
  const candidates = table.includes('.') ? [table, table.split('.').pop()] : [table]
  return candidates.some((name) =>
    new RegExp(`(^|[^a-z0-9_$])${escapeRegExp(name)}([^a-z0-9_$]|$)`).test(text),
  )
}

/* 聚合跨语句提示：按「级别 + 文案」去重（引擎可能对同一批次重复报出），
   并解析每条提示关联的语句序号；解析不到表名时视为作用于全部语句。 */
const groupHints = computed(() => {
  const items = (preInspectState.data?.items || []).filter(isGroupHintItem)
  const statements = preInspectRows.value.filter((row) => row.query)
  const list = []
  const index = new Map()
  const bySeq = new Map()
  items.forEach((item) => {
    const message = String(item.message || '')
    const key = `${item.level}|${message}`
    let hint = index.get(key)
    if (!hint) {
      const tables = extractHintTables(message)
      const matched = tables.length
        ? statements.filter((row) => tables.some((table) => sqlTouchesTable(row.query, table)))
        : []
      // 命中规则优先取后端判定结果（与判级同源，来自 items[].rule_key/rule_name），
      // 后端未关联到具体规则时再退回前端按文案匹配
      const rule = item.rule_name
        ? { name: String(item.rule_name), key: String(item.rule_key || '') }
        : matchGroupHintRule(item)
      hint = {
        key,
        level: item.level === 'must' ? 'must' : 'suggest',
        message,
        occurrences: 0,
        tables,
        ruleName: rule?.name || '',
        ruleKey: rule?.key || '',
        relatedSeqs: (matched.length ? matched : statements).map((row) => row.seq),
      }
      index.set(key, hint)
      list.push(hint)
    }
    hint.occurrences += 1
  })
  list.forEach((hint) => {
    hint.relatedSeqs.forEach((seq) => bySeq.set(seq, (bySeq.get(seq) || 0) + 1))
  })
  return {
    list,
    total: items.length,
    mustCount: items.filter((item) => item.level === 'must').length,
    bySeq,
  }
})

// 该语句参与了 N 条跨语句提示（行内角标）
const rowGroupHintCount = (record) => groupHints.value.bySeq.get(record.seq) || 0

const rowGroupHintTitle = (record) =>
  `该语句参与 ${rowGroupHintCount(record)} 条跨语句提示：${groupHints.value.list
    .filter((hint) => hint.relatedSeqs.includes(record.seq))
    .map((hint) => hint.message)
    .join('；')}`

/* 卡片展开/收起 */
const groupHintOpen = ref(false)
const toggleGroupHint = () => {
  groupHintOpen.value = !groupHintOpen.value
}

/* 定位语句：滚动到对应行并高亮闪烁，建立「提示 ↔ 语句」双向关联 */
// 语句归属组索引：定位语句时若所在对象卡片处于折叠态，先展开再滚动
const seqToGroupKey = computed(() => {
  const map = new Map()
  if (statementGroups.value) {
    statementGroups.value.forEach((group, key) => group.seqs.forEach((seq) => map.set(seq, key)))
  }
  return map
})

const preInspectTableRef = ref(null)
const flashSeq = ref(0)
let flashTimer = null
const locateStatement = async (seq) => {
  const groupKey = seqToGroupKey.value.get(seq)
  if (groupKey && collapsedGroupKeys.value.has(groupKey)) {
    toggleGroupCollapse(groupKey)
  }
  selectedSeq.value = seq
  flashSeq.value = seq
  await nextTick()
  const selector = `tr[data-row-seq="${seq}"]`
  const scoped = preInspectTableRef.value?.$el?.querySelector?.(selector)
  const row = scoped || document.querySelector(selector)
  row?.scrollIntoView?.({ behavior: 'smooth', block: 'center' })
  if (flashTimer) clearTimeout(flashTimer)
  flashTimer = setTimeout(() => {
    flashSeq.value = 0
  }, 1600)
}

// 数据刷新后：含必须改进的跨语句提示时默认展开（阻断类信息必须直接可见），否则收起
watch(
  groupHints,
  (value) => {
    groupHintOpen.value = value.mustCount > 0
  },
  { immediate: true },
)

// 图标悬停提示：有数量显示「XX N 项」，为 0 时友好提示
const auditTooltip = (count, label) => {
  return count > 0 ? `${label} ${count} 项` : `暂无${label}的问题`
}

const isRowExpanded = (record) => preInspectExpandedKeys.value.includes(record.seq)

const toggleRowExpand = (record) => {
  // 通过的语句没有可展开内容，点击徽标不展开（避免出现空白展开区）
  if (!record.must.length && !record.suggest.length) return
  const keys = preInspectExpandedKeys.value
  if (keys.includes(record.seq)) {
    preInspectExpandedKeys.value = keys.filter((key) => key !== record.seq)
    return
  }
  preInspectExpandedKeys.value = [...keys, record.seq]
}

const preInspectRowProps = (record) => ({
  // 供「跨语句提示」的涉及语句 chip 定位使用（locateStatement → tr[data-row-seq]）
  'data-row-seq': record.seq,
  onClick: () => {
    if (record.isGroupHeader) {
      // 组头行整行可点：切换该对象卡片的折叠状态
      toggleGroupCollapse(record.groupKey)
      return
    }
    selectedSeq.value = record.seq
  },
  class: [
    'preinspect-row',
    record.isGroupHeader ? 'is-group-header' : '',
    record.isGroupMember ? 'is-group-member' : '',
    !record.isGroupHeader && record.seq === selectedSeq.value ? 'is-selected' : '',
    record.seq === flashSeq.value ? 'is-flash' : '',
  ]
    .filter(Boolean)
    .join(' '),
})

// Tab2/弹窗展示的 SQL：固定为工单提交时的完整内容，与选中行无关，保证信息无遗漏
const selectedSql = computed(() => String(orderDetail.value?.content || '').trim())

const selectedSqlMeta = computed(() => {
  const text = selectedSql.value || ''
  if (!text) return ''
  const lines = text.split('\n').length
  const bytes = new TextEncoder().encode(text).length
  return `${lines} 行 ${bytes} B`
})

const copyTextToClipboard = async (text) => {
  const value = String(text || '')
  if (!value) {
    message.warning('没有可复制的内容')
    return
  }
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value)
    } else {
      const textarea = document.createElement('textarea')
      textarea.value = value
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      document.body.removeChild(textarea)
    }
    message.success('已复制完整 SQL')
  } catch {
    message.error('复制失败，请手动复制')
  }
}

/* Tab2 编辑器：切 Tab 或换选中行时同步内容 */
const tabSqlRef = ref(null)
const syncTabSqlEditor = async () => {
  if (preInspectTab.value !== 'sql') return
  await nextTick()
  const editor = tabSqlRef.value
  if (!editor) return
  editor.setContent(selectedSql.value)
  editor.setReadonly(true)
  editor.setDialect(orderDetail.value?.db_type || 'MySQL')
}

watch([preInspectTab, selectedSql], () => {
  syncTabSqlEditor()
})

// 数据刷新后重置选中行、展开状态与语句分组折叠（新内容默认全折叠，组头即摘要）
watch(preInspectRows, () => {
  selectedSeq.value = 1
  preInspectExpandedKeys.value = []
  const next = new Set(collapsedGroupKeys.value)
  statementGroups.value?.forEach((_, key) => next.add(key))
  collapsedGroupKeys.value = next
})

/* SQL 详情弹窗 */
const sqlDetailModalOpen = ref(false)
const sqlWrapEnabled = ref(false)
const modalSqlRef = ref(null)

const openSqlDetailModal = async () => {
  sqlDetailModalOpen.value = true
  await nextTick()
  const editor = modalSqlRef.value
  if (!editor) return
  editor.setContent(selectedSql.value)
  editor.setReadonly(true)
  editor.setDialect(orderDetail.value?.db_type || 'MySQL')
}

const parseTaskResult = (task) => {
  if (!task?.result) return {}
  if (typeof task.result === 'object') return task.result
  try {
    return JSON.parse(task.result)
  } catch {
    return {}
  }
}

/* ===== 执行结果 Tab ===== */
const hasExecutionTasks = computed(() => orderTasks.value.length > 0)

// 执行场景（已产生执行任务）时默认停在「执行结果」Tab
const execTabInitialized = ref(false)
watch(hasExecutionTasks, (val) => {
  if (val && !execTabInitialized.value) {
    execTabInitialized.value = true
    preInspectTab.value = 'exec'
  }
})

const execResultColumns = [
  { title: '序号', key: 'seq', width: 64 },
  // 原「详情」列已删除：结果摘要并入 SQL语句列副行、失败入口移到操作列，
  // 释放的宽度由不定宽的 SQL语句列自动吸收
  { title: 'SQL语句', dataIndex: 'sql', key: 'sql', ellipsis: true },
  { title: '开始执行时间', key: 'start_time', width: 170 },
  { title: '结束执行时间', key: 'end_time', width: 170 },
  { title: '备份状态', key: 'backup', width: 120 },
  { title: '操作', key: 'action', width: 150 },
]

const execResultPagination = {
  pageSize: 10,
  showSizeChanger: false,
  showTotal: (total) => `共 ${total} 条`,
}

// 起止时间：任务表无独立时间列，从 execute_log 的时间戳行提取（首行=开始、末行=结束）
const LOG_TIME_RE = /\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\]/g

const taskStartEnd = (task) => {
  const log = String(parseTaskResult(task).execute_log || '')
  if (!log) return { start: '', end: '' }
  const times = [...log.matchAll(LOG_TIME_RE)].map((m) => m[1])
  return { start: times[0] || '', end: times.length > 1 ? times[times.length - 1] : '' }
}

// 详情列：优先错误信息，其次 execute_log 的 SQL 执行结果行；
// 成功任务无额外输出时显示「执行成功」，失败任务无信息时显示 '-'
const taskDetail = (task) => {
  const result = parseTaskResult(task)
  const err = String(result.error || '').trim()
  if (err) return err
  const lines = String(result.execute_log || '')
    .split('\n')
    .filter((line) => line.includes('SQL执行'))
  const line = (lines[lines.length - 1] || '').replace(/^\[[^\]]*\]\s*/, '')
  if (line) return line
  return task.progress === 'COMPLETED' ? '执行成功' : ''
}

const taskDetailFull = (task) => {
  const result = parseTaskResult(task)
  const err = String(result.error || '').trim()
  return err || String(result.execute_log || '')
}

/* ===== 备份状态 / 回滚 SQL 入口 =====
   数据全部来自 task.result（执行时由 Binlog 生成的回滚 SQL），零新增接口。
   任务页早就有这套展示，这里把同一份数据在工单详情页透出。 */
const taskRollbackSql = (task) => String(parseTaskResult(task).rollback_sql || '').trim()

// 备份状态文案：与任务页 getBackupStatus 同口径
const backupStatusText = (task) => {
  const result = parseTaskResult(task)
  if (result.backup_status) return String(result.backup_status)
  if (result.backup_cost_time) return '备份成功'
  return ''
}

const backupStatusClass = (task) => {
  const text = backupStatusText(task)
  if (!text) return 'is-none'
  if (text.startsWith('备份成功')) return 'is-ok'
  if (text.startsWith('备份失败')) return 'is-fail'
  return 'is-skip'
}

// 任务详情弹窗（复用任务页 TaskResult 组件，两处口径天然一致）
const taskResultOpen = ref(false)
const taskResultData = ref({})

const showTaskResult = (task) => {
  taskResultData.value = {
    ...task,
    result: parseTaskResult(task),
  }
  taskResultOpen.value = true
}

/* ===== 执行失败：结构化诊断 + 恢复出口 =====
   原始错误是数据库返回的英文原文（如 Error 1146: Table 'x' doesn't exist），
   用户看不懂也不知道怎么改。这里按错误码归类成中文原因 + 可执行建议，
   并在抽屉里给出「复制为新工单」——执行失败的工单不支持原地改 SQL，
   只给「重新执行」等于让用户原地打转（SQL 没变必然再失败）。 */
const EXEC_ERROR_RULES = [
  { re: /unknown database|1049/i, category: 'object_missing', reason: '目标库不存在', advice: '① 核对库名拼写；② 确认是否选错了实例或数据源' },
  { re: /1050|42S01|already exists/i, category: 'object_conflict', reason: '目标对象已存在', advice: '① 更换对象名；② 若需覆盖请先单独提交 DROP 工单（数据将丢失，需另行评估）' },
  { re: /1146|1051|42P01|doesn't exist|does not exist/i, category: 'object_missing', reason: '目标对象不存在', advice: '① 核对对象名拼写与大小写；② 确认是否选错了目标库；③ 若该对象应由本工单创建，请调整语句顺序' },
  { re: /1091|42704|can't drop/i, category: 'object_missing', reason: '要删除的列或索引不存在', advice: '① 核对列名/索引名拼写；② 该对象可能已被其他工单删除，请先同步实例元数据' },
  { re: /1060|42701|duplicate column/i, category: 'object_conflict', reason: '列已存在', advice: '① 更换列名；② 若需覆盖请先单独提交删除列的工单' },
  { re: /1061|42P07|duplicate key name/i, category: 'object_conflict', reason: '索引名已存在', advice: '① 更换索引名；② 确认是否重复创建了同名索引' },
  { re: /1062|23505|duplicate entry/i, category: 'rule_violation', reason: '存在重复数据，违反唯一约束', advice: '① 检查待插入/更新的数据是否已存在；② 确认业务上是否应改用 UPDATE' },
  { re: /1213|40001|deadlock/i, category: 'rule_violation', reason: '事务死锁，已被回滚', advice: '① 重试即可；② 若频繁出现请拆分大批次语句、避开业务高峰执行' },
  { re: /1205|lock wait timeout/i, category: 'rule_violation', reason: '等待锁超时', advice: '① 避开业务高峰重试；② 拆分大批次语句；③ 检查是否有长事务占用目标表' },
  { re: /1064|42000|syntax/i, category: 'syntax_error', reason: 'SQL 语法错误', advice: '① 检查报错位置附近的拼写与关键字；② 检查括号、引号是否成对闭合' },
]

const parseExecutionError = (rawError) => {
  const text = String(rawError || '').trim()
  if (!text) return { category: 'rule_violation', reason: '执行失败', advice: '请查看完整错误信息，修正 SQL 后重新提交工单' }
  const hit = EXEC_ERROR_RULES.find((rule) => rule.re.test(text))
  if (hit) return { category: hit.category, reason: hit.reason, advice: hit.advice }
  return { category: 'rule_violation', reason: '执行失败', advice: '请查看完整错误信息，修正 SQL 后复制为新工单重新提交' }
}

// 任务失败判定：有明确错误信息，或任务状态为 FAILED
const taskFailed = (task) => {
  if (!task) return false
  if (String(parseTaskResult(task).error || '').trim()) return true
  return String(task.progress || '').toUpperCase() === 'FAILED'
}

const failureDrawerOpen = ref(false)
const currentFailure = ref(null)

const currentFailureRawError = computed(() => {
  const task = currentFailure.value
  if (!task) return ''
  return String(parseTaskResult(task).error || '').trim() || String(parseTaskResult(task).execute_log || '').trim()
})

// 把执行失败包装成预审问题项结构，复用同一张结构化错误卡片
const currentFailureItem = computed(() => {
  const task = currentFailure.value || {}
  const raw = currentFailureRawError.value
  const parsed = parseExecutionError(raw)
  return {
    level: 'must',
    category: parsed.category,
    message: parsed.reason,
    reason: raw ? `${parsed.reason}（原始错误：${raw}）` : parsed.reason,
    advice: parsed.advice,
    position: task.task_id ? `任务 ${task.task_id}` : '',
  }
})

const openFailureDrawer = (task) => {
  currentFailure.value = task
  failureDrawerOpen.value = true
}

// 复制为新工单：执行失败的工单不允许原地改 SQL，这是唯一真正可行的恢复路径
const handleCopyAsNewOrder = () => {
  const raw = orderDetail.value || {}
  prefillStore.setCreatePrefill({
    instance_id: raw.instance_id,
    db_type: raw.db_type,
    database: raw.database,
    schema: raw.schema,
    executor: raw.executor && raw.executor === raw.applicant ? 'SELF' : (raw.executor || 'SELF'),
    estimated_affected_rows: typeof raw.estimated_affected_rows === 'number' ? raw.estimated_affected_rows : null,
    content: raw.content || '',
    rollback_sql: raw.rollback_sql || '',
    cc: raw.cc || [],
  })
  failureDrawerOpen.value = false
  router.push({ name: 'orders.create' })
}

const getOrderDetail = async () => {
  const res = await getOrderDetailApi({
    order_id: orderId,
  }).catch(() => {})
  if (res) {
    orderDetail.value = res.data
    loadPreInspectSnapshot()
  }
}

const getOrderLogs = async () => {
  const res = await getOrderLogsApi({
    order_id: orderId,
  }).catch(() => {})
  if (res) {
    orderLogs.value = res.data
  }
}

const getOrderTasks = async () => {
  const res = await getOrderTasksApi({
    order_id: orderId,
    is_page: false,
    page: 1,
    page_size: 200,
  }).catch(() => {})
  if (res) {
    orderTasks.value = Array.isArray(res.data) ? res.data : []
  }
}

const refresh = () => {
  getOrderDetail()
  getOrderLogs()
  getOrderTasks()
}

onMounted(async () => {
  getOrderDetail()
  getOrderLogs()
  getOrderTasks()
})
</script>

<style scoped>
.order-detail-page {
  gap: 0;
}

.detail-top-card {
  padding: 0;
}

.detail-top-card :deep(.ant-card-body) {
  padding: 0;
}

.steps-shell {
  padding: 12px 20px;
  border: 1px solid rgb(235, 237, 240);
  border-radius: 8px 8px 0 0;
  background: #fff;
}

/* 步骤条「规范预审」下方的「查阅规范」链接 */
.order-step-standard-hint {
  margin-top: 2px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--gi-color-text-secondary, rgba(79, 100, 112, 0.8));
  white-space: nowrap;
}

.order-step-standard-link {
  color: var(--gi-color-primary, #0f766e);
  text-decoration: none;
}

.order-step-standard-link:hover {
  color: var(--gi-color-primary-hover, #0d6b63);
  text-decoration: underline;
}

.detail-header {
  position: relative;
  display: block;
  padding: 16px 20px 8px;
  border-left: 1px solid rgb(235, 237, 240);
  border-right: 1px solid rgb(235, 237, 240);
  background: #fff;
}

.header-action-slot {
  position: absolute;
  top: 14px;
  right: 20px;
  z-index: 5;
  display: flex;
  justify-content: flex-end;
  max-width: 56%;
  min-width: 0;
}

.detail-title-block {
  display: block;
  width: 100%;
  min-width: 0;
}

.detail-title-row {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 18px;
  font-weight: 600;
  white-space: nowrap;
  min-width: 0;
  max-width: 100%;
  padding-right: 56%;
}

/* ===== 名称+ID 紧密小组：内部永远紧邻，间距固定 ===== */
.title-id-group {
  display: inline-flex;
  align-items: center;
  flex: 0 1 auto;       /* 不主动撑满剩余空间，内容多宽占多宽；超长时可被整体压缩（但内部会先在 title 处省略）*/
  min-width: 0;
  max-width: 100%;
  white-space: nowrap;
}

/* AppTooltip 是名称的外层容器，必须同样约束 min-width:0 才能让内部 span 省略号生效 */
.title-id-group > :deep(.gi-app-tooltip),
.title-id-group > app-tooltip {
  display: inline-block;
  min-width: 0;
  max-width: 100%;
  flex: 0 1 auto;
  white-space: nowrap;
  overflow: hidden;
}

.detail-title-text {
  display: inline-block;
  vertical-align: middle;
  max-width: 100%;
  line-height: 1.4;
  pointer-events: auto;
  min-width: 0;
}
.detail-title-text--ellipsis {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  cursor: default;
  /* ★ 关键：不再使用 flex:1 撑满空间，改为固定最大宽度
     - 名称短：自然收窄，ID 紧跟其后（间距 8px 固定）
     - 名称长：到 480px 边界用 ... 截断，ID 仍然紧贴 ... 之后 */
  display: inline-block;
  flex: 0 1 auto;
  min-width: 0;
  max-width: 480px;
}

/* ===== ID + 复制按钮 紧密小组：内部 ID 文字和复制图标在同一水平基线 ===== */
.order-id-block {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  min-width: 0;
  white-space: nowrap;
  margin-left: 8px;   /* 名称末尾 → ID 块的固定 8px 间距，不随名称长度变化 */
}

/* ★ 工单 ID：字体从 13px 放大到 15px，提升视觉层级但不超过标题(18px) */
.detail-order-id {
  flex: 0 0 auto;
  color: #86909c;           /* 改用中性灰色系 #86909c，比 #8c8c8c 更统一协调（与页面其他次级文字同色）*/
  font-size: 15px;           /* ★ 字体放大：13px → 15px，大一号 */
  font-weight: 500;          /* 字重略提升，配合放大后的字号，不显得细 */
  line-height: 20px;
  white-space: nowrap;
}

/* ===== 复制工单ID 按钮：大小、间距、配色与整体和谐统一 ===== */
.copy-order-id-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  width: 24px;                /* 正方形小按钮，24px 紧凑不突兀 */
  height: 24px;
  margin-left: 6px;           /* ID 文字 → 复制按钮：6px 间距，不挤也不松 */
  padding: 0;
  border: 0;
  border-radius: 6px;         /* 轻微圆角，和页面整体卡片圆角一致 */
  background: transparent;    /* 默认透明，不抢注意力 */
  color: #a9aeb8;             /* 图标默认色：浅灰，作为次级操作 */
  font-size: 14px;
  line-height: 1;
  cursor: pointer;
  user-select: none;
  -webkit-appearance: none;
  appearance: none;
  transition: background-color 0.15s ease, color 0.15s ease, transform 0.12s ease;
}
.copy-order-id-btn:hover {
  background: #f2f3f5;        /* hover 时给一个浅灰背景，反馈清晰 */
  color: #1677ff;             /* 图标变主题蓝，提示可点击 */
}
.copy-order-id-btn:active {
  transform: scale(0.92);     /* 按下时轻微缩小，点击感更好 */
  background: #e5e6eb;
}
.copy-order-id-btn:focus-visible {
  outline: 2px solid #1677ff; /* 键盘可访问性聚焦环 */
  outline-offset: 1px;
}
.copy-order-id-btn svg {
  display: block;
  width: 14px;                /* 图标 14px，与 15px 的ID文字视觉重量匹配 */
  height: 14px;
}

.detail-status-tag {
  flex: 0 0 auto;
  white-space: nowrap;
}

:global(.gi-app-tooltip.order-detail-title-tip .ant-tooltip-inner) {
  padding: 8px 12px !important;
  background: rgba(29, 35, 41, 0.9) !important;
  color: #f7f8fa !important;
  border-radius: 8px !important;
  box-shadow: 0 8px 24px rgba(29,35,41,.18) !important;
  max-width: 640px !important;
}
:global(.gi-app-tooltip.order-detail-title-tip .gi-app-tooltip__content) {
  font-size: 12px !important;
  line-height: 1.55 !important;
  color: #f7f8fa !important;
  white-space: normal !important;
  word-break: break-word !important;
}
:global(.gi-app-tooltip.order-detail-title-tip .ant-tooltip-arrow-content) {
  background: rgba(29, 35, 41, 0.9) !important;
}

.detail-section-card {
  margin-top: var(--gi-spacing-ssm);
  border-radius: var(--gi-radius-card);
}

.execution-summary {
  white-space: pre-wrap;
  word-break: break-word;
}

/* ===== 执行结果 Tab ===== */
.exec-sql-text {
  color: var(--gi-color-text, rgba(0, 0, 0, 0.88));
}

/* SQL 单元格：主行 SQL + 副行执行结果摘要（原详情列内容并入） */
.exec-sql-wrap {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.exec-sql-text {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.exec-sql-summary {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: #86909c;
  font-size: 12px;
  line-height: 16px;
}

.exec-sql-summary.is-failed {
  color: #ff4d4f;
}

/* 操作列的失败原因入口（原详情列失败标签的替代） */
.exec-failure-link {
  font-size: 14px;
  color: #ff4d4f;
}

.exec-detail-link {
  font-size: 14px;
  color: var(--gi-color-primary, #0f766e);
}

/* 回滚 SQL 入口：比「详情」更醒目（用户带着「要回滚」的目的而来），
   仅在有回滚 SQL（备份成功）的任务上渲染 */
.exec-rollback-link {
  font-size: 14px;
  font-weight: 500;
  color: var(--gi-color-primary, #0f766e);
}

/* 备份状态列：纯文字默认色（成功/跳过/未备份不着色），
   仅备份失败保留红色——失败是语义告警，不能淹没在正文色里 */
.exec-backup-status {
  font-size: 14px;
}

.exec-backup-status.is-ok,
.exec-backup-status.is-skip,
.exec-backup-status.is-none {
  color: inherit;
}

/* ===== 执行失败详情抽屉 ===== */
.failure-drawer-card {
  margin-bottom: 16px;
}

.failure-drawer-section {
  margin-bottom: 16px;
}

.failure-drawer-section-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
  font-size: 13px;
  font-weight: 500;
  color: #1f2329;
}

.failure-drawer-copy {
  font-size: 12px;
  font-weight: 400;
}

.failure-drawer-sql,
.failure-drawer-raw {
  margin: 0;
  padding: 10px 12px;
  border-radius: 6px;
  background: #f7f8fa;
  font-size: 12px;
  line-height: 1.6;
  color: #1f2329;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 220px;
  overflow: auto;
}

.failure-drawer-raw {
  color: #cf1322;
}

.failure-drawer-alert {
  margin-top: 4px;
}

.failure-drawer-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.task-meta-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 12px;
  color: #595959;
  font-size: 13px;
}

.task-meta-item {
  color: #8c8c8c;
}

.task-sql-editor {
  border: 1px solid #f0f0f0;
  border-radius: 8px;
  overflow: hidden;
}

/* ===== 规范预审卡片 ===== */
.preinspect-error {
  margin-bottom: 4px;
}

/* ===== Tab1：预审结果表格 ===== */
.preinspect-tabs :deep(.ant-tabs-nav) {
  margin-bottom: 8px;
}

.preinspect-row {
  cursor: pointer;
}

.preinspect-row.is-selected > :deep(.ant-table-cell) {
  background: rgba(22, 119, 255, 0.06);
}

/* 语句分组：组头行 = 对象卡片摘要；成员行缩进 */
.preinspect-row.is-group-header > :deep(.ant-table-cell) {
  background: #f7f8fa;
}

.preinspect-group-seq {
  color: #a9aeb8;
  font-size: 12px;
}

.preinspect-group-head {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  max-width: 100%;
}

.preinspect-group-caret {
  color: #a9aeb8;
  font-size: 12px;
  transition: transform 0.15s ease;
}

.preinspect-group-caret.is-open {
  transform: rotate(90deg);
}

.preinspect-group-name {
  font-weight: 500;
  color: #1f2329;
}

.preinspect-group-summary {
  color: #a9aeb8;
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* SQL 单元格：flex 布局把正文区与 badge/复制图标分层，
   图标固定在行尾且不参与文本截断（此前 antd ellipsis 会把它一起裁掉） */
.preinspect-sql-cell {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  min-width: 0;
  max-width: 100%;
}

.preinspect-sql-main {
  flex: 1 1 auto;
  min-width: 0;
}

/* 前导注释行：弱化置顶，单行截断（完整内容见 hover title / SQL 内容 Tab） */
.preinspect-sql-comment {
  color: #8a919e;
  font-size: 12px;
  line-height: 18px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.preinspect-sql-text {
  display: block;
  color: #1f2329;
  line-height: 20px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.preinspect-row.is-group-member > :deep(.ant-table-cell:first-child) {
  padding-left: 20px;
  color: #a9aeb8;
}

.preinspect-copy-icon {
  flex: 0 0 auto;
  margin-top: 3px;
  color: #a9aeb8;
  font-size: 13px;
  cursor: pointer;
  transition: color 0.15s ease;
}

.preinspect-copy-icon:hover {
  color: #1677ff;
}

.preinspect-audit-group {
  display: inline-flex;
  align-items: center;
  gap: 12px;
}

.preinspect-audit-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  line-height: 20px;
  cursor: pointer;
  user-select: none;
}

.preinspect-audit-chip.is-empty {
  opacity: 0.45;
  cursor: default;
}

.preinspect-audit-icon--must {
  color: #cf1322;
  font-size: 14px;
}

.preinspect-audit-icon--suggest {
  color: #faad14;
  font-size: 14px;
}

.preinspect-audit-num {
  color: inherit;
}

/* ===== 审核结果：整条按状态着色（通过=绿 / 有告警=黄 / 不通过=红） ===== */
.preinspect-result-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  line-height: 20px;
  white-space: nowrap;
}

.preinspect-result-icon {
  font-size: 12px;
  color: inherit;
}

/* 通过：绿色 */
.preinspect-result-tag--passed {
  color: #389e0d;
}

/* 有告警（仅建议改进）：黄色 */
.preinspect-result-tag--warn {
  color: #d48806;
}

/* 不通过（含必须改进/语法错误）：红色 */
.preinspect-result-tag--fail {
  color: #cf1322;
}

.preinspect-expand-body {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 2px 4px;
}

.preinspect-expand-line {
  font-size: 13px;
  line-height: 1.5;
  color: #1f2329;
  word-break: break-word;
}

.preinspect-expand-index,
.preinspect-expand-separator,
.preinspect-expand-text {
  color: inherit;
}

/* 审核信息里 `xxx` 反引号渲染出的行内代码：等宽字体 + 浅底色，与纯文本区分 */
.preinspect-inline-code {
  padding: 0 5px;
  border-radius: 4px;
  background: rgba(0, 0, 0, 0.06);
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 0.92em;
  color: inherit;
}

/* 仅级别文字带色：必须改进红 / 建议改进黄；其余文字全部恢复默认无样式 */
.preinspect-expand-level--must {
  color: #cf1322;
}

.preinspect-expand-level--suggest {
  color: #faad14;
}

/* 致命错误汇总：置顶，与行内卡片区分层级 */
.preinspect-fatal-alert {
  margin-bottom: 12px;
}

/* ===== 跨语句提示：行内角标 + 表尾卡片 ===== */
.preinspect-group-badge {
  display: inline-flex;
  align-items: center;
  flex: 0 0 auto;
  height: 18px;
  margin-left: 2px;
  padding: 0 7px;
  border: 1px solid #ffd591;
  border-radius: 9px;
  background: #fff7e6;
  color: #ad6800;
  font-size: 11px;
  line-height: 1;
  cursor: help;
  vertical-align: middle;
}

.preinspect-row.is-flash > :deep(.ant-table-cell) {
  animation: preinspectRowFlash 1.6s ease;
}

@keyframes preinspectRowFlash {
  0%,
  60% {
    background: rgba(22, 119, 255, 0.12);
  }
  100% {
    background: transparent;
  }
}

.preinspect-group-hint {
  margin-top: 10px;
  border: 1px dashed #ffd591;
  border-left: 3px solid #faad14;
  border-radius: 6px;
  background: #fffbe6;
}

.preinspect-group-hint.is-must {
  border-color: #ffa39e;
  border-left-color: #cf1322;
  background: #fff7f6;
}

.preinspect-group-hint-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 14px;
  cursor: pointer;
  user-select: none;
}

.preinspect-group-hint-head:hover {
  background: rgba(0, 0, 0, 0.02);
}

.preinspect-group-hint-title {
  font-size: 13px;
  font-weight: 600;
  color: #1f2329;
}

.preinspect-group-hint-toggle {
  margin-left: auto;
  font-size: 12px;
  color: #1677ff;
}

.preinspect-group-hint-body {
  padding: 2px 14px 12px;
}

.preinspect-hint-item {
  margin-top: 8px;
  padding: 10px 14px;
  border: 1px solid #f0f0f0;
  border-radius: 6px;
  background: #fff;
}

.preinspect-hint-msg {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: #1f2329;
  line-height: 1.5;
}

.preinspect-hint-level {
  font-size: 12px;
  font-weight: 600;
}

.preinspect-hint-level--must {
  color: #cf1322;
}

.preinspect-hint-level--suggest {
  color: #d46b08;
}

.preinspect-hint-text {
  word-break: break-word;
}

.preinspect-hint-times {
  padding: 0 6px;
  border-radius: 8px;
  background: #f0f0f0;
  color: #595959;
  font-size: 11px;
}

.preinspect-hint-rule {
  font-size: 12px;
  color: #86909c;
}

.preinspect-hint-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
}

.preinspect-hint-meta-label,
.preinspect-hint-meta-empty {
  font-size: 12px;
  color: #86909c;
}

.preinspect-hint-rel {
  display: inline-flex;
  align-items: center;
  height: 20px;
  padding: 0 9px;
  border: 1px solid #d9d9d9;
  border-radius: 10px;
  background: #fafafa;
  color: #1677ff;
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
  transition: all 0.15s ease;
}

.preinspect-hint-rel:hover {
  border-color: #1677ff;
  background: #e6f4ff;
}

/* ===== Tab2：SQL 文本 ===== */
.sql-text-box {
  display: flex;
  flex-direction: column;
}

.sql-text-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 0 2px 6px;
}

.sql-text-meta {
  font-size: 12px;
  color: #86909c;
}

.sql-text-actions {
  display: inline-flex;
  align-items: center;
  gap: 12px;
}

.sql-text-action-icon {
  color: #a9aeb8;
  font-size: 15px;
  cursor: pointer;
  transition: color 0.15s ease;
}

.sql-text-action-icon:hover {
  color: #1677ff;
}

@media (max-width: 992px) {
  .header-action-slot {
    position: static;
    max-width: 100%;
    justify-content: flex-start;
    padding-top: 6px;
  }
  .detail-title-block {
    padding-right: 0;
  }
  .detail-title-row {
    padding-right: 0;
  }
  /* 中屏：名称最大宽度收窄，保证 ID 紧跟不换行 */
  .detail-title-text--ellipsis {
    max-width: 320px;
  }
}

@media (max-width: 768px) {
  /* 窄屏：名称最大宽度进一步收窄，小屏手机上也保证 ID 紧贴可见 */
  .detail-title-text--ellipsis {
    max-width: 200px;
  }
}</style>

<!-- SQL 详情弹窗：a-modal teleport 到 body，样式必须放非 scoped 块 -->
<style>
.sql-detail-modal .ant-modal-body {
  padding: 16px 20px;
}

.sql-detail-modal .sql-modal-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.sql-detail-modal .sql-modal-meta {
  font-size: 13px;
  color: #4e5969;
}

.sql-detail-modal .sql-modal-actions {
  display: inline-flex;
  align-items: center;
  gap: 14px;
}

.sql-detail-modal .sql-modal-wrap-toggle {
  font-size: 13px;
  color: #4e5969;
}

.sql-detail-modal .sql-modal-wrap-toggle:hover {
  color: #1677ff;
}

.sql-detail-modal .sql-modal-copy-icon {
  color: #a9aeb8;
  font-size: 15px;
  cursor: pointer;
  transition: color 0.15s ease;
}

.sql-detail-modal .sql-modal-copy-icon:hover {
  color: #1677ff;
}

.sql-detail-modal .sql-modal-editor {
  border: 1px solid #f0f0f0;
  border-radius: 8px;
  overflow: hidden;
}

/* 换行开关：CodeMirror 行内容在 pre 与 pre-wrap 间切换 */
.sql-detail-modal .sql-modal-editor.is-wrap .cm-content {
  white-space: pre-wrap !important;
  word-break: break-all;
}

.sql-detail-modal .sql-modal-footer {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}

@media (max-width: 768px) {
  /* 5 段水平步骤条 */
  .steps-shell {
    padding: 10px 12px;
  }

  .steps-shell :deep(.ant-steps-item) {
    min-width: 0;
  }

  .steps-shell :deep(.ant-steps-item-title) {
    font-size: 11px;
    padding-inline-end: 6px;
    white-space: normal;
    line-height: 1.3;
  }

  .order-step-standard-hint {
    font-size: 11px;
  }

  /* 执行结果表格没有 scroll.x，列宽合计已超过 375px */
  .detail-section-card :deep(.ant-table-content) {
    overflow-x: auto;
  }

  .detail-section-card :deep(.ant-table) {
    min-width: 640px;
  }

  .preinspect-tabs :deep(.ant-table-content) {
    overflow-x: auto;
  }

  .preinspect-tabs :deep(.ant-table) {
    min-width: 760px;
  }

  .detail-header {
    padding: 12px 12px 8px;
  }

  .detail-title-row {
    gap: 8px;
  }

  .detail-title-text--ellipsis {
    max-width: 150px;
  }

  .copy-order-id-btn {
    width: 32px;
    height: 32px;
  }

  .preinspect-copy-icon {
    width: 28px;
    height: 28px;
    font-size: 15px;
  }

  .sql-text-toolbar {
    gap: 8px;
  }

  .sql-text-action-icon {
    font-size: 18px;
    padding: 4px;
  }

  .failure-drawer-sql,
  .failure-drawer-raw {
    max-height: 30vh;
    font-size: 11px;
  }
}
</style>
