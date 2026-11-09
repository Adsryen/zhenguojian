<script setup lang="ts">
/* CalendarView —— /calendar 追剧日历（V2-2 缺口补齐）
   对齐 v1 calendar_panel.py 日历 tab：日期 chips（今天/周X + M-D）、
   online-first 行列表（封面+标题+状态+简介+分类/更新信息/热度 chips）、
   已上线→播放 + 追剧，未上线→预约/已预约（本地 watchstore 真值，复用
   App.ToggleFollow/Reserve/CancelReservation 绑定）。
   条目点击 → 详情（detailRoute）；已上线行的「播放」→ playerRoute 第 1 集
   （PlayerView bootFromQuery 缺 c 时自取 detail 章节，安全）。
   分页：滚动到底 loadMore（v1 _calendar_scrolled）。 */
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { MOCK_MODE } from '@/services/api'
import { detailRoute, playerRoute } from '@/services/dramaRef'
import { useCalendarStore, calDateLabel, todayYmd, type CalendarRow } from '@/stores/calendar'

const router = useRouter()
const store = useCalendarStore()

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

function goDetail(row: CalendarRow) {
  router.push(detailRoute(row.drama))
}
function goPlay(row: CalendarRow) {
  router.push(playerRoute(row.drama, 1))
}

function onScroll() {
  if (window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 160) {
    void store.loadMore()
  }
}

onMounted(() => {
  if (!store.loadedOnce) void store.load(store.date)
  window.addEventListener('scroll', onScroll, { passive: true })
})
onBeforeUnmount(() => window.removeEventListener('scroll', onScroll))
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">追剧日历</h1>
    <div class="spacer" />
    <div class="date-nav">
      <button class="btn icon" :disabled="store.loading" title="前一天" @click="store.prevDay()">
        <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M15 5l-7 7 7 7" /></svg>
      </button>
      <strong class="date-cur">{{ calDateLabel(store.date) }} {{ store.date.slice(4, 6) }}-{{ store.date.slice(6, 8) }}</strong>
      <button class="btn icon" :disabled="store.loading" title="后一天" @click="store.nextDay()">
        <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M9 5l7 7-7 7" /></svg>
      </button>
      <button class="btn sm" :disabled="store.loading" @click="store.goToday()">今天</button>
    </div>
    <button class="btn" :disabled="store.loading" @click="store.load(store.date)">刷新</button>
  </div>

  <div v-if="MOCK_MODE" class="banner">VITE_MOCK 示例数据，非真实引擎结果</div>

  <div v-if="actionErrorText" class="banner warn">{{ actionErrorText }}</div>

  <div v-if="store.loading && !store.rows.length" class="state-block">
    <div class="dots">加载上新日历…</div>
  </div>
  <div v-else-if="store.error && !store.rows.length" class="state-block clickable" @click="store.load(store.date)">
    <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><rect x="3" y="5" width="18" height="16" rx="2" /><path d="M8 3v4M16 3v4M3 11h18" /></svg>
    <div>加载失败：{{ store.error }}（点此重试）</div>
  </div>

  <template v-else>
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
          <img v-if="coverUrl(row.drama.cover)" :src="coverUrl(row.drama.cover)" :alt="row.drama.title" />
          <div v-else class="cover-ph" />
          <span v-if="row.badge" class="cover-badge">{{ row.badge }}</span>
        </div>
        <div class="cal-body">
          <div class="cal-title-line">
            <button class="cal-title" @click="goDetail(row)">{{ row.drama.title }}</button>
            <span class="status online">已上线</span>
          </div>
          <p class="cal-desc text-muted">{{ row.drama.description }}</p>
          <div class="cal-foot">
            <span v-if="row.drama.category" class="chip static">{{ row.drama.category }}</span>
            <span v-if="row.publishText" class="chip static">{{ row.publishText }}</span>
            <span v-if="row.note" class="chip static heat">{{ row.note }}</span>
            <div class="spacer" />
            <button class="btn sm" :class="{ toggled: store.isFollowed(row) }" @click="store.toggleFollow(row)">
              {{ store.isFollowed(row) ? '已追 ✓' : '追剧' }}
            </button>
            <button class="btn primary sm" @click="goPlay(row)">
              <svg viewBox="0 0 24 24" width="13" height="13" fill="currentColor"><path d="M8 5.5v13l11-6.5z" /></svg>
              播放
            </button>
          </div>
        </div>
      </div>

      <div v-if="pendingRows.length" class="section-title text-muted">待上线</div>
      <div v-for="row in pendingRows" :key="row.id" class="cal-row card">
        <div class="cal-cover" @click="goDetail(row)">
          <img v-if="coverUrl(row.drama.cover)" :src="coverUrl(row.drama.cover)" :alt="row.drama.title" />
          <div v-else class="cover-ph" />
          <span v-if="row.badge" class="cover-badge">{{ row.badge }}</span>
        </div>
        <div class="cal-body">
          <div class="cal-title-line">
            <button class="cal-title" @click="goDetail(row)">{{ row.drama.title }}</button>
            <span class="status pending">{{ row.hint || '即将上线' }}</span>
          </div>
          <p class="cal-desc text-muted">{{ row.drama.description }}</p>
          <div class="cal-foot">
            <span v-if="row.drama.category" class="chip static">{{ row.drama.category }}</span>
            <span v-if="row.note" class="chip static">{{ row.note }}</span>
            <div class="spacer" />
            <button class="btn sm" :class="{ toggled: store.isReserved(row) }" @click="store.toggleReserve(row)">
              {{ store.isReserved(row) ? '已预约 ✓' : '预约' }}
            </button>
          </div>
        </div>
      </div>

      <div v-if="!store.rows.length" class="state-block inline">
        <div>这一天暂时没有上新。</div>
      </div>
      <div v-else-if="store.statusText" class="list-status text-muted">{{ store.statusText }}</div>
    </div>
  </template>
</template>

<style scoped>
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
