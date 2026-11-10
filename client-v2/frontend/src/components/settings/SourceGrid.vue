<script setup lang="ts">
/* SourceGrid —— 站源卡片网格 + 指针式拖拽排序（不用 HTML5 DnD，Pointer Events 实现）。
   状态机：idle → pending（pointerdown 于卡片非开关热区，位移未过 6px 阈值，无视觉变化）
   → dragging（超过阈值：原卡隐藏、目标槽虚线半透明占位、ghost 跟手、其余卡片 FLIP 式
   transform 位移 120ms）→ 落位 commit（顺序变化则 emit reorder，父级持久化）
   → 或 cancel（Escape / pointercancel / 原位释放：ghost 动画飞回原槽后复位）。
   热区排除：pointerdown 落在 .sc-switch 上不进入拖拽，开关 click 正常。
   拖拽后抑制一次 click（防误触发开关）；期间 body 挂 user-select:none + grabbing 光标。
   顺序提交只含可见 slug 全序列，规则细节在 stores/app.ts orderedSources。 */
import { computed, onBeforeUnmount, reactive, ref } from 'vue'
import SourceCard from './SourceCard.vue'
import type { SourceListItem } from '@/services/types'

const props = defineProps<{
  sources: SourceListItem[]
  /** SettingsView 的 toggleMeta 取值器：(slug) => { busy, err } */
  meta: (slug: string) => { busy: boolean; err: string }
}>()

const emit = defineEmits<{
  (e: 'toggle', slug: string, next: boolean): void
  (e: 'reorder', slugs: string[]): void
}>()

const DRAG_THRESHOLD = 6 // px，超过即进入 dragging
const SETTLE_MS = 180 // ghost 飞回动画时长
const BODY_CLASS = 'zgj-src-dragging'

type SlotRect = { left: number; top: number; width: number; height: number }
type Phase = 'idle' | 'pending' | 'dragging' | 'returning'

const drag = reactive({
  phase: 'idle' as Phase,
  pointerId: -1,
  from: -1,
  to: -1,
  startX: 0,
  startY: 0,
  grabDX: 0,
  grabDY: 0,
  gx: 0,
  gy: 0,
  rects: [] as SlotRect[]
})

const rootEl = ref<HTMLElement | null>(null)
let suppressNextClick = false
let clickGuardTimer: ReturnType<typeof setTimeout> | null = null
let settleTimer: ReturnType<typeof setTimeout> | null = null

const draggedSource = computed(() =>
  drag.from >= 0 && drag.phase !== 'idle' ? props.sources[drag.from] : undefined
)
const showBoxes = computed(() => drag.phase === 'dragging' || drag.phase === 'returning')
const placeholderRect = computed<SlotRect | undefined>(() =>
  showBoxes.value ? drag.rects[drag.phase === 'returning' ? drag.from : drag.to] : undefined
)

function itemRects(): SlotRect[] {
  const el = rootEl.value
  if (!el) return []
  return Array.from(el.querySelectorAll<HTMLElement>('.sg-item')).map((n) => {
    const r = n.getBoundingClientRect()
    return { left: r.left, top: r.top, width: r.width, height: r.height }
  })
}

/** 拖拽态下卡片 i（非拖拽卡）应落到的槽位（工作序中的位置） */
function slotShift(i: number): number {
  const { from, to } = drag
  if (i === from || from === to) return i
  if (i > from && i <= to) return i - 1
  if (i >= to && i < from) return i + 1
  return i
}

function isGhosted(i: number): boolean {
  return showBoxes.value && i === drag.from
}

function itemStyle(i: number): Record<string, string> {
  if (drag.phase !== 'dragging' || i === drag.from) return {}
  const s = slotShift(i)
  if (s === i) return { transform: 'none' }
  const a = drag.rects[i]
  const b = drag.rects[s]
  if (!a || !b) return {}
  return { transform: `translate(${b.left - a.left}px, ${b.top - a.top}px)` }
}

const ghostStyle = computed<Record<string, string>>(() => {
  const out: Record<string, string> = {}
  const r = drag.rects[drag.from]
  if (r) {
    out.width = `${r.width}px`
    out.height = `${r.height}px`
    out.transform = `translate3d(${drag.gx}px, ${drag.gy}px, 0)`
  }
  return out
})

