<template>
  <a-modal :open="props.open" :title="props.title" width="940px" wrapClassName="das-form-modal-wrap" @cancel="handleCancel">
    <template #footer>
      <a-button @click="handleCancel">取消</a-button>
      <a-button type="primary" :loading="uiState.loading" :disabled="!props.canManage" @click="onSubmit">确定</a-button>
    </template>

    <a-form :label-col="{ span: 4 }" :wrapper-col="{ span: 20 }" :disabled="!props.canManage" class="modal-form">
      <a-form-item label="对象类型" required>
        <a-select
          v-model:value="formState.principal_type"
          :options="principalTypeOptions"
          :disabled="props.principalDisabled"
          @change="handlePrincipalTypeChange"
        />
      </a-form-item>

      <a-form-item :label="principalLabel" required>
        <a-select
          v-model:value="formState.principal_id"
          :options="principalOptions"
          option-filter-prop="label"
          show-search
          allow-clear
          :disabled="props.principalDisabled"
          placeholder="请选择授权对象"
        />
      </a-form-item>

      <a-form-item label="授权范围" required>
        <div class="grant-scope-boxes">
          <div
            class="grant-scope-box grant-scope-box--compact"
            :class="{ active: formState.grant_scope === 'instance_readonly' }"
            @click="handleGrantScopeChange('instance_readonly')"
          >
            <div class="grant-scope-box__name">实例</div>
          </div>
          <div
            class="grant-scope-box grant-scope-box--compact"
            :class="{ active: formState.grant_scope === 'resource_tables_readonly' }"
            @click="handleGrantScopeChange('resource_tables_readonly')"
          >
            <div class="grant-scope-box__name">库 / Schema / 表</div>
          </div>
        </div>
      </a-form-item>

      <a-form-item label="数据库类型" required>
        <a-select
          v-model:value="formState.db_type"
          :options="dbTypeOptions"
          @change="handleDbTypeChange"
          allow-clear
          placeholder="请选择数据库类型"
        />
      </a-form-item>

      <a-form-item label="环境" required>
        <a-select
          v-model:value="formState.environment"
          :options="props.environments"
          :field-names="{ label: 'name', value: 'id' }"
          @change="changeEnv"
          allow-clear
          placeholder="请选择环境"
        />
      </a-form-item>

      <a-form-item v-if="isResourceGrant" label="实例" required>
        <a-select
          v-model:value="formState.instance_id"
          :options="uiData.instances"
          :field-names="{ label: 'remark', value: 'instance_id' }"
          @change="changeInstance"
          allow-clear
          placeholder="请选择实例"
        >
          <template #option="{ remark, hostname, port }">
            <div class="instance-option">
              <div class="instance-option__title">{{ remark || `${hostname}:${port}` }}</div>
              <div class="instance-option__sub">{{ hostname }}:{{ port }}</div>
            </div>
          </template>
        </a-select>
      </a-form-item>

      <a-form-item v-else label="实例" required>
        <a-select
          v-model:value="formState.instance_ids"
          mode="multiple"
          :options="instanceOptionsWithHint"
          :field-names="{ label: 'remark', value: 'instance_id' }"
          allow-clear
          placeholder="可一次选择多个实例（已授权标签会合并展示直授/角色继承来源，并自动跳过重复授权）"
          max-tag-count="responsive"
        >
          <template #option="{ remark, hostname, port, __grantTag }">
            <div class="instance-option">
              <div class="instance-option__left">
                <div class="instance-option__title">{{ remark || `${hostname}:${port}` }}</div>
                <div class="instance-option__sub">{{ hostname }}:{{ port }}</div>
              </div>
              <div class="instance-option__right">
                <a-tag v-if="__grantTag" color="green" :bordered="false" size="small">
                  {{ __grantTag }}
                </a-tag>
              </div>
            </div>
          </template>
        </a-select>
      </a-form-item>

      <template v-if="isResourceGrant">
        <a-alert
          v-if="resourceLevelWarnMessage"
          type="warning"
          show-icon
          class="resource-hint-alert"
          :message="resourceLevelWarnMessage"
        />
        <a-form-item v-if="needsDatabaseLayer" label="Database" required>
          <a-select
            v-model:value="formState.selected_databases"
            mode="multiple"
            :options="uiData.primaryResources"
            option-filter-prop="label"
            show-search
            allow-clear
            placeholder="可选择多个 Database"
            @change="changeDatabases"
          >
            <template #option="{ label, schema, resource_type }">
              <div class="resource-option-row">
                <span class="resource-option-row__label">{{ label }}</span>
                <a-tag v-if="isResourceAlreadyGranted(resource_type, schema)" color="green">已授权</a-tag>
              </div>
            </template>
          </a-select>
        </a-form-item>

        <a-form-item :label="resourceSelectLabel">
          <div class="resource-picker">
            <a-select
              v-model:value="formState.selected_resources"
              mode="multiple"
              :options="resourceOptions"
              option-filter-prop="label"
              show-search
              allow-clear
              :placeholder="resourcePlaceholder"
              class="resource-picker__select"
            >
            <template #option="{ label, schema, resource_type }">
              <div class="resource-option-row">
                <span class="resource-option-row__label">{{ label }}</span>
                <a-tag v-if="isResourceAlreadyGranted(resource_type, schema)" color="green">已授权</a-tag>
              </div>
            </template>
          </a-select>
            <a-button type="primary" @click="appendRuleItems">加入授权清单</a-button>
          </div>
          <div class="inline-tip inline-tip--block">
            先把要授权的{{ resourceSelectLabel }}加入清单，再分别配置每个资源的授权方式。绿色标记表示该授权主体已拥有相同或更上层的权限。
          </div>
        </a-form-item>

        <a-form-item label="授权清单" required>
          <div v-if="!formState.rule_items.length" class="resource-empty">
            请先选择{{ resourceSelectLabel }}，并点击“加入授权清单”
          </div>
          <div v-else class="resource-rule-list">
            <div
              v-for="item in formState.rule_items"
              :key="item.key"
              class="resource-rule-card"
              :class="{ 'resource-rule-card--skip': item.__grant_status === 'skip' }"
            >
              <div class="resource-rule-card__head">
                <div>
                  <div class="resource-rule-card__title">
                    {{ item.label }}
                    <a-tag
                      v-if="item.__grant_status === 'skip'"
                      color="default"
                      style="margin-left: 8px"
                    >将跳过（已存在）</a-tag>
                    <a-tag v-else color="green" style="margin-left: 8px">新增</a-tag>
                  </div>
                  <div class="resource-rule-card__sub">
                    {{ item.resource_type === 'schema' ? 'Schema' : 'Database' }}
                    <span v-if="item.parent_path && item.resource_type !== 'schema'"> · {{ item.parent_path }}</span>
                    <span v-if="item.__existing_scope" class="grant-existing-scope"> · {{ item.__existing_scope }}</span>
                  </div>
                </div>
                <a-button type="link" danger @click="removeRuleItem(item.key)">移除</a-button>
              </div>

              <div class="resource-rule-card__body">
                <div class="resource-rule-row">
                  <div class="resource-rule-row__label">授权方式</div>
                  <a-radio-group v-model:value="item.mode" @change="() => handleRuleModeChange(item)">
                    <a-radio-button value="all">全部表只读</a-radio-button>
                    <a-radio-button value="exact">指定表只读</a-radio-button>
                    <a-radio-button value="pattern">通配表只读</a-radio-button>
                    <a-radio-button value="exclude_exact">排除指定表</a-radio-button>
                    <a-radio-button value="exclude_pattern">排除通配表</a-radio-button>
                  </a-radio-group>
                </div>

                <div v-if="isExactMode(item.mode)" class="resource-rule-row">
                  <div class="resource-rule-row__label">选择表</div>
                  <a-select
                    v-model:value="item.selected_table_keys"
                    mode="multiple"
                    :options="getTableOptions(item)"
                    option-filter-prop="label"
                    show-search
                    allow-clear
                    max-tag-count="responsive"
                    placeholder="可选择多张表"
                  />
                </div>

                <div v-if="isPatternMode(item.mode)" class="resource-rule-row">
                  <div class="resource-rule-row__label">通配规则</div>
                  <a-textarea
                    v-model:value="item.table_patterns"
                    :rows="2"
                    placeholder="支持多个规则，使用逗号或换行分隔，例如：test_*，tmp_*"
                  />
                </div>

                <div class="grant-summary grant-summary--compact">
                  <div class="grant-summary__title">{{ getRuleSummary(item).title }}</div>
                  <div class="grant-summary__desc">{{ getRuleSummary(item).description }}</div>
                </div>
              </div>
            </div>
          </div>
        </a-form-item>
      </template>
    </a-form>
  </a-modal>
