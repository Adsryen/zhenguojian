<script setup lang="ts">
/* HistoryView —— /history：读 localStorage 'zgj.history'
   （形状 { key: {title,episode,position,duration,ts} }，key=`${dramaId}:${episode}`）。
   分段照 v1 watch.py：今天 / 昨天 / 更早（本地零点分桶），行=标题+集数+进度%+时间。
   点击 → /player 续播参数（PlayerView 现为占位，参数已就绪）。
   注：占位 PlayerView 尚无写入方，故 mock 模式且本地无记录时注入 fixtures 种子并标注示例数据。
   —— 依赖请求：Go watchstore HistoryList 接线后本文件切换数据源（见终报）。 */
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { MOCK_MODE } from '@/services/api'
import type { Drama } from '@/services/types'
import { playerRoute } from '@/services/dramaRef'
import {
  clearHistory,
  dayBucket,
  deleteHistoryEntry,
  formatTime,
  HISTORY_BUCKETS,
  HISTORY_KEY,
  historyKey,
  normTs,
  readHistory,
  splitHistoryKey,
  type HistoryEntry
} from '@/services/local'
import { MOCK_HISTORY_SEED } from '@/services/fixtures'

interface Row {
  key: string
  entry: HistoryEntry
  pct: number
}

const router = useRouter()
const rows = ref<Row[]>([])
const seeded = ref(false)
const toast = ref('')
let toastTimer: number | undefined

function showToast(msg: string) {
  toast.value = msg
  clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => (toast.value = ''), 3200)
}

function pctOf(e: HistoryEntry): number {
  if (!e.duration || e.duration <= 0) return 0
  return Math.min(100, Math.round((Math.max(0, e.position) / e.duration) * 100))
}

function rebuild() {
  const map = readHistory()
  rows.value = Object.entries(map)
    .map(([key, entry]) => ({
      key,
      entry: { ...entry, dramaId: entry.dramaId || splitHistoryKey(key).dramaId },
      pct: pctOf(entry)
    }))
    .sort((a, b) => normTs(b.entry.ts) - normTs(a.entry.ts))
}

/** mock 模式且历史为空：注入种子（写一次 localStorage，让删除/清空等交互真实可用） */
function seedIfMockEmpty() {
  if (!MOCK_MODE || Object.keys(readHistory()).length) return
  const now = new Date()
  const map: Record<string, HistoryEntry> = {}
  for (const s of MOCK_HISTORY_SEED) {
    const d = new Date(now.getFullYear(), now.getMonth(), now.getDate() - s.offsetDays, s.hour, 23)
    map[historyKey(s.id, s.episode)] = {
      title: s.title,
      episode: s.episode,
      position: s.position,
      duration: s.duration,
      ts: d.getTime(),
      dramaId: s.id,
      source: s.id.split(':')[0]
    }
  }
  localStorage.setItem(HISTORY_KEY, JSON.stringify(map))
  seeded.value = true
}

const buckets = computed(() =>
  HISTORY_BUCKETS.map((label, i) => ({
    label,
    items: rows.value.filter((r) => dayBucket(r.entry.ts) === i)
  })).filter((b) => b.items.length)
)

function goResume(r: Row) {
  const e = r.entry
  const id = e.dramaId || splitHistoryKey(r.key).dramaId || ''
  const source = e.source || id.split(':')[0] || ''
  const drama: Drama = { id, source, title: e.title }
  router.push(playerRoute(drama, e.episode, e.position))
}

function removeRow(r: Row) {
  deleteHistoryEntry(r.key)
  rebuild()
  showToast(`已删除《${r.entry.title} 第${r.entry.episode}集》记录`)
}

function onClear() {
  clearHistory()
  seeded.value = false
  rebuild()
  showToast('已清空本地观看历史')
}

function goDiscover() {
  router.push('/discover')
}

onMounted(() => {
  seedIfMockEmpty()
  rebuild()
})

function durText(e: HistoryEntry): string {
  const m = Math.floor(e.duration / 60)
  const s = Math.floor(e.duration % 60)
  return `${m}:${String(s).padStart(2, '0')}`
}

