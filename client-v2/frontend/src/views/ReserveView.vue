<script setup lang="ts">
/* ReserveView —— /reserve?tab=calendar|follow
   Tab1 上新日历：v1 calendar_panel.py 结构（日期 chips + 当日行：封面+标题+已上线/「hint」即将上线+简介+播放/预约）。
     日历列表数据源：Go 端暂无日历条目绑定 —— 保留 // TODO binding: native.Reservations（V2-4 接线）。
       mock 模式走 fixtures.mockCalendarDays()（页面标注示例数据）；真实模式明示待接线，不假装有数据。
     预约开关：真值走 stores/watch（Go watchstore Reserve/CancelReservation；降级读 localStorage）。
   Tab2 追更预约：stores/watch.watchlist 的 followed 行（取消追剧 = ToggleFollow 互斥语义）。
   —— watchstore 绑定已接线；日历条目绑定仍为依赖请求（见终报）。 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MOCK_MODE } from '@/services/api'
import type { CalendarItem, Drama } from '@/services/types'
import { resolveCover } from '@/services/covers'
import { detailRoute } from '@/services/dramaRef'
import { formatTime, readReservations } from '@/services/local'
import { useWatchStore } from '@/stores/watch'
import { mockCalendarDays } from '@/services/fixtures'

const route = useRoute()
const router = useRouter()
const watchStore = useWatchStore()

const tab = ref<'calendar' | 'follow'>('calendar')
const toast = ref('')
let toastTimer: number | undefined

function showToast(msg: string) {
  toast.value = msg
  clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => (toast.value = ''), 3200)
}

/* ---------------- 日历 tab ---------------- */
// TODO binding: native.Reservations(date, offset) —— 日历条目接线前真实模式无数据源
const items = ref<CalendarItem[]>([])
const loading = ref(false)
const calError = ref('')
const selectedDate = ref('')

/** 预约状态：watchstore 行标志为准；无行 id 回落本地 zgj.reservations（mock 纯预约项） */
const reservedIds = computed<Record<string, boolean>>(() => {
  const m: Record<string, boolean> = { ...readReservations() }
  for (const r of watchStore.follows) m[r.id] = r.reserved
  return m
})

function ymd(d: Date): string {
  return `${d.getFullYear()}${String(d.getMonth() + 1).padStart(2, '0')}${String(d.getDate()).padStart(2, '0')}`
}
function parseYmd(s: string): Date {
  return new Date(Number(s.slice(0, 4)), Number(s.slice(4, 6)) - 1, Number(s.slice(6, 8)))
}
const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']
function dateLabel(s: string): string {
  const today = ymd(new Date())
  if (s === today) return '今天'
  return WEEKDAYS[parseYmd(s).getDay()]
}

const dates = computed(() => {
  const set = new Set(items.value.map((it) => it.date || '').filter(Boolean))
  return [...set].sort()
})
const dayItems = computed(() => items.value.filter((it) => (it.date || '') === selectedDate.value))

async function loadCalendar() {
  loading.value = true
  calError.value = ''
  try {
    if (MOCK_MODE) {
      await new Promise((r) => setTimeout(r, 250))
      items.value = mockCalendarDays()
    } else {
      items.value = []
      calError.value = '上新日历待 Go 端绑定（// TODO binding: native.Reservations，V2-4 接线）。mock 模式（VITE_MOCK=1）可看示例版式。'
    }
    const today = ymd(new Date())
    selectedDate.value = dates.value.includes(today) ? today : dates.value[0] || today
  } finally {
    loading.value = false
  }
}

async function onReserveClick(it: CalendarItem) {
  const drama = it.drama
  if (!drama?.id) return
  try {
    await watchStore.reserve(drama)
  } catch (e: any) {
    showToast(e?.message ?? '预约操作失败')
  }
}

function goDetail(id: string) {
  const it = items.value.find((x) => x.drama?.id === id)
  if (it?.drama) router.push(detailRoute(it.drama))
}

/* ---------------- 追更 tab ---------------- */
const follows = computed(() => watchStore.followedRows)

async function unfollow(row: (typeof follows.value)[number]) {
  try {
    await watchStore.toggleFollow(row.drama)
  } catch (e: any) {
    showToast(e?.message ?? '取消追剧失败')
  }
}

const followDate = (row: (typeof follows.value)[number]) => formatTime(row.added)

function syncTab() {
  const t = String(route.query.tab ?? '')
  tab.value = t === 'follow' ? 'follow' : 'calendar'
}

watch(() => route.query.tab, syncTab)
onMounted(() => {
  syncTab()
  loadCalendar()
  void watchStore.ensureLoaded().then(() => {
    if (watchStore.error) showToast(watchStore.error)
  })
})

function coverUrl(c?: string): string {
  if (!c) return ''
  return /^(https?:|data:|blob:)/i.test(c) ? c : c.startsWith('/') ? c : `/${c}`
}

