/* mock fixtures —— 手抄自 v1 引擎真实 catalog 形状（hongguo nativeDrama 字段名逐一对应；
   封面为本地占位 SVG，真实 cover 是引擎返回的 CDN 直链）。仅 VITE_MOCK=1 时使用。 */
import type { ArchiveScanResult, CreatorEntry, CreatorProfileResult, CreatorWorksSnapshot, DanmakuItem, DanmakuWindowResult, CalendarEntry, CalendarItem, CalendarPayload, CatalogResult, Chapter, CreatorsResult, DetailResult, Drama, ResolvePlan, SourceListItem } from './types'

export const MOCK_SOURCES: SourceListItem[] = [
  {
    slug: 'hongguo',
    name: '红果',
    displayName: '红果短剧',
    searchKind: 'online',
    adult: false,
    lanes: ['discover', 'library', 'search'],
    seasons: false,
    catalogKinds: ['drama'],
    enabledByDefault: true,
    implemented: true,
    enabled: true
  },
  {
    slug: 'huangguo_ai',
    name: '黄果AI',
    displayName: '黄果AI剧',
    searchKind: 'online',
    adult: false,
    lanes: ['discover', 'library', 'search'],
    seasons: false,
    catalogKinds: ['ai'],
    enabledByDefault: true,
    implemented: true,
    enabled: true
  },
  {
    slug: 'adult_lab',
    name: '成人测试源',
    displayName: '成人测试源',
    searchKind: 'online',
    adult: true,
    lanes: ['discover', 'search'],
    seasons: false,
    catalogKinds: ['drama'],
    enabledByDefault: false,
    implemented: true,
    enabled: true
  },
  {
    slug: 'youzi',
    name: '柚子',
    displayName: '柚子',
    searchKind: 'online',
    adult: true,
    lanes: ['discover', 'search'],
    seasons: false,
    catalogKinds: ['drama'],
    enabledByDefault: false,
    implemented: true,
    // v1 出厂停用；mock 里置 true 便于演示开→关回写（与 18+ 徽章同屏验证）
    enabled: true
  },
  {
    slug: 'tianyuan',
    name: '天源',
    displayName: '天源短剧',
    searchKind: 'none',
    adult: false,
    lanes: [],
    seasons: false,
    catalogKinds: [],
    enabledByDefault: false,
    implemented: false,
    enabled: false
  }
]

const items: Drama[] = [
  {
    metadataSchema: 1,
    id: 'hongguo:7439220582928952383',
    source: 'hongguo',
    sourceId: '7439220582928952383',
    title: '一世之尊',
    description: '少年自微末崛起，踏碎虚空问道长生，示例数据。',
    cover: '/posters/p1.svg',
    episodes: 68,
    category: '玄幻',
    vip: true,
    heat: '1286万',
    views: '2.1亿',
    onlineDate: '2026-08-14',
    tags: ['漫剧', '改编'],
    // v2-D：改用核心真实枚举（ongoing/finished），mock 才能演到 DetailView 的状态本地化；
    // 「连载」等中文描述串仍保留一条，验证助手原样透传
    releaseStatus: 'ongoing'
  },
  {
    metadataSchema: 1,
    id: 'hongguo:7421530981234567890',
    source: 'hongguo',
    sourceId: '7421530981234567890',
    title: '闪婚老公是大佬',
    description: '契约婚姻暗藏身份反转，示例数据。',
    cover: '/posters/p2.svg',
    episodes: 82,
    category: '都市',
    vip: false,
    heat: '942万',
    views: '1.7亿',
    onlineDate: '2026-09-02',
    tags: ['甜宠'],
    releaseStatus: 'finished'
  },
  {
    metadataSchema: 1,
    id: 'hongguo:7418876543210987654',
    source: 'hongguo',
    sourceId: '7418876543210987654',
    title: '山河引',
    description: '游医行走乱世，以医术破局，示例数据。',
    cover: '/posters/p3.svg',
    episodes: 45,
    category: '古装',
    vip: null,
    heat: '655万',
    views: '8900万',
    onlineDate: '2026-10-11',
    tags: ['权谋'],
    releaseStatus: '更新至40集'
  },
  {
    metadataSchema: 1,
    id: 'hongguo:7399111222333444555',
    source: 'hongguo',
    sourceId: '7399111222333444555',
    title: '重生之我在短剧当编剧',
    description: 'meta 爽剧，编剧亲自下场，示例数据。',
    cover: '',
    episodes: 30,
    category: '都市',
    vip: false,
    heat: '411万',
    onlineDate: '2026-11-01',
    tags: ['穿越'],
    releaseStatus: '连载'
  },
  {
    metadataSchema: 1,
    id: 'adult_lab:7500555666777888999',
    source: 'adult_lab',
    sourceId: '7500555666777888999',
    title: '午夜档案',
    description: '成人向悬疑单元剧（演示 18+ badge 槽）。',
    cover: '/posters/p4.svg',
    episodes: 12,
    category: '悬疑',
    vip: true,
    heat: '77万',
    onlineDate: '2026-10-30',
    tags: ['18+'],
    releaseStatus: '已完结',
    adult: true
  }
]

/* ---------------------------------------------------------------- V2-2 详情页 */

/** 红果形状：35 集，VIP 混合（第 1-3 免费引流、此后隔 3 集 1 集 VIP + 最后 10 集全 VIP） */
export function mockChapters(total = 35): Chapter[] {
  const out: Chapter[] = []
  for (let i = 1; i <= total; i++) {
    const vip = i > 3 && (i % 4 === 0 || i > total - 10)
    out.push({
      currentEpisode: i,
      title: `第${i}集`,
      duration: 132 + (i % 7) * 9,
      vip
      // 红果真实返回就是这种稀疏形状：无逐集 id/url，播放地址走 Resolve
    })
  }
  return out
}

/** Detail 的 mock：{drama, chapters, warning} —— 与 Go 端 marshalJSON 形状一致 */
export function mockDetail(drama: Drama): DetailResult {
  const base = items.find((it) => drama.id.startsWith(it.id)) ?? drama
  return {
    drama: {
      ...base,
      description:
        base.description ||
        `${base.title}：mock 详情。少年偶得上古传承，于微末中崛起，一路打脸逆袭，揭开身世之谜。（示例数据）`,
      tags: base.tags?.length ? base.tags : ['逆袭', '爽剧'],
      heat: base.heat || '518万',
      views: base.views || '4321万',
      onlineDate: base.onlineDate || '2026-10-01',
      episodes: 35
    },
    chapters: mockChapters(35),
    warning: 'VITE_MOCK 示例数据，非真实引擎结果'
  }
}

/** 演职员条 mock（v2-D）：对齐核心 overlay dramaCreators 的 data 形状
    （zhenguojian/sidecar/core_overlay/discovery.go:41-54）；非 hongguo 剧集返回空列表（与真实通道一致） */
export function mockDramaCreators(drama: Drama): CreatorsResult {
  if (!drama.id.startsWith('hongguo:')) return {}
  return {
    creators: [
      { id: '74001', userId: 'u_dao', name: '示例·张导', avatar: '/posters/p1.svg', role: '导演', description: '导演｜代表作示例 A' },
      { id: '74002', userId: 'u_nan', name: '李忆', avatar: '/posters/p2.svg', role: '主演', description: '饰 男主（示例数据）' },
      { id: '74003', userId: 'u_nv', name: '王十七', role: '主演', description: '饰 女主（示例数据）' }
    ],
    recordNumber: '示例（备案号 V10045）',
    heat: '1286万',
    followers: '35.2万',
    plays: '2.1亿'
  }
}

