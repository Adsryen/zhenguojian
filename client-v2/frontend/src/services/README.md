# services/ 三层拆分（2026-11-15）

```
调用方（views / stores / components）
        │  import { api, DEGRADED, MOCK_MODE, ApiError, embedApi, ... } from '@/services/api'
        ▼
api.ts ── facade：只做 isMock/hasBridge 选用与组合；对外导出名与拆分前逐一对应
   ├── bridge.ts   真实桥：window.go 探测、callGo/callGoRaw（错误尾巴 `[code]` 的唯一剥码处）、
   │               realApi（80 方法）、embedBridge（PlayerAttach/SetBounds/HitBands/Detach 真桥面）
   ├── mock/       演示实现 + 测试缝（window.__zgj* 一族）
   │   ├── index.ts   组装 + 文件头「只存在于 mock」清单（字段/被忽略入参/行为替代/缝名）
   │   ├── player.ts  播放内核模拟（mockSnap/eof 位/MOCK_DM_COUNT=80 铺底）
   │   ├── watch.ts   追剧/历史/看过/预约 → local.ts（DEGRADED 降级面同款数据）
   │   ├── update.ts  假 release 表、30s 冷却、进度定时器、CustomEvent 总线
   │   ├── sorani.ts  内存态账号 + 假 SVG 验证码 + track 形状校验
   │   ├── embed.ts   内嵌 mock/无桥面（__zgjMockEmbed 谎报、__zgjHitBands 记录）
   │   └── window.d.ts __zgj* 的 Window 环境声明（从 types.ts 挪来，全工程生效）
   └── contract.ts 跨层唯一形状：NativeApi、ApiError、clampOpacityPercent（量纲约定）、
                   EmbedHostDebug/AttachResult、GO_BINDINGS（Go 侧真实签名清单）、
                   ExactApiImpl（入参槽位编译期门）
types.ts    数据形状本体；顶部 re-export NativeApi/ApiError，旧路径继续可用。
fixtures.ts mock 数据表（保持原路径：stores/player.ts 与 HistoryView.vue 直接 import 它——
            这两个越层引用在调用方地盘，留给后续轮收编）。
local.ts    localStorage 降级数据层（stores/watch.ts 与 mock/watch.ts 共用）。
```

## 入参完整性机制（ExactApiImpl）

TS 可赋值性容忍「实现少写参数」——`(a,b,c,d)=>R` 合法赋给 `(a,b,c,d,e)=>R`，
`saveWatch` 丢 `finished`、`cover` 只接一参、`calendar` 丢 offset/gender 都是这个洞。
`contract.ts` 用类型级恒等比较（`TupExact`）要求实现与契约的**参数元组严格相等**：
bridge/mock 两层都以 `... satisfies NativeApi` 得到真实推断类型，再赋给
`ExactApiImpl<NativeApi, typeof impl>`，少一个参数位编译期即红，报错点名方法名与
expected/got 两个元组。

- **抓到**：签名收窄（少参数位）、可选/必选漂移、参数类型错位、实现缺方法。
- **抓不到**：参数位收了但函数体不用（以 `_x` 显式声明即可通过——但静默截断至少
  变成显式表态）；同为 number 的参数互换顺序（类型相等，靠 GO_BINDINGS 注释兜底）；
  调用方实参数量（属调用方 typecheck 范畴）。
- 入参观测缝（`__zgjLastSaveWatch` 一族）保留原位：机制管「签名诚实」，
  缝管「调用真发生过」。

## 重构纪律

纯搬移，零逻辑改动：`git diff` 的每处代码差异只有三类——(1) import/export 重排；
(2) `cover`/`resolve`/`preloadEpisode` mock 面补齐 `_force`/`_quality` 参数位（行为不变，
由 ExactApiImpl 逼出）；(3) facade 的 if 分支改为一行式委托。逐行核对用：
normalized-code-lines diff（见交付报告）。

## 待办（verification/**，本任务无权改）

`i18n_static_scan.py` 的 `TS_EXEMPT["services/api.ts"]` 13 条条款随字符串搬家，
需要 re-key 到新路径（原因文案不变）：bridge.ts 2 条、mock/player.ts 3 条、
mock/update.ts 3 条、mock/sorani.ts 1 条、mock/watch.ts 2 条、mock/index.ts 2 条。
另：该脚本 `main()` 在 dead-clause 分支引用了尚未定义的 `args`（UnboundLocalError，
条款失效时必现），顺手应修。
