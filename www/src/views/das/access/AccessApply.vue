<template>
  <div class="access-apply-page gi-page-shell">
    <a-card class="apply-card" :bordered="false">
      <template #title>
        <div class="card-header">
          <div class="card-title">新建数据访问申请</div>
        </div>
      </template>
      <template #extra>
        <a-button @click="goBack">返回列表</a-button>
      </template>

      <div class="steps-shell">
        <a-steps :current="step" size="small">
          <a-step title="填写访问信息" />
          <a-step title="指定审批人" />
          <a-step title="提交完成" />
        </a-steps>
      </div>

      <div class="apply-body">
        <!-- 提交成功：不再回到空白表单，直接把单号与下一步动作给出来 -->
        <div v-if="done" class="apply-done">
          <a-result
            status="success"
            title="申请已提交"
            :sub-title="`申请单号 ${doneRequestNo}，等待 ${createdApprover || '审批人'} 审批`"
          >
            <template #extra>
              <a-button type="primary" @click="goDetail(doneRequestNo)">查看申请详情</a-button>
              <a-button @click="resetForm">再发起一单</a-button>
              <a-button @click="goBack">返回列表</a-button>
            </template>
          </a-result>
        </div>

        <template v-else>
          <!-- ====== 第 1 步：访问信息 ====== -->
          <div v-show="step === 0" class="step-pane">
            <div v-if="fromRequestNo" class="prefill-hint">
              <InfoCircleOutlined class="prefill-icon" />
              <span>
                已按申请单 <span class="mono-text">{{ fromRequestNo }}</span> 的内容预填，可按需修改后重新提交。
              </span>
            </div>

            <a-form ref="infoFormRef" layout="vertical" :model="form" :rules="rules">
              <a-form-item label="目标数据源" name="instance_id">
                <a-select
                  v-model:value="form.instance_id"
                  show-search
                  :options="instanceOptions"
                  :filter-option="filterInstanceOption"
                  :loading="instanceLoading"
                  placeholder="按名称 / 地址 / 类型搜索"
                  :not-found-content="instanceLoading ? '加载中…' : '暂无可申请的数据源'"
                  @change="onInstanceChange"
                />
              </a-form-item>

              <!-- 层级是「几选一」而不是筛选器。选「库 / 模式 / 表」后进入授权清单：
                   把对象逐条加入清单，每条独立配置授权方式，一单可混搭
                   「db1 整库 + db2 指定表 + db3 通配」。 -->
              <a-form-item label="申请范围" name="scope">
                <div class="choice-cards">
                  <button
                    v-for="item in SCOPE_OPTIONS"
                    :key="item.value"
                    type="button"
                    class="choice-card"
                    :class="{ 'is-active': form.scope === item.value }"
                    @click="onScopeChange(item.value)"
                  >
                    {{ item.label }}
                  </button>
                </div>
              </a-form-item>

              <template v-if="form.scope === 'list'">
                <a-form-item label="选择资源">
                  <div class="entry-picker">
                    <a-select
                      v-model:value="picker.databases"
                      mode="multiple"
                      show-search
                      option-filter-prop="label"
                      :options="databaseOptions"
                      :loading="schemaLoading"
                      :placeholder="schemaLoading ? '加载中…' : '选择库（可多选）'"
                      not-found-content="该数据源下暂无库"
                      :max-tag-count="3"
                      class="entry-picker__select"
                      @change="onPickerDatabaseChange"
                    />
                    <a-tooltip title="重新从目标数据源采集库清单（刚建的库点这里就能看到）">
                      <a-button :loading="metaRefreshing" :disabled="!form.instance_id" @click="refreshSchemaRows">
                        <template #icon>
                          <ReloadOutlined />
                        </template>
                      </a-button>
                    </a-tooltip>
                    <a-select
                      v-if="hasSchemaLevel"
                      v-model:value="picker.schemas"
                      mode="multiple"
                      show-search
                      option-filter-prop="label"
                      :options="pickerSchemaOptions"
                      :disabled="!picker.databases.length"
                      :placeholder="picker.databases.length ? '选择模式（可多选，整库加入则留空）' : '请先选择库'"
                      not-found-content="所选库下暂无模式"
                      :max-tag-count="2"
                      class="entry-picker__select"
                    />
                    <a-button type="primary" @click="addEntries">加入清单</a-button>
                  </div>
                </a-form-item>

                <a-form-item label="授权清单" required>
                  <div v-if="!form.entries.length" class="entry-empty">
                    请先选择库 / 模式，并点击「加入清单」
                  </div>
                  <div v-else class="entry-list">
                    <div v-for="entry in form.entries" :key="entry.key" class="entry-card">
                      <div class="entry-card__head">
                        <div class="entry-card__meta">
                          <div class="entry-card__title">{{ entry.container }}</div>
                          <div class="entry-card__sub">
                            {{ entry.kind === 'schema' ? '模式' : hasSchemaLevel ? '库（整库）' : '库' }}
                          </div>
                        </div>
                        <a-button type="link" danger @click="removeEntry(entry.key)">移除</a-button>
                      </div>

                      <div class="entry-card__body">
                        <div v-if="entry.fixed" class="entry-fixed-hint">
                          整库授权：该库下全部资源（含今后新建的模式与表）均可查询。
                        </div>
                        <template v-else>
                          <div class="entry-row">
                            <div class="entry-row__label">授权方式</div>
                            <div class="choice-cards">
                              <button
                                v-for="item in entryModeOptions(entry)"
                                :key="item.value"
                                type="button"
                                class="choice-card choice-card--small"
                                :class="{ 'is-active': entry.mode === item.value }"
                                @click="onEntryModeChange(entry, item.value)"
                              >
                                {{ item.label }}
                              </button>
                            </div>
                          </div>

                          <div v-if="entry.mode === 'list'" class="entry-row">
                            <div class="entry-row__label">指定表</div>
                            <a-select
                              v-model:value="entry.tables"
                              mode="multiple"
                              show-search
                              option-filter-prop="label"
                              :options="entryTableOptions(entry)"
                              :loading="tableLoading && !tableOptionCache[entry.container]"
                              :placeholder="tableLoadFailed ? '获取表清单失败，请检查数据源连接' : '选择表（可多选，选一张即单表授权）'"
                              not-found-content="该库/模式下暂无表"
                              :max-tag-count="5"
                              @dropdown-visible-change="(open) => open && ensureTableOptions([entry])"
                            />
                          </div>

                          <div v-if="entry.mode === 'wildcard'" class="entry-row">
                            <div class="entry-row__label">通配符</div>
                            <a-input
                              v-model:value="entry.pattern"
                              placeholder="如 order_*（* 任意字符序列，? 单个字符）"
                              :maxlength="128"
                              allow-clear
                            />
                          </div>

                          <div v-if="entry.mode === 'except'" class="entry-row">
                            <div class="entry-row__label">排除表</div>
                            <a-select
                              v-model:value="entry.except"
                              mode="multiple"
                              show-search
                              option-filter-prop="label"
                              :options="entryTableOptions(entry)"
                              :loading="tableLoading && !tableOptionCache[entry.container]"
                              :placeholder="tableLoadFailed ? '获取表清单失败，请检查数据源连接' : '勾选要排除的表'"
                              not-found-content="该库/模式下暂无表"
                              :max-tag-count="5"
                              @dropdown-visible-change="(open) => open && ensureTableOptions([entry])"
                            />
                          </div>

                          <div class="entry-summary">{{ entrySummary(entry) }}</div>
                        </template>
                      </div>
                    </div>
                  </div>
                </a-form-item>
              </template>

              <a-form-item label="有效期" name="expire_days">
                <div class="choice-cards">
                  <button
                    v-for="item in expireOptions"
                    :key="item.value"
                    type="button"
                    class="choice-card"
                    :class="{ 'is-active': form.expire_days === item.value }"
                    @click="form.expire_days = item.value"
                  >
                    {{ item.label }}
                  </button>
                </div>
              </a-form-item>

              <a-form-item label="申请事由" name="reason">
                <a-textarea
                  v-model:value="form.reason"
                  :rows="3"
                  :maxlength="512"
                  show-count
                  placeholder="说明为什么需要访问该数据源对象，审批人将据此判断是否授权"
                />
              </a-form-item>
            </a-form>
          </div>

          <!-- ====== 第 2 步：指定审批人 ====== -->
          <div v-show="step === 1" class="step-pane">
            <a-form ref="approverFormRef" layout="vertical" :model="form" :rules="rules">
              <a-form-item
                label="审批人"
                name="approver_username"
              >
                <a-select
                  v-model:value="form.approver_username"
                  show-search
                  option-filter-prop="label"
                  :options="approverOptions"
                  :loading="approverLoading"
                  placeholder="按账号搜索"
                  :not-found-content="approverLoading ? '加载中…' : '暂无具备审批权限的账号'"
                />
              </a-form-item>
            </a-form>

            <a-descriptions size="small" :column="2" bordered class="summary-desc">
              <a-descriptions-item label="数据源">{{ summary.instanceText }}</a-descriptions-item>
              <a-descriptions-item label="申请范围">{{ scopeText }}</a-descriptions-item>
              <a-descriptions-item v-if="isListScope" label="资源范围" :span="2">
                <div class="scope-lines">
                  <div v-for="line in scopeLines" :key="line">{{ line }}</div>
                </div>
              </a-descriptions-item>
              <a-descriptions-item label="申请权限">查询（SELECT）</a-descriptions-item>
              <a-descriptions-item label="有效期">{{ accessExpireLabel(form.expire_days) }}</a-descriptions-item>
              <a-descriptions-item label="申请事由" :span="2">{{ form.reason || '-' }}</a-descriptions-item>
            </a-descriptions>
          </div>

          <div class="apply-footer">
            <a-button v-if="step === 1" @click="step = 0">上一步</a-button>
            <a-button v-if="step === 0" type="primary" @click="goStep2">下一步</a-button>
            <a-button v-else type="primary" :loading="submitting" @click="submit">提交申请</a-button>
          </div>
        </template>
      </div>
    </a-card>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { message } from 'ant-design-vue'
