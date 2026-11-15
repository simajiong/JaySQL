<template>
  <div class="step-panel">
    <div class="panel-title">配置恢复目标</div>
    <div class="panel-desc">{{ targetStepDesc }}</div>

    <!-- ============ 逻辑恢复 ============ -->
    <template v-if="!isPhysical">
      <a-form layout="vertical" class="step-form">
        <a-form-item label="目标实例" required>
          <a-select
            v-model:value="form.target_instance_id"
            class="block-select"
            :options="instanceOptions"
            :field-names="{ label: 'label', value: 'value' }"
            :loading="instancesLoading"
            :filter-option="filterInstanceOption"
            show-search
            placeholder="仅可选择与备份集同类型的实例"
            @change="(value) => emit('change-instance', value)"
          >
            <template #option="{ label, sub, envType, envName }">
              <div class="set-option">
                <span class="set-option__main">{{ label }}</span>
                <span class="set-option__sub">{{ sub }}</span>
                <a-tag v-if="envType === 'prod'" color="red" class="set-option__env">{{ envName || '生产' }}</a-tag>
                <a-tag v-else-if="envName" color="blue" class="set-option__env">{{ envName }}</a-tag>
              </div>
            </template>
          </a-select>
        </a-form-item>

        <!-- 单文件产物没有可拆分对象，必须显式指定落到哪个库 -->
        <a-form-item v-if="!supportsObjectSelect" :label="`目标${dbLabel}名`" required>
          <a-input
            v-model:value="form.target_db_name"
            :maxlength="64"
            :placeholder="`例如 ${selectedSet?.source_db_name || 'app_db'}_restore`"
          />
          <div class="field-hint">该备份为单对象产物，需指定导入到的目标{{ dbLabel }}名。</div>
        </a-form-item>
      </a-form>

      <a-divider class="panel-divider" />

      <div class="panel-subtitle">恢复对象</div>
      <RestoreObjectPicker
        :form="form"
        :objects="objectsInfo.objects || []"
        :supports-select="supportsObjectSelect"
        :db-type="selectedSet?.db_type || ''"
        :hint="objectsInfo.hint || ''"
        @change="emit('change')"
      />

      <a-divider class="panel-divider" />

      <div class="panel-subtitle">恢复内容</div>
      <div class="selection-card-group">
        <button type="button" class="selection-card" :class="{ active: form.restore_schema }" @click="togglePart('schema')">
          表结构
        </button>
        <button type="button" class="selection-card" :class="{ active: form.restore_data }" @click="togglePart('data')">
          全量数据
        </button>
      </div>
      <div v-if="restoreTypeInvalid" class="field-hint field-hint--error">表结构与全量数据至少需要选择一项。</div>

      <a-divider class="panel-divider" />

      <div class="panel-subtitle">冲突策略</div>
      <a-row :gutter="16">
        <a-col :span="12">
          <div class="sub-label">结构冲突（目标对象已存在）</div>
          <a-radio-group v-model:value="form.schema_conflict_policy" class="conflict-radio-group">
            <a-radio value="SKIP">跳过</a-radio>
            <a-radio value="ABORT">中断</a-radio>
          </a-radio-group>
        </a-col>
        <a-col :span="12">
          <div class="sub-label">数据冲突（目标表已有数据）</div>
          <a-radio-group v-model:value="form.data_conflict_policy" class="conflict-radio-group">
            <a-radio value="SKIP">跳过</a-radio>
            <a-radio value="ABORT">中断</a-radio>
            <a-radio value="TRUNCATE">清空后导入</a-radio>
          </a-radio-group>
        </a-col>
      </a-row>
    </template>

    <!-- ============ MySQL 物理（仅覆盖已有实例） ============ -->
    <template v-else-if="isMySQLPhysical">
      <a-alert type="error" show-icon class="danger-banner">
        <template #message>
          <span class="danger-banner__title">该操作将覆盖目标实例的整个数据目录</span>
        </template>
        <template #description>
          目标实例现有全部数据库将被替换为备份集内容，且不可撤销。MySQL 物理恢复只支持「覆盖已有实例」，不支持拉起新实例。
        </template>
      </a-alert>

      <a-form layout="vertical" class="step-form">
        <a-form-item label="目标 MySQL 实例" required>
          <a-select
            v-model:value="form.target_instance_id"
            class="block-select"
            :options="instanceOptions"
            :field-names="{ label: 'label', value: 'value' }"
            :loading="instancesLoading"
            :filter-option="filterInstanceOption"
            show-search
            placeholder="仅可选择 MySQL 实例"
            @change="(value) => emit('change-instance', value)"
          >
            <template #option="{ label, sub, envType, envName }">
              <div class="set-option">
                <span class="set-option__main">{{ label }}</span>
                <span class="set-option__sub">{{ sub }}</span>
                <a-tag v-if="envType === 'prod'" color="red" class="set-option__env">{{ envName || '生产' }}</a-tag>
                <a-tag v-else-if="envName" color="blue" class="set-option__env">{{ envName }}</a-tag>
              </div>
            </template>
          </a-select>
        </a-form-item>

        <!-- 手输核对：让用户停下来读一遍自己选了什么 -->
        <a-form-item label="核对目标地址" required>
          <a-input v-model:value="form.confirm_host_port" :placeholder="`请输入 ${expectedHostPort}`" />
          <div v-if="hostPortMatched" class="field-hint field-hint--ok">已与目标实例一致。</div>
          <div v-else-if="form.confirm_host_port" class="field-hint field-hint--error">
            与目标实例不一致，请重新输入 {{ expectedHostPort }}。
          </div>
          <div v-else class="field-hint">手动输入目标实例的 host:port 以确认未选错实例。</div>
        </a-form-item>

        <a-form-item label="恢复后自动启动实例">
          <a-switch v-model:checked="form.auto_start" />
        </a-form-item>
      </a-form>
    </template>

    <!-- ============ PG 物理 ============ -->
    <template v-else>
      <a-form layout="vertical" class="step-form">
        <a-form-item label="恢复方式" required>
          <a-radio-group v-model:value="form.restore_mode" button-style="solid">
            <a-radio-button value="NEW_INSTANCE">拉起新实例</a-radio-button>
            <a-radio-button value="EXISTING_INSTANCE">覆盖已有实例</a-radio-button>
          </a-radio-group>
          <div class="field-hint">
            {{ form.restore_mode === 'NEW_INSTANCE' ? '在目标节点上恢复出一个新实例，不影响现有实例。' : '目标实例数据目录将被备份集内容替换。' }}
          </div>
        </a-form-item>

        <a-row :gutter="16">
          <a-col :span="8">
            <a-form-item label="目标节点" required>
              <a-select
                v-model:value="form.target_node_id"
                class="block-select"
                :options="nodeOptions"
                :field-names="{ label: 'label', value: 'value' }"
                :loading="nodesLoading"
                placeholder="选择执行节点"
              />
            </a-form-item>
          </a-col>
          <a-col :span="10">
            <a-form-item label="目标数据目录" required>
              <a-input v-model:value="form.target_data_dir" placeholder="例如 /var/lib/postgresql/14/main" />
            </a-form-item>
          </a-col>
          <a-col :span="6">
            <a-form-item label="目标端口" required>
              <a-input-number v-model:value="form.target_port" :min="1" :max="65535" style="width: 100%" />
            </a-form-item>
          </a-col>
        </a-row>

        <a-form-item v-if="form.restore_mode === 'EXISTING_INSTANCE'" label="目标 PG 实例">
          <a-select
            v-model:value="form.target_instance_id"
            class="block-select"
            :options="instanceOptions"
            :field-names="{ label: 'label', value: 'value' }"
            :loading="instancesLoading"
            :filter-option="filterInstanceOption"
            show-search
            allow-clear
            placeholder="可选，用于记录与审计"
            @change="(value) => emit('change-instance', value)"
          >
            <template #option="{ label, sub, envType, envName }">
              <div class="set-option">
                <span class="set-option__main">{{ label }}</span>
                <span class="set-option__sub">{{ sub }}</span>
                <a-tag v-if="envType === 'prod'" color="red" class="set-option__env">{{ envName || '生产' }}</a-tag>
                <a-tag v-else-if="envName" color="blue" class="set-option__env">{{ envName }}</a-tag>
              </div>
            </template>
          </a-select>
        </a-form-item>

        <a-form-item label="恢复后自动启动实例">
          <a-switch v-model:checked="form.auto_start" />
        </a-form-item>
      </a-form>
    </template>
  </div>
