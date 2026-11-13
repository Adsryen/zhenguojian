# v1↔v2 双调查：设计 token 引用未定义 + 创作者/演职员页对账（2026-11-13）

方法：全 `src/` 的 `var(--x)` 引用扫描（含回退值判定与注释剥离）、tokens.css/base.css
逐行读、v1 `ui.py`/`creators.py`/`main.py`/`core_client.py` 逐行读、v2
`CreatorView.vue`/`DetailView.vue`/`api.ts`/`fixtures.ts`、Go 侧
`bindings_creator.go`/`bindings_cast.go`/`internal/native/*`、共用的
`zhenguojian/sidecar/core_overlay/discovery.go`。**只读调查**：未跑任何测试/构建，
未改代码；「现在就该红/缺键」这类断言是从审计脚本源码逻辑推出来的，没有执行核实。

## 第一部分：引用了却没定义的 token

扫描口径：`var(--x)` 无回退 = 整条声明在 computed-value 阶段失效（非继承属性回落到
**initial**，不是回落到父级或低优先级的有效声明）；`var(--x, yyy)` 有回退 = 静默降级用 yyy。
失效声明的具体机制核对过：CreatorView 的 scoped `.card`/`.btn`（specificity 高于
base.css 全局 `.card`(base.css:64-68)/`.btn`(base.css:114-126)）**赢下拉链**，随后整条
声明失效 → 取 initial（border-style:none / background:transparent），base.css 里写对的
`var(--surface)`/`var(--border)` 声明救不回来。

### 1.1 全部引用点（共 7 个未定义 token、21 处真实引用）

| token | 引用点 | 回退值 | 所在声明 |
|---|---|---|---|
| `--line` | App.vue:99（`.upd-banner`）| 有：`#2b2b33` | `border-bottom: 1px solid var(--line, #2b2b33)` |
| | App.vue:119（`.upd-banner-btn`）| 有：`#2b2b33` | `border: 1px solid var(--line, #2b2b33)` |
| | CreatorView.vue:390（`.btn`）| **无** | `border: 1px solid var(--line)` |
| | CreatorView.vue:411（`.card`）| **无** | `border: 1px solid var(--line)` |
| | CreatorView.vue:510（`.tool select`）| **无** | `border: 1px solid var(--line)` |
| | CreatorView.vue:543（`.toast`）| **无** | `border: 1px solid var(--line)` |
| `--surface-1` | CreatorView.vue:413（`.card`）| **无** | `background: var(--surface-1)` |
| `--danger-soft` | CreatorView.vue:430（`.err-banner`）| 有：`rgba(212,58,58,.08)` | `background:` |
| `--text-muted` | LibraryView.vue:619,631,705,740,756,764,770,783（8 处，均 `color:`）| 有：`#8a8f98` | 次要文字/榜单数字/提示 |
| `--guo-surface2` | SeasonMenu.vue:103（`.season-btn`）、125（`.season-menu`）| 有：`#171b23` | `background:` |
| `--guo-surface3` | SeasonMenu.vue:111（`.season-btn:hover`）、141（`.season-item:hover`）| 有：`#1c2130` | `background:` |
| `--guo-font-mono` | HotkeyHelp.vue:112（`.hk-keys`）| 有：`ui-monospace, monospace` | `font-family:` |

另：tokens.css:4 注释里有一个 `var(--token)` —— 不是真实引用（palette_audit 自己把
tokens.css 排除出引用扫描，palette_audit.py:90-92；我第一轮裸 grep 曾把它算进来，已剔除）。
确认这 7 个 token 在**全 `src/` 任何文件里都没有定义**（`--line:`/`--surface-1:`/
`--danger-soft:`/`--text-muted:`/`--guo-surface2:`/`--guo-surface3:`/`--guo-font-mono:`
全局 0 命中）；theme.ts 只切 `data-theme` 属性，不注入变量。
仓库无 `var(--${…})` 拼接写法（全仓 grep 0 命中）。

