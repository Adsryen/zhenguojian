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
（自动复用已在跑的 5188；否则自行 `npm run dev:mock -- --port 5188 --strictPort` 并在结束关闭。）
"""
import asyncio
import os
import re
import socket
import subprocess
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

    spawned = None
    if not port_open("127.0.0.1", PORT):
        spawned = subprocess.Popen(
            ["npm", "run", "dev:mock", "--", "--port", str(PORT), "--strictPort"],
            cwd=FRONTEND,
            stdout=open(os.path.join(HERE, ".dev_sorani_mock.log"), "w"),
            stderr=subprocess.STDOUT,
        )
        for _ in range(80):
            if port_open("127.0.0.1", PORT):
                break
            time.sleep(0.5)
        else:
            print("dev server 未能启动，见 verification/.dev_sorani_mock.log")
            sys.exit(2)

    async with async_playwright() as pw:
        browser = await pw.chromium.launch(executable_path=EXEC, args=["--no-sandbox"])

        # ================= 页 1（zh）：M1-M6 + M8 =================
        ctx = await browser.new_context(viewport={"width": 1280, "height": 900})
        page = await ctx.new_page()
        await page.goto(f"{BASE}/#/settings")
        await page.wait_for_selector("[data-testid='sorani-row']", timeout=15000)

        # ---- M1 ----
        badge = page.locator("[data-testid='sorani-status']")
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

    if spawned:
        spawned.terminate()
        try:
            spawned.wait(timeout=10)
        except subprocess.TimeoutExpired:
            spawned.kill()

    failed = [r for r in RESULTS if r[0] == "FAIL"]
    print(f"\n== {len(RESULTS) - len(failed)}/{len(RESULTS)} PASS ==")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    asyncio.run(main())
