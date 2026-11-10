// 封面解析层（海报管线前端侧）。
//
// 背景：红果等站源封面 URL 是签名 HEIC —— webview 直接 <img src> 必挂（HEIC 无解码器），
// 换扩展名为 .jpeg 又 403（签名校验）。真实桥下 api.cover 走 Go 壳层 App.Cover
// （核心 cover action 下载 → ffmpeg HEIC→JPEG 转码 → data URL）；本模块负责：
//   1) 进程内 LRU 缓存（上限 400，命中 touch、淘汰最久未用，只缓存成功值）；
//   2) in-flight 去重（同 drama.id 并发调用复用同一 promise）；
//   3) 兜底语义：失败 resolve('')（绝不 reject）——空值不进缓存，组件先显首字占位，
//      之后再滚动/重进页面会自然重试（转码依赖 ffmpeg，冷启动首张慢是正常的）。
//
// MOCK/DEGRADED 分支（沿用 api.ts 现有导出判断）：直接返回 drama.cover —— mock fixture
// 是本地可渲染的 /posters/*.svg；DEGRADED（无桥）保持与现状一致的原 URL 行为，
// 挂了也有组件首字兜底接住。

import { api, DEGRADED, MOCK_MODE } from './api'
import type { Drama } from './types'

/** LRU 上限：400 张海报 data URL（单张 JPEG ≤4MB 上限由 Go 侧卡，典型 30–80KB）。 */
const LRU_MAX = 400

/** dramaID → 已解析封面（data URL 或 mock 原路径）。空串从不写入（失败总是重试）。 */
const cache = new Map<string, string>()
/** dramaID → 在途 promise（同 id 并发合并；settled 即移除）。 */
const inflight = new Map<string, Promise<string>>()

function cacheGet(id: string): string | undefined {
  const v = cache.get(id)
  if (v !== undefined) {
    // LRU touch：delete+set 把条目移到 Map 尾部（最近使用），头部即最久未用
    cache.delete(id)
    cache.set(id, v)
  }
  return v
}

function cachePut(id: string, src: string): void {
  if (!id || !src) return // 失败（''）不进缓存
  cache.delete(id)
  cache.set(id, src)
  while (cache.size > LRU_MAX) {
    const oldest = cache.keys().next()
    if (oldest.done) break
    cache.delete(oldest.value)
  }
}

/**
 * 解析单个 drama 的封面为可直接绑 <img src> 的字符串。
 * 永 reject 不发生：桥异常 / 缺 ffmpeg / 转码失败 / 核心下载失败 → ''。
 */
export function resolveCover(drama: Drama | null | undefined): Promise<string> {
  if (!drama || !drama.cover) return Promise.resolve('')
  const id = drama.id
  if (!id) return Promise.resolve('')
  const hit = cacheGet(id)
  if (hit !== undefined) return Promise.resolve(hit)
  const flying = inflight.get(id)
  if (flying) return flying

  const task = (async (): Promise<string> => {
    if (MOCK_MODE || DEGRADED) return drama.cover as string
    try {
      // Go 侧返回 data URL 裸字符串（非 JSON）；force=false 走核心缓存优先
      const src = await api.cover(JSON.stringify(drama), false)
      return typeof src === 'string' ? src : ''
    } catch {
      return ''
    }
  })()
    .then((src) => {
      cachePut(id, src)
      return src
    })
    .finally(() => {
      inflight.delete(id)
    })
  inflight.set(id, task)
  return task
}

/** 队列预热（并发 4，fire-and-forget）：列表装载后调用，滚动到位即已有缓存。 */
export function primeCovers(dramas: readonly (Drama | null | undefined)[]): void {
  const queue = dramas.filter(
    (d): d is Drama => !!d && !!d.cover && !!d.id && cacheGet(d.id) === undefined
  )
  if (queue.length === 0) return
  let cursor = 0
  const step = (): Promise<void> => {
    const i = cursor++
    if (i >= queue.length) return Promise.resolve()
    return resolveCover(queue[i]).then(() => step())
  }
  void Promise.all(Array.from({ length: Math.min(4, queue.length) }, () => step()))
}
