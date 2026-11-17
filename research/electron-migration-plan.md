# v2 客户端 Electron 壳迁移方案（第九十八轮定案，2026-11-17）

## 0. 为什么换壳（决策依据，全部有执行级证据）

用户决策（m10416/m10433，选择题确认「换 Electron 壳（JavdBviewed 模式）」）：
Wails(WebView2) 壳里，视频=裸 Win32 `WS_CHILD` 子窗塞进 WebView2 窗口
（`client-v2/internal/mpv/embed_windows.go:278`），必须跟 WebView2 的
Chrome_RenderWidgetHostHWND 渲染层抢 z-order/命中 → alpha.40/41/42 三个真机
版本反复黑屏的结构性根源。

alpha.42 真机数据（用户 m10411 贴回，revision=15bf845 已自证）逐字段定案：
- `ping=pong2ms` + `diag.arrived=7` = IPC 传输通、exe 是真 alpha.42；
- `子窗 764×614`（不再是 1×1）= 几何/可见性分离修复生效；
- `ipc=PlayerSetBounds:7/2P5` + `上报异常=pending>8s` = **Go 侧 embedMu
  锁排队**：发了 7 次只有 2 次进函数体并执行，5 次等锁 >8s（新 bug，待修）；
- `该像素最上层=Chrome_RenderWidgetHostHWND=other` + `topContainsProbe=true`
  = **airspace**：WebView2 渲染层压在视频像素点上，mpv 就算画了也被盖住。
  这是 Wails 壳的结构性问题，任何 Go 侧修补都绕不开。
- `该显示=0(r1 l0 e1 f0)` + `PlayerPlay:5/5e5` = 用户在黑屏画面上反复点播放，
  控制调用全返回「尚未打开播放会话」。

JavdBviewed 对照（读了源码：`JavdBviewed-project/JavdBviewed-Clients/apps/desktop`）：
Electron 37 壳，视频=独立自管无边框窗 `new BaseWindow({parent, frame:false,
skipTaskbar:true})`（`src/main/nativeVideoSurface.ts`）+
`getNativeWindowHandle()` 取 XID/HWND + mpv `--wid=<handle>`
（`src/main/nativePlayer/mpvHost.ts`）+ `setIgnoreMouseEvents(true,
{forward:true})` 让鼠标穿透回 webview + 父窗 9 事件重摆。视频窗是**顶层独立
窗**，不与任何 webview 共享渲染层 → 结构性无 airspace。

## 1. P0 地基验证（本机 Xvfb :99，全部已过）

| 验证项 | 结果 |
|---|---|
| Electron 37.2.0 安装 | `npm i electron@37.2.0`（registry.npmmirror.com + ELECTRON_MIRROR），201MB 二进制，22s |
| 最小 BrowserWindow | 起窗+JS 执行+capturePage，`PROBE_OK title=electron-probe body=真果鉴 v2·Electron 壳探测` |
| **命门：BaseWindow + mpv --wid** | 无边框自管窗取 XID `0x200008` → mpv `--wid --vo=x11 --loop /tmp/testsrc.mp4` → 区域截图 **16563 唯一色、mean=0.49**（占位 UI 单色 #111 背景不可能产生；0.49 中性灰=testsrc 彩条特征）→ 画面真进了自管窗 |
| Electron 依赖库 | 全在系统（libnss3/libssl3/libgtk-3/libgbm/libasound/libatk/libcups/libxkbcommon 均在 /lib/x86_64-linux-gnu；ldconfig 索引只是过期） |

探测脚本在 `/tmp/electron-probe/`（main.js 最小窗、embed.js 命门测试、
embed-shot.png 截图证据）。

## 2. 关键发现：v1 已有现成 sidecar 架构

`v1-final` tag 下 `zhenguojian/sidecar/main.go`：**纯 Go 独立进程**，
stdin/stdout JSON 行协议（`{id, action, ...}` 进 → 合入 id 的 reply 出，
6 并发槽，goroutine 每请求）；v1 Python 主进程 `core_client.py` 用
`subprocess.Popen` 驱动 + 崩溃自拉起。核心模块 `duanjuapp/native`
（= `../guoapp-src/native`，172 文件，独立 go.mod）除 `bridge/main.go`
一个 cgo 导出文件外**全纯 Go**。

⇒ v2 要做的不是发明 sidecar，而是把 **v2 的 Go 业务核心（整个 internal/
树）挂到 stdio 协议上**，驱动器从 Python 换成 Electron 主进程。

## 3. 目标架构

```
┌─ Electron 主进程 (TS) ──────────────────────────────────┐
│  sidecarChild = spawn("sidecar.exe", stdio 行协议)       │
│    ↑↓ JSON 行 {id,action,args…} / {id,reply} / {event}  │
│  视频面: BaseWindow(frame:false) → handle → 传给 Go      │
│  父窗 move/resize/focus 跟踪 → setBounds 重摆            │
│  setIgnoreMouseEvents(true,{forward:true}) 鼠标穿透      │
│  contextBridge preload:                                 │
│    window.go.main.App.<80 方法> = () => Promise<string>  │
│    window.runtime.{EventsOn,EventsOff,EventsEmit,       │
│                     WindowFullscreen,WindowUnfullscreen, │
│                     Quit}                                │
└──────────────┬──────────────────────────────────────────┘
               │ 渲染进程 = 现有 Vue SPA（vite build dist/）
┌──────────────┴───────────┐   ┌────────────────────────────┐
│ bridge.ts 零改动           │   │ Go sidecar（v2 internal/ 全保留）│
│ window.go.main.App 形状   │   │  App 结构体不变              │
│ 兼容（preload 同形实现）    │   │  80 绑定方法 → 协议 dispatch  │
│ VITE_MOCK 路径不变         │   │  EventsEmit → {event} 行    │
│ 30 只 E2E(mock 桥) 不变    │   │  新: videoSurface 动作收 handle│
└──────────────────────────┘   │  mpv --wid=handle spawn      │
                               └────────────────────────────┘
```

