<template>
  <div ref="consoleEditorCardRef" class="console-editor-card" @contextmenu.prevent @click="onConsoleWorkspaceClicked">
    <div class="console-topbar">
      <div v-if="showTopTabbar" class="topbar-row">
        <div class="tabbar-center">
          <a-button
            v-if="tabScrollState.canScrollLeft"
            size="small"
            class="tab-scroll-button"
            @click="scrollTabsBy(-240)"
          >
            <template #icon>
              <LeftOutlined />
            </template>
          </a-button>
          <div
            ref="tabScrollerRef"
            class="console-tab-scroll"
            @scroll="updateTabScrollState"
            @wheel.prevent="handleTabWheel"
          >
            <div v-if="!visiblePanes.length" class="console-tab-empty">暂无 SQL窗口，点击右上角 + 新建</div>
            <a-dropdown
              v-for="pane in visiblePanes"
              :key="pane.key"
              placement="bottomLeft"
              :trigger="['contextmenu']"
            >
              <div class="console-tab-shell">
                <div
                  v-if="renamingTabKey !== pane.key"
                  :data-tab-key="pane.key"
                  class="console-tab-item"
                  :class="[
                    `tab-kind-${pane.kind}`,
                    {
                      'is-active': pane.key === uiData.activeKey,
                      'is-pinned': pane.pinned,
                      'is-dragging': draggingTabKey === pane.key,
                    },
                  ]"
                  :style="getPaneEnvironmentStyle(pane)"
                  :draggable="pane.kind === 'console'"
                  @click="handleTabChange(pane.key)"
                  @dblclick.stop="startRenamePane(pane)"
                  @dragstart="handleTabDragStart(pane, $event)"
                  @dragover.prevent="handleTabDragOver(pane, $event)"
                  @drop.prevent="handleTabDrop(pane, $event)"
                  @dragend="handleTabDragEnd"
                >
                  <AppTooltip
                    placement="bottomLeft"
                    :title="buildPaneTooltipTitle(pane)"
                  >
                    <template #title>
                      <div class="tab-tooltip-card tab-tooltip-card--simple">
                        <div class="tab-tooltip-card__head">
                          <DatabaseFilled
                            class="tab-tooltip-card__icon"
                            :class="{ 'is-unbound': !isPaneBoundToInstance(pane) && pane?.kind !== 'preview' && !pane?.customTitle }"
                          />
                          <span
                            class="tab-tooltip-card__instance-name"
                            :class="{ 'is-unbound': !isPaneBoundToInstance(pane) && pane?.kind !== 'preview' && !pane?.customTitle }"
                          >
                            {{ buildPaneTooltipVm(pane)?.instanceName || buildPaneTitle(pane) || '窗口' }}
                          </span>
                        </div>
                        <div
                          v-if="buildPaneTooltipVm(pane)?.host"
                          class="tab-tooltip-card__row tab-tooltip-card__row--sub"
                        >
                          <EnvironmentOutlined class="tab-tooltip-card__row-icon" />
                          <span class="tab-tooltip-card__row-text">
                            {{ buildPaneTooltipVm(pane)?.host }}{{ buildPaneTooltipVm(pane)?.port ? `:${buildPaneTooltipVm(pane)?.port}` : '' }}
                          </span>
                        </div>
                      </div>
                    </template>
                    <div class="console-tab-content">
                      <span class="console-tab-status" :class="`status-${getPaneStatus(pane)}`">
                        <WarningOutlined v-if="getPaneStatus(pane) === 'disconnected'" />
                        <LoadingOutlined v-else-if="getPaneStatus(pane) === 'running'" />
                        <span v-else-if="getPaneStatus(pane) === 'dirty'" class="console-tab-dirty-dot"></span>
                        <TableOutlined v-else-if="pane.kind === 'preview'" />
                        <ConsoleDbIcon
                          v-else-if="pane.kind !== 'preview' && isPaneBoundToInstance(pane)"
                          :type="getPaneDbType(pane) || 'mysql'"
                        />
                        <CodeOutlined v-else />
                      </span>
                      <span class="console-tab-texts">
                        <span
                          v-if="getPaneEnvironmentMeta(pane)?.color"
                          class="console-tab-env-dot"
                        />
                        <span
                          class="console-tab-title"
                          :class="{ 'is-unbound': pane.kind !== 'preview' && !isPaneBoundToInstance(pane) && !pane.customTitle }"
                        >{{ buildPaneTitle(pane) }}</span>
                      </span>
                      <PushpinFilled v-if="pane.pinned" class="console-tab-pin" />
                    </div>
                  </AppTooltip>
                  <button
                    v-if="canClosePane(pane)"
                    type="button"
                    class="console-tab-close"
                    @click.stop="removeTab(pane.key)"
                  >
                    <CloseOutlined />
                  </button>
                </div>
                <div
                  v-else
                  :data-tab-key="pane.key"
                  class="console-tab-item is-active is-editing"
                >
                  <span class="console-tab-status">
                    <EditOutlined />
                  </span>
                  <a-input
                    ref="renameInputRef"
                    v-model:value="renamingValue"
                    size="small"
                    class="console-tab-rename-input"
                    @click.stop
                    @blur="commitRenamePane()"
                    @pressEnter="commitRenamePane()"
                    @keydown.esc.stop.prevent="cancelRenamePane()"
                  />
                </div>
              </div>
              <template #overlay>
                <a-menu
                  class="console-tab-context-menu"
                  @contextmenu.prevent.stop
                  @click="({ key }) => handlePaneContextMenuAction(key, pane)"
                >
                  <a-menu-item key="rename">重命名标签</a-menu-item>
                  <a-menu-item :key="pane.pinned ? 'unpin' : 'pin'">
                    {{ pane.pinned ? '取消固定标签' : '固定标签' }}
                  </a-menu-item>
                  <a-menu-item key="close" :disabled="!canClosePane(pane)">关闭当前标签</a-menu-item>
                  <a-menu-item key="close-others">关闭其他所有标签</a-menu-item>
                  <a-menu-item key="close-right">关闭右侧所有标签</a-menu-item>
                  <a-menu-divider />
                  <a-menu-item key="copy-connection">复制当前 SQL窗口连接串</a-menu-item>
                  <a-menu-item key="split-horizontal" disabled>横向拆分窗口</a-menu-item>
                  <a-menu-item key="split-vertical" disabled>纵向拆分窗口</a-menu-item>
                </a-menu>
              </template>
            </a-dropdown>
          </div>
          <a-button
            v-if="tabScrollState.canScrollRight"
            size="small"
            class="tab-scroll-button"
            @click="scrollTabsBy(240)"
          >
            <template #icon>
              <RightOutlined />
            </template>
          </a-button>
        </div>

        <div class="tabbar-right">
          <div class="tabbar-split-create">
            <a-button
              size="small"
              type="primary"
              class="tabbar-primary-action tabbar-primary-action--main"
              @click="addBlankConsoleTab()"
            >
              <template #icon>
                <PlusOutlined />
              </template>
              新建
            </a-button>
            <a-dropdown placement="bottomRight" :trigger="['click']">
              <a-button size="small" class="tabbar-entry-button tabbar-primary-action--toggle">
                <template #icon>
                  <DownOutlined />
                </template>
              </a-button>
              <template #overlay>
                <a-menu class="console-tab-create-menu" @click="handleCreateTabMenuAction">
                  <a-menu-item key="current">基于当前 SQL窗口新建</a-menu-item>
                  <a-menu-divider />
                  <a-menu-item v-for="item in uiData.instances" :key="`instance:${item.instance_id}`">
                    {{ formatInstanceLabel(item) }}
                  </a-menu-item>
                </a-menu>
              </template>
            </a-dropdown>
          </div>
          <a-dropdown placement="bottomRight" :trigger="['click']">
            <a-button size="small" class="tabbar-entry-button tabbar-more-button">
              <template #icon>
                <EllipsisOutlined />
              </template>
            </a-button>
            <template #overlay>
              <a-menu class="console-tab-global-menu" @click="handleGlobalTabAction">
                <a-menu-item key="close-all">关闭所有标签</a-menu-item>
                <a-menu-item key="close-others">关闭其他标签</a-menu-item>
                <a-menu-item :key="activePane?.pinned ? 'unpin-current' : 'pin-current'">
                  {{ activePane?.pinned ? '取消固定当前标签' : '固定当前标签' }}
                </a-menu-item>
                <a-menu-item key="restore-layout">恢复默认标签布局</a-menu-item>
              </a-menu>
            </template>
          </a-dropdown>
        </div>
      </div>

      <div class="toolbar-row">
        <div v-if="hasActivePane" class="action-group">
          <a-space class="console-toolbar" :size="4">
            <a-button
              size="small"
              type="primary"
              :loading="currentTabLoading"
              :disabled="!hasActivePane || currentTabLoading || uiState.actionCooldown.execute"
              @click="executeSqlQuery()"
            >
              <template #icon>
                <PlayCircleOutlined />
              </template>
              执行
            </a-button>
            <AppTooltip title="解释 SQL（生成执行计划）">
              <span class="inline-disabled-button">
                <a-button
                  size="small"
                  class="tool-icon-button"
                  :disabled="!hasActivePane || currentTabLoading || !canUseExplain || uiState.actionCooldown.explain"
                  @click="explainSqlQuery()"
                >
                  <template #icon>
                    <FileSearchOutlined />
                  </template>
                </a-button>
              </span>
            </AppTooltip>
            <AppTooltip title="取消正在执行的 SQL">
              <a-button
                size="small"
                class="tool-icon-button tool-icon-button--danger"
                :disabled="!hasActivePane || !currentTabLoading"
                @click="cancelSqlExecution()"
              >
                <template #icon>
                  <CloseCircleOutlined />
                </template>
              </a-button>
            </AppTooltip>
            <AppTooltip title="格式化当前 SQL">
              <a-button size="small" class="tool-icon-button" :disabled="!hasActivePane" @click="formatSqlContent()">
                <template #icon>
                  <CodeOutlined />
                </template>
              </a-button>
            </AppTooltip>
            <AppTooltip title="更多操作">
              <a-dropdown placement="bottomLeft" trigger="click">
                <a-button size="small" class="tool-icon-button more-action-button" :disabled="!hasActivePane">
                  <template #icon>
                    <EllipsisOutlined />
                  </template>
                </a-button>
                <template #overlay>
                  <a-menu class="console-more-menu">
                    <a-menu-item key="favorite" @click="addToFavorites()">
                      <StarOutlined />
                      SQL收藏
                    </a-menu-item>
                    <a-menu-item v-if="canOpenFavoriteLibrary" key="favorite-library" @click="openSqlLibraryDrawer('favorite')">
                      <FolderOpenOutlined />
                      SQL收藏库
                    </a-menu-item>
                    <a-menu-item v-if="canOpenHistoryLibrary" key="history-library" @click="openSqlLibraryDrawer('history')">
                      <TableOutlined />
                      SQL历史
                    </a-menu-item>
                    <a-menu-item key="db-dict" :disabled="!canUseDbDict">
                      <a v-if="canUseDbDict" @click="generatorDataDictionary()">
                        <BookOutlined />
                        数据字典
                      </a>
                      <span v-else class="disabled-menu-item">
                        <BookOutlined />
                        数据字典（仅 MySQL 系）
                      </span>
                    </a-menu-item>
                  </a-menu>
                </template>
              </a-dropdown>
            </AppTooltip>
          </a-space>
        </div>

        <div v-if="hasActivePane" class="context-group">
          <div class="context-control context-control--instance">
            <span class="control-label">实例</span>
            <AppTooltip v-if="uiData.instance_id" :title="currentInstanceLabel" placement="topLeft">
              <div class="instance-select-shell">
              <a-select
                class="context-select instance-select"
                :class="{ 'is-display-overlay': showInstanceAddrDisplay }"
                v-model:value="uiData.instance_id"
                size="small"
                show-search
                allow-clear
                placeholder="实例"
                option-label-prop="displayLabel"
                dropdown-class-name="context-select-dropdown instance-select-dropdown instance-select-dropdown--two-line"
                :options="instanceSelectOptions"
                :filter-option="filterInstanceOption"
                :loading="runtime.instanceLoading"
                :disabled="!hasActivePane"
                @change="handleInstanceChange"
                @focus="instanceSelectFocused = true"
                @blur="instanceSelectFocused = false"
                @dropdownVisibleChange="handleSelectDropdownVisibleChange"
              >
                <template #option="{ ellipsisRemark, hostPort, fullRemark, longLabel, environmentName, environmentColor }">
                  <div class="instance-select-option">
                    <div class="instance-select-option__head">
                      <div class="instance-select-option__name" :title="fullRemark || longLabel">
                        {{ ellipsisRemark }}
                      </div>
                      <span
                        v-if="environmentName"
                        class="instance-select-option__env"
                        :style="buildEnvironmentTagStyle(environmentColor)"
                      >{{ environmentName }}</span>
                    </div>
                    <div class="instance-select-option__addr" :title="hostPort">
                      {{ hostPort }}
                    </div>
                  </div>
                </template>
              </a-select>
              <div v-if="showInstanceAddrDisplay" class="instance-select-display" aria-hidden="true">
                <span class="instance-select-display__name">{{ instanceDisplayName }}</span>
                <span class="instance-select-display__addr">{{ selectedInstanceAddr }}</span>
              </div>
              </div>
            </AppTooltip>
            <div v-else class="instance-select-shell">
            <a-select
              class="context-select instance-select"
              :class="{ 'is-display-overlay': showInstanceAddrDisplay }"
              v-model:value="uiData.instance_id"
              size="small"
              show-search
              allow-clear
              placeholder="实例"
              option-label-prop="displayLabel"
              dropdown-class-name="context-select-dropdown instance-select-dropdown instance-select-dropdown--two-line"
              :options="instanceSelectOptions"
              :filter-option="filterInstanceOption"
              :loading="runtime.instanceLoading"
              :disabled="!hasActivePane"
              @change="handleInstanceChange"
              @focus="instanceSelectFocused = true"
              @blur="instanceSelectFocused = false"
              @dropdownVisibleChange="handleSelectDropdownVisibleChange"
            >
              <template #option="{ ellipsisRemark, hostPort, fullRemark, longLabel, environmentName, environmentColor }">
                <div class="instance-select-option">
                  <div class="instance-select-option__head">
                    <div class="instance-select-option__name" :title="fullRemark || longLabel">
                      {{ ellipsisRemark }}
                    </div>
                    <span
                      v-if="environmentName"
                      class="instance-select-option__env"
                      :style="buildEnvironmentTagStyle(environmentColor)"
                    >{{ environmentName }}</span>
                  </div>
                  <div class="instance-select-option__addr" :title="hostPort">
                    {{ hostPort }}
                  </div>
                </div>
              </template>
            </a-select>
            <div v-if="showInstanceAddrDisplay" class="instance-select-display" aria-hidden="true">
              <span class="instance-select-display__name">{{ instanceDisplayName }}</span>
              <span class="instance-select-display__addr">{{ selectedInstanceAddr }}</span>
            </div>
            </div>
          </div>

          <div class="context-control context-control--database">
            <span class="control-label">{{ databaseLabel }}</span>
            <AppTooltip v-if="uiData.database" :title="currentDatabaseLabel" placement="topLeft">
              <a-select
                class="context-select database-select"
                v-model:value="uiData.database"
                size="small"
                show-search
                allow-clear
                :placeholder="databaseLabel"
                option-label-prop="label"
                dropdown-class-name="context-select-dropdown database-select-dropdown"
                :filter-option="filterDatabaseOption"
                :loading="runtime.databaseLoading"
                :disabled="!hasActivePane || !canSelectDatabase"
                @change="handleDatabaseChange"
                @dropdownVisibleChange="handleSelectDropdownVisibleChange"
              >
                <a-select-option v-for="item in uiData.databaseOptions" :key="item.value" :value="item.value" :label="item.label">
                  <AppTooltip :title="item.label" placement="topLeft">
                    <span class="select-option-label">{{ item.label }}</span>
                  </AppTooltip>
                </a-select-option>
              </a-select>
            </AppTooltip>
            <a-select
              v-else
              class="context-select database-select"
              v-model:value="uiData.database"
              size="small"
              show-search
              allow-clear
              :placeholder="databaseLabel"
              option-label-prop="label"
              dropdown-class-name="context-select-dropdown database-select-dropdown"
              :filter-option="filterDatabaseOption"
              :loading="runtime.databaseLoading"
              :disabled="!hasActivePane || !canSelectDatabase"
              @change="handleDatabaseChange"
              @dropdownVisibleChange="handleSelectDropdownVisibleChange"
            >
              <a-select-option v-for="item in uiData.databaseOptions" :key="item.value" :value="item.value" :label="item.label">
                <AppTooltip :title="item.label" placement="topLeft">
                  <span class="select-option-label">{{ item.label }}</span>
                </AppTooltip>
              </a-select-option>
            </a-select>
          </div>

          <div v-if="showsSchemaSelector" class="context-control context-control--schema">
            <span class="control-label">{{ schemaLabel }}</span>
            <AppTooltip v-if="uiData.schema" :title="currentSchemaLabel" placement="topLeft">
              <a-select
                class="context-select schema-select"
                v-model:value="uiData.schema"
                size="small"
                show-search
                allow-clear
                :placeholder="schemaLabel"
                option-label-prop="label"
                dropdown-class-name="context-select-dropdown schema-select-dropdown"
                :filter-option="filterSchemaOption"
                :loading="runtime.schemaLoading"
                :disabled="!hasActivePane || !canSelectSchema"
                @change="handleSchemaChange"
                @dropdownVisibleChange="handleSelectDropdownVisibleChange"
              >
                <a-select-option v-for="item in uiData.schemaOptions" :key="item.value" :value="item.value" :label="item.label">
                  <AppTooltip :title="item.label" placement="topLeft">
                    <span class="select-option-label">{{ item.label }}</span>
                  </AppTooltip>
                </a-select-option>
              </a-select>
            </AppTooltip>
            <a-select
              v-else
              class="context-select schema-select"
              v-model:value="uiData.schema"
              size="small"
              show-search
              allow-clear
              :placeholder="schemaLabel"
              option-label-prop="label"
              dropdown-class-name="context-select-dropdown schema-select-dropdown"
              :filter-option="filterSchemaOption"
              :loading="runtime.schemaLoading"
              :disabled="!hasActivePane || !canSelectSchema"
              @change="handleSchemaChange"
              @dropdownVisibleChange="handleSelectDropdownVisibleChange"
            >
              <a-select-option v-for="item in uiData.schemaOptions" :key="item.value" :value="item.value" :label="item.label">
                <AppTooltip :title="item.label" placement="topLeft">
                  <span class="select-option-label">{{ item.label }}</span>
                </AppTooltip>
              </a-select-option>
            </a-select>
          </div>

        </div>
      </div>

      <a-alert
        v-if="uiState.contextError.title"
        :type="uiState.contextError.type === 'warning' ? 'warning' : 'error'"
        show-icon
        closable
        class="context-error-alert"
        :message="uiState.contextError.title"
        :description="contextErrorDescription"
        @close="clearContextError"
      >
        <template #action v-if="canRetryContextError || canClearContextSelection || canInspectContextError">
          <a-space :size="6">
            <a-button
              v-if="canRetryContextError"
              size="small"
              type="link"
              :style="{ paddingLeft: 0, paddingRight: 0 }"
              @click="handleRetryContextError"
            >重试连接</a-button>
            <a-button
              v-if="canClearContextSelection"
              size="small"
              type="link"
              :style="{ paddingLeft: 0, paddingRight: 0 }"
              @click="handleClearContextSelection"
            >清空选择</a-button>
          </a-space>
        </template>
      </a-alert>

      <div v-if="uiState.actionHint" class="hint-row" :class="`is-${uiState.actionHintTone}`">
        <component :is="actionHintIcon" />
        <span class="hint-text">{{ uiState.actionHint }}</span>
      </div>

    </div>

    <div class="console-editor-wrap" :class="{ 'is-empty': !hasActivePane }">
      <template v-if="hasActivePane">
        <a-spin :spinning="currentTabLoading" tip="Loading...">
          <a-dropdown placement="bottomLeft" :trigger="['contextmenu']">
            <div class="console-editor-surface">
              <CodeMirror
                ref="codemirrorRef"
                :init-val="uiData.editorState.content"
                :height="editorHeightPx"
                :dialect="currentDbFamily"
                @state-change="handleEditorStateChange"
              />
            </div>
            <template #overlay>
              <a-menu
                class="console-editor-context-menu"
                @contextmenu.prevent.stop
                @click="handleEditorContextMenuAction"
              >
                <a-menu-item key="execute" :disabled="!canUseEditorContextAction('execute')">执行</a-menu-item>
                <a-menu-item key="format" :disabled="!canUseEditorContextAction('format')">格式化</a-menu-item>
                <a-menu-item key="explain" :disabled="!canUseEditorContextAction('explain')">执行计划</a-menu-item>
                <a-menu-item key="favorite" :disabled="!canUseEditorContextAction('favorite')">SQL收藏</a-menu-item>
                <a-menu-item key="favorite-library" :disabled="!canUseEditorContextAction('favorite-library')">SQL收藏库</a-menu-item>
                <a-menu-item key="history-library" :disabled="!canUseEditorContextAction('history-library')">SQL历史</a-menu-item>
                <a-menu-divider />
                <a-menu-item key="cut" :disabled="!canUseEditorContextAction('cut')">剪切</a-menu-item>
                <a-menu-item key="copy" :disabled="!canUseEditorContextAction('copy')">复制</a-menu-item>
                <a-menu-item
                  key="paste"
                  :disabled="!canUseEditorContextAction('paste')"
                  @mousedown.prevent="onPasteItemMouseDown"
                >粘贴</a-menu-item>
                <a-menu-item key="select-all" :disabled="!canUseEditorContextAction('select-all')">全选</a-menu-item>
              </a-menu>
            </template>
          </a-dropdown>
        </a-spin>
      </template>
      <div v-else class="console-empty-state">
        <div class="console-home-shell">
          <div class="console-home-hero">
            <div class="console-home-illustration" aria-hidden="true">
              <div class="console-home-illustration-ring"></div>
              <div class="console-home-illustration-core">
                <CodeOutlined />
              </div>
              <span class="console-home-illustration-chip console-home-illustration-chip--sql">SQL</span>
              <span class="console-home-illustration-chip console-home-illustration-chip--play">
                <PlayCircleOutlined />
              </span>
              <span class="console-home-illustration-dot console-home-illustration-dot--one"></span>
              <span class="console-home-illustration-dot console-home-illustration-dot--two"></span>
            </div>
            <div class="console-home-intro">
              <div class="console-home-title">SQL窗口</div>
              <div class="console-home-action-bar">
                <div class="instance-select-shell">
                <a-select
                  v-model:value="emptyHomeData.selectedInstanceId"
                  class="console-home-instance-select instance-select"
                  :class="{ 'is-display-overlay': showHomeInstanceAddrDisplay }"
                  size="large"
                  show-search
                  allow-clear
                  placeholder="请选择数据源"
                  option-label-prop="displayLabel"
                  dropdown-class-name="context-select-dropdown instance-select-dropdown instance-select-dropdown--two-line"
                  :options="instanceSelectOptions"
                  :filter-option="filterInstanceOption"
                  :loading="runtime.instanceLoading"
                  @focus="homeInstanceSelectFocused = true"
                  @blur="homeInstanceSelectFocused = false"
                  @dropdownVisibleChange="handleSelectDropdownVisibleChange"
                >
                  <template #option="{ ellipsisRemark, hostPort, fullRemark, longLabel, environmentName, environmentColor }">
                    <div class="instance-select-option">
                      <div class="instance-select-option__head">
                        <div class="instance-select-option__name" :title="fullRemark || longLabel">
                          {{ ellipsisRemark }}
                        </div>
                        <span
                          v-if="environmentName"
                          class="instance-select-option__env"
                          :style="buildEnvironmentTagStyle(environmentColor)"
                        >{{ environmentName }}</span>
                      </div>
                      <div class="instance-select-option__addr" :title="hostPort">
                        {{ hostPort }}
                      </div>
                    </div>
                  </template>
                </a-select>
                <div v-if="showHomeInstanceAddrDisplay" class="instance-select-display" aria-hidden="true">
                  <span class="instance-select-display__name">{{ emptyHomeInstanceDisplayName }}</span>
                  <span class="instance-select-display__addr">{{ emptyHomeSelectedInstanceAddr }}</span>
                </div>
                </div>
                <a-button
                  type="primary"
                  size="large"
                  :disabled="!canStartEmptyHomeQuery"
                  @click="startQueryFromEmptyHome()"
                >
                  开始查询
                </a-button>
              </div>
            </div>
          </div>

          <a-spin :spinning="uiState.emptyHomeLoading">
            <div class="console-home-content">
              <div class="console-home-section">
                <div class="console-home-section-header">
                  <div>
                    <div class="console-home-section-title">快捷入口</div>
                  </div>
                </div>

                <a-tabs v-model:activeKey="uiState.emptyHomeTab" size="small" class="console-home-tabs">
                  <a-tab-pane key="recent">
                    <template #tab>
                      <span class="console-home-tab-label">
                        <TableOutlined />
                        最近访问
                      </span>
                    </template>
                    <div v-if="recentAccessCards.length" class="console-home-card-grid">
                      <div
                        v-for="item in recentAccessCards"
                        :key="`recent-${item.key}`"
                        class="console-home-card console-home-card--source"
                        :class="{ 'is-active': emptyHomeData.selectedInstanceId === item.instance_id }"
                        @click="selectEmptyHomeInstance(item.instance_id)"
                      >
                        <div class="console-home-card-title-row">
                          <div class="console-home-card-title" :title="item.title">{{ item.title }}</div>
                          <a-tag class="console-home-card-tag">{{ item.dbTypeLabel }}</a-tag>
                        </div>
                        <div class="console-home-card-meta" :title="item.meta">{{ item.meta }}</div>
                        <div class="console-home-card-submeta" :title="item.subMeta">{{ item.subMeta }}</div>
                        <div class="console-home-card-footer">
                          <span>{{ item.timeText }}</span>
                          <a-button
                            type="link"
                            size="small"
                            class="console-home-card-action"
                            @click.stop="startQueryFromEmptyHome(item.instance_id)"
                          >
                            开始
                          </a-button>
                        </div>
                      </div>
                    </div>
                    <a-empty
                      v-else
                      class="console-home-tab-empty"
                      :image="Empty.PRESENTED_IMAGE_SIMPLE"
                      description="暂无最近访问记录"
                    />
                  </a-tab-pane>

                  <a-tab-pane key="favorite">
                    <template #tab>
                      <span class="console-home-tab-label">
                        <StarOutlined />
                        我的收藏
                      </span>
                    </template>
                    <div v-if="favoriteCards.length" class="console-home-card-grid">
                      <div
                        v-for="item in favoriteCards"
                        :key="`favorite-${item.id}`"
                        class="console-home-card console-home-card--favorite"
                      >
                        <div class="console-home-card-title-row">
                          <div class="console-home-card-title">{{ item.title }}</div>
                          <a-tag v-if="item.updatedAtText" class="console-home-card-tag">{{ item.updatedAtText }}</a-tag>
                        </div>
                        <div class="console-home-card-sql">{{ item.sqlPreview }}</div>
                        <div class="console-home-card-footer">
                          <span>{{ item.subtitle }}</span>
                          <a-button type="link" size="small" class="console-home-card-action" @click="openFavoriteInNewWindow(item.raw)">
                            插入新窗口
                          </a-button>
                        </div>
                      </div>
                    </div>
                    <a-empty
                      v-else
                      class="console-home-tab-empty"
                      :image="Empty.PRESENTED_IMAGE_SIMPLE"
                      description="暂无收藏 SQL"
                    />
                  </a-tab-pane>
                </a-tabs>
              </div>
            </div>
          </a-spin>
        </div>
      </div>
    </div>
  </div>
  <ConsoleDbDict
    ref="dbDictRef"
    :open="uiState.isDbDictOpen"
    @update:open="uiState.isDbDictOpen = false"
  />
  <DasFavoriteFormModal
    :open="uiState.isFavoritesOpen"
    v-model:modelValue="favoritesFormState"
    @update:open="uiState.isFavoritesOpen = $event"
    @submit="handleFavoritesSubmit"
  />
  <CreateConsoleTabFormModal
    :open="uiState.isCreateTabModalOpen"
    v-model:modelValue="createTabFormState"
    :instances="uiData.instances"
    :bound-instance-ids="boundInstanceIdsForModal"
    :favorited-instance-ids="favoritedInstanceIdsForModal"
    :preset-hint="createTabModalHint"
    @update:open="uiState.isCreateTabModalOpen = $event"
    @submit="handleCreateConsoleTabSubmit"
  />
  <a-modal v-model:open="uiState.pasteAssistOpen" title="粘贴" width="640px" @cancel="closePasteAssist">
    <div style="color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65)); font-size: 12px; margin-bottom: 10px">
      当前环境为 HTTP，浏览器禁止页面直接读取系统剪贴板。请在下方输入框按 Ctrl/⌘+V 粘贴，然后点击“插入”。
    </div>
    <a-textarea
      ref="pasteAssistInputRef"
      v-model:value="uiState.pasteAssistValue"
      :auto-size="{ minRows: 6, maxRows: 12 }"
      placeholder="在这里 Ctrl/⌘+V 粘贴"
      @paste="handlePasteAssistPaste"
    />
    <template #footer>
      <a-space>
        <a-button @click="closePasteAssist">取消</a-button>
        <a-button type="primary" @click="confirmPasteAssistInsert">插入</a-button>
      </a-space>
    </template>
  </a-modal>
  <a-modal v-model:open="uiState.copyAssistOpen" title="复制" width="640px" @cancel="closeCopyAssist">
    <div style="color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65)); font-size: 12px; margin-bottom: 10px">
      当前环境可能限制页面直接写入系统剪贴板。请在下方输入框按 Ctrl/⌘+C 复制。
    </div>
    <a-textarea
      ref="copyAssistInputRef"
      v-model:value="uiState.copyAssistValue"
      :auto-size="{ minRows: 6, maxRows: 12 }"
      readonly
      placeholder="复制内容会显示在这里"
    />
    <template #footer>
      <a-space>
        <a-button @click="selectCopyAssistContent">全选</a-button>
        <a-button type="primary" @click="closeCopyAssist">关闭</a-button>
      </a-space>
    </template>
  </a-modal>
  <a-drawer
    v-model:open="uiState.isSqlLibraryDrawerOpen"
    :title="sqlLibraryDrawerTitle"
    placement="right"
    width="75vw"
    :body-style="{ padding: '12px 16px', overflow: 'hidden', display: 'flex', flexDirection: 'column', minHeight: 0, height: '100%' }"
    :root-class-name="uiState.isSqlLibraryFullscreen ? 'sql-library-drawer sql-library-drawer--fullscreen' : 'sql-library-drawer sql-library-drawer--normal'"
    :destroy-on-close="false"
  >
    <template #extra>
      <a-button type="link" size="small" @click="uiState.isSqlLibraryFullscreen = !uiState.isSqlLibraryFullscreen">
        {{ uiState.isSqlLibraryFullscreen ? '退出全屏' : '全屏' }}
      </a-button>
    </template>
    <DasFavoriteLibrary
      v-if="uiState.sqlLibraryDrawerType === 'favorite'"
      @insert-sql="insertSqlFromLibrary"
      @close="uiState.isSqlLibraryDrawerOpen = false"
    />
    <DasHistoryLibrary
      v-else-if="uiState.sqlLibraryDrawerType === 'history'"
      @insert-sql="insertSqlFromLibrary"
      @close="uiState.isSqlLibraryDrawerOpen = false"
    />
  </a-drawer>
