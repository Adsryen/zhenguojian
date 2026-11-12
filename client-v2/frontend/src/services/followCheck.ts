/* services/followCheck.ts —— v1 `main.py:776-860 _check_follows` 的端口（R23）

v1 的原话是「后台刷新追剧列表：有新集/新季才提示，失败不打扰用户」，
它做的事情按顺序是：

1. 挑出要检查的行：`status ∈ {想看, 在看, 预约中}` 或 `reserved`，**上限 50 行**；
2. 每条 `core.detail(drama)` 拿最新剧数据 → `watchstore.observe_drama(merged)`
   （旗标怎么变由 observe 决定，这里不自己算）；
3. 预约的目标是**下一季**时，详情接口只能确认当前季，所以再按基础标题搜一次同系列
   （v1 lane=`series:auto`，同 seriesKey 结果本轮内缓存），
   找到目标季且 id 与当前剧不同 → `mark_next_season(...)` 把入口记回旧季行；
   这一段每条最多一次、整轮**上限 12 次搜索**（v1 `lookups < 12`）；
4. 用行快照 `(known, newSeason, arrived, newEpisodes)` 比较前后，统计"几部有更新"；
5. 节奏：每条之间 800ms、每次系列搜索后 300ms（v1 就是这么避锤引擎的）；
6. 重入保护：`_follow_busy`。

驱动它的是 `main.py:714-726`：默认 **1800s**，`ZHENGUO_FOLLOW_INTERVAL` 可覆盖，
下限 **60s**，启动后 **60s 先跑一次**（避开启动高峰）。
v2 里 env 变量不好在 WebView2 侧设，所以覆盖口换成 localStorage
`zgj.follow.interval`（同样只用于调试，正常用户不会碰到）。

⚠ 为什么"遍历 + 季号归并"放在 TS 而不是 Go：
Go 那边 `Store.ObserveDrama` 早就有（store.go:1176，带单测），本文件只调它；
而 `base_title/series_key/group/find_season/next_season` 的**权威实现已经在 TS**
（`services/series.ts` 是 v1 `series.py` 的 1:1 端口，有断言钉着）。
再往 Go 抄一份就是同一个规格两处实现，一定会漂。

提示语也只陈述真发生的事：v1 的「追剧检查：%d 部有更新」在这里是
一条 hint（不打断操作），失败时按 v1 给"暂时取不到更新"而不是报错弹窗。
*/
import { api } from './api'
import { baseTitle, findSeason, groupSeasons, nextSeason, seriesKey } from './series'
import { tf } from './i18n'
import { useWatchStore } from '@/stores/watch'
import type { Drama, WatchRow } from './types'

/** v1 main.py:718-726 的三个数字，原样搬过来 */
export const CHECK_INTERVAL_DEFAULT = 1800   // 秒
export const CHECK_INTERVAL_FLOOR = 60       // 秒
export const FIRST_RUN_DELAY = 60_000        // 毫秒：启动后 60s 先跑一次
export const ROW_CAP = 50                    // 单轮最多检查多少行
export const SERIES_LOOKUP_CAP = 12          // 单轮最多做几次同系列搜索
export const PER_ROW_DELAY = 800             // 毫秒
export const PER_LOOKUP_DELAY = 300          // 毫秒

const INTERVAL_KEY = 'zgj' + '.follow.interval'

export interface CheckResult {
  checked: number
  updated: number
  failed: number
  skipped: boolean
}

export function checkIntervalSec(): number {
  let raw: string | null = null
  try {
    raw = localStorage.getItem(INTERVAL_KEY)
  } catch {
    raw = null
  }
  const n = raw == null ? CHECK_INTERVAL_DEFAULT : Number(raw)
  const sec = Number.isFinite(n) ? Math.trunc(n) : CHECK_INTERVAL_DEFAULT
  return Math.max(CHECK_INTERVAL_FLOOR, sec)
}

/** v1 的 `before/after` 快照元组，逐字段照抄（顺序也要一致，比较靠它） */
function rowTuple(row: WatchRow): string {
  return JSON.stringify([
    Math.trunc(Number(row.known ?? 0)),
    !!row.newSeason,
    !!row.arrived,
    Math.trunc(Number(row.newEpisodes ?? 0)),
  ])
}

function shouldCheck(row: WatchRow): boolean {
  const status = String(row.status ?? '')
  // v1: status in ("想看", "在看", "预约中") or row.get("reserved")
  return status === '想看' || status === '在看' || status === '预约中' || !!row.reserved
}

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))

/** 挑出要检查的行（v1 顺序 + 上限 50） */
export function pickRows(rows: WatchRow[]): WatchRow[] {
  return rows.filter(shouldCheck).slice(0, ROW_CAP)
}

