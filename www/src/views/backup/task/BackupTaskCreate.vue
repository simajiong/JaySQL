<template>
  <div class="gi-page-shell backup-task-create-page" :class="{ 'is-precheck-step': currentStep === 3 }">
    <a-card class="create-steps-card" :bordered="false">
      <a-steps :current="currentStep" size="small">
        <a-step v-for="item in stepTitles" :key="item" :title="item" />
      </a-steps>
    </a-card>

    <div class="create-body">
      <a-card class="create-main-card" :bordered="false">
        <div v-if="currentStep === 0" class="step-panel">
          <div class="step1-layout-grid">
            <div class="step1-col step1-col-left">
              <div class="section-block section-block-fill task-basic-info-block">
                <div class="section-title">任务基础信息</div>
                <a-form layout="vertical" class="step-form-compact">
                  <a-form-item label="任务名称" required>
                    <a-input
                      v-model:value="formData.name"
                      maxlength="128"
                      placeholder="请输入任务名称"
                      @input="handleNameInput"
                    />
                  </a-form-item>

                  <a-form-item label="数据源" required>
                    <div class="instance-selector-row">
                      <a-select
                        v-model:value="formData.source_instance_id"
                        class="instance-selector"
                        :loading="uiState.instancesLoading"
                        :options="instances"
                        :field-names="{ label: 'displayLabel', value: 'value' }"
                        :filter-option="filterInstanceOption"
                        show-search
                        allowClear
                        dropdown-class-name="instance-select-dropdown--two-line"
                        placeholder="按 IP、备注、数据库类型搜索实例"
                        @change="handleInstanceChange"
                      >
                        <template #option="{ ellipsisRemark, hostPort, fullRemark, longLabel, value }">
                          <div class="instance-select-option">
                            <div class="instance-select-option__name" :title="fullRemark || longLabel">
                              {{ ellipsisRemark }}
                            </div>
                            <div class="instance-select-option__addr" :title="hostPort">
                              {{ hostPort }}
                            </div>
                          </div>
                        </template>
                      </a-select>
                      <a-button :loading="uiState.instancesLoading" @click="loadInstances">刷新</a-button>
                    </div>
                    <a-alert
                      v-if="instanceUnavailable"
                      class="instance-unavailable-alert"
                      type="error"
                      show-icon
                    >
                      <template #message>实例连接失败，无法创建任务</template>
                      <template #description>
                        <a-collapse v-model:activeKey="uiState.instanceErrorPanels" ghost>
                          <a-collapse-panel key="detail" header="查看原始错误">
                            <pre class="instance-unavailable-pre">{{ instanceUnavailableDetail || '暂无错误详情' }}</pre>
                          </a-collapse-panel>
                        </a-collapse>
                      </template>
                      <template #action>
                        <a-space>
                          <a-button size="small" :loading="uiState.instanceChecking" @click="retryInstanceCheck">重新检测</a-button>
                          <a-button size="small" @click="copyInstanceError">复制错误</a-button>
                          <a-button size="small" type="link" @click="editInstanceConnection">编辑连接信息</a-button>
                        </a-space>
                      </template>
                    </a-alert>
                  </a-form-item>

                  <a-row :gutter="20" class="driver-and-backup-type-row">
                    <a-col :xs="24" :md="8" class="driver-col-35">
                      <a-form-item label="备份类型" required>
                        <div class="selection-card-group">
                          <button
                            type="button"
                            class="selection-card"
                            :class="{ active: formData.driver_type === 'LOGICAL' }"
                            @click="selectDriverType('LOGICAL')"
                          >
                            逻辑备份
                          </button>
                          <button
                            type="button"
                            class="selection-card"
                            :class="{ active: isPhysicalDriver, disabled: physicalDriverBlocked }"
                            :disabled="physicalDriverBlocked"
                            @click="selectDriverType(physicalDriverType)"
                          >
                            物理备份
                          </button>
                        </div>
                      </a-form-item>
                    </a-col>
                    <a-col :xs="24" :md="16" class="driver-col-65">
                      <a-form-item
                        label="备份对象"
                        required
                        :class="{ 'selection-form-item-disabled': !isLogicalDriver }"
                      >
                        <div class="selection-card-group">
                          <button
                            type="button"
                            class="selection-card"
                            :class="{ active: logicalBackupIncludesSchema, disabled: !isLogicalDriver }"
                            :disabled="!isLogicalDriver"
                            @click="toggleLogicalBackupPart('SCHEMA')"
                          >
                            表结构
                          </button>
                          <button
                            type="button"
                            class="selection-card"
                            :class="{ active: logicalBackupIncludesData, disabled: !isLogicalDriver }"
                            :disabled="!isLogicalDriver"
                            @click="toggleLogicalBackupPart('DATA')"
                          >
                            数据
                          </button>
                        </div>
                      </a-form-item>
                    </a-col>
                  </a-row>
                  <div v-if="!isLogicalDriver" class="physical-strategy-section">
                    <a-form-item label="备份策略">
                      <div class="selection-card-group">
                        <button
                          type="button"
                          class="selection-card"
                          :class="{ active: formData.driver_config.strategy_mode === 'MIXED' }"
                          @click="formData.driver_config.strategy_mode = 'MIXED'"
                        >
                        全量 + 增量（推荐）
                        </button>
                        <button
                          type="button"
                          class="selection-card"
                          :class="{ active: formData.driver_config.strategy_mode === 'SINGLE' }"
                          @click="(() => { formData.driver_config.strategy_mode = 'SINGLE'; formData.backup_type = 'FULL' })()"
                        >
                        全量
                        </button>
                      </div>
                    </a-form-item>
                  </div>
                </a-form>
              </div>
            </div>

            <div class="step1-col step1-col-right">
              <div class="section-block section-block-sticky section-block-fill">
                <div class="section-title">备份参数</div>
                <a-form
                  layout="vertical"
                  class="step-form-compact storage-target-form"
                  :class="{ 'is-logical': !isPhysicalDriver }"
                >
                  <a-form-item label="备份存储目标" required>
                    <a-select
                      v-model:value="formData.storage_target_id"
                      :options="storageTargetOptions"
                      :field-names="{ label: 'label', value: 'value' }"
                      :loading="uiState.storageTargetsLoading"
                      allow-clear
                      placeholder="选择存储目标（备份中心机），备份完成后文件直接写入此处"
                    >
                      <template #option="{ raw, disabled, statusColor, statusLabel, label, healthy }">
                        <div class="storage-target-option">
                          <span v-if="disabled" class="storage-target-option__lock">🔒</span>
                          <span class="storage-target-option__main">{{ raw?.name || label }}</span>
                          <a-tag v-if="!healthy" :color="statusColor" style="margin-left: auto">{{ statusLabel }}</a-tag>
                        </div>
                        <div class="storage-target-option__sub muted">
                          <template v-if="String(raw?.type || '').toUpperCase() === 'LOCAL_SERVER'">
                            SSH：{{ raw?.ssh_user || '-' }}@{{ raw?.host || '-' }}:{{ raw?.port || '-' }}{{ raw?.base_path || '' }}
                          </template>
                          <template v-else>
                            {{ raw?.s3_endpoint || '' }} / {{ raw?.s3_bucket || '' }}
                          </template>
                        </div>
                      </template>
                    </a-select>
                    <a-button type="link" size="small" @click="router.push('/backup/storage_targets')">
                      管理存储目标
                    </a-button>
                  </a-form-item>

                  <a-form-item label="备份目录">
                    <a-input
                      v-model:value="formData.output_dir"
                      :placeholder="isPhysicalDriver ? physicalOutputDirPlaceholder : '填写相对于存储目标备份根路径下的子目录，留空则按实例/任务/批次自动建子目录'"
                    />
                  </a-form-item>
                  <a-collapse v-model:activeKey="advancedOpen" ghost class="advanced-options-collapse">
                    <a-collapse-panel key="advanced" header="高级选项">
                  <a-form-item label="并行数">
                    <a-input-number v-model:value="formData.driver_config.process_max" :min="1" :max="64" style="width: 220px" />
                  </a-form-item>
                  <a-form-item label="单次执行最长运行时间">
                    <a-input-number
                      v-model:value="formData.driver_config.max_runtime_hours"
                      :min="1"
                      :max="720"
                      :precision="0"
                      addon-after="小时"
                      placeholder="默认 12"
                      style="width: 220px"
                    />
                  </a-form-item>
                  <template v-if="isLogicalDriver">
                    <a-form-item label="数据库操作系统用户">
                      <a-input
                        v-model:value="formData.driver_config.os_user"
                        placeholder="例如 postgres / mysql；填写后执行与预检查优先使用此用户"
                      />
                    </a-form-item>
                    <a-form-item :label="logicalToolPathLabel">
                      <a-input
                        v-model:value="formData.driver_config.tool_path"
                        :placeholder="logicalToolPathPlaceholder"
                      />
                    </a-form-item>
                    <a-form-item label="压缩策略">
                      <div style="display: flex; align-items: center; gap: 12px; flex-wrap: wrap">
                        <a-switch v-model:checked="formData.driver_config.compress_enabled" />
                        <a-radio-group
                          v-model:value="formData.driver_config.compress_level"
                          :disabled="!formData.driver_config.compress_enabled"
                          size="small"
                          button-style="solid"
                        >
                          <a-radio-button value="FAST">快</a-radio-button>
                          <a-radio-button value="HIGH">高</a-radio-button>
                        </a-radio-group>
                      </div>
                    </a-form-item>
                  </template>
                  <template v-if="isMySQLPhysicalDriver">
                    <a-form-item label="数据库操作系统用户">
                      <a-input
                        v-model:value="formData.driver_config.os_user"
                        placeholder="例如 mysql；填写后预检查和执行优先使用该 OS 用户"
                      />
                      <div class="form-tip">XtraBackup 默认按数据库进程所属用户执行；如实例环境特殊，可手工指定。</div>
                    </a-form-item>
                    <a-form-item label="xtrabackup / mariabackup 路径">
                      <a-input
                        v-model:value="formData.driver_config.tool_path"
                        placeholder="例如 /usr/bin/xtrabackup 或 /usr/bin/mariabackup"
                      />
                      <div class="form-tip">留空时系统会按版本自动搜索 `xtrabackup` / `mariabackup`。</div>
                    </a-form-item>
                  </template>
                  <a-form-item v-if="isPostgresPhysicalDriver && physicalDriverAvailable" label="Stanza">
                      <a-input v-model:value="formData.driver_config.stanza" placeholder="留空时优先复用实例 archive_command 中的 stanza" />
                  </a-form-item>
                    </a-collapse-panel>
                  </a-collapse>
                </a-form>
              </div>
            </div>
          </div>
        </div>

        <div v-else-if="currentStep === 1" class="step-panel">
          <div class="tree-card">
            <div class="tree-toolbar">
              <div class="tree-toolbar-main">
                <div class="tree-title">对象结构树</div>
              </div>
              <div class="tree-toolbar-actions">
                <a-select
                  v-model:value="formData.object_mode"
                  class="object-mode-select"
                  :options="currentObjectModeOptions"
                  :disabled="isPhysicalDriver || !formData.source_instance_id || instanceUnavailable"
                  @change="handleObjectModeChange"
                />
              </div>
            </div>
            <a-spin :spinning="objectUi.loading">
              <a-empty v-if="!formData.source_instance_id" description="请先在步骤 1 选择数据源" />
              <a-tree
                v-else
                checkable
                block-node
                :tree-data="displayTreeData"
                :checked-keys="displayCheckedKeys"
                :expanded-keys="objectUi.expandedKeys"
                :load-data="isPhysicalDriver ? null : loadObjectNode"
                @check="(checkedKeys) => { if (!isPhysicalDriver) handleObjectCheck(checkedKeys) }"
                @expand="(keys) => (objectUi.expandedKeys = keys)"
              />
            </a-spin>
          </div>
        </div>

        <div v-else-if="currentStep === 2" class="step-panel">
          <div class="schedule-grid">
            <a-card class="schedule-card" :bordered="false">
              <template #title>调度频率与执行时间</template>
              <a-form layout="vertical">
                <a-form-item v-if="!isPhysicalMixedMode" label="调度日">
                  <div class="weekday-toolbar">
                    <div class="weekday-tags" :class="{ disabled: everyDayChecked }">
                      <a-checkable-tag
                        v-for="item in weekdayOptions"
                        :key="item.value"
                        :checked="formData.schedule_days.includes(item.value)"
                        @change="(checked) => handleWeekdayChange(item.value, checked)"
                      >
                        {{ item.label }}
                      </a-checkable-tag>
                    </div>
                    <a-checkbox v-model:checked="everyDayChecked">每天</a-checkbox>
                  </div>
                </a-form-item>
                <a-form-item v-if="isPhysicalMixedMode" label="全量执行日">
                  <div class="weekday-toolbar">
                    <div class="weekday-tags">
                      <a-checkable-tag
                        v-for="item in weekdayOptions"
                        :key="`FULL-${item.value}`"
                        :checked="isPhysicalMixedDayChecked('FULL', item.value)"
                        :disabled="!formData.schedule_days.includes(item.value)"
                        @change="(checked) => handlePhysicalMixedDayChange('FULL', item.value, checked)"
                      >
                        {{ item.label }}
                      </a-checkable-tag>
                    </div>
                  </div>
                </a-form-item>
                <a-form-item v-if="isPhysicalMixedMode" label="增量执行日">
                  <div class="weekday-toolbar">
                    <div class="weekday-tags">
                      <a-checkable-tag
                        v-for="item in weekdayOptions"
                        :key="`INCR-${item.value}`"
                        :checked="isPhysicalMixedDayChecked('INCR', item.value)"
                        :disabled="!formData.schedule_days.includes(item.value)"
                        @change="(checked) => handlePhysicalMixedDayChange('INCR', item.value, checked)"
                      >
                        {{ item.label }}
                      </a-checkable-tag>
                    </div>
                  </div>
                </a-form-item>
                <a-form-item label="执行时间">
                  <div class="schedule-time-editor">
                    <a-select
                      v-model:value="scheduleHourModel"
                      class="schedule-time-select"
                      :options="scheduleHourOptions"
                    />
                    <span class="schedule-time-sep">:</span>
                    <a-select
                      v-model:value="scheduleMinuteModel"
                      class="schedule-time-select"
                      :options="scheduleMinuteOptions"
                    />
                  </div>
                </a-form-item>
              </a-form>
            </a-card>

            <a-card class="schedule-card" :bordered="false">
              <template #title>保存时间</template>
              <a-radio-group v-model:value="formData.retention_days" class="retention-group">
                <a-radio-button :value="7">7 天</a-radio-button>
                <a-radio-button :value="30">30 天</a-radio-button>
                <a-radio-button :value="60">60 天</a-radio-button>
                <a-radio-button :value="90">90 天</a-radio-button>
                <a-radio-button :value="180">半年</a-radio-button>
                <a-radio-button :value="365">1 年</a-radio-button>
              </a-radio-group>
              <div class="form-tip">到期后系统会自动清理对应备份集文件和记录，默认保留 30 天。</div>
            </a-card>
          </div>
        </div>

        <div v-else-if="currentStep === 3" class="step-panel">
          <div class="precheck-table-section">
            <div class="precheck-table-section__title">
              <span>校验参数</span>
              <span v-if="precheckStats.total" class="precheck-stats">
                <span class="precheck-stats__item is-success">成功 {{ precheckStats.success }}</span>
                <span
                  class="precheck-stats__item"
                  :class="precheckStats.failed > 0 ? 'is-failed' : 'is-muted'"
                >失败 {{ precheckStats.failed }}</span>
              </span>
            </div>
            <div class="precheck-table-wrapper">
              <a-table
                :columns="precheckTableColumns"
                :data-source="precheckTableData"
                :pagination="false"
                :loading="uiState.prechecking"
                size="small"
                row-key="item_key"
              >
                <template #emptyText>
                  <a-empty :description="uiState.prechecking ? '正在执行预校验...' : '暂未执行预校验'" />
                </template>
              </a-table>
            </div>
          </div>

          <a-modal
            v-model:open="detailModal.open"
            :title="detailModal.title"
            width="720px"
            :footer="null"
            @cancel="detailModal.open = false"
          >
            <pre class="precheck-detail-modal-body">{{ detailModal.content }}</pre>
          </a-modal>
        </div>

        <div v-else class="step-panel">
          <div class="review-grid">
            <a-card size="small" title="任务信息">
              <div class="review-item"><span class="review-label">任务名称：</span><span class="review-value">{{ formData.name || '-' }}</span></div>
              <div class="review-item"><span class="review-label">数据源IP：</span><span class="review-value">{{ currentInstanceEndpoint }}</span></div>
              <div class="review-item"><span class="review-label">数据源名称：</span><span class="review-value">{{ currentInstanceDisplayName }}</span></div>
              <div class="review-item"><span class="review-label">备份方式：</span><span class="review-value">{{ driverTypeLabel(formData.driver_type) }}</span></div>
              <div class="review-item"><span class="review-label">存储目标：</span><span class="review-value">{{ storageSummary }}</span></div>
            </a-card>
            <a-card size="small" title="对象信息">
              <div class="review-item"><span class="review-label">备份对象：</span><span class="review-value">{{ reviewObjectModeLabel }}</span></div>
              <div class="review-item"><span class="review-label">备份类型：</span><span class="review-value">{{ backupTypeLabel(formData.backup_type, formData.driver_type) }}</span></div>
              <div class="review-item"><span class="review-label">已选对象：</span><span class="review-value">{{ selectedObjectSummary }}</span></div>
              <div class="review-item"><span class="review-label">数据库类型：</span><span class="review-value">{{ currentInstance?.db_type || '-' }}</span></div>
            </a-card>
            <a-card size="small" title="周期策略">
              <div class="review-item"><span class="review-label">执行频率：</span><span class="review-value">{{ scheduleSummary }}</span></div>
              <div class="review-item"><span class="review-label">执行时间：</span><span class="review-value">{{ formData.schedule_time || '-' }}</span></div>
              <div class="review-item"><span class="review-label">保存时间：</span><span class="review-value">{{ retentionLabel(formData.retention_days) }}</span></div>
            </a-card>
          </div>

          <a-alert
            v-if="uiState.submitPrepareError.visible"
            type="error"
            show-icon
            closable
            class="submit-prepare-error-alert"
            @close="clearSubmitPrepareError"
          >
            <template #message>{{ uiState.submitPrepareError.title || '备份程序准备失败' }}</template>
            <template #description>
              <div class="submit-error-body">
                <div v-if="uiState.submitPrepareError.suggestion" class="submit-error-suggestion">
                  {{ uiState.submitPrepareError.suggestion }}
                </div>
                <a-collapse v-if="uiState.submitPrepareError.detail || uiState.submitPrepareError.raw_error" ghost>
                  <a-collapse-panel key="detail" header="查看详细信息（排查用）">
                    <pre v-if="uiState.submitPrepareError.detail" class="instance-unavailable-pre">{{ uiState.submitPrepareError.detail }}</pre>
                    <pre v-if="uiState.submitPrepareError.raw_error && uiState.submitPrepareError.raw_error !== uiState.submitPrepareError.detail" class="instance-unavailable-pre">
{{ uiState.submitPrepareError.raw_error }}</pre>
                  </a-collapse-panel>
                </a-collapse>
                <div class="submit-error-actions">
                  <a-space>
                    <a-button size="small" @click="copySubmitPrepareError">复制错误详情</a-button>
                    <a-button size="small" type="link" @click="editInstanceConnection">编辑数据源连接信息</a-button>
                    <a-button size="small" type="link" @click="retrySubmitTask">重新提交</a-button>
                  </a-space>
                </div>
              </div>
            </template>
          </a-alert>
        </div>

        <div class="step-actions">
          <div class="step-actions-left">
            <a-button class="step-nav-btn" @click="handleBack">
              <template #icon><LeftOutlined /></template>
              返回列表
            </a-button>
          </div>
          <div class="step-actions-center">
            <a-button v-if="currentStep > 0" class="step-nav-btn" @click="currentStep -= 1">
              <template #icon><LeftOutlined /></template>
              上一步
            </a-button>
            <a-button
              v-if="currentStep < maxStepIndex"
              class="step-nav-btn"
              :loading="uiState.submitting || uiState.prechecking"
              :disabled="currentStep === 3 && (!precheckCompleted || hasBlockingPrecheckFailures)"
              @click="handleNext"
            >
              {{ nextStepButtonText }}
              <template #icon><RightOutlined /></template>
            </a-button>
          </div>
          <div class="step-actions-right">
            <a-button
              v-if="currentStep === 3"
              type="primary"
              :loading="uiState.prechecking"
              @click="rerunPrecheck"
            >重新校验</a-button>
            <template v-if="currentStep === reviewStepIndex">
              <a-button :loading="uiState.submitting" :disabled="instanceUnavailable" @click="submitTask(false)">保存并退出</a-button>
              <a-button type="primary" :loading="uiState.submitting" :disabled="instanceUnavailable" @click="handleStartSubmit">{{ isEditMode ? '保存并启动' : '启动任务' }}</a-button>
            </template>
          </div>
        </div>
        <div v-if="currentStep === reviewStepIndex && instanceUnavailable" class="submit-disabled-hint">实例不可用，无法提交</div>
      </a-card>
    </div>
  </div>
