/* services/nextSeason.ts — 本季播完查下一季（v1 main.py:2542-2584 移植）。
   语义锚点：
   - 触发=一季真结局（hasNextEpisode=false），非每集（v1 同款）；
   - catalog(query=base_title||title, lane="series:next", page=1, source)
     → series.group → next_season(rows, current)；
   - busy 防重入（seriesKey 级）+ prompted 幂等键 (dramaId, episodes, season)
     + 分组结果 900s 缓存（key=source:seriesKey）——均照抄 v1 常量；
   - 没查到：v1 状态栏「没查到下一季，可在详情页预约」6s；
   - 查到且已追/已预约：v1 mark_next_season（v2 Go 侧 SetNextSeason 绑定待补，
     见返报依赖请求；未接前跳过该副作用，只提示）。
   纯查询副作用；不跨季自动播放（v1 告诫保留：下一季是另一部剧，先让用户确认）。 */

import { api } from './api'
import { baseTitle, groupSeasons, seasonNumber, seriesKey, type SeasonRow } from './series'
import type { Drama } from './types'

export interface NextSeasonHit {
  drama: Drama
  season: number
  unit: string
  title: string
}

const CACHE_MS = 900_000 // v1 self._series_cache TTL 900s
const prompted = new Set<string>()
const busy = new Set<string>()
const cache = new Map<string, { at: number; rows: SeasonRow[] }>()

/** 查当前剧的下一季；命中缓存则同步返回。 */
export async function lookupNextSeason(drama: Drama): Promise<NextSeasonHit | null> {
  const title = String(drama?.title ?? '').trim()
  if (!title) return null
  const key = seriesKey(title)
  if (!key) return null
  const current = seasonNumber(title)
  const promptKey = `${drama.id}|${Math.trunc(Number(drama.episodes ?? 0))}|${current}`
  if (prompted.has(promptKey)) return null
  if (busy.has(key)) return null
  const source = String(drama.source || 'hongguo')
  const cacheKey = `${source}:${key}`
  busy.add(key)
  try {
    let entry = cache.get(cacheKey)
    if (!entry || Date.now() - entry.at > CACHE_MS) {
      // 测试后门（与 __zgjDiscoverInjectCatalog / __zgjCoverFault 同路数）：
      // 注入「查到下一季」的行，让 R6 的回写链（api.setNextSeason → 追剧记录 nextDrama）
      // 能在 mock 下被端到端断言，而不必篡改 mock 基础数据集（那会牵动一堆别的断言）。
      // 只在数组非空时生效，产品路径完全不变；注入值同样走 groupSeasons，不绕过归并逻辑。
      const injected = (window as any)?.__zgjNextSeasonRows as Drama[] | undefined
      if (Array.isArray(injected) && injected.length) {
        entry = { at: Date.now(), rows: groupSeasons(injected, title) }
      } else {
        const res = await api.catalog({ source, query: baseTitle(title) || title, page: 1, lane: 'series:next' })
        entry = { at: Date.now(), rows: groupSeasons(res.items ?? [], title) }
        cache.set(cacheKey, entry)
      }
    }
    const found = entry.rows
      .filter((r) => Math.trunc(Number(r.season ?? 0)) > current)
      .sort((a, b) => a.season - b.season)[0]
    if (!found) return null
    if (String(found.drama.id ?? '') === String(drama.id ?? '')) return null
    prompted.add(promptKey)
    return { drama: found.drama, season: found.season, unit: found.unit, title: found.title }
  } catch {
    return null // 网络失败静默（v1 error 分支只打状态栏，不打断播放）
  } finally {
    busy.delete(key)
  }
}

/** 测试后门：清空进程内缓存/幂等集合。 */
export function resetNextSeasonState() {
  prompted.clear()
  busy.clear()
  cache.clear()
}
