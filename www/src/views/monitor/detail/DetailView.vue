<template>
  <!-- 本页颜色统一取自系统主色调 token，色值定义在文件末尾的主题块 -->
  <!-- gi-page-shell：复用公共页面容器（flex 纵向 + 统一 gap），
       四边间距与最大宽度一律交给布局壳 .layout-content，与其它二级页面保持一致 -->
  <div class="gi-page-shell monitor-detail" :class="{ 'is-expanded': ovExpanded }">
    <!-- ====================================================================
         第一部分：工具栏 —— 搜索框 / 环境 / 类型 / 刷新
         ==================================================================== -->
    <PageHeader title="监控明细">
      <template #right>
      <div class="toolbar-actions">
        <div class="search-wrap">
          <!-- 复合搜索控件：关键字 + 环境 + 类型，三条件组合实时过滤 -->
          <div class="compound-search" :class="{ focused: searchFocused }">
            <svg width="13" height="13" viewBox="0 0 16 16" fill="none" aria-hidden="true" class="search-icon">
              <circle cx="7" cy="7" r="4.6" stroke="currentColor" stroke-width="1.4" />
              <path d="M10.6 10.6L14 14" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
            </svg>
            <input
              ref="searchInput"
              v-model="filters.search"
              class="field search-input"
              type="search"
              placeholder="搜索实例、描述或实例ID"
              aria-label="搜索实例"
              @input="onSearchInput"
              @keyup.enter="reload"
              @focus="onSearchFocus"
              @blur="onSearchBlur"
            />
            <div class="search-divider"></div>
            <select
              v-model="filters.environment"
              class="field search-select"
              aria-label="环境筛选"
              @change="onFilterChange"
              @focus="onSearchFocus"
              @blur="onSearchBlur"
            >
              <option value="">全部环境</option>
              <option v-for="opt in environmentOptions" :key="opt.value" :value="opt.value">
                {{ opt.label }}
              </option>
            </select>
            <div class="search-divider"></div>
            <select
              v-model="filters.dbType"
              class="field search-select"
              aria-label="数据库类型筛选"
              @change="onFilterChange"
              @focus="onSearchFocus"
              @blur="onSearchBlur"
            >
              <option value="">全部类型</option>
              <option v-for="opt in dbTypeOptions" :key="opt.value" :value="opt.value">
                {{ opt.label }}
              </option>
            </select>
          </div>

          <!-- 搜索结果面板：完整列出所有同时满足环境、类型、关键字的实例，不做数量截断 -->
          <div v-show="showSuggest" class="suggest-panel">
            <div v-if="!suggestInstances.length" class="suggest-empty">
              <svg width="40" height="40" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                <circle cx="11" cy="11" r="7" stroke="currentColor" stroke-width="1.5" />
                <path d="M20 20l-3.5-3.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
                <path d="M8 11h6" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
              </svg>
              <p>暂无符合条件的数据库实例</p>
              <button class="btn btn-sm btn-primary" type="button" @mousedown.prevent="resetFilters">
                一键重置筛选条件
              </button>
            </div>
            <template v-else>
              <div class="suggest-hint">共 {{ suggestInstances.length }} 个实例</div>
              <button
                v-for="item in suggestInstances"
                :key="item.instance_id"
                type="button"
                class="suggest-row"
                :class="{ active: item.instance_id === selectedId }"
                @mousedown.prevent="selectInstance(item.instance_id)"
              >
                <span class="class-dot" :style="{ background: classDot(item.screen_class) }"></span>
                <span class="suggest-main">
                  <span class="suggest-name">{{ item.name || '—' }}</span>
                  <span class="suggest-sub">
                    {{ item.target || '—' }} · {{ item.db_type || '—' }} · {{ item.environment_name || '—' }}
                  </span>
                </span>
              </button>
            </template>
          </div>
        </div>
        <button class="btn" type="button" :disabled="loading" @click="reload">
          <svg width="13" height="13" viewBox="0 0 16 16" fill="none" aria-hidden="true">
            <path
              d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9M13.5 2v3h-3"
              stroke="currentColor"
              stroke-width="1.4"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
          刷新
        </button>
      </div>
      </template>
    </PageHeader>

    <div class="layout">
      <main>
        <!-- ==================================================================
             第二部分：实例概览（整卡可展开 / 收起）
             收起态：一行摘要 —— 健康分 + 告警数 + 定位信息
                       （运行状态 / 可达性 / 最高等级 pill 已按需求移除，target 上提到标题行）
             展开态：健康信息带（健康分 / 运行状态 / 核心告警，均无分组小标题）+ 基本信息 3 行字段
             每个字段都是后端算好的：有值显示值，没值显示 "—" 并把 reason 挂到 title
             ================================================================== -->
        <section class="card ov-card" :data-collapsed="ovExpanded ? 'false' : 'true'" aria-labelledby="ov-title">
          <div class="ov-head">
            <span class="class-badge" :class="badgeClass">{{ profileHealthClassLabel }}</span>
            <h2 class="instance-title" id="ov-title" :title="profile.name || '—'">
              {{ clipText(profile.name, INSTANCE_NAME_CLIP) || '—' }}
            </h2>
            <span class="instance-meta" :title="profile.target || '—'">
              {{ clipText(profile.target, INSTANCE_ADDR_CLIP) || '—' }}
            </span>
            <!-- 健康分是实例状态的主线指标，放在页首最显眼的位置；状态即由该分数按阈值区间推导 -->
            <div
              class="head-score clickable"
              :class="scoreTone"
              :title="headScoreTitle"
              @click="openAlertsModal"
            >
              <b class="num">{{ healthScoreText }}</b>
              <span class="label">健康分</span>
              <!-- 触底标记：扣分合计已超满分，0 分是截断结果，必须显式说明，否则用户无从判断严重程度 -->
              <span v-if="healthScoreCapped" class="score-floor">超扣 {{ healthScoreOverDeduct }} 分</span>
            </div>
            <!-- 收起态摘要：与标题同一行，紧随健康分之后（告警数 + 类型版本 + 环境 + 运行时长）。
                 展开态不渲染——这些信息在下方健康信息带与基本信息里都有，同一行不再重复。 -->
            <div v-if="!ovExpanded" class="ov-summary">
              <!-- 告警数量区域：点击打开「实例告警明细」弹窗（与该数字同口径的未恢复告警列表） -->
              <span class="pill clickable"
                :class="profile.alert_count ? topAlertPillClass : 'ok'"
                title="点击查看告警明细"
                @click="openAlertsModal"
              >
                {{ profile.alert_count || 0 }} 条告警
              </span>
              <span class="sm-sep"></span>
              <span class="sm-item">{{ profile.db_type || '—' }}<span class="sub">{{ profile.db_version?.value || '' }}</span></span>
              <span class="sm-item">{{ profile.environment_name || '—' }}</span>
              <span class="sm-item">运行 {{ profile.uptime?.value || '—' }}</span>
            </div>
            <div class="ov-actions">
              <button
                class="btn btn-ghost"
                type="button"
                :aria-expanded="String(ovExpanded)"
                aria-controls="ov-detail"
                @click="ovExpanded = !ovExpanded"
              >
                <svg width="11" height="11" viewBox="0 0 12 12" fill="none" aria-hidden="true">
                  <path
                    d="M2.5 4.5L6 8l3.5-3.5"
                    stroke="currentColor"
                    stroke-width="1.4"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                  />
                </svg>
                <span>{{ ovExpanded ? '收起详情' : '展开详情' }}</span>
              </button>
              <button class="btn btn-primary" type="button" :disabled="collecting" @click="collectNow">
                <svg width="13" height="13" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                  <path d="M8.8 1.5L3 9h4l-.8 5.5L12 7H8l.8-5.5z" fill="currentColor" />
                </svg>
                立即采集
              </button>
            </div>
          </div>

          <!-- 展开态：健康信息带 + 基本信息 3 行字段
               信息带只剩「核心告警」一格（健康分格已按需求移除 —— 分数与状态在标题行已有，
               运行状态 / 可达性 / 数据质量格此前也已移除），展开态不再重复同一信息 -->
          <div v-if="ovExpanded" class="ov-detail" id="ov-detail">
            <div class="health-band">
              <!-- 核心告警（无标题）：顶部汇总行 + 全量列表；超过 3 条时列表内部竖向滚动 -->
              <div class="hb-cell">
                <!-- 汇总行：只保留「有 N 条告警，请处理！」，不再按 P1/P2/P3 分级计数；
                     点击打开「实例告警明细」弹窗 -->
                <div class="alert-summary">
                  <span
                    class="pill clickable"
                    :class="profile.alert_count ? 'err' : 'ok'"
                    title="点击查看告警明细"
                    @click="openAlertsModal"
                  >
                    {{ profile.alert_count ? '有 ' + profile.alert_count + ' 条告警，请处理！' : '暂无告警' }}
                  </span>
                </div>
                <ul v-if="profile.alert_briefs?.length" class="alert-mini">
                  <li v-for="(alert, index) in profile.alert_briefs" :key="index">
                    <span class="lv" :class="levelClass(alert.level)">{{ alert.level }}</span>
                    <span class="tt">{{ alertLine(alert) }}</span>
                  </li>
                </ul>
                <p v-else class="hb-note">当前没有待认领告警</p>
              </div>
            </div>

            <!-- 第 1 行：基本信息（末列为「规格」：CPU / 内存 / 磁盘 合并展示） -->
            <div class="ov-row">
              <div class="ov-row-head">
                <svg width="13" height="13" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                  <rect x="2" y="2.5" width="12" height="11" rx="2" stroke="currentColor" stroke-width="1.3" />
                  <path d="M2 6.2h12M6.2 13.5V6.2" stroke="currentColor" stroke-width="1.3" />
                </svg>
                <span>基本信息</span>
              </div>
              <dl class="ov-grid">
                <FieldItem label="实例 ID" :value="profile.instance_id" />
                <FieldItem label="数据库版本类型" :value="dbVersionType" />
                <FieldItem label="操作系统" :field="profile.os_name" />
                <FieldItem label="部署形态" :field="profile.deploy_provider" />
                <FieldItem label="规格" :value="specSummary" />
              </dl>
            </div>

            <!-- 第 2 行：架构（运行时长由原「规格与运行」行并入本行） -->
            <div class="ov-row">
              <div class="ov-row-head">
                <svg width="13" height="13" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                  <rect x="1.8" y="2.2" width="5.4" height="4.4" rx="1" stroke="currentColor" stroke-width="1.3" />
                  <rect x="8.8" y="9.4" width="5.4" height="4.4" rx="1" stroke="currentColor" stroke-width="1.3" />
                  <path d="M4.5 6.6v3.2a1.6 1.6 0 0 0 1.6 1.6h2.7" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
                </svg>
                <span>架构</span>
              </div>
              <dl class="ov-grid">
                <FieldItem label="架构模式" :field="profile.deploy_mode" />
                <FieldItem label="当前角色" :field="profile.role" />
                <FieldItem label="复制拓扑" :field="profile.replica_count" />
                <FieldItem label="高可用模式" :field="profile.ha_mode" />
                <FieldItem label="运行时长" :field="profile.uptime" />
              </dl>
            </div>
          </div>
        </section>

        <!-- ==================================================================
             第三部分：Tab 指标明细（主机层 / 引擎层 / 内核层）
             ================================================================== -->
        <section class="card" aria-label="指标明细">
          <div class="tab-bar">
            <div class="tabs" role="tablist">
              <button
                v-for="tab in tabs"
                :key="tab.key"
                class="tab"
                type="button"
                role="tab"
                :aria-selected="String(activeLayer === tab.key)"
                :aria-controls="'panel-' + tab.key"
                @click="activeLayer = tab.key"
              >
                {{ tab.label }}
                <!-- 指标项数量徽标：按「指标项」计数而非「卡片/图表」计数 ——
                     一张卡片可能承载多条曲线（如「每秒事务数」= xact_commit + deadlocks +
                     xact_rollback + tps），徽标要反映用户真正看到的指标个数；
                     口径 = 该 Tab 当前可见卡片的 series 条数之和，「仅看异常」筛选时同步变化 -->
                <span class="tab-count">{{ metricCountOf(tab.key) }}</span>
              </button>
            </div>
            <!-- 行尾操作区：全屏 + 仅看异常 + 时间范围，与 Tab 右端对齐 -->
            <div class="tab-actions">
              <a-tooltip :title="isFullscreen ? '退出全屏' : '全屏'">
                <button
                  type="button"
                  class="icon-btn"
                  :aria-label="isFullscreen ? '退出全屏' : '全屏'"
                  :aria-pressed="String(isFullscreen)"
                  @click="toggleFullscreen"
                >
                  <!-- 图标随状态切换：全屏态用「向内收拢」，非全屏用「向外展开」 -->
                  <svg
                    v-if="isFullscreen"
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="2"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    aria-hidden="true"
                  >
                    <path d="M9 3v4a2 2 0 0 1-2 2H3" />
                    <path d="M15 3v4a2 2 0 0 0 2 2h4" />
                    <path d="M9 21v-4a2 2 0 0 0-2-2H3" />
                    <path d="M15 21v-4a2 2 0 0 1 2-2h4" />
                  </svg>
                  <svg
                    v-else
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="2"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    aria-hidden="true"
                  >
                    <path d="M3 9V5a2 2 0 0 1 2-2h4" />
                    <path d="M21 9V5a2 2 0 0 0-2-2h-4" />
                    <path d="M3 15v4a2 2 0 0 0 2 2h4" />
                    <path d="M21 15v4a2 2 0 0 1-2 2h-4" />
                  </svg>
                </button>
              </a-tooltip>
              <a-tooltip title="仅看异常">
                <label class="toggle">
                  <input v-model="abnormalOnly" type="checkbox" aria-label="仅看异常" />
                </label>
              </a-tooltip>
              <a-tooltip title="恢复默认顺序">
                <button
                  type="button"
                  class="icon-btn"
                  :disabled="!isCustomSorted(activeLayer)"
                  aria-label="恢复默认顺序"
                  @click="resetOrder(activeLayer)"
                >
                  <svg
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="2"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    aria-hidden="true"
                  >
                    <path d="M3 7v6h6" />
                    <path d="M3.5 13a9 9 0 1 0 2.6-8.5L3 7" />
                  </svg>
                </button>
              </a-tooltip>
              <div class="time-select">
                <select id="time-range" v-model="timeRange" class="field" aria-label="时间范围" @change="onRangeChange">
                  <option value="15m">15分钟</option>
                  <option value="1h">1小时</option>
                  <option value="6h">6小时</option>
                  <option value="24h">1天</option>
                  <option value="7d">7天</option>
                  <option value="custom">自定义</option>
                </select>
                <input
                  class="field time-input"
                  type="datetime-local"
                  step="60"
                  aria-label="开始时间"
                  :readonly="timeRange !== 'custom'"
                  :value="timeRange === 'custom' ? customStart : toDatetimeLocal(historyMeta.start_time)"
                  @input="timeRange === 'custom' ? (customStart = $event.target.value) : null"
                  @change="timeRange === 'custom' ? onCustomTimeChange() : null"
                />
                <span class="time-sep">-</span>
                <input
                  class="field time-input"
                  type="datetime-local"
                  step="60"
                  aria-label="结束时间"
                  :readonly="timeRange !== 'custom'"
                  :value="timeRange === 'custom' ? customEnd : toDatetimeLocal(historyMeta.end_time)"
                  @input="timeRange === 'custom' ? (customEnd = $event.target.value) : null"
                  @change="timeRange === 'custom' ? onCustomTimeChange() : null"
                />
              </div>
            </div>
          </div>

          <!-- 指标放大弹窗：独立时间选择，默认继承外部当前时间范围 -->
          <a-modal
            v-model:open="modalOpen"
            width="960px"
            :footer="null"
            :destroy-on-close="false"
            wrap-class-name="monitor-detail-modal-wrap"
            @cancel="closeModal"
          >
            <template #title>
              <span class="modal-title-wrap">
                {{ modalCard?.name }}
                <MetricHelp v-if="modalCard" :title="modalCard.name" :rows="modalCard.series" />
              </span>
            </template>
            <div class="modal-toolbar">
              <div class="time-select">
                <select
                  id="modal-time-range"
                  v-model="modalTimeRange"
                  class="field"
                  aria-label="时间范围"
                  @change="onModalRangeChange"
                >
                  <option value="15m">15分钟</option>
                  <option value="1h">1小时</option>
                  <option value="6h">6小时</option>
                  <option value="24h">1天</option>
                  <option value="7d">7天</option>
                  <option value="custom">自定义</option>
                </select>
                <input
                  class="field time-input"
                  type="datetime-local"
                  step="60"
                  aria-label="开始时间"
                  :readonly="modalTimeRange !== 'custom'"
                  :value="modalTimeRange === 'custom' ? modalCustomStart : toDatetimeLocal(modalHistoryMeta.start_time)"
                  @input="modalTimeRange === 'custom' ? (modalCustomStart = $event.target.value) : null"
                  @change="modalTimeRange === 'custom' ? onModalCustomTimeChange() : null"
                />
                <span class="time-sep">-</span>
                <input
                  class="field time-input"
                  type="datetime-local"
                  step="60"
                  aria-label="结束时间"
                  :readonly="modalTimeRange !== 'custom'"
                  :value="modalTimeRange === 'custom' ? modalCustomEnd : toDatetimeLocal(modalHistoryMeta.end_time)"
                  @input="modalTimeRange === 'custom' ? (modalCustomEnd = $event.target.value) : null"
                  @change="modalTimeRange === 'custom' ? onModalCustomTimeChange() : null"
                />
              </div>
              <!-- 刷新：同步采集一次并取回最新值，与左侧时间选择右端对齐 -->
              <a-tooltip title="重新采集并刷新最新数据">
                <button
                  type="button"
                  class="icon-btn modal-refresh-btn"
                  :disabled="modalRefreshing"
                  aria-label="刷新"
                  @click="refreshModal"
                >
                  <svg
                    class="refresh-icon"
                    :class="{ spinning: modalRefreshing }"
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="2"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    aria-hidden="true"
                  >
                    <path d="M21 12a9 9 0 1 1-2.6-6.4" />
                    <path d="M21 3v6h-6" />
                  </svg>
                </button>
              </a-tooltip>
            </div>
            <div ref="modalChartEl" class="modal-chart">
              <MetricTrendChart
                v-if="modalCard"
                :series="chartSeriesOf(modalCard, Object.keys(modalHistory).length ? modalHistory : null)"
                :name="modalCard.name"
                :unit="modalCard.series[0]?.unit || ''"
                :width="modalChartWidth"
                :height="320"
                :pad="MODAL_CHART_PAD"
                :fill="false"
                :tone="modalCard ? cardTone(modalCard) : 'default'"
                :time-mode="modalTimeRange === '7d' ? 'date' : 'time'"
                selectable
                @select-range="onModalSelectRange"
              />
              <span v-else class="chart-empty">暂无趋势数据</span>
            </div>
            <!-- 图例与收起卡片完全同款：仅「圆点 + 指标名」居中排列，
                 说明与当前值挪到 title 悬停查看；底部无脚注、无分隔线 -->
            <ul v-if="modalCard && legendRowsOf(modalCard).length" class="modal-legend">
              <li
                v-for="row in legendRowsOf(modalCard)"
                :key="row.key"
                class="ml-item"
                :class="{ off: isSeriesOff(modalCard, row), clickable: modalCard.series.length > 1 }"
                :title="legendItemTitle(modalCard, row)"
                @click="modalCard.series.length > 1 && onLegendItemClick(modalCard, row)"
              >
                <span class="lg-dot" :style="isSeriesOff(modalCard, row) ? {} : { background: row.dotColor || row.color }"></span>
                <span class="ml-name">{{ row.name }}</span>
              </li>
            </ul>
          </a-modal>

          <!-- 排序操作的无障碍播报（键盘排序时不可见但会被读屏播报） -->
          <p class="sr-only" role="status" aria-live="polite">{{ sortMessage }}</p>

          <!-- 面板：三个 Tab 共用一套结构，按 layer 过滤 -->
          <div
            v-for="tab in tabs"
            v-show="activeLayer === tab.key"
            :key="tab.key"
            :id="'panel-' + tab.key"
            class="panel-body"
            role="tabpanel"
            :aria-labelledby="'tab-' + tab.key"
            :ref="(el) => setPanelRef(tab.key, el)"
          >
              <!-- 指标区：整层一个网格。分组语义仍由后端下发并体现在默认顺序里，
                   但排序允许跨组调整，所以不再按组拆成多个网格（组标题已下线，视觉上本就是连续网格） -->
              <div
                class="metric-grid"
                :data-grid-key="tab.key"
                :ref="(el) => setGridRef(tab.key, el)"
                @dragover.prevent="onGridDragOver($event, tab.key)"
                @drop.prevent="onGridDrop($event, tab.key)"
              >
                <article
                  v-for="(card, cardIndex) in orderedOf(tab.key)"
                  :key="card.key"
                  class="metric-card"
                  :class="cardClass(card, tab.key, cardIndex, orderedOf(tab.key).length)"
                  :data-quality="card.quality || (card.series.every((row) => row.value == null) ? 'no_data' : 'ok')"
                  :data-key="card.key"
                  :data-name="(card.name + ' ' + card.key).toLowerCase()"
                >
                  <div class="mc-head">
                    <!-- 排序手柄：鼠标拖拽 + 键盘（回车抓取 / 方向键移动 / Esc 取消） -->
                    <span
                      class="mc-handle"
                      draggable="true"
                      role="button"
                      tabindex="0"
                      :aria-label="`拖拽排序：${card.name}`"
                      title="拖拽排序（也可回车后用方向键移动）"
                      @dragstart="onDragStart($event, tab.key, card.key)"
                      @dragend="onDragEnd"
                      @keydown="onHandleKeydown($event, tab.key, cardIndex)"
                    >
                      <svg viewBox="0 0 24 24" width="12" height="12" fill="currentColor" aria-hidden="true">
                        <circle cx="9" cy="6" r="1.5" />
                        <circle cx="15" cy="6" r="1.5" />
                        <circle cx="9" cy="12" r="1.5" />
                        <circle cx="15" cy="12" r="1.5" />
                        <circle cx="9" cy="18" r="1.5" />
                        <circle cx="15" cy="18" r="1.5" />
                      </svg>
                    </span>
                    <h3 class="mc-name" :title="card.name">{{ card.name }}</h3>
                    <!-- 指标释义：全局统一的「?」入口，弹层列出该指标下所有数值项的 名称/说明/单位 -->
                    <MetricHelp :title="card.name" :rows="card.series" />
                    <button
                      type="button"
                      class="mc-fullscreen"
                      aria-label="全屏查看"
                      title="全屏查看"
                      @click.stop="openModal(card)"
                    >
                      <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <polyline points="15 3 21 3 21 9" />
                        <polyline points="9 21 3 21 3 15" />
                        <line x1="21" y1="3" x2="14" y2="10" />
                        <line x1="3" y1="21" x2="10" y2="14" />
                      </svg>
                    </button>
                  </div>
                  <!-- 趋势图独立成组件：hover 状态内聚，一次鼠标移动只重渲染这一张图 -->
                  <MetricTrendChart
                    class="mc-chart"
                    :series="chartSeriesOf(card)"
                    :name="card.name"
                    :unit="card.series[0]?.unit || ''"
                    :width="cardWidth"
                    :height="86"
                    :tone="cardTone(card)"
                    :time-mode="timeRange === '7d' ? 'date' : 'time'"
                  />
                  <!-- 图例：居中收纳「圆点 + 英文指标名」，一行放不下自动换行；
                       多行时平时只露第一行，悬停整行展开全部（左侧 ▼ 为可展开指示）。
                       旧版「共 N 项 / M 项无数据」脚注已移除，不留空占位 -->
                  <div
                    v-if="legendRowsOf(card).length"
                    class="lg-inline"
                    :class="{ expandable: legendOverflow[card.key], open: legendOpen[card.key] }"
                    :ref="(el) => setLegendEl(card.key, el)"
                    @mouseenter="setLegendOpen(card.key, true)"
                    @mouseleave="setLegendOpen(card.key, false)"
                    @focusin="setLegendOpen(card.key, true)"
                    @focusout="setLegendOpen(card.key, false)"
                  >
                    <!-- 只有内容真的不止一行才给倒三角 + 悬停展开；单行能放下的不加 -->
                    <span v-if="legendOverflow[card.key]" class="lgi-caret" aria-hidden="true">
                      <svg viewBox="0 0 10 6" width="10" height="6" fill="currentColor">
                        <path d="M0 0l5 6 5-6z" />
                      </svg>
                    </span>
                    <span
                      v-for="row in legendRowsOf(card)"
                      :key="row.key"
                      class="lgi-item"
                      :class="{ off: isSeriesOff(card, row), clickable: card.series.length > 1 }"
                      :title="legendItemTitle(card, row)"
                      @click="card.series.length > 1 && onLegendItemClick(card, row)"
                    >
                      <span class="lg-dot" :style="isSeriesOff(card, row) ? {} : { background: row.dotColor || row.color }"></span>
                      <span class="lgi-name">{{ row.name }}</span>
                    </span>
                  </div>
                  <p
                    v-if="cardNote(card)"
                    class="mc-note"
                    :class="{ reason: !card.available, off: card.quality === 'unsupported' }"
                  >
                    {{ cardNote(card) }}
                  </p>
                </article>
              </div>

            <!-- 空状态：该层没有任何监控项（含方言折叠区也为空）时统一提示 -->
            <div
              v-if="!orderedOf(tab.key).length && !unsupportedOf(tab.key).length"
              class="layer-empty"
              role="status"
            >
              <svg class="le-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                <path
                  d="M3 12h4l2-7 4 14 2-7h6"
                  stroke="currentColor"
                  stroke-width="1.6"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                />
              </svg>
              <p class="le-title">{{ layerEmptyMessage(tab.key).title }}</p>
              <p class="le-desc">{{ layerEmptyMessage(tab.key).desc }}</p>
              <button
                v-if="layerEmptyMessage(tab.key).action"
                class="le-btn"
                type="button"
                @click="goTemplateConfig"
              >
                去配置监控模板
              </button>
            </div>

            <!-- 方言不适用：默认折叠，绝不静默缺数 -->
            <div v-if="unsupportedOf(tab.key).length" class="dialect-group">
              <button
                class="dialect-toggle"
                type="button"
                :aria-expanded="String(dialectOpen)"
                @click="dialectOpen = !dialectOpen"
              >
                <svg width="11" height="11" viewBox="0 0 12 12" fill="none" aria-hidden="true">
                  <path
                    d="M4.5 2.5L8 6l-3.5 3.5"
                    stroke="currentColor"
                    stroke-width="1.4"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                  />
                </svg>
                方言不适用指标：当前实例为 {{ dbTypeLabel }}，以下指标语义不适用
                <span class="count">点开展开查看原因</span>
              </button>
              <div class="dialect-body" :class="{ open: dialectOpen }">
                <div class="metric-grid">
                  <article
                    v-for="card in unsupportedOf(tab.key)"
                    :key="card.key"
                    class="metric-card"
                    :data-quality="card.quality"
                  >
                    <div class="mc-head">
                      <h3 class="mc-name">{{ card.display_name }}</h3>
                    </div>
                    <div class="mc-foot">
                      <span class="mc-key">{{ card.key }}</span>
                      <span class="mc-th">{{ card.threshold }}</span>
                    </div>
                    <p v-if="card.reason" class="mc-note off">{{ card.reason }}</p>
                  </article>
                </div>
              </div>
            </div>
          </div>

          <p v-if="!filteredCards.length" class="empty">没有匹配的指标，换个关键字试试</p>
        </section>
      </main>
    </div>

    <!-- 实例告警明细弹窗（共用组件）：三个入口复用同一份实现 ——
         页首「N 条告警」数量区、页首健康分、展开态健康分格。
         点击任一一处打开的弹窗列、数据、过滤与排序完全一致。 -->
    <InstanceAlertsModal
      v-model:open="alertsModalOpen"
      :instance-id="selectedId"
      :instance-name="profile?.name || selectedId || ''"
      :score-capped="healthScoreCapped"
      :score-over-deduct="healthScoreOverDeduct"
      :score-deduct-total="healthScoreDeductTotal"
    />
  </div>