const placeholderStyle = computed<Record<string, string>>(() => {
  const out: Record<string, string> = {}
  const r = placeholderRect.value
  if (!r) {
    out.display = 'none'
    return out
  }
  out.left = `${r.left}px`
  out.top = `${r.top}px`
  out.width = `${r.width}px`
  out.height = `${r.height}px`
  return out
})

/** 行列命中：优先取包含 ghost 中心的槽位；落缝隙/网格外取中心距最近者 */
function hitSlot(x: number, y: number): number {
  const rects = drag.rects
  for (let i = 0; i < rects.length; i++) {
    const r = rects[i]
    if (x >= r.left && x <= r.left + r.width && y >= r.top && y <= r.top + r.height) return i
  }
  let best = drag.from
  let bestD = Infinity
  for (let i = 0; i < rects.length; i++) {
    const cx = rects[i].left + rects[i].width / 2
    const cy = rects[i].top + rects[i].height / 2
    const d = (cx - x) ** 2 + (cy - y) ** 2
    if (d < bestD) {
      bestD = d
      best = i
    }
  }
  return best
}

function beginDrag(e: PointerEvent) {
  drag.rects = itemRects()
  const r = drag.rects[drag.from]
  if (!r || drag.rects.length !== props.sources.length) {
    resetDrag()
    return
  }
  drag.grabDX = e.clientX - r.left
  drag.grabDY = e.clientY - r.top
  drag.gx = r.left
  drag.gy = r.top
  drag.to = drag.from
  drag.phase = 'dragging'
  document.body.classList.add(BODY_CLASS)
}

function onPointerDown(e: PointerEvent, i: number) {
  if (e.button !== 0 || !e.isPrimary || drag.phase !== 'idle') return
  const target = e.target as HTMLElement
  if (target.closest('.sc-switch')) return // 开关热区：交还给 click
  drag.phase = 'pending'
  drag.pointerId = e.pointerId
  drag.from = i
  drag.to = i
  drag.startX = e.clientX
  drag.startY = e.clientY
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
}

function onPointerMove(e: PointerEvent) {
  if (e.pointerId !== drag.pointerId) return
  if (drag.phase === 'pending') {
    if (Math.hypot(e.clientX - drag.startX, e.clientY - drag.startY) <= DRAG_THRESHOLD) return
    beginDrag(e)
  }
  if (drag.phase !== 'dragging') return
  drag.gx = e.clientX - drag.grabDX
  drag.gy = e.clientY - drag.grabDY
  const own = drag.rects[drag.from]
  drag.to = hitSlot(drag.gx + own.width / 2, drag.gy + own.height / 2)
}

function onPointerUp(e: PointerEvent) {
  if (e.pointerId !== drag.pointerId) return
  if (drag.phase === 'pending') {
    resetDrag() // 未过阈值 = 普通点击，不拦截
    return
  }
  if (drag.phase === 'returning') {
    finishSettle() // Escape 已启动回位，此次释放仅收尾
    return
  }
  if (drag.phase !== 'dragging') return
  if (drag.to === drag.from) {
    startReturn() // 落点无效（回原位）：取消
    return
  }
  const out = props.sources.map((s) => s.slug)
  const [moved] = out.splice(drag.from, 1)
  out.splice(drag.to, 0, moved)
  armClickGuard()
  resetDrag()
  emit('reorder', out)
}

function onPointerCancel() {
  if (drag.phase === 'dragging') startReturn()
  else if (drag.phase === 'pending' || drag.phase === 'returning') resetDrag()
}

function startReturn() {
  const r = drag.rects[drag.from]
  if (!r) {
    resetDrag()
    return
  }
  drag.phase = 'returning'
  drag.to = drag.from
  drag.gx = r.left
  drag.gy = r.top
  armClickGuard()
  settleTimer = setTimeout(finishSettle, SETTLE_MS + 40)
}

function finishSettle() {
  if (settleTimer) {
    clearTimeout(settleTimer)
    settleTimer = null
  }
  resetDrag()
}

