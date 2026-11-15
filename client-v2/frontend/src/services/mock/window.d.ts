/* mock 面（无桥降级同样经这里）的验证钩子在 Window 上的环境声明。
   三拆（2026-11-15）时从 types.ts 挪来：缝的完整清单见 ./index.ts 文件头。 */
import type { HitBandsRecord, SoraniLoginAttempt } from '../types'

declare global {
  interface Window {
    /** embedApi.playerHitBands 在 mock/无桥时的落点（真实 exe 不写） */
    __zgjHitBands?: HitBandsRecord
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
  }
}
