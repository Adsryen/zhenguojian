# 还差什么（第五十九轮总账）

盘点方法：不看我自己写过的叙述，逐条回仓库核。
分四类：**要你拍板的** / **只能真机验的** / **子代理正在查的** / **清单本身的过期项**。

---

## 一、需要你拍板的（7 项，每项给两个选项与依据）

### 1. 文件级删除绑定（R22b-2）
v1 归档区**按文件**成行，每行有「删除」（`downloads.py:661`，tooltip
「删除该文件（加密缓存保留，可重新解密）」）与合并成品的「删除合并成品」
（`downloads.py:1641-1652`，含确认框、提示「删除后需要重新合并才能找回」）。
v2 归档页**按剧分组**，没有文件级删除绑定。

- A：补 `App.DeleteDownloadFile(path)`，边界照 `PlayerOpenLocal` 已有的
  「只允许下载根之下 + 只取 basename 的守卫」那条链（已实现、已测）。
- B：先不做，等真机看过归档页表现再定。
需要你说的是：删合并成品要不要一起、要不要确认框（v1 有）。

### 2. 「删除该剧」入口位置（R22b-3）
v1 在左侧剧列表 + 按钮（`downloads.py:1195`）。v2 队列页是平铺 + 筛选，
**能力已在**（`removeBatch`），只缺一个入口该放哪。

### 3. 三个初始骨架预留绑定（R24）
`evidence_audit` 现算出的前端零引用绑定：`BrowseFiltered`（前端只用 `BrowseFilters`）、
`MergeList`（注释自写「同 DownloadList.merges」，被取代）、
`TrayStatus`（注释说「冒烟自检用」，但 `internal/selftest` 里没有一步用它 ——
注释与实现不一致）。
选项：删 / 接上 / 保留并注明"预留、当前无调用方"。

### 4. 十七个 i18n 真死键（R25）
`i18n_key_audit` 现算：613/613 键、字面量用到 564、动态取键 12 处，
"未用 49"拆三档后**真死候选 17 个**（其中 4 个是 `tray.*`，见第 3 项同源）。
选项：删 / 其中 `tray.*` 接上托盘本地化。
**注意**：托盘菜单文本目前硬编码中文，而 v1 同样硬编码（`main.py:739-742`
`QAction("显示主窗口")`/`QAction("退出")`）—— 所以这不是 v2 落后 v1，
接本地化是**新需求**。

### 5. v2 从不归还核心会话（R26）
`engine.go:148-150` 会自动给每个非豁免动作补 `session = owner + ":" + action`，
但 v2 生产代码里 `release` / `cancelRead` **零调用点**；
v1 真在用（`core_client.py:499/530`，调用方 6 处，其中
`library.py:483` 就在弹幕保存的 finally 里 —— 就是我移植的那条链）。
本机无法判定后果（乐观：同 session 的更早读会被核心取消，占用有界；
悲观：核心按 session 持句柄，长跑累积）。
- A：照 v1 补 `native.Release(session)`，在同样 5 个位置调用、错误吞掉（v1 是 `cleanup only`）。
- B：先真机观察长跑表现，再定。
我没直接动手，因为"在没跑过真实核心的机器上往拆除路径加清理调用"，
属于看起来对齐、实际可能引入新 bug 那一类。

### 6. 设计 token 的配色决定（本轮新登记）
`palette_audit` 现算：规矩 1–4 全 PASS，但有 **42 个定义了没人用的 token**，
包括 `--accent-v1` 整族（v1 的品牌红 #ff3b5c/#fe2c55，值都对得上、原样保留着）
和 `--guo-*` 一部分。v2 界面实际用的是 guoapp 蓝（`--accent` 55 处、`--accent-soft` 11 处）。
- A：保留红色族作为"v1 对照参考"，注明不接线；
- B：删掉，界面只留一套语义色。
（另：`--danger-soft`、`--guo-surface2/3`、`--guo-font-mono`、`--text-muted`
是被引用但没定义、**写了回退值**的 —— 静默降级不是 bug，
但退配色散在组件里，该不该收进 tokens.css 也是个决定。子代理 C 正在数影响面。）