/* ------------------------------------------------ V2-L 创作者页 mock（示例数据） */

/* mockCreatorProfile/mockCreatorWorks —— 形状逐键对齐 discovery.go 实测（探针 m00190：
   profile=入参平铺兜底、tabs 可为空数组；空 tabs 时核心 works 报「该演职员暂无公开作品」）。
   统计串照 formatHeat 口径（「35.2万」）。u_nv 故意 tabs=[] 用来验证空分类态。 */
const MOCK_CREATOR_STATS: Record<string, { fans: string; following: string; likes: string }> = {
  u_dao: { fans: '35.2万', following: '86', likes: '128.6万' },
  u_nan: { fans: '9.8万', following: '412', likes: '45.3万' },
  u_nv: { fans: '1.2万', following: '35', likes: '6.6万' }
}

/** 钩子：headless 验证计数 works 请求次数（排序切换重拉断言）；失败开关断言红条 */
function creatorHook(name: string): void {
  if (typeof window === 'undefined') return
  ;(window as any)[name] = (((window as any)[name] as number) ?? 0) + 1
}

export function mockCreatorProfile(creator: CreatorEntry): CreatorProfileResult {
  creatorHook('__zgjCreatorProfileCalls')
  // 失败开关用 localStorage（跨导航存活，headless 验证断言红条/重试用）
  try {
    if (localStorage.getItem('zgj.creatorFail') === '1') {
      throw new Error('模拟创作者信息加载失败（示例数据）')
    }
  } catch (e: any) {
    if (String(e?.message ?? '').includes('示例数据')) throw e
  }
  const uid = creator.userId ?? creator.id ?? ''
  const stats = MOCK_CREATOR_STATS[uid]
  const tabs =
    uid === 'u_dao'
      ? [
          {
            id: 8,
            name: '作品',
            total: 12,
            subTabs: [
              { id: 100, name: '连续剧', total: 9 },
              { id: 101, name: '短片', total: 3 }
            ]
          }
        ]
      : uid === 'u_nan'
        ? [{ id: 8, name: '作品', total: 5, subTabs: [{ id: 100, name: '连续剧', total: 5 }] }]
        : []
  return {
    profile: { ...creator, ...(stats ?? {}) },
    tabs
  }
}

/** works 分页池：page1 = 池前 8 条（cursor 'c2'），page2 = 41-12 条（尾）。
    sort=hot_desc 时按热度串翻转次序，模拟服务端排序。 */
export function mockCreatorWorks(creator: CreatorEntry, cursor: string, sort: string): CreatorWorksSnapshot {
  creatorHook('__zgjCreatorWorksCalls')
  const uid = creator.userId ?? creator.id ?? ''
  if (uid === 'u_nv') throw new Error('该演职员暂无公开作品（示例数据）')
  let pool = items.filter((d) => d.id.startsWith('hongguo:'))
  if (sort === 'hot_desc') pool = [...pool].reverse()
  // 池仅 4 条：第二页派生副本（id 加 -p2 后缀、标题标注），保证「加载更多」
  // 追加 + 按 id 去重两条断言都有真实可观察行为
  const page =
    cursor === 'mock-c2'
      ? pool.map((d) => ({ ...d, id: d.id + '-p2', title: `${d.title}（第二页）` }))
      : pool.slice(0, 3)
  return {
    items: page,
    cursor: cursor === 'mock-c2' ? '' : 'mock-c2',
    hasMore: cursor !== 'mock-c2'
  }
}

/* ---------------------------------------------------------------- V2-2 历史页种子 */

export interface MockHistoryItem {
  /** drama id（含冒号，如 hongguo:74392…）；写历史时 key = `${id}:${episode}` */
  id: string
  title: string
  episode: number
  position: number
  duration: number
  /** epoch 秒：相对「现在」的偏移，保证 今天/昨天/更早 三段都有数据 */
  offsetDays: number
  hour: number
}

/** 种子不自动写入 localStorage，由 HistoryView 在 mock 模式且无记录时注入展示 */
export const MOCK_HISTORY_SEED: MockHistoryItem[] = [
  { id: 'hongguo:7439220582928952383', title: '一世之尊', episode: 12, position: 245, duration: 132, offsetDays: 0, hour: 9 },
  { id: 'hongguo:7421530981234567890', title: '闪婚老公是大佬', episode: 30, position: 118, duration: 158, offsetDays: 0, hour: 21 },
  { id: 'hongguo:7418876543210987654', title: '山河引', episode: 7, position: 158, duration: 158, offsetDays: 1, hour: 13 },
  { id: 'huangguo_ai:7512000000000000001', title: '星河远征（AI）', episode: 3, position: 44, duration: 120, offsetDays: 1, hour: 23 },
  { id: 'hongguo:7399111222333444555', title: '重生之我在短剧当编剧', episode: 28, position: 90, duration: 150, offsetDays: 3, hour: 20 },
  { id: 'hongguo:7439220582928952383', title: '一世之尊', episode: 8, position: 132, duration: 132, offsetDays: 9, hour: 11 }
]

/* ---------------------------------------------------------------- V2-2 日历页种子 */

/** 上新日历：今天起 7 天 × 红果形状条目（date=YYYYMMDD，online 标志+hint 与 v1 calendar_panel 一致） */
export function mockCalendarDays(): CalendarItem[] {
  const fmt = (d: Date) =>
    `${d.getFullYear()}${String(d.getMonth() + 1).padStart(2, '0')}${String(d.getDate()).padStart(2, '0')}`
  const now = new Date()
  const out: CalendarItem[] = []
  const pool = [
    ['夜阑直播', '悬疑', true, ''],
    ['长公主的替影', '古装', true, ''],
    ['逆熵实验室', '科幻', false, '今晚 18:00'],
    ['雾中告别', '都市', false, '今日 20:00'],
    ['机甲小厨娘', '漫剧', false, '明日 10:00'],
    ['山海藏舟', '玄幻', false, '待定'],
    ['第二十四次日落', '甜宠', false, '11-14 12:00'],
    ['深巷来电', '悬疑', false, '11-15 08:00'],
    ['我的AI室友', 'AI剧', false, '11-15 19:30']
  ] as const
  for (let day = 0; day < 7; day++) {
    const d = new Date(now.getFullYear(), now.getMonth(), now.getDate() + day)
    const dateStr = fmt(d)
    const per = day === 0 ? 4 : 1 + (day % 3)
    for (let k = 0; k < per; k++) {
      const [title, cat, online, hint] = pool[(day * 3 + k) % pool.length]
      out.push({
        date: dateStr,
        online: day === 0 ? online : false,
        hint: day === 0 ? hint || '已上线' : hint,
        drama: {
          id: `hongguo:mock-cal-${day}-${k}`,
          source: 'hongguo',
          title: `${title}`,
          cover: `/posters/p${((day + k) % 4) + 1}.svg`,
          category: cat,
          description: `${cat}题材，mock 日历条目（示例数据）。`,
          vip: (day + k) % 3 === 0,
          heat: `${120 + day * 37}万`,
          tags: [cat],
          onlineDate: `${dateStr.slice(0, 4)}-${dateStr.slice(4, 6)}-${dateStr.slice(6, 8)}`,
          episodes: 24
        }
      })
    }
  }
  return out
}