### 1.2 每处引用在界面上是什么 / 失效后看到什么

按「无回退 = 声明失效」的 5 处（都在 CreatorView）：

- **`.card`（390/411/413 波及）**：这是创作者页的「头部卡 + 作品卡 + 骨架卡」三张卡
  （template:298 `.profile card`、320 `.skel card`、327 `.works card`）。边框应画
  1px 细线、底应填表面色；失效后 `border` 回落 initial（style:none → **无描边**），
  `background` 回落 initial（**transparent**，透出页面 `--bg`）。用户看到：页面变成
  没有卡片边界感的两块内容，深色主题下「卡比页面亮一档」的层次消失，只剩 14px padding
  撑出的空块（radius 声明独立有效但无底色可切角）。同页 `.btn.ghost`（402-405，返回按钮）
  无碍——它自己把 border-color 显式设成 transparent（独立声明，不依赖 --line）。
- **`.btn` 的边框（390）**：重试按钮×2（template:294 profile 失败、349 works 失败）、
  「复制 ID」（309）、「加载更多」（363）。`background: var(--surface-2)`(391)、
  `border-radius`(393) 是**独立声明**照常生效 → 按钮变成「有填充、无描边」的扁块。
- **`.tool select`（510）**：分类/排序两个原生下拉（template:333,339）。失去 1px 边框，
  底色 `--surface-2`(512) 保留。
- **`.toast`（543）**：复制成功/失败提示条（template:370）。底色 `--surface-3`(542) 保留，
  只丢描边——四处里症状最轻。

有回退的 16 处 = 静默降级，但**不等于没事**：

- **App.vue:99/119**（更新横幅）：回退值 `#2b2b33` 两主题恒定。深色下与 `--border`
  `#2b2b36` 只差最后 3（肉眼难辨）；**浅色主题下横幅会在浅色底上画一条近深色线**
  （本该 `#e4e4ea`）。
- **LibraryView 8 处**：恒定 `#8a8f98`。深色 ≈ `--muted` 实际值 `#8d97a8`（偏差小）；
  浅色下 `--muted` 是 `#5f6878`（更深保证白底对比度），实际拿到的 `#8a8f98` 偏浅 →
  状态行/榜单数字/提示**对比度低于设计值**。
- **SeasonMenu 4 处（详情页「全 N 季」选择器，DetailView.vue:562 挂载）——最重的真症状**：
  回退 `#171b23`/`#1c2130` 是**深色主题的固定值**，而文字色 `var(--guo-text)` 是已定义
  token（浅色=`#23283a`，tokens.css:211）。浅色主题下季按钮和弹层面板 = 近黑底上
  深灰字，**文字几乎不可见**；hover 底色同样还是深色。深色主题恰好正常
  （回退值 = 深色正确值本身）。这是「有回退」掩护下的主题错配，比无回退那几处更难看。
- **`--danger-soft`（430）**：恒定 8% 半透明红，两种底色下都能凑合 → 症状轻；但
  `#d43a3a` 既不是深色 `--danger #ff5d5d` 也不是浅色 `#cf222e`，是第三种红（色相漂移）。
- **`--guo-font-mono`（112）**：回退链本身合法有效，纯命名漂移，视觉无损失。

### 1.3 v2 里语义最近的**已定义** token（dark/light 双值）

