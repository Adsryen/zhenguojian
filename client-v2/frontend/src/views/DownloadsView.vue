<script setup lang="ts">
/* DownloadsView —— /downloads：下载与合并（V2-3）。
   数据源：useDownloadsStore —— 真实模式走 Go downloadsvc（事件 downloads:update + 兜底轮询），
   VITE_MOCK 走 fixtures.ts 有状态模拟（页面标注「示例数据」）。
   交互对齐 v1 downloads.py：行操作按状态（排队/下载中→暂停·移除；暂停→续传·移除；
   失败→重试·移除；完成→播放·归档·移除），全部暂停/全部开始，并发槽位设置；
   合并入口 = 整季（按剧分组，已完成≥2）或勾选已完成分集合并；合并队列/归档分页。
   添加下载：搜索 → 选剧 → detail 分集多选 → 入队（核心 enqueueDownloads）。
   ——未尽：DetailView 的 localStorage 暂存队列缺 drama.source，无法在此转正式任务（见终报依赖请求）。 */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api, MOCK_MODE } from '@/services/api'
import type { Chapter, Drama, DownloadEntry, DownloadJobRow } from '@/services/types'
import { chapterEpisode, playerRoute } from '@/services/dramaRef'
import { useDownloadsStore } from '@/stores/downloads'

type Tab = 'queue' | 'merges' | 'archive'

const router = useRouter()
const store = useDownloadsStore()

const tab = ref<Tab>('queue')
const toast = ref('')
let toastTimer: number | undefined

function showToast(msg: string) {
  toast.value = msg
  clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => (toast.value = ''), 3200)
}

watch(
  () => store.lastError,
  (err) => {
    if (err) showToast(err)
  }
)

onMounted(() => {
  store.attach()
  void store.loadSettings()
})
onUnmounted(() => {
  clearTimeout(toastTimer)
  store.detach()
})

/* ---------------------------------------------------------------- 展示辅助 */

const STATE_TEXT: Record<string, string> = {
  queued: '排队中',
  downloading: '下载中',
  paused: '已暂停',
  completed: '已完成',
  failed: '失败',
  removing: '删除中'
}

function stateText(job: DownloadJobRow): string {
  return STATE_TEXT[job.state] ?? job.state
}

const MERGE_STATE_TEXT: Record<string, string> = {
  queued: '排队中',
  running: '合并中',
  done: '已完成',
  failed: '失败',
  canceled: '已取消'
}

function mergeStateText(state: string): string {
  return MERGE_STATE_TEXT[state] ?? state
}

function epNo(job: DownloadJobRow): number {
  return (job.index ?? 0) + 1
}

function epLabel(job: DownloadJobRow): string {
  const c = job.chapter?.title
  return c && !/^第\s*\d+\s*集$/.test(c) ? `第${epNo(job)}集 · ${c}` : `第${epNo(job)}集`
}

function titleOf(job: DownloadJobRow): string {
  return job.drama?.title || '未知剧集'
}

function fmtBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i += 1
  }
  return `${i === 0 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`
}

function fmtSpeed(n?: number): string {
  if (!n || n <= 0) return ''
  return `${fmtBytes(n)}/s`
}

function fmtEta(sec?: number): string {
  if (!sec || sec <= 0 || !Number.isFinite(sec)) return ''
  const s = Math.ceil(sec)
  if (s < 60) return `剩余 ${s}s`
  if (s < 3600) return `剩余 ${Math.floor(s / 60)}m${String(s % 60).padStart(2, '0')}s`
  return `剩余 ${Math.floor(s / 3600)}h${String(Math.floor((s % 3600) / 60)).padStart(2, '0')}m`
}

function pctOf(job: DownloadJobRow): number {
  const p = job.progress ?? (job.total > 0 ? job.bytes / job.total : 0)
  return Math.max(0, Math.min(100, Math.round(p * 100)))
}

