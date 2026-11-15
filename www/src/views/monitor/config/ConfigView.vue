<template>
  <div class="gi-page-shell config-page">
    <PageHeader title="监控配置">
      <template #right>
        <a-space wrap>
          <a-button @click="gotoAlertConfig">
            <BellOutlined />
            告警配置
          </a-button>
          <a-button :loading="loadingTargets" @click="reloadAll">
            <SyncOutlined />
            刷新
          </a-button>
        </a-space>
      </template>
    </PageHeader>

    <a-card class="section-card" :bordered="false">
      <a-tabs v-model:activeKey="activeTab" class="config-tabs">
        <!-- 三个 Tab 的搜索/筛选/新建统一挂到 Tab 名称同一行、靠右显示：
             原先各自占内容区顶部一整行，把表格往下挤，视觉上也与 Tab 割裂。
             按 activeTab 动态切换，避免把「新建模板」误点到另一个 Tab 上；
             控件绑定与事件保持原样，只是挂载位置变了。
             插槽用 rightExtra：tabBarExtraContent 在 antd-vue 4 已废弃（会打 devWarning）。 -->
        <template #rightExtra>
          <div class="tabs-extra">
            <template v-if="activeTab === 'targets'">
              <a-input-search
                v-model:value="filters.search"
                allow-clear
                class="search-input"
                placeholder="搜索实例、描述或实例ID"
                @search="reloadAll"
              />
              <a-select
                v-model:value="filters.environment"
                allow-clear
                class="filter-select"
                placeholder="全部环境"
                :options="environmentOptions"
                @change="reloadTargets"
              />
              <a-select
                v-model:value="filters.dbType"
                allow-clear
                class="filter-select"
                placeholder="全部类型"
                :options="dbTypeOptions"
                @change="reloadAll"
              />
            </template>
            <template v-else-if="activeTab === 'strategy'">
              <a-button type="primary" @click="openStrategyModal(null)">
                <PlusOutlined />
                新建模板
              </a-button>
              <a-select
                v-model:value="strategyFilter.dbType"
                :options="strategyDbTypeOptions"
                allow-clear
                class="filter-select"
                placeholder="全部数据库类型"
                @change="reloadStrategyTemplates"
              />
            </template>
            <template v-else-if="activeTab === 'templates'">
              <a-button type="primary" @click="openTemplateModal()">
                <PlusOutlined />
                新建模板
              </a-button>
              <a-select
                v-model:value="templateFilter.dbType"
                allow-clear
                class="filter-select"
                placeholder="全部数据库类型"
                :options="templateDbTypeOptions"
                @change="reloadTemplates"
              />
            </template>
          </div>
        </template>

        <a-tab-pane key="targets" tab="监控对象">
          <div ref="targetsHost" class="table-height-host">
            <a-table
              :columns="targetColumns"
              :data-source="targetState.targets"
              row-key="instance_id"
              :loading="loadingTargets"
              :pagination="{ pageSize: 8 }"
              :scroll="{ x: 1040, y: tableBodyHeight.targets }"
            >
              <template #bodyCell="{ column, record }">
                <template v-if="column.key === 'name'">
                  <div class="datasource-cell">
                    <img
                      class="db-type-icon"
                      :src="dbTypeLogo(record.db_type)"
                      :alt="record.db_type || 'db'"
                      :title="record.db_type || '未知类型'"
                    />
                    <div class="datasource-text">
                      <div class="datasource-name" :title="record.name">{{ record.name || '—' }}</div>
                      <div class="datasource-target" :title="record.target">{{ record.target || '—' }}</div>
                    </div>
                  </div>
                </template>
                <template v-if="column.key === 'enabled'">
                  <!-- 只读文本：启用状态统一收敛到「编辑对象」入口，不再支持行内直接开关 -->
                  <span>{{ record.enabled ? '启用' : '未启用' }}</span>
                </template>
                <template v-if="column.key === 'collect_status'">
                  <a-tooltip :title="record.last_collect_message || record.engine_note || ''">
                    <span>{{ collectStatusText(record.last_collect_status) }}</span>
                  </a-tooltip>
                </template>
                <template v-if="column.key === 'collect_quality'">
                  <!-- 可下钻的前提是后端给了明细；没有明细时保持纯文本，不做假链接 -->
                  <a v-if="hasQualityDetails(record)" class="quality-link" @click="openQualityDrawer(record)">
                    <span>{{ qualityText(record.collect_quality) }} · {{ record.quality_details.length }} 项</span>
                  </a>
                  <a-tooltip v-else title="暂无降级明细：该实例尚未产出非正常指标，或明细未落到最新采集结果">
                    <span>{{ qualityText(record.collect_quality) }}</span>
                  </a-tooltip>
                </template>
                <template v-if="column.key === 'template'">
                  <a-tooltip :title="templateTooltip(record)">
                    <span>{{ record.template_label || record.metrics_template || '—' }}</span>
                  </a-tooltip>
                </template>
                <template v-if="column.key === 'actions'">
                  <a-space class="gi-table-actions">
                    <a-button type="link" size="small" @click="openTargetModal(record)">编辑对象</a-button>
                    <a-button
                      type="link"
                      size="small"
                      :disabled="!record.collectable"
                      @click="collectNow(record)"
                    >立即采集</a-button>
                  </a-space>
                </template>
              </template>
            </a-table>
          </div>
        </a-tab-pane>

        <a-tab-pane key="strategy" tab="采集策略">
          <div ref="strategyHost" class="table-height-host">
            <a-table
                :columns="strategyTemplateColumns"
                :data-source="filteredStrategyTemplates"
                row-key="id"
                :loading="loadingStrategyTemplates"
                :pagination="{ pageSize: 6 }"
                :scroll="{ x: 1100, y: tableBodyHeight.strategy }"
              >
                <template #bodyCell="{ column, record }">
                  <template v-if="column.key === 'name'">
                    <span>{{ strategyDisplayName(record) }}</span>
                  </template>
                  <template v-if="column.key === 'db_type'">
                    <span>{{ record.db_type_label || record.db_type }}</span>
                  </template>
                  <template v-if="column.key === 'summary'">
                    <span>{{ strategySummary(record) }}</span>
                  </template>
                  <template v-if="column.key === 'effective_instances'">
                    <!-- 生效实例 = 显式挂载 + 未挂载但由默认模板兜底的实例；只看 bound 会把默认模板显示成 0，误以为兜底失效 -->
                    <a-tooltip
                      v-if="record.is_default && record.effective_instances > record.bound_instances"
                      :title="`显式挂载 ${record.bound_instances} 个，另有 ${record.effective_instances - record.bound_instances} 个未挂载实例由默认模板兜底`"
                    >
                      <span>{{ record.effective_instances }} 个实例</span>
                    </a-tooltip>
                    <span v-else>{{ record.effective_instances }} 个实例</span>
                  </template>
                  <template v-if="column.key === 'enabled'">
                    <span>{{ record.enabled ? '启用' : '停用' }}</span>
                  </template>
                  <template v-if="column.key === 'actions'">
                    <a-space class="gi-table-actions">
                      <a-button type="link" size="small" @click="openStrategyModal(record)">策略配置</a-button>
                      <a-button type="link" size="small" @click="openStrategyBindModal(record)">绑定实例</a-button>
                      <a-button
                        type="link"
                        size="small"
                        danger
                        :disabled="record.builtin || record.is_default"
                        @click="removeStrategyTemplate(record)"
                      >删除</a-button>
                    </a-space>
                  </template>
                </template>
              </a-table>
          </div>
        </a-tab-pane>

        <a-tab-pane key="templates" tab="指标模板">
          <div ref="templatesHost" class="table-height-host">
            <a-table
              :columns="templateColumns"
              :data-source="filteredTemplateList"
              row-key="id"
              :loading="loadingTemplates"
              :pagination="{ pageSize: 10 }"
              :scroll="{ x: 1320, y: tableBodyHeight.templates }"
            >
              <template #bodyCell="{ column, record }">
                <template v-if="column.key === 'name'">
                  <div>{{ record.display_name || record.name }}</div>
                </template>
                <template v-if="column.key === 'db_type'">
                  <span>{{ record.db_type_label || record.db_type }}</span>
                </template>
                <template v-if="column.key === 'metric_count'">
                  {{ record.metric_count }} 项
                </template>
                <template v-if="column.key === 'bound_instances'">
                  {{ record.bound_instances }} 个实例
                </template>
                <template v-if="column.key === 'enabled'">
                  {{ record.enabled ? '启用' : '停用' }}
                </template>
                <template v-if="column.key === 'actions'">
                  <a-space class="gi-table-actions">
                    <a-button type="link" size="small" @click="openTemplateModal(record)">指标配置</a-button>
                    <a-button type="link" size="small" @click="openBindModal(record)">绑定实例</a-button>
                    <a-popconfirm
                      v-if="!record.builtin"
                      title="确认删除该模板？删除后不可恢复。"
                      @confirm="removeTemplate(record)"
                    >
                      <a-button type="link" danger size="small">删除</a-button>
                    </a-popconfirm>
                  </a-space>
                </template>
              </template>
            </a-table>
          </div>
        </a-tab-pane>

        <a-tab-pane key="health" tab="采集健康度">
          <a-spin :spinning="loadingStatus">
            <a-row :gutter="[16, 16]">
              <a-col v-for="tile in healthTiles" :key="tile.key" :xs="24" :sm="12" :xl="6">
                <div class="health-tile">
                  <div class="health-tile-label">{{ tile.label }}</div>
                  <div class="health-tile-value" :class="tile.tone">{{ tile.value }}</div>
                  <div class="health-tile-note">{{ tile.note }}</div>
                </div>
              </a-col>
            </a-row>

            <div class="health-section-title">按引擎汇总</div>
            <a-table
              :columns="engineColumns"
              :data-source="healthEngines"
              :pagination="false"
              row-key="key"
              size="small"
            >
              <template #bodyCell="{ column, record }">
                <template v-if="column.key === 'implemented'">
                  <span>{{ record.implemented ? '支持采集' : '未实现' }}</span>
                </template>
              </template>
            </a-table>

            <div class="health-section-title">采集失败 / 退避中的实例</div>
            <a-empty v-if="!status.failures?.length" description="暂无采集失败实例" :image="false" />
            <a-table
              v-else
              :columns="failureColumns"
              :data-source="status.failures"
              :pagination="{ pageSize: 8 }"
              row-key="instance_id"
              size="small"
            />
          </a-spin>
        </a-tab-pane>

      </a-tabs>
    </a-card>

    <!-- 「部分可用」下钻：没有下钻入口的汇总标签只是噪音，这里给出缺项与原因 -->
    <a-drawer
      :open="qualityDrawer.open"
      :title="`数据质量明细 · ${qualityDrawer.target}`"
      width="760"
      @close="qualityDrawer.open = false"
    >
      <a-empty v-if="!qualityDrawer.items.length" description="没有非正常指标" />
      <a-table
        v-else
        :columns="qualityDetailColumns"
        :data-source="qualityDrawer.items"
        :pagination="{ pageSize: 12, size: 'small' }"
        size="small"
        row-key="metric_key"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'quality'">
            <span>{{ qualityText(record.quality) }}</span>
          </template>
          <template v-else-if="column.key === 'reason'">
            <span class="quality-reason">{{ record.reason || '—' }}</span>
          </template>
        </template>
      </a-table>
    </a-drawer>

    <a-modal
      v-model:open="targetModal.open"
      title="编辑监控对象"
      width="760px"
      :confirm-loading="savingTarget"
      @ok="submitTarget"
    >
      <a-form layout="vertical">
        <a-row :gutter="16">
          <a-col :span="12">
            <a-form-item label="实例名称">
              <a-input :value="targetModal.form.name" disabled />
            </a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="实例目标">
              <a-input :value="targetModal.form.target" disabled />
            </a-form-item>
          </a-col>
          <a-col :span="8">
            <a-form-item label="启用监控">
              <a-switch v-model:checked="targetModal.form.enabled" />
            </a-form-item>
          </a-col>
          <a-col :span="8">
            <a-form-item label="所属业务">
              <a-input v-model:value="targetModal.form.business" placeholder="如：支付中心" />
            </a-form-item>
          </a-col>
          <a-col :span="8">
            <a-form-item label="部署形态">
              <a-select v-model:value="targetModal.form.deploy_mode" :options="deployModeOptions" />
            </a-form-item>
          </a-col>
          <a-col :span="16">
            <a-form-item label="Prometheus 目标标识">
              <a-input v-model:value="targetModal.form.prometheus_target" placeholder="建议填主机名（如 db-01），与 node_exporter 的 instance 标签一致" />
            </a-form-item>
          </a-col>
          <a-col :span="8">
            <a-form-item label="指标模板">
              <a-select
                v-model:value="targetModal.form.metrics_template"
                :options="targetTemplateOptions"
                :loading="loadingTemplates"
                placeholder="选择该实例的采集指标模板"
              />
            </a-form-item>
          </a-col>
          <a-col :span="8">
            <a-form-item label="基础资源采集来源">
              <a-select v-model:value="targetModal.form.resource_source" :options="resourceSourceOptions" />
            </a-form-item>
          </a-col>
          <a-col :span="8">
            <a-form-item label="引擎指标采集来源">
              <a-select v-model:value="targetModal.form.engine_source" :options="engineSourceOptions" />
            </a-form-item>
          </a-col>
          <a-col :span="8">
            <a-form-item label="实例标签">
              <a-select v-model:value="targetModal.form.labels" mode="tags" placeholder="输入标签后回车" />
            </a-form-item>
          </a-col>
        </a-row>
      </a-form>
    </a-modal>

    <a-modal
      v-model:open="templateModal.open"
      :title="templateModal.form.id ? '指标模板配置' : '新建指标模板'"
      width="900px"
      :confirm-loading="savingTemplate"
      @ok="submitTemplate"
    >
      <a-spin :spinning="loadingTemplateDetail">
        <a-form layout="vertical">
          <!-- 模板基础信息：一行水平排布（编辑态无「复制已有模板」，各列自动放宽） -->
          <div class="template-meta-card">
            <a-row :gutter="16">
              <a-col :span="templateModal.form.id ? 7 : 6">
                <a-form-item label="模板名称" class="compact-form-item">
                  <a-input v-model:value="templateModal.form.display_name" placeholder="如 核心库模板" />
                </a-form-item>
              </a-col>
              <a-col :span="templateModal.form.id ? 6 : 5">
                <a-form-item label="数据库类型" class="compact-form-item">
                  <a-select
                    v-model:value="templateModal.form.db_type"
                    :disabled="!!templateModal.form.id"
                    :options="templateDbTypeFormOptions"
                    @change="onTemplateDbTypeChange"
                  >
                    <template #option="{ label, logo }">
                      <span class="db-type-option">
                        <img v-if="logo" class="db-type-option-logo" :src="logo" :alt="label" />
                        <span>{{ label }}</span>
                      </span>
                    </template>
                  </a-select>
                </a-form-item>
              </a-col>
              <a-col :span="templateModal.form.id ? 7 : 6">
                <a-form-item label="模板说明" class="compact-form-item">
                  <a-input v-model:value="templateModal.form.description" placeholder="说明该模板的适用场景" />
                </a-form-item>
              </a-col>
              <a-col v-if="!templateModal.form.id" :span="4">
                <a-form-item label="复制已有模板" class="compact-form-item">
                  <a-select
                    v-model:value="templateModal.form.copy_from"
                    allow-clear
                    :options="copySourceOptions"
                    placeholder="复制后裁剪"
                    @change="onCopyFromChange"
                  />
                </a-form-item>
              </a-col>
              <a-col :span="templateModal.form.id ? 4 : 3">
                <a-form-item label="启用状态" class="compact-form-item">
                  <a-tooltip :title="templateModal.form.is_default ? '内置默认模板不可停用' : '停用后绑定它的实例将回落到默认模板'">
                    <a-switch
                      v-model:checked="templateModal.form.enabled"
                      :disabled="templateModal.form.is_default"
                    />
                  </a-tooltip>
                </a-form-item>
              </a-col>
            </a-row>
          </div>
        </a-form>

        <div class="template-picker-toolbar">
          <span class="picker-summary">
            已勾选 <b>{{ templateModal.form.metric_keys.length }}</b> 项 / 可选
            {{ templatePickableCount }} 项
          </span>
          <a-space>
            <a-button size="small" @click="selectAllTemplateMetrics">全选可选</a-button>
            <a-button size="small" @click="clearTemplateMetrics">清空</a-button>
          </a-space>
        </div>

        <a-empty v-if="!templateMetricGroups.length" description="该数据库类型暂无可配置的指标" />
        <div v-else class="template-picker">
          <div v-for="group in templateMetricGroups" :key="group.layer" class="picker-group">
            <div class="picker-group-title">
              <a-tag :color="layerColor(group.layer)">{{ group.label }}</a-tag>
              <span class="picker-count">
                已选 {{ group.selected }}/{{ group.total }}
                <span v-if="group.noSource" class="picker-disabled">（{{ group.noSource }} 项无采集源）</span>
                <span v-if="group.disabled" class="picker-disabled">（{{ group.disabled }} 项全局停用）</span>
              </span>
              <a-checkbox
                :checked="group.selected === group.selectable && group.selectable > 0"
                :indeterminate="group.selected > 0 && group.selected < group.selectable"
                :disabled="!group.selectable"
                @change="toggleTemplateGroup(group, $event)"
              >全选</a-checkbox>
            </div>
            <!-- 注意：同一弹窗里 engine / kernel 两层各有一组复选框。
                 之前把它们都包在 a-checkbox-group 里并 v-model:value 到同一个数组，
                 Ant Design 的 CheckboxGroup 会按自己注册的子节点维护状态，导致跨层误清空。
                 改成单个复选框自行绑定 :checked + @change，彻底消除层级间联动。 -->
            <div class="picker-items">
              <a-row :gutter="[8, 8]">
                <!-- 明细口径下同一采集键可能出现在多个监控项里（如 perf.conn_count 同属
                     会话连接/线程），row key 追加 detail_item 保证唯一，避免 v-for 复用错乱 -->
                <a-col
                  v-for="(item, index) in group.items"
                  :key="`${group.layer}-${item.metric_key}-${item.detail_item || ''}-${index}`"
                  :xs="24"
                  :sm="12"
                  :xl="8"
                >
                  <a-tooltip :title="metricPickerTooltip(item)">
                    <a-checkbox
                      :checked="templateMetricKeySet.has(item.metric_key)"
                      :disabled="!item.enabled"
                      @change="toggleMetricKey(item.metric_key, $event)"
                    >
                      {{ item.display_name }}
                      <a-tag v-if="item.available === false" color="default">无采集源</a-tag>
                      <a-tag v-else-if="!item.enabled" color="default">全局停用</a-tag>
                    </a-checkbox>
                  </a-tooltip>
                </a-col>
              </a-row>
            </div>
          </div>
        </div>
      </a-spin>
    </a-modal>

    <a-modal
      v-model:open="bindModal.open"
      :title="`绑定实例到模板：${bindModal.template?.display_name || bindModal.template?.name || ''}`"
      width="860px"
      :confirm-loading="savingBind"
      ok-text="绑定到该模板"
      @ok="submitBind('bind')"
    >
      <template #footer>
        <a-space>
          <a-button @click="bindModal.open = false">取消</a-button>
          <a-button :loading="savingBind" @click="submitBind('unbind')">回落到默认模板</a-button>
          <a-button type="primary" :loading="savingBind" @click="submitBind('bind')">绑定到该模板</a-button>
        </a-space>
      </template>
      <a-table
        :columns="bindColumns"
        :data-source="bindModal.candidates"
        row-key="instance_id"
        size="small"
        :pagination="{ pageSize: 8 }"
        :row-selection="{ selectedRowKeys: bindModal.selected, onChange: onBindSelectionChange }"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'datasource'">
            <div class="bind-ds-cell">
              <div class="bind-ds-name" :title="record.name">{{ record.name || '—' }}</div>
              <div class="bind-ds-target" :title="record.target">{{ record.target || '—' }}</div>
            </div>
          </template>
          <template v-if="column.key === 'current'">
            <span>{{ record.template_label || '默认模板' }}</span>
          </template>
        </template>
      </a-table>
    </a-modal>

    <!-- 采集策略模板编辑：策略项由后端注册表驱动渲染，前端不认识任何具体策略名 -->
    <a-modal
      v-model:open="strategyModal.open"
      :title="strategyModal.editingId ? '编辑采集策略模板' : '新建采集策略模板'"
      width="720px"
      :confirm-loading="savingStrategy"
      @ok="submitStrategyTemplate"
    >
      <!-- 模板信息一行排布、不换行：模板名称 → 数据库类型 → 模板说明 → 复制已有模板 → 启用状态。
           模板标识（后端必填唯一键）不再由用户输入，提交时按「库类型-custom-时间戳」自动生成 -->
      <a-spin :spinning="loadingStrategyDetail">
        <a-form layout="vertical">
          <div class="template-meta-card">
            <a-row :gutter="16">
              <a-col :span="strategyModal.editingId ? 6 : 5">
                <a-form-item label="模板名称" class="compact-form-item">
                  <a-input v-model:value="strategyModal.form.display_name" placeholder="留空则自动生成" />
                </a-form-item>
              </a-col>
              <a-col :span="strategyModal.editingId ? 6 : 5">
                <a-form-item label="数据库类型" class="compact-form-item">
                  <a-select
                    v-model:value="strategyModal.form.db_type"
                    :options="strategyDbTypeFormOptions"
                    :disabled="Boolean(strategyModal.editingId)"
                  >
                    <template #option="{ label, logo }">
                      <span class="db-type-option">
                        <img v-if="logo" class="db-type-option-logo" :src="logo" :alt="label" />
                        <span>{{ label }}</span>
                      </span>
                    </template>
                  </a-select>
                </a-form-item>
              </a-col>
              <a-col :span="strategyModal.editingId ? 8 : 6">
                <a-form-item label="模板说明" class="compact-form-item">
                  <a-input v-model:value="strategyModal.form.description" placeholder="这个模板适合什么样的实例" />
                </a-form-item>
              </a-col>
              <a-col v-if="!strategyModal.editingId" :span="5">
                <a-form-item label="复制已有模板" class="compact-form-item">
                  <a-select
                    v-model:value="strategyModal.form.copy_from"
                    allow-clear
                    :options="strategyCopySourceOptions"
                    placeholder="复制后沿用其策略项"
                    @change="onStrategyCopyFromChange"
                  />
                </a-form-item>
              </a-col>
              <a-col :span="strategyModal.editingId ? 4 : 3">
                <a-form-item label="启用状态" class="compact-form-item">
                  <a-switch v-model:checked="strategyModal.form.enabled" />
                </a-form-item>
              </a-col>
            </a-row>
          </div>

          <a-divider orientation="left" plain>策略项</a-divider>
          <div class="strategy-item-grid">
            <div v-for="row in strategyItemRows" :key="row.spec.type" class="strategy-item-row">
              <div class="strategy-item-main">
                <span class="strategy-item-name">
                  {{ row.spec.name }}
                  <!-- 解释文字收进问号 tooltip：点击图标展示，行内不再铺描述避免挤压控件 -->
                  <a-tooltip :title="row.spec.description" trigger="click">
                    <QuestionCircleOutlined class="strategy-item-help" />
                  </a-tooltip>
                </span>
                <a-input-number
                  v-if="row.spec.value_kind === 'int'"
                  v-model:value="strategyModal.items[row.spec.type]"
                  class="strategy-item-control"
                  :min="row.spec.min"
                  :max="row.spec.max"
                  :addon-after="row.spec.unit || undefined"
                />
                <a-select
                  v-else-if="row.spec.value_kind === 'enum'"
                  v-model:value="strategyModal.items[row.spec.type]"
                  class="strategy-item-control"
                  :options="row.spec.options || []"
                />
                <a-input
                  v-else
                  v-model:value="strategyModal.items[row.spec.type]"
                  class="strategy-item-control"
                  placeholder="留空表示沿用实例当前绑定"
                />
              </div>
            </div>
          </div>
        </a-form>
      </a-spin>
    </a-modal>

    <!-- 绑定实例到采集策略模板：实例必须归属某个策略模板，勾选绑定 / 取消勾选回落默认，无「解绑」语义 -->
    <a-modal
      v-model:open="strategyBindModal.open"
      width="860px"
      :confirm-loading="savingStrategyBind"
    >
      <template #title>
        <span class="modal-title-dot" aria-hidden="true"></span>绑定实例到模板：{{ strategyDisplayName(strategyBindModal.template) || strategyBindModal.template?.name || '—' }}
      </template>
      <template #footer>
        <a-space>
          <a-button @click="strategyBindModal.open = false">取消</a-button>
          <!-- 默认模板本身就是兜底，对它「回落默认」是无意义的空操作，不提供入口 -->
          <a-button v-if="!strategyBindModal.template?.is_default" :loading="savingStrategyBind" @click="submitStrategyBind('unbind')">回落到默认模板</a-button>
          <a-button type="primary" :loading="savingStrategyBind" @click="submitStrategyBind('bind')">绑定到该模板</a-button>
        </a-space>
      </template>
      <a-input-search
        v-model:value="strategyBindModal.search"
        placeholder="搜索数据源名称 / IP"
        style="width: 240px; margin-bottom: 12px"
        allow-clear
      />
      <a-table
        :columns="bindColumns"
        :data-source="strategyBindCandidates"
        row-key="instance_id"
        size="small"
        :pagination="{ pageSize: 8 }"
        :row-selection="{ selectedRowKeys: strategyBindModal.selected, onChange: onStrategyBindSelectionChange }"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'datasource'">
            <div class="bind-ds-cell">
              <div class="bind-ds-name">{{ record.name || '—' }}</div>
              <div class="bind-ds-target">{{ record.target || '—' }}</div>
            </div>
          </template>
          <template v-if="column.key === 'environment_name'">
            <span>{{ record.environment_name || '—' }}</span>
          </template>
          <template v-if="column.key === 'current'">
            <span>{{ record.strategy_template_label || '默认模板' }}</span>
          </template>
        </template>
      </a-table>
    </a-modal>

  </div>
