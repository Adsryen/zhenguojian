#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""danmaku_toggle_verify.py —— 「下载时保存弹幕」勾选框（R22a 的最后一环）

前两轮把 Go 侧凑齐了（写旁挂文件的 `danmaku.CollectSidecar`、本地播放挂同名 `.ass`、
跃迁触发 + 有界重试），但**用户还摸不到**：开关只在 Go 里，界面没有入口。
本笔补 UI。这一环缺了，整条链就是"引擎在、没人发动"
（本任务第 6 轮下一季、第 23 轮观察器、第 39 轮 SaveLocalWatch 全是同一个形状）。

v1 侧规格（逐字抄自 `zhenguojian/app/zhenguojian/downloads.py:1095-1099`）：
  QCheckBox("下载时保存弹幕")
  setToolTip("勾选后，每集解密完成会额外保存同名 .ass 字幕和 .danmaku.json；默认关闭")
  setChecked(tools.danmaku_download_enabled())            # 默认 False
  toggled.connect(self._on_danmaku_download_toggled)      # 即点即存，不等"保存"按钮

断言：
  ① 标签与 tooltip 逐字等于 v1
  ② 默认**关**（默认开就会在用户不知道的情况下多打核心）
  ③ 勾上后立刻生效并给确认提示（不需要点那颗「保存」）
  ④ 刷新后仍是勾上的（真值经设置面回来，不是内存态）
  ⑤ 取消勾选同样即点即存、刷新保持
  ⑥ 写失败必须**弹回原值 + 明说已回滚**（失败了却把勾留着，等于界面骗人）
  ⑦ en 态标签与 tooltip 都是英文（新键必须成对）
  ⑧ 全程无 JS 错误

