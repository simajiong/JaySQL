import Antd from 'ant-design-vue'
import { createPinia } from 'pinia'
import piniaPluginPersistedstate from 'pinia-plugin-persistedstate'
import { createApp } from 'vue'
import { VueCookieNext } from 'vue-cookie-next'
import App from './App.vue'
import './permission' // 路由权限控制
import './utils/entryQuery' // 进入路由前先留一份原始 query（冷启动深链兜底，见该文件说明）
import router from './router'
import { setupUserStoreTabSync } from './store/user'
import { setupPreventHistorySwipe } from './utils/preventHistorySwipe'
// 大屏图表语义色：把 chartTokens 的色板注入成 CSS 自定义属性（--chart-instance-* / --chart-alert-*），
// 让样式表与 SVG 共用同一份色值。放样式引入之后、createApp 之前，首帧即有色。
import { applyChartTokens } from '@/views/monitor/chartTokens'
applyChartTokens()

// 引入antd样式
import 'ant-design-vue/dist/reset.css'
import '@/assets/scss/index.scss'
import 'epic-designer/dist/style.css'
import { setupAntd } from 'epic-designer/dist/ui/antd'
setupAntd()

// 引入vue-cropper
import VueCodemirror from 'vue-codemirror'
import VueCropper from 'vue-cropper'
import 'vue-cropper/dist/index.css'

// 引入highlight.js
import hljsVuePlugin from '@highlightjs/vue-plugin' // 支持vue3的组件
import 'highlight.js/lib/common' // 依赖包
import 'highlight.js/styles/atom-one-dark.css' // 样式

const app = createApp(App)

// pinia
const pinia = createPinia()
pinia.use(piniaPluginPersistedstate)
app.use(pinia)
// 多标签页登录态同步：其他 tab 登录/续期/退出时，本 tab 内存态同步更新
// （否则旧 tab 唤醒后拿过期 token 发请求，401 被局部吞成「空数据」假象）
setupUserStoreTabSync(pinia)

app
  .use(router)
  .use(Antd)
  .use(VueCookieNext)
  .use(VueCropper)
  .use(VueCodemirror)
  .use(hljsVuePlugin)
  .mount('#app')

setupPreventHistorySwipe()
