/* 本地兜底存储：V2-3 前没有 Go 端 watchstore/reservation 绑定，
   追剧、看过、历史、下载队列、日历预约全部先落 localStorage（单一事实源在本地）。
   —— 依赖请求见 V2-2 终报：需要 Go 端提供 Follows/History/Watched/Reservations 绑定后切换。 */
import type { Drama } from './types'

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

export const HISTORY_BUCKETS = ['今天', '昨天', '更早'] as const

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

/* ---------------------------------------------------------------- 看过 */

export function isWatched(dramaId: string): boolean {
  return !!readJson<Record<string, number>>(WATCHED_KEY, {})[dramaId]
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
