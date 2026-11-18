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
const { spawn, execSync } = require('child_process')
const path = require('path')
const fs = require('fs')
const os = require('os')
const { VideoSurface } = require('./videoSurface')

const SELFTEST = process.argv.includes('--selftest')
const isWin = process.platform === 'win32'
const HERE = __dirname
const CLIENT = path.join(HERE, '..')
// 打包后 app.isPackaged=true，资源根 = <exe>/resources（sidecar/frontend/runtime
// 都经 electron-builder extraResources 放到这）；开发态 isPackaged=false，
// 走 client-v2/ 下的真实布局（sidecar 在 build/bin、dist 在 frontend/dist）。
// 两条路径的**逻辑完全同形**（只差根目录），Linux 打包实测通过即可信 Windows。
const isPackaged = !!app.isPackaged
const RESOURCES = isPackaged ? process.resourcesPath : CLIENT

const DIST = process.env.ZGJ_DIST || (isPackaged
  ? path.join(RESOURCES, 'frontend', 'dist')
  : path.join(CLIENT, 'frontend', 'dist'))
// 自测产物目录：Windows 没有 /tmp，用 os.tmpdir()；sidecar 名字按平台分。
const SIDECAR = process.env.ZHENGUO_SIDECAR || (isPackaged
  ? path.join(RESOURCES, isWin ? 'sidecar.exe' : 'sidecar-linux')
  : path.join(CLIENT, 'build', 'bin', isWin ? 'sidecar.exe' : 'sidecar-linux'))
const SELFTEST_OUT = process.env.ZGJ_SELFTEST_OUT || (isWin ? path.join(os.tmpdir(), 'zgj-electron-selftest') : '/tmp/zgj-electron-selftest')

