<script setup lang="ts">
/* DetailView —— /detail/:id?d=<compact Drama JSON>（兜底 ?source=&title=）
   版式照 v1 detail_layout.py：模糊封面铺头区 + 150x225 封面 + 标题/简介/热度/标签；
   选集照 v1 detail_episodes.py：GROUP_SIZE=30 分组 + 44px 网格 + 当前集高亮/未播变淡/VIP 角标。
   操作：播放(→/player 带参，PlayerView 已实装)、下载当集/全部(v2-I 双轨：真实桥走
    App.DownloadStart 分块入队（磁盘守卫在核心侧，前端不重复），MOCK/DEGRADED 保留旧
    Resolve→plan→localStorage 'downloads.queue' 暂存)、追剧(stores/watch → Go watchstore)、
    看过(stores/watch → Go seen-library.json，范围 A 迁移；localStorage 仅降级/一次性迁移源)。
 *     追剧真值、「看到第 N 集」锚点与看过标记已接 Go watchstore；下载真实队列入队已接
    DownloadStart（v2-I），队列页轮询/控制面此前已接（V2-3）。
    v2-D（子代理 D）：页面自身作滚动容器（.shell-main 约定，见 style 注释）；更新状态 pill +
    集数/统计文案本地化（v1 main.py:229/472-485 规则，键在 i18n.ts）；简介下方「演职人员」横排条
    （App.DramaCreators → native dramaCreators，discovery 未接入/失败整条静默隐藏）。 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, DEGRADED, MOCK_MODE } from '@/services/api'
import type { Chapter, CreatorsResult, CreatorEntry, DetailResult, DownloadEntry, Drama } from '@/services/types'
import { resolveCover } from '@/services/covers'
import { releaseStatusKey, t, tf } from '@/services/i18n'
import { chapterEpisode, detailRoute, dramaFromQuery, playerRoute } from '@/services/dramaRef'
import { enqueueDownloads, queueLength } from '@/services/local'
import { useWatchStore } from '@/stores/watch'
import EpisodeGrid from '@/components/EpisodeGrid.vue'
import SeasonMenu from '@/components/SeasonMenu.vue'

const watchStore = useWatchStore()

const route = useRoute()
const router = useRouter()

const drama = ref<Drama | null>(null)
const detail = ref<DetailResult | null>(null)
const loading = ref(false)
const error = ref('')
const toast = ref('')
/** toast 可选跳转链接（v2-I：真实入队成功 → /downloads；非空时 toast 内渲染按钮） */
const toastLink = ref('')
let toastTimer: number | undefined

const selectedIdx = ref(0)
const currentEpisode = ref(0)
/** 追剧态 = watchstore 行标志（stores/watch 乐观更新/回滚即时反映） */
const followed = computed(() => {
  const id = drama.value?.id
  return !!id && !!watchStore.follows.find((r) => r.id === id)?.followed
})
/** 看过态 = watchStore.seen（Go seen-library 真值；loadSeen 首载后响应式生效） */
const watched = computed(() => {
  const id = drama.value?.id
  return !!id && watchStore.isSeen(id)
})
const played = ref<number[]>([])
const anchorPos = ref(0)
const downloading = ref(false)
const descExpanded = ref(false)

function showToast(msg: string, link = '') {
  toast.value = msg
  toastLink.value = link
  clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => {
    toast.value = ''
    toastLink.value = ''
  }, link ? 6000 : 3600) // 带跳转按钮时给足点击时间
}
function goSeason(d: Drama) {
  router.push(detailRoute(d))
}

function goToastLink() {
  const link = toastLink.value
  toast.value = ''
  toastLink.value = ''
  if (link) router.push(link)
}

const chapters = computed<Chapter[]>(() => detail.value?.chapters ?? [])
const meta = computed<Drama>(() => ({ ...(drama.value ?? ({} as Drama)), ...(detail.value?.drama ?? {}) }))
const warning = computed(() => detail.value?.warning || (MOCK_MODE ? 'VITE_MOCK 示例数据，非真实引擎结果' : ''))

const selectedChapter = computed<Chapter | null>(() => chapters.value[selectedIdx.value] ?? chapters.value[0] ?? null)
const selectedEpisode = computed(() =>
  selectedChapter.value ? chapterEpisode(selectedChapter.value, chapters.value.indexOf(selectedChapter.value)) : 0
)