</template>

<script setup>
import { computed, h, inject, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { Modal, message } from 'ant-design-vue'
import {
  CheckCircleOutlined,
  ClockCircleOutlined,
  CloseCircleOutlined,
  ExclamationCircleOutlined,
  EyeOutlined,
  LeftOutlined,
  LoadingOutlined,
  RightOutlined,
} from '@ant-design/icons-vue'
import { useRoute, useRouter } from 'vue-router'
import {
  createBackupTaskApi,
  getBackupInstancesApi,
  getBackupNodesApi,
  getBackupObjectsApi,
  getBackupTaskDetailApi,
  precheckBackupTaskApi,
  getStorageTargetsApi,
  updateBackupTaskApi,
} from '@/api/backup'
import { testInstanceConnectionApi } from '@/api/admin'
import {
  isRetentionReconcileCanceled,
  updateBackupTaskWithRetentionConfirm,
} from '@/utils/retentionReconcile'

defineOptions({ name: 'BackupTaskCreateView' })

const route = useRoute()
const router = useRouter()

const taskId = computed(() => `${route.params?.id || ''}`.trim())
const isEditMode = computed(() => !!taskId.value)
const pageTitle = computed(() => (isEditMode.value ? '编辑备份任务' : '新建备份任务'))
const presetSourceInstanceId = computed(() => {
  const raw = `${route.query?.source_instance_id || ''}`.trim()
  if (!raw) return null
  const parsed = Number(raw)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : null
})

const objectModeOptions = [
  { label: '全部实例', value: 'ALL' },
  { label: '自定义对象', value: 'CUSTOM' },
]
const physicalObjectModeOptions = [{ label: '全部实例', value: 'ALL' }]
const currentObjectModeOptions = computed(() => (isPhysicalDriver.value ? physicalObjectModeOptions : objectModeOptions))
const weekdayOptions = [
  { label: '周一', value: 'MON' },
  { label: '周二', value: 'TUE' },
  { label: '周三', value: 'WED' },
  { label: '周四', value: 'THU' },
  { label: '周五', value: 'FRI' },
  { label: '周六', value: 'SAT' },
  { label: '周日', value: 'SUN' },
]

const scheduleHourOptions = Array.from({ length: 24 }, (_, index) => {
  const value = String(index).padStart(2, '0')
  return { label: value, value }
})

const scheduleMinuteOptions = Array.from({ length: 12 }, (_, index) => {
  const value = String(index * 5).padStart(2, '0')
  return { label: value, value }
})

const uiState = reactive({
  instancesLoading: false,
  nodesLoading: false,
  storageTargetsLoading: false,
  submitting: false,
  instanceChecking: false,
  instanceErrorPanels: [],
  instanceRuntimeError: '',
  submitPrepareError: {
    visible: false,
    stage: '',
    title: '',
    suggestion: '',
    detail: '',
    raw_error: '',
  },
  lastSubmitEnabled: true,
  prechecking: false,
  precheckResult: {
    summary: '',
    items: [],
    resolved: null,
    error: '',
  },
})

const currentStep = ref(0)
const instances = ref([])
const nodes = ref([])
const storageTargets = ref([])
const nameEdited = ref(false)
const lastAutoName = ref('')
const previewTaskId = ref('')

// 第四步「启动前预校验」为满高布局（滚动条只作用于表格区域），
// 通过公共布局的 setLayoutContentNoScroll 关掉内容区滚动，离开该步/卸载时恢复。
const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)
watch(currentStep, (step) => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(step === 3)
  }
})
onBeforeUnmount(() => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(false)
  }
})

const formData = reactive({
  name: '',
  source_instance_id: null,
  storage_target_id: null,
  executor_node_id: null,
  repository_node_id: null,
  driver_type: 'LOGICAL',
  driver_config: {
    process_max: 2,
    // 单次执行最长运行时间（小时）：留空（null）= 使用平台默认 12 小时
    max_runtime_hours: null,
    os_user: '',
    tool_path: '',
    start_fast: true,
    stanza: '',
    strategy_mode: 'MIXED',
    full_backup_days: ['WED', 'SUN'],
    other_days_type: 'INCR',
    fallback_to_full_when_missing: true,
    // 压缩策略：任务级可选，默认启用「快」模式
    compress_enabled: true,
    compress_level: 'FAST',
  },
  backup_type: 'FULL',
  object_mode: 'ALL',
  output_dir: '',
  schedule_days: ['MON', 'WED', 'FRI', 'SUN'],
  schedule_time: '01:00',
  retention_days: 30,
})

const objectUi = reactive({
  loading: false,
  treeData: [],
  checkedKeys: [],
  expandedKeys: [],
})

// 高级选项折叠面板：默认收起（并行数 / OS 用户 / 工具路径 / 压缩策略等）
const advancedOpen = ref([])

const currentInstance = computed(() => {
  const id = formData.source_instance_id
  if (!id) return null
  return instances.value.find((item) => item.value === id)?.raw || null
})

const effectiveTaskId = computed(() => `${taskId.value || previewTaskId.value || ''}`.trim())
const reviewStepIndex = computed(() => 4)
const maxStepIndex = computed(() => stepTitles.value.length - 1)
const nextStepButtonText = computed(() => {
  if (currentStep.value === 2) return '执行预校验'
  return '下一步'
})

const normalizeInstanceCheckStatus = (value) => String(value || '').trim().toLowerCase()
const instanceUnavailable = computed(
  () => normalizeInstanceCheckStatus(currentInstance.value?.last_check_status) === 'error' || !!uiState.instanceRuntimeError,
)
const instanceUnavailableDetail = computed(() => {
  const detail = String(currentInstance.value?.last_check_message || '').trim()
  return detail || uiState.instanceRuntimeError
})

const isPhysicalDriver = computed(() => ['PG_BACKREST', 'XTRABACKUP'].includes(String(formData.driver_type || '').toUpperCase()))
const isPostgresPhysicalDriver = computed(() => String(formData.driver_type || '').toUpperCase() === 'PG_BACKREST')
const isMySQLPhysicalDriver = computed(() => String(formData.driver_type || '').toUpperCase() === 'XTRABACKUP')
const isLogicalDriver = computed(() => !isPhysicalDriver.value)
const stepTitles = computed(() => ['数据源与目标配置', '备份对象选择', '备份策略与周期', '启动前预校验', '启动任务'])
const hasSelectedInstance = computed(() => !!formData.source_instance_id)
const physicalDriverType = computed(() => {
  const dbType = String(currentInstance.value?.db_type || '').trim()
  if (dbType === 'PostgreSQL') return 'PG_BACKREST'
  if (dbType === 'MySQL') return 'XTRABACKUP'
  return ''
})
const physicalDriverAvailable = computed(() => !!physicalDriverType.value)
const physicalDriverBlocked = computed(() => hasSelectedInstance.value && !physicalDriverAvailable.value)
const physicalDriverHint = computed(() => {
  if (!hasSelectedInstance.value) {
    return '请先选择数据源。'
  }
  if (physicalDriverAvailable.value) {
    return isMySQLPhysicalDriver.value
      ? '当前数据源支持 MySQL 物理备份。'
      : '当前数据源支持 PostgreSQL 物理备份。'
  }
  const dbType = String(currentInstance.value?.db_type || '').trim() || '当前'
  return `${dbType} 数据源暂不支持物理备份，请使用逻辑备份。`
})
const logicalBackupIncludesSchema = computed(() => formData.backup_type === 'FULL' || formData.backup_type === 'SCHEMA')
const logicalBackupIncludesData = computed(() => formData.backup_type === 'FULL' || formData.backup_type === 'DATA')
// 金仓走 PG 逻辑通道，但客户端工具是 sys_dump / sys_restore / ksql，
// 提示文案必须跟着换，否则运维会照着 pg_dump 的路径去找（金仓装机没有这个文件）。
const isKingbaseInstance = computed(() => {
  const dbType = String(currentInstance.value?.db_type || '').trim().toLowerCase()
  return dbType.startsWith('kingbase')
})
const logicalToolPathLabel = computed(() => {
  if (isKingbaseInstance.value) return 'sys_dump 路径'
  return currentInstance.value?.db_type === 'PostgreSQL' ? 'pg_dump 路径' : 'mysqldump 路径'
})
const logicalToolPathPlaceholder = computed(() => {
  if (isKingbaseInstance.value) return '例如 /opt/Kingbase/ES/V8/Server/bin/sys_dump'
  return currentInstance.value?.db_type === 'PostgreSQL'
    ? '例如 /usr/local/pgsql/bin/pg_dump'
    : '例如 /usr/local/mysql/bin/mysqldump'
})
const physicalOutputDirPlaceholder = computed(() =>
  isMySQLPhysicalDriver.value
    ? '留空使用存储目标根路径下的 xtrabackup/mysql 自动子目录，或填入相对路径，例如 project-a/cluster-01'
    : '留空使用存储目标根路径下的 pgbackrest/postgresql 自动子目录，或填入相对路径，例如 project-a/cluster-01',
)

