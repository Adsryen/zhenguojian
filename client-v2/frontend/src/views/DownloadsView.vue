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
import type { Chapter, Drama, DownloadEntry, DownloadJobRow, MergeEntryRow } from '@/services/types'
import { chapterEpisode, playerRoute } from '@/services/dramaRef'
import { t, tf } from '@/services/i18n'
import { STAT_GROUPS, statMatch, useDownloadsStore, type StatKey } from '@/stores/downloads'
import { useAppStore } from '@/stores/app'

type Tab = 'queue' | 'merges' | 'archive'

const router = useRouter()
const store = useDownloadsStore()
const appStore = useAppStore()

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
  // 统计条站源下拉的 displayName 映射依赖源注册表（v1 SOURCE_LABELS 的 v2 等价物）
  if (!appStore.sources.length) void appStore.loadSources()
})
onUnmounted(() => {
  clearTimeout(toastTimer)
  store.detach()
})

/* ---------------------------------------------------------------- 展示辅助 */

// 状态标签表：computed 包 t() 键，切语言即时更新
const STATE_TEXT = computed<Record<string, string>>(() => ({
  queued: t('downloads.state.queued'),
  downloading: t('downloads.state.downloading'),
  paused: t('downloads.state.paused'),
  completed: t('downloads.state.completed'),
  failed: t('downloads.state.failed'),
  removing: t('downloads.state.removing')
}))

function stateText(job: DownloadJobRow): string {
  return STATE_TEXT.value[job.state] ?? job.state
}

const MERGE_STATE_TEXT = computed<Record<string, string>>(() => ({
  queued: t('downloads.state.queued'),
  running: t('downloads.state.merging'),
  done: t('downloads.state.completed'),
  failed: t('downloads.state.failed'),
  canceled: t('downloads.state.canceled')
}))

function mergeStateText(state: string): string {
  return MERGE_STATE_TEXT.value[state] ?? state
}

/** 合并任务集号区间文案（原模板内联 padStart 逻辑收进函数） */
function mergeRangeText(m: { episodes: number[]; count: number }): string {
  return tf('downloads.epRange', {
    a: String(m.episodes[0] ?? 0).padStart(3, '0'),
    b: String(m.episodes[m.episodes.length - 1] ?? 0).padStart(3, '0'),
    n: m.count
  })
}

// 集号：核心协议 index=真实集号（1-based，app_downloads.go 校验 <1 拒绝）；
// 兼容旧记录（index 0-based）：优先 chapter.currentEpisode，index<1 时回退 index+1。
function epNo(job: DownloadJobRow): number {
  const cur = Number(job.chapter?.currentEpisode ?? 0)
  if (Number.isFinite(cur) && cur >= 1) return cur
  const ix = Number(job.index ?? 0)
  return ix >= 1 ? ix : ix + 1
}

function epLabel(job: DownloadJobRow): string {
  const base = tf('downloads.epNo', { n: epNo(job) })
  const c = job.chapter?.title?.trim()
  return c && !/^第\s*\d+\s*集$/.test(c) ? `${base} · ${c}` : base
}

function titleOf(job: DownloadJobRow): string {
  return job.drama?.title?.trim() || job.chapter?.title?.trim() || t('downloads.unknownDrama')
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
  if (s < 60) return tf('downloads.eta', { t: `${s}s` })
  if (s < 3600) return tf('downloads.eta', { t: `${Math.floor(s / 60)}m${String(s % 60).padStart(2, '0')}s` })
  return tf('downloads.eta', { t: `${Math.floor(s / 3600)}h${String(Math.floor((s % 3600) / 60)).padStart(2, '0')}m` })
}

function pctOf(job: DownloadJobRow): number {
  const p = job.progress ?? (job.total > 0 ? job.bytes / job.total : 0)
  return Math.max(0, Math.min(100, Math.round(p * 100)))
}

function sizeText(job: DownloadJobRow): string {
  if (job.state === 'completed') return job.total > 0 ? fmtBytes(job.total) : fmtBytes(job.bytes)
  if (job.total > 0) return `${fmtBytes(job.bytes)} / ${fmtBytes(job.total)}`
  // 下载中但尚未解析出体积（核心 resolve 前置窗口 bytes/total/progress 全 0）：
  // 显示占位文案，避免出现空白或 0 B / 0 B（待 i18n 收敛）
  if (job.state === 'downloading' || job.state === 'queued') {
    return job.bytes > 0 ? fmtBytes(job.bytes) : t('downloads.resolving')
  }
  return job.bytes > 0 ? fmtBytes(job.bytes) : ''
}

