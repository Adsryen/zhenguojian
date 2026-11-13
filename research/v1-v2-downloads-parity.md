# v1 ↔ v2 下载面板对账（第三十三轮）

方法同 `v1-v2-shortcut-parity.md` / `v1-v2-settings-parity.md`：
先把 v1 侧**逐行抄全**（`zhenguojian/app/zhenguojian/downloads.py`），再和 v2
（`client-v2/frontend/src/views/DownloadsView.vue` + `stores/downloads.ts` + Go 侧）比。
每条都带文件+行号，下一个人可以自己核，不用信我的转述。

## 行内动作（按任务状态）

v1 侧是硬映射（`downloads.py:52-58`）：

```python
ROW_ACTIONS = {
    "downloading": ("pause", "remove"),
    "queued":      ("pause", "remove"),
    "paused":      ("resume", "remove"),
    "completed":   ("play", "folder", "remove"),
    "failed":      ("retry", "remove"),
}
ACTION_TEXT = {"pause": "暂停", "resume": "继续", "play": "播放",
               "retry": "重试", "folder": "打开目录", "remove": "删除"}
ALWAYS_ON_ICONS = {"failed": ("retry",)}   # 失败行的「重试」常驻提亮
```

| v1 动作 | v2 对应（行号） | 状态 |
|---|---|---|
| 暂停 / 继续 / 删除 / 播放 / 打开目录 | `DownloadsView.vue:641`（`row-open`）等行内按钮组 | ✅ 齐 |
| 重试（失败行） | `:667 store.retry(job)`；`stores/downloads.ts:258` 注释写明 **v1 语义 = 核心没有独立 retry，重试即 resume** | ✅ 语义一致；键名本轮从 `search.retry` 改成 `downloads.retry`（文案不变，见下） |
| 状态文案 6 个（排队中/下载中/已暂停/已完成/失败/删除中） | `downloads.state.*`（含 merging、canceled） | ✅ v2 更全 |

## 面板级动作

| v1（`downloads.py` 行号） | v2（`DownloadsView.vue` 行号） | 状态 |
|---|---|---|
| 打开下载目录 `:1084` | `statbar.openFolder` `:455` 一带 | ✅ |
| 全部暂停 `:1090` / 全部继续 `:1092` | `:455 / :456 controlAll('pauseAll'\|'resumeAll')` | ✅ |
| 一键合并整季 `:1207` | `:577 downloads.mergeSeason`（按剧组） | ✅ |
| 合并选中分集 `:1213` | `:585 downloads.mergeSelected` | ✅ |
| **按季合并选中 `:1219`（勾选左侧多部剧，按季序逐季各出一个成品）** | ✅ **已贯通**（Go `MergeStartMulti`，提交 `4a32dfa`/`b0ef1c3`，CI `43323235422` ✓；界面 `cdbd1ad`，`merge_multi_verify` @5210 13 条 ALL PASS） | 🟡→✅ **能力补齐、界面也接上了**。原本缺的原因是 `MergeStart` 两种 mode 都锁死单剧：`season` 要单个 `dramaID`（空就报「缺少剧集信息」），`selected` 更是逐条比对剧号、混合直接拒绝：`else if jobDramaID(job) != dramaID { return nil, errors.New("请选择同一部剧的分集合并") }` —— v2 界面上那句报错就是能力缺失的可见症状。新开一条绑定而不是加第三种 mode，理由写在 `internal/downloadsvc/multi.go` 与 `bindings_download.go` 注释里 |
| **删除该剧 `:1195`**（danger；tooltip「移除左侧选中剧集的全部下载任务和已保存分集」） | 无该按钮 | ❌ **缺按钮，但能力已在**：`stores/downloads.ts:267 removeBatch(ids)` + `selectAll` 现成。缺的是"按剧一次性选中并删除"的**入口位置**——v1 有左侧剧列表，v2 队列页是平铺列表 + 筛选，这个按钮该放哪是 UI 决定，不猜 |
| 停止全部合并 | `:694 store.mergeCancel('')` | ✅ v2 多出 |
| 下载位置…（选择文件夹 / 恢复默认 / 取消 / 确定 `:335-357`） | 无 | ⚠ 刻意不做，理由与证据见 `v1-v2-settings-parity.md` 第 1 条（v2 下载根由核心决定，客户端无写入口） |
| 并发槽位 | `:468 concurrencyLabel` + `:479 save` | ✅（v2 还多 `:474 verifyDecode` 解码校验开关） |

