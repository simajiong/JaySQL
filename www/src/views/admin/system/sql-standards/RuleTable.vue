<template>
  <!-- scroll.y 仅用于触发表头与表体分离（表头固定），
       实际高度由 CSS 用 flex 占满父容器剩余空间，随窗口 resize 自适应 -->
  <a-table
    size="middle"
    :columns="columns"
    :row-key="(record) => record._key"
    :data-source="filteredRules"
    :loading="loading"
    :pagination="pagination"
    :locale="tableLocale"
    :scroll="{ y: '1px' }"
    @change="handleTableChange"
  >
    <template #bodyCell="{ column, record }">
      <template v-if="column.key === 'level'">
        <a-space :size="4">
          <!-- 规则级别展示与状态解耦：关闭时仍显示原有 must/suggest，不显示“关闭”。
               去掉 a-tag 的圆角/背景/边框，只保留对应级别的字体颜色 -->
          <span
            v-if="record._displayLevel === 'must'"
            class="level-text level-text--must"
          >必须改进</span>
          <span
            v-else-if="record._displayLevel === 'suggest'"
            class="level-text level-text--suggest"
          >建议改进</span>
          <span v-else class="level-text level-text--off">关闭</span>
          <!-- 状态为关闭时不再显示级别不一致提示图标，避免与“关闭”状态产生视觉关联或残留占位 -->
          <a-tooltip v-if="record._levelInconsistent && record.level !== 'off'" title="同一能力项下不同数据库的规则级别不一致；编辑保存后会以本表单的级别统一写入所有勾选的数据库">
            <ExclamationCircleOutlined class="level-warn-icon" />
          </a-tooltip>
        </a-space>
      </template>
      <template v-if="column.key === 'rule_type'">
        <span>{{ formatRuleType(record) }}</span>
      </template>
      <template v-if="column.key === 'db_type'">
        <a-space :size="4" align="center" class="db-logo-list">
          <!-- 数据库类型列与「状态」列解耦：logo 展示规则实际拥有的数据库，
               规则关闭（level=off）不隐藏 logo，避免开关切换时图标消失/出现过渡效果 -->
          <img
            v-for="db in record.dbTypes"
            :key="db"
            class="db-logo"
            :src="DB_LOGO[db] || mysqlLogo"
            :alt="DB_LABELS[db] || db"
            :title="DB_LABELS[db] || db"
          />
        </a-space>
      </template>
      <template v-if="column.key === 'enabled'">
        <a-switch
          :checked="record.level !== 'off'"
          :disabled="!canManage"
          size="small"
          @change="(checked) => handleToggle(record, checked)"
        />
      </template>
      <template v-if="column.key === 'action'">
        <a-space class="gi-table-actions">
          <a-button type="link" size="small" :disabled="!canManage" @click="handleEdit(record)">编辑</a-button>
        </a-space>
      </template>
    </template>
  </a-table>

  <a-drawer
    v-model:open="isModalOpen"
    title="编辑规则"
    :width="'40%'"
    :body-style="{ paddingBottom: '80px' }"
  >
    <a-form :model="form" layout="vertical">
      <a-form-item label="规则类型">
        <span>{{ form.ruleTypeText }}</span>
      </a-form-item>
      <a-form-item label="规则名称" required>
        <a-input :value="form.name" disabled />
      </a-form-item>
      <a-form-item label="支持的功能模块" required>
        <a-checkbox-group :value="['order']" disabled>
          <a-checkbox value="order">SQL 工单</a-checkbox>
        </a-checkbox-group>
      </a-form-item>
      <a-form-item label="支持数据库" required>
        <a-checkbox-group
          :value="form.dbTypes"
          class="db-checkbox-group"
          @change="onDbTypesChange"
        >
          <a-checkbox
            v-for="db in drawerDbOptions"
            :key="db"
            :value="db"
            :disabled="isDbCheckboxDisabled(db)"
          >
            {{ DB_LABELS[db] || db }}
          </a-checkbox>
        </a-checkbox-group>
      </a-form-item>
      <a-form-item v-if="isCharLengthRule" label="CHAR 最大允许长度" required>
        <a-input-number
          v-model:value="form.maxLength"
          :min="1"
          :max="2048"
          :precision="0"
          style="width: 240px"
        />
      </a-form-item>
      <a-form-item v-if="isVarcharLengthRule" label="VARCHAR 最大允许长度" required>
        <a-input-number
          v-model:value="form.varcharMaxLength"
          :min="1"
          :max="VARCHAR_LENGTH_MAX"
          :precision="0"
          style="width: 240px"
        />
      </a-form-item>
      <a-form-item v-if="isAutoIncRule && form.level !== 'off'" label="表自增初始值" required>
        <a-input-number
          v-model:value="form.autoIncValue"
          :min="1"
          :max="2147483647"
          :precision="0"
          style="width: 240px"
        />
      </a-form-item>
      <a-form-item v-if="isAutoIncRule && form.level !== 'off'" label="字段名大小写" required>
        <a-select v-model:value="form.autoIncNameCase" style="width: 240px">
          <a-select-option value="lower">小写</a-select-option>
          <a-select-option value="upper">大写</a-select-option>
        </a-select>
      </a-form-item>
      <a-form-item v-if="isColumnCountRule && form.level !== 'off'" label="字段最大数量" required>
        <a-input-number
          v-model:value="form.maxColumnCount"
          :min="1"
          :max="COLUMN_COUNT_MAX"
          :precision="0"
          style="width: 240px"
        />
      </a-form-item>
      <a-form-item v-if="isPKKeyPartsRule && form.level !== 'off'" label="最大值" required>
        <a-input-number
          v-model:value="form.pkMaxKeyParts"
          :min="1"
          :max="PK_KEY_PARTS_MAX"
          :precision="0"
          style="width: 240px"
        />
      </a-form-item>
      <a-form-item v-if="isPKColTypeRule && form.level !== 'off'" label="类型">
        <a-select
          v-model:value="form.pkTypes"
          mode="multiple"
          show-search
          allow-clear
          :max-tag-count="6"
          :max-tag-placeholder="(omitted) => `+ ${omitted.length} ...`"
          placeholder="请选择允许的主键列类型（留空表示不限制类型）"
          style="width: 100%"
          :options="pkColTypeOptions"
        />
        <div v-if="dbTypeListDiffHint" class="param-hint">
          各数据库当前生效的白名单不同——{{ dbTypeListDiffHint }}；保存后将以本表单的列表统一写入所有勾选的数据库。
        </div>
      </a-form-item>
      <a-form-item v-if="isSecKeyPartsRule && form.level !== 'off'" label="最大值" required>
        <a-input-number
          v-model:value="form.secMaxKeyParts"
          :min="1"
          :max="SEC_KEY_PARTS_MAX"
          :precision="0"
          style="width: 240px"
        />
      </a-form-item>
      <a-form-item v-if="isKeywordRule && form.level !== 'off'" label="关键字列表" required>
        <a-select
          v-model:value="form.keywords"
          mode="multiple"
          show-search
          allow-clear
          :max-tag-count="10"
          :max-tag-placeholder="(omitted) => `+ ${omitted.length} ...`"
          placeholder="请选择禁止使用的关键字，取消选中表示放开该关键字"
          style="width: 100%"
          :options="keywordOptions"
        />
      </a-form-item>
      <a-form-item v-if="isEngineRule && form.level !== 'off'" label="表引擎" required>
        <a-select
          v-model:value="form.engines"
          mode="multiple"
          show-search
          allow-clear
          :max-tag-count="5"
          :max-tag-placeholder="(omitted) => `+ ${omitted.length} ...`"
          placeholder="请选择允许的存储引擎"
          style="width: 100%"
          :options="engineOptions"
        />
      </a-form-item>
      <a-form-item v-if="isUniqIdxRule && form.level !== 'off'" label="唯一索引名称正则表达式" required>
        <a-input
          v-model:value="form.uniqIdxRegex"
          :maxlength="UNIQ_IDX_REGEX_MAX"
          allow-clear
          placeholder="请输入唯一索引名称必须匹配的正则表达式"
          style="width: 240px"
        />
      </a-form-item>
      <a-form-item v-if="isNormalIdxRule && form.level !== 'off'" label="索引名称正则表达式" required>
        <a-input
          v-model:value="form.normalIdxRegex"
          :maxlength="UNIQ_IDX_REGEX_MAX"
          allow-clear
          placeholder="请输入普通索引名称必须匹配的正则表达式"
          style="width: 240px"
        />
      </a-form-item>
      <a-form-item v-if="isForbiddenTypeRule && form.level !== 'off'" label="数据类型" required>
        <a-select
          v-model:value="form.forbiddenTypes"
          mode="multiple"
          show-search
          allow-clear
          :max-tag-count="10"
          :max-tag-placeholder="(omitted) => `+ ${omitted.length} ...`"
          placeholder="请选择禁止使用的数据类型"
          style="width: 100%"
          :options="forbiddenTypeOptions"
        />
        <div v-if="dbTypeListDiffHint" class="param-hint">
          各数据库当前生效的禁用列表不同——{{ dbTypeListDiffHint }}；保存后将以本表单的列表统一写入所有勾选的数据库。
        </div>
      </a-form-item>
      <a-form-item v-if="isDatabaseCharsetRule && form.level !== 'off'" label="库字符集列表" required>
        <a-select
          v-model:value="form.databaseCharsets"
          mode="multiple"
          show-search
          allow-clear
          :max-tag-count="10"
          :max-tag-placeholder="(omitted) => `+ ${omitted.length} ...`"
          placeholder="请选择允许的库字符集"
          style="width: 100%"
          :options="databaseCharsetOptions"
        />
      </a-form-item>
      <a-form-item v-if="isDMLAffectedRowsRule && form.level !== 'off'" label="影响行数" required>
        <a-input-number
          v-model:value="form.dmlAffectedRows"
          :min="1"
          :max="DML_AFFECTED_ROWS_MAX"
          :precision="0"
          style="width: 240px"
        />
        <div class="param-hint">UPDATE/DELETE 单条语句预计影响/扫描行数超过该值时告警（按级别决定是否阻断）</div>
      </a-form-item>
      <a-form-item v-if="isDMLJoinTablesRule && form.level !== 'off'" label="最大值" required>
        <a-input-number
          v-model:value="form.dmlJoinTables"
          :min="1"
          :max="DML_JOIN_TABLES_MAX"
          :precision="0"
          style="width: 240px"
        />
        <div class="param-hint">UPDATE/DELETE 语句关联表数（含主表）超过该值时告警</div>
      </a-form-item>
      <a-form-item v-if="isDMLLimitRowsRule && form.level !== 'off'" label="最大值" required>
        <a-input-number
          v-model:value="form.dmlLimitRows"
          :min="1"
          :max="DML_LIMIT_ROWS_MAX"
          :precision="0"
          style="width: 240px"
        />
        <div class="param-hint">UPDATE/DELETE 语句 LIMIT 行数超过该值时告警</div>
      </a-form-item>
      <a-form-item v-if="isDMLInsertRowsRule && form.level !== 'off'" label="最大行数" required>
        <a-input-number
          v-model:value="form.dmlInsertRows"
          :min="1"
          :max="DML_INSERT_ROWS_MAX"
          :precision="0"
          style="width: 240px"
        />
        <div class="param-hint">单条 INSERT 语句 VALUES 总行数超过该值时告警</div>
      </a-form-item>
      <a-form-item v-if="isDMLUpdateTimeRule && form.level !== 'off'" label="建议更新的字段" required>
        <a-input
          v-model:value="form.dmlUpdateTimeCols"
          :maxlength="256"
          allow-clear
          placeholder="多个候选列用英文逗号分隔，如：update_time,modified_at"
          style="width: 240px"
        />
        <div class="param-hint">UPDATE 语句 SET 子句未包含任一候选列时提醒（多个列用英文逗号分隔）</div>
      </a-form-item>
      <a-form-item v-if="isDMLCreateTimeRule && form.level !== 'off'" label="不建议更新的字段" required>
        <a-input
          v-model:value="form.dmlCreateTimeCols"
          :maxlength="256"
          allow-clear
          placeholder="多个字段用英文逗号分隔，如：create_time,gmt_create"
          style="width: 240px"
        />
        <div class="param-hint">UPDATE 语句 SET 子句包含任一字段时告警（创建时间属于数据元信息，更新会破坏审计追溯）</div>
      </a-form-item>
      <a-form-item v-if="isDMLWhereInRule && form.level !== 'off'" label="最大值" required>
        <a-input-number
          v-model:value="form.dmlWhereIn"
          :min="1"
          :max="DML_WHERE_IN_MAX"
          :precision="0"
          style="width: 240px"
        />
        <div class="param-hint">WHERE 条件中 IN 子句元素个数超过该值时告警</div>
      </a-form-item>
      <a-form-item v-if="isTableCharsetRule && form.level !== 'off'" label="表字符集" required>
        <a-select
          v-model:value="form.tableCharsets"
          mode="multiple"
          show-search
          allow-clear
          :max-tag-count="10"
          :max-tag-placeholder="(omitted) => `+ ${omitted.length} ...`"
          placeholder="请选择允许的表字符集"
          style="width: 100%"
          :options="tableCharsetOptions"
        />
      </a-form-item>
      <a-form-item v-if="isTableCollationRule && form.level !== 'off'" label="表校验规则列表" required>
        <a-select
          v-model:value="form.tableCollations"
          mode="multiple"
          show-search
          allow-clear
          :max-tag-count="6"
          :max-tag-placeholder="(omitted) => `+ ${omitted.length} ...`"
          placeholder="请选择允许的表校验规则（排序规则）"
          style="width: 100%"
          :options="tableCollationOptions"
        />
      </a-form-item>
      <a-form-item v-if="isIdxCountRule && form.level !== 'off'" label="最大值" required>
        <a-input-number
          v-model:value="form.idxMaxCount"
          :min="1"
          :max="IDX_COUNT_MAX"
          :precision="0"
          style="width: 240px"
        />
      </a-form-item>
      <a-form-item v-if="isTableNameCaseRule && form.level !== 'off'" label="表名大小写值" required>
        <a-select v-model:value="form.tableNameCase" style="width: 240px">
          <a-select-option value="lower">小写</a-select-option>
          <a-select-option value="upper">大写</a-select-option>
        </a-select>
      </a-form-item>
      <a-form-item v-if="isRequiredColsRule && form.level !== 'off'" label="表必须包含字段列表(,分隔)" required>
        <a-input
          v-model:value="form.requiredCols"
          :maxlength="512"
          allow-clear
          placeholder="请输入表必须包含的字段，多个字段用英文逗号分隔"
          style="width: 240px"
        />
      </a-form-item>
      <a-form-item label="规则级别" required>
        <a-radio-group v-model:value="form.level">
          <a-radio value="must">必须改进</a-radio>
          <a-radio value="suggest">建议改进</a-radio>
        </a-radio-group>
        <div v-if="dbLevelDiffHint" class="param-hint">
          各数据库当前生效的级别不同——{{ dbLevelDiffHint }}；保存后将以本表单的级别统一写入所有勾选的数据库。
        </div>
      </a-form-item>
      <a-form-item label="描述">
        <a-textarea
          v-model:value="form.description"
          :rows="4"
          :maxlength="512"
          show-count
          placeholder="填写该规范项的描述说明"
        />
      </a-form-item>
    </a-form>
    <template #footer>
      <a-space style="float: right">
        <a-button @click="isModalOpen = false">取消</a-button>
        <a-button type="primary" :loading="submitting" @click="onSubmit">确定</a-button>
      </a-space>
    </template>
  </a-drawer>