</template>

<script setup>
import { computed, inject, nextTick, onActivated, onBeforeUnmount, onDeactivated, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { message } from 'ant-design-vue'
import {
  collectMonitorInstanceApi,
  getMonitorDetailApi,
  getMonitorMetricHistoryApi,
  refreshMonitorDetailApi,
} from '@/api/monitor'
import FieldItem from './components/FieldItem.vue'
import MetricTrendChart from '../components/MetricTrendChart.vue'
import MetricHelp from '../components/MetricHelp.vue'
import InstanceAlertsModal from '../components/InstanceAlertsModal.vue'
import PageHeader from '../components/PageHeader.vue'
import { fmtByteAuto, toBytes, unitDim, unitRateSuffix } from '../components/chartUnits'
import { curveColorAt, toneColorAt } from './detailTheme'
import { CLASS_DOT_COLOR, operatorTextInText } from '../utils'
import { clipText, INSTANCE_ADDR_CLIP, INSTANCE_NAME_CLIP } from '@/utils/text'
// 监控模块的数据源白名单：类型筛选下拉也走同一份，避免两页各写一套
import { filterMonitorDbTypeOptions } from '../monitorDbTypes'

// ---------------------------------------------------------------------------
// 数据：全部来自后端，前端不持有任何静态指标定义
// ---------------------------------------------------------------------------
const loading = ref(false)
const collecting = ref(false)
const detail = ref(null)
const history = ref({}) // metric_key -> [{ ts, value }]

const filters = reactive({ search: '', environment: '', dbType: '' })
const route = useRoute()
// 深链入口：大屏/列表「查看详情」会带 ?instance_id=xxx 跳转。
// 此前这里从不读 query，深链参数被静默丢弃，页面总落在默认实例上。
const selectedId = ref(String(route.query.instance_id || ''))
const ovExpanded = ref(false)
// 展开态：概览卡按内容撑开（CSS 同步放开内部限高），此时改为整页滚动；
// 收起态：恢复「页面不滚、只有 Tab 内容区滚」的原机制。
// 与 CSS 的 .monitor-detail.is-expanded 成对出现，两处必须一起改。
watch(ovExpanded, (open) => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(!open)
  }
})
const abnormalOnly = ref(false)
const timeRange = ref('15m')
const customStart = ref('')
const customEnd = ref('')
const historyMeta = ref({ start_time: '', end_time: '' })
const dialectOpen = ref(false)
const showSuggest = ref(false)
const searchFocused = ref(false)
const searchInput = ref(null)

// 容器宽度：用于决定网格列数与图表 viewBox 宽度（指标不再分页，全量渲染）
const gridWidths = reactive({})
let observer = null

// 未收录数据库类型的兜底 Tab（与后端 MonitorDisplayLayoutFor 返回 null 时对应）
const DEFAULT_TABS = [
  { key: 'host', label: '基础指标' },
  { key: 'engine', label: '数据库指标' },
  { key: 'kernel', label: '引擎指标' },
]
// Tab 由后端展示目录下发：MySQL 三个、PostgreSQL 两个，前端不再写死数量与名称。
// 注意：tabs 依赖下面的 selected（computed），必须定义在 selected 之后，否则
// watch 在 setup 期立即求值会触发 TDZ ReferenceError，整页白屏。
const activeLayer = ref('host')

// 本页自己管滚动：关掉布局壳（.layout-content）的纵向滚动，只让 Tab 内容区滚。
// 与备份恢复 / 审计日志等页面同一套机制，离开页面时必须还原，否则会影响其它页面。
const setLayoutContentNoScroll = inject('setLayoutContentNoScroll', null)

// ---------------------------------------------------------------------------
// 派生数据
// ---------------------------------------------------------------------------
const selected = computed(() => detail.value?.selected || null)

// Tab 由后端展示目录下发（依赖 selected，必须在其后定义）；未收录类型回落默认三 Tab。
const tabs = computed(() => {
  const list = selected.value?.display?.tabs
  if (Array.isArray(list) && list.length) {
    return list.map((tab) => ({ key: tab.key, label: tab.label }))
  }
  return DEFAULT_TABS
})
// 切换实例类型后 Tab 数量可能变化（MySQL 3 → PG 2），当前 Tab 不存在时回落到第一个。
// immediate：后端只会下发"有监控项"的 Tab，若当前 Tab 因迁移/裁剪被移除，
// 首帧就该切到第一个可用 Tab，否则所有面板 v-show 都不命中，内容区会是一片空白。
watch(
  tabs,
  (list) => {
    if (!list.some((tab) => tab.key === activeLayer.value)) {
      activeLayer.value = list[0]?.key || 'host'
    }
  },
  { immediate: true }
)

