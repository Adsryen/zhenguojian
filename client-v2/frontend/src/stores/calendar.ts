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
    nextOffset: 0,
    followed: {} as Record<string, boolean>, // 本地 watchstore 追剧态
    reserved: {} as Record<string, boolean>, // 本地 watchstore 预约态
    flagsLoaded: false,
    actionError: '',
    loadedOnce: false
  }),
  getters: {
    onlineRows: (s) => s.rows.filter((r) => r.online),
    pendingRows: (s) => s.rows.filter((r) => !r.online),
    /** v1 状态条文案（加载/错误/空日/没有更多） */
    statusText(s): string {
      if (s.loading && !s.rows.length) return t('calendar.statusFirstLoad')
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
      if (this.loading) return
      this.loading = true
      this.error = ''
      try {
        const page: CalendarPayload = await api.calendar(date, 0, this.gender)
        const list = (page.items ?? []).map(normalizeItem).filter(Boolean) as CalendarRow[]
        this.dates = (page.dates ?? []).filter((d) => /^\d{8}$/.test(String(d)))
        // v1：首载 date="" 时选 defaultDate ?? dates 末位 ?? 今天
        this.date = date || page.defaultDate || (this.dates.length ? this.dates[this.dates.length - 1] : todayYmd())
        this.rows = list
        this.hasMore = !!page.hasMore
        this.nextOffset = Number(page.nextOffset ?? 0)
        if (!this.flagsLoaded) await this.loadFlags()
      } catch (e: any) {
        this.error = e?.message ?? String(e)
      } finally {
        this.loading = false
        this.loadedOnce = true
      }
    },

    /** 滚动到底翻页（v1 _calendar_scrolled：offset=nextOffset 追加） */
    async loadMore() {
      if (this.loading || !this.hasMore) return
      this.loading = true
      try {
        const page: CalendarPayload = await api.calendar(this.date, this.nextOffset, this.gender)
        const list = (page.items ?? []).map(normalizeItem).filter(Boolean) as CalendarRow[]
        const seen = new Set(this.rows.map((r) => r.id))
        this.rows = this.rows.concat(list.filter((r) => !seen.has(r.id)))
        this.hasMore = !!page.hasMore
        this.nextOffset = Number(page.nextOffset ?? this.nextOffset)
      } catch (e: any) {
        this.error = e?.message ?? String(e)
      } finally {
        this.loading = false
      }
    },

    async selectDate(v: string) {
      if (this.loading || !v || v === this.date) return
      await this.load(v)
    },

    /** 男频/女频切换（v1 chip 语义）：改频道后整表重载，其余查询态（日期/翻页）保持 */
    async setGender(g: number) {
      if (this.loading || this.gender === g) return
      this.gender = g
      this.hasMore = false
      this.nextOffset = 0
      await this.load(this.date || todayYmd())
    },

    /** 前后翻页：优先在 dates 轴上走一格，出轴则按自然日 ±1（核心接受任意 YYYYMMDD） */
    async shiftDay(n: number) {
      if (this.loading) return
      const base = this.date || todayYmd()
      const i = this.dates.indexOf(base)
      const target = i >= 0 && this.dates[i + n] ? this.dates[i + n] : addDaysYmd(base, n)
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