import { InfoCircleOutlined, ReloadOutlined } from '@ant-design/icons-vue'
import { useUserStore } from '@/store/user'
import {
  CreateAccessRequestApi,
  GetAccessApproverOptionsApi,
  GetAccessInstanceOptionsApi,
  GetAccessRequestDetailApi,
  GetAccessSchemaOptionsApi,
  GetAccessTableOptionsApi,
  RefreshInstanceMetaApi,
} from '@/api/das'
import {
  ACCESS_EXPIRE_DEFAULT,
  ACCESS_EXPIRE_FOREVER,
  ACCESS_EXPIRE_OPTIONS,
  accessExpireLabel,
  parseAccessResourceSpec,
} from './accessRequestMeta'

defineOptions({ name: 'DasAccessApply' })

// 申请范围：整个实例 / 库·模式·表授权清单。
// 清单模式下每条条目独立配置授权方式（参考「数据授权」弹窗的实现方式），
// 一单可混搭「db1 整库 + db2 指定表 + db3 通配」。
const SCOPE_OPTIONS = [
  { label: '整个实例', value: 'instance' },
  { label: '库 / 模式 / 表', value: 'list' },
]

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()

const userName = computed(() => String(userStore.username || ''))

// 类似创建：带上原单号，加载原单内容预填
const fromRequestNo = computed(() => String(route.query.from || '').trim())

