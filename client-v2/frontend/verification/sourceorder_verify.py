#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""站源卡片手动拖拽排序验证（headless chromium + VITE_MOCK dev server）。

自管理 dev:mock：**独占端口 5193**，启停走 `_devserver`（端口预检 + 退出回查）。
     旧写法要求人工「先起 5173 的 dev:mock」，没起就在 `/#/settings` 导航上超时——
     报错长得像页面坏了，其实只是没人起服务器；5173 还被 contextmenu/hitbands/searchall
     共用，串味风险叠加。
断言：
  A  默认/持久化/容错：坏档→默认序；存序生效；未知 slug 忽略；清键→红果置顶。
  B  指针拖拽：第 3 张卡拖到第 1 位 → DOM 序变化 + localStorage 落盘 + 刷新保持。
  C  SourceSwitch 弹层菜单顺序与设置页一致。
  D  点在卡片开关热区不触发拖拽（按下后拖 30px 顺序不变），普通 click 正常切换。
截图输出：本目录 sourceorder-drag.png / sourceorder-after.png。
断言以 PASS/FAIL 行打印，任何 FAIL 退出码非 0。
"""
import json
import os
import sys

from playwright.sync_api import sync_playwright

import _devserver

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)  # client-v2/frontend

PORT = 5193
BASE = f"http://127.0.0.1:{PORT}"
EXE = "/home/ryen/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
OUT = os.path.dirname(os.path.abspath(__file__))
LS_KEY = "zgj.source-order"

# mock fixtures registry 原序（hongguo 本就在首）
DEFAULT_ORDER = ["hongguo", "huangguo_ai", "adult_lab", "youzi", "tianyuan"]
SHUFFLED = ["youzi", "tianyuan", "hongguo", "huangguo_ai", "adult_lab"]
UNKNOWN_EXPECT = ["adult_lab", "hongguo", "youzi", "huangguo_ai", "tianyuan"]
# B 拖拽后：第 3 张（adult_lab）到第 1 位
AFTER_B = ["adult_lab", "hongguo", "huangguo_ai", "youzi", "tianyuan"]

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line)
    if not cond:
        failures.append(name)


def dom_order(page):
    return page.eval_on_selector_all(
        ".sg-item", "els=>els.map(e=>e.dataset.slug)"
    )


def ls_raw(page):
    return page.evaluate(f"() => localStorage.getItem({json.dumps(LS_KEY)})")


def ls_order(page):
    raw = ls_raw(page)
    try:
        return json.loads(raw) if raw else None
    except Exception:
        return None


def set_ls(page, value):
    page.evaluate(
        "(v) => localStorage.setItem('zgj.source-order', v)", json.dumps(value)
    )


def clear_ls(page):
    page.evaluate("localStorage.removeItem('zgj.source-order')")


def goto_settings(page):
    page.goto(BASE + "/#/settings")
    page.wait_for_selector(".sg-item", timeout=8000)
    page.wait_for_timeout(120)  # 入场过渡稳定


def main():
    assert os.path.exists(EXE), f"headless shell 不存在: {EXE}"
    proc = _devserver.start_dev(FRONTEND, PORT, True, HERE, name="sourceorder")
    try:
        run_all(proc)
    finally:
        _devserver.stop_dev(proc, PORT, name="sourceorder")


def run_all(proc):
    with sync_playwright() as p:
        browser = p.chromium.launch(executable_path=EXE)
        ctx = browser.new_context(viewport={"width": 1440, "height": 900})
        page = ctx.new_page()
        page.goto(BASE + "/#/settings")
        page.wait_for_selector(".sg-item", timeout=8000)

        # ---------- A：默认 / 持久化 / 容错 ----------
        # 真·语法坏档：非 JSON 文本（set_ls 会 dumps 包引号，此处必须裸写）
        page.evaluate("localStorage.setItem('zgj.source-order', String.fromCharCode(123) + 'oops')")
        page.reload()
        page.wait_for_selector(".sg-item", timeout=8000)
        got = dom_order(page)
        check("A0 坏档(非法JSON)回退默认序", got == DEFAULT_ORDER, f"got={got}")

        set_ls(page, SHUFFLED)
        page.reload()
        page.wait_for_selector(".sg-item", timeout=8000)
        got = dom_order(page)
        check("A1 已存 order 生效", got == SHUFFLED, f"got={got}")

        set_ls(page, ["adult_lab", "zzz_unknown", "hongguo", "youzi", "huangguo_ai", "tianyuan"])
        page.reload()
        page.wait_for_selector(".sg-item", timeout=8000)
        got = dom_order(page)
        check("A2 order 中未知 slug 忽略、其余按序", got == UNKNOWN_EXPECT, f"got={got}")

        clear_ls(page)
        page.reload()
        page.wait_for_selector(".sg-item", timeout=8000)
        got = dom_order(page)
        check("A3 清键后默认序：红果 hongguo 置顶", got[0] == "hongguo" and got == DEFAULT_ORDER, f"got={got}")

        # ---------- B：拖第 3 张卡到第 1 位 ----------
        b3 = page.locator(".sg-item").nth(2).bounding_box()
        b1 = page.locator(".sg-item").nth(0).bounding_box()
        x3, y3 = b3["x"] + b3["width"] / 2, b3["y"] + b3["height"] / 2
        x1, y1 = b1["x"] + b1["width"] / 2, b1["y"] + b1["height"] / 2

        page.mouse.move(x3, y3)
        page.mouse.down()
        page.mouse.move((x3 + x1) / 2, y3, steps=8)  # 越过 6px 阈值 → dragging
        ghost = page.locator(".sg-ghost").count()
        ph = page.locator(".sg-placeholder").count()
        page.mouse.move(x1, y1, steps=12)  # ghost 中心落到槽 0
        page.wait_for_timeout(220)  # 等占位 left/top 120ms 过渡稳定，避免测到中间态
        tr0 = page.eval_on_selector(".sg-item", "e=>e.style.transform")
        ph_box = page.locator(".sg-placeholder").bounding_box() if ph else None
        ph_over_slot1 = bool(
            ph_box and abs((ph_box["x"] + ph_box["width"] / 2) - (b1["x"] + b1["width"] / 2)) < 20
            and abs((ph_box["y"] + ph_box["height"] / 2) - (b1["y"] + b1["height"] / 2)) < 20
        )
        check("B1 拖拽中：ghost+占位可见", ghost == 1 and ph == 1, f"ghost={ghost} ph={ph}")
        check("B1b 首卡被顶开（translate 位移）", bool(tr0) and tr0 != "none", f"tr0={tr0!r}")
        check("B1c 占位落在第 1 槽", ph_over_slot1)
        page.screenshot(path=os.path.join(OUT, "sourceorder-drag.png"))
        page.mouse.up()
        page.wait_for_timeout(250)

        got = dom_order(page)
        check("B2 落位后 DOM 序正确", got == AFTER_B, f"got={got}")
        stored = ls_order(page)
        check("B3 localStorage 落盘", stored == AFTER_B, f"stored={stored}")
        check("B4 拖拽后无 ghost 残留", page.locator(".sg-ghost").count() == 0)

        page.reload()
        page.wait_for_selector(".sg-item", timeout=8000)
        got = dom_order(page)
        check("B5 刷新后顺序保持", got == AFTER_B, f"got={got}")
        page.screenshot(path=os.path.join(OUT, "sourceorder-after.png"))

        # ---------- C：SourceSwitch 弹层顺序一致 ----------
        page.goto(BASE + "/#/discover")
        page.wait_for_selector(".ss-btn", timeout=8000)
        page.click(".ss-btn")
        page.wait_for_selector(".ss-panel", timeout=3000)
        menu = page.eval_on_selector_all(".ss-item", "els=>els.map(e=>e.dataset.slug)")
        check("C 弹层菜单顺序与设置页一致", menu == AFTER_B, f"menu={menu}")
        page.keyboard.press("Escape")

        # ---------- D：开关热区不触发拖拽 ----------
        goto_settings(page)
        order0 = dom_order(page)
        ls0 = ls_raw(page)
        sw = page.locator('.sg-item[data-slug="youzi"] .sc-switch')
        aria0 = sw.get_attribute("aria-checked")
        sb = sw.bounding_box()
        cx, cy = sb["x"] + sb["width"] / 2, sb["y"] + sb["height"] / 2
        page.mouse.move(cx, cy)
        page.mouse.down()
        page.mouse.move(cx + 30, cy + 30, steps=6)
        page.mouse.up()
        page.wait_for_timeout(200)
        d_ghost = page.locator(".sg-ghost").count()
        d_order = dom_order(page)
        d_aria = page.locator('.sg-item[data-slug="youzi"] .sc-switch').get_attribute("aria-checked")
        check("D1 开关上按住拖动不进入拖拽（顺序不变）", d_order == order0 and d_ghost == 0,
              f"order={d_order} ghost={d_ghost}")
        check("D1b 开关上拖出不误切换", d_aria == aria0, f"aria: {aria0}->{d_aria}")

        page.click('.sg-item[data-slug="youzi"] .sc-switch')
        try:
            page.wait_for_function(
                """(v) => { const el = document.querySelector('.sg-item[data-slug="youzi"] .sc-switch');
                            return el && el.getAttribute('aria-checked') !== v; }""",
                arg=aria0,
                timeout=3000,
            )
            flipped = True
        except Exception:
            flipped = False
        after_order = dom_order(page)
        check("D2 普通点击开关正常切换", flipped, f"aria0={aria0}")
        check("D2b 切换后顺序与落盘不变", after_order == order0 and ls_raw(page) == ls0)

        browser.close()

    print("-" * 40)
    if failures:
        print(f"RESULT: FAIL ({len(failures)}) -> {failures}")
        sys.exit(1)
    print("RESULT: ALL PASS")


if __name__ == "__main__":
    main()
