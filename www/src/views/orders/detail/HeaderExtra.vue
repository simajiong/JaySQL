<template>
  <a-space class="header-action-group" wrap>
    <a-tooltip :title="submitApprovalBlockReason" :disabled="!submitApprovalBlocked">
      <a-button
        v-if="showSubmitApproval"
        type="primary"
        size="small"
        :disabled="submitApprovalBlocked"
        @click="openSubmitApprovalModal"
      >
        提交审批
      </a-button>
    </a-tooltip>

    <a-button
      v-if="canRecheck"
      size="small"
      :loading="uiState.recheckLoading"
      @click="handleRecheck"
    >
      重新检查
    </a-button>

    <a-button
      v-if="canApprove"
      type="primary"
      size="small"
      @click="openApprovalModal('APPROVED')"
    >
      审批通过
    </a-button>

    <a-button
      v-if="canApprove"
      danger
      size="small"
      @click="openApprovalModal('REJECTED')"
    >
      审批不通过
    </a-button>

    <a-button
      v-if="canTransfer"
      size="small"
      @click="openTransferModal"
    >
      转交
    </a-button>

    <a-button
      v-if="canExecute"
      type="primary"
      size="small"
      :loading="uiState.genOrderTasksLoading"
      @click="genOrderTasks"
    >
      执行
    </a-button>

    <a-button
      v-if="canRestart"
      size="small"
      :loading="uiState.genOrderTasksLoading"
      @click="genOrderTasks"
    >
      重启
    </a-button>

    <!-- 查看任务：任务页路由是隐藏的（无菜单入口），已生成过任务的工单
         在此之前只能手敲 URL 到达；已完成工单的回滚 SQL 就存在任务详情里 -->
    <a-button v-if="canViewTasks" size="small" @click="goOrderTasks">
      查看任务
    </a-button>

    <a-button
      v-if="canWithdraw"
      size="small"
      @click="openWithdrawConfirm"
    >
      撤回
    </a-button>

    <a-button
      v-if="canEditWithdrawn"
      type="primary"
      size="small"
      @click="handleEditWithdrawn"
    >
      编辑
    </a-button>

    <a-button
      v-if="canTerminate"
      danger
      size="small"
      @click="openTerminateModal"
    >
      终止
    </a-button>

    <a-button v-if="canCopyAsNewOrder" size="small" @click="handleCopyAsNewOrder">
      类似创建
    </a-button>
  </a-space>

  <!-- 撤回确认弹窗：与工单列表撤回弹窗保持一致（撤回后任务不再执行，可编辑再次提交，工单 ID 不变） -->
  <a-modal
    v-model:open="withdrawConfirm.visible"
    :confirm-loading="withdrawConfirm.submitting"
    :mask-closable="false"
    :width="440"
    wrap-class-name="order-detail-withdraw-confirm-modal"
    @cancel="closeWithdrawConfirm"
  >
    <template #title>
      <div class="withdraw-title">
        <span class="withdraw-title-icon">
          <ExclamationCircleFilled />
        </span>
        <span>确认要撤回吗？</span>
      </div>
    </template>
    <div class="withdraw-body">撤回后，任务不再执行，支持编辑再次提交</div>
    <template #footer>
      <a-button :disabled="withdrawConfirm.submitting" @click="closeWithdrawConfirm">取 消</a-button>
      <a-button :loading="withdrawConfirm.submitting && withdrawConfirm.mode === 'edit'" @click="handleWithdraw(true)">
        撤回并编辑
      </a-button>
      <a-button type="primary" :loading="withdrawConfirm.submitting && withdrawConfirm.mode === 'withdraw'" @click="handleWithdraw(false)">
        撤 回
      </a-button>
    </template>
  </a-modal>

  <a-modal :open="uiData.modalOpen" :title="uiData.modalTitle" @cancel="handleCancel">
    <template #footer>
      <a-button @click="handleCancel">取消</a-button>
      <a-button type="primary" :loading="uiData.modalLoading" @click="handleSubmit">
        {{ uiData.modalOkText }}
      </a-button>
    </template>

    <a-form ref="formRef" :model="uiData.formData" layout="vertical">
      <a-form-item
        v-if="uiData.modalAction === 'submitApproval'"
        label="审批人"
        name="approver"
        :rules="[{ required: true, message: '请选择审批人' }]"
      >
        <a-select
          v-model:value="uiData.formData.approver"
          :options="uiData.userList"
          :field-names="{ label: 'username', value: 'username' }"
          allow-clear
          class="modal-field-full-width"
        />
      </a-form-item>

      <!-- 通知方式：与「选择审批者」同一处，避免审批人选定后再去别处配通知。
           实时提示审批人是否已绑定钉钉，把「发不出去」这件事前置暴露在提交之前。 -->
      <a-form-item
        v-if="uiData.modalAction === 'submitApproval'"
        label="通知方式"
      >
        <div class="notify-channel-block">
          <a-checkbox
            v-model:checked="uiData.formData.notifyDingTalk"
            :disabled="!dingTalkPreview.available"
          >
            钉钉
          </a-checkbox>

          <!-- 未选审批人：先提示，不请求 -->
          <div v-if="!uiData.formData.approver" class="notify-channel-tip">
            选择审批人后，可查看其钉钉接收状态
          </div>
          <div v-else-if="uiData.dingTalkPreviewLoading" class="notify-channel-tip">
            正在检查审批人的钉钉接收状态…
          </div>
          <a-alert
            v-else-if="dingTalkPreview.available && dingTalkStreamDown"
            type="warning"
            show-icon
            message="钉钉长连接未建立：审批人能收到卡片，但在钉钉里点「通过 / 转交」可能无法回传，请联系管理员检查「通知渠道」中的钉钉企业应用配置"
            class="notify-channel-alert"
          />
          <a-alert
            v-else-if="dingTalkPreview.available"
            type="success"
            show-icon
            :message="dingTalkReadyText"
            class="notify-channel-alert"
          />
          <a-alert
            v-else
            type="warning"
            show-icon
            :message="dingTalkPreview.reason || '当前无法通过钉钉通知该审批人'"
            class="notify-channel-alert"
          />

          <div class="notify-channel-tip">飞书等其它平台将在后续版本开放</div>
        </div>
      </a-form-item>

      <a-form-item
        v-if="uiData.modalAction === 'transfer'"
        label="新审批人"
        name="newClaimer"
        :rules="[{ required: true, message: '请选择新审批人' }]"
      >
        <a-select
          v-model:value="uiData.formData.newClaimer"
          :options="uiData.userList"
          :field-names="{ label: 'username', value: 'username' }"
          allow-clear
          class="modal-field-full-width"
        />
      </a-form-item>

      <a-form-item
        :label="modalTextAreaLabel"
        name="confirmMsg"
        :rules="[
          { max: 256, message: '附加信息不能超过256个字符' },
          { validator: validateConfirmMsg, trigger: 'blur' },
        ]"
      >
        <a-textarea
          v-model:value="uiData.formData.confirmMsg"
          :placeholder="confirmMsgPlaceholder"
          :rows="3"
          allow-clear
          class="modal-field-full-width"
        />
      </a-form-item>
    </a-form>
  </a-modal>