function resetDrag() {
  document.body.classList.remove(BODY_CLASS)
  drag.phase = 'idle'
  drag.pointerId = -1
  drag.from = -1
  drag.to = -1
  drag.rects = []
}

function armClickGuard() {
  suppressNextClick = true
  if (clickGuardTimer) clearTimeout(clickGuardTimer)
  clickGuardTimer = setTimeout(() => {
    suppressNextClick = false
    clickGuardTimer = null
  }, 500)
}

/** 拖拽手势结束后浏览器仍会派发一次 click：吞掉它，防误触开关 */
function onClickCapture(e: MouseEvent) {
  if (!suppressNextClick) return
  suppressNextClick = false
  e.stopPropagation()
  e.preventDefault()
}

function onKeydown(e: KeyboardEvent) {
  if (e.key !== 'Escape') return
  if (drag.phase === 'dragging') startReturn()
  else if (drag.phase === 'pending') resetDrag()
}
document.addEventListener('keydown', onKeydown)

onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown)
  if (settleTimer) clearTimeout(settleTimer)
  if (clickGuardTimer) clearTimeout(clickGuardTimer)
  document.body.classList.remove(BODY_CLASS)
})
</script>

<template>
  <div
    ref="rootEl"
    class="sg-root"
    @pointermove="onPointerMove"
    @pointerup="onPointerUp"
    @pointercancel="onPointerCancel"
    @click.capture="onClickCapture"
    @dragstart.prevent
  >
    <div
      v-for="(s, i) in sources"
      :key="s.slug"
      class="sg-item"
      :data-slug="s.slug"
      :class="{ 'sg-ghosted': isGhosted(i) }"
      :style="itemStyle(i)"
      @pointerdown="onPointerDown($event, i)"
    >
      <SourceCard
        :source="s"
        :busy="meta(s.slug).busy"
        :error="meta(s.slug).err"
        @toggle="(slug: string, next: boolean) => emit('toggle', slug, next)"
      />
    </div>

    <!-- 拖拽中的目标槽占位（虚线半透明）与跟手 ghost -->
    <div v-if="placeholderRect" class="sg-placeholder" :style="placeholderStyle" />
    <div
      v-if="showBoxes && draggedSource"
      class="sg-ghost"
      :class="{ 'sg-settle': drag.phase === 'returning' }"
      :style="ghostStyle"
    >
      <SourceCard
        :source="draggedSource"
        :busy="meta(draggedSource.slug).busy"
        :error="meta(draggedSource.slug).err"
      />
    </div>
  </div>
</template>

<style scoped>
.sg-root {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(230px, 1fr));
  gap: 10px;
  margin-top: 8px;
}
.sg-item {
  cursor: grab;
  border-radius: var(--guo-radius);
  user-select: none;
  transition: transform 120ms ease-out, box-shadow 120ms ease-out;
}
/* hover 微抬升：提示卡片可拖动 */
.sg-item:hover:not(.sg-ghosted) {
  transform: translateY(-1px);
  box-shadow: 0 4px 12px var(--guo-shadow);
}
.sg-ghosted {
  visibility: hidden;
}
.sg-placeholder {
  position: fixed;
  z-index: 79;
  pointer-events: none;
  border-radius: var(--guo-radius);
  border: 1.5px dashed color-mix(in srgb, var(--accent) 55%, transparent);
  background: color-mix(in srgb, var(--accent) 10%, transparent);
  transition: left 120ms ease-out, top 120ms ease-out;
}
.sg-ghost {
  position: fixed;
  left: 0;
  top: 0;
  z-index: 80;
  pointer-events: none;
}
.sg-ghost :deep(.sc) {
  box-shadow: 0 10px 28px var(--guo-shadow);
  transform: scale(1.03);
  border-color: var(--accent);
}
.sg-settle {
  transition: transform 180ms ease-out;
}
</style>

<style>
/* 拖拽期间的全局光标/选择抑制（限定类名生效，卸载时移除） */
body.zgj-src-dragging,
body.zgj-src-dragging * {
  cursor: grabbing !important;
  user-select: none !important;
}
</style>