const step = ref(0)
const submitting = ref(false)
const done = ref(false)
const doneRequestNo = ref('')
const createdApprover = ref('')

const expireOptions = ACCESS_EXPIRE_OPTIONS

const form = reactive({
  instance_id: undefined,
  scope: 'instance',
  // 授权清单：每条 = { key, kind, database, schema, container, mode, tables, pattern, except, fixed }
  // kind=database 单层结构可带表圈选；三层结构的整库条目 fixed=true（只能整个库）。
  entries: [],
  expire_days: ACCESS_EXPIRE_DEFAULT,
  reason: '',
  approver_username: undefined,
})

// 清单「添加行」的临时选择状态（不随表单提交）
const picker = reactive({ databases: [], schemas: [] })

const isListScope = computed(() => form.scope === 'list')

const infoFormRef = ref(null)
const approverFormRef = ref(null)

const rules = {
  instance_id: [{ required: true, message: '请选择目标数据源' }],
  reason: [{ required: true, message: '请填写申请事由' }],
  approver_username: [{ required: true, message: '请选择审批人' }],
}

// ---------------------------------------------------------------------------
// 下拉取数：申请入口的三个接口不走授权过滤（申请人本来就还没权限）
// ---------------------------------------------------------------------------
const instanceLoading = ref(false)
const instanceOptions = ref([])
const instanceLabelMap = reactive({})
// 实例的派生元信息：has_schema_level 由服务端下发（PostgreSQL / SQL Server 为 true），
// 决定清单里能不能加「模式」条目。前端不按 db_type 自己 switch，避免两边口径漂移。
const instanceMetaMap = reactive({})

// 下拉与回显里实例名称的最大展示宽度（字符数），超长截断加省略号
const INSTANCE_NAME_MAX = 20
const truncateInstanceName = (name) => {
  const text = String(name || '')
  return text.length > INSTANCE_NAME_MAX ? `${text.slice(0, INSTANCE_NAME_MAX)}…` : text
}

const filterInstanceOption = (input, option) => {
  const keyword = String(input || '').trim().toLowerCase()
  if (!keyword) return true
  return String(option.searchText || '').includes(keyword)
}

const loadInstanceOptions = async () => {
  instanceLoading.value = true
  try {
    const res = await GetAccessInstanceOptionsApi().catch(() => null)
    instanceOptions.value = (res?.data || []).map((item) => {
      const suffix = `（${item.db_type || '-'} · ${item.addr || '-'}）`
      // 名称超长时截断加省略号，括号里的类型 / IP 始终完整展示
      const label = `${truncateInstanceName(item.name)}${suffix}`
      // 搜索走完整名称，不受截断影响
      const searchText = `${item.name}${suffix}`.toLowerCase()
      instanceLabelMap[item.instance_id] = `${item.name}${suffix}`
      instanceMetaMap[item.instance_id] = {
        dbType: item.db_type || '',
        hasSchemaLevel: Boolean(item.has_schema_level),
      }
      return { value: item.instance_id, label, searchText }
    })
  } finally {
    instanceLoading.value = false
  }
}

