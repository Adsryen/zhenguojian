#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""detail_reserve_verify.py —— 详情页「预约」按钮（R21d：对账查出的入口缺失）

v1 详情页头部有预约按钮（`main.py:174-176` 建按钮，`main.py:440-454` 定四态文案）；
v2 之前**整个详情页没有预约入口**，只有日历页能预约（`stores/calendar.ts:252`）。
本轮补上按钮，并把四态文案照 v1 的规则搬过来：

```
reserved                  → 「✓ 已预约」
episodes <= 0             → 「☆ 预约上线」
releaseStatus == finished → 「☆ 预约下一季」
其余                       → 「☆ 预约更新」
```

⚠ 两处刻意不与 v1 逐字相同，理由都在 `services/i18n.ts` 的注释里：
1. v1 成功提示「已预约；应用运行期间每 30 分钟检查一次，上线/更新后会在「追更预约」页提示」
   ——后半句在 v2 **还没有**：Go 里有 `Store.ObserveDrama`（store.go:1176），
   但**没有任何调用方**（既无定时器也无绑定），所以照抄就是许一个不会兑现的承诺。
   v2 现在只说「已预约《{t}》」，tooltip 干脆写明「上线/更新提醒尚未接通」。
2. v1 的「追更预约」页在 v2 被拆成两页（追更预约=追剧列表，新剧预约=日历），
   所以不能沿用 v1 的页名。
   → ⑦ 把这句"不许诺"钉成断言：将来 R23 接通后台检查时，改这条测试要写得有意，
     而不是悄悄把承诺放回去。

断言：
  ① ongoing 剧 → 「☆ 预约更新」
  ② finished 剧 → 「☆ 预约下一季」（fixtures 里 releaseStatus 只有 finished/ongoing 各一条）
  ③ 点击 → 「✓ 已预约」+ toast「已预约《一世之尊》」
  ④ 刷新后仍「✓ 已预约」（真值持久化，不是内存态）
  ⑤ v1 互斥语义：已追剧时点预约 → 追剧按钮回到「＋ 追剧」
  ⑥ 取消：点「✓ 已预约」→ 回到「☆ 预约更新」+ toast「已取消预约」
  ⑦ en 态标签为英文，且 tooltip 仍写着"尚未接通"（不许悄悄放回承诺）
  ⑧ 「☆ 预约上线」分支**在 mock 里造不出来**：`mockDetail` 恒返回分集，
     `episodes` 不可能为 0。这一支只在真机/未上线剧上成立 —— 所以这里只如实记录，
     不为了凑数去伪造数据。

