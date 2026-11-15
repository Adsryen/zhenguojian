# 仓库结构与拆分方案（第六轮，参考 JavdBviewed-Clients 实物）

这份东西的每条判断后面都跟着**实测数字或文件路径**，不是通用建议。
需要拍板的地方单独列在最后一节。

---

## 一、先纠正一个因果：黑屏与"文件太大"无关

用户猜「是不是单体文件代码太多导致的」。答案是**不是**，两条证据：

1. 出问题的路径是**原生 Win32 窗口**（注册类 → 建子窗 → `--wid` 交给 mpv →
   MoveWindow/ShowWindow/SetWindowPos）。这条链在 Go+user32 里，
   前端 JS 打包多大都影响不到它。alpha.28 已经把"注册失败"这一层修掉了
   （那句「画面未内嵌」不再出现），剩下的怀疑对象只有窗口层级/z-order/mpv 是否画。
2. 参考项目自己就有一个 **4196 行的 `src/renderer/src/main.ts`**
   （`JavdBviewed-Clients/apps/desktop/src/renderer/src/main.ts`），
   比我们任何一个文件都大，运行正常。
   所以"文件大 ⇒ 有问题"在他们项目里直接被反证。

我们的最大文件实测（不含测试/生成物）：

| 文件 | 行 |
|---|---|
| `internal/watchstore/store.go` | 1686 |
| `services/i18n.ts` | 1453 |
| `views/DownloadsView.vue` | 1384 |
| `services/fixtures.ts` | 1286 |
| `services/api.ts` | 1194 |
| `internal/player/service.go` | 1129 |
| `views/SettingsView.vue` | 1063 |

**但拆分仍然值得做**，理由不是体积，而是它和**我这几轮反复抓的那一族 bug 同源**：
`api.ts` 一个文件里同时装着"真实桥调用 + mock 实现 + 测试缝"，
于是出现过 mock 太配合（丢掉 `finished` 实参、`localSaveWatch` 连参数位都没有）、
缝打在死路径上、`snapshot.eof` 这类只在 mock 存在的字段被当成契约。
**把"真相来源"分文件，是让这类错误变可见的手段**，不是洁癖。

---

## 二、参考项目实测：哪些观念能搬，哪些不能

`/home/ryen/Git/JavdBviewed-project/JavdBviewed-Clients` 实物：

- **pnpm monorepo**：`apps/{desktop,android,mobile,tv}` +
  `packages/{sync-client,sync-protocol,theme}`。
- 桌面端 = Electron + `electron-vite` + `electron-builder`（nsis）+ `electron-updater`，
  `publish.provider: github`，`artifactName: JavdBviewed-desktop-${version}-${os}-${arch}.${ext}`。
- `src/` 四分：**`main` / `preload` / `renderer` / `shared`**，
  其中 `shared/` 放跨进程逻辑**并就近放测试**
  （`mediaLibrary.ts` 1312 行 ↔ `mediaLibrary.test.ts` 1181 行；
  `clipboard.ts`、`cloudConnection.ts`+test、`desktopRelease.ts`+test…）。
- 更新链路：`desktopUpdateProvider.ts` 直接用
  `electron-updater/out/providers/GenericProvider.js`，
  发现逻辑吃的是 release 行（`tag_name / draft / prerelease / assets[]`，
  其中 **`latest.yml` 是随版本发布的一个资产**），
  并且**这段发现逻辑有独立单测**（`desktopReleaseDiscovery.test.ts`）。

### 值得搬的（按性价比排序）

1. **把"版本元数据"做成 release 资产**（他们的 `latest.yml` 模式）。
   这正是我们更新器 403 的结构性解法：
   实测 `gh-proxy.com/https://api.github.com/...releases` → **403 rate limit**、
   `ghproxy.net/...api.github.com` → **403 Invalid input**，
   而 `gh-proxy.com/https://github.com/.../releases.atom` → **200**。
   也就是说：**只要不去碰 api.github.com，公开镜像就能用**。
   我们现在的 `ReleasesAPIURL` 是 API JSON，天生走不通镜像。
   搬法：CI 发布时多写一个 `latest.json` 资产（版本号、通道、资产名、大小、哈希），
   更新器改成"按候选链取 `releases/download/<tag>/latest.json`"，
   **完全不依赖 api.github.com**，也不需要 token/配额。
