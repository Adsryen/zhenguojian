<script setup lang="ts">
/* PlayerView（V2-3c + V2-F 播放器体验包）：路由 query（d/ep/pos/c，dramaRef.playerRoute 约定，兼容 ?drama=<id>）→ store.bootFromQuery。
   控制条 = 播放/暂停、进度条（拖拽 Seek）、倍速 0.5–3（v1 SPEED_CHOICES，player.py:61）、
   音量（0-100，持久化 zgj.player.volume，v1 player.py:1899-1905 语义）、弹幕开关（PlayerDanmaku）、
   播完「下一集」（player:ended → playNext，v1 player.py:1185 语义）、关闭=PlayerClosePlayer+返回。
   数据由 player:tick / player:ended / player:error / danmaku:stats 事件驱动（stores/player.ts）；
   旧 ReportViewport 已随契约移除——Go 侧不再收视口，ASS 缩放由服务内部处理。
   弹幕由 mpv ASS 渲染，本组件 DOM 不画弹幕（任务定案）。
   V2-4 画面内嵌：attach（spawn 前）→ PlayerSetBounds 上报 .pl-stage 物理像素矩形；
   加载/出错/播完/未开播上报零尺寸让位 DOM 覆盖层；离开 detach 隐藏。
   V2-4b 点击穿透带：随几何上报 PlayerHitBands(top,bottom)——覆盖层与视频
   矩形的实际重叠高度（物理像素），Go 侧 WM_NCHITTEST 命中带内让给 WebView2。
   mock/无桥/回退开关下 attach 返回 embedded:false，本组件零特殊分支（reportBounds 直通短路）。
   ── V2-F 追加（体验包）──
   ① 全屏：按钮 + F 键 → window.runtime.WindowFullscreen/WindowUnfullscreen
      （可选调用：本仓库未生成 wailsjs/runtime 垫片文件，frontend/wailsjs 不存在，
       直接走注入的全局 runtime；不可用时 hint 明示，不假成功）。
   ② 快捷键全集：Space 播/暂、←→ ±5s、↑↓ 音量±5、F 全屏、M 静音、D 弹幕、? 帮助浮层
      （8s 自动关 / Esc 关，v1 player.py:1326-1329 + 861 HotkeyHelpOverlay 语义；
       输入控件聚焦时忽略，对照 v1 main.py:1765-1769）。Esc 无浮层时退出全屏（v1 同款次序）。
   ③ 常驻 ⏮/⏭（首/末集禁用）+ 播完自动连播（store.markEnded，zgj.player.autoplay 默认开）。
   ④ 右侧可折叠选集面板（300px/折叠 36px）：只读复用 EpisodeGrid（未改该组件；其
      current watch 自动切分组，DOM 落位由本层 scrollIntoView '.ep-btn.current' 补齐），
      点击切集走 store.openEpisode(drama, ep, 0, chapter)。
   ⑤ 画质下拉（自动/1080/720/540）：PlayerOpen 无 quality 形参（禁改 app.go）→
      偏好持久化 + 「重开当前集」占位 + hint「画质切换待核心接线」（stores/player.ts setQuality）。
   ⑥ 进度 5s 节流保存：见 store.applySnapshot → maybePersistWatch（v1 main.py:2495 同款）。
   布局契约不变：.pl-top/.pl-stage/.pl-bar 仍为垂直兄弟链（pl-mid 内 stage 在 bar 之上），
   hitbands 断言（top.bottom<=stage.top、bar.top>=stage.bottom）继续成立。 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { usePlayerStore, SPEED_CHOICES, QUALITY_CHOICES } from '@/stores/player'
import { dramaFromQuery, detailRoute } from '@/services/dramaRef'
import { api, embedApi } from '@/services/api'
import type { Chapter } from '@/services/types'
import EpisodeGrid from '@/components/EpisodeGrid.vue'
import HotkeyHelp from '@/components/HotkeyHelp.vue'
import { t, tf } from '@/services/i18n'

/* ── V2-F 文案已收敛至 i18n.ts player.*（原组件内 L 表）；t() 随 locale 响应 ── */
const hotkeys = computed(() => [
  { keys: t('player.hotkey.space.keys'), desc: t('player.hotkey.space.desc') },
  { keys: t('player.hotkey.arrows.keys'), desc: t('player.hotkey.arrows.desc') },
  { keys: t('player.hotkey.volume.keys'), desc: t('player.hotkey.volume.desc') },
  { keys: t('player.hotkey.fullscreen.keys'), desc: t('player.hotkey.fullscreen.desc') },
  { keys: t('player.hotkey.mute.keys'), desc: t('player.hotkey.mute.desc') },
  { keys: t('player.hotkey.danmaku.keys'), desc: t('player.hotkey.danmaku.desc') },
  { keys: t('player.hotkey.help.keys'), desc: t('player.hotkey.help.desc') }
])

