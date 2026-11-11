#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""v2-D 验证：日历页滚动修复 + 详情状态本地化 + 演职员条 + 双语键（headless chromium）。

自管理 dev server：`npm run dev:mock -- --port 5177 --strictPort`（VITE_MOCK 模式）。
断言（任何 FAIL 退出码非 0）：
  ① /#/calendar 在 800x600 视口下 .cal-scroll 可滚动（scrollHeight>clientHeight 且 scrollTop 可变化）
  ② 详情 mock 态显示「已完结」pill，正文无裸枚举 finished/ongoing；集数文案「共 N 集」
  ③ 演职员条渲染 ≥2 个姓名（mock fixtures 3 人：示例·张导/李忆/王十七）
  ④ localStorage zgj.locale=en 重载后 Completed / Cast & Crew 等英文键生效、「已完结」消失
截图：本目录 calendar-scroll-fixed.png / detail-cast.png。
"""
import os
import re
import signal
import subprocess
import sys
import time
import urllib.request

from playwright.sync_api import sync_playwright

BASE = "http://127.0.0.1:5177"
EXE = "/home/ryen/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
OUT = os.path.dirname(os.path.abspath(__file__))
FRONT = os.path.abspath(os.path.join(OUT, ".."))

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


def start_server():
    log = open(os.path.join(OUT, "dev-5177.log"), "wb")
    proc = subprocess.Popen(
        ["npm", "run", "dev:mock", "--", "--port", "5177", "--strictPort"],
        cwd=FRONT, stdout=log, stderr=subprocess.STDOUT, start_new_session=True,
    )
    deadline = time.time() + 60
    while time.time() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(f"dev server 提前退出 code={proc.returncode}，见 dev-5177.log")
        try:
            with urllib.request.urlopen(BASE + "/", timeout=2) as r:
                if r.status == 200:
                    return proc
        except Exception:
            time.sleep(0.5)
    proc.kill()
    raise RuntimeError("dev server 60s 未就绪，见 dev-5177.log")


def main():
    proc = start_server()
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE)
            page = browser.new_context(viewport={"width": 800, "height": 600}).new_page()

            # ---------- ① 日历页滚动 ----------
            page.goto(BASE + "/#/calendar")
            page.wait_for_selector(".cal-scroll")
            page.wait_for_function("document.querySelectorAll('.cal-row').length > 0", timeout=15000)
            rows = page.eval_on_selector_all(".cal-row", "els=>els.length")
            # 今天默认 4 行；若不足以溢出，点第 4 个日期 chip（mock 该日 5 行）
            fits = page.evaluate("() => { const e=document.querySelector('.cal-scroll'); return e.scrollHeight <= e.clientHeight }")
            if fits:
                page.locator(".date-chips .chip").nth(3).click()
                page.wait_for_function("document.querySelectorAll('.cal-row').length >= 5", timeout=15000)
                rows = page.eval_on_selector_all(".cal-row", "els=>els.length")
            geom = page.evaluate(
                "() => { const e=document.querySelector('.cal-scroll');"
                "e.scrollTop = 120;"
                "return { sh:e.scrollHeight, ch:e.clientHeight, st:e.scrollTop, wy:window.scrollY } }"
            )
            check("① .cal-scroll 内容溢出视口", geom["sh"] > geom["ch"], f"scrollHeight={geom['sh']} clientHeight={geom['ch']} rows={rows}")
            check("① scrollTop 可变化（容器滚动生效）", geom["st"] > 0, f"scrollTop={geom['st']}")
            check("① 窗口本体仍不滚（shell-main 约定未破坏）", geom["wy"] == 0, f"window.scrollY={geom['wy']}")
            page.wait_for_timeout(250)  # 让 loadMore（若有）落地再截图
            page.screenshot(path=os.path.join(OUT, "calendar-scroll-fixed.png"))

            # ---------- 进入详情（mock：闪婚老公是大佬 releaseStatus=finished） ----------
            page.goto(BASE + "/#/discover")
            page.wait_for_selector(".page-title")
            page.get_by_text("闪婚老公是大佬", exact=True).first.click()
            page.wait_for_url(re.compile(r"/#/detail/"), timeout=15000)
            page.wait_for_selector(".status-pill")

            # ---------- ② 状态本地化 ----------
            body = page.inner_text("body")
            check("② 状态 pill 显示「已完结」", "已完结" in body)
            check("② 无裸枚举 finished", not re.search(r"\bfinished\b", body, re.I))
            check("② 无裸枚举 ongoing", not re.search(r"\bongoing\b", body, re.I))
            check("② 完结措辞「共 N 集」", re.search(r"共 \d+ 集", body) is not None,
                  (re.search(r"共 \d+ 集", body) or type("o", (), {"group": lambda s, x: ""})()).group(0))

            # ---------- ③ 演职员条 ----------
            page.wait_for_selector(".cast-item", timeout=15000)
            names = page.eval_on_selector_all(".cast-name", "els=>els.map(e=>e.textContent.trim())")
            check("③ 演员条渲染 ≥2 个姓名", len(names) >= 2, f"names={names}")
            check("③ mock 姓名在列", "李忆" in names and "王十七" in names)
            page.locator(".cast-section").scroll_into_view_if_needed()
            page.wait_for_timeout(200)
            page.screenshot(path=os.path.join(OUT, "detail-cast.png"))

            # ---------- ④ 双语（en 键） ----------
            page.evaluate("() => localStorage.setItem('zgj.locale','en')")
            page.reload()
            page.wait_for_selector(".status-pill")
            page.wait_for_selector(".cast-item", timeout=15000)
            body_en = page.inner_text("body")
            check("④ en 态显示 Completed", "Completed" in body_en)
            check("④ en 态显示 Cast & Crew", "Cast & Crew" in body_en)
            check("④ en 态无中文「已完结」", "已完结" not in body_en)
            check("④ en 态集数文案 {n} episodes in total", re.search(r"\d+ episodes in total", body_en) is not None)

            browser.close()
    finally:
        os.killpg(os.getpgid(proc.pid), signal.SIGTERM)
        try:
            proc.wait(timeout=10)
        except Exception:
            os.killpg(os.getpgid(proc.pid), signal.SIGKILL)

    print(f"\n{'ALL PASS' if not failures else 'FAILURES: ' + ', '.join(failures)}")
    sys.exit(0 if not failures else 1)


if __name__ == "__main__":
    main()
