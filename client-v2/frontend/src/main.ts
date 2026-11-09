import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import { applyTheme, loadTheme } from './styles/theme' // V2-4：首屏主题，避免闪白
import './styles/tokens.css'
import './styles/base.css'

applyTheme(loadTheme()) // V2-4 一行级改动：挂载前落 html[data-theme]

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
