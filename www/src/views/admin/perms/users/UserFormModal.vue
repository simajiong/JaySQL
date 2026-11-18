<template>
  <a-modal
    :open="props.open"
    :title="props.title"
    :width="modalWidth"
    wrapClassName="user-form-modal-wrap"
    :focusTriggerAfterClose="false"
    :bodyStyle="{
      display: 'flex',
      flexDirection: 'column',
      height: modalBodyHeight,
      maxHeight: modalBodyHeight,
      minHeight: '360px',
      overflow: 'hidden',
    }"
    @cancel="handleCancel"
  >
    <template #footer>
      <a-button @click="handleCancel">取消</a-button>
      <a-button type="primary" :loading="uiState.loading" :disabled="!props.canManage" @click="onSubmit">确定</a-button>
    </template>

    <a-tabs
      v-model:activeKey="activeTabKey"
      :forceRender="true"
      :destroyInactiveTabPane="false"
      class="top-tabs"
    >
      <a-tab-pane key="basic" tab="基本信息">
        <a-form
          ref="formRef"
          :model="formData"
          :rules="rules"
          :label-col="{ span: 4 }"
          :wrapper-col="{ span: 20 }"
          :disabled="!props.canManage"
          class="modal-form"
        >
          <a-form-item label="用户名" name="username" has-feedback>
            <a-input v-model:value="formData.username" placeholder="请输入用户名" allow-clear />
          </a-form-item>
          <a-form-item v-if="isCreate" label="密码" name="password" has-feedback>
            <a-input-password
              v-model:value="formData.password"
              type="password"
              placeholder="请输入密码（大小写+数字+特殊字符）"
              autocomplete="new-password"
            >
              <template #iconRender="visible">
                <EyeOutlined v-if="visible" />
                <EyeInvisibleOutlined v-else />
              </template>
            </a-input-password>
          </a-form-item>
          <a-form-item v-if="!isCreate" label="重置密码" name="reset_password" has-feedback :help="hintResetPwd">
            <a-input-password
              v-model:value="formData.reset_password"
              placeholder="不修改请留空；填写则按此密码重置用户登录密码"
              autocomplete="new-password"
            >
              <template #iconRender="visible">
                <EyeOutlined v-if="visible" />
                <EyeInvisibleOutlined v-else />
              </template>
            </a-input-password>
          </a-form-item>
          <a-form-item label="邮箱" name="email" has-feedback>
            <a-input v-model:value="formData.email" placeholder="请输入邮箱" allow-clear />
          </a-form-item>
          <a-form-item label="手机号" name="mobile" has-feedback>
            <a-input v-model:value="formData.mobile" placeholder="请输入手机号" allow-clear />
          </a-form-item>
          <a-form-item label="默认角色" name="builtin_role_id">
            <a-select
              v-model:value="selectedBuiltinRoleId"
              :options="builtinRoleOptions"
              placeholder="请选择一个默认角色（只能选一个）"
              option-filter-prop="label"
              allow-clear
              style="width: 100%"
              @change="onBuiltinRoleChange"
            />
          </a-form-item>
          <a-form-item label="自定义角色" name="custom_role_ids">
            <a-select
              v-model:value="selectedCustomRoleIds"
              :options="customRoleOptions"
              mode="multiple"
              placeholder="请选择自定义角色（可多选）"
              option-filter-prop="label"
              show-search
              allow-clear
              style="width: 100%"
              @change="onCustomRolesChange"
            />
          </a-form-item>
          <a-form-item label="状态" name="is_active" has-feedback>
            <a-select
              v-model:value="formData.is_active"
              style="width: 100%"
              :options="statusOptions"
              placeholder="请选择状态"
            />
          </a-form-item>
        </a-form>
      </a-tab-pane>

      <a-tab-pane key="permissions" tab="权限分配">
        <div class="permission-tabs-wrap" v-if="(props.permissions || []).length > 0">
          <a-tabs
            v-model:activeKey="activeModuleKey"
            class="module-tabs"
            :forceRender="true"
            :destroyInactiveTabPane="false"
          >
            <a-tab-pane
              v-for="mod in moduleOrder"
              :key="mod"
              :tab="MODULE_LABEL_MAP[mod]"
            >
              <div class="perm-checklist-wrap">
                <div class="perm-checklist">
                  <template v-for="sub in moduleGrouped[mod]?.subs || []" :key="sub.key">
                    <div
                      class="sub-title"
                      :class="{ collapsed: isSubCollapsed(mod, sub.key) }"
                      @click="toggleSub(mod, sub.key)"
                    >
                      <span class="sub-caret">{{ isSubCollapsed(mod, sub.key) ? '▸' : '▾' }}</span>
                      <!-- 分组整体勾选：勾/去勾该分组下所有可操作项（@click.stop 避免触发折叠） -->
                      <a-checkbox
                        class="sub-check"
                        :checked="subAllChecked(sub)"
                        :indeterminate="subIndeterminate(sub)"
                        :disabled="subAllDisabled(sub)"
                        @click.stop
                        @change="(e) => onSubCheckChange(sub, e.target.checked)"
                      />
                      <span class="sub-label">{{ sub.label }}</span>
                    </div>
                    <div v-show="!isSubCollapsed(mod, sub.key)" class="perm-list">
                      <div
                        v-for="perm in sub.items"
                        :key="'wrap-' + (perm.id != null ? perm.id : perm.value)"
                        class="perm-item-wrapper"
                      >
                        <a-checkbox
                          :checked="permChecked(perm)"
                          :disabled="permDisabled(perm)"
                          @change="(e) => onSinglePermChange(perm.id != null ? perm.id : perm.value, e.target.checked)"
                        >
                          <span class="perm-cn">{{ permName(perm) }}</span>
                          <span v-if="permByRole(perm)" class="perm-by-role">（{{ permByRole(perm) }}）</span>
                        </a-checkbox>
                      </div>
                    </div>
                  </template>
                  <a-empty
                    v-if="!moduleGrouped[mod]?.subs?.length"
                    :image="Empty.PRESENTED_IMAGE_SIMPLE"
                    description="当前模块暂无权限点"
                  />
                </div>
              </div>
            </a-tab-pane>
          </a-tabs>
        </div>
      </a-tab-pane>
    </a-tabs>
  </a-modal>
