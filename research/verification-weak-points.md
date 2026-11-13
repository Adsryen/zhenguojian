# 验收/门禁装置体检：哪些"看起来绿"其实抓不到东西

生成：2026-11-13（本文件是本次只读体检唯一写入的文件；未运行任何测试/构建，
未改动任何被审文件）。方法：逐行读 `client-v2/frontend/verification/*.py` 全部 41 个
脚本（外加 06:31 并发新增的 `theme_contrast_verify.py`，共 42 个）、
`.github/workflows/build-v2.yml`，并对可疑判据做了**仅内存里**的正则复演/在座对账
（不落盘、不改文件，复演处标注"实测"）。凡未核实的都写明"未核实"。

标注：【实】= 我在当前源码上实际复演/对账验证过；其余为读码推断。

---

## A. 无牙齿（断言几乎不可能失败 / 区间包住坏实现）

### A1. player_verify.py:162-163 —— "恢复播放"用 `>=`
```python
check("③ Space 恢复播放（时间前进）", main_time(page) >= t_a,
      f"t={main_time(page)} base={t_a}")
```
- **抓不到什么**：`t_a` 是暂停期间冻结时刻。恢复坏掉（Space 只能暂停不能恢复）时
  时间继续停在 `t_a`，`>=` 照样绿。时间不可能倒退，所以这条只有实现崩掉才红。
- **最小反例**：`PlayerView` 的 keydown 分支里把"恢复"写死成"只暂停不恢复"
  → ③暂停断言绿、③恢复断言也绿，全脚本绿。
- **建议**：`main_time(page) > t_a`，更稳的是两次采样 `t2 > t1 > t_a`，
  并读 store `status==='playing'` 做第二真相。

### A2. player_verify.py:308-316 ⑧e —— 钳位测的是浏览器，不是 store
```python
# 越界输入由 store 钳回 20–100（滑条本身给不了越界值，这里直接驱动 store）
rng.evaluate("""(el) => {
  el.value = '5';
  ...
check("⑧e 越界值被钳成下限 20（不写脏 LS）",
      page.evaluate("() => localStorage.getItem('zgj.player.danmakuOpacity')") == "20",
```
- **抓不到什么**：`el.value='5'` 打在 `input[type=range] min=20` 上，**浏览器在赋值瞬间就
  把 value 钳成 20**，input 事件带过去的已经是 20 —— `stores/player.ts:695`
  的 `Math.max(20, …)` 根本没被越界值触发。注释说"直接驱动 store"，代码驱动的是滑条
  （声明与事实不符）。【实】
- **最小反例**：把 `setDanmakuOpacity` 的钳位整段删掉 → ⑧e 依旧绿（滑条替它钳了）。
- **建议**：像同文件 ⑥ 那样拿 pinia 实例**直接调 store action**
  `store.setDanmakuOpacity(5)` 再断言 LS=20；或 `el.dispatchEvent` 之前先证明元素值被改成功。

### A3. sorani_verify.py:391-392 M13 —— 上限断言的上界不可达
```python
await many_point_drag(pc, "[data-testid='sorani-handle']", 240, steps=600)
...
check("M13 600 步拖动：点位被夹在上限内且仍发得出（v1 test_point_cap_4000）",
      2 <= n13 <= 4002, f"点位数={n13}")
```
- **抓不到什么**：输入只有 600 步，`n13` 天然 ≪ 4000；`<= 4002` 是恒真侧。
  v1 的 `test_point_cap_4000` 钉的那条钳位，v2 这条测不到。
- **最小反例**：把点位上限常量删掉/改成 999999 → 全绿。
- **建议**：用 `page.evaluate` 直接喂 >4000 点的合成 track 进采集函数（或加"原始事件数"
  缝），断言 `原始>4000 且 提交==4000`；否则就把断言名改成"600 步仍能发出"，别称上限。

### A4. sorani_verify.py:557-558 M21b —— 区间 `[340,350]` 含"换算坏了"的 350
```python
check("M21b 拖过轨道上限时 x 撞在钳位边界（≈349 ≤ 350），不为负也不越界",
      bool(xs21b) and min(xs21b) >= 0 and max(xs21b) <= 350 and max(xs21b) >= 340,
```
- **抓不到什么**：scale 被写死成 2 的坏实现，265px→钳位**恰好 350**，落在区间里 → 这条绿。
  这正是第 57 轮在 M21 上修掉的形状——M21（525-527 行）现在判
  `abs(max-exp_x)<=6 and max<350`（有牙齿了），但姊妹条 M21b 原样保留。
- **最小反例**：`scale 写死 2` → M21 红、M21b 绿 —— 整套能抓到靠的是 M21 而不是它自己；
  单看 M21b 的"撞在钳位边界"承诺是虚的。
- **建议**：M21b 判 `max == round(limitDisp*scale)`（期望值 ±1），而不是"≤上限且≥340"。

