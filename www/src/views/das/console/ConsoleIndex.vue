<template>
  <div
    class="console-page"
    :class="{
      'is-workspace-fullscreen': uiState.workspaceFullscreen,
      'is-result-fullscreen': uiData.resultFullscreen,
    }"
    @contextmenu.prevent
  >
    <div class="console-shell">
      <div class="workspace-tabbar">
        <div class="workspace-tabbar-main">
          <a-button
            v-if="tabbarScrollState.canScrollLeft"
            size="small"
            class="workspace-tab-scroll-button"
            @click="consoleRightRef?.scrollTabsBy?.(-240)"
          >
            <template #icon>
              <LeftOutlined />
            </template>
          </a-button>

          <div
            ref="externalTabScrollerRef"
            class="workspace-tab-scroll"
            @scroll="consoleRightRef?.updateTabScrollState?.()"
            @wheel.prevent="handleExternalTabWheel"
          >
            <div v-if="!tabbarPanes.length" class="workspace-tab-empty">暂无 SQL窗口</div>
            <a-dropdown
              v-for="(pane, index) in tabbarPanes"
              :key="pane.key"
              placement="bottomLeft"
              :trigger="['contextmenu']"
            >
              <div class="workspace-tab-shell" :class="getTabShellClass(index, pane.key)">
                <div
                  v-if="tabRenamingTabKey !== pane.key"
                  :data-tab-key="pane.key"
                  class="workspace-tab-item"
                  :class="[
                    `tab-kind-${pane.kind}`,
                    {
                      'is-active': pane.key === tabbarActiveKey,
                      'is-pinned': pane.pinned,
                      'is-dragging': tabDraggingTabKey === pane.key,
                    },
                  ]"
                  :style="consoleRightRef?.getPaneEnvironmentStyle?.(pane)"
                  :draggable="pane.kind === 'console'"
                  @click="consoleRightRef?.handleTabChange?.(pane.key)"
                  @dblclick.stop="consoleRightRef?.startRenamePane?.(pane)"
                  @dragstart="consoleRightRef?.handleTabDragStart?.(pane, $event)"
                  @dragover.prevent="consoleRightRef?.handleTabDragOver?.(pane, $event)"
                  @drop.prevent="consoleRightRef?.handleTabDrop?.(pane, $event)"
                  @dragend="consoleRightRef?.handleTabDragEnd?.()"
                >
                  <AppTooltip
                    placement="bottomLeft"
                    :title="consoleRightRef?.buildPaneTooltipTitle?.(pane) || pane?.instance_remark || (pane?.kind === 'preview' ? '数据查看' : '窗口')"
                  >
                    <template #title>
                      <div class="tab-tooltip-card tab-tooltip-card--simple">
                        <div class="tab-tooltip-card__head">
                          <DatabaseFilled
                            class="tab-tooltip-card__icon"
                            :class="{ 'is-unbound': !(consoleRightRef?.isPaneBoundToInstance?.(pane)) && pane?.kind !== 'preview' && !pane?.customTitle }"
                          />
                          <span
                            class="tab-tooltip-card__instance-name"
                            :class="{ 'is-unbound': !(consoleRightRef?.isPaneBoundToInstance?.(pane)) && pane?.kind !== 'preview' && !pane?.customTitle }"
                          >
                            {{ consoleRightRef?.buildPaneTooltipVm?.(pane)?.instanceName || consoleRightRef?.buildPaneTitle?.(pane) || '窗口' }}
                          </span>
                        </div>
                        <div
                          v-if="consoleRightRef?.buildPaneTooltipVm?.(pane)?.host"
                          class="tab-tooltip-card__row tab-tooltip-card__row--sub"
                        >
                          <EnvironmentOutlined class="tab-tooltip-card__row-icon" />
                          <span class="tab-tooltip-card__row-text">
                            {{ consoleRightRef?.buildPaneTooltipVm?.(pane)?.host }}{{ consoleRightRef?.buildPaneTooltipVm?.(pane)?.port ? `:${consoleRightRef?.buildPaneTooltipVm?.(pane)?.port}` : '' }}
                          </span>
                        </div>
                      </div>
                    </template>
                    <div class="workspace-tab-content">
                      <span class="workspace-tab-status" :class="`status-${consoleRightRef?.getPaneStatus?.(pane)}`">
                        <WarningOutlined v-if="consoleRightRef?.getPaneStatus?.(pane) === 'disconnected'" />
                        <LoadingOutlined v-else-if="consoleRightRef?.getPaneStatus?.(pane) === 'running'" />
                        <ConsoleDbIcon
                          v-else-if="pane.kind !== 'preview' && consoleRightRef?.isPaneBoundToInstance?.(pane)"
                          :type="consoleRightRef?.getPaneDbType?.(pane) || 'mysql'"
                        />
                        <TableOutlined v-else-if="pane.kind === 'preview'" />
                      </span>
                      <span class="workspace-tab-texts">
                        <span
                          class="workspace-tab-title"
                          :class="{ 'is-unbound': pane.kind !== 'preview' && !consoleRightRef?.isPaneBoundToInstance?.(pane) && !pane.customTitle }"
                        >{{ consoleRightRef?.buildPaneTitle?.(pane) }}</span>
                      </span>
                      <!-- 收藏 / 刷新资源树：从对象树实例节点迁移到 tab 上，仅激活 tab 且已绑定实例时显示 -->
                      <span
                        v-if="pane.kind === 'console' && String(pane.instance_id || '').trim() && pane.key === tabbarActiveKey"
                        class="workspace-tab-tools"
                      >
                        <a-tooltip :title="consoleLeftRef?.isFavoriteInstance?.(pane.instance_id) ? '取消收藏' : '收藏'" placement="top">
                          <button
                            type="button"
                            class="workspace-tab-tool-btn"
                            :class="{ 'is-fav': consoleLeftRef?.isFavoriteInstance?.(pane.instance_id) }"
                            @click.stop.prevent="consoleLeftRef?.toggleFavoriteInstance?.(pane.instance_id)"
                            @mousedown.stop.prevent
                          >
                            <StarFilled v-if="consoleLeftRef?.isFavoriteInstance?.(pane.instance_id)" />
                            <StarOutlined v-else />
                          </button>
                        </a-tooltip>
                        <a-tooltip title="刷新资源树" placement="top">
                          <button
                            type="button"
                            class="workspace-tab-tool-btn"
                            @click.stop.prevent="consoleLeftRef?.reloadInstanceTree?.(pane.instance_id)"
                            @mousedown.stop.prevent
                          >
                            <ReloadOutlined />
                          </button>
                        </a-tooltip>
                      </span>
                      <PushpinFilled v-if="pane.pinned" class="workspace-tab-pin" />
                    </div>
                  </AppTooltip>
                  <button
                    v-if="consoleRightRef?.canClosePane?.(pane)"
                    type="button"
                    class="workspace-tab-close"
                    @click.stop="consoleRightRef?.removeTab?.(pane.key)"
                  >
                    <CloseOutlined />
                  </button>
                </div>
                <div
                  v-else
                  :data-tab-key="pane.key"
                  class="workspace-tab-item is-active is-editing"
                >
                  <span class="workspace-tab-status">
                    <EditOutlined />
                  </span>
                  <a-input
                    ref="externalRenameInputRef"
                    :value="tabRenamingValue"
                    size="small"
                    class="workspace-tab-rename-input"
                    @click.stop
                    @update:value="setExternalRenamingValue"
                    @blur="consoleRightRef?.commitRenamePane?.()"
                    @pressEnter="consoleRightRef?.commitRenamePane?.()"
                    @keydown.esc.stop.prevent="consoleRightRef?.cancelRenamePane?.()"
                  />
                </div>
              </div>
              <template #overlay>
                <a-menu class="console-tab-context-menu" @click="({ key }) => handleExternalPaneContextMenu(key, pane)">
                  <a-menu-item key="rename">重命名标签</a-menu-item>
                  <a-menu-item :key="pane.pinned ? 'unpin' : 'pin'">
                    {{ pane.pinned ? '取消固定标签' : '固定标签' }}
                  </a-menu-item>
                  <a-menu-item key="close" :disabled="!consoleRightRef?.canClosePane?.(pane)">关闭当前标签</a-menu-item>
                  <a-menu-item key="close-others">关闭其他所有标签</a-menu-item>
                  <a-menu-item key="close-right">关闭右侧所有标签</a-menu-item>
                  <a-menu-divider />
                  <a-menu-item key="copy-connection">复制当前 SQL窗口连接串</a-menu-item>
                  <a-menu-item key="split-horizontal" disabled>横向拆分窗口</a-menu-item>
                  <a-menu-item key="split-vertical" disabled>纵向拆分窗口</a-menu-item>
                </a-menu>
              </template>
            </a-dropdown>

            <a-button
              v-if="tabbarPanes.length > 0"
              size="small"
              class="workspace-tab-plus-button workspace-tab-plus-button--inline"
              @click="consoleRightRef?.openCreateConsoleTabModal?.('blank')"
            >
              <template #icon>
                <PlusOutlined />
              </template>
            </a-button>
          </div>

          <a-button
            v-if="tabbarScrollState.canScrollRight"
            size="small"
            class="workspace-tab-scroll-button"
            @click="consoleRightRef?.scrollTabsBy?.(240)"
          >
            <template #icon>
              <RightOutlined />
            </template>
          </a-button>
        </div>

        <div class="workspace-tabbar-actions">
          <AppTooltip :title="uiState.workspaceFullscreen ? '退出全屏 (Esc)' : '全屏编辑'" placement="bottom">
            <a-button
              size="small"
              type="text"
              class="workspace-tab-entry-button workspace-tab-fullscreen-button"
              :class="{ 'is-in-fullscreen': uiState.workspaceFullscreen }"
              @click="toggleWorkspaceFullscreen"
            >
              <template #icon>
                <FullscreenExitOutlined v-if="uiState.workspaceFullscreen" />
                <FullscreenOutlined v-else />
              </template>
            </a-button>
          </AppTooltip>
          <a-dropdown placement="bottomRight" :trigger="['click']">
            <a-button size="small" class="workspace-tab-entry-button workspace-tab-more-button">
              <template #icon>
                <EllipsisOutlined />
              </template>
            </a-button>
            <template #overlay>
              <a-menu class="console-tab-global-menu" @click="({ key }) => handleExternalGlobalTabAction(key)">
                <a-menu-item key="close-all">关闭所有标签</a-menu-item>
                <a-menu-item key="close-others">关闭其他标签</a-menu-item>
                <a-menu-item :key="tabbarActivePane?.pinned ? 'unpin-current' : 'pin-current'">
                  {{ tabbarActivePane?.pinned ? '取消固定当前标签' : '固定当前标签' }}
                </a-menu-item>
                <a-menu-item key="restore-layout">恢复默认标签布局</a-menu-item>
              </a-menu>
            </template>
          </a-dropdown>
        </div>
      </div>

      <div class="console-shell-body" :class="{ 'is-no-active-pane': !uiState.showResultPanel }">
        <SplitPanel
          :min-left-width="0"
          :default-left-width="280"
          :collapse-threshold="60"
          :max-left-width="600"
          :default-width-ratio-of-viewport="0.24"
          :auto-responsive="true"
          storage-key="das#consoleLeftSidebar.v2"
          :show-toggle-button="true"
          collapse-aria-label="收起资源树"
          expand-aria-label="展开资源树"
        >
            <template #left-content>
              <ConsoleLeft ref="consoleLeftRef" />
            </template>

            <template #right-content>
              <div ref="consoleWorkspaceRef" class="console-workspace">
                <ConsoleRight
                  ref="consoleRightRef"
                  :editor-height="uiData.editorHeight"
                  :show-top-tabbar="false"
                  @renderResultTable="renderResultTable"
                  @renderExecutionMessage="renderExecutionMessage"
                  @activePaneChange="handleActivePaneChange"
                />

                <div
                  v-if="uiState.showResultPanel"
                  class="workspace-resizer"
                  role="separator"
                  aria-label="调整编辑区与结果区高度"
                  tabindex="0"
                  @pointerdown="startWorkspaceResize"
                  @keydown.up.prevent="adjustEditorHeight(-20)"
                  @keydown.down.prevent="adjustEditorHeight(20)"
                ></div>

                <a-card v-if="uiState.showResultPanel" ref="resultCardRef" class="result-card" size="small">
                  <!-- 标题区即 Tab 导航：顶替原「查询结果」标题，Tabs 自带导航栏已隐藏 -->
                  <template #title>
                    <div class="result-tabbar">
                      <button
                        type="button"
                        class="result-tabbar-item"
                        :class="{ 'is-active': uiData.resultTab === 'info' }"
                        @click="uiData.resultTab = 'info'"
                      >
                        <span class="result-tabbar-text">执行信息</span>
                      </button>
                      <button
                        v-for="resultPane in visibleResultTabItems"
                        :key="resultPane.key"
                        type="button"
                        class="result-tabbar-item"
                        :class="{ 'is-active': uiData.resultTab === resultPane.key }"
                        @click="uiData.resultTab = resultPane.key"
                      >
                        <span class="result-tabbar-text">{{ resultPane.label }}</span>
                        <CloseOutlined
                          class="result-tabbar-close"
                          @click.stop.prevent="closeResultTab(resultPane.key)"
                        />
                      </button>
                    </div>
                  </template>
                  <template #extra>
                    <div class="result-card-toolbar">
                      <div class="result-toolbar-actions">
                          <a-dropdown
                            :disabled="!canUseResultActions || !filteredTableData.length"
                            trigger="click"
                            placement="bottomRight"
                            @visibleChange="(val) => (uiState.exportFormatPickerOpen = val)"
                          >
                            <a-button size="small" :disabled="!canUseResultActions || !filteredTableData.length">
                            导出
                          </a-button>
                          <template #overlay>
                            <div class="export-format-popover">
                              <div class="export-format-list">
                                <button
                                  type="button"
                                  class="export-format-item"
                                  @click="handlePickExportFormat('csv')"
                                >
                                  <span class="export-format-name">CSV</span>
                                </button>
                                <button
                                  type="button"
                                  class="export-format-item"
                                  @click="handlePickExportFormat('json')"
                                >
                                  <span class="export-format-name">JSON</span>
                                </button>
                                <button
                                  type="button"
                                  class="export-format-item"
                                  @click="handlePickExportFormat('sql')"
                                >
                                  <span class="export-format-name">SQL</span>
                                </button>
                              </div>
                            </div>
                          </template>
                        </a-dropdown>
                      </div>
                      <div class="result-toolbar-search">
                        <a-input-search
                          v-model:value="uiData.resultKeyword"
                          size="small"
                          allow-clear
                          :disabled="!canUseResultActions"
                          class="result-search"
                          placeholder="搜索当前结果"
                        />
                      </div>
                      <div class="result-toolbar-view-switch">
                        <AppTooltip title="行展示" placement="bottom">
                          <a-button
                            type="text"
                            size="small"
                            :disabled="!canUseResultActions || !filteredTableData.length"
                            class="result-view-toggle"
                            :class="{ 'is-active': uiData.resultViewMode === 'row' }"
                            @click="setResultViewMode('row')"
                          >
                            <template #icon>
                              <TableOutlined />
                            </template>
                          </a-button>
                        </AppTooltip>
                        <AppTooltip title="列展示" placement="bottom">
                          <a-button
                            type="text"
                            size="small"
                            :disabled="!canUseResultActions || !filteredTableData.length"
                            class="result-view-toggle"
                            :class="{ 'is-active': uiData.resultViewMode === 'column' }"
                            @click="setResultViewMode('column')"
                          >
                            <template #icon>
                              <UnorderedListOutlined />
                            </template>
                          </a-button>
                        </AppTooltip>
                      </div>
                      <AppTooltip :title="uiData.resultFullscreen ? '退出全屏' : '全屏显示'" placement="bottom">
                        <a-button
                          type="text"
                          size="small"
                          class="workspace-tab-entry-button workspace-tab-fullscreen-button result-fullscreen-button"
                          :class="{ 'is-in-fullscreen': uiData.resultFullscreen }"
                          @click="toggleResultFullscreen"
                        >
                          <template #icon>
                            <FullscreenExitOutlined v-if="uiData.resultFullscreen" />
                            <FullscreenOutlined v-else />
                          </template>
                        </a-button>
                      </AppTooltip>
                    </div>
                  </template>

                  <div v-if="resultNotice.type === 'error'" class="result-error-block">
                    <div class="result-error-main">
                      <div class="result-error-title">{{ resultNotice.title }}</div>
                      <div class="result-error-detail">{{ resultNotice.detail }}</div>
                    </div>
                    <div class="result-error-actions">
                      <a-button size="small" @click="copyResultNotice">复制错误</a-button>
                      <a-button size="small" type="primary" @click="retryResultNotice">重试</a-button>
                      <a-button size="small" type="link" @click="editResultNoticeInstance">编辑连接信息</a-button>
                    </div>
                  </div>

                  <a-tabs v-model:activeKey="uiData.resultTab" size="small" class="result-tabs">
                    <!-- 执行信息：固定 Tab，不可关闭 -->
                    <a-tab-pane key="info" tab="执行信息">
                      <div class="info-pane">
                        <a-empty v-if="!uiData.executionMessage" description="暂无执行信息" />
                        <pre v-else class="exec-message">{{ uiData.executionMessage }}</pre>
                      </div>
                    </a-tab-pane>

                    <!-- 结果集：可关闭，label 按顺序编号 -->
                    <a-tab-pane
                      v-for="resultPane in visibleResultTabItems"
                      :key="resultPane.key"
                      :tab="resultPane.label"
                    >
                      <div class="result-pane">
                        <a-empty
                          v-if="uiData.tableColumns.length === 0"
                          class="result-empty"
                          description="执行 SQL 后可在这里查看结果集"
                        />
                        <template v-else-if="uiData.resultViewMode === 'row'">
                          <div :ref="(el) => bindResultTableRegionRef(resultPane.key, el)" class="result-table-region">
                            <a-table
                              :class="['result-table', { 'is-compact-layout': !hasHorizontalScroll }]"
                              size="small"
                              table-layout="fixed"
                              row-key="__goinsightRowKey"
                              :data-source="paginatedTableData"
                              :columns="antdColumns"
                              :scroll="tableScroll"
                              :pagination="false"
                              :customRow="(record) => ({
                                class: [
                                  'result-table-row',
                                  { 'is-selected': String(record.__goinsightRowKey) === String(uiData.selectedRowKey) },
                                ],
                                onClick: () => handleSelectRow(record),
                              })"
                            />
                          </div>
                          <div class="result-footer-wrap">
                            <div class="result-footer-right">
                              <div class="result-pagination-summary">
                                <span>返回 {{ filteredTableData.length }} 行{{ uiData.resultKeyword ? `（原始 ${uiData.tableData.length} 行）` : '' }}</span>
                                <span>耗时 {{ formattedDuration }}</span>
                              </div>
                              <div class="result-pagination-wrap">
                                <a-pagination
                                  v-model:current="uiData.pagination.current"
                                  v-model:pageSize="uiData.pagination.pageSize"
                                  size="small"
                                  :total="filteredTableData.length"
                                  :show-size-changer="true"
                                  :show-less-items="true"
                                  :page-size-options="['30', '50', '100', '200']"
                                  @change="handlePaginationChange"
                                  @showSizeChange="handlePaginationShowSizeChange"
                                />
                              </div>
                            </div>
                          </div>
                        </template>
                        <template v-else>
                          <div class="result-column-view" :ref="(el) => bindResultTableRegionRef(resultPane.key, el)">
                            <div class="column-view-header">
                              <span class="column-view-title">列格式详情</span>
                              <span class="column-view-row-info">
                                {{ selectedRowForColumnView ? `第 ${selectedRowForColumnView.__goinsightRowKey} / ${filteredTableData.length} 行` : '' }}
                              </span>
                            </div>
                            <div v-if="!selectedRowForColumnView" class="column-view-empty">
                              <a-empty description="暂无数据" />
                            </div>
                            <div v-else class="column-view-body">
                              <div
                                v-for="col in uiData.tableColumns"
                                :key="col"
                                class="column-view-item"
                              >
                                <div class="column-view-name" :title="col">{{ col }}:</div>
                                <div
                                  class="column-view-value"
                                  :title="normalizeCellValue(selectedRowForColumnView?.[col])"
                                  @click="() => openCellModal(col, selectedRowForColumnView?.[col])"
                                >
                                  <span class="column-view-value-text">{{ normalizeCellValue(selectedRowForColumnView?.[col]) || '(NULL)' }}</span>
                                </div>
                              </div>
                            </div>
                          </div>
                        </template>
                      </div>
                    </a-tab-pane>
                  </a-tabs>
                </a-card>
              </div>
            </template>
        </SplitPanel>
      </div>
    </div>
  </div>

  <a-modal
    :open="uiState.exportConfirmOpen"
    :title="null"
    :footer="null"
    width="320px"
    :closable="true"
    centered
    class="export-confirm-modal"
    @update:open="(val) => (uiState.exportConfirmOpen = val)"
    @ok="executeConfirmExport"
    @cancel="cancelExportConfirm"
  >
    <div class="export-confirm-card">
      <div class="export-confirm-title">确认导出为 {{ selectedExportMeta?.label || '' }}</div>
      <div class="export-confirm-subtitle">共 {{ filteredTableData.length }} 行 / {{ uiData.tableColumns?.length || 0 }} 列</div>
      <div class="export-confirm-actions">
        <a-button size="small" @click="cancelExportConfirm">取消</a-button>
        <a-button type="primary" size="small" @click="executeConfirmExport">导出</a-button>
      </div>
    </div>
  </a-modal>

  <a-modal v-model:open="uiState.cellModalOpen" :title="uiState.cellModalTitle" width="760px">
    <div class="cell-modal-body">
      <pre class="cell-modal-content">{{ uiState.cellModalDisplay }}</pre>
    </div>
    <template #footer>
      <a-space>
        <a-button @click="copyCellValue">复制</a-button>
        <a-button type="primary" @click="uiState.cellModalOpen = false">关闭</a-button>
      </a-space>
    </template>
  </a-modal>

  <teleport to="body">
    <div
      v-if="uiState.resultContextMenuOpen"
      ref="resultContextMenuRef"
      class="result-context-menu"
      :style="resultContextMenuStyle"
      @mousedown.stop
      @click.stop
      @contextmenu.prevent
    >
      <button type="button" class="result-context-menu-item" @click="handleSelectContextCell">选中单元格</button>
      <div class="result-context-menu-divider"></div>
      <button type="button" class="result-context-menu-item" @click="handleCopyContextCell">复制单元格</button>
      <button type="button" class="result-context-menu-item" @click="handleCopyContextRow">复制当前行</button>
      <button type="button" class="result-context-menu-item" @click="handleCopyContextColumn">复制当前列</button>
      <button type="button" class="result-context-menu-item" @click="handleCopyCurrentPageRows">复制当前页</button>
      <div class="result-context-menu-divider"></div>
      <div
        class="result-context-menu-item has-submenu"
        :class="{
          'open-upward': uiState.resultContextSubmenuUpward,
          'open-leftward': uiState.resultContextSubmenuLeftward,
        }"
        @mouseenter="openCopyAsSubmenu"
        @mouseleave="scheduleCloseCopyAsSubmenu"
      >
        <span>复制为</span>
        <span class="result-context-menu-arrow">›</span>
        <div
          class="result-context-submenu"
          :style="{ display: uiState.copyAsSubmenuOpen ? 'block' : 'none' }"
          @mouseenter="openCopyAsSubmenu"
          @mouseleave="scheduleCloseCopyAsSubmenu"
        >
          <button type="button" class="result-context-menu-item" @click="handleCopyContextAs('sql')">SQL</button>
          <button type="button" class="result-context-menu-item" @click="handleCopyContextAs('csv')">CSV</button>
          <button type="button" class="result-context-menu-item" @click="handleCopyContextAs('json')">JSON</button>
          <button type="button" class="result-context-menu-item" @click="handleCopyContextAs('xml')">XML</button>
        </div>
      </div>
    </div>
  </teleport>
