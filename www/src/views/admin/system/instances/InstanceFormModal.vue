<template>
  <a-modal
    :open="props.open"
    :title="props.title"
    :width="isCloudTabActive ? 1080 : 720"
    wrap-class-name="instance-form-modal-wrap instance-modal-with-scroll"
    @cancel="handleCancel"
  >
    <template #footer>
      <div class="instance-modal-footer">
        <a-button
          class="instance-test-btn"
          :disabled="isCloudTabActive || !props.canTestConnection && !props.canManage"
          :loading="uiState.testing"
          @click="handleTestConnection"
        >
          测试连接
        </a-button>
        <div class="instance-footer-actions">
          <a-tooltip v-if="isCloudTabActive" :title="cloudImportDisabledReason" placement="topLeft">
            <a-button
              type="primary"
              ghost
              class="instance-import-btn"
              :disabled="!canCloudImport || !props.canManage"
              :loading="cloudImportLoading"
              @click="handleCloudImportClick"
            >
              <ImportOutlined />
              导入
            </a-button>
          </a-tooltip>
          <a-button
            v-else
            class="instance-import-btn"
            style="visibility: hidden; pointer-events: none"
          >
            导入
          </a-button>
          <a-button
            class="instance-confirm-btn"
            type="primary"
            :disabled="isCloudTabActive || !props.canManage"
            :loading="uiState.loading"
            @click="onSubmit"
          >
            确定
          </a-button>
        </div>
      </div>
    </template>

    <div class="instance-modal-body">
      <div class="instance-modal-scroll">
        <a-tabs
          v-model:activeKey="activeTabKey"
          class="instance-tabs"
          :animated="false"
        >
          <a-tab-pane key="manual" tab="手动配置">
            <a-alert
              v-if="isCopyMode"
              class="instance-copy-hint"
              type="info"
              show-icon
              :message="copyHintText"
            />
            <a-form
              ref="formRef"
              layout="vertical"
              :model="formData"
              :rules="rules"
              :disabled="!props.canManage"
              class="instance-modal-form"
            >
            <a-row :gutter="16">
              <a-col :span="12">
                <a-form-item label="环境" name="environment">
                  <a-select
                    v-model:value="formData.environment"
                    :options="props.environments"
                    :field-names="{ label: 'name', value: 'id' }"
                    placeholder="请选择所属环境"
                    allow-clear
                  />
                </a-form-item>
              </a-col>
              <a-col :span="12">
                <a-form-item label="数据库类型" name="db_type">
                  <a-select
                    v-model:value="formData.db_type"
                    :options="uiData.dbTypes"
                    placeholder="请选择数据库类型"
                    allow-clear
                  />
                </a-form-item>
              </a-col>
            </a-row>

            <!-- OceanBase 专属：租户模式（MySQL / Oracle）。模式决定后续连接、元数据同步、
                 SQL 工作台走哪条通道，必须先于其他参数确定，因此紧贴「数据库类型」下方。 -->
            <a-row v-if="isOceanBase" :gutter="16">
              <a-col :span="12">
                <a-form-item label="租户模式" name="oceanbase_mode">
                  <a-radio-group
                    :value="oceanbaseMode"
                    button-style="solid"
                    @change="handleOceanBaseModeChange"
                  >
                    <a-radio-button value="mysql">MySQL 模式</a-radio-button>
                    <a-radio-button value="oracle">Oracle 模式</a-radio-button>
                  </a-radio-group>
                </a-form-item>
              </a-col>
            </a-row>

            <!-- 数据源名称 + 连接地址：一行两列，独立方框（与「服务器主机登录」同一套方框风格） -->
            <div class="instance-basic-section">
              <a-row :gutter="16">
                <a-col :span="12">
                  <a-form-item label="数据源名称" name="remark">
                    <a-input v-model:value="formData.remark" placeholder="请输入数据源名称" />
                  </a-form-item>
                </a-col>
                <a-col :span="12">
                  <a-form-item label="连接地址" name="connection_address">
                    <a-input
                      v-model:value="formData.connection_address"
                      placeholder="例如 192.168.53.76:5432"
                      @blur="applyConnectionAddressToFields"
                      @pressEnter="applyConnectionAddressToFields"
                    />
                  </a-form-item>
                </a-col>
              </a-row>
            </div>

            <a-row :gutter="16">
              <a-col :span="12">
                <a-form-item label="登录账号" name="user">
                  <a-input v-model:value="formData.user" placeholder="请输入登录账号" />
                </a-form-item>
              </a-col>
              <a-col :span="12">
                <a-form-item
                  label="密码"
                  name="password"
                >
                  <a-input-password
                    v-model:value="formData.password"
                    :placeholder="isEditMode ? '留空则不修改密码' : '请输入密码'"
                    :status="uiState.passwordFieldError ? 'error' : undefined"
                  />
                </a-form-item>
              </a-col>
            </a-row>

            <!-- 运维录入型画像字段：部署形态 / 云厂商 / 高可用模式从数据库侧探测不出来，
                 只能在这里录入；留空时监控概览按"未录入 + 原因"展示，不会假装有值。 -->
            <a-row :gutter="16">
              <a-col :span="8">
                <a-form-item label="部署形态" name="deploy_provider">
                  <a-select
                    v-model:value="formData.deploy_provider"
                    :options="uiData.deployProviders"
                    placeholder="请选择部署形态"
                    allow-clear
                  />
                </a-form-item>
              </a-col>
              <a-col :span="8">
                <a-form-item label="高可用模式" name="ha_mode">
                  <a-auto-complete
                    v-model:value="formData.ha_mode"
                    :options="uiData.haModes"
                    placeholder="请输入或选择高可用模式"
                    allow-clear
                  />
                </a-form-item>
              </a-col>
              <a-col :span="8">
                <a-form-item label="磁盘类型" name="disk_type">
                  <a-input v-model:value="formData.disk_type" placeholder="如 SSD / HDD / ESSD PL1" allow-clear />
                </a-form-item>
              </a-col>
            </a-row>
            <a-row v-if="formData.deploy_provider === 'cloud'" :gutter="16">
              <a-col :span="8">
                <a-form-item label="云厂商" name="cloud_vendor">
                  <a-select
                    v-model:value="formData.cloud_vendor"
                    :options="uiData.cloudVendors"
                    placeholder="请选择云厂商"
                    allow-clear
                  />
                </a-form-item>
              </a-col>
            </a-row>

            <div
              v-if="showsSSHSection"
              class="host-login-section"
              :class="{ collapsed: uiState.sshSectionCollapsed, expanded: !uiState.sshSectionCollapsed, 'has-error': sshHasFieldError }"
            >
              <div class="host-login-section__header" @click="toggleSSHSection">
                <component
                  :is="uiState.sshSectionCollapsed ? ICaretRightFilled : ICaretDownFilled"
                  class="host-login-section__caret"
                />
                <div class="host-login-section__title">服务器主机登录（用于备份/恢复与主机终端，监控采集不需要）</div>
                <div class="host-login-section__optional">选填</div>
              </div>
              <div v-show="!uiState.sshSectionCollapsed" class="host-login-section__body">
                <div v-if="showsWindowsSSHHint" class="host-login-windows-hint">
                  Windows 主机默认没有 SSH 服务：请先在主机上安装并启动 OpenSSH Server（管理员 PowerShell 运行
                  <code>Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0</code> 与
                  <code>Start-Service sshd</code>），并放行防火墙 22 端口；SSH 用户填 Windows 系统账号（如 Administrator），
                  不是数据库账号，root 在 Windows 上不存在。
                </div>
                <a-row :gutter="16">
                  <a-col :span="12">
                    <a-form-item label="SSH 用户" name="ssh_user">
                      <a-input
                        v-model:value="formData.ssh_user"
                        placeholder="例如 hs-ops / admin / deploy；Linux 下也可填 root / oracle，Windows 下填系统账号"
                      />
                    </a-form-item>
                  </a-col>
                  <a-col :span="12">
                    <a-form-item label="SSH 端口" name="ssh_port">
                      <a-input-number
                        v-model:value="formData.ssh_port"
                        style="width: 100%"
                        :min="1"
                        :max="65535"
                        placeholder="默认 22"
                      />
                    </a-form-item>
                  </a-col>
                </a-row>
                <div class="host-login-auth__row">
                  <div
                    class="host-login-auth__box"
                    :class="{ active: uiState.sshAuthMode === 'password' }"
                    @click="switchSSHAuthMode('password')"
                  >
                    <div class="host-login-auth__title">使用密码</div>
                  </div>
                  <div
                    class="host-login-auth__box"
                    :class="{ active: uiState.sshAuthMode === 'key' }"
                    @click="switchSSHAuthMode('key')"
                  >
                    <div class="host-login-auth__title">使用 SSH 私钥</div>
                  </div>
                </div>
                <a-form-item
                  v-if="uiState.sshAuthMode === 'password'"
                  label="SSH 密码"
                  name="ssh_password"
                  :extra="isEditMode ? undefined : '与 SSH 私钥二选一填写；不需要备份/恢复或主机登录时可整块留空。'"
                >
                  <a-input-password
                    v-model:value="formData.ssh_password"
                    :placeholder="isEditMode ? '留空则不修改' : '请输入系统用户的 SSH 密码（非数据库密码）'"
                  />
                </a-form-item>
                <a-form-item
                  v-else
                  label="SSH 私钥"
                  name="ssh_private_key"
                  :extra="isEditMode ? '编辑模式可重新粘贴私钥；留空则不修改。' : '与 SSH 密码二选一填写；粘贴完整 BEGIN ... END 私钥内容。不需要备份/恢复时可整块留空。'"
                >
                  <a-textarea
                    v-model:value="formData.ssh_private_key"
                    :rows="4"
                    placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;...粘贴完整私钥内容...&#10;-----END OPENSSH PRIVATE KEY-----"
                  />
                </a-form-item>
              </div>
            </div>

            <!-- 原「扩展配置（可选）」分组标题已移除，分组内的专属配置项保留 -->
            <div v-if="showsOptionalSection" class="instance-form-section">
              <a-row :gutter="16">
                <a-col v-if="showsDatabaseField" :span="12">
                  <a-form-item :label="databaseFieldLabel" name="database">
                    <a-input
                      v-model:value="formData.database"
                      :placeholder="databaseFieldPlaceholder"
                      :maxlength="60"
                      @input="onDatabaseInput"
                    />
                  </a-form-item>
                </a-col>
                <a-col v-if="isPgProtocolType" :span="12">
                  <a-form-item label="SSL 模式" name="sslmode">
                    <a-select v-model:value="formData.sslmode" :options="uiData.postgresSslModes" />
                  </a-form-item>
                </a-col>
                <a-col v-if="showsServiceNameField" :span="12">
                  <a-form-item label="服务名" name="service_name">
                    <a-input v-model:value="formData.service_name" :placeholder="serviceNamePlaceholder" />
                  </a-form-item>
                </a-col>
              </a-row>
            </div>

          </a-form>
        </a-tab-pane>

        <a-tab-pane key="cloud" tab="从公有云导入">
          <CloudImportPanel
            ref="cloudPanelRef"
            v-if="activeTabKey === 'cloud'"
            :environments="props.environments"
            :can-manage="props.canManage"
            @imported="handleCloudImported"
          />
        </a-tab-pane>
      </a-tabs>
      </div>
    </div>
  </a-modal>

  <InstanceCheckReportModal
    :open="checkReportVisible"
    :report="checkReport"
    @close="checkReportVisible = false"
  />
