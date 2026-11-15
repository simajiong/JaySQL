<template>
  <div class="gi-page-shell datasec-page">
    <div class="gi-page-toolbar gi-toolbar-single-line">
      <div class="gi-page-title">数据安全</div>
      <div class="gi-toolbar-actions">
        <a-button v-if="activeTab === 'rules' && canManage" type="primary" @click="openRuleModal()">
          <PlusOutlined />新增规则
        </a-button>
        <template v-if="activeTab === 'columns' && canManage">
          <a-button :loading="scanLoading" @click="openScanModal">自动发现</a-button>
          <a-button type="primary" @click="openColumnModal()"><PlusOutlined />绑定列</a-button>
        </template>
        <a-button v-if="activeTab === 'grants'" type="primary" @click="openGrantModal">
          <PlusOutlined />申请明文访问
        </a-button>
      </div>
    </div>

    <a-card>
      <a-tabs v-model:activeKey="activeTab" @tab-click="handleTabSwitch">
        <!-- 库表列配置的筛选栏上移，与 tab 标题同行（v-show 保持组件状态不丢） -->
        <template #rightExtra>
          <div v-show="activeTab === 'columns'" class="columns-filter-bar">
            <a-select
              v-model:value="columnFilter.instance_id"
              show-search
              option-filter-prop="label"
              placeholder="选择实例"
              class="filter-select filter-instance"
              :options="instanceOptions"
              @change="handleInstanceChange"
            />
            <a-select
              v-model:value="columnFilter.status"
              allow-clear
              placeholder="全部状态"
              class="filter-select filter-status"
              :options="[
                { label: '已生效', value: 'enabled' },
                { label: '待确认建议', value: 'suggested' },
              ]"
              @change="loadColumns"
            />
          </div>
        </template>
        <!-- ==================== Tab 1：脱敏规则 ==================== -->
        <a-tab-pane key="rules" tab="脱敏规则">
          <a-table
            size="small"
            row-key="id"
            :columns="ruleColumns"
            :data-source="ruleList"
            :loading="ruleLoading"
            :pagination="false"
            :scroll="{ x: 'max-content' }"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'data_type'">
                {{ dataTypeLabel(record.data_type) }}
              </template>
              <template v-else-if="column.key === 'algorithm'">
                {{ algorithmLabel(record.algorithm, record.params) }}
              </template>
              <template v-else-if="column.key === 'mask_preview'">
                <span class="mono">{{ previewOf(record) }}</span>
              </template>
              <template v-else-if="column.key === 'enabled'">
                {{ record.enabled === 1 ? '启用' : '停用' }}
              </template>
              <template v-else-if="column.key === 'built_in'">
                {{ record.built_in === 1 ? '内置' : '自定义' }}
              </template>
              <template v-else-if="column.key === 'remark'">
                {{ remarkOf(record) }}
              </template>
              <template v-else-if="column.key === 'action'">
                <a-space v-if="canManage">
                  <a class="action-link" @click="openRuleModal(record)">编辑</a>
                  <a-popconfirm
                    :title="record.built_in === 1 ? '内置规则不可删除，确认停用？' : '确认删除该规则？'"
                    @confirm="removeRule(record)"
                  >
                    <a class="action-link">{{ record.built_in === 1 ? '停用' : '删除' }}</a>
                  </a-popconfirm>
                </a-space>
                <span v-else class="text-muted">-</span>
              </template>
            </template>
          </a-table>
        </a-tab-pane>

        <!-- ==================== Tab 2：列配置 ==================== -->
        <a-tab-pane key="columns" tab="库表列配置">
          <a-table
            size="small"
            row-key="id"
            :columns="columnColumns"
            :data-source="columnList"
            :loading="columnLoading"
            :pagination="false"
            :scroll="{ x: 'max-content' }"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'instance_id'">
                <div class="instance-cell">
                  <div class="instance-id-row">
                    <a class="instance-id-text" @click="goInstance(record.instance_id)">{{ record.instance_id }}</a>
                    <a-tooltip title="复制实例 ID">
                      <a-button
                        type="text"
                        size="small"
                        class="copy-id-btn"
                        @click.stop="copyInstanceId(record.instance_id)"
                      >
                        <CopyOutlined />
                      </a-button>
                    </a-tooltip>
                  </div>
                  <a
                    v-if="instanceNameOf(record.instance_id)"
                    class="instance-name"
                    @click="goInstance(record.instance_id)"
                  >
                    {{ instanceNameOf(record.instance_id) }}
                  </a>
                </div>
              </template>
              <template v-else-if="column.key === 'column_path'">
                <span class="mono">{{ record.schema_name || '-' }}.{{ record.table_name || '-' }}.{{ record.column_name }}</span>
              </template>
              <template v-else-if="column.key === 'rule_name'">
                {{ ruleNameOf(record.rule_id) }}
              </template>
              <template v-else-if="column.key === 'status'">
                {{ columnStatusText(record.status) }}
              </template>
              <template v-else-if="column.key === 'confidence'">
                {{ record.confidence }}%
              </template>
              <template v-else-if="column.key === 'source'">
                {{ record.source === 'scan' ? '自动发现' : '手工' }}
              </template>
              <template v-else-if="column.key === 'action'">
                <a-space v-if="canManage">
                  <a v-if="record.status !== 'enabled'" class="action-link" @click="changeColumnStatus(record, 'enabled')">启用</a>
                  <a v-else class="action-link" @click="changeColumnStatus(record, 'disabled')">停用</a>
                  <a-popconfirm title="确认删除该列配置？" @confirm="removeColumn(record)">
                    <a class="action-link">删除</a>
                  </a-popconfirm>
                </a-space>
                <span v-else class="text-muted">-</span>
              </template>
            </template>
          </a-table>
        </a-tab-pane>

        <!-- ==================== Tab 3：明文工单 ==================== -->
        <a-tab-pane key="grants" tab="明文工单">
          <div class="filter-bar">
            <a-radio-group v-model:value="grantFilter.scope" @change="loadGrants">
              <a-radio-button value="mine">我的申请</a-radio-button>
              <a-radio-button v-if="canApprove" value="all">全部记录</a-radio-button>
            </a-radio-group>
            <a-select
              v-model:value="grantFilter.status"
              placeholder="状态"
              style="width: 140px"
              :options="[
                { label: '全部状态', value: '' },
                { label: '待审批', value: 'pending' },
                { label: '已生效', value: 'approved' },
                { label: '已驳回', value: 'rejected' },
                { label: '已过期', value: 'expired' },
                { label: '已收回', value: 'revoked' },
              ]"
              @change="loadGrants"
            />
          </div>
          <a-table
            size="small"
            row-key="id"
            :columns="grantColumns"
            :data-source="grantList"
            :loading="grantLoading"
            :pagination="false"
            :scroll="{ x: 'max-content' }"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'grant_no'">
                <span class="mono">{{ record.grant_no }}</span>
              </template>
              <template v-else-if="column.key === 'scope'">
                <span class="mono">{{ grantScopeText(record) }}</span>
              </template>
              <template v-else-if="column.key === 'status'">
                <a-tag :color="grantStatusColor(record.status)">{{ grantStatusLabel(record) }}</a-tag>
              </template>
              <template v-else-if="column.key === 'expires_at'">
                {{ record.expires_at || '-' }}
              </template>
              <template v-else-if="column.key === 'action'">
                <a-space>
                  <template v-if="canApprove && record.status === 'pending' && record.username !== username">
                    <a class="action-link" @click="decideGrant(record, 'approve')">批准</a>
                    <a class="action-danger" @click="decideGrant(record, 'reject')">驳回</a>
                  </template>
                  <a-popconfirm
                    v-if="canApprove && record.status === 'approved'"
                    title="确认收回该明文授权？收回后立即按脱敏返回。"
                    @confirm="decideGrant(record, 'revoke')"
                  >
                    <a class="action-danger">收回</a>
                  </a-popconfirm>
                </a-space>
              </template>
            </template>
          </a-table>
        </a-tab-pane>
      </a-tabs>
    </a-card>

    <!-- 规则编辑弹窗 -->
    <a-modal v-model:open="ruleModal.open" :title="ruleModal.form.id ? '编辑规则' : '新增规则'" @ok="submitRule">
      <a-form layout="vertical">
        <a-form-item label="规则名称" required>
          <a-input v-model:value="ruleModal.form.name" :maxlength="64" placeholder="例如：手机号-保留前3后4" />
        </a-form-item>
        <a-form-item label="敏感类型" required>
          <a-select v-model:value="ruleModal.form.data_type" :options="dataTypeOptions" />
        </a-form-item>
        <a-form-item label="脱敏算法" required>
          <a-select v-model:value="ruleModal.form.algorithm" :options="algorithmOptions" />
        </a-form-item>
        <a-form-item v-if="needsParams" label="保留位数">
          <a-space>
            <span>保留前</span>
            <a-input-number v-model:value="keepHead" :min="0" :max="20" style="width: 80px" />
            <span>位 + 后</span>
            <a-input-number v-model:value="keepTail" :min="0" :max="20" style="width: 80px" />
            <span>位</span>
          </a-space>
        </a-form-item>
        <a-form-item label="实时预览">
          <div class="preview-line">
            <span class="mono preview-input">13812345678 → </span>
            <span class="mono preview-output">{{ rulePreview }}</span>
          </div>
        </a-form-item>
        <a-form-item label="备注">
          <a-input v-model:value="ruleModal.form.remark" :maxlength="255" />
        </a-form-item>
        <a-form-item label="启用">
          <a-switch v-model:checked="ruleModal.form.enabled" />
        </a-form-item>
      </a-form>
    </a-modal>

    <!-- 绑定列弹窗 -->
    <a-modal v-model:open="columnModal.open" title="绑定脱敏规则到列" @ok="submitColumn">
      <a-form layout="vertical">
        <a-form-item label="实例" required>
          <a-select
            v-model:value="columnModal.form.instance_id"
            show-search option-filter-prop="label"
            :options="instanceOptions"
            @change="handleColumnInstanceChange"
          />
        </a-form-item>
        <a-form-item label="库 / Schema" required>
          <a-select
            v-model:value="columnModal.schema"
            show-search option-filter-prop="label"
            :loading="modalSchemaLoading"
            :options="modalSchemaOptions"
            @change="handleModalSchemaChange"
          />
        </a-form-item>
        <a-form-item label="表" required>
          <a-select
            v-model:value="columnModal.table"
            show-search option-filter-prop="label"
            :loading="modalTableLoading"
            :options="modalTableOptions"
            @change="handleModalTableChange"
          />
        </a-form-item>
        <a-form-item label="列" required>
          <a-select
            v-model:value="columnModal.column"
            show-search option-filter-prop="label"
            :loading="modalColumnLoading"
            :options="modalColumnOptions"
          />
        </a-form-item>
        <a-form-item label="脱敏规则" required>
          <a-select
            v-model:value="columnModal.form.rule_id"
            :options="enabledRuleOptions"
            placeholder="选择规则"
          />
        </a-form-item>
      </a-form>
    </a-modal>

    <!-- 自动发现弹窗 -->
    <a-modal v-model:open="scanModal.open" title="敏感列自动发现" @ok="submitScan" ok-text="开始扫描">
      <a-form layout="vertical">
        <a-form-item label="实例" required>
          <a-select v-model:value="scanModal.form.instance_id" :options="instanceOptions" @change="handleScanInstanceChange" />
        </a-form-item>
        <a-form-item label="库 / Schema" required>
          <a-select
            v-model:value="scanModal.form.schema_name"
            show-search option-filter-prop="label"
            :loading="scanSchemaLoading"
            :options="scanSchemaOptions"
            @change="handleScanSchemaChange"
          />
        </a-form-item>
        <a-form-item label="表名（多个用英文逗号分隔）" required>
          <a-select
            v-model:value="scanModal.tableSelection"
            mode="tags"
            :open="false"
            :token-separators="[',', ' ']"
            placeholder="输入表名后回车，或从下方选择"
          />
          <div class="table-pick-tools">
            <a-space :size="8" wrap>
              <a-button size="small" :disabled="!scanTableNames.length" @click="selectAllScanTables">全选</a-button>
              <a-button size="small" :disabled="!scanTableNames.length" @click="invertScanTables">反选</a-button>
              <a-input-search
                v-model:value="scanModal.pattern"
                size="small"
                allow-clear
                :disabled="!scanTableNames.length"
                placeholder="通配：t_user* / *_log / order_?"
                enter-button="匹配"
                style="width: 250px"
                @search="applyScanPattern"
              />
              <a-button v-if="scanModal.tableSelection.length" size="small" type="link" class="action-link" @click="clearScanTables">
                清空
              </a-button>
            </a-space>
            <span :class="['pick-count', scanModal.tableSelection.length > 50 ? 'pick-count-warn' : 'text-muted']">
              已选 {{ scanModal.tableSelection.length }} / {{ scanTableNames.length }}
            </span>
          </div>
          <a-select
            v-model:value="scanModal.tableSelection"
            mode="multiple"
            show-search option-filter-prop="label"
            :loading="scanTableLoading"
            :options="scanTableOptions"
            placeholder="也可从下拉中勾选"
            style="margin-top: 8px"
          />
        </a-form-item>
        <div class="scan-note">
          抽样上限 500 行 / 表，只读文本列，避开大表高峰；建议结果需人工确认后生效。
        </div>
        <div v-if="scanResult.length" class="scan-result">
          <div v-for="tableResult in scanResult" :key="tableResult.table" class="scan-result-table">
            <div class="scan-result-title">
              {{ tableResult.schema }}.{{ tableResult.table }} · 采样 {{ tableResult.sampled }} 列 · 耗时 {{ tableResult.elapsed }}
            </div>
            <a-empty v-if="!tableResult.suggestions.length" description="未发现敏感列" :image="simpleImage" />
            <div v-for="s in tableResult.suggestions" :key="s.column_name" class="scan-result-item">
              <span class="mono">{{ s.column_name }}</span>
              <a-tag :color="dataTypeColor(s.data_type)">{{ dataTypeLabel(s.data_type) }}</a-tag>
              <span :class="['conf-text', s.confidence >= 90 ? 'conf-high' : 'conf-mid']">{{ s.confidence }}%</span>
              <span class="sample-hit">{{ s.sample_hit }}</span>
            </div>
          </div>
        </div>
      </a-form>
    </a-modal>

    <!-- 明文申请弹窗 -->
    <a-modal v-model:open="grantModal.open" title="申请明文访问" @ok="submitGrant">
      <a-form layout="vertical">
        <a-form-item label="实例" required>
          <a-select v-model:value="grantModal.form.instance_id" :options="instanceOptions" @change="handleGrantInstanceChange" />
        </a-form-item>
        <a-form-item label="范围">
          <a-space>
            <a-select
              v-model:value="grantModal.form.schema_name"
              show-search allow-clear option-filter-prop="label"
              placeholder="库（留空=整实例）"
              style="width: 150px"
              :loading="grantSchemaLoading"
              :options="grantSchemaOptions"
              @change="handleGrantSchemaChange"
            />
            <a-select
              v-model:value="grantModal.form.table_name"
              show-search allow-clear option-filter-prop="label"
              placeholder="表（留空=整库）"
              style="width: 150px"
              :loading="grantTableLoading"
              :options="grantTableOptions"
              @change="handleGrantTableChange"
            />
            <a-select
              v-model:value="grantModal.form.column_name"
              show-search allow-clear option-filter-prop="label"
              placeholder="列（留空=整表）"
              style="width: 150px"
              :loading="grantColumnLoading"
              :options="grantColumnOptions"
            />
          </a-space>
        </a-form-item>
        <a-form-item label="有效时长" required>
          <a-select
            v-model:value="grantModal.form.hours"
            :options="[
              { label: '2 小时', value: 2 },
              { label: '8 小时', value: 8 },
              { label: '24 小时', value: 24 },
            ]"
          />
        </a-form-item>
        <a-form-item label="申请事由" required>
          <a-textarea v-model:value="grantModal.form.reason" :rows="3" :maxlength="255" placeholder="例如：客诉工单 #8821 需核对用户手机号" />
        </a-form-item>
        <div class="scan-note">工单审批通过后限时生效，到期自动收回；明文查询将逐条记录审计。</div>
      </a-form>
    </a-modal>
  </div>
