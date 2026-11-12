/* 播放状态容器（V2-3c）：PlayerView 与新 Go 播放契约之间的唯一状态层。
   - 真实模式：PlayerOpen(dramaJSON, chapterJSON, index) 后由 wails 事件驱动——
     player:tick（500ms，payload=Snapshot 对象）/ player:ended / player:error / danmaku:stats；
     payload 兼容对象与 JSON 字符串两种形态（wails 给对象，防御旧序列化）。
   - VITE_MOCK：轮询 api.playerState()（api.ts 内置模拟时钟，Snapshot+eof 扩展位）。
   - 纯浏览器兜底（无桥且非 mock）：demoMode —— 本地演示时钟 + 80 条演示弹幕，
     状态栏标「示例数据」。
      时间基：Snapshot 毫秒（posMs/durationMs）→ store 秒制（界面 fmtClock 不变）。
   localStorage：音量键 zgj.player.volume（v1 set_volume 语义，0-100）；
   弹幕调试键 zgj.danmaku.override / zgj.danmaku.demo（override 在新契约下仅作演示开关遗留位）。
   V2-F 播放器体验包追加：
   - 连播开关 zgj.player.autoplay（默认开）：markEnded → 自动 playNext（v1 连播语义）；
   - 画质 zgj.player.quality（auto/1080/720/540，v1 QUALITY_INDEX_SLUGS 档位序）：
     PlayerOpen 尚无 quality 形参（不改 app.go），切换=重开当前集续播占位 + hint 提示
     「演示模式或未载入剧目：…」（player.qualityDeferred），不假成功；
   - 进度节流保存（v1 main.py:2495 同款）：播放中 position 变化≥5s 或距上次≥5s tick
     即 saveWatch(false)，崩溃不丢进度；shutdown 落盘保留；
   - M 静音=音量 0 ↔ 恢复（桥无 mpv mute 属性，用 setVolume 近似，见返报差异）。 */
import { defineStore } from 'pinia'
import { api, MOCK_MODE } from '@/services/api'
import { t, tf } from '@/services/i18n'
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
import { lookupNextSeason, type NextSeasonHit } from '@/services/nextSeason'
import { mockDanmakuAll } from '@/services/fixtures'

export const VOLUME_KEY = 'zgj.player.volume'
export const DM_OVERRIDE_KEY = 'zgj.danmaku.override'
export const DM_DEMO_KEY = 'zgj.danmaku.demo'
/** V2-F：连播开关（默认开）与画质偏好（v1 QUALITY_INDEX_SLUGS：auto/1080p/720p/540p → 档位序索引） */
export const AUTOPLAY_KEY = 'zgj.player.autoplay'
export const QUALITY_KEY = 'zgj.player.quality'
/** 弹幕不透明度百分比 20–100（v1 app-settings 的 danmakuOpacity：同量纲、同默认 100） */
export const DM_OPACITY_KEY = 'zgj.player.danmakuOpacity'
export const QUALITY_CHOICES = ['auto', '1080', '720', '540'] as const
const qualityLabel = (q: string): string =>
  q === 'auto' ? t('player.qualityAuto') : (QUALITY_MAP[q] ?? q)

/* R9c-2：数字档位是 ASCII 标签，只有「自动」是需要翻译的词，
   所以不再维护整表中文的 QUALITY_LABELS（已随本次收敛删除，避免留下没人用的中文常量）。 */
const QUALITY_MAP: Record<string, string> = { '1080': '1080P', '720': '720P', '540': '540P' }
/** v1 main.py:2495 节流参数：5s 时间窗 / 5s 位移阈值 */
export const WATCH_SAVE_INTERVAL_MS = 5000
export const WATCH_SAVE_POS_DELTA = 5
/** v1 SPEED_CHOICES（player.py:61）：0.5–3.0 单点档位 */
export const SPEED_CHOICES = [0.5, 0.75, 1, 1.25, 1.5, 2, 3]
const DEMO_DURATION = 180

function readQuality(): string {
  const q = localStorage.getItem(QUALITY_KEY)
  return q && (QUALITY_CHOICES as readonly string[]).includes(q) ? q : 'auto'
}

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