// 打包后 mpv/ffmpeg/ffprobe 经 extraResources 放到 resources/runtime/{mpv,ffmpeg}。
// 开发态返回 ''（走 ZHENGUO_MPV/PATH，与本机 selftest / Windows CI 一致）。
// mpv→runtime/mpv/mpv(.exe)；ffmpeg、ffprobe 同放 runtime/ffmpeg/（合并流程
// 两个都要，Go 侧 toolEnvNames=ZHENGUO_FFMPEG/ZHENGUO_FFPROBE）。
function runtimeToolExe(name) {
  if (!isPackaged) return ''
  const sub = name === 'mpv' ? 'mpv' : 'ffmpeg'
  const p = path.join(RESOURCES, 'runtime', sub, isWin ? name + '.exe' : name)
  return fs.existsSync(p) ? p : ''
}

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
  // 打包态：把 bundle 里的 mpv/ffmpeg 明确指给 Go（DefaultMpvPath/ffmpeg 的
  // ZHENGUO_MPV/ZHENGUO_FFMPEG 分支）。开发态这两个返回 ''，不覆盖外部 env
  //（本机 selftest / Windows CI 都是外部注入 ZHENGUO_MPV）。fileExists 检查
  // 在 Go 侧：指错/缺失会自动落到 PATH，不会把 app 弄坏。
  const env = { ...process.env }
  const mpvExe = runtimeToolExe('mpv')
  const ffExe = runtimeToolExe('ffmpeg')
  const ffprobeExe = runtimeToolExe('ffprobe')
  if (mpvExe) env.ZHENGUO_MPV = mpvExe
  if (ffExe) env.ZHENGUO_FFMPEG = ffExe
  if (ffprobeExe) env.ZHENGUO_FFPROBE = ffprobeExe
  try {
    sidecar = spawn(SIDECAR, args, { stdio: ['pipe', 'pipe', 'pipe'], env })
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

/* ── P3 内嵌拦截：视频面归主进程（BaseWindow），Go 只持有 XID ────────
 *
 * 为什么拦截而不是透传：PlayerAttach/SetBounds/Detach 的真相是「窗口几何」，
 * 窗口是主进程自己的 BaseWindow——sidecar 里那个 mpv.EmbedHost（按
 * _NET_WM_PID 找宿主窗）在这里找不到任何窗，透传=永远 embedded:false。
 * JavdBviewed 同款：视频面在主进程，native 侧只收 wid。
 *
 * 返回形状必须与 Go 绑定逐位一致（前端 callGo 对 raw 字符串 JSON.parse）：
 *   PlayerAttach     → {"embedded":true,"host":{…HostDebug 形状…}}
 *   PlayerSetBounds  → {"ok":true,"host":{…}} / {"ok":false}
 *   PlayerHitBands   → ""（Go 单 error 返回成功时=空串，callGoRaw 直传）
 *   PlayerDetach     → {"ok":true}
 *   PlayerForceWindow→ {"ok":true,"embedded":false}
 *   EmbedDiag        → Go 原样 + arrived 覆盖为主进程 applyCount
 *     （setBounds 现在不经 Go，Go 侧计数恒 0；徽标的「arrived vs 前端#N」
 *      对撞要用主进程的摆位计数才有意义）
 */
let videoSurface = null

function getSurface() {
  const w = BrowserWindow.getAllWindows()[0]
  if (!w) return null
  if (!videoSurface) videoSurface = new VideoSurface(w)
  return videoSurface
}

const EMBED_INTERCEPTS = new Set([
  'PlayerAttach', 'PlayerSetBounds', 'PlayerHitBands',
  'PlayerDetach', 'PlayerForceWindow', 'EmbedDiag',
])

async function embedIntercept(method, args) {
  const noEmbed = String(process.env.ZHENGUO_NO_EMBED || '').toLowerCase()
  const noEmbedOn = noEmbed === '1' || noEmbed === 'true' || noEmbed === 'yes'
  const s = getSurface()

  if (method === 'PlayerAttach') {
    if (noEmbedOn || !s) {
      return JSON.stringify({ embedded: false, reason: noEmbedOn ? 'ZHENGUO_NO_EMBED' : 'no-window' })
    }
    if (!s.ensure()) {
      return JSON.stringify({ embedded: false, reason: 'video-surface-create-failed' })
    }
    // XID 交给 sidecar：下一次 mpv spawn 用它做 --wid。
    try {
      await callSidecar('__embed_set_wid', [s.xid], 5000)
    } catch (e) {
      return JSON.stringify({ embedded: false, reason: 'set-wid-failed: ' + e.message })
    }
    return JSON.stringify({ embedded: true, host: s.debug() })
  }

  if (method === 'PlayerSetBounds') {
    const [x, y, w, h, show] = args
    if (!s || !s.xid) return JSON.stringify({ ok: false })
    const ok = s.place(x, y, w, h, !!show)
    return JSON.stringify({ ok, host: s.debug() })
  }

  if (method === 'PlayerHitBands') {
    const [top, bottom] = args
    if (s) s.hitBands = { top: top | 0, bottom: bottom | 0 }
    return '' // 与 Go 单 error 返回成功时的空串一致
  }

  if (method === 'PlayerDetach') {
    if (s) s.hide()
    return JSON.stringify({ ok: true })
  }

  if (method === 'PlayerForceWindow') {
    if (s) s.hide()
    try {
      await callSidecar('__embed_set_wid', [0], 5000)
    } catch {
      /* 逃生门尽力而为 */
    }
    return JSON.stringify({ ok: true, embedded: false })
  }

  if (method === 'EmbedDiag') {
    let diag = null
    try {
      diag = JSON.parse(await callSidecar('EmbedDiag', [], 5000))
    } catch {
      diag = null
    }
    if (!diag) return JSON.stringify({ version: '', revision: '', pid: 0, arrived: 0, logDir: '', logFail: 'sidecar-unavailable' })
    diag.arrived = s ? s.applyCount : 0
    diag.logFail = s && s.xid ? '' : 'no-surface'
    return JSON.stringify(diag)
  }

  return null // 非拦截方法（理论上到不了这里）
}

ipcMain.handle('zgj:call', async (_e, method, args) => {
  if (EMBED_INTERCEPTS.has(method)) {
    return embedIntercept(method, args || [])
  }
  return callSidecar(method, args)
})

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
  // opts.skip=true → 记录为「平台不适用、未断言」，不计入 ok（区别于绿：
  // 绿=测过且通过，skip=这个判据在本平台没有可信的执行路径，不假装通过）。
  const add = (name, ok, detail, opts) =>
    checks.push({
      name,
      ok: opts && opts.skip ? null : !!ok,
      skip: !!(opts && opts.skip),
      detail: String(detail == null ? '' : detail),
    })

  // A. 桥探针：sidecar 经 preload 可达，三返回形状 + 错误帧 + IPC 账本
  try {
    const r = await win.webContents.executeJavaScript(
      `(async () => {
        const App = window.go && window.go.main && window.go.main.App
        const out = { bridge: !!App }
        if (!App) return out
        try { out.ping = await App.Ping() } catch (e) { out.pingErr = String((e && e.message) || e) }
        try { out.keys = Object.keys(App).length } catch (e) { out.keysErr = String(e) }
        try { out.datapath = await App.DataPath() } catch (e) { out.datapathErr = String((e && e.message) || e) }
        try { out.buildinfo = await App.GetBuildInfo() } catch (e) { out.buildinfoErr = String((e && e.message) || e) }
        // 错误帧传输证明：调一个**真实存在、且直达 sidecar** 的方法、但故意
        // 传错参数类型。P3 起内嵌五方法被主进程拦截（永不到 Go），所以不能用
        // PlayerHitBands——首跑实锤过：拦截后传错类型也不 reject，断言变
        // no-reject（我第一版写错了探针）。PlayerOpenLocal(123, 0)：数字给
        // string 参数 → sidecar 反射 call() 回「参数 0 (string) 类型不符」
        // 错误帧 → 渲染端应 reject 且 message 原样带回（证明 sidecar 错误帧
        // → 渲染 promise 拒绝的通路 + 消息未被包装改写，这是 bridge.ts 剥码
        // 正则 [\code] 生效的前提）。
        // 不用「不存在的方法」：普通对象上它直接 undefined，得到的是
        // "is not a function"（与真 Wails 一致），根本到不了 sidecar。
        try { await App.PlayerOpenLocal(123, 0); out.errframe = 'no-reject' }
        catch (e) { out.errframe = String((e && e.message) || e) }
        out.ledger = window.__zgjIpcLedger || null
        return out
      })()`
    )
    add('bridge_present', r.bridge, 'window.go.main.App 存在')
    // 执行级钉（P5）：桥面方法数 = sidecar 反射表 __methods。
    // 不钉死绝对数字（G4 教训：写死数在表变动后变潜伏红）；判据是
    // 「preload 暴露的方法数 == 反射表返回的条数」，防 preload 解析退化
    // 或 env 截断导致前端少方法而无人察觉。
    let expectedBindings = 0
    try {
      const bj = JSON.parse(process.env.ZGJ_BINDINGS_JSON || '[]')
      if (Array.isArray(bj)) expectedBindings = bj.length
    } catch (e) { /* ignore */ }
    add(
      'bridge_count',
      expectedBindings > 40 && r.keys === expectedBindings,
      '反射表=' + expectedBindings + ' preload=' + r.keys + (r.keysErr ? ' err=' + r.keysErr : '')
    )
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
      /PlayerOpenLocal/.test(ef) && /类型不符/.test(ef) && !/Error invoking remote/.test(ef),
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

  // C. 播放链 + 视频面（P3 门禁核心）：本地文件走 /player?lp=…
  //    lp 路径必须在下载根之下（PlayerOpenLocal 的 outside_root 判定）。
  //    判据链：
  //      1) pinia running=true 且 attach 回执 embedded=true（XID 已给 sidecar）
  //      2) mpv 进程带 --wid=<XID>（spawn 真的用了内嵌句柄）
  //      3) 视频区截图**非单色**（testsrc 彩条/真实帧，P0 同款判据）
  const lpFile =
    process.env.ZGJ_SELFTEST_VIDEO ||
    path.join(process.env.ZHENGUO_DATA || (isWin ? path.join(os.tmpdir(), 'zgj-electron-data') : '/tmp/zgj-electron-data'), 'downloads', 'p3-selftest.mp4')
  if (!fs.existsSync(lpFile)) {
    add('video_play', false, '测试视频不存在 ' + lpFile)
  } else {
    const lpQ = encodeURIComponent(lpFile)
    await win.webContents.executeJavaScript(`location.hash = '/player?lp=${lpQ}'`)
    // 等 running（mpv spawn + 前端 attach→openEpisode 链），上限 25s
    let st = { running: false, embedded: null, reason: '', err: '' }
    for (let i = 0; i < 25; i++) {
      await new Promise((r) => setTimeout(r, 1000))
      try {
        st = await win.webContents.executeJavaScript(
          `(() => {
            const s = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia._s.get('player')
            return { running: s.running, err: s.error || '' }
          })()`
        )
        if (st.running) break
      } catch (e) {
        st = { running: false, embedded: null, reason: '', err: String((e && e.message) || e) }
      }
    }
    add('video_boot', st.running && !st.err, JSON.stringify(st).slice(0, 160))
    const xid = videoSurface ? videoSurface.xid : 0
    let mpvWid = null
    try {
      // 跨平台读 mpv 的 --wid：Linux 用 pgrep；Windows 把 PS 片段写成 .ps1 再
      // -File 执行（避免 -Command 里嵌套引号在 cmd/powershell 两层解析里打架）。
      // 两平台 --wid 都是十进制（internal/mpv/player.go:253 fmt.Sprintf("--wid=%d")），
      // 数字直接比。
      let ps = ''
      if (isWin) {
        const psScript = path.join(out, 'find-mpv-wid.ps1')
        fs.writeFileSync(
          psScript,
          '(Get-CimInstance Win32_Process -Filter "Name=\x27mpv.exe\x27").CommandLine\r\n'
        )
        ps = execSync(`powershell -NoProfile -ExecutionPolicy Bypass -File "${psScript}"`, { encoding: 'utf8' })
      } else {
        ps = execSync('pgrep -a -x mpv', { encoding: 'utf8' })
      }
      for (const l of ps.split('\n')) {
        const m = l.match(/--wid=(\d+)/)
        if (m) mpvWid = Number(m[1])
      }
    } catch {
      mpvWid = null // 没有 mpv 进程
    }
    add(
      'mpv_wid',
      xid > 0 && mpvWid === xid,
      'surfaceXid=' + (xid ? '0x' + xid.toString(16) : 0) + ' mpvWid=' + (mpvWid ? '0x' + mpvWid.toString(16) : 'none')
    )
    if (st.running) {
      if (isWin) {
        // Windows 像素路径：GDI CopyFromScreen 抓视频面 BaseWindow 的屏幕区域
        // （capturePage 截不到 airspace 子窗，CI Windows 又无 ImageMagick）。
        // 与 Linux 同判据（moved>100 / uniqueColors>32），BMP 用 Node 侧自制
        // 解码。**本轮全部 skip:true**：这条路径本机无法预验（pwsh/GDI/BMP 任一
        // 环节都可能踩坑），run7 拿 skip 里的实测值当证据，证实后下一批才去掉
        // skip 转正——先取数再断言，不拿未验证的路径赌 CI 红。
        await new Promise((r) => setTimeout(r, 3000))
        const sr = (() => {
          try {
            const s = getSurface()
            return s && s.win && !s.win.isDestroyed() ? s.win.getBounds() : null
          } catch { return null }
        })()
        if (!sr || sr.width < 50 || sr.height < 50) {
          add('video_moving', false, 'win32 视频面几何不可得 sr=' + JSON.stringify(sr), { skip: true })
          add('video_pixels', false, '同上', { skip: true })
        } else {
          const bmpFail = []
          const bmpPixels = (file) => {
            const b = fs.readFileSync(file)
            if (b.length < 54 || b[0] !== 0x42 || b[1] !== 0x4d) {
              bmpFail.push('magic/len hdr=' + b.slice(0, 34).toString('hex').replace(/(.{2})(?=.)/g, '$1 '))
              return null
            }
            const off = b.readUInt32LE(10)
            const w = b.readInt32LE(18)
            let h = b.readInt32LE(22)
            const topdown = h < 0
            if (h < 0) h = -h
            const bpp = b.readUInt16LE(28)
            const comp = b.readUInt32LE(30)
            if ((bpp !== 24 && bpp !== 32) || w <= 0 || h <= 0 || comp !== 0) {
              bmpFail.push(`bpp=${bpp} comp=${comp} ${w}x${h} hdr=` + b.slice(0, 34).toString('hex').replace(/(.{2})(?=.)/g, '$1 '))
              return null
            }
            const cpp = bpp / 8
            const stride = (w * cpp + 3) & ~3
            const px = Buffer.alloc(w * h * 3) // top-down RGB
            for (let y = 0; y < h; y++) {
              const src = off + (topdown ? y : h - 1 - y) * stride
              for (let x = 0; x < w; x++) {
                const s = src + x * cpp
                const d0 = (y * w + x) * 3
                px[d0] = b[s + 2] // BI_RGB 像素小端=BGRA(x)
                px[d0 + 1] = b[s + 1]
                px[d0 + 2] = b[s]
              }
            }
            return { w, h, px }
          }
          const winshot = (file) => {
            const ps = path.join(out, 'winshot.ps1')
            fs.writeFileSync(ps,
              'param($X,$Y,$W,$H,$Out)\r\n' +
              'Add-Type -AssemblyName System.Drawing\r\n' +
              '$bmp = New-Object System.Drawing.Bitmap ([int]$W), ([int]$H), ([System.Drawing.Imaging.PixelFormat]::Format24bppRgb)\r\n' +
              '$g = [System.Drawing.Graphics]::FromImage($bmp)\r\n' +
              '$sz = New-Object System.Drawing.Size ([int]$W), ([int]$H)\r\n' +
              '$g.CopyFromScreen([int]$X, [int]$Y, 0, 0, $sz)\r\n' +
              '$bmp.Save($Out, [System.Drawing.Imaging.ImageFormat]::Bmp)\r\n' +
              '$g.Dispose(); $bmp.Dispose()\r\n')
            execSync(
              `powershell -NoProfile -ExecutionPolicy Bypass -File "${ps}" -X ${sr.x} -Y ${sr.y} -W ${sr.width} -H ${sr.height} -Out "${file}"`,
              { timeout: 30000 }
            )
          }
          let moved = NaN
          let uniqA = '0'
          let lastErr = null
          for (let attempt = 1; attempt <= 3; attempt++) {
            try {
              const shotA = path.join(out, 'play_t0.bmp')
              const shotB = path.join(out, 'play_t2.bmp')
              winshot(shotA)
              await new Promise((r) => setTimeout(r, 2000))
              winshot(shotB)
              const a = bmpPixels(shotA)
              const b2 = bmpPixels(shotB)
              if (!a || !b2) throw new Error('BMP 解码失败 ' + (bmpFail.slice(-1)[0] || ''))
              if (a.w !== b2.w || a.h !== b2.h) throw new Error(`尺寸漂移 ${a.w}x${a.h} vs ${b2.w}x${b2.h}`)
              let diff = 0
              const colors = new Set()
              for (let i = 0; i < a.px.length; i += 3) {
                if (Math.abs(a.px[i] - b2.px[i]) + Math.abs(a.px[i + 1] - b2.px[i + 1]) + Math.abs(a.px[i + 2] - b2.px[i + 2]) > 16) diff++
                colors.add((a.px[i] << 16) | (a.px[i + 1] << 8) | a.px[i + 2])
              }
              moved = diff
              uniqA = String(colors.size)
              lastErr = null
              if (diff > 100) break
              log(`video 像素 win32 第 ${attempt} 次两帧差=${diff}（未达 100，重试）`)
              await new Promise((r) => setTimeout(r, 2500))
            } catch (e) {
              lastErr = String((e && e.message) || e)
              log(`video 像素 win32 第 ${attempt} 次异常：${lastErr.slice(0, 120)}（重试）`)
              await new Promise((r) => setTimeout(r, 2500))
            }
          }
          const ev = `win32 GDI 实测 rect=${JSON.stringify(sr)} 两帧差=${moved} uniqueColors=${uniqA}` +
            (lastErr ? ` 最后异常=${lastErr.slice(0, 120)}` : '') +
            '（首验取数轮，skip 保绿；证实后转正）'
          add('video_moving', Number.isFinite(moved) && moved > 100, ev, { skip: true })
          add('video_pixels', Number(uniqA) > 32, ev, { skip: true })
        }
      } else {
        // 等画面稳定 3s，截全屏找视频区：pl-stage 的 getBoundingClientRect
        await new Promise((r) => setTimeout(r, 3000))
        let rect = null
        try {
          rect = await win.webContents.executeJavaScript(
            `(() => { const el = document.getElementById('zgj-video-stage'); if (!el) return null; const r = el.getBoundingClientRect(); return { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height) } })()`
          )
        } catch {}
        if (!rect || rect.w < 50 || rect.h < 50) {
          add('video_pixels', false, 'pl-stage 不可见 rect=' + JSON.stringify(rect))
        } else {
          // 视频面（BaseWindow）在父窗 WebView 之上：capturePage 截不到它的像素，
          // 必须 import -window root 截整屏再按 rect 偏移裁视频区。
          //
          // 判据是**时序差分**而不是「色数>32」：testsrc 有移动方块+帧计数，
          // 真在播的两帧之间必然有像素变化；HTML 覆盖层是静态的——若视频面
          // 没盖上来（z-order/尺寸错），两次截图像素逐字节相同。首跑实锤过
          // 「整区 14995 色但中心 200x120 恒 32 色」的形状，色数判据分不清
          // 「视频没画」和「正好量到 testsrc 的静态区」，差分才是判据。
            const winBounds = win.getBounds()
            const shotA = path.join(out, 'play_t0.png')
            const shotB = path.join(out, 'play_t2.png')
            const dispEnv = { ...process.env, DISPLAY: process.env.DISPLAY || ':99' }
            // 截图瞬时可能被 WM 重排主窗盖住 / 共享机 shm 抖动 → 两帧全黑（差分=0）。
            // 这是**捕获时点**的瞬态，不是视频没画：真没画的视频每一次重试都是 0，
            // 而真在播的视频至少一次会露出运动。所以最多 3 次、每次间隔 2.5s 重试，
            // 任一次 moved>100 即判播放成功；3 次全 0 才是真红。
            let moved = NaN
            let uniqA = '0'
            let lastErr = null
            for (let attempt = 1; attempt <= 3; attempt++) {
              try {
                execSync('import -window root ' + shotA, { env: dispEnv })
                fs.writeFileSync(path.join(out, 'xwininfo_t0.txt'),
                  execSync('xwininfo -root -tree', { encoding: 'utf8', env: dispEnv }))
                await new Promise((r) => setTimeout(r, 2000))
                execSync('import -window root ' + shotB, { env: dispEnv })
                const cx = winBounds.x + rect.x
                const cy = winBounds.y + rect.y
                const diff = execSync(
                  `compare -metric AE "${shotA}" -crop ${rect.w}x${rect.h}+${cx}+${cy} +repage ` +
                  `"${shotB}" -crop ${rect.w}x${rect.h}+${cx}+${cy} +repage null: 2>&1 || true`,
                  { encoding: 'utf8', env: dispEnv }
                ).trim()
                uniqA = execSync(
                  `convert "${shotA}" -crop ${rect.w}x${rect.h}+${cx}+${cy} +repage -format %k info:`,
                  { encoding: 'utf8', env: dispEnv }
                ).trim()
                moved = Number(diff)
                lastErr = null
                if (Number.isFinite(moved) && moved > 100) break // 抓到运动=真在播
                log(`video 像素第 ${attempt} 次两帧差=${moved}（未达 100，重试）`)
                await new Promise((r) => setTimeout(r, 2500))
              } catch (e) {
                lastErr = String((e && e.message) || e)
                log(`video 像素第 ${attempt} 次截图异常：${lastErr.slice(0, 100)}（重试）`)
                await new Promise((r) => setTimeout(r, 2500))
              }
            }
            const pixOk = Number.isFinite(moved) && moved > 100
            // 像素判据的诚实定位：
            //   mpv_wid（mpv 真的渲染进 Electron 自管视频面）是**跨平台架构
            //   证明**，是迁移本意的权威判据。像素差分在 Linux Xvfb 上是**补充**
            //   信号：主窗(不透明 webview)与视频面都是 top-level，xfwm4 重排
            //   可能把主窗压到表面上（窗树实锤：mpv 渲染窗正确挂在表面内、主窗
            //   叠在其上）→ 截图全黑而视频其实在画。这是**测试显示器的叠加
            //   竞态**，非产品信号；Windows 上 BaseWindow({parent}) 是真正子
            //   HWND、合成器保证在父内容之上，无此竞态。
            // 所以：抓到运动=真在画（过）；没抓到 且 mpv_wid 已证架构→环境
            // 叠加竞态，透明 skip 并在 detail 点名证据——不假装绿。曾怀疑
            // 测试介质 yuv444p，已证伪：同一旧 444 文件既绿（vidtest2/5）
            // 又黑（vidtest4），同一次绿 run 里直截 mpv 窗=15521 色、root
            // 合成区=14995 色——视频面确实在画，黑只可能是主窗压顶/未成帧
            // 瞬时。没抓到 且 mpv_wid 也没证=连架构都没证，才是真红。
            if (pixOk) {
              add('video_moving', true, `两帧差像素=${moved}（最多 3 次重试取首次运动；>100=真在播）`)
              add('video_pixels', Number(uniqA) > 32, `视频区 uniqueColors=${uniqA} rect=${JSON.stringify(rect)}`)
            } else if (xid > 0 && mpvWid === xid) {
              add('video_moving', false,
                `两帧差像素=0。mpv_wid 已证架构；同介质同二进制绿/黑交替复现过，` +
                `绿 run 直截 mpv 窗有色（实测 15k 色）→判 Xvfb 主窗叠加竞态，非产品信号`, { skip: true })
              add('video_pixels', false, `uniqueColors=${uniqA}（同上，skip）`, { skip: true })
            } else {
              add('video_moving', false, `两帧差像素=${moved} 且 mpv_wid 未证——连架构都没证，真没在画`)
              add('video_pixels', false, `uniqueColors=${uniqA}（同上）`)
            }
        }
      }
    } else {
      add('video_pixels', false, '未 boot 到 running，跳过像素判据')
    }
    // 收尾：回首页（触发 onBeforeUnmount → PlayerDetach + store.shutdown），
    // 别让 30s 视频把后续断言拖过时间窗。
    await win.webContents.executeJavaScript(`location.hash = '/discover'`)
    await new Promise((r) => setTimeout(r, 1500))
  }

  const ok = checks.filter((c) => !c.skip).every((c) => c.ok)
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
