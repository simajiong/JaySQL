<template>
  <div class="gi-page-shell instance-list-page-shell">
    <!-- 页面标题在内容卡片之外；标题 + 筛选/搜索/按钮全部恒为同一行
         （极窄窗口由该行横向滚动，绝不折行） -->
    <PageToolbar class="gi-toolbar-single-line">
      <template #left>
        <div class="gi-page-title">实例配置</div>
        </template>
        <template #right>
          <div class="instance-toolbar-right">
            <a-input-search
              v-model:value="filters.search"
              class="instance-search"
              placeholder="搜索名称/ID/IP"
              allowClear
              @search="handleSearch"
            />
            <a-tooltip title="开启后打开本页会立即补测一轮异常/待检测实例（最多 3 条）。常态探活由服务端后台执行：异常实例约 60 秒复测、可用实例约 10 分钟兜底，无需保持页面打开。">
              <a-button class="auto-check-btn" @click.stop.prevent>
                <a-switch v-model:checked="uiState.autoCheck" size="small" @click.stop @change.stop />
              </a-button>
            </a-tooltip>
            <a-button danger :disabled="!canManageInstance || !selectedRowKeys.length" @click="handleBatchDelete">
              批量删除
            </a-button>
            <a-button :disabled="!canManageInstance" @click="handleBatchImport">
              <ImportOutlined />
              批量导入
            </a-button>
            <a-button type="primary" :disabled="!canManageInstance" @click="handleAdd">
              <PlusOutlined />
              新增实例
            </a-button>
          </div>
        </template>
    </PageToolbar>

    <a-card class="instance-page-card" :bordered="false">
      <PageTableSection>
        <a-table
          class="instance-table"
          size="middle"
          :columns="tableColumns"
          :row-key="(record) => record.id"
          :data-source="tableRows"
          :pagination="pagination"
          :loading="uiState.loading"
          :row-selection="canManageInstance ? rowSelection : undefined"
          :scroll="{ x: 1310, y: 'calc(100vh - 310px)' }"
          @change="handleTableChange"
        >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'data_source_id'">
            <div class="data-source-id-cell">
              <div class="data-source-id-main">
                <div class="data-source-id-row">
                  <span class="data-source-id-text">{{ record.data_source_id }}</span>
                  <a-tooltip title="复制数据源 ID">
                    <a-button
                      type="text"
                      size="small"
                      class="copy-id-btn"
                      @click="copyDataSourceId(record)"
                    >
                      <CopyOutlined />
                    </a-button>
                  </a-tooltip>
                </div>
                <div class="data-source-name-row">
                <DataSourceName :name="record.remark" class="data-source-name-text" />
                <a-popover
                  v-if="canManageInstance"
                  :open="renameState.id === record.id"
                  trigger="click"
                  placement="bottomLeft"
                  overlay-class-name="instance-rename-popover"
                  @open-change="(open) => handleRenameOpenChange(open, record)"
                >
                  <template #content>
                    <div class="rename-popover-body">
                      <a-input
                        v-model:value="renameState.draft"
                        size="small"
                        :maxlength="256"
                        placeholder="数据源名称"
                        @press-enter="confirmRename(record)"
                      />
                      <a-button size="small" @click="cancelRename">取消</a-button>
                      <a-button
                        size="small"
                        type="primary"
                        :loading="renameState.savingId === record.id"
                        @click="confirmRename(record)"
                      >
                        保存
                      </a-button>
                    </div>
                  </template>
                  <a-tooltip title="修改数据源名称">
                    <a-button type="text" size="small" class="rename-btn" @click.stop>
                      <FormOutlined />
                    </a-button>
                  </a-tooltip>
                </a-popover>
              </div>
              </div>
            </div>
          </template>

          <template v-else-if="column.key === 'environment_name'">
            <span>{{ record.environment_name || '-' }}</span>
          </template>

          <template v-else-if="column.key === 'connection_address'">
            <span class="connection-address-text">{{ record.hostname }}:{{ record.port }}</span>
          </template>

          <template v-else-if="column.key === 'db_type_version'">
            <div class="db-type-cell">
              <img
                class="db-type-logo"
                :src="dbTypeLogo(record.db_type)"
                :alt="record.db_type"
                :title="record.db_type"
              />
              <span class="db-type-text" :title="dbTypeWithVersion(record)">{{ dbTypeWithVersion(record) }}</span>
            </div>
          </template>

          <template v-else-if="column.key === 'created_by'">
            <span>{{ record.created_by || '-' }}</span>
          </template>

          <template v-else-if="column.key === 'status'">
            <div class="status-cell">
              <span class="status-dot" :class="`is-${normalizeStatus(record.last_check_status)}`"></span>
              <!-- 检测时间改为悬浮提示：列表里不再占位，但"有没有在探测"仍可查 -->
              <span
                class="status-text"
                :title="lastCheckedText(record) ? `最近检测：${record.last_checked_at}` : ''"
              >
                {{ formatStatusText(record.last_check_status) }}
              </span>
            </div>
          </template>

          <template v-else-if="column.key === 'action'">
            <div class="action-cell">
              <!-- 探活失败时高频操作前置到「更多」外，样式与操作列其他 text 按钮一致 -->
              <a-button
                v-if="canTestInstance"
                type="text"
                size="small"
                class="retry-action-btn"
                :loading="uiState.testingIds.includes(record.id)"
                title="重新检测连通性"
                @click="handleTestConnection(record)"
              >
                重试
              </a-button>
              <a-dropdown>
                <a-button type="text" class="more-action-btn">
                  更多
                  <DownOutlined />
                </a-button>
                <template #overlay>
                  <a-menu>
                    <a-menu-item v-if="canManageInstance" key="edit" @click="handleEdit(record)">
                      <EditOutlined />
                      编辑
                    </a-menu-item>
                    <a-menu-item v-if="canManageInstance" key="copy" @click="handleCopy(record)">
                      <CopyOutlined />
                      复制
                    </a-menu-item>
                    <a-menu-item v-if="canTestInstance" key="test" @click="handleTestConnection(record)">
                      <LinkOutlined />
                      刷新连通性
                    </a-menu-item>
                    <a-menu-item
                      v-if="canTestInstance"
                      key="status_detail"
                      :disabled="normalizeStatus(record.last_check_status) !== 'error'"
                      @click="showStatusDetailModal(record)"
                    >
                      <InfoCircleOutlined />
                      详情
                    </a-menu-item>
                    <a-menu-item v-if="canSyncMetaInstance" key="sync" @click="handleSyncMeta(record)">
                      <SyncOutlined />
                      同步元数据
                    </a-menu-item>
                    <a-menu-item v-if="canManageInstance" key="delete">
                      <a-popconfirm title="确认删除吗？" ok-text="是" cancel-text="否" @confirm="handleDelete(record)">
                        <span class="danger-action">
                          <DeleteOutlined />
                          删除
                        </span>
                      </a-popconfirm>
                    </a-menu-item>
                  </a-menu>
                </template>
              </a-dropdown>
            </div>
          </template>
        </template>
      </a-table>
      </PageTableSection>
    </a-card>

    <InstanceFormModal
      :open="uiState.isModalOpen"
      v-model:modelValue="formState"
      :environments="uiData.environments"
      :can-manage="canManageInstance"
      :can-test-connection="canTestInstance"
      :existing-addresses="uiData.addressBook"
      :copy-source-id="uiState.copySourceId"
      :copy-source-label="uiState.copySourceLabel"
      :title="modalTitle"
      @update:open="handleModalOpenChange"
      @submit="onSubmit"
    />
    <InstanceBatchImportModal
      :open="uiState.isBatchImportOpen"
      :environments="uiData.environments"
      :existing-rows="uiData.tableData || []"
      @update:open="uiState.isBatchImportOpen = $event"
      @done="handleBatchImportDone"
    />

    <!-- 删除被拒：数据源仍被备份/恢复任务引用。
         这里只做两件事——说清拦了什么、把人送到能处理它的页面；
         不做「一键连任务一起删」，破坏性交给用户在各自入口确认。 -->
    <a-modal
      :open="blockingState.open"
      title="无法删除数据源"
      :footer="null"
      width="640px"
      @cancel="closeBlockingModal"
    >
      <a-alert
        type="warning"
        show-icon
        message="以下数据源仍被备份或恢复类记录引用，需先处理关联记录才能删除"
        description="数据源删除成功时，其访问权限（库/表授权）会自动回收，无需手工清理。"
      />
      <div
        v-for="item in blockingState.items"
        :key="item.key"
        class="blocking-instance-block"
      >
        <div class="blocking-instance-title">{{ item.label }}</div>
        <div
          v-for="(resource, index) in item.resources"
          :key="`${item.key}-${index}`"
          class="blocking-resource-row"
        >
          <span class="blocking-kind">{{ resource.kindText }}</span>
          <span class="blocking-resource-name" :title="resource.name">{{ resource.name }}</span>
          <a-tag v-if="resource.status" :color="resource.active ? 'red' : 'default'">
            {{ resource.status }}
          </a-tag>
          <a-button type="link" size="small" @click="gotoBlockingResource(item, resource)">
            {{ resource.actionText }}
          </a-button>
        </div>
      </div>
      <div class="blocking-modal-footer">
        <a-button @click="closeBlockingModal">知道了</a-button>
      </div>
    </a-modal>
  </div>
