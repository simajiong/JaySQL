<template>
  <div class="param-page" ref="pageRef">
    <!-- 顶部：模式切换 + 类型/实例下拉选择 -->
    <a-card size="small" class="inst-bar">
      <div class="inst-row">
        <a-radio-group v-model:value="mode" button-style="solid" @change="handleModeChange">
          <a-radio-button value="single">单实例</a-radio-button>
          <a-radio-button value="compare">多实例对比</a-radio-button>
        </a-radio-group>
        <div class="head-tools">
          <a-select
            v-model:value="filterType"
            class="labeled-select labeled-type"
            style="width: 170px"
            allow-clear
            placeholder="类型"
            @change="handleTypeChange"
          >
            <a-select-option v-for="t in typeOptions" :key="t.value" :value="t.value" :label="t.label">
              <div class="type-option">
                <img :src="dbTypeLogo(t.value)" :alt="t.label" class="type-logo" />
                <span>{{ t.label }}</span>
              </div>
            </a-select-option>
          </a-select>
          <a-select
            v-if="mode === 'single'"
            v-model:value="currentInstanceId"
            class="labeled-select labeled-name"
            style="width: 340px"
            show-search
            option-filter-prop="label"
            placeholder="名称"
            @change="loadParams"
          >
            <a-select-option
              v-for="inst in visibleInstances"
              :key="inst.instance_id"
              :value="inst.instance_id"
              :label="instanceOptionLabel(inst)"
            >
              <div class="inst-option">
                <span class="inst-name" :title="inst.remark || inst.hostname">{{ truncate20(inst.remark || inst.hostname) }}</span>
                <span class="inst-addr" :title="`${inst.hostname}:${inst.port}`">{{ truncate20(inst.hostname) }}:{{ inst.port }}</span>
              </div>
            </a-select-option>
          </a-select>
          <a-select
            v-else
            v-model:value="compareIds"
            class="name-select"
            style="width: 420px"
            mode="multiple"
            :max-tag-count="3"
            :label-render="renderNameTag"
            placeholder="名称"
            @change="handleCompareChange"
          >
            <a-select-option
              v-for="inst in visibleInstances"
              :key="inst.instance_id"
              :value="inst.instance_id"
              :label="truncate15(inst.remark || inst.hostname)"
            >
              <div class="inst-option">
                <span class="inst-name" :title="inst.remark || inst.hostname">{{ truncate20(inst.remark || inst.hostname) }}</span>
                <span class="inst-addr" :title="`${inst.hostname}:${inst.port}`">{{ truncate20(inst.hostname) }}:{{ inst.port }}</span>
              </div>
            </a-select-option>
          </a-select>
          <a-input-search
            v-model:value="filters.search"
            placeholder="搜索参数名…"
            style="width: 240px"
            allowClear
          />
          <a-checkbox v-if="mode === 'compare'" v-model:checked="onlyDiff">只看差异</a-checkbox>
          <!-- 展开/收起与统计卡仅在单实例模式提供（对比模式删除，见下方 stats-row） -->
          <a-button v-if="mode === 'single'" type="text" class="collapse-btn" :style="{ color: colorTokens.primary }" @click="statsCollapsed = !statsCollapsed">
            {{ statsCollapsed ? '展开' : '收起' }}
          </a-button>
        </div>
      </div>
        <!-- 统计卡：仅单实例模式显示；v-show 隐藏时不占布局高度，对比模式无残留空白。
             （变更流水入口已删，参数级历史仍走操作列「历史」按钮） -->
        <div v-show="mode === 'single' && !statsCollapsed" class="stats-row">
          <div class="stat"><span class="num">{{ filteredParams.length }}</span><span class="lbl">参数总数</span></div>
          <div class="stat"><span class="num">{{ modifiedCount }}</span><span class="lbl">已偏离默认值</span></div>
          <div class="stat"><span class="num">{{ restartCount }}</span><span class="lbl">需重启生效</span></div>
          <div class="stat"><span class="num">{{ highRiskCount }}</span><span class="lbl">高危参数</span></div>
        </div>
      </a-card>

    <!-- 参数表 -->
    <a-card size="small" class="table-card" ref="tableCardRef">
      <a-table
        :columns="tableColumns"
        :data-source="tableRows"
        :loading="loading"
        :pagination="pagination"
        :scroll="{ y: tableScrollY }"
        row-key="name"
        size="small"
        table-layout="fixed"
        @change="handleTableChange"
      >
        <template #headerCell="{ column }">
          <template v-if="String(column.key || '').startsWith('inst_')">
            <div class="inst-col-head">
              <span class="inst-col-name" :title="column.instName">{{ truncate15(column.instName) }}</span>
              <span class="inst-col-addr" :title="`${column.instIp}:${column.instPort}`">{{ truncate15(column.instIp) }}:{{ column.instPort }}</span>
            </div>
          </template>
        </template>
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'name'">
            <span class="mono pname">{{ record.name }}</span>
          </template>
          <template v-else-if="column.key === 'current'">
            <span class="mono">{{ displayValue(record, 'current_value') }}</span>
            <span v-if="isModified(record)" class="plain-mark">已修改</span>
            <span v-if="record.pending_restart" class="plain-mark">待重启</span>
          </template>
          <template v-else-if="column.key === 'default'">
            <span class="mono muted">{{ record.default_value || '—' }}</span>
          </template>
          <template v-else-if="column.key === 'effect'">
            <span>{{ effectLabel(record) }}</span>
          </template>
          <template v-else-if="column.key === 'data_type'">
            <span>{{ record.data_type || '—' }}</span>
          </template>
          <template v-else-if="column.key === 'description'">
            <a-tooltip :title="record.description"><span class="desc">{{ record.description || '—' }}</span></a-tooltip>
          </template>
          <template v-else-if="column.key === 'action'">
            <!-- 需重启生效的参数不允许修改（后端同口径拦截），修改/历史置灰 -->
            <a-button
              v-if="canModify && mode === 'single'"
              type="link" size="small" class="action-link"
              :disabled="isRestartParam(record)"
              :style="isRestartParam(record) ? {} : { color: colorTokens.primary }"
              @click="openModify(record)"
            >修改</a-button>
            <a-button
              type="link" size="small" class="action-link"
              :disabled="isRestartParam(record)"
              :style="isRestartParam(record) ? {} : { color: colorTokens.primary }"
              @click="openChanges(record.name)"
            >历史</a-button>
          </template>
          <template v-else-if="column.key.startsWith('inst_')">
            <span class="mono">{{ record.values[column.key.slice(5)] ?? '—' }}</span>
          </template>
        </template>
      </a-table>
    </a-card>

    <!-- 修改弹窗 -->
    <a-modal
      v-model:open="modifyUi.open"
      :title="`修改参数 · ${currentInstanceLabel}`"
      :confirm-loading="modifyUi.saving"
      ok-text="确认修改"
      cancel-text="取消"
      @ok="submitModify"
    >
      <template v-if="modifyUi.record">
        <a-descriptions :column="1" size="small" bordered class="mb12">
          <a-descriptions-item label="参数名"><span class="mono">{{ modifyUi.record.name }}</span></a-descriptions-item>
          <a-descriptions-item label="当前值">
            <span class="mono">{{ modifyUi.record.current_value }}</span>
          </a-descriptions-item>
          <a-descriptions-item label="默认值"><span class="mono muted">{{ modifyUi.record.default_value || '—' }}</span></a-descriptions-item>
          <a-descriptions-item label="含义说明">{{ modifyUi.record.description || '—' }}</a-descriptions-item>
        </a-descriptions>
        <a-alert v-if="modifyUi.record.risk === 'high'" type="error" show-icon class="mb12"
          message="高危参数"
          description="修改可能导致实例性能下降甚至不可用（连接耗尽、内存溢出、主从异常等）。请先在测试环境验证，并安排低峰期执行。" />
        <a-alert v-else-if="modifyUi.record.risk === 'mid'" type="warning" show-icon class="mb12"
          message="该参数存在一定风险，建议评估后修改。" />
        <a-form layout="vertical">
          <a-form-item label="新值" required>
            <a-input v-model:value="modifyUi.newValue" class="mono" placeholder="请输入新值" />
          </a-form-item>
          <a-form-item v-if="modifyUi.record.risk === 'high'">
            <a-checkbox v-model:checked="modifyUi.ackRisk">我已评估该参数变更风险，确认在当前环境执行</a-checkbox>
          </a-form-item>
          <a-form-item label="变更原因（必填，写入审计日志）" required>
            <a-input v-model:value="modifyUi.reason" placeholder="例如：缓冲池命中率偏低，低峰期调整" />
          </a-form-item>
        </a-form>
      </template>
    </a-modal>

    <!-- 历史/变更流水抽屉 -->
    <a-drawer v-model:open="historyUi.open" :title="historyUi.title" width="520" destroy-on-close>
      <a-spin :spinning="historyUi.loading">
        <a-timeline v-if="historyUi.list.length" class="history-tl">
          <a-timeline-item v-for="item in historyUi.list" :key="item.id"
            :color="item.exec_result === 'success' ? 'green' : item.exec_result === 'failed' ? 'red' : 'gray'">
            <div class="h-head">
              <span class="mono">{{ item.param_name }}</span>
              <span class="mono vals">{{ item.old_value }} → {{ item.new_value }}</span>
            </div>
            <div class="h-meta">
              {{ item.operator }} · {{ formatTime(item.created_at) }} ·
              <a-tag :color="item.exec_result === 'success' ? 'green' : item.exec_result === 'failed' ? 'red' : 'gray'" class="ml0">
                {{ execResultLabel(item) }}
              </a-tag>
              <a-tag v-if="item.effective === 'pending_restart' || item.effective === 'restart'" color="orange">待重启生效</a-tag>
              <a-tag v-else-if="item.effective === 'new_conn'" color="blue">新连接生效</a-tag>
            </div>
            <div class="h-msg" v-if="item.exec_message">{{ item.exec_message }}</div>
            <div class="h-reason">原因：{{ item.reason }}</div>
            <a-button
              v-if="canModify && item.exec_result === 'success' && mode === 'single'"
              type="link" size="small" class="pl0"
              @click="rollback(item)"
            >回滚到 {{ item.old_value }}</a-button>
          </a-timeline-item>
        </a-timeline>
        <a-empty v-else description="暂无修改记录" />
      </a-spin>
    </a-drawer>
  </div>
