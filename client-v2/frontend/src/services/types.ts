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
  /** 上游连通性/使用备注（v1 settings_panel.FALLBACK_SOURCES 同表）。 */
  note?: string
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

/** 核心日历条目（case "calendar" → hongguoCalendarItem 平铺形状，逐键对照
    guoapp-src/native/core/provider_hongguo_calendar.go；条目属于所查询日期，无逐条 date） */
export interface CalendarEntry {
  seriesId?: string
  title?: string
  cover?: string
  description?: string
  category?: string
  tags?: string[]
  episodeCnt?: number
  publishAt?: number // ms 时间戳
  publishText?: string // sub_info 原文（如「今晚18:00 更新」）
  hint?: string // 未上线：上线时间文案
  note?: string // 已上线=热度文本；未上线=预计上线文本
  heat?: string
  expectedText?: string
  isOnline?: boolean
  subscribed?: boolean // 服务端订阅态（账号侧）
  badge?: string // title_left_icon 角标文案
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

/** Snapshot 的弹幕部分（internal/player.DanmakuState） */
export interface PlayerDanmakuState {
  enabled: boolean
  count: number
  note: string
  /** 壳层回报的当前不透明度，**0.05–1.0 小数**（internal/player.DanmakuState.Opacity；
   *  未设过时壳层按 1.0 报）。前端 localStorage 存的是 20–100 百分比，两者换算在这一层收口。 */
  opacity?: number
}

/** Snapshot 的当前集标识（internal/player.EpisodeRef） */
export interface PlayerEpisode {
  dramaID: string
  title: string
  index: number
}

/**
 * App.PlayerState() / App.PlayerOpen() 返回值，也是 player:tick 事件 payload
 * （internal/player.Snapshot；posMs/durationMs 毫秒制）
 */
export interface PlayerSnapshot {
  playing: boolean
  posMs: number
  durationMs: number
  speed: number
  volume: number
  danmaku: PlayerDanmakuState
  episode: PlayerEpisode
  /**
   * mock 扩展位：本集播完标记（VITE_MOCK 轮询面无事件总线，靠它感知播完）。
   * 真实后端不发此字段，播完走 player:ended 事件。
   */
  eof?: boolean
}

/** player:ended 事件 payload（service.go markEndedLocked） */
export interface PlayerEndedPayload {
  episode: PlayerEpisode
}

/** player:error 事件 payload（service.go emit player:error） */
export interface PlayerErrorPayload {
  message: string
}

/** danmaku:stats 事件 payload（service.go emitStatsLocked） */
export interface DanmakuStatsPayload {
  count: number
  note: string
}

/** 一次覆盖层穿透带上报（物理像素；对应绑定 App.PlayerHitBands(top,bottom)） */
export interface HitBandsReport {
  top: number
  bottom: number
}

/** mock/无桥路径下的记录钩子形状：window.__zgjHitBands（headless 验证断言用） */
export interface HitBandsRecord {
  count: number
  last: HitBandsReport | null
}

declare global {
  interface Window {
    /** embedApi.playerHitBands 在 mock/无桥时的落点（真实 exe 不写） */
    __zgjHitBands?: HitBandsRecord
    /** mock 验证钩子：置 true 让 embedApi.attach 谎报 embedded:true，
        以驱动 PlayerView 的 setBounds/hitBands 上报链路（纯浏览器无副作用） */
    __zgjMockEmbed?: boolean
    /** mock 验证钩子：mockApi.suggestions 调用计数（联想防抖/门控断言用；真实桥不写） */
    __zgjSuggestCalls?: number
    /** mock 验证钩子：mockApi.openDownloadPath 最近一次路径 */
    __zgjOpenPath?: string
    /** mock 验证钩子：置 '1' 让 mockApi.soraniLogin 强制失败（M5 登录失败路径断言用） */
    __zgj?: { mockLoginFail?: string }
    /** mock 验证钩子：mockApi.soraniLogin 最近一次入参（M8 track 形状断言用；真实桥不写） */
    __zgjSoraniLastLogin?: SoraniLoginAttempt | null
  }
}

/** 榜单候选（rankingBoards action；核心按站源可用性过滤，app_runtime.go:457-462。V2-E 侧栏） */
export interface RankingBoard {
  id: string
  source: string
  name: string
  description?: string
}

/** 榜单单行（rankings 应答 items 元素；drama 为 nativeDrama 平铺） */
export interface RankingItem {
  rank: number
  drama: Drama
  metric?: string
}

/** rankings 应答平铺形状（键以 guoapp-src/native/core/app_rankings.go:60-94 为准，无 boards 键） */
export interface RankingsResult {
  boardId?: string
  page?: number
  items: RankingItem[]
  hasMore?: boolean
  totalPages?: number
  updatedText?: string
  fetchedAt?: string
  stale?: boolean
  warning?: string
}

/** 适配层统一接口：wails 实现与 mock 实现同签名 */
export interface NativeApi {
  ping(): Promise<string> | Promise<unknown>
  dataPath(): Promise<string>
  listSources(): Promise<SourceListItem[]>
  setSourceEnabled(slug: string, enabled: boolean): Promise<void>
  catalog(params: CatalogParams): Promise<CatalogResult>
  // ── V2-2 剧库推荐（bindings_library.go）：题材个性化推荐一页 + 「全部」tab 壳层页码游标重置 ──
  recommendations(category: string, force: boolean, more: boolean): Promise<CatalogResult>
  recommendationPageReset(): Promise<{ ok: boolean }>
  // ── V2-E 榜单侧栏（bindings_rankings.go）：候选列表 + 单页榜单，应答原样透传 ──
  rankingBoards(): Promise<RankingBoard[]>
  rankings(board: string, page: number, force: boolean): Promise<RankingsResult>
  detail(drama: Drama): Promise<DetailResult>
  /** 封面管线（Go bindings_cover.go）：核心下载 + HEIC→JPEG 转码，resolve 为 data URL 字符串 */
  cover(dramaJSON: string, force: boolean): Promise<string>
  browseFilters(source: string): Promise<BrowseFiltersResult>
  resolve(drama: Drama, chapter: Chapter, index: number, quality: number): Promise<ResolvePlan>
  // ── V2-3c 播放桥（app.go「V2-3c 播放绑定」；事件 player:tick/ended/error + danmaku:stats，见 stores/player.ts）──
  /** 起播：drama/chapter 原样对象交给 Go，Go 内部走核心 resolve；index 1 起始；返回 Snapshot */
  playerOpen(drama: Drama, chapter: Chapter, index: number, quality?: string): Promise<PlayerSnapshot>
  playerPlay(): Promise<void>
  pauseVideo(): Promise<void>
  seekVideo(positionMs: number): Promise<void>
  setSpeed(v: number): Promise<void>
  setVolume(v: number): Promise<void>
  closeVideo(): Promise<void>
  playerState(): Promise<PlayerSnapshot>
  setDanmaku(on: boolean): Promise<void>
  /** 弹幕不透明度，百分比 20–100（v1 tools.danmaku_opacity 同一量纲）；
      真桥侧由壳层换算成 0.2–1.0 再交给 App.PlayerDanmakuOpacity。 */
  setDanmakuOpacity(percent: number): Promise<void>
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
  // ── 看过标记（范围 A：Go bindings_seen.go / seen-library.json，值=epoch 秒）──
  seenList(): Promise<Record<string, number>>
  toggleSeen(dramaId: string): Promise<{ on: boolean }>
  importSeen(entriesJSON: string): Promise<{ added: number }>
  calendar(date: string, offset: number, gender: number): Promise<CalendarPayload>
  /** 演职员条（v2-D）：真实桥 = App.DramaCreators（bindings_cast.go）；discovery 未接入时 reject，调用方静默隐藏 */
  dramaCreators(drama: Drama): Promise<CreatorsResult>
  /** 创作者页头部（V2-L）：真实桥 = App.CreatorProfile（bindings_creator.go）；
      discovery 未启用/核心报错时 reject，调用方亮红条 + 重试 */
  creatorProfile(creator: CreatorEntry): Promise<CreatorProfileResult>
  /** 创作者作品墙：tab/subTab 传 0 = 核心自动取首个作品分类；
      sort ∈ 'time_desc'|'hot_desc'；cursor 空串 = 首屏 */
  creatorWorks(creator: CreatorEntry, tab: number, subTab: number, cursor: string, sort: string): Promise<CreatorWorksSnapshot>
  // ── V2-4 设置/指纹 ──
  resourceSettings(): Promise<ResourceSettingsSnapshot>
  saveResourceSettings(patch: Partial<ResourceSettingsSnapshot>): Promise<unknown>
  buildInfo(): Promise<BuildInfo>
  // ── V2-3 下载与合并（bindings_download.go；事件 downloads:update）──
  downloadList(): Promise<DownloadSnapshot>
  downloadStart(drama: Drama, entries: DownloadEntry[], quality: number): Promise<{ added: number }>
  downloadPause(id: string): Promise<{ ok: boolean }>
  downloadResume(id: string): Promise<{ ok: boolean }>
  downloadRetry(id: string): Promise<{ ok: boolean }>
  downloadCancel(id: string): Promise<{ ok: boolean }>
  downloadArchive(id: string): Promise<{ ok: boolean }>
  downloadRestore(id: string): Promise<{ ok: boolean }>
  downloadControlAll(command: 'pauseAll' | 'resumeAll'): Promise<{ ok: boolean }>
  downloadRemoveBatch(ids: string[]): Promise<{ completed: string[]; failures: Record<string, string> }>
  downloadSettings(): Promise<DownloadSettingsSnapshot>
  saveDownloadSettings(concurrency: number, bySource: boolean, mergeVerify: boolean): Promise<DownloadSettingsSnapshot>
  mergeStart(mode: 'season' | 'selected', drama: Drama | null, jobIds: string[]): Promise<{ queued: number; replaced: boolean; episodes: number[] }>
  mergeCancel(id: string): Promise<{ ok: boolean }>
  downloadDirectory(): Promise<DownloadDirectoryInfo>
  /** 归档目录扫描（App.ArchiveScan → downloadsvc.ScanArchiveRoot；交付 B/C） */
  archiveScan(): Promise<ArchiveScanResult>
  // ── 搜索联想（bindings_suggest.go App.Suggest；核心应答 items=string[]，红果专属 ≤10 条，app_suggestions.go:10-21）──
  /** 切集预缓冲（bindings_suggest.go App.PreloadEpisode；失败静默） */
  preloadEpisode(drama: Drama, chapter: Chapter, index: number, quality: number): Promise<string>
  /** 资源管理器打开下载目录/文件（bindings_download_windows.go App.OpenDownloadPath） */
  openDownloadPath(path: string): Promise<void>
  suggestions(query: string): Promise<string[]>
  // ── V-UPD 程序内更新（bindings_update.go；事件 update:available / update:progress）──
  /** App.vue mount 懒触发启动自动检查（真实桥首次调用起 goroutine；mock 走 localStorage+定时器） */
  updateStartup(): Promise<{ ok: boolean }>
  /** 手动检查；force=false 时 30s 冷却回缓存（cooldown=true） */
  updateCheck(force: boolean): Promise<UpdateCheckResult>
  /** 当前版本/通道/最近检查/下载状态/设置摘要（UI 轮询面） */
  updateStatus(): Promise<UpdateStatus>
  /** 下载（或续传）tag 的资产；asset 空=默认 exe；进度走事件/mock 定时器 */
  updateDownload(tag: string, asset: string): Promise<{ ok: boolean }>
  updateCancel(): Promise<{ ok: boolean }>
  /** 应用已下载更新：exe→重启替换（restarting），zip→manual 指引 */
  updateApply(): Promise<UpdateApplyResult>
  updateSettingsGet(): Promise<UpdateSettings>
  updateSettingsSet(patch: Partial<UpdateSettings>): Promise<UpdateSettings>
  // ── 青空次元账号（bindings_sorani.go App.SoraniAccount 单绑定，api 层按 command 映射四方法）──
  /** command ""：登录态 {loggedIn,name} */
  soraniStatus(): Promise<SoraniStatus>
  /** command captcha：滑块验证码（图为 base64 data URL，原图 600×360 / 模板 110×360） */
  soraniCaptcha(): Promise<SoraniCaptcha>
  /** command login：accountJSON={identifier,password,captchaId,track}（Go 侧再嵌 account 键） */
  soraniLogin(identifier: string, password: string, captchaId: string, track: SoraniTrack): Promise<SoraniStatus>
  /** command logout：清会话，返回空态 */
  soraniLogout(): Promise<SoraniStatus>
}

/** 核心下载根目录（bindings_download.go DownloadDirectory：core action downloadDirectory + os.Stat） */
export interface DownloadDirectoryInfo {
  directory: string
  exists: boolean
}

/** 归档扫描分组（downloadsvc.ArchiveGroup，ScanArchiveRoot 文件系统为准 + 索引富化） */
export interface ArchiveGroupRow {
  name: string
  /** *.mp4/*.mkv 分集产物计数（=「N 集」） */
  episodes: number
  bytes: number
  /** 最近产物 mtime（unix ms） */
  mtime: number
  hasMerged: boolean
  mergedCount?: number
  mergedBytes?: number
  /** 索引记录里该剧分集带 archived 旗标 */
  archived?: boolean
  /** 剧名来自索引记录；false=文件系统孤儿组 */
  indexed?: boolean
  folders?: string[]
  /** 点开目标（相对 directory，slash 分隔）：最新合并产物或最新分集目录 */
  openPath: string
}

/** App.ArchiveScan 应答；目录不存在 → exists:false + groups:[] */
export interface ArchiveScanResult {
  directory: string
  exists: boolean
  groups: ArchiveGroupRow[]
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

/** 演职员单项（核心 overlay dramaCreators 的 creators[]，逐键对齐
    zhenguojian/sidecar/core_overlay/discovery.go 与 Go 壳层 bindings_cast.go 头注释） */
export interface CreatorEntry {
  id?: string
  userId?: string
  name: string
  avatar?: string
  description?: string
  role?: string
}

/** dramaCreators data 平铺：creators + 账号统计（heat/followers/plays 用于详情头替换 drama 粗估字段） */
export interface CreatorsResult {
  creators?: CreatorEntry[]
  recordNumber?: string
  heat?: string
  followers?: string
  plays?: string
}

/** CreatorProfile data.profile（V2-L）：核心 = 入参 creator 平铺兜底 + 真接口覆盖
    （sidecar/core_overlay/discovery.go discoveryProfile）；fields 可能全缺（探针实测
    不存在 uid 只回传 {id,userId,name}），UI 必须容忍缺省 */
export interface CreatorProfileInfo extends CreatorEntry {
  /** formatHeat 已格式化串（如「35.2万」），非数字 */
  fans?: string
  following?: string
  likes?: string
}

/** CreatorProfile data.tabs[]：discoveryCreatorTabs 形状 {id,name,total,subTabs[]} */
export interface CreatorTabGroup {
  id: number
  name: string
  total?: number
  subTabs?: { id: number; name: string; total?: number }[]
}

export interface CreatorProfileResult {
  profile: CreatorProfileInfo
  tabs?: CreatorTabGroup[]
}

/** CreatorWorks data：items 为标准 nativeDrama 目录形状（封面直接走 covers.resolveCover） */
export interface CreatorWorksSnapshot {
  items: Drama[]
  cursor?: string
  hasMore?: boolean
}

/** 上新日历应答（核心 data 平铺；items 兼容 flat CalendarEntry 与 legacy 嵌套 CalendarItem） */
export interface CalendarPayload {  date?: string
  items?: CalendarItem[]
  /** 核心 App.Calendar 透传字段（provider_hongguo_calendar.hongguoCalendarPage） */
  dates?: string[]
  defaultDate?: string
  hasMore?: boolean
  nextOffset?: number
  sessionId?: string
  fetchedAt?: string
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
  /** 核心侧提示（如代理不可用），原样透传展示 */
  warning?: string
}

/** 下载任务快照行（核心 nativeDownloadJob + Go 侧 speed/eta 补算，对齐 downloadsvc.applyJobsLocked） */
export interface DownloadJobRow {
  id: string
  drama?: Drama
  chapter?: Chapter
  index: number
  quality?: number
  actualQuality?: number
  bitrate?: number
  state: 'queued' | 'downloading' | 'paused' | 'completed' | 'failed' | 'removing'
  archived?: boolean
  folder?: string
  bytes: number
  total: number
  progress: number
  error?: string
  created?: number
  revision?: number
  retryAt?: number
  resolveRetries?: number
  speed?: number
  eta?: number
}

/** 合并条目行（对齐 downloadsvc.MergeEntry） */
export interface MergeEntryRow {
  id: string
  dramaId: string
  title: string
  episodes: number[]
  count: number
  state: 'queued' | 'running' | 'done' | 'failed' | 'canceled'
  stage?: string
  progress: number
  detail?: string
  output?: string
  bytes?: number
}

/** App.DownloadList / downloads:update 事件载荷 */
export interface DownloadSnapshot {
  jobs: DownloadJobRow[]
  merges: MergeEntryRow[]
  status?: string
  mergeVerify?: boolean
}

/** App.DownloadStart entries 单元 */
export interface DownloadEntry {
  chapter: Chapter
  index: number
}

/** App.DownloadSettings：资源设置 + mergeVerify */
export type DownloadSettingsSnapshot = ResourceSettingsSnapshot & { mergeVerify?: boolean }

/** 打包提交指纹（exe 同级 build-info.json 或构建期 VCS 注入） */
export interface BuildInfo {
  revision?: string
  commitTime?: string
  modified?: boolean
  version?: string
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

// ── V-UPD 程序内更新（对应 Go internal/updater JSON 面）──

/** 发布资产（zhenGuoJian-v2.exe / -app.zip 等） */
export interface UpdateAsset {
  name: string
  url: string
  size: number
}

/** 一次 GitHub Releases 检查选出的最新发布 */
export interface UpdateRelease {
  tag: string
  version: string
  prerelease: boolean
  publishedAt: string
  body: string
  assets: UpdateAsset[]
}

/** UpdateCheck 应答：available=false 时看 skippedTag/cooldown/error 区分语义 */
export interface UpdateCheckResult {
  available: boolean
  current: string
  dev: boolean
  channel: 'stable' | 'prerelease'
  skippedTag?: string
  cooldown?: boolean
  checkedAt: number
  error?: string
  release?: UpdateRelease
}

/** 下载任务状态（update:progress 事件同款字段 + dest） */
export interface UpdateDownloadState {
  tag: string
  asset: string
  dest: string
  state: 'idle' | 'downloading' | 'done' | 'error' | 'canceled'
  total: number
  done: number
  pct: number
  speedBps: number
  error?: string
}

/** update:progress 事件 payload */
export interface UpdateProgressEvent {
  tag: string
  asset: string
  total?: number
  done?: number
  pct?: number
  speedBps?: number
  state: 'downloading' | 'done' | 'error' | 'canceled'
  error?: string
}

/** update:available 事件 payload */
export interface UpdateAvailableEvent {
  tag: string
  version: string
  publishedAt?: string
  body?: string
  current?: string
  dev?: boolean
  channel?: string
  assets?: UpdateAsset[]
}

/** Go 侧 update-settings.json（真实桥）；mock 面同构存 localStorage */
export interface UpdateSettings {
  autocheck: boolean
  prerelease: boolean
  mirrorPrefix: string
  ignoredTag: string
  lastCheckAt: number
}

/** UpdateStatus 应答 */
export interface UpdateStatus {
  current: string
  dev: boolean
  channel: 'stable' | 'prerelease'
  checking: boolean
  settings?: UpdateSettings
  lastCheck?: UpdateCheckResult
  download?: UpdateDownloadState
}

/** UpdateApply 应答：restart=已排定重启；manual=zip 手动解压指引 */
export interface UpdateApplyResult {
  kind: 'restart' | 'manual'
  restarting?: boolean
  updateDir?: string
  hint?: string
}

// ── 青空次元账号（协议只读参考 guoapp-src provider_sorani_account.go:84-116；
//    v1 采集语义 zhenguojian/app/zhenguojian/sorani_login.py:138-220）──

/** 轨迹单点：type down|move|up；t=相对 startTime 毫秒（down 恒为 0） */
export interface SoraniTrackPoint {
  x: number
  y: number
  type: 'down' | 'move' | 'up'
  t: number
}

/** 拖动轨迹（v2 语义：按 captcha 应答原图空间归一上报，v1 报显示空间 ≤300px；
    真实上游一般 600×360 / 模板 110×360，但一切以 captcha 应答字段动态计算） */
export interface SoraniTrack {
  bgImageWidth: number
  bgImageHeight: number
  templateImageWidth: number
  templateImageHeight: number
  /** epoch 毫秒（按下时刻） */
  startTime: number
  /** epoch 毫秒（释放时刻，>= startTime） */
  stopTime: number
  /** 2..5000 点（采集上限 4000）；首点恒 {x:0,y:0,type:'down',t:0}，尾点 type='up'；
      x=clamp(round(dx·bgImageWidth/显示宽), 0, bgImageWidth−templateImageWidth) */
  trackList: SoraniTrackPoint[]
}

/** captcha 应答：图为 base64 data URL；尺寸=原图像素 */
export interface SoraniCaptcha {
  id: string
  backgroundImage: string
  templateImage: string
  backgroundWidth: number
  backgroundHeight: number
  templateWidth: number
  templateHeight: number
}

/** status/logout/login 应答 */
export interface SoraniStatus {
  loggedIn: boolean
  name: string
}

/** mock 面 login 入参快照（window.__zgjSoraniLastLogin，headless 断言用） */
export interface SoraniLoginAttempt {
  identifier: string
  password: string
  captchaId: string
  track: SoraniTrack
}

