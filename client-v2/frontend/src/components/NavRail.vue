<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { t } from '@/services/i18n'

const store = useAppStore()
const route = useRoute()

// 三段语义：发现 / 我的 / 系统（对齐参照项目侧栏；页面命名与 v1 main.py 页签一致）
// V2-4：文案进 i18n 字典，locale 翻转即时重渲染（computed 读 locale.value）
const sections = computed(() => [
  {
    label: t('nav.section.discover'),
    items: [
      { to: '/discover', text: t('nav.discover'), icon: 'compass' },
      { to: '/library', text: t('nav.library'), icon: 'library' },
      { to: '/search', text: t('nav.search'), icon: 'search' },
      // 追剧日历实页（V2-5）：/reserve 保留路由兼容深层链接与预约视图
      { to: '/calendar', text: t('nav.calendar'), icon: 'calendar' }
    ]
  },
  {
    label: t('nav.section.mine'),
    items: [
      // V2-2：追更预约并入 /reserve?tab=follow（旧 /follow 只做重定向兜底）
      { to: '/reserve?tab=follow', text: t('nav.follow'), icon: 'heart' },
      { to: '/history', text: t('nav.history'), icon: 'history' },
      { to: '/downloads', text: t('nav.downloads'), icon: 'download' }
    ]
  },
  {
    label: t('nav.section.system'),
    items: [{ to: '/settings', text: t('nav.settings'), icon: 'settings' }]
  }
])

// V2-2：/reserve 带 tab query，日历项(无 query)命中默认 tab，追更项命中 ?tab=follow
function isActive(to: string): boolean {
  const [path, qs] = to.split('?')
  if (path !== route.path) return false
  if (!qs) return route.query.tab !== 'follow'
  const want = new URLSearchParams(qs).get('tab')
  return route.query.tab === want
}
</script>

<template>
  <aside class="nav" :class="{ collapsed: store.navCollapsed }">
    <div class="brand">
      <div class="brand-mark">真</div>
      <div v-if="!store.navCollapsed" class="brand-text">
        <div class="brand-name">真果鉴</div>
        <div class="brand-sub">V2</div>
      </div>
    </div>

    <nav class="nav-body">
      <div v-for="sec in sections" :key="sec.label" class="nav-section">
        <div v-if="!store.navCollapsed" class="section-label">{{ sec.label }}</div>
        <div v-else class="section-divider"></div>
        <RouterLink
          v-for="item in sec.items"
          :key="item.to"
          :to="item.to"
          class="nav-item"
          :class="{ active: isActive(item.to) }"
          :title="item.text"
        >
          <span class="ico-wrap">
            <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
              <template v-if="item.icon === 'compass'">
                <circle cx="12" cy="12" r="9" /><path d="m15.5 8.5-2 5-5 2 2-5z" />
              </template>
              <template v-else-if="item.icon === 'library'">
                <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20V4H6.5A2.5 2.5 0 0 0 4 6.5v13z" /><path d="M4 19.5A2.5 2.5 0 0 0 6.5 22H20v-5" />
              </template>
              <template v-else-if="item.icon === 'search'">
                <circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3" />
              </template>
              <template v-else-if="item.icon === 'calendar'">
                <rect x="3" y="5" width="18" height="16" rx="2" /><path d="M8 3v4M16 3v4M3 11h18" />
              </template>
              <template v-else-if="item.icon === 'heart'">
                <path d="M19.5 12.6 12 20l-7.5-7.4a5 5 0 1 1 7.5-6.5 5 5 0 1 1 7.5 6.5" />
              </template>
              <template v-else-if="item.icon === 'history'">
                <circle cx="12" cy="12" r="9" /><path d="M12 7v5l3.5 2" />
              </template>
              <template v-else-if="item.icon === 'download'">
                <path d="M12 4v11m0 0 4-4m-4 4-4-4" /><path d="M4 19h16" />
              </template>
              <template v-else>
                <circle cx="12" cy="12" r="3.2" />
                <path d="M19 12a7 7 0 0 0-.15-1.4l2-1.55-2-3.45-2.35.95a7 7 0 0 0-2.4-1.4L13.7 2.6h-3.4l-.4 2.55a7 7 0 0 0-2.4 1.4L5.15 5.6l-2 3.45 2 1.55A7 7 0 0 0 5 12c0 .48.05.95.15 1.4l-2 1.55 2 3.45 2.35-.95a7 7 0 0 0 2.4 1.4l.4 2.55h3.4l.4-2.55a7 7 0 0 0 2.4-1.4l2.35.95 2-3.45-2-1.55c.1-.45.15-.92.15-1.4" />
              </template>
            </svg>
          </span>
          <span v-if="!store.navCollapsed" class="nav-text">{{ item.text }}</span>
        </RouterLink>
      </div>
    </nav>

    <button class="nav-toggle" :title="store.navCollapsed ? t('nav.expand') : t('nav.collapse')" @click="store.toggleNav()">
      <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
        <path d="M4 6h16M4 12h16M4 18h16" />
      </svg>
      <span v-if="!store.navCollapsed" class="toggle-text">{{ t('nav.collapse') }}</span>
    </button>
  </aside>
</template>

<style scoped>
.nav {
  width: 200px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  background: var(--surface-2);
  border-right: 1px solid var(--border);
  transition: width 0.18s ease;
  overflow: hidden;
}
.nav.collapsed {
  width: 60px;
}

.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 16px 14px 12px;
}
.brand-mark {
  width: 32px;
  height: 32px;
  border-radius: var(--guo-radius);
  background: var(--accent);
  color: var(--on-accent);
  font-weight: 700;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.brand-name {
  font-weight: 700;
  font-size: 15px;
  white-space: nowrap;
}
.brand-sub {
  font-size: 11px;
  color: var(--muted);
  white-space: nowrap;
}

.nav-body {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 4px 8px;
}
.nav-section {
  margin-bottom: 10px;
}
.section-label {
  font-size: 11px;
  color: var(--muted-2);
  padding: 8px 8px 4px;
  letter-spacing: 1px;
}
.section-divider {
  height: 1px;
  background: var(--border);
  margin: 8px 6px;
}
.nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  margin: 1px 0;
  border-radius: var(--guo-radius);
  color: var(--muted);
  text-decoration: none;
  white-space: nowrap;
  transition: background 0.12s, color 0.12s;
}
.nav-item:hover {
  background: var(--guo-chip-hover);
  color: var(--text);
}
.nav-item.active {
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}
.ico-wrap {
  display: flex;
  flex-shrink: 0;
}
.nav-text {
  font-size: 13.5px;
}

.nav-toggle {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 8px;
  padding: 9px 10px;
  background: transparent;
  border: 1px solid var(--border);
  border-radius: var(--guo-radius);
  color: var(--muted);
  cursor: pointer;
}
.nav-toggle:hover {
  background: var(--guo-chip-hover);
  color: var(--text);
}
.toggle-text {
  font-size: 12.5px;
}
</style>
