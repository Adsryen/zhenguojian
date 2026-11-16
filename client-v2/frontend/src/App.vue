<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import NavRail from '@/components/NavRail.vue'
import { useAppStore } from '@/stores/app'
import { setupContextMenu } from '@/services/contextMenu' // V2-6 右键菜单：全局一处挂载
import { installFollowCheckTestSeam, startFollowCheck, stopFollowCheck } from '@/services/followCheck' // R23 后台追剧检查
import { api } from '@/services/api'
import { t, tf } from '@/services/i18n'
import { cleanReleaseBody } from '@/services/updatenotes'
import type { UpdateAvailableEvent } from '@/services/types'

const store = useAppStore()
setupContextMenu()

/* ── V-UPD 全局顶部更新横幅（update:available 事件驱动；无事件永不出条） ── */
const updBanner = ref<UpdateAvailableEvent | null>(null)

/* 手动检查更新的结果通知（update:notify，Go 侧 app.go：托盘「检查更新」走
   CheckManual → 原生信息框失败时（非 Windows 恒失败）回落该事件，payload
   {title, text}）。第九十五轮对账补上：此前前端**没有监听**，用户在 Linux
   上点托盘检查更新后结果静默丢失。与 updBanner 区分：这是「查完了告诉你」，
   自动 8s 消失，不常驻、不挂操作按钮。 */
const updNotify = ref<{ title: string; text: string } | null>(null)
let notifyTimer: number | undefined
function onUpdNotify(payload: { title?: string; text?: string }) {
  if (!payload?.text) return
  updNotify.value = { title: payload.title ?? '', text: payload.text }
  window.clearTimeout(notifyTimer)
  notifyTimer = window.setTimeout(() => { updNotify.value = null }, 8000)
}

/* 横幅摘要：release body 常是 markdown（CI 自动生成的资产对照表），直接截 60 字
   会露出「## | 文件 | 用途 |」噪音——先净化再截。 */
const updBannerSum = computed(() => {
  const body = updBanner.value?.body ?? ''
  if (!body) return ''
  const text = cleanReleaseBody(body, 200).replace(/\s+/g, ' ').trim()
  return text.length > 90 ? text.slice(0, 90) + '…' : text
})
let offBus: (() => void) | undefined
let offNotify: (() => void) | undefined

function onUpdAvailable(payload: UpdateAvailableEvent) {
  if (!payload?.tag) return
  updBanner.value = payload
}

onMounted(() => {
  void api.updateStartup().catch(() => undefined) // 懒触发启动自动检查（真实桥 goroutine / mock 定时器）
  // v1 main.py:714-726：追剧后台检查在**主窗口**建定时器（启动 60s 后先跑一次，之后每 1800s）。
  // 所以挂在这里（App 级）而不是某个视图，切页不会打断它；卸载时清掉，避免热重载叠加。
  startFollowCheck()
  installFollowCheckTestSeam() // 仅测试缝，产品路径不依赖它
  window.addEventListener('zgj:update-available', ((e: CustomEvent<UpdateAvailableEvent>) => onUpdAvailable(e.detail)) as EventListener)
  window.addEventListener('zgj:update-notify', ((e: CustomEvent<{ title?: string; text?: string }>) => onUpdNotify(e.detail)) as EventListener)
  const EventsOn = (window as any).runtime?.EventsOn ?? (window as any).wails?.EventsOn
  if (typeof EventsOn === 'function') {
    const off = EventsOn('update:available', onUpdAvailable)
    const offN = EventsOn('update:notify', onUpdNotify)
    if (typeof off === 'function') offBus = off
    if (typeof offN === 'function') offNotify = offN
  }
})
onBeforeUnmount(() => {
  offBus?.()
  offNotify?.()
  stopFollowCheck()
  window.clearTimeout(notifyTimer)
})

function updGoSettings() {
  window.location.hash = '#/settings'
}
async function updSkipVersion() {
  const tag = updBanner.value?.tag
  updBanner.value = null
  if (tag) await api.updateSettingsSet({ ignoredTag: tag }).catch(() => undefined)
}
</script>

<template>
  <div class="shell" :class="{ 'nav-collapsed': store.navCollapsed }">
    <NavRail />
    <main class="shell-main">
      <div v-if="updBanner" class="upd-banner" data-testid="update-banner">
        <span class="upd-banner-text">
          {{ tf('update.banner', { tag: updBanner.tag }) }}
          <em v-if="updBannerSum" class="upd-banner-sum">{{ updBannerSum }}</em>
        </span>
        <button class="upd-banner-btn" data-testid="update-banner-go" @click="updGoSettings">{{ t('update.banner.go') }}</button>
        <button class="upd-banner-btn upd-banner-ghost" data-testid="update-banner-skip" @click="updSkipVersion">{{ t('update.banner.skip') }}</button>
      </div>
      <div v-if="updNotify" class="upd-notify" data-testid="update-notify">
        <span class="upd-notify-text" :title="updNotify.title">{{ updNotify.text }}</span>
      </div>
      <router-view />
    </main>
  </div>
</template>

<style scoped>
.shell {
  display: flex;
  height: 100%;
  overflow: hidden;
}
.shell-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  /* 页面自带滚动容器时这里永不出条；忘写的页面从裁切变可滚，兜底 */
  overflow-y: auto;
}
.upd-banner {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 5px 14px;
  font-size: 12.5px;
  line-height: 1.5;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.upd-banner-text {
  flex: 1;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.upd-banner-sum {
  font-style: normal;
  color: var(--muted-2, #7a7a85);
  margin-left: 6px;
}
.upd-banner-btn {
  flex-shrink: 0;
  font-size: 12px;
  padding: 2px 10px;
  border-radius: 6px;
  border: 1px solid var(--border);
  cursor: pointer;
}
.upd-banner-ghost {
  color: var(--guo-text, #d7dce5);
  background: transparent;
}
/* 手动检查结果通知：比横幅更安静（无按钮、自动消失），不抢「有新版本」横幅的位置 */
.upd-notify {
  padding: 4px 14px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--muted-2, #7a7a85);
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.upd-notify-text {
  display: block;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