/** 已追平到第几集：watchstore 历史行（Go 每剧一行，取该剧全部历史行的最大集）；无播放记录时 = 第 1 集 */
function resetAnchor() {
  const id = drama.value?.id ?? ''
  const hist = id ? watchStore.history.filter((r) => r.drama.id === id) : []
  let latest: (typeof hist)[number] | undefined
  for (const r of hist) if (!latest || r.updated > latest.updated) latest = r
  currentEpisode.value = latest?.episode ?? 1
  anchorPos.value = latest ? Math.max(0, Math.floor(latest.position)) : 0
  played.value = hist.map((r) => r.episode)
  const idx = chapters.value.findIndex((c, i) => chapterEpisode(c, i) === currentEpisode.value)
  selectedIdx.value = idx >= 0 ? idx : 0
}

async function loadDetail() {
  const d = drama.value
  if (!d) return
  loading.value = true
  error.value = ''
  void loadCreators(d) // 演职员条与详情并行拉取，失败静默（见 loadCreators 注释）
  try {
    detail.value = await api.detail(d)
  } catch (e: any) {
    detail.value = null
    error.value = e?.message ?? String(e)
  } finally {
    loading.value = false
    await Promise.all([watchStore.ensureLoaded(), watchStore.loadSeen()])
    resetAnchor()
  }
}

function onSelectChapter(index: number) {
  selectedIdx.value = index
}

/* ---------- 操作 ---------- */

function goPlay() {
  const d = drama.value
  const ch = selectedChapter.value
  if (!d || !ch) return
  // PlayRequested 语义：跳 /player 带 drama/chapter/episode/pos —— PlayerView 已实装，以下参数即其入参
  router.push(playerRoute(d, selectedEpisode.value, anchorPos.value, ch))
}

/* ---------- 下载（v2-I 双轨，照 stores/watch useLocalLayer 惯例） ----------
   MOCK / DEGRADED：保留旧本地暂存（Resolve→plan→local.ts 'downloads.queue'，文案含「尚未开始
   真实下载」，不假装成功）；真实桥：api.downloadStart → App.DownloadStart（bindings_download.go:42
   DownloadStart(dramaJSON, entriesJSON「[{chapter,index}]」, quality float64) → {added}）。
   index=真实集号 1-based（chapterEpisode 保证 ≥1）；quality=0 自动最高档（与 DownloadsView
   同默认；v1 main.py:2711 磁盘守卫已在核心 enqueue 侧，失败透出中文错误，前端不重复实现）。
   v1 main.py:2721-2728 分块语义保留：核心单次入队上限 500 集（downloadsvc service.go:342
   「单次最多添加 500 集」），长季按 500 分块提交并累加 added。 */

/** 真实入队文案（i18n.ts 本任务禁改，键表随返报上交，后续统一抽取） */
/* v2-6-parity：本地 L 表迁入 i18n.ts 的 detail.*（zh 逐字不变）。
   dlAllReady/dlToastGo 用 getter 而非取值常量：模块级求值会冻结 locale，
   getter 每次渲染重算，切语言即时生效。 */
const L = {
  dlQueued: (n: number) => tf('detail.dlQueued', { n }),
  get dlAllReady() { return t('detail.dlAllReady') },
  dlFail: (m: unknown) => tf('detail.dlFail', { m: String(m ?? '') }),
  get dlToastGo() { return t('detail.dlToastGo') }
}

/** v1 main.py:185-186 copy_summary：复制完整简介（折叠态也复制全文，与展开无关）。
    剪贴板 web API 优先 → Wails ClipboardWrite 兜底（与封面诊断复制同一套路）。 */
async function copySummary() {
  const text = (meta.value?.description || '').trim()
  if (!text) {
    showToast(t('detail.noDescription'))
    return
  }
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const app = (window as any).go?.main?.App
    if (app?.ClipboardWrite) {
      try {
        await app.ClipboardWrite(text)
      } catch (e: any) {
        showToast(tf('common.copyFail', { m: e?.message ?? e }))
        return
      }
    } else {
      showToast(t('common.copyNoClipboard'))
      return
    }
  }
  showToast(t('detail.copySummaryDone'))
}

/** DownloadStart quality：0=核心自动选最高档（与 resolve 阶梯的 quality=1 语义无关） */
const DOWNLOAD_QUALITY = 0
/** 核心单次入队上限（v1 同款分块阈值） */
const ENQUEUE_CHUNK = 500

