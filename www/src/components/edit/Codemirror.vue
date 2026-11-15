<template>
  <!-- resize 的唯一尺寸源 -->
  <div ref="wrapper" class="cm-wrapper" :initVal="props.initVal" :height="props.height">
    <!-- CodeMirror 挂载点 -->
    <div ref="editor" style="height: 100%"></div>
  </div>
</template>

<script setup>
defineOptions({ name: 'GoCodemirror' })

import { autocompletion, completionKeymap } from '@codemirror/autocomplete'
import { copyLineDown, cursorDocEnd, cursorDocStart, deleteLine, historyKeymap, indentWithTab, selectAll } from '@codemirror/commands'
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import {
  MSSQL,
  MySQL,
  PLSQL,
  PostgreSQL,
  SQLDialect,
  StandardSQL,
  schemaCompletionSource,
  sql,
} from '@codemirror/lang-sql'
import { Compartment, EditorSelection, EditorState, Prec } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import { tags } from '@lezer/highlight'
import { basicSetup } from 'codemirror'
import { format } from 'sql-formatter'
import {
  FAMILY_CLICKHOUSE,
  FAMILY_MYSQL,
  FAMILY_ORACLE,
  FAMILY_POSTGRES,
  FAMILY_SQLSERVER,
  resolveDbFamily,
} from '../../utils/dbFamily'
import { extractAliasScope } from '../../utils/sqlAliasParser'
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'

const emit = defineEmits(['state-change'])

/* refs */
const wrapper = ref(null)
const editor = ref(null)
/* 必须 shallowRef：EditorView 实例 toRawType() 是 'Object'，ref() 会把它塞进
   reactive() 变成 Proxy。之后 editorView.value.dispatch() 的 this 就是 Proxy，
   CodeMirror 内部按对象身份查 ViewPlugin（view.plugin(spec) → pluginMap.get(spec)）
   时，Vue 会对 Map 的值做 toReactive 包装，导致 PluginInstance.plugin != spec，
   重新 find 也因元素被包装而落空，最终把 null 写进原始 pluginMap 并永久缓存
   （pluginMap.clear() 只在 viewPlugin facet 变化时才执行）。
   后果：所有 ViewPlugin 型 decorations 静默丢失 —— 语法高亮、当前行高亮、
   括号匹配全没了，但静态装饰和 Layer（drawSelection）正常，极难排查。
   DOM 节点不受影响（toRawType 是 'HTMLDivElement'，不参与 reactive）。 */
const editorView = shallowRef(null)
let resizeObserver = null
let nativeCopyBridgeTimer = null
let nativeCopyBridgeTextarea = null

/* props（原样保留） */
const props = defineProps({
  initVal: {
    type: String,
    default: '',
  },
  height: {
    type: String,
    default: '200px',
  },
  // 外部权威方言（引擎家族：mysql / postgresql / sqlserver / oracle / clickhouse）。
  // 用途：父组件可能在编辑器挂载前就调 setDialect（那时没有 view、会被静默丢弃），
  // 靠这个 prop 兜底，保证方言一定能落到正确值，不会一直停在 standard。
  dialect: {
    type: String,
    default: '',
  },
})

/* compartments：语言（决定高亮）与补全（决定提示）拆开。
   schema 每次加载表元数据都会变，若和语言绑在同一个 compartment，
   语法树会被反复重建、补全命名空间反复重建，高亮也会跟着抖。 */
const languageCompartment = new Compartment()
const completionCompartment = new Compartment()
const editableCompartment = new Compartment()
const readonlyCompartment = new Compartment()
const currentDbType = ref(props.dialect || 'standard')
const currentCompletionSchema = ref({})
// view 未就绪时暂存的方言，initEditor 里补应用
let pendingDialect = props.dialect || ''

const getSelectionTextFromView = (view) => {
  if (!view) return ''
  let text = ''
  for (const range of view.state.selection.ranges) {
    if (!range.empty) {
      text += view.state.doc.sliceString(range.from, range.to)
    }
  }
  return text
}