</template>

<script setup>
import {
  CreateFavoritesApi,
  ExecuteClickHouseQueryApi,
  GetFavoritesApi,
  GetHistoryApi,
  ExecuteMySQLQueryApi,
  ExecuteOracleQueryApi,
  ExecutePostgresQueryApi,
  ExecuteSQLServerQueryApi,
  ExplainMySQLQueryApi,
  ExplainPostgresQueryApi,
  GetDBDictApi,
  GetInstancesApi,
  GetResourcesApi,
  GetSchemaTablesApi,
} from '@/api/das'
import AppTooltip from '@/components/patterns/AppTooltip.vue'
import CodeMirror from '@/components/edit/Codemirror.vue'
import { useUserStore } from '@/store/user'
import { idbDel, idbGet, idbSet } from '@/utils/idbKv'
import {
  FAMILY_CLICKHOUSE,
  FAMILY_MYSQL,
  FAMILY_ORACLE,
  FAMILY_POSTGRES,
  FAMILY_SQLSERVER,
  resolveDbFamily,
  resolveFamilyInContext,
  resolveInstanceDbFamily,
} from '@/utils/dbFamily'
import { getPureSchemaDisplayName } from '@/utils/schemaDisplay'
import { appendHexAlpha, buildEnvironmentTagStyle, getContrastTextColor, normalizeHexColor } from '@/utils/color'
import DasFavoriteFormModal from '@/views/das/favorite/DasFavoriteFormModal.vue'
import CreateConsoleTabFormModal from './CreateConsoleTabFormModal.vue'
import DasFavoriteLibrary from './DasFavoriteLibrary.vue'
import DasHistoryLibrary from './DasHistoryLibrary.vue'
import {
  CheckCircleOutlined,
  CloseOutlined,
  CloseCircleOutlined,
  BookOutlined,
  CodeOutlined,
  DownOutlined,
  EditOutlined,
  EllipsisOutlined,
  FolderOpenOutlined,
  InfoCircleOutlined,
  FileSearchOutlined,
  LeftOutlined,
  LoadingOutlined,
  PlayCircleOutlined,
  PlusOutlined,
  PushpinFilled,
  RightOutlined,
  StarOutlined,
  StopOutlined,
  TableOutlined,
  WarningOutlined,
  DatabaseFilled,
  EnvironmentOutlined,
  GlobalOutlined,
} from '@ant-design/icons-vue'
import { message, Empty } from 'ant-design-vue'
import { computed, inject, nextTick, onActivated, onBeforeUnmount, onMounted, provide, reactive, ref, watch } from 'vue'
import ConsoleDbDict from './ConsoleDbDict.vue'

const props = defineProps({
  editorHeight: {
    type: Number,
    default: 280,
  },
  showTopTabbar: {
    type: Boolean,
    default: true,
  },
})

const reportDraftDebug = () => {}