</template>

<script setup>
import { ref, reactive, computed, watch, nextTick, markRaw } from 'vue'
import { message, Modal } from 'ant-design-vue'
import { ImportOutlined, CaretDownFilled, CaretRightFilled } from '@ant-design/icons-vue'
const ICaretDownFilled = markRaw(CaretDownFilled)
const ICaretRightFilled = markRaw(CaretRightFilled)
import { testInstanceConnectionApi, testInstanceConnectionByPayloadApi } from '@/api/admin'
import { isOceanBaseType, normalizeOceanBaseMode, OCEANBASE_ORACLE } from '@/utils/dbFamily'
import CloudImportPanel from './CloudImportPanel.vue'
import InstanceCheckReportModal from './InstanceCheckReportModal.vue'

// 定义props和emits
const props = defineProps({
  open: Boolean,
  title: String,
  environments: Array,
  // 是否允许"写"操作：没权限的话，"测试连接"和"确定"按钮会禁用
  canManage: {
    type: Boolean,
    default: true,
  },
  // 是否允许"测试连接"操作：独立于 manage，用 system.instance.test 权限单独判断
  canTestConnection: {
    type: Boolean,
    default: true,
  },
  // 全量实例地址簿：[{ id, hostname, port, remark, data_source_id }]
  // 仅用于「ip:port 唯一性」的即时预校验（后端仍是最终权威校验）。
  existingAddresses: {
    type: Array,
    default: () => [],
  },
  // 非空表示当前弹窗是「复制」某条实例而来（仍走新增保存链路，isEditMode 为 false）
  copySourceId: {
    type: [Number, String],
    default: null,
  },
  // 源实例的展示名，用于复制态顶部提示文案
  copySourceLabel: {
    type: String,
    default: '',
  },
})
const emit = defineEmits(['update:open', 'submit', 'cloud-imported'])

// 连接检测报告（SSH 状态 + 采集权限体检），仅在存在缺口时弹出，
// 正常情况下不打扰纳管流程。
const checkReportVisible = ref(false)
const checkReport = ref({})
const openCheckReport = (report) => {
  if (!report) return
  const hasPrivilegeGap = Number(report.failed_count || 0) > 0 || Number(report.warn_count || 0) > 0
  const sshAbnormal = report.ssh_status && report.ssh_status !== 'ok'
  if (!hasPrivilegeGap && !sshAbnormal) return
  checkReport.value = report
  checkReportVisible.value = true
}

const activeTabKey = ref('manual')
const cloudPanelRef = ref(null)

const canCloudImport = computed(() => !!cloudPanelRef.value?.canImport)
const cloudImportLoading = computed(() => !!cloudPanelRef.value?.isImporting)
const cloudImportDisabledReason = computed(() => cloudPanelRef.value?.importDisabledReason || '')

const resetCloudTab = () => {
  activeTabKey.value = 'manual'
}

const handleCloudImportClick = async () => {
  if (!cloudPanelRef.value) return
  try {
    await cloudPanelRef.value.triggerImport()
  } catch {
    // 子组件内部已做错误 toast，这里只静默吞错避免 promise reject 冒泡
  }
}

const handleCloudImported = (result) => {
  emit('cloud-imported', result)
  emit('update:open', false)
  emit('submit', { __cloudImported: true, ...result })
}

// 云导入Tab激活时,禁用 footer 的"测试连接"和"确定"
const isCloudTabActive = computed(() => activeTabKey.value === 'cloud')

// 是否编辑态：通过 title 粗略判断（InstanceList.vue 传入“编辑数据库实例/新增数据库实例”）
// 这里不新增 props 字段，保持兼容；若后续需要更严谨，可改为显式传 isEdit。
const isEditMode = computed(() => (props.title || '').includes('编辑'))

// 复制态：由父组件传入 copySourceId 标识，保存语义同「新增」
const isCopyMode = computed(() => props.copySourceId !== null && props.copySourceId !== undefined && props.copySourceId !== '')
const copyHintText = computed(() => {
  const label = String(props.copySourceLabel || '').trim() || '源实例'
  return `已复制「${label}」的配置，请修改连接地址（IP:端口）后再保存；数据库密码与 SSH 凭据不会复制，请重新填写（SSH 凭据选填，不填不影响监控采集，仅备份/恢复需要）。`
})

// 表单数据
const formData = defineModel('modelValue', {
  type: Object,
  required: true,
})

// 表单引用
const formRef = ref()

/* 数据库类型白名单：仅保留平台当前真正支持的类型。
   每一项都必须在后端打通三件事，否则加进来只是"能存不能连"：
     1) forms 的 db_type oneof 白名单（common/forms/instance.go）
     2) 连接测试分派（common/services/instance_connection.go）
     3) 元数据同步分派（common/tasks/tasks.go: SyncDBMetaForInstance）
   SQLServer 三项均已具备（DAS 侧另有执行器、schema 浏览、行数限制改写），
   此前只是前端白名单把它漏掉了，属于"后端能连、界面选不到"。
   MongoDB 仍不在白名单内：DAS 执行链路没有 Mongo 分支。 */