const cleanupNativeCopyBridge = () => {
  if (nativeCopyBridgeTimer != null) {
    window.clearTimeout(nativeCopyBridgeTimer)
    nativeCopyBridgeTimer = null
  }
  if (nativeCopyBridgeTextarea?.parentNode) {
    nativeCopyBridgeTextarea.parentNode.removeChild(nativeCopyBridgeTextarea)
  }
  nativeCopyBridgeTextarea = null
}

const prepareNativeCopyBridge = (view) => {
  const text = getSelectionTextFromView(view)
  if (!text || typeof document === 'undefined' || !document.body) {
    return
  }
  cleanupNativeCopyBridge()
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', 'true')
  textarea.setAttribute('aria-hidden', 'true')
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  textarea.style.left = '-9999px'
  textarea.style.top = '0'
  textarea.style.pointerEvents = 'none'
  document.body.appendChild(textarea)
  textarea.focus()
  textarea.select()
  nativeCopyBridgeTextarea = textarea
  nativeCopyBridgeTimer = window.setTimeout(() => {
    cleanupNativeCopyBridge()
    view?.focus()
  }, 0)
}

/* 固定扩展 */
const sqlHighlightStyle = HighlightStyle.define([
  { tag: [tags.keyword, tags.controlKeyword, tags.operatorKeyword, tags.modifier], color: '#7c3aed', fontWeight: '700' },
  { tag: [tags.comment, tags.lineComment, tags.blockComment], color: '#6b7280', fontStyle: 'italic' },
  { tag: [tags.string, tags.special(tags.string)], color: '#0f766e' },
  { tag: [tags.number, tags.integer, tags.float, tags.bool, tags.null], color: '#b45309' },
  { tag: [tags.operator, tags.punctuation, tags.separator], color: '#475569' },
  { tag: [tags.paren, tags.squareBracket, tags.brace], color: '#475569' },
  { tag: [tags.name, tags.variableName, tags.propertyName], color: '#0f172a' },
  { tag: [tags.definition(tags.name), tags.standard(tags.name)], color: '#1d4ed8' },
  { tag: [tags.typeName, tags.className], color: '#0369a1' },
  { tag: [tags.function(tags.name), tags.labelName], color: '#2563eb' },
  { tag: [tags.atom, tags.special(tags.variableName)], color: '#9333ea' },
])

const sqlEditorTheme = Prec.highest(EditorView.theme({
  '&': {
    backgroundColor: '#ffffff',
    color: '#0f172a',
    fontSize: '13px',
  },
  '.cm-content': {
    caretColor: '#1677ff',
  },
  '.cm-cursor, .cm-dropCursor': {
    borderLeftColor: '#1677ff',
  },
  '.cm-selectionBackground': {
    backgroundColor: '#7cb8ff !important',
  },
  '& > .cm-scroller > .cm-selectionLayer .cm-selectionBackground': {
    backgroundColor: '#7cb8ff !important',
  },
  '&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground': {
    backgroundColor: '#7cb8ff !important',
  },
  '.cm-content::selection, .cm-content ::selection': {
    backgroundColor: '#7cb8ff !important',
  },
  '.cm-line::selection, .cm-line ::selection': {
    backgroundColor: '#7cb8ff !important',
  },
  '.cm-activeLine': {
    backgroundColor: '#f8fafc',
  },
  '.cm-activeLineGutter': {
    backgroundColor: '#f1f5f9',
  },
}, { dark: false }))

const fixedExtensions = [
  basicSetup,
  Prec.highest(keymap.of(completionKeymap)),
  keymap.of([
    { key: 'Alt-Shift-ArrowDown', run: copyLineDown },
    ...historyKeymap,
    indentWithTab,
  ]),
  EditorView.domEventHandlers({
    copy(_event, view) {
      const text = getSelectionTextFromView(view)
      if (!text) return false
      return false
    },
    cut(event, view) {
      const text = getSelectionTextFromView(view)
      if (!text) return false
      if (!event?.clipboardData?.setData) return false
      event.clipboardData.setData('text/plain', text)
      event.preventDefault()
      const selection = view.state.selection.main
      view.dispatch({
        changes: { from: selection.from, to: selection.to, insert: '' },
        selection: EditorSelection.cursor(selection.from),
        scrollIntoView: true,
      })
      return true
    },
  }),
  EditorView.lineWrapping,
  EditorView.updateListener.of((update) => {
    if (!update.docChanged && !update.selectionSet) return
    const selection = update.state.selection.main
    syncSelectionVisualState(!selection?.empty)
    emit('state-change', {
      content: update.state.doc.toString(),
      cursorPos: selection?.head ?? 0,
      selectedText: selection?.empty ? '' : update.state.doc.sliceString(selection.from, selection.to),
      hasSelection: !selection?.empty,
    })
  }),
  // 标准增量高亮：按视口懒解析、随编辑增量更新，不在输入事务里做全文档
  // 同步解析（此前用 StateField 每键 ensureSyntaxTree 全量重解析 + 重建装饰，
  // 最长阻塞主线程 200ms，是 IME 输入被打断、光标异常的主要来源）。
  syntaxHighlighting(sqlHighlightStyle),
  sqlEditorTheme,
]