</template>

<script setup>
import {
  bindMonitorStrategyInstancesApi,
  bindMonitorTemplateInstancesApi,
  collectMonitorInstanceApi,
  createMonitorStrategyTemplateApi,
  createMonitorTemplateApi,
  deleteMonitorStrategyTemplateApi,
  deleteMonitorTemplateApi,
  getMonitorMetricCatalogApi,
  getMonitorStatusApi,
  getMonitorStrategyTemplateDetailApi,
  getMonitorStrategyTemplatesApi,
  getMonitorTargetConfigsApi,
  getMonitorTemplateDetailApi,
  getMonitorTemplatesApi,
  unbindMonitorStrategyInstancesApi,
  updateMonitorStrategyTemplateApi,
  updateMonitorTargetConfigApi,
  updateMonitorTemplateApi,
} from '@/api/monitor'
import { message } from 'ant-design-vue'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { BellOutlined, PlusOutlined, QuestionCircleOutlined, SyncOutlined } from '@ant-design/icons-vue'
import { dbTypeLogo } from '@/utils/dbTypeLogo'
import PageHeader from '../components/PageHeader.vue'
// 监控模块的数据源白名单：只在这里维护可选范围，业务组件不再硬编码类型
import {
  decorateMonitorDbTypeOptions,
  filterMonitorDbTypeOptions,
  isMonitorDbTypeSupported,
  monitorDbTypeHint,
} from '../monitorDbTypes'
import {
  categoryLabel,
  collectStatusText,
  createDefaultStatus,
  createDefaultTemplateDetail,
  createDefaultTemplates,
  layerColor,
  layerLabel,
  metricLayerOf,
  qualityText,
} from '../utils'

