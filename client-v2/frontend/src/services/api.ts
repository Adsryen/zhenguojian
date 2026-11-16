/* 绑定适配层 facade：组件只依赖本模块，永不直接触碰 window.go。
   2026-11-15 起实现拆三层，本文件只做**选用与组合**，对外形状（import 名与行为）与拆分前一致：
   - ./bridge   真实桥：window.go 探测、callGo/callGoRaw、错误码剥离约定、realApi、embedBridge
   - ./mock     演示实现与测试缝：mockApi（window.__zgj* 一族）；
                「只存在于 mock」的字段/行为清单在 ./mock/index.ts 文件头
   - ./contract 跨层形状：NativeApi、ApiError、Go 侧签名清单 GO_BINDINGS、
                入参槽位精确匹配检查（ExactApiImpl——机制与边界见该文件底部）
   - 真实模式：window.go.main.App.<Method>(...) 返回 JSON 字符串（Go 侧 (string, error)）
   - VITE_MOCK=1：fixtures 双实现，可在纯浏览器里验证 UI */
import type { EmbedSetStatus, AttachResult, NativeApi, EmbedDiagInfo } from './contract'
import { hasBridge, realApi, embedBridge, callGoRaw } from './bridge'
import { mockApi } from './mock'
import { mockEmbedApi } from './mock/embed'

const isMock = import.meta.env.VITE_MOCK === '1'

/* 降级策略：真实 exe 里桥必在；若 WebView2 初始化竞态导致 window.go 短暂缺失，
   首次访问时重试探测，仍缺失则回落 mock 面并亮横幅（DEGRADED），避免整页死锁。 */
export const DEGRADED = !isMock && !hasBridge
export const bridgeMissing = DEGRADED
export const api: NativeApi = isMock ? mockApi : hasBridge ? realApi : mockApi
export const MOCK_MODE = isMock

/* 旧命名再导出：调用方一行不改（'@/services/api' 的全部具名导出原位保留）。 */
export { ApiError, clampOpacityPercent } from './contract'
export type { EmbedHostDebug, AttachResult, EmbedDiagInfo } from './contract'
// 第九十七轮 alpha.42：IPC 账本摘要（复制诊断的 ipc= 行）。走 facade 出口，
// 组件层不直接 import bridge（与 embedApi 同纪律）。
export { ipcLedgerSummary } from './bridge'

/* ── V2-4 播放画面内嵌绑定（App.{PlayerAttach,PlayerSetBounds,PlayerHitBands,PlayerDetach}）──
   不进 NativeApi 面：这是播放页生命周期细节，mock/无桥/非 Windows 一律降级为
   no-op（attach 返回 embedded:false），PlayerView 无需按模式特判——内嵌不可得
   时整条播放链路保持现行行为（mpv 独立窗 / 浏览器无画面）。
   时序契约：PlayerAttach → PlayerSetBounds（真实几何 + show=false 起手摆位）
   → PlayerOpen（spawn 前 SetWid 生效）→ 状态变化重报 show → 离开 PlayerDetach。
   ⚠ 几何与可见性分开上报：只要量得出尺寸就摆位，show 只决定看不看得见。
   旧约定「零尺寸=隐藏」让隐藏态永远不摆位，mpv 于是拿到创建尺寸的 1×1 drawable。
   分支顺序与拆分前逐行一致：isMock → mock 面（./mock/embed）；无桥 → 直接降级；
   真桥 → ./bridge 的 embedBridge。 */
export const embedApi = {
  attach: async (): Promise<AttachResult> => {
    if (isMock) return mockEmbedApi.attach()
    if (!hasBridge) return { embedded: false }
    return embedBridge.attach()
  },
  setBounds: async (x: number, y: number, w: number, h: number, show: boolean): Promise<EmbedSetStatus> => {
    if (isMock) return mockEmbedApi.setBounds(x, y, w, h, show)
    // 无桥也要如实回报「没送出去」，别让调用方以为成功了
    if (!hasBridge) return { ok: false, error: 'no-bridge' }
    return embedBridge.setBounds(x, y, w, h, show)
  },
  /* 覆盖层穿透带（App.PlayerHitBands）：top/bottom=HTML 覆盖层与视频矩形
     的实际重叠高度（物理像素），0=该侧不禁。桥缺失/mock 记录到
     window.__zgjHitBands 供 headless 验证断言（该记录在 mock 面）。 */
  playerHitBands: async (top: number, bottom: number): Promise<void> => {
    if (isMock || !hasBridge) return mockEmbedApi.playerHitBands(top, bottom)
    return embedBridge.playerHitBands(top, bottom)
  },
  detach: async (): Promise<void> => {
    if (isMock || !hasBridge) return
    return embedBridge.detach()
  },
  forceWindow: async (): Promise<boolean> => {
    if (isMock) return mockEmbedApi.forceWindow()
    if (!hasBridge) return false
    return embedBridge.forceWindow()
  },
  /* 诊断面：exe 身份 + Go 侧 setBounds 到达计数（alpha.40 矛盾的分判器）。
     mock 面回合成形状（arrived=__zgjBounds 计数）；无桥回空形状。 */
  diag: async (): Promise<EmbedDiagInfo> => {
    if (isMock) return mockEmbedApi.diag()
    if (!hasBridge) return { version: '', revision: '', pid: 0, arrived: 0, logDir: '', logFail: 'no-bridge' }
    return embedBridge.diag()
  },
  /* 现场心跳（第九十七轮 alpha.42）：复制诊断前测一次真实 Ping 往返时长。
     Ping 是 0 参数绑定——alpha.41 指纹里 0 参数全通、5 参数全挂，现场 Ping
     直接回答「此刻 IPC 通不通」。mock/无桥回 -1（调用方显示 mock/no-bridge，
     不显示 timeout——timeout 专指真桥 2s 不返回）。 */
  pingLive: async (): Promise<number> => {
    if (isMock || !hasBridge) return -1
    const t0 = performance.now()
    await callGoRaw('Ping')
    return performance.now() - t0
  }
}
