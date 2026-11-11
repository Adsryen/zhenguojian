# V-TRAY 真机验证点清单（系统托盘 + 托盘检查更新）

构建：`client-v2` Windows 产物（Wails v2.10.1 纯 go build 管线，CGO_ENABLED=0 已绿）。
托盘默认开启、零设置项；非 Windows（Linux 开发机）自动降级：`TrayStatus()` 返回 `unsupported: 当前平台不支持托盘`，X 保持旧行为直接退出。

## 1. 托盘图标外观
- [ ] 启动后通知区出现「真果鉴」图标，**应为应用图标**（exe 资源 RT_GROUP_ICON id=3 的 16px 小图），不是通用程序白图标。
- [ ] 悬停 tooltip = `真果鉴`。
- [ ] 若显示为通用图标：查 `LoadImageW(MAKEINTRESOURCEW(3), IMAGE_ICON, SM_CXSMICON…)` 是否被资源层级回落（代码三级回落：exe id3 小尺寸 → exe id3 默认尺寸 → IDI_APPLICATION）；可临时 `TrayStatus()` 确认 running 排除注册失败。
- [ ] 高 DPI（150%/200% 缩放）下图标不糊、不超格（SM_CXSMICON 感知）；Win11 隐藏托盘角展开正常。

## 2. X = 隐藏进托盘（OnBeforeClose）
- [ ] 点主窗 X：窗口消失、**进程不退**（任务管理器可见），托盘图标在。
- [ ] 隐藏期间：下载/合并任务继续（开一个下载后关窗，进度事件恢复窗口后仍正确）；mpv 若正播放——预期行为是隐藏窗口同时**音频继续**（同 v1 语义）；如需停播属产品决策，暂记风险。
- [ ] 隐藏后从任务栏/开始菜单再次启动 exe：会起第二实例（**无单实例锁，已知遗留风险**，见 §8）。

## 3. 双击托盘图标 = 恢复主窗
- [ ] 隐藏态双击托盘图标：主窗恢复并置前（含最小化态：RestoreWindow + SetForegroundWindow）。
- [ ] 单击：无动作（Windows 约定单击不响应，双击恢复；如产品要单击也恢复，改 tray_windows.go 的 WM_LBUTTONDBLCLK 分支加单击计时）。

## 4. 右键菜单三项
- [ ] 右键弹原生菜单：**显示真果鉴 / 检查更新 / 退出程序**（中文字面量，英文态豁免——原生 Win32 菜单读不到 Vue i18n）。
- [ ] 菜单在光标处弹出且不被任务栏遮住（GetCursorPos + SetForegroundWindow 前置 + TPM_BOTTOMALIGN；Win11 通知区弹出位置应贴合图标）。
- [ ] 「显示真果鉴」= 同双击恢复；「检查更新」见 §5；「退出程序」= 真退出（进程结束，托盘图标消失）。
- [ ] 菜单展开期间再点桌面 = 菜单自动收起无副作用（宿主为隐藏顶层窗+SetForegroundWindow 经典补丁生效；曾用消息窗方案此条必挂，已修正）。

## 5. 托盘「检查更新」
- [ ] 有新版：主窗顶部横幅出现（update:available，复用 App.vue 现有 go/skip），**且**原生 MessageBox 弹「发现新版本 vX.Y.Z，可在主窗口顶部横幅下载更新。」（标题「真果鉴」，MB_ICONINFORMATION）。主窗隐藏时也能收到弹窗（这是隐藏态的唯一反馈，勿省）。
- [ ] 已最新：原生 MessageBox「当前已是最新版本（vX.Y.Z）」；30 秒内连点第二次加「·30 秒内沿用上次结果」（Check 冷却复用，不打网络）。
- [ ] 已跳过该版本：「版本 vX.Y.Z 已被跳过，解除忽略前不会提醒。」
- [ ] 断网点检查：「检查更新失败：<原因>」，程序不崩，恢复网络后再点可成功。
- [ ] 连点/双击菜单项多次：无卡死（检查在转发协程执行，托盘消息循环不阻塞）；结果框可能叠加属预期。
- [ ] 前端「启动自动检查」开关 + 3s 静默检查与托盘互不干扰（同一 Service 互斥锁）。
- [ ] 后台周期复查（autocheck 扩展语义，Go 侧 ticker）：启动查一次 + 此后每 6h 静默复查；命中新版只发横幅事件（update:available），**不**弹原生框（弹窗专属于托盘手动入口）。关闭开关=当轮跳过（不查不发），重开后下一轮恢复；连开连关不重复起循环（startupOnce）。周期在单测注入 40ms 验证 ≥3 轮与 StopAutoCheck 即停。

