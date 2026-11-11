<script setup lang="ts">
/* PosterGrid —— 虚拟滚动海报网格：
   - 列数按「行实际渲染宽度」自适应（min 列宽 160px）：行宽 = scroller.clientWidth
     扣掉左右 padding（PAD_X×2）；colW 向下取整两位小数，保证 n×colW+gaps ≤ 行宽，
     杜绝任何收缩（历史 bug：colW 按含 padding 的 clientWidth 算、行盒又额外 24px
     内缩 → 每行恒溢出 96px → flex-shrink 统一收缩，而 nowrap 长标题卡片被
     min-width:auto 卡在 min-content → 该卡（常为搜索首条全名命中）比同排其余大）
   - 卡片 flex-shrink:0 + min-width:0，封面区固定 aspect 1:1.4 + img object-fit:cover，
     首卡/加载中/图未回一律同尺寸
   - 行高 = 列宽×1.4(3:4 封面) + 标题区
   - 只渲染视口 ±1 屏行（绝对定位 + transform 位移）
   - 卡片：封面走 resolveCover（核心下载 + HEIC→JPEG 转码 → data URL；mock 原样 URL），
     解析完成前/失败给 surface2 占位+标题首字兜底（不渲染 raw HEIC，避免必挂的 <img>）；
     VIP / 18+ badge 槽（左上）+ 集数徽标（右下，v1 poster_wall._badge_text 语义）；
     click emit('select', drama)
   - 顶部 hero 槽（slot name="hero"，v1 PosterWall.set_hero 的对应物）：随墙一起滚走，
     虚拟滚动偏移用 .pg-viewport 的 offsetTop 实测（contentTop），不写死 hero 高度
   - 下拉刷新（refreshable + emit('refresh')，v1 browse_panel.py:151 set_refresh_enabled/
     refreshRequested 的手势版）：仅在顶部起手势，跟手位移 + 松手回弹，超阈值 emit refresh；
     滚轮不参与（桌面滚轮场景不强制） */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { Drama } from '@/services/types'
import { resolveCover } from '@/services/covers'
import SkeletonCard from './SkeletonCard.vue'

const props = withDefaults(
  defineProps<{
    items: Drama[]
    loading?: boolean
    loadingMore?: boolean
    /** 卡片 id → 是否收藏（追更）状态槽，V2-2 起接 */
    selectedIds?: Set<string>
    /** 顶部下拉刷新手势（v1 browse_panel set_refresh_enabled 语义），找剧页开，搜索/剧库不开 */
    refreshable?: boolean
    /** 顶部 hero 槽是否有内容（宿主有 slot 但本次不渲染 hero 时传 false，
        避免空 wrapper 的 margin 把墙整体下推 16px） */
    showHero?: boolean
  }>(),
  { loading: false, loadingMore: false, refreshable: false, showHero: false }
)

const emit = defineEmits<{
  (e: 'select', drama: Drama): void
  (e: 'loadMore'): void
  (e: 'refresh'): void
}>()

/* 组件内中文文案常量表（i18n.ts 不归本任务；键表见返报） */
const L = {
  epiTotal: (n: number) => `共${n}集`,
  epiUpdate: (n: number) => `更新至${n}集`,
  statusOngoing: '更新中',
  statusFinished: '已完结',
  pull: '下拉刷新',
  release: '松开刷新',
  refreshing: '正在刷新…'
}

/* 集数徽标（右下）：照 v1 poster_wall._badge_text:52-69「集数优先，其次更新状态」，
   并针对 v2 真实数据形状（releaseStatus 混着枚举 ongoing/finished 与中文串）做本地化：
   - releaseStatus 自带集数（如「更新至40集」）→ 原样用：状态+进度双信息，比 episodes 总数更准
   - 有 episodes：ongoing 语义 → 「更新至N集」；否则 → 「共N集」
   - 无 episodes：枚举串映射成中文（ongoing→更新中 / finished→已完结），其它文本原样透传
   与 v1 的差异：v1 默认口径是「全N集」，这里按任务口径统一成「共N集」（v2 详情页
   detail.episodesTotal 亦为「共 {n} 集」，同屏术语一致）。 */
const ONGOING_RE = /(更新至|更新中|连载|在更|未完结)/
function badgeText(d: Drama): string {
  const status = String(d.releaseStatus ?? '').trim()
  const eps = Number(d.episodes ?? 0)
  if (/\d+\s*集/.test(status)) return status
  if (eps > 0) return ONGOING_RE.test(status) || status === 'ongoing' ? L.epiUpdate(eps) : L.epiTotal(eps)
  if (status === 'ongoing') return L.statusOngoing
  if (status === 'finished') return L.statusFinished
  return status
}

