/* electron/main.js —— 真果鉴 V2 Electron 壳（第九十八轮 P2）。
 *
 * 职责边界（对照 research/electron-migration-plan.md）：
 *   - spawn 并看护 sidecar（zgj_sidecar 入口，stdio 行协议，P1 已提交 9315351）；
 *   - 把 sidecar 的 {id,result}/{id,error} 回执解进调用方 Promise，
 *     {event,data} 广播给所有窗口的 webContents；
 *   - preload 把同一形态架到渲染进程（window.go.main.App / window.runtime），
 *     前端 bridge.ts/api.ts/contract.ts 一字符不改。
 *
 * 错误语义（关键约定，勿改）：sidecar 的 error 帧原文是
 * 「message [code]」（Go 侧错误码尾约定）。本壳把它包成 Error(message 原文)
 * 让 ipcRenderer.invoke 拒绝——渲染端 bridge.ts 的剥码正则 /\[([a-z_]+)\]\s*$/
 * 要求 code 在**消息末尾**，所以这里**不许**加任何前缀/后缀包装。
 *
 * stdout 纪律：--selftest 模式下 stdout 只允许出现一行 SELFTEST_JSON（selftest.sh
 * 按前缀抓取）；其余诊断全部走 stderr。
 */
'use strict'

const { app, BrowserWindow, ipcMain } = require('electron')
const { spawn } = require('child_process')
const path = require('path')
const fs = require('fs')

const SELFTEST = process.argv.includes('--selftest')
const HERE = __dirname
const CLIENT = path.join(HERE, '..')
const DIST = path.join(CLIENT, 'frontend', 'dist')
const SIDECAR = process.env.ZHENGUO_SIDECAR || path.join(CLIENT, 'build', 'bin', 'sidecar-linux')
const SELFTEST_OUT = process.env.ZGJ_SELFTEST_OUT || '/tmp/zgj-electron-selftest'

function log(...a) {
  if (SELFTEST) console.error('[electron]', ...a)
  else console.log('[electron]', ...a)
}

/* ── sidecar 生命周期 ─────────────────────────────────────────────── */

let sidecar = null
let sidecarStdin = null
let shuttingDown = false
let restartCount = 0
const MAX_RESTARTS = 5
const pending = new Map() // id -> {resolve, reject}
let seq = 0

function startSidecar() {
  const dataDir = process.env.ZHENGUO_DATA
  const args = dataDir ? [dataDir] : []
  try {
    sidecar = spawn(SIDECAR, args, { stdio: ['pipe', 'pipe', 'pipe'] })
  } catch (e) {
    log('sidecar spawn 失败:', e.message)
    return
  }
  sidecarStdin = sidecar.stdin
  let buf = ''
  sidecar.stdout.on('data', (d) => {
    buf += d.toString('utf8')
    let i
    while ((i = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, i).trim()
      buf = buf.slice(i + 1)
      if (line) handleLine(line)
    }
  })
  sidecar.stderr.on('data', (d) => process.stderr.write('[sidecar] ' + d))
  sidecar.on('error', (e) => {
    // ENOENT 这类 spawn 失败**不会**发 exit 事件——pending 的调用若不清会
    // 永远悬着（首版 selftest 就这么挂死 240s 超时的）。路径错误是持续性
    // 的，重试无意义，不进入崩溃自拉起。
    log('sidecar spawn 失败（不重试）:', e.message)
    sidecar = null
    sidecarStdin = null
    for (const p of pending.values()) p.reject(new Error('sidecar spawn failed: ' + e.message))
    pending.clear()
    broadcast('zgj:sidecar-dead', { reason: e.message })
  })
  sidecar.on('exit', (code, sig) => {
    log('sidecar exit code=' + code + ' sig=' + sig)
    sidecar = null
    sidecarStdin = null
    for (const p of pending.values()) p.reject(new Error('sidecar exited (code=' + code + ')'))
    pending.clear()
    if (shuttingDown) return
    if (restartCount < MAX_RESTARTS) {
      restartCount++
      log('sidecar 崩溃自拉起 #' + restartCount)
      setTimeout(startSidecar, 300)
      broadcast('zgj:sidecar-restarted', { n: restartCount })
    } else {
      log('sidecar 重启超上限，宣告死亡')
      broadcast('zgj:sidecar-dead', {})
    }
  })
}

