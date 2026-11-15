# 开发计划：D1–D5 + UI/架构借鉴（第六轮定稿）

参考实物：`/home/ryen/Git/JavdBviewed-project/JavdBviewed-Clients`
（pnpm monorepo，`apps/{desktop,android,mobile,tv}` + `packages/{sync-client,sync-protocol,theme}`；
桌面端 Electron + electron-vite + electron-builder(nsis) + electron-updater）。

## 从那个项目实际读到的东西（不是印象，是 ls 出来的）

- `apps/desktop/src/`：**`main` / `preload` / `renderer` / `shared`** 四分，
  `shared/` 放跨进程逻辑并**就近放测试**（`mediaLibrary.ts` 1312 行 ↔ `.test.ts` 1181 行）。
- `renderer/src/`：**`features/<域>`**（actor / home / library / localScan / player /
  searchHistory / series / settings）+ **`ui/`** + **`shell/`** + **`styles/`**。
- `ui/` 里**没有组件，全是纯逻辑模块 + 每个一份测试**：
  `confirmDialog.ts`、`contextMenu.ts`、`contextMenuKeyboard.ts`、
  `cardDblclickGuard.ts`、`toast.ts`、`icons.ts`。
  → 这条对我们最有价值：**交互语义做成可单测的纯模块**，
  而不是只能靠 Playwright 端到端去撞（我这几轮大量时间花在
  "选择器/时序/覆盖层"这类本可以单测的东西上）。
- `styles/` 拆成 `base.css` / `components.css` / `contextMenu.css` /
  `feedback.css` / `index.css`（我们是 tokens.css + base.css 两个大文件）。
- `packages/theme/`：`resolveTheme.ts` + `resolveTheme.test.ts` + `types.ts` +
  `constants.ts` —— **主题解析是一个有测试的独立模块**（我们的主题散在
  `styles/theme.ts` + store 里，没有单测）。
- 更新链路：`desktopUpdateProvider.ts` 用 `electron-updater` 的 GenericProvider，
  元数据是**随 release 发布的资产 `latest.yml`**，发现逻辑有独立单测。

---

## 阶段 0 —— **D2 已完成**（提交 `3658d6a`→`3b4069c`，锚点 tag `v1-final`）

实际执行与计划的差异（都是执行中发现的，不是事后美化）：

1. **不能"移动"那两个 overlay 输入，只能复制** —— 移动会打断 v1 自己的构建
   （`build.yml` 在 `v1.*` tag 上构建 sidecar 模块）。计划里我写的"搬"是错的量词。
2. **复制成 `.go` 会让 CI 红**：`go vet ./...` 会下钻编译 `client-v2/coreoverlay`，
   而那两个文件是 `package core` + 引用只在 core 包内存在的 `nativeDrama`，
   必然报 `undefined: nativeDrama`。v1 没暴露这个问题，
   只是因为它的构建只 `go build .`（根包），没下钻。
   → 最终形态：仓库里存 `.go.txt`，由 `make-overlay.py` 构建时落地成临时 `.go`，
   那个目录**在模块里不再是一个 Go 包**。
3. **删除前做了引用扫雷**，其中一条差点误判：`build-v2.yml:66/267` 里的
   `zhenguojian/client-v2/` 是 **Go 模块名**，不是目录路径，与 v1 无关。
4. v1 的 workflow 不是删掉而是**移出 `.github/workflows/`**（放到
   `workflows-retired/`），因为留着一个"一 dispatch 就红"的工作流是陷阱。
5. `SOURCE_MANIFEST.json`（191 文件的哈希快照）**没有任何代码读它**，
   只有两份 `AI_BUILD.md` 提到。我没改它——改一个没人读的清单只会制造新的不一致。

验证：CI 两 job ✓（复制后、.go.txt 后各跑一次）；删除后的验证见本次 CI。

## 阶段 0b（原计划，保留备查）

（下面这段是执行前的计划原文）


### D2 v1 移除三步走
1. 把构建输入搬出来：`zhenguojian/sidecar/core_overlay/{discovery.go,poster_metadata.go}`
   → `client-v2/coreoverlay/`，改 `client-v2/scripts/make-overlay.py:16` 指向新路径；
   ⚠ 不先做这步就删 `zhenguojian/`，**两个 CI job 一起红**，
   而且红的原因（overlay 找不到文件）看起来像"构建脚本坏了"，很难归因。
