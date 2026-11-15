<template>
  <div class="order-create-wrapper" :class="{ 'is-in-fullscreen': !!uiState.fullscreenTarget }">
    <a-card class="order-create-card" :bordered="false">
      <div class="steps-shell">
        <a-steps :current="0" size="small">
          <a-step title="提交任务" />
          <a-step title="规范预审" />
          <a-step title="提交审批" />
          <a-step title="执行任务" />
          <a-step title="完成" />
        </a-steps>
      </div>

      <a-row class="create-row" :gutter="20">
        <a-col :xs="24" :lg="8">
          <div class="left-panel">
            <div class="left-panel-scroll">
              <a-form ref="formRef" :model="formState" layout="vertical" :rules="rules" autocomplete="off">
                <a-form-item label="任务名称" name="title" class="form-item-required">
                  <a-input
                    v-model:value="formState.title"
                    placeholder="请输入任务名称"
                    allow-clear
                  />
                </a-form-item>

                <a-form-item label="数据源" name="instance_id" class="form-item-required">
                  <div class="inline-select-row">
                    <a-select
                      v-model:value="formState.db_type"
                      class="source-type-select"
                      :options="uiData.dbTypes"
                      placeholder="数据库类型"
                      @change="handleDbTypeChange"
                    />
                    <a-select
                      v-model:value="formState.instance_id"
                      :class="['source-instance-select', { 'auto-selected-instance': uiState.instanceSelectionMuted }]"
                      show-search
                      option-filter-prop="search"
                      :disabled="!formState.db_type"
                      placeholder="请选择数据库实例"
                      allow-clear
                      @change="handleInstanceChange"
                    >
                      <a-select-option
                        v-for="item in filteredDataSources"
                        :key="item.instance_id"
                        :value="item.instance_id"
                        :label="formatInstanceSelectionLabel(item)"
                        :search="`${item.remark || ''} ${item.hostname}:${item.port}`"
                      >
                        <div class="instance-option">
                          <div class="instance-option-remark">{{ item.remark || `${item.hostname}:${item.port}` }}</div>
                          <div class="instance-option-address">
                            {{ item.hostname }}:{{ item.port }}
                            <span v-if="isDataSourceUnavailable(item)" class="instance-option-badge">不可用</span>
                          </div>
                        </div>
                      </a-select-option>
                    </a-select>
                    <a-button class="icon-button" @click="loadDataSources">
                      <template #icon>
                        <ReloadOutlined />
                      </template>
                    </a-button>
                  </div>
                  <a-alert v-if="instanceUnavailable" class="instance-unavailable-alert" type="warning" show-icon>
                    <template #message>
                      <span class="instance-unavailable-title">实例当前不可用</span>
                    </template>
                    <template #description>
                      <div class="instance-unavailable-body">
                        <div class="instance-unavailable-subtitle">提交后可能无法执行，请先确认实例连通性。</div>
                        <div class="instance-unavailable-actions">
                          <a-button
                            size="small"
                            :loading="uiState.instanceChecking"
                            :disabled="!selectedDataSource?.id"
                            @click="retryInstanceCheck"
                          >
                            重新检测
                          </a-button>
                          <a-button size="small" :disabled="!instanceUnavailableDetail" @click="copyInstanceError">
                            {{ uiState.instanceErrorCopied ? '已复制' : '复制错误' }}
                          </a-button>
                          <a-button size="small" type="link" @click="editInstanceConnection">编辑连接信息</a-button>
                          <a-popover placement="bottomLeft" trigger="click" overlay-class-name="order-instance-error-popover">
                            <template #content>
                              <div class="order-instance-error-detail">
                                <div class="order-instance-error-title">原始错误</div>
                                <pre class="order-instance-error-pre">{{ instanceUnavailableDetail || '暂无错误详情' }}</pre>
                              </div>
                            </template>
                            <a-button type="link" size="small" class="instance-error-link">查看原始错误</a-button>
                          </a-popover>
                        </div>
                      </div>
                    </template>
                  </a-alert>
                </a-form-item>

                <a-form-item label="数据库" name="database" class="form-item-required">
                  <div class="inline-select-row">
                    <a-select
                      v-model:value="formState.database"
                      class="database-select"
                      show-search
                      option-filter-prop="label"
                      :options="uiData.databaseOptions"
                      :disabled="!formState.instance_id || !needsDatabaseSelection || instanceUnavailable"
                      :placeholder="needsDatabaseSelection ? '请选择数据库' : '当前 SQL 无需选择数据库'"
                      allow-clear
                      @change="handleDatabaseChange"
                    />
                    <a-button class="icon-button" :disabled="!formState.instance_id || instanceUnavailable" :loading="uiState.metaRefreshing" @click="refreshDatabaseOptions">
                      <template #icon>
                        <ReloadOutlined />
                      </template>
                    </a-button>
                  </div>
                  <div v-if="databaseHintText" class="field-tip">{{ databaseHintText }}</div>
                </a-form-item>

                <a-form-item v-if="isPgLikeSelected" label="Schema" name="schema" class="form-item-required">
                  <div class="inline-select-row">
                    <a-select
                      v-model:value="formState.schema"
                      class="database-select"
                      show-search
                      option-filter-prop="label"
                      :options="uiData.schemaOptions"
                      :disabled="!formState.instance_id || !formState.database || !needsSchemaSelection || instanceUnavailable"
                      :placeholder="needsSchemaSelection ? '请选择 Schema' : '当前 SQL 无需选择现有 Schema'"
                      allow-clear
                      @change="handleSchemaChange"
                    />
                    <a-button
                      class="icon-button"
                      :disabled="!formState.instance_id || !formState.database || !needsSchemaSelection || instanceUnavailable"
                      :loading="uiState.metaRefreshing"
                      @click="refreshSchemaOptions"
                    >
                      <template #icon>
                        <ReloadOutlined />
                      </template>
                    </a-button>
                  </div>
                  <div v-if="schemaHintText" class="field-tip">{{ schemaHintText }}</div>
                </a-form-item>

                <a-form-item label="执行人" name="executor" class="form-item-required">
                  <a-select
                    v-model:value="formState.executor"
                    :options="uiData.executorOptions"
                    show-search
                    option-filter-prop="label"
                    placeholder="请选择执行人"
                    allow-clear
                  />
                </a-form-item>

                <a-form-item label="预估影响行数" name="estimated_affected_rows">
                  <a-input-number
                    v-model:value="formState.estimated_affected_rows"
                    class="full-width"
                    :min="0"
                    :precision="0"
                    placeholder="请输入预估影响行数"
                  />
                </a-form-item>

                <a-form-item label="备注" name="remark">
                  <a-textarea
                    v-model:value="formState.remark"
                    class="remark-textarea"
                    rows="1"
                    placeholder="填写变更原因、期望时间窗口、业务说明等"
                  />
                </a-form-item>

                <a-form-item class="form-actions">
                  <a-button type="primary" :loading="uiState.loading" @click="handleSaveAndSubmit">
                    {{ editOrderId ? '保存修改并提交' : '保存并提交' }}
                  </a-button>
                </a-form-item>
              </a-form>
            </div>
          </div>
        </a-col>

        <a-col :xs="24" :lg="16">
          <div class="right-panel">
            <div class="sql-section sql-section-change" :class="{ 'is-fullscreen': uiState.fullscreenTarget === 'change' }">
              <div class="sql-section-header">
                <span class="sql-title required">变更 SQL</span>
                <div class="sql-section-header-right">
                  <a-radio-group v-model:value="uiState.changeSqlMode" size="small">
                    <a-radio value="text">SQL 文本</a-radio>
                    <a-radio value="file">SQL 文件</a-radio>
                  </a-radio-group>
                  <a-button
                    type="text"
                    size="small"
                    class="fullscreen-btn"
                    :title="uiState.fullscreenTarget === 'change' ? '退出全屏 (Esc)' : '全屏编辑'"
                    @click="toggleFullscreen('change')"
                  >
                    <FullscreenExitOutlined v-if="uiState.fullscreenTarget === 'change'" />
                    <FullscreenOutlined v-else />
                  </a-button>
                </div>
              </div>

              <div v-if="uiState.changeSqlMode === 'file'" class="upload-bar">
                <a-upload
                  :show-upload-list="false"
                  accept=".sql"
                  :before-upload="(file) => readSqlFile(file, 'change')"
                >
                  <a-button>
                    <template #icon>
                      <UploadOutlined />
                    </template>
                    选择 SQL 文件
                  </a-button>
                </a-upload>
                <span class="upload-name">{{ uiState.changeSqlFileName || '未选择文件' }}</span>
              </div>

              <div class="editor-shell editor-shell-main">
                <CodeMirror ref="codemirrorRef" :height="'100%'" @state-change="handleMainSqlStateChange" />
              </div>

              <!-- 语法错误内联提示：就近展示在编辑器下方，给出位置与修改建议。
                   提示在 SQL 被修改后自动消失，避免陈旧提示误导用户。 -->
              <div v-if="syntaxState.message" class="sql-syntax-alert">
                <span class="sql-syntax-alert-text">{{ syntaxState.message }}</span>
                <span v-if="syntaxState.advice" class="sql-syntax-alert-advice">{{ syntaxState.advice }}</span>
              </div>
            </div>

            <div class="sql-section sql-section-rollback" :class="{ 'is-fullscreen': uiState.fullscreenTarget === 'rollback' }">
              <div class="sql-section-header">
                <span class="sql-title">回滚 SQL</span>
                <div class="sql-section-header-right">
                  <a-radio-group v-model:value="uiState.rollbackSqlMode" size="small">
                    <a-radio value="text">SQL 文本</a-radio>
                    <a-radio value="file">SQL 文件</a-radio>
                  </a-radio-group>
                  <a-button
                    type="text"
                    size="small"
                    class="fullscreen-btn"
                    :title="uiState.fullscreenTarget === 'rollback' ? '退出全屏 (Esc)' : '全屏编辑'"
                    @click="toggleFullscreen('rollback')"
                  >
                    <FullscreenExitOutlined v-if="uiState.fullscreenTarget === 'rollback'" />
                    <FullscreenOutlined v-else />
                  </a-button>
                </div>
              </div>

              <div v-if="uiState.rollbackSqlMode === 'file'" class="upload-bar">
                <a-upload
                  :show-upload-list="false"
                  accept=".sql"
                  :before-upload="(file) => readSqlFile(file, 'rollback')"
                >
                  <a-button>
                    <template #icon>
                      <UploadOutlined />
                    </template>
                    选择 SQL 文件
                  </a-button>
                </a-upload>
                <span class="upload-name">{{ uiState.rollbackSqlFileName || '未选择文件' }}</span>
              </div>

              <div class="editor-shell editor-shell-rollback">
                <CodeMirror ref="rollbackCodemirrorRef" :height="'100%'" @state-change="handleRollbackSqlStateChange" />
              </div>
            </div>
          </div>
        </a-col>
      </a-row>
    </a-card>
  </div>
