<template>
  <div class="gi-page-shell backup-restore-page">
    <div class="gi-page-toolbar restore-toolbar">
      <div class="toolbar-left">
        <div class="gi-page-title">数据恢复</div>
      </div>
      <div class="toolbar-right">
        <a-input-search
          v-model:value="query.search"
          class="search-input"
          placeholder="搜索任务名称/ID"
          @search="handleSearch"
        />
        <!-- 批量删除：仅在勾选数据后显示，样式与【数据备份】页保持一致 -->
        <a-button
          v-if="canManageRestore && selectedRowKeys.length"
          danger
          :loading="batchDeleteUi.loading"
          :disabled="!selectedRowKeys.length"
          @click="openBatchDeleteModal"
        >
          批量删除 ({{ selectedRowKeys.length }})
        </a-button>
        <a-button
          v-if="canExecuteRestore"
          type="primary"
          class="restore-primary-btn"
          @click="openCreate"
          >创建恢复</a-button
        >
      </div>
    </div>

    <a-card class="restore-table-card" :bordered="false">
      <!-- 选择提示条：与【数据备份】页同款样式 -->
      <div v-if="selectedRowKeys.length" class="restore-selection-bar">
        <span class="restore-selection-text">
          已选择 <strong>{{ selectedRowKeys.length }}</strong> 个恢复任务
        </span>
        <a-button type="link" size="small" @click="clearSelection">取消选择</a-button>
      </div>
      <div ref="tableWrapRef" class="restore-table-wrap">
        <a-table
          class="restore-table"
          :columns="columns"
          :data-source="pagedRows"
          :loading="tableLoading"
          :pagination="pagination"
          :scroll="tableScroll"
          row-key="row_key"
          :row-selection="canManageRestore ? rowSelection : undefined"
          :row-class-name="rowClassName"
          @change="handleTableChange"
        >
          <template #headerCell="{ column }">
            <template v-if="column.key === 'restore_mode'">
              <div class="table-header-filter">
                <span>恢复方式</span>
                <a-popover trigger="click" placement="bottomRight">
                  <template #content>
                    <div class="header-filter-panel">
                      <a-select
                        v-model:value="query.restore_type"
                        style="width: 180px"
                        :options="restoreTypeOptions"
                        @change="handleFilterChange"
                      />
                    </div>
                  </template>
                  <FilterOutlined
                    :class="['header-filter-icon', { active: !!query.restore_type }]"
                  />
                </a-popover>
              </div>
            </template>
            <template v-else-if="column.key === 'created_at'">
              <div class="table-header-filter">
                <span>创建时间</span>
                <a-popover trigger="click" placement="bottomRight">
                  <template #content>
                    <div class="header-filter-panel">
                      <a-range-picker
                        v-model:value="query.dateRange"
                        value-format=""
                        style="width: 100%"
                        @change="handleFilterChange"
                      />
                      <div class="header-filter-actions">
                        <a-button size="small" @click="resetDateFilter">重置</a-button>
                      </div>
                    </div>
                  </template>
                  <FilterOutlined
                    :class="['header-filter-icon', { active: !!query.dateRange?.length }]"
                  />
                </a-popover>
              </div>
            </template>
            <template v-else-if="column.key === 'status'">
              <div class="table-header-filter">
                <span>状态</span>
                <a-popover trigger="click" placement="bottomRight">
                  <template #content>
                    <div class="header-filter-panel">
                      <a-select
                        v-model:value="query.status"
                        style="width: 180px"
                        :options="statusOptions"
                        @change="handleFilterChange"
                      />
                    </div>
                  </template>
                  <FilterOutlined :class="['header-filter-icon', { active: !!query.status }]" />
                </a-popover>
              </div>
            </template>
          </template>
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'id'">
              <div class="task-id-cell">
                <!-- 结构与样式对齐【数据备份】页任务ID列：普通 button 而非 a-button，
                     避免 antd 按钮继承全局 controlHeight=40px 把第一行撑高、两行文字间距过大 -->
                <div class="task-id-row">
                  <a-tooltip :title="record.task_id || '-'">
                    <button type="button" class="task-id-link" @click="openRecordDetail(record)">{{
                      record.task_id || '-'
                    }}</button>
                  </a-tooltip>
                  <a-tooltip title="复制任务ID">
                    <button
                      type="button"
                      class="task-id-copy-btn"
                      aria-label="复制任务ID"
                      @click.stop="handleCopyTaskId(record.task_id)"
                    >
                      <CopyOutlined />
                    </button>
                  </a-tooltip>
                </div>
                <div class="restore-name-row">
                  <template v-if="!editingRestoreName[record.row_key]">
                    <a-tooltip :title="restoreNameFull(record)">
                      <span class="restore-name-text">{{ restoreNameDisplay(record) }}</span>
                    </a-tooltip>
                    <a-tooltip title="修改任务名称">
                      <button
                        v-if="canManageRestore"
                        type="button"
                        class="restore-name-edit-btn"
                        aria-label="修改任务名称"
                        @click.stop="startEditRestoreName(record)"
                      >
                        <EditOutlined />
                      </button>
                    </a-tooltip>
                  </template>
                  <template v-else>
                    <a-input
                      :ref="(el) => setRestoreNameInputRef(record.row_key, el)"
                      v-model:value="editingRestoreName[record.row_key]"
                      size="small"
                      :maxLength="64"
                      class="restore-name-input"
                      placeholder="请输入任务名称"
                      @pressEnter="saveRestoreName(record)"
                      @blur="saveRestoreName(record)"
                      @keydown.esc="cancelEditRestoreName(record)"
                    />
                  </template>
                </div>
              </div>
            </template>

            <template v-else-if="column.key === 'restore_mode'">
              <div class="cell-primary">{{ record.restore_mode_label }}</div>
              <div v-if="record.restore_mode_desc" class="cell-secondary ellipsis-text">
                {{ record.restore_mode_desc }}
              </div>
            </template>

            <template v-else-if="column.key === 'source'">
              <a-tooltip :title="record.source_tooltip">
                <div class="data-source-cell">
                  <img
                    class="db-logo"
                    :src="dbTypeLogo(record.db_type)"
                    :alt="record.db_type || 'db'"
                  />
                  <div class="data-source-text">
                    <div class="cell-primary ellipsis-text">
                      <DataSourceName :name="record.source_primary" />
                    </div>
                    <div class="cell-secondary ellipsis-text">{{ record.source_secondary }}</div>
                  </div>
                </div>
              </a-tooltip>
            </template>

            <template v-else-if="column.key === 'target'">
              <a-tooltip :title="record.target_tooltip">
                <div class="data-source-cell">
                  <img
                    class="db-logo"
                    :src="dbTypeLogo(record.db_type)"
                    :alt="record.db_type || 'db'"
                  />
                  <div class="data-source-text">
                    <div class="cell-primary ellipsis-text">
                      <DataSourceName :name="record.target_primary" />
                    </div>
                    <div class="cell-secondary ellipsis-text">{{ record.target_secondary }}</div>
                  </div>
                </div>
              </a-tooltip>
            </template>

            <template v-else-if="column.key === 'created_by'">
              <span class="cell-primary">{{ record.created_by || '-' }}</span>
            </template>

            <template v-else-if="column.key === 'created_at'">
              <div class="cell-primary">{{ formatDateTime(record.created_at) }}</div>
            </template>

            <template v-else-if="column.key === 'status'">
              <!-- 合并列：上方进度条直观展示恢复进度，下方为状态文字 -->
              <div class="status-progress-cell">
                <a-progress
                  :percent="displayProgress(record)"
                  size="small"
                  :show-info="true"
                  :status="record.status === 'FAILED' ? 'exception' : 'normal'"
                  :stroke-color="record.status === 'FAILED' ? undefined : restoreProgressColor"
                />
                <span :class="['status-tag', statusClass(record.status, record.current_stage, record.execution_mode)]">
                  {{ statusTagLabel(record) }}
                </span>
              </div>
            </template>

            <template v-else-if="column.key === 'action'">
              <div class="action-group">
                <a-button
                  v-if="canExecutePendingPlan(record)"
                  type="link"
                  size="small"
                  class="execute-plan-btn"
                  @click="openExecutePlanDialog(record)"
                >
                  立即执行
                </a-button>
                <a-dropdown placement="bottomRight" trigger="click">
                  <a-button size="small" type="link" class="more-action-btn">
                    更多
                  </a-button>
                  <template #overlay>
                    <a-menu>
                      <a-menu-item @click="openRecordDetail(record)">详情</a-menu-item>
                      <a-menu-item @click="openMonitorPage(record)">监控页</a-menu-item>
                      <!-- 编辑收纳进「更多」菜单：禁用态与「终止」一致（antd 菜单项默认置灰不可点） -->
                      <a-menu-item :disabled="!canEditRestore(record)" @click="openEdit(record)">
                        编辑
                      </a-menu-item>
                      <a-menu-item :disabled="!canStopRestore(record)" @click="handleStop(record)">
                        {{ record.status === 'STOP_REQUESTED' ? '强制终止' : '终止' }}
                      </a-menu-item>
                      <!-- 重跑：详情弹窗不再提供该入口，收敛到列表；按相同配置新建一条任务 -->
                      <a-menu-item :disabled="!canRetryRestore(record)" @click="handleRetryRestore(record)">
                        重跑
                      </a-menu-item>
                      <a-menu-item danger @click="handleDelete(record)">删除</a-menu-item>
                    </a-menu>
                  </template>
                </a-dropdown>
              </div>
            </template>
          </template>
        </a-table>
      </div>
    </a-card>

    <!-- 批量删除确认弹窗：交互与【数据备份】页保持一致（清单 + 勾选确认） -->
    <a-modal
      :open="batchDeleteUi.open"
      title="批量删除恢复任务"
      :confirm-loading="batchDeleteUi.loading"
      :ok-button-props="{ danger: true, disabled: !batchDeleteUi.confirmed }"
      ok-text="确认删除"
      cancel-text="取消"
      @ok="handleBatchDelete"
      @cancel="closeBatchDeleteModal"
    >
      <div class="batch-delete-list">
        <div class="batch-delete-list-title">本次将删除以下 {{ selectedRows.length }} 个恢复任务：</div>
        <ul class="batch-delete-list-body">
          <li v-for="item in selectedRows" :key="item.row_key">
            <span class="batch-delete-item-name">{{ item.name || '未命名任务' }}</span>
            <span class="batch-delete-item-id">{{ item.id }}</span>
          </li>
        </ul>
      </div>
      <a-checkbox v-model:checked="batchDeleteUi.confirmed">
        我已知悉删除后不可恢复，确认删除所选的 {{ selectedRows.length }} 个恢复任务
      </a-checkbox>
    </a-modal>

    <a-modal
      :open="uiState.executePlanOpen"
      title="确认立即执行恢复计划"
      width="520px"
      :confirm-loading="uiState.executePlanLoading"
      ok-text="确认执行"
      cancel-text="取消"
      @ok="confirmExecutePlan"
      @cancel="closeExecutePlanDialog"
    >
      <a-alert
        type="warning"
        show-icon
        class="execute-plan-alert"
        message="此操作将把当前计划模式任务切换为执行模式，并立即调用 pg_restore / mysql 命令。"
      />
      <a-descriptions
        :column="1"
        size="small"
        bordered
        class="execute-plan-summary"
      >
        <a-descriptions-item label="任务ID">{{ executePlanState.run?.task_id || '-' }}</a-descriptions-item>
        <a-descriptions-item label="目标">{{
          executePlanState.run?.target_db_name || executePlanState.run?.target_display || '-'
        }}</a-descriptions-item>
        <a-descriptions-item label="恢复方式">{{
          logicalRestoreModeLabel(executePlanState.run?.restore_mode || '-')
        }}</a-descriptions-item>
      </a-descriptions>
      <a-form
        ref="executePlanFormRef"
        :model="executePlanState.form"
        :rules="executePlanRules"
        :label-col="{ span: 5 }"
        :wrapper-col="{ span: 18 }"
        class="execute-plan-form"
      >
        <a-form-item label="二次确认" name="confirm">
          <a-checkbox v-model:checked="executePlanState.form.confirmChecked">
            我已确认该恢复计划并知晓执行风险
          </a-checkbox>
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal
      :open="uiState.modalOpen"
      :title="modalTitle"
      width="840px"
      :confirm-loading="uiState.modalLoading"
      :ok-text="modalOkText"
      cancel-text="取消"
      @ok="handleSubmit"
      @cancel="closeModal"
    >
      <template v-if="isPhysicalRestoreSet" #footer>
        <a-button @click="closeModal">取消</a-button>
        <a-button :loading="uiState.precheckLoading" @click="handlePhysicalPrecheck"
          >执行预检查</a-button
        >
        <a-button
          v-if="canExecuteRestore"
          type="primary"
          :loading="uiState.modalLoading"
          :disabled="!precheckState.passed"
          @click="handleSubmit"
        >
          提交恢复
        </a-button>
      </template>
      <a-form
        ref="formRef"
        :model="formData"
        :rules="rules"
        :label-col="{ span: 5 }"
        :wrapper-col="{ span: 18 }"
      >
        <a-form-item label="任务名称" name="name">
          <a-input v-model:value="formData.name" maxlength="128" placeholder="请输入任务名称" />
        </a-form-item>
        <a-form-item v-if="!isPhysicalRestoreSet" label="目标实例" name="target_instance_id">
          <a-select
            v-model:value="formData.target_instance_id"
            class="target-instance-select"
            :loading="uiState.instancesLoading"
            :options="instances"
            :field-names="{ label: 'displayLabel', value: 'value' }"
            show-search
            allow-clear
            placeholder="请选择目标实例"
            option-label-prop="displayLabel"
            dropdown-class-name="instance-select-dropdown--two-line"
            :filter-option="filterTargetInstanceOption"
          >
            <template #option="{ ellipsisRemark, hostPort, fullRemark, longLabel }">
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
        </a-form-item>
        <a-form-item label="备份集" name="backup_set_id">
          <a-select
            v-model:value="formData.backup_set_id"
            :loading="uiState.setsLoading"
            :options="backupSetOptions"
            show-search
            allow-clear
            placeholder="请选择备份集"
            @change="handleSetChange"
          />
        </a-form-item>
        <a-form-item v-if="isPhysicalRestoreSet && selectedBackupSet" label="备份链路">
          <div class="chain-preview-card" :class="{ 'is-orphan': !selectedChain.hasAncestor }">
            <div class="chain-preview-title">
              <span>恢复链路预览</span>
              <a-tag :color="selectedChain.total === 1 ? 'green' : 'blue'">
                {{ selectedChain.total === 1 ? '单集恢复' : `${selectedChain.total} 段链路` }}
              </a-tag>
              <span v-if="selectedChain.totalSizeText" class="chain-preview-total"
                >总大小：{{ selectedChain.totalSizeText }}</span
              >
            </div>
            <a-alert
              v-if="!selectedChain.hasAncestor"
              class="chain-preview-alert"
              type="warning"
              show-icon
              message="当前选中的备份集未在平台内找到对应 FULL 基线，请确保 pgBackRest 仓库中存在可用的 FULL 基准集，否则恢复将失败。"
            />
            <div class="chain-preview-flow">
              <template
                v-for="(item, idx) in selectedChain.items"
                :key="item.id || `chain-item-${idx}`"
              >
                <div
                  class="chain-preview-node"
                  :class="[
                    `is-${String(item.backup_type || 'FULL').toLowerCase()}`,
                    { 'is-selected': item.id === selectedBackupSet.id },
                  ]"
                >
                  <a-tag :color="item.color || 'default'">{{ item.label }}</a-tag>
                  <span class="chain-preview-name" :title="item.name">{{ item.name }}</span>
                  <span class="chain-preview-meta"
                    >{{ item.createdText }} · {{ item.sizeText }}</span
                  >
                  <a-tag v-if="item.id === selectedBackupSet.id" color="blue">当前选择</a-tag>
                </div>
                <div v-if="idx < selectedChain.items.length - 1" class="chain-preview-arrow">
                  ← 依赖
                </div>
              </template>
            </div>
          </div>
        </a-form-item>
        <a-form-item label="恢复对象">
          <a-textarea
            :value="restoreObjectDisplay"
            :rows="2"
            readonly
            placeholder="请选择备份集后查看恢复对象"
          />
        </a-form-item>
        <a-form-item
          v-if="!isPhysicalRestoreSet && isPostgresLogicalMultiRestore"
          label="目标库后缀"
          name="target_db_suffix"
          :extra="targetDbExtra"
        >
          <a-input v-model:value="formData.target_db_suffix" :placeholder="targetDbPlaceholder" />
        </a-form-item>
        <a-form-item
          v-else-if="!isPhysicalRestoreSet"
          label="目标库名"
          name="target_db_name"
          :extra="targetDbExtra"
        >
          <a-input v-model:value="formData.target_db_name" :placeholder="targetDbPlaceholder" />
        </a-form-item>
        <a-form-item v-else label="恢复说明" :extra="targetDbExtra">
          <a-input value="实例级物理恢复，不需要填写目标库名" disabled />
        </a-form-item>
        <template v-if="!isPhysicalRestoreSet">
          <a-form-item label="恢复模式" name="restore_mode">
            <a-radio-group v-model:value="formData.restore_mode" button-style="solid">
              <a-radio-button value="new">新建恢复</a-radio-button>
              <a-radio-button value="overwrite">覆盖恢复</a-radio-button>
              <a-radio-button value="append">追加恢复</a-radio-button>
            </a-radio-group>
          </a-form-item>
          <a-form-item
            label="运行模式"
            name="mode"
            extra="默认先生成恢复计划；只有切换到执行模式并填写确认项时才会真正导入。"
          >
            <a-radio-group v-model:value="formData.mode" button-style="solid">
              <a-radio-button value="plan">计划模式</a-radio-button>
              <a-radio-button value="execute">执行模式</a-radio-button>
            </a-radio-group>
          </a-form-item>
          <a-form-item
            v-if="supportsDatabaseSelection"
            label="指定数据库"
            extra="留空表示按备份集默认范围恢复；适用于 zip / 多库恢复场景。"
          >
            <a-select
              v-model:value="formData.selected_databases"
              mode="tags"
              allow-clear
              :token-separators="[',', ' ']"
              placeholder="输入源数据库名后回车确认"
            />
          </a-form-item>
          <a-form-item
            v-if="supportsSchemaSelection"
            label="指定 Schema"
            extra="仅 PostgreSQL / 金仓逻辑恢复生效；留空表示不额外筛选。"
          >
            <a-select
              v-model:value="formData.selected_schemas"
              mode="tags"
              allow-clear
              :token-separators="[',', ' ']"
              placeholder="例如 public、sales"
            />
          </a-form-item>
          <a-form-item
            v-if="supportsTableSelection"
            label="指定表"
            extra="仅 PostgreSQL / 金仓逻辑恢复生效；格式建议 schema.table。"
          >
            <a-select
              v-model:value="formData.selected_tables"
              mode="tags"
              allow-clear
              :token-separators="[',', ' ']"
              placeholder="例如 public.orders"
            />
          </a-form-item>
          <a-form-item label="高级参数">
            <div class="logical-advanced-panel">
              <div class="logical-advanced-grid">
                <div class="advanced-field">
                  <span class="advanced-label">并发数</span>
                  <a-input-number
                    v-model:value="formData.parallel"
                    :min="1"
                    :max="32"
                    style="width: 100%"
                  />
                </div>
                <div class="advanced-field">
                  <span class="advanced-label">超时时间</span>
                  <a-input v-model:value="formData.timeout" placeholder="例如 4h" />
                </div>
                <div v-if="selectedBackupSet?.db_type === 'MySQL'" class="advanced-field">
                  <span class="advanced-label">目标字符集</span>
                  <a-input v-model:value="formData.target_charset" placeholder="例如 utf8mb4" />
                </div>
                <div v-if="selectedBackupSet?.db_type === 'MySQL'" class="advanced-field">
                  <span class="advanced-label">目标排序规则</span>
                  <a-input
                    v-model:value="formData.target_collation"
                    placeholder="例如 utf8mb4_unicode_ci"
                  />
                </div>
                <div v-if="isPostgresLogicalSet" class="advanced-field">
                  <span class="advanced-label">目标编码</span>
                  <a-input v-model:value="formData.target_encoding" placeholder="例如 UTF8" />
                </div>
              </div>
              <div class="logical-switch-grid">
                <a-switch
                  v-model:checked="formData.foreign_key_checks"
                  checked-children="关闭 FK 校验"
                  un-checked-children="保留 FK 校验"
                />
                <a-switch
                  v-model:checked="formData.exit_on_error"
                  checked-children="遇错终止"
                  un-checked-children="允许继续"
                />
                <a-switch
                  v-if="isPostgresLogicalSet"
                  v-model:checked="formData.no_owner"
                  checked-children="忽略 Owner"
                  un-checked-children="保留 Owner"
                />
                <a-switch
                  v-if="isPostgresLogicalSet"
                  v-model:checked="formData.no_privileges"
                  checked-children="忽略权限"
                  un-checked-children="保留权限"
                />
                <a-switch
                  v-if="isPostgresLogicalSet"
                  v-model:checked="formData.clean_if_exists"
                  checked-children="清理已存在对象"
                  un-checked-children="不清理已存在对象"
                />
                <a-switch
                  v-if="selectedBackupSet?.db_type === 'MySQL'"
                  v-model:checked="formData.close_binlog"
                  checked-children="关闭 Binlog"
                  un-checked-children="保留 Binlog"
                />
              </div>
            </div>
          </a-form-item>
          <a-form-item label="环境属性">
            <a-space wrap>
              <a-switch
                v-model:checked="formData.is_cloud"
                checked-children="云数据库"
                un-checked-children="自建数据库"
              />
            </a-space>
          </a-form-item>
          <a-form-item
            v-if="formData.mode === 'execute'"
            label="确认清单"
            extra="危险操作必须显式确认，缺少确认项时后端会直接阻断。"
          >
            <div class="logical-confirm-panel">
              <div class="logical-switch-grid">
                <a-switch
                  v-model:checked="formData.execute_confirmed"
                  checked-children="确认执行"
                  un-checked-children="未确认执行"
                />
              </div>
            </div>
          </a-form-item>
        </template>
        <template v-if="isPhysicalRestoreSet">
          <a-form-item label="恢复模式">
            <a-radio-group v-model:value="formData.restore_mode">
              <a-radio-button v-if="selectedSetIsMySQL" value="EXISTING_INSTANCE"
                >覆盖目标实例（EXISTING_INSTANCE）</a-radio-button
              >
              <a-radio-button v-else value="NEW_INSTANCE">恢复到新实例</a-radio-button>
            </a-radio-group>
          </a-form-item>
          <a-form-item
            v-if="selectedSetIsMySQL"
            label="目标实例"
            name="target_instance_id"
            extra="覆盖恢复：将停止目标实例、清空其数据目录并回填备份数据。"
          >
            <a-select
              v-model:value="formData.target_instance_id"
              :options="mysqlInstanceOptions"
              show-search
              allow-clear
              placeholder="请选择要覆盖恢复的 MySQL 实例"
            />
          </a-form-item>
          <template v-else>
            <a-form-item label="目标节点" name="target_node_id" extra="物理恢复由目标节点本机执行。">
              <a-select
                v-model:value="formData.target_node_id"
                :options="nodeOptions"
                :loading="uiState.nodesLoading"
                show-search
                allow-clear
                placeholder="请选择目标节点"
              />
            </a-form-item>
            <a-form-item
              label="目标数据目录"
              name="target_data_dir"
              extra="建议使用全新目录，避免覆盖已有实例数据目录。"
            >
              <a-input
                v-model:value="formData.target_data_dir"
                placeholder="/data/postgresql/restore-demo-001"
              />
            </a-form-item>
            <a-form-item label="目标端口" name="target_port" extra="默认通过新端口启动恢复后的实例。">
              <a-input-number
                v-model:value="formData.target_port"
                style="width: 100%"
                :min="1"
                :max="65535"
              />
            </a-form-item>
          </template>
          <a-form-item
            label="自动启动"
            :extra="selectedSetIsMySQL ? '恢复完成后自动启动目标 MySQL 实例。' : ''"
          >
            <a-switch v-model:checked="formData.auto_start" />
          </a-form-item>
          <a-alert
            v-if="selectedSetIsMySQL"
            type="warning"
            show-icon
            message="覆盖模式为高危操作"
            description="提交后平台将：停止目标 MySQL 实例 → 清空其数据目录 → 回填备份数据 → 重新启动实例。目标实例现有数据将被直接丢弃。"
          />
        </template>
        <a-form-item v-if="isPhysicalRestoreSet" label="高级参数">
          <div class="advanced-panel">
            <div>并发数：自动按服务端策略执行</div>
            <div>恢复选项：当前按备份集元信息与默认恢复参数执行</div>
          </div>
        </a-form-item>
      </a-form>
      <a-alert
        v-if="isPhysicalRestoreSet && selectedBackupSet"
        class="restore-source-tip"
        type="info"
        show-icon
        :message="`恢复来源：${selectedBackupSet.name}`"
        :description="`${selectedBackupSet.db_type || '-'} / ${selectedBackupSet.object_summary || '-'} / ${selectedBackupSet.file_name || '-'}`"
      />
      <div v-if="isPhysicalRestoreSet" class="precheck-panel">
        <div class="panel-title">预检查结果</div>
        <a-empty
          v-if="!precheckState.summary && !precheckState.items.length"
          description="点击“执行预检查”后显示结果"
        />
        <template v-else>
          <a-alert
            :type="precheckState.passed ? 'success' : 'warning'"
            show-icon
            :message="precheckState.summary || '预检查已完成'"
          />
          <div class="resolved-grid" v-if="precheckState.resolved && !selectedSetIsMySQL">
            <div class="resolved-item">
              <span class="resolved-label">目标主机</span>
              <span class="resolved-value">{{ precheckState.resolved.target_host || '-' }}</span>
            </div>
            <div class="resolved-item">
              <span class="resolved-label">工具目录</span>
              <span class="resolved-value">{{
                precheckState.resolved.target_pg_bin_dir || '-'
              }}</span>
            </div>
            <div class="resolved-item">
              <span class="resolved-label">PostgreSQL 版本</span>
              <span class="resolved-value">{{
                precheckState.resolved.target_pg_version || '-'
              }}</span>
            </div>
            <div class="resolved-item">
              <span class="resolved-label">仓库路径</span>
              <span class="resolved-value">{{ precheckState.resolved.repo_path || '-' }}</span>
            </div>
          </div>
          <div class="resolved-grid" v-else-if="precheckState.resolved && selectedSetIsMySQL">
            <div class="resolved-item">
              <span class="resolved-label">目标数据目录</span>
              <span class="resolved-value">{{
                precheckState.resolved.target_data_dir || '-'
              }}</span>
            </div>
            <div class="resolved-item">
              <span class="resolved-label">目标版本</span>
              <span class="resolved-value">{{
                precheckState.resolved.target_version || '-'
              }}</span>
            </div>
            <div class="resolved-item">
              <span class="resolved-label">xtrabackup</span>
              <span class="resolved-value">{{
                precheckState.resolved.xtrabackup_path || '-'
              }}</span>
            </div>
            <div class="resolved-item">
              <span class="resolved-label">服务单元</span>
              <span class="resolved-value">{{
                precheckState.resolved.service_unit || '未识别（需人工启动）'
              }}</span>
            </div>
          </div>
          <a-table
            :columns="precheckColumns"
            :data-source="precheckState.items"
            :pagination="false"
            size="small"
            row-key="item_key"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'passed'">
                <a-tag :color="record.passed ? 'green' : 'red'">{{
                  record.passed ? '通过' : '未通过'
                }}</a-tag>
              </template>
              <template v-else-if="column.key === 'message'">
                <div>{{ record.message || '-' }}</div>
                <div class="cell-secondary" v-if="record.detail">
                  {{ JSON.stringify(record.detail) }}
                </div>
              </template>
            </template>
          </a-table>
        </template>
      </div>
    </a-modal>

    <a-modal
      :open="detailState.open"
      :title="null"
      width="1120px"
      :footer="null"
      :closable="false"
      wrap-class-name="restore-run-detail-modal"
      @cancel="closeDetail"
    >
      <RestoreRunDetailPanel
        :run="detailState.run"
        :items="detailState.items"
        :statistics="detailState.itemStatistics"
        :loading="detailState.loading"
        :loading-action="detailState.actionLoading"
        :refresh-seconds="5"
        @close="closeDetail"
        @refresh="refreshDetail"
        @view-log="openLogDrawer(detailState.run, $event)"
        @stop="handleStop(detailState.run)"
        @download-log="handleDownloadDetailLog"
      />
    </a-modal>

    <a-drawer
      :open="logState.open"
      title="恢复任务日志"
      placement="right"
      width="55vw"
      @close="closeLogDrawer"
    >
      <a-skeleton v-if="logState.loading && !logState.run" active :paragraph="{ rows: 10 }" />
      <template v-else-if="logState.run">
        <div class="log-summary-card">
          <div class="log-summary-line">
            <span
              >目标库：{{ logState.run.target_display || logState.run.target_db_name || '-' }}</span
            >
            <span
              >实例：{{ formatHostPort(logState.run.target_host, logState.run.target_port) }}</span
            >
            <span
              >状态：<span
                :class="[
                  'status-pill',
                  statusClass(logState.run.status, logState.run.current_stage),
                ]"
                >{{ statusLabel(logState.run.status, logState.run.current_stage) }}</span
              ></span
            >
          </div>
          <div class="log-summary-line">
            <span>备份集：{{ logState.run.set_name || '-' }}</span>
            <span>开始：{{ formatDateTime(logState.run.start_at) }}</span>
            <span>结束：{{ formatDateTime(logState.run.end_at) }}</span>
          </div>
        </div>

        <div class="drawer-toolbar">
          <a-space wrap>
            <a-select
              v-model:value="logState.itemID"
              style="width: 260px"
              :options="logTargetOptions"
              @change="fetchLogContent"
            />
            <a-button :loading="logState.loading" @click="fetchLogContent">刷新日志</a-button>
            <a-button @click="copyText(logState.content || '', '已复制全部日志')"
              >复制全部日志</a-button
            >
            <a-button @click="handleDownloadLog">下载日志文件</a-button>
          </a-space>
        </div>

        <div class="drawer-meta">
          <span>日志路径：{{ formatStoragePath(logState.meta.log_path, null) }}</span>
          <span>读取方式：{{ logState.meta.read_mode || '-' }}</span>
          <span>日志大小：{{ formatSize(logState.meta.log_size_bytes) }}</span>
        </div>

        <div ref="logScrollRef" class="log-content" @scroll="handleLogScroll">
          <pre class="log-content-inner">{{ logState.content || '暂无日志内容' }}</pre>
        </div>
      </template>
    </a-drawer>

    <a-drawer
      :open="physicalDetailState.open"
      width="960"
      title="物理恢复详情"
      placement="right"
      @close="closePhysicalDetail"
    >
      <template v-if="physicalDetailState.run">
        <div class="detail-grid">
          <div class="metric-card">
            <div class="metric-label">状态</div>
            <div class="metric-value">
              <span :class="['status-tag', physicalStatusClass(physicalDetailState.run.status)]">
                <span class="status-dot"></span>
                {{ physicalDetailState.run.status_label || physicalDetailState.run.status || '-' }}
              </span>
            </div>
          </div>
          <div class="metric-card">
            <div class="metric-label">当前阶段</div>
            <div class="metric-value detail-text">
              {{ physicalDetailState.run.current_stage_label || '-' }}
            </div>
          </div>
          <div class="metric-card">
            <div class="metric-label">总进度</div>
            <div class="metric-value">{{ physicalDetailState.run.progress_percent || 0 }}%</div>
          </div>
          <div class="metric-card">
            <div class="metric-label">当前动作</div>
            <div class="metric-value detail-text">
              {{ physicalDetailState.run.current_action || '-' }}
            </div>
          </div>
        </div>

        <a-card size="small" title="恢复摘要" class="detail-card">
          <a-descriptions :column="2" size="small" bordered>
            <a-descriptions-item label="备份集">{{
              physicalDetailState.run.backup_set_name || '-'
            }}</a-descriptions-item>
            <a-descriptions-item label="驱动类型">{{
              physicalDetailState.run.driver_type || '-'
            }}</a-descriptions-item>
            <a-descriptions-item label="源实例">{{
              physicalDetailState.run.source_display || '-'
            }}</a-descriptions-item>
            <a-descriptions-item label="目标节点">{{
              physicalDetailState.run.target_node_name || '-'
            }}</a-descriptions-item>
            <a-descriptions-item label="目标主机">{{
              physicalDetailState.run.target_host || '-'
            }}</a-descriptions-item>
            <a-descriptions-item label="目标目录">{{
              physicalDetailState.run.target_data_dir || '-'
            }}</a-descriptions-item>
            <a-descriptions-item label="目标端口">{{
              physicalDetailState.run.target_port || '-'
            }}</a-descriptions-item>
            <template v-if="String(physicalDetailState.run.driver_type || '').toUpperCase() === 'XTRABACKUP'">
              <a-descriptions-item label="目标版本">{{
                physicalDetailState.run.resolved_extra?.target_version || '-'
              }}</a-descriptions-item>
              <a-descriptions-item label="备份源版本">{{
                physicalDetailState.run.resolved_extra?.source_version || '-'
              }}</a-descriptions-item>
              <a-descriptions-item label="xtrabackup">{{
                physicalDetailState.run.resolved_extra?.xtrabackup_path || '-'
              }}</a-descriptions-item>
              <a-descriptions-item label="服务单元">{{
                physicalDetailState.run.resolved_extra?.service_unit || '未识别（需人工启动）'
              }}</a-descriptions-item>
            </template>
            <template v-else>
              <a-descriptions-item label="stanza">{{
                physicalDetailState.run.stanza || '-'
              }}</a-descriptions-item>
              <a-descriptions-item label="工具目录">{{
                physicalDetailState.run.target_pg_bin_dir || '-'
              }}</a-descriptions-item>
            </template>
            <a-descriptions-item label="自动启动">{{
              physicalDetailState.run.auto_start ? '是' : '否'
            }}</a-descriptions-item>
            <a-descriptions-item label="发起人">{{
              physicalDetailState.run.created_by || '-'
            }}</a-descriptions-item>
          </a-descriptions>
          <div class="detail-progress">
            <a-progress :percent="physicalDetailState.run.progress_percent || 0" />
          </div>
          <div class="detail-error" v-if="physicalDetailState.run.error_message">
            {{ physicalDetailState.run.error_message }}
          </div>
        </a-card>

        <a-card size="small" title="预检查结果" class="detail-card">
          <a-empty v-if="!physicalDetailPrecheckItems.length" description="暂无预检查详情" />
          <a-table
            v-else
            :columns="precheckColumns"
            :data-source="physicalDetailPrecheckItems"
            :pagination="false"
            size="small"
            row-key="item_key"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'passed'">
                <a-tag :color="record.passed ? 'green' : 'red'">{{
                  record.passed ? '通过' : '未通过'
                }}</a-tag>
              </template>
              <template v-else-if="column.key === 'message'">
                <div>{{ record.message || '-' }}</div>
                <div class="cell-secondary" v-if="record.detail">
                  {{ JSON.stringify(record.detail) }}
                </div>
              </template>
            </template>
          </a-table>
        </a-card>

        <a-card size="small" title="执行日志" class="detail-card">
          <div class="drawer-meta">
            <span>日志路径：{{ formatStoragePath(physicalDetailState.logMeta.log_path, null) }}</span>
            <span>读取方式：{{ physicalDetailState.logMeta.read_mode || '-' }}</span>
          </div>
          <div class="log-content physical-log-content">
            <pre class="log-content-inner">{{
              physicalDetailState.logContent || '暂无日志内容'
            }}</pre>
          </div>
        </a-card>
      </template>
      <a-skeleton v-else active :paragraph="{ rows: 8 }" />
    </a-drawer>
  </div>