const instance = computed(() => selected.value?.instance || {})
const profile = computed(() => selected.value?.profile || {})
const metrics = computed(() => instance.value.metrics || [])
// 「结论」块已从展开态移除，这里不再派生 conclusion 文本
const instances = computed(() => detail.value?.instances || [])

const environmentOptions = computed(() => detail.value?.filters?.environments || [])
// 顶部「数据库类型」筛选：后端会带出实例里实际存在的类型，但监控只有
// MySQL / PostgreSQL 两套采集实现，其余类型选了也拿不到指标，这里按白名单收窄。
const dbTypeOptions = computed(() =>
  filterMonitorDbTypeOptions(
    (detail.value?.filters?.db_types || []).map((item) => ({ label: item.label, value: item.value })),
  ),
)
// 兜底：下拉收窄后若当前值不在可选范围内，清空，避免筛出"暂无实例"却看不到原因。
watch(dbTypeOptions, (options) => {
  if (!filters.dbType) return
  if (options.some((item) => item.value === filters.dbType)) return
  filters.dbType = ''
})
const dbTypeLabel = computed(() => instance.value.db_type || '当前实例')

// 复合搜索：环境 + 类型 + 关键字同时命中，完整返回不截断。
// 数据量大时仍是 O(n) 字符串匹配，instances 来自后端全量返回（detail 接口）。
const suggestInstances = computed(() => {
  const text = filters.search.trim().toLowerCase()
  const env = filters.environment
  const dbType = filters.dbType
  return instances.value.filter((item) => {
    if (env && item.environment_name !== env) return false
    if (dbType && item.db_type !== dbType) return false
    if (!text) return true
    const hay = `${item.name || ''} ${item.target || ''} ${item.instance_id || ''} ${
      item.db_type || ''
    } ${item.environment_name || ''}`.toLowerCase()
    return hay.includes(text)
  })
})

// 运行状态 / 可达性 pill 与「数据质量」文案已随展开态对应格一并移除（同一信息不再重复展示），
// statusText / statusPillClass / reachablePillClass / qualityText 四个 computed 随之删除。
const profileHealthClassLabel = computed(() => profile.value.health_score?.class_label || '未采集')
const badgeClass = computed(() => {
  const key = profile.value.health_score?.class_key || profile.value.screen_class
  // 注意：risk 不能映射成 ''——空串是假值，会被 || 兜底成 uncollected（灰色），
  // 这正是「风险实例显示为灰色」的根因；risk 必须有自己的档位类。
  return { healthy: 'healthy', risk: 'risk', fault: 'fault', uncollected: 'uncollected' }[key] || 'uncollected'
})
const scoreTone = computed(() => {
  const key = profile.value.health_score?.class_key
  if (key === 'healthy') return 'tone-ok'
  if (key === 'risk') return 'tone-risk'
  return 'tone-fault'
})
// 告警行文案：等级走独立的 lv 徽标，这里拼「指标名 + 当前值 + 阈值 + 已持续时长」。
// message 由后端按"%s 当前值 %s，阈值 %s %s"生成，已带单位格式化，前端不再二次拼数值，
// 避免与告警中心 / 事件列表的口径不一致；比较符在展示层统一转中文（>= → 大于等于）。
const alertLine = (alert) => {
  const body = operatorTextInText(alert?.message) || alert?.metric_label || '暂无告警描述'
  const duration = alert?.duration ? `。已持续 ${alert.duration}` : ''
  return `${body}${duration}。`
}
// 健康分「扣分明细」已从展开态移除，这里不再派生 deductions

// ---- 健康分下限兜底（扣分合计超满分时按 0 展示）----
// 口径在后端：HealthScoreInfo.score 已做 0 分截断，deduct_total / capped / over_deduct 说明扣了多少。
// 这里再兜一层，是防"接口来自旧版本服务或数据异常时把负数渲染出来"——
// 负分对运维没有任何解释力，只会让人怀疑系统算错了。
const HEALTH_SCORE_FULL = 100
const healthScoreRaw = computed(() => {
  const info = profile.value.health_score
  if (!info || info.available === false) return null
  const value = Number(info.score)
  return Number.isFinite(value) ? value : null
})
// 展示值：取不到显示「—」，负数一律按 0 展示
const healthScoreText = computed(() => {
  const value = healthScoreRaw.value
  if (value == null) return '—'
  return String(Math.max(0, value))
})
// 扣分合计：优先用后端下发的 deduct_total；老服务没这个字段时由 deductions 自行求和
const healthScoreDeductTotal = computed(() => {
  const info = profile.value.health_score
  if (!info) return 0
  const total = Number(info.deduct_total)
  if (Number.isFinite(total) && total > 0) return total
  const list = Array.isArray(info.deductions) ? info.deductions : []
  return list.reduce((acc, item) => acc + (Number(item?.score) || 0), 0)
})
// 是否触底：后端 capped 为准；字段缺失时用「扣分合计 > 满分」自行判定
const healthScoreCapped = computed(() => {
  const info = profile.value.health_score
  if (!info || info.available === false) return false
  if (info.capped === true) return true
  return healthScoreDeductTotal.value > HEALTH_SCORE_FULL
})
// 超扣幅度：触底时 > 0，未触底恒为 0（界面据此显示「超扣 N 分」）
const healthScoreOverDeduct = computed(() => {
  if (!healthScoreCapped.value) return 0
  const over = Number(profile.value.health_score?.over_deduct)
  if (Number.isFinite(over) && over > 0) return over
  return healthScoreDeductTotal.value - HEALTH_SCORE_FULL
})
// 健康分悬浮说明：触底时把"为什么是 0 分"直接讲清楚
const headScoreTitle = computed(() => {
  const base = `${profileHealthClassLabel.value} · 点击查看扣分明细与告警`
  if (!healthScoreCapped.value) return base
  return `${base}（实际扣分 ${healthScoreDeductTotal.value} 分，已超出满分，健康分按最低分 0 展示）`
})

// 基本信息「数据库版本类型」：按「类型 版本」格式合并，例如 MySQL 8.0.43
// 规格（基本信息行第 5 列）：CPU / 内存 / 磁盘 合并成一段紧凑文本，例如 4C 12GB 98.8GB。
// 口径（与需求一致）：CPU 取核数加 C；内存向上取整（11.3 → 12GB）；
// 磁盘四舍五入保留 1 位小数（98.82 → 98.8GB）；三段之间以空格分隔。
// 取不到的项直接跳过，不编造 0；三项都取不到时交给 FieldItem 显示「—」并挂原因。
const specSummary = computed(() => {
  const parts = []
  const cpu = profile.value.cpu_cores
  if (cpu?.available && Number(cpu.num) > 0) parts.push(`${Number(cpu.num)}C`)
  const mem = profile.value.memory_gb
  if (mem?.available && Number(mem.num) > 0) parts.push(`${Math.ceil(Number(mem.num))}GB`)
  const disk = profile.value.disk_gb
  if (disk?.available && Number(disk.num) > 0) parts.push(`${Number(disk.num).toFixed(1)}GB`)
  return parts.join(' ')
})

const dbVersionType = computed(() => {
  const type = profile.value.db_type || ''
  const ver = profile.value.db_version?.value || ''
  if (!type && !ver) return '—'
  return `${type} ${ver}`.trim()
})

// 「N 条告警」pill 使用最高等级颜色
const topAlertPillClass = computed(() => {
  const lvl = profile.value.alert_top_level
  if (lvl === 'P1') return 'p1'
  if (lvl === 'P2') return 'p2'
  if (lvl === 'P3') return 'p3'
  return 'ok'
})

// 命中待处理告警的指标键：用于给卡片打「关注」标记（后端质量枚举里没有 warn，
// "正在告警"是另一种状态，必须靠待处理告警反查，不能让前端凭阈值自己猜）。
const alertingKeys = computed(() => {
  const set = new Set()
  ;(instance.value.alerts || []).forEach((alert) => {
    if (alert.metric_key) set.add(alert.metric_key)
  })
  return set
})

// ---- 卡片视图模型 ----
// 两种来源统一成同一种结构：
//  1) 展示目录模式（后端下发 selected.display）：一个「监控项」= 一张卡片，
//     卡片内每个 series 是一根曲线（如「网络流量」= input_traffic + output_traffic）；
//  2) 兜底模式（未收录的数据库类型）：一个采集指标 = 一张卡片、一根曲线。
// 模板只认这一种结构，两种来源的差异到此为止。
// 多条曲线时的区分方式：**同一色相（系统主色调）的不同明度档位**，
// 色值不写在这里 —— 由 CSS 主题 token 用 color-mix() 从主色调现算（见文件末主题块），
// 这里只引用变量名。这样主题切换时曲线、图例、边框会一起变，不会漏。
//
// 单序列曲线的实际描边色：与 MetricTrendChart 的 tone-* 样式一一对应，
// 与图例圆点 / Tooltip 圆点共用同一份 token 引用，保证"圆点一个色、曲线另一个色"
// 这种错位不会发生。
function toneStroke(tone) {
  return toneColorAt(tone)
}

// 无采集源的指标（附件里有、平台没采）的统一样式：曲线为空、值显示 —
const NO_SOURCE_REASON = '当前平台未采集该指标（业务口径已定义，暂无采集源）'

const metricByKey = computed(() => {
  const map = {}
  metrics.value.forEach((metric) => {
    if (metric && metric.key) map[metric.key] = metric
  })
  return map
})

// 采集值 → 展示值的换算：只在量纲一致、进制不同时使用（如 B/s → KB）
function scaleOf(series) {
  const num = Number(series?.scale)
  return Number.isFinite(num) && num > 0 ? num : 1
}

function scaledPoints(metricKey, scale, source) {
  if (!metricKey) return EMPTY_POINTS
  const raw = (source || history.value)[metricKey]
  if (!raw || !raw.length) return EMPTY_POINTS
  if (scale === 1) return raw
  return raw.map((point) => ({
    ts: point.ts,
    value: point.value == null ? null : Number(point.value) * scale,
  }))
}

function buildLayoutCard(item) {
  const series = (item.series || []).map((row, index) => {
    const metricKey = row.metric_key || ''
    const scale = scaleOf(row)
    const points = scaledPoints(metricKey, scale)
    const metric = metricKey ? metricByKey.value[metricKey] : null
    let value = null
    let reason = ''
    if (!metricKey) {
      reason = NO_SOURCE_REASON
    } else if (row.not_applicable) {
      // 角色不适用（如单机实例上的「备库 APPLY 延迟」）：条目保留、不显示假数据，
      // 直接把后端算好的原因呈现出来 —— 与「模板未勾选」「采集失败」区分开。
      reason = row.not_applicable_reason || '当前实例角色下该指标不适用'
    } else if (metric) {
      // 质量 != ok 时绝不显示 0：沿用"没采到就是没采到"的硬规则
      value = metric.available ? Number(metric.value) * scale : null
      reason = metric.available ? '' : `数据不可用：${metric.reason || '未采集到有效值'}`
    } else if (points.length) {
      const last = points[points.length - 1]
      value = last && last.value != null ? last.value : null
    } else {
      reason = '未纳入当前采集模板或暂无数据'
    }
    return {
      key: `${item.key}#${row.key || index}`,
      name: row.key || item.name,
      definition: row.definition || '',
      unit: row.unit || '',
      metricKey,
      points,
      value,
      reason,
      // 曲线描边：单序列沿用 tone 的 CSS 配色（currentColor = 图表不覆写描边，
      // 保证面积图透明度等既有表现不变），多序列才用调色板区分曲线。
      color:
        (item.series || []).length === 1 ? 'currentColor' : curveColorAt(index),
      // 图例圆点色：必须单独给"具体色值"（currentColor 落在图例文字色上会显示成灰黑），
      // 与曲线实际描边色同源 —— 单序列取 tone 色，多序列取调色板色（见下方回填）。
      dotColor: '',
    }
  })
  const card = {
    key: item.key,
    name: item.name,
    layer: item.tab,
    order: item.order || 0,
    mode: 'layout',
    series,
  }
  // 圆点色回填：单序列跟随整卡 tone（= 曲线描边色），多序列沿用曲线自身的调色板色
  const tone = cardTone(card)
  series.forEach((row, index) => {
    row.dotColor =
      series.length === 1 ? toneStroke(tone) : curveColorAt(index)
  })
  return card
}

function buildLegacyCard(card) {
  const built = {
    key: card.key,
    name: card.display_name || card.key,
    layer: card.layer,
    order: card.order || 999,
    mode: 'metric',
    quality: card.quality,
    available: card.available,
    reason: card.reason,
    threshold: card.threshold,
    unit: card.unit,
    group: card.group,
    series: [
      {
        key: card.key,
        name: card.display_name || card.key,
        definition: card.advice || card.threshold || '',
        unit: card.unit || '',
        metricKey: card.key,
        points: history.value[card.key] || EMPTY_POINTS,
        value: card.available ? card.value : null,
        reason: card.reason || '',
        // 曲线描边沿用 tone 的 CSS 配色，与改造前完全一致
        color: 'currentColor',
        // 图例圆点色：取曲线实际描边色（tone 档位），避免显示成文字灰
        dotColor: '',
      },
    ],
  }
  built.series[0].dotColor = toneStroke(cardTone(built))
  return built
}

// 全部卡片：展示目录优先，未收录类型回落到字典推导
const allCards = computed(() => {
  const layout = selected.value?.display
  if (layout && Array.isArray(layout.items) && layout.items.length) {
    return layout.items
      .slice()
      .sort((a, b) => (a.order || 0) - (b.order || 0))
      .map(buildLayoutCard)
  }
  return metrics.value.filter((card) => card.quality !== 'unsupported').map(buildLegacyCard)
})

// ---- 指标过滤：仅看异常（关键字搜索已下线，DataSource 不变） ----
const ABNORMAL = ['warn', 'partial', 'unsupported', 'error', 'no_data', 'disabled']

// 异常判定：任一序列取不到值，或任一序列命中待处理告警
function isAbnormalCard(card) {
  if (card.mode === 'layout') {
    return card.series.some(
      (row) => row.value == null || (row.metricKey && alertingKeys.value.has(row.metricKey))
    )
  }
  return ABNORMAL.includes(card.quality) || alertingKeys.value.has(card.key)
}

const filteredCards = computed(() => {
  if (!abnormalOnly.value) return allCards.value
  return allCards.value.filter(isAbnormalCard)
})

function cardsOfLayer(layer) {
  return filteredCards.value.filter((card) => card.layer === layer)
}
function unsupportedOf(layer) {
  // 展示目录模式下，目录本身就是最终展示口径，不再额外挂"方言不适用"折叠区
  if (selected.value?.display) return []
  return metrics.value.filter((card) => card.layer === layer && card.quality === 'unsupported')
}

// 默认顺序：展示目录模式直接按目录 order；兜底模式先按分组再按组内 order
function groupsOf(layer) {
  const cards = cardsOfLayer(layer)
  const map = new Map()
  cards.forEach((card) => {
    const name = card.group || '其他'
    if (!map.has(name)) map.set(name, [])
    map.get(name).push(card)
  })
  return Array.from(map.entries())
    .map(([name, list]) => ({
      key: `${layer}:${name}`,
      name,
      cards: list.slice().sort((a, b) => (a.order || 999) - (b.order || 999)),
    }))
    .sort((a, b) => (a.cards[0]?.order || 999) - (b.cards[0]?.order || 999))
}

// ---------------------------------------------------------------------------
// 空状态：某个 Tab 一个监控项都没有时，说明原因并给下一步指引。
// 三个 Tab 复用同一套文案结构与样式，理由按层区分（依据后端下发的模板信息）。
// ---------------------------------------------------------------------------
const router = useRouter()