2. **`shared` 层 + 就近测试**：对应我们该做的是把 `services/` 拆成
   `bridge.ts`（真实调用）/ `mock/`（演示与测试缝，明确标注"只存在于 mock"）/
   `contract.ts`（与 Go 侧对齐的类型与字段清单）。
   目的是让"mock 比真实更宽松"这件事在文件结构上就露出来。
3. **就近放测试**（他们的 `.test.ts` 与源文件同目录）：
   我们的 Playwright 验收已经集中在 `verification/`，这点不必照搬；
   但 Go 侧 `*_test.go` 与源文件同目录已经是我们的做法 ✓。
4. **多端共享 theme 包**：我们只有一个客户端，暂时不需要；
   若以后要做 Android/TV（他们有四端），`tokens.css` + i18n 抽包才有意义。

### 不该搬的

- **换引擎**（Electron 替代 Wails）：那是重写。而且体积上我们更省
  （我们 exe 21MB + runtime 139MB 里主要是 mpv/ffmpeg；
  Electron 自带 Chromium，桌面端单包通常 80–150MB）。
- **nsis 安装包**：我们现在是"单 exe 覆盖升级 + runtime 一次装"，
  用户升级只下 21MB。换 nsis 会让每次升级都变成整包。
  （如果要装图标关联/开始菜单/卸载器，才值得引入 nsis。）
- 他们 `main.ts` 4196 行这种**单体本身**——那是反面例子，不是目标。

---

## 三、v1 代码能不能直接从 master 删掉：**不能就这么删**

这条最重要，因为照直觉删掉会**当场打断 CI**。

实测依赖关系：

- `client-v2/scripts/make-overlay.py:16` 生成 `go build -overlay`，
  把 **`zhenguojian/sidecar/core_overlay/discovery.go`（17.8KB）
  与 `poster_metadata.go`（2.7KB）** 注入核心包；
  两个 job 都带 `-overlay` 构建（`build-v2.yml` 里 windows 与 linux 都用）。
  → 这两个文件**住在 v1 目录树里**，删 `zhenguojian/` 就是删构建输入。
- 核心源码在 `guoapp-src/native`（`go.mod:52` replace 指过去），与 v1 的 Python 无关 ✓。
- ⚠ 实测后修正我先前的一句话：`zhenguojian/` 下**不只有 Python**。
  那里还有一个完整的 Go 模块 `zhenguojian/sidecar/`
  （自带 `go.mod`/`go.sum`/`vendor/`，被跟踪文件 **1677 个**），
  CI 的 overlay 就从它里面取 2 个文件。所以"删 v1 会打断构建"比我写的更严重一点：
  要动的不只是一个目录，是**一个还在被构建引用的 Go 模块**。
- v1 的 **Python 部分**实测规模：**41 个文件 / 23,386 行** PyQt5
  （`git ls-files zhenguojian/app/zhenguojian`）。
  这部分没有任何构建依赖，只有**参照价值**：
  我这几十轮的对账全部引用它的 `文件:行号`（如 `main.py:2511-2522`、
  `poster_wall.py:650-662`、`browse_panel.py:402-403`），
  而且 v1 自带的 3 个测试文件（`test_calendar_panel.py`、`test_sorani_login.py`、
  `test_palette.py`）是我唯一能交叉核对期望值的规格来源。

所以正确顺序是（每步都可单独回滚）：

1. **先把那 2 个 Go 文件搬进 `client-v2/`**（建议 `client-v2/coreoverlay/`），
   改 `make-overlay.py` 指向新路径 → CI 复跑验证；
   这一步之后，`zhenguojian/` 里剩下的就全是"参照物"，不再是构建输入。
2. **给 v1 打一个不可漂移的锚**：`git tag v1-final`（比分支好——分支会被人推进，
   我的 `main.py:2511` 这类引用会失效；tag 不会）。
