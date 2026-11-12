<script setup lang="ts">
/* ReserveView —— /reserve?tab=follow：追更列表（v1 watch.py follow 页对齐，J 轮）
   ① 旧日历 tab 已合并至 /calendar 实页（CalendarView）：本页检测 route.query.tab，
      非 follow（含 /reserve 裸路径与深链接 ?tab=calendar）一律 router.replace('/calendar')。
   ② 行内按钮（v1 watch.py:266-290 底栏动作逐行化）：
      「继续播放」仅对有历史进度（watch store history 行，episode>0）的行显示，
        点击 → playerRoute(drama, episode, position)，与 HistoryView.goResume 同路径同参数语义；
      「标记已看」→ watch store toggleSeen（v1 _mark_watched；已看态可再点撤销，
        按钮翻「已看 ✓」，真值 Go seen-library / mock 落 localStorage）；
      去详情页 / 取消追剧（原有，保留）。
   ③ 站源筛选 chips（v1 watch.py:387-421 _refresh_chips）：按 rows 里出现的源去重
      （drama.source，缺失回落 id 前缀，排除 local —— v1 _source_group），排序对齐
      设置页 orderedSources；「全部」+ 各源 displayName（app store，拿不到显 name/slug）；
      仅 >1 源时显示（v1 chip_bar.setVisible）；纯前端派生过滤，不发请求。
   —— 日历条目数据面早已整体移交 CalendarView/stores/calendar，并且**已接真桥**：
      stores/calendar.ts:107 透传 Go Calendar(date, offset, gender)（bindings_calendar.go:43）。
      旧注释里那句 `TODO binding: native.Reservations` 会让人以为还欠一个绑定，其实不欠：
      reserved 旗标走 watchstore 行（calendar.ts loadFlags），本页也没有日历数据面。 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { Drama, HistoryRow, WatchRow } from '@/services/types'
import { resolveCover } from '@/services/covers'
import { releaseStatusText, t, tf } from '@/services/i18n'
import { detailRoute, playerRoute } from '@/services/dramaRef'
import { formatTime } from '@/services/local'
import { useWatchStore } from '@/stores/watch'
import { useAppStore } from '@/stores/app'

const route = useRoute()
const router = useRouter()
const watchStore = useWatchStore()
const appStore = useAppStore()

/* 文案已收敛至 i18n.ts reserve.*（原组件内 L 表 + 模板内联中文）；t/tf 随 locale 响应 */
const toast = ref('')
let toastTimer: number | undefined

function showToast(msg: string) {
  toast.value = msg
  clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => (toast.value = ''), 3200)
}

/* ---------------- ① 日历旧路径 redirect ---------------- */
function enforceRoute() {
  if (String(route.query.tab ?? '') === 'follow') return
  void router.replace('/calendar')
}
watch(() => route.query.tab, enforceRoute)

/* ---------------- 列表与站源 chips ---------------- */
const follows = computed(() => watchStore.followedRows)

/** v1 _source_group：drama.source → id 前缀兜底（'local' 不算源） */
function sourceGroupOf(d: Drama): string {
  const s = String(d.source || '').trim().toLowerCase()
  if (s) return s
  const i = String(d.id || '').indexOf(':')
  if (i > 0) {
    const p = d.id.slice(0, i).toLowerCase()
    if (p !== 'local') return p
  }
  return ''
}

/** rows 里出现过的源（去重 + 设置页源序，未知源按 slug 排尾） */
const sourceChips = computed<string[]>(() => {
  const present: string[] = []
  for (const r of follows.value) {
    const g = sourceGroupOf(r.drama)
    if (g && !present.includes(g)) present.push(g)
  }
  const order = appStore.orderedSources.map((s) => String(s.slug).toLowerCase())
  return [...present].sort((a, b) => {
    const ia = order.includes(a) ? order.indexOf(a) : order.length
    const ib = order.includes(b) ? order.indexOf(b) : order.length
    return ia - ib || a.localeCompare(b)
  })
})

const sourceFilter = ref<string | null>(null)
/** v1：只剩一个源时筛选无意义，chips 隐藏且过滤复位 */
const showChips = computed(() => sourceChips.value.length > 1)

watch(sourceChips, (list) => {
  if (sourceFilter.value && !list.includes(sourceFilter.value)) sourceFilter.value = null
})