function sizeText(job: DownloadJobRow): string {
  if (job.state === 'completed') return fmtBytes(job.total)
  if (job.total > 0) return `${fmtBytes(job.bytes)} / ${fmtBytes(job.total)}`
  return job.bytes > 0 ? fmtBytes(job.bytes) : ''
}

/* ---------------------------------------------------------------- 行操作（v1 ROW_ACTIONS 对齐） */

function goPlay(job: DownloadJobRow) {
  const d: Drama = job.drama ?? { id: '', source: '', title: '' }
  if (!d.id) {
    showToast('任务缺少剧集信息，无法播放')
    return
  }
  router.push(playerRoute(d, epNo(job), 0, job.chapter ?? undefined))
}

/* 勾选：仅「已完成且未归档」可选（合并/批量删除共用） */
const selected = ref<Set<string>>(new Set())

function toggleSelect(job: DownloadJobRow) {
  const next = new Set(selected.value)
  if (next.has(job.id)) next.delete(job.id)
  else next.add(job.id)
  selected.value = next
}

watch(
  () => store.jobs,
  (jobs) => {
    const alive = new Set(jobs.map((j) => j.id))
    const next = new Set([...selected.value].filter((id) => alive.has(id)))
    if (next.size !== selected.value.size) selected.value = next
  }
)

const selectedJobs = computed(() => store.jobs.filter((j) => selected.value.has(j.id)))
const selectedCompletable = computed(() => selectedJobs.value.every((j) => j.state === 'completed' && !j.archived))
const selectedSingleDrama = computed(() => {
  const ids = new Set(selectedJobs.value.map((j) => j.drama?.id ?? ''))
  return ids.size === 1 ? selectedJobs.value[0].drama : null
})

/* 整季合并入口：每部剧已完成≥2（v1 _merge_targets 语义） */

async function onMergeSeason(drama: Drama) {
  const res = await store.mergeSeason(drama)
  if (res) showToast(`已加入合并队列：${drama.title}`)
}

async function onMergeSelected() {
  const res = await store.mergeSelected([...selected.value])
  if (res) showToast('已加入合并队列')
}

async function onRemoveBatch() {
  const ids = [...selected.value]
  const res = await store.removeBatch(ids)
  if (!res) return
  const fails = Object.keys(res.failures ?? {}).length
  showToast(fails ? `删除 ${res.completed.length} 个，失败 ${fails} 个` : `已删除 ${res.completed.length} 个任务`)
  selected.value = new Set()
}

/* ---------------------------------------------------------------- 设置面板 */

const showSettings = ref(false)
const conc = ref(2)
const bySource = ref(false)
const verify = ref(true)
const savingSettings = ref(false)

watch(
  () => [store.settings?.downloadConcurrency, store.settings?.downloadBySource, store.mergeVerify] as const,
  ([c, b, v]) => {
    if (typeof c === 'number') conc.value = c
    if (typeof b === 'boolean') bySource.value = b
    if (typeof v === 'boolean') verify.value = v
  },
  { immediate: true }
)

async function onSaveSettings() {
  savingSettings.value = true
  const ok = await store.saveSettings(conc.value, bySource.value, verify.value)
  savingSettings.value = false
  if (ok) showToast('下载设置已保存')
}

/* ---------------------------------------------------------------- 添加下载（搜索→详情→多选→入队） */

const showAdd = ref(false)
const addQuery = ref('')
const addSource = ref('hongguo')
const searching = ref(false)
const searchResults = ref<Drama[]>([])
const pickedDrama = ref<Drama | null>(null)
const chapters = ref<Chapter[]>([])
const chapterErr = ref('')
const pickedChapters = ref<Set<number>>(new Set())
const quality = ref(2)
const enqueuing = ref(false)

const QUALITY_OPTIONS = [
  { value: 0, label: '省流' },
  { value: 1, label: '高清' },
  { value: 2, label: '超清' }
]

function resetAddPanel() {
  showAdd.value = false
  addQuery.value = ''
  searchResults.value = []
  pickedDrama.value = null
  chapters.value = []
  chapterErr.value = ''
  pickedChapters.value = new Set()
}

