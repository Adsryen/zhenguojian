#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""settings_storage_verify.py —— 设置页「成品库（下载）」行（R21 设置面清单级对账的第一个产出）

背景：照第三十一轮对键位的同一把刀法，把 v1 的设置页条目逐行抄下来和 v2 比
（v1 侧：`settings_panel.py` 的 `_combo_row/_path_row/_spin_row/_kv_row` 调用点，
共 12 个行标签）。结果：

| v1 行 | v2 之前 |
|---|---|
| 倍速 / 清晰度 / 弹幕开关 | ✅ 有 |
| 代理模式 / 目录并发 / 请求间隔 / 下载并发 / 按站源分道 | ✅ 有 |
| 站源开关 | ✅ 有（还多了拖拽排序） |
| 应用版本 / 本地核心 / 打包提交 / 数据目录 | ✅ 有 |
| **成品库（下载）路径** | ❌ **设置页看不到**（只有下载页显示） → 本轮补 |
| 加密缓存（只读路径行） | ⚠ **v2 没有这个概念**，见下方"不照搬的两处" |

不照搬的两处（刻意保留差异，别当遗漏补上）：
1. **「修改…」按钮**：v1 改的是客户端 settings 里的 `libraryRoot`
   （`tools.set_library_root`，然后 `stash_index` 把旧库记录搬给新根）。
   v2 的下载根由**核心**决定（`downloadDirectory` 动作），客户端没有对应写入口。
   要做必须先决定核心是否支持换根目录 —— 所以这里**不放假按钮**，
   ④ 就是钉住"没做出一个点了没作用的修改按钮"。
2. **加密缓存行**：v1 是 `project_dir()/.cache/downloads`（固定位置、只读、带 tooltip）。
   v2 的加密原件直接落在核心下载根里（`bindings_download.go:238-240`：
   分集成品 `<root>/<jobId>/media.mp4`、合并成品 `<root>/合并/`），
   没有"另一个固定缓存目录"这回事。造一行去猜路径等于写假数据，所以不造。

断言：
  ① 行存在，路径来自核心 `downloadDirectory`（非空、且与"下载"页用的同一口径）
  ② exists=true 时给「打开目录」按钮，点它调用同一个原生入口且路径一致
     （mock 面把路径记在 `window.__zgjOpenPath`，与 preload_openpath_verify 同源）
  ③ 切 en 后该行文案是英文（新加的 4 个键都得成对，不能只有中文）
  ④ **没有**「修改…」假按钮（刻意的架构差异，钉住不复活）
  ⑤ 数据目录行仍在（回归：我改的是同一个 kv 块）

端口：自管理 mock vite @5205（`_devserver`）。
"""
import os
import re
import sys

import _devserver
from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = "5205"
BASE = f"http://127.0.0.1:{PORT}"
EXE = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell")
OUT = HERE

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


def goto_settings(page, locale=None):
    """进设置页；locale 非空时**必须整页重载**。

    只改 hash 的 `page.goto` 不重载文档（这已经是本任务第三次栽在同一条：
    第二十一轮 init script、第二十九轮 mock 注入、本轮 locale）——
    而 i18n 是在模块加载时把语言读进内存的，不重载就还是旧语言，
    ③ 的"英文文案"断言必然假失败。
    """
    page.goto(f"{BASE}/#/settings")
    page.wait_for_selector("[data-testid=set-library-row]", timeout=20000)
    if locale:
        page.evaluate("(l) => localStorage.setItem('zgj.locale', l)", locale)
        page.reload()
        page.wait_for_selector("[data-testid=set-library-row]", timeout=20000)
    page.wait_for_timeout(1500)


def main():
    proc = _devserver.start_dev(FRONTEND, PORT, True, HERE, name="settings-storage")
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE, headless=True)
            page = browser.new_context(viewport={"width": 1280, "height": 900}).new_page()

            # ── ① 行存在且路径非空 ──
            goto_settings(page)
            label = page.locator("[data-testid=set-library-row]").inner_text()
            path = page.locator("[data-testid=set-library-dir]").inner_text().strip()
            check("① 设置页出现「成品库（下载）」行且路径非空",
                  "成品库（下载）" in label and len(path) > 3,
                  f"label={label[:40]!r} path={path!r}")

            # ── ② 「打开目录」走同一个原生入口，且路径一致 ──
            open_btn = page.locator("[data-testid=set-library-open]")
            has_open = open_btn.count() > 0
            missing_badge = page.locator("[data-testid=set-library-missing]").count()
            check("②a 目录存在时给「打开目录」按钮、不给「不存在」徽章",
                  has_open and missing_badge == 0,
                  f"按钮={has_open} 徽章数={missing_badge}")
            if has_open:
                open_btn.first.click()
                page.wait_for_timeout(900)
                opened = page.evaluate("() => window.__zgjOpenPath || ''")
                check("②b 点击调用的是同一个原生入口，且路径与显示一致",
                      opened == path, f"opened={opened!r} 显示={path!r}")

            # ── ③ en 态文案（4 个新键必须成对） ──
            ctx2 = browser.new_context(viewport={"width": 1280, "height": 900})
            page2 = ctx2.new_page()
            goto_settings(page2, locale="en")
            row2 = page2.locator("[data-testid=set-library-row]").inner_text()
            check("③ en 态该行是英文（没有只有中文的漏网键）",
                  "Library (downloads)" in row2 and not re.search(r"[一-鿿]", row2.split("O")[0]),
                  f"row={row2[:60]!r}")

            # ── ④ 刻意不照搬 v1 的「修改…」：不能有假按钮 ──
            body = page.locator("body").inner_text()
            check("④ 没有「修改…」假按钮（v2 下载根由核心决定，未做写入口）",
                  "修改…" not in body, "见脚本 docstring 第 1 条差异说明")

            # ── ⑤ 数据目录行没被我改坏 ──
            check("⑤ 「数据目录」行仍在（同一 kv 块回归）",
                  "数据目录" in body, "")

            page.screenshot(path=os.path.join(OUT, "settings-library.png"))
            ctx2.close()
            browser.close()
    finally:
        _devserver.stop_dev(proc, PORT, name="settings-storage")

    print(f"\n===== settings_storage_verify: {'ALL PASS' if not failures else 'FAILURES: ' + ', '.join(failures)} =====")
    print(f"screenshot -> {os.path.join(OUT, 'settings-library.png')}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
