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

## 6. 拍板与落地（2026-11-18，用户 m01534）

用户拍板 = 改进版 A：**依赖与主程序分离打包**。NSIS 全量包只在首装/换依赖时装一次；
日常「检查更新」走 ~25MB 主程序包增量（app.asar + sidecar.exe + frontend/dist 三件易变，
robocopy 覆盖 resources，Electron 框架 dll 不动）。下载完程序内自动应用+重启，不再
「GitHub 下载-解压-替换」。

落地组成（本批，均已本机验证；Windows 执行级由打包 CI 线实锤）：
- CI 双产物：win workflow 加「Build app.zip」——手写 zip 条目（.NET ZipFile，条目名
  resources 层级、正斜杠、零前缀，Linux pwsh 实测 40 条目对拍定型；弃 Compress-Archive/
  tar.exe 因 Windows 行为本机不可复现）。zip 进 artifact，NSIS 维持原样。
- 版本统一：ZGJ_BUILD_VERSION=package.json 基号+「-alpha.$GITHUB_RUN_NUMBER」同源三处
  （electron-builder -c version / sidecar ldflags -X main.buildVersion / build-info.txt）。
- 断链修复（Go 侧）：sidecar_main.go 补 updater.EmbeddedVersion=buildVersion 转发
  （Wails 入口有、sidecar 入口没有 → 此前 Electron 形态恒判 dev「永远可更新」）；
  -ldflags 注入实测 GetBuildInfo version 正确回显。
- updater zip 分支（Go 侧）：Electron 安装形态判定 IsElectronInstall()（env
  ZHENGUO_SHELL=electron + resources\app.asar + 上级 zgj-v2.exe 结构断言，sync.Once 缓存）
  → Download 空 asset 默认改 DefaultAppZipAsset；PrepareApply .zip 在该形态生成
  BuildApplyBatZip restart 计划（等旧 sidecar 退→taskkill /T Electron 整树≤20s→tar -xf
  →结构断言→robocopy /E（≥8 才 fail）→start 新版；fail 路径也拉旧版），判定不满足回落
  manual 旧语义（dev/Wails 零影响）。bat 关键前提：LaunchDetached 的 cmd /C start 秒退
  →bat PPID 链经死进程断开→taskkill /T 杀不到 bat 自身。
- 测试：BuildApplyBatZip golden（CRLF+关键行+引号剥离）、IsElectronInstall env 闸、
  非 Windows 平台闸回落——go test ./internal/updater/ 全绿；GOOS=windows go vet 干净。

**待用户点头的两件事（发版前）**：
1. 更新源=GitHub Release，需要打 `v2.0.0-alpha.N` tag 触发发布链——与旧纪律
   「不打 alpha tag」冲突，需明示改口（artifact 只作测试包，Release 才是更新源）。
2. 首个 Release 上 NSIS+app.zip+latest.json 三资产后，删掉 Check 的 ZHENGUO_SHELL
   短路（173b235 加的横幅止血），横幅恢复显示且指向 Electron 资产。

镜像链（chain.go）对 latest.json/ReleaseDownloadURL 的代理整体保留，不动。
release-check.sh 的 EXPECTED_ASSETS 与 latest.json 断言仍是 Wails 三件套写死——
首个 Electron Release 前要么适配要么跳过，勿让它误伤发布链。

## 7. 发版接线遗留风险（首个 tag 前必须拍板，本批未实现发版链）

- build-v2.yml 触发器是 `push tags v2.*`，**会匹配 v2.0.0-alpha.N**：Electron 线一打
  alpha tag 就连带跑整条 Wails 发布链，同一 Release 被塞 Wails 三件套并覆盖
  latest.json（gen_latest_json.ps1 写的 exe=Wails 单 exe）——两条发布线互相污染。
- 更险的在消费端：老 Wails 安装版（用户机器上的 alpha.42）看到的新 latest.json 若
  exe 资产=475MB NSIS，其单 exe move 语义会把 NSIS 改名成自己的启动 exe → 双击弹
  安装向导、原程序不可用。**唯一现役 Wails 用户=用户本人**，迁移到 Electron 安装版
  后该风险自然消失，但切换窗口期内必须防。
