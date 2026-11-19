<!--
DanmakuCanvas.vue —— v3 web 模式（P1）弹幕覆盖层。

桌面形态的弹幕由 mpv 用 ASS 渲染（容器里没有 mpv），web 模式换成
canvas 自绘：数据走现有 `DanmakuAll(session, durationSec)` 绑定
（Go 侧已抓好的全集弹幕，形状 internal/danmaku.Item），绘制规则
对齐 B 站经典滚动语义：
  · 每条从右边缘滚出，显示时长=durationMs（缺省 6s）
  · 车道防撞：每条占用一条车道，直到它的前一条滚出右缘
  · mode 4/5（顶/底固定）按 B 站语义固定位置淡入淡出
pointer-events:none，不拦视频区的点按/拖动。
-->
<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '@/services/api'
import type { DanmakuItem } from '@/services/types'

const props = defineProps<{
  session: string
  video: HTMLVideoElement | null
  enabled: boolean
  /** 不透明度 20–100（与 v1 同量纲，100=不透明） */
  opacity: number
  onCount: (n: number) => void
}>()

const canvas = ref<HTMLCanvasElement | null>(null)
let raf = 0
let items: DanmakuItem[] = []
let laneEnds: number[] = [] // 每条车道被占用到（视频秒）
const DEFAULT_DISP = 6 // 缺省显示秒（v1/mpv ASS 默认 6s 同口径）

function dispSec(it: DanmakuItem): number {
  const d = Number(it.durationMs)
  return d > 0 ? d / 1000 : DEFAULT_DISP
}

async function load() {
  const v = props.video
  if (!props.session || !v || !(v.duration > 0)) return
  try {
    const res = await api.danmakuAll(props.session, Math.ceil(v.duration))
    items = res?.items ?? []
    laneEnds = []
    if (typeof res?.total === 'number') props.onCount(res.total)
    else if (items.length) props.onCount(items.length)
  } catch {
    items = [] // 弹幕拿不到不阻塞播放（与桌面形态同语义：静默降级）
  }
}

watch(() => props.session, (s, old) => {
  if (s && s !== old) void load()
  else {
    items = []
    laneEnds = []
  }
})

function draw() {
  raf = requestAnimationFrame(draw)
  const cv = canvas.value
  const v = props.video
  if (!cv || !v) return
  const W = cv.clientWidth
  const H = cv.clientHeight
  if (W === 0 || H === 0) return
  if (cv.width !== W || cv.height !== H) {
    cv.width = W
    cv.height = H
  }
  const ctx = cv.getContext('2d')
  if (!ctx) return
  ctx.clearRect(0, 0, W, H)
  if (!props.enabled || !items.length) return
  ctx.globalAlpha = Math.max(0.2, Math.min(1, props.opacity / 100))
  const t = v.currentTime
  const fontPx = Math.max(18, Math.round(H / 18))
  ctx.font = `bold ${fontPx}px "PingFang SC","Microsoft YaHei",sans-serif`
  ctx.textBaseline = 'top'
  const maxLanes = 12

  for (const it of items) {
    const s = (it.timeMs ?? 0) / 1000
    const disp = dispSec(it)
    if (t < s - 0.2 || t > s + disp + 0.2) continue // 窗口外不画（留 0.2s 防抖）
    const text = it.text ?? ''
    if (!text) continue
    ctx.lineWidth = fontPx / 14
    ctx.strokeStyle = 'rgba(0,0,0,0.8)'
    ctx.fillStyle = it.color && /^#[0-9a-fA-F]{6}$/.test(it.color) ? it.color : '#ffffff'

    if (it.mode === 4 || it.mode === 5) {
      // 顶/底固定：右侧 30% 区域，进入 0.3s 淡入、结束 0.5s 淡出
      const prog = (t - s) / disp
      const a = prog < 0.05 ? prog / 0.05 : prog > 0.9 ? (1 - prog) / 0.1 : 1
      const saveAlpha = ctx.globalAlpha
      ctx.globalAlpha = saveAlpha * Math.max(0, Math.min(1, a))
      const x = W - ctx.measureText(text).width - fontPx * 0.5
      const y = it.mode === 4 ? H * 0.06 : H * 0.8
      ctx.strokeText(text, x, y)
      ctx.fillText(text, x, y)
      ctx.globalAlpha = saveAlpha
      continue
    }

    // 滚动（mode 1 与其他值一律按滚动处理——P1 简化，绝大多数条目本就是滚动）
    const tw = ctx.measureText(text).width
    const travel = W + tw
    const x = W - ((t - s) / disp) * travel
    // 车道分配：找一条前一条已滚出右缘的车道
    let lane = laneEnds.findIndex((e) => e <= s)
    if (lane === -1) {
      if (laneEnds.length >= maxLanes) continue // 车道满：跳过这条（防刷屏）
      lane = laneEnds.length
      laneEnds.push(0)
    }
    laneEnds[lane] = s + disp
    const y = fontPx * 0.5 + lane * fontPx * 1.25
    if (y > H - fontPx) continue // 超出画面高度的车道不画
    ctx.strokeText(text, x, y)
    ctx.fillText(text, x, y)
  }
}

onMounted(() => {
  void load()
  raf = requestAnimationFrame(draw)
})
onBeforeUnmount(() => {
  cancelAnimationFrame(raf)
})
</script>

<template>
  <canvas ref="canvas" class="pl-danmaku"></canvas>
</template>

<style scoped>
.pl-danmaku {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  pointer-events: none; /* 弹幕不拦视频区点按/进度拖动 */
  z-index: 2; /* 视频(默认流)之上、浮层提示(z 更高)之下 */
}
</style>