function posText(r: Row): string {
  const isToday = dayBucket(r.entry.ts) === 0
  if (!isToday) return formatTime(r.entry.ts)
  const time = new Date(normTs(r.entry.ts))
  return `${String(time.getHours()).padStart(2, '0')}:${String(time.getMinutes()).padStart(2, '0')}`
}
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">历史</h1>
    <span v-if="seeded" class="chip static sample-chip">VITE_MOCK 示例数据</span>
    <div class="spacer" />
    <button v-if="rows.length" class="btn danger" @click="onClear">清空历史</button>
  </div>

  <div v-if="!rows.length" class="state-block">
    <svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3.5 2" /></svg>
    <div>还没有追剧记录。</div>
    <div class="text-muted hint">去「找剧」挑一部，回到这里就能接着上次的位置继续看。</div>
    <button class="btn primary" @click="goDiscover">去找剧</button>
  </div>

  <div v-else class="hist-list">
    <section v-for="b in buckets" :key="b.label" class="bucket">
      <h2 class="bucket-label">{{ b.label }}</h2>
      <div class="card row-card">
        <div
          v-for="(r, i) in b.items"
          :key="r.key"
          class="hrow"
          :class="{ divided: i > 0 }"
        >
          <button class="hrow-main" @click="goResume(r)">
            <span class="h-title">{{ r.entry.title }}</span>
            <span class="h-sub">
              第 {{ r.entry.episode }} 集
              <template v-if="r.entry.duration > 0"> · 看到 {{ r.pct }}%（{{ durText(r.entry) }}）</template>
            </span>
            <span v-if="r.pct > 0" class="bar"><i :style="{ width: r.pct + '%' }" /></span>
          </button>
          <span class="h-time text-muted">{{ posText(r) }}</span>
          <button class="h-del" title="删除该条记录" @click.stop="removeRow(r)">
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h16M10 11v6m4-6v6M6 7l1 13h10l1-13M9 7V4h6v3" /></svg>
          </button>
        </div>
      </div>
    </section>
    <p class="footnote text-muted">
      历史存在本机 localStorage（zgj.history）；Go watchstore 绑定在 V2-3 接线后切换。
      点击行 → /player 续播（播放页当前为占位，参数已就绪）。
    </p>
  </div>

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
.btn.danger {
  color: var(--danger);
  border-color: var(--danger);
}
.hist-list {
  padding: 4px 24px 12px;
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.bucket-label {
  font-size: 13px;
  color: var(--muted);
  font-weight: 600;
  margin: 0 2px 8px;
}
.row-card {
  padding: 2px 14px;
}
.hrow {
  display: flex;
  align-items: center;
  gap: 10px;
  position: relative;
}
.hrow.divided::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 1px;
  background: var(--border);
}
.hrow-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  align-items: flex-start;
  background: none;
  border: none;
  color: inherit;
  padding: 12px 0 11px;
  cursor: pointer;
  text-align: left;
}
.h-title {
  font-size: 14px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 100%;
}
.h-sub {
  font-size: 12px;
  color: var(--muted);
}
.bar {
  display: block;
  width: min(240px, 45%);
  height: 3px;
  border-radius: 2px;
  background: var(--surface-3);
  overflow: hidden;
}
.bar i {
  display: block;
  height: 100%;
  background: var(--accent);
  border-radius: 2px;
}
.h-time {
  font-size: 11.5px;
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}
.h-del {
  flex-shrink: 0;
  border: none;
  background: none;
  color: var(--muted-2);
  cursor: pointer;
  padding: 6px;
  border-radius: var(--guo-radius);
  display: inline-flex;
}
.hrow:hover .h-del {
  color: var(--danger);
}
.h-del:hover {
  background: var(--chip-hover);
}
.state-block {
  flex: 1;
}
.hint {
  font-size: 12.5px;
  max-width: 340px;
  line-height: 1.6;
}
.footnote {
  font-size: 11.5px;
  line-height: 1.7;
  margin: 2px 2px 8px;
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