## 归档（成品文件）页签

| v1 | v2 | 状态 |
|---|---|---|
| 组行「打开」`:726` | `:776 row-open`（+ 复制路径、重新扫描） | ✅ |
| 归档文件「播放」`:655`（`playRequested.emit(target)`，直接播这个路径） | ✅ 归档组「播放」按钮（第四十六轮，仅当 `openPath` 是合并产物文件时出现） | 已接：走 `App.PlayerOpenLocal`；v1 按文件成行、v2 按剧分组，所以分集目录那两组**故意不给按钮**（给了就是死按钮），这条差异与断言都在 `downloads_archive_scan_verify` ⑧a。**原行的其余说明保留如下** —— 原本缺的原因是 **归档文件「删除」`:661`**（tooltip「删除该文件（加密缓存保留，可重新解密）」） | 无 | ❌ **缺**，需要 Go 绑定：现有只有按任务号的 `DownloadRemoveBatch`（`bindings_download.go:133`），没有文件级删除；而且这个绑定**必须自带路径约束**（只允许删下载根之下），否则等于给前端一个删任意文件的口子——这条安全边界要在实现时定，不能事后补 |

## 本轮做完的 / 待决定的

- ✅ 行内动作与状态文案全部对得上（含"重试 = resume"这条容易被误当 bug 的语义）
- ✅ `downloads.retry` 键独立出来：之前借用 `search.retry`，文案一致但**键名撒谎**
  （下载行的按钮挂在下拉菜单的键上），改名字不改文字
- ❌ 四个缺口按性质分两类，都在等决定而不是等我猜：
  - 需要 Go（跨季批量合并 / 归档播放 / 归档删除文件）——三个都要新增绑定或改 `MergeStart` 入参形状；
    CI 验证路径已经打通（`workflow_dispatch`，不发 Release），但**做成什么样要先定**
    （尤其归档删除的安全边界与"播放本地文件"要不要进播放器 UI）
  - 只需要 UI 决定（「删除该剧」放哪）——v2 队列页没有 v1 那个左侧剧列表

## 第四十三轮：按季合并选中的 Go 侧已落地（界面差一步）

`internal/downloadsvc/multi.go` + `App.MergeStartMulti`。规则逐条照 v1 `_multi_targets`
（downloads.py:1940-1962）：只取 `completed`、每部不足 2 集**整部丢掉**、按季号稳定升序、
命名 `<去季号安全名> S%02dE%02d.mkv`（E 取该季最小集号）、同名 base 加 ` (2)`、
目标不足两部整批不发起、**正在合并的那一季跳过而不是报错**
（v1 `_start_batch(skip_running=True)`；这跟单剧 `MergeStart` 撞到运行中直接报错不一样，
是 v1 本来就有的差别，不是我给它分的特权）。

为什么新开一条绑定而不是加第三种 mode：加 mode 要把返回从"一条排队"变成"多条排队"，
已有两个调用点和它们的断言会一起变形。

CI `43323235422` 两 job ✓（8 组新测试）。本轮唯一一次红是**我的期望写错**：
`strip_season(" 某剧 - 第3季 ")` 我期望 `某剧 -`，实得 `某剧`——
v1 第一刀是 `.strip(" 　-·~～")`，两端对称的字符集；`第3季` 被剥掉后
那根连字符已经在**结尾**，所以照样被吃掉。我把两刀在脑子里合并成了一刀。
已补三条用例钉住（结尾的吃 / 紧贴的吃 / 中间的留）。

