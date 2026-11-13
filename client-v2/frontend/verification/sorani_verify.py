#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""sorani_verify —— v2-M2 青空次元登录（mock 面）端到端验证（Playwright + dev:mock 5188）。

断言：
  M1 设置「站源账号」行渲染 + 状态=未登录（进页 soraniStatus() 成功路径）。
  M2 点登录 → 对话框三态之状态页 → 开始验证 → 滑块页两图渲染（naturalWidth 600/110）。
  M3 page.mouse 真实按下-移动-释放拖动 → 账密页出现。
  M4 填账密提交（默认成功）→ 对话框关闭 + 行状态「已登录」。
  M5 window.__zgj.mockLoginFail='1' → 提交失败：红字（登录失败+安全验证未通过）+
     自动回滑块页重取 + 已填账密保留（input value 断言）。
  M6 登出 → 行状态回「未登录」。
  M7 en locale：站源账号区 + 对话框（状态/滑块页）零 CJK。
  M8 __zgjSoraniLastLogin 捕获的 track：首点 down(0,0,t=0)、尾点 up、t 单调不减、
     x∈[0,490]、bgImageWidth=600、templateImageWidth=110、startTime>0、stopTime>=startTime。
截图：sorani-captcha.png（M2 对话框滑块页）、sorani-row.png（M4 已登录行）。