</template>

<script setup>
import { getDasDataGrantsApi, getDasInstancesListApi, getDasSchemasListApi, getDasTablesListApi } from '@/api/admin'
import { getPureSchemaDisplayName } from '@/utils/schemaDisplay'
import { message } from 'ant-design-vue'
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { normalizeDataGrantRows } from './dataGrantAdapter'

const emit = defineEmits(['update:open', 'submit'])
const props = defineProps({
  open: Boolean,
  title: String,
  canManage: {
    type: Boolean,
    default: true,
  },
  users: Array,
  roles: Array,
  environments: Array,
  initialPrincipalType: {
    type: String,
    default: '',
  },
  initialPrincipalId: {
    type: [String, Number],
    default: undefined,
  },
  principalDisabled: {
    type: Boolean,
    default: false,
  },
})

// 与实例管理的 db_type 枚举对齐（backend das/forms/admin.go oneof）。
// TiDB / Doris / OceanBase(MySQL 模式) 走 MySQL 家族的「库」维度；
// Oracle 与 OceanBase(Oracle 模式) 走「Schema」维度，资源类型由后端按 effective family 返回。
const dbTypeOptions = [
  { value: 'MySQL', label: 'MySQL' },
  { value: 'TiDB', label: 'TiDB' },
  { value: 'Doris', label: 'Doris' },
  { value: 'OceanBase', label: 'OceanBase' },
  { value: 'PostgreSQL', label: 'PostgreSQL' },
  // 金仓：PG 协议，库/schema 两级与 PG 同口径（后端 dbfamily 已归到 postgres 家族）
  { value: 'Kingbase', label: 'Kingbase' },
  { value: 'Oracle', label: 'Oracle' },
]

const principalTypeOptions = [
  { value: 'user', label: '用户' },
  { value: 'role', label: '角色' },
]

const createDefaultFormState = () => ({
  principal_type: 'user',
  principal_id: undefined,
  grant_scope: 'instance_readonly',
  db_type: '',
  environment: '',
  instance_id: '',
  instance_ids: [],
  selected_databases: [],
  selected_resources: [],
  rule_items: [],
})

const formState = reactive(createDefaultFormState())

const uiState = reactive({
  loading: false,
  fetchingGrants: false,
  selectedInstanceHint: '',
})

const uiData = reactive({
  instances: [],
  primaryResources: [],
  secondaryResources: [],
  tableCatalogCache: {},
  primaryResourceType: 'database',
})

const currentPrincipalKey = ref('')
const currentGrantRaw = ref([])