</template>

<script setup>
import {
  createInstancesApi,
  deleteInstancesApi,
  getEnvironmentsApi,
  getInstancesApi,
  syncInstanceMetaApi,
  testInstanceConnectionApi,
  updateInstanceRemarkApi,
  updateInstancesApi,
} from '@/api/admin'
import DataSourceName from '@/components/patterns/DataSourceName.vue'
import PageTableSection from '@/components/patterns/PageTableSection.vue'
import PageToolbar from '@/components/patterns/PageToolbar.vue'
import {
  CopyOutlined,
  DeleteOutlined,
  DownOutlined,
  EditOutlined,
  FormOutlined,
  ImportOutlined,
  InfoCircleOutlined,
  LinkOutlined,
  PlusOutlined,
  SyncOutlined,
} from '@ant-design/icons-vue'
import { useThrottleFn } from '@vueuse/core'
import { Modal, message } from 'ant-design-vue'
import { computed, createVNode, nextTick, onActivated, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import InstanceBatchImportModal from './InstanceBatchImportModal.vue'
import InstanceFormModal from './InstanceFormModal.vue'
import { useUserStore } from '@/store/user'
import { dbTypeDisplayName } from '@/utils/dbFamily'
import { dbTypeLogo } from '@/utils/dbTypeLogo'

const router = useRouter()
const route = useRoute()

const normalizeStatus = (value) => {
  const normalized = String(value || '').trim().toLowerCase()
  if (normalized === 'success') return 'success'
  if (normalized === 'error') return 'error'
  return 'unknown'
}

const formatStatusText = (value) => {
  const normalized = normalizeStatus(value)
  if (normalized === 'success') return '可用'
  if (normalized === 'error') return '不可用'
  return '待检测'
}

// 最近检测时间：后端 LocalTime 已序列化成 "2006-01-02 15:04:05"（无值为 null），
// 列表里只截 HH:mm:ss 够用（同一天看得清"刚才探过"），完整值放 title 里。
const lastCheckedText = (record) => {
  const raw = String(record?.last_checked_at || '').trim()
  if (raw.length < 19) return ''
  return raw.slice(11, 19)
}

// 数据库类型 + 版本：如「MySQL 8.0.43」「PostgreSQL 16.15」。
// 口径与实例画像 DetailView.dbVersionType、慢SQL分析 dbTypeText、监控快照一致：
// 版本未采集（db_version 为空）时只显示类型，避免出现「MySQL 」带尾空格；
// 类型与版本均缺失时返回占位符「-」，绝不透出 undefined / null。
// 类型名统一走 dbTypeDisplayName（utils/dbFamily），OceanBase 会自动带出租户模式。
const dbTypeWithVersion = (record) => {
  const rawType = String(record?.db_type || '').trim()
  // OceanBase 带出租户模式（OceanBase (MySQL) / OceanBase (Oracle)）：
  // 同为 OceanBase 的两条记录走的通道完全不同，列表里必须能区分。
  const type = rawType ? dbTypeDisplayName(rawType, record?.extra_config) : ''
  const version = String(record?.db_version || '').trim()
  const text = `${type} ${version}`.trim()
  return text || '-'
}

const uiState = reactive({
  loading: false,
  isEditMode: false,
  isModalOpen: false,
  testingIds: [],
  autoCheck: true,
  autoCheckRunning: false,
  isBatchImportOpen: false,
  // 复制态：非空表示当前弹窗是从某条实例「复制」而来（仍走新增保存链路）
  copySourceId: null,
  copySourceLabel: '',
})

const filters = reactive({
  status: undefined,
  dbType: undefined,
  environment: undefined,
  search: '',
})

const uiData = reactive({
  environments: [],
  tableData: [],
  // 全量实例的 {id, hostname, port, remark} 轻量快照，仅用于「复制」/新增时的 ip:port 冲突预校验。
  // 不能复用 tableData —— 那是当前分页的数据，翻页后地址簿会残缺。
  addressBook: [],
})

const userStore = useUserStore()
const canManageInstance = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.instance.manage'),
)
const canTestInstance = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.instance.test'),
)
const canSyncMetaInstance = computed(
  () => !!userStore?.is_superuser || (userStore?.permissions || []).includes('system.instance.sync_meta'),
)