async function onSearch() {
  const q = addQuery.value.trim()
  if (!q) return
  searching.value = true
  try {
    const res = await api.catalog({ source: addSource.value, query: q, page: 1 })
    searchResults.value = res?.items ?? []
    if (!searchResults.value.length) showToast('没有找到匹配的剧集')
  } catch (e) {
    showToast(`搜索失败：${(e as Error).message}`)
  } finally {
    searching.value = false
  }
}

async function onPick(d: Drama) {
  pickedDrama.value = d
  chapters.value = []
  chapterErr.value = ''
  pickedChapters.value = new Set()
  try {
    const res = await api.detail(d)
    const list = Array.isArray(res?.chapters) ? res.chapters : []
    chapters.value = list
    if (!list.length) chapterErr.value = '该剧暂无可下载分集'
  } catch (e) {
    chapterErr.value = `分集加载失败：${(e as Error).message}`
  }
}

function toggleChapter(i: number) {
  const next = new Set(pickedChapters.value)
  if (next.has(i)) next.delete(i)
  else next.add(i)
  pickedChapters.value = next
}

function pickAllChapters() {
  pickedChapters.value =
    pickedChapters.value.size === chapters.value.length
      ? new Set()
      : new Set(chapters.value.map((_, i) => i))
}

async function onEnqueue() {
  const drama = pickedDrama.value
  if (!drama || !pickedChapters.value.size || enqueuing.value) return
  const entries: DownloadEntry[] = [...pickedChapters.value]
    .sort((a, b) => a - b)
    .map((i) => ({ chapter: chapters.value[i], index: i }))
  enqueuing.value = true
  try {
    const res = await api.downloadStart(drama, entries, quality.value)
    showToast(`已加入下载队列 ${res?.added ?? entries.length} 集`)
    resetAddPanel()
    tab.value = 'queue'
    await store.refresh()
  } catch (e) {
    showToast(`入队失败：${(e as Error).message}`)
  } finally {
    enqueuing.value = false
  }
}
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">下载与合并</h1>
    <span v-if="MOCK_MODE" class="chip static sample-chip">VITE_MOCK 示例数据</span>
    <div class="spacer" />
    <button class="btn" :disabled="!!store.busyId" @click="store.controlAll('pauseAll')">全部暂停</button>
    <button class="btn" :disabled="!!store.busyId" @click="store.controlAll('resumeAll')">全部开始</button>
    <button class="btn" :class="{ primary: showSettings }" @click="showSettings = !showSettings">并发设置</button>
    <button class="btn primary" :class="{ pressed: showAdd }" @click="showAdd ? resetAddPanel() : (showAdd = true)">
      {{ showAdd ? '收起添加' : '添加下载' }}
    </button>
  </div>

  <p v-if="store.status" class="status-line text-muted">{{ store.status }}</p>

  <!-- 并发设置（对齐核心 resourceSettings.downloadConcurrency，1..8） -->
  <div v-if="showSettings" class="card panel">
    <div class="panel-row">
      <label class="p-label">并发下载数</label>
      <input v-model.number="conc" class="num" type="number" min="1" max="8" step="1" />
      <span class="text-muted p-hint">同时下载的分集数（核心默认 2）</span>
    </div>
    <div class="panel-row">
      <label class="p-check"><input v-model="bySource" type="checkbox" />按来源并行（每来源各一组槽位）</label>
      <label class="p-check"><input v-model="verify" type="checkbox" />合并前完整解码校验（耗时但更稳）</label>
    </div>
    <div v-if="store.settings?.warning" class="p-warn text-muted">{{ store.settings.warning }}</div>
    <div class="panel-row">
      <button class="btn primary" :disabled="savingSettings" @click="onSaveSettings">
        {{ savingSettings ? '保存中…' : '保存设置' }}
      </button>
    </div>
  </div>

  <!-- 添加下载：搜索 → 选剧 → 分集多选 → 入队 -->
  <div v-if="showAdd" class="card panel">
    <div class="panel-row">
      <input v-model="addQuery" class="textin" placeholder="搜索剧名，回车搜索" @keyup.enter="onSearch" />
      <button class="btn" :disabled="searching || !addQuery.trim()" @click="onSearch">
        {{ searching ? '搜索中…' : '搜索' }}
      </button>
    </div>
    <div v-if="searchResults.length && !pickedDrama" class="result-list">
      <button v-for="d in searchResults" :key="d.id" class="result-item" @click="onPick(d)">
        <span class="r-title">{{ d.title }}</span>
        <span class="r-sub text-muted">{{ d.source }}{{ d.episodes ? ` · 全${d.episodes}集` : '' }}</span>
      </button>
    </div>
    <template v-if="pickedDrama">
      <div class="panel-row">
        <span class="picked-title">{{ pickedDrama.title }}</span>
        <button class="btn small" @click="pickAllChapters">
          {{ pickedChapters.size === chapters.length ? '取消全选' : '全选' }}
        </button>
        <span class="text-muted p-hint">已选 {{ pickedChapters.size }} 集</span>
        <label class="p-label q-label">清晰度</label>
        <select v-model.number="quality" class="sel">
          <option v-for="o in QUALITY_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
        </select>
      </div>
      <p v-if="chapterErr" class="text-muted">{{ chapterErr }}</p>
      <div v-else class="chapter-grid">
        <button
          v-for="(c, i) in chapters"
          :key="i"
          class="chapter"
          :class="{ on: pickedChapters.has(i) }"
          @click="toggleChapter(i)"
        >
          {{ chapterEpisode(c, i) }}
        </button>
      </div>
      <div class="panel-row">
        <button class="btn primary" :disabled="!pickedChapters.size || enqueuing" @click="onEnqueue">
          {{ enqueuing ? '入队中…' : '加入下载队列' }}
        </button>
        <button class="btn" @click="resetAddPanel">取消</button>
      </div>
    </template>
  </div>

  <!-- 分页 -->
  <div class="tabs">
    <button class="tab" :class="{ on: tab === 'queue' }" @click="tab = 'queue'">
      下载队列<em>{{ store.activeJobs.length }}</em>
    </button>
    <button class="tab" :class="{ on: tab === 'merges' }" @click="tab = 'merges'">
      合并队列<em>{{ store.merges.length }}</em>
    </button>
    <button class="tab" :class="{ on: tab === 'archive' }" @click="tab = 'archive'">
      归档<em>{{ store.archivedJobs.length }}</em>
    </button>
  </div>

  <!-- 下载队列 -->
  <template v-if="tab === 'queue'">
    <div v-if="store.mergeGroups.length || selectedJobs.length" class="merge-bar card">
      <span class="mb-label">合并入口</span>
      <button
        v-for="g in store.mergeGroups"
        :key="g.dramaId"
        class="chip"
        :disabled="!!store.busyId"
        :title="`已完成 ${g.jobs.length} 集，整季合并`"
        @click="onMergeSeason(g.drama)"
      >
        《{{ g.title }}》整季（{{ g.jobs.length }}集）
      </button>
      <button
        v-if="selectedJobs.length >= 2 && selectedCompletable && selectedSingleDrama"
        class="chip active"
        :disabled="!!store.busyId"
        @click="onMergeSelected"
      >
        合并所选（{{ selectedJobs.length }}集）
      </button>
      <button v-if="selectedJobs.length" class="chip danger-chip" :disabled="!!store.busyId" @click="onRemoveBatch">
        删除所选（{{ selectedJobs.length }}）
      </button>
      <span v-if="selectedJobs.length && !(selectedCompletable && selectedSingleDrama)" class="text-muted mb-hint">
        合并所选需同一部剧且全部完成
      </span>
    </div>

    <div v-if="!store.activeJobs.length" class="state-block">
      <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12m0 0l-4-4m4 4l4-4M4 19h16" /></svg>
      <div>下载队列为空。</div>
      <div class="text-muted hint">点右上角「添加下载」搜索剧集并选择分集。</div>
    </div>

    <div v-else class="card list">
      <div
        v-for="(job, i) in store.activeJobs"
        :key="job.id"
        class="row"
        :class="{ divided: i > 0, busy: store.busyId === job.id }"
      >
        <input
          v-if="job.state === 'completed' && !job.archived"
          class="row-check"
          type="checkbox"
          :checked="selected.has(job.id)"
          @change="toggleSelect(job)"
        />
        <span v-else class="row-check placeholder" />
        <div class="r-main">
          <div class="r-line1">
            <span class="r-title">{{ titleOf(job) }}</span>
            <span class="r-ep text-muted">{{ epLabel(job) }}</span>
            <span class="pill" :class="'pill-' + job.state">{{ stateText(job) }}</span>
          </div>
          <div class="r-line2">
            <span v-if="job.state === 'downloading'" class="bar"><i :style="{ width: pctOf(job) + '%' }" /></span>
            <span class="r-meta text-muted">{{ sizeText(job) }}</span>
            <span v-if="fmtSpeed(job.speed)" class="r-meta text-muted">{{ fmtSpeed(job.speed) }}</span>
            <span v-if="fmtEta(job.eta)" class="r-meta text-muted">{{ fmtEta(job.eta) }}</span>
            <span v-if="job.error" class="r-err" :title="job.error">{{ job.error }}</span>
          </div>
        </div>
        <div class="r-ops">
          <button v-if="job.state === 'completed'" class="btn small" @click="goPlay(job)">播放</button>
          <button
            v-if="job.state === 'downloading' || job.state === 'queued'"
            class="btn small"
            :disabled="!!store.busyId"
            @click="store.control(job, 'pause')"
          >
            暂停
          </button>
          <button
            v-if="job.state === 'paused'"
            class="btn small"
            :disabled="!!store.busyId"
            @click="store.control(job, 'resume')"
          >
            续传
          </button>
          <button
            v-if="job.state === 'failed'"
            class="btn small"
            :disabled="!!store.busyId"
            @click="store.retry(job)"
          >
            重试
          </button>
          <button
            v-if="job.state === 'completed'"
            class="btn small"
            :disabled="!!store.busyId"
            @click="store.control(job, 'archive')"
          >
            归档
          </button>
          <button
            class="btn small danger"
            :disabled="!!store.busyId || job.state === 'removing'"
            @click="store.control(job, 'remove')"
          >
            {{ job.state === 'removing' ? '删除中…' : '删除' }}
          </button>
        </div>
      </div>
    </div>
  </template>

  <!-- 合并队列 -->
  <template v-if="tab === 'merges'">
    <div v-if="store.merges.length" class="mb-bar">
      <button class="btn danger" :disabled="!!store.busyId" @click="store.mergeCancel('')">停止全部合并</button>
    </div>
    <div v-if="!store.merges.length" class="state-block">
      <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="6" width="18" height="12" rx="2" /><path d="M9 10l5 2-5 2z" /></svg>
      <div>暂无合并任务。</div>
      <div class="text-muted hint">在「下载队列」页勾选已完成分集，或点整季合并入口。</div>
    </div>
    <div v-else class="card list">
      <div v-for="(m, i) in store.merges" :key="m.id" class="row" :class="{ divided: i > 0 }">
        <div class="r-main">
          <div class="r-line1">
            <span class="r-title">{{ m.title }}</span>
            <span class="r-ep text-muted">全{{ String(m.episodes[0] ?? 0).padStart(3, '0') }}-{{ String(m.episodes[m.episodes.length - 1] ?? 0).padStart(3, '0') }}集（{{ m.count }}集）</span>
            <span class="pill" :class="'pill-' + m.state">{{ mergeStateText(m.state) }}</span>
          </div>
          <div class="r-line2">
            <span v-if="m.state === 'running'" class="bar"><i :style="{ width: Math.round(m.progress * 100) + '%' }" /></span>
            <span v-if="m.stage" class="r-meta text-muted">{{ m.stage }}</span>
            <span v-if="m.detail" class="r-err" :class="{ ok: m.state === 'done' }" :title="m.detail">{{ m.detail }}</span>
            <span v-if="m.state === 'done' && m.output" class="r-meta text-muted out-path" :title="m.output">{{ m.output }}</span>
          </div>
        </div>
        <div class="r-ops">
          <button
            v-if="m.state === 'running' || m.state === 'queued'"
            class="btn small danger"
            :disabled="!!store.busyId"
            @click="store.mergeCancel(m.id)"
          >
            取消
          </button>
        </div>
      </div>
    </div>
  </template>

  <!-- 归档 -->
  <template v-if="tab === 'archive'">
    <div v-if="!store.archivedJobs.length" class="state-block">
      <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7h18l-2 13H5L3 7zm3 0V5a2 2 0 012-2h8a2 2 0 012 2v2M9 12h6" /></svg>
      <div>暂无归档任务。</div>
      <div class="text-muted hint">已完成分集点「归档」后在这里找回。</div>
    </div>
    <div v-else class="card list">
      <div v-for="(job, i) in store.archivedJobs" :key="job.id" class="row" :class="{ divided: i > 0, busy: store.busyId === job.id }">
        <span class="row-check placeholder" />
        <div class="r-main">
          <div class="r-line1">
            <span class="r-title">{{ titleOf(job) }}</span>
            <span class="r-ep text-muted">{{ epLabel(job) }}</span>
            <span class="pill pill-completed">已完成·归档</span>
          </div>
        </div>
        <div class="r-ops">
          <button class="btn small" :disabled="!!store.busyId" @click="store.control(job, 'restore')">恢复</button>
          <button class="btn small danger" :disabled="!!store.busyId" @click="store.control(job, 'remove')">删除</button>
        </div>
      </div>
    </div>
  </template>

  <p class="footnote text-muted">
    任务与合并进度由 Go 下载服务每 {{ MOCK_MODE ? '1500' : '1500' }}ms 推送（downloads:update）；
    {{ MOCK_MODE ? '当前为 VITE_MOCK 示例数据：下载/合并均为本地模拟，非真实引擎结果。' : '合并流水线与 v1 media.MergeTask 对齐（解密→规范化→拼接→校验→成品）。' }}
  </p>

  <div v-if="toast" class="toast">{{ toast }}</div>
