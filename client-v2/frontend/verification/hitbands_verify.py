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


def bounds_rec(page):
    """embedApi.setBounds 的上报序列（mock 面记录，见 services/mock/embed.ts）。"""
    return page.evaluate("()=> window.__zgjBounds ?? null")


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

            # ── 场景 C：几何与可见性分开上报 ──
            # 钉的是第六十一轮真机数据定出的根因：旧 reportBounds 在 running 之前
            # 上报 0×0（"零尺寸=隐藏"），而旧 ApplyBounds 的隐藏分支只 SW_HIDE
            # 不 MoveWindow —— 子窗从创建起一直是 1×1，mpv 早在 spawn 前就被
            # SetWid 指到它，GL drawable 也就一直是 1×1（徽标实测 1×1 @328,147）。
            bd = bounds_rec(page)
            blast = bd["last"] if bd else None
            stage2 = rects2["stage"]
            cfirst = bd["first"] if bd else None
            check("C1 起手（PlayerOpen 之前那次）就摆真实几何且 show=false",
                  cfirst is not None and cfirst["w"] > 1 and cfirst["h"] > 1 and cfirst["show"] is False,
                  f"first={cfirst}")
            check("C2 隐藏态也照实摆位（存在 show=false 且 w>1 的上报）",
                  bd is not None and bd["hiddenWithGeometry"] >= 1,
                  f"rec={bd}")
            check("C3 上报尺寸≈stage 实测×DPR（±2px 取整容差）",
                  blast is not None
                  and abs(blast["w"] - round((stage2["right"] - stage2["left"]) * rects2["dpr"])) <= 2
                  and abs(blast["h"] - round((stage2["bottom"] - stage2["top"]) * rects2["dpr"])) <= 2,
                  f"bounds={blast} stage={stage2} dpr={rects2['dpr']}")
            check("C4 running 翻转后 show 变真（可见性由 show 表达，不再靠抹掉尺寸）",
                  blast is not None and blast["show"] is True,
                  f"bounds={blast}")

            # C5 徽标端到端：Go 侧新加的"该像素最上层是谁"必须真的显示出来。
            # 为什么这条不能省：这类诊断字段的历史失败模式是
            # "Go 加了字段、TS 类型加了、前端没接"（GO_BINDINGS_EMBED 就零引用过），
            # 那种错在单测和 typecheck 里都不响，只有浏览器里看得见。
            # mock attach 带回合成 host（Chrome_RenderWidgetHostHWND / topIsChild=false），
            # 所以断言的是**具体值**，不是"元素存在"。
            badge = page.locator('[data-testid="embed-debug"]').inner_text() if page.locator(
                '[data-testid="embed-debug"]').count() else ""
            check("C5 徽标渲染出「最上层」读数（Go→TS→界面接线通）",
                  "Chrome_RenderWidgetHostHWND=other" in badge and "{top}" not in badge,
                  f"badge={badge[:220]!r}")

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