</template>

<script setup>
import { computed, h, inject, nextTick, onActivated, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { message } from 'ant-design-vue'
import {
  diffInstanceParamsApi,
  getParamChangesApi,
  getInstanceParamsApi,
  getPerformanceInstancesApi,
  modifyInstanceParamApi,
  rollbackParamApi,
} from '@/api/performance'
import { useUserStore } from '@/store/user'
import { dbTypeLogo } from '@/utils/dbTypeLogo'
import { colorTokens } from '@/design/tokens'
import dayjs from 'dayjs'

const userStore = useUserStore()
const canModify = computed(() => userStore.hasPermission('performance.param.modify'))
// 满高页面：内容区切 overflow:hidden，页面级不出现滚动条（退出时恢复）
const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)

/* ===== 实例选择 ===== */
const mode = ref('single')
const instances = ref([])
const currentInstanceId = ref('')
const compareIds = ref([])
// 参数管理支持的数据库类型（顺序即下拉展示顺序）
// Kingbase 走 PG 通道，参数采集/修改复用 collectPostgresParams 一系实现
const TYPE_ORDER = ['Oracle', 'MySQL', 'SQLServer', 'PostgreSQL', 'Kingbase', 'TiDB']
const filterType = ref(undefined)

// 类型选项按「当前实例清单里实际存在的 db_type」动态生成：
// 不存在的类型不出现在下拉里，避免用户选出空结果
const typeOptions = computed(() => {
  const present = new Set(instances.value.map((i) => i.db_type))
  return TYPE_ORDER.filter((t) => present.has(t)).map((t) => ({ value: t, label: t }))
})

