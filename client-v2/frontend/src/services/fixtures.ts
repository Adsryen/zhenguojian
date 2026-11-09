/* mock fixtures —— 手抄自 v1 引擎真实 catalog 形状（hongguo nativeDrama 字段名逐一对应；
   封面为本地占位 SVG，真实 cover 是引擎返回的 CDN 直链）。仅 VITE_MOCK=1 时使用。 */
import type { CatalogResult, Drama, SourceListItem } from './types'

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
