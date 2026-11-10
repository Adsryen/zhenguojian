<script setup lang="ts">
/* PosterGrid —— 虚拟滚动海报网格：
   - 列数按容器宽度自适应（min 列宽 160px）
   - 行高 = 列宽×1.4(3:4 封面) + 标题区
   - 只渲染视口 ±1 屏行（绝对定位 + transform 位移）
   - 卡片：封面走 resolveCover（核心下载 + HEIC→JPEG 转码 → data URL；mock 原样 URL），
     解析完成前/失败给 surface2 占位+标题首字兜底（不渲染 raw HEIC，避免必挂的 <img>）；
     VIP / 18+ badge 槽、更新角标；click emit('select', drama) */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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
  }>(),
  { loading: false, loadingMore: false }
)

const emit = defineEmits<{
  (e: 'select', drama: Drama): void
  (e: 'loadMore'): void
}>()

const scroller = ref<HTMLElement | null>(null)
const viewW = ref(0)
const viewH = ref(0)
const scrollTop = ref(0)

const MIN_COL = 160
const GAP = 14
const TITLE_H = 54
const BUFFER_ROWS = 1

const cols = computed(() => Math.max(1, Math.floor((viewW.value + GAP) / (MIN_COL + GAP))))
const colW = computed(() => {
  const n = cols.value
  return (viewW.value - GAP * (n - 1)) / n
})
const coverH = computed(() => colW.value * 1.4)
const rowH = computed(() => coverH.value + TITLE_H + GAP)
const rowCount = computed(() => Math.ceil(props.items.length / cols.value))
const totalH = computed(() => rowCount.value * rowH.value)

const firstRow = computed(() => Math.max(0, Math.floor(scrollTop.value / rowH.value) - BUFFER_ROWS))
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
  () => [scrollTop.value, totalH.value, viewH.value] as const,
  ([st, total, vh]) => {
    if (total <= 0) return
    if (st + vh >= total - rowH.value * 1.5) emit('loadMore')
  }
)

function onScroll() {
  const el = scroller.value
  if (el) scrollTop.value = el.scrollTop
}

let ro: ResizeObserver | null = null
onMounted(() => {
  const el = scroller.value
  if (!el) return
  viewW.value = el.clientWidth
  viewH.value = el.clientHeight
  ro = new ResizeObserver(() => {
    if (scroller.value) {
      viewW.value = scroller.value.clientWidth
      viewH.value = scroller.value.clientHeight
    }
  })
  ro.observe(el)
})
onBeforeUnmount(() => ro?.disconnect())

/* 封面加载失败标记 */
const broken = ref(new Set<string>())
function markBroken(id: string) {
  broken.value = new Set(broken.value).add(id)
}

function fallbackChar(d: Drama) {
  return d.title?.trim()?.[0] || '剧'
}
</script>

<template>
  <div ref="scroller" class="pg-scroller" @scroll.passive="onScroll">
    <!-- 首次加载：骨架 -->
    <div v-if="loading && !items.length" class="pg-skeleton" :style="{ gap: GAP + 'px' }">
      <SkeletonCard v-for="n in 12" :key="n" :width="colW" />
    </div>

    <div v-else-if="!items.length" class="pg-empty">没有内容</div>

    <div v-else class="pg-viewport" :style="{ height: totalH + 'px' }">
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

            <!-- badge 槽 -->
            <div class="badges-top">
              <span v-if="d.vip === true" class="badge badge-vip">VIP</span>
              <span v-if="d.adult || (d.tags || []).includes('18+')" class="badge badge-adult">18+</span>
            </div>
            <span v-if="d.releaseStatus" class="badge badge-update">{{ d.releaseStatus }}</span>
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
</template>

<style scoped>
.pg-scroller {
  height: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 4px 24px 24px;
  position: relative;
}
.pg-viewport {
  position: relative;
}
.pg-row {
  position: absolute;
  top: 0;
  left: 24px;
  right: 24px;
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
.badge-update {
  position: absolute;
  right: 6px;
  bottom: 6px;
  background: var(--guo-badge-bg);
  color: var(--on-accent);
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