// 名称下拉只展示所选类型下的实例
const visibleInstances = computed(() =>
  filterType.value ? instances.value.filter((i) => i.db_type === filterType.value) : [],
)

// 20 字符截断：超过用 ... 表示（对比模式列头用 15 字符口径）
const truncate20 = (s) => {
  const v = String(s || '')
  return v.length > 20 ? v.slice(0, 20) + '...' : v
}
const truncate15 = (s) => {
  const v = String(s || '')
  return v.length > 15 ? v.slice(0, 15) + '...' : v
}

const instanceOptionLabel = (inst) =>
  `${truncate20(inst.remark || inst.hostname)} (${truncate20(inst.hostname)}:${inst.port})`

const currentInstanceLabel = computed(() => {
  const inst = instances.value.find((i) => i.instance_id === currentInstanceId.value)
  // 名称与 IP 各 20 字符截断，超长用 ... 表示
  return inst ? `${truncate20(inst.remark || inst.hostname)} (${truncate20(inst.hostname)}:${inst.port})` : ''
})

// 「实例管理」列表默认排序：created_at desc, id desc（后端 AdminGetInstancesView 口径）。
// 该接口只返回性能分析可见的实例，故在此按同一排序取「排序第一」的实例作为默认选中项。
const pickFirstInstance = (list) => {
  const ordered = [...list].sort((a, b) => {
    const ta = a.created_at ? new Date(a.created_at).getTime() : 0
    const tb = b.created_at ? new Date(b.created_at).getTime() : 0
    return tb - ta
  })
  return ordered[0] || null
}

