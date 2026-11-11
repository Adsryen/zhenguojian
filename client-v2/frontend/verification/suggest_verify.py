#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""搜索页联想词（v1 对齐，子代理 G）验证（headless chromium + mock dev server @5184）。

自动拉起/关闭 `npm run dev:mock -- --port 5184 --strictPort`（谁拉起谁关闭；若 5184 已被占用
则复用并在结束时保留——5180/5182/5183 上可能存在其他代理的 vite，与本验证无关，勿杀，
`pgrep -af vite` 可查）。

断言：
  S0 进入搜索页，当前源=红果短剧。
  S1 输入触发联想面板：词表≥8 且均含输入词；与「最近搜索」/引导 hint 互斥；无默认高亮；
     面板在途时新请求未回、Enter 搜索后面板不复闪（token 作废在途应答）；快速键入中 Enter 直搜正常。
     截图 search-suggest.png（面板展开态）。
  S2 防抖：连续键入 3 字后共只发 1 次 suggestions（window.__zgjSuggestCalls）。
  S3 键盘链：↓↓ 高亮第三词 → Enter 选中：回填输入、执行搜索、面板关；最近搜索仅新增 1 条（不双记）；
     选中后不重复拉联想。
  S4 鼠标点击联想词 → 执行搜索，最近搜索置顶且唯一。
  S5 Esc 关闭（输入保留）；点击外部关闭。
  S6 非红果源输入不触发（无面板、计数不增）；勾选「全部来源」门控放行（+1 请求、面板开）；取消收起。
  S7 en locale：hint 与 aria-label 走英文键；词表照常。
  S8 全程无未捕获 JS 错误（pageerror）。