</template>

<script setup>
import { regEmail, regPassword, regPhone } from '@/utils/validate'
import { EyeInvisibleOutlined, EyeOutlined } from '@ant-design/icons-vue'
import { Empty } from 'ant-design-vue'
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useWindowSize } from '@vueuse/core'

const { width: winWidth } = useWindowSize()

const modalWidth = computed(() => {
  const w = typeof winWidth.value === 'number' ? winWidth.value : 1280
  return Math.min(820, Math.max(380, Math.floor(w * 0.78)))
})

const modalBodyHeight = computed(() => {
  const h = typeof window !== 'undefined' ? window.innerHeight : 800
  return 'calc(100vh - ' + Math.max(220, Math.floor(h * 0.32)) + 'px)'
})

let lockTimers = []

const lockModalPosition = () => {
  if (typeof document === 'undefined') return
  const wrap = document.querySelector('.user-form-modal-wrap')
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
    try { clearTimeout(lockTimers.pop()) } catch { /* noop */ }
  }
}

onBeforeUnmount(() => {
  clearLockTimers()
})

const BUILTIN_ROLE_CODES = ['member', 'admin', 'global_readonly', 'super_admin', 'viewer']

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
  // datasource.instance.ssh：加进 ACTION 集合是为了让 code 解析按
  // 「最后一段是动作」归组到 datasource.instance 子分组，而不是散成独立分组
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
  'datasec.rule': '脱敏规则',
  'datasec.plain': '明文工单',
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

// Tab 顺序与侧栏菜单一致：SQL 工作台 → 数据源管理 → 备份恢复 → 监控告警 → 性能分析 → 审计日志 → 数据安全 → 系统管理
const DISPLAY_ORDER = ['sql_order', 'datasource', 'backup', 'monitor', 'performance', 'audit', 'datasec', 'system']
const moduleOrder = DISPLAY_ORDER

const statusOptions = [
  { label: '在线', value: true },
  { label: '离线', value: false },
]

const props = defineProps({
  open: Boolean,
  title: String,
  canManage: {
    type: Boolean,
    default: true,
  },
  roles: {
    type: Array,
    default: () => [],
  },
  permissions: {
    type: Array,
    default: () => [],
  },
})
const emit = defineEmits(['update:open', 'submit'])

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

const formData = defineModel('modelValue', {
  type: Object,
  required: true,
})

const isCreate = computed(() => props.title === '新增用户')
const activeTabKey = ref('basic')
const activeModuleKey = ref('sql_order')

const formRef = ref()
const uiState = reactive({
  loading: false,
})

const isBuiltinRole = (role) => {
  if (typeof role.is_builtin === 'boolean') return role.is_builtin
  if (typeof role.builtin === 'boolean') return role.builtin
  if (role.role_type === 'builtin') return true
  return BUILTIN_ROLE_CODES.includes(role.code)
}

const builtinRoleOptions = computed(() =>
  (props.roles || []).filter((item) => isBuiltinRole(item)).map((item) => ({
    label: item.name,
    value: item.id,
    code: item.code,
    builtin: true,
    permission_ids: item.permission_ids || [],
  })),
)

const customRoleOptions = computed(() =>
  (props.roles || []).filter((item) => !isBuiltinRole(item)).map((item) => ({
    label: item.name,
    value: item.id,
    code: item.code,
    builtin: false,
    permission_ids: item.permission_ids || [],
  })),
)

const allRoleOptions = computed(() =>
  [...builtinRoleOptions.value, ...customRoleOptions.value],
)

const computeInitialRoleIds = () => {
  const ids = new Set()
  if (Array.isArray(formData.value?.role_ids)) {
    formData.value.role_ids.forEach((id) => ids.add(id))
  }
  if (formData.value?.primary_role_id) {
    ids.add(formData.value.primary_role_id)
  }
  if (Array.isArray(formData.value?.addon_role_ids)) {
    formData.value.addon_role_ids.forEach((id) => ids.add(id))
  }
  return Array.from(ids)
}

const splitRoleIds = (allIds) => {
  const builtinSet = new Set(builtinRoleOptions.value.map((o) => o.value))
  const strId = (v) => v == null ? '' : String(v)
  const builtinMapById = new Map(builtinRoleOptions.value.map((o) => [strId(o.value), o]))
  const customSet = new Set(customRoleOptions.value.map((o) => strId(o.value)))
  const builtin = []
  const custom = []
  ;(allIds || []).forEach((id) => {
    const sid = strId(id)
    if (builtinSet.has(id) || builtinMapById.has(sid)) {
      builtin.push(id)
    } else if (customSet.has(sid)) {
      custom.push(id)
    } else {
      const opt = (props.roles || []).find((r) => strId(r.id) === sid)
      if (opt) {
        if (isBuiltinRole(opt)) builtin.push(id)
        else custom.push(id)
      }
    }
  })
  return { builtin, custom }
}

const selectedBuiltinRoleId = ref()
const selectedCustomRoleIds = ref([])
const selectedRoleIds = computed(() => {
  const arr = []
  if (selectedBuiltinRoleId.value != null && selectedBuiltinRoleId.value !== '') arr.push(selectedBuiltinRoleId.value)
  if (Array.isArray(selectedCustomRoleIds.value)) arr.push(...selectedCustomRoleIds.value)
  return arr
})

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      const initial = computeInitialRoleIds()
      const { builtin, custom } = splitRoleIds(initial)
      selectedBuiltinRoleId.value = builtin.length > 0 ? builtin[0] : undefined
      selectedCustomRoleIds.value = custom.length > 0 ? [...custom] : []
      activeTabKey.value = 'basic'
      collapsedSubs.value = {}
    }
  },
  { immediate: true },
)

