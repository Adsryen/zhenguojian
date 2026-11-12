<script setup lang="ts">
/* Search —— 搜索：输入 + 「全部来源」勾选 + 最近搜索(localStorage 每源≤10) + 结果
   单源（默认）：catalog source=当前源 lane=search（同 lane 新搜索自动取消旧搜索，v1 语义）。
   全部来源：orderedSources 里 enabled && implemented 的源并行扇出（worker 池并发≤4，
   各源 page1，lane=search:<slug> 分源互不取消），结果按源分组流式渲染；
   单源失败只落该组失败行，全组皆败才出整体 error 条。勾选态持久化 zgj.search.all。 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { api } from '@/services/api'
import type { Drama } from '@/services/types'
import { detailRoute } from '@/services/dramaRef'
import { t, tf } from '@/services/i18n'
import SourceSwitch from '@/components/SourceSwitch.vue'
import PosterGrid from '@/components/PosterGrid.vue'
import SearchGroups from '@/components/SearchGroups.vue'
import {
  fetchGroup,
  fanoutTargets,
  loadAllPref,
  makeGroups,
  runFanout,
  saveAllPref,
  type SearchGroup
} from '@/stores/searchAll'

const store = useAppStore()
const router = useRouter()

const query = ref('')
const active = ref('')
const items = ref<Drama[]>([])
const page = ref(0)
const hasMore = ref(false)
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')
const searched = ref(false)
let requestSeq = 0

/* ── 全部来源（扇出）态 ── */
const searchAll = ref(loadAllPref())
const groups = ref<SearchGroup[]>([])
const allGroupsFailed = computed(
  () => groups.value.length > 0 && groups.value.every((g) => g.status === 'error')
)

const recents = ref<string[]>([])
const recentKey = computed(() => `zgj.recent.${store.currentSlug}`)

function loadRecents() {
  try {
    recents.value = JSON.parse(localStorage.getItem(recentKey.value) || '[]').slice(0, 10)
  } catch {
    recents.value = []
  }
}
function pushRecent(q: string) {
  const list = [q, ...recents.value.filter((x) => x !== q)].slice(0, 10)
  recents.value = list
  localStorage.setItem(recentKey.value, JSON.stringify(list))
}
function clearRecents() {
  recents.value = []
  localStorage.removeItem(recentKey.value)
}

/* ── 搜索联想词（子代理 G；v1 main.py:1361 _on_search_typed 语义照搬）──
   门控：query 非空 &&（当前源=红果 || 「全部来源」扇出勾选——扇出含红果，联想同源可用）。
   其它源/空输入：收起面板，「最近搜索」接管（v1 退化路径同款，互斥显示联想优先）。
   300ms 防抖 + token 丢过期应答（v1 _suggest_token）；↑↓EnterEsc + 鼠标点击；
   选中回填输入框后走 doSearch → 最近搜索由既有逻辑记录一次，不双记。 ── */
const suggestWrap = ref<HTMLElement | null>(null)
const suggestWords = ref<string[]>([])
const suggestOpen = ref(false)
const suggestIndex = ref(-1)
const suggestLoading = ref(false)
let suggestTimer: ReturnType<typeof setTimeout> | undefined
let suggestToken = 0
let suppressQueryWatch = false // doSearch/选中回填的输入框赋值不重复拉联想（防搜后面板复闪）
let suggestEnterPicked = false // keydown 已消费 Enter（选中联想词）：keyup.enter 不再触发起常规搜索

const suggestable = computed(() => store.currentSlug === 'hongguo' || searchAll.value)

function closeSuggest() {
  // 收起 = 作废：清在途防抖 + 递增 token 丢弃已发出的过期应答（否则 Enter 搜索/Esc 后旧请求会复闪面板）
  clearTimeout(suggestTimer)
  suggestToken++
  suggestOpen.value = false
  suggestIndex.value = -1
  suggestWords.value = []
  suggestLoading.value = false
}

