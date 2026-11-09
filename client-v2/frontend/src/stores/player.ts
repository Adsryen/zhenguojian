/* 播放状态容器（V2-3b）：PlayerView 与播放桥之间的唯一状态层。
   - 真实模式：Open 后由 wails 事件驱动（player:tick 500ms 快照 / player:state）；
   - VITE_MOCK：轮询 api.playerState()（api.ts 内置模拟时钟）；
   - 纯浏览器兜底（无桥且非 mock）：demoMode —— 本地演示时钟 + 80 条演示弹幕，
     状态栏标「示例数据」（父代理指令 m00080：Resolve/桥不可用 → 演示回落）。
   localStorage：音量持久化键 zgj.player.volume（v1 set_volume player.py:1899-1905 语义，0-100）；
   弹幕调试键 zgj.danmaku.override（强制 session）/ zgj.danmaku.demo（强制演示）。 */
import { defineStore } from 'pinia'
import { api, MOCK_MODE } from '@/services/api'
import { ApiError, type Chapter, type Drama, type ResolvePlan } from '@/services/types'
import { dramaFromQuery } from '@/services/dramaRef'
import { mockDanmakuAll } from '@/services/fixtures'

export const VOLUME_KEY = 'zgj.player.volume'
export const DM_OVERRIDE_KEY = 'zgj.danmaku.override'
export const DM_DEMO_KEY = 'zgj.danmaku.demo'
/** v1 SPEED_CHOICES（player.py:61）：0.5–3.0 单点档位 */
export const SPEED_CHOICES = [0.5, 0.75, 1, 1.25, 1.5, 2, 3]
const DEMO_DURATION = 180

function bridgeAvailable(): boolean {
  return !!window.go?.main?.App?.Open
}

function clampVolume(v: unknown): number {
  const n = Math.round(Number(v))
  if (!Number.isFinite(n)) return 100
  return Math.max(0, Math.min(100, n))
}

