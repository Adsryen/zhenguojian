/* ================================================================
   网页式右键上下文菜单（V2-6）。
   背景：Wails 生产壳禁用了 WebView2 默认右键菜单（main.go 壳参数），
   选中文字后无「复制」可用——这里在 document 级接管 contextmenu，
   按上下文弹出自绘菜单（复制/剪切/粘贴/全选）。
   剪贴板策略：navigator.clipboard 优先；被 WebView2 权限策略拒绝时
   回落 Go 桥 App.ClipboardWrite / App.ClipboardRead（bindings_clipboard.go）。
   桥不存在（VITE_MOCK/浏览器直开）只用 web API，不报错。
   挂载方式：App.vue 里 setupContextMenu() 一处调用，不改任何 view 组件。
   ================================================================ */
import { t } from './i18n'

type CtxAction = () => void

interface MenuItem {
  label: string
  hint?: string
  run: CtxAction
}

const STYLE_ID = 'zgj-ctx-style'
const Z_INDEX = 70 // 应用内弹层最高 60（DetailView/Reserve/History 遮罩），菜单再高一档

let menuEl: HTMLDivElement | null = null
let globalCleanups: Array<() => void> = []

/* ---------- 剪贴板通道（web 优先，桥回落） ---------- */

function bridgeApp(): any {
  return (window as any).go?.main?.App
}

async function clipboardWrite(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    return
  } catch {
    /* WebView2 权限/焦点策略可能拒绝 → 回落桥 */
  }
  const app = bridgeApp()
  if (app?.ClipboardWrite) {
    try {
      await app.ClipboardWrite(text)
    } catch (err) {
      console.warn('[contextMenu] 桥写剪贴板失败:', err)
    }
  }
}

async function clipboardRead(): Promise<string> {
  try {
    if (navigator.clipboard?.readText) {
      return await navigator.clipboard.readText()
    }
  } catch {
    /* readText 可能被权限策略直接 reject → 回落桥 */
  }
  const app = bridgeApp()
  if (app?.ClipboardRead) {
    try {
      return String((await app.ClipboardRead()) ?? '')
    } catch (err) {
      console.warn('[contextMenu] 桥读剪贴板失败:', err)
    }
  }
  return ''
}

/* ---------- 输入框上下文 ---------- */

const EDITABLE_INPUT_TYPES = ['text', 'search', 'url', 'email', 'password']

interface EditableCtx {
  node: HTMLInputElement | HTMLTextAreaElement
  isPassword: boolean
}

function editableContext(target: EventTarget | null): EditableCtx | null {
  if (!(target instanceof Element)) return null
  const node = target.closest('input, textarea')
  if (!(node instanceof HTMLInputElement) && !(node instanceof HTMLTextAreaElement)) return null
  if (node.disabled || node.readOnly) return null
  if (node instanceof HTMLTextAreaElement) return { node, isPassword: false }
  const type = (node.type || 'text').toLowerCase()
  if (!EDITABLE_INPUT_TYPES.includes(type)) return null
  return { node, isPassword: type === 'password' }
}

function fireInputEvent(node: HTMLInputElement | HTMLTextAreaElement): void {
  node.dispatchEvent(new Event('input', { bubbles: true }))
}

function cutAction(ctx: EditableCtx): CtxAction {
  return () => {
    const { node } = ctx
    node.focus() // 恢复 selectionStart/End 呈现（blur 不清选择值）
    const start = node.selectionStart ?? 0
    const end = node.selectionEnd ?? 0
    // 任务书指定 execCommand('cut') 优先；返回 false（无选择/被禁）走手动路径
    if (end > start && document.execCommand('cut')) return
    const selected = node.value.slice(start, end)
    if (!selected) return
    void clipboardWrite(selected).then(() => {
      node.setRangeText('', start, end, 'end')
      fireInputEvent(node)
    })
  }
}

function pasteAction(ctx: EditableCtx): CtxAction {
  return () => {
    const { node } = ctx
    node.focus()
    void clipboardRead().then((text) => {
      if (!text) return
      const start = node.selectionStart ?? node.value.length
      const end = node.selectionEnd ?? start
      node.setRangeText(text, start, end, 'end')
      fireInputEvent(node)
    })
  }
}

function selectAllInputAction(ctx: EditableCtx): CtxAction {
  return () => {
    ctx.node.focus()
    ctx.node.select()
  }
}

/* ---------- 页面级动作 ---------- */

function copySelectionAction(selected: string): CtxAction {
  return () => void clipboardWrite(selected)
}

function selectAllPageAction(): CtxAction {
  return () => {
    if (!document.execCommand('selectAll')) {
      const sel = window.getSelection()
      if (!sel) return
      const range = document.createRange()
      range.selectNodeContents(document.body)
      sel.removeAllRanges()
      sel.addRange(range)
    }
  }
}

/* ---------- 菜单构建 ---------- */

