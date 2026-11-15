<template>
  <a-modal
    :open="props.open"
    :title="null"
    :width="modalWidth"
    :footer="null"
    :body-style="{ padding: 0, overflow: 'hidden' }"
    wrapClassName="user-detail-modal-wrap"
    @cancel="handleCancel"
  >
    <div class="user-detail">
      <div class="detail-body">
        <div class="section basic-section">
          <div class="section-title">
            <UserOutlined class="title-icon" />
            <span>基本信息</span>
          </div>
          <div class="info-grid">
            <div class="info-item">
              <div class="info-label">用户名</div>
              <div class="info-value">{{ userInfo.username || '-' }}</div>
            </div>
            <div class="info-item">
              <div class="info-label">邮箱</div>
              <div class="info-value">{{ userInfo.email || '-' }}</div>
            </div>
            <div class="info-item">
              <div class="info-label">手机号</div>
              <div class="info-value">{{ userInfo.mobile || '-' }}</div>
            </div>
            <!-- 钉钉账号：不需要手工绑定，提交审批时按手机号自动匹配钉钉 userid 并回填。
                 这里只做只读核对，便于排查「审批人为什么收不到钉钉通知」。 -->
            <div class="info-item">
              <div class="info-label">钉钉账号</div>
              <div class="info-value">
                <template v-if="userInfo.dingtalk_userid">
                  {{ userInfo.dingtalk_userid }}
                  <span class="text-muted">（已绑定）</span>
                </template>
                <span v-else class="text-muted">未绑定（按手机号自动匹配）</span>
              </div>
            </div>
            <div class="info-item">
              <div class="info-label">状态</div>
              <div class="info-value">{{ userStatusText }}</div>
            </div>
            <div class="info-item">
              <div class="info-label">加入时间</div>
              <div class="info-value">{{ userInfo.date_joined || '-' }}</div>
            </div>
            <div class="info-item">
              <div class="info-label">角色</div>
              <div class="info-value role-value">
                <template v-if="sortedRoles.length">
                  <template v-for="(item, idx) in sortedRoles" :key="item.id">
                    <template v-if="item.is_builtin">[默认]</template>
                    <template v-else>[自定义]</template>
                    {{ item.name }}<span v-if="idx < sortedRoles.length - 1" class="role-sep">、</span>
                  </template>
                </template>
                <span v-else class="text-muted">未分配角色</span>
              </div>
            </div>
          </div>
        </div>

        <div class="section perm-section">
          <div class="section-title">
            <SafetyOutlined class="title-icon" />
            <span>权限详情</span>
            <span class="perm-total">共 {{ effectivePermissions.length }} 项权限</span>
          </div>

          <div v-if="effectivePermissions.length" class="perm-scroll">
            <div class="perm-wrapper">
              <a-tabs v-model:activeKey="activeModuleKey" class="perm-tabs" size="small">
                <a-tab-pane
                  v-for="mod in moduleOrder"
                  :key="mod"
                  :tab="MODULE_LABEL_MAP[mod] + ' (' + (moduleGrouped[mod]?.total || 0) + ')'"
                >
                  <div v-if="moduleGrouped[mod]?.subs?.length" class="perm-content">
                    <div
                      v-for="sub in moduleGrouped[mod].subs"
                      :key="sub.key"
                      class="sub-row"
                    >
                      <span class="sub-label">{{ sub.label }}：</span>
                      <span class="perm-inline">
                        <template v-for="(p, idx) in sub.items" :key="p.code">
                          <span class="perm-item-inline" :title="p.code">{{ p.cnName }}</span><span v-if="idx < sub.items.length - 1" class="perm-gap"></span>
                        </template>
                      </span>
                    </div>
                  </div>
                  <a-empty
                    v-else
                    :image="Empty.PRESENTED_IMAGE_SIMPLE"
                    description="当前模块暂无权限"
                    style="padding: 24px 0;"
                  />
                </a-tab-pane>
              </a-tabs>
            </div>
          </div>
          <a-empty
            v-else
            description="暂未授予任何有效权限"
            :image="Empty.PRESENTED_IMAGE_SIMPLE"
            style="padding: 24px 0;"
          />
        </div>
      </div>
    </div>
  </a-modal>
</template>

<script setup>
import { getPermissionsApi, getUserAccessApi } from '@/api/admin'
import {
  UserOutlined,
  SafetyOutlined,
} from '@ant-design/icons-vue'
import { Empty } from 'ant-design-vue'
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useWindowSize } from '@vueuse/core'