</template>

<script setup>
import { message } from 'ant-design-vue'
import { ExclamationCircleOutlined } from '@ant-design/icons-vue'
import { computed, reactive, ref, watch } from 'vue'
// 规则类型解析：md 名称匹配 → 归一化匹配 → rule_key 分类，见 ruleTypeUtils.js
import { resolveRuleType as formatRuleType } from './ruleTypeUtils'
import identifierKeywords from './identifierKeywords.json'
// 数据库类型列的品牌图标（此前模板直接引用了未导入的 mysqlLogo/pgLogo ⇒ 全列破图）
import mysqlLogo from '@/assets/logo/mysql.png'
import pgLogo from '@/assets/logo/postgresql.png'
import oracleLogo from '@/assets/logo/oracle.png'
import tidbLogo from '@/assets/logo/tidb.png'
import sqlserverLogo from '@/assets/logo/sqlserver.png'
import kingbaseLogo from '@/assets/logo/kingbase.png'

// 规则目录的数据库标识 → 展示名 / 品牌图标（与 utils/dbTypeLogo.js 的图标资源保持一致）
const DB_LABELS = { mysql: 'MySQL', postgres: 'PostgreSQL', kingbase: 'Kingbase', oracle: 'Oracle', tidb: 'TiDB', sqlserver: 'SQLServer' }
const DB_LOGO = { mysql: mysqlLogo, postgres: pgLogo, kingbase: kingbaseLogo, oracle: oracleLogo, tidb: tidbLogo, sqlserver: sqlserverLogo }

// 数据库 logo 固定顺序（金仓紧跟 PostgreSQL，SQLServer 排在 TiDB 之后，与后端目录顺序一致）
const DB_ORDER = ['mysql', 'postgres', 'kingbase', 'oracle', 'tidb', 'sqlserver']

// 把 db_type（mysql/postgres/oracle/tidb/all）归一化为按固定顺序排列的数组
const normalizeDbTypes = (dbTypes) => {
  const set = new Set()
  dbTypes.forEach((t) => {
    if (t === 'all') {
      DB_ORDER.forEach((db) => set.add(db))
    } else if (DB_ORDER.includes(t)) {
      set.add(t)
    }
  })
  return DB_ORDER.filter((t) => set.has(t))
}

const props = defineProps({
  rules: { type: Array, default: () => [] },
  loading: { type: Boolean, default: false },
  canManage: { type: Boolean, default: false },
  filters: {
    type: Object,
    default: () => ({
      status: 'all',
      dbType: 'all',
      ruleType: 'all',
      level: 'all',
      keyword: '',
    }),
  },
})

const emit = defineEmits(['update'])

// 分页配置：默认每页 20 条，支持切换每页条数与翻页（不显示「跳至 页」快速跳页）。
// current/pageSize 用受控值（reactive），这样搜索关键字变化时可以主动重置回第一页
const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showQuickJumper: false,
  pageSizeOptions: ['10', '20', '30', '50', '100'],
  showTotal: (total) => `共 ${total} 条`,
})

// 受控分页：翻页、切换每页条数后把结果回写到 pagination，否则分页不会更新
const handleTableChange = (pag) => {
  pagination.current = pag.current
  pagination.pageSize = pag.pageSize
}

// 空状态文案：带搜索关键字时给出更明确的「无匹配」提示
const tableLocale = computed(() => {
  const kw = (props.filters?.keyword || '').trim()
  return {
    emptyText: kw ? `没有匹配「${kw}」的规则` : '暂无规则',
  }
})

// 列宽按百分比自适应分配：总宽 100%，表格宽度随容器自适应，
// 不设置 scroll.x，因此不会出现父容器右侧的横向滚动条；
// 单元格统一使用 12px 16px 内边距，保证相邻列之间留白间距完全一致。
//
// 规则名称、描述各收窄 10%，腾出宽度平均分配给其余 5 列：
//   规则名称 22% * 0.9 = 19.8%   腾出 2.2%
//   描述     33% * 0.9 = 29.7%   腾出 3.3%
//   合计腾出 5.5%，其余 5 列每列 +1.1%
const columns = [
  { title: '规则级别', dataIndex: 'level', key: 'level', width: '12.1%' },
  { title: '规则名称', dataIndex: 'name', key: 'name', width: '19.8%' },
  { title: '规则类型', dataIndex: 'rule_type', key: 'rule_type', width: '9.1%', align: 'center' },
  { title: '数据库类型', dataIndex: 'db_type', key: 'db_type', width: '11.1%', align: 'center' },
  { title: '状态', dataIndex: 'enabled', key: 'enabled', width: '9.1%', align: 'center' },
  { title: '描述', dataIndex: 'description', key: 'description', width: '29.7%' },
  { title: '操作', dataIndex: 'action', key: 'action', width: '9.1%', align: 'center' },
]

const isModalOpen = ref(false)
const submitting = ref(false)
const editingRecord = ref(null)
const form = reactive({
  id: undefined,
  name: '',
  ruleTypeText: '',
  dbTypes: [],
  level: 'suggest',
  description: '',
  maxLength: 64,
  varcharMaxLength: 1024,
  autoIncValue: 1,
  autoIncNameCase: 'lower',
  maxColumnCount: 50,
  pkMaxKeyParts: 2,
  pkTypes: ['bigint', 'int'],
  secMaxKeyParts: 2,
  tableCharsets: ['utf8mb4', 'utf8'],
  tableCollations: ['utf8mb4_general_ci', 'utf8_general_ci', 'utf8mb4_bin', 'utf8_bin'],
  idxMaxCount: 5,
  tableNameCase: 'lower',
  requiredCols: 'create_time,update_time',
  keywords: [],
  engines: ['InnoDB'],
  uniqIdxRegex: 'uk_\\w+',
  normalIdxRegex: 'idx1_\\w+',
  forbiddenTypes: ['double', 'float', 'enum', 'bit', 'set'],
  databaseCharsets: ['utf8mb4', 'utf8'],
  dmlAffectedRows: 10000,
  dmlJoinTables: 5,
  dmlLimitRows: 5,
  dmlInsertRows: 1000,
  dmlUpdateTimeCols: 'update_time',
  dmlCreateTimeCols: 'create_time',
  dmlWhereIn: 1000,
})

// CHAR 长度规则的参数键与取值范围（与后端 standards.go 的 rp.MaxLength / defaultMaxCharLength 对齐）
const CHAR_LENGTH_RULE_KEY = 'limit_char_type_col_len'
const CHAR_LENGTH_DEFAULT = 64
const CHAR_LENGTH_MAX = 2048