</template>

<script setup>
import {
  approvalOrderApi,
  generateOrderTasksApi,
  getOrderUsersApi,
  previewDingTalkNotifyApi,
  submitOrderApprovalApi,
  terminateOrderApi,
  transferOrderApi,
  withdrawOrderApi,
} from '@/api/order'
import { useOrderCreatePrefillStore } from '@/store/prefill'
import { useUserStore } from '@/store/user'
import { useThrottleFn } from '@vueuse/core'
import { ExclamationCircleFilled } from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import { computed, reactive, ref, toRaw, watch } from 'vue'
import { useRouter } from 'vue-router'

const props = defineProps({
  orderDetail: {
    type: Object,
    default: () => ({}),
  },
  inspectData: {
    type: Object,
    default: null,
  },
})

const emit = defineEmits(['refresh'])

const formRef = ref()
const userStore = useUserStore()
const router = useRouter()
const orderCreatePrefillStore = useOrderCreatePrefillStore()

const uiState = reactive({
  genOrderTasksLoading: false,
  recheckLoading: false,
})

const uiData = reactive({
  modalOpen: false,
  modalLoading: false,
  modalAction: '',
  modalTitle: '',
  modalOkText: '确定',
  dingTalkPreviewLoading: false,
  formData: {
    approver: '',
    newClaimer: '',
    confirmMsg: '',
    approvalStatus: 'APPROVED',
    // 是否向审批人推送钉钉审批通知。默认不勾选，选定审批人且其已绑定钉钉时自动勾上。
    notifyDingTalk: false,
  },
  userList: [],
})

