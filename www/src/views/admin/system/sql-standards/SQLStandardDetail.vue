<template>
  <div class="standard-detail-page">
    <a-page-header @back="handleBack">
      <template #title>
        <span class="standard-title" :title="standard.name || 'SQL 规范详情'">
          {{ standard.name || 'SQL 规范详情' }}
        </span>
      </template>
    </a-page-header>

    <a-card :bordered="false" :body-style="{ padding: '0' }">
      <a-tabs v-model:activeKey="activeTab">
        <template #tabBarExtraContent>
          <a-space class="rule-filter-bar" :size="8" align="center">
            <a-select
              v-model:value="filters.status"
              class="filter-select filter-select-status"
              :options="statusOptions"
              :dropdown-match-select-width="false"
              dropdown-class-name="rule-filter-dropdown"
              :dropdown-style="{ minWidth: '116px' }"
            />
            <a-select
              v-model:value="filters.dbType"
              class="filter-select filter-select-db"
              :options="dbTypeOptions"
              :dropdown-match-select-width="false"
              dropdown-class-name="rule-filter-dropdown"
              :dropdown-style="{ minWidth: '132px' }"
            />
            <a-select
              v-model:value="filters.ruleType"
              class="filter-select filter-select-type"
              :options="ruleTypeOptions"
              :dropdown-match-select-width="false"
              dropdown-class-name="rule-filter-dropdown"
              :dropdown-style="{ minWidth: '116px' }"
            />
            <a-select
              v-model:value="filters.level"
              class="filter-select filter-select-level"
              :options="levelOptions"
              :dropdown-match-select-width="false"
              dropdown-class-name="rule-filter-dropdown"
              :dropdown-style="{ minWidth: '120px' }"
            />
            <!-- 规则名称搜索：紧跟第四个下拉框右侧，尺寸/圆角/间距与下拉框一致。
                 用单个 a-input（而非 a-input-search）避免输入框与尾部搜索按钮拼接造成的边框错位；
                 支持输入防抖即时过滤、回车立即查询、一键清空 -->
            <a-input
              v-model:value="searchInput"
              class="filter-search"
              placeholder="请输入规则名称"
              allow-clear
              @press-enter="handleSearchByName"
            />
          </a-space>
        </template>
        <a-tab-pane key="structure" tab="DDL">
          <RuleTable
            :rules="structureRules"
            :filters="filters"
            :loading="loading || searching"
            :can-manage="canManage"
            @update="handleUpdateRule"
          />
        </a-tab-pane>
        <a-tab-pane key="query_update" tab="DML">
          <RuleTable
            :rules="queryUpdateRules"
            :filters="filters"
            :loading="loading || searching"
            :can-manage="canManage"
            @update="handleUpdateRule"
          />
        </a-tab-pane>
      </a-tabs>
    </a-card>
  </div>
</template>

<script setup>
import { message } from 'ant-design-vue'
import { computed, inject, onActivated, onDeactivated, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getSQLStandardDetailApi, updateSQLStandardRuleApi } from '@/api/admin'
import { useUserStore } from '@/store/user'
import RuleTable from './RuleTable.vue'
import { resolveRuleType as formatRuleType } from './ruleTypeUtils'

// 向公共布局申请「内容区不滚动」：本页自行用 flex + 表格内部滚动消化溢出，
// 保证页面外层永远不出现滚动条（与监控大屏 / SQL 查询页同一套机制）
const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()

const standardID = computed(() => route.params.id)
const loading = ref(false)
const standard = reactive({
  id: undefined,
  name: '',
  description: '',
  env_type: '',
  is_default: false,
  enabled: false,
  rules: [],
})
const activeTab = ref('structure')

const filters = reactive({
  status: 'all',
  dbType: 'all',
  ruleType: 'all',
  level: 'all',
  keyword: '',
})

// 规则名称搜索：
// - searchInput 是输入框的实时值（v-model），filters.keyword 是防抖后真正参与过滤的值
// - 输入即触发 300ms 防抖过滤；回车跳过防抖立即查询；清空（allow-clear 或删空）立即恢复
// - searching 用于搜索过程中给表格展示加载态，不刷新整页、不重新请求接口
const searchInput = ref('')
const searching = ref(false)
let searchTimer = null

const applyKeyword = (value) => {
  filters.keyword = (value || '').trim() // 忽略首尾空格
  searching.value = false
}

