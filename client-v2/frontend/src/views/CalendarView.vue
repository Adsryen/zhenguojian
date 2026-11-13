<script setup lang="ts">
/* CalendarView —— /calendar 追剧日历（V2-2 缺口补齐）
   对齐 v1 calendar_panel.py 日历 tab：日期 chips（今天/周X + M-D）、
   online-first 行列表（封面+标题+状态+简介+分类/更新信息/热度 chips）、
   已上线→播放 + 追剧，未上线→预约/已预约（本地 watchstore 真值，复用
   App.ToggleFollow/Reserve/CancelReservation 绑定）。
   条目点击 → 详情（detailRoute）；已上线行的「播放」→ playerRoute 第 1 集
   （PlayerView bootFromQuery 缺 c 时自取 detail 章节，安全）。
   分页：滚动到底 loadMore（v1 _calendar_scrolled）。 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { MOCK_MODE } from '@/services/api'
import { resolveCover } from '@/services/covers'
import { detailRoute, playerRoute } from '@/services/dramaRef'
import { useCalendarStore, calDateLabel, todayYmd, type CalendarRow } from '@/stores/calendar'
import { t, tf } from '@/services/i18n'

const router = useRouter()
const store = useCalendarStore()
// 页内滚动容器（fillViewport 要知道内容有没有超过一屏）
const scrollRef = ref<HTMLElement | null>(null)

/** v1 calendar_panel.py CHIP_GENDERS：("2",全部) ("1",男频) ("0",女频)；文案走 i18n calendar.* */
const GENDER_CHIPS = computed(() => [
  { v: 2, label: t('calendar.genderAll') },
  { v: 1, label: t('calendar.genderMale') },
  { v: 0, label: t('calendar.genderFemale') }
])
function pickGender(v: number) {
  void store.setGender(v)
}

const actionErrorText = ref('')
let actionErrorTimer: number | undefined
watch(
  () => store.actionError,
  (msg) => {
    if (!msg) return
    actionErrorText.value = msg
    store.actionError = ''
    window.clearTimeout(actionErrorTimer)
    actionErrorTimer = window.setTimeout(() => (actionErrorText.value = ''), 4000)
  }
)

const dates = computed(() => (store.dates.length ? store.dates : [store.date || todayYmd()]))
const onlineRows = computed(() => store.onlineRows)
const pendingRows = computed(() => store.pendingRows)

function coverUrl(c?: string): string {
  if (!c) return ''
  return /^(https?:|data:|blob:)/i.test(c) ? c : c.startsWith('/') ? c : `/${c}`
}

/* 封面解析（海报管线）：行封面走 resolveCover —— 真实桥下 raw cover 可能是签名 HEIC
   （<img> 直绑必挂），Go 端转码后回 data URL；mock/降级返回 fixture 本地 svg 原路径。
   rows 为整组替换（load/loadMore 都重赋 store.rows），watch 一次覆盖两个 tab 的行。 */
const coverMap = ref(new Map<string, string>())
watch(
  () => store.rows,
  (rs) => {
    for (const row of rs) {
      const d = row.drama
      if (!d || !d.cover || coverMap.value.has(d.id)) continue
      void resolveCover(d).then((src) => {
        if (src && coverMap.value.get(d.id) !== src) {
          coverMap.value = new Map(coverMap.value).set(d.id, src)
        }
      })
    }
  },
  { immediate: true }
)
function resolvedCover(row: CalendarRow): string {
  return coverMap.value.get(row.drama.id) || ''
}

function goDetail(row: CalendarRow) {
  router.push(detailRoute(row.drama))
}
function goPlay(row: CalendarRow) {
  router.push(playerRoute(row.drama, 1))
}

/* 滚动监听挂在页内滚动容器 .cal-scroll 上（不是 window）：App.vue 的
   .shell-main 是 overflow:hidden 的 flex 列，window 永不产生滚动，
   旧实现把监听挂在 window 导致「到底加载更多」失效且整页无法滚动。 */
function onScroll(e: Event) {
  const el = e.target as HTMLElement
  if (el && el.scrollHeight - el.scrollTop - el.clientHeight < 160) {
    void store.loadMore().then(() => fillViewport())
  }
}

function onStatusClick() {
  if (!store.retryable) return
  // 首屏就失败（一行都没有）→ 重试首屏；已有内容 → 重试**下一页**，
  // 后者保留已加载项，正是 v1 `failed_page_retries_same_cursor_without_clearing_items`
  void (store.rows.length ? store.retryLoadMore() : store.load(store.date))
}

/* v1 两条测试在这件事上有名有姓：
 *   short_first_pages_fill_viewport_automatically —— 首屏比视口短时自动补一页；
 *   resize_refills_visible_space —— 窗口拉高后剩余空白仍要填满。
 * v2 之前两条都没有：短列表停在半屏，用户得自己滚一下才翻页。 */