</template>

<script setup>
import { testInstanceConnectionApi } from '@/api/admin'
import { RefreshInstanceMetaApi } from '@/api/das'
import { createOrderApi, getOrderDataSourcesApi, getOrderDetailApi, getOrderSchemasApi, getOrderUsersApi, preInspectOrderApi, updateOrderContentApi } from '@/api/order'
import CodeMirror from '@/components/edit/Codemirror.vue'
import { useOrderCreatePrefillStore } from '@/store/prefill'
import { useUserStore } from '@/store/user'
import { ReloadOutlined, UploadOutlined, FullscreenOutlined, FullscreenExitOutlined } from '@ant-design/icons-vue'
import { useThrottleFn } from '@vueuse/core'
import { message } from 'ant-design-vue'
import { computed, nextTick, onActivated, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'

const router = useRouter()
const route = useRoute()
const userStore = useUserStore()
const formRef = ref()
const codemirrorRef = ref()
const rollbackCodemirrorRef = ref()

const TITLE_MAX_CHARS = 64

const formState = reactive({
  title: '',
  remark: '',
  db_type: 'MySQL',
  instance_id: undefined,
  database: undefined,
  schema: undefined,
  executor: undefined,
  estimated_affected_rows: null,
  rollback_sql: '',
  content: '',
  cc: [],
})

const editorState = reactive({
  content: '',
  rollbackSql: '',
})

const rules = {
  title: [
    {
      validator: async (_rule, value) => {
        const trimmed = String(value || '').trim()
        if (!trimmed) return Promise.reject('请输入任务名称')
        const chars = [...trimmed]
        if (chars.length < 3) return Promise.reject('任务名称至少 3 个字符')
        if (chars.length > TITLE_MAX_CHARS) return Promise.reject(`最多支持 ${TITLE_MAX_CHARS} 个字符`)
        return Promise.resolve()
      },
      trigger: 'change',
    },
  ],
  instance_id: [
    {
      validator: async (_rule, value) => {
        if (!value) return Promise.reject('请选择数据源')
        return Promise.resolve()
      },
      trigger: 'change',
    },
  ],
  database: [
    {
      validator: async (_rule, value) => {
        if (!needsDatabaseSelection.value) return Promise.resolve()
        if (!value) return Promise.reject('请选择数据库')
        return Promise.resolve()
      },
      trigger: ['change', 'blur'],
    },
  ],
  schema: [
    {
      validator: async (_rule, value) => {
        if (!needsSchemaSelection.value) return Promise.resolve()
        if (!value) return Promise.reject('请选择 Schema')
        return Promise.resolve()
      },
      trigger: ['change', 'blur'],
    },
  ],
  executor: [
    {
      validator: async (_rule, value) => {
        if (!value) return Promise.reject('请选择执行人')
        return Promise.resolve()
      },
      trigger: 'change',
    },
  ],
  remark: [{ max: 1024, message: '备注不能超过 1024 个字符', trigger: 'blur' }],
}

const uiState = reactive({
  loading: false,
  changeSqlMode: 'text',
  rollbackSqlMode: 'text',
  changeSqlFileName: '',
  rollbackSqlFileName: '',
  instanceSelectionMuted: false,
  instanceChecking: false,
  instanceErrorCopied: false,
  instanceRuntimeError: '',
  // 库清单正在向目标实例重新采集：刷新按钮据此转 loading，
  // 避免「点了没反应」——同步 SQLServer 的多个库本来就要几秒。
  metaRefreshing: false,
  fullscreenTarget: null,
})

const uiData = reactive({
  dataSources: [],
  executorOptions: [],
  databaseOptions: [],
  schemaOptions: [],
  dbTypes: [
    { value: 'MySQL', label: 'MySQL' },
    { value: 'PostgreSQL', label: 'PostgreSQL' },
    // 金仓：PG 协议，审核/执行均复用 PG 通道（后端 db_support.isPgLikeDB 已含 kingbase）
    { value: 'Kingbase', label: 'Kingbase' },
    { value: 'Oracle', label: 'Oracle' },
    { value: 'TiDB', label: 'TiDB' },
    { value: 'Doris', label: 'Doris' },
    // 与后端 validateOrderSQLTypeSupport / GetOrderDataSourcesService 的放行清单保持一致；
    // 库/schema 两级联动与 PG 同口径（isPgLikeSelected 已包含 sqlserver）
    { value: 'SQLServer', label: 'SQLServer' },
  ],
})

const normalizedDbType = computed(() => String(formState.db_type || '').toLowerCase())
const trimmedChangeSql = computed(() => normalizeSqlIntentText(editorState.content))
const isPgLikeSelected = computed(() => ['postgresql', 'kingbase', 'sqlserver'].includes(normalizedDbType.value))
const isCreateSchemaIntent = computed(() => /^create\s+schema\b/i.test(trimmedChangeSql.value))
const isCreateDatabaseIntent = computed(() => {
  if (/^create\s+database\b/i.test(trimmedChangeSql.value)) return true
  return isCreateSchemaIntent.value && !isPgLikeSelected.value
})
const needsDatabaseSelection = computed(() => !isCreateDatabaseIntent.value)
const needsSchemaSelection = computed(
  () => isPgLikeSelected.value && !isCreateDatabaseIntent.value && !isCreateSchemaIntent.value,
)
const databaseHintText = computed(() => {
  if (isCreateDatabaseIntent.value) return '已识别 CREATE DATABASE，当前无需选择数据库'
  if (isCreateSchemaIntent.value && isPgLikeSelected.value) return '已识别 CREATE SCHEMA，请先选择目标数据库'
  return ''
})
const schemaHintText = computed(() => {
  if (!isPgLikeSelected.value) return ''
  if (isCreateSchemaIntent.value) return '已识别 CREATE SCHEMA，当前无需选择现有 Schema'
  return ''
})
const filteredDataSources = computed(() =>
  uiData.dataSources
    .filter((item) => !formState.db_type || item.db_type === formState.db_type),
)

const normalizeCheckStatus = (value) => String(value || '').trim().toLowerCase()
const isDataSourceUnavailable = (item) => normalizeCheckStatus(item?.last_check_status) === 'error'
const selectedDataSource = computed(() => uiData.dataSources.find((item) => item.instance_id === formState.instance_id) || null)
const instanceUnavailable = computed(
  () => normalizeCheckStatus(selectedDataSource.value?.last_check_status) === 'error' || Boolean(uiState.instanceRuntimeError),
)
const instanceUnavailableDetail = computed(() =>
  String(selectedDataSource.value?.last_check_message || uiState.instanceRuntimeError || '').trim(),
)
let copiedTimer = null

const resetCopiedState = () => {
  uiState.instanceErrorCopied = false
  if (copiedTimer) {
    clearTimeout(copiedTimer)
    copiedTimer = null
  }
}

const retryInstanceCheck = async () => {
  const instanceId = selectedDataSource.value?.id
  if (!instanceId) return
  try {
    uiState.instanceChecking = true
    await testInstanceConnectionApi(instanceId, { silentBusinessError: true, silentHttpError: true })
    uiState.instanceRuntimeError = ''
    await loadDataSources()
    message.success('已重新检测实例连通性')
  } finally {
    uiState.instanceChecking = false
  }
}

const copyInstanceError = async () => {
  const text = instanceUnavailableDetail.value
  if (!text) return
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
    } else {
      const textarea = document.createElement('textarea')
      textarea.value = text
      textarea.setAttribute('readonly', 'readonly')
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      document.body.removeChild(textarea)
    }
    resetCopiedState()
    uiState.instanceErrorCopied = true
    copiedTimer = setTimeout(() => resetCopiedState(), 1600)
  } catch {
    resetCopiedState()
    message.error('复制失败')
  }
}

