<script setup lang="ts">
/* SliderCaptcha —— 青空（sorani）滑块验证码：背景图 + 拼图模板叠加 + 底部滑轨。
   与 v1（sorani_login.py SliderCaptcha）采集语义 1:1，坐标空间按任务书改原图口径：
   - 真实 mousedown/mousemove/mouseup 事件链（move/up 挂 window 级监听；服务端拒收纯合成瞬移轨迹）；
   - 按下点=轨迹原点，首点 {x:0,y:0,type:'down',t:0}；t=距 down 的单调毫秒；
   - 显示空间 x=clamp(round(dx),0,显示宽−显示模板宽)，上报按 600 空间归一：
     x=round(显示x × backgroundWidth/显示宽) ∈ [0, backgroundWidth−templateWidth]（600/110 → [0,490]）；
     y=round(dy × scale) 不钳制（v1 对齐）；
   - 点数上限 4000（Go valid() 上限 5000，留余量）；
   - 释放时位移 <2（显示 px）或点数 <2 → 判定非真人拖动，reset 重来（v1 REJECTED_MIN_OFFSET）；
   - track 上报验证码原图尺寸（bgImageWidth=backgroundWidth=600），与坐标同空间。 */
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { t } from '@/services/i18n'
import type { SoraniCaptcha, SoraniTrack, SoraniTrackPoint } from '@/services/types'

const props = defineProps<{ captcha: SoraniCaptcha | null; disabled?: boolean }>()
const emit = defineEmits<{
  (e: 'solved', track: SoraniTrack): void
  (e: 'refresh'): void
}>()

/* v1 常量（sorani_login.py:52-58），滑轨视觉同款 */
const DISPLAY_MAX_WIDTH = 300
const TRACK_HEIGHT = 40
const TRACK_GAP = 10
const HANDLE_MIN_WIDTH = 40
const TRACK_LIMIT_POINTS = 4000
const REJECTED_MIN_OFFSET = 2

const dragging = ref(false)
const offset = ref(0) // 模板/把手左缘（显示空间 px）

let origin = { x: 0, y: 0 }
let downAt = 0 // performance.now() 基线（t 单调）
let startMs = 0 // Date.now() 基线（startTime）
let points: SoraniTrackPoint[] = []

const dispW = computed(() =>
  props.captcha ? Math.min(DISPLAY_MAX_WIDTH, props.captcha.backgroundWidth) : DISPLAY_MAX_WIDTH
)
const dispH = computed(() =>
  props.captcha
    ? Math.round((props.captcha.backgroundHeight * dispW.value) / props.captcha.backgroundWidth)
    : 180
)
const tplDispW = computed(() =>
  props.captcha
    ? Math.max(1, Math.round((props.captcha.templateWidth * dispW.value) / props.captcha.backgroundWidth))
    : 55
)
const scale = computed(() => (props.captcha ? props.captcha.backgroundWidth / dispW.value : 2))
const limitDisp = computed(() => Math.max(1, dispW.value - tplDispW.value))
const handleW = computed(() => Math.max(tplDispW.value, HANDLE_MIN_WIDTH))

/** 显示空间左缘 → 原图空间 x，钳 [0, backgroundWidth−templateWidth]（600/110 → [0,490]） */
function normX(xDisp: number): number {
  const c = props.captcha
  if (!c) return 0
  return Math.max(0, Math.min(c.backgroundWidth - c.templateWidth, Math.round(xDisp * scale.value)))
}

function detach() {
  window.removeEventListener('mousemove', onMove)
  window.removeEventListener('mouseup', onUp)
}

function reset() {
  detach()
  dragging.value = false
  offset.value = 0
  points = []
}

function onDown(ev: MouseEvent) {
  if (!props.captcha || props.disabled || ev.button !== 0) return
  ev.preventDefault()
  dragging.value = true
  origin = { x: ev.clientX, y: ev.clientY }
  downAt = performance.now()
  startMs = Date.now()
  offset.value = 0
  points = [{ x: 0, y: 0, type: 'down', t: 0 }] // 首点固定原点（v1 _on_press 同款）
  window.addEventListener('mousemove', onMove)
  window.addEventListener('mouseup', onUp)
}

