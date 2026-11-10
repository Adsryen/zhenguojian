<script setup lang="ts">
/* SettingsView —— V2-4 设置页。分组照 v1 settings_panel.py：
   外观（主题/语言）· 站源开关 · 播放默认 · 网络与下载 · 存储与关于。
   主题即时生效（html[data-theme] + theme.ts），语言即时生效（i18n locale ref）。
   站源开关唯一落点 api.setSourceEnabled（→ App.SetSourceEnabled），失败行内红字并回滚；
   播放/网络在核心绑定（ResourceSettings/SaveResourceSettings）就绪前仅存 localStorage，
   带「本地暂存，核心接线后生效」角标——不做假保存成功。 */
import { computed, onMounted, reactive, ref, watch } from 'vue'
import SourceGrid from '@/components/settings/SourceGrid.vue'
import { api, MOCK_MODE } from '@/services/api'
import { locale, setLocale, t } from '@/services/i18n'
import { applyTheme, loadTheme, type ThemeMode } from '@/styles/theme'
import { useAppStore } from '@/stores/app'

const store = useAppStore()

/* ---------------- 外观 ---------------- */
const themeMode = ref<ThemeMode>(loadTheme())
const themeOptions: Array<{ value: ThemeMode; labelKey: string }> = [
  { value: 'dark', labelKey: 'settings.theme.dark' },
  { value: 'light', labelKey: 'settings.theme.light' },
  { value: 'auto', labelKey: 'settings.theme.auto' }
]
function pickTheme(mode: ThemeMode) {
  themeMode.value = mode
  applyTheme(mode) // 即时：写 html[data-theme] + 持久化 + auto 挂系统监听
}
const localeOptions: Array<{ value: 'zh' | 'en'; label: string }> = [
  { value: 'zh', label: '中文' },
  { value: 'en', label: 'English' }
]

/* ---------------- 站源开关 ---------------- */
const toggleMeta = reactive<Record<string, { busy: boolean; err: string }>>({})
function meta(slug: string) {
  if (!toggleMeta[slug]) toggleMeta[slug] = { busy: false, err: '' }
  return toggleMeta[slug]
}
const lastSync = ref('')

async function onToggle(slug: string, next: boolean) {
  const m = meta(slug)
  m.busy = true
  m.err = ''
  try {
    await api.setSourceEnabled(slug, next)
    const row = store.sources.find((s) => s.slug === slug)
    if (row) row.enabled = next // 成功才落 UI：失败路径不触碰状态 = 天然回滚
    lastSync.value = `${t('settings.sources.synced')}${slug} → ${next ? 'ON' : 'OFF'}`
  } catch (e: any) {
    m.err = e?.message ?? String(e)
  } finally {
    m.busy = false
  }
}

/** 拖拽落位提交：store 写 state + localStorage（静默持久化，无 toast） */
function onReorder(slugs: string[]) {
  store.setSourceOrder(slugs)
}

/* ---------------- 播放默认（本地暂存） ---------------- */
interface PlaybackPrefs {
  speed: number
  quality: string
  danmaku: boolean
}
const PLAYBACK_KEY = 'zgj.' + 'player' // 拼接式键名：防脱敏管道损坏字面量
const playbackDefaults: PlaybackPrefs = { speed: 1.0, quality: 'auto', danmaku: true }

function loadJson<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key)
    return raw ? { ...fallback, ...JSON.parse(raw) } : fallback
  } catch {
    return fallback
  }
}
function saveJson(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value))
  } catch {
    /* 写入失败：本会话内仍生效 */
  }
}

const playback = ref<PlaybackPrefs>(loadJson(PLAYBACK_KEY, playbackDefaults))
watch(playback, (v) => {
  saveJson(PLAYBACK_KEY, v)
  if (!MOCK_MODE) api.setSpeed(v.speed).catch(() => { /* 未在播放：作为默认值本机生效即可 */ })
}, { deep: true })

const speedChoices = [0.5, 0.75, 1.0, 1.25, 1.5, 2.0, 3.0]
const speedLabel = (s: number) => (s === Math.trunc(s) ? s.toFixed(1) : String(s)) + 'x'
const qualityChoices: Array<{ value: string; labelKey?: string; label?: string }> = [
  { value: 'auto', labelKey: 'settings.quality.auto' },
  { value: '1080p', label: '1080P' },
  { value: '720p', label: '720P' },
  { value: '540p', label: '540P' }
]