/** 追剧日历 mock：与核心 App.Calendar（case "calendar" → hongguoCalendarPage）
    同构的平铺页形状 —— items 属于所查询日期、dates 轴、defaultDate/hasMore/nextOffset。
    mockCalendarDays() 保留给 ReserveView（嵌套形状），两者互不影响。 */
export function mockCalendarPage(date = ''): CalendarPayload {
  const fmt = (d: Date) =>
    `${d.getFullYear()}${String(d.getMonth() + 1).padStart(2, '0')}${String(d.getDate()).padStart(2, '0')}`
  const now = new Date()
  const todayStr = fmt(now)
  // 核心日期轴 ≈ 今天前 2 天 .. 后 4 天
  const dates: string[] = []
  for (let day = -2; day <= 4; day++) {
    dates.push(fmt(new Date(now.getFullYear(), now.getMonth(), now.getDate() + day)))
  }
  const selected = /^\d{8}$/.test(date) ? date : todayStr
  const pool: CalendarEntry[] = [
    { seriesId: 'mock-cal-1', title: '夜阑直播', cover: '/posters/p1.svg', category: '悬疑', tags: ['悬疑', '直播'], episodeCnt: 24, isOnline: true, subscribed: false, heat: '1234万热度', note: '1234万热度', publishText: '24集全', badge: '爆' },
    { seriesId: 'mock-cal-2', title: '长公主的替影', cover: '/posters/p2.svg', category: '古装', tags: ['古装', '权谋'], episodeCnt: 18, isOnline: true, subscribed: true, heat: '890万热度', note: '890万热度', publishText: '更新至12集' },
    { seriesId: 'mock-cal-3', title: '逆熵实验室', cover: '/posters/p3.svg', category: '科幻', tags: ['科幻'], isOnline: false, subscribed: false, hint: '今晚 18:00', expectedText: '预计今日 18:00 上线', note: '预计今日 18:00 上线', publishAt: now.getTime() },
    { seriesId: 'mock-cal-4', title: '雾中告别', cover: '/posters/p4.svg', category: '都市', tags: ['都市', '情感'], isOnline: false, subscribed: false, hint: '今日 20:00', note: '预计今日 20:00 上线' },
    { seriesId: 'mock-cal-5', title: '机甲小厨娘', cover: '/posters/p1.svg', category: '漫剧', tags: ['漫剧'], isOnline: false, subscribed: true, hint: '待定上线时间', note: '敬请期待', badge: '新' },
    { seriesId: 'mock-cal-6', title: '山海藏舟', cover: '/posters/p2.svg', category: '玄幻', tags: ['玄幻'], episodeCnt: 30, isOnline: true, subscribed: false, note: '456万热度', publishText: '30集全' }
  ]
  const dayIdx = dates.indexOf(selected)
  // 前天日为空日（演示「这一天没有上新」空态），其余日切片变化
  const items = dayIdx < 0 || dayIdx === 0 ? [] : pool.slice(0, 2 + (dayIdx % 4)).map((it) => ({ ...it }))
  return {
    items,
    dates,
    defaultDate: todayStr,
    hasMore: false,
    nextOffset: 0,
    sessionId: 'mock-calendar',
    fetchedAt: new Date().toISOString()
  }
}

/** 分页驱动用的日历 mock（第五十四轮新增，专供验收脚本）。
 *
 * ⚠ 为什么要有它：默认 `mockCalendarPage` 永远返回 `hasMore: false`，
 * 于是 v1 用测试钉住的六条分页行为（首屏短自动补页、resize 重填、
 * 重复页停止自动请求并可重试、快速切性别丢弃过期响应、
 * 失败页同游标重试且不清空、首页不重复请求）**在这台机器上根本无法触发**——
 * 不是 v2 实现对了所以没测，而是 mock 把那条路径堵死所以测不到。
 * 这类"fixture 太配合，导致行为面永远走不到"我在第三十九轮（图片遮挡）
 * 和第四十一轮（恒真断言）都记过，形状不同、根子相同。
 *
 * 因此这里按 `window.__zgjCalPaging` 这个测试缝造一个可控分页器：
 *   pageSize   每页几条（默认 2）
 *   total      真总条数（默认 12）
 *   repeatFrom 从第几条起服务端只回重复内容（触发"连续多页没有新剧"）
 *   failFrom   从第几条起抛错（触发失败重试）
 *   slowMs     每页延迟（触发"快速切性别丢过期响应"）
 *   gender     回显当次请求的频道，用于证明最终数据来自最后一次请求
 * 默认不设这个变量时，分页器完全不参与（走原来的 mockCalendarPage）。
 */
export async function mockCalendarPagePaged(
  date: string,
  offset: number,
  gender: number
): Promise<CalendarPayload> {
  void date // 分页 mock 不按日期切片（与 mockCalendarPage 的差别），参数只为签名一致
  const cfg = ((globalThis as any).window ?? {}).__zgjCalPaging ?? {}
  const size = Number(cfg.pageSize ?? 2)
  const total = Number(cfg.total ?? 12)
  const repeatFrom = cfg.repeatFrom === undefined ? null : Number(cfg.repeatFrom)
  const failFrom = cfg.failFrom === undefined ? null : Number(cfg.failFrom)
  const slowMs = Number(cfg.slowMs ?? 0)
  const dates: string[] = []
  const now = new Date()
  const fmt = (d: Date) =>
    `${d.getFullYear()}${String(d.getMonth() + 1).padStart(2, '0')}${String(d.getDate()).padStart(2, '0')}`
  for (let day = -2; day <= 4; day++) {
    dates.push(fmt(new Date(now.getFullYear(), now.getMonth(), now.getDate() + day)))
  }
  const wait = (ms: number) => new Promise((r) => setTimeout(r, ms))

  const build = (): CalendarPayload => {
    if (failFrom !== null && offset >= failFrom) {
      throw new Error('mock 日历分页故意失败（offset=' + offset + '）')
    }
    // ⚠ 重复模式要**老实重复第一页的内容、同时把游标往前推**。
    // 我第一版写成 `from = 0`（回退游标），结果 offset 在 2↔4 之间来回跳，
    // 客户端每次都能拿到几条新剧 → 永远凑不出"连续多页没有新剧"，
    // 于是 ⑤ 那组断言测的是一个被 mock 自己消解掉的场景。
    // v1 的对应测试（test_calendar_panel.py:248）用的正是
    // "不管 offset 多少都返回同一条 seriesId"，这里照那个语义做。
    // 重复区是**有界的窗口**（repeatUntil 默认往后 3 页）：
    // 真实服务端的"连续几页重复"是暂时的，窗口无限大的话
    // 客户端的"重试能拿回新内容"这一条永远测不到。
    const repeatUntil = repeatFrom === null ? null
      : Number(cfg.repeatUntil ?? (repeatFrom + size * 3))
    const repeating = repeatFrom !== null && offset >= repeatFrom && offset < (repeatUntil as number)
    const from = repeating ? 0 : offset
    const items: CalendarEntry[] = []
    const take = repeating ? Math.min(size, repeatFrom ?? size) : size
    for (let k = 0; k < take; k++) {
      const i = repeating ? k : from + k
      if (!repeating && i >= total) break
      items.push({
        seriesId: 'mock-cal-p' + i,
        title: '分页剧' + String(i + 1) + (cfg.gender ? '[g' + gender + ']' : ''),
        cover: '/posters/p' + (i % 4 + 1) + '.svg',
        category: '分页',
        tags: ['分页'],
        episodeCnt: 10 + i,
        isOnline: i % 2 === 0,
        subscribed: false,
        note: '分页 mock',
        publishText: i % 2 === 0 ? (10 + i) + '集全' : '待上线'
      })
    }
    const next = (repeating ? offset : from) + size
    // 调用日志（只在这个测试缝里存在）：分页的判据要靠**实际序列**验证，
    // 不能靠我脑补"第几次调用落在哪一区"——本轮就是靠它对出游标是 7 而不是我预期的 6。
    try {
      const w = (globalThis as any).window
      if (w) {
        w.__zgjCalLog = w.__zgjCalLog || []
        w.__zgjCalLog.push({ offset, gender, items: items.length, next, repeating })
      }
    } catch { /* 非浏览器环境 */ }
    return {
      items,
      dates,
      defaultDate: fmt(now),
      // 重复模式下 hasMore 保持 true：客户端必须**自己**发现"这几页没有新剧"并停下，
      // 而不是靠服务端说没有更多（那正是 v1 那条测试钉的行为）
      hasMore: repeating ? true : next < total,
      nextOffset: next,
      sessionId: 'mock-calendar-paged',
      fetchedAt: new Date().toISOString()
    }
  }
  if (slowMs > 0) await wait(slowMs)
  return build()
}