watch(searchInput, (value) => {
  if (searchTimer) {
    clearTimeout(searchTimer)
    searchTimer = null
  }
  if (!value || !value.trim()) {
    // 清空关键字：立即恢复为仅按下拉条件筛选的结果，避免残留上一轮过滤
    applyKeyword('')
    return
  }
  searching.value = true
  searchTimer = setTimeout(() => applyKeyword(value), 300)
})

// 回车立即查询（不等防抖）
const handleSearchByName = () => {
  if (searchTimer) {
    clearTimeout(searchTimer)
    searchTimer = null
  }
  applyKeyword(searchInput.value)
}

// 状态与规则级别为通用枚举，选项固定；数据库类型与规则类型随当前 Tab 动态生成
const statusOptions = [
  { label: '全部状态', value: 'all' },
  { label: '启用', value: 'on' },
  { label: '停用', value: 'off' },
]
const levelOptions = [
  { label: '全部级别', value: 'all' },
  { label: '必须改进', value: 'must' },
  { label: '建议改进', value: 'suggest' },
]

const canManage = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.inspect.manage'),
)

const structureRules = computed(() => standard.rules.filter((r) => r.category === 'structure'))
const queryUpdateRules = computed(() => standard.rules.filter((r) => r.category === 'query_update'))

// 当前 Tab 下的规则集合：筛选选项与失效回退都以它为准
const currentRules = computed(() =>
  activeTab.value === 'structure' ? structureRules.value : queryUpdateRules.value,
)

const DB_LABELS = { mysql: 'MySQL', postgres: 'PostgreSQL', kingbase: 'Kingbase', oracle: 'Oracle', tidb: 'TiDB', sqlserver: 'SQLServer' }
const DB_ORDER = ['mysql', 'postgres', 'kingbase', 'oracle', 'tidb', 'sqlserver']

// 数据库类型选项：只保留当前 Tab 实际存在的数据库，避免选中后永远筛不出结果
const dbTypeOptions = computed(() => {
  const set = new Set()
  currentRules.value.forEach((r) => {
    const t = String(r.db_type || '').toLowerCase()
    if (t === 'all') {
      DB_ORDER.forEach((db) => set.add(db))
    } else if (DB_ORDER.includes(t)) {
      set.add(t)
    }
  })
  return [
    { label: '全部数据库', value: 'all' },
    ...DB_ORDER.filter((t) => set.has(t)).map((t) => ({ label: DB_LABELS[t], value: t })),
  ]
})

// 规则类型选项：只保留当前 Tab 实际出现的类型（如「DDL」Tab 不会出现更新/删除/写入）
const ruleTypeOptions = computed(() => {
  const set = new Set()
  currentRules.value.forEach((r) => {
    set.add(formatRuleType(r))
  })
  const sorted = Array.from(set)
    .filter((t) => t !== '待补充')
    .sort()
    .map((t) => ({ label: t, value: t }))
  return [
    { label: '全部类型', value: 'all' },
    ...sorted,
    ...(set.has('待补充') ? [{ label: '待补充', value: '待补充' }] : []),
  ]
})

// 切换 Tab（或数据变化）后，若已选中的值在新选项集合中不存在，回退为「全部」
const resetInvalidFilters = () => {
  const isValid = (options, value) => options.some((o) => o.value === value)
  if (!isValid(statusOptions, filters.status)) filters.status = 'all'
  if (!isValid(dbTypeOptions.value, filters.dbType)) filters.dbType = 'all'
  if (!isValid(ruleTypeOptions.value, filters.ruleType)) filters.ruleType = 'all'
  if (!isValid(levelOptions, filters.level)) filters.level = 'all'
}
watch([activeTab, dbTypeOptions, ruleTypeOptions], resetInvalidFilters, { immediate: true })

const fetchDetail = async () => {
  loading.value = true
  const res = await getSQLStandardDetailApi(standardID.value).catch(() => {})
  if (res) {
    Object.assign(standard, res.data)
  }
  loading.value = false
}