const syncRoleIdsToForm = () => {
  const arr = selectedRoleIds.value
  formData.value.role_ids = Array.isArray(arr) ? [...arr] : []
  if (formData.value.role_ids.length > 0 && !formData.value.primary_role_id) {
    formData.value.primary_role_id = formData.value.role_ids[0]
  } else if (formData.value.role_ids.length === 0) {
    formData.value.primary_role_id = undefined
  }
}

const onBuiltinRoleChange = () => {
  syncRoleIdsToForm()
  onSelectedRolesChange()
}

const onCustomRolesChange = () => {
  syncRoleIdsToForm()
  onSelectedRolesChange()
}

watch(
  selectedRoleIds,
  () => {
    syncRoleIdsToForm()
  },
  { deep: true },
)

const allPermsById = computed(() => {
  const map = {}
  ;(props.permissions || []).forEach((p) => {
    const k = p.id != null ? p.id : p.value
    if (k != null) map[k] = p
  })
  return map
})

const ALL_ACTION_KEYS = new Set(Object.keys(ACTION_LABEL_MAP))

const SOURCE_MODULE_MAP = {
  sql_order: ['sql', 'order'],
  datasource: ['datasource'],
  backup: ['backup'],
  monitor: ['monitor'],
  performance: ['performance'],
  audit: ['audit'],
  system: ['system'],
}
const SOURCE_TO_DISPLAY = (() => {
  const m = {}
  Object.entries(SOURCE_MODULE_MAP).forEach(([disp, sources]) => {
    sources.forEach((s) => { m[s] = disp })
  })
  return m
})()

const SORT_SUB_BY_DISPLAY = (dispKey, keys) => {
  if (dispKey === 'sql_order') {
    const preferred = [
      'sql.console', 'sql.favorite', 'sql.history', 'sql.main',
      'order.order', 'order.main',
    ]
    return keys.slice().sort((a, b) => {
      const ia = preferred.indexOf(a)
      const ib = preferred.indexOf(b)
      if (ia === -1 && ib === -1) return a.localeCompare(b)
      if (ia === -1) return 1
      if (ib === -1) return -1
      return ia - ib
    })
  }
  if (dispKey === 'datasource') {
    // 数据源 Tab 内的分组顺序：数据源 → 参数管理 → 多实例对比
    // （不加这条会按字母序排成 compare / instance / param，把「多实例对比」顶到最前）
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
  return keys.slice().sort()
}

const moduleGrouped = computed(() => {
  const result = {}
  DISPLAY_ORDER.forEach((m) => {
    result[m] = { subs: [] }
  })
  const subMap = {}
  ;(props.permissions || []).forEach((perm) => {
    const code = (perm.code || perm.value_code || '').toString()
    const name = perm.name || perm.label || perm.code || ''
    const pid = perm.id != null ? perm.id : perm.value
    const p = { ...perm, code, name, id: pid, value: pid }
    if (!code) return
    const parts = code.split('.')
    const moduleKey = parts[0] || 'other'
    if (!MODULE_LABEL_MAP[moduleKey]) return

    let subKey
    const lastIsAction = ALL_ACTION_KEYS.has(parts[parts.length - 1])
    if (parts.length >= 3 && lastIsAction) {
      subKey = parts.slice(0, parts.length - 1).join('.')
    } else if (parts.length === 2 && lastIsAction) {
      subKey = `${moduleKey}.main`
    } else if (parts.length === 2) {
      subKey = code
    } else {
      subKey = `${moduleKey}.main`
    }

    const dispKey = SOURCE_TO_DISPLAY[moduleKey] || moduleKey
    if (!subMap[dispKey]) subMap[dispKey] = {}
    if (!subMap[dispKey][subKey]) subMap[dispKey][subKey] = []
    subMap[dispKey][subKey].push(p)
  })
  DISPLAY_ORDER.forEach((dispKey) => {
    const subs = subMap[dispKey] || {}
    const subKeys = SORT_SUB_BY_DISPLAY(dispKey, Object.keys(subs))
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
          subLabel = parts.length >= 2 ? parts.slice(1).map((s) => SUB_LABEL_MAP?.[parts[0] + '.' + s] || s).join(' / ') : MODULE_LABEL_MAP[moduleKey]
        }
      }
      result[dispKey].subs.push({
        key: subKey,
        label: subLabel,
        items: subs[subKey].sort((a, b) => (a.code || '').localeCompare(b.code || '')),
      })
    })
  })
  return result
})

const permDisplayMap = computed(() => {
  const map = {}
  ;(props.permissions || []).forEach((perm) => {
    const pid = perm.id != null ? perm.id : perm.value
    const code = perm.code || perm.value_code || ''
    const rawName = perm.name || perm.label || perm.code || ''
    const parts = code.split('.')
    const action = parts[parts.length - 1] || ''
    const moduleKey = parts[0] || ''
    const actionLabel = ACTION_LABEL_MAP[action] || action
    const moduleLabel = MODULE_LABEL_MAP[moduleKey] || moduleKey
    let cnName = rawName
    if (!cnName || !/[\u4e00-\u9fa5]/.test(cnName)) {
      cnName = actionLabel && moduleLabel && moduleLabel !== actionLabel ? `${moduleLabel}${actionLabel}` : (rawName || actionLabel || code)
    }
    map[pid] = { name: cnName, code, module: moduleKey, byRole: '' }
  })
  return map
})

const builtinRolePermissionIdsByCode = computed(() => {
  const result = { member: new Set(), admin: new Set(), global_readonly: new Set() }
  ;(props.roles || []).forEach((role) => {
    if (!isBuiltinRole(role)) return
    if (role.code === 'member') {
      ;(role.permission_ids || []).forEach((id) => result.member.add(id))
    } else if (role.code === 'admin') {
      ;(role.permission_ids || []).forEach((id) => result.admin.add(id))
    } else if (role.code === 'global_readonly') {
      ;(role.permission_ids || []).forEach((id) => result.global_readonly.add(id))
    }
  })
  return result
})

const currentBuiltinCodes = computed(() => {
  const set = new Set()
  ;(selectedRoleIds.value || []).forEach((rid) => {
    const opt = allRoleOptions.value.find((r) => r.value === rid)
    if (opt?.builtin && BUILTIN_ROLE_CODES.includes(opt.code)) set.add(opt.code)
  })
  return set
})