const GRANT_SOURCE_DIRECT = 'USER_DIRECT'
const ROLE_SOURCE_PREFIX = 'ROLE_INHERIT'
const roleSourceLabel = (name) => `${ROLE_SOURCE_PREFIX}:${name || ''}`
const roleNameFromSource = (src) => {
  if (typeof src !== 'string' || !src.startsWith(ROLE_SOURCE_PREFIX + ':')) return ''
  return src.slice((ROLE_SOURCE_PREFIX + ':').length) || ''
}
const getGrantSourceMeta = (sourceSet) => {
  const arr = Array.isArray(sourceSet) ? sourceSet : Array.from(sourceSet || [])
  const direct = arr.includes(GRANT_SOURCE_DIRECT)
  const roleNames = Array.from(new Set(arr.map(roleNameFromSource).filter(Boolean)))
  return {
    direct,
    roleNames,
  }
}
const buildGrantHintTag = (sourceSet) => {
  const { direct, roleNames } = getGrantSourceMeta(sourceSet)
  if (direct && roleNames.length) return '已授权（直授 + 角色）'
  if (direct) return '本主体已授权'
  if (roleNames.length) return '角色继承已授权'
  return ''
}
const buildGrantSourceSummaryText = (sourceSet) => {
  const { direct, roleNames } = getGrantSourceMeta(sourceSet)
  const parts = []
  if (direct) parts.push('本主体直连授权')
  if (roleNames.length) parts.push(`通过角色继承（${roleNames.join('、')}）`)
  return parts.join('；')
}
const mergeGrantRowsByResource = (rows) => {
  const merged = new Map()
  ;(Array.isArray(rows) ? rows : []).forEach((row) => {
    if (!row) return
    const iid = String(row.instance_id || '')
    const rt = String(row.resource_type || '')
    const schema = String(row.schema || '')
    if (!iid || !rt) return
    const key = resourceKey(iid, rt, schema)
    const source = String(row.__grant_source || GRANT_SOURCE_DIRECT)
    if (!merged.has(key)) {
      merged.set(key, {
        ...(row || {}),
        __grant_sources: [source],
      })
      return
    }
    const current = merged.get(key)
    const sourceSet = new Set(Array.isArray(current.__grant_sources) ? current.__grant_sources : [])
    sourceSet.add(source)
    // 同一资源若既有直授又有角色继承，优先保留直授记录的主体快照与时间字段
    if (source === GRANT_SOURCE_DIRECT) {
      merged.set(key, {
        ...(row || {}),
        __grant_sources: Array.from(sourceSet),
      })
      return
    }
    current.__grant_sources = Array.from(sourceSet)
  })
  return Array.from(merged.values())
}

// 当前环境 + DB 下，wwc 真正已经授予过「实例级完整授权」的「环境内候选」
// Map<instance_id,Set<source>>，只包含 currentGrantMap.instanceAll 和当前 uiData.instances 的交集
const currentEnvInstanceGrantSources = computed(() => {
  const out = new Map()
  const envInstanceIds = new Set(
    (uiData.instances || [])
      .map((i) => String(i?.instance_id || ''))
      .filter(Boolean),
  )
  for (const iid of envInstanceIds) {
    if (currentGrantMap.value.instanceAll.has(iid)) {
      const src = currentGrantMap.value.instanceAllSources.get(iid)
      if (src) out.set(iid, new Set(src))
      else out.set(iid, new Set())
    }
  }
  return out
})
// 在「实例」多选框里按“最终生效权限”显示一条合并标签，
// 同一实例如果同时来自直授和角色继承，也只展示一个已授权提示。
const instanceOptionsWithHint = computed(() => (uiData.instances || []).map((item) => {
  const iid = String(item?.instance_id || '')
  const srcSet = currentEnvInstanceGrantSources.value.get(iid)
  const tag = buildGrantHintTag(srcSet)
  return {
    ...item,
    __grantTag: tag,
  }
}))

const resourceKey = (instanceID, resourceType, schema) =>
  `${String(instanceID || '')}|${String(resourceType || '')}|${String(schema || '')}`

const emptyGrantMap = () => ({
  instanceAll: new Set(),
  instanceAny: new Set(),
  exactByInstance: new Map(),
  // instance_id -> Set<source>: 当前该实例的实例级完整授权来源（USER_DIRECT / ROLE_INHERIT:<name>）
  instanceAllSources: new Map(),
  // resourceKey -> Set<source>: 当前该库/表级授权来源
  exactSources: new Map(),
})

const addSourceEntry = (container, key, source) => {
  if (!key) return
  if (!container.has(key)) container.set(key, new Set())
  container.get(key).add(source)
}

const sourceSummaryForInstance = (iid) => {
  const sources = currentGrantMap.value.instanceAllSources.get(String(iid || ''))
  return buildGrantSourceSummaryText(sources)
}

const buildGrantMap = () => {
  const map = emptyGrantMap()
  const list = Array.isArray(currentGrantRaw.value) ? currentGrantRaw.value : []
  list.forEach((row) => {
    const iid = String(row?.instance_id || '')
    const rt = String(row?.resource_type || '')
    const schema = String(row?.schema || '')
    if (!iid) return
    const sourceList = Array.isArray(row?.__grant_sources) && row.__grant_sources.length
      ? row.__grant_sources.map((item) => String(item || '')).filter(Boolean)
      : [String(row?.__grant_source || GRANT_SOURCE_DIRECT)]
    if (rt === 'instance') {
      map.instanceAll.add(iid)
      sourceList.forEach((source) => addSourceEntry(map.instanceAllSources, iid, source))
    } else {
      map.instanceAny.add(iid)
      const key = resourceKey(iid, rt, schema)
      if (!map.exactByInstance.has(iid)) {
        map.exactByInstance.set(iid, new Set())
      }
      map.exactByInstance.get(iid).add(key)
      sourceList.forEach((source) => addSourceEntry(map.exactSources, key, source))
    }
  })
  return map
}

const currentGrantMap = computed(() => buildGrantMap())

const hasPrincipalContext = computed(() => !!formState.principal_type && !!formState.principal_id)

const resourceLevelWarnMessage = computed(() => {
  if (!isResourceGrant.value || !formState.instance_id) return ''
  if (currentGrantMap.value.instanceAll.has(String(formState.instance_id))) {
    return '该授权主体已拥有实例「完整只读」权限，当前选择的库 / Schema / 表级授权将与实例级权限重叠，属于冗余赋权。建议仅在需要实例级权限回收后保留库表级残留访问时使用。'
  }
  return ''
})