// 钉钉接收能力预览：由后端算「这位审批人是否已绑定钉钉、走卡片还是工作通知」。
// 前端不做任何推导（手机号匹配、渠道是否配置都在后端），避免两边口径漂移。
const dingTalkPreview = reactive({
  available: false,
  reason: '',
  channel_name: '',
  mode: '',
  approver_bound: false,
  stream_connected: true,
})

// 卡片模式下长连接断了才是问题：工作通知走「点链接回平台」，不依赖长连接。
const dingTalkStreamDown = computed(
  () => dingTalkPreview.mode === 'card' && !dingTalkPreview.stream_connected,
)

const dingTalkReadyText = computed(() => {
  const approver = uiData.formData.approver || '该审批人'
  if (dingTalkPreview.mode === 'card') {
    return `${approver} 已绑定钉钉，将收到审批卡片，可直接在钉钉内「通过 / 转交」`
  }
  if (dingTalkPreview.mode === 'work_notice') {
    return `${approver} 已绑定钉钉，将以工作通知推送（需点链接回平台审批）`
  }
  return `${approver} 已绑定钉钉，将收到审批通知`
})

const resetDingTalkPreview = () => {
  dingTalkPreview.available = false
  dingTalkPreview.reason = ''
  dingTalkPreview.channel_name = ''
  dingTalkPreview.mode = ''
  dingTalkPreview.approver_bound = false
  dingTalkPreview.stream_connected = true
  uiData.formData.notifyDingTalk = false
  uiData.dingTalkPreviewLoading = false
}

// 审批人变化即查一次接收能力。带竞态防护：快速连续切换审批人时，
// 只认最后一次请求的结果，避免旧响应把新审批人的状态覆盖回去。
let dingTalkPreviewSeq = 0
const fetchDingTalkPreview = async (approver) => {
  const target = String(approver || '').trim()
  if (!target) {
    resetDingTalkPreview()
    return
  }
  const seq = ++dingTalkPreviewSeq
  uiData.dingTalkPreviewLoading = true
  const res = await previewDingTalkNotifyApi({ approver: target }).catch(() => null)
  if (seq !== dingTalkPreviewSeq) return
  uiData.dingTalkPreviewLoading = false
  const data = res?.data || {}
  dingTalkPreview.available = Boolean(data.available)
  dingTalkPreview.reason = data.reason || ''
  dingTalkPreview.channel_name = data.channel_name || ''
  dingTalkPreview.mode = data.mode || ''
  dingTalkPreview.approver_bound = Boolean(data.approver_bound)
  // 后端只在卡片模式下返回 stream_connected，工作通知模式下该字段为 false，
  // 但那时并不代表异常，故按 mode 判定后再用，默认视为正常。
  dingTalkPreview.stream_connected = data.mode === 'card' ? Boolean(data.stream_connected) : true
  // 可用即默认勾选：提交人显式选了审批人，本意就是要通知到他。
  // 不可用则保持未勾选并置灰，避免勾了也发不出去、提交人却以为已通知。
  uiData.formData.notifyDingTalk = dingTalkPreview.available
}