/** 下载走本地暂存面？（api.ts 判定：VITE_MOCK=1 或启动即无桥 → 降级 local） */
function downloadViaLocal(): boolean {
  return MOCK_MODE || DEGRADED
}

/** 真实桥分块入队，返回核心累计 added（entries 非空，调用方保证）。
    drama 传 meta（query 紧凑 JSON ⊕ Detail 返回合并）——核心 Enqueue 要求 id+source 非空。 */
async function enqueueChunks(entries: DownloadEntry[]): Promise<number> {
  let added = 0
  for (let off = 0; off < entries.length; off += ENQUEUE_CHUNK) {
    const res = await api.downloadStart(meta.value, entries.slice(off, off + ENQUEUE_CHUNK), DOWNLOAD_QUALITY)
    added += res?.added ?? 0
  }
  return added
}

function toastQueued(added: number) {
  showToast(added ? L.dlQueued(added) : L.dlAllReady, '/downloads')
}

async function downloadCurrent() {
  const d = drama.value
  const ch = selectedChapter.value
  if (!d || !ch || downloading.value) return
  downloading.value = true
  try {
    if (downloadViaLocal()) {
      // 旧本地暂存（mock/降级）：Resolve 取 plan（quality=1 高清兜底），plan 原样暂存
      const plan = await api.resolve(d, ch, chapters.value.indexOf(ch), 1)
      const added = enqueueDownloads([
        { dramaId: d.id, title: d.title, chapter: ch as Record<string, any>, episode: selectedEpisode.value, plan: plan as Record<string, any>, addedAt: Date.now() }
      ])
      showToast(
        added
          ? tf('detail.localQueuedOne', { n: selectedEpisode.value, q: queueLength() })
          : tf('detail.localDupOne', { n: selectedEpisode.value, q: queueLength() })
      )
      return
    }
    const added = await enqueueChunks([{ chapter: ch, index: selectedEpisode.value }])
    toastQueued(added)
  } catch (e: any) {
    // 核心中文错误透出（磁盘剩余不足/下载集数无效/服务未初始化等）
    showToast(downloadViaLocal() ? tf('detail.resolveFail', { m: e?.message ?? e }) : L.dlFail(e?.message ?? e))
  } finally {
    downloading.value = false
  }
}

async function downloadAll() {
  const d = drama.value
  if (!d || downloading.value || !chapters.value.length) return
  if (downloadViaLocal()) {
    // 降级/示例态只登记占位（plan=null）：逐集 Resolve ×N 会锤引擎，地址留待恢复真桥后统一处理
    const now = Date.now()
    const items = chapters.value.map((ch, i) => ({
      dramaId: d.id,
      title: d.title,
      chapter: ch as Record<string, any>,
      episode: chapterEpisode(ch, i),
      plan: null,
      addedAt: now
    }))
    const added = enqueueDownloads(items)
    showToast(
      added
        ? tf('detail.localQueuedAll', { a: added, q: queueLength() })
        : tf('detail.localAllReady', { q: queueLength() })
    )
    return
  }
  downloading.value = true
  try {
    // 下载全部=当前季已加载全部 chapters；index 用 chapterEpisode（currentEpisode 优先，兜底位序+1）
    const entries: DownloadEntry[] = chapters.value.map((ch, i) => ({ chapter: ch, index: chapterEpisode(ch, i) }))
    toastQueued(await enqueueChunks(entries))
  } catch (e: any) {
    showToast(L.dlFail(e?.message ?? e))
  } finally {
    downloading.value = false
  }
}

async function onFollow() {
  const d = drama.value
  if (!d) return
  // stores/watch：真值 Go watchstore ToggleFollow（无桥降级 localStorage 记录）
  try {
    const on = await watchStore.toggleFollow(d)
    showToast(tf(on ? 'detail.followDone' : 'detail.unfollowDone', { t: d.title }))
  } catch (e: any) {
    showToast(e?.message ?? t('detail.followFail'))
  }
}

async function onWatchedMark() {
  const d = drama.value
  if (!d) return
  // stores/watch：真值 Go ToggleSeen（bindings_seen.go；无桥降级 localStorage 同名键）
  try {
    const on = await watchStore.toggleSeen(d.id)
    showToast(on ? '已标记看过' : '已取消看过标记')
  } catch (e: any) {
    showToast(e?.message ?? '看过标记失败')
  }
}