</template>

<script setup>
import { computed, onActivated, onMounted, reactive, ref } from 'vue'
import { message } from 'ant-design-vue'
import { CopyOutlined, PlusOutlined } from '@ant-design/icons-vue'
import { Empty } from 'ant-design-vue'
import { useRouter } from 'vue-router'
import { useUserStore } from '@/store/user'
import { GetInstancesApi } from '@/api/das'
import { writeClipboard } from '@/utils/clipboard'
import {
  getDataSecRulesApi, createDataSecRuleApi, updateDataSecRuleApi, deleteDataSecRuleApi,
  getDataSecColumnsApi, saveDataSecColumnApi, updateDataSecColumnStatusApi, deleteDataSecColumnApi,
  getDataSecSchemaOptionsApi, getDataSecTableOptionsApi, getDataSecColumnOptionsApi,
  scanDataSecTableApi,
  getDataSecGrantsApi, createDataSecGrantApi, decideDataSecGrantApi,
} from '@/api/datasec'
import { maskPreview, sampleOfDataType } from '@/views/datasource/datasec/maskPreview'
import { matchTablePatterns, orderSelection } from '@/views/datasource/datasec/scanTablePick'

defineOptions({ name: 'DataSecView' })

const simpleImage = Empty.PRESENTED_IMAGE_SIMPLE
const userStore = useUserStore()
const router = useRouter()
const username = computed(() => userStore.userInfo?.username || '')
const canManage = computed(() => userStore.hasPermission('datasec.rule.manage'))
const canApprove = computed(() => userStore.hasPermission('datasec.plain.approve'))
const canViewInstance = computed(() => userStore.hasPermission('system.instance.view'))