// 弹窗标题三态：复制 / 编辑 / 新增。
// 注意「复制」标题不含「编辑」二字 —— 弹窗内部靠 title.includes('编辑') 判定编辑态，
// 复制必须走新增语义（密码必填、不回写原实例）。
const modalTitle = computed(() => {
  if (uiState.copySourceId) return '复制数据库实例'
  return uiState.isEditMode ? '编辑数据库实例' : '新增数据库实例'
})

const selectedRowKeys = ref([])

// ===== 表头列筛选（所属环境 / 数据库类型 / 状态）=====
// 选项全部从当前页数据派生，保证下拉里覆盖页面出现的所有值；
// 过滤是客户端行为（作用于当前页数据），不改变服务端查询参数。
const uniqueFilterOptions = (values) =>
  [...new Set(values.map((v) => String(v || '').trim()).filter(Boolean))]
    .sort((a, b) => a.localeCompare(b, 'zh'))
    .map((v) => ({ text: v, value: v }))

// 类型列按「类型名」过滤（不带版本）：同类型不同版本的行被同一项命中，
// OceanBase 会按租户模式拆成 OceanBase (MySQL) / OceanBase (Oracle) 两项
const dbTypeFilterValue = (record) => {
  const raw = String(record?.db_type || '').trim()
  return raw ? dbTypeDisplayName(raw, record?.extra_config) : ''
}

const columnFilterState = reactive({
  environment_name: null,
  db_type_version: null,
  status: null,
})

const tableColumns = computed(() => [
  {
    title: '数据源',
    dataIndex: 'data_source_id',
    key: 'data_source_id',
    width: 220,
    fixed: 'left',
    align: 'left',
  },
  {
    title: '环境',
    dataIndex: 'environment_name',
    key: 'environment_name',
    width: 120,
    align: 'center',
    // 表头筛选器整组居中（与表体居中一致），customHeaderCell 见下方 CSS
    customHeaderCell: () => ({ class: 'env-header-cell' }),
    filters: uniqueFilterOptions(tableRows.value.map((row) => row.environment_name)),
    filteredValue: columnFilterState.environment_name,
    onFilter: (value, record) => String(record.environment_name || '').trim() === value,
  },
  {
    title: '数据源IP',
    key: 'connection_address',
    width: 220,
    align: 'left',
  },
  {
    title: '数据库类型',
    key: 'db_type_version',
    // 210：容纳「OceanBase (Oracle) 4.2.1.0」这类「类型 + 租户模式 + 版本」的组合，
    // 并为行首 16px 类型 logo 预留空间
    width: 210,
    align: 'left',
    filters: uniqueFilterOptions(tableRows.value.map((row) => dbTypeFilterValue(row))),
    filteredValue: columnFilterState.db_type_version,
    onFilter: (value, record) => dbTypeFilterValue(record) === value,
  },
  {
    title: '创建人',
    dataIndex: 'created_by',
    key: 'created_by',
    width: 120,
    align: 'left',
  },
  {
    title: '创建时间',
    dataIndex: 'created_at',
    key: 'created_at',
    width: 180,
    align: 'left',
  },
  {
    title: '状态',
    key: 'status',
    // 120：只剩「● 状态文字」，重试/详情已迁至操作列
    width: 120,
    fixed: 'right',
    align: 'center',
    filters: uniqueFilterOptions(tableRows.value.map((row) => formatStatusText(row.last_check_status))),
    filteredValue: columnFilterState.status,
    onFilter: (value, record) => formatStatusText(record.last_check_status) === value,
  },
  {
    title: '操作',
    key: 'action',
    width: 120,
    fixed: 'right',
    align: 'center',
  },
])

const tableRows = computed(() => uiData.tableData || [])

const rowSelection = computed(() => ({
  selectedRowKeys: selectedRowKeys.value,
  onChange: (keys) => {
    selectedRowKeys.value = keys
  },
}))

const defaultForm = {
  environment: '',
  db_type: '',
  hostname: '',
  port: null,
  user: '',
  password: '',
  extra_config: {},
  database: '',
  sslmode: '',
  auth_source: '',
  service_name: '',
  // OceanBase 租户模式（表单展示字段，提交前会折进 extra_config.oceanbase_mode）
  oceanbase_mode: 'mysql',
  remark: '',
  // 录入型画像字段：部署形态 / 云厂商 / 高可用模式（监控概览要展示，但探测不出来）
  deploy_provider: '',
  cloud_vendor: '',
  ha_mode: '',
  disk_type: '',
}
const formState = ref({ ...defaultForm })