| 未定义 | 最接近已定义 | dark 值 | light 值 | 可否等同 |
|---|---|---|---|---|
| `--line` | `--border`(tokens.css:24/135) | `#2b2b36` | `#e4e4ea` | **可**。base.css 的 `.card`/`.btn`/`.chip` 细边框全用 `--border`(base.css:66,96,120)；App.vue 回退 `#2b2b33` 就是 `#2b2b36` 的抄录漂偏 |
| `--surface-1` | `--surface`(tokens.css:20/131) | `#141821` | `#ffffff` | **可**。v2 阶梯本来就叫 `--surface/--surface-2/3/4`(20-23)，无 `-1`；base.css `.card` 底色= `--surface`(65) |
| `--danger-soft` | `--danger`(42/152) 的半透明版 | `#ff5d5d` | `#cf222e` | **不可直接等同现有 token**——现有最接近的 `--accent-v1-soft #ff3b5c24/#fe2c551a`(37/147) 是品牌红族非报错色族；`--danger-border`(73/181) 是描边不是底。需要新增 |
| `--text-muted` | `--muted`(49/158) | `#8d97a8` | `#5f6878` | **可**。v1 原 muted 另挂在 `--muted-v1`(50/159)；`.text-muted` 工具类(base.css:70-72)已用 `--muted` |
| `--guo-surface3` | `--guo-surface-3`(98/202) | `#1c2130` | `#e9edf5` | **可**——回退值逐位等于深色值，纯粹是连字符拼写漂移（见 1.6） |
| `--guo-surface2` | `--surface-2`(21/132) | `#171b23` | `#f7f7fa` | **可**——回退值=深色 `--surface-2`；tokens.css 里没有 `--guo-surface-2` 这个档 |
| `--guo-font-mono` | （无） | — | — | **不可等同**：tokens.css 没有任何 font-family token；base.css:14 全局字体硬写 `"Segoe UI","Microsoft YaHei",…` |

### 1.4 v1（ui.py 调色板 + QSS）里的对应概念与换算

v1 用 `#AARRGGBB`，v2 约定 `#RRGGBBAA`（换算核对过两例：v1 `accent_soft` dark
`#24ff3b5c`→v2 `--accent-v1-soft` `#ff3b5c24`；light `#1afe2c55`→`#fe2c551a`，
ui.py:30,121 vs tokens.css:37,147）。

- `--line` 概念 = v1 `"border"`：**#2b2b36 / #e4e4ea**（ui.py:26,117），QSS 里
  QMenu/QTabPane/列表/输入框/表头分隔线都用它（ui.py:251,257,263,269,277,279,284）。
- `--surface-1` 概念 = v1 `"surface"`：**#141821 / #ffffff**（ui.py:22,113，注释
  「列表/表格/输入框」；QSS 用法 ui.py:268-269,279-280）。v1 键名没有 `surface1`。
- `--danger-soft` 概念：v1 **没有现成对应**——soft 族只有 `accent_soft`（dark alpha
  0x24≈14%、light 0x1a≈10%，ui.py:30,121）与 `guo_accent_soft`（alpha 0x26，ui.py:88,179）。
  v1 的报错底是**实色渐变族** `danger_hover_bg #2a1d21/#fdecee`、`danger_pressed_bg`
  （ui.py:62-63,153-154，role=danger 按钮 ui.py:397-402），不是半透明。v1 的
  CreatorPage 失败态本来就只是普通 hint 文字（creators.py:358,406），没有淡红横幅概念
  → `.err-banner` 的 tint 是 v2 新增语义。补值按 soft 族惯例 alpha：dark `#ff5d5d24`、
  light `#cf222e1a`。
- `--text-muted` 概念 = v1 `"muted"` **#a2a2b2/#55556a**（ui.py:40,131）或
  `guo_muted` **#8d97a8/#5f6878**（ui.py:93,184）——v2 都已翻译，分别挂 `--muted-v1`
  与 `--muted`。回退 `#8a8f98` 两头都不精确命中，属第三种值。
- `--guo-surface3` 概念 = v1 `guo_surface3` **#1c2130/#e9edf5**（ui.py:83,174）——
  v2 tokens.css 的 `--guo-surface-3` 逐值一致。**v1 没有 `guo_surface2` 键**
  （guo_* 派生清单 ui.py:80-106,171-197 只有 surface3）→ `--guo-surface2` 的意图值
  只能对到 v1 `surface2` **#171b23/#f7f7fa**（ui.py:23,114）。
- `--guo-font-mono` 概念：v1 **没有**等宽 token，全局字体写死 `'Microsoft YaHei'`
  （ui.py:248）。