defineOptions({ name: 'MonitorConfigView' })

const router = useRouter()
// 告警域（规则 / 通知与静默 / 健康分级）已迁到独立的「告警配置」页，这里只留入口
const gotoAlertConfig = () => router.push({ name: 'monitor.alert-config' })

const activeTab = ref('targets')
const loadingTargets = ref(false)
const loadingStatus = ref(false)
const loadingTemplates = ref(false)
const loadingTemplateDetail = ref(false)
const savingTarget = ref(false)
const savingTemplate = ref(false)
const savingBind = ref(false)

const filters = reactive({
  search: '',
  environment: undefined,
  dbType: undefined,
})

const templateFilter = reactive({
  dbType: undefined,
})

const targetState = reactive({
  filters: {
    environments: [],
    db_types: [],
    engines: [],
  },
  targets: [],
})

const statusState = reactive({
  value: createDefaultStatus(),
})

const templatesState = reactive({
  value: createDefaultTemplates(),
})

const templateDetailState = reactive({
  value: createDefaultTemplateDetail(),
  // 当前勾选界面可选项（来自内置指标清单接口，按引擎过滤后的引擎层+内核层指标）
  picker: [],
})

const targetModal = reactive({
  open: false,
  form: createTargetForm(),
})

// 数据质量下钻抽屉。
const qualityDrawer = reactive({
  open: false,
  target: '',
  items: [],
})

const hasQualityDetails = (record) =>
  record.collect_quality !== 'ok' && Array.isArray(record.quality_details) && record.quality_details.length > 0

const openQualityDrawer = (record) => {
  qualityDrawer.target = record.name || record.target || record.instance_id || '—'
  qualityDrawer.items = [...(record.quality_details || [])]
  qualityDrawer.open = true
}

const qualityDetailColumns = [
  { title: '指标', dataIndex: 'metric_label', key: 'metric_label', width: 210 },
  { title: '状态', dataIndex: 'quality', key: 'quality', width: 110 },
  { title: '原因', dataIndex: 'reason', key: 'reason' },
]

const templateModal = reactive({
  open: false,
  form: createTemplateForm(),
  builtinHint: '',
  dropped: 0,
})

const bindModal = reactive({
  open: false,
  template: null,
  candidates: [],
  selected: [],
})

const status = computed(() => statusState.value || createDefaultStatus())
const templates = computed(() => templatesState.value || createDefaultTemplates())
const templateDetail = computed(() => templateDetailState.value || createDefaultTemplateDetail())
const environmentOptions = computed(() => targetState.filters.environments || [])
// 顶部「全部类型」筛选：后端 filters.db_types 会带出实例里实际存在的类型，
// 但监控只有 MySQL / PostgreSQL 两套采集实现，其余类型选了也拿不到指标，
// 所以这里按监控白名单收窄，只露出 MySQL / PostgreSQL。
const dbTypeOptions = computed(() =>
  filterMonitorDbTypeOptions(
    (targetState.filters.db_types || []).map((item) => ({ label: item.label, value: item.value })),
  ),
)

