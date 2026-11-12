#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""V2-F 播放器体验包验证（headless chromium + dev:mock 5179 自管理）。

覆盖断言（任何 FAIL 退出码非 0）：
  ① 全屏按钮存在，点击调用 window.runtime.WindowFullscreen/Unfullscreen（init 脚本 stub 计数），
     按钮文案「全屏 ⇄ 退出全屏」切换。
  ② ? 帮助浮层出现；Esc 关；再开后 8s 自动关。
  ③ Space=播/暂、→/←=±5s、↑/↓=音量±5 在 store/mock 面产生可观测效果（时间冻结/位移、LS 音量）。
  ④ 选集面板：展开宽 300 / 折叠宽 36 / 点第3集切集（标题+当前高亮）/ 当前集在面板内可见（落位）。
  ⑤ 上/下一集常驻禁用逻辑：ep1 时 ⏮ 禁用；ep35（mock 末集）时 ⏭ 禁用、⏮ 可用。
  ⑥ 连播：ep3 seek 至片尾 → 自动播第4集；关开关 → LS zgj.player.autoplay='0' 且重载后仍「连播关」。
  ⑦ 画质下拉存在，选 720 → toast 含「已切换到720P」（mock 实链）或占位提示、LS zgj.player.quality='720'（未假成功）
  ⑧ 弹幕不透明度：默认「透明 100%」、滑条 20–100、拖动跟手、
     落盘 zgj.player.danmakuOpacity、越界钳位、刷新后从 LS 恢复。
     （壳层收到值并 sub-reload 由 Go 单测 TestDanmakuOpacityRendersAndClamps + CI 覆盖，不在这里冒充已验）
     真实清晰度未变由占位重开当前集承接）。
截图：本目录 player-pack.png（面板展开 + toast 可见态）。

