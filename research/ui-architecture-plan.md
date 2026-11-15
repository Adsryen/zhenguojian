# UI 架构与体验借鉴方案（D 线，只读实测，未改任何代码）

生成时间：2026-11-15。两边实物都亲自读过；所有 `文件:行号` 现测。
量法与可复跑命令在最后一节（七）。

参考项目根：`/home/ryen/Git/JavdBviewed-project/JavdBviewed-Clients`（下称 **他们**）
我们：`/home/ryen/Git/zhenguojian/client-v2/frontend`（下称 **我们**）

**⚠ 行号有效期**：本文所有指向我们前端的 `文件:行号` 都是**读自当前工作区**（不是 HEAD），
而且**已经漂过一次**：写这份文档期间另有线在改 `client-v2/frontend/src`（`git status` 实测 16 个脏文件），
其中 `views/PlayerView.vue` 从 926 行变成 932 行，**第 189 行往后整体下移 6 行**。
我按新文件把本文涉及的 8 处重新定位过了（`:188-194` isTypingTarget、`:196-249` onHotkey、
`:199-210` Esc 支、`:201` helpOpen、`:207` 浮层判据、`:380` keydown 注册、`:603` `.pl-toast`、
`:656`/`:922` 两处 z-index 70）。**动 P1/P2 前请再跑一次这条重定位**：
`grep -n 'function isTypingTarget\|function onHotkey\|pl-toast\|z-index: 70' src/views/PlayerView.vue`。
其余脏文件（`api.ts`/`i18n.ts`/`types.ts`、`internal/updater/**`）**本文一个行号都没引用** ——
我写的时候专门避开了它们。**已证实这个避法是对的**：写这份文档期间 D3 落地了，
`services/api.ts` 从 57KB 变成 57 行的 facade，新增 `bridge.ts`(287) / `contract.ts`(377) / `mock/`。
如果我照原样去引 `api.ts:行号`，那些引用现在已经全部作废。**顺手量了这件事的真实代价**：
`research/*.md` 里共有 **9 个唯一的 `api.ts:行号`** 引用，**9 个全部指向 >57 行** →
D3 一落地就**全部悬空**（对比 `i18n.ts` 的 10 处引用最大指到 `:1414`，文件现 1455 行，暂时还活着）。
那是 D 线之外的问题，但值得 lead 记一笔：**"行号引用"在这种并行改动下是易碎品**，
也是本文把 P5（大规模移动文件）压在 D2 之后的量化理由。

> 与既有两份文档的关系：`research/dev-plan-2026-11.md:105-110` 已经把 D-UI 排成三批
> （纯模块+单测 / features 布局 / resolveTheme），`research/repo-structure-proposal.md` 定了
> 结构与 D2 顺序。本文不推翻它，**只做两件事**：把那三批落到"改哪个文件、几步、怎么验收"，
> 并纠正其中一条会影响排序的事实（见下一节 F1）。

---

## 一、结论先说

**F1（要纠正的一条事实）**：他们的 `apps/desktop/src/renderer` 里 **`.vue` 文件数 = 0**，
155 个 `.ts`。桌面端渲染是手写 DOM（`renderer/src/main.ts` 4196 行）。
所以 "参考他们的 `features/<域>` 布局" 搬过来的**不是目录形状**（那是没有框架的产物），
而是那条真正起了作用的约束：**每个交互语义 = 一个无 DOM 依赖的纯模块 + 一个同名测试**。
这条约束可以完整搬到 Vue 里；他们的 `renderXxx(state): string` 模板字符串**不能也不该搬**（见 Q5-N4）。

**F2**：他们测试的**体量**是现测的，不是印象：
renderer 测试 3800 行 / 源码 9685 行（39%），`shared/` 测试 5400 行 / 源码 6111 行（88%），
60 个 `.test.ts` 对 95 个源文件，另有 45 个 Playwright spec（`e2e/specs`）。
——注意最后一项：**他们是单测和 e2e 都要，不是用单测替掉 e2e**。
我们的 30 只 `*_verify.py` 不在这次方案的删除清单上。

**F3**：我们前端 **一个 `.test.ts` 都没有**（`src` 下 `.test.ts`/`.spec.ts` 计数 = 0），
`package.json` 里没有 `test` 脚本，`node_modules/vitest` 未安装，
CI 的前端步骤只有 `npm ci` + `npm run build`（`.github/workflows/build-v2.yml:160-171`，
`npm run build` 实测在 `:171`；**连 `typecheck` 都不跑**）。
所以"引入 vitest"的成本 ≈ 一个新配置文件 + 一个 devDependency + CI 加一行。
这是本方案里性价比最高的一件事。

**F4**：他们 `ui/` 那批模块的价值不在"有测试"，在**切分线画对了**：
每个文件前半是纯核心（可 node 单测），后半是 DOM 壳。
`ui/toast.ts:15-70` 是 scheduler，`:74-116` 才是 DOM；`ui/confirmDialog.ts:30-50` 是 FIFO 控制器，
`:54-147` 是遮罩。我们的对应物**没有这条切分线**——toast 是视图里的一个 `ref`，共 7 份（见 2.2 表 U6）。

**F5**：最该先搬的三个具体东西，按"用户已可感知的错"排序，不是按"像不像他们"：
① Esc/浮层的**单点分发**（替掉我们 `services/overlays.ts` 的记账补丁）；
② **分页失败的承诺不一致**（三个页面三种行为，其中搜索页静默停翻页）；
③ **破坏性操作零确认**（全仓 `grep 'confirm('` = 0 命中）。

---

## 二、现状对照表

他们的做法 / 我们的现状 / 差距 / 搬不搬。行号都是我自己打开文件看到的。

### 2.1 测试基座

| # | 他们 | 我们 | 差距 | 搬不搬 |
|---|---|---|---|---|
| T1 | `apps/desktop/vitest.config.ts` 全文 8 行：`environment: 'node'` + `include: ['src/**/*.test.ts']` | 无 vitest（`node_modules/vitest` 缺失） | 交互语义只能靠 Playwright 撞 | **搬**，逐字搬这个 config |
| T2 | 测试与源码**同目录同名**：`ui/toast.ts` ↔ `ui/toast.test.ts` | `verification/*_verify.py` 集中一处，与源码不同目录 | 改一个函数要看两个目录 | **搬**（同目录）；`verification/` 保持不动 |
| T3 | 测试里显式 `import { describe, expect, it } from 'vitest'`（如 `styles/tokensParity.test.ts:8`） | — | 若开 `globals: true` 要动 `tsconfig.types`，vue-tsc 会漏检 | **搬显式 import**，不开 globals |
| T4 | `styles/tokensParity.test.ts:60-86` 用 `readFileSync` 直接对 **CSS 文件文本**做静态断言 | 同类检查已有两份，但都是 Python + 要起浏览器或不在 CI：`verification/palette_audit.py`（静态、四条 A/B/C/D，正则 `:60-64`）与 `verification/theme_contrast_verify.py:153-154`（**7 个名字的写死黑名单**，判据 `:28` 自称"静态兜底"） | 两份**都不在 CI**（`build-v2.yml:160-171` 只有 install+build）→ 破了没人知道；且黑名单只防旧拼法、不防新造 | **搬思路**：变成 `tokens.css` 旁的 `.test.ts`（全量判定，不是黑名单），进 CI |
| T5 | 纯逻辑模块**零 DOM 依赖**即可测：`ui/cardDblclickGuard.ts:94-108` 把 `nowFn/sleepFn` 作参数注入（假时钟） | `services/dramaRef.ts`（83 行、7 个导出、`grep` 无 `window/document/localStorage`）已经满足同样条件，但没测 | 已有免费可测的纯模块，一个测都没写 | **搬**：这是 P0 的第一批 |

### 2.2 `ui/` 纯逻辑 ↔ 我们的对应物