const userStore = useUserStore()
const getConsoleDraftStorage = () => {
  if (typeof window === 'undefined') {
    return null
  }
  return window.localStorage
}
const getStorageItemSafely = (key) => {
  try {
    return getConsoleDraftStorage()?.getItem(key) ?? null
  } catch {
    return null
  }
}
const setStorageItemSafely = (key, value) => {
  try {
    getConsoleDraftStorage()?.setItem(key, value)
    return true
  } catch {
    return false
  }
}
const removeStorageItemSafely = (key) => {
  try {
    getConsoleDraftStorage()?.removeItem(key)
  } catch (error) {
    void error
  }
}
const readPersistedUserStore = () => {
  try {
    const raw = window.localStorage.getItem('user-store')
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
}
const resolveDraftOwnerKey = () => {
  const persistedUser = readPersistedUserStore()
  const uid = String(userStore.uid || persistedUser?.uid || '').trim()
  if (uid) {
    return `uid:${encodeURIComponent(uid)}`
  }
  const username = String(userStore.username || persistedUser?.username || '').trim().toLowerCase()
  if (username) {
    return `username:${encodeURIComponent(username)}`
  }
  return 'anonymous'
}
const draftOwnerKey = resolveDraftOwnerKey()
const resolveDraftOwnerCandidates = () => {
  const persistedUser = readPersistedUserStore()
  const candidates = []
  const uid = String(userStore.uid || persistedUser?.uid || '').trim()
  if (uid) {
    candidates.push(`uid:${encodeURIComponent(uid)}`)
  }
  const username = String(userStore.username || persistedUser?.username || '').trim().toLowerCase()
  if (username) {
    candidates.push(`username:${encodeURIComponent(username)}`)
  }
  candidates.push(draftOwnerKey)
  return Array.from(new Set(candidates.filter(Boolean)))
}
const draftOwnerCandidates = resolveDraftOwnerCandidates()
const ACTION_CLICK_COOLDOWN_MS = 800
const ACTION_HINT_DURATION_MS = 2200

const runtime = reactive({
  queryResultMessageByTab: {},
  resultByTab: {},
  tableLoadingByTab: {},
  abortControllerByTab: {},
  instanceLoading: false,
  databaseLoading: false,
  schemaLoading: false,
})

const uiState = reactive({
  isDbDictOpen: false,
  isFavoritesOpen: false,
  isCreateTabModalOpen: false,
  emptyHomeLoading: false,
  emptyHomeTab: 'recent',
  pasteAssistOpen: false,
  pasteAssistValue: '',
  copyAssistOpen: false,
  copyAssistValue: '',
  isSqlLibraryDrawerOpen: false,
  sqlLibraryDrawerType: '',
  isSqlLibraryFullscreen: false,
  actionHint: '',
  actionHintTone: 'warning',
  actionCooldown: {
    execute: false,
    explain: false,
  },
  contextError: {
    type: '',
    category: '',
    title: '',
    detail: '',
    source: '',
  },
})

const clearContextError = () => {
  uiState.contextError.type = ''
  uiState.contextError.category = ''
  uiState.contextError.title = ''
  uiState.contextError.detail = ''
  uiState.contextError.source = ''
}
const setContextError = (payload = {}) => {
  uiState.contextError.type = payload.type || 'error'
  uiState.contextError.category = payload.category || 'generic'
  uiState.contextError.title = payload.title || ''
  uiState.contextError.detail = payload.detail || ''
  uiState.contextError.source = payload.source || ''
}

let actionHintTimer = null
const actionCooldownTimers = {
  execute: null,
  explain: null,
}
let editorAutosaveTimer = null
const flushEditorDraft = () => {
  if (editorAutosaveTimer) {
    clearTimeout(editorAutosaveTimer)
    editorAutosaveTimer = null
  }
  saveTabToCache()
}
const handleDraftVisibilityChange = () => {
  if (document.visibilityState === 'hidden') {
    flushEditorDraft()
  }
}

const consoleEditorCardRef = ref(null)
const tabScrollerRef = ref(null)
const renameInputRef = ref(null)
const dbDictRef = ref(null)
const codemirrorRef = ref(null)
const pasteAssistInputRef = ref(null)
const copyAssistInputRef = ref(null)
const favoritesFormState = ref({ sqltext: '' })
const createTabFormState = ref({ instance_id: '' })
const createTabModalHint = ref('')
const lastCreatedInstanceId = ref('')
const renamingTabKey = ref(null)
const renamingValue = ref('')
const draggingTabKey = ref(null)
const tabScrollState = reactive({
  canScrollLeft: false,
  canScrollRight: false,
})

const dasInstanceData = inject('dasInstanceData')
const dasConsoleAction = inject('dasConsoleAction')
const dasResultNotice = inject('dasResultNotice')
const dasFavoriteInstanceState = inject('dasFavoriteInstanceState', null)
const emit = defineEmits(['renderResultTable', 'renderExecutionMessage', 'activePaneChange'])

const uiData = reactive({
  instances: [],
  panes: [],
  activeKey: 1,
  nextTabKey: 2,
  nextConsoleIndex: 2,
  characterSet: 'utf8mb4',
  instance_id: '',
  instance_remark: '',
  db_type: '',
  database: '',
  schema_name: '',
  schema: '',
  resource_type: '',
  display_name: '',
  databaseOptions: [],
  schemaOptions: [],
  queryResultMessage: undefined,
  editorState: {
    content: '',
    selectedText: '',
    cursorPos: 0,
    hasSelection: false,
  },
})

const emptyHomeData = reactive({
  selectedInstanceId: undefined,
  historyRecords: [],
  favoriteRecords: [],
})

let ignoreNextSharedSync = false
const PANES_STORAGE_KEY = 'das#panes'
const ACTIVE_TAB_STORAGE_KEY = 'das#activeKey'
const TAB_CACHE_PREFIX = 'das#tab#'

const buildScopedStorageKey = (key, ownerKey = draftOwnerKey) => `${key}:${ownerKey}`
const getScopedStorageItem = (key) => {
  for (const ownerKey of draftOwnerCandidates) {
    const value = getStorageItemSafely(buildScopedStorageKey(key, ownerKey))
    // #region debug-point B:storage-read
    reportDraftDebug('B', 'ConsoleRight.vue:getScopedStorageItem', 'scoped storage read', {
      key,
      ownerKey,
      hit: value != null,
      valueLength: value?.length || 0,
    })
    // #endregion
    if (value != null) {
      if (ownerKey !== draftOwnerKey) {
        setStorageItemSafely(buildScopedStorageKey(key, draftOwnerKey), value)
      }
      return value
    }
  }
  return null
}
const setScopedStorageItem = (key, value) => {
  const storageKey = buildScopedStorageKey(key)
  const ok = setStorageItemSafely(storageKey, value)
  // #region debug-point B:storage-write
  reportDraftDebug('B', 'ConsoleRight.vue:setScopedStorageItem', 'scoped storage write', {
    key,
    storageKey,
    ok,
    valueLength: String(value || '').length,
  })
  // #endregion
  return ok
}
const removeScopedStorageItem = (key) => {
  draftOwnerCandidates.forEach((ownerKey) => {
    // #region debug-point B:storage-remove
    reportDraftDebug('B', 'ConsoleRight.vue:removeScopedStorageItem', 'scoped storage remove', {
      key,
      ownerKey,
    })
    // #endregion
    removeStorageItemSafely(buildScopedStorageKey(key, ownerKey))
  })
}
const buildDraftStorageKey = (key, ownerKey = draftOwnerKey) => `das:${ownerKey}:${key}`
const readMirroredDraft = async (key) => {
  for (const ownerKey of draftOwnerCandidates) {
    const raw = await idbGet(buildDraftStorageKey(key, ownerKey)).catch(() => null)
    if (raw != null) {
      if (ownerKey !== draftOwnerKey) {
        mirrorDraftSet(key, raw)
      }
      return raw
    }
  }
  return null
}
const mirrorDraftSet = (key, value) => {
  idbSet(buildDraftStorageKey(key), value).catch(() => {})
}
const mirrorDraftRemove = (key) => {
  draftOwnerCandidates.forEach((ownerKey) => {
    idbDel(buildDraftStorageKey(key, ownerKey)).catch(() => {})
  })
}

const hydrateConsoleCacheFromDrafts = async () => {
  const panesRaw = await readMirroredDraft(PANES_STORAGE_KEY)
  if (panesRaw && !getScopedStorageItem(PANES_STORAGE_KEY)) {
    setScopedStorageItem(PANES_STORAGE_KEY, panesRaw)
  }
  const activeRaw = await readMirroredDraft(ACTIVE_TAB_STORAGE_KEY)
  if (activeRaw && !getScopedStorageItem(ACTIVE_TAB_STORAGE_KEY)) {
    setScopedStorageItem(ACTIVE_TAB_STORAGE_KEY, activeRaw)
  }
  let panes = null
  try {
    panes = panesRaw ? JSON.parse(panesRaw) : null
  } catch {
    panes = null
  }
  if (!Array.isArray(panes)) {
    try {
      const fallbackRaw = getScopedStorageItem(PANES_STORAGE_KEY)
      panes = fallbackRaw ? JSON.parse(fallbackRaw) : null
    } catch {
      panes = null
    }
  }
  if (!Array.isArray(panes)) return

  await Promise.all(
    panes
      .map((pane) => Number(pane?.key))
      .filter((key) => Number.isFinite(key) && key > 0)
      .map(async (key) => {
        const tabStorageKey = `${TAB_CACHE_PREFIX}${key}`
        if (getScopedStorageItem(tabStorageKey)) return
        const raw = await readMirroredDraft(tabStorageKey)
        if (raw) {
          setScopedStorageItem(tabStorageKey, raw)
        }
      }),
  )
}

const normalizeDbTypeValue = (value) => String(value || '').trim().toLowerCase()
const normalizeSearchText = (value) => String(value || '').trim().toLowerCase()

/* 家族判定统一入口：入参一律是「引擎家族」（utils/dbFamily 的 FAMILY_* / resolveDbFamily 的返回值），
   而不是 db_type。家族值对 MySQL / PostgreSQL / Oracle / ClickHouse / SQLServer 恰好与
   历史 db_type 小写写法同名，因此下面几个判定的行为与改造前完全一致；
   OceanBase 由 resolveDbFamily 按 extra_config.oceanbase_mode 映射到 mysql 或 oracle。 */
const isMysqlFamily = (family) => family === FAMILY_MYSQL
const isPostgresFamily = (family) => family === FAMILY_POSTGRES
const isSqlServerFamily = (family) => family === FAMILY_SQLSERVER
const isOracleFamily = (family) => family === FAMILY_ORACLE
const isClickHouseFamily = (family) => family === FAMILY_CLICKHOUSE

const currentTabLoading = computed(() => !!runtime.tableLoadingByTab[uiData.activeKey])
// 最小高度 = 一行 SQL（与 ConsoleIndex MIN_EDITOR_HEIGHT 一致，见其注释）
const EDITOR_HEIGHT_FLOOR = 26
const editorHeightPx = computed(() => {
  const raw = Number(props.editorHeight)
  const safe = Number.isFinite(raw) && raw > 0 ? raw : 280
  return `${Math.max(EDITOR_HEIGHT_FLOOR, Math.min(2000, Math.floor(safe)))}px`
})
/* 当前实例的引擎家族：驱动全部「按家族渲染 / 派发」的判断（库/Schema 选择、DB字典、
   执行计划、执行端点、SQL 方言…）。
   优先用实例的 db_family —— 后端 /api/v1/das/instances 已按 extra_config 算好，
   前端不必也不该重复解析；实例缺失（未选中/缓存态）时按 db_type 本地兜底
   （此兜底对 OceanBase 只能落到 MySQL 模式，属已知降级，不影响已选中实例的正常路径）。 */
const currentDbFamily = computed(() => {
  const meta = currentInstanceMeta.value
  const fromInstance = meta ? resolveInstanceDbFamily(meta) : ''
  if (fromInstance) return fromInstance
  return resolveDbFamily(uiData.db_type)
})
const hasUserPermission = (permission) => {
  if (!permission) {
    return true
  }
  if (userStore.is_superuser) {
    return true
  }
  return (userStore.permissions || []).includes(permission)
}
const canOpenFavoriteLibrary = computed(
  () => hasUserPermission('sql.console.access') && hasUserPermission('sql.favorite.view'),
)
const canOpenHistoryLibrary = computed(
  () => hasUserPermission('sql.console.access') && hasUserPermission('sql.history.view'),
)
const sqlLibraryDrawerTitle = computed(() => {
  if (uiState.sqlLibraryDrawerType === 'favorite') {
    return 'SQL收藏库'
  }
  if (uiState.sqlLibraryDrawerType === 'history') {
    return 'SQL历史'
  }
  return '更多操作'
})
const isMysqlLike = computed(() => isMysqlFamily(currentDbFamily.value) || isClickHouseFamily(currentDbFamily.value))
const isPgLike = computed(() => isPostgresFamily(currentDbFamily.value) || isSqlServerFamily(currentDbFamily.value))
const canSelectDatabase = computed(() => !!uiData.instance_id && !isOracleFamily(currentDbFamily.value))
const showsSchemaSelector = computed(
  () => !!uiData.instance_id && (isPgLike.value || isOracleFamily(currentDbFamily.value)),
)
const canSelectSchema = computed(() => (isPgLike.value ? !!uiData.database : !!uiData.instance_id))
const canUseDbDict = computed(() => isMysqlFamily(currentDbFamily.value))
const canUseExplain = computed(
  () => isMysqlFamily(currentDbFamily.value) || isPostgresFamily(currentDbFamily.value),
)
const databaseLabel = computed(() => (isMysqlLike.value ? '库' : '数据库'))
const schemaLabel = computed(() => (isPgLike.value ? 'Schema' : 'Schema'))
const explainSupportHint = '执行计划解析仅支持 PostgreSQL 9.6+、MySQL 5.7+ 的单条 SELECT / WITH 查询'
const currentInstanceLabel = computed(() => formatInstanceLabel(getInstanceMetaById(uiData.instance_id)))
const instanceSelectOptions = computed(() => {
  return (uiData.instances || []).map((item) => {
    const remark = String(item?.remark || '').trim()
    const hostname = String(item?.hostname || '').trim()
    const port = String(item?.port || '').trim()
    const hostPort = hostname ? (port ? `${hostname}:${port}` : hostname) : ''
    const displayRemark = remark || hostPort
    const shortRemark = remark
    const ellipsisRemark = displayRemark.length > 15 ? displayRemark.slice(0, 15).trimEnd() + '...' : displayRemark
    const environmentName = String(item?.environment_name || item?.environmentName || '').trim()
    const environmentColor = normalizeHexColor(item?.environment_color || item?.environmentColor || '') || ''
    const displayLabel = ellipsisRemark || String(item?.instance_name || item?.name || '').trim() || String(item?.instance_id || '').slice(0, 15)
    const fullLabel = remark && hostPort ? `${remark} | ${hostPort}` : (remark || hostPort)
    return {
      value: item.instance_id,
      label: displayLabel,
      displayLabel,
      longLabel: fullLabel || displayLabel,
      shortLabel: displayLabel,
      ellipsisRemark,
      hostPort,
      fullRemark: remark,
      environmentName,
      environmentColor,
      instanceMeta: {
        db_type: item?.db_type,
        last_check_status: item?.last_check_status,
      },
      searchText: `${remark || ''} ${hostPort || ''} ${hostname || ''} ${item?.instance_id || ''} ${item?.db_type || ''} ${environmentName}`.toLowerCase(),
    }
  })
})

// 「实例」选择器：把当前实例的 host:port 通过 CSS 变量注入到已选中项下方（antd-vue 无 labelRender 插槽）
const formatInstanceAddress = (instance) => {
  if (!instance) return ''
  const hostname = String(instance?.hostname || '').trim()
  const port = String(instance?.port || '').trim()
  if (!hostname) return port ? `:${port}` : ''
  return port ? `${hostname}:${port}` : hostname
}
const selectedInstanceAddr = computed(() => formatInstanceAddress(getInstanceMetaById(uiData.instance_id)))
const emptyHomeSelectedInstanceAddr = computed(() =>
  formatInstanceAddress(getInstanceMetaById(emptyHomeData.selectedInstanceId)),
)

// 「实例名 ip:port」同行展示：antd-vue 单选不支持自定义已选项渲染，用覆盖层自绘（聚焦/搜索时让位给输入框）
const instanceSelectFocused = ref(false)
const homeInstanceSelectFocused = ref(false)

const findInstanceDisplayName = (instanceId) => {
  const option = (instanceSelectOptions.value || []).find((item) => item.value === instanceId)
  return option?.ellipsisRemark || option?.displayLabel || ''
}
const instanceDisplayName = computed(() => findInstanceDisplayName(uiData.instance_id))
const emptyHomeInstanceDisplayName = computed(() => findInstanceDisplayName(emptyHomeData.selectedInstanceId))

const showInstanceAddrDisplay = computed(() => !!selectedInstanceAddr.value && !instanceSelectFocused.value)
const showHomeInstanceAddrDisplay = computed(
  () => !!emptyHomeSelectedInstanceAddr.value && !homeInstanceSelectFocused.value,
)
const contextErrorDescription = computed(() => {
  const ce = uiState.contextError
  const detail = String(ce.detail || '').trim()
  if (detail) return detail
  if (!ce.title) return ''
  if (ce.category === 'connection') return '可能原因：实例未启动、主机/端口变更、网络 ACL 阻断或账号凭据过期，请管理员确认后重试。'
  if (ce.category === 'permission') return '请联系管理员为当前账号授予该实例/库/Schema 的访问权限。'
  return '请稍后重试，如持续失败请联系管理员查看服务端日志。'
})
const canRetryContextError = computed(() => {
  const ce = uiState.contextError
  return !!ce.title && ['database-change', 'schema-change', 'completion-load'].includes(String(ce.source || ''))
})
const canClearContextSelection = computed(() => {
  return !!uiState.contextError.title && (!!uiData.database || !!uiData.schema || !!uiData.instance_id)
})
let pendingContextRetryTask = null
const handleRetryContextError = () => {
  if (pendingContextRetryTask) {
    pendingContextRetryTask()
  }
}
const handleClearContextSelection = () => {
  clearContextError()
  clearContextSelection()
  saveTabToCache()
}

const boundInstanceIdsForModal = computed(() => {
  const ids = []
  const order = []
  for (const pane of uiData.panes || []) {
    const id = pane?.instance_id || ''
    if (!id) continue
    if (!ids.includes(id)) order.push(id)
    ids.push(id)
  }
  const active = activePane.value?.instance_id
  if (active && !order.includes(String(active))) order.unshift(String(active))
  return order
})

const favoritedInstanceIdsForModal = computed(() => {
  if (!dasFavoriteInstanceState) return []
  return Array.isArray(dasFavoriteInstanceState.ids) ? [...dasFavoriteInstanceState.ids] : []
})

const normalizeInstanceCheckStatus = (value) => String(value || '').trim().toLowerCase()
const currentInstanceMeta = computed(() => getInstanceMetaById(uiData.instance_id) || null)
const currentInstanceCheckStatus = computed(() => normalizeInstanceCheckStatus(currentInstanceMeta.value?.last_check_status))
const isCurrentInstanceUnavailable = computed(() => currentInstanceCheckStatus.value === 'error')

const clearResultNotice = () => {
  if (!dasResultNotice) return
  dasResultNotice.type = ''
  dasResultNotice.title = ''
  dasResultNotice.detail = ''
  dasResultNotice.instance_id = ''
}

const setInstanceUnavailableNotice = (instanceMeta) => {
  if (!dasResultNotice) return
  const detail = String(instanceMeta?.last_check_message || '').trim()
  dasResultNotice.type = 'error'
  dasResultNotice.title = '连接失败'
  dasResultNotice.detail = detail || '实例连接异常，无法执行 SQL。'
  dasResultNotice.instance_id = String(instanceMeta?.instance_id || uiData.instance_id || '').trim()
}
const currentDatabaseLabel = computed(() => getDatabaseOptionByValue(uiData.database)?.label || '')
const currentSchemaLabel = computed(() => getSchemaOptionByValue(uiData.schema)?.label || '')
const activePane = computed(() => uiData.panes.find((item) => item.key === uiData.activeKey) || null)
const hasActivePane = computed(() => !!activePane.value)
const hasEditorSelection = computed(() => {
  if (uiData.editorState.hasSelection) {
    return true
  }
  const selectedText = codemirrorRef.value?.getSelectedText?.()
  return typeof selectedText === 'string' && selectedText.length > 0
})

const shortHostText = (value) => {
  const text = String(value || '').trim()
  if (!text) return ''
  const parts = text.split('.').filter(Boolean)
  if (parts.length >= 2 && parts.every((item) => /^\d+$/.test(item))) {
    return parts.slice(-2).join('.')
  }
  return text.length > 12 ? text.slice(0, 12) : text
}

const normalizePaneClosable = (pane = {}, kind = 'console', key = 1) => {
  if (kind === 'console' && key === 1) {
    return true
  }
  return pane?.closable !== false
}

const normalizePane = (pane = {}, fallbackKey = 1) => {
  const key = Number(pane?.key) || fallbackKey
  const kind = pane?.kind === 'preview' ? 'preview' : 'console'
  return {
    key,
    kind,
    title: String(pane?.title || '').trim(),
    customTitle: String(pane?.customTitle || '').trim(),
    closable: normalizePaneClosable(pane, kind, key),
    pinned: !!pane?.pinned,
    consoleIndex: Number(pane?.consoleIndex) || key,
    instance_id: String(pane?.instance_id || '').trim(),
    instance_remark: String(pane?.instance_remark || '').trim(),
    db_type: String(pane?.db_type || '').trim(),
    database: String(pane?.database || '').trim(),
    schema: String(pane?.schema || '').trim(),
    schema_name: String(pane?.schema_name || '').trim(),
    display_name: String(pane?.display_name || '').trim(),
    environment_name: String(pane?.environment_name || '').trim(),
    environment_color: String(pane?.environment_color || '').trim(),
    hasSqlContent: !!pane?.hasSqlContent,
    connectionState: pane?.connectionState === 'disconnected' ? 'disconnected' : 'normal',
  }
}

const persistPanes = () => {
  const raw = JSON.stringify(uiData.panes)
  // #region debug-point B:persist-panes
  reportDraftDebug('B', 'ConsoleRight.vue:persistPanes', 'persist panes', {
    panesLength: uiData.panes.length,
    activeKey: uiData.activeKey,
    paneKeys: uiData.panes.map((pane) => pane.key),
    rawLength: raw.length,
  })
  // #endregion
  setScopedStorageItem(PANES_STORAGE_KEY, raw)
  mirrorDraftSet(PANES_STORAGE_KEY, raw)
}

const getPaneByKey = (key) => uiData.panes.find((item) => item.key === key)

const updatePane = (key, patch = {}, options = {}) => {
  const target = getPaneByKey(key)
  if (!target) return
  Object.assign(target, patch)
  if (options.persist !== false) {
    persistPanes()
  }
}

const reorderPinnedFirst = (panes = uiData.panes) => {
  const pinned = []
  const normal = []
  panes.forEach((pane) => {
    if (pane.pinned) {
      pinned.push(pane)
    } else {
      normal.push(pane)
    }
  })
  uiData.panes = [...pinned, ...normal]
  persistPanes()
}

function resolveInstanceEnvironmentFields(instanceMeta, fallback = {}) {
  return {
    environment_name: String(instanceMeta?.environment_name || fallback?.environment_name || '').trim(),
    environment_color: String(instanceMeta?.environment_color || fallback?.environment_color || '').trim(),
  }
}

const buildPaneContextPatch = (overrides = {}) => ({
  ...resolveInstanceEnvironmentFields(
    getInstanceMetaById(overrides.instance_id ?? uiData.instance_id),
    {
      ...activePane.value,
      ...overrides,
    },
  ),
  instance_id: overrides.instance_id ?? uiData.instance_id,
  instance_remark: overrides.instance_remark ?? uiData.instance_remark,
  db_type: overrides.db_type ?? uiData.db_type,
  database: overrides.database ?? uiData.database,
  schema: overrides.schema ?? uiData.schema,
  schema_name: overrides.schema_name ?? uiData.schema_name,
  display_name: overrides.display_name ?? uiData.display_name,
  hasSqlContent: Boolean(
    String(overrides.hasSqlContent ?? codemirrorRef.value?.getContent?.() ?? uiData.editorState.content ?? '').trim(),
  ),
  connectionState: overrides.connectionState ?? activePane.value?.connectionState ?? 'normal',
})

const syncActivePaneMeta = (overrides = {}, options = {}) => {
  if (!uiData.activeKey) return
  updatePane(uiData.activeKey, buildPaneContextPatch(overrides), options)
}

const getPaneInstanceMeta = (pane) => getInstanceMetaById(pane?.instance_id) || null
/* 标签页的「展示用数据库类型」：保留原始 db_type，用于图标与文字（OceanBase 图标靠它识别）。 */
const getPaneDbType = (pane) => {
  if (!pane) return 'mysql'
  const instanceMeta = getPaneInstanceMeta(pane)
  if (instanceMeta?.db_type) return normalizeDbTypeValue(instanceMeta.db_type)
  if (pane.db_type) return normalizeDbTypeValue(pane.db_type)
  const familyValue = normalizeDbTypeValue(uiData.db_type)
  if (familyValue === 'postgresql') return 'postgresql'
  if (['mysql', 'tidb', 'doris', 'oracle', 'sqlserver', 'clickhouse'].includes(familyValue)) return familyValue
  return 'mysql'
}
/* 标签页的「引擎家族」：给需要按家族分支的调用方（如 ConsoleIndex 生成 INSERT 时决定引号风格）。
   OceanBase 必须走这条路径 —— getPaneDbType 返回的 'oceanbase' 判不出 MySQL / Oracle 模式。 */
const getPaneFamily = (pane) => {
  if (!pane) return FAMILY_MYSQL
  const instanceMeta = getPaneInstanceMeta(pane)
  return (
    resolveInstanceDbFamily(instanceMeta) ||
    resolveFamilyInContext(pane.db_type || uiData.db_type, instanceMeta)
  )
}
const isPaneBoundToInstance = (pane) => {
  if (!pane) return false
  const instanceMeta = getPaneInstanceMeta(pane)
  if (instanceMeta && String(instanceMeta.instance_id || '').trim()) return true
  if (String(pane.instance_id || '').trim()) return true
  return false
}

const buildPaneInstanceShort = (pane) => {
  const instanceMeta = getPaneInstanceMeta(pane)
  if (instanceMeta?.hostname) {
    return shortHostText(instanceMeta.hostname)
  }
  return shortHostText(pane?.instance_remark || '')
}

const countDisplayWidth = (value) => {
  const s = String(value || '')
  let w = 0
  for (let i = 0; i < s.length; i++) {
    const code = s.charCodeAt(i)
    if (code < 0x007F) {
      w += 1
    } else if (code < 0x0300) {
      w += 1
    } else if (code >= 0xAC00 && code <= 0xD7AF) {
      w += 2
    } else if (code >= 0x1100 && code <= 0x115F) {
      w += 2
    } else if (code >= 0x2E80 && code <= 0x9FFF) {
      w += 2
    } else if (code >= 0x3000 && code <= 0x303F) {
      w += 2
    } else if (code >= 0xFF00 && code <= 0xFF60) {
      w += 2
    } else if (code >= 0xFFE0 && code <= 0xFFE6) {
      w += 2
    } else {
      w += 1
    }
  }
  return w
}

const INSTANCE_LABEL_MAX_WIDTH = 20
const UNBOUND_TAB_TITLE = '未选择实例'

const truncateInstanceLabel = (value, maxWidth = INSTANCE_LABEL_MAX_WIDTH) => {
  const s = String(value || '').trim()
  if (!s) return ''
  const total = countDisplayWidth(s)
  if (total <= maxWidth) return s
  let acc = 0
  let end = 0
  for (let i = 0; i < s.length; i++) {
    const chW = countDisplayWidth(s.charAt(i))
    if (acc + chW > maxWidth - 1) break
    acc += chW
    end = i + 1
  }
  if (end <= 0) end = 1
  return `${s.slice(0, end)}…`
}

const buildPreviewPaneTitle = (value) => {
  const raw = String(value || '').trim()
  if (!raw) {
    return '数据查看'
  }
  if (raw === '数据查看' || raw.startsWith('数据查看 ')) {
    return raw
  }
  return `数据查看 ${raw}`
}

const buildPaneTitle = (pane) => {
  if (!pane) return ''
  if (pane.customTitle) {
    return pane.customTitle
  }
  if (pane.kind === 'preview') {
    return buildPreviewPaneTitle(pane.title)
  }
  const instanceMeta = getPaneInstanceMeta(pane)
  const instanceName = truncateInstanceLabel(
    instanceMeta?.remark || pane.instance_remark || instanceMeta?.display_name || instanceMeta?.name || '',
  )
  if (!instanceName) {
    return UNBOUND_TAB_TITLE
  }
  return instanceName
}

const getPaneStatusText = (pane) => {
  switch (getPaneStatus(pane)) {
    case 'connected':
      return '已连接'
    case 'running':
      return '执行中'
    case 'disconnected':
      return '连接失败'
    case 'error':
      return '状态异常'
    case 'dirty':
      return '有未保存变更'
    default:
      return '待连接'
  }
}

const buildPaneTooltipVm = (pane) => {
  if (!pane) return null
  const instanceMeta = getPaneInstanceMeta(pane)
  return {
    instanceName: instanceMeta?.remark || pane.instance_remark || instanceMeta?.display_name || instanceMeta?.name || UNBOUND_TAB_TITLE,
    host: instanceMeta?.hostname || pane?.instance_host || '',
    port: instanceMeta?.port || pane?.instance_port || '',
    bound: isPaneBoundToInstance(pane),
  }
}

const buildPaneTooltipTitle = (pane) => {
  if (!pane) return ''
  const info = buildPaneTooltipVm(pane)
  const hostPart = info.host ? `${info.host}${info.port ? `:${info.port}` : ''}` : ''
  return [info.instanceName, hostPart].filter(Boolean).join(' | ')
}


const getPaneEnvironmentMeta = (pane) => {
  const instanceMeta = getPaneInstanceMeta(pane)
  const env = resolveInstanceEnvironmentFields(instanceMeta, pane)
  return {
    name: env.environment_name,
    color: normalizeHexColor(env.environment_color),
  }
}

const getPaneEnvironmentStyle = (pane) => {
  if (!pane) {
    return null
  }
  const env = getPaneEnvironmentMeta(pane)
  if (!env.color) {
    return null
  }
  return {
    '--console-tab-env-color': env.color,
    '--console-tab-env-soft-bg': appendHexAlpha(env.color, '14'),
    '--console-tab-env-soft-border': appendHexAlpha(env.color, '40'),
    '--console-tab-env-text': env.color,
    // 非激活 tab 的弱化文字色（≈72% 透明），对应无环境时 rgba(15,23,42,0.70) 的层级
    '--console-tab-env-text-soft': appendHexAlpha(env.color, 'B8'),
    '--console-tab-env-active-text': getContrastTextColor(env.color),
    '--console-tab-env-active-shadow': appendHexAlpha(env.color, '66'),
  }
}

const getPaneStatus = (pane) => {
  if (!pane) return 'idle'
  if (pane.connectionState === 'disconnected') return 'disconnected'
  if (runtime.tableLoadingByTab[pane.key]) return 'running'
  if (pane.hasSqlContent) return 'dirty'
  return 'idle'
}

const canClosePane = (pane) => !!pane && pane.closable !== false

const visiblePanes = computed(() => uiData.panes)

const defaultConsolePane = (key = 1) =>
  normalizePane({
    key,
    kind: 'console',
    consoleIndex: key,
    title: `窗口${key}`,
    closable: true,
  }, key)

const createConsolePane = (options = {}) =>
  normalizePane({
    key: options.key,
    kind: 'console',
    consoleIndex: options.consoleIndex,
    customTitle: options.customTitle || '',
    closable: options.closable !== false,
    pinned: !!options.pinned,
    instance_id: options.instance_id,
    instance_remark: options.instance_remark,
    db_type: options.db_type,
    database: options.database,
    schema: options.schema,
    schema_name: options.schema_name,
    display_name: options.display_name,
    environment_name: options.environment_name,
    environment_color: options.environment_color,
    hasSqlContent: !!options.hasSqlContent,
  }, options.key)

const createPreviewPane = (options = {}) =>
  normalizePane({
    key: options.key,
    kind: 'preview',
    title: options.title || '数据查看',
    customTitle: options.customTitle || '',
    closable: true,
    instance_id: options.instance_id,
    instance_remark: options.instance_remark,
    db_type: options.db_type,
    database: options.database,
    schema: options.schema,
    schema_name: options.schema_name,
    display_name: options.display_name,
    environment_name: options.environment_name,
    environment_color: options.environment_color,
    hasSqlContent: !!options.hasSqlContent,
  }, options.key)

const createTabCachePayload = (payload = {}) => ({
  userInput: String(payload.userInput || ''),
  instance_id: payload.instance_id || '',
  schema: payload.schema || '',
  resource_type: payload.resource_type || '',
  database: payload.database || '',
  schema_name: payload.schema_name || '',
  db_type: payload.db_type || '',
  instance_remark: payload.instance_remark || '',
  display_name: payload.display_name || '',
  environment_name: payload.environment_name || '',
  environment_color: payload.environment_color || '',
  result: payload.result,
  queryResultMessage: payload.queryResultMessage,
})

const writeTabCache = (tabKey, payload = {}) => {
  const raw = JSON.stringify(createTabCachePayload(payload))
  const tabStorageKey = `${TAB_CACHE_PREFIX}${tabKey}`
  setScopedStorageItem(tabStorageKey, raw)
  mirrorDraftSet(tabStorageKey, raw)
}

const ensureActivePaneVisible = () => {
  nextTick(() => {
    const container = tabScrollerRef.value
    if (!container) return
    const activeNode = container.querySelector(`[data-tab-key="${uiData.activeKey}"]`)
    if (!(activeNode instanceof HTMLElement)) return
    const left = activeNode.offsetLeft
    const right = left + activeNode.offsetWidth
    if (left < container.scrollLeft) {
      container.scrollTo({ left: left - 16, behavior: 'smooth' })
    } else if (right > container.scrollLeft + container.clientWidth) {
      container.scrollTo({ left: right - container.clientWidth + 16, behavior: 'smooth' })
    }
    updateTabScrollState()
  })
}

const updateTabScrollState = () => {
  const container = tabScrollerRef.value
  if (!container) {
    tabScrollState.canScrollLeft = false
    tabScrollState.canScrollRight = false
    return
  }
  tabScrollState.canScrollLeft = container.scrollLeft > 4
  tabScrollState.canScrollRight = container.scrollLeft + container.clientWidth < container.scrollWidth - 4
}

const scrollTabsBy = (offset) => {
  tabScrollerRef.value?.scrollBy?.({ left: offset, behavior: 'smooth' })
  nextTick(() => updateTabScrollState())
}

const handleTabWheel = (event) => {
  const container = tabScrollerRef.value
  if (!container) return
  const delta = Math.abs(event.deltaY) > Math.abs(event.deltaX) ? event.deltaY : event.deltaX
  container.scrollLeft += delta
  updateTabScrollState()
}

const bindTabScrollerRef = (el) => {
  tabScrollerRef.value = el || null
  nextTick(() => updateTabScrollState())
}

const bindRenameInputRef = (el) => {
  renameInputRef.value = el || null
}

const startRenamePane = (pane) => {
  if (!pane) return
  renamingTabKey.value = pane.key
  renamingValue.value = pane.customTitle || buildPaneTitle(pane)
  nextTick(() => {
    const input = renameInputRef.value?.input || renameInputRef.value
    input?.focus?.()
    input?.select?.()
  })
}

const commitRenamePane = () => {
  if (!renamingTabKey.value) return
  const value = String(renamingValue.value || '').trim()
  updatePane(renamingTabKey.value, {
    customTitle: value,
  })
  renamingTabKey.value = null
  renamingValue.value = ''
}

const cancelRenamePane = () => {
  renamingTabKey.value = null
  renamingValue.value = ''
}

const buildConnectionString = (pane) => {
  if (!pane) return ''
  const instanceMeta = getPaneInstanceMeta(pane)
  const instanceLabel = formatInstanceLabel(instanceMeta || {
    remark: pane.instance_remark,
    hostname: instanceMeta?.hostname,
    port: instanceMeta?.port,
  })
  const segments = [instanceLabel]
  if (pane.database) {
    segments.push(`database=${pane.database}`)
  }
  if (pane.schema_name || pane.display_name) {
    segments.push(`schema=${pane.schema_name || pane.display_name}`)
  }
  return segments.filter(Boolean).join(' | ')
}

const tryCopyTextByExecCommand = (text) => {
  if (typeof document === 'undefined') {
    return false
  }
  if (typeof document.execCommand !== 'function') {
    return false
  }
  const textarea = document.createElement('textarea')
  textarea.value = String(text || '')
  textarea.setAttribute('readonly', 'true')
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  textarea.style.left = '-9999px'
  textarea.style.top = '0'
  textarea.style.pointerEvents = 'none'
  document.body.appendChild(textarea)
  textarea.focus()
  textarea.select()
  let ok = false
  try {
    ok = !!document.execCommand('copy')
  } catch {
    ok = false
  }
  document.body.removeChild(textarea)
  return ok
}

const copyText = async (text) => {
  if (!text) return false
  if (navigator?.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch (error) {
      void error
    }
  }
  return tryCopyTextByExecCommand(text)
}

const tryReadClipboardTextByExecCommand = () => {
  if (typeof document === 'undefined') {
    return ''
  }
  if (typeof document.execCommand !== 'function') {
    return ''
  }
  const textarea = document.createElement('textarea')
  textarea.value = ''
  textarea.setAttribute('readonly', 'true')
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  textarea.style.left = '-9999px'
  textarea.style.top = '0'
  textarea.style.pointerEvents = 'none'
  document.body.appendChild(textarea)
  textarea.focus()
  textarea.select()
  try {
    document.execCommand('paste')
  } catch (error) {
    void error
  }
  const text = textarea.value
  document.body.removeChild(textarea)
  return String(text || '')
}

const tryPasteByExecCommand = () => {
  if (typeof document === 'undefined') return false
  if (typeof document.execCommand !== 'function') return false
  try {
    codemirrorRef.value?.focus?.()
    let ok = false
    try {
      ok = !!document.execCommand('paste')
    } catch {
      ok = false
    }
    if (ok) {
      return true
    }
    const el = document.createElement('div')
    el.setAttribute('contenteditable', 'true')
    el.style.position = 'fixed'
    el.style.top = '-9999px'
    el.style.left = '0'
    el.style.width = '1px'
    el.style.height = '1px'
    el.style.padding = '0'
    el.style.margin = '0'
    el.style.border = '0'
    el.style.outline = '0'
    el.style.opacity = '0'
    el.style.overflow = 'hidden'
    el.style.pointerEvents = 'none'
    el.style.whiteSpace = 'pre'
    el.setAttribute('autocomplete', 'off')
    el.setAttribute('autocorrect', 'off')
    el.setAttribute('autocapitalize', 'off')
    el.setAttribute('spellcheck', 'false')
    el.textContent = ''
    document.body.appendChild(el)
    try {
      const range = document.createRange()
      range.selectNodeContents(el)
      const sel = window.getSelection?.()
      sel?.removeAllRanges?.()
      sel?.addRange?.(range)
      el.focus?.()
      const ok2 = document.execCommand('paste')
      const text = el.innerText || el.textContent || ''
      if (ok2 && text) {
        codemirrorRef.value?.focus?.()
        codemirrorRef.value?.insertTextAtCursor?.(text)
        return true
      }
      return false
    } finally {
      try {
        const sel = window.getSelection?.()
        sel?.removeAllRanges?.()
      } catch {
      }
      document.body.removeChild(el)
    }
  } catch {
    return false
  }
}

const __pasteMousedownDone = { value: false }

const refocusEditorForPaste = () => {
  const doFocus = () => codemirrorRef.value?.focus?.()
  try { doFocus() } catch {}
  nextTick().then(() => { try { doFocus() } catch {} }).catch(() => {})
  setTimeout(() => { try { doFocus() } catch {} }, 0)
  setTimeout(() => { try { doFocus() } catch {} }, 50)
}

const onPasteItemMouseDown = () => {
  __pasteMousedownDone.value = false
  if (!hasActivePane.value) return
  if (window.isSecureContext && navigator?.clipboard?.readText) {
    return
  }
  const pasted = tryPasteByExecCommand()
  if (pasted) {
    __pasteMousedownDone.value = true
    try { saveTabToCache() } catch {}
  } else {
    refocusEditorForPaste()
  }
}

const pasteClipboardToEditor = async () => {
  if (!hasActivePane.value) return
  try {
    if (window.isSecureContext && navigator?.clipboard?.readText) {
      try {
        const text = await navigator.clipboard.readText()
        if (text) {
          refocusEditorForPaste()
          nextTick().then(() => {
            try { codemirrorRef.value?.insertTextAtCursor?.(text) } catch {}
            try { saveTabToCache() } catch {}
          }).catch(() => {})
          return
        }
      } catch {
      }
    }
    if (__pasteMousedownDone.value) {
      __pasteMousedownDone.value = false
      return
    }
    const pasted = tryPasteByExecCommand()
    if (pasted) {
      await nextTick()
      saveTabToCache()
      return
    }
    refocusEditorForPaste()
  } catch {
    try { refocusEditorForPaste() } catch {}
  }
}

const readClipboardText = async () => {
  if (navigator?.clipboard?.readText) {
    return await navigator.clipboard.readText()
  }
  const legacy = tryReadClipboardTextByExecCommand()
  if (legacy) {
    return legacy
  }
  throw new Error('浏览器限制无法读取系统剪贴板')
}

const openPasteAssist = () => {
  uiState.pasteAssistValue = ''
  uiState.pasteAssistOpen = true
  nextTick(() => {
    const el = pasteAssistInputRef.value?.resizableTextArea?.textArea || pasteAssistInputRef.value
    el?.focus?.()
  })
}

const closePasteAssist = () => {
  uiState.pasteAssistOpen = false
  uiState.pasteAssistValue = ''
}

const openCopyAssist = (text) => {
  uiState.copyAssistValue = String(text || '')
  uiState.copyAssistOpen = true
  nextTick(() => {
    selectCopyAssistContent()
  })
}

const selectCopyAssistContent = () => {
  const el = copyAssistInputRef.value?.resizableTextArea?.textArea || copyAssistInputRef.value
  el?.focus?.()
  el?.select?.()
}

const closeCopyAssist = () => {
  uiState.copyAssistOpen = false
  uiState.copyAssistValue = ''
}

const insertTextToEditor = async (text) => {
  const value = String(text || '')
  if (!value) return false
  codemirrorRef.value?.focus?.()
  codemirrorRef.value?.insertTextAtCursor?.(value)
  await nextTick()
  clearActionHint()
  saveTabToCache()
  return true
}

const confirmPasteAssistInsert = async () => {
  const ok = await insertTextToEditor(uiState.pasteAssistValue)
  if (!ok) {
    showActionHint('请先粘贴内容', 'warning')
    return
  }
  closePasteAssist()
  showActionHint('已插入', 'success')
}

const handlePasteAssistPaste = async (event) => {
  const pasted = event?.clipboardData?.getData?.('text/plain') || ''
  if (!pasted) {
    return
  }
  event.preventDefault()
  uiState.pasteAssistValue = pasted
  await confirmPasteAssistInsert()
}

const getEditorContent = () => codemirrorRef.value?.getContent?.() || uiData.editorState.content || ''

const getEditorSelectedText = () => {
  const selectedText = codemirrorRef.value?.getSelectedText?.()
  if (typeof selectedText === 'string' && selectedText.length) {
    return selectedText
  }
  return uiData.editorState.selectedText || ''
}

const getEditorContextSqlText = () => (hasEditorSelection.value ? getEditorSelectedText() : getEditorContent())

const canUseEditorContextAction = (actionKey) => {
  if (!hasActivePane.value) {
    return false
  }
  if (actionKey === 'execute' || actionKey === 'favorite') {
    return !currentTabLoading.value && !uiState.actionCooldown.execute
  }
  if (actionKey === 'favorite-library') {
    return canOpenFavoriteLibrary.value
  }
  if (actionKey === 'history-library') {
    return canOpenHistoryLibrary.value
  }
  if (actionKey === 'format') {
    return true
  }
  if (actionKey === 'explain') {
    return !currentTabLoading.value && canUseExplain.value && !uiState.actionCooldown.explain
  }
  if (actionKey === 'cut' || actionKey === 'copy') {
    return hasEditorSelection.value
  }
  if (actionKey === 'paste' || actionKey === 'select-all') {
    return true
  }
  return false
}

const closeTabsByKeys = (keys = []) => {
  const closeSet = new Set(keys.map((item) => Number(item)))
  if (!closeSet.size) return
  closeSet.forEach((key) => {
    abortTabExecution(key)
    delete runtime.queryResultMessageByTab[key]
    delete runtime.resultByTab[key]
    delete runtime.tableLoadingByTab[key]
    const tabStorageKey = `${TAB_CACHE_PREFIX}${key}`
    removeScopedStorageItem(tabStorageKey)
    mirrorDraftRemove(tabStorageKey)
  })
  const remaining = uiData.panes.filter((pane) => !closeSet.has(pane.key))
  uiData.panes = remaining
  if (!uiData.panes.length) {
    uiData.activeKey = 0
    removeScopedStorageItem(ACTIVE_TAB_STORAGE_KEY)
    mirrorDraftRemove(ACTIVE_TAB_STORAGE_KEY)
    codemirrorRef.value?.setContent?.('')
    resetContextState()
    applyEditorCompletion({})
    uiData.queryResultMessage = ''
    syncSharedContext({})
    emit('renderResultTable', null)
    emit('renderExecutionMessage', '')
    persistPanes()
    ensureActivePaneVisible()
    return
  }
  if (!uiData.panes.some((pane) => pane.key === uiData.activeKey)) {
    uiData.activeKey = uiData.panes[0]?.key || 0
  }
  persistPanes()
  loadTabFromCache()
  restoreContextByState()
  applyActiveTabRuntime()
  ensureActivePaneVisible()
}

const closeOtherTabs = (tabKey = uiData.activeKey) => {
  closeTabsByKeys(uiData.panes.filter((pane) => pane.key !== tabKey).map((pane) => pane.key))
}

const closeRightTabs = (tabKey = uiData.activeKey) => {
  const index = uiData.panes.findIndex((pane) => pane.key === tabKey)
  if (index < 0) return
  closeTabsByKeys(uiData.panes.slice(index + 1).map((pane) => pane.key))
}

const togglePinPane = (tabKey = uiData.activeKey, nextPinned) => {
  const pane = getPaneByKey(tabKey)
  if (!pane || pane.kind !== 'console') return
  pane.pinned = typeof nextPinned === 'boolean' ? nextPinned : !pane.pinned
  reorderPinnedFirst()
  ensureActivePaneVisible()
}

const restoreDefaultLayout = () => {
  uiData.panes = [...uiData.panes]
    .sort((left, right) => left.key - right.key)
    .map((pane) => ({
      ...pane,
      pinned: false,
    }))
  persistPanes()
  ensureActivePaneVisible()
}

const handleGlobalTabAction = ({ key }) => {
  if (key === 'close-all') {
    closeTabsByKeys(uiData.panes.map((pane) => pane.key))
    return
  }
  if (key === 'close-others') {
    closeOtherTabs()
    return
  }
  if (key === 'pin-current') {
    togglePinPane(uiData.activeKey, true)
    return
  }
  if (key === 'unpin-current') {
    togglePinPane(uiData.activeKey, false)
    return
  }
  if (key === 'restore-layout') {
    restoreDefaultLayout()
  }
}

const handlePaneContextMenuAction = async (actionKey, pane) => {
  if (!pane) return
  if (actionKey === 'rename') {
    startRenamePane(pane)
    return
  }
  if (actionKey === 'pin') {
    togglePinPane(pane.key, true)
    return
  }
  if (actionKey === 'unpin') {
    togglePinPane(pane.key, false)
    return
  }
  if (actionKey === 'close') {
    removeTab(pane.key)
    return
  }
  if (actionKey === 'close-others') {
    closeOtherTabs(pane.key)
    return
  }
  if (actionKey === 'close-right') {
    closeRightTabs(pane.key)
    return
  }
  if (actionKey === 'copy-connection') {
    const copied = await copyText(buildConnectionString(pane))
    message[copied ? 'success' : 'warning'](copied ? 'SQL窗口连接串已复制' : '复制失败，请重试')
  }
}

const createConsoleTabPayloadFromCurrent = () => ({
  userInput: '',
  instance_id: uiData.instance_id,
  schema: uiData.schema,
  resource_type: uiData.resource_type,
  database: uiData.database,
  schema_name: uiData.schema_name,
  db_type: uiData.db_type,
  instance_remark: uiData.instance_remark,
  display_name: uiData.display_name,
  environment_name: activePane.value?.environment_name || '',
  environment_color: activePane.value?.environment_color || '',
})

const createConsoleTabPayloadFromInstance = (instance) => ({
  userInput: '',
  instance_id: instance?.instance_id || '',
  schema: '',
  resource_type: '',
  database: '',
  schema_name: '',
  db_type: instance?.db_type || '',
  instance_remark: instance?.remark || '',
  display_name: '',
  environment_name: instance?.environment_name || '',
  environment_color: instance?.environment_color || '',
})

const createBlankConsoleTabPayload = () => ({
  userInput: '',
  instance_id: '',
  schema: '',
  resource_type: '',
  database: '',
  schema_name: '',
  db_type: '',
  instance_remark: '',
  display_name: '',
  environment_name: '',
  environment_color: '',
  result: null,
  queryResultMessage: '',
})

const addTab = (options = {}) => {
  const tabKey = Number(options.key) || uiData.nextTabKey
  uiData.nextTabKey = Math.max(uiData.nextTabKey, tabKey + 1)
  const consoleIndex = options.kind === 'preview'
    ? undefined
    : Number(options.consoleIndex) || uiData.nextConsoleIndex
  if (options.kind !== 'preview') {
    uiData.nextConsoleIndex = Math.max(uiData.nextConsoleIndex, Number(consoleIndex) + 1)
  }
  const pane = options.kind === 'preview'
    ? createPreviewPane({ key: tabKey, ...options })
    : createConsolePane({ key: tabKey, consoleIndex, ...options })
  uiData.panes.push(pane)
  reorderPinnedFirst()
  if (options.prefill) {
    writeTabCache(tabKey, options.prefill)
  }
  uiData.activeKey = tabKey
  loadTabFromCache()
  applyActiveTabRuntime()
  ensureActivePaneVisible()
  return tabKey
}

const addConsoleTabFromCurrentContext = () => {
  saveTabToCache()
  addTab({
    kind: 'console',
    prefill: createConsoleTabPayloadFromCurrent(),
  })
}

const openCreateConsoleTabModal = (mode = 'blank', preset = {}) => {
  let defaultInstanceId = ''
  let hint = ''
  if (mode === 'preset') {
    defaultInstanceId = String(preset?.instance_id || '')
    if (preset?.fromCurrent) {
      hint = '已预填当前 SQL 窗口上下文，可直接确定'
    } else if (defaultInstanceId) {
      const meta = getInstanceMetaById(defaultInstanceId)
      if (meta) {
        hint = `已预填数据源：${formatInstanceShortLabel(meta)}`
      }
    }
  } else if (mode === 'blank') {
    defaultInstanceId = lastCreatedInstanceId.value || ''
    if (!defaultInstanceId) {
      try {
        const raw = typeof window !== 'undefined' ? window.localStorage.getItem('das.console.lastCreatedInstance') : ''
        if (raw) defaultInstanceId = raw
      } catch {
        // ignore
      }
    }
  }
  createTabFormState.value = { instance_id: defaultInstanceId || '' }
  createTabModalHint.value = hint
  uiState.isCreateTabModalOpen = true
}

const addBlankConsoleTab = () => {
  openCreateConsoleTabModal('blank')
}

const handleCreateConsoleTabSubmit = async (payload) => {
  const instanceId = String(payload?.instance_id || '').trim()
  if (!instanceId) {
    showActionHint('请先选择数据源', 'warning')
    return
  }
  try {
    lastCreatedInstanceId.value = instanceId
    if (typeof window !== 'undefined') {
      try {
        window.localStorage.setItem('das.console.lastCreatedInstance', instanceId)
      } catch {
        // ignore
      }
    }
    uiState.isCreateTabModalOpen = false
    await createConsoleTabForInstance(instanceId)
  } catch (err) {
    console.error(err)
  }
}

const startQueryFromEmptyHome = async (instanceId = emptyHomeData.selectedInstanceId) => {
  const targetInstanceId = String(instanceId || '').trim()
  if (!targetInstanceId) {
    showActionHint('请先选择数据源', 'warning')
    return
  }
  const instanceMeta = getInstanceMetaById(targetInstanceId)
  if (!instanceMeta) {
    showActionHint('未找到对应数据源，请刷新后重试', 'warning')
    return
  }
  if (normalizeInstanceCheckStatus(instanceMeta.last_check_status) === 'error') {
    showActionHint(instanceMeta.last_check_message || '当前数据源不可用', 'warning')
    return
  }
  emptyHomeData.selectedInstanceId = targetInstanceId
  await createConsoleTabForInstance(targetInstanceId)
}

const createConsoleTabForInstance = async (instanceId) => {
  const instanceMeta = getInstanceMetaById(instanceId)
  saveTabToCache()
  addTab({
    kind: 'console',
    prefill: createConsoleTabPayloadFromInstance(instanceMeta),
    instance_id: instanceMeta?.instance_id,
    instance_remark: instanceMeta?.remark,
    db_type: instanceMeta?.db_type,
  })
  if (instanceMeta?.instance_id) {
    await handleInstanceChange(instanceMeta.instance_id)
  }
}

const handleCreateTabMenuAction = async ({ key }) => {
  if (key === 'current') {
    openCreateConsoleTabModal('preset', {
      instance_id: uiData.instance_id,
      database: uiData.database,
      schema: uiData.schema,
      fromCurrent: true,
    })
    return
  }
  if (String(key).startsWith('instance:')) {
    const instanceId = String(key).slice(9)
    openCreateConsoleTabModal('preset', { instance_id: instanceId })
  }
}

const handleTabDragStart = (pane, event) => {
  if (!pane || pane.kind !== 'console') return
  draggingTabKey.value = pane.key
  event.dataTransfer?.setData('text/plain', String(pane.key))
  event.dataTransfer.effectAllowed = 'move'
}

const handleTabDragOver = (pane, event) => {
  if (!draggingTabKey.value || !pane) return
  const draggingPane = getPaneByKey(draggingTabKey.value)
  if (!draggingPane || draggingPane.pinned !== pane.pinned) {
    event.dataTransfer.dropEffect = 'none'
    return
  }
  event.dataTransfer.dropEffect = 'move'
}

const handleTabDrop = (pane, event) => {
  const sourceKey = Number(event.dataTransfer?.getData('text/plain') || draggingTabKey.value)
  const sourcePane = getPaneByKey(sourceKey)
  if (!sourcePane || !pane || sourcePane.key === pane.key || sourcePane.pinned !== pane.pinned) {
    draggingTabKey.value = null
    return
  }
  const panes = [...uiData.panes]
  const fromIndex = panes.findIndex((item) => item.key === sourcePane.key)
  const toIndex = panes.findIndex((item) => item.key === pane.key)
  if (fromIndex < 0 || toIndex < 0) {
    draggingTabKey.value = null
    return
  }
  const [moved] = panes.splice(fromIndex, 1)
  panes.splice(toIndex, 0, moved)
  uiData.panes = panes
  persistPanes()
  draggingTabKey.value = null
  ensureActivePaneVisible()
}

const handleTabDragEnd = () => {
  draggingTabKey.value = null
}

const stripContextSelectNativeTitles = () => {
  if (typeof document === 'undefined') return
  const selectors = [
    '.context-select [title]',
    '.context-select-dropdown [title]',
  ]
  document.querySelectorAll(selectors.join(', ')).forEach((node) => {
    node.removeAttribute('title')
  })
}

const scheduleStripContextSelectNativeTitles = () => {
  nextTick(() => {
    stripContextSelectNativeTitles()
  })
}

const getInstanceMetaById = (instanceId) =>
  uiData.instances.find((item) => String(item.instance_id) === String(instanceId))

const formatInstanceLabel = (instance) => {
  if (!instance) return ''
  const remark = String(instance?.remark || '').trim()
  const hostname = String(instance?.hostname || '').trim()
  const port = String(instance?.port || '').trim()
  const hostPort = hostname ? `${hostname}${port ? `:${port}` : ''}` : ''
  if (!remark) return hostPort
  if (!hostPort) return remark
  return remark ? `${remark} | ${hostPort}` : hostPort
}

// 快捷入口卡片专用截断：实例名称 / IP:Port 各自最多 20 字符，超出补 ...（与监控、性能页口径一致）
const clipHomeCardText = (value, limit = 20) => {
  const text = String(value || '').trim()
  if (!text) return ''
  return text.length > limit ? `${text.slice(0, limit)}...` : text
}

// 组合标签（"备注 | host:port"）按分隔符拆开分别截断，避免整串截断把 IP 挤掉
const clipInstanceCardTitle = (label) => {
  const text = String(label || '').trim()
  if (!text) return ''
  const separatorIndex = text.indexOf(' | ')
  if (separatorIndex === -1) return clipHomeCardText(text)
  return `${clipHomeCardText(text.slice(0, separatorIndex))} | ${clipHomeCardText(text.slice(separatorIndex + 3))}`
}

const formatInstanceShortLabel = (instance) => {
  if (!instance) return ''
  const remark = String(instance?.remark || '').trim()
  if (remark) return remark
  const hostname = String(instance?.hostname || '').trim()
  const port = String(instance?.port || '').trim()
  const hostPort = hostname ? `${hostname}${port ? `:${port}` : ''}` : ''
  return hostPort || String(instance?.instance_name || instance?.name || '')
}

const formatDbTypeLabel = (value) => {
  switch (normalizeDbTypeValue(value)) {
    case 'mysql':
      return 'MySQL'
    case 'postgresql':
    case 'postgres':
      return 'PostgreSQL'
    case 'kingbase':
      return 'Kingbase'
    case 'sqlserver':
      return 'SQL Server'
    case 'oracle':
      return 'Oracle'
    case 'clickhouse':
      return 'ClickHouse'
    case 'tidb':
      return 'TiDB'
    case 'doris':
      return 'Doris'
    default:
      return String(value || '数据源').trim() || '数据源'
  }
}

const parseTimeValue = (value) => {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value
  }
  const parsed = new Date(value).getTime()
  return Number.isNaN(parsed) ? NaN : parsed
}