### A5. sorani_verify.py:306-307 M10 —— "确实撞到上限"没被断言
```python
check("M10 x 钳到 [0,490]，而 y **不钳**（…）",
      max(xs) <= 490 and min(ys) < 0,
```
- 注释说"拖到 900px 远，说明确实撞到上限"，断言只有 `<=490`：若拖动实现中途
  把手卡住（max≈300），钳位是否生效根本不会被区分。抓不到"钳位上限值本身写错（偏大）"。
  【实：900px 是输入量，不是断言量】
- **建议**：加 `max(xs) >= 489`（撞线证据）再谈 `<=490`。

### A6. detail_download_verify.py:184-185 —— "下载当集"的 index 判 `>=1`
```python
check("② 下载当集：entries 长度=1 且 index≥1", len(e1) == 1 and e1[0]["index"] >= 1,
```
- **抓不到什么**："当集"的正确集号没有被对照。stub 的 Detail 固定 8 集，前端播哪集它都送
  `index>=1` 就绿。
- **最小反例**：`DownloadCurrent` 实现改成永远 `index=8`（末集）→ 全绿。
- **建议**：先断言当前集号（store/title），再要求 `e1[0]["index"] == 当前集号`。

### A7. creator_verify.py:151 —— `"7" in status_txt` 子串判数
```python
check("C4e 状态行已加载 7 部 + 末页按钮消失", "7" in status_txt and page.locator(".works-status .btn").count() == 0,
```
- "17"、"7/35"、任何带 7 的串都绿。建议 `re.search(r"已?加载?\s*7\s*部", status_txt)` 逐字。

### A8. discover_hero_verify.py:206-208 —— `pulled >= 60` 包住阻尼写错
```python
check("A3 顶部下拉超阈值 → 触发既有强刷新 loadFirst(true)",
      after == base_cnt + 1 and pulled >= 60,
```
- 注释给的期望是"阻尼 0.5 × 264，封顶 130"→ 应判 ≈130±几；`>=60` 让阻尼 0.23 的坏实现
  （61px）也过。建议 `120 <= pulled <= 132`。

### A9. merge_multi_verify.py:208-210 —— "已完成的行标可播"没排除别的行
```python
check("⑩a 「合并」页签有行，且已完成的行标成可双击播放",
      rows_n >= 1 and playable.count() >= 1,
```
- **最小反例**：`.playable` 无脑挂到**所有** merge 行（含失败行）→ 仍绿。
- 建议：从 `merges_of` 取 done 集，判 `playable 的行 id 集合 == done 集合`。

### A10. downloads_archive_scan_verify.py:181-188 ⑥b —— 两个相反结局都算过
```python
toast_ok = d["toast"].strip() in ("已复制目录路径", "复制失败，请手动选择路径")
...
toast_ok and (clip == "" or clip == MOCK_DIR),
```
- 本环境 `grant_permissions` 成功（101-103 行 try 里，注释也写明"失败才走 copyFail 分支"），
  但断言不检查"本环境属于哪一支"：**复制功能整个坏掉（总是失败）** 时 ⑥b 依旧绿。
- **最小反例**：把 copyDir 实现改成恒抛异常 → toast=「复制失败…」→ 绿。
- 建议：把分支判成输入（`clip_granted = try grant 成功`），granted 时只接受成功支并逐字比剪贴板。

### A11. discover_hero_verify.py:163-172 —— 测的是测试自己的 oracle
`exp_by_title` 由 FIXTURE_JS（测试内自己写的 badge() 复刻函数，68 行注释自称"独立实现"）
算出；163-172 三条"A4 口径"断言是 `exp_by_title.get("山河引") == "更新至40集"` ——
**产品 DOM 没参与**，是"我的复刻 vs 我的复刻"。牙齿在 158-161（mismatch=DOM vs oracle，
这条有牙）。163-172 只在 fixtures 数据变动时假红（吵闹方向）。建议：这三条并入 fixture
数据回归说明，或改成拿 fixtures 原始字段现算期望。

---

## B. 条件式跳过

现状结论：**脚本内的老病基本已被"前置断言 + 无条件打印"改造**（下面列出正例），
真正剩下的洞在**元检测器**自己。

### B1. run_all.py:194-198 + 319-323 —— 静默跳过的对账**默认关**
```python
ap.add_argument("--audit-checks", action="store_true",
                help="…只当人工判读的线索…所以默认关掉，免得狼来了。")
...
if args.audit_checks and res["code"] == 0 and printed < res["declared"]:
```
- 第 22 轮立的"声明数 vs 打印数"防线默认不跑；`exit0 + FAIL行=0` 直接进 bad 判定线
  （313-314 行）。**任何脚本"少测几条但 exit 0"在默认全量回归里不可见。**

### B2. run_all.py:96-97 —— 声明计数本身有形状盲区【实】
```python
return sum(1 for line in text.splitlines() if re.match(r"\s*check\(", line))
```
- 只数**行首**的 `check(`。`ok = check(...)`（creator_verify.py:123 就是这么写的）、
  单行 `if img.count() > 0: check(...)`（正是第 22 轮的病形）都不计数。