</template>

<style scoped>
.spacer {
  flex: 1;
}
.sample-chip {
  cursor: default;
  font-size: 12px;
}
.status-line {
  margin: 0 24px 6px;
  font-size: 12px;
}
.hint {
  font-size: 12px;
  max-width: 420px;
}
.footnote {
  margin: 14px 24px 24px;
  font-size: 12px;
}

/* 面板 */
.panel {
  margin: 0 24px 12px;
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.panel-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.p-label {
  font-size: 13px;
  color: var(--text);
}
.p-hint {
  font-size: 12px;
}
.p-check {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  cursor: pointer;
}
.p-warn {
  font-size: 12px;
}
.q-label {
  margin-left: 12px;
}
.num,
.sel,
.textin {
  background: var(--surface-2, var(--surface));
  border: 1px solid var(--border);
  border-radius: var(--radius);
  color: var(--text);
  padding: 6px 10px;
  font-size: 13px;
}
.textin {
  width: 260px;
}
.num {
  width: 72px;
}

/* 搜索结果 */
.result-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 220px;
  overflow: auto;
}
.result-item {
  display: flex;
  align-items: baseline;
  gap: 10px;
  text-align: left;
  background: none;
  border: 1px solid transparent;
  border-radius: var(--radius);
  color: var(--text);
  padding: 8px 10px;
  cursor: pointer;
}
.result-item:hover {
  border-color: var(--border);
  background: var(--chip-hover, var(--surface));
}
.r-title {
  font-size: 14px;
  font-weight: 600;
}
.r-sub {
  font-size: 12px;
}
.picked-title {
  font-size: 14px;
  font-weight: 600;
}

