# v1 ↔ v2 详情页对账（第三十四轮）

同样先逐行抄 v1：`main.py:174-192`（头部按钮条）、`main.py:440-454`（四态文案）、
`main.py:1539-1546`（预约处理与提示）、`detail_episodes.py:110-160`（分集区批量条）、
`main.py:266`（全 N 季）。v2 侧：`views/DetailView.vue` + `stores/watch.ts` + `services/local.ts`。

## 头部按钮

| v1 | v2 | 状态 |
|---|---|---|
| `＋ 加入追剧` / `✓ 已在追剧` | `＋ 追剧` / `已追剧 ✓` | ✅ 语义同（措辞不同，v2 全站统一短式） |
| `☆ 预约上线 / 预约下一季 / 预约更新 / ✓ 已预约` | 「☆ 预约…」四态 | ✅ **本轮补**（之前整页没有预约入口，只有日历页能预约） |
| `下载全剧` | `下载全部（N 集）` | ✅ |
| `复制简介` | 同名 | ✅ |
| 保存海报（`main.py:1495` 弹另存为） | `↓ 高清海报` | ✅（真机弹框仍待验） |
| `全 N 季 ▾`（`main.py:266`） | SeasonMenu | ✅ |
| 播放入口 | `播放第 1 集` + 分集格 | ✅ |

## 分集区批量条（v1 `detail_episodes.py:110-160`）

| v1 | v2 | 状态 |
|---|---|---|
| `批量下载` / `退出批量` | 下载当集 / 下载全部 | ⚠ v2 没有"批量选集"模式；对应能力由**下载面板**的选集流程承担（`DownloadsView` 的勾选 + `enqueue`），不是缺失但入口不同 |
| `全选` / `清空` | `selectAll` / `unselectAll`（下载面板） | ✅（同上，位置不同） |
| `▶ 播放选中` / `↓ 下载选中` | `enqueue` / 播放第 N 集 | ✅ |
| `未选择分集` / `已选 N 集` | `downloads.mergeSelHint` 等 | ✅ |

## 本轮对账顺带挖出的两个真 bug（都不是"详情页按钮"本身）

1. **预约写进去读不回来（影响真机降级面，不只 mock）**
   `localReserve` 只往 `zgj.reservations` 写 `id→true`，而 `localFollowRows()` 只从
   `readFollows()` 长行；v1 互斥语义（预约同时取消追剧）一生效，这条剧**两头都不在**：
   刷新后按钮回到未预约、日历/追更页也看不见它。
   修：伴生表 `zgj.reservations.drama`（id→drama）+ 两处读路径合并。
   ⚠ 真桥不可用时前端走的就是这条本地层，所以真用户在桥故障期间的预约同样会"看着成功、刷新就没"。
2. **提示语空书名号** `已追剧《》` / `已预约《》`
   `?drama=<id>` 简写链接进来的 `drama` 是最小形状，`title` 是空串。
   修：`displayName()` = 详情标题 → 路由标题 → `detail.noTitle`。
   没顺手改的：下载草稿里 `title: d.title`（那是写入的数据不是展示文案，改它要单独判断）。

## 还没接上的那条链（R23，需要决定后再做）

v1 预约成功后承诺「应用运行期间每 30 分钟检查一次，上线/更新后在追更预约页提示」，
其实现是 `main.py:718-720`（间隔 1800s，可用 `ZHENGUO_FOLLOW_INTERVAL` 覆盖）驱动的
`watchstore.observe_drama`（`watchstore.py:348-426`）。

v2 侧：**`Store.ObserveDrama` 已移植**（`internal/watchstore/store.go:1176`），
但**全仓零调用方** —— 没有定时器、没有绑定、前端也不触发。
于是"预约"目前只是打了个标记，**没人去发现它上线了**。
所以详情页 tooltip 当时明写「上线/更新提醒尚未接通」，
`detail_reserve_verify` ⑦ 把这句钉成断言，防止有人悄悄把承诺放回 UI 而不实现功能。

> **这段已经过期（第四十一到四十二轮）**：后台检查接通了
> （`services/followCheck.ts` 照 v1 `main.py:714-726/776-860` 的节奏与规则，
> Go 侧补了 `App.ObserveDrama` 绑定），tooltip 也随之换成
> 「应用运行期间每 30 分钟检查一次更新」，⑦b 改成钉"承诺必须与真节奏一致（含 30 这个数字）"。
> 上面这段留着是因为它记录了**当时的状态与那条规矩**，不是当前的实现状态。

要接通需要定三件事（都是决定，不是我能替你猜的）：
1. 检查节奏：照 v1 的 1800s + 环境变量覆盖，还是挂到已有的 Go 侧循环上？
2. 触发点：`App.startup` 起 goroutine，还是前端 store 定时调 `ObserveAll` 绑定？
3. 提醒落地：v1 会把 `arrived/newSeason/updateSeen` 等旗标写回行并在追更页提示；
   v2 要不要同一批旗标（Go 已有字段：`NewSeason/Arrived/ArrivedSeen/UpdateSeen`）。
