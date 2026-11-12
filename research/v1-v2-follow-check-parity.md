# v1 ↔ v2 后台追剧/预约检查对账（R23，提交 `d9f687a` + `397260f` + 收尾笔）

## v1 的完整规格（逐条有行号）

| 环节 | v1 出处 | 内容 |
|---|---|---|
| 定时器 | `main.py:714-726` | 主窗口建 `QTimer`；间隔 `ZHENGUO_FOLLOW_INTERVAL` 默认 **1800s**、下限 **60s**；启动后 **60s** 先跑一次；`_follow_busy` 防重入 |
| 挑行 | `main.py:780-782` | `status ∈ {想看, 在看, 预约中}` **或** `reserved`，上限 **50** 行 |
| 快照 | `main.py:786-788` | `(known, newSeason, arrived, newEpisodes)` |
| 逐条观察 | `main.py:806-812` | `core.detail(drama)` → 合并 → `watchstore.observe_drama(merged)`（旗标怎么变**由 observe 决定**，调用方不算） |
| 下一季 | `main.py:813-832` | 预约的是下一季时详情只能确认当前季 → 按 `base_title` 搜同系列（lane `series:auto`、本轮按 `series_key` 缓存）→ `find_season(target)` 或 `next_season(current)` → 不同 id 才 `mark_next_season`；**单轮搜索上限 12 次** |
| 节奏 | `main.py:833-835` | 条间 `sleep(0.8)`、搜索后 `sleep(0.3)` |
| 容错 | `main.py:836-838` | 单条异常只记 `failed`，**不打断整轮** |
| 结论 | `main.py:849-860` | 比前后快照算 `updated`；状态栏「追剧检查：%d 部有更新」/「%d 部暂时取不到更新」/「追剧检查失败：%s」 |

v1 的引擎本体在 `watchstore.py:348-426 observe_drama`。

## v2 之前的状态（为什么这是个"缺口"而不是"没做"）

`Store.ObserveDrama` 早就 1:1 移植了（`internal/watchstore/store.go:1176`，带单测），
但**全仓零调用方**：没有定时器、没有对外绑定、前端不触发。
所以用户点「预约/追剧」只是写了个标记，**没有任何人负责发现它上线了**。
（第三十四轮我在详情页对账时发现这点，当时先把 UI 承诺改成明说"尚未接通"，不假装能用。）

## v2 的实现与两条架构约束

1. **Go 只补最小一块**：`App.ObserveDrama(dramaJSON)`（`bindings_watchstore.go`）。
   不加 `Store.SnapshotRow`，比较观察前后的行用现有 `FollowList()/ToRowJSON()`；
   比较函数比的是二者的 JSON 形态——手写字段白名单一加新字段就漏比。
   `observed:false` 照 v1「没这条追剧记录就直接 return，连 emit 都不发」。
2. **遍历/节奏/季号归并放 TS**（`services/followCheck.ts`）：
   `base_title/series_key/group/find_season/next_season` 的权威实现已经在 `services/series.ts`
   （v1 `series.py` 的端口，有断言钉着）。在 Go 再抄一份 = 同一个规格两处实现，必漂。
3. **每轮开头/结尾都 `watch.load(true)` 现读主表**：
   v1 现读 `self._watch.follows()`；而 v2 里 `watchstore.OnChanged` **没接任何 Wails 事件**
   （全仓只有 `update:available` 一个监听），旗标在 Go 推进后没人推给前端，
   不主动重读就得等用户自己切页 —— 那不是 v1 的行为。
4. **mock/降级面的 `observeDrama` 刻意不模拟旗标推进**（`services/api.ts` 注释）。
   照抄一遍 Go 的规格 = 又一处会漂的真相；代价是「N 部有更新」这条计数**在 mock 里证明不了**，
   所以验证脚本明写这条边界，不把全绿读成"检测已验证"。
5. 提示语落点：v1 状态栏 → v2 `stores/watch.ts.checkHint` + `ReserveView` 一句话提示
   （`data-testid=follow-check-hint`），不弹窗、不打断操作。

## 验证（`follow_check_verify.py` @5207，13 条 ALL PASS）

①常数逐个对 v1（1800/60/50/12/800/300/60000）；②间隔四态（默认/下限/坏值回落/自定义）；
③挑行规则（含只认 reserved 的那种行，非候选状态不进）；④单轮 50 行上限；
⑤真跑一轮 `checked=3`；⑤b 无更新无失败时**不打提示**；
⑥注入 2 行详情失败 → 仍跑完整轮 + 提示语逐字等于 v1；
⑦`_follow_busy`：第二轮 `skipped=true`（不是排队）；⑧en 态无中文残留；
⑨中途塞第三条 → 第二轮 `checked=3`（证明现读，不吃页面缓存）；
⑩启动钩子已排期 + `stopFollowCheck()` 后清空（热重载不叠第二个定时器）。

**没被证明的**：真实"检测到更新"的旗标推进（Go 侧规格，只有 Go 单测 + 打包机真跑能证）；
以及真机上 `detail` 的真实网络往返耗时/失败率。

## 给后人的一个坑（写进代码注释）

`FIRST_RUN_DELAY = 60s` 意味着**任何单个页面会话跑超过 60 秒的验收脚本**，
都会在半路被这轮检查打断（它会重读主表、可能改写 store 行）。
目前全量回归里最长的一只是 33s，没撞上；撞上时该拆会话或显式 `stopFollowCheck()`，
**不要为了测试去改小这个 60s**（那是 v1 的数字）。
