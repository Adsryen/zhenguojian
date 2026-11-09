<script setup lang="ts">
/* DetailView —— /detail/:id?d=<compact Drama JSON>（兜底 ?source=&title=）
   版式照 v1 detail_layout.py：模糊封面铺头区 + 150x225 封面 + 标题/简介/热度/标签；
   选集照 v1 detail_episodes.py：GROUP_SIZE=30 分组 + 44px 网格 + 当前集高亮/未播变淡/VIP 角标。
   操作：播放(→/player 带参，PlayerView 现为占位)、下载当集/全部(Resolve→plan→localStorage
   'downloads.queue'，V2-3 才接下载引擎)、追剧/看过(localStorage 兜底)。
   —— 依赖请求（详见终报）：追剧需 Go 端 watchstore 绑定；// TODO binding: native.Follow/FollowList */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, MOCK_MODE } from '@/services/api'
import type { Chapter, DetailResult, Drama } from '@/services/types'
import { chapterEpisode, dramaFromQuery, playerRoute } from '@/services/dramaRef'
import {
  enqueueDownloads,
  historyAnchor,
  isFollowed,
  isWatched,
  queueLength,
  readHistory,
  toggleFollow,
  toggleWatched
} from '@/services/local'
import EpisodeGrid from '@/components/EpisodeGrid.vue'

const route = useRoute()
const router = useRouter()

const drama = ref<Drama | null>(null)
const detail = ref<DetailResult | null>(null)
const loading = ref(false)
const error = ref('')
const toast = ref('')
let toastTimer: number | undefined

const selectedIdx = ref(0)
const currentEpisode = ref(0)
const followed = ref(false)
const watched = ref(false)
const played = ref<number[]>([])
const anchorPos = ref(0)
const downloading = ref(false)
const descExpanded = ref(false)

function showToast(msg: string) {
  toast.value = msg
  clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => (toast.value = ''), 3600)
}

const chapters = computed<Chapter[]>(() => detail.value?.chapters ?? [])
const meta = computed<Drama>(() => ({ ...(drama.value ?? ({} as Drama)), ...(detail.value?.drama ?? {}) }))
const warning = computed(() => detail.value?.warning || (MOCK_MODE ? 'VITE_MOCK 示例数据，非真实引擎结果' : ''))

const selectedChapter = computed<Chapter | null>(() => chapters.value[selectedIdx.value] ?? chapters.value[0] ?? null)
const selectedEpisode = computed(() =>
  selectedChapter.value ? chapterEpisode(selectedChapter.value, chapters.value.indexOf(selectedChapter.value)) : 0
)

/** 已追平到第几集：选集当前集；无播放记录时 = 第 1 集 */
function resetAnchor() {
  const id = drama.value?.id ?? ''
  const anchor = id ? historyAnchor(id) : undefined
  currentEpisode.value = anchor?.episode ?? 1
  anchorPos.value = anchor ? Math.max(0, Math.floor(anchor.position)) : 0
  played.value = id
    ? Object.values(readHistory())
        .filter((e) => (e.dramaId || '') === id)
        .map((e) => e.episode)
    : []
  const idx = chapters.value.findIndex((c, i) => chapterEpisode(c, i) === currentEpisode.value)
  selectedIdx.value = idx >= 0 ? idx : 0
}

async function loadDetail() {
  const d = drama.value
  if (!d) return
  loading.value = true
  error.value = ''
  try {
    detail.value = await api.detail(d)
  } catch (e: any) {
    detail.value = null
    error.value = e?.message ?? String(e)
  } finally {
    loading.value = false
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
  // PlayRequested 语义：跳 /player 带 drama/chapter/episode/pos —— PlayerView 现为占位页，参数已就绪
  router.push(playerRoute(d, selectedEpisode.value, anchorPos.value, ch))
}

async function downloadCurrent() {
  const d = drama.value
  const ch = selectedChapter.value
  if (!d || !ch || downloading.value) return
  downloading.value = true
  try {
    // Resolve 取 plan（quality=1 高清兜底），plan 原样暂存 —— 真实下载 V2-3 接入，这里不假装成功
    const plan = await api.resolve(d, ch, chapters.value.indexOf(ch), 1)
    const added = enqueueDownloads([
      { dramaId: d.id, title: d.title, chapter: ch as Record<string, any>, episode: selectedEpisode.value, plan: plan as Record<string, any>, addedAt: Date.now() }
    ])
    showToast(
      added
        ? `第${selectedEpisode.value}集已加入本地暂存队列（共 ${queueLength()} 项）。下载队列 V2-3 接入，尚未开始真实下载`
        : `第${selectedEpisode.value}集已在队列中（共 ${queueLength()} 项）`
    )
  } catch (e: any) {
    showToast(`获取播放地址失败：${e?.message ?? e}`)
  } finally {
    downloading.value = false
  }
}

function downloadAll() {
  const d = drama.value
  if (!d || downloading.value || !chapters.value.length) return
  // 全部只登记占位（plan=null）：逐集 Resolve ×N 会锤引擎，地址留待 V2-3 下载器执行时统一拉取
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
      ? `已登记 ${added} 集到本地队列（共 ${queueLength()} 项）。播放地址与真实下载待 V2-3 下载队列接入，尚未开始下载`
      : `本剧已全部在队列中（共 ${queueLength()} 项）`
  )
}