### 7. commit-msg 钩子（R33）
我在第 43/54/57/59 轮共四次用 `git commit -m "...反引号..."`，shell 吃掉内容
（第 57 轮那次把 `test_sorani_login.py` 这个证据来源文件名从提交信息里删掉了）。
装个钩子就能机械拒绝，但它动的是你机器上的 `.git/hooks`，我不擅自装。

---

## 二、只能真机验的（55 条，别把 CI 绿当这些的替代）

`research/v2-windows-followups.md` 共 55 个待打勾条目，其中第 14 节是
**机器生成**的名单（`core_action_audit` 算出：生产 26 个核心动作、
假核心覆盖 11 个，**剩下 15 个只有真实核心才会第一次执行**：
browseFiltered / browseFilters / calendar / catalog / creatorProfile /
creatorWorks / danmaku / detail / dramaCreators / initialize / listSources /
preload / setSourceEnabled / soraniAccount / suggestions）。

优先级最高的三条：
1. `client-v2.exe --selftest` 首跑（`--selftest` 那 9 步里 3 步一直是 PENDING，含 decrypt）；
2. 本地成品播放链（`.ass` 挂载、`release` 归还，见第 5 项）；
3. 按季合并选中（SxxExx 命名 + 归档归组，第四十七、四十八轮补过，没在真机核过）。

---

## 三、子代理正在查的（4 路，只读、各写一份新文档，不撞车）

| 包 | 范围 | 交付 |
|---|---|---|
| A | 搜索 / 找剧 / 剧库 三面 | **已交** `research/v1-v2-discover-search-parity.md` |
| B | 播放器 + 弹幕 | **已交** `research/v1-v2-player-parity.md` |
| C | 创作者页 + 未定义 token 影响面 | **已交** `research/v1-v2-creator-and-tokens.md` |
| D | 验收装置体检 | **已交** `research/verification-weak-points.md` |

对账规矩沿用前八面：每条带 `文件:行号`；**不许拿 v1 注释当规格**
（本任务已两次因此写出假差异）；差异分"可直接修 / 需决定 / 平台不适用"三类。

---

## 四、清单本身的过期项（本轮核掉，别再当缺口汇报）

PRD 里三条 `[ ]` 是**过期勾选**而不是未做：

- **R4 弹幕透明度**：三层都有实物 —— Go 侧 `internal/danmaku/ass.go:33,51-56`
  （含 0.05–1.0 钳位与 ASS alpha 反向）、i18n `player.danmakuOpacity*` 中英文成对、
  验收 `player_verify.py` 有断言。**只剩真机观感复验**（已在 followups 第 12 节登记）。
- **R6 SetNextSeason**：理由写的是「`grep SetNextSeason client-v2` 为空」，
  现在它出现在 `bindings_watchstore.go`、`services/api.ts:295`、
  `services/nextSeason.ts`、`bindings_watchstore_test.go` —— 早做完并测过了。
- **R9 i18n 静态棘轮**：理由写「现 32 处」，实跑是
  `界面中文 0 处 / 基线 0 处`（BASELINE 已是空表，新增任何一处直接红）。

这条记进方法论：**"还差什么"必须先核清单自己的准确性**，
否则我会把已完成的事继续报成缺口 —— 那和把未做的事报成完成是同一类错，
只是方向相反。

---

## 五、我这轮已经做完并验掉的

- `CreatorView.vue` 引用两个未定义 CSS 变量（`--line`×4、`--surface-1`×1，无回退）
  → 按钮与卡片**一直没描边**、卡片**底色透明**，而控制台/构建全程干净。
  改成用既有 `--border` / `--surface`（不是加别名：全仓 42:4 与 39:1 的引用比例说明
  这是 CreatorView 造的漂移名，v1 里也根本没有这两个概念）。
- `creator_verify.py` 加 T1/T2 读**计算值**（名字定义了 ≠ 声明生效，只有计算样式能证明）。
  反向验证实测修前是 `w:'0px'`、`bg:'rgba(0,0,0,0)'` → 断言确实有牙齿。
- `palette_audit.py`（按 v1 `test_palette.py` 四条规矩 + 引用面分档 + 死 token 名单，
  自检 21/21）。**明确不接成 run_all 硬门**：这几轮我加了五道审计，
  每加一道就先要修审计自己的 bug，节奏被工具拖着走 —— 它当"改 tokens.css 时手动跑"用。