const formatHomeTime = (value) => {
  const timestamp = parseTimeValue(value)
  return Number.isFinite(timestamp) ? formatDateTime(timestamp) : '-'
}

const truncateSqlPreview = (value, limit = 120) => {
  const normalized = String(value || '').replace(/\s+/g, ' ').trim()
  if (!normalized) return '暂无 SQL 内容'
  return normalized.length > limit ? `${normalized.slice(0, limit)}...` : normalized
}

const normalizeFavoriteTitle = (record) => {
  const title = String(record?.title || '').trim()
  if (title) return title
  return truncateSqlPreview(record?.sqltext, 24)
}

const recentAccessCards = computed(() => {
  const oneWeekAgo = Date.now() - 7 * 24 * 60 * 60 * 1000
  const seen = new Set()
  const cards = []
  const sortedRecords = [...(emptyHomeData.historyRecords || [])].sort(
    (a, b) => parseTimeValue(b?.created_at) - parseTimeValue(a?.created_at),
  )
  for (const record of sortedRecords) {
    const createdAt = parseTimeValue(record?.created_at)
    if (!Number.isFinite(createdAt) || createdAt < oneWeekAgo) continue
    const instanceId = String(record?.instance_id || '').trim()
    const dedupeKey = instanceId || `${record?.hostname || ''}:${record?.port || ''}:${record?.schema || ''}`
    if (!dedupeKey || seen.has(dedupeKey)) continue
    seen.add(dedupeKey)
    const instanceMeta = getInstanceMetaById(instanceId) || record
    const schemaText = String(record?.schema || record?.display_name || '').trim()
    cards.push({
      key: dedupeKey,
      instance_id: instanceId,
      title: clipInstanceCardTitle(formatInstanceLabel(instanceMeta)) || String(record?.instance_name || schemaText || '未命名数据源'),
      meta: schemaText ? `最近使用: ${schemaText}` : '最近访问的数据源',
      subMeta: String(record?.hostname || '').trim()
        ? clipHomeCardText(`${record.hostname}${record.port ? `:${record.port}` : ''}`)
        : `数据库类型: ${formatDbTypeLabel(record?.db_type || instanceMeta?.db_type)}`,
      dbTypeLabel: formatDbTypeLabel(record?.db_type || instanceMeta?.db_type),
      timeText: formatHomeTime(record?.created_at),
    })
    if (cards.length >= 3) break
  }
  return cards
})

const favoriteCards = computed(() =>
  [...(emptyHomeData.favoriteRecords || [])]
    .sort((a, b) => {
      const timeB = parseTimeValue(b?.updated_at || b?.created_at)
      const timeA = parseTimeValue(a?.updated_at || a?.created_at)
      return timeB - timeA
    })
    .slice(0, 3)
    .map((record) => ({
    id: record?.id || normalizeFavoriteTitle(record),
    title: normalizeFavoriteTitle(record),
    subtitle: '收藏 SQL',
    sqlPreview: truncateSqlPreview(record?.sqltext),
    updatedAtText: formatHomeTime(record?.updated_at),
    raw: record,
    })),
)

const emptyHomeSelectedInstanceMeta = computed(() => getInstanceMetaById(emptyHomeData.selectedInstanceId) || null)
const canStartEmptyHomeQuery = computed(
  () =>
    !!emptyHomeData.selectedInstanceId
    && normalizeInstanceCheckStatus(emptyHomeSelectedInstanceMeta.value?.last_check_status) !== 'error',
)

const pickFirstAvailableInstanceId = (fallbackPreference = '') => {
  const available = uiData.instances || []
  if (!available.length) return undefined
  const safePref = String(fallbackPreference || '').trim()
  if (safePref && available.some((t) => String(t.instance_id) === safePref)) return safePref
  const healthy = available.find((t) => normalizeInstanceCheckStatus(t?.last_check_status) !== 'error')
  return (healthy || available[0])?.instance_id
}

const syncEmptyHomeSelectedInstance = () => {
  const currentId = String(emptyHomeData.selectedInstanceId || '').trim()
  const lastCreated = (() => {
    try {
      return typeof window !== 'undefined' ? String(window.localStorage.getItem('das.console.lastCreatedInstance') || '').trim() : ''
    } catch {
      return ''
    }
  })()
  if (currentId && getInstanceMetaById(currentId)) {
    return
  }
  if (lastCreated && !getInstanceMetaById(lastCreated)) {
    try {
      window.localStorage.removeItem('das.console.lastCreatedInstance')
    } catch {}
  }
  const candidate = pickFirstAvailableInstanceId(lastCreated)
  emptyHomeData.selectedInstanceId = candidate || undefined
}