| # | 他们的做法 | 我们的现状 | 差距 | 搬不搬 |
|---|---|---|---|---|
| U1 | `ui/contextMenu.ts:50-62` `resolveEscTarget({confirmOpen,menuOpen,detailOpen})` → 单一优先级链；`main.ts:3785-3812` 一个全局 keydown 分发；`contextMenu.ts:8-9` 明写"本组件不自注册 Esc 监听" | `services/overlays.ts:19` 靠 CSS 选择器串 `.zgj-ctx-menu, [role="dialog"]` 判"有没有浮层"；`:37-61` 再加一个 window 捕获阶段的 **Esc 记账**；`contextMenu.ts:254-256` 自己听 Esc | 我们**自己承认**这个机制站不住：`overlays.ts:10-12` 原话"用注册顺序去赌谁先跑是站不住的" | **搬**（P2，收益最高、风险最高） |
| U2 | `ui/contextMenu.ts:153-189` 菜单键盘导航 + `ui/contextMenuKeyboard.ts:25-39` `nextMenuHighlight`（环回、跳过禁用项）有独立测试 | `services/contextMenu.ts:279-300` 菜单项是 `div`（`:284`），无 `tabindex`、无 `role`、无 `focus()`；`:254-256` 只认 Escape | 右键菜单**完全不可键盘操作** | **搬** `contextMenuKeyboard.ts` 三个函数（可逐字搬，无 DOM） |
| U3 | `ui/contextMenu.ts:111-116` 关闭时焦点还给触发元素；`confirmDialog.ts:64-67` 同样 | `closeMenu()`（`contextMenu.ts:241-248`）不碰焦点 | 键盘/读屏用户按完菜单丢焦点 | **搬**（DOM 壳里 3 行） |
| U4 | `ui/contextMenu.ts:65-78` `clampMenuPosition` 纯函数 + 测试（含"菜单大于视口→0,0"） | `contextMenu.ts:304-308` 内联 `Math.max(6, Math.min(...))` | 边界情况（菜单比视口大）无处理无测试 | **搬**（纯函数，5 行搬走） |
| U5 | `ui/confirmDialog.ts:30-50` Promise 型 + FIFO 队列；`:103-111` Tab 焦点陷阱；`:115-118` 点遮罩=取消；`:9-12,99` destructive 变红；`main.ts` 里 6 处调用 | **全仓 0 处确认对话框**；`grep 'confirm('` 空 | 破坏性操作直接落刀 | **搬**（P4） |
| U6 | `ui/toast.ts:15-70` scheduler（顺序、到期、`tick`），DOM 挂 `body`（`:83`），`aria-live=polite`（`:82`），error 用 `role=alert`（`:97`），时长常量 `:25` 3200ms | 7 份各自 `ref('')`：`DetailView.vue:83`、`ReserveView.vue:38`、`DiscoverView.vue:244`、`HistoryView.vue:33`、`DownloadsView.vue:29`、`CreatorView.vue:210`，+ `SettingsView.vue:36` 内联 `setTimeout(4000)` | 连发第二条**覆盖**第一条并重置计时（`DownloadsView.vue:29-33`）；切路由即消失；6 处 `<div class="toast">` 无 `role`（`DetailView.vue:673`、`ReserveView.vue:248`、`DiscoverView.vue:353`、`HistoryView.vue:176`、`DownloadsView.vue:975`、`CreatorView.vue:370`） | **搬** scheduler 纯核心（P1） |
| U7 | `ui/cardDblclickGuard.ts:5-19`：F34 真机事故 → 记录两次点击、跨卡判定（`:31-32` 500ms/5px）、`:94-108` 钉住窗口推迟重排 | `DownloadsView.vue:223-240` 论证"v1 的 0.8s 去抖是 Qt 自报两次，不抄"——**这个判断是对的**；但合并行 `:844-845` 是 keyed `v-for` + `store.merges` 整体替换，**重排竞态这一类**没有任何守卫 | 我们否掉了一种双击故障（正确的），漏了另一种（他们防的那种）。⚠ 这条我**没有实测复现**：未核实 | **部分搬**：只搬纯判定（`noteCardClick`/`isCrossedCardGesture`），不搬 `PIN_WINDOW_MS` 那套等待重排的复杂度 |
| U8 | `ui/icons.ts:7+` 集中注册表 + `ui/icons.test.ts:6-12` 断言"全部是非空 24×24 svg"、`:14-45` "按钮用到的图标都已注册"、`:47-50` 未知名回落 | 34 个内联 `<svg>` 散在 10 个 `.vue`（`DetailView.vue` 7 个、`DownloadsView.vue` 6 个、`LibraryView/DiscoverView/CalendarView` 各 4 个…） | 同一图标多次手抄；无"用没用注册"检查 | **暂不搬**（见 Q5-N5）：Vue 里有 SFC 组件这个更好的位置，该搬的是**测试思路**不是注册表 |
| U9 | `ui/contextMenu.ts:83-88` `isEditableTarget`（鸭子类型，node 可测） | `PlayerView.vue:188-194` `isTypingTarget` 内联一份，只服务播放页热键 | 输入框判据只有播放页有；其它页的热键/`/` 无守卫 | **搬**（P1，一行改导入） |
| U10 | `shared/clipboard.ts` 是**纯契约文件**（无逻辑，9 行） | `contextMenu.ts:33-67` 剪贴板回落（web→Go 桥），另有 `DetailView.vue:186-197`、`DiscoverView.vue:68-79`、`DownloadsView.vue:406` 各自再写一遍 | 同一回落策略 4 处实现 | **搬**成 `ui/clipboard.ts` + 注入 `app` 的可测函数 |

### 2.3 分页反馈

| 他们 | 我们 | 差距 |
|---|---|---|
| 一条规则一处实现且有测：`libraryWall.ts:80-86` `loadMoreRemaining`/`canLoadMore`；`:94-108` `appendLibraryPage` 按 `sourceId::itemId` 去重；`libraryPage.ts:95-120` `applyLibraryListResult(state, result, snapshot, mode)` | **三页三样**（下） | 同一种失败在三页给出三种用户可见承诺 |
| `paginationFeedback.test.ts:6-27` 三条断言：分页失败**保留卡片 + 显示失败 + 「重试加载」**；剧集页同；空后续页**结束分页** | | |

- `LibraryView.vue:189-206`：失败 → `retryPending` + `retrySnapshot`，`:520` 有 `lib-more` 重试按钮。✅ 与他们一致。
- `DiscoverView.vue:233-240`：失败 → `toast` + **`hasMore.value = false`**（`:238`）→ 该页本轮**永久停止翻页，无重试入口**。
- `SearchView.vue:268-282`：`:275` `items.concat(res.items ?? [])` ——**完全没有去重**（`PosterGrid.vue:377` 的 `:key="d.id+'#'+i"` 只是不报重复 key，`DiscoverView.vue:213-220` 的注释已经把这件事说穿过）；`:278-280` `catch { hasMore.value = false }` ——**静默**停翻页，无 toast 无重试。

去重+连续空页停止这条规则也只有两份实现：`LibraryView.vue:120-139`（`knownIds`/`emptyPages>=2`）与 `DiscoverView.vue:221-232`（`seen`/`emptyPages<2`）。`SearchView` 两份都没有。

### 2.4 样式与主题

