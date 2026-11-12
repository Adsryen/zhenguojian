#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""搜索页「全部来源」扇出验证（headless chromium + VITE_MOCK dev server）。

自动拉起/关闭 `npm run dev:mock`（127.0.0.1:5173）；若端口已被占用则复用并在结束时保留。
断言：
  A1 未勾选搜索 = 现状单源行为（PosterGrid，无分组）。
  A2 勾选搜索扇出 4 组（hongguo/huangguo_ai/adult_lab/youzi），未接入源 tianyuan 被排除。
  A3 组头展示 displayName，adult 源带 18+ 徽章。
  A4 组顺序 = orderedSources 序（红果置顶）。
  A5 组内容：hongguo/adult_lab 各 21 卡；huangguo_ai/youzi 空组提示行。
  A6 流式：组头先渲染（骨架瓦片），不等齐。
  A7 失败注入（window.__zgjSearchFault=['youzi']）：youzi 组失败行「该源搜索失败：模拟源故障（youzi）」
     且剥掉 [sim_fault] 尾缀，其余组正常。
  A8 全组皆败（fault='*'）出整体 error 条；清除钩子后「重试」恢复。
  A9 取消勾选清空分组态回单源。
  A10 勾选态持久化：reload 后仍勾选（zgj.search.all==='1'），扇出结构保留。
  A11 跨源点击：adult_lab 组卡片 → /#/detail/adult_lab%3A…，query.d.source=adult_lab，详情出「午夜档案」。
  A12 全程无未捕获 JS 错误 / console error。
截图：本目录 searchall_groups.png / searchall_fault.png / searchall_allfail.png /
       searchall_single.png / searchall_detail.png。
