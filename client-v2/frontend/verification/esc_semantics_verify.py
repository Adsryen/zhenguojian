#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""esc_semantics_verify.py —— Esc 键分层语义（第三十一轮补的两处 v1 对齐）

背景：系统性对了一遍 v1 的快捷键清单后，发现 v2 少**两整条**语义
（v1 侧证据：`main.py:1765-1771`、`main.py:1774-1784`、`player.py:925-941/1321-1326`）：

1. v1 `_on_main_esc`：主窗口这边按 Esc，**仅当详情覆盖页开着 = 返回**。
   v2 之前详情页按 Esc 完全没有反应。
2. v1 `_on_playback_esc`：播放窗 Esc 的次序是「全屏 → 退全屏，否则关窗（关窗即停播）」。
   v2 只做到退全屏，非全屏时 Esc 什么也不做。

同时立了一条新契约（`services/overlays.ts`）：**浮层优先**。
右键菜单是命令式 DOM + 自己在捕获阶段听 keydown，
如果页面级 Esc 不先看有没有浮层，就会出现"一次按键干两件事"——
菜单被关掉的同时页面也返回了，用户看起来像"按 Esc 直接跳走"。
这条不是假想风险：② 就是专门钉它的。

断言（全部走真实 DOM 与真实路由）：
  ① 详情页 Esc = 返回（离开 /#/detail）
  ② 详情页 + 右键菜单开着：第一次 Esc 只关菜单、**仍在详情页**；第二次 Esc 才返回
  ③ 播放器页 Esc（非全屏、无浮层）= 关播放器（v1 那条被漏掉的分支）
  ④ 播放器页先按 ? 出帮助浮层：Esc 只收浮层，人还在播放器页（v1 帮助优先级最高）
  ⑤ 到 /#/settings 按 Esc：仍停在 settings ——证明监听器随 DetailView 卸载摘干净，
     没变成全局副作用（这条是防"我修的比坏的更多"）

⚠ 断言一律写成"回到了 /#/discover"，不写成"离开了 detail/player"：
  Playwright 新标签的首页是 about:blank，直接 goto 目标页后 `router.back()` 会退到 about:blank，
  那时"不在详情页"这种否定式断言会**假绿**。本脚本第一版就是这样被骗过去的。

