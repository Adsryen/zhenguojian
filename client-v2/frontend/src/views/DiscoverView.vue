<script setup lang="ts">
/* Discover —— 找剧：SourceSwitch + 分类 chips + 首屏 hero 大卡 + 虚拟滚动海报墙（catalog 分页，lane=discover）
   分类来源：browseFilters(groups[key=category])；默认构建无 overlay → 兜底 hongguo 三分类
   （值取自 guoapp-src core/provider_hongguo_catalog.go hongguoAppGenres）。
   v1 质感对齐：hero 大卡（browse_panel.py:388-394 + poster_wall._WallHero）、下拉刷新手势
   （browse_panel.py:151 set_refresh_enabled/refreshRequested 的手势版，复用既有 loadFirst(force)）。 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { api, DEGRADED, MOCK_MODE } from '@/services/api'
import { ApiError, type Drama } from '@/services/types'
import { detailRoute } from '@/services/dramaRef'
import { t, tf } from '@/services/i18n'
import { coverDiagnosticText, coverDiagnostics, onCoverDiagnostics, type CoverDiagnostics } from '@/services/covers'
import SourceSwitch from '@/components/SourceSwitch.vue'
import PosterGrid from '@/components/PosterGrid.vue'
import HeroCard from '@/components/HeroCard.vue'

interface Chip {
  id: string
  name: string
}

const store = useAppStore()
const router = useRouter()

const items = ref<Drama[]>([])
const page = ref(0)
const hasMore = ref(true)
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')
const warning = ref('')
const chips = ref<Chip[]>([])
const activeChip = ref('')
const toast = ref('')
let toastTimer: number | undefined
let requestSeq = 0

/* ---- 封面失败警示条（covers.ts 诊断驱动；修「全部首字占位但看不到报错」） ---- */
const COVER_FAIL_THRESHOLD = 5
/** '' | 'missing-ffmpeg' | 'error' */
const coverBanner = ref('')
const coverFailCount = ref(0)
/** 本页会话内关闭后不再弹（组件卸载重挂视为新会话） */
let coverBannerDismissed = false
let unsubCoverDiag: (() => void) | undefined

function applyCoverDiag(d: CoverDiagnostics) {
  coverFailCount.value = d.failCount
  if (coverBannerDismissed) return
  if (d.failCount < COVER_FAIL_THRESHOLD) {
    coverBanner.value = ''
    return
  }
  // 缺 ffmpeg 只要出现过就优先引导（可操作），否则泛化报错
  coverBanner.value = (d.kindCounts['missing-ffmpeg'] ?? 0) > 0 ? 'missing-ffmpeg' : 'error'
}

async function copyCoverDiagnostics() {
  const text = coverDiagnosticText()
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const app = (window as any).go?.main?.App
    if (app?.ClipboardWrite) {
      try {
        await app.ClipboardWrite(text)
      } catch (e: any) {
        showToast(`复制失败：${e?.message ?? e}`)
        return
      }
    } else {
      showToast('复制失败：剪贴板不可用')
      return
    }
  }
  showToast(t('covers.banner.copied'))
}

function dismissCoverBanner() {
  coverBannerDismissed = true
  coverBanner.value = ''
}

const FALLBACK_CHIPS: Chip[] = [
  { id: '', name: '全部' },
  { id: 'short_play', name: '真人剧' },
  { id: 'comic_series', name: '漫剧' },
  { id: 'ai_series', name: 'AI剧' }
]

/* ---- hero 大卡（v1 browse_panel.py:388-394：首个分类且 items 非空 → items[0] 上墙剔除、其余进网格） ----
   - 仅首个分类（chips[0]，兜底即「全部」）出 hero，与 v1 _is_first_tab 同口径；
     分类页签切走后 v1 也不出 hero
   - 条数 <2 不出：只有 1 条时 v1 的墙是空的（items[1:]），这里把这一条留作普通卡片，
     避免「一张大卡压一片空墙」
   - 去重：网格剔掉 hero 本体——按 id 且按标题剔（翻页同推荐位会重复同标题，
     v1 真机反馈过「同一部剧出现两次」正是没剔的后果）
   - 矮窗隐藏：v1 HERO_MIN_H=140 以下让位给墙；这里等价口径 = 网格可视高 < hero 260 + 一行 200 */
const HERO_H = 260
const HERO_MIN_ROW = 200
const gridWrap = ref<HTMLElement | null>(null)
const heroFits = ref(true)

const isFirstChip = computed(() => activeChip.value === (chips.value[0]?.id ?? ''))
const heroDrama = computed<Drama | null>(() => {
  if (!heroFits.value || !isFirstChip.value) return null
  if (items.value.length < 2) return null
  return items.value[0]
})
const gridItems = computed<Drama[]>(() => {
  const hero = heroDrama.value
  if (!hero) return items.value
  return items.value.filter((d) => d.id !== hero.id && d.title !== hero.title)
})