- 提交信息里我先前写「自检 21/21」而实际 20/21（补丁 assert 提前失败没落地）：
  已改完代码、重跑验证、`--amend` 修正，现在那句是实测值。

全量 `run_all` 29/29、失败 0、端口泄漏 0；`creator_verify` 23 条 ALL PASS。


---

## 六、四路全部回来后（第五十九轮收尾）

### 已经修掉的（都做过反向验证）

| # | 问题 | 严重性 | 提交 |
|---|---|---|---|
| 1 | `danmakuAll` 少发 `durationMs`，核心入口直接拒绝；而我的测试**禁止**该字段 | 真机必挂 + CI 只会绿 | `a9b7273` |
| 2 | 浅色主题三处"引用未定义 token + 深色回退值"，季浮层 1.10:1 | 文字几乎不可见 | `8c9ca4d` |
| 3 | i18n 键对账对**连字符键**隐形（`source.note.huangguo-video` 删英文也绿） | 硬门有形状盲区 | `b2aa55c` |
| 4 | run_all 的"残留端口"列对 52xx 恒空集 | 一列假绿 | `b2aa55c` |
| 5 | 搜索页是三个列表页里唯一不标"示例数据/降级"的；且不读核心 warning | 假数据当真结果 | `b2aa55c` |

### 队列里剩下的（按"能骗过多久的绿"排序，均属可直接修）

1. `player_verify:162` 用 `>=` 判恢复播放（时间停住也绿）、`:308-316` 的钳位断言打在
   浏览器自钳上（store 钳位整删仍绿，注释与代码不符）；
2. `sorani` M13 的点位上限输入不可达（删钳位仍绿）、M21b 的 `[340,350]` 区间仍包含坏值
   —— 与第五十七轮我修掉的 M21 同族，**姊妹条没修**；
3. `evidence_audit` 的"有 Go 单测"判据是全文 `\bName\b`，会命中结构体字段
   （`App.Cover` 实际是被 `store_test.go` 里的 `e.Drama.Cover` 蹭到）→ "27 个桥接+Go单测"有水分；
4. `hitbands_verify:64-70` 的 oracle 与 PlayerView 同公式（公式错两边同错）；
5. `i18n_sweep:262-264` 整页 en 硬断言的"数据可解释"判据只 ≥1 字子串
   （注释说 ≥3 字段，代码不是）；
6. `detail_download:184` 判 `index>=1`（恒送末集也绿）、`downloads_archive ⑥b`
   成功/失败 toast 都算过；
7. `detail_reserve ⑦b` 注释承诺"改间隔不改文案会红"是**错的**（30 在 i18n 字面量里）；
8. 约 20/30 只脚本没有 pageerror 守卫，`player_verify:107` 甚至把 console 监听写成空操作。

### CI 结构性事实（这条改变我过去几轮的说法）

`build-v2.yml` 只在 `workflow_dispatch` 与 tag `v2.*` 触发 ——
**普通 push 什么都不跑**；且 Frontend 步骤只 `vite build`，仓里存在的 `typecheck` 脚本无人调用。
所以我说"CI 绿"时，指的是**我手动 dispatch 的那几次**，不是每次提交都有门在守。
这条我必须讲明，别让"CI"两个字显得比实际更强。
30 只 Playwright 与 i18n_static_scan / palette_audit / exemption_probe 都没接进 CI（只在本机 run_all 里）。

### 新增待决（子代理 A 报的 (b) 类，12 条里最重的三条）

- **找剧多维筛选面板整体未移植**：v1 有 9 维服务器面板 + 摘要/重置/缓存
  （browse_panel.py:311-416），v2 只有一排芯片；
  Go 侧 `App.BrowseFiltered` 绑定**已存在**（app.go:248-260）而前端 0 处调用
  —— 与 R24 那条"预留绑定"是同一件事的两面。
- **剧库「全部」tab 跟随站源 vs v1 恒红果**：v2 自己的 Go 壳（library.go:74-85）
  反而保留着 v1 语义，是前端绕开了它（LibraryView.vue:145/400-410）。
- **剧库「换一批」未移植，且理由写错**：LibraryView.vue:13 称 v1"本页无此入口"，
  实际入口就是 `main.py:1115-1117` 的「刷新推荐」按钮 ——
  这次是 **v2 注释曲解 v1**（我这几轮一直在抓的方向反过来又出现一次）。