选择器说明：设置面板那颗按钮**没有 testid**，只有文案键 `downloads.concurrencyBtn`
（zh「并发设置」/ en「Concurrency」）。上一个版本我照键名编了个
`[data-testid=concurrencyBtn]`，那是"查一个本仓不存在的选择器"的老毛病（第 40 轮同型）。
"""
import os
import re
import sys

import _devserver
from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = "5209"
BASE = f"http://127.0.0.1:{PORT}"
EXE = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell")
OUT = HERE

CB = "[data-testid=dl-save-danmaku]"
V1_LABEL = "下载时保存弹幕"
V1_HINT = "勾选后，每集解密完成会额外保存同名 .ass 字幕和 .danmaku.json；默认关闭"
BTN_ZH = "并发设置"
BTN_EN = "Concurrency"

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail[:170]}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


def open_panel(page, btn_label):
    """进下载页 → 展开设置面板 → 等到勾选框出现。"""
    page.goto(BASE + "/#/downloads")
    btn = page.get_by_role("button", name=btn_label, exact=True)
    btn.first.wait_for(timeout=25000)
    page.wait_for_timeout(1200)
    if not page.locator(CB).count():
        btn.first.click()
    page.wait_for_selector(CB, timeout=20000)
    page.wait_for_timeout(500)


def cb(page):
    return page.locator(CB).is_checked()


def flip_cb(page, expect_change=True):
    """点 label 换勾选状态。

    ⚠ 不用 `locator.check()/uncheck()`：勾选框是 `v-model` + `@change` 里**异步**写设置，
    Playwright 的 check() 在同一次调用内就要求看到状态翻转，
    实测报 "Clicking the checkbox did not change its state" —— 那不是产品坏了
    （用 dispatch click 再看，状态确实翻了）。
    """
    page.locator(CB).locator("xpath=ancestor::label[1]").click()
    page.wait_for_timeout(900)
    return cb(page)


def set_cb(page, want):
    got = cb(page)
    if got != want:
        flip_cb(page)
    return cb(page)



def toast(page):
    el = page.locator(".toast")
    return el.first.inner_text().strip() if el.count() else ""


def label_and_tip(page):
    lab = page.locator(CB).locator("xpath=ancestor::label[1]").inner_text().strip()
    tip = page.locator(CB).get_attribute("title") or ""
    return lab, tip


def main():
    proc = _devserver.start_dev(FRONTEND, PORT, True, HERE, name="danmaku-toggle")
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE, headless=True)
            ctx = browser.new_context(viewport={"width": 1280, "height": 860})
            page = ctx.new_page()
            js_errors = []
            page.on("pageerror", lambda e: js_errors.append(str(e)[:150]))

            # ── ①② ──
            open_panel(page, BTN_ZH)
            lab, tip = label_and_tip(page)
            check("① 标签与 tooltip 逐字等于 v1（downloads.py:1095-1099）",
                  lab == V1_LABEL and tip == V1_HINT, f"label={lab!r} tip={tip!r}")
            check("② 默认是**关**的（v1 tools.danmaku_download_enabled 默认 False）",
                  cb(page) is False, f"checked={cb(page)}")

            # ── ③ 勾上即生效 ──
            set_cb(page, True)
            check("③a 勾上后就是勾着（不依赖那颗「保存」按钮）", cb(page) is True,
                  f"checked={cb(page)}")
            check("③b 给了确认提示", "已开启" in toast(page), toast(page))

            # ── ④ 刷新后保持 ──
            page.reload()
            open_panel(page, BTN_ZH)
            check("④ 刷新后仍是勾上的（真值经设置面回来，不是内存态）",
                  cb(page) is True, f"checked={cb(page)}")

            # ── ⑤ 取消也即点即存 ──
            set_cb(page, False)
            check("⑤a 取消后提示是「已关闭」", "已关闭" in toast(page), toast(page))
            page.reload()
            open_panel(page, BTN_ZH)
            check("⑤b 取消也保持住（刷新后仍未勾）", cb(page) is False, f"checked={cb(page)}")

            # ── ⑥ 写失败要回滚并说出来 ──
            # 失败注入缝 `__zgjFailDanmakuToggle`：没有它就只能靠真断桥，
            # 而"失败 → 弹回原值 + 一句话"恰恰是最容易写错、又最难自然触发的一支。
            page.evaluate("() => { window.__zgjFailDanmakuToggle = true }")
            flip_cb(page, expect_change=False)  # 这一支就是要它翻回去
            check("⑥a 写失败后勾选框弹回原值（没骗人说勾上了）",
                  cb(page) is False, f"checked={cb(page)}")
            check("⑥b 明说保存失败已回滚", "已回滚" in toast(page), toast(page))
            page.evaluate("() => { delete window.__zgjFailDanmakuToggle }")

            # ── ⑦ en 态 ──
            ctx2 = browser.new_context(viewport={"width": 1280, "height": 860})
            page2 = ctx2.new_page()
            # 顺序很重要：**先起文档 → 改 localStorage → 整页 reload**。
            # 上一版我改成"设完 locale 直接 goto 另一个 hash"，文档没换，
            # i18n 还是模块加载时读的 zh，于是等 "Concurrency" 等到超时——
            # 这条坑我在 docstring 里已经写过一次，还是踩了第二次，
            # 所以把它写成代码注释而不是只写文档。
            # 顺序：起文档 → 写 locale → **整页 reload** → 再展开面板。
            # 两个坑都在这儿：
            # ① 只改 hash 不重载文档，i18n 还是启动时读的 zh（本任务第四次）；
            # ② 勾选框在"并发设置"面板里，面板是折叠的，
            #    所以不能先等 CB 再点按钮——顺序反了就是 25 秒空等（上一版就这样）。
            page2.goto(BASE + "/#/downloads")
            page2.wait_for_timeout(2000)
            page2.evaluate("() => localStorage.setItem('zgj.locale', 'en')")
            page2.reload()
            page2.wait_for_timeout(2000)
            open_panel(page2, BTN_EN)
            lab_en, tip_en = label_and_tip(page2)
            check("⑦a en 态标签是英文",
                  lab_en == "Save danmaku on download" and not re.search(r"[一-鿿]", lab_en),
                  lab_en)
            check("⑦b en 态 tooltip 也是英文且写明默认关（新键必须成对）",
                  "off by default" in tip_en and not re.search(r"[一-鿿]", tip_en), tip_en)
            ctx2.close()

            check("⑧ 全程无 JS 错误", not js_errors, "; ".join(js_errors[:2]))
            page.screenshot(path=os.path.join(OUT, "danmaku-toggle.png"))
            browser.close()
    finally:
        _devserver.stop_dev(proc, PORT, name="danmaku-toggle")

    print(f"\n===== danmaku_toggle_verify: {'ALL PASS' if not failures else 'FAILURES: ' + ', '.join(failures)} =====")
    print(f"screenshot -> {os.path.join(OUT, 'danmaku-toggle.png')}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