const normalizeDbType = (value) => String(value || '').trim().toLowerCase()
/* 家族判定统一走 utils/dbFamily，与后端 dbfamily 同口径：
   percona / pg / polar-postgres / OceanBase 等在这里都能被正确归类，
   不再各自维护一份白名单（漏一种类型就退化成通用方言，方言关键字不高亮）。 */
const resolveFamily = (value) => normalizeDbType(resolveDbFamily(value))

/* lang-sql 内置 MySQL / PostgreSQL / MSSQL / PLSQL / SQLite / StandardSQL。
   Oracle 用 PLSQL 并补上 PLSQL 词表缺的常见 Oracle 关键字；
   ClickHouse 没有内置方言，用 PostgreSQL 词表派生。
   方言词表决定哪些 token 被解析成 Keyword —— 落到 StandardSQL 时
   TOP / OFFSET / MERGE / RETURNING / SHOW / EXPLAIN / SAMPLE… 一律不高亮。 */
const ORACLE_EXTRA_KEYWORDS =
  'merge nvl nvl2 decode dual offset over fetch sysdate systimestamp bulk collect forall package tablespace savepoint'
const CLICKHOUSE_EXTRA_KEYWORDS =
  'prewhere final settings global array materialized engine ttl optimize distributed sample attach detach freeze mutation'
let oracleDialect = null
let clickHouseDialect = null
const deriveDialect = (base, extraKeywords) =>
  SQLDialect.define({ ...base.spec, keywords: `${base.spec.keywords || ''} ${extraKeywords}` })

const getDialectByDbType = (dbType) => {
  switch (resolveFamily(dbType)) {
    case FAMILY_MYSQL:
      return MySQL
    case FAMILY_POSTGRES:
      return PostgreSQL
    case FAMILY_SQLSERVER:
      return MSSQL
    case FAMILY_ORACLE:
      if (!oracleDialect) oracleDialect = deriveDialect(PLSQL, ORACLE_EXTRA_KEYWORDS)
      return oracleDialect
    case FAMILY_CLICKHOUSE:
      if (!clickHouseDialect) clickHouseDialect = deriveDialect(PostgreSQL, CLICKHOUSE_EXTRA_KEYWORDS)
      return clickHouseDialect
    default:
      return StandardSQL
  }
}

const getFormatterLanguage = (dbType) => {
  switch (resolveFamily(dbType)) {
    case FAMILY_MYSQL:
      return 'mysql'
    case FAMILY_POSTGRES:
      return 'postgresql'
    case FAMILY_SQLSERVER:
      return 'tsql'
    case FAMILY_ORACLE:
      return 'plsql'
    default:
      return 'sql'
  }
}