// 引擎清单来自后端能力注册表：页面不再硬编码 MySQL / PostgreSQL
const engineList = computed(() => targetState.filters.engines || [])
// 模板下拉全部来自 /monitor/templates —— 前端不再写死 default/oltp/report 这类占位值
const sameEngine = (left, right) => String(left || '').toLowerCase() === String(right || '').toLowerCase()
// 模板列表筛选下拉：来自 /monitor/templates 的 db_types，再按白名单收窄
const templateDbTypeOptions = computed(() => filterMonitorDbTypeOptions(
  (templates.value.db_types || []).map((item) => ({ label: item.label, value: item.value })),
))
// 新建模板的「数据库类型」下拉：value 用引擎 key（mysql/postgresql），
// 与模板记录里的 db_type 同形，避免编辑态选不中；同时带上品牌 Logo 供插槽渲染。
const templateDbTypeFormOptions = computed(() => decorateMonitorDbTypeOptions(
  engineList.value
    .filter((item) => item.implemented)
    .map((item) => ({ label: item.label, value: item.key })),
))
// 指标模板列表：后端 API 仍保留全部类型，前端只展示 MySQL / PostgreSQL；
// 未来要放开时，只改 monitorDbTypes.js 的白名单即可。
const filteredTemplateList = computed(() =>
  (templates.value.templates || []).filter((item) =>
    isMonitorDbTypeSupported(item.db_type) || isMonitorDbTypeSupported(item.db_type_label),
  ),
)
const targetTemplateOptions = computed(() => (templates.value.templates || [])
  .filter((item) => item.enabled)
  .filter((item) => !targetModal.form.db_type || sameEngine(item.db_type, targetModal.form.db_type))
  .map((item) => ({
    label: `${item.display_name || item.name}`,
    value: item.name,
  })))
const copySourceOptions = computed(() => (templates.value.templates || [])
  .filter((item) => item.enabled && sameEngine(item.db_type, templateModal.form.db_type))
  .map((item) => ({
    label: `${item.display_name || item.name}（${item.metric_count} 项）`,
    value: item.name,
  })))

const deployModeOptions = [
  { label: '单机', value: 'standalone' },
  { label: '主从', value: 'master_slave' },
  { label: '分布式', value: 'cluster' },
]

const resourceSourceOptions = [
  { label: 'Prometheus', value: 'prometheus' },
  { label: '直连', value: 'direct' },
  { label: '关闭', value: 'none' },
]

const engineSourceOptions = [
  { label: '直连', value: 'direct' },
  { label: 'Prometheus', value: 'prometheus' },
  { label: '关闭', value: 'none' },
]

// ---------------------------------------------------------------------------
// 指标模板勾选界面
//
// 可选项来自内置指标清单接口（engine 过滤），默认用前后端同口径的层级前缀排除主机层 ——
// 主机层指标与数据库类型无关，不进模板，也就不该出现在勾选列表里制造误解。
//
// 例外：MySQL / PostgreSQL / 金仓 / Oracle / SQLServer / TiDB 的监控明细「基础指标」页展示了主机层指标
// （CPU / 内存 / 磁盘 / IOPS / Swap / 网络错误 / 文件句柄 / 时钟偏移），
// 默认模板已按展示目录同步，因此这些类型必须能勾选主机层，否则已勾选状态无法回显。
// 口径与后端 services.hostMetricsInTemplate 保持一致（改一处必须同步另一处）。
// 金仓（KingbaseES）展示目录整目录复用 PostgreSQL，host 层指标同样在「基础指标」Tab 里，
// 漏掉它会让用户新建金仓模板时看不到主机层 → 模板里没有 res.* →
// 明细页「基础指标」Tab 被模板门控整页剪掉，只剩「数据库指标」（2026-11-11 修复）。
// 注意：这里判断的是「主机层能否进模板」，与 monitorDbTypes.js 的对外开放白名单
// 是两件事 —— 前者跟随后端能力，后者只是前端入口收窄，不要互相复用。
// ---------------------------------------------------------------------------
const TEMPLATE_HOST_DB_TYPES = ['mysql', 'postgresql', 'kingbase', 'oracle', 'sqlserver', 'tidb']
const templateHostAllowed = computed(() => {
  const dbType = String(templateModal.form.db_type || '').toLowerCase()
  return TEMPLATE_HOST_DB_TYPES.includes(dbType)
})

// 明细口径行（无采集源）自带 layer 字段；字典口径行按 key 前缀推导
const pickerLayerOf = (item) => item.layer || metricLayerOf(item.metric_key)

const templatePickableItems = computed(() => (templateDetailState.picker || [])
  .filter((item) => templateHostAllowed.value || pickerLayerOf(item) !== 'host'))

const templatePickableCount = computed(() => templatePickableItems.value.filter((item) => item.enabled).length)

// 快速判断某个指标 key 是否处于已勾选状态（供单个复选框使用）
const templateMetricKeySet = computed(() => new Set(templateModal.form.metric_keys || []))

const templateMetricGroups = computed(() => {
  const selected = templateMetricKeySet.value
  const pickable = templatePickableItems.value
  // 层级集合从接口数据里推出来（默认排除不进模板的主机层），不在前端写死分组
  const layers = Array.from(new Set(pickable.map(pickerLayerOf)))
    .filter((layer) => templateHostAllowed.value || layer !== 'host')
    .sort((a, b) => ['host', 'engine', 'kernel'].indexOf(a) - ['host', 'engine', 'kernel'].indexOf(b))
  return layers
    .map((layer) => {
      const items = pickable.filter((item) => pickerLayerOf(item) === layer)
      const enabledItems = items.filter((item) => item.enabled)
      return {
        layer,
        label: layerLabel(layer, templateModal.form.db_type),
        items,
        total: items.length,
        selectable: enabledItems.length,
        // 无采集源（明细口径行）单独计数，不与"全局停用"混在一起
        noSource: items.filter((item) => item.available === false).length,
        disabled: items.filter((item) => !item.enabled && item.available !== false).length,
        // 父节点三态只统计「可选且已选」的子项；被全局停用的项不可勾，因此不应影响父节点状态
        selected: enabledItems.filter((item) => selected.has(item.metric_key)).length,
      }
    })
    .filter((group) => group.total > 0)
})

const targetColumns = [
  // 「数据源」列：左侧为数据库类型图标，右侧为名称 + IP:端口。
  // 名称取 record.name，IP:端口取 record.target。
  { title: '数据源', dataIndex: 'name', key: 'name', width: 260 },
  { title: '环境', dataIndex: 'environment_name', key: 'environment_name', width: 110 },
  { title: '启用监控', dataIndex: 'enabled', key: 'enabled', width: 90 },
  { title: '采集状态', dataIndex: 'last_collect_status', key: 'collect_status', width: 110 },
  { title: '数据质量', dataIndex: 'collect_quality', key: 'collect_quality', width: 110 },
  { title: '指标模板', dataIndex: 'template_label', key: 'template', width: 170 },
  { title: '业务', dataIndex: 'business', key: 'business', width: 110 },
  { title: '操作', key: 'actions', width: 170, fixed: 'right' },
]

// 采集策略 Tab 的两张表：
//   模板表 —— 策略的定义处（改这里，挂载实例立即继承，不做快照固化）
//   实例表 —— 只回答"挂在哪个模板 / 有没有被覆盖 / 是否发生回退"
// 实例表里不再逐列展示周期/保留/超时：那些值都继承自模板，
// 摆在这里只会让人以为"改这一格就能改这个实例"，而实际改不动。
const strategyTemplateColumns = [
  { title: '模版名称', dataIndex: 'name', key: 'name', width: 220 },
  { title: '数据库类型', dataIndex: 'db_type', key: 'db_type', width: 130 },
  { title: '策略概要', dataIndex: 'summary', key: 'summary', ellipsis: true },
  { title: '生效实例', dataIndex: 'effective_instances', key: 'effective_instances', width: 100 },
  { title: '状态', dataIndex: 'enabled', key: 'enabled', width: 90 },
  { title: '操作', key: 'actions', width: 220, fixed: 'right' },
]

// 指标模板列表列
const templateColumns = [
  { title: '模版名称', dataIndex: 'name', key: 'name', width: 220 },
  { title: '数据库类型', dataIndex: 'db_type', key: 'db_type', width: 130 },
  { title: '指标数', dataIndex: 'metric_count', key: 'metric_count', width: 100 },
  { title: '生效实例', dataIndex: 'bound_instances', key: 'bound_instances', width: 110 },
  { title: '状态', dataIndex: 'enabled', key: 'enabled', width: 90 },
  { title: '说明', dataIndex: 'description', key: 'description', ellipsis: true },
  { title: '操作', key: 'actions', width: 220, fixed: 'right' },
]

// 绑定实例弹窗列（指标模板 / 采集策略两弹窗共用）：数据源（名称+ip:port）/ 类型 / 环境 / 当前生效模板。
// 不设横向滚动，数据源与当前生效模板两列不定宽，自动填满表格区域避免尾部空白。
const bindColumns = [
  { title: '数据源', key: 'datasource', ellipsis: true },
  { title: '类型', dataIndex: 'db_type', key: 'db_type', width: 110 },
  { title: '环境', dataIndex: 'environment_name', key: 'environment_name', width: 110 },
  { title: '当前生效模板', key: 'current' },
]

const engineColumns = [
  { title: '引擎', dataIndex: 'label', key: 'label', width: 140 },
  { title: '采集能力', dataIndex: 'implemented', key: 'implemented', width: 120 },
  { title: '实例数', dataIndex: 'instances', key: 'instances', width: 90 },
  { title: '采集成功', dataIndex: 'healthy', key: 'healthy', width: 90 },
  { title: '采集失败', dataIndex: 'failed', key: 'failed', width: 90 },
  { title: '最近采集', dataIndex: 'latest_at', key: 'latest_at' },
]

// 健康度「按引擎汇总」当前只展示 MySQL / PostgreSQL 两行；
// 后端 /monitor/status 仍返回全部引擎，恢复其他引擎展示时只需扩充这个白名单。
const visibleHealthEngines = ['mysql', 'postgresql']
const healthEngines = computed(() =>
  (status.value.engines || []).filter((item) => visibleHealthEngines.includes(item.key))
)

const failureColumns = [
  { title: '实例', dataIndex: 'target', key: 'target', width: 190 },
  { title: '类型', dataIndex: 'db_type', key: 'db_type', width: 110 },
  { title: '连续失败', dataIndex: 'consecutive_failures', key: 'consecutive_failures', width: 100 },
  { title: '最近采集', dataIndex: 'last_collect_at', key: 'last_collect_at', width: 170 },
  { title: '失败原因', dataIndex: 'message', key: 'message' },
]