const loadInstances = async () => {
  // supported_only 必须为 false：服务端该过滤是慢 SQL 采集口径（MySQL/PG/Oracle），
  // 会把参数管理已支持的 SQLServer（collectSQLServerParams）/TiDB（走 MySQL family）误滤掉；
  // 类型白名单由下方前端过滤兜底
  const res = await getPerformanceInstancesApi({ supported_only: false }).catch(() => null)
  // 请求失败时不动已有选择（避免把用户当前实例清空），交给上层提示
  if (!res) return
  const list = (res?.data?.instances || []).filter((i) => TYPE_ORDER.includes(i.db_type))
  instances.value = list
  if (!list.length) {
    // 无实例：清空类型/名称与已加载参数，避免残留上一次的选择
    filterType.value = undefined
    currentInstanceId.value = undefined
    paramList.value = []
    pagination.total = 0
    return
  }
  // 初始化时回填默认选中项（实例管理列表排序第一）。实例清单刷新后若当前选中实例已不可见，
  // 同样回落到默认项；用户已有的有效选择不覆盖。
  const stillVisible = list.some((i) => i.instance_id === currentInstanceId.value)
  if (!stillVisible) {
    const first = pickFirstInstance(list)
    filterType.value = first.db_type
    currentInstanceId.value = first.instance_id
  } else if (!filterType.value) {
    // 类型被清空但实例仍有效时，把类型补齐为实例自身类型，保持两个下拉口径一致
    filterType.value = list.find((i) => i.instance_id === currentInstanceId.value)?.db_type
  }
}

// 类型切换：单实例模式自动选中该类型下排序第一的实例；类型被清空时连已选实例与参数列表一起清掉
const handleTypeChange = () => {
  if (mode.value === 'single') {
    const first = pickFirstInstance(visibleInstances.value)
    if (first && first.instance_id !== currentInstanceId.value) {
      currentInstanceId.value = first.instance_id
      loadParams()
    }
    if (!first) {
      currentInstanceId.value = undefined
      paramList.value = []
      pagination.total = 0
    }
  } else {
    if (filterType.value) {
      compareIds.value = compareIds.value.filter((id) =>
        visibleInstances.value.some((i) => i.instance_id === id),
      )
    } else {
      compareIds.value = []
    }
    loadDiff()
  }
}

// 对比模式多选：最多 3 个实例
const handleCompareChange = (ids) => {
  if (ids.length > 3) {
    compareIds.value = ids.slice(0, 3)
    message.warning('最多同时对比 3 个实例')
  }
  loadDiff()
}

const handleModeChange = () => {
  if (mode.value === 'compare') {
    // 默认带入当前实例 + 同类型的下一个实例（未选实例时留空，由用户勾选）
    compareIds.value = currentInstanceId.value ? [currentInstanceId.value] : []
    const current = instances.value.find((i) => i.instance_id === currentInstanceId.value)
    const sibling = instances.value.find((i) => i.db_type === current?.db_type && i.instance_id !== currentInstanceId.value)
    if (sibling) compareIds.value.push(sibling.instance_id)
    if (compareIds.value.length >= 2) loadDiff()
    else diffResult.value = null
  } else {
    loadParams()
  }
}

/* ===== 参数加载 ===== */
const loading = ref(false)
const paramList = ref([])
const diffResult = ref(null)

const filters = reactive({ search: '' })
const onlyDiff = ref(false)
// 统计卡默认收起，展开时显示四个统计卡
const statsCollapsed = ref(true)
// 表体滚动高度：实测表格卡片可用空间（窗口缩放/展开收起时重算），表头与分页固定可见
const tableCardRef = ref(null)
const pageRef = ref(null)
let pageResizeObserver = null
const tableScrollY = ref(400)

