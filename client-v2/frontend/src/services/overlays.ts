/* services/overlays.ts —— 「现在有没有浮层压在上面」的唯一判据
 *
 * 为什么需要它：v1 的 Esc 语义是分层的（`player.py` 帮助浮层优先、
 * `_on_playback_esc` 全屏→退全屏否则关窗、`main.py:1781` 主窗口 Esc=详情返回）。
 * v2 里这些层分散在不同地方：Vue 组件（Sorani 登录框）、
 * 命令式 DOM（右键菜单 `services/contextMenu.ts` 自己 addEventListener）。
 * 页面级 Esc 若不先看有没有浮层，就会发生**一次按键干两件事**：
 * 菜单被关掉的同时页面也返回了——用户看起来像"按 Esc 直接跳走"。
 *
 * 判据用 DOM，不用"注册计数"：`document.addEventListener('keydown', …, true)`
 * 的菜单监听跑在捕获阶段、组件监听跑在冒泡阶段，注册顺序并不保证谁先；
 * 而"元素在不在 DOM 里"与顺序无关，也不会有漏 unregister 导致的永久假阳性。
 *
 * ⚠ 新增浮层时必须让它落进这里认得的标记之一（`.zgj-ctx-menu` / `[role=dialog]`），
 * 否则它会和页面级 Esc 打架。约定写在这里，别散落到各个组件里再抄一遍。
 */

/** 会吃掉 Esc 的浮层根节点标记（右键菜单 + 模态框） */
const OVERLAY_SELECTOR = '.zgj-ctx-menu, [role="dialog"]'

/** 当前是否有浮层开着（按 DOM 实际状态判断） */
export function anyModalOpen(): boolean {
  return !!document.querySelector(OVERLAY_SELECTOR)
}

/* ───────── Esc 记账：为什么光看 DOM 还不够 ─────────
 *
 * 实测踩到的：右键菜单在 **document 捕获阶段**听 keydown，Esc 一按就先 `closeMenu()`
 * 把节点从 DOM 里摘掉；等页面级监听（冒泡阶段）再去看 `anyModalOpen()` 时
 * 已经是 false 了 —— 于是"关菜单"和"页面返回"在同一次按键里都发生。
 * 用注册顺序去赌谁先跑是站不住的（菜单是命令式挂载、对话框组件是声明式，
 * 两者时机都不一样），所以改成：**在任何消费者之前**先把"这次 Esc 出发时有没有浮层"记下来。
 *
 * 记在 window 的捕获阶段：window 捕获早于 document 捕获，
 * 因此无论消费者注册得多早、在哪个阶段听，读到的都是按键**发生那一刻**的状态。
 */
let escSawOverlayFlag = false
let installed = false

/** 全局装一次（main.ts 启动时）；重复调用无害 */
export function installEscLedger(): void {
  if (installed || typeof window === 'undefined') return
  installed = true
  window.addEventListener(
    'keydown',
    (e: KeyboardEvent) => {
      if (e.key === 'Escape') escSawOverlayFlag = anyModalOpen()
    },
    true, // 捕获：必须早于一切消费者
  )
}

/** 本次 Esc 按下时是否有浮层开着（供页面级 Esc 判断该不该让位） */
export function escSawOverlay(): boolean {
  return escSawOverlayFlag
}

/** 用完即清，避免状态漂到下一次按键（页面级处理器调用） */
export function clearEscLedger(): void {
  escSawOverlayFlag = false
}
