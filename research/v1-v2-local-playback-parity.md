# v1 ↔ v2 本地成品播放对账（R22，Go 半边已通）

## v1 那条链（每步有出处）

| 环节 | v1 出处 | 内容 |
|---|---|---|
| 触发：追剧/历史面板打开一行 | `watch.py:636-645` | `watched(id).local_path` 存在且 `os.path.isfile` → `localPlayRequested(path, resume=entry.position)`；文件没了 → 状态栏「本地文件已不存在：<文件名>」；否则走线上 resolve |
| 触发：下载页合并成品 | `downloads.py:1631-1640` | 0.8s 去抖（双击会自报两次）→ `openLocalRequested(path, "")`；不在 → 「成品文件不存在：<名>」 |
| 汇聚 | `main.py:2648 / 2668 / 2684` | 两个面板 + 下载页都进 `open_local(path, decryption_key, resume)` |
| 播放 | `main.py:2609-2640` | 标题「本地播放 <basename>」；`open_for(basename)`；**清空选集条 + `set_neighbours(False, False)`**（本地文件没有季/集概念）；`player.play(path, local=True, decryption_key, resume, reuse=True)` |
| 本地弹幕 | `main.py:1880-1905 _attach_sidecar_danmaku` | 挂**下载时保存的同名 .ass**；注释还提醒：本地播放给的可能核心缓存里的加密原件，而 .ass 写在「下载/」下解密后的那份旁边 |
| 进度回写 | `main.py:2448-2449 / 2504-2505` → `watch.py:save_local_watch` | 由文件名 stem 反查剧（`_resolve_local`），history 行写 `local_path`；同位置同时长不重复写 |

## v2 之前的状态：**三段都断在半路**

- `internal/player/service.go:53` 有 `Plan.Local`，`internal/mpv/player.go:248/284`
  的 `lavfOptions(local, key)` 会把本地/解密参数一路带给 mpv —— **底层早就备好**；
- `internal/watchstore/store.go:321/395` 有 `LocalPath/HasLocalPath` 且会序列化出 `localPath`；
  **更正（第三十九轮）**：我原先在这条里写「没有任何代码写它」不准确 ——
  写入函数 `Store.SaveLocalWatch`（store.go:1445，v1 watchstore.py:608-637 的端口，
  带去抖与 duration 回落）**本来就在**，缺的是绑定、调用点和单测（三条都没有）。
  也就是说断点位置比我原先说的更靠外一层，这直接改变了下一笔该写什么：
  不是补一个 resolver，而是补一条 12 行的绑定 + 测试（`b3c2541`）。
- `frontend/src/services/types.ts:445` 声明了 `localPath?`，全仓**零消费点**（grep 空）。

所以这不是"入口没做"，是**一整条链没接**：没 writer、没 reader、没有能播本地文件的绑定。
上一轮我在下载面/详情页对账里写"归档播放需要 Go 绑定"，方向对但入口说错了：
v1 的本地播放入口在**追剧页与历史页**（外加下载页的合并成品），不是归档 tab。

## 本笔做的（Go 半边，CI `43300893766` 两个 job ✓）

1. `Service.OpenLocal(ctx, LocalRequest{Path, Title, DecryptionKey, ResumeMs})`：
   - **不经过 Resolver**（磁盘上的文件没有"解析地址"这一步；单测把 Resolver 摘掉跑通）；
   - `req` 的 DramaID/Index 留空 —— v1 用"清空选集条 + 关上下集"表达的同一件事，
     v2 的选集与上下集都读会话里的 `Episode`，留空就是那条语义；
   - `Session` 留空 → 沿用 `Open` 里 `plan.Session != ""` 的同一判断，线上弹幕抓取不启动。
2. `App.PlayerOpenLocal(path, resumeSeconds)` + **路径守卫** `checkLocalPlayable`：
   只放下载根之下的文件（根口径与归档扫描、设置页同用核心 `downloadDirectory`）。
   这不是可选加固：字符串来自前端，mpv 拿到什么就读什么。
   两个判定都不是随手写的：`filepath.Rel` 而不是 `HasPrefix`（后者会被 `..` 上跳骗过）；
   大小写折叠**只按平台语义开**（Windows 不折叠会拒自家成品；
   Linux 折叠会把 `/data/Downloads` 当成根内的 `/data/downloads` 放进来）——
   本机跑不了 Windows，所以把 fold 做成参数，`TestNormalizeForContainment`
   一条测试同时跑两种平台语义。