## 6. 退出与清理
- [ ] 托盘「退出程序」→ 进程数秒内完全退出，**任务管理器无残留 zhenguojian.exe、无 mpv.exe 孤儿**（OnShutdown → trayH.Stop → downloadSvc.OnShutdown → playerSvc.Close 链路）。
- [ ] 退出时托盘图标立即消失（NIM_DELETE）； explorer 崩溃重启场景见 §9。
- [ ] 更新「应用并重启」：bat 轮询等到进程真退出并完成 exe 替换（UpdateApply 路径已补 allowQuit 旗标，否则会被 beforeClose 拦成隐藏——回归必测）。
- [ ] 主窗 X（隐藏）后**不能**再靠 Alt+F4 退出？——Alt+F4 同 WM_CLOSE 也是隐藏；唯一退出入口 = 托盘菜单/任务管理器（产品确认接受）。

## 7. 降级矩阵
- [ ] Linux 开发机构建运行：不弹托盘、日志 `[zhenguojian] tray: 当前平台不支持托盘`、X 直接退出（beforeClose 对非 running 放行）；`go test ./internal/tray/ ./internal/updater/ -race` 绿。
- [ ] Windows Server 2019/老 shell：NIM_ADD 用 NOTIFYICONDATA V1 尺寸（cbSize=96 路径）兼容 Shell5；图标小尺寸缺失时回落 IDI_APPLICATION 不报错。
- [ ] 用户禁用通知区图标/「其他图标」折叠：功能不受影响（托盘宿主窗独立于 shell 可见性）。

## 8. 遗留风险
- **多实例互踩**：无单实例锁。双实例各自 NIM_ADD（uID=0 由 shell 按 hwnd 分配，不冲突），但两个进程会各弹横幅/结果框，且更新 bat 互踩。后续建议：数据目录 lockfile 或命名 mutex `Global\zhenguojian-v2`。
- **DPI**：LoadImage 走 SM_CXSMICON（每监视器近似）；监视器间拖拽不重取尺寸，极端混合 DPI 下可能略糊（可接受）。
- **恢复置前失败**：SetForegroundWindow 受 Windows 前台锁限制，极端时窗口恢复但未置前（无闪屏/丢失，用户点任务栏/再双击可补）。
- **托盘隐藏态阻塞退出**：若用户忘了托盘常驻，任务管理器是唯一显性出口（图标常亮即提示）。

## 9. explorer 重启（TaskbarCreated）
- [ ] 任务管理器结束 explorer.exe 再启动：托盘图标应自动重新出现（已 RegisterWindowMessageW("TaskbarCreated") 监听重注册）。若未重现，检查消息循环是否收到该广播消息。

## 10. 已验证（Linux CI 门，2026-11-11 终版全量重跑）
- `GOOS=windows CGO_ENABLED=0 go build ./...` ✓；`GOOS=windows go vet ./...` ✓
- `go build ./...` ✓；`go vet ./...` ✓；`gofmt -l`（含 app.go/main.go/bindings_update.go 两包全量）干净
- `go test ./internal/tray/ ./internal/updater/ -race -count=1` ✓（ok 1.139s / 5.168s；updater 含 TestServiceAutoCheckTicker / TestServiceCheckManualNotify / TestManualCheckTextTable 三个 V-TRAY 新测试）
- `npx vue-tsc --noEmit` rc=0；`npm run build` ✓（tray.* 键改名后重跑）；全部交付文件 CRLF=0