const updateScrollY = () => {
  const card = tableCardRef.value?.$el || tableCardRef.value
  if (!card) return
  // 直接实测「卡片顶边 → 视口底」的剩余空间，不依赖父级高度链：
  // 公告条、头部行加高等任何动态布局变化都能被 ResizeObserver/resize 捕捉后重算。
  // 注意 scroll.y 只约束表体：总高 = 表头 + 表体(scrollY) + 分页，表头高度必须单独扣除
  const rect = card.getBoundingClientRect()
  const viewportBottomPad = 24 // layout-content 底部 padding
  const cardPad = 16 + 2       // 卡片 body 上下 padding（底部 2px，与分页下方留白一致）
  const headerEl = card.querySelector('.ant-table-header') || card.querySelector('.ant-table-thead')
  const headerH = headerEl ? headerEl.offsetHeight : 39
  const paginationEl = card.querySelector('.ant-pagination')
  const paginationH = paginationEl ? paginationEl.offsetHeight : 60 // offsetHeight 已含与表格的 6px 间距（padding-top）
  const extra = 4
  const available = window.innerHeight - rect.top - viewportBottomPad - cardPad - headerH - paginationH - extra
  tableScrollY.value = available > 280 ? available : 280
}
const pagination = reactive({ current: 1, pageSize: 100, total: 0, showTotal: (t) => `共 ${t} 条` })

const loadParams = async () => {
  if (!currentInstanceId.value) return
  loading.value = true
  const res = await getInstanceParamsApi({ instance_id: currentInstanceId.value }).catch(() => null)
  loading.value = false
  if (!res) return message.error('参数查询失败，请检查实例连接')
  paramList.value = res.data?.params || []
  pagination.total = filteredParams.value.length
  pagination.current = 1
}

const loadDiff = async () => {
  if (compareIds.value.length < 2) {
    diffResult.value = null
    return
  }
  loading.value = true
  const res = await diffInstanceParamsApi({ instance_ids: compareIds.value.join(',') }).catch(() => null)
  loading.value = false
  if (!res) return message.error('对比查询失败')
  diffResult.value = res.data
}

/* ===== 本地筛选 ===== */
const isModified = (record) => {
  const def = record.default_value
  return def !== '' && def != null && String(record.current_value) !== String(def)
}

const filteredParams = computed(() => {
  let list = paramList.value
  if (filters.search) list = list.filter((p) => p.name.toLowerCase().includes(filters.search.toLowerCase()))
  if (activeFilters.effect.length) list = list.filter((p) => activeFilters.effect.includes(effectKey(p)))
  if (activeFilters.data_type.length) list = list.filter((p) => activeFilters.data_type.includes(p.data_type))
  return list
})

const modifiedCount = computed(() => filteredParams.value.filter(isModified).length)
const restartCount = computed(() => filteredParams.value.filter((p) => p.need_restart).length)
const highRiskCount = computed(() => filteredParams.value.filter((p) => p.risk === 'high').length)

const handleTableChange = (pg, f) => {
  pagination.current = pg.current
  pagination.pageSize = pg.pageSize
  activeFilters.effect = f?.effect || []
  activeFilters.data_type = f?.data_type || []
}

/* ===== 表格列 ===== */
// 列头过滤：生效方式 / 类型（antd 不自过滤，统一在 handleTableChange 收口后作用于全量数据）
const EFFECT_FILTERS = [
  { text: '需重启', value: 'restart' },
  { text: '立即生效', value: 'immediate' },
  { text: '新连接生效', value: 'new_conn' },
]

const effectKey = (record) => {
  // PG 等家族 effect 直接是 'restart'（context=postmaster），不能只看 need_restart
  if (record.need_restart || record.effect === 'restart') return 'restart'
  if (record.effect === 'immediate') return 'immediate'
  return 'new_conn'
}

// 需重启生效的参数不允许在线修改（后端同口径拦截），修改/历史置灰禁用
const isRestartParam = (record) => effectKey(record) === 'restart'

const activeFilters = reactive({ effect: [], data_type: [] })

