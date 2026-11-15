<template>
  <div class="gi-page-shell snapshot-page">
    <PageHeader title="负载快照">
      <template #right>
        <!-- 筛选组件与刷新按钮同一行（页面标题条右侧操作区） -->
        <a-space wrap align="center" class="page-actions">
          <a-input-search
            v-model:value="filters.search"
            placeholder="搜索实例名或 IP"
            class="toolbar-search"
            allow-clear
          />
          <a-select v-model:value="filters.dbType" class="toolbar-select" allow-clear placeholder="全部类型">
            <a-select-option value="MySQL">MySQL</a-select-option>
            <a-select-option value="PostgreSQL">PostgreSQL</a-select-option>
            <a-select-option value="Kingbase">Kingbase</a-select-option>
          </a-select>
          <a-select v-model:value="filters.environment" class="toolbar-select" allow-clear placeholder="全部环境">
            <a-select-option v-for="env in environmentOptions" :key="env" :value="env">{{ env }}</a-select-option>
          </a-select>
          <a-select v-model:value="filters.snapshot" class="toolbar-select" allow-clear placeholder="全部快照状态">
            <a-select-option value="has">有快照</a-select-option>
            <a-select-option value="none">无快照</a-select-option>
          </a-select>
          <a-tooltip title="刷新">
            <a-button :loading="loading" aria-label="刷新" @click="load">
              <SyncOutlined />
            </a-button>
          </a-tooltip>
        </a-space>
      </template>
    </PageHeader>

    <a-card class="section-card" :bordered="false">
      <div class="table-height-host">
        <a-table
          :columns="columns"
          :data-source="filteredItems"
          :loading="loading"
          :pagination="paginationConfig"
          row-key="instance_id"
          :scroll="{ x: 1080 }"
          @change="onTableChange"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'name'">
              <div class="inst-cell">
                <div class="inst-name" :title="record.instance_name">{{ clipText(record.instance_name, 15) }}</div>
                <div
                  v-if="record.addr && record.addr !== record.instance_name"
                  class="inst-addr"
                  :title="record.addr"
                >{{ clipText(record.addr, 20) }}</div>
              </div>
            </template>
            <template v-else-if="column.key === 'snapshot'">
              <template v-if="record.snapshot_count > 0">
                <span class="num">{{ record.snapshot_count }}</span> 份 · 最近 <b>{{ relativeTime(record.last_snapshot_at) }}</b>
                <a-tag v-if="record.running" class="running-tag" color="processing">生成中</a-tag>
              </template>
              <a-tag v-else-if="record.running" class="running-tag" color="processing">生成中</a-tag>
              <span v-else class="muted">暂无快照</span>
            </template>
            <template v-else-if="column.key === 'actions'">
              <a-space class="gi-table-actions">
                <a-button
                  type="link"
                  size="small"
                  :disabled="record.snapshot_count === 0 && !record.running"
                  @click="goReport(record)"
                >查看</a-button>
                <a-button
                  v-if="canManageSnapshot"
                  type="link"
                  size="small"
                  :disabled="record.running"
                  @click="openCreate(record)"
                >生成</a-button>              </a-space>
            </template>
          </template>
        </a-table>
      </div>
    </a-card>

    <a-modal v-model:open="createModal.open" title="生成负载快照" @ok="submitCreate" :confirm-loading="createModal.submitting">
      <p class="create-desc">
        将对 <b>{{ createModal.instance?.hostname }}</b>（{{ createModal.instance?.db_type }}）执行两点采样：
        立即采集基线，等待观察窗口结束后再做终点采样并差分出报告。生成期间会对实例执行少量只读查询。
      </p>
      <a-form layout="vertical">
        <a-form-item label="观察窗口（分钟）" help="窗口越长报告越能代表稳定负载，1-120 分钟。">
          <a-input-number v-model:value="createModal.window" :min="1" :max="120" style="width: 160px" />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { message } from 'ant-design-vue'
import { SyncOutlined } from '@ant-design/icons-vue'
import PageHeader from '../components/PageHeader.vue'
import { clipText } from '../utils'
import { useUserStore } from '@/store/user'
import {
  createLoadSnapshotApi,
  getLoadSnapshotInstancesApi,
} from '@/api/monitor'