## 还没做的三件（都在 v1 有据，别当成已对齐）

1. **前端入口**：追剧/历史行若 `localPath` 存在 → 调 `PlayerOpenLocal`，
   否则走现在的 resolve；文件不在时给 v1 那句「本地文件已不存在：<文件名>」；
   播放器标题「本地播放 <文件名>」。
2. **`localPath` 的写入侧**：v1 是 `save_local_watch`（文件名 stem 反查剧集 + 写进 history 行）。
   Go 里字段与序列化都在，缺 writer 与 `Store` 侧的 `_resolve_local` 等价物。
   **没有这一件，第 1 件永远不会触发**（行里压根没有 localPath）。
3. **本地弹幕 sidecar .ass**（`main.py:1880`）：`OpenLocal` 现在只是"不去线上抓"，
   没有挂下载时保存的同名 .ass —— 需要下载侧的命名约定一起做。

## 一条方法论：我的阳性对照在 CI 上真的拦了一次假绿

`TestOpenLocalDoesNotFetchOnlineDanmaku` 我特意加了"线上 Open 必须先触发抓取"的对照，
第一版 CI 就红在这一句（`等待超时: 线上 Open 应触发抓取`）——
原因是我漏了 `setLoaded`：抓取窗口要先知道时长才发起。
**如果没写对照，这条用例会永远绿着，什么也不证明**（第 27 轮"恒为 0 的计数不是证据"的机器兑现）。
本地侧现在也给一次 `setLoaded`，否则"没抓到"可能只是"还没时长"。

## 第三十九轮的三条读数错（全在我的测试里，端口语义一条没改）

CI 首跑红三条，判到根上都是**我的测试写错**：

1. `row["episode"].(float64)` 静默失败得零值 → 断言报"值是 0"，看着像端口漏字段。
   真相是同包 `decode()` 开了 `json.Decoder.UseNumber()`，数字是 **json.Number**。
   ⚠ 这条最阴的地方是它的**表现形态是假阳性**：类型断言失败不报错，给零值。
   以后在 watchstore 包里读行数值一律走 `rowNum` helper（两种数字类型都收，别的类型直接判错）。
2. `updated` 的相等比较同样落在 json.Number 上。
3. `NeedsDramaID` 用例假设库文件必然存在，而 v2 是**首次变更才落盘** ——
   空 dramaID 那条什么都没写，文件本就不该在。现在 `localRows()` 把"文件不存在"
   当成"一条都没写过"的证据，而不是读库失败。

五条语义断言一条没删、没放宽：去抖阈值仍两侧都测（0.4s 不写 / 0.6s 写）、
`finished=true` 仍必须落盘、`duration=0` 仍回落、仍要求绝对路径。


## 第四十轮：入口接通了，并且挖出一个契约级缺陷

三段断口的第三段（前端入口）补完：`HistoryView.goResume` 见到行上的 `localPath`
就走 `?lp=` → 播放器 `bootFromQuery` 的本地分支（**完全不碰 resolve**、失败不回落演示模式、
不给「重试」死按钮）、选集侧栏整块不渲染、上下集 disabled —— 逐条对 v1
（watch.py:636-645、main.py:2612-2618/2634）。CI `43309923496` 两 job ✓，
`history_local_play_verify` @5208 14 条 ALL PASS。

### 挖出来的东西比入口更值钱：`!!snapshot.episode.index` 是个错误判据

前端一直用「集号非空」推断"会话已打开"。本地会话刻意没有集号，于是被读成没打开
→ `running=false` → 进度节流落盘整条不触发。修法不是在前端特判 `index===0`
（那等于让一个字段兼职两件事，下一处读它的代码还得再猜），
而是让契约自己说：Go `Snapshot.Local`（由 `plan.Local` 落）+ 前端/mock 同形。

发现路径值得记一句：**它不是读代码读出来的**，是断言 ⑤ 反过来逼出来的——
我原先把 ⑤ 写成"比 localPath 字段对不对"，而那个值是我自己灌的种子，
所以写入压根没发生也照样绿；把 ⑤ 改成"ts 前进 + position 变了"之后立刻红，
顺藤才摸到契约这一层。→ **断言要测"发生了什么"，不是测"字段长什么样"。**