const currentGrantScopeTextForExact = (instanceID, resourceType, schema) => {
  const iid = String(instanceID || '')
  if (currentGrantMap.value.instanceAll.has(iid)) {
    const src = sourceSummaryForInstance(iid)
    return '实例级全量授权' + (src ? `（${src}）` : '')
  }
  const exact = currentGrantMap.value.exactByInstance.get(iid)
  const key = resourceKey(iid, resourceType, schema)
  if (exact && exact.has(key)) {
    const srcSet = currentGrantMap.value.exactSources.get(key)
    const srcArr = srcSet ? Array.from(srcSet) : []
    const direct = srcArr.includes(GRANT_SOURCE_DIRECT)
    const roleNames = srcArr.map(roleNameFromSource).filter(Boolean)
    const parts = []
    if (direct) parts.push('本主体直连授权')
    if (roleNames.length) parts.push(`通过角色继承（${roleNames.join('、')}）`)
    return '已存在库表级授权' + (parts.length ? `（${parts.join('；')}）` : '')
  }
  return ''
}

const isResourceAlreadyGranted = (resourceType, schema) => {
  if (!isResourceGrant.value || !formState.instance_id) return false
  const iid = String(formState.instance_id)
  if (currentGrantMap.value.instanceAll.has(iid)) return true
  const exact = currentGrantMap.value.exactByInstance.get(iid)
  return !!(exact && exact.has(resourceKey(iid, resourceType, schema)))
}

const fetchCurrentGrants = async () => {
  if (!hasPrincipalContext.value) {
    currentPrincipalKey.value = ''
    currentGrantRaw.value = []
    return
  }
  const ptype = String(formState.principal_type)
  const pid = formState.principal_id
  const key = `${ptype}:${String(pid)}`
  if (currentPrincipalKey.value === key) return
  currentPrincipalKey.value = key
  uiState.fetchingGrants = true
  try {
    // 「已授权」提示的数据源与列表页统一为新授权表：旧表看不到 system / self / migrate
    // 来源的授权，用它算提示会把已生效的授权当成「未授权」，诱导管理员重复赋权。
    // （旧表没有 principal_type / principal_id 过滤，这里同样在前端按主体筛选。）
    const strId = (id) => {
      if (id === null || id === undefined || id === '') return ''
      return typeof id === 'number' || typeof id === 'string' ? String(id) : ''
    }
    const pidStr = strId(pid)
    const res = await getDasDataGrantsApi({
      is_page: false,
      status: 'active',
    }).catch(() => null)
    const rawAll = normalizeDataGrantRows(res?.data)
    const merged = []
    const withSource = (rows, source) =>
      Array.isArray(rows)
        ? rows.map((row) => ({ ...(row || {}), __grant_source: source }))
        : []
    const directGrantRows = rawAll.filter((row) => {
      if (!row) return false
      const rowPT = strId(row.principal_type) || String(row.principal_type || '')
      const rowPID = strId(row.principal_id)
      return rowPT === ptype && rowPID === pidStr
    })
    merged.push(...withSource(directGrantRows, GRANT_SOURCE_DIRECT))

    if (ptype === 'user') {
      const user = (props.users || []).find((u) => strId(u?.uid) === pidStr)
      const directRoleIds = Array.isArray(user?.role_ids) ? user.role_ids : []
      const addonRoleIds = Array.isArray(user?.addon_role_ids) ? user.addon_role_ids : []
      const roleIdSet = new Set(
        [...directRoleIds, ...addonRoleIds]
          .map((id) => strId(id))
          .filter(Boolean)
      )
      if (roleIdSet.size > 0) {
        // props.roles 构建 id -> 显示名 的映射
        const roleNameById = new Map()
        for (const role of props.roles || []) {
          const rid = strId(role?.id)
          if (!rid) continue
          roleNameById.set(rid, role?.name || role?.code || rid)
        }
        // 从整表 rawAll 里把属于这些 role_id 的授权行挑出来，按 role_id 分组打 source
        const rowsByRoleId = new Map()
        for (const row of rawAll) {
          if (!row) continue
          const rowPT = strId(row.principal_type) || String(row.principal_type || '')
          const rowPID = strId(row.principal_id)
          if (rowPT !== 'role') continue
          if (!roleIdSet.has(rowPID)) continue
          if (!rowsByRoleId.has(rowPID)) rowsByRoleId.set(rowPID, [])
          rowsByRoleId.get(rowPID).push(row)
        }
        for (const [rid, rows] of rowsByRoleId.entries()) {
          const roleName = roleNameById.get(rid) || rid
          merged.push(...withSource(rows, roleSourceLabel(roleName)))
        }
      }
    }
    currentGrantRaw.value = mergeGrantRowsByResource(merged)
  } catch {
    currentGrantRaw.value = []
  } finally {
    uiState.fetchingGrants = false
  }
}

const isResourceGrant = computed(() => formState.grant_scope === 'resource_tables_readonly')
const isPostgresFamily = computed(() => ['PostgreSQL', 'Kingbase', 'SQLServer'].includes(formState.db_type))
const needsDatabaseLayer = computed(() => isPostgresFamily.value)
const primaryResourceType = computed(() => (formState.db_type === 'Oracle' ? 'schema' : 'database'))
const principalLabel = computed(() => (formState.principal_type === 'role' ? '角色' : '用户'))
const resourceSelectLabel = computed(() => {
  if (needsDatabaseLayer.value) return 'Schema'
  if (formState.db_type === 'Oracle' || uiData.primaryResourceType === 'schema') return 'Schema'
  return '库 / 资源'
})
const resourcePlaceholder = computed(() => (needsDatabaseLayer.value ? '可选择多个 Schema' : `可选择多个${resourceSelectLabel.value}`))
const supportsMixedRuleItems = computed(() => isResourceGrant.value)

