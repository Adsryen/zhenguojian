/* wsBridge.ts —— v3 容器版的 Go 桥传输层（WebSocket RPC）。
   桌面形态用 window.go（Wails），容器形态用本文件把同样的方法面走 /rpc WS。
   协议与 sidecar_main.go 的 stdio 信封**逐字段同构**：
     上行 {"id":n,"method":m,"args":[...]}
     下行 {"id":n,"result":"<string>"} / {"id":n,"error":"<msg [code]>"}
          {"event":name,"data":...}
   所以 callGoRaw 拿到的 result 仍是「Go 侧 marshalJSON 的字符串」，与 window.go
   完全一致——realApi 80 方法零改写。

   启用条件（webBridgeEnabled）：http(s) + 无 window.go + 非 VITE_MOCK。
   桌面 loadFile(file:) 与 vite mock dev 天然不满足，行为零变化。 */


/** 是否走 WS 桥（同步判断，facade 在 import 时就需要它选面）。 */
export function webBridgeEnabled(): boolean {
  if (typeof window === 'undefined') return false
  if ((window as any).go?.main?.App) return false // 桌面桥优先
  if (import.meta.env.VITE_MOCK === '1') return false
  if (typeof location === 'undefined') return false
  return /^https?:$/.test(location.protocol)
}

let ws: WebSocket | null = null
let connected = false
let connectP: Promise<boolean> | null = null
let nextId = 1

type Pending = { resolve: (v: string) => void; reject: (e: Error) => void }
const pending = new Map<number, Pending>()
const eventListeners = new Map<string, Set<(payload: any) => void>>()

/** 注册事件监听，返回解绑函数（对齐 window.runtime.EventsOn 的签名）。 */
export function onWsEvent(name: string, cb: (payload: any) => void): () => void {
  let set = eventListeners.get(name)
  if (!set) {
    set = new Set()
    eventListeners.set(name, set)
  }
  set.add(cb)
  return () => set!.delete(cb)
}