const deriveResourceNames = (resource, dbType = uiData.db_type) => {
  // dbType 位置历史上传的是 db_type（含 "OceanBase"），统一按家族归一；
  // OceanBase 的租户模式只能从实例配置得知，所以把当前实例作为上下文一并传入，
  // 其余类型（含已是家族值的情况）口径不变 —— resolveDbFamily 对家族值是幂等的。
  const family = resolveFamilyInContext(
    dbType || resource?.db_family || resource?.db_type,
    currentInstanceMeta.value,
  )
  const resourcePath = String(resource?.resource_path || resource?.schema || '').trim()
  const parentPath = String(resource?.parent_path || '').trim()

  if (family === FAMILY_POSTGRES || family === FAMILY_SQLSERVER) {
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

const buildResourceOptionLabel = (resource, dbType) => {
  return deriveResourceNames(resource, dbType).displayName || resource?.display_name || resource?.schema || ''
}

const buildResourceOption = (resource, dbType) => ({
  value: resource.schema,
  label: buildResourceOptionLabel(resource, dbType),
  resource,
})

const sortOptions = (options = []) =>
  [...options].sort((a, b) => String(a.label || '').localeCompare(String(b.label || ''), 'zh-Hans-CN'))

const parseColumnListFromRaw = (rawColumns) => {
  if (Array.isArray(rawColumns)) {
    const out = []
    for (const item of rawColumns) {
      const s = String(item || '').trim()
      if (!s) continue
      const segs = s.split('$$').map((p) => String(p || '').trim())
      out.push(segs[0] || s)
    }
    return out
  }
  const raw = String(rawColumns || '').trim()
  if (!raw) return []
  const segs = raw.split('@@').map((p) => String(p || '').trim()).filter(Boolean)
  if (!segs.length) return []
  const out = []
  for (const seg of segs) {
    const parts = seg.split('$$').map((p) => String(p || '').trim())
    out.push(parts[0] || seg)
  }
  return out.filter(Boolean)
}

const buildCompletionMap = (rows = []) =>
  (Array.isArray(rows) ? rows : []).reduce((acc, row) => {
    const tableName = String(row?.table_name || '').trim()
    if (!tableName) return acc
    acc[tableName] = parseColumnListFromRaw(row?.columns)
    return acc
  }, {})

const applyEditorCompletion = (completionData = {}) => {
  // SQL 方言/补全按家族切换：OceanBase MySQL 模式必须拿到 mysql 方言，
  // 传 'OceanBase' 会退化成通用 SQL（高亮与补全都不对）。
  codemirrorRef.value?.setDialect?.(currentDbFamily.value, completionData)
}

let nextCompletionRetry = null
const loadSchemaCompletion = async (instanceId, schema, options = {}) => {
  const opts = options || {}
  const exposeError = !!opts.exposeError
  let setRetry = typeof opts.setRetry === 'function' ? opts.setRetry : null
  if (!instanceId || !schema) return {}
  const apiParams = { instance_id: instanceId, schema }
  let capturedErr = null
  const run = async () => {
    const res = await GetSchemaTablesApi(apiParams).catch((err) => {
      capturedErr = err || null
      return null
    })
    return buildCompletionMap(res?.data || [])
  }
  try {
    const result = await run()
    const category = capturedErr?.data?.category || capturedErr?.data?.__errorCategory || ''
    const raw = capturedErr?.data?.message || capturedErr?.data?.__errorRaw || ''
    if (capturedErr && exposeError) {
      const ceTitle = category === 'connection'
        ? '加载表元数据失败：当前实例无法连接'
        : (category === 'permission' ? '加载表元数据失败：权限不足' : '加载表元数据失败')
      setContextError({
        type: 'error',
        category: category || 'generic',
        title: ceTitle,
        detail: raw || '',
        source: 'completion-load',
      })
      if (setRetry) {
        try {
          setRetry(() => loadSchemaCompletion(instanceId, schema, { exposeError: true, setRetry }))
        } catch (e) {
          /* ignore */
        }
      } else {
        pendingContextRetryTask = () => {
          pendingContextRetryTask = null
          loadSchemaCompletion(instanceId, schema, { exposeError: true })
        }
      }
      return result
    }
    if (!capturedErr && exposeError) {
      clearContextError()
    }
    return result
  } catch (e) {
    if (exposeError) {
      setContextError({
        type: 'error',
        category: 'generic',
        title: '加载表元数据失败',
        detail: e?.message || '',
        source: 'completion-load',
      })
      pendingContextRetryTask = () => {
        pendingContextRetryTask = null
        loadSchemaCompletion(instanceId, schema, { exposeError: true })
      }
    }
    return {}
  } finally {
    nextCompletionRetry = null
  }
}

const buildSharedContext = (tables = {}) => ({
  instance_id: uiData.instance_id,
  db_type: uiData.db_type,
  database: uiData.database,
  schema_name: uiData.schema_name,
  schema: uiData.schema,
  resource_type: uiData.resource_type,
  display_name: uiData.display_name,
  remark: uiData.instance_remark,
  tables,
})

const syncSharedContext = (tables = {}) => {
  ignoreNextSharedSync = true
  dasInstanceData.value = buildSharedContext(tables)
}

const syncActivePaneContextState = () => {
  if (!uiData.activeKey) return
  const instanceMeta = getInstanceMetaById(uiData.instance_id)
  const env = resolveInstanceEnvironmentFields(instanceMeta, activePane.value || {})
  syncActivePaneMeta({
    instance_id: uiData.instance_id,
    instance_remark: uiData.instance_remark,
    db_type: uiData.db_type,
    database: uiData.database,
    schema: uiData.schema,
    schema_name: uiData.schema_name,
    display_name: uiData.display_name,
    environment_name: env.environment_name,
    environment_color: env.environment_color,
  }, { persist: false })
  persistPanes()
}

const PREFILL_INSTANCE_STORAGE_KEY = 'goinsight#das#prefill-instance'

const clearContextSelection = () => {
  uiData.database = ''
  uiData.schema_name = ''
  uiData.schema = ''
  uiData.resource_type = ''
  uiData.display_name = ''
  uiData.databaseOptions = []
  uiData.schemaOptions = []
  applyEditorCompletion({})
}

const fetchInstances = async () => {
  runtime.instanceLoading = true
  try {
    const res = await GetInstancesApi().catch(() => null)
    uiData.instances = res?.data || []
    hydratePaneEnvironmentsFromInstances()
    syncActivePaneContextState()
    syncEmptyHomeSelectedInstance()
  } finally {
    runtime.instanceLoading = false
  }
}

const fetchEmptyHomeData = async ({ preferRecent = false } = {}) => {
  uiState.emptyHomeLoading = true
  try {
    const [historyRes, favoritesRes] = await Promise.all([
      GetHistoryApi({
        page_size: 6,
        page: 1,
        is_page: true,
      }).catch(() => null),
      GetFavoritesApi({
        page_size: 4,
        page: 1,
        is_page: true,
      }).catch(() => null),
    ])
    emptyHomeData.historyRecords = Array.isArray(historyRes?.data) ? historyRes.data : []
    emptyHomeData.favoriteRecords = Array.isArray(favoritesRes?.data) ? favoritesRes.data : []
    if (preferRecent) {
      uiState.emptyHomeTab = 'recent'
    }
    syncEmptyHomeSelectedInstance()
  } finally {
    uiState.emptyHomeLoading = false
  }
}

const fetchResources = async (instanceId, resourceType, parentPath = '', options = {}) => {
  if (!instanceId) return []
  const opts = options || {}
  const silent = opts.silent !== false
  if (silent) {
    const res = await GetResourcesApi({
      instance_id: instanceId,
      resource_type: resourceType,
      parent_path: parentPath,
    }).catch(() => null)
    return res?.data || []
  }
  const res = await GetResourcesApi({
    instance_id: instanceId,
    resource_type: resourceType,
    parent_path: parentPath,
  }).catch((err) => Promise.reject(err))
  return res?.data || []
}

const fetchDatabaseOptions = async (instanceMeta) => {
  if (!instanceMeta?.instance_id) {
    uiData.databaseOptions = []
    uiData.schemaOptions = []
    return []
  }
  runtime.databaseLoading = true
  try {
    // 家族优先取实例下发的 db_family（OceanBase 单靠 db_type 判不出该取 database 还是 schema）
    const family = resolveInstanceDbFamily(instanceMeta)
    const resourceType = isOracleFamily(family) ? 'schema' : 'database'
    const resources = await fetchResources(instanceMeta.instance_id, resourceType)
    const nextOptions = sortOptions(resources.map((item) => buildResourceOption(item, family)))
    if (isOracleFamily(family)) {
      uiData.databaseOptions = []
      uiData.schemaOptions = nextOptions
    } else {
      uiData.databaseOptions = nextOptions
    }
    return nextOptions
  } finally {
    runtime.databaseLoading = false
  }
}

const fetchSchemaOptions = async (instanceMeta, databaseValue) => {
  if (!instanceMeta?.instance_id || !databaseValue) {
    uiData.schemaOptions = []
    return []
  }
  runtime.schemaLoading = true
  try {
    const resources = await fetchResources(instanceMeta.instance_id, 'schema', databaseValue)
    const nextOptions = sortOptions(
      resources.map((item) => buildResourceOption(item, resolveInstanceDbFamily(instanceMeta))),
    )
    uiData.schemaOptions = nextOptions
    return nextOptions
  } finally {
    runtime.schemaLoading = false
  }
}

const getDatabaseOptionByValue = (value) => uiData.databaseOptions.find((item) => item.value === value)
const getSchemaOptionByValue = (value) => uiData.schemaOptions.find((item) => item.value === value)

const updateContextFromResource = (resource) => {
  if (!resource) {
    uiData.schema = ''
    uiData.schema_name = ''
    uiData.resource_type = ''
    uiData.display_name = ''
    return
  }
  const resourceNames = deriveResourceNames(resource, currentDbFamily.value)
  if (isPgLike.value) {
    uiData.database = resourceNames.databaseName || uiData.database
    uiData.schema = resourceNames.resourcePath || resource.schema
    uiData.schema_name = resourceNames.schemaName
    uiData.resource_type = resource.resource_type
    uiData.display_name = resourceNames.displayName
    return
  }
  uiData.database = resourceNames.databaseName || resource.schema
  uiData.schema = resourceNames.resourcePath || resource.schema
  uiData.schema_name = resourceNames.schemaName
  uiData.resource_type = resource.resource_type
  uiData.display_name = resourceNames.displayName
}

const applySchemaSelection = async (resource, options = {}) => {
  const opts = options || {}
  const exposeError = !!opts.exposeError
  if (!resource) {
    uiData.schema = ''
    uiData.schema_name = ''
    uiData.resource_type = ''
    uiData.display_name = ''
    applyEditorCompletion({})
    syncSharedContext({})
    saveTabToCache()
    return
  }
  updateContextFromResource(resource)
  if (isCurrentInstanceUnavailable.value) {
    applyEditorCompletion({})
    syncSharedContext({})
    saveTabToCache()
    return
  }
  const completionOpts = exposeError ? {
    exposeError: true,
  } : {}
  const completionData = await loadSchemaCompletion(uiData.instance_id, uiData.schema, completionOpts)
  applyEditorCompletion(completionData)
  syncSharedContext(completionData)
  saveTabToCache()
}

const handleInstanceChange = async (value) => {
  clearResultNotice()
  const instanceMeta = getInstanceMetaById(value)
  uiData.instance_id = value || ''
  uiData.instance_remark = instanceMeta?.remark || ''
  uiData.db_type = instanceMeta?.db_type || ''
  clearContextSelection()
  syncSharedContext({})
  saveTabToCache()
  if (!value || !instanceMeta) {
    return
  }
  if (normalizeInstanceCheckStatus(instanceMeta.last_check_status) === 'error') {
    applyEditorCompletion({})
    syncSharedContext({})
    saveTabToCache()
    return
  }
  await fetchDatabaseOptions(instanceMeta)
  if (isOracleFamily(resolveInstanceDbFamily(instanceMeta))) {
    return
  }
  if (!canSelectDatabase.value && !showsSchemaSelector.value) {
    return
  }
  saveTabToCache()
}

const handleDatabaseChange = async (value) => {
  const option = getDatabaseOptionByValue(value)
  if (!option) {
    uiData.database = ''
    uiData.schema = ''
    uiData.schema_name = ''
    uiData.resource_type = ''
    uiData.display_name = ''
    uiData.schemaOptions = []
    applyEditorCompletion({})
    syncSharedContext({})
    saveTabToCache()
    return
  }
  if (isCurrentInstanceUnavailable.value) {
    if (isPgLike.value) {
      uiData.database = option.value
      uiData.schema = ''
      uiData.schema_name = ''
      uiData.resource_type = option.resource.resource_type
      uiData.display_name = option.label
    } else {
      updateContextFromResource(option.resource)
    }
    applyEditorCompletion({})
    syncSharedContext({})
    saveTabToCache()
    return
  }
  if (isPgLike.value) {
    uiData.database = option.value
    uiData.schema = ''
    uiData.schema_name = ''
    uiData.resource_type = option.resource.resource_type
    uiData.display_name = option.label
    applyEditorCompletion({})
    let schemaOptions = []
    try {
      schemaOptions = await fetchResources(getInstanceMetaById(uiData.instance_id)?.instance_id, 'schema', option.value, { silent: false })
        .then((resources) => {
          const nextOptions = sortOptions((resources || []).map((item) => buildResourceOption(item, resolveInstanceDbFamily(getInstanceMetaById(uiData.instance_id)))))
          uiData.schemaOptions = nextOptions
          return nextOptions
        })
      clearContextError()
    } catch (e) {
      const data = e?.data || {}
      const category = data?.category || data?.__errorCategory || ''
      const raw = data?.message || data?.__errorRaw || e?.message || ''
      const ceTitle = category === 'connection'
        ? '切换数据库失败：当前实例无法连接'
        : (category === 'permission' ? '切换数据库失败：权限不足' : '切换数据库失败')
      setContextError({
        type: 'error',
        category: category || 'generic',
        title: ceTitle,
        detail: raw,
        source: 'database-change',
      })
      pendingContextRetryTask = () => {
        pendingContextRetryTask = null
        handleDatabaseChange(value)
      }
      saveTabToCache()
      return
    }
    syncSharedContext({})
    if (schemaOptions.length === 1) {
      await handleSchemaChange(schemaOptions[0].value)
      return
    }
    saveTabToCache()
    return
  }
  try {
    await applySchemaSelection(option.resource, { exposeError: true })
    if (uiState.contextError.source === 'completion-load') {
      clearContextError()
    }
  } catch (e) {
    const data = e?.data || {}
    const category = data?.category || data?.__errorCategory || ''
    const raw = data?.message || data?.__errorRaw || e?.message || ''
    const ceTitle = category === 'connection'
      ? '切换数据库失败：当前实例无法连接'
      : (category === 'permission' ? '切换数据库失败：权限不足' : '切换数据库失败')
    setContextError({
      type: 'error',
      category: category || 'generic',
      title: ceTitle,
      detail: raw,
      source: 'database-change',
    })
    pendingContextRetryTask = () => {
      pendingContextRetryTask = null
      handleDatabaseChange(value)
    }
    saveTabToCache()
    return
  }
}

const handleSchemaChange = async (value) => {
  const option = getSchemaOptionByValue(value)
  if (!option) {
    uiData.schema = ''
    uiData.schema_name = ''
    uiData.resource_type = ''
    uiData.display_name = ''
    applyEditorCompletion({})
    syncSharedContext({})
    saveTabToCache()
    return
  }
  if (isCurrentInstanceUnavailable.value) {
    updateContextFromResource(option.resource)
    applyEditorCompletion({})
    syncSharedContext({})
    saveTabToCache()
    return
  }
  try {
    await applySchemaSelection(option.resource, { exposeError: true })
  } catch (e) {
    const data = e?.data || {}
    const category = data?.category || data?.__errorCategory || ''
    const raw = data?.message || data?.__errorRaw || e?.message || ''
    const ceTitle = category === 'connection'
      ? '切换 Schema 失败：当前实例无法连接'
      : (category === 'permission' ? '切换 Schema 失败：权限不足' : '切换 Schema 失败')
    setContextError({
      type: 'error',
      category: category || 'generic',
      title: ceTitle,
      detail: raw,
      source: 'schema-change',
    })
    pendingContextRetryTask = () => {
      pendingContextRetryTask = null
      handleSchemaChange(value)
    }
    saveTabToCache()
    return
  }
}

const filterOptionByCandidates = (keyword, candidates = []) => {
  if (!keyword) return true
  return candidates.map((item) => normalizeSearchText(item)).some((item) => item.includes(keyword))
}

const filterInstanceOption = (input, option) => {
  const keyword = normalizeSearchText(input)
  if (!keyword) return true
  const instance = getInstanceMetaById(option?.value)
  const candidates = [
    option?.children,
    option?.ellipsisRemark,
    option?.hostPort,
    option?.fullRemark,
    instance?.remark,
    instance?.hostname,
    instance ? `${instance.hostname}:${instance.port}` : '',
    instance?.db_type,
    String(option?.value || ''),
    String(instance?.instance_id || ''),
  ]
  return filterOptionByCandidates(keyword, candidates)
}

const filterDatabaseOption = (input, option) => {
  const keyword = normalizeSearchText(input)
  const target = getDatabaseOptionByValue(option?.value)
  return filterOptionByCandidates(keyword, [option?.children, target?.label, target?.value])
}

const filterSchemaOption = (input, option) => {
  const keyword = normalizeSearchText(input)
  const target = getSchemaOptionByValue(option?.value)
  return filterOptionByCandidates(keyword, [option?.children, target?.label, target?.value])
}

const handleSelectDropdownVisibleChange = (open) => {
  if (!open) return
  scheduleStripContextSelectNativeTitles()
}

const isEscaped = (text, index) => {
  let slashCount = 0
  for (let i = index - 1; i >= 0 && text[i] === '\\'; i -= 1) {
    slashCount += 1
  }
  return slashCount % 2 === 1
}

const splitSqlStatements = (sqlText) => {
  const statements = []
  let start = 0
  let inSingle = false
  let inDouble = false
  let inBacktick = false
  let inLineComment = false
  let inBlockComment = false

  for (let i = 0; i < sqlText.length; i += 1) {
    const char = sqlText[i]
    const next = sqlText[i + 1]

    if (inLineComment) {
      if (char === '\n') inLineComment = false
      continue
    }

    if (inBlockComment) {
      if (char === '*' && next === '/') {
        inBlockComment = false
        i += 1
      }
      continue
    }

    if (!inSingle && !inDouble && !inBacktick) {
      if (char === '-' && next === '-') {
        inLineComment = true
        i += 1
        continue
      }
      if (char === '/' && next === '*') {
        inBlockComment = true
        i += 1
        continue
      }
    }

    if (!inDouble && !inBacktick && char === "'" && !isEscaped(sqlText, i)) {
      inSingle = !inSingle
      continue
    }
    if (!inSingle && !inBacktick && char === '"' && !isEscaped(sqlText, i)) {
      inDouble = !inDouble
      continue
    }
    if (!inSingle && !inDouble && char === '`') {
      inBacktick = !inBacktick
      continue
    }

    if (char === ';' && !inSingle && !inDouble && !inBacktick) {
      statements.push({
        from: start,
        to: i + 1,
        text: sqlText.slice(start, i + 1),
      })
      start = i + 1
    }
  }

  if (start < sqlText.length) {
    statements.push({
      from: start,
      to: sqlText.length,
      text: sqlText.slice(start),
    })
  }

  return statements
    .map((item) => {
      const trimmed = item.text.trim()
      if (!trimmed) return null
      const leftTrimmedLength = item.text.length - item.text.trimStart().length
      const rightTrimmedLength = item.text.length - item.text.trimEnd().length
      return {
        from: item.from + leftTrimmedLength,
        to: item.to - rightTrimmedLength,
        text: trimmed,
      }
    })
    .filter(Boolean)
}

const resolveSqlExecutionDetail = () => {
  const selectedSql = (uiData.editorState.selectedText || codemirrorRef.value?.getSelectedText?.() || '').trim()
  if (selectedSql) {
    return { sqltext: selectedSql, source: 'selection' }
  }

  const fullContent = uiData.editorState.content || codemirrorRef.value?.getContent?.() || ''
  const fullSql = fullContent.trim()
  if (!fullSql) {
    return { sqltext: '', source: 'empty' }
  }

  const cursorPos = Number.isFinite(uiData.editorState.cursorPos) ? uiData.editorState.cursorPos : 0
  const statements = splitSqlStatements(fullContent)
  if (!statements.length) {
    return { sqltext: '', source: 'empty' }
  }

  const current = statements.find((item) => cursorPos >= item.from && cursorPos <= item.to)
  if (current?.text) {
    return { sqltext: current.text, source: statements.length === 1 ? 'single' : 'cursor' }
  }

  if (statements.length === 1) {
    return { sqltext: statements[0].text, source: 'single' }
  }

  return { sqltext: '', source: 'cursor-missing' }
}

const emitActiveExecutionMessage = () => {
  emit('renderExecutionMessage', runtime.queryResultMessageByTab[uiData.activeKey])
}

const applyActiveTabRuntime = () => {
  if (!uiData.activeKey || !activePane.value) {
    uiData.queryResultMessage = ''
    emit('renderResultTable', null)
    emit('renderExecutionMessage', '')
    return
  }
  const key = uiData.activeKey
  uiData.queryResultMessage = runtime.queryResultMessageByTab[key]
  const res = runtime.resultByTab[key]
  emit('renderResultTable', res || null)
  emitActiveExecutionMessage()
}

const clearAbortController = (tabKey) => {
  delete runtime.abortControllerByTab[tabKey]
}

const abortTabExecution = (tabKey) => {
  const controller = runtime.abortControllerByTab[tabKey]
  if (!controller) return false
  controller.abort()
  clearAbortController(tabKey)
  return true
}

const resetContextState = () => {
  uiData.instance_id = ''
  uiData.instance_remark = ''
  uiData.db_type = ''
  clearContextSelection()
}

const selectEmptyHomeInstance = (instanceId) => {
  emptyHomeData.selectedInstanceId = instanceId || undefined
}

const restoreContextByState = async () => {
  if (!uiData.instance_id) {
    applyEditorCompletion({})
    syncSharedContext({})
    return
  }
  const instanceMeta = getInstanceMetaById(uiData.instance_id)
  if (!instanceMeta) {
    resetContextState()
    syncSharedContext({})
    return
  }
  uiData.db_type = instanceMeta.db_type
  uiData.instance_remark = instanceMeta.remark || ''
  if (normalizeInstanceCheckStatus(instanceMeta.last_check_status) === 'error') {
    uiData.databaseOptions = []
    uiData.schemaOptions = []
    applyEditorCompletion({})
    syncSharedContext({})
    return
  }
  const databaseOptions = await fetchDatabaseOptions(instanceMeta)

  if (isPgLike.value) {
    if (!uiData.database) {
      syncSharedContext({})
      return
    }
    const hasDatabase = databaseOptions.some((item) => item.value === uiData.database)
    if (!hasDatabase) {
      uiData.database = ''
      uiData.schema = ''
      uiData.schema_name = ''
      uiData.resource_type = ''
      uiData.display_name = ''
      syncSharedContext({})
      return
    }
    await fetchSchemaOptions(instanceMeta, uiData.database)
    if (!uiData.schema) {
      syncSharedContext({})
      return
    }
    const schemaOption = getSchemaOptionByValue(uiData.schema)
    if (!schemaOption) {
      uiData.schema = ''
      uiData.schema_name = ''
      uiData.resource_type = ''
      uiData.display_name = ''
      syncSharedContext({})
      return
    }
    updateContextFromResource(schemaOption.resource)
    const completionData = await loadSchemaCompletion(uiData.instance_id, uiData.schema)
    applyEditorCompletion(completionData)
    syncSharedContext(completionData)
    return
  }

  if (isOracleFamily(currentDbFamily.value)) {
    uiData.schemaOptions = databaseOptions
    uiData.databaseOptions = []
    if (!uiData.schema) {
      syncSharedContext({})
      return
    }
    const schemaOption = getSchemaOptionByValue(uiData.schema)
    if (!schemaOption) {
      uiData.schema = ''
      uiData.resource_type = ''
      uiData.display_name = ''
      syncSharedContext({})
      return
    }
    updateContextFromResource(schemaOption.resource)
    const completionData = await loadSchemaCompletion(uiData.instance_id, uiData.schema)
    applyEditorCompletion(completionData)
    syncSharedContext(completionData)
    return
  }

  if (!uiData.database) {
    syncSharedContext({})
    return
  }
  const databaseOption = getDatabaseOptionByValue(uiData.database)
  if (!databaseOption) {
    uiData.database = ''
    uiData.schema = ''
    uiData.resource_type = ''
    uiData.display_name = ''
    syncSharedContext({})
    return
  }
  updateContextFromResource(databaseOption.resource)
  const completionData = await loadSchemaCompletion(uiData.instance_id, uiData.schema)
  applyEditorCompletion(completionData)
  syncSharedContext(completionData)
}

const removeTab = (targetKey) => {
  if (!canClosePane(getPaneByKey(targetKey))) return
  closeTabsByKeys([targetKey])
}

const handleTabChange = (value) => {
  saveTabToCache()
  uiData.activeKey = value
  loadTabFromCache()
  restoreContextByState()
  applyActiveTabRuntime()
  ensureActivePaneVisible()
}

const loadTab = () => {
  const rawPanes = getScopedStorageItem(PANES_STORAGE_KEY)
  // #region debug-point C:load-tab-entry
  reportDraftDebug('C', 'ConsoleRight.vue:loadTab', 'load tab entry', {
    rawPanesLength: rawPanes?.length || 0,
    activeRaw: getScopedStorageItem(ACTIVE_TAB_STORAGE_KEY),
    ownerKey: draftOwnerKey,
    ownerCandidates: draftOwnerCandidates,
  })
  // #endregion
  let panes = null
  try {
    panes = rawPanes ? JSON.parse(rawPanes) : null
  } catch {
    panes = null
  }
  if (Array.isArray(panes)) {
    uiData.panes = panes.map((pane, index) => normalizePane(pane, index + 1))
  } else {
    uiData.panes = []
  }
  reorderPinnedFirst()
  const cachedActiveKey = Number(getScopedStorageItem(ACTIVE_TAB_STORAGE_KEY))
  uiData.activeKey = Number.isFinite(cachedActiveKey) && cachedActiveKey > 0 ? cachedActiveKey : uiData.panes?.[0]?.key ?? 0
  uiData.nextTabKey = Math.max(uiData.panes.reduce((maxKey, pane) => Math.max(maxKey, Number(pane?.key) || 0), 0) + 1, 1)
  uiData.nextConsoleIndex = Math.max(
    uiData.panes
      .filter((pane) => pane?.kind === 'console')
      .reduce((maxIndex, pane) => Math.max(maxIndex, Number(pane?.consoleIndex) || 0), 0) + 1,
    1,
  )
  // #region debug-point C:load-tab-exit
  reportDraftDebug('C', 'ConsoleRight.vue:loadTab', 'load tab exit', {
    panesLength: uiData.panes.length,
    activeKey: uiData.activeKey,
    nextTabKey: uiData.nextTabKey,
    paneKeys: uiData.panes.map((pane) => pane.key),
  })
  // #endregion
}

const saveTabToCache = () => {
  const editorContent = getEditorContent()
  // #region debug-point D:save-tab
  reportDraftDebug('D', 'ConsoleRight.vue:saveTabToCache', 'save tab to cache', {
    activeKey: uiData.activeKey,
    hasActivePane: !!activePane.value,
    panesLength: uiData.panes.length,
    editorLength: editorContent.length,
    editorPreview: editorContent.slice(0, 80),
    ownerKey: draftOwnerKey,
  })
  // #endregion
  if (!uiData.activeKey || !activePane.value) {
    removeScopedStorageItem(ACTIVE_TAB_STORAGE_KEY)
    mirrorDraftRemove(ACTIVE_TAB_STORAGE_KEY)
    return
  }
  const instanceMeta = getInstanceMetaById(uiData.instance_id)
  const env = resolveInstanceEnvironmentFields(instanceMeta, activePane.value || {})
  const tabData = {
    userInput: editorContent,
    instance_id: uiData.instance_id,
    schema: uiData.schema,
    resource_type: uiData.resource_type,
    database: uiData.database,
    schema_name: uiData.schema_name,
    db_type: uiData.db_type,
    instance_remark: uiData.instance_remark,
    display_name: uiData.display_name,
    environment_name: env.environment_name,
    environment_color: env.environment_color,
    result: runtime.resultByTab[uiData.activeKey],
    queryResultMessage: runtime.queryResultMessageByTab[uiData.activeKey],
  }
  syncActivePaneMeta({
    instance_id: tabData.instance_id,
    instance_remark: tabData.instance_remark,
    db_type: tabData.db_type,
    database: tabData.database,
    schema: tabData.schema,
    schema_name: tabData.schema_name,
    display_name: tabData.display_name,
    environment_name: tabData.environment_name,
    environment_color: tabData.environment_color,
    hasSqlContent: Boolean(String(tabData.userInput || '').trim()),
  }, { persist: false })
  let tabRaw = ''
  try {
    tabRaw = JSON.stringify(tabData)
  } catch {
    const fallback = {
      userInput: tabData.userInput,
      instance_id: tabData.instance_id,
      schema: tabData.schema,
      resource_type: tabData.resource_type,
      database: tabData.database,
      schema_name: tabData.schema_name,
      db_type: tabData.db_type,
      instance_remark: tabData.instance_remark,
      display_name: tabData.display_name,
      environment_name: tabData.environment_name,
      environment_color: tabData.environment_color,
      queryResultMessage: tabData.queryResultMessage,
    }
    tabRaw = JSON.stringify(fallback)
  }
  const tabStorageKey = `${TAB_CACHE_PREFIX}${uiData.activeKey}`
  try {
    setScopedStorageItem(tabStorageKey, tabRaw)
  } catch {
    setScopedStorageItem(tabStorageKey, JSON.stringify({ userInput: tabData.userInput }))
  }
  mirrorDraftSet(tabStorageKey, tabRaw)
  const activeRaw = String(uiData.activeKey)
  setScopedStorageItem(ACTIVE_TAB_STORAGE_KEY, activeRaw)
  mirrorDraftSet(ACTIVE_TAB_STORAGE_KEY, activeRaw)
  persistPanes()
}

const readTabCache = (tabKey) => {
  try {
    return JSON.parse(getScopedStorageItem(`${TAB_CACHE_PREFIX}${tabKey}`))
  } catch {
    return null
  }
}

const loadTabFromCache = () => {
  // #region debug-point E:load-tab-cache-entry
  reportDraftDebug('E', 'ConsoleRight.vue:loadTabFromCache', 'load tab from cache entry', {
    activeKey: uiData.activeKey,
    panesLength: uiData.panes.length,
    hasActivePane: !!activePane.value,
  })
  // #endregion
  if (!uiData.activeKey || !activePane.value) {
    uiData.characterSet = 'utf8mb4'
    uiData.editorState = {
      ...uiData.editorState,
      content: '',
      selectedText: '',
      cursorPos: 0,
      hasSelection: false,
    }
    codemirrorRef.value?.setContent?.('')
    resetContextState()
    syncSharedContext({})
    uiData.queryResultMessage = ''
    return
  }
  const tabData = readTabCache(uiData.activeKey)
  if (tabData != null) {
    // #region debug-point E:load-tab-cache-hit
    reportDraftDebug('E', 'ConsoleRight.vue:loadTabFromCache', 'load tab from cache hit', {
      activeKey: uiData.activeKey,
      userInputLength: String(tabData.userInput || '').length,
      tabKeys: Object.keys(tabData || {}),
      ownerKey: draftOwnerKey,
    })
    // #endregion
    uiData.editorState = {
      ...uiData.editorState,
      content: tabData.userInput || '',
      selectedText: '',
      cursorPos: 0,
      hasSelection: false,
    }
    codemirrorRef.value?.setContent(tabData.userInput || '')
    uiData.characterSet = 'utf8mb4'
    uiData.instance_id = tabData.instance_id || ''
    uiData.schema = tabData.schema || ''
    uiData.resource_type = tabData.resource_type || ''
    uiData.database = tabData.database || ''
    uiData.schema_name = tabData.schema_name || ''
    uiData.db_type = tabData.db_type || ''
    uiData.instance_remark = tabData.instance_remark || ''
    uiData.display_name = getPureSchemaDisplayName({
      dbType: tabData.db_type,
      schema: tabData.schema,
      displayName: tabData.display_name,
      schemaName: tabData.schema_name,
      databaseName: tabData.database,
    }) || tabData.display_name || ''
    runtime.resultByTab[uiData.activeKey] = tabData.result
    runtime.queryResultMessageByTab[uiData.activeKey] = tabData.queryResultMessage
    syncActivePaneMeta({
      instance_id: uiData.instance_id,
      instance_remark: uiData.instance_remark,
      db_type: uiData.db_type,
      database: uiData.database,
      schema: uiData.schema,
      schema_name: uiData.schema_name,
      display_name: uiData.display_name,
      environment_name: tabData.environment_name || '',
      environment_color: tabData.environment_color || '',
      hasSqlContent: Boolean(String(tabData.userInput || '').trim()),
    }, { persist: false })
    syncSharedContext({})
  } else {
    // #region debug-point E:load-tab-cache-miss
    reportDraftDebug('E', 'ConsoleRight.vue:loadTabFromCache', 'load tab from cache miss', {
      activeKey: uiData.activeKey,
      panesLength: uiData.panes.length,
    })
    // #endregion
    uiData.characterSet = 'utf8mb4'
    uiData.editorState = {
      ...uiData.editorState,
      content: '',
      selectedText: '',
      cursorPos: 0,
      hasSelection: false,
    }
    codemirrorRef.value?.setContent('')
    resetContextState()
    delete runtime.resultByTab[uiData.activeKey]
    delete runtime.queryResultMessageByTab[uiData.activeKey]
    syncActivePaneMeta({
      instance_id: '',
      instance_remark: '',
      db_type: '',
      database: '',
      schema: '',
      schema_name: '',
      display_name: '',
      environment_name: '',
      environment_color: '',
      hasSqlContent: false,
    }, { persist: false })
    syncSharedContext({})
  }
}

const hydratePaneEnvironmentsFromInstances = () => {
  let changed = false
  uiData.panes = uiData.panes.map((pane) => {
    const instanceMeta = getInstanceMetaById(pane.instance_id)
    if (!instanceMeta) {
      return pane
    }
    const env = resolveInstanceEnvironmentFields(instanceMeta, pane)
    const nextRemark = String(instanceMeta.remark || pane.instance_remark || '').trim()
    if (
      nextRemark === pane.instance_remark &&
      env.environment_name === pane.environment_name &&
      env.environment_color === pane.environment_color
    ) {
      return pane
    }
    changed = true
    return normalizePane({
      ...pane,
      instance_remark: nextRemark,
      environment_name: env.environment_name,
      environment_color: env.environment_color,
    }, pane.key)
  })
  if (changed) {
    persistPanes()
  }
}

const hydratePanesFromCache = () => {
  let changed = false
  uiData.panes = uiData.panes.map((pane) => {
    const tabData = readTabCache(pane.key)
    if (!tabData) {
      return pane
    }
    changed = true
    return normalizePane({
      ...pane,
      instance_id: tabData.instance_id || pane.instance_id,
      instance_remark: tabData.instance_remark || pane.instance_remark,
      db_type: tabData.db_type || pane.db_type,
      database: tabData.database || pane.database,
      schema: tabData.schema || pane.schema,
      schema_name: tabData.schema_name || pane.schema_name,
      display_name: getPureSchemaDisplayName({
        dbType: tabData.db_type || pane.db_type,
        schema: tabData.schema || pane.schema,
        displayName: tabData.display_name || pane.display_name,
        schemaName: tabData.schema_name || pane.schema_name,
        databaseName: tabData.database || pane.database,
      }) || tabData.display_name || pane.display_name || '',
      environment_name: tabData.environment_name || pane.environment_name,
      environment_color: tabData.environment_color || pane.environment_color,
      hasSqlContent: Boolean(String(tabData.userInput || '').trim()) || pane.hasSqlContent,
    }, pane.key)
  })
  if (changed) {
    persistPanes()
  }
}

const generatorDataDictionary = () => {
  if (!uiData.schema) {
    message.warning('请先选择实例与库 / Schema')
    return
  }
  const payLoad = {
    instance_id: uiData.instance_id,
    schema: uiData.schema,
    db_type: uiData.db_type,
  }
  GetDBDictApi(payLoad).then((res) => {
    uiState.isDbDictOpen = true
    dbDictRef.value.render(res.data)
  })
}

const openSqlLibraryDrawer = (type) => {
  if (type === 'favorite') {
    if (!canOpenFavoriteLibrary.value) {
      return
    }
    uiState.sqlLibraryDrawerType = 'favorite'
    uiState.isSqlLibraryDrawerOpen = true
    return
  }
  if (type === 'history') {
    if (!canOpenHistoryLibrary.value) {
      return
    }
    uiState.sqlLibraryDrawerType = 'history'
    uiState.isSqlLibraryDrawerOpen = true
  }
}

const insertSqlToCurrentEditor = async (sqltext, { silentSuccess = false } = {}) => {
  const nextSql = String(sqltext || '').trim()
  if (!nextSql) {
    showActionHint('没有可插入的 SQL', 'warning')
    return
  }
  const currentContent = codemirrorRef.value?.getContent?.() || ''
  const mergedContent = currentContent.trim()
    ? `${currentContent.replace(/\s*$/, '')}\n\n${nextSql}`
    : nextSql
  codemirrorRef.value?.setContent?.(mergedContent)
  await nextTick()
  codemirrorRef.value?.focus?.()
  uiData.editorState = {
    ...uiData.editorState,
    content: mergedContent,
  }
  clearActionHint()
  syncActivePaneMeta({
    hasSqlContent: true,
  }, { persist: false })
  saveTabToCache()
  if (!silentSuccess) {
    showActionHint('已插入到当前编辑器', 'success')
  }
}

const waitForCondition = async (checker, { timeoutMs = 1200, intervalMs = 40 } = {}) => {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    if (checker()) return true
    await new Promise((resolve) => setTimeout(resolve, intervalMs))
  }
  return checker()
}

const normalizeHistoryRecordParams = (params) => {
  if (!params) return null
  if (typeof params === 'object') return params
  const text = String(params || '').trim()
  if (!text) return null
  try {
    return JSON.parse(text)
  } catch {
    return null
  }
}

const resolvePgHistoryContext = (record, sqltext) => {
  const rawSchema = String(record?.schema || '').trim()
  const params = normalizeHistoryRecordParams(record?.params)
  const candidates = [
    params?.schema,
    params?.Schema,
    params?.database,
    params?.Database,
    params?.database_name,
    params?.databaseName,
    params?.db,
    params?.db_name,
    params?.dbName,
    rawSchema,
  ].map((item) => String(item || '').trim()).filter(Boolean)

  const candidate = candidates[0] || ''
  let databaseValue = ''
  let schemaName = ''

  if (candidate) {
    const dotIndex = candidate.indexOf('.')
    if (dotIndex > 0) {
      databaseValue = candidate.slice(0, dotIndex).trim()
      schemaName = candidate.slice(dotIndex + 1).trim()
    } else if (uiData.databaseOptions.some((item) => item.value === candidate)) {
      databaseValue = candidate
    } else {
      schemaName = candidate
    }
  }

  if (!schemaName) {
    schemaName = extractQualifiedSchemaFromSql(sqltext)
  }

  if (!databaseValue && uiData.databaseOptions.length === 1) {
    databaseValue = String(uiData.databaseOptions[0]?.value || '').trim()
  }

  return { databaseValue, schemaName, rawSchema }
}

const applyHistoryContextSelection = async (record, sqltext) => {
  if (!record || isCurrentInstanceUnavailable.value) {
    return
  }
  const rawSchema = String(record?.schema || '').trim()

  if (isOracleFamily(currentDbFamily.value)) {
    if (!rawSchema) return
    await handleSchemaChange(rawSchema)
    return
  }

  if (isPgLike.value) {
    const { databaseValue, schemaName } = resolvePgHistoryContext(record, sqltext)
    if (databaseValue) {
      await waitForCondition(() => !runtime.databaseLoading)
      await handleDatabaseChange(databaseValue)
    }
    if (!schemaName) return
    await waitForCondition(() => !runtime.schemaLoading)
    const family = currentDbFamily.value
    const schemaOption = uiData.schemaOptions.find((item) => deriveResourceNames(item.resource, family).schemaName === schemaName)
      || uiData.schemaOptions.find((item) => String(item.value || '').trim().endsWith(`.${schemaName}`))
      || uiData.schemaOptions.find((item) => item.value === `${databaseValue}.${schemaName}` || item.value === schemaName)
    if (schemaOption) await handleSchemaChange(schemaOption.value)
    return
  }

  if (!rawSchema) return
  await handleDatabaseChange(rawSchema)
}

const insertSqlFromLibrary = async (payload) => {
  const mode = typeof payload === 'object' && payload ? String(payload.mode || 'current') : 'current'
  const sqltext = typeof payload === 'object' && payload ? payload.sqltext : payload
  if (mode === 'new_console') {
    const instanceId = String(payload?.record?.instance_id || '').trim()
    if (instanceId) {
      await createConsoleTabForInstance(instanceId)
    } else {
      addBlankConsoleTab()
    }
    await nextTick()
    await applyHistoryContextSelection(payload?.record, sqltext)
    await insertSqlToCurrentEditor(sqltext, { silentSuccess: true })
    return
  }
  await insertSqlToCurrentEditor(sqltext, { silentSuccess: true })
}

const openFavoriteInNewWindow = async (record) => {
  const sqltext = String(record?.sqltext || '').trim()
  if (!sqltext) {
    showActionHint('该收藏没有可插入的 SQL', 'warning')
    return
  }
  const instanceId = String(record?.instance_id || emptyHomeData.selectedInstanceId || '').trim()
  if (instanceId) {
    await createConsoleTabForInstance(instanceId)
  } else {
    addBlankConsoleTab()
  }
  await nextTick()
  await insertSqlToCurrentEditor(sqltext, { silentSuccess: true })
}

const addToFavorites = () => {
  const detail = resolveSqlExecutionDetail()
  const fallback = codemirrorRef.value?.getContent?.()?.trim?.() || ''
  const sqltext = detail.sqltext || fallback
  if (!sqltext) {
    showActionHint('请先输入要收藏的 SQL', 'warning')
    return
  }
  uiState.isFavoritesOpen = true
  favoritesFormState.value.sqltext = sqltext
}

const addToFavoritesByText = (sqlInput) => {
  const sqltext = String(sqlInput || '').trim()
  if (!sqltext) {
    showActionHint('请先输入要收藏的 SQL', 'warning')
    return
  }
  uiState.isFavoritesOpen = true
  favoritesFormState.value.sqltext = sqltext
}

const handleFavoritesSubmit = (data) => {
  CreateFavoritesApi(data).then((res) => {
    if (res.code == '0000') {
      showActionHint('收藏成功', 'success')
      fetchEmptyHomeData()
    } else {
      showActionHint(res.message || '收藏失败', 'error')
    }
    uiState.isFavoritesOpen = false
  })
}

const openPreviewTableInCurrentTab = async (payload = {}) => {
  saveTabToCache()
  const targetInstanceId = String(payload?.instance_id || '').trim()
  const sqlText = String(payload?.sqlText || '').trim()
  const tableName = String(payload?.table || '').trim()

  if (!targetInstanceId || !sqlText) {
    if (!targetInstanceId) showActionHint('缺少实例信息，无法查看表数据', 'warning')
    if (!sqlText) showActionHint('缺少可执行的查询 SQL', 'warning')
    return
  }

  if (!uiData.activeKey || !activePane.value) {
    addTab({
      kind: 'console',
      title: tableName || '查询',
      instance_id: targetInstanceId,
      instance_remark: payload?.remark || '',
      db_type: payload?.db_type || '',
      database: payload?.database || '',
      schema_name: payload?.schema_name || '',
      schema: payload?.schema || '',
      display_name: getPureSchemaDisplayName({
        dbType: payload?.db_type,
        schema: payload?.schema,
        displayName: payload?.display_name,
        schemaName: payload?.schema_name,
        databaseName: payload?.database,
      }) || payload?.display_name || payload?.schema_name || payload?.database || '',
      prefill: {
        userInput: sqlText,
        instance_id: targetInstanceId,
        schema: payload?.schema || '',
        resource_type: payload?.resource_type || '',
        database: payload?.database || '',
        schema_name: payload?.schema_name || '',
        db_type: payload?.db_type || '',
        instance_remark: payload?.remark || '',
        display_name: getPureSchemaDisplayName({
          dbType: payload?.db_type,
          schema: payload?.schema,
          displayName: payload?.display_name,
          schemaName: payload?.schema_name,
          databaseName: payload?.database,
        }) || payload?.display_name || payload?.schema_name || payload?.database || '',
      },
    })
    await restoreContextByState()
    delete runtime.resultByTab[uiData.activeKey]
    runtime.queryResultMessageByTab[uiData.activeKey] = ''
    uiData.queryResultMessage = ''
    emit('renderResultTable', null)
    emit('renderExecutionMessage', '')
    saveTabToCache()
    await runSqlAction('execute')
    return
  }

  const currentKey = uiData.activeKey
  const schemaDisplayName = getPureSchemaDisplayName({
    dbType: payload?.db_type,
    schema: payload?.schema,
    displayName: payload?.display_name,
    schemaName: payload?.schema_name,
    databaseName: payload?.database,
  }) || payload?.display_name || payload?.schema_name || payload?.database || ''

  uiData.instance_id = targetInstanceId
  uiData.instance_remark = payload?.remark || ''
  uiData.db_type = payload?.db_type || ''
  uiData.database = payload?.database || ''
  uiData.schema = payload?.schema || ''
  uiData.schema_name = payload?.schema_name || ''
  uiData.display_name = schemaDisplayName

  syncActivePaneMeta({
    instance_id: targetInstanceId,
    instance_remark: payload?.remark || '',
    db_type: payload?.db_type || '',
    database: payload?.database || '',
    schema: payload?.schema || '',
    schema_name: payload?.schema_name || '',
    display_name: schemaDisplayName,
    hasSqlContent: true,
  })

  const existingContent = codemirrorRef.value?.getContent?.() || ''
  const trimmedExisting = String(existingContent || '').trim()
  const mergedContent = trimmedExisting
    ? `${String(existingContent).replace(/\s*$/, '')}\n\n${sqlText}`
    : sqlText

  writeTabCache(currentKey, {
    userInput: mergedContent,
    instance_id: targetInstanceId,
    schema: payload?.schema || '',
    resource_type: payload?.resource_type || '',
    database: payload?.database || '',
    schema_name: payload?.schema_name || '',
    db_type: payload?.db_type || '',
    instance_remark: payload?.remark || '',
    display_name: schemaDisplayName,
  })

  if (codemirrorRef.value && typeof codemirrorRef.value.setContent === 'function') {
    codemirrorRef.value.setContent(mergedContent)
  }
  uiData.editorState = {
    ...uiData.editorState,
    content: mergedContent,
  }

  delete runtime.resultByTab[currentKey]
  runtime.queryResultMessageByTab[currentKey] = ''
  uiData.queryResultMessage = ''
  emit('renderResultTable', null)
  emit('renderExecutionMessage', '')

  await restoreContextByState()

  const instanceMeta = getInstanceMetaById(targetInstanceId)
  const shouldSkipCompletion = normalizeInstanceCheckStatus(instanceMeta?.last_check_status) === 'error'
  if (!shouldSkipCompletion) {
    const nextSchema = payload?.schema || uiData.schema || ''
    try {
      const completionData = nextSchema ? await loadSchemaCompletion(targetInstanceId, nextSchema) : {}
      applyEditorCompletion(completionData)
    } catch {
      applyEditorCompletion({})
    }
  } else {
    applyEditorCompletion({})
  }

  saveTabToCache()
  await runSqlAction('execute')
}

const isEditableShortcutTarget = (target) => {
  if (!(target instanceof HTMLElement)) {
    return false
  }
  if (target.closest('.cm-editor')) {
    return true
  }
  const tagName = String(target.tagName || '').toLowerCase()
  if (tagName === 'input' || tagName === 'textarea' || tagName === 'select') {
    return true
  }
  if (target.isContentEditable || target.closest('[contenteditable="true"]')) {
    return true
  }
  if (target.closest('[role="textbox"]')) {
    return true
  }
  return false
}

const handleGlobalSelectAllShortcut = (event) => {
  const pressedSelectAll = (event.metaKey || event.ctrlKey) && String(event.key || '').toLowerCase() === 'a'
  if (!pressedSelectAll) {
    return
  }
  const activeElement = document.activeElement instanceof HTMLElement ? document.activeElement : null
  // #region debug-point ENV:global-ctrl-a
  if (typeof fetch === 'function') {
    fetch('http://127.0.0.1:7777/event', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        sessionId: 'sql-editor-no-select',
        runId: 'pre',
        hypothesisId: 'A',
        location: 'ConsoleRight.vue:handleGlobalSelectAllShortcut',
        msg: '[DEBUG] ctrl-a captured',
        data: {
          key: String(event.key || ''),
          metaKey: !!event.metaKey,
          ctrlKey: !!event.ctrlKey,
          targetTag: event.target?.tagName || '',
          targetClass: (event.target instanceof HTMLElement && event.target.className) || '',
          activeTag: activeElement?.tagName || '',
          activeClass: activeElement?.className || '',
          activeIsEditable: activeElement ? isEditableShortcutTarget(activeElement) : false,
          focusInsideConsole:
            activeElement instanceof HTMLElement && consoleEditorCardRef.value?.contains?.(activeElement),
        },
        ts: Date.now(),
      }),
    }).catch(() => {})
  }
  // #endregion
  if (isEditableShortcutTarget(activeElement)) {
    return
  }
  if (!consoleEditorCardRef.value) {
    return
  }
  const focusInsideConsole =
    activeElement instanceof HTMLElement && consoleEditorCardRef.value.contains(activeElement)
  const focusInsideLeftTree =
    activeElement instanceof HTMLElement && !!activeElement.closest('.console-left-card, .resource-tree-panel, .resource-tree')
  if (!focusInsideConsole && !focusInsideLeftTree) {
    return
  }
  event.preventDefault()
  event.stopPropagation()
  codemirrorRef.value?.selectAllContent?.()
}

