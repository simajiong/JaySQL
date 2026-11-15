<template>
  <a-modal :open="open" title="任务详情" width="60%" @cancel="handleCancel" :footer="null">
    <a-tabs v-model:activeKey="activeTab" class="task-detail-tabs">
      <!-- 执行信息：耗时统计 + 导出文件信息 + 错误信息（均按原有条件渲染迁移） -->
      <a-tab-pane key="info" tab="执行信息">
        <a-row :gutter="[16, 16]">
          <a-col :xs="24" :sm="12" :md="8">
            <div class="stat-item">
              <a-statistic title="SQL执行耗时" :value="parseCostMs(formData?.result?.execute_cost_time) ?? '-'">
                <template #prefix>
                  <ThunderboltOutlined style="color: #1890ff" />
                </template>
                <template #suffix v-if="parseCostMs(formData?.result?.execute_cost_time) !== null">ms</template>
              </a-statistic>
            </div>
          </a-col>
          <a-col :xs="24" :sm="12" :md="8">
            <div class="stat-item">
              <a-statistic title="备份耗时" :value="parseCostMs(formData?.result?.backup_cost_time) ?? '-'">
                <template #prefix>
                  <SaveOutlined style="color: #52c41a" />
                </template>
                <template #suffix v-if="parseCostMs(formData?.result?.backup_cost_time) !== null">ms</template>
              </a-statistic>
            </div>
          </a-col>
          <a-col :xs="24" :sm="12" :md="8">
            <div class="stat-item">
              <a-statistic
                title="影响行数"
                :value="Number(formData?.result?.affected_rows ?? 0)"
                :value-style="{ color: '#fa8c16' }"
              >
                <template #prefix>
                  <DatabaseOutlined style="color: #fa8c16" />
                </template>
              </a-statistic>
            </div>
          </a-col>
        </a-row>

        <!-- 导出文件信息（EXPORT 任务） -->
        <template v-if="formData?.sql_type === 'EXPORT'">
          <div class="pane-section-title">
            <FileTextOutlined class="section-icon" />
            <span>导出文件信息</span>
          </div>
          <a-descriptions :column="1" bordered size="small">
            <a-descriptions-item label="文件名">
              <a-typography-text copyable>
                {{ formData.result.file_name || '-' }}
              </a-typography-text>
            </a-descriptions-item>
            <a-descriptions-item label="文件大小">
              <a-tag color="blue">{{ formatFileSize(formData?.result?.file_size) }}</a-tag>
            </a-descriptions-item>
            <a-descriptions-item label="导出行数">
              <a-tag color="green">{{ formData?.result?.export_rows || 0 }} 行</a-tag>
            </a-descriptions-item>
            <a-descriptions-item label="文件加密秘钥">
              <a @click="copyRecord(formData?.result?.encryption_key)">
                {{ formData?.result?.encryption_key || '-' }}
              </a>
            </a-descriptions-item>
            <a-descriptions-item label="文件下载路径">
              <a v-if="formData?.result?.download_url" :href="formData?.result?.download_url" target="_blank"
                class="download-link">
                <DownloadOutlined /> 点击下载
              </a>
              <span v-else>-</span>
            </a-descriptions-item>
          </a-descriptions>
        </template>

        <!-- 错误信息（执行失败时展示，置于统计之后） -->
        <template v-if="formData?.result?.error">
          <div class="pane-section-title error-title">
            <CloseCircleOutlined class="section-icon error-icon" />
            <span>错误信息</span>
          </div>
          <div class="code-wrapper">
            <CodeMirror :initVal="formData.result.error" />
          </div>
        </template>
      </a-tab-pane>

      <!-- SQL 全文 -->
      <a-tab-pane key="sql" tab="SQL 全文">
        <div v-if="formData?.sql" class="sql-toolbar">
          <a class="sql-copy-link" @click="copyRecord(formData.sql)">
            <CopyOutlined />
            复制
          </a>
        </div>
        <div v-if="formData?.sql" class="code-wrapper">
          <CodeMirror :initVal="formData.sql" height="220px" />
        </div>
        <a-empty v-else description="当前任务暂无 SQL 内容" />
      </a-tab-pane>

      <!-- 执行日志 -->
      <a-tab-pane v-if="formData?.result?.execute_log" key="log" tab="执行日志">
        <div class="code-wrapper">
          <CodeMirror :initVal="formData.result.execute_log" :height="'350px'" />
        </div>
      </a-tab-pane>

      <!-- 回滚 SQL -->
      <a-tab-pane v-if="formData?.result?.rollback_sql" key="rollback" tab="回滚 SQL">
        <div class="rollback-toolbar">
          <a-typography-text class="rollback-hint" type="secondary">
            备份时由 Binlog 自动生成，请复制后到「SQL 查询」人工执行
          </a-typography-text>
          <a class="rollback-copy-link" @click="copyRecord(formData.result.rollback_sql)">
            <CopyOutlined />
            复制
          </a>
        </div>
        <div class="code-wrapper">
          <CodeMirror :initVal="formData.result.rollback_sql" />
        </div>
      </a-tab-pane>
    </a-tabs>

    <!-- Footer -->
    <div class="modal-footer">
      <a-button type="primary" @click="handleCancel">关闭</a-button>
    </div>
  </a-modal>