// ---------------------------------------------------------------------------
// 元数据（库 / 模式）：三层结构的数据源在 insight_instance_schemas 里有两类行：
//   resource_type='database' → schema=库名，parent_path=''
//   resource_type='schema'   → schema='库.模式'，parent_path=库名，display_name=模式名
// 单层结构（MySQL 系）只有 database 行。按 resource_type 拆开才能做到「先库后模式」。
// ---------------------------------------------------------------------------
const schemaLoading = ref(false)
const schemaRows = ref([])
// 手动重采库清单时的 loading：一次 SQLServer 同步要按库逐个查 schema，
// 不给反馈的话用户会以为按钮没生效。
const metaRefreshing = ref(false)

// 授权路径里取库名（"库.模式" 两段式）
const databaseOfPath = (path) => {
  const text = String(path || '')
  const idx = text.indexOf('.')
  return idx > 0 ? text.slice(0, idx) : text
}

const loadSchemaRows = async () => {
  if (!form.instance_id) {
    schemaRows.value = []
    return
  }
  schemaLoading.value = true
  try {
    const res = await GetAccessSchemaOptionsApi({ instance_id: form.instance_id }).catch(() => null)
    schemaRows.value = res?.data || []
  } finally {
    schemaLoading.value = false
  }
}

// 手动刷新：先让后端去目标实例真正重采一次库清单，再读快照。
// 只重读快照是没用的——快照没过期时新建的库怎么点都看不到。
const refreshSchemaRows = async () => {
  if (!form.instance_id || metaRefreshing.value) return
  metaRefreshing.value = true
  try {
    const res = await RefreshInstanceMetaApi(form.instance_id).catch((err) => {
      message.error(String(err?.message || err?.data?.message || '刷新库列表失败，请检查数据源连接状态').trim())
      return null
    })
    if (!res) return
    await loadSchemaRows()
    picker.databases = picker.databases.filter((db) => databaseOptions.value.some((opt) => opt.value === db))
  } finally {
    metaRefreshing.value = false
  }
}

// 是否有独立的「模式」层级：优先用服务端下发的 has_schema_level，
// 缺失时按元数据里是否存在 schema 行兜底。
const hasSchemaLevel = computed(() => {
  if (!form.instance_id) return false
  const meta = instanceMetaMap[form.instance_id]
  if (meta) return meta.hasSchemaLevel
  return schemaRows.value.some((row) => String(row.resource_type || '').toLowerCase() === 'schema')
})

const databaseOptions = computed(() => {
  const rows = schemaRows.value
  const dbRows = rows.filter((row) => String(row.resource_type || '').toLowerCase() === 'database')
  const source = dbRows.length ? dbRows : rows
  const seen = new Set()
  const out = []
  for (const row of source) {
    // 有 database 行时用它的 schema（库名）；只有 schema 行时退化成 parent_path
    const value = dbRows.length
      ? String(row.schema || '')
      : String(row.parent_path || row.schema || '')
    if (!value || seen.has(value)) continue
    seen.add(value)
    out.push({ value, label: String(row.display_name || '') || value })
  }
  return out
})

// 添加行的模式选项：所选库下全部模式，value=「库.模式」完整路径（跨库唯一）。
// 选了多个库时 label 带库前缀，避免 db1.public 与 db2.public 无法区分。
const pickerSchemaOptions = computed(() => {
  const dbs = picker.databases || []
  if (!dbs.length) return []
  const dbSet = new Set(dbs)
  return schemaRows.value
    .filter(
      (row) =>
        String(row.resource_type || '').toLowerCase() === 'schema' &&
        dbSet.has(String(row.parent_path || '')),
    )
    .map((row) => {
      const path = String(row.schema || '')
      const leaf = String(row.display_name || '') || path.slice(path.indexOf('.') + 1)
      return { value: path, label: dbs.length > 1 ? path : leaf }
    })
    .filter((item) => item.value)
})

// ---------------------------------------------------------------------------
// 授权清单：条目构建 / 添加 / 移除 / 逐条配置
// ---------------------------------------------------------------------------

let entrySeq = 0

const buildEntry = ({ kind, database, schema = '', mode = 'all', tables = [], pattern = '', except = [] }) => {
  const container = kind === 'schema' ? String(schema || '') : String(database || '')
  const fixed = kind === 'database' && hasSchemaLevel.value
  entrySeq += 1
  return {
    key: `${kind}|${container}|${entrySeq}`,
    kind,
    database: String(database || ''),
    schema: String(schema || ''),
    container,
    // 三层结构的整库条目只能「整个库」，不允许表圈选
    mode: fixed ? 'all' : mode || 'all',
    tables: [...tables],
    pattern: String(pattern || ''),
    except: [...except],
    fixed,
  }
}

