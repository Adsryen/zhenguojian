/* electron/videoSurface.js —— P3 视频面：Electron 主进程自管的 BaseWindow。
 *
 * 架构位置（对照 research/electron-migration-plan.md 的 JavdBviewed 模式）：
 *   - 窗口归主进程：getNativeWindowHandle() 拿到 XID（Linux）/HWND（Windows），
 *     经 __embed_set_wid 交给 sidecar，Go 只做 playerSvc.SetWid（mpv --wid）；
 *   - 内嵌五方法（PlayerAttach/SetBounds/HitBands/Detach/ForceWindow）由
 *     main.js 拦截，本文件承载几何与可见性；
 *   - 输入模型与 v1 同形：BaseWindow 无 webContents，setIgnoreMouseEvents(true,
 *     {forward:true}) 让事件穿到父窗 WebView——HTML 层（覆盖层/控件）独占输入，
 *     视频面是纯画布。v1 里 mpv 子窗同理：HTML 控制层盖在 Qt 子窗之上的区域
 *     本来就不给 mpv 吃鼠标。
 *
 * 坐标语义（2026-11-17 探针 /tmp/p3-probe/coord.js 实测，Xvfb+xfwm4）：
 *   BaseWindow.setBounds 的 x/y 是**屏幕绝对**坐标（setBounds({x:100,y:50})
 *   后 xwininfo 绝对位置=100,50，不是相对父窗）。所以每次摆放都要
 *   screen = 父窗内容区 origin + 前端上报的视口物理像素（见 main.js
 *   embedIntercept 的换算）。父窗 move/resize 由 main.js 挂事件重摆
 *   （JavdBviewed 同款；X11 子窗不会自动跟父，Electron 的 parent 只影响
 *   层级/生命周期，不重定位）。
 *   前端量的是视口×dpr 物理像素（PlayerView.vue:206-223），与 X 的 CSS 像素
 *   同刻度（本环境无 HiDPI 缩放）；有 WM 装饰时偏移正好被 contentBounds
 *   吃掉——比 Wails 版「裸 Win32 屏幕坐标自己减边框」干净。
 */
'use strict'

const { BaseWindow, screen } = require('electron')
const { execSync } = require('node:child_process')

/* 句柄读数：Linux XID 是 4 字节；Windows HWND 是 8 字节（JavdBviewed 同款
   双分支）。都转 Number（XID < 2^32；HWND 64 位下 < 2^48 远小于 2^53 安全）。 */
function readHandle(raw) {
  if (!raw || raw.length < 4) return 0
  if (raw.length >= 8) {
    const v = raw.readBigUInt64LE(0)
    return v > BigInt(Number.MAX_SAFE_INTEGER) ? 0 : Number(v)
  }
  return raw.readUInt32LE(0)
}

/* X11：某屏幕点最上层窗，向上走到 top-level（父=0 或 parent<0x100000 的
   低编号根/帧窗），判「是不是我的表面」。等价 Wails 版 RealWindowFromPoint
   判读表的最后一格（airspace 读数），只是读法从 Win32 换 X11。 */
function x11TopWindowAt(screenX, screenY) {
  try {
    const root = execSync('xdpyinfo', { encoding: 'utf8' })
      .match(/root window: 0x([0-9a-f]+)/i)?.[1]
    if (!root) return null
    let id =
      execSync(`xwininfo -root -tree`, { encoding: 'utf8' })
        .split('\n')
        .map((l) => l.trim())
        .find((l) => /^0x[0-9a-f]+.*at +\d+,\d+$/.test(l))?.match(/^0x([0-9a-f]+)/)?.[1] || root
    // 逐窗问绝对位置太重；直接用 xwininfo -id 按点查询不可用，
    // 改为：遍历 root tree 里所有带 at 坐标的窗，取包含 (screenX,screenY)
    // 且层级最深（tree 输出顺序=深度后序，最后出现的包含者最深）的一个。
    const lines = execSync('xwininfo -root -tree', { encoding: 'utf8' })
    let found = null
    for (const line of lines.split('\n')) {
      const m = line.trim().match(/^0x([0-9a-f]+)[^,]*?:.*?at +(\d+),(\d+)(?: \((?:hidden|obscured)\))?/i)
      if (!m) continue
      const [, wid, xStr, yStr, state] = m
      if (state && state !== '(has no name)' && /\(hidden\)/.test(state)) continue
      const x = +xStr
      const y = +yStr
      const wm = line.match(/\((\d+)x(\d+)\)/)
      const w = wm ? +wm[1] : 0
      const h = wm ? +wm[2] : 0
      if (w > 0 && h > 0 && screenX >= x && screenX < x + w && screenY >= y && screenY < y + h) {
        found = wid
      }
    }
    return found ? '0x' + found : '0x' + root
  } catch {
    return null
  }
}