/** Resolve 的 mock plan：与 Go 端 (string,error) JSON 形状一致 */
export function mockResolvePlan(drama: Drama, chapter: Chapter, index: number): ResolvePlan {
  return {
    url: `https://mock.invalid/${drama.source}/${encodeURIComponent(drama.id)}/${index}.m3u8`,
    headers: { 'User-Agent': 'ZhenguoJian-V2-mock', Referer: `https://${drama.source}.mock/` },
    quality: index,
    qualities: [
      { quality: 0, name: '标清' },
      { quality: 1, name: '高清' },
      { quality: 2, name: '超清' }
    ],
    session: `mock-${drama.id}-${chapter.currentEpisode ?? index}`,
    routeIndex: 0,
    routeCount: 1
  }
}

/** danmakuAll 的 mock：80 条演示弹幕铺满 180s（V2-3b PlayerView 演示态） */
export function mockDanmakuAll(session: string): DanmakuWindowResult {
  const texts = [
    '哈哈哈哈', '前方高能', '名场面', 'awsl', 'This is the way', '弹幕走一波',
    '演员演技在线', 'BGM 神了', '三刷打卡', '空降成功', '经费燃烧', '这运镜绝了',
    '爷青回', '泪目', '细节狂魔', '剧情反转', '上床睡觉', '火钳刘明', '考古现场', '制作组加鸡腿'
  ]
  const items: DanmakuItem[] = []
  for (let i = 0; i < 80; i++) {
    items.push({
      id: `demo-${session}-${i}`,
      timeMs: Math.round((i * 180000) / 80) + (i % 5) * 300,
      text: texts[i % texts.length],
      ...(i % 9 === 0 ? { color: '#ff3b5c' } : {})
    })
  }
  return { items, startMs: 0, nextMs: 180000, total: items.length }
}

// 用 fixture 模板造分页数据：page1..3 有数据，hasMore 到 page3 为止
export function mockCatalog(source: string, query: string, page: number): CatalogResult {
  const pool = items.filter((it) => it.source === source || source === 'hongguo')
  const named = query
    ? pool.filter((it) => it.title.includes(query) || (it.tags ?? []).some((t) => t.includes(query)))
    : pool
  const gen: Drama[] = []
  if (named.length > 0 && page <= 3) {
    for (let i = 0; i < 21; i++) {
      const base = named[i % named.length]
      gen.push({ ...base, id: `${base.id}#${page}#${i}`, title: named.length === 1 ? `${base.title}` : `${base.title}` })
    }
  }
  return {
    items: gen,
    hasMore: named.length > 0 && page < 3,
    page,
    warning: 'VITE_MOCK 示例数据，非真实引擎结果',
    localSearch: false,
    fresh: page === 1
  }
}

/* ---------------------------------------------------------------- V2-3 下载与合并（有状态 mock）
   模拟 downloadsvc 语义：入队→并发槽位→进度推进→暂停/续传/重试/移除，
   合并 FIFO 队列按 v1 阶段推进（解密→解析→统一→合并→校验→保存）。
   设置持久化在 localStorage（key 同 resourceSettings 风格），仅 VITE_MOCK 使用。 */
import type {
  DownloadEntry,
  DownloadJobRow,
  DownloadSettingsSnapshot,
  DownloadSnapshot,
  MergeEntryRow
} from './types'

const MOCK_DL_SETTINGS_KEY = 'zgj.mock.downloadSettings'
const MOCK_DL_TICK_MS = 1000
/** 与 Go 侧 mergeStages 对齐 */
const MOCK_MERGE_STAGES: Array<[string, number, number]> = [
  ['准备中', 0, 0.02],
  ['解密分集', 0.02, 0.1],
  ['解析媒体信息', 0.1, 0.16],
  ['统一分集格式', 0.16, 0.46],
  ['合并视频', 0.46, 0.58],
  ['完整解码校验', 0.58, 0.95],
  ['保存成品', 0.95, 1]
]

interface MockDlState {
  seq: number
  jobs: DownloadJobRow[]
  merges: MergeEntryRow[]
  status: string
  timer?: ReturnType<typeof setInterval>
}

function mockDlSettings(): Required<Pick<DownloadSettingsSnapshot, 'downloadConcurrency' | 'downloadBySource' | 'mergeVerify' | 'danmakuDownload'>> {
  try {
    const raw = localStorage.getItem(MOCK_DL_SETTINGS_KEY)
    if (raw) {
      const s = JSON.parse(raw) as Record<string, unknown>
      return {
        downloadConcurrency: typeof s.downloadConcurrency === 'number' ? s.downloadConcurrency : 2,
        downloadBySource: !!s.downloadBySource,
        mergeVerify: s.mergeVerify === undefined ? true : !!s.mergeVerify,
        // 「下载时保存弹幕」默认**关**（v1 tools.danmaku_download_enabled 默认 False）
        danmakuDownload: !!s.danmakuDownload
      }
    }
  } catch {
    /* 忽略：回落默认 */
  }
  return { downloadConcurrency: 2, downloadBySource: false, mergeVerify: true, danmakuDownload: false }
}

function mockDlSaveSettings(s: DownloadSettingsSnapshot): void {
  localStorage.setItem(
    MOCK_DL_SETTINGS_KEY,
    JSON.stringify({
      downloadConcurrency: s.downloadConcurrency ?? 2,
      downloadBySource: !!s.downloadBySource,
      mergeVerify: s.mergeVerify !== false,
      danmakuDownload: !!s.danmakuDownload
    })
  )
}

const mockDlHongguo: Drama = {
  id: 'hongguo:7439220582928952383',
  source: 'hongguo',
  sourceId: '7439220582928952383',
  title: '一世之尊',
  cover: '/posters/p1.svg',
  episodes: 35
}
const mockDlShan: Drama = {
  id: 'hongguo:7421530981234567890',
  source: 'hongguo',
  sourceId: '7421530981234567890',
  title: '闪婚老公是大佬',
  cover: '/posters/p2.svg',
  episodes: 82
}