const pagination = reactive({
  current: 1,
  pageSize: 10,
  total: 0,
  pageSizeOptions: ['10', '20', '50', '100'],
  showSizeChanger: true,
  position: ['bottomRight'],
  showTotal: (total) => `共 ${total} 条`,
})

const resetToFirstPage = () => {
  pagination.current = 1
}

const fetchData = async () => {
  uiState.loading = true
  const params = {
    page_size: pagination.pageSize,
    page: pagination.current,
    is_page: true,
    search: filters.search,
    db_type: filters.dbType,
    environment: filters.environment,
    status: filters.status,
  }
  const res = await getInstancesApi(params).catch(() => null)
  if (res) {
    pagination.total = res.total || 0
    uiData.tableData = res.data || []
  }
  uiState.loading = false
  syncTableScrollbarWidth()
}

// 表头为表体垂直滚动条预留的占位列宽度需与表体实际占用一致，否则右侧固定列
// （状态 / 操作）表头与表体竖线错位：antd 用固定测量值占位，而 overlay 滚动条
// （macOS）实际不占宽度。这里把真实宽度写进 CSS 变量，表头占位列跟随。
const syncTableScrollbarWidth = () => {
  nextTick(() => {
    const bodyEl = document.querySelector('.instance-table .ant-table-body')
    if (!bodyEl) return
    const width = Math.max(0, bodyEl.offsetWidth - bodyEl.clientWidth)
    document.documentElement.style.setProperty('--gi-inst-scrollbar-w', `${width}px`)
  })
}

// 拉取全量实例地址簿（is_page=false 走后端「不分页」分支，一次拿全）。
// 只保留冲突校验需要的字段，避免把 extra_config 等敏感负载透传给弹窗。
const fetchAddressBook = async () => {
  const res = await getInstancesApi({ is_page: false }).catch(() => null)
  const rows = res?.data || []
  uiData.addressBook = rows.map((row) => ({
    id: row.id,
    hostname: row.hostname,
    port: row.port,
    remark: row.remark,
    data_source_id: row.data_source_id || row.instance_id,
  }))
}

const copyTextFallback = (text) => {
  try {
    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.style.position = 'fixed'
    textarea.style.top = '-1000px'
    textarea.style.left = '-1000px'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.focus()
    textarea.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(textarea)
    return ok
  } catch {
    return false
  }
}

// 复制文本到剪贴板（含 http 非安全上下文 fallback），返回是否成功
const writeClipboard = async (text) => {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
    return copyTextFallback(text)
  } catch {
    return copyTextFallback(text)
  }
}

const copyToClipboard = async (text, successText) => {
  const value = String(text || '').trim()
  if (!value) return
  if (await writeClipboard(value)) {
    message.success(successText)
  } else {
    message.error('复制失败，请手动选择文本复制')
  }
}

const copyDataSourceId = (record) => copyToClipboard(record?.data_source_id, '已复制数据源 ID')

// 探活失败详情（原状态列 popover 迁入「更多」菜单后，改用 Modal 承载）
const showStatusDetailModal = (record) => {
  const detail = String(record?.last_check_message || '').trim()
  Modal.info({
    title: `探活详情 · ${record?.data_source_id || ''}`,
    width: 640,
    centered: true,
    content: createVNode('div', {}, [
      createVNode('div', { style: 'display:flex; align-items:center; justify-content:space-between; margin-bottom:8px;' }, [
        createVNode('span', { style: 'font-weight:500;' }, '原始错误'),
        createVNode(
          'a',
          {
            style: 'font-size:13px;',
            onClick: () => copyStatusDetail(record),
          },
          '复制',
        ),
      ]),
      createVNode(
        'pre',
        {
          style: 'max-height:320px; overflow:auto; margin:0; padding:10px 12px; background:rgba(5,17,25,0.04); border-radius:6px; white-space:pre-wrap; word-break:break-all; font-size:12.5px;',
        },
        detail || '暂无错误详情',
      ),
    ]),
    okText: '知道了',
  })
}

const getEnvironments = async () => {
  const res = await getEnvironmentsApi().catch(() => null)
  uiData.environments = res?.data || []
}

const handleSearch = (value) => {
  filters.search = value || ''
  resetToFirstPage()
  fetchData()
}

const handleTableChange = (pager, tableFilters) => {
  // 表头列筛选为客户端过滤，只更新受控 filteredValue，不重新请求服务端
  if (tableFilters) {
    columnFilterState.environment_name = tableFilters.environment_name || null
    columnFilterState.db_type_version = tableFilters.db_type_version || null
    columnFilterState.status = tableFilters.status || null
  }
  const pageChanged =
    pager.current !== pagination.current || pager.pageSize !== pagination.pageSize
  pagination.current = pager.current
  pagination.pageSize = pager.pageSize
  if (pageChanged) fetchData()
}

const handleAdd = () => {
  uiState.isEditMode = false
  uiState.copySourceId = null
  uiState.copySourceLabel = ''
  formState.value = { ...defaultForm }
  getEnvironments()
  fetchAddressBook()
  uiState.isModalOpen = true
}

// ===== 复制实例 =====
const COPY_REMARK_SUFFIX = '（副本）'

const appendCopySuffix = (remark) => {
  const base = String(remark || '').trim()
  if (!base) return base
  if (base.endsWith(COPY_REMARK_SUFFIX)) return base
  const next = base + COPY_REMARK_SUFFIX
  // 后端 remark 上限 256，超长时保持原名而不是截断
  return next.length <= 256 ? next : base
}