</template>

<script setup>
import { computed, h, inject, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import dayjs from 'dayjs'

import { Modal, message } from 'ant-design-vue'
import { CopyOutlined, EditOutlined, FilterOutlined } from '@ant-design/icons-vue'
import { useRoute, useRouter } from 'vue-router'
import DataSourceName from '@/components/patterns/DataSourceName.vue'
import { useUserStore } from '@/store/user'
import { dbTypeLogo } from '@/utils/dbTypeLogo'
// 日志路径与备份侧同源口径：能解析到存储目标时补「地址:」，解析不到则只显示路径
import { formatStoragePath } from '@/utils/storagePath'
import RestoreRunDetailPanel from './RestoreRunDetailPanel.vue'
import {
  createPhysicalRestoreRunApi,
  createRestoreRunApi,
  deletePhysicalRestoreRunApi,
  deleteRestoreRunApi,
  downloadRestoreRunLogApi,
  getBackupNodesApi,
  getBackupInstancesApi,
  getBackupSetsApi,
  getPhysicalRestoreRunDetailApi,
  getPhysicalRestoreRunLogApi,
  getPhysicalRestoreRunsApi,
  getRestoreRunDetailApi,
  getRestoreRunLogApi,
  getRestoreRunsApi,
  precheckPhysicalRestoreApi,
  retryRestoreRunApi,
  stopPhysicalRestoreRunApi,
  stopRestoreRunApi,
  updatePhysicalRestoreRunApi,
  updateRestoreRunApi,
  updateRestoreRunNameApi,
} from '@/api/backup'

defineOptions({ name: 'BackupRestoreView' })

const extractListFromResponse = (payload, fallback = []) => {
  if (Array.isArray(payload)) return payload
  if (!payload || typeof payload !== 'object') return fallback
  const candidates = [
    payload.data,
    payload.list,
    payload.items,
    payload.rows,
    payload.result,
    payload.payload,
    payload.records,
  ]
  for (let i = 0; i < candidates.length; i += 1) {
    const value = candidates[i]
    if (Array.isArray(value)) return value
  }
  return fallback
}

const isCanceledRequestError = (err) => {
  const rawMsg = String(err?.message || err?.msg || err?._message || '')
  return !!rawMsg && /已取消|canceled|ERR_CANCELED/i.test(rawMsg)
}

const fetchRestoresRetry = async (retries = 2) => {
  let lastErr = null
  for (let i = 0; i <= retries; i += 1) {
    try {
      const res = await getRestoreRunsApi({ is_page: false })
      return extractListFromResponse(res, [])
    } catch (err) {
      lastErr = err
    }
  }
  throw lastErr || new Error('逻辑恢复列表加载失败')
}

const fetchPhysicalRestoresRetry = async (retries = 2) => {
  let lastErr = null
  for (let i = 0; i <= retries; i += 1) {
    try {
      const res = await getPhysicalRestoreRunsApi({ is_page: false })
      return extractListFromResponse(res, [])
    } catch (err) {
      lastErr = err
    }
  }
  throw lastErr || new Error('物理恢复列表加载失败')
}

const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()

const hasPermission = (permission) =>
  userStore.is_superuser || !permission || (userStore.permissions || []).includes(permission)
const canExecuteRestore = computed(() => hasPermission('backup.restore.execute'))
const canManageRestore = computed(() => hasPermission('backup.restore.execute'))

const ACTIVE_STATUSES = ['PENDING', 'RUNNING', 'STOP_REQUESTED']
// 合并「状态」「进度」两列后表格总宽相应减少一列（原进度列 220px）
const TABLE_SCROLL_X = 1300

// 进度条主题色（与全站 --gi-color-primary 一致，替换默认绿色）
const restoreProgressColor = 'var(--gi-color-primary, #0f766e)'

// ===== 任务名称编辑（交互与【数据备份】页任务ID列名称编辑保持一致）=====
const RESTORE_NAME_MAX_LENGTH = 64
const RESTORE_NAME_ELLIPSIS_AT = 20

const restoreNameInputRefs = {}
const editingRestoreName = reactive({})
const cancellingRestoreNameEdit = new Set()

const countRestoreNameRunes = (text) => Array.from(String(text || '')).length

// 列表展示：超过 20 个字符用省略号截断，悬停 tooltip 查看完整名称
const restoreNameDisplay = (record) => {
  const text = String(record?.name || '').trim()
  if (!text) return '-'
  if (countRestoreNameRunes(text) <= RESTORE_NAME_ELLIPSIS_AT) return text
  return `${Array.from(text).slice(0, RESTORE_NAME_ELLIPSIS_AT).join('')}...`
}

const restoreNameFull = (record) => String(record?.name || '').trim()

const validateRestoreName = (name) => {
  if (!name) {
    message.warning('任务名称不能为空')
    return false
  }
  const runeLen = countRestoreNameRunes(name)
  if (runeLen < 2) {
    message.warning('任务名称至少 2 个字符')
    return false
  }
  if (runeLen > RESTORE_NAME_MAX_LENGTH) {
    message.warning(`任务名称最长 ${RESTORE_NAME_MAX_LENGTH} 个字符`)
    return false
  }
  return true
}

const setRestoreNameInputRef = (key, el) => {
  if (el) restoreNameInputRefs[key] = el
}

const startEditRestoreName = (record) => {
  if (!canManageRestore.value) return
  editingRestoreName[record.row_key] = restoreNameFull(record)
  nextTick(() => {
    const inputEl =
      restoreNameInputRefs[record.row_key]?.$el?.querySelector('input') ||
      restoreNameInputRefs[record.row_key]
    if (inputEl?.focus) inputEl.focus()
    if (inputEl?.select) inputEl.select()
  })
}

const cancelEditRestoreName = (record) => {
  cancellingRestoreNameEdit.add(record.row_key)
  delete editingRestoreName[record.row_key]
  delete restoreNameInputRefs[record.row_key]
  setTimeout(() => cancellingRestoreNameEdit.delete(record.row_key), 100)
}

// 走轻量改名接口：只写 name 字段，不触发恢复计划重建/状态校验等副作用。
const saveRestoreName = async (record) => {
  if (cancellingRestoreNameEdit.has(record.row_key)) {
    cancellingRestoreNameEdit.delete(record.row_key)
    return
  }
  const key = record.row_key
  const newName = String(editingRestoreName[key] || '').trim()
  const oldName = restoreNameFull(record)
  delete editingRestoreName[key]
  delete restoreNameInputRefs[key]
  if (newName === oldName) return
  if (!validateRestoreName(newName)) return
  try {
    await updateRestoreRunNameApi(record.id, { name: newName })
    record.name = newName
    const sourceList = record.run_kind === 'physical' ? physicalTableData.value : tableData.value
    const matched = (sourceList || []).find((item) => Number(item.id) === Number(record.id))
    if (matched) matched.name = newName
    message.success('任务名称已更新')
  } catch (error) {
    message.error(error?.message || '任务名称更新失败')
  }
}

const uiState = reactive({
  loading: false,
  physicalLoading: false,
  modalOpen: false,
  modalLoading: false,
  modalMode: 'create',
  instancesLoading: false,
  setsLoading: false,
  nodesLoading: false,
  precheckLoading: false,
  executePlanOpen: false,
  executePlanLoading: false,
})

const query = reactive({
  search: '',
  restore_type: '',
  status: '',
  dateRange: [],
  page: 1,
  page_size: 10,
  is_page: false,
})

const tableData = ref([])
const tableWrapRef = ref()
const tableScrollY = ref(360)
const logScrollRef = ref()
const instances = ref([])
const sets = ref([])
const backupNodes = ref([])
const formRef = ref()
let refreshTimer = null
let routeRestoreSyncing = false
let resizeObserver = null

const buildDefaultFormData = () => ({
  id: null,
  name: '',
  backup_set_id: null,
  target_instance_id: null,
  target_node_id: null,
  target_data_dir: '',
  target_port: 5433,
  auto_start: true,
  restore_mode: 'new',
  mode: 'plan',
  target_db_name: '',
  target_db_suffix: '',
  selected_databases: [],
  selected_schemas: [],
  selected_tables: [],
  parallel: 4,
  timeout: '4h',
  target_charset: '',
  target_collation: '',
  target_encoding: '',
  close_binlog: false,
  foreign_key_checks: false,
  exit_on_error: true,
  no_owner: true,
  no_privileges: true,
  clean_if_exists: false,
  is_cloud: false,
  execute_confirmed: false,
})

const formData = reactive(buildDefaultFormData())

const detailState = reactive({
  open: false,
  loading: false,
  actionLoading: false,
  runID: null,
  run: null,
  items: [],
  itemStatistics: {},
})

const logState = reactive({
  open: false,
  loading: false,
  runID: null,
  run: null,
  items: [],
  itemID: 0,
  content: '',
  meta: {},
  autoFollow: true,
})

const executePlanState = reactive({
  open: false,
  loading: false,
  run: null,
  form: {
    confirmChecked: false,
  },
})

const executePlanFormRef = ref()
const executePlanRules = {
  confirm: [
    {
      validator: (_, value) =>
        value ? Promise.resolve() : Promise.reject(new Error('请勾选执行确认')),
      trigger: 'change',
    },
  ],
}

const physicalTableData = ref([])
const precheckState = reactive({
  passed: false,
  summary: '',
  items: [],
  resolved: null,
})
const physicalDetailState = reactive({
  open: false,
  id: null,
  run: null,
  logContent: '',
  logMeta: {},
})

const statusOptions = [
  { label: '全部', value: '' },
  { label: '等待中', value: 'PENDING' },
  { label: '运行中', value: 'RUNNING' },
  { label: '成功', value: 'SUCCESS' },
  { label: '失败', value: 'FAILED' },
  { label: '已终止', value: 'STOPPED' },
]

const restoreTypeOptions = [
  { label: '全部', value: '' },
  { label: '逻辑恢复', value: 'logical' },
  { label: '物理恢复', value: 'physical' },
]

const columns = [
  { title: '任务ID', key: 'id', dataIndex: 'id', width: 220 },
  { title: '恢复方式', key: 'restore_mode', width: 180 },
  { title: '源数据源', key: 'source', width: 240 },
  { title: '目标数据源', key: 'target', width: 240 },
  { title: '创建人', key: 'created_by', width: 140 },
  { title: '创建时间', key: 'created_at', width: 220 },
  // 原「状态」「进度」两列合并为一列「状态」：上方进度条、下方状态文字
  // 状态列移动到操作列之前，并与操作列一起右侧冻结
  // 状态列表头与表体内容水平居中，与「备份集」页面保持一致
  { title: '状态', key: 'status', width: 180, fixed: 'right', align: 'center' },
  // 操作列表头与表体内容水平居中，与「数据备份」「备份集」页面保持一致
  { title: '操作', key: 'action', width: 160, fixed: 'right', align: 'center' },
]

const precheckColumns = [
  { title: '检查项', dataIndex: 'label', key: 'label', width: 180 },
  { title: '结果', key: 'passed', width: 90 },
  { title: '说明', key: 'message' },
]

const tableScroll = computed(() => ({
  x: TABLE_SCROLL_X,
  y: tableScrollY.value,
}))

const pagination = computed(() => ({
  total: filteredRows.value.length,
  current: query.page,
  pageSize: query.page_size,
  showSizeChanger: true,
  showQuickJumper: filteredRows.value.length > query.page_size,
  showTotal: (t) => `共 ${t} 条`,
  hideOnSinglePage: false,
  pageSizeOptions: ['10', '20', '50', '100'],
}))

const tableLoading = computed(() => uiState.loading || uiState.physicalLoading)

const logicalRows = computed(() =>
  (tableData.value || []).map((item) => {
    const sourceDisplay = buildInstanceDisplay(
      item.source_remark,
      item.source_host,
      item.source_port,
    )
    const targetDisplay = buildInstanceDisplay(
      item.target_remark,
      item.target_host,
      item.target_port,
    )
    return {
      ...item,
      row_key: `logical-${item.id}`,
      run_kind: 'logical',
      task_id: item.task_id || '-',
      name: item.name || '',
      source_primary: sourceDisplay,
      source_secondary: formatHostPort(item.source_host, item.source_port),
      source_tooltip: buildDataSourceTooltip(
        item.source_remark,
        item.source_host,
        item.source_port,
        item.set_name,
      ),
      target_primary: targetDisplay,
      target_secondary: formatHostPort(item.target_host, item.target_port),
      target_tooltip: buildDataSourceTooltip(
        item.target_remark,
        item.target_host,
        item.target_port,
        item.target_display,
      ),
      restore_mode_label: '逻辑恢复',
      restore_mode_desc: '',
      current_stage_label: logicalStageLabel(item.current_stage),
    }
  }),
)

const physicalRows = computed(() =>
  (physicalTableData.value || []).map((item) => ({
    ...item,
    row_key: `physical-${item.id}`,
    run_kind: 'physical',
    task_id: item.task_id || '-',
    name: item.name || item.backup_set_name || '物理恢复任务',
    source_primary: buildInstanceDisplay(item.source_remark, item.source_host, item.source_port),
    source_secondary: formatHostPort(item.source_host, item.source_port),
    source_tooltip: buildDataSourceTooltip(
      item.source_remark,
      item.source_host,
      item.source_port,
      item.backup_set_name,
    ),
    target_primary:
      String(
        item.target_name || item.target_remark || item.target_node_name || item.target_host || '-',
      ).trim() || '-',
    target_secondary: formatHostPort(item.target_host, item.target_port),
    target_tooltip: [
      `目标名称：${String(item.target_name || item.target_remark || item.target_node_name || '-').trim() || '-'}`,
      `目标地址：${formatHostPort(item.target_host, item.target_port)}`,
    ].join('\n'),
    restore_mode_label: '物理恢复',
    restore_mode_desc: '',
    current_stage_label: item.current_stage_label || logicalStageLabel(item.current_stage),
  })),
)

const mergedRows = computed(() =>
  [...logicalRows.value, ...physicalRows.value].sort(
    (left, right) => timeValue(right.created_at) - timeValue(left.created_at),
  ),
)

const filteredRows = computed(() =>
  mergedRows.value.filter((item) => {
    if (query.restore_type && item.run_kind !== query.restore_type) {
      return false
    }
    if (
      query.status &&
      String(item.status || '').toUpperCase() !== String(query.status || '').toUpperCase()
    ) {
      return false
    }
    const hasRange =
      Array.isArray(query.dateRange) &&
      query.dateRange.length === 2 &&
      query.dateRange[0] != null &&
      String(query.dateRange[0] || '').trim() !== '' &&
      query.dateRange[1] != null &&
      String(query.dateRange[1] || '').trim() !== ''
    if (hasRange) {
      const current = timeValue(item.created_at)
      const start = dayjs(query.dateRange[0]).startOf('day').valueOf()
      const end = dayjs(query.dateRange[1]).endOf('day').valueOf()
      if (current > 0 && (current < start || current > end)) {
        return false
      }
    }
    const keyword = String(query.search || '')
      .trim()
      .toLowerCase()
    if (!keyword) return true
    const haystack = [
      item.task_id,
      item.name,
      item.restore_mode_label,
      item.restore_mode_desc,
      item.source_primary,
      item.source_secondary,
      item.target_primary,
      item.target_secondary,
      item.created_by,
      item.backup_set_name,
      item.target_data_dir,
    ]
      .join(' ')
      .toLowerCase()
    return haystack.includes(keyword)
  }),
)

// 分页越界纠偏单独放 watch：computed 内写 query.page 属于副作用，会让依赖追踪失真
watch(
  () => [filteredRows.value.length, query.page_size],
  () => {
    const total = filteredRows.value.length
    const pageSize = Math.max(1, Number(query.page_size || 10))
    const maxPage = Math.max(1, Math.ceil(total / pageSize))
    const page = Math.min(Math.max(1, Number(query.page || 1)), maxPage)
    if (query.page !== page) query.page = page
  },
)

const pagedRows = computed(() => {
  const pageSize = Math.max(1, Number(query.page_size || 10))
  const maxPage = Math.max(1, Math.ceil(filteredRows.value.length / pageSize))
  const page = Math.min(Math.max(1, Number(query.page || 1)), maxPage)
  const start = (page - 1) * pageSize
  return filteredRows.value.slice(start, start + pageSize)
})

const backupSetOptions = computed(() =>
  sets.value.map((item) => ({
    value: item.id,
    label: `${item.db_type} | ${item.name} | ${item.object_summary || item.source_db_name || '-'}`,
  })),
)

const nodeOptions = computed(() =>
  (backupNodes.value || []).map((item) => ({
    label: `${item.name} | ${item.host || '-'} | ${item.status || '-'}`,
    value: item.id,
  })),
)

const selectedBackupSet = computed(
  () => sets.value.find((item) => item.id === formData.backup_set_id) || null,
)

// 金仓类型归一（Kingbase / KingbaseES 两种写法都认）
const isKingbaseDbType = (dbType) => /^kingbase/i.test(String(dbType || '').trim())

// 金仓（Kingbase）与 PostgreSQL 共用逻辑恢复通道：sys_restore / ksql 的参数与
// pg_restore / psql 同源，所以「忽略 Owner / 忽略权限 / 清理已存在对象 / 目标编码」
// 这些选项对金仓同样有效，必须一起放开，否则金仓备份集在向导里少一半选项。
const isKingbaseSet = computed(() => {
  const dbType = String(selectedBackupSet.value?.db_type || '').trim().toLowerCase()
  return dbType === 'kingbase' || dbType === 'kingbasees'
})

const isPostgresLogicalMultiRestore = computed(
  () =>
    (selectedBackupSet.value?.db_type === 'PostgreSQL' || isKingbaseSet.value) &&
    selectedBackupSet.value?.driver_type !== 'PG_BACKREST' &&
    (selectedBackupSet.value?.object_mode === 'ALL' || !selectedBackupSet.value?.source_db_name),
)

const isPhysicalRestoreSet = computed(() => {
  const set = selectedBackupSet.value
  if (!set) return false
  const driverType = String(set.driver_type || '').toUpperCase()
  if (set.db_type === 'PostgreSQL' && driverType === 'PG_BACKREST') return true
  if (set.db_type === 'MySQL' && driverType === 'XTRABACKUP') return true
  return false
})

// 放在 isPhysicalRestoreSet 之后：computed 虽惰性求值，但依赖顺序更清晰，
// 也避免后续维护者误以为可以提前读取。
const isPostgresLogicalSet = computed(
  () =>
    !isPhysicalRestoreSet.value &&
    (selectedBackupSet.value?.db_type === 'PostgreSQL' || isKingbaseSet.value),
)

const selectedSetIsMySQL = computed(() => {
  const set = selectedBackupSet.value
  return !!set && set.db_type === 'MySQL' && String(set.driver_type || '').toUpperCase() === 'XTRABACKUP'
})

const mysqlInstanceOptions = computed(() =>
  (instances.value || [])
    .filter((item) => String(item?.raw?.db_type || item?.db_type || '').toUpperCase() === 'MYSQL')
    .map((item) => ({
      label: `${item.raw?.remark || item.raw?.hostname || '-'} | ${item.raw?.hostname || '-'}:${item.raw?.port || '-'}`,
      value: item.value,
    })),
)

const supportsDatabaseSelection = computed(
  () =>
    !isPhysicalRestoreSet.value &&
    !!selectedBackupSet.value &&
    (selectedBackupSet.value?.object_mode === 'ALL' || !selectedBackupSet.value?.source_db_name),
)

const supportsSchemaSelection = computed(() => isPostgresLogicalSet.value)

const supportsTableSelection = computed(() => isPostgresLogicalSet.value)

const restoreObjectDisplay = computed(() => {
  if (!selectedBackupSet.value) return ''
  if (isPhysicalRestoreSet.value) return '实例级物理恢复，按备份集中的整个数据目录执行恢复'
  return selectedBackupSet.value.object_summary || selectedBackupSet.value.source_db_name || '-'
})

const chainFormatBytes = (bytes = 0) => {
  const b = Number(bytes) || 0
  if (b < 1024) return `${b} B`
  const kb = b / 1024
  if (kb < 1024) return `${kb.toFixed(1)} KB`
  const mb = kb / 1024
  if (mb < 1024) return `${mb.toFixed(1)} MB`
  const gb = mb / 1024
  return `${gb.toFixed(1)} GB`
}

const chainFormatDate = (value) => {
  if (!value) return '-'
  const s = String(value).replace('T', ' ').slice(0, 16)
  return s || '-'
}

const backupTypeChipLabel = (type) => {
  const t = String(type || '').toUpperCase()
  if (t === 'FULL') return 'FULL 全量'
  if (t === 'DIFF') return 'DIFF 差异'
  if (t === 'INCR') return 'INCR 增量'
  if (t === 'SCHEMA') return '结构'
  if (t === 'DATA') return '数据'
  return '备份集'
}

const backupTypeChipColor = (type) => {
  const t = String(type || '').toUpperCase()
  if (t === 'FULL') return 'green'
  if (t === 'DIFF') return 'orange'
  if (t === 'INCR') return 'blue'
  if (t === 'SCHEMA') return 'purple'
  if (t === 'DATA') return 'cyan'
  return 'default'
}

const selectedChain = computed(() => {
  const set = selectedBackupSet.value
  if (!set) return { items: [], total: 0, hasAncestor: true, totalSizeText: '' }
  if (String(set.driver_type || '').toUpperCase() !== 'PG_BACKREST') {
    return {
      items: [
        {
          id: set.id,
          backup_type: set.backup_type || '',
          label: backupTypeChipLabel(set.backup_type),
          color: backupTypeChipColor(set.backup_type),
          name: set.name,
          createdText: chainFormatDate(set.created_at),
          sizeText: chainFormatBytes(set.size_bytes),
        },
      ],
      total: 1,
      hasAncestor: true,
      totalSizeText: chainFormatBytes(set.size_bytes),
    }
  }
  const backupType = String(set.backup_type || 'FULL').toUpperCase()
  if (backupType === 'FULL') {
    return {
      items: [
        {
          id: set.id,
          backup_type: 'FULL',
          label: 'FULL 全量',
          color: 'green',
          name: set.name,
          createdText: chainFormatDate(set.created_at),
          sizeText: chainFormatBytes(set.size_bytes),
        },
      ],
      total: 1,
      hasAncestor: true,
      totalSizeText: chainFormatBytes(set.size_bytes),
    }
  }
  const instanceId = set.source_instance_id || set.instance_id || 0
  const taskId = set.task_id || 0
  const sameScope = (sets.value || []).filter((s) => {
    if (String(s.driver_type || '').toUpperCase() !== 'PG_BACKREST') return false
    if (instanceId && Number(s.source_instance_id || s.instance_id || 0) !== Number(instanceId))
      return false
    if (taskId && Number(s.task_id || 0) !== Number(taskId)) return false
    return true
  })
  sameScope.sort((a, b) => new Date(a.created_at || 0) - new Date(b.created_at || 0))
  const fullList = sameScope.filter((s) => String(s.backup_type || '').toUpperCase() === 'FULL')
  const createdAt = new Date(set.created_at || 0).getTime()
  let anchorFull = null
  for (let i = fullList.length - 1; i >= 0; i -= 1) {
    const fAt = new Date(fullList[i].created_at || 0).getTime()
    if (fAt <= createdAt) {
      anchorFull = fullList[i]
      break
    }
  }
  if (!anchorFull) {
    return {
      items: [
        {
          id: set.id,
          backup_type: backupType,
          label: backupTypeChipLabel(backupType),
          color: backupTypeChipColor(backupType),
          name: set.name,
          createdText: chainFormatDate(set.created_at),
          sizeText: chainFormatBytes(set.size_bytes),
        },
      ],
      total: 1,
      hasAncestor: false,
      totalSizeText: chainFormatBytes(set.size_bytes),
    }
  }
  const anchorAt = new Date(anchorFull.created_at || 0).getTime()
  const betweenList = sameScope.filter((s) => {
    if (s.id === anchorFull.id) return false
    if (s.id === set.id) return false
    if (String(s.backup_type || '').toUpperCase() === 'FULL') return false
    const cAt = new Date(s.created_at || 0).getTime()
    return cAt > anchorAt && cAt < createdAt
  })
  const chainNodes = [anchorFull, ...betweenList, set]
  let totalBytes = 0
  const items = chainNodes.map((s) => {
    const t = String(s.backup_type || '').toUpperCase()
    totalBytes += Number(s.size_bytes || 0)
    return {
      id: s.id,
      backup_type: t,
      label: backupTypeChipLabel(t),
      color: backupTypeChipColor(t),
      name: s.name,
      createdText: chainFormatDate(s.created_at),
      sizeText: chainFormatBytes(s.size_bytes),
    }
  })
  return {
    items,
    total: items.length,
    hasAncestor: true,
    totalSizeText: chainFormatBytes(totalBytes),
  }
})

const targetDbPlaceholder = computed(() =>
  isPostgresLogicalMultiRestore.value
    ? '例如 _restore_20260806143000，可留空表示同名恢复'
    : '例如 orders_restore_20260806143000',
)

const targetDbExtra = computed(() =>
  isPhysicalRestoreSet.value
    ? '物理恢复在当前数据恢复页面内完成，需先执行预检查后再提交。'
    : isPostgresLogicalMultiRestore.value
      ? `系统会识别备份集中的全部 ${isKingbaseSet.value ? '金仓' : 'PostgreSQL'} 数据库，并按“源库名 + 后缀”批量恢复。`
      : '逻辑恢复默认要求新目标库名，若目标库已存在会直接拒绝提交。',
)

const logTargetOptions = computed(() => {
  const options = [{ label: '主日志', value: 0 }]
  ;(logState.items || []).forEach((item) => {
    options.push({
      label: `${item.source_object_name || '-'} -> ${item.target_object_name || '-'}`,
      value: item.id,
    })
  })
  return options
})

const physicalDetailPrecheckItems = computed(
  () => physicalDetailState.run?.precheck_summary?.items || [],
)

const parseJSONPayload = (value, fallback = null) => {
  if (value == null || value === '') return fallback
  if (typeof value === 'string') {
    try {
      return JSON.parse(value)
    } catch {
      return fallback
    }
  }
  if (typeof value === 'object') return value
  return fallback
}

const normalizeStringArray = (value) => {
  if (Array.isArray(value)) {
    return value.map((item) => String(item || '').trim()).filter(Boolean)
  }
  if (typeof value === 'string') {
    return value
      .split(',')
      .map((item) => item.trim())
      .filter(Boolean)
  }
  return []
}

const normalizeLogicalRestoreMode = (value) => {
  const text = String(value || '')
    .trim()
    .toLowerCase()
  if (text === 'overwrite') return 'overwrite'
  if (text === 'append') return 'append'
  return 'new'
}

const normalizeLogicalExecutionMode = (value) => {
  const text = String(value || '')
    .trim()
    .toLowerCase()
  return text === 'execute' ? 'execute' : 'plan'
}

const logicalRestoreModeLabel = (value) => {
  const text = String(value || '')
    .trim()
    .toUpperCase()
  if (text === 'NEW' || text === 'NEW_INSTANCE') return '新建恢复'
  if (text === 'OVERWRITE') return '覆盖恢复'
  if (text === 'APPEND') return '追加恢复'
  return value ? String(value) : '-'
}

const resetFormData = () => {
  Object.assign(formData, buildDefaultFormData())
}

const rules = {
  name: [{ max: 128, message: '任务名称不能超过 128 个字符', trigger: 'blur' }],
  backup_set_id: [{ required: true, message: '请选择备份集', trigger: 'change' }],
  target_instance_id: [
    {
      validator: () =>
        isPhysicalRestoreSet.value || formData.target_instance_id
          ? Promise.resolve()
          : Promise.reject(new Error('请选择目标实例')),
      trigger: 'change',
    },
  ],
  restore_mode: [
    {
      validator: () =>
        isPhysicalRestoreSet.value || String(formData.restore_mode || '').trim()
          ? Promise.resolve()
          : Promise.reject(new Error('请选择恢复模式')),
      trigger: 'change',
    },
  ],
  mode: [
    {
      validator: () =>
        isPhysicalRestoreSet.value || String(formData.mode || '').trim()
          ? Promise.resolve()
          : Promise.reject(new Error('请选择运行模式')),
      trigger: 'change',
    },
  ],
  target_db_name: [
    {
      validator: (_, value) => {
        const text = String(value || '').trim()
        if (!text) {
          if (isPhysicalRestoreSet.value || isPostgresLogicalMultiRestore.value)
            return Promise.resolve()
          return Promise.reject(new Error('请输入目标库名'))
        }
        if (!/^[A-Za-z0-9_-]+$/.test(text)) {
          return Promise.reject(
            new Error(
              isPostgresLogicalMultiRestore.value
                ? '后缀仅支持字母、数字、下划线(_)与短横线(-)'
                : '库名仅支持字母、数字、下划线(_)与短横线(-)',
            ),
          )
        }
        return Promise.resolve()
      },
      trigger: 'blur',
    },
  ],
  target_db_suffix: [
    {
      validator: (_, value) => {
        const text = String(value || '').trim()
        if (!isPostgresLogicalMultiRestore.value || !text) return Promise.resolve()
        if (!/^[A-Za-z0-9_-]+$/.test(text)) {
          return Promise.reject(new Error('后缀仅支持字母、数字、下划线(_)与短横线(-)'))
        }
        return Promise.resolve()
      },
      trigger: 'blur',
    },
  ],
}

const isDirtyTime = (value) => {
  const text = String(value || '').trim()
  return !text || text.startsWith('0001-01-01')
}

const formatDateTime = (value) => {
  if (isDirtyTime(value)) return '-'
  return String(value).replace('T', ' ').slice(0, 19)
}

const timeValue = (value) => {
  if (isDirtyTime(value)) return 0
  const parsed = dayjs(String(value))
  return parsed.isValid() ? parsed.valueOf() : 0
}

const formatSize = (size) => {
  const value = Number(size || 0)
  if (!value) return '-'
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  if (value < 1024 * 1024 * 1024) return `${(value / 1024 / 1024).toFixed(1)} MB`
  return `${(value / 1024 / 1024 / 1024).toFixed(2)} GB`
}

const formatHostPort = (host, port) => {
  const left = String(host || '').trim()
  const right = Number(port || 0)
  if (!left && !right) return '-'
  return `${left || '-'}:${right || '-'}`
}

const statusLabel = (status, stage = '', executionMode = '') => {
  const text = String(status || '').toUpperCase()
  const currentStage = String(stage || '').toUpperCase()
  const planMode = String(executionMode || '').toUpperCase() === 'PLAN'
  if (text === 'FAILED' && currentStage === 'PRECHECK') return '预检查失败'
  if (text === 'SUCCESS') return '成功'
  if (text === 'FAILED') return '失败'
  if (text === 'RUNNING') return '运行中'
  if (text === 'PENDING' && planMode) return '计划待执行'
  if (text === 'PENDING') return '等待中'
  if (text === 'STOP_REQUESTED') return '终止中'
  if (text === 'STOPPED') return '已终止'
  const fallbackMap = {
    PENDING: '等待中',
    RUNNING: '运行中',
    SUCCESS: '成功',
    FAILED: '失败',
    STOPPED: '已终止',
    STOP_REQUESTED: '终止中',
    FINISHED: '成功',
  }
  return fallbackMap[text] || (status ? String(status) : '未知')
}

const statusClass = (status, stage = '', executionMode = '') => {
  const text = String(status || '').toUpperCase()
  const currentStage = String(stage || '').toUpperCase()
  const planMode = String(executionMode || '').toUpperCase() === 'PLAN'
  if (text === 'PENDING' && planMode) return 'status-plan'
  if (text === 'SUCCESS') return 'status-success'
  if (text === 'FAILED' && currentStage === 'PRECHECK') return 'status-failed'
  if (text === 'FAILED') return 'status-failed'
  if (text === 'RUNNING' || text === 'STOP_REQUESTED') return 'status-running'
  if (text === 'PENDING') return 'status-pending'
  if (text === 'STOPPED') return 'status-stopped'
  return 'status-default'
}

const physicalStatusClass = (status) => {
  const text = String(status || '').toUpperCase()
  if (text === 'SUCCESS') return 'status-success'
  if (text === 'FAILED') return 'status-failed'
  if (text === 'RUNNING') return 'status-running'
  if (text === 'PENDING') return 'status-pending'
  if (text === 'STOPPED') return 'status-stopped'
  return 'status-default'
}

const statusTagLabel = (record) =>
  statusLabel(record?.status, record?.current_stage, record?.execution_mode)

const displayProgress = (record) => {
  if (String(record?.status || '').toUpperCase() === 'SUCCESS') return 100
  return Math.max(0, Math.min(100, Number(record?.progress_percent || 0)))
}

const rowClassName = (record) => {
  const status = String(record?.status || '').toUpperCase()
  if (status === 'FAILED') return 'restore-row-failed'
  if (status === 'STOPPED') return 'restore-row-stopped'
  return ''
}

const buildInstanceDisplay = (remark, host, port) =>
  String(remark || '').trim() || formatHostPort(host, port)

const buildDataSourceTooltip = (remark, host, port, fallback = '-') =>
  [
    `实例名称：${String(remark || '').trim() || fallback || '-'}`,
    `实例地址：${formatHostPort(host, port)}`,
  ].join('\n')

const logicalStageLabel = (stage) => {
  const text = String(stage || '').toUpperCase()
  if (!text) return '-'
  if (text === 'PENDING') return '等待执行'
  if (text === 'PRECHECK') return '预检查'
  if (text === 'RUNNING' || text === 'EXECUTING') return '执行中'
  if (text === 'SUCCESS' || text === 'COMPLETED') return '已完成'
  if (text === 'FAILED') return '执行失败'
  if (text === 'STOP_REQUESTED') return '终止中'
  if (text === 'STOPPED') return '已终止'
  return stage || '-'
}

const modalTitle = computed(() => (uiState.modalMode === 'edit' ? '编辑恢复任务' : '创建恢复'))
const modalOkText = computed(() => (uiState.modalMode === 'edit' ? '保存修改' : '提交恢复'))

const copyText = async (text, successMessage = '复制成功') => {
  const value = String(text || '')
  if (!value) {
    message.warning('没有可复制的内容')
    return
  }
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(value)
    } else if (typeof document !== 'undefined') {
      const textarea = document.createElement('textarea')
      textarea.value = value
      textarea.setAttribute('readonly', 'readonly')
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      document.body.removeChild(textarea)
    }
    message.success(successMessage)
  } catch {
    message.error('复制失败')
  }
}