const handleUpdateRule = async (ruleOrRules) => {
  const rules = (Array.isArray(ruleOrRules) ? ruleOrRules : [ruleOrRules]).filter(Boolean)
  if (rules.length === 0) {
    return
  }
  const settled = await Promise.allSettled(
    rules.map((rule) =>
      updateSQLStandardRuleApi({
        rule_id: rule.id,
        level: rule.level,
        params: rule.params || {},
        description: rule.description ?? '',
      }),
    ),
  )

  const successRules = []
  const failedCount = settled.filter((r) => r.status === 'rejected' || !r.value).length
  settled.forEach((result, idx) => {
    if (result.status === 'fulfilled' && result.value) {
      // 以后端回写的最新值为准，保证页面展示与数据库完全一致（刷新后不会回退）
      const updated = result.value?.data || {}
      successRules.push({
        id: updated.id ?? rules[idx].id,
        level: updated.level ?? rules[idx].level,
        params: updated.params ?? rules[idx].params ?? {},
        description: updated.description ?? rules[idx].description ?? '',
      })
    }
  })

  if (successRules.length > 0) {
    message.success(`规则已更新${successRules.length > 1 ? `（${successRules.length} 条）` : ''}`)
    successRules.forEach((rule) => {
      const idx = standard.rules.findIndex((r) => r.id === rule.id)
      if (idx !== -1) {
        standard.rules[idx].level = rule.level
        standard.rules[idx].params = rule.params || {}
        standard.rules[idx].description = rule.description
      }
    })
  }
  if (failedCount > 0) {
    message.error(`${failedCount} 条规则更新失败，请刷新后重试`)
  }
}

const handleBack = () => {
  router.push({ name: 'view.admin.sql_standards' })
}

onActivated(() => setLayoutContentNoScroll?.(true))
onDeactivated(() => setLayoutContentNoScroll?.(false))

onMounted(() => {
  setLayoutContentNoScroll?.(true)
  fetchDetail()
})

onUnmounted(() => {
  setLayoutContentNoScroll?.(false)
  // 组件卸载时清掉未触发的防抖定时器，避免在已销毁实例上再写响应式状态
  if (searchTimer) {
    clearTimeout(searchTimer)
    searchTimer = null
  }
})
</script>

<style scoped>
/* 整页占满内容区高度（外层 layout 已是 100vh 且不滚动）：
   高度用 flex 逐级分配 —— 面包屑 / Tab 栏固定，剩余全部给表格 + 分页；
   本页自身 overflow:hidden，页面外层永远不出现滚动条，窗口 resize 自适应 */
.standard-detail-page {
  height: 100%;
  display: flex;
  flex-direction: column;
  gap: 4px;
  overflow: hidden;
}

/* 面包屑/标题区：固定高度，不参与剩余空间分配；
   标题置顶显示，紧贴内容区顶部，避免标题文字被上方空间挤压后又被 overflow 截断 */
.standard-detail-page :deep(.ant-page-header) {
  padding: 0;
  flex: 0 0 auto;
  margin-top: 0;
  margin-bottom: 4px;
}

.standard-detail-page :deep(.ant-page-header-heading) {
  margin-top: 0;
}

/* 标题行高/左侧区域紧凑化，避免标题区上下留白过大 */
.standard-detail-page :deep(.ant-page-header-heading-left) {
  margin-top: 0;
  margin-bottom: 0;
}

/* 标题字号/行高：AntD 默认标题字号 20px，实际字体（尤其是中文）的渲染高度会超过行框；
   这里用 line-height: 1.75（35px）给足空间，配合 vertical-align: top 让文字紧贴行框顶部，
   避免 overflow:hidden + ellipsis 裁掉上半部分。同时保留超长省略号。 */
.standard-detail-page :deep(.ant-page-header-heading-title) {
  line-height: 1.75;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: top;
}

/* 规范名称：明确渲染并防止超长时换行/溢出；
   不要锁死高度，line-height 1.75 确保 20px 中文字体不会被 overflow:hidden 截半；
   垂直置顶对齐，避免文字在父行框中偏下被上沿裁剪 */
.standard-detail-page :deep(.ant-page-header-heading-title) .standard-title {
  display: inline-block;
  max-width: 100%;
  line-height: 1.75;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: top;
}

/* Tab 导航左侧留白 + 圆角过渡，使【DDL】标签与左侧边缘有自然弧形间隔；固定高度 */
.standard-detail-page :deep(.ant-tabs-nav) {
  margin-bottom: 8px;
  padding-left: 16px;
  flex: 0 0 auto;
}

.standard-detail-page :deep(.ant-tabs-tab) {
  border-radius: 8px 8px 0 0;
}

/* 卡片占满剩余高度 */
.standard-detail-page :deep(.ant-card) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.standard-detail-page :deep(.ant-card-body) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 0;
  overflow: hidden;
}

/* tabs 容器占满剩余高度 */
.standard-detail-page :deep(.ant-tabs) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