function layerEmptyMessage(layer) {
  const label = (tabs.value.find((tab) => tab.key === layer) || {}).label || '监控项'
  // 展示目录模式：目录里这一 Tab 本来就没有监控项（如 PostgreSQL 的基础指标），
  // 这不是采集故障，而是"按该类型的业务口径，这里没有要展示的项"。
  if (selected.value?.display) {
    const dbType = instance.value.db_type || '当前类型'
    return {
      title: `暂无${label}`,
      desc: `按 ${dbType} 的指标口径，「${label}」下没有需要展示的监控项。`,
      action: null,
    }
  }
  if (layer === 'host') {
    // 主机层来自 Prometheus、不受模板约束：为空只可能是采集源没数据。
    // 必须区分两种「没数据」，否则排查方向会被带偏：
    //   ① 没登记采集目标 → 压根没有采集源，先去实例配置里登记；
    //   ② 登记了但拿不到 → node_exporter 挂了，或 Prometheus 里该 target 的
    //      instance 标签与实例登记地址不一致（标签对不上是静默失败，最常见）。
    // 旧文案一律写成「未获取到数据，请检查 Prometheus 采集配置」，
    // 把「没登记」也说成「采集故障」—— 类型未收录回落默认 Tab 时尤其误导。
    const target = (selected.value?.prometheus_target || '').trim()
    return {
      title: `暂无${label}`,
      desc: target
        ? `主机层由 Prometheus + node_exporter 采集。该实例已登记采集目标「${target}」，但当前没有数据——请检查 node_exporter 是否存活，以及 Prometheus 中该 target 的 instance 标签是否与实例登记地址一致。`
        : '主机层由 Prometheus + node_exporter 采集。该实例尚未登记 Prometheus 采集目标，因此没有采集源，请先在实例配置中登记采集目标。',
      action: '/monitor/config',
    }
  }
  const template = selected.value?.template
  if (!template || !template.name) {
    return {
      title: `暂无${label}`,
      desc: '该实例尚未绑定监控模板，此层的监控项未纳入采集。请先在「监控配置 → 指标模板」创建模板，并在实例配置中绑定。',
      action: '/monitor/config',
    }
  }
  const name = template.display_name || template.name
  return {
    title: `暂无${label}`,
    desc: `当前生效的模板「${name}」没有勾选此层的监控项，系统按模板口径展示与采集。请到「监控配置 → 指标模板」勾选后再采集。`,
    action: '/monitor/config',
  }
}

function goTemplateConfig() {
  router.push('/monitor/config')
}

// ---------------------------------------------------------------------------
// 网格测量：容器宽度决定列数（≥900px 三列，否则两列）与图表 viewBox 宽度
// ---------------------------------------------------------------------------
const gridEls = {}
function setGridRef(key, el) {
  if (el) gridEls[key] = el
  else delete gridEls[key]
}
const panelEls = {}
function setPanelRef(key, el) {
  if (el) panelEls[key] = el
}

// ---------------------------------------------------------------------------
// 指标卡自定义排序
// 设计要点：
//  1. 顺序按 Tab 独立保存（host / engine / kernel 三个数组），互不影响；
//  2. 保存的是「指标 key 的有序数组」，新增指标自动排在末尾、删除/禁用指标自动失效，
//     因此后端增减指标不会残留无效项或造成顺序错乱；
//  3. 只在用户主动操作（拖拽 / 键盘 / 恢复默认）时写盘，加载时仅做内存裁剪；
//  4. 全部是 O(n) 的字符串比较，单次排序 n≈指标数（几十条），不会成为渲染瓶颈。
// ---------------------------------------------------------------------------
const CARD_ORDER_KEY = 'goinsight.monitor.cardOrder.v1'

function loadCardOrder() {
  const initial = { host: [], engine: [], kernel: [] }
  try {
    const raw = localStorage.getItem(CARD_ORDER_KEY)
    if (!raw) return initial
    const parsed = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object') return initial
    tabs.value.forEach((tab) => {
      const list = parsed[tab.key]
      // 只接受字符串数组，脏数据直接丢弃而不是让后续排序崩掉
      if (Array.isArray(list)) initial[tab.key] = list.filter((key) => typeof key === 'string')
    })
  } catch {
    // 隐私模式下 localStorage 不可用 / 数据被外部改坏：静默退回默认顺序
  }
  return initial
}

function persistCardOrder() {
  try {
    localStorage.setItem(CARD_ORDER_KEY, JSON.stringify(cardOrder))
  } catch {
    // 超配额等情况忽略排序持久化，不影响主流程
  }
}

const cardOrder = reactive(loadCardOrder())

// 默认顺序：分组（后端下发，按首项 order 排）→ 组内 order
function naturalCardsOf(layer) {
  if (selected.value?.display) {
    // 展示目录模式：顺序完全由后端目录 order 决定
    return cardsOfLayer(layer)
      .slice()
      .sort((a, b) => (a.order || 0) - (b.order || 0))
  }
  return groupsOf(layer).flatMap((group) => group.cards)
}

// 有效顺序 = 保存顺序 ∩ 当前指标，未记录的（新增）指标保持后端默认相对顺序排在末尾。
// Array.prototype.sort 是稳定排序，rank 相同的元素维持 naturalCardsOf 的原次序。
function effectiveOrder(layer) {
  const list = naturalCardsOf(layer)
  const saved = cardOrder[layer] || []
  if (!saved.length) return list
  const rank = new Map()
  saved.forEach((key, index) => {
    if (!rank.has(key)) rank.set(key, index) // 重复 key 只认第一次出现
  })
  const LAST = Number.MAX_SAFE_INTEGER
  return list.slice().sort((a, b) => (rank.get(a.key) ?? LAST) - (rank.get(b.key) ?? LAST))
}

// 三个面板都用 v-show 常驻，模板里每帧都会取一次；做成 computed 只算一遍
const orderedByLayer = computed(() => {
  const result = {}
  tabs.value.forEach((tab) => {
    result[tab.key] = effectiveOrder(tab.key)
  })
  return result
})

function orderedOf(layer) {
  return orderedByLayer.value[layer] || []
}

// Tab 徽标口径：指标项数 = 该层可见卡片承载的曲线（series）条数之和。
// 不直接用卡片数，是因为一个监控项可能按多指标并行采集（「网络流量」两条、
// 「每秒事务数」四条），展示给用户的应该是可读的数据项数量。
// 与 orderedOf 同源（都基于 filteredCards），「仅看异常」过滤后两处一起收敛。
const metricCountByLayer = computed(() => {
  const result = {}
  tabs.value.forEach((tab) => {
    result[tab.key] = (orderedByLayer.value[tab.key] || []).reduce(
      (sum, card) => sum + (card.series?.length || 0),
      0
    )
  })
  return result
})

function metricCountOf(layer) {
  return metricCountByLayer.value[layer] || 0
}

function isCustomSorted(layer) {
  return !!(cardOrder[layer] && cardOrder[layer].length)
}

// 数据刷新后清理失效项。注意：仅当还有有效项时才落盘——
// 否则用户切到另一种数据库类型的实例（指标 key 完全不同）会把已保存的顺序清空。
function pruneCardOrder() {
  let changed = false
  tabs.value.forEach((tab) => {
    const saved = cardOrder[tab.key] || []
    if (!saved.length) return
    const valid = new Set(allCards.value.filter((card) => card.layer === tab.key).map((card) => card.key))
    const kept = []
    const seen = new Set()
    saved.forEach((key) => {
      if (!seen.has(key) && valid.has(key)) {
        seen.add(key)
        kept.push(key)
      }
    })
    if (kept.length === saved.length) return
    if (kept.length) {
      cardOrder[tab.key] = kept
      changed = true
    }
  })
  if (changed) persistCardOrder()
}

// 把「插入到第 index 个可见卡片之前」落成一份全量顺序并持久化。
// 以当前全部指标为基准重建，天然吸收新增项、剔除已删除项。
function commitOrder(layer, key, index) {
  const visible = orderedOf(layer)
  const beforeKey = index >= 0 && index < visible.length ? visible[index].key : null
  if (beforeKey === key) return // 原地放下

  const allKeys = allCards.value.filter((card) => card.layer === layer).map((card) => card.key)
  const valid = new Set(allKeys)
  let keys = (cardOrder[layer] || []).filter((k) => valid.has(k))
  const seen = new Set(keys)
  allKeys.forEach((k) => {
    if (!seen.has(k)) {
      seen.add(k)
      keys.push(k)
    }
  })

  keys = keys.filter((k) => k !== key)
  const pos = beforeKey ? keys.indexOf(beforeKey) : -1
  if (pos < 0) keys.push(key)
  else keys.splice(pos, 0, key)

  cardOrder[layer] = keys
  persistCardOrder()
}

function resetOrder(layer) {
  cardOrder[layer] = []
  persistCardOrder()
  grabbedKey.value = ''
  grabSnapshot = null
  sortMessage.value = '已恢复默认顺序'
}

// ---- 鼠标拖拽 ----
const dragKey = ref('')
const dragLayer = ref('')
const dropIndex = ref(-1)
let dragNodes = [] // 拖拽开始时缓存卡片节点，避免 dragover 里高频查询 DOM

function onDragStart(event, layer, key) {
  dragLayer.value = layer
  dragKey.value = key
  dropIndex.value = -1
  const grid = gridEls[layer]
  dragNodes = grid ? Array.from(grid.querySelectorAll('.metric-card')) : []
  const card = event.target && event.target.closest ? event.target.closest('.metric-card') : null
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    // Firefox 不 setData 不会启动拖拽
    event.dataTransfer.setData('text/plain', key)
    // 用整张卡片做拖拽影像，而不是只有手柄那一个小点
    if (card && event.dataTransfer.setDragImage) event.dataTransfer.setDragImage(card, 24, 20)
  }
}

function onGridDragOver(event, layer) {
  if (!dragKey.value || dragLayer.value !== layer) return
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
  const total = dragNodes.length
  if (!total) return
  const target = event.target && event.target.closest ? event.target.closest('.metric-card') : null
  let next = total // 落在网格空白处 → 追加到末尾
  if (target) {
    const index = dragNodes.indexOf(target)
    if (index >= 0) {
      const rect = target.getBoundingClientRect()
      // 以卡片水平中线为界，决定插在它前面还是后面（网格是横向排列，比上下更直观）
      next = event.clientX > rect.left + rect.width / 2 ? index + 1 : index
    }
  }
  if (next !== dropIndex.value) dropIndex.value = next
}

function onGridDrop(event, layer) {
  if (!dragKey.value || dragLayer.value !== layer) {
    resetDrag()
    return
  }
  commitOrder(layer, dragKey.value, dropIndex.value)
  resetDrag()
}

function onDragEnd() {
  resetDrag()
}

function resetDrag() {
  dragKey.value = ''
  dragLayer.value = ''
  dropIndex.value = -1
  dragNodes = []
}

// ---- 键盘排序：回车抓取 → 方向键移动 → 回车确认 / Esc 取消 ----
const grabbedKey = ref('')
const sortMessage = ref('')
let grabSnapshot = null

function onHandleKeydown(event, layer, index) {
  const visible = orderedOf(layer)
  const card = visible[index]
  if (!card) return
  const key = event.key

  if (key === 'Enter' || key === ' ' || key === 'Spacebar') {
    event.preventDefault()
    if (grabbedKey.value === card.key) {
      grabbedKey.value = ''
      grabSnapshot = null
      sortMessage.value = `已放下 ${card.name}`
    } else {
      grabbedKey.value = card.key
      grabSnapshot = (cardOrder[layer] || []).slice()
      sortMessage.value = `已抓取 ${card.name}，方向键移动，回车确认，Esc 取消`
    }
    return
  }

  if (grabbedKey.value !== card.key) return

  if (key === 'Escape') {
    event.preventDefault()
    if (grabSnapshot) cardOrder[layer] = grabSnapshot
    persistCardOrder()
    grabbedKey.value = ''
    grabSnapshot = null
    sortMessage.value = '已取消排序调整'
    return
  }

  const delta =
    key === 'ArrowLeft' || key === 'ArrowUp' ? -1 : key === 'ArrowRight' || key === 'ArrowDown' ? 1 : 0
  if (!delta) return
  event.preventDefault()
  // commitOrder 的语义是「插入到第 N 个之前」，所以要越过下一个元素才是 index + 2
  const target = Math.max(0, Math.min(visible.length, delta < 0 ? index - 1 : index + 2))
  const before = target < visible.length ? visible[target].key : null
  if (before === card.key) return
  commitOrder(layer, card.key, target)
  const pos = Math.min(Math.max(target, 1), visible.length)
  sortMessage.value = `${card.name} 已移动到第 ${pos} 位`
  // 卡片 DOM 节点随 key 复用被移动，焦点需要重新落回手柄
  nextTick(() => focusHandle(layer, card.key))
}

function focusHandle(layer, key) {
  const grid = gridEls[layer]
  if (!grid) return
  const card = Array.from(grid.querySelectorAll('.metric-card')).find((el) => el.dataset.key === key)
  const handle = card && card.querySelector('.mc-handle')
  if (handle) handle.focus()
}

// 卡片状态类：告警态 + 拖拽/键盘/放置位置三种视觉反馈
function cardClass(card, layer, index, total) {
  return {
    alerting: isAlerting(card),
    'mc-dragging': dragKey.value === card.key,
    'mc-grabbed': grabbedKey.value === card.key,
    'mc-drop-before': dragLayer.value === layer && dropIndex.value === index,
    'mc-drop-after': dragLayer.value === layer && dropIndex.value === total && index === total - 1,
  }
}

// 用容器宽度决定列数：与 demo 的 @container 查询保持同一判据
// 图表 viewBox 宽度取单个卡片宽度，保证曲线比例与 demo 一致
//
// 两个防抖措施（否则拖窗口时每张图的坐标/刻度/路径都会重算，直接掉帧）：
//  1. 8px 死区：宽度小幅抖动不重算；
//  2. rAF 合并：一帧内的多次 resize 回调只算一次。
const WIDTH_DEADZONE = 8
function measureGrids() {
  Object.entries(gridEls).forEach(([key, el]) => {
    if (el && el.clientWidth) gridWidths[key] = el.clientWidth
  })
  const first = Object.values(gridEls).find((el) => el && el.clientWidth)
  if (first) {
    const width = first.clientWidth
    const cols = width >= 900 ? 3 : 2
    const next = Math.max(200, (width - 14 * (cols - 1)) / cols)
    if (Math.abs(next - cardWidth.value) >= WIDTH_DEADZONE) cardWidth.value = next
  }
  measureLegends()
}

// 图例是否真的超过一行：只有换行了才加倒三角并支持悬停展开，
// 单行能放下的卡片保持干净（判断方式：临时套单行高度，overflow:hidden 下
// scrollHeight 仍是完整内容高度，与 clientHeight 一比即知是否溢出）。
const legendEls = new Map()
const legendOverflow = ref({})
// 展开/收起改 JS 事件驱动：纯 CSS :hover 在「布局位移 / 页面滚动 / 测量瞬间塌缩」
// 场景下命中判定会与视觉状态脱节（表现为鼠标移出后不回收）；mouseenter/mouseleave
// 是明确的事件边界，状态一定收敛，focusin/focusout 保留键盘可达性。
const legendOpen = ref({})
function setLegendOpen(key, open) {
  legendOpen.value[key] = open
}
// 兜底对账：滚动/布局变化后，若某个「展开中」的图例实际已不在鼠标下方且无焦点，
// 强制收回（覆盖滚轮滚动时鼠标不动、部分浏览器不派发 boundary 事件的场景）。
function pruneLegendOpen() {
  const openKeys = Object.keys(legendOpen.value).filter((key) => legendOpen.value[key])
  if (!openKeys.length) return
  openKeys.forEach((key) => {
    const el = legendEls.get(key)
    if (!el) return
    let hovered = false
    try {
      hovered = el.matches(':hover')
    } catch {
      hovered = false
    }
    const focused = el.contains(document.activeElement)
    if (!hovered && !focused) legendOpen.value[key] = false
  })
}
function setLegendEl(key, el) {
  if (el) legendEls.set(key, el)
  else {
    legendEls.delete(key)
    delete legendOpen.value[key]
  }
}
function measureLegends() {
  const next = {}
  legendEls.forEach((el, key) => {
    if (!el) return
    // 展开中的图例跳过测量：套 .measuring 会瞬间塌缩，可能把 hover 事件状态搅乱
    if (el.classList.contains('open')) {
      next[key] = true
      return
    }
    el.classList.add('measuring')
    next[key] = el.scrollHeight > el.clientHeight + 1
    el.classList.remove('measuring')
  })
  const prev = legendOverflow.value
  const keys = Object.keys(next)
  // 结果没变就不换引用：避免 ResizeObserver → 重渲染 → 尺寸变化 的回环抖动
  const changed =
    keys.length !== Object.keys(prev).length || keys.some((key) => next[key] !== prev[key])
  if (changed) legendOverflow.value = next
}
// 卡片集合变化（数据到达 / 切 Tab / 只看异常）后元素会重建，需重新测量
watch([filteredCards, activeLayer], () => nextTick(scheduleMeasure))

