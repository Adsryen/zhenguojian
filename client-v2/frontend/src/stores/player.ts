/* 播放状态容器（V2-3c）：PlayerView 与新 Go 播放契约之间的唯一状态层。
   - 真实模式：PlayerOpen(dramaJSON, chapterJSON, index) 后由 wails 事件驱动——
     player:tick（500ms，payload=Snapshot 对象）/ player:ended / player:error / danmaku:stats；
     payload 兼容对象与 JSON 字符串两种形态（wails 给对象，防御旧序列化）。
   - VITE_MOCK：轮询 api.playerState()（api.ts 内置模拟时钟，Snapshot+eof 扩展位）。
   - 纯浏览器兜底（无桥且非 mock）：demoMode —— 本地演示时钟 + 80 条演示弹幕，
     状态栏标「示例数据」。
   时间基：Snapshot 毫秒（posMs/durationMs）→ store 秒制（界面 fmtClock 不变）。
   localStorage：音量键 zgj.player.volume（v1 set_volume 语义，0-100）；
   弹幕调试键 zgj.danmaku.override / zgj.danmaku.demo（override 在新契约下仅作演示开关遗留位）。 */
import { defineStore } from 'pinia'
import { api, MOCK_MODE } from '@/services/api'
import {
  ApiError,
  type Chapter,
  type DanmakuStatsPayload,
  type Drama,
  type PlayerEndedPayload,
  type PlayerErrorPayload,
  type PlayerSnapshot
} from '@/services/types'
import { dramaFromQuery } from '@/services/dramaRef'
import { mockDanmakuAll } from '@/services/fixtures'

export const VOLUME_KEY = 'zgj.player.volume'
export const DM_OVERRIDE_KEY = 'zgj.danmaku.override'
export const DM_DEMO_KEY = 'zgj.danmaku.demo'
/** v1 SPEED_CHOICES（player.py:61）：0.5–3.0 单点档位 */
export const SPEED_CHOICES = [0.5, 0.75, 1, 1.25, 1.5, 2, 3]
const DEMO_DURATION = 180

function bridgeAvailable(): boolean {
  return !!window.go?.main?.App?.PlayerOpen
}

function clampVolume(v: unknown): number {
  const n = Math.round(Number(v))
  if (!Number.isFinite(n)) return 100
  return Math.max(0, Math.min(100, n))
}

/** wails EventsEmit 发结构体 → JS 收到对象；防御 JSON 字符串形态 */
function asObject<T>(payload: unknown): T | null {
  if (payload == null) return null
  if (typeof payload === 'string') {
    try {
      return JSON.parse(payload) as T
    } catch {
      return null
    }
  }
  if (typeof payload === 'object') return payload as T
  return null
}

type EventsBus = {
  EventsOn?: (n: string, cb: (data: unknown) => void) => void
  EventsOff?: (n: string) => void
}

const PLAYER_EVENTS = ['player:tick', 'player:ended', 'player:error', 'danmaku:stats'] as const

export interface PlayerStoreState {
  title: string
  running: boolean
  opened: boolean
  paused: boolean
  /** 秒（Snapshot 毫秒 ÷1000 后的界面时间基） */
  position: number
  duration: number
  speed: number
  volume: number
  eof: boolean
  danmakuOn: boolean
  demoMode: boolean
  /** 弹幕条数；null=未取/不可用（来源：snapshot.danmaku.count / danmaku:stats） */
  danmakuCount: number | null
  /** snapshot.danmaku.note：非官方源等提示语 */
  danmakuNote: string
  status: string
  error: string
  loading: boolean
  /** 当前剧与集号：ended→下一集、重开都靠它 */
  drama: Drama | null
  episodeIndex: number
  ticker?: number
  eventsBus?: EventsBus
}

