#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""merge_multi_verify.py —— 「按季合并选中」的界面那一步（R22b 收尾）

Go 侧第四十三轮已完成并由 CI 验过（`internal/downloadsvc/multi.go`：季序稳定升序、
每部不足 2 集整部丢掉、`<去季号名> S%02dE%02d.mkv` 命名、同名加 ` (2)`、
运行中的那一季**跳过而不是报错**）。本脚本验**界面**：按钮出现在哪、
按下去排进去的是不是勾选那几部、勾选会不会残留。

落点理由（v1 靠左侧剧列表复选框，v2 没那个列表）：
「合并入口」这一条本来就按剧分组列出 chip（每颗一部可合并的剧），
所以复选挂在 chip 上、勾选≥2 才出现批次按钮。是最接近的现有结构，不是新造页面。

⚠ 有一条**不在这台机器上验证**，写明白而不是假装测过：
`multi_targets`（不足两部可合并）那条门槛在 mock 里走不到——
内置示例态恰好有两部可合并的剧，界面上"勾≥2 才出现按钮"与"可合并<2 就报错"
这两个条件互相排斥，没有注入点能同时满足。
所以这里只在 API 层验"码与 Go 一致"（同码名才有意义），
门槛本身由 Go 单测覆盖（`TestMergeStartMultiNeedsAtLeastTwoSeasons`）。
另外 mock **不复刻**季序与命名规则（那是第二个真相），
所以本脚本不断言输出文件名，只断言"排进去的是勾选那几部"。

