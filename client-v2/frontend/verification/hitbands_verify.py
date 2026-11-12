#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""V2-4b 覆盖层点击穿透带验证（headless chromium + VITE_MOCK dev server）。

自管理 dev:mock：独占端口 5197，启停走 `_devserver`（端口预检 + 严格收口）。
     旧写法要求人工「先起 5173」，且 5173 被多个脚本共用——没人起就报错、
     有人起就可能测到别人的服务器。
链路：PlayerView mount → embedApi.attach（__zgjMockEmbed=true 谎报内嵌可用）
→ reportBounds → reportHitBands → embedApi.playerHitBands → window.__zgjHitBands。

场景 A（现行布局实况）：标题条/控制条与 .pl-stage 为 flex 兄弟、零重叠
  → 断言链路触发且 top/bottom==0（重叠式测量在兄弟布局下的正确值）。
场景 B（浮层布局，注入 CSS 让两条 absolute 叠进 stage 矩形）：
  → 断言 top/bottom>0 且 ≈ 覆盖层与 stage 实际重叠高度 × devicePixelRatio，
     并截图 hitbands.png（虚线框出视频矩形、半透明标注上下穿透带）。

断言以 PASS/FAIL 行打印，任何 FAIL 退出码非 0。
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)

from playwright.sync_api import sync_playwright

import _devserver

PORT = 5197
BASE = f"http://127.0.0.1:{PORT}"
PLAYER_URL = f"{BASE}/#/player?d=mock_drama_1&ep=1"
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


RECT_JS = """() => {
  const g = (s) => { const e = document.querySelector(s); if (!e) return null;
    const r = e.getBoundingClientRect(); return {top:r.top,bottom:r.bottom,left:r.left,right:r.right}; };
  return { dpr: window.devicePixelRatio || 1, stage: g('.pl-stage'), top: g('.pl-top'), bar: g('.pl-bar') };
}"""

OVERLAP_CSS = """
.player { position: relative; }
.pl-top { position: absolute; top: 0; left: 0; right: 0; z-index: 30; }
.pl-bar { position: absolute; bottom: 0; left: 0; right: 0; z-index: 30; }
"""


def hit_bands(page):
    return page.evaluate("()=> window.__zgjHitBands ?? null")


def expected_overlap(rects):
    """按页面实测矩形算期望穿透带（物理像素，与 PlayerView 同一公式）。"""
    dpr = rects["dpr"]
    s, t, b = rects["stage"], rects["top"], rects["bar"]
    top = round((t["bottom"] - s["top"]) * dpr) if t["bottom"] > s["top"] else 0
    bottom = round((s["bottom"] - b["top"]) * dpr) if b["top"] < s["bottom"] else 0
    return top, bottom


def main():
    proc = _devserver.start_dev(FRONTEND, PORT, True, HERE, name='hitbands')
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE)
            ctx = browser.new_context(viewport={"width": 1280, "height": 800})
            ctx.add_init_script("window.__zgjMockEmbed = true;")
            page = ctx.new_page()
            page.goto(PLAYER_URL)
            page.wait_for_selector(".pl-stage", timeout=15000)
            page.wait_for_timeout(2500)  # 等 attach→bootFromQuery→reportBounds 全链

            # ── 场景 A：现行兄弟布局 ──
            hb = hit_bands(page)
            check("A1 上报链路触发（__zgjHitBands 存在且 count>=1）", bool(hb) and hb["count"] >= 1,
                  f"count={hb['count'] if hb else 'null'}")
            rects = page.evaluate(RECT_JS)
            expA = expected_overlap(rects)
            last = hb["last"] if hb else None
            check("A2 兄弟布局实测重叠为 0", last is not None and last["top"] == expA[0] and last["bottom"] == expA[1],
                  f"reported={last} expected={expA}")
            check("A3 覆盖层确在 stage 矩形之外（top<=stage.top 等）",
                  rects["top"]["bottom"] <= rects["stage"]["top"] + 0.5
                  and rects["bar"]["top"] >= rects["stage"]["bottom"] - 0.5,
                  f"top.bottom={rects['top']['bottom']} stage=({rects['stage']['top']},{rects['stage']['bottom']}) bar.top={rects['bar']['top']}")

            # ── 场景 B：注入浮层 CSS（覆盖层叠进视频矩形 = 真机问题的前提态） ──
            before = hb["count"]
            page.evaluate("(css) => { const s = document.createElement('style'); s.id='zgj-hb-test'; s.textContent = css; document.head.appendChild(s); }", OVERLAP_CSS)
            page.wait_for_timeout(800)  # ResizeObserver（stage+浮层）重报
            hb2 = hit_bands(page)
            rects2 = page.evaluate(RECT_JS)
            expB = expected_overlap(rects2)
            last2 = hb2["last"] if hb2 else None
            check("B1 布局变化触发重报（count 增长）", bool(hb2) and hb2["count"] > before,
                  f"{before} -> {hb2['count'] if hb2 else 'null'}")
            check("B2 穿透带>0（浮层确实侵入视频矩形）",
                  last2 is not None and last2["top"] > 0 and last2["bottom"] > 0,
                  f"reported={last2}")
            check("B3 穿透带≈实测重叠×DPR（±1px 取整容差）",
                  last2 is not None and abs(last2["top"] - expB[0]) <= 1 and abs(last2["bottom"] - expB[1]) <= 1,
                  f"reported={last2} expected={expB} dpr={rects2['dpr']}")

            # 截图：标注视频矩形（虚线）与上下穿透带（半透明红/蓝）。
            page.evaluate("""(r) => {
              const mk = (css, txt) => { const d = document.createElement('div');
                d.style.cssText = css; d.textContent = txt; document.body.appendChild(d); return d; };
              const S = r.stage, R = (v) => Math.round(v);
              mk(`position:fixed;left:${S.left}px;top:${S.top}px;width:${S.right - S.left}px;height:${S.bottom - S.top}px;
                  border:2px dashed #ffd400;pointer-events:none;z-index:90;`, '');
              mk(`position:fixed;left:${S.left}px;top:${S.top}px;width:${S.right - S.left}px;height:${R(r.topBand)}px;
                  background:rgba(255,64,64,.28);pointer-events:none;z-index:91;`, '穿透带 TOP');
              mk(`position:fixed;left:${S.left}px;bottom:${Math.max(0, 800 - S.bottom)}px;width:${S.right - S.left}px;height:${R(r.bottomBand)}px;
                  background:rgba(64,120,255,.28);pointer-events:none;z-index:91;`, '穿透带 BOTTOM');
            }""", {"stage": rects2["stage"], "topBand": last2["top"], "bottomBand": last2["bottom"]})
            page.wait_for_timeout(300)
            page.screenshot(path=os.path.join(OUT, "hitbands.png"))
            print(f"screenshot -> {os.path.join(OUT, 'hitbands.png')}")

            browser.close()

        if failures:
            print(f"FAILED: {len(failures)} check(s)")
            sys.exit(1)
        print("ALL CHECKS PASSED")


    finally:
        _devserver.stop_dev(proc, PORT, name='hitbands')
if __name__ == "__main__":
    main()