const editInstanceConnection = () => {
  const target = String(selectedDataSource.value?.instance_id || selectedDataSource.value?.id || '').trim()
  router.push({ path: '/datasource/instance', query: target ? { edit_instance_id: target } : {} })
}

const syncEditorDialect = (dbType = formState.db_type) => {
  codemirrorRef.value?.setDialect(dbType || 'sql')
  rollbackCodemirrorRef.value?.setDialect(dbType || 'sql')
}

const syncSqlContent = () => {
  formState.content = codemirrorRef.value?.getContent() || editorState.content || ''
  formState.rollback_sql = rollbackCodemirrorRef.value?.getContent() || editorState.rollbackSql || ''
}

const clearInstanceCascade = () => {
  formState.instance_id = undefined
  formState.database = undefined
  formState.schema = undefined
  uiState.instanceSelectionMuted = false
  resetCopiedState()
  uiState.instanceRuntimeError = ''
  uiData.databaseOptions = []
  uiData.schemaOptions = []
}

const normalizeSqlIntentText = (sqlText) =>
  String(sqlText || '')
    .replace(/^\s*(--.*?(\r?\n|$)|\/\*[\s\S]*?\*\/)*/g, '')
    .trimStart()

const formatInstanceSelectionLabel = (item) => {
  const address = `${item.hostname}:${item.port}`
  const base = item.remark ? `${item.remark} (${address})` : address
  return isDataSourceUnavailable(item) ? `${base}（不可用）` : base
}