端口：自管理 mock vite @5210（`_devserver`）。
"""
import json
import os
import re
import sys

import _devserver
from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = "5210"
BASE = f"http://127.0.0.1:{PORT}"
EXE = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell")
OUT = HERE

CHECKS = "[data-testid=merge-group-check]"
MULTI = "[data-testid=merge-multi]"
V1_TIP = "勾选多部剧（如同一部的多季），按季数顺序逐季合并，每季输出一个「剧名 S01E01」样式的成品"

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail[:180]}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


def open_downloads(page, locale=None):
    page.goto(BASE + "/#/downloads")
    page.wait_for_selector(".merge-bar", timeout=25000)
    if locale:
        page.evaluate("(l) => localStorage.setItem('zgj.locale', l)", locale)
        page.reload()  # 只改 hash 不重载文档，i18n 是模块加载时读的
        page.wait_for_selector(".merge-bar", timeout=25000)
    page.wait_for_timeout(1600)
    # 内置示例态里有两处合并入口相关元素，只留「合并入口」这一条
    page.wait_for_selector(CHECKS, timeout=20000)


def toast(page):
    el = page.locator(".toast")
    return el.first.inner_text().strip() if el.count() else ""


def merges_of(page, with_id=False):
    """mock 的下载状态在内存里（不是 localStorage），所以从 store 读。"""
    return page.evaluate("""(needId) => {
      const st = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia.state.value;
      return (st.downloads.merges ?? []).map((m) => ({
        id: needId ? m.id : undefined, dramaId: m.dramaId, state: m.state, count: m.count }));
    }""", with_id)


def main():
    proc = _devserver.start_dev(FRONTEND, PORT, True, HERE, name="merge-multi")
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE, headless=True)
            ctx = browser.new_context(viewport={"width": 1280, "height": 900})
            page = ctx.new_page()
            js_errors = []
            page.on("pageerror", lambda e: js_errors.append(str(e)[:150]))

            open_downloads(page)

            groups_before = page.locator(CHECKS).count()
            check("① 两部可合并的剧各有一颗复选，此时批次按钮不出现",
                  groups_before == 2 and page.locator(MULTI).count() == 0,
                  f"checks={groups_before} multi={page.locator(MULTI).count()}")

            # ── ② 勾 1 部 ──
            page.locator(CHECKS).first.check()
            page.wait_for_timeout(700)
            check("② 只勾 1 部时批次按钮仍不出现（v1 的门槛：不成批）",
                  page.locator(MULTI).count() == 0, f"multi={page.locator(MULTI).count()}")

            # ── ③ 勾 2 部 ──
            page.locator(CHECKS).nth(1).check()
            page.wait_for_timeout(700)
            label = page.locator(MULTI).inner_text().strip() if page.locator(MULTI).count() else ""
            tip = page.locator(MULTI).get_attribute("title") if page.locator(MULTI).count() else ""
            check("③a 勾 2 部后出现「按季合并选中（2 部）」（v1 downloads.py:1933 的文案）",
                  label == "按季合并选中（2 部）", f"label={label!r}")
            check("③b tooltip 是 v1 那句（只去掉「左侧」两个字）", tip == V1_TIP, f"tip={tip!r}")

            # ── ④ 按下去：排进去的必须是勾选那两部 ──
            # mergeGroups 是 **getter**，不在 $pinia.state.value 里（我上一版从 state 读，
            # 拿到空数组，于是断言变成"期望 0 部"这种荒谬比较）。getter 要从 store 实例上取。
            expected = page.evaluate("""() => {
              const app = document.querySelector('#app');
              const pinia = app.__vue_app__.config.globalProperties.$pinia;
              return (pinia._s.get('downloads').mergeGroups ?? []).map((g) => g.dramaId);
            }""")
            page.locator(MULTI).click()
            page.wait_for_timeout(2200)
            all_merges = merges_of(page, True)
            # 判"排进去了"要看**在排/在跑**的那些，而不是"列表新增了几条"：
            # mock 的历史里同一 id 可以既有 done 又有新的 running（它 unshift 不清旧行），
            # 上一版我用 id 做集合差，正好把这条重复 id 当成"不是新增"漏掉，
            # 得出 added=1 的假失败。按状态筛才是这件事的语义。
            active = [m for m in all_merges if m["state"] in ("running", "queued")]
            active_ids = sorted({m["dramaId"] for m in active})
            check("④ 两部剧各有一条在排的合并（id 集合与可合并分组一致）",
                  len(active) == 2 and active_ids == sorted(set(expected)) and len(set(expected)) == 2,
                  f"active={json.dumps(active, ensure_ascii=False)} expected={expected}")

            # ── ⑤ 提示与勾选清空 ──
            check("⑤a 提示「已排入 2 季的合并」", "已排入 2 季的合并" in toast(page), toast(page))
            still_checked = page.evaluate(
                """([sel]) => [...document.querySelectorAll(sel)].some(x => x.checked)""", [CHECKS])
            check("⑤b 排完清空勾选（不留「以为还要再点一次」的状态）",
                  still_checked is False, f"still_checked={still_checked}")
            check("⑤c 批次按钮随之消失", page.locator(MULTI).count() == 0,
                  f"multi={page.locator(MULTI).count()}")

            # ── ⑥ 刷新后不残留 ──
            page.reload()
            open_downloads(page)
            check("⑥ 刷新后批次按钮不出现（勾选不残留）",
                  page.locator(MULTI).count() == 0, f"multi={page.locator(MULTI).count()}")

            # ── ⑦ 码名与 Go 一致（门槛本身在 Go 单测覆盖，见 docstring） ──
            codes = page.evaluate("""async () => {
              const out = {};
              const st = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia;
              const store = st.state.value ? st._s.get('downloads') : null;
              for (const [name, arg] of [['none', []], ['targets', [
                { id: 'hongguo:9001', title: '单集剧', source: 'hongguo' }
              ]]]) {
                try {
                  await store.mergeStartMulti(arg);
                  out[name] = 'no-error';
                } catch (e) { out[name] = String(e?.code ?? ''); }
              }
              return out;
            }""")
            check("⑦ mock 报的 code 与 Go 的尾巴同名（multi_none / multi_targets）",
                  codes.get("none") == "multi_none" and codes.get("targets") == "multi_targets",
                  f"codes={codes}")

            # ── ⑧ en 态 ──
            ctx2 = browser.new_context(viewport={"width": 1280, "height": 900})
            page2 = ctx2.new_page()
            page2.goto(BASE + "/#/downloads")
            page2.wait_for_timeout(2500)
            page2.evaluate("() => localStorage.setItem('zgj.locale', 'en')")
            page2.reload()
            page2.wait_for_selector(".merge-bar", timeout=25000)
            page2.wait_for_timeout(1800)
            page2.locator(CHECKS).first.check()
            page2.locator(CHECKS).nth(1).check()
            page2.wait_for_timeout(700)
            if page2.locator(MULTI).count():
                lab_en = page2.locator(MULTI).inner_text().strip()
                tip_en = page2.locator(MULTI).get_attribute("title") or ""
                check("⑧a en 态标签是英文", lab_en == "Merge selected seasons (2 titles)", lab_en)
                check("⑧b en 态 tooltip 是英文（新键必须成对）",
                      "season" in tip_en and not re.search(r"[一-鿿]", tip_en), tip_en)
            else:
                check("⑧a en 态标签是英文", False, "批次按钮没出现")
                check("⑧b en 态 tooltip 是英文（新键必须成对）", False, "同上")
            ctx2.close()

            check("⑨ 全程无 JS 错误", not js_errors, "; ".join(js_errors[:2]))
            page.screenshot(path=os.path.join(OUT, "merge-multi.png"))
            print("NOTE ⑦ 只验码名一致；「不足两部」的门槛行为由 Go 单测覆盖"
                  "（mock 里界面按钮与门槛条件互斥，没注入点能同时满足——不为此临时改产品代码）")
            browser.close()
    finally:
        _devserver.stop_dev(proc, PORT, name="merge-multi")

    print(f"\n===== merge_multi_verify: {'ALL PASS' if not failures else 'FAILURES: ' + ', '.join(failures)} =====")
    print(f"screenshot -> {os.path.join(OUT, 'merge-multi.png')}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