const addEntries = () => {
  const dbs = picker.databases || []
  if (!dbs.length) {
    message.warning('请先选择库')
    return
  }
  const incoming = []
  if (hasSchemaLevel.value && (picker.schemas || []).length) {
    for (const path of picker.schemas) {
      incoming.push(buildEntry({ kind: 'schema', database: databaseOfPath(path), schema: path }))
    }
  } else {
    for (const db of dbs) {
      incoming.push(buildEntry({ kind: 'database', database: db }))
    }
  }
  // 同对象只保留一条（key 前两段 = 类型|容器）
  const containerKey = (entry) => `${entry.kind}|${entry.container}`
  const existing = new Set(form.entries.map(containerKey))
  const added = []
  let dup = 0
  for (const entry of incoming) {
    const key = containerKey(entry)
    if (existing.has(key)) {
      dup += 1
      continue
    }
    existing.add(key)
    added.push(entry)
  }
  if (!added.length) {
    message.info(dup ? `所选资源均已在清单中（${dup} 条）` : '没有可加入的资源')
    return
  }
  form.entries.push(...added)
  picker.schemas = []
  message.success(`已加入清单 ${added.length} 条${dup ? `，重复跳过 ${dup} 条` : ''}`)
}

const removeEntry = (key) => {
  form.entries = form.entries.filter((entry) => entry.key !== key)
}

// 每种对象可用的授权方式：整个对象 / 指定表 / 通配符 / 排除表
const entryModeOptions = (entry) => [
  { value: 'all', label: entry.kind === 'schema' ? '整个模式' : '整个库' },
  { value: 'list', label: '指定表' },
  { value: 'wildcard', label: '通配符' },
  { value: 'except', label: '排除表' },
]

const onEntryModeChange = (entry, mode) => {
  if (entry.fixed || entry.mode === mode) return
  entry.mode = mode
  entry.tables = []
  entry.except = []
  entry.pattern = ''
  if (mode === 'list' || mode === 'except') ensureTableOptions([entry])
}

const entrySummary = (entry) => {
  if (entry.fixed || entry.mode === 'all') {
    return entry.kind === 'schema' ? '整个模式只读（含今后新建的表）' : '整个库只读（含今后新建的表）'
  }
  if (entry.mode === 'wildcard') {
    return `表名匹配 ${String(entry.pattern || '').trim() || '-'}`
  }
  if (entry.mode === 'except') {
    return `除 ${entry.except.join('、') || '-'} 外的全部表`
  }
  return `指定表：${entry.tables.join('、') || '-'}`
}

// ---------------------------------------------------------------------------
// 表清单：按容器缓存（一次会话不重复连库），条目切到指定表 / 排除表时按需拉取
// ---------------------------------------------------------------------------
const tableLoading = ref(false)
const tableLoadFailed = ref(false)
const tableOptionCache = reactive({})

const entryTableOptions = (entry) =>
  (tableOptionCache[entry.container] || []).map((row) => ({
    value: row.name,
    label: row.comment ? `${row.name}（${row.comment}）` : row.name,
  }))

const ensureTableOptions = async (entries) => {
  if (!form.instance_id) return
  const pending = [
    ...new Set(
      entries
        .filter((entry) => !entry.fixed && (entry.mode === 'list' || entry.mode === 'except'))
        .map((entry) => entry.container),
    ),
  ].filter((container) => container && !tableOptionCache[container])
  if (!pending.length) return
  tableLoading.value = true
  try {
    const results = await Promise.all(
      pending.map((container) =>
        GetAccessTableOptionsApi({ instance_id: form.instance_id, schema: container })
          .then((res) => {
            tableOptionCache[container] = (res?.data || []).map((item) => ({
              name: item.name,
              comment: item.comment || '',
            }))
            return true
          })
          .catch(() => false),
      ),
    )
    if (results.some((ok) => !ok)) tableLoadFailed.value = true
  } finally {
    tableLoading.value = false
  }
}

const onPickerDatabaseChange = () => {
  // 模式必须仍挂在所选库下：换库后把失去归属的模式清掉
  const dbSet = new Set(picker.databases || [])
  picker.schemas = (picker.schemas || []).filter((path) => dbSet.has(String(path).split('.')[0]))
}

const onScopeChange = (value) => {
  form.scope = value
}

const onInstanceChange = () => {
  // 换数据源：清单、选择状态、元数据、表清单缓存全部作废
  picker.databases = []
  picker.schemas = []
  form.entries = []
  schemaRows.value = []
  // 表清单按容器缓存，换实例后必须清掉，否则会串到另一个数据源
  Object.keys(tableOptionCache).forEach((key) => delete tableOptionCache[key])
  tableLoadFailed.value = false
  if (form.instance_id) loadSchemaRows()
}

const approverLoading = ref(false)
const approverOptions = ref([])

