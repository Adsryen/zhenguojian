/* electron/preload.js —— 把 sidecar 协议架到渲染进程，形态与 Wails 逐位一致。
 *
 * 前端零改动的前提全在这里兑现（对照 bridge.ts / player.ts / downloads.ts 的读法）：
 *
 *   window.go.main.App  —— 一个**普通对象**：{ Method: (...args) => invoke(...) }。
 *                          方法名单来自 sidecar 的 __methods 协议方法（主进程在
 *                          起窗前取回、写进 process.env.ZGJ_BINDINGS_JSON）——
 *                          与 Go 反射表同源，与 Wails「App 恰好是这些绑定方法」
 *                          的形态一致。
 *                          为什么不用 Proxy（首版就是这么写的）：contextBridge
 *                          用结构化克隆过边界，**克隆不了 Proxy** —— 表现为
 *                          "An object could not be cloned"，preload 整体失败、
 *                          window.go 缺失（selftest 实锤）。普通函数对象可克隆，
 *                          且这正是 Wails 生成代码的形状。
 *                          用 Object.create(null)：无原型链，缺失方法就是
 *                          undefined（bridge.ts 判 typeof !== 'function' →
 *                          bridge_missing），不会撞到 Object.prototype 的同名函数。
 *                            - 成功 resolve 到 sidecar 的 result 字符串；
 *                            - 失败 reject 一个 Error(message=error 原文)，
 *                              message 尾部保留 ` [code]`，bridge.ts 的剥码正则
 *                              /\[([a-z_]+)\]\s*$/ 原样命中。
 *
 *   window.runtime       —— Wails runtime 的等价面。player.ts:441 / downloads.ts:48
 *                          读 `w.wails?.EventsOn ? w.wails : w.runtime`，我们只给
 *                          runtime（不给 wails，避免二义）。
 *                            EventsOn(name, cb)  订阅 sidecar 事件（zgj:event 过滤分发）；
 *                            EventsOff(name)     退订；
 *                            WindowFullscreen/WindowUnfullscreen  走 zgj:win；
 *                            Quit                走 zgj:quit（__shutdown + app.quit）。
 *
 * contextIsolation=true / nodeIntegration=false：渲染进程拿不到 Node，只有这里
 * 白名单暴露的两个名字。
 */
'use strict'

const { contextBridge, ipcRenderer } = require('electron')

/* 事件分发：zgj:event → (name, data) → 每个订阅了 name 的回调 */
const subs = new Map() // name -> Set<fn>
ipcRenderer.on('zgj:event', (_e, name, data) => {
  const set = subs.get(name)
  if (!set) return
  for (const cb of [...set]) {
    try {
      cb(data)
    } catch {
      // 回调抛错不许打断分发（一个订阅者异常不影响他人）。
    }
  }
})

const runtimeShim = {
  EventsOn(name, cb) {
    if (typeof name !== 'string' || typeof cb !== 'function') return
    if (!subs.has(name)) subs.set(name, new Set())
    subs.get(name).add(cb)
  },
  EventsOff(name) {
    if (typeof name !== 'string') return
    subs.delete(name)
  },
  // 窗口级：主进程持有 BrowserWindow，走 zgj:win 转发。
  WindowFullscreen() {
    ipcRenderer.send('zgj:win', 'fullscreen')
  },
  WindowUnfullscreen() {
    ipcRenderer.send('zgj:win', 'unfullscreen')
  },
  // 退出：主进程走 __shutdown 优雅收尾再 app.quit（对照 Go 的 runtime.Quit 语义）。
  Quit() {
    ipcRenderer.send('zgj:quit')
  },
}

// 绑定方法面：sidecar __methods 经主进程写入 env（起窗前已定，preload 每次
// 执行都读到同一份）。JSON 解析失败 → 空对象 → 全部调用 bridge_missing 降级
// （与「桥缺失」同一条 UI 路径，不炸）。
const appObj = Object.create(null)
try {
  const names = JSON.parse(process.env.ZGJ_BINDINGS_JSON || '[]')
  if (Array.isArray(names)) {
    for (const n of names) {
      if (typeof n === 'string' && n.length > 0) {
        appObj[n] = (...args) =>
          ipcRenderer.invoke('zgj:call', n, args).then(
            (v) => v,
            // Electron 把主进程 handle 的 Promise 拒绝包成
            // "Error invoking remote method 'zgj:call': Error: <原文>"。
            // 剥掉这层壳，让渲染端拿到的 message = sidecar 错误原文（= Go
            // 错误字符串，尾部 ` [code]` 约定与 Wails 形态逐位一致）。
            (e) => {
              const m = e && typeof e.message === 'string' ? e.message : String(e)
              const clean = m
                .replace("Error invoking remote method 'zgj:call': ", '')
                .replace(/^Error: /, '')
              throw new Error(clean)
            }
          )
      }
    }
  }
} catch {
  /* 空对象，前端走降级 */
}

contextBridge.exposeInMainWorld('go', { main: { App: appObj } })
contextBridge.exposeInMainWorld('runtime', runtimeShim)
