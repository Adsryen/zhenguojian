#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""calendar_paging_verify.py —— 日历分页的六条行为（照 v1 的测试名逐条对）

来源：v1 `zhenguojian/app/zhenguojian/test_calendar_panel.py` 里这六条用例
（行号在下面的断言名里）——那是 v1 自己钉住的行为，不是我发明的规格：
  :215 existing_first_page_is_not_requested_twice
  :222 short_first_pages_fill_viewport_automatically
  :236 resize_refills_visible_space
  :248 duplicate_pages_stop_automatic_requests_and_can_retry
  :274 rapid_gender_change_discards_stale_result
  :286 failed_page_retries_same_cursor_without_clearing_items

v2 之前的状态（本轮对账结论）：
  · `api.ts` 的 mock `calendar: async (date) => mockCalendarPage(date)`
    **把 offset 与 gender 直接丢掉**，默认 mock 永远 `hasMore: false`
    → 这六条在这台机器上**一条都触发不了**（不是"实现对了所以不用测"，
      是 fixture 太配合所以走不到那条路）；
  · 视图只有"滚到底 loadMore"，没有视口补页、没有 resize 重填、
    没有重复页停摆，`statusText` 写着「点此重试」而它是个**不可点的 div**；
  · `setGender`/`selectDate`/`shiftDay` 用 `if (this.loading) return`
    把用户的新点击**整个丢掉**，而 v1 是"照样发新请求、丢弃过期响应"。
本轮把这四条补上，本脚本负责钉住。

⚠ 测不到的那两条也说清楚（不假装覆盖）：
  · `theme_colors_and_controls_fit_at_narrow_width` / `themes_update_existing_calendar_rows`
    是 Qt 主题重绘的测试；web 侧由 CSS 变量承担，本脚本只顺带看窄宽控件不溢出，
    **没有**断言"主题切换会重绘已存在行"——那是 Qt 才需要的机制。

