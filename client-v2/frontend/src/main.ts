import { createApp } from 'vue'
import { installEscLedger } from '@/services/overlays'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import { applyTheme, loadTheme } from './styles/theme' // V2-4：首屏主题，避免闪白
import './styles/tokens.css'
import './styles/base.css'

applyTheme(loadTheme()) // V2-4 一行级改动：挂载前落 html[data-theme]

// Esc 记账必须第一个装：它要在任何浮层消费者之前记录"这次 Esc 出发时有没有浮层"
// （见 services/overlays.ts 的说明——右键菜单在 document 捕获阶段就把节点摘掉了）。
installEscLedger()

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