const QUALITY_OPTIONS = computed(() =>
  QUALITY_CHOICES.map((q) => ({
    value: q,
    label: q === 'auto' ? t('player.qualityAuto') : `${q}p`
  }))
)

const route = useRoute()
const router = useRouter()
const store = usePlayerStore()

const dragging = ref(false)
const dragPos = ref(0)

/* V2-F：全屏态 / 帮助浮层 / 选集面板 */
const isFullscreen = ref(false)
const helpOpen = ref(false)
const panelOpen = ref(true)
const chapters = ref<Chapter[]>([])
const chaptersLoading = ref(false)
const panelBodyEl = ref<HTMLElement | null>(null)

/* ── V2-4 内嵌几何上报 ── */
const stageEl = ref<HTMLElement | null>(null)
/* 覆盖层穿透带（V2-4b）：标题条/控制条与 .pl-stage 的实际重叠才需要让出命中；
   现行 flex 兄弟布局重叠恒 0（这些像素本就在 host 窗之外），布局若改为
   叠在画面上（absolute 浮层）自动生效，WndProc 按带返回 HTTRANSPARENT。 */
const topEl = ref<HTMLElement | null>(null)
const barEl = ref<HTMLElement | null>(null)
let embedActive = false
let stageRO: ResizeObserver | undefined
let barsRO: ResizeObserver | undefined

function videoShouldShow(): boolean {
  // 仅在实际出画面期间占据视频矩形；其余状态让位给 DOM 覆盖层（原生子窗盖在 DOM 之上）。
  return store.running && !store.loading && !store.error && !store.eof
}

async function reportHitBands(el: HTMLElement) {
  const s = el.getBoundingClientRect()
  const dpr = window.devicePixelRatio || 1
  const t = topEl.value?.getBoundingClientRect()
  const top = t && t.bottom > s.top ? Math.round((t.bottom - s.top) * dpr) : 0
  const b = barEl.value?.getBoundingClientRect()
  const bottom = b && b.top < s.bottom ? Math.round((s.bottom - b.top) * dpr) : 0
  await embedApi.playerHitBands(top, bottom)
}

async function reportBounds() {
  if (!embedActive) return
  const el = stageEl.value
  if (!el || !videoShouldShow()) {
    await embedApi.setBounds(0, 0, 0, 0)
    if (el) await reportHitBands(el) // 隐藏态也刷新带：host 已 SW_HIDE，值仅作状态同步
    return
  }
  const r = el.getBoundingClientRect()
  const dpr = window.devicePixelRatio || 1
  await embedApi.setBounds(
    Math.round(r.left * dpr),
    Math.round(r.top * dpr),
    Math.round(r.width * dpr),
    Math.round(r.height * dpr)
  )
  await reportHitBands(el)
}

watch(
  () => [store.running, store.loading, store.error, store.eof],
  () => void reportBounds()
)

const sliderValue = computed({
  get: () => (dragging.value ? dragPos.value : store.position),
  set: (v: number) => {
    dragPos.value = v
  }
})