端口：自管理 mock vite @5206（`_devserver`）。
"""
import os
import re
import sys

import _devserver
from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = "5206"
BASE = f"http://127.0.0.1:{PORT}"
EXE = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell")
OUT = HERE

ONGOING = f"{BASE}/#/detail/hongguo%3A7439220582928952383"    # 一世之尊（ongoing）
FINISHED = f"{BASE}/#/detail/hongguo%3A7421530981234567890"   # 闪婚老公是大佬（finished）
BTN = "[data-testid=detail-reserve]"

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


def clear_state(page):
    """起点清零。**必须在导航之前**做：
    store 是模块加载时就绪的，页面打开后再删 localStorage 不会影响
    已经读进内存的 follows —— 先清后开才真的清（第一版顺序写反，白清一遍）。"""
    page.goto(BASE + "/#/discover")
    # 必须等应用真的起来再改 hash：
    # 上一版这里立刻 goto 详情页，而路由还没就绪时"只有 hash 变"的导航会被吞掉
    # ——现场是 URL 停在 /#/discover、detail-reserve 按钮数 0，
    # 看起来像"按钮没渲染"，其实根本没导航过去（我绕了两圈才靠打现场看清）。
    page.wait_for_selector(".page-title", timeout=20000)
    page.evaluate("""() => {
      for (const k of Object.keys(localStorage)) {
        if (k.includes('follow') || k.includes('reserv') || k.includes('watch')) localStorage.removeItem(k);
      }
    }""")


def wait_btn(page, timeout=20000):
    """等按钮出现；超时就先把现场打出来再抛——
    不看现场就改代码，等于靠猜（本轮第一次失败就是这么绕了两圈）。"""
    try:
        page.wait_for_selector(BTN, timeout=timeout)
    except Exception:
        print("  [现场] url=", page.url)
        print("  [现场] 按钮数=", page.locator(BTN).count())
        print("  [现场] .btn 前 8 个=", page.evaluate(
            "() => [...document.querySelectorAll('.btn')].map(x => x.innerText.trim()).slice(0, 8)"))
        print("  [现场] body 头 100=", page.evaluate("() => document.body.innerText.slice(0, 100)"))
        raise


def open_detail(page, url, locale=None):
    clear_state(page)
    if locale:
        page.evaluate("(l) => localStorage.setItem('zgj.locale', l)", locale)
    page.goto(url)
    wait_btn(page)
    if locale:
        page.reload()          # 只改 hash 不重载文档，i18n 是模块加载时读的
        wait_btn(page)
    page.wait_for_timeout(1200)


def btn(page):
    return page.locator(BTN).inner_text().strip()


def toast(page):
    el = page.locator(".toast")
    return el.first.inner_text().strip() if el.count() else ""


def main():
    proc = _devserver.start_dev(FRONTEND, PORT, True, HERE, name="detail-reserve")
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE, headless=True)
            page = browser.new_context(viewport={"width": 1280, "height": 860}).new_page()

            # ── ① ongoing → 预约更新 ──
            open_detail(page, ONGOING)
            check("① ongoing 剧的按钮是「☆ 预约更新」（v1 main.py:452 的规则）",
                  btn(page) == "☆ 预约更新", btn(page))

            # ── ② finished → 预约下一季 ──
            open_detail(page, FINISHED)
            check("② finished 剧的按钮是「☆ 预约下一季」",
                  btn(page) == "☆ 预约下一季", btn(page))

            # ── ③ 点击预约 ──
            open_detail(page, ONGOING)
            page.locator(BTN).click()
            page.wait_for_timeout(900)
            check("③a 点击后按钮变「✓ 已预约」", btn(page) == "✓ 已预约", btn(page))
            check("③b toast 是「已预约《一世之尊》」（不带未接通的承诺）",
                  "已预约《一世之尊》" in toast(page), toast(page))

            # ── ④ 持久化 ──
            page.reload()
            page.wait_for_selector(BTN, timeout=20000)
            page.wait_for_timeout(1400)
            check("④ 刷新后仍「✓ 已预约」（真值在 watchstore，不是内存态）",
                  btn(page) == "✓ 已预约", btn(page))

            # ── ⑤ v1 互斥：预约同时取消追剧 ──
            follow = page.locator("button").filter(has_text="追剧").first
            before = follow.inner_text().strip()
            follow.click()
            page.wait_for_timeout(800)
            now_follow = page.locator("button").filter(has_text="追剧").first.inner_text().strip()
            reserved_now = btn(page)
            if "已追剧" in now_follow:
                # 已追剧状态下再点预约：v1 watchstore.py:224-241 预约同时取消追剧
                page.locator(BTN).click()
                page.wait_for_timeout(900)
                after = page.locator("button").filter(has_text="追剧").first.inner_text().strip()
                check("⑤ 已追剧时点预约 → 追剧被取消（v1 互斥语义）",
                      "已追剧" not in after, f"预约前追剧按钮={now_follow} → 点预约后={after}")
            else:
                check("⑤ 已追剧时点预约 → 追剧被取消（v1 互斥语义）", False,
                      f"没能进入已追剧态：before={before} after={now_follow}")

            # ── ⑥ 取消预约 ──
            if btn(page) != "✓ 已预约":
                page.locator(BTN).click()
                page.wait_for_timeout(800)
            page.locator(BTN).click()
            page.wait_for_timeout(900)
            check("⑥a 取消后回到「☆ 预约更新」", btn(page) == "☆ 预约更新", btn(page))
            check("⑥b 取消的 toast 是「已取消预约」（v1 main.py:1541 原文）",
                  "已取消预约" in toast(page), toast(page))

            # ── ⑦ en 态 + 不许诺 ──
            open_detail(page, ONGOING, locale="en")
            label_en = btn(page)
            tip_en = page.locator(BTN).get_attribute("title") or ""
            check("⑦a en 态标签是英文", re.search(r"[一-鿿]", label_en) is None, label_en)
            check("⑦b tooltip 明说提醒未接通（R23 之前不许把承诺放回 UI）",
                  "not wired" in tip_en or "尚未接通" in tip_en, tip_en)

            page.screenshot(path=os.path.join(OUT, "detail-reserve.png"))

            print("NOTE ⑧「☆ 预约上线」分支没断言："
                  "mockDetail 恒返回分集，episodes 不可能为 0，这一支只能在真机/未上线剧上验；"
                  "为了凑数伪造数据比少一条断言更糟。")
            browser.close()
    finally:
        _devserver.stop_dev(proc, PORT, name="detail-reserve")

    print(f"\n===== detail_reserve_verify: {'ALL PASS' if not failures else 'FAILURES: ' + ', '.join(failures)} =====")
    print(f"screenshot -> {os.path.join(OUT, 'detail-reserve.png')}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