const matchedPhysicalExecutorNode = computed(() => {
  if (!isPhysicalDriver.value) return null
  const hostname = String(currentInstance.value?.hostname || '').trim().toLowerCase()
  if (!hostname) return null
  const sameHostNodes = nodes.value.filter((node) => String(node.host || '').trim().toLowerCase() === hostname)
  if (!sameHostNodes.length) return null
  const online = sameHostNodes.find((n) => String(n.status || '').toUpperCase() === 'ONLINE')
  return online || sameHostNodes[0]
})

watch(
  [isPhysicalDriver, matchedPhysicalExecutorNode],
  () => {
    if (!isPhysicalDriver.value) return
    if (matchedPhysicalExecutorNode.value) {
      formData.executor_node_id = matchedPhysicalExecutorNode.value.id
    } else {
      formData.executor_node_id = null
    }
  },
  { immediate: true },
)

watch(maxStepIndex, (value) => {
  if (currentStep.value > value) {
    currentStep.value = value
  }
})

watch(
  () => [
    formData.source_instance_id,
    formData.storage_target_id,
    formData.executor_node_id,
    formData.repository_node_id,
    formData.driver_type,
    formData.backup_type,
    formData.object_mode,
    formData.output_dir,
    formData.schedule_time,
    formData.retention_days,
    JSON.stringify(formData.schedule_days || []),
    JSON.stringify(formData.driver_config || {}),
    JSON.stringify(objectUi.checkedKeys || []),
  ],
  () => {
    resetPrecheckResult()
  },
)

const storageTargetOptions = computed(() =>
  storageTargets.value
    .filter((t) => {
      const type = String(t?.type || '').toUpperCase()
      // 本地服务器 + 全部 S3 兼容对象存储（OSS / COS / OBS / MinIO）
      return type === 'LOCAL_SERVER' || ['OSS_ALIYUN', 'COS_TENCENT', 'OBS_HUAWEI', 'S3_MINIO'].includes(type)
    })
    .map((t) => {
      const type = String(t.type || '').toUpperCase()
      const isLocal = type === 'LOCAL_SERVER'
      const sub = isLocal
        ? `${t.ssh_user || '-'}@${t.host || '-'}:${t.port || ''}${t.base_path || ''}`
        : `${t.s3_endpoint || ''} / ${t.s3_bucket || ''}`
      const conn = String(t.connection_status || '').toUpperCase()
      // 本地服务器：需指纹确认且连接正常；对象存储：无指纹概念，连通测试通过即可用。
      const healthy = isLocal ? !!t.fingerprint_confirmed && conn === 'OK' : conn === 'OK'
      const statusLabel = isLocal
        ? !t.fingerprint_confirmed
          ? '未确认指纹'
          : healthy
            ? '连接正常'
            : conn === 'FAILED'
              ? '连接失败'
              : conn === 'PENDING'
                ? '检测中'
                : '未检测'
        : healthy
          ? '连接正常'
          : conn === 'FAILED'
            ? '连通测试失败'
            : conn === 'PENDING'
              ? '检测中'
              : '未做连通测试'
      const statusColor = healthy
        ? 'green'
        : conn === 'FAILED'
          ? 'red'
          : isLocal
            ? t.fingerprint_confirmed
              ? 'default'
              : 'orange'
            : 'orange'
      return {
        value: t.id,
        label: healthy ? `${t.name}（${sub}）` : `${t.name}（${sub} · ${statusLabel}）`,
        disabled: isLocal ? !t.fingerprint_confirmed : conn !== 'OK',
        healthy,
        raw: t,
        statusLabel,
        statusColor,
      }
    }),
)

const currentInstanceEndpoint = computed(() => {
  const hostname = String(currentInstance.value?.hostname || '').trim()
  const port = String(currentInstance.value?.port || '').trim()
  if (!hostname) return '-'
  return port ? `${hostname}:${port}` : hostname
})

const currentInstanceDisplayName = computed(() => {
  const remark = String(currentInstance.value?.remark || '').trim()
  const fallback = currentInstanceEndpoint.value
  const name = remark || (fallback === '-' ? '' : fallback)
  if (!name) return '-'
  return name.length > 15 ? `${name.slice(0, 15).trimEnd()}...` : name
})

const LOCAL_SERVICE_HOST_FALLBACK = '192.168.53.102'
const LOCAL_SERVICE_DEFAULT_BACKUP_DIR = '/opt/goInsight/backend/goinsight/media/backup'

const localServiceHost = computed(() => {
  const browserHost = String(window?.location?.hostname || '').trim()
  if (browserHost && browserHost !== 'localhost' && browserHost !== '127.0.0.1') {
    return browserHost
  }
  return LOCAL_SERVICE_HOST_FALLBACK
})

const joinStoragePath = (...segments) => {
  const parts = segments
    .map((item) => String(item || '').trim())
    .filter(Boolean)
    .map((item, index) => {
      if (index === 0) return item.replace(/\/+$/g, '')
      return item.replace(/^\/+/g, '').replace(/\/+$/g, '')
    })
  return parts.join('/') || ''
}

// 备份目录必须位于所选存储目标的备份根路径（base_path）之下：
// - 绝对路径：必须以根路径开头，否则报错并给出友好提示；
// - 相对路径：自动拼接为「根路径/相对路径」；
// - 留空：沿用默认值（由服务端按实例/任务/批次自动建子目录）。
const resolveOutputDirAgainstTarget = () => {
  const raw = `${formData.output_dir || ''}`.trim()
  if (!raw) {
    return { ok: true, value: '' }
  }
  const target = storageTargets.value.find((t) => t.id === formData.storage_target_id)
  const normalizedBase = `${target?.base_path || ''}`.trim().replace(/\/+$/g, '')
  // 目标未配置根路径（如对象存储桶根）时不做前缀约束
  if (!normalizedBase) {
    return { ok: true, value: raw }
  }
  if (raw.startsWith('/')) {
    if (raw === normalizedBase || raw.startsWith(`${normalizedBase}/`)) {
      return { ok: true, value: raw }
    }
    return {
      ok: false,
      error: `备份目录必须位于存储目标「${target?.name || ''}」的根路径 ${normalizedBase} 之下：请输入以 ${normalizedBase} 开头的绝对路径，或直接填写相对子目录（系统会自动拼接为 ${normalizedBase}/子目录）`,
    }
  }
  return { ok: true, value: `${normalizedBase}/${raw.replace(/^\/+/g, '')}` }
}

const storageSummary = computed(() => {
  const target = storageTargetOptions.value.find((t) => t.value === formData.storage_target_id)?.raw
  const suffix = `${formData.output_dir || ''}`.trim()
  if (isPhysicalDriver.value) {
    const targetType = String(target?.type || '').toUpperCase()
    const physicalSubDir = isMySQLPhysicalDriver.value ? 'xtrabackup/mysql' : 'pgbackrest/postgresql'
    if (targetType === 'LOCAL_SERVER' && target) {
      const host = String(target.host || '').trim()
      const path = joinStoragePath(target.base_path || '', suffix, physicalSubDir) || physicalSubDir
      return `${host || '-'}:${path}`
    }
    if (['OSS_ALIYUN', 'COS_TENCENT', 'S3_MINIO', 'OBS_HUAWEI'].includes(targetType) && target) {
      return `${target.s3_endpoint || '-'} / ${target.s3_bucket || '-'} / ${joinStoragePath(suffix, physicalSubDir) || physicalSubDir}`
    }
    return '未选择存储目标'
  }
  if (target) {
    const targetType = String(target.type || '').toUpperCase()
    if (targetType === 'LOCAL_SERVER') {
      const host = String(target.host || '').trim()
      const path = joinStoragePath(target.base_path || '', suffix) || suffix || LOCAL_SERVICE_DEFAULT_BACKUP_DIR
      return host ? `${host}:${path}` : `${localServiceHost.value}:${path}`
    }
    return `${target.s3_endpoint || ''} / ${target.s3_bucket || ''} / ${suffix || ''}`
  }
  return `${localServiceHost.value}:${suffix || LOCAL_SERVICE_DEFAULT_BACKUP_DIR}`
})

const selectedObjectSummary = computed(() => {
  if (isPostgresPhysicalDriver.value) return 'PostgreSQL 整实例物理备份'
  if (isMySQLPhysicalDriver.value) return 'MySQL 整实例物理备份'
  if (formData.object_mode === 'ALL') return '全部实例'
  if (!objectUi.checkedKeys.length) return '未选择'
  return `已选择 ${objectUi.checkedKeys.length} 个对象节点`
})

const reviewObjectModeLabel = computed(() => (isPhysicalDriver.value ? '整实例' : formData.object_mode === 'ALL' ? '全部实例' : '自定义对象'))
const isPhysicalMixedMode = computed(() => isPhysicalDriver.value && formData.driver_config.strategy_mode === 'MIXED')
const PHYSICAL_MIXED_DEFAULT_FULL_DAYS = ['WED', 'SUN']
const PHYSICAL_MIXED_DEFAULT_INCREMENTAL_DAYS = ['MON', 'TUE', 'THU', 'FRI', 'SAT']

const scheduleSummary = computed(() => {
  if (formData.schedule_days.length === 7) return '每天'
  return weekdayOptions
    .filter((item) => formData.schedule_days.includes(item.value))
    .map((item) => item.label)
    .join('、')
})

const everyDayChecked = computed({
  get: () => formData.schedule_days.length === 7,
  set: (checked) => {
    formData.schedule_days = checked ? weekdayOptions.map((item) => item.value) : ['MON', 'WED', 'FRI', 'SUN']
    if (isPhysicalDriver.value && formData.driver_config.strategy_mode === 'MIXED') {
      const remainingFull = formData.driver_config.full_backup_days.filter((d) => formData.schedule_days.includes(d))
      if (remainingFull.length < 1) {
        const firstSchedule = formData.schedule_days[0]
        if (firstSchedule) formData.driver_config.full_backup_days = [firstSchedule]
      } else {
        formData.driver_config.full_backup_days = remainingFull
      }
    }
  },
})

const isPhysicalMixedDayChecked = (type, value) => {
  if (type === 'FULL') {
    return formData.driver_config.full_backup_days.includes(value)
  }
  return !formData.driver_config.full_backup_days.includes(value)
}

const instanceNameForTask = (instance) => {
  if (!instance) return ''
  const remark = `${instance.remark || ''}`.trim()
  if (remark) return remark.replace(/\s+/g, '_')
  return `${instance.hostname}_${instance.port}`
}

const buildDefaultTaskName = (instance) => {
  if (!instance) return ''
  const date = new Date()
  const yyyy = `${date.getFullYear()}`
  const mm = `${date.getMonth() + 1}`.padStart(2, '0')
  const dd = `${date.getDate()}`.padStart(2, '0')
  return `backup_${instanceNameForTask(instance)}_${yyyy}${mm}${dd}`
}

const handleNameInput = () => {
  nameEdited.value = true
}

const updateLogicalBackupType = (nextSchemaSelected, nextDataSelected) => {
  if (nextSchemaSelected && nextDataSelected) {
    formData.backup_type = 'FULL'
    return true
  }
  if (nextSchemaSelected) {
    formData.backup_type = 'SCHEMA'
    return true
  }
  if (nextDataSelected) {
    formData.backup_type = 'DATA'
    return true
  }
  return false
}

const toggleLogicalBackupPart = (part) => {
  const nextSchemaSelected = part === 'SCHEMA' ? !logicalBackupIncludesSchema.value : logicalBackupIncludesSchema.value
  const nextDataSelected = part === 'DATA' ? !logicalBackupIncludesData.value : logicalBackupIncludesData.value
  const updated = updateLogicalBackupType(nextSchemaSelected, nextDataSelected)
  if (!updated) {
    message.warning('备份对象至少选择一个')
  }
}

const selectDriverType = async (type) => {
  if (!type) {
    if (!hasSelectedInstance.value) {
      message.info('请先选择数据源。')
    }
    return
  }
  if (['PG_BACKREST', 'XTRABACKUP'].includes(String(type).toUpperCase()) && physicalDriverBlocked.value) {
    message.warning(physicalDriverHint.value)
    return
  }
  if (formData.driver_type === type) return
  formData.driver_type = type
  await handleDriverTypeChange()
}

const handleDriverTypeChange = async () => {
  if (isPhysicalDriver.value) {
    formData.driver_config.strategy_mode = 'MIXED'
    formData.driver_config.start_fast = true
    formData.driver_config.full_backup_days = [...PHYSICAL_MIXED_DEFAULT_FULL_DAYS]
    formData.driver_config.other_days_type = 'INCR'
    formData.driver_config.fallback_to_full_when_missing = true
    formData.schedule_days = [...weekdayOptions.map((item) => item.value)]
    formData.backup_type = 'FULL'
    formData.object_mode = 'ALL'
    if (formData.source_instance_id) {
      await loadLogicalObjectTree()
    } else {
      objectUi.treeData = []
      objectUi.checkedKeys = []
      objectUi.expandedKeys = []
    }
    return
  }
  if (!formData.object_mode) {
    formData.object_mode = 'ALL'
  }
  if (formData.source_instance_id) {
    await loadLogicalObjectTree()
  }
}

