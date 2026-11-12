/* 绑定适配层：组件只依赖本模块，永不直接触碰 window.go。
   - 真实模式：window.go.main.App.<Method>(...) 返回 JSON 字符串（Go 侧 (string, error)）
   - VITE_MOCK=1：fixtures 双实现，可在纯浏览器里验证 UI */
import { ApiError } from './types'
import { t } from './i18n' /* R9c-2：mock 面错误串会直接进用户可见 toast，所以走字典 */
import type {
  ArchiveScanResult,
  BrowseFiltersResult,
  BuildInfo,
  CalendarPayload,
  CatalogParams,
  CatalogResult,
  Chapter,
  CreatorProfileResult,
  CreatorWorksSnapshot,
  CreatorsResult,
  DanmakuWindowResult,
  DetailResult,
  DownloadDirectoryInfo,
  DownloadSettingsSnapshot,
  DownloadSnapshot,
  Drama,
  HistoryRow,
  NativeApi,
  PlayerSnapshot,
  RankingBoard,
  RankingsResult,
  ResolvePlan,
  ResourceSettingsSnapshot,
  SoraniCaptcha,
  SoraniStatus,
  SourceListItem,
  UpdateApplyResult,
  UpdateAvailableEvent,
  UpdateCheckResult,
  UpdateDownloadState,
  UpdateRelease,
  UpdateSettings,
  UpdateStatus,
  WatchRow,
} from './types'
import { MOCK_SOURCES, mockArchiveScan, mockHongguoFilterGroups, mockSourceCategoryGroups, mockCatalog, mockCreatorProfile, mockCreatorWorks, mockDanmakuAll, mockDetail, mockDramaCreators, mockDownloadControl, mockDownloadControlAll, mockDownloadDirectory, mockDownloadList, mockDownloadRemoveBatch, mockDownloadSettings, mockDownloadStart, mockMergeCancel, mockMergeStart, mockResolvePlan, mockSaveDownloadSettings, mockCalendarPage } from './fixtures'
import { mockLibraryRecommendations, mockLibraryPageReset } from './fixtures'
import { mockRankingBoards, mockRankings } from './fixtures'
import { mockSuggestions } from './fixtures'
import {
  clearHistory as clearHistoryLocal,
  deleteHistoryEntry,
  HISTORY_KEY,
  historyKey,
  isFollowed,
  markNextSeason as localMarkNextSeason,
  readFollows,
  readHistory,
  readReservations,
  readWatched,
  toggleFollow,
  toggleReservation,
  rememberReservation,
  readReservationDramas,
  forgetReservation,
  toggleWatched,
  WATCHED_KEY
} from './local'

const isMock = import.meta.env.VITE_MOCK === '1'

function wailsApp(): Record<string, (...args: any[]) => Promise<string>> {
  const app = window.go?.main?.App
  if (!app) {
    throw new ApiError('未检测到 Wails 原生桥（浏览器直开需 VITE_MOCK=1 或 wails dev）', 'bridge_missing')
  }
  return app
}

/** Go 侧 bound 方法调用：返回原始字符串（Ping/DataPath 这类非 JSON 值直接用） */
async function callGoRaw(method: string, ...args: any[]): Promise<string> {
  const fn = wailsApp()[method]
  if (typeof fn !== 'function') {
    throw new ApiError(`原生方法 ${method} 不存在`, 'bridge_missing')
  }
  try {
    const raw = await fn(...args)
    return typeof raw === 'string' ? raw : JSON.stringify(raw)
  } catch (err: any) {
    const message = typeof err === 'string' ? err : err?.message ?? String(err)
    const m = message.match(/\[([a-z_]+)\]\s*$/)
    throw new ApiError(m ? message.slice(0, m.index) : message, m ? m[1] : '')
  }
}

/** 同上 + JSON 解析（Go 侧 marshalJSON 的对象/数组返回） */
async function callGo<T>(method: string, ...args: any[]): Promise<T> {
  const raw = await callGoRaw(method, ...args)
  if (raw === '' || raw === undefined) return undefined as unknown as T
  return JSON.parse(raw) as T
}

const delay = (ms: number) => new Promise((r) => setTimeout(r, ms))

// ── V-UPD mock 更新面（fake releases 含「比 mock 当前版本更新」项；verify V2-V5 依赖）──
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

/* 真实桥探测：exe 内 window.go 必在；纯浏览器/WebView2 异常时自动降级 local 面，
   避免整页 bridge_missing 死锁（降级会显示顶部提示横幅）。 */
const hasBridge =
  typeof window !== 'undefined' &&
  !!((window as any).go?.main?.App && (window as any).runtime?.EventsOn) ||
  !!((window as any).go?.main?.App)