defineOptions({ name: 'MonitorSnapshotView' })

const userStore = useUserStore()

// 「生成」按「管理性能」控制，与后端 POST /monitor/snapshots 的守卫（performance.manage）同口径。
// 负载快照页面唯一入口在「性能分析 → 负载快照」，因此不再掺入 monitor.manage。
const canManageSnapshot = computed(() => userStore.hasPermission('performance.manage'))

const router = useRouter()
const loading = ref(false)
const items = ref([])
const filters = reactive({ search: '', dbType: undefined, environment: undefined, snapshot: undefined })

// 列宽比例分配（相对表格实际宽度，随视口伸缩）：数据源占 25%，其余 4 列均分剩余 75%（各 18.75%），
// Σ = 100%，与 scroll.x（min-width 兜底）口径一致。
// 操作列 align:'right' 同时作用于表头与表体（内联 text-align）；表头需居中，由样式区 CSS 覆盖。
const columns = [
  { title: '数据源', key: 'name', width: '25%' },
  { title: '数据库类型', dataIndex: 'db_type', key: 'db_type', width: '18.75%' },
  { title: '环境', dataIndex: 'environment_name', key: 'environment_name', width: '18.75%' },
  { title: '快照概览', key: 'snapshot', width: '18.75%' },
  { title: '操作', key: 'actions', width: '18.75%', fixed: 'right', align: 'right' },
]

const environmentOptions = computed(() => {
  const set = new Set()
  items.value.forEach((item) => {
    if (item.environment_name) set.add(item.environment_name)
  })
  return [...set]
})

const filteredItems = computed(() => {
  return items.value.filter((item) => {
    if (filters.search) {
      const kw = filters.search.toLowerCase()
      const hit = `${item.hostname} ${item.instance_name}`.toLowerCase().includes(kw)
      if (!hit) return false
    }
    if (filters.dbType && item.db_type !== filters.dbType) return false
    if (filters.environment && item.environment_name !== filters.environment) return false
    if (filters.snapshot === 'has' && item.snapshot_count === 0 && !item.running) return false
    if (filters.snapshot === 'none' && (item.snapshot_count > 0 || item.running)) return false
    return true
  })
})

const createModal = reactive({ open: false, submitting: false, instance: null, window: 15 })
let pollTimer = null

// ----- 分页：总条数 + 当前页/总页数 + 每页行数选择（受控，筛选变化时回到第 1 页） -----
const pager = reactive({ current: 1, size: 10 })
const paginationConfig = computed(() => ({
  current: pager.current,
  pageSize: pager.size,
  showSizeChanger: true,
  pageSizeOptions: ['10', '20', '50', '100'],
  showTotal: (total) => {
    const pages = Math.max(1, Math.ceil(total / pager.size))
    return `共 ${total} 条 · 第 ${Math.min(pager.current, pages)}/${pages} 页`
  },
}))

function onTableChange(pag) {
  pager.current = pag.current
  pager.size = pag.pageSize
}

watch(
  () => [filters.search, filters.dbType, filters.environment, filters.snapshot],
  () => {
    pager.current = 1
  }
)

async function load() {
  loading.value = true
  try {
    const data = await getLoadSnapshotInstancesApi()
    // 请求层 resolve 的是响应体 {code, data, message}，载荷在 .data 这一层
    items.value = data?.data?.items || []
    schedulePoll()
  } catch {
    // 拦截器已 toast，这里不再重复
  } finally {
    loading.value = false
  }
}

// 有进行中的任务时 10s 轮询，全部落定后停止
function schedulePoll() {
  if (pollTimer) {
    clearTimeout(pollTimer)
    pollTimer = null
  }
  if (items.value.some((item) => item.running)) {
    pollTimer = setTimeout(load, 10000)
  }
}

function relativeTime(value) {
  if (!value) return ''
  const diff = Date.now() - new Date(String(value).replace(/-/g, '/')).getTime()
  if (Number.isNaN(diff) || diff < 0) return value
  const minutes = Math.floor(diff / 60000)
  if (minutes < 1) return '刚刚'
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  return `${Math.floor(hours / 24)} 天前`
}