| # | 他们 | 我们 | 差距 | 搬不搬 |
|---|---|---|---|---|
| S1 | `styles/index.css:1-14` 引 13 个 css；跨页样式在 `components.css`/`feedback.css`/`contextMenu.css` | `styles/` 只有 `tokens.css` + `base.css` + `theme.ts`；其余全在 SFC `scoped`（`DownloadsView.vue:978-1384` = 407 行） | `.toast` 规则被手抄 6 份 | **部分搬**：只把"≥2 个视图用的类"抽共享文件（见 Q3 第④条） |
| S2 | z-index 有阶梯且注释写明理由：`feedback.css:10` toast 90、`:44` confirm 100、`contextMenu.css:4` 1200、`searchHistory.css:4` `1250 /* 高于 E6 右键菜单 1200 */` | 实测 22 处 z-index，值域 1/2/3/30/60/**70**/79/80/1000。`contextMenu.ts:22` 的注释说"应用内弹层最高 60，菜单再高一档" —— **这句话已经过期**：`PlayerView.vue:656` 和 `:922` 就是 70（与菜单同层，胜负由 DOM 顺序决定），`HotkeyHelp.vue:57` 80，`SourceGrid.vue:337/348` 79/80，`SoraniLoginDialog.vue:203` 1000 | 注释给出的**理由是错的**，而错理由比没理由更危险 | **搬**：立一张阶梯表 + 一个静态测扫全仓（P3） |
| S3 | `packages/theme/src/resolveTheme.ts:13-20` 纯函数 + `resolveTheme.test.ts`；`constants.ts:10,13` 唯一来源；`themeController.ts:15-31` 暴露 `getThemeState()`（含 `resolved`）与 `subscribeTheme` | `styles/theme.ts:33-37` `applyResolved` 是**私有且直接摸 document/matchMedia**；`:39` `currentMode` 私有；**没有任何导出能问"现在解析出来是 light 还是 dark"**；`SettingsView.vue:21` 自己 `ref(loadTheme())` = 第二份真相 | 主题解析不可测、不可查询、真相有两处 | **搬纯函数**（P0 顺手）；**不搬独立包**（见 Q5-N3） |
| S4 | `resolveTheme.ts:25-37` `isThemePreference`/`coerceThemePreference` 把"非法输入"当独立概念测 | `theme.ts:16-23` `loadTheme` 里 `VALID.includes` 顺手做，`:25-31` `systemIsDark()` 在 matchMedia 抛错时**返回 true**（"按产品默认 dark"），无测试钉 | 这条降级规则没有任何东西守着 | **搬** |
| S5 | `tokensParity.test.ts:10-58` 显式 REQUIRED_VARS 名单 | 我按同一套 `extractBlock`/`declaredVars` 现量：`tokens.css` dark 94 个变量 / light 94 个，**当前集合对称，0 差集**；全仓 `var(--x)` 未定义引用 **实测 0 处**（唯一命中是 `tokens.css:4` 注释里"只用 var(--token)"）。⚠ **这里已有一道门**：`verification/theme_contrast_verify.py` 的 ①② 用 `getComputedStyle` 实测浅/深两套的 WCAG 对比度（文件头 `:10-12` 记录修前 1.10:1 → 修后 12.46:1；四条判据在 `:23-29`），③ 是源码静态扫描 | 缺口不是"没门"，是**那道门的三个硬限制**：(a) ③ 是**写死的 7 个名字黑名单**（`theme_contrast_verify.py:153-154` `banned = [...]`），只能防"上一批犯过的拼法"再来一次，**新造一个未定义名字它不知道**；(b) 它要起浏览器 + 独占 5212 端口，**不在 CI**；(c) 它不判 dark/light **键集合对称**（那条只在 `palette_audit.py` 的 B 里，同样不在 CI）。另：`research/v1-v2-creator-and-tokens.md:45-77` 那份"7 个未定义引用"清单**已经过期**，那 7 个引用现在都不存在了（我逐个 grep 过） | **搬** T4 那个测，并要求它把黑名单升级成**全量对称/全量定义**判定 |
| S6 | 组件不写字面色（靠 `var(--x, 回退)`） | 实测 `.vue` 的 `<style>` 块里 **16 处字面色**：PlayerView 5 / LibraryView 4 / HotkeyHelp 2 / SoraniLoginDialog 2 / SeasonMenu 1 / DetailView 1 / DownloadsView 1 | 其中 3 处是**已定义的 token 被手抄成字面量**：`LibraryView.vue:710/714/718` 写死 `#ff7a45`/`#ffc04d`/`#5ec2ff`，正是 `tokens.css:93-95`（和 light 块 `:199-201`）的 `--rank-1/2/3` | **搬**：静态测断言"字面量若等于某 token 值 → 必须用 var()" |
| S7 | — | `base.css:1` 自述契约"只引用 tokens，不允许出现字面色值"（对 base.css 成立），但 `.vue` 无任何机械检查（`theme_contrast_verify.py` 判的是**计算值对比度**，字面色只要对比够就一路放行）；`:14` 字体族写死，全仓**没有 `--font-sans` token**（`--font-mono` 有，`tokens.css:108/213`，仅 `HotkeyHelp.vue:112` 用过一次） | 契约写在注释里而不是测里 | **搬**：把注释变断言 |
| S8 | `themeSection.ts:11-15` LABELS 单处；`:27` `role="radiogroup"`+`aria-label` | `SettingsView.vue:22-26` 自己列 options + i18n 键（还行），但**主题切换没有 radiogroup 语义** | 轻 | 顺手改 |

### 2.5 `features/<域>` 布局

| 他们 | 我们 | 判断 |
|---|---|---|
| `features/<域>/` 里是 `xxx.ts`(纯) + `xxx.test.ts` + `xxxDom.ts`（DOM 壳）。例：`searchHistory/searchHistory.ts` 85 行纯函数（`:1-85`，`StorageLike` 注入）+ `searchHistoryDom.ts` 191 行 DOM 绑定 | `views/*.vue` 一个文件里混着 状态机 + 布局 + 样式。`DownloadsView.vue` 1384 = script `1-563` / template `564-977` / style `978-1384` | **值得搬的是切分，不是目录**：我们的渲染在 SFC 模板里，没有"render 函数模块"这个位置可对应 |
| `player/playerCommon.ts:149-158` `PLAYER_SHORTCUTS` 表 + `:163-171` `resolvePlayerShortcut` + `:186-200` `shortcutToControl` + `:176-183` `dockShouldAutoHide`，全纯、全测 | `PlayerView.vue:196-249` `onHotkey` 一个 switch 直接把按键接到 `store.togglePlay()`/`store.seek()` | 同一件事他们是"表→纯函数→副作用"三段，我们是一段 |

---

## 三、六个问题，逐条落到文件

### Q1 `ui/` 纯逻辑 + 单测，在我们这怎么落地；vitest 最小成本

**引入成本（4 个动作，都在 `client-v2/frontend/`）**

1. `package.json`：加 devDependency `vitest`。我们 vite 实测 `6.4.5`（`package-lock.json:1479-1482`），
   配套的是 vitest 3.x —— **具体小版本未核实**，我没跑安装；
   registry 现为 `https://registry.npmmirror.com`（`npm config get registry` 实测），
   CI 已备双源回落（`build-v2.yml:163-170`），**离线能否装成功未核实**。
2. `package.json` scripts：加 `"test": "vitest run"`、`"test:watch": "vitest"`。
3. 新建 `vitest.config.ts`，**逐字照抄**他们 `apps/desktop/vitest.config.ts`：
   `test: { environment: 'node', include: ['src/**/*.test.ts'] }`。
   不需要 vue 插件、不需要 jsdom/happy-dom、不需要 `@vue/test-utils`。
   **前提纪律**：只测纯模块。要测带 DOM 的东西，先把它拆成"纯核心 + DOM 壳"两段
   （模板就是 `ui/toast.ts:15-70` / `:74-116`）。
4. `tsconfig.json` **不动**：`include` 已含 `src/**/*.ts`（`:35-37`），
   `npm run typecheck`（`vue-tsc --noEmit`，`package.json:8`）会自动覆盖 `.test.ts`。
   ⚠ 代价是测试文件也得过 `noUnusedLocals`/`noUnusedParameters`（`:26-27`），
   写测试时别留未用变量。因为 `types: ["vite/client"]`（`:32`）里没有 vitest 全局，
   **必须显式 `import { describe, it, expect } from 'vitest'`** —— 这正是他们全部测试文件的写法
   （`tokensParity.test.ts:8`、`icons.test.ts:1`），零额外配置，照抄就好。

CI 接线：`build-v2.yml:171` 那行 `npm run build` 之前插 `npm run test`。
（现状是前端在 CI 里**只有 install + build**，连 typecheck 都不跑 —— 加这一步不占用别的预算。）

**最该先抽的现有逻辑（按"已可感知错 × 可测性"排，每条都是具体行号）**