const extractFilename = (headers = {}, fallback = 'restore.log') => {
  const cd = headers?.['content-disposition'] || headers?.['Content-Disposition']
  if (!cd) return fallback
  const match = /filename\*=UTF-8''([^;]+)|filename="?([^"]+)"?/i.exec(cd)
  const raw = decodeURIComponent(match?.[1] || match?.[2] || '')
  return raw || fallback
}

const canEditRestore = (record) => {
  if (!canManageRestore.value) return false
  const status = String(record?.status || '').toUpperCase()
  const stage = String(record?.current_stage || '').toUpperCase()
  return status === 'PENDING' || (status === 'FAILED' && stage === 'PRECHECK')
}

const canStopRestore = (record) => {
  if (!canManageRestore.value) return false
  const status = String(record?.status || '').toUpperCase()
  // STOP_REQUESTED 也要能点：此时终止请求已下发但进程无响应，
  // 再次点击会触发服务端强制收口，解锁「终止不了/删不掉/不能重跑」的中间态。
  return status === 'RUNNING' || status === 'STOP_REQUESTED'
}

// 重跑：仅终态可用，物理恢复不支持（后端 retry 接口只覆盖逻辑恢复链路）
const canRetryRestore = (record) => {
  if (!canManageRestore.value) return false
  const physical =
    String(record?.restore_kind || '').toUpperCase() === 'PHYSICAL' ||
    String(record?.driver_type || '').toUpperCase() === 'PHYSICAL'
  if (physical) return false
  return ['FAILED', 'STOPPED', 'SUCCESS'].includes(String(record?.status || '').toUpperCase())
}