- **最小反例**：新脚本写 `if page.locator(X).count(): check("关键", ...)` 同一行
  —— declared 少计 1，printed 少计 1，对账平账 → 即使打开 --audit-checks 也看不见跳过。
- 建议：计数改成 `re.search(r"\bcheck\(", line)`（同行多算可接受），把"单行 if 包住
  check"列为 code review 禁止形状，或 run_all 直接 grep 该形状报警。

### B3. 正例（改造过的形状，供对照）
- contextmenu_verify.py:113-141：`if not hit: check(..., False, "没有可命中的 img…")`
  else 打印真断言 —— 第 22 轮病灶已修。
- esc_semantics_verify.py:118-131：②a/②b 在 `if menu_visible:` 里，但 118 行
  `check("②前置 …", menu_visible)` 保证缺席即红。
- creator_verify.py:132-145：轮询设 flag（`if count>0: seen_btn=True`）后
  **无条件** `check(seen_btn)` —— 条件在采集、断言在外，正确形状。
- merge_multi_verify.py:188-190 / sorani M13 394-395：else 分支显式判 False。

---

## C. 选择器/testid 对账（去 src 核对过，不是只看脚本）

方法【实】：脚本里全部 `data-testid` 引用（剔除注释行/文档串行）89 个 → 与 src 里
静态 `data-testid="…"` + 动态 `:data-testid="\`…-\`` 共 122 个对账。

- **结论：当前没有"指向不存在 testid 的活引用"。** 两个历史幽灵只活在注释里：
  - history_local_play_verify.py:157-161 记录了 `player-demo` 幽灵（恒真断言）如何被改成
    `.pl-badge.demo`（`PlayerView.vue:376` 【实】存在）。
  - danmaku_toggle_verify.py:26-28 记录 `concurrencyBtn` 是照键名编的选择器（老毛病声明）。
- 动态 testid 两处易被误报"不存在"，核实为存在：
  - rankings_verify.py:139/153 用 `rank-board-huangdou-all`、`rank-row-3` ——
    src/views/LibraryView.vue:540 `:data-testid="\`rank-board-${b.id}\`"`、
    :552 `rank-row-${it.rank}`，且 fixtures.ts:1120 确有 `id: 'huangdou-all'`。【实】
- "count()==0" 负断言逐个查证**目标可渲染**（否则是永真）：
  `player-retry`（PlayerView.vue:391，v-if 分支）、`follow-check-hint`、
  `.pl-panel`、`.sg-ghost`（SourceGrid.vue）、`.sa-allfail`（SearchGroups.vue）、
  `.err-banner`/`.works-status .btn`/`.profile`（CreatorView.vue）、`.desc .more`
  （DetailView.vue:575）均在 src 存在。【实】
- 残余风险：负断言的牙齿依赖"坏实现会让它出现"，这一条无法静态证明（见 D 类反向验证）。

---

## D. 判据与实现同源（第二真相同源/拿种子当证据）

### D1. hitbands_verify.py:64-70 + 92-93 —— 期望值 = 实现同一公式的抄本
```python
def expected_overlap(rects):
    """按页面实测矩形算期望穿透带（物理像素，与 PlayerView 同一公式）。"""
...
check("A2 兄弟布局实测重叠为 0", last is not None and last["top"] == expA[0] and last["bottom"] == expA[1],
```
- **抓不到什么**：oracle 自己承认抄被测代码的公式。公式级的**语义**错
  （比如穿透带该按 mpv 父窗坐标算而两边都按 stage 相对算）两边一起错 → 绿。
  A3 的独立几何（覆盖层在 stage 外）和 B 组场景部分兜底。
- **最小反例**：把实现和测试注释里那条公式同步改错（或本来就是错的）测不出。
  真机 mpv 侧对 band 的用法没有断言（那是 Go/mpv 面，见 CI 节）。
- 建议：期望从 v1 契约（穿透带==浮层在视频矩形内的物理像素高度）直接算，
  不从"两边同一个 round((t.bottom-s.top)*dpr)"。

### D2. evidence_audit.py:202 —— "Go 有单测"= 名字在测试里**被提过**【实】
```python
tested = re.search(r"\b%s\b" % re.escape(name), go_test_text) is not None
```
- 复演验证：`App.Cover` 被判"桥接+Go单测"，命中的是
  `internal/watchstore/store_test.go` 里 `e.Drama.Cover != "https:/..."` —— **结构体字段**，
  不是绑定测试。自检（330 行）更是把 `_ = "PlayerOpen"`（字符串字面量）钉成"算测到"。
- **后果**：等级分布 27 个"桥接+Go单测"含水分；"只有编译级"名单（真机清单的来源）被低估。
- 建议：匹配 `\.Name(` 调用形状，或把等级改名"提及级"；对 27 个人工核一遍调用点。