watch(
  () => uiData.formData.approver,
  (value) => {
    if (uiData.modalAction !== 'submitApproval') return
    fetchDingTalkPreview(value)
  },
)

const progress = computed(() => String(props.orderDetail?.progress || '').toUpperCase())
const isApplicant = computed(() => props.orderDetail?.applicant === userStore.username)
const isApprover = computed(() => props.orderDetail?.current_approver === userStore.username)
const isExecutor = computed(() => props.orderDetail?.executor === userStore.username)
const hasPermission = (permission) =>
  Boolean(userStore.is_superuser || (userStore.permissions || []).includes(permission))
const hasSubmitPermission = computed(() => hasPermission('order.submit'))
const hasApprovePermission = computed(() => hasPermission('order.approve'))
const hasExecutePermission = computed(() => hasPermission('order.execute'))
const hasManagePermission = computed(() => hasPermission('order.manage'))

const showSubmitApproval = computed(() => hasSubmitPermission.value && ['WAIT_PRECHECK', 'REJECTED'].includes(progress.value) && isApplicant.value)

const preInspectError = computed(() => String(props.inspectData?.error || ''))
const preInspectData = computed(() => props.inspectData?.data || null)
// 语法错误由后端作为结构化问题项返回并显式标记（has_syntax_error）。
// 历史实现判定 status === 'syntax_error'，但后端从不产生该值，属死代码，这里改用显式字段。
const hasSyntaxError = computed(() => Boolean(preInspectData.value?.has_syntax_error))
// 预审执行失败（连不上库/权限不足等系统错误）：与语法错误区分，提示用户重试而非改 SQL
const preInspectFailed = computed(() => Boolean(preInspectError.value))
const hasMustItems = computed(() => preInspectData.value?.status === 'must_fix' || (preInspectData.value?.must_count || 0) > 0)
const submitApprovalBlocked = computed(() => hasSyntaxError.value || preInspectFailed.value || hasMustItems.value)
const submitApprovalBlockReason = computed(() => {
  if (hasSyntaxError.value && hasMustItems.value) {
    return '当前 SQL 存在语法错误，且规范预审包含必须修改项。请按提示位置修正语法、处理必须修改项后，再点击「重新检查」并提交审批。'
  }
  if (hasSyntaxError.value) {
    return '当前 SQL 存在语法错误，请按提示的位置修正后点击「重新检查」并提交审批。'
  }
  if (preInspectFailed.value) {
    return '规范预审执行失败，请点击「重新检查」重试；若持续失败请联系数据库管理员。'
  }
  if (hasMustItems.value) {
    return `规范预审包含 ${preInspectData.value?.must_count || 0} 项必须修改项，请先整改后再点击「重新检查」并提交审批。`
  }
  return ''
})

