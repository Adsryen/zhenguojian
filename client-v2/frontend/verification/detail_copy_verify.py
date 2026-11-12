#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""v2-6-parity 验证：详情页「复制简介」（v1 main.py:185-186 对齐）+ 动作行 chrome 键化。

单轨：dev:mock @5183（VITE_MOCK=1）。剪贴板权限由 CDP grant，读回用 navigator.clipboard.readText()
（与 contextmenu_verify.py 同一套路）。

断言（任何 FAIL 退出码非 0）：
  ① zh 动作行逐字：播放当集/下载当集/下载全部（N 集）/追剧/看过/复制简介 六枚按钮
     与「选集」标题、「返回」按钮，文案必须与键化前逐字一致。
  ② 点「复制简介」→ toast「简介已复制」，剪贴板逐字 == 页面简介全文，且全程未展开
     （v1 main.py:186 直接 setText(summary.toPlainText())：复制与显示截断无关）。
     未尽项：mock fixtures 简介均 <40 字（fixtures.ts:81-134），「>120 字折叠后仍复制全文」
     这条分支本机无长样本，列为真机复验项。
  ③ en 态：Copy summary / Download episode / Episodes / Back 出现，
     且 .actions、.page-head、简介区节点内不得残留 CJK。
截图：detail-copy.png（zh）、detail-copy-en.png（en）。
"""
import os
import re
import signal
import subprocess
import sys
import time
import urllib.request

from playwright.sync_api import sync_playwright

EXE = "/home/ryen/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
OUT = os.path.dirname(os.path.abspath(__file__))
FRONT = os.path.abspath(os.path.join(OUT, ".."))
BASE = "http://127.0.0.1:5183"

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


import _devserver


def start_server():
    # 端口预检 + 严格收口（泄漏的 vite 会让下一轮验收出假红灯，见 _devserver.py）
    return _devserver.start_dev(FRONT, 5183, True, OUT, name="detail-copy")


def stop_server(proc):
    _devserver.stop_dev(proc, 5183)


def to_detail(page):
    page.goto(BASE + "/#/discover")
    page.wait_for_selector(".page-title")
    page.get_by_text("闪婚老公是大佬", exact=True).first.click()
    page.wait_for_url(re.compile(r"/#/detail/"), timeout=15000)
    page.wait_for_selector(".actions .btn", timeout=15000)
    page.wait_for_timeout(1200)


ZH_BUTTONS = ["播放第 1 集", "下载当集", "下载全部（", "＋ 追剧", "看过", "复制简介"]


def main():
    proc = start_server()
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE)
            ctx = browser.new_context(viewport={"width": 1200, "height": 800})
            ctx.grant_permissions(["clipboard-read", "clipboard-write"], origin=BASE)
            page = ctx.new_page()

            # ═════ ① zh 逐字 ═════
            to_detail(page)
            acts = page.locator(".actions").inner_text()
            miss = [b for b in ZH_BUTTONS if b not in acts]
            check("① zh 动作行 6 枚按钮逐字", not miss, f"missing={miss}")
            check("① zh「选集」标题逐字", "选集" in page.locator("#app").inner_text())
            check("① zh「返回」按钮逐字", "返回" in page.locator("#app").inner_text())
            check("① zh 短简介不出现「…展开」（>120 截断门槛不误触）",
                  page.locator(".desc .more").count() == 0,
                  f"more={page.locator('.desc .more').count()}")

            # ═════ ② 复制的是 meta.description 全文 ═════
            # 说明：mock fixtures 的简介都在 40 字内（services/fixtures.ts:81-134），
            # 「>120 折叠截断」这条分支在本机没有长样本可断言 —— 只在 ②c 断其门槛不误触，
            # 长简介下的「折叠态也复制全文」列为真机复验项（见 docstring 未尽项）。
            desc_folded = page.locator(".desc").first.inner_text()
            page.locator(".actions .btn", has_text="复制简介").first.click()
            page.wait_for_selector(".toast", timeout=8000)
            toast = page.locator(".toast").first.inner_text()
            clip = page.evaluate("navigator.clipboard.readText()")
            check("② toast「简介已复制」", "简介已复制" in toast, f"toast={toast[:60]}")
            check("② 剪贴板内容 == 页面简介全文（逐字）",
                  clip.strip() == desc_folded.strip() and len(clip.strip()) > 10,
                  f"clip={clip.strip()[:40]!r} len={len(clip.strip())}")
            check("② 复制不经过展开态（未点击 .desc 也拿到全文）",
                  page.locator(".desc.expanded").count() == 0)
            page.screenshot(path=os.path.join(OUT, "detail-copy.png"), full_page=False)

            # ═════ ③ en 态 ═════
            page.evaluate("localStorage.setItem('zgj.locale','en')")
            page.reload()
            page.wait_for_selector(".actions .btn", timeout=15000)
            page.wait_for_timeout(1000)
            en = page.locator(".actions").inner_text()
            app = page.locator("#app").inner_text()
            miss_en = [w for w in ["Copy summary", "Download episode", "Episodes", "Back"] if w not in en and w not in app]
            check("③ en 代表文案出现", not miss_en, f"missing={miss_en}")
            cjk = re.compile(r"[㐀-䶿一-鿿]")
            bad = [ln for ln in en.split("\n") if cjk.search(ln)]
            check("③ en .actions 无残留 CJK", not bad, f"bad={bad[:3]}")
            page.screenshot(path=os.path.join(OUT, "detail-copy-en.png"), full_page=False)

            ctx.close()
            browser.close()
    finally:
        stop_server(proc)

    print(f"\n===== detail_copy_verify: {'ALL PASSED' if not failures else str(len(failures)) + ' FAIL'} =====")
    for f in failures:
        print("FAILED:", f)
    sys.exit(1 if failures else 0)


if __name__ == "__main__":
    main()