function chipLabel(slug: string): string {
  const s = appStore.sources.find((x) => String(x.slug).toLowerCase() === slug)
  return s?.displayName || s?.name || slug
}

const visibleFollows = computed(() =>
  showChips.value && sourceFilter.value
    ? follows.value.filter((r) => sourceGroupOf(r.drama) === sourceFilter.value)
    : follows.value
)

/* ---------------- ② 行内按钮 ---------------- */
/** 每部剧最新一条历史（watchstore 每剧一行，防御性取 max(updated)；episode≤0 不算进度） */
const historyById = computed<Record<string, HistoryRow>>(() => {
  const m: Record<string, HistoryRow> = {}
  for (const r of watchStore.history) {
    const id = r?.drama?.id
    if (!id || !(Number(r.episode) > 0)) continue
    if (!m[id] || Number(r.updated) > Number(m[id].updated)) m[id] = r
  }
  return m
})

/** 「继续播放」可见性 = 有历史进度行（v1 双击行→_open_current 的按钮化） */
const hasProgress = (row: WatchRow) => !!historyById.value[row.id]

function watchedEpText(row: WatchRow): string {
  const h = historyById.value[row.id]
  return h ? tf('reserve.watchedEp', { n: h.episode }) : ''
}

function goResume(row: WatchRow) {
  const h = historyById.value[row.id]
  if (!h) return
  // 与 HistoryView.goResume 同路径：/player?d=&ep=&pos=（drama 用追更行的完整对象）
  router.push(playerRoute(row.drama, h.episode, h.position))
}

async function onMarkSeen(row: WatchRow) {
  try {
    await watchStore.toggleSeen(row.id)
  } catch (e: any) {
    showToast(e?.message ?? t('reserve.markSeenFail'))
  }
}

async function unfollow(row: WatchRow) {
  try {
    await watchStore.toggleFollow(row.drama)
  } catch (e: any) {
    showToast(e?.message ?? t('reserve.unfollowFail'))
  }
}

const followDate = (row: WatchRow) => formatTime(row.added)

onMounted(() => {
  enforceRoute()
  void watchStore.ensureLoaded().then(() => {
    if (watchStore.error) showToast(watchStore.error)
  })
  void watchStore.loadSeen()
  void appStore.loadSources()
})

function coverUrl(c?: string): string {
  if (!c) return ''
  return /^(https?:|data:|blob:)/i.test(c) ? c : c.startsWith('/') ? c : `/${c}`
}

/* 封面解析（海报管线）：追更行的 drama 走 resolveCover —— 真实桥下
   raw cover 可能是签名 HEIC（<img> 直绑必挂），Go 端转码后回 data URL；
   mock/降级返回 fixture 本地 svg 原路径。解析出前显 cover-ph 占位。 */
