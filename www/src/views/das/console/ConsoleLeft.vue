<template>
  <div class="console-left-root" :style="{ height: leftHeight }" @contextmenu.prevent>
    <div class="tree-area">
        <div class="tree-toolbar">
        <div
          class="search-combo"
          :class="{ 'is-disabled': !uiData.selectedInstanceId, 'is-focused': searchFocused }"
        >
          <span class="search-combo-prefix">
            <SearchOutlined />
          </span>
          <input
            ref="searchInputRef"
            class="search-combo-input"
            :value="uiData.resourceTreeKeyword"
            :placeholder="searchPlaceholder"
            :disabled="!uiData.selectedInstanceId"
            @focus="searchFocused = true"
            @blur="searchFocused = false"
            @input="handleDebouncedSearchInput($event.target.value)"
            @keydown.enter="handleSearch(uiData.resourceTreeKeyword)"
            @keydown.esc="handleClearSearch"
          />
          <span v-if="searchHitCountInfo.visible" class="search-hit-badge" :class="searchHitCountInfo.level">
            {{ searchHitCountInfo.text }}
          </span>
          <span v-if="uiData.resourceTreeKeyword" class="search-combo-clear" @click="handleClearSearch">
            <CloseOutlined />
          </span>
          <span class="search-combo-divider"></span>
          <a-dropdown :trigger="['click']" placement="bottomRight">
            <button
              type="button"
              class="search-combo-icon-button"
              :disabled="!uiData.selectedInstanceId"
              @click.stop
            >
              <DownOutlined />
            </button>
            <template #overlay>
              <a-menu @click="({ key }) => handleFilterMenuClick(key)">
                <a-menu-item key="searchGlobal">
                  <span class="menu-item-title">全局搜索对象</span>
                  <span class="menu-item-hint">⌘K / Ctrl+K</span>
                </a-menu-item>
                <a-menu-item key="expandHit">
                  <span class="menu-item-title">展开所有命中节点</span>
                </a-menu-item>
                <a-menu-divider />
                <a-menu-item key="clearSearchHistory">
                  <span class="menu-item-title">清空搜索历史</span>
                </a-menu-item>
              </a-menu>
            </template>
          </a-dropdown>
        </div>
      </div>
      <a-alert
        v-if="uiState.offlineMessage"
        class="offline-alert"
        type="warning"
        show-icon
        closable
        @close="uiState.offlineMessage = ''"
      >
        <template #message>实例当前不可用</template>
        <template #description>{{ uiState.offlineMessage }}</template>
      </a-alert>
      <!-- 2026-11-10：实例列表加载失败与「无授权」是两回事。
           此前 fetchInstances 的 .catch(() => {}) 会把 401/5xx/网络异常一律吞掉，
           左树静默渲染「暂无授权数据源」，错误被伪装成权限问题。现在失败态单独渲染并提供重试。 -->
      <div v-if="uiState.instancesError" class="tree-error-tip">
        <div class="tree-error-text">实例列表加载失败：{{ uiState.instancesError }}</div>
        <a class="tree-error-retry" @click="handleRetryFetchInstances">重试</a>
      </div>
      <div v-else-if="!uiState.isTreeLoading && !uiData.instances.length" class="tree-empty-tip">暂无授权数据源，请联系管理员配置数据访问权限</div>
      <div v-else-if="!uiData.selectedInstanceId && !uiState.isTreeLoading" class="tree-empty-tip">选择数据源后，这里将展示数据库对象</div>
      <a-spin v-else class="tree-spin" :spinning="uiState.isTreeLoading" tip="加载中...">
        <div id="tree-container" @click.capture="handleTreeSwitcherClickCapture">
          <a-tree
            :tree-data="displayInstanceResourceTreeData"
            class="tree filter-tree"
            :selected-keys="uiData.instanceResourceSelectedKeys"
            :expanded-keys="uiData.instanceResourceExpandedKeys"
            :auto-expand-parent="uiData.instanceResourceAutoExpandParent"
            :load-data="loadInstanceResourceNode"
            :show-icon="false"
            block-node
            @select="handleInstanceResourceSelect"
            @expand="handleInstanceResourceExpand"
          >
            <template #title="nodeRecord">
              <div
                v-if="nodeRecord.nodeKind === 'emptyPlaceholder'"
                class="empty-placeholder-row"
                @contextmenu.prevent
                @click.stop
              >
                <span class="empty-placeholder-text">{{ nodeRecord.emptyPlaceholderText }}</span>
              </div>
              <a-dropdown v-else :trigger="['contextmenu']">
                <!-- 分组节点：统一文件夹图标，展开显示主题色、折叠为淡灰 -->
                <div
                  v-if="nodeRecord.nodeKind === 'group'"
                  class="tree-title-row group-node-row tree-title-row--full-row"
                  :data-node-key="nodeRecord.key"
                  @contextmenu.prevent.stop
                >
                  <span
                    class="tree-node-icon-prefix group-node-icon"
                    :class="{ 'is-expanded': isTreeNodeExpandedOrLeaf(nodeRecord, uiData.instanceResourceExpandedKeys) }"
                  >
                    <FolderOpenOutlined v-if="isTreeNodeExpandedOrLeaf(nodeRecord, uiData.instanceResourceExpandedKeys)" />
                    <FolderOutlined v-else />
                  </span>
                  <span class="tree-title-main group-node-label">{{ groupLabel(nodeRecord.group) }}</span>
                </div>
                <!-- 视图/存储过程/函数/触发器/事件/索引等对象节点 -->
                <div
                  v-else-if="nodeRecord.nodeKind === 'object'"
                  class="tree-title-row object-node-row tree-title-row--full-row"
                  :data-node-key="nodeRecord.key"
                  @dblclick.stop="handleNodeDblClick(nodeRecord)"
                  @contextmenu.prevent.stop
                >
                  <span class="tree-node-icon-prefix object-node-icon">
                    <component :is="groupIcon(objectGroupOf(nodeRecord))" />
                  </span>
                  <span
                    class="tree-title-main object-node-name"
                    :title="objectNodeTitle(nodeRecord)"
                    v-html="highlightKeyword(String(nodeRecord.title || ''))"
                  />
                  <span
                    v-if="nodeRecord.kind === 'index' && indexColumnsText(nodeRecord.attrs)"
                    class="tree-node-subtext index-node-columns"
                  >({{ indexColumnsText(nodeRecord.attrs) }})</span>
                </div>
                <div
                  v-else-if="nodeRecord.nodeKind === 'table'"
                  class="table-node-row table-node-row--full-row"
                  :data-node-key="nodeRecord.key"
                  @dblclick.stop="handleNodeDblClick(nodeRecord)"
                  @contextmenu.prevent.stop
                >
                  <div class="table-node-main">
                    <span class="table-node-name">
                      <PermissionHint
                        v-if="String(nodeRecord.title || '').split('#').length === 2"
                        :hasAccess="String(nodeRecord.title || '').split('#')[1] === 'allow'"
                      />
                      <span
                        class="name-text"
                        :title="String(nodeRecord.title || '').split('#')[0]"
                        v-html="highlightKeyword(String(nodeRecord.title || '').split('#')[0])"
                      />
                    </span>
                    <span v-if="nodeSizeText(nodeRecord)" class="tree-node-size">{{ nodeSizeText(nodeRecord) }}</span>
                  </div>
                </div>
                <div
                  v-else-if="nodeRecord.nodeKind === 'column'"
                  class="tree-title-row column-node-row tree-title-row--full-row"
                  @dblclick.stop="handleNodeDblClick(nodeRecord)"
                  @contextmenu.prevent.stop
                >
                  <span
                    class="tree-node-icon-prefix column-node-icon"
                    :class="{ 'is-expanded': isTreeNodeExpandedOrLeaf(nodeRecord, uiData.instanceResourceExpandedKeys), 'is-pk': nodeRecord.column_primary }"
                  ><KeyOutlined v-if="nodeRecord.column_primary" /><MenuOutlined v-else /></span>
                  <span
                    class="column-node-name"
                    :title="columnNodeTitle(nodeRecord)"
                    v-html="highlightKeyword(nodeRecord.column_name || getTreeNodeTitle(nodeRecord))"
                  />
                  <span v-if="nodeRecord.column_type" class="tree-node-subtext column-node-type">{{ nodeRecord.column_type }}</span>
                </div>
                <div
                  v-else
                  class="tree-title-row tree-title-row--full-row"
                  :data-node-key="nodeRecord.key"
                  @contextmenu.prevent.stop
                >
                  <span
                    v-if="nodeRecord.kind === 'database' ||
                          nodeRecord.nodeKind === 'database' ||
                          nodeRecord.resource?.resource_type === 'database'"
                    class="tree-node-icon-prefix"
                    :class="{ 'is-expanded': isTreeNodeExpandedOrLeaf(nodeRecord, uiData.instanceResourceExpandedKeys) }"
                  >
                    <DatabaseOutlined />
                  </span>
                  <span
                    v-else-if="nodeRecord.kind === 'schema' ||
                               nodeRecord.nodeKind === 'schema' ||
                               nodeRecord.resource?.resource_type === 'schema'"
                    class="tree-node-icon-prefix"
                    :class="{ 'is-expanded': isTreeNodeExpandedOrLeaf(nodeRecord, uiData.instanceResourceExpandedKeys) }"
                  >
                    <PartitionOutlined />
                  </span>
                  <span class="tree-title-main tree-node-name-flex" v-html="highlightKeyword(getTreeNodeTitle(nodeRecord))" />
                  <span v-if="nodeSizeText(nodeRecord)" class="tree-node-size">{{ nodeSizeText(nodeRecord) }}</span>
                </div>
                <template #overlay>
                  <a-menu @click="({ key: menuKey }) => onContextMenuClick(nodeRecord, menuKey)">
                    <a-menu-item key="refreshNodeChildren">刷新</a-menu-item>
                    <a-menu-item key="copyNodeValue">复制</a-menu-item>
                    <a-menu-item v-if="['resource'].includes(nodeRecord.kind)" key="insertUseSqlToEditor">插入切换语句</a-menu-item>
                    <a-menu-item v-if="nodeRecord.nodeKind === 'table' || isViewTreeNode(nodeRecord)" key="previewTableData">查看数据</a-menu-item>
                    <a-menu-item v-if="nodeRecord.nodeKind === 'table' && supportsTableStructureMenu" key="showTableStructure">查看表信息</a-menu-item>
                    <a-menu-item v-if="objectInfoMetaOf(nodeRecord)" key="showObjectInfo">查看{{ objectInfoMetaOf(nodeRecord).label }}信息</a-menu-item>
                  </a-menu>
                </template>
              </a-dropdown>
            </template>
          </a-tree>
        </div>
      </a-spin>

      <div class="server-search-panel" v-if="serverSearchHasPanel">
        <div class="server-search-panel-header">
          <span class="server-search-panel-title">
            {{ uiState.serverSearch.loading ? '搜索中…' : `搜索结果（${uiState.serverSearch.total}）` }}
          </span>
          <a-button size="small" type="text" @click="handleClearSearch">清空</a-button>
        </div>
        <a-spin :spinning="uiState.serverSearch.loading" wrapperClassName="server-search-panel-spin">
          <a-empty
            v-if="!uiState.serverSearch.loading && uiState.serverSearch.items.length === 0"
            description="未找到匹配对象"
            :image="Empty.PRESENTED_IMAGE_SIMPLE"
          />
          <div v-else class="server-search-list">
            <div
              v-for="item in uiState.serverSearch.items"
              :key="item.type + '::' + (item.database_name || '') + '::' + (item.schema_name || '') + '::' + (item.table_name || '')"
              class="server-search-item"
              :class="{ 'is-locating': uiState.locateInProgress }"
              @click="locateToObject(item)"
            >
              <div class="server-search-item-left">
                <span :class="['ss-item-type', 'ss-item-type--' + item.type]">
                  {{ item.type === 'database' ? '库' : item.type === 'schema' ? 'Schema' : '表' }}
                </span>
                <span
                  class="ss-item-title"
                  v-html="highlightKeyword(
                    item.type === 'database'
                      ? item.database_name
                      : item.type === 'schema'
                      ? (item.schema_name || item.database_name)
                      : item.table_name
                  )"
                ></span>
              </div>
              <div class="server-search-item-right">
                <span
                  class="ss-item-path"
                  :title="(item.database_name || '') + (item.schema_name ? '.' + item.schema_name : '')"
                >
                  {{ item.database_name }}
                  <template v-if="item.schema_name">.{{ item.schema_name }}</template>
                </span>
              </div>
            </div>
          </div>
        </a-spin>
      </div>
    </div>

    <a-drawer
      v-model:open="uiState.open"
      :width="uiState.drawerWidth"
      :drawer-style="{ minWidth: '480px', maxWidth: '90vw', position: 'relative', height: '100%', maxHeight: '100vh', display: 'flex', flexDirection: 'column' }"
      :body-style="{ padding: '12px 16px 16px', flex: '1 1 auto', height: '0', minHeight: '0', boxSizing: 'border-box', display: 'flex', flexDirection: 'column', overflow: 'hidden' }"
      :title="drawerTitleModeLabel + (selectedTableDisplayName ? ' · ' + selectedTableDisplayName : '')"
      placement="right"
      :mask="false"
    >
      <div
        class="drawer-resize-handle"
        title="拖拽调整宽度"
        @mousedown.stop.prevent="startDrawerResize"
      />
      <template #extra>
        <div class="drawer-meta-extra-bar">
          <a-tooltip title="刷新">
            <button
              class="drawer-meta-refresh-button"
              type="button"
              :disabled="uiState.loadingMeta || !uiData.selectedTable"
              @click.prevent="refreshTableMeta(uiState.tableDrawerMode)"
            >
              <ReloadOutlined :spin="uiState.loadingMeta" />
            </button>
          </a-tooltip>
        </div>
      </template>
      <div
        ref="tableMetaDrawerBodyRef"
        class="table-meta-drawer-body"
        tabindex="0"
        @contextmenu.prevent.stop="() => {}"
        @keydown="handleDrawerBodyKeydown"
        @mousedown.stop
      >
        <a-spin :spinning="uiState.loadingMeta" wrapperClassName="drawer-meta-spin" class="drawer-meta-spin-root">
          <section class="drawer-meta-section drawer-meta-section--summary">
            <div class="drawer-meta-section-title">
              <DatabaseOutlined />
              <span>概览</span>
            </div>
            <div v-if="uiData.tableSummary.length" class="drawer-meta-summary-grid">
              <div
                v-for="row in uiData.tableSummary"
                :key="row.key"
                class="drawer-meta-summary-item"
                :title="`${row.label}：${row.value || ''}`"
              >
                <span class="drawer-meta-summary-label" :title="row.label"><span class="drawer-meta-summary-label-text">{{ row.label }}</span><span class="drawer-meta-summary-label-colon">：</span></span>
                <span
                  v-if="['行数','数据大小','索引大小','空闲空间','自增主键值','AVG_ROW_LENGTH','DATA_LENGTH','INDEX_LENGTH','MAX_DATA_LENGTH','VERSION'].includes(row.label) || /^(CREATE|UPDATE|CHECK)_TIME$/i.test(String(row.label||'').replace(/\s+/g,''))"
                  class="drawer-meta-summary-value drawer-meta-mono"
                >{{ row.value || '-' }}</span>
                <span
                  v-else-if="['引擎','表类型','字符集','排序规则','行格式','Schema','所属库','表名','表注释'].includes(row.label)"
                  class="drawer-meta-summary-value"
                  :class="{ 'drawer-meta-comment': row.label === '表注释', 'drawer-meta-mono': row.label !== '表注释' && row.label !== '表名' }"
                >{{ row.value || '-' }}</span>
                <span v-else class="drawer-meta-summary-value">{{ row.value || '-' }}</span>
              </div>
            </div>
            <a-empty v-else :image="Empty.PRESENTED_IMAGE_SIMPLE" description="暂无概览信息" />
          </section>

          <section class="drawer-meta-section drawer-meta-section--ddl drawer-meta-section--flex">
            <div class="drawer-meta-section-title drawer-meta-section-title--with-action">
              <span class="drawer-meta-section-title-inner">
                <ScheduleOutlined />
                <span>DDL 脚本</span>
              </span>
              <a-tooltip title="复制 DDL 脚本">
                <button
                  class="drawer-meta-copy-button drawer-meta-copy-button--inline"
                  type="button"
                  :disabled="!String(uiData.tableDdlText || '').trim() || uiState.isCopyingTableInfo"
                  @click.prevent="copyDdlOnly"
                >
                  <CopyOutlined />
                </button>
              </a-tooltip>
            </div>
            <div class="drawer-meta-ddl-scroll-wrap">
              <highlightjs
                v-if="String(uiData.tableDdlText || '').trim()"
                language="sql"
                :code="uiData.tableDdlText"
                class="drawer-meta-ddl-block"
              />
              <a-empty v-else :image="Empty.PRESENTED_IMAGE_SIMPLE" description="暂无 DDL" />
            </div>
          </section>
        </a-spin>
      </div>
    </a-drawer>
  </div>
</template>

