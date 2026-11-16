/* 契约层：bridge（真实桥）与 mock（演示面）共用的**唯一形状来源**。
   三层拆分的动机（2026-11-15，见 services/README.md）：
   - 过去 api.ts 一个文件同时装 realApi / mockApi / 测试缝，"mock 比真实更宽松"
     只能在发版试跑时肉眼抓（saveWatch 丢第 5 参 finished、calendar 丢 offset/gender、
     cover 只接 1 参……全是同一族 bug）。
   - 本文件把"每个方法收哪些入参、对应 Go 侧哪条绑定"变成**编译期门**：
     任何一层的实现想少一个参数位，typecheck 直接红。

   ⚠ NativeApi / ApiError 的物理家在这里；types.ts 保留 re-export，
   所有旧 import 路径（'./types'、'@/services/types'）继续可用。 */
import type {
  ArchiveScanResult,
  BrowseFiltersResult,
  BuildInfo,
  CalendarPayload,
  CatalogParams,
  CatalogResult,
  Chapter,
  CreatorEntry,
  CreatorProfileResult,
  CreatorWorksSnapshot,
  CreatorsResult,
  DanmakuWindowResult,
  DetailResult,
  DownloadDirectoryInfo,
  DownloadEntry,
  DownloadSettingsSnapshot,
  DownloadSnapshot,
  Drama,
  HistoryRow,
  PlayerSnapshot,
  RankingBoard,
  RankingsResult,
  ResolvePlan,
  ResourceSettingsSnapshot,
  SoraniCaptcha,
  SoraniStatus,
  SoraniTrack,
  SourceListItem,
  UpdateApplyResult,
  UpdateCheckResult,
  UpdateSettings,
  UpdateStatus,
  WatchRow
} from './types'

/** 带协议 code 的错误（discovery_unavailable / source_blocked 等）。
    两层都抛它：bridge 从 Go 错误尾巴剥出 code，mock 按 Go 同形 code 造它。 */
export class ApiError extends Error {
  code: string
  retryAt?: number
  constructor(message: string, code = '', retryAt?: number) {
    super(message)
    this.code = code
    this.retryAt = retryAt
  }
}

/** v1 tools.danmaku_opacity：20–100 的百分比，非法值回落 100（不透明）。
    量纲约定属于契约：真实桥侧再由 bridge 换算成 0.2–1.0 交给 PlayerDanmakuOpacity。 */
export function clampOpacityPercent(v: number): number {
  const n = Number(v)
  if (!Number.isFinite(n)) return 100
  return Math.min(100, Math.max(20, Math.round(n)))
}

/* ── 内嵌绑定应答形状（App.PlayerAttach / PlayerSetBounds 的 JSON；不进 NativeApi 面：
   这是播放页生命周期细节，桥缺失/mock/非 Windows 一律降级 no-op，见 facade embedApi）── */
/** 内嵌现场（Go 侧 mpv.HostDebug）。真机「音频有、画面黑」只能靠它定位。 */
export interface EmbedHostDebug {
  supported?: boolean
  /* ApplyBounds 真正被调用的次数（Go 侧计数）。
     「最近上报 0×0」有两种相反的成因：从没调用过 vs 调用时量到 0。
     光看数值分不开，这一眼就能分开。 */
  applyCount?: number
  parentHwnd?: string
  parentClass?: string
  childHwnd?: string
  childVisible?: boolean
  childLeft?: number
  childTop?: number
  childWidth?: number
  childHeight?: number
  lastInX?: number
  lastInY?: number
  lastInW?: number
  lastInH?: number
  lastOutVisible?: boolean
  lastOutX?: number
  lastOutY?: number
  lastOutW?: number
  lastOutH?: number
  /* 「这个像素上谁在最上面」直读（判读表最后一格不再靠人眼看覆盖层）。
     非 Windows / 桥读失败时全为 undefined，徽标显示 ?。 */
  probeX?: number
  probeY?: number
  topAtCenterHwnd?: string
  topAtCenterClass?: string
  topIsChild?: boolean
  /* 自检位：false = 探测点不在返回窗口的矩形里，说明这条读数不可信
     （POINT 按值打包错了不会报错，只会返回另一个窗口的句柄）。 */
  topContainsProbe?: boolean
}