const canExecutePendingPlan = (record) => {
  if (!canManageRestore.value) return false
  if (String(record?.status || '').toUpperCase() !== 'PENDING') return false
  if (String(record?.execution_mode || '').toUpperCase() !== 'PLAN') return false
  return true
}

const openExecutePlanDialog = (record) => {
  if (!record || !record.id) return
  executePlanState.run = {
    id: record.id,
    task_id: record.task_id,
    restore_mode: record.restore_mode,
    target_db_name: record.target_db_name,
    target_display: record.target_display,
  }
  executePlanState.form.confirmChecked = false
  uiState.executePlanOpen = true
  nextTick(() => {
    executePlanFormRef.value?.clearValidate?.()
  })
}

const closeExecutePlanDialog = () => {
  if (uiState.executePlanLoading) return
  uiState.executePlanOpen = false
  executePlanState.run = null
  executePlanState.form.confirmChecked = false
}

const confirmExecutePlan = async () => {
  if (!executePlanState.run?.id) return
  try {
    await executePlanFormRef.value?.validate()
  } catch {
    return
  }
  const runId = executePlanState.run.id
  uiState.executePlanLoading = true
  try {
    const res = await getRestoreRunDetailApi(runId)
    const run = res?.data?.run || {}
    const options = parseJSONPayload(run.options, {})
    const logicalPayload = {
      name: run.name || executePlanState.run.task_id || '',
      backup_set_id: Number(run.backup_set_id || 0) || null,
      target_instance_id: Number(run.target_instance_id || 0) || null,
      target_db_name: options.target_db_name || run.target_db_name || '',
      target_db_suffix: options.target_db_suffix || '',
      mode: 'execute',
      restore_mode: normalizeLogicalRestoreMode(
        run.restore_mode || options.restore_mode,
      ),
      selected_databases: normalizeStringArray(options.selected_databases),
      selected_schemas: normalizeStringArray(options.selected_schemas),
      selected_tables: normalizeStringArray(options.selected_tables),
      parallel: Number(options.parallel || 4) || 4,
      timeout: String(options.timeout || '4h').trim() || '4h',
      target_charset: String(options.target_charset || '').trim(),
      target_collation: String(options.target_collation || '').trim(),
      target_encoding: String(options.target_encoding || '').trim(),
      close_binlog: !!options.close_binlog,
      foreign_key_checks: !!options.foreign_key_checks,
      exit_on_error: options.exit_on_error !== false,
      no_owner: options.no_owner !== false,
      no_privileges: options.no_privileges !== false,
      clean_if_exists: !!options.clean_if_exists,
      is_cloud: !!options.is_cloud,
      execute_confirmed: true,
    }
    await updateRestoreRunApi({
      id: runId,
      ...logicalPayload,
    })
    message.success('已切换为执行模式并进入执行队列')
    uiState.executePlanOpen = false
    executePlanState.run = null
    executePlanState.form.confirmChecked = false
    await refreshAll()
  } catch (err) {
    const text =
      err?.response?.data?.error_message ||
      err?.response?.data?.message ||
      err?.message ||
      '提交执行失败'
    message.error(text)
  } finally {
    uiState.executePlanLoading = false
  }
}