const healthTiles = computed(() => {
  const health = status.value.collect
  const runner = status.value.runner
  return [
    { key: 'ok', label: '采集正常', value: health.ok, tone: 'good', note: `后台每 ${runner.collect_interval_seconds} 秒调度一轮` },
    { key: 'failed', label: '采集失败', value: health.failed, tone: health.failed ? 'bad' : '', note: '连不上 / 认证失败，指标不可信' },
    { key: 'stale', label: '数据滞后或未采集', value: health.stale_or_never, tone: health.stale_or_never ? 'warn' : '', note: '连续失败会触发退避，最长 10 分钟一轮' },
    { key: 'partial', label: '部分指标不适用', value: health.partial + health.unsupported, tone: health.partial + health.unsupported ? 'warn' : '', note: '例如 PG 未安装 pg_stat_statements' },
    { key: 'latest', label: '最近采集时间', value: runner.latest_collect_at || '—', tone: '', note: `并发 ${runner.workers} 个采集 worker` },
    { key: 'ms', label: '平均采集耗时', value: `${runner.average_collect_ms} ms`, tone: '', note: `最慢 ${runner.slowest_collect_ms} ms` },
    { key: 'fresh', label: '最新数据新鲜度', value: health.fresh_seconds ? `${health.fresh_seconds} 秒` : '—', tone: '', note: '超过 180 秒视为滞后' },
    { key: 'limit', label: '页面即时补采上限', value: runner.on_demand_limit, tone: '', note: '防止页面退化成采集器' },
  ]
})

function createTargetForm(record = {}) {
  return {
    instance_id: record.instance_id || '',
    // db_type 只用于前端过滤模板下拉，不参与提交（后端会按实例真实类型校验）
    db_type: record.db_type || '',
    name: record.name || '',
    target: record.target || '',
    enabled: record.enabled ?? true,
    business: record.business || '',
    group_name: record.group_name || '',
    deploy_mode: record.deploy_mode || 'standalone',
    prometheus_target: record.prometheus_target || '',
    resource_source: record.resource_source || 'prometheus',
    engine_source: record.engine_source || 'direct',
    // 0 = 跟随平台周期。只有实例显式设置过（follows=false）才回填显式值；
    // 否则会把平台生效值物化成"自定义"，平台以后改周期就带不动它了。
    scrape_interval_seconds: record.scrape_interval_follows_platform === false ? record.scrape_interval_seconds || 0 : 0,
    retention_hours: record.retention_hours || 168,
    timeout_seconds: record.timeout_seconds || 5,
    retry_count: record.retry_count ?? 2,
    metrics_template: record.metrics_template || '',
    labels: [...(record.labels || [])],
  }
}

function createTemplateForm(record = {}) {
  return {
    id: record.id || undefined,
    name: record.name || '',
    display_name: record.display_name || '',
    db_type: record.db_type || '',
    description: record.description || '',
    enabled: record.enabled ?? true,
    copy_from: undefined,
    metric_keys: [],
    builtin: !!record.builtin,
    is_default: !!record.is_default,
  }
}

const reloadTargets = async () => {
  loadingTargets.value = true
  try {
    const params = {}
    if (filters.search) params.search = filters.search
    if (filters.environment) params.environment = filters.environment
    if (filters.dbType) params.db_type = filters.dbType
    const res = await getMonitorTargetConfigsApi(params)
    targetState.filters = res?.data?.filters || { environments: [], db_types: [], engines: [] }
    targetState.targets = res?.data?.targets || []
  } catch {
    message.error('监控对象配置加载失败')
  } finally {
    loadingTargets.value = false
  }
}

const reloadStatus = async () => {
  loadingStatus.value = true
  try {
    const res = await getMonitorStatusApi()
    statusState.value = res?.data ? { ...createDefaultStatus(), ...res.data } : createDefaultStatus()
  } catch {
    message.error('采集健康度加载失败')
  } finally {
    loadingStatus.value = false
  }
}

const reloadAll = async () => {
  await Promise.all([reloadTargets(), reloadStatus(), reloadTemplates(), reloadStrategyTemplates()])
}

// ---------------------------------------------------------------------------
// 采集策略：模板 - 实例 两级配置
//
// 页面只做两件事：编辑模板里的策略项、决定实例挂哪个模板。
// 实例不直接配周期 / 保留 / 超时 / 重试 —— 那些值全部来自模板，改模板即改全部挂载实例
// （实时继承，不做快照固化）。个别实例要特殊照顾走「覆盖项」，而不是复制一个新模板。
//
// 策略项的具体种类由后端注册表下发（strategyTypes），前端按 value_kind 动态渲染控件：
// 新增一种策略不需要改这个页面一行代码。
// ---------------------------------------------------------------------------

const strategyState = reactive({ value: { templates: [], types: [], db_types: [], engines: [] } })
const strategyFilter = reactive({ dbType: undefined })
const loadingStrategyTemplates = ref(false)
const loadingStrategyDetail = ref(false)
const savingStrategy = ref(false)
const savingStrategyBind = ref(false)

const strategyTemplates = computed(() => strategyState.value?.templates || [])
const strategyTypes = computed(() => strategyState.value?.types || [])
// 与指标模板同一套白名单：目前只对外开放 MySQL / PostgreSQL
const filteredStrategyTemplates = computed(() =>
  strategyTemplates.value.filter((item) => isMonitorDbTypeSupported(item.db_type) || isMonitorDbTypeSupported(item.db_type_label)),
)
const strategyDbTypeOptions = computed(() => filterMonitorDbTypeOptions(
  (strategyState.value?.db_types || []).map((item) => ({ label: item.label, value: item.value })),
))
const strategySummary = (record) => record.summary || '—'

// 模板列展示名：剥掉系统生成的「默认采集策略（MySQL）」这类尾部括号。
// 括号里的类型名与「数据库类型」列重复，去掉后两个默认模板也不会看起来像同一个。
// 只处理内置默认模板，用户自建模板的展示名一个字都不动。
// record 允许为 null（弹窗标题在未选中模板时会以 null 调用），统一回退空串。
const strategyDisplayName = (record) => {
  const name = record?.display_name || record?.name || ''
  if (!record?.builtin || !record?.is_default) return name
  return name.replace(/（[^）]*）\s*$/, '').trim() || name
}
// 新建模板的类型下拉：与监控模块白名单一致
const strategyDbTypeFormOptions = computed(() => decorateMonitorDbTypeOptions(
  engineList.value.filter((item) => item.implemented).map((item) => ({ label: item.label, value: item.key })),
))

// 「复制已有模板」下拉：与当前所选数据库类型相同的模板，按展示名排列（仅创建态渲染）
const strategyCopySourceOptions = computed(() =>
  (strategyState.value.templates || [])
    .filter((item) => item.db_type === strategyModal.form.db_type)
    .map((item) => ({ label: item.display_name || item.name, value: item.name })),
)

// 选择复制来源后立即载入其策略项作为初值，用户可在此基础上微调；
// 清空选择不回退（保留当前已填内容），与指标模板弹窗的 onCopyFromChange 口径一致
const onStrategyCopyFromChange = async (name) => {
  if (!name) return
  const source = (strategyState.value.templates || []).find((item) => item.name === name)
  if (!source?.id) return
  loadingStrategyDetail.value = true
  try {
    const res = await getMonitorStrategyTemplateDetailApi(source.id)
    const detail = res?.data || {}
    const items = {}
    for (const spec of strategyTypes.value) items[spec.type] = spec.default
    for (const row of detail.items || []) {
      if (row.orphan || row.item_key) continue
      items[row.item_type] = row.value
    }
    strategyModal.items = items
    message.success(`已载入「${source.display_name || source.name}」的策略项，可继续调整`)
  } catch {
    message.error('策略项载入失败')
  } finally {
    loadingStrategyDetail.value = false
  }
}

const reloadStrategyTemplates = async () => {
  loadingStrategyTemplates.value = true
  try {
    const params = {}
    if (strategyFilter.dbType) params.db_type = strategyFilter.dbType
    const res = await getMonitorStrategyTemplatesApi(params)
    strategyState.value = res?.data ? { ...{ templates: [], types: [], db_types: [], engines: [] }, ...res.data } : { templates: [], types: [], db_types: [], engines: [] }
  } catch {
    message.error('采集策略模板加载失败')
  } finally {
    loadingStrategyTemplates.value = false
  }
}

const createStrategyForm = () => ({ name: '', display_name: '', db_type: 'mysql', description: '', enabled: true, copy_from: '' })

const strategyModal = reactive({
  open: false,
  editingId: null,
  form: createStrategyForm(),
  // items: 策略项类型 -> 值。键集合由后端注册表决定，页面不硬编码任何策略名
  items: {},
})

// 弹窗里的策略项行：按注册表顺序渲染，值缺省取注册表的 default
const strategyItemRows = computed(() => strategyTypes.value.map((spec) => ({
  spec,
  value: strategyModal.items[spec.type] ?? spec.default,
})))

const openStrategyModal = async (record) => {
  strategyModal.editingId = record?.id || null
  if (record) {
    loadingStrategyDetail.value = true
    try {
      const res = await getMonitorStrategyTemplateDetailApi(record.id)
      const detail = res?.data || {}
      strategyModal.form = {
        name: detail.template?.name || record.name || '',
        display_name: detail.template?.display_name || record.display_name || '',
        db_type: detail.template?.db_type || record.db_type || 'mysql',
        description: detail.template?.description || record.description || '',
        enabled: detail.template?.enabled ?? record.enabled ?? true,
        copy_from: '',
      }
      const items = {}
      for (const spec of strategyTypes.value) items[spec.type] = spec.default
      for (const row of detail.items || []) {
        if (row.orphan || row.item_key) continue
        items[row.item_type] = row.value
      }
      strategyModal.items = items
    } catch {
      message.error('采集策略模板详情加载失败')
      return
    } finally {
      loadingStrategyDetail.value = false
    }
  } else {
    strategyModal.form = createStrategyForm()
    const items = {}
    for (const spec of strategyTypes.value) items[spec.type] = spec.default
    strategyModal.items = items
  }
  strategyModal.open = true
}

