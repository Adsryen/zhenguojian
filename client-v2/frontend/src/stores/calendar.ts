import { defineStore } from 'pinia'
import { t, tf } from '@/services/i18n'
import { api } from '@/services/api'
import type { CalendarEntry, CalendarItem, CalendarPayload, Drama } from '@/services/types'

/* 追剧日历 store —— 对齐 v1 calendar_panel.py：
   - 数据面 = 核心 case "calendar" 平铺页（items 属于所查询日期，dates 轴，
     defaultDate/hasMore/nextOffset 翻页）；兼容 legacy 嵌套 mock 形状。
   - 展示态 = 已上线在前（v1 items.sort online-first），在线时按 已上线/待上线 分段。
   - 预约/追剧真值 = 本地 watchstore（v1 watch.is_reserved 语义）；
     核心 subscribed 仅作服务端账号侧初值（无本地记录时兜底显示）。 */

export interface CalendarRow {
  id: string // hongguo:seriesId（与详情/播放路由 Drama.id 同源）
  drama: Drama
  online: boolean
  subscribed: boolean // 服务端订阅（账号侧，只读）
  hint: string // 未上线时间文案
  note: string // 已上线=热度；未上线=预计上线文本
  badge: string // 封面角标（title_left_icon）
  publishText: string // sub_info（如「更新至12集」）
}

export function ymd(d: Date): string {
  return `${d.getFullYear()}${String(d.getMonth() + 1).padStart(2, '0')}${String(d.getDate()).padStart(2, '0')}`
}

export function parseYmd(s: string): Date {
  return new Date(Number(s.slice(0, 4)), Number(s.slice(4, 6)) - 1, Number(s.slice(6, 8)))
}

export function addDaysYmd(s: string, n: number): string {
  const d = parseYmd(s)
  d.setDate(d.getDate() + n)
  return ymd(d)
}

export function todayYmd(): string {
  return ymd(new Date())
}

/* 下标 = Date.getDay()（0=周日），存 i18n 键、渲染处 t() 取；顺序即星期，别排成周一开头 */
const WEEKDAYS = ['calendar.wkSun', 'calendar.wkMon', 'calendar.wkTue', 'calendar.wkWed',
              'calendar.wkThu', 'calendar.wkFri', 'calendar.wkSat']

/** YYYYMMDD → YYYY-MM-DD（Drama.onlineDate 展示格式） */
function dayDash(s: string): string {
  return `${s.slice(0, 4)}-${s.slice(4, 6)}-${s.slice(6, 8)}`
}

export function calDateLabel(s: string): string {
  if (!/^\d{8}$/.test(s)) return ''
  if (s === todayYmd()) return t('calendar.today')
  return t(WEEKDAYS[parseYmd(s).getDay()])
}

/** 双形状归一：core 平铺 CalendarEntry → row；legacy 嵌套 CalendarItem（mockCalendarDays）→ row */
function normalizeItem(raw: CalendarItem | CalendarEntry): CalendarRow | null {
  const nested = raw as CalendarItem
  if (nested.drama && nested.drama.id) {
    const d = nested.drama
    return {
      id: d.id,
      drama: d,
      online: !!nested.online,
      subscribed: false,
      hint: String(nested.hint ?? ''),
      note: d.heat ? tf('calendar.heatNote', { h: d.heat }) : '',
      badge: '',
      publishText: d.episodes ? tf('calendar.epCount', { n: d.episodes }) : ''
    }
  }
  const it = raw as CalendarEntry
  const seriesId = it.seriesId ? String(it.seriesId) : ''
  if (!seriesId) return null
  const id = `hongguo:${seriesId}`
  const drama: Drama = {
    id,
    source: 'hongguo',
    sourceId: seriesId,
    title: it.title || t('calendar.unknownTitle'),
    cover: it.cover || '',
    description: it.description || '',
    episodes: it.episodeCnt,
    category: it.category,
    tags: it.tags,
    heat: it.heat,
    onlineDate: it.publishAt ? dayDash(ymd(new Date(it.publishAt))) : undefined
  }
  return {
    id,
    drama,
    online: it.isOnline === true,
    subscribed: it.subscribed === true,
    hint: it.hint || '',
    note: it.note || '',
    badge: it.badge || '',
    publishText: it.publishText || ''
  }
}