const { width: winWidth } = useWindowSize()

const modalWidth = computed(() => {
  const w = typeof winWidth.value === 'number' ? winWidth.value : 1280
  return Math.min(820, Math.max(380, Math.floor(w * 0.78)))
})

let lockTimers = []

const lockModalPosition = () => {
  if (typeof document === 'undefined') return
  const wrap = document.querySelector('.user-detail-modal-wrap')
  if (!wrap) return
  const header = wrap.querySelector('.ant-modal-header')
  const modalEl = wrap.querySelector('.ant-modal')
  if (header) {
    header.style.pointerEvents = 'none'
    const tEl = header.querySelector('.ant-modal-title')
    const cEl = header.querySelector('.ant-modal-close')
    if (tEl) tEl.style.pointerEvents = 'auto'
    if (cEl) cEl.style.pointerEvents = 'auto'
  }
  if (modalEl) {
    if (modalEl.style.transform) modalEl.style.transform = ''
    if (modalEl.style.left) modalEl.style.left = ''
    if (modalEl.style.top) modalEl.style.top = ''
    if (modalEl.style.marginLeft) modalEl.style.marginLeft = ''
    if (modalEl.style.marginTop) modalEl.style.marginTop = ''
  }
}

const clearLockTimers = () => {
  while (lockTimers.length) {
    const t = lockTimers.pop()
    if (t != null) clearTimeout(t)
  }
}

const MODULE_LABEL_MAP = {
  sql: 'SQL查询',
  order: '工单管理',
  sql_order: 'SQL 工作台',
  datasource: '数据源管理',
  backup: '备份恢复',
  monitor: '监控告警',
  performance: '性能分析',
  audit: '审计日志',
  datasec: '数据安全',
  system: '系统管理',
}

const ACTION_LABEL_MAP = {
  access: '访问',
  view: '查看',
  manage: '管理',
  execute: '执行',
  approve: '审批',
  submit: '提交',
  test: '测试',
  sync_meta: '同步元数据',
  export: '导出',
  download: '下载',
  // 与 UserFormModal 保持一致：让 datasource.instance.ssh 归组到 datasource.instance
  ssh: 'SSH登录',
}

const SUB_LABEL_MAP = {
  'sql.console': 'SQL工作台',
  'sql.favorite': 'SQL收藏',
  'sql.history': 'SQL历史',
  'sql.main': 'SQL查询',
  'order.order': '工单管理',
  'order.main': '工单管理',
  'backup.task': '备份任务',
  'backup.set': '备份集',
  'backup.restore': '恢复任务',
  'backup.main': '备份恢复',
  'monitor.monitor': '监控告警',
  'monitor.main': '监控告警',
  'datasource.instance': '数据源',
  'datasource.param': '参数管理',
  'datasource.compare': '多实例对比',
  'audit.log': '审计日志',
  'audit.policy': '审计策略',
  'audit.main': '审计日志',
  'system.admin': '系统管理',
  'system.user': '用户管理',
  'system.role': '角色管理',
  'system.permission': '权限点管理',
  'system.environment': '环境管理',
  'system.instance': '实例管理',
  'system.inspect': 'SQL规范',
  'system.das': '数据访问授权',
  'system.approval_flow': '审批流配置',
  'system.main': '系统管理',
}

const DISPLAY_ORDER = ['sql_order', 'datasource', 'backup', 'monitor', 'performance', 'audit', 'datasec', 'system']
const moduleOrder = DISPLAY_ORDER
const ALL_ACTION_KEYS = new Set(Object.keys(ACTION_LABEL_MAP))

onBeforeUnmount(() => {
  clearLockTimers()
})

const props = defineProps({
  open: Boolean,
  user: Object,
})
const emit = defineEmits(['update:open'])

// 注意：此 watch 必须定义在 props 之后（immediate 会同步求值 props.open，
// 放在 defineProps 之前会触发 TDZ ReferenceError 导致整页白屏）
watch(
  () => props.open,
  (val) => {
    clearLockTimers()
    if (val) {
      lockTimers.push(setTimeout(lockModalPosition, 0))
      lockTimers.push(setTimeout(lockModalPosition, 80))
      lockTimers.push(setTimeout(lockModalPosition, 200))
      lockTimers.push(setTimeout(lockModalPosition, 500))
    }
  },
  { immediate: true }
)