/* ---------- 演职员条（v2-D，对齐 v1 main.py:355-380 _load_creators + creators.py CastStrip） ---------- */

const creators = ref<CreatorEntry[]>([])
/** 演员账号统计：heat/followers/plays 有值时替换详情头粗估线（v1 _render_meta 同款规则） */
const creatorStats = ref<CreatorsResult | null>(null)

async function loadCreators(d: Drama) {
  creators.value = []
  creatorStats.value = null
  // v1 只对 hongguo: 前缀剧目查询；discovery 未接入（默认构建恒 discovery_unavailable）
  // 或核心失败 → 整条静默隐藏，不报错（v1 失败会清条+状态提示，v2 取静默以免常态刷屏）
  if (!d.id.startsWith('hongguo:')) return
  try {
    const res = await api.dramaCreators(d)
    creators.value = (res.creators ?? []).filter((c) => (c?.name || '').trim() !== '')
    creatorStats.value = res
  } catch {
    /* 静默 */
  }
}

/** 头像走 resolveCover（真实桥 = 核心封面管线，v1 avatar_drama 同款 id 约定 creator:<userId>；
    mock/降级 = cover 原路径）。key = userId||id||name */
const castCovers = ref<Record<string, string>>({})
function castKey(c: CreatorEntry): string {
  return c.userId || c.id || c.name
}
watch(creators, (list) => {
  castCovers.value = {}
  for (const c of list) {
    if (!c.avatar) continue
    const key = castKey(c)
    void resolveCover({ id: `creator:${key}`, source: 'hongguo', title: c.name, cover: c.avatar } as Drama).then((src) => {
      if (src) castCovers.value = { ...castCovers.value, [key]: src }
    })
  }
})

/* ---------- 头像区数据 ---------- */

/** ongoing/finished → 标题旁状态 pill（v1 set_release_status：未知值不显示 pill） */
const statusKey = computed(() => releaseStatusKey(meta.value.releaseStatus))

const heatText = computed(() => {
  const m = meta.value
  const parts: string[] = []
  const st = creatorStats.value
  // v1 _render_meta（main.py:472-485）：演员统计有值 → 统计行替换 drama 粗估字段
  if (st && (st.heat || st.followers || st.plays)) {
    if (st.heat) parts.push(tf('detail.stat.heat', { v: st.heat }))
    if (st.followers) parts.push(tf('detail.stat.followers', { v: st.followers }))
    if (st.plays) parts.push(tf('detail.stat.plays', { v: st.plays }))
  } else {
    if (m.heat) parts.push(tf('detail.stat.heat', { v: m.heat }))
    if (m.views) parts.push(tf('detail.views', { v: m.views }))
    if (m.onlineDate) parts.push(tf('detail.onlineSince', { date: String(m.onlineDate).slice(0, 10) }))
  }
  // 集数文案（v1:476-478）：更新中「已更新 N 集」，其余「共 N 集」
  const episodes = Number(m.episodes) || 0
  if (episodes > 0) {
    parts.push(
      String(m.releaseStatus || '').toLowerCase() === 'ongoing'
        ? tf('detail.episodesUpdated', { n: episodes })
        : tf('detail.episodesTotal', { n: episodes })
    )
  }
  // 非枚举值（如中文「更新至66集」描述串）原样透出，仅 ongoing/finished 被本地化吃掉
  const raw = String(m.releaseStatus || '').trim()
  if (raw && !releaseStatusKey(raw)) parts.push(raw)
  return parts
})

/** 选集区头：按当前已加载章节数走 v1 同款 ongoing/完结措辞 */
const episodesHead = computed(() => {
  const n = chapters.value.length
  return String(meta.value.releaseStatus || '').toLowerCase() === 'ongoing'
    ? tf('detail.episodesUpdated', { n })
    : tf('detail.episodesTotal', { n })
})

const metaTags = computed(() => {
  const m = meta.value
  const tags = new Set<string>()
  if (m.category) tags.add(m.category)
  for (const t of m.tags ?? []) if (t) tags.add(t)
  if (m.vip) tags.add('VIP')
  return [...tags]
})

const anchorText = computed(() => {
  const last = played.value.length ? Math.max(...played.value) : 0
  return last ? `看到第 ${last} 集` : ''
})

watch(
  () => route.params.id,
  (id) => {
    if (route.name !== 'detail') return
    const raw = String(id ?? '')
    drama.value = raw ? dramaFromQuery(decodeURIComponent(raw), route.query) : null
    detail.value = null
    if (drama.value) {
      loadDetail()
    }
  }
)