const coverMap = ref(new Map<string, string>())
function ensureCovers(list: (Drama | undefined | null)[]) {
  for (const d of list) {
    if (!d || !d.cover || coverMap.value.has(d.id)) continue
    void resolveCover(d).then((src) => {
      if (src && coverMap.value.get(d.id) !== src) {
        coverMap.value = new Map(coverMap.value).set(d.id, src)
      }
    })
  }
}
function resolvedCover(d?: Drama | null): string {
  return (d && coverMap.value.get(d.id)) || ''
}
watch(follows, (list) => ensureCovers(list.map((r) => r.drama)), { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">{{ t('reserve.pageTitle') }}</h1>
    <div class="spacer" />
    <button class="btn" :disabled="watchStore.loading" @click="void watchStore.load(true)">{{ t('reserve.refresh') }}</button>
  </div>

  <!-- 后台检查结论（R23）：v1 打在状态栏，这里同样是一句话提示，不打断操作 -->
  <div v-if="watchStore.checkHint" class="text-muted hint" role="status" data-testid="follow-check-hint">
    {{ watchStore.checkHint }}
  </div>

  <div v-if="watchStore.loading && !follows.length" class="state-block">
    <div class="dots">{{ t('reserve.loading') }}</div>
  </div>
  <div v-else-if="!follows.length" class="state-block">
    <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M12 21s-7.5-4.9-9.6-9A5.6 5.6 0 0 1 12 6.7 5.6 5.6 0 0 1 21.6 12c-2.1 4.1-9.6 9-9.6 9z" /></svg>
    <div>{{ t('reserve.emptyAll') }}</div>
    <div class="text-muted hint">{{ t('reserve.emptyHint') }}</div>
    <button class="btn primary" @click="router.push('/discover')">{{ t('reserve.findDrama') }}</button>
  </div>
  <template v-else>
    <!-- ③ 站源筛选 chips（v1 _refresh_chips：>1 源才显示） -->
    <div v-if="showChips" class="chips source-chips" data-testid="source-chips">
      <button class="chip" :class="{ active: !sourceFilter }" @click="sourceFilter = null">{{ t('reserve.filterAll') }}</button>
      <button
        v-for="s in sourceChips"
        :key="s"
        class="chip"
        :class="{ active: sourceFilter === s }"
        @click="sourceFilter = s"
      >
        {{ chipLabel(s) }}
      </button>
    </div>

    <div class="cal-list">
      <div v-for="f in visibleFollows" :key="f.id" class="cal-row card">
        <div class="cal-cover" @click="router.push(detailRoute(f.drama))">
          <img v-if="resolvedCover(f.drama)" :src="coverUrl(resolvedCover(f.drama))" :alt="f.drama.title" />
          <div v-else class="cover-ph" />
        </div>
        <div class="cal-body">
          <div class="cal-title-line">
            <button class="cal-title" @click="router.push(detailRoute(f.drama))">{{ f.drama.title }}</button>
            <span class="status">{{ tf('reserve.added', { d: followDate(f) }) }}</span>
            <span v-if="watchedEpText(f)" class="status online">{{ watchedEpText(f) }}</span>
          </div>
          <p class="cal-desc text-muted">{{ releaseStatusText(f.drama.releaseStatus) || f.drama.description }}</p>
          <div class="cal-foot">
            <div class="spacer" />
            <button v-if="hasProgress(f)" class="btn primary sm" :title="watchedEpText(f)" @click="goResume(f)">
              <svg viewBox="0 0 24 24" width="13" height="13" fill="currentColor"><path d="M8 5.5v13l11-6.5z" /></svg>
              {{ t('reserve.resume') }}
            </button>
            <button class="btn sm" :class="{ toggled: watchStore.isSeen(f.id) }" @click="onMarkSeen(f)">
              {{ watchStore.isSeen(f.id) ? t('reserve.seenOn') : t('reserve.markSeen') }}
            </button>
            <button class="btn sm" @click="router.push(detailRoute(f.drama))">{{ t('reserve.detail') }}</button>
            <button class="btn sm danger" @click="unfollow(f)">{{ t('reserve.unfollow') }}</button>
          </div>
        </div>
      </div>
      <div v-if="!visibleFollows.length" class="state-block inline">
        <div>{{ t('reserve.emptyFiltered') }}</div>
      </div>
    </div>
  </template>

  <div v-if="toast" class="toast">{{ toast }}</div>
</template>

<style scoped>
.spacer {
  flex: 1;
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 10px 24px 0;
}
.cal-list {
  padding: 12px 24px 18px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.cal-row {
  display: flex;
  gap: 14px;
  padding: 14px;
}
.cal-cover {
  width: 84px;
  height: 126px; /* 2:3，v1 日历小封面比例 */
  flex-shrink: 0;
  border-radius: var(--guo-radius);
  overflow: hidden;
  background: var(--cover-bg);
  cursor: pointer;
}
.cal-cover img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.cover-ph {
  width: 100%;
  height: 100%;
}
.cal-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 7px;
}
.cal-title-line {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.cal-title {
  border: none;
  background: none;
  color: var(--text);
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
  padding: 0;
  text-align: left;
}
.cal-title:hover {
  color: var(--accent);
}
.status {
  font-size: 11.5px;
  color: var(--muted);
  background: var(--surface-3);
  padding: 2px 8px;
  border-radius: 4px;
}
.status.online {
  color: var(--success);
  background: var(--accent-soft);
}
.cal-desc {
  font-size: 12.5px;
  line-height: 1.6;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  margin: 0;
}
.cal-foot {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: auto;
  flex-wrap: wrap;
}
.btn.sm {
  height: 30px;
  padding: 0 12px;
  font-size: 12.5px;
}
.btn.toggled {
  border-color: var(--success);
  color: var(--success);
}
.btn.danger {
  color: var(--danger);
  border-color: var(--danger);
}
.state-block {
  flex: 1;
}
.state-block.inline {
  flex: none;
  padding: 48px 0;
}
.hint {
  font-size: 12.5px;
  max-width: 380px;
  line-height: 1.6;
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
