#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""next_season_verify.py —— R6：季末查到下一季后，把入口写回追剧记录（v1 mark_next_season）。

验的是这条以前**断掉的链**：v1 `main.py:2598` 在「本季播完且查到下一季」时调
`watchstore.mark_next_season`，把 `nextDrama/nextSeason/newSeason/arrived` 记在**旧季**行上，
追剧列表双击/「去观看」才可能直达下一季。v2 里 `Store.MarkNextSeason` 与它的单测早就齐了，
缺的是对外绑定与前端调用点 —— 所以之前那句「Go 侧 SetNextSeason 绑定待补」只对了一半。

怎么把这条链跑起来（不改 mock 基础数据集）：
  · `window.__zgjNextSeasonRows` 注入「查到的下一季行」（nextSeason.ts 里的后门，
    与 `__zgjDiscoverInjectCatalog` / `__zgjCoverFault` 同路数；注入值同样过 groupSeasons，
    不绕过季号归并）；
  · 追剧关系用 `zgj.follows` 播种（mock 面的 watchstore 就是这个键）；
  · 拖进度条到片尾触发 eof → store.checkNextSeason → api.setNextSeason。

断言：
  ① 已追 + 查到下一季 → `zgj.follows[id]` 长出 nextDrama/nextSeason=2/newSeason/arrived
  ② 同时 UI 给「去看 …第二季 ▸」入口（v1：不跨季自动播放，等用户确认）
  ③ **没追剧时 marked=false 且不谎报**：`zgj.follows` 里不会凭空出现这一行
     （v1 mark_next_season 对未追的 id 直接 return False）
  ④ 点入口 → 真跳去下一季（URL 换成下一季 id）