<script setup>
import {
  GetInstancesApi,
  GetPermittedTablesBySchemaApi,
  GetResourcesApi,
  GetSchemaBrowserChildrenApi,
  GetSchemaTablesApi,
  GetTableInfoApi,
  RefreshInstanceMetaApi,
} from '@/api/das'
import {
  FAMILY_CLICKHOUSE,
  FAMILY_MYSQL,
  FAMILY_ORACLE,
  FAMILY_POSTGRES,
  FAMILY_SQLSERVER,
  resolveFamilyInContext,
  resolveInstanceDbFamily,
} from '@/utils/dbFamily'
import { getPureSchemaDisplayName, isPostgresSchemaDbType } from '@/utils/schemaDisplay'
import {
  CloseOutlined,
  CopyOutlined,
  DatabaseOutlined,
  DownOutlined,
  EyeOutlined,
  FieldTimeOutlined,
  FolderOpenOutlined,
  FolderOutlined,
  FunctionOutlined,
  KeyOutlined,
  MenuOutlined,
  PartitionOutlined,
  ReloadOutlined,
  ScheduleOutlined,
  SearchOutlined,
  StarFilled,
  StarOutlined,
  TableOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons-vue'
import { Modal, message, Empty } from 'ant-design-vue'
import { computed, inject, onBeforeUnmount, onMounted, reactive, ref, watch, nextTick } from 'vue'
import { useDasObjectSearchStore } from '@/store/dasObjectSearch'
import PermissionHint from './components/PermissionHint.vue'

const dasInstanceData = inject('dasInstanceData')
const dasConsoleAction = inject('dasConsoleAction')
const dasFavoriteInstanceState = inject('dasFavoriteInstanceState', null)
const dasOS = useDasObjectSearchStore()

// ---------------------------------------------------------------------------
// 对象分组：库 / 模式下固定六分组，表下固定三分组（分组为空也渲染，便于一眼看全）
// ---------------------------------------------------------------------------

const DATABASE_GROUPS = ['tables', 'views', 'procedures', 'triggers', 'events', 'functions']
const TABLE_GROUPS = ['columns', 'indexes', 'table-triggers']

const GROUP_META = {
  tables: { label: 'Tables', icon: TableOutlined },
  views: { label: 'Views', icon: EyeOutlined },
  procedures: { label: 'Procedures', icon: FunctionOutlined },
  triggers: { label: 'Triggers', icon: ThunderboltOutlined },
  events: { label: 'Events', icon: FieldTimeOutlined },
  functions: { label: 'Functions', icon: FunctionOutlined },
  columns: { label: 'Columns', icon: MenuOutlined },
  indexes: { label: 'Indexes', icon: KeyOutlined },
  'table-triggers': { label: 'Triggers', icon: ThunderboltOutlined },
}

const groupLabel = (group) => GROUP_META[group]?.label || group
const groupIcon = (group) => GROUP_META[group]?.icon || FolderOpenOutlined

// 对象节点 → 对应的分组 key，用于复用分组图标
const OBJECT_TO_GROUP = {
  table: 'tables',
  view: 'views',
  procedure: 'procedures',
  function: 'functions',
  trigger: 'triggers',
  event: 'events',
  index: 'indexes',
}
const objectGroupOf = (node) => OBJECT_TO_GROUP[String(node?.kind || node?.objectKind || '')] || 'tables'

// 对象体积展示：12GB / 16KB；为 0（或拿不到有效数值）时只显示 “0” 且不带单位
const SIZE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
const formatObjectSize = (bytes) => {
  const value = Number(bytes)
  if (!Number.isFinite(value) || value <= 0) return '0'
  let size = value
  let unitIndex = 0
  while (size >= 1024 && unitIndex < SIZE_UNITS.length - 1) {
    size /= 1024
    unitIndex += 1
  }
  const rounded = size >= 10 || unitIndex === 0 ? Math.round(size) : Math.round(size * 10) / 10
  return `${rounded}${SIZE_UNITS[unitIndex]}`
}

// 库 / Schema 用接口下发的 sizeBytes，表用已有的 data_length_kb；
// 两者都没有（引擎暂不支持统计）时返回空串，行内不渲染大小
const nodeSizeText = (node) => {
  if (!node) return ''
  if (node.sizeBytes !== undefined && node.sizeBytes !== null && node.sizeBytes !== '') {
    return formatObjectSize(node.sizeBytes)
  }
  const kb = node.data_length_kb
  if (kb !== undefined && kb !== null && kb !== '') {
    const parsed = Number(kb)
    if (Number.isFinite(parsed)) return formatObjectSize(parsed * 1024)
  }
  return ''
}

// 列节点 tooltip：类型 / 主键 / 可空 / 注释，避免把一串属性塞进标题行
const columnNodeTitle = (node) => {
  const parts = []
  if (node?.column_type) parts.push(String(node.column_type))
  if (node?.column_primary) parts.push('主键')
  if (node?.column_unique) parts.push('唯一')
  if (node?.column_nullable === true) parts.push('可空')
  if (node?.column_default) parts.push(`默认 ${node.column_default}`)
  if (node?.column_comment) parts.push(String(node.column_comment))
  return parts.length ? `${node?.column_name || ''} · ${parts.join(' · ')}` : String(node?.column_name || '')
}

// 索引列：MySQL 直接用 attrs.columns，PG 从 indexdef 尾部括号中提取
const indexColumnsText = (attrs) => {
  const raw = String(attrs?.columns || '').trim()
  if (raw) return raw
  const def = String(attrs?.definition || '')
  const matched = def.match(/\(([^()]*)\)\s*(?:WHERE[\s\S]*)?$/i)
  return matched ? matched[1].replace(/\s+/g, ' ').trim() : ''
}

// 对象节点标题：把属性（唯一索引、触发器时机、字段类型等）拼到 tooltip 上
const objectNodeTitle = (node) => {
  const attrs = node?.attrs || {}
  const parts = []
  if (attrs.column_type) parts.push(String(attrs.column_type))
  if (String(attrs.is_primary || '') === '1' || attrs.is_primary === true) parts.push('主键')
  if (String(attrs.unique || '') === '1' || attrs.unique === true) parts.push('唯一')
  if (attrs.columns) parts.push(`(${String(attrs.columns)})`)
  if (attrs.timing || attrs.event) parts.push([attrs.timing, attrs.event].filter(Boolean).join(' '))
  if (attrs.comment) parts.push(String(attrs.comment))
  const base = String(node?.title || '')
  return parts.length ? `${base} · ${parts.join(' · ')}` : base
}

// 分组节点工厂：isLeaf=false + children=[] 才会触发 a-tree 的 load-data
// resource 用于 tables 分组复用既有授权通道；source 指向宿主节点（表/库节点）
const makeGroupNode = (opts) => {
  const {
    group, parent, instanceId, dbFamily, schemaPath = '', table = '',
    count = 0, db = '', resource = null, source = null,
  } = opts
  const key = `${parent.key}#group#${group}`
  return {
    key,
    kind: 'group',
    nodeKind: 'group',
    group,
    title: groupLabel(group),
    label: groupLabel(group),
    db,
    schemaPath,
    table,
    dbFamily,
    instanceId,
    resource,
    source,
    count,
    isLeaf: false,
    selectable: false,
    children: [],
    childrenLoaded: false,
    loading: false,
  }
}

const DEDUP_MESSAGE_WINDOW = 1200
const lastShownMessageAt = new Map()
const NOOP = () => {}
const showMessage = (type, text, duration) => {
  const now = Date.now()
  const key = `${type}::${text}`
  const last = lastShownMessageAt.get(key) || 0
  if (last && now - last < DEDUP_MESSAGE_WINDOW) {
    lastShownMessageAt.set(key, now)
    return NOOP
  }
  lastShownMessageAt.set(key, now)
  let fn = message[type]
  if (typeof fn !== 'function') fn = message.info
  let closer = NOOP
  try {
    if (typeof duration === 'number') {
      closer = fn(text, duration) || NOOP
    } else {
      closer = fn(text) || NOOP
    }
  } catch (e) {
    closer = message.info(text) || NOOP
  }
  if (typeof closer !== 'function') closer = NOOP
  return () => {
    try { closer() } catch (e) {}
  }
}
const uiState = reactive({
  open: false,
  isTreeLoading: false,
  instancesError: '',
  offlineInstanceNoticeShown: false,
  offlineMessage: '',
  isCurrentInstanceRefreshing: false,
  searchFocused: false,
  refreshLock: new Set(),
  searchHistory: [],
  serverSearch: {
    loading: false,
    abortController: null,
    keyword: '',
    total: 0,
    items: [],
  },
  locateInProgress: false,
  locateCancel: null,
  isCopyingTableInfo: false,
  drawerActiveTab: 'ddl',
  tableDrawerMode: 'structure',
  // 抽屉当前展示的对象类型：table（默认）/ view / procedure / function / trigger / event
  drawerObjectType: 'table',
  loadingMeta: false,
  _lastDrawerTableKey: '',
  drawerWidth: '50%',
})

const uiData = reactive({
  tableInfo: '',
  tableSummary: [],
  tableColumns: [],
  tableIndexes: [],
  tableDdlText: '',
  instances: [],
  selectedInstanceId: undefined,
  selectedDatabase: undefined,
  selectedSchema: undefined,
  selectedTable: undefined,
  instanceResourceTreeData: [],
  instanceResourceExpandedKeys: [],
  instanceResourceSelectedKeys: [],
  instanceResourceAutoExpandParent: true,
  resourceTreeKeyword: '',
  selectedDbType: '',
  selectedInstanceRemark: '',
  instanceData: {
    schema: '',
    instance_id: '',
    db_type: '',
    database: '',
    schema_name: '',
    resource_type: '',
    display_name: '',
    remark: '',
    tables: {},
  },
  preExpandSnapshotExpandedKeys: [],
  preExpandSnapshotSelectedKeys: [],
  preRefreshSnapshotExpandedKeys: [],
  preRefreshSnapshotSelectedKeys: [],
  preRefreshSnapshotKeyword: '',
})

const currentSearchMode = computed(() => dasOS.mode(uiData.selectedInstanceId))
const serverSearchHasPanel = computed(() =>
  currentSearchMode.value === 'server' && String(uiData.resourceTreeKeyword || '').trim().length >= 2,
)

const searchInputRef = ref(null)
const debounceTimerRef = ref(null)
const tableMetaDrawerBodyRef = ref(null)
const EXPANDED_KEYS_STORAGE_PREFIX = 'das#expandedKeys#'
const SEARCH_HISTORY_STORAGE_KEY = 'das#searchHistory'
const SEARCH_DEBOUNCE_MS = 120
const MAX_SEARCH_HISTORY = 10

const searchPlaceholder = computed(() => {
  if (!uiData.selectedInstanceId) return '请先选择实例'
  return '搜索'
})

/* 引擎家族（唯一判定入口，见 utils/dbFamily）：
   优先用实例下发的 db_family —— 后端已按 extra_config 把 OceanBase 解析成 mysql / oracle；
   OceanBase 的 db_type 只有 "OceanBase" 一个值，光看它判不出该走哪条通道。
   实例还没加载时按 db_type 本地兜底（对 OceanBase 只能落到 MySQL 模式，属已知降级）。 */
const selectedInstanceMeta = computed(() => getInstanceMeta(uiData.selectedInstanceId) || null)
const currentDbFamily = computed(() => {
  const fromInstance = resolveInstanceDbFamily(selectedInstanceMeta.value)
  if (fromInstance) return fromInstance
  return resolveFamilyInContext(uiData.selectedDbType || uiData.instanceData.db_type, null)
})
const isMysqlFamily = computed(() => currentDbFamily.value === FAMILY_MYSQL)
const isPostgresFamily = computed(() => currentDbFamily.value === FAMILY_POSTGRES)
const treeToolbarLogo = computed(() => {
  if (!uiData.selectedInstanceId) return ''
  if (isPostgresFamily.value) return pgLogo
  if (isMysqlFamily.value) return mysqlLogo
  return ''
})
// 「查看表信息」的能力开关：过去只放 MySQL / ClickHouse / PG，
// 因为后端 getTableStruc / getTableBase 没有 SQLServer 分支（点了只会返回空/报错）。
// 后端 tablemeta_sqlserver.go 已补齐 DDL 与概览两条链路，这里对齐放开。
const supportsTableStructureMenu = computed(
  () =>
    isMysqlFamily.value ||
    currentDbFamily.value === FAMILY_CLICKHOUSE ||
    isPostgresFamily.value ||
    currentDbFamily.value === FAMILY_SQLSERVER,
)
const supportsTableMetadataMenu = computed(() => isMysqlFamily.value)

const isRefreshingCurrentTree = computed(() => {
  if (!uiData.selectedInstanceId) return false
  return uiState.isCurrentInstanceRefreshing || uiState.isTreeLoading
})

const searchHitCountInfo = computed(() => {
  const keyword = String(uiData.resourceTreeKeyword || '').trim()
  if (keyword.length < 2) return { visible: false, text: '', level: 'default' }
  const mode = currentSearchMode.value
  if (mode === 'loading') return { visible: true, text: '加载中', level: 'default' }
  if (mode === 'server') {
    if (uiState.serverSearch.loading) return { visible: true, text: '搜索中', level: 'default' }
    const total = Number(uiState.serverSearch.total) || 0
    if (total === 0) return { visible: true, text: '无匹配', level: 'empty' }
    return { visible: false, text: '', level: 'default' }
  }
  const treeHits = countFilteredLeafNodes(uiData.instanceResourceTreeData)
  const clientHits = dasOS.searchClient(uiData.selectedInstanceId, keyword, { limit: 1 })
  const total = Math.max(treeHits, clientHits.total)
  if (total === 0) return { visible: true, text: '无匹配', level: 'empty' }
  return { visible: false, text: '', level: 'default' }
})

const displayInstanceResourceTreeData = computed(() => {
  // 实例根节点行已取消（实例名 / 收藏 / 刷新移至工作区 tab），
  // 渲染时把唯一实例根的子节点（库/Schema 层）上提为树顶层。
  // 数据结构（instanceRoot 及其展开键/懒加载/右键逻辑）保持不变。
  const promoteRootChildren = (nodes) => {
    if (!Array.isArray(nodes)) return nodes
    if (nodes.length === 1 && nodes[0]?.kind === 'instanceRoot') return nodes[0]?.children || []
    return nodes
  }
  const keyword = String(uiData.resourceTreeKeyword || '').trim()
  if (keyword.length < 2) return promoteRootChildren(uiData.instanceResourceTreeData)
  const mode = currentSearchMode.value
  let base = null
  let fallbackHits = []
  if (mode === 'server') {
    base = filterTreeNodesByKeyword(uiData.instanceResourceTreeData, keyword, null)
    fallbackHits = Array.isArray(uiState.serverSearch.items) ? uiState.serverSearch.items : []
  } else {
    const clientHits = dasOS.searchClient(uiData.selectedInstanceId, keyword, { limit: 200 })
    base = filterTreeNodesByKeyword(uiData.instanceResourceTreeData, keyword, clientHits.items)
    fallbackHits = clientHits.items || []
  }
  if (!base || !base.length || !hasDescendantNodes(base)) {
    if (fallbackHits.length) {
      const skeleton = buildVirtualSkeletonFromFlatHits(fallbackHits, keyword)
      nextTick(() => ensureAutoExpandForAllHitAncestors())
      return skeleton
    }
  }
  nextTick(() => ensureAutoExpandForAllHitAncestors())
  return promoteRootChildren(base)
})

const hasDescendantNodes = (nodes) => {
  if (!Array.isArray(nodes) || !nodes.length) return false
  for (const n of nodes) {
    if (!n) continue
    if (n?.kind === 'instanceRoot') return hasDescendantNodes(n.children)
    return true
  }
  return false
}

const buildVirtualSkeletonFromFlatHits = (flatHits, keyword) => {
  const out = []
  if (!uiData.instanceResourceTreeData?.length || !flatHits?.length) return out
  const root = uiData.instanceResourceTreeData[0]
  const rootChildrenByDbKey = new Map()
  const rootKey = String(root?.key || 'virtualRoot')
  for (const hit of flatHits) {
    const dbKey = String(hit.database_name || '').toLowerCase()
    const scKey = String(hit.schema_name || '').toLowerCase()
    if (!rootChildrenByDbKey.has(dbKey)) {
      const label = hit.database_name || ''
      rootChildrenByDbKey.set(dbKey, {
        key: [rootKey, 'flat-skel', 'database', label].filter(Boolean).join('#'),
        title: label,
        kind: 'database',
        nodeKind: 'database',
        _flatSkeleton: true,
        isLeaf: false,
        children: [],
        childrenLoaded: false,
        resource: root?.resource || null,
        instanceId: root?.instanceId || uiData.selectedInstanceId,
        dbType: root?.dbType || uiData.selectedDbType || '',
        _flatHitDbName: hit.database_name || '',
        schemaNodesByKey: new Map(),
      })
    }
    const dbNode = rootChildrenByDbKey.get(dbKey)
    if (hit.type === 'database') continue
    const needSchema = isPostgresFamily.value || currentDbFamily.value === FAMILY_SQLSERVER
    if (needSchema || hit.schema_name) {
      const scFullKey = `${dbKey}::${scKey}`
      if (!dbNode.schemaNodesByKey.has(scFullKey)) {
        const label = hit.schema_name || ''
        dbNode.schemaNodesByKey.set(scFullKey, {
          key: [rootKey, 'flat-skel', 'schema', label].filter(Boolean).join('#'),
          title: label,
          kind: 'schema',
          nodeKind: 'schema',
          _flatSkeleton: true,
          isLeaf: false,
          children: [],
          childrenLoaded: false,
          resource: root?.resource || null,
          instanceId: root?.instanceId || uiData.selectedInstanceId,
          dbType: root?.dbType || uiData.selectedDbType || '',
          _flatHitDbName: hit.database_name || '',
          _flatHitSchemaName: hit.schema_name || '',
        })
      }
      const schemaNode = dbNode.schemaNodesByKey.get(scFullKey)
      if (hit.type === 'table' && hit.table_name) {
        schemaNode.children.push({
          key: [rootKey, 'flat-skel', 'table', hit.database_name || '', hit.schema_name || '', hit.table_name].filter(Boolean).join('#'),
          title: hit.table_name,
          kind: 'table',
          nodeKind: 'table',
          isLeaf: true,
          _flatVirtual: true,
          _flatHitObj: hit,
          table_rows: hit.table_rows,
          data_length_kb: hit.data_length_kb,
          engine: hit.engine,
          table_comment: hit.table_comment,
          update_time: hit.update_time,
          resource: root?.resource || null,
          instanceId: root?.instanceId || uiData.selectedInstanceId,
          dbType: root?.dbType || uiData.selectedDbType || '',
        })
      }
    } else if (hit.type === 'table' && hit.table_name) {
      dbNode.children.push({
        key: [rootKey, 'flat-skel', 'table', hit.database_name || '', hit.table_name].filter(Boolean).join('#'),
        title: hit.table_name,
        kind: 'table',
        nodeKind: 'table',
        isLeaf: true,
        _flatVirtual: true,
        _flatHitObj: hit,
        table_rows: hit.table_rows,
        data_length_kb: hit.data_length_kb,
        engine: hit.engine,
        table_comment: hit.table_comment,
        update_time: hit.update_time,
        resource: root?.resource || null,
        instanceId: root?.instanceId || uiData.selectedInstanceId,
        dbType: root?.dbType || uiData.selectedDbType || '',
      })
    }
  }
  const dbChildren = []
  for (const [, db] of rootChildrenByDbKey) {
    const schemaChildren = []
    for (const [, sc] of db.schemaNodesByKey || []) {
      schemaChildren.push(sc)
    }
    if (schemaChildren.length) db.children = [...db.children, ...schemaChildren]
    dbChildren.push(db)
  }
  return [{
    ...root,
    children: dbChildren,
    expanded: true,
    _flatSkeleton: true,
  }]
}

const persistExpandedKeysForInstance = (instanceId) => {
  if (!instanceId || typeof window === 'undefined') return
  try {
    const key = EXPANDED_KEYS_STORAGE_PREFIX + String(instanceId)
    const arr = Array.isArray(uiData.instanceResourceExpandedKeys)
      ? uiData.instanceResourceExpandedKeys.slice(0, 500).map(String)
      : []
    localStorage.setItem(key, JSON.stringify(arr))
  } catch { /* ignore */ }
}

const restoreExpandedKeysForInstance = (instanceId) => {
  if (!instanceId || typeof window === 'undefined') return false
  try {
    const key = EXPANDED_KEYS_STORAGE_PREFIX + String(instanceId)
    const raw = localStorage.getItem(key)
    if (!raw) return false
    const arr = JSON.parse(raw)
    if (Array.isArray(arr) && arr.length) {
      uiData.instanceResourceExpandedKeys = arr.map(String)
      uiData.instanceResourceAutoExpandParent = true
      return true
    }
  } catch { /* ignore */ }
  return false
}

const loadSearchHistory = () => {
  if (typeof window === 'undefined') return []
  try {
    const raw = localStorage.getItem(SEARCH_HISTORY_STORAGE_KEY)
    if (!raw) return []
    const arr = JSON.parse(raw)
    return Array.isArray(arr) ? arr.filter(Boolean) : []
  } catch {
    return []
  }
}

const saveSearchHistory = (historyArr) => {
  if (typeof window === 'undefined') return
  try {
    localStorage.setItem(SEARCH_HISTORY_STORAGE_KEY, JSON.stringify((historyArr || []).slice(0, MAX_SEARCH_HISTORY)))
  } catch { /* ignore */ }
}

const appendSearchKeywordToHistory = (kw) => {
  const keyword = String(kw || '').trim()
  if (!keyword || keyword.length < 2) return
  const next = [keyword, ...(uiState.searchHistory || []).filter((x) => x !== keyword)].slice(0, MAX_SEARCH_HISTORY)
  uiState.searchHistory = next
  saveSearchHistory(next)
}

uiState.searchHistory = loadSearchHistory()

const emitInstanceData = () => {
  dasInstanceData.value = { ...uiData.instanceData }
}

const resetInstanceData = (shouldEmit = false) => {
  uiData.instanceData = {
    schema: '',
    instance_id: '',
    db_type: uiData.selectedDbType,
    database: '',
    schema_name: '',
    resource_type: '',
    display_name: '',
    remark: uiData.selectedInstanceRemark,
    tables: {},
  }
  if (shouldEmit) {
    emitInstanceData()
  }
}

const clearCurrentSelection = (shouldEmit = false) => {
  uiData.selectedDatabase = undefined
  uiData.selectedSchema = undefined
  uiData.selectedTable = undefined
  uiData.selectedDbType = ''
  uiData.selectedInstanceRemark = ''
  uiState.offlineMessage = ''
  resetInstanceResourceTree()
  resetInstanceData(shouldEmit)
}

// 实例列表加载失败的提示文案：区分网络异常与业务错误。
// 注意 get() 包装器（utils/request.js）会把 HTTP 错误归一化成响应体 {code, message}，
// 网络层错误则原样抛 AxiosError（无 data，code 为 ERR_NETWORK/ECONNABORTED 等）。
const resolveInstancesErrorMessage = (error) => {
  const errCode = String(error?.code || '').toUpperCase()
  const isNetwork =
    !error?.data &&
    (errCode === 'ERR_NETWORK' ||
      errCode === 'ECONNABORTED' ||
      errCode === 'ETIMEDOUT' ||
      /network error|timeout/i.test(String(error?.message || '')))
  if (isNetwork) {
    return '网络异常，请检查网络后重试'
  }
  const message = String(error?.message || error?.msg || error?.data?.message || '').trim()
  return message || '请稍后重试'
}

// 失败重试入口（模板「重试」链接）：清掉错误态重新拉取
const handleRetryFetchInstances = () => {
  void fetchInstances()
}

const fetchInstances = async () => {
  // 2026-11-10：不再 .catch(() => {}) 静默吞错。
  // 分三种情况：
  //   1. 成功（含空列表）→ 正常口径，空列表就是「确实无授权」；
  //   2. 401/422 → 会话过期，request.js 全局接管（刷新 token / 跳登录），这里不重复提示；
  //   3. 其余失败 → instancesError 置位，模板渲染红色提示 + 重试，且保留既有列表不清空。
  // 取消（路由切换/闲置唤醒会 abort 在途请求）不是错误，保持原状。
  uiState.instancesError = ''
  let res = null
  try {
    res = await GetInstancesApi()
  } catch (error) {
    const isCanceled = error?.canceled === true || error?.code === 'ERR_CANCELED'
    // 401 的归一化形态是 {code: 401, msg: ...}（数字 code），所以除了 .status 还要认 code
    const status = error?.status || Number(error?.code)
    if (!isCanceled && status !== 401 && status !== 422) {
      uiState.instancesError = resolveInstancesErrorMessage(error)
    }
  }
  if (res) {
    uiData.instances = res.data || []
  }
  if (uiState.instancesError) {
    // 失败时保留旧列表与选中态，等用户重试；不做 validIds 清理
    return
  }
  const available = uiData.instances || []
  const validIds = new Set((available || []).map((t) => String(t.instance_id)))
  if (uiData.selectedInstanceId && !validIds.has(String(uiData.selectedInstanceId))) {
    uiData.selectedInstanceId = undefined
    clearCurrentSelection()
  }
}

const normalizeSearchText = (value) => String(value || '').trim().toLowerCase()

const normalizeDbTypeValue = (value) => String(value || '').trim().toLowerCase()

const deriveResourceNames = (resource, dbType = uiData.selectedDbType || uiData.instanceData.db_type || '') => {
  // 统一按家族归一；resource 若带 db_family（后端下发）优先用它，否则回落到实例上下文
  // —— OceanBase 的租户模式只能从实例配置得知，单看 db_type 会误判成 MySQL 模式。
  const family = resolveFamilyInContext(resource?.db_family || dbType, selectedInstanceMeta.value)
  const resourcePath = String(resource?.resource_path || resource?.schema || '').trim()
  const parentPath = String(resource?.parent_path || '').trim()
  const isPgLike = family === FAMILY_POSTGRES || family === FAMILY_SQLSERVER

  if (isPgLike) {
    if (resource?.resource_type === 'database') {
      const rawDisplayName = String(resource?.display_name || '').trim()
      const databaseName = String(resource?.database_name || rawDisplayName || resourcePath).trim()
      return {
        resourcePath,
        databaseName,
        schemaName: '',
        displayName: rawDisplayName || databaseName || resourcePath,
      }
    }

    const databaseName = String(resource?.database_name || parentPath).trim()
    const rawDisplayName = String(resource?.display_name || '').trim()
    let schemaName = getPureSchemaDisplayName({
      dbType: family,
      schema: resourcePath,
      displayName: rawDisplayName,
      schemaName: resource?.schema_name,
      databaseName,
      parentPath,
    })
    if (!schemaName && databaseName && resourcePath.startsWith(`${databaseName}.`)) {
      schemaName = resourcePath.slice(databaseName.length + 1)
    }
    if (!schemaName) {
      const parts = resourcePath.split('.')
      schemaName = parts.slice(1).join('.') || resourcePath
    }
    return {
      resourcePath,
      databaseName,
      schemaName,
      displayName: schemaName || resourcePath,
    }
  }

  if (family === FAMILY_ORACLE) {
    const schemaName = String(resource?.schema_name || resource?.display_name || resourcePath).trim()
    return {
      resourcePath,
      databaseName: '',
      schemaName,
      displayName: String(resource?.display_name || schemaName || resourcePath).trim(),
    }
  }

  const databaseName = String(resource?.database_name || resource?.display_name || resourcePath).trim()
  return {
    resourcePath,
    databaseName,
    schemaName: '',
    displayName: String(resource?.display_name || databaseName || resourcePath).trim(),
  }
}

const formatDbType = (value) => {
  switch (normalizeDbTypeValue(value)) {
    case 'mysql':
      return 'MySQL'
    case 'postgresql':
      return 'PostgreSQL'
    case 'kingbase':
      return 'Kingbase'
    case 'sqlserver':
      return 'SQL Server'
    case 'oracle':
      return 'Oracle'
    case 'tidb':
      return 'TiDB'
    case 'doris':
      return 'Doris'
    case 'clickhouse':
      return 'ClickHouse'
    case 'polardb':
    case 'polardb-mysql':
    case 'polardbmysql':
      return 'PolarDB MySQL'
    case 'polardb-pg':
    case 'polardb-postgresql':
    case 'polardbpg':
      return 'PolarDB PostgreSQL'
    default:
      return value ? String(value) : '--'
  }
}

const formatInstanceLabel = (instance) => {
  const remark = String(instance?.remark || '').trim()
  const hostPort = `${instance?.hostname || ''}:${instance?.port || ''}`
  return remark ? `${remark} | ${hostPort}` : hostPort
}

const formatInstanceShortLabel = (instance) => {
  const remark = String(instance?.remark || '').trim()
  if (remark) return remark
  const hostPort = `${instance?.hostname || ''}:${instance?.port || ''}`.trim()
  return hostPort !== ':' ? hostPort : String(instance?.name || instance?.instance_name || '')
}

const resetInstanceResourceTree = () => {
  uiData.instanceResourceTreeData = []
  uiData.instanceResourceExpandedKeys = []
  uiData.instanceResourceSelectedKeys = []
  uiData.instanceResourceAutoExpandParent = true
}

const getResourceOptionLabel = (resource) => {
  return deriveResourceNames(resource).displayName || resource?.display_name || resource?.schema || ''
}

const filterTreeNodesByKeyword = (nodes, keyword, clientFlatHits = null) => {
  const needle = normalizeSearchText(keyword)
  if (!needle) return nodes

  const flatHitsByDb = new Map()
  const flatHitsBySchema = new Map()
  const flatHitsByFullKey = new Map()
  const descendantHitsByDb = new Map()
  const descendantHitsBySchema = new Map()
  if (Array.isArray(clientFlatHits) && clientFlatHits.length) {
    for (const hit of clientFlatHits) {
      const dbKey = String(hit.database_name || '').toLowerCase()
      const scKey = String(hit.schema_name || '').toLowerCase()
      const tblKey = String(hit.table_name || '').toLowerCase()
      if (hit.type === 'database' && hit.database_name) {
        flatHitsByDb.set(dbKey, hit)
      } else if (hit.type === 'schema') {
        const key = `${dbKey}::${scKey}`
        flatHitsBySchema.set(key, hit)
        if (dbKey) {
          if (!descendantHitsByDb.has(dbKey)) descendantHitsByDb.set(dbKey, [])
          descendantHitsByDb.get(dbKey).push(hit)
        }
      } else if (hit.type === 'table' && hit.table_name) {
        const fullKey = `${dbKey}::${scKey}::${tblKey}`
        flatHitsByFullKey.set(fullKey, hit)
        if (dbKey) {
          if (!descendantHitsByDb.has(dbKey)) descendantHitsByDb.set(dbKey, [])
          descendantHitsByDb.get(dbKey).push(hit)
        }
        if (dbKey || scKey) {
          const sKey = `${dbKey}::${scKey}`
          if (!descendantHitsBySchema.has(sKey)) descendantHitsBySchema.set(sKey, [])
          descendantHitsBySchema.get(sKey).push(hit)
        }
      }
    }
  }

  const makeVirtualNodeFromHit = (hit, parentNode) => {
    if (!hit) return null
    const kind = hit.type === 'database' ? 'database' : hit.type === 'schema' ? 'schema' : 'table'
    const title = kind === 'table' ? (hit.table_name || hit.display_name || '') :
      kind === 'schema' ? (hit.display_name || hit.schema_name || '') : (hit.display_name || hit.database_name || '')
    const key = [
      String(parentNode?.key || parentNode?.resource?.instance_id || 'virtual'),
      'flat',
      kind,
      hit.database_name || '',
      hit.schema_name || '',
      hit.table_name || '',
    ].filter(Boolean).join('#')
    return {
      key,
      title,
      kind,
      isLeaf: kind === 'table',
      nodeKind: kind,
      _flatVirtual: true,
      _flatHitObj: hit,
      resource: parentNode?.resource || null,
      instanceId: parentNode?.instanceId || uiData.selectedInstanceId,
      dbType: parentNode?.dbType || uiData.selectedDbType || '',
      children: [],
      childrenLoaded: false,
    }
  }

  const collectSearchableText = (node) => {
    const parts = []
    const res = node?.resource
    if (node?.title) parts.push(String(node.title).split('#')[0])
    if (res) {
      if (isDatabaseKind(node)) {
        if (res.database_name) parts.push(String(res.database_name))
        if (res.display_name) parts.push(String(res.display_name))
      } else if (isSchemaKind(node)) {
        if (res.schema_name) parts.push(String(res.schema_name))
        if (res.display_name) parts.push(String(res.display_name))
        else if (res.schema) parts.push(String(res.schema))
      } else if (isTableKind(node)) {
        if (node?.table_name) parts.push(String(node.table_name))
        else if (res.display_name) parts.push(String(res.display_name))
      } else if (node?.kind === 'instanceRoot') {
        parts.push(String(node.title || '').split('#')[0])
      }
    } else {
      if (node?.table_name) parts.push(String(node.table_name))
    }
    return parts
  }
  const isTableKind = (node) => {
    if (!node) return false
    if (node.nodeKind === 'table') return true
    if (node.kind === 'table') return true
    if (node.resource?.resource_type === 'table') return true
    if (node.table_name && !node.column_name && node.nodeKind !== 'column') return true
    return false
  }
  const isDatabaseKind = (node) => {
    if (!node) return false
    if (node.kind === 'database' || node.resource?.resource_type === 'database') return true
    return node.nodeKind === 'resource' && node.resource?.resource_type === 'database'
  }
  const isSchemaKind = (node) => {
    if (!node) return false
    if (node.kind === 'schema' || node.resource?.resource_type === 'schema') return true
    return node.nodeKind === 'resource' && node.resource?.resource_type === 'schema'
  }
  const isColumnKind = (node) => {
    if (!node) return false
    if (node.nodeKind === 'column' || node.column_name) return true
    return false
  }
  // 视图等对象节点不参与匹配（分组节点同理），但它们的子节点命中时仍会被保留
  const isAllowedMatchTarget = (node) =>
    node?.kind === 'instanceRoot' || isDatabaseKind(node) || isSchemaKind(node) || isTableKind(node)

  const selfTextContainsKeyword = (node, keywordLower) => {
    if (!keywordLower || !isAllowedMatchTarget(node)) return false
    if (node?.kind === 'instanceRoot') return false
    const texts = collectSearchableText(node)
    for (let i = 0; i < texts.length; i++) {
      const t = normalizeSearchText(texts[i])
      if (t && t.includes(keywordLower)) return true
    }
    return false
  }

  const nodeDatabaseName = (n) => {
    if (n?.resource?.database_name) return n.resource.database_name
    if (n?.resource?.schema && !n.resource?.schema_name) return n.resource.schema
    return ''
  }
  const nodeSchemaName = (n) => n?.resource?.schema_name || (n?.kind === 'schema' ? n.title || '' : '')
  const nodeTableName = (n) => n?.table_name || n?.title || ''

  const hitMatchesFromFlatIndex = (node) => {
    if (!clientFlatHits) return false
    if (isDatabaseKind(node)) {
      return flatHitsByDb.has(String(nodeDatabaseName(node)).toLowerCase())
    }
    if (isSchemaKind(node)) {
      const key = `${String(nodeDatabaseName(node)).toLowerCase()}::${String(nodeSchemaName(node)).toLowerCase()}`
      return flatHitsBySchema.has(key)
    }
    if (isTableKind(node)) {
      const findParentDb = (n) => String(n?.resource?.database_name || n?.resource?.parent_path || '').toLowerCase()
      const findParentSchema = (n) => String(n?.resource?.schema_name || (n?.resource?.schema ? getPureSchemaDisplayName(n.resource.schema, n.resource.db_type, n.resource.parent_path) : '')).toLowerCase()
      const db = findParentDb(node)
      const sc = findParentSchema(node)
      const tbl = String(nodeTableName(node)).toLowerCase()
      return flatHitsByFullKey.has(`${db}::${sc}::${tbl}`)
    }
    return false
  }

  const descendantFlatHitsForNode = (node) => {
    if (!clientFlatHits) return []
    if (isDatabaseKind(node)) {
      const key = String(nodeDatabaseName(node)).toLowerCase()
      return Array.isArray(descendantHitsByDb.get(key)) ? descendantHitsByDb.get(key) : []
    }
    if (isSchemaKind(node)) {
      const key = `${String(nodeDatabaseName(node)).toLowerCase()}::${String(nodeSchemaName(node)).toLowerCase()}`
      return Array.isArray(descendantHitsBySchema.get(key)) ? descendantHitsBySchema.get(key) : []
    }
    return []
  }

  const walk = (list = []) => {
    const result = []
    for (const node of list) {
      if (isColumnKind(node)) continue
      if (node?.nodeKind === 'emptyPlaceholder') continue
      if (node?._flatVirtual) {
        result.push({ ...node })
        continue
      }
      const children = walk(node.children || [])
      const allowed = isAllowedMatchTarget(node)
      const selfHit = allowed && selfTextContainsKeyword(node, needle)
      const flatHit = allowed && hitMatchesFromFlatIndex(node)
      const descendantHits = descendantFlatHitsForNode(node)
      const hasLoadedChildren = Array.isArray(node.children) && node.children.length > 0
      const childrenDeferred = Array.isArray(node.children) && node.children.length === 0 && node.childrenLoaded === false && node.isLeaf === false
      const childHit = children.length > 0
      const descendantFlatHit = !!(Array.isArray(descendantHits) && descendantHits.length > 0)
      if (selfHit || childHit || flatHit || descendantFlatHit) {
        let mergedChildren = children
        if ((selfHit || flatHit) && (childrenDeferred || !hasLoadedChildren)) {
          mergedChildren = (node.children || [])
        } else if (childHit) {
          mergedChildren = children
        } else if (descendantFlatHit && !childHit) {
          mergedChildren = []
          const alreadySeenVirtualKeys = new Set()
          for (const hit of descendantHits) {
            const vNode = makeVirtualNodeFromHit(hit, node)
            if (!vNode || alreadySeenVirtualKeys.has(String(vNode.key))) continue
            alreadySeenVirtualKeys.add(String(vNode.key))
            mergedChildren.push(vNode)
          }
        }
        result.push({
          ...node,
          ...(flatHit && !selfHit ? { _flatMatch: true } : {}),
          ...(descendantFlatHit && !childHit ? { _flatInjected: true } : {}),
          children: mergedChildren,
        })
      }
    }
    return result
  }
  return walk(nodes)
}

const countFilteredLeafNodes = (nodes) => {
  const keyword = String(uiData.resourceTreeKeyword || '').trim()
  let raw = []
  if (nodes?.length && keyword.length >= 2) {
    if (currentSearchMode.value === 'server') {
      raw = filterTreeNodesByKeyword(nodes, keyword, null)
    } else {
      const clientHits = dasOS.searchClient(uiData.selectedInstanceId, keyword, { limit: 200 })
      raw = filterTreeNodesByKeyword(nodes, keyword, clientHits.items)
    }
  }
  const walk = (list = []) => {
    let count = 0
    for (const node of list) {
      const kind = node?.kind
      const nodeKind = node?.nodeKind
      const isDatabase = kind === 'database' || node?.resource?.resource_type === 'database'
      const isSchema = kind === 'schema' || node?.resource?.resource_type === 'schema'
      const isTable = nodeKind === 'table' || kind === 'table' || node?.resource?.resource_type === 'table'
      const isVirtual = node?._flatVirtual && node?._flatHitObj
      if (isDatabase) count += 1
      else if (isSchema) count += 1
      else if (isTable) count += 1
      else if (isVirtual) count += 1
      if (node?._flatMatch) count += 1
      if (node?.children && node.children.length) count += walk(node.children)
    }
    return count
  }
  return walk(raw)
}

let serverSearchTimer = null
const triggerServerSearch = (rawKeyword, opts = {}) => {
  const keyword = String(rawKeyword || '').trim()
  if (uiState.serverSearch.abortController) {
    try { uiState.serverSearch.abortController.abort() } catch {}
    uiState.serverSearch.abortController = null
  }
  if (serverSearchTimer) {
    clearTimeout(serverSearchTimer)
    serverSearchTimer = null
  }
  const run = async () => {
    if (keyword.length < 2) {
      uiState.serverSearch.keyword = ''
      uiState.serverSearch.items = []
      uiState.serverSearch.total = 0
      uiState.serverSearch.loading = false
      return
    }
    uiState.serverSearch.loading = true
    uiState.serverSearch.keyword = keyword
    const ac = new AbortController()
    uiState.serverSearch.abortController = ac
    try {
      dasOS.reportAudit(uiData.selectedInstanceId, keyword)
      const res = await dasOS.searchServer(uiData.selectedInstanceId, keyword, { limit: 200, signal: ac.signal })
      if (uiState.serverSearch.keyword !== keyword) return
      uiState.serverSearch.total = Number(res.total) || 0
      uiState.serverSearch.items = Array.isArray(res.items) ? res.items : []
    } catch (err) {
      if (err?.name === 'AbortError' || err?.message?.toLowerCase?.()?.includes('abort')) return
      uiState.serverSearch.total = 0
      uiState.serverSearch.items = []
      showMessage('error', err?.message || '搜索服务异常，请稍后再试', 3)
    } finally {
      if (uiState.serverSearch.abortController === ac) uiState.serverSearch.abortController = null
      if (uiState.serverSearch.keyword === keyword) uiState.serverSearch.loading = false
    }
  }
  if (opts.immediate) {
    run()
  } else {
    serverSearchTimer = setTimeout(run, SEARCH_DEBOUNCE_MS)
  }
}

const handleSearch = (value) => {
  const keyword = value || ''
  uiData.resourceTreeKeyword = keyword
  uiData.instanceResourceAutoExpandParent = !!keyword
  appendSearchKeywordToHistory(keyword)
  const mode = currentSearchMode.value
  if (mode === 'client') {
    dasOS.reportAudit(uiData.selectedInstanceId, keyword)
  } else if (mode === 'server') {
    triggerServerSearch(keyword, { immediate: true })
  }
}

const handleDebouncedSearchInput = (value) => {
  const keyword = value || ''
  uiData.resourceTreeKeyword = keyword
  uiData.instanceResourceAutoExpandParent = !!keyword
  const mode = currentSearchMode.value
  if (mode === 'server') {
    triggerServerSearch(keyword)
  }
  if (debounceTimerRef.value) clearTimeout(debounceTimerRef.value)
  debounceTimerRef.value = setTimeout(() => {
    appendSearchKeywordToHistory(keyword)
    if (mode === 'client') dasOS.reportAudit(uiData.selectedInstanceId, keyword)
  }, SEARCH_DEBOUNCE_MS)
}

const handleClearSearch = () => {
  uiData.resourceTreeKeyword = ''
  uiData.instanceResourceAutoExpandParent = false
  try {
    uiState.serverSearch.abortController?.abort?.()
  } catch {}
  uiState.serverSearch.abortController = null
  uiState.serverSearch.loading = false
  uiState.serverSearch.items = []
  uiState.serverSearch.total = 0
  uiState.serverSearch.keyword = ''
  if (serverSearchTimer) {
    clearTimeout(serverSearchTimer)
    serverSearchTimer = null
  }
  try { searchInputRef.value?.blur?.() } catch {}
}

const handleFilterMenuClick = (key) => {
  if (key === 'clearSearchHistory') {
    uiState.searchHistory = []
    saveSearchHistory([])
    showMessage('success', '搜索历史已清空')
    return
  }
  if (key === 'expandHit') {
    const keyword = String(uiData.resourceTreeKeyword || '').trim()
    if (!keyword) {
      showMessage('info', '请先输入搜索关键词')
      return
    }
    uiData.instanceResourceAutoExpandParent = true
    ensureAutoExpandForAllHitAncestors({ force: true })
    showMessage('success', '已展开所有命中节点的父链路')
    return
  }
  if (key === 'searchGlobal') {
    const keyword = String(uiData.resourceTreeKeyword || '').trim()
    if (!keyword) {
      try { searchInputRef.value?.focus?.() } catch {}
      showMessage('info', '请先输入要搜索的对象名')
      return
    }
    appendSearchKeywordToHistory(keyword)
    triggerServerSearch(keyword, { immediate: true })
    showMessage('info', `搜索「${keyword}」${currentSearchMode.value === 'server' ? '（服务端模式）' : '（本地模式）'}`)
  }
}

const findNodeByResourcePath = (nodes, matchFn) => {
  for (const node of nodes || []) {
    if (matchFn(node)) return node
    if (node?.children?.length) {
      const found = findNodeByResourcePath(node.children, matchFn)
      if (found) return found
    }
  }
  return null
}

const resourceTreeLoadDataPromise = async (node) => {
  if (!node) return
  if (node.childrenLoaded === true) return
  const loader = loadInstanceResourceNode
  if (typeof loader !== 'function') return
  await loader({ dataRef: node })
}

const expandKeyInTree = async (key, waitMs = 80) => {
  const keyStr = String(key)
  if (!keyStr) return
  const set = new Set((uiData.instanceResourceExpandedKeys || []).map(String))
  set.add(keyStr)
  uiData.instanceResourceExpandedKeys = Array.from(set)
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, waitMs))
}