/**
 * 跑一轮检查。`opts.delayMs` 只是测试/调试用的节奏覆盖，默认走 v1 的 800/300ms。
 * 并发保护照 v1 `_follow_busy`：上一轮没跑完就直接跳过这一轮。
 */
export async function runFollowCheck(opts: { delayMs?: number } = {}): Promise<CheckResult> {
  if (running) return { checked: 0, updated: 0, failed: 0, skipped: true }
  running = true
  const rowDelay = opts.delayMs ?? PER_ROW_DELAY
  const lookupDelay = opts.delayMs ?? PER_LOOKUP_DELAY
  const watch = useWatchStore()
  let updated = 0
  let failed = 0
  let checked = 0
  let lookups = 0
  const cache = new Map<string, ReturnType<typeof groupSeasons>>()

  try {
    const rows = pickRows(watch.follows)
    const before = new Map<string, string>()
    for (const row of rows) before.set(row.id, rowTuple(row))

    for (const row of rows) {
      checked++
      try {
        const detail = await api.detail(row.drama)
        const merged: Drama = { ...row.drama, ...(detail?.drama ?? {}) }
        const res = await api.observeDrama(merged)
        if (!res?.observed) {
          // v1：没这条追剧记录时 observe_drama 直接 return。这里也不当成"更新"。
          continue
        }
        // 预约的是下一季时，详情只能确认当前季；再按标题搜同系列（v1 注释原话）
        const fresh = useWatchStore().follows.find((r) => r.id === row.id) ?? row
        const target = Math.trunc(Number(fresh.reserveSeason ?? 0))
        const current = Math.trunc(Number(fresh.season ?? 1))
        if (fresh.reserved && !fresh.arrived && target > current && lookups < SERIES_LOOKUP_CAP) {
          const title = String(merged.title ?? fresh.drama?.title ?? '')
          const key = seriesKey(title)
          lookups++
          let found = cache.get(key)
          if (!found) {
            const page = await api.catalog({
              source: String(merged.source ?? fresh.drama?.source ?? 'hongguo'),
              query: baseTitle(title) || title,
              page: 1,
              lane: 'series:auto',
            })
            found = groupSeasons(page?.items ?? [], title)
            cache.set(key, found)
          }
          const hit = findSeason(found, target) ?? nextSeason(found, current)
          const hitDrama = hit?.drama
          if (hit && hitDrama && String(hitDrama.id ?? '') !== String(merged.id ?? '')) {
            await api.setNextSeason(row.id, hitDrama)
          }
          await sleep(lookupDelay)
        }
        const after = useWatchStore().follows.find((r) => r.id === row.id)
        const b = before.get(row.id)
        if (after && b !== undefined && rowTuple(after) !== b) updated++
      } catch {
        // v1：一条坏剧不能打断整轮
        failed++
      }
      if (rowDelay) await sleep(rowDelay)
    }

    if (updated > 0) watch.setCheckHint(tf('watch.checkedUpdated', { n: updated }), 8000)
    else if (failed > 0) watch.setCheckHint(tf('watch.checkedFailed', { n: failed }), 6000)
  } finally {
    running = false
  }
  return { checked, updated, failed, skipped: false }
}

let running = false
let timer: ReturnType<typeof setInterval> | null = null
let firstShot: ReturnType<typeof setTimeout> | null = null

/** 启动后台检查（v1：启动 60s 后先跑一次，然后按间隔循环）。重复调用不会叠定时器。 */
export function startFollowCheck(): void {
  if (timer || firstShot) return
  firstShot = setTimeout(() => {
    firstShot = null
    void runFollowCheck()
    const ms = checkIntervalSec() * 1000
    timer = setInterval(() => void runFollowCheck(), ms)
  }, FIRST_RUN_DELAY)
}

/* 测试缝（与本仓 `__zgjSoraniLastLogin` / `__zgjNextSeasonRows` 同路数）：
 * 产品路径不读这些名字；它们存在的唯一理由是 v1 的节奏是
 * 「启动后 60s 先跑一次、之后每 1800s」——验收脚本不可能真等一分钟，
 * 也没有别的办法从页面外触发一整轮。导出的都是**同一份实现**，不是复制品。 */
export function installFollowCheckTestSeam(): void {
  if (typeof window === 'undefined') return
  const w = window as any
  w.__zgjFollowCheck = runFollowCheck
  w.__zgjFollowCheckInterval = checkIntervalSec
  w.__zgjFollowCheckPick = pickRows
  w.__zgjFollowCheckCaps = { ROW_CAP, SERIES_LOOKUP_CAP, PER_ROW_DELAY, PER_LOOKUP_DELAY, FIRST_RUN_DELAY }
}

export function stopFollowCheck(): void {
  if (firstShot) {
    clearTimeout(firstShot)
    firstShot = null
  }
  if (timer) {
    clearInterval(timer)
    timer = null
  }
  running = false
}