### 落点已按这个打算做完（第四十四轮，提交 `cdbd1ad`）

v1 靠左侧剧列表的复选框；v2 下载页没有那个列表，但**「合并」页签已经按剧分组**
（每行一颗「一键合并整季」）。所以给那些分组行加复选、
选中≥2 时出现「按季合并选中（N 部）」，是 v1 那套选择在最接近的现有结构上的落点，
不是新造一个页面。

实际做完的样子（`DownloadsView.vue` 的「合并入口」那一行）：
每颗分组 chip 前面挂一个复选（`data-testid=merge-group-check`），
勾选 ≥2 才出现 `merge-multi` 那颗，标签「按季合并选中（N 部）」与 tooltip
逐字照 v1（只把「左侧」去掉——v2 没有那个列表，留着就是指一个不存在的东西）；
排完清空勾选，批次按钮随之消失。
错误话术按 Go 的 `[multi_none] / [multi_targets] / [multi_running]` 码取 i18n，
**中文句子只进日志**，所以英文界面不会看到中文（第 42 轮 ⑦b 立的规矩）。

mock 面刻意**不复刻季序与命名**（那是第二个真相，Go 的规则表已有单测），
所以 `merge_multi_verify` 只断言"两部剧各有一条在排的合并"和码名一致，
不断言输出文件名。`multi_targets` 门槛在 mock 里走不到
（内置示例态恰好两部可合并，与"勾 ≥2 才出按钮"互斥），
这条边界写在脚本 docstring 与运行时 NOTE 里，没有为了凑一条绿去给产品临时加注入缝。

## 第四十五轮：更正我自己写进上一节的一条假差异

我上一轮写「v1 多季批次完成后不自动播放，这条差异没实现」——
那是**照抄 v1 docstring 的一句话、没去核它的代码**。
v1 `start_merge_multi` 的 docstring 确实有那句
"never auto-plays when it finishes (reserved for single-season merges)"，
但 `grep -rnE "auto_play|autoplay|自动播放"` 在整个 v1 客户端只命中一句无关注释
（main.py:2545「不跨季自动播放」），**没有任何"合并完成后自动播放"的实现**：
`_merge_one_done`（2565-2577）与 `_finish_merge_batch`（2586-2605）都只写文本。
所以那条差异不存在，v2 不用补旗标。

v1 真有、v2 当时缺的是另一件事：**双击合并队列里已完成的条目 → 播放那个成品**
（`downloads.py:1139` itemDoubleClicked → `_open_merge_entry` 1495-1498 →
`_play_merged_path` 1631-1640，带 0.8s 去抖，文件不在提示
「成品文件不存在：<文件名>」）。这条现在接上了：v2 走上一轮补好的 `PlayerOpenLocal`，
`multi.go` 与代码注释里也已把这条更正记进去。


## 第四十七轮：复核发现我上一轮的命名**没落到磁盘**，并顺带修掉它的下游影响

追这条线的时候发现两件必须记的事：

1. **命名只在排队结构里算过，从没写到文件上。**
   `MergeStartMulti` 把名字塞进了 `MergeEntry.Output`，
   而 `runMerge` 的保存那一步（merge.go:195）从来不读它，自己按
   `"剧名 全NNN-NNN集.mkv"` 拼名，跑完再把 Output 覆盖成真实路径。
   所以磁盘上一直是默认名，而我的测试还全绿 ——
   因为我测的是 `buildMultiTargets`（那个纯函数），**没测最终文件名**。
   两个错叠在一起：字段用错（`Output` 的语义是"已落盘绝对路径"，
   排队阶段塞名字会让 UI 的 done 分支显示一个不存在的路径）+ 测试停在计算层。
   修法：新增 `MergeEntry.OutputName`（持久化，含恢复），
   `runMerge` 读它并照 v1 `media.py:1033-1038` 只取 basename
   （名字是从剧名拼的，标题不能把成品写到 `下载/合并/` 之外；洗完为空按 v1 报错，
   不悄悄退回默认名）。
   新增三条测试：跑完整假管线**看磁盘上那个文件叫什么**、basename 守卫四种输入、
   排队阶段 `Output` 必须为空而 `OutputName` 必须有值。
   另外撤掉一条**把 bug 当规格钉住**的旧断言（它要求 `Output == "某剧 S01E01.mkv"`）——
   那种断言最危险：下一个人会去改实现对齐测试。

