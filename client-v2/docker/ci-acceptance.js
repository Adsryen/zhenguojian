#!/usr/bin/env node
/* ci-acceptance.js —— v3 容器镜像 CI 验收（零依赖：只用 node≥22 内置 fetch + WebSocket）。
 *
 * 在 CI runner 的 docker 容器（127.0.0.1:8080）上跑：
 *   ZGJ_WEB_USER / ZGJ_WEB_PASS / ZGJ_EXPECT_VERSION 环境变量注入。
 *
 * 判据分层（全部**不依赖外网**——github runner 到红果源站不可达不可靠）：
 *   1. HTTP 面：/health 200 {ok,mode:web}、错口令 401、对口令 200+cookie、
 *      /whoami 回显、静态 index 含 zgj、/file 无 cookie 401
 *   2. WS 面：带 cookie 连 /rpc、Ping=pong、__methods 含 REQUIRED 全集
 *      （89 活表里的 web 形态核心子集——漂移检测：新增绑定忘了登记 CI 即红）
 *   3. 只读真调用（容器内本地状态，零外网）：GetBuildInfo 版本=构建参数
 *      （-ldflags 注入实锤）、DataPath=/data/appdata、DownloadList、
 *      DownloadDirectory、UpdateStatus
 * 任一失败 exit 1，CI 红。
 */
const USER = process.env.ZGJ_WEB_USER
const PASS = process.env.ZGJ_WEB_PASS
const EXPECT_VERSION = process.env.ZGJ_EXPECT_VERSION
const BASE = process.env.ZGJ_BASE_URL || 'http://127.0.0.1:8080'
if (!USER || !PASS || !EXPECT_VERSION) {
  console.error('缺少 ZGJ_WEB_USER / ZGJ_WEB_PASS / ZGJ_EXPECT_VERSION')
  process.exit(2)
}

let cookie = ''
let failed = 0
function ok(cond, label, extra) {
  if (cond) console.log('  PASS  ' + label + (extra ? '  (' + extra + ')' : ''))
  else {
    console.log('  FAIL  ' + label + (extra ? '  (' + extra + ')' : ''))
    failed++
  }
}

// —— WS 客户端（协议与 stdio 面同信封：args=[]json.RawMessage 裸 JSON 片段）——
class Rpc {
  constructor() {
    this.idc = 0
    this.pend = new Map()
    this.ws = new WebSocket('ws://' + BASE.replace(/^http:/, '') + '/rpc', { headers: { Cookie: cookie } })
    // node 内置 WebSocket 的 headers 选项：标准构造第二参是对象（与 fetch 同语义的
    // 兼容层）；若该 node 版本不支持带 headers 构造（<22），退化为无 cookie 连接——
    // 但那样 /rpc 会 401，测试会红而不是静默，符合门禁语义。
    this.ws.onmessage = (ev) => {
      const m = JSON.parse(typeof ev.data === 'string' ? ev.data : new TextDecoder().decode(ev.data))
      if (m.id !== undefined && this.pend.has(m.id)) {
        const p = this.pend.get(m.id)
        this.pend.delete(m.id)
        m.error ? p.err(m.error) : p.res(m.result)
      }
    }
  }
  open() {
    return new Promise((res, rej) => {
      this.ws.onopen = res
      this.ws.onerror = () => rej(new Error('ws connect failed'))
      setTimeout(() => rej(new Error('ws open timeout')), 10000)
    })
  }
  call(method, ...frags) {
    return new Promise((res, rej) => {
      const id = ++this.idc
      this.pend.set(id, { res, err: rej })
      this.ws.send(JSON.stringify({ id, method, args: frags }))
      setTimeout(() => {
        if (this.pend.has(id)) {
          this.pend.delete(id)
          rej(new Error('timeout ' + method))
        }
      }, 20000)
    })
  }
  close() {
    try { this.ws.close() } catch { /* ignore */ }
  }
}