const loading = ref(false)
const userInfo = reactive({ ...props.user })
const roles = ref([])
const effectivePermissions = ref([])
const allPermissions = ref([])
const activeModuleKey = ref('sql_order')

const sortedRoles = computed(() => [...(roles.value || [])].sort((a, b) => {
  if (!!a.is_builtin === !!b.is_builtin) return 0
  return a.is_builtin ? -1 : 1
}))

const userStatusText = computed(() =>
  userInfo.is_active && !userInfo.is_deleted ? '在线' : '离线'
)

const permCodeToPerm = computed(() => {
  const map = {}
  ;(allPermissions.value || []).forEach((p) => {
    const code = (p.code || '').toString()
    if (code) {
      const parts = code.split('.')
      const moduleKey = parts[0] || ''
      const action = parts[parts.length - 1] || ''
      const actionLabel = ACTION_LABEL_MAP[action] || action

      let subKey
      const lastIsAction = ALL_ACTION_KEYS.has(parts[parts.length - 1])
      if (parts.length >= 3 && lastIsAction) {
        subKey = parts.slice(0, parts.length - 1).join('.')
      } else if (parts.length === 2 && lastIsAction) {
        subKey = `${moduleKey}.main`
      } else if (parts.length === 2) {
        subKey = p.code
      } else {
        subKey = `${moduleKey}.main`
      }

      const subLabel = SUB_LABEL_MAP[subKey] || MODULE_LABEL_MAP[moduleKey] || subKey

      const rawName = p.name || p.label || ''
      let cnName
      if (/[\u4e00-\u9fa5]/.test(rawName)) {
        cnName = rawName
      } else {
        cnName = actionLabel && subLabel && actionLabel !== subLabel
          ? `${actionLabel} ${subLabel}`
          : (rawName || actionLabel || code)
      }
      map[code] = { ...p, code, cnName, module: moduleKey, subKey }
    }
  })
  return map
})

const moduleGrouped = computed(() => {
  const result = {}
  DISPLAY_ORDER.forEach((m) => {
    result[m] = { subs: [], total: 0 }
  })
  const subMap = {}
  const sourceModuleMap = {
    sql_order: ['sql', 'order'],
    datasource: ['datasource'],
    backup: ['backup'],
    monitor: ['monitor'],
    performance: ['performance'],
    audit: ['audit'],
    system: ['system'],
  }
  const toDisplayMap = {}
  Object.entries(sourceModuleMap).forEach(([disp, sources]) => {
    sources.forEach((s) => { toDisplayMap[s] = disp })
  })

  ;(effectivePermissions.value || []).forEach((code) => {
    const p = permCodeToPerm.value[code]
    if (!p) return
    const parts = (p.code || '').toString().split('.')
    const moduleKey = parts[0] || 'other'
    if (!MODULE_LABEL_MAP[moduleKey]) return

    let subKey
    const lastIsAction = ALL_ACTION_KEYS.has(parts[parts.length - 1])
    if (parts.length >= 3 && lastIsAction) {
      subKey = parts.slice(0, parts.length - 1).join('.')
    } else if (parts.length === 2 && lastIsAction) {
      subKey = `${moduleKey}.main`
    } else if (parts.length === 2) {
      subKey = p.code
    } else {
      subKey = `${moduleKey}.main`
    }

    const dispKey = toDisplayMap[moduleKey] || moduleKey
    if (!subMap[dispKey]) subMap[dispKey] = {}
    if (!subMap[dispKey][subKey]) subMap[dispKey][subKey] = []
    subMap[dispKey][subKey].push(p)
  })

  const sortSubKeysByModule = (dispKey, keys) => {
    if (dispKey === 'sql_order') {
      const sqlOrder = [
        'sql.console', 'sql.favorite', 'sql.history', 'sql.main',
        'order.order', 'order.main',
      ]
      return keys.slice().sort((a, b) => {
        const ia = sqlOrder.indexOf(a)
        const ib = sqlOrder.indexOf(b)
        if (ia === -1 && ib === -1) return a.localeCompare(b)
        if (ia === -1) return 1
        if (ib === -1) return -1
        return ia - ib
      })
    }
    if (dispKey === 'datasource') {
      // 数据源 Tab 分组顺序：数据源 → 参数管理 → 多实例对比（与编辑弹窗一致）
      const preferred = ['datasource.instance', 'datasource.param', 'datasource.compare']
      return keys.slice().sort((a, b) => {
        const ia = preferred.indexOf(a)
        const ib = preferred.indexOf(b)
        if (ia === -1 && ib === -1) return a.localeCompare(b)
        if (ia === -1) return 1
        if (ib === -1) return -1
        return ia - ib
      })
    }
    return keys.sort()
  }

  const sortItemsByAction = (items) => {
    const actionOrder = ['access', 'view', 'manage', 'execute', 'submit', 'approve', 'test', 'sync_meta', 'export', 'download']
    return items.slice().sort((a, b) => {
      const pa = (a.code || '').split('.').pop() || ''
      const pb = (b.code || '').split('.').pop() || ''
      const ia = actionOrder.indexOf(pa)
      const ib = actionOrder.indexOf(pb)
      if (ia === -1 && ib === -1) return (a.code || '').localeCompare(b.code || '')
      if (ia === -1) return 1
      if (ib === -1) return -1
      return ia - ib
    })
  }

  DISPLAY_ORDER.forEach((dispKey) => {
    const subs = subMap[dispKey] || {}
    const subKeys = sortSubKeysByModule(dispKey, Object.keys(subs))
    let modTotal = 0
    subKeys.forEach((subKey) => {
      let subLabel
      const subParts = subKey.split('.')
      const moduleKey = subParts[0]
      if (subKey === `${moduleKey}.main`) {
        subLabel = MODULE_LABEL_MAP[moduleKey]
      } else {
        subLabel = SUB_LABEL_MAP[subKey]
        if (!subLabel) {
          const parts = subKey.split('.')
          subLabel = parts.length >= 2
            ? parts.slice(1).map((s) => SUB_LABEL_MAP?.[parts[0] + '.' + s] || s).join(' / ')
            : MODULE_LABEL_MAP[moduleKey]
        }
      }
      const items = sortItemsByAction(subs[subKey])
      modTotal += items.length
      result[dispKey].subs.push({
        key: subKey,
        label: subLabel,
        items,
      })
    })
    result[dispKey].total = modTotal
  })
  return result
})

