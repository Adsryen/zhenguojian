/* V-UPD mock 更新面：fake releases 含「比 mock 当前版本更近」项（verify V2-V5 依赖）；
   语义对齐 bindings_update.go / internal/updater Service。
   ⚠ 假数据本身（版本表、example.invalid 资产 URL、'~/.config/… (mock)' 之类）只存在于 mock；
   真实桥的 releases 来自 GitHub，设置持久化在 Go 侧 update-settings.json，
   这里的 settings 存 localStorage（zgj.update.settings）。 */
import type { NativeApi, UpdateAvailableEvent, UpdateCheckResult, UpdateDownloadState, UpdateRelease, UpdateSettings, UpdateStatus } from '../types'
import { delay } from './util'

const MOCK_UPDATE_CURRENT_VERSION = 'v0.0.0-dev'
const MOCK_UPDATE_SETTINGS_KEY = 'zgj.update.settings'
const MOCK_UPDATE_MOCK_SPEED_BPS = 1_900_000 // ≈1.9MB/150ms tick，21MB≈3.3s 到 100%

interface MockUpdateRelease {
  tag_name: string
  prerelease: boolean
  published_at: string
  body: string
  assets: { name: string; size: number; browser_download_url: string }[]
}

const MOCK_UPDATE_RELEASES: MockUpdateRelease[] = [
  {
    tag_name: 'v1.9.2',
    prerelease: false,
    published_at: '2026-04-01T02:00:00Z',
    body: 'v1 series release (ignored by updater: no v2 assets)',
    assets: [{ name: 'zhenGuoJian-windows-x64.zip', size: 15_000_000, browser_download_url: 'https://example.invalid/v1.9.2.zip' }]
  },
  {
    tag_name: 'v2.0.0',
    prerelease: false,
    published_at: '2026-08-15T02:00:00Z',
    body: 'First stable v2 release.\n\n- stable pipeline\n- player core',
    assets: [
      { name: 'zhenGuoJian-v2.exe', size: 21_000_000, browser_download_url: 'https://example.invalid/v2.0.0.exe' },
      { name: 'zhenGuoJian-v2-app.zip', size: 1_000_000, browser_download_url: 'https://example.invalid/v2.0.0-app.zip' },
      { name: 'build-info.json', size: 300, browser_download_url: 'https://example.invalid/v2.0.0-build-info.json' }
    ]
  },
  {
    tag_name: 'v2.0.1',
    prerelease: false,
    published_at: '2026-09-20T02:00:00Z',
    body: 'Bugfix release.\n\n- fix danmaku overlay\n- fix resume download',
    assets: [
      { name: 'zhenGuoJian-v2.exe', size: 21_000_100, browser_download_url: 'https://example.invalid/v2.0.1.exe' },
      { name: 'zhenGuoJian-v2-app.zip', size: 1_000_100, browser_download_url: 'https://example.invalid/v2.0.1-app.zip' }
    ]
  },
  {
    tag_name: 'v2.0.2-alpha.3',
    prerelease: true,
    published_at: '2026-10-05T02:00:00Z',
    body: 'Alpha preview (test channel only).\n\n- new suggest cache',
    assets: [
      { name: 'zhenGuoJian-v2.exe', size: 21_000_200, browser_download_url: 'https://example.invalid/v2.0.2a3.exe' },
      { name: 'zhenGuoJian-v2-app.zip', size: 1_000_200, browser_download_url: 'https://example.invalid/v2.0.2a3-app.zip' }
    ]
  }
]

function readUpdateSettings(): UpdateSettings {
  const base: UpdateSettings = { autocheck: true, prerelease: false, mirrorPrefix: '', ignoredTag: '', lastCheckAt: 0 }
  try {
    return { ...base, ...JSON.parse(localStorage.getItem(MOCK_UPDATE_SETTINGS_KEY) || '{}') }
  } catch {
    return base
  }
}

function persistUpdateSettings(s: UpdateSettings) {
  localStorage.setItem(MOCK_UPDATE_SETTINGS_KEY, JSON.stringify(s))
}

function mockV2Releases(prerelease: boolean): MockUpdateRelease[] {
  const v2 = MOCK_UPDATE_RELEASES.filter((r) =>
    r.assets.some((a) => a.name.toLowerCase() === 'zhenguojian-v2.exe' || a.name.toLowerCase() === 'zhenguojian-v2-app.zip')
  )
  return prerelease ? v2 : v2.filter((r) => !r.prerelease)
}

// 简化 semver-lite（与 Go internal/updater/semver.go 同语义：核心版本+预发布数字段）
function mockVerKey(tag: string): number[] | null {
  const m = /^[vV]?(\d+)\.(\d+)\.(\d+)(?:-([A-Za-z]+)(?:\.(\d+))?)?/.exec(tag)
  if (!m) return null
  return [Number(m[1]), Number(m[2]), Number(m[3]), m[4] ? 0 : 1, m[4] ? (m[5] ? Number(m[5]) : -1) : 0, m[4] ? m[4].charCodeAt(0) : 0]
}