onMounted(() => {
  const raw = String(route.params.id ?? '')
  drama.value = raw ? dramaFromQuery(decodeURIComponent(raw), route.query) : null
  if (drama.value) {
    loadDetail()
  } else {
    error.value = '缺少剧目参数（路由应为 /detail/:id）'
  }
})

function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/discover')
}

function coverUrl(c?: string): string {
  if (!c) return ''
  if (/^(https?:|data:|blob:)/i.test(c)) return c
  return c.startsWith('/') ? c : `/${c}`
}

/* 主封面走 resolveCover（真实桥 = 核心下载 + HEIC→JPEG 转码 data URL；mock = 原路径）。
   raw meta.cover 可能是签名 HEIC，不再直绑 <img>；解析出前/失败显 cover-empty 兜底。 */
const coverResolved = ref('')
watch(
  () => [meta.value.id, meta.value.cover] as const,
  () => {
    coverResolved.value = ''
    const d = meta.value
    if (!d.cover) return
    void resolveCover(d).then((src) => {
      if (meta.value.id === d.id) coverResolved.value = src
    })
  },
  { immediate: true }
)

</script>

<template>
  <div class="detail">
    <!-- 头区：模糊封面铺底（v1 BlurCoverBackground） -->
    <div v-if="drama" class="hero">
      <div class="hero-bg" aria-hidden="true">
        <img v-if="coverResolved" :src="coverUrl(coverResolved)" alt="" />
      </div>
      <div class="hero-main">
        <div class="hero-side">
          <button class="btn back" @click="goBack">
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 18l-6-6 6-6" /></svg>
            {{ t('common.back') }}
          </button>
          <div class="cover-box" :class="{ empty: !coverResolved }">
            <img v-if="coverResolved" :src="coverUrl(coverResolved)" :alt="meta.title" />
            <div v-else class="cover-empty">{{ (meta.title || drama.id).slice(0, 4) }}</div>
          </div>
        </div>
        <div class="hero-meta">
          <h1 class="title">
            {{ meta.title || t('detail.noTitle') }}
            <span v-if="statusKey" class="status-pill" :class="statusKey === 'status.ongoing' ? 'ongoing' : 'finished'">{{ t(statusKey) }}</span>
            <span v-if="meta.vip" class="vip-chip">VIP</span>
          </h1>
          <!-- 多季切换（v1 season_button）：同系列 ≥2 季时出现在热度行上方 -->
          <div class="season-row">
            <SeasonMenu :drama="meta" @choose="goSeason" />
          </div>
          <div v-if="heatText.length" class="heat-row">
            <span v-for="h in heatText" :key="h" class="heat-item">{{ h }}</span>
          </div>
          <div v-if="metaTags.length" class="chips tag-row">
            <span v-for="t in metaTags" :key="t" class="chip static">{{ t }}</span>
          </div>
          <p v-if="meta.description" class="desc" :class="{ expanded: descExpanded }" @click="descExpanded = !descExpanded">
            {{ meta.description }}<span v-if="!descExpanded && meta.description.length > 120" class="more">{{ t('detail.expand') }}</span>
          </p>
          <p v-if="anchorText" class="anchor text-muted">{{ anchorText }}</p>
        </div>
      </div>
    </div>

    <div v-if="warning && !loading" class="banner warn-banner">
      <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" /></svg>
      {{ warning }}
    </div>

    <!-- 三态 -->
    <div v-if="loading" class="skel">
      <div class="skel-actions" />
      <div class="skel-grid">
        <div v-for="i in 24" :key="i" class="skel-cell" />
      </div>
    </div>
    <div v-else-if="error" class="state-block">
      <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><circle cx="12" cy="12" r="9" /><path d="M12 8v5m0 3.5 0 0.01" /></svg>
      <div>{{ tf('detail.loadFailMsg', { m: error }) }}</div>
      <button class="btn" @click="loadDetail()">{{ t('common.retry') }}</button>
    </div>
    <template v-else-if="drama">
      <!-- 演职员条（v2-D）：简介下方横排，discovery 未接入/失败时整条不渲染 -->
      <div v-if="creators.length" class="cast-section card">
        <div class="section-head">
          <h2>{{ t('detail.cast.title') }}</h2>
        </div>
        <div class="cast-row">
          <div
            v-for="c in creators"
            :key="castKey(c)"
            class="cast-item"
            :title="[c.role, c.description].filter(Boolean).join(' · ') || c.name"
            @click="router.push({ path: '/creator', query: { u: JSON.stringify(c), name: c.name } })"
          >
            <div class="cast-avatar">
              <img v-if="castCovers[castKey(c)]" :src="coverUrl(castCovers[castKey(c)])" :alt="c.name" />
              <span v-else class="cast-initial">{{ (c.name || '').slice(0, 1) }}</span>
            </div>
            <span class="cast-name">{{ c.name }}</span>
            <span v-if="c.role" class="cast-role">{{ c.role }}</span>
          </div>
        </div>
      </div>

      <!-- 操作区（v1 detail_layout：播放 primary accent + 下载 success） -->
      <div class="actions">
        <button class="btn primary" :disabled="!chapters.length" @click="goPlay">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="currentColor"><path d="M8 5.5v13l11-6.5z" /></svg>
          {{ selectedEpisode ? tf('detail.playEpisode', { n: selectedEpisode }) : t('detail.play') }}
        </button>
        <button class="btn" :disabled="!chapters.length || downloading" @click="downloadCurrent">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4v12m0 0 4-4m-4 4-4-4M4 20h16" /></svg>
          {{ downloading ? t('detail.downloading') : t('detail.downloadCurrent') }}
        </button>
        <button class="btn" :disabled="!chapters.length || downloading" @click="downloadAll">{{ tf('detail.downloadAll', { n: chapters.length }) }}</button>
        <button class="btn" :class="{ toggled: followed }" @click="onFollow">{{ followed ? t('detail.followed') : t('detail.follow') }}</button>
        <button class="btn" :class="{ toggled: watched }" @click="onWatchedMark">{{ watched ? t('detail.watched') : t('detail.watchMark') }}</button>
        <button class="btn" :disabled="!meta.description" @click="copySummary">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V5a2 2 0 0 1 2-2h10" /></svg>
          {{ t('detail.copySummary') }}
        </button>
      </div>

      <!-- 选集 -->
      <div class="ep-section card">
        <div class="section-head">
          <h2>{{ t('detail.episodes') }}</h2>
          <span v-if="chapters.length" class="text-muted">{{ episodesHead }}</span>
          <span v-else class="text-muted">{{ t('detail.noEpisodes') }}</span>
        </div>
        <EpisodeGrid
          v-if="chapters.length"
          :chapters="chapters"
          :current="selectedEpisode"
          :drama-vip="meta.vip"
          :played="played"
          @select="(_chapter, index) => onSelectChapter(index)"
        />
      </div>
    </template>

    <div v-if="toast" class="toast">
      {{ toast }}
      <button v-if="toastLink" class="toast-action" @click="goToastLink">{{ L.dlToastGo }}</button>
    </div>
  </div>
