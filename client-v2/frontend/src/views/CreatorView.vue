<script setup lang="ts">
/* CreatorView —— /creator?u=<creator JSON>&name=<显示名>（V2-L，对应 v1 creators.py
   CreatorPage :289-416）。
   头部：头像(resolveCover，失败回落首字圆) + 名字 + meta「{role} · 红果号 {userId}」+
   简介 + 三项统计（v1 label 顺序 粉丝/关注/获赞，值在前）+ 复制 ID（clipboard 双轨，
   mock 走 window.__zgjCopied 钩子）。
   作品区：v1 _WorksGrid 等价 = 复用 PosterGrid；排序 最新/最热 → 服务端重拉（与 v1
   _sort_changed 客户端重排不同，核心 creatorWorks 原生支持 sort，直接传参更准）；
   tabs 扁平化为「tab subTab」组合下拉（v1 tab 切换 UI，creators.py:323-360）；
   分页 = hasMore+cursor「加载更多」+ 按 id 去重；profile 失败红条+重试（v1
   _retry_load：profile 未加载→只重拉 profile，已加载→作品 reset 重拉）。
   tabs 为空数组 → 「该演职员暂无公开作品」空态且不调 works（探针实测核心同语义报错）。 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, DEGRADED, MOCK_MODE } from '@/services/api'
import type { CreatorEntry, CreatorProfileInfo, CreatorTabGroup, Drama } from '@/services/types'
import { resolveCover } from '@/services/covers'
import { detailRoute } from '@/services/dramaRef'
import { t, tf } from '@/services/i18n'
import PosterGrid from '@/components/PosterGrid.vue'

const route = useRoute()
const router = useRouter()

/* ---------- 路由入参（演员条整对象，最小集 {userId,name,avatar}） ---------- */

const creator = ref<CreatorEntry | null>(null)
const badArg = ref(false)

function parseArg(): CreatorEntry | null {
  const raw = String(route.query.u ?? '')
  if (!raw) return null
  try {
    const obj = JSON.parse(raw) as CreatorEntry
    if (obj && (obj.userId || obj.id) && obj.name) return obj
  } catch {
    /* fallthrough */
  }
  return null
}

/* ---------- profile / 头部 ---------- */

const profile = ref<CreatorProfileInfo | null>(null)
const profileError = ref('')
const profileLoading = ref(true)
const tabs = ref<CreatorTabGroup[]>([])
const avatarSrc = ref('')
const uid = computed(() => profile.value?.userId || profile.value?.id || creator.value?.userId || creator.value?.id || '')

function coverUrl(c?: string): string {
  if (!c) return ''
  if (/^(https?:|data:|blob:)/i.test(c)) return c
  return c.startsWith('/') ? c : `/${c}`
}

async function loadProfile() {
  const c = creator.value
  if (!c) return
  profileLoading.value = true
  profileError.value = ''
  try {
    const res = await api.creatorProfile(c)
    profile.value = res.profile ?? {}
    tabs.value = Array.isArray(res.tabs) ? res.tabs : []
    const src = profile.value.avatar
    if (src) {
      void resolveCover({
        id: `creator:${uid.value}`,
        source: 'hongguo',
        title: profile.value.name,
        cover: src
      } as Drama).then((s) => {
        if (s) avatarSrc.value = s
      })
    }
  } catch (e: any) {
    profile.value = null
    profileError.value = e?.message ?? String(e)
  } finally {
    profileLoading.value = false
  }
}

/* ---------- tabs 扁平组合下拉（v1 tab/subTab 两级切换） ---------- */

interface TabChoice {
  key: string
  label: string
  tab: number
  subTab: number
}

const tabChoices = computed<TabChoice[]>(() => {
  const out: TabChoice[] = []
  for (const g of tabs.value) {
    const subs = g.subTabs?.length ? g.subTabs : [{ id: 0, name: g.name }]
    for (const sb of subs) {
      const subName = g.subTabs?.length ? sb.name : ''
      out.push({
        key: `${g.id}:${sb.id}`,
        label: subName ? `${g.name} ${subName}` : g.name,
        tab: g.id,
        subTab: sb.id
      })
    }
  }
  return out
})

