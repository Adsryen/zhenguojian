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
| **归档文件「播放」`:655`**（`playRequested.emit(target)`，直接播这个路径） | 无 | ❌ **缺**，需要 Go 绑定：`PlayerOpen` 只接受 drama/chapter 并走 resolve（`app.go:326`），没有"按本地路径播"的入口（mpv `loadfile`） |
| **归档文件「删除」`:661`**（tooltip「删除该文件（加密缓存保留，可重新解密）」） | 无 | ❌ **缺**，需要 Go 绑定：现有只有按任务号的 `DownloadRemoveBatch`（`bindings_download.go:133`），没有文件级删除；而且这个绑定**必须自带路径约束**（只允许删下载根之下），否则等于给前端一个删任意文件的口子——这条安全边界要在实现时定，不能事后补 |

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