const activeTab = ref('rules')

const DATA_TYPE_LABELS = {
  phone: '手机号', id_card: '身份证号', bank_card: '银行卡号',
  email: '邮箱', address: '地址', name: '姓名', custom: '自定义',
}
const DATA_TYPE_COLORS = {
  phone: 'blue', id_card: 'purple', bank_card: 'geekblue',
  email: 'cyan', address: 'orange', name: 'green', custom: 'default',
}
const ALGORITHM_LABELS = {
  full: '全部遮蔽', head_tail: '保留前后', tail: '仅保留后几位',
  head: '仅保留前几位', email: '邮箱脱敏', address: '地址脱敏', name: '姓名脱敏', hash: 'SHA-256 哈希',
}
const dataTypeLabel = (v) => DATA_TYPE_LABELS[v] || v
const dataTypeColor = (v) => DATA_TYPE_COLORS[v] || 'default'
const algorithmLabel = (v, params) => {
  const base = ALGORITHM_LABELS[v] || v
  try {
    const p = typeof params === 'string' && params ? JSON.parse(params) : (params || {})
    if (v === 'head_tail') return `${base}（前${p.keep_head ?? 0}后${p.keep_tail ?? 0}）`
    if (v === 'tail') return `${base}（后${p.keep_tail ?? 0}）`
    if (v === 'head') return `${base}（前${p.keep_head ?? 0}）`
  } catch { /* params 非法时只显示算法名 */ }
  return base
}
// 前端本地预览：与后端 ApplyMaskAlgorithm 同口径（见 datasec/services/mask.go）
// 样例按敏感类型取，身份证按 18 位、银行卡按 62 开头，长度形态与真实列一致。
const previewOf = (record) => maskPreview(record.algorithm, record.params, sampleOfDataType(record.data_type))
// 备注里的「内置：」与「来源」列信息重复，展示时去掉（兼容已落库的旧数据）
const remarkOf = (record) => String(record.remark || '').replace(/^内置[:：]\s*/, '')

