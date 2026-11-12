/* watch store —— 观看历史 / 追剧·预约 / 看过标记（真值源 = Go watchstore 绑定）。
   数据面：api.watchlist()/historyList()（bindings_watchstore.go，camelCase 行 JSON，
   updated/added 为 epoch 秒，position/duration 为秒）；
   看过标记 api.seenList()/toggleSeen()（bindings_seen.go → seen-library.json，epoch 秒；
   真实桥首载时把 localStorage 存量标记一次性 ImportSeen 上传后清本地键）。
   localStorage（services/local.ts）仅保留为「无桥降级缓存」：
   - MOCK / DEGRADED（api.ts 判定：VITE_MOCK=1 或启动即无桥）：读写直接走 local.ts；
   - 真实桥竞态抛 bridge_missing：写操作回落 local.ts 同名函数重试一次。
   写操作乐观更新，失败回滚并暴露中文错误文本（this.error）。 */
import { defineStore } from 'pinia'
import { api, DEGRADED, MOCK_MODE } from '@/services/api'
import { tf } from '@/services/i18n'
import type { Drama, HistoryRow, WatchRow } from '@/services/types'
import {
  clearHistory as clearHistoryLocal,
  clearWatchedMarks,
  deleteHistoryEntry,
  HISTORY_KEY,
  historyKey,
  isFollowed as isFollowedLocal,
  readFollows,
  readHistory,
  readReservations,
  readWatched,
  splitHistoryKey,
  toggleFollow as toggleFollowLocal,
  toggleReservation as toggleReservationLocal,
  toggleWatched as toggleWatchedLocal
} from '@/services/local'

/** 桥不可用类错误（api.ts wailsApp/callGoRaw 抛出形状） */
function isBridgeDown(e: unknown): boolean {
  const err = e as { code?: string; message?: string }
  return err?.code === 'bridge_missing' || /未检测到 Wails|原生方法.*不存在|桥不可用/.test(String(err?.message ?? ''))
}

/* R9c-2：原先这里用 `${action}失败：${m}` 拼中文句子，9 个调用点各传一个中文动词。
   拼接式文案换语言就不成句子了，所以整句进 i18n（watch.err*，zh 与旧输出逐字一致），
   本函数随之删除——留着就是没人调的死码。 */

/* ---------------------------------------------------------------- 降级面映射
   与 api.ts mockApi.watchlist/historyList 同源形状（api.ts:319-347），但两处修正：
   1. 键切分用 local.splitHistoryKey（末位冒号），api.ts mock 用 split('#') 与写入键
      `${dramaId}:${episode}` 不符，真实模式不受影响，降级面绕开该映射。
   2. 写操作直接落 local.ts，不经 mockApi（其 removeHistory 同样受 '#' 切分影响）。 */

function localFollowRows(): WatchRow[] {
  const res = readReservations()
  return readFollows().map((f) => {
    const reserved = !!res[f.drama.id]
    return {
      id: f.drama.id,
      drama: f.drama,
      status: reserved ? '预约中' : '在看',
      known: f.drama.episodes ?? 0,
      read: 0,
      added: f.ts / 1000,
      season: 1,
      followed: !reserved,
      reserved
    }
  })
}

function localHistoryRows(): HistoryRow[] {
  const rows: HistoryRow[] = []
  for (const [key, e] of Object.entries(readHistory())) {
    const id = e.dramaId || splitHistoryKey(key).dramaId
    rows.push({
      drama: { id, source: id.split(':')[0] ?? '', title: e.title, cover: e.cover ?? '' } as Drama,
      episode: e.episode,
      position: e.position,
      duration: e.duration,
      updated: e.ts / 1000
    })
  }
  return rows.sort((a, b) => b.updated - a.updated)
}

/** 降级面写：本地追剧开关（对齐 mockApi.toggleFollow 语义） */
function localToggleFollow(drama: Drama): boolean {
  return toggleFollowLocal(drama)
}

/** 降级面写：本地预约（v1 互斥：预约即取消追，对齐 mockApi.reserve） */
function localReserve(drama: Drama): boolean {
  const on = toggleReservationLocal(drama.id)
  if (on && isFollowedLocal(drama.id)) toggleFollowLocal(drama)
  return on
}