2. **接通命名后暴露的下游问题**：v2 归档扫描原来只认
   `mergeNameInfix = " 全"` 这一个中缀（因为过去磁盘上只有那一种名字），
   所以 `剧名 S01E01.mkv` 会自成一组 `m:剧名 S01E01`，
   剧名组反而丢掉「含合并」徽标 —— 同一个成品文件在 v1/v2 归档页归属不同。
   改成照 v1 `_archive_drama_key`（downloads.py:89-95）剥三种形状再去季号。
   核 v1 时看清两处细节：
   - v1 的 `strip_season` 用 `sub`（**不锚结尾**），所以「第二季 测试剧」也要剥；
   - v1 那句 `strip_season(stem) or stem or "合集"` 里中间那道 `or stem`
     是**永远走不到的死分支**（`strip_season` 空时已返回「合集」），
     所以只有集号的文件名（`S01E01`）在 v1 归进「合集」组。
     我第一版期望写成 `S01E01`，按核过的结果改正为「合集」。
   测试两条：`archiveDramaKey` 解析表 + **扫描链路集成**
   （断言没有任何 `m:` 组、剧名组 HasMerged/MergedCount/MergedBytes 正确、
   openPath 指向最新那份、`Episodes` 仍是 2 不被合并产物污染）。

### 挂一笔技术债（本轮不夹带做）

`第N季` 的识别现在有**两份实现**：`internal/watchstore/season.go` 的 `SeasonNumber`，
和归档扫描里新加的 `stripSeasonSuffixLocal`（一个要序号、一个要去后缀）。
我没给 watchstore 新开导出，因为那会让"归档扫描"耦合"追剧季号"两个子系统；
但同一份中文规格写两遍就是会漂的地方，该收进一个独立 series 包统一实现。

## 第四十五轮（下半）：双击播放合并成品接通，并删掉一段照抄来的死代码

`DownloadsView` 的合并行现在 `@dblclick` → 走 `App.PlayerOpenLocal` 本地播放：
标题「本地播放 <文件名>」、选集侧栏不渲染、上下集禁用 —— 与详情页那条本地播放同一条分支。
`merge_multi_verify` 加到 17 条（⑩⑪⑬），全绿。

三处措辞/行为差异登记在这里，别让下一个人以为还欠着：

1. **文件不在的提示语**：v1 在合并这一处说「成品文件不存在：<文件名>」
   （`downloads.py:1639`），在历史/追剧那两处说「本地文件已不存在：<文件名>」
   （`watch.py:641`）。同一件事两句说法。v2 归一为一句（`player.localMissing`，
   由 `ApiError.code=local_missing` 触发），是有意的收敛，不是漏做。
2. **v1 那 0.8s 双击去抖没有照搬**：v1 注释原话是
   "a double click reports itself twice"（Qt 的现象）。web 的 `dblclick` 是一次事件，
   没有这回事。我一开始把去抖抄了过来，并为它写断言——
   **把去抖整段摘掉之后那条断言照样通过**，说明它是一条恒真断言，
   被测的东西在本平台根本不存在。所以代码与断言一起删掉，
   并在 `DownloadsView.onMergeRowDblclick` 注释与脚本 ⑫ 位置各写一句为什么。
   （这是本任务里第四次"绿灯不等于覆盖"：条件式跳过、种子值冒充证据、
   不存在的 testid、这次是恒真断言。）
3. **v1 docstring 与它自己的代码矛盾**（上一节已记）：
   「多季批次完成后不自动播放（reserved for single-season merges）」——
   v1 里两种合并**都**没有自动播放的实现。
