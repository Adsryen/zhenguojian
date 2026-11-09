# v2 转型技术 spike 报告（Wails 方案 go/no-go）

日期：2026-11-09 ｜ 环境：Linux 本机（Go 1.27.3 @ ~/.local/go/bin, Node v22.22.3, 无 sudo/无 wine/无 mingw/直连 GitHub 不通）
执行人：spike 子代理。产物 = `client-v2/spike/` + `.github/workflows/v2-probe.yml`（未提交，禁 git 写）。

## 八项结果总览

| # | 项 | 结论 | 证据摘要 |
|---|-----|------|----------|
| 1 | Wails CLI 安装 | ✅ | `go install ...@latest` 一次成功（GOPROXY=goproxy.cn），CLI v2.17.1 → `~/go/bin/wails`（35MB） |
| 2 | 脚手架 | ✅ | `wails init -n hello -t vue` 成功（模板源 wails.io，无需 GitHub 代理）；首次报 "unable to find Go compiler" 系 PATH 无 go，export 后解决 |
| 3 | Linux 可开发性 | ⚠️→可行 | 本机 **无法** `wails build` Linux 目标：`pkg-config/webkit2gtk-4.0 缺失`（无 sudo，放弃）。但 vite 前端全通（`npm install`@npmmirror + `npm run build` 出 dist/ 16 modules）；Go 单测全通 |
| 4 | 交叉编译探测 | ✅（超预期） | **不需要 mingw**：Wails Windows 前端走纯 Go WebView2Loader（无 cgo）。本机实测 `wails build -platform windows/amd64 -s`（库 v2.17.1）→ 12,011,520B PE 头 OK 含前端资产；`GOOS=windows CGO_ENABLED=0 go build`（库 v2.10.1 pin）→ 6,666,752B 亦 OK。exe 未本机运行（无 wine），真机冒烟靠 CI 产物 |
| 5 | v2-probe.yml | ✅ | `.github/workflows/v2-probe.yml` 已写，python3-yaml 解析通过，LF 行尾。windows-latest：go 1.24 + node 22 + `cmd/wails@v2.10.1`（pin 理由见下）→ frontend `npm install` → `wails build -platform windows/amd64 -s` → upload-artifact hello.exe。**待主代理提交+触发** |
| 6 | engine 直调冒烟 | ✅ | `core.NewEngine` **不存在**——引擎构造 `newNativeEngine` 未导出；唯一导出入口 `core.NativeRequest(json)`（app_runtime.go:305）。spike 直调：initialize 0.0s → `catalog source=hongguo page=1` **51 items / 3.1s**（hasMore=true）；带 query "赘婿" **23 items**（localSearch=false，在线搜索）。v2 无需改 v1 |
| 7 | mpv 嵌入设计 | ✅（编译级） | `spike/mpvembed/`：跨平台 IPC 读写器（requestID 配对、跳异步事件/噪声行）+ windows-only 子窗口（CreateWindowExW WS_CHILD，手写 wndclassexw 布局，x/sys 未导出该类型）+ mpv 启动骨架（--wid/--input-ipc-server=\\.\pipe\zgj-spike）。`GOOS=windows go build ./...` + vet ✓；Linux net.Pipe 单测 2/2 ✓（success 应答 + error 应答）。真机验证未做 |
| 8 | 弹幕 ASS 骨架 | ✅ | `spike/danmaku/`：[]Item→ASS（[Script Info]/[V4+ Styles]/[Events] 三段、\move 滚动、#RRGGBB→&HBBGGRR&、按追尾模型简单分轨写 MarginV）。Linux `go test` 3/3 ✓（单条字段/同刻三轨+复用轨/空输入+乱序） |

## 关键决策与理由

- **CI 固定 wails v2.10.1，不用 @latest**：任务要求 CI go-version=1.24（对齐 zhenguojian/sidecar/go.mod），但 wails v2.17.1 库 go.mod 要求 `go 1.26.0` → 1.24 下会触发 toolchain 自动下载、违背对齐意图。v2.10.1 库要求 go 1.22.0，与 1.24 严配。hello/go.mod 已同步 pin v2.10.1。若未来想用新版 wails，须把 CI go 提到 ≥1.26 并接受与 sidecar 版本分叉（另行决策）。
- **CLI v2.10.1 在本地 go1.27 下构建报 `internal error: package "context" without types`**（旧内嵌 x/tools 不兼容新 export data）——只影响"旧CLI×新工具链"组合，CI 的 go1.24+v2.10.1 是经典组合不受影响；本地等价性已用纯 `go build`（1.27×库2.10.1）与 CLI2.17.1×库2.17.1 双路编译验证。
- **engine 接入面定为 NativeRequest JSON 分发器**：v2 壳直接 `core.NativeRequest(string)` 即可拿到全部 v1 能力（initialize/catalog/search/detail/danmaku/...），**无需 v2-1 给 core 加导出入口**；想要 typed API 时在 v2 侧做薄封装，不动 v1。
- spike 目录附 `.gitignore`（node_modules/、dist/、build/bin/、*.exe）——提交前主代理勿手动加入这些。

## 依赖请求（需其它工作流落地）

1. **主代理（立即）**：提交 `client-v2/spike/` 全部 + `.github/workflows/v2-probe.yml`，`workflow_dispatch` 触发 v2-probe，下载 hello.exe 在真 Windows（WebView2 运行时 runner 自带）冒烟：窗口出现、前端加载、Go 绑定 Ping 按钮可用。这是唯一未闭环的验证。
2. **v2-5（播放器实施）**：① mpv 嵌入用 mpv.exe（非 mpv.com，控制台闪窗）+ CREATE_NO_WINDOW（骨架已写 TODO）；② --wid 宿主 hwnd 与 Wails 主窗口的层级/尺寸同步需真机调试；③ 管道重试/退避在 v2 实施期补。
3. **v2-1（核心绑定）**：无 core 改动需求；若做 typed bindings 另立小项。

## go/no-go 结论：**GO**

- 最大风险点原本假设"Linux 开发机做不了 Windows 构建"——实测**证伪**：wails windows/amd64 无 cgo 依赖，本 Linux 机 5.4s 直出 12MB 可分发 exe，CI 只是可复现性加保险，不是硬通道。
- v1↔v2 数据链路实测通（3.1s 拿 51 条真实红果数据），v1 核心可原样复用，转型成本主要在 UI 壳。
- mpv 嵌入与弹幕 ASS 两条最"平台相关"的链路都以纯 Go 形态编译/测试通过，风险集中在真机嵌入效果（已列入依赖请求 1/2），无架构性障碍。
- 版本治理注意：wails 库版本会拽 go 工具链下限（2.17 要 1.26），monorepo 里要么统一升 1.26（牵动 sidecar），要么 pin 老版（当前选择）。

## 产物清单（路径相对仓库根）

- `client-v2/spike/.gitignore`
- `client-v2/spike/hello/`（wails vue 模板项目；go.mod 已 pin wails v2.10.1 + go 1.24.1；node_modules 与 build/bin 被 ignore）
- `client-v2/spike/go.mod`（module zgj/spike, replace duanjuapp/native => ../../guoapp-src/native）
- `client-v2/spike/enginesmoke/main.go`
- `client-v2/spike/mpvembed/{ipc.go,ipc_test.go,window_windows.go,player_windows.go}`
- `client-v2/spike/danmaku/{ass.go,ass_test.go}`
- `.github/workflows/v2-probe.yml`
