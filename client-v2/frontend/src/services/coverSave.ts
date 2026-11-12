/* ================================================================
   coverSave.ts —— 详情页「保存高清海报」（v1 main.py:1485-1530 + media.py:136-150 对齐）

   v1 的四步语义逐字搬过来：
     1) 列表/详情平时用 400px 小图；只有「保存海报」才单独去要同一张图的原图。
     2) 高清地址不能改签名再拼——封面 URL 的 x-signature 改一个字符就 403。
        可行的是**无签名 CDN 的模板地址**：从 /novel-pic/<32hex> 抽资源号，
        拼 `https://<host>/novel-pic/<asset>~tplv-shrink:1600:0.image`
        （宽度给 1600 时服务端直接回原图尺寸，实测 400x572 → 700x1000，像素 3 倍），
        主机按 p6 → p3 顺序试。正则/模板/主机顺序与 v1 media.py:136-150 逐字一致。
     3) 候选全部失败要**回落小图**并且把这件事说出来（v1 状态栏
        「（没取到高清版，用的小图）」），不是静默给一张小图。
     4) 落盘文件名 `<safe_name(title)> 海报.jpg`，safe_name 是 v1 decrypt.py:23-40
        （非法字符→_、strip(" .")、60 字上限、Windows 保留名前缀加 _、空→「短剧」）。

   保存通道（零 Go 方案，见任务 notes R5b）：
     - 首选 File System Access `showSaveFilePicker`（Chromium/WebView2 支持）：
       用户能选目录和文件名，最接近 v1 的 QFileDialog；
     - 不支持或被拦（headless、老 webview、策略拒绝）时回落 `<a download>`，
       由 webview 的下载目录接管；
     - 用户取消（AbortError）与真失败分两种结果，不混成「保存失败」。
   HEIC→JPEG 的转码不在这里做：Go 侧 App.Cover 已经转码成 data URL
   （bindings_cover.go:60，coverFFmpegArgs 逐字对齐 v1 media.export_cover .jpg 分支）。
   ================================================================ */

import { api, DEGRADED, MOCK_MODE } from './api'
import type { Drama } from './types'

/** v1 media.py:136 `_COVER_ASSET`：从封面 URL 抽 32 位资源号。 */
const COVER_ASSET = /\/novel-pic\/([0-9a-f]{32})/i

/** v1 media.py:138 `_COVER_HIRES_HOSTS`：顺序即尝试顺序。 */
const COVER_HIRES_HOSTS = ["p6-novel.byteimg.com", "p3-novel.byteimg.com"]

/** v1 `hi_res_cover_urls`：同一张图的高清候选地址；认不出资源号返回空表。 */
export function hiResCoverUrls(cover: string): string[] {
  const match = COVER_ASSET.exec(cover || "")
  if (!match) return []
  const asset = match[1]
  return COVER_HIRES_HOSTS.map((host) => `https://${host}/novel-pic/${asset}~tplv-shrink:1600:0.image`)
}