// ---------- 实例下拉（三个 Tab 共用） ----------
const instanceOptions = ref([])
// instance_id -> { name, addr }：列表里实例名称要跟着 ID 一起展示，名称取实例的「数据源名称」，
// 未填名称时退化为 主机:端口（与实例管理页的展示口径一致）。
const instanceMeta = ref({})
const loadInstances = async () => {
  if (instanceOptions.value.length) return
  const res = await GetInstancesApi().catch(() => null)
  const rows = res?.data || []
  instanceOptions.value = rows.map((item) => ({
    label: `${item.hostname || '-'}:${item.port}（${item.db_type || '-'}）`,
    value: item.instance_id,
  }))
  const meta = {}
  rows.forEach((item) => {
    meta[item.instance_id] = {
      name: String(item.remark || '').trim(),
      addr: item.hostname ? `${item.hostname}:${item.port}` : '',
    }
  })
  instanceMeta.value = meta
}
const instanceNameOf = (instanceId) => {
  const meta = instanceMeta.value[instanceId]
  if (!meta) return ''
  return meta.name || meta.addr
}
const copyInstanceId = async (instanceId) => {
  const value = String(instanceId || '').trim()
  if (!value) return
  if (await writeClipboard(value)) {
    message.success('已复制实例 ID')
  } else {
    message.error('复制失败，请手动选择文本复制')
  }
}
// 跳实例管理并按实例 ID 检索：目标页已支持 ?search= 参数（会写入搜索框并查询），
// 这里只负责带上参数，不复制目标页的查询逻辑。
const goInstance = (instanceId) => {
  const value = String(instanceId || '').trim()
  if (!value) return
  if (!canViewInstance.value) {
    message.warning('当前账号没有实例管理查看权限')
    return
  }
  router.push({ name: 'view.admin.instance', query: { search: value } })
}

// ---------- 脱敏规则 ----------
const ruleList = ref([])
const ruleLoading = ref(false)
const ruleColumns = [
  { title: '规则名称', dataIndex: 'name', key: 'name' },
  { title: '敏感类型', key: 'data_type', width: 110 },
  { title: '算法', key: 'algorithm', width: 170 },
  { title: '效果预览', key: 'mask_preview', width: 170 },
  { title: '状态', key: 'enabled', width: 80 },
  { title: '来源', key: 'built_in', width: 90 },
  { title: '备注', dataIndex: 'remark', key: 'remark', ellipsis: true },
  { title: '操作', key: 'action', width: 130 },
]
const loadRules = async () => {
  ruleLoading.value = true
  try {
    const res = await getDataSecRulesApi()
    ruleList.value = res?.data || []
  } finally {
    ruleLoading.value = false
  }
}
const ruleNameOf = (ruleId) => ruleList.value.find((r) => r.id === ruleId)?.name || `#${ruleId}`
const enabledRuleOptions = computed(() =>
  ruleList.value.filter((r) => r.enabled === 1).map((r) => ({ label: `${r.name}（${algorithmLabel(r.algorithm, r.params)}）`, value: r.id })))

