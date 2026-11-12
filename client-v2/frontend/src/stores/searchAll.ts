/* searchAll —— 搜索页「全部来源」扇出逻辑（纯函数模块，状态由 SearchView 持有）：
   - 勾选态持久化 localStorage zgj.search.all（'1'/'0'，刷新保留）
   - 扇出目标 = orderedSources ∩ (enabled && implemented)，组顺序沿用 orderedSources
   - 简单 worker 池并发上限 4；每源各取 page1
   - lane 按源分流（search:<slug>）：core laneSession 语义（engine.go:161-166）
     同 lane 新请求取消旧请求且跨源共享 session——扇出并发若共用 "search" lane
     会互相取消，只有先发起的存活；分源 lane 后互不干扰且同源重搜仍取消旧请求
   - 每源独立 try/catch：失败只落该组 error 态（文案剥掉尾部 [code]），不影响其他组
   - 验证钩子（参照 covers.ts:112 __zgjCoverFault 先例，仅 VITE_MOCK=1 生效）：
     window.__zgjSearchFault = ['youzi', …]（或含 '*'）→ 对应源请求同步抛错，
     错误文案带 [sim_fault] 尾缀，用于验证失败行的 code 剥除 */
import { api, MOCK_MODE } from '@/services/api'
import { t } from '@/services/i18n'
import type { Drama, SourceListItem } from '@/services/types'

export const SEARCH_ALL_KEY = 'zgj.search.all'
/** 扇出并发上限（worker 池大小） */
export const FANOUT_CONCURRENCY = 4

export type SearchGroupStatus = 'loading' | 'done' | 'error'

/** 一个站源在「全部来源」结果里的一组 */
export interface SearchGroup {
  slug: string
  /** 组头展示名（displayName || name） */
  name: string
  adult: boolean
  status: SearchGroupStatus
  items: Drama[]
  /** status==='error' 时的原因短语（已剥 [code] 尾缀） */
  error?: string
}

export function loadAllPref(): boolean {
  try {
    return localStorage.getItem(SEARCH_ALL_KEY) === '1'
  } catch {
    return false
  }
}

export function saveAllPref(on: boolean): void {
  try {
    localStorage.setItem(SEARCH_ALL_KEY, on ? '1' : '0')
  } catch {
    /* 写不进不致命，会话内生效 */
  }
}

/** 扇出目标：已启用且已接入的源，保持入参数组（= orderedSources）顺序 */
export function fanoutTargets(sources: SourceListItem[]): SourceListItem[] {
  return sources.filter((s) => s.enabled && s.implemented)
}

export function makeGroups(targets: SourceListItem[]): SearchGroup[] {
  return targets.map((s) => ({
    slug: s.slug,
    name: s.displayName || s.name || s.slug,
    adult: !!s.adult,
    status: 'loading' as SearchGroupStatus,
    items: [] as Drama[]
  }))
}

/** 失败原因展示文案：ApiError 在 callGoRaw 已剥 code，但字符串 throw 仍可能带，防御性再剥一次 */
export function cleanErrorMessage(e: unknown): string {
  const msg = e instanceof Error ? e.message : String((e as { message?: unknown })?.message ?? e)
  return (msg || '').replace(/\s*\[[a-zA-Z0-9_-]+\]\s*$/, '').trim() || t('searchAll.errUnknown')
}

/** 验证钩子：仅 mock 面注入源级故障 */
export function maybeInjectFault(slug: string): void {
  if (!MOCK_MODE || typeof window === 'undefined') return
  const faults = (window as unknown as { __zgjSearchFault?: unknown }).__zgjSearchFault
  if (!Array.isArray(faults)) return
  if (faults.includes(slug) || faults.includes('*')) {
    throw new Error(`模拟源故障（${slug}） [sim_fault]`)
  }
}

/** 拉一源 page1 并就地更新组状态；isStale() 为真（已有更新的搜索接管）则静默放弃 */
export async function fetchGroup(
  g: SearchGroup,
  term: string,
  isStale: () => boolean
): Promise<void> {
  g.status = 'loading'
  g.error = undefined
  try {
    maybeInjectFault(g.slug)
    const res = await api.catalog({
      source: g.slug,
      query: term,
      page: 1,
      lane: `search:${g.slug}`
    })
    if (isStale()) return
    g.items = res.items ?? []
    g.status = 'done'
  } catch (e) {
    if (isStale()) return
    g.status = 'error'
    g.error = cleanErrorMessage(e)
  }
}

/** worker 池扇出：先完成的组先渲染（groups 为父级 ref 数组的 reactive 代理，就地写回） */
export async function runFanout(
  groups: SearchGroup[],
  term: string,
  isStale: () => boolean
): Promise<void> {
  let cursor = 0
  const worker = async (): Promise<void> => {
    while (cursor < groups.length) {
      if (isStale()) return
      const g = groups[cursor]
      cursor += 1
      await fetchGroup(g, term, isStale)
    }
  }
  const n = Math.min(FANOUT_CONCURRENCY, groups.length)
  await Promise.all(Array.from({ length: n }, () => worker()))
}