const parseExtraConfigObject = (raw) => {
  if (!raw) return {}
  if (typeof raw === 'object') return { ...raw }
  try {
    const parsed = JSON.parse(String(raw))
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

// 复制时只搬「配置」，不搬「凭据」：密码是密文无法回显、SSH 密码是密文、
// SSH 私钥虽然明文但属于凭据，全部剔除后由用户在弹窗里重新填写。
const pickCopyExtraConfig = (raw) => {
  const cfg = parseExtraConfigObject(raw)
  delete cfg.ssh_password
  delete cfg.ssh_private_key
  delete cfg.password
  delete cfg.ssh_passphrase
  return cfg
}

const handleCopy = async (record) => {
  // 复制前刷新地址簿：冲突提示要在弹窗打开那一刻就是准的
  await fetchAddressBook()
  uiState.isEditMode = false
  uiState.copySourceId = record?.id ?? null
  uiState.copySourceLabel =
    String(record?.remark || '').trim() || String(record?.data_source_id || '').trim() || `#${record?.id ?? ''}`
  formState.value = {
    ...defaultForm,
    environment: record?.environment ?? '',
    db_type: record?.db_type || '',
    hostname: record?.hostname || '',
    port: record?.port ?? null,
    user: record?.user || '',
    extra_config: pickCopyExtraConfig(record?.extra_config),
    remark: appendCopySuffix(record?.remark),
    deploy_provider: record?.deploy_provider || '',
    cloud_vendor: record?.cloud_vendor || '',
    ha_mode: record?.ha_mode || '',
    disk_type: record?.disk_type || '',
    // 凭据一律留空，强制重新录入
    password: '',
    ssh_password: '',
    ssh_private_key: '',
  }
  getEnvironments()
  uiState.isModalOpen = true
}

const handleModalOpenChange = (open) => {
  uiState.isModalOpen = open
  if (!open) {
    uiState.copySourceId = null
    uiState.copySourceLabel = ''
  }
}

const handleBatchImport = () => {
  getEnvironments()
  uiState.isBatchImportOpen = true
}

const handleBatchImportDone = async ({ needRefresh = false } = {}) => {
  if (needRefresh) {
    await fetchData()
    fetchAddressBook()
  }
}

const handleEdit = (record) => {
  uiState.isEditMode = true
  uiState.copySourceId = null
  uiState.copySourceLabel = ''
  formState.value = {
    ...record,
    password: '',
  }
  getEnvironments()
  fetchAddressBook()
  uiState.isModalOpen = true
}

const pushTestingId = (id) => {
  if (!uiState.testingIds.includes(id)) {
    uiState.testingIds.push(id)
  }
}

const popTestingId = (id) => {
  uiState.testingIds = uiState.testingIds.filter((item) => item !== id)
}

const handleTestConnection = useThrottleFn(async (record) => {
  pushTestingId(record.id)
  try {
    const res = await testInstanceConnectionApi(record.id, {
      silentBusinessError: true,
      silentHttpError: true,
    })
    if (res) {
      await fetchData()
    }
  } catch {
    await fetchData()
  } finally {
    popTestingId(record.id)
  }
}, 800)

// 页面级补测：**不再自建 30 秒轮询**。常态探活已交给服务端后台任务
// （internal/common/services/instance_probe.go：异常实例 60s 复测、可用实例 10min 兜底），
// 页面只在打开/重新开启开关时补测一轮，避免浏览器标签页各自重复探测同一批实例。
const runAutoProbeOnce = async () => {
  if (!uiState.autoCheck || uiState.autoCheckRunning || uiState.loading) return
  const candidates = (tableRows.value || []).filter((row) => normalizeStatus(row.last_check_status) !== 'success')
  if (!candidates.length) return
  uiState.autoCheckRunning = true
  const batch = candidates.slice(0, 3)
  // 让被补测的行显示 loading，用户能看出"正在探"，而不是页面像死了一样。
  batch.forEach((record) => pushTestingId(record.id))
  try {
    for (const record of batch) {
      await testInstanceConnectionApi(record.id, {
        silentBusinessError: true,
        silentHttpError: true,
      }).catch(() => null)
    }
    await fetchData()
  } finally {
    batch.forEach((record) => popTestingId(record.id))
    uiState.autoCheckRunning = false
  }
}

const handleSyncMeta = useThrottleFn(async (record) => {
  const key = `instance-sync-meta-${record?.id || ''}`
  message.loading({ content: '正在同步元数据...', key, duration: 0 })
  const res = await syncInstanceMetaApi(record.id).catch(() => null)
  if (res) {
    message.success({ content: '元数据同步成功', key, duration: 2 })
  } else {
    message.destroy(key)
  }
}, 800)

// ---------------------------------------------------------------------------
// 行内修改数据源名称：气泡卡片里改，保存走只更新 remark 的轻量接口
// ---------------------------------------------------------------------------

const renameState = reactive({
  // 当前打开改名气泡的实例主键；draft 是输入框草稿
  id: null,
  draft: '',
  savingId: null,
})

const handleRenameOpenChange = (open, record) => {
  if (open) {
    renameState.id = record.id
    renameState.draft = String(record.remark || '')
  } else if (renameState.id === record.id) {
    renameState.id = null
    renameState.draft = ''
  }
}

const cancelRename = () => {
  renameState.id = null
  renameState.draft = ''
}

const confirmRename = useThrottleFn(async (record) => {
  if (renameState.savingId) return
  const remark = String(renameState.draft || '').trim()
  if (remark.length < 2) {
    message.warning('数据源名称至少 2 个字符')
    return
  }
  if (remark === String(record.remark || '').trim()) {
    cancelRename()
    return
  }
  renameState.savingId = record.id
  const res = await updateInstanceRemarkApi(record.id, remark).catch(() => null)
  renameState.savingId = null
  if (!res) return
  // 本地直接改，避免为改个名字整表刷新（分页/滚动位置都会跳）
  const row = (uiData.tableData || []).find((item) => item.id === record.id)
  if (row) row.remark = remark
  message.success('数据源名称已更新')
  cancelRename()
}, 600)

const copyStatusDetail = async (record) => {  const text = String(record?.last_check_message || '').trim()
  if (!text) return
  let ok = false
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      ok = true
    } else {
      ok = copyTextFallback(text)
    }
  } catch {
    ok = copyTextFallback(text)
  }
  if (ok) {
    message.success('已复制错误信息')
  } else {
    message.error('复制失败，请手动选择文本复制')
  }
}

