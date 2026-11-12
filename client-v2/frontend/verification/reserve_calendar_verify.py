#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""reserve_calendar_verify —— 追剧/日历两页 v1 对齐补齐（J 轮）的 mock 端到端验证。

覆盖断言：
  ① follow 行内按钮：有历史进度的行出现「继续播放」且点击 → #/player?ep=12&pos=245
     （与 HistoryView 续播同路径）；无进度行不出现该按钮；「标记已看」点击后
     zgj.watched 落键 + 按钮翻「已看 ✓」，再点撤销（toggleSeen 语义）。
  ② 站源 chips 过滤：全部/红果短剧/黄果AI剧（displayName 来自 app store mock 源表），
     点「黄果AI剧」只剩该源行，点「全部」还原；不发网络请求（纯前端派生，
     以 mock 侧无额外调用 + DOM 即时变化佐证）。
  ③ 日历性别 chip：[data-gender] 初始 2（全部）→ 点「男频」=1 → 点「女频」=0，
     chip active 类跟随；store.gender 经 api.calendar 第三形参透传 Go Calendar
     （bindings_calendar.go:43）。mock 面 mockApi.calendar 忽略 gender（fixtures 无
     频段切分），故以 DOM data-gender（store state 镜像）断言，真实桥下即请求参数。
  ④ /reserve 与 /reserve?tab=calendar → router.replace → 落 #/calendar（旧占位合并）。

方法：VITE_MOCK dev server（**独占端口 5186**，启停走 `_devserver`：
     进门先断言端口为空，**绝不复用别人的服务器**——旧版「端口被占则复用」会让
     5182 上的 mock 孤儿冒充 detail_download_verify 的非 mock 服务器，制造 ② 段假红灯。
localStorage 种子经 add_init_script 注入（zgj.follows / zgj.history），
使 follow 列表非空且首行有进度（mock watchlist = localStorage 降级面）。