const DB_TYPE_CONFIG = {
  MySQL: {
    defaultPort: 3306,
  },
  TiDB: {
    defaultPort: 4000,
  },
  Doris: {
    defaultPort: 9030,
  },
  ClickHouse: {
    defaultPort: 9000,
  },
  PostgreSQL: {
    defaultPort: 5432,
  },
  /* 金仓 KingbaseES：兼容 PostgreSQL 协议与方言，连接、元数据同步、SQL 工作台全部复用
     PG 通道（后端 dbfamily 已把它归到 postgres 家族），因此表单侧与 PostgreSQL 同构：
     同样需要「默认数据库」与「SSL 模式」，同样支持主机 SSH。
     默认端口 54321 是 KingbaseES V8 的安装默认值（不是 PG 的 5432）；
     默认库取 test（金仓初始化后自带的示例库），与 system 用户配套。 */
  Kingbase: {
    defaultPort: 54321,
  },
  Oracle: {
    defaultPort: 1521,
  },
  /* SQLServer 默认实例监听 1433；命名实例走 SQL Browser（UDP 1434）动态端口，
     用户按实际端口改即可，这里只给最常见的默认值。
     默认库用 master：SQL Server 必须指定初始库，留空会导致登录后无默认上下文。
     label 单独给：入库值必须是 SQLServer（后端 oneof），但给人看应写成 SQL Server，
     与列表页 dbTypeDisplayName 的展示口径一致。 */
  SQLServer: {
    label: 'SQL Server',
    defaultPort: 1433,
  },
  /* OceanBase 一个类型对应两种租户模式，模式决定后续走 MySQL 还是 Oracle 通道：
     MySQL 模式按 MySQL 协议接入（可留空默认库）；Oracle 模式按 Oracle 协议接入且服务名（= 租户名）必填。
     默认端口取 OBServer 直连口 2881；经 OBProxy 接入时通常是 2883。 */
  OceanBase: {
    defaultPort: 2881,
  },
}

// 状态
const uiState = reactive({
  loading: false,
  testing: false,
  testStatus: 'idle',
  testMessage: '',
  passwordFieldError: false,
  sshSectionCollapsed: false,
  sshAuthMode: 'password',
  sshFieldErrorMap: reactive({ user: false, port: false, password: false, key: false }),
})

const uiData = reactive({
  // label 缺省与入库值同名；只有展示写法需要区分时才显式给（如 SQLServer → SQL Server）
  dbTypes: Object.keys(DB_TYPE_CONFIG).map((key) => ({ value: key, label: DB_TYPE_CONFIG[key].label || key })),
  postgresSslModes: [
    { value: 'disable', label: 'disable' },
    { value: 'prefer', label: 'prefer' },
    { value: 'require', label: 'require' },
  ],
  // 录入型画像字段的选项：值是入库原值，标签是给人看的
  deployProviders: [
    { value: 'self_built', label: '自建' },
    { value: 'cloud', label: '云' },
  ],
  cloudVendors: [
    { value: '阿里云', label: '阿里云' },
    { value: '华为云', label: '华为云' },
    { value: '腾讯云', label: '腾讯云' },
    { value: 'AWS', label: 'AWS' },
    { value: '其他', label: '其他' },
  ],
  haModes: [
    { value: '无', label: '无' },
    { value: 'MHA', label: 'MHA' },
    { value: 'MGR', label: 'MGR' },
    { value: 'Orchestrator', label: 'Orchestrator' },
    { value: 'PXC', label: 'PXC' },
    { value: '云高可用版', label: '云高可用版' },
    { value: '共享存储', label: '共享存储' },
  ],
})

// 主机 SSH 端口的默认值：表单会自动填上它，因此「端口 == 22」不能当作
// 「用户启用过 SSH」的证据（见 sshBlockProvided）。
const DEFAULT_SSH_PORT = 22

// OceanBase 双模式判定：只认 extra_config.oceanbase_mode 的归一化结果，
// 与后端 dbfamily.OceanBaseMode 同一口径，避免「前端按 A 通道提交、后端按 B 通道连接」。
const isOceanBase = computed(() => isOceanBaseType(formData.value?.db_type))
// PG 协议族：PostgreSQL 与金仓 KingbaseES。两者在连接参数（库名/SSL）与表单结构上完全一致，
// 只有品牌、默认端口与默认库不同。所有「PG 专属字段」的显隐都走这里，避免逐处写类型枚举。
const isPgProtocolType = computed(() =>
  ['PostgreSQL', 'Kingbase'].includes(formData.value?.db_type),
)
const oceanbaseMode = computed(() => normalizeOceanBaseMode(formData.value?.oceanbase_mode))
const isOceanBaseOracleMode = computed(() => isOceanBase.value && oceanbaseMode.value === OCEANBASE_ORACLE)
// 默认数据库：PostgreSQL / Kingbase / SQLServer 用它作为连接库；OceanBase MySQL 模式用它记录租户的默认库（选填，可留空）。
// OceanBase Oracle 模式与 Oracle 没有「库」的概念，因此不显示该字段。
const showsDatabaseField = computed(
  () =>
    isPgProtocolType.value ||
    formData.value?.db_type === 'SQLServer' ||
    (isOceanBase.value && !isOceanBaseOracleMode.value),
)
// 服务名：Oracle 与 OceanBase(Oracle 模式) 共用同一个字段（后端 extra_config.service_name 同一份语义）
const showsServiceNameField = computed(
  () => formData.value?.db_type === 'Oracle' || isOceanBaseOracleMode.value,
)
const serviceNamePlaceholder = computed(() =>
  isOceanBaseOracleMode.value ? '例如 oboracle（租户名）' : '例如 XEPDB1 / ORCL',
)
// 「专属配置」分组（默认数据库 / SSL 模式 / 服务名）只要有一个子项会渲染才出现。
// 早前这里用的是类型白名单，MySQL / TiDB / Doris 三个子项都不满足时会留下一个
// 空 a-row 造成的 8px 空白；改为按实际子项判定后空白消失，OceanBase MySQL 模式同理。
const showsOptionalSection = computed(() => showsDatabaseField.value || showsServiceNameField.value)
// 主机 SSH 用于备份/恢复与总览的「主机登录」终端：平台暂不支持 OceanBase 备份，因此不为它开放该区块。
// Oracle / SQL Server 于 2026-10-25 放开：两者都常跑在需要登主机排查的环境上
// （Oracle 的 ADR / 监听日志、SQL Server 的 ERRORLOG 与作业历史）。
// 🔴 口径必须与后端 host_login.go 的 hostLoginSupportedDBTypes 保持一致，
//    且 normalizeInstanceExtraConfig 对应分支必须调用 mergeSSHFieldsIntoExtraConfig，
//    否则表单填完保存后 ssh_* 会被静默丢弃 —— 入口开了但凭据永远存不下来。
const showsSSHSection = computed(() =>
  ['MySQL', 'PostgreSQL', 'Kingbase', 'TiDB', 'Doris', 'Oracle', 'SQLServer'].includes(formData.value?.db_type),
)
// Windows 高频部署形态（SQL Server 几乎必是，Oracle 也常见）：主机默认没有 SSH 服务，
// 需要先在主机上安装 OpenSSH Server，SSH 账号是 Windows 系统账号（Administrator 等）而非
// 数据库账号，root 在 Windows 上不存在 —— 表单里明确提示，避免「填了 root 连不上」。
const showsWindowsSSHHint = computed(() => ['SQLServer', 'Oracle'].includes(formData.value?.db_type))
const sshHasFieldError = computed(() => {
  const m = uiState.sshFieldErrorMap
  return !!(m.user || m.port || m.password || m.key)
})
// 主机 SSH 是选填：整块留空时按「未启用 SSH」处理 —— 不触发任何 SSH 校验、也不往
// extra_config 里写 ssh_*。判断口径：
//   - ssh_user / ssh_password / ssh_private_key 任一有内容 => 用户启用了 SSH；
//   - 只看「端口」不算：ssh_port 由 applyTypeDefaults 自动填 22，端口留在 22 无法区分
//     「默认值」与「用户真填了 22」，因此只有被改成非 22 才视为用户主动配置了 SSH。
const sshBlockProvided = computed(() => {
  const data = formData.value || {}
  const user = String(data.ssh_user || '').trim()
  const password = String(data.ssh_password || '').trim()
  const privateKey = String(data.ssh_private_key || '').trim()
  if (user || password || privateKey) return true
  const port = Number(data.ssh_port)
  return Number.isFinite(port) && port > 0 && port !== DEFAULT_SSH_PORT
})
const databaseFieldLabel = computed(() => '默认数据库')
// 占位提示按类型区分：PostgreSQL 默认库 postgres；SQLServer 默认库 master；
// OceanBase MySQL 模式的默认库是选填的
const databaseFieldPlaceholder = computed(() => {
  if (isOceanBase.value) return '选填，例如 oceanbase'
  if (formData.value?.db_type === 'Kingbase') return '默认 test'
  if (formData.value?.db_type === 'SQLServer') return '默认 master'
  return '默认 postgres'
})

const toggleSSHSection = () => {
  if (sshHasFieldError.value) {
    uiState.sshSectionCollapsed = false
    return
  }
  uiState.sshSectionCollapsed = !uiState.sshSectionCollapsed
}