</template>

<script setup>
import { computed, inject } from 'vue'
import RestoreObjectPicker from './RestoreObjectPicker.vue'

defineOptions({ name: 'RestoreWizardStepTarget' })

const form = inject('restoreWizardForm')

const props = defineProps({
  objectsInfo: { type: Object, required: true },
  instances: { type: Array, default: () => [] },
  instancesLoading: { type: Boolean, default: false },
  nodes: { type: Array, default: () => [] },
  nodesLoading: { type: Boolean, default: false },
  selectedSet: { type: Object, default: null },
  isPhysical: { type: Boolean, default: false },
  isMySQLPhysical: { type: Boolean, default: false },
})

const emit = defineEmits(['change', 'change-instance'])

// 金仓与 PostgreSQL 同属 PG 家族：层级都是「库」而不是 MySQL 的「数据库」，
// 目标实例也要同族放行（Kingbase 与 KingbaseES 只是写法差异，不能因此筛空列表）。
const PG_FAMILY_DB_TYPES = ['postgresql', 'kingbase', 'kingbasees']
const isPgFamily = (dbType) => PG_FAMILY_DB_TYPES.includes(String(dbType || '').trim().toLowerCase())

const dbLabel = computed(() => (isPgFamily(props.selectedSet?.db_type) ? '库' : '数据库'))