function scheduleSuggest(term: string) {
  clearTimeout(suggestTimer)
  const token = ++suggestToken
  if (!term || !suggestable.value) {
    closeSuggest()
    return
  }
  suggestTimer = setTimeout(() => {
    suggestLoading.value = true
    api
      .suggestions(term)
      .then((words) => {
        if (token !== suggestToken) return // v1：上一次输入的联想词，丢掉
        suggestWords.value = words
        suggestOpen.value = words.length > 0 // 空结果/核心异常收起面板回落最近搜索
        suggestIndex.value = -1
      })
      .catch(() => {
        if (token === suggestToken) closeSuggest()
      })
      .finally(() => {
        if (token === suggestToken) suggestLoading.value = false
      })
  }, 300)
}

watch(query, (v) => {
  if (suppressQueryWatch) {
    suppressQueryWatch = false
    return
  }
  scheduleSuggest(v.trim().slice(0, 64))
})

watch(suggestable, (ok) => {
  // 切源/勾「全部来源」：门控变化即重估（关联想→收起；开联想且有词→补拉）
  clearTimeout(suggestTimer)
  if (!ok) closeSuggest()
  else if (query.value.trim()) scheduleSuggest(query.value.trim().slice(0, 64))
})

function onSuggestFocus() {
  const term = query.value.trim().slice(0, 64)
  if (!term || !suggestable.value) return
  if (suggestWords.value.length) suggestOpen.value = true
  else scheduleSuggest(term)
}

function pickSuggest(word: string) {
  if (!word) return
  clearTimeout(suggestTimer)
  suggestToken++
  if (query.value !== word) {
    suppressQueryWatch = true // 同词赋值不会触发 watcher，避免标志位残留吞掉下一次真实输入
    query.value = word // v1 _on_suggestion：回填输入再搜索，recents 走 doSearch 既有记录一次
  }
  closeSuggest()
  void doSearch(word)
}

function onSuggestKeydown(e: KeyboardEvent) {
  if (e.isComposing || e.keyCode === 229) return // IME 候选中不劫持按键
  if (!suggestOpen.value || !suggestWords.value.length) return
  const n = suggestWords.value.length
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    suggestIndex.value = suggestIndex.value < 0 ? 0 : (suggestIndex.value + 1) % n
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    suggestIndex.value = suggestIndex.value < 0 ? n - 1 : (suggestIndex.value - 1 + n) % n
  } else if (e.key === 'Enter') {
    if (suggestIndex.value >= 0) {
      e.preventDefault()
      suggestEnterPicked = true
      pickSuggest(suggestWords.value[suggestIndex.value])
    }
  } else if (e.key === 'Escape') {
    e.preventDefault()
    closeSuggest()
  }
}

function onSearchEnter() {
  if (suggestEnterPicked) {
    suggestEnterPicked = false
    return
  }
  closeSuggest()
  void doSearch()
}

function onDocClick(e: MouseEvent) {
  if (!suggestOpen.value) return
  if (suggestWrap.value && !suggestWrap.value.contains(e.target as Node)) closeSuggest()
}
onMounted(() => document.addEventListener('click', onDocClick, true))
onBeforeUnmount(() => {
  document.removeEventListener('click', onDocClick, true)
  clearTimeout(suggestTimer)
  suggestToken++
})

async function doSearchAll(term: string, seq: number) {
  // 刷新后立即点搜索：站源表可能还没回来，先等 loadSources（loadSources 自带并发去重/缓存）
  if (!store.sources.length) await store.loadSources()
  if (seq !== requestSeq) return
  const targets = fanoutTargets(store.orderedSources)
  if (!targets.length) {
    groups.value = []
    if (seq === requestSeq) error.value = t('search.noSources')
    return
  }
  groups.value = makeGroups(targets)
  await runFanout(groups.value, term, () => seq !== requestSeq)
}

