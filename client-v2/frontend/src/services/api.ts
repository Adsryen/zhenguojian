/* 绑定适配层：组件只依赖本模块，永不直接触碰 window.go。
   - 真实模式：window.go.main.App.<Method>(...) 返回 JSON 字符串（Go 侧 (string, error)）
   - VITE_MOCK=1：fixtures 双实现，可在纯浏览器里验证 UI */
import {
  ApiError,
  type BrowseFiltersResult,
  type CatalogParams,
  type CatalogResult,
  type Chapter,
  type DetailResult,
  type Drama,
  type NativeApi,
  type PlayerState,
  type DanmakuWindowResult,
  type ResolvePlan,
  type SourceListItem
} from './types'
import { MOCK_SOURCES, mockCatalog, mockDanmakuAll, mockDetail, mockResolvePlan } from './fixtures'

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
  danmakuAll: (session: string) => callGo<DanmakuWindowResult>('DanmakuAll', session)
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
  }
}

export const api: NativeApi = isMock ? mockApi : realApi
export { ApiError }
export const MOCK_MODE = isMock