</template>

<style scoped>
/* v2-A 滚动修复：App.vue 的 .shell-main 是 overflow:hidden 的 flex column，
   窗口永不滚动，页面必须自供滚动容器（DiscoverView 走 PosterGrid 内滚，
   SettingsView 自带 overflow-y:auto —— 本页同款约定）。原 min-height:100%
   让长详情被 shell-main 裁掉且无从滚动。 */
.detail {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
}
/* 头区 */
.hero {
  position: relative;
  overflow: hidden;
}
.hero-bg {
  position: absolute;
  inset: -60px -60px 0;
}
.hero-bg img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  /* 高斯模糊底：CSS filter 近似 v1 BlurCoverBackground（radius=40 亮度压暗） */
  filter: blur(42px) brightness(0.5) saturate(1.15);
  transform: scale(1.25);
}
.hero::after {
  content: '';
  position: absolute;
  inset: 0;
  background: linear-gradient(180deg, rgba(15, 17, 23, 0.35), var(--bg) 92%);
  pointer-events: none;
}
.hero-main {
  position: relative;
  z-index: 1;
  display: flex;
  gap: 20px;
  padding: 18px 24px 22px;
}
.hero-side {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 150px;
  flex-shrink: 0;
}
.back {
  align-self: flex-start;
}
.cover-box {
  width: 150px;
  height: 225px; /* v1 COVER_THUMB=(150,225)，2:3 */
  border-radius: var(--guo-radius-lg);
  overflow: hidden;
  background: var(--cover-bg);
  box-shadow: 0 10px 28px var(--guo-shadow);
}
.cover-box img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.cover-box.empty {
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--muted);
  font-size: 15px;
}
.hero-meta {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding-top: 4px;
}
.title {
  font-size: 21px;
  font-weight: 700;
  line-height: 1.35;
  display: flex;
  align-items: center;
  gap: 8px;
}
.season-row { display: flex; align-items: center; gap: 8px; min-height: 26px; }
.vip-chip {
  font-size: 11px;
  font-weight: 600;
  color: var(--guo-vip-fg);
  background: var(--guo-vip-bg);
  border-radius: 4px;
  padding: 2px 7px;
}
/* v2-D：更新状态 pill（v1 set_release_status 的 ongoing/finished 本地化） */
.status-pill {
  font-size: 11px;
  font-weight: 600;
  border-radius: 4px;
  padding: 2px 7px;
  background: var(--surface-2);
}
.status-pill.ongoing {
  color: var(--success);
}
.status-pill.finished {
  color: var(--muted);
}
.heat-row {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  font-size: 12.5px;
  color: var(--muted);
}
.heat-item:first-child {
  color: var(--score);
}
.tag-row {
  padding: 0;
  gap: 6px;
}
.chip.static {
  cursor: default;
  font-size: 12px;
  padding: 3px 10px;
}
.desc {
  font-size: 13px;
  line-height: 1.7;
  color: var(--text);
  cursor: pointer;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.desc.expanded {
  -webkit-line-clamp: unset;
  display: block;
}
.desc .more {
  color: var(--accent);
}
.anchor {
  font-size: 12px;
}
.warn-banner {
  margin: 0 24px;
}
/* 操作区 */
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  padding: 6px 24px 14px;
}
.actions .btn {
  height: 38px;
  padding: 0 16px;
  gap: 7px;
}
.actions .btn.toggled {
  border-color: var(--success);
  color: var(--success);
}
/* 选集卡片 */
.ep-section {
  margin: 0 24px 24px;
  padding: 16px 18px;
}
.section-head {
  display: flex;
  align-items: baseline;
  gap: 12px;
  margin-bottom: 12px;
}
.section-head h2 {
  font-size: 15px;
  font-weight: 600;
}
.section-head .text-muted {
  font-size: 12px;
}
/* 演职员条（v2-D，v1 creators.py CastStrip 横排版式；点击进创作者页 /creator） */
.cast-section {
  margin: 0 24px 10px;
  padding: 14px 18px;
}
.cast-row {
  display: flex;
  gap: 18px;
  overflow-x: auto;
  padding-bottom: 4px;
}
.cast-item {
  display: flex;
  cursor: pointer;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  width: 64px;
  flex-shrink: 0;
}
.cast-avatar {
  width: 48px;
  height: 48px;
  border-radius: 50%;
  overflow: hidden;
  background: var(--cover-bg);
  display: flex;
  align-items: center;
  justify-content: center;
}
.cast-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.cast-initial {
  font-size: 18px;
  color: var(--muted);
}
.cast-name {
  font-size: 12px;
  max-width: 64px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cast-role {
  font-size: 11px;
  color: var(--muted);
}
/* 三态 */
.state-block {
  flex: 1;
  margin: 40px 24px;
}
/* 骨架 */
.skel {
  padding: 10px 24px 24px;
}
.skel-actions {
  height: 38px;
  width: 60%;
  border-radius: var(--guo-radius);
  margin-bottom: 18px;
  background: linear-gradient(90deg, var(--surface-2) 25%, var(--surface-3) 50%, var(--surface-2) 75%);
  background-size: 200% 100%;
  animation: shimmer 1.4s linear infinite;
}
.skel-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(64px, 1fr));
  gap: 8px;
}
.skel-cell {
  height: 44px;
  border-radius: var(--guo-radius);
  background: linear-gradient(90deg, var(--surface-2) 25%, var(--surface-3) 50%, var(--surface-2) 75%);
  background-size: 200% 100%;
  animation: shimmer 1.4s linear infinite;
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
  max-width: 78%;
  box-shadow: 0 6px 24px var(--guo-shadow);
  z-index: 60;
}
/* v2-I：真实入队成功 toast 内的跳转按钮（→ /downloads） */
.toast-action {
  margin-left: 10px;
  padding: 0;
  border: none;
  background: none;
  color: var(--accent);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  white-space: nowrap;
  text-decoration: underline;
  text-underline-offset: 2px;
}
</style>