async function doSearch(q?: string) {
  const term = (q ?? query.value).trim().slice(0, 64)
  if (!term) return
  if (query.value !== term) {
    // 程序化回填（联想选中/最近搜索/切源重搜）不重复触发联想拉取
    suppressQueryWatch = true
    query.value = term
  }
  active.value = term
  pushRecent(term)
  const seq = ++requestSeq
  loading.value = true
  error.value = ''
  searched.value = true
  items.value = []
  if (searchAll.value) {
    await doSearchAll(term, seq)
    if (seq !== requestSeq) return
    loading.value = false
    return
  }
  groups.value = []
  hasMore.value = true
  try {
    const res = await api.catalog({ source: store.currentSlug, query: term, page: 1, lane: 'search' })
    if (seq !== requestSeq) return
    items.value = res.items ?? []
    page.value = 1
    hasMore.value = !!res.hasMore
  } catch (e: any) {
    if (seq !== requestSeq) return
    error.value = e?.message ?? String(e)
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

async function loadMore() {
  if (!active.value || loading.value || loadingMore.value || !hasMore.value || error.value) return
  const seq = requestSeq
  loadingMore.value = true
  try {
    const res = await api.catalog({ source: store.currentSlug, query: active.value, page: page.value + 1, lane: 'search' })
    if (seq !== requestSeq) return
    items.value = items.value.concat(res.items ?? [])
    page.value += 1
    hasMore.value = !!res.hasMore
  } catch {
    hasMore.value = false
  } finally {
    loadingMore.value = false
  }
}

/* 失败组的单源重试：仅动这一组，其他组结果保留 */
async function retryGroup(slug: string) {
  const g = groups.value.find((x) => x.slug === slug)
  if (!g || !active.value || loading.value) return
  const seq = requestSeq
  await fetchGroup(g, active.value, () => seq !== requestSeq)
}

function clearSearch() {
  searched.value = false
  items.value = []
  groups.value = []
  active.value = ''
  error.value = ''
}

watch(searchAll, (on) => {
  saveAllPref(on)
  if (!on) {
    // 取消勾选：清空分组态；有在搜的词则回到单源结果
    groups.value = []
    if (searched.value && active.value) void doSearch(active.value)
  }
})

watch(() => store.currentSlug, () => {
  loadRecents()
  // 扇出模式覆盖全部源，切当前源不影响分组结果，不重搜
  if (active.value && !searchAll.value) void doSearch(active.value)
})

onMounted(async () => {
  await store.loadSources()
  loadRecents()
})
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">{{ t('search.title') }}</h1>
    <SourceSwitch :model-value="store.currentSlug" :sources="store.orderedSources" :loading="store.sourcesLoading" @update:model-value="store.setSource($event)" />
  </div>

  <div class="search-bar">
    <div ref="suggestWrap" class="input-wrap suggest-wrap">
      <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
        <circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3" />
      </svg>
      <input
        v-model="query"
        :placeholder="t('search.placeholder')"
        maxlength="64"
        data-testid="search-input"
        autocomplete="off"
        @focus="onSuggestFocus"
        @keydown="onSuggestKeydown"
        @keyup.enter="onSearchEnter"
      />
      <!-- 联想下拉（v1 hongguo suggestions 对齐）：与「最近搜索」互斥，联想优先 -->
      <div
        v-if="suggestOpen"
        class="sg-panel card"
        role="listbox"
        :aria-label="t('search.suggest.aria')"
        data-testid="suggest-panel"
      >
        <button
          v-for="(w, i) in suggestWords"
          :key="w"
          class="sg-item"
          :class="{ active: i === suggestIndex }"
          role="option"
          :aria-selected="i === suggestIndex"
          data-testid="suggest-item"
          @mouseenter="suggestIndex = i"
          @mousedown.prevent="pickSuggest(w)"
        >{{ w }}</button>
        <div class="sg-foot" data-testid="suggest-hint">{{ t('search.suggest.hint') }}</div>
      </div>
    </div>
    <button class="btn primary" :disabled="loading || !query.trim()" @click="doSearch()">
      {{ loading ? t('search.groupLoading') : t('search.button') }}
    </button>
    <button v-if="searched" class="btn" @click="clearSearch">{{ t('search.clear') }}</button>
    <label class="all-src" :title="t('search.allHint')">
      <input v-model="searchAll" type="checkbox" class="all-src-box" data-testid="search-all" />
      <span>{{ t('search.all') }}</span>
    </label>
  </div>

  <div v-if="!searched && recents.length && !suggestOpen" class="recents" data-testid="recents">
    <span class="rec-label">{{ t('search.recents') }}</span>
    <button v-for="r in recents" :key="r" class="chip" @click="doSearch(r)">{{ r }}</button>
    <button class="link-btn" @click="clearRecents">{{ t('search.clearAll') }}</button>
  </div>
  <div v-else-if="!searched && !suggestOpen" class="state-block hint">
    <svg viewBox="0 0 24 24" width="40" height="40" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
      <circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3" />
    </svg>
    <div>{{ searchAll ? t('search.hintAll') : tf('search.hintSource', { name: store.current?.displayName || store.current?.name || t('search.noSourceName') }) }}</div>
  </div>

  <div v-if="searched && error" class="state-block">
    <div>{{ t('search.failed') }}{{ error }}</div>
    <button class="btn" @click="doSearch(active)">{{ t('search.retry') }}</button>
  </div>
  <SearchGroups
    v-else-if="searched && searchAll && groups.length"
    :groups="groups"
    :all-failed="allGroupsFailed && !loading"
    @retry="retryGroup"
    @retry-all="doSearch(active)"
    @select="(d) => router.push(detailRoute(d))"
  />
  <div v-else-if="searched && !loading && !items.length" class="state-block">
    <div>{{ tf('search.noResult', { q: active }) }}</div>
  </div>
  <PosterGrid
    v-else-if="searched"
    :items="items"
    :loading="loading"
    :loading-more="loadingMore"
    @load-more="loadMore"
    @select="(d) => router.push(detailRoute(d))"
  />
</template>

<style scoped>
.search-bar {
  display: flex;
  gap: 10px;
  padding: 4px 24px 10px;
}
.input-wrap {
  flex: 1;
  max-width: 460px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 12px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  color: var(--muted);
}
.input-wrap:focus-within {
  border-color: var(--accent);
}
.input-wrap input {
  flex: 1;
  border: none;
  outline: none;
  background: transparent;
  color: var(--text);
  font-size: 13.5px;
  padding: 9px 0;
}
.all-src {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  align-self: center;
  font-size: 13px;
  color: var(--text);
  cursor: pointer;
  user-select: none;
  white-space: nowrap;
}
.all-src-box {
  width: 14px;
  height: 14px;
  accent-color: var(--accent);
  cursor: pointer;
}
.recents {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 4px 24px 8px;
}
/* 联想面板：观感对齐 SourceSwitch .ss-panel（card + 6px 内衬 + 同款 hover/active） */
.suggest-wrap {
  position: relative;
}
.sg-panel {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  min-width: 100%;
  max-height: 306px;
  overflow-y: auto;
  z-index: 30;
  padding: 6px;
  display: flex;
  flex-direction: column;
  gap: 2px;
  box-shadow: 0 8px 24px var(--guo-shadow);
}
.sg-item {
  display: block;
  padding: 7px 10px;
  border: none;
  border-radius: var(--guo-radius);
  background: transparent;
  color: var(--text);
  cursor: pointer;
  font-size: 13px;
  text-align: left;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.sg-item:hover {
  background: var(--guo-chip-hover);
}
.sg-item.active {
  background: var(--accent-soft);
}
.sg-foot {
  margin-top: 2px;
  padding: 5px 10px 2px;
  font-size: 11px;
  color: var(--muted-2);
  border-top: 1px solid var(--border);
  white-space: nowrap;
}
.rec-label {
  font-size: 12px;
  color: var(--muted-2);
}
.link-btn {
  border: none;
  background: none;
  color: var(--accent);
  cursor: pointer;
  font-size: 12px;
}
.hint {
  flex: 1;
}
</style>
