<template>
  <div class="gi-page-shell backup-task-page">
    <div class="gi-page-toolbar">
      <div>
        <div class="gi-page-title">数据备份</div>
      </div>
      <a-space>
        <!-- 与首页「我的资源 · 备份任务」卡同一口径：只看 created_by = 当前用户的任务 -->
        <a-checkbox v-model:checked="query.mine" @change="handleSearch">只看我创建的</a-checkbox>
        <a-input-search
          v-model:value="query.search"
          placeholder="搜索任务ID/名称/数据源/创建人"
          class="search-input"
          @search="handleSearch"
        />
        <!-- 注：原顶部「数据源」下拉已按需求移除（2026-10-22）。
             按数据源过滤现在只剩一个入口——从数据源管理 / 数据源总览带 ?instance_id 跳进来，
             由下方 a-alert 说明当前过滤口径并提供「查看全部任务」退出。 -->
        <a-button v-if="canViewStorageTargets" @click="openStorageTargets">存储目标</a-button>
        <a-button
          v-if="canManageBackupTask && selectedRowKeys.length"
          danger
          :loading="batchDeleteUi.loading"
          :disabled="!selectedRowKeys.length"
          @click="openBatchDeleteModal"
        >
          批量删除 ({{ selectedRowKeys.length }})
        </a-button>
        <a-button v-if="canManageBackupTask" type="primary" @click="openCreate">新建备份任务</a-button>
      </a-space>
    </div>

    <a-card>
      <!-- 从「数据源管理」删除被拒跳进来时的上下文提示：
           说清当前按哪个数据源过滤；实例记录已被删除（历史孤儿任务）时降级为警告，
           但过滤照旧生效（后端按主键 source_instance_id 匹配）。 -->
      <a-alert
        v-if="jumpContext.active"
        class="backup-jump-context"
        :type="jumpContext.isEmpty ? 'success' : jumpContext.instanceMissing ? 'warning' : 'info'"
        show-icon
        :message="jumpContext.message"
      >
        <template #action>
          <a-space>
            <a-button type="link" size="small" @click="clearInstanceFilter">查看全部任务</a-button>
            <a-button
              v-if="canViewInstances"
              type="link"
              size="small"
              @click="backToInstances"
            >
              返回数据源管理
            </a-button>
          </a-space>
        </template>
      </a-alert>
      <div v-if="selectedRowKeys.length" class="backup-task-selection-bar">
        <span class="backup-task-selection-text">
          已选择 <strong>{{ selectedRowKeys.length }}</strong> 个备份任务
        </span>
        <a-button type="link" size="small" @click="clearSelection">取消选择</a-button>
      </div>
      <div ref="tableWrapRef" class="backup-task-table-wrap">
      <a-table
        class="backup-task-table"
        :columns="columns"
        :data-source="tableData"
        :loading="uiState.loading"
        :pagination="pagination"
        :row-selection="canManageBackupTask ? rowSelection : undefined"
        :scroll="tableScroll"
        table-layout="fixed"
        row-key="id"
        @change="handleTableChange"
      >
        <template #headerCell="{ column }">
          <div class="table-header-content" :class="{ 'is-center': isCenteredColumn(column.key) }">
            <span class="table-header-text">{{ column.title }}</span>
            <span
              v-if="isResizableColumn(column.key)"
              class="column-resize-handle"
              @mousedown.stop.prevent="startColumnResize(column.key, $event)"
              @dblclick.stop.prevent="autoFitColumn(column.key)"
            />
          </div>
        </template>
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'task_id'">
            <div class="task-id-row">
              <a-tooltip :title="record.id || '-'">
                <button type="button" class="task-id-link" @click="openRunsDrawer(record)">{{ record.id || '-' }}</button>
              </a-tooltip>
              <a-tooltip title="复制任务ID">
                <button
                  type="button"
                  class="task-id-copy-btn"
                  aria-label="复制任务ID"
                  @click.stop="copyTaskId(record)"
                >
                  <CopyOutlined />
                </button>
              </a-tooltip>
            </div>
            <div class="task-desc task-name-row">
              <template v-if="!editingTaskName[record.id]">
                <a-tooltip :title="record.name || '未命名任务'">
                  <span class="task-name-text">{{ taskNameDisplay(record.name) }}</span>
                </a-tooltip>
                <a-tooltip title="修改任务名称">
                  <button
                    v-if="canManageBackupTask"
                    type="button"
                    class="task-name-edit-btn"
                    aria-label="修改任务名称"
                    @click.stop="startEditTaskName(record)"
                  >
                    <EditOutlined />
                  </button>
                </a-tooltip>
              </template>
              <template v-else>
                <a-input
                  :ref="(el) => setTaskNameInputRef(record.id, el)"
                  v-model:value="editingTaskName[record.id]"
                  size="small"
                  :maxLength="64"
                  class="task-name-input"
                  placeholder="请输入任务名称"
                  @pressEnter="saveTaskName(record)"
                  @blur="saveTaskName(record)"
                  @keydown.esc="cancelEditTaskName(record)"
                />
              </template>
            </div>
          </template>
          <template v-else-if="column.key === 'data_source'">
            <!-- 数据库类型图标 + 实例名称/实例地址，层级与图标展现方式同「数据恢复」页数据源列：名称为主、地址为辅 -->
            <div class="data-source-cell">
              <img
                class="db-logo"
                :src="dbTypeLogo(record.db_type)"
                :alt="record.db_type || 'db'"
              />
              <div class="data-source-text">
                <div class="task-id">
                  <DataSourceName :name="record.instance_remark" />
                </div>
                <a-tooltip :title="`${record.instance_host || '-'}:${record.instance_port || '-'}`">
                  <div class="task-desc muted">
                    {{ record.instance_host }}:{{ record.instance_port }}
                  </div>
                </a-tooltip>
              </div>
            </div>
          </template>
          <template v-else-if="column.key === 'backup_type'">
            <!-- 备份类型：按驱动类型展示「逻辑备份 / 物理备份」，复用既有 driverTypeLabel -->
            <span>{{ driverTypeLabel(record.driver_type) }}</span>
          </template>
          <template v-else-if="column.key === 'storage'">
            <a-tooltip :title="storageTargetName(record) || '-'">
              <span>{{ storageTargetName(record) || '-' }}</span>
            </a-tooltip>
          </template>
          <template v-else-if="column.key === 'created_at'">
            <span>{{ formatDateTime(record.created_at) }}</span>
          </template>
          <template v-else-if="column.key === 'created_by'">
            <span>{{ record.created_by || '-' }}</span>
          </template>
          <template v-else-if="column.key === 'task_status'">
            <div class="backup-task-status-stack" :style="{ alignItems: (columnSpecs[column.key]?.align === 'center' ? 'center' : 'flex-start') }">
              <div class="backup-task-status-top">{{ strategyLabel(record) }}</div>
              <div class="backup-task-status-bottom">
                <a-tag
                  v-if="isLastStatusSuccess(record)"
                  size="small"
                  class="backup-status-tag backup-status-tag-success"
                >
                  {{ lastBackupResultLabel(record) }}
                </a-tag>
                <a-tag
                  v-else-if="isLastStatusFailed(record)"
                  color="error"
                  size="small"
                  class="backup-status-tag"
                >
                  {{ lastBackupResultLabel(record) }}
                </a-tag>
                <a-tag
                  v-else-if="isLastStatusRunning(record)"
                  color="processing"
                  size="small"
                  class="backup-status-tag"
                >
                  {{ lastBackupResultLabel(record) }}
                </a-tag>
                <a-tag
                  v-else-if="isLastStatusStopped(record)"
                  color="warning"
                  size="small"
                  class="backup-status-tag"
                >
                  {{ lastBackupResultLabel(record) }}
                </a-tag>
                <span v-else class="backup-status-text">{{ lastBackupResultLabel(record) }}</span>
              </div>
            </div>
          </template>
          <template v-else-if="column.key === 'actions'">
            <div class="action-cell">
              <a-popconfirm
                v-if="canManageBackupTask"
                title="确认立即执行一次备份吗？"
                ok-text="确认"
                cancel-text="取消"
                @confirm="handleRun(record)"
              >
                <a-button size="small" type="primary">立即备份</a-button>
              </a-popconfirm>
              <a-dropdown v-if="canManageBackupTask" placement="bottomRight" trigger="click">
                <a-button size="small" type="link" class="more-action-btn">更多</a-button>
                <template #overlay>
                  <a-menu>
                    <a-menu-item @click="openEdit(record)">编辑</a-menu-item>
                    <a-menu-item @click="openRunsDrawer(record)">详情</a-menu-item>
                    <a-menu-item danger @click="openDeleteModal(record)">删除</a-menu-item>
                  </a-menu>
                </template>
              </a-dropdown>
              <a-button v-else size="small" type="link" @click="openRunsDrawer(record)">详情</a-button>
            </div>
          </template>
        </template>
      </a-table>
      </div>
    </a-card>

    <a-modal
      :open="deleteUi.open"
      :confirm-loading="deleteUi.loading"
      ok-text="确认删除"
      cancel-text="取消"
      @ok="handleDeleteTask"
      @cancel="closeDeleteModal"
    >
      <template #title>
        <span class="delete-modal-title">
          删除备份任务
          <a-tooltip title="删除时，系统会先自动处理长时间未收口的终止中记录；如果当前仍存在正在排队或执行中的备份，本次删除仍会被拦截。">
            <QuestionCircleOutlined class="delete-task-help-icon" />
          </a-tooltip>
        </span>
      </template>
      <div class="delete-tip">
        请输入任务名称确认删除：
        <span class="delete-task-name">{{ deleteUi.taskName || '-' }}</span>
      </div>
      <a-input
        v-model:value="deleteUi.confirmName"
        :placeholder="`请输入任务名称：${deleteUi.taskName || ''}`"
        allow-clear
      />
    </a-modal>

    <a-modal
      :open="batchDeleteUi.open"
      title="批量删除备份任务"
      :confirm-loading="batchDeleteUi.loading"
      :ok-button-props="{ danger: true, disabled: !batchDeleteUi.confirmed }"
      ok-text="确认删除"
      cancel-text="取消"
      @ok="handleBatchDelete"
      @cancel="closeBatchDeleteModal"
    >
      <div class="batch-delete-list">
        <div class="batch-delete-list-title">本次将删除以下 {{ selectedRows.length }} 个任务：</div>
        <ul class="batch-delete-list-body">
          <li v-for="item in selectedRows" :key="item.id">
            <span class="batch-delete-item-name">{{ item.name || '未命名任务' }}</span>
            <span class="batch-delete-item-id">{{ item.id }}</span>
          </li>
        </ul>
      </div>
      <a-checkbox v-model:checked="batchDeleteUi.confirmed">
        我已知悉删除后不可恢复，确认删除所选的 {{ selectedRows.length }} 个备份任务
      </a-checkbox>
    </a-modal>

    <a-drawer :open="runUi.open" width="1040" title="备份任务详情" :body-style="{ overflow: 'hidden' }" @close="closeRunsDrawer">
      <div v-if="runUi.task" class="run-drawer-shell-v2">
        <div class="v2-task-header-simple">
          <div class="v2-task-header-top">
            <div class="v2-task-title-wrap">
              <span class="v2-task-title">{{ runTaskName }}</span>
              <span class="v2-task-id-wrap">
                <span class="v2-task-id-text">{{ runUi.task?.id || '-' }}</span>
                <a-tooltip v-if="runUi.task?.id" title="复制任务ID">
                  <CopyOutlined class="copy-icon" @click="copyTaskId(runUi.task)" />
                </a-tooltip>
              </span>
              <span v-if="!runUi.task?.enabled" class="v2-status-pill is-paused">
                <span class="v2-status-dot" />
                已暂停
              </span>
            </div>
            <div class="v2-task-actions">
              <a-popconfirm
                v-if="canManageBackupTask"
                :title="runUi.task?.enabled ? '确认暂停该备份任务的调度吗？' : '确认恢复该备份任务的调度吗？'"
                ok-text="确认"
                cancel-text="取消"
                @confirm="handleToggleSchedule(runUi.task, !runUi.task?.enabled)"
              >
                <a-button size="small">
                  {{ runUi.task?.enabled ? '暂停任务调度' : '恢复任务调度' }}
                </a-button>
              </a-popconfirm>
              <a-popconfirm
                v-if="canManageBackupTask"
                title="确认立即执行一次备份吗？"
                ok-text="确认"
                cancel-text="取消"
                @confirm="handleRun(runUi.task)"
              >
                <a-button size="small" type="primary">立即备份</a-button>
              </a-popconfirm>
            </div>
          </div>

          <div class="v2-task-inline-meta">
            <div class="v2-inline-meta-item"><span class="v2-inline-meta-label">数据源</span><span class="v2-inline-meta-value">{{ runTaskInstance }}</span></div>
            <div class="v2-inline-meta-item"><span class="v2-inline-meta-label">类型</span><span class="v2-inline-meta-value">{{ runTaskDbType }}</span></div>
            <div class="v2-inline-meta-item"><span class="v2-inline-meta-label">备份方式</span><span class="v2-inline-meta-value">{{ runTaskBackupMode }}</span></div>
            <div class="v2-inline-meta-item"><span class="v2-inline-meta-label">最近成功时间</span><span class="v2-inline-meta-value">{{ recentSuccessTime }}</span></div>
            <button type="button" class="v2-inline-more-btn" @click="runUi.headerMoreOpen = !runUi.headerMoreOpen">{{ runUi.headerMoreOpen ? '收起' : '更多' }}</button>
          </div>
          <div v-if="runUi.headerMoreOpen" class="v2-summary-inline">
            <div class="v2-summary-grid">
              <div class="v2-summary-item">
                <span class="v2-summary-label">调度周期</span>
                <span class="v2-summary-value v2-summary-value-with-action">
                  <span>{{ currentScheduleSummary }}</span>
                  <a-popover
                    v-if="canManageBackupTask"
                    trigger="click"
                    placement="bottomLeft"
                    :open="scheduleUi.open"
                    @openChange="handleSchedulePopoverOpenChange"
                  >
                    <template #content>
                      <div class="v2-schedule-editor">
                        <div class="v2-schedule-editor-title">修改调度周期</div>
                        <div class="v2-schedule-editor-days">
                          <a-checkable-tag
                            v-for="item in weekdayOptions"
                            :key="item.value"
                            :checked="scheduleUi.days.includes(item.value)"
                            @change="(checked) => toggleScheduleDay(item.value, checked)"
                          >
                            {{ item.label }}
                          </a-checkable-tag>
                        </div>
                        <div class="v2-schedule-time-editor">
                          <a-select
                            v-model:value="scheduleUi.hour"
                            size="small"
                            class="v2-schedule-time-select"
                            :options="scheduleHourOptions"
                          />
                          <span class="v2-schedule-time-sep">:</span>
                          <a-select
                            v-model:value="scheduleUi.minute"
                            size="small"
                            class="v2-schedule-time-select"
                            :options="scheduleMinuteOptions"
                          />
                        </div>
                        <div class="v2-schedule-editor-actions">
                          <a-button size="small" @click="closeScheduleEdit">取消</a-button>
                          <a-button size="small" type="primary" :loading="scheduleUi.saving" @click="saveScheduleEdit">保存</a-button>
                        </div>
                      </div>
                    </template>
                    <button type="button" class="v2-inline-action-btn" @click="openScheduleEdit">修改</button>
                  </a-popover>
                </span>
              </div>
              <div class="v2-summary-item"><span class="v2-summary-label">存储目标</span><span class="v2-summary-value">{{ taskStorageTargetName }}</span></div>
              <div class="v2-summary-item"><span class="v2-summary-label">恢复范围</span><span class="v2-summary-value">{{ backupTypeLabel(runUi.taskDetail?.backup_type || runUi.task.backup_type, runUi.taskDetail?.driver_type || latestRun?.driver_type) }}</span></div>
              <div class="v2-summary-item">
                <span class="v2-summary-label">对象范围</span>
                <span class="v2-summary-value v2-summary-value-with-action">
                  <span>{{ taskObjectRangeDisplay }}</span>
                  <a-tooltip title="查看备份对象">
                    <button type="button" class="v2-object-view-btn" @click="openObjectViewer">
                      查看
                    </button>
                  </a-tooltip>
                </span>
              </div>
              <div class="v2-summary-item">
                <span class="v2-summary-label">保存时间</span>
                <span v-if="retentionUi.editing && canManageBackupTask" class="v2-summary-value retention-editing-wrap">
                  <a-select
                    v-model:value="retentionUi.value"
                    size="small"
                    class="v2-retention-select"
                    :options="retentionOptions"
                    :loading="retentionUi.saving"
                    :disabled="retentionUi.saving"
                    @change="handleUpdateRetentionDays"
                    @blur="cancelRetentionEdit"
                  />
                </span>
                <span v-else class="v2-summary-value v2-summary-value-with-action">
                  <span>{{ currentRetentionLabel }}</span>
                  <button v-if="canManageBackupTask" type="button" class="v2-inline-action-btn" @click="openRetentionEdit">修改</button>
                </span>
              </div>
            </div>
          </div>
        </div>

        <div v-if="isMixedStrategyCardVisible" class="v2-strategy-card">
          <!-- 混合备份策略可折叠：默认收起仅显示标题行 + 摘要，点击整行切换展开/收起 -->
          <button type="button" class="v2-strategy-head v2-strategy-toggle" @click="mixedStrategyExpanded = !mixedStrategyExpanded">
            <span class="v2-strategy-title">混合备份策略</span>
            <a-tag color="blue" style="margin-left:6px">推荐模式</a-tag>
            <span class="v2-strategy-sub" style="margin-left:12px">{{ mixedStrategyExpanded ? '全量基准 + 增量/差异接力，全自动按周循环执行' : mixedStrategySummary }}</span>
            <span :class="['v2-strategy-chevron', { 'is-open': mixedStrategyExpanded }]" />
          </button>
          <div v-show="mixedStrategyExpanded" class="v2-strategy-body">
          <div class="strategy-grid">
            <div class="strategy-column is-full">
              <div class="strategy-column-header">
                <span class="strategy-dot is-full" />
                <span>全量基准日</span>
              </div>
              <div class="strategy-weekdays">
                <a-checkable-tag v-for="item in weekdayOptions" :key="'v2-full-' + item.value" :checked="strategyOverview.fullDays.includes(item.value)" disabled>{{ item.label }}</a-checkable-tag>
              </div>
              <div class="strategy-column-footer">{{ strategyOverview.fullDays.length ? `每周共 ${strategyOverview.fullDays.length} 天做全量基准` : '未配置全量日（实际每日执行全量）' }}</div>
            </div>
            <div class="strategy-arrow">→</div>
            <div class="strategy-column is-other">
              <div class="strategy-column-header">
                <span class="strategy-dot is-other" />
                <span>其余日期：{{ strategyOverview.otherDaysTypeLabel }}</span>
              </div>
              <div class="strategy-weekdays">
                <a-checkable-tag v-for="item in weekdayOptions" :key="'v2-other-' + item.value" :checked="strategyOverview.otherDays.includes(item.value)" disabled>{{ item.label }}</a-checkable-tag>
              </div>
              <div class="strategy-column-footer">{{ strategyOverview.fallbackEnabled ? '已开启智能兜底：无基准时自动转 FULL' : '未开启兜底：缺少基准可能执行失败' }}</div>
            </div>
          </div>
          <div class="v2-strategy-preview">
            <span class="strategy-preview-label">下次执行预判：</span>
            <span class="strategy-preview-value">{{ strategyOverview.nextActionPreview }}</span>
          </div>
          </div>
        </div>

        <div class="v2-tabs-section">
          <a-tabs v-model:activeKey="runUi.v2Tab" class="v2-main-tabs">
            <a-tab-pane key="records" tab="执行记录">
              <div class="v2-records-pane">
                <a-spin :spinning="runUi.loading" class="v2-records-loading">
                  <a-empty v-if="!runUi.loading && (!runUi.runs || runUi.runs.length === 0)" description="暂无执行记录。点击“立即备份”后将生成第一条执行记录。" />
                  <div v-if="runUi.runs && runUi.runs.length > 0" class="v2-records-table-scroll">
                    <a-table
                      :data-source="paginatedRuns"
                      :pagination="false"
                      :row-key="(r) => r.id"
                      size="small"
                      class="v2-run-table"
                      :expand-icon-column-index="-1"
                      v-model:expandedRowKeys="runUi.expandedRowKeys"
                    >
                      <template #expandedRowRender="{ record }">
                        <div class="v2-expand-panel">
                          <a-tabs :activeKey="runUi.expandTabMap[record.id] || 'overview'" size="small" @update:activeKey="(k) => setExpandTab(record.id, k)">
                            <a-tab-pane key="overview" tab="执行概览">
                              <div class="detail-kpi-grid" style="grid-template-columns:repeat(4,1fr);gap:8px;margin-bottom:10px">
                                <div class="detail-kpi-card" style="padding:8px 10px"><div class="detail-kpi-label">执行状态</div><div class="detail-kpi-value"><a-tag :color="statusColor(record.status)">{{ statusLabel(record.status) }}</a-tag></div></div>
                                <div class="detail-kpi-card" style="padding:8px 10px"><div class="detail-kpi-label">当前阶段</div><div class="detail-kpi-value">{{ record.stage_label || '-' }}</div></div>
                                <div class="detail-kpi-card" style="padding:8px 10px"><div class="detail-kpi-label">触发方式</div><div class="detail-kpi-value">{{ triggerModeLabel(record.trigger_mode) }}</div></div>
                                <div class="detail-kpi-card" style="padding:8px 10px"><div class="detail-kpi-label">总耗时</div><div class="detail-kpi-value">{{ formatDuration(record.duration_seconds) }}</div></div>
                              </div>
                              <a-descriptions :column="2" bordered size="small" class="detail-descriptions">
                                <a-descriptions-item label="开始时间">{{ formatDateTime(record.start_at) }}</a-descriptions-item>
                                <a-descriptions-item label="结束时间">{{ formatDateTime(record.end_at) }}</a-descriptions-item>
                                <a-descriptions-item v-if="isPhysicalDriver(record.driver_type)" :label="isPhysicalDriver(record.driver_type) ? '执行位置' : '执行节点'">{{ executorDisplayLabel(record) }}</a-descriptions-item>
                                <a-descriptions-item v-if="isPhysicalDriver(record.driver_type)" :label="isPhysicalDriver(record.driver_type) ? '备份程序状态' : '节点状态'">{{ executorStatusLabel(record.executor_node_status, record.driver_type) }}</a-descriptions-item>
                                <a-descriptions-item v-if="record.stop_state_label" label="终止状态">{{ stopStateDisplay(record) }}</a-descriptions-item>
                                <a-descriptions-item v-if="record.stop_state_message" label="终止说明">{{ record.stop_state_message }}</a-descriptions-item>
                                <a-descriptions-item label="数据库版本">{{ record.server_version || '-' }}</a-descriptions-item>
                                <a-descriptions-item label="执行工具">{{ record.tool || '-' }}</a-descriptions-item>
                                <a-descriptions-item label="备份驱动">{{ driverTypeLabel(record.driver_type) }}</a-descriptions-item>
                                <a-descriptions-item label="备份类型">{{ backupTypeLabel(record.backup_type, record.driver_type) }}</a-descriptions-item>
                                <a-descriptions-item label="备份范围">{{ objectModeLabel(record.object_mode, record.driver_type) }}</a-descriptions-item>
                                <a-descriptions-item v-if="record.backup_type_reason" label="类型判定说明" :span="2">{{ record.backup_type_reason }}</a-descriptions-item>
                                <a-descriptions-item label="备份对象" :span="2">{{ record.object_summary || '-' }}</a-descriptions-item>
                              </a-descriptions>
                              <div class="timeline-title" style="margin:10px 0 6px;font-size:13px">执行过程</div>
                              <div class="run-stage-timeline">
                                <div v-for="stage in buildRunTimeline(record)" :key="stage.key" class="timeline-item" :class="[`is-${stage.state}`]">
                                  <div class="timeline-dot" />
                                  <div class="timeline-content"><div class="timeline-stage">{{ stage.label }}</div><div class="timeline-desc">{{ stage.description }}</div></div>
                                </div>
                              </div>
                            </a-tab-pane>
                            <a-tab-pane key="artifact" tab="产物信息">
                              <a-descriptions :column="2" bordered size="small" class="detail-descriptions">
                                <a-descriptions-item label="产物文件">{{ record.file_name || '-' }}</a-descriptions-item>
                                <a-descriptions-item label="文件大小">{{ formatSize(record.size_bytes) }}</a-descriptions-item>
                                <a-descriptions-item label="产物路径" :span="2">
                                  <div class="path-row">
                                    <span class="path-text" :title="fullStoragePath(record.file_path, record)">{{ fullStoragePath(record.file_path, record) }}</span>
                                    <a-tooltip v-if="record.file_path" title="复制路径">
                                      <CopyOutlined class="copy-icon" @click="copyText(fullStoragePath(record.file_path, record))" />
                                    </a-tooltip>
                                  </div>
                                </a-descriptions-item>
                                <a-descriptions-item label="日志路径" :span="2">
                                  <div class="path-row">
                                    <span class="path-text" :title="fullStoragePath(record.log_path, record)">{{ fullStoragePath(record.log_path, record) }}</span>
                                    <a-tooltip v-if="record.log_path" title="复制路径">
                                      <CopyOutlined class="copy-icon" @click="copyText(fullStoragePath(record.log_path, record))" />
                                    </a-tooltip>
                                  </div>
                                </a-descriptions-item>
                                <a-descriptions-item label="SHA256" :span="2">
                                  <div class="path-row">
                                    <span class="path-text" :title="record.sha256 || '-'">{{ compactSha(record.sha256) }}</span>
                                    <a-tooltip v-if="record.sha256" title="复制 SHA256">
                                      <CopyOutlined class="copy-icon" @click="copyText(record.sha256)" />
                                    </a-tooltip>
                                  </div>
                                </a-descriptions-item>
                                <a-descriptions-item label="备份集">{{ record.set_name || '-' }}</a-descriptions-item>
                                <a-descriptions-item label="校验结果">{{ record.sha256 ? '已完成校验' : '未生成校验值' }}</a-descriptions-item>
                              </a-descriptions>
                            </a-tab-pane>
                            <a-tab-pane key="log" tab="执行日志">
                              <div class="log-toolbar" style="margin-bottom:8px">
                                <a-space>
                                  <a-button size="small" :loading="logUi.runId === record.id && logUi.loading" @click="refreshRunLogFor(record)">刷新日志</a-button>
                                  <a-button size="small" @click="openLogModal(record)">全屏查看</a-button>
                                  <a-button size="small" @click="copyText((getRunLogCache(record.id)) || '')">复制日志</a-button>
                                </a-space>
                              </div>
                              <a-alert
                                v-if="record.status === 'STOP_REQUESTED'"
                                :type="stopStateTone(record)"
                                show-icon
                                class="detail-inline-alert"
                                :message="record.stop_state_message || '已收到终止请求，正在终止当前备份进程。'"
                              />
                              <a-alert
                                v-else-if="record.status === 'PENDING_PREPARE'"
                                :type="(record.prepare_status && record.prepare_status === 'PREPARE_ERROR') ? 'error' : 'info'"
                                show-icon
                                class="detail-inline-alert"
                                :message="(record.prepare_status && record.prepare_status === 'PREPARE_ERROR') ? (record.prepare_title ? `${record.prepare_title}（阶段：${prepareStageLabel(record.prepare_stage)}）` : `备份准备失败（阶段：${prepareStageLabel(record.prepare_stage)}），请查看下方异常分析。`) : '任务正在进行备份环境准备（节点接入、SSH 拓扑、仓库初始化等），请稍候。'"
                              />
                              <pre class="log-preview inline-log-preview" style="min-height:280px;max-height:40vh">{{ (runUi.selectedRunId === record.id ? highlightedLogContent : getRunLogCache(record.id)) || '暂无日志内容，点击刷新日志按钮加载。' }}</pre>
                            </a-tab-pane>
                            <a-tab-pane key="analysis" tab="异常分析">
                              <a-descriptions :column="1" bordered size="small" class="detail-descriptions">
                                <a-descriptions-item label="失败阶段">{{ (record.prepare_status && record.prepare_status === 'PREPARE_ERROR' && record.prepare_stage) ? prepareStageLabel(record.prepare_stage) : (record.stage_label || '-') }}</a-descriptions-item>
                                <a-descriptions-item label="中文标题">
                                  <span v-if="record.prepare_status && record.prepare_status === 'PREPARE_ERROR' && record.prepare_title">{{ record.prepare_title }}</span>
                                  <span v-else>-</span>
                                </a-descriptions-item>
                                <a-descriptions-item label="原始错误">
                                  <span v-if="record.prepare_status && record.prepare_status === 'PREPARE_ERROR' && record.prepare_raw_error">{{ record.prepare_raw_error }}</span>
                                  <span v-else-if="record.error_message">{{ record.error_message }}</span>
                                  <span v-else>无</span>
                                </a-descriptions-item>
                                <a-descriptions-item label="详细描述">
                                  <span v-if="record.prepare_status && record.prepare_status === 'PREPARE_ERROR' && record.prepare_detail">{{ record.prepare_detail }}</span>
                                  <span v-else>-</span>
                                </a-descriptions-item>
                                <a-descriptions-item label="影响范围">{{ buildRunAnalysis(record, getRunLogCache(record.id)).impact }}</a-descriptions-item>
                                <a-descriptions-item label="修复建议">
                                  <div class="analysis-list">
                                    <template v-if="record.prepare_status && record.prepare_status === 'PREPARE_ERROR' && record.prepare_suggestion">
                                      <div class="analysis-item">1. {{ record.prepare_suggestion }}</div>
                                    </template>
                                    <div v-for="(tip, idx) in buildRunAnalysis(record, getRunLogCache(record.id)).tips" :key="`tip-${idx}`" class="analysis-item">{{ (record.prepare_status && record.prepare_status === 'PREPARE_ERROR' && record.prepare_suggestion ? idx + 2 : idx + 1) }}. {{ tip }}</div>
                                  </div>
                                </a-descriptions-item>
                              </a-descriptions>
                              <div class="analysis-actions" style="margin-top:10px">
                                <a-space>
                                  <a-button size="small" @click="openLogModal(record)">查看日志</a-button>
                                  <a-button size="small" v-if="record.executor_node_id" @click="openAgentInstall({ id: record.executor_node_id })">去修复节点</a-button>
                                  <a-button size="small" type="primary" @click="handleRun(runUi.task)">重新执行</a-button>
                                </a-space>
                              </div>
                            </a-tab-pane>
                          </a-tabs>
                        </div>
                      </template>
                      <a-table-column title="执行时间" data-index="created_at" width="190">
                        <template #default="{ record }">{{ formatDateTime(record.created_at) }}</template>
                      </a-table-column>
                      <a-table-column title="耗时" width="120">
                        <template #default="{ record }">{{ formatDuration(record.duration_seconds) }}</template>
                      </a-table-column>
                      <a-table-column title="触发方式" width="120">
                        <template #default="{ record }">{{ triggerModeLabel(record.trigger_mode) }}</template>
                      </a-table-column>
                      <a-table-column title="产物摘要">
                        <template #default="{ record }">{{ runArtifactSummary(record) }}</template>
                      </a-table-column>
                      <a-table-column title="状态" width="120">
                        <template #default="{ record }"><a-tag class="v2-run-status-tag" :color="statusColor(record.status)">{{ statusLabel(record.status) }}</a-tag></template>
                      </a-table-column>
                      <a-table-column title="操作" width="150" fixed="right">
                        <template #default="{ record }">
                          <a-space size="small" split>
                            <a-button
                              v-if="
                                canManageBackupTask &&
                                ['PENDING', 'RUNNING', 'STOP_REQUESTED'].includes(record.status)
                              "
                              type="link"
                              size="small"
                              danger
                              @click="handleStopRun(record)"
                            >
                              终止
                            </a-button>
                            <a-button class="v2-link-btn" type="link" size="small" @click="setExpandRowFocus(record)">详情</a-button>
                          </a-space>
                        </template>
                      </a-table-column>
                    </a-table>
                  </div>
                </a-spin>
                <div v-if="runUi.runs && runUi.runs.length > 0" class="v2-pagination-wrap">
                  <span class="v2-total-text">共 {{ filteredRuns.length }} 条</span>
                  <a-pagination
                    size="small"
                    v-model:current="runUi.pagination.page"
                    v-model:pageSize="runUi.pagination.pageSize"
                    :total="filteredRuns.length"
                    show-size-changer
                    :page-size-options="['10', '20', '50']"
                    :show-total="false"
                  />
                </div>
              </div>
            </a-tab-pane>
            <a-tab-pane key="sets" tab="备份集">
              <div class="v2-pane-card">
                <a-empty v-if="successfulRunsWithSet.length === 0" description="当前暂无可用备份集。" />
                <template v-else>
                  <a-table :data-source="successfulRunsWithSet" :pagination="false" size="small" row-key="id" class="v2-run-table">
                    <a-table-column title="备份集 ID" width="200">
                      <template #default="{ record }">
                        <div class="v2-set-id-cell">
                          <span class="v2-set-id-text" :title="record.backup_sets_id || '-'">{{ record.backup_sets_id || '-' }}</span>
                          <a-tooltip v-if="record.backup_sets_id" title="复制备份集ID">
                            <CopyOutlined class="copy-icon" @click="copyText(record.backup_sets_id, '备份集ID已复制')" />
                          </a-tooltip>
                        </div>
                      </template>
                    </a-table-column>
                    <a-table-column title="备份时间" width="190">
                      <template #default="{ record }">{{ formatDateTime(record.end_at || record.start_at || record.created_at) }}</template>
                    </a-table-column>
                    <a-table-column title="类型" width="180">
                      <template #default="{ record }">{{ backupTypeLabel(record.backup_type, record.driver_type) }}</template>
                    </a-table-column>
                    <a-table-column title="大小" width="120">
                      <template #default="{ record }">{{ formatSize(record.size_bytes) }}</template>
                    </a-table-column>
                    <a-table-column title="操作" width="100">
                      <template #default="{ record }">
                        <a-button class="v2-link-btn" type="link" size="small" @click="goBackupSets(record)">查看</a-button>
                      </template>
                    </a-table-column>
                  </a-table>
                  <!-- 无可用关联时整行不渲染，避免底部留出空档 -->
                  <div v-if="latestRun?.set_id" class="v2-pane-footer">
                    <a-button size="small" @click="goBackupSets(latestRun)">进入备份集页面</a-button>
                  </div>
                </template>
              </div>
            </a-tab-pane>
            <a-tab-pane key="config" tab="任务配置">
              <a-descriptions :column="2" bordered size="small" class="detail-descriptions">
                <a-descriptions-item label="任务名称">{{ runTaskName }}</a-descriptions-item>
                <a-descriptions-item label="任务编号">{{ runUi.task?.id || '-' }}</a-descriptions-item>
                <a-descriptions-item label="数据源">{{ runTaskInstance }}</a-descriptions-item>
                <a-descriptions-item label="数据库类型">{{ runTaskDbType }}</a-descriptions-item>
                <a-descriptions-item label="备份驱动">{{ driverTypeLabel(runUi.taskDetail?.driver_type || latestRun?.driver_type) }}</a-descriptions-item>
                <a-descriptions-item label="恢复范围">{{ backupTypeLabel(runUi.taskDetail?.backup_type || runUi.task.backup_type, runUi.taskDetail?.driver_type || latestRun?.driver_type) }}</a-descriptions-item>
                <a-descriptions-item label="对象范围">{{ objectModeLabel(runUi.taskDetail?.object_mode || runUi.task.object_mode, runUi.taskDetail?.driver_type || latestRun?.driver_type) }}</a-descriptions-item>
                <a-descriptions-item label="备份对象">{{ taskObjectSummary }}</a-descriptions-item>
                <a-descriptions-item :label="taskExecutorLabel">{{ taskExecutorDisplay }}</a-descriptions-item>
                <a-descriptions-item label="调度周期">{{ scheduleSummary(runUi.taskDetail?.schedule_days, runUi.taskDetail?.schedule_time) }}</a-descriptions-item>
                <a-descriptions-item label="存储目标">
                  <template v-if="resolveStorageTargetID(runUi.taskDetail) || resolveStorageTargetID(runUi.task)">
                    <span>{{ storageTargetDisplayName(resolveStorageTargetID(runUi.taskDetail) || resolveStorageTargetID(runUi.task)) || `ID ${resolveStorageTargetID(runUi.taskDetail) || resolveStorageTargetID(runUi.task)}` }}</span>
                  </template>
                  <template v-else>
                    <a-tag color="default" style="margin-right: 6px">未关联</a-tag>
                    <span class="muted">未绑定存储目标，请编辑任务重新选择</span>
                  </template>
                </a-descriptions-item>
                <a-descriptions-item label="备份目录">{{ runUi.taskDetail?.output_dir || runUi.task?.output_dir || '-' }}</a-descriptions-item>
                <a-descriptions-item label="保留天数">{{ runUi.taskDetail?.retention_days || runUi.task?.retention_days || '-' }}</a-descriptions-item>
                <a-descriptions-item label="创建人">{{ runUi.task?.created_by || '-' }}</a-descriptions-item>
                <a-descriptions-item label="当前状态">{{ taskStatusLabel(realTaskStatus(runUi.task), runUi.task?.last_status) }}</a-descriptions-item>
              </a-descriptions>
            </a-tab-pane>
          </a-tabs>
        </div>
      </div>
    </a-drawer>

    <a-drawer :open="objectViewerUi.open" title="备份对象" width="25vw" @close="closeObjectViewer">
      <template #extra>
        <a-button v-if="canManageBackupTask" size="small" @click="handleEditBackupObjects">编辑对象</a-button>
      </template>
      <div class="object-viewer-shell">
        <div class="object-viewer-meta">
          <div class="object-viewer-label">备份对象</div>
          <div class="object-viewer-value">{{ taskObjectSummary }}</div>
        </div>
        <a-empty v-if="objectViewerTreeData.length === 0" description="当前暂无可展示的对象树信息" />
        <a-tree
          v-else
          class="object-viewer-tree"
          :tree-data="objectViewerTreeData"
          :show-icon="true"
          :expanded-keys="objectViewerUi.expandedKeys"
          @expand="(keys) => (objectViewerUi.expandedKeys = keys)"
        />
      </div>
    </a-drawer>

    <a-modal :open="logUi.open" title="执行日志" width="900px" @cancel="closeLogModal">
      <template #footer>
        <a-button @click="closeLogModal">关闭</a-button>
        <a-button :loading="logUi.loading" @click="refreshRunLog">刷新日志</a-button>
      </template>
      <div class="run-summary">
        <div><strong>执行状态：</strong><a-tag :color="statusColor(logUi.status)">{{ statusLabel(logUi.status) }}</a-tag></div>
        <div v-if="isPhysicalDriver(logUi.driverType)"><strong>{{ isPhysicalDriver(logUi.driverType) ? '执行位置' : '执行节点' }}：</strong>{{ logUi.executorNodeDisplay || logUi.executorNodeName || '-' }}</div>
        <div><strong>备份文件：</strong>{{ fullStoragePath(logUi.filePath, selectedRun) }}</div>
        <div><strong>日志文件：</strong>{{ fullStoragePath(logUi.logPath, selectedRun) }}</div>
        <div><strong>日志大小：</strong>{{ formatSize(logUi.logSizeBytes) }}</div>
        <div><strong>日志来源：</strong>{{ logUi.readModeLabel }}</div>
      </div>
      <a-alert
        v-if="logUi.isRunning"
        type="info"
        show-icon
        class="log-alert"
        message="任务正在执行中，日志会自动刷新。"
      />
      <a-alert
        v-else-if="logUi.errorMessage"
        type="error"
        show-icon
        class="log-alert"
        :message="logUi.errorMessage"
      />
      <pre class="log-preview">{{ logUi.content || '暂无日志内容' }}</pre>
    </a-modal>
  </div>