const loadInstances = async () => {
  uiState.instancesLoading = true
  try {
    const res = await getBackupInstancesApi({ is_page: false })
    instances.value = (res?.data || []).map((item) => {
      const hostname = String(item?.hostname || '').trim()
      const port = String(item?.port || '').trim()
      const hostPort = hostname ? (port ? `${hostname}:${port}` : hostname) : ''
      const remark = String(item?.remark || '').trim()
      const displayRemark = remark || hostPort
      const ellipsisRemark = displayRemark.length > 15 ? displayRemark.slice(0, 15).trimEnd() + '...' : displayRemark
      const displayLabel = ellipsisRemark || hostPort || String(item?.instance_id || '').slice(0, 15)
      const fullLabel = remark && hostPort ? `${remark} | ${hostPort}` : (remark || hostPort)
      const searchText = `${item.db_type || ''} ${item.hostname || ''} ${item.port || ''} ${item.remark || ''} ${hostPort || ''} ${String(item?.instance_id || '')}`.toLowerCase()
      return {
        value: item.id,
        label: displayLabel,
        displayLabel,
        ellipsisRemark,
        hostPort,
        fullRemark: remark,
        longLabel: fullLabel || displayLabel,
        searchText,
        raw: item,
      }
    })
  } finally {
    uiState.instancesLoading = false
  }
}

const loadNodes = async () => {
  uiState.nodesLoading = true
  try {
    const res = await getBackupNodesApi({ is_page: false })
    nodes.value = res?.data || []
  } finally {
    uiState.nodesLoading = false
  }
}

// 用户尚未选择时，默认选中第一个可用（指纹已确认）的存储目标
const ensureDefaultStorageTarget = () => {
  if (formData.storage_target_id) return
  const first = storageTargetOptions.value.find((item) => !item.disabled)
  if (first) formData.storage_target_id = first.value
}

const loadStorageTargets = async () => {
  uiState.storageTargetsLoading = true
  try {
    const res = await getStorageTargetsApi({ is_page: false })
    storageTargets.value = res?.data || []
    ensureDefaultStorageTarget()
  } finally {
    uiState.storageTargetsLoading = false
  }
}

const filterInstanceOption = (input, option) => {
  const keyword = `${input || ''}`.trim().toLowerCase()
  if (!keyword) return true
  return `${option?.searchText || option?.label || ''}`.includes(keyword)
}

const normalizeTreeKeysFromSpec = (dbType, spec) => {
  const keys = []
  const databases = spec?.databases || []
  if (dbType === 'MySQL') {
    databases.forEach((db) => {
      if (!db?.name) return
      if (!db.tables || db.tables.length === 0) {
        keys.push(`db:${db.name}`)
        return
      }
      db.tables.forEach((t) => keys.push(`db:${db.name}/table:${t}`))
    })
    return keys
  }
  databases.forEach((db) => {
    if (!db?.name) return
    const schemas = db.schemas || []
    const tables = db.tables || []
    if ((!schemas || schemas.length === 0) && (!tables || tables.length === 0)) {
      keys.push(`db:${db.name}`)
      return
    }
    schemas.forEach((s) => keys.push(`db:${db.name}/schema:${s}`))
    tables.forEach((t) => {
      if (!t?.schema || !t?.name) return
      keys.push(`db:${db.name}/schema:${t.schema}/table:${t.name}`)
    })
  })
  return keys
}

const mandatoryPrecheckItems = computed(() => (uiState.precheckResult.items || []).filter((item) => item?.blocking !== false))
const hasBlockingPrecheckFailures = computed(() => mandatoryPrecheckItems.value.some((item) => !item?.passed))
const precheckCompleted = computed(() => (uiState.precheckResult.items || []).length > 0)

const formatPrecheckValue = (value) => {
  if (value === undefined || value === null) return ''
  if (Array.isArray(value)) return value.join(' -> ')
  if (typeof value === 'object') {
    try {
      return JSON.stringify(value)
    } catch (e) {
      return String(value)
    }
  }
  return String(value)
}

const resetPrecheckResult = () => {
  uiState.precheckResult.summary = ''
  uiState.precheckResult.items = []
  uiState.precheckResult.resolved = null
  uiState.precheckResult.error = ''
}

const precheckDisplayItems = ref([])
let precheckAnimationTimer = null

const clearPrecheckAnimation = () => {
  if (precheckAnimationTimer) {
    clearInterval(precheckAnimationTimer)
    precheckAnimationTimer = null
  }
}

onBeforeUnmount(() => {
  clearPrecheckAnimation()
})

const startPrecheckAnimation = () => {
  clearPrecheckAnimation()
  if (!precheckDisplayItems.value.length) return
  precheckAnimationTimer = setInterval(() => {
    const pendingIndex = precheckDisplayItems.value.findIndex((it) => it.status === 'PENDING')
    if (pendingIndex === -1) {
      clearPrecheckAnimation()
      return
    }
    precheckDisplayItems.value[pendingIndex].status = 'RUNNING'
  }, 350)
}

const precheckRowStatus = (item) => {
  if (item?.status) return String(item.status).toUpperCase()
  if (item?.passed) return 'PASSED'
  const severity = String(item?.severity || '').toUpperCase()
  if (item?.blocking === false || severity === 'WARNING' || severity === 'WARN') return 'WARNING'
  return 'FAILED'
}

const precheckTableColumns = [
  {
    title: '检查结果',
    key: 'status',
    dataIndex: 'status',
    width: 120,
    align: 'center',
    customRender: ({ record }) => renderStatusTag(record.status),
  },
  {
    title: '检查项',
    key: 'label',
    dataIndex: 'label',
    width: 220,
    ellipsis: true,
  },
  {
    title: '检查内容',
    key: 'message',
    dataIndex: 'message',
    minWidth: 360,
    ellipsis: true,
  },
  {
    title: '操作',
    key: 'action',
    width: 100,
    align: 'center',
    customRender: ({ record }) => {
      const hasDetail = !!formatPrecheckDetail(record.detail)
      if (!hasDetail) return h('span', { class: 'precheck-no-action' }, '-')
      return h(
        'a',
        {
          class: 'precheck-detail-link',
          onClick: () => openPrecheckDetail(record),
        },
        [h(EyeOutlined), ' 详情'],
      )
    },
  },
]

// 逻辑备份（MySQL / PostgreSQL）预检查项的前端标准文案。
// 仅覆盖展示层：检查项名称 + 检查内容说明，实际校验结果仍在「详情」中查看。
const LOGICAL_PRECHECK_COPY = {
  database_version_info: {
    label: '数据库版本识别',
    message: '检查源数据库版本，确认备份工具兼容性',
  },
  executor_node: {
    label: '执行环境节点在线',
    message: '检查备份执行节点存活、网络可达',
  },
  source_ssh: {
    label: '数据库主机 SSH 连通校验',
    message: '检查数据库服务器 SSH 连接可用性',
  },
  database_runtime: {
    label: '数据库进程运行状态',
    message: '检查源数据库服务进程是否正常运行',
  },
  logical_backup_tool: {
    label: '备份工具可用性校验',
    message: '检查备份工具是否已安装可调用',
  },
  storage_target: {
    label: '存储目标连通性校验',
    message: '检查备份落地存储目标连接正常',
  },
  executor_sshpass: {
    label: 'sshpass 依赖检查',
    message: '检查执行节点 sshpass 是否存在',
  },
  execution_plan: {
    label: '执行计划校验',
    message: '检查备份任务调度参数、执行配置是否合法',
  },
}

const isLogicalPrecheck = computed(() => String(formData.driver_type || '').toUpperCase() === 'LOGICAL')

// 逻辑备份时套用标准文案，同时保留后端原始 message 作为详情兜底
const applyPrecheckCopy = (item) => {
  if (!isLogicalPrecheck.value) return item
  const copy = LOGICAL_PRECHECK_COPY[String(item?.item_key || '')]
  if (!copy) return item
  return { ...item, label: copy.label, message: copy.message, raw_message: item.message }
}

const precheckTableData = computed(() => {
  const source =
    precheckDisplayItems.value.length > 0 && uiState.prechecking
      ? precheckDisplayItems.value
      : uiState.precheckResult.items || []
  return source.map((item) => {
    const withCopy = applyPrecheckCopy(item)
    return {
      ...withCopy,
      status: precheckRowStatus(withCopy),
    }
  })
})

const precheckStats = computed(() => {
  const items = uiState.precheckResult.items || []
  let success = 0
  items.forEach((item) => {
    if (item?.passed) success += 1
  })
  return { success, failed: items.length - success, total: items.length }
})

const statusTagConfig = {
  RUNNING: { color: 'blue', icon: LoadingOutlined, text: '检查中', spin: true },
  PENDING: { color: 'default', icon: ClockCircleOutlined, text: '待检查', spin: false },
  PASSED: { color: 'success', icon: CheckCircleOutlined, text: '通过', spin: false },
  FAILED: { color: 'error', icon: CloseCircleOutlined, text: '失败', spin: false },
  WARNING: { color: 'warning', icon: ExclamationCircleOutlined, text: '警告', spin: false },
}

const renderStatusTag = (status) => {
  const cfg = statusTagConfig[status] || statusTagConfig.PENDING
  const Icon = cfg.icon
  return h(
    'span',
    { class: ['precheck-status-tag', `is-${String(status || 'pending').toLowerCase()}`] },
    [h(Icon, { class: cfg.spin ? 'anticon-spin' : '', style: { marginRight: '6px' } }), cfg.text],
  )
}

const detailModal = reactive({
  open: false,
  title: '',
  content: '',
})

const openPrecheckDetail = (record) => {
  detailModal.title = `${record.label || '检查项'} - 详情`
  detailModal.content = formatPrecheckDetail(record.detail) || record.raw_message || record.message || '无详细说明'
  detailModal.open = true
}

const buildObjectSpecFromCheckedKeys = (dbType, checkedKeys) => {
  if (dbType === 'MySQL') {
    const dbAll = new Set()
    const dbTables = new Map()
    checkedKeys.forEach((key) => {
      if (typeof key !== 'string') return
      if (key.startsWith('db:') && !key.includes('/')) {
        dbAll.add(key.slice(3))
        return
      }
      const match = /^db:([^/]+)\/table:(.+)$/.exec(key)
      if (!match) return
      const db = match[1]
      const table = match[2]
      if (dbAll.has(db)) return
      if (!dbTables.has(db)) dbTables.set(db, new Set())
      dbTables.get(db).add(table)
    })
    const databases = []
    Array.from(new Set([...dbAll, ...dbTables.keys()]))
      .sort()
      .forEach((db) => {
        if (dbAll.has(db)) {
          databases.push({ name: db, tables: [] })
        } else {
          databases.push({ name: db, tables: Array.from(dbTables.get(db)).sort() })
        }
      })
    return { databases }
  }
  const dbAll = new Set()
  const dbSchemas = new Map()
  const dbTables = new Map()
  checkedKeys.forEach((key) => {
    if (typeof key !== 'string') return
    if (key.startsWith('db:') && !key.includes('/')) {
      dbAll.add(key.slice(3))
      return
    }
    const schemaMatch = /^db:([^/]+)\/schema:([^/]+)$/.exec(key)
    if (schemaMatch) {
      const db = schemaMatch[1]
      const schema = schemaMatch[2]
      if (dbAll.has(db)) return
      if (!dbSchemas.has(db)) dbSchemas.set(db, new Set())
      dbSchemas.get(db).add(schema)
      return
    }
    const tableMatch = /^db:([^/]+)\/schema:([^/]+)\/table:(.+)$/.exec(key)
    if (!tableMatch) return
    const db = tableMatch[1]
    const schema = tableMatch[2]
    const table = tableMatch[3]
    if (dbAll.has(db)) return
    if (dbSchemas.has(db) && dbSchemas.get(db).has(schema)) return
    if (!dbTables.has(db)) dbTables.set(db, [])
    dbTables.get(db).push({ schema, name: table })
  })
  const databases = []
  Array.from(new Set([...dbAll, ...dbSchemas.keys(), ...dbTables.keys()]))
    .sort()
    .forEach((db) => {
      databases.push({
        name: db,
        schemas: dbAll.has(db) ? [] : Array.from(dbSchemas.get(db) || []).sort(),
        tables: dbAll.has(db) ? [] : dbTables.get(db) || [],
      })
    })
  return { databases }
}

const getDatabaseLevelKeys = () =>
  (Array.isArray(objectUi.treeData?.[0]?.children) ? objectUi.treeData[0].children : [])
    .map((node) => node?.key)
    .filter((key) => typeof key === 'string' && key)

const getAllInstanceSelectionKeys = () => ['root', ...getDatabaseLevelKeys()]

const normalizeTreeCheckedKeys = (keys) => {
  const nextKeys = Array.isArray(keys) ? keys.filter((key) => typeof key === 'string' && key) : []
  const databaseKeys = getDatabaseLevelKeys()
  const hasAllDatabases = databaseKeys.length > 0 && databaseKeys.every((key) => nextKeys.includes(key))
  const withoutRoot = nextKeys.filter((key) => key !== 'root')
  return hasAllDatabases ? Array.from(new Set(['root', ...withoutRoot])) : withoutRoot
}

const applyAllInstanceSelection = () => {
  objectUi.checkedKeys = getAllInstanceSelectionKeys()
  objectUi.expandedKeys = ['root']
}

const getNodeSelectionState = (node, checkedSet) => {
  if (!node) return { anySelected: false, fullySelected: false }
  if (node.key === 'root') {
    const children = Array.isArray(node.children) ? node.children : []
    const childStates = children.map((child) => getNodeSelectionState(child, checkedSet))
    const anySelected = checkedSet.has('root') || childStates.some((state) => state.anySelected)
    const fullySelected = checkedSet.has('root') || (children.length > 0 && childStates.every((state) => state.fullySelected))
    return { anySelected, fullySelected }
  }
  const selfSelected = checkedSet.has(node.key)
  const children = Array.isArray(node.children) ? node.children : []
  if (!children.length) {
    return { anySelected: selfSelected, fullySelected: selfSelected }
  }
  const childStates = children.map((child) => getNodeSelectionState(child, checkedSet))
  const anySelected = selfSelected || childStates.some((state) => state.anySelected)
  const fullySelected = selfSelected || childStates.every((state) => state.fullySelected)
  return { anySelected, fullySelected }
}