const hasFullAccessBuiltin = computed(() => {
  const codes = currentBuiltinCodes.value
  return codes.has('admin') || codes.has('super_admin')
})

const permProvidedByRoleMap = computed(() => {
  const map = {}
  const allPerms = props.permissions || []
  const idSet = new Set()
  allPerms.forEach((p) => {
    const k = p.id != null ? p.id : p.value
    if (k != null) idSet.add(k)
  })
  ;(selectedRoleIds.value || []).forEach((rid) => {
    const opt = allRoleOptions.value.find((r) => r.value === rid)
    if (!opt) return
    let pids = opt.permission_ids || []
    if (pids.length === 0 && opt.code === 'member') pids = Array.from(builtinRolePermissionIdsByCode.value.member)
    if (pids.length === 0 && opt.code === 'admin') pids = Array.from(builtinRolePermissionIdsByCode.value.admin)
    if (pids.length === 0 && opt.code === 'global_readonly') pids = Array.from(builtinRolePermissionIdsByCode.value.global_readonly)
    pids.forEach((pid) => {
      if (!idSet.has(pid)) return
      if (!map[pid]) map[pid] = []
      map[pid].push(opt)
    })
  })
  return map
})

const permDisabledMap = computed(() => {
  const map = {}
  const allPerms = props.permissions || []
  const hasAdminBuiltin = currentBuiltinCodes.value.has('admin')
  const hasSuperAdminBuiltin = currentBuiltinCodes.value.has('super_admin')
  // 全量权限内置角色：admin / super_admin，用户自己就有代码级 bypass，不需要再手动钩权限
  const hasFullAccess = hasAdminBuiltin || hasSuperAdminBuiltin
  // hasAnyBuiltin：当前用户是否「至少挂了一个内置角色」—— 包含 member/viewer，
  // 以前只算 admin/global_readonly/super_admin 漏了 member，导致“普通成员”场景被规则 ⑤ 命中，
  // 整个 system 模块全灰，无法手动补系统管理类权限（用户诉求：允许直接授系统管理权限给普通成员）
  const hasAnyBuiltin = currentBuiltinCodes.value.size > 0
  allPerms.forEach((perm) => {
    const pid = perm.id != null ? perm.id : perm.value
    const providers = permProvidedByRoleMap.value[pid] || []
    // 当前这个权限点已经被用户的任一角色（内置/自定义）「自带」了 → 在此处灰掉，
    // 因为要调整这个权限必须去改那个角色本身（避免用户误以为在用户侧钩掉就真的没了，实际继承仍在）
    const hasBuiltinProvider = providers.some((o) => BUILTIN_ROLE_CODES.includes(o.code))
    const alreadyProvidedByRole = providers.length > 0 || hasBuiltinProvider
    let disabled = false
    // ① 系统管理员 / 超级管理员角色：代码级 bypass 所有权限，全部置灰（避免误操作钩掉任何权限）
    if (hasFullAccess) disabled = true
    // ② 任何由「当前角色（内置或自定义）已自带」的权限，用户不能在「用户编辑」页直接改
    //   （要改得去角色管理里改对应角色）—— 保持原有口径不变
    else if (alreadyProvidedByRole) disabled = true
    // ③ 规则 ③④⑤ 旧版针对 system 模块的“不能直连授系统管理权限”的强制限制
    //    —— 用户诉求：希望对普通成员这种 member 内置角色的用户也能直接授系统管理权限，
    //    所以全部移除，由规则 ② 单独控制（系统角色自带的才灰，真正的「未分配权限」都允许直接钩）
    //    （仅保留一个兜底：如果当前用户连任何内置角色都没挂（极端场景），system 模块仍锁，
    //    防止不挂基础 member 就乱授系统管理导致鉴权边界模糊）
    else if (!hasAnyBuiltin) {
      const code = (perm.code || perm.value_code || '').toString()
      const moduleKey = (code.split('.') || [])[0] || ''
      if (moduleKey === 'system') disabled = true
    }
    map[pid] = disabled
  })
  return map
})

const permProvidedNameMap = computed(() => {
  const map = {}
  Object.entries(permProvidedByRoleMap.value).forEach(([pid, opts]) => {
    if (Array.isArray(opts) && opts.length > 0) {
      map[pid] = '来自角色'
    }
  })
  return map
})

const directCheckedPermIds = ref([])

const collapsedSubs = ref({})

const subKey = (mod, subKeyVal) => `${mod}::${subKeyVal}`

const isSubCollapsed = (mod, sk) => !!collapsedSubs.value[subKey(mod, sk)]

const toggleSub = (mod, sk) => {
  collapsedSubs.value = {
    ...collapsedSubs.value,
    [subKey(mod, sk)]: !collapsedSubs.value[subKey(mod, sk)],
  }
}

const initialPermIdsFromProps = () => {
  return Array.isArray(formData.value?.direct_permission_ids)
    ? [...formData.value.direct_permission_ids]
    : Array.isArray(formData.value?.permission_ids)
      ? [...formData.value.permission_ids]
      : []
}

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      directCheckedPermIds.value = initialPermIdsFromProps()
      activeModuleKey.value = 'sql_order'
      if (!isCreate.value && formData.value) {
        formData.value.reset_password = ''
      }
    }
  },
)

const hintResetPwd = computed(() => {
  const v = (formData.value?.reset_password || '').trim()
  if (!v) return '出于安全考虑，系统不存储明文密码，无法直接查看原密码；管理员仅可在此重置为新密码'
  // 不满足强度规则时不返回 help 文案，让校验规则的红色错误提示正常展示
  if (!regPassword.test(v)) return undefined
  return '点击确定后用户需使用新密码登录'
})