| 优先级 | 抽什么 | 现在在哪 | 抽到哪 | 为什么先它 |
|---|---|---|---|---|
| A0 | 4 份完全相同的 `coverUrl` | `DetailView.vue:512-516`、`ReserveView.vue:154-157`、`CalendarView.vue:50-53`、`CreatorView.vue:51-55` | `services/assetUrl.ts` | **零行为变更的纯移动**，用来把 vitest 骨架跑通；四份里三份写法还不一样（DetailView 是 3 行 return，另三份是三元一行） |
| A1 | 已有的 3 个零依赖纯模块，**不用抽，直接写测** | `services/dramaRef.ts`（83 行/7 导出：`compactDrama:7`、`detailRoute:27`、`playerRoute:34`、`dramaFromQuery:46`、`chapterEpisode:64`、`chapterVip:72`、`updateCount:80`）、`services/updatenotes.ts:7` `cleanReleaseBody`（被 `App.vue:23` 用）、`stores/downloads.ts:32-42` `STAT_GROUPS`/`statMatch` | 就地同名 `.test.ts` | 证明"vitest 在这仓能用"最便宜的一条路；这三个文件 `grep` 无 `window`/`document`/`localStorage`/`navigator` |
| A2 | 分页三规则（去重 / 连续空页停 / 失败保内容） | `LibraryView.vue:120-139`+`:189-206`；`DiscoverView.vue:221-232`+`:233-240`；`SearchView.vue:268-282` | `ui/listPagination.ts`：`appendPage(existing, incoming)` + `remaining(total, loaded)` + `canLoadMore(...)` + `applyListResult(state, res, mode)` | 2.3 节的三页分歧；照 `paginationFeedback.test.ts:6-27` 写 3 条断言 |
| A3 | Esc/浮层优先级链 | `services/overlays.ts:19`（选择器串）+ `:37-61`（记账）+ `contextMenu.ts:254-256` + `DetailView.vue:488-498` + `PlayerView.vue:199-210` | `ui/escLayers.ts`：`resolveEscTarget(input)`（可**逐字搬**他们 `contextMenu.ts:50-62`）+ `isEditableTarget`（搬 `:83-88`） | 修 F5-①；`esc_semantics_verify.py` ①-⑤ 现成是验收口径 |
| A4 | 播放页热键表 | `PlayerView.vue:196-249`（switch）+ `:188-194`（输入框守卫） | `ui/playerShortcuts.ts`，照 `playerCommon.ts:149-171` + `:186-200` 拆"表 / resolve / toControl" | 拆完 `isTypingTarget` 自然合并进 A3 的 `isEditableTarget`；顺带让热键能给别的页用 |
| A5 | 联想框键盘导航（含 IME 守卫） | `SearchView.vue:166-195`：`:167` `isComposing || keyCode === 229` 不劫持、`:170-175` 环回、`:176-181` Enter 与 `:188-194` `suggestEnterPicked` 标志握手 | `ui/suggestKeyboard.ts`（纯：输入 `key/index/n` → 输出 `nextIndex/action`） | 那段 Enter 与 `onSearchEnter` 的**双处理器握手**（两个模块靠一个布尔标志互相知会）是典型只靠 e2e 才能验、其实完全能单测的东西 |
| A6 | "手势后吞一次点击"两份实现 | `PosterGrid.vue:321-330`（下拉刷新释放后 300ms）、`SourceGrid.vue:240-253`（拖拽结束后 500ms） | `ui/gestureClickGuard.ts`，阈值作常量、时钟注入（照 `cardDblclickGuard.ts:47-63`+`:94-108`） | 两份是同一件事的两个魔法数（300/500）；合起来后可对齐 U7 的跨卡判定 |
| A7 | toast scheduler | 7 份（U6 行号） | `ui/toast.ts`，**纯核心照抄**他们 `:15-70`，DOM 壳改成一个挂 `body` 的 Vue 组件（`:74-116` 的形状） | F5-③；同时修"连发被覆盖 + 切路由消失 + 无 role" |
| A8 | 剪贴板回落 | `contextMenu.ts:33-67`、`DetailView.vue:186-197`、`DiscoverView.vue:68-79`、`DownloadsView.vue:406` | `ui/clipboard.ts`（`app` 与 `navigator` 作参数注入） | 4 份 web→Go 桥回落；`contextMenu.ts:6-8` 注释把策略写清了但代码在四处漂移 |
| A9 | 菜单项模型 + 位置收敛 | `contextMenu.ts:174-218` `buildItems`（判定 + 建对象混在一起）、`:304-308` clamp | `ui/contextMenuModel.ts`：`buildItems` 改成吃鸭子类型 `{hasSelection, editable, copyAllBlock}`，`clampMenuPosition` 照搬 `:65-78` | `buildItems` 现在直接调 `t()` 和读 `window.getSelection()`，把**判定**与**文案**分开就能测 |

**验收口径**：`npm run test` 绿 + `npm run typecheck` 绿 + `run_all.py` 30/30 不变。
**风险**：低。全部是**新增文件 + 视图改 import 行**；不动模板、不动类名 →
Playwright 断言依赖的 `.toast`（20 处）、`.zgj-ctx-menu`（14 处）、`.zgj-ctx-item`（6 处）、
`.pl-toast`（5 处）、`.toast-action`（2 处）选择器一个都不变。

---

### Q2 `features/<域>` 对我们值不值；拿一个文件做样板

**判断：值得做，但不是现在，且不是搬目录名**。理由：我们的渲染边界在 SFC 模板里，
他们 features 的核心价值（`renderXxx(state)` 与 DOM 绑定分离）在 Vue 里由框架提供了。
把 `views/` 改名成 `features/<域>/` 的收益**只在测试可寻址性**，
而这件事 A0-A9 已经用 `src/ui/*` 拿到了——不用动任何一个视图文件的位置。

**样板切法（只做 `DownloadsView.vue`，别全切）**

为什么挑它做样板，两条实测理由：
- 它最大（1384 行）且 script/template/style 三段最整齐（`1-563` / `564-977` / `978-1384`）；
- **`research/*.md` 里 `DownloadsView.vue:行号` 的引用数 = 0**（密度实测：LibraryView 28、
  SearchView 26、CreatorView 22、`player.ts` 21、DiscoverView 19、PlayerView 14、DetailView 12，
  DownloadsView 与 SettingsView 都没有）。切它，引用失效成本为零。

切法（5 步，每步独立可验收、可单独回滚）：

| 步 | 从 `DownloadsView.vue` 拿走 | 放到 | 验收 |
|---|---|---|---|
| 1 | `:56-149` 展示辅助：`STATE_TEXT`、`MERGE_STATE_TEXT`、`stateText`、`mergeStateText`、`mergeRangeText:82`、`epNo:92`、`epLabel:99`、`fmtBytes:109`、`fmtSpeed:121`、`fmtEta:126`、`pctOf:134`、`sizeText:139` | `features/downloads/format.ts`。⚠ 这几个里 `fmtEta`/`epLabel` 用了 `tf()` → 把**文案格式化器作参数注入**（`fmtEta(sec, tpl)`），纯函数只管算 `1h02m`；这是他们 `playerCommon.ts:59` `formatClock` 的做法 | 单测钉 `fmtBytes(0)`/负数/`1023`/`1TB`、`pctOf` 的 clamp、`epNo` 的 `index<1` 分支 |
| 2 | `:162-221` 勾选与合并目标：`toggleSelect:164`、`:171-178` 的存活清理、`selectedCompletable:181`、`selectedSingleDrama:182`、`toggleMergeGroup:197` | `features/downloads/selection.ts`（纯：`Set` 进 `Set` 出） | 单测钉"任务消失后勾选自动收敛"（`:171-178` 这段 watcher 的语义）与 `selectedCompletable` 的空集 `every` 恒真坑 |
| 3 | `:271-314` 统计条筛选：`statChips:284`、`effSource:298`（"所选源消失则不再生效"）、`visibleJobs:302`、`filteredEmpty:309`、`chooseStat:312` | `features/downloads/filters.ts`（复用 `stores/downloads.ts:40` `statMatch`） | 单测钉 `chooseStat('')` 清、再点同项=取消（`:313` 那行的三元）、`effSource` 失效降级 |
| 4 | `:241-246` `onMergeRowDblclick` | `features/downloads/dblclick.ts`，接 A6 的 `ui/gestureClickGuard` | `merge_multi_verify.py:217` 的 dblclick 断言必须照旧绿；`:227` 那条注释说旧的恒真断言已删——**不要把它写回来** |
| 5 | `:203-221`/`:248-268` 等桥接调用留视图 | — | `run_all` 全量；重点 `downloads_statbar_verify.py`、`merge_multi_verify.py` |

切完 `DownloadsView.vue` 的 script 从 563 行降到 ~250 行（全是桥接调用 + `ref` 装配）。
**风险**：中。第 1-3 步是纯移动（模板里的调用点名字不变，加 import 即可）；
第 4 步碰行为，必须单独一个 commit 才好回滚。
**回滚**：每步一个 commit；步 1-3 出问题直接 revert，视图文件未被破坏性修改。

**其它 10 个视图现在不要切**——按 Q6 的引用密度，LibraryView/SearchView/CreatorView 切一次的
引用重指向成本是 DownloadsView 的无穷多倍（它 0 处）。

---

### Q3 样式拆分与 `packages/theme` 式主题模块，我们的具体缺口