const switchSSHAuthMode = async (next) => {
  if (uiState.sshAuthMode === next) return
  const curPassword = (formData.value?.ssh_password || '').toString()
  const curKey = (formData.value?.ssh_private_key || '').toString().trim()
  const sourceLabel = uiState.sshAuthMode === 'password' ? 'SSH 密码' : 'SSH 私钥'
  const sourceValue = uiState.sshAuthMode === 'password' ? curPassword : curKey
  const hasSource = !!sourceValue
  const targetEmpty = next === 'password' ? !curPassword : !curKey
  if (hasSource && targetEmpty) {
    let ok = false
    try {
      await new Promise((resolve) => {
        Modal.confirm({
          title: '切换凭据模式',
          content: `切换后已填写的「${sourceLabel}」将被清空，是否继续？`,
          okText: '继续切换',
          cancelText: '取消',
          okButtonProps: { danger: false },
          onOk: () => resolve(true),
          onCancel: () => resolve(false),
        })
      }).then((res) => { ok = res })
    } catch {
      ok = false
    }
    if (!ok) return
  }
  if (next === 'password') {
    uiState.sshAuthMode = 'password'
    formData.value.ssh_private_key = ''
  } else {
    uiState.sshAuthMode = 'key'
    formData.value.ssh_password = ''
  }
  nextTick(() => {
    try { formRef.value?.clearValidate?.(['ssh_password', 'ssh_private_key']) } catch {
      // 表单对象不存在时忽略
    }
  })
}

const HOST_RE = /^[A-Za-z0-9.-]+$/

const DATABASE_NAME_LIMIT = 60
let databaseLimitTimer = null
const onDatabaseInput = (event) => {
  const raw = String(event?.target?.value ?? formData.value?.database ?? '')
  if (raw.length > DATABASE_NAME_LIMIT) {
    formData.value.database = raw.slice(0, DATABASE_NAME_LIMIT)
    try {
      if (databaseLimitTimer) return
      databaseLimitTimer = setTimeout(() => {
        try {
          message.warning(`数据库名称不能超过 ${DATABASE_NAME_LIMIT} 个字符`)
        } finally {
          databaseLimitTimer = null
        }
      }, 120)
    } catch {
      // setTimeout 清理异常忽略
    }
  }
}

/* ===== ip:port 唯一性（复制 / 新增时的即时预校验）=====
   口径与后端保持一致：只比 hostname + port，其余字段（名称、账号、环境、库类型…）一律不参与冲突判断。
   后端 AdminCreateInstancesService / AdminUpdateInstancesService 与库上 uniq_hostname 索引仍是最终权威校验，
   这里只是把冲突提前到「字段下方」，避免用户点了确定才被后端驳回。 */
const normalizeAddressKey = (hostname, port) => {
  const host = String(hostname || '').trim().toLowerCase()
  const parsedPort = Number(port)
  if (!host || !Number.isFinite(parsedPort) || parsedPort <= 0) return ''
  return `${host}:${parsedPort}`
}

const instanceDisplayLabel = (item) => {
  if (!item) return ''
  const label = String(item.remark || '').trim() || String(item.data_source_id || '').trim()
  if (label) return label
  return item.id !== null && item.id !== undefined ? `#${item.id}` : ''
}

const isCopySourceItem = (item) =>
  isCopyMode.value && !!item && String(item.id) === String(props.copySourceId)

const findAddressConflict = (hostname, port) => {
  const key = normalizeAddressKey(hostname, port)
  if (!key) return null
  // 编辑态放过「自己」，否则不改地址也会被自身拦住
  const selfId = isEditMode.value ? formData.value?.id : null
  for (const item of props.existingAddresses || []) {
    if (selfId !== null && selfId !== undefined && String(item.id) === String(selfId)) continue
    if (normalizeAddressKey(item?.hostname, item?.port) === key) return item
  }
  return null
}

const addressConflictMessage = (item) => {
  const label = instanceDisplayLabel(item) || '未知实例'
  const address = `${String(item?.hostname || '').trim()}:${item?.port}`
  if (isCopySourceItem(item)) {
    return `该连接地址与源实例「${label}」（${address}）相同，请修改 IP 或端口后再保存`
  }
  return `该连接地址已被实例「${label}」（${address}）占用，请更换 IP 或端口`
}

// 表单校验规则（需响应式：编辑/新增切换时 password 是否必填不同）
const rules = computed(() => ({
  environment: [{ required: true, message: '请选择所属环境', trigger: 'change' }],
  db_type: [{ required: true, message: '请选择数据库类型', trigger: 'change' }],
  connection_address: [
    { required: true, message: '请输入连接地址', trigger: ['blur', 'change'] },
    {
      validator: (_, value) => {
        const raw = String(value || '').trim()
        if (!raw) {
          return Promise.resolve()
        }
        const parsed = parseConnectionAddress(value)
        if (!parsed.ok) {
          return Promise.reject(new Error(parsed.message))
        }
        return Promise.resolve()
      },
      trigger: ['blur', 'change'],
    },
    {
      // ip:port 唯一性：格式不合法时交给上一条规则报错，这里只在格式 OK 时才判冲突，
      // 避免同一个框里同时冒出「格式错误」和「地址冲突」两句话。
      validator: (_, value) => {
        const parsed = parseConnectionAddress(value)
        if (!parsed.ok) {
          return Promise.resolve()
        }
        const conflict = findAddressConflict(parsed.hostname, parsed.port)
        if (conflict) {
          return Promise.reject(new Error(addressConflictMessage(conflict)))
        }
        return Promise.resolve()
      },
      trigger: ['blur', 'change'],
    },
  ],
  user: [
    { required: props.canManage, message: '请输入登录账号', trigger: 'blur' },
    {
      validator: (_, value) => {
        const v = (value || '').trim()
        if (!v) {
          return Promise.resolve()
        }
        if (v.length < 1 || v.length > 128) {
          return Promise.reject(new Error('登录账号长度需在 1 到 128 个字符之间'))
        }
        if (/[\r\n\t]/.test(v)) {
          return Promise.reject(new Error('登录账号不能包含换行或制表符'))
        }
        return Promise.resolve()
      },
      trigger: 'blur',
    },
  ],
  password: [
    // 新增态必须输入密码；编辑态允许留空（留空表示不修改密码）
    ...(!isEditMode.value
      ? [
          {
            required: props.canManage,
            message: '请输入访问数据库的密码',
            trigger: ['change', 'blur'],
          },
        ]
      : []),
    {
      validator: (_, value) => {
        // 编辑态：不改密码则允许留空
        if (isEditMode.value && (!value || !(value + '').trim())) {
          return Promise.resolve()
        }
        const trimmed = (value || '').toString().trim()
        if (!trimmed) {
          return Promise.resolve()
        }
        const v = String(value || '')
        if (v.length < 1 || v.length > 256) {
          return Promise.reject(new Error('密码长度需在 1 到 256 个字符之间'))
        }
        if (/[\r\n\t]/.test(v)) {
          return Promise.reject(new Error('密码不能包含换行或制表符'))
        }
        return Promise.resolve()
      },
      trigger: ['change', 'blur'],
    },
  ],
  database: [
    {
      validator: (_, value) => {
        if (!showsDatabaseField.value) return Promise.resolve()
        const v = (value || '').trim()
        if (!v) return Promise.resolve()
        if (v.length > DATABASE_NAME_LIMIT) {
          return Promise.reject(new Error(`默认数据库不能超过 ${DATABASE_NAME_LIMIT} 个字符`))
        }
        return Promise.resolve()
      },
      trigger: ['change', 'blur'],
    },
  ],
  sslmode: [
    {
      validator: (_, value) => {
        if (!isPgProtocolType.value) return Promise.resolve()
        if (!value) return Promise.resolve()
        return Promise.resolve()
      },
      trigger: 'change',
    },
  ],
  service_name: [
    {
      validator: (_, value) => {
        // Oracle 与 OceanBase(Oracle 模式) 共用该字段：都要求服务名（OceanBase 下即租户名）
        if (!showsServiceNameField.value) return Promise.resolve()
        const v = (value || '').trim()
        if (!v) {
          return Promise.reject(
            new Error(isOceanBaseOracleMode.value ? '请输入 OceanBase 服务名（租户名）' : '请输入 Oracle 服务名'),
          )
        }
        if (v.length > 128) {
          return Promise.reject(new Error('服务名不能超过 128 个字符'))
        }
        return Promise.resolve()
      },
      trigger: ['change', 'blur'],
    },
  ],
  ssh_user: [
    {
      validator: (_, value) => {
        if (!showsSSHSection.value) { uiState.sshFieldErrorMap.user = false; return Promise.resolve() }
        const v = (value || '').trim()
        // SSH 是选填：监控采集全程不使用 SSH（服务端直连数据库 + Prometheus 拉主机指标），
        // 只有备份/恢复需要。此前在新增态强制必填，逼得"只想接入监控"的用户
        // 必须先开通主机 SSH 才能保存实例。留空时由后端在测试连接里给出提示。
        if (!v) { uiState.sshFieldErrorMap.user = false; return Promise.resolve() }
        if (v.length > 128) {
          uiState.sshFieldErrorMap.user = true
          return Promise.reject(new Error('SSH 用户不能超过 128 个字符'))
        }
        if (/[\r\n\t]/.test(v)) {
          uiState.sshFieldErrorMap.user = true
          return Promise.reject(new Error('SSH 用户不能包含换行或制表符'))
        }
        uiState.sshFieldErrorMap.user = false
        return Promise.resolve()
      },
      trigger: 'blur',
    },
  ],
  ssh_port: [
    {
      validator: (_, value) => {
        if (!showsSSHSection.value) { uiState.sshFieldErrorMap.port = false; return Promise.resolve() }
        // 同 ssh_user：SSH 整体选填，端口也随之不再必填。
        if (value === null || value === undefined || value === '') { uiState.sshFieldErrorMap.port = false; return Promise.resolve() }
        const num = Number(value)
        if (!Number.isFinite(num) || !Number.isInteger(num)) {
          uiState.sshFieldErrorMap.port = true
          return Promise.reject(new Error('SSH 端口号必须为整数'))
        }
        if (num < 1 || num > 65535) {
          uiState.sshFieldErrorMap.port = true
          return Promise.reject(new Error('SSH 端口号范围为 1-65535'))
        }
        uiState.sshFieldErrorMap.port = false
        return Promise.resolve()
      },
      trigger: 'change',
    },
  ],
  ssh_password: [
    {
      validator: (_, value) => {
        if (!showsSSHSection.value) { uiState.sshFieldErrorMap.password = false; return Promise.resolve() }
        const v = (value || '').toString()
        // 主机 SSH 是选填：留空 = 未启用 SSH，直接放行。
        // 这里不再强制「与 SSH 私钥二选一」——整块留空时那条规则会把「只想接入监控」的用户拦死
        // （前端报错、后端也报错，表现为点确定没反应）。凭据完整性交给「测试连接」的
        // SSH 探测结果与备份侧预检提示，不阻断纳管流程。
        if (!v.trim()) {
          uiState.sshFieldErrorMap.password = false
          return Promise.resolve()
        }
        if (v.length > 1024) {
          uiState.sshFieldErrorMap.password = true
          return Promise.reject(new Error('SSH 密码不能超过 1024 个字符'))
        }
        uiState.sshFieldErrorMap.password = false
        return Promise.resolve()
      },
      trigger: ['change', 'blur'],
    },
  ],
  ssh_private_key: [
    {
      validator: (_, value) => {
        if (!showsSSHSection.value) { uiState.sshFieldErrorMap.key = false; return Promise.resolve() }
        const v = (value || '').toString()
        // 同 ssh_password：留空即未启用 SSH，放行；只对已填内容做格式校验。
        if (!v.trim()) {
          uiState.sshFieldErrorMap.key = false
          return Promise.resolve()
        }
        if (v.length > 8192) {
          uiState.sshFieldErrorMap.key = true
          return Promise.reject(new Error('SSH 私钥不能超过 8192 个字符'))
        }
        uiState.sshFieldErrorMap.key = false
        return Promise.resolve()
      },
      trigger: ['change', 'blur'],
    },
  ],
  remark: [
    { required: props.canManage, message: '请输入数据源名称', trigger: ['blur', 'change'] },
    {
      validator: (_, value) => {
        const v = (value || '').trim()
        if (!v) {
          return Promise.resolve()
        }
        if (v.length < 3 || v.length > 256) {
          return Promise.reject(new Error('数据源名称长度需在 3 到 256 个字符之间'))
        }
        return Promise.resolve()
      },
      trigger: 'blur',
    },
  ],
}))

