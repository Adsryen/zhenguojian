<script setup lang="ts">
import { t, tf } from '@/services/i18n'
/* SeasonMenu —— 详情页「全 N 季 ▾」切换按钮（v1 main.py:241-270 set_seasons +
   1547-1586 后台系列查询 移植）。规则逐字对齐 v1：
   - 数据= catalog(query=base_title||title, lane="series:auto", page=1, source)
     的 items 经 series.group 收拢（同季留集数多者）；
   - rows ≤ 1 时整体隐藏（v1 setVisible(False)）；
   - 当前季条目前缀「✓ 」（v1 把勾写进文字而非 checkable，避免双指示器）；
   - 菜单文案 = season_label + 「 · N 集」（有集数才带）；
   - 900s 系列缓存（v1 _series_cache TTL），同剧重进不重查。
   选择非当前季 → emit choose(drama)，由 DetailView 走 detailRoute 导航。 */
import { computed, onBeforeUnmount, ref, watch } from 'vue'

import { api } from '@/services/api'
import { baseTitle, groupSeasons, seasonLabel, seriesKey, type SeasonRow } from '@/services/series'
import type { Drama } from '@/services/types'

const props = defineProps<{ drama: Drama | null }>()
const emit = defineEmits<{ (e: 'choose', drama: Drama): void }>()

const rows = ref<SeasonRow[]>([])
const open = ref(false)
const CACHE_MS = 900_000
const cache = new Map<string, { at: number; rows: SeasonRow[] }>()
let token = 0

const visible = computed(() => rows.value.length > 1)
const buttonLabel = computed(() => tf('season.all', { n: rows.value.length })) // v1 setText("全 %d 季 ▾")，单位固定「季」

async function lookup(drama: Drama | null) {
  const my = ++token
  rows.value = []
  open.value = false
  if (!drama?.title) return
  const title = String(drama.title).trim()
  const key = seriesKey(title)
  if (!key) return
  const source = String(drama.source || 'hongguo')
  const cacheKey = `${source}:${key}`
  const hit = cache.get(cacheKey)
  if (hit && Date.now() - hit.at < CACHE_MS) {
    if (my === token) rows.value = hit.rows
    return
  }
  try {
    const res = await api.catalog({ source, query: baseTitle(title) || title, page: 1, lane: 'series:auto' })
    const grouped = groupSeasons(res.items ?? [], title)
    cache.set(cacheKey, { at: Date.now(), rows: grouped })
    if (my === token) rows.value = grouped
  } catch {
    if (my === token) rows.value = [] // 查不到=隐藏按钮（v1 后台失败静默）
  }
}

function rowLabel(row: SeasonRow): string {
  const label = seasonLabel(row.season, row.unit)
  return row.episodes > 0 ? tf('season.episodes', { label, n: row.episodes }) : label
}

function pick(row: SeasonRow) {
  open.value = false
  if (String(row.drama.id ?? '') === String(props.drama?.id ?? '')) return
  emit('choose', row.drama)
}

function onDocClick(e: MouseEvent) {
  if (!open.value) return
  if (!(e.target as HTMLElement).closest('.season-root')) open.value = false
}
document.addEventListener('click', onDocClick, true)
onBeforeUnmount(() => document.removeEventListener('click', onDocClick, true))

watch(() => props.drama?.id, () => void lookup(props.drama), { immediate: true })
defineExpose({ lookup })
</script>

<template>
  <div v-if="visible" class="season-root">
    <button class="season-btn" type="button" :title="t('season.title')" @click.stop="open = !open">
      {{ buttonLabel }}
    </button>
    <div v-if="open" class="season-menu" role="menu">
      <button
        v-for="row in rows"
        :key="String(row.drama.id)"
        class="season-item"
        :class="{ current: String(row.drama.id ?? '') === String(props.drama?.id ?? '') }"
        type="button"
        role="menuitem"
        @click.stop="pick(row)"
      >
        {{ (String(row.drama.id ?? '') === String(props.drama?.id ?? '') ? '✓ ' : '') + rowLabel(row) }}
      </button>
    </div>
  </div>
</template>

<style scoped>
/* 观感对齐 v1 season_button（ghost 小按钮）与 SourceSwitch 弹层样式族 */
.season-root { position: relative; display: inline-flex; }
.season-btn {
  border: 1px solid var(--guo-border, #262c3a);
  background: var(--surface-2);
  color: var(--guo-text, #d7dce5);
  border-radius: 6px;
  padding: 4px 10px;
  font-size: 13px;
  cursor: pointer;
  white-space: nowrap;
}
.season-btn:hover { background: var(--guo-surface-3); }
.season-menu {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  z-index: 60;
  min-width: 180px;
  max-height: 320px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  padding: 4px;
  border: 1px solid var(--guo-border, #262c3a);
  border-radius: 6px;
  background: var(--surface-2);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
}
.season-item {
  border: 0;
  background: transparent;
  color: var(--guo-text, #d7dce5);
  text-align: left;
  padding: 6px 8px;
  border-radius: 4px;
  font-size: 13px;
  cursor: pointer;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.season-item:hover { background: var(--guo-surface-3); }
.season-item.current { color: var(--guo-accent, #4a83ff); }
</style>