const scroller = ref<HTMLElement | null>(null)
const viewportEl = ref<HTMLElement | null>(null)
const heroEl = ref<HTMLElement | null>(null)
const viewW = ref(0)
const viewH = ref(0)
const scrollTop = ref(0)
/* contentTop：.pg-viewport 顶边到滚动内容起点的实际距离（= scroller 上 padding + hero 高 + hero 下边距）。
   实测而非算定值：hero 槽空/隐藏/换行高度都自动对上，虚拟滚动的行窗口才不会错位。 */
const contentTop = ref(0)

const MIN_COL = 160
const GAP = 14
const TITLE_H = 54
const BUFFER_ROWS = 1
// PAD_X 必须与 CSS .pg-content 左右 padding 一致：行内容可用宽 = clientWidth - PAD_X*2
const PAD_X = 24

/* 下拉刷新（v1 browse_panel.py:151 refreshRequested 的手势版）：
   位移按 PTR_DAMP 阻尼，最多 PTR_MAX；松手时 ≥PTR_THRESHOLD 才触发。 */
const PTR_THRESHOLD = 80
const PTR_DAMP = 0.5
const PTR_MAX = 130
const PTR_HOLD = 44

// rowW：行盒的实际内容宽（历史 bug 根因即此处曾直接用含 padding 的 viewW）
const rowW = computed(() => Math.max(0, viewW.value - PAD_X * 2))
const cols = computed(() => Math.max(1, Math.floor((rowW.value + GAP) / (MIN_COL + GAP))))
const colW = computed(() => {
  const n = cols.value
  // 向下取整到 0.01px：保证 n×colW + (n-1)×GAP ≤ rowW，绝不触发收缩
  return Math.floor(((rowW.value - GAP * (n - 1)) / n) * 100) / 100
})
const coverH = computed(() => Math.round(colW.value * 1.4 * 100) / 100)
const rowH = computed(() => coverH.value + TITLE_H + GAP)
const rowCount = computed(() => Math.ceil(props.items.length / cols.value))
const totalH = computed(() => rowCount.value * rowH.value)

/* 行窗口用「视口内相对偏移」local = scrollTop - contentTop：hero 在顶部占位时，
   第一行要等 hero 滚上去之后才该被回收/渲染。 */
const localScroll = computed(() => scrollTop.value - contentTop.value)
const firstRow = computed(() => Math.max(0, Math.floor(localScroll.value / rowH.value) - BUFFER_ROWS))
const lastRow = computed(() =>
  Math.min(
    rowCount.value - 1,
    firstRow.value + Math.ceil(viewH.value / rowH.value) + BUFFER_ROWS * 2 - 1
  )
)

const rows = computed(() => {
  const out: { index: number; slice: { d: Drama; i: number }[] }[] = []
  for (let r = firstRow.value; r <= lastRow.value && rowCount.value > 0; r++) {
    const start = r * cols.value
    const slice = props.items
      .slice(start, start + cols.value)
      .map((d, k) => ({ d, i: start + k }))
    out.push({ index: r, slice })
  }
  return out
})

// 封面解析：coverSrc.get(d.id) 仅在解析出可用 src（data URL / mock 原 URL）后填充。
// 可见窗口（含 ±BUFFER 行）每次变化都触发 ensureCovers；resolveCover 自带
// LRU + in-flight 去重，重复调用同一 id 安全（命中缓存即刻返回）。
const coverSrc = ref(new Map<string, string>())
function setCoverSrc(id: string, src: string) {
  if (coverSrc.value.get(id) === src) return
  coverSrc.value = new Map(coverSrc.value).set(id, src)
}
function ensureCovers(list: Drama[]) {
  for (const d of list) {
    if (!d || !d.cover || coverSrc.value.has(d.id)) continue
    void resolveCover(d).then((src) => {
      if (src) setCoverSrc(d.id, src)
    })
  }
}
watch(
  rows,
  (rs) => ensureCovers(rs.flatMap((r) => r.slice.map((s) => s.d))),
  { immediate: true }
)

