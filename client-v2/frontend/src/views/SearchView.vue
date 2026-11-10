<script setup lang="ts">
/* Search —— 搜索：输入 + 「全部来源」勾选 + 最近搜索(localStorage 每源≤10) + 结果
   单源（默认）：catalog source=当前源 lane=search（同 lane 新搜索自动取消旧搜索，v1 语义）。
   全部来源：orderedSources 里 enabled && implemented 的源并行扇出（worker 池并发≤4，
   各源 page1，lane=search:<slug> 分源互不取消），结果按源分组流式渲染；
   单源失败只落该组失败行，全组皆败才出整体 error 条。勾选态持久化 zgj.search.all。 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { api } from '@/services/api'
import type { Drama } from '@/services/types'
import { detailRoute } from '@/services/dramaRef'
import { t } from '@/services/i18n'
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
  query.value = term
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
    <h1 class="page-title">搜索</h1>
    <SourceSwitch :model-value="store.currentSlug" :sources="store.orderedSources" :loading="store.sourcesLoading" @update:model-value="store.setSource($event)" />
  </div>

  <div class="search-bar">
    <div class="input-wrap">
      <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
        <circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3" />
      </svg>
      <input
        v-model="query"
        placeholder="搜索剧名 / 标签，回车开始"
        maxlength="64"
        @keyup.enter="doSearch()"
      />
    </div>
    <button class="btn primary" :disabled="loading || !query.trim()" @click="doSearch()">
      {{ loading ? '搜索中…' : '搜索' }}
    </button>
    <button v-if="searched" class="btn" @click="clearSearch">清除</button>
    <label class="all-src" :title="t('search.allHint')">
      <input v-model="searchAll" type="checkbox" class="all-src-box" data-testid="search-all" />
      <span>{{ t('search.all') }}</span>
    </label>
  </div>

  <div v-if="!searched && recents.length" class="recents">
    <span class="rec-label">最近搜索</span>
    <button v-for="r in recents" :key="r" class="chip" @click="doSearch(r)">{{ r }}</button>
    <button class="link-btn" @click="clearRecents">清空</button>
  </div>
  <div v-else-if="!searched" class="state-block hint">
    <svg viewBox="0 0 24 24" width="40" height="40" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
      <circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3" />
    </svg>
    <div>{{ searchAll ? t('search.hintAll') : `输入关键词搜索当前源（${store.current?.displayName || store.current?.name || '未选源'}）` }}</div>
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
    <div>没有找到与「{{ active }}」相关的剧</div>
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