/** v1 decrypt.py:23-28 非法字符表 + Windows 保留名（老内核/DOS 层解析会炸）。 */
const ILLEGAL = /[\\/:*?"<>|\x00-\x1f]/g
const RESERVED = new Set(["CON", "PRN", "AUX", "NUL",
  ...Array.from({ length: 9 }, (_, i) => `COM${i + 1}`),
  ...Array.from({ length: 9 }, (_, i) => `LPT${i + 1}`)])

/** v1 decrypt.safe_name(value, fallback="短剧", limit=60) */
export function safeName(value: string, fallback = "短剧", limit = 60): string {
  let text = (value || "").replace(ILLEGAL, "_").trim().replace(/[ .]+$/g, "").replace(/^[ .]+/g, "")
  text = text.slice(0, limit).replace(/[ .]+$/g, "")
  if (!text) return fallback
  if (RESERVED.has(text.split(".")[0].toUpperCase())) text = "_" + text
  return text.slice(0, limit)
}

/** v1 main.py:1496 suggested 的文件名部分：`<剧名> 海报.jpg` */
export function posterFileName(title: string): string {
  return `${safeName(title)} 海报.jpg`
}

export type PosterSaveResult =
  | { kind: "saved"; hires: boolean; size: string }
  | { kind: "cancelled" }
  | { kind: "no-source" }
  | { kind: "failed"; message: string }

/** data URL / 普通 URL → Blob（App.Cover 给的是 data URL；mock 给的是本地 svg 路径）。 */
async function toBlob(src: string): Promise<Blob> {
  if (src.startsWith("data:")) {
    const [head, body] = src.slice(5).split(",")
    const mime = head.split(";")[0] || "image/jpeg"
    if (/base64/i.test(head)) {
      const bin = atob(body)
      const bytes = new Uint8Array(bin.length)
      for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
      return new Blob([bytes], { type: mime })
    }
    return new Blob([decodeURIComponent(body)], { type: mime })
  }
  const res = await fetch(src)
  if (!res.ok) throw new Error(`读取海报失败（HTTP ${res.status}）`)
  return await res.blob()
}

type SaveFileHandleWindow = Window & {
  showSaveFilePicker?: (opts: unknown) => Promise<{ createWritable: () => Promise<{ write: (d: Blob) => Promise<void>; close: () => Promise<void> }> }>
}

/** v1 用 ffprobe 拿宽高（media.probe）；这里用解码器等价拿到，不新增 Go 绑定。 */
async function imageSize(blob: Blob): Promise<string> {
  try {
    const bmp = await createImageBitmap(blob)
    const size = `${bmp.width}x${bmp.height}`
    bmp.close?.()
    return size
  } catch {
    return ""
  }
}

/** 落盘：picker 优先，回落 `<a download>`。AbortError → cancelled（不算失败）。 */
async function saveBlob(blob: Blob, filename: string): Promise<"saved" | "cancelled"> {
  const picker = (window as SaveFileHandleWindow).showSaveFilePicker
  if (typeof picker === "function") {
    try {
      const handle = await picker({
        suggestedName: filename,
        types: [{ description: "海报图片", accept: { "image/jpeg": [".jpg", ".jpeg"] } }],
      })
      const writable = await handle.createWritable()
      await writable.write(blob)
      await writable.close()
      return "saved"
    } catch (e) {
      if ((e as { name?: string })?.name === "AbortError") return "cancelled"
      /* 其余（策略拒绝/异常）不致命：继续走下载通道 */
    }
  }
  const url = URL.createObjectURL(blob)
  try {
    const a = document.createElement("a")
    a.href = url
    a.download = filename
    a.rel = "noopener"
    document.body.appendChild(a)
    a.click()
    a.remove()
  } finally {
    // 立刻 revoke 会让部分 webview 拿不到数据，交出去再延后释放
    setTimeout(() => URL.revokeObjectURL(url), 4000)
  }
  return "saved"
}

/**
 * 保存海报全流程。失败语义与 v1 一致：
 *   - 候选高清逐个试（api.cover 带替换后的 cover + force=true）；
 *   - 全失败回落小图，hires=false 由调用方把「没取到高清版」说出来；
 *   - 连小图都拿不到 → no-source（v1「没拿到这张海报的原图，请先在剧库里刷新一次」）；
 *   - MOCK/DEGRADED 没有 Go 转码链路，直接用 drama.cover（与 covers.ts:119 同一分支判断），
 *     必然 hires=false。
 */
export async function savePoster(drama: Drama): Promise<PosterSaveResult> {
  const cover = drama.cover || ""
  let src = ""
  let hires = false
  try {
    if (MOCK_MODE || DEGRADED) {
      src = cover
    } else {
      for (const candidate of hiResCoverUrls(cover)) {
        try {
          const got = await api.cover(JSON.stringify({ ...drama, cover: candidate }), true)
          if (typeof got === "string" && got) {
            src = got
            hires = true
            break
          }
        } catch {
          /* 单个候选失败按 v1 继续试下一个 */
        }
      }
      if (!src) {
        const small = await api.cover(JSON.stringify(drama), false)
        if (typeof small === "string" && small) src = small
      }
    }
  } catch (e) {
    return { kind: "failed", message: String((e as Error)?.message ?? e) }
  }
  if (!src) return { kind: "no-source" }

  try {
    const blob = await toBlob(src)
    const size = await imageSize(blob)
    const outcome = await saveBlob(blob, posterFileName(drama.title || ""))
    return outcome === "cancelled" ? { kind: "cancelled" } : { kind: "saved", hires, size }
  } catch (e) {
    return { kind: "failed", message: String((e as Error)?.message ?? e) }
  }
}