3. **再从 master 删 `zhenguojian/app/`**，并在 `research/` 各对账文档开头
   加一行"行号引用指向 tag `v1-final`"。
4. **目录规范这件事，实测结论是"已经比我以为的好"**，不要为了动而动：
   分层 `.gitignore` 已存在（root、`client-v2/frontend/`、`client-v2/spike/`、`zhenguojian/`），
   `node_modules/` 与 `dist/*`（保留 `.gitkeep`）都在忽略内；
   `client-v2/spike/` 磁盘上 773 个文件但**只有 43 个被跟踪**
   —— 那是它自带的 `.gitignore` 在起作用，属于"留结论、不留产物"的有意做法，
   代码注释里还有多处引用 spike 的实测数据（比如 embed_windows.go 提到
   spike 版把 `LazyProc.Call` 的第二/第三返回值搞混过）。

   ⚠ 记一条我自己的过程错：这一段我第一版是**凭印象写的猜测**
   （"该确认的是 spike/、dist、node_modules、.venv-build 是否被忽略"），
   而且第一次实测时把路径写成了 `spike`（不存在）而不是 `client-v2/spike`，
   得到"没被忽略、也没被跟踪"的错误读数，差一点就写进文档变成新的假事实。
   重测后才拿到真实数字。教训：**测错了路径的"实测"和猜一样不可信**，
   读到意外结果时先怀疑量法，再下结论。

⚠ 我不建议在没有第 1 步的情况下直接删 v1 —— 那会让两个 job 一起红，
而且红的原因（overlay 找不到文件）看起来像"构建脚本坏了"，很难一眼归因。

---

## 四、目录规范：我建议的最小版本

现在顶层是 `zhenguojian/`（v1）+ `client-v2/` + `guoapp-src/` + `research/` + `.trellis/`。
**我的建议是别大动**，只做三件事：

1. `client-v2/coreoverlay/`（承接 v1 搬来的 2 个 Go 文件）；
2. `client-v2/frontend/src/services/` 三分（`bridge` / `mock` / `contract`）；
3. `research/` 里加一份 `INDEX.md`：现在已有 15 份对账/跟进文档，
   靠文件名猜内容已经开始费劲（`v1-v2-*` 十个、`v2-windows-followups`、`GAPS`、
   `SESSION-STATE`），一份"每份文档回答什么问题"的索引比改目录名有用。

大规模改名（`apps/`、`packages/` 那套 monorepo 布局）我建议**等 v1 移除之后**再说，
因为那会让所有 `文件:行号` 引用与 git 历史追溯成本翻倍，
而现在正是这些引用在替我们守住 v1↔v2 一致性。

---

## 五、需要你拍板

| # | 决定 | 我的倾向 |
|---|---|---|
| D1 | 更新器改成"latest.json 资产 + 候选链"，彻底不碰 api.github.com | **建议做**，这是 403 的根治，不是加镜像 |
| D2 | v1 移除的三步走（先把 `zhenguojian/sidecar` 的构建输入搬进 client-v2 → 打 `v1-final` tag → 删 Python 那 41 个文件） | **建议按序做**；跳步会打断 CI。sidecar 是 1677 文件的 Go 模块，要不要一起搬需要单独定 |
| D3 | `services/` 三分（bridge/mock/contract） | 建议做，理由是让"mock 太宽松"变可见，不是体积 |
| D4 | 顶层目录 monorepo 化 | **建议暂缓**，等 D2 完成 |
| D5 | 退出提速（托盘收尾与 mpv quit 并发，2–3s → 约 1.5s） | 取舍：更快退出 vs 更强"不留孤儿 mpv"保证；你定 |
| D6 | nsis 安装包 / 开始菜单 / 卸载器 | 现在不建议；除非你要文件关联或卸载体验 |

（另：黑屏那条我已经在 alpha.29 里埋好现场诊断，
等你按 `localStorage.setItem('zgj.embedDebug','1')` 截一张图，
或直接看 DevTools 里 `[embed]` 那几行。）
