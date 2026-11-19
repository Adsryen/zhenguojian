/* 桥接层：真实 Wails 桥的一切——window.go 探测、callGo/callGoRaw、错误码约定、
   realApi（80 方法）、内嵌绑定的真桥面（embedBridge）。
   本文件**不含**任何 mock/演示/测试缝逻辑；mock 面见 ./mock，对外形状见 ./api（facade）。
   入参完整性：realApi 同样过 ExactApiImpl 编译期门（机制说明在 ./contract）。 */
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
  UpdateCheckResult,
  UpdateSettings,
  UpdateStatus,
  WatchRow
} from './types'
import { ApiError, clampOpacityPercent } from './contract'
import type { EmbedSetStatus, AttachResult, EmbedHostDebug, EmbedDiagInfo, ExactApiImpl } from './contract'
import { webBridgeEnabled, whenWsReady, wsCall } from './wsBridge'

/** 真实桥探测：exe 内 window.go 必在；纯浏览器/WebView2 异常时自动降级 local 面，
   避免整页 bridge_missing 死锁（降级会显示顶部提示横幅）。 */
export const hasBridge =
  typeof window !== 'undefined' &&
  !!((window as any).go?.main?.App)

function wailsApp(): Record<string, (...args: any[]) => Promise<string>> {
  const app = window.go?.main?.App
  if (!app) {
    throw new ApiError('未检测到 Wails 原生桥（浏览器直开需 VITE_MOCK=1 或 wails dev）', 'bridge_missing')
  }
  return app
}

/* ── IPC 账本（第九十七轮 alpha.42）────────────────────────────────────────

   alpha.41 真机指纹：attach/diag/Ping（0 参数）全到达、setBounds（5 参数）
   全没到且 promise 全未 settle。要再定案一层——「丢消息是 setBounds 专属
   还是所有带参调用都丢」——前端必须留一份**每方法**的发送/结算/错误账。
   callGoRaw 是全部 Go 调用的唯一咽喉（realApi 80 方法 + embedBridge 全走它），
   在这里记账覆盖面就是 100%，且零 mock 依赖：真桥/挂起/被拒都会计。
   只统计进入 callGoRaw 的调用（mock 面在 facade 就分流了，不经过这里——
   账本读数是「发给真桥的量」，与 diag 的 arrived 对撞正合适）。
   挂起的调用只算 sent 不算 settled——sent-settled 差值就是「在飞/挂起」数，
   这是复制诊断里最有用的一行。 */
type IpcLedgerEntry = { sent: number; settled: number; err: number }
function ipcLedger(): Record<string, IpcLedgerEntry> {
  const w = (globalThis as any).window
  if (w && typeof w === 'object') {
    if (!w.__zgjIpcLedger) w.__zgjIpcLedger = {}
    return w.__zgjIpcLedger as Record<string, IpcLedgerEntry>
  }
  return {} // node/vitest：无 window 时记在空对象上（单测会直接给假 window）
}
function ipcBump(method: string, field: 'sent' | 'settled' | 'err'): void {
  const e = (ipcLedger()[method] ||= { sent: 0, settled: 0, err: 0 })
  e[field]++
}
/** 复制诊断的 ipc= 行：按 sent 降序取前 15，挂起数（sent-settled）>0 的排最前。 */
export function ipcLedgerSummary(): string {
  const rows = Object.entries(ipcLedger())
    .map(([m, e]) => ({ m, ...e, pending: e.sent - e.settled }))
    .filter((e) => e.sent > 0)
    .sort((a, b) => (b.pending - a.pending) || (b.sent - a.sent))
    .slice(0, 15)
    .map((e) => `${e.m}:${e.sent}/${e.settled}${e.err ? `e${e.err}` : ''}${e.pending ? `P${e.pending}` : ''}`)
  return rows.join(' ') || 'empty'
}

/** Go 侧 bound 方法调用：返回原始字符串（Ping/DataPath 这类非 JSON 值直接用）。
    错误码约定（唯一剥码处）：Go 侧以 `message [code]` 结尾抛错，这里剥出 code 造 ApiError。 */
export async function callGoRaw(method: string, ...args: any[]): Promise<string> {
  ipcBump(method, 'sent')
  try {
    const raw = await callGoRawInner(method, ...args)
    ipcBump(method, 'settled')
    return raw
  } catch (err) {
    ipcBump(method, 'settled')
    ipcBump(method, 'err')
    throw err
  }
}

