// 表名批量选择的纯逻辑：全选 / 通配匹配 / 反选共用的排序与模式解析。
// 抽出来是为了能脱离组件直接验证边界（通配转义、去重、清单外表名保序）。

// 通配转正则：* 匹配任意多字符，? 匹配单个字符，忽略大小写。
// 先转义正则元字符再替换通配符，避免用户输入 .+()[] 之类把模式打乱。
export const wildcardToRegExp = (pattern) => {
  const escaped = String(pattern)
    .replace(/[.+^${}()|[\]\\]/g, '\\$&')
    .replace(/\*/g, '.*')
    .replace(/\?/g, '.')
  return new RegExp(`^${escaped}$`, 'i')
}

// 选中项归一：去空、去重，清单内的按清单顺序排，清单外的手工输入按原顺序追加在末尾。
// 统一排序是为了让批量操作与逐条勾选混合时结果稳定，不因操作顺序不同产生差异。
export const orderSelection = (names, universe) => {
  const order = new Map((universe || []).map((name, idx) => [name, idx]))
  const known = []
  const custom = []
  const seen = new Set()
  ;(names || []).forEach((name) => {
    const key = String(name || '').trim()
    if (!key || seen.has(key)) return
    seen.add(key)
    if (order.has(key)) known.push(key)
    else custom.push(key)
  })
  known.sort((a, b) => order.get(a) - order.get(b))
  return [...known, ...custom]
}

// 解析多组通配模式（英文/中文逗号、空白分隔），返回命中的表名与未命中的模式。
// 只从已加载的表清单里匹配——后端只接受标识符，模式必须在前端展开成具体表名。
export const matchTablePatterns = (raw, universe) => {
  const names = universe || []
  const patterns = String(raw || '').trim().split(/[,，\s]+/).filter(Boolean)
  const matched = new Set()
  const missing = []
  patterns.forEach((pattern) => {
    const regExp = wildcardToRegExp(pattern)
    const hit = names.filter((name) => regExp.test(name))
    if (hit.length) hit.forEach((name) => matched.add(name))
    else missing.push(pattern)
  })
  return { patterns, matched: [...matched], missing }
}