const permCheckedMap = computed(() => {
  const map = {}
  const allPerms = props.permissions || []
  // 系统管理员 / 超级管理员：代码级 bypass 所有权限校验，前端强制全部勾选（语义：拥有全部权限）
  if (hasFullAccessBuiltin.value) {
    allPerms.forEach((p) => {
      const pid = p.id != null ? p.id : p.value
      if (pid == null) return
      map[pid] = true
    })
    return map
  }
  const rolePids = new Set()
  Object.keys(permProvidedByRoleMap.value).forEach((pid) => {
    const k = Number.isNaN(Number(pid)) ? pid : Number(pid)
    rolePids.add(k)
    rolePids.add(String(k))
  })
  ;(directCheckedPermIds.value || []).forEach((id) => {
    if (id == null) return
    const k = Number.isNaN(Number(id)) ? id : Number(id)
    rolePids.add(k)
    rolePids.add(String(k))
  })
  allPerms.forEach((p) => {
    const pid = p.id != null ? p.id : p.value
    if (pid == null) return
    map[pid] = rolePids.has(pid) || rolePids.has(String(pid)) || rolePids.has(Number(pid))
  })
  return map
})

const permDisplayWithRole = computed(() => {
  const map = {}
  ;(props.permissions || []).forEach((p) => {
    const pid = p.id != null ? p.id : p.value
    map[pid] = {
      ...permDisplayMap.value[pid],
      byRole: permProvidedNameMap.value[pid] || '',
    }
  })
  return map
})

Object.defineProperty(permDisplayMap, 'value', {
  get: () => permDisplayWithRole.value,
  configurable: true,
})

// -------- 4 个 helper 函数：完全兜底，避免取不到值导致 checkbox 空显示 --------
const getPid = (perm) => perm && (perm.id != null ? perm.id : perm.value)

const permName = (perm) => {
  if (!perm) return ''
  const pid = getPid(perm)
  // 1. perm 对象直接带的字段优先
  const directName = perm.name || perm.label || ''
  if (/[\u4e00-\u9fa5]/.test(directName) && directName.length <= 24) return directName
  // 2. permDisplayMap 里计算的中文名
  const fromMap = pid != null ? (permDisplayMap.value[pid]?.name || permDisplayMap.value[String(pid)]?.name || '') : ''
  if (fromMap) return fromMap
  // 3. 用 code 拼一个保底名（绝对不返回空）
  const code = perm.code || perm.value_code || directName || `#${pid}`
  const parts = String(code).split('.')
  const moduleLabel = MODULE_LABEL_MAP[parts[0]] || parts[0] || ''
  const action = parts[parts.length - 1] || ''
  const actionLabel = ACTION_LABEL_MAP[action] || action
  return (moduleLabel && actionLabel && moduleLabel !== actionLabel) ? `${moduleLabel}${actionLabel}` : (directName || code || `权限-${pid}`)
}

const permByRole = (perm) => {
  if (!perm) return ''
  const pid = getPid(perm)
  if (pid == null) return ''
  return permProvidedNameMap.value[pid] || permProvidedNameMap.value[String(pid)] || permProvidedNameMap.value[Number(pid)] || ''
}

const permChecked = (perm) => {
  if (!perm) return false
  const pid = getPid(perm)
  if (pid == null) return false
  const r = permCheckedMap.value[pid]
  if (r != null) return r
  if (permCheckedMap.value[String(pid)] != null) return permCheckedMap.value[String(pid)]
  if (permCheckedMap.value[Number(pid)] != null) return permCheckedMap.value[Number(pid)]
  return false
}

const permDisabled = (perm) => {
  if (!perm) return true
  const pid = getPid(perm)
  if (pid == null) return true
  const r = permDisabledMap.value[pid]
  if (r != null) return !!r
  if (permDisabledMap.value[String(pid)] != null) return !!permDisabledMap.value[String(pid)]
  if (permDisabledMap.value[Number(pid)] != null) return !!permDisabledMap.value[Number(pid)]
  return false
}

const onSinglePermChange = (pid, checked) => {
  setPermChecked(pid, checked)
  applyPerformanceInclusion(pid, checked)
}

const setPermChecked = (pid, checked) => {
  const next = new Set(directCheckedPermIds.value || [])
  if (checked) next.add(pid)
  else {
    next.delete(pid)
    ;[String(pid), Number(pid)].forEach((v) => next.delete(v))
  }
  const filtered = Array.from(next).filter((id) => {
    const disabled = permDisabledMap.value[id] ?? permDisabledMap.value[String(id)] ?? permDisabledMap.value[Number(id)]
    return !disabled
  })
  directCheckedPermIds.value = filtered
}

// 带「包含关系」的权限项：勾 manage 自动补勾 view；取消 view 时连带取消 manage
// （manage 不能脱离 view 单独存在）。取消 manage 不动 view；勾 view 不自动升到 manage。
// ① 性能分析：管理性能 ⊃ 查看性能分析
// ② 数据源管理（2026-10-25）：参数管理 ⊃ 参数查看，交互口径与性能分析保持一致
const PERFORMANCE_REQUIRE_MAP = {
  'performance.manage': 'performance.view',
  'datasource.param.manage': 'datasource.param.view',
}

const permCodeById = (pid) => {
  const perm = allPermsById.value[pid] ?? allPermsById.value[String(pid)] ?? allPermsById.value[Number(pid)]
  return (perm?.code || perm?.value_code || '').toString()
}

const permIdByCode = (code) => {
  const found = (props.permissions || []).find((p) => (p.code || p.value_code || '').toString() === code)
  return found ? getPid(found) : null
}

const applyPerformanceInclusion = (pid, checked) => {
  const code = permCodeById(pid)
  if (!code) return
  if (checked) {
    // 勾了带依赖的项 → 自动补勾它依赖的项（manage → view）
    const reqCode = PERFORMANCE_REQUIRE_MAP[code]
    if (!reqCode) return
    const reqPid = permIdByCode(reqCode)
    if (reqPid != null && !permChecked({ id: reqPid })) setPermChecked(reqPid, true)
    return
  }
  // 取消了被依赖的项 → 连带取消依赖它的项（view → manage）
  Object.entries(PERFORMANCE_REQUIRE_MAP).forEach(([depCode, reqCode]) => {
    if (reqCode !== code) return
    const depPid = permIdByCode(depCode)
    if (depPid == null) return
    if (permChecked({ id: depPid })) setPermChecked(depPid, false)
  })
}

