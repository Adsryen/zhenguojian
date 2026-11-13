# v1 ↔ v2 Sorani 登录滑块对账（第五十五轮）

规格来源不是我的转述，而是 v1 自己的测试：
`zhenguojian/app/zhenguojian/test_sorani_login.py`，361 行、**21 条用例**。
v2 侧是 `components/SliderCaptcha.vue`（v1 `sorani_login.py:52-58` 常量同款）
+ `components/SoraniLoginDialog.vue`，验收脚本 `verification/sorani_verify.py`
（原有 14 条：M1–M8）。

先说清一件事：**v2 的 track 形状其实早就在测**（M8 钉了
首点 down、尾点 up、`t` 单调、`x∈[0,490]`、`bgImageWidth=600`、
`templateImageWidth=110`、`startTime>0`、`stopTime>=startTime`）。
我一开始在本轮的记录里写"14 条里有 0 条钉 track 形状"，
**那句说过头了** —— 翻 M8 的实现才看见它一直在钉。
所以真正的缺口是 M8 没覆盖的四类"形状之外"的语义，见下表。

## 逐条对账

| v1 用例 | v2 实现 | 之前是否已测 | 现在 |
|---|---|---|---|
| `:168 roundtrip_and_garbage` | v2 侧不存在同名机制（这是 v1 图像编解码的往返测试） | — | 不适用：Go 侧做图，不在滑块组件里 |
| `:188 display_geometry` | `DISPLAY_MAX_WIDTH=300`/`dispH`/`tplDispW`/`limitDisp` | ⚠ 半（M8 只看 600 空间口径） | 由 M10 的 `max x=490` 间接钉住显示→原图换算 |
| `:196 drag_yields_go_shape_track` | `onUp` 组装 7 个键 | ❌ **键集合从没被钉**（M8 只测其中几个**值**；多一个/少一个键现有断言抓不到，而 Go `valid()` 是按契约校验的） | ✅ M9 断言键集合**正好等于**那 7 个 |
| `:229 x_clamps_to_limit_y_does_not` | `normX()` 钳 x；y 原样 | ❌ 没测（尤其"y **不该**被钳"这一半） | ✅ M10：`max x=490` 且 `min y < 0`（拖出去仍保留负抖动） |
| `:236 short_drag_discarded` | `REJECTED_MIN_OFFSET = 2` + `points < 2` → `reset()` | ❌ 没测 | ✅ M11/M11b：拖 1px 不进账密页，且把手复位 |
| `:246 press_outside_handle_ignored` | `onDown` 只挂在把手上 | ❌ 没测 | ✅ M12：从轨道右端外按下再拖，整段忽略 |
| `:253 point_cap_4000` | `TRACK_LIMIT_POINTS = 4000` | ❌ 没测 | ✅ M13：600 步拖动点位仍在 `[2, 4002]` |
| `:280 lazy_status_on_show` / `:388 row_mounted_lazy_status` | 状态按需读（`soraniStatus`） | ✅ M1/M2 | 不变 |
| `:287 full_login_flow_and_payload` | 全流程 | ✅ M3/M4 | 不变 |
| `:303 captcha_then_credentials_order` | 页序 status→captcha→creds | ✅ M3/M4 | 不变 |
| `:311 submit_without_captcha_restarts_flow` | `needTrack` | ❌ 没测 | 本轮没补（见末尾"还剩什么"） |
| `:321 missing_credentials_blocked_locally` | `needCreds` | ❌ 没测 | 同上 |
| `:328 captcha_fail_falls_back_to_main_with_error` | `captchaFail` + 回 status | ❌ 没测 | 同上 |
| `:334 login_rejected_reissues_captcha` | `submit()` 的 `beginCaptcha()` | ✅ M5（含账密保留） | 不变 |
| `:356 logout` | `soraniLogout` | ✅ M6 | 不变 |
| `:397 logged_in_chip_and_logout_button` | 行内徽标/登出 | ✅ M4/M6 | 不变 |
| `:404 status_failure_shows_unknown` | `sorani.unknown` | ❌ 没测 | 同上 |
| `:411 usable_accepts_account_only_core` | 只有账号无密码可用 | ❌ 没测 | 同上 |
| `:425 make_sorani_dialog_wired` / `:436 populate_twice_keeps_single_account_row` | 装配/重复装配去重 | — | Qt 装配语义，web 无对应机制 |

## 本轮补上的（`sorani_verify.py` M9–M13）

- **M9** track 键集合正好 7 个：
  `['bgImageHeight','bgImageWidth','startTime','stopTime','templateImageHeight','templateImageWidth','trackList']`；
- **M10** `x` 钳到 490、`y` **不钳**（`min y = -18`，拖出上界仍留负抖动）——
  这一半最容易在"顺手加个 clamp"时被抹掉，而抹掉就等于伪造真人轨迹；
