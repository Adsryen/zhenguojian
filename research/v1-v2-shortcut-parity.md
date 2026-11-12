# v1 ↔ v2 键位对账

来源（v1 侧全部逐行核对，不是回忆）：
`zhenguojian/app/zhenguojian/main.py:1765-1771`、`main.py:1774-1784`、
`player.py:925-941`（帮助浮层的 Esc/? 与"快捷键互相吃掉"的处理）、
`player.py:997-1007`、`player.py:1321-1326`。
v2 侧：`client-v2/frontend/src/views/PlayerView.vue`（`onHotkey`）、
`views/DetailView.vue`（`onGlobalEsc`）、`services/overlays.ts`、`main.ts`。

| 键 | v1 行为 | v1 出处 | v2 状态 | 本机证据 |
|---|---|---|---|---|
| `Space` | 播放/暂停 | main.py:1768 | ✅ 早有 | `player_verify` ③ |
| `←` / `→` | 快退/快进 5s | main.py:1766-1767 | ✅ 早有 | `player_verify` ③ |
| `↑` / `↓` | 音量 ±5 | player.py:1321-1322 | ✅ 早有 | `player_verify` ⑤ |
| `F` | 全屏切换 | player.py:1323 | ✅ 早有 | `player_verify`（按钮路径同函数） |
| `M` | 静音切换 | player.py:1324 | ✅ 早有 | `player_verify` ⑤ |
| `D` | 弹幕开关 | main.py:1769 | ✅ 早有 | `player_verify` ⑤/⑧ |
| `?` | 快捷键帮助浮层 | player.py:1325 | ✅ 早有 | `player_verify` ② |
| `Esc`（帮助开着） | 只收浮层，不做别的 | player.py:925-941 + 997-1007 | ✅ 早有 | `esc_semantics_verify` ④ |
| `Esc`（播放器，非全屏） | **关播放器（=停播）** | main.py:1774-1779 | ✅ **本轮补** | `esc_semantics_verify` ③ |
| `Esc`（详情页） | **返回上一页** | main.py:1781-1784 | ✅ **本轮补** | `esc_semantics_verify` ①② |
| `Esc`（搜索联想开着） | 收联想 | v1 无对应，v2 自设 | ✅ 早有 | `suggest_verify` |
| `Esc`（右键菜单开着） | 关菜单 | v2 自设（v1 无右键菜单快捷键） | ✅ 早有 | `contextmenu_verify` 场景A/B |

## 本轮补的两处，以及一起立下的契约

v2 之前 `Esc` 在详情页与播放器**非全屏**态都毫无反应——两整条键位语义缺失。
补的时候撞上并解决了一个真问题：**一次按键干两件事**。
右键菜单在 `document` **捕获阶段**听 `keydown`，Esc 一按就先把菜单节点摘掉；
页面级监听随后用"DOM 里还有没有浮层"来判断该不该让位，此时已经看不到了，
于是"关菜单"和"页面返回"同帧发生。

对策是 `services/overlays.ts` 的 **Esc 记账**：在 `window` 捕获阶段
（早于任何 document 捕获监听）记录"这次 Esc 出发时有没有浮层"，
页面级处理器读这个快照 + 当前 DOM 两层判据。
契约一句话：**新增浮层必须落进 `.zgj-ctx-menu` / `[role=dialog]` 标记之一**，
否则它会和页面级 Esc 打架。

## 顺带一条测试方法论

`esc_semantics_verify` 的断言写成"回到了 `/#/discover`"，**不是**"离开了详情页"：
Playwright 新标签首页是 `about:blank`，直接 `goto` 详情页后 `router.back()` 会退到那里，
否定式断言因此**假绿**——第一版就是这样被骗过去，看起来"①通过"其实走错了地方。