const getDefaultPort = (dbType) => DB_TYPE_CONFIG[dbType]?.defaultPort
const parseConnectionAddress = (raw) => {
  const value = String(raw || '').trim()
  if (!value) {
    return { ok: false, message: '请输入连接地址' }
  }
  const separatorIndex = value.lastIndexOf(':')
  if (separatorIndex <= 0 || separatorIndex === value.length - 1) {
    return { ok: false, message: '请输入正确的连接地址，格式为 IP:端口' }
  }
  const hostname = value.slice(0, separatorIndex).trim()
  const portText = value.slice(separatorIndex + 1).trim()
  if (hostname.length < 3 || hostname.length > 255) {
    return { ok: false, message: '主机名长度需在 3 到 255 个字符之间' }
  }
  if (!HOST_RE.test(hostname)) {
    return { ok: false, message: '连接地址中的主机名仅支持字母、数字、点(.)、短横线(-)' }
  }
  if (!/^\d+$/.test(portText)) {
    return { ok: false, message: '连接地址中的端口号必须为整数' }
  }
  const port = Number(portText)
  if (port < 1 || port > 65535) {
    return { ok: false, message: '连接地址中的端口号范围为 1-65535' }
  }
  return { ok: true, hostname, port }
}

const syncConnectionAddress = () => {
  const hostname = (formData.value?.hostname || '').trim()
  const port = formData.value?.port
  if (!hostname && !port) {
    formData.value.connection_address = ''
    return
  }
  formData.value.connection_address = port ? `${hostname}:${port}` : hostname
}

const applyConnectionAddressToFields = () => {
  const parsed = parseConnectionAddress(formData.value?.connection_address)
  if (!parsed.ok) return parsed
  formData.value.hostname = parsed.hostname
  formData.value.port = parsed.port
  formData.value.connection_address = `${parsed.hostname}:${parsed.port}`
  return parsed
}

const safeParseExtraConfig = (raw) => {
  if (!raw) return {}
  if (typeof raw === 'string') {
    try {
      return JSON.parse(raw)
    } catch {
      return {}
    }
  }
  if (typeof raw === 'object') return raw
  return {}
}

const applyTypeDefaults = (newType, oldType) => {
  const nextPort = getDefaultPort(newType)
  const prevPort = getDefaultPort(oldType)
  if (nextPort && (!formData.value?.port || formData.value.port === prevPort)) {
    formData.value.port = nextPort
    syncConnectionAddress()
  }
  if (newType === 'PostgreSQL' || newType === 'Kingbase') {
    // 两者的默认库不同：PG 是 postgres，金仓初始化后自带的是 test。
    // 互切时也要换掉「上一个类型的默认库」——否则 PG → 金仓会残留 postgres，
    // 连上去直接报「database "postgres" does not exist」。
    const nextDefaultDB = newType === 'Kingbase' ? 'test' : 'postgres'
    const prevDefaultDB = oldType === 'Kingbase' ? 'test' : oldType === 'PostgreSQL' ? 'postgres' : ''
    if (!formData.value.database || formData.value.database === prevDefaultDB) {
      formData.value.database = nextDefaultDB
    }
    if (!formData.value.sslmode) formData.value.sslmode = 'disable'
  } else if (newType === 'SQLServer') {
    // SQL Server 登录必须有默认库上下文；默认给 master，用户可改成业务库。
    if (!formData.value.database) formData.value.database = 'master'
  } else if (oldType === 'PostgreSQL' && formData.value.database === 'postgres') {
    // 离开 PostgreSQL：清掉自动填入的 postgres，避免它被当成其他类型的默认库提交
    // （例如 OceanBase MySQL 模式的「默认数据库」字段会读到这个残留值）
    formData.value.database = ''
  } else if (oldType === 'Kingbase' && formData.value.database === 'test') {
    // 同理：test 是金仓的默认库，切到别的类型时不能残留
    formData.value.database = ''
  } else if (oldType === 'SQLServer' && formData.value.database === 'master') {
    // 同理：master 只属于 SQLServer，切到别的类型时不能残留。
    formData.value.database = ''
  }
  if (newType === 'OceanBase') {
    // 模式必须有值：缺失/非法一律归一化到 mysql，与后端 OceanBaseMode 的兜底一致，
    // 避免用户没动过模式就提交、后端判成 MySQL 模式而前端按别的语义展示。
    formData.value.oceanbase_mode = normalizeOceanBaseMode(formData.value.oceanbase_mode)
  }
  if (showsSSHSection.value && !formData.value.ssh_port) {
    formData.value.ssh_port = DEFAULT_SSH_PORT
  }
}