const principalOptions = computed(() => {
  if (formState.principal_type === 'role') {
    return (props.roles || []).map((item) => ({
      label: item?.code ? `${item.name} (${item.code})` : item.name,
      value: item.id,
    }))
  }
  return (props.users || []).map((item) => ({
    label: item?.username || item?.nick_name,
    value: item.uid,
  }))
})

const resourceOptions = computed(() => (needsDatabaseLayer.value ? uiData.secondaryResources : uiData.primaryResources))
const selectedResourceEntries = computed(() => {
  const selected = new Set(formState.selected_resources)
  return resourceOptions.value.filter((item) => selected.has(item.value))
})

const createRuleItem = (resource) => {
  const existingScope = currentGrantScopeTextForExact(formState.instance_id, resource.resource_type, resource.schema)
  return {
    key: `${resource.resource_type}:${resource.schema}`,
    schema: resource.schema,
    label: resource.label,
    resource_type: resource.resource_type,
    display_name: resource.display_name,
    parent_path: resource.parent_path,
    mode: 'all',
    selected_table_keys: [],
    table_patterns: '',
    __grant_status: existingScope ? 'skip' : 'new',
    __existing_scope: existingScope || '',
  }
}

const parsePatterns = (value) => String(value || '')
  .split(/[\n,，]+/)
  .map((item) => item.trim())
  .filter(Boolean)

const wildcardToRegExp = (pattern) => {
  const escaped = pattern.replace(/[.+^${}()|[\]\\]/g, '\\$&')
  return new RegExp(`^${escaped.replaceAll('*', '.*').replaceAll('?', '.')}$`, 'i')
}

const isExactMode = (mode) => ['exact', 'exclude_exact'].includes(mode)
const isPatternMode = (mode) => ['pattern', 'exclude_pattern'].includes(mode)

const getTableOptions = (item) => uiData.tableCatalogCache[item.schema] || []

const getMatchedTablesByItem = (item) => {
  if (!supportsMixedRuleItems.value || item.mode === 'all') {
    return []
  }
  if (isExactMode(item.mode)) {
    const selected = new Set(item.selected_table_keys || [])
    return getTableOptions(item).filter((option) => selected.has(option.value))
  }
  const patterns = parsePatterns(item.table_patterns)
  if (!patterns.length) {
    return []
  }
  const regexList = patterns.map(wildcardToRegExp)
  return getTableOptions(item).filter((option) => regexList.some((regex) => regex.test(option.table_name)))
}

const getRuleSummary = (item) => {
  if (item.mode === 'all') {
    return {
      title: '整个资源只读',
      description: '该资源下全部表都将授予只读查询权限。',
    }
  }
  const matched = getMatchedTablesByItem(item)
  if (isExactMode(item.mode)) {
    return item.mode === 'exact'
      ? {
          title: `已选 ${matched.length} 张表`,
          description: '仅这些表具备只读权限。',
        }
      : {
          title: `已排除 ${matched.length} 张表`,
          description: '除这些表之外，其余表均具备只读权限。',
        }
  }
  const patterns = parsePatterns(item.table_patterns)
  return item.mode === 'pattern'
    ? {
        title: `通配规则 ${patterns.length} 条，命中 ${matched.length} 张表`,
        description: '仅命中的表具备只读权限。',
      }
    : {
        title: `排除规则 ${patterns.length} 条，命中 ${matched.length} 张表`,
        description: '除命中的表之外，其余表均具备只读权限。',
      }
}

const resetAllSelections = () => {
  uiData.instances = []
  uiData.primaryResources = []
  uiData.secondaryResources = []
  uiData.tableCatalogCache = {}
  uiData.primaryResourceType = primaryResourceType.value
  formState.instance_id = ''
  formState.instance_ids = []
  formState.selected_databases = []
  formState.selected_resources = []
  formState.rule_items = []
}

const resetResourceSelections = () => {
  uiData.primaryResources = []
  uiData.secondaryResources = []
  uiData.tableCatalogCache = {}
  uiData.primaryResourceType = primaryResourceType.value
  formState.selected_databases = []
  formState.selected_resources = []
  formState.rule_items = []
}

const handlePrincipalTypeChange = () => {
  if (props.principalDisabled) {
    return
  }
  formState.principal_id = undefined
  currentPrincipalKey.value = ''
  currentGrantRaw.value = []
}

const refreshRuleItemGrantStatus = () => {
  formState.rule_items = formState.rule_items.map((item) => {
    const existingScope = currentGrantScopeTextForExact(formState.instance_id, item.resource_type, item.schema)
    return {
      ...item,
      __grant_status: existingScope ? 'skip' : 'new',
      __existing_scope: existingScope || '',
    }
  })
}

const handleGrantScopeChange = (val) => {
  if (typeof val === 'string') {
    formState.grant_scope = val
  } else if (val && typeof val === 'object' && typeof val.target?.value === 'string') {
    formState.grant_scope = val.target.value
  }
  formState.instance_id = ''
  formState.instance_ids = []
  resetResourceSelections()
}

const handleDbTypeChange = () => {
  formState.environment = ''
  formState.instance_id = ''
  resetAllSelections()
}

const normalizeResourceOption = (item) => ({
  value: item.schema,
  label: item.resource_type === 'schema'
    ? getPureSchemaDisplayName({
      dbType: formState.db_type,
      schema: item.schema,
      displayName: item.display_name,
      schemaName: item.schema_name,
      databaseName: item.database_name,
      parentPath: item.parent_path,
    }) || item.display_name || item.schema
    : item.display_name || item.schema,
  schema: item.schema,
  resource_type: item.resource_type,
  display_name: item.resource_type === 'schema'
    ? getPureSchemaDisplayName({
      dbType: formState.db_type,
      schema: item.schema,
      displayName: item.display_name,
      schemaName: item.schema_name,
      databaseName: item.database_name,
      parentPath: item.parent_path,
    }) || item.display_name || item.schema
    : item.display_name || item.schema,
  parent_path: item.parent_path,
})