// 审批人候选：只返回持有 das.access.approve 的账号，转交与新建共用同一个接口。
// 排除自己——自批自审在服务端一律拦截，下拉里就不该出现自己。
const loadApproverOptions = async () => {
  approverLoading.value = true
  try {
    const res = await GetAccessApproverOptionsApi().catch(() => null)
    approverOptions.value = (res?.data || [])
      .filter((item) => String(item.username) !== userName.value)
      .map((item) => ({ value: item.username, label: item.name || item.username }))
  } finally {
    approverLoading.value = false
  }
}

// ---------------------------------------------------------------------------
// 步骤流转
// ---------------------------------------------------------------------------
const summary = computed(() => ({
  instanceText: instanceLabelMap[form.instance_id] || form.instance_id || '-',
}))

const scopeText = computed(() =>
  isListScope.value ? `授权清单（${form.entries.length} 项）` : '整个实例',
)

const scopeLines = computed(() =>
  form.entries.map((entry) => `${entry.container}：${entrySummary(entry)}`),
)

// 清单校验：确认清单非空，且每条表级圈选都填了内容
const validateEntries = () => {
  if (!form.entries.length) {
    message.warning('请先把要申请的库 / 模式加入授权清单')
    return false
  }
  for (const entry of form.entries) {
    if (entry.fixed || entry.mode === 'all') continue
    if (entry.mode === 'list' && !entry.tables.length) {
      message.warning(`请为 ${entry.container} 选择要授权的表`)
      return false
    }
    if (entry.mode === 'wildcard' && !String(entry.pattern || '').trim()) {
      message.warning(`请为 ${entry.container} 填写表名匹配规则`)
      return false
    }
    if (entry.mode === 'except' && !entry.except.length) {
      message.warning(`请为 ${entry.container} 勾选要排除的表`)
      return false
    }
  }
  return true
}

const goStep2 = async () => {
  try {
    await infoFormRef.value?.validate()
  } catch {
    return
  }
  if (isListScope.value && !validateEntries()) return
  step.value = 1
  if (!approverOptions.value.length) loadApproverOptions()
}

// 服务端 success 文案多数是 "ok" 占位值，不能直接弹给用户看
const serverMsg = (res, fallback) => {
  const text = String(res?.message || res?.msg || '').trim()
  return text && text !== 'ok' ? text : fallback
}

// 清单派生申请层级（与服务端 derivedAccessResourceLevel 同口径）：表 > 模式 > 库
const derivedResourceLevel = () => {
  const entries = form.entries || []
  if (entries.some((entry) => !entry.fixed && entry.mode !== 'all')) return 'table'
  if (entries.some((entry) => entry.kind === 'schema')) return 'schema'
  return 'database'
}

const submit = async () => {
  try {
    await approverFormRef.value?.validate()
  } catch {
    return
  }
  submitting.value = true
  try {
    const payload = {
      instance_id: form.instance_id,
      resource_level: isListScope.value ? derivedResourceLevel() : 'instance',
      // 申请入口只受理查询，这里恒为 select（服务端仍有白名单校验兜底）
      actions: 'select',
      expire_days: form.expire_days,
      reason: String(form.reason || '').trim(),
      approver_username: form.approver_username,
    }
    if (isListScope.value) {
      payload.entries = form.entries.map((entry) => ({
        kind: entry.kind,
        database: entry.database,
        schema: entry.kind === 'schema' ? entry.schema : '',
        table_mode: entry.fixed || entry.mode === 'all' ? '' : entry.mode,
        tables: entry.mode === 'list' ? [...entry.tables] : [],
        pattern: entry.mode === 'wildcard' ? String(entry.pattern || '').trim() : '',
        except: entry.mode === 'except' ? [...entry.except] : [],
      }))
    }
    const res = await CreateAccessRequestApi(payload).catch(() => null)
    if (!res) return
    createdApprover.value = form.approver_username || ''
    doneRequestNo.value = String(res?.data?.request_no || '')
    done.value = true
    step.value = 2
    message.success(serverMsg(res, '申请已提交，等待审批'))
  } finally {
    submitting.value = false
  }
}

const resetForm = () => {
  done.value = false
  doneRequestNo.value = ''
  createdApprover.value = ''
  step.value = 0
  form.instance_id = undefined
  form.scope = 'instance'
  form.entries = []
  form.expire_days = ACCESS_EXPIRE_DEFAULT
  form.reason = ''
  form.approver_username = undefined
  picker.databases = []
  picker.schemas = []
  schemaRows.value = []
  Object.keys(tableOptionCache).forEach((key) => delete tableOptionCache[key])
  tableLoadFailed.value = false
  // 清掉 ?from=，否则「再发起一单」会被预填数据反复覆盖
  router.replace({ path: '/datasource/access/apply' })
}

const goBack = () => router.push('/datasource/access')
const goDetail = (requestNo) => {
  if (!requestNo) {
    goBack()
    return
  }
  router.push(`/datasource/access/${requestNo}`)
}