/* 补全 schema 数据规范化：统一处理 {table: [cols]} 与 {table: {col: {type,info}}} 两种格式 */
const NORMALIZED_SCHEMA_CACHE = new WeakMap()
const normalizeCompletionSchema = (rawSchema) => {
  if (!rawSchema || typeof rawSchema !== 'object') return { tables: Object.create(null), columns: Object.create(null) }
  if (NORMALIZED_SCHEMA_CACHE.has(rawSchema)) return NORMALIZED_SCHEMA_CACHE.get(rawSchema)
  const tables = Object.create(null)
  const columns = Object.create(null)
  for (const key of Object.keys(rawSchema)) {
    const value = rawSchema[key]
    const keyLower = key.toLowerCase()
    tables[keyLower] = key
    if (Array.isArray(value)) {
      const colMap = Object.create(null)
      for (const col of value) {
        const cname = String(col || '')
        if (!cname) continue
        colMap[cname.toLowerCase()] = { label: cname, type: 'property' }
      }
      columns[keyLower] = colMap
    } else if (value && typeof value === 'object') {
      const colMap = Object.create(null)
      if (value.self && typeof value.self === 'object') {
        // 增强格式
      }
      for (const ck of Object.keys(value)) {
        if (ck === 'self') continue
        const cv = value[ck]
        if (typeof cv === 'string') {
          colMap[ck.toLowerCase()] = { label: cv, type: 'property' }
        } else if (cv && typeof cv === 'object') {
          colMap[ck.toLowerCase()] = {
            label: cv.label || ck,
            type: cv.type || 'property',
            info: cv.info,
            detail: cv.detail,
            boost: cv.boost,
          }
        }
      }
      columns[keyLower] = colMap
    }
  }
  const normalized = { tables, columns }
  NORMALIZED_SCHEMA_CACHE.set(rawSchema, normalized)
  return normalized
}

/* 别名感知的 CompletionSource：只在命中 alias. 前缀时返回，否则 return null（交给 lang-sql 默认处理） */
const createAliasAwareCompletionSource = (rawSchema) => {
  const normalized = normalizeCompletionSchema(rawSchema)
  const lookupRealTableKey = (scopeAliases, aliasOrName) => {
    if (!aliasOrName) return null
    const lower = aliasOrName.toLowerCase()
    const candidates = []
    if (scopeAliases && scopeAliases[lower]) {
      candidates.push(scopeAliases[lower])
      const segs = String(scopeAliases[lower]).split('.')
      if (segs.length >= 2) {
        candidates.push(segs.slice(-2).join('.'))
        candidates.push(segs[segs.length - 1])
      }
    }
    candidates.push(aliasOrName)
    if (lower.includes('.')) {
      const segs = lower.split('.')
      candidates.push(segs.slice(-2).join('.'))
      candidates.push(segs[segs.length - 1])
    }
    for (const cand of candidates) {
      if (!cand) continue
      const key = String(cand).toLowerCase()
      if (normalized.tables[key]) return normalized.tables[key]
    }
    return null
  }
  return (ctx) => {
    if (!ctx || typeof ctx.pos !== 'number') return null
    const doc = ctx.state.doc
    const pos = ctx.pos
    const textBefore = doc.sliceString(Math.max(0, pos - 512), pos)
    const match = textBefore.match(/([A-Za-z_][A-Za-z0-9_$]*)\.([A-Za-z0-9_$]*)$/)
    if (!match) return null
    const aliasRaw = match[1]
    const partial = match[2] || ''
    const from = pos - partial.length
    try {
      const fullText = doc.toString()
      const scopeAliases = extractAliasScope(fullText, pos) || (typeof window !== 'undefined' && window.__GI_DEBUG_ALIAS__ ? {} : {})
      const realTableKey = lookupRealTableKey(scopeAliases, aliasRaw)
      if (!realTableKey) return null
      const colMap = normalized.columns[realTableKey.toLowerCase()]
      if (!colMap) return null
      const options = []
      const partialLower = partial.toLowerCase()
      for (const ckey of Object.keys(colMap)) {
        const col = colMap[ckey]
        if (partial && !ckey.startsWith(partialLower)) continue
        options.push({
          label: col.label,
          type: col.type || 'property',
          info: col.info,
          detail: col.detail,
          boost: (col.boost != null ? col.boost : 12) + (partial ? 5 : 0),
        })
      }
      if (!options.length) return null
      return {
        from,
        options,
        validFor: /^[A-Za-z0-9_$]*$/,
      }
    } catch (e) {
      if (typeof console !== 'undefined' && console.debug) console.debug('[alias-completion] parse failed', e)
      return null
    }
  }
}

const syncSelectionVisualState = (hasSelection) => {
  wrapper.value?.classList.toggle('cm-wrapper--has-selection', !!hasSelection)
}

/* 只跟方言有关：换它才会重建语法树（高亮生效的来源）。 */
const buildLangExtension = (dbType = currentDbType.value) => [
  sql({ dialect: getDialectByDbType(dbType) }),
]

