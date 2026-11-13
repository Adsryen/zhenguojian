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
   - V2-E 榜单侧栏（右侧 320px）：移植 v1 main.py:1095-1283 board_box/RankList 区——
     候选来自 App.RankingBoards（rankingBoards action，rankings 应答本身无 boards 键；
     桥接失败回退常量表 provider_rankings.go:23-37），分页加载 + seen 去重 +
     连续 2 空页停止 + 切榜 token 作废在途（v1 _rank_token），更新 hint 取「·」前
     首段（v1 _sync_rank_info），点行进详情与推荐流同路（detailRoute）。
   - 文案统一走 i18n t()/tf()。 */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { api, DEGRADED, MOCK_MODE } from '@/services/api'
import { ApiError, type CatalogResult, type Drama, type RankingBoard, type RankingItem } from '@/services/types'
import { detailRoute } from '@/services/dramaRef'
import { resolveCover } from '@/services/covers'
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
    // 红果筛选面板体裁在 key=genre 组；category 组为其他站源的分类（交集过滤保底）
    const group = res.groups?.find((g) => g.key === 'genre') ?? res.groups?.find((g) => g.key === 'category')
    // genre 组只取体裁行（主题/时间等维度行 key 不同，不会误入）
    const opts = (group?.options ?? []).filter((o) => GENRE_IDS.has(o.id))
    if (opts.length) {
      chips.value = [{ id: '', name: t('library.genre.all') }, ...opts.map((o) => ({ id: o.id, name: o.name || o.id }))]
      return
    }
    throw new ApiError('browseFilters: no genre/category group', 'empty_filters')
  } catch (e: any) {
    console.info('browseFilters 兜底:', e?.code === 'discovery_unavailable' ? '构建通道缺 overlay' : e?.message ?? e)
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

/* ---------------- V2-E 榜单侧栏（v1 main.py:1095-1283 board_box/RankList 语义） ---------------- */

// 候选兜底常量表：照抄 guoapp-src/native/core/provider_rankings.go:23-37。
// 正常路径用 App.RankingBoards（核心已按站源可用性过滤，app_runtime.go:457-462），
// 桥接失败/返回空时回退本表（与 v1 board_box 同源常量）。
const RANK_BOARDS_FALLBACK: RankingBoard[] = [
  { id: 'hongguo-hot', source: 'hongguo', name: '总热播榜' },
  { id: 'hongguo-real', source: 'hongguo', name: '真人剧榜' },
  { id: 'hongguo-comic', source: 'hongguo', name: '漫剧榜' },
  { id: 'hongguo-ai', source: 'hongguo', name: 'AI剧榜' },
  { id: 'huangdou-all', source: 'huangdou', name: '总榜' },
  { id: 'huangdou-mogai', source: 'huangdou', name: '魔改榜' },
  { id: 'huangdou-search', source: 'huangdou', name: '搜索榜' },
  { id: 'huangdou-favorite', source: 'huangdou', name: '收藏榜' },
  { id: 'huangdou-finish', source: 'huangdou', name: '完结榜' },
  { id: 'huangguo-hot', source: 'huangguo_ai', name: '热播榜' },
  { id: 'huangguo-recommend', source: 'huangguo_ai', name: '推荐榜' },
  { id: 'huangguo-potential', source: 'huangguo_ai', name: '潜力榜' }
]

const rankBoards = ref<RankingBoard[]>([])
const rankBoard = ref('')
const rankRows = ref<RankingItem[]>([])
const rankLoading = ref(false)
const rankLoadingMore = ref(false)
const rankError = ref('')
const rankNote = ref('') // 空榜/warning 提示（v1 rank_info 行）
const rankUpdated = ref('') // 更新文案首段（v1 _sync_rank_info：「·」前）
const rankCoverSrc = ref(new Map<string, string>())
const rankPage = ref(0)
const rankTotalPages = ref(0)
const rankHasMore = ref(false)
const rankRetryPage = ref(0)
const narrow = ref(false)
const rankOpen = ref(false)

const rankKnownIds = new Set<string>()
const rankCoverDone = new Set<string>()
let rankEmptyPages = 0
let rankToken = 0

const narrowMq = window.matchMedia('(max-width: 1100px)')
function syncNarrow(): void {
  narrow.value = narrowMq.matches
  if (!narrowMq.matches) rankOpen.value = false // 回宽屏收起抽屉
}

const rankStatusText = computed(() => {
  if (rankLoading.value) return t('library.rank.loading')
  if (rankLoadingMore.value) return t('library.rank.loadingMore')
  if (rankError.value) return t('library.rank.loadFailed') // v1：加载失败，点击重试
  if (!rankRows.value.length) return rankNote.value
  return `${tf('library.loadedCount', { n: rankRows.value.length })} · ${rankHasMore.value ? t('library.scrollMore') : t('library.allLoaded')}`
})

const rankCanLoadMore = computed(
  () => !rankLoading.value && !rankLoadingMore.value && !rankError.value && rankHasMore.value && rankRows.value.length > 0
)

function rankNoClass(rank: number): string {
  return rank <= 3 ? `rank-${rank}` : 'rank-other'
}

function rankSub(it: RankingItem): string {
  if (it.metric) return it.metric
  return [it.drama.category, it.drama.heat].filter(Boolean).join(' · ')
}

function fallbackChar(d: Drama): string {
  return d.title.trim().charAt(0) || '剧'
}

function markRankBroken(id: string): void {
  const m = new Map(rankCoverSrc.value)
  m.set(id, '') // 置空 → 模板回落文字占位（PosterGrid 同款）
  rankCoverSrc.value = m
}

async function ensureRankCovers(list: RankingItem[]): Promise<void> {
  const fresh = list.filter((it) => it?.drama?.id && !rankCoverDone.has(it.drama.id))
  for (const it of fresh) rankCoverDone.add(it.drama.id)
  if (!fresh.length) return
  const urls = await Promise.all(fresh.map((it) => resolveCover(it.drama)))
  const m = new Map(rankCoverSrc.value)
  fresh.forEach((it, i) => {
    if (urls[i]) m.set(it.drama.id, urls[i])
  })
  rankCoverSrc.value = m
}

async function loadRankBoards(): Promise<void> {
  if (rankBoards.value.length) return
  try {
    const list = await api.rankingBoards()
    rankBoards.value = list.length ? list : RANK_BOARDS_FALLBACK
  } catch (e: any) {
    console.warn('rankingBoards 回退常量表:', e?.message ?? e)
    rankBoards.value = RANK_BOARDS_FALLBACK
  }
  if (!rankBoard.value) {
    rankBoard.value = rankBoards.value[0]?.id ?? ''
    if (rankBoard.value) void loadRankings(1)
  }
}

function pickBoard(id: string): void {
  if (rankBoard.value === id) return
  rankBoard.value = id
  void loadRankings(1) // v1 board_box currentIndexChanged → _load_rankings(1)
}

// v1 _load_rankings 同款：token 作废在途；page==1 清表；page>1 拒并发/越界页；
// 应答 seen 去重；hasMore && (!totalPages || page<totalPages)；连续 2 空页停。
async function loadRankings(page = 1): Promise<void> {
  if (!rankBoard.value) return
  page = Math.max(1, page | 0)
  if (page > 1 && (rankLoading.value || rankLoadingMore.value)) return
  if (page > 1 && rankTotalPages.value && page > rankTotalPages.value) return
  const token = ++rankToken
  rankError.value = ''
  rankNote.value = ''
  if (page === 1) {
    rankRows.value = []
    rankKnownIds.clear()
    rankPage.value = 0
    rankTotalPages.value = 0
    rankHasMore.value = false
    rankEmptyPages = 0
    rankUpdated.value = ''
  }
  if (page > 1) rankLoadingMore.value = true
  else rankLoading.value = true
  try {
    const res = await api.rankings(rankBoard.value, page, false)
    if (token !== rankToken) return
    const merged = page > 1 ? rankRows.value.slice() : []
    const fresh: RankingItem[] = []
    for (const it of res.items ?? []) {
      // v1 rank_list.append_rows：无 id/rank 的行直接丢弃，seen 按 drama.id 去重（rank_list.py:283-311）
      const id = it?.drama?.id
      if (!id || typeof it.rank !== 'number' || rankKnownIds.has(id)) continue
      rankKnownIds.add(id)
      merged.push(it)
      fresh.push(it)
    }
    rankRows.value = merged
    rankPage.value = page
    rankTotalPages.value = Number(res.totalPages || 0)
    rankHasMore.value = !!res.hasMore && (!rankTotalPages.value || page < rankTotalPages.value)
    rankEmptyPages = fresh.length === 0 ? rankEmptyPages + 1 : 0
    if (rankEmptyPages >= 2) rankHasMore.value = false
    const ut = String(res.updatedText || '')
    if (ut) rankUpdated.value = (ut.split('·')[0] || '').trim()
    if (!rankRows.value.length) rankNote.value = res.warning || t('library.rank.empty')
    void ensureRankCovers(fresh)
  } catch (e: any) {
    if (token !== rankToken) return
    rankError.value = e?.message ?? String(e)
    rankRetryPage.value = page // v1 _rank_retry_page
  } finally {
    if (token === rankToken) {
      rankLoading.value = false
      rankLoadingMore.value = false
    }
  }
}

function retryRankings(): void {
  void loadRankings(rankRetryPage.value || 1)
}

function openRankDetail(d: Drama): void {
  router.push(detailRoute(d)) // 与推荐流点击同路径（query 带 compactDrama）
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
  syncNarrow()
  narrowMq.addEventListener('change', syncNarrow)
  void loadRankBoards() // 侧栏与推荐流并行加载，互不阻塞
  await store.loadSources()
  if (store.currentSlug) {
    await loadChips(store.currentSlug)
    await loadFirst(false)
  }
})