class VideoSurface {
  constructor(parent) {
    this.parent = parent
    this.win = null
    this.xid = 0
    this.last = null // 最近一次有效几何（相对父内容区，物理像素）
    this.lastShow = false
    this.applyCount = 0
    this.hitBands = { top: 0, bottom: 0 }
    // 事件穿过去给父窗 WebView（forward=true：父窗还能收到 mousemove，
    // 悬停态不丢；JavdBviewed 同款参数）。
    this._onMoved = () => this.reposition()
    this._onResized = () => this.reposition()
    this._onMinimized = () => {
      if (this.win && !this.win.isDestroyed()) this.win.hide()
    }
    this._onRestored = () => {
      if (this.win && !this.win.isDestroyed() && this.lastShow) this.show()
    }
    parent.on('moved', this._onMoved)
    parent.on('resized', this._onResized)
    parent.on('minimize', this._onMinimized)
    parent.on('restore', this._onRestored)
  }

  ensure() {
    if (this.win && !this.win.isDestroyed()) return true
    const w = this.last?.w ?? 800
    const h = this.last?.h ?? 450
    this.win = new BaseWindow({
      parent: this.parent,
      width: Math.max(1, w),
      height: Math.max(1, h),
      x: 0,
      y: 0,
      frame: false,
      show: false,
      skipTaskbar: true,
      resizable: false,
      movable: false,
      backgroundColor: '#000000',
      useContentSize: true,
    })
    this.xid = readHandle(this.win.getNativeWindowHandle())
    if (this.xid === 0) {
      try {
        this.win.destroy()
      } catch {}
      this.win = null
      return false
    }
    try {
      this.win.setIgnoreMouseEvents(true, { forward: true })
    } catch (e) {
      // 个别平台/版本不支持时降级为不穿透（按钮会点不到视频区，但画面还在）。
      console.error('[videoSurface] setIgnoreMouseEvents 失败（降级不穿透）:', e.message)
    }
    return true
  }

  /* 父窗所在显示器的 DIP→物理缩放。前端 dpr 与它同源（页面缩放 100% 时
     devicePixelRatio==scaleFactor）；CI 100% 缩放恒 1，实机 125/150% 才走
     非 1 分支。取不到显示器的极端情况兜 1（=旧行为）。 */
  _scale(cb) {
    try {
      return screen.getDisplayMatching(cb).scaleFactor || 1
    } catch {
      return 1
    }
  }

  /* 摆到 (relX,relY,relW,relH)——相对父窗**内容区**的**物理像素**（前端
     getBoundingClientRect×dpr 原样上报，PlayerView.vue reportBounds；Wails
     时代直传 Win32 SetWindowPos，物理像素即正确语义）。
     Electron bounds API 全是 **DIP**：必须显式 ÷scaleFactor——否则缩放≠100%
     时视频面按 DIP 解释物理像素，向右下偏 (scale−1)× 并放大（run10 CI 恒
     100% 缩放 dpr=1 掩盖；用户实机 125/150% 即「有声无画」候选根因）。
     换算后钳进内容区（JavdBviewed surfaceBoundsInScreen 同款）。
     this.last 存**原始物理像素**：reposition 回灌走同一换算，debug/徽标
     字段与 Wails 时代 HostDebug 语义一致。 */
  place(relX, relY, relW, relH, show) {
    if (relW < 1 || relH < 1) return false // 无几何：不摆（可见性另行处理）
    if (!this.ensure()) return false
    const cb = this.parent.getContentBounds()
    const sc = this._scale(cb)
    const px = Math.round(cb.x + relX / sc)
    const py = Math.round(cb.y + relY / sc)
    // 钳：不超出父窗内容区右/下边缘（左/上不钳——前端量出来的本就在视口内）。
    const maxX = Math.round(cb.x + cb.width)
    const maxY = Math.round(cb.y + cb.height)
    const w = Math.min(Math.round(relW / sc), maxX - px)
    const h = Math.min(Math.round(relH / sc), maxY - py)
    if (w < 1 || h < 1) return false
    this.last = { x: relX, y: relY, w: relW, h: relH }
    this.lastShow = !!show
    this.win.setBounds({ x: px, y: py, width: w, height: h })
    if (show) this.show()
    else this.hide()
    this.applyCount += 1
    return true
  }