/* 分集多选 */
.chapter-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(44px, 1fr));
  gap: 6px;
  max-height: 240px;
  overflow: auto;
}
.chapter {
  padding: 7px 0;
  text-align: center;
  border-radius: var(--radius);
  border: 1px solid var(--border);
  background: none;
  color: var(--text);
  font-size: 13px;
  cursor: pointer;
}
.chapter:hover {
  border-color: var(--accent);
}
.chapter.on {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--on-accent, #fff);
}

/* 分页 */
.tabs {
  display: flex;
  gap: 8px;
  padding: 0 24px 10px;
}
.tab {
  padding: 6px 14px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: none;
  color: var(--muted);
  font-size: 13px;
  cursor: pointer;
}
.tab em {
  font-style: normal;
  margin-left: 6px;
  font-size: 11px;
  color: var(--muted);
}
.tab.on {
  background: var(--accent-soft, var(--surface));
  border-color: var(--accent);
  color: var(--accent);
}

/* 合并入口条 */
.merge-bar {
  margin: 0 24px 10px;
  padding: 10px 14px;
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.mb-label {
  font-size: 12px;
  color: var(--muted);
  margin-right: 4px;
}
.mb-hint {
  font-size: 12px;
}
.mb-bar {
  margin: 0 24px 10px;
}
.danger-chip {
  color: var(--danger);
  border-color: var(--danger);
}

/* 任务列表 */
.list {
  margin: 0 24px;
  padding: 2px 14px;
}
.row {
  display: flex;
  align-items: center;
  gap: 10px;
  position: relative;
  min-height: 54px;
}
.row.divided::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 1px;
  background: var(--border);
}
.row.busy {
  opacity: 0.55;
  pointer-events: none;
}
.row-check {
  flex-shrink: 0;
  width: 16px;
  cursor: pointer;
}
.row-check.placeholder {
  cursor: default;
}
.r-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 0;
}
.r-line1 {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.r-line1 .r-title {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.r-ep {
  font-size: 12px;
  white-space: nowrap;
}
.r-line2 {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}
.r-meta {
  font-size: 12px;
  white-space: nowrap;
}
.out-path {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.r-err {
  font-size: 12px;
  color: var(--danger);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 45%;
}
.r-err.ok {
  color: var(--muted);
}
.bar {
  display: inline-block;
  width: min(220px, 36%);
  height: 4px;
  border-radius: 2px;
  background: var(--surface-3, var(--border));
  overflow: hidden;
  flex-shrink: 0;
}
.bar i {
  display: block;
  height: 100%;
  background: var(--accent);
  border-radius: 2px;
}
.r-ops {
  display: flex;
  gap: 6px;
  flex-shrink: 0;
}
.btn.small {
  padding: 4px 10px;
  font-size: 12px;
}
.btn.danger {
  color: var(--danger);
  border-color: var(--danger);
}
.btn.pressed {
  border-color: var(--accent);
}

/* 状态 pill */
.pill {
  padding: 2px 9px;
  border-radius: 999px;
  font-size: 11px;
  white-space: nowrap;
  border: 1px solid var(--border);
  color: var(--muted);
}
.pill-downloading {
  color: var(--accent);
  border-color: var(--accent);
}
.pill-queued {
  color: var(--muted);
}
.pill-paused {
  color: var(--warning, #d29922);
  border-color: var(--warning, #d29922);
}
.pill-completed,
.pill-done {
  color: var(--success, #2ea043);
  border-color: var(--success, #2ea043);
}
.pill-failed,
.pill-removing {
  color: var(--danger);
  border-color: var(--danger);
}
.pill-running {
  color: var(--accent);
  border-color: var(--accent);
}
.pill-canceled {
  color: var(--muted);
}

/* toast */
.toast {
  position: fixed;
  left: 50%;
  bottom: 28px;
  transform: translateX(-50%);
  background: var(--surface);
  border: 1px solid var(--border);
  color: var(--text);
  border-radius: var(--radius);
  padding: 10px 18px;
  font-size: 13px;
  box-shadow: 0 6px 24px rgba(0, 0, 0, 0.35);
  z-index: 30;
}
</style>
