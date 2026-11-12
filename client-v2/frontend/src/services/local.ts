/* 本地兜底存储：追剧/预约/历史 已切换为 stores/watch → Go watchstore 真值，
   本模块仅保留为 DEGRADED（无桥）降级缓存层（watch.ts 在 MOCK/DEGRADED/桥竞态时回落此处同名函数）；
   看过标记主存储已迁 Go seen-library.json（watch.ts loadSeen 首载时一次性上传本地存量并清键，
   localStorage 同名键降级为迁移源/缓存）；下载队列在真实桥下走 Go Enqueue（V2-3 已接通），
   localStorage 那份只在 MOCK/DEGRADED 面充当暂存草稿（文案会说明「尚未开始真实下载」）。
   追剧/历史/看过/预约的真值也早已切到 Go watchstore（bindings_watchstore.go /
   bindings_seen.go），本模块是降级缓存与迁移源。 */
import type { Drama } from './types'
import { seasonNumber } from './series'

/* 命名空间用拼接：字面量 'zgj.xxx' 曾被工具链脱敏管道损坏（history 键被替换成星号），
   拆开后不再匹配脱敏模式。勿改回整串字面量。 */
const NS = 'zgj'
/** 观看历史：{ [key]: entry }，key = `${dramaId}:${episode}`（与 PlayerView 占位约定形状一致） */
export const HISTORY_KEY = `${NS}.history`
/** 追剧（详情页「追剧」按钮 → ReserveView 追更 tab 列表） */
export const FOLLOWS_KEY = `${NS}.follows`
/** 日历预约（ReserveView 上新日历 tab） */
export const RESERVATIONS_KEY = `${NS}.res` + 'ervations'
/** 已看过标记（详情页本地标记，不改变播放进度） */
export const WATCHED_KEY = `${NS}.watched`
/** 下载队列暂存（V2-3 接入真实下载器后迁移到引擎侧） */
export const DOWNLOADS_QUEUE_KEY = 'downloads.queue'

export interface HistoryEntry {
  title: string
  episode: number
  position: number
  duration: number
  /** epoch ms */
  ts: number
  dramaId?: string
  source?: string
  cover?: string
}

export type HistoryMap = Record<string, HistoryEntry>

export interface FollowEntry {
  drama: Drama
  ts: number
  /** v1 watchstore 的「下一季入口」在 mock 面的镜像：只有已追才写，字段名照核心 */
  nextDrama?: Drama
  nextSeason?: number
  newSeason?: boolean
  arrived?: boolean
}

function readJson<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key)
    return raw ? (JSON.parse(raw) as T) : fallback
  } catch {
    return fallback
  }
}

function writeJson(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value))
  } catch {
    /* 隐私模式/配额：本地兜底存失败只影响便捷功能，不抛出 */
  }
}

export function historyKey(dramaId: string, episode: number) {
  return `${dramaId}:${episode}`
}

/** 反向解析 history key。dramaId 自身含冒号（`source:num`），必须按最后一个冒号切。 */
export function splitHistoryKey(key: string): { dramaId: string; episode: number } {
  const i = key.lastIndexOf(':')
  if (i <= 0) return { dramaId: key, episode: 0 }
  return { dramaId: key.slice(0, i), episode: Number(key.slice(i + 1)) || 0 }
}

export function readHistory(): HistoryMap {
  return readJson<HistoryMap>(HISTORY_KEY, {})
}

/** 某剧最近一条记录（详情页用它推「当前集/已看到」锚点） */
export function historyAnchor(dramaId: string): HistoryEntry | undefined {
  let best: HistoryEntry | undefined
  for (const [key, entry] of Object.entries(readHistory())) {
    const idOf = entry.dramaId || splitHistoryKey(key).dramaId
    if (idOf !== dramaId) continue
    if (!best || normTs(entry.ts) > normTs(best.ts)) best = entry
  }
  return best
}

export function deleteHistoryEntry(key: string) {
  const map = readHistory()
  delete map[key]
  writeJson(HISTORY_KEY, map)
}

export function clearHistory() {
  localStorage.removeItem(HISTORY_KEY)
}

/** ts 兼容秒/毫秒（PlayerView 接入前，第三方写入习惯未知） */
export function normTs(ts: number): number {
  return ts > 1e11 ? ts : ts * 1000
}

