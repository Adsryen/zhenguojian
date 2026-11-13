<script setup lang="ts">
/* HotkeyHelp（V2-F）：播放页快捷键帮助浮层。
   对齐 v1 player.py:861 HotkeyHelpOverlay 语义：整屏遮罩 + 居中卡片，
   点遮罩关闭；?/Esc 再按关闭（父层按键分发处理）；8s 无操作自动收起（本组件定时器）。
   纯展示组件：文案由父层（PlayerView 的 L 常量表）传入，样式走深色 token。 */
import { onBeforeUnmount, onMounted } from 'vue'
import { t } from '@/services/i18n'

interface HotkeyRow {
  keys: string
  desc: string
}

const props = defineProps<{
  title: string
  rows: readonly HotkeyRow[]
  /** 自动收起毫秒数（v1 同款 8s；<=0 关闭自动收） */
  autoCloseMs?: number
}>()

const emit = defineEmits<{ (e: 'close'): void }>()

let timer: number | undefined

onMounted(() => {
  const ms = props.autoCloseMs ?? 8000
  if (ms > 0) timer = window.setTimeout(() => emit('close'), ms)
})

onBeforeUnmount(() => {
  if (timer) clearTimeout(timer)
})
</script>

<template>
  <div class="hk-scrim" @click="emit('close')">
    <div class="hk-card" @click.stop>
      <div class="hk-head">
        <span class="hk-title">{{ title }}</span>
        <button class="hk-x" type="button" :title="t('player.helpClose')" @click="emit('close')">✕</button>
      </div>
      <ul class="hk-rows">
        <li v-for="r in rows" :key="r.keys">
          <span class="hk-keys">{{ r.keys }}</span>
          <span class="hk-desc">{{ r.desc }}</span>
        </li>
      </ul>
      <div class="hk-foot">{{ t('player.helpFooter') }}</div>
    </div>
  </div>
</template>

<style scoped>
.hk-scrim {
  position: fixed;
  inset: 0;
  z-index: 80;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.55);
}
.hk-card {
  width: 340px;
  max-width: calc(100vw - 48px);
  border: 1px solid var(--border);
  border-radius: var(--guo-radius-lg);
  background: var(--surface-2);
  color: var(--text);
  padding: 14px 16px 12px;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.45);
}
.hk-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}
.hk-title {
  font-size: 14px;
  font-weight: 600;
}
.hk-x {
  border: none;
  background: transparent;
  color: var(--muted, #9aa3b2);
  font-size: 14px;
  cursor: pointer;
  padding: 2px 6px;
  border-radius: var(--guo-radius);
}
.hk-x:hover {
  color: var(--danger, #f56c6c);
}
.hk-rows {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.hk-rows li {
  display: flex;
  align-items: baseline;
  gap: 12px;
  font-size: 13px;
}
.hk-keys {
  flex-shrink: 0;
  min-width: 76px;
  font-family: var(--font-mono);
  font-size: 12px;
  padding: 2px 8px;
  border: 1px solid var(--border);
  border-radius: var(--guo-radius);
  background: var(--surface-3);
  color: var(--text);
  text-align: center;
}
.hk-desc {
  color: var(--muted, #9aa3b2);
}
.hk-foot {
  margin-top: 12px;
  padding-top: 8px;
  border-top: 1px solid var(--border);
  font-size: 11px;
  color: var(--muted, #9aa3b2);
}
</style>