端口：自管理 mock vite @5211（`_devserver`）。
测试缝：`window.__zgjCalPaging`（见 fixtures.ts 的 mockCalendarPagePaged 注释）。
"""
import json
import re
import os
import sys
import time

import _devserver
from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = "5211"
BASE = f"http://127.0.0.1:{PORT}"
EXE = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell")
OUT = HERE
CAL = "[data-testid=cal-status]"
CARD = ".cal-card, .cal-item"

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail[:200]}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


def set_paging(page, cfg):
    page.evaluate("(c) => { window.__zgjCalPaging = c }", cfg)


def rows(page):
    return page.evaluate("""() => {
      const st = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia._s.get('calendar');
      return st.rows.map((r) => r.id);
    }""")


def status(page):
    el = page.locator(CAL)
    return el.first.inner_text().strip() if el.count() else ""


def fresh_open(ctx, cfg):
    """开一个**全新 page**（新文档 = 新 store 实例）。

    ⚠ 为什么必须这样：pinia store 是 SPA 单例，而 `onMounted` 里有
    `if (!store.loadedOnce) void store.load(...)` —— 换 hash 再进来**不会重置游标**，
    上一场景留下的 pageSize/nextOffset 会串进下一场景。
    本轮我第一版就是这么测出"重复页连击只有 1"的假失败：
    ① 场景把 pageSize 设成 1、游标推到 7，⑤ 场景继承了这个状态，
    重复窗口 [4,10) 里只落在两次请求上。
    顺带说明这不是产品 bug：v1 `:215` 测的正是"同一份数据在飞行中不重复请求首屏"，
    v2 的 loadedOnce 是它的对应实现。"""
    page = ctx.new_page()
    errs = []
    page.on("pageerror", lambda e: errs.append(str(e)[:120]))
    # ⚠ 必须先等应用真的挂载完，再设测试缝、再切路由：
    # `page.goto` 只保证文档存在，不保证 Vue 已经起来。
    # 第一版没等这一步，`.cal-scroll` 等到超时——
    # 而我手工 debug 版加了 1.5s 等待就能过，差别就在这里（不是产品问题）。
    page.goto(BASE + "/#/discover")
    page.wait_for_selector(".page-title, .nav-rail", timeout=25000)
    page.evaluate("(c) => { window.__zgjCalPaging = c; window.__zgjCalLog = [] }", cfg)
    page.goto(BASE + "/#/calendar")
    page.wait_for_selector(".cal-scroll", timeout=25000)
    page.wait_for_timeout(2000)
    return page, errs


def main():
    proc = _devserver.start_dev(FRONTEND, PORT, True, HERE, name="cal-paging")
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE, headless=True)
            ctx = browser.new_context(viewport={"width": 1280, "height": 700})
            page = ctx.new_page()
            js_errors = []
            page.on("pageerror", lambda e: js_errors.append(str(e)[:150]))
            # ── ① 首屏短 → 自动补页（v1 :222）──
            page.goto(BASE + "/#/discover")
            page.wait_for_timeout(1500)
            set_paging(page, {"pageSize": 1, "total": 12})
            page.goto(BASE + "/#/calendar")
            page.wait_for_selector(".cal-scroll", timeout=25000)
            page.wait_for_timeout(3500)
            n1 = len(rows(page))
            check("①a 首屏只有 1 条时自己补页（v1 short_first_pages_fill_viewport_automatically）",
                  n1 >= 3, f"rows={n1}")
            overflow = page.evaluate(
                "() => { const e = document.querySelector('.cal-scroll');"
                " return e.scrollHeight > e.clientHeight + 8 }")
            check("①b 补到内容超过视口才停（不是无限补）", overflow is True, f"overflow={overflow}")

            # ── ② 游标确实推进（v1 :215 的那条要 count 请求次数，
            # 需要 store 侧记录；本轮改用可观察的 nextOffset 判"翻页在推进"，
            # 并把"首页不重复请求"列为**未覆盖**，写在末尾 NOTE 里）──
            offs = page.evaluate("""() => {
              const st = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia._s.get('calendar');
              return st.nextOffset;
            }""")
            check("② 游标推进过（说明翻页是真的推进而不是原地重复）",
                  int(offs) >= 2, f"nextOffset={offs}")

            # ── ③ resize 后继续填满（v1 :236）──
            before = len(rows(page))
            page.set_viewport_size({"width": 1280, "height": 1600})
            page.wait_for_timeout(3000)
            after = len(rows(page))
            check("③ 视口拉高后自动补页（v1 resize_refills_visible_space）",
                  after > before, f"{before} → {after}")

            # ── ④ 快速切性别：最终数据来自最后一次请求（v1 :274）──
            page.set_viewport_size({"width": 1280, "height": 700})
            set_paging(page, {"pageSize": 3, "total": 9, "slowMs": 1200, "gender": 1})
            gender_btns = page.locator(".gender-chips .chip")
            check("④a 性别胶囊有 2 个（全部/男频/女频里非当前的那两个可点）",
                  gender_btns.count() >= 2, f"count={gender_btns.count()}")
            # 连点两次不同频道，第一次的请求要能被第二次的响应覆盖掉
            gender_btns.nth(1).click()
            page.wait_for_timeout(150)
            gender_btns.nth(2).click()
            page.wait_for_timeout(4000)
            st4 = page.evaluate("""() => {
              const st = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia._s.get('calendar');
              // 标题挂在 r.drama.title（normalizeItem 两个分支都这样），
              // 我第一版读 r.title 全是 undefined，all() 里拿到 None 直接 TypeError。
              return { gender: st.gender, titles: st.rows.map(r => (r.drama && r.drama.title) || '') };
            }""")
            titles4 = st4.get("titles") or []
            last_label = gender_btns.nth(2).inner_text().strip()
            check("④b 连点两格后，数据来自**最后一次**点击（旧响应被丢弃）",
                  bool(titles4) and all(("[g" + str(st4["gender"]) + "]") in t for t in titles4),
                  f"gender={st4['gender']} titles={titles4[:2]} 最后点的={last_label!r}")
            # ⚠ 别把频道值写死：v2 的 GENDER_CHIPS 是 全部=2 / 男频=1 / 女频=0
            #   （CalendarView.vue:24-28），我第一版按"0=全部"反着猜，
            #   于是这条断言会去对一个错误的期望值。
            #   现在从页面自己的 chip 顺序读回来，跟视图共用同一份映射。
            want_g = page.evaluate("""(label) => {
              const chips = [...document.querySelectorAll('.gender-chips .chip')];
              const i = chips.findIndex((c) => c.innerText.trim() === label);
              if (i < 0) return null;
              const st = document.querySelector('#app').__vue_app__
                .config.globalProperties.$pinia._s.get('calendar');
              // chip 顺序与 store 的 GENDER_CHIPS 一致（v: 2,1,0）
              return [2, 1, 0][i];
            }""", last_label)
            check("④c store.gender 等于最后点击那一格的值（值从页面自己的 chip 映射读，不写死）",
                  want_g is not None and int(st4["gender"]) == int(want_g),
                  f"gender={st4['gender']} 期望={want_g} 标签={last_label!r}")

            # ── ⑤ 连续重复页 → 停止自动请求 + 文案 + 可点重试（v1 :248）──
            # 重复窗口取 [2, 2+2*3) = [2,8)：第一页(offset 0)正常，从第二页起全是重复，
            # 客户端该在连击 3 页后自己停下来。
            page5, err5 = fresh_open(ctx, {"pageSize": 2, "total": 20, "repeatFrom": 2})
            for _ in range(8):
                page5.evaluate("() => { const e=document.querySelector('.cal-scroll'); e.scrollTop = e.scrollHeight }")
                page5.wait_for_timeout(700)
            st5 = status(page5)
            stalled = page5.evaluate("""() => {
              const st = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia._s.get('calendar');
              return { stalled: st.stalled, hasMore: st.hasMore, n: st.rows.length, offset: st.nextOffset };
            }""")
            seq5 = page5.evaluate("() => window.__zgjCalLog || []")
            rep = [c for c in seq5 if c.get("repeating")]
            offsets = [c["offset"] for c in seq5]
            check("⑤a 请求序列进入重复区且游标持续推进（不是原地重复同一页）",
                  len(rep) >= 3 and offsets == sorted(set(offsets)),
                  f"序列={offsets} 重复次数={len(rep)}")
            check("⑤b 连击 3 页后停摆：文案是 v1 原话「连续多页没有新剧」，hasMore=false",
                  "连续多页没有新剧" in st5 and stalled.get("stalled") is True
                  and stalled.get("hasMore") is False,
                  f"status={st5!r} stalled={stalled.get('stalled')} hasMore={stalled.get('hasMore')}")
            n_before5 = stalled.get("n")
            clickable = page5.locator(CAL + ":not([disabled])").count()
            check("⑤c 状态行**真的可点**（原来是个不可点的 div，文案却在说点此重试）",
                  clickable >= 1, f"clickable={clickable}")
            off_at_stall = int(stalled.get("offset", 0))
            page5.locator(CAL).first.click()
            page5.wait_for_timeout(2500)
            st5b = page5.evaluate("""() => {
              const st = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia._s.get('calendar');
              return { stalled: st.stalled, offset: st.nextOffset, n: st.rows.length };
            }""")
            check("⑤d 点重试后解除停摆，且请求的是**下一页**（游标比停摆时更大）",
                  st5b.get("stalled") is False and int(st5b.get("offset", 0)) > off_at_stall,
                  f"停摆时={off_at_stall} 现在={st5b.get('offset')}")
            check("⑤e 重试不清空已加载（行数不减）",
                  st5b.get("n", 0) >= n_before5, f"{n_before5} → {st5b.get('n')}")
            js_errors.extend(err5)
            page5.close()

            # ── ⑥ 失败页：同游标重试且不清空（v1 :286）──
            page6, err6 = fresh_open(ctx, {"pageSize": 2, "total": 12, "failFrom": 2})
            for _ in range(4):
                page6.evaluate("() => { const e=document.querySelector('.cal-scroll'); e.scrollTop = e.scrollHeight }")
                page6.wait_for_timeout(900)
            st6 = status(page6)
            check("⑥a 翻页失败时状态行是「加载失败：…（点此重试）」",
                  "加载失败" in st6 and "点此重试" in st6, f"status={st6!r}")
            rows6 = page6.evaluate("""() => {
              const st = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia._s.get('calendar');
              return { n: st.rows.length, offset: st.nextOffset };
            }""")
            check("⑥b 失败**没有清空已加载**", rows6.get("n", 0) >= 2, json.dumps(rows6))
            check("⑥c 失败后游标停在原地（重试拿同一页，而不是跳过一页丢内容）",
                  int(rows6.get("offset", 0)) == 2, f"nextOffset={rows6.get('offset')}")
            # 关掉失败注入后点重试 → 同一游标真的补上
            page6.evaluate("() => { window.__zgjCalPaging = { pageSize: 2, total: 12 } }")
            page6.locator(CAL).first.click()
            page6.wait_for_timeout(2500)
            n6b = len(rows(page6))
            check("⑥d 点重试后同一页真的补了进来", n6b > rows6.get("n", 0),
                  f"{rows6.get('n')} → {n6b}")
            js_errors.extend(err6)
            page6.close()

            # ── ⑦ 窄宽下控件不溢出（v1 :304 的一半，说清只测一半）──
            page.set_viewport_size({"width": 760, "height": 700})
            page.wait_for_timeout(1200)
            fits = page.evaluate("""() => {
              const chips = document.querySelector('.gender-chips');
              if (!chips) return null;
              const r = chips.getBoundingClientRect();
              return r.right <= window.innerWidth + 1 && r.left >= -1;
            }""")
            check("⑦ 760px 宽下频道胶囊不溢出（v1 narrow_width 测试的**一半**；主题重绘 web 不需要）",
                  fits is True, f"fits={fits}")

            check("⑧ 全程无 JS 错误", not js_errors, "; ".join(js_errors[:3]))
            print("NOTE 覆盖边界：①v1 的 theme_* 两条是 Qt 重绘语义，web 由 CSS 变量承担，没有断言；"
                  "②v1 :215「首页不被请求两次」需要数请求次数，本轮只断言了游标推进 —— 未覆盖，不假装")
            page.screenshot(path=os.path.join(OUT, "calendar-paging.png"))
            browser.close()
    finally:
        _devserver.stop_dev(proc, PORT, name="cal-paging")

    print(f"\n===== calendar_paging_verify: {'ALL PASS' if not failures else 'FAILURES: ' + ', '.join(failures)} =====")
    print(f"screenshot -> {os.path.join(OUT, 'calendar-paging.png')}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