const selectKeyInTree = async (key, scrollInto = true) => {
  const keyStr = String(key)
  if (!keyStr) return
  uiData.instanceResourceSelectedKeys = [keyStr]
  uiData.instanceResourceAutoExpandParent = true
  await nextTick()
  if (!scrollInto) return
  await new Promise((resolve) => setTimeout(resolve, 60))
  try {
    const el = document.querySelector(`.instance-resource-tree [data-node-key="${keyStr}"]`)
    el?.scrollIntoView?.({ block: 'center', behavior: 'smooth' })
  } catch {}
}

const locateToObject = async (hit) => {
  if (!hit || uiState.locateInProgress) return
  if (!uiData.selectedInstanceId) return
  const dbName = String(hit.database_name || '').trim()
  const schemaName = String(hit.schema_name || '').trim()
  const tableName = String(hit.table_name || '').trim()
  const type = String(hit.type || 'table')
  const rootKey = uiData.instanceResourceTreeData?.[0]?.key ? String(uiData.instanceResourceTreeData[0].key) : ''
  if (!rootKey) return
  const token = { cancelled: false }
  uiState.locateInProgress = true
  uiState.locateCancel = () => { token.cancelled = true }
  const clearKeywordAfter = async () => {
    try {
      await new Promise((resolve) => setTimeout(resolve, 150))
      if (token.cancelled) return
      const kw = String(uiData.resourceTreeKeyword || '').trim()
      if (kw.length < 2) return
      uiData.resourceTreeKeyword = ''
      uiData.instanceResourceAutoExpandParent = true
      await nextTick()
    } catch {}
  }
  try {
    await expandKeyInTree(rootKey, 80)
    if (token.cancelled) return
    if (!dbName) {
      await selectKeyInTree(rootKey)
      return
    }
    let dbNode = findNodeByResourcePath(uiData.instanceResourceTreeData, (n) => {
      if (n?._flatSkeleton && n?._flatHitDbName && String(n._flatHitDbName).toLowerCase() === dbName.toLowerCase()) return true
      if (n?.kind === 'database' || n?.resource?.resource_type === 'database') {
        const db = n?.resource?.database_name || (n?.resource?.schema && !n?.resource?.schema_name ? n.resource.schema : '')
        return String(db).toLowerCase() === dbName.toLowerCase()
      }
      return false
    })
    if (!dbNode) {
      showMessage('info', '未找到对应数据库节点，请检查权限或刷新实例')
      return
    }
    await expandKeyInTree(dbNode.key, 60)
    await resourceTreeLoadDataPromise(dbNode)
    if (token.cancelled) return
    if (type === 'database' || (!schemaName && !tableName)) {
      await clearKeywordAfter()
      await selectKeyInTree(dbNode.key)
      return
    }
    let schemaNode = null
    const needSchemaLevel = isPostgresFamily.value || currentDbFamily.value === FAMILY_SQLSERVER
    if (needSchemaLevel) {
      schemaNode = findNodeByResourcePath(uiData.instanceResourceTreeData, (n) => {
        if (n?._flatSkeleton && (String(n?._flatHitDbName || '').toLowerCase() === dbName.toLowerCase()) && (String(n?._flatHitSchemaName || '').toLowerCase() === schemaName.toLowerCase())) return true
        if (n?.kind !== 'schema' && n?.resource?.resource_type !== 'schema') return false
        const sc = getPureSchemaDisplayName(n.resource?.schema, n.resource?.db_type, n.resource?.parent_path)
        const db = String(n.resource?.parent_path || n.resource?.database_name || '').toLowerCase()
        return db === dbName.toLowerCase() && String(sc).toLowerCase() === schemaName.toLowerCase()
      })
      if (!schemaNode && !tableName) {
        await clearKeywordAfter()
        await selectKeyInTree(dbNode.key)
        return
      }
      if (schemaNode) {
        await expandKeyInTree(schemaNode.key, 60)
        await resourceTreeLoadDataPromise(schemaNode)
        if (token.cancelled) return
      }
    }
    if (type === 'schema' || !tableName) {
      if (schemaNode) {
        await clearKeywordAfter()
        await selectKeyInTree(schemaNode.key)
      } else if (dbNode) {
        await clearKeywordAfter()
        await selectKeyInTree(dbNode.key)
      }
      return
    }
    // 表下沉到 tables 分组下：定位前先把该分组展开并加载，否则搜不到表节点
    const ownerContainer = schemaNode || dbNode
    const tablesGroupNode = (ownerContainer?.children || []).find((child) => child?.kind === 'group' && child.group === 'tables')
    if (tablesGroupNode) {
      await expandKeyInTree(tablesGroupNode.key, 60)
      await resourceTreeLoadDataPromise(tablesGroupNode)
      if (token.cancelled) return
    }
    const tableNode = findNodeByResourcePath(uiData.instanceResourceTreeData, (n) => {
      if (n?.nodeKind !== 'table' && !(n?._flatVirtual && n?._flatHitObj?.table_name)) return false
      if (n?._flatVirtual && n?._flatHitObj) {
        const hitDb = String(n._flatHitObj.database_name || '').toLowerCase()
        const hitSc = String(n._flatHitObj.schema_name || '').toLowerCase()
        const hitTbl = String(n._flatHitObj.table_name || '').toLowerCase()
        return (!dbName || hitDb === dbName.toLowerCase()) && (!schemaName || hitSc === schemaName.toLowerCase()) && hitTbl === tableName.toLowerCase()
      }
      const parent = n?.parentNode
      // 表挂在分组下时父节点是 group，真正的库/模式信息在分组的 source（或 resource）上
      const ownerNode = parent?.kind === 'group' ? parent.source || parent : parent
      let parentDb = ''
      let parentSchema = ''
      if (ownerNode?.kind === 'schema' || ownerNode?.resource?.resource_type === 'schema') {
        parentDb = String(ownerNode.resource?.parent_path || ownerNode.resource?.database_name || '').toLowerCase()
        parentSchema = String(getPureSchemaDisplayName(ownerNode.resource?.schema, ownerNode.resource?.db_type, ownerNode.resource?.parent_path) || '').toLowerCase()
      } else if (ownerNode?.kind === 'database' || ownerNode?.resource?.resource_type === 'database') {
        parentDb = String(ownerNode.resource?.database_name || ownerNode.resource?.schema || '').toLowerCase()
      }
      const dbMatch = !dbName || parentDb === dbName.toLowerCase()
      const scMatch = !schemaName || parentSchema === schemaName.toLowerCase()
      const tblMatch = String(n?.title || n?.table_name || '').toLowerCase() === tableName.toLowerCase()
      return dbMatch && scMatch && tblMatch
    })
    if (tableNode) {
      await clearKeywordAfter()
      await selectKeyInTree(tableNode.key)
    } else {
      const msg = schemaName
        ? `表「${dbName}.${schemaName}.${tableName}」`
        : `表「${dbName}.${tableName}」`
      showMessage('info', `${msg}未在当前已加载节点中命中，请先展开对应库/Schema 或刷新实例`, 4)
      if (schemaNode) {
        await clearKeywordAfter()
        await selectKeyInTree(schemaNode.key)
      } else if (dbNode) {
        await clearKeywordAfter()
        await selectKeyInTree(dbNode.key)
      }
    }
  } catch (err) {
    showMessage('error', err?.message || '定位对象失败')
  } finally {
    uiState.locateInProgress = false
    uiState.locateCancel = null
  }
}

const escapeHtml = (str) =>
  String(str || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')

const highlightKeyword = (text) => {
  const source = escapeHtml(text)
  const keyword = normalizeSearchText(uiData.resourceTreeKeyword)
  if (!keyword) return source
  const rawKeyword = String(uiData.resourceTreeKeyword || '').trim()
  if (!rawKeyword) return source
  const needle = escapeHtml(rawKeyword)
  if (!needle) return source
  const lowerSource = source.toLowerCase()
  const lowerNeedle = needle.toLowerCase()
  if (!lowerSource.includes(lowerNeedle)) return source
  const regex = new RegExp(needle.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'gi')
  return source.replace(regex, '<span class="tree-hit">$&</span>')
}

const acquireRefreshLock = (instanceId) => {
  if (!instanceId) return false
  const key = String(instanceId)
  if (uiState.refreshLock.has(key)) return false
  uiState.refreshLock.add(key)
  return true
}
const releaseRefreshLock = (instanceId) => {
  if (!instanceId) return
  uiState.refreshLock.delete(String(instanceId))
}

const restoreKeysAfterRefresh = (rootKeyStr) => {
  const snapshotExpanded = Array.isArray(uiData.preRefreshSnapshotExpandedKeys)
    ? uiData.preRefreshSnapshotExpandedKeys.map(String)
    : []
  const snapshotSelected = Array.isArray(uiData.preRefreshSnapshotSelectedKeys)
    ? uiData.preRefreshSnapshotSelectedKeys.map(String)
    : []
  const rootNode = (uiData.instanceResourceTreeData || []).find(
    (n) => String(n.instanceId || n.key) === rootKeyStr,
  )
  const rootKey = rootNode ? String(rootNode.key) : rootKeyStr
  const existedInNew = (keySet) => {
    const existed = new Set()
    const walk = (list) => {
      for (const n of list || []) {
        if (n?.key != null) existed.add(String(n.key))
        if (n?.children?.length) walk(n.children)
      }
    }
    walk(uiData.instanceResourceTreeData)
    return keySet.filter((k) => existed.has(k))
  }
  const expandedCandidates = existedInNew(snapshotExpanded)
  const nextExpanded = new Set([rootKey])
  expandedCandidates.forEach((k) => nextExpanded.add(k))
  uiData.instanceResourceExpandedKeys = Array.from(nextExpanded)
  const selectedCandidates = existedInNew(snapshotSelected)
  if (selectedCandidates.length) {
    uiData.instanceResourceSelectedKeys = selectedCandidates
  } else {
    uiData.instanceResourceSelectedKeys = rootKey ? [rootKey] : []
  }
  const kw = uiData.preRefreshSnapshotKeyword
  if (kw !== undefined && kw !== null) {
    uiData.resourceTreeKeyword = kw
    uiData.instanceResourceAutoExpandParent = !!kw
  }
}

// 浏览器式刷新：刷新后沿「刷新前已展开」的路径逐层重建子级数据。
// 根刷新会用全新节点替换 children（childrenLoaded=false），若只把展开键还原，
// 会出现「箭头仍是展开态、内容却塌陷」的割裂体验；这里按展开键把每层子级
// 重新取数重建，保证刷新前后视觉状态一致（展开的照旧展开且内容可见）。
const refreshExpandedDescendants = async (parentNodes, expandedSet, opts = {}) => {
  const nextParents = []
  for (const parentNode of parentNodes || []) {
    const children = Array.isArray(parentNode.children) ? parentNode.children : []
    for (const childNode of children) {
      if (!childNode || childNode.isLeaf) continue
      if (!expandedSet.has(String(childNode.key))) continue
      try {
        // userTriggered=false：库清单重采只在根刷新时做一次，深层节点只重建子级，
        // 避免把一次刷新放大成对目标库的连续采集
        await refreshResourceNodeChildren(childNode, { ...opts, userTriggered: false })
      } catch {
        // 单个节点重建失败不阻塞其余路径；该分支的展开键会在
        // restoreKeysAfterRefresh 中因新树中不存在而被过滤，视觉上表现为收起（可接受降级）
        continue
      }
      if (childNode.childrenLoaded || (Array.isArray(childNode.children) && childNode.children.length)) {
        nextParents.push(childNode)
      }
    }
  }
  if (nextParents.length) {
    await refreshExpandedDescendants(nextParents, expandedSet, opts)
  }
}

const handleToolbarRefreshClick = async (_event, force = false) => {
  const instanceId = uiData.selectedInstanceId
  if (!instanceId) return
  const rootNode = (uiData.instanceResourceTreeData || []).find(
    (n) => String(n.instanceId) === String(instanceId),
  )
  if (!rootNode) return
  if (!acquireRefreshLock(instanceId)) return
  uiState.isCurrentInstanceRefreshing = true
  const instanceMeta = getInstanceMeta(instanceId)
  if (instanceMeta && isInstanceConnectionUnavailable(instanceMeta)) {
    showOfflineInstanceNotice(instanceMeta)
  }
  uiData.preRefreshSnapshotExpandedKeys = (uiData.instanceResourceExpandedKeys || []).slice()
  uiData.preRefreshSnapshotSelectedKeys = (uiData.instanceResourceSelectedKeys || []).slice()
  uiData.preRefreshSnapshotKeyword = uiData.resourceTreeKeyword
  let confirmResult = true
  if (force) {
    try {
      confirmResult = await Modal.confirm({
        title: '强制重新同步元数据？',
        content: '此操作将清除本地缓存并从数据库重新加载完整对象列表，实例对象多时可能耗时较久。',
        okText: '确认同步',
        cancelText: '取消',
        okType: 'primary',
      })
    } catch {
      confirmResult = false
    }
  }
  if (!confirmResult) {
    releaseRefreshLock(instanceId)
    uiState.isCurrentInstanceRefreshing = false
    return
  }
  const closeLoading = showMessage('loading', force ? '强制同步中…' : '刷新中…', 0)
  const startedAt = Date.now()
  try {
    const refreshed = await refreshResourceNodeChildren(rootNode, { userTriggered: true })
    if (refreshed) {
      uiData.instanceResourceTreeData = [...uiData.instanceResourceTreeData]
      // 浏览器式刷新：先按刷新前展开键重建展开路径上的子级，再还原 keys，
      // 保证深层展开键在新树中已存在、不会被误过滤
      await refreshExpandedDescendants(
        Array.isArray(rootNode.children) ? rootNode.children : [],
        new Set((uiData.preRefreshSnapshotExpandedKeys || []).map(String)),
      )
      restoreKeysAfterRefresh(String(instanceId))
      const dur = Date.now() - startedAt
      showMessage('success', force ? `强制同步完成 · 耗时 ${dur}ms` : (dur > 2000 ? `刷新成功 · 耗时 ${dur}ms` : '刷新成功'))
    }
  } catch (err) {
    uiData.instanceResourceExpandedKeys = (uiData.preRefreshSnapshotExpandedKeys || []).slice()
    uiData.instanceResourceSelectedKeys = (uiData.preRefreshSnapshotSelectedKeys || []).slice()
    uiData.resourceTreeKeyword = uiData.preRefreshSnapshotKeyword
    if (!detectSessionExpiredFromError(err)) {
      showMessage('error', force ? '强制同步失败，已保留刷新前数据' : '刷新失败，已保留刷新前数据')
    }
  } finally {
    closeLoading()
    releaseRefreshLock(instanceId)
    uiState.isCurrentInstanceRefreshing = false
    persistExpandedKeysForInstance(instanceId)
    dasOS.invalidate(instanceId)
    dasOS.ensureIndex(instanceId, { force: true }).catch(() => {})
  }
}

const handleReloadTree = async () => {
  if (!uiData.selectedInstanceId) return
  const instanceMeta = getInstanceMeta(uiData.selectedInstanceId)
  if (!instanceMeta?.instance_id) return
  if (isInstanceConnectionUnavailable(instanceMeta)) {
    showOfflineInstanceNotice(instanceMeta)
  }
  const rootNode = (uiData.instanceResourceTreeData || [])[0]
  if (!rootNode) return
  if (!acquireRefreshLock(uiData.selectedInstanceId)) return
  uiState.isCurrentInstanceRefreshing = true
  uiData.preRefreshSnapshotExpandedKeys = (uiData.instanceResourceExpandedKeys || []).slice()
  uiData.preRefreshSnapshotSelectedKeys = (uiData.instanceResourceSelectedKeys || []).slice()
  uiData.preRefreshSnapshotKeyword = uiData.resourceTreeKeyword
  const closeLoading = showMessage('loading', '刷新中...', 0)
  try {
    const refreshed = await refreshResourceNodeChildren(rootNode, { userTriggered: true })
    if (refreshed) {
      uiData.instanceResourceTreeData = [...uiData.instanceResourceTreeData]
      await refreshExpandedDescendants(
        Array.isArray(rootNode.children) ? rootNode.children : [],
        new Set((uiData.preRefreshSnapshotExpandedKeys || []).map(String)),
      )
      restoreKeysAfterRefresh(String(uiData.selectedInstanceId))
      showMessage('success', '刷新成功')
    }
  } catch (err) {
    uiData.instanceResourceExpandedKeys = (uiData.preRefreshSnapshotExpandedKeys || []).slice()
    uiData.instanceResourceSelectedKeys = (uiData.preRefreshSnapshotSelectedKeys || []).slice()
    uiData.resourceTreeKeyword = uiData.preRefreshSnapshotKeyword
    if (!detectSessionExpiredFromError(err)) {
      showMessage('error', '刷新失败')
    }
  } finally {
    closeLoading()
    releaseRefreshLock(uiData.selectedInstanceId)
    uiState.isCurrentInstanceRefreshing = false
    persistExpandedKeysForInstance(uiData.selectedInstanceId)
  }
}

const handleReloadTreeForInstance = async (nodeRecord) => {
  const instanceId = nodeRecord?.instanceId || uiData.selectedInstanceId
  if (!instanceId) return
  const rootNode = (uiData.instanceResourceTreeData || []).find((n) => String(n.instanceId) === String(instanceId))
  if (!rootNode) return
  if (!uiData.selectedInstanceId || String(uiData.selectedInstanceId) !== String(instanceId)) {
    uiData.selectedInstanceId = instanceId
  }
  if (!acquireRefreshLock(instanceId)) return
  uiState.isCurrentInstanceRefreshing = true
  const instanceMeta = getInstanceMeta(instanceId)
  if (instanceMeta && isInstanceConnectionUnavailable(instanceMeta)) {
    showOfflineInstanceNotice(instanceMeta)
  }
  uiData.preRefreshSnapshotExpandedKeys = (uiData.instanceResourceExpandedKeys || []).slice()
  uiData.preRefreshSnapshotSelectedKeys = (uiData.instanceResourceSelectedKeys || []).slice()
  uiData.preRefreshSnapshotKeyword = uiData.resourceTreeKeyword
  const closeLoading = showMessage('loading', '刷新中...', 0)
  try {
    const refreshed = await refreshResourceNodeChildren(rootNode, { userTriggered: true })
    if (refreshed) {
      uiData.instanceResourceTreeData = [...uiData.instanceResourceTreeData]
      await refreshExpandedDescendants(
        Array.isArray(rootNode.children) ? rootNode.children : [],
        new Set((uiData.preRefreshSnapshotExpandedKeys || []).map(String)),
      )
      restoreKeysAfterRefresh(String(instanceId))
      showMessage('success', '刷新成功')
    }
  } catch (err) {
    uiData.instanceResourceExpandedKeys = (uiData.preRefreshSnapshotExpandedKeys || []).slice()
    uiData.instanceResourceSelectedKeys = (uiData.preRefreshSnapshotSelectedKeys || []).slice()
    uiData.resourceTreeKeyword = uiData.preRefreshSnapshotKeyword
    if (!detectSessionExpiredFromError(err)) {
      showMessage('error', '刷新失败')
    }
  } finally {
    closeLoading()
    releaseRefreshLock(instanceId)
    uiState.isCurrentInstanceRefreshing = false
    persistExpandedKeysForInstance(instanceId)
    dasOS.invalidate(instanceId)
    dasOS.ensureIndex(instanceId, { force: true }).catch(() => {})
  }
}

const isFavoriteInstance = (instanceId) => {
  if (!dasFavoriteInstanceState || !instanceId) return false
  return !!dasFavoriteInstanceState.has(instanceId)
}

const toggleFavoriteInstance = (instanceId) => {
  if (!dasFavoriteInstanceState || !instanceId) return
  const added = dasFavoriteInstanceState.toggle(instanceId)
  showMessage('success', added ? '已收藏' : '已取消收藏')
}

// 收藏/刷新入口已迁移到工作区 tab（ConsoleIndex 渲染），这里暴露给宿主组件调用
defineExpose({
  toggleFavoriteInstance,
  isFavoriteInstance,
  reloadInstanceTree: (instanceId) => handleReloadTreeForInstance({ instanceId }),
})

const collectKeysToCollapse = (level = 'root') => {
  const rootList = uiData.instanceResourceTreeData || []
  const keys = new Set()
  const includeKey = (k) => { if (k != null) keys.add(String(k)) }
  if (level === 'root') {
    for (const node of rootList || []) includeKey(node.key)
    return Array.from(keys)
  }
  // 层级加深后按语义收起（原先按硬编码 depth，分组插进来后会错位）
  const walkFor = (nodes, parentKind = null) => {
    for (const node of nodes || []) {
      const kind = node?.kind
      if (level === 'database') {
        if (kind === 'instanceRoot' || kind === 'database') includeKey(node.key)
      } else if (level === 'schema') {
        if (kind === 'instanceRoot' || kind === 'database' || kind === 'schema') includeKey(node.key)
        // 库/模式下的分组节点等价于「已展开到 Schema 层」
        else if (kind === 'group' && (parentKind === 'database' || parentKind === 'schema')) includeKey(node.key)
      } else {
        includeKey(node.key)
      }
      if (node?.children?.length) walkFor(node.children, kind)
    }
  }
  walkFor(rootList)
  return Array.from(keys)
}

const doCollapseByLevel = (level = 'root') => {
  if (!uiData.selectedInstanceId) return
  uiData.instanceResourceExpandedKeys = collectKeysToCollapse(level)
  uiData.instanceResourceAutoExpandParent = false
  showMessage('success', level === 'root' ? '已全部收起' : `已收起到 ${level === 'database' ? '数据库层' : 'Schema 层'}`)
  persistExpandedKeysForInstance(uiData.selectedInstanceId)
}

const handleCollapseAll = () => doCollapseByLevel('root')
const handleCollapseLevelClick = (key) => doCollapseByLevel(String(key || 'schema'))

const focusSearchInput = () => {
  nextTick(() => {
    try {
      const el = searchInputRef.value
      if (el) {
        el.focus?.()
        try { el.select?.() } catch { /* ignore */ }
      }
    } catch { /* ignore */ }
  })
}

const onGlobalKeydown = (e) => {
  if (!e || typeof window === 'undefined') return
  const tagName = (e.target?.tagName || '').toLowerCase()
  const isTypingArea = ['input', 'textarea', 'select'].includes(tagName)
    || e.target?.isContentEditable
  const ctrlK = (e.ctrlKey || e.metaKey) && (e.key === 'k' || e.key === 'K')
  const ctrlF = (e.ctrlKey || e.metaKey) && (e.key === 'f' || e.key === 'F')
  if (ctrlK || ctrlF) {
    e.preventDefault()
    focusSearchInput()
    return
  }
  if (e.key === 'F5' && !isTypingArea) {
    e.preventDefault()
    if (e.shiftKey) {
      handleToolbarRefreshClick(e, true)
    } else {
      handleToolbarRefreshClick(e, false)
    }
    return
  }
  if ((e.ctrlKey || e.metaKey) && e.shiftKey && (e.key === 'e' || e.key === 'E')) {
    e.preventDefault()
    handleCollapseAll()
  }
}

const copyText = async (text, successMessage = '已复制') => {
  if (!text) {
    message.warning('没有可复制的内容')
    return false
  }
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
    message.success(successMessage)
    return true
  } catch {
    message.error('复制失败')
    return false
  }
}