const realApi: NativeApi = {
  // Ping/DataPath 是裸字符串返回（非 JSON），用 raw
  ping: () => callGoRaw('Ping'),
  dataPath: () => callGoRaw('DataPath'),
  // ListSources 返回 marshalJSON([]SourceListItem) → 数组
  listSources: () => callGo<SourceListItem[]>('ListSources'),
  setSourceEnabled: async (slug, enabled) => {
    await callGo('SetSourceEnabled', slug, enabled)
  },
  // Catalog 绑定是位置参数 (source, query, category, page, lane, force)
  catalog: (p: CatalogParams) =>
    callGo<CatalogResult>(
      'Catalog',
      p.source,
      p.query ?? '',
      p.category ?? '',
      p.page ?? 1,
      p.lane ?? '',
      p.force ?? false
    ),
  // ── V2-2 剧库推荐：App.{Recommendations,RecommendationPageReset}（bindings_library.go，位置参数）──
  recommendations: (category, force, more) => callGo<CatalogResult>('Recommendations', category, force, more),
  recommendationPageReset: () => callGo<{ ok: boolean }>('RecommendationPageReset'),
  // ── V2-E 榜单侧栏：App.{RankingBoards,Rankings}（bindings_rankings.go，应答原样透传；
  //    rankingBoards 的 {items} 包裹在本层拆掉，v1 boards() 同款）──
  rankingBoards: () => callGo<{ items?: RankingBoard[] }>('RankingBoards').then((r) => r?.items ?? []),
  rankings: (board, page, force) => callGo<RankingsResult>('Rankings', board, page, force),
  // Detail 绑定是 dramaJSON 位置参数
  detail: (drama: Drama) => callGo<DetailResult>('Detail', JSON.stringify(drama)),
  // Cover 绑定是 (dramaJSON, force)；返回 data URL 裸字符串非 JSON → callGoRaw
  cover: (dramaJSON: string, force: boolean) => callGoRaw('Cover', dramaJSON, force),
  resolve: (drama: Drama, chapter: Chapter, index: number, quality: number) =>
    callGo<ResolvePlan>('Resolve', JSON.stringify(drama), JSON.stringify(chapter), index, quality),
  // BrowseFilters 绑定是 (source, category) 位置参数
  browseFilters: (source: string) => callGo<BrowseFiltersResult>('BrowseFilters', source, ''),
  // ── V2-3c 播放绑定：App.{PlayerOpen,PlayerPlay,PlayerPause,PlayerSeek,PlayerSpeed,PlayerVolume,PlayerDanmaku,PlayerClosePlayer,PlayerState,DanmakuAll} ──
  // PlayerOpen 吃 drama/chapter 原样对象 JSON + 1 起始集号，Go 内部走核心 resolve，返回 Snapshot。
  playerOpen: (drama: Drama, chapter: Chapter, index: number, quality?: string) =>
    callGo<PlayerSnapshot>('PlayerOpen', JSON.stringify(drama), JSON.stringify(chapter), index, quality ?? 'auto'),
  playerPlay: async () => { await callGo('PlayerPlay') },
  pauseVideo: async () => { await callGo('PlayerPause') },
  seekVideo: async (positionMs: number) => { await callGo('PlayerSeek', positionMs) },
  setSpeed: async (v: number) => { await callGo('PlayerSpeed', v) },
  setVolume: async (v: number) => { await callGo('PlayerVolume', Math.round(v)) },
  closeVideo: async () => { await callGo('PlayerClosePlayer') },
  playerState: () => callGo<PlayerSnapshot>('PlayerState'),
  setDanmaku: async (on: boolean) => { await callGo('PlayerDanmaku', on) },
  // 百分比 → 0.2–1.0：v1 设置里存的是「不透明度百分比」（tools.danmaku_opacity 20-100，
  // 没设过按 100），Go 侧 SetDanmakuOpacity 吃 0.05–1.0，换算只在这一处发生。
  setDanmakuOpacity: async (percent: number) => {
    await callGo('PlayerDanmakuOpacity', clampOpacityPercent(percent) / 100)
  },
  // R6：v1 main.py:2598 季末查到下一季后 mark_next_season 的绑定（Go 侧方法早已齐备，
  // 缺的是这条对外绑定 + 前端调用点）。marked=false 表示旧季不在追剧表，照 v1 不谎报。
  setNextSeason: async (dramaID: string, nextDrama: Drama) =>
    callGo<{ marked: boolean }>('SetNextSeason', dramaID, JSON.stringify(nextDrama)),
  observeDrama: async (drama: Drama) =>
    callGo<{ observed: boolean; changed: boolean; row: WatchRow | null }>('ObserveDrama', JSON.stringify(drama)),
  danmakuAll: (session: string) => callGo<DanmakuWindowResult>('DanmakuAll', session),
  watchlist: () => callGo<WatchRow[]>('Watchlist'),
  historyList: () => callGo<HistoryRow[]>('HistoryList'),
  toggleFollow: (drama) => callGo<{ followed: boolean }>('ToggleFollow', JSON.stringify(drama)),
  reserve: (drama) => callGo<{ reserved: boolean }>('Reserve', JSON.stringify(drama)),
  cancelReservation: (id) => callGo<{ ok: boolean }>('CancelReservation', id),
  removeFollow: (id) => callGo<{ ok: boolean }>('RemoveFollow', id),
  saveWatch: (drama, ep, pos, dur, fin) =>
    callGo<{ ok: boolean }>('SaveWatch', JSON.stringify(drama), ep, pos, dur, fin),
  removeHistory: (id) => callGo<{ ok: boolean }>('RemoveHistory', id),
  clearHistory: () => callGo<{ ok: boolean }>('ClearHistory'),
  // ── 看过标记：App.SeenList/ToggleSeen/ImportSeen（bindings_seen.go，值=epoch 秒）──
  seenList: () => callGo<Record<string, number>>('SeenList'),
  toggleSeen: (id) => callGo<{ on: boolean }>('ToggleSeen', id),
  importSeen: (entriesJSON) => callGo<{ added: number }>('ImportSeen', entriesJSON),
  calendar: (date, offset, gender) => callGo<CalendarPayload>('Calendar', date, offset, gender),
  dramaCreators: (drama) => callGo<CreatorsResult>('DramaCreators', JSON.stringify(drama)),
  // ── V2-L 创作者页（bindings_creator.go，discovery 双态通道）──
  creatorProfile: (creator) => callGo<CreatorProfileResult>('CreatorProfile', JSON.stringify(creator)),
  creatorWorks: (creator, tab, subTab, cursor, sort) =>
    callGo<CreatorWorksSnapshot>('CreatorWorks', JSON.stringify(creator), tab, subTab, cursor, sort),
  resourceSettings: () => callGo<ResourceSettingsSnapshot>('GetResourceSettings'),
  saveResourceSettings: (patch) => callGo<unknown>('SaveResourceSettings', JSON.stringify(patch)),
  buildInfo: () => callGo<BuildInfo>('GetBuildInfo'),
  // ── V-UPD 程序内更新：App.Update*（bindings_update.go；事件 update:available/update:progress）──
  updateStartup: () => callGo<{ ok: boolean }>('UpdateStartup'),
  updateCheck: (force) => callGo<UpdateCheckResult>('UpdateCheck', force),
  updateStatus: () => callGo<UpdateStatus>('UpdateStatus'),
  updateDownload: (tag, asset) => callGo<{ ok: boolean }>('UpdateDownload', tag, asset),
  updateCancel: () => callGo<{ ok: boolean }>('UpdateCancel'),
  updateApply: () => callGo<UpdateApplyResult>('UpdateApply'),
  updateSettingsGet: () => callGo<UpdateSettings>('UpdateSettingsGet'),
  updateSettingsSet: (patch) => callGo<UpdateSettings>('UpdateSettingsSet', JSON.stringify(patch)),
  // ── V2-3 下载与合并：App.Download*/Merge*（bindings_download.go，JSON 字符串返回）──
  downloadList: () => callGo<DownloadSnapshot>('DownloadList'),
  downloadStart: (drama, entries, quality) =>
    callGo<{ added: number }>('DownloadStart', JSON.stringify(drama), JSON.stringify(entries), quality),
  downloadPause: (id) => callGo<{ ok: boolean }>('DownloadPause', id),
  downloadResume: (id) => callGo<{ ok: boolean }>('DownloadResume', id),
  downloadRetry: (id) => callGo<{ ok: boolean }>('DownloadRetry', id),
  downloadCancel: (id) => callGo<{ ok: boolean }>('DownloadCancel', id),
  downloadArchive: (id) => callGo<{ ok: boolean }>('DownloadArchive', id),
  downloadRestore: (id) => callGo<{ ok: boolean }>('DownloadRestore', id),
  downloadControlAll: (command) => callGo<{ ok: boolean }>('DownloadControlAll', command),
  downloadRemoveBatch: (ids) =>
    callGo<{ completed: string[]; failures: Record<string, string> }>('DownloadRemoveBatch', JSON.stringify(ids)),
  downloadSettings: () => callGo<DownloadSettingsSnapshot>('DownloadSettings'),
  saveDownloadSettings: (concurrency, bySource, mergeVerify) =>
    callGo<DownloadSettingsSnapshot>('SaveDownloadSettings', concurrency, bySource, mergeVerify),
  mergeStart: (mode, drama, jobIds) =>
    callGo<{ queued: number; replaced: boolean; episodes: number[] }>(
      'MergeStart',
      mode,
      drama ? JSON.stringify(drama) : '',
      JSON.stringify(jobIds)
    ),
  mergeCancel: (id) => callGo<{ ok: boolean }>('MergeCancel', id),
  downloadDirectory: () => callGo<DownloadDirectoryInfo>('DownloadDirectory'),
  archiveScan: () => callGo<ArchiveScanResult>('ArchiveScan'),
  preloadEpisode: (drama, chapter, index, quality) =>
    callGo<string>('PreloadEpisode', JSON.stringify(drama), JSON.stringify(chapter), index, quality),
  openDownloadPath: (path) => callGo<void>('OpenDownloadPath', path),
  // ── 搜索联想：App.Suggest(query)→data JSON {items:[]}（bindings_suggest.go；
  //    Linux 探针 2026-11-11：真实核心 items 为 string[]，如「妈」→10 条剧名）。
  //    兼容对象数组：name/text/title 任一字段取标签（v1 main.py done() 同款防御）。──
  suggestions: async (q: string) => {
    const raw = await callGo<{ items?: unknown } | unknown[]>('Suggest', q)
    const list = Array.isArray(raw) ? raw : (raw as { items?: unknown })?.items
    if (!Array.isArray(list)) return []
    const out: string[] = []
    for (const it of list) {
      if (typeof it === 'string') { if (it.trim()) out.push(it.trim()) }
      else if (it && typeof it === 'object') {
        const o = it as Record<string, unknown>
        const label = String(o.name ?? o.text ?? o.title ?? '').trim()
        if (label) out.push(label)
      }
    }
    return out.slice(0, 10)
  },
  // ── 青空次元账号（bindings_sorani.go App.SoraniAccount(command, accountJSON) 单绑定；
  //    status/captcha/logout 的 accountJSON 传 "{}"，login 传 {identifier,password,captchaId,track}
  //    （Go 侧再嵌 account 键——顶层平铺会被 nativeInput 静默丢掉））──
  soraniStatus: () => callGo<SoraniStatus>('SoraniAccount', '', '{}'),
  soraniCaptcha: () => callGo<SoraniCaptcha>('SoraniAccount', 'captcha', '{}'),
  soraniLogin: (identifier, password, captchaId, track) =>
    callGo<SoraniStatus>('SoraniAccount', 'login', JSON.stringify({ identifier, password, captchaId, track })),
  soraniLogout: () => callGo<SoraniStatus>('SoraniAccount', 'logout', '{}')
}