export const usePlayerStore = defineStore('player', {
  state: (): PlayerStoreState => ({
    title: '',
    running: false,
    opened: false,
    paused: true,
    position: 0,
    duration: 0,
    speed: 1,
    volume: clampVolume(localStorage.getItem(VOLUME_KEY) ?? 100),
    eof: false,
    danmakuOn: true,
    demoMode: false,
    danmakuCount: null,
    danmakuNote: '',
    status: '',
    error: '',
    loading: false,
    drama: null,
    episodeIndex: 0
  }),

  getters: {
    progressPct: (s) => (s.duration > 0 ? Math.min(100, (s.position / s.duration) * 100) : 0),
    posLabel: (s) => fmtClock(s.position),
    durLabel: (s) => fmtClock(s.duration),
    /** 已知总集数且当前集非最后一集 → 播完出「下一集」按钮 */
    hasNextEpisode: (s) => !!s.drama?.episodes && s.drama.episodes > 0 && s.episodeIndex < s.drama.episodes
  },

  actions: {
    /** 入口：路由 query（d/ep/pos/c，dramaRef.playerRoute 约定；兼容 ?drama=<id> 简写）→ PlayerOpen */
    async bootFromQuery(q: Record<string, unknown>) {
      this.loading = true
      this.error = ''
      const forcedDemo = localStorage.getItem(DM_DEMO_KEY) === '1'
      if (forcedDemo || (!MOCK_MODE && !bridgeAvailable())) {
        await this.startDemo(String(q.title ?? '演示影片'))
        this.loading = false
        return
      }
      const drama = dramaFromQuery(String(q.drama ?? q.id ?? ''), q as Record<string | number | symbol, unknown>)
      const ep = Math.max(1, Number(q.ep ?? 1) || 1)
      const startPosSec = Math.max(0, Number(q.pos ?? 0) || 0)
      let chapter: Chapter | null = null
      if (typeof q.c === 'string' && q.c) {
        try {
          chapter = JSON.parse(q.c) as Chapter
        } catch {
          chapter = null
        }
      }
      try {
        await this.openEpisode(drama, ep, startPosSec, chapter)
      } catch (err) {
        const e = err as ApiError
        // 桥/解析不可用 → 演示回落（状态栏「示例数据」）
        if (e?.code === 'bridge_missing' || e?.code === 'not_ready' || !bridgeAvailable()) {
          await this.startDemo(String(q.title ?? drama.title) || '演示影片')
        } else {
          this.error = e?.message ? `播放失败：${e.message}` : '播放失败'
        }
      } finally {
        this.loading = false
      }
    },

    /** 核心：drama+chapter 原样对象交 PlayerOpen（Go 内部 resolve，前端不再预解析），index 1 起始 */
    async openEpisode(drama: Drama, ep: number, startPosSec = 0, chapterHint?: Chapter | null) {
      let chapter = chapterHint ?? null
      if (!chapter) {
        const detail = await api.detail(drama)
        // ?drama=<id> 简写链接只有 id：用详情回填标题/总集数（hasNextEpisode 依赖 episodes）
        if (detail.drama) {
          const d = detail.drama
          drama = {
            ...d,
            ...drama,
            title: drama.title || d.title || '',
            source: drama.source || d.source || '',
            episodes: drama.episodes || d.episodes || 0,
            cover: drama.cover || d.cover || ''
          }
        }
        chapter = detail.chapters?.[ep - 1] ?? { currentEpisode: ep }
      }
      this.drama = drama
      this.episodeIndex = ep
      this.title = `${drama.title || '影片'} · 第${ep}集`
      this.eof = false
      const snap = await api.playerOpen(drama, chapter, ep)
      this.opened = true
      this.running = true
      this.paused = false
      this.demoMode = false
      this.applySnapshot(snap)
      this.attach()
      // 音量恢复（v1 变更即存 → 启动即取）
      await this.applyVolume(this.volume)
      if (startPosSec > 0) await this.seek(startPosSec)
    },

    /** 播完换下一集：沿用 drama，集号+1（chapter 由 detail 重新取） */
    async playNext() {
      if (!this.drama || !this.hasNextEpisode) return
      this.loading = true
      this.error = ''
      try {
        await this.openEpisode(this.drama, this.episodeIndex + 1, 0)
      } catch (e) {
        this.error = (e as ApiError)?.message ?? '下一集打开失败'
      } finally {
        this.loading = false
      }
    },

    /** Snapshot（毫秒制）→ store（秒制）。eof 只在 mock 扩展位存在时同步；真实播完走 ended 事件 */
    applySnapshot(s: PlayerSnapshot) {
      if (!s || typeof s !== 'object') return
      const openedNow = !!s.episode?.index
      this.opened = openedNow
      this.running = openedNow
      this.paused = !s.playing
      this.position = Math.max(0, Number(s.posMs ?? 0) / 1000)
      if (Number(s.durationMs ?? 0) > 0) this.duration = Number(s.durationMs) / 1000
      if (typeof s.speed === 'number' && s.speed > 0) this.speed = s.speed
      if (typeof s.volume === 'number') this.volume = clampVolume(s.volume)
      if (s.danmaku) {
        this.danmakuOn = !!s.danmaku.enabled
        if (typeof s.danmaku.count === 'number') this.danmakuCount = s.danmaku.count
        this.danmakuNote = s.danmaku.note ?? ''
      }
      if (typeof s.eof === 'boolean') this.eof = s.eof
      if (s.episode?.title && s.episode?.index) {
        const label = `${s.episode.title} · 第${s.episode.index}集`
        if (label !== this.title) this.title = label
      }
    },

    /** 演示模式：本地时钟 + fixtures 弹幕，不触碰 Go */
    async startDemo(title: string) {
      this.demoMode = true
      this.title = title ? `${title}（示例数据）` : '示例数据'
      this.status = '演示模式：PlayerOpen/播放桥不可用，当前为示例数据'
      this.opened = true
      this.running = true
      this.paused = false
      this.duration = DEMO_DURATION
      this.position = 0
      const demo = mockDanmakuAll('demo')
      this.danmakuCount = demo.items.length
      this.startDemoTicker()
    },

    startDemoTicker() {
      this.stopTicker()
      let last = Date.now()
      this.ticker = setInterval(() => {
        const now = Date.now()
        if (!this.paused && !this.eof) {
          this.position = Math.min(this.duration, this.position + ((now - last) / 1000) * this.speed)
          if (this.position >= this.duration) this.markEnded()
        }
        last = now
      }, 500) as unknown as number
    },

    /** 接线：真实事件订阅；mock/无事件总线 → 轮询 playerState() 兜底 */
    attach() {
      if (this.demoMode) return
      if (MOCK_MODE) {
        this.startPolling()
        return
      }
      const w = window as unknown as { wails?: EventsBus; runtime?: EventsBus }
      const bus = w.wails?.EventsOn ? w.wails : w.runtime
      if (!bus?.EventsOn) {
        // 极端兜底：事件 API 不可用则轮询 PlayerState()（任务要求保留）
        this.startPolling()
        return
      }
      this.detach()
      bus.EventsOn('player:tick', (payload: unknown) => {
        const s = asObject<PlayerSnapshot>(payload)
        if (s) this.applySnapshot(s)
      })
      bus.EventsOn('player:ended', (payload: unknown) => {
        const m = asObject<PlayerEndedPayload>(payload)
        if (m?.episode?.index) this.episodeIndex = m.episode.index // payload={"episode":EpisodeRef}
        this.markEnded()
      })
      bus.EventsOn('player:error', (payload: unknown) => {
        const m = asObject<PlayerErrorPayload>(payload)
        if (m?.message) this.error = m.message
      })
      bus.EventsOn('danmaku:stats', (payload: unknown) => {
        const m = asObject<DanmakuStatsPayload>(payload)
        if (!m) return
        if (typeof m.count === 'number') this.danmakuCount = m.count
        this.danmakuNote = m.note ?? this.danmakuNote
      })
      this.eventsBus = bus
    },

    startPolling() {
      this.stopTicker()
      void this.pollSnapshot()
      this.ticker = setInterval(() => void this.pollSnapshot(), 500) as unknown as number
    },

    async pollSnapshot() {
      try {
        const s = await api.playerState()
        this.applySnapshot(s)
        // mock 面 eof → 播完（真实后端不发 eof，由 player:ended 驱动）
        if (s.eof && !this.eof) this.markEnded()
      } catch {
        /* 轮询失败静默，退出时兜底 */
      }
    },

    /** 播完定案：eof + 提示（v1「下一集」按钮语义，player.py:1185） */
    markEnded() {
      this.eof = true
      this.paused = true
      this.running = true
      this.status = '本集已播完，可播放下一集'
    },

    async togglePlay() {
      if (this.demoMode) {
        this.paused = !this.paused
        if (!this.paused) {
          this.eof = false
          if (this.position >= this.duration) this.position = 0
        }
        return
      }
      try {
        if (this.paused) {
          if (this.eof) await this.seek(0) // 播完 keep-open：从头播走 Seek 路径
          await api.playerPlay()
        } else {
          await api.pauseVideo()
        }
        this.paused = !this.paused
        this.eof = false
      } catch (e) {
        this.error = (e as ApiError)?.message ?? '操作失败'
      }
    },

    /** 界面秒制入口（PlayerView 传秒），桥内毫秒 */
    async seek(sec: number) {
      const target = Math.max(0, Math.min(this.duration || sec, sec))
      if (this.demoMode) {
        this.position = target
        this.eof = false
        return
      }
      try {
        await api.seekVideo(target * 1000)
        this.position = target
        this.eof = false
      } catch (e) {
        this.error = (e as ApiError)?.message ?? '跳转失败'
      }
    },

    async setSpeed(v: number) {
      const snapped = v < 0.5 ? 0.5 : v > 3 ? 3 : v
      if (this.demoMode) {
        this.speed = snapped
        return
      }
      try {
        await api.setSpeed(snapped)
        this.speed = snapped
      } catch (e) {
        this.error = (e as ApiError)?.message ?? '倍速失败'
      }
    },

    async applyVolume(v: number) {
      const vol = clampVolume(v)
      this.volume = vol
      localStorage.setItem(VOLUME_KEY, String(vol)) // v1 变更即存（player.py:1899-1905 同源语义）
      if (this.demoMode || !this.opened) return
      try {
        await api.setVolume(vol)
      } catch {
        /* 未 Open 时报错可忽略 */
      }
    },

    async toggleDanmaku() {
      const on = !this.danmakuOn
      this.danmakuOn = on
      if (this.demoMode) return
      try {
        await api.setDanmaku(on)
      } catch (e) {
        this.error = (e as ApiError)?.message ?? '弹幕开关失败'
      }
    },

    stopTicker() {
      if (this.ticker) {
        clearInterval(this.ticker)
        this.ticker = undefined
      }
    },

    detach() {
      this.stopTicker()
      const bus = this.eventsBus
      if (bus?.EventsOff) {
        for (const name of PLAYER_EVENTS) bus.EventsOff(name)
      }
      this.eventsBus = undefined
    },

    /** 关闭=PlayerClosePlayer+返回（任务定案）；路由卸载也调用，幂等 */
    async shutdown() {
      this.detach()
      // SaveWatch 接线：退出播放页时写观看进度（真值 = watch store → Go watchstore；
      // mock/DEGRADED 由 watch store 降级写 localStorage）。失败静默，不打断关闭流程。
      if (this.drama && this.episodeIndex > 0 && this.position > 0) {
        const finished = this.eof || (this.duration > 0 && this.position >= this.duration - 1)
        try {
          const { useWatchStore } = await import('./watch')
          await useWatchStore().saveWatch(this.drama, this.episodeIndex, Math.floor(this.position), Math.floor(this.duration), finished)
        } catch {
          /* 进度写入失败静默 */
        }
      }
      if (this.demoMode || !this.opened) {
        this.opened = false
        this.running = false
        return
      }
      try {
        await api.closeVideo()
      } catch {
        /* 已经关了 */
      }
      this.opened = false
      this.running = false
    }
  }
})

function fmtClock(sec: number): string {
  const s = Math.max(0, Math.floor(sec))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const r = s % 60
  const mm = `${m}`.padStart(h > 0 ? 2 : 1, '0')
  const ss = `${r}`.padStart(2, '0')
  return h > 0 ? `${h}:${mm}:${ss}` : `${mm}:${ss}`
}