</template>

<script setup>
import SplitPanel from '@/components/panel/index.vue'
import AppTooltip from '@/components/patterns/AppTooltip.vue'
import {
  CloseOutlined,
  CopyOutlined,
  EditOutlined,
  EllipsisOutlined,
  ExpandOutlined,
  FileTextOutlined,
  FullscreenOutlined,
  FullscreenExitOutlined,
  InfoCircleOutlined,
  LeftOutlined,
  LoadingOutlined,
  PlusOutlined,
  PushpinFilled,
  ReloadOutlined,
  RightOutlined,
  StarFilled,
  StarOutlined,
  TableOutlined,
  WarningOutlined,
  DatabaseFilled,
  EnvironmentOutlined,
  GlobalOutlined,
  UnorderedListOutlined,
} from '@ant-design/icons-vue'
import { Modal, message } from 'ant-design-vue'
import { computed, h, inject, nextTick, onBeforeUnmount, onMounted, provide, reactive, ref, resolveComponent, watch } from 'vue'
import { useRouter } from 'vue-router'
import ConsoleDbIcon from './ConsoleDbIcon.vue'
import ConsoleLeft from './ConsoleLeft.vue'
import ConsoleRight from './ConsoleRight.vue'
import { FAMILY_CLICKHOUSE, FAMILY_MYSQL, FAMILY_POSTGRES, resolveDbFamily } from '@/utils/dbFamily'

const RESULT_PAGE_SIZE = 30
const DEFAULT_EDITOR_RATIO = 0.44
const MIN_PANEL_FALLBACK = 20
const RESULT_CELL_TOOLTIP_CHAR_LIMIT = 260
const RESULT_CELL_LONG_CHAR_LIMIT = 640
const RESULT_CELL_LONG_LINE_LIMIT = 18
const RESULT_CELL_MIN_CHARS_TO_TIP = 20
const RESULT_CELL_MAX_CHARS_NO_TIP = 28

// #region debug-point M:workspace-resize-log
const DEBUG_SERVER_URL = 'http://192.168.1.16:7777/event'
const DEBUG_SESSION_ID = 'editor-highlight-resize'
const DEBUG_RUN_ID = 'pre-fix'
const sendDebugEvent = (hypothesisId, location, msg, data = {}) => {
  // debug send disabled by default
  void hypothesisId; void location; void msg; void data
}
// #endregion

const dasInstanceData = reactive({})
provide('dasInstanceData', dasInstanceData)

const dasConsoleAction = reactive({
  nonce: 0,
  type: '',
  payload: null,
})
provide('dasConsoleAction', dasConsoleAction)

const FAVORITE_INSTANCES_STORAGE_KEY = 'das#favoriteInstanceIds'
const safeParseFavoriteIds = (raw) => {
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.map((id) => String(id)) : []
  } catch (e) {
    return []
  }
}
const initialFavoriteIds = safeParseFavoriteIds(
  typeof window !== 'undefined' ? window.localStorage.getItem(FAVORITE_INSTANCES_STORAGE_KEY) : '',
)
const dasFavoriteInstanceState = reactive({
  ids: initialFavoriteIds,
  has(instanceId) {
    return this.ids.some((id) => String(id) === String(instanceId))
  },
  toggle(instanceId) {
    const key = String(instanceId)
    if (!key) return false
    const index = this.ids.findIndex((id) => String(id) === key)
    if (index >= 0) {
      this.ids.splice(index, 1)
    } else {
      this.ids.push(key)
    }
    try {
      window.localStorage.setItem(FAVORITE_INSTANCES_STORAGE_KEY, JSON.stringify(this.ids))
    } catch (e) {
      /* ignore persistence errors */
    }
    return index < 0
  },
})
provide('dasFavoriteInstanceState', dasFavoriteInstanceState)

const resultNotice = reactive({
  type: '',
  title: '',
  detail: '',
  instance_id: '',
})
provide('dasResultNotice', resultNotice)

const router = useRouter()
const consoleRightRef = ref(null)
const consoleLeftRef = ref(null)
const externalTabScrollerRef = ref(null)
const externalRenameInputRef = ref(null)
const setWorkspaceFullscreen = inject('setWorkspaceFullscreen', null)

const uiState = reactive({
  cellModalOpen: false,
  cellModalTitle: '',
  cellModalDisplay: '',
  cellModalRaw: '',
  showResultPanel: true,
  resultCellTooltipOpen: false,
  resultCellTooltipKey: '',
  resultCellTooltipText: '',
  resultCellPopoverOpen: false,
  resultCellPopoverKey: '',
  resultCellPopoverCol: '',
  resultCellPopoverRaw: '',
  resultCellPopoverDisplay: '',
  resultCellPopoverMeta: null,
  resultContextMenuOpen: false,
  resultContextMenuX: 0,
  resultContextMenuY: 0,
  resultContextRowKey: '',
  resultContextColumn: '',
  resultContextRaw: '',
  resultContextSubmenuUpward: false,
  resultContextSubmenuLeftward: false,
  copyAsSubmenuOpen: false,
  exportFormatPickerOpen: false,
  exportConfirmOpen: false,
  selectedExportFormat: '',
  workspaceFullscreen: false,
})

const uiData = reactive({
  tableColumns: [],
  tableData: [],
  resultDuration: '',
  executionMessage: '',
  executionView: null,
  resultTab: 'info',
  resultFullscreen: false,
  resultKeyword: '',
  editorHeight: 280,
  pagination: {
    current: 1,
    pageSize: RESULT_PAGE_SIZE,
  },
  resultViewMode: 'row',
  selectedRowKey: '',
  // 已被用户手动关闭的结果集 Tab key 集合（只影响 UI 展示，不删除底层数据）
  closedResultKeys: [],
})

const tabbarPanes = computed(() => consoleRightRef.value?.visiblePanes || [])
const tabbarActiveKey = computed(() => consoleRightRef.value?.uiData?.activeKey || 0)
const tabbarInstances = computed(() => consoleRightRef.value?.uiData?.instances || [])
const tabbarActivePane = computed(() => consoleRightRef.value?.activePane || null)
const activeTabbarIndex = computed(() => tabbarPanes.value.findIndex((pane) => pane.key === tabbarActiveKey.value))
const tabbarScrollState = computed(() => consoleRightRef.value?.tabScrollState || {
  canScrollLeft: false,
  canScrollRight: false,
})
const tabRenamingTabKey = computed(() => consoleRightRef.value?.renamingTabKey || null)
const tabRenamingValue = computed(() => consoleRightRef.value?.renamingValue || '')
const tabDraggingTabKey = computed(() => consoleRightRef.value?.draggingTabKey || null)

const bindExternalTabRefs = () => {
  consoleRightRef.value?.bindTabScrollerRef?.(externalTabScrollerRef.value)
  consoleRightRef.value?.bindRenameInputRef?.(externalRenameInputRef.value)
}

const handleExternalTabWheel = (event) => {
  consoleRightRef.value?.handleTabWheel?.(event)
}

const handleExternalPaneContextMenu = (key, pane) => {
  consoleRightRef.value?.handlePaneContextMenuAction?.(key, pane)
}

const handleExternalCreateTabMenu = (key) => {
  consoleRightRef.value?.handleCreateTabMenuAction?.({ key })
}

const handleExternalGlobalTabAction = (key) => {
  if (key === 'create-blank') {
    consoleRightRef.value?.addBlankConsoleTab?.()
    return
  }
  if (key === 'current' || String(key).startsWith('instance:')) {
    handleExternalCreateTabMenu(key)
    return
  }
  consoleRightRef.value?.handleGlobalTabAction?.({ key })
}

const setExternalRenamingValue = (value) => {
  consoleRightRef.value?.setRenamingValue?.(value)
}

const getTabShellClass = (index, paneKey) => ({
  'is-active': paneKey === tabbarActiveKey.value,
  'is-before-active': activeTabbarIndex.value === index + 1,
  'is-after-active': activeTabbarIndex.value === index - 1,
  'is-first': index === 0,
})

const clearResultNotice = () => {
  resultNotice.type = ''
  resultNotice.title = ''
  resultNotice.detail = ''
  resultNotice.instance_id = ''
}

const handleActivePaneChange = (value) => {
  uiState.showResultPanel = !!value
}

const copyResultNotice = async () => {
  const text = String(resultNotice.detail || '').trim()
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

const retryResultNotice = () => {
  dasConsoleAction.nonce += 1
  dasConsoleAction.type = 'retry-execute'
  dasConsoleAction.payload = { instance_id: resultNotice.instance_id }
}

const editResultNoticeInstance = () => {
  const instanceId = String(resultNotice.instance_id || '').trim()
  router.push({
    path: '/datasource/instance',
    query: instanceId ? { edit_instance_id: instanceId } : {},
  })
}

const EDITOR_HEIGHT_STORAGE_KEY = 'das#editorHeight'
// 最小高度 = 一行 SQL：13px 字号 × 1.6 行高 ≈ 21px + 编辑器容器边框 2px，
// 留 3px 余量取 26（与 ConsoleRight EDITOR_HEIGHT_FLOOR 保持一致）
const MIN_EDITOR_HEIGHT = 26
const MIN_RESULT_HEIGHT = 80
const editorHeightRatio = ref(DEFAULT_EDITOR_RATIO)

const consoleWorkspaceRef = ref(null)
const resultCardRef = ref(null)
const resultTableRegionRef = ref(null)
const resultContextMenuRef = ref(null)
const copyAsSubmenuCloseTimer = ref(null)

const openCopyAsSubmenu = () => {
  if (copyAsSubmenuCloseTimer.value) {
    clearTimeout(copyAsSubmenuCloseTimer.value)
    copyAsSubmenuCloseTimer.value = null
  }
  uiState.copyAsSubmenuOpen = true
}

const scheduleCloseCopyAsSubmenu = (delay = 600) => {
  if (copyAsSubmenuCloseTimer.value) {
    clearTimeout(copyAsSubmenuCloseTimer.value)
  }
  copyAsSubmenuCloseTimer.value = setTimeout(() => {
    uiState.copyAsSubmenuOpen = false
    copyAsSubmenuCloseTimer.value = null
  }, delay)
}

const forceCloseCopyAsSubmenu = () => {
  if (copyAsSubmenuCloseTimer.value) {
    clearTimeout(copyAsSubmenuCloseTimer.value)
    copyAsSubmenuCloseTimer.value = null
  }
  uiState.copyAsSubmenuOpen = false
}
const resultContextRowRef = ref(null)
const resultContextCellEl = ref(null)
const tableBodyScrollY = ref(null)
const tableScrollX = ref(0)
const resultRegionWidth = ref(0)
const tableContentWidth = ref(0)
const RESULT_CONTEXT_MENU_WIDTH = 110
const RESULT_CONTEXT_SUBMENU_WIDTH = 70
const RESULT_CONTEXT_MENU_HEIGHT = 190
const RESULT_CONTEXT_SUBMENU_HEIGHT = 138

const resultContextMenuStyle = computed(() => ({
  left: `${uiState.resultContextMenuX}px`,
  top: `${uiState.resultContextMenuY}px`,
}))

const tableScroll = computed(() => {
  const x = Math.max(tableScrollX.value, tableContentWidth.value || 0) || undefined
  return {
    y: tableBodyScrollY.value ?? 360,
    ...(x ? { x } : {}),
  }
})
const hasHorizontalScroll = computed(() => tableScrollX.value > 0)
const tableContentWidthCss = computed(() => `${Math.max(tableContentWidth.value, DEFAULT_COLUMN_WIDTH)}px`)

const normalizeSearchText = (value) => String(value || '').trim().toLowerCase()

const resultTabItems = computed(() => {
  const sets = Array.isArray(uiData.executionView?.resultSets) ? uiData.executionView.resultSets : []
  if (sets.length) {
    return sets.map((set, index) => ({
      ...set,
      label: `结果集 ${index + 1}`,
    }))
  }
  return [{ key: 'result', label: '结果集 1', result: null }]
})

// 过滤掉被用户手动关闭的结果集 Tab，避免重渲染后它们又冒出来
const visibleResultTabItems = computed(() =>
  resultTabItems.value.filter((item) => !uiData.closedResultKeys.includes(item.key)),
)

const currentResultSet = computed(() => visibleResultTabItems.value.find((item) => item.key === uiData.resultTab) || null)

const canUseResultActions = computed(() => !!currentResultSet.value?.result)

const filteredTableData = computed(() => {
  const keyword = normalizeSearchText(uiData.resultKeyword)
  if (!keyword) return uiData.tableData || []
  const columns = uiData.tableColumns || []
  return (uiData.tableData || []).filter((row) =>
    columns.some((col) => normalizeSearchText(normalizeCellValue(row?.[col])).includes(keyword)),
  )
})

const paginatedTableData = computed(() => {
  const current = uiData.pagination.current || 1
  const pageSize = uiData.pagination.pageSize || RESULT_PAGE_SIZE
  const start = (current - 1) * pageSize
  return filteredTableData.value.slice(start, start + pageSize)
})

const ensureSelectedRowValid = () => {
  const rows = filteredTableData.value || []
  if (!rows.length) {
    uiData.selectedRowKey = ''
    return
  }
  const exists = rows.some((r) => String(r.__goinsightRowKey) === String(uiData.selectedRowKey))
  if (!exists) {
    uiData.selectedRowKey = String(rows[0].__goinsightRowKey)
  }
}

const selectedRowForColumnView = computed(() => {
  const rows = filteredTableData.value || []
  if (!rows.length) return null
  ensureSelectedRowValid()
  return rows.find((r) => String(r.__goinsightRowKey) === String(uiData.selectedRowKey)) || rows[0]
})

const setResultViewMode = (mode) => {
  if (!['row', 'column'].includes(mode)) return
  if (mode === 'column') {
    ensureSelectedRowValid()
  }
  uiData.resultViewMode = mode
  nextTick(recomputeTableBodyHeight)
}

const handleSelectRow = (record) => {
  if (!record) return
  uiData.selectedRowKey = String(record.__goinsightRowKey)
}

const formatDuration = (value) => {
  if (value === null || value === undefined || value === '') return '-'
  const text = String(value).trim()
  if (!text) return '-'
  const matched = text.match(/^(\d+(?:\.\d+)?)(ms|s)?$/i)
  if (!matched) return text
  const amount = Number(matched[1])
  if (Number.isNaN(amount)) return text
  const unit = (matched[2] || 'ms').toLowerCase()
  const milliseconds = unit === 's' ? amount * 1000 : amount
  if (milliseconds < 1000) {
    return `${Math.round(milliseconds)} 毫秒`
  }
  const seconds = milliseconds / 1000
  const roundedSeconds = seconds >= 10 ? seconds.toFixed(1) : seconds.toFixed(2)
  return `${roundedSeconds.replace(/\.0$/, '').replace(/(\.\d*[1-9])0$/, '$1')} 秒`
}

const formattedDuration = computed(() => formatDuration(uiData.resultDuration))

const DEFAULT_COLUMN_WIDTH = 88
const MIN_COLUMN_WIDTH = 56
const MAX_COLUMN_WIDTH = 200
const LONG_TEXT_MAX_COLUMN_WIDTH = 280
const HEADER_WIDTH_CHAR_LIMIT = 18
const CELL_SAMPLE_CHAR_LIMIT = 50
const MAX_SAMPLE_ROWS = 30
const TABLE_HORIZONTAL_GUTTER = 24
const DEFAULT_ROW_HEIGHT = 34
const MIN_ROW_HEIGHT = 28
const MAX_ROW_HEIGHT = 72
const HEADER_RESIZE_HOTZONE = 10
const ROW_RESIZE_HOTZONE = 6

const baseColumnWidths = ref({})
const manualColumnWidths = ref({})
const tableRowHeight = ref(DEFAULT_ROW_HEIGHT)
const rowHeightCss = computed(() => `${tableRowHeight.value}px`)
const resizeState = reactive({
  mode: '',
  column: '',
  startX: 0,
  startY: 0,
  startWidth: DEFAULT_COLUMN_WIDTH,
  startHeight: DEFAULT_ROW_HEIGHT,
})

const normalizeCellValue = (value) => {
  if (value === null || value === undefined) return ''
  if (typeof value === 'string') return value
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}

const tryFormatJson = (value) => {
  const text = normalizeCellValue(value).trim()
  if (!text) return { isJson: false, formatted: '' }
  if (!(text.startsWith('{') || text.startsWith('['))) return { isJson: false, formatted: '' }
  try {
    const obj = JSON.parse(text)
    return { isJson: true, formatted: JSON.stringify(obj, null, 2) }
  } catch {
    return { isJson: false, formatted: '' }
  }
}

const openCellModal = (columnName, rawValue) => {
  const raw = normalizeCellValue(rawValue)
  const { isJson, formatted } = tryFormatJson(raw)
  uiState.cellModalTitle = columnName || '单元格内容'
  uiState.cellModalRaw = raw
  uiState.cellModalDisplay = isJson ? formatted : raw
  uiState.cellModalOpen = true
}

const closeResultCellTooltip = () => {
  uiState.resultCellTooltipOpen = false
  uiState.resultCellTooltipKey = ''
  uiState.resultCellTooltipText = ''
}

const closeResultCellPopover = () => {
  uiState.resultCellPopoverOpen = false
  uiState.resultCellPopoverKey = ''
  uiState.resultCellPopoverCol = ''
  uiState.resultCellPopoverRaw = ''
  uiState.resultCellPopoverDisplay = ''
  uiState.resultCellPopoverMeta = null
}

const classifyCellContent = (raw) => {
  const text = raw || ''
  const charCount = text.length
  const lineCount = text === '' ? 0 : text.split(/\r?\n/).length
  const newlines = lineCount - 1
  const { isJson, formatted } = tryFormatJson(text)
  let category = 'normal'
  if (charCount > RESULT_CELL_LONG_CHAR_LIMIT || lineCount > RESULT_CELL_LONG_LINE_LIMIT || newlines >= 6) {
    category = 'long'
  } else if (isJson && formatted.length > RESULT_CELL_TOOLTIP_CHAR_LIMIT) {
    category = 'long'
  } else if (charCount > RESULT_CELL_TOOLTIP_CHAR_LIMIT) {
    category = 'long'
  }
  return {
    category,
    charCount,
    lineCount,
    newlines,
    isJson,
    display: isJson ? formatted : text,
  }
}

const openResultCellTooltip = (key, rawValue, event) => {
  const container = event?.currentTarget
  if (uiState.resultCellPopoverOpen) return
  const raw = normalizeCellValue(rawValue)
  const info = classifyCellContent(raw)
  if (info.category === 'long') {
    if (uiState.resultCellTooltipKey === key) closeResultCellTooltip()
    return
  }
  let definitelyClipped = false
  if (raw.length >= RESULT_CELL_MIN_CHARS_TO_TIP) definitelyClipped = true
  if (info.lineCount > 1 || info.newlines >= 1) definitelyClipped = true
  if (container instanceof HTMLElement) {
    const checkTargets = [
      container,
      container.querySelector('.cell-ellipsis'),
      container.parentElement,
      container.closest('.gi-result-body-cell, .ant-table-cell'),
    ].filter(Boolean)
    for (const el of checkTargets) {
      if (!(el instanceof HTMLElement)) continue
      const dx = el.scrollWidth - el.clientWidth
      if (dx > 0.5) {
        definitelyClipped = true
        break
      }
    }
  }
  if (!definitelyClipped) {
    if (raw.length <= RESULT_CELL_MAX_CHARS_NO_TIP) {
      if (uiState.resultCellTooltipKey === key) closeResultCellTooltip()
      return
    }
  }
  uiState.resultCellTooltipKey = key
  uiState.resultCellTooltipText = info.display
  uiState.resultCellTooltipOpen = true
}

const toggleResultCellPopover = (col, rawValue, rowKey, event) => {
  if (event) {
    event.preventDefault()
    event.stopPropagation()
  }
  const key = `${rowKey}::${col}`
  if (uiState.resultCellPopoverOpen && uiState.resultCellPopoverKey === key) {
    closeResultCellPopover()
    return
  }
  const raw = normalizeCellValue(rawValue)
  const info = classifyCellContent(raw)
  uiState.resultCellPopoverCol = col
  uiState.resultCellPopoverKey = key
  uiState.resultCellPopoverRaw = raw
  uiState.resultCellPopoverDisplay = info.display
  uiState.resultCellPopoverMeta = {
    charCount: info.charCount,
    lineCount: info.lineCount,
    isJson: info.isJson,
  }
  uiState.resultCellPopoverOpen = true
  closeResultCellTooltip()
}

const copyResultCellPopoverValue = async () => {
  const text = uiState.resultCellPopoverRaw || ''
  if (!text) {
    message.warning('内容为空')
    return
  }
  await copyText(text, '已复制单元格内容')
}

const openResultCellPopoverFullModal = () => {
  const col = uiState.resultCellPopoverCol || ''
  const raw = uiState.resultCellPopoverRaw || ''
  closeResultCellPopover()
  openCellModal(col, raw)
}

const copyCellValue = async () => {
  const text = uiState.cellModalRaw || ''
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
    } else {
      const el = document.createElement('textarea')
      el.value = text
      el.style.position = 'fixed'
      el.style.left = '-9999px'
      el.style.top = '0'
      document.body.appendChild(el)
      el.focus()
      el.select()
      document.execCommand('copy')
      document.body.removeChild(el)
    }
    message.success('已复制')
  } catch {
    message.error('复制失败')
  }
}