// 切换租户模式：两种模式用的是不同的专属字段（Oracle 模式读服务名、MySQL 模式读默认库），
// 因此切换时清掉不属于新模式的那个值，避免「MySQL 模式下留着上一次的租户名」这类脏数据被提交。
const handleOceanBaseModeChange = (event) => {
  const next = normalizeOceanBaseMode(event?.target?.value ?? formData.value?.oceanbase_mode)
  if (formData.value.oceanbase_mode === next) return
  formData.value.oceanbase_mode = next
  if (next === 'oracle') {
    formData.value.database = ''
  } else {
    formData.value.service_name = ''
  }
  nextTick(() => {
    try {
      formRef.value?.clearValidate?.(['service_name', 'database'])
    } catch {
      // 表单实例尚未就绪时忽略
    }
  })
}

const hydrateTypeSpecificFields = () => {
  const cfg = safeParseExtraConfig(formData.value?.extra_config)
  formData.value.database = cfg.database || ''
  // SQLServer 的默认库是必填语义（后端落库兜底 master）：老数据若没写这一项，
  // 编辑态直接显示空会让用户以为要重新填，这里按同一口径补上。
  if (formData.value?.db_type === 'SQLServer' && !formData.value.database) {
    formData.value.database = 'master'
  }
  formData.value.sslmode = cfg.sslmode || ''
  formData.value.auth_source = cfg.auth_source || ''
  formData.value.service_name = cfg.service_name || ''
  // OceanBase 租户模式必须回显：编辑态若丢了它，后端 mergeKeepOld 会保留旧值看起来"没坏"，
  // 但前端提示与服务名字段显隐会与真实模式错位（如 Oracle 模式的实例不显示服务名输入框）。
  formData.value.oceanbase_mode = normalizeOceanBaseMode(cfg.oceanbase_mode)
  formData.value.ssh_user = cfg.ssh_user || ''
  formData.value.ssh_port = Number(cfg.ssh_port) || DEFAULT_SSH_PORT
  if (isEditMode.value) {
    formData.value.ssh_password = ''
    formData.value.ssh_private_key = (cfg.ssh_private_key && typeof cfg.ssh_private_key === 'string' && cfg.ssh_private_key.trim()) ? cfg.ssh_private_key : ''
    uiState.sshAuthMode = formData.value.ssh_private_key ? 'key' : 'password'
  } else {
    formData.value.ssh_password = ''
    formData.value.ssh_private_key = ''
    uiState.sshAuthMode = 'password'
  }
  uiState.sshSectionCollapsed = false
  uiState.sshFieldErrorMap.user = false
  uiState.sshFieldErrorMap.port = false
  uiState.sshFieldErrorMap.password = false
  uiState.sshFieldErrorMap.key = false
  applyTypeDefaults(formData.value?.db_type, '')
  syncConnectionAddress()
}

const buildExtraConfigPayload = () => {
  const mergeSSH = (cfg = {}) => {
    // 整块留空 = 未启用主机 SSH：一个 ssh_* 字段都不写，
    // 避免落库一条「有端口没用户」的半截配置。
    // 编辑态不写也不是问题：后端 AdminUpdateInstancesService 有 mergeKeepOld 会保留旧值。
    if (!sshBlockProvided.value) return cfg
    const sshUser = (formData.value?.ssh_user || '').trim()
    if (sshUser) cfg.ssh_user = sshUser
    const sshPort = Number(formData.value?.ssh_port) || 0
    if (sshPort > 0) cfg.ssh_port = sshPort
    const sshPwd = formData.value?.ssh_password || ''
    if (sshPwd) cfg.ssh_password = sshPwd
    const sshKey = (formData.value?.ssh_private_key || '').trim()
    if (sshKey) cfg.ssh_private_key = sshKey
    return cfg
  }
  switch (formData.value?.db_type) {
    case 'PostgreSQL':
    case 'Kingbase': {
      // 两者 extra_config 结构一致（database + sslmode + SSH），后端 normalizeInstanceExtraConfig
      // 也是同一分支；默认库兜底值不同（PG=postgres，金仓=test）。
      const defaultDB = formData.value?.db_type === 'Kingbase' ? 'test' : 'postgres'
      const cfg = {
        database: (formData.value?.database || defaultDB).trim(),
        sslmode: formData.value?.sslmode || 'disable',
      }
      return mergeSSH(cfg)
    }
    case 'MySQL':
    case 'TiDB':
    case 'Doris': {
      return mergeSSH({})
    }
    case 'SQLServer': {
      // 默认库必须有值：后端 normalizeInstanceExtraConfig 也会兜底成 master，
      // 前后端同一口径，避免出现"界面显示空、库里其实是 master"的错觉。
      const database = (formData.value?.database || '').trim() || 'master'
      // 主机登录放开后这两个引擎也要带 SSH：不 merge 的话表单填了也提交不上去，
      // 表现为「保存成功、再打开凭据是空的」，且界面上没有任何报错。
      return mergeSSH({ database })
    }
    case 'Oracle':
      return mergeSSH({
        service_name: (formData.value?.service_name || '').trim(),
      })
    case 'OceanBase': {
      // 模式必带：后端靠它决定走 MySQL 还是 Oracle 通道，缺了会回落 MySQL 模式。
      const mode = normalizeOceanBaseMode(formData.value?.oceanbase_mode)
      if (mode === 'oracle') {
        return {
          oceanbase_mode: 'oracle',
          service_name: (formData.value?.service_name || '').trim(),
        }
      }
      // MySQL 模式的默认库是选填：留空即不指定（后端按「不写 key」处理，编辑态会保留旧值）
      const database = (formData.value?.database || '').trim()
      return database
        ? { oceanbase_mode: 'mysql', database }
        : { oceanbase_mode: 'mysql' }
    }
    default:
      return showsSSHSection.value ? mergeSSH({}) : {}
  }
}

const clearTestFeedback = () => {
  uiState.testStatus = 'idle'
  uiState.testMessage = ''
  uiState.passwordFieldError = false
}

const getTestErrorMessage = (err) => {
  const messageText = err?.message || err?.data?.message || err?.msg || '连接失败'
  return String(messageText || '连接失败').trim()
}

const isPasswordAuthError = (messageText) => {
  const text = String(messageText || '').toLowerCase()
  return (
    text.includes('28p01') ||
    text.includes('password authentication failed') ||
    text.includes('password') ||
    text.includes('密码') ||
    text.includes('认证失败')
  )
}

const showConnectionSuccess = () => {
  uiState.passwordFieldError = false
  // 只有真填了 SSH 凭据才把 SSH 算进成功文案：整块留空时后端不会去探测 SSH，
  // 说「主机 SSH 验证通过」等于替用户确认了一件根本没验证的事。
  if (showsSSHSection.value && sshBlockProvided.value) {
    message.success('数据库连接与主机 SSH 验证通过')
    return
  }
  message.success('连接测试通过')
}

const showConnectionError = (err) => {
  const detail = getTestErrorMessage(err)
  uiState.passwordFieldError = isPasswordAuthError(detail)
  message.error(detail)
}

watch(
  () => formData.value?.db_type,
  (newType, oldType) => {
    if (!newType) return
    applyTypeDefaults(newType, oldType)
  },
)

// 复制态：打开弹窗后立刻在「连接地址」下方给出冲突提示（首次打开时弹窗内容可能还没挂载，故留一帧兜底）。
const validateCopyAddressIfNeeded = async () => {
  if (!isCopyMode.value) return
  await nextTick()
  if (!formRef.value?.validateFields) {
    await new Promise((resolve) => setTimeout(resolve, 80))
  }
  try {
    await formRef.value?.validateFields?.(['connection_address'])
  } catch {
    // 校验失败时 AntD 已把错误文案渲染在字段下方，这里无需再处理
  }
}

watch(
  () => props.open,
  async (open) => {
    clearTestFeedback()
    if (!open || !formData.value) return
    hydrateTypeSpecificFields()
    // 复制出来的连接地址必然与被复制实例冲突：打开即触发一次字段校验，
    // 让用户第一眼就看到「要改 IP:端口」，而不是等到点「确定」才被拦。
    await validateCopyAddressIfNeeded()
  },
)

watch(
  () => [
    props.open,
    formData.value?.db_type,
    formData.value?.connection_address,
    formData.value?.user,
    formData.value?.password,
    formData.value?.database,
    formData.value?.sslmode,
    formData.value?.auth_source,
    formData.value?.service_name,
    formData.value?.oceanbase_mode,
    formData.value?.ssh_user,
    formData.value?.ssh_port,
    formData.value?.ssh_password,
    formData.value?.ssh_private_key,
  ],
  (current, previous) => {
    if (!props.open || !previous) return
    const changed = current.some((value, index) => value !== previous[index])
    if (changed) {
      clearTestFeedback()
    }
  },
)