### D3. i18n_sweep_verify.py:262-264 —— 整页 en 审计的"数据可解释"判据过宽【实】
```python
hit = (PROVENANCE_RX.match(t)
       or next((tok for tok in DATA_TOKENS if tok in t), None)
       or (len(t) == 1 and t in FIRST_CHARS and t))
```
- `DATA_TOKENS` = fixtures.ts+SEED_JS 里**任意长度≥1** 的含中文引号串（228-233 行注释
  自述把下限从 2 降到 1）；判据是**任意子串包含**。258 行注释说"≥3 字段"，代码不是。
- **最小反例**：在 DiscoverView en 态硬编码一个含 "热门"（fixtures.ts:1275 榜名）的中文
  UI 串 → 被归"数据透传" → `check("audit|en 整页无未定性中文节点")`(539) 绿。
  同类可混的短词很多（爱情/编剧/午夜/总榜…）。GLOBAL_ALLOW(121-128) 的 `红果/短剧`
  子串豁免同理。
- 建议：数据判据至少 `t == tok or tok+"·"分隔结构`；对"节点文本 ⊇ 数据 token 且剩余
  部分是英文"的拼接形态单独建模（注释 259-261 描述的正是这种拼接，但实现比描述松）。

### D4. detail_reserve_verify.py:192-194 —— 注释承诺的保护不存在【实】
```python
#   数字（30 分钟）也一起断，将来有人改小间隔却不改文案，这里会红。
check("⑦b tooltip 承诺的是真存在的节奏（30 分钟），不再写未接通",
      "30" in tip_en and "not wired" not in tip_en, tip_en)
```
- tip 来自 i18n.ts:663 `'…每 30 分钟检查一次更新'`（字面量），DetailView.vue:641
  `:title="t('detail.reserveTip')"`。间隔真值是 followCheck.ts:38
  `CHECK_INTERVAL_DEFAULT = 1800`。**两者没有连线**：改常量不改文案 → "30" 还在文案里
  → 这条照绿（红的是 follow_check ② 的 1800 期望，那是另一只脚本、另一种失败）。
- **最小反例**：`CHECK_INTERVAL_DEFAULT = 900`（文案不动）→ detail_reserve 全绿，
  UI 承诺 30 分钟、实际 15 分钟。
- 建议：i18n 键带参 `每 {n} 分钟`，n 由 `CHECK_INTERVAL_DEFAULT/60` 传入；断言直接
  比对实现常量换算值。

### D5. merge_multi_verify.py:118-133 —— 期望取自被测 store 的 getter
```python
expected = page.evaluate("... pinia._s.get('downloads').mergeGroups ?? []).map((g) => g.dramaId ...")
...
check("④ 两部剧各有一条在排的合并（id 集合与可合并分组一致）",
      len(active) == 2 and active_ids == sorted(set(expected)) and ...
```
- "排进去的必须是勾选那两部"——但期望是**被测代码自己的 mergeGroups**，不是"勾选的两行"。
  今天 mock 恰好只有 2 组（groups_before==2 钉住）才无害；fixtures 加第三组时①红（吵闹），
  所以这是潜伏的"自证"形状而非现行假绿。建议：expected 从 DOM 勾选行取
  （`CHECKS` 勾选集合的 data 属性），别问 store。

### D6. downloads_statbar_verify.py:133-136 ①b —— 内部自洽当外证
`counts.get("") == group_sum == rowCount` 是 app 自己的 chip 计数 vs 自己的行数，
只能抓"两套数打架"，抓不住"一起数错"。作为 UI 自洽门合格，别当计数正确性证据。

### D7. session_state.py:99-116 —— norm() 里死代码（比较器的"幽灵副本"）
`norm` 96-97 行 `return` 之后挂着一整段永不执行的 `if args.check:` 复制体（引用它作用域
里根本没有的 `args`/`body`）。现行活逻辑在 main() 202-215。**风险**：下次有人"修比较器"
改错副本，改完 --check 行为纹丝不动。**建议**：删掉死块。（另：`commits()` 41 行在
git 报错时静默回 0 —— 因该行不参与比较，暂无害。）

---

## E. 空过 / 异常吞掉

### E1. run_all.py:313-314 —— "exit 0 但一条没测"不设防【实】
```python
if res["code"] != 0 or res["failed"]:
    bad.append(res)
```
- `code==0 && PASS==0 && FAIL==0` 计入"exit0 n/n"。第 58 轮的病（工具 exit 0 啥也不干）
  在回归器层没有下限断言（`printed>0` 或声明-打印对账，后者还默认关，见 B1/B2）。
- **最小反例**：任一 `*_verify.py` 的 main 开头 `return 0` —— 全量报表照样"exit0 30/30"。
- 建议：`if res["code"] == 0 and printed == 0 and res["declared"] > 0: bad`；再加
  "每条脚本 printed>=1" 地板。