const renderTreeNodeTitle = (node, state) => {
  if (node?.isEmptyPlaceholder) {
    return h('span', { class: 'object-tree-empty-text' }, String(node?.title || ''))
  }
  const title = node?.node_kind === 'root' ? '实例' : String(node?.title || '')
  const showFullTag =
    ((node?.node_kind === 'database' && formData.object_mode === 'ALL' && state.anySelected) ||
      ((node?.node_kind === 'database' || node?.node_kind === 'schema') &&
        formData.object_mode === 'CUSTOM' &&
        state.fullySelected))
  if (!showFullTag) return title
  return h('span', { class: 'object-tree-title' }, [
    h('span', { class: 'object-tree-title-text' }, title),
    h('span', { class: 'object-tree-title-suffix' }, '（全部）'),
  ])
}

const mapDisplayTreeNodes = (nodes, checkedSet) =>
  (Array.isArray(nodes) ? nodes : []).map((node) => {
    const state = getNodeSelectionState(node, checkedSet)
    const children = Array.isArray(node.children) ? mapDisplayTreeNodes(node.children, checkedSet) : node.children
    return {
      ...node,
      sourceNode: node,
      title: renderTreeNodeTitle(node, state),
      children,
    }
  })

const displayTreeData = computed(() => {
  const checkedSet = new Set(
    normalizeTreeCheckedKeys(formData.object_mode === 'ALL' ? getAllInstanceSelectionKeys() : objectUi.checkedKeys),
  )
  return mapDisplayTreeNodes(objectUi.treeData, checkedSet)
})

const displayCheckedKeys = computed(() =>
  normalizeTreeCheckedKeys(formData.object_mode === 'ALL' ? getAllInstanceSelectionKeys() : objectUi.checkedKeys),
)

const loadLogicalObjectTree = async () => {
  await fetchObjectDatabases()
  if (isPhysicalDriver.value || formData.object_mode === 'ALL') {
    applyAllInstanceSelection()
  }
}

const fetchObjectDatabases = async () => {
  if (!formData.source_instance_id) return
  if (instanceUnavailable.value) return
  objectUi.loading = true
  try {
    uiState.instanceRuntimeError = ''
    const res = await getBackupObjectsApi(
      { instance_id: formData.source_instance_id, level: 'databases' },
      {
        silentBusinessError: true,
        silentHttpError: true,
      },
    )
    const children = (res?.data || []).map((node) => ({ ...node, children: undefined }))
    objectUi.treeData = [
      {
        key: 'root',
        title: '实例',
        selectable: false,
        node_kind: 'root',
        children,
      },
    ]
    objectUi.expandedKeys = ['root']
  } catch (err) {
    uiState.instanceRuntimeError = String(err?.message || err?.data?.message || '').trim()
    objectUi.treeData = []
  } finally {
    objectUi.loading = false
  }
}

const ensureObjectNodeChildrenLoaded = async (data) => {
  if (!data || data.isLeaf) return
  if (Array.isArray(data.children) && data.children.length) return
  const dbType = currentInstance.value?.db_type
  if (!dbType) return
  let params = null
  if (dbType === 'MySQL' && data.node_kind === 'database') {
    params = { instance_id: formData.source_instance_id, level: 'tables', database: data.database }
  }
  // 金仓的对象层级与 PG 一致（库 → schema → 表），共用同一套懒加载分支
  if (dbType === 'PostgreSQL' || isKingbaseInstance.value) {
    if (data.node_kind === 'database') {
      params = { instance_id: formData.source_instance_id, level: 'schemas', database: data.database }
    } else if (data.node_kind === 'schema') {
      params = { instance_id: formData.source_instance_id, level: 'tables', database: data.database, schema: data.schema || data.title }
    }
  }
  if (!params) return
  try {
    const res = await getBackupObjectsApi(params, {
      silentBusinessError: true,
      silentHttpError: true,
    })
    const children = (res?.data || []).map((node) => ({ ...node, children: undefined }))
    if (children.length === 0 && (data.node_kind === 'database' || data.node_kind === 'schema')) {
      const emptyLabel = data.node_kind === 'database' ? '该database下没有表' : '该schema下没有表'
      data.children = [
        {
          key: `${data.key}::__empty__`,
          title: emptyLabel,
          isLeaf: true,
          checkable: false,
          selectable: false,
          disableCheckbox: true,
          disabled: true,
          node_kind: 'empty',
          isEmptyPlaceholder: true,
        },
      ]
    } else {
      data.children = children
    }
    objectUi.treeData = [...objectUi.treeData]
  } catch (err) {
    uiState.instanceRuntimeError = String(err?.message || err?.data?.message || '').trim()
  }
}

const expandCustomObjectTreeToDatabaseLevel = () => {
  objectUi.expandedKeys = ['root']
}

const loadObjectNode = async (treeNode) => {
  const data = treeNode?.dataRef?.sourceNode || treeNode?.dataRef
  await ensureObjectNodeChildrenLoaded(data)
}

const hydrateTreeForCheckedKeys = async (keys) => {
  const checkedKeys = Array.isArray(keys) ? keys : []
  const root = objectUi.treeData?.[0]
  const databases = Array.isArray(root?.children) ? root.children : []
  if (!databases.length || !checkedKeys.length) return

  const targetDatabaseNames = new Set()
  const targetSchemaKeys = new Set()

  checkedKeys.forEach((key) => {
    if (typeof key !== 'string') return
    if (key.startsWith('db:') && !key.includes('/')) {
      targetDatabaseNames.add(key.slice(3))
      return
    }
    const schemaMatch = /^db:([^/]+)\/schema:([^/]+)$/.exec(key)
    if (schemaMatch) {
      targetDatabaseNames.add(schemaMatch[1])
      targetSchemaKeys.add(`db:${schemaMatch[1]}/schema:${schemaMatch[2]}`)
      return
    }
    const tableMatch = /^db:([^/]+)\/schema:([^/]+)\/table:(.+)$/.exec(key)
    if (tableMatch) {
      targetDatabaseNames.add(tableMatch[1])
      targetSchemaKeys.add(`db:${tableMatch[1]}/schema:${tableMatch[2]}`)
      return
    }
    const mysqlTableMatch = /^db:([^/]+)\/table:(.+)$/.exec(key)
    if (mysqlTableMatch) {
      targetDatabaseNames.add(mysqlTableMatch[1])
    }
  })

  for (const databaseNode of databases) {
    if (!targetDatabaseNames.has(databaseNode.database || databaseNode.title)) continue
    await ensureObjectNodeChildrenLoaded(databaseNode)
    const schemas = Array.isArray(databaseNode.children) ? databaseNode.children : []
    for (const schemaNode of schemas) {
      if (!targetSchemaKeys.has(schemaNode.key)) continue
      await ensureObjectNodeChildrenLoaded(schemaNode)
    }
  }
}

const handleObjectCheck = (checkedKeys) => {
  objectUi.checkedKeys = normalizeTreeCheckedKeys(Array.isArray(checkedKeys) ? checkedKeys : checkedKeys?.checked || [])
  if (formData.object_mode === 'ALL') {
    const allDatabaseKeys = getDatabaseLevelKeys()
    const stillAllSelected =
      allDatabaseKeys.length > 0 &&
      allDatabaseKeys.every((key) => objectUi.checkedKeys.includes(key))
    if (!stillAllSelected) {
      formData.object_mode = 'CUSTOM'
      objectUi.checkedKeys = normalizeTreeCheckedKeys(objectUi.checkedKeys)
    }
  }
}

const handleObjectModeChange = async () => {
  objectUi.checkedKeys = []
  objectUi.expandedKeys = []
  if (!formData.source_instance_id || instanceUnavailable.value) {
    objectUi.treeData = []
    return
  }
  await loadLogicalObjectTree()
  if (formData.object_mode === 'CUSTOM' && objectUi.checkedKeys.length === 0) {
    expandCustomObjectTreeToDatabaseLevel()
  }
}

const handleInstanceChange = async () => {
  if (isEditMode.value) {
    nameEdited.value = true
  }
  uiState.instanceErrorPanels = []
  uiState.instanceRuntimeError = ''
  if (isPhysicalDriver.value && !physicalDriverAvailable.value) {
    formData.driver_type = 'LOGICAL'
    message.warning(isKingbaseInstance.value
      ? '金仓暂无物理备份工具链（无 pgBackRest / XtraBackup），已自动切回逻辑备份（sys_dump）。'
      : '当前仅 PostgreSQL / MySQL 支持物理备份，已自动切回逻辑备份。')
  } else if (isPhysicalDriver.value && physicalDriverType.value && formData.driver_type !== physicalDriverType.value) {
    formData.driver_type = physicalDriverType.value
  }
  const instance = currentInstance.value
  const autoName = buildDefaultTaskName(instance)
  if (!nameEdited.value || !formData.name || formData.name === lastAutoName.value) {
    formData.name = autoName
    lastAutoName.value = autoName
    nameEdited.value = false
  }
  objectUi.treeData = []
  objectUi.checkedKeys = []
  objectUi.expandedKeys = []
  if (formData.source_instance_id) {
    await loadLogicalObjectTree()
  }
}

const applyPresetSourceInstance = async () => {
  if (isEditMode.value) return
  if (formData.source_instance_id) return
  const presetId = presetSourceInstanceId.value
  if (!presetId) return
  const matched = instances.value.find((item) => Number(item.value) === presetId)
  if (!matched) return
  formData.source_instance_id = matched.value
  await handleInstanceChange()
}

const retryInstanceCheck = async () => {
  const instance = currentInstance.value
  if (!instance?.id) return
  uiState.instanceChecking = true
  try {
    await testInstanceConnectionApi(instance.id, {
      silentBusinessError: true,
      silentHttpError: true,
    })
    uiState.instanceRuntimeError = ''
    await loadInstances()
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
    message.success('已复制错误信息')
  } catch {
    message.error('复制失败')
  }
}

const editInstanceConnection = () => {
  const instance = currentInstance.value
  if (!instance) return
  const id = `${instance.instance_id || instance.id || ''}`.trim()
  router.push({
    path: '/datasource/instance',
    query: id ? { edit_instance_id: id } : {},
  })
}

const handleWeekdayChange = (value, checked) => {
  if (everyDayChecked.value) return
  const set = new Set(formData.schedule_days)
  if (checked) {
    set.add(value)
  } else {
    set.delete(value)
  }
  formData.schedule_days = weekdayOptions.map((item) => item.value).filter((item) => set.has(item))
  if (isPhysicalDriver.value && formData.driver_config.strategy_mode === 'MIXED') {
    const remainingFull = formData.driver_config.full_backup_days.filter((d) => formData.schedule_days.includes(d))
    if (remainingFull.length < 1) {
      const firstSchedule = formData.schedule_days[0]
      if (firstSchedule) {
        formData.driver_config.full_backup_days = [firstSchedule]
        message.warning('调度日已变更，自动保留最近一个调度日作为全量基准日。')
      }
    } else {
      formData.driver_config.full_backup_days = remainingFull
    }
  }
}

const handleFullDayChange = (value, checked) => {
  if (checked && !formData.schedule_days.includes(value)) {
    message.warning('请先在步骤 3 的调度日中勾选该天，再设为全量基准日。')
    return
  }
  const set = new Set(formData.driver_config.full_backup_days || [])
  if (checked) {
    set.add(value)
  } else {
    if (set.size <= 1) {
      message.warning('至少保留一个全量基准日，避免全周都是增量。')
      return
    }
    set.delete(value)
  }
  formData.driver_config.full_backup_days = weekdayOptions.map((item) => item.value).filter((item) => set.has(item))
}

const handlePhysicalMixedDayChange = (type, value, checked) => {
  if (type === 'FULL') {
    if (checked && !isPhysicalMixedDayChecked('INCR', value)) {
      message.warning('全量执行日和增量执行日不能选择同一天。')
      return
    }
    const set = new Set(formData.driver_config.full_backup_days || [])
    if (checked) {
      set.add(value)
    } else {
      if (set.size <= 1) {
        message.warning('至少保留一个全量执行日。')
        return
      }
      set.delete(value)
    }
    formData.driver_config.full_backup_days = weekdayOptions.map((item) => item.value).filter((item) => set.has(item))
    return
  }
  const set = new Set(formData.driver_config.full_backup_days || [])
  if (checked) {
    if (set.has(value)) {
      if (set.size <= 1) {
        message.warning('至少保留一个全量执行日。')
        return
      }
      set.delete(value)
    }
  } else {
    if (formData.driver_config.full_backup_days.includes(value)) {
      message.warning('全量执行日和增量执行日不能选择同一天。')
      return
    }
    set.add(value)
  }
  formData.driver_config.full_backup_days = weekdayOptions.map((item) => item.value).filter((item) => set.has(item))
}

const applyPresetFullDays = (preset) => {
  const available = [...formData.schedule_days]
  if (preset === 'weekly') {
    const sun = available.includes('SUN') ? 'SUN' : (available[available.length - 1] || 'SUN')
    formData.driver_config.full_backup_days = [sun]
    return
  }
  const mid = available.includes('WED') ? 'WED' : (available[Math.floor(available.length / 2)] || 'WED')
  const weekend = available.includes('SUN') ? 'SUN' : (available[available.length - 1] || 'SUN')
  formData.driver_config.full_backup_days = mid === weekend ? [mid] : [mid, weekend]
}

const driverTypeLabel = (value) => {
  if (['PG_BACKREST', 'XTRABACKUP'].includes(String(value || '').toUpperCase())) return '物理备份'
  return '逻辑备份'
}

const backupTypeLabel = (value, driverType = formData.driver_type) => {
  if (driverType === 'PG_BACKREST') {
    if (formData.driver_config.strategy_mode === 'MIXED') {
      const fullDays = (formData.driver_config.full_backup_days || [])
        .map((d) => weekdayOptions.find((w) => w.value === d)?.label || d)
        .join('、') || '-'
      const otherType = formData.driver_config.other_days_type === 'DIFF' ? '差异' : '增量'
      return `全量：${fullDays}，其余：${otherType}`
    }
    if (value === 'DIFF') return '单一模式：整实例差异备份'
    if (value === 'INCR') return '单一模式：整实例增量备份'
    return '单一模式：整实例全量备份'
  }
  if (driverType === 'XTRABACKUP') {
    if (formData.driver_config.strategy_mode === 'MIXED') {
      const fullDays = (formData.driver_config.full_backup_days || [])
        .map((d) => weekdayOptions.find((w) => w.value === d)?.label || d)
        .join('、') || '-'
      return `全量：${fullDays}，其余：增量`
    }
    if (value === 'INCR') return '单一模式：整实例增量备份'
    return '单一模式：整实例全量备份'
  }
  if (value === 'SCHEMA') return '表结构'
  if (value === 'DATA') return '数据'
  return '表结构 + 数据'
}