先给四条**实测**的"我们已经做对的"，免得把方案写成吐槽：
- `tokens.css` dark/light 各 94 个变量，**键集合完全对称，差集为 0**；
- 全仓 `var(--x)` **未定义引用 = 0**（`research/v1-v2-creator-and-tokens.md:45-77` 那份清单已过期，那 7 个引用现在都不存在）；
- `styles/base.css:1` 立的"只引用 tokens"契约，对 base.css 本身成立。
- `theme_contrast_verify.py` 已经在用 `getComputedStyle` **实测浅/深两套计算值的 WCAG 对比度**
  （文件头 `:10-12`：修前 1.10:1 → 修后 12.46:1；判据四条在 `:23-29`）——这一维比"读源码字面量"强，我没有方案能替它，
  下面的建议都是**在它之上补静态门**，不是把它换掉。

**真缺口，四条，每条带文件:行号**

**① 主题解析没有纯函数、没有可查询状态、真相有两处。**
`styles/theme.ts:33-37` `applyResolved` 是私有的，且**在函数体里同时**读
`window.matchMedia`（`:25-31`）和写 `document.documentElement`（`:35-36`）——
这就是它无法单测的全部原因。对照他们 `resolveTheme.ts:13-20`：
`resolveTheme(preference, systemIsDark) → ResolvedTheme`，两个入参一个返回值，`resolveTheme.test.ts` 测它。
另外：`:39` `currentMode` 是私有变量，外部**无法问"现在实际是 light 还是 dark"**；
他们 `themeController.ts:15-31` 把 `{preference, resolved, systemIsDark}` 做成可读状态 + `subscribeTheme`。
我们的第二份真相在 `SettingsView.vue:21`：`const themeMode = ref<ThemeMode>(loadTheme())`，
它不知道 `auto` 已经解析成了什么，所以设置页选中"跟随系统"时不显示当前落到哪一边。
`:25-31` 的降级（matchMedia 抛错 → 返回 `true` 按 dark）也没有任何测试钉着。
`resolveTheme.ts:25-37` 的 `isThemePreference`/`coerceThemePreference` 把"脏输入"当独立概念测，
我们只在 `theme.ts:18-19` 顺手 `VALID.includes`。

落地：新建 `src/ui/themeModel.ts`（纯：`resolveTheme`、`coerceThemePreference`、`isThemeMode`）+
`themeModel.test.ts`；`theme.ts` 改成**只保留 DOM 壳**并 `import` 它，
再加一个导出的 `getResolvedTheme()`。分 3 步，每步单独可验收：
纯函数+测 → theme.ts 改为调用 → SettingsView 改读 `getThemeMode()`。
风险：低（`theme.ts` 的全部调用方实测只有 2 个文件：`main.ts:6,10` 与 `SettingsView.vue:14,42`）。
**不搬** `packages/theme` 那个独立包（见 Q5-N3）。

**② z-index 没有阶梯，且唯一的说明是错的。**
见 S2。落地：`styles/layers.css` 里定义阶梯变量（`--z-dock/--z-mask/--z-pop/--z-menu/--z-toast/--z-modal`），
`ui/` 或 `styles/` 旁放 `zIndexAudit.test.ts`：读全仓 `.vue`/`.css`/`services/contextMenu.ts`，
断言每个 z-index 值落在阶梯表内、且**没有两个不同用途共用一档**。
现存要改的具体冲突：`contextMenu.ts:22` 的 `Z_INDEX = 70` 与 `PlayerView.vue:656`、`:922` 的 70 同层；
`contextMenu.ts:22` 那句"应用内弹层最高 60"必须一起改，否则错理由继续留在原地。
风险：中——纯数值改动会影响 e2e 的截图/命中判定，建议放 P3 单独 commit。

**③ 组件里还有 16 处字面色，其中 3 处是"token 已存在却被手抄"。**
`LibraryView.vue:710/714/718` 写死 `#ff7a45/#ffc04d/#5ec2ff`，正是 `tokens.css:93-95`（dark）
和 `:199-201`（light）的 `--rank-1/2/3` —— 改这两个 token 时榜单颜色不会跟着动。
另一类是真症状：`DetailView.vue:714` `linear-gradient(180deg, rgba(15, 17, 23, 0.35), var(--bg) 92%)`
把**深色 `--bg` 的 RGB（15,17,23 = `#0f1117`）烘进了渐变**，浅色主题下起点是深灰 → 与
`v1-v2-creator-and-tokens.md:70-74` 记录的 SeasonMenu 那类"有回退掩护下的主题错配"同族（SeasonMenu 的
4 处已改好，我 grep 过；这一处是新发现的）。
落地：`ui/tokensAudit.test.ts`，照 `tokensParity.test.ts:60-86` 的 `extractBlock`/`declaredVars` 形状，
把 `theme_contrast_verify.py:153-154` 那份 7 个名字的黑名单**升级成全量判定**（引用名必须已定义、
dark/light 键集合必须对称），
再加两条：(a) 字面色若逐位等于某个 token 的 dark/light 值 → 报"该用 var(--x)"；
(b) `rgba(r,g,b,…)` 里的 r,g,b 等于某 token 值 → 报。前者现在就能抓到那 3 处。
风险：低（只新增测，不动 CSS）。先让它**只报 rank 那 3 处**再逐步扩名单，别一上来把 16 处全判红。

**④ 跨视图样式只有 2 个共享文件，`.toast` 手抄 6 份。**
他们 `index.css:1-14` 拆 13 个文件；我们不照搬这个粒度——
只抽**实测被 ≥2 个视图复制**的类：`.toast`（`DownloadsView.vue:1369-1384` 等 6 段）、
遮罩类（z-index 60 那组：`DetailView.vue:963`、`ReserveView.vue:380`、`DiscoverView.vue:404`、
`HistoryView.vue:306`、`CreatorView.vue:548`）。
落地：新建 `styles/feedback.css`（名字照他们 `feedback.css`，toast + confirm + 遮罩），
`main.ts` 里加一行 import。**不动** scoped 里的视图私有样式。
风险：低-中——`.toast` 从 scoped 变全局类，要确认 6 份定义不冲突（现测：字号/内边距略有差异，
统一成一份会有一两页视觉微变，需肉眼比一次截图）。

---

### Q4 体验层面：他们做对了、我们没有的（每条指到代码）

**① 一次按键只做一件事：靠"单点分发"，不靠记账。**
他们：`main.ts:3785-3812` 是**唯一**的全局 keydown，Esc 落向由
`contextMenu.ts:50-62` 的 `resolveEscTarget` 纯函数决定；
`contextMenu.ts:8-9` 与 `:154` 两处明写"本组件不自注册 Esc 监听，避免双监听导致一次按键连关两层"。
我们：Esc 有 **6 处**监听 —— `contextMenu.ts:259-260`（document 捕获阶段）、
`DetailView.vue:501`、`PlayerView.vue:380`、`SearchView.vue:349`、`SourceGrid.vue:260`、
`SoraniLoginDialog.vue:122`；为了让它们不打架，`overlays.ts:37-61` 额外造了 window 捕获阶段的
"Esc 记账"。而 `overlays.ts:10-12` 自己写了这个机制的软肋：
"用注册顺序去赌谁先跑是站不住的"。**我们靠一个补丁治另一个补丁**；
他们靠删掉补丁的成因。这是本方案里最实的一条差距。
另外：`overlays.ts:14-15` 立了约定"新增浮层必须落进 `.zgj-ctx-menu` / `[role=dialog]`"，
但 `HotkeyHelp.vue:36` 那个整屏遮罩（z-index 80，`:57`）**两个标记都没有** ——
约定在自己仓里已经被破了，只是因为 `PlayerView.vue:201` 的 `helpOpen` 判断排在
`:207` 的浮层判断之前才没出事（同一处理器内侥幸有序，跨处理器就不成立）。

**② 右键菜单不可键盘操作，且没有 ARIA。**
他们：`contextMenu.ts:130-139` 挂 `role=menu`/`role=menuitem`（`contextMenuKeyboard.ts:9-11`）+
`data-testid`；`:153-189` Arrow/Home/End/Enter/Space，跳过禁用项（`nextMenuHighlight`，
`contextMenuKeyboard.ts:25-39`，**有独立测试**）；`:190-191` 先 `setHighlight` 再 `:217` `menu.focus()`。
我们：`contextMenu.ts:279-300` 用 `div`（`:284`）当菜单项 —— 不可聚焦、Enter 无义、
`role` 一个没有、没有 `focus()`；`:254-256` 唯一认的键是 Escape。
（`verification` 里 5 处 `keyboard.press("ArrowDown")` 全打在搜索联想框，不在右键菜单。）