### E2. 脚本层 except 普查【实：51 处 `except Exception`，逐条分类】
- **有害的活吞**：只有 A10（archive ⑥b 双支豁免）。
- **死代码吞**：searchall_verify.py:75-88、suggest_verify.py:73-86、
  rankings_verify.py:65-78 的 `kill_group`（except 全吞）——这些 helper 在改用
  _devserver 后**没人调用**，是残留（连同 port_open/wait_port）。不害判据，但害读者
  （"以为脚本还自己管进程"）。
- **有兜底的吞**：`_devserver.py:72/83/87/90/94`（就绪探测重试 + 幂等收口，设计如此，
  收口失败另有 99-102 行 raise）；detail_download_verify.py:102-105 queue_len 坏 JSON
  → -1 → 断言红；next_season:158 / esc:116 吞后判 False；danmaku/statbar 的
  `EXEC if exists else None` 是浏览器回退不是吞。
- **check(name, True) 跟随 wait 的形状**（archive 155/162、rankings 133、reserve_calendar
  113/119、sorani 150）：牙齿在 wait 本身（超时→异常→非零退出），PASS 行只是记账。
  注意它们**双计**进 declared 口径；且 wait 的谓词字符串一旦和 check 名字/详情漂移，
  没有任何东西保证两者说同一件事——低危，建议 wait 成功后立刻做真断言而不是 True。

### E3. console/pageerror 守卫覆盖不均【实：grep 计数】
`player_verify.py:107` 甚至挂着 `page.on("console", lambda m: None)`（显式丢弃）。
30 只脚本里只有 10 只（searchall/rankings/discover_hero/creator/suggest/
calendar_paging/theme_contrast/player(丢弃)/merge_multi/danmaku）有任意
pageerror/console 监听；**其余 ~20 只全程 JS 异常静默**（只有症状撞上断言才红）。
建议：公共件里给每只脚本默认装 `pageerror → 末条 FAIL` 的收集器（danmaku ⑥/⑧ 的形状）。

---

## F. 静态解析器的形状盲区

### F1. i18n_key_audit.py:54+69 —— 带连字符的键整族隐形【实·已构造反例】
```python
KEY_DEF_RE = re.compile(r"(?m)^\s{2}'([\w.]+)'\s*:")
PURE_LITERAL_RE = re.compile(r"^['\"]([\w.]+)['\"]$")
```
- 现行活体：`source.note.huangguo-video`（i18n.ts:580/1259，stores/app.ts:15 →
  SourceCard.vue:28 `t(props.source.note)`）。对账结果：audit 抓到 zh/en 各 614 键，
  宽容正则各 615 —— **就差这个带 `-` 的**。它既不进 zh_set/en_set（方向 B 看不见），
  字面量取键也不认（进 dynamic 兜"看不清"）。
- **实测反例**：内存里删掉 en 侧那一行 → `only_zh == []`、`missing == []`，
  run_all 硬门（run_all.py:254-261）绿；英文界面直接把 `source.note.huangguo-video`
  显示到设置页。这正是第 44 轮立这道门要拦的事故形，换个键名形状就绕过。
- **建议**：两处正则的字符类补 `-`（KEY_SHAPE_RE 151 行本来就允许，三处口径先对齐）；
  并给 audit 加"键数地板"（宽容计数 vs 严格计数不等就 FAIL）。

### F2. i18n_static_scan.py:468-491 —— `main()` 在自己的失败路径上 NameError【实】
```python
471    hits = exemption_hits()
...
476            if not args.rebase:        # args 在 491 行才 parse
...
482    if pairs and not args.rebase:
```
- 一旦"豁免条款失效"或"字典 pairs"报警（正是该修的时候），脚本先炸 NameError：
  --rebase 的逃生门在那个状态下不可用，FAIL 报告只剩半截。方向不静默（非零），
  但工具坏在**唯一需要它的分支**上。
- 建议：把 argparse 挪到函数顶部。

### F3. core_action_audit.py:100-104 —— 路由表正则靠"吸进下一张表"侥幸命中【实·复演】
```python
re.compile(r"sourcesActions\s*=\s*map\[string\]bool\{(?P<b>.*?)\n\t\}", re.S),
```
- engine.go:70 的 `sourcesActions` 是**单行表**，`{` 后没有 `\n\t\}`；复演真实文件显示
  它的匹配体一路吞到 discoveryActions 的收尾——两张表的条目并成一份"对"的结果。
  现在数字碰巧对，但：单行表若排在最后、或表被 gofmt 移到顶层（收尾 `\n}` 不是 `\n\t}`）
  → 匹配 0 条 → `table_only_unseen` 静默归零，方向 F"权威名单对账"没牙且没人知道。
  自检 G2 的假源码用的正是"多行表在后"的同一形状，测不出。