</template>

<script setup>
import {
  ThunderboltOutlined,
  SaveOutlined,
  DatabaseOutlined,
  FileTextOutlined,
  DownloadOutlined,
  CloseCircleOutlined,
  CopyOutlined
} from '@ant-design/icons-vue'
import CodeMirror from '@/components/edit/Codemirror.vue'
import { message } from 'ant-design-vue'
import { ref, watch } from 'vue'
import useClipboard from 'vue-clipboard3'

const { toClipboard } = useClipboard()

const props = defineProps({
  open: Boolean,
})

const emit = defineEmits(['update:open'])

// 任务详情数据
const formData = defineModel('modelValue', {
  type: Object,
  required: true
})

// 页签状态：每次打开弹窗回到「执行信息」
const activeTab = ref('info')
watch(
  () => props.open,
  (visible) => {
    if (visible) activeTab.value = 'info'
  },
)

// 复制：支持传入字符串或对象 { sqltext }
const copyRecord = async (value) => {
  try {
    if (!value) {
      message.warning('没有可拷贝的内容')
      return
    }
    await toClipboard(value)
    message.success('已拷贝到剪贴板')
  } catch (e) {
    message.error(String(e))
  }
}

// 关闭
const handleCancel = () => {
  emit('update:open', false)
}

// 格式化文件大小
const formatFileSize = (bytes) => {
  if (!bytes || bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return Math.round(bytes / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i]
}

// 耗时字段归一：后端返回形如 "80.28588ms" 的字符串（也可能已是数字），
// 这里提取数值并取整，单位 "ms" 统一由 suffix 提供，避免出现两个单位。
const parseCostMs = (value) => {
  if (value === null || value === undefined || value === '') return null
  const n = parseFloat(String(value))
  if (Number.isNaN(n)) return null
  return Math.round(n)
}
</script>

<style scoped>
/* 页签内容区不设滚动容器：由弹窗自身承载高度，避免内容区出现额外滚动条 */
.task-detail-tabs :deep(.ant-tabs-content-holder) {
  overflow: visible;
}

/* SQL 全文：复制入口右对齐，不与代码区抢空间 */
.sql-toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 8px;
}

.sql-copy-link {
  font-size: 12px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.sql-copy-link:hover {
  opacity: 0.8;
}

/* 执行信息页签内区块间距 */
.pane-section-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 20px 0 12px;
  font-size: 14px;
  font-weight: 600;
  color: #262626;
}

.section-icon {
  font-size: 15px;
  color: #1890ff;
}

.error-title {
  margin-bottom: 12px;
}

.error-icon {
  color: #ff4d4f;
}

/* 统计项样式 */
.stat-item {
  padding: 12px;
  background: #fafafa;
  border-radius: 6px;
  transition: all 0.3s ease;
}

.stat-item:hover {
  background: #f0f0f0;
  transform: translateY(-2px);
}

:deep(.ant-statistic-title) {
  font-size: 13px;
  color: #8c8c8c;
  margin-bottom: 8px;
}

:deep(.ant-statistic-content) {
  font-size: 20px;
  font-weight: 600;
}

/* 下载链接样式 */
.download-link {
  color: #1890ff;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  transition: all 0.3s ease;
}

.download-link:hover {
  color: #40a9ff;
  text-decoration: underline;
}

/* 代码区域包装 */
.code-wrapper {
  border-radius: 4px;
  overflow: hidden;
  border: 1px solid #f0f0f0;
}

/* 描述列表优化 */
:deep(.ant-descriptions-item-label) {
  font-weight: 500;
  background: #fafafa;
  width: 140px;
}

:deep(.ant-descriptions-item-content) {
  background: #fff;
}

/* 回滚 SQL：说明文字与复制入口一行排布 */
.rollback-toolbar {
  display: flex;
  align-items: center;
  margin-bottom: 12px;
}

.rollback-hint {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
}

.rollback-copy-link {
  flex-shrink: 0;
  margin-left: 16px;
  font-size: 12px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.rollback-copy-link:hover {
  opacity: 0.8;
}

/* Footer */
.modal-footer {
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid #f0f0f0;
  display: flex;
  justify-content: flex-end;
}

/* 响应式调整 */
@media (max-width: 768px) {
  /* 窄屏空间不足，隐藏说明文字保留复制入口 */
  .rollback-hint {
    display: none;
  }

  :deep(.ant-descriptions-item-label) {
    width: 84px;
  }

  .stat-item {
    padding: 8px;
  }

  .code-wrapper :deep(pre) {
    white-space: pre-wrap;
    word-break: break-word;
    font-size: 11px;
  }
}
</style>