const buildSchemaOptionLabel = (item) => {
  return getPureSchemaDisplayName({
    dbType: formState.db_type,
    schema: item.schema,
    displayName: item.display_name,
    schemaName: item.schema_name,
    databaseName: item.database_name,
    parentPath: item.parent_path,
  }) || item.display_name || item.schema
}

const changeEnv = async (value) => {
  formState.instance_id = ''
  resetResourceSelections()
  if (!value || !formState.db_type) return
  const res = await getDasInstancesListApi({
    id: value,
    db_type: formState.db_type,
    is_page: false,
  }).catch(() => {})
  uiData.instances = res?.data || []
}

const loadPrimaryResources = async () => {
  if (!formState.instance_id || !isResourceGrant.value) return
  // 资源维度以后端按 effective family 返回为准：首选 db_type 对应的维度，
  // 拉不到时自动尝试另一维度（覆盖 OceanBase Oracle 租户模式等「类型名≠家族」的场景）。
  let resourceType = primaryResourceType.value
  let res = await getDasSchemasListApi({
    instance_id: formState.instance_id,
    is_page: false,
    resource_type: resourceType,
  }).catch(() => {})
  if (!(res?.data || []).length) {
    const fallbackType = resourceType === 'schema' ? 'database' : 'schema'
    const retry = await getDasSchemasListApi({
      instance_id: formState.instance_id,
      is_page: false,
      resource_type: fallbackType,
    }).catch(() => {})
    if ((retry?.data || []).length) {
      res = retry
      resourceType = fallbackType
    }
  }
  uiData.primaryResourceType = resourceType
  uiData.primaryResources = (res?.data || []).map((item) => normalizeResourceOption(item))
}

const changeInstance = async (value) => {
  resetResourceSelections()
  if (!value || !isResourceGrant.value) return
  await loadPrimaryResources()
  nextTick(() => refreshRuleItemGrantStatus())
}

const changeDatabases = async (values) => {
  formState.selected_resources = []
  uiData.secondaryResources = []
  uiData.tableCatalogCache = {}
  formState.rule_items = []
  if (!values?.length) return
  const results = await Promise.all(values.map((database) => getDasSchemasListApi({
    instance_id: formState.instance_id,
    is_page: false,
    parent_path: database,
    resource_type: 'schema',
  }).catch(() => {})))

  const merged = []
  const seen = new Set()
  results.forEach((res) => {
    ;(res?.data || []).forEach((item) => {
      if (seen.has(item.schema)) return
      seen.add(item.schema)
      merged.push({
        value: item.schema,
        label: buildSchemaOptionLabel(item),
        schema: item.schema,
        resource_type: item.resource_type,
        display_name: buildSchemaOptionLabel(item),
        parent_path: item.parent_path,
      })
    })
  })
  uiData.secondaryResources = merged
}

const buildTableOption = (resourceEntry, row) => ({
  value: `${resourceEntry.schema}::${row.table_name}`,
  label: `${resourceEntry.label} / ${row.table_name}`,
  schema: resourceEntry.schema,
  resource_type: resourceEntry.resource_type,
  table_name: row.table_name,
  table_comment: row.table_comment,
})

const loadTablesForResources = async (resourceEntries) => {
  const pendingResources = resourceEntries.filter((item) => !uiData.tableCatalogCache[item.schema])
  if (pendingResources.length) {
    const responses = await Promise.all(pendingResources.map((item) => getDasTablesListApi({
      instance_id: formState.instance_id,
      schema: item.schema,
    }).catch(() => {})))
    pendingResources.forEach((item, index) => {
      uiData.tableCatalogCache[item.schema] = (responses[index]?.data || []).map((row) => buildTableOption(item, row))
    })
  }
}

const appendRuleItems = async () => {
  const resources = selectedResourceEntries.value
  if (!resources.length) {
    message.warning(`请选择${resourceSelectLabel.value}`)
    return
  }
  const existingKeys = new Set(formState.rule_items.map((item) => item.key))
  const incoming = resources.filter((item) => !existingKeys.has(`${item.resource_type}:${item.schema}`))
  let dedupedCount = 0
  let addedCount = 0
  const newItems = incoming.map((item) => {
    const ri = createRuleItem(item)
    if (ri.__grant_status === 'skip') dedupedCount += 1
    else addedCount += 1
    return ri
  })
  formState.rule_items = [...formState.rule_items, ...newItems]
  formState.selected_resources = []
  const total = newItems.length
  if (!total) return
  const dupCount = resources.length - total
  if (dedupedCount || dupCount) {
    message.info(
      `已加入授权清单 ${total} 条：新增 ${addedCount} 条，已存在/将跳过 ${dedupedCount} 条`
        + (dupCount ? `；清单内重复已过滤 ${dupCount} 条` : ''),
    )
  } else {
    message.success(`已加入授权清单 ${addedCount} 条`)
  }
}

const removeRuleItem = (key) => {
  formState.rule_items = formState.rule_items.filter((item) => item.key !== key)
}

const handleRuleModeChange = async (item) => {
  item.selected_table_keys = []
  item.table_patterns = ''
  if (item.mode === 'all') {
    return
  }
  await loadTablesForResources([item])
}

const validateBaseFields = () => {
  if (!formState.principal_type) {
    message.warning('请选择对象类型')
    return false
  }
  if (!formState.principal_id) {
    message.warning(`请选择${principalLabel.value}`)
    return false
  }
  if (!formState.db_type) {
    message.warning('请选择数据库类型')
    return false
  }
  if (!formState.environment) {
    message.warning('请选择环境')
    return false
  }
  if (isResourceGrant.value && !formState.instance_id) {
    message.warning('请选择实例')
    return false
  }
  if (!isResourceGrant.value && !(formState.instance_ids || []).length) {
    message.warning('请至少选择一个实例')
    return false
  }
  return true
}