- 另 `counts_selfcheck: len(used)==len(acts)`（214 行）**恒真**（used 就是 set(acts)）。
- `parse_production` 每行命中一个 rx 就 `break`（150-154 行）：一行两个 `s.call("a") …
  s.call("b")` 只登记第一个 → b 悄悄缺席 only_real_core（真机名单漏项，静默）。
- 建议：像 evidence_audit 那样用带括号计数的小扫描器抽表；real-repo 复演后加地板
  （三张表各至少 2 条，否则 FAIL）；删掉恒真 selfcheck。

### F4. 注释剥离的"引号奇偶法"是同一把钝刀【实：三处同族】
- ports_audit.py:63 与 palette_audit.py:109 用 `count('"')%2==0` 判 `#`/`//` 在不在串里；
  遇撇号/反引号/转义就错判（`name = "don't"  // 5187` 的注释不会被剥）。第 56 轮修的
  是跨行吞噬，这是同族的下一个形状。目前未构成现行假象【实：对当前仓全量脚本复演
  extract_ports 与声明一致】。
- scan_ts_file:208 `code = line.split("//")[0]`：字符串里含 `//`（URL）的行被拦腰砍，
  砍掉部分里的中文硬编码**不计违规**（假绿方向）。反例：
  `const tip = "详见 https://x 已加入队列失败" ...` 同行后半串的中文漏数。
- evidence_audit.py:119 TRAILING_COMMENT_RE `\s*//[^\n]*$` 也无引号感知：
  同一行里 `//` **之后**的 `t('key')` / `callGo('X')` 会被当注释剥掉 → 方向 A 与
  字面量键抽取双双漏看（假绿方向）。反例（形状成立，现行仓未逐行扫描）：
  `void 0; // 见 http://a  与 callGo('TypoName')` —— 后者本应判红却被剥。

### F5. ports_audit `check_spec`：spec 文件不存在就 SKIP 0（161-163 行）。`.trellis`
被 gitignore → CI 干净检出里恒 SKIP。加上 run_all 225 行把返回值直接扔掉（见 G2），
这条"文档漂移对账"在任何自动化里**永远不会拦任何东西**；且自检注释（330-334 行）
记载它自己曾在假布局上"正确地什么都没测"。

---

## G. "永远闪 / 永远不闪"（自指量与门的可信度）

### G1. 已经"装过一条永远红的门"并拆掉的那条 —— 现在在 VOLATILE 里永不闪
session_state.py:81-88【实】：
```python
# ⚠ 「累计提交」也是易变行，原因不是施工，而是**自指**：
# 这份文件是被某一次提交带进仓库的，那笔提交本身就把计数 +1；
# 于是"文件里写 N"与"HEAD 是 N+1"永远成立，比较器会**永久红**。
"| 累计提交",
```
- 处置正确（拆而不留），代价是：**提交数现在没有任何机器门**（docstring 也这么声明）。
- 同类自指还有 78-80 行的 HEAD/生成时间/未提交条目 —— 都是"施工噪声当待验事实"，
  剥得对。结构性数字（脚本数/端口数/绑定数/键数）仍参与比较【实：selftest A2/A3/A5
  双向钉住，比较器不是摆设】。

### G2. 永远绿（默认不可见/不可达的门）清单
| 门 | 位置 | 为什么永远不闪 |
|---|---|---|
| run_all 静默跳过对账 | run_all.py:194-198, 319 | 默认关；且计数正则漏单行 if 形状（B2） |
| 端口泄漏归因 52xx | run_all.py:36, 41 `51\d{2}`【实】 | 10+ 只脚本用 5201-5212（detail_reserve 5206 … theme_contrast 5212），`declared_ports` 全是空集 → `leaks` 恒空、报表"残留端口"恒 `-`；与 ports_audit.py:34 的 `5[12]\d{2}` 口径漂移 |
| spec 端口表对账（自动） | run_all.py:225 `_ = ports_audit.check_spec(HERE)` + F5 | 返回值扔了 + CI 恒 SKIP |
| SESSION-STATE 对账 | run_all.py:291-293 | rc 只打印，不进 bad |
| 硬编码中文棘轮 i18n_static_scan | 【实：run_all.py 全文无 "static_scan/palette_audit/exemption_probe" 字样；i18n_sweep 只 import 其 SRC 常量、exemption_probe 只读其豁免表——三者的 main() 无人调用】 | run_all/CI 都不跑它的判红主流程；"新增任何一处直接红"（556 行）只在有人手动跑时红 |
| palette_audit undefined_no_fallback | 同上 | 第 59 轮那个静默失效类的静态正门没人自动跑；兜底的 theme_contrast ③（153-154 行）只封**写死的 7 个名字**，新打错的名字（如 `--guo-surfce-3`）两头都不抓【实：banned 是名单不是"未定义"判据】 |
| i18n_exemption_probe | 手动 | 且 ROUTES（43-44 行）没有 /#/player、/#/detail —— 正是历史上"15 处内联中文躲在白名单"的那页；api.ts 的 player: 诊断串豁免条款在该探针的采样面里**永远不可能被 LEAK**（不在采样页面上）【实：对照 ROUTES 与 api.ts 注释】 |
| core_action_audit suspects/table_only_unseen | run_all.py:267-285 | 只 gate stale_fakes/contaminated；"盲区回显"非零不拦路（设计声明） |
| evidence_audit not_called / 等级 | run_all.py:227-240 | 只 gate 方向 A（且 A 的输入形状有 F4/D2 级盲区） |

