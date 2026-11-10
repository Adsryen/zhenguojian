<script setup lang="ts">
/* Library —— 剧库推荐（V2-2）：语义基线 = v1 recommendation_panel.py + core_client.py
   - 「全部」tab：catalog(source, lane="library") 分页加载更多；页码壳层自持、严格递增、
     成功后才 +1（v1 core_client.py:287-299 —— 「重发当前页是下一页看起来坏掉的原因」；
     真实核心 session 游标会吞并重发低页号：重发 page1 返回的是新 current 页，看似重复实为错位）。
   - 题材 tab：Recommendations(category, force, more) action —— 核心返回「累计合并表」
     （app_recommendations.go:105-122），故 more 必须按 known_ids 去重后再上屏；
     连续 2 空页停止（v1 recommendation_panel.py:123-125）；more 失败进入 retryPending，
     未重试成功前不再自动翻页（v1 :154-155）。
   - 刷新/首载先 RecommendationPageReset()（v1 reset_recommendation_page：generation++、页码归零，
     不对应核心 action）再 force 重载。
   - 切题材 = load(force=True) + 清列表（v1 main.py:1190-1205）。
   - 未移植 replace/「换一批」（v1 该分支仅 main.py:1193 按钮触发，本页无此入口）。
   文案暂硬编码中文（i18n.ts 由并行代理锁定，待收敛）。 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { api, DEGRADED, MOCK_MODE } from '@/services/api'
import { ApiError, type CatalogResult, type Drama } from '@/services/types'
import { detailRoute } from '@/services/dramaRef'
import { t, tf } from '@/services/i18n'
import SourceSwitch from '@/components/SourceSwitch.vue'
import PosterGrid from '@/components/PosterGrid.vue'

interface Chip {
  id: string
  name: string
}

const store = useAppStore()
const router = useRouter()

const items = ref<Drama[]>([])
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')
const warning = ref('')
const chips = ref<Chip[]>([])
const activeChip = ref('')
const hasMore = ref(true)
const serverMore = ref(true) // 连续 2 空页规则（与核心 hasMore 分离）
const retryPending = ref('') // 非空 = v1 retry_request：more 失败待重试

const knownIds = new Set<string>()
let allPage = 0 // 「全部」tab 壳层页码游标，仅成功后 +1
let emptyPages = 0
let requestSeq = 0
let retrySnapshot: { force: boolean; more: boolean } | null = null

// v1 main.py:44 同款表；真实题材名以 browseFilters 为准，静态表兜底
// 展示走 i18n 键（genreLabel），name 字段仅作兜底数据，不再直接渲染
const GENRES: Array<{ id: string; name: string }> = [
  { id: '', name: t('library.genre.all') },
  { id: 'short_play', name: t('library.genre.short_play') },
  { id: 'comic_series', name: t('library.genre.comic_series') },
  { id: 'ai_series', name: t('library.genre.ai_series') }
]
// 核心 recommendations 仅认这 3 个 genre（provider_hongguo_recommendations.go:25-31），交集防呆
const GENRE_IDS = new Set(['short_play', 'comic_series', 'ai_series'])
const GENRE_LABEL_KEY: Record<string, string> = {
  short_play: 'library.genre.short_play',
  comic_series: 'library.genre.comic_series',
  ai_series: 'library.genre.ai_series'
}

/** 题材 chip 展示名：已知 genre 走 i18n 键（切语言即时更新），其余用核心返回名兜底 */
function genreLabel(c: Chip): string {
  if (!c.id) return t('library.genre.all')
  const key = GENRE_LABEL_KEY[c.id]
  return key ? t(key) : c.name || c.id
}

/** 横幅兜底文案响应式（res.warning 为数据侧内容，原样展示） */
const warningText = computed(
  () => warning.value || (MOCK_MODE ? t('banner.mock') : DEGRADED ? t('banner.degraded') : '')
)

/** 底部状态行：随 locale/items/retryPending 响应，替代原 updateStatus 手动刷 */
const statusText = computed(() => {
  if (retryPending.value) return tf('library.loadFailedMsg', { m: retryPending.value })
  return items.value.length
    ? `${tf('library.loadedCount', { n: items.value.length })} · ${hasMore.value && serverMore.value ? t('library.scrollMore') : t('library.allLoaded')}`
    : ''
})