const onSubmit = useThrottleFn(async (data) => {
  if (uiState.isEditMode && (!data.password || !(data.password + '').trim())) {
    const { password, ...rest } = data
    void password
    data = rest
  }
  const isEditMode = uiState.isEditMode
  const res = isEditMode
    ? await updateInstancesApi(data).catch(() => null)
    : await createInstancesApi(data).catch(() => null)
  if (res) {
    message.success('操作成功')
    handleModalOpenChange(false)
    await fetchData()
    // 新增/编辑都会改变地址簿，刷新以便下次复制时冲突判断准确
    fetchAddressBook()

    if (!isEditMode) {
      const createdID = res?.data?.id
      if (createdID) {
        await handleSyncMeta({ id: createdID })
      }
    }
  }
}, 800)

// ============================================================================
// 删除数据源被拒时的「阻塞清单」
//
// 后端在实例仍被备份任务 / 恢复记录引用时会拒绝删除，并回传阻塞明细
// （backup_tasks / restore_runs / active_items）。这里把它整理成人能直接用的清单：
// 每条记录都带一个跳转入口，用户点过去处理完再回来删。
// 注意备份域的关联列存的是 insight_instances 主键，所以跳转带的是 record.id。
// ============================================================================
const BLOCKING_KIND_TEXT = {
  backup_task: '备份任务',
  backup_run: '备份执行',
  restore_run: '恢复记录',
}

const blockingState = reactive({
  open: false,
  items: [],
})

const restoreRelationText = (relation) => {
  if (relation === 'source') return '恢复记录（源实例）'
  if (relation === 'target') return '恢复记录（目标实例）'
  return '恢复记录'
}

const buildBlockingResources = (payload) => {
  const resources = []
  const push = (raw, fallbackKind, kindText, actionText, active) => {
    resources.push({
      kind: raw.kind || fallbackKind,
      kindText,
      name: String(raw.name || raw.id || '').trim() || '未命名记录',
      status: String(raw.status || '').trim(),
      active: !!active,
      actionText,
    })
  }
  ;(Array.isArray(payload.backup_tasks) ? payload.backup_tasks : []).forEach((item) =>
    push(item, 'backup_task', BLOCKING_KIND_TEXT.backup_task, '去删除备份任务', false),
  )
  ;(Array.isArray(payload.restore_runs) ? payload.restore_runs : []).forEach((item) =>
    push(item, 'restore_run', restoreRelationText(item.relation), '去处理恢复记录', false),
  )
  ;(Array.isArray(payload.active_items) ? payload.active_items : []).forEach((item) =>
    push(
      item,
      'backup_run',
      item.kind === 'restore_run' ? '恢复执行中' : '备份执行中',
      item.kind === 'restore_run' ? '去处理恢复记录' : '去查看执行',
      true,
    ),
  )
  return resources
}

// 把后端阻塞响应整理成弹窗条目；不是阻塞响应时返回 null（交由全局提示兜底）
const normalizeBlockingPayload = (payload, record) => {
  if (!payload || typeof payload !== 'object') return null
  const resources = buildBlockingResources(payload)
  if (!resources.length) return null
  const address =
    String(payload.host_port || '').trim() ||
    [record?.hostname, record?.port].filter(Boolean).join(':')
  return {
    key: String(payload.id || record?.id || address),
    id: payload.id || record?.id,
    instanceId: String(payload.instance_id || record?.instance_id || ''),
    label: address || `数据源 #${payload.id || record?.id || ''}`,
    resources,
  }
}

const openBlockingModal = (items) => {
  blockingState.items = items
  blockingState.open = true
}

const closeBlockingModal = () => {
  blockingState.open = false
  blockingState.items = []
}

const gotoBlockingResource = (item, resource) => {
  closeBlockingModal()
  if (resource.kind === 'restore_run') {
    router.push({ path: '/backup/restore' })
    return
  }
  // 备份任务与备份执行都在「数据备份」页；后端按主键 source_instance_id 过滤，
  // 顺带把业务实例号带上，便于任务页在没有实例记录时也能显示上下文。
  const query = { instance_id: item.id }
  if (item.instanceId) query.instance_uuid = item.instanceId
  router.push({ name: 'backup.task', query })
}

// 删除单个数据源：被拒且带阻塞明细时不再全局弹 toast，改由弹窗给出处理入口
const submitDeleteInstance = async (record) => {
  try {
    await deleteInstancesApi(record.id, { silentBusinessError: true })
    return { ok: true, id: record.id }
  } catch (error) {
    const blocking = normalizeBlockingPayload(error?.data, record)
    return { ok: false, blocking }
  }
}

const handleDelete = useThrottleFn(async (record) => {
  const result = await submitDeleteInstance(record)
  if (result.ok) {
    message.success('删除成功')
    selectedRowKeys.value = selectedRowKeys.value.filter((item) => item !== record.id)
    await fetchData()
    fetchAddressBook()
    return
  }
  if (result.blocking) {
    openBlockingModal([result.blocking])
  }
}, 800)

const handleBatchDelete = async () => {
  if (!selectedRowKeys.value.length) return
  const ids = [...selectedRowKeys.value]
  const rowMap = new Map((uiData.tableData || []).map((row) => [row.id, row]))
  const blocked = []
  let successCount = 0
  for (const id of ids) {
    // 顺序删除，兼容当前后端仅支持单删
    // 逐条失败时保留后续项，减少一次性批量失败影响
    const result = await submitDeleteInstance(rowMap.get(id) || { id })
    if (result.ok) {
      successCount += 1
      continue
    }
    if (result.blocking) blocked.push(result.blocking)
  }
  selectedRowKeys.value = []
  if (successCount > 0) {
    message.success(`已删除 ${successCount} 个数据源`)
    await fetchData()
    fetchAddressBook()
  }
  if (blocked.length) {
    openBlockingModal(blocked)
  } else if (successCount === 0) {
    message.warning('没有数据源被删除，请查看提示后重试')
  }
}

