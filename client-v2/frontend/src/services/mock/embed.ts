/* 内嵌绑定的 mock/无桥面（真实桥侧见 ../bridge 的 embedBridge，组合逻辑在 facade ./api）。
   不进 NativeApi 面：这是播放页生命周期细节，mock/无桥/非 Windows 一律降级为
   no-op（attach 返回 embedded:false），PlayerView 无需按模式特判——内嵌不可得
   时整条播放链路保持现行行为（mpv 独立窗 / 浏览器无画面）。 */
import type { AttachResult } from '../contract'

export const mockEmbedApi = {
  attach: async (): Promise<AttachResult> => {
    // 验证钩子：headless 场景置 window.__zgjMockEmbed=true 谎报「内嵌可用」，
    // 驱动 PlayerView 的 bounds/hitBands 上报链路（真实 exe 恒 isMock=false）。
    return { embedded: typeof window !== 'undefined' && !!window.__zgjMockEmbed, reason: 'mock-embed' }
  },
  setBounds: async (_x: number, _y: number, _w: number, _h: number): Promise<void> => {
    /* mock/无桥：几何上报 no-op（真实时序契约见 bridge.embedBridge 头注释） */
  },
  /* 覆盖层穿透带：mock/无桥时记录到 window.__zgjHitBands 供 headless 验证断言。 */
  playerHitBands: async (top: number, bottom: number): Promise<void> => {
    if (typeof window !== 'undefined') {
      const rec = window.__zgjHitBands ?? { count: 0, last: null }
      rec.count += 1
      rec.last = { top, bottom }
      window.__zgjHitBands = rec
    }
  },
  detach: async (): Promise<void> => {
    /* mock/无桥：no-op */
  }
}