function localCancelReservation(id: string): void {
  if (readReservations()[id]) toggleReservationLocal(id)
}

function localRemoveFollow(id: string): void {
  if (isFollowedLocal(id)) toggleFollowLocal({ id } as Drama)
}

function localSaveWatch(drama: Drama, episode: number, position: number, duration: number): void {
  const all = readHistory()
  all[historyKey(drama.id, episode)] = {
    title: drama.title,
    episode,
    position,
    duration,
    ts: Date.now(),
    dramaId: drama.id,
    cover: drama.cover
  }
  localStorage.setItem(HISTORY_KEY, JSON.stringify(all))
}

function localRemoveHistory(dramaId: string): void {
  for (const [key, e] of Object.entries(readHistory())) {
    if ((e.dramaId || splitHistoryKey(key).dramaId) === dramaId) deleteHistoryEntry(key)
  }
}

/* ---------------------------------------------------------------- 行整形辅助 */

function provisionalRow(drama: Drama, followed: boolean, reserved: boolean): WatchRow {
  return {
    id: drama.id,
    drama,
    status: reserved ? '预约中' : '在看',
    known: drama.episodes ?? 0,
    read: 0,
    added: Date.now() / 1000,
    season: 1,
    followed,
    reserved
  }
}

/** toggleFollow 真值回写：followed=true 保证行存在且标志对齐；false 移除行
    （Go ToggleFollow「已追则删行」同义；预约行不受追开关影响则只翻标志）。 */
function reconcileFollow(rows: WatchRow[], drama: Drama, followed: boolean): WatchRow[] {
  const cur = rows.find((r) => r.id === drama.id)
  if (followed) {
    if (!cur) return [provisionalRow(drama, true, false), ...rows]
    return rows.map((r) => (r.id === drama.id ? { ...r, followed: true, reserved: false, status: '在看' } : r))
  }
  if (!cur) return rows
  if (cur.reserved) return rows.map((r) => (r.id === drama.id ? { ...r, followed: false, status: '预约中' } : r))
  return rows.filter((r) => r.id !== drama.id)
}

function reconcileReserve(rows: WatchRow[], drama: Drama, reserved: boolean): WatchRow[] {
  const cur = rows.find((r) => r.id === drama.id)
  if (reserved) {
    if (!cur) return [provisionalRow(drama, false, true), ...rows]
    return rows.map((r) => (r.id === drama.id ? { ...r, reserved: true, followed: false, status: '预约中' } : r))
  }
  if (!cur) return rows
  if (cur.followed) return rows.map((r) => (r.id === drama.id ? { ...r, reserved: false, status: '在看' } : r))
  return rows.filter((r) => r.id !== drama.id)
}

/* ---------------------------------------------------------------- 看过标记辅助
   seen 表值统一 epoch 秒（Go SeenMarks 契约）；local.ts 历史写法为 ms，
   读取处归一（normTs 同口径：>1e11 判毫秒）。 */

function seenToSec(v: number): number {
  return v > 1e11 ? v / 1000 : v
}

function localSeenMap(): Record<string, number> {
  const out: Record<string, number> = {}
  for (const [k, v] of Object.entries(readWatched())) out[k] = seenToSec(Number(v) || 0)
  return out
}

/* ---------------------------------------------------------------- store */