/* mock 播放内核（V2-3c 契约）：500ms 模拟 tick（与真实事件源同频），
   内部状态即 player.Snapshot 形状（posMs/durationMs 毫秒制）；
   stores/player.ts 轮询 playerState() 消费。eof 为 mock 扩展位：
   真实后端播完靠 player:ended 事件，mock 面无事件总线，由快照带 eof 兜底。 */
/** v1 tools.danmaku_opacity：20–100 的百分比，非法值回落 100（不透明）。 */
export function clampOpacityPercent(v: number): number {
  const n = Number(v)
  if (!Number.isFinite(n)) return 100
  return Math.min(100, Math.max(20, Math.round(n)))
}

const MOCK_DM_COUNT = 80 // mockDanmakuAll 铺满 180s 的条目数
const mockSnap: PlayerSnapshot = {
  playing: false,
  posMs: 0,
  durationMs: 0,
  speed: 1,
  volume: 100,
  danmaku: { enabled: true, count: 0, note: '' },
  episode: { dramaID: '', title: '', index: 0 }
}
const mockPlayer: { opened: boolean; eof: boolean; timer?: ReturnType<typeof setInterval>; lastTs: number } = {
  opened: false,
  eof: false,
  lastTs: 0
}

/** 当前快照（含 mock 扩展 eof 位）；轮询面每次给浅拷贝，防外部改内部状态 */
function mockSnapshotOut(): PlayerSnapshot {
  return {
    ...mockSnap,
    danmaku: { ...mockSnap.danmaku },
    episode: { ...mockSnap.episode },
    eof: mockPlayer.eof
  }
}

