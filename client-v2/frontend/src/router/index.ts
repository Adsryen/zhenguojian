import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'

// wails 的资产协议用 http://wails.localhost/，history 模式会丢路径 → 用 hash 路由
const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/discover' },
  {
    path: '/discover',
    name: 'discover',
    component: () => import('@/views/DiscoverView.vue'),
  },
  {
    path: '/search',
    name: 'search',
    component: () => import('@/views/SearchView.vue'),
  },
  {
    // V2-2：详情页。query.d = 紧凑 Drama JSON（Detail 绑定入参），兜底 ?source=&title=
    path: '/detail/:id',
    name: 'detail',
    component: () => import('@/views/DetailView.vue'),
  },
  {
    // V2-L：创作者页。query.u = 演员条 creator 整对象 JSON（含 userId），query.name 仅供返回显示
    path: '/creator',
    name: 'creator',
    component: () => import('@/views/CreatorView.vue'),
  },
  {
    // V2-3b：播放页本体已接入（参数 d/ep/pos/c 沿用 dramaRef.playerRoute 约定）
    path: '/player',
    name: 'player',
    component: () => import('@/views/PlayerView.vue'),
  },
  {
    // V2-2：新剧预约 + 追更预约（?tab=follow）
    path: '/reserve',
    name: 'reserve',
    component: () => import('@/views/ReserveView.vue'),
  },
  {
    path: '/library',
    name: 'library',
    component: () => import('@/views/LibraryView.vue'),
  },
  {
    // V2-2：追剧日历（Go Calendar 绑定 + 日期轴/分组/预约联动）
    path: '/calendar',
    name: 'calendar',
    component: () => import('@/views/CalendarView.vue'),
  },
  {
    // V2-6-parity：旧 /follow 骨架页已删（NavRail 早在 V2-2 就指向 /reserve?tab=follow），
    // 保留重定向只为兼容手敲/历史深链，避免 404 空白页。
    path: '/follow',
    redirect: { path: '/reserve', query: { tab: 'follow' } }
  },
  {
    path: '/history',
    name: 'history',
    component: () => import('@/views/HistoryView.vue'),
  },
  {
    path: '/downloads',
    name: 'downloads',
    component: () => import('@/views/DownloadsView.vue'),
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('@/views/SettingsView.vue'),
  }
]

export default createRouter({
  history: createWebHashHistory(),
  routes
})