// VARCHAR 长度规则的参数键与取值范围（与后端 rp.MaxLength / defaultMaxVarcharLength 对齐）
const VARCHAR_LENGTH_RULE_KEY = 'limit_varchar_type_col_len'
const VARCHAR_LENGTH_DEFAULT = 1024
const VARCHAR_LENGTH_MAX = 65535

// 建表自增初始值规则的参数键与取值范围（与后端 rp.AutoIncInitValue / defaultAutoIncInitValue 对齐）
const AUTO_INC_RULE_KEY = 'limit_create_table_auto_inc_init'
const AUTO_INC_DEFAULT = 1
const AUTO_INC_MAX = 2147483647
const AUTO_INC_NAME_CASE_DEFAULT = 'lower'

// 限制表字段数量规则的参数键与取值范围（与后端 rp.MaxColumns / defaultMaxTableColumns 对齐）
const COLUMN_COUNT_RULE_KEY = 'limit_table_column_count'
const COLUMN_COUNT_DEFAULT = 50
const COLUMN_COUNT_MAX = 1000

// 限制主键包含列的个数规则的参数键与取值范围（与后端 rp.MaxKeyParts / defaultPrimaryKeyKeyParts 对齐）
const PK_KEY_PARTS_RULE_KEY = 'limit_pk_column_count'
const PK_KEY_PARTS_DEFAULT = 2
const PK_KEY_PARTS_MAX = 16

// 限制主键列类型规则的参数键与默认值（与后端 rp.PKTypes / defaultPKColumnTypes 对齐）
const PK_COL_TYPE_RULE_KEY = 'limit_pk_col_type'
const PK_COL_TYPE_DEFAULT = ['bigint', 'int']
// 主键列类型的可选集合与「禁用数据类型」同源（都按库取并集，见下方 TYPE_OPTIONS_BY_DB），
// 不再单独维护一份 MySQL 清单——否则 Oracle 的 NUMBER 会成为「不在可选范围内」。

// 限制单个索引包含列的个数规则的参数键与取值范围（与后端 rp.MaxKeyParts / defaultSecondaryKeyParts 对齐）
const SEC_KEY_PARTS_RULE_KEY = 'limit_single_index_column_count'
const SEC_KEY_PARTS_DEFAULT = 2
const SEC_KEY_PARTS_MAX = 16

// 限制表字符集规则的参数键与选项（与后端 rp.Charsets / defaultTableCharsets 对齐）
const TABLE_CHARSET_RULE_KEY = 'limit_tbl_charset'
const TABLE_CHARSET_DEFAULT = ['utf8mb4', 'utf8']
// MySQL 支持的字符集选项（覆盖 8.0 全部常用字符集）
const TABLE_CHARSET_OPTIONS = [
  'utf8mb4', 'utf8', 'utf16', 'utf16le', 'utf32', 'ucs2',
  'gbk', 'gb2312', 'gb18030',
  'big5', 'sjis', 'cp932', 'eucjpms', 'ujis', 'euckr',
  'latin1', 'latin2', 'latin5', 'latin7',
  'ascii', 'binary', 'armscii8', 'cp1250', 'cp1251', 'cp1256',
  'cp850', 'cp852', 'cp866', 'dec8', 'greek', 'hebrew', 'hp8',
  'koi8r', 'koi8u', 'macce', 'macroman', 'swe7', 'tis620',
]

// 限制表校验规则（排序规则）的参数键与选项（与后端 rp.Collations / defaultTableCollations 对齐）
const TABLE_COLLATION_RULE_KEY = 'limit_tbl_collation'
const TABLE_COLLATION_DEFAULT = ['utf8mb4_general_ci', 'utf8_general_ci', 'utf8mb4_bin', 'utf8_bin']
// MySQL 支持的排序规则选项（覆盖各字符集的常用/默认排序规则）
const TABLE_COLLATION_OPTIONS = [
  'utf8mb4_general_ci', 'utf8mb4_general_cs', 'utf8mb4_unicode_ci', 'utf8mb4_bin',
  'utf8mb4_0900_ai_ci', 'utf8mb4_0900_as_cs', 'utf8mb4_0900_c_ci', 'utf8mb4_0900_as_ci', 'utf8mb4_0900_bin',
  'utf8_general_ci', 'utf8_general_cs', 'utf8_unicode_ci', 'utf8_bin',
  'utf16_general_ci', 'utf16_unicode_ci', 'utf16_bin',
  'utf16le_general_ci', 'utf16le_bin',
  'utf32_general_ci', 'utf32_unicode_ci', 'utf32_bin',
  'ucs2_general_ci', 'ucs2_unicode_ci', 'ucs2_bin',
  'gbk_chinese_ci', 'gbk_bin', 'gb2312_chinese_ci', 'gb2312_bin',
  'gb18030_chinese_ci', 'gb18030_bin', 'gb18030_unicode_520_ci',
  'big5_chinese_ci', 'big5_bin', 'sjis_japanese_ci', 'sjis_bin',
  'cp932_japanese_ci', 'cp932_bin', 'eucjpms_japanese_ci', 'eucjpms_bin',
  'ujis_japanese_ci', 'ujis_bin', 'euckr_korean_ci', 'euckr_bin',
  'latin1_swedish_ci', 'latin1_general_ci', 'latin1_general_cs', 'latin1_german1_ci', 'latin1_german2_ci', 'latin1_bin',
  'latin2_general_ci', 'latin2_bin', 'latin5_turkish_ci', 'latin5_bin',
  'latin7_general_ci', 'latin7_general_cs', 'latin7_bin',
  'ascii_general_ci', 'ascii_bin', 'binary',
  'armscii8_general_ci', 'armscii8_bin', 'cp1250_general_ci', 'cp1250_bin',
  'cp1251_general_ci', 'cp1251_ukrainian_ci', 'cp1251_bin', 'cp1256_general_ci', 'cp1256_bin',
  'cp850_general_ci', 'cp850_bin', 'cp852_general_ci', 'cp852_bin', 'cp866_general_ci', 'cp866_bin',
  'dec8_swedish_ci', 'dec8_bin', 'greek_general_ci', 'greek_bin', 'hebrew_general_ci', 'hebrew_bin',
  'hp8_english_ci', 'hp8_bin', 'koi8r_general_ci', 'koi8r_bin', 'koi8u_general_ci', 'koi8u_bin',
  'macce_general_ci', 'macce_bin', 'macroman_general_ci', 'macroman_bin',
  'swe7_bin', 'tis620_thai_ci', 'tis620_bin',
]

// 限制表索引数量规则的参数键与取值范围（与后端 rp.MaxCount / defaultMaxIndexKeys 对齐）
const IDX_COUNT_RULE_KEY = 'limit_tbl_idx_count'
const IDX_COUNT_DEFAULT = 5
const IDX_COUNT_MAX = 2048

// 限制表名大小写规则的参数键（与后端 rp.NameCase / TABLE_NAME_CASE 对齐）
const TABLE_NAME_CASE_RULE_KEY = 'limit_tbl_name_case'
const TABLE_NAME_CASE_DEFAULT = 'lower'

// 表需要包含某些列规则的参数键（与后端 rp.Columns / defaultRequiredColumns 对齐）
const REQUIRED_COLS_RULE_KEY = 'table_must_include_required_columns'
const REQUIRED_COLS_DEFAULT = 'create_time,update_time'

// 字段名/表名不能是关键字规则的参数键（与后端 rp.Keywords 对齐；computed 里同时匹配 col/tbl 两个 rule_key）
// identifierKeywords 来自后端 MySQL 与 PostgreSQL 关键字并集（生成脚本：gen-keywords.mjs）
const KEYWORD_DEFAULTS = identifierKeywords

// 限制表存储引擎规则的参数键（与后端 rp.Engines 对齐）
const ENGINE_RULE_KEY = 'limit_table_storage_engine'
const ENGINE_DEFAULTS = ['InnoDB', 'MyISAM', 'MEMORY', 'CSV', 'ARCHIVE']
const ENGINE_DEFAULT_SELECTED = ['InnoDB']

// 限制唯一索引名格式规则的参数键与默认正则表达式（与后端 rp.Regex 对齐）
const UNIQ_IDX_RULE_KEY = 'limit_uniq_idx_name_fmt'
const UNIQ_IDX_REGEX_DEFAULT = 'uk_\\w+'
const UNIQ_IDX_REGEX_MAX = 256

// 限制普通索引名格式规则的参数键与默认正则表达式（与后端 rp.Regex / defaultNormalIdxRegex 对齐）
const NORMAL_IDX_RULE_KEY = 'limit_normal_idx_name_fmt'
const NORMAL_IDX_REGEX_DEFAULT = 'idx1_\\w+'

// 限制列不能使用部分数据类型规则的参数键（与后端 rp.ForbiddenTypes 对齐）
const FORBIDDEN_TYPE_RULE_KEY = 'limit_col_no_partial_data_type'
const FORBIDDEN_TYPE_DEFAULTS = ['double', 'float', 'enum', 'bit', 'set']
// 主流数据类型选项：覆盖数值、字符、二进制、日期时间、布尔/枚举/集合/JSON、空间类型等
const FORBIDDEN_TYPE_OPTIONS = [
  'tinyint', 'smallint', 'mediumint', 'int', 'integer', 'bigint',
  'decimal', 'numeric', 'float', 'double', 'real',
  'char', 'varchar', 'text', 'tinytext', 'mediumtext', 'longtext',
  'binary', 'varbinary', 'blob', 'tinyblob', 'mediumblob', 'longblob',
  'date', 'time', 'datetime', 'timestamp', 'year',
  'bool', 'boolean', 'enum', 'set', 'bit', 'json',
  'geometry', 'point', 'linestring', 'polygon',
]

/* ===== 按数据库区分的类型清单 =====
   上面两份清单是 MySQL 体系；PG / Oracle 的类型体系与 MySQL 不通用
   （number / varchar2 / long raw / jsonb / uuid 等都不在里面）。
   若对所有库都用 MySQL 清单，Oracle 规则的默认值 long、long raw 会被
   参数校验判为「不在可选范围内」而无法保存，页面也选不到 varchar2 等类型。
   因此下拉选项与参数校验一律按「当前能力项支持的库」取并集（跨库能力项 = 多库并集）。 */
