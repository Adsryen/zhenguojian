<script setup lang="ts">
/* Search —— 搜索：输入 + 按钮 + 最近搜索(localStorage 每源≤10) + 结果 PosterGrid
   走 catalog source=当前源 query=… lane=search（同 lane 新搜索自动取消旧搜索，v1 语义）。 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { api } from '@/services/api'
import type { Drama } from '@/services/types'
import { detailRoute } from '@/services/dramaRef'
import SourceSwitch from '@/components/SourceSwitch.vue'
import PosterGrid from '@/components/PosterGrid.vue'

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

function clearSearch() {
  searched.value = false
  items.value = []
  active.value = ''
  error.value = ''
}

watch(() => store.currentSlug, () => {
  loadRecents()
  if (active.value) doSearch(active.value)
})

onMounted(async () => {
  await store.loadSources()
  loadRecents()
})
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">搜索</h1>
    <SourceSwitch :model-value="store.currentSlug" :sources="store.sources" :loading="store.sourcesLoading" @update:model-value="store.setSource($event)" />
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
    <div>输入关键词搜索当前源（{{ store.current?.displayName || store.current?.name || '未选源' }}）</div>
  </div>

  <div v-if="searched && error" class="state-block">
    <div>搜索失败：{{ error }}</div>
    <button class="btn" @click="doSearch(active)">重试</button>
  </div>
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
