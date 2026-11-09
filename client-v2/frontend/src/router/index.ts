import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'

// wails 的资产协议用 http://wails.localhost/，history 模式会丢路径 → 用 hash 路由
const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/discover' },
  {
    path: '/discover',
    name: 'discover',
    component: () => import('@/views/DiscoverView.vue'),
    meta: { title: '找剧' }
  },
  {
    path: '/search',
    name: 'search',
    component: () => import('@/views/SearchView.vue'),
    meta: { title: '搜索' }
  },
  {
    // V2-2：详情页。query.d = 紧凑 Drama JSON（Detail 绑定入参），兜底 ?source=&title=
    path: '/detail/:id',
    name: 'detail',
    component: () => import('@/views/DetailView.vue'),
    meta: { title: '详情' }
  },
  {
    // V2-3b：播放页本体已接入（参数 d/ep/pos/c 沿用 dramaRef.playerRoute 约定）
    path: '/player',
    name: 'player',
    component: () => import('@/views/PlayerView.vue'),
    meta: { title: '播放页', phase: 'V2-3b' }
  },
  {
    // V2-2：新剧预约 + 追更预约（?tab=follow）
    path: '/reserve',
    name: 'reserve',
    component: () => import('@/views/ReserveView.vue'),
    meta: { title: '预约' }
  },
  {
    path: '/library',
    name: 'library',
    component: () => import('@/views/PlaceholderView.vue'),
    meta: { title: '剧库推荐', phase: 'V2-2' }
  },
  {
    // V2-2：追剧日历（Go Calendar 绑定 + 日期轴/分组/预约联动）
    path: '/calendar',
    name: 'calendar',
    component: () => import('@/views/CalendarView.vue'),
    meta: { title: '追剧日历' }
  },
  {
    path: '/follow',
    name: 'follow',
    component: () => import('@/views/PlaceholderView.vue'),
    meta: { title: '追更预约', phase: 'V2-2' }
  },
  {
    path: '/history',
    name: 'history',
    component: () => import('@/views/HistoryView.vue'),
    meta: { title: '观看历史' }
  },
  {
    path: '/downloads',
    name: 'downloads',
    component: () => import('@/views/DownloadsView.vue'),
    meta: { title: '下载与合并' }
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('@/views/SettingsView.vue'),
    meta: { title: '设置', phase: 'V2-4' }
  }
]

export default createRouter({
  history: createWebHashHistory(),
  routes
})