function mockSimStart() {
  if (mockPlayer.timer) clearInterval(mockPlayer.timer)
  mockPlayer.lastTs = Date.now()
  mockPlayer.timer = setInterval(() => {
    if (!mockPlayer.opened || !mockSnap.playing || mockPlayer.eof) {
      mockPlayer.lastTs = Date.now()
      return
    }
    const now = Date.now()
    mockSnap.posMs = Math.min(
      mockSnap.durationMs,
      mockSnap.posMs + (now - mockPlayer.lastTs) * (mockSnap.speed ?? 1)
    )
    mockPlayer.lastTs = now
    if (mockSnap.posMs >= mockSnap.durationMs && mockSnap.durationMs > 0) {
      mockPlayer.eof = true
      mockSnap.playing = false
    }
  }, 500)
}

/* ── 青空次元账号 mock 面素材（字段形状对齐 provider_sorani_account.go 真实应答；
   图为假 SVG data URL，尺寸与真实一致：背景 600×360、模板 110×360）── */
const soraniMock = { loggedIn: false, name: '', seq: 0 }
const svgDataURL = (svg: string) => 'data:image/svg+xml;utf8,' + encodeURIComponent(svg)
const MOCK_SORANI_BG = svgDataURL(
  '<svg xmlns="http://www.w3.org/2000/svg" width="600" height="360">' +
    '<rect width="600" height="360" fill="#8b93a1"/>' +
    '<rect x="48" y="70" width="240" height="16" rx="8" fill="#7b8392"/>' +
    '<rect x="48" y="140" width="150" height="16" rx="8" fill="#7b8392"/>' +
    '<rect x="300" y="250" width="220" height="16" rx="8" fill="#7b8392"/>' +
    '<rect x="372" y="96" width="110" height="110" rx="12" fill="#5b6472"/>' +
    '<text x="14" y="344" font-size="18" fill="#6b7482">MOCK SORANI CAPTCHA 600x360</text>' +
    '</svg>'
)
const MOCK_SORANI_TPL = svgDataURL(
  '<svg xmlns="http://www.w3.org/2000/svg" width="110" height="360">' +
    '<rect x="0" y="96" width="110" height="110" rx="12" fill="#3f8cff" stroke="#2f6fd0" stroke-width="2"/>' +
    '<text x="10" y="230" font-size="20" fill="#3f8cff">滑块</text>' +
    '</svg>'
)

