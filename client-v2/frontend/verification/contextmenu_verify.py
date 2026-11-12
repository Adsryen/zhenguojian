#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""V2-6 右键上下文菜单验证（headless chromium + VITE_MOCK dev server）。

自管理 dev:mock：独占端口 5196，启停走 `_devserver`（端口预检 + 严格收口）。
     旧写法要求人工「先起 5173」，且 5173 被多个脚本共用——没人起就报错、
     有人起就可能测到别人的服务器。
截图输出：本目录 contextmenu-copy.png / contextmenu-input.png。
断言以 PASS/FAIL 行打印，任何 FAIL 退出码非 0。
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)

from playwright.sync_api import sync_playwright

import _devserver

PORT = 5196
BASE = f"http://127.0.0.1:{PORT}"
EXE = "/home/ryen/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
OUT = os.path.dirname(os.path.abspath(__file__))

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line)
    if not cond:
        failures.append(name)


def item_labels(page):
    return page.eval_on_selector_all(
        ".zgj-ctx-item > span:first-child", "els=>els.map(e=>e.textContent)"
    )


def main():
    proc = _devserver.start_dev(FRONTEND, PORT, True, HERE, name='contextmenu')
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE)
            ctx = browser.new_context(viewport={"width": 1440, "height": 900})
            ctx.grant_permissions(["clipboard-read", "clipboard-write"], origin=BASE)
            page = ctx.new_page()

            # ---- 场景 A：选中文本 → 右键 → 复制 ----
            page.goto(BASE + "/#/discover")
            page.wait_for_selector(".page-title")
            selected = page.evaluate(
                """() => {
                  const el = document.querySelector('.page-title');
                  const r = document.createRange();
                  r.selectNodeContents(el);
                  const s = getSelection();
                  s.removeAllRanges();
                  s.addRange(r);
                  return s.toString();
                }"""
            )
            check("场景A 选区非空", len(selected.strip()) > 0, f"sel={selected!r}")
            box = page.locator(".page-title").bounding_box()
            page.mouse.click(box["x"] + box["width"] / 2, box["y"] + box["height"] / 2, button="right")
            page.wait_for_selector(".zgj-ctx-menu")
            labels = item_labels(page)
            check("场景A 菜单含「复制」", "复制" in labels, f"labels={labels}")
            check("场景A 菜单含「全选」", "全选" in labels)
            hints = page.eval_on_selector_all(".zgj-ctx-key", "els=>els.map(e=>e.textContent)")
            check("场景A 快捷键提示", hints[0] == "Ctrl+C", f"hints={hints}")
            page.screenshot(path=os.path.join(OUT, "contextmenu-copy.png"))
            page.locator(".zgj-ctx-item", has_text="复制").click()
            check("场景A 点击后菜单关闭", page.locator(".zgj-ctx-menu").count() == 0)
            clip = page.evaluate("navigator.clipboard.readText()")
            check("场景A 剪贴板==选区", clip == selected, f"clip={clip!r}")

            # Escape 关闭断言（菜单重开后按 Escape）
            page.mouse.click(box["x"] + box["width"] / 2, box["y"] + box["height"] / 2, button="right")
            page.wait_for_selector(".zgj-ctx-menu")
            page.keyboard.press("Escape")
            check("场景A Escape 关闭", page.locator(".zgj-ctx-menu").count() == 0)

            # 纯媒体区无选区不弹（任务书空菜单路径）
            page.evaluate(
                """() => {
                  const s = getSelection();
                  s.removeAllRanges();
                  const r = document.createRange();
                  const img = document.querySelector('img');
                  if (img) { r.setStart(img, 0); r.collapse(true); }
                }"""
            )
            img = page.locator("img").first
            if img.count() > 0:
                ib = img.bounding_box()
                if ib:
                    page.mouse.click(ib["x"] + ib["width"] / 2, ib["y"] + ib["height"] / 2, button="right")
                    page.wait_for_timeout(120)
                    check("媒体区无选区不弹菜单", page.locator(".zgj-ctx-menu").count() == 0)

            # ---- 场景 B：输入框 → 全选 → 右键三项 → 剪切/粘贴 ----
            page.goto(BASE + "/#/search")
            page.wait_for_selector(".input-wrap input")
            inp = page.locator(".input-wrap input")
            inp.click()
            inp.type("测试剪切板")
            page.keyboard.press("Control+a")
            ib = inp.bounding_box()
            # 点击点落在选中文本内部（5 个 CJK ≈ 70px 宽），保选择不坍缩
            page.mouse.click(ib["x"] + 36, ib["y"] + ib["height"] / 2, button="right")
            page.wait_for_selector(".zgj-ctx-menu")
            labels2 = item_labels(page)
            check(
                "场景B 输入框三项=剪切/粘贴/全选",
                labels2 == ["剪切", "粘贴", "全选"],
                f"labels={labels2}",
            )
            page.screenshot(path=os.path.join(OUT, "contextmenu-input.png"))
            page.keyboard.press("Escape")
            check("场景B Escape 关闭", page.locator(".zgj-ctx-menu").count() == 0)

            # 剪切：内容进剪贴板且输入框清空
            page.mouse.click(ib["x"] + 36, ib["y"] + ib["height"] / 2, button="right")
            page.wait_for_selector(".zgj-ctx-menu")
            page.locator(".zgj-ctx-item", has_text="剪切").click()
            page.wait_for_timeout(200)
            val = inp.input_value()
            check("场景B 剪切后输入框清空", val == "", f"val={val!r}")
            clip2 = page.evaluate("navigator.clipboard.readText()")
            check("场景B 剪贴板==剪切文本", clip2 == "测试剪切板", f"clip={clip2!r}")

            # 粘贴：剪贴板内容回写输入框
            page.mouse.click(ib["x"] + 36, ib["y"] + ib["height"] / 2, button="right")
            page.wait_for_selector(".zgj-ctx-menu")
            page.locator(".zgj-ctx-item", has_text="粘贴").click()
            page.wait_for_timeout(200)
            val2 = inp.input_value()
            check("场景B 粘贴回填输入框", val2 == "测试剪切板", f"val={val2!r}")

            browser.close()

        if failures:
            print(f"\nRESULT: {len(failures)} FAIL -> {failures}")
            sys.exit(1)
        print("\nRESULT: ALL PASS")


    finally:
        _devserver.stop_dev(proc, PORT, name='contextmenu')
if __name__ == "__main__":
    main()