const PG_TYPE_OPTIONS = [
  'smallint', 'integer', 'bigint', 'serial', 'bigserial', 'numeric', 'decimal',
  'real', 'double precision', 'money',
  'char', 'character', 'varchar', 'character varying', 'text',
  'bytea', 'boolean', 'json', 'jsonb', 'xml', 'uuid',
  'date', 'time', 'timetz', 'timestamp', 'timestamptz', 'interval',
  'inet', 'cidr', 'macaddr', 'bit', 'varbit',
]
const ORACLE_TYPE_OPTIONS = [
  'number', 'integer', 'int', 'float', 'binary_float', 'binary_double',
  'char', 'nchar', 'varchar', 'varchar2', 'nvarchar2',
  'long', 'long raw', 'raw',
  'clob', 'nclob', 'blob', 'bfile',
  'date', 'timestamp', 'interval', 'rowid', 'xmltype',
]
// SQLServer 的类型体系：没有 mediumint/year/enum/set，多了 money / sql_variant /
// uniqueidentifier / datetimeoffset / 空间类型；VARCHAR(MAX) 等 MAX 变体在清单里
// 统一按基础类型名展示（长度由「字符长度上限」规则单独约束）。
const SQLSERVER_TYPE_OPTIONS = [
  'tinyint', 'smallint', 'int', 'bigint', 'bit',
  'decimal', 'numeric', 'money', 'smallmoney', 'float', 'real',
  'char', 'varchar', 'nchar', 'nvarchar', 'text', 'ntext',
  'binary', 'varbinary', 'image',
  'date', 'time', 'datetime', 'datetime2', 'smalldatetime', 'datetimeoffset',
  'timestamp', 'rowversion', 'uniqueidentifier', 'xml', 'sql_variant', 'sysname',
  'geography', 'geometry', 'hierarchyid',
]
// 主键类型可选值：与禁用类型同源（主键类型白名单同样按库区分）
const TYPE_OPTIONS_BY_DB = {
  // serial 是 MySQL 主键常见别名，补充进 MySQL 系清单（禁用类型里也可选）
  mysql: [...FORBIDDEN_TYPE_OPTIONS, 'serial'],
  tidb: [...FORBIDDEN_TYPE_OPTIONS, 'serial'],
  postgres: PG_TYPE_OPTIONS,
  kingbase: PG_TYPE_OPTIONS, // 金仓与 PG 同一套类型体系
  oracle: ORACLE_TYPE_OPTIONS,
  sqlserver: SQLSERVER_TYPE_OPTIONS,
}
// unionTypeOptions 把多个库的可用类型合并（保持声明顺序、去重）
const unionTypeOptions = (dbTypes) => {
  const list = []
  const seen = new Set()
  dbTypes.forEach((db) => {
    ;(TYPE_OPTIONS_BY_DB[db] || []).forEach((t) => {
      if (!seen.has(t)) {
        seen.add(t)
        list.push(t)
      }
    })
  })
  // 库未知（脏数据/空）时回退 MySQL 清单，保证下拉不空
  return list.length ? list : FORBIDDEN_TYPE_OPTIONS
}
// SQLServer 的默认淘汰类型（与后端 config.SQLServerForbiddenTypes 保持一致：
// 都是已有更好替代、且会带来查询/存储问题的老类型）
const SQLSERVER_FORBIDDEN_TYPE_DEFAULTS = ['text', 'ntext', 'image', 'sql_variant', 'timestamp', 'money', 'smallmoney']
// SQLServer 可做主键的类型（与后端 config.SQLServerPrimaryKeyTypes 一致：
// MAX 变体、text/ntext/image/xml/sql_variant 与空间类型都不能做主键）
const SQLSERVER_PK_COL_TYPE_DEFAULT = [
  'tinyint', 'smallint', 'int', 'bigint', 'bit',
  'decimal', 'numeric', 'money', 'smallmoney', 'float', 'real',
  'char', 'varchar', 'nchar', 'nvarchar', 'binary', 'varbinary',
  'date', 'time', 'datetime', 'datetime2', 'smalldatetime', 'datetimeoffset',
  'uniqueidentifier', 'rowversion', 'timestamp', 'sysname',
]
// 禁用类型默认值：Oracle / SQLServer 各用自己体系的淘汰类型；
// 其余（含 all 及跨库能力项）沿用 MySQL 口径，与后端 applyStandardRule 的回退分支一致。
const defaultForbiddenTypes = (dbTypes) => {
  if (dbTypes.length === 1 && dbTypes[0] === 'oracle') return ['long', 'long raw']
  if (dbTypes.length === 1 && dbTypes[0] === 'sqlserver') return SQLSERVER_FORBIDDEN_TYPE_DEFAULTS
  return FORBIDDEN_TYPE_DEFAULTS
}
// 主键类型默认值：Oracle 主键常用 NUMBER、SQLServer 用其自身可做主键的清单；
// 其余沿用 MySQL 口径
const defaultPkColTypes = (dbTypes) => {
  if (dbTypes.length === 1 && dbTypes[0] === 'sqlserver') return SQLSERVER_PK_COL_TYPE_DEFAULT
  return dbTypes.includes('oracle') && !dbTypes.includes('mysql') && !dbTypes.includes('tidb')
    ? ['number']
    : PK_COL_TYPE_DEFAULT
}

// 限制库的字符集规则的参数键（与后端 rp.Charsets 对齐）
const DATABASE_CHARSET_RULE_KEY = 'limit_database_charset'
const DATABASE_CHARSET_DEFAULTS = ['utf8mb4', 'utf8']
// MySQL 常见字符集选项
const DATABASE_CHARSET_OPTIONS = [
  'utf8mb4', 'utf8', 'utf16', 'utf16le', 'utf32',
  'gbk', 'gb2312', 'gb18030', 'latin1', 'latin2', 'ascii', 'binary',
  'big5', 'sjis', 'ucs2', 'euckr', 'cp1251', 'cp1256', 'hebrew', 'greek',
]

// ===== DML 规则参数（与后端 standards.go 的 rp 字段及默认阈值对齐）=====
const DML_AFFECTED_ROWS_RULE_KEY = 'limit_update_delete_affected_rows'
const DML_JOIN_TABLES_RULE_KEY = 'limit_update_delete_join_table_count'
const DML_LIMIT_ROWS_RULE_KEY = 'limit_update_delete_data_size'
const DML_INSERT_ROWS_RULE_KEY = 'limit_insert_values_row_count'
const DML_UPDATE_TIME_RULE_KEY = 'update_stmt_suggest_update_time_column'
// UPDATE 不建议更新「创建时间」列：规则名里的列可在规范中配置，默认 create_time
const DML_CREATE_TIME_RULE_KEY = 'update_stmt_avoid_update_create_time_column'
const DML_WHERE_IN_RULE_KEY = 'limit_where_in_element_count'
const DML_AFFECTED_ROWS_DEFAULT = 10000
const DML_AFFECTED_ROWS_MAX = 1000000000
const DML_JOIN_TABLES_DEFAULT = 5
const DML_JOIN_TABLES_MAX = 100
const DML_LIMIT_ROWS_DEFAULT = 5
const DML_LIMIT_ROWS_MAX = 1000000
const DML_INSERT_ROWS_DEFAULT = 1000
const DML_INSERT_ROWS_MAX = 1000000
const DML_UPDATE_TIME_COLS_DEFAULT = 'update_time'
const DML_CREATE_TIME_COLS_DEFAULT = 'create_time'
const DML_WHERE_IN_DEFAULT = 1000
const DML_WHERE_IN_MAX = 100000