</template>

<script setup>
import { computed, h, inject, nextTick, onActivated, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  CopyOutlined,
  DatabaseOutlined,
  EditOutlined,
  PartitionOutlined,
  QuestionCircleOutlined,
  TableOutlined,
} from '@ant-design/icons-vue'
import { Modal, message } from 'ant-design-vue'
import DataSourceName from '@/components/patterns/DataSourceName.vue'
import { useUserStore } from '@/store/user'
import { useRoute, useRouter } from 'vue-router'
import { dbTypeLogo } from '@/utils/dbTypeLogo'
import {
  deleteBackupTaskApi,
  getBackupInstancesApi,
  getBackupRunLogApi,
  getBackupTaskDetailApi,
  getBackupTaskRunsApi,
  getBackupTasksApi,
  getStorageTargetsApi,
  runBackupTaskApi,
  stopBackupRunApi,
  updateBackupTaskApi,
  updateBackupTaskNameApi,
} from '@/api/backup'
import {
  isRetentionReconcileCanceled,
  updateBackupTaskWithRetentionConfirm,
} from '@/utils/retentionReconcile'
import { extractStorageTargetId, formatStoragePath } from '@/utils/storagePath'

defineOptions({ name: 'BackupTaskView' })

const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)
const route = useRoute()
const router = useRouter()
const userStore = useUserStore()
const canManageBackupTask = computed(() => userStore.hasPermission('backup.task.manage'))
const canViewStorageTargets = computed(() =>
  userStore.hasPermission('backup.task.view') || userStore.hasPermission('backup.set.view') || userStore.hasPermission('backup.task.manage'),
)
const canExecuteRestore = computed(() => userStore.hasPermission('backup.restore.execute'))
// 「数据源管理」入口：只有能看实例配置的人才给这个回程按钮，避免点到 403
const canViewInstances = computed(() => userStore.hasPermission('system.instance.view'))

const uiState = reactive({
  loading: false,
  instancesLoading: false,
})

