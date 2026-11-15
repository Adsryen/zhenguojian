/* 内嵌绑定的 mock/无桥面（真实桥侧见 ../bridge 的 embedBridge，组合逻辑在 facade ./api）。
   不进 NativeApi 面：这是播放页生命周期细节，mock/无桥/非 Windows 一律降级为
   no-op（attach 返回 embedded:false），PlayerView 无需按模式特判——内嵌不可得
   时整条播放链路保持现行行为（mpv 独立窗 / 浏览器无画面）。 */
import type { AttachResult } from '../contract'

export const mockEmbedApi = {
  attach: async (): Promise<AttachResult> => {
    // 验证钩子：headless 场景置 window.__zgjMockEmbed=true 谎报「内嵌可用」，
    // 驱动 PlayerView 的 bounds/hitBands 上报链路（真实 exe 恒 isMock=false）。
    // 顺手带回一份合成 host：徽标的判读字段（含"最上层是谁"）在真实桥之外
    // 也需要有人端到端验一遍**渲染出来了没有**——上一轮的教训就是
    // "字段在 Go 侧加了、前端没接"，那种错只有浏览器里才看得见。
    if (typeof window !== 'undefined' && window.__zgjMockEmbed) {
      return {
        embedded: true,
        reason: 'mock-embed',
        host: {
          supported: true,
          parentHwnd: '0xMOCK', parentClass: 'Chrome_WidgetWin_1',
          childHwnd: '0xMOCKC', childVisible: true,
          childLeft: 100, childTop: 50, childWidth: 780, childHeight: 600,
          probeX: 490, probeY: 350,
          topAtCenterHwnd: '0xMOCKT', topAtCenterClass: 'Chrome_RenderWidgetHostHWND',
          topIsChild: false, topContainsProbe: true
        }
      }
    }
    return { embedded: false, reason: 'mock-embed' }
  },
  setBounds: async (x: number, y: number, w: number, h: number, show: boolean): Promise<void> => {
    /* mock/无桥：no-op，但把上报序列记到 window.__zgjBounds 供 headless 断言
       （真实时序契约见 bridge.embedBridge 头注释：几何与 show 分开）。 */
    if (typeof window !== 'undefined') {
      const rec = window.__zgjBounds ?? { count: 0, first: null, last: null, hiddenWithGeometry: 0 }
      rec.count += 1
      if (!rec.first) rec.first = { x, y, w, h, show }
      rec.last = { x, y, w, h, show }
      if (!show && w > 1 && h > 1) rec.hiddenWithGeometry += 1
      window.__zgjBounds = rec
    }
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
  },
  forceWindow: async (): Promise<boolean> => {
    /* mock/无桥没有内嵌可放弃，返回 false 让调用方知道没生效 */
    return false
  }
}