### 同轮在这条脚本身上抓到四处假绿风险（都已改成能真失败）

1. ⑤ 只比种子值（见上）；
2. ⑥c 查了一个**本仓不存在的 testid**（`player-demo`），`count()==0` 恒真；
   演示模式的真标志是 `.pl-badge.demo`；
3. ⑦ 依赖 mock 里"路径含 `..`"的临时启发式 → 改成显式喂 code，
   并写明越界守卫归 Go 单测（mock 不假装重新实现规则）；
4. ⑨ locale 改完没重载文档 → 拿到中文（**本任务第四次**栽在"只改 hash 不重载"：
   21 轮 init script、29 轮 mock 注入、34 轮 reserve、本轮）；
   外加 ⑦ 改过那行的 localPath，⑨ 必须重新灌种子——共用页面状态时改数据的人负责复原。

### 还欠着的一处结构问题（本轮只补字段，没夹带重构）

历史行的映射存在**两份**：`stores/watch.ts` 的 `localHistoryRows()`（mock/降级面实际走这条）
与 `api.ts` 的 `mockApi.historyList()`。第三十四轮的"预约刷新即丢"和本轮的
"localPath 带不出来"都是同一成因：只补了其中一份。
该收成一处（由 `services/local.ts` 出唯一映射），那是一次独立重构。

### 第四十一轮：③ 的写入这半段落地（CI run `43311876496` ✓）

先确认一件事：v2 的**核心不写**这两个旁挂文件
（`grep -rn "danmaku.json" guoapp-src` 为空），所以要补的是客户端，
对端是 v1 `library.py:413-475`（`_queue_danmaku`/`_collect_danmaku`）
+ `danmaku.py:78-101`（document / write_file）。

新增 `internal/danmaku/sidecar.go`：`SidecarPaths / SidecarReady / WriteTextAtomic /
SidecarDocument / CollectSidecar`，resolve 与整集抓取两步**做成注入字段**——
本机没有 Go 工具链、CI 也连不上核心，
只有把"抓完之后怎么写"隔离出来才测得到；接线层（下一笔）负责真调核心。

四条照 v1 而不是照"我觉得更好"，每条都有测试钉着：
- `SidecarReady` 要求两个文件都在且 `.ass` 非空（只查 .ass 则写坏一半永不重补）；
- **无弹幕不写空文件**（写了 Ready 变真，这一集从此补不上——最阴的坏状态）；
- 跳过判定在 resolve **之前**，测试断言"已存过时 Resolve 一次都没被调用"；
- `.danmaku.json` 字节形状对齐 v1：`indent=1`、不转义 HTML、
  空 items 是 `[]` 不是 `null`（Go 的 nil slice 会写成 null）、末尾不补换行。

两处刻意沿用 v1 且写明后果：json 写失败**不回滚** .ass
（半套不会让 Ready 变真，下次整对重写，代价只是中间那段时间本地播放没弹幕）。

### 本轮最值得记的一次 CI 报错：我的测试文件让生产代码编译不过

`sidecar_test.go` 里我为了省事写了 `func min(a, b int) int`，
而 Go 1.21 起 `min/max` 是**内建函数**——同包内同名声明会把内建遮蔽，
于是 `ass.go:56` 的 `min(0.05, o.Opacity)` 与 `fetcher.go:271` 的浮点 min() 全部编译失败。
报错出现在**别人的文件**里，第一眼会以为 danmaku 包本来就坏了。
本机没有 Go 工具链，这类只有 CI 抓得到。已改成 `head()/fileExists()` 并把教训写进注释。
顺带修 `SidecarPaths(video)[0]`（多返回值不能下标取）。

### 剩余

③ 还剩接线两半：下载完成后调用 `CollectSidecar`（含客户端开关「下载时保存弹幕」，
v1 `downloads.py:1095-1099` + `tools.danmaku_download_enabled`，默认关）
与本地播放时 `attach_danmaku(ass, count)`（v1 `main.py:1880-1895`，
它还要读同名 `.danmaku.json` 拿条数）。`OpenLocal` 目前只是"不去线上抓"。
