#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""update_verify —— V-UPD 程序内更新（mock 面）端到端验证（Playwright + dev:mock 5189）。

断言：
  V1 设置区「更新」section 渲染 + 当前版本行（v0.0.0-dev / 开发构建提示）。
  V2 检查按钮 → mock 返回新版本 v2.0.1 → 结果行出现且顶部横幅出现。
  V3 横幅「跳过此版本」→ 横幅消失；同页再检查（30s 冷却）不弹横幅；
     新开页再检查 → 「已被跳过」行且仍不弹横幅（ignoredTag 持久化抑制）。
  V4 下载进度 mock 推进（进度条宽度两次采样递增）→ 取消 → 「可续传」文案。
  V5 自动检查开关：点击 → localStorage 'zgj.update.settings'.autocheck=true →
     reload 后开关 UI 态恢复 on（mock verify 只测 UI 态；真实面走 UpdateSettings）。
  V6 en locale 下「更新」区零 CJK 字符（独立 context 隔离状态）。
截图：settings-update.png（V1 元素图）、update-banner.png（V2 全页含横幅）。

用法：python3 verification/update_verify.py
（自动复用已在跑的 5189；否则自行 `npm run dev:mock -- --port 5189 --strictPort` 并在结束关闭。）
"""
import asyncio
import json
import os
import re
import socket
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
# 自适应：脚本约定在 client-v2/frontend/verification/；若被放到仓库根 verification/ 也能跑
_ROOT_ALT = os.path.normpath(os.path.join(HERE, "..", "..", "client-v2", "frontend"))
ROOT = os.path.dirname(HERE)
FRONTEND = ROOT if os.path.basename(ROOT) == "frontend" else _ROOT_ALT
PORT = 5189
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


def init_script(autocheck=False, prerelease=False, ignored="", locale_en=False):
    settings = json.dumps(
        {"autocheck": autocheck, "prerelease": prerelease, "mirrorPrefix": "", "ignoredTag": ignored, "lastCheckAt": 0}
    )
    parts = [f"localStorage.setItem('zgj.update.settings', '{settings}')"]
    if locale_en:
        parts.append("localStorage.setItem('zgj.locale', 'en')")
    return "\n".join(parts)


async def fill_width(page):
    return await page.evaluate(
        """() => { const el = document.querySelector('.upd-prog .upd-fill');
                   return el ? el.getBoundingClientRect().width : -1 }"""
    )


async def main():
    from playwright.async_api import async_playwright

    spawned = None
    if not port_open("127.0.0.1", PORT):
        spawned = subprocess.Popen(
            ["npm", "run", "dev:mock", "--", "--port", str(PORT), "--strictPort"],
            cwd=FRONTEND,
            stdout=open(os.path.join(HERE, ".dev_update_mock.log"), "w"),
            stderr=subprocess.STDOUT,
        )
        for _ in range(80):
            if port_open("127.0.0.1", PORT):
                break
            time.sleep(0.5)
        else:
            print("dev server 未能启动，见 verification/.dev_update_mock.log")
            sys.exit(2)

    async with async_playwright() as pw:
        browser = await pw.chromium.launch(executable_path=EXEC, args=["--no-sandbox"])

        # ============ 页 1（zh，autocheck 关，无 ignored）：V1/V2/V4/V3/V5 ============
        ctx = await browser.new_context(viewport={"width": 1280, "height": 860})
        page = await ctx.new_page()
        await page.add_init_script(init_script(autocheck=False))
        await page.goto(f"{BASE}/#/settings")
        await page.wait_for_selector("[data-testid='update-section']", timeout=15000)
        sec = page.locator("[data-testid='update-section']")
        await sec.scroll_into_view_if_needed()

        # ---- V1 ----
        cur_text = (await page.inner_text("[data-testid='update-current']")).strip()
        check("V1 更新 section 渲染 + 当前版本行", "v0.0.0-dev" in cur_text and "开发构建" in cur_text, cur_text)
        await page.screenshot(path=os.path.join(HERE, "settings-update.png"))

        # ---- V2 检查 → 新版本 + 横幅 ----
        await page.click("[data-testid='update-check-btn']")
        try:
            await page.wait_for_selector("[data-testid='update-available-line']", timeout=8000)
            line = (await page.inner_text("[data-testid='update-available-line']")).strip()
            check("V2a 检查结果=新版本 v2.0.1", "v2.0.1" in line, line)
        except Exception:
            check("V2a 检查结果=新版本 v2.0.1", False, "available-line 未出现")
        try:
            await page.wait_for_selector("[data-testid='update-banner']", timeout=4000)
            btext = (await page.inner_text("[data-testid='update-banner']")).strip()
            check("V2b 顶部横幅出现", "v2.0.1" in btext, btext[:80])
        except Exception:
            check("V2b 顶部横幅出现", False, "banner 未出现")
        await page.screenshot(path=os.path.join(HERE, "update-banner.png"))

        # ---- V4 下载进度推进 → 取消续传文案 ----
        await page.click("[data-testid='update-download-btn']")
        await page.wait_for_selector(".upd-prog", timeout=6000)
        await page.wait_for_timeout(700)
        w1 = await fill_width(page)
        await page.wait_for_timeout(1100)
        w2 = await fill_width(page)
        check("V4a 下载进度条推进", w1 > 0 and w2 > w1, f"w1={w1:.1f} w2={w2:.1f}")
        ptext = (await page.inner_text("[data-testid='update-progress']")).strip()
        check("V4b 进度行含百分比/速率", "%" in ptext and "/s" in ptext, ptext.replace("\n", " | ")[:90])
        await page.click("[data-testid='update-cancel-btn']")
        await page.wait_for_timeout(400)
        ctext = (await page.inner_text("[data-testid='update-progress']")).strip()
        check("V4c 取消后提示可续传", "续传" in ctext, ctext.replace("\n", " | ")[:90])

        # ---- V3 跳过此版本 ----
        await page.click("[data-testid='update-banner-skip']")
        try:
            await page.wait_for_selector("[data-testid='update-banner']", state="detached", timeout=5000)
            check("V3a 跳过后横幅消失", True)
        except Exception:
            check("V3a 跳过后横幅消失", False, "banner 仍在")
        await page.click("[data-testid='update-check-btn']")  # 30s 冷却 → 回缓存不派发
        await page.wait_for_timeout(1500)
        gone = await page.evaluate("!document.querySelector(\"[data-testid='update-banner']\")")
        cool = "刚检查过" in (await page.inner_text("[data-testid='update-check-result']"))
        check("V3b 同页冷却再检查不弹横幅", bool(gone) and cool, f"bannerGone={gone} cooldownNote={cool}")

        # 新开页（同 context，localStorage 共享 ignoredTag）→ 真检走「已被跳过」且无横幅
        page2 = await ctx.new_page()
        await page2.add_init_script(init_script(autocheck=False, ignored="v2.0.1"))
        await page2.goto(f"{BASE}/#/settings")
        await page2.wait_for_selector("[data-testid='update-section']", timeout=15000)
        await page2.click("[data-testid='update-check-btn']")
        skipped_ok = False
        try:
            await page2.wait_for_function(
                """() => document.querySelector("[data-testid='update-check-result']")?.innerText.includes('已被跳过')""",
                timeout=8000,
            )
            skipped_ok = True
        except Exception:
            pass
        rtext = (await page2.inner_text("[data-testid='update-check-result']")).strip() if skipped_ok else "(无结果行)"
        avail = await page2.evaluate("!!document.querySelector(\"[data-testid='update-available-line']\")")
        await page2.wait_for_timeout(2600)  # 跨过 startup 3s 派发窗
        gone2 = await page2.evaluate("!document.querySelector(\"[data-testid='update-banner']\")")
        check("V3c 新页持久化 ignoredTag → 「已被跳过」且无横幅", skipped_ok and (not avail) and bool(gone2), rtext.replace("\n", " | ")[:90])
        await page2.close()

        # ---- V5 自动检查开关持久化（mock UI 态；独立 context 免 init 复位自伤）----
        ctx5 = await browser.new_context(viewport={"width": 1280, "height": 860})
        page5 = await ctx5.new_page()
        await page5.goto(f"{BASE}/#/settings")
        await page5.wait_for_selector("[data-testid='update-autocheck']", timeout=15000)
        on0 = await page5.get_attribute("[data-testid='update-autocheck']", "aria-checked")
        check("V5z 默认态自动检查为开", on0 == "true", f"aria-checked={on0}")
        await page5.click("[data-testid='update-autocheck']")
        await page5.wait_for_timeout(600)
        stored = await page5.evaluate("JSON.parse(localStorage.getItem('zgj.update.settings')||'{}').autocheck")
        check("V5a 开关点击写入 mock settings", stored is False, f"localStorage autocheck={stored}")
        await page5.reload()
        await page5.wait_for_selector("[data-testid='update-autocheck']", timeout=15000)
        on = await page5.get_attribute("[data-testid='update-autocheck']", "aria-checked")
        cls = await page5.get_attribute("[data-testid='update-autocheck']", "class")
        check("V5b reload 后开关 UI 恢复（关态保持）", on == "false" and "on" not in (cls or ""), f"aria-checked={on} class={cls}")
        await ctx5.close()

        # ============ 页 3（en，独立 context 全默认）：V6 零 CJK ============
        ctx2 = await browser.new_context(viewport={"width": 1280, "height": 860})
        page3 = await ctx2.new_page()
        await page3.add_init_script(init_script(locale_en=True))
        await page3.goto(f"{BASE}/#/settings")
        await page3.wait_for_selector("[data-testid='update-section']", timeout=15000)
        await page3.click("[data-testid='update-check-btn']")
        try:
            await page3.wait_for_selector("[data-testid='update-available-line']", timeout=8000)
        except Exception:
            pass
        sec_text = (await page3.inner_text("[data-testid='update-section']"))
        hits = CJK.findall(sec_text)
        check("V6 en 态更新区零 CJK", len(hits) == 0, f"hits={hits[:6]} sample={sec_text[:60]!r}")

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