function mockCmp(a: string, b: string): number {
  const ka = mockVerKey(a)
  const kb = mockVerKey(b)
  if (!ka || !kb) return 0
  for (let i = 0; i < ka.length; i++) {
    if (ka[i] !== kb[i]) return ka[i] - kb[i]
  }
  return 0
}

function mockPickLatest(prerelease: boolean): MockUpdateRelease | null {
  const pool = mockV2Releases(prerelease)
  if (!pool.length) return null
  return pool.reduce((best, r) => (mockCmp(r.tag_name, best.tag_name) > 0 ? r : best))
}

function mockReleaseByTag(tag: string): UpdateRelease | null {
  const r = MOCK_UPDATE_RELEASES.find((x) => x.tag_name === tag)
  if (!r) return null
  return {
    tag: r.tag_name,
    version: r.tag_name,
    prerelease: r.prerelease,
    publishedAt: r.published_at,
    body: r.body,
    assets: r.assets.map((a) => ({ name: a.name, url: a.browser_download_url, size: a.size }))
  }
}

// 本会话最近检查缓存（isZero=尚未检查过；对齐真实桥 lastCheck 语义）
let updateMockLastCheck: UpdateCheckResult & { isZero?: boolean } = {
  available: false,
  current: MOCK_UPDATE_CURRENT_VERSION,
  dev: true,
  channel: 'stable',
  checkedAt: 0,
  isZero: true
}
let updateMockStarted = false
let updateMockDownload: UpdateDownloadState & { timer?: number } = { tag: '', asset: '', dest: '', state: 'idle', total: 0, done: 0, pct: 0, speedBps: 0 }

/** mock 事件总线：无 Wails runtime，用 window CustomEvent 派发（App.vue 同款监听） */
function dispatchUpdateAvailable(payload: UpdateCheckResult | UpdateAvailableEvent) {
  const p = payload as UpdateCheckResult
  const rel = p.release
  const detail: UpdateAvailableEvent = rel
    ? { tag: rel.tag, version: rel.version, publishedAt: rel.publishedAt, body: rel.body, current: p.current, dev: p.dev, channel: p.channel, assets: rel.assets }
    : (payload as UpdateAvailableEvent)
  window.dispatchEvent(new CustomEvent<UpdateAvailableEvent>('zgj:update-available', { detail }))
}

