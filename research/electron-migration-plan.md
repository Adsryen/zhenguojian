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

- **P1 协议+sidecar（已完成，9315351）**：v2 Go 侧新 `sidecar` 入口
  （build tag `zgj_sidecar` 双入口，`!zgj_sidecar`=Wails 原样），
  `App` 绑定方法 → stdio JSON 行 dispatch（id 关联、32 并发槽、错误=
  字符串带 code、事件下行行）。反射表 89 方法，kind 三分支
  （(string,error)/string/error）——首版漏 7 个非双返回绑定（手写判据
  漏项教训第 5 次）；receiver 不传=运行期 panic（`Type.Method.Func`
  NumIn 多 1）。门禁实跑：Go 单测三条（形状钉名/contract.ts 82 App.X
  ⊆ 反射表机器对账/端到端真跑 runStdioSidecar）；`go-local.sh` 全绿。
- **P2 Electron 骨架（已完成，35bcc83）**：electron 主进程（spawn sidecar+
  崩溃自拉起×5+30s 调用超时+桥 89 方法经 `__methods` 同步）+ 现有 vite 产物
  加载。不引 electron-vite（现有 vite 配置直接复用）。关键修：file:// 白屏
  =vite `base:'/'`→`'./'`；contextBridge 克隆不了 Proxy=改 `Object.create(null)`
  普通对象（方法名经 env 注入）。flake 根因=sidecar 8 并发槽被 9 个无网
  Cover（各挂 30s）占满饿死 Ping→slots 32。门禁实跑：selftest 桥 6 项+
  8 页导航蓝行判据全过，两次连绿。
- **P3 视频面（已完成，803b554）**：实现=主进程**拦截** 6 内嵌方法
  （PlayerAttach/SetBounds/HitBands/Detach/ForceWindow/EmbedDiag），
  VideoSurface（BaseWindow）承载几何与可见性；Go 侧只加
  `__embed_set_wid`/`__embed_get_wid` 私有协议方法+`EmbedWid()` getter，
  XID 经协议给 `playerSvc.SetWid`，mpv spawn 用它 `--wid`。前端零改动。
  坐标语义（探针实测）：`BaseWindow.setBounds` 是**屏幕绝对**坐标，
  `place()` 换算 `screen=contentBounds+rel`（前端报视口×dpr 物理像素）。
  门禁实跑：selftest 段 C 判据链=pinia running → `pgrep mpv` 的
  `--wid==surfaceXid`（0x20000c 实锤）→ 视频区**两帧差分** 627083 像素
  变化（色数判据被首跑证伪：整区 14995 色但中心恒 32 色，分不清
  没画与量到 testsrc 静态区）；xwininfo 窗树 surface=stage 原样
  1079x597@200,91。Windows 侧（HWND 8 字节分支）已 CI 实锤
  （electron-win-package run6 @ae4ad49 全绿：mpv_wid
  surfaceXid==mpvWid=0x2021a + NSIS 打包产物自检 16 PASS；像素差分
  两项 SKIP 待真机——CI 无法证明「人眼见画」，最终判据=用户实机）。
  踩坑沉淀：打包 exe 是 GUI 子系统（PE subsystem=2），pwsh `&` 不等待
  GUI 进程直接返回，自检步五连败的「json 缺失」实为检查跑在 app 刚
  启动 2s 时（自检要 30s+）——必须 Start-Process -PassThru +
  WaitForExit 显式等（npx/node 与 Linux bash 都会等子进程，唯独这条
  不等）。
  打包态 Linux 视频判据补实（2026-11-18）：`out/linux-unpacked --selftest`
  在 Xvfb 上 pixel 两项曾黑/绿交替——最终实锤为测试显示器竞态而非产品/介质
  问题：同一旧 444 文件既绿又黑，且绿 run 里运行中**直截 mpv 窗**（xwininfo
  找到 0x…("x11" "mpv") 窗 id → import -window）=15521 色、root 合成视频区
  =14995 色，两帧差 626966 PASS。生成侧已统一 `-pix_fmt yuv420p`
  （build-linux.sh + Windows CI workflow 同参，保守介质卫生，与黑绿无关）。
  新增执行级桥计数钉 `bridge_count`（preload 键数===sidecar 反射表数，
  expected 从 ZGJ_BINDINGS_JSON 实读、不写死——G4 潜伏红教训），dev/打包态
  均实测 `反射表=89 preload=89 PASS`。