const copyText = async (text, successMessage = '已复制') => {
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
    } else {
      const el = document.createElement('textarea')
      el.value = text
      el.style.position = 'fixed'
      el.style.left = '-9999px'
      el.style.top = '0'
      document.body.appendChild(el)
      el.focus()
      el.select()
      document.execCommand('copy')
      document.body.removeChild(el)
    }
    message.success(successMessage)
  } catch {
    message.error('复制失败')
  }
}

const buildContextRowPayload = (row) => {
  const payload = {}
  for (const col of uiData.tableColumns || []) {
    payload[col] = row?.[col] ?? null
  }
  return payload
}

const toSqlLiteral = (value, dbType) => {
  if (value === null || value === undefined) return 'NULL'
  if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  if (typeof value === 'boolean') return value ? 'TRUE' : 'FALSE'
  const text =
    typeof value === 'object'
      ? JSON.stringify(value)
      : String(value)
  const isMysqlFamily = normalizeDbTypeForIdentifier(dbType) === 'mysql'
  let escaped = text
  if (isMysqlFamily) {
    escaped = escaped
      .replace(/\\/g, '\\\\')
      .replace(/\u0000/g, '\\0')
      .replace(/\n/g, '\\n')
      .replace(/\r/g, '\\r')
      .replace(/\x1a/g, '\\Z')
  }
  escaped = escaped.replaceAll("'", "''")
  return `'${escaped}'`
}

const escapeXmlText = (value) =>
  String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&apos;')

/* 标识符/字面量的转义风格：MySQL 系（含 OceanBase MySQL 模式）用反引号与反斜杠转义，
   其余用标准双引号 + '' 转义。
   入参应是「引擎家族」（ConsoleRight 暴露的 getPaneFamily），也可能是 db_type；
   resolveDbFamily 对家族值幂等，对 db_type 会做归一（OceanBase 缺上下文时回落 MySQL 模式，
   因此调用方应优先传家族）。 */
const normalizeDbTypeForIdentifier = (value) => {
  const raw = String(value ?? '').trim()
  if (!raw) return 'mysql'
  const family = resolveDbFamily(raw)
  if (family === FAMILY_MYSQL || family === FAMILY_CLICKHOUSE) return 'mysql'
  if (family === FAMILY_POSTGRES) return 'postgresql'
  return family
}

const quoteIdentifier = (name, dbType) => {
  const n = String(name || '')
  const quote = normalizeDbTypeForIdentifier(dbType) === 'mysql' ? '`' : '"'
  const escapeChar = quote
  return `${quote}${n.split(escapeChar).join(escapeChar + escapeChar)}${quote}`
}

const extractFirstTableNameFromSql = (sqlText) => {
  const raw = String(sqlText || '').trim()
  if (!raw) return ''
  let sql = raw
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/--.*$/gm, ' ')
    .replace(/\s+/g, ' ')
    .trim()
  const fromMatch = sql.match(/\bFROM\s+(?:ONLY\s+)?([`"\[]?[\w\u4e00-\u9fa5$_\-]+[`"\]]?(?:\.[`"\[]?[\w\u4e00-\u9fa5$_\-]+[`"\]]?){0,2})/i)
  if (fromMatch && fromMatch[1]) {
    const tableRef = String(fromMatch[1] || '').trim()
    const parts = tableRef.split('.').map((p) => String(p || '').trim().replace(/^[`"\[]|[`"\]]$/g, ''))
    if (parts.length) {
      const last = parts[parts.length - 1]
      if (last) return last
    }
  }
  return ''
}

const buildContextRowAsInsertSql = (row) => {
  const active = tabbarActivePane.value || null
  // 优先要「家族」（OceanBase 需按模式决定引号/转义），拿不到再退回展示用 db_type
  const dbType =
    consoleRightRef.value?.getPaneFamily?.(active) ||
    consoleRightRef.value?.getPaneDbType?.(active) ||
    tabbarActivePane.value?.db_type ||
    'mysql'
  const columns = (uiData.tableColumns || []).filter((c) => !String(c || '').startsWith('__goinsight'))
  if (!columns.length) return ''
  const rawTableName =
    currentResultSet.value?.result?.table_name ||
    currentResultSet.value?.result?.tableName ||
    extractFirstTableNameFromSql(currentResultSet.value?.sqltext || currentResultSet.value?.result?.sqltext) ||
    'table_name'
  const tablePart = quoteIdentifier(rawTableName, dbType)
  const colsPart = columns.map((c) => quoteIdentifier(c, dbType)).join(', ')
  const valsPart = columns.map((c) => toSqlLiteral(row?.[c], dbType)).join(', ')
  return `INSERT INTO ${tablePart} (${colsPart}) VALUES (${valsPart});`
}

const buildContextRowXml = (row) => {
  const payload = buildContextRowPayload(row)
  const body = Object.entries(payload)
    .map(([key, value]) => `  <${key}>${escapeXmlText(normalizeCellValue(value))}</${key}>`)
    .join('\n')
  return `<row>\n${body}\n</row>`
}

const isSelectedResultCell = (rowKey, column) =>
  String(uiState.resultContextRowKey || '') === String(rowKey || '') && uiState.resultContextColumn === column

const clearResultContextCell = () => {
  uiState.resultContextRowKey = ''
  uiState.resultContextColumn = ''
  uiState.resultContextRaw = ''
  resultContextRowRef.value = null
  resultContextCellEl.value = null
}

const closeResultContextMenu = () => {
  forceCloseCopyAsSubmenu()
  uiState.resultContextMenuOpen = false
  uiState.resultContextSubmenuUpward = false
  uiState.resultContextSubmenuLeftward = false
}

const setResultContextCell = ({ row, column, rawValue, element }) => {
  uiState.resultContextRowKey = String(row?.__goinsightRowKey || '')
  uiState.resultContextColumn = column || ''
  uiState.resultContextRaw = normalizeCellValue(rawValue)
  resultContextRowRef.value = row || null
  resultContextCellEl.value = element instanceof HTMLElement ? element : null
  if (row) {
    uiData.selectedRowKey = String(row.__goinsightRowKey)
  }
}

const clampResultContextMenuPosition = (clientX, clientY) => {
  if (typeof window === 'undefined') {
    return { x: clientX, y: clientY, flipUpward: false, flipLeftward: false, submenuUpward: false }
  }
  const padding = 12
  const viewportW = window.innerWidth
  const viewportH = window.innerHeight
  const menuW = RESULT_CONTEXT_MENU_WIDTH
  const menuH = RESULT_CONTEXT_MENU_HEIGHT
  const subW = RESULT_CONTEXT_SUBMENU_WIDTH
  const subH = RESULT_CONTEXT_SUBMENU_HEIGHT
  const flipUpward = clientY + menuH > viewportH - padding
  const flipLeftward = clientX + menuW + subW + padding + 6 > viewportW
  let x = flipLeftward
    ? Math.max(padding, clientX - menuW)
    : Math.min(Math.max(clientX, padding), Math.max(padding, viewportW - menuW - subW - padding - 6))
  let y = flipUpward
    ? Math.max(padding, clientY - menuH)
    : Math.min(Math.max(clientY, padding), Math.max(padding, viewportH - menuH - padding))
  const submenuAnchorTop = flipUpward
    ? y + 40
    : y + menuH - 40
  const submenuUpward =
    flipUpward
      ? submenuAnchorTop - subH < padding
      : submenuAnchorTop + subH > viewportH - padding
  return { x, y, flipUpward, flipLeftward, submenuUpward }
}

const openResultContextMenu = (event, column, row, rawValue) => {
  if (!canUseResultActions.value || !row) return
  event.preventDefault()
  event.stopPropagation()
  closeResultCellTooltip()
  const position = clampResultContextMenuPosition(event.clientX, event.clientY)
  const { x, y, flipLeftward, submenuUpward } = position
  uiState.resultContextSubmenuUpward = !!submenuUpward
  uiState.resultContextSubmenuLeftward = !!flipLeftward
  setResultContextCell({
    row,
    column,
    rawValue,
    element: event.currentTarget,
  })
  uiState.resultContextMenuX = x
  uiState.resultContextMenuY = y
  forceCloseCopyAsSubmenu()
  uiState.resultContextMenuOpen = true
}

const handleSelectContextCell = () => {
  const cellEl = resultContextCellEl.value
  if (typeof window === 'undefined' || !(cellEl instanceof HTMLElement)) {
    closeResultContextMenu()
    return
  }
  const selection = window.getSelection?.()
  if (!selection) {
    closeResultContextMenu()
    return
  }
  const range = document.createRange()
  range.selectNodeContents(cellEl)
  selection.removeAllRanges()
  selection.addRange(range)
  closeResultContextMenu()
}

const handleCopyContextCell = async () => {
  await copyText(uiState.resultContextRaw || '', '已复制单元格')
  closeResultContextMenu()
}

const handleCopyContextRow = async () => {
  const row = resultContextRowRef.value
  if (!row) {
    message.warning('当前没有可复制的行')
    return
  }
  const text = (uiData.tableColumns || []).map((col) => normalizeCellValue(row?.[col])).join('\t')
  await copyText(text, '已复制当前行')
  closeResultContextMenu()
}

const handleCopyContextColumn = async () => {
  const column = uiState.resultContextColumn
  if (!column) {
    message.warning('当前没有可复制的列')
    return
  }
  const rows = filteredTableData.value || []
  if (!rows.length) {
    message.warning('当前结果集中没有可复制的数据')
    return
  }
  const text = rows.map((row) => normalizeCellValue(row?.[column])).join('\n')
  await copyText(text, `已复制当前列（${rows.length} 行）`)
  closeResultContextMenu()
}

const handleCopyCurrentPageRows = async () => {
  if (!paginatedTableData.value.length) {
    message.warning('当前页没有可复制的数据')
    closeResultContextMenu()
    return
  }
  await copyText(buildDelimitedText(paginatedTableData.value, '\t'), `已复制当前页（${paginatedTableData.value.length} 行）`)
  closeResultContextMenu()
}

const handleCopyContextAs = async (format) => {
  const row = resultContextRowRef.value
  if (!row) {
    message.warning('当前没有可复制的行')
    return
  }
  const payload = buildContextRowPayload(row)
  let text = ''
  let successMessage = '已复制'
  if (format === 'sql') {
    text = buildContextRowAsInsertSql(row)
    successMessage = '已复制为 SQL'
  } else if (format === 'csv') {
    text = buildDelimitedText([payload], ',', false, true)
    successMessage = '已复制为 CSV'
  } else if (format === 'json') {
    text = JSON.stringify(payload, null, 2)
    successMessage = '已复制为 JSON'
  } else if (format === 'xml') {
    text = buildContextRowXml(row)
    successMessage = '已复制为 XML'
  }
  await copyText(text, successMessage)
  closeResultContextMenu()
}

const handleResultContextMenuPointerDown = (event) => {
  if (!uiState.resultContextMenuOpen) return
  const target = event?.target
  if (target instanceof Node && resultContextMenuRef.value?.contains?.(target)) return
  closeResultContextMenu()
}

const handleResultContextMenuKeydown = (event) => {
  if (event.key === 'Escape') {
    if (uiState.workspaceFullscreen) {
      event.preventDefault()
      exitWorkspaceFullscreen()
      return
    }
    closeResultContextMenu()
  }
}

const handleResultContextMenuScroll = () => {
  if (uiState.resultContextMenuOpen) {
    closeResultContextMenu()
  }
}

const escapeDelimitedCell = (value, delimiter = ',', forceQuote = false) => {
  const raw = normalizeCellValue(value)
  if (!raw) return forceQuote ? '""' : ''
  const escaped = raw.replaceAll('"', '""')
  const needQuote =
    forceQuote ||
    escaped.includes('"') ||
    escaped.includes('\n') ||
    escaped.includes('\r') ||
    escaped.includes(delimiter)
  return needQuote ? `"${escaped}"` : escaped
}

const buildDelimitedText = (rows = [], delimiter = ',', includeHeader = true, forceQuote = false) => {
  const columns = uiData.tableColumns || []
  const body = rows.map((row) =>
    columns.map((col) => escapeDelimitedCell(row?.[col], delimiter, forceQuote)).join(delimiter),
  )
  if (!includeHeader) return body.join('\n')
  const header = columns.map((col) => escapeDelimitedCell(col, delimiter, forceQuote)).join(delimiter)
  return [header, ...body].join('\n')
}

const downloadTextFile = (filename, text, mimeType) => {
  if (typeof window === 'undefined') return
  const blob = new Blob([text], { type: mimeType })
  const url = window.URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  window.URL.revokeObjectURL(url)
}

const copyCurrentPageRows = async () => {
  if (!paginatedTableData.value.length) {
    message.warning('当前页没有可复制的数据')
    return
  }
  await copyText(buildDelimitedText(paginatedTableData.value, '\t'), '已复制当前页结果')
}