const resetPrecheckState = () => {
  precheckState.passed = false
  precheckState.summary = ''
  precheckState.items = []
  precheckState.resolved = null
}

const fetchRestores = async () => {
  uiState.loading = true
  try {
    const rows = await fetchRestoresRetry(2)
    tableData.value = rows
  } catch (err) {
    if (typeof console === 'object' && console && typeof console.error === 'function') {
      try {
        console.error('[BackupRestore] fetchRestores failed:', err)
      } catch {
          /* 日志/提示失败不影响主流程 */
        }
    }
    const skipToast = isCanceledRequestError(err)
    if (!skipToast && typeof message === 'function') {
      try {
        message.warning('逻辑恢复列表加载失败，已降级显示物理恢复')
      } catch {
          /* 日志/提示失败不影响主流程 */
        }
    }
  } finally {
    uiState.loading = false
    await nextTick()
    updateTableScrollY()
    setTimeout(() => updateTableScrollY(), 60)
    setTimeout(() => updateTableScrollY(), 180)
  }
}

const fetchPhysicalRestores = async () => {
  uiState.physicalLoading = true
  try {
    const rows = await fetchPhysicalRestoresRetry(2)
    physicalTableData.value = rows
  } catch (err) {
    if (typeof console === 'object' && console && typeof console.error === 'function') {
      try {
        console.error('[BackupRestore] fetchPhysicalRestores failed:', err)
      } catch {
          /* 日志/提示失败不影响主流程 */
        }
    }
    const skipToast = isCanceledRequestError(err)
    if (!skipToast && typeof message === 'function') {
      try {
        message.warning('物理恢复列表加载失败，已降级显示逻辑恢复')
      } catch {
          /* 日志/提示失败不影响主流程 */
        }
    }
  } finally {
    uiState.physicalLoading = false
    await nextTick()
    updateTableScrollY()
    setTimeout(() => updateTableScrollY(), 60)
    setTimeout(() => updateTableScrollY(), 180)
  }
}

const loadRestoreDetailPayload = async (id) => {
  const res = await getRestoreRunDetailApi(id)
  return res?.data || {}
}

const fillDetailState = async (id) => {
  if (!id) return
  detailState.loading = true
  try {
    const payload = await loadRestoreDetailPayload(id)
    detailState.runID = id
    detailState.run = payload.run || null
    detailState.items = payload.items || []
    detailState.itemStatistics = payload.item_statistics || {}
  } finally {
    detailState.loading = false
  }
}

const fillLogState = async (id, itemID = 0, keepFollow = true) => {
  if (!id) return
  logState.loading = true
  try {
    const payload = await loadRestoreDetailPayload(id)
    logState.runID = id
    logState.run = payload.run || null
    logState.items = payload.items || []
    logState.itemID = itemID && logState.items.some((item) => item.id === itemID) ? itemID : 0
    const logRes = await getRestoreRunLogApi(id, {
      item_id: logState.itemID || undefined,
      tail_kb: 128,
    })
    logState.meta = logRes?.data || {}
    logState.content = logState.meta.log_excerpt || ''
    if (!keepFollow) {
      logState.autoFollow = true
    }
  } finally {
    logState.loading = false
    await nextTick()
    syncLogScrollToBottom()
  }
}

const openDetailRoute = async (id) => {
  if (!id) return
  const nextQuery = { ...route.query }
  delete nextQuery.run_id
  await router.push({
    path: route.path,
    query: {
      ...nextQuery,
      detail_id: String(id),
    },
  })
}

const openDetail = async (id) => {
  detailState.open = true
  await fillDetailState(id)
}

const closeDetail = async () => {
  detailState.open = false
  detailState.runID = null
  detailState.run = null
  detailState.items = []
  detailState.itemStatistics = {}
  if (route.query.detail_id) {
    const nextQuery = { ...route.query }
    delete nextQuery.detail_id
    await router.replace({ path: route.path, query: nextQuery })
  }
}

// 详情面板「刷新」按钮：走与轮询一致的填充逻辑，避免另写一份取数
const refreshDetail = async () => {
  if (detailState.runID) await fillDetailState(detailState.runID)
}

// 列表「更多 → 重跑」：按相同配置新建一条恢复任务（不是同一条记录重跑）
const handleRetryRestore = async (record) => {
  const id = Number(record?.id || 0)
  if (!id) return
  try {
    const res = await retryRestoreRunApi(id)
    message.success(res?.data?.message || '已按相同配置重新发起恢复任务')
    await refreshAll()
  } catch (err) {
    message.error(err?.response?.data?.message || '重跑失败')
  }
}

// 详情面板「下载日志」：不依赖日志抽屉是否打开，直接按当前任务下载
const handleDownloadDetailLog = async () => {
  const id = Number(detailState.runID || 0)
  if (!id) return
  const res = await downloadRestoreRunLogApi(id, {})
  const blob = res.data
  const filename = extractFilename(res.headers, `restore-run-${id}.log`)
  const url = window.URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  window.URL.revokeObjectURL(url)
  message.success('已开始下载日志')
}

const openLogDrawer = async (record, itemID = 0) => {
  const id = record?.id || record
  if (!id) return
  logState.open = true
  await fillLogState(id, itemID, false)
}

const closeLogDrawer = () => {
  logState.open = false
  logState.runID = null
  logState.run = null
  logState.items = []
  logState.itemID = 0
  logState.content = ''
  logState.meta = {}
  logState.autoFollow = true
}

const fetchLogContent = async () => {
  if (!logState.runID) return
  await fillLogState(logState.runID, logState.itemID, true)
}

const handleLogScroll = () => {
  const container = logScrollRef.value
  if (!container) return
  const distance = container.scrollHeight - container.scrollTop - container.clientHeight
  logState.autoFollow = distance <= 24
}

const syncLogScrollToBottom = () => {
  const container = logScrollRef.value
  if (!container || !logState.autoFollow) return
  container.scrollTop = container.scrollHeight
}

const handleDownloadLog = async () => {
  if (!logState.runID) return
  const res = await downloadRestoreRunLogApi(logState.runID, {
    item_id: logState.itemID || undefined,
  })
  const blob = res.data
  const filename = extractFilename(res.headers, `restore-run-${logState.runID}.log`)
  const url = window.URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  window.URL.revokeObjectURL(url)
  message.success('已开始下载日志')
}