async function callGoRawInner(method: string, ...args: any[]): Promise<string> {
  // v3 容器形态：无桌面桥 + http(s) 页面 → 走 WS 桥。协议信封与 stdio 同构，
  // wsCall 返回的仍是「Go 侧原始字符串」；错误字符串同样带 `[code]` 尾，
  // 剥码逻辑与 window.go 分支逐行一致（唯一剥码处的两段）。
  if (webBridgeEnabled()) {
    const ok = await whenWsReady()
    if (!ok) {
      throw new ApiError('WS 桥未连接（容器模式）', 'bridge_missing')
    }
    try {
      const raw = await wsCall(method, ...args)
      return typeof raw === 'string' ? raw : JSON.stringify(raw)
    } catch (err: any) {
      const message = typeof err === 'string' ? err : err?.message ?? String(err)
      const m = message.match(/\[([a-z_]+)\]\s*$/)
      throw new ApiError(m ? message.slice(0, m.index).replace(/\s+$/, '') : message, m ? m[1] : '')
    }
  }
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
    // slice(0, m.index) 会留在分隔空格（`源被屏蔽 [code]` → `源被屏蔽 `）；
    // 尾巴空格是 code 的分隔符不是正文，剥净（第九十五轮 vitest P0 抓出）。
    throw new ApiError(m ? message.slice(0, m.index).replace(/\s+$/, '') : message, m ? m[1] : '')
  }
}

/** 同上 + JSON 解析（Go 侧 marshalJSON 的对象/数组返回） */
export async function callGo<T>(method: string, ...args: any[]): Promise<T> {
  const raw = await callGoRaw(method, ...args)
  if (raw === '' || raw === undefined) return undefined as unknown as T
  return JSON.parse(raw) as T
}

const realApiImpl = {
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
  danmakuAll: (session: string, durationSec: number) =>
    callGo<DanmakuWindowResult>('DanmakuAll', session, durationSec),
  watchlist: () => callGo<WatchRow[]>('Watchlist'),
  historyList: () => callGo<HistoryRow[]>('HistoryList'),
  toggleFollow: (drama) => callGo<{ followed: boolean }>('ToggleFollow', JSON.stringify(drama)),
  reserve: (drama) => callGo<{ reserved: boolean }>('Reserve', JSON.stringify(drama)),
  cancelReservation: (id) => callGo<{ ok: boolean }>('CancelReservation', id),
  removeFollow: (id) => callGo<{ ok: boolean }>('RemoveFollow', id),
  saveWatch: (drama, ep, pos, dur, fin) =>
    callGo<{ ok: boolean }>('SaveWatch', JSON.stringify(drama), ep, pos, dur, fin),
  playerOpenLocal: (path, resumeSeconds) =>
    callGo<PlayerSnapshot>('PlayerOpenLocal', path, resumeSeconds),
  saveLocalWatch: (drama, path, pos, dur, fin) =>
    callGo<{ ok: boolean }>('SaveLocalWatch', JSON.stringify(drama), path, pos, dur, fin),
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
  setDanmakuDownload: (on) =>
    callGo<{ danmakuDownload: boolean }>('DownloadSetDanmakuDownload', on),
  mergeStartMulti: (dramas) =>
    callGo<{ queued: number; replaced?: number; skipped?: number; outputs: string[] }>(
      'MergeStartMulti', JSON.stringify(dramas ?? [])),
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
} satisfies NativeApi

/** 真实桥面（80 方法）。类型 = ExactApiImpl：与契约入参槽位不一致时编译期即红。 */
export const realApi: ExactApiImpl<NativeApi, typeof realApiImpl> = realApiImpl

/* ── V2-4 内嵌绑定的真桥面（App.{PlayerAttach,PlayerSetBounds,PlayerHitBands,PlayerDetach}）。
   mock/无桥的降级分支在 ./mock/embed 与 facade（./api），时序契约见原注释：
   PlayerAttach → PlayerOpen（spawn 前 SetWid 生效）→ 几何/状态变化 PlayerSetBounds
   （物理像素，零尺寸=隐藏）→ 离开 PlayerDetach。 */
