<template>
  <a-card :bordered="false" class="env-binding-card">
    <template #title>
      <div class="manager-title">
        <a-button type="link" class="back-btn" @click="$emit('back')">返回环境列表</a-button>
        <span class="divider">/</span>
        <span class="env-name">
          <span class="env-dot" :style="{ backgroundColor: environmentColor }" />
          {{ props.environment?.name || '环境数据源管理' }}
        </span>
      </div>
    </template>

    <template #extra>
      <a-space>
        <a-button @click="loadInstances">
          <ReloadOutlined />
          刷新
        </a-button>
        <a-button @click="$emit('back')">取消</a-button>
        <a-button type="primary" :loading="uiState.saving" :disabled="!props.canManage" @click="handleSave">
          保存绑定
        </a-button>
      </a-space>
    </template>

    <div class="summary-grid">
      <div class="summary-card">
        <div class="summary-label">当前绑定</div>
        <div class="summary-value">{{ pendingSelectedIds.length }}</div>
      </div>
      <div class="summary-card">
        <div class="summary-label">新增绑定</div>
        <div class="summary-value">{{ changeSummary.added }}</div>
      </div>
      <div class="summary-card">
        <div class="summary-label">移除绑定</div>
        <div class="summary-value">{{ changeSummary.removed }}</div>
      </div>
      <div class="summary-card">
        <div class="summary-label">跨环境迁入</div>
        <div class="summary-value">{{ changeSummary.migrated }}</div>
      </div>
    </div>

    <div class="manager-panels">
      <section class="manager-panel">
        <div class="panel-header">
          <div class="panel-title">已绑定数据源</div>
          <div class="panel-tools">
            <a-input-search
              v-model:value="filters.boundSearch"
              class="panel-search"
              placeholder="搜索名称/IP/ID"
              allow-clear
            />
            <a-select
              v-model:value="filters.boundDbType"
              class="panel-filter"
              :options="dbTypeOptions"
              placeholder="全部类型"
              allow-clear
            />
          </div>
        </div>

        <a-table
          size="small"
          :columns="boundColumns"
          :data-source="boundRows"
          :loading="uiState.loading"
          :pagination="false"
          :row-key="(record) => record.id"
          :row-selection="boundRowSelection"
          :scroll="{ y: 420 }"
        />
      </section>

      <div class="transfer-actions">
        <a-button type="primary" :disabled="!props.canManage || !selectedPoolKeys.length" block @click="bindSelected">
          绑定
        </a-button>
        <a-button :disabled="!props.canManage || !selectedBoundKeys.length" block @click="unbindSelected">解绑</a-button>
      </div>

      <section class="manager-panel">
        <div class="panel-header">
          <div class="panel-title">待绑定数据源</div>
          <div class="panel-tools panel-tools--triple">
            <a-input-search
              v-model:value="filters.poolSearch"
              class="panel-search"
              placeholder="搜索名称/IP/ID"
              allow-clear
            />
            <a-select
              v-model:value="filters.poolDbType"
              class="panel-filter"
              :options="dbTypeOptions"
              placeholder="全部类型"
              allow-clear
            />
            <a-select
              v-model:value="filters.poolScope"
              class="panel-filter panel-filter--wide"
              :options="poolScopeOptions"
            />
          </div>
        </div>

        <a-table
          size="small"
          :columns="poolColumns"
          :data-source="poolRows"
          :loading="uiState.loading"
          :pagination="false"
          :row-key="(record) => record.id"
          :row-selection="poolRowSelection"
          :scroll="{ y: 420 }"
        />
      </section>
    </div>
  </a-card>
</template>

<script setup>
import { getInstancesApi, updateEnvironmentsApi } from '@/api/admin'
import { ReloadOutlined } from '@ant-design/icons-vue'
import { Modal, message, Tooltip } from 'ant-design-vue'
import { computed, inject, onBeforeUnmount, onMounted, reactive, ref, watch, h } from 'vue'

const props = defineProps({
  environment: {
    type: Object,
    default: null,
  },
  canManage: {
    type: Boolean,
    default: true,
  },
})