const ruleModal = reactive({
  open: false,
  form: { id: null, name: '', data_type: 'phone', algorithm: 'head_tail', enabled: true, remark: '' },
  keepHead: 3,
  keepTail: 4,
})
const keepHead = computed({
  get: () => ruleModal.keepHead,
  set: (v) => { ruleModal.keepHead = v },
})
const keepTail = computed({
  get: () => ruleModal.keepTail,
  set: (v) => { ruleModal.keepTail = v },
})
const needsParams = computed(() => ['head_tail', 'tail', 'head'].includes(ruleModal.form.algorithm))
const rulePreview = computed(() => {
  const params = needsParams.value ? JSON.stringify({ keep_head: ruleModal.keepHead, keep_tail: ruleModal.keepTail }) : ''
  return maskPreview(ruleModal.form.algorithm, params)
})
const openRuleModal = (record) => {
  if (record) {
    ruleModal.form = { id: record.id, name: record.name, data_type: record.data_type, algorithm: record.algorithm, enabled: record.enabled === 1, remark: record.remark || '' }
    const p = typeof record.params === 'string' && record.params ? JSON.parse(record.params) : (record.params || {})
    ruleModal.keepHead = p.keep_head ?? 0
    ruleModal.keepTail = p.keep_tail ?? 0
  } else {
    ruleModal.form = { id: null, name: '', data_type: 'phone', algorithm: 'head_tail', enabled: true, remark: '' }
    ruleModal.keepHead = 3
    ruleModal.keepTail = 4
  }
  ruleModal.open = true
}
const submitRule = async () => {
  const f = ruleModal.form
  if (!f.name) { message.warning('请填写规则名称'); return }
  const payload = {
    id: f.id || undefined,
    name: f.name,
    data_type: f.data_type,
    algorithm: f.algorithm,
    params: needsParams.value ? JSON.stringify({ keep_head: ruleModal.keepHead, keep_tail: ruleModal.keepTail }) : '',
    enabled: f.enabled,
    remark: f.remark,
  }
  if (f.id) {
    await updateDataSecRuleApi(f.id, payload)
  } else {
    delete payload.id
    await createDataSecRuleApi(payload)
  }
  message.success('保存成功')
  ruleModal.open = false
  loadRules()
}
const removeRule = async (record) => {
  if (record.built_in === 1) {
    await updateDataSecRuleApi(record.id, { ...record, enabled: false })
    message.success('已停用内置规则')
  } else {
    await deleteDataSecRuleApi(record.id)
    message.success('已删除')
  }
  loadRules()
}

const dataTypeOptions = Object.entries(DATA_TYPE_LABELS).map(([value, label]) => ({ value, label }))
const algorithmOptions = Object.entries(ALGORITHM_LABELS).map(([value, label]) => ({ value, label }))

// ---------- 列配置 ----------
const columnList = ref([])
const columnLoading = ref(false)
const columnFilter = reactive({ instance_id: undefined, status: undefined })
const columnColumns = [
  { title: '实例', key: 'instance_id', width: 180 },
  { title: '列路径（库.表.列）', key: 'column_path' },
  { title: '规则', key: 'rule_name', width: 170 },
  { title: '状态', key: 'status', width: 100 },
  { title: '置信度', key: 'confidence', width: 90 },
  { title: '来源', key: 'source', width: 100 },
  { title: '操作', key: 'action', width: 130 },
]
// 状态列只用文字表达，不再用彩色标签：列表本身就是管理视图，颜色噪声比信息多。
const columnStatusText = (status) => {
  if (status === 'enabled') return '已生效'
  if (status === 'suggested') return '待确认'
  return '停用'
}
const loadColumns = async () => {
  columnLoading.value = true
  try {
    const params = {}
    if (columnFilter.instance_id) params.instance_id = columnFilter.instance_id
    if (columnFilter.status) params.status = columnFilter.status
    const res = await getDataSecColumnsApi(params)
    columnList.value = res?.data || []
  } finally {
    columnLoading.value = false
  }
}
const changeColumnStatus = async (record, status) => {
  await updateDataSecColumnStatusApi(record.id, status)
  message.success(status === 'enabled' ? '已启用' : '已停用')
  loadColumns()
}
const removeColumn = async (record) => {
  await deleteDataSecColumnApi(record.id)
  message.success('已删除')
  loadColumns()
}

// ---------- 库表列级联（弹窗与扫描共用） ----------
// 走 datasec 自己的选项接口：库来自实例元数据快照，表/列连实例实时读结构。
// 不用 /das/schemas 那套——它按调用者已有数据授权过滤，配规则的 DBA 常常没有目标库授权，
// 结果就是「选了实例，库下拉一片空白」。
const buildSchemaOptions = async (instanceId) => {
  if (!instanceId) return []
  const fetchOnce = async () => {
    const res = await getDataSecSchemaOptionsApi({ instance_id: instanceId }).catch(() => null)
    return (res?.data || []).map((s) => ({ label: s.display_name || s.schema, value: s.schema }))
  }
  let options = await fetchOnce()
  if (!options.length) {
    // 实例从未同步过元数据时快照为空，服务端会在这次请求里异步补刷一次；
    // 稍等再取一遍，免得用户第一次选实例就看到空下拉。
    await new Promise((resolve) => setTimeout(resolve, 1500))
    options = await fetchOnce()
  }
  return options
}
const buildTableOptions = async (instanceId, schema) => {
  if (!instanceId || !schema) return []
  const res = await getDataSecTableOptionsApi({ instance_id: instanceId, schema_name: schema }).catch(() => null)
  return (res?.data || []).map((t) => ({ label: t.name, value: t.name }))
}
const buildColumnOptions = async (instanceId, schema, table) => {
  if (!instanceId || !schema || !table) return []
  const res = await getDataSecColumnOptionsApi({
    instance_id: instanceId, schema_name: schema, table_name: table,
  }).catch(() => null)
  return (res?.data || []).map((c) => ({ label: c.name, value: c.name }))
}

