/* 绑定适配层：组件只依赖本模块，永不直接触碰 window.go。
   - 真实模式：window.go.main.App.<Method>(...) 返回 JSON 字符串（Go 侧 (string, error)）
   - VITE_MOCK=1：fixtures 双实现，可在纯浏览器里验证 UI */
import { ApiError } from './types'
import type {
  BrowseFiltersResult,
  BuildInfo,
  CalendarPayload,
  CatalogParams,
  CatalogResult,
  Chapter,
  DanmakuWindowResult,
  DetailResult,
  Drama,
  HistoryRow,
  NativeApi,
  PlayerState,
  ResolvePlan,
  ResourceSettingsSnapshot,
  SourceListItem,
  WatchRow,
} from './types'
import { MOCK_SOURCES, mockCatalog, mockDanmakuAll, mockDetail, mockResolvePlan, mockCalendarDays } from './fixtures'
import {
  clearHistory as clearHistoryLocal,
  deleteHistoryEntry,
  HISTORY_KEY,
  historyKey,
  isFollowed,
  readFollows,
  readHistory,
  readReservations,
  toggleFollow,
  toggleReservation
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
  // Detail 绑定是 dramaJSON 位置参数
  detail: (drama: Drama) => callGo<DetailResult>('Detail', JSON.stringify(drama)),
  resolve: (drama: Drama, chapter: Chapter, index: number, quality: number) =>
    callGo<ResolvePlan>('Resolve', JSON.stringify(drama), JSON.stringify(chapter), index, quality),
  // BrowseFilters 绑定是 (source, category) 位置参数
  browseFilters: (source: string) => callGo<BrowseFiltersResult>('BrowseFilters', source, ''),
  // ── V2-3b 播放内核：App.{Open,Pause,Resume,Seek,SetSpeed,SetVolume,Close,State,SetDanmaku,ReportViewport,DanmakuAll} ──
  openVideo: async (videoJSON: string) => {
    await callGo('Open', videoJSON)
  },
  pauseVideo: async () => { await callGo('Pause') },
  resumeVideo: async () => { await callGo('Resume') },
  seekVideo: async (positionMs: number) => { await callGo('Seek', positionMs) },
  setSpeed: async (v: number) => { await callGo('SetSpeed', v) },
  setVolume: async (v: number) => { await callGo('SetVolume', v) },
  closeVideo: async () => { await callGo('Close') },
  playerState: () => callGo<PlayerState>('State'),
  setDanmaku: async (on: boolean) => { await callGo('SetDanmaku', on) },
  reportViewport: async (w: number, h: number) => { await callGo('ReportViewport', w, h) },
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
  calendar: (date, offset, gender) => callGo<CalendarPayload>('Calendar', date, offset, gender),
  resourceSettings: () => callGo<ResourceSettingsSnapshot>('GetResourceSettings'),
  saveResourceSettings: (patch) => callGo<unknown>('SaveResourceSettings', JSON.stringify(patch)),
  buildInfo: () => callGo<BuildInfo>('GetBuildInfo')
}

/* mock 播放内核：500ms 模拟 tick（与真实事件源同频），stores/player.ts 轮询消费 */
const mockPlayer: PlayerState & { position: number; duration: number; timer?: ReturnType<typeof setInterval>; lastTs: number } = {
  running: false,
  opened: false,
  danmakuOn: true,
  ep: 0,
  title: '',
  position: 0,
  duration: 180,
  paused: false,
  speed: 1,
  volume: 100,
  eof: false,
  lastTs: 0
}