const canLoadMore = computed(() => !loading.value && !loadingMore.value && hasMore.value && serverMore.value && !retryPending.value && !error.value && items.value.length > 0)

async function loadChips(source: string) {
  try {
    const res = await api.browseFilters(source)
    const cat = res.groups?.find((g) => g.key === 'category')
    const opts = (cat?.options ?? []).filter((o) => GENRE_IDS.has(o.id))
    if (opts.length) {
      chips.value = [{ id: '', name: t('library.genre.all') }, ...opts.map((o) => ({ id: o.id, name: o.name || o.id }))]
      return
    }
    throw new ApiError('browseFilters: no category group', 'empty_filters')
  } catch (e: any) {
    if (e?.code !== 'discovery_unavailable') console.warn('browseFilters 兜底:', e?.message ?? e)
    chips.value = GENRES
  }
}

function applyWarning(res: CatalogResult) {
  warning.value = res.warning || ''
}

function takeResult(res: CatalogResult, more: boolean) {
  const list = res.items ?? []
  if (!more) {
    // 首屏/刷新：全量替换（v1 recommendation_panel.py：!more 时 seen 为空集）
    knownIds.clear()
    items.value = list
    for (const d of list) knownIds.add(d.id)
    emptyPages = 0
  } else {
    let freshCount = 0
    const merged = items.value.slice()
    for (const d of list) {
      if (knownIds.has(d.id)) continue // 累计合并表：必须按 known_ids 去重
      knownIds.add(d.id)
      merged.push(d)
      freshCount++
    }
    items.value = merged
    // v1 recommendation_panel.py:123-125：连续 2 空页（more 但 0 新增）→ 停止翻页
    emptyPages = freshCount === 0 ? emptyPages + 1 : 0
    if (emptyPages >= 2) serverMore.value = false
  }
  hasMore.value = !!res.hasMore
}

async function fetchOne(more: boolean, force: boolean): Promise<CatalogResult> {
  if (activeChip.value) {
    return api.recommendations(activeChip.value, force, more)
  }
  const source = store.currentSlug || 'hongguo'
  const page = more ? allPage + 1 : 1
  const res = await api.catalog({ source, category: '', page, lane: 'library', force })
  if (more) allPage = page // 成功后才提交页码；失败不 +1（不重发同一页）
  return res
}

async function loadFirst(force = false) {
  const seq = ++requestSeq
  loading.value = true
  loadingMore.value = false
  error.value = ''
  warning.value = ''
  retryPending.value = ''
  retrySnapshot = null
  hasMore.value = true
  serverMore.value = true
  emptyPages = 0
  try {
    // v1 panel load(!more)：先 reset_recommendation_page（generation++ + 页码归零）；失败忽略
    try {
      await api.recommendationPageReset()
    } catch {
      /* 纯壳层操作，忽略 */
    }
    if (seq !== requestSeq) return
    allPage = 0
    const res = await fetchOne(false, force)
    if (seq !== requestSeq) return
    takeResult(res, false)
    if (activeChip.value === '' && res.page) allPage = res.page // 真实核心 session 游标可能已前进，以其为准
    applyWarning(res)
  } catch (e: any) {
    if (seq !== requestSeq) return
    items.value = []
    knownIds.clear()
    error.value = e?.message ?? String(e)
  } finally {
    if (seq === requestSeq) {
      loading.value = false
    }
  }
}

async function loadMore() {
  if (loading.value || loadingMore.value || !hasMore.value || !serverMore.value || error.value) return
  if (retryPending.value) return // v1 :154-155：有待重试时不再自动翻页
  const seq = requestSeq
  loadingMore.value = true
  try {
    const res = await fetchOne(true, false)
    if (seq !== requestSeq) return
    takeResult(res, true)
  } catch (e: any) {
    if (seq !== requestSeq) return
    // v1 retry_request：保留已加载内容，挂起「重试加载」，禁止后续自动翻页
    retryPending.value = e?.message ?? String(e)
    retrySnapshot = { force: false, more: true }
  } finally {
    if (seq === requestSeq) loadingMore.value = false
  }
}

async function retryLoad() {
  if (!retrySnapshot) return
  const snap = retrySnapshot
  retrySnapshot = null
  if (snap.more) await loadMore()
  else await loadFirst(snap.force)
}