### G3. 会"偶发红"（不稳定门）的清单
- followCheck FIRST_RUN_DELAY=60s 会打断任何 >60s 的单页会话（followCheck.ts:181-184
  注释自认；follow_check_verify 文档说"最长一只 33s"——**未核实**现状是否仍成立）。
- player ②"8s 自动关"、update V4a 固定 sleep 时序、sorani 多处
  `wait_for_timeout + 计数`——红过概率不为零；本仓自己的教训（"重跑就绿当成通过"，
  build-v2.yml:158-162 注释）适用于它们。
- detail_reserve ⑤：`follow = page.locator("button").filter(has_text="追剧")` ——
  若 en/中混排或按钮顺序变化拿到不同按钮，是假红源（吵闹方向）。

---

## H. CI 侧覆盖缺口（`.github/workflows/build-v2.yml` vs 实际源码）

**H0. 触发面【实】**：`on: workflow_dispatch + push tags v2.*`（build-v2.yml:6-9）。
普通分支 push/PR **没有任何 CI job**。v2-probe.yml 是 dispatch-only spike；build.yml
只管 v1 tag。所以：**下面所有 CI 门只在打 tag/手动时跑；日常提交连 Go 测试都不跑。**

**H1. 29+ 只前端验收脚本不在 CI 里跑**【实：workflow 无 python/verification 步骤】。
"run_all 全量 30/30 绿"完全是本机手工纪律；换人/换机/忘跑，CI 无从知道。

**H2. CI 连类型检查都不跑**【实】：Frontend 步骤只有 `npm run build`（=122 行；
package.json `"build": "vite build"`，typecheck 是另一条 `vue-tsc --noEmit`，**没有
任何工作流调用它**）。第 44 轮"typecheck/build/扫描三道全绿"里的 typecheck 只在本地。

**H3. internal/ 计入门本身可信但有两个形状洞**【实：build-v2.yml:55-95】：
- 判据 = 每包 `^func Test*` 唯一数 vs `^--- (PASS|FAIL|SKIP)` 唯一数 + rc + comm 差集
  ——形状不错；`t.Skip` 计入"执行"是声明过的取舍（53-54 行注释）。
- 洞 1：**没有测试文件的包恒 0/0 通过**——某包唯一测试文件被整删，计入门看不见
  （session_state 的"Go 声明用例数"表能反映漂移，但它在 run_all 里只是 advisory，
  run_all 又不在 CI —— 链条断）。
- 洞 2：循环只走 `go list ./internal/...`；**spike/ 是独立 go.mod**【实】——
  spike/danmaku/ass_test.go、spike/mpvembed/ipc_test.go 任何 CI 都不跑（非产品码，危害低）。

**H4. 根包（App 门面层）是唯一真桥可执行处，覆盖面极窄**【实】：
- windows job 跑 `go test -count=2 -v .` + `ran==declared` 门（163-185 行，形状可信，
  且注释里就是"第五十轮才想起来门没装"的事故记录）。
- 但根包只有 3 个测试文件、**18 条用例**（bindings_clipboard/cover/watchstore = 5+8+5【实】），
  覆盖 clipboard/cover/watchstore 三族门面。
- **87 个 App 绑定**（evidence_audit 实测运行输出）中：
  - `仅桥接（Playwright/mock 覆盖，无 Go 单测）`：**57 个** —— 注意等级名的括号有误导：
    Playwright 全跑在 `VITE_MOCK`（local.ts 的 TS 复刻）上，**桥根本没走**；这 57 个的
    Go 侧证据只有 `go vet + go build` 的**编译级**。JSON 入参解析/错误映射（如
    DownloadStart 的 entries 形状、`[disk_full]` 尾码）在 CI 里从没有被 Go 侧执行过。
  - `桥接+Go单测`：27 个 —— 其中含 D2 的"提及即算"水分（Cover 实锤【实】）。
  - `只有编译级证据`：2 个 —— `BrowseFiltered`（app.go:248）、`TrayStatus`（app.go:162）【实】；
    `MergeList` 有 Go 单测但前端零调用。
- 真桥形状的浏览器侧证据只有 3 只脚本的**测试自有 stub**：detail_download_verify.py
  （STUB_JS 自记 payload）、detail_cover_save_verify.py、preload_openpath_verify.py B 段
  （stub `window.go`）。stub 是第二个真相，验的是"前端↔我发明的桥面"，不是"前端↔Go"。