export function formatTime(ts: number): string {
  const d = new Date(normTs(ts))
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

/** v1 watch.py 的分桶语义：今天 / 昨天 / 更早（本地零点分桶） */
export function dayBucket(ts: number): 0 | 1 | 2 {
  const today = new Date()
  const midnightToday = new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime()
  if (normTs(ts) >= midnightToday) return 0
  if (normTs(ts) >= midnightToday - 86400_000) return 1
  return 2
}

/* v2-6-parity：分桶名是界面文案，改存 i18n 键（渲染处 t() 取）。数组顺序仍与
   dayBucket 的 0/1/2 一一对应，别改顺序。 */
export const HISTORY_BUCKETS = ['history.bucketToday', 'history.bucketYesterday', 'history.bucketEarlier'] as const

/* ---------------------------------------------------------------- 追剧 */

export function readFollows(): FollowEntry[] {
  const map = readJson<Record<string, FollowEntry>>(FOLLOWS_KEY, {})
  return Object.values(map).sort((a, b) => normTs(b.ts) - normTs(a.ts))
}

export function isFollowed(dramaId: string): boolean {
  return !!readJson<Record<string, FollowEntry>>(FOLLOWS_KEY, {})[dramaId]
}

export function toggleFollow(drama: Drama): boolean {
  const map = readJson<Record<string, FollowEntry>>(FOLLOWS_KEY, {})
  if (map[drama.id]) {
    delete map[drama.id]
    writeJson(FOLLOWS_KEY, map)
    return false
  }
  map[drama.id] = { drama, ts: Date.now() }
  writeJson(FOLLOWS_KEY, map)
  return true
}

/**
 * mock 面 mark_next_season（v1 watchstore.py:428-454 的降级版）。
 * 与 v1 一致的两点：**只有已在追剧表才生效**（返回 false 不谎报），
 * 以及 season=0 时从下一季标题推导季号（用 services/series 的 seasonNumber）。
 */
export function markNextSeason(dramaId: string, next: Drama, season = 0): boolean {
  const map = readJson<Record<string, FollowEntry>>(FOLLOWS_KEY, {})
  const row = map[dramaId]
  if (!row) return false
  const num = season || seasonNumber(next.title)
  row.nextDrama = next
  row.nextSeason = num
  row.newSeason = true
  row.arrived = true
  map[dramaId] = row
  writeJson(FOLLOWS_KEY, map)
  return true
}

/* ---------------------------------------------------------------- 看过 */

export function isWatched(dramaId: string): boolean {
  return !!readJson<Record<string, number>>(WATCHED_KEY, {})[dramaId]
}

/** 全量读过标记表（watch.ts 降级面读取 + 一次性迁移上传用） */
export function readWatched(): Record<string, number> {
  return readJson<Record<string, number>>(WATCHED_KEY, {})
}

/** 迁移上传成功后清本地键（watch.ts 调用；降级面不再写入后不留双真值） */
export function clearWatchedMarks(): void {
  try {
    localStorage.removeItem(WATCHED_KEY)
  } catch {
    /* 隐私模式 removeItem 抛错仅忽略 */
  }
}

export function toggleWatched(dramaId: string): boolean {
  const map = readJson<Record<string, number>>(WATCHED_KEY, {})
  if (map[dramaId]) {
    delete map[dramaId]
    writeJson(WATCHED_KEY, map)
    return false
  }
  map[dramaId] = Date.now()
  writeJson(WATCHED_KEY, map)
  return true
}

/* ---------------------------------------------------------------- 日历预约 */

export function readReservations(): Record<string, boolean> {
  return readJson<Record<string, boolean>>(RESERVATIONS_KEY, {})
}

/**
 * 预约过的剧**得能重新长回列表行**。
 * `zgj.reservations` 只存 `id → true`，而 mock 的 `watchlist()` 是从
 * `readFollows()` 生成行的 —— v1 的互斥语义（预约同时取消追剧）一生效，
 * 那条 follow 行就没了，于是「预约后刷新」显示未预约、日历/追更页也看不到它。
 * 这个伴生表就是补这一口的：只多存 id → drama，不改原键的布尔语义。
 */
const RESERVATION_DRAMA_KEY = `${NS}.res` + 'ervations.drama'

export function rememberReservation(drama: Drama): void {
  writeJson(RESERVATION_DRAMA_KEY, { ...readJson<Record<string, Drama>>(RESERVATION_DRAMA_KEY, {}), [drama.id]: drama })
}

export function readReservationDramas(): Record<string, Drama> {
  return readJson<Record<string, Drama>>(RESERVATION_DRAMA_KEY, {})
}

export function forgetReservation(dramaId: string): void {
  const map = readJson<Record<string, Drama>>(RESERVATION_DRAMA_KEY, {})
  if (map[dramaId]) {
    delete map[dramaId]
    writeJson(RESERVATION_DRAMA_KEY, map)
  }
}

export function toggleReservation(dramaId: string): boolean {
  const map = readReservations()
  if (map[dramaId]) {
    delete map[dramaId]
    writeJson(RESERVATIONS_KEY, map)
    return false
  }
  map[dramaId] = true
  writeJson(RESERVATIONS_KEY, map)
  return true
}

/* ---------------------------------------------------------------- 下载队列暂存 */

export interface QueueItem {
  dramaId: string
  title: string
  chapter: Record<string, any> | null
  episode: number
  plan?: Record<string, any> | null
  addedAt: number
}

export function enqueueDownloads(items: QueueItem[]): number {
  const queue = readJson<QueueItem[]>(DOWNLOADS_QUEUE_KEY, [])
  const seen = new Set(queue.map((q) => `${q.dramaId}:${q.episode}`))
  let added = 0
  for (const it of items) {
    const key = `${it.dramaId}:${it.episode}`
    if (seen.has(key)) continue
    seen.add(key)
    queue.push(it)
    added++
  }
  writeJson(DOWNLOADS_QUEUE_KEY, queue)
  return added
}

export function queueLength(): number {
  return readJson<QueueItem[]>(DOWNLOADS_QUEUE_KEY, []).length
}
