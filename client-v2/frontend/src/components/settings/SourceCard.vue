<script setup lang="ts">
/* SourceCard —— V2-4 站源卡片（照 v1 settings_widgets.SourceCard 语义）：
   displayName、implemented=false 置灰「未接入」、adult 红 18+ 徽章、
   GuoSwitch 风格自绘开关。纯展示：异步同步逻辑与失败回滚在 SettingsView。 */
import { computed } from 'vue'
import type { SourceListItem } from '@/services/types'
import { t } from '@/services/i18n'

const props = defineProps<{
  source: SourceListItem
  busy?: boolean
  error?: string
}>()

const emit = defineEmits<{
  (e: 'toggle', slug: string, next: boolean): void
}>()

const label = computed(() => props.source.displayName || props.source.name)
const on = computed(() => props.source.enabled && props.source.implemented)
const note = computed(() =>
  !props.source.implemented
    ? t('settings.sources.notImplemented')
    : props.source.adult
      ? t('settings.sources.adultNote')
      : props.source.slug
)

function onClick() {
  if (!props.source.implemented || props.busy) return
  emit('toggle', props.source.slug, !props.source.enabled)
}
</script>

<template>
  <div class="sc" :class="{ disabled: !source.implemented }">
    <div class="sc-main">
      <div class="sc-text">
        <div class="sc-name" :title="source.slug">
          {{ label }}
          <span v-if="source.adult" class="sc-adult">18+</span>
        </div>
        <div class="sc-note">{{ note }}</div>
      </div>
      <button
        class="sc-switch"
        :class="{ on }"
        role="switch"
        :aria-checked="on"
        :aria-label="label"
        :disabled="!source.implemented || busy"
        @click="onClick"
      >
        <span class="sc-knob" />
      </button>
    </div>
    <div v-if="error" class="sc-error">{{ t('settings.sources.retry') }}：{{ error }}</div>
    <div v-if="busy" class="sc-busy">…</div>
  </div>
</template>

<style scoped>
.sc {
  background: var(--guo-chip-bg);
  border: 1px solid var(--guo-border);
  border-radius: var(--guo-radius);
  padding: 10px 12px;
  transition: border-color 0.12s, background 0.12s, opacity 0.12s;
}
.sc:hover:not(.disabled) {
  background: var(--guo-chip-hover);
  border-color: var(--accent);
}
.sc.disabled {
  background: var(--surface-2);
  border-color: var(--border);
}
.sc-main {
  display: flex;
  align-items: center;
  gap: 10px;
}
.sc-text {
  flex: 1;
  min-width: 0;
}
.sc-name {
  font-weight: 600;
  font-size: 13.5px;
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  display: flex;
  align-items: center;
  gap: 6px;
}
.sc.disabled .sc-name {
  color: var(--disabled-text);
}
.sc-adult {
  flex-shrink: 0;
  font-size: 10.5px;
  font-weight: 700;
  padding: 2.5px 5px;
  border-radius: 4px;
  background: var(--danger);
  color: var(--on-accent);
}
.sc-note {
  margin-top: 3px;
  font-size: 11px;
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.sc.disabled .sc-note {
  color: var(--disabled-text);
}

/* GuoSwitch 风格：自绘轨道 + 滑动 knob */
.sc-switch {
  position: relative;
  flex-shrink: 0;
  width: 36px;
  height: 19px;
  padding: 0;
  border-radius: 999px;
  border: 1px solid var(--guo-border);
  background: var(--surface-4);
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}
.sc-switch.on {
  background: var(--accent);
  border-color: var(--accent);
}
.sc-switch:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
.sc-switch:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
.sc-knob {
  position: absolute;
  top: 1.5px;
  left: 2px;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--on-accent);
  transition: left 0.15s ease;
  pointer-events: none;
}
.sc-switch.on .sc-knob {
  left: 19px;
}

.sc-error {
  margin-top: 6px;
  font-size: 11px;
  color: var(--danger);
  line-height: 1.4;
}
.sc-busy {
  margin-top: 4px;
  font-size: 11px;
  color: var(--muted);
}
</style>