- 落地方案（二选一，发版批做）：
  A. build-v2.yml 全 jobs 加 `if: ${{ !contains(github.ref_name, '-') }}`（预发布
     tag 直接跳过 Wails 链），Electron 发布线独立 workflow（tag `v2.0.0-alpha.*` →
     找回对应 sha 的 win artifacts → gen_latest_json_electron.ps1 → 新 Release）；
  B. Electron 发布线换 tag 命名空间（如 `v2.1.0-alpha.N` 起步避开旧链正则？——实测
     v2.* 同样匹配，需 A 的 if 条件配合，B 不独立成立）。
  推荐 A。老 Wails 版横幅抑制：发首个 Electron tag 前，用户别再给 Wails 版点更新。

## 8. 落地状态（2026-11-18 当日，代码全部已推，勿再按 §6/§7 的「将来时」读）

- §7 推荐 A 已实现（**1a50d30**）：新 `.github/workflows/electron-release.yml`
  （tag→找回该 commit 的成功 win-package run→artifact 改名契约资产→build-info
  version 对账→gen_latest_json_electron.ps1→`gh release create --prerelease`）
  + build-v2.yml 两 job `if: !contains(github.ref_name,'-')` 堵 Wails 链污染。
- 更新短路已按门控形态移除（**ec065c1**）：Electron 安装形态仅当 latest.json
  带 appZip 资产才放行横幅；现网 alpha.42 的 latest.json 本就带 appZip
  （生产实测），tag 一发布即自动开闸。
- Windows 实锤演练已落地（**49c17dd**）：selftest 进程内假镜像 + NSIS 装后
  run B(apply)/marker/run C(替换后全套自检) 三步，run15 执行中（结果补记）。
- **唯一剩余决策=用户同意打首个 tag**（§6 旧纪律改口）。tag 号必须与
  全绿 run 的 build-info `version=` 一致（对账护栏会拒错位）：当前有效号=
  **v2.0.0-alpha.21**（run19 @24cf0fa；GITHUB_RUN_NUMBER≠run id 尾数，此前
  报过的 alpha.16 是 run14 旧数、已作废）。随后下一全绿 run（alpha.22）
  走程序内更新真实演练闭环。

### 8.1 run15→run19 实锤序列（收官记录，2026-11-18）
- run15 红：electron asar 穿透补丁把 copyFileSync(<dir>\app.asar) 当归档内路径解析（ENOENT not found in <asarpath>）→ 碰 *.asar 的 fs 操作一律 original-fs（da06593）。
- run16 红：NSIS smoke run C 打印行单行内嵌 `$(if(){}elseif(){})` pwsh ParserError → 展开三行语句形式（ec5bd56）；本地校验补 PSParser errors=0 断言（只数 token 不数错误=验证脚本没断言）。
- run17 红：rmSync 漏网——run A 残留 staging，run B `fs.rmSync(work,recursive)` 时 shim 按扩展名把 stage\app.asar（真实文件）当挂载点穿透 rmdir → EBUSY（5573d07，判据收口 exists/copy/read/write/rm 全集）。
- run18：产品语义全绿——run A/B update_check(available=true tag=v9.9.9)+update_download(asset=zhenGuoJian-v2-app.zip state=done) PASS、bat 真 taskkill/tar 解压/robocopy 替换、update-marker.json 出现、run C 替换后全套 selftest 绿、卸载绿。唯一红=步骤尾 $LASTEXITCODE 传播 exit 1（24cf0fa：taskkill 后清零+显式 exit 0，失败表达收归 throw）。
- **run19（44281918355 @24cf0fa）success = 更新链 Windows 实锤完成**：「update apply exercise: bat 替换 ✓ marker ✓ 新 bundle 全套自检 ✓」。演练设计=进程内假镜像（tag v9.9.9 真实仓库不存在→回落必败，杜绝 round8 式假绿）+ zip 内容=安装件原样副本+marker（替换后应用必然仍可用）。artifact `zhenguojian-v2-electron-win-x64-24cf0fa…`（489MB）。
- **剩余唯一外部项：tag v2.0.0-alpha.N 发布授权**（electron-release.yml 已就位，打 tag 即通电；发布后补 alpha.21→22 真实升级演练收尾；Wails 链已被 `if: !contains(ref_name,'-')` 堵污染）。
