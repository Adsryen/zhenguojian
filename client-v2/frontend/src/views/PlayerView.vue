<script setup lang="ts">
/* PlayerView（V2-3c）：路由 query（d/ep/pos/c，dramaRef.playerRoute 约定，兼容 ?drama=<id>）→ store.bootFromQuery。
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
   mock/无桥/回退开关下 attach 返回 embedded:false，本组件零特殊分支（reportBounds 直通短路）。 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { usePlayerStore, SPEED_CHOICES } from '@/stores/player'
import { dramaFromQuery } from '@/services/dramaRef'
import { embedApi } from '@/services/api'

const route = useRoute()
const router = useRouter()
const store = usePlayerStore()

const dragging = ref(false)
const dragPos = ref(0)

/* ── V2-4 内嵌几何上报 ── */
const stageEl = ref<HTMLElement | null>(null)
/* 覆盖层穿透带（V2-4b）：标题条/控制条与 stage 的实际重叠才需要让出命中；
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
  await store.bootFromQuery({ ...route.query, title: dramaTitleFromQuery() })
  void reportBounds()
})

onBeforeUnmount(() => {
  stageRO?.disconnect()
  stageRO = undefined
  barsRO?.disconnect()
  barsRO = undefined
  window.removeEventListener('resize', onStageResize)
  if (embedActive) void embedApi.detach()
  void store.shutdown()
})
</script>

<template>
  <div class="player">
    <header class="pl-top" ref="topEl">
      <button class="pl-btn ghost" type="button" @click="closePlayer">← 返回</button>
      <h1 class="pl-title">{{ store.title || '播放器' }}</h1>
      <div class="pl-badges">
        <span v-if="store.demoMode" class="pl-badge demo">示例数据</span>
        <span v-if="store.danmakuCount !== null" class="pl-badge">弹幕 {{ store.danmakuCount }} 条</span>
        <span v-if="store.danmakuNote" class="pl-badge">{{ store.danmakuNote }}</span>
        <span v-if="store.eof" class="pl-badge warn">已播完</span>
      </div>
    </header>

    <div id="zgj-video-stage" ref="stageEl" class="pl-stage" @click="store.togglePlay()">
      <div v-if="store.loading" class="pl-hint">正在打开播放会话…</div>
      <div v-else-if="store.error" class="pl-hint err">{{ store.error }}</div>
      <div v-else class="pl-stage-inner">
        <div class="pl-disc" :class="{ spin: store.running && !store.paused && !store.eof }">
          <span class="pl-disc-core" />
        </div>
        <div v-if="store.eof" class="pl-ended">
          <span class="pl-ended-txt">本集已播完</span>
          <button v-if="store.hasNextEpisode" class="pl-btn next" type="button" @click.stop="store.playNext()">
            下一集
          </button>
          <button v-else class="pl-btn ghost" type="button" @click.stop="store.togglePlay()">重新播放</button>
        </div>
        <div v-else-if="store.paused && !store.loading" class="pl-bigplay">▶</div>
      </div>
    </div>

    <footer class="pl-bar" ref="barEl">
      <button class="pl-btn" type="button" :title="store.paused ? '播放' : '暂停'" @click="store.togglePlay()">
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
        <span class="pl-label">倍速</span>
        <select class="pl-select" :value="store.speed" @change="store.setSpeed(Number(($event.target as HTMLSelectElement).value))">
          <option v-for="sp in SPEED_CHOICES" :key="sp" :value="sp">{{ sp }}×</option>
        </select>
      </label>
      <label class="pl-group">
        <span class="pl-label">音量</span>
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
      <button
        class="pl-btn ghost dm"
        :class="{ off: !store.danmakuOn }"
        type="button"
        title="弹幕开关"
        @click="store.toggleDanmaku()"
      >
        弹幕{{ store.danmakuOn ? '开' : '关' }}
      </button>
      <button class="pl-btn ghost close" type="button" @click="closePlayer">关闭</button>
    </footer>

    <div v-if="store.status" class="pl-status" :class="{ warn: store.demoMode }">{{ store.status }}</div>
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
.pl-btn.dm.off {
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
</style>