const loadInstances = async () => {
  uiState.instancesLoading = true
  try {
    const res = await getBackupInstancesApi(
      { is_page: false },
      { skipGlobalMessageError: true, silentHttpError: true },
    ).catch(() => null)
    instances.value = (res?.data || []).map((item) => {
      const hostname = String(item?.hostname || '').trim()
      const port = String(item?.port || '').trim()
      const hostPort = hostname ? (port ? `${hostname}:${port}` : hostname) : ''
      const remark = String(item?.remark || '').trim()
      const displayRemark = remark || hostPort
      const ellipsisRemark =
        displayRemark.length > 15 ? displayRemark.slice(0, 15).trimEnd() + '...' : displayRemark
      const displayLabel =
        ellipsisRemark || hostPort || String(item?.instance_id || '').slice(0, 15)
      const fullLabel = remark && hostPort ? `${remark} | ${hostPort}` : remark || hostPort
      const searchText =
        `${item.db_type || ''} ${item.hostname || ''} ${item.port || ''} ${item.remark || ''} ${hostPort || ''} ${String(item?.instance_id || '')}`.toLowerCase()
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

const filterTargetInstanceOption = (input, option) => {
  const keyword = `${input || ''}`.trim().toLowerCase()
  if (!keyword) return true
  return `${option?.searchText || option?.longLabel || option?.label || ''}`.includes(keyword)
}

const loadSets = async () => {
  uiState.setsLoading = true
  try {
    const res = await getBackupSetsApi(
      { is_page: false, restorable_only: true },
      { skipGlobalMessageError: true, silentHttpError: true },
    ).catch(() => null)
    sets.value = (res?.data || []).filter((item) =>
      ['LOGICAL', 'PG_BACKREST', 'XTRABACKUP'].includes(item.driver_type)
    )
  } finally {
    uiState.setsLoading = false
  }
}

const loadNodes = async () => {
  uiState.nodesLoading = true
  try {
    const res = await getBackupNodesApi(
      { is_page: false },
      { skipGlobalMessageError: true, silentHttpError: true },
    ).catch(() => null)
    backupNodes.value = res?.data || []
  } finally {
    uiState.nodesLoading = false
  }
}

const makeTimestamp = () => dayjs().format('YYYYMMDDHHmmss')
const makeDefaultTargetDBName = (sourceDB) => `${sourceDB || 'db'}_restore_${makeTimestamp()}`
const makeRestoreSuffix = () => `_restore_${makeTimestamp()}`

const handleSetChange = (id) => {
  resetPrecheckState()
  const item = sets.value.find((s) => s.id === id)
  if (!item) return
  if (item.db_type === 'PostgreSQL' && item.driver_type === 'PG_BACKREST') {
    formData.restore_mode = 'NEW_INSTANCE'
    formData.target_db_name = ''
    formData.target_db_suffix = ''
    if (!backupNodes.value.length) {
      loadNodes()
    }
    return
  }
  if (item.db_type === 'MySQL' && item.driver_type === 'XTRABACKUP') {
    formData.restore_mode = 'EXISTING_INSTANCE'
    formData.target_db_name = ''
    formData.target_db_suffix = ''
    if (!instances.value.length) {
      loadInstances()
    }
    return
  }
  formData.restore_mode = normalizeLogicalRestoreMode(formData.restore_mode)
  if (
    (item.db_type === 'PostgreSQL' || isKingbaseDbType(item.db_type)) &&
    item.driver_type !== 'PG_BACKREST' &&
    (item.object_mode === 'ALL' || !item.source_db_name)
  ) {
    if (!String(formData.target_db_suffix || '').trim()) {
      formData.target_db_suffix = makeRestoreSuffix()
    }
    formData.target_db_name = ''
    return
  }
  if (!String(formData.target_db_name || '').trim()) {
    formData.target_db_name = makeDefaultTargetDBName(item.source_db_name)
  }
  formData.target_db_suffix = ''
}

const clearRestoreEntryQuery = async () => {
  if (
    !route.query.backup_set_id &&
    !route.query.open &&
    !route.query.from_task_id &&
    !route.query.from_run_id &&
    !route.query.run_id
  )
    return
  const nextQuery = { ...route.query }
  delete nextQuery.backup_set_id
  delete nextQuery.open
  delete nextQuery.from_task_id
  delete nextQuery.from_run_id
  delete nextQuery.run_id
  await router.replace({ path: route.path, query: nextQuery })
}

// 新建恢复统一走五步向导页（区分逻辑/物理、全量/非全量、同构校验）
const openCreate = async (options = {}) => {
  const presetID = Number(options.backup_set_id || 0) || 0
  uiState.modalOpen = false
  await router.push({
    path: '/backup/restore/wizard',
    query: presetID ? { backup_set_id: String(presetID) } : {},
  })
}

const openEdit = async (record) => {
  if (!canEditRestore(record)) return
  uiState.modalMode = 'edit'
  uiState.modalOpen = true
  if (!instances.value.length) await loadInstances()
  if (!sets.value.length) await loadSets()
  if (record.run_kind === 'physical') {
    if (!backupNodes.value.length) await loadNodes()
    if (!instances.value.length) await loadInstances()
    const res = await getPhysicalRestoreRunDetailApi(record.id)
    const run = res?.data?.run || {}
    resetFormData()
    formData.id = record.id
    formData.name = run.name || record.name || ''
    formData.backup_set_id = Number(run.backup_set_id || record.backup_set_id || 0) || null
    formData.target_instance_id = Number(run.target_instance_id || 0) || null
    formData.target_node_id = Number(run.target_node_id || 0) || null
    formData.target_data_dir = run.target_data_dir || ''
    formData.target_port = Number(run.target_port || 5433) || 5433
    formData.auto_start = run.auto_start !== false
    formData.restore_mode = run.restore_mode || 'NEW_INSTANCE'
    return
  }
  const res = await getRestoreRunDetailApi(record.id)
  const run = res?.data?.run || {}
  const options = parseJSONPayload(run.options, {})
  resetFormData()
  formData.id = record.id
  formData.name = run.name || record.name || ''
  formData.backup_set_id = Number(run.backup_set_id || record.backup_set_id || 0) || null
  formData.target_instance_id =
    Number(run.target_instance_id || record.target_instance_id || 0) || null
  formData.restore_mode = normalizeLogicalRestoreMode(run.restore_mode || options.restore_mode)
  formData.mode = normalizeLogicalExecutionMode(options.execution_mode)
  formData.target_db_name = options.target_db_name || run.target_db_name || ''
  formData.target_db_suffix = options.target_db_suffix || ''
  formData.selected_databases = normalizeStringArray(options.selected_databases)
  formData.selected_schemas = normalizeStringArray(options.selected_schemas)
  formData.selected_tables = normalizeStringArray(options.selected_tables)
  formData.parallel = Number(options.parallel || 4) || 4
  formData.timeout = String(options.timeout || '4h').trim() || '4h'
  formData.target_charset = String(options.target_charset || '').trim()
  formData.target_collation = String(options.target_collation || '').trim()
  formData.target_encoding = String(options.target_encoding || '').trim()
  formData.close_binlog = !!options.close_binlog
  formData.foreign_key_checks = !!options.foreign_key_checks
  formData.exit_on_error = options.exit_on_error !== false
  formData.no_owner = options.no_owner !== false
  formData.no_privileges = options.no_privileges !== false
  formData.clean_if_exists = !!options.clean_if_exists
  formData.is_cloud = !!options.is_cloud
  formData.execute_confirmed = !!options.execute_confirmed
}

const closeModal = () => {
  uiState.modalOpen = false
  uiState.modalMode = 'create'
  resetFormData()
  resetPrecheckState()
  clearRestoreEntryQuery()
}

const handlePhysicalPrecheck = async () => {
  if (selectedSetIsMySQL.value) {
    if (!formData.backup_set_id || !formData.target_instance_id) {
      message.warning('请先选择备份集与目标 MySQL 实例')
      return
    }
  } else if (
    !formData.backup_set_id ||
    !formData.target_node_id ||
    !String(formData.target_data_dir || '').trim() ||
    !formData.target_port
  ) {
    message.warning('请先完整填写物理恢复参数')
    return
  }
  uiState.precheckLoading = true
  try {
    const basePayload = {
      backup_set_id: formData.backup_set_id,
      restore_mode: selectedSetIsMySQL.value ? 'EXISTING_INSTANCE' : formData.restore_mode,
      auto_start: formData.auto_start,
    }
    const precheckPayload = selectedSetIsMySQL.value
      ? { ...basePayload, target_instance_id: formData.target_instance_id }
      : {
          ...basePayload,
          target_node_id: formData.target_node_id,
          target_data_dir: String(formData.target_data_dir || '').trim(),
          target_port: formData.target_port,
        }
    const res = await precheckPhysicalRestoreApi(precheckPayload)
    const payload = res?.data || {}
    precheckState.passed = !!payload.passed
    precheckState.summary = payload.summary || ''
    precheckState.items = payload.items || []
    precheckState.resolved = payload.resolved || null
    if (precheckState.passed) {
      message.success('预检查通过，可以提交恢复')
    } else {
      message.warning(precheckState.summary || '预检查未通过，请先处理失败项')
    }
  } finally {
    uiState.precheckLoading = false
  }
}

const handleSubmit = async () => {
  try {
    await formRef.value?.validate()
  } catch {
    return
  }
  const isPhysicalRun = isPhysicalRestoreSet.value
  uiState.modalLoading = true
  try {
    const logicalPayload = {
      name: formData.name,
      backup_set_id: formData.backup_set_id,
      target_instance_id: formData.target_instance_id,
      target_db_name: isPostgresLogicalMultiRestore.value
        ? ''
        : String(formData.target_db_name || '').trim(),
      target_db_suffix: isPostgresLogicalMultiRestore.value
        ? String(formData.target_db_suffix || '').trim()
        : '',
      mode: normalizeLogicalExecutionMode(formData.mode),
      restore_mode: normalizeLogicalRestoreMode(formData.restore_mode),
      selected_databases: normalizeStringArray(formData.selected_databases),
      selected_schemas: normalizeStringArray(formData.selected_schemas),
      selected_tables: normalizeStringArray(formData.selected_tables),
      parallel: Number(formData.parallel || 4) || 4,
      timeout: String(formData.timeout || '4h').trim() || '4h',
      target_charset: String(formData.target_charset || '').trim(),
      target_collation: String(formData.target_collation || '').trim(),
      target_encoding: String(formData.target_encoding || '').trim(),
      close_binlog: !!formData.close_binlog,
      foreign_key_checks: !!formData.foreign_key_checks,
      exit_on_error: !!formData.exit_on_error,
      no_owner: !!formData.no_owner,
      no_privileges: !!formData.no_privileges,
      clean_if_exists: !!formData.clean_if_exists,
      is_cloud: !!formData.is_cloud,
      execute_confirmed: !!formData.execute_confirmed,
    }
    if (uiState.modalMode === 'edit' && formData.id) {
      const editedID = formData.id
      if (isPhysicalRun) {
        await updatePhysicalRestoreRunApi({
          id: editedID,
          name: formData.name,
          backup_set_id: formData.backup_set_id,
          restore_mode: formData.restore_mode,
          target_instance_id: formData.target_instance_id || 0,
          target_node_id: formData.target_node_id,
          target_data_dir: String(formData.target_data_dir || '').trim(),
          target_port: formData.target_port,
          auto_start: formData.auto_start,
        })
      } else {
        await updateRestoreRunApi({
          id: editedID,
          ...logicalPayload,
        })
      }
      message.success('恢复任务已更新')
      uiState.modalOpen = false
      uiState.modalMode = 'create'
      resetFormData()
      await clearRestoreEntryQuery()
      await refreshAll()
      if (isPhysicalRun) {
        await openPhysicalDetail(editedID)
      } else {
        await openDetailRoute(editedID)
      }
      return
    }
    if (isPhysicalRun) {
      const basePayload = {
        name: formData.name,
        backup_set_id: formData.backup_set_id,
        restore_mode: selectedSetIsMySQL.value ? 'EXISTING_INSTANCE' : formData.restore_mode,
        auto_start: formData.auto_start,
        confirm_text: 'RECOVER',
      }
      const physicalPayload = selectedSetIsMySQL.value
        ? { ...basePayload, target_instance_id: formData.target_instance_id }
        : {
            ...basePayload,
            target_node_id: formData.target_node_id,
            target_data_dir: String(formData.target_data_dir || '').trim(),
            target_port: formData.target_port,
          }
      const res = await createPhysicalRestoreRunApi(physicalPayload)
      message.success('已提交物理恢复任务')
      uiState.modalOpen = false
      resetFormData()
      resetPrecheckState()
      await clearRestoreEntryQuery()
      const runID = res?.data?.id || res?.id || null
      await refreshAll()
      if (runID) {
        await openPhysicalDetail(runID)
      }
      return
    }
    const res = await createRestoreRunApi({
      ...logicalPayload,
    })
    message.success(
      logicalPayload.mode === 'execute' ? '恢复任务已创建并进入执行队列' : '恢复计划已创建',
    )
    uiState.modalOpen = false
    resetFormData()
    await clearRestoreEntryQuery()
    await refreshAll()
    const restoreID = res?.data?.id || res?.data?.ID || null
    if (restoreID) {
      await openDetailRoute(restoreID)
    }
  } finally {
    uiState.modalLoading = false
  }
}

const handleSearch = () => {
  query.page = 1
}

const handleFilterChange = () => {
  query.page = 1
}

const resetDateFilter = () => {
  query.dateRange = []
  query.page = 1
}

const handleTableChange = (pager) => {
  query.page = pager.current
  query.page_size = pager.pageSize
}

const refreshAll = async () => {
  await Promise.allSettled([fetchRestores(), fetchPhysicalRestores()])
}

// 跳独立监控页：阶段时间轴 + 对象级进度 + 实时日志 + 结果摘要，
// 比弹窗更适长时间执行的恢复任务（弹窗会被列表刷新打断）
const openMonitorPage = async (record) => {
  if (!record?.id) return
  await router.push({
    path: `/backup/restore/${record.id}`,
    query: { kind: record.run_kind === 'physical' ? 'physical' : 'logical' },
  })
}

const openRecordDetail = async (record) => {
  if (!record?.id) return
  if (record.run_kind === 'physical') {
    const nextQuery = { ...route.query }
    delete nextQuery.detail_id
    await router.push({
      path: route.path,
      query: {
        ...nextQuery,
        run_id: String(record.id),
      },
    })
    return
  }
  await openDetailRoute(record.id)
}

const handleStop = (record) => {
  if (!record?.id || !canStopRestore(record)) return
  const forcing = String(record?.status || '').toUpperCase() === 'STOP_REQUESTED'
  Modal.confirm({
    title: forcing ? '强制终止恢复任务' : '终止恢复任务',
    content: forcing
      ? '该任务已下发过终止请求但迟迟没有响应。强制终止会直接把任务置为“已终止”，用于解除终止不了、也无法删除或重跑的卡死状态。是否继续？'
      : '终止后会中断当前恢复进程，未完成任务将以“已终止”结束。是否继续？',
    okText: forcing ? '强制终止' : '确认终止',
    okType: 'danger',
    cancelText: '取消',
    async onOk() {
      const res =
        record.run_kind === 'physical'
          ? await stopPhysicalRestoreRunApi(record.id)
          : await stopRestoreRunApi(record.id)
      message.success(res?.data?.stop_state_message || '已发送终止请求')
      await refreshAll()
      if (record.run_kind === 'physical') {
        if (physicalDetailState.open && physicalDetailState.id === record.id) {
          await openPhysicalDetail(record.id, false)
        }
        return
      }
      if (detailState.open && detailState.runID === record.id) {
        await fillDetailState(record.id)
      }
      if (logState.open && logState.runID === record.id) {
        await fillLogState(record.id, logState.itemID, true)
      }
    },
  })
}

const handleDelete = (record) => {
  if (!record?.id) return
  Modal.confirm({
    title: '删除恢复任务',
    content: '删除后仅移除该任务记录，不会删除已有备份集和目标实例数据。是否继续？',
    okText: '确认删除',
    okType: 'danger',
    cancelText: '取消',
    async onOk() {
      if (record.run_kind === 'physical') {
        await deletePhysicalRestoreRunApi(record.id)
      } else {
        await deleteRestoreRunApi(record.id)
      }
      message.success('恢复任务已删除')
      if (record.run_kind === 'physical') {
        if (physicalDetailState.open && physicalDetailState.id === record.id) {
          closePhysicalDetail()
        }
      } else if (detailState.open && detailState.runID === record.id) {
        await closeDetail()
      }
      await refreshAll()
    },
  })
}

// ---------------- 批量删除（交互与【数据备份】页保持一致） ----------------
const selectedRowKeys = ref([])
const selectedRows = ref([])
const batchDeleteUi = reactive({
  open: false,
  loading: false,
  confirmed: false,
})

const clearSelection = () => {
  selectedRowKeys.value = []
  selectedRows.value = []
}

const rowSelection = computed(() => ({
  selectedRowKeys: selectedRowKeys.value,
  columnWidth: 44,
  onChange: (keys, rows) => {
    selectedRowKeys.value = keys
    selectedRows.value = rows
  },
}))

const openBatchDeleteModal = () => {
  if (!selectedRowKeys.value.length) {
    message.warning('请先勾选需要删除的恢复任务')
    return
  }
  batchDeleteUi.open = true
  batchDeleteUi.loading = false
  batchDeleteUi.confirmed = false
}

const closeBatchDeleteModal = () => {
  batchDeleteUi.open = false
  batchDeleteUi.loading = false
  batchDeleteUi.confirmed = false
}

const showBatchDeleteFailures = (failures) => {
  Modal.error({
    title: '部分恢复任务删除失败',
    width: 560,
    okText: '我知道了',
    content: () =>
      h(
        'div',
        { class: 'batch-delete-failure-list' },
        failures.map((text) => h('div', { class: 'batch-delete-failure-item' }, text)),
      ),
  })
}

const handleBatchDelete = async () => {
  if (!batchDeleteUi.confirmed) {
    message.warning('请先勾选删除确认项')
    return
  }
  const rows = [...selectedRows.value]
  if (!rows.length) {
    closeBatchDeleteModal()
    return
  }
  batchDeleteUi.loading = true
  const failures = []
  let successCount = 0
  for (const record of rows) {
    try {
      // 与单条删除一致：按运行类型（物理/逻辑）调用对应接口
      if (record.run_kind === 'physical') {
        await deletePhysicalRestoreRunApi(record.id)
      } else {
        await deleteRestoreRunApi(record.id)
      }
      successCount += 1
    } catch (error) {
      const reason = error?.msg || error?.message || '删除失败'
      failures.push(`${record.name || '未命名任务'}（${record.id}）：${reason}`)
    }
  }
  batchDeleteUi.loading = false
  closeBatchDeleteModal()
  clearSelection()
  await refreshAll()
  if (!failures.length) {
    message.success(`批量删除成功，共删除 ${successCount} 个恢复任务`)
    return
  }
  if (successCount === 0) {
    message.error(`批量删除失败，${failures.length} 个恢复任务均未删除`)
  } else {
    message.warning(`删除完成：成功 ${successCount} 个，失败 ${failures.length} 个`)
  }
  showBatchDeleteFailures(failures)
}

const handleCopyTaskId = async (taskId) => {
  const text = String(taskId || '').trim()
  if (!text) return
  await copyText(text, '任务ID已复制')
}

const shouldAutoRefresh = computed(
  () =>
    tableData.value.some((item) =>
      ACTIVE_STATUSES.includes(String(item?.status || '').toUpperCase()),
    ) ||
    physicalTableData.value.some((item) =>
      ACTIVE_STATUSES.includes(String(item?.status || '').toUpperCase()),
    ) ||
    ACTIVE_STATUSES.includes(String(detailState.run?.status || '').toUpperCase()) ||
    ACTIVE_STATUSES.includes(String(logState.run?.status || '').toUpperCase()) ||
    ACTIVE_STATUSES.includes(String(physicalDetailState.run?.status || '').toUpperCase()),
)

const setupRefreshTimer = () => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
  refreshTimer = setInterval(async () => {
    if (!shouldAutoRefresh.value) return
    await Promise.allSettled([fetchRestores(), fetchPhysicalRestores()])
    if (detailState.open && detailState.runID) {
      await fillDetailState(detailState.runID)
    }
    if (logState.open && logState.runID) {
      await fillLogState(logState.runID, logState.itemID, true)
    }
    if (physicalDetailState.open && physicalDetailState.id) {
      await openPhysicalDetail(physicalDetailState.id, false)
    }
  }, 5000)
}

const syncRestoreEntryFromRoute = async () => {
  const backupSetID = Number(route.query.backup_set_id || 0)
  const shouldOpen = route.query.open === '1' || backupSetID > 0
  const routeTab = String(route.query.tab || '').trim().toLowerCase()
  if (routeTab === 'physical' || routeTab === 'logical') {
    query.restore_type = routeTab
  }
  if (!shouldOpen || !backupSetID || routeRestoreSyncing || uiState.modalOpen) return
  routeRestoreSyncing = true
  try {
    await router.replace({ path: route.path, query: {} })
    await openCreate({ backup_set_id: backupSetID })
  } finally {
    routeRestoreSyncing = false
  }
}

const syncRestoreDetailFromRoute = async () => {
  const detailID = Number(route.query.detail_id || 0)
  if (!detailID) return
  if (detailState.open && detailState.runID === detailID) return
  await openDetail(detailID)
}

const syncPhysicalDetailFromRoute = async () => {
  const runID = Number(route.query.run_id || 0)
  if (!runID) return
  if (physicalDetailState.open && physicalDetailState.id === runID) return
  await openPhysicalDetail(runID)
}

const fetchPhysicalDetailLog = async () => {
  if (!physicalDetailState.id) return
  const res = await getPhysicalRestoreRunLogApi(physicalDetailState.id, { tail_kb: 128 })
  const payload = res?.data || {}
  physicalDetailState.logContent = payload.content || ''
  physicalDetailState.logMeta = {
    log_path: payload.log_path || '',
    read_mode: payload.read_mode || '-',
  }
}

const openPhysicalDetail = async (id, openDrawer = true) => {
  if (!id) return
  physicalDetailState.id = Number(id)
  if (openDrawer) {
    physicalDetailState.open = true
  }
  const res = await getPhysicalRestoreRunDetailApi(physicalDetailState.id)
  physicalDetailState.run = res?.data?.run || null
  await fetchPhysicalDetailLog()
}

const closePhysicalDetail = () => {
  physicalDetailState.open = false
  physicalDetailState.id = null
  physicalDetailState.run = null
  physicalDetailState.logContent = ''
  physicalDetailState.logMeta = {}
  if (route.query.run_id) {
    const nextQuery = { ...route.query }
    delete nextQuery.run_id
    router.replace({ path: route.path, query: nextQuery })
  }
}

const TABLE_BODY_MIN_HEIGHT = 260
const TABLE_BODY_MAX_HEIGHT_DEFAULT = 520

let updateScrollRafId = 0

const updateTableScrollY = () => {
  if (!tableWrapRef.value) return
  if (updateScrollRafId) {
    window.cancelAnimationFrame(updateScrollRafId)
    updateScrollRafId = 0
  }
  updateScrollRafId = window.requestAnimationFrame(() => {
    updateScrollRafId = 0
    if (!tableWrapRef.value) return
    const wrapEl = tableWrapRef.value
    const wrapH = wrapEl.clientHeight || 0
    const headerEl = wrapEl.querySelector(':scope .ant-spin-container .ant-table-thead')
    const paginationEl = wrapEl.querySelector(':scope .ant-spin-container .ant-pagination')
    let headerH = 48
    let paginationH = 56
    if (headerEl) {
      const rect = headerEl.getBoundingClientRect?.()
      headerH = rect && rect.height > 0 ? rect.height : headerEl.offsetHeight || 48
    }
    if (paginationEl) {
      const rect = paginationEl.getBoundingClientRect?.()
      paginationH = rect && rect.height > 0 ? rect.height : paginationEl.offsetHeight || 56
    }
    const overhead = headerH + paginationH + 16
    let nextHeight = TABLE_BODY_MAX_HEIGHT_DEFAULT
    if (wrapH > overhead + TABLE_BODY_MIN_HEIGHT) {
      nextHeight = Math.floor(wrapH - overhead)
    }
    tableScrollY.value = Math.max(TABLE_BODY_MIN_HEIGHT, nextHeight)
  })
}

const setupResizeObserver = () => {
  if (typeof ResizeObserver === 'undefined' || !tableWrapRef.value) return
  if (resizeObserver) return
  resizeObserver = new ResizeObserver(() => {
    nextTick(() => updateTableScrollY())
  })
  resizeObserver.observe(tableWrapRef.value)
}

onMounted(async () => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(true)
  }
  await refreshAll()
  await syncRestoreDetailFromRoute()
  await syncPhysicalDetailFromRoute()
  await nextTick()
  updateTableScrollY()
  setupResizeObserver()
  if (typeof window !== 'undefined') {
    window.addEventListener('resize', updateTableScrollY)
  }
  setupRefreshTimer()
})

onBeforeUnmount(() => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(false)
  }
  if (resizeObserver) {
    resizeObserver.disconnect()
    resizeObserver = null
  }
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
  if (typeof window !== 'undefined') {
    window.removeEventListener('resize', updateTableScrollY)
  }
})