断言以 PASS/FAIL 行打印，任何 FAIL 退出码非 0。
"""
import json
import os
import socket
import subprocess

import _devserver
import sys
import time

from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
BASE = "http://127.0.0.1:5173"
EXE = "/home/ryen/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
SEARCH_URL = BASE + "/#/search"
QUERY = "档"  # 命中「午夜档案」：hongguo 全池可见 + adult_lab 本源自命中 → 两组有内容
EXPECTED_GROUPS = ["hongguo", "huangguo_ai", "adult_lab", "youzi"]

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
        return s.connect_ex(("127.0.0.1", 5173)) == 0


def wait_port(deadline=40.0):
    t0 = time.time()
    while time.time() - t0 < deadline:
        if port_open():
            return True
        time.sleep(0.4)
    return False


def kill_group(proc):
    """杀整个进程组（npm 壳不会把 SIGTERM 传给 vite 子进程，孤儿会占住 5173）"""
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


def settle_groups(page, timeout_ms=20000):
    """等所有组脱离 loading：每组出现真实卡片(.sa-card)或结果行(.sa-note 空/失败)"""
    page.wait_for_function(
        """() => {
            const gs = document.querySelectorAll('.sa-group');
            if (!gs.length) return false;
            return [...gs].every(g => g.querySelector('.sa-card') || g.querySelector('.sa-note'));
        }""",
        timeout=timeout_ms,
    )


def group_slugs(page):
    return page.evaluate(
        """() => [...document.querySelectorAll('.sa-group')].map(g => g.dataset.slug)"""
    )


def cards_in_group(page, slug):
    return page.evaluate(
        f"""() => document.querySelectorAll('.sa-group[data-slug="{slug}"] .sa-card').length"""
    )


def do_search(page, text=QUERY):
    page.fill(".search-bar .input-wrap input", text)
    page.click(".search-bar .btn.primary")


def main():
    # 0) dev server（谁拉起谁关闭；复用他人端口则结束保留）
    # 旧写法「谁拉起谁关闭；端口已开就复用」，而且 Popen 连 --port/--strictPort 都不带
    # ——复用别人的 5173 就等于在未知构建上测，正是端口串味的老问题。改走 _devserver。
    spawned = _devserver.start_dev(FRONTEND, 5173, True, HERE, name="searchall")

    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE, headless=True)
            ctx = browser.new_context(viewport={"width": 1280, "height": 800})
            page = ctx.new_page()
            js_errors, console_errors = [], []
            page.on("pageerror", lambda e: js_errors.append(str(e)))
            page.on(
                "console",
                lambda m: m.type == "error" and console_errors.append(m.text),
            )

            page.goto(SEARCH_URL)
            page.evaluate("localStorage.clear()")
            page.reload()
            page.wait_for_selector(".search-bar")

            # A1 未勾选 = 单源现状
            do_search(page)
            page.wait_for_selector(".pg-viewport", timeout=15000)
            n_poster = page.locator(".pg-viewport .poster").count()
            check("A1 未勾选搜索=单源 PosterGrid（现状不变）",
                  page.locator(".sa-group").count() == 0 and n_poster > 0,
                  f"cards={n_poster}")

            # A2/A6 勾选扇出 + 流式组头先渲染
            page.click("[data-testid=search-all]")
            do_search(page)
            page.wait_for_function(
                "document.querySelectorAll('.sa-group').length === 4", timeout=8000
            )
            early = page.evaluate(
                "() => document.querySelectorAll('.sa-group').length"
                " && !!document.querySelector('.sa-ph')"
            )
            settle_groups(page)
            slugs = group_slugs(page)
            check("A2 勾选扇出=4 组且排除未接入源 tianyuan",
                  slugs == EXPECTED_GROUPS, ",".join(slugs))
            check("A6 流式：组头/骨架先渲染不等齐（早期即有骨架）", bool(early))

            # A3 组头名 + 18+ 徽章
            heads = page.evaluate(
                """() => [...document.querySelectorAll('.sa-group')].map(g => ({
                      name: g.querySelector('.sa-name')?.textContent?.trim(),
                      badge: !!g.querySelector('.sa-head .sa-badge.adult')}))"""
            )
            want = {"hongguo": "红果短剧", "huangguo_ai": "黄果AI剧",
                    "adult_lab": "成人测试源", "youzi": "柚子"}
            names_ok = all(h["name"] == want[s] for h, s in zip(heads, EXPECTED_GROUPS))
            badges_ok = (heads[2]["badge"] and heads[3]["badge"]
                         and not heads[0]["badge"] and not heads[1]["badge"])
            check("A3 组头展示站源名 + adult 源 18+ 徽章", names_ok and badges_ok,
                  json.dumps(heads, ensure_ascii=False))

            # A4 红果置顶有序
            check("A4 组顺序=orderedSources 序（红果置顶）", slugs[0] == "hongguo",
                  ",".join(slugs))

            # A5 组内容计数
            c_hg = cards_in_group(page, "hongguo")
            c_ad = cards_in_group(page, "adult_lab")
            empty_notes = page.evaluate(
                """() => [...document.querySelectorAll('.sa-note.empty')].map(n =>
                      n.closest('.sa-group').dataset.slug)"""
            )
            check("A5 hongguo/adult_lab 各 21 卡，其余组空提示",
                  c_hg == 21 and c_ad == 21 and set(empty_notes) == {"huangguo_ai", "youzi"},
                  f"hongguo={c_hg} adult_lab={c_ad} empty={empty_notes}")
            page.screenshot(path=os.path.join(HERE, "searchall_groups.png"), full_page=False)

            # A7 失败注入：youzi 组失败行、剥 [sim_fault]，其余组不受影响
            page.evaluate("window.__zgjSearchFault = ['youzi']")
            do_search(page)
            settle_groups(page)
            youzi_note = page.evaluate(
                """() => document.querySelector('.sa-group[data-slug=youzi] .sa-note.fail')?.textContent || ''"""
            )
            ok7 = ("该源搜索失败" in youzi_note and "模拟源故障（youzi）" in youzi_note
                   and "sim_fault" not in youzi_note
                   and cards_in_group(page, "hongguo") == 21
                   and cards_in_group(page, "adult_lab") == 21)
            check("A7 单源失败→该组失败行(剥[code]尾缀)，其余组正常", ok7, youzi_note.strip())
            page.screenshot(path=os.path.join(HERE, "searchall_fault.png"))

            # A8 全组皆败→整体 error 条；清除钩子→重试恢复
            page.evaluate("window.__zgjSearchFault = ['*']")
            do_search(page)
            page.wait_for_selector(".sa-allfail", timeout=20000)
            bar = page.locator(".sa-allfail").inner_text()
            check("A8a 全组皆败出整体 error 条", "全部站源均搜索失败" in bar, bar.strip())
            page.screenshot(path=os.path.join(HERE, "searchall_allfail.png"))
            page.evaluate("window.__zgjSearchFault = []")
            page.click(".sa-allfail .sa-retry")
            settle_groups(page)
            check("A8b 清除钩子后重试全部→恢复 4 组内容",
                  cards_in_group(page, "hongguo") == 21
                  and page.locator(".sa-allfail").count() == 0)

            # A9 取消勾选 → 清空分组回单源
            page.click("[data-testid=search-all]")
            page.wait_for_selector(".pg-viewport", timeout=15000)
            check("A9 取消勾选清空分组态回单源",
                  page.locator(".sa-group").count() == 0
                  and page.locator(".pg-viewport .poster").count() > 0)
            page.screenshot(path=os.path.join(HERE, "searchall_single.png"))

            # A10 勾选态持久化（reload 保留）
            page.click("[data-testid=search-all]")
            do_search(page)
            settle_groups(page)
            page.reload()
            page.wait_for_selector(".search-bar")
            checked = page.evaluate(
                """() => document.querySelector('[data-testid=search-all]').checked"""
            )
            ls = page.evaluate("() => localStorage.getItem('zgj.search.all')")
            check("A10 勾选态持久化 reload 保留", checked and ls == "1", f"checked={checked} ls={ls}")

            # A11 跨源点击：adult_lab 组卡片 → 详情带对源（reload 后需重新搜索）
            do_search(page)
            settle_groups(page)
            page.locator('.sa-group[data-slug="adult_lab"] .sa-card').first.click()
            page.wait_for_url("**/#/detail/**", timeout=10000)
            url = page.url
            d_src = page.evaluate(
                """() => {
                    const q = new URLSearchParams(location.hash.split('?')[1] || '');
                    const d = q.get('d');
                    return d ? JSON.parse(d).source : '';
                }"""
            )
            title_ok = page.locator("text=午夜档案").count() > 0
            check("A11 跨源卡片点击路由带对源",
                  "/detail/adult_lab%3A" in url and d_src == "adult_lab" and title_ok,
                  f"url={url[-60:]} d.source={d_src}")
            page.screenshot(path=os.path.join(HERE, "searchall_detail.png"))

            # A12 无未捕获错误
            check("A12 无未捕获 JS 错误/console error",
                  not js_errors and not console_errors,
                  json.dumps({"js": js_errors[:2], "console": console_errors[:2]}, ensure_ascii=False))

            browser.close()
    finally:
        _devserver.stop_dev(spawned, 5173, name="searchall")

    print("=" * 46)
    if failures:
        print(f"RESULT: {len(failures)} FAILED -> {failures}")
        sys.exit(1)
    print("RESULT: ALL PASS (12 checks)")


if __name__ == "__main__":
    main()
