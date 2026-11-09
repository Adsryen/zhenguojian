/* mock fixtures —— 手抄自 v1 引擎真实 catalog 形状（hongguo nativeDrama 字段名逐一对应；
   封面为本地占位 SVG，真实 cover 是引擎返回的 CDN 直链）。仅 VITE_MOCK=1 时使用。 */
import type { DanmakuItem, DanmakuWindowResult, CalendarItem, CatalogResult, Chapter, DetailResult, Drama, ResolvePlan, SourceListItem } from './types'

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
    releaseStatus: '更新至66集'
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
    releaseStatus: '已完结'
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