const query = reactive({
  search: '',
  // 首页「我的资源 · 备份任务」卡点击进来带 mine=1，默认就落到「只看我创建的」，
  // 保证卡片数字与列表数字一致（同一口径）
  mine: String(route.query.mine || '') === '1',
  // 数据源筛选：0 表示「全部」，与后端「InstanceID != 0 才过滤」口径一致。
  // 从「数据源管理」删除被拒跳进来时会带 instance_id（实例主键），此处直接落到筛选上，
  // 用户打开页面就已经是过滤后的结果，不需要再手动选一次。
  instance_id: Number(route.query.instance_id || 0) || 0,
  page: 1,
  page_size: 10,
  is_page: true,
})

// 数据源下拉候选项：与「数据备份-新建任务」「数据恢复」取同一份实例接口（MySQL/PostgreSQL）
const instanceOptions = ref([])

const loadInstances = async () => {
  uiState.instancesLoading = true
  try {
    const res = await getBackupInstancesApi(
      { is_page: false },
      { skipGlobalMessageError: true, silentHttpError: true },
    ).catch(() => null)
    instanceOptions.value = res?.data || []
  } finally {
    uiState.instancesLoading = false
  }
}

const objectViewerUi = reactive({
  open: false,
  expandedKeys: [],
})

const retentionUi = reactive({
  editing: false,
  saving: false,
  value: null,
})

const scheduleUi = reactive({
  open: false,
  saving: false,
  days: [],
  time: '01:00',
  hour: '01',
  minute: '00',
})

const retentionOptions = [
  { label: '7 天', value: 7 },
  { label: '30 天', value: 30 },
  { label: '60 天', value: 60 },
  { label: '90 天', value: 90 },
  { label: '半年', value: 180 },
  { label: '1 年', value: 365 },
]

const scheduleHourOptions = Array.from({ length: 24 }, (_, index) => {
  const value = String(index).padStart(2, '0')
  return { label: value, value }
})

const scheduleMinuteOptions = Array.from({ length: 12 }, (_, index) => {
  const value = String(index * 5).padStart(2, '0')
  return { label: value, value }
})

const tableData = ref([])
const total = ref(0)
const selectedRowKeys = ref([])
const selectedRows = ref([])
const tableWrapRef = ref()
const tableWrapWidth = ref(0)
const tableWrapHeight = ref(0)

// ============================================================================
// 「从数据源管理跳转进来」的上下文
//
// 场景：删数据源时若仍被备份任务引用会被拒绝，前端带 instance_id（实例主键）跳到本页。
// 这里负责把上下文说清楚，并把闭环留出来 —— 任务清空后用户能一键回到数据源管理继续删。
// 实例可能已被删除（历史孤儿任务）：下拉里找不到就降级为警告，但过滤依然生效，
// 因为后端过滤用的是 source_instance_id（主键）而不是业务实例号。
// ============================================================================
const jumpInstanceID = computed(() => Number(route.query.instance_id || 0) || 0)

const jumpContext = computed(() => {
  if (!jumpInstanceID.value) {
    return { active: false, isEmpty: false, instanceMissing: false, message: '' }
  }
  const matched = (instanceOptions.value || []).find(
    (item) => Number(item.id) === jumpInstanceID.value,
  )
  const label = matched
    ? `${matched.hostname}:${matched.port}`
    : String(route.query.instance_uuid || '').trim() || `#${jumpInstanceID.value}`
  const instanceMissing = !uiState.instancesLoading && !matched
  const isEmpty = !uiState.loading && Number(total.value || 0) === 0
  if (isEmpty) {
    return {
      active: true,
      isEmpty: true,
      instanceMissing,
      message: `数据源 ${label} 下已无备份任务，可返回数据源管理继续删除该数据源`,
    }
  }
  const suffix = instanceMissing ? '；该实例已不在实例配置中（可能已被删除）' : ''
  return {
    active: true,
    isEmpty: false,
    instanceMissing,
    message: `当前按数据源 ${label} 过滤，共 ${Number(total.value || 0)} 个备份任务${suffix}`,
  }
})

// 清掉跳转带入的过滤条件，回到「全部任务」
const clearInstanceFilter = () => {
  const rest = { ...route.query }
  delete rest.instance_id
  delete rest.instance_uuid
  router.replace({ path: route.path, query: rest })
  query.instance_id = 0
  handleSearch()
}

const backToInstances = () => {
  router.push({ name: 'view.admin.instance' })
}

const COLUMN_WIDTH_STORAGE_KEY = 'backup_task_table_widths_v7'
const TABLE_WIDTH_GUTTER = 24
const TABLE_HEIGHT_GUTTER = 56
const PAGINATION_RESERVED_HEIGHT = 52
// 多选列固定宽度(与 rowSelection.columnWidth 保持一致),参与横向宽度计算
const SELECTION_COLUMN_WIDTH = 44
const AUTO_FIT_PADDING = 34
const AUTO_FIT_MAX_WIDTH = 560
const MEASURE_FONT = '13px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif'
// 列宽三层定义:
//   min       放下最长真实内容 + padding 的最小宽度(任何视口下都不小于此)
//   preferred 充足空间时的理想宽度(无截断、视觉舒展)
//   max       上限,防止 2K/4K 大屏下被无限拉宽
// 三层弹性:min(内容放得下的最小宽度) → preferred(理想宽度) → max(上限防大屏膨胀)
// weight>0 才参与弹性分配,=0 表示内容固定只取 min。
// 当前 8 列 Σmin ≈ 1271px,溢出走横向滚动。
// task_id 原三层宽度 260/300/360,2026-10-05 按要求整体下调 30% 至 182/210/252,
//   释放的 78/90/108px 平均分给 backup_type 与 created_by(两列各 +39/45/54,即 139/155/184);
//   Σmin 仍为 1271px,横向滚动阈值与小屏表现不变。任务名超长时由 .task-name-text
//   省略号 + tooltip 承载,不影响可读性。
// data_source 需要容纳「16px 数据库类型图标 + 8px 间距 + 实例地址/名称」,故 min 提到 200。
// task_status 需要完整容纳「成功于 2026-09-14 22:30」(约 170px),故 min 提到 176,
// 超出时不再做单元格内滚动,统一交给表格底部横向滚动条。
const columnSpecs = Object.freeze({
  task_id: { title: '任务ID', min: 182, preferred: 210, max: 252, align: 'left', weight: 2 },
  data_source: { title: '数据源', min: 200, preferred: 220, max: 270, align: 'left', weight: 2 },
  backup_type: { title: '备份类型', min: 139, preferred: 155, max: 184, align: 'left', weight: 0 },
  storage: { title: '备份存储空间', min: 130, preferred: 150, max: 190, align: 'center', weight: 1 },
  created_by: { title: '创建人', min: 139, preferred: 155, max: 184, align: 'center', weight: 0 },
  created_at: { title: '创建时间', min: 165, preferred: 170, max: 200, align: 'center', weight: 0 },
  task_status: { title: '状态', min: 176, preferred: 190, max: 224, align: 'center', weight: 0 },
  actions: { title: '操作', min: 140, preferred: 140, max: 160, align: 'center', weight: 0 },
})
const columnOrder = ['task_id', 'data_source', 'backup_type', 'storage', 'created_by', 'created_at', 'task_status', 'actions']

const loadPersistedColumnWidths = () => {
  if (typeof window === 'undefined') return {}
  try {
    const raw = window.localStorage.getItem(COLUMN_WIDTH_STORAGE_KEY)
    const parsed = raw ? JSON.parse(raw) : {}
    const out = {}
    columnOrder.forEach((key) => {
      const value = Number(parsed?.[key])
      if (Number.isFinite(value) && value > 0) {
        out[key] = Math.max(columnSpecs[key].min, Math.round(value))
      }
    })
    return out
  } catch {
    return {}
  }
}

const columnBaseWidths = reactive(loadPersistedColumnWidths())
const resizeState = reactive({
  column: '',
  startX: 0,
  startWidth: 0,
})

const persistColumnWidths = () => {
  if (typeof window === 'undefined') return
  const payload = {}
  columnOrder.forEach((key) => {
    if (columnBaseWidths[key]) payload[key] = columnBaseWidths[key]
  })
  window.localStorage.setItem(COLUMN_WIDTH_STORAGE_KEY, JSON.stringify(payload))
}

const normalizeColumnWidth = (key, width) => {
  const spec = columnSpecs[key]
  if (!spec) return Math.max(80, Math.round(width || 0))
  const value = Number(width || 0)
  return Math.max(spec.min, Math.min(spec.max, Math.round(value || spec.min)))
}

const truncateRune = (text, max = 15) => {
  const s = String(text ?? '').trim()
  if (!s) return '-'
  const chars = Array.from(s)
  return chars.length > max ? chars.slice(0, max).join('') + '…' : s
}
const runeLength = (text) => Array.from(String(text ?? '')).length

const effectiveColumnWidths = computed(() => {
  const widths = {}
  // 第一遍:全部列按 min 起步,累计 baseTotal
  let baseTotal = 0
  columnOrder.forEach((key) => {
    const spec = columnSpecs[key]
    const stored = columnBaseWidths[key] || spec.min
    const base = Math.max(spec.min, Math.min(spec.max, Math.round(stored)))
    widths[key] = base
    baseTotal += base
  })
  const available = Math.max(0, Math.floor(tableWrapWidth.value - TABLE_WIDTH_GUTTER - SELECTION_COLUMN_WIDTH))
  const extra = Math.max(0, available - baseTotal)
  if (extra <= 0) return widths

  // 第二遍:按 weight 把 extra 分配给弹性列,任一列到达 max 后,溢出空间回滚到未达 max 的列
  const elasticKeys = columnOrder.filter((key) => (columnSpecs[key].weight || 0) > 0)
  let remaining = extra
  let totalWeight = elasticKeys.reduce((sum, k) => sum + (columnSpecs[k].weight || 0), 0)
  // 迭代直至剩余空间分配完(可能因 max 截断而需要多轮)
  while (remaining > 0 && totalWeight > 0) {
    let allocatedThisRound = 0
    let activeWeight = 0
    elasticKeys.forEach((key) => {
      if (widths[key] >= columnSpecs[key].max) return
      activeWeight += columnSpecs[key].weight || 0
    })
    if (activeWeight === 0) break
    elasticKeys.forEach((key, index) => {
      const spec = columnSpecs[key]
      if (widths[key] >= spec.max) return
      const isLast = index === elasticKeys.length - 1
      const share = isLast ? remaining - allocatedThisRound : Math.floor((remaining * (spec.weight || 0)) / activeWeight)
      if (share <= 0) return
      const room = spec.max - widths[key]
      const take = Math.min(share, room)
      if (take <= 0) return
      widths[key] += take
      allocatedThisRound += take
    })
    if (allocatedThisRound === 0) break
    remaining -= allocatedThisRound
    totalWeight = activeWeight
  }
  return widths
})

const totalColumnWidth = computed(() => columnOrder.reduce((sum, key) => sum + (effectiveColumnWidths.value[key] || columnSpecs[key].min), 0))
// 表格实际内容宽度 = 数据列宽之和 + 固定多选列宽
const scrollTotalWidth = computed(() => totalColumnWidth.value + SELECTION_COLUMN_WIDTH)
const tableScroll = computed(() => {
  // 固定列(状态/操作)需要 scroll.x 为具体数值才能稳定生效;
  // 取 max(内容总宽, 容器可用宽):容器更窄时横向滚动,更宽时让表格铺满、不留右侧空白。
  const available = Math.max(0, Math.floor(tableWrapWidth.value - TABLE_WIDTH_GUTTER - SELECTION_COLUMN_WIDTH))
  const x = Math.max(scrollTotalWidth.value, available)
  const rawH = Math.floor(tableWrapHeight.value - PAGINATION_RESERVED_HEIGHT - TABLE_HEIGHT_GUTTER)
  const y = Math.max(0, rawH)
  return {
    x,
    y: y || undefined,
    scrollToFirstRowOnChange: false,
  }
})

const isCenteredColumn = (key) => columnSpecs[key]?.align === 'center'
const isResizableColumn = (key) => !!columnSpecs[key]

// 右侧固定列:状态 + 操作。与「备份集」页面保持一致,横向滚动时始终可见。
const FIXED_RIGHT_COLUMNS = ['task_status', 'actions']
const columns = computed(() =>
  columnOrder.map((key) => ({
    title: columnSpecs[key].title,
    dataIndex: key === 'created_by' || key === 'created_at' ? key : undefined,
    key,
    width: effectiveColumnWidths.value[key],
    align: columnSpecs[key].align,
    fixed: FIXED_RIGHT_COLUMNS.includes(key) ? 'right' : undefined,
    customCell: () => ({
      'data-column-key': key,
      class: {
        'backup-task-cell': true,
        'is-center-cell': isCenteredColumn(key),
        'is-wrap-cell': key === 'storage',
      },
    }),
  })),
)

const pagination = computed(() => ({
  total: total.value,
  current: query.page,
  pageSize: query.page_size,
  showSizeChanger: true,
  showTotal: (t) => `共 ${t} 条`,
}))

const clearSelection = () => {
  selectedRowKeys.value = []
  selectedRows.value = []
}

const rowSelection = computed(() => ({
  selectedRowKeys: selectedRowKeys.value,
  columnWidth: SELECTION_COLUMN_WIDTH,
  onChange: (keys, rows) => {
    selectedRowKeys.value = keys
    selectedRows.value = rows
  },
}))


const taskStatusLabel = (status, lastStatusRaw) => {
  if (status === 'SUCCESS') return '成功'
  if (status === 'RUNNING') return '备份中'
  if (status === 'STOPPED') return '已终止'
  if (status === 'FAILED') return '备份失败'
  const raw = ((lastStatusRaw && typeof lastStatusRaw === 'string') ? lastStatusRaw : '').trim()
  return raw === '' ? '未执行' : '状态未知'
}

const taskStatusColor = (status) => {
  if (status === 'SUCCESS') return 'green'
  if (status === 'RUNNING') return 'blue'
  if (status === 'STOPPED') return 'cyan'
  if (status === 'FAILED') return 'red'
  return 'default'
}

const realTaskStatus = (record) => {
  const s = ((record && record.last_status) || '').toUpperCase().trim()
  if (['RUNNING', 'PENDING', 'STOP_REQUESTED'].includes(s)) return 'RUNNING'
  if (s === 'STOPPED') return 'STOPPED'
  if (s === 'FAILED') return 'FAILED'
  if (s === 'SUCCESS') return 'SUCCESS'
  return 'NOT_STARTED'
}

let measureCanvasCtx = null
const getMeasureCtx = () => {
  if (measureCanvasCtx) return measureCanvasCtx
  if (typeof document === 'undefined') return null
  const canvas = document.createElement('canvas')
  measureCanvasCtx = canvas.getContext('2d')
  if (measureCanvasCtx) measureCanvasCtx.font = MEASURE_FONT
  return measureCanvasCtx
}

const measureTextWidth = (text, isStrong = false) => {
  const normalized = String(text || '')
  const ctx = getMeasureCtx()
  if (!ctx) return normalized.length * (isStrong ? 8.4 : 7.4)
  ctx.font = `${isStrong ? '600' : '400'} ${MEASURE_FONT}`
  return ctx.measureText(normalized).width
}

const getColumnWidthSamples = (key) => {
  const samples = [columnSpecs[key]?.title || key]
  const rows = Array.isArray(tableData.value) ? tableData.value.slice(0, 100) : []
  rows.forEach((row) => {
    if (key === 'task_id') {
      samples.push(row.id || '-')
      samples.push(row.name || '未命名任务')
      return
    }
    if (key === 'data_source') {
      samples.push(`${row.instance_host || '-'}:${row.instance_port || '-'}`)
      samples.push(row.instance_remark || '-')
      return
    }
    if (key === 'backup_type') {
      samples.push(driverTypeLabel(row.driver_type))
      return
    }
    if (key === 'storage') {
      samples.push(row.executor_node_name || '本机执行')
      samples.push(row.output_dir || '-')
      return
    }
    if (key === 'created_by') {
      samples.push(row.created_by || '-')
      return
    }
    if (key === 'created_at') {
      samples.push(formatDateTime(row.created_at))
      return
    }
    if (key === 'task_status') {
      samples.push(strategyLabel(row))
      samples.push(lastBackupResultLabel(row))
      return
    }
    if (key === 'actions') {
      samples.push('立即备份 更多')
    }
  })
  return samples
}

const autoFitColumn = (key) => {
  if (!columnSpecs[key]) return
  const samples = getColumnWidthSamples(key)
  const estimated = samples.reduce((max, text) => Math.max(max, measureTextWidth(text, true)), 0) + AUTO_FIT_PADDING
  columnBaseWidths[key] = normalizeColumnWidth(key, estimated)
  persistColumnWidths()
}

const stopColumnResize = () => {
  if (!resizeState.column || typeof document === 'undefined') return
  persistColumnWidths()
  resizeState.column = ''
  document.documentElement.style.userSelect = ''
  document.removeEventListener('mousemove', handleColumnResizeMove)
  document.removeEventListener('mouseup', stopColumnResize)
}

const handleColumnResizeMove = (event) => {
  if (!resizeState.column) return
  const nextWidth = resizeState.startWidth + (event.clientX - resizeState.startX)
  columnBaseWidths[resizeState.column] = normalizeColumnWidth(resizeState.column, nextWidth)
}

const startColumnResize = (key, event) => {
  if (!columnSpecs[key] || typeof document === 'undefined') return
  resizeState.column = key
  resizeState.startX = event.clientX
  resizeState.startWidth = effectiveColumnWidths.value[key] || columnSpecs[key].min
  document.documentElement.style.userSelect = 'none'
  document.addEventListener('mousemove', handleColumnResizeMove)
  document.addEventListener('mouseup', stopColumnResize)
}

const updateTableWrapWidth = () => {
  tableWrapWidth.value = Math.floor(tableWrapRef.value?.clientWidth || 0)
  tableWrapHeight.value = Math.floor(tableWrapRef.value?.clientHeight || 0)
}

const statusLabel = (status) => {
  if (!status) return '未执行'
  if (status === 'SUCCESS') return '成功'
  if (status === 'FAILED') return '失败'
  if (status === 'STOPPED') return '已终止'
  if (status === 'STOP_REQUESTED') return '终止中'
  if (status === 'RUNNING') return '执行中'
  if (status === 'PENDING') return '排队中'
  return status || '-'
}

const statusColor = (status) => {
  if (status === 'SUCCESS') return 'green'
  if (status === 'FAILED') return 'red'
  if (status === 'STOPPED') return 'orange'
  if (status === 'STOP_REQUESTED') return 'gold'
  if (status === 'RUNNING') return 'blue'
  if (status === 'PENDING') return 'orange'
  return 'default'
}

const stopStateTone = (record) => {
  if (!record?.stop_state) return 'info'
  if (record.stop_state === 'UNABLE') return 'error'
  if (record.stop_state === 'UNINTERRUPTIBLE') return 'warning'
  if (record.stop_state === 'IMMEDIATE') return 'success'
  return 'info'
}

const stopStateDisplay = (record) => {
  if (!record?.stop_state_label) return '-'
  if (record.stop_state === 'UNINTERRUPTIBLE' && record.progress_stage_label) {
    return `${record.stop_state_label} / ${record.progress_stage_label}`
  }
  return record.stop_state_label
}

const formatDateTime = (value) => {
  if (!value) return '-'
  const text = `${value}`.replace('T', ' ')
  if (!text || text.startsWith('0001-01-01')) return '-'
  return text.slice(0, 19)
}

const formatDateTimeShort = (value, withTime = true) => {
  if (!value) return ''
  const text = `${value}`.replace('T', ' ')
  if (!text || text.startsWith('0001-01-01')) return ''
  return withTime ? text.slice(0, 16) : text.slice(0, 10)
}

const strategyLabel = (record) => {
  if (!record) return '未执行'
  if (record.enabled === false || !record.enabled) return '已停用'
  const hasRunHistory = !!(record.last_run_id || (record.last_status && String(record.last_status).trim() !== ''))
  if (!hasRunHistory) {
    const days = record.schedule_days
    const hasSchedule = !!record.schedule_time || (Array.isArray(days) && days.length > 0) || (typeof days === 'object' && days && Object.keys(days).length > 0)
    if (!hasSchedule) return '未执行'
  }
  return '运行中'
}

const lastBackupResultLabel = (record) => {
  if (!record) return '等待首次执行'
  const hasRun = !!(record.last_run_id || (record.last_status && String(record.last_status).trim() !== ''))
  if (!hasRun) return '等待首次执行'
  const status = (record.last_status || '').toUpperCase().trim()
  if (['RUNNING', 'PENDING', 'STOP_REQUESTED'].includes(status)) return '执行中...'
  const endTime = record.last_end_at || record.last_start_at
  if (status === 'SUCCESS') {
    const ts = formatDateTimeShort(endTime, true)
    return ts ? `成功于 ${ts}` : '成功'
  }
  if (status === 'FAILED') {
    const ts = formatDateTimeShort(endTime, true)
    return ts ? `失败于 ${ts}` : '失败'
  }
  if (status === 'STOPPED') {
    const ts = formatDateTimeShort(endTime, false)
    return ts ? `已终止于 ${ts}` : '已终止'
  }
  const ts = formatDateTimeShort(endTime, true)
  return ts || (record.last_status ? record.last_status : '等待首次执行')
}

