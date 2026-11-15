/* mock 播放内核面（V2-3c 契约）：500ms 模拟 tick（与真实事件源同频），
   内部状态即 player.Snapshot 形状（posMs/durationMs 毫秒制）；
   stores/player.ts 轮询 playerState() 消费。eof 为 mock 扩展位：
   真实后端播完靠 player:ended 事件，mock 面无事件总线，由快照带 eof 兜底。 */
import type { Chapter, Drama, NativeApi, PlayerSnapshot } from '../types'
import { ApiError, clampOpacityPercent } from '../contract'
import { t } from '../i18n'
import { delay } from './util'

const MOCK_DM_COUNT = 80 // mockDanmakuAll 铺满 180s 的条目数
const mockSnap: PlayerSnapshot = {
  playing: false,
  posMs: 0,
  durationMs: 0,
  speed: 1,
  volume: 100,
  danmaku: { enabled: true, count: 0, note: '' },
  episode: { dramaID: '', title: '', index: 0 },
  // 与 Go `Snapshot.Local` 同形；线上会话每次 open 都会置 false（见 playerOpen）
  local: false
}
const mockPlayer: { opened: boolean; eof: boolean; timer?: ReturnType<typeof setInterval>; lastTs: number } = {
  opened: false,
  eof: false,
  lastTs: 0
}

/** 当前快照（含 mock 扩展 eof 位）；轮询面每次给浅拷贝，防外部改内部状态 */
function mockSnapshotOut(): PlayerSnapshot {
  return {
    ...mockSnap,
    danmaku: { ...mockSnap.danmaku },
    episode: { ...mockSnap.episode },
    eof: mockPlayer.eof
  }
}

function mockSimStart() {
  if (mockPlayer.timer) clearInterval(mockPlayer.timer)
  mockPlayer.lastTs = Date.now()
  mockPlayer.timer = setInterval(() => {
    if (!mockPlayer.opened || !mockSnap.playing || mockPlayer.eof) {
      mockPlayer.lastTs = Date.now()
      return
    }
    const now = Date.now()
    mockSnap.posMs = Math.min(
      mockSnap.durationMs,
      mockSnap.posMs + (now - mockPlayer.lastTs) * (mockSnap.speed ?? 1)
    )
    mockPlayer.lastTs = now
    if (mockSnap.posMs >= mockSnap.durationMs && mockSnap.durationMs > 0) {
      mockPlayer.eof = true
      mockSnap.playing = false
    }
  }, 500)
}

