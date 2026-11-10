<script setup lang="ts">
/* Discover —— 找剧：SourceSwitch + 分类 chips + 虚拟滚动海报墙（catalog 分页，lane=discover）
   分类来源：browseFilters(groups[key=category])；默认构建无 overlay → 兜底 hongguo 三分类
   （值取自 guoapp-src core/provider_hongguo_catalog.go hongguoAppGenres）。 */
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { api, DEGRADED, MOCK_MODE } from '@/services/api'
import { ApiError, type Drama } from '@/services/types'
import { detailRoute } from '@/services/dramaRef'
import { t } from '@/services/i18n'
import { coverDiagnostics, onCoverDiagnostics, type CoverDiagnostics } from '@/services/covers'
import SourceSwitch from '@/components/SourceSwitch.vue'
import PosterGrid from '@/components/PosterGrid.vue'

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
/** 本页会话内关闭后不再弹（组件卸载重挂视为新会话） */
let coverBannerDismissed = false
let unsubCoverDiag: (() => void) | undefined

function applyCoverDiag(d: CoverDiagnostics) {
  if (coverBannerDismissed) return
  coverBanner.value =
    d.failCount >= COVER_FAIL_THRESHOLD ? (d.lastKind === 'missing-ffmpeg' ? 'missing-ffmpeg' : 'error') : ''
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
    items.value = res.items ?? []
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
    items.value = items.value.concat(res.items ?? [])
    page.value += 1
    hasMore.value = !!res.hasMore
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
      coverBanner === 'missing-ffmpeg' ? t('covers.banner.missingFfmpeg') : t('covers.banner.partial')
    }}</span>
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
  <PosterGrid
    v-else
    :items="items"
    :loading="loading"
    :loading-more="loadingMore"
    @load-more="loadMore"
    @select="(d) => router.push(detailRoute(d))"
  />

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
.chips {
  display: flex;
  gap: 8px;
  padding: 6px 24px 10px;
  flex-wrap: wrap;
}
.state-block {
  flex: 1;
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