function onDragStart() {
  dragging.value = true
}

async function onDragEnd() {
  if (!dragging.value) return
  dragging.value = false
  await store.seek(dragPos.value)
}

/* ── V2-F：全屏 ── */
type WailsWindowRuntime = {
  WindowFullscreen?: () => void
  WindowUnfullscreen?: () => void
}

function toggleFullscreen() {
  const rt = (window as unknown as { runtime?: WailsWindowRuntime }).runtime
  const name = isFullscreen.value ? 'WindowUnfullscreen' : 'WindowFullscreen'
  const fn = rt?.[name]
  if (typeof fn !== 'function') {
    // 垫片文件（wailsjs/runtime）本仓库未生成且真实浏览器无 window.runtime → 明示不可用
    store.setHint(t('player.fullscreenUnavailable'))
    return
  }
  fn.call(rt)
  isFullscreen.value = !isFullscreen.value
}

/* ── V2-F：快捷键分发（输入控件聚焦时忽略；对照 v1 main.py:1765-1769 / player.py:1326-1329） ── */
function isTypingTarget(t: EventTarget | null): boolean {
  const el = t as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable === true
}

function onHotkey(e: KeyboardEvent) {
  if (e.repeat || e.ctrlKey || e.metaKey || e.altKey) return
  const k = e.key
  if (k === 'Escape') {
    // v1 次序：帮助浮层在最上 → Esc 先关浮层；无浮层时全屏态 Esc 退出全屏（main.py _on_playback_esc）
    if (helpOpen.value) helpOpen.value = false
    else if (isFullscreen.value) toggleFullscreen()
    return
  }
  if (isTypingTarget(e.target)) return
  switch (k) {
    case ' ':
      e.preventDefault() // 防聚焦按钮被原生 Space 再触发一次
      void store.togglePlay()
      break
    case 'ArrowLeft':
      e.preventDefault()
      void store.seek(store.position - 5)
      break
    case 'ArrowRight':
      e.preventDefault()
      void store.seek(store.position + 5)
      break
    case 'ArrowUp':
      e.preventDefault()
      void store.applyVolume(store.volume + 5)
      break
    case 'ArrowDown':
      e.preventDefault()
      void store.applyVolume(store.volume - 5)
      break
    case 'f':
    case 'F':
      toggleFullscreen()
      break
    case 'm':
    case 'M':
      void store.toggleMute()
      break
    case 'd':
    case 'D':
      void store.toggleDanmaku()
      break
    case '?':
      helpOpen.value = !helpOpen.value
      break
  }
}

/* ── V2-F：选集面板数据（只读复用 EpisodeGrid；其接口满足需求，未自写列表） ── */
async function loadChapters() {
  const drama = store.drama
  if (!drama || store.demoMode) {
    chapters.value = []
    return
  }
  chaptersLoading.value = true
  try {
    const d = await api.detail(drama)
    chapters.value = d.chapters ?? []
  } catch {
    chapters.value = []
  } finally {
    chaptersLoading.value = false
    void nextTick(scrollToCurrent)
  }
}


/* 下一季入口（store.checkNextSeason 查到才出现） */
const nextSeasonLabel = computed(() => {
  const n = store.nextSeason
  if (!n) return ''
  return `去看 ${n.title || `第 ${n.season} ${n.unit}`} ▸`
})
function goNextSeason() {
  const n = store.nextSeason
  if (!n) return
  void router.push(detailRoute(n.drama))
}

function scrollToCurrent() {
  const el = panelBodyEl.value?.querySelector('.ep-btn.current')
  if (el) el.scrollIntoView({ block: 'nearest' })
}

watch(
  () => store.drama?.id,
  () => void loadChapters(),
  { immediate: true }
)
/* ── 预缓冲下一集（v1 main.py set_preload_hook/_on_player_preload_next 对齐：
   当前集会话建立后预热下一集片头，切集秒开；失败静默不打扰播放） ── */