/* ---------------------------------------------------------------- 行操作（v1 ROW_ACTIONS 对齐） */

function goPlay(job: DownloadJobRow) {
  const d: Drama = job.drama ?? { id: '', source: '', title: '' }
  if (!d.id) {
    showToast(t('downloads.toastNoDrama'))
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

// R22b「按季合并选中」的落点：v1 靠左侧剧列表的复选框，
// v2 没有那个列表，但这里已经**按剧分组**（每颗 chip 就是一部剧），
// 所以复选挂在 chip 上、选中≥2 时出现那颗批次按钮 —— 是最接近的现有结构，不是新造页面。
const multiIds = ref<string[]>([])
const mergingMulti = ref(false)
const multiDramas = computed(() =>
  store.mergeGroups.filter((g) => multiIds.value.includes(g.dramaId)).map((g) => g.drama))

function toggleMergeGroup(id: string) {
  multiIds.value = multiIds.value.includes(id)
    ? multiIds.value.filter((x) => x !== id)
    : [...multiIds.value, id]
}

async function onMergeMulti() {
  if (multiDramas.value.length < 2) return
  mergingMulti.value = true
  try {
    const res = await store.mergeStartMulti(multiDramas.value)
    showToast(tf('downloads.mergeMultiDone', { n: res.queued }))
    multiIds.value = [] // 排完清勾选：留着会让人以为还要再点一次
  } catch (e: any) {
    // 按 code 取句子（Go 的错误串只进日志，界面不许显示中文句子给英文用户）
    const map: Record<string, string> = {
      multi_targets: t('downloads.mergeMultiFew'),
      multi_none: t('downloads.mergeMultiNone'),
      multi_running: t('downloads.mergeMultiAllRunning')
    }
    showToast(map[e?.code] ?? (e?.message ?? t('downloads.mergeFail')))
  } finally {
    mergingMulti.value = false
  }
}

/* v1 的双击播放（downloads.py:1139 itemDoubleClicked → _open_merge_entry 1495-1498
   * → _play_merged_path 1631-1640），v2 之前完全没有这个入口——
   * 「合并」页签只把成品路径当文本显示出来，看完得自己去开目录。
   * 走的是本轮刚接通的本地播放那条路（App.PlayerOpenLocal：不 resolve、
   * 选集侧栏不渲染、上下集禁用、标题「本地播放 <文件名>」）。
   *
   * ⚠ 文案：v1 在这里说的是「成品文件不存在：<文件名>」，
   *   而历史/追剧那两处说的是「本地文件已不存在：<文件名>」——两句不同、同一件事。
   *   v2 只留一句（`player.localMissing`，由 ApiError.code=local_missing 触发），
   *   少一句是一处**有意的措辞归一**，不是漏做；这条差异记在
   *   research/v1-v2-downloads-parity.md，别让下一个人以为还欠一条。
   *
   * ⚠ v1 那 0.8s 去抖**没有照搬**：它防的是 Qt 的现象
   *   （v1 注释原话 "a double click reports itself twice"），
   *   而 web 的 dblclick 是一次事件，没有"自报两次"这回事。
   *   我曾把那段去抖抄过来并给它写断言，结果写出一条恒真断言
   *   （摘掉去抖它也过）——那正说明被测的东西在本平台不存在。
   *   留着一段永不触发、也无法验证的代码，比不抄更糟。 */
function onMergeRowDblclick(m: MergeEntryRow) {
  if (m.state !== 'done' || !m.output) return // v1：别的状态双击不做事
  void router.push(playerRoute(
    { id: m.dramaId, title: m.title, source: '', cover: '', episodes: 0 } as Drama,
    0, 0, null, m.output))
}

async function onMergeSeason(drama: Drama) {
  const res = await store.mergeSeason(drama)
  if (res) showToast(tf('downloads.toastMerged', { t: drama.title }))
}

async function onMergeSelected() {
  const res = await store.mergeSelected([...selected.value])
  if (res) showToast(t('downloads.toastMergedPlain'))
}

async function onRemoveBatch() {
  const ids = [...selected.value]
  const res = await store.removeBatch(ids)
  if (!res) return
  const fails = Object.keys(res.failures ?? {}).length
  showToast(
    fails
      ? tf('downloads.toastRemovePartial', { n: res.completed.length, f: fails })
      : tf('downloads.toastRemoved', { n: res.completed.length })
  )
  selected.value = new Set()
}

/* ---------------------------------------------------------------- 统计条筛选（v1 _StatBar 对齐，D1 目标3） */

// 统计条文案已收敛至 i18n.ts downloads.statbar.*（原组件内 L 表）；t() 随 locale 响应
const statLabels = computed<Record<Exclude<StatKey, ''>, string>>(() => ({
  active: t('downloads.statbar.active'),
  paused: t('downloads.statbar.paused'),
  completed: t('downloads.statbar.completed'),
  failed: t('downloads.statbar.failed')
}))

const statKey = ref<StatKey>('')
const sourceFilter = ref('')

const statChips = computed(() => [
  { key: '' as StatKey, label: t('downloads.statbar.all'), count: store.statCounts[''] },
  ...STAT_GROUPS.map((g) => ({ key: g.key as StatKey, label: statLabels.value[g.key], count: store.statCounts[g.key] }))
])

/** slug → 展示名（displayName → name → slug 兜底，同 ReserveView:102 口径） */
function sourceLabel(slug: string): string {
  const s = appStore.sources.find((x) => x.slug === slug)
  return s?.displayName || s?.name || slug
}

const sourceOptions = computed(() => store.queueSources.map((slug) => ({ slug, label: sourceLabel(slug) })))

// 快照推进后所选源可能消失：选择保留但不再生效（v1 set_sources「仍存在才保留」语义）
const effSource = computed(() =>
  sourceOptions.value.some((o) => o.slug === sourceFilter.value) ? sourceFilter.value : ''
)

const visibleJobs = computed(() =>
  store.activeJobs.filter(
    (j) => statMatch(statKey.value, j.state) && (!effSource.value || (j.drama?.source ?? '') === effSource.value)
  )
)

/** 队列非空但筛完为空 → 空结果态（区别于队列本身为空） */
const filteredEmpty = computed(() => store.activeJobs.length > 0 && visibleJobs.value.length === 0)

// v1 _choose 同款：点「全部」清除；再点当前选中项 = 取消筛选
function chooseStat(key: StatKey) {
  statKey.value = key === '' || key === statKey.value ? '' : key
}

/* 打开所在目录（v1 downloads.py _open_path 对齐：目录不存在则先建再开）。
   Go 壳 App.OpenDownloadPath：Windows explorer，文件路径带 /select 定位。
   传参 = downloadDir.directory + job.folder 拼绝对目录（job.folder=source/id）。 */
async function openJobFolder(job: DownloadJobRow) {
  const dir = store.downloadDir?.directory
  if (!job.folder || !dir) {
    showToast(t('downloads.statbar.openTodo'))
    return
  }
  try {
    // 演示环境（MOCK_MODE）：真实 exe 才开 explorer；mock 落 __zgjOpenPath 钩子可断言
    await api.openDownloadPath(`${dir}/${job.folder}`)
  } catch (e) {
    showToast(tf('downloads.toastOpenFail', { e: (e as Error)?.message ?? String(e) }))
  }
}
function openPathTitle(job: DownloadJobRow): string {
  return job.folder ? `${t('downloads.statbar.openFolder')}：${job.folder}` : t('downloads.statbar.openTodo')
}

/* ---------------------------------------------------------------- 归档扫描（交付 C）
   数据源 = App.ArchiveScan（downloadsvc.ScanArchiveRoot，文件系统为准 + 索引富化）：
   一个剧一张卡（N 集 / 大小 / mtime / 含合并徽标），点开走 openDownloadPath 现有链路；
   扫描不可用（旧绑定异常/未返回）时回落原「索引 archived 逐条列表」，保留恢复/删除入口。 */
watch(tab, (v) => {
  if (v === 'archive') void store.loadArchive()
})

function archiveCover(name: string): string {
  return name.trim().slice(0, 1) || '?'
}

function archiveMtimeText(ms: number): string {
  if (!ms) return ''
  const d = new Date(ms)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

async function openArchiveGroup(g: { name: string; openPath: string }) {
  const dir = store.archive?.directory || store.downloadDir?.directory
  if (!dir || !g.openPath || g.openPath === '.') {
    showToast(t('downloads.statbar.openTodo'))
    return
  }
  try {
    await api.openDownloadPath(`${dir}/${g.openPath}`)
  } catch (e) {
    showToast(tf('downloads.toastOpenFail', { e: (e as Error)?.message ?? String(e) }))
  }
}

async function copyArchiveDir() {
  const dir = store.archive?.directory || store.downloadDir?.directory || ''
  if (!dir) return
  try {
    await navigator.clipboard.writeText(dir)
    showToast(t('downloads.archive.copied'))
  } catch {
    showToast(t('downloads.archive.copyFail'))
  }
}

/* ---------------------------------------------------------------- 设置面板 */

const showSettings = ref(false)
const conc = ref(2)
const bySource = ref(false)
const verify = ref(true)
// R22a③「下载时保存弹幕」（v1 downloads.py:1095-1099 那个勾选框，默认关）
const saveDanmaku = ref(false)
const savingDanmaku = ref(false)
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

// 弹幕开关单独跟着 store（它即点即存，不进那个"保存"按钮的批量提交）
watch(() => store.danmakuDownload, (on) => { saveDanmaku.value = !!on }, { immediate: true })

async function onSaveDanmaku() {
  savingDanmaku.value = true
  try {
    const got = await store.setDanmakuDownload(saveDanmaku.value)
    showToast(got ? t('downloads.saveDanmakuOn') : t('downloads.saveDanmakuOff'))
  } catch {
    // 写失败：勾选框已经被 store 弹回原值，这里必须说一句"没写进去、已回滚"。
    // 不说的那版会把失败播报成一次正常操作（⑥b 实测）。
    saveDanmaku.value = store.danmakuDownload
    showToast(t('downloads.saveDanmakuFail'))
  } finally {
    savingDanmaku.value = false
  }
}

async function onSaveSettings() {
  savingSettings.value = true
  const ok = await store.saveSettings(conc.value, bySource.value, verify.value)
  savingSettings.value = false
  if (ok) showToast(t('downloads.toastSettingsSaved'))
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
const quality = ref(0)
const enqueuing = ref(false)

// 核心把 quality 当作“目标实际档位”处理（nativePlaybackChoices：0=自动选最高，
// 其余按 360/540/720/1080 匹配，档位不存在则回落默认最高）。v1 亦恒传 0。
// 旧 0/1/2 枚举与核心语义不符（省流从未生效），改为真实档位值。（待 i18n 收敛）
const QUALITY_OPTIONS = computed(() => [
  { value: 0, label: t('downloads.quality.auto') },
  { value: 540, label: t('downloads.quality.p540') },
  { value: 720, label: t('downloads.quality.p720') },
  { value: 1080, label: t('downloads.quality.p1080') }
])

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
    if (!searchResults.value.length) showToast(t('downloads.toastNoResult'))
  } catch (e) {
    showToast(tf('downloads.toastSearchFailed', { m: (e as Error).message }))
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
    if (!list.length) chapterErr.value = t('downloads.toastNoChapters')
  } catch (e) {
    chapterErr.value = tf('downloads.toastChapterFail', { m: (e as Error).message })
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
  // 核心协议 index=真实集号（1-based）；传 0（旧代码 i）会触发「下载集数无效」
  // 导致整批入队失败。优先取 chapter.currentEpisode，兜底列表位序+1。
  const entries: DownloadEntry[] = [...pickedChapters.value]
    .sort((a, b) => a - b)
    .map((i) => {
      const ch = chapters.value[i]
      const cur = Number(ch?.currentEpisode ?? 0)
      return { chapter: ch, index: Number.isFinite(cur) && cur >= 1 ? cur : i + 1 }
    })
  enqueuing.value = true
  try {
    const res = await api.downloadStart(drama, entries, quality.value)
    showToast(tf('downloads.toastEnqueued', { n: res?.added ?? entries.length }))
    resetAddPanel()
    tab.value = 'queue'
    await store.refresh()
  } catch (e) {
    showToast(tf('downloads.toastEnqueueFail', { m: (e as Error).message }))
  } finally {
    enqueuing.value = false
  }
}
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">{{ t('downloads.title') }}</h1>
    <span v-if="MOCK_MODE" class="chip static sample-chip">{{ t('downloads.mockChip') }}</span>
    <div class="spacer" />
    <button class="btn" :disabled="!!store.busyId" @click="store.controlAll('pauseAll')">{{ t('downloads.pauseAll') }}</button>
    <button class="btn" :disabled="!!store.busyId" @click="store.controlAll('resumeAll')">{{ t('downloads.resumeAll') }}</button>
    <button class="btn" :class="{ primary: showSettings }" @click="showSettings = !showSettings">{{ t('downloads.concurrencyBtn') }}</button>
    <button class="btn primary" :class="{ pressed: showAdd }" @click="showAdd ? resetAddPanel() : (showAdd = true)">
      {{ showAdd ? t('downloads.collapseAdd') : t('downloads.addDownload') }}
    </button>
  </div>

  <p v-if="store.status" class="status-line text-muted">{{ store.status }}</p>

  <!-- 并发设置（对齐核心 resourceSettings.downloadConcurrency，1..8） -->
  <div v-if="showSettings" class="card panel">
    <div class="panel-row">
      <label class="p-label">{{ t('downloads.concurrencyLabel') }}</label>
      <input v-model.number="conc" class="num" type="number" min="1" max="8" step="1" />
      <span class="text-muted p-hint">{{ t('downloads.concurrencyHint') }}</span>
    </div>
    <div class="panel-row">
      <label class="p-check"><input v-model="bySource" type="checkbox" />{{ t('downloads.bySource') }}</label>
      <label class="p-check"><input v-model="verify" type="checkbox" />{{ t('downloads.verifyDecode') }}</label>
      <!-- v1 downloads.py:1095-1099：勾上后每集解密完成会额外保存同名 .ass/.danmaku.json，
           默认关；即点即存（v1 的 toggled 直接写设置，不等下面的保存按钮）。 -->
      <label class="p-check">
        <input v-model="saveDanmaku" type="checkbox" data-testid="dl-save-danmaku"
               :disabled="savingDanmaku" :title="t('downloads.saveDanmakuHint')"
               @change="onSaveDanmaku" />{{ t('downloads.saveDanmaku') }}
      </label>
    </div>
    <div v-if="store.settings?.warning" class="p-warn text-muted">{{ store.settings.warning }}</div>
    <div class="panel-row">
      <button class="btn primary" :disabled="savingSettings" @click="onSaveSettings">
        {{ savingSettings ? t('downloads.saving') : t('downloads.save') }}
      </button>
    </div>
  </div>

  <!-- 添加下载：搜索 → 选剧 → 分集多选 → 入队 -->
  <div v-if="showAdd" class="card panel">
    <div class="panel-row">
      <input v-model="addQuery" class="textin" :placeholder="t('downloads.searchPh')" @keyup.enter="onSearch" />
      <button class="btn" :disabled="searching || !addQuery.trim()" @click="onSearch">
        {{ searching ? t('downloads.searching') : t('downloads.search') }}
      </button>
    </div>
    <div v-if="searchResults.length && !pickedDrama" class="result-list">
      <button v-for="d in searchResults" :key="d.id" class="result-item" @click="onPick(d)">
        <span class="r-title">{{ d.title }}</span>
        <span class="r-sub text-muted">{{ d.source }}{{ d.episodes ? ' · ' + tf('downloads.episodesTotal', { n: d.episodes }) : '' }}</span>
      </button>
    </div>
    <template v-if="pickedDrama">
      <div class="panel-row">
        <span class="picked-title">{{ pickedDrama.title }}</span>
        <button class="btn small" @click="pickAllChapters">
          {{ pickedChapters.size === chapters.length ? t('downloads.unselectAll') : t('downloads.selectAll') }}
        </button>
        <span class="text-muted p-hint">{{ tf('downloads.selectedN', { n: pickedChapters.size }) }}</span>
        <label class="p-label q-label">{{ t('downloads.quality') }}</label>
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
          {{ enqueuing ? t('downloads.enqueuing') : t('downloads.enqueue') }}
        </button>
        <button class="btn" @click="resetAddPanel">{{ t('downloads.cancel') }}</button>
      </div>
    </template>
  </div>

  <!-- 分页 -->
  <div class="tabs">
    <button class="tab" :class="{ on: tab === 'queue' }" @click="tab = 'queue'">
      {{ t('downloads.tabQueue') }}<em>{{ store.activeJobs.length }}</em>
    </button>
    <button class="tab" :class="{ on: tab === 'merges' }" @click="tab = 'merges'">
      {{ t('downloads.tabMerges') }}<em>{{ store.merges.length }}</em>
    </button>
    <button class="tab" :class="{ on: tab === 'archive' }" data-testid="tab-archive" @click="tab = 'archive'">
      {{ t('downloads.tabArchive') }}<em data-testid="archive-count">{{ store.archiveCount }}</em>
    </button>
  </div>

  <!-- 下载队列 -->
  <template v-if="tab === 'queue'">
    <!-- 统计条：状态计数 chip（点击即筛选，再点取消）+ 右侧站源下拉（与状态叠加），纯前端派生 -->
    <div class="stat-bar card" data-testid="stat-bar">
      <button
        v-for="c in statChips"
        :key="c.key || 'all'"
        class="chip"
        :class="{ active: statKey === c.key }"
        data-testid="stat-chip"
        :data-stat="c.key"
        @click="chooseStat(c.key)"
      >
        {{ c.label }}<em data-testid="stat-count">{{ c.count }}</em>
      </button>
      <div class="spacer" />
      <span class="sb-label text-muted">{{ t('downloads.statbar.source') }}</span>
      <select v-model="sourceFilter" class="sel" data-testid="source-select">
        <option value="">{{ t('downloads.statbar.allSources') }}</option>
        <option v-for="o in sourceOptions" :key="o.slug" :value="o.slug">{{ o.label }}</option>
      </select>
    </div>

    <div v-if="store.mergeGroups.length || selectedJobs.length" class="merge-bar card">
      <span class="mb-label">{{ t('downloads.mergeEntry') }}</span>
      <span v-for="g in store.mergeGroups" :key="g.dramaId" class="mb-group">
        <input
          type="checkbox"
          class="mb-check"
          data-testid="merge-group-check"
          :checked="multiIds.includes(g.dramaId)"
          :disabled="!!store.busyId"
          :aria-label="t('downloads.mergeGroupPick')"
          :title="t('downloads.mergeGroupPick')"
          @change="toggleMergeGroup(g.dramaId)"
        />
        <button
          class="chip"
          :disabled="!!store.busyId"
          :title="tf('downloads.mergeSeasonTip', { n: g.jobs.length })"
          @click="onMergeSeason(g.drama)"
        >
          {{ tf('downloads.mergeSeason', { t: g.title, n: g.jobs.length }) }}
        </button>
      </span>
      <button
        v-if="multiIds.length >= 2"
        class="chip primary"
        data-testid="merge-multi"
        :disabled="!!store.busyId || mergingMulti"
        :title="t('downloads.mergeMultiTip')"
        @click="onMergeMulti"
      >
        {{ tf('downloads.mergeMulti', { n: multiIds.length }) }}
      </button>
      <button
        v-if="selectedJobs.length >= 2 && selectedCompletable && selectedSingleDrama"
        class="chip active"
        :disabled="!!store.busyId"
        @click="onMergeSelected"
      >
        {{ tf('downloads.mergeSelected', { n: selectedJobs.length }) }}
      </button>
      <button v-if="selectedJobs.length" class="chip danger-chip" :disabled="!!store.busyId" @click="onRemoveBatch">
        {{ tf('downloads.removeSelected', { n: selectedJobs.length }) }}
      </button>
      <span v-if="selectedJobs.length && !(selectedCompletable && selectedSingleDrama)" class="text-muted mb-hint">
        {{ t('downloads.mergeSelHint') }}
      </span>
    </div>

    <div v-if="!store.activeJobs.length" class="state-block">
      <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12m0 0l-4-4m4 4l4-4M4 19h16" /></svg>
      <div>{{ t('downloads.queueEmpty') }}</div>
      <div class="text-muted hint">{{ t('downloads.queueEmptyHint') }}</div>
    </div>

    <div v-else-if="filteredEmpty" class="state-block" data-testid="filtered-empty">
      <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="7" /><path d="M21 21l-4.35-4.35M8 11h6" /></svg>
      <div>{{ t('downloads.statbar.emptyFiltered') }}</div>
      <div class="text-muted hint">{{ t('downloads.statbar.emptyFilteredHint') }}</div>
    </div>

    <div v-else class="card list">
      <div
        v-for="(job, i) in visibleJobs"
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
          <button v-if="job.state === 'completed'" class="btn small" @click="goPlay(job)">{{ t('downloads.play') }}</button>
          <button
            v-if="job.state === 'completed'"
            class="btn small"
            data-testid="row-open"
            :title="openPathTitle(job)"
            @click="openJobFolder(job)"
          >
            {{ t('downloads.statbar.openFolder') }}
          </button>
          <button
            v-if="job.state === 'downloading' || job.state === 'queued'"
            class="btn small"
            :disabled="!!store.busyId"
            @click="store.control(job, 'pause')"
          >
            {{ t('downloads.pause') }}
          </button>
          <button
            v-if="job.state === 'paused'"
            class="btn small"
            :disabled="!!store.busyId"
            @click="store.control(job, 'resume')"
          >
            {{ t('downloads.resume') }}
          </button>
          <button
            v-if="job.state === 'failed'"
            class="btn small"
            :disabled="!!store.busyId"
            @click="store.retry(job)"
          >
            {{ t('downloads.retry') }}
          </button>
          <button
            v-if="job.state === 'completed'"
            class="btn small"
            :disabled="!!store.busyId"
            @click="store.control(job, 'archive')"
          >
            {{ t('downloads.archive') }}
          </button>
          <button
            class="btn small danger"
            :disabled="!!store.busyId || job.state === 'removing'"
            @click="store.control(job, 'remove')"
          >
            {{ job.state === 'removing' ? t('downloads.removing') : t('downloads.remove') }}
          </button>
        </div>
      </div>
    </div>
  </template>

  <!-- 合并队列 -->
  <template v-if="tab === 'merges'">
    <div v-if="store.merges.length" class="mb-bar">
      <button class="btn danger" :disabled="!!store.busyId" @click="store.mergeCancel('')">{{ t('downloads.stopAllMerges') }}</button>
    </div>
    <div v-if="!store.merges.length" class="state-block">
      <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="6" width="18" height="12" rx="2" /><path d="M9 10l5 2-5 2z" /></svg>
      <div>{{ t('downloads.mergeEmpty') }}</div>
      <div class="text-muted hint">{{ t('downloads.mergeEmptyHint') }}</div>
    </div>
    <div v-else class="card list">
      <div
        v-for="(m, i) in store.merges"
        :key="m.id"
        class="row"
        :class="{ divided: i > 0, playable: m.state === 'done' && !!m.output }"
        data-testid="merge-row"
        :title="m.state === 'done' && m.output ? t('downloads.mergeDblTip') : ''"
        @dblclick="onMergeRowDblclick(m)"
      >
        <div class="r-main">
          <div class="r-line1">
            <span class="r-title">{{ m.title }}</span>
            <span class="r-ep text-muted">{{ mergeRangeText(m) }}</span>
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
            {{ t('downloads.cancel') }}
          </button>
        </div>
      </div>
    </div>
  </template>

  <!-- 归档 -->
  <template v-if="tab === 'archive'">
    <!-- 界面自证：扫描位置 = 核心下载根目录（App.ArchiveScan → downloadDirectory） -->
    <div v-if="store.archive || store.downloadDir" class="dir-line text-muted">
      <span class="dir-ic">📂</span>
      <span>{{ t('downloads.archiveDirPrefix') }}{{ store.archive?.directory || store.downloadDir?.directory }}</span>
      <span v-if="store.archive && !store.archive.exists" class="pill pill-failed">{{ t('downloads.dirMissing') }}</span>
      <button class="btn small" data-testid="copy-dir" @click="copyArchiveDir">{{ t('downloads.archive.copyPath') }}</button>
      <button class="btn small" data-testid="rescan" :disabled="store.archiveLoading" @click="store.loadArchive(true)">{{ t('downloads.archive.rescan') }}</button>
    </div>

    <div v-if="store.archiveLoading && !store.archive" class="state-block text-muted" data-testid="archive-scanning">
      {{ t('downloads.archive.scanning') }}
    </div>

    <!-- 扫描结果：文件系统分组（交付 B/C） -->
    <template v-else-if="store.archive">
      <!-- 目录不存在 → 空态带真实路径（上方 dir-line 有复制按钮） -->
      <div v-if="!store.archive.exists" class="state-block" data-testid="archive-missing">
        <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7h18l-2 13H5L3 7zm3 0V5a2 2 0 012-2h8a2 2 0 012 2v2M9 12h6" /></svg>
        <div class="dir-missing-line">{{ tf('downloads.dirMissingLine', { d: store.archive.directory }) }}</div>
        <div class="text-muted hint">{{ t('downloads.dirMissingHint') }}</div>
      </div>
      <div v-else-if="!store.archive.groups.length" class="state-block" data-testid="archive-empty">
        <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7h18l-2 13H5L3 7zm3 0V5a2 2 0 012-2h8a2 2 0 012 2v2M9 12h6" /></svg>
        <div>{{ t('downloads.archiveEmpty') }}</div>
        <div class="text-muted hint">{{ t('downloads.archive.noMedia') }}</div>
      </div>
      <div v-else class="card list" data-testid="archive-groups">
        <div v-for="(g, i) in store.archive.groups" :key="g.name" class="row" :class="{ divided: i > 0 }">
          <span class="g-avatar" aria-hidden="true">{{ archiveCover(g.name) }}</span>
          <div class="r-main">
            <div class="r-line1">
              <span class="r-title">{{ g.name }}</span>
              <span class="r-ep text-muted" data-testid="archive-eps">{{ tf('downloads.archive.episodesN', { n: g.episodes }) }}</span>
              <span v-if="g.hasMerged" class="pill pill-completed" data-testid="merged-badge">{{ t('downloads.archive.mergedBadge') }}</span>
              <span v-if="g.archived" class="pill pill-paused" data-testid="archived-badge">{{ t('downloads.state.completedArchived') }}</span>
              <span v-if="!g.indexed" class="pill" data-testid="orphan-badge">{{ t('downloads.archive.orphanBadge') }}</span>
            </div>
            <div class="r-line2 text-muted" data-testid="archive-meta">
              <span>{{ fmtBytes(g.bytes) }}</span>
              <span v-if="g.mergedBytes"> · {{ tf('downloads.archive.mergedSize', { s: fmtBytes(g.mergedBytes) }) }}</span>
              <span v-if="g.mtime"> · {{ tf('downloads.archive.mtime', { d: archiveMtimeText(g.mtime) }) }}</span>
            </div>
          </div>
          <div class="r-ops">
            <button class="btn small" data-testid="row-open" :title="tf('downloads.archive.openTip', { p: g.openPath })" @click="openArchiveGroup(g)">{{ t('downloads.statbar.openFolder') }}</button>
          </div>
        </div>
      </div>
    </template>

    <!-- 扫描不可用（真实桥旧版/异常）→ 回落索引 archived 逐条列表，保留恢复/删除入口 -->
    <template v-else>
      <div v-if="!store.archivedJobs.length" class="state-block">
        <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7h18l-2 13H5L3 7zm3 0V5a2 2 0 012-2h8a2 2 0 012 2v2M9 12h6" /></svg>
        <div v-if="store.downloadDir && !store.downloadDir.exists">{{ tf('downloads.dirMissingLine', { d: store.downloadDir.directory }) }}</div>
        <div v-else>{{ t('downloads.archiveEmpty') }}</div>
        <div v-if="store.downloadDir && !store.downloadDir.exists" class="text-muted hint">
          {{ t('downloads.dirMissingHint') }}
        </div>
        <div v-else class="text-muted hint">{{ t('downloads.archiveHint') }}</div>
      </div>
      <div v-else class="card list">
        <div v-for="(job, i) in store.archivedJobs" :key="job.id" class="row" :class="{ divided: i > 0, busy: store.busyId === job.id }">
          <span class="row-check placeholder" />
          <div class="r-main">
            <div class="r-line1">
              <span class="r-title">{{ titleOf(job) }}</span>
              <span class="r-ep text-muted">{{ epLabel(job) }}</span>
              <span class="pill pill-completed">{{ t('downloads.state.completedArchived') }}</span>
            </div>
          </div>
          <div class="r-ops">
            <button class="btn small" data-testid="row-open" :title="openPathTitle(job)" @click="openJobFolder(job)">{{ t('downloads.statbar.openFolder') }}</button>
            <button class="btn small" :disabled="!!store.busyId" @click="store.control(job, 'restore')">{{ t('downloads.restore') }}</button>
            <button class="btn small danger" :disabled="!!store.busyId" @click="store.control(job, 'remove')">{{ t('downloads.remove') }}</button>
          </div>
        </div>
      </div>
    </template>
  </template>

  <p class="footnote text-muted">
    {{ t('downloads.footnote') }} {{ MOCK_MODE ? t('downloads.footnoteMock') : t('downloads.footnoteReal') }}
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
.dir-line {
  margin: 0 24px 8px;
  font-size: 12px;
  display: flex;
  align-items: center;
  gap: 6px;
}
.dir-ic {
  font-size: 13px;
}
.g-avatar {
  flex-shrink: 0;
  width: 30px;
  height: 30px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  background: var(--surface-2, var(--surface));
  border: 1px solid var(--border);
  font-size: 14px;
  font-weight: 600;
}
.dir-missing-line {
  max-width: 560px;
  word-break: break-all;
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

/* 统计条（对齐 merge-bar 版式 + base.css chip 体系） */
.stat-bar {
  margin: 0 24px 10px;
  padding: 8px 14px;
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.stat-bar .chip em {
  font-style: normal;
  margin-left: 6px;
  font-size: 12px;
  opacity: 0.85;
}
.sb-label {
  font-size: 12px;
  margin-left: 4px;
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
/* 双击可播的合并行给一点可点性提示（v1 靠 Qt 行选中态暗示，web 上没说就是猜） */
.row.playable {
  cursor: pointer;
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