function onFollow() {
  const d = drama.value
  if (!d) return
  // localStorage 兜底。TODO binding: native.Follow/Unfollow（Go watchstore 接线后切换，见终报依赖请求）
  followed.value = toggleFollow(d)
  showToast(followed.value ? `已追剧《${d.title}》（本地记录，追更数据待 Go watchstore 接线）` : `已取消追剧《${d.title}》`)
}

function onWatchedMark() {
  const d = drama.value
  if (!d) return
  watched.value = toggleWatched(d.id)
  showToast(watched.value ? '已标记看过' : '已取消看过标记')
}

/* ---------- 头像区数据 ---------- */

const heatText = computed(() => {
  const m = meta.value
  const parts: string[] = []
  if (m.heat) parts.push(`热度 ${m.heat}`)
  if (m.views) parts.push(`点播 ${m.views}`)
  if (m.onlineDate) parts.push(m.onlineDate)
  if (m.releaseStatus) parts.push(m.releaseStatus)
  return parts
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
      followed.value = isFollowed(drama.value.id)
      watched.value = isWatched(drama.value.id)
      loadDetail()
    }
  }
)

onMounted(() => {
  const raw = String(route.params.id ?? '')
  drama.value = raw ? dramaFromQuery(decodeURIComponent(raw), route.query) : null
  if (drama.value) {
    followed.value = isFollowed(drama.value.id)
    watched.value = isWatched(drama.value.id)
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

</script>

<template>
  <div class="detail">
    <!-- 头区：模糊封面铺底（v1 BlurCoverBackground） -->
    <div v-if="drama" class="hero">
      <div class="hero-bg" aria-hidden="true">
        <img v-if="coverUrl(meta.cover)" :src="coverUrl(meta.cover)" alt="" />
      </div>
      <div class="hero-main">
        <div class="hero-side">
          <button class="btn back" @click="goBack">
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 18l-6-6 6-6" /></svg>
            返回
          </button>
          <div class="cover-box" :class="{ empty: !coverUrl(meta.cover) }">
            <img v-if="coverUrl(meta.cover)" :src="coverUrl(meta.cover)" :alt="meta.title" />
            <div v-else class="cover-empty">{{ (meta.title || drama.id).slice(0, 4) }}</div>
          </div>
        </div>
        <div class="hero-meta">
          <h1 class="title">
            {{ meta.title || '（无标题）' }}
            <span v-if="meta.vip" class="vip-chip">VIP</span>
          </h1>
          <div v-if="heatText.length" class="heat-row">
            <span v-for="h in heatText" :key="h" class="heat-item">{{ h }}</span>
          </div>
          <div v-if="metaTags.length" class="chips tag-row">
            <span v-for="t in metaTags" :key="t" class="chip static">{{ t }}</span>
          </div>
          <p v-if="meta.description" class="desc" :class="{ expanded: descExpanded }" @click="descExpanded = !descExpanded">
            {{ meta.description }}<span v-if="!descExpanded && meta.description.length > 120" class="more">…展开</span>
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
      <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><circle cx="12" cy="12" r="9" /><path d="M12 8v5m0 3.5.01" /></svg>
      <div>详情加载失败：{{ error }}</div>
      <button class="btn" @click="loadDetail()">重试</button>
    </div>
    <template v-else-if="drama">
      <!-- 操作区（v1 detail_layout：播放 primary accent + 下载 success） -->
      <div class="actions">
        <button class="btn primary" :disabled="!chapters.length" @click="goPlay">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="currentColor"><path d="M8 5.5v13l11-6.5z" /></svg>
          {{ selectedEpisode ? `播放第 ${selectedEpisode} 集` : '播放' }}
        </button>
        <button class="btn" :disabled="!chapters.length || downloading" @click="downloadCurrent">
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4v12m0 0 4-4m-4 4-4-4M4 20h16" /></svg>
          {{ downloading ? '处理中…' : '下载当集' }}
        </button>
        <button class="btn" :disabled="!chapters.length || downloading" @click="downloadAll">下载全部（{{ chapters.length }} 集）</button>
        <button class="btn" :class="{ toggled: followed }" @click="onFollow">{{ followed ? '已追剧 ✓' : '＋ 追剧' }}</button>
        <button class="btn" :class="{ toggled: watched }" @click="onWatchedMark">{{ watched ? '已看过 ✓' : '看过' }}</button>
      </div>

      <!-- 选集 -->
      <div class="ep-section card">
        <div class="section-head">
          <h2>选集</h2>
          <span v-if="chapters.length" class="text-muted">共 {{ chapters.length }} 集</span>
          <span v-else class="text-muted">暂无剧集</span>
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

    <div v-if="toast" class="toast">{{ toast }}</div>
  </div>
</template>

<style scoped>
.detail {
  display: flex;
  flex-direction: column;
  min-height: 100%;
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
.vip-chip {
  font-size: 11px;
  font-weight: 600;
  color: var(--guo-vip-fg);
  background: var(--guo-vip-bg);
  border-radius: 4px;
  padding: 2px 7px;
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
</style>
