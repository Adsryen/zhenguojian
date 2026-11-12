#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""history_local_play_verify.py —— 历史行的「本地播放」入口（R22a 第②件）

v1 的分支（watch.py:636-645）：历史/追剧行带 `local_path` 且文件还在 →
直接播本地成品（带当时进度），标题「本地播放 <文件名>」，
**不清空选集条也不给上下集**（main.py:2612-2618），
文件不在就提示「本地文件已不存在：<文件名>」并且**不回落线上**。

到本轮为止 v2 三段全断（Go 有 Plan.Local / 有 SaveLocalWatch 但没人调 /
前端 localPath 只在类型里出现过），前三轮补齐了 Go 侧与写入侧，本笔补最后一段：入口。

断言：
  ① 历史行带 localPath 才会出现本地入口（mock historyList 得把字段带出来——
     上一版没带，① 直接红，这是证据装置缺字段而不是产品没做）
  ② 点进去：标题是「本地播放 <文件名>」（v1 main.py:2634 原文）
  ③ 选集侧栏**整块不渲染** + 上下集 disabled（v1 的清空选集条 / set_neighbours）
  ④ 会话不属于任何一集：episodeIndex 0、路由带 lp
  ⑤ 进度**真的写回了一条**（ts 前进 + position 变了 + 仍带 localPath）。
     上一版这里只比 localPath，而那是我灌进去的种子值——写入压根没发生也照样绿；
     顺着这条查下去发现的是契约缺陷（前端用 !!episode.index 推断"会话已打开"，
     本地会话恒 0 → 进度节流整条不触发），已在 Go/TS 两侧补 `Snapshot.local`。
  ⑥ 文件不存在（`__zgjLocalMissing` 注入）：文案「本地文件已不存在：<文件名>」，
     且没有「重试」死按钮、没有回落演示模式（演一段假画面就是骗人）
  ⑦ 拿到 outside_root 这个 code 时前端说的话（守卫本身由 Go 单测覆盖，
     mock 面没有真实目录、不假装实现它——只喂 code）
  ⑧ 没有 localPath 的普通行：仍旧走线上 resolve（回归，标题是「剧名 · 第N集」）
  ⑨ en 态两条错误都是英文