const buildGrantItems = () => {
  if (!isResourceGrant.value) {
    const result = []
    const selectedRaw = (formState.instance_ids || []).filter((id) => id !== null && id !== undefined && id !== '')
    // 规范化：两边统一字符串化 + trim + 去重
    // （解决 Ant a-select multiple 某些场景下会塞数字 id 的历史数据类型不一致问题）
    const seen = new Set()
    const ids = []
    for (const id of selectedRaw) {
      const s = (typeof id === 'string' ? id : String(id)).trim()
      if (!s) continue
      if (seen.has(s)) continue
      seen.add(s)
      ids.push(s)
    }
    const dupInstanceAll = []
    const dupDirect = []
    const dupRole = new Map() // iid -> Set<roleName>
    const finalIds = []
    for (const s of ids) {
      if (currentGrantMap.value.instanceAll.has(s)) {
        dupInstanceAll.push(s)
        const sources = currentGrantMap.value.instanceAllSources.get(s)
        const arr = sources ? Array.from(sources) : []
        if (arr.includes(GRANT_SOURCE_DIRECT)) dupDirect.push(s)
        const roles = arr.map(roleNameFromSource).filter(Boolean)
        if (roles.length) dupRole.set(s, new Set(roles))
      } else {
        finalIds.push(s)
      }
    }
    if (!finalIds.length) {
      const nameOf = (iid) => {
        const ins = (uiData.instances || []).find((i) => String(i?.instance_id) === String(iid))
        return ins?.remark || ins?.name || ins?.hostname || String(iid)
      }
      const directNames = dupDirect.map(nameOf)
      const roleNamesSummary = Array.from(dupRole.entries())
        .map(([iid, roles]) => `${nameOf(iid)}（${Array.from(roles).join('、')}）`)
      const parts = []
      if (directNames.length) parts.push(`本主体直连已授权 ${directNames.length} 个：${directNames.slice(0, 3).join('、')}${directNames.length > 3 ? '…' : ''}`)
      if (roleNamesSummary.length) parts.push(`通过角色继承已授权 ${roleNamesSummary.length} 个：${roleNamesSummary.slice(0, 3).join('、')}${roleNamesSummary.length > 3 ? '…' : ''}`)
      const hint = parts.length ? `（${parts.join('；')}）` : ''
      message.warning(
        `所选择的实例均已获得「实例级完整授权」，无新增内容可提交${hint}。`
        + (roleNamesSummary.length ? '如希望调整角色继承的授权，请前往「角色管理」修改对应角色的授权或角色成员。' : ''),
      )
      return null
    }
    if (dupInstanceAll.length) {
      const nameOf = (iid) => {
        const ins = (uiData.instances || []).find((i) => String(i?.instance_id) === String(iid))
        return ins?.remark || ins?.name || ins?.hostname || String(iid)
      }
      const dupSamples = dupInstanceAll.slice(0, 3).map(nameOf)
      const dupSampleStr = dupSamples.length ? '（例：' + dupSamples.join('、') + (dupInstanceAll.length > 3 ? '…' : '') + '）' : ''
      message.info(`已自动跳过 ${dupInstanceAll.length} 个已存在实例级授权的实例${dupSampleStr}，仅提交新增 ${finalIds.length} 个`)
    }
    formState.instance_ids = finalIds
    finalIds.forEach((iid) => {
      result.push({
        instance_id: iid,
        schema: '*',
        resource_type: 'instance',
        tables: [],
        rule: 'allow',
      })
    })
    return result
  }

  if (!formState.rule_items.length) {
    message.warning('请先加入至少一个授权资源')
    return null
  }

  const items = []
  let skipped = 0
  for (const item of formState.rule_items) {
    if (item.__grant_status === 'skip') {
      skipped += 1
      continue
    }
    if (item.mode === 'all') {
      items.push({
        schema: item.schema,
        resource_type: item.resource_type,
        tables: [],
        rule: 'allow',
      })
      continue
    }

    if (isExactMode(item.mode) && !(item.selected_table_keys || []).length) {
      message.warning(`请为资源 ${item.label} 选择至少一张表`)
      return null
    }
    if (isPatternMode(item.mode) && !parsePatterns(item.table_patterns).length) {
      message.warning(`请为资源 ${item.label} 填写至少一条通配规则`)
      return null
    }

    const matched = getMatchedTablesByItem(item)
    if (!matched.length) {
      message.warning(`资源 ${item.label} 当前没有匹配到任何表`)
      return null
    }

    items.push({
      schema: item.schema,
      resource_type: item.resource_type,
      tables: matched.map((row) => row.table_name),
      rule: ['exclude_exact', 'exclude_pattern'].includes(item.mode) ? 'deny' : 'allow',
    })
  }

  if (!items.length) {
    message.warning('当前清单内容均已存在授权，无新增内容可提交')
    return null
  }
  if (skipped > 0) {
    message.info(`已自动跳过 ${skipped} 条已存在授权，仅提交新增 ${items.length} 条`)
  }
  return items
}

const onSubmit = async () => {
  if (!validateBaseFields()) return
  const grantItems = buildGrantItems()
  if (!grantItems) return

  uiState.loading = true
  try {
    const strIds = []
    if (Array.isArray(formState.instance_ids)) {
      for (const id of formState.instance_ids) {
        if (id === null || id === undefined || id === '') continue
        strIds.push(typeof id === 'string' ? id : String(id))
      }
    }
    const singleStrId =
      formState.instance_id == null || formState.instance_id === ''
        ? ''
        : typeof formState.instance_id === 'string'
          ? formState.instance_id
          : String(formState.instance_id)

    const payload = {
      principal_type: formState.principal_type,
      principal_id: formState.principal_id,
      grant_items: grantItems,
    }
    if (isResourceGrant.value) {
      payload.instance_id = singleStrId
    } else {
      payload.instance_ids = strIds
      if (strIds.length > 0) {
        payload.instance_id = strIds[0]
      }
    }
    emit('submit', payload)
  } finally {
    uiState.loading = false
  }
}