let measureRaf = 0
function scheduleMeasure() {
  if (measureRaf) return
  measureRaf = requestAnimationFrame(() => {
    measureRaf = 0
    measureGrids()
  })
}
function setupObserver() {
  if (typeof ResizeObserver === 'undefined') return
  observer = new ResizeObserver(() => scheduleMeasure())
  Object.values(gridEls).forEach((el) => observer.observe(el))
}
function resetObserver() {
  if (observer) {
    observer.disconnect()
    observer = null
  }
}

// ---------------------------------------------------------------------------
// 趋势图：绘制与交互全部下沉到 MetricTrendChart 子组件，
// 父层只负责喂 points / 宽度 / 配色档位（避免 hover 触发整页重渲染）
// ---------------------------------------------------------------------------
const MODAL_CHART_PAD = { left: 44, right: 22, top: 10, bottom: 28 }
// 卡片宽度（响应式，由容器实测得出）：作为 SVG viewBox 宽度，曲线比例与 demo 一致
const cardWidth = ref(300)
// 空序列用同一个常量引用：否则父组件每次重渲染都会造出新数组，
// 子组件会误判为"数据变了"而空跑一次补间。
const EMPTY_POINTS = []

// ---------------------------------------------------------------------------
// 展示辅助
// ---------------------------------------------------------------------------
function isAlerting(card) {
  if (card.mode === 'layout') {
    return card.series.some((row) => row.metricKey && alertingKeys.value.has(row.metricKey))
  }
  return alertingKeys.value.has(card.key) || card.quality === 'error'
}
// 卡片底部说明：只在"取不到值"时给出原因，其余情况不渲染，避免空占位。
// 展示目录模式下取第一条取不到值的序列的原因（无采集源 / 未纳入模板 / 质量不可用）。
function cardNote(card) {
  if (card.mode === 'layout') {
    const missing = card.series.find((row) => row.value == null)
    return missing ? String(missing.reason || '').trim() : ''
  }
  return card.available ? '' : String(card.reason || '').trim()
}
// 曲线配色档位：告警/采集中异常/方言不适用各自成色，与卡片状态一致
function cardTone(card) {
  if (card.mode === 'layout') {
    if (isAlerting(card)) return 'alert'
    // 全序列都取不到值（含无采集源）时降为灰色，一眼能看出"这张卡没数据"
    return card.series.every((row) => row.value == null) ? 'muted' : 'default'
  }
  if (card.quality === 'error') return 'error'
  if (isAlerting(card)) return 'alert'
  if (card.quality === 'unsupported') return 'muted'
  return 'default'
}

// ---- 多曲线：一个监控项几行指标就有几根线，顺序与图例均为附件表格顺序 ----
// unit 随序列下发给图表：双轴分配与 Tooltip 单位格式化都依赖它
//
// ---- 图例点击切换显隐（Grafana 风格）----
// hiddenSeries[seriesKey]=true 表示该曲线被隐藏；key 以卡片为命名空间，
// 同名指标在不同卡片互不影响。切换实例后选择失去意义，自动清空。
const hiddenSeries = ref({})

// 切换实例后图例显隐选择失去意义，全部恢复显示
watch(instance, () => {
  hiddenSeries.value = {}
})

function seriesKeyOf(card, row) {
  return `${card.key}::${row.metricKey || row.name}`
}

function isSeriesOff(card, row) {
  return !!hiddenSeries.value[seriesKeyOf(card, row)]
}

// 该卡片当前可见的序列数（隐藏后至少保留一条）
function visibleSeriesCount(card) {
  return card.series.filter((row) => !isSeriesOff(card, row)).length
}

function toggleSeries(card, row) {
  const key = seriesKeyOf(card, row)
  if (!hiddenSeries.value[key] && visibleSeriesCount(card) <= 1) return // 最后一条不可隐藏
  if (hiddenSeries.value[key]) delete hiddenSeries.value[key]
  else hiddenSeries.value[key] = true
  // 触发响应式更新（delete 不触发 Vue3 ref 对象的依赖？——直接替换对象最稳）
  hiddenSeries.value = { ...hiddenSeries.value }
}

// 双击（或快速二次点击）：仅显示该指标；若已处于独占状态则恢复全部
function soloSeries(card, row) {
  const soloing = card.series.every((r) => (r === row ? !isSeriesOff(card, r) : isSeriesOff(card, r)))
  const next = {}
  if (!soloing) {
    card.series.forEach((r) => {
      if (r !== row) next[seriesKeyOf(card, r)] = true
    })
  }
  hiddenSeries.value = next
}

// 单击切显隐、双击独占：用 220ms 计时器区分，避免 dblclick 前两次 click 把状态翻转两次
const legendClickTimers = new Map()

function onLegendItemClick(card, row) {
  const key = seriesKeyOf(card, row)
  if (legendClickTimers.has(key)) {
    clearTimeout(legendClickTimers.get(key))
    legendClickTimers.delete(key)
    soloSeries(card, row)
    return
  }
  legendClickTimers.set(
    key,
    setTimeout(() => {
      legendClickTimers.delete(key)
      toggleSeries(card, row)
    }, 220),
  )
}

function chartSeriesOf(card, source) {
  if (!card) return []
  return card.series
    .filter((row) => !isSeriesOff(card, row))
    .map((row) => ({
      name: row.name,
      color: row.color,
      unit: row.unit || '',
      points: source ? scaledPoints(row.metricKey, scaleOf(row), source) : row.points,
    }))
}

// 图例悬停提示：多序列说明交互，单序列保留指标说明
function legendItemTitle(card, row) {
  if ((card.series || []).length <= 1) return row.definition || row.name
  const base = row.valueText ? `${row.name} ${row.valueText}${row.unitText || ''}` : row.definition || row.name
  return isSeriesOff(card, row)
    ? `${base}（已隐藏，点击恢复）`
    : `${base}（点击隐藏，双击仅显示该指标）`
}
// 图例：每个指标一行（仅作曲线识别：颜色圆圈 + 指标名）。
// valueText/unitText 仍计算 —— 放大弹窗的图例继续展示完整信息。
function legendRowsOf(card) {
  if (!card || card.mode !== 'layout') return []
  return card.series.map((row) => {
    const isByte = unitDim(row.unit) === 'bytes'
    // 字节量纲：value 已乘过 Scale（如 KB），先还原成原始字节再自动换算。
    // 直接拿 value 过 fmtByteAuto 会显示得比真实值小 1024^n 倍 —— 与 Tooltip 同源。
    const byteText = isByte ? fmtByteAuto(toBytes(row.value, row.unit)) : ''
    return {
      ...row,
      valueText: row.value == null ? '—' : isByte ? byteText : fmtValue(row.value),
      unitText: isByte ? unitRateSuffix(row.unit) : row.unit || '',
    }
  })
}

function fmtValue(value) {
  if (value == null || Number.isNaN(Number(value))) return '—'
  const num = Number(value)
  const abs = Math.abs(num)
  const digits = abs >= 100 ? 0 : abs >= 1 ? 2 : 4
  return num.toLocaleString('zh-CN', { maximumFractionDigits: digits })
}
// 旧版弹窗脚注（footLeft/footRight：「共 N 项 / 指标键 / 阈值」）已随 modal-foot 一并移除，
// 底部信息由居中的 modal-legend 承担，口径与收起卡片一致。

// ---------------------------------------------------------------------------
// 指标放大弹窗：独立时间状态，默认继承外部当前范围
// ---------------------------------------------------------------------------
// 弹窗宽度固定 960px，但图表 viewBox 宽度用实测值：viewBox 与容器 1:1 时
// preserveAspectRatio="none" 不会拉伸坐标轴文字（用固定 900 会横向拉歪刻度）。
const MODAL_CHART_WIDTH = 912
const modalChartEl = ref(null)
const modalChartWidth = ref(MODAL_CHART_WIDTH)
const modalOpen = ref(false)

function measureModalChart() {
  if (modalChartEl.value?.clientWidth) modalChartWidth.value = modalChartEl.value.clientWidth
}

watch(modalOpen, async (open) => {
  if (!open) return
  await nextTick()
  measureModalChart()
})
const modalCard = ref(null)
const modalTimeRange = ref('15m')
const modalCustomStart = ref('')
const modalCustomEnd = ref('')
const modalHistoryMeta = ref({ start_time: '', end_time: '' })
const modalHistory = ref({})
const modalRefreshing = ref(false)

async function openModal(card) {
  modalCard.value = card
  modalTimeRange.value = timeRange.value
  modalCustomStart.value = customStart.value
  modalCustomEnd.value = customEnd.value
  modalHistoryMeta.value = { ...historyMeta.value }
  modalHistory.value = {}
  modalOpen.value = true
  await loadModalHistory()
}

function closeModal() {
  modalOpen.value = false
  modalCard.value = null
}

async function loadModalHistory() {
  if (!selectedId.value || !modalCard.value) return
  try {
    const params = { instance_id: selectedId.value }
    if (modalTimeRange.value === 'custom') {
      params.range = 'custom'
      params.start_time = formatForApi(modalCustomStart.value)
      params.end_time = formatForApi(modalCustomEnd.value)
    } else {
      params.range = modalTimeRange.value
    }
    const res = await getMonitorMetricHistoryApi(params)
    const payload = res?.data ?? res
    const map = {}
    ;(payload?.series || []).forEach((item) => {
      map[item.key] = item.points || []
    })
    modalHistory.value = map
    modalHistoryMeta.value = {
      start_time: payload?.start_time || '',
      end_time: payload?.end_time || '',
    }
  } catch {
    modalHistory.value = {}
  }
}

function onModalRangeChange() {
  if (modalTimeRange.value === 'custom') {
    const end = new Date()
    const start = new Date(end.getTime() - 15 * 60 * 1000)
    modalCustomStart.value = formatForDatetimeLocal(start)
    modalCustomEnd.value = formatForDatetimeLocal(end)
  }
  loadModalHistory()
}

function onModalCustomTimeChange() {
  if (!modalCustomStart.value || !modalCustomEnd.value) return
  if (new Date(modalCustomStart.value) >= new Date(modalCustomEnd.value)) {
    message.warning('开始时间必须早于结束时间')
    return
  }
  loadModalHistory()
}

// 弹窗图拖拽选区松手：把选中的时间区间落成"自定义"范围并立即查询。
// 起止来自真实采样点的时间戳（组件内吸附），格式已是 yyyy-mm-dd hh:mm:ss。
function onModalSelectRange({ startTs, endTs }) {
  if (!startTs || !endTs) return
  modalTimeRange.value = 'custom'
  modalCustomStart.value = toDatetimeLocal(startTs)
  modalCustomEnd.value = toDatetimeLocal(endTs)
  loadModalHistory()
}

// ---------------------------------------------------------------------------
// 弹窗刷新：同步采集一次 → 合并最新指标值 → 重取趋势数据
// ---------------------------------------------------------------------------

// 刷新按钮：调 /monitor/detail/refresh（后端同步采集并落库，返回全部指标最新值），
// 前端把返回值合并进 instance.metrics（卡片 computed 的数据源），再把 modalCard
// 重新指向计算出的新卡片对象 —— 弹窗与背后列表的值同步更新；最后重取趋势。
async function refreshModal() {
  if (!selectedId.value || !modalCard.value || modalRefreshing.value) return
  modalRefreshing.value = true
  const cardKey = modalCard.value.key
  try {
    const res = await refreshMonitorDetailApi(selectedId.value)
    const payload = res?.data ?? res
    mergeFreshMetrics(payload?.metrics || [])
    // 指标值合并后 allCards 会重算，把弹窗指向同 key 的新卡片对象（值/色调/图例随之更新）
    const freshCard = allCards.value.find((card) => card.key === cardKey)
    if (freshCard) modalCard.value = freshCard
    await loadModalHistory()
    if (payload?.quality === 'ok') {
      message.success('已刷新至最新数据')
    } else {
      message.warning(payload?.status_message || '刷新完成，但部分指标不可用')
    }
  } catch (error) {
    message.error(typeof error === 'string' ? error : error?.message || '刷新失败，请检查实例连通性')
  } finally {
    modalRefreshing.value = false
  }
}

// 把同步采集返回的最新指标合并进 instance.metrics（按 key 覆盖、新 key 追加）。
// 注意质量硬规则：available=false 时前端一律按 null 处理，接口值不可直接采信。
function mergeFreshMetrics(freshList) {
  if (!instance.value || !Array.isArray(freshList) || !freshList.length) return
  const current = Array.isArray(instance.value.metrics) ? instance.value.metrics : []
  const indexByKey = new Map()
  current.forEach((metric, i) => {
    if (metric && metric.key) indexByKey.set(metric.key, i)
  })
  const next = current.slice()
  freshList.forEach((fresh) => {
    if (!fresh || !fresh.key) return
    const idx = indexByKey.get(fresh.key)
    if (idx === undefined) {
      next.push(fresh)
      indexByKey.set(fresh.key, next.length - 1)
    } else {
      next[idx] = { ...next[idx], ...fresh }
    }
  })
  instance.value = { ...instance.value, metrics: next }
}

function levelClass(level) {
  return { P1: 'lv-p1', P2: 'lv-p2', P3: 'lv-p3' }[level] || 'lv-p3'
}
function classDot(screenClass) {
  return CLASS_DOT_COLOR[screenClass] || CLASS_DOT_COLOR.uncollected
}

// ---------------------------------------------------------------------------
// 数据加载
// ---------------------------------------------------------------------------
async function loadDetail() {
  loading.value = true
  try {
    // 实例列表筛选改为前端复合搜索完成，请求后端时只保留选中实例，
    // 避免环境/类型/关键字参数把 instances 过滤成子集。
    const params = {}
    if (selectedId.value) params.instance_id = selectedId.value
    const res = await getMonitorDetailApi(params)
    detail.value = res?.data ?? res
    const id = detail.value?.selected?.instance?.instance_id
    if (id) selectedId.value = id
    // 指标集合可能随模板/方言变化，先按当前数据裁剪一次自定义顺序
    pruneCardOrder()
    await loadHistory()
  } catch (error) {
    message.error(typeof error === 'string' ? error : error?.message || '加载监控明细失败')
  } finally {
    loading.value = false
    await nextTick()
    measureGrids()
    resetObserver()
    setupObserver()
  }
}

async function loadHistory() {
  if (!selectedId.value) return
  try {
    const params = { instance_id: selectedId.value }
    if (timeRange.value === 'custom') {
      params.range = 'custom'
      params.start_time = formatForApi(customStart.value)
      params.end_time = formatForApi(customEnd.value)
    } else {
      params.range = timeRange.value
    }
    const res = await getMonitorMetricHistoryApi(params)
    const payload = res?.data ?? res
    const map = {}
    ;(payload?.series || []).forEach((item) => {
      map[item.key] = item.points || []
    })
    history.value = map
    historyMeta.value = {
      start_time: payload?.start_time || '',
      end_time: payload?.end_time || '',
    }
  } catch {
    history.value = {}
  }
}

function onRangeChange() {
  // 时间范围变化：重新取数，趋势图随之重绘
  if (timeRange.value === 'custom') {
    // 切换到自定义时，默认填充当前时刻往前 15 分钟作为初始区间
    const end = new Date()
    const start = new Date(end.getTime() - 15 * 60 * 1000)
    customStart.value = formatForDatetimeLocal(start)
    customEnd.value = formatForDatetimeLocal(end)
  }
  loadHistory()
}

function onCustomTimeChange() {
  if (!customStart.value || !customEnd.value) return
  if (new Date(customStart.value) >= new Date(customEnd.value)) {
    message.warning('开始时间必须早于结束时间')
    return
  }
  loadHistory()
}