function handleLine(line) {
  let m
  try {
    m = JSON.parse(line)
  } catch {
    // 正常路径不会有（Go 侧 os.Stdout 已改道 stderr）；容错与 v1 同款。
    log('non-protocol line:', line.slice(0, 200))
    return
  }
  if (m.id != null && pending.has(String(m.id))) {
    const p = pending.get(String(m.id))
    pending.delete(String(m.id))
    if (m.error != null) p.reject(new Error(m.error))
    else p.resolve(m.result)
  } else if (m.event) {
    broadcast(m.event, m.data)
  }
}

function callSidecar(method, args, timeoutMs) {
  if (!sidecarStdin || sidecarStdin.destroyed) {
    return Promise.reject(new Error('sidecar not running'))
  }
  const id = String(++seq)
  const ms = timeoutMs || 30000
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      if (pending.delete(id)) reject(new Error('sidecar call ' + method + ' timed out after ' + ms + 'ms'))
    }, ms)
    pending.set(id, {
      resolve: (v) => {
        clearTimeout(timer)
        resolve(v)
      },
      reject: (e) => {
        clearTimeout(timer)
        reject(e)
      },
    })
    sidecarStdin.write(JSON.stringify({ id, method, args: args || [] }) + '\n')
  })
}

function broadcast(name, data) {
  for (const w of BrowserWindow.getAllWindows()) {
    if (!w.isDestroyed()) w.webContents.send('zgj:event', name, data)
  }
}

/* ── IPC：渲染进程 → sidecar ──────────────────────────────────────── */

ipcMain.handle('zgj:call', (_e, method, args) => callSidecar(method, args))

ipcMain.on('zgj:win', (_e, what) => {
  const w = BrowserWindow.getFocusedWindow() || BrowserWindow.getAllWindows()[0]
  if (!w) return
  if (what === 'fullscreen') w.setFullScreen(true)
  else if (what === 'unfullscreen') w.setFullScreen(false)
})

ipcMain.on('zgj:quit', async () => {
  shuttingDown = true
  try {
    await Promise.race([callSidecar('__shutdown'), new Promise((r) => setTimeout(r, 3000))])
  } catch {
    /* 收尾尽力而为 */
  }
  app.quit()
})

/* ── selftest：桥探针 + 8 页导航（截图交给 selftest.sh 判蓝行）────── */

