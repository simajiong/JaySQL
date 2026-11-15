// store/user.js
import { GetUserProfileApi } from '@/api/login'
import {
  aiUserKeyOf,
  clearAIConversationStorage,
  suspendAIConversationPersist,
} from '@/views/ai/aiConversationStorage'
import { defineStore } from 'pinia'

/**
 * 头像地址净化
 * blob: 地址只在创建它的页面生命周期内有效，一旦随 store 持久化到 localStorage，
 * 刷新后必然失效（头像会回落首字方块）。统一在写入前过滤，避免脏值落库。
 */
const sanitizeAvatarUrl = (url) => {
  const value = String(url || '').trim()
  return /^blob:/i.test(value) ? '' : value
}

// 与 persist.paths 保持一致：跨标签页同步只合并这份白名单里的字段
const PERSISTED_USER_FIELDS = [
  'uid',
  'username',
  'nickname',
  'token',
  'avatar',
  'email',
  'mobile',
  'roles',
  'role',
  'date_joined',
  'is_superuser',
  'permission_ids',
  'permissions',
]
const USER_STORE_PERSIST_KEY = 'user-store'
// 历史遗留的独立 token 键（setToken 双写），跨 tab 同步同样要盯它
const USER_TOKEN_LEGACY_KEY = 'onLine'

const readPersistedUserState = () => {
  try {
    const raw = localStorage.getItem(USER_STORE_PERSIST_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw)
    return parsed && typeof parsed === 'object' ? parsed : null
  } catch {
    return null
  }
}

export const useUserStore = defineStore('user', {
  state: () => ({
    uid: '',
    username: '',
    nickname: '',
    token: localStorage.getItem('onLine') || '',
    avatar: '',
    email: '',
    mobile: '',
    roles: [],
    role: '',
    date_joined: '',
    is_superuser: undefined, // 初始为 undefined，用于判断是否已加载
    permission_ids: [],
    permissions: [],
  }),

  persist: {
    key: 'user-store',
    storage: localStorage,
    paths: ['uid', 'username', 'nickname', 'token', 'avatar', 'email', 'mobile', 'roles', 'role', 'date_joined', 'is_superuser', 'permission_ids', 'permissions'],
    // 历史版本曾把本地 blob: 地址写入头像并持久化，恢复时清理这类失效值
    afterRestore: (ctx) => {
      if (ctx.store.avatar !== sanitizeAvatarUrl(ctx.store.avatar)) {
        ctx.store.avatar = ''
      }
    },
  },

  getters: {
    /**
     * 用户显示名
     * 优先展示登录账号，避免昵称与实际登录用户不一致
     */
    displayName: (state) => state.username || state.nickname || '未知用户',

    /**
     * 是否已登录
     */
    isLoggedIn: (state) => !!state.token,

    /**
     * 用户信息是否已完整加载（必须同时具备用户名和超级管理员布尔标记）
     */
    isInfoLoaded: (state) => {
      if (typeof state.is_superuser !== 'boolean') return false
      return !!state.username
    },

    /**
     * 权限判定
     * - 传字符串：命中该权限即通过
     * - 传数组：命中任意一项即通过（数组语义 = 任一）
     * 注意：路由守卫会把 meta.permission 原样透传进来。历史上这里只支持字符串，
     *       叶子路由若声明成数组会 `perms.includes(['a','b'])` 恒为 false → 静默跳 403，
     *       因此曾给 /backup/storage_targets 单独打补丁绕过。此处补齐数组语义，避免复发。
     */
    hasPermission: (state) => (permission) => {
      if (typeof state.is_superuser === 'boolean' && state.is_superuser) {
        return true
      }
      if (!permission) {
        return true
      }
      const perms = Array.isArray(state.permissions) ? state.permissions : []
      if (Array.isArray(permission)) {
        if (!permission.length) return true
        return permission.some((item) => item && perms.includes(item))
      }
      return perms.includes(permission)
    },
  },

  actions: {
    /**
     * 批量设置用户信息
     * @param {Object} userInfo 用户信息对象
     */
    setUserInfo(userInfo) {
      this.uid = userInfo.uid || ''
      this.username = userInfo.username || ''
      this.nickname = userInfo.username || ''
      this.avatar = sanitizeAvatarUrl(userInfo.avatar_file)
      this.email = userInfo.email || ''
      this.mobile = userInfo.mobile || ''
      this.roles = Array.isArray(userInfo.roles) ? userInfo.roles : []
      this.role = userInfo.role || (Array.isArray(userInfo.roles) ? userInfo.roles.map((item) => item.name || item).join('; ') : '')
      this.date_joined = userInfo.date_joined || ''
      // 后端 profile 直接扫描 MySQL tinyint，is_superuser 实际下发的是 1/0（int），
      // 不是布尔。这里必须兼容 1/"1"/true，否则真超管会被当成普通用户走权限过滤，
      // 导致新增路由（如 SQL工作台 → 访问申请）因未绑定权限点被 filterAsyncRoutes 剪掉。
      const su = userInfo.is_superuser
      this.is_superuser = su === true || su === 1 || su === '1'
      this.permission_ids = Array.isArray(userInfo.permission_ids) ? userInfo.permission_ids : []
      this.permissions = Array.isArray(userInfo.effective_permissions) ? userInfo.effective_permissions : []
    },

    /**
     * 设置用户头像（远程/服务端地址；blob: 地址会被过滤，避免持久化后失效）
     * @param {String} avatarUrl
     */
    setUserAvatar(avatarUrl) {
      this.avatar = sanitizeAvatarUrl(avatarUrl)
    },

    /**
     * 从 localStorage 持久化快照同步本 tab 的内存态（多标签页同步的接收端）
     *
     * storage 事件只在「其他 tab 写入」时在本 tab 触发，因此跨 tab 同步只能
     * 依赖事件驱动，而非定时轮询。按白名单整份合并（token + 身份一起换），
     * 避免「B 的 token 配 A 的身份」这种错位——权限判定、草稿归属键都依赖身份。
     */
    syncFromPersisted() {
      const persisted = readPersistedUserState()
      if (!persisted) return false
      let tokenChanged = false
      for (const field of PERSISTED_USER_FIELDS) {
        if (persisted[field] === undefined) continue
        const nextValue = field === 'avatar' ? sanitizeAvatarUrl(persisted[field]) : persisted[field]
        if (field === 'token' && nextValue !== this.token) {
          tokenChanged = true
        }
        this[field] = nextValue
      }
      return tokenChanged
    },

    /**
     * 设置 Token
     * @param {String} token 用户 token
     */
    setToken(token) {
      this.token = token
      localStorage.setItem('onLine', token)
    },

    /**
     * 获取用户信息
     * @returns {Promise<Object>} 用户信息
     */
    async getInfo() {
      try {
        const res = await GetUserProfileApi()

        if (res?.code === '0000') {
          this.setUserInfo(res.data)
          return res.data
        }

        throw new Error(res?.message || '获取用户信息失败/Token失效')
      } catch (error) {
        console.error('[User] 获取用户信息失败:', error)
        throw error
      }
    },

    /**
     * 清除用户信息
     *
     * 这里同时是三条「登录会话结束」路径的必经点：显式退出登录（Layout.vue）、
     * 401 会话过期（utils/request.js）、鉴权失败跳登录（permission.js）。
     */
    clear() {
      // AI 助手会话属于「登录会话级」数据（口径见 views/ai/aiConversationStorage.js）：
      // 登出即清除本账号记录，重新登录必须是全新界面，不把上一个登录会话的聊天记录带过去。
      // 顺序要紧 —— 先关落盘闸门，再清键：若用户当时正停在 AI 页，登出后的整页刷新
      // 会触发该页 beforeunload 的兜底落盘，不关闸会把刚清掉的记录原样写回。
      try {
        suspendAIConversationPersist()
        clearAIConversationStorage(aiUserKeyOf(this))
      } catch (_) {
        /* 存储被禁用时静默降级，不影响登出主流程 */
      }
      this.$reset() // 使用 Pinia 的 $reset 方法重置状态
      this.token = '' // $reset 后需要手动清除 token
      localStorage.removeItem('onLine')
      localStorage.removeItem('user-store')
    },
  },
})