const retentionLabel = (value) => {
  if (value === 7) return '7 天'
  if (value === 30) return '30 天'
  if (value === 60) return '60 天'
  if (value === 90) return '90 天'
  if (value === 180) return '半年'
  if (value === 365) return '1 年'
  return `${value} 天`
}

const normalizeScheduleTime = (value) => {
  const [rawHour = '01', rawMinute = '00'] = String(value || '01:00').split(':')
  const hour = String(rawHour).padStart(2, '0')
  const minuteCandidate = String(rawMinute).padStart(2, '0')
  const minute = scheduleMinuteOptions.some((item) => item.value === minuteCandidate) ? minuteCandidate : '00'
  return `${hour}:${minute}`
}

const scheduleHourModel = computed({
  get: () => normalizeScheduleTime(formData.schedule_time).split(':')[0],
  set: (value) => {
    const [, minute] = normalizeScheduleTime(formData.schedule_time).split(':')
    formData.schedule_time = `${String(value || '01').padStart(2, '0')}:${minute}`
  },
})

const scheduleMinuteModel = computed({
  get: () => normalizeScheduleTime(formData.schedule_time).split(':')[1],
  set: (value) => {
    const [hour] = normalizeScheduleTime(formData.schedule_time).split(':')
    const minute = scheduleMinuteOptions.some((item) => item.value === value) ? value : '00'
    formData.schedule_time = `${hour}:${minute}`
  },
})

const validateStep = async (step) => {
  if (step === 0) {
    if (!formData.name.trim()) {
      message.error('请输入任务名称')
      return false
    }
    if (!formData.source_instance_id) {
      message.error('请选择数据源')
      return false
    }
    if (!formData.storage_target_id) {
      message.error('请选择备份存储目标（必填）')
      return false
    }
    const pickedTarget = storageTargets.value.find((t) => t.id === formData.storage_target_id)
    if (pickedTarget) {
      const pickedType = String(pickedTarget.type || '').toUpperCase()
      const pickedIsLocal = pickedType === 'LOCAL_SERVER'
      // 本地服务器：SSH 可达，必须完成 Host Key 指纹确认（防中间人）
      if (pickedIsLocal && !pickedTarget.fingerprint_confirmed) {
        message.error('请先在「存储目标」管理页完成该目标的 SSH 指纹确认后，再选择该存储目标')
        return false
      }
      // 对象存储（OSS/COS/OBS/MinIO）：无 SSH 指纹概念，连通测试通过即可用（与选项可用性判定保持一致）
      if (!pickedIsLocal && String(pickedTarget.connection_status || '').toUpperCase() !== 'OK') {
        message.error('该对象存储目标尚未通过连通测试，请先在「存储目标」管理页完成连通测试后再选择')
        return false
      }
    }
    const dirCheck = resolveOutputDirAgainstTarget()
    if (!dirCheck.ok) {
      message.error(dirCheck.error)
      return false
    }
    formData.output_dir = dirCheck.value
    if (isPhysicalDriver.value) {
      // 物理备份：仓库=存储目标，不需要用户再选备份节点
    } else if (!logicalBackupIncludesSchema.value && !logicalBackupIncludesData.value) {
      message.error('备份对象至少选择一个')
      return false
    }
    return true
  }
  if (step === 1) {
    if (isPhysicalDriver.value) {
      return true
    }
    if (formData.object_mode === 'CUSTOM' && objectUi.checkedKeys.length === 0) {
      message.error('请选择备份对象')
      return false
    }
    return true
  }
  if (step === 2) {
    if (!formData.schedule_days.length) {
      message.error('请选择至少一个执行星期')
      return false
    }
    if (!formData.schedule_time) {
      message.error('请选择执行时间')
      return false
    }
    return true
  }
  return true
}

const buildPayload = (enabled) => {
  const dbType = currentInstance.value?.db_type
  let objectSpec = null
  if (!isPhysicalDriver.value && formData.object_mode === 'CUSTOM') {
    objectSpec = buildObjectSpecFromCheckedKeys(dbType, objectUi.checkedKeys)
  }
  const effectiveExecutorNodeId = isPhysicalDriver.value
    ? (isPostgresPhysicalDriver.value ? (matchedPhysicalExecutorNode.value?.id || formData.executor_node_id || 0) : 0)
    : 0
  const driverConfig = isPostgresPhysicalDriver.value
    ? {
        process_max: Number(formData.driver_config.process_max ?? 2),
        max_runtime_hours: Number(formData.driver_config.max_runtime_hours) || 0,
        start_fast: true,
        stanza: `${formData.driver_config.stanza || ''}`.trim(),
        strategy_mode: `${formData.driver_config.strategy_mode || 'SINGLE'}`.toUpperCase(),
        full_backup_days: Array.isArray(formData.driver_config.full_backup_days)
          ? [...formData.driver_config.full_backup_days]
          : [],
        other_days_type: `${formData.driver_config.other_days_type || 'INCR'}`.toUpperCase(),
        fallback_to_full_when_missing: !!formData.driver_config.fallback_to_full_when_missing,
      }
    : isMySQLPhysicalDriver.value
      ? {
          process_max: Number(formData.driver_config.process_max ?? 2),
          max_runtime_hours: Number(formData.driver_config.max_runtime_hours) || 0,
          os_user: `${formData.driver_config.os_user || ''}`.trim(),
          tool_path: `${formData.driver_config.tool_path || ''}`.trim(),
          strategy_mode: `${formData.driver_config.strategy_mode || 'SINGLE'}`.toUpperCase(),
          full_backup_days: Array.isArray(formData.driver_config.full_backup_days)
            ? [...formData.driver_config.full_backup_days]
            : [],
          other_days_type: 'INCR',
          fallback_to_full_when_missing: !!formData.driver_config.fallback_to_full_when_missing,
        }
    : {
        process_max: Number(formData.driver_config.process_max ?? 2),
        max_runtime_hours: Number(formData.driver_config.max_runtime_hours) || 0,
        os_user: `${formData.driver_config.os_user || ''}`.trim(),
        tool_path: `${formData.driver_config.tool_path || ''}`.trim(),
        // 压缩策略仅对 MySQL 逻辑备份生效（PG 的 pg_dump -Fc 自带压缩，物理备份走 xbstream/json）
        ...(String(dbType || '').toUpperCase() === 'MYSQL'
          ? {
              compress_enabled: formData.driver_config.compress_enabled !== false,
              compress_level:
                `${formData.driver_config.compress_level || 'FAST'}`.toUpperCase() === 'HIGH' ? 'HIGH' : 'FAST',
            }
          : {}),
      }
  return {
    task_id: effectiveTaskId.value || undefined,
    name: formData.name.trim(),
    source_instance_id: formData.source_instance_id,
    storage_target_id: Number(formData.storage_target_id) || 0,
    executor_node_id: effectiveExecutorNodeId,
    repository_node_id: isPostgresPhysicalDriver.value ? (formData.repository_node_id || 0) : 0,
    driver_type: formData.driver_type,
    driver_config: driverConfig,
    backup_type: formData.backup_type,
    object_mode: isPhysicalDriver.value ? 'ALL' : formData.object_mode,
    object_spec: objectSpec,
    output_dir: `${formData.output_dir || ''}`.trim(),
    schedule_days: [...formData.schedule_days],
    schedule_time: formData.schedule_time,
    retention_days: formData.retention_days,
    enabled,
  }
}

const formatPrecheckDetail = (detail) => {
  if (!detail || typeof detail !== 'object') return ''
  return Object.entries(detail)
    .map(([key, value]) => [key, formatPrecheckValue(value)])
    .filter(([, value]) => value.trim() !== '')
    .map(([key, value]) => `${key}: ${value}`)
    .join('\n')
}

const precheckHintTagColor = (item) => {
  const key = `${item?.item_key || ''}`
  if (key.includes('auto_repair')) {
    return item?.passed ? 'blue' : 'orange'
  }
  return 'orange'
}

const precheckHintTagText = (item) => {
  const key = `${item?.item_key || ''}`
  if (key.includes('auto_repair')) {
    return item?.passed ? '已自动修复' : '自动处理'
  }
  return '提示'
}

const handleNext = async () => {
  const valid = await validateStep(currentStep.value)
  if (!valid) return
  if (currentStep.value === 2) {
    await runStartPrecheck(true)
    return
  }
  currentStep.value += 1
}

const runStartPrecheck = async (moveToStep = false) => {
  const callConfig = { silentBusinessError: true, skipGlobalMessageError: true, timeout: 180_000, disableNetRetry: true }
  if (moveToStep) {
    currentStep.value = 3
  }
  clearPrecheckAnimation()
  resetPrecheckResult()
  uiState.prechecking = true

  const existingItems = uiState.precheckResult.items || []
  if (existingItems.length > 0) {
    precheckDisplayItems.value = existingItems.map((item) => ({
      ...item,
      status: 'PENDING',
    }))
  } else {
    precheckDisplayItems.value = []
  }
  startPrecheckAnimation()

  try {
    const res = await precheckBackupTaskApi(buildPayload(true), callConfig)
    const data = res?.data || res || {}
    previewTaskId.value = `${data.task_id || data.resolved?.task_id || previewTaskId.value || ''}`.trim()
    uiState.precheckResult.summary = `${data.summary || ''}`.trim()
    uiState.precheckResult.items = Array.isArray(data.items) ? data.items : []
    uiState.precheckResult.resolved = data.resolved || null
    precheckDisplayItems.value = []
    const repaired = uiState.precheckResult.items.find((item) => `${item?.item_key || ''}`.includes('auto_repair') && item?.passed)
    const repairTried = uiState.precheckResult.items.find((item) => `${item?.item_key || ''}`.includes('auto_repair') && !item?.passed)
    if (repaired) {
      message.success(repaired.message || '系统已自动修复离线执行节点')
    } else if (repairTried) {
      message.warning(repairTried.message || '系统已尝试自动处理离线节点，请查看失败项详情')
    }
  } catch (err) {
    const errorMessage = err?.message || '预校验失败，请重试'
    uiState.precheckResult.error = errorMessage
    uiState.precheckResult.summary = errorMessage
    uiState.precheckResult.items = []
    uiState.precheckResult.resolved = null
    precheckDisplayItems.value = []
    message.error(errorMessage)
  } finally {
    clearPrecheckAnimation()
    uiState.prechecking = false
  }
}

const handleStartSubmit = async () => {
  if (instanceUnavailable.value) {
    message.error('实例不可用，无法提交')
    return
  }
  const steps = [0, 1, 2]
  for (const step of steps) {
    const valid = await validateStep(step)
    if (!valid) {
      currentStep.value = step
      return
    }
  }
  if (!precheckCompleted.value || hasBlockingPrecheckFailures.value) {
    currentStep.value = 3
    message.error('请先完成启动前预校验，并确保全部强制校验项通过')
    return
  }
  await submitTask(true)
}

