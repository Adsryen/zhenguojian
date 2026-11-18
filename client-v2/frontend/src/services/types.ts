/* v1 行协议数据形状（照抄 zhenguojian/sidecar nativeDrama / catalog / sourceListItem） */

/* ── 三层拆分（2026-11-15）──
   NativeApi 与 ApiError 的物理家挪到 ./contract（bridge/mock/facade 三层共用的唯一形状来源）；
   这里保留 re-export，旧 import 路径（'./types'、'@/services/types'）一律继续可用。
   window.__zgj* 测试缝的 declare global 挪到了 ./mock/window.d.ts（环境声明全工程生效）。 */
export { ApiError } from './contract'
export type { NativeApi } from './contract'

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

/** App.DanmakuWindow(session, start, end) / DanmakuAll(session, durationSec) 返回 */
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
  /** R22a：本地成品会话（Go `Snapshot.Local`，service.go 里由 plan.Local 落）。
   *  ⚠ 前端**不能**再用 `!!episode.index` 去推断"会话是否已打开"：
   *  本地文件没有集号（v1 open_local 同样清空选集条），那样会把本地会话读成没打开，
   *  于是 running=false、进度节流落盘整条不触发——这个坑是 `history_local_play_verify` ⑤
   *  实测出来的（等 7 秒，localStorage 里那条记录一个字段都没变）。 */
  local?: boolean
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

/** window.__zgjBounds：embedApi.setBounds 在 mock 面的上报序列。
 *  hiddenWithGeometry = 「show=false 但几何是真实尺寸」的次数 —— 这正是
 *  第六十一轮修的那个前提（隐藏态也要摆位，mpv 才拿得到非 1×1 的 drawable）。 */
export interface BoundsReport {
  x: number
  y: number
  w: number
  h: number
  show: boolean
}

export interface BoundsRecord {
  count: number
  hiddenWithGeometry: number
  /** 第一次上报 = PlayerOpen 之前的起手摆位，mpv 的 drawable 由它决定。
   *  断言只盯 last 会漏掉整个缺陷（反向验证实测：旧行为下 last 照样是真尺寸），
   *  所以 C1 钉的是 first。 */
  first: BoundsReport | null
  last: BoundsReport | null
}

/* window.__zgj* 验证缝声明已挪至 ./mock/window.d.ts（清单见 ./mock/index.ts 头部） */

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

/* NativeApi 已挪至 ./contract.ts（上方 re-export 保留旧路径） */

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
  /** R22a③：轮询载荷带下来的「下载时保存弹幕」当前值（Go payloadLocked）。
   *  带上它，前端在"别处改了开关"后能自我纠正，而不是要重进设置页。 */
  danmakuDownload?: boolean
}

/** App.DownloadStart entries 单元 */
export interface DownloadEntry {
  chapter: Chapter
  index: number
}

/** App.DownloadSettings：资源设置 + mergeVerify */
export type DownloadSettingsSnapshot = ResourceSettingsSnapshot & { mergeVerify?: boolean
  /** R22a③「下载时保存弹幕」（v1 勾选框，默认关）。真值在 Go 的 downloads.state.json，
   *  不在 localStorage 另存一份——抓弹幕发生在下载完成那一刻，页面关掉也得能跑。 */
  danmakuDownload?: boolean }

/** 打包提交指纹（exe 同级 build-info.json 或构建期 VCS 注入） */
export interface BuildInfo {
  revision?: string
  commitTime?: string
  modified?: boolean
  version?: string
}

/* ApiError 已挪至 ./contract.ts；本文件顶部 re-export 保旧路径 */

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
  /** Electron 安装形态的推荐资产（25MB 主程序包增量）；Wails/dev 无此键 */
  recommendedAsset?: string
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