// 底部加载触发：渲染最后一段时通知父级（IntersectionObserver 过重，滚动距离判断够用）
watch(
  () => [localScroll.value, totalH.value, viewH.value] as const,
  ([local, total, vh]) => {
    if (total <= 0) return
    if (local + vh >= total - rowH.value * 1.5) emit('loadMore')
  }
)

function onScroll() {
  const el = scroller.value
  if (el) scrollTop.value = el.scrollTop
}

/* 几何测量：宽/高来自 scroller，contentTop 来自 .pg-viewport 的 offsetTop（hero 槽高度变 0↔260
   或骨架↔网格换分支时都要重取，故挂 RO + 分支变化后 nextTick 补一次）。 */
function measure() {
  const el = scroller.value
  if (!el) return
  viewW.value = el.clientWidth
  viewH.value = el.clientHeight
  const vp = viewportEl.value
  contentTop.value = vp ? vp.offsetTop : 0
}

let ro: ResizeObserver | null = null
function observe(el: HTMLElement | null) {
  if (!ro) ro = new ResizeObserver(() => measure())
  if (el) ro.observe(el)
}
function unobserve(el: HTMLElement | null) {
  if (ro && el) ro.unobserve(el)
}
watch(heroEl, (el, old) => {
  unobserve(old)
  observe(el)
  void nextTick(measure)
})
watch(viewportEl, (el, old) => {
  unobserve(old)
  observe(el)
  void nextTick(measure)
})
onMounted(() => {
  measure()
  observe(scroller.value)
  observe(heroEl.value)
  observe(viewportEl.value)
})
onBeforeUnmount(() => {
  ro?.disconnect()
  ro = null
})

/* 封面加载失败标记 */
const broken = ref(new Set<string>())
function markBroken(id: string) {
  broken.value = new Set(broken.value).add(id)
}

function fallbackChar(d: Drama) {
  return d.title?.trim()?.[0] || '剧'
}

/* ---------------- 下拉刷新（v1 browse_panel.py:151 set_refresh_enabled 的手势版） ----------------
   - 只在 scrollTop≤0 且向下拖时起势；dy×PTR_DAMP、封顶 PTR_MAX；松手 ≥PTR_THRESHOLD 才 emit refresh
   - 位移打在 .pg-content 上（跟手），松手回弹走 transition；触发后驻留 PTR_HOLD 等父级 loading 走完
   - window 级 move/up/cancel：指针滑出滚动区也不丢手势
   - 滚轮不参与（桌面滚轮场景不强制，任务口径）
   - 手势后 300ms 内吞一次残余 click（.pg-content 捕获阶段），避免「下拉误触某张卡片进详情」 */
const pullDist = ref(0)
const refreshing = ref(false)
let dragging = false
let startY = 0
let moved = false
let swallowClick = false
let releaseAt = 0
let refreshTimer: number | null = null

const pullState = computed(() =>
  refreshing.value ? 'refreshing' : pullDist.value >= PTR_THRESHOLD ? 'ready' : 'pull'
)
const pullText = computed(() =>
  refreshing.value ? L.refreshing : pullDist.value >= PTR_THRESHOLD ? L.release : L.pull
)
const contentStyle = computed(() => ({
  transform: pullDist.value ? `translateY(${pullDist.value}px)` : '',
  transition: dragging ? 'none' : 'transform .26s cubic-bezier(.22,.61,.36,1)'
}))

function ptrDown(e: PointerEvent) {
  if (!props.refreshable || refreshing.value) return
  if (e.pointerType === 'mouse' && e.button !== 0) return
  const el = scroller.value
  if (!el || el.scrollTop > 0) return
  dragging = true
  moved = false
  startY = e.clientY
  pullDist.value = 0
}

function ptrMove(e: PointerEvent) {
  if (!dragging) return
  const dy = e.clientY - startY
  if (dy <= 0) {
    // 已滚动或反向：放弃本次手势，交回原生滚动
    if (moved && scroller.value && scroller.value.scrollTop > 0) {
      dragging = false
      pullDist.value = 0
    }
    return
  }
  if (dy > 2) moved = true
  pullDist.value = Math.min(PTR_MAX, dy * PTR_DAMP)
  if (pullDist.value > 0) e.preventDefault()
}

function ptrUp() {
  if (!dragging) return
  dragging = false
  const go = moved && pullDist.value >= PTR_THRESHOLD
  if (moved) {
    swallowClick = true
    releaseAt = Date.now()
  }
  pullDist.value = refreshing.value ? PTR_HOLD : 0
  if (go) startRefresh()
}