// 绑定列弹窗
const columnModal = reactive({ open: false, schema: '', table: '', form: { instance_id: undefined, rule_id: undefined, column_name: '' } })
const modalSchemaLoading = ref(false)
const modalTableLoading = ref(false)
const modalColumnLoading = ref(false)
const modalSchemaOptions = ref([])
const modalTableOptions = ref([])
const modalColumnOptions = ref([])
const openColumnModal = async (record) => {
  columnModal.open = true
  await loadInstances()
  if (record) {
    columnModal.form.instance_id = record.instance_id
    columnModal.schema = record.schema_name
    columnModal.table = record.table_name
    columnModal.form = { instance_id: record.instance_id, rule_id: record.rule_id, column_name: record.column_name }
    modalSchemaOptions.value = await buildSchemaOptions(record.instance_id)
    modalTableOptions.value = await buildTableOptions(record.instance_id, record.schema_name)
    modalColumnOptions.value = await buildColumnOptions(record.instance_id, record.schema_name, record.table_name)
  } else {
    columnModal.schema = ''
    columnModal.table = ''
    columnModal.form = { instance_id: undefined, rule_id: undefined, column_name: '' }
    modalSchemaOptions.value = []
    modalTableOptions.value = []
    modalColumnOptions.value = []
  }
}
// 切换实例后必须主动拉一次库列表：只清空 schema/table 而不同步取数，
// 下拉选项会一直是空数组，用户看到的就是「暂无数据」。
const handleColumnInstanceChange = async () => {
  columnModal.schema = ''
  columnModal.table = ''
  columnModal.form.column_name = ''
  modalSchemaOptions.value = []
  modalTableOptions.value = []
  modalColumnOptions.value = []
  if (!columnModal.form.instance_id) return
  modalSchemaLoading.value = true
  modalSchemaOptions.value = await buildSchemaOptions(columnModal.form.instance_id)
  modalSchemaLoading.value = false
}
const handleModalSchemaChange = async () => {
  columnModal.table = ''
  columnModal.form.column_name = ''
  if (!columnModal.form.instance_id || !columnModal.schema) return
  modalTableLoading.value = true
  modalTableOptions.value = await buildTableOptions(columnModal.form.instance_id, columnModal.schema)
  modalTableLoading.value = false
}
const handleModalTableChange = async () => {
  columnModal.form.column_name = ''
  if (!columnModal.form.instance_id || !columnModal.schema || !columnModal.table) return
  modalColumnLoading.value = true
  modalColumnOptions.value = await buildColumnOptions(columnModal.form.instance_id, columnModal.schema, columnModal.table)
  modalColumnLoading.value = false
}
const submitColumn = async () => {
  const f = columnModal.form
  if (!f.instance_id || !columnModal.schema || !columnModal.table || !f.column_name || !f.rule_id) {
    message.warning('请完整选择 实例 / 库 / 表 / 列 / 规则')
    return
  }
  await saveDataSecColumnApi({
    instance_id: f.instance_id,
    schema_name: columnModal.schema,
    table_name: columnModal.table,
    column_name: f.column_name,
    rule_id: f.rule_id,
  })
  message.success('绑定成功')
  columnModal.open = false
  columnFilter.instance_id = f.instance_id
  loadColumns()
}

// 自动发现弹窗
const scanLoading = ref(false)
const scanModal = reactive({
  open: false,
  form: { instance_id: undefined, schema_name: '' },
  tableSelection: [],
  pattern: '',
})
const scanSchemaLoading = ref(false)
const scanTableLoading = ref(false)
const scanSchemaOptions = ref([])
const scanTableOptions = ref([])
const scanResult = ref([])

// ---------- 表名快捷选择（全选 / 通配 / 反选） ----------
// 三种操作与手工勾选写的是同一份 tableSelection：提交时仍按逗号拼接，
// 后端拿到的永远是具体表名（通配在本地展开，服务端只接受标识符，不做模式匹配）。
const scanTableNames = computed(() => scanTableOptions.value.map((o) => o.value).filter(Boolean))

// 结果统一排序：清单内的按清单顺序，手工输入的（清单外）保持原顺序追加在末尾。
// 这样批量选择与逐条勾选混合后，选中数组始终稳定，不会因操作顺序不同产生差异。
const orderScanSelection = (names) => orderSelection(names, scanTableNames.value)

const selectAllScanTables = () => {
  if (!scanTableNames.value.length) return
  scanModal.tableSelection = orderScanSelection([...scanTableNames.value, ...scanModal.tableSelection])
}

const invertScanTables = () => {
  if (!scanTableNames.value.length) return
  const selected = new Set(scanModal.tableSelection)
  const inverted = scanTableNames.value.filter((name) => !selected.has(name))
  // 清单外的手工输入表名不属于反选范围，原样保留，避免用户输入被静默丢弃
  const custom = scanModal.tableSelection.filter((name) => !scanTableNames.value.includes(name))
  scanModal.tableSelection = orderScanSelection([...inverted, ...custom])
}

const applyScanPattern = () => {
  const raw = String(scanModal.pattern || '').trim()
  if (!scanTableNames.value.length) {
    message.warning('表清单尚未加载，请先选择库后再按模式匹配')
    return
  }
  if (!raw) {
    message.warning('请输入通配模式，例如 t_user* 或 *_log')
    return
  }
  const { patterns, matched, missing } = matchTablePatterns(raw, scanTableNames.value)
  if (!matched.length) {
    message.warning(`表清单中没有匹配 ${patterns.join('、')} 的表，请检查模式`)
    return
  }
  scanModal.tableSelection = orderScanSelection([...scanModal.tableSelection, ...matched])
  const tail = missing.length ? `；未匹配：${missing.join('、')}` : ''
  message.success(`模式 ${patterns.join('、')} 命中 ${matched.length} 张表${tail}`)
}