onMounted(async () => {
  const editInstanceId = String(route.query?.edit_instance_id || '').trim()
  const statusQuery = String(route.query?.status || '').trim()
  const searchQuery = String(route.query?.search || '').trim()
  if (editInstanceId) {
    filters.search = editInstanceId
  } else if (searchQuery) {
    filters.search = searchQuery
  }
  if (statusQuery) {
    filters.status = statusQuery
  }
  await Promise.all([getEnvironments(), fetchData(), fetchAddressBook()])
  if (editInstanceId) {
    const record =
      (uiData.tableData || []).find((row) => String(row.instance_id) === editInstanceId) ||
      (uiData.tableData || []).find((row) => String(row.id) === editInstanceId) ||
      (uiData.tableData || []).find((row) => String(row.data_source_id) === editInstanceId)
    if (record) {
      handleEdit(record)
      router.replace({ path: route.path, query: {} })
    }
  }
  // 打开页面补测一轮（服务端后台探活不受页面开关影响，详见 runAutoProbeOnce 注释）。
  runAutoProbeOnce()
  // 视口变化会改变表体滚动条占位，需重新对齐表头占位列
  window.addEventListener('resize', syncTableScrollbarWidth)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', syncTableScrollbarWidth)
})

// 其他页面带 ?search=<实例ID> 跳转进来时（如「数据安全 → 实例」），
// 本页被 keepAlive 缓存后 onMounted 不会再执行，这里补一次参数同步。
onActivated(() => {
  const searchQuery = String(route.query?.search || '').trim()
  if (!searchQuery || searchQuery === filters.search) return
  filters.search = searchQuery
  resetToFirstPage()
  fetchData()
})

watch(
  () => uiState.autoCheck,
  (enabled) => {
    if (enabled) {
      runAutoProbeOnce()
    }
  },
)</script>

<style scoped>
.instance-list-page-shell {
  height: 100%;
  overflow: hidden;
  gap: 10px;
  padding: 10px 16px 4px;
}

/* 标题行固定在卡片之外，不参与压缩 */
.instance-list-page-shell > .gi-page-toolbar {
  flex: 0 0 auto;
}

.instance-page-card {
  border-radius: 12px;
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}
.instance-page-card :deep(.ant-card-body) {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
  padding: 0 20px 4px;
  gap: 8px;
}

.instance-page-card :deep(.gi-table-section) {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}

.instance-page-card :deep(.gi-table-section .instance-table),
.instance-page-card :deep(.gi-table-section .ant-table-wrapper),
.instance-page-card :deep(.gi-table-section .ant-spin-nested-loading),
.instance-page-card :deep(.gi-table-section .ant-spin-container) {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}
.instance-page-card :deep(.gi-table-section .ant-spin-container > ul.ant-pagination),
.instance-page-card :deep(.gi-table-section .ant-spin-container > div.ant-pagination) {
  flex: 0 0 auto;
  margin: 4px 0 0;
  padding: 2px 0 2px;
}

.instance-page-card :deep(.ant-table-body) {
  max-height: calc(100vh - 310px);
  overflow-y: auto !important;
}

/* 工具栏右侧（搜索框 + 自动检测开关 + 3 按钮）见公共规范 .gi-toolbar-single-line。
   宽度不足时不折行，由整行横向滚动兜底：整组不收缩，只留搜索框可收缩。 */
.instance-toolbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: nowrap;
  flex: 0 0 auto;
  white-space: nowrap;
}

.auto-check-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding-inline: 10px;
  flex: 0 0 auto;
}

.instance-search {
  flex: 0 1 220px;
  min-width: 132px;
}

/* 搜索框只留一个放大镜：去掉 antd 搜索按钮组的浅灰底色与分隔边框，
   与普通输入框视觉连续 */
.instance-search :deep(.ant-input-group-addon) {
  background: transparent;
  border-left: 0;
}

.instance-search :deep(.ant-input-search-button) {
  background: transparent;
  border-left: 0;
  color: rgba(0, 0, 0, 0.45);
}

.instance-search :deep(.ant-input-search-button:hover) {
  color: #0f766e;
}

.instance-table :deep(.ant-table-pagination.ant-pagination) {
  margin: 10px 0 0;
}

.data-source-id-cell {
  display: flex;
  align-items: center;
  color: rgba(0, 0, 0, 0.72);
  min-width: 0;
}

/* 两行文字区：ID 行 + 名称行 */
.data-source-id-main {
  display: flex;
  flex-direction: column;
  /* 两行间距 2px：原 4px 的一半 */
  gap: 2px;
  min-width: 0;
  flex: 1 1 auto;
}