const getInstanceMeta = (instanceId) => uiData.instances.find((item) => item.instance_id === instanceId)

const normalizeInstanceCheckStatus = (value) => String(value || '').trim().toLowerCase()

const isInstanceConnectionUnavailable = (instanceMeta) => normalizeInstanceCheckStatus(instanceMeta?.last_check_status) === 'error'

const getInstanceUnavailableMessage = (instanceMeta) => {
  const detail = String(instanceMeta?.last_check_message || '').trim()
  return detail || '当前实例连接异常，无法实时加载库表信息'
}

const showOfflineInstanceNotice = (instanceMeta) => {
  uiState.offlineMessage = getInstanceUnavailableMessage(instanceMeta)
}

/* 资源树的「顶层资源类型」：Oracle 以 schema 为根，其余（MySQL 系 / PG / SQL Server）以 database 为根。
   入参是「引擎家族」而不是 db_type —— OceanBase Oracle 模式租户的树根也是 schema，
   按 db_type 判会落到默认分支而被误当成 database。 */
const getPrimaryResourceType = (family) => (family === FAMILY_ORACLE ? 'schema' : 'database')

// PostgreSQL 家族判定（含历史别名）。走 dbFamily 统一口径，避免各处再维护一份别名表；
// OceanBase 两种模式都不属于 PG，行为与改造前一致（因此这里用无上下文的判定即可）。
const isPostgresFamilyDbType = (dbType) => resolveFamilyInContext(dbType, null) === FAMILY_POSTGRES

const extractPureSchemaFromLongPath = (raw) => {
  const s = String(raw || '').trim()
  if (!s) return ''
  if (!s.includes('.')) return s
  const parts = s.split('.').filter((x) => x && x.trim())
  return parts.length ? parts[parts.length - 1] : s
}

const extractDatabaseFromLongPath = (raw) => {
  const s = String(raw || '').trim()
  if (!s) return ''
  if (!s.includes('.')) return ''
  const idx = s.indexOf('.')
  return s.slice(0, idx) || ''
}

const normalizePgSelectedPayloadForApi = (payload, options = {}) => {
  const base = payload && typeof payload === 'object' ? { ...payload } : {}
  const effectiveDbType = String(options.dbType || uiData.selectedDbType || uiData.instanceData.db_type || '').trim()
  if (!isPostgresFamilyDbType(effectiveDbType)) return base
  const explicitSchemaName = String(options.schema_name || base.schema_name || '').trim()
  const explicitDatabaseName = String(options.database_name || base.database_name || base.database || '').trim()
  const rawSchema = String(base.schema || base.table_schema || options.schema || '').trim()
  const pureFromRaw = rawSchema.includes('.') ? extractPureSchemaFromLongPath(rawSchema) : rawSchema
  const dbFromRaw = rawSchema.includes('.') ? extractDatabaseFromLongPath(rawSchema) : ''
  const instanceSchemaName = String(uiData.instanceData?.schema_name || '').trim()
  const instanceDbName = String(uiData.instanceData?.database || '').trim()
  let schemaName = explicitSchemaName
  let databaseName = explicitDatabaseName
  if (!schemaName && rawSchema && pureFromRaw) {
    schemaName = pureFromRaw
  }
  if (!schemaName) schemaName = instanceSchemaName || ''
  if (!databaseName && dbFromRaw) databaseName = dbFromRaw
  if (!databaseName) databaseName = instanceDbName || ''
  const qualifiedSchema = databaseName && schemaName ? `${databaseName}.${schemaName}` : ''
  if (qualifiedSchema) base.schema = qualifiedSchema
  if (schemaName) base.schema_name = schemaName
  if (databaseName) base.database = databaseName
  if (databaseName && !base.database_name) base.database_name = databaseName
  if (base.table_schema && qualifiedSchema) base.table_schema = qualifiedSchema
  return base
}

const detectSessionExpiredFromError = (err) => {
  if (!err) return false
  const status = err?.status || err?.response?.status
  if (status === 401 || status === 422) return true
  const code = String(err?.code || err?.data?.code || err?.response?.data?.code || '')
  if (code === '4001' || code === '4002' || code === '401') return true
  const msg = String(err?.message || err?.data?.message || err?.response?.data?.message || '')
  return /登录|过期|unauthorized|session|token.*(expired|invalid)|jwt/i.test(msg)
}

const isNetworkAbortLikeError = (err) => {
  if (!err) return false
  if (err?.canceled || err?.code === 'ERR_CANCELED') return true
  if (err?.name === 'AbortError') return true
  if (err?.__alreadyHandled) return true
  return false
}

const fetchResources = async (instanceId, resourceType, parentPath = '', opts = {}) => {
  let failed = false
  let res = null
  try {
    res = await GetResourcesApi({
      instance_id: instanceId,
      resource_type: resourceType,
      parent_path: parentPath,
    })
  } catch (err) {
    failed = true
    if (!opts.silentFailure && !isNetworkAbortLikeError(err)) {
      if (detectSessionExpiredFromError(err)) {
        try {
          const { notifySessionExpired } = await import('@/utils/request')
          notifySessionExpired?.()
        } catch (_) {}
      } else if (!opts.userTriggered) {
        // 后台预加载/初始化类请求只在 debug 控制台打印，避免弹一堆 toast
      } else {
        showMessage('error', `加载「${resourceType}」失败，请稍后重试或刷新实例`, 3)
      }
    }
  }
  return { list: res?.data || [], failed }
}

const parseColumnNames = (columns) =>
  String(columns || '')
    .split('@@')
    .map((item) => item.split('$$')[0])
    .filter(Boolean)

const buildTableNodes = (grants, tableList, resourcePath) => {
  const tmpTreeData = []
  const tables = {}

  tableList.forEach((row) => {
    // 2026-10-23：无权限的表直接不渲染（不再用红叉标记），编辑器补全字典同步收敛到已授权表
    if (!checkTableRule(grants, row.table_name)) return
    const tableNode = {
      ...generatorTableNode(grants, row),
      nodeKind: 'table',
      resource_path: resourcePath,
    }
    tmpTreeData.push(tableNode)
    tables[row.table_name] = parseColumnNames(row.columns)
  })

  return { nodes: tmpTreeData, tables }
}

const loadTableNodesForResource = async (resource, opts = {}) => {
  let schemaRaw = null
  let grantsRaw = null
  let schemaFailed = false
  let grantsFailed = false
  let sessionExpiredHit = false
  try {
    schemaRaw = await GetSchemaTablesApi({
      instance_id: resource.instance_id,
      schema: resource.schema,
    })
  } catch (err) {
    schemaFailed = true
    if (detectSessionExpiredFromError(err)) sessionExpiredHit = true
  }
  try {
    grantsRaw = await GetPermittedTablesBySchemaApi({
      instance_id: resource.instance_id,
      schema: resource.schema,
      resource_type: resource.resource_type,
    })
  } catch (err) {
    grantsFailed = true
    if (detectSessionExpiredFromError(err)) sessionExpiredHit = true
  }
  const requestFailed = schemaFailed || grantsFailed
  if (sessionExpiredHit) {
    try {
      const { notifySessionExpired } = await import('@/utils/request')
      notifySessionExpired?.()
    } catch (_) {}
  } else if (requestFailed && opts.userTriggered && !opts.silentFailure) {
    const resourceNames = deriveResourceNames(resource, resource?.db_type || uiData.selectedDbType)
    const label = resourceNames?.displayName || resource?.schema || resource?.display_name || '资源'
    showMessage('error', `加载「${label}」对象列表失败，请稍后重试或刷新实例`, 3)
  }
  if (!grantsRaw && !requestFailed) {
    return { nodes: [], tables: {}, failed: false }
  }
  if (!schemaRaw && !requestFailed) {
    return { nodes: [], tables: {}, failed: false }
  }
  if (requestFailed && (!grantsRaw || !schemaRaw)) {
    return { nodes: [], tables: {}, failed: true }
  }
  const resourceNames = deriveResourceNames(resource, resource?.db_type || uiData.selectedDbType)
  const tableList = (schemaRaw.data || []).map((item) => ({
    ...item,
    resource_path: resource.schema,
    database_name: item?.database_name || resourceNames.databaseName,
    schema_name: item?.schema_name || resourceNames.schemaName,
    display_name: item?.display_name || resourceNames.displayName,
  }))
  const result = buildTableNodes(grantsRaw.data, tableList, resource.schema)
  return { ...result, failed: false }
}

// 表下沉到分组下一层，这里必须递归查找，否则编辑器补全/上下文字典会丢
const buildTableMapFromNodes = (nodes = []) => {
  const acc = {}
  const walk = (list = []) => {
    for (const node of list || []) {
      if (!node) continue
      if (node.nodeKind === 'table') {
        const tableName = String(node?.key || '').split('#')[1]
        if (tableName) {
          acc[tableName] = parseColumnNames(node.columns)
        }
        continue
      }
      if (Array.isArray(node.children)) walk(node.children)
    }
  }
  walk(nodes)
  return acc
}

const getTreeNodeTitle = (node) => {
  if (node?._flatVirtual && node?._flatHitObj) {
    return String(node._flatHitObj.table_name || node._flatHitObj.display_name || node?.title || '')
  }
  if (node?._flatSkeleton) {
    return String(node?._flatHitSchemaName || node?._flatHitDbName || node?.title || '')
  }
  const rawTitle = String(node?.title || '')
  return node?.nodeKind === 'table' ? rawTitle.split('#')[0] : rawTitle
}

const isTreeNodeExpandedOrLeaf = (node, expandedKeys) => {
  if (!node) return false
  if (node.nodeKind === 'table' || node.kind === 'table' || node.nodeKind === 'column' || node.kind === 'column') return true
  if (node.isLeaf) return true
  return expandedKeys && Array.isArray(expandedKeys) && expandedKeys.includes(node.key)
}

const getNodeCopyText = (node) => {
  if (!node) return ''
  if (node.kind === 'instanceRoot') {
    return getTreeNodeTitle(node)
  }
  if (node.nodeKind === 'table') {
    return String(node.table_name || getTreeNodeTitle(node))
  }
  if (node.nodeKind === 'column') {
    return String(node.column_name || getTreeNodeTitle(node))
  }
  if (node.kind === 'database' || node.kind === 'schema' || node.kind === 'resource') {
    return String(deriveResourceNames(node.resource, node.dbType || uiData.selectedDbType).displayName || getTreeNodeTitle(node))
  }
  return getTreeNodeTitle(node)
}

const quoteIdentByDbType = (name, dbType = '') => {
  const safe = String(name || '').trim()
  if (!safe) return ''
  // 按家族决定标识符引号：MySQL 系（含 OceanBase MySQL 模式）用反引号，
  // SQL Server 用方括号，其余（PostgreSQL / Oracle，含 OceanBase Oracle 模式）用双引号。
  // 保留历史上对 "mysql*" 前缀（如 mysql8）的宽松匹配，保证既有行为不变。
  const normalized = normalizeDbTypeValue(dbType)
  const family = resolveFamilyInContext(dbType, selectedInstanceMeta.value)
  if (family === FAMILY_MYSQL || family === FAMILY_CLICKHOUSE || normalized.startsWith('mysql')) {
    return `\`${safe.replace(/`/g, '``')}\``
  }
  if (family === FAMILY_SQLSERVER) {
    return `[${safe.replace(/\]/g, ']]')}]`
  }
  return `"${safe.replace(/"/g, '""')}"`
}

const buildPreviewTableSql = (dbType, tableName, qualifier = '') => {
  const family = resolveFamilyInContext(dbType, selectedInstanceMeta.value)
  const targetTable = String(tableName || '').trim()
  if (!targetTable) return ''

  const quoteIdent = (name) => quoteIdentByDbType(name, family)

  // 限定名必须「逐段」引用：SQLServer / PostgreSQL 的 qualifier 是「库.模式」两段。
  // 整段套一层方括号会得到 [msdb.dbo]，SQLServer 把它当成一个 schema 名去解析，
  // 于是报 Invalid object name 'msdb.dbo.autoadmin_master_switch'
  //（真实对象是 msdb 库下的 dbo schema）。单段限定名拆分后不变，对其余引擎无影响。
  const quoteQualifier = (path) =>
    String(path || '')
      .split('.')
      .map((part) => part.trim())
      .filter(Boolean)
      .map(quoteIdent)
      .join('.')

  const qualifierValue = String(qualifier || '').trim()
  const qualifierPart = quoteQualifier(qualifierValue)
  const qualifiedTable = qualifierPart
    ? `${qualifierPart}.${quoteIdent(targetTable)}`
    : quoteIdent(targetTable)

  if (family === FAMILY_SQLSERVER) {
    return `select top 100 * from ${qualifiedTable};`
  }

  if (family === FAMILY_ORACLE) {
    return `select * from ${qualifiedTable} where rownum <= 100;`
  }

  return `select * from ${qualifiedTable} limit 100;`
}

const buildPreviewContextPayload = (node) => {
  const dbType = normalizeDbTypeValue(uiData.selectedDbType || uiData.instanceData.db_type || '')
  // 分支按家族判（OceanBase 按租户模式落到 mysql / oracle）；payload 里的 db_type 仍用原值，仅供展示
  const family = currentDbFamily.value
  const resourcePath = String(node?.resource_path || node?.table_schema || node?.schemaPath || '').trim()
  const databaseName = String(node?.database_name || node?.db || uiData.instanceData.database || '').trim()
  const schemaName = String(node?.schema_name || uiData.instanceData.schema_name || '').trim()
  const extractPureSchemaFromResource = (rp, fallback = '') => {
    const s = String(rp || fallback || '').trim()
    if (!s) return ''
    if (!s.includes('.')) return s
    const parts = s.split('.').filter((x) => x && x.trim())
    return parts.length ? parts[parts.length - 1] : s
  }
  const displayName = family === FAMILY_POSTGRES
    ? getPureSchemaDisplayName({
      dbType: family,
      schema: resourcePath,
      displayName: node?.display_name || uiData.instanceData.display_name,
      schemaName,
      databaseName,
    })
    : String(node?.display_name || uiData.instanceData.display_name || schemaName || resourcePath).trim()
  const rawTableName = node?.table_name || node?.name || getTreeNodeTitle(node) || ''

  if (family === FAMILY_POSTGRES) {
    const qualifier = schemaName || extractPureSchemaFromResource(resourcePath, databaseName)
    return {
      instance_id: uiData.instanceData.instance_id || uiData.selectedInstanceId || '',
      db_type: dbType,
      remark: uiData.instanceData.remark || uiData.selectedInstanceRemark || '',
      table: String(rawTableName),
      sqlText: buildPreviewTableSql(dbType, rawTableName, qualifier),
      resource_type: 'schema',
      database: databaseName,
      schema: resourcePath,
      schema_name: qualifier,
      display_name: displayName,
    }
  }

  if (family === FAMILY_SQLSERVER) {
    const qualifier = resourcePath || schemaName
    return {
      instance_id: uiData.instanceData.instance_id || uiData.selectedInstanceId || '',
      db_type: dbType,
      remark: uiData.instanceData.remark || uiData.selectedInstanceRemark || '',
      table: String(rawTableName),
      sqlText: buildPreviewTableSql(family, rawTableName, qualifier),
      resource_type: 'schema',
      database: databaseName,
      schema: resourcePath,
      schema_name: schemaName,
      display_name: displayName,
    }
  }

  if (family === FAMILY_ORACLE) {
    const qualifier = schemaName || resourcePath
    return {
      instance_id: uiData.instanceData.instance_id || uiData.selectedInstanceId || '',
      db_type: dbType,
      remark: uiData.instanceData.remark || uiData.selectedInstanceRemark || '',
      table: String(rawTableName),
      sqlText: buildPreviewTableSql(family, rawTableName, qualifier),
      resource_type: 'schema',
      schema: resourcePath,
      schema_name: qualifier,
      display_name: displayName,
      database: '',
    }
  }

  const qualifier = databaseName || resourcePath
  return {
    instance_id: uiData.instanceData.instance_id || uiData.selectedInstanceId || '',
    db_type: dbType,
    remark: uiData.instanceData.remark || uiData.selectedInstanceRemark || '',
    table: String(rawTableName),
    sqlText: buildPreviewTableSql(family, rawTableName, qualifier),
    resource_type: 'database',
    database: qualifier,
    schema: resourcePath,
    display_name: displayName || qualifier,
    schema_name: '',
  }
}

const refreshInstanceRootNode = async (node, opts = {}) => {
  const instanceMeta = getInstanceMeta(node.instanceId)
  if (!instanceMeta?.instance_id) return
  // 用户主动刷新时必须先让后端重采一次库清单：
  // 资源树读的是本地快照表，只重读快照的话，刚建的库在 TTL 到期前怎么刷都看不到。
  // 非用户触发（展开节点、后台补全）不重采，避免把页面浏览变成对目标库的持续采集。
  if (opts.userTriggered) {
    await RefreshInstanceMetaApi(instanceMeta.instance_id).catch((err) => {
      showMessage('error', String(err?.message || err?.data?.message || '刷新库列表失败，请检查数据源连接状态').trim())
    })
  }
  const family = resolveInstanceDbFamily(instanceMeta) || resolveFamilyInContext(instanceMeta.db_type, instanceMeta)
  const primaryType = getPrimaryResourceType(family)
  let resourcesRes = await fetchResources(instanceMeta.instance_id, primaryType, '', opts)
  if (!resourcesRes.list.length && family === FAMILY_ORACLE && primaryType === 'schema') {
    resourcesRes = await fetchResources(instanceMeta.instance_id, 'database', '', opts)
  }
  const list = resourcesRes.list.map((item) => makeInstanceResourceNode(item, family))
  node.children = resourcesRes.failed ? list : injectEmptyPlaceholderIfNeeded(list, node.key, 'instanceRoot')
  if (!resourcesRes.failed) {
    node.childrenLoaded = true
  }
}

const refreshResourceNodeChildren = async (node, opts = {}) => {
  if (node.kind === 'instanceRoot') {
    await refreshInstanceRootNode(node, opts)
    return true
  }

  if (node.kind === 'database' && (isPostgresFamilyDbType(node.dbType) || node.dbType === 'sqlserver')) {
    const resource = node.resource
    const schemasRes = await fetchResources(resource.instance_id, 'schema', resource.schema, opts)
    const list = schemasRes.list.map((item) => makeInstanceResourceNode(item, node.dbType))
    node.children = schemasRes.failed ? list : injectEmptyPlaceholderIfNeeded(list, node.key, 'databaseHasSchema')
    if (!schemasRes.failed) {
      node.childrenLoaded = true
    }
    return true
  }

  if (node.kind === 'database' || node.kind === 'schema') {
    await buildDatabaseGroupNodes(node)
    return true
  }

  if (node.kind === 'table' || node.kind === 'view' || node.nodeKind === 'view') {
    buildTableGroupNodes(node)
    return true
  }

  if (node.kind === 'group') {
    await loadGroupChildren(node, { force: true })
    return true
  }

  if (node.nodeKind === 'column') {
    message.info('当前节点没有可刷新的子级资源')
    return false
  }

  return false
}

const selectTableNode = (node) => {
  if (node?.nodeKind !== 'table') return false
  const dbType = normalizeDbTypeValue(uiData.selectedDbType || uiData.instanceData.db_type || node?.dbType || '')
  const raw = {
    schema: node.resource_path || uiData.instanceData.schema,
    table: node.table_name || getTreeNodeTitle(node),
  }
  uiData.selectedTable = normalizePgSelectedPayloadForApi(raw, {
    dbType,
    schema_name: node.schema_name,
    database_name: node.database_name,
  })
  return true
}

// ---------------------------------------------------------------------------
// 对象树右键「查看X信息」：视图/存储过程/函数/触发器/事件，复用表信息抽屉
// ---------------------------------------------------------------------------

const OBJECT_INFO_META = {
  view: { label: '视图', objectType: 'view' },
  procedure: { label: '存储过程', objectType: 'procedure' },
  function: { label: '函数', objectType: 'function' },
  trigger: { label: '触发器', objectType: 'trigger' },
  event: { label: '事件', objectType: 'event' },
}

// 各家族对对象类型的支持面：后端没实现的组合不出菜单
const OBJECT_INFO_FAMILY_SUPPORT = {
  view: [FAMILY_MYSQL, FAMILY_POSTGRES, FAMILY_ORACLE, FAMILY_CLICKHOUSE, FAMILY_SQLSERVER],
  procedure: [FAMILY_MYSQL, FAMILY_POSTGRES, FAMILY_ORACLE, FAMILY_SQLSERVER],
  function: [FAMILY_MYSQL, FAMILY_POSTGRES, FAMILY_ORACLE, FAMILY_SQLSERVER],
  trigger: [FAMILY_MYSQL, FAMILY_POSTGRES, FAMILY_ORACLE, FAMILY_SQLSERVER],
  event: [FAMILY_MYSQL],
}

// 视图节点判定：分组树对象节点（kind/objectKind = 'view'）与历史扁平结构（nodeKind = 'view'）都算。
// 右键「查看数据」对视图与表保持同一交互，行为按数据库家族生成 SELECT，与具体 db_type 无关。
const isViewTreeNode = (node) =>
  node?.kind === 'view' || node?.nodeKind === 'view' || node?.objectKind === 'view'

const objectInfoMetaOf = (node) => {
  if (!node || node.nodeKind !== 'object') return null
  const kind = String(node.kind || '')
  const meta = OBJECT_INFO_META[kind]
  if (!meta) return null
  const family = resolveFamilyInContext(
    String(node.dbFamily || node.dbType || uiData.selectedDbType || ''),
    selectedInstanceMeta.value,
  )
  return (OBJECT_INFO_FAMILY_SUPPORT[kind] || []).includes(family) ? meta : null
}

const selectObjectNode = (node) => {
  if (node?.nodeKind !== 'object') return false
  const dbType = normalizeDbTypeValue(uiData.selectedDbType || uiData.instanceData.db_type || node?.dbType || '')
  const raw = {
    schema: node.schemaPath || node.resource_path || uiData.instanceData.schema,
    table: node.name || getTreeNodeTitle(node),
  }
  uiData.selectedTable = normalizePgSelectedPayloadForApi(raw, {
    dbType,
    schema_name: node.schema_name,
    database_name: node.database_name,
  })
  return Boolean(uiData.selectedTable?.table)
}

const showObjectInfo = (node) => {
  const meta = objectInfoMetaOf(node)
  if (!meta) return false
  if (!selectObjectNode(node)) {
    message.warning('当前节点缺少可查询的上下文信息')
    return true
  }
  uiState.drawerObjectType = meta.objectType
  uiState.tableDrawerMode = 'base'
  uiState.drawerActiveTab = 'summary'
  resetTableDrawerData()
  uiState.open = true
  refreshTableMeta(uiState.tableDrawerMode)
  return true
}

const syncSelectedResourceTables = (resource, tables = {}) => {
  if (!resource) {
    return
  }
  if (
    uiData.instanceData.instance_id === resource.instance_id &&
    uiData.instanceData.schema === resource.schema &&
    uiData.instanceData.resource_type === resource.resource_type
  ) {
    uiData.instanceData.tables = { ...tables }
  }
}

const applyResourceSelection = (resource, dbType, tables = {}) => {
  if (!resource) {
    resetInstanceData()
    return
  }
  const instanceMeta = getInstanceMeta(resource.instance_id || uiData.selectedInstanceId) || {}
  const resourceNames = deriveResourceNames(resource, dbType)
  // 按家族决定「哪个字段承载库、哪个承载 Schema」：OceanBase Oracle 模式只有 Schema 概念
  const family = resolveFamilyInContext(dbType || instanceMeta?.db_type, instanceMeta)
  let database = ''
  let schemaName = ''
  if (family === FAMILY_POSTGRES || family === FAMILY_SQLSERVER) {
    database = resourceNames.databaseName || uiData.selectedDatabase || ''
    schemaName = resourceNames.schemaName || ''
  } else if (family === FAMILY_ORACLE) {
    schemaName = resourceNames.schemaName || resource.schema || ''
  } else {
    database = resourceNames.databaseName || resource.schema || ''
  }

  uiData.instanceData = {
    instance_id: resource.instance_id || uiData.selectedInstanceId,
    schema: resourceNames.resourcePath || resource.schema,
    db_type: uiData.selectedDbType || dbType,
    database,
    schema_name: schemaName,
    resource_type: resource.resource_type,
    display_name: resourceNames.displayName || resource.display_name || resource.schema,
    remark: uiData.selectedInstanceRemark || instanceMeta.remark || '',
    tables: { ...tables },
  }
  emitInstanceData()
}

const makeInstanceResourceNodeKey = (resource) =>
  `resource:${resource.instance_id}:${resource.resource_type}:${encodeURIComponent(String(resource.schema || ''))}`

// 选中库/模式节点时同步展开：点标题与点箭头行为保持一致，
// 避免「点了库名但树没展开」被当成 bug（实测点标题只 select 不 expand）
const expandInstanceResourceNode = (node) => {
  const key = String(node?.key || '')
  if (!key) return
  const merged = new Set((uiData.instanceResourceExpandedKeys || []).map(String))
  merged.add(key)
  uiData.instanceResourceExpandedKeys = Array.from(merged)
  uiData.instanceResourceAutoExpandParent = false
}

// ---------------------------------------------------------------------------
// 箭头点击接管（capture 阶段拦下，不让 vc-tree 处理）
//
// 根因：vc-tree TreeNode 的箭头 click 处理是 `if (loading.value) return` ——
// loadData 进行期间（几十 ms 到数秒）所有箭头点击被静默丢弃；加载完成后下一次
// 点击是 toggle，把用户「想打开」的点击变成了收起。用户体感：
// 点开 → 没反应 → 再点 → 还是收着 =「打开一个库不展开第二级」。
//
// 展开键本来就是受控属性（expanded-keys 由本组件持有），接管后：
//   * 每次点击都确定性地 toggle，加载中也不丢；
//   * 展开未加载的节点时，vc-tree 的 syncLoadData 会在渲染后自动触发 loadData。
// ---------------------------------------------------------------------------
const findInstanceResourceNodeByKey = (key, list = uiData.instanceResourceTreeData) => {
  for (const node of list || []) {
    if (!node) continue
    if (String(node.key) === String(key)) return node
    const hit = findInstanceResourceNodeByKey(key, node.children)
    if (hit) return hit
  }
  return null
}

const toggleInstanceResourceNode = (node) => {
  const key = String(node?.key || '')
  if (!key) return
  const set = new Set((uiData.instanceResourceExpandedKeys || []).map(String))
  if (set.has(key)) {
    set.delete(key)
  } else {
    set.add(key)
  }
  uiData.instanceResourceExpandedKeys = Array.from(set)
  uiData.instanceResourceAutoExpandParent = false
}

const handleTreeSwitcherClickCapture = (event) => {
  const switcher = event.target?.closest?.('.ant-tree-switcher')
  if (!switcher || switcher.classList.contains('ant-tree-switcher-noop')) return
  const nodeEl = switcher.closest('.ant-tree-treenode')
  if (!nodeEl) return
  const keyEl = nodeEl.querySelector('[data-node-key]')
  if (!keyEl) return
  const node = findInstanceResourceNodeByKey(keyEl.getAttribute('data-node-key'))
  if (!node) return
  event.preventDefault()
  event.stopPropagation()
  toggleInstanceResourceNode(node)
}