const mockApi: NativeApi = {
  ping: async () => 'pong-mock',
  dataPath: async () => '~/.config/ZhenguoJian (mock)',
  listSources: async () => {
    await delay(120)
    return MOCK_SOURCES.map((s) => ({ ...s }))
  },
  setSourceEnabled: async (slug, enabled) => {
    await delay(80)
    const s = MOCK_SOURCES.find((x) => x.slug === slug)
    if (s) s.enabled = enabled
  },
  catalog: async (p: CatalogParams) => {
    await delay(300 + Math.random() * 350)
    return mockCatalog(p.source, p.query ?? '', p.page ?? 1)
  },
  // V2-2 剧库推荐 mock：累计合并表语义（照真实核心 app_recommendations.go:105-122 探针实录）
  recommendations: async (category, force, more) => {
    await delay(200 + Math.random() * 220)
    return mockLibraryRecommendations(category, force, more)
  },
  recommendationPageReset: async () => {
    mockLibraryPageReset()
    return { ok: true }
  },
  // V2-E 榜单 mock：应答形状照 app_rankings.go:60-94（无 boards 键，候选列表独立方法）
  rankingBoards: async () => {
    await delay(100)
    return mockRankingBoards().items
  },
  rankings: async (board, page, force) => {
    await delay(180 + Math.random() * 200)
    return mockRankings(board, page, force)
  },
  detail: async (drama: Drama) => {
    // 测试缝：`__zgjDetailFail` 里列的 id 让详情失败，用来验后台检查的
    // 「一条坏剧不打断整轮 + 结论说 N 部暂时取不到」（v1 main.py:855-857 的那条分支）。
    const failList = (window as any)?.__zgjDetailFail as string[] | undefined
    if (Array.isArray(failList) && failList.includes(String(drama?.id ?? ''))) {
      throw new Error('mock detail 故障注入')
    }
    await delay(250 + Math.random() * 300)
    return mockDetail(drama)
  },
  // mock 面：fixture cover 是本地 /posters/pN.svg，<img> 可直接渲染，原样返回
  cover: async (dramaJSON: string) => {
    try {
      return (JSON.parse(dramaJSON) as Drama).cover ?? ''
    } catch {
      return ''
    }
  },
  resolve: async (drama: Drama, chapter: Chapter, index: number) => {
    await delay(150)
    return mockResolvePlan(drama, chapter, index)
  },
  browseFilters: async (source: string) => {
    await delay(120)
    // mock 与 v2overlay 构建的真实形状一致：红果=服务端筛选面板（genre/别名 category 组），
    // 其余站源=discoveryBrowseFilters 归一的 category 分类组（fixtures 表）。
    if (!source || source === 'hongguo') return mockHongguoFilterGroups()
    return mockSourceCategoryGroups(source)
  },
  playerOpen: async (drama: Drama, chapter: Chapter, index: number, quality?: string) => {
    void quality // mock 面画质无真实语义
    await delay(150)
    if (index < 1) throw new ApiError(t('api.errBadEpisode'), 'bad_video')
    if (!drama || !drama.id) throw new ApiError(t('api.errBadVideoParams'), 'bad_video')
    const durSec = Number(chapter?.duration ?? 0) || 0
    mockSnap.playing = true
    mockSnap.posMs = 0
    mockSnap.durationMs = durSec > 0 ? durSec * 1000 : 180000
    mockSnap.episode = { dramaID: drama.id, title: drama.title ?? '', index }
    // 弹幕语义对齐 Go Service：开弹幕即有已抓计数（mock 铺满 80 条），关时保留计数只撤轨
    mockSnap.danmaku = {
      enabled: mockSnap.danmaku.enabled,
      count: mockSnap.danmaku.enabled ? MOCK_DM_COUNT : 0,
      note: t('api.mockDanmakuNote')
    }
    mockPlayer.opened = true
    mockPlayer.eof = false
    mockSimStart()
    return mockSnapshotOut()
  },
  playerPlay: async () => {
    if (!mockPlayer.opened) throw new ApiError('player: 尚未打开播放会话', 'not_open')
    if (mockPlayer.eof) return // 播完 keep-open：与真实服务一致，需 seek 回片内或换集
    mockSnap.playing = true
    mockPlayer.lastTs = Date.now()
  },
  pauseVideo: async () => {
    if (!mockPlayer.opened) throw new ApiError('player: 尚未打开播放会话', 'not_open')
    mockSnap.playing = false
  },
  seekVideo: async (positionMs: number) => {
    if (!mockPlayer.opened) throw new ApiError('player: 尚未打开播放会话', 'not_open')
    mockSnap.posMs = Math.max(0, Math.min(mockSnap.durationMs, positionMs))
    mockPlayer.lastTs = Date.now()
    if (mockSnap.posMs < mockSnap.durationMs) mockPlayer.eof = false
  },
  setSpeed: async (v: number) => { mockSnap.speed = Math.max(0.5, Math.min(3, v)) },
  setVolume: async (v: number) => { mockSnap.volume = Math.max(0, Math.min(130, v)) },
  closeVideo: async () => {
    if (mockPlayer.timer) clearInterval(mockPlayer.timer)
    mockPlayer.timer = undefined
    mockPlayer.opened = false
    mockPlayer.eof = false
    // 对齐 Go Service.State()：Close 后返回零值骨架（speed/volume 保留播放意图）
    mockSnap.playing = false
    mockSnap.posMs = 0
    mockSnap.durationMs = 0
    mockSnap.episode = { dramaID: '', title: '', index: 0 }
  },
  playerState: async () => {
    if (!mockPlayer.opened && mockSnap.episode.index === 0) {
      // Close 之后：零值骨架（真实 Go 侧 Speed/Volume 保留意图）
      return { ...mockSnapshotOut(), playing: false, posMs: 0, durationMs: 0 }
    }
    return mockSnapshotOut()
  },
  setDanmaku: async (on: boolean) => {
    mockSnap.danmaku = { ...mockSnap.danmaku, enabled: on, count: on ? MOCK_DM_COUNT : mockSnap.danmaku.count }
  },
  setDanmakuOpacity: async (percent: number) => {
    mockSnap.danmaku = { ...mockSnap.danmaku, opacity: clampOpacityPercent(percent) }
  },
  setNextSeason: async (dramaID: string, nextDrama: Drama) => ({
    marked: localMarkNextSeason(dramaID, nextDrama),
  }),
  // mock/降级面的 observeDrama **不模拟旗标推进**：known/newSeason/arrived 怎么变
  // 是 Go 里 observe_drama 的规格（store.go:1176 + 单测），在这里再抄一遍必然漂。
  // 所以 mock 只做两件诚实的事：报告这条在不在追剧表、把行原样回吐。
  // ⚠ 也就是说"检测出 N 部有更新"这条**没在 mock 里被证明**，只有 Go 单测证明过。
  observeDrama: async (drama: Drama) => {
    const rows = readFollows()
    const hit = rows.find((r) => r.drama.id === drama.id)
    return { observed: !!hit, changed: false, row: null as WatchRow | null }
  },
  danmakuAll: async (session: string) => {
    await delay(200)
    return mockDanmakuAll(session)
  },
  // mock：资源设置以 localStorage 模拟核心持久化（改完刷新可还原，便于 UI 验证）
  resourceSettings: async () => {
    await delay(80)
    try {
      return JSON.parse(localStorage.getItem('***' + 'resourceSettings') || '{}')
    } catch {
      return {}
    }
  },
  saveResourceSettings: async (patch) => {
    await delay(80)
    localStorage.setItem('***' + 'resourceSettings', JSON.stringify(patch))
    return { persisted: true }
  },
  buildInfo: async () => ({ revision: 'mock', commitTime: '' }),
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
  },
  // mock：追剧/历史映射 local.ts（浏览器演示与壳内降级共用同一数据面）；日历用 fixtures
  watchlist: async () => {
    // 追剧行 + 「只预约没追剧」的行（v1 互斥后 follow 行不存在，但预约必须是列表里的一条）
    const reservedOnly = Object.entries(readReservationDramas())
      .filter(([id]) => !readFollows().some((f) => f.drama.id === id))
      .map(([, d]) => ({ drama: d, ts: 0 }))
    return [...readFollows(), ...reservedOnly].map((f) => {
      const reserved = !!readReservations()[f.drama.id]
      return {
        id: f.drama.id,
        drama: f.drama,
        status: reserved ? '预约中' : '在看',
        known: f.drama.episodes ?? 0,
        read: 0,
        added: f.ts / 1000,
        season: 1,
        followed: !reserved,
        reserved
      }
    })
  },
  historyList: async () => {
    const rows: HistoryRow[] = []
    for (const [key, e] of Object.entries(readHistory())) {
      const dramaId = key.slice(0, key.lastIndexOf(':'))
      rows.push({
        drama: { id: dramaId, title: e.title, cover: e.cover ?? '' } as Drama,
        episode: e.episode,
        position: e.position,
        duration: e.duration,
        updated: e.ts / 1000
      })
    }
    return rows.sort((a, b) => b.updated - a.updated)
  },
  toggleFollow: async (drama) => ({ followed: toggleFollow(drama) }),
  reserve: async (drama) => {
    const on = toggleReservation(drama.id)
    if (on) {
      rememberReservation(drama)          // 互斥之后没有 follow 行了，靠它把行补回来
      if (isFollowed(drama.id)) toggleFollow(drama) // v1 互斥：预约即取消追
    } else {
      forgetReservation(drama.id)
    }
    return { reserved: on }
  },
  cancelReservation: async (id) => {
    if (readReservations()[id]) toggleReservation(id)
    forgetReservation(id)
    return { ok: true }
  },
  removeFollow: async (id) => {
    if (isFollowed(id)) toggleFollow({ id } as Drama)
    return { ok: true }
  },
  saveWatch: async (drama, ep, pos, dur) => {
    const all = readHistory()
    all[historyKey(drama.id, ep)] = {
      title: drama.title, episode: ep, position: pos, duration: dur,
      ts: Date.now(), dramaId: drama.id, cover: drama.cover
    }
    localStorage.setItem(HISTORY_KEY, JSON.stringify(all))
    return { ok: true }
  },
  removeHistory: async (id) => {
    for (const key of Object.keys(readHistory())) if (key.slice(0, key.lastIndexOf(':')) === id) deleteHistoryEntry(key)
    return { ok: true }
  },
  clearHistory: async () => {
    clearHistoryLocal()
    return { ok: true }
  },
  // mock：看过标记落 local.ts（mock/降级与真实桥同语义；值归一为 epoch 秒对齐 Go SeenList 契约）
  seenList: async () => {
    const out: Record<string, number> = {}
    for (const [k, v] of Object.entries(readWatched())) out[k] = v > 1e11 ? v / 1000 : v
    return out
  },
  toggleSeen: async (id) => ({ on: toggleWatched(id) }),
  importSeen: async (entriesJSON) => {
    let entries: Record<string, number>
    try {
      entries = JSON.parse(entriesJSON) as Record<string, number>
    } catch {
      return { added: 0 }
    }
    const cur = readWatched()
    let added = 0
    for (const [k, v] of Object.entries(entries)) {
      if (!k || Object.prototype.hasOwnProperty.call(cur, k)) continue // 已有 id 不覆盖（Go 真值优先）
      cur[k] = v
      added++
    }
    if (added) localStorage.setItem(WATCHED_KEY, JSON.stringify(cur))
    return { added }
  },
  calendar: async (date) => mockCalendarPage(date || ''),
  dramaCreators: async (drama) => {
    await delay(120)
    return mockDramaCreators(drama)
  },
  creatorProfile: async (creator) => {
    await delay(80)
    return mockCreatorProfile(creator)
  },
  creatorWorks: async (creator, _tab, _subTab, cursor, sort) => {
    await delay(150)
    return mockCreatorWorks(creator, cursor, sort)
  },
  // ── V2-3 下载与合并 mock（fixtures 有状态模拟，tick 1s；轮询消费见 stores/downloads.ts）──
  downloadList: async () => {
    await delay(60)
    return mockDownloadList()
  },
  downloadStart: async (drama, entries, quality) => {
    await delay(120)
    return mockDownloadStart(drama, entries, quality)
  },
  downloadPause: async (id) => mockDownloadControl(id, 'pause'),
  downloadResume: async (id) => mockDownloadControl(id, 'resume'),
  downloadRetry: async (id) => mockDownloadControl(id, 'resume'),
  downloadCancel: async (id) => mockDownloadControl(id, 'remove'),
  downloadArchive: async (id) => mockDownloadControl(id, 'archive'),
  downloadRestore: async (id) => mockDownloadControl(id, 'restore'),
  downloadControlAll: async (command) => mockDownloadControlAll(command),
  downloadRemoveBatch: async (ids) => mockDownloadRemoveBatch(ids),
  downloadSettings: async () => mockDownloadSettings(),
  saveDownloadSettings: async (concurrency, bySource, mergeVerify) =>
    mockSaveDownloadSettings(concurrency, bySource, mergeVerify),
  mergeStart: async (mode, drama, jobIds) => mockMergeStart(mode, drama, jobIds),
  mergeCancel: async (id) => mockMergeCancel(id),
  downloadDirectory: async () => mockDownloadDirectory(),
  archiveScan: async () => mockArchiveScan(),
  preloadEpisode: async () => '',
  openDownloadPath: async (path: string) => {
    if (typeof window !== 'undefined') window.__zgjOpenPath = path
  },
  // ── 搜索联想 mock（fixtures.mockSuggestions，示例数据）；调用计数落
  //    window.__zgjSuggestCalls 供 headless 防抖/门控断言（同 __zgjHitBands 先例）──
  suggestions: async (q: string) => {
    if (typeof window !== 'undefined') window.__zgjSuggestCalls = (window.__zgjSuggestCalls ?? 0) + 1
    await delay(120 + Math.random() * 160)
    return mockSuggestions(q)
  },
  // ── 青空次元账号 mock：login 只校验 track 形状（首 down 尾 up、≥2 点、尺寸/时间自洽）即成功；
  //    window.__zgj.mockLoginFail='1' 强制失败（M5 断言用）；入参快照落 __zgjSoraniLastLogin（M8 断言用）──
  soraniStatus: async () => {
    await delay(80)
    return { loggedIn: soraniMock.loggedIn, name: soraniMock.name }
  },
  soraniCaptcha: async () => {
    await delay(150)
    soraniMock.seq += 1
    return {
      id: `mock-captcha-${soraniMock.seq}`,
      backgroundImage: MOCK_SORANI_BG,
      templateImage: MOCK_SORANI_TPL,
      backgroundWidth: 600,
      backgroundHeight: 360,
      templateWidth: 110,
      templateHeight: 360
    }
  },
  soraniLogin: async (identifier, password, captchaId, track) => {
    await delay(220)
    if (typeof window !== 'undefined') window.__zgjSoraniLastLogin = { identifier, password, captchaId, track }
    if (!identifier.trim() || !password) throw new ApiError(t('api.errLoginInput'), 'bad_input')
    const pts = track.trackList ?? []
    const dims = [track.bgImageWidth, track.bgImageHeight, track.templateImageWidth, track.templateImageHeight]
    const trackOk =
      dims.every((n) => n > 0 && n <= 4000) &&
      pts.length >= 2 &&
      pts.length <= 5000 &&
      pts[0].type === 'down' &&
      pts[pts.length - 1].type === 'up' &&
      track.startTime > 0 &&
      track.stopTime >= track.startTime
    if (!trackOk) throw new ApiError(t('api.errCaptcha'), 'sorani_captcha_fail')
    if (typeof window !== 'undefined' && window.__zgj?.mockLoginFail === '1')
      throw new ApiError(t('api.errCaptcha'), 'sorani_captcha_fail')
    soraniMock.loggedIn = true
    soraniMock.name = identifier.trim()
    return { loggedIn: true, name: identifier.trim() }
  },
  soraniLogout: async () => {
    await delay(120)
    soraniMock.loggedIn = false
    soraniMock.name = ''
    return { loggedIn: false, name: '' }
  }
}