function startRefresh() {
  refreshing.value = true
  pullDist.value = PTR_HOLD
  emit('refresh')
  if (refreshTimer) window.clearTimeout(refreshTimer)
  // 安全阀：父级没把 loading 拉起来时 6s 后自动收回，指示条不会卡死
  refreshTimer = window.setTimeout(() => {
    if (refreshing.value) endRefresh()
  }, 6000)
}

function endRefresh() {
  refreshing.value = false
  pullDist.value = 0
  if (refreshTimer) {
    window.clearTimeout(refreshTimer)
    refreshTimer = null
  }
}

watch(
  () => props.loading,
  (now, prev) => {
    if (refreshing.value && prev && !now) endRefresh()
  }
)

onMounted(() => {
  window.addEventListener('pointermove', ptrMove, { passive: false })
  window.addEventListener('pointerup', ptrUp)
  window.addEventListener('pointercancel', ptrUp)
})
onBeforeUnmount(() => {
  window.removeEventListener('pointermove', ptrMove)
  window.removeEventListener('pointerup', ptrUp)
  window.removeEventListener('pointercancel', ptrUp)
  if (refreshTimer) window.clearTimeout(refreshTimer)
})

function onContentClickCapture(e: MouseEvent) {
  if (!swallowClick) return
  if (Date.now() - releaseAt > 300) {
    swallowClick = false
    return
  }
  swallowClick = false
  e.stopPropagation()
  e.preventDefault()
}
</script>