export const useCalendarStore = defineStore('calendar', {
  state: () => ({
    date: '', // 当前选中日 YYYYMMDD
    dates: [] as string[], // 核心日期轴
    /** 频道性别筛选（v1 calendar_panel.py CHIP_GENDERS："2"=全部 "1"=男频 "0"=女频）。
        透传 Go Calendar(date, offset, gender) 第三形参（bindings_calendar.go:43 已有）。 */
    gender: 2,
    rows: [] as CalendarRow[],
    loading: false,
    error: '',
    hasMore: false,
    // ⚠ 第五十四轮按 v1 的行为补的三个字段，理由逐条写在用法处：
    // v1 `test_calendar_panel.py` 里有六条测试钉着这些行为
    // （viewport 自动补页、resize 重填、重复页停止并可重试、
    //  快速切换性别丢弃过期响应、失败页同游标重试且不清空、首页不重复请求），
    // v2 之前一条都没测，其中三条在实现里就不存在。
    reqSeq: 0, // 请求代号：只认最后一次发出的那一个
    stalled: false, // 连续多页没有新内容 → 停止自动请求
    dupStreak: 0,
    needsFill: false, // 内容不足一屏，需要再补一页（视图据此调 fillViewport）
    nextOffset: 0,
    pageSize: 0,
    followed: {} as Record<string, boolean>, // 本地 watchstore 追剧态
    reserved: {} as Record<string, boolean>, // 本地 watchstore 预约态
    flagsLoaded: false,
    actionError: '',
    loadedOnce: false
  }),
  getters: {
    onlineRows: (s) => s.rows.filter((r) => r.online),
    pendingRows: (s) => s.rows.filter((r) => !r.online),
    /** 状态行能不能点重试。判据放这里（与文案同源），
     *  因为视图那行文案本身就写着「点此重试」——
     *  第五十四轮之前它是个纯 div：话说出去了、事没做（同第三十四轮"尚未接通"那一族）。
     *  只有两种停摆可点：① 有 error；② 连续多页没有新内容。 */
    retryable(s): boolean {
      return !!s.error || !!s.stalled
    },
    /** v1 状态条文案（加载/错误/空日/没有更多/连续多页无新剧） */
    statusText(s): string {
      if (s.loading && !s.rows.length) return t('calendar.statusFirstLoad')
      if (s.stalled) return t('calendar.statusStalled')
      if (s.error) return tf('calendar.statusFailed', { m: s.error })
      if (!s.rows.length) return t('calendar.statusEmptyDay')
      if (s.loading) return t('calendar.statusLoading')
      if (!s.hasMore) return t('calendar.statusNoMore')
      return ''
    }
  },
  actions: {
    /** 追剧/预约本地标志（v1 watchstore 为展示真值） */
    async loadFlags() {
      try {
        const list = await api.watchlist()
        const followed: Record<string, boolean> = {}
        const reserved: Record<string, boolean> = {}
        for (const row of list) {
          if (!row.id) continue
          followed[row.id] = !!row.followed
          reserved[row.id] = !!row.reserved
        }
        this.followed = followed
        this.reserved = reserved
        this.flagsLoaded = true
      } catch {
        /* 本地库读取失败不阻塞日历渲染 */
      }
    },

    /** v1 _sync_reserved 语义：本地 watchstore 是展示真值；
        服务端 subscribed 仅作本地标志未就绪时的初值 */
    isFollowed(row: CalendarRow): boolean {
      return this.flagsLoaded ? !!this.followed[row.id] : false
    },
    isReserved(row: CalendarRow): boolean {
      return this.flagsLoaded ? !!this.reserved[row.id] : row.subscribed
    },

    async load(date = '') {
      // v1 `test_existing_first_page_is_not_requested_twice`：同一份数据在飞行中
      // 时不再重复发首屏请求。这里保留原来的 loading 短路，
      // 但**不再**用它挡掉"用户新的一次点击"（见 setGender 的 reqSeq 处理）。
      if (this.loading && this.loadedOnce) return
      const seq = ++this.reqSeq
      this.loading = true
      this.error = ''
      this.stalled = false
      this.dupStreak = 0
      try {
        const page: CalendarPayload = await api.calendar(date, 0, this.gender)
        if (seq !== this.reqSeq) return // v1 rapid_gender_change_discards_stale_result
        const list = (page.items ?? []).map(normalizeItem).filter(Boolean) as CalendarRow[]
        this.dates = (page.dates ?? []).filter((d) => /^\d{8}$/.test(String(d)))
        // v1：首载 date="" 时选 defaultDate ?? dates 末位 ?? 今天
        this.date = date || page.defaultDate || (this.dates.length ? this.dates[this.dates.length - 1] : todayYmd())
        this.rows = list
        this.hasMore = !!page.hasMore
        this.nextOffset = Number(page.nextOffset ?? 0)
        this.pageSize = Number((page as any).count ?? 0) || list.length
        if (!this.flagsLoaded) await this.loadFlags()
        // v1 `short_first_pages_fill_viewport_automatically`：
        // 首屏比视口短时不能停在"半屏 + 没有更多"，要自己补到满或到尽头。
        this.needsFill = !!this.hasMore
      } catch (e: any) {
        this.error = e?.message ?? String(e)
      } finally {
        if (seq === this.reqSeq) {
          this.loading = false
          this.loadedOnce = true
        }
      }
    },

    /** 滚动到底翻页（v1 _calendar_scrolled：offset=nextOffset 追加） */
    async loadMore() {
      // 停摆之后不再自动请求（v1：`assertFalse(self.runner.pending)`），
      // 但显式重试走 retryLoadMore()，它先把 stalled 清掉。
      if (this.loading || !this.hasMore || this.stalled) return
      const seq = ++this.reqSeq
      const cursor = this.nextOffset // 失败重试要用**同一个游标**（v1 failed_page_retries_same_cursor）
      this.loading = true
      try {
        const page: CalendarPayload = await api.calendar(this.date, cursor, this.gender)
        if (seq !== this.reqSeq) return
        const list = (page.items ?? []).map(normalizeItem).filter(Boolean) as CalendarRow[]
        const seen = new Set(this.rows.map((r) => r.id))
        const fresh = list.filter((r) => !seen.has(r.id))
        // ⚠ v1 `duplicate_pages_stop_automatic_requests_and_can_retry`：
        // 服务端连着给出"一页里没有一条新剧"时，必须**停止自动请求**——
        // 否则滚动条高度不变，用户每滚一下又发一次同样的请求，
        // 界面看着"加载中"其实永远在原地转（v1 的原话是「连续多页没有新剧」）。
        // 去重本身已经保证列表不会重复膨胀，所以这里判的是"这一页有没有推进"。
        this.dupStreak = fresh.length ? 0 : this.dupStreak + 1
        // 游标**无论有没有新内容都推进**（v1 的断言 requests[-1][1] == 4 说的就是这件事：
        // 停在同一页不算重试，往下走才算）。列表只在真拿到新条目时才增长。
        this.nextOffset = Number(page.nextOffset ?? this.nextOffset + (this.pageSize || list.length || 20))
        this.hasMore = !!page.hasMore
        if (this.dupStreak >= 3) {
          this.stalled = true
          this.hasMore = false // 停止自动；retryLoadMore 会重新打开
        } else {
          this.rows = this.rows.concat(fresh)
        }
        this.needsFill = !!this.hasMore
      } catch (e: any) {
        if (seq !== this.reqSeq) return
        // 失败**不清空已加载**，游标也不动：重试拿同一页（v1 同语义）
        this.error = e?.message ?? String(e)
      } finally {
        if (seq === this.reqSeq) this.loading = false
      }
    },

    /** v1 `_retry_new()`：状态行「点此重试」的后端。
     *  两种停摆都用它：失败后重试同一游标；重复页后跳过这一页继续推进。 */
    async retryLoadMore() {
      // 解除停摆即可：游标在 loadMore 里已经推进过，
      // 所以这一次请求拿到的就是**下一页**（v1 `_retry_new` 的语义）。
      this.stalled = false
      this.dupStreak = 0
      this.hasMore = true
      await this.loadMore()
    },

    async selectDate(v: string) {
      if (!v || v === this.date) return
      this.loadedOnce = false // 换日期是新查询，不能被上一次在飞的请求挡住（同上）
      await this.load(v)
    },

    /** 男频/女频切换（v1 chip 语义）：改频道后整表重载，其余查询态（日期/翻页）保持。
     *
     * ⚠ 第五十四轮改了这里：原来第一行是 `if (this.loading) return`，
     * 也就是**正在加载时用户的第二次点击被直接丢掉**。
     * v1 的做法（`test_rapid_gender_change_discards_stale_result`）不是丢弃点击，
     * 而是**照样发新请求、把先回来的旧响应丢弃**——
     * 两者的差别在快速连点时看得见：旧实现会停在"用户最后点的那一格之前"的
     * 数据上（且不会自己纠正），新实现保证最终显示的是最后一次请求的结果。
     */
    async setGender(g: number) {
      if (this.gender === g && this.loadedOnce) return
      this.gender = g
      this.hasMore = false
      this.nextOffset = 0
      this.loadedOnce = false // 绕开 load() 的"飞行中不重复发首屏"短路
      await this.load(this.date || todayYmd())
    },

    /** 前后翻页：优先在 dates 轴上走一格，出轴则按自然日 ±1（核心接受任意 YYYYMMDD） */
    async shiftDay(n: number) {
      const base = this.date || todayYmd()
      const i = this.dates.indexOf(base)
      const target = i >= 0 && this.dates[i + n] ? this.dates[i + n] : addDaysYmd(base, n)
      if (target === this.date) return
      this.loadedOnce = false
      await this.load(target)
    },
    prevDay() {
      return this.shiftDay(-1)
    },
    nextDay() {
      return this.shiftDay(1)
    },
    goToday() {
      return this.load(todayYmd())
    },

    async toggleFollow(row: CalendarRow) {
      try {
        const res = await api.toggleFollow(row.drama)
        this.followed = { ...this.followed, [row.id]: !!res.followed }
        // v1 互斥：追剧即取消预约（Go ToggleFollow 对 reserved 行返回 followed=false，
        // 本地标志随之对齐；绑定返回值即真值）
        if (res.followed) this.reserved = { ...this.reserved, [row.id]: false }
      } catch (e: any) {
        this.actionError = tf('watch.errFollow', { m: e?.message ?? e })
      }
    },

    async toggleReserve(row: CalendarRow) {
      try {
        if (this.isReserved(row)) {
          await api.cancelReservation(row.id)
          this.reserved = { ...this.reserved, [row.id]: false }
        } else {
          const res = await api.reserve(row.drama)
          const on = res.reserved !== false
          this.reserved = { ...this.reserved, [row.id]: on }
          // v1 互斥：预约即取消追剧（Go Reserve 新行 followed:false）
          if (on) this.followed = { ...this.followed, [row.id]: false }
        }
      } catch (e: any) {
        this.actionError = tf('watch.errReserve', { m: e?.message ?? e })
      }
    }
  }
})