const submitTask = async (enabled) => {
  if (instanceUnavailable.value) {
    message.error('实例不可用，无法提交')
    return
  }
  const steps = [0, 1, 2]
  for (const step of steps) {
    const valid = await validateStep(step)
    if (!valid) {
      currentStep.value = step
      return
    }
  }
  if (!precheckCompleted.value || hasBlockingPrecheckFailures.value) {
    currentStep.value = 3
    message.error('备份任务需先通过启动前预校验，才可启动')
    return
  }
  clearSubmitPrepareError()
  uiState.lastSubmitEnabled = enabled
  uiState.submitting = true
  const callConfig = { silentBusinessError: true, skipGlobalMessageError: true, timeout: 180_000, disableNetRetry: true }
  let createdTaskId = ''
  try {
    if (isEditMode.value) {
      await updateBackupTaskWithRetentionConfirm(
        updateBackupTaskApi,
        { id: taskId.value, ...buildPayload(enabled) },
        callConfig,
      )
      message.success(enabled ? '备份任务已保存并启动' : '备份任务已保存，当前为未启动状态')
      router.push('/backup/task')
    } else {
      const createRes = await createBackupTaskApi(buildPayload(enabled), callConfig)
      const taskData = createRes?.data || createRes || {}
      createdTaskId = `${taskData.task_id || ''}`.trim()
      // 任务级 prepare（拓扑校验异步化）只有 PG_BACKREST 实现：
      // 后端创建接口仅对 PG_BACKREST 触发 AsyncPrepareTask（views/backup.go），
      // 且 AsyncPrepareTask 对非 PG 驱动直接置 NONE 返回——XTRABACKUP 没有 prepare 阶段，
      // 其拓扑校验在「启动前预校验」步骤同步完成。若对 XTRABACKUP 也轮询，
      // prepare_status 恒为 NONE，会白等 80×3s 后弹「轮询超时」（2026-10-06 bk-gfgyicvazvtk 实锤）。
      const needPollPrepare = isPostgresPhysicalDriver.value && createdTaskId
      if (needPollPrepare) {
        uiState.lastSubmitEnabled = enabled
        let pollCount = 0
        const POLL_MAX = 80
        const POLL_INTERVAL_MS = 3000
        const pollTimer = setInterval(async () => {
          try {
            pollCount += 1
            const detail = await getBackupTaskDetailApi(createdTaskId, { silentBusinessError: true, skipGlobalMessageError: true, timeout: 15_000, disableNetRetry: true })
            const taskObj = detail?.data || detail || {}
            const ps = `${taskObj.prepare_status || ''}`.trim()
            if (ps === 'PREPARE_OK' || pollCount >= POLL_MAX) {
              clearInterval(pollTimer)
              uiState.submitting = false
              if (pollCount >= POLL_MAX && ps !== 'PREPARE_OK') {
                uiState.submitPrepareError.visible = true
                uiState.submitPrepareError.stage = 'timeout'
                uiState.submitPrepareError.title = '任务级拓扑准备轮询超时'
                uiState.submitPrepareError.suggestion = '后台准备仍在进行中，可点击“重试”继续轮询，或直接跳转到任务列表查看最新状态'
                uiState.submitPrepareError.detail = `已轮询 ${POLL_MAX} 次（约 ${Math.floor(POLL_MAX * POLL_INTERVAL_MS / 1000)} 秒）仍未达到终止态，当前 prepare_status=${ps || 'NONE'}`
                uiState.submitPrepareError.raw_error = ''
                if (currentStep.value < reviewStepIndex.value) currentStep.value = reviewStepIndex.value
              } else {
                message.success(enabled ? '备份任务已创建并启动' : '备份任务已保存，当前为未启动状态')
                router.push('/backup/task')
              }
              return
            }
            if (ps === 'PREPARE_ERROR') {
              clearInterval(pollTimer)
              uiState.submitting = false
              uiState.submitPrepareError.visible = true
              uiState.submitPrepareError.stage = taskObj.prepare_stage || ''
              uiState.submitPrepareError.title = taskObj.prepare_title || '备份程序准备失败'
              uiState.submitPrepareError.suggestion = taskObj.prepare_suggestion || ''
              uiState.submitPrepareError.detail = taskObj.prepare_detail || ''
              uiState.submitPrepareError.raw_error = taskObj.prepare_raw_error || ''
              if (!uiState.submitPrepareError.title) {
                uiState.submitPrepareError.title = '备份程序准备失败'
              }
              if (currentStep.value < reviewStepIndex.value) currentStep.value = reviewStepIndex.value
              return
            }
            const errMsg = `${taskObj.error_message || ''}`.trim()
            const taskStatus = `${taskObj.status || ''}`.trim()
            const enabledFlag = `${taskObj.enabled || ''}`.trim()
            if (errMsg || (taskStatus && ['FAILED','STOPPED','TIMEOUT','CANCELLED'].includes(taskStatus))) {
              const hasSchemaHint = errMsg && (errMsg.includes('001_add_prepare_columns') || errMsg.includes('schema_missing') || errMsg.includes('schema_probe') || errMsg.includes('Unknown column') || errMsg.includes('Data truncated'))
              clearInterval(pollTimer)
              uiState.submitting = false
              uiState.submitPrepareError.visible = true
              uiState.submitPrepareError.stage = taskObj.prepare_stage || (hasSchemaHint ? 'schema_missing' : (taskStatus ? `task_status_${taskStatus.toLowerCase()}` : 'task_fallback'))
              uiState.submitPrepareError.title = taskObj.prepare_title || (hasSchemaHint ? '数据库迁移脚本尚未执行（prepare 列缺失）' : (taskStatus ? `备份任务状态异常：${taskStatus}` : '备份任务执行失败'))
              uiState.submitPrepareError.suggestion = taskObj.prepare_suggestion || (hasSchemaHint ? '请先在 192.168.53.102 MySQL insight 库执行 001_add_prepare_columns.sql 迁移脚本（tasks+runs 九列+PENDING_PREPARE 枚举+双索引）后，再重新点击“立即执行”。' : `任务级状态已变为 ${taskStatus || 'FAILED'}，请查看详细错误后调整重试。`)
              uiState.submitPrepareError.detail = taskObj.prepare_detail || [
                errMsg ? `任务 error_message：${errMsg}` : '',
                taskStatus ? `任务 status：${taskStatus}` : '',
                enabledFlag ? `任务 enabled：${enabledFlag}` : '',
                ps ? `prepare_status：${ps}` : `prepare_status：<空或 NONE>（prepare_* 列可能不存在，已自动兜底）`,
              ].filter(Boolean).join('\n')
              uiState.submitPrepareError.raw_error = taskObj.prepare_raw_error || errMsg || taskStatus || ''
              if (!uiState.submitPrepareError.title) {
                uiState.submitPrepareError.title = '备份程序准备失败'
              }
              if (currentStep.value < reviewStepIndex.value) currentStep.value = reviewStepIndex.value
              return
            }
          } catch (_) {}
        }, POLL_INTERVAL_MS)
        return
      }
      message.success(enabled ? '备份任务已创建并启动' : '备份任务已保存，当前为未启动状态')
      router.push('/backup/task')
    }
  } catch (err) {
    uiState.submitting = false
    // 用户在保留期影响确认框点了取消：请求未提交，静默返回
    if (isRetentionReconcileCanceled(err)) return
    const structured = err?.data
    if (structured && typeof structured === 'object' && structured.stage) {
      uiState.submitPrepareError.visible = true
      uiState.submitPrepareError.stage = structured.stage || ''
      uiState.submitPrepareError.title = structured.title || err?.message || ''
      uiState.submitPrepareError.suggestion = structured.suggestion || ''
      uiState.submitPrepareError.detail = structured.detail || ''
      uiState.submitPrepareError.raw_error = structured.raw_error || ''
      if (!uiState.submitPrepareError.title) {
        uiState.submitPrepareError.title = err?.message || '备份程序准备失败'
      }
      if (currentStep.value < reviewStepIndex.value) {
        currentStep.value = reviewStepIndex.value
      }
    } else {
      const msg = err?.message || String(err || '').trim() || '提交失败，请重试'
      message.error(msg)
    }
  }
}

const clearSubmitPrepareError = () => {
  uiState.submitPrepareError.visible = false
  uiState.submitPrepareError.stage = ''
  uiState.submitPrepareError.title = ''
  uiState.submitPrepareError.suggestion = ''
  uiState.submitPrepareError.detail = ''
  uiState.submitPrepareError.raw_error = ''
}

const copySubmitPrepareError = async () => {
  const parts = []
  if (uiState.submitPrepareError.title) parts.push(`标题：${uiState.submitPrepareError.title}`)
  if (uiState.submitPrepareError.suggestion) parts.push(`建议：${uiState.submitPrepareError.suggestion}`)
  if (uiState.submitPrepareError.stage) parts.push(`阶段：${uiState.submitPrepareError.stage}`)
  if (uiState.submitPrepareError.detail) parts.push(`详细：${uiState.submitPrepareError.detail}`)
  if (uiState.submitPrepareError.raw_error && uiState.submitPrepareError.raw_error !== uiState.submitPrepareError.detail) {
    parts.push(`原始错误：${uiState.submitPrepareError.raw_error}`)
  }
  const text = parts.join('\n') || '暂无错误信息'
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
    message.success('已复制错误详情')
  } catch {
    message.error('复制失败')
  }
}

const retrySubmitTask = async () => {
  await submitTask(uiState.lastSubmitEnabled)
}

const rerunPrecheck = async () => {
  await runStartPrecheck(true)
}

const handleBack = () => {
  Modal.confirm({
    title: isEditMode.value ? '放弃当前编辑？' : '放弃当前创建？',
    content: isEditMode.value ? '未保存的备份任务修改将丢失，是否返回任务列表？' : '未保存的备份任务配置将丢失，是否返回任务列表？',
    okText: '返回列表',
    cancelText: '继续编辑',
    onOk: () => router.push('/backup/task'),
  })
}

const resolveInitialStep = () => {
  const raw = Number(route.query.step)
  if (!Number.isFinite(raw)) return 0
  return Math.max(0, Math.min(stepTitles.length - 1, Math.floor(raw)))
}

const loadTaskDetail = async () => {
  if (!taskId.value) return
  const res = await getBackupTaskDetailApi(taskId.value)
  const data = res?.data || {}
  formData.name = `${data.name || ''}`
  formData.source_instance_id = data.source_instance_id || null
  formData.storage_target_id = Number(data.storage_target_id) || null
  formData.executor_node_id = data.executor_node_id || null
  formData.repository_node_id = data.repository_node_id || null
  formData.driver_type = data.driver_type || 'LOGICAL'
  formData.driver_config.process_max = Number(data.driver_config?.process_max ?? 2)
  formData.driver_config.max_runtime_hours = Number(data.driver_config?.max_runtime_hours) || null
  formData.driver_config.os_user = `${data.driver_config?.os_user || data.driver_config?.process_user || ''}`
  formData.driver_config.tool_path = `${data.driver_config?.tool_path || data.driver_config?.dump_path || data.driver_config?.mysqldump_path || data.driver_config?.pg_dump_path || ''}`
  formData.driver_config.compress_enabled = data.driver_config?.compress_enabled !== false
  formData.driver_config.compress_level = `${data.driver_config?.compress_level || 'FAST'}`.toUpperCase() === 'HIGH' ? 'HIGH' : 'FAST'
  formData.driver_config.start_fast = true
  formData.driver_config.stanza = `${data.driver_config?.stanza || ''}`
  formData.driver_config.strategy_mode = `${data.driver_config?.strategy_mode || 'SINGLE'}`.toUpperCase() || 'SINGLE'
  if (['PG_BACKREST', 'XTRABACKUP'].includes(String(formData.driver_type || '').toUpperCase()) && formData.driver_config.strategy_mode === 'MIXED') {
    formData.schedule_days = [...weekdayOptions.map((item) => item.value)]
  }
  formData.driver_config.full_backup_days = Array.isArray(data.driver_config?.full_backup_days) && data.driver_config.full_backup_days.length > 0
    ? [...data.driver_config.full_backup_days]
    : [...PHYSICAL_MIXED_DEFAULT_FULL_DAYS]
  formData.driver_config.other_days_type = `${data.driver_config?.other_days_type || 'INCR'}`.toUpperCase() || 'INCR'
  formData.driver_config.fallback_to_full_when_missing = data.driver_config?.fallback_to_full_when_missing !== false
  formData.backup_type = data.backup_type || 'FULL'
  formData.object_mode = data.object_mode || 'ALL'
  formData.output_dir = `${data.output_dir || ''}`
  formData.schedule_days = Array.isArray(data.schedule_days) ? data.schedule_days : ['MON', 'WED', 'FRI', 'SUN']
  formData.schedule_time = normalizeScheduleTime(data.schedule_time || '01:00')
  formData.retention_days = data.retention_days || 30
  if (data.last_precheck_result && typeof data.last_precheck_result === 'object') {
    uiState.precheckResult.summary = `${data.last_precheck_result.summary || ''}`.trim()
    uiState.precheckResult.items = Array.isArray(data.last_precheck_result.items) ? data.last_precheck_result.items : []
    uiState.precheckResult.resolved = data.last_precheck_result.resolved || null
    uiState.precheckResult.error = data.last_precheck_status === 'FAILED'
      ? (`${data.last_precheck_result.summary || ''}`.trim() || '最近一次预检查未通过')
      : ''
  }

  nameEdited.value = true
  lastAutoName.value = ''

  if (formData.source_instance_id) {
    await loadLogicalObjectTree()
    if (!isPhysicalDriver.value && formData.object_mode === 'CUSTOM') {
      const dbType = currentInstance.value?.db_type
      const keys = normalizeTreeKeysFromSpec(dbType, data.object_spec || {})
      await hydrateTreeForCheckedKeys(keys)
      objectUi.checkedKeys = keys
      expandCustomObjectTreeToDatabaseLevel()
    }
  }
}

onMounted(async () => {
  await Promise.all([loadInstances(), loadNodes(), loadStorageTargets()])
  await applyPresetSourceInstance()
  await loadTaskDetail()
  currentStep.value = resolveInitialStep()
  if (currentStep.value === 3) {
    await runStartPrecheck()
  }
})
</script>

<style scoped>
html, body, #app {
  height: 100%;
  margin: 0;
  padding: 0;
}

/* 高级选项折叠面板：与表单卡片融为一体，不额外撑高卡片 */
.advanced-options-collapse {
  background: transparent;
}
.advanced-options-collapse :deep(.ant-collapse-item) {
  border: 0;
}
.advanced-options-collapse :deep(.ant-collapse-header) {
  padding: 4px 0 !important;
  color: #1f7a6f;
  font-weight: 500;
}
.advanced-options-collapse :deep(.ant-collapse-content-box) {
  padding: 4px 0 0 !important;
}

.backup-task-create-page {
  padding-top: 10px;
  padding-bottom: 10px;
  box-sizing: border-box;
}

/* 第四步「启动前预校验」：满高布局——步骤条与底部操作栏固定，滚动条只作用于表格区域。
   （配合脚本里 setLayoutContentNoScroll(true)，layout-content-inner 此时 height:100%） */
