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

## 还剩什么（没测不等于没做，明说）

`needTrack` / `needCreds` / `captchaFail` 回退 / `status_failure_shows_unknown` /
`usable_accepts_account_only_core` 这五条 v1 有用例、v2 有实现，
但 v2 侧没有对应断言。它们是错误路径与"只有账号没有密码"的可用性判据，
补起来需要在 mock 里加失败注入（`window.__zgj` 那类缝，已有先例）。
本轮补的是滑块几何与真人判定那一族（最容易在重构里被"顺手统一"掉的语义）。