function goReport(record) {
  router.push(`/performance/snapshot-report/${record.last_snapshot_id || record.instance_id}`)
}

function openCreate(record) {
  createModal.instance = record
  createModal.window = 15
  createModal.open = true
}

async function submitCreate() {
  createModal.submitting = true
  try {
    await createLoadSnapshotApi({
      instance_id: createModal.instance.instance_id,
      window_minutes: createModal.window,
    })
    message.success('快照任务已提交，基线采集中')
    createModal.open = false
    load()
  } catch {
    // 拦截器已 toast
  } finally {
    createModal.submitting = false
  }
}

onMounted(load)
onBeforeUnmount(() => {
  if (pollTimer) clearTimeout(pollTimer)
})
</script>

<style scoped lang="scss">
.snapshot-page {
  /* ----- 顶部筛选控件：已随刷新按钮并入 PageHeader 右侧操作区 -----
     标题条右侧是 flex 容器（gap 8px + flex-wrap），这里只约束各自宽度：
     搜索框定宽 220px（原先按卡片宽度 25% 计算，在标题条里无意义），
     下拉定宽 132px 与搜索框同一行排布，窄屏由容器换行兜底。 */
  .page-actions {
    flex-wrap: wrap;
  }
  .toolbar-search {
    width: 220px;
    min-width: 170px;
  }
  .toolbar-select {
    width: 132px;
  }

  /* 筛选行移出卡片后，卡片顶部内边距由 24 收到 8：
     避免表格与页头之间留出一条空行（与【监控配置】页 .section-card 同一惯例）。 */
  .section-card :deep(.ant-card-body) {
    padding: var(--gi-spacing-sm, 8px) var(--gi-spacing-md, 16px) var(--gi-spacing-md, 16px);
  }

  /* ----- 表格滚动：滚动条只出现在表格内容区（.ant-table-content），表头 sticky 固定 -----
     用 max-height 而非固定高度：行数少时表格按内容收缩（不留大片底部空白），
     行数多时内容区内部滚动，分页紧跟表格，页面本身不产生长滚动。 */
  .table-height-host {
    :deep(.ant-table-content) {
      max-height: calc(100vh - 330px);
      min-height: 120px;
      overflow: auto !important; /* 横向+纵向滚动都收在内容区 */
    }
    :deep(.ant-table-thead > tr > th) {
      position: sticky;
      top: 0;
      z-index: 3;
    }
    /* 操作列表头居中、表体保持右对齐：column.align 渲染成 th/td 的内联 text-align（两者同值），
       无法用 customHeaderCell 覆盖（vc-table 的 alignStyle 在 style 数组后位），只能 CSS !important 覆盖表头；
       本页唯一 fixed-right 列即操作列，用 .ant-table-cell-fix-right 限定，避免误伤其他表头。 */
    :deep(.ant-table-thead > tr > th.ant-table-cell-fix-right) {
      text-align: center !important;
    }
    :deep(.ant-table-pagination.ant-pagination) {
      margin: 14px 0 2px;
    }
  }

  .inst-cell { min-width: 0; }
  .inst-name { font-size: 13px; }
  .inst-addr {
    font-family: var(--gi-font-family-mono, monospace);
    font-size: 11.5px;
    color: var(--gi-text-tertiary, rgba(0, 0, 0, 0.45));
  }
  .num { font-variant-numeric: tabular-nums; }
  .muted { color: var(--gi-text-tertiary, rgba(0, 0, 0, 0.45)); }
  .running-tag { margin-left: 6px; }
  .create-desc { font-size: 13px; color: var(--gi-text-secondary, rgba(0, 0, 0, 0.62)); }
}

@media (max-width: 768px) {
  .toolbar-search,
  .toolbar-select {
    width: 100%;
    min-width: 0;
    max-width: 100%;
  }

  /* 原值 calc(100vh - 330px) 在移动端视口下会压成一条甚至为负 */
  .table-height-host :deep(.ant-table-content) {
    max-height: calc(100dvh - 260px);
    min-height: 200px;
  }
}
</style>
