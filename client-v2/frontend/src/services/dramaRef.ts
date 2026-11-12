/* Drama ⇄ 路由参数：Detail/Resolve 绑定都吃 dramaJSON 原样对象，
   所以详情页/播放器一律通过 query.d（紧凑 Drama JSON）传参，
   保证「刷新/粘贴 URL」也能恢复页面；旧链接的 ?source=&title= 形状仍可兜底。 */
import type { RouteLocationRaw } from 'vue-router'
import type { Chapter, Drama } from './types'

export function compactDrama(d: Drama): Drama {
  return {
    id: d.id,
    source: d.source,
    sourceId: d.sourceId ?? '',
    title: d.title,
    description: d.description ?? '',
    cover: d.cover ?? '',
    episodes: d.episodes ?? 0,
    category: d.category ?? '',
    vip: d.vip ?? null,
    heat: d.heat ?? '',
    views: d.views ?? '',
    onlineDate: d.onlineDate ?? '',
    tags: d.tags ?? [],
    releaseStatus: d.releaseStatus ?? '',
    ...(d.adult ? { adult: true } : {})
  }
}

export function detailRoute(d: Drama): RouteLocationRaw {
  return { path: `/detail/${encodeURIComponent(d.id)}`, query: { d: JSON.stringify(compactDrama(d)) } }
}

/** `localPath` 非空 = 播本地成品（v1 历史/追剧面板的 local_path 分支），
 *  播放器据此**不重开在线会话**、隐藏选集与上下集。
 *  参数追加在最后一位且可选：老调用点一字不改（第 13 轮那条兼容教训）。 */
export function playerRoute(d: Drama, episode: number, position = 0, chapter?: Chapter | null, localPath = ''): RouteLocationRaw {
  const query: Record<string, string> = {
    d: JSON.stringify(compactDrama(d)),
    ep: String(episode),
    pos: String(Math.floor(position))
  }
  if (chapter) query.c = JSON.stringify(chapter)
  if (localPath) query.lp = localPath
  return { path: '/player', query }
}

/** 从 route.query 恢复 Drama；d 解析失败时退回 id/source/title 最小形状 */
export function dramaFromQuery(id: string, q: Record<string | number | symbol, unknown>): Drama {
  const rawD = typeof q.d === 'string' ? q.d : ''
  if (rawD) {
    try {
      const parsed = JSON.parse(rawD) as Drama
      if (parsed && typeof parsed.id === 'string' && parsed.id) return parsed
    } catch {
      /* 落到最小形状兜底 */
    }
  }
  return {
    id,
    source: typeof q.source === 'string' ? q.source : id.split(':')[0] ?? '',
    title: typeof q.title === 'string' ? q.title : ''
  }
}

/** chapter 的集数：v1 _episode_number 语义（currentEpisode 优先，兜底序号+1） */
export function chapterEpisode(chapter: Chapter, fallbackIndex: number): number {
  const v = chapter.currentEpisode
  if (typeof v === 'number' && !Number.isNaN(v) && v > 0) return Math.floor(v)
  if (typeof v === 'string' && /^\d+$/.test(v.trim()) && Number(v) > 0) return Number(v.trim())
  return fallbackIndex + 1
}

/** chapter 的 VIP：v1 _chapter_vip 语义 —— 剧目级标志优先，逐集字段兜底 */
export function chapterVip(chapter: Chapter, dramaVip: boolean | null | undefined): boolean {
  if (dramaVip === true) return true
  if (chapter.vip === true || chapter.vip === 1 || chapter.vip === '1' || chapter.vip === 'true') return true
  if (String(chapter.type || '').toLowerCase() === 'vip') return true
  return chapter.is_vip === 1 || chapter.is_vip === '1' || chapter.is_vip === true
}

/** 「更新至66集」→ 66；「已完结」/空 → 0 */
export function updateCount(releaseStatus?: string): number {
  const m = /(\d+)\s*集/.exec(releaseStatus || '')
  return m ? Number(m[1]) : 0
}