const handleCancel = () => emit('update:open', false)

const fetchPermissions = async () => {
  const res = await getPermissionsApi({ is_page: false }).catch(() => {})
  allPermissions.value = res?.data || []
}

const fetchUserAccess = async () => {
  if (!props.user?.uid) return
  loading.value = true
  try {
    const res = await getUserAccessApi(props.user.uid).catch(() => {})
    if (res) {
      const data = res.data || {}
      roles.value = data.roles || []
      effectivePermissions.value = data.effective_permissions || []
    }
  } finally {
    loading.value = false
  }
}

watch(
  () => props.user,
  (newUser) => {
    if (newUser) {
      Object.assign(userInfo, newUser)
      fetchUserAccess()
    }
  },
  { deep: true, immediate: true }
)

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen && props.user?.uid) {
      fetchPermissions().then(fetchUserAccess)
      for (const mod of DISPLAY_ORDER) {
        if (moduleGrouped.value[mod]?.total > 0) {
          activeModuleKey.value = mod
          break
        }
      }
    }
  }
)
</script>

<style scoped>
.user-detail {
  width: 100%;
  height: calc(100vh - 220px);
  max-height: calc(100vh - 220px);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  box-sizing: border-box;
}

@media (max-height: 760px) {
  .user-detail {
    height: calc(100vh - 180px);
    max-height: calc(100vh - 180px);
  }
}

.detail-body {
  padding: 16px 24px 20px 24px;
  display: flex;
  flex-direction: column;
  height: 100%;
  max-height: 100%;
  box-sizing: border-box;
  min-height: 0;
  flex: 1;
  overflow: hidden;
}

.section {
  flex-shrink: 0;
}

.basic-section {
  margin-bottom: 12px;
}

.perm-section {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}

.section-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 14px;
  font-weight: 600;
  color: #1f1f1f;
  margin-bottom: 10px;
  padding-bottom: 8px;
  border-bottom: 1px solid #f0f0f0;
  flex-shrink: 0;
}

.title-icon {
  color: #1677ff;
  font-size: 14px;
}

.perm-total {
  margin-left: auto;
  font-size: 12px;
  font-weight: 400;
  color: #8c8c8c;
}

