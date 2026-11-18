# P5 更新链选型：Electron 产物形态 vs Go updater 契约（决策草稿）

状态：**待用户实机结果后拍板**。本文把改动面/风险/验收钉清楚，拍板即执行。
关联：electron-migration-plan.md P5 段；用户纪律「不打 alpha tag，artifact 交付」。

## 1. 现契约（Wails 时代，代码实况）

- 元数据：`latest.json`（schema=1），走 `github.com` 形状 URL（`releases/latest/download/latest.json`），**镜像链整体代理的前提**——`internal/updater/latest.go:3-9` 注释实锤：api.github.com 经 gh-proxy 必 403，所以元数据绝不打 API。
- 资产：`assets.exe` 要求 name/url/size/sha256 全齐（latest.go:85,152-160），默认名 `zhenGuoJian-v2.exe`。
- 应用：`service.go:382-402` — exe 资产+Windows → `Kind:"restart"`，bat 由 `BuildApplyBat`（bat.go:22-62）生成：**等旧 PID 退出（≤60×2s）→ `move /Y NEW→CUR` 覆盖单 exe → 启动 CUR → 自删**。zip 资产 → `Kind:"manual"`。
- 生成侧：`client-v2/scripts/gen_latest_json.ps1`（PackageDir 的 exe/app.zip/runtime.zip 三件套 → latest.json + softprops release）。

## 2. 冲突

Electron 版没有「单个自包含 exe」：
- NSIS Setup.exe（475MB，装到 %LOCALAPPDATA%\Programs\zhenguojian-v2\，运行中进程锁 zgj-v2.exe/resources）；
- win-unpacked 目录（~500MB，多文件 + electron framework + asar）。

`move /Y 单文件` 语义对两种形态都不成立。

## 3. 候选

### A. NSIS 静默重装（推荐候选，改动最小、镜像链保留）
新语义：下载 `Setup.exe` 资产 → bat 等旧 PID 退出 → `start /wait Setup.exe /S` → NSIS 覆盖安装 → 拉起新版。
- 改动面：latest.json 增 `assets.installer`（url/size/sha256 同规格，name 允许 .exe 结尾的 installer，语义字段区分）；`service.go` PrepareApply 分支 + `BuildApplyBat` 出 installer 版文本（move→start /wait）；`gen_latest_json.ps1` 抓 NSIS exe；前端「重启更新」文案微调（重启→重装并重启）。Go updater 与镜像链**原样复用**。
- 风险：R1 运行中文件被锁——NSIS 自带 appExit（taskkill 等待），且 bat 已先等旧 PID，双保险；R2 perUser 安装目录与 win-unpacked 手动解压目录混用导致「装了但启动的是旧的」——交付话术固定二选一（用户拿 artifact 是 unpacked zip 测试，正式用户走 NSIS）；R3 静默安装失败无 UI 反馈——bat exit code 记入 updater 日志，前端错误帧兜底（error_frame 体系现成）。
- 验收：CI（run8+ NSIS smoke 步已备）+ 用户实机「设置页检查更新→重启→版本号变」全链路。

### B. electron-updater（基本排除）
`electron-updater` 的 generic provider 可指 latest-mac/yml + 文件 URL，镜像链理论上能凑，但：把 Go 侧 `internal/updater/`（semver 门、镜像链、sha256、错误帧）整层**架空**，双实现漂移风险；且 P2 定的「更新逻辑留在 Go、壳薄」原则被破坏。排除，除非 A 实机暴雷。

### C. unpacked zip 换目录 + 版本指针
latest.json 加 `unpackedZip`；下载解压 `<dataDir>/updates/<tag>/`；快捷方式指向一个 launcher（junction 或 current.txt 指针）。桌面图标/自启路径全是固定安装目录 → 需要 launcher exe 新物种 + junction 权限处理。**复杂度最高、用户可见面变化最大**（安装目录与运行目录分离），仅当 A 的 NSIS 覆盖实机暴雷才退到这条。

### D. win.target=portable 单文件
回到「单 exe」形态、契约零改动？——portable exe 运行时自身在 %TEMP% 解包并锁定外层文件，`move /Y` 覆盖运行中 exe 与 Wails 时代「exe 未自锁」的本质不同，替换必翻车；且 portable 每次启动重解压、无注册表项。排除。

## 4. 拍板条件

1. run8 NSIS smoke 绿（安装→自测→卸载）；
2. 用户实机对 run6/run8 artifact 的反馈（双击 NSIS 装、日常用、黑屏与否）；
3. 两者都过 → **选 A**，按改动面清单执行（Go 侧一个 PR：latest.json schema v2 兼容读 + installer bat + gen_latest_json 改抓）。
若 2 暴雷 NSIS 本身的问题 → C 作后手。

## 5. 附带（拍板后一起做）

- alpha tag 策略：用户曾言不打 tag；但 latest.json 通道依赖 release 面。正式更新链第一次发版时需要用户点头开一个 v2.0.x 正式 release（NSIS + latest.json 上 GitHub Release）——artifact 只能当测试分发，当不了更新源。
- `assets.appZip/runtimeZip` 字段：Electron 版无对应物，schema v2 里标注废弃（读侧保持兼容不报错）。
