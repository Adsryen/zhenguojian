/* ════════════════════════════════════════════════════════════════════════════
   mock 层总入口：演示实现 + 测试缝。与真实桥（../bridge）唯一的汇合点是
   ../contract 的 NativeApi 形状；本层被 facade（../api）在
   `isMock ? mockApi : hasBridge ? realApi : mockApi` 处选用。

   ── 「只存在于 mock」字段/行为清单（真实桥没有对应物；前端当契约用之前先查这张表）──
   A. 形状扩展位
      · PlayerSnapshot.eof（快照播完位）：真实后端不发，播完走 player:ended 事件。
      · Drama.adult：18+ 演示源用，真实引擎 items 不带（见 types.ts 注释）。
      · buildInfo 恒 {revision:'mock'}；preloadEpisode 恒返 ''；
        dataPath 恒 '~/.config/ZhenguoJian (mock)'；ping 恒 'pong-mock'。
   B. 入参被 mock 语义忽略（**参数位必须存在**，由 ExactApiImpl 编译期钉住——
      当年正是 cover/resolve/preloadEpisode 这类"签名不收第 N 参"让
      saveWatch 丢 finished 的 bug 在类型层面合法地潜伏；少收=编译红，
      收了不用=以 _ 前缀显式表态，至少不再是静默截断）
      · cover 的 force（fixture 封面直接原样返回）
      · resolve 的 quality / playerOpen 的 quality（无真实画质语义）
      · danmakuAll 的 durationSec（fixtures 铺满 180s）
      · creatorWorks 的 tab/subTab（分页在 fixtures 内部）
      · calendar 的 offset/gender 默认忽略：仅在 window.__zgjCalPaging 置位时走分页版
   C. 行为替代
      · 弹幕铺底 MOCK_DM_COUNT=80 条 + note='mock …'（api.mockDanmakuNote / api.mockLocalNote）
      · observeDrama 不推进 known/newSeason/arrived 旗标（规格只有 Go 单测证明过）
      · watchlist/historyList/看过/预约 落 local.ts + localStorage（真实桥在 Go watchstore）
      · 更新面假 release 表、30s 冷却、进度定时器与 CustomEvent 总线
        （'zgj:update-available'/'zgj:update-progress'）；设置持久化在
        localStorage 'zgj.update.settings'
      · sorani：内存态 + track 形状校验即成功；验证码是假 SVG data URL
      · cover 直接回 /posters/pN.svg 相对路径（<img> 可渲染）
   D. 测试缝（window.__zgj* 一族；真实桥一律不写这些键）
      __zgjCatalogCalls（按 lane 计 catalog 调用次数）· __zgjDetailFail（按 id 注入详情失败）
      __zgjLocalMissing / __zgjLocalOutside（PlayerOpenLocal 的 local_missing/outside_root 注入）
      __zgjLastSaveWatch（saveWatch/saveLocalWatch 最近一次入参快照）
      __zgjCalPaging（日历分页版开关）· __zgjFailDanmakuToggle（弹幕下载开关写失败）
      __zgjSuggestCalls · __zgjOpenPath · __zgjHitBands · __zgjMockEmbed
      __zgj{SoraniCalls,SoraniLastLogin} 与 __zgj.{mockLoginFail,mockCaptchaFail,
      mockStatusFail,mockSoraniNoName,mockCaptchaSize}
      （声明在 ./window.d.ts；各缝的现场注释跟着各自方法走）
   ════════════════════════════════════════════════════════════════════════════ */
import { ApiError, type ExactApiImpl, type NativeApi } from '../contract'
import type { CatalogParams, Chapter, Drama } from '../types'
import {
  MOCK_SOURCES, mockArchiveScan, mockHongguoFilterGroups, mockSourceCategoryGroups, mockCatalog, mockCreatorProfile, mockCreatorWorks, mockDanmakuAll, mockDetail, mockDramaCreators, mockDownloadControl, mockDownloadControlAll, mockDownloadDirectory, mockDownloadList, mockDownloadRemoveBatch, mockDownloadSettings, mockDownloadStart, mockMergeCancel, mockMergeStart, mockMergeStartMulti, mockResolvePlan, mockSaveDownloadSettings, mockCalendarPage, mockCalendarPagePaged,
  mockLibraryRecommendations, mockLibraryPageReset,
  mockRankingBoards, mockRankings,
  mockSuggestions
} from '../fixtures'
import { delay } from './util'
import { mockPlayerApi } from './player'
import { mockUpdateApi } from './update'
import { mockWatchApi } from './watch'
import { mockSoraniApi } from './sorani'