端口：自管理 mock vite @5203（`_devserver`，进门断言端口为空、退出回查）。
"""
import json
import os
import sys
import urllib.parse

import _devserver
from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FRONT = os.path.abspath(os.path.join(HERE, ".."))
PORT = "5203"
BASE = f"http://127.0.0.1:{PORT}"
EXE = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell")

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


JS_STATE = """() => {
  const s = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia.state.value.player;
  return s.drama ? { id: s.drama.id, title: s.drama.title, source: s.drama.source || 'hongguo',
                     episodes: s.drama.episodes, ep: s.episodeIndex } : null;
}"""


def open_last_episode(page, follow: bool):
    """打开 mock 剧的**最后一集**（`hasNextEpisode=false` 才会走「查下一季」分支）。

    URL 必须带 `title`：`?drama=<id>` 这种简写链接只有 id，store.drama.title 会是空串
    （第一版就栽在这里——空标题让 `lookupNextSeason` 开头 `if (!title) return null` 直接返回，
    ①② 全红，而且失败得毫无线索）。带上标题后 seriesKey/seasonNumber 才有得算，
    注入的「…第二季」也才能与当前季号比大小。
    """
    q = urllib.parse.quote
    base_d = "mock_drama_1"
    title = "R6验证剧 第一季"
    next_title = "R6验证剧 第二季"
    page.goto(f"{BASE}/#/player?drama={q(base_d)}&title={q(title)}&ep=1")
    # 导航刚落地时 Pinia 可能还没挂上（`state.value.player` undefined），
    # 所以整条取值链都要判空——第一版没判，wait_for_function 直接抛 TypeError。
    page.wait_for_function(
        "() => { const app = document.querySelector('#app');"
        " const st = app && app.__vue_app__ && app.__vue_app__.config"
        " && app.__vue_app__.config.globalProperties.$pinia"
        " && app.__vue_app__.config.globalProperties.$pinia.state.value;"
        " const s = st && st.player; return !!(s && s.drama && s.drama.title && s.drama.episodes > 1); }",
        timeout=25000)
    st = page.evaluate(JS_STATE)
    seed = """([follow, st, nextTitle]) => {
      const rows = follow ? { [st.id]: { drama: { id: st.id, title: st.title,
                                                  source: st.source, episodes: st.episodes },
                                        ts: Date.now() } } : {};
      localStorage.setItem('zgj.follows', JSON.stringify(rows));
    }"""
    page.evaluate(seed, [follow, st, next_title])
    # 注入用 evaluate 打在**当前页**：只换 hash 不重载文档，`add_init_script` 根本不会重放
    # （更早一版就是这么静默拿到 rowsInjected=null，查不到下一季、①② 红得毫无线索）。
    # lookupNextSeason 在调用时才读全局，够用。
    # ⚠ `([rows]) => …` 是**解构**：playwright 传进去的数组会被拆成第一个元素，
    # 于是全局拿到的不是数组而是那个对象，`Array.isArray()` 判假 →
    # 注入被静默忽略、走真 catalog 查询 → mock 里没有第二季 → 「查无下一季」，
    # ①② 红但完全看不出是参数写法的问题（我在这上面绕了三版）。
    # 单参数就用 `(rows) => …`，别解构。
    page.evaluate(
        "(rows) => { window.__zgjNextSeasonRows = rows; }",
        [{"id": base_d + "-s2", "title": next_title, "source": st["source"], "episodes": 20}])

    # 走到末集 = 真实用户路径，两步。这里的实测数字（不是猜的）：
    #   分组 chip = ["1–30", "31–35"]（EpisodeGrid 按 GROUP=30 分组），
    #   `cells` **只渲染当前组**，所以 `querySelectorAll('.ep-btn')` 的最后一个
    #   只是"当前组最后一格"（ep=1 时是第30集）——上一轮点它当然到不了末集。
    #   先点最后一个 chip，cells 变成 ["第31集"…"第35集"]，再点最后一格才落到 ep=35。
    # 面板是异步出格的（chapters 回来才渲染），必须先等到格子存在再点。
    # 上一版就是这里点空了：chips.length===0 → 什么都没发生，
    # 后面那次「最后一格」点的是初始组（1–30）的末格第30集，于是永远等不到 ep=35。
    page.wait_for_selector(".ep-btn", timeout=25000)
    has_groups = page.evaluate("() => document.querySelectorAll('.ep-groups .chip').length")
    if has_groups:
        page.evaluate("""() => {
          const chips = document.querySelectorAll('.ep-groups .chip');
          chips[chips.length - 1].click();
        }""")
        page.wait_for_timeout(700)
    page.evaluate("""() => {
      const btns = document.querySelectorAll('.ep-btn');
      if (btns.length) btns[btns.length - 1].click();
    }""")
    page.wait_for_timeout(1500)
    # 末集到位才算进入「本季播完」分支的前提（hasNextEpisode=false）
    page.wait_for_function(
        "() => { const st = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia"
        ".state.value.player; return st.episodeIndex === st.drama.episodes; }", timeout=15000)
    page.wait_for_function("() => Number(document.querySelector('.pl-slider.grow').max) > 10", timeout=15000)
    page.evaluate(
        "() => { const el = document.querySelector('.pl-slider.grow');"
        " el.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));"
        " el.value = el.max; el.dispatchEvent(new Event('input', { bubbles: true }));"
        " el.dispatchEvent(new Event('change', { bubbles: true })); }")
    return st


def read_follows(page):
    return page.evaluate("() => JSON.parse(localStorage.getItem('zgj.follows') || '{}')")


def main():
    proc = _devserver.start_dev(FRONT, PORT, True, HERE, name="next-season")
    shot = os.path.join(HERE, "next-season-entry.png")
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE, headless=True)

            # ── ①② 已追：回写 + UI 入口 ──
            ctx = browser.new_context(viewport={"width": 1280, "height": 760})
            page = ctx.new_page()
            st = open_last_episode(page, follow=True)
            entry = page.locator("[data-testid=pl-next-season]")
            if entry.count() == 0:  # 没打 testid 就用文案找（PlayerView 的 next 区块按钮）
                entry = page.get_by_role("button", name="第二季 ▸")
            ok_hit = False
            try:
                entry.first.wait_for(timeout=8000)
                ok_hit = True
            except Exception:
                ok_hit = False
            rows = read_follows(page)
            row = rows.get(st["id"]) or {}
            check("① 已追 + 查到下一季 → 回写 nextDrama/nextSeason/newSeason/arrived",
                  bool(row.get("nextDrama")) and row.get("nextSeason") == 2
                  and row.get("newSeason") is True and row.get("arrived") is True,
                  f"row={json.dumps({k: row.get(k) for k in ('nextSeason', 'newSeason', 'arrived')}, ensure_ascii=False)}")
            check("② UI 给「去看 …第二季 ▸」入口（不自动跨季播放，v1 告诫）", ok_hit,
                  f"entry={entry.first.inner_text() if ok_hit else '未出现'}")
            page.screenshot(path=shot)

            # ── ④ 点入口：真跳去下一季 ──
            if ok_hit:
                entry.first.click()
                page.wait_for_timeout(2500)
                check("④ 点入口跳到下一季（URL 换成注入的 s2 id）",
                      "mock_drama_1-s2" in page.url, page.url)
            ctx.close()

            # ── ③ 未追：marked=false 且不凭空造行（v1 不谎报） ──
            ctx2 = browser.new_context(viewport={"width": 1280, "height": 760})
            page2 = ctx2.new_page()
            st2 = open_last_episode(page2, follow=False)
            page2.wait_for_timeout(2500)
            rows2 = read_follows(page2)
            check("③ 未追剧时不回写：zgj.follows 里不会凭空出现这一行",
                  st2["id"] not in rows2, f"follows={list(rows2.keys())}")
            ctx2.close()

            browser.close()
    finally:
        _devserver.stop_dev(proc, PORT, name="next-season")

    print(f"\n===== next_season_verify: {'ALL PASS' if not failures else 'FAILURES: ' + ', '.join(failures)} =====")
    print(f"screenshot -> {shot}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