const formatSqlValue = (value) => {
  const raw = normalizeCellValue(value)
  if (raw === '') return 'NULL'
  if (typeof value === 'number' && !Number.isNaN(value)) return String(value)
  if (value === true) return '1'
  if (value === false) return '0'
  const text = raw.replaceAll('\\', '\\\\').replaceAll("'", "\\'")
  return `'${text}'`
}

const buildInsertSqlText = (rows = [], dbType = 'mysql') => {
  const columns = (uiData.tableColumns || []).filter((c) => !String(c || '').startsWith('__goinsight'))
  if (!columns.length) return ''
  const rawTableName =
    currentResultSet.value?.result?.table_name ||
    currentResultSet.value?.result?.tableName ||
    extractFirstTableNameFromSql(currentResultSet.value?.sqltext || currentResultSet.value?.result?.sqltext) ||
    'sql_result'
  const tablePart = quoteIdentifier(rawTableName, dbType)
  const colsPart = columns.map((c) => quoteIdentifier(c, dbType)).join(', ')
  const body = rows.map((row) => {
    const values = columns.map((col) => toSqlLiteral(row?.[col], dbType)).join(', ')
    return `INSERT INTO ${tablePart} (${colsPart}) VALUES (${values});`
  })
  return body.join('\n')
}

const exportFormatMeta = computed(() => ({
  csv: { key: 'csv', label: 'CSV', mime: 'text/csv;charset=utf-8', ext: 'csv' },
  json: { key: 'json', label: 'JSON', mime: 'application/json;charset=utf-8', ext: 'json' },
  sql: { key: 'sql', label: 'SQL', mime: 'text/plain;charset=utf-8', ext: 'sql' },
}))

const selectedExportMeta = computed(() => {
  const key = String(uiState.selectedExportFormat || '').trim()
  return exportFormatMeta.value[key] || null
})

const buildExportText = (format) => {
  const rows = filteredTableData.value || []
  if (format === 'json') return JSON.stringify(rows, null, 2)
  if (format === 'sql') {
    const active = tabbarActivePane.value || null
    // 优先要「家族」（OceanBase 需按模式决定引号/转义），拿不到再退回展示用 db_type
  const dbType =
    consoleRightRef.value?.getPaneFamily?.(active) ||
    consoleRightRef.value?.getPaneDbType?.(active) ||
    tabbarActivePane.value?.db_type ||
    'mysql'
    return buildInsertSqlText(rows, dbType)
  }
  return buildDelimitedText(rows, ',')
}

const exportFilteredResults = (format) => {
  if (!filteredTableData.value.length) {
    message.warning('当前没有可导出的数据')
    return
  }
  const meta = exportFormatMeta.value[format]
  if (!meta) {
    message.warning('暂不支持该导出格式')
    return
  }
  const timestamp = new Date().toISOString().replaceAll(':', '').replaceAll('-', '').replace('T', '-').slice(0, 15)
  downloadTextFile(
    `goinsight-sql-result-${timestamp}.${meta.ext}`,
    buildExportText(format),
    meta.mime,
  )
}

const closeExportFormatPicker = () => {
  uiState.exportFormatPickerOpen = false
}

const handlePickExportFormat = (format) => {
  const meta = exportFormatMeta.value[format]
  if (!meta) return
  if (!filteredTableData.value.length) {
    message.warning('当前没有可导出的数据')
    return
  }
  closeExportFormatPicker()
  uiState.selectedExportFormat = format
  uiState.exportConfirmOpen = true
}

const cancelExportConfirm = () => {
  uiState.exportConfirmOpen = false
}

const executeConfirmExport = () => {
  const format = String(uiState.selectedExportFormat || '').trim()
  const meta = exportFormatMeta.value[format]
  if (!meta) {
    message.warning('请先选择导出格式')
    return
  }
  if (!filteredTableData.value.length) {
    message.warning('当前没有可导出的数据')
    uiState.exportConfirmOpen = false
    return
  }
  exportFilteredResults(format)
  uiState.exportConfirmOpen = false
}

const confirmExportResults = (format) => {
  if (!filteredTableData.value.length) {
    message.warning('当前没有可导出的数据')
    return
  }
  uiState.selectedExportFormat = format
  uiState.exportConfirmOpen = true
}

const getMeasureCtx = () => {
  if (typeof document === 'undefined') return null
  const canvas = document.createElement('canvas')
  const ctx = canvas.getContext('2d')
  if (!ctx) return null
  ctx.font = '12px -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Helvetica,Arial'
  return ctx
}

const estimateColumnWidth = (columnName, rows) => {
  const ctx = getMeasureCtx()
  const sample = Array.isArray(rows) ? rows.slice(0, MAX_SAMPLE_ROWS) : []
  const headerText = String(columnName || '')
  const headerForWidth =
    headerText.length > HEADER_WIDTH_CHAR_LIMIT ? `${headerText.slice(0, HEADER_WIDTH_CHAR_LIMIT)}…` : headerText
  let maxPx = ctx ? ctx.measureText(headerForWidth).width : headerForWidth.length * 8
  let longestSample = 0
  for (const row of sample) {
    const cell = normalizeCellValue(row?.[columnName])
    const compact = cell.length > CELL_SAMPLE_CHAR_LIMIT ? `${cell.slice(0, CELL_SAMPLE_CHAR_LIMIT)}…` : cell
    const px = ctx ? ctx.measureText(compact).width : compact.length * 7
    maxPx = Math.max(maxPx, px)
    longestSample = Math.max(longestSample, cell.length)
  }
  const padding = 22
  const looksLikeLongText = /json|body|content|message|remark|comment|desc|detail|payload|response|request|sql/i.test(
    headerText,
  )
  const max = looksLikeLongText || longestSample > 50 ? LONG_TEXT_MAX_COLUMN_WIDTH : MAX_COLUMN_WIDTH
  const estimated = Math.ceil(maxPx + padding)
  const withBreathing = estimated < max ? Math.min(max, estimated + 35) : estimated
  return Math.max(MIN_COLUMN_WIDTH, Math.min(max, withBreathing))
}

const sumWidths = (widthMap, columns) =>
  (columns || []).reduce((acc, col) => acc + Number(widthMap?.[col] || 0), 0)

const syncColgroupWidthsAcrossTables = () => {
  if (typeof document === 'undefined') return
  const regionEl = resultTableRegionRef.value
  if (!regionEl) return
  const scrollEl = regionEl.querySelector('.ant-table-scroll')
  if (!scrollEl) return
  const headerCols = scrollEl.querySelectorAll('.ant-table-header colgroup > col')
  const bodyCols = scrollEl.querySelectorAll('.ant-table-body colgroup > col, .ant-table-body-inner colgroup > col')
  if (!headerCols?.length || !bodyCols?.length) return
  const len = Math.min(headerCols.length, bodyCols.length)
  for (let i = 0; i < len; i++) {
    const headerCol = headerCols[i]
    const bodyCol = bodyCols[i]
    const w = headerCol.getAttribute('width') || headerCol.style.width || ''
    if (!w) continue
    if (headerCol.getAttribute('width') !== w) {
      headerCol.setAttribute('width', w)
    }
    if (bodyCol.getAttribute('width') !== w) {
      bodyCol.setAttribute('width', w)
    }
    if (headerCol.style.width !== w) headerCol.style.width = w
    if (bodyCol.style.width !== w) bodyCol.style.width = w
  }
}

const recomputeColumnLayout = () => {
  const columns = uiData.tableColumns || []
  if (!columns.length) {
    baseColumnWidths.value = {}
    tableContentWidth.value = 0
    tableScrollX.value = 0
    nextTick(() => syncColgroupWidthsAcrossTables())
    return
  }

  const estimatedBase = {}
  for (const col of columns) {
    estimatedBase[col] = estimateColumnWidth(col, uiData.tableData)
  }

  baseColumnWidths.value = estimatedBase

  const effective = {}
  for (const col of columns) {
    effective[col] = manualColumnWidths.value?.[col] || estimatedBase[col] || DEFAULT_COLUMN_WIDTH
  }

  const total = sumWidths(effective, columns)
  tableContentWidth.value = total
  const availableWidth = Math.max(0, Math.floor((resultRegionWidth.value || 0) - TABLE_HORIZONTAL_GUTTER))
  tableScrollX.value = availableWidth > 0 && total > availableWidth ? total + TABLE_HORIZONTAL_GUTTER : 0
  nextTick(() => syncColgroupWidthsAcrossTables())
}

const clampColumnWidth = (width) => Math.max(MIN_COLUMN_WIDTH, Math.min(LONG_TEXT_MAX_COLUMN_WIDTH, Math.round(width)))
const clampRowHeight = (height) => Math.max(MIN_ROW_HEIGHT, Math.min(MAX_ROW_HEIGHT, Math.round(height)))
const clearResizeCursor = (target) => {
  if (target?.style) target.style.cursor = ''
}

const isInHeaderResizeZone = (event) => {
  const rect = event.currentTarget?.getBoundingClientRect?.()
  if (!rect) return false
  return rect.right - event.clientX <= HEADER_RESIZE_HOTZONE
}

const isInBodyColumnResizeZone = (event) => {
  const rect = event.currentTarget?.getBoundingClientRect?.()
  if (!rect) return false
  return rect.right - event.clientX <= HEADER_RESIZE_HOTZONE
}

const isInBodyRowResizeZone = (event) => {
  const rect = event.currentTarget?.getBoundingClientRect?.()
  if (!rect) return false
  return rect.bottom - event.clientY <= ROW_RESIZE_HOTZONE
}

const isInRowResizeZone = (event) => {
  const rect = event.currentTarget?.getBoundingClientRect?.()
  if (!rect) return false
  return rect.bottom - event.clientY <= ROW_RESIZE_HOTZONE
}

const autoFitColumn = (col) => {
  const width = estimateColumnWidth(col, uiData.tableData)
  manualColumnWidths.value = { ...(manualColumnWidths.value || {}), [col]: width }
  recomputeColumnLayout()
}

const stopResizeInteraction = () => {
  if (!resizeState.mode || typeof document === 'undefined') return
  resizeState.mode = ''
  resizeState.column = ''
  document.documentElement.style.userSelect = ''
  if (document.documentElement.style.webkitUserSelect) {
    document.documentElement.style.webkitUserSelect = ''
  }
  if (document.body?.style) {
    document.body.style.userSelect = ''
    if (document.body.style.webkitUserSelect) {
      document.body.style.webkitUserSelect = ''
    }
  }
  document.removeEventListener('mousemove', handleResizeInteractionMove)
  document.removeEventListener('mouseup', stopResizeInteraction)
}

const handleSelectionLockRecovery = () => {
  if (typeof document === 'undefined') return
  stopResizeInteraction()
  stopWorkspaceResize()
  document.documentElement.style.userSelect = ''
  if (document.documentElement.style.webkitUserSelect) {
    document.documentElement.style.webkitUserSelect = ''
  }
  if (document.body?.style) {
    document.body.style.userSelect = ''
    if (document.body.style.webkitUserSelect) {
      document.body.style.webkitUserSelect = ''
    }
  }
}

const handleEditorMouseDownRecovery = (event) => {
  const target = event?.target
  if (!(target instanceof HTMLElement)) return
  if (!target.closest('.cm-editor')) return
  handleSelectionLockRecovery()
}

const handleVisibilityLockRecovery = () => {
  if (typeof document === 'undefined' || document.visibilityState === 'visible') return
  handleSelectionLockRecovery()
}

const handleResizeInteractionMove = (event) => {
  if (resizeState.mode === 'column' && resizeState.column) {
    const nextWidth = clampColumnWidth(resizeState.startWidth + (event.clientX - resizeState.startX))
    manualColumnWidths.value = { ...(manualColumnWidths.value || {}), [resizeState.column]: nextWidth }
    recomputeColumnLayout()
    nextTick(() => syncColgroupWidthsAcrossTables())
    return
  }

  if (resizeState.mode === 'row') {
    tableRowHeight.value = clampRowHeight(resizeState.startHeight + (event.clientY - resizeState.startY))
  }
}

const startColumnResize = (col, event) => {
  event.preventDefault()
  event.stopPropagation()
  if (typeof document === 'undefined') return
  resizeState.mode = 'column'
  resizeState.column = col
  resizeState.startX = event.clientX
  resizeState.startWidth =
    manualColumnWidths.value?.[col] || baseColumnWidths.value?.[col] || DEFAULT_COLUMN_WIDTH
  document.documentElement.style.userSelect = 'none'
  if (document.documentElement.style.webkitUserSelect !== undefined) {
    document.documentElement.style.webkitUserSelect = 'none'
  }
  if (document.body?.style) {
    document.body.style.userSelect = 'none'
    if (document.body.style.webkitUserSelect !== undefined) {
      document.body.style.webkitUserSelect = 'none'
    }
  }
  document.addEventListener('mousemove', handleResizeInteractionMove)
  document.addEventListener('mouseup', stopResizeInteraction)
}

const maybeStartColumnResize = (col, event) => {
  if (!isInHeaderResizeZone(event)) return
  startColumnResize(col, event)
}

const handleHeaderResizeMouseMove = (event) => {
  if (resizeState.mode === 'column') return
  event.currentTarget.style.cursor = isInHeaderResizeZone(event) ? 'col-resize' : ''
}

const maybeStartBodyCellResize = (col, event) => {
  if (isInBodyColumnResizeZone(event)) {
    startColumnResize(col, event)
    return
  }
  if (isInBodyRowResizeZone(event)) {
    startRowResize(event)
    return
  }
}

const handleBodyCellResizeMouseMove = (event) => {
  if (resizeState.mode) return
  const target = event.currentTarget
  if (isInBodyColumnResizeZone(event)) {
    target.style.cursor = 'col-resize'
    return
  }
  if (isInBodyRowResizeZone(event)) {
    target.style.cursor = 'row-resize'
    return
  }
  clearResizeCursor(target)
}

const startRowResize = (event) => {
  event.preventDefault()
  event.stopPropagation()
  if (typeof document === 'undefined') return
  resizeState.mode = 'row'
  resizeState.startY = event.clientY
  resizeState.startHeight = tableRowHeight.value
  document.documentElement.style.userSelect = 'none'
  if (document.documentElement.style.webkitUserSelect !== undefined) {
    document.documentElement.style.webkitUserSelect = 'none'
  }
  if (document.body?.style) {
    document.body.style.userSelect = 'none'
    if (document.body.style.webkitUserSelect !== undefined) {
      document.body.style.webkitUserSelect = 'none'
    }
  }
  document.addEventListener('mousemove', handleResizeInteractionMove)
  document.addEventListener('mouseup', stopResizeInteraction)
}

const maybeStartRowResize = (event) => {
  if (!isInRowResizeZone(event)) return
  startRowResize(event)
}

const handleRowResizeMouseMove = (event) => {
  if (resizeState.mode === 'row') return
  event.currentTarget.style.cursor = isInRowResizeZone(event) ? 'row-resize' : ''
}

const renderHeaderCell = (col) => {
  return h(
    'div',
    {
      class: 'gi-col-title-wrap',
    },
    [
      h('span', { class: 'gi-col-title' }, col),
    ],
  )
}

const renderBodyCell = (col, rawValue, row) => {
  const Tooltip = resolveComponent('a-tooltip')
  const Popover = resolveComponent('a-popover')
  const Button = resolveComponent('a-button')
  const raw = normalizeCellValue(rawValue)
  const rowKey = String(row?.__goinsightRowKey || '')
  const tooltipKey = `${rowKey}::${col}`
  const info = classifyCellContent(raw)
  const isLong = info.category === 'long'

  const wrapProps = {
    class: ['cell-content-wrap', 'is-result-cell', { 'is-context-selected': isSelectedResultCell(rowKey, col), 'is-long-cell': isLong }],
    onMouseenter: (event) => openResultCellTooltip(tooltipKey, rawValue, event),
    onMouseleave: closeResultCellTooltip,
  }

  if (!isLong) {
    const tooltipTitle = () => {
      if (uiState.resultCellTooltipOpen && uiState.resultCellTooltipKey === tooltipKey) {
        return uiState.resultCellTooltipText
      }
      return raw.length > 0 ? info.display : ''
    }
    const text = h(
      'span',
      {
        class: 'cell-ellipsis',
      },
      raw,
    )
  const tooltipDelay = isLong ? 0.28 : 0.25
    const wrapped = h(
      Tooltip,
      {
        placement: 'topLeft',
        mouseEnterDelay: 0,
        mouseLeaveDelay: tooltipDelay,
        destroyTooltipOnHide: true,
        overlayClassName: 'result-cell-tooltip',
        getPopupContainer: () => (typeof document === 'undefined' ? undefined : document.body),
        open: uiState.resultCellTooltipOpen && uiState.resultCellTooltipKey === tooltipKey,
        title: tooltipTitle(),
      },
      {
        default: () => text,
      },
    )
    return h('div', wrapProps, [wrapped])
  }

  const popoverOpen = uiState.resultCellPopoverOpen && uiState.resultCellPopoverKey === tooltipKey
  const meta = uiState.resultCellPopoverMeta
  const popoverTitle = h(
    'div',
    { class: 'result-cell-popover-title' },
    [
      h('span', { class: 'result-cell-popover-title-col' }, String(col || '单元格内容')),
      h(
        'span',
        { class: 'result-cell-popover-title-meta' },
        [
          h('span', { class: 'result-cell-popover-tag' }, [
            meta?.isJson ? 'JSON' : '长文本',
          ]),
          h('span', {}, [
            `${meta?.charCount ?? info.charCount} 字符 · ${meta?.lineCount ?? info.lineCount} 行`,
          ]),
        ],
      ),
    ],
  )
  const popoverContent = h(
    'div',
    { class: 'result-cell-popover-body' },
    [
      h(
        'pre',
        { class: 'result-cell-popover-pre' },
        uiState.resultCellPopoverKey === tooltipKey ? uiState.resultCellPopoverDisplay : info.display,
      ),
      h(
        'div',
        { class: 'result-cell-popover-footer' },
        [
          h(
            Button,
            {
              size: 'small',
              onClick: () => copyResultCellPopoverValue(),
            },
            {
              icon: () => h(CopyOutlined),
              default: () => '复制全文',
            },
          ),
          h(
            Button,
            {
              size: 'small',
              onClick: () => openResultCellPopoverFullModal(),
            },
            {
              icon: () => h(FullscreenOutlined),
              default: () => '大弹窗查看',
            },
          ),
          h(
            Button,
            {
              size: 'small',
              type: 'primary',
              onClick: () => closeResultCellPopover(),
            },
            {
              default: () => '关闭',
            },
          ),
        ],
      ),
    ],
  )
  const expandBtn = h(
    'button',
    {
      type: 'button',
      class: 'cell-expand-btn',
      title: '展开查看完整内容',
      onClick: (event) => toggleResultCellPopover(col, rawValue, rowKey, event),
    },
    [h(ExpandOutlined)],
  )
  const longText = h(
    'span',
    {
      class: 'cell-ellipsis is-long',
    },
    raw,
  )
  const wrappedLongText = h(
    Tooltip,
    {
      placement: 'topLeft',
      mouseEnterDelay: 0,
      mouseLeaveDelay: 0.28,
      destroyTooltipOnHide: true,
      overlayClassName: 'result-cell-tooltip result-cell-tooltip--long',
      getPopupContainer: () => (typeof document === 'undefined' ? undefined : document.body),
      title: info.display,
    },
    {
      default: () => longText,
    },
  )
  const popoverWrap = h(
    Popover,
    {
      trigger: 'click',
      placement: 'bottomRight',
      destroyTooltipOnHide: true,
      overlayClassName: 'result-cell-popover',
      getPopupContainer: () => (typeof document === 'undefined' ? undefined : document.body),
      open: popoverOpen,
      'onUpdate:open': (next) => {
        if (!next) closeResultCellPopover()
      },
    },
    {
      title: () => popoverTitle,
      content: () => popoverContent,
      default: () => expandBtn,
    },
  )
  return h('div', wrapProps, [wrappedLongText, popoverWrap])
}