const supportsObjectSelect = computed(() => !props.isPhysical && !!props.objectsInfo.supports_object_select)

const restoreTypeInvalid = computed(() => !form.restore_schema && !form.restore_data)

const targetStepDesc = computed(() => {
  if (props.isMySQLPhysical) return 'MySQL 物理恢复为整实例覆盖，请仔细核对目标实例。'
  if (props.isPhysical) return 'PostgreSQL 物理恢复由 pgBackRest 按备份标签回放。'
  return ''
})

const instanceOptions = computed(() => {
  const dbType = String(props.selectedSet?.db_type || '').trim().toLowerCase()
  return props.instances
    .filter(
      (item) =>
        !dbType ||
        String(item.db_type || '').trim().toLowerCase() === dbType ||
        (isPgFamily(dbType) && isPgFamily(item.db_type)),
    )
    .map((item) => {
      const hostPort = item.port ? `${item.hostname || ''}:${item.port}` : item.hostname || ''
      return {
        value: item.id,
        label: item.remark || hostPort || `实例 #${item.id}`,
        sub: `${item.db_type || '-'} · ${hostPort}`,
        envType: String(item.env_type || '').toLowerCase(),
        envName: item.environment_name || '',
        search: `${item.remark || ''} ${item.hostname || ''} ${item.port || ''} ${item.db_type || ''} ${item.environment_name || ''}`.toLowerCase(),
      }
    })
})

const nodeOptions = computed(() =>
  (props.nodes || []).map((item) => ({
    value: item.id,
    label: item.name || item.hostname || `节点 #${item.id}`,
    sub: item.hostname || '',
  })),
)

const filterInstanceOption = (input, option) => String(option?.search || '').includes(String(input || '').toLowerCase())

const selectedInstance = computed(() => props.instances.find((item) => item.id === form.target_instance_id) || null)

const expectedHostPort = computed(() => {
  const item = selectedInstance.value
  if (!item) return 'host:port'
  return item.port ? `${item.hostname || ''}:${item.port}` : item.hostname || ''
})

const hostPortMatched = computed(() => {
  const input = String(form.confirm_host_port || '').trim()
  return !!input && input === expectedHostPort.value
})

