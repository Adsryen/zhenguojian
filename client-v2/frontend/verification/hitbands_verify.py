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
            ctx.grant_permissions(["clipboard-read", "clipboard-write"], origin=BASE)
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

            # C6 徽标自证「上报到底送到了没有」。
            # 为什么这条不能省：真机连续两个 tag 只显示「最近上报 0×0」，
            # 而这个数**分不开**「没调用 setBounds」与「调用时量到 0」两种相反成因，
            # 我只能靠猜、又烧掉一个 tag。现在徽标同时给出：
            #   上报次数（Go 侧 ApplyBounds 计数，经 setBounds 回执回到前端）
            #   前端量得（我这边量到的尺寸 + 第几次量 + DPR）
            #   上报异常（bridge 旧写法 catch{} 吞掉的那一类）
            #   版本号（同一张截图必须能自证是哪个构建）
            # 断言盯的是**具体数值**，不是"字段存在"：C5 那轮的教训。
            import re as _re
            m_calls = _re.search(r"上报(\d+)次", badge)
            m_meas = _re.search(r"前端量得(\d+)x(\d+)@(\d*\.?\d+)#(\d+)", badge)
            m_ver = _re.search(r"[·\s]v(\S+?)(?:\s|·|$)", badge)
            check("C6a 上报次数经回执进入徽标且 >=1（不是 attach 时的旧快照）",
                  bool(m_calls) and int(m_calls.group(1)) >= 1,
                  f"calls={m_calls.group(1) if m_calls else 'NO-MATCH'} badge={badge[:200]!r}")
            check("C6b 前端量得非零尺寸且带测量序号（0×0 会被当成没上报）",
                  bool(m_meas) and int(m_meas.group(1)) > 1 and int(m_meas.group(2)) > 1
                  and int(m_meas.group(4)) >= 1,
                  f"meas={m_meas.groups() if m_meas else 'NO-MATCH'}")
            check("C6c 上报异常为 none（catch{} 吞异常时这里会亮出原因）",
                  "上报异常=none" in badge,
                  f"badge={badge[-160:]!r}")
            check("C6d 徽标带构建版本（截图自证是哪个包）",
                  bool(m_ver) and m_ver.group(1) not in ('?', ''),
                  f"ver={m_ver.group(1) if m_ver else 'NO-MATCH'}")
            # C6e 两侧数字必须对得上：徽标的「最近上报」应等于「前端量得」。
            # 为什么单独钉：真机上「上报 0×0」正是靠这两个数不一致才露馅的，
            # 而 mock 若不镜像 Go 的 Debug 语义，这条会在假现场上通过。
            m_in = _re.search(r"最近上报 (\d+)×(\d+)", badge)
            check("C6e 徽标「最近上报」等于「前端量得」（两侧同源，不是各说各话）",
                  bool(m_in) and bool(m_meas)
                  and int(m_in.group(1)) == int(m_meas.group(1))
                  and int(m_in.group(2)) == int(m_meas.group(2)),
                  f"lastIn={m_in.groups() if m_in else 'NO-MATCH'} measured={m_meas.groups() if m_meas else 'NO-MATCH'}")
            # C6f 「该显示」四元组渲染成 0/1，不是残留占位符
            m_show = _re.search(r"该显示=(\d)\(r(\d) l(\d) e(\d) f(\d)\)", badge)
            check("C6f 徽标带 show 四元组（可见=0 时能看出卡在哪个条件）",
                  bool(m_show) and "{want}" not in badge,
                  f"show={m_show.groups() if m_show else 'NO-MATCH'} badge={badge[-120:]!r}")

            # ── C7：「复制内嵌诊断」按钮把徽标写成纯文本进剪贴板 ──
            # 连续四个 tag 我都靠用户截图读徽标，而整行徽标在界面上会被左边缘裁掉，
            # 每次都要来回追问。这个按钮就是为此加的，断言钉住三件事：
            #   点得中、剪贴板里是完整文本（version= 行 + host= 原始 JSON）、
            #   badge 行与界面上同一份文本（同源，不漂移）。
            page.click('[data-testid="embed-copy-diag"]')
            page.wait_for_timeout(500)
            clip = page.evaluate("navigator.clipboard.readText()")
            c7_ver = _re.search(r"^version=\S+", clip, _re.M)
            c7_host = _re.search(r"^host=\{", clip, _re.M)
            c7_badge = _re.search(r"^badge=.*", clip, _re.M)
            check("C7a 复制成功且含版本行（没进按钮时 readText 会是空串）",
                  bool(c7_ver), f"head={clip[:80]!r}")
            check("C7b 含 host 原始 JSON（读数必须是数字本身，不是渲染过的句子）",
                  bool(c7_host), f"len={len(clip)}")
            # 界面上的 badge = 徽标正文 + 错误行 + 两个按钮的文字（inner_text 全拼起来），
            # 剪贴板里只有徽标正文 —— 所以判「剪贴板徽标行是界面徽标的前缀且足够长」，
            # 而不是要求整段相等（我第一版就按整段相等写的，C7c 当场红，inClip=True
            # 却 FAIL，因为差异正是那两个按钮的文字）。
            c7_line = c7_badge.group(0)[len("badge="):].strip() if c7_badge else ""
            check("C7c 徽标行与界面上同一份文本（同源，不漂移）",
                  bool(c7_line) and len(c7_line) > 40
                  and badge.strip().startswith(c7_line),
                  f"line={c7_line[-60:]!r}")

            # ── 场景 D：自愈合逃生（第九十六轮）──
            # alpha.40 真机「前端发了 3 次 setBounds、Go 侧 applyCount=0、用户黑屏」
            # 之后，产品不该再等下一个 release。这一版：running 后若 N ms 内子窗
            # 从未被摆过（回执 applyCount=0 或 childWidth==1），自动 forceWindow
            # 切 mpv 独立窗让用户先看到画面，并留 reason=auto-fallback-unplaced。
            # mock 缝 __zgjMockNeverPlaced 把回执钉在真机形状（applyCount=0/childWidth=1），
            # __zgjMockForceWindow 让 mock forceWindow 真切一次并记 __zgjForceWindow 计数，
            # __zgjAutoFbMs 把 8s 阈值压到 600ms 让 E2E 不用干等。
            # 新 context 隔离：场景 A-C 的 __zgjBounds/状态不能漏进来。
            #
            # 判据按产品**实际**行为写（读模板确认，不猜）：
            #   - 自动逃生与手动 forceWindowMode 同路径：置 embedActive=false、
            #     embedReason='auto-fallback-unplaced'、重跑 bootFromQuery。
            #   - debug 徽标 gated on embedHost（不清）→ 逃生后**仍在**（与手动路径一致，
            #     显示旧 host 读数供诊断），所以不能断言它消失。
            #   - 独立窗回退提示 embed-fallback gated on `embedReason && !loading`，
            #     要等 re-boot 的 loading 落回才显示 → 必须 wait_for_selector 而非瞬时读。
            ctx_d = browser.new_context(viewport={"width": 1280, "height": 800})
            ctx_d.add_init_script(
                "window.__zgjMockEmbed = true; window.__zgjMockNeverPlaced = true; "
                "window.__zgjMockForceWindow = true; window.__zgjAutoFbMs = 600;")
            page_d = ctx_d.new_page()
            page_d.goto(PLAYER_URL)
            page_d.wait_for_selector(".pl-stage", timeout=15000)
            # 等逃生触发（forceWindow 计数）。触发后 embedReason 已置、re-boot 中，
            # 再等回退提示真的渲染出来（re-boot 的 loading 落回才显示）。
            page_d.wait_for_function(
                "() => (window.__zgjForceWindow ?? 0) >= 1", timeout=9000)
            d_fw = page_d.evaluate("window.__zgjForceWindow")
            d_fb_text = ""
            try:
                page_d.wait_for_selector('[data-testid="embed-fallback"]',
                                         state="visible", timeout=9000)
                d_fb_text = page_d.locator('[data-testid="embed-fallback"]').inner_text()
            except Exception:
                pass
            d_debug = page_d.locator('[data-testid="embed-debug"]').count()
            check("D1 running 后子窗从未被摆过 → 自动 forceWindow 发生",
                  d_fw is not None and d_fw >= 1,
                  f"forceWindow={d_fw}")
            check("D2 逃生后独立窗回退提示渲染出来（用户知道画面去哪了）",
                  d_fb_text.strip() != "",
                  f"fallback={d_fb_text[:80]!r}")
            check("D3 回退提示走 i18n 文案（裸 reason 码不得直接给用户读）",
                  "已自动改用独立窗口播放" in d_fb_text and "auto-fallback-unplaced" not in d_fb_text,
                  f"fallback={d_fb_text[:80]!r}")
            check("D4 debug 徽标保留（与手动逃生路径一致：embedHost 不清，供诊断读旧值）",
                  d_debug == 1, f"debugVisible={d_debug}")
            # 对照组：正常缝（子窗摆好了 applyCount>0）不该触发自动逃生。
            ctx_d2 = browser.new_context(viewport={"width": 1280, "height": 800})
            ctx_d2.add_init_script(
                "window.__zgjMockEmbed = true; window.__zgjAutoFbMs = 700;")
            page_d2 = ctx_d2.new_page()
            page_d2.goto(PLAYER_URL)
            page_d2.wait_for_selector(".pl-stage", timeout=15000)
            page_d2.wait_for_timeout(2200)  # 等 running + 逃生窗(700ms)过去
            d2_fw = page_d2.evaluate("window.__zgjForceWindow")
            d2_debug = page_d2.locator('[data-testid="embed-debug"]').count()
            d2_fb = page_d2.locator('[data-testid="embed-fallback"]').count()
            check("D5 子窗已摆好（正常缝）时不触发自动逃生（对照：不误伤好现场）",
                  (d2_fw is None or d2_fw == 0) and d2_debug == 1 and d2_fb == 0,
                  f"forceWindow={d2_fw} debugVisible={d2_debug} fallbackVisible={d2_fb}")
            ctx_d.close()
            ctx_d2.close()

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