watch(
  () => route.fullPath,
  () => {
    syncRestoreEntryFromRoute()
    syncRestoreDetailFromRoute()
    syncPhysicalDetailFromRoute()
  },
  { immediate: true },
)

watch(
  () => logState.content,
  async () => {
    await nextTick()
    syncLogScrollToBottom()
  },
)

watch(
  () => filteredRows.value.length,
  (length) => {
    const maxPage = Math.max(1, Math.ceil(length / Math.max(1, Number(query.page_size || 10))))
    if (query.page > maxPage) {
      query.page = maxPage
    }
  },
)

watch(
  () => [
    formData.backup_set_id,
    formData.target_node_id,
    formData.target_data_dir,
    formData.target_port,
    formData.auto_start,
    formData.restore_mode,
  ],
  ([backupSetID], [prevBackupSetID]) => {
    if (!uiState.modalOpen) return
    const currentSet = sets.value.find((item) => item.id === backupSetID)
    const previousSet = sets.value.find((item) => item.id === prevBackupSetID)
    const isPhysicalSet = (set) => {
      if (!set) return false
      const driverType = String(set.driver_type || '').toUpperCase()
      return (
        (set.db_type === 'PostgreSQL' && driverType === 'PG_BACKREST') ||
        (set.db_type === 'MySQL' && driverType === 'XTRABACKUP')
      )
    }
    const currentPhysical = isPhysicalSet(currentSet)
    const previousPhysical = isPhysicalSet(previousSet)
    if (currentPhysical || previousPhysical) {
      resetPrecheckState()
    }
  },
)
</script>

<style scoped>
.backup-restore-page {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  height: 100%;
  min-height: 0;
  box-sizing: border-box;
  gap: 12px;
}

.backup-restore-page > .restore-toolbar {
  flex-shrink: 0;
}

.backup-restore-page > .restore-table-card {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  min-height: 0;
  max-width: 100%;
}

.restore-table-card :deep(.ant-card-body) {
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  min-height: 0;
  padding: 12px 16px 8px 16px;
  box-sizing: border-box;
}

.restore-table-card :deep(.ant-card-body::before),
.restore-table-card :deep(.ant-card-body::after) {
  display: none;
}

.restore-toolbar {
  display: flex;
  justify-content: space-between;
  gap: var(--gi-spacing-sm);
  /* 与【数据备份】【备份集】两页一致：标题与右侧控件垂直居中对齐，
     标题到下方卡片的间距统一由页面 shell 的 gap(--gi-spacing-ssm) 决定，此处不再额外补偿 */
  align-items: center;
  background: var(--gi-color-page-bg);
}

.toolbar-left,
.toolbar-right {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
}

.toolbar-left {
  align-items: flex-start;
  flex-direction: column;
  gap: 4px;
}

/* 页面主标题：统一使用公共样式 .gi-page-title，不再重复定义。 */

.search-input {
  width: 320px;
}

.restore-table-card {
  border-radius: 16px;
  border: 1px solid rgba(27, 73, 88, 0.08);
  box-shadow: 0 10px 24px rgba(18, 58, 72, 0.04);
}

.restore-primary-btn {
  border-color: var(--gi-color-primary, #0f766e);
  background: var(--gi-color-primary, #0f766e);
}

/* 选择提示条：样式参数与【数据备份】页 .backup-task-selection-bar 完全一致 */
.restore-selection-bar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  padding: 6px 12px;
  border-radius: 8px;
  background: rgba(15, 118, 110, 0.06);
  border: 1px solid rgba(15, 118, 110, 0.16);
  font-size: 13px;
  color: #24494a;
}

.restore-selection-text strong {
  color: var(--gi-color-primary, #0f766e);
  font-size: 14px;
  padding: 0 2px;
}

.restore-table :deep(.ant-table-tbody > tr.ant-table-row-selected > td) {
  background: rgba(15, 118, 110, 0.04);
}

/* 批量删除弹窗：清单为弹窗首个元素，顶部不留额外空隙，仅保留下方间距 */
.batch-delete-list {
  margin: 0 0 12px;
}

.batch-delete-list-title {
  margin-bottom: 6px;
  font-size: 13px;
  color: #4e5969;
}

.batch-delete-list-body {
  margin: 0;
  padding: 8px 12px 8px 26px;
  max-height: 220px;
  overflow-y: auto;
  list-style: disc;
  border: 1px solid #eef1f4;
  border-radius: 8px;
  background: #fafcff;
}

.batch-delete-list-body li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-size: 13px;
  line-height: 1.9;
  color: #1f2d38;
}

.batch-delete-list-body .batch-delete-item-id {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  color: #9aa6ae;
}

.batch-delete-failure-item {
  margin-bottom: 6px;
  word-break: break-all;
  font-size: 13px;
}

.restore-table-wrap {
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  align-self: stretch;
  box-sizing: border-box;
}

.restore-table-wrap > :deep(.ant-spin-nested-loading),
.restore-table-wrap > :deep(.ant-spin-container) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  width: 100%;
}

.restore-table-wrap > :deep(.ant-spin-container) > :deep(.ant-table) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  width: 100%;
}

.restore-table-wrap > :deep(.ant-spin-container) > :deep(.ant-table) > :deep(.ant-table-container) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.restore-table-wrap
  > :deep(.ant-spin-container)
  > :deep(.ant-table)
  > :deep(.ant-table-container)
  > :deep(.ant-table-body) {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
}

.restore-table-wrap > :deep(.ant-spin-container) > :deep(.ant-table) > :deep(.ant-pagination) {
  flex-shrink: 0;
  margin: 4px 0 0 0;
  padding-inline-end: 4px;
}

.restore-table-wrap
  > :deep(.ant-spin-container)
  > :deep(.ant-table)
  > :deep(.ant-pagination)
  .ant-pagination-item {
  min-width: 32px;
  height: 32px;
  line-height: 30px;
  border-radius: 6px;
  border: 1px solid #d9e2ea;
  background: #fff;
  font-size: 14px;
}