// -------- 分组整体勾选：作用于分组下所有「可操作」项（角色自带/置灰的项不参与） --------
const subEnabledItems = (sub) => (sub?.items || []).filter((p) => !permDisabled(p))

const subAllChecked = (sub) => {
  const items = subEnabledItems(sub)
  if (items.length === 0) return false
  return items.every((p) => permChecked(p))
}

const subIndeterminate = (sub) => {
  const items = subEnabledItems(sub)
  if (items.length === 0) return false
  const some = items.some((p) => permChecked(p))
  return some && !items.every((p) => permChecked(p))
}

const subAllDisabled = (sub) => subEnabledItems(sub).length === 0

const onSubCheckChange = (sub, checked) => {
  subEnabledItems(sub).forEach((p) => {
    const pid = getPid(p)
    if (pid == null) return
    if (checked !== permChecked(p)) onSinglePermChange(pid, checked)
  })
}

const onSelectedRolesChange = () => {
  const roleProvidedPids = new Set()
  Object.keys(permProvidedByRoleMap.value).forEach((pid) => {
    const opts = permProvidedByRoleMap.value[pid]
    const builtin = opts.some((o) => BUILTIN_ROLE_CODES.includes(o.code)) || opts.length > 0
    if (builtin) {
      const k = Number.isNaN(Number(pid)) ? pid : Number(pid)
      roleProvidedPids.add(k)
      roleProvidedPids.add(String(k))
    }
  })
  directCheckedPermIds.value = (directCheckedPermIds.value || []).filter((id) => {
    const inRole = roleProvidedPids.has(id) || roleProvidedPids.has(String(id)) || roleProvidedPids.has(Number(id))
    if (inRole) return false
    const disabled = permDisabledMap.value[id] ?? permDisabledMap.value[String(id)] ?? permDisabledMap.value[Number(id)]
    if (disabled) return false
    return true
  })
  formData.value.permission_ids = buildFinalPermissionIds()
  formData.value.direct_permission_ids = [...directCheckedPermIds.value]
}

const buildFinalPermissionIds = () => {
  const allSet = new Set()
  ;(directCheckedPermIds.value || []).forEach((id) => allSet.add(id))
  Object.keys(permProvidedByRoleMap.value).forEach((pid) => {
    const k = Number.isNaN(Number(pid)) ? pid : Number(pid)
    allSet.add(k)
  })
  return Array.from(allSet)
}

watch(
  directCheckedPermIds,
  (val) => {
    const leafIds = (val || []).filter((id) => allPermsById.value[id] != null || allPermsById.value[String(id)] != null || allPermsById.value[Number(id)] != null)
    formData.value.permission_ids = buildFinalPermissionIds()
    formData.value.direct_permission_ids = leafIds
  },
  { deep: true },
)

const rules = computed(() => ({
  username: [
    { required: props.canManage, message: '请输入用户名', trigger: 'blur' },
    {
      validator: (_, value) => {
        const v = String(value || '').trim()
        if (!v) return Promise.resolve()
        if (v.length > 30) return Promise.reject(new Error('用户名称不能超过 30 个字符'))
        return Promise.resolve()
      },
      trigger: 'blur',
    },
    { min: 3, message: '用户名长度至少为 3 个字符', trigger: 'blur' },
    { pattern: /^[a-zA-Z0-9_]+$/, message: '用户名仅支持字母、数字和下划线', trigger: 'blur' }
  ],
  email: [
    { required: props.canManage, message: '请输入邮箱', trigger: 'blur' },
    {
      validator: (_, value) => {
        const v = String(value || '').trim()
        if (!v) return Promise.resolve()
        if (!regEmail.test(v)) return Promise.reject(new Error('请输入合法的邮箱地址'))
        return Promise.resolve()
      },
      trigger: 'blur',
    },
  ],
  mobile: [
    {
      validator: (_, value) => {
        const v = String(value || '').trim()
        if (!v) return Promise.resolve()
        if (!regPhone.test(v)) return Promise.reject(new Error('请输入合法的手机号码'))
        return Promise.resolve()
      },
      trigger: 'blur',
    },
  ],
  role_ids: [
    {
      validator: async () => {
        if (!props.canManage) return Promise.resolve()
        if (Array.isArray(selectedRoleIds.value) && selectedRoleIds.value.length > 0) {
          return Promise.resolve()
        }
        return Promise.reject(new Error('请至少选择一个角色'))
      },
      trigger: 'change',
    },
  ],
  password: [
    {
      validator: (_, value) => {
        if (!props.canManage) return Promise.resolve()
        if (!isCreate.value && !value) return Promise.resolve()
        if (!value) return Promise.reject(new Error('请输入密码'))
        if (value.length < 8) return Promise.reject(new Error('密码长度至少8个字符'))
        if (!regPassword.test(value))
          return Promise.reject(new Error('密码必须包含大小写字母、数字和特殊字符'))
        return Promise.resolve()
      },
      trigger: 'blur',
    },
  ],
  reset_password: [
    {
      // 管理员重置密码：留空表示不修改；填写则强制强度校验，阻止弱密码提交
      validator: (_, value) => {
        const v = String(value || '').trim()
        if (!v) return Promise.resolve()
        if (!regPassword.test(v))
          return Promise.reject(
            new Error('密码至少7个字符，必须包含大写字母、小写字母、数字和特殊字符'),
          )
        return Promise.resolve()
      },
      trigger: 'blur',
    },
  ],
}))

const handleCancel = () => {
  emit('update:open', false)
  formRef.value?.resetFields()
}