const isLastStatusSuccess = (record) => (record?.last_status || '').toUpperCase().trim() === 'SUCCESS'
const isLastStatusFailed = (record) => (record?.last_status || '').toUpperCase().trim() === 'FAILED'
const isLastStatusRunning = (record) => {
  const status = (record?.last_status || '').toUpperCase().trim()
  return ['RUNNING', 'PENDING', 'STOP_REQUESTED'].includes(status)
}
const isLastStatusStopped = (record) => (record?.last_status || '').toUpperCase().trim() === 'STOPPED'

const MAX_TASK_NAME_LENGTH = 64
const TASK_NAME_ELLIPSIS_AT = 30

const taskNameInputRefs = {}
const editingTaskName = reactive({})
const cancellingEdit = new Set()

const countRunes = (text) => Array.from(String(text || '')).length

const taskNameDisplay = (name) => {
  const text = String(name || '未命名任务').trim()
  if (countRunes(text) <= TASK_NAME_ELLIPSIS_AT) return text
  return `${Array.from(text).slice(0, TASK_NAME_ELLIPSIS_AT).join('')}...`
}

const validateTaskName = (name) => {
  if (!name) {
    message.warning('任务名称不能为空')
    return false
  }
  const runeLen = countRunes(name)
  if (runeLen < 2) {
    message.warning('任务名称至少 2 个字符')
    return false
  }
  if (runeLen > MAX_TASK_NAME_LENGTH) {
    message.warning(`任务名称最长 ${MAX_TASK_NAME_LENGTH} 个字符`)
    return false
  }
  return true
}

const setTaskNameInputRef = (id, el) => {
  if (el) taskNameInputRefs[id] = el
}

const startEditTaskName = (record) => {
  if (!canManageBackupTask.value) return
  editingTaskName[record.id] = String(record.name || '')
  nextTick(() => {
    const inputEl = taskNameInputRefs[record.id]?.$el?.querySelector('input') || taskNameInputRefs[record.id]
    if (inputEl?.focus) inputEl.focus()
    if (inputEl?.select) inputEl.select()
  })
}

const cancelEditTaskName = (record) => {
  cancellingEdit.add(record.id)
  delete editingTaskName[record.id]
  delete taskNameInputRefs[record.id]
  setTimeout(() => cancellingEdit.delete(record.id), 100)
}

// 走轻量改名接口：只写 name 字段，不触发驱动规范化/节点 ensure/目录解析等副作用。
const saveTaskName = async (record) => {
  if (cancellingEdit.has(record.id)) {
    cancellingEdit.delete(record.id)
    return
  }
  const newName = String(editingTaskName[record.id] || '').trim()
  const oldName = String(record?.name || '')
  delete editingTaskName[record.id]
  delete taskNameInputRefs[record.id]
  if (newName === oldName) return
  if (!validateTaskName(newName)) return
  try {
    await updateBackupTaskNameApi(record.id, { name: newName })
    record.name = newName
    const matched = tableData.value.find((item) => item.id === record.id)
    if (matched) matched.name = newName
    message.success('任务名称已更新')
  } catch (error) {
    message.error(error?.message || '任务名称更新失败')
  }
}

const copyText = async (text, successMessage = '复制成功') => {
  const value = String(text || '')
  if (!value) return
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
    } else {
      throw new Error('clipboard unavailable')
    }
    message.success({
      content: successMessage,
      key: 'backup-task-copy-feedback',
      duration: 1,
    })
  } catch {
    message.error({
      content: '复制失败',
      key: 'backup-task-copy-feedback',
      duration: 1.2,
    })
  }
}

const copyTaskId = async (record) => {
  const taskId = String(record?.id || '').trim()
  await copyText(taskId, '复制成功')
}

