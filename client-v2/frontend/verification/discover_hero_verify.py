#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""discover_hero_verify —— 发现页（找剧）mock 端到端验收（hero 大卡已按用户反馈移除）。

覆盖语义：
  - 无 hero：墙面从 items[0] 直接开始（v1 _WallHero 对应物已下线，用户嫌占位过大）
  - 下拉刷新：browse_panel.py:151（wall.set_refresh_enabled + refreshRequested → panel._refresh）
  - 集数徽标：poster_wall.py:52-69 _badge_text（集数优先）+ browse_panel.py:149 双徽标（VIP + 集数）

断言：
  A1 hero 节点不存在 + items[0] 直接进网格 + 墙顶贴齐（无 hero 占位）
  A2 首卡点击 → 详情路由
  A3 顶部下拉超阈值 → 触发既有强刷新（window.__zgjDiscover.refreshes 计数 +1）+ 指示文案切换
  A4 网格卡片集数徽标与 fixture 一致（期望值在页面内 import 同一份 fixtures 重算，非同段代码复制）
  A5 注入空数据 / 单条数据不崩（无 pageerror、状态正确）

用法：python3 verification/discover_hero_verify.py
（自管理 dev:mock：**独占端口 5191**，启停走 `_devserver`——旧写法「已在监听则复用」
     会让别的脚本的 vite 冒充本脚本的服务器，制造与 R8 同类的假结果。）
