/* v1 行协议数据形状（照抄 zhenguojian/sidecar nativeDrama / catalog / sourceListItem） */

export interface Drama {
  metadataSchema?: number
  id: string
  source: string
  sourceId?: string
  title: string
  description?: string
  cover?: string
  episodes?: number
  category?: string
  vip?: boolean | null
  heat?: string
  views?: string
  onlineDate?: string
  tags?: string[]
  releaseStatus?: string
  /** mock-only：源级 18+ 演示用，真实引擎 items 不带 */
  adult?: boolean
}

/** 引擎 chapters[] 单集形状（红果实测：无逐集 vip 字段，drama.vip 整剧标志优先；
   兼容 v1 widgets._episode_number / detail_episodes._chapter_vip 读过的字段名） */
export interface Chapter {
  currentEpisode?: number | string
  title?: string
  duration?: number | string
  /** 逐集 VIP 兜底字段（部分源用） */
  vip?: boolean | number | string
  type?: string
  is_vip?: boolean | number | string
  [key: string]: unknown
}

export interface CatalogResult {
  items: Drama[]
  hasMore: boolean
  page: number
  warning?: string
  localSearch?: boolean
  fresh?: boolean
}

export interface CatalogParams {
  source: string
  query?: string
  category?: string
  page?: number
  lane?: string
  force?: boolean
}

export interface SourceListItem {
  slug: string
  name: string
  displayName?: string
  searchKind?: string
  adult?: boolean
  lanes?: string[]
  seasons?: boolean
  catalogKinds?: string[]
  enabledByDefault?: boolean
  implemented: boolean
  enabled: boolean
}

export interface FilterOption {
  id: string
  name: string
}

export interface FilterGroup {
  key: string
  name: string
  options: FilterOption[]
}

export interface BrowseFiltersResult {
  groups: FilterGroup[]
}

/** App.Detail(dramaJSON) 返回：drama 字段并集 + chapters[] */
export interface DetailResult {
  drama?: Drama
  chapters?: Chapter[]
  warning?: string
  [key: string]: unknown
}

/** App.Resolve(dramaJSON, chapterJSON, index, quality) 返回的播放/下载 plan（v1 平铺字段） */
export interface ResolvePlan {
  url?: string
  headers?: Record<string, string>
  quality?: number | string
  qualities?: { quality: number; name?: string }[]
  session?: string
  routeIndex?: number
  routeCount?: number
  [key: string]: unknown
}

/** 上新日历行（v1 calendar_panel._calendar_drama 的输入形状） */
export interface CalendarItem {
  date?: string
  online?: boolean
  hint?: string
  drama?: Drama
  [key: string]: unknown
}

/** 弹幕条目（core danmaku/danmakuAll 应答 items[] 形状，同 internal/danmaku.Item） */
export interface DanmakuItem {
  id: string
  timeMs: number
  text: string
  color?: string
  mode?: number
  durationMs?: number
}

/** App.DanmakuWindow/DanmakuAll(session) 返回 */
export interface DanmakuWindowResult {
  items: DanmakuItem[]
  startMs?: number
  nextMs?: number
  total?: number
  windows?: Record<string, number>
}

/** App.State() 快照（internal/player.Service.State） */
export interface PlayerState {
  running: boolean
  opened: boolean
  danmakuOn: boolean
  ep: number
  title: string
  position?: number
  duration?: number
  paused?: boolean
  speed?: number
  volume?: number
  eof?: boolean
}

/** 适配层统一接口：wails 实现与 mock 实现同签名 */
export interface NativeApi {
  ping(): Promise<string>
  dataPath(): Promise<string>
  listSources(): Promise<SourceListItem[]>
  setSourceEnabled(slug: string, enabled: boolean): Promise<void>
  catalog(params: CatalogParams): Promise<CatalogResult>
  detail(drama: Drama): Promise<DetailResult>
  browseFilters(source: string): Promise<BrowseFiltersResult>
  resolve(drama: Drama, chapter: Chapter, index: number, quality: number): Promise<ResolvePlan>
  // ── V2-3b 播放内核桥（internal/player.Service；事件走 wails EventsOn，见 stores/player.ts）──
  openVideo(videoJSON: string): Promise<void>
  pauseVideo(): Promise<void>
  resumeVideo(): Promise<void>
  seekVideo(positionMs: number): Promise<void>
  setSpeed(v: number): Promise<void>
  setVolume(v: number): Promise<void>
  closeVideo(): Promise<void>
  playerState(): Promise<PlayerState>
  setDanmaku(on: boolean): Promise<void>
  reportViewport(w: number, h: number): Promise<void>
  danmakuAll(session: string): Promise<DanmakuWindowResult>
  // ── watchstore / 日历绑定（Go 进程内持久化；视图迁移为后续切片）──
  watchlist(): Promise<WatchRow[]>
  historyList(): Promise<HistoryRow[]>
  toggleFollow(drama: Drama): Promise<{ followed: boolean }>
  reserve(drama: Drama): Promise<{ reserved: boolean }>
  cancelReservation(dramaId: string): Promise<{ ok: boolean }>
  removeFollow(dramaId: string): Promise<{ ok: boolean }>
  saveWatch(drama: Drama, episode: number, position: number, duration: number, finished: boolean): Promise<{ ok: boolean }>
  removeHistory(dramaId: string): Promise<{ ok: boolean }>
  clearHistory(): Promise<{ ok: boolean }>
  calendar(date: string, offset: number, gender: number): Promise<CalendarPayload>
  // ── V2-4 设置/指纹 ──
  resourceSettings(): Promise<ResourceSettingsSnapshot>
  saveResourceSettings(patch: Partial<ResourceSettingsSnapshot>): Promise<unknown>
  buildInfo(): Promise<BuildInfo>
}

/** watchstore 追剧行（v1 follow 行同构子集；字段表见主代理契约） */
export interface WatchRow {
  id: string
  drama: Drama
  status: string
  known: number
  read: number
  added: number
  season: number
  latestSeason?: number
  newSeason?: boolean
  followed: boolean
  reserved: boolean
  reserveSeason?: number
  arrived?: boolean
  newEpisodes?: number
  updateAt?: number
  hasUpdate?: boolean
  badge?: number
  seenEpisode?: number
  hasWatched?: boolean
  nextDrama?: Drama | null
  nextSeason?: number
}

/** watchstore 历史行（v1 save_watch 行同构） */
export interface HistoryRow {
  drama: Drama
  episode: number
  position: number
  duration: number
  updated: number
  localPath?: string
}

/** 上新日历应答（核心 data 平铺） */
export interface CalendarPayload {
  date?: string
  items?: CalendarItem[]
  [k: string]: unknown
}

/** 网络与下载设置快照（字段与核心 nativeResourceSettings 一致） */
export interface ResourceSettingsSnapshot {
  proxyMode?: string
  proxyUrl?: string
  catalogConcurrency?: number
  catalogIntervalMs?: number
  downloadConcurrency?: number
  downloadBySource?: boolean
}

/** 打包提交指纹（exe 同级 build-info.json 或构建期 VCS 注入） */
export interface BuildInfo {
  revision?: string
  commitTime?: string
  modified?: boolean
}

/** 带协议 code 的错误（discovery_unavailable / source_blocked 等） */
export class ApiError extends Error {
  code: string
  retryAt?: number
  constructor(message: string, code = '', retryAt?: number) {
    super(message)
    this.code = code
    this.retryAt = retryAt
  }
}