// 库/schema 下拉走工单自己的 schemas 接口（order.submit 即可访问），
// 不依赖 das 的资源查询（需要 sql.console.access），保证「仅有提交工单权限」的用户也能建单；
// 系统库过滤由后端 GetOrderSchemasService 统一下推（非 admin 隐藏 information_schema/mysql/sys/pg_catalog 等）。
const fetchResources = async (params, fallbackMessage = '加载数据库资源失败') => {
  const res = await getOrderSchemasApi({ is_page: false, ...params }).catch((err) => {
    uiState.instanceRuntimeError = String(err?.message || err?.data?.message || fallbackMessage).trim()
    message.error(uiState.instanceRuntimeError)
    return null
  })
  if (res) uiState.instanceRuntimeError = ''
  return res?.data || []
}

const formatSchemaLabel = (resource, fallbackPrefix = '') => {
  const raw = resource?.display_name || resource?.schema || ''
  if (!raw) return fallbackPrefix
  if (fallbackPrefix) {
    const parts = raw.split('.')
    return fallbackPrefix ? `${fallbackPrefix} / ${parts[parts.length - 1]}` : parts[parts.length - 1]
  }
  if (['postgresql', 'kingbase', 'sqlserver'].includes(normalizedDbType.value)) {
    const parts = raw.split('.')
    return parts[parts.length - 1]
  }
  return raw
}

const loadDataSources = async () => {
  const res = await getOrderDataSourcesApi({ is_page: false }).catch(() => null)
  uiData.dataSources = res?.data || []
  const currentExists = filteredDataSources.value.some((item) => item.instance_id === formState.instance_id)
  if (formState.instance_id && !currentExists) {
    clearInstanceCascade()
  }
}

const loadExecutorOptions = async () => {
  const res = await getOrderUsersApi({ is_page: false, perm_scope: 'executor' }).catch(() => null)
  const rows = Array.isArray(res?.data) ? res.data : []
  const currentUser = {
    username: userStore.username,
  }
  const normalized = [
    currentUser,
    ...rows.filter((item) => item?.username && item.username !== currentUser.username),
  ]
  uiData.executorOptions = normalized.map((item) => ({
    value: item.username,
    label: item.username,
  }))
}

const loadDatabaseOptions = async (instanceID = formState.instance_id) => {
  uiData.databaseOptions = []
  uiData.schemaOptions = []
  formState.database = undefined
  formState.schema = undefined
  if (!instanceID) return

  let resourceType = 'database'
  if (normalizedDbType.value === 'oracle') resourceType = 'schema'
  const resources = await fetchResources({
    instance_id: instanceID,
    resource_type: resourceType,
  })
  uiData.databaseOptions = resources.map((item) => ({
    label: formatSchemaLabel(item),
    value: item.schema,
  }))
}

const loadSchemaOptions = async (database = formState.database) => {
  uiData.schemaOptions = []
  formState.schema = undefined
  if (!formState.instance_id || !database || !isPgLikeSelected.value) return

  const schemas = await fetchResources({
    instance_id: formState.instance_id,
    resource_type: 'schema',
    parent_path: database,
  })
  uiData.schemaOptions = schemas.map((item) => ({
    label: formatSchemaLabel(item),
    value: item.schema,
  }))
}

const validateForm = async () => {
  await formRef.value?.validate()
}

const ensureSqlInputs = () => {
  syncSqlContent()
  if (!formState.content.trim()) {
    message.warning('变更 SQL 不能为空')
    return false
  }
  return true
}

const handleDbTypeChange = async () => {
  clearInstanceCascade()
  syncEditorDialect(formState.db_type)
}