// ---------------------------------------------------------------------------
// 类似创建预填：resource_spec 三代格式都兼容
//   v3 entries（清单）/ v1 多资源（databases/schemas/...）/ 旧三列单资源
// ---------------------------------------------------------------------------
const prefillFromRequest = async (requestNo) => {
  const res = await GetAccessRequestDetailApi({ request_no: requestNo }).catch(() => null)
  const req = res?.data?.request
  if (!req) {
    message.warning('原申请单内容读取失败，请手动填写')
    return
  }
  form.instance_id = req.instance_id || undefined
  form.scope = req.resource_level === 'instance' ? 'instance' : 'list'
  // 先拉元数据，hasSchemaLevel 才能定下来（决定清单里能不能放模式条目）
  if (form.scope === 'list') await loadSchemaRows()

  const spec = parseAccessResourceSpec(req.resource_spec)
  const list = []
  const pushEntries = (items) => {
    const seen = new Set(list.map((entry) => `${entry.kind}|${entry.container}`))
    for (const entry of items) {
      const key = `${entry.kind}|${entry.container}`
      if (!entry.container || seen.has(key)) continue
      seen.add(key)
      list.push(entry)
    }
  }

  if (spec && Array.isArray(spec.entries) && spec.entries.length) {
    // v3 清单格式：直接还原
    pushEntries(
      spec.entries.map((item) =>
        buildEntry({
          kind: item.kind === 'schema' ? 'schema' : 'database',
          database: item.database,
          schema: item.schema || '',
          mode: item.table_mode || 'all',
          tables: item.tables || [],
          pattern: item.pattern || '',
          except: item.except || [],
        }),
      ),
    )
  } else if (spec) {
    // v1 多资源格式 → 按容器拆成清单条目
    const schemas = spec.schemas || []
    const databases = spec.databases || []
    if (!schemas.length && databases.length) {
      pushEntries(databases.map((db) => buildEntry({ kind: 'database', database: db })))
    } else if (schemas.length && req.resource_level !== 'table') {
      pushEntries(
        schemas.map((path) =>
          buildEntry({ kind: 'schema', database: databaseOfPath(path), schema: path }),
        ),
      )
    } else {
      const mode = spec.table_mode || 'list'
      for (const container of schemas) {
        const prefix = `${container}.`
        pushEntries([
          buildEntry({
            kind: hasSchemaLevel.value ? 'schema' : 'database',
            database: databaseOfPath(container),
            schema: hasSchemaLevel.value ? container : '',
            mode,
            tables: (spec.tables || []).filter((t) => t.startsWith(prefix)).map((t) => t.slice(prefix.length)),
            pattern: spec.pattern || '',
            except: (spec.except || []).filter((t) => t.startsWith(prefix)).map((t) => t.slice(prefix.length)),
          }),
        ])
      }
    }
  } else {
    // 旧三列单资源
    const db = String(req.database || '') || databaseOfPath(req.schema_name)
    const level = String(req.resource_level || '')
    if (level === 'table' && req.table_name) {
      const container = String(req.schema_name || db || '')
      if (container) {
        pushEntries([
          buildEntry({
            kind: hasSchemaLevel.value ? 'schema' : 'database',
            database: databaseOfPath(container),
            schema: hasSchemaLevel.value ? container : '',
            mode: 'list',
            tables: [String(req.table_name)],
          }),
        ])
      }
    } else if (level === 'schema' && req.schema_name) {
      pushEntries([
        buildEntry({
          kind: 'schema',
          database: databaseOfPath(req.schema_name),
          schema: String(req.schema_name),
        }),
      ])
    } else if (db) {
      pushEntries([buildEntry({ kind: 'database', database: db })])
    }
  }
  form.entries = list

  // 指定表 / 排除表条目需要表清单才能回显，这里预热一次
  if (form.entries.length) ensureTableOptions(form.entries)

  form.reason = req.reason || ''
  // 有效期不继承原单的天数：「原单还剩多少天」折算不出意义（原单可能已过期），
  // 但「长期」是明确意图，必须继承，否则类似创建会静默把永久授权收窄成 6 个月
  form.expire_days = req.requested_expire_at ? ACCESS_EXPIRE_DEFAULT : ACCESS_EXPIRE_FOREVER
  // 审批人也预填原单的处理人，申请人通常还是找同一个人
  form.approver_username = req.approver_username || undefined
}

onMounted(async () => {
  // 实例选项要先到：has_schema_level 决定清单里能不能加「模式」条目
  await loadInstanceOptions()
  loadApproverOptions()
  if (fromRequestNo.value) await prefillFromRequest(fromRequestNo.value)
})
</script>

<style scoped>
.access-apply-page {
  height: 100%;
  width: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
}

.apply-card {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  overflow: hidden;
}

.apply-card :deep(.ant-card-body) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 0;
  overflow: auto;
}