// 参数名完整显示：不设固定宽、不做省略；类型过滤项从当前数据动态取
const singleColumns = computed(() => {
  const dataTypeOptions = [...new Set(paramList.value.map((p) => p.data_type).filter(Boolean))]
    .map((t) => ({ text: t, value: t }))
  return [
    { title: '参数名', key: 'name' },
    { title: '当前值', key: 'current', width: 190 },
    { title: '默认值', key: 'default', width: 140 },
    {
      title: '生效方式', key: 'effect', width: 110,
      filters: EFFECT_FILTERS,
      filteredValue: activeFilters.effect.length ? activeFilters.effect : null,
    },
    {
      title: '类型', key: 'data_type', width: 100,
      filters: dataTypeOptions,
      filteredValue: activeFilters.data_type.length ? activeFilters.data_type : null,
    },
    { title: '含义说明', key: 'description', ellipsis: true },
    { title: '操作', key: 'action', width: 130, fixed: 'right' },
  ]
})

const displayValue = (record, field) => record[field] ?? '—'

const effectLabel = (record) =>
  ({ restart: '需重启', immediate: '立即生效', new_conn: '新连接生效' })[effectKey(record)]

const tableColumns = computed(() => {
  // singleColumns 本身是 computed，必须取 .value，否则 :columns 拿到 ref 对象导致表格不渲染
  if (mode.value === 'single') return singleColumns.value
  // 对比模式：所有列不设固定宽（table-layout=fixed 下平均分），列头走 headerCell 自定义渲染
  const cols = [{ title: '参数名', key: 'name' }]
  diffInstanceIds.value.forEach((id) => {
    const inst = instances.value.find((i) => i.instance_id === id)
    cols.push({
      key: `inst_${id}`,
      instName: inst?.remark || inst?.hostname || id,
      instIp: inst?.hostname || '',
      instPort: inst?.port ?? '',
    })
  })
  cols.push({ title: '生效方式', key: 'effect' })
  return cols
})

const diffInstanceIds = computed(() => (diffResult.value?.instances || []).map((i) => i.instance_id))

// 对比模式多选框的选中 tag：名称 15 字截断，完整名称（含 ip:port）放 title 悬停查看
// 对比模式 tag：按选择顺序显示「实例1/实例2/实例3」，悬停 title 查看完整「名称 (ip:port)」
const renderNameTag = ({ value }) => {
  const idx = compareIds.value.indexOf(value)
  const inst = instances.value.find((i) => i.instance_id === value)
  const full = inst ? instanceOptionLabel(inst) : ''
  const text = idx >= 0 ? `实例${idx + 1}` : full
  return h('span', { class: 'name-tag-text', title: full }, text)
}

const tableRows = computed(() => {
  if (mode.value === 'single') {
    const list = filteredParams.value
    pagination.total = list.length
    const start = (pagination.current - 1) * pagination.pageSize
    return list.slice(start, start + pagination.pageSize)
  }
  let rows = (diffResult.value?.rows || []).map((r) => ({ ...r }))
  if (filters.search) rows = rows.filter((r) => r.name.toLowerCase().includes(filters.search.toLowerCase()))
  if (onlyDiff.value) rows = rows.filter((r) => r.diff)
  pagination.total = rows.length
  return rows
})

/* ===== 修改弹窗 ===== */
const modifyUi = reactive({ open: false, saving: false, record: null, newValue: '', reason: '', ackRisk: false })

const openModify = (record) => {
  modifyUi.record = record
  modifyUi.newValue = String(record.current_value ?? '')
  modifyUi.reason = ''
  modifyUi.ackRisk = false
  modifyUi.open = true
}

const submitModify = async () => {
  const record = modifyUi.record
  if (!record) return
  const newValue = String(modifyUi.newValue || '').trim()
  if (!newValue) return message.warning('新值不能为空')
  if (newValue === String(record.current_value)) return message.warning('新值与当前值相同')
  if (!modifyUi.reason.trim()) return message.warning('变更原因为必填项（审计要求）')
  if (record.risk === 'high' && !modifyUi.ackRisk) return message.warning('高危参数需勾选风险确认')
  modifyUi.saving = true
  const res = await modifyInstanceParamApi({
    instance_id: currentInstanceId.value,
    param_name: record.name,
    new_value: newValue,
    reason: modifyUi.reason.trim(),
    ack_risk: modifyUi.ackRisk,
  }).catch(() => null)
  modifyUi.saving = false
  if (!res) return
  modifyUi.open = false
  const data = res.data || {}
  message.success(`参数已修改（${data.exec_message || '成功'}）。${data.verify_hint || ''}`, 6)
  loadParams()
}

/* ===== 历史 / 回滚 ===== */
const historyUi = reactive({ open: false, loading: false, title: '变更流水', list: [] })