const tabChoice = ref('')
watch(
  tabChoices,
  (list) => {
    if (list.length && !list.some((x) => x.key === tabChoice.value)) tabChoice.value = list[0].key
  },
  { immediate: true }
)

/* ---------- works 墙 ---------- */

type SortKey = 'time_desc' | 'hot_desc'
const sort = ref<SortKey>('time_desc')
const works = ref<Drama[]>([])
const worksCursor = ref('')
const worksHasMore = ref(false)
const worksLoading = ref(false)
const worksLoadingMore = ref(false)
const worksError = ref('')
let requestSeq = 0

const noWorksCategory = computed(() => profileLoaded.value && tabs.value.length === 0)
const profileLoaded = computed(() => !!profile.value && !profileError.value)

async function loadWorks(reset: boolean) {
  const c = creator.value
  if (!c || tabs.value.length === 0) return
  const seq = ++requestSeq
  if (reset) {
    worksLoading.value = true
    worksError.value = ''
  } else {
    worksLoadingMore.value = true
  }
  const choice = tabChoices.value.find((x) => x.key === tabChoice.value)
  try {
    const res = await api.creatorWorks(c, choice?.tab ?? 0, choice?.subTab ?? 0, reset ? '' : worksCursor.value, sort.value)
    if (seq !== requestSeq) return
    const items = Array.isArray(res.items) ? res.items : []
    if (reset) {
      works.value = items
    } else {
      // v1 同款按 id 去重追加；已知 id 集合从当前墙重建
      const seen = new Set(works.value.map((d) => d.id))
      for (const d of items) if (!seen.has(d.id)) works.value.push(d)
    }
    const nextCursor = res.cursor ?? ''
    const appended = items.length > 0
    worksHasMore.value = !!res.hasMore && !!nextCursor && nextCursor !== (reset ? '' : worksCursor.value) && appended
    if (nextCursor) worksCursor.value = nextCursor
  } catch (e: any) {
    if (seq !== requestSeq) return
    worksError.value = e?.message ?? String(e)
    worksHasMore.value = false
  } finally {
    if (seq === requestSeq) {
      worksLoading.value = false
      worksLoadingMore.value = false
    }
  }
}

function onSortChange() {
  void loadWorks(true)
}
watch(tabChoice, (v, old) => {
  if (old !== undefined && v !== old) void loadWorks(true)
})

function onLoadMore() {
  if (!worksHasMore.value || worksLoading.value || worksLoadingMore.value) return
  void loadWorks(false)
}

/* ---------- 失败重试（v1 _retry_load 语义） ---------- */

const retryBusy = ref(false)
async function retry() {
  if (retryBusy.value) return
  retryBusy.value = true
  try {
    if (!profileLoaded.value) {
      await loadProfile()
      if (profileLoaded.value) await loadWorks(true)
    } else {
      works.value = []
      worksCursor.value = ''
      await loadWorks(true)
    }
  } finally {
    retryBusy.value = false
  }
}

/* ---------- 复制 ID ---------- */

const toast = ref('')
let toastTimer: number | undefined

function showToast(msg: string) {
  toast.value = msg
  window.clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => (toast.value = ''), 2200)
}

async function copyId() {
  const text = uid.value
  if (!text) return
  if (MOCK_MODE || DEGRADED) {
    // 验证钩子（__zgjOpenPath 先例）：mock/降级态记入 window 供 headless 断言
    ;(window as any).__zgjCopied = text
    showToast(t('creator.copied'))
    return
  }
  try {
    await navigator.clipboard.writeText(text)
    ;(window as any).__zgjCopied = text
    showToast(t('creator.copied'))
    return
  } catch {
    const app = (window as any).go?.main?.App
    if (app?.ClipboardWrite) {
      try {
        await app.ClipboardWrite(text)
        ;(window as any).__zgjCopied = text
        showToast(t('creator.copied'))
        return
      } catch (e: any) {
        showToast(tf('creator.copyFailed', { msg: e?.message ?? e }))
        return
      }
    }
    showToast(t('creator.copyUnavailable'))
  }
}