const EMPTY_HINT_TEXT = {
  instanceRoot: '该实例下暂无数据库',
  databaseHasSchema: '该数据库下暂无 Schema',
  schemaHasTables: '该 Schema 下暂无表',
  databaseHasTables: '该库下暂无表',
  tableHasColumns: '该表暂无列定义',
  groupHasTables: '该库下暂无表',
  groupHasViews: '该库下暂无视图',
  groupHasProcedures: '该库下暂无存储过程',
  groupHasTriggers: '该库下暂无触发器',
  groupHasEvents: '该库下暂无事件',
  groupHasFunctions: '该库下暂无函数',
  groupHasColumns: '该表暂无列定义',
  groupHasIndexes: '该表暂无索引',
  groupHasTableTriggers: '该表暂无触发器',
}

const buildEmptyPlaceholderNode = (parentKey, hintType) => {
  const text = EMPTY_HINT_TEXT[hintType] || '暂无数据'
  return {
    key: `${parentKey || 'root'}#empty-placeholder#${hintType}`,
    title: text,
    emptyPlaceholderType: hintType,
    emptyPlaceholderText: text,
    nodeKind: 'emptyPlaceholder',
    kind: 'emptyPlaceholder',
    isLeaf: true,
    selectable: false,
    disableCheckbox: true,
    children: undefined,
    childrenLoaded: true,
  }
}

const injectEmptyPlaceholderIfNeeded = (children, parentKey, hintType) => {
  if (!Array.isArray(children)) return children
  const hasRealChild = children.some((c) => c && c.nodeKind !== 'emptyPlaceholder')
  if (!hasRealChild) {
    return [buildEmptyPlaceholderNode(parentKey, hintType)]
  }
  return children.filter((c) => c && c.nodeKind !== 'emptyPlaceholder')
}

const makeInstanceResourceNode = (resource, dbType) => {
  // 节点上存「家族」而不是 db_type：下游（是否需要 Schema 层、引号风格、预览 SQL）
  // 全靠它判定，存 "oceanbase" 会丢掉租户模式。
  const family = resolveFamilyInContext(resource?.db_family || dbType, selectedInstanceMeta.value)
  const isDatabase = resource.resource_type === 'database'
  const needsSchema =
    isDatabase && (family === FAMILY_POSTGRES || family === FAMILY_SQLSERVER)
  const canLoadTables = (!needsSchema && isDatabase) || resource.resource_type === 'schema'
  const meta = ''
  return {
    title: getResourceOptionLabel(resource),
    key: makeInstanceResourceNodeKey(resource),
    isLeaf: !(needsSchema || canLoadTables),
    kind: resource.resource_type,
    dbType: family,
    resource,
    meta,
    nodeKind: 'resource',
    sizeBytes: resource?.size_bytes ?? null,
    children: needsSchema || canLoadTables ? [] : undefined,
    childrenLoaded: !(needsSchema || canLoadTables),
  }
}

// ---------------------------------------------------------------------------
// 分组取数：库 / 模式 → 六分组；表 / 视图 → 三分组
// ---------------------------------------------------------------------------

// 分组 → 该组内对象的节点类型
const GROUP_OBJECT_KIND = {
  tables: 'table',
  views: 'view',
  procedures: 'procedure',
  triggers: 'trigger',
  events: 'event',
  functions: 'function',
  indexes: 'index',
  'table-triggers': 'trigger',
}

const GROUP_EMPTY_HINT = {
  tables: 'groupHasTables',
  views: 'groupHasViews',
  procedures: 'groupHasProcedures',
  triggers: 'groupHasTriggers',
  events: 'groupHasEvents',
  functions: 'groupHasFunctions',
  columns: 'groupHasColumns',
  indexes: 'groupHasIndexes',
  'table-triggers': 'groupHasTableTriggers',
}

// 对象节点：视图可再展开看列/索引，其余为叶子
const makeObjectNode = (opts) => {
  const {
    name, kind, parent, attrs = null, instanceId = '', dbFamily = '',
    schemaPath = '', table = '', db = '',
  } = opts
  const expandable = kind === 'view'
  return {
    key: `${parent.key}#obj#${kind}#${name}`,
    kind,
    nodeKind: 'object',
    objectKind: kind,
    title: name,
    name,
    attrs,
    db,
    schemaPath,
    table,
    dbFamily,
    instanceId,
    isLeaf: !expandable,
    children: expandable ? [] : undefined,
    childrenLoaded: !expandable,
  }
}

// schema-browser 列节点 → 树列节点（带主键 / 类型，联合主键每列都带 is_primary）
const browserColumnsToNodes = (list, parentNode) => (list || [])
  .map((item) => {
    const name = String(item?.name || item?.label || '').trim()
    if (!name) return null
    const attrs = item?.attrs || {}
    const isPrimary = attrs.is_primary === true || String(attrs.is_primary) === '1' || String(attrs.is_primary).toUpperCase() === 'PRI'
    return {
      title: name,
      key: `${parentNode.key}#col#${name}`,
      nodeKind: 'column',
      column_name: name,
      column_type: String(attrs.column_type || attrs.data_type || ''),
      column_comment: String(attrs.comment || ''),
      column_nullable: attrs.nullable === true,
      column_primary: isPrimary,
      scopedSlots: { switcherIcon: 'child' },
      isLeaf: true,
    }
  })
  .filter(Boolean)

const browserItemsToNodes = (list, parentNode, group) => (list || []).map((item) =>
  makeObjectNode({
    name: String(item?.name || item?.label || ''),
    kind: GROUP_OBJECT_KIND[group] || String(item?.kind || 'object'),
    parent: parentNode,
    attrs: item?.attrs || null,
    instanceId: parentNode.instanceId,
    dbFamily: parentNode.dbFamily,
    schemaPath: parentNode.schemaPath,
    table: parentNode.table,
    db: parentNode.db,
  }),
)

// 拉分组计数：失败不阻塞，分组照常渲染（只是没有数量徽标）
const fetchGroupCounts = async (params) => {
  try {
    const res = await GetSchemaBrowserChildrenApi(params)
    const map = {}
    ;(res?.data || []).forEach((item) => {
      map[String(item?.name || '')] = Number(item?.count || 0)
    })
    return map
  } catch (_) {
    return {}
  }
}

// 库 / 模式节点 → 固定六分组（分组为空也渲染）
const buildDatabaseGroupNodes = async (node) => {
  const resource = node.resource
  // resource 缺失说明节点数据异常：必须 reject（而不是静默返回），
  // 否则 rc-tree 把节点记为 loaded，children 永远为空，之后怎么点都展不开
  if (!resource) {
    return Promise.reject(new Error('database node missing resource payload'))
  }
  const instanceId = resource.instance_id || node.instanceId
  const schemaPath = String(resource.schema || '')
  const counts = await fetchGroupCounts({ instance_id: instanceId, schema: schemaPath, scope: 'database-groups' })
  node.children = DATABASE_GROUPS.map((group) =>
    makeGroupNode({
      group,
      parent: node,
      instanceId,
      dbFamily: node.dbType,
      resource,
      schemaPath,
      db: resource.database_name || '',
      count: counts[group] ?? 0,
      source: node,
    }),
  )
  node.childrenLoaded = true
  uiData.instanceResourceTreeData = [...uiData.instanceResourceTreeData]
}

// 表 / 视图节点 → 固定三分组（列数本地可得，索引/触发器展开才知道，用 -1 表示未知）
const buildTableGroupNodes = (node) => {
  const schemaPath = String(node.schemaPath || node.resource_path || '')
  const tableName = String(node.table_name || node.name || '')
  const columnNodes = generatorColumnNodes(node.columns, node.table_schema, node.table_name)
  const columnCount = Array.isArray(columnNodes) ? columnNodes.length : 0
  node.children = TABLE_GROUPS.map((group) =>
    makeGroupNode({
      group,
      parent: node,
      instanceId: node.instanceId,
      dbFamily: node.dbFamily || node.dbType,
      resource: node.resource || null,
      schemaPath,
      table: tableName,
      count: group === 'columns' ? columnCount : -1,
      source: node,
    }),
  )
  node.childrenLoaded = true
  uiData.instanceResourceTreeData = [...uiData.instanceResourceTreeData]
}

// 展开分组：_tables 走既有授权通道（保留 allow/deny 与授权过滤），
// 其余分组走 schema-browser 接口；表下 columns 沿用本地解析，不发请求
const loadGroupChildren = async (node, opts = {}) => {
  if (node.loading) return
  // 刷新场景：先置为未加载，强制重新取一次
  if (opts.force) node.childrenLoaded = false
  node.loading = true
  try {
    if (TABLE_GROUPS.includes(node.group)) {
      if (node.group === 'columns') {
        // 优先走 schema-browser：能拿到 column_key（主键）与完整 column_type
        let list = []
        try {
          const res = await GetSchemaBrowserChildrenApi({
            instance_id: node.instanceId,
            schema: node.schemaPath,
            scope: 'table-group',
            group: 'columns',
            table: node.table,
          })
          list = browserColumnsToNodes(res?.data, node)
        } catch (_) {
          list = []
        }
        // 接口不可用时降级为表元数据里的 columns 串（无主键标识）
        if (!list.length) {
          const src = node.source
          list = generatorColumnNodes(src?.columns, src?.table_schema, src?.table_name)
        }
        node.children = injectEmptyPlaceholderIfNeeded(list, node.key, GROUP_EMPTY_HINT.columns)
      } else {
        const res = await GetSchemaBrowserChildrenApi({
          instance_id: node.instanceId,
          schema: node.schemaPath,
          scope: 'table-group',
          group: node.group,
          table: node.table,
        })
        node.children = injectEmptyPlaceholderIfNeeded(
          browserItemsToNodes(res?.data, node, node.group),
          node.key,
          GROUP_EMPTY_HINT[node.group],
        )
      }
      node.childrenLoaded = true
      uiData.instanceResourceTreeData = [...uiData.instanceResourceTreeData]
      return
    }

    if (node.group === 'tables') {
      const resource = node.resource
      if (!resource) return
      const payload = await loadTableNodesForResource(resource, { userTriggered: true })
      // 表下沉一层后仍要带上实例/库上下文，供表下三分组取数使用
      payload.nodes.forEach((n) => {
        if (!n.instanceId) {
          n.instanceId = resource.instance_id
          n.schemaPath = String(resource.schema || '')
          n.dbFamily = node.dbFamily
        }
      })
      const hintType = GROUP_EMPTY_HINT.tables
      node.children = payload.failed ? payload.nodes : injectEmptyPlaceholderIfNeeded(payload.nodes, node.key, hintType)
      if (!payload.failed) node.childrenLoaded = true
      syncSelectedResourceTables(resource, payload.tables)
      uiData.instanceResourceTreeData = [...uiData.instanceResourceTreeData]
      return
    }

    const res = await GetSchemaBrowserChildrenApi({
      instance_id: node.instanceId,
      schema: node.schemaPath,
      scope: 'group',
      group: node.group,
    })
    node.children = injectEmptyPlaceholderIfNeeded(
      browserItemsToNodes(res?.data, node, node.group),
      node.key,
      GROUP_EMPTY_HINT[node.group],
    )
    node.childrenLoaded = true
    uiData.instanceResourceTreeData = [...uiData.instanceResourceTreeData]
  } finally {
    node.loading = false
  }
}

// ---------------------------------------------------------------------------
// 树构建/同步的并发守卫
//
// 症状（99 复现）：切换窗口/实例时同一秒出现两次 database 列表请求——两个
// buildInstanceResourceTree 并发执行，而 build 一进来就把 expandedKeys 重置成
// [rootKey]，与 syncTreeByContext 末尾的「上下文展开」交错落地时，会把刚展开的
// 库节点又收回去 ⇒ 库节点选中但不展开第二级，且 children 已加载（之后点箭头
// 不再发请求，看起来就像「点了没反应」）。
//
// 处理：① 同一实例的 build 复用 in-flight promise（顺带省掉一次重复请求）；
//      ② syncTreeByContext 串行化，消除两个 sync 交错写 expandedKeys。
// ---------------------------------------------------------------------------
const treeBuildInflight = new Map()
let treeSyncQueue = Promise.resolve()
// 上次成功构建的实例：同一个实例重建树时不能重置用户/上下文的展开状态，
// 否则「刚点开的库」会被随后完成的 build 悄悄收起（快速点开时必现）。
let lastBuiltInstanceId = ''

const buildInstanceResourceTree = async (instanceMeta) => {
  if (!instanceMeta?.instance_id) {
    resetInstanceResourceTree()
    return
  }
  const instanceId = instanceMeta.instance_id
  const inflight = treeBuildInflight.get(instanceId)
  if (inflight) {
    return inflight
  }
  // 不用 Promise.prototype.finally：本项目构建 target 会把它降级成不支持 finally 的中间值
  const task = doBuildInstanceResourceTree(instanceMeta).then(
    (result) => {
      treeBuildInflight.delete(instanceId)
      return result
    },
    (err) => {
      treeBuildInflight.delete(instanceId)
      throw err
    },
  )
  treeBuildInflight.set(instanceId, task)
  return task
}

const doBuildInstanceResourceTree = async (instanceMeta) => {
  uiState.offlineInstanceNoticeShown = false
  const instanceId = instanceMeta.instance_id
  const dbType = instanceMeta.db_type || ''
  const family = resolveInstanceDbFamily(instanceMeta) || resolveFamilyInContext(dbType, instanceMeta)
  const rootKey = `instance:${instanceId}`
  const sameInstance = lastBuiltInstanceId === instanceId
  uiData.instanceResourceTreeData = [
    {
      title: formatInstanceShortLabel(instanceMeta),
      fullTitle: formatInstanceLabel(instanceMeta),
      key: rootKey,
      selectable: false,
      isLeaf: false,
      children: [],
      kind: 'instanceRoot',
      dbType: family,
      instanceId,
    },
  ]
  // 重建树绝不重置本实例已展开的键（key 都含 instance_id，直接过滤保留）：
  // 否则「刚点开的库」会被随后完成的 build 悄悄收起 —— 快速点击/切窗口时必现，
  // 表现就是库节点选中但第二级不展开。其它实例的键整体丢弃。
  const keptKeys = (uiData.instanceResourceExpandedKeys || [])
    .map(String)
    .filter((k) => k === rootKey || k.includes(instanceId))
  uiData.instanceResourceExpandedKeys = Array.from(new Set([rootKey, ...keptKeys]))
  if (!sameInstance) {
    lastBuiltInstanceId = instanceId
    uiData.instanceResourceSelectedKeys = []
  }
  uiData.instanceResourceAutoExpandParent = true

  const primaryType = getPrimaryResourceType(family)
  const fetchOpts = { userTriggered: true }
  let resourcesRes = await fetchResources(instanceId, primaryType, '', fetchOpts)
  if (!resourcesRes.list.length && family === FAMILY_ORACLE && primaryType === 'schema') {
    resourcesRes = await fetchResources(instanceId, 'database', '', fetchOpts)
  }
  const list = resourcesRes.list.map((item) => makeInstanceResourceNode(item, family))
  uiData.instanceResourceTreeData[0].children = resourcesRes.failed ? list : injectEmptyPlaceholderIfNeeded(list, uiData.instanceResourceTreeData[0].key, 'instanceRoot')
}

const loadInstanceResourceNode = async (treeNode) => {
  const node = treeNode?.dataRef
  if (!node) {
    return Promise.resolve()
  }
  const resourceInstanceID = node.instanceId || node.resource?.instance_id
  const instanceMeta = getInstanceMeta(resourceInstanceID || uiData.selectedInstanceId)
  if (resourceInstanceID && isInstanceConnectionUnavailable(instanceMeta)) {
    showOfflineInstanceNotice(instanceMeta)
    node.childrenLoaded = false
    // 必须 reject 而不是 resolve：rc-tree 只在 promise resolve 时把节点记入
    // loadedKeys，一旦记账，之后点箭头不会再触发 loadData（Tree.js onNodeLoad
    // 里 loadedKeysSet.has(key) 早退），children 又是空 ⇒ 该节点永远展开不出来。
    // reject 会让 rc-tree 保留重试机会（实例恢复后再点可正常加载）。
    return Promise.reject(new Error('instance connection unavailable'))
  }
  if (node.childrenLoaded) {
    return Promise.resolve()
  }
  // 并发守卫：层级加深后同一节点可能被重复触发
  if (node.loading) {
    // 未加载完成前不能 resolve：resolve 会被 rc-tree 记为 loaded，
    // 若本次加载最终失败，节点就再也没有触发 loadData 的机会
    return new Promise((resolve, reject) => {
      let waited = 0
      const timer = setInterval(() => {
        waited += 150
        if (!node.loading) {
          clearInterval(timer)
          if (node.childrenLoaded || (Array.isArray(node.children) && node.children.length)) {
            resolve()
          } else {
            reject(new Error('concurrent load failed'))
          }
        } else if (waited >= 15000) {
          clearInterval(timer)
          reject(new Error('concurrent load timeout'))
        }
      }, 150)
    })
  }

  const userTriggered = true
  // 分组节点：按分组类型取实际对象
  if (node.kind === 'group') {
    await loadGroupChildren(node)
    return Promise.resolve()
  }

  if (node.kind === 'database' && (isPostgresFamilyDbType(node.dbType) || node.dbType === 'sqlserver')) {
    const resource = node.resource
    const schemasRes = await fetchResources(resource.instance_id, 'schema', resource.schema, { userTriggered })
    const list = schemasRes.list.map((item) => makeInstanceResourceNode(item, node.dbType))
    node.children = schemasRes.failed ? list : injectEmptyPlaceholderIfNeeded(list, node.key, 'databaseHasSchema')
    if (!schemasRes.failed) {
      node.childrenLoaded = true
    }
    uiData.instanceResourceTreeData = [...uiData.instanceResourceTreeData]
    return Promise.resolve()
  }

  if (node.kind === 'database' || node.kind === 'schema') {
    await buildDatabaseGroupNodes(node)
  } else if (node.kind === 'table' || node.kind === 'view' || node.nodeKind === 'view') {
    buildTableGroupNodes(node)
  }
  return Promise.resolve()
}

const teardownRuntimeRefs = {
  destroyAbort: null,
  searchModeWatchStop: null,
  keywordWatchStop: null,
  serverWatchStop: null,
  expandedKeysWatchStop: null,
  selectedInstanceWatchStop: null,
  instanceCtxWatchStop: null,
  skeletonWatchStop: null,
  locateCancel: null,
}

const expandSkeletonRootOnceRef = { value: false }
teardownRuntimeRefs.skeletonWatchStop = watch(
  () => [uiData.selectedInstanceId, uiData.resourceTreeKeyword, displayInstanceResourceTreeData.value?.[0]?._flatSkeleton],
  () => { expandSkeletonRootOnceRef.value = false },
  { flush: 'post' },
)

const handleInstanceResourceExpand = (expandedKeys) => {
  uiData.instanceResourceExpandedKeys = expandedKeys
  uiData.instanceResourceAutoExpandParent = false
}

const ensureAutoExpandForAllHitAncestors = (opts = {}) => {
  const force = !!opts.force
  if (!force && expandSkeletonRootOnceRef.value) return
  const root = (displayInstanceResourceTreeData.value || [])[0]
  if (!root) return
  const rootKey = String(root.key || '')
  const dbKeys = []
  const schemaKeys = []
  // 表下沉到分组下一层，命中表的祖先里多了一层分组，必须一并展开，否则搜索命中看不见
  const groupKeys = []
  const walk = (list = []) => {
    for (const node of list) {
      if (!node) continue
      const kind = node?.kind
      const nodeKind = node?.nodeKind
      const isDatabase = kind === 'database' || node?.resource?.resource_type === 'database'
      const isSchema = kind === 'schema' || node?.resource?.resource_type === 'schema'
      const isGroup = kind === 'group' || nodeKind === 'group'
      const isTable = nodeKind === 'table' || kind === 'table' || node?.resource?.resource_type === 'table' || node?._flatVirtual
      const hasHitChild = !!(node?._flatMatch || node?._flatInjected || (Array.isArray(node?.children) && node.children.length > 0 && !isTable))
      if (isDatabase && hasHitChild && node.key) dbKeys.push(String(node.key))
      if (isSchema && hasHitChild && node.key) schemaKeys.push(String(node.key))
      if (isGroup && hasHitChild && node.key) groupKeys.push(String(node.key))
      if (node?.children && node.children.length) walk(node.children)
    }
  }
  walk(displayInstanceResourceTreeData.value || [])
  const mergedSet = new Set((uiData.instanceResourceExpandedKeys || []).map(String))
  if (rootKey) mergedSet.add(rootKey)
  for (const k of dbKeys) mergedSet.add(k)
  for (const k of schemaKeys) mergedSet.add(k)
  for (const k of groupKeys) mergedSet.add(k)
  uiData.instanceResourceExpandedKeys = Array.from(mergedSet)
  uiData.instanceResourceAutoExpandParent = true
  expandSkeletonRootOnceRef.value = true
}

// ---------------------------------------------------------------------------
// 双击插入编辑器：对象树删除后该行为收敛到资源树
// ---------------------------------------------------------------------------