<template>
  <div
    ref="scroller"
    class="pg-scroller"
    @scroll.passive="onScroll"
    @pointerdown="ptrDown"
  >
    <!-- 下拉刷新指示：绝对贴在 scroller 顶部（不在 .pg-content 内，避免被位移影响坐标系） -->
    <div
      v-if="refreshable"
      class="pg-ptr"
      data-testid="ptr-indicator"
      :class="'pg-ptr--' + pullState"
      :style="{ opacity: refreshing ? 1 : Math.min(1, pullDist / PTR_THRESHOLD) }"
    >
      <span class="pg-ptr-spin" :class="{ spinning: refreshing }" />
      <span class="pg-ptr-text">{{ pullText }}</span>
    </div>

    <div class="pg-content" :style="contentStyle" @click.capture="onContentClickCapture">
      <!-- hero 槽（v1 PosterWall.set_hero 的对应物）：整块随墙滚走 -->
      <div v-if="showHero && items.length" ref="heroEl" class="pg-hero" data-testid="poster-hero">
        <slot name="hero" />
      </div>

      <!-- 首次加载：骨架 -->
      <div v-if="loading && !items.length" class="pg-skeleton" :style="{ gap: GAP + 'px' }">
        <SkeletonCard v-for="n in 12" :key="n" :width="colW" />
      </div>

      <div v-else-if="!items.length" class="pg-empty">没有内容</div>

      <div v-else ref="viewportEl" class="pg-viewport" :style="{ height: totalH + 'px' }">
        <div
          v-for="row in rows"
          :key="row.index"
          class="pg-row"
          :style="{
            transform: `translateY(${row.index * rowH}px)`,
            gap: GAP + 'px'
          }"
        >
          <div
            v-for="{ d, i } in row.slice"
            :key="d.id + '#' + i"
            class="poster"
            :style="{ width: colW + 'px' }"
            role="button"
            tabindex="0"
            @click="emit('select', d)"
            @keyup.enter="emit('select', d)"
          >
            <div class="cover" :style="{ height: coverH + 'px' }">
              <img
                v-if="coverSrc.get(d.id) && !broken.has(d.id)"
                :src="coverSrc.get(d.id)"
                :alt="d.title"
                loading="lazy"
                decoding="async"
                @error="markBroken(d.id)"
              />
              <div v-else class="cover-fallback">{{ fallbackChar(d) }}</div>

              <!-- badge 槽：左上 VIP / 18+，右下集数（v1 双徽标语义） -->
              <div class="badges-top">
                <span v-if="d.vip === true" class="badge badge-vip">VIP</span>
                <span v-if="d.adult || (d.tags || []).includes('18+')" class="badge badge-adult">18+</span>
              </div>
              <span v-if="badgeText(d)" class="badge badge-epi" data-testid="epi-badge">{{ badgeText(d) }}</span>
            </div>
            <div class="meta">
              <div class="title" :title="d.title">{{ d.title }}</div>
              <div class="sub text-muted">
                <span v-if="d.category">{{ d.category }}</span>
                <span v-if="d.heat"> · {{ d.heat }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 翻页加载条 -->
      <div v-if="loadingMore" class="pg-more">加载中…</div>
    </div>
  </div>
</template>

<style scoped>
.pg-scroller {
  height: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  /* padding 移到 .pg-content：下拉位移作用在 .pg-content 上，若 scroller 自带 padding-top，
     拉开时顶部会露出 scroller 自己的底色（一条缝合线）。scroller 仍 position:relative，
     故 .pg-viewport.offsetTop = 上 padding + hero 高 + hero 下边距，contentTop 实测照旧成立 */
  padding: 0;
  position: relative;
  overscroll-behavior: contain;
}
.pg-content {
  padding: 4px 24px 24px;
}
.pg-hero {
  /* v1 poster_wall.py HERO_PAD = 16 */
  margin-bottom: 16px;
}
.pg-viewport {
  position: relative;
}
.pg-row {
  position: absolute;
  top: 0;
  /* 行盒满宽 = viewport 内容宽（scroller padding 已扣）；历史 left/right 24px 与
     padding 双重内缩导致每行恒溢出 96px，是首卡/长标题卡被 flex 收缩拉大的根因 */
  left: 0;
  right: 0;
  display: flex;
}
.pg-skeleton {
  display: flex;
  flex-wrap: wrap;
}
.pg-empty {
  padding: 60px 0;
  text-align: center;
  color: var(--muted);
}
.pg-more {
  text-align: center;
  color: var(--muted);
  font-size: 12px;
  padding: 12px 0 2px;
}

.poster {
  cursor: pointer;
  border-radius: var(--guo-radius);
  outline: none;
  /* 关键：禁收缩 + 清 min-width:auto —— 否则长标题（nowrap 首行）的 min-content
     会卡住该卡收缩下限，溢出时该卡比同排其余宽（首卡变大的直接机制） */
  flex-shrink: 0;
  min-width: 0;
}
.poster:focus-visible .cover,
.poster:hover .cover {
  box-shadow: 0 6px var(--guo-shadow-blur) var(--guo-shadow);
  border-color: var(--accent);
}

.cover {
  position: relative;
  border-radius: var(--guo-radius);
  overflow: hidden;
  background: var(--surface-3);
  border: 1px solid var(--border);
  transition: box-shadow 0.15s, border-color 0.15s;
  /* 封面区固定 1:1.4（与内联 height=colW*1.4 等价，双保险：宽度由卡片列宽决定，
     高度不随图片 intrinsic/加载状态变化 → 首卡/加载中/图未回尺寸一致） */
  aspect-ratio: 1 / 1.4;
}
.cover img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.cover-fallback {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 42px;
  font-weight: 700;
  color: var(--muted-2);
  background: linear-gradient(160deg, var(--surface-3), var(--surface));
}

.badges-top {
  position: absolute;
  top: 6px;
  left: 6px;
  display: flex;
  gap: 4px;
}
.badge {
  font-size: 10.5px;
  font-weight: 700;
  line-height: 1;
  padding: 4px 6px;
  border-radius: 4px;
  backdrop-filter: blur(4px);
}
.badge-vip {
  background: var(--guo-vip-bg);
  color: var(--guo-vip-fg);
}
.badge-adult {
  background: var(--danger);
  color: var(--on-accent);
}
.badge-epi {
  position: absolute;
  right: 6px;
  bottom: 6px;
  background: var(--guo-badge-bg);
  color: var(--on-accent);
  font-weight: 600;
  letter-spacing: 0.2px;
  max-width: calc(100% - 12px);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 下拉刷新指示条 */
.pg-ptr {
  position: absolute;
  z-index: 3;
  top: 0;
  left: 0;
  right: 0;
  height: 42px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  font-size: 12px;
  color: var(--muted);
  pointer-events: none;
}
.pg-ptr-spin {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  border: 2px solid var(--muted-2);
  border-top-color: var(--accent);
  opacity: 0.65;
}
.pg-ptr--ready .pg-ptr-spin {
  opacity: 1;
}
.pg-ptr-spin.spinning {
  opacity: 1;
  animation: pg-ptr-spin 0.8s linear infinite;
}
@keyframes pg-ptr-spin {
  to {
    transform: rotate(360deg);
  }
}

.meta {
  padding: 7px 2px 0;
}
.title {
  font-size: 13px;
  line-height: 18px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.sub {
  font-size: 11.5px;
  line-height: 16px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