export const mockPlayerApi = {
  playerOpen: async (drama: Drama, chapter: Chapter, index: number, quality?: string) => {
    void quality // mock 面画质无真实语义
    await delay(150)
    if (index < 1) throw new ApiError(t('api.errBadEpisode'), 'bad_video')
    if (!drama || !drama.id) throw new ApiError(t('api.errBadVideoParams'), 'bad_video')
    const durSec = Number(chapter?.duration ?? 0) || 0
    mockSnap.playing = true
    mockSnap.posMs = 0
    mockSnap.durationMs = durSec > 0 ? durSec * 1000 : 180000
    mockSnap.episode = { dramaID: drama.id, title: drama.title ?? '', index }
    mockSnap.local = false
    // 弹幕语义对齐 Go Service：开弹幕即有已抓计数（mock 铺满 80 条），关时保留计数只撤轨
    mockSnap.danmaku = {
      enabled: mockSnap.danmaku.enabled,
      count: mockSnap.danmaku.enabled ? MOCK_DM_COUNT : 0,
      note: t('api.mockDanmakuNote')
    }
    mockPlayer.opened = true
    mockPlayer.eof = false
    mockSimStart()
    return mockSnapshotOut()
  },
  playerPlay: async () => {
    if (!mockPlayer.opened) throw new ApiError('player: 尚未打开播放会话', 'not_open')
    if (mockPlayer.eof) return // 播完 keep-open：与真实服务一致，需 seek 回片内或换集
    mockSnap.playing = true
    mockPlayer.lastTs = Date.now()
  },
  pauseVideo: async () => {
    if (!mockPlayer.opened) throw new ApiError('player: 尚未打开播放会话', 'not_open')
    mockSnap.playing = false
  },
  seekVideo: async (positionMs: number) => {
    if (!mockPlayer.opened) throw new ApiError('player: 尚未打开播放会话', 'not_open')
    mockSnap.posMs = Math.max(0, Math.min(mockSnap.durationMs, positionMs))
    mockPlayer.lastTs = Date.now()
    if (mockSnap.posMs < mockSnap.durationMs) mockPlayer.eof = false
  },
  setSpeed: async (v: number) => { mockSnap.speed = Math.max(0.5, Math.min(3, v)) },
  setVolume: async (v: number) => { mockSnap.volume = Math.max(0, Math.min(130, v)) },
  closeVideo: async () => {
    if (mockPlayer.timer) clearInterval(mockPlayer.timer)
    mockPlayer.timer = undefined
    mockPlayer.opened = false
    mockPlayer.eof = false
    // 对齐 Go Service.State()：Close 后返回零值骨架（speed/volume 保留播放意图）
    mockSnap.playing = false
    mockSnap.posMs = 0
    mockSnap.durationMs = 0
    mockSnap.episode = { dramaID: '', title: '', index: 0 }
  },
  playerState: async () => {
    if (!mockPlayer.opened && mockSnap.episode.index === 0) {
      // Close 之后：零值骨架（真实 Go 侧 Speed/Volume 保留意图）
      return { ...mockSnapshotOut(), playing: false, posMs: 0, durationMs: 0 }
    }
    return mockSnapshotOut()
  },
  setDanmaku: async (on: boolean) => {
    mockSnap.danmaku = { ...mockSnap.danmaku, enabled: on, count: on ? MOCK_DM_COUNT : mockSnap.danmaku.count }
  },
  setDanmakuOpacity: async (percent: number) => {
    mockSnap.danmaku = { ...mockSnap.danmaku, opacity: clampOpacityPercent(percent) }
  },
  playerOpenLocal: async (path, resumeSeconds) => {
    // mock 面没有真实文件，也不假装 stat 过：
    // 「文件在不在」由 `__zgjLocalMissing` 这个测试缝决定（默认视为存在），
    // 报出来的 code 与 Go 完全一致，前端那条错误分支因此在 mock 下也是**同一段代码**。
    const missing = (window as any)?.__zgjLocalMissing as string[] | undefined
    const name = String(path ?? '').split(/[\\/]/).pop() ?? ''
    if (!path) throw new ApiError('empty path', 'bad_video')
    if (Array.isArray(missing) && missing.includes(String(path))) {
      throw new ApiError(`本地文件已不存在：${name}`, 'local_missing')
    }
    // 越界判定在 Go（checkLocalPlayable + 单测六种形状）。mock 面**没有真实目录**，
    // 不去假装实现了那条规则：由 `__zgjLocalOutside` 这个测试缝决定报不报 outside_root，
    // 本脚本 ⑦ 因此只验证"前端拿到这个 code 时说的是什么话"，不验证守卫本身。
    const outside = (window as any)?.__zgjLocalOutside as string[] | undefined
    if (Array.isArray(outside) && outside.includes(String(path))) {
      throw new ApiError('只允许播放下载目录下的成品文件', 'outside_root')
    }
    mockSnap.playing = true
    mockSnap.posMs = Math.max(0, Math.floor(Number(resumeSeconds || 0) * 1000))
    mockSnap.durationMs = mockSnap.durationMs || 1800000
    // 本地会话不属于任何一集：dramaID 空、index 0（与 Go 侧 OpenLocal 同形状）
    mockSnap.episode = { dramaID: '', title: name, index: 0 }
    // 会话是否"已打开"由这个字段说，不由集号猜（本地会话恒 index 0）
    mockSnap.local = true
    mockSnap.danmaku = { ...mockSnap.danmaku, count: 0, note: t('api.mockLocalNote') }
    mockPlayer.opened = true
    mockPlayer.eof = false
    mockSimStart()
    return mockSnapshotOut()
  }
} satisfies Pick<
  NativeApi,
  'playerOpen' | 'playerPlay' | 'pauseVideo' | 'seekVideo' | 'setSpeed' | 'setVolume' |
  'closeVideo' | 'playerState' | 'setDanmaku' | 'setDanmakuOpacity' | 'playerOpenLocal'
>