// index 语义与真实核心一致：集号 1-based（app_downloads.go 要求 ≥1）；
// chapter.currentEpisode 恒等于集号；created 为 UnixMilli；quality=请求档位(0=自动)，
// actualQuality=实际落盘档位（真实样例 1080）。
function seedJob(
  state: MockDlState,
  drama: Drama,
  index: number,
  totalBytes: number,
  bytes: number,
  st: DownloadJobRow['state'],
  extra?: Partial<DownloadJobRow>
): DownloadJobRow {
  const job: DownloadJobRow = {
    id: `mock${String(++state.seq).padStart(4, '0')}`,
    // 对齐核心 app_downloads.go:367：folder = source/jobId（打开所在目录用）
    folder: `${drama.source || 'hongguo'}/mock${String(state.seq).padStart(4, '0')}`,
    drama,
    chapter: { title: `第${index}集`, currentEpisode: index, duration: 132 + (index % 7) * 9 },
    index,
    quality: 0,
    actualQuality: 1080,
    bitrate: 965601,
    state: st,
    bytes,
    total: totalBytes,
    progress: totalBytes > 0 ? Math.min(1, bytes / totalBytes) : 0,
    created: Date.now() - (3600 + index * 7) * 1000,
    revision: 1 + index,
    ...extra
  }
  state.jobs.push(job)
  return job
}

function mockDlSeed(): MockDlState {
  const mb = 1024 * 1024
  const state: MockDlState = { seq: 0, jobs: [], merges: [], status: '示例数据：模拟核心下载队列（VITE_MOCK）' }
  // 一世之尊：1/2 已完成（可整季合并），3 下载中，4 排队，5 失败，6 已暂停（index=集号 1-based）
  for (const i of [1, 2]) seedJob(state, mockDlHongguo, i, 96 * mb, 96 * mb, 'completed')
  seedJob(state, mockDlHongguo, 3, 102 * mb, 37 * mb, 'downloading')
  seedJob(state, mockDlHongguo, 4, 99 * mb, 0, 'queued')
  seedJob(state, mockDlHongguo, 5, 94 * mb, 12 * mb, 'failed', { error: '解析下载直链失败：线路超时' })
  seedJob(state, mockDlHongguo, 6, 101 * mb, 55 * mb, 'paused')
  // 闪婚老公是大佬：1/2/3 已完成
  for (const i of [1, 2, 3]) seedJob(state, mockDlShan, i, 88 * mb, 88 * mb, 'completed')
  // 归档一条
  seedJob(state, mockDlShan, 4, 86 * mb, 86 * mb, 'completed', { archived: true })
  // 合并历史：一世之尊 全001-002集 已完成
  state.merges.push({
    id: `${mockDlHongguo.id}#001-002`,
    dramaId: mockDlHongguo.id,
    title: mockDlHongguo.title,
    episodes: [1, 2],
    count: 2,
    state: 'done',
    stage: '保存成品',
    progress: 1,
    output: 'D:\\ZhenguoJian\\downloads\\合并\\一世之尊 全001-002集.mkv',
    bytes: 188 * mb,
    detail: '示例产物路径'
  })
  return state
}

let mockDl: MockDlState | null = null

function mockDlState(): MockDlState {
  if (!mockDl) mockDl = mockDlSeed()
  if (!mockDl.timer) {
    mockDl.timer = setInterval(() => mockDlTick(), MOCK_DL_TICK_MS)
  }
  return mockDl
}

function mockDlFind(id: string): DownloadJobRow | undefined {
  return mockDlState().jobs.find((j) => j.id === id)
}

/** 模拟核心调度：并发槽位内的 queued → downloading；downloading 前进字节 */
function mockDlTick(): void {
  const state = mockDl
  if (!state) return
  const { downloadConcurrency } = mockDlSettings()
  let active = state.jobs.filter((j) => j.state === 'downloading').length
  for (const job of state.jobs) {
    if (job.state !== 'queued') continue
    if (active >= Math.max(1, downloadConcurrency)) break
    job.state = 'downloading'
    job.error = undefined
    active += 1
  }
  for (const job of state.jobs) {
    if (job.state !== 'downloading') {
      if (job.state !== 'queued') job.speed = 0
      continue
    }
    const step = (5 + Math.random() * 9) * 1024 * 1024
    job.bytes = Math.min(job.total, job.bytes + step)
    job.progress = job.total > 0 ? job.bytes / job.total : 1
    job.speed = Math.round(step)
    job.eta = Math.max(0, Math.round((job.total - job.bytes) / Math.max(1, step)))
    job.revision = (job.revision ?? 0) + 1
    if (job.bytes >= job.total) {
      job.state = 'completed'
      job.progress = 1
      job.speed = 0
      job.eta = 0
      job.revision = 0
    }
  }
  for (const m of state.merges) {
    if (m.state !== 'running') continue
    const next = m.progress + 0.02 + Math.random() * 0.03
    if (next >= 1) {
      m.progress = 1
      m.stage = '保存成品'
      m.state = 'done'
      m.output = `D:\\ZhenguoJian\\downloads\\合并\\${m.title} 全${String(m.episodes[0]).padStart(3, '0')}-${String(m.episodes[m.episodes.length - 1]).padStart(3, '0')}集.mkv`
      m.detail = '示例数据：模拟合并产物'
      state.status = `《${m.title}》合并完成：全${String(m.episodes[0]).padStart(3, '0')}-${String(m.episodes[m.episodes.length - 1]).padStart(3, '0')}集（示例数据）`
    } else {
      m.progress = next
      for (const [name, lo, hi] of MOCK_MERGE_STAGES) {
        if (next >= lo && next < hi) {
          m.stage = name
          break
        }
      }
    }
  }
}

export function mockDownloadList(): DownloadSnapshot {
  const state = mockDlState()
  const { mergeVerify } = mockDlSettings()
  return {
    jobs: state.jobs.map((j) => ({ ...j })),
    merges: state.merges.map((m) => ({ ...m })),
    status: state.status,
    mergeVerify
  }
}

export function mockDownloadStart(drama: Drama, entries: DownloadEntry[], quality: number): { added: number } {
  const state = mockDlState()
  if (!drama?.id || !drama?.source) throw new Error('剧集信息不完整')
  if (!entries.length) throw new Error('请先选择要下载的分集')
  if (entries.length > 500) throw new Error('单次最多添加 500 集')
  let added = 0
  const mb = 1024 * 1024
  for (const e of entries) {
    // 镜像核心校验（app_downloads.go enqueueContext：index<1 → 「下载集数无效」，整批失败）
    if (!Number.isFinite(e.index) || e.index < 1) throw new Error('下载集数无效')
    if (state.jobs.some((j) => j.drama?.id === drama.id && j.index === e.index && !j.archived)) continue
    const total = (85 + ((e.index * 7) % 25)) * mb
    seedJob(state, drama, e.index, total, 0, 'queued', { quality, chapter: e.chapter })
    added += 1
  }
  state.status = `已添加 ${added} 集到下载队列（示例数据）`
  return { added }
}