/** 读持久化的不透明度百分比：非法或未设 → 100（与 v1 tools.danmaku_opacity 同规则） */
function readOpacity(): number {
  try {
    const raw = localStorage.getItem(DM_OPACITY_KEY)
    if (raw == null) return 100
    const n = Number(raw)
    return Number.isFinite(n) ? Math.min(100, Math.max(20, Math.round(n))) : 100
  } catch {
    return 100
  }
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
  /** V2-F：播完自动连播开关（zgj.player.autoplay，默认开） */
  autoplay: boolean
  /** V2-F：画质档位 'auto'|'1080'|'720'|'540'（zgj.player.quality） */
  quality: string
  /** R4-2：弹幕不透明度百分比 20–100（zgj.player.danmakuOpacity，默认 100＝完全不透明） */
  danmakuOpacity: number
  /** 当前集 chapter 原样对象（画质切换重 resolve 用，免再拉详情） */
  currentChapter: Chapter | null
  /** V2-F：临时提示（画质待接线/全屏不可用等），8s 自动消失 */
  hint: string
  /** V2-F：静音前音量（M 键恢复用；无桥 mpv mute 属性，音量 0 近似） */
  preMuteVolume: number
  /** V2-F：节流 saveWatch 记账（上次落盘时间/位置） */
  lastWatchAt: number
  lastWatchPos: number
  /** V2-F：markEnded 幂等记号（已处理播完的集号；换集/seek 回片内重置） */
  eofHandledAt: number
  /** 本季真结局查到的下一季（v1 _check_next_season_after_end；查无=null） */
  nextSeason: NextSeasonHit | null
  hintTimer?: number
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
    danmakuOpacity: readOpacity(),
    demoMode: false,
    danmakuCount: null,
    danmakuNote: '',
    status: '',
    nextSeason: null,
    error: '',
    loading: false,
    drama: null,
    episodeIndex: 0,
    autoplay: localStorage.getItem(AUTOPLAY_KEY) !== '0', // 默认开（仅显式 '0' 关）
    quality: readQuality(),
    currentChapter: null as Chapter | null,
    hint: '',
    preMuteVolume: 0,
    lastWatchAt: 0,
    lastWatchPos: 0,
    eofHandledAt: 0
  }),

  getters: {
    progressPct: (s) => (s.duration > 0 ? Math.min(100, (s.position / s.duration) * 100) : 0),
    posLabel: (s) => fmtClock(s.position),
    durLabel: (s) => fmtClock(s.duration),
    /** 已知总集数且当前集非最后一集 → 播完出「下一集」按钮 */
    hasNextEpisode: (s) => !!s.drama?.episodes && s.drama.episodes > 0 && s.episodeIndex < s.drama.episodes,
    /** V2-F：⏮ 可用性（第一集禁用） */
    hasPrevEpisode: (s) => !!s.drama && s.episodeIndex > 1
  },

  actions: {
    /** 入口：路由 query（d/ep/pos/c，dramaRef.playerRoute 约定；兼容 ?drama=<id> 简写）→ PlayerOpen */
    async bootFromQuery(q: Record<string, unknown>) {
      this.loading = true
      this.error = ''
      const forcedDemo = localStorage.getItem(DM_DEMO_KEY) === '1'
      if (forcedDemo || (!MOCK_MODE && !bridgeAvailable())) {
        await this.startDemo(String(q.title ?? t('player.demoTitle')))
        this.loading = false
        return
      }
      // V2-F 修正：playerRoute 约定参数名是 `d`（原只读 drama/id → d= 链接剧目 id 恒空，
      // PlayerOpen 必抛 bad_video，浏览器侧被下面的宽口 catch 吞成演示模式）
      const drama = dramaFromQuery(String(q.d ?? q.drama ?? q.id ?? ''), q as Record<string | number | symbol, unknown>)
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
        // 桥/解析不可用 → 演示回落（状态栏「示例数据」）。
        // V2-F 修正：mock 面 api 从不走桥，`!bridgeAvailable()` 恒真会把 bad_video 这类
        // 真实播放错误也吞成演示模式；回落只应发生在「非 mock 且无桥」或桥级错误码。
        if (e?.code === 'bridge_missing' || e?.code === 'not_ready' || (!MOCK_MODE && !bridgeAvailable())) {
          await this.startDemo(String(q.title ?? drama.title) || t('player.demoTitle'))
        } else {
          this.error = e?.message ? tf('player.playFailedMsg', { m: e.message }) : t('player.playFailed')
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
      this.currentChapter = chapter
      this.title = tf('player.titleWithEpisode', { title: drama.title || t('player.titleFallback'), n: ep })
      this.eof = false
      this.eofHandledAt = 0 // 集级播完幂等随会话重置（同集重开——如画质占位——也能再触发）
      this.nextSeason = null
      if (this.status) this.status = '' // 清掉上一集「已播完」类提示，连播后不留残影
      this.lastWatchAt = Date.now() // 节流基线随会话重置（v1 换集重计）
      this.lastWatchPos = startPosSec
      const snap = await api.playerOpen(drama, chapter, ep, this.quality)
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
        this.error = (e as ApiError)?.message ?? t('player.nextOpenFailed')
      } finally {
        this.loading = false
      }
    },

    /** V2-F：上一集（⏮ 按钮）；与 playNext 对称，首集不可调（getters 兜底） */
    async playPrev() {
      if (!this.drama || !this.hasPrevEpisode) return
      this.loading = true
      this.error = ''
      try {
        await this.openEpisode(this.drama, this.episodeIndex - 1, 0)
      } catch (e) {
        this.error = (e as ApiError)?.message ?? t('player.prevOpenFailed')
      } finally {
        this.loading = false
      }
    },

    /** V2-F：连播开关（localStorage zgj.player.autoplay，默认开） */
    setAutoplay(on: boolean) {
      this.autoplay = on
      localStorage.setItem(AUTOPLAY_KEY, on ? '1' : '0')
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
        const label = tf('player.titleWithEpisode', { title: s.episode.title, n: s.episode.index })
        if (label !== this.title) this.title = label
      }
      // V2-F：崩溃不丢进度——每次快照都过一遍 5s 节流判定（v1 main.py:2495 同款）
      this.maybePersistWatch()
    },

    /** 演示模式：本地时钟 + fixtures 弹幕，不触碰 Go */
    async startDemo(title: string) {
      this.demoMode = true
      this.title = title ? tf('player.demoWithSuffix', { title }) : t('player.badgeDemo')
      this.status = t('player.demoStatus')
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
        const idx = m?.episode?.index // payload={"episode":EpisodeRef}
        // V2-F：连播链路依赖集号正确——上一集的滞后 ended（idx 与当前不符）直接吞掉
        if (idx && idx !== this.episodeIndex) return
        if (idx) this.episodeIndex = idx
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
        // V2-F 修正：eof 位必须取 applySnapshot 前的旧值——applySnapshot 会同步 s.eof，
        // 原写法 `s.eof && !this.eof` 恒假，mock 面播完从未触发 markEnded（连播/播完 UI 的前置）。
        const wasEof = this.eof
        this.applySnapshot(s)
        // mock 面 eof → 播完（真实后端不发 eof，由 player:ended 驱动）。
        // V2-F：切集竞态防护——playerOpen 未落地时轮询拿到的是旧会话快照，
        // 旧会话的 eof=true 不得触发本集 markEnded（曾致连播双跳 ep3→ep4→ep5）。
        const stale = typeof s?.episode?.index === 'number' && s.episode.index !== this.episodeIndex
        if (s.eof && !wasEof && !stale) this.markEnded()
      } catch {
        /* 轮询失败静默，退出时兜底 */
      }
    },

    /** 播完定案：eof + 提示（v1「下一集」按钮语义，player.py:1185）；
     *  V2-F：连播开且非末集 → 自动播下一集（重复 ended 由 eof 位吞掉） */
    markEnded() {
      // V2-F：防重入不能用 this.eof——pollSnapshot 里 applySnapshot 会先把 s.eof
      // 同步进来，再进 markEnded 就被自己吞掉（连播失效的根因）；改为按集号幂等。
      if (this.eofHandledAt === this.episodeIndex) return
      this.eofHandledAt = this.episodeIndex
      this.eof = true
      this.paused = true
      this.running = true
      if (this.autoplay && !this.demoMode && this.hasNextEpisode) {
        this.status = t('player.endedAutoplay')
        void this.playNext()
        return
      }
      this.status = t('player.endedNext')
      // v1 _check_next_season_after_end：只有「本季真结局」（无下一集）才查下一季，
      // 不跨季自动播放——查到由 UI 给「去看下一季」入口；查无静默（v1 状态栏 6s 语义并入 hint）。
      if (!this.hasNextEpisode && this.drama && !this.demoMode) {
        void this.checkNextSeason()
      }
    },

    /** 查下一季（series:next lane；prompted/busy 幂等在 nextSeason.ts 模块内） */
    async checkNextSeason() {
      const drama = this.drama
      if (!drama) return
      const hit = await lookupNextSeason(drama)
      this.nextSeason = hit
      if (!hit) this.status = t('player.seriesEndNoNext')
    },

    /** V2-F：节流落盘——播放中位移≥5s 或距上次≥5s 即写 saveWatch（未完成态） */
    maybePersistWatch() {
      if (this.demoMode || !this.drama || this.episodeIndex <= 0) return
      if (!this.running || this.paused || this.eof) return
      if (this.duration <= 0 || this.position <= 0) return
      const moved = Math.abs(this.position - this.lastWatchPos)
      if (moved < WATCH_SAVE_POS_DELTA && Date.now() - this.lastWatchAt < WATCH_SAVE_INTERVAL_MS) return
      void this.persistWatch(false)
    },

    /** V2-F：写观看进度（shutdown 与节流共用）；失败静默，不打断播放 */
    async persistWatch(finished: boolean) {
      if (!this.drama || this.episodeIndex <= 0 || this.position <= 0) return
      const pos = Math.floor(this.position)
      const dur = Math.floor(this.duration)
      this.lastWatchAt = Date.now()
      this.lastWatchPos = pos
      try {
        const { useWatchStore } = await import('./watch')
        await useWatchStore().saveWatch(this.drama, this.episodeIndex, pos, dur, finished)
      } catch {
        /* 进度写入失败静默 */
      }
    },

    async togglePlay() {
      if (this.demoMode) {
        this.paused = !this.paused
        if (!this.paused) {
          this.eof = false
          this.eofHandledAt = 0
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
        this.error = (e as ApiError)?.message ?? t('player.actionFailed')
      }
    },

    /** 界面秒制入口（PlayerView 传秒），桥内毫秒 */
    async seek(sec: number) {
      const target = Math.max(0, Math.min(this.duration || sec, sec))
      if (this.demoMode) {
        this.position = target
        this.eof = false
        this.eofHandledAt = 0 // seek 回片内允许再次判定播完
        return
      }
      try {
        await api.seekVideo(target * 1000)
        this.position = target
        this.eof = false
        this.eofHandledAt = 0 // seek 回片内允许再次判定播完
      } catch (e) {
        this.error = (e as ApiError)?.message ?? t('player.seekFailed')
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
        this.error = (e as ApiError)?.message ?? t('player.speedFailed')
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

    /** V2-F：M 静音——桥无 mpv mute 属性，用音量 0 近似（差异见返报）；再按恢复静音前音量 */
    async toggleMute() {
      if (this.volume > 0) {
        this.preMuteVolume = this.volume
        await this.applyVolume(0)
      } else {
        await this.applyVolume(this.preMuteVolume > 0 ? this.preMuteVolume : 50)
      }
    },

    /** 画质档位切换：记住偏好（zgj.player.quality）后带新档位重 resolve
     *  当前集并原位续播（对齐 v1 _switch_route 的换档即重开）。 */
    async setQuality(q: string) {
      if (!(QUALITY_CHOICES as readonly string[]).includes(q) || q === this.quality) return
      this.quality = q
      localStorage.setItem(QUALITY_KEY, q)
      if (this.demoMode || !this.drama) {
        this.setHint(t('player.qualityDeferred'))
        return
      }
      const pos = this.position
      const chapter = this.currentChapter
      this.loading = true
      try {
        await this.openEpisode(this.drama, this.episodeIndex, pos, chapter)
        this.setHint(tf('player.qualitySwitched', { q: qualityLabel(q) }))
      } catch {
        /* 重开失败不叠加提示，错误位已有 */
      } finally {
        this.loading = false
      }
    },

    /** V2-F：临时提示位（8s 自动消失，v1 帮助浮层同款节拍） */
    setHint(text: string) {
      this.hint = text
      if (this.hintTimer) clearTimeout(this.hintTimer)
      this.hintTimer = setTimeout(() => {
        this.hint = ''
        this.hintTimer = undefined
      }, 8000) as unknown as number
    },

    async toggleDanmaku() {
      const on = !this.danmakuOn
      this.danmakuOn = on
      if (this.demoMode) return
      try {
        await api.setDanmaku(on)
        // 壳层的不透明度是进程内状态（v1 每次播放从 app-settings.json 读；
        // v2 存 localStorage），所以开启时回灌一次，避免「设置看着在、其实是默认值」。
        if (on) await api.setDanmakuOpacity(this.danmakuOpacity)
      } catch (e) {
        this.error = (e as ApiError)?.message ?? t('player.danmakuFailed')
      }
    },

    /** R4-2：设弹幕不透明度（百分比 20–100，越大越实；v1「透明 NN%」滑条同量纲）。
     *  立刻落盘 + 下发壳层；壳层会用已抓数据重渲染 ASS 并 sub-reload（v1 语义）。
     *  演示模式（无桥）与弹幕关闭时只记值不下发——开着弹幕再切自然会补上。 */
    async setDanmakuOpacity(percent: number) {
      const n = Math.min(100, Math.max(20, Math.round(Number(percent) || 100)))
      this.danmakuOpacity = n
      try {
        localStorage.setItem(DM_OPACITY_KEY, String(n))
      } catch {
        /* 存不进去不致命，本会话内仍然生效 */
      }
      if (this.demoMode || !this.danmakuOn) return
      try {
        await api.setDanmakuOpacity(n)
      } catch (e) {
        this.error = (e as ApiError)?.message ?? t('player.danmakuOpacityFailed')
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
      if (this.hintTimer) {
        clearTimeout(this.hintTimer)
        this.hintTimer = undefined
      }
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
      // V2-F：与 5s 节流共用 persistWatch；播放中已经节流落盘，这里只补最终态。
      if (this.drama && this.episodeIndex > 0 && this.position > 0) {
        const finished = this.eof || (this.duration > 0 && this.position >= this.duration - 1)
        await this.persistWatch(finished)
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