const handleCancel = () => {
  emit('update:open', false)
  Object.assign(formState, createDefaultFormState())
  currentPrincipalKey.value = ''
  currentGrantRaw.value = []
  applyInitialPrincipal()
  resetAllSelections()
}

const applyInitialPrincipal = () => {
  if (!props.initialPrincipalType || props.initialPrincipalId === undefined || props.initialPrincipalId === null || props.initialPrincipalId === '') {
    return
  }
  formState.principal_type = props.initialPrincipalType
  formState.principal_id = Number(props.initialPrincipalId)
}

watch(
  () => props.open,
  (open) => {
    if (!open) return
    applyInitialPrincipal()
    nextTick(() => fetchCurrentGrants())
  },
  { immediate: true },
)

watch(
  () => [formState.principal_type, formState.principal_id],
  () => {
    fetchCurrentGrants()
    nextTick(() => refreshRuleItemGrantStatus())
  },
  { flush: 'post' },
)

watch(
  () => formState.instance_id,
  () => {
    nextTick(() => refreshRuleItemGrantStatus())
  },
  { flush: 'post' },
)
</script>

<style scoped>
.grant-scope-boxes {
  display: flex;
  align-items: stretch;
  gap: 6px;
  width: 100%;
}

.grant-scope-box {
  flex: 1 1 0%;
  min-width: 0;
  border: 1px solid #d9d9d9;
  border-radius: 8px;
  padding: 10px 14px;
  background: #fff;
  cursor: pointer;
  transition: border-color 0.15s ease, box-shadow 0.15s ease, background 0.15s ease;
  display: flex;
  flex-direction: column;
  gap: 2px;
  box-sizing: border-box;
}

.grant-scope-box:hover {
  border-color: #4096ff;
}

.grant-scope-box.active {
  border-color: #1677ff;
  box-shadow: 0 0 0 2px rgba(22, 119, 255, 0.1);
  background: #e6f4ff;
}

.grant-scope-box__name {
  font-size: 14px;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.88);
  line-height: 1.4;
}

.grant-scope-box__desc {
  font-size: 12px;
  color: rgba(0, 0, 0, 0.45);
  line-height: 1.4;
}

.grant-summary {
  padding: 12px 14px;
  border: 1px solid #f0f0f0;
  border-radius: 8px;
  background: #fafafa;
}

.grant-summary__title {
  color: rgba(0, 0, 0, 0.88);
  font-weight: 600;
}

.grant-summary__desc {
  margin-top: 4px;
  color: rgba(0, 0, 0, 0.45);
  line-height: 1.6;
}

.instance-option__title {
  color: rgba(0, 0, 0, 0.88);
}

.instance-option__sub {
  color: rgba(0, 0, 0, 0.45);
  font-size: 12px;
}

.inline-tip {
  margin-left: 12px;
  color: rgba(0, 0, 0, 0.45);
  font-size: 12px;
}

.inline-tip--block {
  margin-top: 8px;
  margin-left: 0;
}

.resource-picker {
  display: flex;
  gap: 12px;
  align-items: flex-start;
}

.resource-picker__select {
  flex: 1;
}

.resource-empty {
  padding: 14px 16px;
  border: 1px dashed #d9d9d9;
  border-radius: 8px;
  color: rgba(0, 0, 0, 0.45);
  background: #fafafa;
}

.resource-rule-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.resource-rule-card {
  border: 1px solid #f0f0f0;
  border-radius: 10px;
  background: #fafafa;
}

.resource-rule-card__head {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: flex-start;
  padding: 14px 16px;
  border-bottom: 1px solid #f0f0f0;
}

.resource-rule-card__title {
  color: rgba(0, 0, 0, 0.88);
  font-weight: 600;
}

.resource-rule-card__sub {
  margin-top: 4px;
  color: rgba(0, 0, 0, 0.45);
  font-size: 12px;
}

.resource-rule-card__body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px 16px 16px;
}

.resource-rule-row__label {
  margin-bottom: 8px;
  color: rgba(0, 0, 0, 0.65);
  font-weight: 500;
}

.grant-summary--compact {
  padding: 10px 12px;
}

.instance-option--grant {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.instance-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
}

.instance-option__left {
  min-width: 0;
  flex: 1 1 auto;
  overflow: hidden;
}
.instance-option__title {
  color: rgba(0, 0, 0, 0.88);
  font-size: 13px;
  line-height: 1.4;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.instance-option__sub {
  color: rgba(0, 0, 0, 0.45);
  font-size: 12px;
  line-height: 1.3;
  margin-top: 2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.instance-option__right {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
}

.resource-option-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
}

.resource-option-row__label {
  color: rgba(0, 0, 0, 0.88);
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1 1 auto;
}

.resource-hint-alert {
  margin-bottom: 10px;
}

.resource-rule-card--skip {
  opacity: 0.62;
  background: #f5f5f5;
  border-style: dashed;
}

.resource-rule-card--skip .resource-rule-card__title,
.resource-rule-card--skip .resource-rule-card__sub,
.resource-rule-card--skip .grant-summary {
  text-decoration: line-through;
  text-decoration-color: #bfbfbf;
}

.grant-existing-scope {
  color: #8c8c8c;
}
</style>

<style>
/* 把滚动条限制在弹窗内部，父容器（遮罩层）不出现滚动条 */
.das-form-modal-wrap .ant-modal {
  max-height: calc(100vh - 64px);
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden !important;
  box-sizing: border-box !important;
}

.das-form-modal-wrap .ant-modal-content {
  max-height: calc(100vh - 64px);
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden !important;
  flex: 1 1 auto !important;
  min-height: 0 !important;
  box-sizing: border-box !important;
}

.das-form-modal-wrap .ant-modal-body {
  flex: 1 1 auto !important;
  min-height: 0 !important;
  max-height: calc(100vh - 140px);
  overflow-y: auto !important;
  overflow-x: hidden !important;
  padding: 20px 24px !important;
  box-sizing: border-box !important;
}
</style>