function measureHeroFits() {
  const el = gridWrap.value
  if (!el) return
  const h = el.clientHeight
  if (h > 0) heroFits.value = h >= HERO_H + HERO_MIN_ROW
}
// gridWrap 在 v-else 分支里，出现/重建时重新挂观察（RO 首次 observe 即回调，初值不用另测）
let gridRo: ResizeObserver | null = null
watch(gridWrap, (el) => {
  gridRo?.disconnect()
  gridRo = el ? new ResizeObserver(() => measureHeroFits()) : null
  if (el && gridRo) gridRo.observe(el)
  measureHeroFits()
})

/* ---- mock 验证钩子（沿用 covers.ts:131-136 __zgjCoverFault 的 window 惯例，仅 MOCK_MODE 挂载） ----
   - __zgjDiscover.refreshes：force 刷新次数（下拉手势断言）
   - __zgjDiscoverInjectCatalog='empty'|'single'：注入式改写 catalog 结果，测空/单条不崩。
     注入态下 page>1 一律返空并停翻页——否则「墙没填满 → 自动补下一页」会把单条边界刷成
     多页稳态，验证脚本读到的就不是它要断言的那个状态了。 */
interface DiscoverHook {
  refreshes: number
  lastForce: number
}
function discoverHook(): DiscoverHook {
  const w = window as unknown as { __zgjDiscover?: DiscoverHook }
  if (!w.__zgjDiscover) w.__zgjDiscover = { refreshes: 0, lastForce: 0 }
  return w.__zgjDiscover
}
function injectCatalog(list: Drama[], page: number): Drama[] {
  if (!MOCK_MODE) return list
  const mode = (window as unknown as { __zgjDiscoverInjectCatalog?: string }).__zgjDiscoverInjectCatalog
  if (!mode) return list
  if (page > 1) return []
  if (mode === 'empty') return []
  if (mode === 'single') return list.slice(0, 1)
  return list
}

/* 下拉刷新（v1 browse_panel.py:151 set_refresh_enabled + refreshRequested → _refresh 的手势版）：
   手势在 PosterGrid 内判定，这里只接事件复用既有强刷新入口 loadFirst(true) */
function onPullRefresh() {
  loadFirst(true)
}

function selectDrama(d: Drama) {
  router.push(detailRoute(d))
}

async function loadChips(source: string) {
  try {
    const res = await api.browseFilters(source)
    const cat = res.groups?.find((g) => g.key === 'category')
    if (cat?.options?.length) {
      chips.value = [{ id: '', name: '全部' }, ...cat.options.map((o) => ({ id: o.id, name: o.name }))]
      return
    }
    throw new ApiError('无 category 组', 'empty_filters')
  } catch (e: any) {
    if (e?.code !== 'discovery_unavailable') console.warn('browseFilters 兜底:', e?.message ?? e)
    chips.value = FALLBACK_CHIPS
  }
}

