#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""剧库榜单侧栏（V2-E）验证（headless chromium + dev:mock，自管理 5178 端口）。

自动拉起/关闭 `npm run dev:mock -- --port 5178 --strictPort`；若端口已被占用则复用并在结束时保留。
断言：
  A1 侧栏首屏 10 行，名次 data-rank 依次 1..10（mock 榜 hongguo-hot）。
  A2 切榜（huangdou-all）后逐行标题整体变化，chip 高亮迁移。
  A3 点榜首行 → /#/detail/…，query 带 d；返回后侧栏恢复首屏 10 行。
  A4 加载更多 → 20 行，data-testid 去重后仍 20（rank 1..20 无重复），按钮消失、文案「已全部加载」。
  A5 en 态：localStorage zgj.locale=en reload 后 rank-title=「Rankings」、更新 hint 以「Updated:」开头。
  A6 窄屏（1000px）：侧栏收起，rank-toggle 按钮出抽屉，rank-close 可关。
  A7 封面：.rank-cover img 至少 1 张加载成功（naturalWidth>0）。
  A8 全程无未捕获 JS 错误 / console error。
截图：本目录 library-rankings.png（宽屏 zh，10 行）/ library-rankings-more.png / library-rankings-narrow.png。
断言以 PASS/FAIL 行打印，任何 FAIL 退出码非 0。
"""
import os
import socket
import subprocess
import sys
import time

from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = 5178
BASE = f"http://127.0.0.1:{PORT}"
LIB_URL = BASE + "/#/library"
EXE = "/home/ryen/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line)
    if not cond:
        failures.append(name)
    return cond


def port_open():
    with socket.socket() as s:
        s.settimeout(0.4)
        return s.connect_ex(("127.0.0.1", PORT)) == 0


def wait_port(deadline=40.0):
    t0 = time.time()
    while time.time() - t0 < deadline:
        if port_open():
            return True
        time.sleep(0.4)
    return False


def kill_group(proc):
    """杀整个进程组（npm 壳不会把 SIGTERM 传给 vite 子进程，孤儿会占住端口）"""
    import signal
    try:
        os.killpg(os.getpgid(proc.pid), signal.SIGTERM)
    except Exception:
        pass
    try:
        proc.wait(10)
    except Exception:
        try:
            os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
        except Exception:
            pass


def rows(page):
    return page.evaluate(
        """() => [...document.querySelectorAll('.rank-row')].map(r => ({
            rank: r.getAttribute('data-rank'),
            testid: r.getAttribute('data-testid'),
            title: (r.querySelector('.rank-name') || {}).textContent || '',
        }))"""
    )


def wait_rows(page, n, timeout_ms=15000):
    try:
        page.wait_for_function(
            f"() => document.querySelectorAll('.rank-row').length >= {n}", timeout=timeout_ms
        )
        return True
    except Exception:
        return False


def main():
    mine = None
    if not port_open():
        log = open(os.path.join(HERE, ".dev_rankings.log"), "wb")
        mine = subprocess.Popen(
            ["npm", "run", "dev:mock", "--", "--port", str(PORT), "--strictPort"],
            cwd=FRONTEND,
            stdout=log,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        if not wait_port():
            print("FAIL dev server 未能在 40s 内监听", PORT)
            kill_group(mine)
            sys.exit(1)
    else:
        print(f"复用已在监听:{PORT} 的 dev server（结束时保留）")

    js_errors, console_errors = [], []
    try:
        with sync_playwright() as pw:
            browser = pw.chromium.launch(executable_path=EXE, headless=True)
            page = browser.new_page(viewport={"width": 1280, "height": 800})
            page.on("pageerror", lambda e: js_errors.append(str(e)))
            page.on(
                "console",
                lambda m: console_errors.append(m.text) if m.type == "error" else None,
            )

            page.goto(LIB_URL)
            page.evaluate("() => localStorage.clear()")
            page.reload()
            page.wait_for_selector("[data-testid=rank-side]", timeout=15000)
            ok10 = wait_rows(page, 10)
            check("A1a 侧栏首屏 10 行", ok10 and len(rows(page)) == 10, f"rows={len(rows(page))}")
            r1 = rows(page)
            ranks1 = [r["rank"] for r in r1]
            check("A1b 名次依次 1..10", ranks1 == [str(i) for i in range(1, 11)], ",".join(ranks1))

            # A7 封面加载（早断：mock /posters/p*.svg 由 vite 直出）
            try:
                page.wait_for_function(
                    "() => [...document.querySelectorAll('.rank-cover img')].some(i => i.complete && i.naturalWidth > 0)",
                    timeout=8000,
                )
                check("A7 封面 img 加载成功", True)
            except Exception:
                check("A7 封面 img 加载成功", False)

            # A2 切榜
            titles_before = [r["title"] for r in r1]
            page.click("[data-testid=rank-board-huangdou-all]")
            ok_sw = wait_rows(page, 10) and page.wait_for_selector(
                ".rank-chips .chip.active[data-testid='rank-board-huangdou-all']", timeout=8000
            )
            r2 = rows(page)
            titles_after = [r["title"] for r in r2]
            check("A2a 切榜后 chip 高亮迁移", bool(ok_sw))
            check(
                "A2b 切榜后逐行标题整体变化",
                len(titles_after) == 10 and titles_before != titles_after
                and all(b != a for b, a in zip(titles_before, titles_after)),
            )

            # A3 点行进详情（用回 hongguo 榜首行亦可，这里直接点当前榜第 3 行）
            page.click("[data-testid=rank-row-3]")
            try:
                page.wait_for_function("() => location.hash.includes('/#/detail/') || location.hash.includes('#/detail/')", timeout=8000)
            except Exception:
                pass
            hash3 = page.evaluate("() => location.hash")
            check("A3a 点击行进入详情路由", "/detail/" in hash3, hash3[:90])
            check("A3b 详情 query 携带 d", "d=" in hash3)
            page.go_back()
            page.wait_for_selector("[data-testid=rank-side]", timeout=15000)
            ok_back = wait_rows(page, 10)
            r3 = rows(page)
            check(
                "A3c 返回后侧栏重置为第 1 页 10 行",
                ok_back and len(r3) == 10 and [x["rank"] for x in r3] == [str(i) for i in range(1, 11)],
                f"n={len(r3)} board={r3[0]['title'][:12] if r3 else '-'}",
            )

            # A4 加载更多 → 20 行无重复
            page.click("[data-testid=rank-more]")
            ok20 = wait_rows(page, 20)
            r4 = rows(page)
            testids = [x["testid"] for x in r4]
            ranks4 = [x["rank"] for x in r4]
            more_gone = page.locator("[data-testid=rank-more]").count() == 0
            status_txt = (page.locator("[data-testid=rank-status]").inner_text() or "").strip()
            check("A4a 加载更多后 20 行", ok20 and len(r4) == 20, f"n={len(r4)}")
            check("A4b 名次 1..20 无重复", len(set(testids)) == 20 and ranks4 == [str(i) for i in range(1, 21)])
            check("A4c 到底后按钮消失且提示已全部加载", more_gone and "全部加载" in status_txt, status_txt)

            page.screenshot(path=os.path.join(HERE, "library-rankings-more.png"), full_page=False)

            # A5 en 态（先回宽屏 zh 主截图已另拍）
            zh_shot = page.locator("[data-testid=rank-side]")
            zh_shot.screenshot(path=os.path.join(HERE, "library-rankings.png"))
            page.evaluate("() => localStorage.setItem('zgj.locale', 'en')")
            page.reload()
            page.wait_for_selector("[data-testid=rank-side]", timeout=15000)
            wait_rows(page, 10)
            title_en = (page.locator("[data-testid=rank-title]").inner_text() or "").strip()
            upd_en = (page.locator("[data-testid=rank-updated]").inner_text() or "").strip() if page.locator(
                "[data-testid=rank-updated]"
            ).count() else ""
            check("A5a en 态 rank-title=Rankings", title_en == "Rankings", title_en)
            check("A5b en 态更新 hint 走 tf", upd_en.startswith("Updated:"), upd_en)

            # A6 窄屏抽屉
            page.set_viewport_size({"width": 1000, "height": 800})
            page.wait_for_timeout(300)
            side_hidden = page.locator("[data-testid=rank-side]").count() == 0
            tog = page.locator("[data-testid=rank-toggle]")
            check("A6a 窄屏侧栏收起 + 出展开按钮", side_hidden and tog.count() == 1)
            tog.click()
            drawer_ok = page.wait_for_selector("[data-testid=rank-side].drawer", timeout=5000) is not None
            check("A6b 点按钮开抽屉", drawer_ok)
            page.screenshot(path=os.path.join(HERE, "library-rankings-narrow.png"), full_page=False)
            page.click("[data-testid=rank-close]")
            page.wait_for_timeout(300)
            check("A6c 抽屉可关闭", page.locator("[data-testid=rank-side]").count() == 0)

            browser.close()
    finally:
        if mine is not None:
            kill_group(mine)

    check("A8a 无未捕获 JS 错误", not js_errors, "; ".join(js_errors[:3]))
    check("A8b 无 console error", not console_errors, "; ".join(console_errors[:3]))

    print("\nRESULT:", "ALL PASS" if not failures else f"{len(failures)} FAIL: {failures}")
    sys.exit(0 if not failures else 1)


if __name__ == "__main__":
    main()