端口：自管理 mock vite @5208（`_devserver`）。
"""
import os
import sys

import _devserver
from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = "5208"
BASE = f"http://127.0.0.1:{PORT}"
EXE = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell")
OUT = HERE

SEED_TS = 1763000000000            # ⑤ 用它证明"后来真的写过一条"
D1 = "hongguo:7439220582928952383"   # 一世之尊（有分集，走线上）
D2 = "hongguo:7421530981234567890"   # 闪婚老公是大佬（本笔给它一条本地成品）
LOCAL = r"C:\\Users\\ryen\\Downloads\\成品\\闪婚老公是大佬 S01 合并.mp4"

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail[:160]}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


def seed_history(page, with_local=True):
    """直接写 mock 的历史表：两行，一行带 localPath、一行不带。"""
    rows = {
        f"{D2}:1": {
            "title": "闪婚老公是大佬", "episode": 1, "position": 40, "duration": 120,
            "ts": SEED_TS, "dramaId": D2, "cover": "/posters/p2.svg",
        },
        f"{D1}:3": {
            "title": "一世之尊", "episode": 3, "position": 30, "duration": 100,
            "ts": SEED_TS - 10000000, "dramaId": D1, "cover": "/posters/p1.svg",
        },
    }
    if with_local:
        rows[f"{D2}:1"]["localPath"] = LOCAL
    page.evaluate(
        """([key, rows]) => {
          localStorage.setItem(key, JSON.stringify(rows));
          localStorage.removeItem('zgj.localMissing');
        }""", ["zgj" + ".history", rows])


def goto_history(page):
    page.goto(f"{BASE}/#/history")
    page.wait_for_selector(".hrow-main", timeout=20000)
    page.wait_for_timeout(1200)


def main():
    proc = _devserver.start_dev(FRONTEND, PORT, True, OUT, name="local-play")
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE, headless=True)
            page = browser.new_context(viewport={"width": 1280, "height": 800}).new_page()

            # ── ① 带 localPath 的历史行 ──
            page.goto(BASE + "/#/discover")
            page.wait_for_selector(".page-title", timeout=20000)
            seed_history(page)
            goto_history(page)
            rows = page.locator(".hrow-main")
            check("① 历史页两行都在（mock historyList 把 localPath 带出来了）",
                  rows.count() == 2, f"rows={rows.count()}")

            # ── ②③④ 点带 localPath 的那一行 ──
            rows.first.click()
            page.wait_for_selector(".pl-title", timeout=20000)
            page.wait_for_timeout(1800)
            title = page.locator(".pl-title").inner_text().strip()
            check("② 播放器标题是「本地播放 <文件名>」（v1 main.py:2634 原文）",
                  title == "本地播放 闪婚老公是大佬 S01 合并.mp4", title)
            check("③ 路由带 lp 参数（本地会话的标记在 URL 上，刷新也还是本地）",
                  "lp=" in page.url, page.url[-70:])
            check("③b 选集侧栏整块不渲染（本地文件没有选集可言）",
                  page.locator(".pl-panel").count() == 0,
                  f"panel 数={page.locator('.pl-panel').count()}")
            prev = page.locator(".pl-btn.nav.prev")
            nxt = page.locator(".pl-btn.nav.next-ep")
            check("③c 上下集都在但 disabled（v1 set_neighbours(False,False)）",
                  prev.count() == 1 and nxt.count() == 1
                  and prev.is_disabled() and nxt.is_disabled(),
                  f"prev={prev.count()} next={nxt.count()}")

            # ── ⑤ 进度走本地那条写入 ──
            page.wait_for_timeout(6000)  # 等节流窗口（≥5s）过一次
            # ⑤ 必须证明"发生了写入"，而不是"字段看起来对"：
            # 上一版只比 localPath，而那是我**自己灌进去的种子值**——探查脚本实测：
            # 等 7 秒那条记录的 ts/position 一个数都没动，断言却照样绿。
            # 真因后来查到在契约上（见 types.ts 里 Snapshot.local 的注释）：
            # 前端用 `!!episode.index` 判断会话是否已打开，本地会话恒 0 → running=false →
            # 进度节流整条不触发。所以现在比三件事：ts 前进、position 变了、localPath 还在。
            stored = page.evaluate(
                """(prefix) => {
                  const all = JSON.parse(localStorage.getItem('zgj.history') || '{}');
                  const k = Object.keys(all).find(x => x.startsWith(prefix));
                  return k ? all[k] : null;
                }""", D2)
            check("⑤ 本地会话真的写回了一条进度（ts 前进 + position 变了 + 仍带 localPath）",
                  bool(stored) and stored.get("ts", 0) > SEED_TS and stored.get("position") != 40
                  and str(stored.get("localPath", "")).endswith("闪婚老公是大佬 S01 合并.mp4"),
                  f"stored={stored}")

            # ── ⑥ 文件不存在的分支 ──
            page.goto(BASE + "/#/history")
            page.wait_for_timeout(1200)
            # 测试缝：让 Go/mock 报 local_missing（默认视为存在）
            page.evaluate("""(p) => { window.__zgjLocalMissing = [p] }""", LOCAL)
            page.locator(".hrow-main").first.click()
            page.wait_for_selector(".pl-hint", timeout=20000)
            page.wait_for_timeout(1500)
            hint = page.locator(".pl-hint").first.inner_text().strip()
            check("⑥a 文件不在：文案「本地文件已不存在：<文件名>」（v1 watch.py:641 同句）",
                  hint.startswith("本地文件已不存在：闪婚老公是大佬 S01 合并.mp4"), hint)
            check("⑥b 不给「重试」死按钮（重试只会拿到同一条错误）",
                  page.locator("[data-testid=player-retry]").count() == 0, "")
            # ⚠ 上一版查的是 `[data-testid=player-demo]`——那个 testid **本仓不存在**，
            # count()==0 恒真，等于一条永真断言（"绿灯≠覆盖面"的又一副面孔）。
            # 演示模式的真标志是 `.pl-badge.demo`（PlayerView:376，文案 player.badgeDemo）。
            check("⑥c 不回落演示模式（假画面等于骗人）",
                  page.locator(".pl-badge.demo").count() == 0,
                  f"demo chip={page.locator('.pl-badge.demo').count()}")

            # ── ⑦ 越界路径 ──
            page.goto(BASE + "/#/history")
            page.wait_for_timeout(1200)
            OUTSIDE = "C:\\Windows\\System32\\notepad.exe"
            page.evaluate("""([prefix, p]) => {
              const all = JSON.parse(localStorage.getItem('zgj.history') || '{}');
              const k = Object.keys(all).find(x => x.startsWith(prefix));
              all[k].localPath = p;
              localStorage.setItem('zgj.history', JSON.stringify(all));
              // 越界判定在 Go（checkLocalPlayable 六种形状 + 折叠两平台语义都有单测）。
              // mock 面没有真实目录、不假装实现了那条规则：这里只是**喂 code 给前端分支**。
              window.__zgjLocalOutside = [p];
              window.__zgjLocalMissing = [];
            }""", [D2, OUTSIDE])
            # ⚠ 缝必须在 reload **之后**设：window 上的临时变量不跨重载，
            # 第一版写在 reload 前，⑦ 因此永远等不到 .pl-hint（超时看起来像"守卫没生效"）。
            page.reload()
            page.wait_for_selector(".hrow-main", timeout=20000)
            page.wait_for_timeout(1200)
            page.evaluate("""([p]) => { window.__zgjLocalOutside = [p]; window.__zgjLocalMissing = []; }""", [OUTSIDE])
            page.locator(".hrow-main").first.click()
            page.wait_for_selector(".pl-hint", timeout=20000)
            page.wait_for_timeout(1200)
            hint2 = page.locator(".pl-hint").first.inner_text().strip()
            check("⑦ outside_root 这个 code 映射到「只允许播放下载目录下的成品文件」（句子归前端，守卫归 Go 单测）",
                  "只允许播放下载目录下的成品文件" in hint2, hint2)

            # ── ⑧ 没有 localPath 的行仍走线上 ──
            page.goto(BASE + "/#/history")
            page.wait_for_timeout(1200)
            page.evaluate("""() => { window.__zgjLocalMissing = []; window.__zgjLocalOutside = []; }""")
            target = page.locator(".hrow-main").filter(has_text="一世之尊")
            check("⑧a 找到不带 localPath 的那一行", target.count() == 1, f"count={target.count()}")
            target.first.click()
            page.wait_for_selector(".pl-title", timeout=20000)
            page.wait_for_timeout(2000)
            title_online = page.locator(".pl-title").inner_text().strip()
            check("⑧b 线上行的标题仍是「剧名 · 第N集」（本地改动没串味）",
                  "一世之尊" in title_online and "第" in title_online and "本地播放" not in title_online,
                  title_online)
            check("⑧c 线上会话的选集侧栏照常在（只有本地会话才隐藏）",
                  page.locator(".pl-panel").count() == 1, "")

            # ── ⑨ en 态错误文案 ──
            # ⚠ 必须**重新灌种子**：⑦ 把那一行的 localPath 改成了越界路径，
            # 直接续着点会拿到 outside_root 而不是 local_missing，
            # 那句 `startswith("Local file is gone:")` 就永远等不来（第一版就在这超时）。
            # 用例之间共用页面状态时，改过数据的人要负责把它复原。
            # ⚠ 设完 locale **必须整页重载**：只改 hash 不换文档，
            # 而语言是模块加载时从 localStorage 读进内存的——
            # 本任务第四次栽在同一条（21 轮 init script、29 轮 mock 注入、34 轮 reserve、本轮）。
            # 上一次这里只 goto 到 discover，结果 ⑨ 拿到中文，看着像"英文没接线"。
            page.evaluate("() => localStorage.setItem('zgj.locale', 'en')")
            page.goto(BASE + "/#/discover")
            page.reload()
            page.wait_for_selector(".page-title", timeout=20000)
            seed_history(page)
            goto_history(page)
            page.evaluate("""(p) => { window.__zgjLocalMissing = [p]; window.__zgjLocalOutside = []; }""", LOCAL)
            page.locator(".hrow-main").first.click()
            page.wait_for_selector(".pl-hint", timeout=20000)
            page.wait_for_timeout(1200)
            hint_en = page.locator(".pl-hint").first.inner_text().strip()
            check("⑨ en 态错误是英文且带文件名（Go 那句中文只进日志，界面按 code 取 i18n）",
                  hint_en.startswith("Local file is gone:") and "S01" in hint_en, hint_en)

            page.screenshot(path=os.path.join(OUT, "local-play-entry.png"))
            browser.close()
    finally:
        _devserver.stop_dev(proc, PORT, name="local-play")

    print(f"\n===== history_local_play_verify: {'ALL PASS' if not failures else 'FAILURES: ' + ', '.join(failures)} =====")
    print(f"screenshot -> {os.path.join(OUT, 'local-play-entry.png')}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