const preloadedEp = ref(0)
async function preloadNext() {
  if (preloadedEp.value === store.episodeIndex) return
  preloadedEp.value = store.episodeIndex
  const drama = store.drama
  const next = chapters.value[store.episodeIndex] // episodeIndex 1 起 → [ep]=下一集
  if (!drama || !next || store.demoMode) return
  try {
    await api.preloadEpisode(drama, next, store.episodeIndex + 1, store.quality === 'auto' ? 0 : Number(store.quality))
  } catch {
    /* v1 同款：预缓冲失败不影响播放 */
  }
}
watch(
  () => [store.opened, store.episodeIndex] as const,
  ([opened]) => {
    if (opened && chapters.value.length) void preloadNext()
  }
)

watch(
  () => store.episodeIndex,
  () => void nextTick(scrollToCurrent)
)

function onEpisodeSelect(chapter: Chapter, _index: number, episode: number) {
  if (!store.drama || episode === store.episodeIndex) return
  void store.openEpisode(store.drama, episode, 0, chapter)
}

function onQualityChange(ev: Event) {
  void store.setQuality((ev.target as HTMLSelectElement).value)
}

function dramaTitleFromQuery(): string {
  try {
    return dramaFromQuery(String(route.query.drama ?? ''), route.query as Record<string | number | symbol, unknown>).title || ''
  } catch {
    return ''
  }
}

async function closePlayer() {
  await store.shutdown()
  if (window.history.length > 1) router.back()
  else router.push('/')
}

function onStageResize() {
  void reportBounds()
}

onMounted(async () => {
  // attach 必须先于 bootFromQuery→PlayerOpen：mpv spawn 时 --wid 才已就位。
  const attach = await embedApi.attach()
  embedActive = attach.embedded
  if (embedActive) {
    if (stageEl.value) {
      stageRO = new ResizeObserver(() => void reportBounds())
      stageRO.observe(stageEl.value)
    }
    // 浮层自身高度变化（换行/收起）也可能改变重叠，单独观测重报带宽。
    barsRO = new ResizeObserver(() => {
      if (stageEl.value) void reportHitBands(stageEl.value)
    })
    if (topEl.value) barsRO.observe(topEl.value)
    if (barEl.value) barsRO.observe(barEl.value)
    window.addEventListener('resize', onStageResize)
  }
  void reportBounds() // 隐藏态起手（running 尚未翻转，防陈旧矩形残留）
  window.addEventListener('keydown', onHotkey)
  await store.bootFromQuery({ ...route.query, title: dramaTitleFromQuery() })
})

/* 播放失败重试：重放整条 boot 链（解析超时/网络抖动后可直接再试一次）。 */
async function retryBoot() {
  await store.bootFromQuery({ ...route.query, title: dramaTitleFromQuery() })
}

onBeforeUnmount(() => {
  stageRO?.disconnect()
  stageRO = undefined
  barsRO?.disconnect()
  barsRO = undefined
  window.removeEventListener('resize', onStageResize)
  window.removeEventListener('keydown', onHotkey)
  if (embedActive) void embedApi.detach()
  void store.shutdown()
})
</script>