const isPlainInputTarget = (target) => {
  if (!(target instanceof HTMLElement)) return false
  if (target.closest('.cm-editor')) return false
  const tagName = String(target.tagName || '').toLowerCase()
  return tagName === 'input' || tagName === 'textarea' || tagName === 'select' || target.isContentEditable
}

const cycleTabs = (step = 1) => {
  const tabs = visiblePanes.value
  if (!tabs.length) return
  const currentIndex = tabs.findIndex((pane) => pane.key === uiData.activeKey)
  const safeIndex = currentIndex >= 0 ? currentIndex : 0
  const nextIndex = (safeIndex + step + tabs.length) % tabs.length
  handleTabChange(tabs[nextIndex].key)
}

const handleConsoleShortcut = (event) => {
  const key = String(event.key || '').toLowerCase()
  const target = event.target instanceof HTMLElement ? event.target : document.activeElement
  if ((event.metaKey || event.ctrlKey) && !event.altKey && !event.shiftKey && key === 'h' && !isPlainInputTarget(target)) {
    if (!canOpenHistoryLibrary.value) return
    event.preventDefault()
    openSqlLibraryDrawer('history')
    return
  }
  if ((event.metaKey || event.ctrlKey) && !event.altKey && event.shiftKey && key === 's' && !isPlainInputTarget(target)) {
    if (!canOpenFavoriteLibrary.value) return
    event.preventDefault()
    openSqlLibraryDrawer('favorite')
    return
  }
  if ((event.metaKey || event.ctrlKey) && !event.altKey && key === 't' && !isPlainInputTarget(target)) {
    event.preventDefault()
    openCreateConsoleTabModal('blank')
    return
  }
  if ((event.metaKey || event.ctrlKey) && !event.altKey && key === 'w' && !isPlainInputTarget(target)) {
    if (!activePane.value || !canClosePane(activePane.value)) return
    event.preventDefault()
    removeTab(uiData.activeKey)
    return
  }
  if ((event.metaKey || event.ctrlKey) && key === 'tab') {
    event.preventDefault()
    cycleTabs(event.shiftKey ? -1 : 1)
  }
}

const isConnectionBrokenError = (errorDetail) => {
  const text = String(errorDetail || '').toLowerCase()
  if (!text) return false
  return [
    'connection',
    'connect',
    'refused',
    'timeout',
    'broken pipe',
    'network',
    'no route to host',
    'session invalid',
    '连接',
    '断开',
    '超时',
    '会话失效',
  ].some((item) => text.includes(item))
}

const buildSqlPayload = (sqltext, schemaOverride = uiData.schema) => {
  const payLoad = {
    instance_id: uiData.instance_id,
    schema: schemaOverride,
    db_type: uiData.db_type,
    sqltext,
  }
  // MySQL 系（含 OceanBase MySQL 模式）需要显式下发字符集，否则中文/emoji 结果可能乱码
  if (isMysqlFamily(currentDbFamily.value)) {
    payLoad.params = {
      character_set_client: uiData.characterSet,
      character_set_connection: uiData.characterSet,
      character_set_results: uiData.characterSet,
    }
  }
  return payLoad
}

const normalizeQualifiedIdentifier = (value) => {
  const text = String(value || '').trim()
  if (!text) return ''
  if (
    (text.startsWith('`') && text.endsWith('`')) ||
    (text.startsWith('"') && text.endsWith('"')) ||
    (text.startsWith('[') && text.endsWith(']'))
  ) {
    return text.slice(1, -1).trim()
  }
  return text
}

