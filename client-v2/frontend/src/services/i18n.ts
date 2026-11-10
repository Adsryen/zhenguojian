/* ================================================================
   V2-4 极简 i18n —— 无第三方库。
   - locale: vue ref（'zh' | 'en'），持久化 localStorage 'zgj.locale'，默认 zh
   - t(key) 在模板/计算属性里调用即自动跟随 locale 重渲染（读 locale.value）
   - 字典只覆盖「设置页 + 导航」文案；缺 key 回退 zh，再回退 key 本身
   ================================================================ */
import { ref } from 'vue'

export type Locale = 'zh' | 'en'

const STORE_KEY = 'zgj.locale'

function loadLocale(): Locale {
  try {
    return localStorage.getItem(STORE_KEY) === 'en' ? 'en' : 'zh'
  } catch {
    return 'zh'
  }
}

export const locale = ref<Locale>(loadLocale())

export function setLocale(l: Locale): void {
  locale.value = l
  try {
    localStorage.setItem(STORE_KEY, l)
  } catch {
    /* 写不进不致命，会话内生效 */
  }
  document.documentElement.setAttribute('lang', l === 'en' ? 'en' : 'zh-CN')
}

const zh: Record<string, string> = {
  // ---- 导航（NavRail） ----
  'nav.section.discover': '发现',
  'nav.section.mine': '我的',
  'nav.section.system': '系统',
  'nav.discover': '找剧',
  'nav.search': '搜索',
  'nav.library': '剧库推荐',
  'nav.calendar': '新剧预约',
  'nav.follow': '追更预约',
  'nav.history': '观看历史',
  'nav.downloads': '下载与合并',
  'nav.settings': '设置',
  'nav.collapse': '收起',
  'nav.expand': '展开',

  // ---- 设置页头 ----
  'settings.title': '设置',
  'settings.subtitle': '外观 · 站源 · 播放默认 · 网络 · 存储与关于',

  // ---- 外观 ----
  'settings.appearance': '外观',
  'settings.appearance.hint': '主题与语言即时生效，偏好持久化在本机（localStorage）。',
  'settings.theme': '主题',
  'settings.theme.dark': '深色',
  'settings.theme.light': '浅色',
  'settings.theme.auto': '跟随系统',
  'settings.language': '语言',

  // ---- 站源 ----
  'settings.sources': '站源开关',
  'settings.sources.hint': '打开=允许该站参与找剧/搜索；变更即时同步本地核心。',
  'settings.sources.hint.mock': 'VITE_MOCK 示例数据：开关写入 mock 源表，重启页面即还原。',
  'settings.sources.synced': '已同步到本地核心：',
  'settings.sources.notImplemented': '未接入',
  'settings.sources.adultNote': '18+ 内容，出厂默认停用，需手动打开。',
  'settings.sources.retry': '同步失败，已回滚',
  'settings.sources.dragHint': '可拖动卡片调整排序，红果默认置顶。',

  // ---- 播放默认 ----
  'settings.playback': '播放默认',
  'settings.playback.hint': '倍速/清晰度/弹幕对新打开的剧集生效；核心绑定就绪前本地暂存。',
  'settings.playback.speed': '倍速',
  'settings.playback.quality': '清晰度',
  'settings.playback.danmaku': '弹幕',
  'settings.quality.auto': '自动',

  // ---- 网络与下载 ----
  'settings.network': '网络与下载',
  'settings.network.hint': '代理与并发由本地核心统一执行；核心绑定就绪前保存仅落本机。',
  'settings.network.proxyMode': '代理模式',
  'settings.network.proxy.auto': '自动（跟随系统）',
  'settings.network.proxy.direct': '直连',
  'settings.network.proxy.manual': '手动',
  'settings.network.proxyUrl': '代理地址',
  'settings.network.proxyUrl.ph': 'http://127.0.0.1:7890 或 socks5://127.0.0.1:7891',
  'settings.network.catalogConcurrency': '目录并发',
  'settings.network.interval': '请求间隔',
  'settings.network.downloadConcurrency': '下载并发',
  'settings.network.bySource': '按站源分道下载',
  'settings.network.save': '保存设置',
  'settings.network.saved': '已暂存到本机（localStorage），核心接线后生效。',
  'settings.network.lanesUnit': '路',
  'settings.network.msUnit': '毫秒',

  // ---- 角标 ----
  'settings.staged': '本地暂存，核心接线后生效',
  'settings.mockBadge': '示例数据',

  // ---- 存储与关于 ----
  'settings.storage': '存储与关于',
  'settings.storage.hint': '真果鉴 V2 · 短剧聚合客户端（站源适配参考 guoapp）。以下信息只读。',
  'settings.storage.dataDir': '数据目录',
  'settings.storage.version': '应用版本',
  'settings.storage.commit': '打包提交',
  'settings.storage.commitUnknown': '未知（待 App.BuildInfo 绑定）',
  'settings.storage.core': '本地核心',
  'settings.storage.coreConnected': '已连接',
  'settings.storage.coreDisconnected': '未连接',
  'settings.storage.readFail': '读取失败',

  // ---- 右键菜单（contextMenu 服务） ----
  'ctx.copy': '复制',
  'ctx.cut': '剪切',
  'ctx.paste': '粘贴',
  'ctx.selectAll': '全选',

  // ---- 封面失败警示条（covers 诊断 → DiscoverView banner） ----
  'covers.banner.missingFfmpeg':
    '海报转码组件 ffmpeg 未找到：请下载 zhenGuoJian-v2-runtime.zip 解压到程序目录，使 runtime\\ffmpeg\\ffmpeg.exe 就位；或设置 ZHENGUO_FFMPEG 指向 ffmpeg.exe',
  'covers.banner.partial': '部分封面加载失败（F12 查看原因）',
  'covers.banner.dismiss': '关闭提示',

  // ---- 搜索页「全部来源」扇出（SearchView / SearchGroups / searchAll） ----
  'search.all': '全部来源',
  'search.allHint': '对所有已启用且已接入的站源并行搜索',
  'search.hintAll': '勾选了「全部来源」：回车将并行搜索所有已启用且已接入的站源',
  'search.noSources': '没有已启用且已接入的站源，请先在设置中开启',
  'search.failed': '搜索失败：',
  'search.retry': '重试',
  'search.allFailed': '全部站源均搜索失败',
  'search.groupLoading': '搜索中…',
  'search.groupCountSuffix': ' 条',
  'search.groupEmpty': '该源无匹配结果',
  'search.groupFailed': '该源搜索失败：',
  'search.groupRetry': '重试该源'
}