PASS/FAIL 行打印，任何 FAIL 退出码非 0。
"""
import json
import os
import socket
import subprocess
import sys
import time

from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = 5184
BASE = f"http://127.0.0.1:{PORT}"
EXE = "/home/ryen/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
SEARCH_URL = BASE + "/#/search"
INPUT = "[data-testid='search-input']"
PANEL = "[data-testid='suggest-panel']"
ITEMS = "[data-testid='suggest-item']"

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
    """杀整个进程组（npm 壳不会把 SIGTERM 传给 vite 子进程）"""
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


def calls(page):
    return page.evaluate("window.__zgjSuggestCalls || 0")


def item_texts(page):
    return page.evaluate(
        "[...document.querySelectorAll('[data-testid=\"suggest-item\"]')].map(e => e.textContent.trim())"
    )


def recents_key(page, slug="hongguo"):
    return json.loads(page.evaluate(f"localStorage.getItem('zgj.recent.{slug}') || '[]'"))


def recents_before(page):
    """进本页 settle 后的最近搜索快照：reload 后 SearchView 既有的「切源重搜」watch 可能把上一次
    搜索词回推置顶（与联想无关的存量行为），精确列表断言不稳；先等 settle 再取基线，
    记录断言全部改用进程内 delta 自含式。"""
    page.wait_for_timeout(1500)
    return recents_key(page)


def type_text(page, text, delay=40):
    page.focus(INPUT)
    page.keyboard.type(text, delay=delay)


def wait_panel(page, timeout=8000):
    page.wait_for_selector(PANEL, state="visible", timeout=timeout)


def click_item(page, idx):
    box = page.locator(ITEMS).nth(idx).bounding_box()
    page.mouse.click(box["x"] + box["width"] / 2, box["y"] + box["height"] / 2)


def reset_storage(page, source="hongguo", locale=None):
    js = "localStorage.clear(); localStorage.setItem('zhenguojian.source', '" + source + "');"
    if locale:
        js += " localStorage.setItem('zgj.locale', '" + locale + "');"
    page.evaluate(js)
    page.reload()
    page.wait_for_selector(".search-bar")


def main():
    spawned = None
    if not port_open():
        spawned = subprocess.Popen(
            ["npm", "run", "dev:mock", "--", "--port", str(PORT), "--strictPort"],
            cwd=FRONTEND,
            stdout=open(os.path.join(HERE, ".dev_suggest.log"), "w"),
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        if not wait_port():
            kill_group(spawned)
            print("FAIL S0 dev server 未能启动（见 verification/.dev_suggest.log）")
            sys.exit(1)

    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE, headless=True)
            ctx = browser.new_context(viewport={"width": 1280, "height": 800})
            page = ctx.new_page()
            js_errors = []
            page.on("pageerror", lambda e: js_errors.append(str(e)))

            page.goto(SEARCH_URL)
            reset_storage(page)
            # 站源表异步加载，等 displayName 就位再断言
            page.wait_for_function("document.querySelector('.ss-name') && document.querySelector('.ss-name').textContent.includes('红果短剧')", timeout=8000)
            check("S0 当前源=红果短剧", "红果短剧" in page.locator(".ss-name").inner_text())

            # ── S1 输入触发面板 + 词表 + 互斥 ──
            type_text(page, "太奶奶")
            wait_panel(page)
            w = item_texts(page)
            check("S1a 联想面板展开且词表≥8", len(w) >= 8, f"n={len(w)} {w[:2]}…")
            check("S1b 词表均含输入", all("太奶奶" in x for x in w), str(w[:2]))
            check("S1c 面板开时最近搜索/引导 hint 互斥隐藏",
                  page.locator("[data-testid='recents']").count() == 0
                  and page.locator(".hint").count() == 0)
            check("S1d 无默认高亮", page.locator(".sg-item.active").count() == 0)
            page.screenshot(path=os.path.join(HERE, "search-suggest.png"))

            # S1e 在途作废：面板已开，追加 1 字（300ms 后请求发出）→ Enter → 面板不得复闪
            base0 = calls(page)
            type_text(page, "X", delay=10)
            page.wait_for_timeout(330)  # 进入「请求在途或刚回」窗口
            page.keyboard.press("Enter")
            page.wait_for_selector("button:has-text('清除')", timeout=15000)
            page.wait_for_timeout(900)
            check("S1e 搜索后在途/刚到的联想应答被作废（面板不复闪）",
                  page.locator(PANEL).count() == 0, f"calls {base0}->{calls(page)}")
            check("S1e2 选中/直搜经 doSearch 既有逻辑记录最近搜索",
                  page.input_value(INPUT) in recents_key(page), page.input_value(INPUT))

            # S1f 快速键入中直接 Enter：无高亮时走常规搜索
            page.reload()
            page.wait_for_selector(".search-bar")
            pre_f = recents_before(page)
            type_text(page, "测试", delay=30)
            page.keyboard.press("Enter")  # 防抖未到点即 Enter：v1 同款直搜
            page.wait_for_selector("button:has-text('清除')", timeout=15000)
            page.wait_for_timeout(800)
            check("S1f 键入中 Enter 直搜、面板不复闪、delta 新增 1 条",
                  page.locator(PANEL).count() == 0
                  and recents_key(page)[0] == "测试"
                  and recents_key(page).count("测试") == 1
                  and "测试" not in pre_f, f"pre={pre_f} now={recents_key(page)}")

            # ── S2 防抖单发 ──
            base = calls(page)
            type_text(page, "太奶奶", delay=30)
            wait_panel(page)
            page.wait_for_timeout(1000)
            c = calls(page)
            check("S2 连打 3 字只发 1 次联想请求", c - base == 1, f"base={base} now={c}")

            # ── S3 键盘链 ↓↓Enter ──
            texts = item_texts(page)
            pre = recents_before(page)
            # 无初始高亮：第 1 次 ↓ 落首词，故 ↓↓↓ = 第三词
            page.keyboard.press("ArrowDown")
            page.keyboard.press("ArrowDown")
            page.keyboard.press("ArrowDown")
            act = page.locator(".sg-item.active")
            check("S3a ↓↓↓ 高亮第三词",
                  act.count() == 1 and act.inner_text() == texts[2], texts[2])
            page.keyboard.press("Enter")
            page.wait_for_selector(PANEL, state="hidden", timeout=4000)
            check("S3b 选中回填输入框", page.input_value(INPUT) == texts[2])
            page.wait_for_selector("button:has-text('清除')", timeout=15000)
            r = recents_key(page)
            dup_ok = len(r) == len(pre) + (0 if texts[2] in pre else 1)
            check("S3c 搜索执行且最近搜索仅新增 1 条（不双记）",
                  r[0] == texts[2] and r.count(texts[2]) == 1 and dup_ok,
                  f"pre={pre} now={r}")
            c2 = calls(page)
            page.wait_for_timeout(600)
            check("S3d 选中回填不重复拉联想", calls(page) == c2 == base + 1, f"calls={c2}")

            # ── S4 鼠标点击选中 ──
            page.reload()
            page.wait_for_selector(".search-bar")
            type_text(page, "闪婚")
            wait_panel(page)
            v = item_texts(page)
            click_item(page, 1)
            page.wait_for_selector(PANEL, state="hidden", timeout=4000)
            check("S4 点击联想词执行搜索",
                  page.input_value(INPUT) == v[1]
                  and page.locator("button:has-text('清除')").count() == 1, v[1])
            r = recents_key(page)
            check("S4b 最近搜索置顶且唯一", r[0] == v[1] and r.count(v[1]) == 1, str(r))

            # ── S5 Esc / 外点关闭 ──
            page.reload()
            page.wait_for_selector(".search-bar")
            type_text(page, "太")
            wait_panel(page)
            page.keyboard.press("Escape")
            check("S5a Esc 关闭面板且输入保留",
                  page.locator(PANEL).count() == 0 and page.input_value(INPUT) == "太")
            type_text(page, "奶")
            wait_panel(page)
            page.mouse.click(640, 460)  # 内容区空白处
            check("S5b 点击外部关闭面板",
                  page.locator(PANEL).count() == 0 and page.input_value(INPUT) == "太奶")

            # ── S6 非红果源门控 ──
            page.click(".ss-btn")
            page.wait_for_selector(".ss-panel", state="visible")
            page.click(".ss-item[data-slug='huangguo_ai']")
            check("S6a 已切到黄果AI剧", "黄果AI剧" in page.locator(".ss-name").inner_text())
            base6 = calls(page)
            type_text(page, "奶")
            page.wait_for_timeout(800)
            check("S6b 非红果源输入不触发联想（无面板、计数不增）",
                  page.locator(PANEL).count() == 0 and calls(page) == base6,
                  f"calls={calls(page)} base={base6}")
            page.check("[data-testid='search-all']")
            wait_panel(page)
            page.wait_for_timeout(400)
            check("S6c 勾选「全部来源」后门控放行（恰 1 次请求+面板开）",
                  calls(page) == base6 + 1 and page.locator(PANEL).count() == 1)
            page.uncheck("[data-testid='search-all']")
            check("S6d 取消勾选收起面板", page.locator(PANEL).count() == 0)

            # ── S7 en locale ──
            reset_storage(page, source="hongguo", locale="en")
            type_text(page, "妈")
            wait_panel(page)
            hint = page.locator("[data-testid='suggest-hint']").inner_text()
            aria = page.get_attribute(PANEL, "aria-label") or ""
            w7 = item_texts(page)
            check("S7 en 态联想键生效（hint/aria 英文且词表≥8）",
                  hint == "↑↓ select · Enter search · Esc close"
                  and aria == "Search suggestions" and len(w7) >= 8, f"{hint} / {aria}")
            page.keyboard.press("Escape")

            check("S8 全程无未捕获 JS 错误", not js_errors, "; ".join(js_errors[:2]))

            ctx.close()
            browser.close()
    finally:
        if spawned:
            kill_group(spawned)

    print(f"{'ALL PASS' if not failures else 'FAILED: ' + ', '.join(failures)}")
    sys.exit(0 if not failures else 1)


if __name__ == "__main__":
    main()