async function main() {
  console.log('== v3 容器 CI 验收 ' + BASE + ' ==')

  // 1. /health
  let r = await fetch(BASE + '/health')
  let j = await r.json().catch(() => ({}))
  ok(r.status === 200 && j.ok === true && j.mode === 'web', '/health 200 {ok,mode:web}', JSON.stringify(j))

  // 2. 错口令 401
  r = await fetch(BASE + '/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ user: USER, pass: 'wrong-pass-ci' })
  })
  ok(r.status === 401, '错口令 401', 'status=' + r.status)

  // 3. 对口令 200 + cookie
  r = await fetch(BASE + '/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ user: USER, pass: PASS })
  })
  const setc = r.headers.get('set-cookie') || ''
  const m = setc.match(/zgj_session=([^;]+)/)
  ok(r.status === 200 && !!m, '对口令 200+cookie', 'status=' + r.status)
  cookie = 'zgj_session=' + (m ? m[1] : '')

  // 4. /whoami
  r = await fetch(BASE + '/whoami', { headers: { Cookie: cookie } })
  j = await r.json().catch(() => ({}))
  ok(r.status === 200 && j.user === USER, '/whoami 回显 user', JSON.stringify(j))

  // 5. 静态 index
  r = await fetch(BASE + '/')
  const html = await r.text()
  ok(r.status === 200 && /<html|zgj|真果鉴/.test(html), '静态 index 200 且含应用标记', 'len=' + html.length)

  // 6. /file 无 cookie 401
  r = await fetch(BASE + '/file?p=' + encodeURIComponent('/data/appdata/x.mp4'))
  ok(r.status === 401, '/file 无 cookie 401', 'status=' + r.status)

  // 7-9. WS 面
  const rpc = new Rpc()
  await rpc.open()
  ok(true, 'WS /rpc 带 cookie 连上')

  const ping = await rpc.call('Ping')
  ok(ping === 'pong', 'Ping=pong', String(ping))

  const methods = JSON.parse(await rpc.call('__methods'))
  const REQUIRED = [
    // 数据面
    'Rankings', 'RankingBoards', 'Detail', 'Catalog', 'Recommendations',
    'BrowseFilters', 'ListSources', 'SetSourceEnabled', 'Cover',
    // 播放面（P1：web 形态靠这些 + /video/seg）
    'Resolve', 'PlayerState', 'DanmakuAll', 'DanmakuWindow',
    // 下载/合并面（P2：web 形态靠这些 + /file）
    'DownloadStart', 'DownloadPause', 'DownloadResume', 'DownloadCancel', 'DownloadList',
    'DownloadSettings', 'SaveDownloadSettings', 'DownloadDirectory', 'DownloadArchive',
    'MergeStart', 'MergeStartMulti', 'MergeList', 'MergeCancel',
    // 追更/历史/已看
    'Watchlist', 'HistoryList', 'SeenList', 'ToggleFollow', 'ToggleSeen', 'Reserve',
    // 设置面（P3 parity 已证）
    'GetResourceSettings', 'SaveResourceSettings', 'UpdateStatus', 'UpdateSettingsGet', 'UpdateSettingsSet',
    // 基建
    'Ping', 'DataPath', 'GetBuildInfo'
  ]
  const missing = REQUIRED.filter((n) => !methods.includes(n))
  ok(missing.length === 0, '__methods 含 REQUIRED 全集（' + REQUIRED.length + ' 个）',
    '表内 ' + methods.length + (missing.length ? ' 缺: ' + missing.join(',') : ''))

  // 只读真调用（容器本地状态，零外网）
  const bi = JSON.parse(await rpc.call('GetBuildInfo'))
  ok(bi.version === EXPECT_VERSION, 'GetBuildInfo 版本=构建参数（-ldflags 注入实锤）',
    'got=' + bi.version + ' want=' + EXPECT_VERSION)

  const dp = await rpc.call('DataPath')
  ok(dp === '/data/appdata', 'DataPath=/data/appdata', String(dp))

  const dl = JSON.parse(await rpc.call('DownloadList'))
  ok(Array.isArray(dl.jobs), 'DownloadList 返回 jobs 数组', 'jobs=' + dl.jobs.length)

  const dd = JSON.parse(await rpc.call('DownloadDirectory'))
  ok(dd.directory === '/data/appdata/downloads', 'DownloadDirectory 指向 /data/appdata/downloads', JSON.stringify(dd))

  const us = JSON.parse(await rpc.call('UpdateStatus'))
  ok(typeof us.channel === 'string', 'UpdateStatus 返回 channel', JSON.stringify(us).slice(0, 60))

  rpc.close()
  console.log(failed === 0 ? '\nCI_ACCEPTANCE_OK' : '\nCI_ACCEPTANCE_FAIL ' + failed + ' 项')
  process.exit(failed === 0 ? 0 : 1)
}

main().catch((e) => {
  console.error('CI_ACCEPTANCE_ERROR', e)
  process.exit(1)
})