const emit = defineEmits(['back', 'saved'])

const uiState = reactive({
  loading: false,
  saving: false,
})

const filters = reactive({
  boundSearch: '',
  boundDbType: undefined,
  poolSearch: '',
  poolDbType: undefined,
  poolScope: 'all',
})

const allInstances = ref([])
const initialSelectedIds = ref([])
const pendingSelectedIds = ref([])
const selectedBoundKeys = ref([])
const selectedPoolKeys = ref([])

const environmentColor = computed(() => props.environment?.color || '#1677ff')

const poolScopeOptions = [
  { label: '全部数据源', value: 'all' },
  { label: '仅未分配环境', value: 'unassigned' },
  { label: '仅其他环境', value: 'other' },
]

const formatDbType = (value) => {
  const normalized = String(value || '').trim().toLowerCase()
  switch (normalized) {
    case 'postgresql':
      return 'PostgreSQL'
    case 'kingbase':
      return 'Kingbase'
    case 'sqlserver':
      return 'SQL Server'
    case 'clickhouse':
      return 'ClickHouse'
    case 'tidb':
      return 'TiDB'
    default:
      return value || '-'
  }
}

const normalizeText = (value) => String(value || '').trim().toLowerCase()

const formatInstanceName = (record) =>
  record.remark || record.data_source_id || `${record.hostname || '-'}:${record.port || '-'}`

const MAX_NAME_LENGTH = 10

const renderTruncatedName = (text) => {
  const str = String(text || '')
  if (str.length <= MAX_NAME_LENGTH) {
    return str
  }
  const truncated = str.slice(0, MAX_NAME_LENGTH) + '...'
  return h(
    Tooltip,
    { title: str, placement: 'top' },
    () => truncated
  )
}

const matchesSearch = (record, keyword) => {
  const target = normalizeText(
    [
      record.remark,
      record.data_source_id,
      record.hostname,
      record.port,
      record.db_type,
      record.environment_name,
    ].join(' ')
  )
  return !keyword || target.includes(normalizeText(keyword))
}

const matchesDbType = (record, dbType) => !dbType || record.db_type === dbType

const getOriginalEnvironmentId = (record) => {
  if (record.environment === null || record.environment === undefined || record.environment === '') {
    return undefined
  }
  const value = Number(record.environment)
  return Number.isNaN(value) ? undefined : value
}

const dbTypeOptions = computed(() => {
  const seen = new Set()
  return (allInstances.value || [])
    .filter((item) => item?.db_type)
    .filter((item) => {
      if (seen.has(item.db_type)) return false
      seen.add(item.db_type)
      return true
    })
    .map((item) => ({ label: formatDbType(item.db_type), value: item.db_type }))
})

const boundRows = computed(() => {
  const selectedSet = new Set(pendingSelectedIds.value)
  return (allInstances.value || []).filter(
    (item) =>
      selectedSet.has(item.id) &&
      matchesSearch(item, filters.boundSearch) &&
      matchesDbType(item, filters.boundDbType)
  )
})

const poolRows = computed(() => {
  const selectedSet = new Set(pendingSelectedIds.value)
  return (allInstances.value || []).filter((item) => {
    if (selectedSet.has(item.id)) return false
    if (!matchesSearch(item, filters.poolSearch)) return false
    if (!matchesDbType(item, filters.poolDbType)) return false
    const originalEnvironmentId = getOriginalEnvironmentId(item)
    if (filters.poolScope === 'unassigned') {
      return !originalEnvironmentId
    }
    if (filters.poolScope === 'other') {
      return !!originalEnvironmentId && originalEnvironmentId !== props.environment?.id
    }
    return true
  })
})

const changeSummary = computed(() => {
  const initialSet = new Set(initialSelectedIds.value)
  const pendingSet = new Set(pendingSelectedIds.value)
  const addedIds = pendingSelectedIds.value.filter((id) => !initialSet.has(id))
  const removed = initialSelectedIds.value.filter((id) => !pendingSet.has(id)).length
  const migrated = addedIds.filter((id) => {
    const instance = allInstances.value.find((item) => item.id === id)
    const originalEnvironmentId = getOriginalEnvironmentId(instance || {})
    return !!originalEnvironmentId && originalEnvironmentId !== props.environment?.id
  }).length
  return {
    added: addedIds.length,
    removed,
    migrated,
  }
})