### 1.5 结论：最小改动建议

**方案 A（最小改动：只动 tokens.css 一个文件）** —— dark 块（tokens.css:97-121 区段）补：

```css
--line: #2b2b36;            /* = --border；收编 App.vue/CreatorView 旧拼写 */
--surface-1: #141821;       /* = --surface */
--danger-soft: #ff5d5d24;   /* --danger 的半透明版，alpha 对齐 v1 accent_soft dark 0x24 */
--text-muted: #8d97a8;      /* = --muted */
--guo-surface2: #171b23;    /* = --surface-2 */
--guo-surface3: #1c2130;    /* = --guo-surface-3（拼写收编，值不变） */
```

light 块（tokens.css:202-225 区段）补同名键：

```css
--line: #e4e4ea;
--surface-1: #ffffff;
--danger-soft: #cf222e1a;   /* alpha 对齐 v1 accent_soft light 0x1a */
--text-muted: #5f6878;
--guo-surface2: #f7f7fa;
--guo-surface3: #e9edf5;
```

`--guo-font-mono` **不建议**以 token 形式补进 tokens.css：它不是颜色，补进去会触发
palette_audit 规矩 1 误判红（`is_colorish` 的排除词表 palette_audit.py:140 没有 font 类，
`"ui-monospace, …"` 会被当非法色值）→ 要么改 HotkeyHelp.vue:112 为字面 font-family，
要么先给 palette_audit.py 加 font 排除词再补键。

补值后的连锁效果（值得写进提交说明）：App.vue:99/119 的 `#2b2b33` 回退从此不再生效
（浅色主题那条深色线跟着消失）；SeasonMenu 四处回退同理被主题化值接管；
CreatorView 四处 `var(--line)` 恢复描边。`--danger-soft` 会从 `rgba(212,58,58,.08)`
换成 `#ff5d5d24/#cf222e1a`（alpha 8%→14/10%，色相归队 `--danger`）。

**方案 B（后续清理，不做也行）**：把 21 处引用改成已定义名（`--line→--border`、
`--surface-1→--surface`、`--guo-surface3→--guo-surface-3`、`--text-muted→--muted`），
`.err-banner` 底色可改用仓内现成的 soft 配方 `color-mix(in srgb, var(--danger) 12%,
var(--surface))`（base.css:156,163 已有同款惯例）。这能消掉「回退色散在组件里」，但动
6 个文件，不是最小改动。

**能钉住它的静态检查**：`client-v2/frontend/verification/palette_audit.py` 的 H 维已经
**算得准**——按引用点分档（palette_audit.py:67-72 注释里写明了「--line 有回退点但别处
无回退，按点分档、不按 token 并集」的教训），`undefined_no_fallback` 判红（446-451、
--json 模式 exit 1 条件 400-403）。按源码逻辑推断，今天跑它应当红在 `--line`、
`--surface-1` 两个键（`--danger-soft/--text-muted/--guo-surface2/3/--guo-font-mono`
进「只报告」档 452-456；`--guo-surface-3` 会出现在 unused_tokens 死名单 458-463，
正好和漂移引用对眼）。**但它现在钉不住**，盲区按危害排序：

1. **没接线**：run_all.py 的门禁组硬拦的只有 i18n_key_audit（run_all.py:246-260），
   脚本 glob 只收 `*_verify.py`（run_all.py:295-296），palette_audit.py 两头都不沾；
   `.github/workflows` 三个 yml 无引用（grep 0 命中）→ 不跑 CI、不跑全量回归，
   只有手动执行才生效。修法：像 run_all.py:247 那样 `import palette_audit` 并硬拦，
   或改名 `palette_audit_verify.py` 混进 glob。
2. **有回退档不判红、也不比较回退值**：SeasonMenu 那种「浅色主题里深色底+深色字」
   恰好全在有回退档（452-456 只 print），工具不看回退值与两主题 token 值的关系 →
   主题错配类问题永远只是 WARN。