/* ---------------- 网络与下载（本地暂存） ---------------- */
interface NetworkPrefs {
  proxyMode: string
  proxyUrl: string
  catalogConcurrency: number
  intervalMs: number
  downloadConcurrency: number
  downloadBySource: boolean
}
const NETWORK_KEY = 'zgj.' + 'network' // 同上
const networkDefaults: NetworkPrefs = {
  proxyMode: 'auto',
  proxyUrl: '',
  catalogConcurrency: 3,
  intervalMs: 250,
  downloadConcurrency: 2,
  downloadBySource: false
}
const network = ref<NetworkPrefs>(loadJson(NETWORK_KEY, networkDefaults))
const networkStatus = ref('')
async function loadNetwork() {
  try {
    const cur = await api.resourceSettings()
    network.value = {
      proxyMode: cur.proxyMode || 'auto',
      proxyUrl: cur.proxyUrl || '',
      catalogConcurrency: cur.catalogConcurrency ?? 3,
      intervalMs: cur.catalogIntervalMs ?? 250,
      downloadConcurrency: cur.downloadConcurrency ?? 2,
      downloadBySource: !!cur.downloadBySource
    }
  } catch {
    /* 核心不可达：沿用本机暂存值 */
  }
}
const proxyModes = [
  { value: 'auto', labelKey: 'settings.network.proxy.auto' },
  { value: 'direct', labelKey: 'settings.network.proxy.direct' },
  { value: 'manual', labelKey: 'settings.network.proxy.manual' }
]

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, Math.round(v)))

async function saveNetwork() {
  const fixed: NetworkPrefs = {
    ...network.value,
    proxyUrl: network.value.proxyUrl.trim(),
    catalogConcurrency: clamp(network.value.catalogConcurrency || 1, 1, 6),
    intervalMs: clamp(network.value.intervalMs ?? 0, 0, 5000),
    downloadConcurrency: clamp(network.value.downloadConcurrency || 1, 1, 6)
  }
  try {
    await api.saveResourceSettings({
      proxyMode: fixed.proxyMode,
      proxyUrl: fixed.proxyUrl,
      catalogConcurrency: fixed.catalogConcurrency,
      catalogIntervalMs: fixed.intervalMs,
      downloadConcurrency: fixed.downloadConcurrency,
      downloadBySource: fixed.downloadBySource
    })
  } catch (e: any) {
    networkStatus.value = e?.message ?? String(e) // 核心中文校验错误直接展示，不假装成功
    return
  }
  network.value = fixed
  saveJson(NETWORK_KEY, fixed) // mock/降级路径留本机快照，便于演示还原
  networkStatus.value = t('settings.network.saved')
}

/* ---------------- 存储与关于（只读） ---------------- */
const APP_VERSION = '2.0.0-shell'
const dataDir = ref('…')
const coreOk = ref<boolean | null>(null)
const commitText = ref('…')

const sourcesHint = computed(() =>
  MOCK_MODE ? t('settings.sources.hint.mock') : t('settings.sources.hint')
)

onMounted(async () => {
  await store.loadSources()
  try {
    dataDir.value = await api.dataPath()
  } catch {
    dataDir.value = t('settings.storage.readFail')
  }
  try {
    await api.ping()
    coreOk.value = true
  } catch {
    coreOk.value = false
  }
  await loadNetwork()
  try {
    const bi = await api.buildInfo()
    commitText.value = bi.revision && bi.revision !== 'unknown'
      ? `${bi.revision.slice(0, 12)}${bi.commitTime ? ' · ' + bi.commitTime : ''}${bi.modified ? '（含未提交改动）' : ''}`
      : '未知（构建未注入版本信息）'
  } catch {
    commitText.value = '未知'
  }
})
</script>