let fillQueued = false
function fillViewport() {
  if (fillQueued) return
  fillQueued = true
  requestAnimationFrame(async () => {
    fillQueued = false
    const el = scrollRef.value
    if (!el || !store.hasMore || store.loading || store.stalled) return
    // 只在"内容还没超过容器"时补，超过就交给滚动
    if (el.scrollHeight <= el.clientHeight + 8) {
      await store.loadMore()
      // 补一页后若仍不满，下一轮 onMounted/watch 触发或用户滚动会继续；
      // 这里不写递归 while，是为了避免服务端返回空页时把请求打满。
      if (store.needsFill) fillViewport()
    }
  })
}

let ro: ResizeObserver | null = null
onMounted(() => {
  if (!store.loadedOnce) void store.load(store.date).then(() => fillViewport())
  // 用 ResizeObserver 而不是 window resize：页内滚动容器的高度还会被
  // 侧栏折叠、缩放等改变，那些情况 window 不一定发 resize。
  if (typeof ResizeObserver !== 'undefined' && scrollRef.value) {
    ro = new ResizeObserver(() => fillViewport())
    ro.observe(scrollRef.value)
  }
})
onBeforeUnmount(() => {
  ro?.disconnect()
  ro = null
})
</script>

<template>
  <div class="cal-page">
  <div class="page-head">
    <h1 class="page-title">{{ t('calendar.title') }}</h1>
    <div class="spacer" />
    <div class="date-nav">
      <button class="btn icon" :disabled="store.loading" :title="t('calendar.prevDay')" @click="store.prevDay()">
        <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M15 5l-7 7 7 7" /></svg>
      </button>
      <strong class="date-cur">{{ calDateLabel(store.date) }} {{ store.date.slice(4, 6) }}-{{ store.date.slice(6, 8) }}</strong>
      <button class="btn icon" :disabled="store.loading" :title="t('calendar.nextDay')" @click="store.nextDay()">
        <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M9 5l7 7-7 7" /></svg>
      </button>
      <button class="btn sm" :disabled="store.loading" @click="store.goToday()">{{ t('calendar.today') }}</button>
    </div>
    <button class="btn" :disabled="store.loading" @click="store.load(store.date)">{{ t('calendar.refresh') }}</button>
  </div>

  <div ref="scrollRef" class="cal-scroll" @scroll.passive="onScroll">
  <div v-if="MOCK_MODE" class="banner">{{ t('banner.mock') }}</div>

  <div v-if="actionErrorText" class="banner warn">{{ actionErrorText }}</div>

  <div v-if="store.loading && !store.rows.length" class="state-block">
    <div class="dots">{{ t('calendar.loading') }}</div>
  </div>
  <div v-else-if="store.error && !store.rows.length" class="state-block clickable" @click="store.load(store.date)">
    <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><rect x="3" y="5" width="18" height="16" rx="2" /><path d="M8 3v4M16 3v4M3 11h18" /></svg>
    <div>{{ tf('calendar.loadFailed', { e: store.error }) }}</div>
  </div>

  <template v-else>
    <!-- v1 calendar_panel.py 男频/女频胶囊：切频道触发重载（store.setGender → api.calendar 第三形参） -->
    <div class="chips gender-chips" :data-gender="store.gender">
      <button
        v-for="g in GENDER_CHIPS"
        :key="g.v"
        class="chip"
        :class="{ active: store.gender === g.v }"
        :disabled="store.loading"
        @click="pickGender(g.v)"
      >
        {{ g.label }}
      </button>
    </div>

    <div class="chips date-chips">
      <button
        v-for="d in dates"
        :key="d"
        class="chip"
        :class="{ active: store.date === d }"
        :disabled="store.loading"
        @click="store.selectDate(d)"
      >
        {{ calDateLabel(d) }}
        <small>{{ d.slice(4, 6) }}-{{ d.slice(6, 8) }}</small>
      </button>
    </div>

    <div class="cal-list">
      <div v-for="row in onlineRows" :key="row.id" class="cal-row card">
        <div class="cal-cover" @click="goDetail(row)">
          <img v-if="resolvedCover(row)" :src="coverUrl(resolvedCover(row))" :alt="row.drama.title" />
          <div v-else class="cover-ph" />
          <span v-if="row.badge" class="cover-badge">{{ row.badge }}</span>
        </div>
        <div class="cal-body">
          <div class="cal-title-line">
            <button class="cal-title" @click="goDetail(row)">{{ row.drama.title }}</button>
            <span class="status online">{{ t('calendar.online') }}</span>
          </div>
          <p class="cal-desc text-muted">{{ row.drama.description }}</p>
          <div class="cal-foot">
            <span v-if="row.drama.category" class="chip static">{{ row.drama.category }}</span>
            <span v-if="row.publishText" class="chip static">{{ row.publishText }}</span>
            <span v-if="row.note" class="chip static heat">{{ row.note }}</span>
            <div class="spacer" />
            <button class="btn sm" :class="{ toggled: store.isFollowed(row) }" @click="store.toggleFollow(row)">
              {{ store.isFollowed(row) ? t('calendar.followed') : t('calendar.follow') }}
            </button>
            <button class="btn primary sm" @click="goPlay(row)">
              <svg viewBox="0 0 24 24" width="13" height="13" fill="currentColor"><path d="M8 5.5v13l11-6.5z" /></svg>
              {{ t('calendar.play') }}
            </button>
          </div>
        </div>
      </div>

      <div v-if="pendingRows.length" class="section-title text-muted">{{ t('calendar.pending') }}</div>
      <div v-for="row in pendingRows" :key="row.id" class="cal-row card">
        <div class="cal-cover" @click="goDetail(row)">
          <img v-if="resolvedCover(row)" :src="coverUrl(resolvedCover(row))" :alt="row.drama.title" />
          <div v-else class="cover-ph" />
          <span v-if="row.badge" class="cover-badge">{{ row.badge }}</span>
        </div>
        <div class="cal-body">
          <div class="cal-title-line">
            <button class="cal-title" @click="goDetail(row)">{{ row.drama.title }}</button>
            <span class="status pending">{{ row.hint || t('calendar.soon') }}</span>
          </div>
          <p class="cal-desc text-muted">{{ row.drama.description }}</p>
          <div class="cal-foot">
            <span v-if="row.drama.category" class="chip static">{{ row.drama.category }}</span>
            <span v-if="row.note" class="chip static">{{ row.note }}</span>
            <div class="spacer" />
            <button class="btn sm" :class="{ toggled: store.isReserved(row) }" @click="store.toggleReserve(row)">
              {{ store.isReserved(row) ? t('calendar.reserved') : t('calendar.reserve') }}
            </button>
          </div>
        </div>
      </div>

      <div v-if="!store.rows.length" class="state-block inline">
        <div>{{ t('calendar.emptyDay') }}</div>
      </div>
      <!-- ⚠ 第五十四轮：这行原来是个**纯 div**，
           而文案写的是「加载失败：…（点此重试）」——
           也就是界面承诺了一个不存在的行为（与第三十四轮"预约提醒尚未接通"
           那一族同形：话说出去了，事没做）。v1 `calendar_panel.py:165` 的注释
           明确写着"底部状态行；失败时点一下重试"，所以这里补的是**对齐 v1**，
           不是我新发明的交互。
           停摆态（连续多页没有新剧）也走同一个入口：v1 `_retry_new()`
           重试时请求的是**下一页**，不是原地重复。 -->
      <button
        v-else-if="store.statusText"
        class="list-status text-muted"
        :class="{ clickable: store.retryable }"
        :disabled="!store.retryable"
        data-testid="cal-status"
        @click="onStatusClick"
      >{{ store.statusText }}</button>
    </div>
  </template>
  </div>
  </div>