/* 只跟补全有关：schema 变化不再牵动语法树。 */
const buildCompletionExtension = (dbType = currentDbType.value, completionData = currentCompletionSchema.value) => {
  const dialect = getDialectByDbType(dbType)
  const safeSchema = completionData || {}
  const sources = []
  if (Object.keys(safeSchema).length) {
    sources.push(dialect.language.data.of({ autocomplete: schemaCompletionSource({ schema: safeSchema, dialect }) }))
  }
  sources.push(dialect.language.data.of({ autocomplete: createAliasAwareCompletionSource(safeSchema) }))
  return sources
}

const isEmptyCompletion = (value) => !value || Object.keys(value).length === 0
const sameCompletion = (a, b) => a === b || (isEmptyCompletion(a) && isEmptyCompletion(b))

const applySqlLanguage = (dbType = currentDbType.value, completionData = currentCompletionSchema.value) => {
  const nextDbType = dbType || 'standard'
  const nextCompletion = completionData || {}
  /* view 还没建好（父组件在 CodeMirror 挂载前就调了 setDialect）：
     先记下来。原实现在这里直接 return，方言就永远停在 standard 了。 */
  if (!editorView.value) {
    pendingDialect = nextDbType
    currentDbType.value = nextDbType
    currentCompletionSchema.value = nextCompletion
    return
  }
  // 只有方言真正变了才动语言 compartment（原实现只看字符串且要求补全非空，
  // 导致 applyEditorCompletion({}) 这类空调用也会整树重建）。
  const dialectUnchanged = getDialectByDbType(nextDbType) === getDialectByDbType(currentDbType.value)
  const completionUnchanged = sameCompletion(currentCompletionSchema.value, nextCompletion)
  if (dialectUnchanged && completionUnchanged) {
    return
  }
  const effects = []
  if (!dialectUnchanged) {
    currentDbType.value = nextDbType
    effects.push(languageCompartment.reconfigure(buildLangExtension(currentDbType.value)))
  }
  if (!dialectUnchanged || !completionUnchanged) {
    currentCompletionSchema.value = nextCompletion
    effects.push(completionCompartment.reconfigure(buildCompletionExtension(currentDbType.value, currentCompletionSchema.value)))
  }
  editorView.value.dispatch({
    effects,
    selection: editorView.value.state.selection,
    scrollIntoView: false,
  })
  editorView.value.requestMeasure()
}

const emitCurrentState = () => {
  if (!editorView.value) return
  const selection = editorView.value.state.selection.main
  syncSelectionVisualState(!selection?.empty)
  emit('state-change', {
    content: editorView.value.state.doc.toString(),
    cursorPos: selection?.head ?? 0,
    selectedText: selection?.empty ? '' : editorView.value.state.doc.sliceString(selection.from, selection.to),
    hasSelection: !selection?.empty,
  })
}

const releaseDocumentSelectionLock = () => {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  const body = document.body
  if (root?.style) {
    if (root.style.userSelect) root.style.userSelect = ''
    if (root.style.webkitUserSelect) root.style.webkitUserSelect = ''
  }
  if (body?.style) {
    if (body.style.userSelect) body.style.userSelect = ''
    if (body.style.webkitUserSelect) body.style.webkitUserSelect = ''
  }
}

const handleWrapperPointerDown = (event) => {
  releaseDocumentSelectionLock()
  window.requestAnimationFrame(() => {
    editorView.value?.focus()
  })
  void event
}

const handleWrapperKeydown = (event) => {
  const key = String(event?.key || '')
  if ((event?.metaKey || event?.ctrlKey) && !event?.altKey && !event?.shiftKey && key.toLowerCase() === 'c') {
    prepareNativeCopyBridge(editorView.value)
  }
}