export interface PlayerStoreState {
  title: string
  running: boolean
  opened: boolean
  paused: boolean
  /** 秒（与 Go Service tick/state 同一时间基） */
  position: number
  duration: number
  speed: number
  volume: number
  eof: boolean
  danmakuOn: boolean
  demoMode: boolean
  /** 弹幕条数；null=未取/不可用 */
  danmakuCount: number | null
  status: string
  error: string
  loading: boolean
  ticker?: number
  viewportTimer?: number
  eventsBus?: {
    EventsOn?: (n: string, cb: (data: string) => void) => void
    EventsOff?: (n: string) => void
  }
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
    status: '',
    error: '',
    loading: false
  }),

  getters: {
    progressPct: (s) => (s.duration > 0 ? Math.min(100, (s.position / s.duration) * 100) : 0),
    posLabel: (s) => fmtClock(s.position),
    durLabel: (s) => fmtClock(s.duration)
  },

  actions: {
    /** 入口：路由 query（d/ep/pos/c，dramaRef.playerRoute 约定）→ Resolve → Open */
    async bootFromQuery(q: Record<string, unknown>) {
      this.loading = true
      this.error = ''
      const forcedDemo = localStorage.getItem(DM_DEMO_KEY) === '1'
      if (forcedDemo || (!MOCK_MODE && !bridgeAvailable())) {
        await this.startDemo(String(q.title ?? '演示影片'))
        this.loading = false
        return
      }
      try {
        const drama: Drama = dramaFromQuery('', q as Record<string | number | symbol, unknown>)
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
        if (!chapter) {
          const detail = await api.detail(drama)
          chapter = detail.chapters?.[ep - 1] ?? { currentEpisode: ep }
        }
        const plan: ResolvePlan = await api.resolve(drama, chapter, ep - 1, 0)
        const session = String((plan as Record<string, unknown>).playbackSession ?? plan.session ?? '')
        const override = localStorage.getItem(DM_OVERRIDE_KEY)
        const dmSession = override || session
        const video = {
          url: String(plan.url ?? ''),
          title: `${drama.title || '影片'} · 第${ep}集`,
          session: dmSession,
          local: false,
          durationSec: Number(chapter.duration ?? 0) || 0,
          headers: plan.headers ?? {},
          ep
        }
        if (!video.url) throw new ApiError('解析结果缺少播放地址', 'bad_video')
        this.title = video.title
        await api.openVideo(JSON.stringify(video))
        this.opened = true
        this.running = true
        this.demoMode = false
        // 音量恢复（v1 变更即存 → 启动即取）
        await this.applyVolume(this.volume)
        if (startPosSec > 0) await this.seek(startPosSec)
        this.attach()
        if (dmSession) void this.loadDanmakuCount(dmSession)
        else this.danmakuCount = 0
      } catch (err) {
        const e = err as ApiError
        // 桥/解析不可用 → 演示回落（状态栏「示例数据」）
        if (e?.code === 'bridge_missing' || e?.code === 'not_ready' || !bridgeAvailable()) {
          await this.startDemo(String(q.title ?? dramaFromQuery('', q as never).title) || '演示影片')
        } else {
          this.error = e?.message ? `播放失败：${e.message}` : '播放失败'
        }
      } finally {
        this.loading = false
      }
    },

    /** 演示模式：本地时钟 + fixtures 弹幕，不触碰 Go */
    async startDemo(title: string) {
      this.demoMode = true
      this.title = title ? `${title}（示例数据）` : '示例数据'
      this.status = '演示模式：Resolve/播放桥不可用，当前为示例数据'
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
          if (this.position >= this.duration) this.eof = true
        }
        last = now
      }, 500) as unknown as number
    },

    /** 真实事件/模拟轮询接线 */
    attach() {
      if (this.demoMode || MOCK_MODE) {
        this.stopTicker()
        this.ticker = setInterval(() => void this.pollMockState(), 500) as unknown as number
        void this.pollMockState()
        return
      }
      const w = window as unknown as {
        wails?: { EventsOn?: (n: string, cb: (data: string) => void) => void; EventsOff?: (n: string) => void }
        runtime?: { EventsOn?: (n: string, cb: (data: string) => void) => void; EventsOff?: (n: string) => void }
      }
      const bus = w.wails?.EventsOn ? w.wails : w.runtime
      if (!bus?.EventsOn) {
        // 极端兜底：事件 API 不可用则轮询 State()
        this.ticker = setInterval(() => void this.pollMockState(true), 500) as unknown as number
        return
      }
      this.detach()
      bus.EventsOn('player:tick', (payload: string) => {
        try {
          const m = JSON.parse(payload) as {
            position: number; duration: number; title: string; paused: boolean
            speed: number; volume: number; eof: boolean; danmakuCount: number
          }
          this.position = m.position ?? 0
          this.duration = m.duration ?? this.duration
          if (m.title) this.title = m.title
          this.paused = !!m.paused
          this.speed = m.speed ?? this.speed
          this.volume = clampVolume(m.volume)
          this.eof = !!m.eof
          if (typeof m.danmakuCount === 'number') this.danmakuCount = m.danmakuCount
          this.running = true
        } catch {
          /* 忽略坏帧 */
        }
      })
      bus.EventsOn('player:state', (payload: string) => {
        try {
          const m = JSON.parse(payload) as { state: string; why_died?: string }
          if (m.state === 'file-loaded') this.status = '已载入'
          if (m.state === 'disconnected') this.status = m.why_died ? `连接中断，正在重建：${m.why_died}` : '连接中断，正在重建'
          if (m.state === 'exit') {
            this.running = false
            this.status = m.why_died ? `播放器已退出：${m.why_died}` : '播放器已退出'
          }
        } catch {
          /* 忽略 */
        }
      })
      this.eventsBus = bus
    },

    async pollMockState(real = false) {
      try {
        const st = real ? await api.playerState() : await api.playerState()
        this.running = st.running
        this.opened = st.opened
        this.position = st.position ?? this.position
        this.duration = st.duration ?? this.duration
        this.paused = st.paused ?? this.paused
        this.speed = st.speed ?? this.speed
        this.volume = clampVolume(st.volume ?? this.volume)
        this.eof = st.eof ?? this.eof
        this.danmakuOn = st.danmakuOn ?? this.danmakuOn
        if (st.title) this.title = st.title
      } catch {
        /* 轮询失败静默，退出时兜底 */
      }
    },

    async loadDanmakuCount(session: string) {
      try {
        const res = await api.danmakuAll(session)
        this.danmakuCount = res.items?.length ?? 0
      } catch {
        // 弹幕全量不可用：回落演示条目并标注（父代理指令 m00080）
        const demo = mockDanmakuAll(session)
        this.danmakuCount = demo.items.length
        this.status = '弹幕通道不可用，计数为示例数据'
      }
    },

    async togglePlay() {
      if (this.demoMode) {
        this.paused = !this.paused
        if (!this.paused) this.eof = false
        return
      }
      try {
        if (this.paused) await api.resumeVideo()
        else await api.pauseVideo()
        this.paused = !this.paused
      } catch (e) {
        this.error = (e as ApiError)?.message ?? '操作失败'
      }
    },

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

    /** 视口上报（300ms 去抖，Go 侧重建 ASS PlayRes） */
    reportViewport(w: number, h: number) {
      if (this.demoMode || !this.opened) return
      clearTimeout(this.viewportTimer)
      this.viewportTimer = setTimeout(() => {
        api.reportViewport(Math.round(w), Math.round(h)).catch(() => undefined)
      }, 300) as unknown as number
    },

    stopTicker() {
      if (this.ticker) {
        clearInterval(this.ticker)
        this.ticker = undefined
      }
    },

    detach() {
      this.stopTicker()
      clearTimeout(this.viewportTimer)
      const bus = this.eventsBus
      if (bus?.EventsOff) {
        bus.EventsOff('player:tick')
        bus.EventsOff('player:state')
      }
      this.eventsBus = undefined
    },

    /** 关闭=Close+返回（任务定案）；路由卸载也调用，幂等 */
    async shutdown() {
      this.detach()
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
