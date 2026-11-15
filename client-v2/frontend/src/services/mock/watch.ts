/* mock/降级数据面：追剧、历史、预约、看过、观察、下一季——全部落 ./local（localStorage），
   与无桥 DEGRADED 时 stores/watch.ts 回落到 local.ts 的是同一份数据，
   所以「mock 环境下真正被走的」其实是这一族函数（历史上观测缝打在 api.saveWatch
   却读出 null 的那条死路径教训见 mock/index.ts 文件头清单第 4 条）。
   ⚠ 这里**不复刻** Go 的里程碑/旗标规则（store.go:1176、:1327-1376）——
     复刻等于造第二个真相；断言因此打在「有没有带着 finished=true 调用过」这类
     入参观测缝上，而不是打在 mock 的状态上。 */
import type { Drama, HistoryRow, NativeApi, WatchRow } from '../types'
import {
  clearHistory as clearHistoryLocal,
  deleteHistoryEntry,
  HISTORY_KEY,
  historyKey,
  isFollowed,
  markNextSeason as localMarkNextSeason,
  readFollows,
  readHistory,
  readReservations,
  readWatched,
  toggleFollow,
  toggleReservation,
  rememberReservation,
  readReservationDramas,
  forgetReservation,
  toggleWatched,
  WATCHED_KEY
} from '../local'