// 策略模板标识同样不再由用户输入：按库类型 + 创建时间自动生成，如 mysql-custom-20260919-142500
function autoStrategyTemplateName(dbType) {
  const slug = String(dbType || 'db').trim().toLowerCase().replace(/[^a-z0-9_-]/g, '') || 'db'
  const now = new Date()
  const pad = (n) => String(n).padStart(2, '0')
  const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`
  return `${slug}-custom-${stamp}`
}

const submitStrategyTemplate = async () => {
  savingStrategy.value = true
  try {
    const items = Object.entries(strategyModal.items || {}).map(([item_type, value]) => ({ item_type, item_key: '', value }))
    // 模板标识输入项已移除：创建态由系统按「库类型-custom-时间戳」自动生成，编辑态沿用原值
    if (!strategyModal.editingId && !strategyModal.form.name) {
      strategyModal.form.name = autoStrategyTemplateName(strategyModal.form.db_type)
    }
    const payload = {
      name: strategyModal.form.name,
      display_name: strategyModal.form.display_name,
      db_type: strategyModal.form.db_type,
      description: strategyModal.form.description,
      enabled: strategyModal.form.enabled,
      items,
    }
    if (strategyModal.editingId) {
      await updateMonitorStrategyTemplateApi(strategyModal.editingId, payload)
      message.success('采集策略模板已保存，挂载实例立即生效')
    } else {
      payload.copy_from = strategyModal.form.copy_from || ''
      await createMonitorStrategyTemplateApi(payload)
      message.success('采集策略模板已创建')
    }
    strategyModal.open = false
    await Promise.all([reloadStrategyTemplates(), reloadTargets()])
  } catch (error) {
    message.error(error?.message || '保存失败')
  } finally {
    savingStrategy.value = false
  }
}

const removeStrategyTemplate = async (record) => {
  try {
    await deleteMonitorStrategyTemplateApi(record.id)
    message.success('采集策略模板已删除')
    await reloadStrategyTemplates()
  } catch (error) {
    message.error(error?.message || '删除失败')
  }
}

// --- 已挂载实例管理 ---
const strategyBindModal = reactive({
  open: false,
  template: null,
  search: '',
  // selected: 勾选中的 instance_id 列表；打开时预勾选已挂载到该模板的实例
  selected: [],
})

// 绑定弹窗候选实例 = 同类型监控对象（策略模板只能挂同类型实例），支持按名称 / IP 搜索。
// 已挂载到当前模板的在打开时预勾选。
const strategyBindCandidates = computed(() => {
  const tmpl = strategyBindModal.template
  if (!tmpl) return []
  const keyword = (strategyBindModal.search || '').trim().toLowerCase()
  return (targetState.targets || [])
    .filter((item) => sameEngine(item.db_type, tmpl.db_type_label || tmpl.db_type))
    .filter((item) => {
      if (!keyword) return true
      return (item.name || '').toLowerCase().includes(keyword) || (item.target || '').toLowerCase().includes(keyword)
    })
})

const openStrategyBindModal = async (record) => {
  strategyBindModal.template = record
  strategyBindModal.search = ''
  strategyBindModal.selected = []
  strategyBindModal.open = true
  if (!targetState.targets.length) await reloadTargets()
  // 预勾选 = 当前实际生效于该模板的实例：显式挂载的 +（默认模板时）未挂载而由它兜底的。
  // 否则编辑默认模板时勾选全空，看起来像"实例没绑任何策略模板"。
  strategyBindModal.selected = (targetState.targets || [])
    .filter((item) => sameEngine(item.db_type, record.db_type_label || record.db_type))
    .filter((item) => item.strategy_template_id === record.id || (record.is_default && !item.strategy_template_id))
    .map((item) => item.instance_id)
}

const onStrategyBindSelectionChange = (keys) => {
  strategyBindModal.selected = keys
}

const submitStrategyBind = async (mode) => {
  const tmpl = strategyBindModal.template
  if (!tmpl?.id) return
  if (!strategyBindModal.selected.length) {
    message.error(mode === 'bind' ? '请勾选要绑定到该模板的实例' : '请勾选要回落到默认模板的实例')
    return
  }
  savingStrategyBind.value = true
  try {
    if (mode === 'bind') {
      const res = await bindMonitorStrategyInstancesApi(tmpl.id, { instance_ids: strategyBindModal.selected })
      const bound = res?.data?.bound ?? strategyBindModal.selected.length
      message.success(`已将 ${bound} 个实例挂载到该模板`)
    } else {
      const res = await unbindMonitorStrategyInstancesApi({ instance_ids: strategyBindModal.selected })
      const notes = res?.data?.notes || []
      const bound = res?.data?.bound ?? strategyBindModal.selected.length
      message.success(`已将 ${bound} 个实例回落到默认模板`)
      for (const note of notes) message.warning(note)
    }
    strategyBindModal.open = false
    await Promise.all([reloadStrategyTemplates(), reloadTargets()])
  } catch (error) {
    message.error(error?.message || '操作失败')
  } finally {
    savingStrategyBind.value = false
  }
}

// ---------------------------------------------------------------------------
// 指标模板：列表 / 勾选界面 / 绑定实例，全部走 /monitor/templates 系列接口
// ---------------------------------------------------------------------------

const reloadTemplates = async () => {
  loadingTemplates.value = true
  try {
    const params = {}
    if (templateFilter.dbType) params.db_type = templateFilter.dbType
    const res = await getMonitorTemplatesApi(params)
    templatesState.value = res?.data ? { ...createDefaultTemplates(), ...res.data } : createDefaultTemplates()
  } catch {
    message.error('指标模板加载失败')
  } finally {
    loadingTemplates.value = false
  }
}

/** 勾选界面可选项：来自内置指标清单接口，按当前模板的数据库类型过滤。
 *  MySQL / PostgreSQL / Oracle（含共用 MySQL 目录的 TiDB / Doris）传 layout=1，
 *  后端按「监控明细展示目录」输出：多值指标逐行展开、保持明细顺序，
 *  无采集源的行 available=false（界面标记不可用、不可勾），与明细页逐行对账。
 *  Oracle 纳入的原因：它的明细页同样按展示目录出「基础指标」Tab（含主机层），
 *  勾选清单若回落到字典口径，主机层会被 collectScopeEnabled 过滤掉，
 *  模板里已勾选的主机指标就无处回显。
 *  SQL Server 同上：它有自己的展示目录（基础指标 + 数据库指标两个 Tab），
 *  且默认模板按展示目录播种，必须走 layout=1 才能与明细页逐行对账。
 *  金仓同理：展示目录整目录复用 PostgreSQL（两个 Tab，含主机层），必须走 layout=1，
 *  否则勾选清单回落到字典口径时主机层被过滤，模板里永远勾不到 res.*。 */
const LAYOUT_ENGINE_PICKER = ['mysql', 'postgresql', 'kingbase', 'oracle', 'sqlserver', 'tidb', 'doris']

const reloadTemplatePicker = async (engineKey) => {
  if (!engineKey) {
    templateDetailState.picker = []
    return
  }
  try {
    const params = { engine: engineKey }
    if (LAYOUT_ENGINE_PICKER.includes(String(engineKey).toLowerCase())) params.layout = 1
    const res = await getMonitorMetricCatalogApi(params)
    templateDetailState.picker = res?.data?.metrics || []
  } catch {
    templateDetailState.picker = []
    message.error('可选指标加载失败')
  }
}

/** 读取模板详情；把已选指标裁剪到"当前适用的可选项"内，并回报被丢弃的历史项数量。 */
const loadTemplateDetail = async (id) => {
  loadingTemplateDetail.value = true
  try {
    const res = await getMonitorTemplateDetailApi(id)
    templateDetailState.value = res?.data ? { ...createDefaultTemplateDetail(), ...res.data } : createDefaultTemplateDetail()
    const applicable = new Set(templatePickableItems.value.filter((item) => item.enabled).map((item) => item.metric_key))
    const selected = (templateDetail.value.metrics || []).map((item) => item.metric_key)
    const kept = selected.filter((key) => applicable.has(key))
    templateModal.dropped = selected.length - kept.length
    return kept
  } catch (error) {
    message.error(error?.message || '模板详情加载失败')
    return []
  } finally {
    loadingTemplateDetail.value = false
  }
}

const openTemplateModal = async (record) => {
  templateModal.form = createTemplateForm(record || {})
  templateModal.dropped = 0
  templateModal.builtinHint = ''
  if (!templateModal.form.db_type) {
    templateModal.form.db_type = templateDbTypeFormOptions.value[0]?.value || ''
  }
  templateModal.open = true
  loadingTemplateDetail.value = true
  await reloadTemplatePicker(templateModal.form.db_type)
  if (record?.id) {
    templateModal.form.metric_keys = await loadTemplateDetail(record.id)
  } else {
    loadingTemplateDetail.value = false
  }
}

const onTemplateDbTypeChange = async (value) => {
  templateModal.form.metric_keys = []
  templateModal.dropped = 0
  await reloadTemplatePicker(value)
}

const onCopyFromChange = async (name) => {
  if (!name) return
  const source = (templates.value.templates || []).find((item) => item.name === name)
  if (!source?.id) return
  loadingTemplateDetail.value = true
  const keys = await loadTemplateDetail(source.id)
  templateModal.form.metric_keys = keys
  if (templateModal.dropped > 0) {
    message.warning(`已跳过 ${templateModal.dropped} 项当前不适用的指标`)
  }
  loadingTemplateDetail.value = false
}

const normalizeTemplateNameInput = () => {
  const value = (templateModal.form.name || '').trim().toLowerCase()
  templateModal.form.name = value.replace(/[^a-z0-9_-]/g, '-')
}

// 模板标识不再由用户输入：按库类型 + 创建时间自动生成，如 mysql-custom-20260917-173741
function autoTemplateName(dbType) {
  const slug = String(dbType || 'db').trim().toLowerCase().replace(/[^a-z0-9_-]/g, '') || 'db'
  const now = new Date()
  const pad = (n) => String(n).padStart(2, '0')
  const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`
  return `${slug}-custom-${stamp}`
}

// 单个指标勾选/取消：直接操作 Set，避免多个 a-checkbox-group 共享 v-model 带来的跨层清空
const toggleMetricKey = (key, event) => {
  const current = new Set(templateModal.form.metric_keys || [])
  if (event?.target?.checked ?? event) {
    current.add(key)
  } else {
    current.delete(key)
  }
  templateModal.form.metric_keys = Array.from(current)
}