用法：python3 verification/sorani_verify.py
自管理 dev:mock（独占 5188，端口预检 + 严格收口，见 _devserver.py）。
"""
import asyncio
import os
import re
import socket
import subprocess

import _devserver
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = 5188
BASE = f"http://127.0.0.1:{PORT}"
EXEC = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
)
CJK = re.compile(r"[\u3000-\u303f\u3400-\u4dbf\uf900-\ufaff\u4e00-\u9fff\uff01-\uff60]")

RESULTS = []


def check(name, ok, detail=""):
    tag = "PASS" if ok else "FAIL"
    RESULTS.append((tag, name, detail))
    print(f"[{tag}] {name}" + (f"  — {detail}" if detail else ""))
    return ok


def port_open(host, port):
    with socket.socket() as s:
        s.settimeout(0.5)
        return s.connect_ex((host, port)) == 0


async def press_outside_then_drag(page, track_sel, handle_sel, dx_total, steps=12):
    """v1 test_press_outside_handle_ignored：从把手**外面**按下再拖，
    整段必须被忽略（不该把轨道当成把手来拖）。"""
    tb = await page.locator(track_sel).bounding_box()
    hb = await page.locator(handle_sel).bounding_box()
    assert tb and hb
    await page.mouse.move(tb["x"] + tb["width"] - 4, tb["y"] + tb["height"] / 2)  # 靠右端，离把手远
    await page.mouse.down()
    for i in range(1, steps + 1):
        await page.mouse.move(tb["x"] + tb["width"] - 4 - i * 8, tb["y"] + tb["height"] / 2)
        await page.wait_for_timeout(12)
    await page.mouse.up()


async def tiny_drag(page, handle_sel, dx=1):
    """v1 test_short_drag_discarded：只拖 1px 不该发 track（真人判定不过）。"""
    await real_drag(page, handle_sel, dx, steps=2, dy_jitter=0)


async def many_point_drag(page, handle_sel, dx_total, steps):
    """v1 test_point_cap_4000：造出超过上限的移动事件，看点位是否被夹住。"""
    await real_drag(page, handle_sel, dx_total, steps=steps, dy_jitter=1)


async def real_drag(page, handle_sel, dx_total, steps=20, dy_jitter=1):
    """page.mouse 真实事件链：按下 → 分步移动 → 释放（服务端拒纯合成瞬移）。"""
    box = await page.locator(handle_sel).bounding_box()
    assert box, f"{handle_sel} not visible"
    sx, sy = box["x"] + box["width"] / 2, box["y"] + box["height"] / 2
    await page.mouse.move(sx, sy)
    await page.mouse.down()
    step = dx_total / steps
    for i in range(1, steps + 1):
        await page.mouse.move(sx + step * i, sy + (dy_jitter if i % 3 == 0 else 0))
        await page.wait_for_timeout(16)
    await page.mouse.up()


async def main():
    from playwright.async_api import async_playwright

    # 启停走 _devserver：进门先断言端口为空（不再「复用已在跑的 5188」），
    # 收口是 SIGTERM→wait→SIGKILL→回查端口。旧写法 Popen 没有 start_new_session、
    # 结束只 terminate()，本轮实测泄漏过一次 5188。
    spawned = _devserver.start_dev(FRONTEND, PORT, True, HERE, name="sorani")

    async with async_playwright() as pw:
        browser = await pw.chromium.launch(executable_path=EXEC, args=["--no-sandbox"])

        # ================= 页 1（zh）：M1-M6 + M8 =================
        ctx = await browser.new_context(viewport={"width": 1280, "height": 900})
        page = await ctx.new_page()
        await page.goto(f"{BASE}/#/settings")
        await page.wait_for_selector("[data-testid='sorani-row']", timeout=15000)

        # ---- M1 ----
        badge = page.locator("[data-testid='sorani-status']")
        # 状态是异步读核心的：行渲染出来时徽标先显示「未知」（读不到不谎报未登录，见
        # SettingsView:340），读完才是「未登录」。旧写法在 wait_for_selector(row) 之后
        # 立刻取文本，是时序赌运气：HEAD 恰好赢，本轮 api.ts 多一个 i18n 依赖后首帧
        # 变成「未知」就红了。改成等落定，且落不到仍然红。
        try:
            await page.wait_for_function(
                "() => document.querySelector('[data-testid=sorani-status]')"
                "?.textContent.trim() === '未登录'", timeout=8000)
            m1_text = "未登录"
        except Exception:
            m1_text = (await badge.inner_text()).strip()
        check("M1 站源账号行 + 未登录", m1_text == "未登录", m1_text)

        # ---- M2 登录 → 状态页 → 滑块页两图 ----
        await page.click("[data-testid='sorani-login-btn']")
        await page.wait_for_selector("[data-testid='sorani-page-status']", timeout=8000)
        await page.click("[data-testid='sorani-start-btn']")
        await page.wait_for_selector("[data-testid='sorani-bg-img']", timeout=8000)
        dims = await page.evaluate(
            """() => { const b = document.querySelector('[data-testid=\\'sorani-bg-img\\']');
                       const t = document.querySelector('[data-testid=\\'sorani-tpl-img\\']');
                       return { bw: b && b.naturalWidth, tw: t && t.naturalWidth,
                                vis: b && t && b.getBoundingClientRect().width > 0 && t.getBoundingClientRect().width > 0 } }"""
        )
        check(
            "M2 滑块背景图+模板图渲染(600/110)",
            dims.get("bw") == 600 and dims.get("tw") == 110 and dims.get("vis"),
            str(dims),
        )
        await page.locator("[data-testid='sorani-dialog']").screenshot(
            path=os.path.join(HERE, "sorani-captcha.png")
        )

        # ---- M3 真实鼠标拖动 → 账密页 ----
        await real_drag(page, "[data-testid='sorani-handle']", 200)
        try:
            await page.wait_for_selector("[data-testid='sorani-page-creds']:visible", timeout=6000)
            check("M3 真实拖动→账密页", True)
        except Exception:
            err = ""
            try:
                err = (await page.inner_text("[data-testid='sorani-error']")).strip()
            except Exception:
                pass
            check("M3 真实拖动→账密页", False, f"creds 页未出现; error={err}")

        # ---- M4 提交成功 → 关窗 + 已登录 ----
        await page.fill("[data-testid='sorani-identifier']", "demo")
        await page.fill("[data-testid='sorani-password']", "pw1234")
        await page.click("[data-testid='sorani-submit-btn']")
        try:
            await page.wait_for_selector("[data-testid='sorani-dialog']", state="detached", timeout=8000)
            m4_text = (await page.locator("[data-testid='sorani-status']").inner_text()).strip()
            check("M4 提交成功→关窗+行已登录", m4_text.startswith("已登录"), m4_text)
        except Exception:
            err = ""
            try:
                err = (await page.inner_text("[data-testid='sorani-error']")).strip()
            except Exception:
                pass
            check("M4 提交成功→关窗+行已登录", False, f"对话框未关; error={err}")
        await page.locator("[data-testid='sorani-row']").screenshot(
            path=os.path.join(HERE, "sorani-row.png")
        )

        # ---- M8 stub 捕获 track 形状（取 M4 成功那次登录入参快照） ----
        tr = await page.evaluate("() => window.__zgjSoraniLastLogin || null")
        if not tr:
            check("M8 track 快照存在", False, "__zgjSoraniLastLogin 未写入")
        else:
            pts = tr["track"]["trackList"]
            first, last = pts[0], pts[-1]
            xs = [p["x"] for p in pts]
            ts = [p["t"] for p in pts]
            t_mono = all(b >= a for a, b in zip(ts, ts[1:]))
            ok8 = (
                tr["track"]["bgImageWidth"] == 600
                and tr["track"]["templateImageWidth"] == 110
                and tr["track"]["bgImageHeight"] == 360
                and tr["track"]["templateImageHeight"] == 360
                and first["type"] == "down"
                and first["x"] == 0
                and first["y"] == 0
                and first["t"] == 0
                and last["type"] == "up"
                and len(pts) >= 2
                and t_mono
                and all(0 <= x <= 490 for x in xs)
                and tr["track"]["startTime"] > 0
                and tr["track"]["stopTime"] >= tr["track"]["startTime"]
                and tr["identifier"] == "demo"
            )
            check(
                "M8 track 形状(down/up/t单调/x∈[0,490]/600空间)",
                ok8,
                f"n={len(pts)} x∈[{min(xs)},{max(xs)}] t_mono={t_mono}",
            )

        # ---- M5 mockLoginFail='1' → 失败：红字 + 回滑块页 + 账密保留 ----
        # M4 成功后行按钮已切成「登出」；先登出（兼 M6 前置）露出「登录」按钮
        await page.click("[data-testid='sorani-logout-btn']")
        await page.wait_for_selector("[data-testid='sorani-login-btn']", timeout=8000)
        await page.evaluate("() => { window.__zgj = window.__zgj || {}; window.__zgj.mockLoginFail = '1' }")
        await page.click("[data-testid='sorani-login-btn']")
        await page.wait_for_selector("[data-testid='sorani-start-btn']", timeout=8000)
        await page.click("[data-testid='sorani-start-btn']")
        await page.wait_for_selector("[data-testid='sorani-bg-img']", timeout=8000)
        await real_drag(page, "[data-testid='sorani-handle']", 180)
        await page.wait_for_selector("[data-testid='sorani-page-creds']:visible", timeout=6000)
        await page.fill("[data-testid='sorani-identifier']", "keepme@ex.com")
        await page.fill("[data-testid='sorani-password']", "secret99")
        await page.click("[data-testid='sorani-submit-btn']")
        try:
            await page.wait_for_selector("[data-testid='sorani-error']", timeout=8000)
            m5_err = (await page.inner_text("[data-testid='sorani-error']")).strip()
            m5_back = await page.locator("[data-testid='sorani-page-captcha']").is_visible()
            kept_id = await page.input_value("[data-testid='sorani-identifier']")
            kept_pw = await page.input_value("[data-testid='sorani-password']")
            check(
                "M5 失败→红字+回滑块+账密保留",
                "登录失败" in m5_err and "安全验证未通过" in m5_err and m5_back
                and kept_id == "keepme@ex.com" and kept_pw == "secret99",
                f"err={m5_err[:60]} back={m5_back} id_kept={kept_id}",
            )
        except Exception as e:
            check("M5 失败→红字+回滑块+账密保留", False, repr(e))

        # ---- M6 清标志后凭保留账密重拖重提 → 成功关窗；行登出 → 未登录 ----
        await page.evaluate("() => { if (window.__zgj) delete window.__zgj.mockLoginFail }")
        await real_drag(page, "[data-testid='sorani-handle']", 160)
        await page.wait_for_selector("[data-testid='sorani-page-creds']:visible", timeout=6000)
        await page.click("[data-testid='sorani-submit-btn']")
        await page.wait_for_selector("[data-testid='sorani-dialog']", state="detached", timeout=8000)
        m6a = (await page.locator("[data-testid='sorani-status']").inner_text()).strip()
        await page.click("[data-testid='sorani-logout-btn']")
        try:
            await page.wait_for_function(
                "() => (document.querySelector('[data-testid=\\'sorani-status\\']')||{}).textContent === '未登录'",
                timeout=8000,
            )
            check("M6 重试成功→已登录→登出→未登录", m6a.startswith("已登录"), f"retry_badge={m6a}")
        except Exception:
            txt = (await page.locator("[data-testid='sorani-status']").inner_text()).strip()
            check("M6 重试成功→已登录→登出→未登录", False, f"retry_badge={m6a} final_badge={txt}")
        await ctx.close()

        # ============ M9–M13：v1 用测试钉住的滑块语义（自带一个干净 context） ============
        # ⚠ 第一版这段我插在 `ctx.close()` **之后**，于是 Page.evaluate 直接
        #   TargetClosedError —— 而更糟的是它当时的表现可以是"少跑几条但整体还绿"。
        #   现在自己开 ctx3/pc，与 M7 的 ctx2 同一个套路，不再依赖前面的页面活着。
        ctx3 = await browser.new_context(viewport={"width": 1280, "height": 900})
        pc = await ctx3.new_page()
        await pc.goto(f"{BASE}/#/settings")
        await pc.wait_for_selector("[data-testid='sorani-section']", timeout=15000)
        await pc.click("[data-testid='sorani-login-btn']")
        await pc.wait_for_selector("[data-testid='sorani-start-btn']", timeout=8000)
        await pc.click("[data-testid='sorani-start-btn']")
        await pc.wait_for_selector("[data-testid='sorani-slider']", timeout=8000)

        # ============ M9–M13：v1 用测试钉住的滑块语义（M8 只测了字段值） ============
        # 第五十五轮对账时发现：v1 test_sorani_login.py 有 21 条用例，
        # 其中四条属于"轨道形状之外"的滑块语义，v2 一条都没测：
        #   · track 的**键集合必须正好是那 7 个**（M8 只测了其中几个值，
        #     多塞一个键或少一个键都不会被现有断言抓到，而 Go valid() 按契约校验）；
        #   · x 钳到 limit、**y 不钳**（y 是相对按下点的原始抖动，钳了就等于抹掉真人轨迹）；
        #   · 只拖 1px 应当丢弃（真人判定不过）；
        #   · 从把手外面按下再拖应当整段忽略。
        # 另加一条上限对账（v1 test_point_cap_4000）。

        # ---- M9/M10：键集合 + x 钳位 y 不钳 ----
        await pc.evaluate("() => { window.__zgjSoraniLastLogin = null }")
        # 往**右端外**拖：x 应该被钳到 600-110=490，而 dy 用负的抖动，y 应保留负值
        await real_drag(pc, "[data-testid='sorani-handle']", 900, steps=26, dy_jitter=-9)
        try:
            await pc.wait_for_selector("[data-testid='sorani-page-creds']:visible", timeout=8000)
            await pc.fill("[data-testid='sorani-identifier']", "u_m9")
            await pc.fill("[data-testid='sorani-password']", "p_m9")
            await pc.click("[data-testid='sorani-submit-btn']")
            await pc.wait_for_function("() => window.__zgjSoraniLastLogin", timeout=8000)
            t9 = await pc.evaluate("() => window.__zgjSoraniLastLogin")
            track = (t9 or {}).get("track") or {}
            keys = sorted(track.keys())
            want_keys = sorted([
                "bgImageWidth", "bgImageHeight", "startTime", "stopTime",
                "templateImageWidth", "templateImageHeight", "trackList",
            ])
            check("M9 track 的键集合正好是 7 个（v1 test_drag_yields_go_shape_track）",
                  keys == want_keys, f"keys={keys}")
            pts = track.get("trackList") or []
            if pts:
                # x 钳位：末点的 x 不该超过 490；而且拖到 900px 远，说明确实撞到上限
                xs = [q["x"] for q in pts]
                ys = [q["y"] for q in pts]
                check("M10 x 钳到 [0,490]，而 y **不钳**（保留负抖动，v1 test_x_clamps_to_limit_y_does_not）",
                      max(xs) <= 490 and min(ys) < 0,
                      f"max x={max(xs)} min y={min(ys)}")
            else:
                check("M10 x 钳到 [0,490]，而 y **不钳**（保留负抖动，v1 test_x_clamps_to_limit_y_does_not）",
                      False, "trackList 空")
            await pc.wait_for_selector("[data-testid='sorani-dialog']", state="detached", timeout=8000)
        except Exception as exc:
            check("M9 track 的键集合正好是 7 个（v1 test_drag_yields_go_shape_track）", False, repr(exc)[:120])
            check("M10 x 钳到 [0,490]，而 y **不钳**（保留负抖动，v1 test_x_clamps_to_limit_y_does_not）",
                  False, "没走到提交")

        # ---- M11：只拖 1px 应该丢弃，停在滑块页 ----
        # 回到滑块页再测两条"该被忽略的拖动"。
        # ⚠ 上一版这里只等 `sorani-login-btn`，超时了——因为 M10 那一次登录成功后
        #   对话框**还开着**（停在 creds/status 页），此时页面上根本没有行内按钮。
        #   这两个入口名字太像（sorani-login-btn = 行内开框；sorani-start-btn = 框内「开始验证」），
        #   我把状态假设成"已关窗"才导致红。现在按当前界面实际有什么来点：
        #   先关掉可能还开着的对话框，再走行内入口。
        if await pc.locator("[data-testid='sorani-dialog']").count():
            await pc.click("[data-testid='sorani-close-btn']")
            await pc.wait_for_selector("[data-testid='sorani-dialog']", state="detached", timeout=8000)
        # ⚠ 第二次修这条：上一次我只补了"先关窗"，还是等不到 `sorani-login-btn`。
        #   真正的原因是 **M10 那一次登录成功了**：登录成功后行内按钮会变成
        #   「登出」（sorani-logout-btn），`sorani-login-btn` 是 v-if 掉的。
        #   所以这里要先登出回到未登录态，才有入口可点。
        #   （我前两版都在猜"选择器名字对不对"，而该问的是"界面此刻是什么状态"。）
        if await pc.locator("[data-testid='sorani-logout-btn']").count():
            await pc.click("[data-testid='sorani-logout-btn']")
            await pc.wait_for_selector("[data-testid='sorani-login-btn']", timeout=10000)
        await pc.wait_for_selector("[data-testid='sorani-login-btn']", timeout=8000)
        await pc.click("[data-testid='sorani-login-btn']")
        await pc.wait_for_selector("[data-testid='sorani-start-btn']", timeout=8000)
        await pc.click("[data-testid='sorani-start-btn']")
        await pc.wait_for_selector("[data-testid='sorani-slider']", timeout=8000)
        await tiny_drag(pc, "[data-testid='sorani-handle']")
        await pc.wait_for_timeout(900)
        # ⚠ 这里不能用 count()：对话框是 **v-show 三态**（组件头注释自己写着
        #   「三页栈 → v-show 三态」），三个页面永远都在 DOM 里，
        #   count() 恒为 1 —— 我前两版就是被它骗成"产品有 bug"。判可见性才对。
        still_captcha = await pc.locator("[data-testid='sorani-page-creds']:visible").count()
        handle_off = await pc.evaluate(
            "() => { const h=document.querySelector('[data-testid=sorani-handle]');"
            " return h ? Math.round(h.getBoundingClientRect().left) : null }")
        check("M11 只拖 1px 被丢弃，没进账密页（v1 test_short_drag_discarded）",
              still_captcha == 0, f"creds 页出现={still_captcha} 把手 left={handle_off}")
        # 把手该回到原位
        await pc.evaluate("() => document.querySelector('[data-testid=sorani-slider]')?.getBoundingClientRect().x")
        left_now = await pc.evaluate(
            "() => { const h=document.querySelector('[data-testid=sorani-handle]');"
            " const s=document.querySelector('[data-testid=sorani-slider]');"
            " if (!h || !s) return null;"
            " if (s.getBoundingClientRect().width === 0) return null;"
            " return Math.round(h.getBoundingClientRect().left - s.getBoundingClientRect().left) }")
        check("M11b 丢弃后把手复位（offset 回 0；读到 null 算失败，不算没动）",
              left_now is not None and left_now <= 2, f"把手相对左缘={left_now}")

        # ---- M12：从把手外面按下整段忽略（v1 test_press_outside_handle_ignored）----
        await press_outside_then_drag(pc, "[data-testid='sorani-slider']",
                                      "[data-testid='sorani-handle']", 200)
        await pc.wait_for_timeout(900)
        left2 = await pc.evaluate(
            "() => { const h=document.querySelector('[data-testid=sorani-handle]');"
            " const s=document.querySelector('[data-testid=sorani-slider]');"
            " if (!h || !s) return null;"
            " if (s.getBoundingClientRect().width === 0) return null;"
            " return Math.round(h.getBoundingClientRect().left - s.getBoundingClientRect().left) }")
        creds12 = await pc.locator("[data-testid='sorani-page-creds']:visible").count()
        check("M12 把手外按下再拖：整段忽略（没进账密页、把手没动）",
              creds12 == 0 and left2 is not None and left2 <= 2,
              f"creds={creds12} 把手左缘={left2}")

        # ---- M13：点位上限（v1 test_point_cap_4000）----
        await many_point_drag(pc, "[data-testid='sorani-handle']", 240, steps=600)
        await pc.wait_for_timeout(1200)
        creds13 = await pc.locator("[data-testid='sorani-page-creds']:visible").count()
        t13 = await pc.evaluate("() => window.__zgjSoraniLastLogin")
        if creds13:
            # 拖成功进了账密页；点位数量只能在提交时才拿得到，这里用 mock 侧计数
            await pc.fill("[data-testid='sorani-identifier']", "u_m13")
            await pc.fill("[data-testid='sorani-password']", "p_m13")
            await pc.click("[data-testid='sorani-submit-btn']")
            await pc.wait_for_function("() => window.__zgjSoraniLastLogin", timeout=8000)
            t13 = await pc.evaluate("() => window.__zgjSoraniLastLogin")
            n13 = len(((t13 or {}).get("track") or {}).get("trackList") or [])
            check("M13 600 步拖动：点位被夹在上限内且仍发得出（v1 test_point_cap_4000）",
                  2 <= n13 <= 4002, f"点位数={n13}")
        else:
            check("M13 600 步拖动：点位被夹在上限内且仍发得出（v1 test_point_cap_4000）",
                  False, "拖了 600 步却没进账密页")


        # ============ M19–M21 变尺寸换算（v1 :188 test_display_geometry） ============
        # v1 那条测的是"显示空间 300×180 / 模板 55 / limit=245"这组**换算**，
        # 而 v2 的 mock 把 600/360/110 写死了，所以只测过一种尺寸。
        # 现在换个尺寸进来，测同一套换算还成不成立：
        #   背景 400×200、模板 50×200 →
        #     显示宽 = min(300, 400) = 300
        #     显示高 = round(200 * 300 / 400) = 150
        #     模板显示宽 = round(50 * 300 / 400) = 38
        #     limit(显示) = 300 - 38 = 262
        #     原图空间 x 上限 = 400 - 50 = 350
        mg = await browser.new_context(viewport={"width": 1280, "height": 900})
        await mg.add_init_script("localStorage.setItem('zgj.locale', 'zh')")
        await mg.add_init_script("window.__zgj = { mockCaptchaSize: [400, 200, 50, 200] }")
        pg = await mg.new_page()
        await pg.goto(f"{BASE}/#/settings")
        await pg.wait_for_selector("[data-testid='sorani-section']", timeout=15000)
        await pg.click("[data-testid='sorani-login-btn']")
        await pg.wait_for_selector("[data-testid='sorani-start-btn']", timeout=8000)
        await pg.click("[data-testid='sorani-start-btn']")
        # ⚠ 必须等**背景图**，不能等 `sorani-slider`：那个容器在
        #   "验证码加载中"分支里也存在（SliderCaptcha.vue:142 的 v-if 是套在 .sc 内部的），
        #   所以我第一版读到的 tplW/bgW/bgH 全是 None —— 不是产品没换算，
        #   是断言跑在加载态上。等真正渲染出图的那个 testid 才对。
        await pg.wait_for_selector("[data-testid='sorani-bg-img']", timeout=10000)
        geo = await pg.evaluate("""() => {
          const s = document.querySelector('[data-testid=sorani-slider]');
          const t = document.querySelector('[data-testid=sorani-tpl-img]');
          const b = document.querySelector('[data-testid=sorani-bg-img]');
          const num = (el) => (el ? Math.round(parseFloat(getComputedStyle(el).width)) : null);
          const box = (el) => (el ? { w: Math.round(el.getBoundingClientRect().width),
                                      h: Math.round(el.getBoundingClientRect().height) } : null);
          const st = document.querySelector('.sc-stage');
          return { sliderW: num(s),
                   stageW: st ? Math.round(parseFloat(st.style.width)) : null,
                   stageH: st ? Math.round(parseFloat(st.style.height)) : null,
                   tpl: box(t), bg: box(b) };
        }""")
        check("M19 变尺寸下显示空间仍夹到 300 宽（v1 :188 的 dispW 换算）",
              geo.get("sliderW") == 300 and geo.get("stageW") == 300
              and (geo.get("bg") or {}).get("w") == 300,
              str(geo))
        check("M20 显示高与模板显示宽按同一比例换算（400×200→300×150、模板 50→38）",
              geo.get("stageH") == 150 and (geo.get("tpl") or {}).get("w") == 38,
              str(geo))
        # 拖到最右：原图 x 上限该是 400-50=350，而不是默认的 490
        await pg.evaluate("() => { window.__zgjSoraniLastLogin = null }")
        # ⚠ 拖 600px 在这一组尺寸下会**越过轨道本身**
        #   （显示空间 300、limitDisp = 300-38 = 262），指针跑到容器外事件就断了，
        #   于是 creds 永远不出现 —— 我第一版把它当成"钳位没生效"，其实是拖出轨道。
        #   改成拖 250px：仍在轨道内，但对原图空间来说已经越过 350 的上限，
        #   正好检验的是"钳的是这一组的 350，不是默认的 490"。
        await real_drag(pg, "[data-testid='sorani-handle']", 250, steps=24)
        await pg.wait_for_selector("[data-testid='sorani-page-creds']:visible", timeout=8000)
        await pg.fill("[data-testid='sorani-identifier']", "geo")
        await pg.fill("[data-testid='sorani-password']", "pw")
        await pg.click("[data-testid='sorani-submit-btn']")
        await pg.wait_for_function("() => window.__zgjSoraniLastLogin", timeout=8000)
        t21 = await pg.evaluate("() => window.__zgjSoraniLastLogin")
        tr21 = (t21 or {}).get("track") or {}
        xs21 = [q["x"] for q in (tr21.get("trackList") or [])]
        # ⚠ 这条断言我第一版写成 `300 < max x <= 350`，**太松**：
        #   把 scale 写死成 2（= 默认 600/300 的比例，也就是换算坏了）时，
        #   x 会被钳到 350，而 350 照样落在我给的区间里 → 25/25 全绿。
        #   反向验证（临时把 scale 写死）才把这条"近恒真"的断言抓出来 ——
        #   判据不能把"撞上限"和"按正确比例算出来"混为一案，那正好是坏实现的表现。
        #   现在判的是**期望的按比例值**（250 显示 px × 400/300 ≈ 333，容差 ±6），
        #   并且明确要求 max x **没有**撞上 350 上限。
        exp_x = round(250 * 400 / 300)  # 333
        check("M21 x 按这一组的比例算出 ~333（而不是被钳到上限 350；换尺寸不换钳位规则）",
              tr21.get("bgImageWidth") == 400 and tr21.get("templateImageWidth") == 50
              and bool(xs21) and abs(max(xs21) - exp_x) <= 6 and max(xs21) < 350,
              f"bg={tr21.get('bgImageWidth')} tpl={tr21.get('templateImageWidth')} "
              f"max x={max(xs21) if xs21 else None} 期望≈{exp_x}")
        # 上限本身也单独钉一次：拖到轨道尽头（limitDisp = 300-38 = 262 显示 px）时，
        # 原图 x 该被钳在 350 以内，且不越界不为负。
        # ⚠ 上一版这里点 `sorani-reverify-btn` 想"再来一次"，
        #   但 M21 那一次提交成功后**对话框已经关了**（mock 返回成功 → emit close），
        #   按钮早就不在页面上 → 等元素超时。
        #   另外也别学第一版拖 600px：那会拖出轨道、solve 不成立。
        #   改成独立开一个上下文，拖 265px（刚好越过 262 的上限）。
        mg2 = await browser.new_context(viewport={"width": 1280, "height": 900})
        await mg2.add_init_script("localStorage.setItem('zgj.locale', 'zh')")
        await mg2.add_init_script("window.__zgj = { mockCaptchaSize: [400, 200, 50, 200] }")
        pg2 = await mg2.new_page()
        await pg2.goto(f"{BASE}/#/settings")
        await pg2.wait_for_selector("[data-testid='sorani-section']", timeout=15000)
        await pg2.click("[data-testid='sorani-login-btn']")
        await pg2.wait_for_selector("[data-testid='sorani-start-btn']", timeout=8000)
        await pg2.click("[data-testid='sorani-start-btn']")
        await pg2.wait_for_selector("[data-testid='sorani-bg-img']", timeout=10000)
        await real_drag(pg2, "[data-testid='sorani-handle']", 265, steps=26, dy_jitter=0)
        try:
            await pg2.wait_for_selector("[data-testid='sorani-page-creds']:visible", timeout=8000)
            await pg2.fill("[data-testid='sorani-identifier']", "clamp")
            await pg2.fill("[data-testid='sorani-password']", "pw")
            await pg2.click("[data-testid='sorani-submit-btn']")
            await pg2.wait_for_function("() => window.__zgjSoraniLastLogin", timeout=8000)
            t21b = await pg2.evaluate("() => window.__zgjSoraniLastLogin")
            xs21b = [q["x"] for q in ((t21b or {}).get("track") or {}).get("trackList") or []]
            # 262 显示 px × 400/300 = 349（上限 350 之内、且确实撞到了钳位边界）
            check("M21b 拖过轨道上限时 x 撞在钳位边界（≈349 ≤ 350），不为负也不越界",
                  bool(xs21b) and min(xs21b) >= 0 and max(xs21b) <= 350 and max(xs21b) >= 340,
                  f"min={min(xs21b) if xs21b else None} max={max(xs21b) if xs21b else None}")
        except Exception as exc:
            check("M21b 拖过轨道上限时 x 撞在钳位边界（≈349 ≤ 350），不为负也不越界",
                  False, repr(exc)[:120])
        await mg2.close()

        await mg.close()

        # ============ M14–M18：v1 另外五条错误路径（:311/:321/:328/:404/:411） ============
        # v1 的写法是直接把 Qt 的 stack 跳到某页 / 换一个残缺的 core 对象。
        # v2 里没有"强行跳页"的入口，我**不硬造那个状态**（造出来测的是我自己），
        # 所以每条都换成"同一个保证的可达形式"，并把差别写在断言名或注释里。

        # ---- M14 v1 :311「无轨迹不许进账密页」的可达形式 ----
        # v1 能 `stack.setCurrentWidget(page_creds)` 强行跳页再提交；
        # v2 的 creds 页只能由 `SliderCaptcha` emit solved 之后进入，
        # 而 `beginCaptcha()` 会把 track 置 null（SoraniLoginDialog.vue:53），
        # 所以"账密页 + 无轨迹"这个状态在 v2 不可达。
        # 于是测**同一保证的前件**：没拖过滑块就绝不该看到账密页。
        md = await browser.new_context(viewport={"width": 1280, "height": 900})
        await md.add_init_script("localStorage.setItem('zgj.locale', 'zh')")
        pd = await md.new_page()
        await pd.goto(f"{BASE}/#/settings")
        await pd.wait_for_selector("[data-testid='sorani-section']", timeout=15000)
        await pd.click("[data-testid='sorani-login-btn']")
        await pd.wait_for_selector("[data-testid='sorani-start-btn']", timeout=8000)
        creds_d = await pd.locator("[data-testid='sorani-page-creds']:visible").count()
        check("M14 没拖过滑块就看不到账密页（v1 :311 同一保证的可达形式；Qt 强行跳页在 v2 不可达）",
              creds_d == 0, f"creds 可见={creds_d}")

        # ---- M15 v1 :321「空账密本地拦下，根本没打核心」----
        await pd.click("[data-testid='sorani-start-btn']")
        await pd.wait_for_selector("[data-testid='sorani-slider']", timeout=8000)
        await real_drag(pd, "[data-testid='sorani-handle']", 150)
        await pd.wait_for_selector("[data-testid='sorani-page-creds']:visible", timeout=8000)
        # 计数在 mock 侧（api.ts 的 __zgjSoraniCalls），不是靠时间差猜
        before15 = await pd.evaluate("() => (window.__zgjSoraniCalls || {}).login || 0")
        cap_before15 = await pd.evaluate("() => (window.__zgjSoraniCalls || {}).captcha || 0")
        await pd.click("[data-testid='sorani-submit-btn']")
        await pd.wait_for_timeout(1200)
        after15 = await pd.evaluate("() => (window.__zgjSoraniCalls || {}).login || 0")
        err15 = await pd.locator("[data-testid='sorani-error']:visible").count()
        txt15 = (await pd.locator("[data-testid='sorani-error']").first.inner_text()).strip() if err15 else ""
        check("M15 空账密被本地拦下：login 调用数不变 + 红字出现（v1 :321）",
              after15 == before15 and err15 == 1 and bool(txt15),
              f"login {before15}→{after15} err={txt15!r}")
        # 红字必须是 i18n 的那句，不是键名（第 44 轮我自己写出过一次 downloads.mergeFail）
        check("M15b 红字是本地化的句子而不是键名/裸 code",
              ("请输入" in txt15 or "用户名" in txt15) and "sorani." not in txt15 and "captcha_fail" not in txt15,
              f"文案={txt15!r}")
        # 空账密被拦下时**不该**顺手重取验证码（那是"提交失败"分支才做的事）
        cap_after15 = await pd.evaluate("() => (window.__zgjSoraniCalls || {}).captcha || 0")
        check("M15c 本地拦下不额外打验证码请求（次数不变）",
              cap_after15 == cap_before15, f"captcha {cap_before15}→{cap_after15}")

        # ---- M16 v1 :328「取验证码失败 → 回状态页 + 红字」----
        await pd.evaluate("() => { window.mockCaptchaFail = '1' }")
        # ⚠ 上一版这里点的是 `sorani-creds-back`，而它只做 `page = 'captcha'`
        #   ——**不会重新取验证码**，所以注入根本没被触发，红字还是上一步的"请输入…"。
        #   真会重取的是 `sorani-reverify-btn`（@click="beginCaptcha"，SoraniLoginDialog.vue:187）。
        #   先回滑块页，再点「重新验证」。
        # ⚠ 第二次修这条：`sorani-reverify-btn` 其实在**账密页**上（SoraniLoginDialog.vue:186），
        #   不在滑块页 —— 我按名字猜了归属，于是等元素可见等了 30s 超时。
        #   正确做法：就停在账密页，点「重新验证」（@click="beginCaptcha"）→ 取验证码失败。
        await pd.click("[data-testid='sorani-reverify-btn']")
        await pd.wait_for_timeout(1800)
        on_status = await pd.locator("[data-testid='sorani-page-status']:visible").count()
        err16 = (await pd.locator("[data-testid='sorani-error']").first.inner_text()).strip() \
            if await pd.locator("[data-testid='sorani-error']:visible").count() else ""
        # v1 那句中文是「验证码获取失败」；v2 用 sorani.captchaFail 组装，
        # 注入的错误消息本身是 ASCII（不然英文界面会露中文）
        check("M16 取验证码失败：退回状态页 + 红字（v1 :328）",
              on_status == 1 and bool(err16) and "验证码加载失败" in err16,
              f"状态页可见={on_status} 红字={err16[:90]!r}")
        # 注入的错误消息本身是 ASCII（api.ts 里那条 mock 异常），
        # 中文句子由 i18n 的 sorani.captchaFail 组装 —— 所以英文态不会露中文。
        check("M16b 红字里没有裸 code 也没有键名",
              "captcha_fail" not in err16 and "sorani." not in err16,
              f"红字={err16[:80]!r}")
        await pd.evaluate("() => { delete window.mockCaptchaFail }")

        # ---- M17 v1 :404「读状态失败不崩，徽标显示未知」----
        await pd.evaluate("() => { window.__zgj = window.__zgj || {}; window.__zgj.mockStatusFail = '1' }")
        await md.close()
        me = await browser.new_context(viewport={"width": 1280, "height": 900})
        await me.add_init_script("localStorage.setItem('zgj.locale', 'zh')")
        await me.add_init_script("window.__zgj = { mockStatusFail: '1' }")
        pe = await me.new_page()
        await pe.goto(f"{BASE}/#/settings")
        await pe.wait_for_selector("[data-testid='sorani-section']", timeout=15000)
        await pe.wait_for_timeout(1500)
        chip17 = (await pe.locator("[data-testid='sorani-status']").inner_text()).strip()
        check("M17 读状态失败：徽标是「未知」而不是谎报未登录（v1 :404）",
              chip17 == "未知", f"chip={chip17!r}")

        # ---- M18 v1 :411「核心只给账号没有 name」时不该拼出 undefined ----
        await pe.evaluate("() => { window.__zgj = {} }")
        await pe.close()
        await me.close()
        mf = await browser.new_context(viewport={"width": 1280, "height": 900})
        await mf.add_init_script("localStorage.setItem('zgj.locale', 'zh')")
        await mf.add_init_script("window.__zgj = { mockSoraniNoName: '1' }")
        pf = await mf.new_page()
        await pf.goto(f"{BASE}/#/settings")
        await pf.wait_for_selector("[data-testid='sorani-section']", timeout=15000)
        await pf.click("[data-testid='sorani-login-btn']")
        await pf.wait_for_selector("[data-testid='sorani-start-btn']", timeout=8000)
        await pf.click("[data-testid='sorani-start-btn']")
        await pf.wait_for_selector("[data-testid='sorani-slider']", timeout=8000)
        await real_drag(pf, "[data-testid='sorani-handle']", 150)
        await pf.wait_for_selector("[data-testid='sorani-page-creds']:visible", timeout=8000)
        await pf.fill("[data-testid='sorani-identifier']", "solo")
        await pf.fill("[data-testid='sorani-password']", "pw")
        await pf.click("[data-testid='sorani-submit-btn']")
        await pf.wait_for_selector("[data-testid='sorani-dialog']", state="detached", timeout=10000)
        await pf.wait_for_timeout(1500)
        chip18 = (await pf.locator("[data-testid='sorani-status']").inner_text()).strip()
        check("M18 状态没有 name 时徽标是「已登录」，不拼出 undefined（v1 :411 那一族）",
              chip18.startswith("已登录") and "undefined" not in chip18 and "·" not in chip18,
              f"chip={chip18!r}")
        await mf.close()

        # ================= 页 2（en，独立 context）：M7 =================
        ctx2 = await browser.new_context(viewport={"width": 1280, "height": 900})
        await ctx2.add_init_script("localStorage.setItem('zgj.locale', 'en')")
        p2 = await ctx2.new_page()
        await p2.goto(f"{BASE}/#/settings")
        await p2.wait_for_selector("[data-testid='sorani-section']", timeout=15000)
        sec_txt = (await p2.locator("[data-testid='sorani-section']").inner_text()).strip()
        cjk_sec = CJK.findall(sec_txt)
        await p2.click("[data-testid='sorani-login-btn']")
        await p2.wait_for_selector("[data-testid='sorani-dialog']", timeout=8000)
        dlg_txt = (await p2.locator("[data-testid='sorani-dialog']").inner_text()).strip()
        await p2.click("[data-testid='sorani-start-btn']")
        await p2.wait_for_selector("[data-testid='sorani-slider']", timeout=8000)
        dlg_txt2 = (await p2.locator("[data-testid='sorani-dialog']").inner_text()).strip()
        cjk_dlg = CJK.findall(dlg_txt + "\n" + dlg_txt2)
        check(
            "M7 en 站源账号区+对话框零 CJK",
            not cjk_sec and not cjk_dlg and "Source Accounts" in sec_txt,
            f"sec_cjk={cjk_sec[:6]} dlg_cjk={cjk_dlg[:6]} 行文本={(dlg_txt.splitlines() or [''])[0][:40]}",
        )
        await ctx2.close()

        await browser.close()

    _devserver.stop_dev(spawned, PORT, name="sorani")

    failed = [r for r in RESULTS if r[0] == "FAIL"]
    print(f"\n== {len(RESULTS) - len(failed)}/{len(RESULTS)} PASS ==")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    asyncio.run(main())