export const mockWatchApi = {
  setNextSeason: async (dramaID: string, nextDrama: Drama) => ({
    marked: localMarkNextSeason(dramaID, nextDrama),
  }),
  // mock/降级面的 observeDrama **不模拟旗标推进**：known/newSeason/arrived 怎么变
  // 是 Go 里 observe_drama 的规格（store.go:1176 + 单测），在这里再抄一遍必然漂。
  // 所以 mock 只做两件诚实的事：报告这条在不在追剧表、把行原样回吐。
  // ⚠ 也就是说"检测出 N 部有更新"这条**没在 mock 里被证明**，只有 Go 单测证明过。
  observeDrama: async (drama: Drama) => {
    const rows = readFollows()
    const hit = rows.find((r) => r.drama.id === drama.id)
    return { observed: !!hit, changed: false, row: null as WatchRow | null }
  },
  // mock：追剧/历史映射 local.ts（浏览器演示与壳内降级共用同一数据面）；日历用 fixtures
  watchlist: async () => {
    // 追剧行 + 「只预约没追剧」的行（v1 互斥后 follow 行不存在，但预约必须是列表里的一条）
    const reservedOnly = Object.entries(readReservationDramas())
      .filter(([id]) => !readFollows().some((f) => f.drama.id === id))
      .map(([, d]) => ({ drama: d, ts: 0 }))
    return [...readFollows(), ...reservedOnly].map((f) => {
      const reserved = !!readReservations()[f.drama.id]
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
  },
  historyList: async () => {
    const rows: HistoryRow[] = []
    for (const [key, e] of Object.entries(readHistory())) {
      const dramaId = key.slice(0, key.lastIndexOf(':'))
      rows.push({
        drama: { id: dramaId, title: e.title, cover: e.cover ?? '' } as Drama,
        episode: e.episode,
        position: e.position,
        duration: e.duration,
        updated: e.ts / 1000,
        // Go 侧 HistoryEntry.ToRowJSON 会带 localPath（store.go:395）；
        // 这里不跟着带，mock 下"本地播放"入口就永远出不来 ——
        // 那是证据装置缺字段，不是产品没做（history_local_play_verify ① 会直接红）。
        localPath: e.localPath
      })
    }
    return rows.sort((a, b) => b.updated - a.updated)
  },
  toggleFollow: async (drama) => ({ followed: toggleFollow(drama) }),
  reserve: async (drama) => {
    const on = toggleReservation(drama.id)
    if (on) {
      rememberReservation(drama)          // 互斥之后没有 follow 行了，靠它把行补回来
      if (isFollowed(drama.id)) toggleFollow(drama) // v1 互斥：预约即取消追
    } else {
      forgetReservation(drama.id)
    }
    return { reserved: on }
  },
  cancelReservation: async (id) => {
    if (readReservations()[id]) toggleReservation(id)
    forgetReservation(id)
    return { ok: true }
  },
  removeFollow: async (id) => {
    if (isFollowed(id)) toggleFollow({ id } as Drama)
    return { ok: true }
  },
  saveLocalWatch: async (drama, path, pos, dur, fin) => {
    // 同上：如实记入参，不复刻 Go 规则
    if (typeof window !== 'undefined') {
      ;(window as any).__zgjLastSaveWatch = { localPath: path, position: pos, duration: dur, finished: fin }
    }
    const all = readHistory()
    const key = historyKey(drama.id, 1) // v1：本地文件一律记 episode=1
    all[key] = { ...(all[key] ?? {}), title: drama.title, episode: 1, position: pos,
                 duration: dur || all[key]?.duration || 0, ts: Date.now(),
                 dramaId: drama.id, cover: drama.cover, localPath: path }
    localStorage.setItem(HISTORY_KEY, JSON.stringify(all))
    return { ok: true }
  },
  saveWatch: async (drama, ep, pos, dur, fin) => {
    /* ⚠ 发版前本机试跑时发现（我没写轮次号 —— 上次凭记忆写轮次号写错过，
       这里原来签名只到 `dur`，第 5 个实参 `finished` 被**默默丢掉**。而真实链路是
       `api.saveWatch(..., finished)` → `App.SaveWatch(..., finished bool)`
       → `Store.SaveWatch` 用它推进 known/「已看」（store.go:1327-1376）。
       也就是说 mock 面上这条语义完全不可见 —— 所以"播完不落 finished"
       这种 bug 在 28 只 Playwright 全绿下潜伏了很多轮。

       修法只做一件诚实的事：**如实记下入参**（下面的测试缝），
       不复刻 Go 的里程碑规则——Go 的 history 行里也没有 finished 字段
       （store.go:392-393），复刻就等于造第二个真相。
       断言因此打在"有没有带着 finished=true 调用过"，而不是打在 mock 的状态上。 */
    if (typeof window !== 'undefined') {
      ;(window as any).__zgjLastSaveWatch = { episode: ep, position: pos, duration: dur, finished: fin }
    }
    const all = readHistory()
    all[historyKey(drama.id, ep)] = {
      title: drama.title, episode: ep, position: pos, duration: dur,
      ts: Date.now(), dramaId: drama.id, cover: drama.cover
    }
    localStorage.setItem(HISTORY_KEY, JSON.stringify(all))
    return { ok: true }
  },
  removeHistory: async (id) => {
    for (const key of Object.keys(readHistory())) if (key.slice(0, key.lastIndexOf(':')) === id) deleteHistoryEntry(key)
    return { ok: true }
  },
  clearHistory: async () => {
    clearHistoryLocal()
    return { ok: true }
  },
  // mock：看过标记落 local.ts（mock/降级与真实桥同语义；值归一为 epoch 秒对齐 Go SeenList 契约）
  seenList: async () => {
    const out: Record<string, number> = {}
    for (const [k, v] of Object.entries(readWatched())) out[k] = v > 1e11 ? v / 1000 : v
    return out
  },
  toggleSeen: async (id) => ({ on: toggleWatched(id) }),
  importSeen: async (entriesJSON) => {
    let entries: Record<string, number>
    try {
      entries = JSON.parse(entriesJSON) as Record<string, number>
    } catch {
      return { added: 0 }
    }
    const cur = readWatched()
    let added = 0
    for (const [k, v] of Object.entries(entries)) {
      if (!k || Object.prototype.hasOwnProperty.call(cur, k)) continue // 已有 id 不覆盖（Go 真值优先）
      cur[k] = v
      added++
    }
    if (added) localStorage.setItem(WATCHED_KEY, JSON.stringify(cur))
    return { added }
  }
} satisfies Pick<
  NativeApi,
  'setNextSeason' | 'observeDrama' | 'watchlist' | 'historyList' | 'toggleFollow' | 'reserve' |
  'cancelReservation' | 'removeFollow' | 'saveLocalWatch' | 'saveWatch' | 'removeHistory' |
  'clearHistory' | 'seenList' | 'toggleSeen' | 'importSeen'
>