const clearScanTables = () => {
  scanModal.tableSelection = []
}
const openScanModal = async () => {
  scanModal.open = true
  scanModal.pattern = ''
  scanResult.value = []
  await loadInstances()
}
const handleScanInstanceChange = async () => {
  scanModal.form.schema_name = ''
  scanModal.tableSelection = []
  scanModal.pattern = ''
  scanTableOptions.value = []
  if (!scanModal.form.instance_id) return
  scanSchemaLoading.value = true
  scanSchemaOptions.value = await buildSchemaOptions(scanModal.form.instance_id)
  scanSchemaLoading.value = false
}
const handleScanSchemaChange = async () => {
  scanModal.tableSelection = []
  scanModal.pattern = ''
  if (!scanModal.form.instance_id || !scanModal.form.schema_name) return
  scanTableLoading.value = true
  scanTableOptions.value = await buildTableOptions(scanModal.form.instance_id, scanModal.form.schema_name)
  scanTableLoading.value = false
}
const submitScan = async () => {
  const f = scanModal.form
  if (!f.instance_id || !f.schema_name || !scanModal.tableSelection.length) {
    message.warning('请选择实例、库与至少一张表')
    return
  }
  scanLoading.value = true
  try {
    const res = await scanDataSecTableApi({
      instance_id: f.instance_id,
      schema_name: f.schema_name,
      table_names: scanModal.tableSelection.join(','),
    })
    scanResult.value = res?.data || []
    message.success('扫描完成，建议结果请确认后启用')
    loadColumns()
  } finally {
    scanLoading.value = false
  }
}

// 实例切换联动（列配置筛选条）
const handleInstanceChange = () => loadColumns()

// ---------- 明文工单 ----------
const grantList = ref([])
const grantLoading = ref(false)
const grantFilter = reactive({ scope: 'mine', status: '' })
const grantColumns = [
  { title: '工单号', key: 'grant_no', width: 160 },
  { title: '实例', dataIndex: 'instance_id', key: 'instance_id', width: 130 },
  { title: '范围', key: 'scope' },
  { title: '申请人', dataIndex: 'username', key: 'username', width: 110 },
  { title: '事由', dataIndex: 'reason', key: 'reason', ellipsis: true },
  { title: '状态', key: 'status', width: 100 },
  { title: '审批人', dataIndex: 'approver', key: 'approver', width: 100 },
  { title: '到期时间', key: 'expires_at', width: 170 },
  { title: '操作', key: 'action', width: 130 },
]
const grantScopeText = (record) => {
  const parts = [record.schema_name || '全部库', record.table_name || '全部表', record.column_name || '全部列']
  return parts.join(' / ')
}
const GRANT_STATUS = {
  pending: ['待审批', 'orange'], approved: ['已生效', 'green'], rejected: ['已驳回', 'red'],
  expired: ['已过期', 'default'], revoked: ['已收回', 'default'],
}
const grantStatusLabel = (record) => {
  if (record.status === 'pending') return '待审批'
  if (record.status === 'approved' && record.expires_at && new Date(record.expires_at.replace(/-/g, '/')) < new Date()) return '已过期（待刷新）'
  return GRANT_STATUS[record.status]?.[0] || record.status
}
const grantStatusColor = (status) => GRANT_STATUS[status]?.[1] || 'default'
const loadGrants = async () => {
  grantLoading.value = true
  try {
    const params = { scope: grantFilter.scope }
    if (grantFilter.status) params.status = grantFilter.status
    const res = await getDataSecGrantsApi(params)
    grantList.value = res?.data || []
  } finally {
    grantLoading.value = false
  }
}
const grantModal = reactive({ open: false, form: { instance_id: undefined, schema_name: '', table_name: '', column_name: '', hours: 2, reason: '' } })
const grantSchemaLoading = ref(false)
const grantTableLoading = ref(false)
const grantColumnLoading = ref(false)
const grantSchemaOptions = ref([])
const grantTableOptions = ref([])
const grantColumnOptions = ref([])
const openGrantModal = async () => {
  grantModal.open = true
  grantModal.form = { instance_id: undefined, schema_name: '', table_name: '', column_name: '', hours: 2, reason: '' }
  grantSchemaOptions.value = []
  grantTableOptions.value = []
  grantColumnOptions.value = []
  await loadInstances()
}
// 同 handleColumnInstanceChange：换实例要连同库选项一起重新拉
const handleGrantInstanceChange = async () => {
  grantModal.form.schema_name = ''
  grantModal.form.table_name = ''
  grantModal.form.column_name = ''
  grantSchemaOptions.value = []
  grantTableOptions.value = []
  grantColumnOptions.value = []
  if (!grantModal.form.instance_id) return
  grantSchemaLoading.value = true
  grantSchemaOptions.value = await buildSchemaOptions(grantModal.form.instance_id)
  grantSchemaLoading.value = false
}
const handleGrantSchemaChange = async () => {
  grantModal.form.table_name = ''
  grantModal.form.column_name = ''
  if (!grantModal.form.instance_id || !grantModal.form.schema_name) return
  grantTableLoading.value = true
  grantTableOptions.value = await buildTableOptions(grantModal.form.instance_id, grantModal.form.schema_name)
  grantTableLoading.value = false
}
const handleGrantTableChange = async () => {
  grantModal.form.column_name = ''
  if (!grantModal.form.instance_id || !grantModal.form.schema_name || !grantModal.form.table_name) return
  grantColumnLoading.value = true
  grantColumnOptions.value = await buildColumnOptions(grantModal.form.instance_id, grantModal.form.schema_name, grantModal.form.table_name)
  grantColumnLoading.value = false
}
const submitGrant = async () => {
  const f = grantModal.form
  if (!f.instance_id || !f.reason) {
    message.warning('请选择实例并填写申请事由')
    return
  }
  await createDataSecGrantApi(f)
  message.success('申请已提交，等待审批')
  grantModal.open = false
  grantFilter.scope = 'mine'
  loadGrants()
}
const decideGrant = async (record, action) => {
  await decideDataSecGrantApi(record.id, { id: record.id, action })
  message.success(action === 'approve' ? '已批准' : action === 'reject' ? '已驳回' : '已收回')
  loadGrants()
}