const canRecheck = computed(() => hasSubmitPermission.value && ['WAIT_PRECHECK', 'REJECTED'].includes(progress.value) && isApplicant.value)
const canApprove = computed(() => hasApprovePermission.value && progress.value === 'PENDING' && isApprover.value)
const canTransfer = computed(() => hasApprovePermission.value && progress.value === 'PENDING' && isApprover.value)
const canExecute = computed(() => hasExecutePermission.value && progress.value === 'APPROVED' && isExecutor.value)
const canRestart = computed(() => hasExecutePermission.value && progress.value === 'FAILED' && isExecutor.value)
// 查看任务：工单已生成过任务（执行中/已完成/执行失败）即可进入任务页，无角色限制
// （任务页路由本身挂 order.view 权限，与工单详情页同门槛）
const canViewTasks = computed(() => ['EXECUTING', 'COMPLETED', 'FAILED'].includes(progress.value))
// 撤回：与后端 WithdrawOrderService 口径一致，仅执行前（待预审/审批中/审批通过）且申请人本人
const canWithdraw = computed(() =>
  hasSubmitPermission.value
  && ['WAIT_PRECHECK', 'PENDING', 'APPROVED'].includes(progress.value)
  && isApplicant.value,
)
// 编辑：仅「已撤回」工单可原地编辑（工单 ID 不变）
const canEditWithdrawn = computed(() =>
  hasSubmitPermission.value
  && progress.value === 'WITHDRAWN'
  && isApplicant.value,
)
const canTerminate = computed(() =>
  hasManagePermission.value
  && ['WAIT_PRECHECK', 'PENDING', 'APPROVED', 'EXECUTING', 'FAILED', 'REJECTED', 'WITHDRAWN'].includes(progress.value)
  && (isApplicant.value || isApprover.value || isExecutor.value),
)
const canCopyAsNewOrder = computed(() => {
  if (!hasSubmitPermission.value) return false
  if (['WAIT_PRECHECK', 'REJECTED'].includes(progress.value)) return false
  return true
})

const modalTextAreaLabel = computed(() => {
  if (uiData.modalAction === 'submitApproval') return '提交说明'
  if (uiData.modalAction === 'approval') return uiData.formData.approvalStatus === 'REJECTED' ? '驳回原因' : '审批意见'
  if (uiData.modalAction === 'terminate') return '终止原因'
  if (uiData.modalAction === 'transfer') return '转交说明'
  return '附加信息'
})

const confirmMsgPlaceholder = computed(() => {
  if (uiData.modalAction === 'submitApproval') return '可选：填写本次提交审批说明'
  if (uiData.modalAction === 'approval') {
    return uiData.formData.approvalStatus === 'REJECTED' ? '请填写驳回原因（必填）' : '可选：填写审批说明'
  }
  if (uiData.modalAction === 'terminate') return '可选：填写终止原因'
  if (uiData.modalAction === 'transfer') return '可选：填写转交说明'
  return '请输入附加信息'
})

const validateConfirmMsg = async (_rule, value) => {
  if (
    uiData.modalAction === 'approval'
    && uiData.formData.approvalStatus === 'REJECTED'
    && !String(value || '').trim()
  ) {
    throw new Error('驳回时必须填写原因')
  }
}

const openModal = (action, title, okText = '确定') => {
  uiData.modalOpen = true
  uiData.modalAction = action
  uiData.modalTitle = title
  uiData.modalOkText = okText
}

const resetModal = () => {
  uiData.modalOpen = false
  uiData.modalLoading = false
  uiData.modalAction = ''
  uiData.modalTitle = ''
  uiData.modalOkText = '确定'
  uiData.formData.approver = ''
  uiData.formData.newClaimer = ''
  uiData.formData.confirmMsg = ''
  uiData.formData.approvalStatus = 'APPROVED'
  resetDingTalkPreview()
  formRef.value?.resetFields?.()
}

const handleCancel = () => {
  resetModal()
}

const fetchUsers = async () => {
  const res = await getOrderUsersApi({ is_page: false, perm_scope: 'approver' }).catch(() => null)
  const list = Array.isArray(res?.data) ? res.data : []
  const currentUser = {
    username: userStore.username,
  }
  uiData.userList = [
    currentUser,
    ...list.filter((item) => item?.username && item.username !== currentUser.username),
  ].filter((item) => item?.username)
}

const openSubmitApprovalModal = async () => {
  resetDingTalkPreview()
  await fetchUsers()
  openModal('submitApproval', '提交审批', '确定')
}

const openApprovalModal = (status) => {
  uiData.formData.approvalStatus = status
  openModal('approval', status === 'APPROVED' ? '确认审批通过？' : '确认审批不通过？', '确定')
}