/* 初始化编辑器（initVal 只在初始化时生效，行为不变） */
const initEditor = () => {
  if (editorView.value) {
    editorView.value.destroy()
  }

  // 挂载前外部若已指定方言（prop / setDialect），这里补上，避免停在 standard
  const initialDbType = pendingDialect || props.dialect || currentDbType.value || 'standard'
  pendingDialect = ''
  currentDbType.value = initialDbType

  const startState = EditorState.create({
    doc: props.initVal,
    extensions: [
      fixedExtensions,
      languageCompartment.of(buildLangExtension(currentDbType.value)),
      completionCompartment.of(buildCompletionExtension(currentDbType.value, currentCompletionSchema.value)),
      readonlyCompartment.of(EditorState.readOnly.of(false)),
      editableCompartment.of(EditorView.editable.of(true)),
    ],
  })

  editorView.value = new EditorView({
    state: startState,
    parent: editor.value,
  })

  emitCurrentState()
}

/* 设置只读 */
const setReadonly = (readonly) => {
  if (!editorView.value) return
  editorView.value.dispatch({
    effects: readonlyCompartment.reconfigure(EditorState.readOnly.of(readonly)),
  })
}

/* 设置高度（外部强制覆盖） */
const setHeight = (height) => {
  if (!wrapper.value) return
  wrapper.value.style.height = height
  editorView.value?.requestMeasure()
}

/* 设置自动补全 */
const setCompletion = (completionData) => {
  applySqlLanguage(currentDbType.value, completionData)
}

const setDialect = (dbType, completionData = currentCompletionSchema.value) => {
  applySqlLanguage(dbType, completionData)
}

/* 格式化 */
const formatContent = (dbType = currentDbType.value) => {
  if (!editorView.value) return
  const raw = editorView.value.state.doc.toString()
  let formatted = raw
  try {
    formatted = format(raw, { language: getFormatterLanguage(dbType) })
  } catch {
    formatted = format(raw)
  }
  editorView.value.dispatch({
    changes: {
      from: 0,
      to: editorView.value.state.doc.length,
      insert: formatted,
    },
  })
}

const replaceSelection = (content = '') => {
  if (!editorView.value) return
  const selection = editorView.value.state.selection.main
  const from = selection.from
  editorView.value.dispatch({
    changes: {
      from: selection.from,
      to: selection.to,
      insert: content,
    },
    selection: EditorSelection.range(from, from + String(content).length),
    scrollIntoView: true,
  })
  editorView.value.focus()
}

const insertTextAtCursor = (content = '') => {
  if (!editorView.value) return
  const selection = editorView.value.state.selection.main
  const from = selection.from
  editorView.value.dispatch({
    changes: {
      from: selection.from,
      to: selection.to,
      insert: content,
    },
    selection: EditorSelection.cursor(from + String(content).length),
    scrollIntoView: true,
  })
  editorView.value.focus()
}

const formatSelection = (dbType = currentDbType.value) => {
  const raw = getSelectedText()
  if (!raw) return false
  let formatted = raw
  try {
    formatted = format(raw, { language: getFormatterLanguage(dbType) })
  } catch {
    formatted = format(raw)
  }
  replaceSelection(formatted)
  return true
}

/* 获取内容 */
const getContent = () => {
  return editorView.value?.state.doc.toString() || ''
}

/* 获取选中内容 */
const getSelectedText = () => {
  if (!editorView.value) return ''
  let text = ''
  for (const range of editorView.value.state.selection.ranges) {
    if (!range.empty) {
      text += editorView.value.state.doc.sliceString(range.from, range.to)
    }
  }
  return text
}

/* 设置内容 */
const setContent = (content) => {
  if (!editorView.value) return
  editorView.value.dispatch({
    changes: {
      from: 0,
      to: editorView.value.state.doc.length,
      insert: content,
    },
  })
  editorView.value.requestMeasure()
}

const focus = () => {
  editorView.value?.focus()
}

// 强制 CodeMirror 重新测量布局（用于父组件在容器尺寸变化后刷新编辑器）
const layout = () => {
  editorView.value?.requestMeasure()
}

// 跳转并高亮指定行（行号从 1 开始）
const goToLine = (lineNumber = 1) => {
  if (!editorView.value) return
  const doc = editorView.value.state.doc
  let line
  try {
    line = doc.line(Math.max(1, Math.min(lineNumber, doc.lines)))
  } catch {
    return
  }
  editorView.value.dispatch({
    selection: EditorSelection.range(line.from, line.to),
    scrollIntoView: true,
  })
  editorView.value.focus()
}