function mockSimStart() {
  if (mockPlayer.timer) clearInterval(mockPlayer.timer)
  mockPlayer.lastTs = Date.now()
  mockPlayer.timer = setInterval(() => {
    if (!mockPlayer.opened || mockPlayer.paused || mockPlayer.eof) {
      mockPlayer.lastTs = Date.now()
      return
    }
    const now = Date.now()
    mockPlayer.position = Math.min(mockPlayer.duration, mockPlayer.position + ((now - mockPlayer.lastTs) / 1000) * (mockPlayer.speed ?? 1))
    mockPlayer.lastTs = now
    if (mockPlayer.position >= mockPlayer.duration) {
      mockPlayer.eof = true
      mockPlayer.paused = true
    }
  }, 500)
}

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
  detail: async (drama: Drama) => {
    await delay(250 + Math.random() * 300)
    return mockDetail(drama)
  },
  resolve: async (drama: Drama, chapter: Chapter, index: number) => {
    await delay(150)
    return mockResolvePlan(drama, chapter, index)
  },
  browseFilters: async () => {
    await delay(120)
    // 与默认构建（无 overlay）一致：模拟 discovery_unavailable，触发前端兜底 chips
    throw new ApiError('筛选墙通道未启用（mock 与默认构建一致）', 'discovery_unavailable')
  },
  openVideo: async (videoJSON: string) => {
    await delay(150)
    try {
      const v = JSON.parse(videoJSON) as { url?: string; title?: string; ep?: number; durationSec?: number }
      mockPlayer.title = v.title ?? ''
      mockPlayer.ep = v.ep ?? 0
      mockPlayer.duration = v.durationSec && v.durationSec > 0 ? v.durationSec : 180
      mockPlayer.position = 0
      mockPlayer.eof = false
      mockPlayer.paused = false
      mockPlayer.opened = true
      mockPlayer.running = true
      mockSimStart()
    } catch {
      throw new ApiError('播放参数解析失败 [bad_video]', 'bad_video')
    }
  },
  pauseVideo: async () => { mockPlayer.paused = true },
  resumeVideo: async () => {
    mockPlayer.paused = false
    mockPlayer.eof = false
    mockPlayer.lastTs = Date.now()
  },
  seekVideo: async (positionMs: number) => {
    mockPlayer.position = Math.max(0, Math.min(mockPlayer.duration, positionMs / 1000))
    mockPlayer.lastTs = Date.now()
    if (mockPlayer.position < mockPlayer.duration) mockPlayer.eof = false
  },
  setSpeed: async (v: number) => { mockPlayer.speed = Math.max(0.5, Math.min(3, v)) },
  setVolume: async (v: number) => { mockPlayer.volume = Math.max(0, Math.min(100, v)) },
  closeVideo: async () => {
    if (mockPlayer.timer) clearInterval(mockPlayer.timer)
    mockPlayer.timer = undefined
    mockPlayer.opened = false
    mockPlayer.running = false
    mockPlayer.position = 0
    mockPlayer.eof = false
  },
  playerState: async () => {
    const { timer: _t, lastTs: _l, ...rest } = mockPlayer
    return { ...rest } as PlayerState
  },
  setDanmaku: async (on: boolean) => { mockPlayer.danmakuOn = on },
  reportViewport: async () => undefined,
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
  // mock：追剧/历史映射 local.ts（浏览器演示与壳内降级共用同一数据面）；日历用 fixtures
  watchlist: async () =>
    readFollows().map((f) => {
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
    }),
  historyList: async () => {
    const rows: HistoryRow[] = []
    for (const [key, e] of Object.entries(readHistory())) {
      const dramaId = key.split('#')[0]
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
    if (on && isFollowed(drama.id)) toggleFollow(drama) // v1 互斥：预约即取消追
    return { reserved: on }
  },
  cancelReservation: async (id) => {
    if (readReservations()[id]) toggleReservation(id)
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
    for (const key of Object.keys(readHistory())) if (key.split('#')[0] === id) deleteHistoryEntry(key)
    return { ok: true }
  },
  clearHistory: async () => {
    clearHistoryLocal()
    return { ok: true }
  },
  calendar: async () => ({ items: mockCalendarDays() })
}

/* 降级策略：真实 exe 里桥必在；若 WebView2 初始化竞态导致 window.go 短暂缺失，
   首次访问时重试探测，仍缺失则回落 mock 面并亮横幅（DEGRADED），避免整页死锁。 */
export const DEGRADED = !isMock && !hasBridge
export const bridgeMissing = DEGRADED
export const api: NativeApi = isMock ? mockApi : hasBridge ? realApi : mockApi
export { ApiError }
export const MOCK_MODE = isMock