- **P4 门禁迁移**（✅ 2026-11-18 完成）：verification/ 预检体系适配（mock E2E 不变；真桥 E2E
  改走 Electron CDP 或先冻结标注）；session_state/evidence_audit 适配
  「80 绑定=sidecar 方法表」。
  落地实况：30 个 *_verify.py 经全量侦察没有一个起过真 Wails（27 纯 mock +
  3 个「非 mock vite + window.go stub」双轨，stub 形状=Electron preload，
  壳无关继续有效），所以「冻结标注」分支 = 只加注释不改代码，真桥面由
  electron selftest + sidecar_protocol_test.go 覆盖；CDP 真桥 E2E 本体系
  从无（非迁移债），留 P5 后新增能力。
  evidence_audit 判据换桥面：方向 A = 前端调用名 ⊆ sidecar 反射表静态镜像
  （新增方向 S 三种形状 (string,error)/string/error，「Go 有但没进桥」在
  Electron 里同样报「原生方法不存在」，旧 Go 面判据会静默放行）；
  S 段 contract.ts 登记面（主表 77+EMBED 5）⊆ 桥面（实测 89）进 run_all
  预检硬拦。踩坑沉淀：G4「==4」是 P3 加 diag 后的过期写死数，--selftest
  不在 run_all 链上潜伏红无人见（62 轮全量才暴露）；bridge.test.ts 假桥桩
  Foo/NoSuchMethod 被 callGo 解析器扫入造成方向 A 假红——fixture 必须排除。
- **P5 发布**：electron-builder（win x64 + linux）、latest.json 更新链
  移植（electron-updater 或自研下载）、CI 双 job 改写、release-check
  适配。
  CI 执行链实况（run7–run10，2026-11-18）：
  - Windows 像素判据走 **GDI CopyFromScreen**（capturePage 截不到 airspace
    子窗、CI 无 ImageMagick）：主进程取 surface win.getBounds 屏幕 rect →
    pwsh System.Drawing 抓 BMP → Node 自制解码差分/色数。run7 首验取数轮
    暴露 `System.Drawing.Bitmap` 构造默认 32bpp（Save Bmp 保真），解码器
    只认 24bpp 三连失败——修：ps1 显式 Format24bppRgb + 解码器 24/32 双路
    （BI_RGB 小端 BGRA→RGB）+ 失败时 BMP 头 hexdump 进 detail（d5a972c）。
    本机对拍 ImageMagick `compare -metric AE` 逐数一致（448）。run8 实证
    两帧差=51078、uniqueColors=8102（双阈 100/32 的 500/250 倍）→ 转正
    断言（474d146）；几何不可得分支保留 skip。
  - NSIS 安装链冒烟（`/S` 装→bundle 布局断言→装后 --selftest→卸载断言，
    放 Upload 之后红不挡 artifact）run8 首红：installer exit=0 但 exe 不在
    安装目录——**NSIS Setup.exe 双 spawn**（复制自身到 temp 再起子进程继续
    装，`Start-Process -Wait` 只等外壳，500MB 解压未完即断言；与 pwsh 不等
    GUI 子系统同族坑）。修：装后 180s 轮询 exe 就位、卸载后 120s 轮询等
    消失，超时 dump 目录树定案（474d146）。run9 dump 再定案一层：竞态
    已消化但仍红——目录树显示文件完整装在 Programs\zgj-v2：
    electron-builder NSIS 安装目录取 **executableName** 不取 productName
    （卸载器 "Uninstall zgj-v2.exe" 同源佐证），候选探测修复（9185171）。
    **run10（44240361456）= 首个全能力硬断言全绿**：packaged selftest
    18 PASS/0 SKIP（GDI 转正两项 51059/8099 实测绿）+ NSIS 冒烟
    「装→bundle→装后 selftest 整套再 PASS→卸载」全绿；artifact
    zhenguojian-v2-electron-win-x64-918517183f…（499MB）为实机推荐包。
  - 更新链三分支决策（A 静默重装 / B electron-updater / C unpacked 换目录，
    推荐与排除理由见 research/electron-updater-options.md）：run10 冒烟
    已全绿，只剩用户实机反馈后拍板；正式更新源需首个 GitHub Release tag
    （届时征求用户同意——用户纪律：不打 alpha tag，artifact 即交付）。
    联动提醒：scripts/release-check.sh:22 `EXPECTED_ASSETS` 与 latest.json
    exe 契约（:96-137）写死的是 Wails 三件套——选 A 则同步改 assets.installer
    + 5 资产清单，这一步属于更新链落地的同一批提交，勿漏。
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
