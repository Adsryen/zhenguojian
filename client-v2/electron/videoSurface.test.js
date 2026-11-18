// videoSurface DPI 换算单测 —— 钉「前端物理像素 → place()÷scaleFactor →
// setBounds DIP」这条链。背景：用户实机（缩放≠100%）「有声无画」，而 CI
// runner 恒 100% 缩放（dpr=1、scaleFactor=1）物理像素与 DIP 数值相同，
// 单位混用 bug 在 CI 上不可见。force-device-scale-factor 不能模拟实机
// （它只改 Chromium dpr 不改 Electron scaleFactor，两者失配制造假红），
// 所以换算正确性在这里用 mock scaleFactor 钉死。
// 运行：node videoSurface.test.js（无测试框架依赖，electron 模块被拦截）
'use strict'
const assert = require('node:assert')
const Module = require('node:module')

let SCALE = 1 // 每个用例前设置
const lastSetBounds = []

class FakeBaseWindow {
  constructor(opts) {
    this.opts = opts
    this.visible = false
    this.destroyed = false
    lastSetBounds.push(opts)
  }
  getNativeWindowHandle() {
    const b = Buffer.alloc(8)
    b.writeBigUInt64LE(0x1234n)
    return b
  }
  setBounds(r) { lastSetBounds.push(r) }
  showInactive() { this.visible = true }
  hide() { this.visible = false }
  isVisible() { return this.visible }
  isDestroyed() { return this.destroyed }
  destroy() { this.destroyed = true }
  moveTop() {}
  setIgnoreMouseEvents() {}
}

const fakeElectron = {
  BaseWindow: FakeBaseWindow,
  screen: { getDisplayMatching: () => ({ scaleFactor: SCALE }) },
}

const origLoad = Module._load
Module._load = function (request, ...rest) {
  if (request === 'electron') return fakeElectron
  return origLoad.call(this, request, ...rest)
}
const { VideoSurface } = require('./videoSurface')
Module._load = origLoad

function makeParent(contentBounds) {
  return {
    getContentBounds: () => contentBounds,
    getBounds: () => contentBounds,
    getNativeWindowHandle: () => Buffer.alloc(8),
    on() {}, off() {},
  }
}

function run(name, fn) {
  lastSetBounds.length = 0
  try { fn(); console.log('PASS', name) } catch (e) { console.log('FAIL', name, '-', e.message); process.exitCode = 1 }
}

// 1) 100% 缩放（CI 旧路径）：物理像素==DIP，行为与修复前逐值一致
run('scale=1 直传不回归', () => {
  SCALE = 1
  const vs = new VideoSurface(makeParent({ x: 0, y: 0, width: 1280, height: 720 }))
  assert.strictEqual(vs.place(193, 140, 767, 520, true), true)
  const r = lastSetBounds[lastSetBounds.length - 1]
  assert.deepStrictEqual({ x: r.x, y: r.y, width: r.width, height: r.height },
    { x: 193, y: 140, width: 767, height: 520 })
})

// 2) 150% 缩放实机场景：前端报物理像素（CSS×1.5），主进程必须 ÷1.5 回 DIP
run('scale=1.5 物理→DIP 换算', () => {
  SCALE = 1.5
  const vs = new VideoSurface(makeParent({ x: 0, y: 0, width: 1280, height: 720 }))
  // 前端 .pl-stage 在 CSS(193,140) 尺寸 767x520 → 上报物理 (290,210,1150,780)
  assert.strictEqual(vs.place(290, 210, 1150, 780, true), true)
  const r = lastSetBounds[lastSetBounds.length - 1]
  assert.deepStrictEqual({ x: r.x, y: r.y, width: r.width, height: r.height },
    { x: 193, y: 140, width: 767, height: 520 })
})

// 3) 父窗内容区原点非零（DWM 边框/非最大化）：origin 用 DIP，偏移量换算
run('scale=1.5 含父窗原点偏移', () => {
  SCALE = 1.5
  const vs = new VideoSurface(makeParent({ x: 100, y: 60, width: 1280, height: 720 }))
  assert.strictEqual(vs.place(300, 150, 600, 300, true), true)
  const r = lastSetBounds[lastSetBounds.length - 1]
  assert.deepStrictEqual({ x: r.x, y: r.y, width: r.width, height: r.height },
    { x: 300, y: 160, width: 400, height: 200 }) // 100+300/1.5=300, 60+150/1.5=160
})

// 4) 钳制：超宽物理像素换算后仍钳进内容区右/下缘（DIP 域）
run('scale=1.5 钳进内容区', () => {
  SCALE = 1.5
  const vs = new VideoSurface(makeParent({ x: 0, y: 0, width: 1280, height: 720 }))
  assert.strictEqual(vs.place(0, 0, 99999, 99999, true), true)
  const r = lastSetBounds[lastSetBounds.length - 1]
  assert.strictEqual(r.width, 1280)
  assert.strictEqual(r.height, 720)
})

// 5) reposition 走同一换算：last 存原始物理像素，改缩放后重摆不漂移
run('reposition 重换算一致', () => {
  SCALE = 1.5
  const vs = new VideoSurface(makeParent({ x: 0, y: 0, width: 1280, height: 720 }))
  assert.strictEqual(vs.place(290, 210, 1150, 780, true), true)
  lastSetBounds.length = 0
  vs.reposition()
  const r = lastSetBounds[lastSetBounds.length - 1]
  assert.deepStrictEqual({ x: r.x, y: r.y, width: r.width, height: r.height },
    { x: 193, y: 140, width: 767, height: 520 })
})

// 6) debug() 中心点换算：last 物理像素 ÷scale 后与 DIP 原点相加（X11 探针
//    依赖；scale=1.5 时中心点不得按物理像素直加）
run('debug 中心点换算', () => {
  SCALE = 1.5
  const vs = new VideoSurface(makeParent({ x: 0, y: 0, width: 1280, height: 720 }))
  vs.last = { x: 300, y: 300, w: 600, h: 300 } // 物理像素
  const d = vs.debug()
  // 中心物理 (600,450) → DIP (400,300)。xwininfo 在 node 环境不可用，
  // topAtCenterHwnd 为空；这里只钉换算不抛错 + 字段形状在。
  assert.strictEqual(d.supported, true)
  assert.strictEqual(typeof d.childVisible, 'boolean')
})

console.log(process.exitCode ? '== 有失败 ==' : '== 全部通过 ==')