const openChanges = async (paramName) => {
  historyUi.open = true
  historyUi.loading = true
  historyUi.title = paramName ? `修改历史 · ${paramName}` : '参数变更流水'
  const params = paramName ? { instance_id: currentInstanceId.value, param_name: paramName } : {}
  const res = await getParamChangesApi(params).catch(() => null)
  historyUi.loading = false
  historyUi.list = res?.data || []
}

const rollback = async (item) => {
  const res = await rollbackParamApi({ change_id: item.id }).catch(() => null)
  if (!res) return
  message.success(`已回滚：${item.param_name} → ${item.old_value}`)
  openChanges(item.param_name)
  if (mode.value === 'single') loadParams()
}

const execResultLabel = (item) =>
  ({ success: '成功', failed: '失败', unknown: '待核实' })[item.exec_result] || item.exec_result

const formatTime = (t) => (t ? dayjs(t).format('YYYY-MM-DD HH:mm:ss') : '—')

onMounted(() => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(true)
  }
  loadInstances().then(loadParams)
  nextTick(updateScrollY)
  window.addEventListener('resize', updateScrollY)
  // 公告条/侧栏等兄弟布局变化不会触发 window.resize，用 ResizeObserver 兜底。
  // 观察表格卡片自身（而非页面根）：页面根 height:100% 恒定不变不会触发回调，
  // 而对比模式头部 tag 换行撑高头部 → 表格卡被 flex 压缩 → 卡片尺寸变化即触发重算
  const cardEl = tableCardRef.value?.$el || tableCardRef.value
  if (cardEl && typeof ResizeObserver !== 'undefined') {
    pageResizeObserver = new ResizeObserver(() => updateScrollY())
    pageResizeObserver.observe(cardEl)
  }
})
onActivated(() => {
  if (!instances.value.length) {
    loadInstances().then(loadParams)
  }
  nextTick(updateScrollY)
})
onBeforeUnmount(() => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(false)
  }
  window.removeEventListener('resize', updateScrollY)
  pageResizeObserver?.disconnect()
  pageResizeObserver = null
})
watch([statsCollapsed, mode], () => nextTick(updateScrollY))
</script>

<style scoped>
/* 满高布局：整页装进视口，页面级不滚动；表格卡片弹性占满剩余空间 */
.param-page { display: flex; flex-direction: column; gap: 12px; height: 100%; min-height: 0; overflow: hidden; }
.table-card { flex: 1 1 auto; display: flex; flex-direction: column; min-height: 0; overflow: hidden; }
.table-card :deep(.ant-card-body) { flex: 1 1 auto; display: flex; flex-direction: column; min-height: 0; padding: 16px 16px 2px 16px; }
.table-card :deep(.ant-spin-nested-loading),
.table-card :deep(.ant-spin-container) { flex: 1 1 auto; min-height: 0; display: flex; flex-direction: column; }
/* 关键：a-table 的根元素是 .ant-table-wrapper（.ant-spin-container 的直接子元素、
   真正被 flex 拉伸的容器）。wrapper 必须自身是 flex 列容器，分页的 margin-top:auto
   才能生效——否则 wrapper 拉高后剩余空白全部堆在分页下方（此前空白过大的真根因） */
.table-card :deep(.ant-spin-container) > .ant-table-wrapper { flex: 1 1 auto; min-height: 0; display: flex; flex-direction: column; }
.table-card :deep(.ant-table-wrapper) > .ant-table { flex: 0 0 auto; }
/* 分页吸底：margin-top:auto 吸收 wrapper 内全部剩余空白，数据未撑满/大屏时
   分页也钉在卡片底边（不依赖数据量与屏幕高度）；与表格保持 6px 最小间距 */