async function runSelfTest(win) {
  const out = SELFTEST_OUT
  fs.mkdirSync(out, { recursive: true })
  const checks = []
  const add = (name, ok, detail) => checks.push({ name, ok: !!ok, detail: String(detail == null ? '' : detail) })

  // A. 桥探针：sidecar 经 preload 可达，三返回形状 + 错误帧 + IPC 账本
  try {
    const r = await win.webContents.executeJavaScript(
      `(async () => {
        const App = window.go && window.go.main && window.go.main.App
        const out = { bridge: !!App }
        if (!App) return out
        try { out.ping = await App.Ping() } catch (e) { out.pingErr = String((e && e.message) || e) }
        try { out.datapath = await App.DataPath() } catch (e) { out.datapathErr = String((e && e.message) || e) }
        try { out.buildinfo = await App.GetBuildInfo() } catch (e) { out.buildinfoErr = String((e && e.message) || e) }
        // 错误帧传输证明：调一个**真实存在**的方法、但故意传错参数类型
        // （PlayerHitBands 期望 float64，给字符串）→ sidecar 反射 call() 返回
        // 「参数 0 类型不符」错误帧 → 渲染端应 reject 且 message 原样带回
        // （证明 sidecar 错误帧 → 渲染 promise 拒绝的通路 + 消息未被包装改写，
        // 这是 bridge.ts 剥码正则 [\code] 生效的前提）。
        // 不用「不存在的方法」：普通对象上它直接 undefined，得到的是
        // "is not a function"（与真 Wails 一致），根本到不了 sidecar。
        try { await App.PlayerHitBands("x", "y"); out.errframe = 'no-reject' }
        catch (e) { out.errframe = String((e && e.message) || e) }
        out.ledger = window.__zgjIpcLedger || null
        return out
      })()`
    )
    add('bridge_present', r.bridge, 'window.go.main.App 存在')
    add('ping', r.ping === 'pong', 'Ping()=' + JSON.stringify(r.ping) + (r.pingErr ? ' err=' + r.pingErr : ''))
    add(
      'datapath',
      typeof r.datapath === 'string' && r.datapath.length > 0,
      (r.datapath || r.datapathErr || '').slice(0, 120)
    )
    add(
      'buildinfo',
      typeof r.buildinfo === 'string' && r.buildinfo.includes('{'),
      (r.buildinfo || r.buildinfoErr || '').slice(0, 120)
    )
    // 错误帧传输 + 解包证明：真实方法传错类型 → sidecar「参数 0 类型不符」
    // 错误帧到渲染端；且 preload 已剥掉 Electron 的 "Error invoking remote
    // method 'zgj:call': " 壳（断言它**不在**里 = 解包真发生了，渲染端拿到的
    // 就是 Go 原文，[code] 尾约定与 Wails 逐位一致）。
    const ef = r.errframe || ''
    add(
      'error_frame',
      /PlayerHitBands/.test(ef) && /类型不符/.test(ef) && !/Error invoking remote/.test(ef),
      ef.slice(0, 160)
    )
    const lg = r.ledger
    // 账本由**应用自身**的真实调用填充（桥探针直调 App.*，不过 callGoRaw，
    // 所以这里不看具体方法名——startup 的 UpdateStartup/ListSources 等
    // 必经 callGoRaw，账本非空就是「渲染端→sidecar 真实通路已走通」的证据）。
    const lgKeys = lg ? Object.keys(lg) : []
    const lgAny = lgKeys.some((k) => lg[k].sent >= 1)
    add('ipc_ledger', lgAny, 'entries=' + lgKeys.length + ' ' + JSON.stringify(lg || {}).slice(0, 140))
  } catch (e) {
    add('bridge_probe', false, String((e && e.message) || e))
  }

  // B. 8 页导航：逐路由切 hash、截图、读活动项标题与页面头
  const routes = [
    ['/discover', '找剧'],
    ['/library', '剧库推荐'],
    ['/search', '搜索'],
    ['/calendar', '新剧预约'],
    ['/reserve?tab=follow', '追更预约'],
    ['/history', '观看历史'],
    ['/downloads', '下载与合并'],
    ['/settings', '设置'],
  ]
  for (const [route, wantTitle] of routes) {
    const slug = route.replace(/[^a-z0-9]/gi, '_')
    await win.webContents.executeJavaScript(`location.hash = '${route}'`)
    await new Promise((r) => setTimeout(r, 1800))
    let info = { activeTitle: null, hasHead: false, appKids: 0 }
    try {
      info = await win.webContents.executeJavaScript(
        `(() => {
          const active = document.querySelector('.nav-item.active')
          const head = document.querySelector('.page-title') || document.querySelector('.cal-page') || document.querySelector('.set-body')
          const app = document.querySelector('#app')
          return { activeTitle: active ? active.getAttribute('title') : null, hasHead: !!head, appKids: app ? app.childElementCount : 0 }
        })()`
      )
    } catch (e) {
      info = { activeTitle: null, hasHead: false, appKids: 0, err: String((e && e.message) || e) }
    }
    const img = await win.webContents.capturePage()
    fs.writeFileSync(path.join(out, 'page_' + slug + '.png'), img.toPNG())
    add(
      'nav_' + slug,
      info.activeTitle === wantTitle && info.hasHead && info.appKids > 0,
      'active=' + JSON.stringify(info.activeTitle) + ' want=' + JSON.stringify(wantTitle) + ' head=' + info.hasHead + (info.err ? ' err=' + info.err : '')
    )
  }

  const ok = checks.every((c) => c.ok)
  fs.writeFileSync(path.join(out, 'selftest.json'), JSON.stringify({ ok, checks }, null, 1))
  // 唯一允许出现在 stdout 的一行（selftest.sh 按前缀抓）
  console.log('SELFTEST_JSON ' + JSON.stringify({ ok, checks }))
  setTimeout(() => app.exit(ok ? 0 : 1), 300)
}