async function loadFirst(force = false) {
  const source = store.currentSlug
  if (!source) return
  if (force && MOCK_MODE) {
    const hook = discoverHook()
    hook.refreshes += 1
    hook.lastForce = Date.now()
  }
  const seq = ++requestSeq
  loading.value = true
  error.value = ''
  warning.value = ''
  hasMore.value = true
  try {
    const res = await api.catalog({
      source,
      category: activeChip.value,
      page: 1,
      lane: 'discover',
      force
    })
    if (seq !== requestSeq) return
    items.value = injectCatalog(res.items ?? [], 1)
    page.value = 1
    hasMore.value = !!res.hasMore
    warning.value = res.warning || (MOCK_MODE ? 'VITE_MOCK 示例数据，非真实引擎结果' : DEGRADED ? '原生桥不可用，当前显示本机缓存数据' : '')
    if (!items.value.length && !res.warning) error.value = ''
  } catch (e: any) {
    if (seq !== requestSeq) return
    items.value = []
    error.value = e?.message ?? String(e)
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

async function loadMore() {
  const source = store.currentSlug
  if (!source || loading.value || loadingMore.value || !hasMore.value || error.value) return
  const seq = requestSeq
  loadingMore.value = true
  try {
    const res = await api.catalog({
      source,
      category: activeChip.value,
      page: page.value + 1,
      lane: 'discover'
    })
    if (seq !== requestSeq) return
    // 真实引擎 id 在翻页会重复（同推荐位），页内按 id#page 去重即可
    const more = injectCatalog(res.items ?? [], page.value + 1)
    items.value = items.value.concat(more)
    page.value += 1
    hasMore.value = !!res.hasMore && !!more.length
  } catch (e: any) {
    if (seq !== requestSeq) return
    // 翻页失败：停 auto-load，保留已加载内容 + toast
    loadingMore.value = false
    showToast(`加载失败：${e?.message ?? e}`)
    hasMore.value = false
    return
  }
  loadingMore.value = false
}

function showToast(msg: string) {
  toast.value = msg
  clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => (toast.value = ''), 3200)
}

function pickChip(c: Chip) {
  activeChip.value = c.id
  loadFirst(false)
}

watch(
  () => store.currentSlug,
  async (s) => {
    if (!s) return
    activeChip.value = ''
    await loadChips(s)
    await loadFirst(false)
  }
)

onMounted(async () => {
  unsubCoverDiag = onCoverDiagnostics(applyCoverDiag)
  applyCoverDiag(coverDiagnostics()) // 先吃历史累计（如搜索页已失败的）
  await store.loadSources()
  if (store.currentSlug) {
    await loadChips(store.currentSlug)
    await loadFirst(false)
  }
})

onBeforeUnmount(() => {
  unsubCoverDiag?.()
  unsubCoverDiag = undefined
  gridRo?.disconnect()
  gridRo = null
})
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">找剧</h1>
    <SourceSwitch :model-value="store.currentSlug" :sources="store.orderedSources" :loading="store.sourcesLoading" @update:model-value="store.setSource($event)" />
    <div class="spacer" />
    <button class="btn" :disabled="loading" @click="loadFirst(true)">
      <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M21 12a9 9 0 1 1-2.64-6.36M21 3v6h-6" />
      </svg>
      刷新
    </button>
  </div>

  <div v-if="warning" class="banner">
    <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
      <path d="M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
    </svg>
    {{ warning }}
  </div>

  <div v-if="coverBanner" class="banner" :class="{ warn: coverBanner === 'missing-ffmpeg' }" data-testid="cover-banner">
    <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
      <path d="M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
    </svg>
    <span class="cover-banner-text">{{
      coverBanner === 'missing-ffmpeg'
        ? t('covers.banner.missingFfmpeg')
        : tf('covers.banner.partialN', { n: coverFailCount })
    }}</span>
    <button v-if="coverBanner === 'error'" class="btn banner-copy" data-testid="cover-banner-copy" @click="copyCoverDiagnostics">
      {{ t('covers.banner.copy') }}
    </button>
    <button class="btn banner-close" :aria-label="t('covers.banner.dismiss')" data-testid="cover-banner-close" @click="dismissCoverBanner">
      ×
    </button>
  </div>

  <div class="chips">
    <button
      v-for="c in chips"
      :key="c.id"
      class="chip"
      :class="{ active: activeChip === c.id }"
      @click="pickChip(c)"
    >
      {{ c.name }}
    </button>
  </div>

  <div v-if="store.sourcesError" class="state-block">
    <div>源列表获取失败：{{ store.sourcesError }}</div>
    <button class="btn" @click="store.loadSources(true)">重试</button>
  </div>
  <div v-else-if="error && !loading" class="state-block">
    <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
      <circle cx="12" cy="12" r="9" /><path d="M12 8v5m0 3.5.01" />
    </svg>
    <div>{{ error }}</div>
    <button class="btn" @click="loadFirst(true)">重试</button>
  </div>
  <div v-else ref="gridWrap" class="grid-wrap">
    <PosterGrid
      :items="gridItems"
      :loading="loading"
      :loading-more="loadingMore"
      refreshable
      :show-hero="!!heroDrama"
      @load-more="loadMore"
      @refresh="onPullRefresh"
      @select="selectDrama"
    >
      <template v-if="heroDrama" #hero>
        <HeroCard :drama="heroDrama" @select="selectDrama" />
      </template>
    </PosterGrid>
  </div>

  <div v-if="toast" class="toast">{{ toast }}</div>
</template>

<style scoped>
.spacer {
  flex: 1;
}
.cover-banner-text {
  flex: 1;
  min-width: 0;
  line-height: 1.5;
}
.banner-close {
  margin-left: auto;
  flex-shrink: 0;
  padding: 2px 10px;
  font-size: 15px;
  line-height: 1.4;
}
.banner-copy {
  flex-shrink: 0;
  padding: 2px 10px;
  font-size: 12px;
}
.chips {
  display: flex;
  gap: 8px;
  padding: 6px 24px 10px;
  flex-wrap: wrap;
}
.state-block {
  flex: 1;
}
/* 网格包装：给 .pg-scroller 一个确定高度的父级，同时是 hero「矮窗隐藏」的测量对象 */
.grid-wrap {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.toast {
  position: fixed;
  bottom: 22px;
  left: 50%;
  transform: translateX(-50%);
  background: var(--guo-player-scrim);
  color: var(--text);
  padding: 10px 18px;
  border-radius: 999px;
  font-size: 13px;
  box-shadow: 0 6px 24px var(--guo-shadow);
  z-index: 60;
}
</style>