// datetime-local (YYYY-MM-DDTHH:mm) -> API (YYYY-MM-DD HH:mm:ss)
function formatForApi(raw) {
  if (!raw) return ''
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return raw
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:00`
}

function formatForDatetimeLocal(date) {
  const pad = (n) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function toDatetimeLocal(raw) {
  if (!raw) return ''
  const d = new Date(raw.replace(/\s/, 'T'))
  if (Number.isNaN(d.getTime())) return ''
  return formatForDatetimeLocal(d)
}

function reload() {
  loadDetail()
}

function selectInstance(instanceId) {
  selectedId.value = instanceId
  showSuggest.value = false
  loadDetail()
}

function onSearchFocus() {
  searchFocused.value = true
  showSuggest.value = true
}

function onSearchInput() {
  showSuggest.value = true
}

function onFilterChange() {
  showSuggest.value = true
}

function onSearchBlur(e) {
  // 焦点仍在复合搜索控件或结果面板内时不关闭，避免输入框→下拉→结果项之间切换时闪烁
  const related = e?.relatedTarget
  if (related && (related.closest('.compound-search') || related.closest('.suggest-panel'))) {
    return
  }
  searchFocused.value = false
  // 延迟关闭，给点击建议项留出时间
  setTimeout(() => {
    showSuggest.value = false
  }, 150)
}

function resetFilters() {
  filters.search = ''
  filters.environment = ''
  filters.dbType = ''
  showSuggest.value = true
  nextTick(() => {
    searchInput.value?.focus()
  })
}

async function collectNow() {
  if (!selectedId.value) return
  collecting.value = true
  try {
    await collectMonitorInstanceApi(selectedId.value)
    message.success('采集完成')
    await loadDetail()
  } catch (error) {
    message.error(typeof error === 'string' ? error : error?.message || '采集失败')
  } finally {
    collecting.value = false
  }
}

// 实例告警明细弹窗已抽成共用组件 InstanceAlertsModal（结构与取数只有一份）。
// 这里只保留一个开关状态：页首告警数量、页首健康分、展开态健康分三处入口共用。
const alertsModalOpen = ref(false)

function openAlertsModal() {
  if (!selectedId.value) return
  // 复位 + 取数都在组件内部完成（watch open），这里只负责打开
  alertsModalOpen.value = true
}

// ---------------------------------------------------------------------------
// 全屏：页面内沉浸模式（不调用浏览器原生 Fullscreen API）
//
// 为什么不走原生全屏：原生全屏会把整个浏览器窗口置为全屏并弹出「按 esc 退出」提示，
// 用户要的是「页面铺满浏览器可视区」——地址栏/标签栏照旧存在，不进入系统级全屏。
// 因此这里只做布局层切换：由布局壳把顶部 App 栏、公告条与左侧导航一起收掉，
// 内容区自动铺满整屏，退出时全部还原。浏览器 UI 完全不动。
// ---------------------------------------------------------------------------
const isFullscreen = ref(false)
const setPageImmersive = inject('setPageImmersive', null)

function enterImmersive() {
  if (isFullscreen.value) return
  setPageImmersive?.(true)
  isFullscreen.value = true
  // 宽度与高度都变了，卡片列数、图例位置都要重算（window.resize 不触发，显式补一次）
  nextTick(() => {
    measureGrids()
    resetObserver()
    setupObserver()
  })
}

function exitImmersive() {
  if (!isFullscreen.value) return
  isFullscreen.value = false
  setPageImmersive?.(false)
  nextTick(() => {
    measureGrids()
    resetObserver()
    setupObserver()
  })
}

function toggleFullscreen() {
  if (isFullscreen.value) exitImmersive()
  else enterImmersive()
}

// 沉浸模式下没有浏览器的 Esc 兜底，自己接：Esc 退出，与原生全屏的手感一致
function onImmersiveKeydown(event) {
  if (event.key === 'Escape' && isFullscreen.value) {
    event.preventDefault()
    exitImmersive()
  }
}

// 切换 Tab / 过滤条件后重新测量容器宽度。
// 同时把该 Tab 内容区的滚动位置复位到顶部 —— 内容区现在是唯一滚动容器，
// 不复位的话从「引擎指标」滚到底再切到「基础指标」会停在半空。
watch(activeLayer, async (key) => {
  await nextTick()
  measureGrids()
  const el = panelEls[key]
  if (el) el.scrollTop = 0
})
// 仅看异常变化后重新测量容器宽度
watch(abnormalOnly, () => {
  nextTick(() => {
    measureGrids()
    resetObserver()
    setupObserver()
  })
})

onMounted(() => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(true)
  }
  loadDetail()
  window.addEventListener('resize', measureGrids)
  // 捕获阶段监听所有滚动容器（页面主体 + 内部滚动区），滚动后对账图例展开状态
  window.addEventListener('scroll', pruneLegendOpen, { capture: true, passive: true })
  // 沉浸模式用 Esc 退出（页面内全屏没有浏览器的原生 Esc 兜底）
  window.addEventListener('keydown', onImmersiveKeydown)
})
onBeforeUnmount(() => {
  if (typeof setLayoutContentNoScroll === 'function') {
    setLayoutContentNoScroll(false)
  }
  window.removeEventListener('resize', measureGrids)
  window.removeEventListener('scroll', pruneLegendOpen, { capture: true })
  window.removeEventListener('keydown', onImmersiveKeydown)
  // 离开本页时收起沉浸模式，避免把「无顶部栏/无侧栏」的布局留给其它页面
  if (isFullscreen.value) {
    isFullscreen.value = false
    setPageImmersive?.(false)
  }
  resetObserver()
})
// 页面被 keep-alive 缓存时切走不触发 unmount：这里同样收起沉浸模式，
// 否则「无顶部栏」的布局会带到下一个页面
onDeactivated(() => {
  if (isFullscreen.value) exitImmersive()
})
onActivated(() => {
  // 回到本页时图标态已由 exitImmersive 复位，这里只需重测一次卡片宽度
  nextTick(measureGrids)
})
</script>

<style scoped>
/* 主题 token 全部抽到文件末尾的非 scoped 块（选择器 .monitor-detail /
   .monitor-detail-modal-wrap），这里只保留页面自身的布局与尺寸。
   原因：放大弹窗被 a-modal teleport 到 body，脱离本元素，token 必须挂在
   一个同时覆盖两者的作用域上，弹窗里的曲线 / 图例 / 文字才能取到颜色。 */
.monitor-detail {
  display: flex;
  flex-direction: column;
  /* 顶部（工具栏 PageHeader 与概览卡之间）的距离按需求减半：12px → 6px。
     仍由 token 派生而不是写死 6px，后续统一调间距时本页跟着走。 */
  gap: calc(var(--gi-spacing-ssm) / 2);
  /* 本页自己管滚动（脚本里已通过 setLayoutContentNoScroll 关掉布局壳的纵向滚动）：
     根容器撑满可视高度并禁止自身滚动，滚动只发生在 Tab 内容区 .panel-body 内，
     页面头部 / 筛选 / Tab 标签栏因此保持固定，不会出现外层与内层双重滚动条。 */
  height: 100%;
  min-height: 0;
  overflow: hidden;
  /* 容器宽度与四边间距统一交给布局壳 .layout-content（padding 24px，≤1023px 收窄为 16px），
     本页不再自设。原实现里 padding: 12px 20px 12px + max-width: 1560px + margin: -12px auto 0
     是三处硬编码偏差：左右比其它页面各少 4px、底部少 12px、宽屏下还被限宽居中，
     与「监控大屏 / 告警中心 / 告警配置 / 监控配置」的视觉规格对不上。 */
  font-family: 'PingFang SC', 'Noto Sans SC', 'Segoe UI', 'Microsoft YaHei UI', sans-serif;
  font-size: var(--font-body);
  color: var(--gi-text-1);
  background: var(--gi-color-page-bg);
}
/* 展开态只放开「Tab 内容区（指标明细）」的内嵌滚动：让它按内容自然撑开，
   超出可视高度时由页面（.layout-content）整页滚动，脚本里 watch ovExpanded 同步
   setLayoutContentNoScroll(false)。

   上方「健康信息栏」一带的滚动处理：
    - .alert-mini：3 行可视高度 + overflow-y:auto（告警列表区内部滚动）—— 各状态保持不变
    - .card.ov-card：收起态仍是 max-height:52vh 限高滚动；展开态放开固定限高，
      内容随屏幕自适应完整展示，纵向滚动交给页面整页滚动（见下方覆盖规则）
   收起态本就不命中 .is-expanded，机制不变。 */
.monitor-detail.is-expanded {
  height: auto;
  min-height: 100%;
  overflow: visible;
}
.monitor-detail.is-expanded .card.ov-card {
  /* 展开态去掉概览卡的固定 52vh 限高：不再出现卡内长滚动条，
     内容按屏幕自适应完整展示；横向仍由 .monitor-detail 裁剪，不出现横向滚动条 */
  max-height: none;
  overflow: visible;
}
/* Tab 卡片与内容区：展开态不再吃掉剩余高度、不再内嵌滚动，内容完整展示 */
.monitor-detail.is-expanded .card:not(.ov-card) {
  flex: none;
  overflow: visible;
}
.monitor-detail.is-expanded .panel-body {
  flex: none;
  overflow: visible;
}
/* 注意：这里刻意不覆盖 .alert-mini —— 告警列表的 3 行区内滚动在展开态同样保持原实现；
   .card.ov-card 的限高已在上方展开态规则中放开（长滚动条只在收起态可能出现）。 */

*,
*::before,
*::after {
  box-sizing: border-box;
}
h1,
h2,
h3,
h4,
p,
dl,
dd,
dt {
  margin: 0;
}
button {
  font: inherit;
  color: inherit;
}

/* ---------- 第一部分：顶部工具栏（PageHeader 组件统一渲染） ---------- */
.toolbar-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.field {
  height: 32px;
  padding: 0 10px;
  font-size: 13px;
  color: var(--gi-text-1);
  background: var(--gi-color-container-bg);
  border: 1px solid var(--gi-color-border);
  border-radius: var(--gi-radius-sm);
  outline: none;
  transition: border-color 0.2s, box-shadow 0.2s;
}
.field:hover {
  border-color: var(--gi-primary-border-soft);
}
.field:focus {
  border-color: var(--gi-primary);
  box-shadow: 0 0 0 2px var(--gi-primary-ring);
}
.search-wrap {
  position: relative;
}

/* 复合搜索控件：输入框 + 环境下拉 + 类型下拉，共用一根外边框。
   之前用 inset box-shadow 模拟边框，在部分高分屏 / 子像素对齐场景下顶边仍会出现
   1px 渲染不完整的情况；改回普通 border 并配合上方 .monitor-detail 的 padding-top
   调整（12px），让组件顶边落在 layout-content-inner 内容盒内，避免被裁。 */
.compound-search {
  display: inline-flex;
  align-items: center;
  height: 32px;
  background: var(--gi-color-container-bg);
  border: 1px solid var(--gi-color-border);
  border-radius: var(--gi-radius-sm);
  box-shadow: none;
  transition: border-color 0.2s, box-shadow 0.2s;
}
.compound-search.focused,
.compound-search:focus-within {
  border-color: var(--gi-primary);
  box-shadow: 0 0 0 2px var(--gi-primary-ring);
}
.compound-search .search-icon {
  flex: none;
  margin: 0 8px 0 10px;
  color: var(--gi-muted);
}
.compound-search .field {
  height: 30px;
  padding: 0 8px;
  font-size: 13px;
  color: var(--gi-text-1);
  background: transparent;
  border: 0;
  border-radius: 0;
  outline: none;
  box-shadow: none;
}
.compound-search .field:hover,
.compound-search .field:focus {
  border-color: transparent;
  box-shadow: none;
}
.compound-search .search-input {
  width: 200px;
  padding-left: 0;
}
.compound-search .search-select {
  width: 100px;
  cursor: pointer;
}
.compound-search .search-divider {
  flex: none;
  width: 1px;
  height: 18px;
  background: var(--gi-color-border-soft);
}

select.field {
  padding-right: 26px;
  cursor: pointer;
  appearance: none;
  background-image: url("data:image/svg+xml;charset=utf8,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12'%3E%3Cpath d='M2.5 4.5L6 8l3.5-3.5' fill='none' stroke='%239aa8b8' stroke-width='1.4' stroke-linecap='round'/%3E%3C/svg%3E");
  background-repeat: no-repeat;
  background-position: right 8px center;
}

/* 搜索建议面板：完整列出所有匹配实例，高度用滚动承载而非数量截断 */
.suggest-panel {
  position: absolute;
  z-index: 20;
  top: 36px;
  left: 0;
  width: 320px;
  max-height: min(480px, 60vh);
  overflow-y: auto;
  padding: 4px;
  background: var(--gi-color-container-bg);
  border: 1px solid var(--gi-color-border);
  border-radius: var(--gi-radius-md);
  box-shadow: var(--gi-shadow-md);
}
.suggest-hint {
  padding: 6px 8px 4px;
  font-size: 12px;
  color: var(--gi-text-3);
}
.suggest-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 8px;
  font-size: 13px;
  text-align: left;
  background: transparent;
  border: 0;
  border-radius: var(--gi-radius-sm);
  cursor: pointer;
}
.suggest-row:hover {
  background: var(--gi-primary-weak);
}
.suggest-row.active {
  background: var(--gi-primary-weak);
}
.class-dot {
  flex: none;
  width: 7px;
  height: 7px;
  border-radius: 50%;
}
.suggest-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.suggest-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.suggest-sub {
  font-size: 11px;
  color: var(--gi-text-3);
}

/* 空状态 */
.suggest-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 28px 16px;
  text-align: center;
  color: var(--gi-text-3);
}
.suggest-empty svg {
  color: var(--gi-muted);
}
.suggest-empty p {
  margin: 0;
  font-size: 13px;
}

/* 小号按钮 */
.btn-sm {
  height: 28px;
  padding: 0 12px;
  font-size: 12px;
}

.btn {
  height: 32px;
  padding: 0 14px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  border-radius: var(--gi-radius-sm);
  border: 1px solid var(--gi-color-border);
  background: var(--gi-color-container-bg);
  cursor: pointer;
  transition: all 0.2s;
}
.btn:hover {
  border-color: var(--gi-primary);
  color: var(--gi-primary);
}
.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
.btn-primary {
  border-color: var(--gi-primary);
  background: var(--gi-primary);
  color: var(--gi-color-container-bg);
}
.btn-primary:hover {
  background: var(--gi-primary-hover);
  border-color: var(--gi-primary-hover);
  color: var(--gi-color-container-bg);
}

/* ---------- 布局 ---------- */
/* 高度链：.monitor-detail → .layout → main → Tab 卡片 → .panel-body。
   每一层都必须 min-height: 0，否则 flex 子项会被内容撑高，滚动条又跑回外层。 */
.layout {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.layout > main {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.card {
  background: var(--gi-color-container-bg);
  border: 1px solid var(--gi-color-border-soft);
  border-radius: var(--gi-radius-card);
  box-shadow: var(--gi-shadow-sm);
}
.card + .card {
  margin-top: 12px;
}
/* 实例概览卡（含上方「健康信息栏」）：按内容自然高度，不参与剩余空间分配；
   内容超限时给一个上限并允许自身滚动 —— 这是健康信息栏的滚动条，
   展开态也保持不变（.monitor-detail.is-expanded 不覆盖本规则）。 */
.card.ov-card {
  flex: none;
  max-height: 52vh;
  /* 纵向超限时才滚动；横向一律裁剪，避免多出一条横向滚动条 */
  overflow: hidden auto;
}
/* Tab 卡片：吃掉剩余可视高度；它自己不滚，滚动交给内部的 .panel-body */
.card:not(.ov-card) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* ---------- 第二部分：实例概览 ---------- */
.ov-head {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  padding: 14px 16px 10px;
}
/* 收起态：摘要已并入本行，本行即卡片唯一内容行 —— 底部留白与顶部对齐，
   避免"上松下紧"（展开态下方还有信息带，仍用 10px 收紧间距）。 */
.ov-card[data-collapsed='true'] .ov-head {
  padding-bottom: 14px;
}
/* 标题行的健康分块：页首最显眼位置，大号数字 + 状态色。
   色调类复用 scoreTone（tone-ok / tone-risk / tone-fault） */
.head-score {
  display: inline-flex;
  align-items: baseline;
  gap: 4px;
  margin-left: 6px;
  padding: 2px 12px;
  border-radius: 10px;
  font-weight: 700;
}
.head-score .num {
  font-size: 26px;
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
}
.head-score .label {
  font-size: 12px;
  font-weight: 500;
  color: var(--gi-text-2, #5a6a7a);
}
.head-score.tone-ok {
  color: #0f9f6f;
  background: rgba(15, 159, 111, 0.1);
}
.head-score.tone-risk {
  color: #d46b08;
  background: rgba(212, 107, 8, 0.1);
}
.head-score.tone-fault {
  color: #cf1322;
  background: rgba(207, 19, 34, 0.08);
}
/* 触底标记「超扣 N 分」：仅在扣分合计超过满分时出现。
   0 分有两种成因 —— "刚好扣满"与"远超满分"，严重程度完全不同，
   没有这个标记，用户看到 0 分无法区分，容易低估问题。 */
.score-floor {
  flex: none;
  padding: 1px 6px;
  font-size: 11px;
  font-weight: 600;
  line-height: 1.6;
  color: #cf1322;
  background: rgba(207, 19, 34, 0.1);
  border-radius: 4px;
  white-space: nowrap;
}
.class-badge {
  flex: none;
  padding: 2px 10px;
  font-size: 12px;
  font-weight: 600;
  color: var(--gi-color-container-bg);
  background: var(--gi-warning);
  border-radius: 4px;
}
.class-badge.healthy {
  background: var(--gi-success);
}
/* 风险档位：按需求使用红色（与故障同色系，具体档位靠徽标文字区分） */
.class-badge.risk {
  background: var(--gi-error);
}
.class-badge.fault {
  background: var(--gi-error);
}
.class-badge.uncollected {
  background: var(--gi-muted);
}
/* 实例名 / 地址已按字符数截断（15 / 20），这里补 nowrap 兜底：
   窄屏时不再换行顶高头部，完整值由 :title 提供 */
.instance-title {
  font-size: 16px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.instance-meta {
  font-size: 13px;
  color: var(--gi-text-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ov-actions {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.btn-ghost {
  color: var(--gi-text-2);
}
.btn-ghost svg {
  color: var(--gi-muted);
  transition: transform 0.2s;
}
.btn-ghost[aria-expanded='false'] svg {
  transform: rotate(-90deg);
}

/* 「结论」块已删除，对应样式一并清理 */

.chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-left: 6px;
  padding: 1px 8px;
  font-size: 12px;
  border-radius: 10px;
}
.chip.warn {
  color: var(--gi-warning);
  background: var(--gi-warning-weak);
}
.chip.err {
  color: var(--gi-error);
  background: var(--gi-error-weak);
}

/* 健康信息带
   原「结论」块已删除，这里补上与头部之间的间距，避免信息带直接贴住标题行。
   中间「运行状态/可达/数据质量」格移除后，栅格由三列收为两列：
   健康分（窄）与核心告警（宽）按 1 : 1.8 分配整行宽度，不留空轨道 */
/* 健康信息带：健康分格已移除，只剩「核心告警」一格 ——
   栅格必须同步改成单列，否则告警格仍占第一列、右侧整条空列就是一片死白。 */
.health-band {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  margin: 6px 16px 14px;
  border: 1px solid var(--gi-color-border-soft);
  border-radius: var(--gi-radius-md);
  background: var(--gi-primary-surface);
  overflow: hidden;
}
/* 格内小标题移除后改为纵向居中：告警列表较长时，矮格子（健康分）
   不会顶部对齐而在底部留下一大片空白 */
.hb-cell {
  display: flex;
  flex-direction: column;
  justify-content: center;
  padding: 12px 14px;
  border-left: 1px solid var(--gi-color-border-soft);
}
.hb-cell:first-child {
  border-left: 0;
}
/* 各格均无小标题，格内首元素直接由 .hb-cell 的 padding 定位 */
.hb-note {
  margin-top: 8px;
  font-size: 11px;
  line-height: 1.7;
  color: var(--gi-text-3);
}
.hb-note b {
  font-weight: 600;
  color: var(--gi-text-2);
}

/* 告警区顶部汇总行：「有 N 条告警，请处理！」，N 取接口实时值（分级计数 pill 已移除） */
.alert-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 8px;
}
/* 告警列表：全量展示，不截断不折叠。
   可视区固定为 3 条的行高（3×18 + 2×6 = 66px），超过 3 条才出现纵向滚动条；
   只在这一块内部滚动，不撑高概览卡，也不影响分数 / 提示语 / 其他区域 */
.alert-mini {
  --alert-row-h: 18px;
  --alert-row-gap: 6px;
  list-style: none;
  margin: 8px 0 0;
  padding: 0 4px 0 0;
  display: flex;
  flex-direction: column;
  gap: var(--alert-row-gap);
  max-height: calc(3 * var(--alert-row-h) + 2 * var(--alert-row-gap));
  overflow-y: auto;
  overscroll-behavior: contain;
}
.alert-mini li {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  min-width: 0;
  font-size: 12px;
  line-height: var(--alert-row-h, 18px);
  color: var(--gi-text-2);
}
.lv {
  flex: none;
  padding: 0 5px;
  font-size: 11px;
  font-weight: 600;
  line-height: 16px;
  border-radius: 3px;
}
.lv-p1 {
  color: var(--gi-color-container-bg);
  background: var(--gi-error);
}
.lv-p2 {
  color: var(--gi-color-container-bg);
  background: var(--gi-warning);
}
.lv-p3 {
  color: #713f12;
  background: var(--gi-p3);
}
/* 告警正文完整换行展示，不做单行省略 —— 整句「指标名 + 当前值 + 阈值 + 已持续时长」必须可见 */
.alert-mini .tt {
  flex: 1;
  min-width: 0;
  white-space: normal;
  overflow-wrap: anywhere;
}
/* 等级徽标与首行文字基线对齐（正文换行后不再居中） */
.alert-mini .lv {
  margin-top: 1px;
}
/* 持续时长已并入 .tt 的整句文案，不再单独右对齐一列 */

/* 收起态摘要 */
/* 收起态摘要：已并入标题行（紧跟健康分之后），不再独占一行，
   故不设自身内边距，随 .ov-head 的 flex-wrap 与标题行同排/换行。 */
.ov-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 14px;
  min-width: 0;
  padding: 0;
  font-size: 12px;
  color: var(--gi-text-2);
}
.ov-summary .sm-item {
  display: inline-flex;
  align-items: baseline;
  gap: 4px;
}
.ov-summary .sub {
  font-size: 11px;
  color: var(--gi-text-3);
}
.ov-summary .sm-score {
  display: inline-flex;
  align-items: baseline;
  gap: 3px;
  padding: 1px 9px;
  font-weight: 600;
  border-radius: 10px;
  color: var(--gi-warning);
  background: var(--gi-warning-weak);
}
.ov-summary .sm-score.tone-ok {
  color: var(--gi-success);
  background: var(--gi-success-weak);
}
.ov-summary .sm-score.tone-fault {
  color: var(--gi-error);
  background: var(--gi-error-weak);
}
.ov-summary .sm-score .sub {
  font-weight: 400;
}
.sm-sep {
  width: 1px;
  height: 12px;
  background: var(--gi-color-border);
}

/* 字段行：两行（基本信息 / 架构）共用同一套栅格、内边距与分隔线。
   行首虚线统一保留——既把信息带与字段区隔开，也让两行之间的节奏一致 */
.ov-row {
  display: grid;
  grid-template-columns: 108px minmax(0, 1fr);
  gap: 8px 14px;
  padding: 12px 16px;
  border-top: 1px dashed var(--gi-color-border-soft);
}
.ov-row-head {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  /* 与字段小标题（dt，18px）同一行高，顶部对齐，不再额外下压 */
  line-height: 18px;
  color: var(--gi-text-2);
  padding-top: 0;
}
.ov-row-head svg {
  color: var(--gi-primary);
  flex: none;
}
/* 字段栅格：两行共用，行间距按新的字段高度（18 + 4 + 20）统一为 10px。
   auto-fit + minmax(148px, 1fr) 是刻意不写死列数的：未使用的轨道会被 auto-fit 折叠，
   本行实际列数（基本信息 5 列 / 架构 5 列）自动等分整行宽度并保持列首对齐，
   不会出现空白占位列 / 空轨道 / 错位，也无需手工改列数或跨度。 */
.ov-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(148px, 1fr));
  gap: 10px 18px;
}

.pill {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 1px 8px;
  font-size: 12px;
  border-radius: 10px;
  line-height: 18px;
}
.pill.ok {
  color: var(--gi-success);
  background: var(--gi-success-weak);
}
.pill.warn {
  color: var(--gi-warning);
  background: var(--gi-warning-weak);
}
.pill.err {
  color: var(--gi-error);
  background: var(--gi-error-weak);
}
.pill.info {
  color: var(--gi-info);
  background: var(--gi-info-weak);
}
.pill.neutral {
  color: var(--gi-text-3);
  background: var(--gi-muted-weak);
}
.pill.p1 {
  color: var(--gi-error);
  background: var(--gi-error-weak);
}
.pill.p2 {
  color: var(--gi-warning);
  background: var(--gi-warning-weak);
}
.pill.p3 {
  color: var(--gi-p3);
  background: var(--gi-p3-weak);
}

/* 可点击元素统一反馈：手型光标 + hover 微透明 + 点击下沉 */
.clickable {
  cursor: pointer;
  transition: opacity 0.2s, transform 0.1s;
}
.clickable:hover {
  opacity: 0.85;
}
.clickable:active {
  transform: translateY(1px);
}

/* ---------- 第三部分：Tab ---------- */
.tab-bar {
  /* Tab 标签栏常驻：不参与剩余空间分配，也不随内容滚动 */
  flex: none;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 16px;
  border-bottom: 1px solid var(--gi-color-border-soft);
}
.tabs {
  display: flex;
  gap: 2px;
  flex: 1;
}
/* Tab 行尾操作区：与 Tab 右端对齐，不与 Tab 抢宽度 */
.tab-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
.tab {
  position: relative;
  padding: 12px 14px 11px;
  font-size: 14px;
  color: var(--gi-text-2);
  background: transparent;
  border: 0;
  cursor: pointer;
  transition: color 0.2s;
}
.tab:hover {
  color: var(--gi-primary);
}
/* Tab 计数徽标：浅灰小胶囊默认隐入背景，悬停 / 选中时随 Tab 主题色加深，
   字号 11px 低于标签正文、等宽数字防抖动——「可读但不抢视线」 */
.tab-count {
  margin-left: 6px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  padding: 0 5px;
  font-size: 11px;
  font-weight: 400;
  line-height: 16px;
  border-radius: 9px;
  background: var(--gi-muted-weak);
  color: var(--gi-text-3);
  font-variant-numeric: tabular-nums;
  vertical-align: 1px;
  transition: background 0.2s, color 0.2s;
}
.tab:hover .tab-count,
.tab[aria-selected='true'] .tab-count {
  background: color-mix(in srgb, var(--gi-primary) 12%, #fff);
  color: var(--gi-primary);
}
.tab[aria-selected='true'] {
  color: var(--gi-primary);
  font-weight: 600;
}
.tab[aria-selected='true']::after {
  content: '';
  position: absolute;
  left: 10px;
  right: 10px;
  bottom: -1px;
  height: 2px;
  background: var(--gi-primary);
  border-radius: 2px 2px 0 0;
}

/* 仅看异常：只剩复选框，说明走 Tooltip（hover 浮层），不占常驻文案 */
.toggle {
  display: inline-flex;
  align-items: center;
  cursor: pointer;
  user-select: none;
}
.toggle input[type='checkbox'] {
  width: 14px;
  height: 14px;
  margin: 0;
  accent-color: var(--gi-primary);
  cursor: pointer;
}
.panel-body {
  /* 顶部留白：分组标题下线后由 panel-body 自己承担与 Tab 栏的间距。
     取 16px 与左右内边距一致，首个指标卡与容器顶部、相邻分组之间的留白才自然。 */
  padding: 16px;
  /* 本页唯一的纵向滚动容器：吃掉 Tab 卡片的剩余高度，滚动条只出现在它右侧。
     overscroll-behavior 阻止滚动链传递到外层，避免页面整体被带着滚。 */
  flex: 1;
  min-height: 0;
  overflow: auto;
  overscroll-behavior: contain;
  container-type: inline-size;
  container-name: panel;
}

/* 指标卡片网格：容器宽度决定每行 2 或 3 列 */
.metric-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 14px;
}
@container panel (min-width: 900px) {
  .metric-grid {
    grid-template-columns: repeat(3, 1fr);
  }
}
.metric-grid:empty {
  display: none;
}

.metric-card {
  position: relative; /* 放置位置指示条（::after）的定位参照 */
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 13px 14px 12px;
  background: var(--gi-primary-surface);
  border: 1px solid var(--gi-color-border-soft);
  border-radius: var(--gi-radius-md);
  transition: box-shadow 0.2s, border-color 0.2s, transform 0.2s;
  min-height: 226px;
}
/* 悬停：主色调的弱边框档；选中/抓取态见下方 mc-grabbed */
.metric-card:hover {
  border-color: var(--gi-primary-border-soft);
  box-shadow: var(--gi-shadow-md);
  transform: translateY(-1px);
}
/* 告警态：主色调的强调档（边框 + 底色），整卡保持单色相。
   「是哪一级告警」由 P0–P3 / 健康分级徽标用语义色表达，不靠卡片底色区分。 */
.metric-card.alerting {
  border-color: var(--gi-primary-border);
  background: var(--gi-primary-surface-strong);
}
.mc-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.mc-name {
  /* 标题收缩为内容宽度：? 图标才能紧跟标题（行尾的全屏按钮自带 margin-left:auto） */
  flex: 0 1 auto;
  min-width: 0;
  font-size: 14px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.mc-fullscreen {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  padding: 0;
  margin-left: auto;
  color: var(--gi-text-3);
  background: transparent;
  border: 1px solid transparent;
  border-radius: var(--gi-radius-sm);
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.2s, color 0.2s, background 0.2s, border-color 0.2s;
}
.metric-card:hover .mc-fullscreen,
.mc-fullscreen:focus-visible {
  opacity: 1;
}
.mc-fullscreen:hover {
  color: var(--gi-primary);
  background: var(--gi-primary-weak);
  border-color: var(--gi-primary-weak);
}

/* ---------- 指标卡排序：拖拽手柄 + 视觉反馈 ---------- */
/* 手柄始终显示（不再依赖 hover），保留 hover/focus/active 状态色与抓取光标 */
.mc-handle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
  width: 20px;
  height: 24px;
  color: var(--gi-text-3);
  background: transparent;
  border-radius: var(--gi-radius-sm);
  cursor: grab;
  opacity: 1;
  transition: opacity 0.15s, color 0.15s, background 0.15s;
}
.mc-handle:hover {
  color: var(--gi-primary);
  background: var(--gi-primary-weak);
}
.mc-handle:focus-visible {
  outline: 2px solid var(--gi-primary);
  outline-offset: 1px;
}
.mc-handle:active {
  cursor: grabbing;
}

/* 被拖动的卡片：半透明 + 虚线框，明确"这一张正在移动" */
.metric-card.mc-dragging {
  opacity: 0.45;
  border-style: dashed;
}
/* 键盘抓取态 */
.metric-card.mc-grabbed {
  border-color: var(--gi-primary);
  box-shadow: 0 0 0 2px var(--gi-primary-ring);
}

/* 放置位置提示：网格 gap 为 14px，指示条落在缝隙正中 */
.mc-drop-before::after,
.mc-drop-after::after {
  content: '';
  position: absolute;
  top: 4px;
  bottom: 4px;
  width: 3px;
  border-radius: 2px;
  background: var(--gi-primary);
  z-index: 2;
  pointer-events: none;
}
.mc-drop-before::after {
  left: -9px;
}
.mc-drop-after::after {
  right: -9px;
}

/* 恢复默认顺序按钮：与 Tab 行其它控件同一套图标按钮规格 */
.icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  padding: 0;
  color: var(--gi-text-2);
  background: transparent;
  border: 1px solid var(--gi-color-border);
  border-radius: var(--gi-radius-sm);
  cursor: pointer;
  transition: color 0.2s, border-color 0.2s;
}
.icon-btn:hover:not(:disabled) {
  color: var(--gi-primary);
  border-color: var(--gi-primary);
}
.icon-btn:disabled {
  color: var(--gi-text-3);
  opacity: 0.45;
  cursor: not-allowed;
}

/* 仅供读屏使用的文本 */
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
  border: 0;
}

/* 时间范围选择器：位于 Tab 行最右侧，与右端对齐 */
.time-select {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.time-select .field {
  width: 130px;
}
.time-select .time-input {
  width: 170px;
}
.time-select .time-input[readonly] {
  color: var(--gi-text-2);
  background: var(--gi-muted-weak);
  cursor: default;
}
.time-sep {
  color: var(--gi-text-3);
  font-size: 13px;
}
/* 窄屏：Tab 与操作区各占一行，操作区保持右对齐 */
@media (max-width: 900px) {
  .tab-bar {
    flex-wrap: wrap;
    row-gap: 8px;
  }
  .tabs {
    flex: 1 1 100%;
  }
  .tab-actions {
    flex: 1 1 100%;
    justify-content: flex-end;
    padding-bottom: 8px;
  }
  .time-select {
    flex-wrap: wrap;
    justify-content: flex-end;
  }
  .time-select .field {
    flex: 0 0 130px;
  }
  .time-select .time-input {
    flex: 1 1 auto;
    min-width: 140px;
  }
}

/* 趋势图容器：绘制与交互都在 MetricTrendChart 子组件内，这里只管占位 */
.mc-chart {
  flex: 1;
  min-height: 86px;
  position: relative;
}

.chart-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  font-size: 11px;
  color: var(--gi-muted);
}

/* 放大弹窗图例：与收起卡片的内联图例（.lg-inline）完全同款——
   仅「圆点 + 指标名」居中换行，不展示数值；当前值/单位/定义挂在
   .ml-item 的 title 上悬停可读。无分隔线、无脚注，底部干净收尾。 */
.modal-legend {
  list-style: none;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  column-gap: 18px;
  row-gap: 4px;
  margin: 12px 0 0;
  padding: 2px 0 2px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--gi-text-3);
}
.modal-legend .lg-dot {
  flex: none;
  width: 6px;
  height: 6px;
  border-radius: 50%;
}
.ml-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  max-width: 100%;
  cursor: default;
}
/* 与卡片图例同款交互：可点击 + 隐藏置灰 */
.ml-item.clickable {
  cursor: pointer;
}
.ml-item.off .lg-dot {
  background: var(--gi-muted);
  opacity: 0.45;
}
.ml-item.off .ml-name {
  color: var(--gi-muted);
}
.ml-name {
  min-width: 0;
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--gi-text-2);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

/* ---------- 卡片内联图例：居中、可换行、悬停展开 ---------- */
/* 与放大弹窗的 .modal-legend（居中值图例）结构相近但各自独立，互不影响 */
.lg-inline {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  column-gap: 12px;
  row-gap: 3px;
  margin-top: 6px;
  font-size: 11px;
  line-height: 1.6;
  color: var(--gi-text-3);
}
/* 多行时平时只露第一行（11px * 1.6 行高 ≈ 18px），悬停展开全部并留出过渡 */
/* 测量态：与 .expandable 同一套单行约束，仅用于"是否换行"的瞬时判定 */
.lg-inline.measuring {
  max-height: 20px;
  overflow: hidden;
}
.lg-inline.expandable {
  max-height: 20px;
  overflow: hidden;
  cursor: default;
}
/* 展开态由 JS（mouseenter/mouseleave + focusin/focusout）驱动 .open 类：
   不用 CSS :hover —— 布局位移/滚动场景下 :hover 命中判定与视觉脱节，会出现
   「鼠标已移出仍不回收」的残留；JS 事件边界明确，状态一定收敛。 */
.lg-inline.expandable.open {
  max-height: 200px;
}
.lgi-caret {
  flex: none;
  display: inline-flex;
  align-items: center;
  color: var(--gi-text-2);
  transition: transform 0.15s ease;
}
.lg-inline.expandable.open .lgi-caret {
  transform: rotate(180deg);
}
.lgi-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}
/* 图例点击切换显隐：可点击项给指针；隐藏项置灰（点与文字同步淡出） */
.lgi-item.clickable {
  cursor: pointer;
}
.lgi-item.off .lg-dot {
  background: var(--gi-muted);
  opacity: 0.45;
}
.lgi-item.off .lgi-name {
  color: var(--gi-muted);
}
.lg-inline .lg-dot {
  flex: none;
  width: 6px;
  height: 6px;
  border-radius: 50%;
}
.lgi-name {
  /* 名称完整显示：不截断不省略，一行放不下时整项（圆点+名）换行 */
  color: var(--gi-text-2);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  white-space: nowrap;
}

/* 空状态：Tab 层没有任何监控项时的统一提示 */
.layer-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 56px 24px 48px;
  text-align: center;
}

.layer-empty .le-icon {
  width: 34px;
  height: 34px;
  color: var(--gi-muted);
  opacity: 0.7;
  margin-bottom: 4px;
}

.layer-empty .le-title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--gi-text-2);
}

.layer-empty .le-desc {
  margin: 0;
  max-width: 520px;
  font-size: 12.5px;
  line-height: 1.7;
  color: var(--gi-text-3);
}

.layer-empty .le-btn {
  margin-top: 10px;
  padding: 5px 14px;
  font-size: 12.5px;
  color: var(--gi-primary);
  background: transparent;
  border: 1px solid var(--gi-primary);
  border-radius: 6px;
  cursor: pointer;
  transition: background-color 0.2s;
}

.layer-empty .le-btn:hover {
  background: var(--gi-primary-weak);
}

/* 指标放大弹窗 */
.modal-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 4px 0 16px;
  border-bottom: 1px solid var(--gi-color-border-soft);
}
/* 弹窗刷新按钮：贴工具栏右端，与外层 icon-btn 同款外观 */
.modal-refresh-btn {
  margin-left: auto;
  flex: none;
}
.modal-refresh-btn:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
@keyframes modal-spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}
.modal-refresh-btn .refresh-icon.spinning {
  animation: modal-spin 0.8s linear infinite;
}
.modal-chart {
  position: relative;
  height: 320px;
  padding: 16px 0 12px;
}

/* 旧版 .modal-foot（共 N 项 / 指标键 / 阈值）已删除，底部仅保留居中图例 */

/* 统一表单控件字体，避免浏览器默认字体破坏项目排版 */
.field,
.time-select .field,
.time-select .time-input {
  font-family: inherit;
}

/* 覆盖 Ant Design Modal 标题默认样式，与项目标题规范对齐 */
:deep(.ant-modal-title) {
  font-size: 16px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--gi-text-1);
}

/* 弹窗标题行：标题文字 + 指标说明图标同行排列 */
.modal-title-wrap {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  vertical-align: middle;
}

/* 主卡片的「共 N 项 / M 项无数据」脚注已整体移除；
   这个 .mc-foot 现在只服务「方言不适用」折叠区的键值小卡，与主卡片无关 */
.mc-foot {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  color: var(--gi-text-3);
  padding-top: 6px;
  border-top: 1px dashed var(--gi-color-border-soft);
}
.mc-key {
  /* 与右上角时间框等系统控件保持一致，使用页面默认无衬线字体 */
  font-family: 'PingFang SC', 'Noto Sans SC', 'Segoe UI', 'Microsoft YaHei UI', sans-serif;
  color: var(--gi-text-3);
}
.mc-th {
  margin-left: auto;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.mc-note {
  font-size: 11px;
  line-height: 1.55;
  color: var(--gi-text-3);
}
.mc-note.reason {
  color: var(--gi-warning);
}
.mc-note.off {
  color: var(--gi-muted);
}

/* 方言不适用分组 */
.dialect-group {
  margin-top: 6px;
  border: 1px dashed var(--gi-color-border);
  border-radius: var(--gi-radius-md);
  background: var(--gi-primary-surface);
}
.dialect-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 10px 12px;
  font-size: 12px;
  color: var(--gi-text-2);
  background: transparent;
  border: 0;
  cursor: pointer;
  text-align: left;
}
.dialect-toggle svg {
  transition: transform 0.2s;
  color: var(--gi-muted);
}
.dialect-toggle[aria-expanded='true'] svg {
  transform: rotate(90deg);
}
.dialect-toggle .count {
  margin-left: auto;
  color: var(--gi-text-3);
}
.dialect-body {
  display: none;
  padding: 0 12px 12px;
}
.dialect-body.open {
  display: block;
}

/* 空状态：吃掉 Tab 卡片剩余空间并垂直居中，避免挤在内容区底部 */
.empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 32px 0;
  margin: 0;
  text-align: center;
  font-size: 13px;
  color: var(--gi-text-3);
}

/* 顶部留白不再用负 margin 微调：四边间距已统一由布局壳 .layout-content 提供，
   壳自身在 ≤1023px（16px）与 ≤767px（12px）断点会自动收窄，本页跟随即可。 */
@media (max-width: 767px) {
  /* 窄屏：收紧图例指标名可用宽度，保证「圆点 + 名称 + 值 + 单位」居中换行不溢出 */
  .ml-name {
    max-width: 120px;
  }
}</style>

<!-- ==========================================================================
     主题 token（非 scoped：选择器只覆盖本页根节点与放大弹窗的包裹层）

     为什么不用 scoped：指标放大弹窗由 a-modal teleport 到 body，不在 .monitor-detail
     的子树里。把同一套 token 挂到 wrap 类上，弹窗内的曲线 / 图例 / 文字无需另写样式
     即可取到相同颜色，也顺带修好了弹窗里 var() 取不到值导致的着色缺失。

     单一数据源：
       · 基础层 —— 系统全局 Design Token（:root，assets/scss/tokens.scss）。
         全局已有的同名 token（--gi-radius-* / --gi-shadow-* / --gi-color-page-bg /
         --gi-color-container-bg / --gi-color-border）这里不再重复声明，直接继承。
       · 强调层 —— 由系统主色调在「明度 / 透明度」两个维度派生，本页不出现第二种色相。
         这样换主题只需改 --gi-color-primary 一处，整页颜色自动跟随。
     ========================================================================== -->
<style>
.monitor-detail,
.monitor-detail-modal-wrap {
  /* ---------- 系统主色调 ---------- */
  --gi-primary: var(--gi-color-primary);
  --gi-primary-hover: var(--gi-color-primary-hover);
  --gi-primary-active: var(--gi-color-primary-active);

  /* 主色调的底色档位（不透明，混白）：卡片底 / 告警卡片底。
     用不透明色而非 alpha，避免卡片比页面底色更暗、看起来是"凹下去"的。 */
  --gi-primary-surface: color-mix(in srgb, var(--gi-color-primary) 4%, #fff);
  --gi-primary-surface-strong: color-mix(in srgb, var(--gi-color-primary) 8%, #fff);

  /* 主色调的透明度档位：交互态叠加底（悬停 / 选中 / 拖拽落点） */
  --gi-primary-weak: color-mix(in srgb, var(--gi-color-primary) 10%, transparent);

  /* 主色调的线条档位：分隔线 / 图表网格线 → 边框（悬停 → 强调） */
  --gi-primary-line: color-mix(in srgb, var(--gi-color-primary) 12%, #fff);
  --gi-primary-border-soft: color-mix(in srgb, var(--gi-color-primary) 20%, transparent);
  --gi-primary-border: color-mix(in srgb, var(--gi-color-primary) 45%, transparent);

  /* 焦点环 / 主色阴影 */
  --gi-primary-ring: color-mix(in srgb, var(--gi-color-primary) 14%, transparent);
  --gi-primary-shadow: color-mix(in srgb, var(--gi-color-primary) 18%, transparent);

  /* 卡片档位色：告警 / 采集失败 —— 同一色相的更深明度档，而不是第二色相 */
  --gi-primary-tone-alert: color-mix(in srgb, var(--gi-color-primary) 74%, #000);
  --gi-primary-tone-error: color-mix(in srgb, var(--gi-color-primary) 52%, #000);

  /* 多序列曲线配色：淡色系 + 色相拉开，序号越大越"跳"一档色相。
     区分度靠色相间隔而不是明度差 —— 同色相的浅档位在低饱和时肉眼分不出，
     且档位多了以后浅的那几条会淡到几乎看不见。
     同时统一压在中高明度 / 中低饱和，保证是"淡色"而不是高饱和撞色。
     第 1 档仍由主色调混白提亮派生，保住「卡片主色调 = 系统主色」这条基调。 */
  --gi-curve-1: color-mix(in srgb, var(--gi-color-primary) 70%, #fff); /* 青绿（主色调淡版） */
  --gi-curve-2: hsl(212, 60%, 58%); /* 淡蓝 */
  --gi-curve-3: hsl(270, 45%, 62%); /* 淡紫 */
  --gi-curve-4: hsl(28, 68%, 55%); /* 淡琥珀 */
  --gi-curve-5: hsl(150, 42%, 48%); /* 淡翠绿 */
  --gi-curve-6: hsl(338, 52%, 60%); /* 淡玫红 */

  /* ---------- 语义状态色 ----------
     健康 / 风险 / 故障、P0–P3、质量标签这类「等级语义」需要一眼可辨，
     故保留独立色相、不做单色相化；这里只把它们收敛成单一声明点，
     -weak 档统一由基色混白派生，不再各自写一个十六进制。 */
  --gi-success: #0f9f6f;
  --gi-success-weak: color-mix(in srgb, var(--gi-success) 14%, #fff);
  --gi-warning: #d97706;
  --gi-warning-weak: color-mix(in srgb, var(--gi-warning) 14%, #fff);
  --gi-error: #d64545;
  --gi-error-weak: color-mix(in srgb, var(--gi-error) 14%, #fff);
  /* P3 独立黄色，与 P2 橙色区分 */
  --gi-p3: #eab308;
  --gi-p3-weak: color-mix(in srgb, var(--gi-p3) 14%, #fff);
  --gi-info: #1677ff;
  --gi-info-weak: color-mix(in srgb, var(--gi-info) 14%, #fff);
  /* 「无数据」用的中性灰：这是语义（没有值），不是色相选择，保持中性 */
  --gi-muted: #9aa8b8;
  --gi-muted-weak: color-mix(in srgb, var(--gi-muted) 14%, #fff);

  /* ---------- 文字与线条：直接引用系统 token ---------- */
  --gi-text-1: var(--gi-color-text-primary);
  --gi-text-2: var(--gi-color-text-secondary);
  --gi-text-3: var(--gi-color-text-tertiary);
  --gi-color-border-soft: var(--gi-primary-line);

  /* ---------- 排版尺度：与系统 token 对齐 ---------- */
  --font-caption: var(--gi-font-size-caption);
  --font-body: var(--gi-font-size-body);
  --font-title: var(--gi-font-size-title);
}

/* 实例告警明细弹窗（teleport 到 body，样式放本非 scoped 块） */
/* 实例告警明细弹窗正文：全部单元格统一为普通正文（无加粗、无彩色），
   表头保持 antd 原有强调样式，正文与表头形成明显层级区分。
   这里同时覆盖 hover 态，避免行悬浮时文字颜色被 antd 默认规则改写。 */
.inst-alerts .ant-table-tbody > tr > td,
.inst-alerts .ant-table-tbody > tr:hover > td,
.inst-alerts .ant-table-tbody > tr.ant-table-row-hover > td {
  font-weight: 400;
  color: var(--gi-text-1, #163440);
}
.inst-alerts .inst-alerts-name,
.inst-alerts .inst-alerts-plain {
  font-weight: 400;
  color: inherit;
}
.inst-alerts .inst-alerts-error {
  margin-bottom: 12px;
}

@media (max-width: 768px) {
  /* 指标卡 2 列 + 226px 最小高度，窄屏内容挤压且纵向浪费 */
  .metric-grid {
    grid-template-columns: minmax(0, 1fr);
    gap: 10px;
  }

  .metric-card {
    min-height: 0;
    padding: 12px;
  }

  .mc-chart {
    min-height: 120px;
  }

  /* 字段行标签列 108px 在 375 下占比过大 */
  .ov-row {
    grid-template-columns: 88px minmax(0, 1fr);
    gap: 6px 10px;
    padding: 10px 12px;
  }

  .ov-grid {
    grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
    gap: 8px 12px;
  }

  /* 900 断点里给时间控件留了 130/140 下限，这里放开 */
  .time-select .field,
  .time-select .time-input {
    flex: 1 1 100%;
    min-width: 0;
  }

  .tab-bar {
    padding: 0 12px;
  }

  .tabs {
    overflow-x: auto;
    flex-wrap: nowrap;
    scrollbar-width: none;
  }

  .tabs::-webkit-scrollbar {
    display: none;
  }

  .tab {
    padding: 10px 10px 9px;
    font-size: 13px;
    white-space: nowrap;
  }

  .modal-legend {
    column-gap: 12px;
  }
}
</style>