/* 封面解析（海报管线）：日历行 + 追更行的 drama 都走 resolveCover —— 真实桥下
   raw cover 可能是签名 HEIC（<img> 直绑必挂），Go 端转码后回 data URL；
   mock/降级返回 fixture 本地 svg 原路径。解析出前显 cover-ph 占位（与现状一致）。 */
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
watch(dayItems, (list) => ensureCovers(list.map((it) => it.drama)), { immediate: true })
watch(follows, (list) => ensureCovers(list.map((r) => r.drama)), { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">预约</h1>
    <div class="tabs">
      <button class="tab" :class="{ active: tab === 'calendar' }" @click="tab = 'calendar'">新剧日历</button>
      <button class="tab" :class="{ active: tab === 'follow' }" @click="tab = 'follow'">追更预约</button>
    </div>
    <div class="spacer" />
    <button v-if="tab === 'calendar'" class="btn" :disabled="loading" @click="loadCalendar">刷新</button>
  </div>

  <!-- 日历 tab -->
  <template v-if="tab === 'calendar'">
    <div v-if="loading" class="state-block">
      <div class="dots">日历加载中…</div>
    </div>
    <div v-else-if="calError" class="state-block">
      <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><rect x="3" y="5" width="18" height="16" rx="2" /><path d="M8 3v4M16 3v4M3 11h18" /></svg>
      <div>{{ calError }}</div>
      <button class="btn" @click="loadCalendar">重试</button>
    </div>
    <template v-else>
      <div class="banner">VITE_MOCK 示例数据，非真实引擎结果</div>
      <div class="chips date-chips">
        <button
          v-for="d in dates"
          :key="d"
          class="chip"
          :class="{ active: selectedDate === d }"
          @click="selectedDate = d"
        >
          {{ dateLabel(d) }}
          <small>{{ d.slice(4, 6) }}-{{ d.slice(6, 8) }}</small>
        </button>
      </div>

      <div class="cal-list">
        <div v-for="it in dayItems" :key="it.drama?.id" class="cal-row card">
          <div class="cal-cover" @click="goDetail(it.drama!.id)">
            <img v-if="resolvedCover(it.drama)" :src="coverUrl(resolvedCover(it.drama))" :alt="it.drama?.title" />
            <div v-else class="cover-ph" />
          </div>
          <div class="cal-body">
            <div class="cal-title-line">
              <button class="cal-title" @click="goDetail(it.drama!.id)">{{ it.drama?.title }}</button>
              <span v-if="it.online" class="status online">已上线</span>
              <span v-else class="status">{{ it.hint || '待定' }} 即将上线</span>
            </div>
            <p class="cal-desc text-muted">{{ it.drama?.description }}</p>
            <div class="cal-foot">
              <span v-if="it.drama?.category" class="chip static">{{ it.drama.category }}</span>
              <div class="spacer" />
              <button v-if="it.online" class="btn primary sm" @click="goDetail(it.drama!.id)">
                <svg viewBox="0 0 24 24" width="13" height="13" fill="currentColor"><path d="M8 5.5v13l11-6.5z" /></svg>
                播放
              </button>
              <button v-else class="btn sm" :class="{ toggled: !!reservedIds[it.drama!.id] }" @click="onReserveClick(it)">
                {{ reservedIds[it.drama!.id] ? '已预约 ✓' : '预约' }}
              </button>
            </div>
          </div>
        </div>
        <div v-if="!dayItems.length" class="state-block inline">
          <div>这一天暂时没有上新。</div>
        </div>
      </div>
    </template>
  </template>

  <!-- 追更 tab -->
  <template v-else>
    <div v-if="watchStore.loading && !follows.length" class="state-block">
      <div class="dots">读取追更列表…</div>
    </div>
    <div v-else-if="!follows.length" class="state-block">
      <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M12 21s-7.5-4.9-9.6-9A5.6 5.6 0 0 1 12 6.7 5.6 5.6 0 0 1 21.6 12c-2.1 4.1-9.6 9-9.6 9z" /></svg>
      <div>还没有追更的剧。</div>
      <div class="text-muted hint">在详情页点「追剧」，这里就会替你盯着更新（真值存 Go watchstore，无桥时降级本地记录）。</div>
      <button class="btn primary" @click="router.push('/discover')">去找剧</button>
    </div>
    <div v-else class="cal-list">
      <div v-for="f in follows" :key="f.id" class="cal-row card">
        <div class="cal-cover" @click="router.push(detailRoute(f.drama))">
          <img v-if="resolvedCover(f.drama)" :src="coverUrl(resolvedCover(f.drama))" :alt="f.drama.title" />
          <div v-else class="cover-ph" />
        </div>
        <div class="cal-body">
          <div class="cal-title-line">
            <button class="cal-title" @click="router.push(detailRoute(f.drama))">{{ f.drama.title }}</button>
            <span class="status">{{ followDate(f) }} 加入</span>
          </div>
          <p class="cal-desc text-muted">{{ f.drama.releaseStatus || f.drama.description }}</p>
          <div class="cal-foot">
            <div class="spacer" />
            <button class="btn sm" @click="router.push(detailRoute(f.drama))">去详情页</button>
            <button class="btn sm danger" @click="unfollow(f)">取消追剧</button>
          </div>
        </div>
      </div>
    </div>
  </template>

  <div v-if="toast" class="toast">{{ toast }}</div>
</template>

<style scoped>
.spacer {
  flex: 1;
}
.tabs {
  display: inline-flex;
  gap: 4px;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: 999px;
  padding: 3px;
  margin-left: 14px;
}
.tab {
  border: none;
  background: none;
  color: var(--muted);
  font-size: 13px;
  padding: 5px 14px;
  border-radius: 999px;
  cursor: pointer;
}
.tab.active {
  background: var(--accent);
  color: var(--on-accent);
  font-weight: 600;
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
  padding: 4px 24px 18px;
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
}
.chip.static {
  cursor: default;
  font-size: 11.5px;
  padding: 2px 9px;
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