**③ 破坏性操作零确认。**
他们：`confirmDialog.ts` 是 Promise 型 + FIFO 队列（`:30-50`，连续两次调用按序弹不串），
`:103-111` Tab 焦点陷阱，`:115-118` 点遮罩空白=取消，`:124-127` Escape=取消，
`:9-12`+`:99` `destructive: true` → 确认按钮走 danger 样式，`:120`+`:64-67` 焦点去/回。
`main.ts` 里 6 处调用。
我们：**没有这个东西**（`grep 'confirm('` 全仓 0 命中；`components/` 下只有
`SoraniLoginDialog.vue` 一个对话框）。而这些操作直接落刀：
`DownloadsView.vue:728` → `onRemoveBatch`（`:258-268` 直接 `store.removeBatch`）、
`:835` `store.mergeCancel('')` 停全部合并、`:964` 逐条 `remove`、
`HistoryView.vue:94-102` `onClear` 直接 `clearHistory()` 清空整张历史表。
⚠ 代价要说明白：加确认框会让现有 Playwright 脚本少点一次 → 必须同步改脚本
（实测涉及 `merge_multi_verify.py`、`history_local_play_verify.py`、
`follow_check_verify.py`、`calendar_paging_verify.py`、`contextmenu_verify.py`、
`searchall_verify.py`、`i18n_sweep_verify.py` 7 只提到删除/清空的脚本，
其中真正点到危险按钮的以 `merge_multi_verify.py` 与 `history_local_play_verify.py` 为主）。
所以它排在 P4，不与 P1/P2 混在一个 commit。

**④ 分页失败时，用户看到的承诺三页三样。**
他们：`paginationFeedback.test.ts:6-27` 三条断言把承诺钉成一条规则
（失败 → 保留卡片 + 显示原因 + 给「重试加载」；空后续页 → 结束分页并**保留已看到的条目**）。
我们：LibraryView 达标（`:189-206` + `:520`）；DiscoverView 不达标
（`:238` `hasMore = false` → 本轮永久停，无重试）；SearchView 更差
（`:278-280` `catch { hasMore.value = false }`，**无 toast、无重试、无声失败**，
且 `:275` 不去重 → `DiscoverView.vue:213-220` 注释里已经分析过的那种"两张一模一样的卡"
在搜索页依然会发生）。这一条不用搬任何概念，把 A2 抽出来就能三页归一。

**⑤ toast 是变量不是队列。**
他们：`toast.ts:29-70` scheduler（`order[]` + `Map`，`push/dismiss/tick`），
`:74-116` DOM 壳挂 `document.body`（`:83`）→ **跨路由不丢**；
`:82` 容器 `aria-live="polite"`，`:97` error 用 `role="alert"`、其余 `role="status"`；
三态 kind（`:5`）在样式上有左边框色区分（`feedback.css:28-30`）；时长单点常量（`:25` 3200）。
我们：每视图一个 `ref('')`，`showToast` 覆盖前一条 + 重置计时
（`DownloadsView.vue:29-33` 是最小样本，6 份同形）；6 处渲染无 `role`（U6 行号）；
切路由就没了（因为 markup 在视图模板里）；时长两种写死（3200 与 `SettingsView.vue:36` 的 4000）；
`PlayerView.vue:603` 另有第 8 套 `.pl-toast`（走 `store.hint`，带 `role="status"`，反而最像他们）。
（`toast` 有跳转链接这件事我们做得比他们好：`DetailView.vue:675` + `.toast-action`，保留。）

**⑥ 焦点归位与键盘可达性在关闭路径上缺失。**
除了 ②，`confirmDialog`/`contextMenu` 关闭都会把焦点还给触发元素
（`confirmDialog.ts:64-67`、`contextMenu.ts:111-116`）。我们两处都不还
（`contextMenu.ts:241-248` 只 `remove()`）。这是"菜单关掉后 Tab 从页头重新开始"那种体验毛刺的来源。

**⑦ 一条诚实的差异**：他们 `cardDblclickGuard.ts:5-19` 防的是"两次点击之间列表重排，
第二击落到移进同一位置的另一张卡上"（F34 真机事故）。
我们 `DownloadsView.vue:223-240` 论证过"v1 那 0.8s 去抖是 Qt 自报两次，不抄"——
**那个判断正确，别推翻**（`merge_multi_verify.py:227` 记录了旧断言因此被删）。
但重排竞态是另一回事，而我们的合并行是 keyed v-for（`:844-845`）且 `store.merges` 由轮询整体替换 →
风险形状存在。**⚠ 我没能实测复现这条（未核实）**，所以它在方案里是 P1 的"可选加做"，不是必须。

---

### Q5 我明确不搬的（带理由）

| # | 不搬 | 为什么（证据） |
|---|---|---|
| N1 | Electron 全套：`asar`（`electron-builder.yml:30`）、`nsis`（`:52`、`:78` 整块）、打包链 `electron-vite`+`electron-builder`+`electron-updater` | 我们是 Wails 单 exe 覆盖升级。换过去 = 每次升级下整包 + 重写发布链。体积账已在 `research/repo-structure-proposal.md` 三节量过：我们 exe 21MB + runtime 139MB（主要 mpv/ffmpeg），Electron 自带 Chromium 通常 80–150MB 单包 |
| N2 | `preload` IPC 模型：`src/preload/{index.ts,playerPreload.ts}`、`src/main/` 那 40 个 `xxxIpc.ts` + channel 字符串契约（`shared/clipboard.ts:6-11`） | 我们的对应物是 Wails 直接生成 Go 方法绑定（`services/contextMenu.ts:29-31` 的 `(window as any).go?.main?.App`、`App.vue:40` 的 `runtime.EventsOn`），没有 invoke/handle 通道这一层。所以**不要**为了对齐他们再造一层 channel-string IPC 壳。⚠ 注意：D3 已经落地了 `services/bridge.ts`(实测 287 行) + `contract.ts`(377 行) + `mock/`，`api.ts` 收成 57 行 facade —— 那条线是**按我们自己的 Wails 形状**拆的，不是在模仿他们的 preload IPC，本文不评判它的拆法（那是 D3 的决定，已发生）。 |
| N3 | 四端 monorepo：`apps/{android,mobile,tv}` + `packages/{theme,sync-client,sync-protocol}` + `pnpm-workspace.yaml` | 我们只有一个客户端。搬来要先建 `apps/`/`packages/` 目录、把 root `package.json` 改成 6 条 `--filter` 的 typecheck 矩阵（他们 root `package.json:22` 那种），为不存在的端付维护费。`packages/theme` 的**纯函数切分**搬（Q3-①），**包**不搬 |
| N4 | `renderXxx(state): string` 模板字符串渲染 —— 他们 features 里到处是（`themeSection.ts:17-50`、`libraryPage.ts` 的 `renderLibraryPage`、`playerCommon.ts:112` `trackElements`） | 这是"没有框架"的产物。它把转义变成手工活：`playerCommon.ts:68` 专门有个 `esc()`。Vue 模板自动转义，我们搬过来只会新造一个 XSS 面 |
| N5 | `ui/icons.ts` 集中 svg 字符串注册表 | 我们有 SFC，图标该做成组件（或用 `<svg>` slot），不是字符串表。他们 `icons.test.ts` 的**断言形状**值得搬（"用到的图标都存在"、"未知回落"），实现形状不搬 |
| N6 | 他们 `renderer/src/main.ts` 那个 4196 行单体本身 | 反面例子，不是目标（同 `repo-structure-proposal.md`） |
| N7 | 把 `verification/*_verify.py` 换成他们的 e2e/specs 结构 | 我们 30 只脚本自带端口独占与孤儿进程归因（`_devserver.py` 文件头把 R8 事故写得很清楚），换栈=重造这套硬换来的能力，收益只是目录名 |
| N8 | 照搬 `styles/` 拆 13 个文件 | 他们的粒度是无框架 DOM 的产物；我们 scoped style 天然按视图分。只抽"被复制的类"（Q3-④） |

---

### Q6 排序：哪些先做、哪些必须等 D2

**先给排序用的实测数据**（`research/*.md` 里的行号引用面，我用正则数的唯一引用数）：