3. **出处截断且没有行号**：`undefined_ref_sites` 每 token 只取前 4 个点、只到文件名
   （palette_audit.py:244-247）——`--line` 的 6 处会截掉 2 处，报告也指不到 CreatorView:390
   这一行。
4. **注释剥离是启发式**：CSS/HTML/整行//注释会剥、引号奇偶判行尾注释
   （palette_audit.py:100-112），多行模板串里的引用可误判；动态拼接引用扫不到
   （目前全仓无此写法，已核）。
5. **defined 集只认 tokens.css**：组件里若出现局部自定义属性定义+引用（本仓现状没有，
   已全局 grep 核实），会被误报「引用未定义」。
6. **规矩 1 会误伤非颜色 token**（1.5 的 font 键问题）。
7. 它**不会替你映射**「--guo-surface3 其实是 --guo-surface-3 漂了个连字符」——需要人把
   undefined 名单和 unused_tokens 名单对着看。

### 1.6 顺带确认：命名漂移是不是同一批

是同一类问题，证据链：v1 键名是下划线风（`guo_surface3`，ui.py:83）→ v2 tokens.css
按 CSS 惯例改成了连字符风（`--guo-surface-3`，tokens.css:98）→ 组件作者照 v1 原名直译
（`--guo-surface3`，SeasonMenu.vue:111/141）→ 定义侧与引用侧拼法分家：
`--guo-surface-3` 定义后 0 引用（死 token），`--guo-surface3` 引用了 0 定义（靠回退活着），
且回退值=`--guo-surface-3` 的深色值。**同一漂法解释了** `--surface-1`（阶梯首档 v2 叫
`--surface`）、`--text-muted`（v2 叫 `--muted`）、`--line`（v2 叫 `--border`；App.vue
回退 `#2b2b33` vs 真值 `#2b2b36` 的 1-step 漂移也是手抄痕迹）。`--guo-surface2`
略不同：v1 根本没有这个档（ui.py:80-106），是造了个「guo 前缀+surface2」的名字配
surface-2 的值。`--guo-font-mono` 则是仓内从未有过的概念（v1 字体写死 ui.py:248）。
各自的引用点与回退值已全列在 1.1 表。

## 第二部分：创作者/演职员页 v1↔v2 对账

代码位点：
- v1：`creators.py`（CreatorCard:53 / CastStrip:104 / _WorksGrid:194 / CreatorPage:289）、
  `main.py:315-352,354-380,398,421`、`core_client.py:360,438,441`。
- v2：`DetailView.vue:363-397,596-620`、`CreatorView.vue` 全、`api.ts:318-322,913-924`、
  `fixtures.ts:203-294`、`bindings_creator.go`/`bindings_cast.go`（client-v2 根）、
  `internal/native/engine.go:73`（discoveryActions 表）、`discovery_stub.go:23-32`
  （默认构建=discovery_unavailable）、`discovery_overlay.go:18`、共用的核心
  `zhenguojian/sidecar/core_overlay/discovery.go:112-135,181-247,250-330`、
  `router/index.ts:24`、门禁 `verification/creator_verify.py`（C1-C9）。

### 2.1 对账表（v1 行为 → v2 现状 → 差异分类）

