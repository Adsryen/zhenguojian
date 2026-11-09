/* v1 行协议数据形状（照抄 zhenguojian/sidecar nativeDrama / catalog / sourceListItem） */

export interface Drama {
  metadataSchema?: number
  id: string
  source: string
  sourceId?: string
  title: string
  description?: string
  cover?: string
  episodes?: number
  category?: string
  vip?: boolean | null
  heat?: string
  views?: string
  onlineDate?: string
  tags?: string[]
  releaseStatus?: string
  /** mock-only：源级 18+ 演示用，真实引擎 items 不带 */
  adult?: boolean
}

export interface CatalogResult {
  items: Drama[]
  hasMore: boolean
  page: number
  warning?: string
  localSearch?: boolean
  fresh?: boolean
}

export interface CatalogParams {
  source: string
  query?: string
  category?: string
  page?: number
  lane?: string
  force?: boolean
}

export interface SourceListItem {
  slug: string
  name: string
  displayName?: string
  searchKind?: string
  adult?: boolean
  lanes?: string[]
  seasons?: boolean
  catalogKinds?: string[]
  enabledByDefault?: boolean
  implemented: boolean
  enabled: boolean
}

export interface FilterOption {
  id: string
  name: string
}

export interface FilterGroup {
  key: string
  name: string
  options: FilterOption[]
}

export interface BrowseFiltersResult {
  groups: FilterGroup[]
}

/** 适配层统一接口：wails 实现与 mock 实现同签名 */
export interface NativeApi {
  ping(): Promise<string>
  dataPath(): Promise<string>
  listSources(): Promise<SourceListItem[]>
  setSourceEnabled(slug: string, enabled: boolean): Promise<void>
  catalog(params: CatalogParams): Promise<CatalogResult>
  detail(drama: Drama): Promise<Record<string, any>>
  browseFilters(source: string): Promise<BrowseFiltersResult>
}

/** 带协议 code 的错误（discovery_unavailable / source_blocked 等） */
export class ApiError extends Error {
  code: string
  retryAt?: number
  constructor(message: string, code = '', retryAt?: number) {
    super(message)
    this.code = code
    this.retryAt = retryAt
  }
}