const handleInstanceChange = async () => {
  uiState.instanceSelectionMuted = false
  resetCopiedState()
  uiState.instanceRuntimeError = ''
  formState.database = undefined
  formState.schema = undefined
  uiData.databaseOptions = []
  uiData.schemaOptions = []
  await loadDatabaseOptions()
}

const handleDatabaseChange = async () => {
  if (!isPgLikeSelected.value) {
    formState.schema = formState.database
    uiData.schemaOptions = []
    return
  }
  formState.schema = undefined
  uiData.schemaOptions = []
  if (isPgLikeSelected.value) {
    await loadSchemaOptions()
  }
}

const handleSchemaChange = () => {
}

const handleMainSqlStateChange = ({ content }) => {
  editorState.content = content || ''
  syncSqlContent()
}

const handleRollbackSqlStateChange = ({ content }) => {
  editorState.rollbackSql = content || ''
  syncSqlContent()
}

const readSqlFile = async (file, target) => {
  try {
    const text = await file.text()
    if (target === 'change') {
      uiState.changeSqlFileName = file.name
      codemirrorRef.value?.setContent(text)
      editorState.content = text
    } else {
      uiState.rollbackSqlFileName = file.name
      rollbackCodemirrorRef.value?.setContent(text)
      editorState.rollbackSql = text
    }
  } catch {
    message.error('读取 SQL 文件失败')
  }
  return false
}

// 刷新按钮不能只重读本地快照：快照没过期时怎么点都看不到刚建的库。
// 先让后端去目标实例真正重采一次（与写后失效、读时惰性共同构成三条刷新路径），
// 再回来读快照；失败时保留原选项，只提示原因，不把下拉清空。
const refreshMetaAndReload = async (reload) => {
  if (!formState.instance_id || uiState.metaRefreshing) return
  uiState.metaRefreshing = true
  try {
    const res = await RefreshInstanceMetaApi(formState.instance_id).catch((err) => {
      const text = String(err?.message || err?.data?.message || '刷新库列表失败，请检查数据源连接状态').trim()
      uiState.instanceRuntimeError = text
      message.error(text)
      return null
    })
    if (!res) return
    await reload()
  } finally {
    uiState.metaRefreshing = false
  }
}

const refreshSchemaOptions = async () => {
  if (!formState.instance_id || !formState.database) return
  await refreshMetaAndReload(loadSchemaOptions)
}

const refreshDatabaseOptions = async () => {
  if (!formState.instance_id) return
  await refreshMetaAndReload(loadDatabaseOptions)
}

const submitOrder = async () => {
  // 编辑模式：已撤回工单原地更新（工单 ID 不变），保存后回到「待预审」。
  // 除内容外，数据源/数据库/Schema/执行人也可改——工单被撤回的常见原因就是
  // 「选错了库」，后端会按创建同口径重新校验数据源与目标资源
  // （2026-10-24 起不再校验数据访问授权：功能权限 order.submit 决定能不能提交，
  //  数据授权不再参与候选列表与提交校验，与性能分析口径统一）。
  if (editOrderId.value) {
    const res = await updateOrderContentApi({
      order_id: editOrderId.value,
      title: formState.title,
      remark: formState.remark,
      content: formState.content,
      rollback_sql: formState.rollback_sql,
      estimated_affected_rows: formState.estimated_affected_rows || 0,
      db_type: formState.db_type,
      instance_id: formState.instance_id,
      database: formState.database,
      // 与创建同口径：PG 类把 schema 原样上报，MySQL 类把 schema 对齐为 database
      schema: isPgLikeSelected.value ? formState.schema : formState.database,
      executor: formState.executor,
    }).catch(() => null)
    if (!res) {
      return message.error('保存修改失败，请稍后再试')
    }
    message.success('工单已更新，正在进入规范预审')
    navigateAfterSave(editOrderId.value, `工单已更新，工单号 ${editOrderId.value}，可在工单列表中查看`)
    return
  }

  const normalizedSchema = isPgLikeSelected.value ? formState.schema : formState.database
  const res = await createOrderApi({
    title: formState.title,
    remark: formState.remark,
    db_type: formState.db_type,
    instance_id: formState.instance_id,
    database: formState.database,
    schema: normalizedSchema,
    executor: formState.executor,
    estimated_affected_rows: formState.estimated_affected_rows || 0,
    rollback_sql: formState.rollback_sql,
    content: formState.content,
    cc: [],
  }).catch(() => null)

  if (!res) {
    return message.error('保存并提交失败，请稍后再试')
  }
  message.success('工单已保存，正在进入规范预审')
  navigateAfterSave(res.data.order_id, `工单提交成功，工单号 ${res.data.order_id}，可在工单列表中查看`)
}

// 仅有提交权限（无查看权限）的用户不跳详情页（会被路由守卫拦截），提示工单号后停留
const navigateAfterSave = (orderId, fallbackMessage) => {
  if (userStore.hasPermission('order.view')) {
    router.push({ name: 'orders.detail', params: { order_id: orderId } })
  } else {
    message.info(fallbackMessage, 5)
  }
}

/* ===== 语法错误前置校验 =====
   历史上「规范/语法类问题不在第一步拦截」，结果 SQL 写错也能建单，
   产生一批必然卡在预审阶段的脏工单，用户还得「撤回 → 编辑 → 重提」三步才能改。
   语法错误是硬错误（解析不过），规范问题是软建议（可放行到第二步），阈值不同，
   这里只拦截语法错误，规范类问题仍交给第二步的规范预审。 */
const syntaxState = reactive({
  checking: false,
  message: '',
  advice: '',
})

const runPreInspect = async () => {
  const normalizedSchema = isPgLikeSelected.value ? formState.schema : formState.database
  const res = await preInspectOrderApi(
    {
      instance_id: formState.instance_id,
      db_type: formState.db_type,
      database: formState.database,
      schema: normalizedSchema,
      content: formState.content,
    },
    { silentBusinessError: true, silentHttpError: true },
  ).catch(() => null)
  return res?.data || null
}