export function mockDownloadControl(id: string, command: string): { ok: boolean } {
  const state = mockDlState()
  const job = mockDlFind(id)
  if (!job) throw new Error('下载任务不存在，请刷新列表')
  if (command === 'pause' && (job.state === 'downloading' || job.state === 'queued')) {
    job.state = 'paused'
    job.speed = 0
    job.error = '上次下载已暂停'
  } else if (command === 'resume' && (job.state === 'paused' || job.state === 'failed')) {
    job.state = 'queued'
    job.error = undefined
  } else if (command === 'remove') {
    state.jobs = state.jobs.filter((j) => j.id !== id)
  } else if (command === 'archive') {
    job.archived = true
  } else if (command === 'restore') {
    job.archived = false
  }
  return { ok: true }
}

export function mockDownloadControlAll(command: string): { ok: boolean } {
  const state = mockDlState()
  for (const job of state.jobs) {
    if (command === 'pauseAll' && (job.state === 'downloading' || job.state === 'queued')) {
      job.state = 'paused'
      job.speed = 0
      job.error = '上次下载已暂停'
    } else if (command === 'resumeAll' && job.state === 'paused') {
      job.state = 'queued'
      job.error = undefined
    }
  }
  return { ok: true }
}

export function mockDownloadRemoveBatch(ids: string[]): { completed: string[]; failures: Record<string, string> } {
  const state = mockDlState()
  const completed = ids.filter((id) => state.jobs.some((j) => j.id === id))
  state.jobs = state.jobs.filter((j) => !ids.includes(j.id))
  return { completed, failures: {} }
}

export function mockDownloadSettings(): DownloadSettingsSnapshot {
  return {
    proxyMode: 'auto',
    catalogConcurrency: 3,
    catalogIntervalMs: 250,
    ...mockDlSettings()
  }
}

export function mockSaveDownloadSettings(concurrency: number, bySource: boolean, mergeVerify: boolean, danmakuDownload = false): DownloadSettingsSnapshot {
  const snap: DownloadSettingsSnapshot = {
    proxyMode: 'auto',
    catalogConcurrency: 3,
    catalogIntervalMs: 250,
    downloadConcurrency: Math.max(1, Math.min(8, Math.round(concurrency || 2))),
    downloadBySource: !!bySource,
    mergeVerify: !!mergeVerify,
    danmakuDownload: !!danmakuDownload
  }
  mockDlSaveSettings(snap)
  mockDlState().status = '下载设置已保存（示例数据）'
  return mockDownloadSettings()
}

/**
 * mock 面「按季合并选中」。
 *
 * ⚠ 这里**故意不复刻** Go 的规则表（季序、`<去季号名> S%02dE%02d.mkv` 命名、
 * 同名冲突 ` (2)`、"每部至少完成 2 集"的挑选、"运行中的季跳过而不是报错"）——
 * 那些都在 `internal/downloadsvc/multi.go` 有单测，抄到 mock 里就是第二个真相，
 * 一定会漂（本任务第 27/35/41 轮同一个病根）。
 *
 * mock 只承担两件事，够把 UI 接上：
 *  ① 「不足两部整批不发起」这条**门槛**要照 v1 报错，否则界面上那颗按钮
 *     的错误分支永远走不到；
 *  ② 排进去的条目要能在「合并」列表里看见（复用 mockMergeStart 的排队实现）。
 * 所以断言只验"按下的集合对不对、结果有没有落到列表、错误话术出不出现"，
 * 不验命名与季序——那两条属于 Go 的测试。
 */
export function mockMergeStartMulti(dramas: Drama[]): {
  queued: number; replaced?: number; skipped?: number; outputs: string[]
} {
  const state = mockDlState()
  if (!dramas?.length) {
    throw Object.assign(new Error('请先勾选要合并的剧集'), { code: 'multi_none' })
  }
  const completable = dramas.filter((d) =>
    state.jobs.filter((j) => !j.archived && j.state === 'completed' && j.drama?.id === d.id).length >= 2)
  if (completable.length < 2) {
    // code 与 Go 侧 `[multi_targets]` **同一套**，界面那段 switch 在 mock 下走的是同一条代码。
    // ⚠ 刻意不在 api.ts 里"靠中文子串反推 code"：那是手写字符串匹配，
    //   改一个字就静默失效，而且静态扫描器会把它当成硬编码界面文案抓出来（本轮真抓了 3 处）。
    //   ApiError 类在 api.ts（这里 import 会成环），所以直接造一个同形状的 Error：
    //   读侧只看 `e.code` / `e.message`，两者行为一致。
    throw Object.assign(
      new Error('请先勾选至少两部要合并的剧集（每部至少完成 2 集）'),
      { code: 'multi_targets' }
    )
  }
  let queued = 0
  const outputs: string[] = []
  for (const d of completable) {
    const res = mockMergeStart('season', d, [])
    queued += res.queued
    const last = state.merges[state.merges.length - 1]
    outputs.push(last?.output ?? '')
  }
  return { queued, outputs }
}

export function mockMergeStart(mode: 'season' | 'selected', drama: Drama | null, jobIds: string[]): { queued: number; replaced: boolean; episodes: number[] } {
  const state = mockDlState()
  let candidates: DownloadJobRow[] = []
  if (mode === 'selected') {
    if (!jobIds.length) throw new Error('请先勾选要合并的分集')
    candidates = state.jobs.filter((j) => jobIds.includes(j.id))
    if (candidates.some((j) => j.state !== 'completed')) throw new Error('只有已完成的分集可以合并，请检查勾选')
    const ids = new Set(candidates.map((j) => j.drama?.id ?? ''))
    if (ids.size > 1) throw new Error('请选择同一部剧的分集合并')
  } else {
    if (!drama?.id) throw new Error('缺少剧集信息')
    candidates = state.jobs.filter((j) => j.drama?.id === drama.id && j.state === 'completed' && !j.archived)
  }
  if (candidates.length < 2) {
    const title = drama?.title ?? candidates[0]?.drama?.title ?? ''
    throw new Error(`《${title}》可合并的已完成分集不足 2 集`)
  }
  candidates.sort((a, b) => a.index - b.index)
  const d = drama ?? (candidates[0].drama as Drama)
  // 集号解析与 Go 侧 downloadsvc.episodeOfJob 同规则：chapter.currentEpisode 优先，
  // 缺失时 index>=1 直接用（旧 0-based 记录回退 index+1）。
  const episodes = candidates.map((j) => {
    const cur = Number(j.chapter?.currentEpisode ?? 0)
    if (Number.isFinite(cur) && cur >= 1) return cur
    const ix = Number(j.index ?? 0)
    return ix >= 1 ? ix : ix + 1
  })
  const first = episodes[0]
  const last = episodes[episodes.length - 1]
  const id = `${d.id}#${String(first).padStart(3, '0')}-${String(last).padStart(3, '0')}`
  if (state.merges.some((m) => m.id === id && (m.state === 'running' || m.state === 'queued'))) {
    throw new Error('该剧这段分集正在合并中，请等待完成')
  }
  const queued = state.merges.filter((m) => m.id === id && m.state === 'queued')
  state.merges = state.merges.filter((m) => !queued.includes(m))
  state.merges.unshift({
    id,
    dramaId: d.id,
    title: d.title,
    episodes,
    count: episodes.length,
    state: 'running',
    stage: '准备中',
    progress: 0.01
  })
  state.status = `开始合并《${d.title}》全${episodes.length}集（示例数据）`
  mockDlTick()
  return { queued: 1, replaced: queued.length > 0, episodes }
}

