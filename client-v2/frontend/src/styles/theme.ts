/* ================================================================
   V2-4 主题应用工具 —— dark / light / auto 三态。
   - 只操作 document.documentElement[data-theme]（tokens.css 双色板由属性选择器切换）
   - 持久化 localStorage 'zgj.theme'，默认 dark（与 base.css/tokens.css 的缺省一致）
   - auto：监听 matchMedia('(prefers-color-scheme: dark)')，系统翻转即时生效
   - 无 Vue 依赖，main.ts 首屏即可调用 applyTheme(loadTheme())，避免闪白
   ================================================================ */

export type ThemeMode = 'dark' | 'light' | 'auto'

const STORE_KEY = 'zgj.theme'

const VALID: ThemeMode[] = ['dark', 'light', 'auto']

/** 读取持久化的主题模式；非法值/缺省一律 dark。 */
export function loadTheme(): ThemeMode {
  try {
    const v = localStorage.getItem(STORE_KEY) as ThemeMode | null
    return v && VALID.includes(v) ? v : 'dark'
  } catch {
    return 'dark'
  }
}

function systemIsDark(): boolean {
  try {
    return window.matchMedia('(prefers-color-scheme: dark)').matches
  } catch {
    return true // 无 matchMedia 环境（旧 webview）按产品默认 dark
  }
}

function applyResolved(mode: ThemeMode): void {
  const theme = mode === 'auto' ? (systemIsDark() ? 'dark' : 'light') : mode
  document.documentElement.setAttribute('data-theme', theme)
  document.documentElement.style.colorScheme = theme
}

let currentMode: ThemeMode = 'dark'
let mediaAttached = false

/** 应用并持久化主题模式。auto 时挂系统监听（只挂一次，幂等）。 */
export function applyTheme(mode: ThemeMode): void {
  currentMode = mode
  try {
    localStorage.setItem(STORE_KEY, mode)
  } catch {
    /* 隐私模式写不进：本次会话内仍然生效 */
  }
  if (!mediaAttached) {
    mediaAttached = true
    try {
      const mq = window.matchMedia('(prefers-color-scheme: dark)')
      const onChange = () => {
        if (currentMode === 'auto') applyResolved(currentMode)
      }
      if (typeof mq.addEventListener === 'function') mq.addEventListener('change', onChange)
      else if (typeof mq.addListener === 'function') mq.addListener(onChange)
    } catch {
      /* matchMedia 不可用：auto 退化为本次解析一次的 dark，不致命 */
    }
  }
  applyResolved(mode)
}