- **M11/M11b** 拖 1px 丢弃、把手复位；
- **M12** 从把手外面按下再拖，整段忽略；
- **M13** 600 步拖动点位仍在 `[2, 4002]`（`TRACK_LIMIT_POINTS=4000` 生效）。

## 本轮我在验收脚本上犯的错（都记下来，因为它们比产品错更常见）

1. **补丁脚本写出来却没执行**：我先写 `_p55b.py`，转去做别的，
   接着只跑了另一段改 testid 的补丁 —— 于是"已经加了 M9–M13"完全是错觉，
   而脚本仍报 `8/8 PASS`。**"8/8"这个数字本身就是线索**：我加了 6 条，总数不动就说明没跑进去。
   以后加完断言第一件事是看**总数有没有变**，不是看有没有绿。
2. **代码插在 `ctx.close()` 之后**：`TargetClosedError`。
   而且这类错的表现可能是"少跑几条但整体还绿"，所以我把新段落改成自带 context。
3. **testid 猜错**：`sorani-id`/`sorani-pw` 不存在（真名 `sorani-identifier`/`sorani-password`），
   而 `sorani-login-btn`（行内开框）与 `sorani-start-btn`（框内「开始验证」）名字太像，
   我把流程状态假设成"已关窗"，导致 M11 等不到入口。
   另外 replace 时只改了两处、漏了缩进不同的第三处 —— 又一个"改一处忘另一处"。
4. **用 `timeout` 打断长跑脚本泄漏 dev server**（本轮第三次）：
   `端口 5188 已被占用`。以后一律 `setsid nohup … &` 后台跑 + 按 pid 等，
   不用前台 timeout。
5. **本轮开头我写"14 条里有 0 条钉 track 形状"** —— 翻 M8 才发现一直在钉。
   说"没测"和说"测了"一样需要证据。

## 第五十六轮：那五条错误路径也补完了（M14–M18b，22/22）

| v1 | v2 的做法 | 断言 |
|---|---|---|
| `:311` 无轨迹提交该退回滑块流程 | v2 的 creds 页**只能**由 `SliderCaptcha` emit solved 进入，且 `beginCaptcha()` 会把 `track` 置 null（SoraniLoginDialog.vue:53）—— 那个状态在 v2 **不可达**，我没有为了凑断言硬造它 | M14 改测同一保证的可达形式：没拖过滑块就看不到账密页 |
| `:321` 空账密本地拦下、不打核心 | 已有 | M15 用 mock 侧 `__zgjSoraniCalls.login` **计数**（上一版只能靠时间差猜）；M15b 红字是本地化句子而非键名/裸 code；M15c 本地拦下不顺手重取验证码 |
| `:328` 取验证码失败 → 回状态页 + 红字 | 已有 | M16 + M16b（新缝 `mockCaptchaFail`） |
| `:404` 读状态失败 → 「未知」不谎报未登录 | 已有 | M17（新缝 `mockStatusFail`） |
| `:411` 核心只给账号没有 name | 已有，但**数据源有两条** | M18（新缝 `mockSoraniNoName`，`soraniStatus` 与 `soraniLogin` 都要认） |

三个注入缝的错误消息一律写成 **ASCII**：它们会作为 `{msg}` 嵌进
`sorani.captchaFail`/`sorani.statusFail` 的句子，中文会在英文界面露出来
（第 42 轮立的"话术按 code 组装、中文只进日志"）。不设缝时默认行为一字不变。

**反向验证**：把三个注入条件临时改成 `false` → M16/M17/M18 全红，
其中 M17 红成 `chip='未登录'` —— 正是这条断言要防的那种谎报。

两个我自己的错值得记：
1. M16 连错两版：先点 `sorani-creds-back`（只切页、**不重取验证码**），
   再改点 `sorani-reverify-btn` 却以为它在滑块页（它在**账密页**，SoraniLoginDialog.vue:186）
   → 等元素超时 30s。两次都是**按名字猜归属**，而不是读那段 `v-show` 在哪个块里。
2. M18 第一版只在 `soraniStatus` 上打缝，而登录后徽标用的是 `soraniLogin` 的返回值 → 测不到。

一条登记未动的观察：mock 里 `throw new ApiError(t('api.errCaptcha'), …)`
是"抛的时候就把句子翻译好"，与"只带 code、由界面组装话术"的约定不一致 ——
locale 变了这句不会跟着变。改动会牵动多处既有断言，登记着不夹带。

## 顺带修的两个验收装置缺陷（第五十六轮）

1. **`sorani_verify.py` / `update_verify.py` 根本没有 `try/finally`**：
   中途抛异常（本轮就是 Playwright 超时）就留下孤儿 vite 占住端口，
   下一轮撞「端口 5188 已被占用」，且 `run_all` 的泄漏检测会把残留监听
   **误归属**给别的脚本。而 `sorani_verify.py` 头部注释还写着
   「收口是 SIGTERM→wait→SIGKILL→回查端口」——
   **声明了一个代码里并不存在的保证**，这正是我这几轮一直在抓的那类错，这次在我自己的装置里。