/* ---------- 网格点击 → 详情 ---------- */

function selectDrama(d: Drama) {
  router.push(detailRoute(d))
}

/* ---------- 状态行（v1 '已加载 %d 部 · %s'） ---------- */

const worksCount = computed(() => works.value.length)
const statusTail = computed(() => (worksHasMore.value ? t('creator.tailMore') : t('creator.tailEnd')))

async function boot() {
  const c = parseArg()
  if (!c) {
    badArg.value = true
    profileLoading.value = false
    return
  }
  badArg.value = false
  creator.value = c
  profile.value = null
  works.value = []
  worksCursor.value = ''
  avatarSrc.value = ''
  await loadProfile()
  if (profileLoaded.value) await loadWorks(true)
}

onMounted(boot)
// 同路由仅 query 变（演员页点另一位演员 / 验证脚本换 creator）：组件不 remount，必须手动重载
watch(() => route.query.u, () => void boot())
</script>

<template>
  <div class="creator-view">
    <div class="headbar">
      <button class="btn ghost" @click="router.back()">← {{ t('creator.back') }}</button>
      <h1>{{ t('creator.title') }}</h1>
    </div>

    <div v-if="badArg" class="state-block">
      <div>{{ t('creator.invalidArg') }}</div>
    </div>

    <!-- profile 红条（v1 failed label + 重试） -->
    <div v-else-if="profileError" class="banner err-banner">
      <span>{{ tf('creator.failed', { msg: profileError }) }}</span>
      <button class="btn" :disabled="retryBusy" @click="retry">{{ t('creator.retry') }}</button>
    </div>

    <!-- 头部卡（v1 CreatorPage 头部三统计） -->
    <div v-else-if="profile" class="profile card">
      <div class="profile-top">
        <div class="avatar">
          <img v-if="avatarSrc" :src="coverUrl(avatarSrc)" :alt="profile.name" />
          <span v-else class="avatar-initial">{{ (profile.name || '').slice(0, 1) }}</span>
        </div>
        <div class="profile-id">
          <div class="profile-name">{{ profile.name }}</div>
          <div v-if="uid" class="profile-meta">
            {{ [profile.role, tf('creator.hongguoNo', { id: uid })].filter(Boolean).join(' · ') }}
          </div>
          <button v-if="uid" class="btn tiny" @click="copyId">{{ t('creator.copyId') }}</button>
        </div>
      </div>
      <p v-if="profile.description" class="profile-desc">{{ profile.description }}</p>
      <div class="stats">
        <div class="stat"><span class="stat-value">{{ profile.fans || t('creator.statPlaceholder') }}</span><span class="stat-label">{{ t('creator.fans') }}</span></div>
        <div class="stat"><span class="stat-value">{{ profile.following || t('creator.statPlaceholder') }}</span><span class="stat-label">{{ t('creator.following') }}</span></div>
        <div class="stat"><span class="stat-value">{{ profile.likes || t('creator.statPlaceholder') }}</span><span class="stat-label">{{ t('creator.likes') }}</span></div>
      </div>
    </div>

    <div v-else class="skel card"><div class="skel-head" /></div>

    <!-- 作品区 -->
    <template v-if="profile && !profileError">
      <div v-if="noWorksCategory" class="state-block">
        <div>{{ t('creator.noWorks') }}</div>
      </div>
      <div v-else class="works card">
        <div class="works-head">
          <h2>{{ tf('creator.works', { n: worksCount }) }}</h2>
          <div class="works-tools">
            <label v-if="tabChoices.length > 1" class="tool">
              {{ t('creator.tabLabel') }}
              <select v-model="tabChoice">
                <option v-for="x in tabChoices" :key="x.key" :value="x.key">{{ x.label }}</option>
              </select>
            </label>
            <label class="tool">
              {{ t('creator.sortLabel') }}
              <select v-model="sort" @change="onSortChange">
                <option value="time_desc">{{ t('creator.sortTime') }}</option>
                <option value="hot_desc">{{ t('creator.sortHot') }}</option>
              </select>
            </label>
          </div>
        </div>

        <div v-if="worksError" class="banner err-banner">
          <span>{{ tf('creator.failed', { msg: worksError }) }}</span>
          <button class="btn" :disabled="retryBusy" @click="retry">{{ t('creator.retry') }}</button>
        </div>

        <PosterGrid
          v-else
          :items="works"
          :loading="worksLoading"
          :loading-more="worksLoadingMore"
          @select="selectDrama"
          @load-more="onLoadMore"
        />

        <div v-if="!worksLoading && !worksError" class="works-status">
          <span>{{ tf('creator.loaded', { n: worksCount }) }} · {{ statusTail }}</span>
          <button v-if="worksHasMore" class="btn" :disabled="worksLoadingMore" @click="onLoadMore">
            {{ worksLoadingMore ? t('creator.loading') : t('creator.loadMore') }}
          </button>
        </div>
      </div>
    </template>

    <div v-if="toast" class="toast">{{ toast }}</div>
  </div>