export const embedBridge = {
  attach: async (): Promise<AttachResult> => {
    try {
      const r = (await callGo<AttachResult>('PlayerAttach')) ?? { embedded: false }
      // 控制台留现场：排错时用户开 Ctrl+Shift+F12 就能复制给我，
      // 不需要我猜"是不是父窗选错 / 子窗从没被摆过"。
      console.info('[embed] attach', JSON.stringify(r))
      return r
    } catch (err: any) {
      const r = { embedded: false, reason: String(err?.message ?? err) }
      console.info('[embed] attach 失败', JSON.stringify(r))
      return r
    }
  },
  setBounds: async (x: number, y: number, w: number, h: number, show: boolean): Promise<EmbedSetStatus> => {
    /* 第六十三轮：这里原来是 `catch {}`，几何上报**失败得毫无痕迹**。
       真机徽标一直显示「最近上报 0×0」，而我无法区分「没送到 Go」和
       「送到时就是 0」——这两种成因的修法完全相反。异常不再吞掉，
       而是带回 ok=false + error，由徽标原样显示。
       仍然不 throw：上报失败不该打断播放 UI。 */
    /* 第九十六轮看门狗：await 若 8s 内不返回=IPC 挂起。alpha.40 的
       「上报异常=none」正是挂起时的**谎言**——旧代码卡在这个 await 上不返回，
       上层 catch 不触发、error 不置位，徽标于是显示 none。现在 race 一个超时：
       超时即回 ok=false + error='pending>8s'，让「挂起」变成界面上可见的事实。
       原 promise 仍在后台挂着，无所谓——诊断目标是把挂起显形，不是救活它。
       看门狗时长可用 window.__zgjBoundsWdMs 覆盖（vitest 单测把它压到几百 ms）。 */
    const wdMs = (() => {
      if (typeof window !== 'undefined') {
        const o = (window as unknown as { __zgjBoundsWdMs?: number }).__zgjBoundsWdMs
        if (typeof o === 'number' && o > 0) return o
      }
      return 8000
    })()
    let wdTimer: ReturnType<typeof setTimeout> | undefined
    try {
      const wd = new Promise<never>((_, rej) => {
        wdTimer = setTimeout(() => rej(new Error('pending>8s')), wdMs)
      })
      const r = await Promise.race([
        callGo<{ ok: boolean; host?: EmbedHostDebug }>(
          'PlayerSetBounds', x, y, w, h, show),
        wd
      ])
      // 只报"有尺寸的那几次"，否则每次几何变化都打一条会把控制台刷满
      if (w > 1 && h > 1) {
        console.info('[embed] setBounds', x, y, w, h, 'show=' + show, JSON.stringify(r?.host ?? null))
      }
      if (!r?.ok) return { ok: false, host: r?.host, error: 'ok=false' }
      return { ok: true, host: r?.host }
    } catch (err) {
      const e = err as Error
      return { ok: false, error: String(e?.message ?? err ?? 'unknown') }
    } finally {
      if (wdTimer !== undefined) clearTimeout(wdTimer)
    }
  },
  /* 覆盖层穿透带（App.PlayerHitBands）：top/bottom=HTML 覆盖层与视频矩形
     的实际重叠高度（物理像素），0=该侧不禁。 */
  playerHitBands: async (top: number, bottom: number): Promise<void> => {
    try {
      await callGo('PlayerHitBands', top, bottom)
    } catch {
      /* 同 setBounds：上报失败不干扰播放 UI */
    }
  },
  detach: async (): Promise<void> => {
    try {
      await callGo('PlayerDetach')
    } catch {
      /* 同上 */
    }
  },
  /* 逃生门：内嵌黑屏时退回 mpv 独立窗口（App.PlayerForceWindow）。
     返回后调用方要重开当前集，mpv 才会自己建顶层窗。 */
  forceWindow: async (): Promise<boolean> => {
    // ⚠ 这里**不判 isMock**：按本文件分层，mock/无桥的分支在 facade（api.ts）
    //   与 mock/embed.ts，桥面只管调桥。我第一版写了 `if (isMock || !hasBridge)`，
    //   而 `isMock` 在本文件并不存在——typecheck 直接拒（`Cannot find name 'isMock'`）。
    //   凭空标识符这类错本机现在能查了（scripts/go-local.sh + npm run typecheck），
    //   不该再留给 CI。
    try {
      await callGo('PlayerForceWindow')
      return true
    } catch (err: any) {
      console.info('[embed] forceWindow 失败', String(err?.message ?? err))
      return false
    }
  },
  /* 诊断面（App.EmbedDiag）：exe 身份自证 + app.log 的 setBounds 到达计数。
     arrived 与前端自己量的「前端量得#N」并排进复制诊断，两侧读数对撞即可判
     「IPC 没到 Go」还是「到了 Go 没执行」（alpha.40 至今没分开的两件事）。
     失败不 throw：诊断不可用不该打断复制诊断的其余字段。 */
  diag: async (): Promise<EmbedDiagInfo> => {
    try {
      const r = await callGo<EmbedDiagInfo>('EmbedDiag')
      return r ?? { version: '', revision: '', pid: 0, arrived: 0, logDir: '', logFail: '' }
    } catch (err: any) {
      console.info('[embed] diag 失败', String(err?.message ?? err))
      return { version: '', revision: '', pid: 0, arrived: 0, logDir: '', logFail: 'unavailable' }
    }
  }
}