2. 修法不是逐个脚本补 `finally`，而是在 `_devserver.start_dev()` 成功返回前
   `atexit.register(stop_dev, proc, port, name, True)`：
   靠"作者记得写"的规矩一定会再漏，注册在起服务器的那一刻才是结构性的。
   `stop_dev` 本身幂等，脚本显式调用不冲突；`quiet_port=True` 是因为
   解释器退出阶段不该因为"端口没关干净"再抛一次、把真正的失败信息盖掉。
   实测：故意起一台 5215 然后抛异常 → 端口无监听、无残留进程；全量 29/29、0 泄漏。

## 还剩什么

v1 那 21 条里，`:168`（v1 图像编解码往返）与 `:425/:436`（Qt 装配/重复装配去重）
在 v2 没有对应机制，不硬造成断言；其余都已有归属。
滑块几何之外**真正还没覆盖**的只剩一条：`dispW/tplDispW` 随图片实际尺寸变化的
换算（v1 `:188` 只测固定 300/55），需要 mock 给出非默认尺寸的验证码图。

> ↑ 这句已在**第五十七轮作废**：那条补齐了（M19–M21b），过程见下面两节。

## 第五十七轮：变尺寸换算补上，而反向验证抓出我一条近恒真的断言

新缝 `__zgj.mockCaptchaSize = [bgW, bgH, tplW, tplH]`（默认值一字不改），
换 400×200 / 模板 50 进来测同一套换算：

| 断言 | 判的事 |
|---|---|
| M19 | 显示宽夹到 300（`dispW = min(300, bgW)`） |
| M20 | 显示高 150、模板显示宽 38 —— 按**同一比例** |
| M21 | 原图 x ≈ 250×400/300 = **333**（按比例算出来，而不是撞上钳位上限） |
| M21b | 拖过轨道上限时 x = **349 ≤ 350**（这组的 `bgW-tplW`，不是默认的 490） |

### 最重要的不是这四条，是这条

M21 我**第一版**写成 `300 < max x <= 350`。做反向验证时把 `scale` 临时写死成 2
（= 默认那组的比例，等于把换算改坏）——**整只脚本仍然 25/25 全绿**。
因为坏实现下 x 被钳到 350，而 350 正好落在我给的区间里。

判据把「撞上钳位上限」与「按正确比例算出来」当成同一件事，
而那恰恰是坏实现的表现形状。改成 `|max x - 333| <= 6 且 max x < 350` 之后：
坏实现红（max x=350）、好实现绿（333）。

⚠ 如实写：**M21b 对 `scale` 这类错不敏感**（349 与 350 都满足 `<=350`），
所以只有 M21 抓得到它。两条断言的覆盖面不等，不假装"两条都有牙"。
另记一句：写死 `scale` 那次 **typecheck 照样 0** —— 这类换算错静态抓不到，只能靠断言。

### 三条装置错（会重复出现，所以记全）

1. `wait_for_selector(sorani-slider)` 等的是**容器**，
   它在"验证码加载中"分支里也存在 → 读到 `tplW/bgW/bgH` 全是 None，
   看起来像"产品没做换算"，实际是断言跑在加载态上。改成等 `sorani-bg-img`。
2. M21b 想"再来一次"点 `sorani-reverify-btn`，
   而 M21 提交成功后对话框**已经关了**（mock 返回成功 → `emit('close')`）→ 等元素超时。
   改成独立开一个上下文。
3. 第一版拖 600px 想"必然越界"，结果**拖出轨道导致 solve 不成立**、creds 永不出现，
   我一度以为钳位坏了。上限测试该拖的是"刚好越过 `limitDisp`"（262→拖 265）。

## 现在 Sorani 面的覆盖状况

v1 21 条里：**16 条已对上**（滑块几何与真人判定 5 + 错误路径 5 + 主流程与状态 6），
**3 条 v1 专属机制不适用**（`:168` 图像编解码往返、`:425/:436` Qt 装配与重复装配去重），
**2 条明确未覆盖**：`:188` 里"控件在窄宽下适配"的**布局**那一半
（本轮只测数值换算，没测视觉排布），以及 `:280/:388` 的"按需读状态"时序细节
（v2 有实现，只在 M1/M17 中间接碰到）。

## 流程错（第四次同一个）

这笔的提交信息第一版用 `git commit -m "...反引号..."` 写，
shell 把反引号里的内容当命令执行，于是 `test_sorani_login.py` 与
`300 < max x <= 350` 从提交信息里**消失**，还多报两条 command not found。
第 43、54 轮都记过并立过规矩（"带反引号一律走 `-F heredoc`"），这是第四次，
而且发生在刚写完"证据来源不是我的转述"的一笔里。
已 `--amend` + heredoc 重做，并把"装一个 commit-msg 钩子机械拒绝"挂成 **R33** ——
第四次之后，不该再靠我记得。