const openTransferModal = async () => {
  await fetchUsers()
  openModal('transfer', '转交审批人', '确定')
}

const openTerminateModal = () => {
  openModal('terminate', '终止工单', '确定')
}

const buildOrderPrefill = (orderDetail) => {
  const raw = JSON.parse(JSON.stringify(toRaw(orderDetail || {})))
  return {
    title: raw.title || '',
    remark: raw.remark || '',
    db_type: raw.db_type || 'MySQL',
    instance_id: raw.instance_id,
    database: raw.database,
    schema: raw.schema,
    executor: raw.executor && raw.executor === raw.applicant ? 'SELF' : (raw.executor || 'SELF'),
    estimated_affected_rows: typeof raw.estimated_affected_rows === 'number' ? raw.estimated_affected_rows : null,
    content: raw.content || '',
    rollback_sql: raw.rollback_sql || '',
    cc: raw.cc || [],
  }
}

const handleCopyAsNewOrder = () => {
  orderCreatePrefillStore.setCreatePrefill(buildOrderPrefill(props.orderDetail))
  router.push({ name: 'orders.create' })
}

const withdrawConfirm = reactive({
  visible: false,
  submitting: false,
  orderId: '',
  mode: '', // 'withdraw' | 'edit'
})

const openWithdrawConfirm = () => {
  withdrawConfirm.orderId = props.orderDetail?.order_id
  withdrawConfirm.mode = ''
  withdrawConfirm.visible = true
}

const closeWithdrawConfirm = () => {
  if (withdrawConfirm.submitting) return
  withdrawConfirm.visible = false
}

const handleWithdraw = async (goEdit) => {
  withdrawConfirm.mode = goEdit ? 'edit' : 'withdraw'
  withdrawConfirm.submitting = true
  try {
    const res = await withdrawOrderApi({
      order_id: withdrawConfirm.orderId,
    }).catch(() => null)
    if (!res) return
    withdrawConfirm.visible = false
    if (goEdit) {
      // 撤回并编辑：直接进入编辑态，工单 ID 不变
      message.success('工单已撤回，正在进入编辑页')
      router.push({ name: 'orders.create', query: { edit_order_id: withdrawConfirm.orderId } })
    } else {
      // 撤回：停留在详情页，工单进入「已撤回」，仅支持编辑/终止/类似创建
      message.success('工单已撤回')
      emit('refresh')
    }
  } finally {
    withdrawConfirm.submitting = false
  }
}

const handleEditWithdrawn = () => {
  router.push({ name: 'orders.create', query: { edit_order_id: props.orderDetail?.order_id } })
}

const handleRecheck = useThrottleFn(async () => {
  message.info('正在重新执行规范预审')
  emit('refresh')
})

const handleSubmit = useThrottleFn(async () => {
  uiData.modalLoading = true
  try {
    await formRef.value?.validate?.()

    let res = null
    const payload = {
      order_id: props.orderDetail?.order_id,
      msg: uiData.formData.confirmMsg,
    }

    const handleApiError = (err) => {
      const msg = err?.data?.message || err?.message || '操作失败，请稍后重试'
      message.error(msg)
      return null
    }

    if (uiData.modalAction === 'submitApproval') {
      res = await submitOrderApprovalApi({
        order_id: props.orderDetail?.order_id,
        approver: uiData.formData.approver,
        // 空数组 = 只走原有的群通知（与改动前行为一致），不影响未使用钉钉的部署
        notify_channels: uiData.formData.notifyDingTalk ? ['dingtalk'] : [],
      }).catch(handleApiError)
    }

    if (uiData.modalAction === 'approval') {
      res = await approvalOrderApi({
        ...payload,
        status: uiData.formData.approvalStatus,
      }).catch(handleApiError)
    }

    if (uiData.modalAction === 'transfer') {
      res = await transferOrderApi({
        ...payload,
        new_claimer: uiData.formData.newClaimer,
      }).catch(handleApiError)
    }

    if (uiData.modalAction === 'terminate') {
      res = await terminateOrderApi(payload).catch(handleApiError)
    }

    if (!res) return

    message.success('操作成功')
    resetModal()
    emit('refresh')
  } finally {
    uiData.modalLoading = false
  }
})