.restore-table-wrap
  > :deep(.ant-spin-container)
  > :deep(.ant-table)
  > :deep(.ant-pagination)
  .ant-pagination-item-active {
  border-color: var(--gi-color-primary, #0f766e);
  color: var(--gi-color-primary, #0f766e);
  font-weight: 500;
}

.restore-table-wrap
  > :deep(.ant-spin-container)
  > :deep(.ant-table)
  > :deep(.ant-pagination)
  .ant-pagination-options-size-changer.ant-select {
  min-width: 96px;
}

.restore-table-wrap
  > :deep(.ant-spin-container)
  > :deep(.ant-table)
  > :deep(.ant-pagination)
  .ant-pagination-options-size-changer.ant-select
  .ant-select-selector {
  height: 32px !important;
  min-height: 32px;
  line-height: 30px;
  padding: 0 28px 0 11px !important;
  border: 1px solid #d9e2ea;
  border-radius: 6px;
  background: #fff;
  font-size: 14px;
  color: rgba(0, 0, 0, 0.88);
  box-shadow: none;
  transition:
    border-color 0.2s,
    box-shadow 0.2s;
}

.restore-table-wrap
  > :deep(.ant-spin-container)
  > :deep(.ant-table)
  > :deep(.ant-pagination)
  .ant-pagination-options-size-changer.ant-select
  .ant-select-selector:hover {
  border-color: var(--gi-color-primary, #0f766e);
  box-shadow: 0 0 0 2px rgba(15, 118, 110, 0.1);
}

.restore-table-wrap
  > :deep(.ant-spin-container)
  > :deep(.ant-table)
  > :deep(.ant-pagination)
  .ant-pagination-options-size-changer.ant-select.ant-select-focused
  .ant-select-selector,
.restore-table-wrap
  > :deep(.ant-spin-container)
  > :deep(.ant-table)
  > :deep(.ant-pagination)
  .ant-pagination-options-size-changer.ant-select.ant-select-open
  .ant-select-selector {
  border-color: var(--gi-color-primary, #0f766e);
  box-shadow: 0 0 0 2px rgba(15, 118, 110, 0.18);
}

.restore-table-wrap
  > :deep(.ant-spin-container)
  > :deep(.ant-table)
  > :deep(.ant-pagination)
  .ant-pagination-options-size-changer.ant-select
  .ant-select-selection-item {
  line-height: 30px;
  padding-inline-end: 0;
  font-size: 14px;
  font-weight: 500;
}

.restore-table-wrap
  > :deep(.ant-spin-container)
  > :deep(.ant-table)
  > :deep(.ant-pagination)
  .ant-pagination-options-size-changer.ant-select
  .ant-select-arrow {
  color: rgba(0, 0, 0, 0.45);
  right: 10px;
  top: 50%;
  transform: translateY(-50%);
}

.restore-table-wrap
  > :deep(.ant-spin-container)
  > :deep(.ant-table)
  > :deep(.ant-pagination)
  .ant-pagination-total-text {
  height: 32px;
  line-height: 32px;
  font-size: 14px;
  color: rgba(0, 0, 0, 0.72);
  margin-inline-end: 4px;
}

.restore-table :deep(.ant-table-thead > tr > th) {
  background: #f8fbfd;
  border-bottom: 1px solid #e7edf1;
  color: #38505d;
  font-weight: 600;
}

.restore-table :deep(.ant-table-tbody > tr:hover > td) {
  background: #f7fbff;
}

.restore-table :deep(.restore-row-failed > td) {
  background: #fff6f6;
}

.restore-table :deep(.restore-row-stopped > td) {
  background: #fafafa;
}

.cell-primary {
  font-size: 13px;
  color: #17313d;
}

.cell-secondary {
  margin-top: 4px;
  font-size: 12px;
  color: #7e8f99;
}

/* 任务ID行：与【数据备份】页 BackupTask.vue 的 .task-id-row / .task-id-link 保持一致。
   注意不要用 a-button：antd 按钮继承全局 controlHeight=40px，会把第一行文字撑到 40px 高，
   与第二行任务名称之间出现明显空隙。 */
.task-id-row {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  min-width: 0;
}

.task-id-link {
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
  padding: 0;
  border: none;
  background: transparent;
  font-size: 14px;
  font-weight: 600;
  color: var(--gi-color-primary, #0f766e);
  letter-spacing: 0.4px;
  cursor: pointer;
  text-align: left;
  max-width: 180px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-id-link:hover {
  color: var(--gi-color-primary-hover, #0d6b63);
  text-decoration: underline;
}

.task-id-copy-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: none;
  background: transparent;
  color: #b6c0c8;
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
  flex: 0 0 auto;
  transition: color 0.2s ease;
}

.task-id-copy-btn:hover {
  color: #71818c;
}

.task-id-cell {
  min-width: 0;
}

.ellipsis-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 任务名称行:样式与【数据备份】页 task-name-row / task-name-edit-btn 保持一致 */
.restore-name-row {
  margin-top: 4px;
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  max-width: 100%;
}

.restore-name-text {
  color: #7e8f99;
  font-size: 12px;
  line-height: 1.5;
  flex: 0 1 auto;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.restore-name-edit-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: none;
  background: transparent;
  color: #b6c0c8;
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
  flex: 0 0 auto;
  transition: color 0.2s ease;
}

.restore-name-edit-btn:hover {
  color: var(--gi-color-primary, #0f766e);
}

.restore-name-input {
  flex: 1 1 auto;
  min-width: 0;
}

.restore-name-input :deep(.ant-input) {
  font-size: 12px;
  padding: 2px 6px;
  line-height: 1.4;
  min-height: 22px;
}

.table-header-filter {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.header-filter-panel {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.header-filter-actions {
  display: flex;
  justify-content: flex-end;
}

.header-filter-icon {
  color: #8ca0ad;
  cursor: pointer;
}

.header-filter-icon.active {
  color: var(--gi-color-primary, #0f766e);
}

.data-source-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.data-source-text {
  min-width: 0;
}

/* 源/目标数据源的「实例名称」跟随本页表体常规文字样式（即 .cell-primary：13px / #17313d），
   不再使用 DataSourceName 组件默认的小号淡灰；名称下方的 IP 地址仍由 .cell-secondary
   呈现为小一号的次要文字，样式不变。 */
.restore-table :deep(.gi-data-source-name) {
  font-size: inherit;
  color: inherit;
}

.db-logo {
  /* 全站统一：数据库类型 logo 一律 20px */
  width: 20px;
  height: 20px;
  flex: none;
  object-fit: contain;
}

.data-source-icon {
  flex: none;
  color: #7e8f99;
}

/* 操作列内容水平居中（flex 容器需显式居中，避免居右） */
.action-group {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 2px;
}

.more-action-btn {
  display: inline-flex;
  align-items: center;
  padding-inline: 4px;
}

.restore-table :deep(.ant-btn-link) {
  color: var(--gi-color-primary, #0f766e);
}

/* 【更多】按钮字体样式与【数据备份】页操作列保持一致 */
.restore-table :deep(.more-action-btn) {
  color: #667784;
  font-size: 12px;
  font-weight: 400;
}

.restore-table :deep(.more-action-btn:hover),
.restore-table :deep(.more-action-btn:focus) {
  color: #1f2d38;
}

.restore-table :deep(.ant-btn-link.ant-btn-disabled) {
  color: rgba(0, 0, 0, 0.25);
}

.status-pill,
.status-tag {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 72px;
  height: 24px;
  padding: 0 8px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
}

/* 状态列单元格：完整对齐【备份集】页状态列样式（胶囊几何 + 文字样式 + 对应状态配色） */
.restore-table :deep(.status-tag) {
  justify-content: center;
  min-width: 68px;
  height: auto;
  padding: 2px 10px;
  border: 1px solid transparent;
  font-size: 12px;
  font-weight: 400;
  line-height: 20px;
}

/* 合并状态列：上方进度条 + 下方状态文字；进度条通栏铺满，状态文字保持居中 */
.restore-table :deep(.status-progress-cell) {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.restore-table :deep(.status-progress-cell .ant-progress) {
  margin: 0;
  font-size: 12px;
  line-height: 1;
}

/* 进度百分比文字与表格次要文字同款式（12px 淡灰） */
.restore-table :deep(.status-progress-cell .ant-progress-text) {
  color: #7e8f99;
  font-size: 12px;
  line-height: 1;
}

.restore-table :deep(.status-progress-cell .status-tag) {
  align-self: center;
}

/* 与【备份集】页 running / completed / failed 三种状态配色保持一致 */
.restore-table :deep(.status-tag.status-running) {
  color: #0f766e;
  background: rgba(15, 118, 110, 0.08);
  border-color: rgba(15, 118, 110, 0.2);
}

.restore-table :deep(.status-tag.status-success) {
  color: #166534;
  background: rgba(22, 101, 52, 0.08);
  border-color: rgba(22, 101, 52, 0.18);
}

.restore-table :deep(.status-tag.status-failed) {
  color: #b42318;
  background: rgba(180, 35, 24, 0.08);
  border-color: rgba(180, 35, 24, 0.18);
}

/* 【备份集】页没有对应状态（等待中 / 计划 / 已终止 / 未知）：沿用中性灰，几何与文字样式保持一致 */
.restore-table :deep(.status-tag.status-pending),
.restore-table :deep(.status-tag.status-plan),
.restore-table :deep(.status-tag.status-stopped),
.restore-table :deep(.status-tag.status-default) {
  color: #5b6b76;
  background: rgba(91, 107, 118, 0.08);
  border-color: rgba(91, 107, 118, 0.18);
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.status-success {
  color: #167a45;
  background: #eaf8ee;
}

.status-running {
  color: #b26a17;
  background: #fff5e8;
}

.status-pending {
  color: #657786;
  background: #f1f4f6;
}

.status-plan {
  color: #2a5db0;
  background: #e8f1ff;
}

.status-failed {
  color: #cc3d3d;
  background: #fff0f0;
}

.status-stopped {
  color: #66717a;
  background: #f1f3f5;
}

.status-default {
  color: #526572;
  background: #f5f7f9;
}

.execute-plan-btn {
  color: #1677ff;
  font-weight: 600;
}

.execute-plan-alert {
  margin-bottom: 16px;
}

.execute-plan-summary {
  margin-bottom: 16px;
}

.execute-plan-form {
  margin-top: 4px;
}

.advanced-panel {
  width: 100%;
  padding: 10px 12px;
  border: 1px solid #e7edf1;
  border-radius: 10px;
  background: #fafcfd;
  color: #60717c;
  line-height: 1.8;
}

.logical-advanced-panel,
.logical-confirm-panel {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 12px;
  border: 1px solid #e7edf1;
  border-radius: 12px;
  background: #fafcfd;
}

.logical-advanced-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.advanced-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.advanced-label {
  font-size: 12px;
  color: #60717c;
}

.logical-switch-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}

.detail-metrics {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}

.metric-card {
  padding: 14px 16px;
  border: 1px solid #e7edf1;
  border-radius: 12px;
  background: #fbfdff;
}

.metric-label {
  font-size: 12px;
  color: #7c8c95;
}

.metric-value {
  margin-top: 8px;
  font-size: 18px;
  font-weight: 700;
  color: #17313d;
}

.detail-text {
  font-size: 14px;
  font-weight: 600;
}

.detail-card {
  margin-bottom: 16px;
}

.detail-precheck-summary {
  margin-bottom: 12px;
}

.detail-subsection + .detail-subsection,
.detail-card .detail-subsection + .detail-subsection {
  margin-top: 12px;
}

.json-preview {
  margin: 0;
  padding: 12px 14px;
  border-radius: 10px;
  background: #0f1720;
  color: #d7e4ef;
  font-size: 12px;
  line-height: 1.6;
  font-family: Consolas, Monaco, monospace;
  white-space: pre-wrap;
  word-break: break-word;
}

.detail-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}

.detail-progress {
  margin-top: 14px;
}

.detail-summary {
  margin-bottom: 12px;
  font-size: 12px;
  color: #6f828d;
}

.detail-error {
  color: #c53b3b;
  white-space: pre-wrap;
}

.resolved-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin: 12px 0;
}

.resolved-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px;
  border-radius: 10px;
  background: #fafcfd;
  border: 1px solid #e7edf1;
}

.resolved-label {
  font-size: 12px;
  color: #7d8b94;
}

.resolved-value {
  color: #17313d;
  word-break: break-all;
}

.log-summary-card {
  padding: 14px 16px;
  border: 1px solid #e7edf1;
  border-radius: 12px;
  background: #fbfdff;
}

.log-summary-line {
  display: flex;
  gap: 16px;
  flex-wrap: wrap;
  color: #526572;
}

.log-summary-line + .log-summary-line {
  margin-top: 8px;
}

.drawer-toolbar {
  margin-top: 16px;
}

.drawer-meta {
  display: flex;
  gap: 16px;
  flex-wrap: wrap;
  margin: 12px 0;
  font-size: 12px;
  color: #7d8b94;
}

.log-content {
  min-height: 420px;
  max-height: calc(100vh - 280px);
  overflow: auto;
  border-radius: 12px;
  background: #101922;
  border: 1px solid #1e2c38;
}

.log-content-inner {
  margin: 0;
  padding: 16px;
  color: #d9e5ef;
  font-size: 12px;
  line-height: 1.6;
  font-family: Consolas, Monaco, monospace;
  white-space: pre-wrap;
  word-break: break-word;
}

.physical-log-content {
  min-height: 320px;
}

.chain-preview-card {
  padding: 14px 16px 16px;
  border-radius: 12px;
  background: linear-gradient(135deg, #f0f9ff 0%, #fff 60%, #f8fbff 100%);
  border: 1px solid #d8e8ff;
}

.chain-preview-card.is-orphan {
  background: linear-gradient(135deg, #fff8f0 0%, #fff 60%, #fef7f0 100%);
  border-color: #ffd9b3;
}

.chain-preview-title {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  margin-bottom: 12px;
  font-size: 13px;
  font-weight: 600;
  color: #1f2d38;
}

.chain-preview-total {
  margin-left: auto;
  font-size: 12px;
  font-weight: 500;
  color: #485b68;
}

.chain-preview-alert {
  margin-bottom: 12px;
}

.chain-preview-flow {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.chain-preview-node {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  padding: 10px 12px;
  border-radius: 10px;
  background: #fff;
  border: 1px solid #e6eef8;
}

.chain-preview-node.is-full {
  border-color: #d9f3c0;
  background: linear-gradient(135deg, #f6ffed, #fff 72%);
}

.chain-preview-node.is-diff {
  border-color: #ffe0b3;
  background: linear-gradient(135deg, #fff7e6, #fff 72%);
}

.chain-preview-node.is-incr {
  border-color: #cfe2ff;
  background: linear-gradient(135deg, #eaf4ff, #fff 72%);
}

.chain-preview-node.is-selected {
  border-color: #91caff;
  box-shadow: 0 0 0 3px rgba(22, 119, 255, 0.1);
}

.chain-preview-name {
  flex: 1 1 auto;
  min-width: 180px;
  font-size: 13px;
  font-weight: 500;
  color: #1f2d38;
  word-break: break-all;
}

.chain-preview-meta {
  font-size: 12px;
  color: #71818c;
}

.chain-preview-arrow {
  align-self: flex-start;
  padding: 0 0 0 18px;
  font-size: 12px;
  color: #8a98a4;
}

@media (max-width: 1200px) {
  .detail-metrics {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .detail-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 768px) {
  /* 窄屏工具栏转纵向，此时上下两组需要更大的行间距（横向时的 8px 不适用） */
  .restore-toolbar {
    gap: var(--gi-spacing-md);
  }

  /* 窄屏下标题组保持与其它两页相同的 40px 行高并垂直居中，
     避免标题顶部比【数据备份】【备份集】高出 8px */
  .toolbar-left {
    min-height: 40px;
    justify-content: center;
  }

  .restore-toolbar,
  .toolbar-right {
    align-items: stretch;
    flex-direction: column;
  }

  .search-input {
    width: 100%;
  }

  .detail-metrics {
    grid-template-columns: 1fr;
  }

  .detail-grid {
    grid-template-columns: 1fr;
  }

  .logical-advanced-grid,
  .logical-switch-grid,
  .resolved-grid {
    grid-template-columns: 1fr;
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

/* 恢复任务详情弹窗：
   ① wrap 用 flex 做水平+垂直居中，并禁止自身滚动（滚动只发生在弹窗 body 内，页面/父容器都不滚）；
   ② 弹窗本体定最大高度，内容超长时由 .ant-modal-body 内部滚动，弹窗始终完整可见不裁切。
   注意：Modal 默认 Teleport 到 body，挂不到本组件的 [data-v-*] 作用域上，
   这里的 :deep() 会编译成 `[data-v-x] .restore-run-detail-modal …` 而永远匹配不上（弹窗壳样式整段失效），
   因此必须用 :global() 让选择器落到全局。 */
:global(.restore-run-detail-modal) {
  display: flex !important;
  align-items: center;
  justify-content: center;
  overflow: hidden !important;
}

:global(.restore-run-detail-modal .ant-modal) {
  top: 0 !important;
  margin: 0 !important;
  padding-bottom: 0 !important;
  max-width: calc(100vw - 48px) !important;
  max-height: calc(100vh - 48px) !important;
  display: flex !important;
  flex-direction: column !important;
}

/* antd 用的是 CSS-in-JS，运行时注入的样式可能晚于本文件，同权重下会盖掉这里的规则，
   所以这几个关键属性一律加 !important。
   关键：最大高度必须落在 .ant-modal-content 上（它是 body 的 flex 容器），
   只写在 .ant-modal 上时 content 不会收缩 —— 弹窗被裁切、body 也不产生内部滚动条。 */
:global(.restore-run-detail-modal .ant-modal-content) {
  max-height: calc(100vh - 48px) !important;
  display: flex !important;
  flex-direction: column !important;
  padding: 0 !important;
  overflow: hidden !important;
}

/* 滚动条挂在弹窗 body 上：贴弹窗右边缘，面板内部不再单独出滚动条。
   面板的 header / footer 用 sticky 吸顶吸底，视觉上仍是固定区。 */
:global(.restore-run-detail-modal .ant-modal-body) {
  flex: 1 1 auto !important;
  min-height: 0 !important;
  padding: 0 !important;
  overflow-y: auto !important;
  overflow-x: hidden !important;
  overscroll-behavior: contain;
  scrollbar-gutter: stable;
}
:global(.restore-run-detail-modal .ant-modal-body::-webkit-scrollbar) {
  width: 8px;
}
:global(.restore-run-detail-modal .ant-modal-body::-webkit-scrollbar-track) {
  background: transparent;
}
:global(.restore-run-detail-modal .ant-modal-body::-webkit-scrollbar-thumb) {
  background: rgba(15, 118, 110, 0.32);
  border-radius: 4px;
  border: 1px solid transparent;
  background-clip: padding-box;
}
:global(.restore-run-detail-modal .ant-modal-body::-webkit-scrollbar-thumb:hover) {
  background: #0f766e;
}
</style>