| 引用形态 | 唯一引用数 | 谁会被打断 |
|---|---|---|
| `*.py:行号`（v1 Python） | **370**，分布在 17 份文档 | D2 删 `zhenguojian/app/` 时全部失效 → 所以 D2 要先打 `v1-final` tag（`dev-plan-2026-11.md:36-40`） |
| `*.vue:行号` / `*.ts:行号`（我们自己的文件） | **189** | **任何移动我们前端文件**的动作 |
| `*_verify.py:行号` | 30 | 改验收脚本 |

我们侧的引用密度（同一量法）：LibraryView 28、SearchView 26、CreatorView 22、
`stores/player.ts` 21、DiscoverView 19、PlayerView 14、DetailView 12、`api.ts` 11、
`i18n.ts` 10、`fixtures.ts` 9、PosterGrid 8、HotkeyHelp 4、`App.vue` 4、SeasonMenu 3 …
**DownloadsView.vue 与 SettingsView.vue = 0**。

据此的排序（每条标 gate）：

**低风险、先做，不动现有 Playwright 断言（P0–P2）**

- **P0 · vitest 骨架**：只新增 6 个文件（`vitest.config.ts` + `services/assetUrl.ts` +
  4 个 `.test.ts`：assetUrl / dramaRef / updatenotes / downloads），外加 `package.json` 两行 +
  `build-v2.yml:171` 前插一行测试。对象是 Q1 的 A0+A1 ——
  即"已有纯模块 + 4 份 coverUrl 合并"。**没有任何现有文件被移动**，189 处引用一处不破。
- **P1 · `src/ui/` 纯模块**：A2 分页、A3 Esc 纯函数、A4 热键表、A5 联想键、A6 手势守卫、
  A7 toast scheduler、A8 剪贴板、A9 菜单模型。
  新增 `src/ui/`（新目录，不移动 views）；视图只改 import 行 → 类名/选择器/模板不动。
- **P2 · Esc 单点分发**：改 4 个文件（`services/overlays.ts`、`services/contextMenu.ts`、
  `views/DetailView.vue`、`views/PlayerView.vue`）。
  **验收口径已经现成**：`esc_semantics_verify.py` 的 ①-⑤（脚本 `:19-25` 的契约注释）
  就是"一次按键只做一件事"的完整契约，绿了就算过。
  这条是整个方案收益最高、也最容易把行为改坏的一步 → 单独一个 commit，
  且**不并入** P1。删掉 `overlays.ts:37-61` 的记账是它的完成标志。

**需要同时改验收脚本（P3–P4），仍不需要等 D2**

- **P3 · z-index 阶梯 + tokens/字面色审计测**（Q3-②③）。
  纯数值与新增测；但数值改动会影响 e2e 的元素命中/截图，要跑全量。
- **P4 · `ui/confirmDialog.ts` + 接入 4 处破坏性动作**（Q4-③）。
  这条**必须同步改** `merge_multi_verify.py`、`history_local_play_verify.py` 等脚本（多一次点击），
  所以放在"当前有一批脚本改动被允许"的窗口里做，不要和 P1/P2 混 commit。

**必须等 D2（v1 目录移除 + `v1-final` tag）之后**

- **P5 · `views/` → `features/<域>/` 的目录改名**（Q2 全量铺开）。理由：
  它一次性废掉 **189 处**我们自己文件的 `文件:行号` 引用（上表实测），
  而 D2 已经在废 **370 处** v1 引用。两件事分开做 = 引用重指向两次、git 追溯断两次；
  合在 D2 之后做 = 一次性把两份引用都指向稳定的 tag/新路径。
  与 `dev-plan-2026-11.md:108-110`、`repo-structure-proposal.md` 五节 D4 的结论一致。
- **P5 的例外**：Q2 的 `DownloadsView.vue` 样板切法（引用数 0）**不用等 D2**，可以就在 P1 里做。
  这是我相对 dev-plan 加的一条：既然引用成本为 0，样板就不该被 gate 挡住 ——
  先拿它把 `features/<域>/` 的形状验证一遍，再决定别的 10 个视图值不值得排 P5。

**依赖 D1 之后才有意义的**：无。本方案与更新链（D1）无交集。

---

## 四、我明确不建议做的

1. **不要为了"像他们"把 `views/` 改名成 `features/`**。目录改名的实际收益（测试可寻址性）
   已经被 `src/ui/*` 拿到；改名只剩 189 处引用成本。要改名也得是 P5，不是现在。
2. **不要在 vitest 里测组件渲染**。不开 `jsdom`/`happy-dom`，不装 `@vue/test-utils`。
   他们的 `environment: 'node'`（`vitest.config.ts`）不是偷懒，是纪律：
   测不动的东西说明它没被切成纯模块 —— 那就先切模块。加 jsdom 会让这条纪律失效。
3. **不要删任何 `*_verify.py`**，也不要把断言"上移"到单测。他们自己就是单测（60 文件）
   加 e2e（45 spec）两条腿。P1-P4 里如果某条 Playwright 断言变得多余，
   **先留着**，等它因为真·行为改变而必须改脚本时再顺手收。
4. **不要移植 `cardDblclickGuard.ts:75-108` 的"钉住窗口"**（`PIN_WINDOW_MS`/
   `waitGesturePinCleared`）。那是为"网格在两次点击之间被重排/换占位"准备的，代价是把刷新改成异步等待。
   我们目前没有实测复现这条（Q4-⑦ 标了未核实）。只搬 `:22-63` 那两个纯函数就够。
   同理：`DownloadsView.vue:223-240` 论证过"别抄 Qt 去抖"，那个结论**不要推翻**。
5. **不要把 `buildItems` 改成"禁用项 + hint"那套**（他们的 `buildCardMenuItems`，
   `contextMenu.ts:32-48`）。我们 `contextMenu.ts:195-196` 有意把"禁用"换成"不给这一项"，
   注释写了理由（"菜单里点不动的行只会让人以为坏了"）。这是我们的决定，不是缺口。
6. **不要动 `services/i18n.ts`（1453 行）与 `fixtures.ts`（1286 行）来配合本方案**。
   它们是 D3（`services/` 三分）的活，混进来会让本方案的每一步都不再能单独验收。
7. **不要抽 `ui/` 之外的第二套 DI**：他们靠"参数注入时钟/Storage"（
   `searchHistory.ts` 的 `StorageLike`、`cardDblclickGuard.ts:94-108` 的 `nowFn/sleepFn`）就够了。
   别引入 container/symbol 那套，我们只有十几个纯模块。

---

## 五、我没查什么、为什么

1. **没跑任何测试、构建、安装命令。** 任务约束是只读。
   所以 vitest 3.x 与 vite `6.4.5`（实测于 `package-lock.json:1479-1482`）的**兼容性未核实**，
   `npmmirror` 能否装下 vitest 也**未核实**。P0 第一步就是把它装下来跑一个 `dramaRef.test.ts`。
2. **没读他们的 `main/`（Electron 主进程）与 `preload/` 的具体实现**，只 `ls` + 读了
   `shared/clipboard.ts` 这类契约文件。原因：结论（N1/N2）取决于"Wails vs Electron 进程模型"，
   不取决于他们主进程写得好不好；读了也不会改变"不搬"这条。
3. **没读 `packages/sync-client`、`sync-protocol`，没读 `apps/{android,mobile,tv}`。**
   我们只有一个客户端、本轮也没有跨端同步需求；读了只是满足好奇心。
4. **没读他们的 `e2e/specs` 45 个文件的内容**，只数了数量并读了 `package.json` 的
   `test:e2e:smoke/full --grep @smoke/@full`。我没有据此提"分级 e2e"方案，
   因为我们的 30 只脚本已经在 `run_all.py` 里统一跑，是否值得分 smoke/full
   取决于脚本时长 —— **那个我没测**。若 lead 想知道，量法是 `run_all.py` 的每只耗时。
5. **没有实测 Q4-⑦ 的双击重排竞态**（在 `store.merges` 轮询替换时两击跨行）。
   要复现得构造时序，属动手范畴。方案里它被降级为"可选加做"，不作为验收口径。
6. **没逐一核对 6 份 `.toast` scoped 样式是否真的等价**（只确认了 6 处定义与 z-index 60/70 那组冲突）。
   P3/Q3-④ 合并成共享 `feedback.css` 前需要肉眼比一次截图 —— 这是**该阶段的前置动作**，不是本文的结论。