  reposition() {
    if (this.win && !this.win.isDestroyed() && this.last) {
      this.place(this.last.x, this.last.y, this.last.w, this.last.h, this.lastShow)
    }
  }

  show() {
    if (!this.win || this.win.isDestroyed()) return
    if (!this.win.isVisible()) this.win.showInactive()
    try {
      this.win.moveTop()
    } catch {}
  }

  hide() {
    if (!this.win || this.win.isDestroyed()) return
    if (this.win.isVisible()) this.win.hide()
  }

  destroy() {
    try {
      this.parent.off('moved', this._onMoved)
      this.parent.off('resized', this._onResized)
      this.parent.off('minimize', this._onMinimized)
      this.parent.off('restore', this._onRestored)
    } catch {}
    if (this.win && !this.win.isDestroyed()) {
      try {
        this.win.destroy()
      } catch {}
    }
    this.win = null
    this.xid = 0
  }

  /* 镜像 Go mpv.HostDebug 的 JSON 形状（徽标/复制诊断读同一套字段）。
     X11 侧 topAtCenter* 用 xwininfo 根树直读（等价 Windows 的
     WindowFromPoint 判读表最后一格）。 */
  debug() {
    let top = null
    let contains = false
    if (this.last && this.last.w > 1 && this.last.h > 1) {
      const cb = this.parent.getContentBounds()
      // last 是物理像素；中心点换算成 DIP 再查根树（与 place 同一 ÷scale 规则）
      const sc = this._scale(cb)
      const cx = Math.round(cb.x + (this.last.x + this.last.w / 2) / sc)
      const cy = Math.round(cb.y + (this.last.y + this.last.h / 2) / sc)
      const id = x11TopWindowAt(cx, cy)
      if (id) {
        top = id
        contains = id === '0x' + this.xid.toString(16) || id === '0x' + String(this.xid)
      }
    }
    const w = this.last?.w ?? 0
    const h = this.last?.h ?? 0
    return {
      supported: true,
      parentHwnd: '0x' + (this.parent.getNativeWindowHandle?.() ? readHandle(this.parent.getNativeWindowHandle()).toString(16) : 'browserwindow'),
      parentClass: 'electron',
      childHwnd: '0x' + (this.xid ? this.xid.toString(16) : '0'),
      childVisible: !!(this.win && !this.win.isDestroyed() && this.win.isVisible()),
      childLeft: this.last?.x ?? 0,
      childTop: this.last?.y ?? 0,
      childWidth: w,
      childHeight: h,
      applyCount: this.applyCount,
      lastInX: this.last?.x ?? 0,
      lastInY: this.last?.y ?? 0,
      lastInW: w,
      lastInH: h,
      lastOutVisible: this.lastShow,
      lastOutX: this.last?.x ?? 0,
      lastOutY: this.last?.y ?? 0,
      lastOutW: w,
      lastOutH: h,
      probeX: this.last ? Math.round(this.last.w / 2) : 0,
      probeY: this.last ? Math.round(this.last.h / 2) : 0,
      topAtCenterHwnd: top ? '0x' + top.replace(/^0x/, '') : '',
      topAtCenterClass: top ? (contains ? 'zgj-video-surface' : 'other') : 'no-probe',
      topIsChild: false,
      topContainsProbe: contains,
    }
  }
}

module.exports = { VideoSurface, readHandle }