.info-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 10px 20px;
  background: #fafafa;
  border: 1px solid #f0f0f0;
  border-radius: 8px;
  padding: 14px 18px;
}

.info-item {
  min-width: 0;
}

.info-label {
  font-size: 12px;
  color: #8c8c8c;
  margin-bottom: 3px;
}

.info-value {
  font-size: 13px;
  color: #262626;
  font-weight: 500;
  line-height: 1.4;
  word-break: break-all;
}

.role-value {
  display: inline;
}

.role-sep {
  margin: 0 2px;
}

.text-muted {
  color: #bfbfbf;
}

.perm-scroll {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.perm-wrapper {
  border: 1px solid #f0f0f0;
  border-radius: 8px;
  overflow: hidden;
  background: #fff;
  flex: 1;
  min-height: 0;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
}

:deep(.perm-tabs) {
  margin-bottom: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

:deep(.perm-tabs > .ant-tabs-nav) {
  margin-bottom: 0;
  padding: 0;
  background: #fafafa;
  border-bottom: 1px solid #f0f0f0;
  flex-shrink: 0;
  width: 100%;
}

:deep(.perm-tabs > .ant-tabs-nav .ant-tabs-nav-wrap) {
  width: 100%;
}

:deep(.perm-tabs > .ant-tabs-nav .ant-tabs-nav-list) {
  width: 100%;
  display: flex;
  flex-wrap: nowrap;
  justify-content: space-between;
}

:deep(.perm-tabs .ant-tabs-tab) {
  flex: 1 1 0%;
  min-width: 0;
  max-width: none;
  width: 0;
  padding: 8px 0;
  font-size: 13px;
  font-weight: 500;
  margin: 0 !important;
  text-align: center;
  justify-content: center;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  box-sizing: border-box;
}

:deep(.perm-tabs .ant-tabs-nav::before) {
  border-bottom: none !important;
}

:deep(.perm-tabs > .ant-tabs-content-holder) {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}

:deep(.perm-tabs > .ant-tabs-content-holder > .ant-tabs-content) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

:deep(.perm-tabs > .ant-tabs-content-holder > .ant-tabs-content > .ant-tabs-tabpane) {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.perm-content {
  padding: 10px 14px;
  height: 100%;
  box-sizing: border-box;
}

.sub-row {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  padding: 5px 0;
  font-size: 12.5px;
  line-height: 1.6;
  border-bottom: 1px dashed #f0f0f0;
}

.sub-row:last-child {
  border-bottom: none;
}

.sub-label {
  flex-shrink: 0;
  font-weight: 600;
  color: #595959;
  white-space: nowrap;
  min-width: 82px;
}

.perm-inline {
  display: inline;
  line-height: 1.6;
  color: #262626;
}

.perm-item-inline {
  display: inline;
  cursor: default;
  transition: color 0.15s ease;
  border-radius: 2px;
}

.perm-item-inline:hover {
  color: #1677ff;
}

.perm-gap {
  display: inline-block;
  width: 14px;
  height: 0;
}
</style>

<style>
.user-detail-modal-wrap .ant-modal {
  position: absolute !important;
  left: 50% !important;
  top: 32px !important;
  transform: translateX(-50%) !important;
  margin: 0 !important;
  padding: 0 !important;
  width: min(820px, 78vw) !important;
  min-width: 380px !important;
  max-width: min(820px, 78vw) !important;
  max-height: calc(100vh - 64px) !important;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden !important;
  box-sizing: border-box !important;
}

.user-detail-modal-wrap .ant-modal-content {
  max-height: calc(100vh - 64px) !important;
  height: auto;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden !important;
  box-sizing: border-box !important;
}

.user-detail-modal-wrap .ant-modal-body {
  max-height: calc(100vh - 64px) !important;
  flex: 1 1 auto !important;
  min-height: 0 !important;
  overflow: hidden !important;
  padding: 0 !important;
  box-sizing: border-box !important;
}

.user-detail-modal-wrap .ant-modal-header {
  pointer-events: none !important;
  flex-shrink: 0 !important;
}

.user-detail-modal-wrap .ant-modal-close,
.user-detail-modal-wrap .ant-modal-title {
  pointer-events: auto !important;
}

@media (max-width: 768px) {
  .user-detail-modal-wrap .ant-modal {
    min-width: 0 !important;
    width: calc(100vw - 16px) !important;
    max-width: calc(100vw - 16px) !important;
  }
}
</style>