<template>
  <div class="player">
    <header class="pl-top" ref="topEl">
      <button class="pl-btn ghost" type="button" @click="closePlayer">← {{ t('player.back') }}</button>
      <h1 class="pl-title">{{ store.title || t('player.title') }}</h1>
      <div class="pl-badges">
        <span v-if="store.demoMode" class="pl-badge demo">{{ t('player.badgeDemo') }}</span>
        <span v-if="store.danmakuCount !== null" class="pl-badge">{{ tf('player.badgeDanmakuCount', { n: store.danmakuCount }) }}</span>
        <span v-if="store.danmakuNote" class="pl-badge">{{ store.danmakuNote }}</span>
        <span v-if="store.eof" class="pl-badge warn">{{ t('player.badgeEof') }}</span>
      </div>
    </header>

    <div class="pl-mid">
      <div class="pl-col">
        <div id="zgj-video-stage" ref="stageEl" class="pl-stage" @click="store.togglePlay()">
          <div v-if="store.loading" class="pl-hint">{{ t('player.openingSession') }}</div>
          <div v-else-if="store.error" class="pl-hint err">
            {{ store.error }}
            <button class="pl-btn ghost sm" type="button" data-testid="player-retry" @click.stop="retryBoot">
              {{ t('player.retry') }}
            </button>
          </div>
          <div v-else class="pl-stage-inner">
            <div class="pl-disc" :class="{ spin: store.running && !store.paused && !store.eof }">
              <span class="pl-disc-core" />
            </div>
            <div v-if="store.eof" class="pl-ended">
              <span class="pl-ended-txt">{{ t('player.endedEpisode') }}</span>
              <button v-if="store.hasNextEpisode" class="pl-btn next" type="button" @click.stop="store.playNext()">
                {{ t('player.nextEpisode') }}
              </button>
              <!-- 本季真结局：查到下一季给入口（v1 _apply_next_season 语义——不自动跨季播放，
                   点「去看下一季」进那部剧详情，由用户决定从哪集看起）；查无 v1 状态栏 6s 提示。 -->
              <button
                v-else-if="store.nextSeason"
                class="pl-btn next"
                type="button"
                data-testid="pl-next-season"
                @click.stop="goNextSeason"
              >
                {{ nextSeasonLabel }}
              </button>
              <button v-else class="pl-btn ghost" type="button" @click.stop="store.togglePlay()">{{ t('player.replay') }}</button>
            </div>
            <div v-else-if="store.paused && !store.loading" class="pl-bigplay">▶</div>
          </div>
        </div>

        <footer class="pl-bar" ref="barEl">
          <button
            class="pl-btn nav prev"
            type="button"
            :disabled="!store.hasPrevEpisode"
            :title="t('player.prevEpisode')"
            @click="store.playPrev()"
          >⏮</button>
          <button
            class="pl-btn nav next-ep"
            type="button"
            :disabled="!store.hasNextEpisode"
            :title="t('player.nextEpisode')"
            @click="store.playNext()"
          >⏭</button>
          <button class="pl-btn" type="button" :title="store.paused ? t('player.play') : t('player.pause')" @click="store.togglePlay()">
            {{ store.paused ? '▶' : '⏸' }}
          </button>
          <span class="pl-time">{{ store.posLabel }} / {{ store.durLabel }}</span>
          <input
            v-model.number="sliderValue"
            class="pl-slider grow"
            type="range"
            min="0"
            :max="Math.max(store.duration, 1)"
            step="0.5"
            @pointerdown="onDragStart"
            @change="onDragEnd"
          />
          <label class="pl-group">
            <span class="pl-label">{{ t('player.speed') }}</span>
            <select class="pl-select" :value="store.speed" @change="store.setSpeed(Number(($event.target as HTMLSelectElement).value))">
              <option v-for="sp in SPEED_CHOICES" :key="sp" :value="sp">{{ sp }}×</option>
            </select>
          </label>
          <label class="pl-group">
            <span class="pl-label">{{ t('player.volume') }}</span>
            <input
              class="pl-slider vol"
              type="range"
              min="0"
              max="100"
              step="1"
              :value="store.volume"
              @input="store.applyVolume(Number(($event.target as HTMLInputElement).value))"
            />
            <span class="pl-time">{{ store.volume }}</span>
          </label>
          <label class="pl-group">
            <span class="pl-label">{{ t('player.quality') }}</span>
            <select class="pl-select quality" :value="store.quality" @change="onQualityChange">
              <option v-for="q in QUALITY_OPTIONS" :key="q.value" :value="q.value">{{ q.label }}</option>
            </select>
          </label>
          <button
            class="pl-btn ghost ap"
            :class="{ off: !store.autoplay }"
            type="button"
            :title="t('player.autoplayHint')"
            @click="store.setAutoplay(!store.autoplay)"
          >{{ store.autoplay ? t('player.autoplayOn') : t('player.autoplayOff') }}</button>
          <button
            class="pl-btn ghost dm"
            :class="{ off: !store.danmakuOn }"
            type="button"
            :title="t('player.danmakuToggle')"
            @click="store.toggleDanmaku()"
          >
            {{ store.danmakuOn ? t('player.danmakuOn') : t('player.danmakuOff') }}
          </button>
          <button
            class="pl-btn ghost fs"
            type="button"
            :title="isFullscreen ? t('player.exitFullscreen') : t('player.fullscreen')"
            @click="toggleFullscreen"
          >{{ isFullscreen ? t('player.exitFullscreen') : t('player.fullscreen') }}</button>
          <button class="pl-btn ghost hk" type="button" :title="t('player.help')" @click="helpOpen = !helpOpen">⌨</button>
          <button class="pl-btn ghost close" type="button" @click="closePlayer">{{ t('player.close') }}</button>
        </footer>

        <div v-if="store.status" class="pl-status" :class="{ warn: store.demoMode }">{{ store.status }}</div>
      </div>

      <aside class="pl-panel" :class="{ collapsed: !panelOpen }">
        <template v-if="panelOpen">
          <div class="pl-panel-head">
            <span class="pl-panel-title">{{ t('player.episodes') }}<span v-if="store.episodeIndex" class="pl-panel-cur">{{ tf('player.panelEpisode', { n: store.episodeIndex }) }}</span></span>
            <button class="pl-btn ghost sm" type="button" :title="t('player.episodesCollapse')" @click="panelOpen = false">▸</button>
          </div>
          <div ref="panelBodyEl" class="pl-panel-body">
            <div v-if="chaptersLoading" class="pl-panel-note">{{ t('player.episodesLoading') }}</div>
            <div v-else-if="!chapters.length" class="pl-panel-note">{{ store.demoMode ? t('player.episodesEmptyDemo') : t('player.episodesEmpty') }}</div>
            <EpisodeGrid
              v-else
              :chapters="chapters"
              :current="store.episodeIndex"
              :drama-vip="store.drama?.vip"
              @select="onEpisodeSelect"
            />
          </div>
        </template>
        <button v-else class="pl-panel-tab" type="button" :title="t('player.episodesExpand')" @click="panelOpen = true">
          {{ t('player.episodes') }}
        </button>
      </aside>
    </div>

    <HotkeyHelp
      v-if="helpOpen"
      :title="t('player.helpTitle')"
      :rows="hotkeys"
      :auto-close-ms="8000"
      @close="helpOpen = false"
    />

    <div v-if="store.hint" class="pl-toast" role="status">{{ store.hint }}</div>
  </div>