/* 降级策略：真实 exe 里桥必在；若 WebView2 初始化竞态导致 window.go 短暂缺失，
   首次访问时重试探测，仍缺失则回落 mock 面并亮横幅（DEGRADED），避免整页死锁。 */
export const DEGRADED = !isMock && !hasBridge
export const bridgeMissing = DEGRADED
export const api: NativeApi = isMock ? mockApi : hasBridge ? realApi : mockApi
export { ApiError }
export const MOCK_MODE = isMock

/* ── V2-4 播放画面内嵌绑定（App.{PlayerAttach,PlayerSetBounds,PlayerDetach}）──
   不进 NativeApi 面：这是播放页生命周期细节，mock/无桥/非 Windows 一律降级为
   no-op（attach 返回 embedded:false），PlayerView 无需按模式特判——内嵌不可得
   时整条播放链路保持现行行为（mpv 独立窗 / 浏览器无画面）。
   时序契约：PlayerAttach → PlayerOpen（spawn 前 SetWid 生效）→ 几何/状态变化
   PlayerSetBounds（物理像素，零尺寸=隐藏）→ 离开 PlayerDetach。 */
export interface AttachResult {
  embedded: boolean
  reason?: string
}

export const embedApi = {
  attach: async (): Promise<AttachResult> => {
    if (isMock) {
      // 验证钩子：headless 场景置 window.__zgjMockEmbed=true 谎报「内嵌可用」，
      // 驱动 PlayerView 的 bounds/hitBands 上报链路（真实 exe 恒 isMock=false）。
      return { embedded: typeof window !== 'undefined' && !!window.__zgjMockEmbed, reason: 'mock-embed' }
    }
    if (!hasBridge) return { embedded: false }
    try {
      return (await callGo<AttachResult>('PlayerAttach')) ?? { embedded: false }
    } catch (err: any) {
      return { embedded: false, reason: String(err?.message ?? err) }
    }
  },
  setBounds: async (x: number, y: number, w: number, h: number): Promise<void> => {
    if (isMock || !hasBridge) return
    try {
      await callGo('PlayerSetBounds', x, y, w, h)
    } catch {
      /* 几何上报失败不干扰播放 UI（窗口销毁竞态等） */
    }
  },
  /* 覆盖层穿透带（App.PlayerHitBands）：top/bottom=HTML 覆盖层与视频矩形
     的实际重叠高度（物理像素），0=该侧不禁。桥缺失 no-op；mock 记录到
     window.__zgjHitBands 供 headless 验证断言。 */
  playerHitBands: async (top: number, bottom: number): Promise<void> => {
    if (isMock || !hasBridge) {
      if (typeof window !== 'undefined') {
        const rec = window.__zgjHitBands ?? { count: 0, last: null }
        rec.count += 1
        rec.last = { top, bottom }
        window.__zgjHitBands = rec
      }
      return
    }
    try {
      await callGo('PlayerHitBands', top, bottom)
    } catch {
      /* 同 setBounds：上报失败不干扰播放 UI */
    }
  },
  detach: async (): Promise<void> => {
    if (isMock || !hasBridge) return
    try {
      await callGo('PlayerDetach')
    } catch {
      /* 同上 */
    }
  }
}