const toggleTemplateGroup = (group, event) => {
  const selectable = group.items.filter((item) => item.enabled).map((item) => item.metric_key)
  const current = new Set(templateModal.form.metric_keys || [])
  if (event?.target?.checked ?? event) {
    selectable.forEach((key) => current.add(key))
  } else {
    selectable.forEach((key) => current.delete(key))
  }
  templateModal.form.metric_keys = Array.from(current)
}

const selectAllTemplateMetrics = () => {
  // 变量名是 templatePickableItems（可选指标平铺列表）；此前误写成不存在的
  // templatePickerItems，点「全选可选」会直接抛 ReferenceError。
  templateModal.form.metric_keys = templatePickableItems.value
    .filter((item) => item.enabled)
    .map((item) => item.metric_key)
}

const clearTemplateMetrics = () => {
  templateModal.form.metric_keys = []
}

const metricPickerTooltip = (item) => {
  // 无采集源行没有真实采集键，伪键不对外展示
  const parts = [
    item.available === false
      ? `${item.display_name}（无采集源）`
      : `${item.display_name}（${item.metric_key}）`,
  ]
  if (item.detail_item) parts.push(`所属监控项：${item.detail_item}`)
  if (item.category_label || item.category) {
    parts.push(`分类：${item.category_label || categoryLabel(item.category)}`)
  }
  if (item.unit) parts.push(`单位：${item.unit}`)
// 无采集源行的 advice 已带明细口径说明与"不参与采集"的原因；
// 原「在指标字典中被全局停用」的提示随字典一并下线 —— 不再有字典总闸这一层。
  if (item.advice) parts.push(item.advice)
  return parts.join(' | ')
}

const submitTemplate = async () => {
  const form = templateModal.form
  if (!form.id) {
    // 模板标识输入项已移除：系统按「库类型-custom-时间戳」自动生成，保证唯一
    if (!form.name) form.name = autoTemplateName(form.db_type)
    else normalizeTemplateNameInput()
  }
  if (!form.db_type) {
    message.error(`请选择数据库类型（当前支持：${monitorDbTypeHint()}）`)
    return
  }
  // 白名单兜底：后端仍保留其它类型的能力，但前端不提供入口，
  // 这里拦住任何绕过下拉的取值，避免建出指标为空的模板。
  if (!form.id && !isMonitorDbTypeSupported(form.db_type)) {
    message.error(`该数据库类型暂不支持指标模板，当前支持：${monitorDbTypeHint()}`)
    return
  }
  if (!form.metric_keys.length) {
    message.error('至少勾选一个指标；空模板会让实例采不到任何引擎指标')
    return
  }
  savingTemplate.value = true
  try {
    if (form.id) {
      const payload = {
        display_name: form.display_name,
        description: form.description,
        enabled: form.enabled,
      }
      // 内置模板的指标集合随发版补齐，后端会拒绝裁剪，这里就不发
      if (!form.builtin) payload.metric_keys = form.metric_keys
      await updateMonitorTemplateApi(form.id, payload)
      message.success('模板已更新，下一个采集周期生效')
    } else {
      await createMonitorTemplateApi({
        name: form.name,
        display_name: form.display_name,
        db_type: form.db_type,
        description: form.description,
        enabled: form.enabled,
        metric_keys: form.metric_keys,
      })
      message.success('模板已创建')
    }
    templateModal.open = false
    await reloadTemplates()
  } catch (error) {
    message.error(error?.message || '模板保存失败')
  } finally {
    savingTemplate.value = false
  }
}

const removeTemplate = async (record) => {
  try {
    await deleteMonitorTemplateApi(record.id)
    message.success('模板已删除')
    await Promise.all([reloadTemplates(), reloadTargets()])
  } catch (error) {
    message.error(error?.message || '模板删除失败')
  }
}

const openBindModal = async (record) => {
  bindModal.template = record
  bindModal.selected = []
  bindModal.open = true
  // 候选实例 = 同类型的监控对象（模板只能绑同类型实例，后端同样会校验）
  if (!targetState.targets.length) await reloadTargets()
  bindModal.candidates = (targetState.targets || [])
    .filter((item) => sameEngine(item.db_type, record.db_type_label || record.db_type))
  bindModal.selected = bindModal.candidates
    .filter((item) => item.metrics_template === record.name)
    .map((item) => item.instance_id)
}

const onBindSelectionChange = (keys) => {
  bindModal.selected = keys
}

const submitBind = async (mode) => {
  if (!bindModal.template?.id) return
  if (!bindModal.selected.length) {
    message.error('请选择要操作的实例')
    return
  }
  savingBind.value = true
  try {
    const res = await bindMonitorTemplateInstancesApi(bindModal.template.id, {
      instance_ids: bindModal.selected,
      mode,
    })
    const bound = res?.data?.bound ?? bindModal.selected.length
    message.success(mode === 'unbind' ? `已将 ${bound} 个实例回落到默认模板` : `已绑定 ${bound} 个实例`)
    bindModal.open = false
    await Promise.all([reloadTemplates(), reloadTargets()])
  } catch (error) {
    message.error(error?.message || '绑定失败')
  } finally {
    savingBind.value = false
  }
}

// 模板绑定状态标签。口径由后端下发（template_state），前端只做映射，不重复判定：
//   unbound  = 未绑定模版（空值 / 旧占位值 default），用该类型默认模板，属正常状态 → 中性色
//   fallback = 绑定的模板已失效（被删/停用/改名），回落到默认模板，需人工处理     → 橙色
//   bound    = 显式绑定且生效，列表里无需额外标签
// 未绑定的实例以前被标成橙色「回落默认」，是虚惊（狼来了），这里按语义拆开。
const TEMPLATE_STATE_TAG = {
  unbound: { text: '未绑定模版', color: 'default', tip: '未绑定指标模板，自动使用该类型的默认模板' },
  fallback: { text: '回落默认', color: 'orange', tip: '绑定的模板不存在或已停用，当前回落到该类型的默认模板' },
}

// 兼容旧后端（无 template_state）时退化为按 template_fallback 判断，避免标签整体消失
const templateTagOf = (record) => {
  const state = record?.template_state || (record?.template_fallback ? 'fallback' : '')
  return TEMPLATE_STATE_TAG[state] || null
}

const templateTooltip = (record) => {
  const parts = [`生效模板：${record.template_label || record.metrics_template || '—'}`]
  if (record.template_metric_count) parts.push(`模板指标数：${record.template_metric_count}`)
  const tag = templateTagOf(record)
  if (tag) parts.push(tag.tip)
  return parts.join(' | ')
}

// 兜底：类型下拉按白名单收窄后，若当前筛选值已不在可选范围内（历史残留/接口回填），
// 清空条件并重载，避免筛出空列表却看不到任何提示。
watch(dbTypeOptions, (options) => {
  if (!filters.dbType) return
  if (options.some((item) => item.value === filters.dbType)) return
  filters.dbType = undefined
  reloadTargets()
})

// 模板列表的类型筛选下拉也已收窄，历史残留值自动清空并重载。
watch(templateDbTypeOptions, (options) => {
  if (!templateFilter.dbType) return
  if (options.some((item) => item.value === templateFilter.dbType)) return
  templateFilter.dbType = undefined
  reloadTemplates()
})

watch(activeTab, (value) => {
  if (value === 'health') reloadStatus()
  if (value === 'templates' && !templatesState.value.templates?.length) reloadTemplates()
  // 切 Tab 后重算：懒渲染的 pane 此时才有 DOM，能测到真实的 top
  nextTick(syncTableBodyHeight)
})

const openTargetModal = (record) => {
  targetModal.form = createTargetForm(record)
  targetModal.open = true
}

const submitTarget = async () => {
  savingTarget.value = true
  try {
    const payload = { ...targetModal.form }
    delete payload.instance_id
    delete payload.name
    delete payload.target
    delete payload.db_type
    await updateMonitorTargetConfigApi(targetModal.form.instance_id, payload)
    message.success('监控对象配置已保存')
    targetModal.open = false
    await Promise.all([reloadTargets(), reloadTemplates()])
  } catch (error) {
    message.error(error?.message || '监控对象配置保存失败')
  } finally {
    savingTarget.value = false
  }
}

const collectNow = async (record) => {
  try {
    const res = await collectMonitorInstanceApi(record.instance_id)
    const result = res?.data
    if (result?.quality === 'ok') {
      message.success(`采集完成，耗时 ${result.elapsed_ms} ms`)
    } else {
      message.warning(result?.status_message || '采集完成，但存在不可用指标')
    }
    await Promise.all([reloadTargets(), reloadStatus()])
  } catch (error) {
    message.error(error?.message || '立即采集失败，请检查实例连通性')
  }
}

// 表格纵向滚动：让滚动条落在表格内部，而不是父容器（.layout-content）最右侧。
// 高度按「表格顶部 → 内容区底部」的实测距离算，而不是写死「视口高度 - 常量」——
// 后者在不同分辨率/布局下容易算大，内容溢出又把滚动条顶回页面级。
const targetsHost = ref(null)
const strategyHost = ref(null)
const templatesHost = ref(null)
const tableBodyHeight = reactive({ targets: 420, strategy: 420, templates: 420 })
// 底部预留：表头(约 48) + 分页器(32 + 16 上边距) + 卡片内边距(16) + 内容区 padding(24) + 安全余量(24)。
// 这些都在表格宿主元素内部/下方，不预留就会把内容顶出 .layout-content，滚动条又回到父容器上。
// 取值宁大勿小：多出来只是底部留白，小了会重新触发页面级滚动条。
const TABLE_BOTTOM_RESERVE = 160
let fitRetry = 0
const syncTableBodyHeight = () => {
  const hosts = {
    targets: targetsHost.value,
    strategy: strategyHost.value,
    templates: templatesHost.value,
  }
  Object.entries(hosts).forEach(([key, el]) => {
    // 未渲染过、或被 Tab 隐藏（display:none 时 offsetParent 为 null）拿不到有效位置，跳过
    if (!el || !el.offsetParent) return
    const container = el.closest('.layout-content')
    const bottom = container ? container.getBoundingClientRect().bottom : window.innerHeight
    // 两个值都是视口坐标，相减与页面滚动位置无关
    const top = el.getBoundingClientRect().top
    // 下限取小一些：矮窗口下也能靠表格内滚消化，不把内容顶出内容区
    tableBodyHeight[key] = Math.max(180, Math.round(bottom - top - TABLE_BOTTOM_RESERVE))
  })
  // 兜底闭环：预留值是估算，若内容区仍有溢出（滚动条又冒出来），按实际溢出量再收一次表格高度
  fitRetry = 0
  nextTick(fitIntoContent)
}