const onSubmit = async () => {
  try {
    await formRef.value.validateFields()
    uiState.loading = true
    const payload = {
      ...formData.value,
      role_ids: Array.isArray(selectedRoleIds.value) ? [...selectedRoleIds.value] : [],
      permission_ids: directCheckedPermIds.value || [],
    }
    if (payload.role_ids.length > 0 && !payload.primary_role_id) {
      payload.primary_role_id = payload.role_ids[0]
    }
    // ---------- 密码处理 ----------
    if (isCreate.value) {
      // 新增用户：保留 payload.password（必填，已校验通过）
    } else {
      // 编辑用户：
      // 1. 去掉重置密码临时字段（后端表单字段叫 password）
      const resetPwd = String(payload.reset_password || '').trim()
      delete payload.reset_password
      // 2. 没有填新密码 → 不传 password 字段，后端不更新密码哈希
      if (!resetPwd) {
        delete payload.password
      } else {
        // 3. 填了新密码 → 校验长度后传给后端（后端再校验一次）
        payload.password = resetPwd
      }
    }
    emit('submit', payload)
  } catch {
    // ignore
  } finally {
    uiState.loading = false
  }
}
</script>

<style scoped>
:deep(.ant-modal-body) {
  height: calc(100vh - 240px) !important;
  max-height: calc(100vh - 240px) !important;
  min-height: 360px !important;
  overflow: hidden !important;
  display: flex !important;
  flex-direction: column !important;
}

