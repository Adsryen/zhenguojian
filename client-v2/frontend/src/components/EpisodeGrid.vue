<script setup lang="ts">
/* EpisodeGrid —— 照 v1 detail_episodes.py + widgets_guo.EpisodeGrid 质感：
   GROUP_SIZE=30 分组 chip 行、44px 按钮、当前集 accent 实心、未播变淡、VIP 角标。 */
import { computed, ref, watch } from 'vue'
import type { Chapter } from '@/services/types'
import { chapterEpisode, chapterVip } from '@/services/dramaRef'

const props = defineProps<{
  chapters: Chapter[]
  /** 当前集号（1 基）；0 表示未选中 */
  current: number
  dramaVip?: boolean | null
  /** 已播过的集号集合（来自 zgj.history），未播的变淡 */
  played?: number[]
  groupSize?: number
}>()

const emit = defineEmits<{
  (e: 'select', chapter: Chapter, index: number, episode: number): void
}>()

const GROUP = computed(() => props.groupSize ?? 30)
const groupCount = computed(() => Math.max(1, Math.ceil(props.chapters.length / GROUP.value)))
const group = ref(0)

function groupOf(episode: number): number {
  if (episode <= 0) return 0
  return Math.min(groupCount.value - 1, Math.floor((episode - 1) / GROUP.value))
}

const playedSet = computed(() => new Set(props.played ?? []))

interface Cell {
  chapter: Chapter
  index: number
  episode: number
  vip: boolean
}

const cells = computed<Cell[]>(() => {
  const from = group.value * GROUP.value
  return props.chapters.slice(from, from + GROUP.value).map((chapter, i) => {
    const index = from + i
    return {
      chapter,
      index,
      episode: chapterEpisode(chapter, index),
      vip: chapterVip(chapter, props.dramaVip)
    }
  })
})

// 集数标签：优先 chapter.title（v1 _extra_title 语义），否则用集号
function cellLabel(c: Cell): string {
  const t = String(c.chapter.title ?? '').trim()
  if (t && t !== String(c.episode)) return t
  return `${c.episode}`
}

watch(
  () => props.current,
  (ep) => {
    if (ep > 0) group.value = groupOf(ep)
  },
  { immediate: true }
)
watch(
  () => props.chapters,
  () => {
    group.value = groupOf(props.current)
  }
)
</script>

<template>
  <div class="ep-wrap">
    <div v-if="groupCount > 1" class="ep-groups chips">
      <button
        v-for="g in groupCount"
        :key="g"
        class="chip"
        :class="{ active: group === g - 1 }"
        @click="group = g - 1"
      >
        {{ (g - 1) * GROUP + 1 }}–{{ Math.min(g * GROUP, chapters.length) }}
      </button>
    </div>

    <div class="ep-grid">
      <button
        v-for="c in cells"
        :key="c.index"
        class="ep-btn"
        :class="{
          current: current > 0 && c.episode === current,
          dim: current > 0 && c.episode !== current && !playedSet.has(c.episode)
        }"
        :title="`${cellLabel(c)}${c.vip ? ' · VIP' : ''}`"
        @click="emit('select', c.chapter, c.index, c.episode)"
      >
        <span v-if="c.vip" class="vip-tag" />
        {{ cellLabel(c) }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.ep-wrap {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.ep-groups.chips {
  padding: 0;
  gap: 6px;
}
.ep-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(64px, 1fr));
  gap: 8px;
}
.ep-btn {
  position: relative;
  height: 44px;
  border-radius: var(--guo-radius);
  border: 1px solid var(--border);
  background: var(--button-bg);
  color: var(--text);
  font-size: 12px;
  line-height: 1.2;
  padding: 0 6px;
  cursor: pointer;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  transition: background 0.12s ease, opacity 0.12s ease, border-color 0.12s ease;
}
.ep-btn:hover {
  background: var(--button-hover);
  border-color: var(--surface-4);
}
.ep-btn.current {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--on-accent);
  font-weight: 600;
}
.ep-btn.dim {
  opacity: 0.5;
}
.ep-btn.current.dim {
  opacity: 1;
}
/* VIP 角标：右上角小三角，v1 是黄点，这里做成贴片角 */
.vip-tag {
  position: absolute;
  top: 0;
  right: 0;
  width: 0;
  height: 0;
  border-style: solid;
  border-width: 0 12px 12px 0;
  border-color: transparent var(--guo-vip-fg) transparent transparent;
}
</style>