const boundColumns = [
  {
    title: '数据源',
    dataIndex: 'remark',
    key: 'remark',
    customRender: ({ record }) => renderTruncatedName(formatInstanceName(record)),
  },
  {
    title: '类型',
    dataIndex: 'db_type',
    key: 'db_type',
    width: 110,
    customRender: ({ record }) => formatDbType(record.db_type),
  },
  {
    title: '地址',
    key: 'address',
    width: 170,
    customRender: ({ record }) => `${record.hostname}:${record.port}`,
  },
]

const poolColumns = [
  {
    title: '数据源',
    dataIndex: 'remark',
    key: 'remark',
    customRender: ({ record }) => renderTruncatedName(formatInstanceName(record)),
  },
  {
    title: '类型',
    dataIndex: 'db_type',
    key: 'db_type',
    width: 110,
    customRender: ({ record }) => formatDbType(record.db_type),
  },
  {
    title: '当前环境',
    key: 'environment_name',
    width: 140,
    customRender: ({ record }) => record.environment_name || '未分配',
  },
]

const boundRowSelection = computed(() => {
  if (!props.canManage) return undefined
  return {
    selectedRowKeys: selectedBoundKeys.value,
    onChange: (keys) => {
      selectedBoundKeys.value = keys
    },
  }
})

const poolRowSelection = computed(() => {
  if (!props.canManage) return undefined
  return {
    selectedRowKeys: selectedPoolKeys.value,
    onChange: (keys) => {
      selectedPoolKeys.value = keys
    },
  }
})

const getMigratingInstances = (ids) =>
  ids
    .map((id) => allInstances.value.find((item) => item.id === id))
    .filter((item) => {
      const originalEnvironmentId = getOriginalEnvironmentId(item || {})
      return !!originalEnvironmentId && originalEnvironmentId !== props.environment?.id
    })

const applyBindSelected = () => {
  if (!selectedPoolKeys.value.length) return
  pendingSelectedIds.value = Array.from(new Set([...pendingSelectedIds.value, ...selectedPoolKeys.value]))
  selectedPoolKeys.value = []
}

const bindSelected = () => {
  if (!selectedPoolKeys.value.length) return
  const migratingInstances = getMigratingInstances(selectedPoolKeys.value)
  if (!migratingInstances.length) {
    applyBindSelected()
    return
  }

  const preview = migratingInstances
    .slice(0, 5)
    .map((item) => `${formatInstanceName(item)}（当前环境：${item.environment_name || '未分配'}）`)
    .join('\n')
  const remainCount = migratingInstances.length - Math.min(migratingInstances.length, 5)

  Modal.confirm({
    title: '发现数据源已属于其他环境',
    content: `一个数据源只能属于一个环境。以下数据源当前已绑定其他环境，确认后将从原环境迁移到“${props.environment?.name || '-'}”：\n${preview}${remainCount > 0 ? `\n等 ${migratingInstances.length} 个数据源` : ''}`,
    okText: '确认迁移',
    cancelText: '取消',
    onOk: () => {
      applyBindSelected()
    },
  })
}

const unbindSelected = () => {
  if (!selectedBoundKeys.value.length) return
  const removeSet = new Set(selectedBoundKeys.value)
  pendingSelectedIds.value = pendingSelectedIds.value.filter((id) => !removeSet.has(id))
  selectedBoundKeys.value = []
}

const loadInstances = async () => {
  if (!props.environment?.id) return
  uiState.loading = true
  try {
    const res = await getInstancesApi({ is_page: false }).catch(() => null)
    allInstances.value = res?.data || []
    const fallbackIds = allInstances.value
      .filter((item) => getOriginalEnvironmentId(item) === props.environment.id)
      .map((item) => item.id)
    const ids = Array.isArray(props.environment?.data_source_ids) && props.environment.data_source_ids.length
      ? props.environment.data_source_ids
      : fallbackIds
    initialSelectedIds.value = [...ids]
    pendingSelectedIds.value = [...ids]
    selectedBoundKeys.value = []
    selectedPoolKeys.value = []
  } finally {
    uiState.loading = false
  }
}