// 收敛校正：内容区 scrollHeight 超出 clientHeight 多少，就把当前 Tab 的表格高度减多少
const fitIntoContent = () => {
  const key = activeTab.value
  if (!(key in tableBodyHeight)) return
  const container = document.querySelector('.layout-content')
  if (!container) return
  const delta = Math.round(container.scrollHeight - container.clientHeight)
  if (delta <= 0) return
  const next = Math.max(180, tableBodyHeight[key] - delta)
  if (next === tableBodyHeight[key]) return
  tableBodyHeight[key] = next
  if (fitRetry < 3) {
    fitRetry += 1
    nextTick(fitIntoContent)
  }
}

onMounted(() => {
  reloadAll()
  // 等首屏 DOM 布局完成再量高度
  nextTick(syncTableBodyHeight)
  window.addEventListener('resize', syncTableBodyHeight)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', syncTableBodyHeight)
})
</script>

<style scoped>
.config-page {
  display: flex;
  flex-direction: column;
  gap: var(--gi-spacing-ssm);
}

/* 弹窗标题前的小圆点标识（绑定实例弹窗与指标模板绑定弹窗风格一致） */
.modal-title-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  margin-right: 8px;
  border-radius: 50%;
  background: var(--gi-primary, #0f766e);
  vertical-align: middle;
}

/* 绑定实例弹窗「数据源」列：第一行名称、第二行 ip:port（小号淡灰） */
.bind-ds-cell {
  line-height: 1.4;
}
.bind-ds-name {
  color: var(--gi-text-primary, #1f2937);
}
.bind-ds-target {
  margin-top: 2px;
  font-size: 12px;
  color: var(--gi-text-tertiary, #8fa0ac);
}

/* 策略项编辑行：名称 + 控件 + 口径说明，控件类型由后端注册表决定 */
/* 策略项：两列网格，列宽均分；容器收窄（窄屏/弹窗调窄）自动回落单列，布局不错乱 */
.strategy-item-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 20px;
}
@media (max-width: 640px) {
  .strategy-item-grid {
    grid-template-columns: 1fr;
  }
}
.strategy-item-row {
  padding: 8px 0;
  border-bottom: 1px dashed var(--gi-color-border, #e5e8eb);
}
.strategy-item-row:last-child {
  border-bottom: none;
}
.strategy-item-main {
  display: flex;
  align-items: center;
  gap: 10px;
}
.strategy-item-name {
  flex: none;
  min-width: 96px;
}
/* 策略项问号图标：紧跟名称文字，点击弹出解释 tooltip */
.strategy-item-help {
  margin-left: 3px;
  font-size: 13px;
  color: var(--gi-text-tertiary, #8fa0ac);
  cursor: pointer;
  transition: color 0.2s;
}
.strategy-item-help:hover {
  color: var(--gi-primary, #0f766e);
}
/* 控件占满名称以外的空间，替代原固定宽度，避免半列宽下溢出或过短 */
.strategy-item-control {
  flex: 1;
  min-width: 0;
}

/* 数据质量列：有明细时仍可下钻（交互保留），但按纯文本呈现，hover 才提示可点 */
.quality-link {
  color: inherit;
  text-decoration: none;
}
.quality-link:hover {
  text-decoration: underline;
}

.quality-reason {
  display: block;
  white-space: pre-wrap;
  line-height: 1.6;
  color: rgba(0, 0, 0, 0.65);
}

.search-input {
  width: 260px;
}

.filter-select {
  width: 180px;
}

.section-card {
  border-radius: 22px;
  box-shadow: 0 10px 24px rgba(18, 58, 72, 0.06);
}

/* 页面头部与 Tab 之间的留白收紧：卡片内边距由默认 24 降到「上 8 / 左右下 16」，
   Tab 头下边距、筛选行下边距、分页器下边距同步收窄。
   只作用于本页（scoped + :deep），不动公共布局与全局样式；
   间距全部取自项目间距变量，窄屏沿用同一套值，响应式表现不变。 */
.section-card :deep(.ant-card-body) {
  padding: var(--gi-spacing-sm) var(--gi-spacing-md) var(--gi-spacing-md);
}

.section-card :deep(.ant-tabs-nav) {
  margin-bottom: var(--gi-spacing-sm);
}

.section-card :deep(.ant-pagination) {
  margin-bottom: 0;
}

/* Tab 栏右侧操作区：三个 Tab 的搜索/筛选/新建统一与 Tab 名称同排、靠右。
   slot 内容由本组件渲染（带 scoped 属性），所以普通 scoped 选择器能命中；
   ant-tabs 内部节点才需要 :deep。

   宽屏：与 Tab 同处一行，控件靠右、与 Tab 文字垂直居中；
   窄屏：整块折到第二行并保持右对齐，Tab 不被挤成「更多」下拉；
         第二行仍放不下时 extra 内部再折行，不出现横向滚动或溢出。
   实现关键：
     - .ant-tabs-nav 允许 flex-wrap: wrap；
     - .ant-tabs-extra-content 占满剩余空间（flex: 1 1 auto），把内部控件推到右端；
     - .tabs-extra 宽度 100%，折行时保持 justify-content: flex-end。 */
.config-tabs :deep(.ant-tabs-nav) {
  flex-wrap: wrap;
}

.config-tabs :deep(.ant-tabs-extra-content) {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: center;
}

.tabs-extra {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
  min-width: 0;
  width: 100%;
}

/* 按钮/下拉不允许被压扁：宽度不够就换行，而不是挤成一条 */
.tabs-extra > * {
  flex: none;
}

.health-tile {
  padding: 16px;
  border-radius: 16px;
  background: #f5f8fb;
  height: 100%;
}

.health-tile-label {
  font-size: 12px;
  color: #708390;
}

.health-tile-value {
  margin-top: 10px;
  font-size: 22px;
  font-weight: 700;
  color: #14313d;
}

.health-tile-value.good {
  color: #0f9f6f;
}

.health-tile-value.warn {
  color: #d97706;
}

.health-tile-value.bad {
  color: #d64545;
}

.health-tile-note {
  margin-top: 8px;
  font-size: 12px;
  color: #90a0ab;
}

.health-section-title {
  margin: 20px 0 10px;
  font-size: 13px;
  font-weight: 700;
  color: #163440;
}

/* ---------------------------------------------------------------------------
   指标模板：勾选界面与说明文案
   --------------------------------------------------------------------------- */

.template-meta-card {
  padding: 14px 16px;
  margin-bottom: 12px;
  background: var(--gi-bg-subtle, #f6f8fa);
  border: 1px solid var(--gi-border-color, #e8ecf0);
  border-radius: 12px;
}

.compact-form-item {
  margin-bottom: 0;
}

.compact-form-item :deep(.ant-form-item-label) {
  padding-bottom: 4px;
  line-height: 1.4;
}

.compact-form-item :deep(.ant-form-item-label > label) {
  font-size: 12px;
  color: var(--gi-text-secondary, #5f6b7a);
}

.template-extra-row {
  margin-bottom: 12px;
}

.task-desc {
  font-size: 12px;
  color: #8fa0ac;
}

.switch-hint {
  margin-left: 8px;
  font-size: 12px;
  color: #8fa0ac;
}

.template-picker-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 12px;
  margin-bottom: 12px;
  background: var(--gi-bg-subtle, #f6f8fa);
  border-radius: 8px;
}

.picker-summary {
  font-size: 13px;
  color: #4a5b66;
}

.picker-summary b {
  color: #163440;
}


.template-picker {
  max-height: 46vh;
  overflow-y: auto;
  padding-right: 4px;
}

.picker-group {
  margin-bottom: 16px;
}

.picker-group-title {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.picker-count {
  font-size: 12px;
  color: #8fa0ac;
}

.picker-disabled {
  color: #b0bcc4;
}

.picker-items {
  display: block;
}

.picker-items :deep(.ant-checkbox-wrapper) {
  margin-inline-start: 0;
  font-size: 13px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

@media (max-width: 960px) {
  /* 窄屏：控件各自占满一行（.tabs-extra 已允许折行），
     Tab 与操作区上下两行，互不挤压。 */
  .search-input,
  .filter-select {
    width: 100%;
  }
}

/* ---------------------------------------------------------------------------
   监控对象列表：「数据源」列自定义渲染
   左侧数据库类型图标 + 右侧「名称 / IP:端口」两行文字
   --------------------------------------------------------------------------- */
.datasource-cell {
  display: flex;
  align-items: center;
  gap: 10px;
  line-height: 1.2;
}

.db-type-icon {
  flex: none;
  width: 20px;
  height: 20px;
  object-fit: contain;
}

/* 指标模板编辑态下拉选项里的品牌图标（列表列已按需求去掉图标）。
   图标取自共享的 dbTypeLogo，与监控对象数据源列同一套资源。 */
.db-type-option {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.db-type-option-logo {
  /* 全站统一：数据库类型 logo 一律 20px */
  width: 20px;
  height: 20px;
  object-fit: contain;
}

.datasource-text {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.datasource-name {
  font-size: 14px;
  color: var(--gi-text-1, rgba(0, 0, 0, 0.85));
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.datasource-target {
  font-size: 12px;
  color: rgba(0, 0, 0, 0.45);
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

@media (max-width: 768px) {
  /* 现有断点写的是 640px，641~768 这段仍是两列 */
  .strategy-item-grid {
    grid-template-columns: minmax(0, 1fr);
    column-gap: 0;
  }

  .strategy-item-main {
    flex-wrap: wrap;
    row-gap: 6px;
  }

  .strategy-item-name {
    flex: 1 1 100%;
    min-width: 0;
  }

  .strategy-item-control {
    flex: 1 1 100%;
  }

  .tabs-extra {
    justify-content: flex-start;
  }

  .search-input,
  .filter-select {
    width: 100%;
  }

  .section-card :deep(.ant-card-body) {
    padding: 12px;
  }

  :deep(.ant-space) {
    flex-wrap: wrap;
    row-gap: 8px;
  }

  .picker-items :deep(.ant-checkbox-wrapper) {
    white-space: normal;
    overflow: visible;
    text-overflow: clip;
    max-width: 100%;
  }

  .template-picker {
    max-height: 56vh;
  }
}
</style>