const antdColumns = computed(() => {
  const cols = uiData.tableColumns || []
  const baseWidths = baseColumnWidths.value || {}
  const manualWidths = manualColumnWidths.value || {}
  return cols.map((col) => ({
    title: () => renderHeaderCell(col),
    dataIndex: col,
    key: col,
    width: manualWidths[col] || baseWidths[col] || DEFAULT_COLUMN_WIDTH,
    customHeaderCell: () => ({
      class: [
        'gi-result-header-cell',
        {
          'is-col-resizing': resizeState.mode === 'column' && resizeState.column === col,
        },
      ],
      // 表头右缘 10px 热区拖动调宽（宽度只存内存，刷新/重新执行后恢复自动列宽）
      onMousedown: (event) => maybeStartColumnResize(col, event),
      onMousemove: handleHeaderResizeMouseMove,
      onMouseleave: (event) => clearResizeCursor(event.currentTarget),
    }),
    customCell: (record) => ({
      class: 'gi-result-body-cell',
      onMousedown: maybeStartRowResize,
      onMousemove: handleRowResizeMouseMove,
      onMouseleave: (event) => clearResizeCursor(event.currentTarget),
      onContextmenu: (event) => openResultContextMenu(event, col, record, record?.[col]),
    }),
    customRender: ({ text, record }) => renderBodyCell(col, text, record),
  }))
})

const recomputeTableBodyHeight = () => {
  const regionEl = resultTableRegionRef.value
  if (!regionEl) return

  const regionHeight = regionEl.clientHeight

  if (!regionHeight) return

  const theadEl = regionEl.querySelector('.ant-table-thead')

  const theadHeight = theadEl ? theadEl.getBoundingClientRect().height : 0

  // 表格底部预留：即表格与下方分页栏之间的垂直间距。
  // 原为 8 + 12 = 20px，按需求减半为 10px（横向滚动条高度已含在 .ant-table-body 内部，不影响可见行）
  const reserved = 10
  const bodyHeight = Math.max(48, Math.floor(regionHeight - theadHeight - reserved))
  tableBodyScrollY.value = bodyHeight
  nextTick(() => syncColgroupWidthsAcrossTables())
}

const normalizePaginationState = () => {
  const total = filteredTableData.value.length || 0
  const pageSize = uiData.pagination.pageSize || RESULT_PAGE_SIZE
  const totalPages = Math.max(1, Math.ceil(total / pageSize))
  if (uiData.pagination.current > totalPages) {
    uiData.pagination.current = totalPages
  }
  if (uiData.pagination.current < 1) {
    uiData.pagination.current = 1
  }
}

const handlePaginationChange = (page, pageSize) => {
  uiData.pagination.current = page
  uiData.pagination.pageSize = pageSize
}

const handlePaginationShowSizeChange = (current, size) => {
  uiData.pagination.current = current
  uiData.pagination.pageSize = size
  normalizePaginationState()
}

const normalizeExecutionView = (value) => {
  if (!value) return null
  if (value?.mode === 'single' || value?.mode === 'batch') {
    return value
  }
    return {
      mode: 'single',
      summary: null,
      resultSets: [
        {
          key: 'result',
          label: '结果集 1',
          result: value,
        },
      ],
      activeResultKey: 'result',
      executionMessage: '',
    }
}

const clearRenderedResult = () => {
  uiData.tableColumns = []
  uiData.tableData = []
  uiData.resultDuration = ''
  uiData.resultKeyword = ''
  uiData.pagination.current = 1
  manualColumnWidths.value = {}
  uiData.resultViewMode = 'row'
  uiData.selectedRowKey = ''
}

// 关闭某个结果集 Tab（仅隐藏，不删数据）
const closeResultTab = (key) => {
  if (uiData.resultTab === key) {
    const next = visibleResultTabItems.value.find((item) => item.key !== key)
    uiData.resultTab = next ? next.key : 'info'
  }
  if (!uiData.closedResultKeys.includes(key)) {
    uiData.closedResultKeys.push(key)
  }
}

// 当底层结果集变化时，清理已经不存在的结果集 key 的关闭记录
const pruneClosedResultKeys = () => {
  const validKeys = new Set(resultTabItems.value.map((item) => item.key))
  uiData.closedResultKeys = uiData.closedResultKeys.filter((key) => validKeys.has(key))
}

const bindResultTableRegionRef = (paneKey, el) => {
  if (paneKey === uiData.resultTab) {
    resultTableRegionRef.value = el || null
  }
}

const applyRenderedResultSet = (resultSet) => {
  if (!resultSet?.result) {
    clearRenderedResult()
    return
  }
  const value = resultSet.result
  uiData.tableColumns = value.columns || []
  uiData.tableData = (value.data || []).map((row, index) => ({
    ...(row || {}),
    __goinsightRowKey: index + 1,
  }))
  uiData.resultDuration = value.duration ?? ''
  uiData.resultKeyword = ''
  uiData.pagination.current = 1
  manualColumnWidths.value = {}
  uiData.resultViewMode = 'row'
  uiData.selectedRowKey = uiData.tableData.length ? String(uiData.tableData[0].__goinsightRowKey) : ''
}

const syncRenderedResultByTab = () => {
  if (uiData.resultTab === 'info') {
    clearRenderedResult()
    return
  }
  applyRenderedResultSet(currentResultSet.value)
}

let tableRegionResizeObserver
const handleWindowResize = () => {
  nextTick(() => {
    uiData.editorHeight = getEditorHeightFromRatio(editorHeightRatio.value)
    const regionEl = resultTableRegionRef.value
    resultRegionWidth.value = regionEl?.clientWidth || resultRegionWidth.value
    recomputeColumnLayout()
    recomputeTableBodyHeight()
  })
}

const getWorkspaceMetrics = () => {
  const workspaceEl = consoleWorkspaceRef.value
  if (!workspaceEl) {
    return {
      availableHeight: 520,
      minEditorHeight: MIN_EDITOR_HEIGHT,
      minResultHeight: MIN_RESULT_HEIGHT,
    }
  }
  const workspaceHeight = workspaceEl.getBoundingClientRect().height
  // 容器尚未完成布局（隐藏/挂载早期）时测量值为 0，直接参与夹取会把编辑区压成 0，走默认兜底
  if (!workspaceHeight) {
    return {
      availableHeight: 520,
      minEditorHeight: MIN_EDITOR_HEIGHT,
      minResultHeight: MIN_RESULT_HEIGHT,
    }
  }
  const topbarHeight = workspaceEl.querySelector('.console-topbar')?.getBoundingClientRect().height || 0
  const resizerHeight = workspaceEl.querySelector('.workspace-resizer')?.getBoundingClientRect().height || 0
  // 编辑卡片「除编辑器内容外的 chrome」（topbar + 提示行 + wrap 内边距 + 边框）不再逐项估算：
  // 旧实现 editorChromeHeight = 2 只算了边框，漏掉 hint-row 与 20px 上下 padding 约 44px，
  // availableHeight 偏大 → 「拖到最大」时结果区被压到 MIN_RESULT_HEIGHT 之下、上下限失真。
  // 改为直接实测：卡片总高 − 编辑器内容高，两者同步变化，差值在拖拽全程恒定。
  const editorCardEl = workspaceEl.querySelector('.console-editor-card')
  const editorContentH = editorCardEl?.querySelector('.cm-wrapper')?.getBoundingClientRect().height || 0
  let editorChromeHeight = topbarHeight + 2
  if (editorCardEl && editorContentH > 0) {
    editorChromeHeight = Math.max(0, editorCardEl.getBoundingClientRect().height - editorContentH)
  }
  const availableHeight = Math.max(0, Math.floor(workspaceHeight - resizerHeight - editorChromeHeight))
  if (availableHeight >= MIN_EDITOR_HEIGHT + MIN_RESULT_HEIGHT) {
    return {
      availableHeight,
      minEditorHeight: MIN_EDITOR_HEIGHT,
      minResultHeight: MIN_RESULT_HEIGHT,
    }
  }

  const adaptiveMinHeight = Math.max(
    MIN_PANEL_FALLBACK,
    Math.floor(availableHeight * 0.3),
  )
  const minEditorHeight = Math.min(
    availableHeight,
    Math.max(adaptiveMinHeight, Math.floor(availableHeight * DEFAULT_EDITOR_RATIO)),
  )
  const minResultHeight = Math.max(
    MIN_PANEL_FALLBACK,
    Math.min(availableHeight - minEditorHeight, Math.ceil(availableHeight * (1 - DEFAULT_EDITOR_RATIO))),
  )

  return {
    availableHeight,
    minEditorHeight,
    minResultHeight: Math.max(MIN_PANEL_FALLBACK, Math.min(availableHeight - minEditorHeight, minResultHeight)),
  }
}

const getMaxEditorHeight = () => {
  const { availableHeight, minEditorHeight, minResultHeight } = getWorkspaceMetrics()
  const maxHeight = Math.floor(availableHeight - minResultHeight)
  return Math.max(minEditorHeight, maxHeight)
}

const clampEditorRatio = (value) => {
  const normalized = Number(value)
  if (!Number.isFinite(normalized)) return DEFAULT_EDITOR_RATIO
  return Math.min(0.97, Math.max(0.08, normalized))
}

const getEditorHeightFromRatio = (ratio) => {
  const { availableHeight } = getWorkspaceMetrics()
  return clampEditorHeight(Math.floor(availableHeight * clampEditorRatio(ratio)))
}

const getCurrentEditorHeightRatio = (height) => {
  const { availableHeight } = getWorkspaceMetrics()
  if (!availableHeight) return DEFAULT_EDITOR_RATIO
  return clampEditorRatio(height / availableHeight)
}

const clampEditorHeight = (value) => {
  const normalized = Number.isFinite(value) ? value : MIN_EDITOR_HEIGHT
  const { minEditorHeight } = getWorkspaceMetrics()
  return Math.min(getMaxEditorHeight(), Math.max(minEditorHeight, Math.floor(normalized)))
}

const loadPersistedEditorHeight = () => {
  if (typeof sessionStorage === 'undefined') return null
  const raw = sessionStorage.getItem(EDITOR_HEIGHT_STORAGE_KEY)
  if (raw === null) return null
  const parsed = Number(raw)
  return Number.isFinite(parsed) ? parsed : null
}

const persistEditorHeight = (ratio) => {
  if (typeof sessionStorage === 'undefined') return
  sessionStorage.setItem(EDITOR_HEIGHT_STORAGE_KEY, String(clampEditorRatio(ratio)))
}

const adjustEditorHeight = (delta) => {
  uiData.editorHeight = clampEditorHeight(uiData.editorHeight + delta)
  nextTick(recomputeTableBodyHeight)
}

let resizeStartY = 0
let resizeStartHeight = 0
let isResizingWorkspace = false
// Pointer Capture 持有的元素与 pointerId：拖拽期间把 pointermove/pointerup 强制
// 派发到 resizer 元素本身，鼠标移出浏览器窗口松手（Chrome 在窗口外不派发 mouseup，
// 旧实现 isResizingWorkspace 从此卡 true，之后任何鼠标移动都会接管编辑区高度，
// 表现为「拖拽失灵、高度乱跳、无法拖到预期位置」）也能可靠收到 pointerup/pointercancel。
let resizePointerEl = null
let resizePointerId = null

const handleWorkspaceResizeMove = (event) => {
  if (!isResizingWorkspace) return
  const deltaY = event.clientY - resizeStartY
  uiData.editorHeight = clampEditorHeight(resizeStartHeight + deltaY)
  nextTick(recomputeTableBodyHeight)
  // #region debug-point N:workspace-resize-move
  sendDebugEvent('N', 'ConsoleIndex.vue:handleWorkspaceResizeMove', 'workspace resize move', {
    clientY: event.clientY,
    deltaY,
    nextEditorHeight: uiData.editorHeight,
  })
  // #endregion
}

const stopWorkspaceResize = () => {
  if (!isResizingWorkspace) return
  isResizingWorkspace = false
  document.documentElement.style.userSelect = ''
  if (document.documentElement.style.webkitUserSelect) {
    document.documentElement.style.webkitUserSelect = ''
  }
  if (document.body?.style) {
    document.body.style.userSelect = ''
    if (document.body.style.webkitUserSelect) {
      document.body.style.webkitUserSelect = ''
    }
  }
  document.removeEventListener('pointermove', handleWorkspaceResizeMove)
  document.removeEventListener('pointerup', stopWorkspaceResize)
  document.removeEventListener('pointercancel', stopWorkspaceResize)
  if (resizePointerEl && resizePointerId != null) {
    try { resizePointerEl.releasePointerCapture(resizePointerId) } catch (_) {}
  }
  resizePointerEl = null
  resizePointerId = null
  // #region debug-point O:workspace-resize-stop
  sendDebugEvent('O', 'ConsoleIndex.vue:stopWorkspaceResize', 'workspace resize stop', {
    finalEditorHeight: uiData.editorHeight,
  })
  // #endregion
}

const startWorkspaceResize = (event) => {
  if (typeof document === 'undefined') return
  // 只响应主键，右键/中键误触不进入拖拽
  if (event.button !== undefined && event.button !== 0) return
  isResizingWorkspace = true
  resizeStartY = event.clientY
  resizeStartHeight = uiData.editorHeight
  document.documentElement.style.userSelect = 'none'
  if (document.documentElement.style.webkitUserSelect !== undefined) {
    document.documentElement.style.webkitUserSelect = 'none'
  }
  if (document.body?.style) {
    document.body.style.userSelect = 'none'
    if (document.body.style.webkitUserSelect !== undefined) {
      document.body.style.webkitUserSelect = 'none'
    }
  }
  // Pointer Capture：即使指针移出窗口/移到 iframe 上，pointerup/pointercancel
  // 仍会派发到 resizer，拖拽状态必然被释放；pointercancel（触控手势打断等）同样走 stop。
  resizePointerEl = event.currentTarget || null
  resizePointerId = event.pointerId ?? null
  if (resizePointerEl?.setPointerCapture && resizePointerId != null) {
    try { resizePointerEl.setPointerCapture(resizePointerId) } catch (_) {}
  }
  document.addEventListener('pointermove', handleWorkspaceResizeMove)
  document.addEventListener('pointerup', stopWorkspaceResize)
  document.addEventListener('pointercancel', stopWorkspaceResize)
  // #region debug-point P:workspace-resize-start
  sendDebugEvent('P', 'ConsoleIndex.vue:startWorkspaceResize', 'workspace resize start', {
    clientY: event.clientY,
    startEditorHeight: resizeStartHeight,
  })
  // #endregion
}

// 查询结果全屏：与 SQL 编辑区全屏（workspaceFullscreen）保持同一套交互与表现——
// 纯 CSS class 切换（.console-page.is-result-fullscreen），不走浏览器原生 Fullscreen API，
// 两者均支持按钮切换 + Esc 退出。
const exitResultFullscreen = () => {
  if (!uiData.resultFullscreen) return
  uiData.resultFullscreen = false
  nextTick(recomputeTableBodyHeight)
}

const toggleResultFullscreen = () => {
  if (uiData.resultFullscreen) {
    exitResultFullscreen()
    return
  }
  uiData.resultFullscreen = true
  nextTick(recomputeTableBodyHeight)
}

const handleWorkspaceEscapeKey = (event) => {
  if (event.key !== 'Escape') return
  if (uiData.resultFullscreen) {
    event.preventDefault()
    exitResultFullscreen()
    return
  }
  if (uiState.workspaceFullscreen) {
    event.preventDefault()
    exitWorkspaceFullscreen()
  }
}

const relayoutAllConsoles = () => {
  const panes = consoleRightRef.value?.visiblePanes || []
  panes.forEach((pane) => {
    const editor = consoleRightRef.value?.getPaneEditor?.(pane)
    editor?.layout?.()
  })
  nextTick(recomputeTableBodyHeight)
}

const enterWorkspaceFullscreen = () => {
  // 结果全屏是 fixed 覆盖层，进入编辑区全屏前先退出，避免遮挡
  uiData.resultFullscreen = false
  uiState.workspaceFullscreen = true
  if (typeof setWorkspaceFullscreen === 'function') setWorkspaceFullscreen(true)
  nextTick(relayoutAllConsoles)
}

const exitWorkspaceFullscreen = () => {
  uiState.workspaceFullscreen = false
  if (typeof setWorkspaceFullscreen === 'function') setWorkspaceFullscreen(false)
  nextTick(relayoutAllConsoles)
}

const toggleWorkspaceFullscreen = () => {
  if (uiState.workspaceFullscreen) {
    exitWorkspaceFullscreen()
  } else {
    enterWorkspaceFullscreen()
  }
}

onMounted(() => {
  const persistedHeight = loadPersistedEditorHeight()
  if (persistedHeight !== null) {
    if (persistedHeight > 1) {
      uiData.editorHeight = clampEditorHeight(persistedHeight)
      editorHeightRatio.value = getCurrentEditorHeightRatio(uiData.editorHeight)
    } else {
      editorHeightRatio.value = clampEditorRatio(persistedHeight)
      uiData.editorHeight = getEditorHeightFromRatio(editorHeightRatio.value)
    }
  } else {
    uiData.editorHeight = getEditorHeightFromRatio(editorHeightRatio.value)
  }

  if (typeof window !== 'undefined') {
    window.addEventListener('resize', handleWindowResize)
    window.addEventListener('blur', handleSelectionLockRecovery)
    window.addEventListener('scroll', handleResultContextMenuScroll, true)
  }
  if (typeof document !== 'undefined') {
    document.addEventListener('visibilitychange', handleVisibilityLockRecovery)
    document.addEventListener('mousedown', handleEditorMouseDownRecovery, true)
    document.addEventListener('mousedown', handleResultContextMenuPointerDown)
    document.addEventListener('keydown', handleResultContextMenuKeydown)
    document.addEventListener('keydown', handleWorkspaceEscapeKey)
  }

  if (typeof ResizeObserver !== 'undefined') {
    tableRegionResizeObserver = new ResizeObserver((entries) => {
      const width = entries?.[0]?.contentRect?.width
      if (width) {
        resultRegionWidth.value = Math.floor(width)
      }
      nextTick(() => {
        recomputeColumnLayout()
        recomputeTableBodyHeight()
      })
    })
  }

  nextTick(() => {
    uiData.editorHeight = getEditorHeightFromRatio(editorHeightRatio.value)
    persistEditorHeight(editorHeightRatio.value)
    recomputeTableBodyHeight()
  })
})

onBeforeUnmount(() => {
  handleSelectionLockRecovery()
  tableRegionResizeObserver?.disconnect?.()
  if (copyAsSubmenuCloseTimer.value) {
    clearTimeout(copyAsSubmenuCloseTimer.value)
    copyAsSubmenuCloseTimer.value = null
  }
  if (uiState.workspaceFullscreen) {
    try {
      if (typeof setWorkspaceFullscreen === 'function') setWorkspaceFullscreen(false)
    } catch {}
  }
  if (typeof window !== 'undefined') {
    window.removeEventListener('resize', handleWindowResize)
    window.removeEventListener('blur', handleSelectionLockRecovery)
    window.removeEventListener('scroll', handleResultContextMenuScroll, true)
  }
  if (typeof document !== 'undefined') {
    stopWorkspaceResize()
    stopResizeInteraction()
  }
  if (typeof document !== 'undefined') {
    document.removeEventListener('visibilitychange', handleVisibilityLockRecovery)
    document.removeEventListener('mousedown', handleEditorMouseDownRecovery, true)
    document.removeEventListener('mousedown', handleResultContextMenuPointerDown)
    document.removeEventListener('keydown', handleResultContextMenuKeydown)
    document.removeEventListener('keydown', handleWorkspaceEscapeKey)
  }
})

