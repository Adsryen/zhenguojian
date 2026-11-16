/* mock 面（无桥降级同样经这里）的验证钩子在 Window 上的环境声明。
   三拆（2026-11-15）时从 types.ts 挪来：缝的完整清单见 ./index.ts 文件头。 */
import type { BoundsRecord, HitBandsRecord, SoraniLoginAttempt } from '../types'

declare global {
  interface Window {
    /** embedApi.playerHitBands 在 mock/无桥时的落点（真实 exe 不写） */
    __zgjHitBands?: HitBandsRecord
    /** embedApi.setBounds 在 mock 面的上报序列（真实 exe 不写） */
    __zgjBounds?: BoundsRecord
    /** mock 验证钩子：置 true 让 embedApi.attach 谎报 embedded:true，
        以驱动 PlayerView 的 setBounds/hitBands 上报链路（纯浏览器无副作用） */
    __zgjMockEmbed?: boolean
    /** mock 验证钩子：mockApi.suggestions 调用计数（联想防抖/门控断言用；真实桥不写） */
    __zgjSuggestCalls?: number
    /** mock 验证钩子：mockApi.openDownloadPath 最近一次路径 */
    __zgjOpenPath?: string
    /** mock 验证钩子：置 '1' 让 mockApi.soraniLogin 强制失败（M5 登录失败路径断言用） */
    __zgj?: { mockLoginFail?: string }
    /** mock 验证钩子：mockApi.soraniLogin 最近一次入参（M8 track 形状断言用；真实桥不写） */
    __zgjSoraniLastLogin?: SoraniLoginAttempt | null
    /* ── 第九十六轮（自愈合逃生 / 看门狗）── */
    /** mock 缝：置 true 让 mockEmbedApi.setBounds 的回执钉在 alpha.40 真机形状
        （applyCount=0 / childWidth=1）——自愈合 E2E 驱动「running 了但子窗
        从没被摆过」判定；真实 exe 不读 */
    __zgjMockNeverPlaced?: boolean
    /** mock 缝：置 true 让 mockEmbedApi.forceWindow 返回 true 并记 __zgjForceWindow
        （自愈合 E2E 断言「切独立窗真的发生了」）；真实 exe 不读 */
    __zgjMockForceWindow?: boolean
    /** mockEmbedApi.forceWindow 的调用计数（自愈合 E2E 断言用） */
    __zgjForceWindow?: number
    /** 看门狗时长覆盖（ms）：bridge.embedBridge.setBounds 的 pending 阈值；
        vitest 单测压到几百 ms；真实 exe 无此键=默认 8000 */
    __zgjBoundsWdMs?: number
  }
}
