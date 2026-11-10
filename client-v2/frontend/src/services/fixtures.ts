/* mock fixtures —— 手抄自 v1 引擎真实 catalog 形状（hongguo nativeDrama 字段名逐一对应；
   封面为本地占位 SVG，真实 cover 是引擎返回的 CDN 直链）。仅 VITE_MOCK=1 时使用。 */
import type { DanmakuItem, DanmakuWindowResult, CalendarEntry, CalendarItem, CalendarPayload, CatalogResult, Chapter, DetailResult, Drama, ResolvePlan, SourceListItem } from './types'

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

function mockDlSettings(): Required<Pick<DownloadSettingsSnapshot, 'downloadConcurrency' | 'downloadBySource' | 'mergeVerify'>> {
  try {
    const raw = localStorage.getItem(MOCK_DL_SETTINGS_KEY)
    if (raw) {
      const s = JSON.parse(raw) as Record<string, unknown>
      return {
        downloadConcurrency: typeof s.downloadConcurrency === 'number' ? s.downloadConcurrency : 2,
        downloadBySource: !!s.downloadBySource,
        mergeVerify: s.mergeVerify === undefined ? true : !!s.mergeVerify
      }
    }
  } catch {
    /* 忽略：回落默认 */
  }
  return { downloadConcurrency: 2, downloadBySource: false, mergeVerify: true }
}

function mockDlSaveSettings(s: DownloadSettingsSnapshot): void {
  localStorage.setItem(
    MOCK_DL_SETTINGS_KEY,
    JSON.stringify({
      downloadConcurrency: s.downloadConcurrency ?? 2,
      downloadBySource: !!s.downloadBySource,
      mergeVerify: s.mergeVerify !== false
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
    drama,
    chapter: { title: `第${index + 1}集`, currentEpisode: index + 1, duration: 132 + (index % 7) * 9 },
    index,
    quality: 2,
    actualQuality: 2,
    bitrate: 2800000,
    state: st,
    bytes,
    total: totalBytes,
    progress: totalBytes > 0 ? Math.min(1, bytes / totalBytes) : 0,
    created: Math.floor(Date.now() / 1000) - 3600 - index * 7,
    revision: 1 + index,
    ...extra
  }
  state.jobs.push(job)
  return job
}

function mockDlSeed(): MockDlState {
  const mb = 1024 * 1024
  const state: MockDlState = { seq: 0, jobs: [], merges: [], status: '示例数据：模拟核心下载队列（VITE_MOCK）' }
  // 一世之尊：1/2 已完成（可整季合并），3 下载中，4 排队，5 失败，6 已暂停
  for (const i of [0, 1]) seedJob(state, mockDlHongguo, i, 96 * mb, 96 * mb, 'completed')
  seedJob(state, mockDlHongguo, 2, 102 * mb, 37 * mb, 'downloading')
  seedJob(state, mockDlHongguo, 3, 99 * mb, 0, 'queued')
  seedJob(state, mockDlHongguo, 4, 94 * mb, 12 * mb, 'failed', { error: '解析下载直链失败：线路超时' })
  seedJob(state, mockDlHongguo, 5, 101 * mb, 55 * mb, 'paused')
  // 闪婚老公是大佬：1/2/3 已完成
  for (const i of [0, 1, 2]) seedJob(state, mockDlShan, i, 88 * mb, 88 * mb, 'completed')
  // 归档一条
  seedJob(state, mockDlShan, 3, 86 * mb, 86 * mb, 'completed', { archived: true })
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

export function mockSaveDownloadSettings(concurrency: number, bySource: boolean, mergeVerify: boolean): DownloadSettingsSnapshot {
  const snap: DownloadSettingsSnapshot = {
    proxyMode: 'auto',
    catalogConcurrency: 3,
    catalogIntervalMs: 250,
    downloadConcurrency: Math.max(1, Math.min(8, Math.round(concurrency || 2))),
    downloadBySource: !!bySource,
    mergeVerify: !!mergeVerify
  }
  mockDlSaveSettings(snap)
  mockDlState().status = '下载设置已保存（示例数据）'
  return mockDownloadSettings()
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
  const episodes = candidates.map((j) => j.index + 1)
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