| # | 维度 | v1 行为（file:line） | v2 现状（file:line） | 分类 |
|---|---|---|---|---|
| 1 | 入口 | 详情页演员条卡片 → 详情栈切 index1（main.py:316,350-352）；可反复进，每次点击 show_creator 全量重置（creators.py:323-324，token++ 防串台 336,357,391,405） | 条项点击 → `push /creator?u=<整对象>`（DetailView.vue:611；router/index.ts:24）；query.u 变化重 boot（CreatorView.vue:277） | 等价 |
| 2 | 入口条件 | 无 userId 的演员**卡片置灰不可点**（creators.py:159） | 条项恒可点（DetailView.vue:606-611）；CreatorView 只要求 `userId||id`（:35）。核心 profile 只读 `userId`（discovery.go:250）→ 光有 id 的人 v2 能进页但**必吃红条**，v1 直接在入口挡掉 | 可直接修（条项对无 userId 者禁用/隐藏） |
| 3 | 演员条截断 | 只显示前 8 +「查看全部 %d 人/收起演职员」（creators.py:175-179） | 全部渲染，无截断无展开（DetailView.vue:607 v-for 全列表） | 需要决定（版式选择；人数多时页头被拉长） |
| 4 | 演员条加载态 | 占位「正在加载演职员…」（creators.py:145-146） | 无加载占位，数据到达前整条不渲染（DetailView.vue:369-381,601） | 需要决定（轻） |
| 5 | 演员条失败 | 清条 + 状态栏提示「演职员信息暂不可用：%s」（main.py:378-379） | `catch {}` 静默（DetailView.vue:379-380）。v2 注释自称「避免常态刷屏」——那是 v2 的动机，不是 v1 规格；v1 明确有提示 | 需要决定 |
| 6 | 备案号 recordNumber | 详情页显示（main.py:369-370 setText/setVisible，控件 317-320,328） | 类型（types.ts:479）和 mock（fixtures.ts:211）都带该字段，**DetailView 无任何渲染点**（全 src grep 仅这两处） | 可直接修（展示缺失） |
| 7 | 粉丝/关注/获赞 | 只列非 None 的项、「值 标签」行内、`    ` 分隔；全缺 → 整行隐藏（creators.py:340-341）；**无 "--" 占位** | 恒三格堆叠（值上/标签下，CreatorView.vue:313-317），falsy → `creator.statPlaceholder` = "--"（i18n zh/en 均为 "--"）。v1 判据是 `is not None`，v2 是 falsy：核心串 `"0"` 两边都显示，但**数值 0/空串 v2 显 "--"、v1 显 " 获赞"式空值行**（formatHeat 对 0 的实际输出未核实） | 需要决定（显示规范；「--」是 v2 新增） |
| 8 | 「{role} · 红果号 {id}」行 | 进页立刻显示（取自入口对象，role 缺则「演职员」，userId 缺则空串仍显示）（creators.py:327） | profile 加载成功且 uid 非空才显示（CreatorView.vue:306-308）；uid 取 profile.userId→profile.id→入口对象（:49） | 可直接修（时机回齐；内容等价） |
| 9 | 复制 ID | 按钮「复制红果号」（creators.py:306）；点击**无任何文案反馈**，空串也写 clipboard（:373） | 「复制 ID」（CreatorView.vue:309，uid 为空时不显示按钮 :306）；三段文案：成功「已复制创作者 ID」、Go 轨失败「复制失败：{msg}」、双轨皆不可用「复制失败：剪贴板不可用」（:216-245；i18n 键全在）。入口对象变化后值优先取 profile.userId | v2 增强（v1 无对应文案） |
| 10 | 返回 | 「← 返回剧集详情」，永远回详情栈 index0（creators.py:298；main.py:341） | 「← 返回」`router.back()`（CreatorView.vue:283；i18n creator.back） | 平台差异（栈→路由历史）；v2 深链直达时 back 不保证落详情页 |
| 11 | 参数缺失 | 无此路径（唯一入口是卡片点击，对象必有） | `parseArg` 失败 → 「创作者参数无效，请从详情页演员条进入」（CreatorView.vue:258-264,287-289） | v2 新增防御 |
| 12 | 排序 最新/最热 | 服务器**永远收 time_desc**（creators.py:388）；「最热」= 已加载条目客户端重排（`_heat_value` 解析「万/亿」串，:36-39,363-372）、保留滚动位置、并给海报挂**热度名次角标**（set_ranked，:371→:276-282，badge_rank=:256,282） | sort 透传服务端重拉（CreatorView.vue:147,173-175；核心认 time_desc/hot_desc、非法回落 time_desc，discovery.go:289-291,297）；无名次角标、reset 重拉丢滚动位置 | 需要决定：v2 全局热序更准，但 v1 的「名次角标」在 v2 没有等价物 |
| 13 | works 分页触发 | 滚动到底自动（ScrollLoader，creators.py:318,408-409），无按钮 | 滚动近底自动 `emit loadMore`（PosterGrid.vue:153-158）**+**「加载更多」按钮（CreatorView.vue:363-365）；两路都走 `loadWorks(false)`（:180-183） | 等价（按钮是多出来的兜底） |
| 14 | hasMore 断环 | 三重：无 cursor / cursor 没变 / **去重后墙数量没涨**（creators.py:399-400） | `hasMore && nextCursor && cursor变化 && items.length>0`——第四条是**去重前的原始条数**（CreatorView.vue:157-159）。若某页返回全重复项+新 cursor+hasMore，v2 会一直「还有更多」，v1 会停 | 可直接修（把 appended 改成去重后 push 的计数） |
| 15 | works 去重 | `id 为真且没见过`才加（creators.py:393-394） | `!seen.has(d.id)`（CreatorView.vue:154-155），不检查 id 真值；核心页内已按 id 去重（discovery.go:311-325），跨页去重才轮到前端 | 基本等价（id 为空进 mock 面的行为未核实） |
| 16 | 失败重试 | profile 未载入 → 整页 show_creator 重走；works 失败且墙非空 → **不清墙续游标**（reset=not wall.count()）（creators.py:360-362） | profile 未载入同 v1；works 失败重试**总是清墙**（works=[]）+cursor 归零重拉首页（CreatorView.vue:188-203） | 可直接修（恢复 v1 续拉语义） |
| 17 | 失败文案 | profile 失败「作品加载失败：%s」（creators.py:358，措辞与所拉对象不符是 v1 自身旧错）、works 失败「加载失败：%s」（:406）；都是普通 hint 文字 + 重试钮（:313-314,319） | 两路统一「加载失败：{msg}」红条横幅（CreatorView.vue:292-295,347-350；i18n creator.failed）+重试 | 可直接修/可接受（v2 措辞已把 v1 的不一致拉平；红条横幅是 v2 视觉增强） |
| 18 | 空 tabs | 「该演职员暂无公开作品」+「作品 0」+不调 works+重试隐藏（creators.py:349-355）；核心同名错误兜底（discovery.go:219） | `noWorksCategory` → state-block 同文案、loadWorks 直接 return（CreatorView.vue:132,135-137,324-326；i18n creator.noWorks en "No public works"）；无「作品 0」计数行 | 等价（v2 少了计数行，措辞相同） |
| 19 | tab/子 tab | 先摆占位项「全部作品」(0,0)（creators.py:334）再按 tabs 重建；无 subTabs 时合成 `[{id:0,name:"全部"}]` → 标签「%s 全部」（:345-346）；combo 恒可见 | `tabChoices` 扁平「tab sub」；无 subTabs 时标签只有 tab.name（CreatorView.vue:97-102）；选项 ≤1 时下拉整个隐藏（template:331）；核心 tab=0=自动选首个、非法 tab 报「作品分类已更新，请重新打开个人作品页」（discovery.go:214-247） | 需要决定（标签措辞/占位差异，轻） |
| 20 | 中英双语 | 无 en（creators.py 全中文硬编码；ui.py 亦无 i18n 层） | creator.* 25 键 + detail 60 键 **zh/en 全存在**（i18n.ts 两张表逐一核，无一露键名）；「露键名」门禁已硬拦（t() 缺键回退键名 i18n.ts:1414-1416 → run_all.py:246-260 判红） | v2 新增能力；本轮范围内**未复现**键名露出 |
| 21 | 数据通道可用性 | v1 sidecar 构建期 overlay 常含 discovery（core_client.py:360,438-441 直连） | v2 双态：v2overlay 构建 → 真数据（discovery_overlay.go:18-20）；**默认构建 → `code=discovery_unavailable`**（discovery_stub.go:23-32）→ 创作者页红条可重试、演员条静默缺位（两个 bindings 文件透传错误） | 平台差异（构建变体）；发版若漏 overlay，两页恒空态 |
| 22 | mock 面 | —（v1 无 mock 层） | `creatorWorks` mock **忽略 tab/subTab**（api.ts:921；fixtures.ts:271-294：分页 3+4、hot=池反转、u_nv 抛错；profile 失败钩子 `zgj.creatorFail`）；门禁 C3/C4/C8 只钉排序/分页/空态（creator_verify.py docstring:20-30） | 可直接修（mock 补 tab 维度；分类切换在 mock 下不可观测） |
| 23 | 卡片 tooltip | 「%s %s，查看作品」accessibleName（creators.py:64）+poster_tooltip(name/description)；头像加载失败 tooltip 换成「头像暂未加载成功，仍可点击查看作品。」（:69-72） | cast-item `:title` = 「role · description」，缺则 name（DetailView.vue:610）；无加载失败 tooltip（骨架=首字圆，:614-615） | 平台差异/轻（内容构成不同但都有 hover 文案） |