### 前端几乎零改动的关键
- `bridge.ts` 只认 `window.go?.main?.App`（method→Promise，错误=字符串带
  ` [code]` 尾）——preload 按**完全相同的形状**实现 80 个方法（方法名清单
  唯一来源 `frontend/src/services/contract.ts` GO_BINDINGS），bridge.ts
  /api.ts/contract.ts 一个字符不改，vitest 49 条原样过。
- 事件总线：player store 找 `w.wails?.EventsOn ? w.wails : w.runtime`，
  PlayerView 用 `window.runtime?.WindowFullscreen` → preload 暴露
  `window.runtime` shim 即覆盖全部 19 处 EventsOn/5 处 EventsOff/
  全屏/Quit。
- mock 面（VITE_MOCK=1）是浏览器原生的，与壳无关 → **30 只 Playwright E2E
  继续对 `vite --mode mock` 跑，一行不改**，仍是前端第一道防线。

### 被结构性删掉的 Wails 时代包袱（黑屏根因所在）
- `findHostWindow`（Windows EnumWindows 找父 / X11 _NET_CLIENT_LIST+
  QueryTree+pid 匹配+WM reparent 探测）——**整个消失**：Electron 直接把
  视频窗 handle 告诉 Go，不再「猜哪个窗是父」。
- WS_CHILD 子窗 + WndProc 注册 + airspace 对抗（research 22 节判读表、
  探针、最上层直读）——视频窗改顶层独立窗，无渲染层冲突。
- 自愈合逃生门（alpha.42 加的 forceWindow 兜底）不再需要——没有「内嵌
  失败」这个状态了，只有「视频窗没摆上」。

### 保留并搬进 Electron 的
- `internal/` 全部业务（player/downloadsvc/watchstore/updater/sorani/
  danmaku/cover/…）原样；`App` 结构体原样；80 个绑定方法体原样。
- hitbands（穿透带）：第一阶段用 `setIgnoreMouseEvents(true,{forward:
  true})` 整窗穿透（前端 stage div 本来就有 @click 处理播放控制），
  WndProc 钩子版 hitbands 作为 P3 增强项（可选）。
- app_log.go 取证面（arrived/lock 采样/wails 日志白名单）保留，日志源
  从 wailslogger 换成协议层的到达日志。

## 4. 分阶段与门禁（每阶段本机可执行验证，不盲建）

- **P1 协议+sidecar**：v2 Go 侧新 `sidecar` 入口（build tag 或独立 main），
  `App` 绑定方法 → stdio JSON 行 dispatch（id 关联、并发、错误=字符串带
  code、事件下行行）。门禁：Go 单测（协议往返/80 方法全量反射枚举防漏/
  并发 id 不串）；`go-local.sh` 全绿。
- **P2 Electron 骨架**：electron 主进程（spawn sidecar+崩溃自拉起+IPC
  桥+preload shim）+ 现有 vite 产物加载。不引 electron-vite（现有 vite
  配置直接复用，主进程 TS 用 esbuild 编）。门禁：Xvfb 起真 UI，8 页导航
  全过（复用 smoke_nav 蓝行判据），截图证据；vue-tsc/vitest/E2E 30 只原样绿。
- **P3 视频面**：BaseWindow+handle→Go→mpv --wid，父窗事件重摆，鼠标穿透。
  门禁：Xvfb 截图视频区非单色（彩条判据，同 P0）；Windows 侧靠 CI
  （TestEmbedHostRuntimeRealWindows 同款思路重写为 surface 测试）。
- **P4 门禁迁移**：verification/ 预检体系适配（mock E2E 不变；真桥 E2E
  改走 Electron CDP 或先冻结标注）；session_state/evidence_audit 适配
  「80 绑定=sidecar 方法表」。
- **P5 发布**：electron-builder（win x64 + linux）、latest.json 更新链
  移植（electron-updater 或自研下载）、CI 双 job 改写、release-check
  适配。
- **并行独立小修（不等换壳）**：`embedMu` 锁排队 bug（alpha.42 数据
  `7/2P5` 实证）——即使换壳期间也要修，因为 Wails 版还会继续发；修完
  可发 alpha.43 让用户至少拿到「不排队」的版本。

## 5. 风险与诚实边界
- Electron 打包体积（~100MB+）> Wails 单 exe——用户已知情选择。
- 本机 Electron 验证的是 Linux 路径；Windows HWND 嵌入（8 字节 handle）
  仍只能靠 CI windows job 真跑（同 alpha.41 的 TestEmbedHostRuntime
  RealWindows 教训：只在某平台能跑的必须在目标平台 CI 真执行）。
- sidecar 崩溃=整个业务面失效，必须有自拉起+前端降级横幅（v1
  core_client.py 的 respawn 逻辑是现成参考）。
- 迁移期间 Wails 版线继续维护（小修照发），直到 Electron 版整链验证通过
  才停。