/**
 * 多标签页登录态同步（2026-11-10）
 *
 * 背景：同源页面的 storage 事件只在「写入方以外的其他标签页」触发。另一个 tab
 * 登录 / token 续期 / 退出登录后，本 tab 的 Pinia 内存态仍攥着旧 token；闲置
 * 唤醒（forceMarkIdleWakeup）后本 tab 拿过期 token 发请求 → 401，而调用方的
 * `.catch(() => {})` 会把 401 吞成「空数据」假象（左树显示「暂无授权数据源」
 * 但编辑区还展示草稿 SQL —— 2026-11-10 1.19 请求日志实锤该场景）。
 *
 * 这里监听 user-store / onLine 的跨 tab 变更：
 *   - 有新快照 → 合并回内存（token 与身份一起同步）；
 *   - 键被移除（其他 tab 退出登录）→ 仅清本地 token，后续请求走全局 401 流程
 *     跳登录。不在本 tab 重复执行 clear() 的清理副作用（AI 会话清键等留给
 *     登出方所在 tab 处理，避免双写竞争）。
 */
export function setupUserStoreTabSync(pinia) {
  if (typeof window === 'undefined') return
  if (window.__GI_USER_STORE_TAB_SYNC__) return
  window.__GI_USER_STORE_TAB_SYNC__ = true
  window.addEventListener('storage', (event) => {
    if (!event) return
    if (event.key !== USER_STORE_PERSIST_KEY && event.key !== USER_TOKEN_LEGACY_KEY) return
    const store = useUserStore(pinia)
    if (event.newValue == null) {
      // 其他 tab 退出登录：本地 token 立即失效
      if (store.token) store.token = ''
      return
    }
    store.syncFromPersisted()
  })
}