// 取消按钮
const handleCancel = () => {
  clearTestFeedback()
  resetCloudTab()
  emit('update:open', false)
  formRef.value?.resetFields()
}

// 表单字段名 → 中文标签，仅用于「保存被拦下」时的定位提示
const FIELD_LABELS = {
  environment: '所属环境',
  db_type: '数据库类型',
  connection_address: '连接地址',
  remark: '数据源名称',
  user: '登录账号',
  password: '密码',
  database: '默认数据库',
  sslmode: 'SSL 模式',
  service_name: '服务名',
  oceanbase_mode: '租户模式',
  ssh_user: 'SSH 用户',
  ssh_port: 'SSH 端口',
  ssh_password: 'SSH 密码',
  ssh_private_key: 'SSH 私钥',
}
const SSH_FIELD_NAMES = ['ssh_user', 'ssh_port', 'ssh_password', 'ssh_private_key']

// 校验失败时的「错误定位」：把错误字段指出来、把折叠区块展开、并滚到第一个错误项。
// 之前这里只有 console.error，用户看到的是「点了确定没反应」（尤其错误落在
// 折叠起来的「服务器主机登录」区块里时，红字和红框都看不见）。
const reportValidationFailure = async (err) => {
  const errorFields = Array.isArray(err?.errorFields) ? err.errorFields : []
  const names = errorFields
    .map((item) => (Array.isArray(item?.name) ? item.name[0] : item?.name))
    .filter((name) => name !== undefined && name !== null && name !== '')
    .map((name) => String(name))
  if (!names.length) return false

  const hitsSSH = names.some((name) => SSH_FIELD_NAMES.includes(name))
  if (hitsSSH) {
    uiState.sshSectionCollapsed = false
  }
  await nextTick()
  try {
    // a-form 自带 scrollToField：会自己找最近的可滚动祖先（弹窗主体 .ant-modal-body）
    formRef.value?.scrollToField?.(names[0], { behavior: 'smooth', block: 'center' })
  } catch {
    // 弹窗尚未挂载等场景下滚动可能失败，不影响提示本身
  }

  const labels = [...new Set(names.map((name) => FIELD_LABELS[name] || name))]
  const shown = labels.slice(0, 3).join('、')
  message.error(`保存被拦下：请先修正「${shown}」${labels.length > 3 ? ` 等 ${labels.length} 项` : ''}后再试`)
  return true
}

// 提交表单
const onSubmit = async () => {
  try {
    applyConnectionAddressToFields()
    await formRef.value.validateFields()
    uiState.loading = true

    // 编辑态如果密码留空：不提交 password 字段（语义 = 不修改密码）
    // 这里不直接改 v-model 的 formData，以免影响 UI；只在提交 payload 中剔除。
    const payload = { ...(formData.value || {}) }
    delete payload.use_type
    delete payload.connection_address
    payload.extra_config = buildExtraConfigPayload()
    delete payload.database
    delete payload.sslmode
    delete payload.auth_source
    delete payload.service_name
    // 租户模式与上面几项一样，只是「表单展示字段」，真实值已折进 extra_config
    delete payload.oceanbase_mode
    if (isEditMode.value && (!payload.password || !(String(payload.password).trim()))) {
      delete payload.password
    }

    emit('submit', payload)
  } catch (err) {
    // 表单校验失败：给出字段级定位；非校验异常（罕见）也要有可见反馈，不留「静默无响应」
    const located = await reportValidationFailure(err)
    if (!located) {
      console.error(err)
      message.error('保存失败：请检查表单内容后重试')
    }
  } finally {
    uiState.loading = false
  }
}

const handleTestConnection = async () => {
  if (uiState.testing) return
  const addressParsed = applyConnectionAddressToFields()
  if (!addressParsed?.ok) {
    try {
      await formRef.value.validateFields(['connection_address'])
    } catch {
      return
    }
  }
  const fields = ['db_type', 'connection_address', 'user']
  if (showsDatabaseField.value) {
    fields.push('database')
  }
  if (isPgProtocolType.value) {
    fields.push('sslmode')
  }
  if (showsServiceNameField.value) {
    fields.push('service_name')
  }
  const pwd = (formData.value?.password || '').trim()
  if (!isEditMode.value || pwd) {
    fields.push('password')
  }
  try {
    await formRef.value.validateFields(fields)
  } catch {
    return
  }

  uiState.testing = true
  clearTestFeedback()
  try {
    if (isEditMode.value && !pwd) {
      const id = formData.value?.id
      if (!id) {
        return
      }
      const testResult = await testInstanceConnectionApi(id, {
        silentBusinessError: true,
        silentHttpError: true,
      })
      showConnectionSuccess()
      openCheckReport(testResult?.data)
    } else {
      const payload = {
        db_type: formData.value?.db_type,
        hostname: (formData.value?.hostname || '').trim(),
        port: formData.value?.port,
        user: (formData.value?.user || '').trim(),
        password: formData.value?.password || '',
        extra_config: buildExtraConfigPayload(),
      }
      const testResult = await testInstanceConnectionByPayloadApi(payload, {
        silentBusinessError: true,
        silentHttpError: true,
      })
      showConnectionSuccess()
      openCheckReport(testResult?.data)
    }
  } catch (err) {
    showConnectionError(err)
  } finally {
    uiState.testing = false
  }
}
</script>

<style scoped>
.instance-modal-body {
  padding-top: 4px;
  display: flex;
  flex-direction: column;
}
/* 复制态顶部提示：与表单同宽，不抢视觉焦点 */
.instance-copy-hint {
  margin-bottom: 16px;
  border-radius: 8px;
}
/* 纵向滚动由弹窗主体（.ant-modal-body）承载：滚动条贴弹窗右边缘，
   内容层本身不再单独出滚动条（避免看起来是「里层容器」在滚）。 */
.instance-modal-scroll {
  flex: 1 1 auto;
  width: 100%;
}

/* ===== 服务器主机登录 Section ===== */
.host-login-section {
  margin-top: 4px;
  border: 1px solid #0f766e;
  border-radius: 8px;
  background: #fafdfc;
  transition: border-color 0.18s ease, box-shadow 0.18s ease;
  overflow: hidden;
}
.host-login-section.collapsed {
  border-color: #d9d9d9;
  background: #fff;
}
.host-login-section.has-error {
  border-color: #cf1322;
  box-shadow: 0 0 0 2px rgba(207, 19, 34, 0.06);
}
.host-login-section__header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  background: #f1f9f7;
  cursor: pointer;
  user-select: none;
  border-bottom: 1px solid transparent;
  transition: background 0.18s ease, border-color 0.18s ease;
}
.host-login-section.collapsed .host-login-section__header {
  background: #fafafa;
  border-bottom: none;
}
.host-login-section.has-error .host-login-section__header {
  background: #fff1f0;
}
.host-login-section__caret {
  color: #0f766e;
  font-size: 13px;
  flex: 0 0 auto;
}
.host-login-section.collapsed .host-login-section__caret {
  color: rgba(0, 0, 0, 0.45);
}
.host-login-section__title {
  flex: 1 1 auto;
  font-size: 13.5px;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.86);
  letter-spacing: 0.2px;
}

/* Windows 主机提示条：装 OpenSSH Server 引导 + 账号口径，浅黄底弱化呈现 */
.host-login-windows-hint {
  margin-bottom: 12px;
  padding: 8px 12px;
  border-radius: 6px;
  background: #fdf6ec;
  border: 1px solid #faecd8;
  font-size: 12.5px;
  line-height: 1.7;
  color: #8c6a1d;
}
.host-login-windows-hint code {
  padding: 0 4px;
  border-radius: 4px;
  background: rgba(0, 0, 0, 0.05);
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-size: 11.5px;
}
.host-login-section__optional {
  flex: 0 0 auto;
  color: rgba(0, 0, 0, 0.45);
  font-size: 12.5px;
  font-weight: 500;
}
.host-login-section__body {
  padding: 16px 16px 4px;
}

/* ===== 数据源名称 + 连接地址（一行两列）=====
   无边框、无底色的普通表单项分组：内外边距全部归零，
   纵向节奏完全交给内部 a-row 里 form-item 自带的 margin，
   横向沿用 a-row(gutter=16) + a-col(span=12)，
   因此尺寸、间距、左对齐与「环境/数据库类型」「登录账号/密码」等行完全一致。 */
.instance-basic-section {
  margin: 0;
  padding: 0;
  border: none;
  background: transparent;
}

