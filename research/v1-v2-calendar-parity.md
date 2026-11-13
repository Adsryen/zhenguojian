# v1 ↔ v2 追剧日历对账（第五十四轮）

方法同第三十一到三十四轮：把 v1 那一面**逐条抄全**，再对 v2，
每条带 `文件:行`，下一个人可以自己核，不用信我的转述。

这次的 v1 出处是两份：

- `zhenguojian/app/zhenguojian/calendar_panel.py`（917 行，日历面板本体）；
- `zhenguojian/app/zhenguojian/test_calendar_panel.py`（361 行、**10 条用例**）。

第二份是关键：它是 v1 自己钉住的行为清单，
比我读代码总结出来的"应该有哪些行为"可靠得多 —— 而且它把分页那一族
（首屏补页 / resize 重填 / 重复页停摆 / 过期响应丢弃 / 失败同游标重试 / 首页不重复请求）
全部写成了可执行断言。v2 一条都没测过。

## 六条分页行为对账

| v1 用例（test_calendar_panel.py） | v2 之前的实现 | 现在 |
|---|---|---|
| `:215 existing_first_page_is_not_requested_twice` | `onMounted` 有 `if (!store.loadedOnce)`，语义相近但**没测** | ⚠ **仍未覆盖**（脚本只断言游标推进；要数请求次数得先给 store 加计数，不为此改产品代码） |
| `:222 short_first_pages_fill_viewport_automatically` | ❌ 没有：短列表停在半屏，用户得自己滚 | ✅ `fillViewport()`（视图层，RAF 单飞），①a/①b |
| `:236 resize_refills_visible_space` | ❌ 没有 | ✅ `ResizeObserver` 挂在 `.cal-scroll` 上，③ |
| `:248 duplicate_pages_stop_automatic_requests_and_can_retry` | ❌ 没有：服务端连着回同一页时，滚动条不变 → 每滚一次再发同一请求，界面"加载中"其实原地转 | ✅ `dupStreak >= 3` → `stalled`；文案用 v1 原话「连续多页没有新剧（点此重试）」；**游标照常推进**（v1 的 `requests[-1][1] == 4` 就是这个意思），⑤a–⑤e |
| `:274 rapid_gender_change_discards_stale_result` | ❌ 语义相反：`if (this.loading) return` **把用户的新点击整个丢掉** | ✅ `reqSeq`：照样发新请求，只认最后一次回来的响应，④b/④c |
| `:286 failed_page_retries_same_cursor_without_clearing_items` | ⚠ 半个：失败确实不清空、游标不动，**但没有任何地方能重试** | ✅ 状态行改成按钮 + `retryLoadMore()`，⑥a–⑥d |

## 「点此重试」是个不可点的 div —— 界面承诺了不存在的行为

v2 之前的那行是：

```vue
<div v-else-if="store.statusText" class="list-status text-muted">{{ store.statusText }}</div>
```

而 `calendar.statusFailed` 的文案是 **「加载失败：{m}（点此重试）」**。
也就是说界面对用户说"点此重试"，点下去什么都没有。

这与第三十四轮我改的那条（预约提醒 tooltip 明写"尚未接通"）是同一族问题，
只是方向相反：**那次是话没说满，这次是话说满了事没做。**
v1 `calendar_panel.py:165` 的注释明确写着"底部状态行；失败时点一下重试"，
所以这里补的是**对齐 v1**，不是我新发明的交互。

## 为什么这些行为在这台机器上以前根本触发不了

`services/api.ts` 的 mock 分支写的是：

```ts
calendar: async (date) => mockCalendarPage(date || ''),
```

**`offset` 与 `gender` 两个入参被直接丢掉**，而 `mockCalendarPage` 永远返回
`hasMore: false`。所以：滚动翻页、频道切换、重复页、失败重试
这些路径在 VITE_MOCK 下**一条都走不到**，Playwright 也就无从断言。

这是"fixture 太配合导致行为面走不到"，与我记过的
第三十九轮"mock 的图片全部就绪所以遮挡测不到"、
第四十一轮"种子值冒充证据"是同一类：**绿灯不代表覆盖。**

补法：新增 `mockCalendarPagePaged(date, offset, gender)`（fixtures.ts），
用测试缝 `window.__zgjCalPaging` 驱动 `pageSize/total/repeatFrom/repeatUntil/failFrom/slowMs/gender`，
并把每次调用记进 `window.__zgjCalLog`
（**分页判据必须看实际请求序列**，不能看我对"第几次调用落在哪一区"的想象 ——
本轮就是靠这条日志发现游标走到 7 而不是我以为的 6）。
不设这个变量时，默认路径与原来完全一致，所以既有 16 条日历断言看到的数据没变。

## 新验证：`calendar_paging_verify.py`（@5211，18 条 ALL PASS）

明写的未覆盖项（不假装）：

- v1 `:304 theme_colors_and_controls_fit_at_narrow_width` 与
  `:334 themes_update_existing_calendar_rows` 是 **Qt 主题重绘**语义；
  web 侧由 CSS 变量承担，本脚本只测"760px 下频道胶囊不溢出"这半个，
  **没有**断言"主题切换会重绘已存在行"。
- v1 `:215` 那条要数请求次数，本轮只断言游标推进。

## 本轮我自己的五个错（都在测试装置上，全靠跑出来）

1. mock 的重复模式把游标**退回 0** → "连续重复页"变成"来回跳页"，每页都有新剧，
   停摆永远不触发（我一开始以为是产品没生效）。
2. `retryLoadMore` 自己 `+pageSize` 推游标 —— 读了 v1 才知道游标本该在 `loadMore`
   里推进，重试只解除停摆；多推一次会**跳掉一整页内容**。
3. 引用不存在的 `size0()`；`date` 参数留着不用（typecheck 拦下，改成显式 `void date` 并写明原因）。
4. 频道值按"0=全部"反着猜（`GENDER_CHIPS` 实际是 全部=2/男频=1/女频=0）；
   读 `r.title`（真在 `r.drama.title`）→ TypeError。
   **两处都是"我以为的形状"与代码的形状不一致**，而代码一秒就能查。
5. **场景之间没重置 store**：pinia 是 SPA 单例，`onMounted` 的
   `if (!store.loadedOnce)` 让"换个 hash 再进来"不重载 —— ⑤ 继承了 ① 的
   `pageSize=1` 与游标 7，于是"连击只有 1 次"。
   讽刺的是这正是 v1 `:215` 那条行为的镜像：**我一边要测它，一边被它坑。**
   现在每个分页场景开**全新文档**，并等应用挂载完再设测试缝
   （第一版没等挂载 → `.cal-scroll` 超时；手工 debug 版加了等待就过 —— 差别在装置不在产品）。

## 正反都验过

把 `if (this.dupStreak >= 3)` 临时改成 `if (false && ...)`：
⑤b 文案退回「没有更多了」、⑤c 状态行不可点 —— **断言真的红**，
不是"因为没条件所以跳过"。还原后全绿。