onUnmounted(() => {
  narrowMq.removeEventListener('change', syncNarrow)
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
    <button v-if="narrow" class="btn" data-testid="rank-toggle" @click="rankOpen = !rankOpen">
      {{ rankOpen ? t('library.rank.hide') : t('library.rank.show') }}
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

  <div class="lib-body">
    <section class="lib-feed">
      <div v-if="store.sourcesError" class="state-block">
        <div>{{ t('library.sourcesFail') }}{{ store.sourcesError }}</div>
        <button class="btn" @click="store.loadSources(true)">{{ t('search.retry') }}</button>
      </div>
      <div v-else-if="error && !loading" class="state-block" data-testid="lib-error">
        <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
          <circle cx="12" cy="12" r="9" /><path d="M12 8v5m0 3.5 0 0.01" />
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
    </section>

    <aside v-if="!narrow || rankOpen" class="rank-side" :class="{ drawer: narrow }" data-testid="rank-side">
      <div class="rank-head">
        <span class="rank-title" data-testid="rank-title">{{ t('library.rank.title') }}</span>
        <span class="spacer" />
        <button v-if="narrow" class="btn" data-testid="rank-close" @click="rankOpen = false">
          {{ t('library.rank.hide') }}
        </button>
      </div>
      <div v-if="rankBoards.length" class="rank-chips" data-testid="rank-chips">
        <button
          v-for="b in rankBoards"
          :key="b.id"
          class="chip"
          :class="{ active: rankBoard === b.id }"
          :data-testid="`rank-board-${b.id}`"
          :title="b.description || b.id"
          @click="pickBoard(b.id)"
        >
          {{ b.name || b.id }}
        </button>
      </div>
      <div class="rank-scroll" data-testid="rank-scroll">
        <div
          v-for="it in rankRows"
          :key="it.drama.id"
          class="rank-row"
          :data-testid="`rank-row-${it.rank}`"
          :data-rank="it.rank"
          role="button"
          tabindex="0"
          @click="openRankDetail(it.drama)"
          @keydown.enter="openRankDetail(it.drama)"
        >
          <span class="rank-no" :class="rankNoClass(it.rank)">{{ it.rank }}</span>
          <span class="rank-cover">
            <img
              v-if="rankCoverSrc.get(it.drama.id)"
              :src="rankCoverSrc.get(it.drama.id)"
              :alt="it.drama.title"
              loading="lazy"
              @error="markRankBroken(it.drama.id)"
            />
            <span v-else class="rank-cover-fb">{{ fallbackChar(it.drama) }}</span>
          </span>
          <span class="rank-info">
            <span class="rank-name">{{ it.drama.title }}</span>
            <span class="rank-metric">{{ rankSub(it) }}</span>
          </span>
        </div>
        <div v-if="rankLoading && !rankRows.length" class="rank-hint" data-testid="rank-loading">
          {{ t('library.rank.loading') }}
        </div>
        <div v-else-if="!rankLoading && !rankRows.length && rankNote" class="rank-hint" data-testid="rank-empty">
          {{ rankNote }}
        </div>
      </div>
      <div v-if="rankUpdated" class="rank-updated" data-testid="rank-updated">
        {{ tf('library.rank.updated', { t: rankUpdated }) }}
      </div>
      <div class="rank-foot">
        <span class="rank-status" data-testid="rank-status">{{ rankStatusText }}</span>
        <button v-if="rankError" class="btn" data-testid="rank-retry" @click="retryRankings">{{ t('library.retryLoad') }}</button>
        <button v-else-if="rankCanLoadMore" class="btn" data-testid="rank-more" @click="loadRankings(rankPage + 1)">
          {{ t('library.loadMore') }}
        </button>
      </div>
    </aside>
  </div>

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
  color: var(--muted);
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
  color: var(--muted);
}
/* ---- V2-E 榜单侧栏 ---- */
.lib-body {
  flex: 1;
  display: flex;
  min-height: 0;
}
.lib-feed {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.lib-feed :deep(.pg-scroller) {
  height: auto;
  flex: 1;
  min-height: 0;
}
.rank-side {
  width: 320px;
  flex-shrink: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  border-left: 1px solid var(--border, #2b2b36);
}
.rank-side.drawer {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  z-index: 30;
  background: var(--surface, #141821);
  box-shadow: -12px 0 28px rgb(0 0 0 / 35%);
}
.rank-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 14px 2px;
}
.rank-title {
  font-size: 14px;
  font-weight: 600;
}
.rank-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 8px 14px 10px;
}
.rank-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}
.rank-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 14px;
  cursor: pointer;
}
.rank-row:hover {
  background: var(--surface-3, #24242e);
}
.rank-no {
  width: 26px;
  flex-shrink: 0;
  text-align: center;
  font-size: 16px;
  font-weight: 700;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
/* 名次高亮色照 v1 rank_list.py:102-110 DESIGN 表 */
.rank-no.rank-1 {
  color: #ff7a45;
  font-size: 18px;
}
.rank-no.rank-2 {
  color: #ffc04d;
  font-size: 18px;
}
.rank-no.rank-3 {
  color: #5ec2ff;
  font-size: 18px;
}
.rank-cover {
  width: 60px;
  height: 84px;
  flex-shrink: 0;
  border-radius: 6px;
  overflow: hidden;
  background: var(--surface-2, #171b23);
  display: flex;
  align-items: center;
  justify-content: center;
}
.rank-cover img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.rank-cover-fb {
  font-size: 20px;
  color: var(--muted);
}
.rank-info {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.rank-name {
  font-size: 13px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.rank-metric {
  font-size: 12px;
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.rank-hint {
  padding: 14px;
  font-size: 12px;
  color: var(--muted);
  text-align: center;
}
.rank-updated {
  padding: 0 14px 2px;
  font-size: 12px;
  color: var(--muted);
  flex-shrink: 0;
}
.rank-foot {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 14px 12px;
  flex-shrink: 0;
}
.rank-status {
  flex: 1;
  font-size: 12px;
  color: var(--muted);
}
</style>