function wsUrl(): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/rpc`
}

function openSocket(): Promise<boolean> {
  return new Promise((resolve) => {
    let settled = false
    const done = (ok: boolean) => {
      if (settled) return
      settled = true
      connected = ok
      resolve(ok)
    }
    const sock = new WebSocket(wsUrl())
    sock.onopen = () => {
      ws = sock
      // 握手探测：__methods 能回来即桥活（方法表同源，兼自检）。
      sock.send(JSON.stringify({ id: nextId++, method: '__methods', args: [] }))
      done(true)
    }
    sock.onerror = () => done(false)
    sock.onclose = () => {
      // 断线：清空在飞请求（让它们以断连报错而不是悬挂），下次调用重连。
      connected = false
      connectP = null
      for (const [, p] of pending) p.reject(new Error('WS 连接已断开'))
      pending.clear()
      if (sock === ws) ws = null
    }
    sock.onmessage = (ev) => handleMessage(String(ev.data))
    // 握手超时兜底（onopen 不触发时靠这个收口）。
    setTimeout(() => {
      if (!settled) {
        try { sock.close() } catch { /* noop */ }
        done(false)
      }
    }, 4000)
  })
}

function handleMessage(raw: string) {
  let m: any
  try {
    m = JSON.parse(raw)
  } catch {
    return
  }
  // 事件下行（无 id）。双通道对齐桌面行为：
  //  ① EventsOn 回调（App.vue 探测链）；② window CustomEvent `zgj:<name>`
  //     （App.vue 还挂了 window.addEventListener('zgj:update-available')）。
  if (typeof m.event === 'string') {
    const set = eventListeners.get(m.event)
    if (set) for (const cb of [...set]) {
      try { cb(m.data) } catch (e) { console.warn('[ws] 事件回调异常', m.event, e) }
    }
    try {
      window.dispatchEvent(new CustomEvent(`zgj:${m.event}`, { detail: m.data }))
    } catch { /* 无 window（测试缝）时忽略 */ }
    return
  }
  if (typeof m.id !== 'number') return
  const p = pending.get(m.id)
  if (!p) return
  pending.delete(m.id)
  if (typeof m.error === 'string') {
    // 与 window.go 一致：错误是带可选 [code] 尾的字符串，callGoRawInner 统一剥码。
    p.reject(new Error(m.error))
  } else {
    const result = typeof m.result === 'string' ? m.result : JSON.stringify(m.result ?? '')
    p.resolve(result)
  }
}

/** 连上（幂等）并返回是否可用。超时保护，失败不抛（facade 会回落到 bridge_missing）。 */
export async function whenWsReady(timeoutMs = 4000): Promise<boolean> {
  if (!webBridgeEnabled()) return false
  if (connected) return true
  if (!connectP) connectP = openSocket()
  try {
    return await Promise.race([
      connectP,
      new Promise<boolean>((r) => setTimeout(() => r(false), timeoutMs)),
    ])
  } finally {
    // 即便超时也保留 connectP：后台连成后 connected 会被 onopen 置位，下次调用即走 WS。
  }
}

/** 发一个方法调用，返回 Go 侧原始字符串（与 callGoRaw 期望一致）。 */
export function wsCall(method: string, ...args: any[]): Promise<string> {
  return new Promise<string>((resolve, reject) => {
    if (!ws || !connected) {
      reject(new Error('WS 桥未就绪'))
      return
    }
    const id = nextId++
    pending.set(id, { resolve, reject })
    try {
      ws.send(JSON.stringify({ id, method, args }))
    } catch (e: any) {
      pending.delete(id)
      reject(new Error(String(e?.message ?? e)))
    }
    // 单调用 120s 兜底（大下载/合并可能长，但超过这个基本是挂了）。
    setTimeout(() => {
      if (pending.has(id)) {
        pending.delete(id)
        reject(new Error(`${method} 超时(120s)`))
      }
    }, 120000)
  })
}

/* 登录态探测：/whoami 200=已登录。模块级缓存——router 守卫每次导航都问，
   不能每次都发请求；登出后手动清（logout 会整页跳转，缓存自然作废）。 */
let authState: Promise<boolean> | null = null
export function checkAuth(force = false): Promise<boolean> {
  if (!webBridgeEnabled()) return Promise.resolve(true) // 桌面/mock 无鉴权
  if (authState && !force) return authState
  authState = (async () => {
    try {
      const r = await fetch('/whoami', { credentials: 'same-origin' })
      return r.status === 200
    } catch {
      return false
    }
  })()
  return authState
}

/** 提交登录（表单页用）。成功=cookie 已落，调用方整页刷新进主应用。 */
export async function login(user: string, pass: string): Promise<{ ok: boolean; error?: string }> {
  try {
    const r = await fetch('/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ user, pass }),
    })
    if (r.status === 200) return { ok: true }
    let msg = `HTTP ${r.status}`
    try {
      const b = await r.json()
      if (b?.error) msg = b.error
    } catch { /* body 非 JSON */ }
    return { ok: false, error: msg }
  } catch (e: any) {
    return { ok: false, error: String(e?.message ?? e) }
  }
}

/** 登出（清 cookie + 整页回登录页）。 */
export async function logout(): Promise<void> {
  try {
    await fetch('/logout', { method: 'POST', credentials: 'same-origin' })
  } catch { /* 忽略网络错误，反正要跳走 */ }
  window.location.hash = '#/login'
  window.location.reload()
}

/** 启动时在 ready 后装 window.runtime.EventsOn  shim，App.vue 的探测链零改动。 */
export async function installRuntimeShim(): Promise<void> {
  if (typeof window === 'undefined') return
  const ok = await whenWsReady()
  if (!ok) return
  const w = window as any
  w.runtime = w.runtime ?? {}
  w.runtime.EventsOn = (name: string, cb: (payload: any) => void) => onWsEvent(name, cb)
}