2. 打不可漂移的锚：`git tag v1-final`（分支会被推进，我那些
   `main.py:2511-2522`、`poster_wall.py:650-662` 引用就会失效；tag 不会）。
3. 删 `zhenguojian/app/zhenguojian/`（实测 41 文件 / 23,386 行 Python），
   并在 `research/*.md` 顶部统一加一行"行号引用指向 tag `v1-final`"，
   给出取法：`git show v1-final:zhenguojian/app/zhenguojian/main.py`。

验证：CI 两 job ✓ + 全量 `run_all` 30/30。

### D1 的服务端（CI 侧）
发布时多产出一个资产 `latest.json`（契约见下），由 workflow 生成并随 release 上传。

---

## `latest.json` 契约（D1 双方必须一致，所以写死在这里）

```json
{
  "schema": 1,
  "generatedAt": "2026-11-15T12:00:00Z",
  "tag": "v2.0.0-alpha.29",
  "version": "2.0.0-alpha.29",
  "prerelease": true,
  "notes": "发布页正文摘要（可空）",
  "assets": {
    "exe":        { "name": "zhenGuoJian-v2.exe",          "size": 21000000, "sha256": "…", "url": "https://github.com/Adsryen/zhenguojian/releases/download/<tag>/zhenGuoJian-v2.exe" },
    "appZip":     { "name": "zhenGuoJian-v2-app.zip",      "size": 8400000,  "sha256": "…" },
    "runtimeZip": { "name": "zhenGuoJian-v2-runtime.zip",  "size": 145000000,"sha256": "…" },
    "buildInfo":  { "name": "build-info.json",             "size": 120 }
  }
}
```

取法（**全程不碰 `api.github.com`**，这是 D1 的全部意义）：

- 稳定通道：`https://github.com/<owner>/<repo>/releases/latest/download/latest.json`
  （GitHub 会重定向到最新**非预发布** release 的该资产；`github.com` 形状镜像支持）。
- 测试版通道：先取 `https://github.com/<owner>/<repo>/releases.atom`
  （实测走 `gh-proxy.com` 前缀 **200**），从 `<link href="…/releases/<tag>">` 得到最新 tag，
  再取 `releases/download/<tag>/latest.json`；
  预发布与否**由 tag 文本判**（我们的 tag 已含 `-alpha.N`），不依赖 API 的 `prerelease` 字段。
- 每个 URL 都走现有 `ChainCandidates`（用户前缀 → 直连 → 内置三镜像）。

实测依据（为什么必须换数据源）：
`gh-proxy.com/https://api.github.com/…` → **403 rate limit**；
`ghproxy.net/https://api.github.com/…` → **403 Invalid input**；
`gh-proxy.com/https://github.com/…/releases.atom` → **200**，27KB，10 个 entry。

---

## 并发分工（文件所有权互不重叠）

| 谁 | 做什么 | 独占文件 | 不许碰 |
|---|---|---|---|
| **我** | D2 全部 + D1 服务端（CI 产出 latest.json）+ 最终全量回归 | `.github/workflows/build-v2.yml`、`client-v2/scripts/make-overlay.py`、`client-v2/coreoverlay/`、`zhenguojian/**` | —— |
| **A** | D1 客户端：更新器改吃 latest.json（含 atom 发现预发布通道），带单测 | `client-v2/internal/updater/**` | workflow、overlay、v1 |
| **B** | D3：`services/` 三分（bridge / mock / contract），`api.ts` 保留为 facade 以免动调用方 | `client-v2/frontend/src/services/**` | views/stores（若必须改，先写进报告） |
| **C** | D5：退出并发化（托盘收尾与 mpv quit 并行），保持"不留孤儿 mpv" | `client-v2/app.go` 的 shutdown 区、`client-v2/internal/tray/**` | updater、services |
| **D** | UI/架构借鉴方案（只读两边仓库） | 新建 `research/ui-architecture-plan.md` | 一切代码 |

冲突控制：
- A/B/C 都**不许跑 `run_all`**（端口固定，会互相撞）；
  允许 `npm run typecheck` / `npm run build` / 单只 `*_verify.py`（各自端口）。
  Go 侧本机编不了（见 GAPS 第七节），所以 A/C 的 Go 改动**由我统一交 CI 验证**。
- 我最后合并三方改动，跑全量 + CI，再打 alpha.30。

---

## 阶段 1（D1/D3/D5 落地后）

