// 剪贴板工具
//
// 内网通过 http://IP:port 访问时不是安全上下文，navigator.clipboard 不可用，
// 必须退化为 textarea + execCommand('copy')，否则复制按钮在线上环境静默失效。

const copyTextFallback = (text) => {
  try {
    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.style.position = 'fixed'
    textarea.style.top = '-1000px'
    textarea.style.left = '-1000px'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.focus()
    textarea.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(textarea)
    return ok
  } catch {
    return false
  }
}

// 写入剪贴板，返回是否成功（不抛异常）
export const writeClipboard = async (text) => {
  const value = String(text == null ? '' : text)
  if (!value) return false
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(value)
      return true
    }
    return copyTextFallback(value)
  } catch {
    return copyTextFallback(value)
  }
}