7. **没读 `.trellis/spec/` 与 `.trellis/tasks/`**。本仓由 Trellis 管理（`AGENTS.md`），
   落地时 P0 之前应先跑 `trellis-before-dev` 取包级规范；我没有替方案预设规范内容。
8. **没查 Go 侧**（`client-v2/internal/**`，Go 单测声明 246 个，实测于 `research/SESSION-STATE.md`）。
   本方案只覆盖前端 UI/架构；Go 侧已有测试文化，不是缺口所在。

---

## 六、一页执行表

| 阶段 | 动哪些文件 | 新增 | 验收口径 | 回滚 | gate |
|---|---|---|---|---|---|
| **P0** vitest 骨架 | `package.json`(+2 script/devDep)、`.github/workflows/build-v2.yml:171` 前插 `npm run test` | `vitest.config.ts`（照抄他们 8 行）、`services/assetUrl.ts`+测、`services/dramaRef.test.ts`、`services/updatenotes.test.ts`、`stores/downloads.test.ts`；4 个视图各改 1 行 import（`DetailView.vue:512-516`、`ReserveView.vue:154-157`、`CalendarView.vue:50-53`、`CreatorView.vue:51-55` 删掉） | `npm run test` 绿；`npm run typecheck` 绿；`run_all` 30/30；`grep -rn "function coverUrl" src/views` = 0 | revert 单个 commit（未动模板/类名） | 无 |
| **P1** `src/ui/` 纯模块 | 各视图改 import 行 + 删内联实现 | `ui/{listPagination,escLayers,playerShortcuts,suggestKeyboard,gestureClickGuard,toastScheduler,clipboard,contextMenuModel,themeModel}.ts` + 同名测；**可选** `features/downloads/{format,selection,filters}.ts`（DownloadsView 引用数 0，可在此阶段做） | 同上 + 新纯模块合计 ≥60 个 `it`；模板/类名零 diff | 每个模块一个 commit，逐个 revert | 无 |
| **P2** Esc 单点分发 | `services/overlays.ts`（删 `:37-61` 记账）、`services/contextMenu.ts`（删 `:254-256`）、`views/DetailView.vue:488-498`、`views/PlayerView.vue:199-210`、`App.vue` 或 `main.ts` 加唯一分发点 | `ui/escLayers.test.ts` 覆盖链 | **`esc_semantics_verify.py` ①-⑤ 全绿**（契约现成）+ `run_all` 30/30 | revert 即回到记账机制（P1 成果不受影响） | 依赖 P1-A3 |
| **P3** z-index 阶梯 + 样式审计测 | `styles/layers.css`（新）、`services/contextMenu.ts:22`（改值+改注释）、`PlayerView.vue:656/922`、`HotkeyHelp.vue:57`、`SourceGrid.vue:337/348`、`SoraniLoginDialog.vue:203`、`LibraryView.vue:710/714/718`、`DetailView.vue:714` | `ui/tokensAudit.test.ts`、`ui/zIndexAudit.test.ts`、`styles/feedback.css` | 测绿 + `run_all` 30/30 + 6 份 `.toast` 合并后逐页截图比 | 数值类单独 commit（与测分开） | 依赖 P0（要有 CI 测位） |
| **P4** confirmDialog + 破坏性接入 | `ui/confirmDialog.ts`+测、`DownloadsView.vue:728/835/964`、`HistoryView.vue:94`、`verification/merge_multi_verify.py`、`history_local_play_verify.py` | 同上 | 新测覆盖 FIFO/焦点陷阱/遮罩取消 + **改过的** verify 脚本绿 + `run_all` 30/30 | 视图接入点与模块分开 commit：只 revert 接入点=功能不生效但代码留着（可加开关） | 依赖 P1-A7、P2（Esc 链要认 confirm） |
| **P5** `views/ → features/` | 其余 10 个视图 + `research/*.md` 里 189 处 `文件:行号` 重指向 | — | `run_all` 30/30 + 引用审计脚本重跑为 0 悬挂 | 按视图分 commit | **等 D2**（`v1-final` tag 落定后再做） |

---

## 七、附：本文所有"实测数字"的量法（可复跑）

lead 或后续任何人想复核一个数字，直接跑对应那条。**全部是只读命令。**
（我没跑任何测试/构建/git 写操作 —— 任务约束。）

| 文中数字 | 量法 |
|---|---|
| 我们 `.test.ts`/`.spec.ts` = 0 | `find client-v2/frontend/src \( -name '*.test.ts' -o -name '*.spec.ts' \) \| wc -l` |
| 他们 renderer `.vue` = 0、`.ts` = 155 | `find <ref>/apps/desktop/src/renderer -name '*.vue' \| wc -l` / 同 `-name '*.ts'` |
| 他们测试体量 3800/9685、5400/6111 | `find <ref>/apps/desktop/src/renderer -name '*.test.ts' -exec wc -l {} + \| tail -1`，再对 `! -name '*.test.ts'` 跑一遍；`shared/` 同理 |
| vitest 未安装 | `ls -d client-v2/frontend/node_modules/vitest` → 不存在 |
| vite 版本 6.4.5 | `grep -A2 '"node_modules/vite"' client-v2/frontend/package-lock.json`（`:1479-1482`） |
| CI 前端只有 install+build | `.github/workflows/build-v2.yml:160-171` 直接看 |
| `tokens.css` dark/light 各 94、差集 0 | 复刻他们 `styles/tokensParity.test.ts:60-86` 的 `extractBlock`/`declaredVars` 两个函数，用 node 跑：`node -e` 读 `src/styles/tokens.css`，选择器分别取 `:root,` 与 `[data-theme="light"]`（**注意我们的属性选择器用双引号**，他们源码里是单引号，抄函数时要改） |
| 未定义 `var(--x)` 引用 = 0 | 遍历 `src/**.{vue,ts,css}` 抓 `/var\(\s*(--[\w-]+)\s*(,)?/g`，与 tokens.css+base.css 的**已定义集合**取差。唯一命中是 `styles/tokens.css:4` 的注释文字"只用 var(--token)"，非真引用 |
| `.vue` 的 `<style>` 块内 16 处字面色 | 同样的遍历，只在 `<style>`…`</style>` 区间内匹配 `/#[0-9a-fA-F]{3,8}\b\|rgba?\(/g`，且先 `replace(/var\([^)]*\)/g,'')` 去掉 token 引用 |
| z-index 22 处、值域 1/2/3/30/60/70/79/80/1000 | `grep -rn 'z-index\|zIndex' client-v2/frontend/src` |
| `research/*.md` 引用面 370 / 189 / 30 | `grep -rhoE '[a-z_]+\.py:[0-9]+(-[0-9]+)?' research/*.md \| sort -u \| wc -l`（v1）；把式子换成 `[A-Za-z0-9_]+\.(vue\|ts):[0-9]+(-[0-9]+)?`（我们，189）与 `[a-z_]+_verify\.py:[0-9]+`（脚本，30）。分文件密度：后接 `\| awk -F: '{print $1}' \| sort \| uniq -c \| sort -rn` |
| 每只 verify 脚本用到的键位 | `grep -rhoE 'keyboard\.press\([^)]*\)' client-v2/frontend/verification/*_verify.py \| sort \| uniq -c` → Escape 13 / ArrowDown 5 / Enter 4 / ? 4 … |
| Playwright 选择器依赖面（`.toast` 20 等） | `grep -rhoE "(['"])[^'"]*(toast\|context-menu\|zgj-ctx\|confirm)[^'"]*\1" verification/*_verify.py \| sort \| uniq -c` |
| 30 只验收脚本 | `ls client-v2/frontend/verification/*_verify.py \| wc -l` |
| `showToast` 6 份 / `coverUrl` 4 份 | `grep -rn 'function showToast\|function coverUrl' client-v2/frontend/src` |

两处**要自己重跑才有意义**的提醒：
- 抄 `tokensParity.test.ts` 那两个辅助函数时必须改选择器引号：他们源码里是 `[data-theme='light']`（单引号），我们是 `[data-theme="light"]`（双引号）。引号不改 → `indexOf` 返回 -1 → `extractBlock` 返回空串 → 变量数读到 0 而**不报错**，这是会静默给出假数字的量法坑。
- z-index 那 22 处里 `services/contextMenu.ts` 是**模板字符串**里的 `${Z_INDEX}`（`:22` 定义、`:221` 使用），
  按 CSS 字面量 grep 会漏掉它。