const togglePart = (part) => {
  if (part === 'schema') {
    if (form.restore_schema && !form.restore_data) return
    form.restore_schema = !form.restore_schema
    return
  }
  if (form.restore_data && !form.restore_schema) return
  form.restore_data = !form.restore_data
}

defineExpose({ hostPortMatched })
</script>

<style scoped lang="scss">
.step-panel {
  max-width: 960px;
}

.panel-title {
  font-size: var(--gi-font-size-body-lg, 16px);
  font-weight: 600;
  color: var(--gi-color-text-primary, #1f2d3d);
}

.panel-desc,
.panel-subtitle {
  margin-top: var(--gi-spacing-xs, 4px);
  font-size: var(--gi-font-size-caption, 12px);
  color: var(--gi-text-tertiary, #8a979f);
}

.panel-subtitle {
  font-size: var(--gi-font-size-body, 14px);
  font-weight: 600;
  color: var(--gi-color-text-primary, #1f2d3d);
  margin-bottom: var(--gi-spacing-sm, 8px);
}

.step-form {
  max-width: 720px;
  margin-top: var(--gi-spacing-md, 16px);
}

.block-select {
  width: 100%;
  max-width: 640px;
}

.set-option {
  display: flex;
  align-items: baseline;
  gap: var(--gi-spacing-sm, 8px);
  overflow: hidden;
}

.set-option__main {
  color: var(--gi-color-text-primary, #1f2d3d);
}

.set-option__sub {
  color: var(--gi-text-tertiary, #8a979f);
  font-size: var(--gi-font-size-caption, 12px);
}

.set-option__env {
  transform: scale(0.85);
  transform-origin: left center;
}

.panel-divider {
  margin: var(--gi-spacing-md, 16px) 0;
}

.sub-label {
  font-size: var(--gi-font-size-caption, 12px);
  color: var(--gi-text-tertiary, #8a979f);
  margin-bottom: var(--gi-spacing-xs, 4px);
}

.field-hint {
  margin-top: var(--gi-spacing-xs, 4px);
  font-size: var(--gi-font-size-caption, 12px);
  color: var(--gi-text-tertiary, #8a979f);
}

.field-hint--error {
  color: var(--gi-color-error, #ff4d4f);
}

.field-hint--ok {
  color: var(--gi-color-primary, #0f766e);
}

.conflict-radio-group {
  display: flex;
  flex-wrap: wrap;
  gap: var(--gi-spacing-md, 16px);
  padding-top: var(--gi-spacing-xs, 4px);
}

.conflict-radio-group :deep(.ant-radio-wrapper) {
  color: var(--gi-color-text-secondary, #5c6b73);
}

.conflict-radio-group :deep(.ant-radio-checked .ant-radio-inner) {
  border-color: var(--gi-color-primary, #0f766e);
}

.conflict-radio-group :deep(.ant-radio-checked .ant-radio-inner::after) {
  background-color: var(--gi-color-primary, #0f766e);
}

.selection-card-group {
  display: flex;
  gap: var(--gi-spacing-sm, 8px);
  flex-wrap: wrap;
}

.selection-card {
  min-width: 108px;
  padding: 8px 16px;
  border: 1px solid var(--gi-border-color, #d9e1e6);
  border-radius: var(--gi-radius-md, 8px);
  background: var(--gi-color-container-bg, #fff);
  color: var(--gi-color-text-secondary, #5c6b73);
  cursor: pointer;
  transition: all 120ms;
}

.selection-card:hover {
  border-color: var(--gi-color-primary, #0f766e);
  color: var(--gi-color-primary, #0f766e);
}

.selection-card.active {
  border-color: var(--gi-color-primary, #0f766e);
  background: var(--gi-color-primary-light, rgba(15, 118, 110, 0.08));
  color: var(--gi-color-primary, #0f766e);
  font-weight: 600;
}

.danger-banner {
  max-width: 860px;
  margin-bottom: var(--gi-spacing-md, 16px);
}

.danger-banner__title {
  font-weight: 600;
}
</style>