// 返回 true 表示可以继续提交；false 表示已被语法错误拦截
const checkSyntaxBeforeSubmit = async () => {
  if (!formState.instance_id || !String(formState.content || '').trim()) return true
  syntaxState.checking = true
  const data = await runPreInspect()
  syntaxState.checking = false
  // 预审执行失败（连不上库/权限不足）时放行：这是系统问题，不应拦住用户建单
  if (!data) return true
  if (!data.has_syntax_error) {
    syntaxState.message = ''
    syntaxState.advice = ''
    return true
  }
  const item = (data.items || []).find((entry) => entry.category === 'syntax_error')
  syntaxState.message = item?.message || '当前 SQL 存在语法错误，请修正后再提交'
  syntaxState.advice = item?.advice || ''
  return false
}

// SQL 变更即清除提示：提示只反映「上一次校验」的结论，改过后不再适用
watch(
  () => formState.content,
  () => {
    if (!syntaxState.message) return
    syntaxState.message = ''
    syntaxState.advice = ''
  },
)

const handleSaveAndSubmit = useThrottleFn(async () => {
  try {
    uiState.loading = true
    if (uiState.fullscreenTarget) {
      uiState.fullscreenTarget = null
      await new Promise((r) => requestAnimationFrame(() => r(null)))
    }
    await validateForm()
    if (!ensureSqlInputs()) return

    // 语法错误在提交前拦截，避免产生必然卡在预审阶段的脏工单
    const canSubmit = await checkSyntaxBeforeSubmit()
    if (!canSubmit) {
      message.error('SQL 存在语法错误，请按提示修正后再提交')
      return
    }

    await submitOrder()
  } finally {
    uiState.loading = false
  }
})

const applyDraft = async (draft) => {
  if (!draft) return
  formState.title = draft.title || ''
  formState.remark = draft.remark || ''
  formState.db_type = draft.db_type || 'MySQL'
  formState.instance_id = draft.instance_id
  formState.database = draft.database
  formState.schema = ['postgresql', 'kingbase', 'sqlserver'].includes(String(draft.db_type || '').toLowerCase())
    ? draft.schema
    : (draft.schema || draft.database)
  formState.executor = draft.executor === 'SELF' ? userStore.username : (draft.executor || undefined)
  formState.estimated_affected_rows = typeof draft.estimated_affected_rows === 'number'
    ? draft.estimated_affected_rows
    : null
  uiState.changeSqlMode = draft.changeSqlMode || 'text'
  uiState.rollbackSqlMode = draft.rollbackSqlMode || 'text'
  uiState.changeSqlFileName = draft.changeSqlFileName || ''
  uiState.rollbackSqlFileName = draft.rollbackSqlFileName || ''
  syncEditorDialect(formState.db_type)
  if (formState.instance_id) {
    await loadDatabaseOptions(formState.instance_id)
    formState.database = draft.database
    if (isPgLikeSelected.value && formState.database) {
      await loadSchemaOptions(formState.database)
    }
    formState.schema = draft.schema
  }
  codemirrorRef.value?.setContent(draft.content || '')
  rollbackCodemirrorRef.value?.setContent(draft.rollback_sql || '')
}

watch(
  [isCreateDatabaseIntent, isCreateSchemaIntent, isPgLikeSelected],
  ([createDatabaseIntent, createSchemaIntent, pgLike]) => {
    if (createDatabaseIntent) {
      if (formState.database || formState.schema) {
        formState.database = undefined
        formState.schema = undefined
        uiData.schemaOptions = []
      }
      return
    }

    if (pgLike && createSchemaIntent) {
      if (formState.schema) {
        formState.schema = undefined
      }
      return
    }

    if (!pgLike && formState.schema) {
      formState.schema = undefined
      uiData.schemaOptions = []
    }
  },
)

const toggleFullscreen = (target) => {
  if (uiState.fullscreenTarget === target) {
    uiState.fullscreenTarget = null
  } else {
    uiState.fullscreenTarget = target
  }
  nextTick(() => {
    if (target === 'change') codemirrorRef.value?.layout?.()
    else rollbackCodemirrorRef.value?.layout?.()
  })
}

const handleGlobalEsc = (e) => {
  if (e.key !== 'Escape') return
  if (!uiState.fullscreenTarget) return
  e.preventDefault()
  uiState.fullscreenTarget = null
  nextTick(() => {
    codemirrorRef.value?.layout?.()
    rollbackCodemirrorRef.value?.layout?.()
  })
}

onMounted(async () => {
  window.addEventListener('keydown', handleGlobalEsc)
  await loadDataSources()
  await loadExecutorOptions()
  syncEditorDialect(formState.db_type)

  const orderCreatePrefillStore = useOrderCreatePrefillStore()
  const prefill = orderCreatePrefillStore.consumeCreatePrefill()
  if (prefill) {
    await applyDraft(prefill)
    await tryEnterEditMode()
    return
  }

  const editInstanceId = String(route.query?.edit_instance_id || '').trim()
  if (!editInstanceId) {
    await tryEnterEditMode()
    return
  }
  const matched = uiData.dataSources.find((item) => String(item.instance_id || item.id || '').trim() === editInstanceId)
  if (!matched) return
  formState.db_type = matched.db_type || formState.db_type
  clearInstanceCascade()
  formState.instance_id = matched.instance_id
  syncEditorDialect(formState.db_type)
  await loadDatabaseOptions(formState.instance_id)
})

// ===== 编辑模式（已撤回工单原地编辑，工单 ID 不变）=====
// 路由带 ?edit_order_id=<id> 时进入：拉取工单详情预填表单，
// 提交时走 update-content 而不是新建。
// 内容与「数据源/数据库/Schema/执行人」均可编辑（工单被撤回的常见原因就是选错了库）；
// 任务名称沿用创建后的口径保持锁定。保存后工单回到「待预审」。
const editOrderId = ref('')
let loadedEditOrderId = ''