watch(
  () => uiData.editorHeight,
  (height) => {
    editorHeightRatio.value = getCurrentEditorHeightRatio(height)
    persistEditorHeight(editorHeightRatio.value)
  },
)

watch(
  () => resultTableRegionRef.value,
  async (el) => {
    tableRegionResizeObserver?.disconnect?.()
    if (el) {
      tableRegionResizeObserver?.observe?.(el)
      await nextTick()
      resultRegionWidth.value = el.clientWidth || 0
      recomputeColumnLayout()
      recomputeTableBodyHeight()
    }
  },
  { immediate: true },
)

watch(
  () => [uiData.tableColumns.length, uiData.tableData.length],
  async () => {
    closeResultContextMenu()
    if (!uiData.tableColumns.length || !uiData.tableData.length) {
      clearResultContextCell()
    }
    await nextTick()
    normalizePaginationState()
    recomputeColumnLayout()
    recomputeTableBodyHeight()
  },
  { immediate: true },
)

watch(
  [() => consoleRightRef.value, () => externalTabScrollerRef.value, () => externalRenameInputRef.value],
  async () => {
    await nextTick()
    bindExternalTabRefs()
  },
  { immediate: true },
)

watch(
  () => uiData.resultKeyword,
  () => {
    uiData.pagination.current = 1
    ensureSelectedRowValid()
    nextTick(() => {
      normalizePaginationState()
      recomputeTableBodyHeight()
    })
  },
)

watch(
  () => uiData.resultTab,
  () => {
    closeResultContextMenu()
    clearResultContextCell()
    syncRenderedResultByTab()
    nextTick(() => {
      normalizePaginationState()
      recomputeColumnLayout()
      recomputeTableBodyHeight()
    })
  },
)

watch(
  () => consoleWorkspaceRef.value,
  async (el) => {
    if (!el) return
    await nextTick()
    uiData.editorHeight = getEditorHeightFromRatio(editorHeightRatio.value)
    recomputeTableBodyHeight()
  },
  { immediate: true },
)

const renderResultTable = (value) => {
  closeResultContextMenu()
  clearResultContextCell()
  if (value) {
    clearResultNotice()
  }
  uiData.executionView = normalizeExecutionView(value)
  // 新的执行结果回来后，重置已关闭的结果集 Tab（重新执行意味着结果集应重新展示）
  uiData.closedResultKeys = []
  // 有真实结果集时切到第一个结果集（执行完直接看到表格）；
  // 无结果数据或执行失败时停在「执行信息」（失败场景由 renderExecutionMessage 兜底切回）
  const sets = Array.isArray(uiData.executionView?.resultSets) ? uiData.executionView.resultSets : []
  const firstSet = sets.find((item) => item.result)
  uiData.resultTab = firstSet ? firstSet.key : 'info'
  syncRenderedResultByTab()
  nextTick(() => {
    normalizePaginationState()
    recomputeColumnLayout()
    recomputeTableBodyHeight()
  })
}

const renderExecutionMessage = (value) => {
  uiData.executionMessage = value || ''
  if (uiData.executionMessage.includes('结果: 执行失败') || uiData.executionMessage.includes('结果: 解释失败')) {
    uiData.resultTab = 'info'
    return
  }
  if (uiData.executionView?.mode === 'batch' && !visibleResultTabItems.value.length) {
    uiData.resultTab = 'info'
  }
}
</script>