const selectAllContent = () => {
  if (!editorView.value) return
  const docLength = editorView.value.state.doc.length
  editorView.value.dispatch({
    selection: EditorSelection.range(0, docLength),
    scrollIntoView: true,
  })
  editorView.value.focus()
}


/* lifecycle */
onMounted(() => {
  // height 初始生效
  if (wrapper.value) {
    wrapper.value.style.height = props.height
  }

  initEditor()

  // resize 时通知 CodeMirror 重算
  resizeObserver = new ResizeObserver(() => {
    editorView.value?.requestMeasure()
  })

  resizeObserver.observe(wrapper.value)
  wrapper.value?.addEventListener('pointerdown', handleWrapperPointerDown, true)
  wrapper.value?.addEventListener('keydown', handleWrapperKeydown, true)
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  wrapper.value?.removeEventListener('pointerdown', handleWrapperPointerDown, true)
  wrapper.value?.removeEventListener('keydown', handleWrapperKeydown, true)
  cleanupNativeCopyBridge()
  editorView.value?.destroy()
})

/* 方言 prop 变化即应用（切实例 / 切引擎家族时不用依赖外部记得调 setDialect） */
watch(
  () => props.dialect,
  (val) => {
    if (!val) return
    applySqlLanguage(val, currentCompletionSchema.value)
  },
  { immediate: true },
)

/* 外部动态修改 height 时仍然生效 */
watch(
  () => props.height,
  (val) => {
    if (!wrapper.value) return
    wrapper.value.style.height = val
    editorView.value?.requestMeasure()
  },
)

/* expose（原样保留） */
defineExpose({
  focus,
  formatContent,
  formatSelection,
  getContent,
  getSelectedText,
  goToLine,
  insertTextAtCursor,
  replaceSelection,
  setCompletion,
  setDialect,
  setContent,
  setReadonly,
  setHeight,
  selectAllContent,
  layout,
  editorView,
})
</script>

<style scoped>
.cm-wrapper {
  overflow: hidden;
  border: 1px solid #f0f0f0;
  user-select: text;
  -webkit-user-select: text;
}

/* 统一 scroller 的字体与行高，让 gutter 与内容区继承同一套排版，
   避免外部样式或浏览器默认行高导致行号/内容基线错位 */
.cm-wrapper :deep(.cm-scroller) {
  overflow: auto;
  font-family: 'SF Mono', 'Menlo', 'Consolas', 'Monaco', 'Courier New', monospace;
  line-height: 1.6;
}

/* 显式让内容区、gutter、行、行号元素继承 scroller 的字体与行高，
   防止其他作用域样式把它们改回不同值 */
.cm-wrapper :deep(.cm-content),
.cm-wrapper :deep(.cm-line),
.cm-wrapper :deep(.cm-gutters),
.cm-wrapper :deep(.cm-gutterElement) {
  font-family: inherit;
  line-height: inherit;
}

.cm-wrapper.cm-wrapper--has-selection :deep(.cm-activeLine),
.cm-wrapper.cm-wrapper--has-selection :deep(.cm-activeLineGutter) {
  background-color: transparent !important;
}

.cm-wrapper :deep(.cm-selectionLayer .cm-selectionBackground),
.cm-wrapper :deep(.cm-editor.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground) {
  background: rgba(22, 119, 255, 0.32) !important;
  opacity: 1 !important;
}

.cm-wrapper :deep(.cm-content::selection),
.cm-wrapper :deep(.cm-content ::selection),
.cm-wrapper :deep(.cm-line::selection),
.cm-wrapper :deep(.cm-line ::selection) {
  background: rgba(22, 119, 255, 0.32) !important;
  color: inherit !important;
}

/* CodeMirror 填满容器 */
.cm-wrapper :deep(.cm-editor) {
  height: 100%;
  user-select: text;
  -webkit-user-select: text;
}

:deep(.cm-scroller),
:deep(.cm-content) {
  user-select: text;
  -webkit-user-select: text;
}

/* gutter 样式 */
:deep(.cm-gutters) {
  color: #9f9d9d;
  background-color: #f7f7f7;
}

/* 去焦点 outline */
:deep(.cm-editor.cm-focused) {
  outline: none !important;
}
</style>
