// 前端本地脱敏预览：与后端 datasec/services/mask.go 的 ApplyMaskAlgorithm 保持同一口径。
// 仅用于规则列表展示与编辑弹窗的实时预览，真正的脱敏永远以后端结果为准。
const SAMPLE = '13812345678'

// 各敏感类型的真实样例：效果预览按类型取材，避免所有规则都拿手机号演示。
// 取真实规则里合法的样本——身份证 18 位带校验位、银行卡 62 开头、地址含省市区，
// 这样 preview 出来的长度与形态和实际列内容一致，不会让人误判规则效果。
export const DATA_TYPE_SAMPLES = {
  phone: '13812345678',
  id_card: '330106199211044628',
  bank_card: '6222020200112347',
  email: 'zhangsan@example.com',
  address: '浙江省杭州市西湖区文一西路100号',
  name: '张伟',
  custom: 'ABCD1234XY5678',
}

export const sampleOfDataType = (dataType) => DATA_TYPE_SAMPLES[dataType] || SAMPLE

function parseParams(raw) {
  try {
    if (typeof raw === 'string' && raw) return JSON.parse(raw)
    if (raw && typeof raw === 'object') return raw
  } catch { /* 非法参数按空处理 */ }
  return {}
}

function maskKeep(value, keepHead, keepTail) {
  const runes = Array.from(value)
  keepHead = Math.max(keepHead || 0, 0)
  keepTail = Math.max(keepTail || 0, 0)
  if (keepHead + keepTail >= runes.length) return '*'.repeat(runes.length)
  return runes.slice(0, keepHead).join('') + '*'.repeat(runes.length - keepHead - keepTail) + (keepTail ? runes.slice(runes.length - keepTail).join('') : '')
}

export function maskPreview(algorithm, rawParams, sample = SAMPLE) {
  const params = parseParams(rawParams)
  switch (algorithm) {
    case 'head_tail':
      return maskKeep(sample, params.keep_head, params.keep_tail)
    case 'tail':
      return maskKeep(sample, 0, params.keep_tail)
    case 'head':
      return maskKeep(sample, params.keep_head, 0)
    case 'full':
      return '*'.repeat(Array.from(sample).length)
    case 'email': {
      const at = sample.lastIndexOf('@')
      if (at <= 0) return '*'.repeat(sample.length)
      return sample[0] + '*'.repeat(Math.max(at - 1, 3)) + sample.slice(at)
    }
    case 'address': {
      const runes = Array.from(sample)
      let keep = 0
      runes.forEach((ch, i) => {
        if ('省市区县'.includes(ch)) keep = i + 1
      })
      if (!keep || keep >= runes.length) return maskKeep(sample, 6, 0)
      return runes.slice(0, keep).join('') + '*'.repeat(runes.length - keep)
    }
    case 'name': {
      const runes = Array.from(sample)
      return runes.length <= 1 ? '*'.repeat(runes.length) : runes[0] + '*'.repeat(runes.length - 1)
    }
    case 'hash':
      return 'sha256:3b3f6a9c1d2e4f50…'
    default:
      return sample
  }
}