- **D-UI 第一批（低风险，先做）**：把已有交互逻辑抽成可单测纯模块，照他们 `ui/` 的做法：
  `contextMenuKeyboard`（方向键/Enter/Esc 语义）、`cardDblclickGuard`（双击去抖）、
  `confirmDialog`、`toast` 队列。这些现在散在 `.vue` 里，只能靠 Playwright 验。
- **D-UI 第二批**：`views/` → `features/<域>/` 布局 + `ui/` + `styles/` 拆分；
  这一步会动大量文件，**必须等 D2 完成**（否则 `文件:行号` 引用与 git 追溯同时翻倍）。
- **D-UI 第三批**：`resolveTheme` 独立模块 + 单测（我们现在主题解析无测试）。

## 阶段 2（D4，你批准但我建议按序）

顶层 monorepo 化（`apps/` + `packages/`）。**排在 D2/D3 之后**，理由写在上面：
它移动的正是在 D2/D3 里被改的目录，同时做会让冲突面积最大。

---

## 里程碑与验收口径

| 里程碑 | 内容 | 验收 |
|---|---|---|
| M1 | D2 完成，v1 Python 不在 master | CI 两 job ✓；`run_all` 30/30；`v1-final` tag 可取原文 |
| M2 | D1 完成 | 更新器在**无 api.github.com** 路径下能发现新版；单测覆盖 atom 解析与 latest.json 校验；真机复测一次 |
| M3 | D3 完成 | typecheck/build ✓；`run_all` 30/30；mock 侧不再出现"入参被默默丢弃"（contract 层显式列出每个实参） |
| M4 | D5 完成 | 退出实测耗时对比（目标 2–3s → ≤1.5s）+ 任务管理器无孤儿 mpv |
| M5 | D-UI 第一批 | 新增纯模块各有单测；对应 Playwright 断言可简化 |

## 仍然挂着的前置问题

**黑屏那条还没定位**，等 alpha.29 的 `[embed]` 现场数据。
它不属于 D1–D5，但优先级高于本计划里任何一项 —— 数据一到我就先修它。


---

## 执行状态（第六轮收尾）

| 项 | 状态 | 实证 |
|---|---|---|
| **D2** v1 移除 | ✅ 完成 | tag `v1-final`；master 删 `zhenguojian/`；CI 两 job ✓；`scripts/v1show.sh` |
| **D3** services 三分 | ✅ 完成 | `run_all` 30/30、typecheck 0、负注入 `ContractMismatch` 生效 |
| **D1** 更新器 latest.json | ✅ 完成（客户端 + CI 产出） | `internal/updater` 42→**60/60**；`GOOS=windows go vet` rc=0 |
| **D5** 退出并发化 | ✅ 完成 | 根包 `-count=2` ✓、`internal/tray` ✓、变异测试 10 个变体全红 |
| **D4** monorepo 化 | ⏸ 按计划暂缓 | 等 D2/D3 稳定后再评估 |
| **D6** nsis | ❌ 未做（建议不做） | —— |

## 期间额外修掉的两个真 bug（都不是计划里的，是并发跑出来的）

1. **合并取消竞态**（`downloadsvc`）：running 已置、`entry.cancel` 未登记的窗口里
   取消被静默丢弃却返回 ok。由恢复硬门后 Windows 的"偶发红"追出，
   **没有重跑到绿**。已修 + 确定性测试。
2. **诊断代码自己截断日志**：`grep … | head -40` 在 `pipefail` 下吃 SIGPIPE
   → errexit 终止步骤，导致"打印失败原因"反而让日志断在诊断之前。
   两处（另一处 `sed | head -12`）已兜住。

## 新增待决（D7）

**退出时 downloads 步骤可能被 2s 预算放弃**，从而跳过末尾那次
`merges.state.json` 落盘（串行在某个区间内本来还能落）。
两个消法：把 downloads 也标 terminal（最坏回到 5s，把延迟请回来），
或调小 downloadsvc 内部那个 5s 等待。
当前选择是"保留 2s 预算 + 写进注释与日志"。需要你定。

## 一条方法论收获（写进这里，因为它改变速度）

`GAPS §七` 那条"本机没有可用 Go 工具链"是**未验证的环境假设**，
本轮被推翻：`GOTOOLCHAIN=go1.25.3` 可用，`internal/...` 全量 **16 秒**跑完，
`GOOS=windows go vet` 可用。之前每轮为 Go 编译错付 10–20 分钟 CI 等待，
本轮白跑 3 次才追到根因。
**结论：环境假设也要当成"待验证事实"来对待**，否则它会一直收租。
