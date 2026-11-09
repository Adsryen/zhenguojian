/* 绑定适配层：组件只依赖本模块，永不直接触碰 window.go。
   - 真实模式：window.go.main.App.<Method>(...) 返回 JSON 字符串（Go 侧 (string, error)）
   - VITE_MOCK=1：fixtures 双实现，可在纯浏览器里验证 UI */
import {
  ApiError,
  type BrowseFiltersResult,
  type CatalogParams,
  type CatalogResult,
  type Drama,
  type NativeApi,
  type SourceListItem
} from './types'
import { MOCK_SOURCES, mockCatalog } from './fixtures'

const isMock = import.meta.env.VITE_MOCK === '1'

function wailsApp(): Record<string, (...args: any[]) => Promise<string>> {
  const app = window.go?.main?.App
  if (!app) {
    throw new ApiError('未检测到 Wails 原生桥（浏览器直开需 VITE_MOCK=1 或 wails dev）', 'bridge_missing')
  }
  return app
}

/** Go 侧 bound 方法调用：返回原始字符串（Ping/DataPath 这类非 JSON 值直接用） */
async function callGoRaw(method: string, ...args: any[]): Promise<string> {
  const fn = wailsApp()[method]
  if (typeof fn !== 'function') {
    throw new ApiError(`原生方法 ${method} 不存在`, 'bridge_missing')
  }
  try {
    const raw = await fn(...args)
    return typeof raw === 'string' ? raw : JSON.stringify(raw)
  } catch (err: any) {
    const message = typeof err === 'string' ? err : err?.message ?? String(err)
    const m = message.match(/\[([a-z_]+)\]\s*$/)
    throw new ApiError(m ? message.slice(0, m.index) : message, m ? m[1] : '')
  }
}

/** 同上 + JSON 解析（Go 侧 marshalJSON 的对象/数组返回） */
async function callGo<T>(method: string, ...args: any[]): Promise<T> {
  const raw = await callGoRaw(method, ...args)
  if (raw === '' || raw === undefined) return undefined as unknown as T
  return JSON.parse(raw) as T
}

const delay = (ms: number) => new Promise((r) => setTimeout(r, ms))

const realApi: NativeApi = {
  // Ping/DataPath 是裸字符串返回（非 JSON），用 raw
  ping: () => callGoRaw('Ping'),
  dataPath: () => callGoRaw('DataPath'),
  // ListSources 返回 marshalJSON([]SourceListItem) → 数组
  listSources: () => callGo<SourceListItem[]>('ListSources'),
  setSourceEnabled: async (slug, enabled) => {
    await callGo('SetSourceEnabled', slug, enabled)
  },
  // Catalog 绑定是位置参数 (source, query, category, page, lane, force)
  catalog: (p: CatalogParams) =>
    callGo<CatalogResult>(
      'Catalog',
      p.source,
      p.query ?? '',
      p.category ?? '',
      p.page ?? 1,
      p.lane ?? '',
      p.force ?? false
    ),
  // Detail 绑定是 dramaJSON 位置参数
  detail: (drama: Drama) => callGo<Record<string, any>>('Detail', JSON.stringify(drama)),
  // BrowseFilters 绑定是 (source, category) 位置参数
  browseFilters: (source: string) => callGo<BrowseFiltersResult>('BrowseFilters', source, '')
}

const mockApi: NativeApi = {
  ping: async () => 'pong-mock',
  dataPath: async () => '~/.config/ZhenguoJian (mock)',
  listSources: async () => {
    await delay(120)
    return MOCK_SOURCES.map((s) => ({ ...s }))
  },
  setSourceEnabled: async (slug, enabled) => {
    await delay(80)
    const s = MOCK_SOURCES.find((x) => x.slug === slug)
    if (s) s.enabled = enabled
  },
  catalog: async (p: CatalogParams) => {
    await delay(300 + Math.random() * 350)
    return mockCatalog(p.source, p.query ?? '', p.page ?? 1)
  },
  detail: async (drama: Drama) => ({ id: drama.id, chapters: [], mock: true }),
  browseFilters: async () => {
    await delay(120)
    // 与默认构建（无 overlay）一致：模拟 discovery_unavailable，触发前端兜底 chips
    throw new ApiError('筛选墙通道未启用（mock 与默认构建一致）', 'discovery_unavailable')
  }
}

export const api: NativeApi = isMock ? mockApi : realApi
export { ApiError }
export const MOCK_MODE = isMock