function pickChip(c: Chip) {
  if (activeChip.value === c.id) return
  activeChip.value = c.id
  // v1 main.py:1190-1205：切 tab → 清列表 + load(force=True)
  items.value = []
  knownIds.clear()
  loadFirst(true)
}

watch(
  () => store.currentSlug,
  async (s) => {
    if (!s) return
    activeChip.value = ''
    items.value = []
    knownIds.clear()
    await loadChips(s)
    await loadFirst(false)
  }
)

onMounted(async () => {
  await store.loadSources()
  if (store.currentSlug) {
    await loadChips(store.currentSlug)
    await loadFirst(false)
  }
})
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">{{ t('library.title') }}</h1>
    <SourceSwitch
      :model-value="store.currentSlug"
      :sources="store.orderedSources"
      :loading="store.sourcesLoading"
      @update:model-value="store.setSource($event)"
    />
    <div class="spacer" />
    <button class="btn" :disabled="loading" data-testid="lib-refresh" @click="loadFirst(true)">
      <svg
        viewBox="0 0 24 24"
        width="14"
        height="14"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      >
        <path d="M21 12a9 9 0 1 1-2.64-6.36M21 3v6h-6" />
      </svg>
      {{ t('library.refresh') }}
    </button>
  </div>

  <div v-if="warningText" class="banner">
    <svg
      viewBox="0 0 24 24"
      width="15"
      height="15"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
    >
      <path d="M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
    </svg>
    {{ warningText }}
  </div>

  <div class="chips">
    <button
      v-for="c in chips"
      :key="c.id"
      class="chip"
      :class="{ active: activeChip === c.id }"
      :data-testid="`lib-chip-${c.id || 'all'}`"
      @click="pickChip(c)"
    >
      {{ genreLabel(c) }}
    </button>
  </div>

  <div v-if="store.sourcesError" class="state-block">
    <div>{{ t('library.sourcesFail') }}{{ store.sourcesError }}</div>
    <button class="btn" @click="store.loadSources(true)">{{ t('search.retry') }}</button>
  </div>
  <div v-else-if="error && !loading" class="state-block" data-testid="lib-error">
    <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
      <circle cx="12" cy="12" r="9" /><path d="M12 8v5m0 3.5.01" />
    </svg>
    <div>{{ error }}</div>
    <button class="btn" @click="loadFirst(true)">{{ t('search.retry') }}</button>
  </div>
  <div v-else-if="!loading && !items.length" class="state-block" data-testid="lib-empty">
    <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
      <circle cx="12" cy="12" r="9" /><path d="M8.5 12h7" />
    </svg>
    <div>{{ warningText || t('library.empty') }}</div>
    <button class="btn" @click="loadFirst(true)">{{ t('search.retry') }}</button>
  </div>
  <template v-else>
    <div v-if="retryPending" class="banner warn" data-testid="lib-retry">
      {{ tf('library.loadFailedMsg', { m: retryPending }) }}
      <button class="btn banner-btn" @click="retryLoad">{{ t('library.retryLoad') }}</button>
    </div>
    <div v-if="statusText" class="lib-status" data-testid="lib-status">{{ statusText }}</div>
    <PosterGrid
      :items="items"
      :loading="loading"
      :loading-more="loadingMore"
      @load-more="loadMore"
      @select="(d: Drama) => router.push(detailRoute(d))"
    />
    <div class="lib-foot">
      <button v-if="canLoadMore" class="btn" data-testid="lib-more" @click="loadMore">{{ t('library.loadMore') }}</button>
      <span v-else-if="!loading && items.length && (hasMore ? !serverMore : true)" class="lib-foot-hint">{{ t('library.allRecommendLoaded') }}</span>
    </div>
  </template>

</template>

<style scoped>
.spacer {
  flex: 1;
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
.banner-btn {
  margin-left: auto;
  flex-shrink: 0;
  padding: 2px 12px;
  font-size: 13px;
}
.lib-status {
  padding: 0 24px 6px;
  font-size: 12px;
  color: var(--text-muted, #8a8f98);
}
.lib-foot {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 10px;
  padding: 8px 24px 16px;
  flex-shrink: 0;
}
.lib-foot-hint {
  font-size: 12px;
  color: var(--text-muted, #8a8f98);
}
</style>