</template>

<style scoped>
.cal-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.cal-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
}
.spacer {
  flex: 1;
}
.date-nav {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  margin-right: 10px;
}
.date-cur {
  font-size: 14px;
  color: var(--text);
  min-width: 86px;
  text-align: center;
}
.btn.icon {
  width: 30px;
  height: 30px;
  padding: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 4px 24px 0;
}
.date-chips {
  padding-top: 2px;
}
.gender-chips {
  padding-top: 10px;
}
.date-chips .chip small {
  color: var(--muted-2);
  margin-left: 5px;
  font-size: 11px;
}
.date-chips .chip.active small {
  color: inherit;
  opacity: 0.8;
}
.cal-list {
  padding: 12px 24px 18px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.section-title {
  font-size: 12.5px;
  margin-top: 6px;
}
.cal-row {
  display: flex;
  gap: 14px;
  padding: 14px;
}
.cal-cover {
  position: relative;
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
.cover-badge {
  position: absolute;
  top: 4px;
  left: 4px;
  font-size: 10.5px;
  line-height: 1;
  padding: 3px 5px;
  border-radius: 4px;
  background: var(--accent);
  color: var(--on-accent);
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
.status.pending {
  color: var(--warning, #d7a441);
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
.chip.static {
  cursor: default;
  font-size: 11.5px;
  padding: 2px 9px;
}
.chip.static.heat {
  color: var(--muted-2);
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
.state-block {
  flex: 1;
}
.state-block.inline {
  flex: none;
  padding: 48px 0;
}
.state-block.clickable {
  cursor: pointer;
}
.list-status.clickable {
  cursor: pointer;
  text-decoration: underline dotted;
}
.list-status:disabled {
  cursor: default;
}
.list-status {
  text-align: center;
  font-size: 12.5px;
  padding: 10px 0 2px;
}
.dots {
  font-size: 13px;
  color: var(--muted);
}
</style>