.data-source-id-row {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.data-source-id-text {
  font-weight: 500;
  color: #0f766e;
}

.copy-id-btn {
  padding: 0;
  width: 20px;
  height: 20px;
  color: rgba(0, 0, 0, 0.45);
  flex: 0 0 auto;
}

.copy-id-btn:hover {
  color: #0f766e;
}

.data-source-name-row {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  min-width: 0;
  max-width: 100%;
}

/* 名称行：名称 + 改名铅笔（复制 ID 在 ID 行） */
.data-source-name-text {
  display: inline-block;
  min-width: 0;
  /* 右侧一个 20px 图标 + 间距，避免名称把按钮挤出去 */
  max-width: calc(100% - 24px);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rename-btn {
  padding: 0;
  width: 20px;
  height: 20px;
  color: rgba(0, 0, 0, 0.45);
  flex: 0 0 auto;
}

.rename-btn:hover {
  color: #0f766e;
}

.connection-address-text {
  color: rgba(0, 0, 0, 0.72);
}

/* 数据库类型列：logo + 类型/版本文本 */
.db-type-cell {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.db-type-logo {
  /* 20px：与全站 dbTypeLogo 品牌资源统一尺寸 */
  width: 20px;
  height: 20px;
  object-fit: contain;
  flex: 0 0 auto;
}

/* 数据库类型列：与连接地址同款文字色；类型+版本理论上很短，
   仍按本页惯例做超长省略兜底（完整值见悬浮 title） */
.db-type-text {
  display: inline-block;
  min-width: 0;
  max-width: 100%;
  flex: 0 1 auto;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
  color: rgba(0, 0, 0, 0.72);
}

.status-cell {
  display: flex;
  align-items: center;
  /* 状态列表头与表体均居中：表头由列 align="center" 控制，表体在这里补齐 */
  justify-content: center;
  gap: 8px;
  flex-wrap: nowrap;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #d9d9d9;
  flex: 0 0 auto;
}

.status-dot.is-success {
  background: #52c41a;
}

.status-dot.is-error {
  background: #ff4d4f;
}

.status-dot.is-unknown {
  background: #d9d9d9;
}

.status-text {
  min-width: 56px;
  text-align: left;
}

/* 重试按钮：与「更多」同款 text 按钮，紧跟其前 */
.retry-action-btn {
  color: #0f766e;
}

.retry-action-btn:hover,
.retry-action-btn:focus {
  color: #149188;
  background: transparent;
}

.action-cell {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 2px;
}

.more-action-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: rgba(0, 0, 0, 0.65);
  padding: 0;
  border: 0;
  box-shadow: none;
  background: transparent;
}

.more-action-btn:hover,
.more-action-btn:focus,
.more-action-btn:active {
  border-color: transparent;
  background: transparent;
  box-shadow: none;
  color: rgba(0, 0, 0, 0.88);
}

.danger-action {
  color: #ff4d4f;
}

/* 表头筛选器紧贴列标题文字：antd 默认 space-between 把漏斗推到单元格最右侧，
   且 trigger 带 -16px 的右外边距，这里都收回（与数据源总览页同款处理） */
.instance-table :deep(.ant-table-filter-column) {
  justify-content: flex-start;
}

/* 漏斗紧贴列标题：antd 默认 trigger 有 margin-inline-start 4px + 左右 4px padding，
   视觉上离标题 8px；这里全部收掉，只留图标自身 2px 留白 */
.instance-table :deep(.ant-table-filter-trigger) {
  margin-inline-start: 0;
  margin-inline-end: 0;
  padding-inline: 2px;
}

/* 环境列表头居中：筛选器整组（文字 + 漏斗）居中，与表体居中一致，
   漏斗仍紧贴文字（customHeaderCell 加的 class，见列定义） */
.instance-table :deep(.env-header-cell .ant-table-filter-column) {
  justify-content: center;
}

/* 右侧固定列（状态 / 操作）表头与表体竖线错位的修复：
   1）表头占位列宽度 = 表体真实滚动条宽度（antd 用固定测量值占位，overlay 滚动条
      （macOS）实际不占宽度，两端基准不一致）；
   2）antd 计算表头固定右列的 sticky right 时也会把「假定的滚动条宽度」算进去
      （实测状态列 right:128 / 操作列 right:8，而表体对应 120 / 0），
      这里按不含滚动条的期望值直接固定。
   位置由列序决定：倒数第 1 个 th 是滚动条占位列，倒数第 2 是操作列，倒数第 3 是状态列。
   ⚠️ 操作列宽 120 需与列定义（tableColumns 中状态/操作列 width）保持一致。 */
.instance-table :deep(.ant-table-header colgroup col:last-child),
.instance-table :deep(.ant-table-cell-scrollbar) {
  width: var(--gi-inst-scrollbar-w, 8px) !important;
  min-width: var(--gi-inst-scrollbar-w, 8px) !important;
  max-width: var(--gi-inst-scrollbar-w, 8px) !important;
  padding: 0 !important;
}

.instance-table :deep(.ant-table-thead th.ant-table-cell-fix-right:nth-last-child(2)) {
  right: 0 !important;
}

.instance-table :deep(.ant-table-thead th.ant-table-cell-fix-right:nth-last-child(3)) {
  right: 120px !important;
}

/* 删除被拒弹窗：阻塞清单 */
.blocking-instance-block {
  margin-top: 12px;
  padding: 10px 12px;
  border: 1px solid rgba(0, 0, 0, 0.08);
  border-radius: 8px;
}

.blocking-instance-title {
  margin-bottom: 6px;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.88);
}

.blocking-resource-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.blocking-kind {
  flex: 0 0 auto;
  color: rgba(0, 0, 0, 0.45);
  font-size: 12px;
}

.blocking-resource-name {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: rgba(0, 0, 0, 0.72);
}

.blocking-modal-footer {
  margin-top: 16px;
  text-align: right;
}

@media (max-width: 768px) {
  /* 视口矮时表体被 calc(100vh - 310px) 压到只剩一两行 */
  .instance-page-card :deep(.ant-table-body) {
    max-height: calc(100vh - 210px) !important;
  }

  /* 复制 ID / 改名按钮原本 20x20 */
  .copy-id-btn,
  .rename-btn {
    width: 32px;
    height: 32px;
  }

  .instance-list-page-shell {
    padding: 8px 10px 4px;
  }
}

/* 重命名气泡挂在 body 上，需非 scoped 覆盖 */
</style>

<!-- 改名气泡 teleport 到 body，scoped 选择器够不到，须放非 scoped 块 -->
<style>
.instance-rename-popover .ant-popover-inner-content {
  padding: 12px;
}

.instance-rename-popover .rename-popover-body {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 260px;
}

.instance-rename-popover .rename-popover-body .ant-input {
  flex: 1 1 auto;
  min-width: 0;
}
@media (max-width: 768px) {
  .instance-rename-popover .rename-popover-body {
    width: min(260px, calc(100vw - 32px));
  }
}

@media (max-width: 768px) {
  /* 视口矮时表体被 calc(100vh - 310px) 压到只剩一两行 */
  .instance-page-card :deep(.ant-table-body) {
    max-height: calc(100vh - 210px) !important;
  }

  /* 复制 ID / 改名按钮原本 20x20 */
  .copy-id-btn,
  .rename-btn {
    width: 32px;
    height: 32px;
  }

  .instance-list-page-shell {
    padding: 8px 10px 4px;
  }
}

/* 重命名气泡挂在 body 上，需非 scoped 覆盖 */
@media (max-width: 768px) {
  .instance-rename-popover .rename-popover-body {
    width: min(260px, calc(100vw - 32px));
  }
}
</style>