端口：自管理 mock vite @5204（`_devserver`）。
"""
import os
import sys

import _devserver
from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = "5204"
BASE = f"http://127.0.0.1:{PORT}"
EXE = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell")
OUT = HERE

DETAIL = f"{BASE}/#/detail/hongguo%3A7439220582928952383"
PLAYER = f"{BASE}/#/player?d=mock_drama_1&ep=1"

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


def select_text_of(page, selector):
    """给标题建真实选区（v2 的右键菜单按"有没有选中文本"决定给不给「复制」）"""
    return page.evaluate(
        """(sel) => {
          const el = document.querySelector(sel);
          if (!el) return false;
          const r = document.createRange();
          r.selectNodeContents(el);
          const s = getSelection();
          s.removeAllRanges();
          s.addRange(r);
          return s.toString().length > 0;
        }""",
        selector,
    )


def main():
    proc = _devserver.start_dev(FRONTEND, PORT, True, HERE, name="esc")
    shots = []
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE, headless=True)
            page = browser.new_context(viewport={"width": 1280, "height": 760}).new_page()

            # ── ① 详情页 Esc = 返回 ──
            # 先进 discover 再进 detail：这样"返回"有明确的目的地可断。
            # （直接 goto 详情页时，Playwright 的新标签首页是 about:blank，
            #   router.back() 会退到 about:blank，
            #   那时"URL 不含 /#/detail"会**假绿**——本仓第一次跑就是这样。）
            page.goto(f"{BASE}/#/discover")
            page.wait_for_timeout(1500)
            page.goto(DETAIL)
            page.wait_for_selector(".title", timeout=20000)
            page.wait_for_timeout(1200)
            check("①前置 已经在详情页", "/#/detail/" in page.url, page.url)
            page.keyboard.press("Escape")
            page.wait_for_timeout(900)
            check("① 详情页按 Esc = 返回（v1 _on_main_esc）",
                  "/#/discover" in page.url, page.url)

            # ── ② 浮层优先：菜单开着时，Esc 只关菜单 ──
            page.goto(f"{BASE}/#/discover")
            page.wait_for_timeout(1200)
            page.goto(DETAIL)
            page.wait_for_selector(".title", timeout=20000)
            page.wait_for_timeout(1200)
            has_sel = select_text_of(page, ".title")
            box = page.locator(".title").bounding_box()
            page.mouse.click(box["x"] + box["width"] / 2, box["y"] + box["height"] / 2, button="right")
            menu_visible = False
            try:
                page.wait_for_selector(".zgj-ctx-menu", timeout=6000)
                menu_visible = True
            except Exception:
                menu_visible = False
            check("②前置 详情页右键能出菜单", menu_visible, f"选区={has_sel}")
            if menu_visible:
                page.keyboard.press("Escape")
                page.wait_for_timeout(700)
                gone = page.locator(".zgj-ctx-menu").count() == 0
                still_detail = "/#/detail/" in page.url
                check("②a 第一次 Esc 只关菜单，页面**不**返回（浮层优先）",
                      gone and still_detail, f"菜单消失={gone} url={page.url}")
                page.screenshot(path=os.path.join(OUT, "esc-menu-closed.png"))
                shots.append("esc-menu-closed.png")
                page.keyboard.press("Escape")
                page.wait_for_timeout(900)
                check("②b 菜单已关后，第二次 Esc 才执行返回",
                      "/#/discover" in page.url, page.url)

            # ── ③ 播放器 Esc = 关窗（v1 非全屏分支）──
            page.goto(f"{BASE}/#/discover")
            page.wait_for_timeout(1200)
            page.goto(PLAYER)
            page.wait_for_selector(".pl-title", timeout=20000)
            page.wait_for_timeout(1500)
            check("③前置 播放器页已就绪", "/#/player" in page.url, page.url)
            page.keyboard.press("Escape")
            page.wait_for_timeout(1200)
            check("③ 播放器非全屏按 Esc = 关播放器（v1 _on_playback_esc 的 else 分支）",
                  "/#/discover" in page.url, page.url)

            # ── ④ 帮助浮层优先：按 ? 出浮层，Esc 只收浮层 ──
            page.goto(PLAYER)
            page.wait_for_selector(".pl-title", timeout=20000)
            page.wait_for_timeout(1500)
            page.keyboard.press("?")
            page.wait_for_timeout(600)
            help_on = page.locator(".hk-scrim").count() > 0
            check("④前置 按 ? 出帮助浮层", help_on, f".hk-scrim={page.locator('.hk-scrim').count()}")
            if help_on:
                page.keyboard.press("Escape")
                page.wait_for_timeout(700)
                check("④ Esc 先收帮助浮层，人还在播放器页（v1 帮助优先级最高）",
                      page.locator(".hk-scrim").count() == 0 and "/#/player" in page.url,
                      f"浮层={page.locator('.hk-scrim').count()} url={page.url}")
                page.screenshot(path=os.path.join(OUT, "esc-help-closed.png"))
                shots.append("esc-help-closed.png")

            # ── ⑤ 离开详情页后监听器不该继续影响别的页面 ──
            page.goto(f"{BASE}/#/settings")
            page.wait_for_timeout(1500)
            page.keyboard.press("Escape")
            page.wait_for_timeout(800)
            check("⑤ settings 按 Esc 仍在 settings（监听器随组件卸载摘干净，没变成全局副作用）",
                  "/#/settings" in page.url, page.url)

            browser.close()
    finally:
        _devserver.stop_dev(proc, PORT, name="esc")

    print(f"\n===== esc_semantics_verify: {'ALL PASS' if not failures else 'FAILURES: ' + ', '.join(failures)} =====")
    for sh in shots:
        print(f"screenshot -> {os.path.join(OUT, sh)}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