export interface AttachResult {
  embedded: boolean
  reason?: string
  host?: EmbedHostDebug
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
  /** R22a：播一个下载根下的成品文件（Go `PlayerOpenLocal`，路径校验在核心侧）。
   *  错误尾巴 `[local_missing]` / `[outside_root]` 由 callGoRaw 剥成 ApiError.code。 */
  playerOpenLocal(path: string, resumeSeconds: number): Promise<PlayerSnapshot>
  /** R22a 第①件：本地文件的进度写入（Go `SaveLocalWatch`；v1 save_local_watch 同义）。 */
  saveLocalWatch(drama: Drama, path: string, position: number, duration: number, finished: boolean): Promise<{ ok: boolean }>
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
  /** 记住「下一季是另一部剧」的入口（v1 mark_next_season）。
   *  marked=false 表示旧季不在追剧表——照 v1 不谎报成功。 */
  setNextSeason(dramaID: string, nextDrama: Drama): Promise<{ marked: boolean }>
  /** R23：把"最新剧数据"交给核心观察器（v1 watchstore.observe_drama）。
   *  observed=false 表示这条根本不在追剧表（v1 对未追的剧直接 return，连 emit 都不发）。 */
  observeDrama(drama: Drama): Promise<{ observed: boolean; changed: boolean; row: WatchRow | null }>
  // durationSec：核心要求 durationMs>0（app_danmaku_all.go:181），0 会被拒
  danmakuAll(session: string, durationSec: number): Promise<DanmakuWindowResult>
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
  /** R22a③：单独设「下载时保存弹幕」，不连带改并发（v1 那个勾选框就是即点即存）。 */
  setDanmakuDownload(on: boolean): Promise<{ danmakuDownload: boolean }>
  mergeStart(mode: 'season' | 'selected', drama: Drama | null, jobIds: string[]): Promise<{ queued: number; replaced: boolean; episodes: number[] }>
  /** R22b「按季合并选中」（v1 start_merge_multi → App.MergeStartMulti）。
   *  勾选的剧由前端给出；季序、命名、`不足 2 集丢掉`、`运行中的季跳过` 全在 Go 算，
   *  规则表见 internal/downloadsvc/multi.go 与它的单测。 */
  mergeStartMulti(dramas: Drama[]): Promise<{ queued: number; replaced?: number; skipped?: number; outputs: string[] }>
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

/* ────────────────────────────────────────────────────────────────────────
   Go 侧真实签名清单（2026-11-15 从 client-v2 的 app.go / bindings_*.go 逐条抄录）。
   `Record<keyof NativeApi, string>` 让这张表**不可能欠账**：NativeApi 新增方法而这里
   没登记，typecheck 直接红。用途：审"前端少传/多传参数"时的对照基准——
   例如 saveWatch 五参对应 `App.SaveWatch(dramaJSON string, episode, position,
   duration float64, finished bool)`（bindings_watchstore.go:296），
   mock 面曾经只接四参把 finished 默默丢掉，就是这张表本该早就拦住的那类事。
   ──────────────────────────────────────────────────────────────────────── */
export const GO_BINDINGS: Record<keyof NativeApi, string> = {
  ping: 'App.Ping() string (raw, not JSON; app.go:182)',
  dataPath: 'App.DataPath() (string, error) (raw, not JSON)',
  listSources: 'App.ListSources() → marshalJSON([]SourceListItem)',
  setSourceEnabled: 'App.SetSourceEnabled(slug string, enabled bool)',
  catalog: 'App.Catalog(source, query, category string, page int, lane string, force bool)',
  recommendations: 'App.Recommendations(category string, force, more bool)',
  recommendationPageReset: 'App.RecommendationPageReset()',
  rankingBoards: 'App.RankingBoards() -> {items:[...]} (shell strips items)',
  rankings: 'App.Rankings(board string, page int, force bool)',
  detail: 'App.Detail(dramaJSON string)',
  cover: 'App.Cover(dramaJSON string, force bool) -> data URL raw string (callGoRaw)',
  browseFilters: 'App.BrowseFilters(source, category string) -- frontend always passes "" as 2nd arg',
  resolve: 'App.Resolve(dramaJSON, chapterJSON string, index, quality int)',
  playerOpen: 'App.PlayerOpen(dramaJSON, chapterJSON string, index int, quality string)',
  playerOpenLocal: 'App.PlayerOpenLocal(path string, resumeSeconds float64)',
  saveLocalWatch: 'App.SaveLocalWatch(dramaJSON, path string, position, duration float64, finished bool)',
  playerPlay: 'App.PlayerPlay()',
  pauseVideo: 'App.PlayerPause()',
  seekVideo: 'App.PlayerSeek(ms float64)',
  setSpeed: 'App.PlayerSpeed(v float64)',
  setVolume: 'App.PlayerVolume(v int) -- frontend sends Math.round(v)',
  closeVideo: 'App.PlayerClosePlayer()',
  playerState: 'App.PlayerState() -> Snapshot (NO eof field; real end-of-play goes via player:ended event)',
  setDanmaku: 'App.PlayerDanmaku(on bool)',
  setDanmakuOpacity: 'App.PlayerDanmakuOpacity(v float64) -- 0.2-1.0, converted from percent by shell layer',
  setNextSeason: 'App.SetNextSeason(dramaID, nextDramaJSON string)',
  observeDrama: 'App.ObserveDrama(dramaJSON string)',
  danmakuAll: 'App.DanmakuAll(session string, durationSec float64)',
  watchlist: 'App.Watchlist()',
  historyList: 'App.HistoryList()',
  toggleFollow: 'App.ToggleFollow(dramaJSON string)',
  reserve: 'App.Reserve(dramaJSON string)',
  cancelReservation: 'App.CancelReservation(dramaID string)',
  removeFollow: 'App.RemoveFollow(dramaID string)',
  saveWatch: 'App.SaveWatch(dramaJSON string, episode, position, duration float64, finished bool) -- 5 params; historical note: mock once dropped the 5th (finished)',
  removeHistory: 'App.RemoveHistory(dramaID string)',
  clearHistory: 'App.ClearHistory()',
  seenList: 'App.SeenList()',
  toggleSeen: 'App.ToggleSeen(dramaID string)',
  importSeen: 'App.ImportSeen(entriesJSON string)',
  calendar: 'App.Calendar(date string, offset float64, gender float64)',
  dramaCreators: 'App.DramaCreators(dramaJSON string)',
  creatorProfile: 'App.CreatorProfile(creatorJSON string)',
  creatorWorks: 'App.CreatorWorks(creatorJSON string, tab, subTab int, cursor, sort string)',
  resourceSettings: 'App.GetResourceSettings()',
  saveResourceSettings: 'App.SaveResourceSettings(settingsJSON string)',
  buildInfo: 'App.GetBuildInfo() string',
  updateStartup: 'App.UpdateStartup() string',
  updateCheck: 'App.UpdateCheck(force bool)',
  updateStatus: 'App.UpdateStatus()',
  updateDownload: 'App.UpdateDownload(tag, asset string)',
  updateCancel: 'App.UpdateCancel()',
  updateApply: 'App.UpdateApply()',
  updateSettingsGet: 'App.UpdateSettingsGet()',
  updateSettingsSet: 'App.UpdateSettingsSet(patchJSON string)',
  downloadList: 'App.DownloadList()',
  downloadStart: 'App.DownloadStart(dramaJSON, entriesJSON string, quality float64)',
  downloadPause: 'App.DownloadPause(jobID string)',
  downloadResume: 'App.DownloadResume(jobID string)',
  downloadRetry: 'App.DownloadRetry(jobID string)',
  downloadCancel: 'App.DownloadCancel(jobID string)',
  downloadArchive: 'App.DownloadArchive(jobID string)',
  downloadRestore: 'App.DownloadRestore(jobID string)',
  downloadControlAll: 'App.DownloadControlAll(command string)',
  downloadRemoveBatch: 'App.DownloadRemoveBatch(jobIDsJSON string)',
  downloadSettings: 'App.DownloadSettings()',
  saveDownloadSettings: 'App.SaveDownloadSettings(concurrency float64, bySource, mergeVerify bool)',
  setDanmakuDownload: 'App.DownloadSetDanmakuDownload(on bool)',
  mergeStart: 'App.MergeStart(mode, dramaJSON, jobIDsJSON string) -- dramaJSON is \"\" when drama is null',
  mergeStartMulti: 'App.MergeStartMulti(dramasJSON string)',
  mergeCancel: 'App.MergeCancel(mergeID string)',
  downloadDirectory: 'App.DownloadDirectory()',
  archiveScan: 'App.ArchiveScan()',
  preloadEpisode: 'App.PreloadEpisode(dramaJSON, chapterJSON string, index, quality int)',
  openDownloadPath: 'App.OpenDownloadPath(path string) error',
  suggestions: 'App.Suggest(query string) → {items:string[]}',
  soraniStatus: 'App.SoraniAccount("", "{}")',
  soraniCaptcha: 'App.SoraniAccount("captcha", "{}")',
  soraniLogin: 'App.SoraniAccount("login", accountJSON{identifier,password,captchaId,track})',
  soraniLogout: 'App.SoraniAccount("logout", "{}")'
}

/** 内嵌绑定（不在 NativeApi 面里，见 facade embedApi） */
/** embedApi.setBounds 的回执。
   ⚠ 为什么不再返回 void：旧写法在 bridge 里 `catch {}` 把异常**整个吞掉**，
   于是「上报失败」和「上报成功」在前端长得一模一样，徽标只剩 0×0 一个数字，
   我据此连猜了两个 tag。现在把失败原因带回来，由徽标显示。 */
export interface EmbedSetStatus {
  ok: boolean
  host?: EmbedHostDebug
  error?: string
}

/* 内嵌诊断面（App.EmbedDiag，第九十六轮）：exe 身份自证 + app.log 的
   setBounds 到达计数。arrived 与前端自己量的「前端量得#N」并排放在复制
   诊断里，两侧读数对撞即可判「IPC 没到 Go」还是「到了 Go 没执行」。 */
export interface EmbedDiagInfo {
  version: string
  revision: string
  pid: number
  /** Go 实际收到的 PlayerSetBounds 次数（0=从未到达） */
  arrived: number
  logDir: string
  logFail: string
}

export const GO_BINDINGS_EMBED: Record<'attach' | 'setBounds' | 'playerHitBands' | 'detach' | 'diag', string> = {
  attach: 'App.PlayerAttach() → AttachResult{embedded,reason?,host?}',
  setBounds: 'App.PlayerSetBounds(x, y, w, h int, show bool) -- physical pixels; geometry always placed, show only toggles visibility',
  playerHitBands: 'App.PlayerHitBands(top, bottom float64) error',
  detach: 'App.PlayerDetach()',
  diag: 'App.EmbedDiag() -- {version,revision,pid,arrived,logDir,logFail}; arrived = Go-side setBounds arrival count'
}

/* ────────────────────────────────────────────────────────────────────────
   「入参槽位精确匹配」检查机制（本层存在的理由）

   TS 的函数可赋值性天然容忍**少写参数**：`(a,b,c,d)=>R` 可以赋给
   `(a,b,c,d,e)=>R`。mockApi 当年就是栽在这上面——签名只到第 4 参，
   第 5 个实参 `finished` 在类型层面合法地被丢掉。
   `satisfies NativeApi` 也拦不住它（同一套可赋值性规则）。

   本机制用类型级恒等比较（`(<T>()=>T extends X?1:2) extends (<T>()=>T extends Y?1:2)`）
   把「实现方的参数元组」与「契约的参数元组」做**严格相等**：
     · 少一个参数位 → 该方法的检查类型变成 ContractMismatch 占位 → 赋值处编译错，
       报错点名方法名与 expected/got 两个元组；
     · 可选/必选的差别、参数类型错位同样被抓（恒等比较不像可赋值性那样宽松）。

   抓不到的（诚实边界，详见 README）：
     · 参数**位收了但在函数体里不用**（写成 `_finished` 就通过检查）——
       但至少逼作者看见这个参数并显式表态，静默截断变成显式弃用；
     · 同为 number 的两参互换顺序（position/duration）——类型相等；
       靠上面的 GO_BINDINGS 注释与 labeled tuple 提示兜底；
     · 运行时实参数量由调用方决定，属调用方的 typecheck 范畴，不在本机制内。
   ──────────────────────────────────────────────────────────────────────── */

/** 函数参数的元组类型 */
type ParamsOf<F> = F extends (...args: infer P) => any ? P : never

/** 类型级严格相等（能区分「少一个参数」「可选 vs 必选」，普通可赋值性不能） */
export type TupExact<X, Y> = (<T>() => T extends X ? 1 : 2) extends (<T>() => T extends Y ? 1 : 2) ? true : false

/** 某方法参数表与契约不一致时的占位类型：让编译错误点名是哪个方法 */
interface ContractMismatch<K extends string, Expected, Got> {
  readonly __contractMismatch: `contract param-slot mismatch on method \"${K}\": the implementation signature must list every contract parameter (prefix unused ones with _)`
  readonly expected: Expected
  readonly got: Got
}

/** 实现层缺方法时的占位类型 */
interface ContractMissing<K extends string> {
  readonly __contractMissing: `implementation is missing method \"${K}\"`
}

/** 对实现对象逐方法校验「参数元组 === 契约元组」。
    用法（bridge/mock 两层同构）：
      const impl = { ...方法… } satisfies NativeApi
      export const theApi: ExactApiImpl<NativeApi, typeof impl> = impl
    ⚠ 必须用 `satisfies` 得到 impl 的推断类型，不能先标注成 NativeApi——
      标注会把属性类型拓宽成契约本身，检查就空转了。 */
export type ExactApiImpl<C, Impl> = {
  [K in keyof C]: K extends string
    ? K extends keyof Impl
      ? TupExact<ParamsOf<C[K]>, ParamsOf<Impl[K]>> extends true
        ? Impl[K]
        : ContractMismatch<K, ParamsOf<C[K]>, ParamsOf<Impl[K]>>
      : ContractMissing<K>
    : never
}