</template>

<style scoped>
/* 版式：头部卡 + 作品卡两张 .card，壳层风格与 DetailView 一致（CSS 变量、无硬编码色） */
.creator-view {
  padding: 8px 0 24px;
}
.headbar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}
.headbar h1 {
  font-size: 16px;
  margin: 0;
}
.btn {
  border: 1px solid var(--line);
  background: var(--surface-2);
  color: var(--text);
  border-radius: 8px;
  padding: 4px 10px;
  cursor: pointer;
  font-size: 13px;
}
.btn.tiny {
  padding: 2px 8px;
  font-size: 12px;
}
.btn.ghost {
  background: none;
  border-color: transparent;
}
.btn:disabled {
  opacity: 0.55;
  cursor: default;
}
.card {
  border: 1px solid var(--line);
  border-radius: 12px;
  background: var(--surface-1);
  padding: 14px;
  margin-bottom: 14px;
}
.banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  border-radius: 10px;
  padding: 10px 12px;
  margin-bottom: 12px;
  font-size: 13px;
}
.err-banner {
  border: 1px solid var(--danger, #d43a3a);
  color: var(--danger, #d43a3a);
  background: var(--danger-soft, rgba(212, 58, 58, 0.08));
}
.profile-top {
  display: flex;
  gap: 14px;
  align-items: center;
}
.avatar {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  overflow: hidden;
  flex-shrink: 0;
  background: var(--surface-3);
  display: flex;
  align-items: center;
  justify-content: center;
}
.avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.avatar-initial {
  font-size: 26px;
}
.profile-name {
  font-size: 16px;
  font-weight: 600;
}
.profile-meta {
  font-size: 12px;
  opacity: 0.7;
  margin: 2px 0 6px;
}
.profile-desc {
  font-size: 13px;
  margin: 10px 0 0;
  opacity: 0.85;
}
.stats {
  display: flex;
  gap: 28px;
  margin-top: 14px;
}
.stat {
  display: flex;
  flex-direction: column;
}
.stat-value {
  font-size: 15px;
  font-weight: 600;
}
.stat-label {
  font-size: 12px;
  opacity: 0.65;
}
.works-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.works-head h2 {
  font-size: 14px;
  margin: 0;
}
.works-tools {
  display: flex;
  gap: 12px;
  font-size: 12px;
}
.tool {
  display: flex;
  align-items: center;
  gap: 6px;
}
.tool select {
  border: 1px solid var(--line);
  border-radius: 6px;
  background: var(--surface-2);
  color: var(--text);
  padding: 2px 6px;
  font-size: 12px;
}
.works-status {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  font-size: 12px;
  opacity: 0.75;
  margin-top: 10px;
}
.state-block {
  text-align: center;
  padding: 32px 0;
  font-size: 13px;
  opacity: 0.8;
}
.skel-head {
  height: 92px;
  border-radius: 10px;
  background: var(--surface-3);
}
.toast {
  position: fixed;
  left: 50%;
  bottom: 26px;
  transform: translateX(-50%);
  background: var(--surface-3);
  border: 1px solid var(--line);
  color: var(--text);
  border-radius: 10px;
  padding: 8px 14px;
  font-size: 13px;
  z-index: 60;
}
</style>