export const useWatchStore = defineStore('watch', {
  state: () => ({
    follows: [] as WatchRow[],
    history: [] as HistoryRow[],
    /** 看过标记 { dramaId: epoch 秒 }；真值 = Go seen-library.json，降级面 = local.ts */
    seen: {} as Record<string, number>,
    loading: false,
    error: '',
    /** 已首载标志（视图用它避免重复拉取；核心四字段之外的唯一扩展位） */
    loaded: false,
    seenLoaded: false
  }),
  getters: {
    /** 追更列表（v1：followed 行；预约行 Go FollowList 也返回，标志位区分） */
    followedRows: (s) => s.follows.filter((r) => r.followed),
    isSeen: (s) => (dramaId: string): boolean => !!dramaId && s.seen[dramaId] !== undefined
  },
  actions: {
    /** 是否走降级缓存面（与 api.ts DEGRADED/mock 判定同风格） */
    useLocalLayer(): boolean {
      return MOCK_MODE || DEGRADED
    },

    /** 桥面调用；降级面直接走 local.ts；真实桥竞态 bridge_missing 回落 local.ts 一次 */
    async callBridge<T>(run: () => Promise<T>, local: () => T): Promise<T> {
      if (this.useLocalLayer()) return local()
      try {
        return await run()
      } catch (e) {
        if (isBridgeDown(e)) return local()
        throw e
      }
    },

    async load(force = false) {
      if (this.loading || (this.loaded && !force)) return
      this.loading = true
      this.error = ''
      try {
        const [follows, history] = await Promise.all([
          this.callBridge(() => api.watchlist(), localFollowRows),
          this.callBridge(() => api.historyList(), localHistoryRows)
        ])
        this.follows = follows ?? []
        this.history = history ?? []
      } catch (e) {
        this.error = tf('watch.errLoadRows', { m: (e as Error)?.message ?? String(e) })
      } finally {
        this.loading = false
        this.loaded = true
      }
    },

    ensureLoaded() {
      return this.load(false)
    },

    /** 看过标记首载（详情页按钮/角标消费前调用；seenLoaded 幂等）。
        真实桥路径附带一次性迁移：localStorage 存量 ImportSeen 上传 → 清本地键。 */
    async loadSeen(force = false) {
      if (this.seenLoaded && !force) return
      try {
        if (this.useLocalLayer()) {
          this.seen = localSeenMap()
        } else {
          const goMap = await api.seenList()
          this.seen = goMap ?? {}
          const local = localSeenMap()
          const pending = Object.keys(local).filter((k) => this.seen[k] === undefined)
          if (pending.length) {
            try {
              await api.importSeen(JSON.stringify(local))
              clearWatchedMarks()
              for (const k of pending) this.seen[k] = local[k]
            } catch {
              /* 上传失败：localStorage 键保留待下次迁移，本会话按 local 并集显示 */
              for (const k of pending) this.seen[k] = local[k]
            }
          }
        }
        this.seenLoaded = true
      } catch (e) {
        this.seen = localSeenMap() // 桥读失败 → 降级缓存面（下次 force 重试）
        this.error = tf('watch.errLoadSeen', { m: (e as Error)?.message ?? String(e) })
      }
    },

    /** 看过开关（详情页按钮；纯标记，不改播放进度——v1 MarkWatched 的进度副作用刻意不采用）。
        返回切换后的真值。 */
    async toggleSeen(dramaId: string): Promise<boolean> {
      if (!dramaId) return false
      const prev = this.seen[dramaId]
      const had = prev !== undefined
      const will = !had
      if (had) delete this.seen[dramaId]
      else this.seen[dramaId] = Date.now() / 1000
      try {
        const res = await this.callBridge(
          () => api.toggleSeen(dramaId),
          () => ({ on: toggleWatchedLocal(dramaId) })
        )
        const on = !!res.on
        if (on === will) return on
        if (on) this.seen[dramaId] = Date.now() / 1000
        else delete this.seen[dramaId]
        return on
      } catch (e) {
        if (had) this.seen[dramaId] = prev
        else delete this.seen[dramaId]
        this.error = tf('watch.errToggleSeen', { m: (e as Error)?.message ?? String(e) })
        throw e
      }
    },

    /** 追剧开关（详情页按钮）。返回切换后的 followed 真值。 */
    async toggleFollow(drama: Drama): Promise<boolean> {
      const prev = this.follows
      const cur = prev.find((r) => r.id === drama.id)
      const will = !(cur && cur.followed)
      this.follows = reconcileFollow(prev, drama, will)
      try {
        const res = await this.callBridge(
          () => api.toggleFollow(drama),
          () => ({ followed: localToggleFollow(drama) })
        )
        const on = !!res.followed
        if (on !== will) this.follows = reconcileFollow(this.follows, drama, on)
        return on
      } catch (e) {
        this.follows = prev
        this.error = tf('watch.errFollow', { m: (e as Error)?.message ?? String(e) })
        throw e
      }
    },

    /** 取消追剧（追更列表「取消追剧」按钮，Go RemoveFollow 删行语义）。 */
    async removeFollow(dramaId: string): Promise<void> {
      const prev = this.follows
      this.follows = prev.filter((r) => r.id !== dramaId)
      try {
        await this.callBridge(
          () => api.removeFollow(dramaId),
          () => {
            localRemoveFollow(dramaId)
            return { ok: true }
          }
        )
      } catch (e) {
        this.follows = prev
        this.error = tf('watch.errUnfollow', { m: (e as Error)?.message ?? String(e) })
        throw e
      }
    },

    /** 预约开关（日历/卡片「预约」按钮；v1 互斥：预约同时取消追剧，对齐 calendar.ts toggleReserve）。
        Go Reserve 置真不可反向，取消走 CancelReservation。返回切换后的 reserved 真值。 */
    async reserve(drama: Drama): Promise<boolean> {
      const prev = this.follows
      const cur = prev.find((r) => r.id === drama.id)
      const will = !(cur && cur.reserved)
      this.follows = reconcileReserve(prev, drama, will)
      try {
        const res = await this.callBridge<{ reserved: boolean }>(
          () =>
            will
              ? api.reserve(drama)
              : api.cancelReservation(drama.id).then(() => ({ reserved: false })),
          () => ({ reserved: localReserve(drama) })
        )
        const on = !!res.reserved
        if (on !== will) this.follows = reconcileReserve(this.follows, drama, on)
        return on
      } catch (e) {
        this.follows = prev
        this.error = tf('watch.errReserve', { m: (e as Error)?.message ?? String(e) })
        throw e
      }
    },

    /** 取消预约。 */
    async cancelReservation(dramaId: string): Promise<void> {
      const prev = this.follows
      const cur = prev.find((r) => r.id === dramaId)
      if (cur) this.follows = reconcileReserve(prev, cur.drama, false)
      try {
        await this.callBridge(
          () => api.cancelReservation(dramaId),
          () => {
            localCancelReservation(dramaId)
            return { ok: true }
          }
        )
      } catch (e) {
        this.follows = prev
        this.error = tf('watch.errUnreserve', { m: (e as Error)?.message ?? String(e) })
        throw e
      }
    },

    /** 播放进度写入（player store shutdown 接线；调用方自行静默 catch）。
        乐观：本地 history 表按「每剧一行」upsert（与 Go SaveWatch 同键语义）。 */
    async saveWatch(drama: Drama, episode: number, position: number, duration: number, finished: boolean): Promise<void> {
      if (!drama?.id || episode <= 0) return
      const prev = this.history
      const row: HistoryRow = {
        drama: { ...drama },
        episode,
        position,
        duration,
        updated: Date.now() / 1000
      }
      if (this.loaded) {
        this.history = [row, ...prev.filter((r) => r.drama.id !== drama.id)].sort((a, b) => b.updated - a.updated)
      }
      try {
        await this.callBridge(
          () => api.saveWatch(drama, episode, position, duration, finished),
          () => {
            localSaveWatch(drama, episode, position, duration)
            return { ok: true }
          }
        )
      } catch (e) {
        if (this.loaded) this.history = prev
        throw e
      }
    },

    /** 删除某部剧的历史（Go/local 均为整剧删除：watchstore 每剧一行）。 */
    async removeHistory(dramaId: string): Promise<void> {
      const prev = this.history
      this.history = prev.filter((r) => r.drama.id !== dramaId)
      try {
        await this.callBridge(
          () => api.removeHistory(dramaId),
          () => {
            localRemoveHistory(dramaId)
            return { ok: true }
          }
        )
      } catch (e) {
        this.history = prev
        this.error = tf('watch.errDelete', { m: (e as Error)?.message ?? String(e) })
        throw e
      }
    },

    async clearHistory(): Promise<void> {
      const prev = this.history
      this.history = []
      try {
        await this.callBridge(
          () => api.clearHistory(),
          () => {
            clearHistoryLocal()
            return { ok: true }
          }
        )
      } catch (e) {
        this.history = prev
        this.error = tf('watch.errClear', { m: (e as Error)?.message ?? String(e) })
        throw e
      }
    }
  }
})