/* ===== 凭据模式 Box-style 双选 ===== */
.host-login-auth__row {
  display: flex;
  gap: 12px;
  margin: 4px 0 18px;
}
.host-login-auth__box {
  flex: 1 1 0;
  min-height: 72px;
  padding: 12px 14px;
  border: 1.5px solid #d9d9d9;
  border-radius: 8px;
  background: #fff;
  cursor: pointer;
  transition: border-color 0.18s ease, background 0.18s ease, box-shadow 0.18s ease;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 4px;
}
.host-login-auth__box:hover {
  border-color: #99d1c6;
  background: #f5fbf9;
}
.host-login-auth__box.active {
  border-color: #0f766e;
  background: #f1f9f7;
  box-shadow: 0 0 0 2px rgba(15, 118, 110, 0.08);
}
.host-login-auth__title {
  font-size: 13px;
  font-weight: 600;
  color: rgba(0, 0, 0, 0.82);
}
.host-login-auth__box.active .host-login-auth__title {
  color: #0f766e;
}
.host-login-auth__sub {
  font-size: 11.5px;
  color: rgba(0, 0, 0, 0.45);
  line-height: 1.45;
}

:deep(.instance-form-modal-wrap .ant-form-item) {
  margin-bottom: 18px;
}

:deep(.instance-form-modal-wrap .ant-form-item-label > label) {
  color: rgba(0, 0, 0, 0.78);
  font-size: 13px;
  font-weight: 500;
}

:deep(.instance-form-modal-wrap .ant-input),
:deep(.instance-form-modal-wrap .ant-input-affix-wrapper),
:deep(.instance-form-modal-wrap .ant-input-number),
:deep(.instance-form-modal-wrap .ant-select-selector) {
  height: 40px;
  min-height: 40px;
  border-radius: 8px;
}

:deep(.instance-form-modal-wrap .ant-input) {
  padding-top: 0;
  padding-bottom: 0;
  line-height: 38px;
}

:deep(.instance-form-modal-wrap .ant-input-affix-wrapper) {
  padding-top: 0;
  padding-bottom: 0;
  line-height: 38px;
  box-sizing: border-box;
}

:deep(.instance-form-modal-wrap .ant-input-password.ant-input-affix-wrapper) {
  height: 40px !important;
  min-height: 40px !important;
  padding: 0 11px !important;
  line-height: 38px !important;
  border-radius: 8px !important;
  box-sizing: border-box !important;
}

:deep(.instance-form-modal-wrap .ant-input-affix-wrapper > input.ant-input) {
  height: 38px;
  line-height: 38px;
  padding-top: 0;
  padding-bottom: 0;
}

:deep(.instance-form-modal-wrap .ant-input-password.ant-input-affix-wrapper > input.ant-input) {
  height: 38px !important;
  line-height: 38px !important;
  padding: 0 !important;
}

:deep(.instance-form-modal-wrap .ant-input-password.ant-input-affix-wrapper .ant-input-suffix) {
  height: 100%;
  display: inline-flex;
  align-items: center;
}

:deep(.instance-form-modal-wrap .ant-input-number-input) {
  height: 38px;
  line-height: 38px;
}

:deep(.instance-form-modal-wrap .ant-select-single .ant-select-selector) {
  display: flex;
  align-items: center;
}

:deep(.instance-form-modal-wrap .ant-input-number) {
  width: 100%;
}

.instance-form-section {
  margin-top: 4px;
  padding-top: 4px;
}

.instance-form-section-title {
  margin-bottom: 12px;
  color: rgba(0, 0, 0, 0.72);
  font-size: 13px;
  font-weight: 600;
}

.instance-form-subsection {
  margin: 4px 0 12px;
  color: rgba(0, 0, 0, 0.68);
  font-size: 12.5px;
  font-weight: 600;
  padding-left: 2px;
}

.test-result-item {
  margin-bottom: 0;
}

.test-result-box {
  border-radius: 8px;
  padding: 12px 14px;
  border: 1px solid transparent;
}

.test-result-box.is-success {
  background: #f6ffed;
  border-color: #b7eb8f;
  color: #389e0d;
}

.test-result-box.is-error {
  background: #fff2f0;
  border-color: #ffccc7;
  color: #cf1322;
}

.test-result-title {
  font-weight: 600;
  margin-bottom: 4px;
}

.test-result-message {
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.6;
}

.instance-modal-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
}

.instance-footer-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.instance-test-btn {
  border-color: #d0d5dd;
  color: rgba(0, 0, 0, 0.72);
}

.instance-confirm-btn {
  min-width: 88px;
  border-color: #7fbfae;
  background: #e6f4ef;
  color: #0f766e;
}
.instance-confirm-btn:hover,
.instance-confirm-btn:focus {
  border-color: #66b39f !important;
  background: #d4ecdf !important;
  color: #0f766e !important;
}

.instance-import-btn {
  min-width: 88px;
  border-radius: 8px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border-color: #0f766e !important;
  color: #0f766e !important;
}

.instance-confirm-btn:hover,
.instance-confirm-btn:focus {
  border-color: #115e59;
  background: #115e59;
}

</style>

<!-- ===== 弹窗滚动层级（非 scoped）=====
     .ant-modal-wrap / .ant-modal / .ant-modal-content 由 AntD 渲染并通过 Teleport 挂到 body 下，
     不在本组件的 scoped 作用域内：scoped 里的 :deep(X) 会被编译成 [data-v-x] X，
     在 Teleport 之后的 DOM 上永远匹配不到祖先，滚动约束全部静默失效 ——
     表现就是滚动条出现在视口最右侧（.ant-modal-wrap 默认 overflow:auto 且 position:fixed），
     标题栏和底部按钮被一起滚出可视区。
     所以这一层单独用非 scoped 块处理，并用 .instance-form-modal-wrap 做命名空间避免全局污染。
-->
<style>
/* 1) wrap 关掉自带的 overflow:auto —— 它是 position:fixed; inset:0，
      一旦滚动，滚动条就贴在视口最右侧，看起来像「页面在滚」 */
.instance-form-modal-wrap.instance-modal-with-scroll {
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden !important;
}

/* 2) modal 本体限高：取消 AntD 默认的 top:100px / padding-bottom，改走 flex 纵向链 */
.instance-form-modal-wrap.instance-modal-with-scroll .ant-modal.ant-modal {
  margin: 0 auto;
  top: auto;
  padding-bottom: 0;
  display: flex;
  flex-direction: column;
  max-height: calc(100vh - 64px);
  overflow: hidden;
}

/* 3) .ant-modal 与 .ant-modal-content 之间还夹着一层无 class 的 tabindex 焦点包裹层，
      flex 高度链必须穿过它，否则 .ant-modal-body 拿不到受限高度、限高形同虚设 */
.instance-form-modal-wrap.instance-modal-with-scroll .ant-modal > div,
.instance-form-modal-wrap.instance-modal-with-scroll .ant-modal-content {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* 4) 标题栏与底部操作区不参与滚动，始终固定可见 */
.instance-form-modal-wrap.instance-modal-with-scroll .ant-modal-header,
.instance-form-modal-wrap.instance-modal-with-scroll .ant-modal-footer {
  flex: none;
}

/* 5) 唯一的滚动容器：弹窗内容区。内容少时贴着内容，内容超了才出滚动条 */
.instance-form-modal-wrap.instance-modal-with-scroll .ant-modal-body {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  /* 滚动不向外传导，避免带动父层 / 页面 */
  overscroll-behavior: contain;
  /* 预留滚动条槽位：切 tab / 展开 SSH 时内容不左右抖动 */
  scrollbar-gutter: stable;
  /* 注意：不要写 scrollbar-width:thin —— Chrome 121+ 标准属性优先，
     会把下面 ::-webkit-scrollbar 的 8px 宽度吃掉，退回 overlay（看不见滚动条） */
  scrollbar-color: rgba(15, 118, 110, 0.36) transparent;
}

.instance-form-modal-wrap.instance-modal-with-scroll .ant-modal-body::-webkit-scrollbar {
  width: 8px;
}
.instance-form-modal-wrap.instance-modal-with-scroll .ant-modal-body::-webkit-scrollbar-track {
  background: transparent;
}
.instance-form-modal-wrap.instance-modal-with-scroll .ant-modal-body::-webkit-scrollbar-thumb {
  background: rgba(15, 118, 110, 0.32);
  border-radius: 4px;
  border: 1px solid transparent;
  background-clip: padding-box;
}
.instance-form-modal-wrap.instance-modal-with-scroll .ant-modal-body::-webkit-scrollbar-thumb:hover {
  background: #0f766e;
}
</style>