function collect(ev: MouseEvent, kind: 'move' | 'up') {
  const dy = ev.clientY - origin.y
  offset.value = Math.max(0, Math.min(limitDisp.value, Math.round(ev.clientX - origin.x)))
  if (points.length >= TRACK_LIMIT_POINTS) return
  points.push({
    x: normX(offset.value),
    y: Math.round(dy * scale.value), // y 不钳制（v1 对齐）
    type: kind,
    t: Math.max(0, Math.round(performance.now() - downAt))
  })
}

function onMove(ev: MouseEvent) {
  if (dragging.value) collect(ev, 'move')
}

function onUp(ev: MouseEvent) {
  if (!dragging.value) return
  detach()
  dragging.value = false
  const c = props.captcha
  if (!c) return
  collect(ev, 'up') // 尾点 up（v1 _on_release 对齐：先追加后判定）
  if (offset.value < REJECTED_MIN_OFFSET || points.length < 2) {
    reset() // 真人判定不过：拒发重来（v1: offset<2px 或点数<2）
    return
  }
  const track: SoraniTrack = {
    bgImageWidth: c.backgroundWidth, // 原图口径：600
    bgImageHeight: c.backgroundHeight, // 360
    templateImageWidth: c.templateWidth, // 110
    templateImageHeight: c.templateHeight, // 360
    startTime: startMs,
    stopTime: Date.now(),
    trackList: points.slice()
  }
  emit('solved', track)
}

function onRefresh() {
  reset()
  emit('refresh')
}

watch(
  () => props.captcha,
  () => reset()
)
onBeforeUnmount(detach)

defineExpose({ reset })
</script>

<template>
  <div class="sc" :style="{ width: dispW + 'px' }" data-testid="sorani-slider">
    <div v-if="!captcha" class="sc-empty" data-testid="sorani-captcha-loading">
      {{ t('sorani.captchaLoading') }}
    </div>
    <template v-else>
      <div class="sc-stage" :style="{ width: dispW + 'px', height: dispH + 'px' }">
        <img
          class="sc-bg"
          data-testid="sorani-bg-img"
          :src="captcha.backgroundImage"
          :width="dispW"
          :height="dispH"
          alt=""
          draggable="false"
        />
        <img
          class="sc-tpl"
          data-testid="sorani-tpl-img"
          :src="captcha.templateImage"
          :style="{ left: offset + 'px', width: tplDispW + 'px', height: dispH + 'px' }"
          alt=""
          draggable="false"
        />
      </div>
      <div class="sc-track" :style="{ height: TRACK_HEIGHT + 'px', marginTop: TRACK_GAP + 'px' }">
        <div class="sc-fill" :style="{ width: offset + handleW + 'px' }"></div>
        <div
          class="sc-handle"
          :class="{ 'is-drag': dragging, 'is-disabled': disabled }"
          data-testid="sorani-handle"
          :style="{ left: offset + 'px', width: handleW + 'px' }"
          @mousedown="onDown"
        >⇄</div>
        <button class="sc-refresh" type="button" data-testid="sorani-refresh-btn" @click="onRefresh">
          {{ t('sorani.refresh') }}
        </button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.sc {
  user-select: none;
}
.sc-stage {
  position: relative;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  line-height: 0;
}
.sc-bg {
  display: block;
}
.sc-tpl {
  position: absolute;
  top: 0;
  pointer-events: none;
}
.sc-track {
  position: relative;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  overflow: hidden;
}
.sc-fill {
  position: absolute;
  top: 0;
  left: 0;
  bottom: 0;
  width: 0;
  background: var(--accent-soft);
  border-radius: 8px 0 0 8px;
}
.sc-handle {
  position: absolute;
  top: -1px;
  bottom: -1px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface-3);
  color: var(--muted);
  font-size: 14px;
  cursor: grab;
}
.sc-handle.is-drag {
  cursor: grabbing;
  background: var(--accent-soft);
  color: var(--accent);
}
.sc-handle.is-disabled {
  cursor: not-allowed;
  opacity: 0.6;
}
.sc-refresh {
  position: absolute;
  right: 6px;
  top: 50%;
  transform: translateY(-50%);
  z-index: 2;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface-2);
  color: var(--text);
  font-size: 12px;
  padding: 2px 8px;
  cursor: pointer;
}
.sc-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 180px;
  border: 1px dashed var(--border);
  border-radius: 8px;
  color: var(--muted);
  font-size: 13px;
}
</style>
