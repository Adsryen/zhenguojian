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
  DownloadSettingsSnapshot,
  DownloadSnapshot,
  Drama,
  HistoryRow,
  NativeApi,
  PlayerSnapshot,
  ResolvePlan,
  ResourceSettingsSnapshot,
  SourceListItem,
  WatchRow,
} from './types'
import { MOCK_SOURCES, mockCatalog, mockDanmakuAll, mockDetail, mockDownloadControl, mockDownloadControlAll, mockDownloadList, mockDownloadRemoveBatch, mockDownloadSettings, mockDownloadStart, mockMergeCancel, mockMergeStart, mockResolvePlan, mockSaveDownloadSettings, mockCalendarPage } from './fixtures'
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
  // ── V2-3c 播放绑定：App.{PlayerOpen,PlayerPlay,PlayerPause,PlayerSeek,PlayerSpeed,PlayerVolume,PlayerDanmaku,PlayerClosePlayer,PlayerState,DanmakuAll} ──
  // PlayerOpen 吃 drama/chapter 原样对象 JSON + 1 起始集号，Go 内部走核心 resolve，返回 Snapshot。
  playerOpen: (drama: Drama, chapter: Chapter, index: number) =>
    callGo<PlayerSnapshot>('PlayerOpen', JSON.stringify(drama), JSON.stringify(chapter), index),
  playerPlay: async () => { await callGo('PlayerPlay') },
  pauseVideo: async () => { await callGo('PlayerPause') },
  seekVideo: async (positionMs: number) => { await callGo('PlayerSeek', positionMs) },
  setSpeed: async (v: number) => { await callGo('PlayerSpeed', v) },
  setVolume: async (v: number) => { await callGo('PlayerVolume', Math.round(v)) },
  closeVideo: async () => { await callGo('PlayerClosePlayer') },
  playerState: () => callGo<PlayerSnapshot>('PlayerState'),
  setDanmaku: async (on: boolean) => { await callGo('PlayerDanmaku', on) },
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
  buildInfo: () => callGo<BuildInfo>('GetBuildInfo'),
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
  mergeCancel: (id) => callGo<{ ok: boolean }>('MergeCancel', id)
}

/* mock 播放内核（V2-3c 契约）：500ms 模拟 tick（与真实事件源同频），
   内部状态即 player.Snapshot 形状（posMs/durationMs 毫秒制）；
   stores/player.ts 轮询 playerState() 消费。eof 为 mock 扩展位：
   真实后端播完靠 player:ended 事件，mock 面无事件总线，由快照带 eof 兜底。 */
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
  playerOpen: async (drama: Drama, chapter: Chapter, index: number) => {
    await delay(150)
    if (index < 1) throw new ApiError('集号无效', 'bad_video')
    if (!drama || !drama.id) throw new ApiError('播放参数解析失败', 'bad_video')
    const durSec = Number(chapter?.duration ?? 0) || 0
    mockSnap.playing = true
    mockSnap.posMs = 0
    mockSnap.durationMs = durSec > 0 ? durSec * 1000 : 180000
    mockSnap.episode = { dramaID: drama.id, title: drama.title ?? '', index }
    // 弹幕语义对齐 Go Service：开弹幕即有已抓计数（mock 铺满 80 条），关时保留计数只撤轨
    mockSnap.danmaku = {
      enabled: mockSnap.danmaku.enabled,
      count: mockSnap.danmaku.enabled ? MOCK_DM_COUNT : 0,
      note: 'VITE_MOCK 示例弹幕'
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
  calendar: async (date) => mockCalendarPage(date || ''),
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
  mergeCancel: async (id) => mockMergeCancel(id)
}

/* 降级策略：真实 exe 里桥必在；若 WebView2 初始化竞态导致 window.go 短暂缺失，
   首次访问时重试探测，仍缺失则回落 mock 面并亮横幅（DEGRADED），避免整页死锁。 */
export const DEGRADED = !isMock && !hasBridge
export const bridgeMissing = DEGRADED
export const api: NativeApi = isMock ? mockApi : hasBridge ? realApi : mockApi
export { ApiError }
export const MOCK_MODE = isMock