</template>

<style scoped>
.player {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: var(--bg);
  color: var(--text);
}
.pl-top {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--border);
  background: var(--surface-2);
}
.pl-title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.pl-badges {
  margin-left: auto;
  display: flex;
  gap: 8px;
}
.pl-badge {
  font-size: 12px;
  padding: 2px 8px;
  border-radius: 999px;
  background: var(--surface-3);
  color: var(--muted, #9aa3b2);
}
.pl-badge.demo {
  background: var(--accent-soft);
  color: var(--accent);
}
.pl-badge.warn {
  color: var(--warning, #e6a23c);
}
/* V2-F：stage/bar 与选集面板并排的中间行（stage/bar 仍是 pl-col 内垂直兄弟，穿透带契约不变） */
.pl-mid {
  flex: 1;
  min-height: 0;
  display: flex;
}
.pl-col {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}
.pl-stage {
  flex: 1;
  min-height: 0;
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  background: radial-gradient(120% 120% at 50% 0%, #10141c 0%, #000 90%);
  cursor: pointer;
}
.pl-hint {
  color: var(--muted, #9aa3b2);
  font-size: 13px;
}
.pl-hint.err {
  color: var(--danger, #f56c6c);
}
.pl-stage-inner {
  position: relative;
}
.pl-disc {
  width: 120px;
  height: 120px;
  border-radius: 50%;
  border: 2px solid var(--border);
  display: flex;
  align-items: center;
  justify-content: center;
  opacity: 0.5;
}
.pl-disc.spin {
  animation: pl-rot 6s linear infinite;
  border-color: var(--accent-soft);
}
.pl-disc-core {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  background: var(--accent);
  box-shadow: 0 0 24px var(--accent-soft);
}
@keyframes pl-rot {
  to {
    transform: rotate(360deg);
  }
}
.pl-ended {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
}
.pl-ended-txt {
  font-size: 13px;
  color: var(--muted, #9aa3b2);
}
.pl-btn.next {
  border-color: var(--accent);
  color: var(--accent);
}
.pl-bigplay {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 42px;
  color: #fff;
  text-shadow: 0 2px 12px rgba(0, 0, 0, 0.6);
}
.pl-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  padding: 10px 16px;
  border-top: 1px solid var(--border);
  background: var(--surface-2);
}
.pl-btn {
  border: 1px solid var(--border);
  background: var(--surface-3);
  color: var(--text);
  border-radius: 8px;
  padding: 6px 12px;
  font-size: 13px;
  cursor: pointer;
}
.pl-btn.ghost {
  background: transparent;
}
.pl-btn.sm {
  padding: 2px 8px;
  font-size: 12px;
}
.pl-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.pl-btn.dm.off,
.pl-btn.ap.off {
  opacity: 0.55;
}
.pl-btn.close {
  color: var(--muted);
}
.pl-btn.close:hover {
  color: var(--danger);
}
.pl-time {
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  color: var(--muted, #9aa3b2);
  white-space: nowrap;
}
.pl-slider {
  accent-color: var(--accent);
  height: 4px;
}
.pl-slider.grow {
  flex: 1;
  min-width: 120px;
}
.pl-slider.vol {
  width: 90px;
}
.pl-group {
  display: flex;
  align-items: center;
  gap: 6px;
}
.pl-label {
  font-size: 12px;
  color: var(--muted, #9aa3b2);
}
.pl-select {
  background: var(--surface-3);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 4px 6px;
  font-size: 12px;
}
.pl-status {
  padding: 6px 16px;
  font-size: 12px;
  color: var(--muted, #9aa3b2);
  border-top: 1px solid var(--border);
  background: var(--surface);
}
.pl-status.warn {
  color: var(--accent);
}
/* V2-F：右侧选集面板（展开 300 / 折叠 36） */
.pl-panel {
  width: 300px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  border-left: 1px solid var(--border);
  background: var(--surface-2);
  overflow: hidden;
}
.pl-panel.collapsed {
  width: 36px;
}
.pl-panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border);
  font-size: 13px;
}
.pl-panel-title {
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.pl-panel-cur {
  margin-left: 6px;
  font-weight: 400;
  color: var(--muted, #9aa3b2);
  font-size: 12px;
}
.pl-panel-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 10px 12px;
}
.pl-panel-note {
  font-size: 12px;
  color: var(--muted, #9aa3b2);
  padding: 6px 0;
}
.pl-panel-tab {
  writing-mode: vertical-rl;
  height: 100%;
  border: none;
  background: transparent;
  color: var(--muted, #9aa3b2);
  font-size: 12px;
  letter-spacing: 2px;
  cursor: pointer;
  padding: 10px 0;
}
.pl-panel-tab:hover {
  color: var(--accent);
}
/* V2-F：临时提示 toast（画质待接线/全屏不可用等，8s 自动消失） */
.pl-toast {
  position: fixed;
  left: 50%;
  bottom: 84px;
  transform: translateX(-50%);
  z-index: 70;
  max-width: min(560px, calc(100vw - 48px));
  padding: 8px 14px;
  border: 1px solid var(--border);
  border-radius: var(--guo-radius-lg);
  background: var(--surface-3);
  color: var(--text);
  font-size: 12px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
}
</style>