export function mockMergeCancel(id: string): { ok: boolean } {
  const state = mockDlState()
  const targets = state.merges.filter((m) => (!id || m.id === id) && (m.state === 'running' || m.state === 'queued'))
  for (const m of targets) {
    m.state = 'canceled'
    m.detail = id ? '已取消排队' : '合并已取消'
  }
  return { ok: true }
}

/** 下载根目录 mock（真实面：DownloadDirectory 绑定 = core downloadDirectory + os.Stat） */
export function mockDownloadDirectory(): { directory: string; exists: boolean } {
  // 纯 Windows 路径形状：openDownloadPath 等消费端不做 CJK 假设，
  // 演示横幅文案由消费端 MOCK_MODE 自行标注
  return { directory: 'D:\\ZhenguoJian\\downloads', exists: true }
}

/** 归档目录扫描 mock（真实面：App.ArchiveScan → downloadsvc.ScanArchiveRoot）。
 *  3 组样例树（示例数据）：一世之尊=分集+合并产物回组（含合并徽标）、
 *  闪婚老公是大佬=纯下载分集（archived 旗标）、凡人修仙传=文件系统孤儿大文件组（indexed:false）。
 *  localStorage 'zgj.mock.archiveMissing'='1' → exists:false 空态（验证脚本用）。 */
export function mockArchiveScan(): ArchiveScanResult {
  const directory = mockDownloadDirectory().directory
  if (typeof localStorage !== 'undefined' && localStorage.getItem('zgj.mock.archiveMissing') === '1') {
    return { directory, exists: false, groups: [] }
  }
  const day = 86_400_000
  const now = Date.now()
  const groups = [
    {
      name: '一世之尊',
      episodes: 2,
      bytes: 1_572_866_048,
      mtime: now - day,
      hasMerged: true,
      mergedCount: 1,
      mergedBytes: 3_221_225_472,
      archived: true,
      indexed: true,
      folders: ['hongguo/mock0001', 'hongguo/mock0002'],
      openPath: '合并/一世之尊 全001-002集.mkv',
    },
    {
      name: '闪婚老公是大佬',
      episodes: 1,
      bytes: 402_653_184,
      mtime: now - 3 * day,
      hasMerged: false,
      archived: true,
      indexed: true,
      folders: ['xh/mock0004'],
      openPath: 'xh/mock0004',
    },
    {
      name: '凡人修仙传',
      episodes: 30,
      bytes: 17_179_869_184,
      mtime: now - 7 * day,
      hasMerged: false,
      archived: false,
      indexed: false,
      folders: ['fanren/mock0030'],
      openPath: 'fanren/mock0030',
    },
  ]
  return { directory, exists: true, groups }
}

/* ---------------------------------------------------------------- V2-2 剧库推荐（有状态 mock）
   形状对齐真实核心探针实录（2026-11-10，client-v2 zz 探针）：
   recommendations(short_play) 首屏 18 条 / more 返回「累计合并表」36 条
   （前 18 条 id 与首屏逐条相同——去重责任在壳层/前端，见 app_recommendations.go:105-122）。
   剧本设计：
   - short_play：36 条封顶、每页新增 18——more 全量重发 → 验证前端 known_ids 去重不重复渲染。
   - comic_series：恒 8 条但 hasMore 永真——more 返回同一累计表（0 新增）→ 验证「连续 2 空页停止」。
     （真实核心该场景走 error「暂时没有新的推荐…」；mock 取不报错变体专门验证壳层计数规则，
      前端 error 分支另有断言覆盖。）
   - ai_series：首屏即空 + warning → 验证空态文案。
   「全部」tab 走 mockCatalog（api.catalog 面），本块不重复实现。仅 VITE_MOCK 使用。 */

interface LibState {
  cursor: number // 已累计交付条数
  pages: number // 仅用于回显 page 字段（真实核心同款：首屏 1、more 递增）
}

const LIB_SPECS: Record<string, { total: number; step: number; forever: boolean }> = {
  short_play: { total: 36, step: 18, forever: false },
  comic_series: { total: 8, step: 8, forever: true },
  ai_series: { total: 0, step: 0, forever: false }
}

let libStates: Record<string, LibState> = {}

function libEntry(pool: Drama[], category: string, i: number): Drama {
  const base = pool[i % pool.length]
  return {
    ...base,
    id: `hongguo:mock-lib-${category}-${i}`,
    sourceId: `mock-lib-${category}-${i}`,
    title: `${base.title}·${i + 1}`
  }
}

export function mockLibraryRecommendations(category: string, force: boolean, more: boolean): CatalogResult {
  const spec = LIB_SPECS[category] ?? { total: 0, step: 0, forever: false }
  const pool = items.filter((it) => it.source === 'hongguo')
  let st = libStates[category]
  if (!st || force || !more) {
    // 首屏/刷新：重置累计窗口（真实核心 force=重开推荐状态机）
    st = { cursor: Math.min(spec.step, spec.total), pages: 1 }
    libStates[category] = st
  } else {
    st.cursor = Math.min(st.cursor + spec.step, spec.total)
    st.pages += 1
  }
  const cum: Drama[] = []
  for (let i = 0; i < st.cursor; i++) cum.push(libEntry(pool, category, i))
  const hasMore = spec.forever ? spec.total > 0 : st.cursor < spec.total
  return {
    items: cum,
    hasMore,
    page: st.pages,
    warning: cum.length === 0 ? `${category} 暂无推荐（示例数据）` : 'VITE_MOCK 示例数据，非真实引擎结果',
    localSearch: false,
    fresh: true
  }
}

/** 壳层页码游标重置（对应 App.RecommendationPageReset；mock 面清空全部累计状态） */
export function mockLibraryPageReset(): void {
  libStates = {}
}

/* ---------------------------------------------------------------- 榜单侧栏（V2-E rankings，有状态 mock）
   形状对齐真实核心（guoapp-src/native/core/app_rankings.go:60-94）：
   rankings 应答平铺 {boardId,page,items:[{rank,drama,metric}],hasMore,
   totalPages,updatedText,fetchedAt,stale}，**无 boards 键**——候选列表来自
   独立 action rankingBoards（app_runtime.go:457-462 {items:[…]}），mock 面
   用 mockRankingBoards 提供同形状。剧本设计：
   - 2 个榜（hongguo-hot 总热播榜 / huangdou-all 总榜，id/名照真实常量表
     provider_rankings.go:23-37），每榜 20 条 × 每页 10 条 × 2 页：
     首屏恰好 10 行名次 1..10（断言①），加载更多追加 11..20 不重复（断言④），
     两榜标题错位取池（hongguo 起 0 / huangdou 起 10）→ 切榜首屏逐行不同（断言②）。
    - 榜内去重仍由前端 append（v1 rank_list.py:283-311 seen 集同款）承担；
      mock 榜内 id 天然唯一，重复页重发时前端 seen 集拦截。
   仅 VITE_MOCK=1 使用；封面复用本地占位 SVG 池。 */

export interface MockRankingBoard {
  id: string
  source: string
  name: string
  description: string
}

const RANK_BOARD_SPECS: MockRankingBoard[] = [
  { id: 'hongguo-hot', source: 'hongguo', name: '总热播榜', description: '红果站内观看、互动等综合热度，每日更新（示例数据）' },
  { id: 'huangdou-all', source: 'huangdou', name: '总榜', description: '黄豆短剧全库综合榜，含跨站重合条目（示例数据）' }
]