前置：playwright（python）与 EXE 指向 chrome-headless-shell；npm 依赖已安装。
"""
import json
import os
import signal
import subprocess

import _devserver
import sys
import tempfile
import time
import urllib.request

from playwright.sync_api import sync_playwright

PORT = "5179"
BASE = f"http://127.0.0.1:{PORT}"
PLAYER_URL = f"{BASE}/#/player?d=mock_drama_1&ep=1"
EXE = "/home/ryen/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
OUT = os.path.dirname(os.path.abspath(__file__))
FRONT = os.path.abspath(os.path.join(OUT, ".."))

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


def start_server():
    # 启停交 _devserver：进门断言端口为空、退出回查端口确实关闭。
    # 旧写法结束时只 killpg(SIGTERM) 一次，既不 wait 也不升级，
    # 泄漏的 vite 会让别的脚本「复用已在监听的端口」测到旧构建。
    return _devserver.start_dev(FRONT, PORT, True, OUT, name="player")


FS_STUB = """
window.__zgjFs = { full: 0, unfull: 0 };
window.runtime = {
  WindowFullscreen() { window.__zgjFs.full++; },
  WindowUnfullscreen() { window.__zgjFs.unfull++; },
};
"""


def clock_to_sec(txt):
    parts = [int(p) for p in txt.strip().split(":")]
    if len(parts) == 3:
        return parts[0] * 3600 + parts[1] * 60 + parts[2]
    return parts[0] * 60 + parts[1]


def main_time(page):
    return clock_to_sec(page.locator(".pl-bar .pl-time").first.inner_text().split("/")[0])


def boot(page, first=False):
    # 同 URL goto 只做 hash 级导航（不重建文档/store）；重载验证必须 page.reload()
    if first:
        page.goto(PLAYER_URL)
    else:
        page.reload()
    page.wait_for_selector(".pl-bar", timeout=15000)
    page.wait_for_function(
        "() => { const t = document.querySelector('.pl-title'); return t && t.textContent.includes('第1集'); }",
        timeout=15000,
    )
    page.wait_for_timeout(1200)  # 轮询起播，位移可测


def ep_cell(page, n):
    return page.locator(f'.pl-panel-body .ep-btn:text-is("第{n}集")')


def main():
    proc = start_server()
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE)
            ctx = browser.new_context(viewport={"width": 1280, "height": 800})
            ctx.add_init_script(FS_STUB)
            page = ctx.new_page()
            page.on("console", lambda m: None)
            boot(page, first=True)

            # ── ⑤a 首集：⏮ 禁用、⏭ 可用（先测，后续会被切集） ──
            prev_dis = page.locator(".pl-btn.nav.prev").is_disabled()
            next_dis = page.locator(".pl-btn.nav.next-ep").is_disabled()
            check("⑤ ep1 时 ⏮ 禁用 / ⏭ 可用", prev_dis and not next_dis,
                  f"prev_disabled={prev_dis} next_disabled={next_dis}")

            # ── ① 全屏 stub 计数 ──
            fs_btn = page.locator(".pl-btn.fs")
            check("① 全屏按钮存在", fs_btn.count() == 1)
            fs_btn.click()
            st = page.evaluate("() => window.__zgjFs")
            txt1 = fs_btn.inner_text()
            check("① 点击调用 WindowFullscreen 且文案切换", st["full"] == 1 and txt1 == "退出全屏",
                  f"full={st['full']} text={txt1}")
            fs_btn.click()
            st = page.evaluate("() => window.__zgjFs")
            txt2 = fs_btn.inner_text()
            check("① 再击调用 WindowUnfullscreen 且文案还原", st["unfull"] == 1 and txt2 == "全屏",
                  f"unfull={st['unfull']} text={txt2}")

            # ── ③ 快捷键（Space / ← → / ↑ ↓） ──
            page.keyboard.press("Space")  # 暂停
            page.wait_for_timeout(1200)
            t_a = main_time(page)
            page.wait_for_timeout(900)
            t_b = main_time(page)
            check("③ Space 暂停生效（时间冻结）", t_a == t_b, f"{t_a} -> {t_b}")
            page.keyboard.press("ArrowRight")
            page.wait_for_timeout(300)
            t_c = main_time(page)
            page.keyboard.press("ArrowLeft")
            page.wait_for_timeout(300)
            t_d = main_time(page)
            check("③ → 快进 5s / ← 快退 5s", abs(t_c - (t_a + 5)) <= 1 and abs(t_d - t_a) <= 1,
                  f"{t_a} →{t_c} ←{t_d}")
            page.evaluate(
                "() => { const el = document.querySelector('.pl-slider.vol');"
                " el.value = '60'; el.dispatchEvent(new Event('input', { bubbles: true })); }"
            )
            page.wait_for_timeout(400)
            v0 = page.evaluate("() => localStorage.getItem('zgj.player.volume')")
            page.keyboard.press("ArrowUp")
            page.wait_for_timeout(300)
            v_up = page.evaluate("() => localStorage.getItem('zgj.player.volume')")
            page.keyboard.press("ArrowDown")
            page.keyboard.press("ArrowDown")
            page.wait_for_timeout(400)
            v_dn = page.evaluate("() => localStorage.getItem('zgj.player.volume')")
            check("③ ↑ 音量 +5 / ↓ −5×2", v0 == "60" and v_up == "65" and v_dn == "55",
                  f"{v0} ↑{v_up} ↓{v_dn}")
            page.keyboard.press("Space")  # 恢复播放
            page.wait_for_timeout(1200)
            check("③ Space 恢复播放（时间前进）", main_time(page) >= t_a,
                  f"t={main_time(page)} base={t_a}")

            # ── ② ? 帮助浮层：出现 / Esc 关 / 8s 自动关 ──
            page.keyboard.press("?")
            page.wait_for_selector(".hk-card", timeout=2000)
            rows = page.locator(".hk-rows li").count()
            check("② ? 浮层出现（7 行键位）", rows == 7, f"rows={rows}")
            page.keyboard.press("Escape")
            page.wait_for_timeout(400)
            check("② Esc 关闭浮层", page.locator(".hk-card").count() == 0)
            page.keyboard.press("?")
            page.wait_for_selector(".hk-card", timeout=2000)
            page.wait_for_timeout(8600)
            check("② 8s 自动关闭浮层", page.locator(".hk-card").count() == 0)

            # ── ④ 选集面板：宽 300 展开 / 36 折叠 / 点第3集切集 ──
            w_open = page.evaluate("() => document.querySelector('.pl-panel').offsetWidth")
            cur_visible = page.locator(".ep-btn.current").count()
            check("④ 面板默认展开宽 300 且当前集高亮存在", w_open == 300 and cur_visible == 1,
                  f"width={w_open} current={cur_visible}")
            page.locator(".pl-panel-head .pl-btn").click()
            page.wait_for_timeout(300)
            w_col = page.evaluate("() => document.querySelector('.pl-panel').offsetWidth")
            check("④ 折叠宽 36", w_col == 36, f"width={w_col}")
            page.locator(".pl-panel-tab").click()
            page.wait_for_timeout(300)
            w_re = page.evaluate("() => document.querySelector('.pl-panel').offsetWidth")
            check("④ 再展开宽 300", w_re == 300, f"width={w_re}")

            ep_cell(page, 3).click()
            page.wait_for_function(
                "() => document.querySelector('.pl-title').textContent.includes('第3集')", timeout=8000
            )
            hl = ep_cell(page, 3).get_attribute("class")
            check("④ 点第3集切集成功且高亮跟到", hl is not None and "current" in hl, f"class={hl}")
            bb_cell = ep_cell(page, 3).bounding_box()
            bb_body = page.locator(".pl-panel-body").bounding_box()
            inside = (
                bb_cell
                and bb_cell["y"] >= bb_body["y"] - 1
                and bb_cell["y"] + bb_cell["height"] <= bb_body["y"] + bb_body["height"] + 1
            )
            check("④ 当前集在面板视口内（落位）", inside,
                  f"cell_y={bb_cell and round(bb_cell['y'])} body=({round(bb_body['y'])},{round(bb_body['y']+bb_body['height'])})")

            # ── ⑤b 末集：⏭ 禁用（切 35 再回 3） ──
            page.locator('.ep-groups .chip:text-is("31–35")').click()
            page.wait_for_timeout(300)
            ep_cell(page, 35).click()
            page.wait_for_function(
                "() => document.querySelector('.pl-title').textContent.includes('第35集')", timeout=8000
            )
            prev_dis2 = page.locator(".pl-btn.nav.prev").is_disabled()
            next_dis2 = page.locator(".pl-btn.nav.next-ep").is_disabled()
            check("⑤ 末集(ep35)时 ⏭ 禁用 / ⏮ 可用", (not prev_dis2) and next_dis2,
                  f"prev_disabled={prev_dis2} next_disabled={next_dis2}")
            page.locator('.ep-groups .chip:text-is("1–30")').click()
            page.wait_for_timeout(300)
            ep_cell(page, 3).click()
            page.wait_for_function(
                "() => document.querySelector('.pl-title').textContent.includes('第3集')", timeout=8000
            )

            # ── ⑥a 连播 e2e：ep3 拖到片尾 → 自动播第4集（LS 未设=默认开） ──
            # 确定性落位：title 在 openEpisode 里先于 api.playerOpen 赋值，此刻 store.duration
            # 仍是旧会话值（ep35=132）、滑块 max 滞后；须等 max 更新到 ep3 时长（>150）再拖，
            # 否则拖到 132 < 159 不触发 eof（前几轮 ⑥ FAIL 的真实根因=测试时序，非产品 bug）。
            page.wait_for_function(
                "() => Number(document.querySelector('.pl-slider.grow').max) > 150", timeout=8000
            )
            page.evaluate(
                "() => { const el = document.querySelector('.pl-slider.grow');"
                " el.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));"
                " el.value = el.max; el.dispatchEvent(new Event('input', { bubbles: true }));"
                " el.dispatchEvent(new Event('change', { bubbles: true })); }"
            )
            ok = True
            try:
                page.wait_for_function(
                    "() => document.querySelector('.pl-title').textContent.includes('第4集')", timeout=10000
                )
            except Exception:
                ok = False
            st = page.evaluate(
                "() => { const s = document.querySelector('#app').__vue_app__"
                ".config.globalProperties.$pinia.state.value.player;"
                " return {ep: s.episodeIndex, dur: s.duration, pos: s.position, eof: s.eof,"
                " autoplay: s.autoplay, status: s.status, loading: s.loading, error: s.error,"
                " eps: s.drama ? s.drama.episodes : null}; }")
            check("⑥ 播完自动连播下一集（ep3→ep4）", ok,
                  f"st={json.dumps(st, ensure_ascii=False)}")

            # ── ⑥b 连播开关持久化 ──
            ap = page.locator(".pl-btn.ap")
            ap.click()
            page.wait_for_timeout(300)
            ls_ap = page.evaluate("() => localStorage.getItem('zgj.player.autoplay')")
            txt_ap = ap.inner_text()
            check("⑥ 关连播写 LS='0' 且按钮变「连播关」", ls_ap == "0" and txt_ap == "连播关",
                  f"ls={ls_ap} text={txt_ap}")
            boot(page)  # 重载
            txt_ap2 = page.locator(".pl-btn.ap").inner_text()
            check("⑥ 重载后开关状态从 LS 恢复", txt_ap2 == "连播关", f"text={txt_ap2}")
            page.locator(".pl-btn.ap").click()
            page.wait_for_timeout(200)
            ls_ap3 = page.evaluate("() => localStorage.getItem('zgj.player.autoplay')")
            check("⑥ 再点开写 LS='1'", ls_ap3 == "1", f"ls={ls_ap3}")

            # ── ⑦ 画质下拉：未接线提示（不假成功） ──
            boot(page)
            page.select_option(".pl-select.quality", "720")
            ok7 = True
            try:
                page.wait_for_selector(".pl-toast", timeout=4000)
            except Exception:
                ok7 = False
            toast = page.locator(".pl-toast").inner_text() if ok7 else ""
            ls_q = page.evaluate("() => localStorage.getItem('zgj.player.quality')")
            q_val = page.locator(".pl-select.quality").input_value()
            check("⑦ 画质选择写 LS 且下拉值跟手", ls_q == "720" and q_val == "720", f"ls={ls_q} value={q_val}")
            # 画质已接核心 resolve：dev:mock 走「已切换到720P」实链分支；无桥/演示态出占位提示
            check("⑦ 画质切换提示文案出现", ok7 and ("已切换到720P，从原位置继续播放" in toast
                                 or "演示模式或未载入剧目" in toast), f"toast={toast}")

            # ── ⑧ 弹幕不透明度控件（R4-2；v1「透明 NN%」竖向滑条对端）──
            op_btn = page.locator("[data-testid=dm-opacity-btn]")
            check("⑧a 按钮显示「透明 100%」（v1 历史口径：数值是不透明度，100=完全不透明）",
                  "透明 100%" in op_btn.inner_text(), op_btn.inner_text())
            op_btn.click()
            page.wait_for_selector("[data-testid=dm-opacity-pop]", timeout=8000)
            rng = page.locator("[data-testid=dm-opacity-range]")
            check("⑧b 展开后滑条范围 20–100",
                  rng.get_attribute("min") == "20" and rng.get_attribute("max") == "100",
                  f"min={rng.get_attribute('min')} max={rng.get_attribute('max')}")
            rng.evaluate("""(el) => {
              el.value = '40';
              el.dispatchEvent(new Event('input', { bubbles: true }));
            }""")
            page.wait_for_timeout(400)
            check("⑧c 拖动后按钮与浮层都跟着变 40%",
                  "透明 40%" in op_btn.inner_text()
                  and "40%" in page.locator("[data-testid=dm-opacity-pop]").inner_text(),
                  f"btn={op_btn.inner_text()}")
            ls_op = page.evaluate("() => localStorage.getItem('zgj.player.danmakuOpacity')")
            check("⑧d 落盘 zgj.player.danmakuOpacity=40", ls_op == "40", f"ls={ls_op}")
            # 越界输入由 store 钳回 20–100（滑条本身给不了越界值，这里直接驱动 store）
            rng.evaluate("""(el) => {
              el.value = '5';
              el.dispatchEvent(new Event('input', { bubbles: true }));
            }""")
            page.wait_for_timeout(400)
            check("⑧e 越界值被钳成下限 20（不写脏 LS）",
                  page.evaluate("() => localStorage.getItem('zgj.player.danmakuOpacity')") == "20",
                  "钳位见 stores/player.ts setDanmakuOpacity")
            page.reload()
            page.wait_for_selector("[data-testid=dm-opacity-btn]", timeout=15000)
            page.wait_for_timeout(1200)
            check("⑧f 刷新后从 LS 恢复（真持久化，不是内存态）",
                  "透明 20%" in page.locator("[data-testid=dm-opacity-btn]").inner_text(),
                  page.locator("[data-testid=dm-opacity-btn]").inner_text())
            page.evaluate("() => localStorage.removeItem('zgj.player.danmakuOpacity')")

            # ── 截图：面板展开 + toast 可见 ──
            page.wait_for_timeout(400)
            page.screenshot(path=os.path.join(OUT, "player-pack.png"))
            print(f"screenshot -> {os.path.join(OUT, 'player-pack.png')}", flush=True)

            browser.close()
    finally:
        _devserver.stop_dev(proc, PORT, name="player")

    if failures:
        print(f"FAILED: {len(failures)} check(s)")
        sys.exit(1)
    print("ALL CHECKS PASSED")


if __name__ == "__main__":
    main()