export const mockUpdateApi = {
  // ── V-UPD 程序内更新（mock 面：语义对齐 bindings_update.go / internal/updater Service）──
  updateStartup: async () => {
    // 懒触发启动自动检查（真实桥=goroutine sleep 3s；mock 同延迟后按 settings 决定派发）
    if (!updateMockStarted) {
      updateMockStarted = true
      window.setTimeout(async () => {
        const s = readUpdateSettings()
        if (!s.autocheck) return
        const pick = mockPickLatest(!!s.prerelease)
        if (!pick || pick.tag_name === s.ignoredTag) return
        const rel = mockReleaseByTag(pick.tag_name)!
        const res: UpdateCheckResult = {
          available: true,
          current: MOCK_UPDATE_CURRENT_VERSION,
          dev: true,
          channel: s.prerelease ? 'prerelease' : 'stable',
          checkedAt: Math.floor(Date.now() / 1000),
          release: rel
        }
        updateMockLastCheck = res
        s.lastCheckAt = res.checkedAt
        persistUpdateSettings(s)
        dispatchUpdateAvailable(res)
      }, 3000)
    }
    return { ok: true }
  },
  updateCheck: async (force) => {
    const s = readUpdateSettings()
    const now = Math.floor(Date.now() / 1000)
    // 30s 冷却（参照 guoapp：非强制且距上次实检 <30s → 回缓存并标 cooldown）
    if (!force && !updateMockLastCheck.isZero && updateMockLastCheck.checkedAt > 0 && now - updateMockLastCheck.checkedAt < 30) {
      return { ...updateMockLastCheck, cooldown: true }
    }
    await delay(500)
    const res: UpdateCheckResult = {
      available: false,
      current: MOCK_UPDATE_CURRENT_VERSION,
      dev: true,
      channel: s.prerelease ? 'prerelease' : 'stable',
      checkedAt: Math.floor(Date.now() / 1000)
    }
    const pick = mockPickLatest(!!s.prerelease)
    if (!pick) {
      res.error = 'mock: 全部候选失败（示例数据缺失）'
    } else if (s.ignoredTag === pick.tag_name) {
      res.skippedTag = s.ignoredTag // 「跳过此版本」抑制一切检查（含手动，决策 f）
    } else {
      res.available = true
      res.release = mockReleaseByTag(pick.tag_name) ?? undefined
    }
    updateMockLastCheck = res
    s.lastCheckAt = res.checkedAt
    persistUpdateSettings(s)
    if (res.available && res.release) dispatchUpdateAvailable(res)
    return res
  },
  updateStatus: async () => {
    const s = readUpdateSettings()
    return {
      current: MOCK_UPDATE_CURRENT_VERSION,
      dev: true,
      channel: s.prerelease ? 'prerelease' : 'stable',
      checking: false,
      settings: s,
      lastCheck: updateMockLastCheck.isZero ? undefined : { ...updateMockLastCheck },
      download: { ...updateMockDownload, timer: undefined }
    } as UpdateStatus
  },
  updateDownload: async (tag, asset) => {
    const rel = mockReleaseByTag(tag)
    if (!rel) return { ok: false }
    const a = rel.assets.find((x) => x.name === (asset || 'zhenGuoJian-v2.exe')) ?? rel.assets[0]
    if (updateMockDownload.state === 'downloading' && updateMockDownload.timer) {
      window.clearInterval(updateMockDownload.timer)
      updateMockDownload = { ...updateMockDownload, timer: undefined }
    }
    if (updateMockDownload.asset === a.name && updateMockDownload.state === 'done') {
      return { ok: true } // 幂等：已完成直接成功（真实桥同语义）
    }
    const from = updateMockDownload.asset === a.name && updateMockDownload.state === 'canceled' ? updateMockDownload.done : 0
    updateMockDownload = { tag, asset: a.name, dest: `%LOCALAPPDATA%\\ZhenguoJian\\updates\\${tag}\\${a.name}`, state: 'downloading', total: a.size, done: from, pct: a.size > 0 ? from / a.size : 0, speedBps: 0 }
    const t0 = Date.now()
    const done0 = from
    updateMockDownload.timer = window.setInterval(() => {
      if (updateMockDownload.state !== 'downloading') return
      const step = Math.round((MOCK_UPDATE_MOCK_SPEED_BPS * 150) / 1000)
      updateMockDownload.done = Math.min(updateMockDownload.total, updateMockDownload.done + step)
      updateMockDownload.pct = updateMockDownload.total > 0 ? updateMockDownload.done / updateMockDownload.total : 1
      const secs = Math.max(0.15, (Date.now() - t0) / 1000)
      updateMockDownload.speedBps = Math.round((updateMockDownload.done - done0) / secs)
      if (updateMockDownload.done >= updateMockDownload.total) {
        updateMockDownload.state = 'done'
        updateMockDownload.speedBps = 0
        if (updateMockDownload.timer) window.clearInterval(updateMockDownload.timer)
        updateMockDownload.timer = undefined
      }
      const st = updateMockDownload
      window.dispatchEvent(new CustomEvent('zgj:update-progress', { detail: { tag: st.tag, asset: st.asset, total: st.total, done: st.done, pct: st.pct, speedBps: st.speedBps, state: st.state } }))
    }, 150)
    return { ok: true }
  },
  updateCancel: async () => {
    if (updateMockDownload.state !== 'downloading') return { ok: false }
    if (updateMockDownload.timer) window.clearInterval(updateMockDownload.timer)
    updateMockDownload = { ...updateMockDownload, state: 'canceled', speedBps: 0, timer: undefined }
    return { ok: true }
  },
  updateApply: async () => {
    if (updateMockDownload.state !== 'done') {
      return { kind: 'manual' as const, hint: 'mock: 请先完成下载' }
    }
    if (updateMockDownload.asset.toLowerCase().endsWith('.zip')) {
      return { kind: 'manual' as const, updateDir: updateMockDownload.dest, hint: 'mock: zip 资产请手动解压覆盖安装目录' }
    }
    return { kind: 'restart' as const, restarting: true, updateDir: updateMockDownload.dest }
  },
  updateSettingsGet: async () => readUpdateSettings(),
  updateSettingsSet: async (patch) => {
    const cur = readUpdateSettings()
    const next: UpdateSettings = {
      autocheck: patch.autocheck ?? cur.autocheck,
      prerelease: patch.prerelease ?? cur.prerelease,
      mirrorPrefix: patch.mirrorPrefix ?? cur.mirrorPrefix,
      ignoredTag: patch.ignoredTag ?? cur.ignoredTag,
      lastCheckAt: patch.lastCheckAt ?? cur.lastCheckAt
    }
    persistUpdateSettings(next)
    return next
  }
} satisfies Pick<
  NativeApi,
  'updateStartup' | 'updateCheck' | 'updateStatus' | 'updateDownload' | 'updateCancel' |
  'updateApply' | 'updateSettingsGet' | 'updateSettingsSet'
>