const RANK_POOL_TITLES = [
  '一世之尊', '夜阑直播', '南柯入梦', '山河故人', '逆光情深', '长安小厨娘', '都市医尊', '重生九零',
  '双面甜妻', '战神归来', '青梅有点甜', '凤还朝', '迷雾追凶', '晚风入怀', '武林外史传', '总裁的替嫁新娘',
  '星城故事', '归路有灯', '暗河烽火', '爱在烟火处'
]

let rankStates: Record<string, number> = {} // boardId → 已交付页数（仅用于回显 page）

/** 榜单候选 mock（对应 App.RankingBoards → {items:[…]}，v1 board_box 数据源） */
export function mockRankingBoards(): { items: MockRankingBoard[] } {
  return { items: RANK_BOARD_SPECS.map((b) => ({ ...b })) }
}

/**
 * 榜单一页 mock（对应 App.Rankings）。page 越界（<1 或 >totalPages）回空增量，
 * 模拟核心「榜单页码无效」之外的正常耗尽路径；force 在 mock 面无副作用
 * （真实面跳核心 5min 缓存，缓存语义不在 mock 职责内）。
 */
export function mockRankings(boardId: string, page: number, _force: boolean): {
  boardId: string
  page: number
  items: Array<{ rank: number; drama: Drama; metric: string }>
  hasMore: boolean
  totalPages: number
  updatedText: string
  fetchedAt: string
  stale: boolean
} {
  const totalPages = 2
  const valid = page >= 1 && page <= totalPages
  const rows: Array<{ rank: number; drama: Drama; metric: string }> = []
  if (valid) {
    const boardOffset = boardId === 'hongguo-hot' ? 0 : 10 // 两榜错位取标题池，切榜首屏逐行不同
    for (let r = (page - 1) * 10 + 1; r <= page * 10; r++) {
      const idx = r - 1
      const base = items[idx % items.length]
      const drama: Drama = {
        ...base,
        id: `${boardId === 'hongguo-hot' ? 'hongguo' : 'huangdou'}:mock-rank-${idx + 1}`,
        sourceId: `mock-rank-${boardId}-${idx + 1}`,
        source: boardId === 'hongguo-hot' ? 'hongguo' : 'huangdou',
        title: RANK_POOL_TITLES[(boardOffset + idx) % RANK_POOL_TITLES.length],
        // 主池个别条目无 cover（如 午夜档案），行行显式给 p1..p4 循环封面
        cover: `/posters/p${(idx % 4) + 1}.svg`
      }
      rows.push({ rank: r, drama, metric: `${9800 - idx * 137}万热度（示例数据）` })
    }
    rankStates[boardId] = Math.max(rankStates[boardId] || 0, page)
  }
  return {
    boardId,
    page: valid ? page : (rankStates[boardId] || 1),
    items: rows,
    hasMore: valid && page < totalPages,
    totalPages,
    updatedText: '11月10日已更新（示例数据）',
    fetchedAt: '2026-11-10T19:00:00Z',
    stale: false
  }
}

/** mock 面清空榜单游标（测试辅助；真实面无壳层状态，无需 reset） */
export function mockRankingsReset(): void {
  rankStates = {}
}

/* ---------------------------------------------------------------- 搜索联想（子代理 G）
   mockSuggestions：红果联想词 mock 池，形状对齐真实探针（items=纯字符串，
   前缀命中在前、包含命中补位；cap=10 同 hongguoSuggestionLimit，
   provider_hongguo_suggestions.go:16）。命中不足时以「输入+常见后缀」模板
   补到 ≥8 条，保证 mock 演示下拉始终有内容。示例数据，仅 VITE_MOCK 使用。 */
const MOCK_SUGGEST_POOL = [
  '十八岁太奶奶驾到重整家族荣耀',
  '十八岁太奶奶驾到2',
  '太奶奶重生当团宠',
  '奶奶的传家秘方',
  '妈咪受辱大小姐回国掀翻全场',
  '妈来',
  '闪婚老公是大佬',
  '闪婚老公竟是隐藏首富',
  '重生之我在短剧当编剧',
  '逆流人生之她逆袭了',
  '医妃倾城之嫡女归来',
  '无双战神在都市'
]
const MOCK_SUGGEST_SUFFIXES = ['第二季', '大结局', '全集', '免费观看', '续集', '番外', '真人版', '小说版']

export function mockSuggestions(query: string): string[] {
  const q = (query || '').trim()
  if (!q) return []
  const prefix = MOCK_SUGGEST_POOL.filter((w) => w.startsWith(q))
  const contains = MOCK_SUGGEST_POOL.filter((w) => !w.startsWith(q) && w.includes(q))
  const out: string[] = [...prefix, ...contains]
  if (out.length < 8) {
    for (const s of MOCK_SUGGEST_SUFFIXES) {
      const w = `${q}${s}`
      if (!out.includes(w)) out.push(w)
      if (out.length >= 10) break
    }
  }
  return out.slice(0, 10)
}

// ── browseFilters mock（与 v2overlay 构建真实形状一致）──

export interface MockFilterGroup { key: string; name: string; options: { id: string; name: string }[] }

export function mockHongguoFilterGroups(): { groups: MockFilterGroup[] } {
  return {
    groups: [
      {
        key: 'genre',
        name: '体裁',
        options: [
          { id: 'short_play', name: '真人剧' },
          { id: 'comic_series', name: '漫剧' },
          { id: 'ai_series', name: 'AI剧' }
        ]
      },
      {
        key: 'creation_status',
        name: '状态',
        options: [
          { id: 'creation_status_0', name: '已完结' },
          { id: 'creation_status_1', name: '连载中' }
        ]
      },
      // discoveryOverlay 的红果别名组（v2 芯片行消费）
      {
        key: 'category',
        name: '体裁',
        options: [
          { id: 'short_play', name: '真人剧' },
          { id: 'comic_series', name: '漫剧' },
          { id: 'ai_series', name: 'AI剧' }
        ]
      }
    ]
  }
}

const MOCK_SOURCE_CATEGORIES: Record<string, { id: string; name: string }[]> = {
  huangdou: [{ id: '男频', name: '男频' }, { id: '女频', name: '女频' }],
  yeguo: [{ id: '热播', name: '热播' }, { id: 'new', name: '最新' }],
  youzi: [
    { id: 'dramas', name: 'AI短剧' },
    { id: 'ai-face-swap', name: 'AI换脸' },
    { id: 'ai-adult/real', name: 'AI真人' },
    { id: 'ai-adult/anime', name: 'AI动漫' },
    { id: 'ai-adult/remix', name: 'AI魔改' }
  ],
  guipian: [{ id: '恐怖', name: '恐怖' }, { id: '悬疑', name: '悬疑' }],
  'huangguo-video': [{ id: '1', name: '热门' }, { id: '2', name: '新上线' }],
  sorani: [{ id: '1', name: '国产' }, { id: '2', name: '日韩' }, { id: '5', name: '其他' }]
}

export function mockSourceCategoryGroups(source: string): { groups: MockFilterGroup[] } {
  const options = MOCK_SOURCE_CATEGORIES[source] ?? []
  return {
    groups: [
      { key: 'category', name: '分类', options: [{ id: '', name: '全部' }, ...options] }
    ]
  }
}