/* ── 入口 ─────────────────────────────────────────────────────────── */

app.whenReady().then(async () => {
  if (SELFTEST) fs.mkdirSync(SELFTEST_OUT, { recursive: true })
  startSidecar()

  // 等 sidecar 就绪（Ping 有界重试 10s；单次探测 1s，防「重试循环×单调用
  // 30s 超时」叠出分钟级卡死）
  let ready = false
  for (let i = 0; i < 50 && !ready; i++) {
    try {
      await callSidecar('Ping', [], 1000)
      ready = true
    } catch {
      await new Promise((r) => setTimeout(r, 200))
    }
  }
  if (!ready) log('警告：sidecar 10s 内未就绪，继续起窗（桥探针会红）')

  // 绑定方法面：sidecar __methods（=Go 反射表）→ env → preload 建普通对象。
  // 必须在**创建 BrowserWindow 之前**写 env：preload 的 process.env 是
  // 渲染进程启动那一刻的快照，起窗后再写它读不到。
  let bindings = []
  try {
    const raw = await callSidecar('__methods', [], 5000)
    const arr = JSON.parse(raw)
    if (Array.isArray(arr)) bindings = arr.filter((x) => typeof x === 'string')
  } catch (e) {
    log('__methods 获取失败：', (e && e.message) || e)
  }
  process.env.ZGJ_BINDINGS_JSON = JSON.stringify(bindings)
  if (bindings.length === 0) log('警告：绑定方法列表为空，桥探针将红')
  log('绑定方法 ' + bindings.length + ' 个（sidecar 反射表 → preload）')

  const win = new BrowserWindow({
    width: 1280,
    height: 800,
    useContentSize: true,
    frame: !SELFTEST,
    title: '真果鉴',
    webPreferences: {
      preload: path.join(HERE, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
    },
  })
  // 诊断：renderer console 与 preload 加载错误都要能看见（必须在 loadFile
  // **之前**挂——preload 在文档加载时就跑，晚挂会错过它的死亡现场）。
  win.webContents.on('console-message', (_e, level, message) => {
    console.error('[renderer]', message)
  })
  win.webContents.on('preload-error', (_e, preloadPath, error) => {
    console.error('[preload-error]', preloadPath, String(error && error.stack ? error.stack : error))
  })
  await win.loadFile(path.join(DIST, 'index.html'))
  if (SELFTEST) {
    await new Promise((r) => setTimeout(r, 3000))
    runSelfTest(win).catch((e) => {
      console.log('SELFTEST_JSON ' + JSON.stringify({ ok: false, checks: [{ name: 'selftest_crash', ok: false, detail: String((e && e.message) || e) }] }))
      setTimeout(() => app.exit(1), 200)
    })
  }
})

app.on('before-quit', () => {
  shuttingDown = true
})

app.on('window-all-closed', () => {
  shuttingDown = true
  callSidecar('__shutdown').catch(() => {})
  setTimeout(() => app.quit(), 500)
})