// ---------- Tab 切换懒加载 ----------
const loadedTabs = { rules: false, columns: false, grants: false }
const handleTabSwitch = async (key) => {
  await loadTabData(key)
}
const loadTabData = async (key) => {
  if (loadedTabs[key]) return
  loadedTabs[key] = true
  if (key === 'rules') await loadRules()
  if (key === 'columns') {
    await Promise.all([loadRules(), loadColumns(), loadInstances()])
  }
  if (key === 'grants') {
    await Promise.all([loadGrants(), loadInstances()])
  }
}

onMounted(() => loadTabData('rules'))
onActivated(() => loadTabData(activeTab.value))
</script>

<style scoped>
.datasec-page .filter-bar {
  display: flex;
  gap: 10px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}
/* 库表列配置筛选栏：上移到 tab 标题行右侧（a-tabs #rightExtra）。
   与「告警中心」同款方案：extra 在 tab 导航行内垂直居中；
   空间不足时筛选区自身横向滚动，不挤压左侧 tab 标签。 */
.datasec-page :deep(.ant-tabs-extra-content) {
  display: flex;
  align-items: center;
}
.columns-filter-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: nowrap;
  max-width: min(480px, calc(100vw - 360px));
  overflow-x: auto;
}
.columns-filter-bar .filter-select {
  flex: 0 1 auto;
  min-width: 0;
}
.columns-filter-bar .filter-instance {
  width: 220px;
}
.columns-filter-bar .filter-status {
  width: 140px;
}
/* 窄屏（<768px）：筛选栏整体折到 tab 下一行，控件各占一行 */
@media (max-width: 768px) {
  .datasec-page :deep(.ant-tabs-nav) {
    flex-wrap: wrap;
  }
  .datasec-page :deep(.ant-tabs-extra-content) {
    flex: 1 1 100%;
    min-width: 0;
  }
  .columns-filter-bar {
    max-width: 100%;
    width: 100%;
    overflow-x: visible;
  }
  .columns-filter-bar .filter-instance,
  .columns-filter-bar .filter-status {
    width: 100%;
    flex: 1 1 100%;
  }
}
.mono {
  font-family: 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12.5px;
}
.text-muted {
  color: var(--text-muted-color, #98a2b3);
}
/* 操作列统一用系统主题色（与 design/tokens.js 的 primary 对齐） */
.action-link {
  color: var(--ant-colorPrimary, #0f766e);
}
.action-link:hover {
  color: var(--ant-colorPrimaryHover, #0d6b63);
}
/* 实例列：上行实例 ID（主题色，可点进实例管理）+ 复制，下行实例名称。
   结构与「实例配置」页的数据源 ID 单元格保持一致。 */
.instance-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.instance-id-row {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}
.instance-id-text {
  font-family: 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12.5px;
  font-weight: 500;
  color: var(--ant-colorPrimary, #0f766e);
}
.instance-id-text:hover {
  color: var(--ant-colorPrimaryHover, #0d6b63);
  text-decoration: underline;
}
.copy-id-btn {
  padding: 0;
  width: 18px;
  height: 18px;
  min-width: 18px;
  flex: 0 0 auto;
  color: var(--text-muted-color, #98a2b3);
}
.copy-id-btn:hover {
  color: var(--ant-colorPrimary, #0f766e);
}
.instance-name {
  font-size: 12px;
  line-height: 1.4;
  color: var(--text-muted-color, #98a2b3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 168px;
}
.instance-name:hover {
  color: var(--ant-colorPrimary, #0f766e);
}
.sample-hit {
  display: block;
  color: #98a2b3;
  font-size: 12px;
  margin-top: 2px;
}
.conf-text {
  font-weight: 600;
}
.conf-high {
  color: #16a34a;
}
.conf-mid {
  color: #d97706;
}
.action-danger {
  color: #dc2626;
}
.preview-line {
  background: #101828;
  border-radius: 6px;
  padding: 8px 12px;
}
.preview-input {
  color: #98a2b3;
}
.preview-output {
  color: #7ef0b1;
}
/* 表名快捷选择条：全选 / 反选 / 通配匹配 + 已选计数 */
.table-pick-tools {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-top: 8px;
}
.pick-count {
  font-size: 12.5px;
  white-space: nowrap;
}
.pick-count-warn {
  font-size: 12.5px;
  white-space: nowrap;
  color: #d97706;
}
.scan-note {
  background: #fffbeb;
  border-left: 3px solid #d97706;
  border-radius: 0 6px 6px 0;
  padding: 8px 12px;
  font-size: 12.5px;
  color: #92400e;
}
.scan-result {
  margin-top: 12px;
  max-height: 320px;
  overflow: auto;
}
.scan-result-table {
  margin-bottom: 12px;
}
.scan-result-title {
  font-weight: 600;
  font-size: 13px;
  margin-bottom: 6px;
}
.scan-result-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 0;
  border-bottom: 1px dashed #f0f0f0;
}
.scan-result-item .sample-hit {
  display: inline;
  margin: 0;
}
</style>