**H5. exe 级冒烟不在 CI**：没有任何步骤跑 `client-v2.exe --selftest`；internal/selftest
包在 test-linux 有计入门【实：包在 internal 下】，但"发布产物在真 Windows 上能起桥"
只有手动。`build-v2.yml:207-210` 只验 exe 文件存在。

**H6. 前端 mock 面执行不到的 Go 包/绑定汇总**（"既没 Go 单测也没被任何自动执行"）：
- H4 的 57 个"仅桥接"绑定 = App 门面层无执行；其中 mpv 交互（PlayerSeek/画质 resolve/
  sub-reload）、danmaku 壳层下发、下载器网络面、updater 真下载链 —— 
  **未逐个核实**（要按名字列全表请再跑 `evidence_audit.py --json`），
  但机制成立：mock 面不执行它们，根包 18 测不覆盖它们。
- followCheck 的 `updated>0 → 提示语` 分支：mock 恒不推进旗标（follow_check_verify.py
  头部 ❌ 声明【实】）→ 只有真机；ObserveDrama 的 Go 侧 changed 判定有单测（CI 注释
  55-57 行提到它逼出过真 bug）——但 TS↔Go 的"发现更新"端到端零覆盖。
- detail_copy_verify.py docstring 13-14 行【实】：>120 字折叠样本在 mock 不存在，
  "折叠也复制全文"列真机复验（contextmenu C2 用 DOM 注入部分兜住了菜单侧）。
- detail_reserve ⑧ `episodes<=0→「☆ 预约上线」`分支：mockDetail 恒返回分集，
  造不出来，脚本明说不测【实：docstring 34-36 + 198-200 NOTE】。

---

## I. 如果只能修 5 处（优先级清单）

1. **F1 i18n_key_audit 连字符键隐形**（`[\w.]+`→`[\w.-]+`，两处 + 加键数地板）。
   现行活键（huangguo-video）已经在盲区里跑，硬门对第 44 轮事故形的防护等于名单制；
   一行正则 + 一行地板判据，成本最低、现行假绿实锤。
2. **H0/H1/H2 CI 断链**：至少加一个 PR/push 触发的 job 跑 `typecheck` +
   `ports_audit/evidence_audit/i18n_key_audit --selftest` + 静态类（F2 修好后）
   `i18n_static_scan`；Playwright 面可以后置，但"全部绿=本地口头承诺"必须先降级为
   "至少静态门在 CI 绿"。这是所有其它门的地基。
3. **E1+B1+B2 run_all 的牙齿三件套**：`printed==0 && declared>0` 判 bad；
   declared 计数改 `\bcheck\(` 搜索式；--audit-checks 默认开为"报表列"（不拦路，
   但让少测每周可见，配合它自己 92-94 行的立论）。
4. **G2 端口段漂移**：run_all.py:36/41 的 `51\d{2}` → `5[12]\d{2}` 与 ports_audit:34 对齐；
   顺手把 `--only`/新脚本再入集时 `declared_ports` 空集报警（现在 theme_contrast 5212
   起泄漏谁都看不见）。
5. **D 类"同源/种子"抽验包**：按性价比——D3 收紧数据判据（≥3 字 + 全等/拼接结构），
   A2 player 钳位改真驱动 store，A1 resume 改 `>`，A6 `index>=1` 改对当前集，
   D4 tooltip 接参。这五条共同点：都是"当前全绿状态下真实回归可存活"的活洞。

次级（记录不展开）：D2 等级判据改调用形状、F3 路由表扫描器重写、A10 ⑥b 分环境判支、
E3 pageerror 公共化、F2 argparse 上移、D7 死块清理。

---

## J. 正向对照（这次体检里确认"真的有牙"的）

- run_all --selftest 的 porcelain 还原判据（表驱动、含"staged 不许覆盖"反例，139-158）；
- ports_audit --selftest 对"行尾历史注释"事故形状的钉（254-266）；"每个脚本都要抽到端口、
  至少一个非 _verify 探针"是防判据退化的真地板（293-301）；
- evidence_audit/palette_audit 的结构不认识时**主动炸 + run_all 捕获判红**
  （run_all.py:262-265、palette_audit.py:126-127）—— fail-closed，没降级成"没东西可查所以过"；
- follow_check_verify ②③④⑦⑨⑩：走产品导出的同一实现函数（非复刻），期望值是 v1 常量；
  头部 ❌ 声明诚实；
- session_state --selftest 的双向钉（A2 "结构性数字变了必红"、A5 "第二个字段也必红"），
  比较器被证明不是摆设【实：读形状，未运行】；
- detail_cover_save_verify 的 stub 契约（1600 候选计数、PNG 解码尺寸、picker write/close
  计数、`expect_download` 负分支显式超时=期望）形状扎实；
- theme_contrast_verify（新）的对比度算的是**浏览器计算值**不是源码字面量——方向正确
  （但 ③ 是名单制，见 G2）。