<template>
  <div class="page-head set-head">
    <div>
      <h1 class="page-title">{{ t('settings.title') }}</h1>
      <div class="set-sub text-muted">{{ t('settings.subtitle') }}</div>
    </div>
  </div>

  <div class="set-body">
    <!-- ① 外观 -->
    <section class="card sec">
      <div class="sec-head">
        <span class="sec-title">{{ t('settings.appearance') }}</span>
      </div>
      <div class="sec-hint">{{ t('settings.appearance.hint') }}</div>
      <div class="field">
        <label class="field-label">{{ t('settings.theme') }}</label>
        <div class="field-control chip-row">
          <button
            v-for="opt in themeOptions"
            :key="opt.value"
            class="chip"
            :class="{ active: themeMode === opt.value }"
            @click="pickTheme(opt.value)"
          >
            {{ t(opt.labelKey) }}
          </button>
        </div>
      </div>
      <div class="field">
        <label class="field-label">{{ t('settings.language') }}</label>
        <div class="field-control chip-row">
          <button
            v-for="opt in localeOptions"
            :key="opt.value"
            class="chip"
            :class="{ active: locale === opt.value }"
            @click="setLocale(opt.value)"
          >
            {{ opt.label }}
          </button>
        </div>
      </div>
    </section>

    <!-- ② 站源开关 -->
    <section class="card sec">
      <div class="sec-head">
        <span class="sec-title">{{ t('settings.sources') }}</span>
        <span v-if="MOCK_MODE" class="badge badge-warn">{{ t('settings.mockBadge') }}</span>
      </div>
      <div class="sec-hint">
        {{ sourcesHint }}
        <span class="sec-drag-hint">{{ t('settings.sources.dragHint') }}</span>
        <template v-if="lastSync"><br />{{ lastSync }}</template>
      </div>
      <div v-if="store.sourcesLoading && !store.sources.length" class="sec-hint">…</div>
      <div v-else-if="store.sourcesError" class="sec-error-line">{{ store.sourcesError }}</div>
      <SourceGrid
        v-else
        :sources="store.orderedSources"
        :meta="meta"
        @toggle="onToggle"
        @reorder="onReorder"
      />
    </section>

    <!-- ③ 播放默认 -->
    <section class="card sec">
      <div class="sec-head">
        <span class="sec-title">{{ t('settings.playback') }}</span>
        <span class="badge badge-warn">{{ t('settings.staged') }}</span>
      </div>
      <div class="sec-hint">{{ t('settings.playback.hint') }}</div>
      <div class="field">
        <label class="field-label">{{ t('settings.playback.speed') }}</label>
        <div class="field-control">
          <select v-model.number="playback.speed" class="ctl">
            <option v-for="s in speedChoices" :key="s" :value="s">{{ speedLabel(s) }}</option>
          </select>
        </div>
      </div>
      <div class="field">
        <label class="field-label">{{ t('settings.playback.quality') }}</label>
        <div class="field-control">
          <select v-model="playback.quality" class="ctl">
            <option v-for="q in qualityChoices" :key="q.value" :value="q.value">
              {{ q.labelKey ? t(q.labelKey) : q.label }}
            </option>
          </select>
        </div>
      </div>
      <div class="field">
        <label class="field-label">{{ t('settings.playback.danmaku') }}</label>
        <div class="field-control">
          <button
            class="sw"
            :class="{ on: playback.danmaku }"
            role="switch"
            :aria-checked="playback.danmaku"
            @click="playback.danmaku = !playback.danmaku"
          >
            <span class="sw-knob" />
          </button>
        </div>
      </div>
    </section>

    <!-- ④ 网络与下载 -->
    <section class="card sec">
      <div class="sec-head">
        <span class="sec-title">{{ t('settings.network') }}</span>
        <span class="badge badge-warn">{{ t('settings.staged') }}</span>
      </div>
      <div class="sec-hint">{{ t('settings.network.hint') }}</div>
      <div class="field">
        <label class="field-label">{{ t('settings.network.proxyMode') }}</label>
        <div class="field-control">
          <select v-model="network.proxyMode" class="ctl">
            <option v-for="m in proxyModes" :key="m.value" :value="m.value">{{ t(m.labelKey) }}</option>
          </select>
        </div>
      </div>
      <div class="field">
        <label class="field-label">{{ t('settings.network.proxyUrl') }}</label>
        <div class="field-control grow">
          <input
            v-model="network.proxyUrl"
            class="ctl"
            type="text"
            :placeholder="t('settings.network.proxyUrl.ph')"
          />
        </div>
      </div>
      <div class="field">
        <label class="field-label">{{ t('settings.network.catalogConcurrency') }}</label>
        <div class="field-control with-unit">
          <input v-model.number="network.catalogConcurrency" class="ctl num" type="number" min="1" max="6" />
          <span class="unit">{{ t('settings.network.lanesUnit') }}</span>
        </div>
      </div>
      <div class="field">
        <label class="field-label">{{ t('settings.network.interval') }}</label>
        <div class="field-control with-unit">
          <input v-model.number="network.intervalMs" class="ctl num" type="number" min="0" max="5000" step="250" />
          <span class="unit">{{ t('settings.network.msUnit') }}</span>
        </div>
      </div>
      <div class="field">
        <label class="field-label">{{ t('settings.network.downloadConcurrency') }}</label>
        <div class="field-control with-unit">
          <input v-model.number="network.downloadConcurrency" class="ctl num" type="number" min="1" max="6" />
          <span class="unit">{{ t('settings.network.lanesUnit') }}</span>
        </div>
      </div>
      <div class="field">
        <label class="field-label">{{ t('settings.network.bySource') }}</label>
        <div class="field-control">
          <button
            class="sw"
            :class="{ on: network.downloadBySource }"
            role="switch"
            :aria-checked="network.downloadBySource"
            @click="network.downloadBySource = !network.downloadBySource"
          >
            <span class="sw-knob" />
          </button>
        </div>
      </div>
      <div class="field">
        <label class="field-label"></label>
        <div class="field-control save-row">
          <button class="btn" @click="saveNetwork">{{ t('settings.network.save') }}</button>
          <span v-if="networkStatus" class="save-ok">{{ networkStatus }}</span>
        </div>
      </div>
    </section>

    <!-- ⑤ 存储与关于 -->
    <section class="card sec">
      <div class="sec-head">
        <span class="sec-title">{{ t('settings.storage') }}</span>
      </div>
      <div class="sec-hint">{{ t('settings.storage.hint') }}</div>
      <div class="kv">
        <span class="kv-k">{{ t('settings.storage.version') }}</span>
        <span class="kv-v">真果鉴 v{{ APP_VERSION }}</span>
      </div>
      <div class="kv">
        <span class="kv-k">{{ t('settings.storage.core') }}</span>
        <span class="kv-v">
          <span class="dot" :style="{ background: coreOk ? 'var(--guo-success)' : 'var(--muted-2)' }" />
          {{ coreOk === null ? '…' : coreOk ? t('settings.storage.coreConnected') : t('settings.storage.coreDisconnected') }}
        </span>
      </div>
      <div class="kv">
        <span class="kv-k">{{ t('settings.storage.commit') }}</span>
        <span class="kv-v muted">{{ t('settings.storage.commitUnknown') }}</span>
      </div>
      <div class="kv">
        <span class="kv-k">{{ t('settings.storage.dataDir') }}</span>
        <span class="kv-v selectable">{{ dataDir }}</span>
      </div>
      <div class="kv">
        <span class="kv-k">打包提交</span>
        <span class="kv-v selectable">{{ commitText }}</span>
      </div>
    </section>
  </div>