const formatSize = (size) => {
  const n = Number(size || 0)
  if (!n) return '-'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`
}

const weekdayLabelMap = {
  MON: '周一',
  TUE: '周二',
  WED: '周三',
  THU: '周四',
  FRI: '周五',
  SAT: '周六',
  SUN: '周日',
}

const weekdayOptions = [
  { label: '周一', value: 'MON' },
  { label: '周二', value: 'TUE' },
  { label: '周三', value: 'WED' },
  { label: '周四', value: 'THU' },
  { label: '周五', value: 'FRI' },
  { label: '周六', value: 'SAT' },
  { label: '周日', value: 'SUN' },
]

const currentWeekdayLabel = () => {
  const d = new Date()
  const idx = d.getDay()
  return ['SUN', 'MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT'][idx] || 'MON'
}

const isMixedStrategyCardVisible = computed(() => {
  const detail = runUi.taskDetail || runUi.task
  if (!detail) return false
  const driverType = String(detail.driver_type || '').toUpperCase()
  if (!['PG_BACKREST', 'XTRABACKUP'].includes(driverType)) return false
  const strategyMode = String(detail.driver_config?.strategy_mode || '').toUpperCase()
  return strategyMode === 'MIXED'
})

const strategyOverview = computed(() => {
  const detail = runUi.taskDetail || runUi.task
  const driverConfig = detail?.driver_config || {}
  const configuredFullDays = Array.isArray(driverConfig.full_backup_days) ? [...new Set(driverConfig.full_backup_days)] : []
  const fullDays = configuredFullDays.filter((d) => !!weekdayLabelMap[d])
  const allDayKeys = weekdayOptions.map((item) => item.value)
  const otherDays = allDayKeys.filter((d) => !fullDays.includes(d))
  const otherType = String(driverConfig.other_days_type || 'INCR').toUpperCase()
  const otherDaysTypeLabel = otherType === 'DIFF' ? '差异备份' : '增量备份'
  const fallbackEnabled = driverConfig.fallback_to_full_when_missing !== false
  const today = currentWeekdayLabel()
  const scheduleDays = Array.isArray(detail?.schedule_days) ? detail.schedule_days : []
  const todayIsScheduled = scheduleDays.length === 0 ? true : scheduleDays.includes(today)
  const scheduleTime = detail?.schedule_time || ''
  let nextAction = ''
  if (scheduleDays.length === 0) {
    nextAction = '当前任务未配置调度周期，可手动立即备份。'
  } else if (!todayIsScheduled) {
    nextAction = `今天（${weekdayLabelMap[today]}）不在调度日，下次会在已选日期的 ${scheduleTime || '指定时间'} 执行。`
  } else if (!fullDays.length) {
    // 与后端 resolveEffectiveBackupType 对齐：未配置全量日时每个调度日都执行 FULL
    nextAction = `未配置全量日，按平台默认规则，每个调度日的 ${scheduleTime || '指定时间'} 都将执行 FULL 全量备份。`
  } else {
    if (fullDays.includes(today)) {
      nextAction = `今天（${weekdayLabelMap[today]}）是全量基准日，如按计划触发，将执行 FULL 备份。`
    } else {
      if (fallbackEnabled) {
        nextAction = `今天（${weekdayLabelMap[today]}）是非全量日，将优先执行 ${otherDaysTypeLabel}；若仓库中尚无 FULL 基准，会智能降级为 FULL。`
      } else {
        nextAction = `今天（${weekdayLabelMap[today]}）是非全量日，将执行 ${otherDaysTypeLabel}；请确保仓库中已有 FULL 基准，否则可能失败。`
      }
    }
  }
  return {
    fullDays,
    otherDays,
    otherDaysTypeLabel,
    fallbackEnabled,
    nextActionPreview: nextAction,
  }
})

// 混合备份策略卡片折叠状态：每次打开任务详情抽屉重置为收起
const mixedStrategyExpanded = ref(false)
// 收起时的摘要行：聚合全量基准天数、其余日期类型、兜底开关等关键信息
const mixedStrategySummary = computed(() => {
  const o = strategyOverview.value
  const parts = []
  if (o.fullDays.length) {
    parts.push(`全量基准 ${o.fullDays.length} 天/周`)
    parts.push(`其余日期${o.otherDaysTypeLabel}`)
  } else {
    // 未配置全量日时后端实际每天执行全量，「其余日期增量」的说法与事实相反
    parts.push('未配置全量日（实际每日执行全量）')
  }
  parts.push(o.fallbackEnabled ? '智能兜底已开启' : '未开启兜底')
  return parts.join(' · ')
})

const formatDuration = (seconds) => {
  const total = Math.max(0, Number(seconds || 0))
  if (!total) return '-'
  if (total < 60) return `${total}秒`
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const secs = total % 60
  if (hours > 0) return `${hours}小时${minutes}分${secs}秒`
  return `${minutes}分${secs}秒`
}

const driverTypeLabel = (value) => {
  if (['PG_BACKREST', 'XTRABACKUP'].includes(String(value || '').toUpperCase())) return '物理备份'
  return '逻辑备份'
}

// 混合策略【恢复范围】文案：按后端 resolveEffectiveBackupType 的真实语义描述
// 哪天是全备、哪天是增备。未配置全量日时，平台规则是每个调度日都执行 FULL，
// 必须如实展示，不能显示成「全量日：-」让用户误以为有增备。
const mixedRestoreScopeText = (detail, driverType) => {
  const cfg = detail?.driver_config || {}
  const rawFullDays = Array.isArray(cfg.full_backup_days) ? [...new Set(cfg.full_backup_days)] : []
  const fullDays = rawFullDays.filter((d) => !!weekdayLabelMap[d])
  if (!fullDays.length) {
    const rawSchedDays = Array.isArray(detail?.schedule_days) ? detail.schedule_days : []
    const schedDays = [...new Set(rawSchedDays.filter((d) => !!weekdayLabelMap[d]))]
    const schedLabels = schedDays.length ? schedDays.map((d) => weekdayLabelMap[d]).join('、') : '每天'
    return `混合策略（未配置全量日：${schedLabels}均执行全量备份，暂无增备日）`
  }
  const fullLabels = fullDays.map((d) => weekdayLabelMap[d]).join('、')
  if (driverType === 'PG_BACKREST') {
    const otherType = String(cfg.other_days_type || 'INCR').toUpperCase()
    const otherLabel = otherType === 'DIFF' ? '差异' : '增量'
    return `混合策略（全量日：${fullLabels}，其余：${otherLabel}）`
  }
  return `混合策略（全量日：${fullLabels}，其余：增量）`
}

const backupTypeLabel = (value, driverType) => {
  if (driverType === 'PG_BACKREST') {
    const strategyMode = String(runUi.taskDetail?.driver_config?.strategy_mode || '').toUpperCase()
    if (strategyMode === 'MIXED') {
      return mixedRestoreScopeText(runUi.taskDetail, driverType)
    }
    if (value === 'DIFF') return '单一模式：整实例差异'
    if (value === 'INCR') return '单一模式：整实例增量'
    return '单一模式：整实例全量'
  }
  if (driverType === 'XTRABACKUP') {
    const strategyMode = String(runUi.taskDetail?.driver_config?.strategy_mode || '').toUpperCase()
    if (strategyMode === 'MIXED') {
      return mixedRestoreScopeText(runUi.taskDetail, driverType)
    }
    if (value === 'INCR') return '单一模式：整实例增量'
    return '单一模式：整实例全量'
  }
  if (value === 'SCHEMA') return '仅结构'
  if (value === 'DATA') return '仅数据'
  return '表结构+全量数据'
}

const objectModeLabel = (value, driverType) => {
  if (isPhysicalDriver(driverType)) return '整实例'
  if (value === 'ALL') return '全部实例'
  if (value === 'CUSTOM') return '自定义对象'
  return '-'
}

const triggerModeLabel = (value) => {
  if (value === 'SCHEDULED') return '定时触发'
  if (value === 'MANUAL') return '手动触发'
  return '-'
}

const nodeStatusLabel = (value) => {
  if (value === 'ONLINE') return '在线'
  if (value === 'OFFLINE') return '离线'
  return '-'
}

const isPhysicalDriver = (driverType) => ['PG_BACKREST', 'XTRABACKUP'].includes(String(driverType || '').toUpperCase())

const executorDisplayLabel = (record) => {
  if (isPhysicalDriver(record?.driver_type)) {
    const host = record?.instance_host || record?.hostname || '-'
    const port = record?.instance_port || record?.port || ''
    return port ? `${host}:${port}` : host
  }
  return record?.executor_node_name || '本机执行'
}

const executorStatusLabel = (status, driverType) => {
  if (isPhysicalDriver(driverType)) {
    if (status === 'ONLINE') return '就绪'
    if (status === 'OFFLINE') return '未就绪'
    return '-'
  }
  return nodeStatusLabel(status)
}

const scheduleSummary = (days, time) => {
  const list = Array.isArray(days) ? days : []
  const labels = list.map((item) => weekdayLabelMap[item] || item).filter(Boolean)
  if (!labels.length && !time) return '未配置'
  return `${labels.join('、') || '未配置'} ${time || ''}`.trim()
}

const compactSha = (value) => {
  const text = String(value || '').trim()
  if (!text) return '-'
  return text
}

const isZipArtifact = (fileName) => String(fileName || '').trim().toLowerCase().endsWith('.zip')

// 金仓与 PostgreSQL 共用同一条逻辑恢复通道（sys_restore / ksql 与 pg_restore / psql
// 的参数完全对齐），因此整实例范围、zip 归档这两种「PG 支持而 MySQL 不支持」的形态，
// 判断时按家族放行，不能只认 'PostgreSQL' 字面量。
const isPostgresLogicalFamilyDbType = (dbType) =>
  ['postgresql', 'kingbase', 'kingbasees', 'kingbase es'].includes(String(dbType || '').trim().toLowerCase())

const resolveRestoreAction = (record) => {
  if (!record) {
    return { enabled: false, reason: '请选择一条执行记录后再发起恢复。' }
  }
  if (record.status !== 'SUCCESS') {
    if (record.status === 'FAILED') return { enabled: false, reason: '本次执行失败，未生成可用备份集，无法恢复。' }
    if (record.status === 'STOPPED') return { enabled: false, reason: '本次执行已手动终止，不会生成可用备份集。' }
    if (record.status === 'STOP_REQUESTED') return { enabled: false, reason: record.stop_state_message || '本次执行仍在终止中，请待状态收口后再判断是否可恢复。' }
    if (record.status === 'PENDING') return { enabled: false, reason: '本次执行仍在排队，尚未生成备份集。' }
    return { enabled: false, reason: '本次执行尚未完成，暂时不能恢复。' }
  }
  if (!record.set_id) {
    return { enabled: false, reason: '本次执行尚未写入备份集记录，暂时不能恢复。' }
  }
  if (record.driver_type === 'PG_BACKREST') {
    return { enabled: true, reason: '' }
  }
  if (record.driver_type === 'XTRABACKUP') {
    return { enabled: false, reason: 'MySQL 物理备份恢复链路暂未在当前版本开放。' }
  }
  if (record.object_mode === 'ALL' && !isPostgresLogicalFamilyDbType(runTaskDbType.value)) {
    return { enabled: false, reason: '该备份集包含多个对象或整实例范围，当前暂不支持平台内一键恢复。' }
  }
  if (!isPostgresLogicalFamilyDbType(runTaskDbType.value) && isZipArtifact(record.file_name)) {
    return { enabled: false, reason: '该备份集是压缩归档形式，当前暂不支持平台内一键恢复。' }
  }
  return { enabled: true, reason: '' }
}

const runArtifactSummary = (record) => {
  if (!record) return '-'
  if (record.status === 'SUCCESS') {
    if (record.file_name) return `${record.file_name} / ${formatSize(record.size_bytes)}`
    return `产物已生成 / ${formatSize(record.size_bytes)}`
  }
  if (record.status === 'STOPPED') return '已手动终止，未生成可用备份集'
  if (record.status === 'STOP_REQUESTED') return record.stop_state_message || '正在终止当前备份进程'
  if (record.status === 'FAILED') return '未生成可用产物'
  if (record.status === 'PENDING') return '等待执行节点领取'
  return `当前阶段：${record.stage_label || '执行中'}`
}

const storageTargets = ref([])
const storageTargetMap = computed(() => {
  const map = new Map()
  storageTargets.value.forEach((t) => {
    map.set(t.id, t)
  })
  return map
})
const storageTargetDisplayName = (id) => {
  const sid = Number(id) || 0
  if (!sid) return ''
  const t = storageTargetMap.value.get(sid)
  if (!t) return ''
  const sub = String(t.type || '').toUpperCase() === 'LOCAL_SERVER'
    ? `${t.ssh_user || '-'}@${t.host || '-'}:${t.port || '-'}`
    : `${t.s3_endpoint || ''} / ${t.s3_bucket || ''}`
  return `${t.name}（${sub}）`
}

const loadStorageTargets = async () => {
  const res = await getStorageTargetsApi({ is_page: false }, { skipGlobalMessageError: true, silentHttpError: true }).catch(() => null)
  storageTargets.value = res?.data || []
}

const fetchTasks = async () => {
  uiState.loading = true
  try {
    const res = await getBackupTasksApi({ ...query })
    tableData.value = res?.data || []
    total.value = res?.total || 0
  } catch {
    // 两类错误都在这里收口，均不再向上抛：
    // 1) request.js 会在路由切换时统一 abort 在途请求，以 ERR_CANCELED 形态 reject——
    //    取消不是失败，不吞会冒成 unhandledrejection（控制台只显示 "Object"）。
    //    触发场景：点「查看全部任务」时 router.replace 与紧随其后的 handleSearch 撞在一起。
    // 2) 真实失败的用户提示已由响应拦截器统一给出，这里只负责别污染控制台。
  } finally {
    uiState.loading = false
  }
}

let runPollTimer = null

const scheduleRunPolling = () => {
  if (runPollTimer) {
    clearTimeout(runPollTimer)
    runPollTimer = null
  }
  const hasRunning = tableData.value.some((item) => item.task_status === 'RUNNING')
  const drawerHasTransientRun = runUi.open && (runUi.runs || []).some((r) => ['PENDING', 'RUNNING', 'STOP_REQUESTED'].includes(r.status))
  if (!hasRunning && !drawerHasTransientRun) return
  runPollTimer = setTimeout(async () => {
    await fetchTasks()
    if (runUi.open && runUi.task?.id) {
      await fetchTaskRuns(runUi.task.id, false)
    }
    scheduleRunPolling()
  }, 4000)
}

const handleSearch = () => {
  query.page = 1
  clearSelection()
  fetchTasks()
}

const handleTableChange = (pager) => {
  query.page = pager.current
  query.page_size = pager.pageSize
  clearSelection()
  fetchTasks()
}

const openCreate = () => {
  router.push('/backup/task/create')
}

const openEdit = (record) => {
  router.push(`/backup/task/${record.id}/edit`)
}

const openObjectViewer = () => {
  objectViewerUi.open = true
  objectViewerUi.expandedKeys = collectObjectTreeExpandedKeys(objectViewerTreeData.value)
}

const closeObjectViewer = () => {
  objectViewerUi.open = false
  objectViewerUi.expandedKeys = []
}

const handleEditBackupObjects = () => {
  const taskId = String(runUi.task?.id || runUi.taskDetail?.id || '').trim()
  if (!taskId) return
  closeObjectViewer()
  router.push({
    path: `/backup/task/${taskId}/edit`,
    query: { step: '1' },
  })
}

const retentionLabel = (value) => {
  const days = Number(value || 0)
  if (days === 7) return '7 天'
  if (days === 30) return '30 天'
  if (days === 60) return '60 天'
  if (days === 90) return '90 天'
  if (days === 180) return '半年'
  if (days === 365) return '1 年'
  return days > 0 ? `${days} 天` : '-'
}

const buildTaskUpdatePayload = (detail, overrides = {}) => {
  const base = detail || {}
  return {
    id: base.id,
    name: base.name,
    source_instance_id: base.source_instance_id,
    storage_target_id: base.storage_target_id,
    executor_node_id: base.executor_node_id,
    repository_node_id: base.repository_node_id,
    driver_type: base.driver_type,
    backup_type: base.backup_type,
    object_mode: base.object_mode,
    object_spec: base.object_spec,
    output_dir: base.output_dir,
    schedule_days: base.schedule_days,
    schedule_time: base.schedule_time,
    retention_days: base.retention_days,
    enabled: base.enabled,
    ...overrides,
  }
}

const openRetentionEdit = () => {
  retentionUi.value = currentRetentionDays.value
  retentionUi.editing = true
}

const cancelRetentionEdit = () => {
  if (retentionUi.saving) return
  retentionUi.editing = false
}

const handleUpdateRetentionDays = async (value) => {
  const task = runUi.taskDetail || runUi.task
  const days = Number(value || 0)
  if (!task?.id || !days) {
    cancelRetentionEdit()
    return
  }
  if (days === Number(currentRetentionDays.value || 0)) {
    cancelRetentionEdit()
    return
  }
  retentionUi.saving = true
  try {
    await updateBackupTaskWithRetentionConfirm(
      updateBackupTaskApi,
      buildTaskUpdatePayload(task, { retention_days: days }),
    )
    if (runUi.taskDetail) runUi.taskDetail.retention_days = days
    if (runUi.task) runUi.task.retention_days = days
    const matched = tableData.value.find((item) => item.id === task.id)
    if (matched) matched.retention_days = days
    message.success('保存时间已更新')
    retentionUi.editing = false
  } catch (err) {
    // 用户在保留期影响确认框点了取消：保存未提交，保持编辑态让用户重新决定
    if (isRetentionReconcileCanceled(err)) return
    throw err
  } finally {
    retentionUi.saving = false
  }
}

const openScheduleEdit = () => {
  scheduleUi.days = Array.isArray(runUi.taskDetail?.schedule_days || runUi.task?.schedule_days)
    ? [...(runUi.taskDetail?.schedule_days || runUi.task?.schedule_days)]
    : []
  scheduleUi.time = String(runUi.taskDetail?.schedule_time || runUi.task?.schedule_time || '01:00')
  const [hour = '01', minute = '00'] = scheduleUi.time.split(':')
  scheduleUi.hour = String(hour).padStart(2, '0')
  scheduleUi.minute = scheduleMinuteOptions.some((item) => item.value === String(minute).padStart(2, '0'))
    ? String(minute).padStart(2, '0')
    : '00'
  scheduleUi.time = `${scheduleUi.hour}:${scheduleUi.minute}`
  scheduleUi.open = true
}

const closeScheduleEdit = () => {
  if (scheduleUi.saving) return
  scheduleUi.open = false
}

const handleSchedulePopoverOpenChange = (open) => {
  if (open) {
    openScheduleEdit()
    return
  }
  closeScheduleEdit()
}

const toggleScheduleDay = (day, checked) => {
  const current = new Set(scheduleUi.days)
  if (checked) current.add(day)
  else current.delete(day)
  scheduleUi.days = weekdayOptions.map((item) => item.value).filter((value) => current.has(value))
}

const saveScheduleEdit = async () => {
  const task = runUi.taskDetail || runUi.task
  if (!task?.id) {
    closeScheduleEdit()
    return
  }
  if (!Array.isArray(scheduleUi.days) || scheduleUi.days.length === 0) {
    message.error('请至少选择一个调度日')
    return
  }
  const nextTime = `${String(scheduleUi.hour || '01').padStart(2, '0')}:${String(scheduleUi.minute || '00').padStart(2, '0')}`
  if (!nextTime) {
    message.error('请选择执行时间')
    return
  }
  const currentDays = Array.isArray(task.schedule_days) ? task.schedule_days : []
  const unchanged =
    currentDays.join(',') === scheduleUi.days.join(',') &&
    String(task.schedule_time || '') === nextTime
  if (unchanged) {
    closeScheduleEdit()
    return
  }
  scheduleUi.saving = true
  try {
    await updateBackupTaskWithRetentionConfirm(
      updateBackupTaskApi,
      buildTaskUpdatePayload(task, {
        schedule_days: [...scheduleUi.days],
        schedule_time: nextTime,
      }),
    )
    if (runUi.taskDetail) {
      runUi.taskDetail.schedule_days = [...scheduleUi.days]
      runUi.taskDetail.schedule_time = nextTime
    }
    if (runUi.task) {
      runUi.task.schedule_days = [...scheduleUi.days]
      runUi.task.schedule_time = nextTime
    }
    const matched = tableData.value.find((item) => item.id === task.id)
    if (matched) {
      matched.schedule_days = [...scheduleUi.days]
      matched.schedule_time = nextTime
    }
    message.success('调度周期已更新')
    scheduleUi.open = false
  } catch (err) {
    // 用户在保留期影响确认框点了取消：保存未提交，保持弹窗可继续修改
    if (isRetentionReconcileCanceled(err)) return
    throw err
  } finally {
    scheduleUi.saving = false
  }
}

const refreshTaskAndRuns = async (taskId) => {
  await fetchTasks()
  if (runUi.open && runUi.task?.id === taskId) {
    const latestTask = tableData.value.find((item) => item.id === taskId)
    if (latestTask) {
      runUi.task = latestTask
    }
    await Promise.all([fetchTaskDetail(taskId), fetchTaskRuns(taskId, false)])
  }
}

const handleToggleEnabled = async (record, checked) => {
  await updateBackupTaskApi({
    id: record.id,
    name: record.name,
    source_instance_id: record.source_instance_id,
    storage_target_id: record.storage_target_id,
    executor_node_id: record.executor_node_id,
    object_mode: record.object_mode,
    object_spec: record.object_spec,
    output_dir: record.output_dir,
    enabled: !!checked,
  })
  if (record) {
    record.enabled = !!checked
  }
  message.success(checked ? '已恢复任务调度，后续会按计划自动执行' : '已暂停任务调度，后续不再按计划自动执行')
  await refreshTaskAndRuns(record.id)
}

const handleToggleSchedule = async (record, checked) => {
  if (!record?.id) return
  await handleToggleEnabled(record, checked)
}

const resolveStorageTargetID = (record) => {
  const v = Number(record?.storage_target_id ?? record?.storageTargetId ?? 0) || 0
  return v > 0 ? v : 0
}

const storageTargetName = (record) => {
  const sid = resolveStorageTargetID(record)
  if (!sid) return ''
  const t = storageTargetMap.value.get(sid)
  if (!t) return `ID ${sid}`
  return t.name || `ID ${sid}`
}

// 产物路径 / 日志路径统一展示为「地址 + 路径」的完整形态（拼接口径见 utils/storagePath.js）。
// 存储目标优先从路径自带的 st:<id>:// 解析，退化到执行记录、任务详情、任务上的 storage_target_id。
const fullStoragePath = (value, record) => {
  const sid =
    extractStorageTargetId(value) ||
    resolveStorageTargetID(record) ||
    resolveStorageTargetID(runUi.taskDetail) ||
    resolveStorageTargetID(runUi.task)
  return formatStoragePath(value, storageTargetMap.value.get(sid) || null)
}

const handleRun = async (record) => {
  if (!resolveStorageTargetID(record)) {
    message.error('该任务未绑定存储目标：请点击「编辑」任务，重新选择存储目标后再执行备份')
    return
  }
  await runBackupTaskApi(record.id)
  // 物理备份手动触发一律走全量，这里把预期直接告诉用户，避免与定时执行的增量产生困惑
  if (isPhysicalDriver(record?.driver_type)) {
    message.success('已提交备份任务执行：物理备份手动触发将强制执行全量备份')
  } else {
    message.success('已提交备份任务执行')
  }
  await refreshTaskAndRuns(record.id)
  scheduleRunPolling()
}

const stopRunRequestInFlight = ref(false)

const handlePauseCurrentRun = (record) => {
  if (isPhysicalDriver(record?.driver_type)) {
    message.warning('当前物理备份暂不支持断点续跑。你可以终止本次备份，或暂停该任务的后续调度。')
    return
  }
  message.warning('当前逻辑备份不支持断点续跑。你可以终止本次备份，或暂停该任务的后续调度。')
}

const buildStopConfirmConfig = (record) => {
  if (record?.status === 'PENDING') {
    return {
      title: '终止备份',
      content: '当前任务仍在排队，确认后会直接终止，不会真正启动备份进程。是否继续？',
      okText: '确认终止',
      okType: 'danger',
    }
  }
  if (record?.status === 'STOP_REQUESTED') {
    return {
      title: record.stop_state_label || '终止备份',
      content: record.stop_state_message || '当前已收到终止请求，请稍后刷新查看结果。',
      okText: '刷新状态',
      okType: record.stop_state === 'UNABLE' ? 'default' : 'primary',
      alreadyRequested: true,
    }
  }
  if (record?.stop_state === 'UNABLE') {
    return {
      title: record.stop_state_label || '终止备份',
      content: `${record.stop_state_message || '当前可能无法真正终止。'}${record.stop_state_hint ? `\n\n${record.stop_state_hint}` : ''}\n\n是否仍发送终止请求？`,
      okText: '继续发送终止请求',
      okType: 'danger',
    }
  }
  if (record?.stop_state === 'UNINTERRUPTIBLE') {
    return {
      title: record.stop_state_label || '终止备份',
      content: `${record.stop_state_message || '当前阶段暂时不能安全中断。'}${record.stop_state_hint ? `\n\n${record.stop_state_hint}` : ''}\n\n是否继续发送终止请求？`,
      okText: '继续发送终止请求',
      okType: 'danger',
    }
  }
  return {
    title: '终止备份',
    content: '终止后会立即中止当前备份，本次不会生成可用备份集。是否继续？',
    okText: '确认终止',
    okType: 'danger',
  }
}

const handleStopRun = (record) => {
  if (!record?.id || stopRunRequestInFlight.value) return
  const stopConfig = buildStopConfirmConfig(record)
  const confirmed = Modal.confirm({
    ...stopConfig,
    title: stopConfig.title,
    content: stopConfig.content,
    okText: stopConfig.okText,
    okType: stopConfig.okType,
    cancelText: '取消',
    async onOk() {
      if (stopConfig.alreadyRequested) {
        try { await refreshTaskAndRuns(runUi.task?.id || record.task_id || '') } catch (_) {}
        try { scheduleRunPolling() } catch (_) {}
        try { scheduleLogPolling() } catch (_) {}
        try { confirmed.destroy?.() } catch (_) {}
        try { Modal.destroyAll() } catch (_) {}
        return
      }
      stopRunRequestInFlight.value = true
      try {
        const res = await stopBackupRunApi(record.id)
        const result = res?.data || {}
        if (result.status === 'STOPPED') {
          try { message.success(result.stop_state_message || '已终止当前备份') } catch (_) {}
        } else if (result.stop_state === 'UNABLE') {
          try { message.warning(result.stop_state_message || '终止请求已记录，但当前可能无法真正终止') } catch (_) {}
        } else if (result.stop_state === 'UNINTERRUPTIBLE') {
          try { message.warning(result.stop_state_message || '已记录终止请求，当前阶段不可立即中断') } catch (_) {}
        } else {
          try { message.success(result.stop_state_message || (record.status === 'PENDING' ? '已终止排队中的备份任务' : '已发送终止请求，正在终止当前备份')) } catch (_) {}
        }
        try { await refreshTaskAndRuns(runUi.task?.id || record.task_id || '') } catch (_) {}
        try { scheduleRunPolling() } catch (_) {}
        try { scheduleLogPolling() } catch (_) {}
      } catch (err) {
        stopRunRequestInFlight.value = false
        try { message.error(err?.msg || err?.message || '终止失败，请稍后重试') } catch (_) {}
        try { confirmed.destroy?.() } catch (_) {}
        try { Modal.destroyAll() } catch (_) {}
        return Promise.reject(err)
      } finally {
        stopRunRequestInFlight.value = false
      }
      try { confirmed.destroy?.() } catch (_) {}
      try { Modal.destroyAll() } catch (_) {}
    },
  })
}

const deleteUi = reactive({
  open: false,
  loading: false,
  taskId: null,
  taskName: '',
  confirmName: '',
})

const openDeleteModal = (record) => {
  deleteUi.open = true
  deleteUi.loading = false
  deleteUi.taskId = record?.id || null
  deleteUi.taskName = record?.name || ''
  deleteUi.confirmName = ''
}

const closeDeleteModal = () => {
  deleteUi.open = false
  deleteUi.loading = false
  deleteUi.taskId = null
  deleteUi.taskName = ''
  deleteUi.confirmName = ''
}

const handleDeleteTask = async () => {
  if (!deleteUi.taskId) return
  if ((deleteUi.confirmName || '').trim() !== (deleteUi.taskName || '').trim()) {
    message.error('任务名称输入不正确')
    return
  }
  deleteUi.loading = true
  try {
    await deleteBackupTaskApi(deleteUi.taskId, { confirm_name: deleteUi.confirmName.trim() })
    message.success('删除成功')
    if (runUi.task?.id === deleteUi.taskId) {
      closeRunsDrawer()
    }
    closeDeleteModal()
    await fetchTasks()
  } catch (error) {
    const messageText = error?.msg || error?.message || ''
    if (messageText.includes('执行中的备份记录')) {
      message.error('当前仍有正在排队或执行中的备份，无法删除；历史残留的终止中记录系统会自动收口。')
      return
    }
    throw error
  } finally {
    deleteUi.loading = false
  }
}

// ---------------- 批量删除 ----------------
const batchDeleteUi = reactive({
  open: false,
  loading: false,
  confirmed: false,
})

const openBatchDeleteModal = () => {
  if (!selectedRowKeys.value.length) {
    message.warning('请先勾选需要删除的备份任务')
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
    title: '部分备份任务删除失败',
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
      await deleteBackupTaskApi(record.id, { confirm_name: record.name || '' })
      successCount += 1
      if (runUi.task?.id === record.id) {
        closeRunsDrawer()
      }
    } catch (error) {
      const reason = error?.msg || error?.message || '删除失败'
      failures.push(`${record.name || '未命名任务'}（${record.id}）：${reason}`)
    }
  }
  batchDeleteUi.loading = false
  closeBatchDeleteModal()
  clearSelection()
  await fetchTasks()
  if (!failures.length) {
    message.success(`批量删除成功，共删除 ${successCount} 个备份任务`)
    return
  }
  if (successCount === 0) {
    message.error(`批量删除失败，${failures.length} 个备份任务均未删除`)
  } else {
    message.warning(`删除完成：成功 ${successCount} 个，失败 ${failures.length} 个`)
  }
  showBatchDeleteFailures(failures)
}

const runUi = reactive({
  open: false,
  loading: false,
  task: null,
  taskDetail: null,
  runs: [],
  selectedRunId: null,
  activeTab: 'overview',
  v2Tab: 'records',
  headerMoreOpen: false,
  expandTabMap: {},
  expandKeyMap: {},
  expandedRowKeys: [],
  logCache: {},
  pagination: {
    page: 1,
    pageSize: 10,
  },
})

const selectedRun = computed(() => runUi.runs.find((item) => item.id === runUi.selectedRunId) || null)
const latestRun = computed(() => runUi.runs[0] || null)
const recentSuccessRun = computed(() => runUi.runs.find((item) => item.status === 'SUCCESS') || null)
const recentFailedRun = computed(() => runUi.runs.find((item) => item.status === 'FAILED') || null)
const selectedRunCanStop = computed(() => ['PENDING', 'RUNNING', 'STOP_REQUESTED'].includes(selectedRun.value?.status || ''))
const selectedRunCanPausePrompt = computed(() => ['RUNNING', 'STOP_REQUESTED'].includes(selectedRun.value?.status || ''))
const runTaskName = computed(() => runUi.task?.name || '未命名任务')
const runTaskInstance = computed(() => {
  if (!runUi.task) return '-'
  return `${runUi.task.instance_host || '-'}:${runUi.task.instance_port || '-'}`
})
const runTaskDbType = computed(() => runUi.task?.db_type || runUi.taskDetail?.db_type || '-')
const topTaskStatusTone = computed(() => {
  const status = realTaskStatus(runUi.task)
  if (status === 'SUCCESS') return 'success'
  if (status === 'FAILED') return 'failed'
  if (status === 'RUNNING') return 'running'
  if (status === 'STOPPED') return 'stopped'
  return 'default'
})
const runTaskBackupMode = computed(() => {
  const detail = runUi.taskDetail || runUi.task || {}
  return driverTypeLabel(detail.driver_type || latestRun.value?.driver_type)
})
const recentSuccessTime = computed(() => formatDateTime(recentSuccessRun.value?.end_at || recentSuccessRun.value?.start_at))
const taskExecutorLabel = computed(() => {
  const driverType = runUi.taskDetail?.driver_type || latestRun.value?.driver_type
  return isPhysicalDriver(driverType) ? '执行位置' : '执行节点'
})
const taskExecutorDisplay = computed(() => executorDisplayLabel({ ...(runUi.taskDetail || {}), ...(runUi.task || {}), driver_type: runUi.taskDetail?.driver_type || latestRun.value?.driver_type }))
// 顶部摘要区「存储目标」：仅展示存储目标名称（不再展示执行节点、IP、端口等细节）
const taskStorageTargetName = computed(() => {
  const id = resolveStorageTargetID(runUi.taskDetail) || resolveStorageTargetID(runUi.task)
  if (!id) return '-'
  return storageTargetName({ storage_target_id: id }) || '-'
})
const taskObjectRangeDisplay = computed(() => {
  const detail = runUi.taskDetail || runUi.task || {}
  return objectModeLabel(detail.object_mode, detail.driver_type || latestRun.value?.driver_type)
})
const taskObjectSummary = computed(() => {
  const detail = runUi.taskDetail || runUi.task || {}
  const modeLabel = objectModeLabel(detail.object_mode, detail.driver_type || latestRun.value?.driver_type)
  const objectSummary = String(detail.object_summary || '').trim()
  if (!objectSummary) return modeLabel
  const normalizedSummary = objectSummary.replace(/\s+/g, '')
  const normalizedMode = String(modeLabel || '').replace(/\s+/g, '')
  if (normalizedSummary === normalizedMode) return modeLabel
  if (modeLabel === '自定义对象' && ['自定义', '自定义对象', '自定义对象/自定义', '自定义/自定义对象'].includes(normalizedSummary)) return modeLabel
  if (modeLabel === '全部实例' && normalizedSummary === '全部实例') return modeLabel
  return `${modeLabel} / ${objectSummary}`
})
const currentRetentionDays = computed(() => Number(runUi.taskDetail?.retention_days || runUi.task?.retention_days || 0))
const currentRetentionLabel = computed(() => retentionLabel(currentRetentionDays.value))
const currentScheduleSummary = computed(() => scheduleSummary(runUi.taskDetail?.schedule_days || runUi.task?.schedule_days, runUi.taskDetail?.schedule_time || runUi.task?.schedule_time))
const objectViewerTreeData = computed(() => buildObjectViewerTreeData(runUi.taskDetail || runUi.task || {}))
const latestRunRestoreAction = computed(() => resolveRestoreAction(latestRun.value))
const selectedRunRestoreAction = computed(() => resolveRestoreAction(selectedRun.value))
const filteredRuns = computed(() => (Array.isArray(runUi.runs) ? [...runUi.runs] : []))
const successfulRunsWithSet = computed(() => filteredRuns.value.filter((item) => item.status === 'SUCCESS' && item.set_id))
const highlightedLogContent = computed(() => logUi.content || '暂无日志内容')

const buildObjectViewerTreeData = (detail) => {
  const driverType = String(detail?.driver_type || latestRun.value?.driver_type || '').toUpperCase()
  const mode = String(detail?.object_mode || '').toUpperCase()
  const dbType = String(detail?.db_type || runTaskDbType.value || '').trim()
  const objectSpec = detail?.object_spec
  if (isPhysicalDriver(driverType)) {
    return [{ key: 'instance:all', title: '整实例', isLeaf: true, icon: renderObjectTreeIcon('database') }]
  }
  if (mode === 'ALL') {
    return [{ key: 'instance:all', title: '全部实例', isLeaf: true, icon: renderObjectTreeIcon('database') }]
  }
  const databases = Array.isArray(objectSpec?.databases) ? objectSpec.databases : []
  if (!databases.length) {
    const summary = String(detail?.object_summary || '').trim()
    return summary ? [{ key: 'summary:fallback', title: summary, isLeaf: true, icon: renderObjectTreeIcon('database') }] : []
  }
  const children = dbType === 'MySQL' ? buildMySQLObjectTree(databases) : buildPostgresObjectTree(databases)
  return children.length
    ? [{ key: 'root:databases', title: '数据库', selectable: false, icon: renderObjectTreeIcon('database'), children }]
    : []
}

const renderObjectTreeIcon = (kind) =>
  h(
    'span',
    { class: 'object-tree-icon-prefix' },
    [
      kind === 'schema'
        ? h(PartitionOutlined)
        : kind === 'table'
          ? h(TableOutlined)
          : h(DatabaseOutlined),
    ],
  )

const buildMySQLObjectTree = (databases) =>
  databases
    .filter((db) => db?.name)
    .map((db) => {
      const databaseName = String(db.name).trim()
      const tables = Array.isArray(db.tables) ? db.tables.filter(Boolean) : []
      if (!tables.length) {
        return { key: `db:${databaseName}`, title: `${databaseName}(全部)`, isLeaf: true, icon: renderObjectTreeIcon('database') }
      }
      return {
        key: `db:${databaseName}`,
        title: databaseName,
        selectable: false,
        icon: renderObjectTreeIcon('database'),
        children: [
          {
            key: `db:${databaseName}:tables`,
            title: 'Tables',
            selectable: false,
            icon: renderObjectTreeIcon('table'),
            children: tables.map((table) => ({
              key: `db:${databaseName}/table:${table}`,
              title: String(table),
              isLeaf: true,
              icon: renderObjectTreeIcon('table'),
            })),
          },
        ],
      }
    })

const buildPostgresObjectTree = (databases) =>
  databases
    .filter((db) => db?.name)
    .map((db) => {
      const databaseName = String(db.name).trim()
      const schemaNames = Array.isArray(db.schemas) ? db.schemas.filter(Boolean).map((item) => String(item)) : []
      const fullSchemaSet = new Set(schemaNames)
      const rawTables = Array.isArray(db.tables) ? db.tables : []
      if (!schemaNames.length && !rawTables.length) {
        return { key: `db:${databaseName}`, title: `${databaseName}(全部)`, isLeaf: true, icon: renderObjectTreeIcon('database') }
      }
      const schemaMap = new Map()
      schemaNames.forEach((schema) => schemaMap.set(schema, []))
      rawTables.forEach((table) => {
        if (table && typeof table === 'object') {
          const schemaName = String(table.schema || '').trim()
          const tableName = String(table.name || '').trim()
          if (!schemaName || !tableName) return
          if (!schemaMap.has(schemaName)) schemaMap.set(schemaName, [])
          schemaMap.get(schemaName).push(tableName)
          return
        }
        const tableName = String(table || '').trim()
        if (!tableName) return
        if (!schemaMap.has('public')) schemaMap.set('public', [])
        schemaMap.get('public').push(tableName)
      })
      return {
        key: `db:${databaseName}`,
        title: databaseName,
        selectable: false,
        icon: renderObjectTreeIcon('database'),
        children: Array.from(schemaMap.entries()).map(([schemaName, tables]) => ({
          key: `db:${databaseName}/schema:${schemaName}`,
          title: fullSchemaSet.has(schemaName) ? `${schemaName}(全部)` : schemaName,
          selectable: false,
          icon: renderObjectTreeIcon('schema'),
          children: tables.length
            ? tables.map((tableName) => ({
                key: `db:${databaseName}/schema:${schemaName}/table:${tableName}`,
                title: tableName,
                isLeaf: true,
                icon: renderObjectTreeIcon('table'),
              }))
            : undefined,
        })),
      }
    })

const collectObjectTreeExpandedKeys = (treeData, limit = 12) => {
  const keys = []
  const walk = (nodes) => {
    if (!Array.isArray(nodes) || keys.length >= limit) return
    nodes.forEach((node) => {
      if (!node || keys.length >= limit) return
      if (Array.isArray(node.children) && node.children.length) {
        keys.push(node.key)
        walk(node.children)
      }
    })
  }
  walk(treeData)
  return keys
}

const prepareStageLabel = (stage) => {
  if (!stage) return '-'
  const map = {
    load_task: '读取备份任务配置',
    load_instance: '加载源数据库实例',
    load_storage: '加载存储目标与仓库节点',
    load_nodes: '加载执行节点与仓库节点拓扑',
    ssh_trust_executor: '授权执行节点 SSH 公钥',
    ssh_trust_repo: '授权仓库节点 SSH 公钥',
    repo_access_remote: '仓库节点接入（SSH 互信/sshpass）',
    repo_check_tools: '仓库节点工具检查（pgBackRest 版本）',
    repo_check_repo: '仓库路径与权限检查',
    stanza_create: 'pgBackRest stanza-create 初始化',
    stanza_check: 'pgBackRest stanza-check 校验',
    archive_command_check: 'PG archive_command 配置校验',
    executor_prepare: '执行节点准备工作',
    node_preflight: '节点预检（磁盘/内存/SSH）',
    timeout: 'Prepare 超时',
    panic: '系统异常（Panic）',
    unknown: '未知 Prepare 阶段',
    task_missing: '关联任务不存在',
    instance_missing: '关联源实例不存在',
    storage_missing: '关联存储目标不存在',
    node_missing: '关联节点不存在',
  }
  return map[stage] || stage
}

const buildRunTimeline = (record) => {
  if (!record) return []
  const steps = [
    { key: 'queued', label: '等待执行节点领取', description: '任务已创建，等待节点开始执行。', matched: ['等待执行节点领取'] },
    { key: 'connect', label: '连接源库', description: '开始探测数据库版本并校验连通性。', matched: ['连接源库'] },
    { key: 'dump', label: '导出数据', description: '执行逻辑备份导出数据库对象。', matched: ['导出中', '导出完成'] },
    { key: 'archive', label: '归档产物', description: '将导出文件归档为最终备份产物。', matched: ['归档中'] },
    { key: 'checksum', label: '校验产物', description: '计算大小与 SHA256，确认产物完整性。', matched: ['校验中', '校验完成'] },
    { key: 'set', label: '写入备份集', description: '落库备份集记录，供恢复与下载使用。', matched: ['写入备份集'] },
    { key: 'done', label: '执行完成', description: '任务执行结束，等待页面刷新最终结果。', matched: ['执行完成'] },
  ]
  const currentIndex = (() => {
    if (record.status === 'FAILED' || record.status === 'STOPPED') {
      const idx = steps.findIndex((step) => step.matched.includes(record.stage_label))
      return idx >= 0 ? idx : steps.findIndex((step) => step.key === 'dump')
    }
    if (record.status === 'SUCCESS') return steps.length - 1
    const idx = steps.findIndex((step) => step.matched.includes(record.stage_label))
    return idx >= 0 ? idx : 0
  })()
  return steps.map((step, index) => {
    let state = 'pending'
    if (record.status === 'SUCCESS') {
      state = 'done'
    } else if (record.status === 'FAILED' || record.status === 'STOPPED') {
      if (index < currentIndex) state = 'done'
      else if (index === currentIndex) state = 'failed'
    } else if (index < currentIndex) {
      state = 'done'
    } else if (index === currentIndex) {
      state = 'active'
    }
    return { ...step, state }
  })
}

const buildRunAnalysis = (record, logContent) => {
  if (!record) {
    return {
      type: 'info',
      title: '暂无可分析的执行记录',
      summary: '请选择一条执行记录查看异常分析。',
      impact: '无',
      tips: ['可先从左侧选择一条执行记录。'],
    }
  }
  if (record.status === 'SUCCESS') {
    return {
      type: 'success',
      title: '本次备份执行成功',
      summary: '已经生成备份产物并写入备份集，当前重点是确认产物路径、大小和校验值。',
      impact: '本次执行可用于后续恢复。',
      tips: ['如需恢复，请直接进入备份集页面操作。', '建议保留本次日志，便于后续审计。'],
    }
  }
  if (record.status === 'PENDING') {
    return {
      type: 'warning',
      title: '任务正在等待执行节点领取',
      summary: '任务已经入队，但当前还没有进入实际导出阶段。',
      impact: '短时间内不会产生产物与完整日志。',
      tips: ['确认执行节点在线且 Agent 正常运行。', '如果排队时间过长，优先去检查节点状态。'],
    }
  }
  if (record.status === 'PENDING_PREPARE') {
    if (record.prepare_status && record.prepare_status === 'PREPARE_ERROR') {
      const tips = []
      if (record.prepare_suggestion) tips.push(record.prepare_suggestion)
      tips.push('修复根因后，重新点击「立即执行」触发新的运行记录。')
      tips.push('如需排查 SSH 连通性，优先检查执行节点与仓库节点之间的 SSH 互信或 ssh_password 凭据。')
      return {
        type: 'error',
        title: record.prepare_title || `备份环境准备失败（阶段：${prepareStageLabel(record.prepare_stage)}）`,
        summary: [
          `失败阶段：${prepareStageLabel(record.prepare_stage)}${record.prepare_stage ? `（${record.prepare_stage}）` : ''}`,
          record.prepare_detail ? `详细描述：${record.prepare_detail}` : '',
          record.prepare_raw_error ? `原始错误：${record.prepare_raw_error}` : '',
        ].filter((s) => s).join('；'),
        impact: '本次执行在备份环境准备阶段已失败，不会进入导出流程，也不会生成备份产物。',
        tips,
      }
    }
    return {
      type: 'info',
      title: '任务正在进行备份环境准备',
      summary: `当前 prepare 阶段：${prepareStageLabel(record.prepare_stage)}（${record.prepare_status || '待开始'}），正在完成节点接入、SSH 拓扑、仓库初始化等准备工作。`,
      impact: '准备成功后会自动晋升为 PENDING，再由执行节点领取；准备失败则直接结束本次执行。',
      tips: ['prepare 通常在 30-90 秒内完成，可点击刷新按钮查看最新状态。', '如长时间停留在 PENDING_PREPARE（>30 分钟），请检查后端服务与 nohup.out 日志。'],
    }
  }
  if (record.status === 'STOP_REQUESTED') {
    if (record.stop_state === 'UNABLE') {
      return {
        type: 'error',
        title: record.stop_state_label || '终止失败/无法终止',
        summary: record.stop_state_message || '终止请求已发出，但平台当前无法确认执行进程是否真正退出。',
        impact: '本次执行可能继续运行，也可能需要人工收口。',
        tips: [record.stop_state_hint || '请优先检查执行节点、Agent 日志和备份进程状态。', '必要时人工结束残留进程，并在平台中收口本次执行。'],
      }
    }
    if (record.stop_state === 'UNINTERRUPTIBLE') {
      return {
        type: 'warning',
        title: record.stop_state_label || '当前阶段不可立即中断',
        summary: record.stop_state_message || '系统已收到终止请求，但当前阶段暂时不能安全中断。',
        impact: '本次执行不会写入可用备份集，但需要等待当前阶段结束后再收口。',
        tips: [record.stop_state_hint || '请继续观察日志是否推进到阶段结束。', '如果长时间无变化，再检查节点和执行进程。'],
      }
    }
    return {
      type: 'warning',
      title: record.stop_state_label || '任务正在终止中',
      summary: record.stop_state_message || '服务端已收到终止请求，当前正在等待备份进程退出并收口状态。',
      impact: '本次执行不会生成可用备份集，请继续观察日志是否完成终止。',
      tips: [record.stop_state_hint || '如果日志仍在变化，说明进程正在有序退出。', '如长时间未结束，请检查执行节点上是否存在残留备份进程。'],
    }
  }
  if (record.status === 'STOPPED') {
    return {
      type: 'warning',
      title: '本次备份已手动终止',
      summary: record.error_message || '本次执行已被手动中止，平台不会把当前产物记为可恢复备份集。',
      impact: '会保留日志和运行目录，供后续排查，但不会生成可用备份集。',
      tips: ['如需继续备份，请重新执行该任务。', '若只是想暂停后续自动执行，可直接使用“暂停任务调度”。'],
    }
  }
  if (record.status === 'RUNNING') {
    return {
      type: 'info',
      title: '任务正在执行中',
      summary: `当前阶段：${record.stage_label || '执行中'}，建议结合实时日志观察是否持续推进。`,
      impact: '任务尚未结束，产物信息可能仍在变化。',
      tips: ['如果日志持续刷新，通常说明执行正常。', '如果长时间停留在同一阶段，请查看实时日志确认是否卡住。'],
    }
  }
  const merged = `${record.error_message || ''}\n${logContent || ''}`.toLowerCase()
  if (merged.includes('access denied') || merged.includes('authentication') || merged.includes('password')) {
    return {
      type: 'error',
      title: '失败原因偏向账号或权限问题',
      summary: '平台已收到认证/权限相关错误，通常发生在连接源库或执行导出命令阶段。',
      impact: '本次执行无法读取源库对象，因此不会生成可用产物。',
      tips: ['检查数据源账号密码是否已更新。', '确认账号具备 mysqldump / pg_dump 所需读取权限。', '修复后重新执行一次备份。'],
    }
  }
  if (merged.includes('connection refused') || merged.includes('timeout') || merged.includes('no route') || merged.includes('connect')) {
    return {
      type: 'error',
      title: '失败原因偏向网络连通性问题',
      summary: '执行节点在连接源库或上报阶段遇到网络异常，需要优先检查链路连通性。',
      impact: '任务无法顺利进入完整导出阶段。',
      tips: ['确认执行节点可以访问目标实例地址与端口。', '检查防火墙、安全组或 VPN 链路。', '如果节点离线，先修复 Agent。'],
    }
  }
  if (merged.includes('permission denied') || merged.includes('not writable') || merged.includes('read-only')) {
    return {
      type: 'error',
      title: '失败原因偏向节点目录权限不足',
      summary: '执行节点无法写入备份目录或日志目录，需要先修复目录权限。',
      impact: '即使导出开始，也无法生成或保存最终产物。',
      tips: ['检查备份目录和日志目录是否可写。', '确认 Agent 运行用户具备对应目录权限。', '必要时切换到新的备份目录。'],
    }
  }
  if (merged.includes('no such file') || merged.includes('not found') || merged.includes('mysqldump') || merged.includes('pg_dump')) {
    return {
      type: 'error',
      title: '失败原因偏向备份工具缺失或版本不匹配',
      summary: '执行节点未命中合适的备份工具，或工具目录不完整。',
      impact: '任务无法真正发起导出，因此不会生成有效备份文件。',
      tips: ['检查节点 tools 目录是否完整。', '确认已安装匹配数据库大版本的 mysqldump / pg_dump。', '修复工具后重新执行备份。'],
    }
  }
  return {
    type: 'error',
    title: '本次备份执行失败',
    summary: record.error_message || '平台已记录失败，但当前无法自动归类为明确的错误类型，请结合日志继续分析。',
    impact: '本次执行未生成可用产物。',
    tips: ['优先查看执行日志中的最后 50 行。', '确认失败阶段对应的节点、网络、权限和工具配置。', '修复后可直接重新执行该任务。'],
  }
}

const runAnalysis = computed(() => buildRunAnalysis(selectedRun.value, logUi.content))

const fetchTaskDetail = async (taskId) => {
  const res = await getBackupTaskDetailApi(taskId)
  runUi.taskDetail = res?.data || null
}

const syncSelectedRunLog = async (record, preferredTab) => {
  if (!record) {
    logUi.runId = null
    logUi.content = ''
    return
  }
  runUi.selectedRunId = record.id
  if (preferredTab) {
    runUi.activeTab = preferredTab
  } else if (record.status === 'FAILED' || record.status === 'STOPPED') {
    runUi.activeTab = 'analysis'
  } else if (record.status === 'SUCCESS') {
    runUi.activeTab = 'artifact'
  } else {
    runUi.activeTab = 'log'
  }
  logUi.runId = record.id
  await refreshRunLog(false)
  scheduleLogPolling()
}

const fetchTaskRuns = async (taskId, withLoading = true) => {
  if (withLoading) runUi.loading = true
  try {
    const res = await getBackupTaskRunsApi(taskId)
    runUi.runs = res?.data || []
    const maxPage = Math.max(1, Math.ceil(filteredRuns.value.length / Math.max(1, Number(runUi.pagination.pageSize || 10))))
    if (runUi.pagination.page > maxPage) {
      runUi.pagination.page = maxPage
    }
    const preferred = runUi.runs.find((item) => item.id === runUi.selectedRunId) || runUi.runs[0] || null
    await syncSelectedRunLog(preferred, runUi.selectedRunId ? runUi.activeTab : '')
  } finally {
    if (withLoading) runUi.loading = false
  }
}

const openRunsDrawer = async (record) => {
  runUi.open = true
  runUi.task = record
  runUi.taskDetail = null
  runUi.selectedRunId = null
  runUi.activeTab = 'overview'
  runUi.v2Tab = 'records'
  runUi.headerMoreOpen = false
  mixedStrategyExpanded.value = false
  runUi.expandTabMap = {}
  runUi.expandKeyMap = {}
  runUi.expandedRowKeys = []
  runUi.logCache = {}
  runUi.pagination = { page: 1, pageSize: 10 }
  await Promise.all([fetchTaskDetail(record.id), fetchTaskRuns(record.id)])
  scheduleRunPolling()
}

const closeRunsDrawer = () => {
  runUi.open = false
  runUi.task = null
  runUi.taskDetail = null
  runUi.runs = []
  runUi.selectedRunId = null
  runUi.activeTab = 'overview'
  runUi.v2Tab = 'records'
  runUi.headerMoreOpen = false
  runUi.expandTabMap = {}
  runUi.expandKeyMap = {}
  runUi.expandedRowKeys = []
  runUi.logCache = {}
  runUi.pagination = { page: 1, pageSize: 10 }
  closeLogModal()
}

const focusRun = async (record, preferredTab = '') => {
  if (!record) return
  await syncSelectedRunLog(record, preferredTab)
}

const paginatedRuns = computed(() => {
  const list = filteredRuns.value || []
  const { page, pageSize } = runUi.pagination
  const start = (page - 1) * pageSize
  return list.slice(start, start + pageSize)
})


const setExpandTab = (runId, tabKey) => {
  runUi.expandTabMap = { ...runUi.expandTabMap, [runId]: tabKey }
  if (tabKey === 'log') {
    const record = runUi.runs.find((r) => r.id === runId)
    if (record) refreshRunLogFor(record)
  }
}

const setExpandRowFocus = (record) => {
  if (!record) return
  const idx = runUi.expandedRowKeys.indexOf(record.id)
  if (idx >= 0) {
    runUi.expandedRowKeys.splice(idx, 1)
  } else {
    runUi.expandedRowKeys = [...runUi.expandedRowKeys, record.id]
    const preferred = record.status === 'SUCCESS' ? 'artifact' : record.status === 'FAILED' || record.status === 'STOPPED' ? 'analysis' : 'overview'
    setExpandTab(record.id, preferred)
  }
  syncSelectedRunLog(record)
}

const refreshRunLogFor = async (record) => {
  if (!record) return
  const prevRunId = logUi.runId
  const prevContent = logUi.content
  logUi.runId = record.id
  logUi.content = ''
  await refreshRunLog(false)
  runUi.logCache[record.id] = logUi.content || prevContent
  if (prevRunId && prevRunId !== record.id) {
    logUi.runId = prevRunId
    logUi.content = prevContent
  }
}

const getRunLogCache = (runId) => {
  if (runUi.selectedRunId === runId) return logUi.content || ''
  return runUi.logCache[runId] || ''
}

const logUi = reactive({
  open: false,
  loading: false,
  runId: null,
  status: '',
  executorNodeName: '',
  executorNodeDisplay: '',
  driverType: '',
  filePath: '',
  logPath: '',
  content: '',
  errorMessage: '',
  isRunning: false,
  logSizeBytes: 0,
  readMode: '',
  readModeLabel: '-',
})

let logPollTimer = null

const stopLogPolling = () => {
  if (logPollTimer) {
    clearTimeout(logPollTimer)
    logPollTimer = null
  }
}

const scheduleLogPolling = () => {
  stopLogPolling()
  if ((!runUi.open && !logUi.open) || !logUi.runId || !logUi.isRunning) return
  logPollTimer = setTimeout(async () => {
    await refreshRunLog(false)
    scheduleLogPolling()
  }, 2500)
}

const resolveLogReadModeLabel = (mode) => {
  if (mode === 'file_tail') return '执行日志文件'
  if (mode === 'db_excerpt') return '数据库摘要'
  return '-'
}

const refreshRunLog = async (withLoading = true) => {
  if (!logUi.runId) return
  if (withLoading) logUi.loading = true
  try {
    const res = await getBackupRunLogApi(logUi.runId, { tail_kb: 64 })
    const data = res?.data || {}
    logUi.status = data.status || ''
    logUi.executorNodeName = data.executor_node_name || ''
    logUi.filePath = data.file_path || ''
    logUi.logPath = data.log_path || ''
    logUi.content = data.log_excerpt || ''
    logUi.errorMessage = data.error_message || ''
    logUi.isRunning = !!data.is_running
    logUi.logSizeBytes = Number(data.log_size_bytes || 0)
    logUi.readMode = data.read_mode || ''
    logUi.readModeLabel = resolveLogReadModeLabel(logUi.readMode)
  } finally {
    if (withLoading) logUi.loading = false
  }
}

const openLogModal = async (record) => {
  if (record?.id) {
    await focusRun(record, 'log')
  }
  const run = selectedRun.value
  if (run) {
    logUi.driverType = run.driver_type || ''
    logUi.executorNodeDisplay = executorDisplayLabel(run)
    logUi.runId = run.id
  }
  logUi.open = true
  await refreshRunLog()
  scheduleLogPolling()
}

const closeLogModal = () => {
  logUi.open = false
  if (runUi.open && selectedRun.value) {
    scheduleLogPolling()
    return
  }
  stopLogPolling()
  logUi.runId = null
  logUi.status = ''
  logUi.executorNodeName = ''
  logUi.executorNodeDisplay = ''
  logUi.driverType = ''
  logUi.filePath = ''
  logUi.logPath = ''
  logUi.content = ''
  logUi.errorMessage = ''
  logUi.isRunning = false
  logUi.logSizeBytes = 0
  logUi.readMode = ''
  logUi.readModeLabel = '-'
}

const goBackupSets = (record) => {
  const setName = String(record?.set_name || record?.file_name || '').trim()
  router.push({
    path: '/backup/sets',
    query: setName ? { search: setName } : {},
  })
}

const goRestore = (record) => {
  const restoreAction = resolveRestoreAction(record)
  if (!restoreAction.enabled) {
    message.warning(restoreAction.reason || '当前执行暂不支持平台内恢复')
    return
  }
  if (record?.driver_type === 'PG_BACKREST') {
    router.push({
      path: '/backup/restore',
      query: {
        tab: 'physical',
        open: '1',
        backup_set_id: String(record.set_id),
        from_task_id: runUi.task?.id ? String(runUi.task.id) : '',
        from_run_id: record?.id ? String(record.id) : '',
      },
    })
    return
  }
  if (record?.driver_type === 'XTRABACKUP') {
    message.warning('MySQL 物理备份恢复链路暂未在当前版本开放。')
    return
  }
  router.push({
    path: '/backup/restore',
    query: {
      open: '1',
      backup_set_id: String(record.set_id),
      from_task_id: runUi.task?.id ? String(runUi.task.id) : '',
      from_run_id: record?.id ? String(record.id) : '',
    },
  })
}

const openAgentInstall = (record) => {
  if (!record?.id) return
  router.push({ path: `/backup/agent-install/${record.id}` })
}

const openStorageTargets = () => {
  router.push('/backup/storage_targets')
}

onMounted(async () => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(true)
  }
  query.search = String(route.query.search || '').trim()
  query.instance_id = Number(route.query.instance_id || 0) || 0
  await Promise.all([fetchTasks(), loadStorageTargets(), loadInstances()])
  await nextTick()
  updateTableWrapWidth()
  if (typeof ResizeObserver !== 'undefined' && tableWrapRef.value) {
    tableResizeObserver = new ResizeObserver(() => updateTableWrapWidth())
    tableResizeObserver.observe(tableWrapRef.value)
  }
  scheduleRunPolling()
})

// 本页 keepAlive，从「数据源管理」第二次跳进来时 mounted 不会再跑：
// 这里同步 URL 上的实例过滤条件，避免"点了别的数据源，页面还是上一次的过滤"。
onActivated(() => {
  const urlInstanceID = Number(route.query.instance_id || 0) || 0
  if (urlInstanceID === query.instance_id) return
  query.instance_id = urlInstanceID
  handleSearch()
})

let tableResizeObserver = null
onBeforeUnmount(() => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(false)
  }
  if (runPollTimer) {
    clearTimeout(runPollTimer)
    runPollTimer = null
  }
  stopLogPolling()
  tableResizeObserver?.disconnect?.()
  if (typeof document !== 'undefined') {
    stopColumnResize()
  }
  persistColumnWidths()
})
</script>

<style scoped>
.backup-task-page {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: var(--gi-spacing-ssm);
  box-sizing: border-box;
  overflow: hidden;
}

.backup-task-page > .ant-card {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.backup-task-page > .ant-card > :deep(.ant-card-body) {
  flex: 1 1 auto;
  min-height: 0;
  padding: 16px 20px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* 页面主标题：统一使用公共样式 .gi-page-title（assets/components/patterns/pagePatterns.scss），
   此处不再重复定义，避免与【数据恢复】【备份集】两页漂移。 */

.search-input {
  width: 280px;
  max-width: 100%;
}

/* 跳转上下文提示条：与表格留出间距，且不参与纵向滚动区域的高度计算 */
.backup-jump-context {
  margin-bottom: 12px;
  flex: 0 0 auto;
}

.backup-task-table-wrap {
  width: 100%;
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.backup-task-table-wrap :deep(.ant-table-wrapper),
.backup-task-table-wrap :deep(.ant-spin-nested-loading),
.backup-task-table-wrap :deep(.ant-spin-container) {
  display: flex;
  flex-direction: column;
  min-height: 0;
  flex: 1 1 auto;
}

.backup-task-table-wrap :deep(.ant-spin-container) {
  overflow: hidden;
}

.backup-task-table-wrap :deep(.ant-spin-container > .ant-table) {
  min-height: 0;
  flex: 1 1 auto;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.backup-task-table-wrap :deep(.ant-spin-container > .ant-pagination),
.backup-task-table-wrap :deep(.ant-spin-container > .ant-table + .ant-pagination),
.backup-task-table-wrap :deep(.ant-table-wrapper .ant-pagination) {
  flex: 0 0 auto;
  /* 去掉分页上方的分隔横线与多余留白,紧贴表体底部滚动条 */
  margin: 6px 0 0 0;
  padding-top: 0;
  border-top: none;
  align-items: center;
  row-gap: 0;
}

/* 表体自适应填充剩余高度:消除表体底部与分页之间的空白 */
.backup-task-table-wrap :deep(.ant-spin-container > .ant-table > .ant-table-container) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.backup-task-table-wrap :deep(.ant-table-container > .ant-table-header) {
  flex: 0 0 auto;
}

.backup-task-table-wrap :deep(.ant-table-container > .ant-table-body),
.backup-task-table-wrap :deep(.ant-table-container > .ant-table-content) {
  flex: 1 1 auto;
  min-height: 0;
  height: auto !important;
  max-height: none !important;
  overflow: auto;
}

/* 分页控件样式统一:页码按钮 / 跳页输入框 / 每页条数下拉框 高度一致(32px) */
.backup-task-table-wrap :deep(.ant-pagination .ant-pagination-item),
.backup-task-table-wrap :deep(.ant-pagination .ant-pagination-prev),
.backup-task-table-wrap :deep(.ant-pagination .ant-pagination-next),
.backup-task-table-wrap :deep(.ant-pagination .ant-pagination-jump-prev),
.backup-task-table-wrap :deep(.ant-pagination .ant-pagination-jump-next) {
  min-width: 32px;
  height: 32px;
  line-height: 30px;
  margin-inline-end: 6px;
}

.backup-task-table-wrap :deep(.ant-pagination .ant-pagination-item) {
  border-radius: 6px;
  border: 1px solid #d9e2ea;
  background: #fff;
  font-size: 14px;
}

.backup-task-table-wrap :deep(.ant-pagination .ant-pagination-item a) {
  line-height: 30px;
}

.backup-task-table-wrap :deep(.ant-pagination .ant-pagination-item-active) {
  border-color: var(--gi-color-primary, #0f766e);
  background: rgba(15, 118, 110, 0.06);
}

.backup-task-table-wrap :deep(.ant-pagination .ant-pagination-item-active a) {
  color: var(--gi-color-primary, #0f766e);
  font-weight: 600;
}

.backup-task-table-wrap :deep(.ant-pagination .ant-pagination-total-text) {
  height: 32px;
  line-height: 32px;
  font-size: 14px;
  color: rgba(0, 0, 0, 0.72);
  margin-inline-end: 8px;
}

.backup-task-table-wrap :deep(.ant-pagination .ant-pagination-options) {
  display: inline-flex;
  align-items: center;
  height: 32px;
  margin-inline-start: 10px;
}

/* 跳页数字输入框 */
.backup-task-table-wrap
  :deep(.ant-pagination .ant-pagination-options-quick-jumper)
  input {
  height: 32px;
  min-height: 32px;
  padding: 0 8px;
  border: 1px solid #d9e2ea;
  border-radius: 6px;
  font-size: 14px;
  line-height: 30px;
}

.backup-task-table-wrap
  :deep(.ant-pagination .ant-pagination-options-quick-jumper)
  input:hover,
.backup-task-table-wrap
  :deep(.ant-pagination .ant-pagination-options-quick-jumper)
  input:focus {
  border-color: var(--gi-color-primary, #0f766e);
  box-shadow: 0 0 0 2px rgba(15, 118, 110, 0.12);
}

/* 每页条数下拉框:与左侧数字输入框等高、同宽高比 */
.backup-task-table-wrap :deep(.ant-pagination .ant-pagination-options-size-changer.ant-select) {
  min-width: 96px;
}

.backup-task-table-wrap
  :deep(.ant-pagination .ant-pagination-options-size-changer.ant-select .ant-select-selector) {
  height: 32px !important;
  min-height: 32px;
  padding: 0 28px 0 11px !important;
  border: 1px solid #d9e2ea;
  border-radius: 6px;
  background: #fff;
  font-size: 14px;
  box-shadow: none;
  transition:
    border-color 0.2s,
    box-shadow 0.2s;
}

.backup-task-table-wrap
  :deep(.ant-pagination .ant-pagination-options-size-changer.ant-select .ant-select-selector:hover) {
  border-color: var(--gi-color-primary, #0f766e);
  box-shadow: 0 0 0 2px rgba(15, 118, 110, 0.1);
}

.backup-task-table-wrap
  :deep(.ant-pagination .ant-pagination-options-size-changer.ant-select.ant-select-focused .ant-select-selector),
.backup-task-table-wrap
  :deep(.ant-pagination .ant-pagination-options-size-changer.ant-select.ant-select-open .ant-select-selector) {
  border-color: var(--gi-color-primary, #0f766e);
  box-shadow: 0 0 0 2px rgba(15, 118, 110, 0.18);
}

.backup-task-table-wrap
  :deep(.ant-pagination .ant-pagination-options-size-changer.ant-select .ant-select-selection-item) {
  line-height: 30px;
  padding-inline-end: 0;
  font-size: 14px;
  font-weight: 500;
}

.backup-task-table-wrap
  :deep(.ant-pagination .ant-pagination-options-size-changer.ant-select .ant-select-arrow) {
  color: rgba(0, 0, 0, 0.45);
  right: 10px;
  top: 50%;
  transform: translateY(-50%);
}

/* 批量选择反馈:选中行高亮 + 已选提示条 */
.backup-task-page .backup-task-selection-bar {
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

.backup-task-page .backup-task-selection-text strong {
  color: var(--gi-color-primary, #0f766e);
  font-size: 14px;
  padding: 0 2px;
}

.backup-task-table :deep(.ant-table-tbody > tr.ant-table-row-selected > td) {
  background: rgba(15, 118, 110, 0.07);
}

.backup-task-table :deep(.ant-table-tbody > tr.ant-table-row-selected:hover > td) {
  background: rgba(15, 118, 110, 0.12);
}

.backup-task-table :deep(.ant-table-tbody > tr.ant-table-row-selected > td:first-child) {
  box-shadow: inset 3px 0 0 var(--gi-color-primary, #0f766e);
}

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

.backup-task-table-wrap :deep(.ant-table) {
  min-height: 0;
}

.backup-task-table-wrap :deep(.ant-table-container) {
  min-height: 0;
}

.backup-task-page :deep(.gi-page-toolbar) {
  flex: 0 0 auto;
}

.muted {
  margin-top: 2px;
  font-size: 12px;
  color: #9aa6ae;
}

.node-modal-layout {
  display: grid;
  grid-template-columns: 300px minmax(0, 1fr);
  gap: 16px;
  min-height: 640px;
}

.node-sidebar,
.node-editor {
  min-width: 0;
}

.node-sidebar {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px;
  background: #fafcff;
  border: 1px solid #edf2f7;
  border-radius: 16px;
}

.node-sidebar-header,
.node-editor-header,
.node-credential-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.node-sidebar-title,
.node-editor-title,
.node-credential-title {
  font-size: 15px;
  font-weight: 600;
  color: #1f2d38;
}

.node-sidebar-subtitle,
.node-editor-subtitle,
.node-credential-subtitle {
  margin-top: 4px;
  font-size: 12px;
  line-height: 1.6;
  color: #71818c;
}

.node-card-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  overflow: auto;
}

.node-card {
  width: 100%;
  padding: 14px;
  border: 1px solid #e8edf2;
  border-radius: 14px;
  background: #fff;
  text-align: left;
  cursor: pointer;
  transition: all 0.2s ease;
}

.node-card:hover {
  border-color: #b7d8ff;
  box-shadow: 0 8px 20px rgba(24, 144, 255, 0.08);
}

.node-card.active {
  border-color: #91caff;
  background: #f0f8ff;
}

.node-card-top,
.node-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.node-card-name {
  font-size: 14px;
  font-weight: 600;
  color: #1f2d38;
}

.node-card-meta {
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.6;
  color: #71818c;
  word-break: break-word;
}

.node-card-footer {
  margin-top: 10px;
  font-size: 12px;
  color: #8a98a4;
}

.node-editor {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 4px 2px;
}

.node-kpi-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.node-kpi-card {
  padding: 12px 14px;
  background: #fafcff;
  border: 1px solid #edf2f7;
  border-radius: 12px;
}

.node-kpi-label {
  display: block;
  margin-bottom: 6px;
  font-size: 12px;
  color: #8a98a4;
}

.node-kpi-value {
  display: block;
  font-size: 13px;
  line-height: 1.6;
  color: #1f2d38;
  word-break: break-word;
}

.node-inline-alert {
  margin-bottom: 0;
}

.node-editor-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.node-form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 16px;
}

.node-credential-panel {
  padding: 16px;
  background: #fafcff;
  border: 1px solid #edf2f7;
  border-radius: 14px;
}

.node-form-grid.is-credential {
  margin-top: 12px;
}

.node-private-key-item :deep(.ant-form-item-control-input) {
  min-height: 0;
}

.node-test-output {
  min-height: 140px;
  max-height: 260px;
  overflow: auto;
  padding: 12px;
  margin: 0;
  border-radius: 12px;
  background: #0f172a;
  color: #dbeafe;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 12px;
  line-height: 1.55;
}

.task-id {
  display: block;
  font-size: 14px;
  color: #1f2d38;
  letter-spacing: 0.4px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ant-table-cell[data-column-key="task_id"] .task-id {
  font-weight: 600;
}

.task-id-row {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  min-width: 0;
}

.task-id-main {
  flex: 1 1 auto;
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

/* 数据源单元格：数据库类型图标 + 文字。
   图标尺寸(16px)、与文字间距(8px)与「数据恢复」页 .data-source-cell / .db-logo 完全一致。 */
.data-source-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.data-source-text {
  min-width: 0;
}

/* 数据源「实例名称」跟随所在主文字行样式（.task-id：14px / #1f2d38），
   覆盖 DataSourceName 组件默认的小号淡灰（12px / #748893）；
   下方实例地址由 .task-desc.muted 呈现为小一号的次要文字。 */
.backup-task-table :deep(.gi-data-source-name) {
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

.task-desc {
  margin-top: 2px;
  display: block;
  font-size: 12px;
  color: #9aa6ae;
  line-height: 1.5;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-name-row {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  min-width: 0;
}

.task-name-text {
  color: #9aa6ae;
  font-size: 12px;
  line-height: 1.5;
  /* 与复制按钮一致:不拉伸,图标紧贴文字;超长时再收缩并 ellipsis */
  flex: 0 1 auto;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-name-edit-btn {
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
  opacity: 1;
  transition: color 0.2s ease, opacity 0.2s ease;
}

.task-name-edit-btn:hover {
  color: var(--gi-color-primary, #0f766e);
}

.task-name-input {
  flex: 1 1 auto;
  min-width: 0;
}

.task-name-input :deep(.ant-input) {
  font-size: 12px;
  padding: 2px 6px;
  line-height: 1.4;
  min-height: 22px;
  height: 22px;
}

.cell-ellipsis {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.run-summary {
  display: grid;
  gap: 6px;
  margin-bottom: 12px;
  color: #34404a;
}

.run-drawer-shell {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.run-task-overview,
.latest-run-card,
.run-history-panel,
.run-detail-panel {
  border-radius: 14px;
  box-shadow: 0 8px 24px rgba(15, 23, 42, 0.04);
}

.run-task-header,
.latest-run-main {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.run-task-title-line,
.latest-run-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.run-task-title,
.latest-run-title {
  font-size: 18px;
  font-weight: 600;
  color: #1f2d38;
}

.run-task-subtitle,
.latest-run-desc {
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.6;
  color: #71818c;
}

.run-summary-grid,
.latest-run-metrics,
.detail-kpi-grid {
  display: grid;
  gap: 12px;
}

.run-summary-grid {
  grid-template-columns: repeat(4, minmax(0, 1fr));
  margin-top: 16px;
}

.strategy-overview-card {
  margin-top: 16px;
  padding: 18px 18px 16px;
  border-radius: 14px;
  background: linear-gradient(135deg, #eaf4ff 0%, #fff 62%, #f5faff 100%);
  border: 1px solid #d7e8ff;
}

.strategy-card-title {
  display: flex;
  align-items: center;
  font-size: 15px;
  font-weight: 600;
  color: #1f2d38;
}

.strategy-card-title-main {
  font-size: 15px;
  font-weight: 600;
  color: #1f2d38;
}

.strategy-card-subtitle {
  margin-top: 4px;
  font-size: 12px;
  line-height: 1.6;
  color: #647683;
}

.strategy-grid {
  display: grid;
  grid-template-columns: 1fr 36px 1fr;
  align-items: stretch;
  gap: 12px;
  margin-top: 14px;
}

.strategy-column {
  padding: 14px 14px 12px;
  background: #fff;
  border-radius: 12px;
  border: 1px solid #e6eef8;
}

.strategy-column-header {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 600;
  color: #1f2d38;
}

.strategy-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #d9dee3;
}

.strategy-dot.is-full {
  background: #52c41a;
  box-shadow: 0 0 0 4px rgba(82, 196, 26, 0.14);
}

.strategy-dot.is-other {
  background: #1677ff;
  box-shadow: 0 0 0 4px rgba(22, 119, 255, 0.14);
}

.strategy-weekdays {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 10px;
}

.strategy-column-footer {
  margin-top: 10px;
  font-size: 12px;
  color: #71818c;
}

.strategy-arrow {
  align-self: center;
  text-align: center;
  font-size: 20px;
  color: #94a3af;
  font-weight: 600;
}

.strategy-preview {
  margin-top: 14px;
  padding: 12px 14px;
  border-radius: 10px;
  background: #f0f7ff;
  border: 1px dashed #b8d5ff;
  font-size: 13px;
  line-height: 1.7;
  color: #234162;
}

.strategy-preview-label {
  font-weight: 600;
  color: #0f54a8;
}

.latest-run-card {
  border: 1px solid #e8edf2;
}

.latest-run-card.is-success {
  background: linear-gradient(135deg, #f6ffed, #ffffff 58%);
}

.latest-run-card.is-failed {
  background: linear-gradient(135deg, #fff2f0, #ffffff 58%);
}

.latest-run-card.is-running {
  background: linear-gradient(135deg, #f0f8ff, #ffffff 58%);
}

.latest-run-metrics {
  grid-template-columns: repeat(6, minmax(0, 1fr));
  margin-top: 16px;
}

.summary-kv,
.metric-item,
.detail-kpi-card {
  padding: 12px 14px;
  background: #fafcff;
  border: 1px solid #edf2f7;
  border-radius: 12px;
}

.summary-label,
.metric-label,
.detail-kpi-label {
  display: block;
  margin-bottom: 6px;
  font-size: 12px;
  color: #8a98a4;
}

.summary-value,
.metric-value,
.detail-kpi-value {
  font-size: 13px;
  line-height: 1.6;
  color: #1f2d38;
  word-break: break-word;
}

.detail-kpi-grid {
  grid-template-columns: repeat(4, minmax(0, 1fr));
  margin-bottom: 16px;
}

.run-detail-layout {
  display: grid;
  grid-template-columns: 320px minmax(0, 1fr);
  gap: 16px;
  min-height: 560px;
}

.run-history-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.run-history-item {
  padding: 14px;
  border: 1px solid #edf2f7;
  border-radius: 12px;
  background: #fafcff;
  cursor: pointer;
  transition: all 0.2s ease;
}

.run-history-item:hover {
  border-color: #cfe2ff;
  box-shadow: 0 6px 18px rgba(24, 144, 255, 0.08);
}

.run-history-item.active {
  border-color: #91caff;
  background: #f0f8ff;
}

.run-history-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}

.run-history-name {
  font-size: 14px;
  font-weight: 600;
  color: #1f2d38;
}

.run-history-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 12px;
  margin-top: 8px;
  font-size: 12px;
  color: #8a98a4;
}

.run-history-stage {
  margin-top: 10px;
  font-size: 13px;
  font-weight: 500;
  color: #1f2d38;
}

.run-history-result {
  margin-top: 6px;
  font-size: 12px;
  color: #4c5d68;
  line-height: 1.6;
}

.run-history-error {
  margin-top: 8px;
  font-size: 12px;
  line-height: 1.5;
  color: #cf1322;
  word-break: break-word;
}

.detail-descriptions {
  margin-top: 4px;
}

.timeline-title {
  margin: 18px 0 10px;
  font-size: 14px;
  font-weight: 600;
  color: #1f2d38;
}

.run-stage-timeline {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.timeline-item {
  display: flex;
  gap: 12px;
  align-items: flex-start;
}

.timeline-dot {
  width: 10px;
  height: 10px;
  margin-top: 6px;
  border-radius: 50%;
  background: #d9dee3;
  flex: 0 0 auto;
}

.timeline-item.is-done .timeline-dot {
  background: #52c41a;
}

.timeline-item.is-active .timeline-dot {
  background: #1677ff;
  box-shadow: 0 0 0 4px rgba(22, 119, 255, 0.12);
}

.timeline-item.is-failed .timeline-dot {
  background: #ff4d4f;
  box-shadow: 0 0 0 4px rgba(255, 77, 79, 0.12);
}

.timeline-stage {
  font-size: 13px;
  font-weight: 600;
  color: #1f2d38;
}

.timeline-desc {
  margin-top: 3px;
  font-size: 12px;
  line-height: 1.6;
  color: #8a98a4;
}

.path-row {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 4px;
}

.path-text {
  flex: 0 1 auto;
  min-width: 0;
  word-break: break-all;
}

.copy-icon {
  flex: none;
  cursor: pointer;
  color: #8ca0ad;
  font-size: 13px;
  transition: color 0.15s ease;
}

.copy-icon:hover {
  color: var(--gi-color-primary, #0f766e);
}

.detail-inline-alert {
  margin-top: 14px;
}

.log-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}

.log-toolbar-meta {
  display: flex;
  gap: 16px;
  flex-wrap: wrap;
  font-size: 12px;
  color: #8a98a4;
}

.inline-log-preview {
  min-height: 420px;
  max-height: 52vh;
}

.analysis-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.analysis-item {
  line-height: 1.7;
  color: #34404a;
}

.analysis-actions {
  margin-top: 16px;
}

.muted-inline {
  font-size: 12px;
  color: #8a98a4;
}

.error-text {
  margin-top: 4px;
  color: #cf1322;
  font-size: 12px;
  line-height: 1.4;
  word-break: break-all;
}

.log-alert {
  margin-bottom: 12px;
}

.log-preview {
  min-height: 360px;
  max-height: 60vh;
  overflow: auto;
  padding: 12px;
  margin: 0;
  background: #0f172a;
  color: #dbeafe;
  border-radius: 8px;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 12px;
  line-height: 1.5;
}

.table-header-content {
  position: relative;
  display: flex;
  align-items: center;
  min-height: 24px;
  padding-right: 12px;
  font-weight: 600;
  color: #1f2d38;
}

.table-header-content.is-center {
  justify-content: center;
}

.table-header-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.column-resize-handle {
  position: absolute;
  top: -10px;
  right: -8px;
  width: 14px;
  height: calc(100% + 20px);
  cursor: col-resize;
}

.backup-task-table :deep(.ant-table-thead > tr > th) {
  padding: 12px;
  font-weight: 600;
  background: #fafcff;
  border-bottom: 1px solid #e8edf2;
  position: sticky;
  top: 0;
  z-index: 5;
}

.backup-task-table :deep(.ant-table-header) {
  overflow-x: auto;
  overflow-y: hidden;
}

.backup-task-table :deep(.ant-table-body) {
  overflow-x: auto;
}

.backup-task-table :deep(.ant-table-header::-webkit-scrollbar) {
  height: 0;
}

.backup-task-table :deep(.ant-table-sticky-holder) {
  z-index: 8;
  background: #fafcff;
}

.backup-task-table :deep(.ant-table-sticky-holder::after) {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 1px;
  background: #e8edf2;
}

/* 固定列(状态/操作)背景必须不透明,否则横向滚动时下层内容会透出来 */
.backup-task-table :deep(.ant-table-tbody > tr > td.ant-table-cell-fix-left),
.backup-task-table :deep(.ant-table-tbody > tr > td.ant-table-cell-fix-right) {
  background: #fff;
}

.backup-task-table :deep(.ant-table-tbody > tr:hover > td.ant-table-cell-fix-left),
.backup-task-table :deep(.ant-table-tbody > tr:hover > td.ant-table-cell-fix-right) {
  background: #f7fbff;
}

.backup-task-table :deep(.ant-table-tbody > tr.ant-table-row-selected > td.ant-table-cell-fix-left),
.backup-task-table :deep(.ant-table-tbody > tr.ant-table-row-selected > td.ant-table-cell-fix-right) {
  background: #eef5f5;
}

.backup-task-table
  :deep(.ant-table-tbody > tr.ant-table-row-selected:hover > td.ant-table-cell-fix-left),
.backup-task-table
  :deep(.ant-table-tbody > tr.ant-table-row-selected:hover > td.ant-table-cell-fix-right) {
  background: #e2efee;
}

/* 表头固定列保持与普通表头一致的不透明底色 */
.backup-task-table :deep(.ant-table-thead > tr > th.ant-table-cell-fix-left),
.backup-task-table :deep(.ant-table-thead > tr > th.ant-table-cell-fix-right) {
  background: #fafcff;
}

.backup-task-table :deep(.ant-table-tbody > tr > td) {
  padding: 12px;
  vertical-align: middle;
  border-bottom: 1px solid #f0f3f6;
}

.backup-task-table :deep(.ant-table-tbody > tr:hover > td) {
  background: #f7fbff;
}

.backup-task-table :deep(.ant-table-cell) {
  word-break: break-word;
  white-space: normal;
}

.backup-task-table :deep(.is-center-cell) {
  text-align: center;
  vertical-align: middle;
}

/* 单行内容列(创建时间 / 数据库类型 / 创建人):
   - 创建时间:超出列宽时单元格内横向滑动查看,不省略、不隐藏
   - 数据库类型 / 创建人:保持 nowrap + ellipsis */
.backup-task-table :deep(.ant-table-cell[data-column-key="created_at"]) > span {
  display: block;
  white-space: nowrap;
  overflow-x: auto;
  overflow-y: hidden;
  scrollbar-width: thin;
  scrollbar-color: rgba(140, 160, 175, 0.45) transparent;
}

.backup-task-table :deep(.ant-table-cell[data-column-key="created_at"]) > span::-webkit-scrollbar {
  height: 6px;
}

.backup-task-table :deep(.ant-table-cell[data-column-key="created_at"]) > span::-webkit-scrollbar-thumb {
  background: rgba(140, 160, 175, 0.35);
  border-radius: 3px;
}

.backup-task-table :deep(.ant-table-cell[data-column-key="created_at"]) > span::-webkit-scrollbar-thumb:hover {
  background: rgba(140, 160, 175, 0.6);
}

.backup-task-table :deep(.ant-table-cell[data-column-key="created_at"]) > span::-webkit-scrollbar-track {
  background: transparent;
}

.backup-task-table :deep(.ant-table-cell[data-column-key="created_by"]) > span {
  display: block;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.backup-task-table :deep(.is-center-cell .ant-switch),
.backup-task-table :deep(.is-center-cell .ant-tag) {
  margin-inline: auto;
}

.backup-task-status-stack {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 2px;
  line-height: 1.45;
  width: 100%;
  max-width: 100%;
}

.backup-task-status-top {
  font-size: inherit;
  font-weight: inherit;
  color: inherit;
  white-space: nowrap;
  /* 状态列已按最长文案设置最小宽度,内容完整展示;整体超宽时交给表格底部横向滚动条 */
  overflow: hidden;
  text-overflow: clip;
  max-width: 100%;
}

.backup-task-status-bottom {
  font-size: 11px;
  color: #9aa7b0;
  max-width: 100%;
  white-space: nowrap;
  overflow: hidden;
}

.backup-status-tag,
.backup-status-text {
  font-size: 11px !important;
  line-height: 1.4 !important;
  padding: 0 5px !important;
  border-radius: 4px;
  margin: 0;
}

.backup-status-tag {
  white-space: nowrap;
  vertical-align: middle;
}

.backup-status-tag-success {
  color: var(--ant-color-primary);
  background: color-mix(in srgb, var(--ant-color-primary) 10%, transparent);
  border: 1px solid color-mix(in srgb, var(--ant-color-primary) 24%, transparent);
}

.backup-status-text {
  color: #9aa7b0;
}

.backup-task-table :deep(.is-wrap-cell) {
  white-space: normal;
}

.action-cell {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.more-action-btn {
  padding-inline: 4px;
  color: #667784;
  font-size: 12px;
  font-weight: 400;
}

.more-action-btn:hover {
  color: #1f2d38;
}

.delete-modal-title {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.delete-task-help-icon {
  font-size: 12px;
  color: var(--gi-color-text-quaternary, #b6c0cd);
  cursor: help;
  transition: color 0.2s;
}

.delete-task-help-icon:hover {
  color: var(--gi-color-text-secondary, #667784);
}

.delete-tip {
  margin: 10px 0;
  font-size: 13px;
  color: #4c5d68;
  line-height: 1.6;
}

.delete-task-name {
  color: #cf1322;
  font-weight: 600;
}

@media (max-width: 1200px) {
  .node-modal-layout {
    grid-template-columns: 1fr;
  }

  .node-kpi-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .run-detail-layout {
    grid-template-columns: 1fr;
  }

  .run-summary-grid,
  .latest-run-metrics,
  .detail-kpi-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .strategy-grid {
    grid-template-columns: 1fr;
  }

  .strategy-arrow {
    transform: rotate(90deg);
  }
}

@media (max-width: 768px) {
  .node-sidebar-header,
  .node-editor-header,
  .node-credential-header {
    flex-direction: column;
    align-items: stretch;
  }

  .node-kpi-grid,
  .node-form-grid {
    grid-template-columns: 1fr;
  }

  .run-task-header,
  .latest-run-main,
  .path-row,
  .log-toolbar {
    flex-direction: column;
    align-items: stretch;
  }

  .run-summary-grid,
  .latest-run-metrics,
  .detail-kpi-grid {
    grid-template-columns: 1fr;
  }

  .strategy-grid {
    grid-template-columns: 1fr;
  }

  .strategy-arrow {
    transform: rotate(90deg);
  }
}

.run-drawer-shell-v2 {
  display: flex;
  flex-direction: column;
  gap: 12px;
  height: 100%;
  min-height: 0;
}

.v2-task-header-simple {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px 14px;
  background: #fff;
  border: 1px solid #eef1f4;
  border-radius: 8px;
}

.v2-task-header-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.v2-task-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 14px;
  background: #fff;
  border: 1px solid #eef1f4;
  border-radius: 8px;
}

.v2-task-title-wrap {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.v2-task-title {
  font-size: 16px;
  font-weight: 600;
  color: #1f2d38;
}

.v2-task-id-wrap {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.v2-task-id-text {
  color: #9aa6ae;
  font-size: 12px;
  line-height: 1.4;
}

.v2-status-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 24px;
  padding: 0 10px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
  border: 1px solid #dbe3ea;
  background: #f8fafc;
  color: #64748b;
}

.v2-status-pill.is-success {
  border-color: #bbf7d0;
  background: #f0fdf4;
  color: #15803d;
}

.v2-status-pill.is-running {
  border-color: #bae6fd;
  background: #f0f9ff;
  color: #0369a1;
}

.v2-status-pill.is-failed {
  border-color: #fecaca;
  background: #fef2f2;
  color: #dc2626;
}

.v2-status-pill.is-stopped,
.v2-status-pill.is-paused {
  border-color: #fde68a;
  background: #fffbeb;
  color: #b45309;
}

.v2-status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: currentColor;
}

.v2-task-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.v2-task-inline-meta {
  display: flex;
  align-items: center;
  gap: 18px;
  flex-wrap: wrap;
  font-size: 13px;
}

.v2-inline-meta-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.v2-inline-meta-label {
  color: #a3adb8;
}

.v2-inline-meta-value {
  color: #a3adb8;
  font-weight: inherit;
}

.v2-inline-more-btn {
  padding: 0;
  border: 0;
  background: transparent;
  color: #0f766e;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
}

.v2-summary-inline {
  margin-top: 2px;
  padding-top: 12px;
}

.v2-summary-grid {
  display: grid;
  grid-template-columns: 32% repeat(4, minmax(0, 1fr));
  gap: 12px 16px;
}

.v2-summary-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.v2-summary-label {
  font-size: 12px;
  color: #a3adb8;
}

.v2-summary-value {
  font-size: 12px;
  line-height: 20px;
  color: #a3adb8;
  font-weight: inherit;
  word-break: break-word;
}

.v2-summary-value-with-action {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.v2-object-view-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--gi-color-primary, #0f766e);
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
}

.v2-object-view-btn:hover {
  color: #0d9488;
}

.v2-inline-action-btn {
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--gi-color-primary, #0f766e);
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
}

.v2-inline-action-btn:hover {
  color: #0d9488;
}

.retention-editing-wrap {
  display: inline-flex;
  align-items: center;
}

.v2-retention-select {
  min-width: 92px;
}

.v2-schedule-editor {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-width: 260px;
}

.v2-schedule-editor-title {
  font-size: 12px;
  color: #8ca0ad;
}

.v2-schedule-editor-days {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.v2-schedule-time-editor {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.v2-schedule-time-select {
  width: 84px;
}

.v2-schedule-time-sep {
  color: #5f6b7a;
  font-size: 13px;
  line-height: 1;
}

.v2-schedule-editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.object-viewer-shell {
  display: flex;
  flex-direction: column;
  gap: 16px;
  height: 100%;
  min-height: 0;
}

.object-viewer-meta {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.object-viewer-label {
  font-size: 12px;
  color: #8ca0ad;
}

.object-viewer-value {
  font-size: 13px;
  line-height: 20px;
  color: #1f2d38;
  word-break: break-word;
}

.object-viewer-tree {
  flex: 1;
  min-height: 0;
  overflow: auto;
}

.object-viewer-tree :deep(.ant-tree) {
  display: inline-block;
  min-width: max-content;
  white-space: nowrap;
}

.object-viewer-tree :deep(.ant-tree-list),
.object-viewer-tree :deep(.ant-tree-list-holder),
.object-viewer-tree :deep(.ant-tree-list-holder-inner) {
  min-width: max-content;
}

.object-viewer-tree :deep(.ant-tree-treenode),
.object-viewer-tree :deep(.ant-tree-node-content-wrapper),
.object-viewer-tree :deep(.ant-tree-title) {
  white-space: nowrap;
}

.object-viewer-tree :deep(.ant-tree-treenode) {
  width: max-content;
}

.object-viewer-tree :deep(.ant-tree-node-content-wrapper) {
  display: inline-flex;
  align-items: center;
  width: auto;
}

.object-tree-icon-prefix {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  margin-right: 4px;
  font-size: 13px;
  line-height: 14px;
  color: rgba(0, 0, 0, 0.65);
  vertical-align: middle;
}

.object-viewer-tree :deep(.ant-tree-node-content-wrapper:hover) {
  background: rgba(15, 118, 110, 0.08);
}

.v2-task-meta {
  padding: 8px 14px;
  background: #fff;
  border: 1px solid #eef1f4;
  border-radius: 8px;
}

.v2-meta-row {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 4px 14px;
}

.v2-meta-row + .v2-meta-row {
  margin-top: 6px;
  padding-top: 6px;
  border-top: 1px dashed #f0f2f5;
}

.v2-meta-item {
  display: flex;
  align-items: baseline;
  gap: 6px;
  font-size: 12px;
  min-width: 0;
}

.v2-meta-label {
  flex-shrink: 0;
  color: #8a98a4;
}

.v2-meta-value {
  flex: 1;
  min-width: 0;
  color: #1f2d38;
  word-break: break-all;
}

.v2-strategy-card {
  padding: 10px 14px;
  background: linear-gradient(135deg, #eaf4ff 0%, #fff 62%, #f5faff 100%);
  border: 1px solid #d7e8ff;
  border-radius: 8px;
}

.v2-strategy-head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  font-size: 13px;
}

.v2-strategy-title {
  font-weight: 600;
  color: #1f2d38;
}

.v2-strategy-sub {
  font-size: 12px;
  color: #647683;
}

/* 折叠开关行：重置 button 默认样式，占满卡片宽度可整行点击 */
.v2-strategy-toggle {
  width: 100%;
  padding: 0;
  border: none;
  background: transparent;
  text-align: left;
  font: inherit;
  cursor: pointer;
}

.v2-strategy-toggle:focus-visible {
  outline: 2px solid #1f6feb;
  outline-offset: 2px;
  border-radius: 4px;
}

/* 展开区：与标题行的间距 */
.v2-strategy-body {
  margin-top: 10px;
}

/* 收起/展开箭头：纯 CSS chevron，收起朝右、展开朝下，随状态旋转过渡 */
.v2-strategy-chevron {
  margin-left: auto;
  flex: none;
  width: 7px;
  height: 7px;
  border-right: 1.5px solid #647683;
  border-bottom: 1.5px solid #647683;
  transform: rotate(-45deg);
  transition: transform 0.2s ease;
}

.v2-strategy-chevron.is-open {
  transform: rotate(45deg);
}

.v2-strategy-preview {
  margin-top: 8px;
  padding: 8px 10px;
  border-radius: 6px;
  background: #f0f7ff;
  border: 1px dashed #b8d5ff;
  font-size: 12px;
  line-height: 1.6;
  color: #234162;
}

.v2-metric-label {
  color: #8a98a4;
  font-size: 11px;
}

.v2-metric-value {
  color: #1f2d38;
  word-break: break-all;
  line-height: 1.5;
}

.v2-tabs-section {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: #fff;
  border: 1px solid #eef1f4;
  border-radius: 8px;
  padding: 0;
  overflow: hidden;
}

.v2-tabs-section :deep(.v2-main-tabs) {
  margin: 0;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.v2-tabs-section :deep(.v2-main-tabs > .ant-tabs-nav) {
  margin: 0;
  padding: 0 14px;
  background: #fafbfc;
  border-bottom: 1px solid #eef1f4;
}

.v2-tabs-section :deep(.v2-main-tabs > .ant-tabs-nav .ant-tabs-tab) {
  padding: 8px 14px;
  font-size: 13px;
}

.v2-tabs-section :deep(.v2-main-tabs > .ant-tabs-content-holder) {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 10px 14px 12px;
}

.v2-tabs-section :deep(.v2-main-tabs > .ant-tabs-content-holder > .ant-tabs-content),
.v2-tabs-section :deep(.v2-main-tabs > .ant-tabs-content-holder > .ant-tabs-content > .ant-tabs-tabpane) {
  height: 100%;
  min-height: 0;
}

.v2-records-pane {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

.v2-records-loading {
  flex: 1;
  min-height: 0;
}

.v2-records-loading :deep(.ant-spin-nested-loading),
.v2-records-loading :deep(.ant-spin-container) {
  height: 100%;
  min-height: 0;
}

.v2-records-table-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding-right: 2px;
}

.v2-run-table {
  width: 100%;
}

.v2-run-table :deep(.ant-table-thead > tr > th) {
  padding: 8px 10px;
  background: #fafcff;
  border-bottom: 1px solid #e8edf2;
  font-weight: 600;
  color: #1f2d38;
  font-size: 12px;
}

.v2-run-table :deep(.ant-table-tbody > tr > td) {
  padding: 8px 10px;
  font-size: 12px;
  border-bottom: 1px solid #f0f3f6;
  vertical-align: middle;
}

.v2-run-table :deep(.ant-table-tbody > tr:hover > td) {
  background: #f7fbff;
}

.v2-run-table :deep(.ant-table-expanded-row > td) {
  padding: 0 !important;
  background: #fcfdff;
}

.v2-expand-panel {
  padding: 10px 14px 14px 36px;
  border-top: 1px dashed #e8edf2;
}

.v2-expand-panel :deep(.ant-tabs) {
  margin: 0;
}

.v2-expand-panel :deep(.ant-tabs > .ant-tabs-nav) {
  margin: 0 0 8px 0;
}

.v2-expand-panel :deep(.ant-tabs > .ant-tabs-nav .ant-tabs-tab) {
  padding: 6px 12px;
  font-size: 12px;
}

.v2-pagination-wrap {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 8px;
  padding-top: 8px;
}

.v2-total-text {
  font-size: 12px;
  color: #8a98a4;
}

.v2-pane-card {
  border: 1px solid #eef1f4;
  border-radius: 8px;
  padding: 12px;
  background: #fff;
}

/* 备份集表格底部工具条：整体靠右下；无可用备份集时该行不渲染，不占高度 */
.v2-pane-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  margin-top: 12px;
}

/* 备份集 ID 单元格：文本超宽截断，复制图标始终贴在其右侧且不被压缩 */
.v2-set-id-cell {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.v2-set-id-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--gi-color-primary, #0f766e);
  font-variant-numeric: tabular-nums;
}

/* 操作列链接按钮统一使用系统主题色（与全局 Layout 的 --gi-color-primary 对齐） */
.v2-link-btn.ant-btn-link {
  color: var(--gi-color-primary, #0f766e);
}

.v2-link-btn.ant-btn-link:hover,
.v2-link-btn.ant-btn-link:focus {
  color: var(--gi-color-primary-hover, #0d6b63);
}

/* 执行记录状态列：去掉药丸形底色与描边，只保留状态语义色文字 */
.v2-run-status-tag.ant-tag {
  margin-inline-end: 0;
  padding: 0;
  border: none;
  background: transparent;
  border-radius: 0;
  font-size: 13px;
}

.v2-bottom-tip {
  margin-top: 6px;
  padding: 4px 0;
}

.v2-tip-text {
  font-size: 11px;
  color: #9aa6ae;
}

@media (max-width: 1200px) {
  .v2-task-header-top {
    flex-direction: column;
    align-items: stretch;
  }
  .v2-task-actions {
    flex-wrap: wrap;
  }
  .v2-summary-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .v2-meta-row {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 768px) {
  .v2-task-inline-meta {
    gap: 10px 14px;
  }
  .v2-summary-grid {
    grid-template-columns: 1fr;
  }
  .v2-record-toolbar {
    flex-direction: column;
    align-items: stretch;
  }
  .v2-record-search {
    max-width: none;
  }
  .v2-record-toolbar-actions {
    justify-content: flex-end;
  }
  .v2-meta-row {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 768px) {
  /* 主工具栏 a-space 没有 wrap，搜索框 280px 会撑出屏幕 */
  :deep(.gi-page-toolbar) {
    flex-wrap: wrap;
    row-gap: 8px;
  }

  :deep(.ant-space) {
    flex-wrap: wrap;
    row-gap: 8px;
  }

  :deep(.ant-space-item) {
    max-width: 100%;
  }

  .search-input {
    width: 100%;
  }

  /* PC 上拖拽过的列宽会持久化到 localStorage，窄屏需要解除固定布局 */
  .backup-task-table-wrap :deep(.ant-table) {
    table-layout: auto;
  }
}
</style>