function buildItems(e: MouseEvent): MenuItem[] {
  const inputCtx = editableContext(e.target)
  if (inputCtx) {
    const items: MenuItem[] = [
      { label: t('ctx.cut'), hint: 'Ctrl+X', run: cutAction(inputCtx) }
    ]
    if (!inputCtx.isPassword) {
      items.push({ label: t('ctx.paste'), hint: 'Ctrl+V', run: pasteAction(inputCtx) })
    }
    items.push({ label: t('ctx.selectAll'), hint: 'Ctrl+A', run: selectAllInputAction(inputCtx) })
    return items
  }
  const selected = window.getSelection()?.toString() ?? ''
  if (selected.trim().length > 0) {
    return [
      { label: t('ctx.copy'), hint: 'Ctrl+C', run: copySelectionAction(selected) },
      { label: t('ctx.selectAll'), hint: 'Ctrl+A', run: selectAllPageAction() }
    ]
  }
  // 纯媒体区（图/视频/画布）且无选区：无可复制内容，不弹（任务书约定的空菜单路径）
  const target = e.target
  if (target instanceof Element && target.closest('img, video, canvas')) return []
  return [{ label: t('ctx.selectAll'), hint: 'Ctrl+A', run: selectAllPageAction() }]
}

const STYLE_TEXT = `
.zgj-ctx-menu{position:fixed;z-index:${Z_INDEX};min-width:200px;padding:4px;
  border-radius:8px;border:1px solid var(--border,#2b2b36);
  background:var(--surface-2,#171b23);
  box-shadow:0 8px 24px rgba(0,0,0,.32);
  font-size:13px;color:var(--text,#d7dce5);
  user-select:none;-webkit-user-select:none}
.zgj-ctx-item{display:flex;align-items:center;justify-content:space-between;gap:24px;
  padding:6px 10px;border-radius:4px;line-height:1.5;cursor:default}
.zgj-ctx-item:hover{background:var(--guo-accent-soft,#4a83ff26)}
.zgj-ctx-key{color:var(--muted,#8d97a8);font-size:12px;letter-spacing:.2px}
`

function injectStyle(): void {
  if (document.getElementById(STYLE_ID)) return
  const style = document.createElement('style')
  style.id = STYLE_ID
  style.textContent = STYLE_TEXT
  document.head.appendChild(style)
}

function closeMenu(): void {
  if (menuEl) {
    menuEl.remove()
    menuEl = null
  }
  globalCleanups.forEach((fn) => fn())
  globalCleanups = []
}

function addGlobalListeners(): void {
  const onPointerDown = (ev: PointerEvent) => {
    if (menuEl && ev.target instanceof Node && !menuEl.contains(ev.target)) closeMenu()
  }
  const onKeyDown = (ev: KeyboardEvent) => {
    if (ev.key === 'Escape') closeMenu()
  }
  const onScrollOrResize = () => closeMenu()
  const onBlur = () => closeMenu()
  document.addEventListener('pointerdown', onPointerDown, true)
  document.addEventListener('keydown', onKeyDown, true)
  window.addEventListener('scroll', onScrollOrResize, true)
  window.addEventListener('resize', onScrollOrResize)
  window.addEventListener('blur', onBlur)
  globalCleanups.push(
    () => document.removeEventListener('pointerdown', onPointerDown, true),
    () => document.removeEventListener('keydown', onKeyDown, true),
    () => window.removeEventListener('scroll', onScrollOrResize, true),
    () => window.removeEventListener('resize', onScrollOrResize),
    () => window.removeEventListener('blur', onBlur)
  )
}

function showMenu(e: MouseEvent): void {
  const items = buildItems(e)
  if (items.length === 0) return
  injectStyle()
  closeMenu() // 重开时先清旧实例与旧监听

  menuEl = document.createElement('div')
  menuEl.className = 'zgj-ctx-menu'
  menuEl.style.left = '0px'
  menuEl.style.top = '0px'
  for (const item of items) {
    const row = document.createElement('div')
    row.className = 'zgj-ctx-item'
    const label = document.createElement('span')
    label.textContent = item.label
    row.appendChild(label)
    if (item.hint) {
      const key = document.createElement('span')
      key.className = 'zgj-ctx-key'
      key.textContent = item.hint
      row.appendChild(key)
    }
    row.addEventListener('click', () => {
      closeMenu()
      item.run()
    })
    menuEl.appendChild(row)
  }
  document.body.appendChild(menuEl)

  // 视口内 clamp：先渲染量尺寸，再定位
  const rect = menuEl.getBoundingClientRect()
  const x = Math.max(6, Math.min(e.clientX, window.innerWidth - rect.width - 6))
  const y = Math.max(6, Math.min(e.clientY, window.innerHeight - rect.height - 6))
  menuEl.style.left = `${x}px`
  menuEl.style.top = `${y}px`

  addGlobalListeners()
}

function onContextMenu(e: MouseEvent): void {
  e.preventDefault()
  showMenu(e)
}

let installed = false

/** App.vue 挂载入口：document 级接管 contextmenu。重复调用无副作用。 */
export function setupContextMenu(): void {
  if (installed || typeof document === 'undefined') return
  installed = true
  document.addEventListener('contextmenu', onContextMenu)
}