const tryEnterEditMode = async () => {
  const targetId = String(route.query?.edit_order_id || '').trim()
  if (!targetId) return
  if (targetId === loadedEditOrderId) return
  const res = await getOrderDetailApi({ order_id: targetId }).catch(() => null)
  const detail = res?.data
  if (!detail?.order_id) {
    message.error('加载工单失败，请从工单列表重试')
    return
  }
  if (String(detail.progress || '').toUpperCase() !== 'WITHDRAWN') {
    message.warning('仅「已撤回」状态的工单支持编辑')
    return
  }
  loadedEditOrderId = targetId
  editOrderId.value = targetId
  await applyDraft({
    title: detail.title,
    remark: detail.remark,
    db_type: detail.db_type,
    instance_id: detail.instance_id,
    database: detail.database,
    schema: detail.schema,
    executor: detail.executor,
    estimated_affected_rows: typeof detail.estimated_affected_rows === 'number' ? detail.estimated_affected_rows : null,
    content: detail.content || '',
    rollback_sql: detail.rollback_sql || '',
  })
}

// keepAlive 路由：从列表「撤回并编辑」二次进入时 onMounted 不会再触发，需在 onActivated 兜底
onActivated(() => {
  tryEnterEditMode()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleGlobalEsc)
})
</script>

<style scoped>
.order-create-wrapper {
  width: 100%;
  height: 100%;
  min-height: 0;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  overflow: hidden;
  overscroll-behavior: none;
}

.order-create-wrapper.is-in-fullscreen {
  align-items: stretch;
}

.order-create-card {
  width: 100%;
  max-width: 1800px;
  height: 100%;
  min-height: 0;
  background: transparent;
  display: flex;
  flex-direction: column;
}

.order-create-wrapper.is-in-fullscreen .order-create-card {
  max-width: 100%;
}

.order-create-card :deep(.ant-card-body) {
  flex: 1;
  min-height: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}

.steps-shell {
  flex: 0 0 auto;
  margin-bottom: 16px;
  padding: 12px 20px;
  border: 1px solid #eceff5;
  border-radius: 8px;
  background: #fff;
}

.create-row {
  flex: 1;
  min-height: 0;
  margin: 0;
  row-gap: 16px;
}

.create-row :deep(.ant-col) {
  height: 100%;
  min-height: 0;
}

.is-in-fullscreen .create-row > :deep(.ant-col:first-child) {
  display: none !important;
}

.is-in-fullscreen .create-row > :deep(.ant-col:last-child) {
  flex: 0 0 100% !important;
  max-width: 100% !important;
  width: 100% !important;
}

.left-panel,
.right-panel {
  height: 100%;
  min-height: 0;
  padding: 18px 20px;
  border: 1px solid #eceff5;
  border-radius: 8px;
  background: #fff;
  display: flex;
  flex-direction: column;
}

.left-panel-scroll {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding-right: 4px;
}

.left-panel :deep(.ant-form-item) {
  margin-bottom: 10px;
}

.left-panel :deep(.ant-form-item-label) {
  padding-bottom: 4px;
}

.left-panel :deep(.ant-form-item-label > label) {
  font-size: 13px;
  height: 22px;
}

.left-panel :deep(.ant-form-item-label > label::before) {
  display: none !important;
}

.left-panel .form-item-required :deep(.ant-form-item-label > label)::before {
  display: inline-block !important;
  content: '*';
  margin-right: 4px;
  color: #ff4d4f;
  font-size: 13px;
  line-height: 1;
  font-weight: 600;
  vertical-align: middle;
}

.left-panel :deep(.ant-form-item-control) {
  min-height: 0;
}

.left-panel :deep(.ant-form-item-explain),
.left-panel :deep(.ant-form-item-explain-error),
.left-panel :deep(.ant-form-item-explain-warning),
.left-panel :deep(.ant-form-item-explain-validating),
.left-panel :deep(.ant-form-item-explain-success),
.left-panel :deep(.ant-form-item-help) {
  display: none !important;
  visibility: hidden !important;
  height: 0 !important;
  min-height: 0 !important;
  max-height: 0 !important;
  opacity: 0 !important;
  margin: 0 !important;
  padding: 0 !important;
  overflow: hidden !important;
  pointer-events: none !important;
}

.left-panel :deep(.ant-form-item) {
  margin-bottom: 18px;
}

.left-panel :deep(.ant-form-item .ant-form-item-control-input) {
  min-height: 0;
}

.left-panel :deep(.ant-form-item-has-error .ant-input),
.left-panel :deep(.ant-form-item-has-error .ant-select-selector),
.left-panel :deep(.ant-form-item-has-error .ant-input-number),
.left-panel :deep(.ant-form-item-has-error .ant-picker),
.left-panel :deep(.ant-form-item-has-error .ant-input-affix-wrapper) {
  border-color: #ff4d4f !important;
}

.inline-select-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.source-type-select {
  min-width: 0;
  flex: 3.5 1 0;
}

.source-instance-select {
  min-width: 0;
  flex: 6.5 1 0;
}

.database-select {
  min-width: 0;
  flex: 1 1 auto;
}

.field-tip {
  margin-top: 6px;
  color: #8c8c8c;
  font-size: 12px;
  line-height: 1.4;
}

.instance-unavailable-alert {
  margin-top: 10px;
  padding: 10px 12px;
  border: 1px solid var(--ant-colorBorderSecondary, #eceff5);
  border-radius: 10px;
  background: var(--ant-colorFillQuaternary, rgba(0, 0, 0, 0.03));
}

.instance-unavailable-alert :deep(.ant-alert-icon) {
  margin-top: 2px;
  color: var(--ant-colorWarning, #faad14);
}

.instance-unavailable-alert :deep(.ant-alert-message) {
  color: #1f2329;
  font-weight: 600;
  line-height: 1.4;
}

.instance-unavailable-alert :deep(.ant-alert-description) {
  margin-top: 6px;
  color: #595959;
}

.instance-unavailable-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.instance-unavailable-subtitle {
  font-size: 12px;
  line-height: 1.4;
}

.instance-unavailable-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 10px;
  margin-top: 10px;
}

.instance-unavailable-actions :deep(.ant-btn-sm) {
  height: 22px;
  padding: 0 8px;
  line-height: 20px;
}

.instance-error-link {
  padding-inline: 0;
}

.instance-unavailable-alert :deep(.order-instance-error-popover .ant-popover-inner-content) {
  padding: 0;
}

.order-instance-error-detail {
  width: 360px;
  max-width: min(360px, 70vw);
  padding: 10px 12px;
}

.order-instance-error-title {
  margin-bottom: 8px;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.88);
}