// 标识符引用：MySQL 系用反引号，其余用双引号；简单名不引用，保持 SQL 可读
const quoteIdent = (name, family) => {
  const raw = String(name || '')
  if (!raw) return ''
  if (family === 'mysql' || family === 'clickhouse') {
    return /^[A-Za-z0-9_$]+$/.test(raw) ? raw : '`' + raw.replace(/`/g, '``') + '`'
  }
  return /^[a-z_][a-z0-9_$]*$/.test(raw) ? raw : '"' + raw.replace(/"/g, '""') + '"'
}

const buildInsertText = (node) => {
  if (!node) return ''
  const family = String(node.dbFamily || node.dbType || '').trim().toLowerCase()
  const kind = String(node.kind || '')
  if (node.nodeKind === 'table' || kind === 'table' || kind === 'view') {
    // 限定名：库.表（PG / SQLServer 为 库.模式.表）
    const parts = String(node.schemaPath || node.resource_path || '')
      .split('.')
      .filter(Boolean)
      .map((p) => quoteIdent(p, family))
    parts.push(quoteIdent(String(node.table_name || node.name || '').split('#')[0] || node.title, family))
    return parts.filter(Boolean).join('.')
  }
  if (node.nodeKind === 'column') {
    return quoteIdent(node.column_name || node.title, family)
  }
  if (node.nodeKind === 'object' || ['procedure', 'function', 'event', 'index', 'trigger'].includes(kind)) {
    return quoteIdent(node.name || node.title, family)
  }
  return ''
}

const insertToEditor = (text) => {
  const value = String(text || '').trim()
  if (!value) return
  if (!dasConsoleAction) {
    showMessage('warning', '编辑器通道不可用')
    return
  }
  dasConsoleAction.type = 'insert-sql-to-editor'
  dasConsoleAction.payload = { sqlText: value }
  dasConsoleAction.nonce = Date.now()
}

const handleNodeDblClick = (node) => {
  if (!node) return
  if (node.nodeKind === 'group' || node.nodeKind === 'emptyPlaceholder') return
  const text = buildInsertText(node)
  if (text) insertToEditor(text)
}

// dblclick 直接绑在 title 插槽的行元素上：这棵树的节点行没有 data-node-key，
// 靠 DOM 反查不可靠（对象树那棵有该属性），插槽里 nodeRecord 本身就是节点对象。

const prepareResourceTablesForSelection = async (node) => {
  await buildDatabaseGroupNodes(node)
  const tablesGroup = (node.children || []).find((child) => child?.kind === 'group' && child.group === 'tables')
  if (tablesGroup && !tablesGroup.childrenLoaded) {
    await loadGroupChildren(tablesGroup)
  }
}

// 从库/模式节点向下找 tables 分组，再把表摊平成 {表名: 列名数组}
const collectTablesFromResourceNode = (node) => {
  const tablesGroup = (node.children || []).find((child) => child?.kind === 'group' && child.group === 'tables')
  return buildTableMapFromNodes(tablesGroup?.children || [])
}

const handleInstanceResourceSelect = async (selectedKeys, event) => {
  const node = event?.node
  if (!node) {
    return
  }
  if (node._flatVirtual && node._flatHitObj) {
    uiData.instanceResourceSelectedKeys = (uiData.instanceResourceSelectedKeys || []).filter(
      (k) => String(k) !== String(node.key),
    )
    nextTick(() => locateToObject(node._flatHitObj)).catch(() => {})
    return
  }
  uiData.instanceResourceSelectedKeys = selectedKeys
  // 分组节点不承载库/表上下文：选中它不改变编辑器上下文，也不展开取数以外的副作用
  if (node.kind === 'group') {
    return
  }
  const currentInstanceMeta = getInstanceMeta(node.instanceId || node.resource?.instance_id || uiData.selectedInstanceId)
  if (node.kind === 'database') {
    if (isPostgresFamilyDbType(node.dbType) || node.dbType === 'sqlserver') {
      const resourceNames = deriveResourceNames(node.resource, node.dbType)
      uiData.selectedDatabase = resourceNames.databaseName || node.resource?.schema
      uiData.selectedSchema = undefined
      resetInstanceData()
      uiData.instanceData.instance_id = node.resource?.instance_id || ''
      uiData.instanceData.db_type = uiData.selectedDbType
      uiData.instanceData.database = resourceNames.databaseName || node.resource?.schema || ''
      uiData.instanceData.resource_type = 'database'
      uiData.instanceData.display_name = resourceNames.displayName || node.resource?.display_name || node.resource?.schema || ''
      uiData.instanceData.remark = uiData.selectedInstanceRemark
      emitInstanceData()
      expandInstanceResourceNode(node)
      return
    }
    if (isInstanceConnectionUnavailable(currentInstanceMeta)) {
      applyResourceSelection(node.resource, node.dbType, {})
      showOfflineInstanceNotice(currentInstanceMeta)
      return
    }
    const instanceMeta = getInstanceMeta(node.resource.instance_id)
    const dbType = normalizeDbTypeValue(instanceMeta?.db_type || node.dbType || '')
    uiData.selectedDatabase = node.resource.schema
    uiData.selectedSchema = undefined
    uiState.isTreeLoading = true
    try {
      await prepareResourceTablesForSelection(node).catch(() => {})
      expandInstanceResourceNode(node)
      applyResourceSelection(node.resource, dbType, collectTablesFromResourceNode(node))
    } finally {
      uiState.isTreeLoading = false
    }
    return
  }
  if (node.kind === 'schema') {
    if (isInstanceConnectionUnavailable(currentInstanceMeta)) {
      const dbType = normalizeDbTypeValue(currentInstanceMeta?.db_type || node.dbType || '')
      applyResourceSelection(node.resource, dbType, {})
      showOfflineInstanceNotice(currentInstanceMeta)
      return
    }
    const instanceMeta = getInstanceMeta(node.resource.instance_id)
    const dbType = normalizeDbTypeValue(instanceMeta?.db_type || node.dbType || '')
    const resourceNames = deriveResourceNames(node.resource, dbType)
    uiData.selectedSchema = resourceNames.resourcePath || node.resource.schema
    if (resourceNames.databaseName) {
      uiData.selectedDatabase = resourceNames.databaseName
    }
    uiState.isTreeLoading = true
    try {
      await prepareResourceTablesForSelection(node).catch(() => {})
      expandInstanceResourceNode(node)
      applyResourceSelection(node.resource, dbType, collectTablesFromResourceNode(node))
    } finally {
      uiState.isTreeLoading = false
    }
    return
  }
  if (node.kind === 'table') {
    let vals = String(node.key || '').split('#')
    const dbType = normalizeDbTypeValue(uiData.selectedDbType || uiData.instanceData.db_type || node?.dbType || '')
    const raw = {
      schema: node.resource_path || uiData.instanceData.schema,
      table: vals[1],
    }
    uiData.selectedTable = normalizePgSelectedPayloadForApi(raw, {
      dbType,
      schema_name: node.schema_name,
      database_name: node.database_name,
    })
    return
  }
  if (node.kind === 'resource') {
    const instanceMeta = getInstanceMeta(node.resource.instance_id)
    const dbType = normalizeDbTypeValue(instanceMeta?.db_type || node.dbType || '')
    applyResourceSelection(node.resource, dbType)
  }
}

// 串行化包装：上下文同步涉及 build（重置 expandedKeys）与上下文展开两段异步，
// 两个 sync 并发交错是「库节点被收起」的根因，这里按入队顺序逐个执行。
const syncTreeByContext = (context) => {
  const snapshot = { ...(context || {}) }
  treeSyncQueue = treeSyncQueue.then(() => doSyncTreeByContext(snapshot)).catch((e) => {
    console.error('[TREE] sync failed:', e && e.stack || e)
  })
  return treeSyncQueue
}

// 树未就绪（构建中的间隙）导致 sync 提前返回时的补同步：有次数上限 + 上下文签名去重，
// 保证「恢复上下文后库节点一定被展开」，同时不会无限循环。
let contextResyncTimer = null
let contextResyncCount = 0
let contextResyncSig = ''
const scheduleContextResync = () => {
  const sig = [uiData.selectedInstanceId, uiData.selectedDatabase, uiData.selectedSchema].join('|')
  if (contextResyncSig !== sig) {
    contextResyncSig = sig
    contextResyncCount = 0
  }
  if (contextResyncCount >= 3) return
  contextResyncCount += 1
  clearTimeout(contextResyncTimer)
  contextResyncTimer = setTimeout(() => {
    syncTreeByContext(dasInstanceData.value || {})
  }, 300)
}


const doSyncTreeByContext = async (context) => {
  if (!context?.instance_id) {
    uiData.selectedInstanceId = undefined
    clearCurrentSelection()
    return
  }
  if (!uiData.instances.length) {
    await fetchInstances()
  }
  const instanceMeta = getInstanceMeta(context.instance_id)
  if (!instanceMeta) {
    return
  }
  const instanceChanged = uiData.selectedInstanceId !== context.instance_id
  if (instanceChanged) {
    uiState.offlineMessage = ''
    uiState.offlineInstanceNoticeShown = false
    if (isInstanceConnectionUnavailable(instanceMeta)) {
      showOfflineInstanceNotice(instanceMeta)
    }
  }
  uiData.selectedInstanceId = context.instance_id
  uiData.selectedDbType = instanceMeta.db_type || ''
  uiData.selectedInstanceRemark = instanceMeta.remark || ''
  uiData.instanceData = {
    ...uiData.instanceData,
    instance_id: context.instance_id,
    db_type: context.db_type || instanceMeta.db_type || '',
    database: context.database || '',
    schema_name: context.schema_name || '',
    schema: context.schema || '',
    resource_type: context.resource_type || '',
    display_name: getPureSchemaDisplayName({
      dbType: context.db_type || instanceMeta.db_type || '',
      schema: context.schema,
      displayName: context.display_name,
      schemaName: context.schema_name,
      databaseName: context.database,
    }) || context.display_name || context.schema_name || context.schema || context.database || '',
    remark: context.remark || instanceMeta.remark || '',
    tables: context.tables || uiData.instanceData.tables || {},
  }
  if (instanceChanged || !uiData.instanceResourceTreeData.length) {
    await buildInstanceResourceTree(instanceMeta)
    // build 会把 expandedKeys 重置成 [rootKey]，持久化的展开状态必须在重置之后恢复，
    // 否则恢复结果会被随后的 build 覆盖（或反过来覆盖上下文展开）。这里恢复并补上 rootKey。
    restoreExpandedKeysForInstance(context.instance_id)
    const restoredRootKey = `instance:${context.instance_id}`
    uiData.instanceResourceExpandedKeys = Array.from(
      new Set([restoredRootKey, ...(uiData.instanceResourceExpandedKeys || [])]),
    )
  }

  const root = uiData.instanceResourceTreeData?.[0]
  if (!root?.children?.length) {
    // 树可能仍在别的构建流程中（切窗口/切实例瞬间）：安排一次补同步。
    // 不补的话上下文对应的库节点永远不会被展开 —— 正是「库选中但不展开第二级」的来源之一。
    scheduleContextResync()
    return
  }
  const family = resolveInstanceDbFamily(instanceMeta) || resolveFamilyInContext(instanceMeta.db_type, instanceMeta)

  if (family === FAMILY_POSTGRES || family === FAMILY_SQLSERVER) {
    if (!context.database) {
      uiData.instanceResourceSelectedKeys = []
      return
    }
    const databaseNode = root.children.find((node) => node?.resource?.schema === context.database)
    if (!databaseNode) return
    uiData.instanceResourceExpandedKeys = Array.from(new Set([root.key, ...(uiData.instanceResourceExpandedKeys || []), databaseNode.key]))
    uiData.instanceResourceSelectedKeys = [databaseNode.key]
    if (!context.schema) {
      return
    }
    // loadInstanceResourceNode 失败（如实例暂不可达）会 reject，不应阻断上下文同步
    await loadInstanceResourceNode(databaseNode).catch(() => {})
    const schemaNode = (databaseNode.children || []).find((node) => node?.resource?.schema === context.schema)
    if (!schemaNode) return
    uiData.instanceResourceSelectedKeys = [schemaNode.key]
    return
  }

  if (!context.schema) {
    uiData.instanceResourceSelectedKeys = []
    return
  }
  const targetNode = root.children.find((node) => node?.resource?.schema === context.schema)
  if (!targetNode) return
  uiData.instanceResourceSelectedKeys = [targetNode.key]
}

const generatorColumnNodes = (columns, tableSchema, tableName) => {
  const rawList = String(columns || '')
    .split('@@')
    .filter(Boolean)
  if (!rawList.length) return []

  const nullableFlags = new Set(['YES', 'Y', 'NULLABLE', 'NULL', '可空'])
  const primaryFlags = new Set(['PRI', 'PK', 'PRIMARY', 'PRIMARY KEY', '主键'])
  const uniqueFlags = new Set(['UNI', 'UNIQUE', '唯一'])

  const parseColumnSegments = (rawSegment) => {
    const parts = String(rawSegment || '').split('$$').map((s) => String(s || '').trim())
    const [rawName, rawType, rawExtra1 = '', rawExtra2 = '', rawExtra3 = '', rawExtra4 = ''] = parts
    const name = String(rawName || '').trim()
    const type = String(rawType || '').trim()

    const extras = [rawExtra1, rawExtra2, rawExtra3, rawExtra4].map((s) => String(s || '').trim()).filter(Boolean)
    const lowerExtras = extras.map((s) => s.toLowerCase())

    let nullable = false
    let primaryKey = false
    let unique = false
    let autoIncrement = false
    let defaultText = ''
    let commentText = ''

    const consumed = new Array(extras.length).fill(false)

    lowerExtras.forEach((s, idx) => {
      if (consumed[idx]) return
      if (nullableFlags.has(s) || nullableFlags.has(s.toUpperCase())) {
        nullable = true
        consumed[idx] = true
        return
      }
      if (primaryFlags.has(s.toUpperCase())) {
        primaryKey = true
        consumed[idx] = true
        return
      }
      if (uniqueFlags.has(s.toUpperCase())) {
        unique = true
        consumed[idx] = true
        return
      }
      const normalized = s.replace(/\s+/g, ' ').toLowerCase()
      if (normalized === 'auto_increment' || normalized === 'auto-increment' || normalized.endsWith(' auto_increment') || normalized.startsWith('auto_increment')) {
        autoIncrement = true
        consumed[idx] = true
        return
      }
      if (s.toLowerCase().startsWith('default ') || s.toLowerCase().startsWith('default:')) {
        defaultText = s.replace(/^default[\s:]*/i, '').trim()
        consumed[idx] = true
        return
      }
      if (s === 'NOT NULL' || s === 'NOT NULL' || s.toLowerCase() === 'not null') {
        nullable = false
        consumed[idx] = true
      }
    })

    const remaining = extras.filter((_, idx) => !consumed[idx])
    if (remaining.length) {
      commentText = remaining.join(' ')
    }

    return {
      name,
      type,
      nullable,
      primaryKey,
      unique,
      autoIncrement,
      defaultText,
      comment: commentText,
    }
  }

  return rawList
    .map((v) => {
      const parsed = parseColumnSegments(v)
      const colName = parsed.name
      const flatFlags = []
      if (parsed.primaryKey) flatFlags.push('主键')
      if (parsed.unique) flatFlags.push('唯一')
      if (parsed.autoIncrement) flatFlags.push('自增')
      flatFlags.push(parsed.nullable ? '可空' : '非空')
      const joinedText = [parsed.name, parsed.type, ...flatFlags, parsed.defaultText ? `DEFAULT ${parsed.defaultText}` : '', parsed.comment]
        .filter(Boolean)
        .join(' ')
      return {
        title: joinedText,
        key: `${tableSchema}#${tableName}#${colName}`,
        column_name: colName,
        column_type: parsed.type,
        column_comment: parsed.comment,
        column_nullable: parsed.nullable,
        column_primary: parsed.primaryKey,
        column_unique: parsed.unique,
        column_auto_increment: parsed.autoIncrement,
        column_default: parsed.defaultText,
        nodeKind: 'column',
        scopedSlots: { switcherIcon: 'child' },
        isLeaf: true,
      }
    })
    .filter((item) => item.column_name)
}

const generatorTableNode = (grants, table) => {
  const hasColumns = parseColumnNames(table.columns).length > 0
  const rule = checkTableRule(grants, table.table_name) ? 'allow' : 'deny'
  return {
    title: `${table.table_name}#${rule}`,
    key: `${table.table_schema}#${table.table_name}`,
    scopedSlots: { title: 'custom' },
    kind: 'table',
    table_name: table.table_name,
    table_schema: table.table_schema,
    database_name: table.database_name,
    schema_name: table.schema_name,
    display_name: table.display_name,
    columns: table.columns,
    table_rows: table.table_rows,
    data_length_kb: table.data_length_kb,
    engine: table.engine,
    update_time: table.update_time,
    table_comment: table.table_comment,
    isLeaf: !hasColumns,
    children: hasColumns ? [] : undefined,
    childrenLoaded: !hasColumns,
  }
}

// 通配符 → 正则：* 任意序列、? 单字符（与后端 wildcardMatchTable 同口径：
// 模式按存储原样、表名小写化后匹配，不加 i 标志，避免「树上有权限、查询被拒」错位）
const wildcardRegExp = (pattern) => {
  const escaped = String(pattern || '')
    .replace(/[.+^${}()|[\]\\]/g, '\\$&')
    .replace(/\*/g, '.*')
    .replace(/\?/g, '.')
  return new RegExp(`^${escaped}$`)
}

// 表级授权判定（与后端 matchTableByGrantRow 同口径）。
// 授权行三种 table_mode：exact 精确表名 / wildcard 通配符 / except 排除清单
// （table_name 存逗号拼接的排除表，命中排除项 = 不覆盖）。任一行命中即有权限。
const checkTableRule = (grants, table) => {
  if (!grants) return false
  if (grants.tables === '*') return true
  // 优先用结构化 table_grants（带 table_mode）；旧格式 tables 数组按 exact 处理
  const rows = Array.isArray(grants.table_grants) && grants.table_grants.length
    ? grants.table_grants
    : Array.isArray(grants.tables)
      ? grants.tables
      : []
  if (!rows.length) return false
  const tableName = String(table || '').toLowerCase()
  const hasAllow = rows.some((r) => r.rule === 'allow')
  if (hasAllow) {
    return rows.some((r) => {
      if (r.rule !== 'allow') return false
      const mode = String(r.table_mode || 'exact').toLowerCase()
      if (mode === 'wildcard') return wildcardRegExp(r.table).test(tableName)
      if (mode === 'except') {
        return !String(r.table || '')
          .split(',')
          .some((item) => item.trim().toLowerCase() === tableName)
      }
      return String(r.table || '').trim().toLowerCase() === tableName
    })
  }
  // 纯 deny 行（旧模型）：deny 清单里的表无权限，其余可见
  return !rows.some(
    (r) => r.rule === 'deny' && String(r.table || '').trim().toLowerCase() === tableName,
  )
}

const buildUseSqlFromNode = (node) => {
  if (!node || !node.resource) return ''
  const instanceMeta = getInstanceMeta(node.instanceId || node.resource?.instance_id || uiData.selectedInstanceId) || {}
  const dbType = normalizeDbTypeValue(instanceMeta?.db_type || node.dbType || uiData.selectedDbType || '')
  // 家族必须结合实例判定：OceanBase 的租户模式只在实例配置里
  const family =
    resolveInstanceDbFamily(instanceMeta) ||
    resolveFamilyInContext(node.dbType || uiData.selectedDbType, instanceMeta)
  const resourceNames = deriveResourceNames(node.resource, family)
  const databaseName = resourceNames.databaseName || String(node.resource?.schema || '').trim()
  const schemaName = resourceNames.resourcePath || String(node.resource?.schema || '').trim()
  const isDatabase = node.kind === 'database'

  if (!dbType || (!databaseName && !schemaName)) return ''

  if (family === FAMILY_SQLSERVER) {
    const parts = []
    if (databaseName) {
      parts.push(`USE ${quoteIdentByDbType(databaseName, family)};`)
    }
    if (!isDatabase && schemaName) {
      parts.push(`-- 当前 Schema: ${quoteIdentByDbType(schemaName, family)}`)
      parts.push(`-- 提示：SQL Server 中切换默认 Schema 需针对登录用户单独设置 DEFAULT_SCHEMA`)
    }
    return parts.join('\n')
  }

  if (family === FAMILY_POSTGRES) {
    const parts = []
    if (databaseName) {
      parts.push(`-- PostgreSQL 连接已绑定数据库 ${quoteIdentByDbType(databaseName, family)}，切换需重新建立连接`)
    }
    if (!isDatabase && schemaName) {
      parts.push(`SET search_path TO ${quoteIdentByDbType(schemaName, family)}, "$user", public;`)
    }
    return parts.join('\n')
  }

  if (family === FAMILY_ORACLE) {
    const parts = []
    if (!isDatabase && schemaName) {
      parts.push(`ALTER SESSION SET CURRENT_SCHEMA = ${quoteIdentByDbType(schemaName, family)};`)
    } else if (databaseName) {
      parts.push(`-- Oracle 当前 PDB / CDB: ${quoteIdentByDbType(databaseName, family)}`)
      parts.push(`-- 提示：切换 Container 请使用 ALTER SESSION SET CONTAINER = ...`)
    }
    return parts.join('\n')
  }

  const target = databaseName || schemaName
  if (!target) return ''
  return `USE ${quoteIdentByDbType(target, family)};`
}

const onContextMenuClick = async (node, menuKey) => {
  if (!node) return

  if (menuKey === 'copyNodeValue') {
    const targetText = getNodeCopyText(node)
    copyText(targetText, '已复制')
    return
  }

  if (menuKey === 'refreshNodeChildren') {
    const instanceMeta = getInstanceMeta(node.instanceId || node.resource?.instance_id || uiData.selectedInstanceId)
    if (isInstanceConnectionUnavailable(instanceMeta) && node.kind !== 'instanceRoot') {
      showOfflineInstanceNotice(instanceMeta)
      return
    }
    const loadingMessage = message.loading('刷新中...', 0)
    // 浏览器式刷新：记录刷新前展开键，刷新后沿展开路径逐层重建子级，
    // 避免出现「箭头仍为展开态、内容却塌陷」的割裂体验
    const snapshotExpandedSet = new Set((uiData.instanceResourceExpandedKeys || []).map(String))
    try {
      const refreshed = await refreshResourceNodeChildren(node, { userTriggered: true })
      if (refreshed) {
        await refreshExpandedDescendants(
          Array.isArray(node.children) ? node.children : [],
          snapshotExpandedSet,
        )
        uiData.instanceResourceTreeData = [...uiData.instanceResourceTreeData]
        message.success('刷新成功')
      }
    } catch {
      message.error('刷新失败')
    } finally {
      loadingMessage()
    }
  }

  if (menuKey === 'insertUseSqlToEditor' && ['database', 'schema', 'resource'].includes(node.kind)) {
    const sqlText = buildUseSqlFromNode(node)
    if (!sqlText) {
      message.warning('当前节点缺少可生成语句的信息')
      return
    }
    const instanceMeta = getInstanceMeta(node.instanceId || node.resource?.instance_id || uiData.selectedInstanceId) || {}
    const dbType = normalizeDbTypeValue(instanceMeta?.db_type || node.dbType || uiData.selectedDbType || '')
    const resourceNames = deriveResourceNames(node.resource, dbType)
    const payload = {
      instance_id: node.resource?.instance_id || uiData.selectedInstanceId || '',
      db_type: dbType,
      remark: uiData.instanceData.remark || uiData.selectedInstanceRemark || '',
      sqlText,
      database: resourceNames.databaseName || uiData.instanceData.database || '',
      schema: resourceNames.resourcePath || uiData.instanceData.schema || '',
      schema_name: resourceNames.schemaName || resourceNames.resourcePath || uiData.instanceData.schema_name || '',
      resource_type: node.kind,
      display_name: resourceNames.displayName || node.resource?.display_name || uiData.instanceData.display_name || '',
    }
    if (dasConsoleAction) {
      dasConsoleAction.type = 'insert-sql-to-editor'
      dasConsoleAction.payload = payload
      dasConsoleAction.nonce = Date.now()
    }
    return
  }

  if (menuKey === 'previewTableData' && (node.nodeKind === 'table' || isViewTreeNode(node))) {
    const payload = buildPreviewContextPayload(node)
    if (!payload.instance_id || !payload.sqlText) {
      message.warning('当前表缺少可执行的上下文信息')
      return
    }
    if (dasConsoleAction) {
      dasConsoleAction.type = 'preview-table-data'
      dasConsoleAction.payload = payload
      dasConsoleAction.nonce = Date.now()
    }
    return
  }

  if (menuKey === 'showObjectInfo' && showObjectInfo(node)) {
    return
  }

  if ((menuKey === 'showTableStructure' || menuKey === 'showTableMetadata') && selectTableNode(node)) {
    if (menuKey === 'showTableStructure') {
      showTableStructure()
    } else {
      showTableMetadata()
    }
  }
}

const parseDdlToColumnsAndIndexes = (rawDdl) => {
  const columns = []
  const indexes = []
  const ddlText = String(rawDdl || '').trim()
  if (!ddlText) {
    return { columns, indexes }
  }
  const m = ddlText.match(/CREATE\s+(?:OR\s+REPLACE\s+)?TABLE\s+[^(]+\(([\s\S]*)\)\s*([^;]*);?\s*$/i)
  const body = m ? m[1].trim() : ''
  const tailOptions = m ? (m[2] || '').trim() : ''
  if (!body) {
    return { columns, indexes }
  }
  const lines = []
  let current = ''
  let depth = 0
  let inSingle = false
  let inDouble = false
  let inBack = false
  for (let i = 0; i < body.length; i += 1) {
    const ch = body[i]
    const prev = body[i - 1] || ''
    if (ch === "'" && prev !== '\\' && !inDouble && !inBack) inSingle = !inSingle
    else if (ch === '"' && prev !== '\\' && !inSingle && !inBack) inDouble = !inDouble
    else if (ch === '`' && !inSingle && !inDouble) inBack = !inBack
    if (!inSingle && !inDouble && !inBack) {
      if (ch === '(') depth += 1
      else if (ch === ')') depth -= 1
      else if (ch === ',' && depth === 0) {
        lines.push(current.trim())
        current = ''
        continue
      }
    }
    current += ch
  }
  if (current.trim()) lines.push(current.trim())

  const stripQuote = (s) => String(s || '')
    .trim()
    .replace(/^[`'"[]+|[`'"\]]+$/g, '')

  const parseTypeWithBrackets = (tokens, startIdx) => {
    let idx = startIdx
    let typ = tokens[idx] || ''
    idx += 1
    while (idx < tokens.length && /^\s*\(/.test(tokens[idx])) {
      let buffer = ''
      let parenDepth = 0
      let started = false
      while (idx < tokens.length) {
        const piece = tokens[idx]
        for (let k = 0; k < piece.length; k += 1) {
          const c = piece[k]
          if (c === '(') { parenDepth += 1; started = true }
          else if (c === ')') parenDepth -= 1
          buffer += c
        }
        idx += 1
        if (started && parenDepth <= 0) break
      }
      typ += buffer
      if (idx < tokens.length && /^(unsigned|zerofill)$/i.test(tokens[idx])) {
        typ += ` ${tokens[idx]}`
        idx += 1
      }
    }
    return { type: typ, nextIdx: idx }
  }

  const pushIndex = (raw) => {
    const line = raw.trim()
    if (!line) return
    const typeUpper = line.toUpperCase()
    let idxType = 'NORMAL'
    if (/^PRIMARY\s+KEY/i.test(typeUpper)) idxType = 'PRIMARY'
    else if (/^UNIQUE(?:\s+(?:KEY|INDEX))?/i.test(typeUpper)) idxType = 'UNIQUE'
    else if (/^FULLTEXT(?:\s+(?:KEY|INDEX))?/i.test(typeUpper)) idxType = 'FULLTEXT'
    else if (/^SPATIAL(?:\s+(?:KEY|INDEX))?/i.test(typeUpper)) idxType = 'SPATIAL'
    else if (/^(?:KEY|INDEX)/i.test(typeUpper)) idxType = 'NORMAL'
    else return
    const openParen = line.lastIndexOf('(')
    const closeParen = line.lastIndexOf(')')
    const head = openParen >= 0 ? line.slice(0, openParen).trim() : line.trim()
    const colsPart = openParen >= 0 && closeParen > openParen ? line.slice(openParen + 1, closeParen).trim() : ''
    const headTokens = head.split(/\s+/).filter(Boolean)
    let name = ''
    for (let i = 0; i < headTokens.length; i += 1) {
      const t = headTokens[i].toUpperCase()
      if (['PRIMARY', 'KEY', 'INDEX', 'UNIQUE', 'FULLTEXT', 'SPATIAL', 'CONSTRAINT'].includes(t)) continue
      if (/^USING$/i.test(t)) { i += 1; continue }
      name = stripQuote(headTokens[i])
      break
    }
    if (!name && idxType === 'PRIMARY') name = 'PRIMARY'
    const columnList = colsPart
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean)
      .map((seg) => {
        const base = seg.split(/\s+/)[0] || seg
        const len = /\((\d+)\)/.exec(seg)
        return {
          name: stripQuote(base),
          length: len ? Number(len[1]) : undefined,
        }
      })
    const usingM = /USING\s+(BTREE|HASH|RTREE|GIN|GIST)/i.exec(line)
    indexes.push({
      indexName: name,
      indexType: idxType,
      indexMethod: usingM ? usingM[1].toUpperCase() : '',
      columnList,
      columnText: columnList.map((c) => (c.length ? `${c.name}(${c.length})` : c.name)).join(', '),
      comment: '',
    })
  }

  lines.forEach((rawLine) => {
    const line = rawLine.trim()
    if (!line) return
    const upperStart = line.split(/\s+/)[0]?.toUpperCase() || ''
    if (['PRIMARY', 'UNIQUE', 'UNIQUE', 'KEY', 'INDEX', 'FULLTEXT', 'SPATIAL', 'CONSTRAINT'].includes(upperStart)) {
      pushIndex(line)
      return
    }
    if (/^(CHECK|FOREIGN|PARTITION|WITH|LIKE)/i.test(upperStart)) return
    const tokens = []
    let buf = ''
    let qS = false
    let qD = false
    let qB = false
    for (let i = 0; i < line.length; i += 1) {
      const c = line[i]
      const prev = line[i - 1] || ''
      if (c === "'" && prev !== '\\' && !qD && !qB) { qS = !qS; buf += c; continue }
      if (c === '"' && prev !== '\\' && !qS && !qB) { qD = !qD; buf += c; continue }
      if (c === '`' && !qS && !qD) { qB = !qB; buf += c; continue }
      if (/\s/.test(c) && !qS && !qD && !qB) {
        if (buf) { tokens.push(buf); buf = '' }
        continue
      }
      buf += c
    }
    if (buf) tokens.push(buf)
    if (tokens.length < 2) return
    const columnName = stripQuote(tokens[0])
    if (!columnName) return
    const { type, nextIdx } = parseTypeWithBrackets(tokens, 1)
    let idx = nextIdx
    let nullable = true
    let primary = false
    let unique = false
    let autoIncrement = false
    let defaultValue = ''
    let comment = ''
    let charset = ''
    let collate = ''
    while (idx < tokens.length) {
      const t = tokens[idx]
      const u = String(t || '').toUpperCase()
      if (u === 'NOT') {
        const next = tokens[idx + 1]?.toUpperCase?.() || ''
        if (next === 'NULL') {
          nullable = false
          idx += 2
          continue
        }
      }
      if (u === 'NULL') {
        nullable = true
        idx += 1
        continue
      }
      if (u === 'AUTO_INCREMENT') {
        autoIncrement = true
        idx += 1
        continue
      }
      if (u === 'PRIMARY') {
        const next = tokens[idx + 1]?.toUpperCase?.() || ''
        if (next === 'KEY') {
          primary = true
          idx += 2
          continue
        }
      }
      if (u === 'UNIQUE') {
        const next = tokens[idx + 1]?.toUpperCase?.() || ''
        if (next === 'KEY' || next === 'UNIQUE') {
          unique = true
          idx += next ? 2 : 1
          continue
        }
        unique = true
        idx += 1
        continue
      }
      if (u === 'DEFAULT') {
        let parts = []
        idx += 1
        let quoteDepth = 0
        let inS = false
        let inD = false
        let inB = false
        while (idx < tokens.length) {
          const piece = tokens[idx]
          for (let k = 0; k < piece.length; k += 1) {
            const cc = piece[k]
            const pp = piece[k - 1] || ''
            if (cc === "'" && pp !== '\\' && !inD && !inB) inS = !inS
            else if (cc === '"' && pp !== '\\' && !inS && !inB) inD = !inD
            else if (cc === '`' && !inS && !inD) inB = !inB
            if (!inS && !inD && !inB && cc === '(') quoteDepth += 1
            else if (!inS && !inD && !inB && cc === ')') quoteDepth -= 1
          }
          parts.push(piece)
          const nextToken = tokens[idx + 1]?.toUpperCase?.() || ''
          const stop = ['COMMENT', 'COLLATE', 'CHARACTER', 'SET', 'ON', 'AUTO_INCREMENT', 'PRIMARY', 'UNIQUE', 'KEY', 'UNSIGNED', 'ZEROFILL', 'NULL', 'NOT']
          if (!inS && !inD && !inB && quoteDepth <= 0 && stop.includes(nextToken)) {
            idx += 1
            break
          }
          if (!inS && !inD && !inB && quoteDepth <= 0 && idx + 1 >= tokens.length) {
            idx += 1
            break
          }
          idx += 1
        }
        defaultValue = parts.join(' ')
        continue
      }
      if (u === 'COMMENT') {
        idx += 1
        const parts = []
        while (idx < tokens.length) {
          parts.push(tokens[idx])
          idx += 1
        }
        comment = parts.join(' ').replace(/^'|'$/g, '').replace(/''/g, "'")
        continue
      }
      if (u === 'CHARACTER') {
        const next = tokens[idx + 1]?.toUpperCase?.() || ''
        if (next === 'SET') {
          charset = tokens[idx + 2] || ''
          idx += 3
          continue
        }
      }
      if (u === 'SET') {
        charset = tokens[idx + 1] || ''
        idx += 2
        continue
      }
      if (u === 'COLLATE') {
        collate = tokens[idx + 1] || ''
        idx += 2
        continue
      }
      if (/^(BTREE|HASH|RTREE|GIN|GIST|VISIBLE|INVISIBLE)$/i.test(String(t || ''))) {
        idx += 1
        continue
      }
      idx += 1
    }
    if (primary) {
      const existPk = indexes.find((x) => x.indexType === 'PRIMARY')
      if (existPk) {
        if (!existPk.columnList.some((x) => x.name === columnName)) {
          existPk.columnList.push({ name: columnName })
        }
        existPk.columnText = existPk.columnList.map((c) => c.name).join(', ')
      } else {
        indexes.push({
          indexName: 'PRIMARY',
          indexType: 'PRIMARY',
          indexMethod: 'BTREE',
          columnList: [{ name: columnName }],
          columnText: columnName,
          comment: '',
        })
      }
    }
    columns.push({
      column_name: columnName,
      column_type: type,
      column_nullable: nullable,
      column_primary: primary,
      column_unique: unique,
      column_auto_increment: autoIncrement,
      column_default: defaultValue,
      column_charset: charset,
      column_collate: collate,
      column_comment: comment,
    })
  })

  const tailInfo = tailOptions || ''
  if (tailInfo && columns.length) {
    const engineM = /ENGINE\s*=\s*([a-zA-Z0-9_]+)/i.exec(tailInfo)
    const charsetM = /(?:DEFAULT\s+)?CHARSET\s*=\s*([a-zA-Z0-9_]+)/i.exec(tailInfo)
    const collateM = /COLLATE\s*=\s*([a-zA-Z0-9_]+)/i.exec(tailInfo)
    if (engineM || charsetM || collateM) {
      columns.forEach((c) => {
        if (charsetM && !c.column_charset) c.column_charset = charsetM[1]
        if (collateM && !c.column_collate) c.column_collate = collateM[1]
        void engineM
      })
    }
  }
  return { columns, indexes }
}

const normalizeSummaryLabel = (rawKey) => {
  const raw = String(rawKey || '').trim()
  const map = [
    { pattern: /^LAST_VACUUM$/i, label: '上次VACUUM' },
    { pattern: /^LAST_AUTOVACUUM$/i, label: '上次自动VACUUM' },
    { pattern: /^LAST_ANALYZE$/i, label: '上次ANALYZE' },
    { pattern: /^LAST_AUTOANALYZE$/i, label: '上次自动ANALYZE' },
    { pattern: /^DEAD_ROWS$/i, label: '死元组' },
    { pattern: /^CHARACTER_SET_NAME$/i, label: '字符集' },
    { pattern: /^COLLATION_NAME$/i, label: 'COLLATION' },
    { pattern: /TABLE.*NAME|表名|NAME\s*\(.*TABLE.*\)/i, label: '表名' },
    { pattern: /TABLE.*COMMENT|表注释|COMMENT\s*\(.*TABLE.*\)|COMMENTS$/i, label: '表注释' },
    {
      pattern: /TABLE.*SCHEMA|TABLE.*CATALOG|所属库|^DATABASE$|^DATABASE_NAME$|数据库名|SCHEMA\s*\(.*TABLE.*\)/i,
      label: '所属库',
    },
    { pattern: /^SCHEMA_NAME$/i, label: 'Schema' },
    { pattern: /ENGINE|引擎/i, label: '引擎' },
    { pattern: /ROW.*FORMAT|行格式/i, label: '行格式' },
    { pattern: /(?:TABLE\s+)?COLLATION|排序规则|COLLATE/i, label: '排序规则' },
    { pattern: /CHARACTER\s*SET|^CHARSET$/i, label: '字符集' },
    { pattern: /TABLE\s+TYPE|表类型|^TYPE$/i, label: '表类型' },
    { pattern: /TEMPORARY/i, label: '临时表' },
    // 视图 / 存储过程 / 函数 / 触发器 / 事件 概览字段
    { pattern: /^(ROUTINE|TRIGGER|EVENT|VIEW|MVIEW|OBJECT)_SCHEMA$/i, label: '所属库' },
    { pattern: /^(ROUTINE|TRIGGER|EVENT|VIEW|MVIEW|OBJECT)_NAME$|^PRONAME$|^TGNAME$/i, label: '对象名' },
    { pattern: /^(ROUTINE|OBJECT)_TYPE$/i, label: '对象类型' },
    { pattern: /^DEFINER$/i, label: '定义者' },
    { pattern: /^OWNER$/i, label: '所有者' },
    { pattern: /^CREATED$|CREATE_DATE$/i, label: '创建时间' },
    { pattern: /^LAST_DDL_TIME$|LAST_ALTERED|MODIFY_DATE$/i, label: '最近修改' },
    { pattern: /^STATUS$|^STATE$/i, label: '状态' },
    { pattern: /^IS_UPDATABLE$/i, label: '可更新' },
    { pattern: /^CHECK_OPTION$/i, label: '检查选项' },
    { pattern: /^SECURITY_TYPE$|SECURITY_DEFINER/i, label: '安全模式' },
    { pattern: /^EVENT_TYPE$/i, label: '调度类型' },
    { pattern: /^INTERVAL_FIELD$/i, label: '间隔单位' },
    { pattern: /^INTERVAL_VALUE$/i, label: '间隔' },
    { pattern: /_EXECUTED$|^LAST$/i, label: '最近执行' },
    { pattern: /^ENDS?$/i, label: '结束时间' },
    { pattern: /^START$/i, label: '开始时间' },
    { pattern: /^ON_COMPLETION$/i, label: '完成后' },
    { pattern: /^ARGUMENTS$|IDENTITY_ARGUMENTS$/i, label: '参数' },
    { pattern: /^RETURNS$|FUNCTION_RESULT$/i, label: '返回类型' },
    { pattern: /^LANGUAGE$|^LANNAME$/i, label: '语言' },
    { pattern: /^TABLE_NAME$/i, label: '所属表' },
    { pattern: /^ACTION_TIMING$|^TIMING$/i, label: '触发时机' },
    { pattern: /^EVENT$/i, label: '触发事件' },
    { pattern: /^ACTION_STATEMENT$|^STATEMENT$/i, label: '执行语句' },
    { pattern: /^SQL_MODE$/i, label: 'SQL_MODE' },
    { pattern: /^COMMENT$/i, label: '注释' },
    { pattern: /^ROUTINE_COMMENT$/i, label: '注释' },
    { pattern: /^EVENT_COMMENT$/i, label: '注释' },
    { pattern: /^VIEW_DEFINITION$/i, label: '定义' },
    { pattern: /^TOTAL_ROWS$/i, label: '行数' },
    { pattern: /^TOTAL_BYTES$/i, label: '总大小' },
  ]
  const hit = map.find((x) => x.pattern.test(raw))
  return hit ? hit.label : raw
}

const BYTE_SIZE_LABELS = new Set([
  'avg_row_length',
  'data_length',
  'index_length',
  'data_free',
  'max_data_length',
])

const formatBytesForDisplay = (rawBytes, digits = 2) => {
  const n = Number(rawBytes)
  if (!Number.isFinite(n) || Number.isNaN(n)) return String(rawBytes ?? '')
  const b = Math.abs(n)
  if (b === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const idx = Math.min(units.length - 1, Math.floor(Math.log(b) / Math.log(1024)))
  const scaled = b / Math.pow(1024, idx)
  const fixed = scaled.toFixed(digits)
  const clean = fixed.includes('.')
    ? fixed.replace(/0+$/, '').replace(/\.$/, '')
    : fixed
  return `${n < 0 ? '-' : ''}${clean} ${units[idx]}`
}

const parseBaseToSummary = (rows) => {
  const seenKeys = new Set()
  const list = []
  ;(rows || []).forEach((row) => {
    if (!row || typeof row !== 'object') return
    Object.keys(row).forEach((rawKey) => {
      const label = normalizeSummaryLabel(rawKey)
      const key = label.toLowerCase()
      if (seenKeys.has(key)) return
      seenKeys.add(key)
      let value = row[rawKey]
      if (value === null || value === undefined) value = ''
      let valueStr = String(value).trim()
      if (BYTE_SIZE_LABELS.has(key) && valueStr !== '') {
        const fmt = formatBytesForDisplay(valueStr)
        if (fmt !== '') valueStr = fmt
      }
      list.push({
        key,
        label,
        value: valueStr,
      })
    })
  })
  const ordered = []
  const priorityKeys = [
    '所属库',
    'Schema',
    '表类型',
    '引擎',
    '字符集',
    'COLLATION',
    '排序规则',
    '行数',
    'ROWS',
    'TABLE_ROWS',
    '死元组',
    'DEAD_ROWS',
    '空闲空间',
    'DATA_FREE',
    '数据大小',
    'DATA_SIZE',
    '索引大小',
    'INDEX_SIZE',
    'AUTO_INCREMENT',
    'AVG_ROW_LENGTH',
    'DATA_LENGTH',
    'INDEX_LENGTH',
    'MAX_DATA_LENGTH',
    'CREATE_TIME',
    'UPDATE_TIME',
    'CHECK_TIME',
    'VERSION',
    '行格式',
    '上次VACUUM',
    '上次自动VACUUM',
    '上次ANALYZE',
    '上次自动ANALYZE',
    'TABLE_NAME',
    '表名',
    'TABLE_COMMENT',
    '表注释',
  ]
  priorityKeys.forEach((label) => {
    const it = list.find((x) => String(x.label || '').toLowerCase() === String(label || '').toLowerCase() && x.value)
    if (it && !ordered.includes(it)) ordered.push(it)
  })
  list.forEach((it) => {
    if (!ordered.includes(it) && it.value) ordered.push(it)
  })
  return ordered
}

const selectedTableDisplayName = computed(() => {
  const s = uiData.selectedTable || {}
  const tbl = String(s.table_name || s.table || s.name || '').trim()
  const db = String(s.database || s.database_name || s.catalog || '').trim()
  const dbType = String(uiData.selectedDbType || uiData.instanceData?.db_type || '').trim()
  let sch = String(s.schema_name || s.schema || '').trim()
  if (isPostgresFamilyDbType(dbType)) {
    const pure = sch.includes('.') ? extractPureSchemaFromLongPath(sch) : sch
    sch = pure
  }
  if (!tbl) return ''
  const path = [db, sch, tbl].filter(Boolean).join(' · ')
  return path || tbl
})

const OBJECT_TYPE_LABELS = {
  view: '视图',
  'materialized-view': '物化视图',
  procedure: '存储过程',
  function: '函数',
  trigger: '触发器',
  event: '事件',
}

const drawerTitleModeLabel = computed(() => {
  const objectType = String(uiState.drawerObjectType || 'table')
  if (objectType !== 'table' && OBJECT_TYPE_LABELS[objectType]) {
    return `${OBJECT_TYPE_LABELS[objectType]}信息`
  }
  return uiState.tableDrawerMode === 'structure' ? '表结构' : '表信息'
})

const resetTableDrawerData = () => {
  uiData.tableSummary = []
  uiData.tableColumns = []
  uiData.tableIndexes = []
  uiData.tableDdlText = ''
  uiData.tableInfo = ''
}

const refreshTableMeta = async (_mode = uiState.tableDrawerMode) => {
  if (!uiData.selectedTable) return
  const dbType = normalizeDbTypeValue(uiData.selectedDbType || uiData.instanceData.db_type || '')
  const isPg = isPostgresFamilyDbType(dbType)
  const selectedTable = normalizePgSelectedPayloadForApi({ ...uiData.selectedTable }, { dbType })
  if (isPg) {
    const needSyncToSelected = (
      String(uiData.selectedTable.schema || '') !== String(selectedTable.schema || '')
      || String(uiData.selectedTable.schema_name || '') !== String(selectedTable.schema_name || '')
      || String(uiData.selectedTable.database || '') !== String(selectedTable.database || '')
    )
    if (needSyncToSelected) {
      uiData.selectedTable = { ...uiData.selectedTable, ...selectedTable }
    }
  }
  uiState.loadingMeta = true
  try {
    const objectType = String(uiState.drawerObjectType || 'table')
    const isTableObject = objectType === 'table'
    const fetchBase = async () => {
      const baseParams = {
        ...selectedTable,
        type: 'base',
        object_type: objectType,
        instance_id: uiData.instanceData.instance_id,
      }
      const baseRes = await GetTableInfoApi(baseParams).catch(() => null)
      if (baseRes?.data?.length) {
        uiData.tableSummary = parseBaseToSummary(baseRes.data)
      }
    }
    const fetchStructure = async () => {
      const params = {
        ...selectedTable,
        type: 'structure',
        object_type: objectType,
        instance_id: uiData.instanceData.instance_id,
      }
      const res = await GetTableInfoApi(params).catch(() => null)
      let ddlText = ''
      if (res?.data) {
        res.data.forEach((row) => {
          for (const key in row) {
            if (key.toLowerCase() === 'create table' || key.toLowerCase() === 'statement') {
              ddlText = String(row[key] || '')
            }
          }
        })
      }
      // 列/索引解析只对表 DDL 有意义；视图/存储过程等对象直接展示 DDL
      if (isTableObject && ddlText) {
        const { columns, indexes } = parseDdlToColumnsAndIndexes(ddlText)
        uiData.tableColumns = columns
        uiData.tableIndexes = indexes
      } else {
        uiData.tableColumns = []
        uiData.tableIndexes = []
      }
      uiData.tableDdlText = ddlText
      uiData.tableInfo = ddlText
    }
    const tasks = [fetchStructure(), fetchBase()]
    await Promise.all(tasks)
  } catch {
    message.warning('获取表信息失败')
  } finally {
    uiState.loadingMeta = false
  }
}

const getTableMeta = async (type) => {
  uiState.tableDrawerMode = type === 'structure' ? 'structure' : 'base'
  uiState.drawerActiveTab = type === 'structure' ? 'columns' : 'summary'
  resetTableDrawerData()
  uiState.open = true
  await refreshTableMeta(uiState.tableDrawerMode)
}

const showTableStructure = () => {
  uiState.drawerObjectType = 'table'
  getTableMeta('structure')
}

const showTableMetadata = () => {
  uiState.drawerObjectType = 'table'
  getTableMeta('base')
}

const copyTextToClipboard = async (text) => {
  const safeText = String(text || '')
  if (!safeText) return false
  try {
    if (navigator?.clipboard && typeof navigator.clipboard.writeText === 'function') {
      await navigator.clipboard.writeText(safeText)
      return true
    }
  } catch {
  }
  try {
    const textarea = document.createElement('textarea')
    textarea.value = safeText
    textarea.setAttribute('readonly', 'readonly')
    textarea.style.position = 'fixed'
    textarea.style.top = '-2000px'
    textarea.style.left = '-2000px'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    textarea.setSelectionRange(0, textarea.value.length)
    const ok = document.execCommand('copy')
    document.body.removeChild(textarea)
    return Boolean(ok)
  } catch {
    return false
  }
}

const getDrawerSelectedText = () => {
  if (typeof window === 'undefined') return ''
  const drawer = tableMetaDrawerBodyRef.value
  const selection = window.getSelection?.()
  if (!selection || selection.rangeCount <= 0) return ''
  const range = selection.getRangeAt(0)
  if (drawer instanceof HTMLElement && drawer.contains(range.commonAncestorContainer)) {
    return String(selection.toString() || '')
  }
  return ''
}

const handleDrawerBodyKeydown = async (event) => {
  const key = String(event.key || '').toLowerCase()
  const isCopyShortcut = (event.metaKey || event.ctrlKey) && !event.altKey && key === 'c'
  if (!isCopyShortcut) return

  const selectedText = getDrawerSelectedText()
  if (selectedText) {
    return
  }

  const drawer = tableMetaDrawerBodyRef.value
  if (!(drawer instanceof HTMLElement)) return
  const textToCopy = String(uiData.tableInfo || '').trim()
  if (!textToCopy) return
  event.preventDefault()
  const ok = await copyTextToClipboard(textToCopy)
  if (ok) {
    message.success('已复制')
  } else {
    message.error('复制失败')
  }
}

const formatTableSize = (value) => {
  if (value === null || value === undefined || value === '') return '大小 -'
  const num = Number(value)
  if (Number.isNaN(num)) return `大小 ${value}`
  if (num >= 1024 * 1024) return `大小 ${(num / (1024 * 1024)).toFixed(1).replace(/\.0$/, '')} GB`
  if (num >= 1024) return `大小 ${(num / 1024).toFixed(1).replace(/\.0$/, '')} MB`
  return `大小 ${num} KB`
}

const leftHeight = ref('auto')
const resizeObserver = ref(null)
const isStackedConsoleLayout = () => typeof window !== 'undefined' && window.innerWidth <= 1200

const syncHeightWithRightPanel = () => {
  if (isStackedConsoleLayout()) {
    resizeObserver.value?.disconnect?.()
    leftHeight.value = 'auto'
    return
  }
  const rightPanel = document.querySelector('.right-content')
  if (rightPanel) {
    resizeObserver.value?.disconnect?.()
    resizeObserver.value = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const height = entry.contentRect.height
        leftHeight.value = `${height}px`
      }
    })
    resizeObserver.value.observe(rightPanel)
    const initialHeight = rightPanel.getBoundingClientRect().height
    if (initialHeight) {
      leftHeight.value = `${Math.floor(initialHeight)}px`
    }
  }
}

const copyDdlOnly = async () => {
  const text = String(uiData.tableDdlText || '').trim()
  if (!text) {
    message.warning('暂无可复制的 DDL')
    return
  }
  if (uiState.isCopyingTableInfo) return
  uiState.isCopyingTableInfo = true
  try {
    const ok = await copyTextToClipboard(text)
    if (ok) {
      message.success('DDL 已复制')
    } else {
      message.error('复制失败')
    }
  } catch {
    message.error('复制失败')
  } finally {
    uiState.isCopyingTableInfo = false
  }
}

const drawerResizeState = {
  startX: 0,
  startWidthPx: 0,
  onMove: null,
  onUp: null,
}

const clampDrawerWidth = (widthPx) => {
  const vw = typeof window !== 'undefined' ? window.innerWidth : 1280
  const minW = Math.max(480, vw * 0.3)
  const maxW = Math.max(minW + 40, vw * 0.9)
  const next = Math.min(Math.max(widthPx, minW), maxW)
  return Math.round(next)
}

const startDrawerResize = (e) => {
  if (typeof window === 'undefined') return
  const curr = typeof uiState.drawerWidth === 'number'
    ? uiState.drawerWidth
    : String(uiState.drawerWidth || '50%').trim()
  const currentWidthPx = (() => {
    if (typeof curr === 'number' && curr > 0) return curr
    if (/%$/.test(curr)) {
      const pct = Number(String(curr).slice(0, -1)) || 50
      return Math.round(window.innerWidth * Math.min(Math.max(pct, 30), 90) / 100)
    }
    const px = Number(String(curr).replace(/px$/i, ''))
    if (px > 0) return px
    return Math.round(window.innerWidth * 0.5)
  })()
  const startX = e?.clientX ?? 0
  drawerResizeState.startX = startX
  drawerResizeState.startWidthPx = clampDrawerWidth(currentWidthPx)

  const originalSelect = typeof document !== 'undefined' ? document.body.style.userSelect : ''
  const originalCursor = typeof document !== 'undefined' ? document.body.style.cursor : ''
  if (typeof document !== 'undefined') {
    document.body.style.userSelect = 'none'
    document.body.style.cursor = 'col-resize'
  }

  const onMove = (ev) => {
    const clientX = ev?.clientX ?? drawerResizeState.startX
    const delta = drawerResizeState.startX - clientX
    const nextPx = clampDrawerWidth(drawerResizeState.startWidthPx + delta)
    uiState.drawerWidth = nextPx
  }

  const onUp = () => {
    if (typeof window !== 'undefined') {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
    }
    if (typeof document !== 'undefined') {
      document.body.style.userSelect = originalSelect || ''
      document.body.style.cursor = originalCursor || ''
    }
    drawerResizeState.onMove = null
    drawerResizeState.onUp = null
    teardownRuntimeRefs.drawerResizeTeardown = null
  }

  teardownRuntimeRefs.drawerResizeTeardown = () => {
    try { onUp() } catch {}
  }

  drawerResizeState.onMove = onMove
  drawerResizeState.onUp = onUp
  window.addEventListener('mousemove', onMove)
  window.addEventListener('mouseup', onUp)
}

const handleWindowResize = () => {
  if (isStackedConsoleLayout()) {
    resizeObserver.value?.disconnect?.()
    leftHeight.value = 'auto'
    return
  }
  const rightPanel = document.querySelector('.right-content')
  if (rightPanel) {
    const nextHeight = rightPanel.getBoundingClientRect().height
    if (nextHeight) {
      leftHeight.value = `${Math.floor(nextHeight)}px`
    }
  }
  if (typeof uiState.drawerWidth === 'number') {
    uiState.drawerWidth = clampDrawerWidth(Number(uiState.drawerWidth))
  }
}

const destroyRuntime = () => {
  if (debounceTimerRef.value) {
    try { clearTimeout(debounceTimerRef.value) } catch {}
    debounceTimerRef.value = null
  }
  uiState?.refreshLock?.clear?.()
  if (resizeObserver.value) {
    try { resizeObserver.value.disconnect?.() } catch {}
    resizeObserver.value = null
  }
  try {
    ;[
      'searchModeWatchStop',
      'keywordWatchStop',
      'serverWatchStop',
      'expandedKeysWatchStop',
      'selectedInstanceWatchStop',
      'instanceCtxWatchStop',
      'skeletonWatchStop',
      'drawerSelectedTableWatchStop',
    ].forEach((k) => {
      const fn = teardownRuntimeRefs[k]
      if (typeof fn === 'function') {
        try { fn() } catch {}
      }
      teardownRuntimeRefs[k] = null
    })
    if (typeof teardownRuntimeRefs.drawerResizeTeardown === 'function') {
      try { teardownRuntimeRefs.drawerResizeTeardown() } catch {}
      teardownRuntimeRefs.drawerResizeTeardown = null
    }
    const cancelLocate = teardownRuntimeRefs.locateCancel || uiState?.locateCancel
    if (typeof cancelLocate === 'function') cancelLocate()
  } catch {}
  try {
    const instId = uiData?.selectedInstanceId
    if (instId) dasOS.invalidate(instId)
  } catch {}
  try {
    if (typeof window !== 'undefined') {
      window.removeEventListener('resize', handleWindowResize)
      window.removeEventListener('keydown', onGlobalKeydown, true)
    }
  } catch {}
}

onMounted(() => {
  // 2026-11-10：无实例上下文时主动预取实例列表。
  // 此前 fetchInstances 只被 doSyncTreeByContext 调用（要求共享上下文带 instance_id），
  // 当缓存 tab 未绑定实例（「未选择实例」）时上下文为空，左树永远不发起请求，
  // 空列表被渲染成「暂无授权数据源」——而授权其实没问题（实测工单）。
  // 预取后空上下文走第二条空态文案「选择数据源后，这里将展示数据库对象」，不再误导。
  if (!dasInstanceData?.value?.instance_id) {
    void fetchInstances()
  }
  syncTreeByContext(dasInstanceData.value || {})
  if (typeof window !== 'undefined') {
    window.addEventListener('resize', handleWindowResize)
    window.addEventListener('keydown', onGlobalKeydown, true)
  }
  setTimeout(() => {
    syncHeightWithRightPanel()
  }, 100)
})

onBeforeUnmount(() => {
  destroyRuntime()
})

teardownRuntimeRefs.expandedKeysWatchStop = watch(
  () => uiData.instanceResourceExpandedKeys,
  (val) => {
    if (!uiData.selectedInstanceId) return
    persistExpandedKeysForInstance(uiData.selectedInstanceId)
    void val
  },
)

teardownRuntimeRefs.selectedInstanceWatchStop = watch(
  () => uiData.selectedInstanceId,
  (instanceId, prev) => {
    if (!instanceId || instanceId === prev) return
    uiState.isCurrentInstanceRefreshing = false
    // 展开状态恢复挪进 doSyncTreeByContext：这里若直接 restore，会与随后的
    // 上下文展开（合并写 expandedKeys）竞争，谁后落地谁生效 ⇒ 偶发把库节点收起。
    const selected = (uiData.instanceResourceSelectedKeys || []).filter(Boolean)
    if (!selected.length) {
      // 实例根节点已不参与渲染，不再把 rootKey 设为选中（会选中一个不可见节点）。
      // 具体库/Schema 的选中由 syncTreeByContext 依据上下文恢复。
      uiData.instanceResourceSelectedKeys = []
    }
    handleClearSearch()
    dasOS.ensureIndex(instanceId, { force: false }).catch(() => {})
  },
)

teardownRuntimeRefs.instanceCtxWatchStop = watch(
  () => {
    const value = dasInstanceData.value || {}
    return [value.instance_id || '', value.db_type || '', value.database || '', value.schema || '', value.resource_type || ''].join('||')
  },
  async () => {
    await syncTreeByContext(dasInstanceData.value || {})
  },
)

teardownRuntimeRefs.sqlWorkspaceFocusedWatchStop = watch(
  () => dasConsoleAction?.nonce,
  () => {
    if (!dasConsoleAction?.type || !dasConsoleAction.nonce) return
    if (dasConsoleAction.type !== 'sql-workspace-focused') return
    if (!uiState.open) return
    uiState.open = false
  },
)

const _drawerGlobalClickHandler = (event) => {
  if (!uiState.open) return
  const target = (event && event.target) || null
  if (!target || typeof target.closest !== 'function') return
  if (target.closest('.ant-drawer, .ant-drawer-mask, .console-left-root')) return
  uiState.open = false
}

teardownRuntimeRefs.drawerGlobalClickBindStop = watch(
  () => uiState.open,
  (isOpen) => {
    if (typeof window === 'undefined' || typeof document === 'undefined') return
    if (isOpen) {
      document.addEventListener('click', _drawerGlobalClickHandler, { capture: true, passive: true })
      return
    }
    document.removeEventListener('click', _drawerGlobalClickHandler, { capture: true })
  },
  { immediate: true },
)

if (typeof window !== 'undefined') {
  const _cleanupDrawerGlobal = () => {
    try {
      document.removeEventListener('click', _drawerGlobalClickHandler, { capture: true })
    } catch (e) {
      /* ignore */
    }
  }
  if (typeof addEventListener === 'function') {
    addEventListener('beforeunload', _cleanupDrawerGlobal, { once: true })
  }
  teardownRuntimeRefs.drawerGlobalUnloadStop = _cleanupDrawerGlobal
}

teardownRuntimeRefs.drawerSelectedTableWatchStop = watch(
  () => {
    const s = uiData.selectedTable || {}
    return [
      String(uiState.open),
      String(s.instance_id || ''),
      String(s.database || s.database_name || ''),
      String(s.schema || s.schema_name || ''),
      String(s.table_name || s.table || s.name || ''),
    ].join('||')
  },
  async (curr, prev) => {
    if (curr === prev) return
    if (!uiState.open || !uiData.selectedTable) return
    const s = uiData.selectedTable
    const key = [s.instance_id || '', s.database || s.database_name || '', s.schema || s.schema_name || '', s.table_name || s.table || s.name || ''].join('||')
    const prevKey = (uiState._lastDrawerTableKey || '')
    if (key === prevKey) return
    uiState._lastDrawerTableKey = key
    resetTableDrawerData()
    await refreshTableMeta(uiState.tableDrawerMode)
  },
)
</script>

<style scoped>
.console-left-root {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
  height: 100%;
  overflow: hidden;
  box-sizing: border-box;
  background: transparent;
  border: 0;
  border-radius: 0;
  box-shadow: none;
  padding: 8px 10px 8px 10px;
}

.panel-tag {
  flex-shrink: 0;
  padding: 2px 8px;
  border-radius: 999px;
  background: var(--ant-colorFillAlter, #fafafa);
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  font-size: 12px;
}

.empty-placeholder-row {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-height: 22px;
  padding-left: 2px;
  cursor: default;
  user-select: none;
}
.empty-placeholder-icon {
  font-size: 12px;
  color: var(--ant-color-text-tertiary, #a5a5a5);
}
.empty-placeholder-text {
  font-size: 12px;
  color: var(--ant-color-text-tertiary, #a5a5a5);
  white-space: nowrap;
}

.tree-empty-tip {
  margin-top: 12px;
  padding: 0 4px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ant-color-text-secondary, rgba(0, 0, 0, 0.45));
}

/* 实例列表加载失败提示（区别于「暂无授权」的空态文案） */
.tree-error-tip {
  margin-top: 12px;
  padding: 10px 12px;
  border: 1px solid #ffccc7;
  border-radius: 6px;
  background: #fff2f0;
  font-size: 12px;
  line-height: 1.7;
}

.tree-error-text {
  color: #a8071a;
  word-break: break-all;
}

.tree-error-retry {
  display: inline-block;
  margin-top: 4px;
  color: var(--gi-color-primary, #0f766e);
  font-weight: 500;
  cursor: pointer;
}

.tree-error-retry:hover {
  text-decoration: underline;
}
:deep(.ant-tree-node-content-wrapper:has(.empty-placeholder-row)) {
  pointer-events: none;
}
:deep(.ant-tree-node-content-wrapper:has(.empty-placeholder-row)):hover {
  background: transparent !important;
}
/* 树展开/收起动画时长压到 0.001s：
   vc-tree 在列表动画期间（listChanging=true）会直接丢弃 expand 点击，
   用户在树刚变化后点箭头会被静默吞掉——「点了库但第二级不展开」的偶发来源之一。
   归零后动画窗口缩到一帧内；保留极小时长是为了 transitionend 正常触发，
   否则动画结束回调不执行会把 listChanging 永久卡在 true。 */
.tree-spin :deep(.ant-motion-collapse),
.tree-spin :deep(.ant-motion-collapse-legacy) {
  transition-duration: 0.001s !important;
  animation-duration: 0.001s !important;
}

.tree-title-row {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-height: 22px;
}

.tree-title-main {
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
  font-size: 13px;
  line-height: 20px;
}

/* 名称 + 体积：名称吸收剩余宽度、超出省略号截断；体积固定贴右，始终单行 */
.tree-node-name-flex {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tree-node-size {
  flex: 0 0 auto;
  margin-left: auto;
  padding-left: 6px;
  color: var(--ant-colorTextTertiary, rgba(0, 0, 0, 0.45));
  font-size: 12px;
  line-height: 20px;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.resource-node-meta {
  font-size: 11px;
  line-height: 18px;
  color: var(--ant-colorTextTertiary, rgba(0, 0, 0, 0.45));
}

/* 分组节点：文件夹图标 + 名称 */
.group-node-row {
  display: flex !important;
  align-items: center;
  width: 100%;
  min-width: 0;
  gap: 4px;
}

.group-node-label {
  font-weight: 500;
  letter-spacing: 0.2px;
}

.object-node-row {
  display: flex !important;
  align-items: center;
  width: 100%;
  min-width: 0;
  gap: 4px;
}

.object-node-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tree-node-icon-prefix {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  margin-right: 4px;
  font-size: 13px;
  line-height: 14px;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  vertical-align: middle;
  transition: color 0.2s ease;
}

.tree-node-icon-prefix.is-expanded,
:deep(.ant-tree-node-content-wrapper.ant-tree-node-selected) .tree-node-icon-prefix {
  color: #52c41a;
}

/* 分组（Columns / Indexes / Triggers…）文件夹图标：折叠淡灰，展开/选中取主题色 */
.group-node-icon {
  font-size: 14px;
  color: var(--ant-colorTextQuaternary, rgba(0, 0, 0, 0.25));
}

.group-node-icon.is-expanded,
:deep(.ant-tree-node-content-wrapper.ant-tree-node-selected) .group-node-icon {
  color: var(--ant-colorPrimary, #0f8a83);
}

.table-node-row {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
  padding: 1px 0;
}

.table-node-main {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 8px;
  min-width: 0;
}

.table-node-name {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  flex: 0 1 auto;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
}

.name-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.table-node-stats {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  min-width: 0;
  margin-left: auto;
}

.stats-item {
  font-size: 11px;
  line-height: 18px;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.55));
}

.table-comment-inline {
  display: inline-block;
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tree-area {
  flex: 1;
  min-height: 0;
  padding: 10px 10px 10px 10px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  box-sizing: border-box;
  background: var(--ant-colorBgContainer, #ffffff);
  border: 1px solid var(--console-border-color, #d9d9d9);
  border-radius: 8px;
  overflow: hidden;
}

/* 视图切换：分段控件（segmented）形态，轨道浅灰、选中段白底浮起 */
.left-view-switch {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 2px;
  background: var(--ant-colorFillQuaternary, rgba(0, 0, 0, 0.04));
  border-radius: 6px;
}

.left-view-switch button {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: none;
  background: transparent;
  color: #8c8c8c;
  font-size: 12px;
  line-height: 20px;
  padding: 1px 8px;
  border-radius: 4px;
  cursor: pointer;
  transition: color 0.2s, background 0.2s, box-shadow 0.2s;
}

.left-view-switch button:hover {
  color: #2f6fd6;
}

.left-view-switch button.is-active {
  color: #2f6fd6;
  background: var(--ant-colorBgContainer, #ffffff);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.1);
}

.schema-browser-host {
  flex: 1;
  min-height: 0;
}

.tree-toolbar {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  padding-top: 2px;
  padding-bottom: 6px;
  position: sticky;
  top: 0;
  z-index: 2;
  background: var(--ant-colorBgContainer, #ffffff);
}

.search-combo-prefix {
  flex: 0 0 auto;
  align-self: center;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  margin-right: 6px;
  color: var(--ant-colorTextPlaceholder, rgba(0, 0, 0, 0.35));
  font-size: 14px;
  pointer-events: none;
}

.search-combo.is-focused .search-combo-prefix {
  color: var(--ant-colorPrimary, #0f8a83);
}

.search-hit-badge {
  flex: 0 0 auto;
  align-self: center;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 28px;
  max-width: 64px;
  height: 20px;
  padding: 0 8px;
  margin-right: 4px;
  border-radius: 999px;
  font-size: 11px;
  line-height: 1;
  font-weight: 500;
  letter-spacing: 0.2px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  box-sizing: border-box;
}

.search-hit-badge.level-default {
  color: rgba(0, 0, 0, 0.55);
  background: rgba(0, 0, 0, 0.06);
}

.search-hit-badge.level-empty {
  color: #d4380d;
  background: rgba(255, 77, 79, 0.08);
}

.search-combo-clear {
  flex: 0 0 auto;
  align-self: center;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  margin-right: 2px;
  color: var(--ant-colorTextQuaternary, rgba(0, 0, 0, 0.25));
  font-size: 12px;
  cursor: pointer;
  border-radius: 50%;
  transition: color 0.15s ease, background 0.15s ease;
}

.search-combo-clear:hover {
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.45));
  background: rgba(0, 0, 0, 0.04);
}

.search-combo-divider {
  flex: 0 0 auto;
  align-self: stretch;
  width: 1px;
  margin: 6px 0;
  background: var(--ant-colorBorder, #d9d9d9);
}

.search-combo-icon-button {
  flex: 0 0 auto;
  align-self: stretch;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  padding: 0;
  border: none;
  outline: none;
  background: transparent;
  color: rgba(0, 0, 0, 0.55);
  font-size: 14px;
  line-height: 1;
  cursor: pointer;
  -webkit-appearance: none;
  appearance: none;
  transition: color 0.15s ease, background 0.15s ease;
}

.search-combo-icon-button:hover:not(:disabled) {
  color: var(--ant-colorPrimaryHover, #0b7770);
  background: rgba(15, 138, 131, 0.06);
}

.search-combo-icon-button:disabled {
  color: var(--ant-colorTextDisabled, rgba(0, 0, 0, 0.25));
  cursor: not-allowed;
  background: transparent;
}

.toolbar-menu-item {
  display: flex !important;
  align-items: center !important;
  justify-content: space-between !important;
  gap: 12px !important;
}

.toolbar-menu-item-title {
  flex: 1 1 auto;
  min-width: 0;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
  font-size: 13px;
  line-height: 1.4;
}

.toolbar-menu-item-hint {
  flex: 0 0 auto;
  color: var(--ant-colorTextTertiary, rgba(0, 0, 0, 0.45));
  font-size: 11px;
  line-height: 1;
}

.offline-alert {
  margin: 2px 0 2px;
}

.tree-spin {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.tree-area :deep(.ant-spin-nested-loading) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.tree-area :deep(.ant-spin-container) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.tree-search-input {
  flex: 1 1 auto;
  min-width: 0;
  display: inline-flex;
  align-items: center;
  height: 30px;
  box-sizing: border-box;
}

.search-combo {
  flex: 1 1 auto;
  min-width: 0;
  height: 34px;
  display: inline-flex;
  align-items: stretch;
  box-sizing: border-box;
  padding: 0 0 0 14px;
  border: 1px solid #c7d4e0;
  border-radius: 0;
  background: #ffffff;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
  overflow: hidden;
}

.search-combo:hover:not(.is-disabled) {
  border-color: var(--ant-colorPrimaryHover, #0b7770);
}

.search-combo:focus-within:not(.is-disabled) {
  border-color: var(--ant-colorPrimary, #0f8a83);
}

.search-combo.is-disabled {
  background: var(--ant-colorBgContainerDisabled, #f5f5f5);
  cursor: not-allowed;
}

.search-combo-input {
  flex: 1 1 auto;
  min-width: 0;
  height: 100%;
  padding: 0 6px 0 0;
  border: none;
  outline: none;
  background: transparent;
  font-size: 13px;
  line-height: 1;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
  box-sizing: border-box;
}

.search-combo-input::placeholder {
  color: var(--ant-colorTextPlaceholder, rgba(0, 0, 0, 0.35));
  font-size: 13px;
}

.search-combo-input:disabled {
  color: var(--ant-colorTextDisabled, rgba(0, 0, 0, 0.25));
  cursor: not-allowed;
}

.search-combo-clear {
  flex: 0 0 auto;
  align-self: center;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  margin-right: 4px;
  color: var(--ant-colorTextQuaternary, rgba(0, 0, 0, 0.25));
  font-size: 12px;
  cursor: pointer;
  border-radius: 50%;
  transition: color 0.15s ease, background 0.15s ease;
}

.search-combo-clear:hover {
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.45));
  background: rgba(0, 0, 0, 0.04);
}

.search-combo-divider {
  flex: 0 0 auto;
  align-self: stretch;
  width: 1px;
  margin: 6px 0;
  background: var(--ant-colorBorder, #d9d9d9);
}

.search-combo-icon-button {
  flex: 0 0 auto;
  align-self: stretch;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 42px;
  padding: 0;
  border: none;
  outline: none;
  background: transparent;
  color: rgba(0, 0, 0, 0.55);
  font-size: 15px;
  line-height: 1;
  cursor: pointer;
  -webkit-appearance: none;
  appearance: none;
  transition: color 0.15s ease, background 0.15s ease;
}

.search-combo-icon-button:hover:not(:disabled) {
  color: var(--ant-colorPrimaryHover, #0b7770);
  background: rgba(15, 138, 131, 0.06);
}

.search-combo-icon-button:disabled {
  color: var(--ant-colorTextDisabled, rgba(0, 0, 0, 0.25));
  cursor: not-allowed;
  background: transparent;
}

:deep(#tree-container) {
  flex: 1;
  min-height: 0;
  min-width: 0;
  width: 100%;
  height: 100%;
  max-height: none;
  display: flex;
  flex-direction: column;
  overflow-y: auto;
  overflow-x: hidden;
  border: 0;
  border-radius: 0;
  /* 库/Schema 一级节点左边缘与上方搜索框严格对齐（两者同处 .tree-area 的 10px 内边距内），
     内部层级由 antd indent 逐级缩进，形成统一对齐基线 */
  padding: 2px 0 4px 0;
  background: transparent;
  box-sizing: border-box;
  scrollbar-width: thin;
  scrollbar-color: #d9d9d9 transparent;
}

:deep(#tree-container:hover) {
  scrollbar-color: var(--ant-colorPrimary, #0f8a83) transparent;
}

:deep(#tree-container::-webkit-scrollbar) {
  width: 6px;
  height: 6px;
}

:deep(#tree-container::-webkit-scrollbar-track) {
  background: transparent;
}

:deep(#tree-container::-webkit-scrollbar-thumb) {
  background: #d9d9d9;
  border-radius: 3px;
  transition: background 0.2s ease, width 0.2s ease;
}

:deep(#tree-container:hover::-webkit-scrollbar-thumb) {
  background: var(--ant-colorPrimary, #0f8a83);
}

:deep(#tree-container:hover::-webkit-scrollbar) {
  width: 8px;
  height: 8px;
}

/* 树宽度锁定面板：此前是 width/max-content（整棵树随最宽行撑开），
   会导致名称被硬裁切、右侧体积被挤出可视区；改为 100% 后行宽受限，
   名称按省略号截断，体积始终贴右可见 */
:deep(.ant-tree) {
  width: 100%;
  min-width: 0;
}

:deep(.ant-tree-indent-unit) {
  width: 18px;
}

:deep(.ant-tree-treenode) {
  align-items: center;
}

:deep(.ant-tree.ant-tree-show-line li span.ant-tree-switcher) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 22px;
  line-height: 22px;
}

:deep(.ant-tree li) {
  padding: 1px 0;
}

:deep(.ant-tree-node-content-wrapper) {
  border-radius: 6px;
  white-space: nowrap;
  width: 100%;
  min-width: 100%;
  max-width: 100%;
  flex: 1 1 auto;
  padding: 2px 6px;
  box-sizing: border-box;
}

.tree-title-row--full-row,
.table-node-row--full-row {
  width: 100%;
  min-width: 100%;
  max-width: 100%;
  box-sizing: border-box;
}

.tree-title-row.tree-title-row--full-row {
  display: flex;
}

.table-node-row.table-node-row--full-row .table-node-main {
  width: 100%;
  min-width: 100%;
  max-width: 100%;
  box-sizing: border-box;
}

:deep(.ant-tree-node-content-wrapper:hover) {
  background: rgba(15, 138, 131, 0.06);
}

:deep(.ant-tree-node-content-wrapper.ant-tree-node-selected) {
  background: rgba(15, 138, 131, 0.10);
  box-shadow: inset 0 0 0 1px rgba(15, 138, 131, 0.20);
}

:deep(.ant-tree-node-content-wrapper.ant-tree-node-selected:hover) {
  background: rgba(15, 138, 131, 0.14);
}

:deep(.ant-tree-node-content-wrapper.ant-tree-node-selected .tree-title-main),
:deep(.ant-tree-node-content-wrapper.ant-tree-node-selected .table-node-name),
:deep(.ant-tree-node-content-wrapper.ant-tree-node-selected .name-text) {
  color: rgba(0, 0, 0, 0.82);
  font-weight: 500;
}

:deep(.ant-tree-node-content-wrapper.ant-tree-node-selected .resource-node-meta),
:deep(.ant-tree-node-content-wrapper.ant-tree-node-selected .stats-item) {
  color: rgba(0, 0, 0, 0.55);
}

/* 展开三角与节点内容的间距减半（antd 默认 switcher 宽 24px），
   让库/Schema 名称整体靠左 */
:deep(.ant-tree .ant-tree-switcher) {
  width: 12px;
  min-width: 12px;
}

.column-node-row {
  display: inline-flex;
  flex-wrap: nowrap;
  align-items: center;
  gap: 6px;
  min-height: 22px;
  max-width: 100%;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.column-node-name {
  flex: 0 0 auto;
  min-width: 0;
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
  font-size: 13px;
  line-height: 20px;
  font-weight: 500;
  letter-spacing: 0.1px;
}

/* 主键列：钥匙图标（联合主键的每一列都会命中） */
.column-node-icon.is-pk,
.column-node-icon.is-pk.is-expanded {
  color: #faad14;
}

/* 行内次要信息：列类型、索引列清单 */
.tree-node-subtext {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--ant-colorTextQuaternary, rgba(0, 0, 0, 0.35));
  font-size: 12px;
  line-height: 20px;
  font-weight: 400;
}

.column-node-type {
  margin-left: -2px;
}

.index-node-columns {
  margin-left: 6px;
}

:deep(.ant-tree-node-content-wrapper:hover) .column-node-name {
  color: var(--ant-colorText, rgba(0, 0, 0, 0.92));
}

.tree-hit {
  color: var(--ant-colorPrimary, #0f8a83);
  font-weight: 500;
  background: rgba(15, 138, 131, 0.08);
  padding: 0 1px;
  border-radius: 2px;
}

:deep(.ant-tree-title) {
  white-space: nowrap;
  display: flex;
  width: 100%;
  min-width: 0;
  max-width: 100%;
  align-items: center;
}

/* 约束虚拟列表宽度链路：行宽锁定为面板宽度，
   名称超宽时才能按省略号截断，而不是被硬裁切、把右侧体积挤出可视区。
   注意锚点必须是模板内真实带 scope 属性的 #tree-container ——
   .filter-tree 挂在 a-tree 组件根元素上，没有 data-v 属性，选择器永远不会命中 */
#tree-container :deep(.ant-tree-list-holder),
#tree-container :deep(.ant-tree-list-holder-inner) {
  width: 100%;
  min-width: 0;
}

#tree-container :deep(.ant-tree-treenode) {
  width: 100%;
  min-width: 0;
  max-width: 100%;
}

#tree-container :deep(.ant-tree-node-content-wrapper) {
  width: 100%;
  min-width: 0;
  max-width: 100%;
  white-space: nowrap;
  overflow: hidden;
}

@media (max-width: 768px) {
  .console-left-root {
    height: auto !important;
    padding: 8px !important;
  }

  .tree-area {
    padding: 8px;
  }

  .table-node-main {
    flex-direction: column;
    align-items: flex-start;
  }

  .table-node-stats {
    flex-wrap: wrap;
    margin-left: 0;
  }
}

.server-search-panel {
  flex: 0 0 auto;
  max-height: 220px;
  min-height: 80px;
  margin: 0 10px 10px 10px;
  background: var(--ant-colorBgContainer, #ffffff);
  border: 1px solid var(--ant-colorBorderSecondary, #f0f0f0);
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 1px 2px rgba(15, 138, 131, 0.04);
}

.server-search-panel-header {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 10px;
  border-bottom: 1px solid var(--ant-colorBorderSecondary, #f0f0f0);
  background: var(--ant-colorFillQuaternary, rgba(15, 138, 131, 0.04));
}

.server-search-panel-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
}

.server-search-panel-spin {
  min-height: 60px;
}

.server-search-list {
  flex: 1 1 auto;
  min-height: 60px;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 2px 0;
}

.server-search-item {
  display: flex;
  flex-direction: row;
  align-items: flex-start;
  justify-content: space-between;
  padding: 6px 10px;
  cursor: pointer;
  transition: background-color 120ms ease;
  border-bottom: 1px dashed var(--ant-colorBorderSecondary, #f0f0f0);
  gap: 8px;
}

.server-search-item:last-child {
  border-bottom: none;
}

.server-search-item:hover {
  background: var(--ant-colorPrimaryBg, rgba(15, 138, 131, 0.06));
}

.server-search-item.is-locating {
  opacity: 0.6;
  cursor: progress;
}

.server-search-item-left {
  display: flex;
  flex: 1 1 auto;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.server-search-item-right {
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  max-width: 45%;
  min-width: 0;
  gap: 2px;
}

.ss-item-type {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 42px;
  padding: 1px 6px;
  border-radius: 3px;
  font-size: 11px;
  line-height: 1.4;
  font-weight: 600;
  border: 1px solid transparent;
}

.ss-item-type--database {
  color: #1677ff;
  background: rgba(22, 119, 255, 0.08);
  border-color: rgba(22, 119, 255, 0.18);
}
.ss-item-type--schema {
  color: #722ed1;
  background: rgba(114, 46, 209, 0.08);
  border-color: rgba(114, 46, 209, 0.18);
}
.ss-item-type--table {
  color: var(--ant-colorPrimary, #0f8a83);
  background: var(--ant-colorPrimaryBg, rgba(15, 138, 131, 0.06));
  border-color: rgba(15, 138, 131, 0.2);
}

.ss-item-title {
  flex: 1 1 auto;
  min-width: 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
  word-break: break-all;
  font-weight: 500;
}

.ss-item-path {
  display: inline-block;
  max-width: 100%;
  font-size: 11px;
  line-height: 1.4;
  color: var(--ant-colorTextTertiary, rgba(0, 0, 0, 0.45));
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  direction: rtl;
  text-align: left;
  font-variant-numeric: tabular-nums;
}

.ss-item-comment {
  display: inline-block;
  max-width: 100%;
  font-size: 11px;
  line-height: 1.4;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

:deep(.server-search-panel .ant-empty) {
  padding: 16px 0;
  margin: 0;
}

:deep(.server-search-panel .ant-empty-image) {
  height: 40px;
  margin-bottom: 6px;
}

:deep(.server-search-panel .ant-empty-description) {
  font-size: 12px;
  color: var(--ant-colorTextTertiary, rgba(0, 0, 0, 0.45));
}

.drawer-meta-copy-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  padding: 0;
  margin: 0;
  background-color: transparent;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  border: 1px solid transparent;
  border-radius: 6px;
  cursor: pointer;
  transition:
    background-color 0.16s ease,
    color 0.16s ease,
    border-color 0.16s ease;
  outline: none;
}

.drawer-meta-copy-button:hover:not(:disabled),
.drawer-meta-refresh-button:hover:not(:disabled) {
  background-color: var(--ant-colorPrimaryBg, rgba(22, 119, 255, 0.1));
  color: var(--ant-colorPrimary, #1677ff);
  border-color: var(--ant-colorBorder, rgba(5, 5, 5, 0.06));
}

.drawer-meta-copy-button:active:not(:disabled),
.drawer-meta-refresh-button:active:not(:disabled) {
  background-color: var(--ant-colorPrimaryBgHover, rgba(22, 119, 255, 0.2));
  color: var(--ant-colorPrimaryActive, #0958d9);
}

.drawer-meta-copy-button:focus-visible,
.drawer-meta-refresh-button:focus-visible {
  border-color: var(--ant-colorPrimary, #1677ff);
  box-shadow: 0 0 0 2px rgba(22, 119, 255, 0.15);
}

.drawer-meta-copy-button:disabled,
.drawer-meta-refresh-button:disabled {
  cursor: not-allowed;
  color: var(--ant-colorTextQuaternary, rgba(0, 0, 0, 0.25));
  background-color: transparent;
}

.drawer-meta-refresh-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  padding: 0;
  margin: 0;
  background-color: transparent;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  border: 1px solid transparent;
  border-radius: 6px;
  cursor: pointer;
  transition:
    background-color 0.16s ease,
    color 0.16s ease,
    border-color 0.16s ease;
  outline: none;
}

.drawer-meta-extra-bar {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.table-meta-drawer-body {
  width: 100%;
  min-height: 360px;
  outline: none;
  padding-right: 4px;
}

.table-meta-drawer-body:focus-visible {
  outline: none;
}

.drawer-meta-spin-root {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  height: 0;
  min-height: 0;
  min-width: 0;
  width: 100%;
  overflow: hidden;
}

.drawer-meta-spin {
  min-height: 0;
  height: 100%;
  width: 100%;
}

.drawer-meta-spin > * {
  min-height: 0;
  min-width: 0;
}

:deep(.drawer-meta-spin .ant-spin-container) {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  min-width: 0;
  overflow: hidden;
}

.drawer-meta-section {
  margin-bottom: 12px;
}

.drawer-meta-section:last-child {
  margin-bottom: 0;
}

.drawer-meta-section-title > span:first-of-type {
  letter-spacing: 0.1px;
}

.drawer-meta-section-title-inner > span:first-of-type {
  letter-spacing: 0.1px;
}

.drawer-meta-count-pill {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 16px;
  padding: 0 5px;
  font-size: 10.5px;
  font-weight: 500;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  background-color: var(--ant-colorFillTertiary, rgba(0, 0, 0, 0.08));
  border-radius: 10px;
  line-height: 16px;
  letter-spacing: 0.2px;
}

.table-meta-drawer-body {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  width: 100%;
  height: 0;
  min-height: 0;
  overflow: hidden;
}

.table-meta-drawer-body > * {
  min-width: 0;
}

.drawer-meta-section--summary {
  flex: 0 0 auto;
}

.drawer-meta-section--flex {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-height: 0;
  margin-bottom: 0;
}

.drawer-meta-ddl-scroll-wrap {
  flex: 1 1 auto;
  min-height: 0;
  overflow-x: auto;
  overflow-y: auto;
  border-radius: 8px;
  border: 1px solid var(--ant-colorBorderSecondary, rgba(5, 5, 5, 0.08));
}

.drawer-meta-summary-grid {
  display: grid;
  grid-template-columns: auto 1fr auto 1fr;
  gap: 4px 12px;
  max-height: none;
  overflow: hidden;
  padding: 10px 12px;
  background-color: var(--ant-colorFillSecondary, rgba(0, 0, 0, 0.04));
  border-radius: 0;
  border: 1px solid var(--ant-colorBorderSecondary, rgba(5, 5, 5, 0.08));
}

.drawer-meta-summary-item {
  display: contents;
}

.drawer-meta-summary-item > * {
  min-width: 0;
}

.drawer-meta-summary-label {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 0;
  height: 26px;
  line-height: 26px;
  padding-right: 4px;
  min-width: 0;
  flex: 0 0 auto;
  white-space: nowrap;
}

.drawer-meta-summary-label-text {
  flex: 0 0 auto;
  min-width: 0;
  max-width: none;
  font-size: 12px;
  font-weight: 500;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  text-align: right;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.drawer-meta-summary-label-colon {
  flex: 0 0 auto;
  font-size: 12px;
  font-weight: 500;
  color: var(--ant-colorTextTertiary, rgba(0, 0, 0, 0.45));
  line-height: 26px;
  white-space: nowrap;
  margin-left: 1px;
}

.drawer-meta-summary-value {
  display: inline-flex;
  align-items: center;
  height: 26px;
  font-size: 12.5px;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 100%;
}

.drawer-meta-comment {
  color: var(--ant-colorTextTertiary, rgba(0, 0, 0, 0.45));
  font-size: 12px;
  line-height: 1.6;
}

.drawer-meta-tertiary {
  color: var(--ant-colorTextTertiary, rgba(0, 0, 0, 0.45));
}

.drawer-meta-mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace;
  font-size: 11.5px;
  letter-spacing: 0.1px;
}

.drawer-meta-copy-button--inline {
  width: 24px;
  height: 24px;
  border-radius: 5px;
  font-size: 12.5px;
}

.drawer-meta-section-title {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  margin-bottom: 8px;
  font-size: 12.5px;
  font-weight: 500;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
}

.drawer-meta-section-title--with-action {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  height: 24px;
  margin-bottom: 8px;
}

.drawer-meta-section-title-inner {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  font-size: 12.5px;
  font-weight: 500;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
}

.drawer-resize-handle {
  position: absolute;
  top: 0;
  left: -1px;
  width: 4px;
  height: 100%;
  cursor: col-resize;
  background-color: transparent;
  z-index: 20;
  transition: background-color 0.16s ease, left 0.16s ease;
}

.drawer-resize-handle::after {
  content: '';
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  width: 2px;
  height: 44px;
  border-radius: 2px;
  background-color: var(--ant-colorBorder, rgba(5, 5, 5, 0.12));
  opacity: 0.85;
}

.drawer-resize-handle:hover {
  background-color: rgba(22, 119, 255, 0.08);
}

.drawer-resize-handle:hover::after {
  background-color: var(--ant-colorPrimary, #1677ff);
  opacity: 1;
  width: 3px;
}

.table-meta-drawer-body :deep(.ant-empty) {
  margin: 24px 0;
}

.drawer-meta-ddl-scroll-wrap :deep(.hljs) {
  padding: 14px 16px;
  margin: 0;
  min-height: 100%;
  border-radius: 0;
  background-color: #0f172a;
  color: #e2e8f0;
  font-size: 12px;
  line-height: 1.6;
}

.drawer-meta-ddl-block {
  margin: 0;
  display: block;
  width: 100%;
  height: 100%;
}

@media (max-width: 768px) {
  /* 概览信息 4 列网格在窄屏每列只剩 ~90px */
  .drawer-meta-summary-grid {
    grid-template-columns: auto 1fr;
    gap: 4px 8px;
    padding: 8px 10px;
  }

  .drawer-meta-summary-value {
    font-size: 12px;
  }

  .search-combo {
    height: 32px;
    padding-left: 10px;
  }

  .search-combo-icon-button {
    width: 36px;
  }

  .search-hit-badge {
    display: none;
  }

  /* 抽屉宽度拖拽把手只绑了 mousedown，触屏是纯装饰且挡住左边缘 */
  .drawer-resize-handle {
    display: none;
  }

  .server-search-panel {
    margin: 0 8px 8px;
    max-height: 40vh;
  }

  .server-search-item {
    padding: 8px;
    gap: 6px;
  }

  .drawer-meta-ddl-scroll-wrap :deep(.hljs) {
    padding: 10px;
    font-size: 11px;
  }
}
</style>