.backup-task-create-page.is-precheck-step {
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

.backup-task-create-page.is-precheck-step .create-body {
  flex: 1;
  min-height: 0;
}

.backup-task-create-page.is-precheck-step .create-main-card {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.backup-task-create-page.is-precheck-step .create-main-card :deep(.ant-card-body) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.backup-task-create-page.is-precheck-step .step-panel {
  flex: 1;
  min-height: 0;
}

.create-steps-card {
  margin-bottom: 8px;
  border-radius: 16px;
  flex-shrink: 0;
}

.create-steps-card :deep(.ant-card-body) {
  padding-top: 12px;
  padding-bottom: 12px;
}

.create-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.create-main-card {
  border-radius: 16px;
}

.create-main-card :deep(.ant-card-body) {
  padding-top: 12px;
  padding-bottom: 12px;
}

.step-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.step1-layout-grid {
  display: grid;
  /* 两列等宽，保证「任务基础信息」与「备份参数」卡片视觉对齐 */
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 16px;
  /* 两列卡片随内容自适应，且始终保持等高：任一列（如展开高级选项）变高，
     另一列卡片同步撑开到相同高度 */
  align-items: stretch;
}

.step1-col {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.section-block {
  flex-shrink: 0;
  width: 100%;
  box-sizing: border-box;
  padding: 20px;
  border-radius: 16px;
  background: linear-gradient(180deg, #fbfcff 0%, #f8faff 100%);
  border: 1px solid rgba(24, 144, 255, 0.08);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.02), 0 4px 12px rgba(24, 144, 255, 0.03);
}

.step1-col-left,
.step1-col-right {
  box-sizing: border-box;
}

.step1-col-left > .section-block,
.step1-col-right > .section-block {
  /* 撑满列高：网格行高由内容较多的一列决定，卡片本身必须 flex:1 才能跟随等高 */
  flex: 1 1 auto;
  width: 100%;
  box-sizing: border-box;
}

.section-block-fill {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* 紧凑表单：任务基础信息的表单项纵向间距收紧（antd 垂直布局默认 24px 过大） */
.task-basic-info-block :deep(.ant-form-item) {
  margin-bottom: 14px;
}

.storage-target-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.storage-target-form :deep(.ant-form-item) {
  margin-bottom: 0;
}

.driver-and-backup-type-row > * {
  margin-bottom: 0;
}

.physical-strategy-section {
  margin-top: 16px;
}

.driver-col-35 {
  flex: 0 0 50%;
  max-width: 50%;
}

.driver-col-65 {
  flex: 0 0 50%;
  max-width: 50%;
}

@media (max-width: 991px) {
  .step1-layout-grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .driver-col-35,
  .driver-col-65 {
    flex: 0 0 100%;
    max-width: 100%;
  }
}

.driver-and-backup-type-row :deep(.ant-form-item) {
  margin-bottom: 0;
}

.selection-form-item-disabled :deep(.ant-form-item-label > label),
.selection-form-item-disabled :deep(.ant-form-item-required) {
  color: #bfbfbf !important;
}

.selection-card-group {
  display: flex;
  flex-wrap: nowrap;
  gap: 10px;
  width: 100%;
  min-width: 0;
}

.selection-card {
  flex: 1 1 0;
  min-width: 0;
  padding: 9px 10px;
  border: 1px solid #d9dde3;
  border-radius: 10px;
  background: #fff;
  color: #1f2329;
  font-size: 14px;
  line-height: 22px;
  cursor: pointer;
  text-align: center;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  transition: all 0.2s ease;
}

.selection-card:hover:not(.disabled) {
  border-color: rgba(15, 118, 110, 0.45);
}

.selection-card.active {
  border-color: var(--gi-color-primary, #0f766e);
  color: var(--gi-color-primary, #0f766e);
  background: rgba(15, 118, 110, 0.04);
}

.selection-card.disabled {
  color: #bfbfbf;
  background: #fafafa;
  cursor: not-allowed;
}

.selection-card.active.disabled,
.selection-card.active:disabled {
  color: var(--gi-color-primary, #0f766e);
  border-color: var(--gi-color-primary, #0f766e);
  background: rgba(15, 118, 110, 0.04);
}

.selection-card:disabled {
  cursor: not-allowed;
}

@media (max-width: 1400px) {
  .selection-card-group {
    gap: 8px;
  }

  .selection-card {
    padding: 8px 8px;
    font-size: 13px;
    line-height: 20px;
    border-radius: 9px;
  }
}

@media (max-width: 1200px) {
  .selection-card {
    padding: 7px 6px;
    font-size: 12px;
    line-height: 18px;
    border-radius: 8px;
  }
}

.backup-type-group {
  display: inline-flex !important;
  flex-wrap: nowrap;
  justify-content: flex-start;
  width: 100%;
  max-width: 100%;
  overflow: hidden;
}

.backup-type-group .ant-radio-button-wrapper {
  flex: 1 1 0;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  padding-left: 8px;
  padding-right: 8px;
  text-align: center;
}

.step-actions-inline {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  background: linear-gradient(135deg, #fbfdff 0%, #f2f8ff 100%);
}

.step-actions-inline .step-actions-left {
  flex: 1;
}

.step-actions-inline .step-actions-right {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
}

.section-block,
.tree-card,
.schedule-card,
.review-grid .ant-card {
  padding: 16px;
  border: 1px solid #e8edf2;
  border-radius: 16px;
  background: #fafcff;
}

.schedule-card {
  padding: 0;
}

.schedule-card > .ant-card-body {
  padding: 16px;
}

.strategy-nested-block {
  margin-top: 0;
  padding: 14px 16px;
  border: 1px solid #e8edf2;
  border-radius: 16px;
  background: #f7faff;
}

.physical-executor-info {
  margin-bottom: 2px;
}

.physical-executor-info .ant-descriptions-item-label {
  width: 120px;
  background: #f7faff;
}

.executor-node-name {
  margin-left: 8px;
  font-size: 13px;
  font-weight: 500;
  color: #1f2937;
}

.executor-status {
  margin-left: 10px;
  font-size: 12px;
  font-weight: 500;
}

.executor-status.is-online {
  color: #16a34a;
}

.executor-missing-text {
  display: inline-block;
  margin-left: 8px;
  font-size: 13px;
  line-height: 1.7;
  color: #7f1d1d;
}

.physical-executor-hint {
  margin-top: 2px;
  margin-bottom: 16px;
}

.submit-prepare-error-alert {
  margin-top: 18px;
}

.submit-error-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.submit-error-suggestion {
  color: #3f3f3f;
  font-size: 13px;
  line-height: 1.8;
}

.submit-error-actions {
  margin-top: 2px;
}

.section-title {
  margin-bottom: 12px;
  font-size: 16px;
  font-weight: 600;
  color: #1f2329;
}

.step-form-compact {
  width: 100%;
  max-width: 760px;
}

.form-tip,
.tree-subtitle,
.review-tip {
  color: #8c8c8c;
  font-size: 12px;
  line-height: 1.7;
}

.instance-selector-row {
  display: flex;
  gap: 12px;
}

.instance-selector {
  flex: 1;
}

.instance-unavailable-alert {
  margin-top: 10px;
}

.tree-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 12px;
}

.tree-toolbar-main {
  min-width: 0;
}

.tree-toolbar-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.object-mode-select {
  width: 160px;
}

.tree-title {
  font-size: 14px;
  font-weight: 600;
}

.tree-card :deep(.ant-tree-checkbox-indeterminate .ant-tree-checkbox-inner::after) {
  top: 50%;
  left: 21.5%;
  width: 5px;
  height: 8px;
  border: 2px solid #fff;
  border-top: 0;
  border-left: 0;
  background: transparent;
  box-sizing: border-box;
  transform: rotate(45deg) scale(1) translate(-50%, -50%);
  opacity: 1;
}

.tree-card :deep(.ant-tree-checkbox-indeterminate .ant-tree-checkbox-inner) {
  background-color: var(--gi-color-primary, #0f766e);
  border-color: var(--gi-color-primary, #0f766e);
}

.tree-card :deep(.object-tree-title) {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.tree-card :deep(.object-tree-title-suffix) {
  color: #8c8c8c;
}

.tree-card :deep(.object-tree-empty-text) {
  color: #8c8c8c;
}

.schedule-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}

.schedule-card {
  border-radius: 16px;
  background: #fafcff;
}

.instance-unavailable-pre {
  margin: 0;
  max-height: 160px;
  overflow: auto;
  padding: 12px 14px;
  border-radius: 14px;
  background: rgba(0, 0, 0, 0.03);
  white-space: pre-wrap;
  word-break: break-word;
}

.weekday-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.weekday-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}

.weekday-tags.disabled {
  opacity: 0.45;
  pointer-events: none;
}

.retention-group {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.schedule-time-editor {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.schedule-time-select {
  width: 96px;
}

.schedule-time-sep {
  color: #5f6b7a;
  font-size: 13px;
  line-height: 1;
}

.strategy-nested-block {
  margin-top: 0;
  padding: 16px 18px;
  border: 1px solid #e6f0ff;
  border-radius: 12px;
  background: linear-gradient(135deg, #fbfdff 0%, #f5faff 100%);
}

.mixed-strategy-block .ant-form-item {
  margin-bottom: 16px;
}

.mixed-strategy-block .ant-form-item:last-child {
  margin-bottom: 0;
}

.fallback-tip {
  margin-left: 12px;
  vertical-align: middle;
}

.review-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}

.review-item {
  display: flex;
  align-items: flex-start;
  justify-content: flex-start;
  gap: 0;
  padding: 10px 0;
  border-bottom: 1px dashed #f0f0f0;
}

.review-label {
  flex: 0 0 auto;
  color: #1f2329;
}

.review-value {
  flex: 1 1 auto;
  min-width: 0;
  color: #8c8c8c;
  word-break: break-word;
}

.review-item:last-child {
  border-bottom: none;
}

.step-actions {
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  align-items: center;
  gap: 12px;
  margin-top: 12px;
  padding-top: 14px;
  border-top: 1px dashed rgba(0,0,0,0.06);
  flex-shrink: 0;
}

.step-actions-left {
  display: flex;
  align-items: center;
  justify-content: flex-start;
}

.step-actions-center {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  grid-column-start: 2;
}

.step-actions-right {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
}

.step-nav-btn {
  background: #fff;
  border: 1px solid #d9dde3;
  color: #1f2329;
  box-shadow: none;
}

.step-nav-btn:hover,
.step-nav-btn:focus,
.step-nav-btn:active {
  background: #fff;
  border-color: var(--gi-color-primary, #0f766e);
  color: var(--gi-color-primary, #0f766e);
}

.step-actions :deep(.ant-btn-icon) {
  color: inherit;
  opacity: 1;
}

.submit-disabled-hint {
  margin-top: 6px;
  text-align: right;
  color: rgba(0, 0, 0, 0.45);
  font-size: 12px;
}

.precheck-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.precheck-section__title {
  color: #0f766e;
  font-size: 14px;
  font-weight: 600;
}

.precheck-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.precheck-item {
  border: 1px solid #d9f2ef;
  border-radius: 10px;
  padding: 12px 14px;
  background: #f8fffe;
}

.precheck-item.failed {
  border-color: #ffccc7;
  background: #fff8f7;
}

.precheck-item.warning {
  border-color: #ffe7ba;
  background: #fffdf7;
}

.precheck-item__head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.precheck-item__label {
  color: #1f2329;
  font-size: 14px;
  font-weight: 600;
}

.precheck-item__message {
  color: #4e5969;
  line-height: 1.6;
}

.precheck-item__detail {
  margin: 8px 0 0;
  padding: 10px 12px;
  border-radius: 8px;
  background: rgba(15, 118, 110, 0.06);
  color: #4e5969;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}

/* 预校验表格样式：区块撑满剩余高度，滚动条只出现在表格容器内 */
.precheck-table-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
  flex: 1;
  min-height: 0;
}

.precheck-table-section__title {
  display: flex;
  align-items: center;
  gap: 10px;
  color: #0f766e;
  font-size: 14px;
  font-weight: 600;
  flex-shrink: 0;
}

.precheck-stats {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  font-weight: 500;
}

.precheck-stats__item {
  display: inline-flex;
  align-items: center;
  padding: 1px 10px;
  border-radius: 999px;
  line-height: 1.7;
}

.precheck-stats__item.is-success {
  color: #0f766e;
  background: #e6f4f1;
}

.precheck-stats__item.is-failed {
  color: #cf1322;
  background: #fff1f0;
}

.precheck-stats__item.is-muted {
  color: #8a97a1;
  background: #f2f4f5;
}

.precheck-table-wrapper {
  width: 100%;
  max-width: 100%;
  /* 唯一滚动容器：纵向滚动条只作用于表格区域，表头经 sticky 吸顶固定 */
  flex: 1;
  min-height: 0;
  overflow: auto;
  background: #fff;
  border-radius: 10px;
  border: 1px solid #e8e9eb;
}

.precheck-table-wrapper :deep(.ant-table-small) {
  border-radius: 10px;
}

/* 表头固定：表格体滚动时表头行吸顶（antd 表格默认 border-collapse: separate，sticky 边框不丢） */
.precheck-table-wrapper :deep(.ant-table-thead > tr > th) {
  position: sticky;
  top: 0;
  z-index: 2;
}

.precheck-status-tag {
  display: inline-flex;
  align-items: center;
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.6;
}

.precheck-status-tag.is-running {
  color: #165996;
  background: rgba(22, 89, 150, 0.08);
}

.precheck-status-tag.is-pending {
  color: #4b5563;
  background: rgba(75, 85, 99, 0.08);
}

.precheck-status-tag.is-passed {
  color: var(--gi-color-primary, #0f766e);
  background: color-mix(in srgb, var(--gi-color-primary, #0f766e) 8%, transparent);
}

.precheck-status-tag.is-warning {
  color: #b45309;
  background: rgba(180, 83, 9, 0.08);
}

.precheck-status-tag .anticon {
  font-size: 12px;
}

/*
 * 检查结果 / 操作 两列的单元格内容由 customRender 里的 h() 渲染，
 * 节点上没有本组件的 data-v scopeId —— 上面这组 scoped 规则（含 is-failed 的
 * 底色徽标）实际从未命中，所以线上一直呈现「无底色纯文本」。
 * 2026-10-25 按需求只补两条：失败标红、详情去主题色。必须从模板内的
 * .precheck-table-wrapper 出发用 :deep() 穿透（编译成 [data-v-x] 后代选择器）。
 * 其余状态徽标（running/pending/passed/warning）按「其他不变」维持现状未启用；
 * 若要恢复带底色徽标，需同样改成 :deep() 写法。
 */
.precheck-table-wrapper :deep(.precheck-status-tag.is-failed) {
  color: var(--gi-color-error, #ff4d4f);
}

.precheck-table-wrapper :deep(.precheck-detail-link) {
  color: inherit; /* 去掉全局 a 的主题色，按普通文本展示 */
}

.precheck-table-wrapper :deep(.precheck-detail-link:hover) {
  text-decoration: underline; /* 保留可点暗示，但不引入颜色 */
}

.precheck-no-action {
  color: #c9cdd4;
}

.precheck-detail-modal-body {
  margin: 0;
  padding: 16px;
  border-radius: 8px;
  background: rgba(15, 118, 110, 0.06);
  color: #4e5969;
  font-size: 13px;
  line-height: 1.7;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 480px;
  overflow-y: auto;
}

@media (max-width: 1100px) {
  .schedule-grid,
  .review-grid {
    grid-template-columns: 1fr;
  }

  .step-form-compact {
    max-width: 100%;
  }

  .tree-toolbar {
    align-items: flex-start;
    flex-direction: column;
  }

  .tree-toolbar-actions {
    width: 100%;
  }

  .object-mode-select {
    flex: 1;
  }
}

@media (max-width: 820px) {
  .step-actions {
    grid-template-columns: 1fr;
    row-gap: 10px;
  }

  .step-actions-left,
  .step-actions-center,
  .step-actions-right {
    justify-content: center;
    grid-column-start: unset;
  }
}

.instance-select-option {
  display: flex;
  flex-direction: column;
  justify-content: center;
  line-height: 1.3;
  padding: 2px 0;
}

.instance-select-option__name {
  color: #1f2329;
  font-size: 14px;
  font-weight: 500;
  line-height: 20px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.instance-select-option__addr {
  color: #8c8c8c;
  font-size: 12px;
  line-height: 18px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

:deep(.instance-select-dropdown--two-line .ant-select-item-option-content) {
  padding-top: 4px;
  padding-bottom: 4px;
  height: auto;
  line-height: 1.3;
}

:deep(.instance-select-dropdown--two-line .ant-select-item-option) {
  height: auto;
  line-height: 1.3;
  padding-top: 4px;
  padding-bottom: 4px;
}
</style>
