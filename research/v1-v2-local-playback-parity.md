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
- `internal/watchstore/store.go:321/395` 有 `LocalPath/HasLocalPath` 且会序列化出 `localPath`，
  但**没有任何代码写它**（439 只是 JSON 读回）；
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