.card-header {
  display: flex;
  align-items: center;
  gap: 16px;
  min-height: 40px;
}

.card-title {
  font-weight: 600;
  white-space: nowrap;
}

/* 步骤条与表单之间不加分隔线：去掉卡片头下边框，步骤条自身也不画下边框 */
.apply-card :deep(.ant-card-head) {
  border-bottom: none;
}

.steps-shell {
  padding: 12px 20px;
}

.apply-body {
  flex: 1 1 auto;
  min-height: 0;
  padding: 20px;
}

/* 表单不通栏：宽屏下单行控件拉满会让阅读距离过长 */
.step-pane {
  max-width: 720px;
}

.prefill-hint {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-bottom: 16px;
  padding: 8px 12px;
  border-radius: 6px;
  background: var(--gi-color-fill-quaternary, #f5f7f8);
  font-size: 13px;
  line-height: 1.6;
  color: var(--gi-color-text-secondary);
}

.prefill-icon {
  margin-top: 3px;
  color: var(--gi-color-primary, #0f766e);
  flex: 0 0 auto;
}

.mono-text {
  font-family: var(--gi-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 12px;
}

/* 三选一/五选一都用独立方框：连体的 radio-button 看起来像分段控件，
   容易被理解成「可以同时选」，也不便于以后单独加说明文案 */
.choice-cards {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.choice-card {
  min-width: 104px;
  height: 32px;
  padding: 0 14px;
  border: 1px solid var(--gi-color-border, #d9d9d9);
  border-radius: 6px;
  background: var(--gi-color-bg-container, #fff);
  color: var(--gi-color-text, rgb(0 0 0 / 88%));
  font-family: inherit;
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
  transition: border-color 0.2s, color 0.2s, background 0.2s;
}

.choice-card--small {
  min-width: 0;
  height: 28px;
  padding: 0 10px;
  font-size: 12px;
}

.choice-card:hover {
  border-color: var(--gi-color-primary, #0f766e);
  color: var(--gi-color-primary, #0f766e);
}

.choice-card.is-active {
  border-color: var(--gi-color-primary, #0f766e);
  color: var(--gi-color-primary, #0f766e);
  background: rgb(15 118 110 / 6%);
  font-weight: 600;
}

.choice-card:focus-visible {
  outline: 2px solid rgb(15 118 110 / 35%);
  outline-offset: 1px;
}

/* 授权清单 */
.entry-picker {
  display: flex;
  gap: 8px;
  align-items: flex-start;
}

.entry-picker__select {
  flex: 1;
  min-width: 0;
}

.entry-empty {
  padding: 14px 16px;
  border: 1px dashed var(--gi-color-border, #d9d9d9);
  border-radius: 8px;
  color: var(--gi-color-text-secondary);
  background: var(--gi-color-fill-quaternary, #fafafa);
}

.entry-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.entry-card {
  border: 1px solid rgb(235, 237, 240);
  border-radius: 10px;
  background: var(--gi-color-fill-quaternary, #fafafa);
}

.entry-card__head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid rgb(235, 237, 240);
}

.entry-card__title {
  color: var(--gi-color-text, rgb(0 0 0 / 88%));
  font-weight: 600;
  font-family: var(--gi-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 13px;
  word-break: break-all;
}

.entry-card__sub {
  margin-top: 2px;
  color: var(--gi-color-text-secondary);
  font-size: 12px;
}

.entry-card__body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 12px 16px 14px;
}

.entry-row__label {
  margin-bottom: 6px;
  color: var(--gi-color-text-secondary);
  font-weight: 500;
  font-size: 12px;
}

.entry-fixed-hint {
  color: var(--gi-color-text-secondary);
  font-size: 12px;
  line-height: 1.6;
}

.entry-summary {
  padding: 8px 12px;
  border: 1px solid rgb(235, 237, 240);
  border-radius: 6px;
  background: var(--gi-color-bg-container, #fff);
  color: var(--gi-color-text-secondary);
  font-size: 12px;
  line-height: 1.6;
  word-break: break-all;
}

.summary-desc {
  margin-top: 8px;
}

.summary-desc :deep(.ant-descriptions-item-label) {
  width: 92px;
}

.scope-lines {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 12px;
  line-height: 1.6;
  word-break: break-all;
}

.apply-footer {
  margin-top: 24px;
  padding-top: 16px;
  border-top: 1px solid rgb(235, 237, 240);
  display: flex;
  align-items: center;
  gap: 8px;
  max-width: 720px;
}

.apply-done {
  max-width: 720px;
}

@media (max-width: 768px) {
  .apply-body {
    padding: 12px;
  }

  .entry-picker {
    flex-direction: column;
    align-items: stretch;
  }

  .apply-footer {
    flex-direction: column-reverse;
    align-items: stretch;
  }

  .apply-footer :deep(.ant-btn) {
    width: 100%;
  }
}
</style>