.table-card :deep(.ant-pagination) { flex-shrink: 0; margin-top: auto !important; margin-bottom: 0 !important; padding-top: 6px; }
.inst-row { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
/* 第一行右侧工具组：类型/名称/搜索/展开 靠右对齐 */
.head-tools { margin-left: auto; display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
/* 第一行控件整体加高：24px → 36px（+50%），内部元素比例不变。
   注意 height:36px 只钉单选框：多选框（对比模式名称）必须 auto + min-height，
   否则 tag 换行时冲出边框（tag 不占布局高度，还会连带分页被裁） */
.inst-row :deep(.ant-select-single .ant-select-selector) { height: 36px !important; }
.inst-row :deep(.ant-select-single .ant-select-selector .ant-select-selection-item),
.inst-row :deep(.ant-select-single .ant-select-selector .ant-select-selection-placeholder) { line-height: 34px !important; }
.inst-row :deep(.ant-select-single .ant-select-selector .ant-select-selection-search) { height: 34px; }
/* 对比模式多选框：随 tag 换行自动增高（上限 3 个实例，最多两行），内容垂直居中 */
.inst-row :deep(.ant-select-multiple .ant-select-selector) {
  height: auto !important; min-height: 36px;
  padding-top: 4px; padding-bottom: 4px;
  align-content: center;
}
.inst-row :deep(.ant-radio-button-wrapper) { height: 36px !important; line-height: 34px !important; }
.inst-row :deep(.ant-input-affix-wrapper) { height: 36px !important; padding-top: 0; padding-bottom: 0; }
.inst-row :deep(.ant-btn) { height: 36px; padding-top: 0; padding-bottom: 0; }
/* 字段名进框：类型 / 名称 常驻前缀 */
.labeled-select { position: relative; }
.labeled-select :deep(.ant-select-selector) { position: relative; padding-left: 48px !important; }
.labeled-select :deep(.ant-select-selection-search) { left: 48px !important; }
.labeled-select :deep(.ant-select-selector)::before {
  position: absolute; left: 12px; top: 0; z-index: 1;
  display: flex; align-items: center; height: 100%;
  color: #8f959e; font-size: 13px;
}
.labeled-type :deep(.ant-select-selector)::before { content: '类型'; }
.labeled-name :deep(.ant-select-selector)::before { content: '名称'; }
/* 类型下拉：20px 品牌 logo + 文案 */
.type-option { display: flex; align-items: center; gap: 8px; }
.type-logo { width: 20px; height: 20px; object-fit: contain; flex-shrink: 0; }
/* 实例下拉：名称 + 淡灰色 ip:port */
.inst-option { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.inst-option .inst-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.inst-option .inst-addr { color: #a9aeb8; font-size: 12px; white-space: nowrap; }
/* 对比模式列头：名称 + 淡灰色 ip:port（各 15 字符截断） */
.inst-col-head { display: flex; flex-direction: column; gap: 2px; line-height: 1.4; }
.inst-col-head .inst-col-name { font-weight: 600; }
.inst-col-head .inst-col-addr { color: #a9aeb8; font-size: 12px; font-weight: 400; }
/* 统计卡：与搜索框同高（32px）、数字无色、随行宽拉伸 */
.inst-bar .stats-row { margin-top: 10px; }
.stats-row { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.stat {
  display: flex; align-items: center; gap: 8px;
  height: 32px; padding: 0 16px;
  background: #fff; border: 1px solid #e5e6eb; border-radius: 6px;
  flex: 1 1 auto; min-width: fit-content; white-space: nowrap;
}
.stat .num { font-size: 16px; font-weight: 700; color: #1f2329; }
.stat .lbl { font-size: 12px; color: #646a73; white-space: nowrap; }
.mono { font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 13px; }
.muted { color: #8f959e; }
.pname { font-weight: 400; }
.plain-mark { margin-left: 8px; font-size: 12px; color: #8f959e; }
.desc { color: #646a73; font-size: 12px; }
.ml0 { margin-left: 0; }
.pl0 { padding-left: 0; }
.mb12 { margin-bottom: 12px; }
/* 对比模式多选框：tag 文本超宽省略号截断，完整名称悬停 title 查看 */
.name-select :deep(.ant-select-selection-item) { max-width: 100%; overflow: hidden; }
.name-select :deep(.ant-select-selection-item-content) {
  display: inline-block; max-width: 100%;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap; vertical-align: bottom;
}
.name-select :deep(.name-tag-text) { display: inline-block; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.history-tl :deep(.h-head) { display: flex; gap: 10px; align-items: baseline; flex-wrap: wrap; }
.history-tl :deep(.vals) { color: #1f2329; font-weight: 600; }
.history-tl :deep(.h-meta) { font-size: 12px; color: #8f959e; margin: 2px 0; display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.history-tl :deep(.h-msg) { font-size: 12px; color: #646a73; }
.history-tl :deep(.h-reason) { font-size: 12px; color: #646a73; background: #f5f6f7; border-radius: 6px; padding: 4px 8px; margin-top: 4px; }
</style>