"""
import asyncio
import os
import socket
import subprocess
import sys
import time

from playwright.async_api import async_playwright

import _devserver

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
FRONTEND = ROOT  # 脚本已从仓库根 verification/ 迁入 frontend/verification/
PORT = 5191
BASE = f"http://127.0.0.1:{PORT}"
SHOT = os.path.join(HERE, "discover-hero.png")
SHOT_PULL = os.path.join(HERE, "discover-hero-pull.png")
SHOT_EMPTY = os.path.join(HERE, "discover-hero-empty.png")
EXEC = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
)
LOG = "/tmp/zgj_k_verify_vite.log"

RESULTS = []


def check(name, ok, detail=""):
    tag = "PASS" if ok else "FAIL"
    RESULTS.append((tag, name, detail))
    print(f"[{tag}] {name}" + (f"  — {detail}" if detail else ""), flush=True)
    return ok


def port_open(port):
    with socket.socket() as s:
        s.settimeout(0.5)
        return s.connect_ex(("127.0.0.1", port)) == 0


def start_server():
    """dev:mock 自管理：端口预检 + 就绪等待见 _devserver.py（不再复用他人实例）。"""
    return _devserver.start_dev(FRONTEND, PORT, True, HERE, name="discover-hero")
    raise RuntimeError(f"dev:mock 起不来，日志见 {LOG}")


# 页面内同源期望：import vite 正在服务的同一份 fixtures，用 mockCatalog 造数后按徽标口径重算。
# 与组件里 badgeText 同规则但独立实现（规则来源 v1 poster_wall._badge_text：集数优先）。
FIXTURE_JS = r"""
async () => {
  const m = await import('/src/services/fixtures.ts')
  const list = m.mockCatalog('hongguo', '', 1).items
  const ONGOING = /(更新至|更新中|连载|在更|未完结)/
  const badge = (d) => {
    const s = String(d.releaseStatus ?? '').trim()
    const eps = Number(d.episodes ?? 0)
    if (/\d+\s*集/.test(s)) return s                       // 自带集数：原样（信息量最大）
    if (eps > 0) return (ONGOING.test(s) || s === 'ongoing') ? '更新至' + eps + '集' : '共' + eps + '集'
    if (s === 'ongoing') return '更新中'
    if (s === 'finished') return '已完结'
    return s
  }
  return list.map((d) => ({
    id: d.id,
    title: d.title,
    status: String(d.releaseStatus ?? ''),
    eps: Number(d.episodes ?? 0),
    badge: badge(d),
  }))
}
"""

# 当前 DOM 里的网格卡片：标题 + 右下集数徽标 + 左上 VIP/18+
CARDS_JS = r"""
() => [...document.querySelectorAll('.pg-viewport .poster')].map((el) => ({
  title: (el.querySelector('.title')?.textContent || '').trim(),
  badge: (el.querySelector('.badge-epi')?.textContent || '').trim(),
  vip: !!el.querySelector('.badge-vip'),
  adult: !!el.querySelector('.badge-adult'),
}))
"""


async def goto_discover(page):
    await page.goto(f"{BASE}/#/discover", wait_until="load")
    await page.wait_for_timeout(300)
    await page.wait_for_selector(".pg-viewport .poster, .pg-empty, .state-block", timeout=15000)
    await page.wait_for_timeout(700)


async def force_refresh(page):
    """点头部既有「刷新」按钮（loadFirst(true)），非 mock 钩子路径。"""
    await page.locator("button.btn:has-text('刷新')").first.click()
    await page.wait_for_timeout(1000)


async def main():
    proc = start_server()
    errors = []
    try:
        async with async_playwright() as pw:
            browser = await pw.chromium.launch(executable_path=EXEC, args=["--no-sandbox"])
            page = await browser.new_page(viewport={"width": 1440, "height": 900})
            page.on("pageerror", lambda e: errors.append(f"pageerror: {e}"))
            page.on(
                "console",
                lambda m: m.type == "error" and errors.append(f"console: {(m.text or '')[:120]}"),
            )

            await goto_discover(page)
            expected = await page.evaluate(FIXTURE_JS)
            first_title = expected[0]["title"] if expected else ""
            exp_by_title = {}
            for d in expected:
                exp_by_title.setdefault(d["title"], d["badge"])

            # ---------------- A1 无 hero + 全量进网格 ----------------
            hero = page.locator('[data-testid="poster-hero"]')
            hero_nodes = await hero.count()
            cards = await page.evaluate(CARDS_JS)
            grid_titles = [c["title"] for c in cards]
            check("A1 hero 大卡已移除（页面无 hero 节点）", hero_nodes == 0, f"hero_nodes={hero_nodes}")
            check("A1 items[0] 直接进网格（不再上墙剔除）", first_title != "" and grid_titles and grid_titles[0] == first_title, f"grid[0]={grid_titles[:1]} items[0]={first_title!r}")
            check("A1 网格非空", len(cards) > 0, f"cards={len(cards)}")
            vptop = await page.evaluate(
                "() => { const vp = document.querySelector('.pg-viewport');"
                "  return vp ? vp.offsetTop : -1; }")
            check("A1 墙顶贴齐（contentTop 无 hero 占位，≤8px）", 0 <= vptop <= 8, f"offsetTop={vptop}")

            # ---------------- A4 集数徽标 ----------------
            mismatch = [
                f"{c['title']}: got {c['badge']!r} want {exp_by_title.get(c['title'])!r}"
                for c in cards
                if c["badge"] != exp_by_title.get(c["title"], None)
            ]
            no_badge = [c["title"] for c in cards if not c["badge"]]
            bases = list(dict.fromkeys([d["title"] for d in expected]))[:4]
            check(
                "A4 每张网格卡的集数徽标与 fixture 一致",
                len(cards) > 0 and not mismatch and not no_badge,
                f"mismatch={mismatch[:3]} 无徽标={no_badge[:3]}",
            )
            check(
                "A4 集数口径：releaseStatus 自带「N集」时原样保留",
                exp_by_title.get("山河引") == "更新至40集",
                f"山河引 → {exp_by_title.get('山河引')!r}",
            )
            check(
                "A4 集数口径：ongoing+episodes → 更新至N集 / finished+episodes → 共N集",
                exp_by_title.get("一世之尊") == "更新至68集" and exp_by_title.get("闪婚老公是大佬") == "共82集",
                f"一世之尊 → {exp_by_title.get('一世之尊')!r}, 闪婚老公是大佬 → {exp_by_title.get('闪婚老公是大佬')!r}",
            )
            check(
                "A4 双徽标并存（VIP 左上 + 集数右下）",
                any(c["vip"] and c["badge"] for c in cards),
                f"样例={[(c['title'], c['vip'], c['badge']) for c in cards[:2]]}",
            )

            # ---------------- A3 下拉刷新手势 ----------------
            base_cnt = await page.evaluate("() => (window.__zgjDiscover || {}).refreshes || 0")
            await page.evaluate("() => document.querySelector('.pg-scroller').scrollTo(0, 0)")
            await page.wait_for_timeout(250)
            sbox = await page.locator(".pg-scroller").bounding_box()
            x = sbox["x"] + sbox["width"] * 0.5
            y0 = sbox["y"] + 150
            await page.mouse.move(x, y0)
            await page.mouse.down()
            seen = set()
            for step in range(1, 12):
                await page.mouse.move(x, y0 + step * 24)  # 裸位移 264px，阻尼 0.5 → 132 > 阈值 80
                await page.wait_for_timeout(40)
                seen.add((await page.locator('[data-testid="ptr-indicator"]').first.inner_text()).strip())
            pulled = await page.evaluate(
                "() => { const t = getComputedStyle(document.querySelector('.pg-content')).transform;"
                "  return t === 'none' ? 0 : Math.round(new DOMMatrixReadOnly(t).f); }"
            )
            await page.screenshot(path=SHOT_PULL)
            await page.mouse.up()
            await page.wait_for_timeout(150)
            after = await page.evaluate("() => (window.__zgjDiscover || {}).refreshes || 0")
            await page.wait_for_function(
                "() => { const t = getComputedStyle(document.querySelector('.pg-content')).transform;"
                "  return t === 'none' || new DOMMatrixReadOnly(t).f < 1 }",
                timeout=8000,
            )
            check(
                "A3 顶部下拉超阈值 → 触发既有强刷新 loadFirst(true)",
                after == base_cnt + 1 and pulled >= 60,
                f"refreshes {base_cnt} → {after}，跟手位移≈{pulled}px（阻尼 0.5 × 裸位移 264px，封顶 130）",
            )
            check(
                "A3 指示文案随进度切换（下拉刷新 → 松开刷新）",
                any("下拉刷新" in s for s in seen) and any("松开刷新" in s for s in seen),
                f"{sorted(seen)}",
            )
            await page.wait_for_timeout(900)
            cards_after = await page.evaluate(CARDS_JS)
            check(
                "A3 刷新后回弹并原位复现网格（未闪成空墙）",
                len(cards_after) > 0 and cards_after[0]["title"] == first_title,
                f"cards={len(cards_after)} first={cards_after[:1] and cards_after[0]['title']!r}",
            )

            # ---------------- A5 空 / 单条 ----------------
            await page.evaluate("() => { window.__zgjDiscoverInjectCatalog = 'empty' }")
            await force_refresh(page)
            empty_nodes = await page.locator(".pg-empty").count()
            await page.screenshot(path=SHOT_EMPTY)
            check(
                "A5 空数据不崩（空态在位、无 pageerror）",
                empty_nodes == 1 and not errors,
                f"empty={empty_nodes} errors={len(errors)}",
            )

            await page.evaluate("() => { window.__zgjDiscoverInjectCatalog = 'single' }")
            await force_refresh(page)
            single = await page.evaluate(CARDS_JS)
            check(
                "A5 单条数据不崩（1 张普通卡、徽标照常）",
                len(single) == 1 and bool(single[0]["badge"]),
                f"cards={len(single)} 例={single[:1]}",
            )

            await page.evaluate("() => { delete window.__zgjDiscoverInjectCatalog }")
            await force_refresh(page)

            # ---------------- A2 首卡点击进详情 ----------------
            await page.screenshot(path=SHOT)
            await page.locator(".pg-viewport .poster").first.click()
            await page.wait_for_timeout(900)
            url = page.url
            check("A2 首卡点击 → 详情路由", "detail" in url, url)

            await browser.close()
    finally:
        _devserver.stop_dev(proc, PORT, name="discover-hero")

    check("无 JS 运行时错误", not errors, "; ".join(errors[:3]))
    print(f"\n截图：{SHOT}\n      {SHOT_PULL}（下拉中）\n      {SHOT_EMPTY}（空数据）")
    failed = [n for tag, n, _ in RESULTS if tag == "FAIL"]
    print(f"合计 {len(RESULTS)} 项，失败 {len(failed)} 项" + (f"：{failed}" if failed else ""))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