/* Tab 内容区左右留白 16px（表格左侧与标题左对齐协调），底部留白 8px */
.standard-detail-page :deep(.ant-tabs-content),
.standard-detail-page :deep(.ant-tabs-content-holder) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.standard-detail-page :deep(.ant-tabs-content) {
  padding: 0 16px 8px;
}

/* tabpane 占满剩余空间，内部表格基于剩余高度展示 */
.standard-detail-page :deep(.ant-tabs-tabpane) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* 消除表格默认底部 margin，避免与分页分离 */
.standard-detail-page :deep(.ant-table-wrapper) {
  margin-bottom: 0 !important;
}

/* 分页器底部 margin 归零 */
.standard-detail-page :deep(.ant-pagination) {
  margin-bottom: 0;
}

/* 规则筛选栏：与 Tab 标签同行右侧对齐，紧凑不换行 */
.rule-filter-bar {
  flex-wrap: nowrap;
  white-space: nowrap;
  padding-right: 16px;
}

/* 下拉框与搜索框统一：高度 32px、圆角 6px、完整闭合边框、基线对齐 */
.rule-filter-bar :deep(.ant-select),
.rule-filter-bar :deep(.ant-input-affix-wrapper) {
  height: 32px;
}

.rule-filter-bar :deep(.ant-select-selector) {
  height: 32px !important;
  border-radius: 6px !important;
}

.rule-filter-bar :deep(.ant-select-selection-item),
.rule-filter-bar :deep(.ant-select-selection-placeholder) {
  line-height: 30px;
}

/* 各控件固定宽度，保证筛选栏整体整齐；宽度按最长选项文字 + 图标占位留出余量。
   统一不允许被 flex 压缩，避免窄屏下拉框/搜索框被挤扁导致文字溢出 */
.filter-select-status,
.filter-select-db,
.filter-select-type,
.filter-select-level,
.filter-search {
  flex-shrink: 0;
}

.filter-select-status {
  width: 116px;
}
.filter-select-db {
  width: 132px;
}
.filter-select-type {
  width: 116px;
}
.filter-select-level {
  width: 120px;
}

/* 规则名称搜索框：紧跟第四个下拉框，高度/圆角与下拉框一致 */
.filter-search {
  width: 200px;
}

.rule-filter-bar :deep(.ant-input-affix-wrapper) {
  border-radius: 6px;
}

/* 输入文字与 placeholder：与下拉项的 30px 行高对齐，超长时省略号截断不溢出边框 */
.rule-filter-bar :deep(.ant-input),
.rule-filter-bar :deep(.ant-input::placeholder) {
  line-height: 30px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 清空图标：贴近输入框内边距，避免超出 32px 高的 wrapper */
.rule-filter-bar :deep(.ant-input-clear-icon) {
  font-size: 12px;
}

/* 强制 a-space 的子项不被压缩（优先级高于 ant-space-item 默认 flex-shrink:1） */
.rule-filter-bar :deep(.ant-space-item) {
  flex-shrink: 0 !important;
}</style>

<style>
/* 下拉面板默认渲染到 body，scoped 选择器命中不到，这里用独立类名限定作用域：
   宽度按内容自适应（配合 dropdown-match-select-width=false），文字不换行、不截断 */
.rule-filter-dropdown .ant-select-item {
  white-space: nowrap;
}

.rule-filter-dropdown .ant-select-item-option-content {
  overflow: visible;
  text-overflow: clip;
}

@media (max-width: 768px) {
  /* 筛选栏挂在 tabs 的 extra slot 里，公共工具栏的横滚兜底管不到它 */
  .standard-detail-page :deep(.ant-tabs-nav) {
    flex-wrap: wrap;
    row-gap: 8px;
  }

  .standard-detail-page :deep(.ant-tabs-extra-content) {
    width: 100%;
    margin-inline-start: 0 !important;
  }

  .rule-filter-bar {
    flex-wrap: wrap !important;
    white-space: normal !important;
    padding-right: 0;
    width: 100%;
  }

  /* 原来用 !important 把收缩能力锁死了，这里解锁 */
  .rule-filter-bar :deep(.ant-space-item) {
    flex-shrink: 1 !important;
  }

  .filter-select-status,
  .filter-select-db,
  .filter-select-type,
  .filter-select-level {
    width: calc(50% - 4px) !important;
    flex: 0 0 calc(50% - 4px) !important;
  }

  .filter-search {
    width: 100% !important;
    flex: 0 0 100% !important;
  }
}
</style>
