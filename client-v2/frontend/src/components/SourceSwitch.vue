<script setup lang="ts">
/* SourceSwitch —— v1 listSources 下拉：
   displayName、implemented=false 置灰、adult 源「18+」红徽章、enabled 状态点 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { SourceListItem } from '@/services/types'

const props = defineProps<{
  sources: SourceListItem[]
  modelValue: string
  loading?: boolean
}>()
const emit = defineEmits<{ (e: 'update:modelValue', slug: string): void }>()

const open = ref(false)
const root = ref<HTMLElement | null>(null)

const current = computed(() => props.sources.find((s) => s.slug === props.modelValue))

function toggle() {
  open.value = !open.value
}
function pick(s: SourceListItem) {
  if (!s.implemented) return
  emit('update:modelValue', s.slug)
  open.value = false
}
function onDocClick(e: MouseEvent) {
  if (root.value && !root.value.contains(e.target as Node)) open.value = false
}
onMounted(() => document.addEventListener('click', onDocClick, true))
onBeforeUnmount(() => document.removeEventListener('click', onDocClick, true))
</script>

<template>
  <div ref="root" class="ss">
    <button class="ss-btn" :disabled="loading || !sources.length" @click="toggle">
      <span class="dot" :style="{ background: current?.enabled ? 'var(--guo-success)' : 'var(--muted-2)' }" />
      <span class="ss-name">{{ current ? (current.displayName || current.name) : '选择源' }}</span>
      <span v-if="current?.adult" class="ss-adult">18+</span>
      <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
        <path :d="open ? 'm6 15 6-6 6 6' : 'm6 9 6 6 6-6'" />
      </svg>
    </button>

    <div v-if="open" class="ss-panel card">
      <div v-if="!sources.length" class="ss-empty">源列表加载中…</div>
      <button
        v-for="s in sources"
        :key="s.slug"
        class="ss-item"
        :class="{ disabled: !s.implemented, active: s.slug === modelValue }"
        :disabled="!s.implemented"
        :title="s.implemented ? '' : '源未接入，待 V2-x 实现'"
        @click="pick(s)"
      >
        <span class="dot" :style="{ background: s.enabled ? 'var(--guo-success)' : 'var(--muted-2)' }" />
        <span class="ss-item-name">{{ s.displayName || s.name }}</span>
        <span v-if="s.adult" class="ss-adult">18+</span>
        <span v-if="!s.implemented" class="ss-tag">未接入</span>
      </button>
    </div>
  </div>
</template>

<style scoped>
.ss {
  position: relative;
}
.ss-btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 7px 12px;
  background: var(--button2-bg);
  border: 1px solid var(--button2-border);
  border-radius: var(--radius);
  color: var(--text);
  cursor: pointer;
  font-size: 13px;
}
.ss-btn:hover:not(:disabled) {
  background: var(--button2-hover);
}
.ss-btn:disabled {
  opacity: 0.6;
  cursor: default;
}
.ss-name {
  font-weight: 600;
}
.ss-panel {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  min-width: 240px;
  z-index: 30;
  padding: 6px;
  display: flex;
  flex-direction: column;
  gap: 2px;
  box-shadow: 0 8px 24px var(--guo-shadow);
}
.ss-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border: none;
  border-radius: var(--guo-radius);
  background: transparent;
  color: var(--text);
  cursor: pointer;
  font-size: 13px;
  text-align: left;
}
.ss-item:hover:not(.disabled) {
  background: var(--guo-chip-hover);
}
.ss-item.active {
  background: var(--accent-soft);
}
.ss-item.disabled {
  color: var(--disabled-text);
  cursor: not-allowed;
}
.ss-item-name {
  flex: 1;
  white-space: nowrap;
}
.ss-adult {
  font-size: 10.5px;
  font-weight: 700;
  padding: 2.5px 5px;
  border-radius: 4px;
  background: var(--danger);
  color: var(--on-accent);
}
.ss-tag {
  font-size: 11px;
  color: var(--disabled-text);
}
.ss-empty {
  padding: 14px;
  text-align: center;
  color: var(--muted);
  font-size: 12.5px;
}
</style>