const en: Record<string, string> = {
  'nav.section.discover': 'Discover',
  'nav.section.mine': 'My Library',
  'nav.section.system': 'System',
  'nav.discover': 'Browse',
  'nav.search': 'Search',
  'nav.library': 'Library',
  'nav.calendar': 'Upcoming',
  'nav.follow': 'Following',
  'nav.history': 'History',
  'nav.downloads': 'Downloads',
  'nav.settings': 'Settings',
  'nav.collapse': 'Collapse',
  'nav.expand': 'Expand',

  'settings.title': 'Settings',
  'settings.subtitle': 'Appearance · Sources · Playback · Network · About',

  'settings.appearance': 'Appearance',
  'settings.appearance.hint': 'Theme and language apply instantly; preferences persist on this device (localStorage).',
  'settings.theme': 'Theme',
  'settings.theme.dark': 'Dark',
  'settings.theme.light': 'Light',
  'settings.theme.auto': 'System',
  'settings.language': 'Language',

  'settings.sources': 'Sources',
  'settings.sources.hint': 'On = source participates in browse/search; changes sync to the local core instantly.',
  'settings.sources.hint.mock': 'VITE_MOCK sample data: switches write to the mock source table and reset on reload.',
  'settings.sources.synced': 'Synced to local core: ',
  'settings.sources.notImplemented': 'Not wired',
  'settings.sources.adultNote': '18+ content, disabled by default; turn on manually.',
  'settings.sources.retry': 'Sync failed, rolled back',
  'settings.sources.dragHint': 'Drag cards to reorder; Hongguo pinned first by default.',

  'settings.playback': 'Playback Defaults',
  'settings.playback.hint': 'Speed/quality/danmaku apply to newly opened episodes; staged locally until core bindings land.',
  'settings.playback.speed': 'Speed',
  'settings.playback.quality': 'Quality',
  'settings.playback.danmaku': 'Danmaku',
  'settings.quality.auto': 'Auto',

  'settings.network': 'Network & Downloads',
  'settings.network.hint': 'Proxy and concurrency are enforced by the local core; saving only persists locally until bindings land.',
  'settings.network.proxyMode': 'Proxy Mode',
  'settings.network.proxy.auto': 'Auto (system)',
  'settings.network.proxy.direct': 'Direct',
  'settings.network.proxy.manual': 'Manual',
  'settings.network.proxyUrl': 'Proxy URL',
  'settings.network.proxyUrl.ph': 'http://127.0.0.1:7890 or socks5://127.0.0.1:7891',
  'settings.network.catalogConcurrency': 'Catalog Concurrency',
  'settings.network.interval': 'Request Interval',
  'settings.network.downloadConcurrency': 'Download Concurrency',
  'settings.network.bySource': 'Per-source Lanes',
  'settings.network.save': 'Save Settings',
  'settings.network.saved': 'Staged locally (localStorage); takes effect once core is wired.',
  'settings.network.lanesUnit': 'lanes',
  'settings.network.msUnit': 'ms',

  'settings.staged': 'Staged locally — effective after core wiring',
  'settings.mockBadge': 'Sample data',

  'settings.storage': 'Storage & About',
  'settings.storage.hint': 'ZhenGuoJian V2 · short-drama aggregator (source adapters reference guoapp). Read-only below.',
  'settings.storage.dataDir': 'Data Directory',
  'settings.storage.version': 'App Version',
  'settings.storage.commit': 'Build Commit',
  'settings.storage.commitUnknown': 'Unknown (awaiting App.BuildInfo binding)',
  'settings.storage.core': 'Local Core',
  'settings.storage.coreConnected': 'Connected',
  'settings.storage.coreDisconnected': 'Not connected',
  'settings.storage.readFail': 'Read failed',

  // ---- Context menu (contextMenu service) ----
  'ctx.copy': 'Copy',
  'ctx.cut': 'Cut',
  'ctx.paste': 'Paste',
  'ctx.selectAll': 'Select All',

  // ---- Cover failure banner (covers diagnostics → DiscoverView banner) ----
  'covers.banner.missingFfmpeg':
    'Poster transcoder ffmpeg not found: download zhenGuoJian-v2-runtime.zip and extract it into the app directory so runtime/ffmpeg/ffmpeg.exe is in place, or set ZHENGUO_FFMPEG to your ffmpeg.exe',
  'covers.banner.partial': 'Some covers failed to load (press F12 for details)',
  'covers.banner.dismiss': 'Dismiss',

  // ---- Search page "all sources" fan-out (SearchView / SearchGroups / searchAll) ----
  'search.all': 'All sources',
  'search.allHint': 'Search every enabled & wired source in parallel',
  'search.hintAll': '"All sources" is checked: Enter searches every enabled & wired source in parallel',
  'search.noSources': 'No enabled & wired sources — turn some on in Settings first',
  'search.failed': 'Search failed: ',
  'search.retry': 'Retry',
  'search.allFailed': 'All sources failed to search',
  'search.groupLoading': 'Searching…',
  'search.groupCountSuffix': ' results',
  'search.groupEmpty': 'No matches from this source',
  'search.groupFailed': 'This source failed: ',
  'search.groupRetry': 'Retry this source'
}

const dicts: Record<Locale, Record<string, string>> = { zh, en }

/** 翻译；缺 key 回退 zh 表，再回退 key 本身。模板里调用即随 locale 响应。 */
export function t(key: string): string {
  const table = dicts[locale.value]
  return table[key] ?? zh[key] ?? key
}

// 首屏把 lang 属性同步到当前 locale（主题走 main.ts 的 applyTheme 一行）
document.documentElement.setAttribute('lang', locale.value === 'en' ? 'en' : 'zh-CN')