const handleSave = async () => {
  if (!props.environment?.id) return
  uiState.saving = true
  try {
    const res = await updateEnvironmentsApi({
      id: props.environment.id,
      name: props.environment.name,
      color: props.environment.color,
      data_source_ids: [...pendingSelectedIds.value],
    }).catch(() => null)
    if (!res) return
    message.success('数据源绑定已更新')
    initialSelectedIds.value = [...pendingSelectedIds.value]
    emit('saved', {
      id: props.environment.id,
      data_source_ids: [...pendingSelectedIds.value],
    })
  } finally {
    uiState.saving = false
  }
}

watch(
  () => [props.environment?.id, JSON.stringify(props.environment?.data_source_ids || [])],
  () => {
    loadInstances()
  },
  { immediate: true }
)

const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)

onMounted(() => {
  if (setLayoutContentNoScroll) {
    setLayoutContentNoScroll(true)
  }
})

onBeforeUnmount(() => {
  if (setLayoutContentNoScroll) {
    setLayoutContentNoScroll(false)
  }
})
</script>

<style scoped>
.env-binding-card {
  min-height: 760px;
  overflow: hidden;
}

.manager-title {
  display: flex;
  align-items: center;
  gap: 10px;
}

.back-btn {
  padding: 0;
}

.divider {
  color: rgba(0, 0, 0, 0.35);
}

.env-name {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}

.env-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
}

.summary-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
  margin-bottom: 16px;
}

.summary-card {
  padding: 16px 18px;
  border: 1px solid #f0f0f0;
  border-radius: 12px;
  background: #fafafa;
}

.summary-label {
  color: rgba(0, 0, 0, 0.45);
  font-size: 13px;
}

.summary-value {
  margin-top: 8px;
  color: rgba(0, 0, 0, 0.88);
  font-size: 24px;
  font-weight: 600;
  line-height: 1;
}

.manager-panels {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
  gap: 20px;
  align-items: stretch;
}

.manager-panel {
  min-width: 0;
}

.panel-header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: center;
  margin-bottom: 12px;
}

.panel-title {
  color: rgba(0, 0, 0, 0.88);
  font-size: 16px;
  font-weight: 600;
  flex-shrink: 0;
}

.panel-tools {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: nowrap;
  min-width: 0;
  flex: 1;
  justify-content: flex-end;
}

.panel-tools--triple {
  flex: 1;
}

.panel-search {
  min-width: 0;
  flex: 1 1 160px;
  max-width: 240px;
}

.panel-filter {
  min-width: 0;
  flex: 0 0 110px;
  max-width: 120px;
}

.panel-filter--wide {
  flex: 0 0 120px;
  max-width: 140px;
}

.transfer-actions {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 12px;
  min-width: 96px;
  align-self: stretch;
}

.transfer-actions :deep(.ant-btn) {
  width: 100%;
}

@media (max-width: 768px) {
  /* 4 列统计卡在窄屏每格只剩 ~80px */
  .summary-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
    margin-bottom: 12px;
  }

  .summary-card {
    padding: 12px;
  }

  /* 左右面板 + 中间 transfer 的三栏结构改为纵向堆叠 */
  .manager-panels {
    grid-template-columns: minmax(0, 1fr);
    gap: 16px;
  }

  .transfer-actions {
    flex-direction: row;
    justify-content: center;
    align-self: auto;
  }

  .panel-header {
    flex-wrap: wrap;
  }

  .panel-title {
    flex: 1 0 100%;
  }

  .panel-tools {
    flex: 1 0 100%;
    justify-content: flex-start;
  }

  .panel-search {
    flex: 1 1 100%;
    max-width: none;
  }

  .panel-filter {
    flex: 0 0 110px;
  }
}
</style>
