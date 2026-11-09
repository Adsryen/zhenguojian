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
    path: '/library',
    name: 'library',
    component: () => import('@/views/PlaceholderView.vue'),
    meta: { title: '剧库推荐', phase: 'V2-2' }
  },
  {
    path: '/calendar',
    name: 'calendar',
    component: () => import('@/views/PlaceholderView.vue'),
    meta: { title: '新剧预约', phase: 'V2-2' }
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
    component: () => import('@/views/PlaceholderView.vue'),
    meta: { title: '观看历史', phase: 'V2-3' }
  },
  {
    path: '/downloads',
    name: 'downloads',
    component: () => import('@/views/PlaceholderView.vue'),
    meta: { title: '下载与合并', phase: 'V2-3' }
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('@/views/PlaceholderView.vue'),
    meta: { title: '设置', phase: 'V2-4' }
  }
]

export default createRouter({
  history: createWebHashHistory(),
  routes
})