---

## 七、发版前本机试跑（第五十九轮续）

### 工具链实情（先说结论，别说半截话）

- 本机**没有 Go**，也没有 pkg-config/GTK/webkit → **Linux GUI 版本机编不出来**，
  而且那本来不是发货物（发版目标是 Windows exe）。
- 我在 `~/.local/go` 装了用户级 Go 1.24.6：`go list ./internal/...` 通、依赖拉得动，
  但**连 hello world 都编不过**，失败在 stdlib 汇编
  （`internal/runtime/syscall/linux/asm_linux_amd64.s:27
  ABI selector only permitted when compiling runtime`）。
- 判定链已查清（`cmd/asm/internal/asm/parse.go:893` 的 `p.allowABI` ←
  `objabi.LookupPkgSpecial(...).AllowAsmABI`，表里只有父包没有 `.../linux` 子包），
  但**原因未查明**。我一度断言"镜像给的文件不自洽"，随后用两个独立来源
  各下一次、sha256 完全相同 → 那个指控是错的，已 `--amend` 更正
  （这与我这几轮一直抓的"把对自己现象的解释当成已验证事实"是同一个错）。
- 下一轮的具体实验：换 Go 版本（1.23 / 1.25）或装发行版 `golang-go`，看现象是否消失。
  在此之前，**Go 侧的唯一门仍是 CI**。

### 本轮改掉三条 v1 差距

| 条目 | v1 依据 | v2 之前的实情 | 证据 |
|---|---|---|---|
| 播完那一刻落 finished | `main.py:2511-2522` | 只有 `shutdown()` 才算终态 → 连播时**每一集的「已看」都不落** | ⑥a2 正反都验（撤掉后红在 `{ep:1, finished:false}`，正是 bug 形状） |
| 倍速持久化 | QSettings 变更即存 + `player.py:1668` 每次新会话显式应用 | 完全不存；而 `settings_storage_verify.py` 表里写着「倍速 ✅ 有」，**一条断言都没有** | ⑦s/⑦s2/⑦s3 |
| 找剧翻页去重 + 连续 2 页才停 | `poster_wall.py:650-662` + `browse_panel.py:402-403` | 裸 concat；注释把 PosterGrid 的**渲染 :key** 当成内容去重 | 改了，但**没钉住**（见下） |

顺带修的两个 mock 洞：`api.ts` 的 mock `saveWatch` 丢掉第 5 个实参；
而 mock 环境实际走的是**本地层**（`watch.ts:273 useLocalLayer()`），
`localSaveWatch` 连 `finished` 参数位都没有 —— 我第一版把观测缝打在
`api.saveWatch` 上，那是死路径，断言读出 null 才追到这里。

### 新登记两条（都是"我没测到"而不是"我测过了"）

- **G-a 找剧去重缺一个数据层可观察量**：我为它写的两版判据都倒在错前提上
  —— ① 判"标题互不相同"（fixtures 本来有同名剧）；② 判"DOM 卡片数不变"
  （`PosterGrid` 是虚拟渲染，实测滚一下 21→15，那测的是窗口不是数据）；
  ③ 用新加的 catalog 调用计数一验，**7→7**：我打的滚动根本没满足
  `PosterGrid.vue:153-158` 的 emit 条件，"卡数没变"其实是"什么都没发生"。
  要钉住它，正好需要先补上子代理 A 报的另一条 (a) 类差距：
  **v1 找剧页有「已加载 N 部·…」状态行，v2 没有**。补上那行才能断言 N 不涨。
  断言已撤回，不留一条"看起来过了、其实什么都没看"的绿。
- **G-b `discover_hero_verify.py` A2 抖动**：本轮三次运行红两次
  （失败时 URL 停在 `/#/discover`），它在本人改动之前就执行、与改动无关，
  判为用例自身时序抖动。**不接受"重跑就好"**，下一轮给它加稳定等待条件。

### 门禁（现算）

全量 `run_all` **30/30、失败 0、端口泄漏 0**；`player_verify` 33 项 ALL PASS；
`discover_hero_verify` 回到 15/15；typecheck 0、build ✓；累计 **156 笔**。