.order-instance-error-pre {
  margin: 0;
  max-height: 220px;
  overflow: auto;
  padding: 10px 12px;
  border-radius: 10px;
  background: rgba(0, 0, 0, 0.03);
  white-space: pre-wrap;
  word-break: break-word;
}

.instance-option {
  display: flex;
  flex-direction: column;
  line-height: 1.3;
}

.instance-option-remark {
  color: #1f2329;
}

.instance-option-address {
  color: #8c8c8c;
  font-size: 12px;
}

.instance-option-badge {
  margin-left: 8px;
  color: #ff4d4f;
  font-weight: 600;
}

.auto-selected-instance :deep(.ant-select-selection-item) {
  color: #bfbfbf !important;
}

.source-instance-select :deep(.ant-select-selection-item) {
  font-size: 13px;
}

.icon-button {
  flex: 0 0 auto;
}

.full-width {
  width: 100%;
}

.form-actions {
  margin-top: 6px;
  margin-bottom: 0;
}

.remark-textarea :deep(.ant-input) {
  min-height: 32px !important;
  max-height: 32px !important;
  height: 32px !important;
  line-height: 22px !important;
  padding: 4px 11px !important;
  resize: none;
  overflow-y: auto;
}

.right-panel {
  gap: 18px;
}

.sql-section {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.sql-section-change {
  flex: 6;
}

.sql-section-rollback {
  flex: 4;
}

.sql-section-header {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}

.sql-section-header-right {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.fullscreen-btn {
  width: 28px;
  height: 28px;
  padding: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 6px;
  color: #4e5969;
  transition: background-color 0.15s ease, color 0.15s ease;
}

.fullscreen-btn:hover,
.fullscreen-btn:focus {
  color: #0f8a83;
  background: rgba(15, 138, 131, 0.08);
}

.fullscreen-btn :deep(.anticon) {
  font-size: 14px;
}

.sql-title {
  font-size: 14px;
  font-weight: 600;
  color: #1f2329;
}

.sql-title.required::before {
  margin-right: 4px;
  color: #ff4d4f;
  content: '*';
}

.upload-bar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}

.upload-name {
  color: #8c8c8c;
  font-size: 12px;
}

.editor-shell {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  border: 1px solid #eceff5;
  border-radius: 6px;
  background: #fff;
  transition: box-shadow 0.2s ease;
}

.editor-shell-main,
.editor-shell-rollback {
  height: auto;
}

.is-in-fullscreen .sql-section:not(.is-fullscreen) {
  display: none !important;
}

.is-in-fullscreen .right-panel {
  gap: 0;
  padding: 18px 20px 20px;
}

.is-in-fullscreen .sql-section.is-fullscreen {
  flex: 1 1 100%;
  min-height: 0;
  height: 100%;
  padding: 14px 16px 18px;
  background: #fff;
  border: 1px solid #e5e6eb;
  border-radius: 10px;
  box-shadow:
    0 10px 30px rgba(29, 35, 41, 0.08),
    0 2px 8px rgba(29, 35, 41, 0.05);
}

.is-in-fullscreen .sql-section.is-fullscreen .editor-shell {
  border: 1px solid #e5e6eb;
  border-radius: 8px;
  box-shadow: 0 1px 3px rgba(29, 35, 41, 0.04) inset;
}

.precheck-card {
  margin-top: 16px;
}

/* 语法错误内联提示条：紧贴编辑器下方，就近原则 */
.sql-syntax-alert {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-top: 8px;
  padding: 8px 12px;
  border: 1px solid #ffccc7;
  border-radius: 6px;
  background: #fff2f0;
}

.sql-syntax-alert-text {
  font-size: 13px;
  line-height: 1.5;
  color: #cf1322;
  word-break: break-word;
}

.sql-syntax-alert-advice {
  font-size: 12px;
  line-height: 1.5;
  color: #d46b08;
  word-break: break-word;
}

.inspect-wrapper {
  margin-top: 16px;
}

@media (max-width: 992px) {
  .order-create-card {
    max-width: 100%;
  }

  .create-row :deep(.ant-col) {
    height: auto;
    min-height: 0;
  }

  .left-panel,
  .right-panel {
    padding: 16px;
    height: auto;
    min-height: 0;
  }

  .left-panel-scroll {
    overflow: visible;
    padding-right: 0;
  }

  .right-panel {
    gap: 16px;
  }

  .sql-section-change,
  .sql-section-rollback {
    flex: none;
  }

  .editor-shell-main {
    height: 300px;
  }

  .editor-shell-rollback {
    height: 250px;
  }
}

@media (max-width: 1200px) {
  .order-create-card {
    max-width: 100%;
  }
}

@media (max-width: 768px) {
  /* 5 段水平步骤条在窄屏必然挤压 */
  .steps-shell {
    padding: 10px 12px;
    margin-bottom: 10px;
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

  /* 类型 + 实例 + 刷新并排，窄屏实例名会被截断 */
  .inline-select-row {
    flex-wrap: wrap;
    row-gap: 8px;
  }

  .source-type-select {
    flex: 1 1 calc(50% - 30px);
  }

  .source-instance-select {
    flex: 1 1 calc(50% + 6px);
  }

  .database-select {
    flex: 1 1 calc(100% - 40px);
  }

  .left-panel,
  .right-panel {
    padding: 12px;
  }

  .upload-bar {
    flex-wrap: wrap;
    row-gap: 6px;
  }

  .editor-shell-main {
    height: 220px;
  }

  .editor-shell-rollback {
    height: 180px;
  }
}
</style>