<style scoped>
.console-page {
  --console-primary: var(--ant-colorPrimary, #1677ff);
  --console-page-bg: #ffffff;
  --console-card-bg: #ffffff;
  --console-border-color: rgba(5, 17, 25, 0.12);
  --console-resizer-grip-size: 16px;
  --console-resizer-grip-gap: 3px;
  --console-radius: 10px;
  --console-border: 1px solid var(--console-border-color);
  --console-muted: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  display: flex;
  flex-direction: column;
  gap: 0;
  height: 100%;
  min-height: 0;
  overflow: hidden;
  background: #ffffff;
  background-image: none !important;
}

.console-shell {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  border: var(--console-border);
  border-radius: 10px;
  background: #ffffff;
  overflow: hidden;
}

.workspace-tabbar {
  position: relative;
  flex: 0 0 auto;
  display: flex;
  align-items: flex-end;
  gap: 12px;
  min-width: 0;
  padding: 2px 8px 0 6px;
  background: linear-gradient(180deg, #f7f9fc 0%, #eef2f7 100%);
}

.workspace-tabbar::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 1px;
  background: var(--console-border-color);
  z-index: 0;
}

.workspace-tabbar-main {
  position: relative;
  z-index: 1;
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: flex-end;
  gap: 6px;
}

.workspace-tab-plus-button {
  flex: 0 0 auto;
  min-width: 28px;
  height: 28px;
  padding-inline: 0;
  background: #ffffff;
  border: 1px solid rgba(0, 0, 0, 0.10);
  color: rgba(15, 23, 42, 0.78);
  box-shadow: none;
  border-radius: 8px;
  transition: all 0.15s ease;
  align-self: flex-end;
}

.workspace-tab-plus-button--inline {
  margin-left: 2px;
  margin-bottom: 3px;
}

.workspace-tab-plus-button:not(:disabled):hover {
  background: rgba(22, 119, 255, 0.06);
  border-color: rgba(22, 119, 255, 0.30);
  color: rgba(15, 23, 42, 0.92);
}

.workspace-tab-plus-button :deep(.anticon) {
  font-size: 14px;
}

.workspace-tabbar-actions {
  position: relative;
  z-index: 1;
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 8px;
  padding-bottom: 8px;
}

.workspace-tabbar-create {
  display: inline-flex;
  align-items: center;
  gap: 0;
}

.workspace-tab-scroll {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: flex-end;
  gap: 3px;
  box-sizing: border-box;
  padding: 0 18px;
  overflow-x: auto;
  overflow-y: hidden;
  scroll-padding-inline: 18px;
  scrollbar-width: none;
}

.workspace-tab-scroll::-webkit-scrollbar {
  display: none;
}

.workspace-tab-shell {
  flex: 0 0 auto;
  position: relative;
  z-index: 1;
}

.workspace-tab-shell.is-before-active,
.workspace-tab-shell.is-after-active {
  z-index: 2;
}

.workspace-tab-empty {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  height: 34px;
  padding: 0 14px;
  border: 1px dashed rgba(5, 17, 25, 0.14);
  border-radius: 10px 10px 0 0;
  background: rgba(255, 255, 255, 0.72);
  color: var(--console-muted);
  font-size: 12px;
  white-space: nowrap;
}

.workspace-tab-item {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  max-width: 252px;
  height: 34px;
  padding: 0 12px 0 10px;
  border: 1px solid rgba(15, 23, 42, 0.10);
  border-radius: 10px 10px 0 0;
  background: rgba(255, 255, 255, 0.38);
  background-clip: padding-box;
  color: rgba(15, 23, 42, 0.70);
  cursor: pointer;
  transition: color 0.18s ease, background-color 0.18s ease, opacity 0.18s ease,
    border-color 0.18s ease;
}

.workspace-tab-item:hover {
  background: rgba(255, 255, 255, 0.62);
  border-color: rgba(15, 23, 42, 0.16);
  color: rgba(15, 23, 42, 0.88);
}

.workspace-tab-item.is-active {
  z-index: 4;
  margin-top: 1px;
  margin-bottom: 0;
  border-color: var(--console-border-color);
  border-bottom-color: #ffffff;
  border-radius: 10px 10px 0 0;
  background: #ffffff;
  color: var(--console-tab-env-text, rgba(15, 23, 42, 0.92));
  box-shadow: none;
}

.workspace-tab-item.tab-kind-preview {
  color: rgba(51, 65, 85, 0.8);
}

.workspace-tab-item.is-dragging {
  opacity: 0.62;
}

.workspace-tab-item.is-editing {
  width: 252px;
  max-width: 252px;
  cursor: text;
}

.workspace-tab-item:not(.is-active) {
  height: 28px;
  margin-bottom: 3px;
  border-radius: 8px;
  /* 非当前 tab 文字用浅灰色，与环境色解耦（环境色仅由激活 tab 文字与按钮图标承担） */
  color: rgba(148, 163, 184, 0.95);
}

.workspace-tab-item:not(.is-active):hover {
  color: rgba(100, 116, 139, 0.95);
}

.workspace-tab-item:not(.is-active)::before {
  display: none;
}

.workspace-tab-content {
  min-width: 0;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.workspace-tab-status {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  /* 与全站统一的 20px 数据库类型 logo 对齐 */
  width: 20px;
  color: currentColor;
  flex: 0 0 auto;
}

.workspace-tab-dirty-dot {
  width: 8px;
  height: 8px;
  border-radius: 999px;
  background: currentColor;
}

.workspace-tab-texts {
  display: inline-flex;
  align-items: center;
  min-width: 0;
  overflow: hidden;
}

.workspace-tab-title,
.workspace-tab-affix {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.workspace-tab-title {
  max-width: 220px;
  font-size: 12px;
  font-weight: 600;
  transition: color 0.15s ease;
}

.workspace-tab-title.is-unbound {
  color: var(--ant-colorTextTertiary, rgba(0, 0, 0, 0.28));
  font-weight: 400;
  font-style: normal;
}

.workspace-tab-affix {
  max-width: 70px;
  font-size: 11px;
  opacity: 0.78;
}

.workspace-tab-pin {
  flex: 0 0 auto;
  font-size: 11px;
  /* 图钉不参与环境色：与非当前 tab 实例名同一中性色 */
  color: rgba(15, 23, 42, 0.88);
}

/* tab 上的收藏 / 刷新资源树工具按钮（从对象树实例节点迁移而来） */
.workspace-tab-tools {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  flex: 0 0 auto;
}

.workspace-tab-tool-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  border-radius: 4px;
  background: transparent;
  /* 收藏/刷新等工具按钮跟随环境色（与激活 tab 文字一致），未绑定环境时回退中性色 */
  color: var(--console-tab-env-text, rgba(15, 23, 42, 0.88));
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
  opacity: 1;
  transition: opacity 0.15s ease, background-color 0.15s ease, color 0.15s ease;
}

.workspace-tab-tool-btn:hover {
  background-color: rgba(0, 0, 0, 0.06);
}

.workspace-tab-tool-btn.is-fav {
  /* 已收藏用 StarFilled 实心图标表达状态，颜色仍跟随环境色 */
  color: var(--console-tab-env-text, rgba(15, 23, 42, 0.88));
}

.tab-tooltip-card {
  min-width: 120px;
  max-width: 240px;
  padding: 2px 6px;
  background: transparent;
  border-radius: 0;
  border: 0;
  box-shadow: none;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
  font-size: 12px;
  line-height: 1.4;
}

.tab-tooltip-card--simple {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.tab-tooltip-card__head {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-bottom: 0;
  min-height: 15px;
}

.tab-tooltip-card__icon {
  flex: 0 0 auto;
  font-size: 11px;
  color: currentColor;
  opacity: 0.75;
}

.tab-tooltip-card__icon.is-unbound {
  opacity: 0.35;
}

.tab-tooltip-card__instance-name {
  flex: 1 1 auto;
  min-width: 0;
  font-size: 12px;
  font-weight: 500;
  color: currentColor;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tab-tooltip-card__instance-name.is-unbound {
  opacity: 0.45;
  font-weight: 400;
}

.tab-tooltip-card__type-chip {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  padding: 0 5px;
  font-size: 11px;
  line-height: 15px;
  color: currentColor;
  background: rgba(0, 0, 0, 0.08);
  border-radius: 2px;
  font-weight: 400;
}

.tab-tooltip-card__row {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-top: 0;
}

.tab-tooltip-card__row--sub {
  font-size: 12px;
  color: currentColor;
  opacity: 0.75;
}

.tab-tooltip-card__row-icon {
  flex: 0 0 auto;
  font-size: 10px;
  opacity: 0.6;
  color: currentColor;
}

.tab-tooltip-card__row-text {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.tab-tooltip-card__dot {
  margin: 0 2px;
  opacity: 0.45;
  font-weight: 500;
}

.tab-tooltip-card__divider {
  margin: 3px 0;
  height: 1px;
  background: rgba(0, 0, 0, 0.12);
}

.tab-tooltip-card__foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}

.tab-tooltip-card__status-pill {
  display: inline-flex;
  align-items: center;
  padding: 0 5px;
  font-size: 11px;
  line-height: 15px;
  border-radius: 2px;
  font-weight: 400;
  color: currentColor;
  background: rgba(0, 0, 0, 0.08);
}

.tab-tooltip-card__status-pill.is-connected {
  color: #389e0d;
  background: rgba(82, 196, 26, 0.16);
}

.tab-tooltip-card__status-pill.is-running {
  color: #1677ff;
  background: rgba(22, 119, 255, 0.16);
}

.tab-tooltip-card__status-pill.is-disconnected,
.tab-tooltip-card__status-pill.is-error {
  color: #cf1322;
  background: rgba(207, 19, 34, 0.14);
}

.tab-tooltip-card__status-pill.is-dirty {
  color: #d46b08;
  background: rgba(250, 173, 20, 0.18);
}

.tab-tooltip-card__status-pill.is-unknown,
.tab-tooltip-card__status-pill {
  color: currentColor;
  opacity: 0.75;
  background: rgba(0, 0, 0, 0.06);
}

.tab-tooltip-card__index {
  flex: 0 0 auto;
  font-size: 11px;
  opacity: 0.5;
  color: currentColor;
  font-variant-numeric: tabular-nums;
}

.workspace-tab-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border: 0;
  border-radius: 999px;
  background: transparent;
  /* 关闭按钮跟随环境色（与激活 tab 文字一致），未绑定环境时回退中性色 */
  color: var(--console-tab-env-text, rgba(15, 23, 42, 0.88));
  cursor: pointer;
  opacity: 0;
  transition: all 0.15s ease;
}

.workspace-tab-item:hover .workspace-tab-close,
.workspace-tab-item.is-active .workspace-tab-close {
  opacity: 0.9;
}

.workspace-tab-close:hover {
  background: rgba(15, 23, 42, 0.08);
}

.workspace-tab-rename-input {
  width: 100%;
}

.workspace-tab-rename-input :deep(.ant-input) {
  height: 28px;
  border-radius: 8px;
}

.workspace-tab-scroll-button,
.workspace-tab-entry-button,
.workspace-tab-primary-action {
  height: 28px;
  border-radius: 8px;
  font-size: 12px;
}

.workspace-tab-scroll-button :deep(.anticon),
.workspace-tab-entry-button :deep(.anticon),
.workspace-tab-primary-action :deep(.anticon) {
  font-size: 12px;
}

.workspace-tab-primary-action--main {
  border-top-right-radius: 0;
  border-bottom-right-radius: 0;
  padding-inline: 10px 9px;
  font-weight: 500;
  box-shadow: none;
}

.workspace-tab-primary-action--toggle {
  min-width: 24px;
  padding-inline: 0;
  border-top-left-radius: 0;
  border-bottom-left-radius: 0;
  margin-left: -1px;
}

.workspace-tab-primary-action--toggle:not(:disabled) {
  border-color: var(--ant-colorPrimary, #0f8a83);
  color: #ffffff;
  background: var(--ant-colorPrimary, #0f8a83);
}

.workspace-tab-primary-action--toggle:not(:disabled):hover {
  border-color: var(--ant-colorPrimaryHover, #117e78);
  background: var(--ant-colorPrimaryHover, #117e78);
  color: #ffffff;
}

.workspace-tab-more-button {
  min-width: 28px;
  padding-inline: 0;
}

.workspace-tab-fullscreen-button {
  min-width: 28px;
  padding-inline: 0;
}

.workspace-tab-fullscreen-button.is-in-fullscreen {
  background: color-mix(in srgb, var(--ant-colorPrimary, #0f8a83) 8%, transparent);
  color: var(--ant-colorPrimary, #0f8a83);
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--ant-colorPrimary, #0f8a83) 22%, transparent);
}

.workspace-tab-fullscreen-button.is-in-fullscreen:hover,
.workspace-tab-fullscreen-button.is-in-fullscreen:focus {
  background: color-mix(in srgb, var(--ant-colorPrimary, #0f8a83) 14%, transparent);
  color: var(--ant-colorPrimaryHover, #0d7a74);
}

.console-page.is-workspace-fullscreen {
  padding: 0;
  height: 100vh;
  min-height: 100vh;
}

.console-page.is-workspace-fullscreen .console-shell {
  height: 100vh;
  min-height: 100vh;
  border-radius: 0;
  border-left: 0;
  border-right: 0;
  border-bottom: 0;
}

.console-page.is-workspace-fullscreen :deep(.split-wrapper .right-content) {
  padding: 0 !important;
}

.console-page.is-workspace-fullscreen .console-workspace {
  padding: 10px 14px 14px;
}

.console-page.is-workspace-fullscreen .result-card {
  box-shadow: 0 10px 30px rgba(29, 35, 41, 0.06), 0 2px 8px rgba(29, 35, 41, 0.04);
  border: 1px solid var(--ant-colorBorderSecondary, #eef0f2);
  border-radius: 10px;
}

.console-page.is-workspace-fullscreen .workspace-tabbar {
  background: linear-gradient(180deg, #f5f8fb 0%, #e9eef5 100%);
  padding-inline: 10px;
}

.console-page.is-workspace-fullscreen .result-card :deep(.ant-card-head) {
  padding-inline: 14px;
}

.console-page.is-workspace-fullscreen .result-card :deep(.ant-card-body) {
  padding-inline: 14px;
}

.console-shell-body {
  flex: 1 1 auto;
  min-height: 0;
  height: 0;
  background: #ffffff;
  display: flex;
  flex-direction: column;
}

.console-shell-body.is-no-active-pane :deep(.split-wrapper .scalable),
.console-shell-body.is-no-active-pane :deep(.split-wrapper .separator),
.console-shell-body.is-no-active-pane :deep(.split-wrapper .collapsed-handle) {
  display: none !important;
}

.console-shell-body :deep(.split-wrapper) {
  --split-panel-gap: 8px;
  --split-handle-bg: var(--console-page-bg);
  --split-handle-border: var(--console-border-color);
  --split-handle-accent: var(--console-primary);
  height: 100%;
  max-height: none;
  min-height: 0;
  border-radius: 0;
  border: 0;
  background: var(--console-page-bg);
  background-image: none !important;
  padding: 0;
  gap: 0;
}

.console-page :deep(.split-wrapper .left-content),
.console-page :deep(.split-wrapper .right-content) {
  background: transparent;
  background-image: none !important;
}

.console-page :deep(.split-wrapper .left-content) {
  padding: 0;
}

.console-page :deep(.split-wrapper .right-content) {
  padding: 0;
  overflow: hidden;
}

.console-page :deep(.split-wrapper .separator),
.console-page :deep(.split-wrapper .collapsed-handle) {
  background-color: transparent;
  background-image: none !important;
  border: 0;
  box-shadow: none;
}

.console-page :deep(.split-wrapper .separator) {
  top: 0;
  bottom: 0;
  border-radius: 0;
  width: 12px;
}

.console-page :deep(.split-wrapper .separator i),
.console-page :deep(.split-wrapper .collapsed-handle i) {
  display: none;
}

.console-page :deep(.split-wrapper .separator::after),
.console-page :deep(.split-wrapper .collapsed-handle::after) {
  content: '';
  position: absolute;
  left: 50%;
  top: 10px;
  bottom: 10px;
  width: 1px;
  transform: translateX(-50%);
  background: transparent;
}

.console-page :deep(.split-wrapper .separator:hover),
.console-page :deep(.split-wrapper .collapsed-handle:hover) {
  background-color: rgba(22, 119, 255, 0.04);
}

.console-page :deep(.split-wrapper .separator:hover::after),
.console-page :deep(.split-wrapper .collapsed-handle:hover::after) {
  background: transparent;
}

.console-workspace {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  /* 顶部留白减半（10px → 5px）：非全屏时压缩标签条与编辑卡片之间的空隙 */
  padding: 5px 12px 8px 12px;
  gap: 0;
  box-sizing: border-box;
}

.console-workspace > :deep(.console-editor-card) {
  flex: 0 0 auto;
  min-height: 0;
}

.console-workspace > .workspace-resizer {
  flex-shrink: 0;
}

.console-workspace > .result-card {
  flex: 1;
  min-height: 0;
}

.result-search {
  width: 168px;
}

.workspace-resizer {
  height: 10px;
  margin: 0;
  border-radius: 0;
  border: 0;
  background: transparent;
  cursor: row-resize;
  flex-shrink: 0;
  position: relative;
  overflow: hidden;
  z-index: 1;
  /* Pointer Capture 拖拽需要：阻止触屏/触控板手势（滚动、文字选择）打断拖拽序列 */
  touch-action: none;
  transition: background-color 0.2s ease;
}

.workspace-resizer::before {
  content: '';
  position: absolute;
  left: 50%;
  top: 50%;
  width: 42px;
  height: 3px;
  border-radius: 2px;
  transform: translate(-50%, -50%);
  background: var(--console-border-color, rgba(5, 17, 25, 0.16));
  transition: background-color 0.2s ease, width 0.2s ease;
}

.workspace-resizer:hover,
.workspace-resizer:focus-visible {
  background: rgba(22, 119, 255, 0.04);
  outline: none;
}

.workspace-resizer:hover::before,
.workspace-resizer:focus-visible::before {
  background: var(--console-primary, #1677ff);
  width: 64px;
}

.result-card {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  border-radius: 10px;
  border: 0;
  background: var(--console-card-bg);
  box-shadow: 0 0 0 1px var(--console-border-color, rgba(5, 17, 25, 0.10));
  overflow: hidden;
}

.result-card :deep(.ant-card-head) {
  min-height: 38px;
  border-bottom-color: var(--console-border-color);
  background: var(--ant-colorFillAlter, #fafafa);
}

.result-card :deep(.ant-card-head-wrapper) {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  min-height: 42px;
  min-width: 0;
  flex-wrap: nowrap;
}

.result-card :deep(.ant-card-head-title) {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  min-height: 38px;
  padding: 0;
  /* 标题区已改造成 Tab 导航，解除 antd 默认的单行省略，避免标签被截断 */
  overflow: visible;
  white-space: normal;
  text-overflow: clip;
}

.result-card :deep(.ant-card-extra) {
  display: flex;
  align-items: center;
  padding: 0;
  min-width: 0;
  max-width: 100%;
  overflow-x: hidden;
  overflow-y: hidden;
  scrollbar-width: none;
}

.result-card :deep(.ant-card-extra::-webkit-scrollbar) {
  display: none;
}

.result-card :deep(.ant-card-body) {
  flex: 1;
  display: flex;
  flex-direction: column;
  padding: 8px 10px 8px;
  min-height: 0;
  overflow: hidden;
}

.result-error-block {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border-radius: 10px;
  border: 1px solid rgba(255, 77, 79, 0.28);
  background: rgba(255, 77, 79, 0.06);
  margin-bottom: 10px;
}

.result-error-main {
  min-width: 0;
  flex: 1;
}

.result-error-title {
  font-weight: 600;
  color: rgba(0, 0, 0, 0.88);
  margin-bottom: 4px;
}

.result-error-detail {
  font-size: 12px;
  line-height: 1.5;
  color: rgba(0, 0, 0, 0.72);
  white-space: pre-wrap;
  word-break: break-word;
}

.result-error-actions {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.result-card-toolbar {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-left: 6px;
}

.result-toolbar-actions {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.result-toolbar-search {
  flex: 0 0 auto;
  width: 172px;
  min-width: 172px;
}

.result-toolbar-search :deep(.ant-input-search),
.result-toolbar-search :deep(.ant-input-group-wrapper) {
  display: block;
  width: 100%;
}

.result-toolbar-search :deep(.ant-input-group) {
  display: flex;
  align-items: stretch;
  width: 100%;
  height: 26px;
}

.result-toolbar-search :deep(.ant-input-affix-wrapper) {
  display: flex;
  align-items: center;
  flex: 1 1 auto;
  min-width: 0;
  height: 26px;
  padding: 0 8px;
  border-top-left-radius: 6px;
  border-bottom-left-radius: 6px;
}

.result-toolbar-search :deep(.ant-input-group-addon) {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 28px;
  width: 28px;
  min-width: 28px;
  height: 26px;
  padding: 0;
  overflow: hidden;
  border-top-right-radius: 6px;
  border-bottom-right-radius: 6px;
}

.result-toolbar-search :deep(.ant-input-search-button) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 26px;
  min-width: 28px;
  width: 28px;
  padding: 0;
  background: #ffffff;
  color: var(--ant-colorPrimary, #0f8a83);
  border-color: var(--ant-colorBorder, #d9d9d9);
  box-shadow: none;
}

.result-toolbar-search :deep(.ant-input-search-button:hover) {
  background: rgba(22, 119, 255, 0.06);
  color: var(--ant-colorPrimaryHover, #0d7a74);
  border-color: var(--ant-colorPrimaryHover, #0d7a74);
}

.result-toolbar-search :deep(.ant-input-search-button:active) {
  background: rgba(22, 119, 255, 0.1);
  color: var(--ant-colorPrimaryActive, #0b6a65);
  border-color: var(--ant-colorPrimaryActive, #0b6a65);
}

.result-toolbar-search :deep(.ant-input-search-button[disabled]) {
  background: rgba(0, 0, 0, 0.04);
  color: rgba(0, 0, 0, 0.25);
  border-color: var(--ant-colorBorder, #d9d9d9);
}

.result-toolbar-search :deep(.ant-input) {
  height: 24px;
  line-height: 24px;
}

.result-toolbar-actions :deep(.ant-btn) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 26px;
  padding: 0 10px;
  line-height: 24px;
}

.result-toolbar-actions :deep(.ant-btn > span) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
}

.result-toolbar-actions :deep(.ant-btn.fullscreen-trigger) {
  padding: 0 8px;
  line-height: 24px;
  color: var(--ant-colorPrimary, #0f8a83);
}

.export-format-popover {
  min-width: 0;
  width: auto;
  padding: 4px;
  background: #ffffff;
  border-radius: 8px;
  box-shadow: 0 8px 24px rgba(15, 23, 42, 0.12), 0 2px 6px rgba(15, 23, 42, 0.06);
}

.export-format-list {
  display: flex;
  flex-direction: column;
  gap: 0;
  padding: 0;
  margin: 0;
  list-style: none;
}

.export-format-item {
  width: 100%;
  min-width: 0;
  height: 22px;
  border: 0;
  border-radius: 6px;
  padding: 0 10px;
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
  background: transparent;
  color: inherit;
  cursor: pointer;
  text-align: left;
  white-space: nowrap;
  transition: background 0.14s ease, color 0.14s ease;
}

.export-format-item:hover,
.export-format-item:focus-visible {
  background: color-mix(in srgb, var(--ant-colorPrimary, #0f8a83) 10%, #ffffff);
  color: var(--ant-colorPrimary, #0f8a83);
  outline: none;
}

.export-format-name {
  font-size: 11px;
  font-weight: 500;
  line-height: 1;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.85));
  letter-spacing: 0.03em;
}

.export-confirm-modal :deep(.ant-modal-content) {
  padding: 14px 14px 12px;
  border-radius: 12px;
  background: #ffffff;
  box-shadow: 0 16px 40px rgba(15, 23, 42, 0.16);
}

.export-confirm-modal :deep(.ant-modal-close) {
  top: 6px;
  right: 6px;
}

.export-confirm-card {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 8px;
  padding: 2px 2px 0;
  text-align: left;
}

.export-confirm-title {
  font-size: 15px;
  font-weight: 600;
  line-height: 1.3;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
}

.export-confirm-subtitle {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.55));
}

.export-confirm-actions {
  width: 100%;
  display: inline-flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 4px;
}

.result-fullscreen-button {
  min-width: 28px;
  padding-inline: 0;
  margin-left: 0;
}

.result-toolbar-view-switch {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-left: 0;
}

.result-view-toggle {
  min-width: 28px;
  padding-inline: 0;
  margin-left: 0;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.45));
  border-radius: 6px;
  transition: all 0.15s ease;
}

.result-view-toggle :deep(.anticon) {
  font-size: 14px;
}

.result-view-toggle:hover:not([disabled]) {
  color: var(--ant-colorPrimary, #1677ff);
  background: rgba(22, 119, 255, 0.06);
}

.result-view-toggle.is-active {
  color: var(--ant-colorPrimary, #1677ff);
  background: rgba(22, 119, 255, 0.1);
}

.result-view-toggle.is-active:hover:not([disabled]) {
  background: rgba(22, 119, 255, 0.14);
}

.result-table-row.is-selected :deep(.ant-table-cell) {
  background-color: rgba(22, 119, 255, 0.06) !important;
}

.result-table-row:hover :deep(.ant-table-cell) {
  background-color: rgba(0, 0, 0, 0.025);
}

.result-table-row.is-selected:hover :deep(.ant-table-cell) {
  background-color: rgba(22, 119, 255, 0.09) !important;
}

.result-column-view {
  display: flex;
  flex-direction: column;
  min-height: 0;
  width: 100%;
}

.column-view-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex: 0 0 auto;
  padding: 6px 12px;
  border-bottom: 1px solid var(--console-border-color, rgba(5, 17, 25, 0.08));
  background: linear-gradient(180deg, #fafbfd 0%, #f4f7fb 100%);
}

.column-view-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
}

.column-view-row-info {
  font-size: 12px;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.45));
}

.column-view-empty {
  flex: 1 1 auto;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 40px 0;
}

.column-view-body {
  flex: 1 1 auto;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 4px 0;
}

.column-view-item {
  display: flex;
  width: 100%;
  min-height: 34px;
  border-bottom: 1px solid var(--ant-colorBorderSecondary, rgba(5, 17, 25, 0.06));
}

.column-view-item:last-child {
  border-bottom: none;
}

.column-view-name {
  flex: 0 0 180px;
  min-width: 180px;
  max-width: 180px;
  padding: 8px 8px 8px 12px;
  font-size: 12px;
  font-weight: 600;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  background: var(--ant-colorFillSecondary, rgba(0, 0, 0, 0.02));
  border-right: 1px solid var(--ant-colorBorderSecondary, rgba(5, 17, 25, 0.06));
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  line-height: 1.5;
  user-select: all;
  text-align: right;
}

.column-view-value {
  flex: 1 1 auto;
  min-width: 0;
  padding: 8px 12px;
  font-size: 12px;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
  line-height: 1.5;
  cursor: pointer;
  transition: background 0.15s ease;
  user-select: all;
}

.column-view-value:hover {
  background: rgba(22, 119, 255, 0.04);
}

.column-view-value-text {
  display: block;
  width: 100%;
  word-break: break-all;
  white-space: pre-wrap;
  max-height: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
}

@media (max-width: 768px) {
  .column-view-name {
    flex-basis: 120px;
    min-width: 120px;
    max-width: 120px;
  }
}

.result-tabs {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

/* Tabs 自带的导航栏隐藏：导航改由卡片标题区的 .result-tabbar 承担 */
.result-tabs :deep(.ant-tabs-nav) {
  display: none;
}

/* 卡片标题区即 Tab 导航（顶替原「查询结果」标题） */
.result-tabbar {
  display: flex;
  align-items: center;
  gap: 18px;
  min-height: 30px;
}

.result-tabbar-item {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 0 5px;
  border: 0;
  background: transparent;
  font-family: inherit;
  font-size: 12px;
  line-height: 1.2;
  font-weight: 400;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  cursor: pointer;
  transition: color 0.2s;
}

.result-tabbar-item:hover {
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
}

/* 激活态：文字与下方高亮下划线同色，都用主题主色的浅色调（teal-500） */
.result-tabbar-item.is-active,
.result-tabbar-item.is-active:hover {
  color: #14b8a6;
  font-weight: 500;
}

.result-tabbar-item.is-active::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 2px;
  border-radius: 1px;
  background: #14b8a6;
}

.result-tabs :deep(.ant-tabs-content-holder),
.result-tabs :deep(.ant-tabs-content),
.result-tabs :deep(.ant-tabs-tabpane) {
  height: 100%;
  min-height: 0;
}

.result-tabs :deep(.ant-tabs-content-holder) {
  flex: 1;
}

.result-pane,
.message-pane,
.info-pane {
  height: 100%;
  min-height: 0;
}

.result-pane {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.result-empty {
  margin-top: auto;
  margin-bottom: auto;
}

.result-table-region {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  padding-bottom: 0;
  box-sizing: border-box;
}

.result-footer-wrap {
  flex-shrink: 0;
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
  min-height: 20px;
  padding-top: 0;
  padding-bottom: 0;
}

.result-footer-right {
  display: inline-flex;
  align-items: center;
  justify-content: flex-end;
  gap: 4px;
  flex: 0 0 auto;
  min-width: 0;
  flex-wrap: wrap;
}

.result-footer-actions {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex: 0 0 auto;
}

.result-pagination-summary {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 4px;
  color: var(--console-muted);
  font-size: 11px;
  line-height: 12px;
  flex-wrap: wrap;
  text-align: right;
  min-width: 0;
  margin-inline-end: 2px;
}

.result-pagination-summary span {
  white-space: nowrap;
}

.result-pagination-wrap :deep(.ant-pagination) {
  margin-left: 0;
  flex-shrink: 0;
  padding: 0;
  line-height: 18px;
}

.result-pagination-wrap :deep(.ant-pagination.ant-pagination-simple .ant-pagination-simple-pager),
.result-pagination-wrap :deep(.ant-pagination.ant-pagination-small .ant-pagination-item),
.result-pagination-wrap :deep(.ant-pagination .ant-pagination-prev),
.result-pagination-wrap :deep(.ant-pagination .ant-pagination-next),
.result-pagination-wrap :deep(.ant-pagination .ant-pagination-jump-prev),
.result-pagination-wrap :deep(.ant-pagination .ant-pagination-jump-next) {
  height: 20px;
  line-height: 18px;
  min-width: 20px;
  border-radius: 4px;
  margin-inline-end: 2px;
}

.result-pagination-wrap :deep(.ant-pagination.ant-pagination-small .ant-pagination-options) {
  padding-block: 0;
  margin-inline-start: 2px;
}

.result-pagination-wrap :deep(.ant-select.ant-select-sm),
.result-pagination-wrap :deep(.ant-select.ant-select-sm .ant-select-selector) {
  height: 20px !important;
  min-height: 20px !important;
}

.result-pagination-wrap :deep(.ant-select.ant-select-sm .ant-select-selection-item),
.result-pagination-wrap :deep(.ant-select.ant-select-sm .ant-select-selection-placeholder) {
  line-height: 20px !important;
}

.result-footer-wrap :deep(.ant-pagination-total-text) {
  margin-inline-end: 0;
  line-height: 20px;
}

@media (max-width: 960px) {
  .result-footer-right {
    width: 100%;
    justify-content: flex-end;
  }
}

.message-pane,
.info-pane {
  overflow: auto;
  border: 0;
  border-radius: 0;
  padding: 8px 8px 6px;
  background: var(--console-card-bg);
}

/* 标题区的 Tab 关闭按钮：默认弱化，hover 变红，点击不触发 Tab 切换（模板里已 stop.prevent） */
.result-tabbar-close {
  margin-left: 4px;
  font-size: 11px;
  color: var(--ant-colorTextQuaternary, rgba(0, 0, 0, 0.25));
  transition: color 0.2s;
}

.result-tabbar-close:hover {
  color: var(--ant-colorError, #ff4d4f);
}

.exec-message {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 11px;
  line-height: 1.45;
}

.result-table {
  min-width: 100%;
}

.result-table :deep(table) {
  table-layout: fixed;
}

.result-table.is-compact-layout {
  min-width: 100%;
}

.result-table.is-compact-layout :deep(.ant-table-container) {
  width: 100%;
  min-width: 100%;
}

.result-table.is-compact-layout :deep(table) {
  width: auto !important;
  min-width: v-bind(tableContentWidthCss) !important;
  table-layout: fixed;
}

.result-card :deep(.ant-table-cell) {
  box-sizing: border-box;
}

.result-card :deep(.ant-table) {
  font-size: 12px;
}

.result-table :deep(.ant-table-thead > tr > th),
.result-table :deep(.ant-table-tbody > tr > td) {
  padding: 6px 10px !important;
  box-sizing: border-box;
}

.result-table :deep(.gi-result-header-cell) {
  position: relative;
  white-space: nowrap;
  overflow: hidden;
}

/* 列宽拖拽热区指示：悬停时右缘显示可拖拽提示线，拖拽中保持高亮 */
.result-table :deep(.gi-result-header-cell)::after {
  content: '';
  position: absolute;
  top: 18%;
  bottom: 18%;
  right: 2px;
  width: 2px;
  border-radius: 1px;
  background: transparent;
  transition: background-color 0.15s ease;
  pointer-events: none;
}

.result-table :deep(.gi-result-header-cell:hover)::after,
.result-table :deep(.gi-result-header-cell.is-col-resizing)::after {
  background: var(--ant-colorPrimary, #0f8a83);
}

.result-table :deep(.gi-result-body-cell) {
  position: relative;
  white-space: nowrap;
  overflow: hidden;
  height: v-bind(rowHeightCss);
  max-height: v-bind(rowHeightCss);
  vertical-align: middle;
  box-sizing: border-box;
}

.result-table :deep(.gi-result-body-cell > .cell-content-wrap) {
  height: 100%;
  max-height: v-bind(rowHeightCss);
  overflow: hidden;
}

.result-card :deep(.ant-table-container) {
  position: relative;
  z-index: 2;
}

.result-card :deep(.ant-table-container::before) {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: 8px;
  pointer-events: none;
  z-index: -1;
}

.result-card :deep(.ant-table),
.result-card :deep(.ant-table table),
.result-card :deep(.ant-table colgroup),
.result-card :deep(.ant-table col) {
  box-sizing: border-box;
}

.result-card :deep(.ant-table table) {
  table-layout: fixed;
}

.result-card :deep(.ant-table-header table),
.result-card :deep(.ant-table-body table),
.result-card :deep(.ant-table-body-inner table) {
  width: 100% !important;
  table-layout: fixed !important;
}

.result-card :deep(.ant-table-body) {
  overflow: auto;
  overflow-x: hidden;
  scrollbar-gutter: auto;
}

.result-card :deep(.ant-table-body::-webkit-scrollbar) {
  display: block !important;
  -webkit-appearance: none !important;
  appearance: none !important;
  width: 10px;
  height: 10px;
  background: transparent;
}

.result-card :deep(.ant-table-body::-webkit-scrollbar-thumb) {
  display: block;
  background: rgba(15, 23, 42, 0.22);
  border-radius: 10px;
  border: 2px solid transparent;
  background-clip: padding-box;
  min-width: 40px;
  min-height: 40px;
}

.result-card :deep(.ant-table-body::-webkit-scrollbar-thumb:hover) {
  background: rgba(15, 23, 42, 0.40);
  border: 2px solid transparent;
  background-clip: padding-box;
}

.result-card :deep(.ant-table-body::-webkit-scrollbar-thumb:active) {
  background: rgba(15, 23, 42, 0.52);
  border: 2px solid transparent;
  background-clip: padding-box;
}

.result-card :deep(.ant-table-body::-webkit-scrollbar-track) {
  display: block;
  background: rgba(15, 23, 42, 0.12);
  border-radius: 10px;
}

.result-card :deep(.ant-table-body::-webkit-scrollbar-track:hover) {
  background: rgba(15, 23, 42, 0.16);
}

.result-card :deep(.ant-table-body::-webkit-scrollbar-corner) {
  background: rgba(15, 23, 42, 0.12);
}

.result-card :deep(.ant-table-body) {
  scrollbar-width: thin;
  scrollbar-color: rgba(15, 23, 42, 0.22) rgba(15, 23, 42, 0.12);
}

.result-card :deep(.ant-table-content),
.result-card :deep(.ant-table-body-inner) {
  overflow: hidden;
}

.result-card :deep(.ant-table-scroll) {
  position: relative;
  overflow: auto;
}

.result-card :deep(.ant-table-ping-left),
.result-card :deep(.ant-table-ping-right) {
  box-shadow: none;
}

.result-card :deep(.ant-table-thead > tr > th),
.result-card :deep(.ant-table-tbody > tr > td) {
  border-right: 0 !important;
  border-left: 0 !important;
  box-shadow: inset -1px 0 0 var(--ant-colorBorderSecondary, #f0f0f0);
  box-sizing: border-box !important;
}

.result-card :deep(.ant-table-thead > tr > th:last-child),
.result-card :deep(.ant-table-tbody > tr > td:last-child) {
  box-shadow: none;
}

.result-card :deep(.ant-table-thead > tr > th:first-child),
.result-card :deep(.ant-table-tbody > tr > td:first-child) {
  box-shadow:
    inset 1px 0 0 var(--ant-colorBorderSecondary, #f0f0f0),
    inset -1px 0 0 var(--ant-colorBorderSecondary, #f0f0f0);
}

.result-card :deep(.ant-table-thead > tr > th:first-child:last-child),
.result-card :deep(.ant-table-tbody > tr > td:first-child:last-child) {
  box-shadow: inset 1px 0 0 var(--ant-colorBorderSecondary, #f0f0f0);
}

.result-card :deep(.ant-table-thead > tr > th:last-child) {
  padding-right: 10px !important;
}

.result-card :deep(.ant-table-tbody > tr > td) {
  border-bottom-color: var(--ant-colorBorderSecondary, #f0f0f0);
}

.gi-col-title-wrap {
  display: block;
  width: 100%;
  min-width: 0;
  overflow: hidden;
  padding-right: 0;
}

.gi-col-title {
  display: block;
  width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  user-select: none;
}

.cell-content-wrap {
  position: relative;
  display: flex;
  align-items: center;
  min-width: 0;
  width: 100%;
  min-height: calc(v-bind(rowHeightCss) - 16px);
  padding-inline: 0;
  border-radius: 4px;
}

.cell-content-wrap.is-long-cell {
  padding-inline-end: 0;
}

.cell-content-wrap.is-context-selected {
  background: rgba(22, 119, 255, 0.08);
  box-shadow: inset 0 0 0 1px rgba(22, 119, 255, 0.22);
}

.cell-ellipsis {
  display: block;
  width: 100%;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: default;
}

.cell-ellipsis.is-long {
  max-width: calc(100% - 22px);
  cursor: default;
}

.cell-expand-btn {
  flex: 0 0 auto;
  width: 20px;
  height: 20px;
  margin-left: 2px;
  padding: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--ant-colorBorderSecondary, rgba(15, 23, 42, 0.1));
  background: var(--ant-colorBgLayout, rgba(248, 250, 252, 0.6));
  color: var(--ant-colorTextSecondary, rgba(15, 23, 42, 0.55));
  border-radius: 6px;
  cursor: pointer;
  font-size: 12px;
  line-height: 1;
  transition: background 120ms ease, color 120ms ease, border-color 120ms ease, transform 120ms ease;
  outline: none;
}

.cell-expand-btn:hover {
  color: #0f8a83;
  border-color: rgba(15, 138, 131, 0.45);
  background: rgba(15, 138, 131, 0.08);
}

.cell-expand-btn:active {
  transform: translateY(0.5px);
}

.cell-tooltip {
  max-width: 640px;
  max-height: 260px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
  font-size: 12px;
  line-height: 1.55;
}

:global(.result-cell-tooltip) {
  pointer-events: auto;
  z-index: 10060;
}

:global(.result-cell-tooltip--long) {
  pointer-events: auto;
  z-index: 10060;
}

:global(.result-cell-tooltip .ant-tooltip-inner) {
  max-width: 560px;
  max-height: 360px;
  min-height: 32px;
  overflow: auto;
  padding: 10px 12px;
  border: 1px solid var(--ant-colorBorderSecondary, rgba(15, 23, 42, 0.12));
  border-radius: 10px;
  background: var(--ant-colorBgElevated, rgba(248, 250, 252, 0.98));
  box-shadow: 0 14px 34px rgba(15, 23, 42, 0.12), 0 2px 10px rgba(15, 23, 42, 0.06);
  backdrop-filter: blur(10px);
  color: var(--ant-colorText, rgba(15, 23, 32, 0.92));
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
  font-size: 12px;
  line-height: 1.55;
  white-space: pre-wrap;
  word-break: break-word;
  pointer-events: auto;
  scrollbar-gutter: stable;
}

:global(.result-cell-tooltip--long .ant-tooltip-inner) {
  max-width: 620px;
  max-height: 400px;
  min-height: 40px;
}

:global(.result-cell-tooltip .ant-tooltip-arrow::before) {
  background: var(--ant-colorBgElevated, rgba(248, 250, 252, 0.98));
}

:global(.result-cell-tooltip .ant-tooltip-inner::-webkit-scrollbar) {
  width: 8px;
  height: 8px;
}

:global(.result-cell-tooltip .ant-tooltip-inner::-webkit-scrollbar-track) {
  background: transparent;
  border-radius: 9999px;
}

:global(.result-cell-tooltip .ant-tooltip-inner::-webkit-scrollbar-thumb) {
  background-color: rgba(15, 23, 42, 0.22);
  border-radius: 9999px;
  border: 1px solid rgba(248, 250, 252, 0.65);
}

:global(.result-cell-tooltip .ant-tooltip-inner::-webkit-scrollbar-thumb:hover) {
  background-color: rgba(15, 23, 42, 0.34);
}

:global(.result-cell-popover) {
  min-width: 480px;
  max-width: 820px;
  pointer-events: auto;
  z-index: 10060;
}

:global(.result-cell-popover .ant-popover-inner) {
  padding: 0;
  border: 1px solid var(--ant-colorBorderSecondary, rgba(15, 23, 42, 0.12));
  border-radius: 12px;
  background: var(--ant-colorBgElevated, rgba(248, 250, 252, 0.985));
  box-shadow: 0 20px 48px rgba(15, 23, 42, 0.16), 0 4px 14px rgba(15, 23, 42, 0.08);
  backdrop-filter: blur(12px);
  overflow: hidden;
}

:global(.result-cell-popover .ant-popover-arrow) {
  display: none;
}

:global(.result-cell-popover .ant-popover-title) {
  margin: 0;
  padding: 0;
  min-width: 0;
  border-bottom: 0;
  background: transparent;
}

.result-cell-popover-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 14px 10px;
  border-bottom: 1px solid var(--ant-colorBorderSecondary, rgba(15, 23, 42, 0.08));
  background: rgba(255, 255, 255, 0.55);
}

.result-cell-popover-title-col {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
  font-size: 13px;
  color: var(--ant-colorText, rgba(15, 23, 42, 0.92));
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
}

.result-cell-popover-title-meta {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--ant-colorTextSecondary, rgba(15, 23, 42, 0.6));
  font-size: 11px;
  line-height: 1;
}

.result-cell-popover-tag {
  display: inline-flex;
  align-items: center;
  height: 18px;
  padding: 0 7px;
  border-radius: 999px;
  background: rgba(15, 138, 131, 0.1);
  color: #0f8a83;
  font-weight: 600;
  font-size: 11px;
  line-height: 18px;
}

.result-cell-popover-body {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.result-cell-popover-pre {
  margin: 0;
  padding: 12px 14px;
  max-height: 360px;
  min-height: 72px;
  overflow: auto;
  background: var(--ant-colorBgContainer, #ffffff);
  color: var(--ant-colorText, rgba(15, 23, 42, 0.92));
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}

.result-cell-popover-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  padding: 10px 14px;
  border-top: 1px solid var(--ant-colorBorderSecondary, rgba(15, 23, 42, 0.08));
  background: rgba(255, 255, 255, 0.55);
}

.cell-modal-body {
  max-height: 62vh;
  overflow: auto;
  border: 1px solid var(--ant-colorBorderSecondary, #f0f0f0);
  border-radius: 8px;
  padding: 10px;
  background: var(--ant-colorBgContainer, #ffffff);
}

.cell-modal-content {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
  font-size: 12px;
  line-height: 1.55;
}

.result-context-menu {
  position: fixed;
  z-index: 2100;
  width: 110px;
  padding: 5px;
  border: 1px solid var(--ant-colorBorderSecondary, rgba(15, 23, 42, 0.12));
  border-radius: 10px;
  background: var(--ant-colorBgElevated, rgba(248, 250, 252, 0.98));
  box-shadow: 0 14px 34px rgba(15, 23, 42, 0.12), 0 2px 10px rgba(15, 23, 42, 0.06);
  backdrop-filter: blur(10px);
}

.result-context-menu-item {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  min-height: 30px;
  padding: 0 8px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--ant-colorText, rgba(15, 23, 32, 0.9));
  font-size: 13px;
  line-height: 1;
  text-align: left;
  white-space: nowrap;
  cursor: pointer;
  transition: background-color 120ms ease, color 120ms ease;
}

.result-context-menu-item:hover {
  background: rgba(15, 118, 110, 0.1);
  color: var(--ant-colorText, rgba(15, 23, 32, 0.9));
}

.result-context-menu-divider {
  height: 1px;
  margin: 5px 4px;
  background: var(--ant-colorBorderSecondary, rgba(15, 23, 42, 0.12));
}

.result-context-menu-item.has-submenu {
  cursor: default;
}

.result-context-menu-arrow {
  color: var(--ant-colorTextQuaternary, rgba(0, 0, 0, 0.25));
  font-size: 12px;
}

.result-context-menu-item.has-submenu {
  position: relative;
}

.result-context-menu-item.has-submenu::before {
  content: '';
  position: absolute;
  top: -12px;
  left: 100%;
  width: calc(6px + 18px);
  height: calc(100% + 24px);
  background: transparent;
  z-index: 0;
  pointer-events: auto;
}

.result-context-menu-item.has-submenu.open-upward::before {
  top: auto;
  bottom: -12px;
}

.result-context-menu-item.has-submenu.open-leftward::before {
  left: auto;
  right: 100%;
}

.result-context-submenu {
  position: absolute;
  top: -12px;
  left: calc(100% + 6px);
  display: none;
  width: 70px;
  padding: 5px;
  border: 1px solid var(--ant-colorBorderSecondary, rgba(15, 23, 42, 0.12));
  border-radius: 10px;
  background: var(--ant-colorBgElevated, rgba(248, 250, 252, 0.98));
  box-shadow: 0 14px 34px rgba(15, 23, 42, 0.12), 0 2px 10px rgba(15, 23, 42, 0.06);
  backdrop-filter: blur(10px);
}

.result-context-menu-item.has-submenu.open-upward .result-context-submenu {
  top: auto;
  bottom: -12px;
}

.result-context-menu-item.has-submenu.open-leftward .result-context-submenu {
  left: auto;
  right: calc(100% + 6px);
}

.result-context-menu-item.has-submenu.open-upward.open-leftward::before {
  top: auto;
  bottom: -12px;
  left: auto;
  right: 100%;
}

.result-card :deep(.ant-table-thead > tr > th) {
  font-weight: 600;
  background: var(--ant-colorFillAlter, #fafafa);
}

.result-card :deep(.ant-pagination) {
  line-height: 1;
  font-size: 11px;
}

.result-card :deep(.ant-pagination-item),
.result-card :deep(.ant-pagination-prev),
.result-card :deep(.ant-pagination-next),
.result-card :deep(.ant-pagination-options-size-changer) {
  margin-bottom: 0;
}

.result-card :deep(.ant-pagination-item),
.result-card :deep(.ant-pagination-prev .ant-pagination-item-link),
.result-card :deep(.ant-pagination-next .ant-pagination-item-link) {
  min-width: 22px;
  height: 22px;
  line-height: 20px;
  font-size: 11px;
}

.result-card :deep(.ant-pagination-options) {
  display: inline-flex;
  align-items: center;
}

.result-card :deep(.ant-pagination-options-size-changer) {
  min-width: 88px;
  font-size: 11px;
}

.result-card :deep(.ant-pagination-options-size-changer .ant-select-selector) {
  height: 22px;
  padding: 0 8px;
}

.result-card :deep(.ant-pagination-options-size-changer .ant-select-selection-item),
.result-card :deep(.ant-pagination-options-size-changer .ant-select-selection-search-input) {
  height: 20px;
  line-height: 20px;
  font-size: 11px;
}

.result-card :deep(.ant-pagination-jump-next),
.result-card :deep(.ant-pagination-jump-prev) {
  min-width: 22px;
  height: 22px;
  line-height: 22px;
}

/* 结果全屏：与编辑区全屏同套交互（class 切换 + Esc 退出），fixed 覆盖层铺满视口 */
.console-page.is-result-fullscreen .result-card {
  position: fixed;
  inset: 0;
  z-index: 1000;
  width: 100%;
  height: 100%;
  border: 0;
  border-radius: 0;
  box-shadow: none;
  margin: 0;
  background: var(--ant-colorBgContainer, #ffffff);
}

.console-page.is-result-fullscreen .result-card :deep(.ant-card-head) {
  border-bottom-color: var(--ant-colorBorderSecondary, #f0f0f0);
  padding-inline: 14px;
}

.console-page.is-result-fullscreen .result-card :deep(.ant-card-body) {
  padding: 12px;
}

@media (max-width: 1200px) {
  .console-workspace {
    gap: 6px;
  }

  .workspace-tabbar {
    padding: 6px 6px 0;
    gap: 8px;
  }

  .workspace-tabbar-actions {
    padding-bottom: 5px;
  }

  /* 窄屏分支不再改写 SplitPanel 的布局方向：宽度统一由组件管理
     （默认 280 / 最小 0 / 最大 600），这里只保留纵向尺寸约束。
     原先的 `width:100% !important` + `flex:0 0 clamp(180px,32%,280px)` 会
     盖掉默认宽度并退回「按视口比例」的老行为，与新的取值区间冲突。 */
  .console-shell-body :deep(.split-wrapper) {
    height: 100%;
    max-height: none;
    min-height: 0;
    padding: 8px;
  }

  /* 只做「不超过容器 40%」的上限保护，不覆盖具体宽度：
     宽屏下仍是 280px 默认宽度，极窄屏才等比收敛，避免工作区被挤没。 */
  .console-page :deep(.split-wrapper .scalable) {
    max-width: 40%;
    flex: 0 0 auto;
    min-height: 0;
  }

  .console-shell-body :deep(.split-wrapper .right-content) {
    padding: 0;
    min-height: 0;
    flex: 1 1 auto;
  }
}

@media (max-width: 768px) {
  .console-page :deep(.split-wrapper) {
    padding: 6px;
    gap: 8px;
  }

  .result-card :deep(.ant-card-head) {
    padding-inline: 10px;
  }

  .result-card :deep(.ant-card-body) {
    padding: 10px;
  }

  .result-search {
    width: 140px;
  }

  .result-toolbar-search {
    width: 140px;
    min-width: 140px;
  }
}

@media (max-width: 768px) {
  /* 左树 + 右工作区双栏：窄屏给树一个合理占比，避免编辑器被压到 180px 以下 */
  .console-shell-body :deep(.split-wrapper) {
    padding: 6px;
    gap: 6px;
  }

  .console-page :deep(.split-wrapper .scalable) {
    flex: 0 0 44% !important;
    max-width: 44% !important;
    min-width: 120px;
  }

  .console-page :deep(.split-wrapper .separator) {
    width: 6px;
  }

  .console-workspace {
    padding: 4px 8px 8px;
  }

  /* 结果卡头部：Tab 与工具栏换行，否则右侧按钮被 overflow:hidden 直接裁掉 */
  .result-card :deep(.ant-card-head-wrapper) {
    flex-wrap: wrap;
    row-gap: 6px;
    min-height: 0;
  }

  .result-card :deep(.ant-card-head-title) {
    flex: 1 1 100%;
    min-height: 0;
    flex-shrink: 1;
  }

  .result-card :deep(.ant-card-extra) {
    flex: 1 1 100%;
    width: 100%;
    max-width: 100%;
    overflow-x: auto;
    overflow-y: hidden;
    -webkit-overflow-scrolling: touch;
  }

  .result-tabbar {
    gap: 12px;
    width: 100%;
    overflow-x: auto;
    scrollbar-width: none;
  }

  .result-tabbar::-webkit-scrollbar {
    display: none;
  }

  .result-card-toolbar {
    flex-wrap: wrap;
    row-gap: 6px;
    margin-left: 0;
    width: 100%;
  }

  .result-toolbar-search {
    flex: 1 1 120px;
    width: auto;
    min-width: 110px;
  }

  .result-tabbar-close {
    margin-left: 2px;
    padding: 6px;
    font-size: 13px;
  }

  /* 标签页按钮放大到触控尺寸；关闭按钮默认 opacity:0，触屏没有 hover 会看不见 */
  .workspace-tab-close {
    opacity: 0.9;
    width: 28px;
    height: 28px;
  }

  .workspace-tab-tool-btn {
    width: 28px;
    height: 28px;
  }

  .workspace-tabbar {
    padding: 4px 4px 0 2px;
    gap: 6px;
  }

  .workspace-tab-scroll {
    padding: 0 8px;
  }

  .workspace-tab-item {
    max-width: 168px;
    height: 32px;
  }

  .workspace-tab-title {
    max-width: 110px;
    font-size: 11px;
  }

  .column-view-name {
    flex-basis: 88px;
    min-width: 88px;
    max-width: 88px;
    padding: 8px 6px 8px 8px;
    font-size: 11px;
  }

  .result-footer-wrap {
    justify-content: space-between;
    row-gap: 6px;
  }

  .result-footer-right {
    justify-content: space-between;
    width: 100%;
  }
}
</style>