用法：python3 verification/reserve_calendar_verify.py
产物：verification/reserve-follow.png、verification/calendar-gender.png
"""
import asyncio
import json
import os
import socket
import subprocess

import _devserver
import sys
import time
from urllib.parse import urlparse

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)  # client-v2/frontend（本项目验证目录约定：frontend/verification/）
PORT = 5186
BASE = f"http://127.0.0.1:{PORT}"
EXEC = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
)

SEED_JS = """
(() => {
  if (localStorage.getItem('zgj.verify-seed')) return;
  const now = Date.now();
  const follows = {
    'hongguo:7439220582928952383': {
      drama: { id: 'hongguo:7439220582928952383', source: 'hongguo', title: '一世之尊',
               cover: '', episodes: 66, description: '种子剧目A（红果源）' },
      ts: now - 3600e3
    },
    'huangguo_ai:7512000000000000001': {
      drama: { id: 'huangguo_ai:7512000000000000001', source: 'huangguo_ai', title: '星河远征（AI）',
               cover: '', episodes: 20, description: '种子剧目B（黄果AI源）' },
      ts: now - 7200e3
    }
  };
  localStorage.setItem('zgj.follows', JSON.stringify(follows));
  const history = {
    'hongguo:7439220582928952383:12': {
      title: '一世之尊', episode: 12, position: 245, duration: 1320,
      ts: now - 600e3, dramaId: 'hongguo:7439220582928952383', cover: ''
    }
  };
  localStorage.setItem('zgj.history', JSON.stringify(history));
  localStorage.setItem('zgj.verify-seed', '1');
})()
"""

RESULTS = []


def check(name, ok, detail=""):
    tag = "PASS" if ok else "FAIL"
    RESULTS.append((tag, name, detail))
    print(f"[{tag}] {name}" + (f"  — {detail}" if detail else ""))
    return ok


def port_open(host, port):
    with socket.socket() as s:
        s.settimeout(0.5)
        return s.connect_ex((host, port)) == 0


def row_for(page, title):
    return page.locator(".cal-row").filter(has=page.locator(".cal-title", has_text=title)).first


async def main():
    from playwright.async_api import async_playwright

    # 端口预检 + 严格收口见 _devserver.py（旧写法「端口被占则复用」是 R8 假红灯的来源）
    owned = _devserver.start_dev(FRONTEND, PORT, True, HERE, name="reserve-calendar")

    try:
        async with async_playwright() as pw:
            browser = await pw.chromium.launch(
                executable_path=EXEC if os.path.exists(EXEC) else None, headless=True
            )
            ctx = await browser.new_context(viewport={"width": 1280, "height": 860})
            await ctx.add_init_script(SEED_JS)
            page = await ctx.new_page()

            # ---------- ④（先测 redirect，避免污染后续）：/reserve 裸路径 + tab=calendar → /calendar ----------
            await page.goto(f"{BASE}/#/reserve", wait_until="load")
            try:
                await page.wait_for_function("location.hash.startsWith('#/calendar')", timeout=8000)
                check("④a /reserve 裸路径 redirect 到 /calendar", True, await page.evaluate("location.hash"))
            except Exception:
                check("④a /reserve 裸路径 redirect 到 /calendar", False, await page.evaluate("location.hash"))
            await page.goto(f"{BASE}/#/reserve?tab=calendar", wait_until="load")
            try:
                await page.wait_for_function("location.hash.startsWith('#/calendar')", timeout=8000)
                check("④b /reserve?tab=calendar redirect 到 /calendar", True, await page.evaluate("location.hash"))
            except Exception:
                check("④b /reserve?tab=calendar redirect 到 /calendar", False, await page.evaluate("location.hash"))

            # ---------- ③ 日历性别 chips（真实桥 = api.calendar 第三形参） ----------
            await page.goto(f"{BASE}/#/calendar", wait_until="load")
            gender_bar = page.locator(".gender-chips")
            await gender_bar.wait_for(state="visible", timeout=8000)
            g0 = await gender_bar.get_attribute("data-gender")
            labels = await gender_bar.locator(".chip").all_inner_texts()
            check("③a 性别 chip 行存在，默认=2(全部)，标签=全部/男频/女频",
                  g0 == "2" and labels == ["全部", "男频", "女频"], f"data-gender={g0} labels={labels}")
            await gender_bar.locator(".chip", has_text="男频").click()
            await page.wait_for_timeout(400)
            g1 = await gender_bar.get_attribute("data-gender")
            male_active = "active" in (await gender_bar.locator(".chip", has_text="男频").get_attribute("class") or "")
            check("③b 点「男频」→ store.gender=1（api.calendar 第三形参透传，mock 面数据不变属预期）",
                  g1 == "1" and male_active, f"data-gender={g1} active={male_active}")
            await gender_bar.locator(".chip", has_text="女频").click()
            await page.wait_for_timeout(400)
            g2 = await gender_bar.get_attribute("data-gender")
            check("③c 点「女频」→ store.gender=0", g2 == "0", f"data-gender={g2}")
            await page.screenshot(path=os.path.join(HERE, "calendar-gender.png"))

            # ---------- ① follow 行内按钮 ----------
            await page.goto(f"{BASE}/#/reserve?tab=follow", wait_until="load")
            await page.locator(".cal-row").first.wait_for(state="visible", timeout=8000)
            rows = page.locator(".cal-row")
            n = await rows.count()
            check("①a follow 列表渲染（2 种子行）", n == 2, f"rows={n}")

            row_a = row_for(page, "一世之尊")   # 有历史进度
            row_b = row_for(page, "星河远征")   # 无进度
            btn_resume_a = row_a.locator("button", has_text="继续播放")
            btn_resume_b = row_b.locator("button", has_text="继续播放")
            has_a = await btn_resume_a.count() == 1
            has_b = await btn_resume_b.count() == 1
            chip_a = "看到第 12集" in (await row_a.locator(".cal-title-line").inner_text())
            check("①b 有进度行出现「继续播放」+进度角标，无进度行不出现",
                  has_a and not has_b and chip_a, f"有进度行={has_a} 无进度行显={has_b} 角标={chip_a}")

            # 标记已看：点 → 已看 ✓ + localStorage zgj.watched；再点 → 撤销
            btn_seen = row_a.locator("button", has_text="标记已看")
            await btn_seen.click()
            await row_a.locator("button", has_text="已看 ✓").wait_for(state="visible", timeout=4000)
            seen_map = json.loads(await page.evaluate("localStorage.getItem('zgj.watched')||'{}'"))
            marked = "hongguo:7439220582928952383" in seen_map
            check("①c 「标记已看」→ 按钮翻「已看 ✓」且 zgj.watched 落键（toggleSeen 真值）",
                  marked, f"watched={list(seen_map.keys())}")
            await row_a.locator("button", has_text="已看 ✓").click()
            await row_a.locator("button", has_text="标记已看").wait_for(state="visible", timeout=4000)
            seen_map2 = json.loads(await page.evaluate("localStorage.getItem('zgj.watched')||'{}'"))
            check("①d 再点「已看 ✓」可撤销标记", "hongguo:7439220582928952383" not in seen_map2)
            await row_a.locator("button", has_text="标记已看").click()  # 复标，留截图态

            # 继续播放 → /player?ep=12&pos=245
            await btn_resume_a.click()
            await page.wait_for_function("location.hash.startsWith('#/player')", timeout=8000)
            hash_now = await page.evaluate("location.hash")
            q = urlparse(hash_now.replace("#", "", 1)).query
            params = dict(p.split("=", 1) for p in q.split("&") if "=" in p)
            check("①e 「继续播放」→ #/player 且 ep=12&pos=245（HistoryView 同路径）",
                  params.get("ep") == "12" and params.get("pos") == "245" and "d" in params,
                  f"ep={params.get('ep')} pos={params.get('pos')}")

            # ---------- ② 站源 chips ----------
            await page.goto(f"{BASE}/#/reserve?tab=follow", wait_until="load")
            await page.locator(".cal-row").first.wait_for(state="visible", timeout=8000)
            chips = page.locator("[data-testid=source-chips]")
            await chips.wait_for(state="visible", timeout=8000)
            chip_labels = await chips.locator(".chip").all_inner_texts()
            check("②a 站源 chips = 全部 + displayName（红果短剧/黄果AI剧）",
                  chip_labels == ["全部", "红果短剧", "黄果AI剧"], f"labels={chip_labels}")
            await chips.locator(".chip", has_text="黄果AI剧").click()
            await page.wait_for_timeout(200)
            n_after = await page.locator(".cal-row").count()
            titles = await page.locator(".cal-row .cal-title").all_inner_texts()
            check("②b 点「黄果AI剧」本地过滤 → 只剩该源行（无网络请求，纯派生）",
                  n_after == 1 and titles == ["星河远征（AI）"], f"rows={n_after} titles={titles}")
            await chips.locator(".chip", has_text="全部").click()
            await page.wait_for_timeout(200)
            n_all = await page.locator(".cal-row").count()
            check("②c 点「全部」还原两行", n_all == 2, f"rows={n_all}")

            await page.screenshot(path=os.path.join(HERE, "reserve-follow.png"))
            await browser.close()
    finally:
        _devserver.stop_dev(owned, PORT, name="reserve-calendar")

    fails = [r for r in RESULTS if r[0] == "FAIL"]
    print(f"\n== {len(RESULTS) - len(fails)}/{len(RESULTS)} PASS ==")
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