</template>

<style scoped>
.set-head {
  padding-bottom: 6px;
}
.set-sub {
  margin-top: 4px;
  font-size: 12.5px;
}
.set-body {
  flex: 1;
  overflow-y: auto;
  padding: 6px 24px 28px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.sec {
  padding: 14px 16px 16px;
}
.sec-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-left: 9px;
  border-left: 3px solid var(--accent);
}
.sec-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--text);
}
.badge {
  font-size: 11px;
  padding: 1px 8px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--surface-2);
  color: var(--muted);
  white-space: nowrap;
}
.badge-warn {
  color: var(--warning);
  border-color: color-mix(in srgb, var(--warning) 45%, var(--border));
}
.sec-hint {
  margin: 6px 0 4px;
  font-size: 12px;
  color: var(--muted);
  line-height: 1.5;
}
.sec-error-line {
  margin-top: 6px;
  font-size: 12px;
  color: var(--danger);
}
.sec-drag-hint {
  color: var(--muted-2);
}

.field {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 0;
}
.field-label {
  width: 96px;
  flex-shrink: 0;
  font-size: 13px;
  color: var(--text);
}
.field-control {
  display: flex;
  align-items: center;
  gap: 8px;
}
.field-control.grow {
  flex: 1;
  max-width: 460px;
}
.chip-row {
  flex-wrap: wrap;
}
.ctl {
  background: var(--guo-chip-bg);
  border: 1px solid var(--guo-border);
  border-radius: var(--guo-radius);
  padding: 5px 10px;
  color: var(--text);
  font-size: 13px;
  font-family: inherit;
  min-width: 140px;
}
.ctl:hover,
.ctl:focus {
  border-color: var(--accent);
  outline: none;
}
.field-control.grow .ctl {
  flex: 1;
  min-width: 0;
}
.num {
  min-width: 90px;
  max-width: 120px;
}
.unit {
  font-size: 12px;
  color: var(--muted);
}
.with-unit {
  gap: 6px;
}

/* 通用小开关（与 SourceCard 的 GuoSwitch 风格同口径） */
.sw {
  position: relative;
  width: 36px;
  height: 19px;
  padding: 0;
  border-radius: 999px;
  border: 1px solid var(--guo-border);
  background: var(--surface-4);
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}
.sw.on {
  background: var(--accent);
  border-color: var(--accent);
}
.sw:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
.sw-knob {
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
.sw.on .sw-knob {
  left: 19px;
}

.save-row {
  min-height: 32px;
}
.save-ok {
  font-size: 12px;
  color: var(--muted);
}

.kv {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 3px 0;
}
.kv-k {
  width: 96px;
  flex-shrink: 0;
  font-size: 13px;
  color: var(--text);
}
.kv-v {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12.5px;
  color: var(--muted);
  min-width: 0;
  word-break: break-all;
}
.kv-v.muted {
  color: var(--muted-2);
}
.kv-v.selectable {
  cursor: text;
  user-select: text;
}
</style>
