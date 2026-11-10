<script setup lang="ts">
/* SearchGroups —— 搜索页「全部来源」分组结果列表（扇出模式专用，不动 PosterGrid）：
   - 组顺序 = orderedSources 顺序（父级构造 groups 时保证）；组头 = 站源名 + 18+ 徽章 + 计数
   - 组内缩略卡网格：不嵌套 PosterGrid 的虚拟滚动（避免页面级双滚动条），样式复用 tokens
   - 封面走 resolveCover：真机 HEIC→dataURL / mock SVG 两态兼容；失败/未回退标题首字占位
   - 组级状态：loading→骨架瓦片（先完成的组先出内容，不等齐）；done→网格或空提示；
     error→浅灰单行「该源搜索失败：原因」+ 该源重试，不影响其他组 */
import { ref, watch } from 'vue'
import type { Drama } from '@/services/types'
import { resolveCover } from '@/services/covers'
import { t } from '@/services/i18n'
import type { SearchGroup } from '@/stores/searchAll'

const props = defineProps<{
  groups: SearchGroup[]
  /** 全组皆败（父级判定并等扇出收尾后传入）：顶部整体 error 条，组内失败行仍各自保留 */
  allFailed?: boolean
}>()

const emit = defineEmits<{
  (e: 'select', drama: Drama): void
  (e: 'retry', slug: string): void
  (e: 'retry-all'): void
}>()

/* 封面解析：同 PosterGrid 手法——解析出可用 src 才渲染 <img>，Map/Set 整体替换触发更新 */
const coverSrc = ref(new Map<string, string>())
const broken = ref(new Set<string>())
function setCoverSrc(id: string, src: string) {
  if (coverSrc.value.get(id) === src) return
  coverSrc.value = new Map(coverSrc.value).set(id, src)
}
function markBroken(id: string) {
  if (broken.value.has(id)) return
  broken.value = new Set(broken.value).add(id)
}
function ensureCover(d: Drama) {
  if (!d || !d.cover || coverSrc.value.has(d.id) || broken.value.has(d.id)) return
  void resolveCover(d).then((src) => {
    if (src) setCoverSrc(d.id, src)
    else markBroken(d.id)
  })
}
// 组内容到达（status/items 变化）即预取该组封面；ensureCover 自带去重守卫
watch(
  () => props.groups.map((g) => `${g.slug}:${g.status}:${g.items.length}`).join('|'),
  () => {
    for (const g of props.groups) for (const d of g.items) ensureCover(d)
  },
  { immediate: true }
)

function fallbackChar(d: Drama) {
  return d.title?.trim()?.[0] || '剧'
}
</script>

<template>
  <div class="sa-scroll">
    <div v-if="allFailed" class="sa-allfail">
      <span>{{ t('search.allFailed') }}</span>
      <button class="sa-retry" @click="emit('retry-all')">{{ t('search.retry') }}</button>
    </div>
    <section v-for="g in groups" :key="g.slug" class="sa-group" :data-slug="g.slug">
      <header class="sa-head">
        <h2 class="sa-name">{{ g.name }}</h2>
        <span v-if="g.adult" class="sa-badge adult">18+</span>
        <span v-if="g.status === 'loading'" class="sa-count">{{ t('search.groupLoading') }}</span>
        <span v-else-if="g.status === 'done' && g.items.length" class="sa-count">
          {{ g.items.length }}{{ t('search.groupCountSuffix') }}
        </span>
      </header>

      <div v-if="g.status === 'loading'" class="sa-grid">
        <div v-for="n in 12" :key="n" class="sa-tile sa-ph">
          <div class="sa-cover" />
        </div>
      </div>

      <div v-else-if="g.status === 'error'" class="sa-note fail">
        <span>{{ t('search.groupFailed') }}{{ g.error }}</span>
        <button class="sa-retry" @click="emit('retry', g.slug)">{{ t('search.groupRetry') }}</button>
      </div>

      <div v-else-if="!g.items.length" class="sa-note empty">{{ t('search.groupEmpty') }}</div>

      <div v-else class="sa-grid">
        <div
          v-for="(d, i) in g.items"
          :key="d.id + '#' + i"
          class="sa-tile sa-card"
          role="button"
          tabindex="0"
          @click="emit('select', d)"
          @keyup.enter="emit('select', d)"
        >
          <div class="sa-cover">
            <img
              v-if="coverSrc.get(d.id) && !broken.has(d.id)"
              :src="coverSrc.get(d.id)"
              :alt="d.title"
              loading="lazy"
              decoding="async"
              @error="markBroken(d.id)"
            />
            <div v-else class="sa-fallback">{{ fallbackChar(d) }}</div>
            <div class="sa-badges">
              <span v-if="d.vip === true" class="sa-badge vip">VIP</span>
              <span v-if="d.adult || (d.tags || []).includes('18+')" class="sa-badge adult">18+</span>
            </div>
          </div>
          <div class="sa-title" :title="d.title">{{ d.title }}</div>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.sa-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 2px 24px 24px;
}
.sa-allfail {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 10px 0 2px;
  padding: 10px 14px;
  font-size: 13px;
  border: 1px solid var(--border);
  border-radius: var(--guo-radius);
  background: var(--surface);
  color: var(--danger);
}
.sa-group {
  margin-bottom: 10px;
}
.sa-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 0 8px;
}
.sa-name {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
}
.sa-count {
  font-size: 12px;
  color: var(--muted-2);
}
.sa-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(132px, 1fr));
  gap: 14px;
}
.sa-tile {
  min-width: 0;
  border-radius: var(--guo-radius);
}
.sa-card {
  cursor: pointer;
  outline: none;
}
.sa-card:focus-visible .sa-cover,
.sa-card:hover .sa-cover {
  border-color: var(--accent);
  box-shadow: 0 6px var(--guo-shadow-blur) var(--guo-shadow);
}
.sa-cover {
  position: relative;
  aspect-ratio: 1 / 1.4;
  border-radius: var(--guo-radius);
  overflow: hidden;
  border: 1px solid var(--border);
  background: var(--surface-3);
  transition: box-shadow 0.15s, border-color 0.15s;
}
.sa-cover img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.sa-fallback {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 34px;
  font-weight: 700;
  color: var(--muted-2);
  background: linear-gradient(160deg, var(--surface-3), var(--surface));
}
.sa-ph .sa-cover {
  animation: sa-pulse 1.2s ease-in-out infinite;
}
@keyframes sa-pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.55;
  }
}
.sa-badges {
  position: absolute;
  top: 6px;
  left: 6px;
  display: flex;
  gap: 4px;
}
.sa-badge {
  font-size: 10.5px;
  font-weight: 700;
  line-height: 1;
  padding: 3px 5px;
  border-radius: 4px;
  backdrop-filter: blur(4px);
}
.sa-badge.vip {
  background: var(--guo-vip-bg);
  color: var(--guo-vip-fg);
}
.sa-badge.adult {
  background: var(--danger);
  color: var(--on-accent);
}
.sa-title {
  margin-top: 6px;
  font-size: 12.5px;
  line-height: 17px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.sa-note {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
  color: var(--muted);
  padding: 6px 0 2px;
}
.sa-note.empty {
  color: var(--muted-2);
}
.sa-retry {
  border: none;
  background: none;
  color: var(--accent);
  cursor: pointer;
  font-size: 12px;
}
</style>