.modal-form {
  padding-top: 8px;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.top-tabs {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

:deep(.top-tabs .ant-tabs-nav) {
  flex-shrink: 0;
}

:deep(.top-tabs .ant-tabs-content-holder),
:deep(.top-tabs .ant-tabs-content) {
  flex: 1 1 0% !important;
  min-height: 0 !important;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden !important;
}

:deep(.top-tabs .ant-tabs-tabpane) {
  flex: 1 1 0% !important;
  min-height: 0 !important;
  display: none !important;
  flex-direction: column !important;
  overflow: hidden !important;
}

:deep(.top-tabs .ant-tabs-tabpane.ant-tabs-tabpane-active) {
  display: flex !important;
}

.permission-tabs-wrap {
  display: flex;
  flex-direction: column;
  gap: 12px;
  flex: 1;
  min-height: 0;
}

.role-permission-summary {
  border: 1px solid #e6f4ff;
  background: #fafcff;
  border-radius: 8px;
  padding: 12px 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.summary-row {
  display: flex;
  align-items: flex-start;
  font-size: 13px;
}

.summary-label {
  flex-shrink: 0;
  color: #8c8c8c;
  width: 130px;
  padding-top: 2px;
}

.summary-content {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}

.role-summary-tag {
  padding: 2px 10px;
  border-radius: 4px;
  font-weight: 500;
}

.summary-extra .count {
  font-weight: 600;
  color: #1677ff;
  margin-right: 6px;
}

.summary-extra .names {
  color: #262626;
}

.summary-extra .more {
  color: #8c8c8c;
  margin-left: 2px;
}

.summary-extra .empty {
  color: #bfbfbf;
  font-style: italic;
}

.sub-meta {
  font-size: 12px;
  color: #8c8c8c;
  font-weight: 400;
  margin-left: 8px;
}

.sub-title {
  display: flex;
  align-items: baseline;
}

.permission-hint {
  padding: 8px 12px;
  background: #e6f4ff;
  border: 1px solid #91caff;
  color: #0958d9;
  border-radius: 6px;
  font-size: 12px;
  display: flex;
  align-items: center;
}

.permission-hint.hint-success {
  background: #f6ffed;
  border-color: #b7eb8f;
  color: #389e0d;
}

.permission-hint.hint-warn {
  background: #fffbe6;
  border-color: #ffe58f;
  color: #d48806;
}

.module-tabs {
  border: 1px solid #f0f0f0;
  border-radius: 8px;
  background: #fff;
  padding: 0 0 16px 0;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

:deep(.module-tabs .ant-tabs-nav) {
  margin-bottom: 8px;
  position: sticky;
  top: 0;
  z-index: 5;
  background: #fff;
  flex-shrink: 0;
  width: 100%;
}

:deep(.module-tabs .ant-tabs-nav-wrap) {
  width: 100%;
}

:deep(.module-tabs .ant-tabs-nav-list) {
  width: 100%;
  display: flex !important;
  flex-wrap: nowrap !important;
  justify-content: space-between;
  gap: 0;
}

:deep(.module-tabs .ant-tabs-tab) {
  flex: 1 1 0%;
  min-width: 0;
  max-width: none;
  width: 0;
  text-align: center;
  justify-content: center;
  padding: 8px 0;
  margin: 0 !important;
  font-size: 13px;
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  box-sizing: border-box;
  border: none;
}

:deep(.module-tabs .ant-tabs-content-holder),
:deep(.module-tabs .ant-tabs-content) {
  flex: 1 1 0% !important;
  min-height: 0 !important;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden !important;
}

:deep(.module-tabs .ant-tabs-tabpane) {
  flex: 1 1 0% !important;
  min-height: 0 !important;
  display: none !important;
  flex-direction: column !important;
  overflow: hidden !important;
}

:deep(.module-tabs .ant-tabs-tabpane.ant-tabs-tabpane-active) {
  display: flex !important;
}

.perm-checklist-wrap {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 4px 4px 4px 0;
}

.perm-checklist {
  padding: 4px 4px 8px 4px;
}

.sub-title {
  font-size: 13px;
  font-weight: 600;
  color: #262626;
  margin: 12px 4px 8px 4px;
  line-height: 1;
  cursor: pointer;
  user-select: none;
  display: flex;
  align-items: center;
  gap: 6px;
}

.sub-title.collapsed .sub-label,
.sub-title.collapsed .sub-meta {
  opacity: 0.7;
}

.sub-caret {
  font-size: 12px;
  color: #8c8c8c;
  width: 14px;
  text-align: center;
  flex-shrink: 0;
}

.sub-check {
  flex-shrink: 0;
  margin-right: 2px;
}

.sub-check :deep(.ant-checkbox-inner) {
  width: 14px;
  height: 14px;
}

.sub-title:first-of-type {
  margin-top: 4px;
}

.perm-list {
  display: flex;
  flex-wrap: wrap;
  padding: 4px 16px 12px 16px;
}

.perm-item-wrapper {
  width: 50%;
  padding-right: 24px;
  margin-bottom: 10px;
  box-sizing: border-box;
  display: block;
}

.perm-item-wrapper :deep(.ant-checkbox-wrapper) {
  min-width: 0;
  width: 100%;
}

.perm-cn {
  font-size: 13px;
  color: #262626;
  margin-right: 4px;
  word-break: break-all;
}

.perm-by-role {
  font-size: 12px;
  color: #8c8c8c;
}

.sub-title {
  padding-left: 16px;
  padding-right: 16px;
  margin-left: 0;
  margin-right: 0;
}

.perm-checklist {
  padding: 4px 0 8px 0;
}
</style>

<style>
/* 响应式尺寸：比默认小一圈，靠上定位，顶部/底部各 32px 留白 */
.user-form-modal-wrap .ant-modal {
  position: absolute !important;
  left: 50% !important;
  top: 32px !important;
  transform: translateX(-50%) !important;
  margin: 0 !important;
  padding: 0 !important;
  padding-bottom: 0 !important;
  width: min(820px, 78vw) !important;
  max-width: min(820px, 78vw) !important;
  min-width: 380px !important;
  max-height: calc(100vh - 64px) !important;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden !important;
  box-sizing: border-box !important;
}

.user-form-modal-wrap .ant-modal-content {
  max-height: calc(100vh - 64px) !important;
  height: auto;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden !important;
  box-sizing: border-box !important;
  flex: 1 1 auto !important;
  min-height: 0 !important;
}

/* 头部样式：禁用 pointer-events（彻底阻断拖拽启动），标题/关闭按钮单独恢复 */
.user-form-modal-wrap .ant-modal-header {
  cursor: default !important;
  user-select: none !important;
  -webkit-user-select: none !important;
  touch-action: none !important;
  pointer-events: none !important;
  flex-shrink: 0 !important;
}

.user-form-modal-wrap .ant-modal-header .ant-modal-title {
  cursor: default !important;
  user-select: text !important;
  -webkit-user-select: text !important;
  pointer-events: auto !important;
}

.user-form-modal-wrap .ant-modal-close {
  cursor: pointer !important;
  pointer-events: auto !important;
}

/* 内容区高度由 bodyStyle + flex 组合控制，整体受 max-height: 100vh-64px 约束 */
.user-form-modal-wrap .ant-modal-body {
  display: flex !important;
  flex-direction: column !important;
  flex: 1 1 auto !important;
  min-height: 0 !important;
  height: auto !important;
  overflow: hidden !important;
  box-sizing: border-box !important;
}

.user-form-modal-wrap .top-tabs {
  flex: 1 1 0% !important;
  display: flex !important;
  flex-direction: column !important;
  min-height: 0 !important;
}

.user-form-modal-wrap .top-tabs > .ant-tabs-nav {
  flex-shrink: 0 !important;
}

.user-form-modal-wrap .top-tabs > .ant-tabs-content-holder,
.user-form-modal-wrap .top-tabs > .ant-tabs-content-holder > .ant-tabs-content {
  flex: 1 1 0% !important;
  min-height: 0 !important;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden !important;
}

.user-form-modal-wrap .top-tabs > .ant-tabs-content-holder > .ant-tabs-content > .ant-tabs-tabpane {
  flex: 1 1 0% !important;
  min-height: 0 !important;
  display: none !important;
  flex-direction: column !important;
  overflow: hidden !important;
}

.user-form-modal-wrap .top-tabs > .ant-tabs-content-holder > .ant-tabs-content > .ant-tabs-tabpane.ant-tabs-tabpane-active {
  display: flex !important;
}

.user-form-modal-wrap .module-tabs {
  flex: 1 1 0% !important;
  min-height: 0 !important;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden !important;
}

.user-form-modal-wrap .module-tabs > .ant-tabs-nav {
  flex-shrink: 0 !important;
  width: 100% !important;
  margin-bottom: 8px !important;
  background: #fff !important;
}

.user-form-modal-wrap .module-tabs > .ant-tabs-nav .ant-tabs-nav-wrap {
  width: 100% !important;
}

.user-form-modal-wrap .module-tabs > .ant-tabs-nav .ant-tabs-nav-list {
  width: 100% !important;
  display: flex !important;
  flex-wrap: nowrap !important;
  justify-content: space-between !important;
}

.user-form-modal-wrap .module-tabs > .ant-tabs-nav .ant-tabs-tab {
  flex: 1 1 0% !important;
  min-width: 0 !important;
  max-width: none !important;
  width: 0 !important;
  text-align: center !important;
  justify-content: center !important;
  padding: 8px 0 !important;
  margin: 0 !important;
  font-size: 13px !important;
  font-weight: 500 !important;
  white-space: nowrap !important;
  overflow: hidden !important;
  text-overflow: ellipsis !important;
  box-sizing: border-box !important;
  border: none !important;
}

.user-form-modal-wrap .module-tabs > .ant-tabs-ink-bar {
  transition: width 0.3s ease, left 0.3s ease;
}

.user-form-modal-wrap .module-tabs > .ant-tabs-content-holder,
.user-form-modal-wrap .module-tabs > .ant-tabs-content-holder > .ant-tabs-content {
  flex: 1 1 0% !important;
  min-height: 0 !important;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden !important;
}

.user-form-modal-wrap .module-tabs > .ant-tabs-content-holder > .ant-tabs-content > .ant-tabs-tabpane {
  flex: 1 1 0% !important;
  min-height: 0 !important;
  display: none !important;
  flex-direction: column !important;
  overflow: hidden !important;
}

.user-form-modal-wrap .module-tabs > .ant-tabs-content-holder > .ant-tabs-content > .ant-tabs-tabpane.ant-tabs-tabpane-active {
  display: flex !important;
}

@media (max-width: 768px) {
  .user-form-modal-wrap .ant-modal {
    min-width: 0 !important;
    width: calc(100vw - 16px) !important;
    max-width: calc(100vw - 16px) !important;
  }

  /* 权限项两列 → 单列 */
  .perm-item-wrapper {
    width: 100%;
    padding-right: 0;
  }
}
</style>