// 当前编辑的能力项是否为「限制 CHAR 类型字段长度」规则（mysql/postgres 共用同一后缀）
const isCharLengthRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(CHAR_LENGTH_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制 VARCHAR 类型字段长度」规则（mysql/postgres 共用同一后缀）
const isVarcharLengthRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(VARCHAR_LENGTH_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制建表自增初始值」规则
const isAutoIncRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(AUTO_INC_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制表字段的数量」规则
const isColumnCountRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(COLUMN_COUNT_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制主键包含列的个数」规则
const isPKKeyPartsRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(PK_KEY_PARTS_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制主键列类型」规则
const isPKColTypeRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(PK_COL_TYPE_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制单个索引包含列的个数」规则
const isSecKeyPartsRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(SEC_KEY_PARTS_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制表字符集」规则（仅 MySQL）
const isTableCharsetRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(TABLE_CHARSET_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制表校验规则」规则
const isTableCollationRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(TABLE_COLLATION_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制表索引数量」规则
const isIdxCountRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(IDX_COUNT_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制表名大小写」规则
const isTableNameCaseRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(TABLE_NAME_CASE_RULE_KEY)),
)

// 当前编辑的能力项是否为「表需要包含某些列」规则
const isRequiredColsRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(REQUIRED_COLS_RULE_KEY)),
)

// 当前编辑的能力项是否为「字段名/表名不能是关键字」规则（两个规则共用关键字列表配置）
const isKeywordRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => {
    const key = String(r.rule_key || '')
    return key.includes('col_name_no_keyword') || key.includes('tbl_name_no_keyword')
  }),
)

// 当前编辑的能力项是否为「限制表存储引擎」规则
const isEngineRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(ENGINE_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制唯一索引名格式」规则
const isUniqIdxRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(UNIQ_IDX_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制普通索引名格式」规则
const isNormalIdxRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(NORMAL_IDX_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制列不能使用部分数据类型」规则
const isForbiddenTypeRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(FORBIDDEN_TYPE_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制库的字符集」规则（仅 MySQL）
const isDatabaseCharsetRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(DATABASE_CHARSET_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制 UPDATE/DELETE 单个语句总影响行数」规则（仅 MySQL）
const isDMLAffectedRowsRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(DML_AFFECTED_ROWS_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制 UPDATE/DELETE 语句多表关联的数量」规则
const isDMLJoinTablesRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(DML_JOIN_TABLES_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制 UPDATE/DELETE 语句 LIMIT 数据量大小」规则（仅 MySQL）
const isDMLLimitRowsRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(DML_LIMIT_ROWS_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制 INSERT 语句一条 INSERT VALUES 的总行数」规则
const isDMLInsertRowsRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(DML_INSERT_ROWS_RULE_KEY)),
)

// 当前编辑的能力项是否为「UPDATE 语句建议更新表上的修改时间列」规则
const isDMLUpdateTimeRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(DML_UPDATE_TIME_RULE_KEY)),
)

// 当前编辑的能力项是否为「UPDATE 语句不建议更新表上的创建时间列」规则
const isDMLCreateTimeRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(DML_CREATE_TIME_RULE_KEY)),
)

// 当前编辑的能力项是否为「限制 WHERE 条件中 IN 子句包含元素个数」规则
const isDMLWhereInRule = computed(() =>
  (editingRecord.value?._rules || []).some((r) => String(r.rule_key || '').includes(DML_WHERE_IN_RULE_KEY)),
)

// 关键字下拉选项：来自 MySQL + PostgreSQL 关键字并集，统一大写，与后端 DefaultIdentifierKeywords 对齐
const keywordOptions = computed(() =>
  KEYWORD_DEFAULTS.map((kw) => ({ label: kw, value: kw })),
)

// 表引擎下拉选项
const engineOptions = computed(() =>
  ENGINE_DEFAULTS.map((engine) => ({ label: engine, value: engine })),
)

// 当前编辑能力项支持的库（下拉选项/默认值/校验都按它取并集）
const editingDbTypes = computed(() => editingRecord.value?.dbTypes || [])

// 禁用数据类型下拉选项：按当前能力项支持的库取并集（Oracle 才有 long raw / varchar2 等）
const forbiddenTypeOptionList = computed(() => unionTypeOptions(editingDbTypes.value))
const forbiddenTypeOptions = computed(() =>
  forbiddenTypeOptionList.value.map((t) => ({ label: t.toUpperCase(), value: t })),
)

// 库字符集下拉选项
const databaseCharsetOptions = computed(() =>
  DATABASE_CHARSET_OPTIONS.map((c) => ({ label: c, value: c })),
)

// 主键列类型下拉选项（类型名展示为小写；同样按库取并集，Oracle 才有 number）
const pkColTypeOptionList = computed(() => unionTypeOptions(editingDbTypes.value))
const pkColTypeOptions = computed(() =>
  pkColTypeOptionList.value.map((t) => ({ label: t, value: t })),
)

// 表字符集下拉选项
const tableCharsetOptions = computed(() =>
  TABLE_CHARSET_OPTIONS.map((c) => ({ label: c, value: c })),
)

// 表校验规则（排序规则）下拉选项
const tableCollationOptions = computed(() =>
  TABLE_COLLATION_OPTIONS.map((c) => ({ label: c, value: c })),
)

// 从规则 params 中解析指定整数参数，非法或缺失时回退默认值并按上限收敛
const parseRuleParamInt = (params, key, defaultValue, maxValue) => {
  let raw = params
  if (typeof raw === 'string') {
    try {
      raw = JSON.parse(raw)
    } catch {
      raw = null
    }
  }
  const value = Number(raw?.[key])
  if (!Number.isFinite(value) || value < 1) {
    return defaultValue
  }
  return Math.min(Math.floor(value), maxValue)
}

// 从规则 params 中解析字符串数组参数，非法或缺失时回退默认值
// toUpper=true 时把数组元素统一转大写（用于关键字、引擎等）；toUpper=false 时保留原大小写（用于数据类型）
const parseRuleParamStringArray = (params, key, defaultValue, toUpper = true) => {
  let raw = params
  if (typeof raw === 'string') {
    try {
      raw = JSON.parse(raw)
    } catch {
      raw = null
    }
  }
  const value = raw?.[key]
  if (Array.isArray(value)) {
    const filtered = value
      .map((v) => {
        const s = String(v).trim()
        return toUpper ? s.toUpperCase() : s
      })
      .filter((v) => v)
    if (filtered.length > 0) {
      return filtered
    }
  }
  return defaultValue
}

// 合并行下各数据库当前生效的类型清单（差异提示用）。
// 抽屉展示的是主规则的参数；跨库合并行的各库规则可能各自保存过不同清单
// （如 Oracle 默认禁 long/long raw，MySQL 默认禁 double/float/enum/bit/set）。
// 若不做提示，用户会误以为展示的清单对全部库生效——实际只有点确定保存后才统一写入。
const dbTypeListDiffHint = computed(() => {
  if (!isForbiddenTypeRule.value && !isPKColTypeRule.value) return ''
  const paramKey = isPKColTypeRule.value ? 'pk_types' : 'types'
  const rules = editingRecord.value?._rules || []
  if (rules.length <= 1) return ''
  const parts = []
  rules.forEach((r) => {
    const db = normalizeDbTypes([r.db_type])[0]
    if (!db) return
    // 直接读各库规则已保存的 params 原值（不回退默认），如实反映实际生效配置
    const list = parseRuleParamStringArray(r.params, paramKey, [], false)
    parts.push(`${DB_LABELS[db] || db}：${list.length ? list.map((t) => t.toUpperCase()).join('、') : '未配置'}`)
  })
  const uniq = new Set(parts)
  if (uniq.size <= 1) return ''
  return parts.join('；')
})

// 级别差异提示：合并行显示的是主规则的级别，各库实际各存一行、级别各管各的
// （历史上补种脚本按出厂默认写入，可能与主规则不一致）。如实列出各库当前生效级别，
// 避免出现「页面显示建议改进、Oracle 预审却按必须改进阻断」的认知偏差。
const LEVEL_LABELS = { must: '必须改进', suggest: '建议改进', off: '已关闭' }
const dbLevelDiffHint = computed(() => {
  const rules = editingRecord.value?._rules || []
  if (rules.length <= 1) return ''
  const parts = []
  rules.forEach((r) => {
    const db = normalizeDbTypes([r.db_type])[0]
    if (!db) return
    parts.push(`${DB_LABELS[db] || db}：${LEVEL_LABELS[r.level] || r.level || '未配置'}`)
  })
  const uniq = new Set(parts)
  if (uniq.size <= 1) return ''
  return parts.join('；')
})

// 校验字符串是否为合法的正则表达式
const isValidRegExp = (pattern) => {
  try {
    new RegExp(pattern)
    return true
  } catch {
    return false
  }
}

// 从规则 params 中解析字符串参数，非法或缺失时回退默认值
const parseRuleParamString = (params, key, defaultValue) => {
  let raw = params
  if (typeof raw === 'string') {
    try {
      raw = JSON.parse(raw)
    } catch {
      raw = null
    }
  }
  const value = raw?.[key]
  if (typeof value === 'string' && value.trim() !== '') {
    return value
  }
  return defaultValue
}

// 从规则 params 中解析「表必须包含字段列表」参数：数组或逗号分隔字符串统一转为逗号分隔文本，缺失时回退默认值
const parseRuleParamColumns = (params, defaultValue) => {
  let raw = params
  if (typeof raw === 'string') {
    try {
      raw = JSON.parse(raw)
    } catch {
      raw = null
    }
  }
  const value = raw?.columns
  if (Array.isArray(value)) {
    const list = value.map((c) => String(c).trim()).filter((c) => c)
    return list.length > 0 ? list.join(',') : defaultValue
  }
  if (typeof value === 'string' && value.trim() !== '') {
    return value.trim()
  }
  return defaultValue
}

// 从规则 params 中解析 name_case 参数，仅允许 lower/upper，非法或缺失时回退默认值
const parseRuleParamNameCase = (params, key, defaultValue) => {
  let raw = params
  if (typeof raw === 'string') {
    try {
      raw = JSON.parse(raw)
    } catch {
      raw = null
    }
  }
  const value = String(raw?.[key] ?? '').trim().toLowerCase()
  if (value === 'lower' || value === 'upper') {
    return value
  }
  return defaultValue
}

// 记录每条规则「关闭前的级别」，用于重新开启时还原。
// 不能直接用 base_level：开发环境规范的 base_level 多为 must，会导致重新开启时把「建议改进」误改成「必须改进」。
const restoreLevels = new Map()

// 能力项聚合 key（rule_key 去掉数据库前缀后的后缀）
const ruleAggKey = (rule) => {
  const suffix = rule.rule_key?.includes('.')
    ? rule.rule_key.slice(rule.rule_key.indexOf('.') + 1)
    : rule.rule_key
  return `${rule.category}::${suffix ?? rule.name}`
}

// 按能力项唯一标识聚合：同一能力项跨数据库合并为一行，行内显示所有支持的数据库 logo。
// 这样既能合并名称完全相同的能力项，也能合并名称仅存在空格/标点差异的等价能力项。
// _primary 是本行用于展示和编辑的「那一条」规则，编辑/开关的作用范围默认就是它。
// 按能力项唯一标识聚合：同一能力项跨数据库合并为一行，行内显示所有支持的数据库 logo。
const aggregatedRules = computed(() => {
  const map = new Map()
  props.rules.forEach((rule) => {
    const aggKey = ruleAggKey(rule)
    const existing = map.get(aggKey)
    if (!existing) {
      map.set(aggKey, {
        ...rule,
        _key: aggKey,
        _rules: [rule],
        _primary: rule,
        dbTypes: normalizeDbTypes([rule.db_type]),
      })
      return
    }
    existing._rules.push(rule)
    existing.dbTypes = normalizeDbTypes([...existing.dbTypes, rule.db_type])
    // 展示/编辑使用的规则固定为 sort_order 最小的一条，保证「列表显示」与「被修改的规则」始终是同一条
    if ((rule.sort_order ?? Infinity) < (existing._primary.sort_order ?? Infinity)) {
      Object.assign(existing, {
        ...rule,
        _key: aggKey,
        _primary: rule,
        dbTypes: existing.dbTypes,
      })
    }
  })
  return Array.from(map.values())
    .map((row) => ({
      ...row,
      // 数据库类型列与状态列解耦：dbTypes 为规则实际拥有的全部数据库，
      // 规则关闭（level=off）不影响 logo 展示；编辑抽屉的勾选项同样取自该字段
      // 同一能力项下不同数据库的级别不一致时给出提示，避免误以为修改会覆盖全部
      _levelInconsistent: new Set(row._rules.map((r) => r.level)).size > 1,
      // 规则级别展示值：off 时仍显示原有 must/suggest，不显示“关闭”
      _displayLevel: computeDisplayLevel(row),
    }))
    .sort((a, b) => {
      const minA = Math.min(...a._rules.map((r) => r.sort_order ?? Infinity))
      const minB = Math.min(...b._rules.map((r) => r.sort_order ?? Infinity))
      return minA - minB
    })
})

// 按能力项聚合后再根据外部筛选条件过滤；搜索按名称（不区分大小写）
const filteredRules = computed(() => {
  const f = props.filters || {}
  return aggregatedRules.value.filter((row) => {
    const enabled = row.level !== 'off'
    if (f.status === 'on' && !enabled) return false
    if (f.status === 'off' && enabled) return false

    if (f.dbType && f.dbType !== 'all' && !row.dbTypes.includes(f.dbType)) return false

    if (f.ruleType && f.ruleType !== 'all' && formatRuleType(row) !== f.ruleType) return false

    if (f.level && f.level !== 'all' && row._displayLevel !== f.level) return false

    // 规则名称搜索：模糊匹配（包含即可）、不区分大小写、忽略首尾空格；
    // 中文与特殊字符走同一套 includes 逻辑，无需额外处理；仅匹配规则名称，不涉及描述等其它字段
    if (f.keyword && f.keyword.trim()) {
      const kw = f.keyword.trim().toLowerCase()
      if (!row.name.toLowerCase().includes(kw)) return false
    }

    return true
  })
})

// 搜索关键字变化（含清空）时回到第一页，
// 避免停留在超出结果范围的页码上导致页面看起来是空的
watch(
  () => (props.filters?.keyword || '').trim(),
  () => {
    pagination.current = 1
  },
)

// 记录各能力项最近一次的非关闭级别，供重新开启时还原
watch(
  () => props.rules,
  (rules) => {
    ;(rules || []).forEach((rule) => {
      if (rule.level && rule.level !== 'off') {
        restoreLevels.set(ruleAggKey(rule), rule.level)
      }
    })
  },
  { immediate: true, deep: false },
)

// 规则级别展示值：当规则当前状态为 off 时，级别列仍按“原有级别”展示（must/suggest），
// 而不是直接显示为“关闭”，避免状态与规则级别混为一谈。
// 取值优先级：当前非 off 级别 > 关闭前记录 > 同能力项其它数据库非 off 级别 > base_level > suggest
const computeDisplayLevel = (row) => {
  const primary = row._primary || row
  if (primary.level && primary.level !== 'off') {
    return primary.level
  }
  const remembered = restoreLevels.get(row._key)
  if (remembered) {
    return remembered
  }
  const sibling = (row._rules || []).find((r) => r.level && r.level !== 'off')
  if (sibling) {
    return sibling.level
  }
  const base = primary.base_level || row.base_level
  if (base === 'must' || base === 'suggest') {
    return base
  }
  return 'suggest'
}

// 重新开启时还原的级别：优先用「关闭前记录」>「当前级别」>「同能力项其它数据库级别」> base_level > suggest
const resolveRestoreLevel = (record) => {
  if (record._primary?.level && record._primary.level !== 'off') {
    return record._primary.level
  }
  const remembered = restoreLevels.get(record._key)
  if (remembered) {
    return remembered
  }
  const sibling = (record._rules || []).find((r) => r.level && r.level !== 'off')
  if (sibling) {
    return sibling.level
  }
  const base = record._primary?.base_level || record.base_level
  if (base === 'must' || base === 'suggest') {
    return base
  }
  return 'suggest'
}

const handleEdit = (record) => {
  const primary = record._primary || record
  editingRecord.value = record
  // 能力项支持的库：默认值（禁用类型/主键类型）与下拉选项都按它区分
  const dbTypes = record.dbTypes || normalizeDbTypes([primary.db_type])
  Object.assign(form, {
    id: primary.id,
    name: primary.name,
    ruleTypeText: formatRuleType(primary),
    dbTypes,
    // 级别为关闭状态时按「重新开启还原」的口径回显，避免出现无选项可选
    level: primary.level && primary.level !== 'off' ? primary.level : resolveRestoreLevel(record),
    description: primary.description || '',
    maxLength: parseRuleParamInt(primary.params, 'max_length', CHAR_LENGTH_DEFAULT, CHAR_LENGTH_MAX),
    varcharMaxLength: parseRuleParamInt(primary.params, 'max_length', VARCHAR_LENGTH_DEFAULT, VARCHAR_LENGTH_MAX),
    autoIncValue: parseRuleParamInt(primary.params, 'auto_inc_init_value', AUTO_INC_DEFAULT, AUTO_INC_MAX),
    autoIncNameCase: parseRuleParamNameCase(primary.params, 'name_case', AUTO_INC_NAME_CASE_DEFAULT),
    maxColumnCount: parseRuleParamInt(primary.params, 'max_column_count', COLUMN_COUNT_DEFAULT, COLUMN_COUNT_MAX),
    pkMaxKeyParts: parseRuleParamInt(primary.params, 'max_key_parts', PK_KEY_PARTS_DEFAULT, PK_KEY_PARTS_MAX),
    pkTypes: parseRuleParamStringArray(primary.params, 'pk_types', defaultPkColTypes(dbTypes), false),
    secMaxKeyParts: parseRuleParamInt(primary.params, 'max_key_parts', SEC_KEY_PARTS_DEFAULT, SEC_KEY_PARTS_MAX),
    tableCharsets: parseRuleParamStringArray(primary.params, 'charsets', TABLE_CHARSET_DEFAULT, false),
    tableCollations: parseRuleParamStringArray(primary.params, 'collations', TABLE_COLLATION_DEFAULT, false),
    idxMaxCount: parseRuleParamInt(primary.params, 'max_count', IDX_COUNT_DEFAULT, IDX_COUNT_MAX),
    tableNameCase: parseRuleParamNameCase(primary.params, 'name_case', TABLE_NAME_CASE_DEFAULT),
    requiredCols: parseRuleParamColumns(primary.params, REQUIRED_COLS_DEFAULT),
    keywords: parseRuleParamStringArray(primary.params, 'keywords', KEYWORD_DEFAULTS),
    engines: parseRuleParamStringArray(primary.params, 'engines', ENGINE_DEFAULT_SELECTED),
    uniqIdxRegex: parseRuleParamString(primary.params, 'regex', UNIQ_IDX_REGEX_DEFAULT),
    normalIdxRegex: parseRuleParamString(primary.params, 'regex', NORMAL_IDX_REGEX_DEFAULT),
    forbiddenTypes: parseRuleParamStringArray(primary.params, 'types', defaultForbiddenTypes(dbTypes), false),
    databaseCharsets: parseRuleParamStringArray(primary.params, 'charsets', DATABASE_CHARSET_DEFAULTS, false),
    dmlAffectedRows: parseRuleParamInt(primary.params, 'max_affected_rows', DML_AFFECTED_ROWS_DEFAULT, DML_AFFECTED_ROWS_MAX),
    dmlJoinTables: parseRuleParamInt(primary.params, 'max_join_tables', DML_JOIN_TABLES_DEFAULT, DML_JOIN_TABLES_MAX),
    dmlLimitRows: parseRuleParamInt(primary.params, 'max_limit_rows', DML_LIMIT_ROWS_DEFAULT, DML_LIMIT_ROWS_MAX),
    dmlInsertRows: parseRuleParamInt(primary.params, 'max_insert_rows', DML_INSERT_ROWS_DEFAULT, DML_INSERT_ROWS_MAX),
    dmlUpdateTimeCols: parseRuleParamString(primary.params, 'update_time_columns', DML_UPDATE_TIME_COLS_DEFAULT),
    dmlCreateTimeCols: parseRuleParamString(primary.params, 'create_time_columns', DML_CREATE_TIME_COLS_DEFAULT),
    dmlWhereIn: parseRuleParamInt(primary.params, 'max_in_elements', DML_WHERE_IN_DEFAULT, DML_WHERE_IN_MAX),
  })
  isModalOpen.value = true
}

/* ===== 支持数据库勾选 =====
   选项来自该能力项实际拥有的数据库规则（record.dbTypes）：
   - 仅支持一个数据库时：默认勾选且不可取消；
   - 同时支持两个时：可自由勾选，但至少保留一个（最后一个勾选项不可取消）。 */
const drawerDbOptions = computed(() => editingRecord.value?.dbTypes || [])

const isDbCheckboxDisabled = (db) => {
  if (drawerDbOptions.value.length <= 1) {
    return true
  }
  // 仅剩一个勾选项时禁止取消，保证「至少选择一个」
  return form.dbTypes.length === 1 && form.dbTypes.includes(db)
}

const onDbTypesChange = (checked) => {
  const next = normalizeDbTypes(checked || [])
  if (next.length === 0) {
    message.warning('至少需要支持一个数据库')
    return
  }
  form.dbTypes = next
}

const handleToggle = (record, checked) => {
  if (!props.canManage) {
    message.warning('无权限修改规则状态')
    return
  }
  const nextLevel = checked ? resolveRestoreLevel(record) : 'off'
  // 列表上的开关按「能力项」整体启停：同一能力项下所有数据库规则同步切换，
  // 避免出现「MySQL 被关闭但 PostgreSQL 仍显示」这类不一致的图标展示。
  const rules = (record._rules || [record])
    .map((rule) => {
      if (rule.level === nextLevel) {
        return null
      }
      return { ...rule, level: nextLevel }
    })
    .filter(Boolean)
  if (rules.length === 0) {
    return
  }
  emit('update', rules)
}

const onSubmit = async () => {
  if (form.dbTypes.length === 0) {
    message.warning('至少需要支持一个数据库')
    return
  }
  if (isCharLengthRule.value) {
    const len = Number(form.maxLength)
    if (!Number.isInteger(len) || len < 1 || len > CHAR_LENGTH_MAX) {
      message.warning(`CHAR 最大允许长度须为 1 ~ ${CHAR_LENGTH_MAX} 之间的整数`)
      return
    }
  }
  if (isVarcharLengthRule.value) {
    const len = Number(form.varcharMaxLength)
    if (!Number.isInteger(len) || len < 1 || len > VARCHAR_LENGTH_MAX) {
      message.warning(`VARCHAR 最大允许长度须为 1 ~ ${VARCHAR_LENGTH_MAX} 之间的整数`)
      return
    }
  }
  if (isAutoIncRule.value && form.level !== 'off') {
    const inc = Number(form.autoIncValue)
    if (!Number.isInteger(inc) || inc < 1 || inc > AUTO_INC_MAX) {
      message.warning('表自增初始值须为 1 ~ 2147483647 之间的整数')
      return
    }
    const nc = String(form.autoIncNameCase || '').trim().toLowerCase()
    if (nc !== 'lower' && nc !== 'upper') {
      message.warning('字段名大小写须选择大写或小写')
      return
    }
  }
  if (isColumnCountRule.value && form.level !== 'off') {
    const cnt = Number(form.maxColumnCount)
    if (!Number.isInteger(cnt) || cnt < 1 || cnt > COLUMN_COUNT_MAX) {
      message.warning(`字段最大数量须为 1 ~ ${COLUMN_COUNT_MAX} 之间的整数`)
      return
    }
  }
  if (isPKKeyPartsRule.value && form.level !== 'off') {
    const parts = Number(form.pkMaxKeyParts)
    if (!Number.isInteger(parts) || parts < 1 || parts > PK_KEY_PARTS_MAX) {
      message.warning(`主键包含列的最大值须为 1 ~ ${PK_KEY_PARTS_MAX} 之间的整数`)
      return
    }
  }
  if (isPKColTypeRule.value && form.level !== 'off') {
    // 空白名单 = 不限制主键列类型，允许直接保存（不再强制至少选择一项）
    // 非法值由前端固定选项 + 后端保存校验双重拦截；可选集合按库取并集（Oracle 的 number 等）
    const types = Array.isArray(form.pkTypes) ? form.pkTypes.filter((t) => String(t).trim()) : []
    const invalid = types.filter((t) => !pkColTypeOptionList.value.includes(t))
    if (invalid.length > 0) {
      message.warning(`主键列类型 ${invalid.join('、')} 不在可选范围内`)
      return
    }
  }
  if (isSecKeyPartsRule.value && form.level !== 'off') {
    const parts = Number(form.secMaxKeyParts)
    if (!Number.isInteger(parts) || parts < 1 || parts > SEC_KEY_PARTS_MAX) {
      message.warning(`单个索引包含列的最大值须为 1 ~ ${SEC_KEY_PARTS_MAX} 之间的整数`)
      return
    }
  }
  if (isTableCharsetRule.value && form.level !== 'off') {
    const charsets = Array.isArray(form.tableCharsets) ? form.tableCharsets.filter((c) => String(c).trim()) : []
    if (charsets.length === 0) {
      message.warning('请至少选择一个允许的表字符集')
      return
    }
    const invalid = charsets.filter((c) => !TABLE_CHARSET_OPTIONS.includes(c))
    if (invalid.length > 0) {
      message.warning(`表字符集 ${invalid.join('、')} 不在可选范围内`)
      return
    }
  }
  if (isTableCollationRule.value && form.level !== 'off') {
    const collations = Array.isArray(form.tableCollations) ? form.tableCollations.filter((c) => String(c).trim()) : []
    if (collations.length === 0) {
      message.warning('请至少选择一个允许的表校验规则')
      return
    }
    const invalid = collations.filter((c) => !TABLE_COLLATION_OPTIONS.includes(c))
    if (invalid.length > 0) {
      message.warning(`表校验规则 ${invalid.join('、')} 不在可选范围内`)
      return
    }
  }
  if (isIdxCountRule.value && form.level !== 'off') {
    const cnt = Number(form.idxMaxCount)
    if (!Number.isInteger(cnt) || cnt < 1 || cnt > IDX_COUNT_MAX) {
      message.warning(`表索引数量的最大值须为 1 ~ ${IDX_COUNT_MAX} 之间的整数`)
      return
    }
  }
  if (isTableNameCaseRule.value && form.level !== 'off') {
    const nc = String(form.tableNameCase || '').trim().toLowerCase()
    if (nc !== 'lower' && nc !== 'upper') {
      message.warning('表名大小写值须选择小写或大写')
      return
    }
  }
  if (isRequiredColsRule.value && form.level !== 'off') {
    const cols = String(form.requiredCols || '')
      .split(/[,，]/)
      .map((c) => c.trim())
      .filter((c) => c)
    if (cols.length === 0) {
      message.warning('请至少填写一个表必须包含的字段')
      return
    }
  }
  if (isEngineRule.value && form.level !== 'off') {
    const engines = Array.isArray(form.engines) ? form.engines.filter((e) => String(e).trim()) : []
    if (engines.length === 0) {
      message.warning('请至少选择一个允许的存储引擎')
      return
    }
    const invalid = engines.filter((e) => !ENGINE_DEFAULTS.includes(e))
    if (invalid.length > 0) {
      message.warning(`存储引擎 ${invalid.join('、')} 不在可选范围内`)
      return
    }
  }
  if (isUniqIdxRule.value && form.level !== 'off') {
    const regex = String(form.uniqIdxRegex || '').trim()
    // 留空表示未修改，按默认表达式生效，不报错
    if (regex !== '' && !isValidRegExp(regex)) {
      message.warning(`唯一索引名称正则表达式不合法，请检查后重新输入（示例：${UNIQ_IDX_REGEX_DEFAULT}）`)
      return
    }
  }
  if (isNormalIdxRule.value && form.level !== 'off') {
    const regex = String(form.normalIdxRegex || '').trim()
    // 留空表示未修改，按默认表达式生效，不报错
    if (regex !== '' && !isValidRegExp(regex)) {
      message.warning(`索引名称正则表达式不合法，请检查后重新输入（示例：${NORMAL_IDX_REGEX_DEFAULT}）`)
      return
    }
  }
  if (isForbiddenTypeRule.value && form.level !== 'off') {
    const types = Array.isArray(form.forbiddenTypes) ? form.forbiddenTypes.filter((t) => String(t).trim()) : []
    if (types.length === 0) {
      message.warning('请至少选择一个禁止使用的数据类型')
      return
    }
    // 可选集合按库取并集：Oracle 规则的长期默认值 long / long raw 必须被认作合法
    const invalid = types.filter((t) => !forbiddenTypeOptionList.value.includes(t))
    if (invalid.length > 0) {
      message.warning(`数据类型 ${invalid.join('、')} 不在可选范围内`)
      return
    }
  }
  if (isDatabaseCharsetRule.value && form.level !== 'off') {
    const charsets = Array.isArray(form.databaseCharsets) ? form.databaseCharsets.filter((c) => String(c).trim()) : []
    if (charsets.length === 0) {
      message.warning('请至少选择一个允许的库字符集')
      return
    }
    const invalid = charsets.filter((c) => !DATABASE_CHARSET_OPTIONS.includes(c))
    if (invalid.length > 0) {
      message.warning(`字符集 ${invalid.join('、')} 不在可选范围内`)
      return
    }
  }
  if (isDMLAffectedRowsRule.value && form.level !== 'off') {
    const n = Number(form.dmlAffectedRows)
    if (!Number.isInteger(n) || n < 1 || n > DML_AFFECTED_ROWS_MAX) {
      message.warning(`影响行数须为 1 ~ ${DML_AFFECTED_ROWS_MAX} 之间的整数`)
      return
    }
  }
  if (isDMLJoinTablesRule.value && form.level !== 'off') {
    const n = Number(form.dmlJoinTables)
    if (!Number.isInteger(n) || n < 1 || n > DML_JOIN_TABLES_MAX) {
      message.warning(`关联表数量最大值须为 1 ~ ${DML_JOIN_TABLES_MAX} 之间的整数`)
      return
    }
  }
  if (isDMLLimitRowsRule.value && form.level !== 'off') {
    const n = Number(form.dmlLimitRows)
    if (!Number.isInteger(n) || n < 1 || n > DML_LIMIT_ROWS_MAX) {
      message.warning(`LIMIT 行数最大值须为 1 ~ ${DML_LIMIT_ROWS_MAX} 之间的整数`)
      return
    }
  }
  if (isDMLInsertRowsRule.value && form.level !== 'off') {
    const n = Number(form.dmlInsertRows)
    if (!Number.isInteger(n) || n < 1 || n > DML_INSERT_ROWS_MAX) {
      message.warning(`INSERT VALUES 最大行数须为 1 ~ ${DML_INSERT_ROWS_MAX} 之间的整数`)
      return
    }
  }
  if (isDMLUpdateTimeRule.value && form.level !== 'off') {
    const cols = String(form.dmlUpdateTimeCols || '')
      .split(/[,，]/)
      .map((c) => c.trim())
      .filter((c) => c)
    if (cols.length === 0) {
      message.warning('请至少填写一个建议更新的修改时间字段')
      return
    }
  }
  if (isDMLCreateTimeRule.value && form.level !== 'off') {
    const cols = String(form.dmlCreateTimeCols || '')
      .split(/[,，]/)
      .map((c) => c.trim())
      .filter((c) => c)
    if (cols.length === 0) {
      message.warning('请至少填写一个不建议更新的字段')
      return
    }
  }
  if (isDMLWhereInRule.value && form.level !== 'off') {
    const n = Number(form.dmlWhereIn)
    if (!Number.isInteger(n) || n < 1 || n > DML_WHERE_IN_MAX) {
      message.warning(`IN 子句元素个数最大值须为 1 ~ ${DML_WHERE_IN_MAX} 之间的整数`)
      return
    }
  }
  submitting.value = true
  const record = editingRecord.value
  const primary = record?._primary || record
  if (primary) {
    // 按勾选的数据库联动更新该能力项下的全部规则：
    //   勾选的库 → 应用抽屉中编辑的级别与描述（关闭状态的规则一并启用）；
    //   未勾选的库 → 置为 off（关闭），保留其原描述与参数，便于再次勾选时还原。
    // CHAR 长度规则额外把「CHAR 最大允许长度」写入勾选库规则的 params.max_length；
    // 自增初始值规则额外把「表自增初始值」写入 params.auto_inc_init_value。
    const description = String(form.description || '').trim()
    const rules = (record._rules || [record]).map((rule) => {
      const db = normalizeDbTypes([rule.db_type])[0]
      const checked = form.dbTypes.includes(db)
      let nextParams = rule.params || {}
      if (checked && isCharLengthRule.value) {
        nextParams = { ...nextParams, max_length: Number(form.maxLength) }
      }
      if (checked && isVarcharLengthRule.value) {
        nextParams = { ...nextParams, max_length: Number(form.varcharMaxLength) }
      }
      if (isAutoIncRule.value) {
        // 自增初始值规则：开启时同时写入 auto_inc_init_value 与 name_case；
        // 关闭或该库被取消勾选时把两个参数都移除，不参与提交或校验。
        if (checked && form.level !== 'off') {
          nextParams = {
            ...nextParams,
            auto_inc_init_value: Number(form.autoIncValue),
            name_case: String(form.autoIncNameCase).toLowerCase(),
          }
        } else {
          const { auto_inc_init_value, name_case, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isColumnCountRule.value) {
        // 仅在「数据库被勾选」且「规则级别不是 off」时写入 max_column_count；
        // 其余情况（规则关闭 / 该库被取消勾选）把该键从 params 中移除，不参与提交或校验。
        if (checked && form.level !== 'off') {
          nextParams = { ...nextParams, max_column_count: Number(form.maxColumnCount) }
        } else {
          const { max_column_count, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isPKKeyPartsRule.value) {
        // 限制主键包含列的个数规则：开启时写入 max_key_parts（与后端 rp.MaxKeyParts 对齐）；
        // 关闭或该库被取消勾选时移除该键，引擎回退默认值。
        if (checked && form.level !== 'off') {
          nextParams = { ...nextParams, max_key_parts: Number(form.pkMaxKeyParts) }
        } else {
          const { max_key_parts, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isPKColTypeRule.value) {
        // 限制主键列类型规则：开启时把用户选中的类型白名单（去重、小写）写入 params.pk_types；
        // 留空表示不限制类型（落库空数组，引擎侧直接放行）；关闭或该库被取消勾选时移除该键。
        if (checked && form.level !== 'off') {
          const selected = Array.from(
            new Set(
              (form.pkTypes || [])
                .map((t) => String(t).trim().toLowerCase())
                .filter((t) => t),
            ),
          )
          nextParams = { ...nextParams, pk_types: selected }
        } else {
          const { pk_types, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isSecKeyPartsRule.value) {
        // 限制单个索引包含列的个数规则：开启时写入 max_key_parts；
        // 关闭或该库被取消勾选时移除该键，引擎回退默认值。
        if (checked && form.level !== 'off') {
          nextParams = { ...nextParams, max_key_parts: Number(form.secMaxKeyParts) }
        } else {
          const { max_key_parts, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isTableCharsetRule.value) {
        // 限制表字符集规则：开启时把用户选中的字符集白名单（去重、小写）写入 params.charsets；
        // 留空视为未修改，回退默认 utf8mb4/utf8；关闭或该库被取消勾选时移除该键。
        if (checked && form.level !== 'off') {
          const selected = Array.from(
            new Set(
              (form.tableCharsets || [])
                .map((c) => String(c).trim().toLowerCase())
                .filter((c) => c),
            ),
          )
          nextParams = { ...nextParams, charsets: selected.length > 0 ? selected : TABLE_CHARSET_DEFAULT }
        } else {
          const { charsets, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isTableCollationRule.value) {
        // 限制表校验规则：开启时把用户选中的排序规则白名单（去重、小写）写入 params.collations；
        // 留空视为未修改，回退默认列表；关闭或该库被取消勾选时移除该键。
        if (checked && form.level !== 'off') {
          const selected = Array.from(
            new Set(
              (form.tableCollations || [])
                .map((c) => String(c).trim().toLowerCase())
                .filter((c) => c),
            ),
          )
          nextParams = { ...nextParams, collations: selected.length > 0 ? selected : TABLE_COLLATION_DEFAULT }
        } else {
          const { collations, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isIdxCountRule.value) {
        // 限制表索引数量规则：开启时写入 max_count（与后端 rp.MaxCount 对齐）；
        // 关闭或该库被取消勾选时移除该键，引擎回退默认值。
        if (checked && form.level !== 'off') {
          nextParams = { ...nextParams, max_count: Number(form.idxMaxCount) }
        } else {
          const { max_count, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isTableNameCaseRule.value) {
        // 限制表名大小写规则：开启时写入 name_case（与后端 TABLE_NAME_CASE 对齐）；
        // 关闭或该库被取消勾选时移除该键，引擎回退默认小写。
        if (checked && form.level !== 'off') {
          nextParams = { ...nextParams, name_case: String(form.tableNameCase || 'lower').trim().toLowerCase() }
        } else {
          const { name_case, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isRequiredColsRule.value) {
        // 表需要包含某些列规则：开启时把逗号分隔的字段列表（去空白、去重）写入 params.columns；
        // 留空视为未修改，回退默认 create_time,update_time；关闭或该库被取消勾选时移除该键。
        if (checked && form.level !== 'off') {
          const cols = Array.from(
            new Set(
              String(form.requiredCols || '')
                .split(/[,，]/)
                .map((c) => c.trim())
                .filter((c) => c),
            ),
          )
          nextParams = {
            ...nextParams,
            columns: cols.length > 0 ? cols : REQUIRED_COLS_DEFAULT.split(','),
          }
        } else {
          const { columns, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isKeywordRule.value) {
        // 关键字规则：开启时把用户选中的关键字列表（去重、大写）写入 params.keywords；
        // 取消部分关键字即缩小该列表；全部取消或关闭规则时移除 keywords，引擎回退到默认全集。
        if (checked && form.level !== 'off') {
          const selected = Array.from(
            new Set(
              (form.keywords || [])
                .map((k) => String(k).trim().toUpperCase())
                .filter((k) => k),
            ),
          )
          nextParams = { ...nextParams, keywords: selected }
        } else {
          const { keywords, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isEngineRule.value) {
        // 存储引擎规则：开启时把用户选中的引擎列表写入 params.engines；
        // 关闭或该库被取消勾选时移除 engines，引擎回退到默认支持列表。
        if (checked && form.level !== 'off') {
          const selected = Array.from(
            new Set(
              (form.engines || [])
                .map((e) => String(e).trim())
                .filter((e) => e),
            ),
          )
          nextParams = { ...nextParams, engines: selected }
        } else {
          const { engines, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isUniqIdxRule.value) {
        // 唯一索引名格式规则：开启时把用户输入的正则表达式写入 params.regex；
        // 留空视为未修改，回退默认表达式；关闭或该库被取消勾选时移除 regex。
        if (checked && form.level !== 'off') {
          const regex = String(form.uniqIdxRegex || '').trim()
          nextParams = { ...nextParams, regex: regex === '' ? UNIQ_IDX_REGEX_DEFAULT : regex }
        } else {
          const { regex, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isNormalIdxRule.value) {
        // 普通索引名格式规则：开启时把用户输入的正则表达式写入 params.regex（与后端
        // defaultNormalIdxRegex 对齐，留空回退默认 idx1_\w+）；关闭或该库被取消勾选时移除 regex。
        if (checked && form.level !== 'off') {
          const regex = String(form.normalIdxRegex || '').trim()
          nextParams = { ...nextParams, regex: regex === '' ? NORMAL_IDX_REGEX_DEFAULT : regex }
        } else {
          const { regex, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isForbiddenTypeRule.value) {
        // 限制列不能使用部分数据类型规则：开启时把用户选中的类型列表（去重、小写）写入 params.types；
        // 关闭或该库被取消勾选时移除 types，引擎不再限制任何数据类型。
        if (checked && form.level !== 'off') {
          const selected = Array.from(
            new Set(
              (form.forbiddenTypes || [])
                .map((t) => String(t).trim().toLowerCase())
                .filter((t) => t),
            ),
          )
          nextParams = { ...nextParams, types: selected }
        } else {
          const { types, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isDatabaseCharsetRule.value) {
        // 限制库的字符集规则：开启时把用户选中的字符集列表（去重、小写）写入 params.charsets；
        // 关闭或该库被取消勾选时移除 charsets，引擎不再限制库字符集。
        if (checked && form.level !== 'off') {
          const selected = Array.from(
            new Set(
              (form.databaseCharsets || [])
                .map((c) => String(c).trim().toLowerCase())
                .filter((c) => c),
            ),
          )
          nextParams = { ...nextParams, charsets: selected }
        } else {
          const { charsets, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isDMLAffectedRowsRule.value) {
        // 限制 UPDATE/DELETE 单个语句总影响行数：开启时写入 max_affected_rows；
        // 关闭或该库被取消勾选时移除该键，引擎不限制影响行数。
        if (checked && form.level !== 'off') {
          nextParams = { ...nextParams, max_affected_rows: Number(form.dmlAffectedRows) }
        } else {
          const { max_affected_rows, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isDMLJoinTablesRule.value) {
        // 限制 UPDATE/DELETE 语句多表关联的数量：开启时写入 max_join_tables；
        // 关闭或该库被取消勾选时移除该键。
        if (checked && form.level !== 'off') {
          nextParams = { ...nextParams, max_join_tables: Number(form.dmlJoinTables) }
        } else {
          const { max_join_tables, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isDMLLimitRowsRule.value) {
        // 限制 UPDATE/DELETE 语句 LIMIT 数据量大小：开启时写入 max_limit_rows；
        // 关闭或该库被取消勾选时移除该键。
        if (checked && form.level !== 'off') {
          nextParams = { ...nextParams, max_limit_rows: Number(form.dmlLimitRows) }
        } else {
          const { max_limit_rows, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isDMLInsertRowsRule.value) {
        // 限制 INSERT 语句一条 INSERT VALUES 的总行数：开启时写入 max_insert_rows；
        // 关闭或该库被取消勾选时移除该键。
        if (checked && form.level !== 'off') {
          nextParams = { ...nextParams, max_insert_rows: Number(form.dmlInsertRows) }
        } else {
          const { max_insert_rows, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isDMLUpdateTimeRule.value) {
        // UPDATE 语句建议更新表上的修改时间列：开启时把逗号分隔的候选列写入 update_time_columns；
        // 留空回退默认 update_time；关闭或该库被取消勾选时移除该键。
        if (checked && form.level !== 'off') {
          const cols = String(form.dmlUpdateTimeCols || '')
            .split(/[,，]/)
            .map((c) => c.trim())
            .filter((c) => c)
          nextParams = { ...nextParams, update_time_columns: cols.length > 0 ? cols.join(',') : DML_UPDATE_TIME_COLS_DEFAULT }
        } else {
          const { update_time_columns, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isDMLCreateTimeRule.value) {
        // UPDATE 语句不建议更新表上的创建时间列：开启时把逗号分隔的字段写入 create_time_columns；
        // 留空回退默认 create_time；关闭或该库被取消勾选时移除该键。
        if (checked && form.level !== 'off') {
          const cols = String(form.dmlCreateTimeCols || '')
            .split(/[,，]/)
            .map((c) => c.trim())
            .filter((c) => c)
          nextParams = { ...nextParams, create_time_columns: cols.length > 0 ? cols.join(',') : DML_CREATE_TIME_COLS_DEFAULT }
        } else {
          const { create_time_columns, ...rest } = nextParams
          nextParams = rest
        }
      }
      if (isDMLWhereInRule.value) {
        // 限制 WHERE 条件中 IN 子句包含元素个数：开启时写入 max_in_elements；
        // 关闭或该库被取消勾选时移除该键。
        if (checked && form.level !== 'off') {
          nextParams = { ...nextParams, max_in_elements: Number(form.dmlWhereIn) }
        } else {
          const { max_in_elements, ...rest } = nextParams
          nextParams = rest
        }
      }
      return {
        ...rule,
        level: checked ? form.level : 'off',
        params: nextParams,
        description: checked ? description : rule.description || '',
      }
    })
    emit('update', rules)
  }
  submitting.value = false
  isModalOpen.value = false
  editingRecord.value = null
}
</script>

<style scoped>
/* 单元格统一内边距，保证相邻列之间的留白间距完全一致 */
:deep(.ant-table-cell) {
  padding: 12px 16px;
}

/* 规则参数行下方的说明文案 */
.param-hint {
  margin-top: 4px;
  color: rgba(0, 0, 0, 0.45);
  font-size: 12px;
  line-height: 1.5;
}

/* 表头所有列不换行 */
:deep(.ant-table-thead > tr > th) {
  white-space: nowrap;
}

/* 表格占满父容器（tabpane）剩余空间：逐级 flex 传递高度，
   表头固定、表体自适应剩余高度并在内容溢出时内部滚动，分页固定在底部。

   注意：.ant-table-wrapper 是 a-table 的「根元素」，本组件的 scopeId 就落在它自己身上；
   :deep() 会被编译成后代选择器 [data-v-x] .ant-table-wrapper，永远选不中它自己，
   因此这里必须写普通选择器 —— 编译结果是 .ant-table-wrapper[data-v-x]，才能命中。
   少了这条，容器会退回 display:block + min-height:auto，被表格内容整体撑高，
   分页随之被挤出 tabpane 的 overflow:hidden 区域而「消失」。 */
.ant-table-wrapper {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  margin-bottom: 0;
}

:deep(.ant-spin-nested-loading) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

:deep(.ant-spin-container) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

:deep(.ant-table) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

:deep(.ant-table-container) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

/* 表体：占满剩余高度，内容溢出时才出现内部纵向滚动；
   覆盖 scroll.y 写入的内联 max-height，改为随 flex 自适应 */
:deep(.ant-table-body) {
  flex: 1;
  min-height: 0;
  max-height: none !important;
  overflow-y: auto !important;
}

/* 分页条与表格同宽、同属一个容器，固定在表格底部不随内容滚动；
   右对齐展示，小屏空间不足时自动换行，桌面端与移动端显示一致且不溢出 */
:deep(.ant-pagination) {
  flex: 0 0 auto;
  margin: 12px 0 0;
  justify-content: flex-end;
  flex-wrap: wrap;
  row-gap: 8px;
}

/* 规则级别列：去掉 a-tag 的圆角、背景、边框，只保留字体颜色 */
.level-text {
  font-weight: 500;
  background: transparent;
  border: none;
  padding: 0;
  margin: 0;
  line-height: 1.5;
}

.level-text--must {
  color: #ff4d4f;
}

.level-text--suggest {
  color: #faad14;
}

.level-text--off {
  color: rgba(0, 0, 0, 0.45);
}

.level-warn-icon {
  color: var(--ant-color-warning, #faad14);
  font-size: 13px;
}

.db-logo {
  /* 全站统一：数据库类型 logo 一律 20px */
  width: 20px;
  height: 20px;
  display: inline-block;
  object-fit: contain;
  vertical-align: middle;
}

.db-logo-list {
  display: inline-flex;
  align-items: center;
  flex-wrap: nowrap;
}

/* 分页器底部 margin 归零（表格容器自身的 margin 已在上方统一处理） */
:deep(.ant-pagination) {
  margin-bottom: 0;
}

@media (max-width: 768px) {
  /* 规则表列数多且只有 y 滚动 */
  :deep(.ant-table-content) {
    overflow-x: auto;
  }

  :deep(.ant-table) {
    min-width: 900px;
  }
}
</style>