### 2.2 「v1 有 v2 无」清单（本维度上）

备案号展示（表#6）、演员条 8 人截断+展开（#3）、「演职员信息暂不可用」失败提示（#5）、
无 userId 演员入口禁用（#2）、热度模式的名次角标（#12）、works 重试的「不清墙续拉」
（#16）、演员条「正在加载演职员…」占位（#4）。

### 2.3 「v2 有 v1 无」清单

复制成功/失败/不可用三段文案（#9）、参数缺失防御（#11）、红条横幅视觉（#17）、
「--」统计占位（#7）、加载更多按钮（#13）、en 全量文案（#20）、mock/降级测试缝
`__zgjCopied`/`__zgjCreatorWorksCalls`/`zgj.creatorFail`（CreatorView.vue:219-227;
fixtures.ts:229-244）。

## 我没查什么、为什么

- **没开浏览器、没跑任何脚本**（含 palette_audit/i18n_key_audit/creator_verify）：
  任务限定只读且禁止跑测试/构建。界面后果由 CSS 级联规则（ICVT 取 initial、非继承）
  + 声明上下文推出；「palette_audit 今天该红」是从其源码逻辑（235-247/400-403/446-456）
  推断的。
- **没逐像素验证浅色主题下 SeasonMenu 的实际可读性**：同上，只能从「底色恒为深色
  回退值、文字色是被定义的双主题 token」这两条代码事实推出「近黑底+深灰字」。
- **`formatHeat` 对 0/负数的输出没读实现**（discovery.go:271 调用）：只影响表#7 里
  「数值 0」边界的措辞，已标未核实。
- **v1 PosterCard 角标的具体画法没深挖**（widgets_guo.py）：名次角标的存在性证据在
  creators.py:256,276-282 传 badge_rank 已足够；样式细节不影响对账结论。
- **没对比 discovery.go 在 v1 sidecar 与 v2 overlay 两条构建里是否有分叉版本**：
  两边引用的是同一路径的文件（zhenguojian/sidecar/core_overlay/discovery.go），
  构建合入机制不在本任务范围。
- **红果真实接口行为（翻页深度、has_more 语义）没法离线核实**：表#14 的「全重复页」
  场景是从 v1 比 v2 多一道防线（creators.py:400）推出的风险差，不是已观测到的线上故障。
- **v2 CreatorView 的 `route.query.name` 参数**：已核实——入口 push 了它
  （DetailView.vue:611），router/index.ts:23 注释声称「仅供返回显示」，但 CreatorView
  实际只读 `query.u`（CreatorView.vue:31,277），无任何消费点。v1 没有这个参数概念
  （入口传的是对象本身）。死参数 + 过期注释，清理与否不影响行为。