// 查看任务：直达任务页（隐藏路由）。跳转失败兜底与 genOrderTasks 同款
// （发版后旧标签页懒加载旧 hash chunk 404 的场景）
const goOrderTasks = async () => {
  const target = {
    name: 'orders.tasks',
    params: { order_id: props.orderDetail?.order_id },
  }
  try {
    await router.push(target)
  } catch (navErr) {
    console.warn('[orders] 跳转任务页失败，整页兜底:', navErr)
    message.warning('页面跳转失败，正在刷新重试…')
    window.location.assign(`/sql-workbench/orders/tasks/${props.orderDetail?.order_id}`)
  }
}

const genOrderTasks = useThrottleFn(async () => {
  uiState.genOrderTasksLoading = true
  try {
    const res = await generateOrderTasksApi({
      order_id: props.orderDetail?.order_id,
    }).catch(() => null)
    if (!res) return

    // 跳转失败（典型场景：发版后旧标签页懒加载旧 hash chunk 404）不再静默，
    // 给用户明确提示并整页跳转兜底（整页加载必取最新前端）
    const target = {
      name: 'orders.tasks',
      params: { order_id: props.orderDetail?.order_id },
    }
    try {
      await router.push(target)
    } catch (navErr) {
      console.warn('[orders] 跳转任务页失败，整页兜底:', navErr)
      message.warning('页面跳转失败，正在刷新重试…')
      window.location.assign(`/sql-workbench/orders/tasks/${props.orderDetail?.order_id}`)
    }
  } finally {
    uiState.genOrderTasksLoading = false
  }
})
</script>

<style scoped>
.header-action-group {
  align-items: center;
  flex-wrap: nowrap !important;
  gap: var(--gi-spacing-xs) var(--gi-spacing-sm);
  justify-content: flex-end;
  width: 100%;
  overflow-x: auto;
  white-space: nowrap;
}

.header-action-group :deep(.ant-btn) {
  border-radius: var(--gi-radius-md);
  flex: 0 0 auto;
}

.modal-field-full-width {
  width: 100%;
}

/* 通知方式：勾选与状态提示同排展示，提示行不再单独占一行表单，避免弹窗被撑高 */
.notify-channel-block {
  display: flex;
  flex-direction: column;
  gap: var(--gi-spacing-sm);
  width: 100%;
}

.notify-channel-block .ant-checkbox-wrapper {
  margin-inline-start: 0;
}

.notify-channel-tip {
  color: var(--gi-color-text-tertiary);
  font-size: var(--gi-font-size-caption);
  line-height: 1.6;
}

.notify-channel-alert {
  align-items: flex-start;
}

.notify-channel-alert :deep(.ant-alert-message) {
  font-size: var(--gi-font-size-caption);
  line-height: 1.6;
}

@media (max-width: 767px) {
  .header-action-group {
    width: 100%;
  }
}
</style>

<style>
/* 撤回确认弹窗样式（a-modal teleport 到 body，须放非 scoped 块），与工单列表撤回弹窗一致 */
.order-detail-withdraw-confirm-modal .withdraw-title {
  display: flex;
  align-items: center;
  gap: 10px;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.88);
}

.order-detail-withdraw-confirm-modal .withdraw-title-icon {
  display: inline-flex;
  font-size: 22px;
  color: #faad14;
  line-height: 1;
}

.order-detail-withdraw-confirm-modal .withdraw-body {
  padding: 4px 0 8px 32px;
  color: rgba(0, 0, 0, 0.65);
  font-size: 14px;
  line-height: 1.6;
}
</style>