const mockApiImpl = {
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
    // 调用计数缝：用来证明"翻页真的发生过"。
    // 第五十九轮的教训：A5b 第一版把滚动打在 `.pg-viewport` 上，
    // 而真正的触底监听在 `.pg-scroller`（PosterGrid.vue:153-158），
    // 于是 loadMore 一次都没跑，"卡片数没变"就"通过"了去重断言 ——
    // 空判据。有这个计数才能先断言"确实请求过第 2 页"。
    if (typeof window !== 'undefined') {
      const w = window as unknown as { __zgjCatalogCalls?: Record<string, number> }
      w.__zgjCatalogCalls = w.__zgjCatalogCalls || {}
      const lane = String(p.lane || '')
      w.__zgjCatalogCalls[lane] = (w.__zgjCatalogCalls[lane] || 0) + 1
    }
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
  // （force 无真实语义，但参数位保留——见文件头清单 B）
  cover: async (dramaJSON: string, _force: boolean) => {
    try {
      return (JSON.parse(dramaJSON) as Drama).cover ?? ''
    } catch {
      return ''
    }
  },
  resolve: async (drama: Drama, chapter: Chapter, index: number, _quality: number) => {
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
  danmakuAll: async (session: string, _durationSec: number) => {
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
  // ⚠ 第五十四轮：这里原来写死 `mockCalendarPage(date)`，**offset 与 gender 都被丢掉**，
  // 所以分页与频道切换在这台机器上永远走不到（v1 用测试钉住的六条日历分页行为一条都测不了）。
  // 只有设了测试缝 `window.__zgjCalPaging` 才走分页版；默认路径与原来完全一致，
  // 不改变既有 16 条日历断言看到的数据。
  calendar: (date, offset, gender) =>
    (globalThis as any).window && (globalThis as any).window.__zgjCalPaging
      ? mockCalendarPagePaged(date || '', Number(offset) || 0, Number(gender) || 0)
      : Promise.resolve(mockCalendarPage(date || '')),
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
  setDanmakuDownload: async (on) => {
    // 测试缝：`__zgjFailDanmakuToggle` 让它失败，专门验证
    // "写失败 → 勾选框弹回原值 + 明说已回滚"这条分支。
    // 没有缝的话这条分支只能靠真断桥，而它恰恰是最容易写错的一种
    // （失败了却把勾选留在 true，等于界面在骗人）。
    if ((window as any)?.__zgjFailDanmakuToggle) {
      throw new ApiError('mock 保存弹幕开关失败', 'mock_fail')
    }
    // mock 面把开关存进示例态设置表同一个键（真值语义与 Go 一致：默认关）
    const s = mockDownloadSettings()
    mockSaveDownloadSettings(
      s.downloadConcurrency ?? 2, !!s.downloadBySource, s.mergeVerify !== false, !!on)
    return { danmakuDownload: !!on }
  },
  mergeStart: async (mode, drama, jobIds) => mockMergeStart(mode, drama, jobIds),
  mergeStartMulti: async (dramas) => mockMergeStartMulti(dramas),
  mergeCancel: async (id) => mockMergeCancel(id),
  downloadDirectory: async () => mockDownloadDirectory(),
  archiveScan: async () => mockArchiveScan(),
  // mock 无预缓冲语义；四个参数位仍保留（ExactApiImpl，见文件头清单 B）
  preloadEpisode: async (_drama: Drama, _chapter: Chapter, _index: number, _quality: number) => '',
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
  ...mockPlayerApi,
  ...mockWatchApi,
  ...mockUpdateApi,
  ...mockSoraniApi
} satisfies NativeApi

/** mock 面 80 方法。类型 = ExactApiImpl：入参槽位与 NativeApi 契约逐个严格相等，
    少一个参数位（哪怕函数体根本用不到它）编译期即红——机制与边界见 ../contract 底部注释。 */
export const mockApi: ExactApiImpl<NativeApi, typeof mockApiImpl> = mockApiImpl