const extractQualifiedSchemaFromSql = (sqltext) => {
  const sql = String(sqltext || '')
  if (!sql) return ''
  const patterns = [
    /\b(?:from|join|update|into|table|desc|describe)\s+((?:`[^`]+`|"[^"]+"|\[[^\]]+\]|[A-Za-z0-9_$-]+))\s*\.\s*(?:`[^`]+`|"[^"]+"|\[[^\]]+\]|[A-Za-z0-9_$-]+)/i,
    /\b((?:`[^`]+`|"[^"]+"|\[[^\]]+\]|[A-Za-z0-9_$-]+))\s*\.\s*(?:`[^`]+`|"[^"]+"|\[[^\]]+\]|[A-Za-z0-9_$-]+)/i,
  ]
  for (const pattern of patterns) {
    const matched = sql.match(pattern)
    if (matched?.[1]) {
      return normalizeQualifiedIdentifier(matched[1])
    }
  }
  return ''
}

const resolveExecutionSchemaContext = (sqltext) => {
  if (uiData.schema) {
    return {
      schema: uiData.schema,
      database: uiData.database,
      schemaName: uiData.schema_name,
      displayName: uiData.display_name,
      inferredFromSql: false,
    }
  }

  if (isPgLike.value && uiData.database) {
    return {
      schema: uiData.database,
      database: uiData.database,
      schemaName: '',
      displayName: uiData.database,
      inferredFromSql: false,
    }
  }

  const inferredSchema = extractQualifiedSchemaFromSql(sqltext)
  if (!inferredSchema) {
    return {
      schema: '',
      database: uiData.database,
      schemaName: '',
      displayName: '',
      inferredFromSql: false,
    }
  }

  if (isPgLike.value || isOracleFamily(currentDbFamily.value)) {
    if (!uiData.database && !isOracleFamily(currentDbFamily.value)) {
      return {
        schema: '',
        database: uiData.database,
        schemaName: '',
        displayName: '',
        inferredFromSql: false,
      }
    }
    return {
      schema: isOracleFamily(currentDbFamily.value) ? inferredSchema : `${uiData.database}.${inferredSchema}`,
      database: uiData.database,
      schemaName: inferredSchema,
      displayName: inferredSchema,
      inferredFromSql: true,
    }
  }

  return {
    schema: inferredSchema,
    database: inferredSchema,
    schemaName: '',
    displayName: inferredSchema,
    inferredFromSql: true,
  }
}

const SQL_REQUEST_CONFIG = {
  silentBusinessError: true,
  silentHttpError: true,
}

const parseDurationToMs = (value) => {
  if (value === null || value === undefined || value === '') return 0
  const text = String(value).trim()
  if (!text) return 0
  const matched = text.match(/^(\d+(?:\.\d+)?)(ms|s)?$/i)
  if (!matched) return 0
  const amount = Number(matched[1])
  if (Number.isNaN(amount)) return 0
  const unit = String(matched[2] || 'ms').toLowerCase()
  return unit === 's' ? amount * 1000 : amount
}

const formatDurationHuman = (valueMs) => {
  const ms = Math.max(Number(valueMs) || 0, 0)
  if (ms < 1000) {
    return `${Math.round(ms)}ms`
  }
  if (ms < 60000) {
    return `${(ms / 1000).toFixed(3)}s`
  }
  const totalSec = Math.floor(ms / 1000)
  const minutes = Math.floor(totalSec / 60)
  const seconds = totalSec % 60
  if (minutes < 60) {
    return seconds > 0 ? `${minutes}m ${seconds}s` : `${minutes}m`
  }
  const hours = Math.floor(minutes / 60)
  const remainMin = minutes % 60
  return remainMin > 0 ? `${hours}h ${remainMin}m` : `${hours}h`
}

const normalizeDurationText = (value) => {
  if (value === null || value === undefined || value === '') {
    return '-'
  }
  if (typeof value === 'number') {
    return formatDurationHuman(value)
  }
  const str = String(value).trim()
  const num = Number(str)
  if (str.length > 0 && !Number.isNaN(num)) {
    return formatDurationHuman(num)
  }
  return str
}

const formatPreciseDuration = (valueMs) => formatDurationHuman(valueMs)

const padDateTimePart = (value) => String(value).padStart(2, '0')

const formatDateTime = (timestamp) => {
  if (!Number.isFinite(timestamp)) return '-'
  const date = new Date(timestamp)
  if (Number.isNaN(date.getTime())) return '-'
  return [
    date.getFullYear(),
    padDateTimePart(date.getMonth() + 1),
    padDateTimePart(date.getDate()),
  ].join('-') + ` ${[padDateTimePart(date.getHours()), padDateTimePart(date.getMinutes()), padDateTimePart(date.getSeconds())].join(':')}`
}

const resolveResultRowCount = (result) => {
  const rows = result?.data
  if (Array.isArray(rows)) {
    return rows.length
  }
  if (typeof result?.rows === 'number') {
    return result.rows
  }
  if (typeof result?.row_count === 'number') {
    return result.row_count
  }
  if (typeof result?.count === 'number') {
    return result.count
  }
  return 0
}

const buildSqlFailureMessage = ({ action, duration, errorDetail }) => {
  const lines = [
    `结果: ${action === 'explain' ? '解释失败' : '执行失败'}`,
    `耗时: ${normalizeDurationText(duration)}`,
  ]
  lines.push(`详情: ${errorDetail || '未知错误'}`)
  return lines.join('\n')
}

const clearActionHint = () => {
  uiState.actionHint = ''
  if (actionHintTimer) {
    clearTimeout(actionHintTimer)
    actionHintTimer = null
  }
}

const showActionHint = (text, tone = 'warning') => {
  uiState.actionHint = text
  uiState.actionHintTone = tone
  if (actionHintTimer) {
    clearTimeout(actionHintTimer)
  }
  actionHintTimer = window.setTimeout(() => {
    uiState.actionHint = ''
    actionHintTimer = null
  }, ACTION_HINT_DURATION_MS)
}

const actionHintIcon = computed(() => {
  if (uiState.actionHintTone === 'success') return CheckCircleOutlined
  if (uiState.actionHintTone === 'error') return CloseCircleOutlined
  if (uiState.actionHintTone === 'info') return InfoCircleOutlined
  return WarningOutlined
})

provide('consoleToast', (text, tone) => showActionHint(text, tone))

const guardActionClick = async (action, callback) => {
  if (uiState.actionCooldown[action]) {
    return
  }
  uiState.actionCooldown[action] = true
  if (actionCooldownTimers[action]) {
    clearTimeout(actionCooldownTimers[action])
  }
  actionCooldownTimers[action] = window.setTimeout(() => {
    uiState.actionCooldown[action] = false
    actionCooldownTimers[action] = null
  }, ACTION_CLICK_COOLDOWN_MS)
  await callback()
}

const buildRequestConfig = (abortController) =>
  abortController ? { signal: abortController.signal, ...SQL_REQUEST_CONFIG } : SQL_REQUEST_CONFIG

const requestSqlAction = async ({ action, family, payload, tabKey }) => {
  const abortController = typeof AbortController !== 'undefined' ? new AbortController() : null
  if (abortController) {
    runtime.abortControllerByTab[tabKey] = abortController
  }
  try {
    if (action === 'explain') {
      if (isMysqlFamily(family)) {
        return await ExplainMySQLQueryApi(payload, buildRequestConfig(abortController))
      }
      if (isPostgresFamily(family)) {
        return await ExplainPostgresQueryApi(payload, buildRequestConfig(abortController))
      }
      throw new Error(explainSupportHint)
    }
    if (isMysqlFamily(family)) {
      return await ExecuteMySQLQueryApi(payload, buildRequestConfig(abortController))
    }
    if (isClickHouseFamily(family)) {
      return await ExecuteClickHouseQueryApi(payload, buildRequestConfig(abortController))
    }
    if (isPostgresFamily(family)) {
      return await ExecutePostgresQueryApi(payload, buildRequestConfig(abortController))
    }
    if (isSqlServerFamily(family)) {
      return await ExecuteSQLServerQueryApi(payload, buildRequestConfig(abortController))
    }
    if (isOracleFamily(family)) {
      return await ExecuteOracleQueryApi(payload, buildRequestConfig(abortController))
    }
    // 走到这里说明家族既不是 MySQL / ClickHouse / PG / SQLServer / Oracle：
    // 文案回显原始 db_type（用户认识的名字），不要回显家族名
    throw new Error(`不支持的数据库类型: ${uiData.db_type}`)
  } finally {
    clearAbortController(tabKey)
  }
}

const buildSingleExecutionView = ({ sqltext, result, status = 'success', message: executionMessage = '', fetchDurationText = '-' }) => ({
  mode: 'single',
  summary: null,
  resultSets: [
    {
      key: 'result',
      label: '结果集',
      sqltext,
      status,
      queryDuration: normalizeDurationText(result?.duration),
      fetchDuration: fetchDurationText,
      result,
    },
  ],
  activeResultKey: 'result',
  executionMessage,
})

const buildStatementMessage = ({ action, status, result, errorDetail, fetchDurationText }) => {
  if (status === 'canceled') {
    return ['结果: 已取消执行', `耗时: ${normalizeDurationText(result?.duration)}`].join('\n')
  }
  if (status === 'error') {
    return buildSqlFailureMessage({
      action,
      duration: normalizeDurationText(result?.duration),
      errorDetail,
    })
  }
  const lines = [action === 'explain' ? '结果: 已生成执行计划' : '结果: 执行成功']
  if (action === 'explain') {
    lines.push('说明: 已获取执行计划，未实际执行 SQL')
    lines.push(`适用: ${explainSupportHint.replace('解释功能当前支持 ', '')}`)
  }
  lines.push(`查询耗时: ${normalizeDurationText(result?.duration)}`)
  lines.push(`获取耗时: ${fetchDurationText}`)
  if (action !== 'explain') {
    lines.push(`返回行数: ${resolveResultRowCount(result)}`)
  }
  return lines.join('\n')
}

const buildBatchExecutionMessage = (summary, items = []) => {
  const lines = [
    `结果: 已处理 ${summary.processedCount} 条语句`,
    `成功: ${summary.successCount}`,
    `错误: ${summary.errorCount}`,
    `开始时间: ${summary.startedAt}`,
    `结束时间: ${summary.endedAt}`,
    `总耗时: ${summary.totalDuration}`,
  ]
  const failedItem = items.find((item) => item.status === 'error')
  if (failedItem) {
    lines.push(`已停止: 第 ${failedItem.sequence} 条执行失败`)
    lines.push(`失败详情: ${failedItem.errorDetail || '未知错误'}`)
  }
  const canceledItem = items.find((item) => item.status === 'canceled')
  if (canceledItem) {
    lines.push(`已停止: 第 ${canceledItem.sequence} 条被取消`)
  }
  return lines.join('\n')
}

const buildBatchExecutionView = ({ summary, items, resultSets }) => ({
  mode: 'batch',
  summary,
  resultSets,
  activeResultKey: resultSets[0]?.key || 'message',
  executionMessage: buildBatchExecutionMessage(summary, items),
})

const executeSqlRequest = async ({ action, sqltext, tabKey }) => {
  // 派发按「家族」而不是 db_type：OceanBase MySQL 模式必须走 /query/mysql，
  // Oracle 模式走 /query/oracle（后端也按同一口径二次判定）。
  const family = currentDbFamily.value
  const executionContext = resolveExecutionSchemaContext(sqltext)
  const payload = buildSqlPayload(sqltext, executionContext.schema)
  const startedAtMs = Date.now()
  const res = await requestSqlAction({ action, family, payload, tabKey })
  const endedAtMs = Date.now()
  const requestDurationMs = Math.max(endedAtMs - startedAtMs, 0)
  return {
    res,
    startedAtMs,
    endedAtMs,
    requestDurationMs,
    serverDurationMs: parseDurationToMs(res?.data?.duration),
  }
}

const runSqlActionBySqlText = async (action, sqlInput, options = {}) => {
  const tabKey = uiData.activeKey
  if (runtime.tableLoadingByTab[tabKey]) {
    message.warning('当前 SQL 正在执行，请稍候或先停止执行')
    return
  }
  saveTabToCache()
  clearResultNotice()
  if (!uiData.instance_id) {
    showActionHint('请先选择实例，再执行或解释 SQL。')
    return
  }
  const instanceMeta = getInstanceMetaById(uiData.instance_id)
  if (normalizeInstanceCheckStatus(instanceMeta?.last_check_status) === 'error') {
    setInstanceUnavailableNotice(instanceMeta)
    updatePane(tabKey, { connectionState: 'disconnected' })
    if (uiData.activeKey === tabKey) {
      emit('renderResultTable', null)
    }
    return
  }
  const sqltext = String(sqlInput || '').trim()
  if (!sqltext) {
    showActionHint(options.emptyMessage || '未选中 SQL。先框选 SQL，或将光标放在目标语句中再执行。')
    return
  }
  if (action === 'explain' && !canUseExplain.value) {
    showActionHint(explainSupportHint)
    return
  }
  clearActionHint()

  let nextMessage = ''
  const requestStartedAtMs = Date.now()
  runtime.tableLoadingByTab[tabKey] = true
  try {
    const { res, requestDurationMs, serverDurationMs } = await executeSqlRequest({ action, sqltext, tabKey })
    const fetchDurationMs = Math.max(requestDurationMs - serverDurationMs, 0)
    const fetchDurationText = formatPreciseDuration(fetchDurationMs)
    nextMessage = buildStatementMessage({
      action,
      status: 'success',
      result: res?.data,
      fetchDurationText,
    })
    const executionView = buildSingleExecutionView({
      sqltext,
      result: res?.data,
      message: nextMessage,
      fetchDurationText,
    })
    runtime.resultByTab[tabKey] = executionView
    runtime.queryResultMessageByTab[tabKey] = nextMessage
    updatePane(tabKey, { connectionState: 'normal' })
    if (action === 'explain') {
      message.success('已生成执行计划')
    }
    if (uiData.activeKey === tabKey) {
      emit('renderResultTable', executionView)
      uiData.queryResultMessage = nextMessage
      emit('renderExecutionMessage', nextMessage)
    }
  } catch (err) {
    if (err?.canceled || err?.code === 'ERR_CANCELED') {
      nextMessage = '结果: 已取消执行'
      message.info('已取消执行')
    } else {
      nextMessage = buildSqlFailureMessage({
        action,
        duration: `${Math.max(Date.now() - requestStartedAtMs, 0)}ms`,
        errorDetail: err?.message || err?.data?.message || '未知错误',
      })
      if (isConnectionBrokenError(err?.message || err?.data?.message)) {
        updatePane(tabKey, { connectionState: 'disconnected' })
      }
    }
    runtime.queryResultMessageByTab[tabKey] = nextMessage
    delete runtime.resultByTab[tabKey]
    if (uiData.activeKey === tabKey) {
      emit('renderResultTable', null)
      uiData.queryResultMessage = nextMessage
      emit('renderExecutionMessage', nextMessage)
    }
  } finally {
    runtime.tableLoadingByTab[tabKey] = false
    saveTabToCache()
  }
}

const runSqlAction = async (action) => {
  const executeDetail = resolveSqlExecutionDetail()
  await runSqlActionBySqlText(action, executeDetail.sqltext, {
    emptyMessage: '未选中 SQL。先框选 SQL，或将光标放在目标语句中再执行。',
  })
}

const executeSqlBatchByText = async (sqlInput, options = {}) => {
  const tabKey = uiData.activeKey
  if (runtime.tableLoadingByTab[tabKey]) {
    message.warning('当前 SQL 正在执行，请稍候或先停止执行')
    return
  }
  saveTabToCache()
  clearResultNotice()
  if (!uiData.instance_id) {
    showActionHint('请先选择实例，再执行 SQL。')
    return
  }
  const instanceMeta = getInstanceMetaById(uiData.instance_id)
  if (normalizeInstanceCheckStatus(instanceMeta?.last_check_status) === 'error') {
    setInstanceUnavailableNotice(instanceMeta)
    updatePane(tabKey, { connectionState: 'disconnected' })
    if (uiData.activeKey === tabKey) {
      emit('renderResultTable', null)
    }
    return
  }
  const batchSqlText = String(sqlInput || '').trim()
  if (!batchSqlText) {
    showActionHint(options.emptyMessage || '请先框选要执行的 SQL。')
    return
  }
  const statements = splitSqlStatements(batchSqlText)
  if (!statements.length) {
    showActionHint(options.emptyMessage || '选中的内容中没有可执行的 SQL。')
    return
  }
  clearActionHint()
  if (statements.length === 1) {
    await runSqlActionBySqlText('execute', batchSqlText, {
      emptyMessage: options.emptyMessage || '请先框选要执行的 SQL。',
    })
    return
  }

  const startedAtMs = Date.now()
  const items = []
  const resultSets = []
  runtime.tableLoadingByTab[tabKey] = true
  try {
    for (let index = 0; index < statements.length; index += 1) {
      const sqltext = statements[index].text
      try {
        const { res, requestDurationMs, serverDurationMs } = await executeSqlRequest({
          action: 'execute',
          sqltext,
          tabKey,
        })
        const fetchDurationMs = Math.max(requestDurationMs - serverDurationMs, 0)
        const fetchDurationText = formatPreciseDuration(fetchDurationMs)
        const statementResult = res?.data || {}
        const item = {
          key: `statement-${index + 1}`,
          sequence: index + 1,
          sqltext,
          status: 'success',
          message: 'OK',
          errorDetail: '',
          queryDuration: normalizeDurationText(statementResult.duration),
          fetchDuration: fetchDurationText,
          rowCount: resolveResultRowCount(statementResult),
        }
        items.push(item)
        resultSets.push({
          key: `result-${resultSets.length + 1}`,
          label: `结果 ${resultSets.length + 1}`,
          sqltext,
          status: 'success',
          queryDuration: item.queryDuration,
          fetchDuration: fetchDurationText,
          result: statementResult,
        })
      } catch (err) {
        const isCanceled = err?.canceled || err?.code === 'ERR_CANCELED'
        const errorDetail = isCanceled ? '已取消执行' : err?.message || err?.data?.message || '未知错误'
        items.push({
          key: `statement-${index + 1}`,
          sequence: index + 1,
          sqltext,
          status: isCanceled ? 'canceled' : 'error',
          message: isCanceled ? '已取消' : `失败: ${errorDetail}`,
          errorDetail,
          queryDuration: '-',
          fetchDuration: '-',
          rowCount: 0,
        })
        if (isCanceled) {
          message.info('已取消执行')
        }
        break
      }
    }

    const endedAtMs = Date.now()
    const summary = {
      totalCount: statements.length,
      processedCount: items.length,
      successCount: items.filter((item) => item.status === 'success').length,
      errorCount: items.filter((item) => item.status === 'error').length,
      canceledCount: items.filter((item) => item.status === 'canceled').length,
      startedAt: formatDateTime(startedAtMs),
      endedAt: formatDateTime(endedAtMs),
      totalDuration: formatPreciseDuration(endedAtMs - startedAtMs),
      items,
    }
    const executionView = buildBatchExecutionView({
      summary,
      items,
      resultSets,
    })
    runtime.resultByTab[tabKey] = executionView
    runtime.queryResultMessageByTab[tabKey] = executionView.executionMessage
    if (uiData.activeKey === tabKey) {
      emit('renderResultTable', executionView)
      uiData.queryResultMessage = executionView.executionMessage
      emit('renderExecutionMessage', executionView.executionMessage)
    }
  } finally {
    runtime.tableLoadingByTab[tabKey] = false
    saveTabToCache()
  }
}

const executeSqlQuery = async () => {
  await guardActionClick('execute', async () => {
    const selectedSql = String(uiData.editorState.selectedText || codemirrorRef.value?.getSelectedText?.() || '').trim()
    if (selectedSql) {
      const selectedStatements = splitSqlStatements(selectedSql)
      if (selectedStatements.length > 1) {
        await executeSqlBatchByText(selectedSql, {
          emptyMessage: '请先框选要执行的 SQL。',
        })
        return
      }
    }
    await runSqlAction('execute')
  })
}

const explainSqlQuery = async () => {
  await guardActionClick('explain', async () => {
    await runSqlAction('explain')
  })
}

const cancelSqlExecution = () => {
  const tabKey = uiData.activeKey
  if (!runtime.tableLoadingByTab[tabKey]) return
  abortTabExecution(tabKey)
}

const formatSqlContent = () => {
  codemirrorRef.value?.formatContent(uiData.db_type)
  saveTabToCache()
}

const formatSqlByContextMenu = async () => {
  if (!hasActivePane.value) return
  if (hasEditorSelection.value) {
    const formatted = codemirrorRef.value?.formatSelection?.(uiData.db_type)
    if (!formatted) {
      message.warning('请先选中要格式化的 SQL')
      return
    }
  } else {
    const fullSql = getEditorContent().trim()
    if (!fullSql) {
      message.warning('请先输入要格式化的 SQL')
      return
    }
    codemirrorRef.value?.formatContent?.(uiData.db_type)
  }
  await nextTick()
  clearActionHint()
  saveTabToCache()
}

const copyEditorSelection = async (removeSelection = false) => {
  const selectedText = getEditorSelectedText()
  if (!selectedText) {
    showActionHint(removeSelection ? '请先选中要剪切的 SQL' : '请先选中要复制的 SQL', 'warning')
    return
  }
  const copied = await copyText(selectedText)
  if (!copied) {
    openCopyAssist(selectedText)
    return
  }
  if (removeSelection) {
    codemirrorRef.value?.replaceSelection?.('')
    await nextTick()
    saveTabToCache()
    return
  }
}

const copyEditorAll = async () => {
  const text = String(getEditorContent() || '')
  if (!text.trim()) {
    showActionHint('请先输入要复制的 SQL', 'warning')
    return
  }
  const copied = await copyText(text)
  if (!copied) {
    openCopyAssist(text)
    return
  }
  showActionHint('已复制', 'success')
}

const executeSqlByContextMenu = async () => {
  await guardActionClick('execute', async () => {
    const sqlText = getEditorContextSqlText()
    const statements = splitSqlStatements(sqlText)
    if (statements.length > 1) {
      await executeSqlBatchByText(sqlText, {
        emptyMessage: hasEditorSelection.value ? '请先选中要执行的 SQL。' : '请先输入要执行的 SQL。',
      })
      return
    }
    await runSqlActionBySqlText('execute', sqlText, {
      emptyMessage: hasEditorSelection.value ? '请先选中要执行的 SQL。' : '请先输入要执行的 SQL。',
    })
  })
}

const explainSqlByContextMenu = async () => {
  await guardActionClick('explain', async () => {
    await runSqlActionBySqlText('explain', getEditorContextSqlText(), {
      emptyMessage: hasEditorSelection.value ? '请先选中要查看执行计划的 SQL。' : '请先输入要查看执行计划的 SQL。',
    })
  })
}

const addToFavoritesByContextMenu = () => {
  addToFavoritesByText(getEditorContextSqlText())
}

const handleEditorContextMenuAction = async ({ key }) => {
  if (key === 'execute') {
    await executeSqlByContextMenu()
    return
  }
  if (key === 'format') {
    await formatSqlByContextMenu()
    return
  }
  if (key === 'explain') {
    await explainSqlByContextMenu()
    return
  }
  if (key === 'favorite') {
    addToFavoritesByContextMenu()
    return
  }
  if (key === 'favorite-library') {
    openSqlLibraryDrawer('favorite')
    return
  }
  if (key === 'history-library') {
    openSqlLibraryDrawer('history')
    return
  }
  if (key === 'cut') {
    await copyEditorSelection(true)
    return
  }
  if (key === 'copy') {
    await copyEditorSelection(false)
    return
  }
  if (key === 'paste') {
    await pasteClipboardToEditor()
    return
  }
  if (key === 'select-all') {
    codemirrorRef.value?.selectAllContent?.()
  }
}

const handleEditorStateChange = (state) => {
  // #region debug-point F:editor-state-change
  reportDraftDebug('F', 'ConsoleRight.vue:handleEditorStateChange', 'editor state change', {
    contentLength: String(state?.content || '').length,
    contentPreview: String(state?.content || '').slice(0, 80),
    activeKey: uiData.activeKey,
    panesLength: uiData.panes.length,
    hasActivePane: !!activePane.value,
  })
  // #endregion
  uiData.editorState = {
    content: state?.content || '',
    selectedText: state?.selectedText || '',
    cursorPos: state?.cursorPos || 0,
    hasSelection: !!state?.hasSelection,
  }
  clearActionHint()
  // 持久化全部并入 800ms 防抖：此前 syncActivePaneMeta（含 persistPanes 的
  // 全量 stringify + localStorage + IndexedDB 双写）在每次按键同步执行，
  // 大 SQL 草稿下会造成输入主线程卡顿，干扰 IME 组合输入。
  if (editorAutosaveTimer) {
    clearTimeout(editorAutosaveTimer)
  }
  editorAutosaveTimer = setTimeout(() => {
    editorAutosaveTimer = null
    syncActivePaneMeta({
      hasSqlContent: Boolean(String(state?.content || '').trim()),
    }, { persist: false })
    persistPanes()
    saveTabToCache()
  }, 800)
}

watch(
  dasInstanceData,
  async (newVal) => {
    const nextValue = newVal?.value
    if (!nextValue) return
    if (ignoreNextSharedSync) {
      ignoreNextSharedSync = false
      return
    }
    if (!nextValue.instance_id) {
      resetContextState()
      applyEditorCompletion({})
      saveTabToCache()
      return
    }
    clearResultNotice()
    if (!uiData.instances.length || !getInstanceMetaById(nextValue.instance_id)) {
      await fetchInstances()
    }
    uiData.instance_id = nextValue.instance_id
    uiData.db_type = nextValue.db_type
    uiData.instance_remark = nextValue.remark || getInstanceMetaById(nextValue.instance_id)?.remark || ''
    uiData.database = nextValue.database || ''
    uiData.schema_name = nextValue.schema_name || ''
    uiData.schema = nextValue.schema || ''
    uiData.resource_type = nextValue.resource_type || ''
    uiData.display_name = getPureSchemaDisplayName({
      dbType: nextValue.db_type,
      schema: nextValue.schema,
      displayName: nextValue.display_name,
      schemaName: nextValue.schema_name,
      databaseName: nextValue.database,
    }) || nextValue.display_name || nextValue.schema_name || nextValue.schema || nextValue.database || ''
    await restoreContextByState()
    const instanceMeta = getInstanceMetaById(nextValue.instance_id)
    const shouldSkipCompletion = normalizeInstanceCheckStatus(instanceMeta?.last_check_status) === 'error'
    const completionData =
      shouldSkipCompletion
        ? {}
        : nextValue.tables && Object.keys(nextValue.tables).length
          ? nextValue.tables
          : nextValue.schema
            ? await loadSchemaCompletion(nextValue.instance_id, nextValue.schema)
            : {}
    applyEditorCompletion(completionData)
    saveTabToCache()
  },
  { deep: true },
)

watch(
  () => dasConsoleAction?.nonce,
  async (nonce) => {
    if (!nonce || !dasConsoleAction?.type) {
      return
    }
    if (dasConsoleAction.type === 'preview-table-data') {
      await openPreviewTableInCurrentTab(dasConsoleAction.payload || {})
      return
    }
    if (dasConsoleAction.type === 'insert-sql-to-editor') {
      const payload = dasConsoleAction.payload || {}
      const sqlText = String(payload?.sqlText || '').trim()
      if (sqlText) {
        await insertSqlToCurrentEditor(sqlText)
      }
      return
    }
    if (dasConsoleAction.type === 'retry-execute') {
      await runSqlAction('execute')
    }
  },
)

watch(
  () => [
    uiData.activeKey,
    uiData.instance_id,
    uiData.instance_remark,
    uiData.db_type,
    uiData.database,
    uiData.schema,
    uiData.schema_name,
    uiData.display_name,
  ],
  () => {
    syncActivePaneContextState()
  },
)

watch(
  () => [
    uiData.instance_id,
    uiData.database,
    uiData.schema,
    uiData.instances.length,
    uiData.databaseOptions.length,
    uiData.schemaOptions.length,
  ],
  () => {
    scheduleStripContextSelectNativeTitles()
  },
)

watch(
  () => [uiData.activeKey, visiblePanes.value.length],
  () => {
    nextTick(() => {
      updateTabScrollState()
      ensureActivePaneVisible()
    })
  },
)

watch(
  () => hasActivePane.value,
  (value) => {
    emit('activePaneChange', value)
    if (!value) {
      fetchEmptyHomeData({ preferRecent: true })
    }
  },
  { immediate: true },
)

let _lastNotifyWorkspaceFocusedAt = 0
const onConsoleWorkspaceClicked = () => {
  const now = typeof performance !== 'undefined' ? performance.now() : Date.now()
  if (now - _lastNotifyWorkspaceFocusedAt < 80) return
  _lastNotifyWorkspaceFocusedAt = now
  if (!dasConsoleAction) return
  dasConsoleAction.type = 'sql-workspace-focused'
  dasConsoleAction.payload = null
  dasConsoleAction.nonce = (dasConsoleAction.nonce || 0) + 1
}


// 消费外部页面（数据源总览「SQL查询」按钮等）通过 localStorage 预置的实例上下文。
// 页面 keepAlive：首次挂载走 onMounted，之后再次跳转进来只触发 onActivated，
// 因此两处都要消费；key 读取即删除，两处天然互斥不会重复应用。
//
// 消费动作 = 直接打开绑定该实例的查询窗口（与空首页「开始查询」同路径）。
// 此前只调 handleInstanceChange 预填选择器：没有已恢复的查询窗口时会停在空首页，
// 用户还得再点一次「开始查询」，表现为「有时进窗口、有时进空首页」的不一致。
const consumePrefillInstance = async () => {
  try {
    const raw = window.localStorage.getItem(PREFILL_INSTANCE_STORAGE_KEY)
    if (!raw) return
    window.localStorage.removeItem(PREFILL_INSTANCE_STORAGE_KEY)
    const payload = JSON.parse(raw)
    const instanceId = String(payload?.instance_id || '').trim()
    if (!instanceId || !getInstanceMetaById(instanceId)) return
    await createConsoleTabForInstance(instanceId)
  } catch {
    window.localStorage.removeItem(PREFILL_INSTANCE_STORAGE_KEY)
  }
}

onMounted(async () => {
  // #region debug-point G:mounted-start
  reportDraftDebug('G', 'ConsoleRight.vue:onMounted', 'mounted start', {
    panesLength: uiData.panes.length,
    activeKey: uiData.activeKey,
    ownerKey: draftOwnerKey,
    ownerCandidates: draftOwnerCandidates,
    userStoreUid: String(userStore.uid || ''),
    userStoreUsername: String(userStore.username || ''),
  })
  // #endregion
  document.addEventListener('keydown', handleGlobalSelectAllShortcut, true)
  document.addEventListener('keydown', handleConsoleShortcut, true)
  window.addEventListener('resize', updateTabScrollState)
  window.addEventListener('pagehide', flushEditorDraft)
  window.addEventListener('beforeunload', flushEditorDraft)
  document.addEventListener('visibilitychange', handleDraftVisibilityChange)
  await hydrateConsoleCacheFromDrafts()
  // #region debug-point G:after-hydrate
  reportDraftDebug('G', 'ConsoleRight.vue:onMounted', 'after hydrate drafts', {
    panesRawLength: getScopedStorageItem(PANES_STORAGE_KEY)?.length || 0,
    activeRaw: getScopedStorageItem(ACTIVE_TAB_STORAGE_KEY),
  })
  // #endregion
  loadTab()
  hydratePanesFromCache()
  loadTabFromCache()
  // #region debug-point G:after-load
  reportDraftDebug('G', 'ConsoleRight.vue:onMounted', 'after load cache', {
    panesLength: uiData.panes.length,
    activeKey: uiData.activeKey,
    editorStateLength: String(uiData.editorState.content || '').length,
  })
  // #endregion
  await fetchInstances()
  await fetchEmptyHomeData()
  await consumePrefillInstance()
  await restoreContextByState()
  applyActiveTabRuntime()
  scheduleStripContextSelectNativeTitles()
  updateTabScrollState()
  ensureActivePaneVisible()
})

// keepAlive 激活：从其他页面（如数据源总览）再次跳转进来时消费新的预置实例。
// fetchInstances 仅在首次挂载执行，激活时实例列表已在内存中，直接应用预置即可。
onActivated(async () => {
  await consumePrefillInstance()
  applyActiveTabRuntime()
  ensureActivePaneVisible()
})

onBeforeUnmount(() => {
  document.removeEventListener('keydown', handleGlobalSelectAllShortcut, true)
  document.removeEventListener('keydown', handleConsoleShortcut, true)
  window.removeEventListener('resize', updateTabScrollState)
  window.removeEventListener('pagehide', flushEditorDraft)
  window.removeEventListener('beforeunload', flushEditorDraft)
  document.removeEventListener('visibilitychange', handleDraftVisibilityChange)
  clearActionHint()
  flushEditorDraft()
  Object.keys(actionCooldownTimers).forEach((key) => {
    if (actionCooldownTimers[key]) {
      clearTimeout(actionCooldownTimers[key])
      actionCooldownTimers[key] = null
    }
  })
  Object.keys(runtime.abortControllerByTab).forEach((tabKey) => {
    abortTabExecution(tabKey)
  })
  saveTabToCache()
})

defineExpose({
  uiData,
  activePane,
  visiblePanes,
  tabScrollState,
  renamingTabKey,
  renamingValue,
  draggingTabKey,
  bindTabScrollerRef,
  bindRenameInputRef,
  buildPaneTitle,
  buildPaneTooltipVm,
  buildPaneTooltipTitle,
  buildPaneInstanceShort,
  getPaneStatus,
  getPaneEnvironmentStyle,
  canClosePane,
  handleTabChange,
  removeTab,
  startRenamePane,
  commitRenamePane,
  cancelRenamePane,
  setRenamingValue: (value) => {
    renamingValue.value = value
  },
  handleTabDragStart,
  handleTabDragOver,
  handleTabDrop,
  handleTabDragEnd,
  handlePaneContextMenuAction,
  scrollTabsBy,
  updateTabScrollState,
  handleTabWheel,
  openCreateConsoleTabModal,
  addBlankConsoleTab,
  handleCreateTabMenuAction,
  handleGlobalTabAction,
  formatInstanceLabel,
  formatInstanceShortLabel,
  getPaneDbType,
  getPaneFamily,
  isPaneBoundToInstance,
})
</script>

<style scoped>
.console-editor-card {
  border: 0;
  border-radius: 10px;
  background: var(--ant-colorBgContainer, #ffffff);
  box-shadow: 0 0 0 1px var(--console-border-color, rgba(5, 17, 25, 0.10));
  overflow: hidden;
}

.console-topbar {
  border-bottom: 0;
  background: #ffffff;
  padding: 6px 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.topbar-row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.tabbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 0 0 auto;
}

.tabbar-center {
  min-width: 0;
  flex: 1 1 auto;
  display: flex;
  align-items: center;
  gap: 6px;
}

.tabbar-entry-button,
.tab-scroll-button,
.tabbar-primary-action {
  height: 28px;
  border-radius: 6px;
  font-size: 12px;
}

.tabbar-entry-button :deep(.anticon),
.tab-scroll-button :deep(.anticon),
.tabbar-primary-action :deep(.anticon) {
  font-size: 12px;
}

.tabbar-split-create {
  display: inline-flex;
  align-items: center;
  gap: 0;
}

.tabbar-primary-action--main {
  border-top-right-radius: 0;
  border-bottom-right-radius: 0;
  padding-inline: 10px 9px;
  font-weight: 500;
  box-shadow: none;
}

.tabbar-primary-action--toggle {
  min-width: 24px;
  padding-inline: 0;
  border-top-left-radius: 0;
  border-bottom-left-radius: 0;
  margin-left: -1px;
}

.tabbar-primary-action--toggle:not(:disabled) {
  border-color: var(--ant-colorPrimary, #0f8a83);
  color: #ffffff;
  background: var(--ant-colorPrimary, #0f8a83);
}

.tabbar-primary-action--toggle:not(:disabled):hover {
  border-color: var(--ant-colorPrimaryHover, #117e78);
  color: #ffffff;
  background: var(--ant-colorPrimaryHover, #117e78);
}

.tabbar-more-button {
  min-width: 28px;
  padding-inline: 0;
}

.tabbar-more-button :deep(.anticon) {
  font-size: 13px;
}

.console-tab-scroll {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 4px;
  overflow-x: auto;
  overflow-y: hidden;
  padding-bottom: 2px;
  scrollbar-width: none;
}

.console-tab-scroll::-webkit-scrollbar {
  display: none;
}

.console-tab-empty {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  height: 28px;
  padding: 0 12px;
  border: 1px dashed var(--console-border-color, #d9d9d9);
  border-radius: 6px;
  background: var(--ant-colorFillAlter, #fafafa);
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  font-size: 12px;
  white-space: nowrap;
}

.console-tab-shell {
  flex: 0 0 auto;
}

.console-tab-item {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  max-width: 240px;
  height: 28px;
  padding: 0 12px 0 10px;
  border-radius: 6px;
  border: 1px solid var(--console-border-color, #d9d9d9);
  background: var(--ant-colorBgContainer, #ffffff);
  color: var(--ant-colorTextSecondary, #606266);
  cursor: pointer;
  transition: all 0.18s ease;
}

.console-tab-item:hover {
  background: var(--ant-colorBgContainer, #ffffff);
  border-color: var(--ant-colorBorderSecondary, #c5ccd6);
  color: var(--ant-colorText, #303133);
}

.console-tab-item.is-active {
  border-color: var(--console-tab-env-soft-border, #b7dfbf);
  background: var(--console-tab-env-soft-bg, #eef8f0);
  color: var(--console-tab-env-text, #0f766e);
  box-shadow: none;
}

.console-tab-item.tab-kind-preview {
  background: var(--ant-colorBgContainer, #ffffff);
  border-color: var(--console-border-color, #d9d9d9);
  color: var(--ant-colorTextSecondary, #606266);
}

.console-tab-item.tab-kind-preview.is-active {
  border-color: var(--console-tab-env-soft-border, #dbe2ff);
  background: var(--console-tab-env-soft-bg, #f5f7ff);
  color: var(--console-tab-env-text, #5b6cb8);
}

.console-tab-item.is-dragging {
  opacity: 0.6;
}

.console-tab-item.is-editing {
  width: 240px;
  max-width: 240px;
  cursor: text;
}

.console-tab-content {
  min-width: 0;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.console-tab-status {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  /* 与全站统一的 20px 数据库类型 logo 对齐 */
  width: 20px;
  color: currentColor;
  flex: 0 0 auto;
}

.console-tab-status.status-running :deep(.anticon) {
  color: inherit;
}

.console-tab-dirty-dot {
  width: 8px;
  height: 8px;
  border-radius: 999px;
  background: currentColor;
}

.console-tab-texts {
  display: inline-flex;
  align-items: center;
  min-width: 0;
  overflow: hidden;
}

.console-tab-title,
.console-tab-affix {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 页签上的环境标识：小圆点取实例环境色，IP 走 hover tooltip */
.console-tab-env-dot {
  flex: 0 0 auto;
  width: 6px;
  height: 6px;
  margin-right: 5px;
  border-radius: 50%;
  background: var(--console-tab-env-color, transparent);
  box-shadow: 0 0 0 2px var(--console-tab-env-soft-bg, transparent);
}

.console-tab-title {
  max-width: 220px;
  font-size: 12px;
  font-weight: 500;
  transition: color 0.15s ease;
}

.console-tab-title.is-unbound {
  color: var(--ant-colorTextTertiary, rgba(0, 0, 0, 0.28));
  font-weight: 400;
  font-style: normal;
}

.console-tab-affix {
  max-width: 68px;
  font-size: 11px;
  opacity: 0.82;
}

.console-tab-pin {
  flex: 0 0 auto;
  font-size: 11px;
  color: inherit;
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

.console-tab-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: inherit;
  cursor: pointer;
  opacity: 0;
  transition: all 0.15s ease;
}

.console-tab-item:hover .console-tab-close,
.console-tab-item.is-active .console-tab-close {
  opacity: 0.9;
}

.console-tab-close:hover {
  background: rgba(255, 255, 255, 0.18);
}

.console-tab-item:not(.is-active) .console-tab-close:hover {
  background: rgba(15, 23, 42, 0.08);
}

.console-tab-rename-input {
  width: 100%;
}

.console-tab-rename-input :deep(.ant-input) {
  height: 28px;
  border-radius: 6px;
}

.toolbar-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-width: 0;
}

.action-group {
  display: flex;
  align-items: center;
  flex: 0 0 auto;
}

.console-toolbar {
  display: inline-flex;
  align-items: center;
}

.console-toolbar :deep(.ant-space-item) {
  margin-left: 0 !important;
  margin-right: 0 !important;
}

.console-toolbar :deep(.ant-space-item + .ant-space-item) {
  margin-left: 4px !important;
}

.console-toolbar :deep(.ant-btn-primary) {
  box-shadow: none;
}

.tool-icon-button {
  min-width: 28px;
  padding-inline: 6px;
}

.toolbar-tooltip-trigger {
  display: inline-flex;
}

.more-action-button {
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
}

.tool-icon-button--danger {
  color: var(--ant-colorError, #ff4d4f) !important;
  border-color: var(--ant-colorErrorBorder, #ffa39e) !important;
}

.tool-icon-button--danger:hover:not(:disabled) {
  color: var(--ant-colorErrorHover, #ff7875) !important;
  border-color: var(--ant-colorErrorHover, #ff7875) !important;
  background: rgba(255, 77, 79, 0.06) !important;
}

.tool-icon-button--danger:active:not(:disabled) {
  color: var(--ant-colorErrorActive, #d9363e) !important;
  border-color: var(--ant-colorErrorActive, #d9363e) !important;
  background: rgba(255, 77, 79, 0.12) !important;
}

.tool-icon-button--danger:disabled {
  color: var(--ant-colorTextDisabled, rgba(0, 0, 0, 0.25)) !important;
  border-color: var(--ant-colorBorder, #d9d9d9) !important;
  background: transparent !important;
  cursor: not-allowed;
}

.disabled-menu-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--ant-colorTextDisabled, rgba(0, 0, 0, 0.25));
}

.context-group {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  flex-wrap: nowrap;
  min-width: 0;
  flex: 1 1 auto;
  overflow-x: auto;
  overflow-y: hidden;
  scrollbar-width: none;
}

.context-control {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  flex: 0 0 auto;
}

.context-control--instance {
  /* 单行「实例名 ip:port」需要更多横向空间：名称 98px + IP 113px + 内边距 */
  flex: 0 1 300px;
  min-width: 210px;
  max-width: min(400px, 46vw);
}

.context-control--database {
  flex: 0 1 180px;
  min-width: 140px;
  max-width: min(260px, 32vw);
}

.context-control--schema {
  flex: 0 1 180px;
  min-width: 140px;
  max-width: min(260px, 32vw);
}

.control-label {
  flex: 0 0 auto;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  font-size: 12px;
  line-height: 30px;
  white-space: nowrap;
}

.context-select {
  flex: 1 1 auto;
  min-width: 0;
  width: 100%;
  max-width: 100%;
}

.instance-select {
  width: 100% !important;
  max-width: 100%;
}

:deep(.instance-select) {
  width: 100% !important;
  max-width: 100%;
}

.instance-status-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0 8px;
  height: 24px;
  border-radius: 999px;
  font-size: 12px;
  line-height: 22px;
  border: 1px solid transparent;
  cursor: default;
  user-select: none;
}

.instance-status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #d9d9d9;
}

.instance-status-badge.is-success {
  border-color: rgba(82, 196, 26, 0.36);
  background: rgba(82, 196, 26, 0.08);
  color: rgba(0, 0, 0, 0.72);
}

.instance-status-badge.is-success .instance-status-dot {
  background: #52c41a;
}

.instance-status-badge.is-error {
  border-color: rgba(255, 77, 79, 0.36);
  background: rgba(255, 77, 79, 0.08);
  color: rgba(0, 0, 0, 0.72);
}

.instance-status-badge.is-error .instance-status-dot {
  background: #ff4d4f;
}

.instance-status-badge.is-unknown {
  border-color: rgba(0, 0, 0, 0.12);
  background: rgba(0, 0, 0, 0.03);
  color: rgba(0, 0, 0, 0.6);
}

.context-error-alert {
  margin-top: 8px;
  margin-bottom: 6px;
  border-radius: 8px;
  font-size: 12px;
}

.context-error-alert :deep(.ant-alert-message) {
  font-weight: 500;
  font-size: 12px;
  line-height: 1.4;
  margin-bottom: 2px;
}

.context-error-alert :deep(.ant-alert-description) {
  font-size: 12px;
  line-height: 1.5;
  color: rgba(0, 0, 0, 0.72);
}

.context-error-alert :deep(.ant-alert-action) {
  padding-inline-start: 8px;
}

.database-select,
.schema-select {
  width: 100%;
  max-width: 100%;
}

.charset-select {
  width: 84px;
}

.context-group::-webkit-scrollbar {
  display: none;
}

:deep(.context-select .ant-select-selector) {
  height: 30px !important;
  padding-inline: 8px !important;
  border-radius: 6px !important;
}

:deep(.context-select .ant-select-selection-placeholder),
:deep(.context-select .ant-select-selection-item) {
  line-height: 28px !important;
}

:deep(.context-select .ant-select-selection-item) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

:deep(.context-select .ant-select-item-option-content) {
  overflow: hidden;
}

.select-option-label {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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

.instance-select-option__head {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.instance-select-option__head .instance-select-option__name {
  flex: 1 1 auto;
  min-width: 0;
  font-size: 13px;
}

.instance-select-option__env {
  flex: 0 0 auto;
  max-width: 96px;
  padding: 0 6px;
  border: 1px solid transparent;
  border-radius: 4px;
  font-size: 11px;
  line-height: 16px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 「实例」选择器已选中项：单行「实例名 ip:port」
   antd-vue 单选不支持自定义已选项渲染，且 .ant-select-selection-item:after 被 antd 占用（基线对齐），
   故用覆盖层自绘：名称可截断、IP 常驻；聚焦/搜索时隐藏，让位给搜索输入 */
.instance-select-shell {
  position: relative;
  display: flex;
  flex: 1 1 auto;
  min-width: 0;
}

.instance-select-display {
  position: absolute;
  top: 0;
  bottom: 0;
  left: 8px;
  right: 26px;
  display: flex;
  align-items: center;
  gap: 6px;
  overflow: hidden;
  pointer-events: none;
}

.instance-select-display__name {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 14px;
  line-height: 1.2;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
}

.instance-select-display__addr {
  flex: 0 0 auto;
  font-size: 11px;
  line-height: 1;
  font-family: var(--ant-font-family-code, ui-monospace, SFMono-Regular, Menlo, Consolas, monospace);
  font-variant-numeric: tabular-nums;
  color: #8c8c8c;
  white-space: nowrap;
}

:deep(.instance-select.is-display-overlay .ant-select-selection-item) {
  color: transparent !important;
}

/* 下拉项：名称 + 环境标签一行，host:port 第二行 */
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

:deep(.console-toolbar .ant-btn-sm) {
  height: 30px;
  border-radius: 6px;
  padding-inline: 10px;
  font-size: 12px;
}

:deep(.console-toolbar .tool-icon-button.ant-btn-sm) {
  padding-inline: 6px;
}

.hint-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  padding: 0 2px;
}

.hint-row.is-warning {
  color: var(--ant-colorWarning, #faad14);
}

.hint-row.is-success {
  color: var(--ant-colorSuccess, #52c41a);
}

.hint-row.is-error {
  color: var(--ant-colorError, #ff4d4f);
}

.hint-row.is-info {
  color: var(--ant-colorInfo, #1677ff);
}

.hint-label {
  flex-shrink: 0;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  font-size: 12px;
  font-weight: 500;
}

.hint-text {
  font-size: 12px;
  line-height: 1.4;
}

.console-editor-wrap {
  padding: 8px 12px 12px;
  background: var(--ant-colorBgContainer, #ffffff);
}

.console-editor-wrap.is-empty {
  display: flex;
  align-items: stretch;
}

.console-editor-surface {
  border: 1px solid var(--console-border-color, #d9d9d9);
  border-radius: 8px;
  background: var(--ant-colorBgContainer, #ffffff);
  overflow: hidden;
}

.console-empty-state {
  --console-home-accent: #0f8a83;
  --console-home-accent-soft: rgba(15, 138, 131, 0.1);
  --console-home-accent-softer: rgba(15, 138, 131, 0.06);
  --console-home-accent-border: rgba(15, 138, 131, 0.28);
  --console-home-accent-strong: rgba(15, 138, 131, 0.42);
  width: 100%;
  min-height: 460px;
  display: flex;
  align-items: stretch;
  justify-content: flex-start;
  border: 0;
  border-radius: 8px;
  background: #ffffff;
  text-align: left;
  padding: 8px 4px 12px;
}

.console-home-shell {
  display: flex;
  flex-direction: column;
  gap: 18px;
  width: 100%;
  min-height: 0;
}

.console-home-hero {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 24px;
  min-height: 168px;
  padding: 20px 24px 4px;
}

.console-home-illustration {
  position: relative;
  width: 150px;
  height: 150px;
  flex: 0 0 150px;
}

.console-home-illustration-ring {
  position: absolute;
  inset: 22px;
  border-radius: 50%;
  border: 10px solid var(--console-home-accent-border);
  box-shadow: inset 0 0 0 8px var(--console-home-accent-softer);
}

.console-home-illustration-core {
  position: absolute;
  left: 50%;
  top: 50%;
  width: 56px;
  height: 56px;
  transform: translate(-50%, -50%);
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 18px;
  background: linear-gradient(135deg, rgba(15, 138, 131, 0.16), rgba(15, 138, 131, 0.07));
  color: var(--console-home-accent);
  font-size: 26px;
}

.console-home-illustration-chip {
  position: absolute;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 40px;
  height: 28px;
  padding: 0 10px;
  border-radius: 999px;
  background: var(--console-home-accent-soft);
  color: var(--console-home-accent);
  font-size: 12px;
  font-weight: 600;
  box-shadow: 0 8px 18px rgba(15, 138, 131, 0.08);
}

.console-home-illustration-chip--sql {
  left: 88px;
  top: 16px;
}

.console-home-illustration-chip--play {
  left: 6px;
  bottom: 18px;
  width: 34px;
  min-width: 34px;
  padding: 0;
}

.console-home-illustration-dot {
  position: absolute;
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: var(--console-home-accent-strong);
}

.console-home-illustration-dot--one {
  left: 28px;
  top: 26px;
}

.console-home-illustration-dot--two {
  right: 18px;
  bottom: 26px;
}

.console-home-intro {
  max-width: 560px;
  min-width: 0;
}

.console-home-title {
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
  font-size: 28px;
  font-weight: 600;
  line-height: 1.2;
}

.console-home-action-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 18px;
}

.console-home-instance-select {
  /* 宽度由外层 .instance-select-shell 决定（见下），这里只是兜底口径 */
  width: 300px;
  max-width: 100%;
  flex: 0 0 300px;
}

.console-home-action-bar .instance-select-shell {
  /* 壳才是 flex 项：此前 flex:0 0 auto 收缩到内容宽（约 120px），
     内层 select 的 width:100% 只能继承壳宽，写 420px 也不生效。
     现按需求固定 300px（≈ 原 120px 的 2.5 倍），select 撑满壳 */
  flex: 0 0 300px;
  width: 300px;
}

.console-home-action-bar .instance-select-display {
  left: 11px;
  right: 30px;
}

.console-home-action-bar .instance-select-display__name {
  font-size: 16px;
}

.console-home-instance-select :deep(.ant-select-selection-placeholder) {
  color: rgba(15, 23, 42, 0.28);
  font-size: 13px;
  font-weight: 400;
}

.console-home-selection-hint {
  margin-top: 10px;
  color: rgba(15, 23, 42, 0.5);
  font-size: 13px;
  font-weight: 500;
  line-height: 1.7;
  letter-spacing: 0.02em;
}

.console-home-content {
  display: flex;
  flex-direction: column;
  gap: 18px;
  padding: 0 14px 8px;
}

.console-home-section {
  border: 1px solid rgba(5, 17, 25, 0.08);
  border-radius: 12px;
  background: linear-gradient(180deg, rgba(248, 250, 252, 0.6), #ffffff 34%);
  padding: 14px 16px 16px;
}

.console-home-section-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.console-home-section-title {
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
  font-size: 15px;
  font-weight: 600;
}

.console-home-tabs :deep(.ant-tabs-nav) {
  margin-bottom: 12px;
}

.console-home-tab-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.console-home-tab-empty {
  padding: 24px 0 8px;
}

.console-home-tab-empty :deep(.ant-empty-description) {
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.45));
  font-size: 13px;
}

.console-home-card-grid {
  display: grid;
  gap: 12px;
}

.console-home-card-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.console-home-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-height: 136px;
  padding: 14px;
  border: 1px solid rgba(5, 17, 25, 0.08);
  border-radius: 10px;
  background: #ffffff;
  transition: border-color 0.2s ease, box-shadow 0.2s ease, transform 0.2s ease;
}

.console-home-card--source {
  cursor: pointer;
}

.console-home-card:hover {
  border-color: var(--console-home-accent-border);
  box-shadow: 0 10px 24px rgba(15, 23, 42, 0.06);
  transform: translateY(-1px);
}

.console-home-card.is-active {
  border-color: var(--console-home-accent-strong);
  box-shadow: 0 0 0 2px var(--console-home-accent-softer);
}

.console-home-card-title-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
}

.console-home-card-title {
  flex: 1;
  min-width: 0;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.88));
  font-size: 14px;
  font-weight: 600;
  line-height: 1.5;
  word-break: break-word;
}

.console-home-card-tag {
  flex: 0 0 auto;
  margin-inline-end: 0;
  color: var(--console-home-accent);
  border-color: rgba(15, 138, 131, 0.18);
  background: var(--console-home-accent-softer);
}

.console-home-card-meta,
.console-home-card-submeta {
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  font-size: 12px;
  line-height: 1.5;
}

.console-home-card-sql {
  min-height: 54px;
  color: var(--ant-colorText, rgba(0, 0, 0, 0.82));
  font-size: 12px;
  line-height: 1.7;
  word-break: break-word;
  display: -webkit-box;
  line-clamp: 3;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.console-home-card-footer {
  margin-top: auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  color: var(--ant-colorTextSecondary, rgba(0, 0, 0, 0.65));
  font-size: 12px;
}

.console-home-card-action {
  color: var(--console-home-accent) !important;
}

.console-home-card-action:hover,
.console-home-card-action:focus {
  color: var(--console-home-accent) !important;
  opacity: 0.88;
}

.console-editor-surface :deep(.cm-wrapper) {
  border: 0;
  border-radius: 0;
}

:deep(.cm-editor),
:deep(.cm-scroller),
:deep(.cm-content) {
  background: #ffffff;
  background-image: none !important;
}

:deep(.cm-gutters) {
  color: var(--ant-colorTextTertiary, rgba(0, 0, 0, 0.45));
  background-color: #ffffff;
  border-right: 0 !important;
  box-shadow: none !important;
}

:deep(.cm-activeLineGutter) {
  background-color: transparent !important;
}

@media (max-width: 1400px) {
  .console-tab-item {
    max-width: 210px;
  }

  .context-group {
    gap: 4px;
  }

}

@media (max-width: 1200px) {
  .console-editor-card {
    border-bottom: 1px solid var(--console-border-color, #d9d9d9);
    border-radius: 0;
  }

  .instance-select {
    width: 100% !important;
  }

  :deep(.instance-select) {
    width: 100% !important;
  }

  .topbar-row {
    gap: 8px;
  }

  .database-select,
  .schema-select {
    width: 100%;
    max-width: 100%;
  }

  .charset-select {
    width: 78px;
  }

  .console-home-hero {
    align-items: flex-start;
  }

  .console-home-card-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 920px) {
  .console-topbar {
    padding: 6px 8px;
  }

  .topbar-row {
    flex-wrap: wrap;
  }

  .tabbar-center {
    order: 3;
    width: 100%;
  }

  .control-label {
    line-height: 30px;
  }

  .context-control--instance {
    flex: 0 1 240px;
    min-width: 190px;
    max-width: 320px;
  }

  .instance-select {
    width: 100% !important;
  }

  :deep(.instance-select) {
    width: 100% !important;
  }

  .context-control--database,
  .context-control--schema {
    flex: 0 1 150px;
    min-width: 120px;
    max-width: 220px;
  }

  .database-select,
  .schema-select {
    width: 100%;
    max-width: 100%;
  }

  .charset-select {
    width: 72px;
  }

  .console-home-hero {
    flex-direction: column;
    gap: 16px;
    padding-inline: 12px;
  }

  .console-home-intro {
    width: 100%;
    max-width: none;
  }

  .console-home-action-bar {
    flex-direction: column;
    align-items: stretch;
  }

  .console-home-instance-select {
    width: 100%;
    max-width: none;
    flex: 1 1 auto;
  }

  .console-home-action-bar .instance-select-shell {
    width: 100%;
    flex: 1 1 auto;
  }

  .console-home-card-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}

:global(.sql-library-drawer.sql-library-drawer--normal .ant-drawer-content-wrapper) {
  width: 75vw !important;
  height: 100vh !important;
  top: 0 !important;
  right: 0 !important;
  overflow: hidden;
  box-shadow: -10px 0 40px rgba(29,35,41,.08);
}

:global(.sql-library-drawer.sql-library-drawer--normal .ant-drawer-content) {
  height: 100vh !important;
  min-height: 0 !important;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden;
}

:global(.sql-library-drawer.sql-library-drawer--normal .ant-drawer-wrapper-body) {
  flex: 1 1 auto !important;
  min-height: 0 !important;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden;
}

:global(.sql-library-drawer.sql-library-drawer--normal .ant-drawer-header) {
  flex: 0 0 auto;
  min-height: 0;
}

:global(.sql-library-drawer.sql-library-drawer--normal .ant-drawer-body) {
  flex: 1 1 auto !important;
  min-height: 0 !important;
}

:global(.sql-library-drawer.sql-library-drawer--fullscreen) {
  z-index: 1060 !important;
}

:global(.sql-library-drawer.sql-library-drawer--fullscreen .ant-drawer-content-wrapper) {
  width: 100vw !important;
  height: 100vh !important;
  top: 0 !important;
  right: 0 !important;
  overflow: hidden;
}

:global(.sql-library-drawer.sql-library-drawer--fullscreen .ant-drawer-content) {
  height: 100vh !important;
  min-height: 0 !important;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden;
}

:global(.sql-library-drawer.sql-library-drawer--fullscreen .ant-drawer-wrapper-body) {
  flex: 1 1 auto !important;
  min-height: 0 !important;
  display: flex !important;
  flex-direction: column !important;
  overflow: hidden;
}

:global(.sql-library-drawer.sql-library-drawer--fullscreen .ant-drawer-header) {
  flex: 0 0 auto;
  min-height: 0;
}

:global(.sql-library-drawer.sql-library-drawer--fullscreen .ant-drawer-body) {
  flex: 1 1 auto !important;
  min-height: 0 !important;
}

:global(.sql-library-drawer .ant-drawer-mask) {
  backdrop-filter: blur(2px);
}

@media (max-width: 768px) {
  /* 操作按钮组 + 实例/库/Schema 三个选择器挤在一行，容器没有 flex-wrap */
  .toolbar-row {
    flex-wrap: wrap;
    row-gap: 8px;
    gap: 8px;
  }

  .action-group {
    width: 100%;
    justify-content: flex-start;
  }

  .context-group {
    width: 100%;
    flex: 1 1 100%;
    flex-wrap: wrap;
    justify-content: flex-start;
    row-gap: 6px;
    overflow-x: visible;
  }

  .context-control--instance,
  .context-control--database,
  .context-control--schema {
    flex: 1 1 100%;
    min-width: 0;
    max-width: 100%;
  }

  .control-label {
    font-size: 11px;
  }

  .console-toolbar :deep(.ant-btn-sm) {
    height: 34px;
    padding-inline: 12px;
  }

  .console-editor-wrap {
    padding: 6px 8px 8px;
  }

  .console-empty-state {
    min-height: 320px;
    padding: 8px 2px 10px;
  }

  .console-home-hero {
    min-height: 120px;
    padding: 12px 8px 0;
  }

  .console-home-title {
    font-size: 22px;
  }

  .console-home-content {
    padding: 0 8px 8px;
  }
}
</style>
