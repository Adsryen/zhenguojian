#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""layout_cover_verify —— 真机 bug①/② 的 mock 端到端验证（Playwright + VITE_MOCK dev server）。

覆盖断言：
  A. 搜索页（query=世，首条为全名命中长标题）第 1/2 卡 rect 宽高差 ≤2px，且注入超长标题/
     热度文本后仍 ≤2px（回归「首卡变大」机制：行溢出×flex-shrink×min-content）。
  B. 找剧页第 1/2 卡 rect 宽高差 ≤2px。
  C. 注入 window.__zgjCoverFault（文案含「未找到 ffmpeg」）→ 封面失败累计 ≥5 后警示条出现，
     文案含 runtime/ffmpeg 指引；点关闭 → 消失且再触发失败不重弹。
  D. 注入 generic 错误（不含 ffmpeg/未找到）→ 警示条显「部分封面加载失败（F12 查看原因）」。

方法说明：mock 分支 fixture 封面是本地 svg、api.cover(mock) 不 reject，故走 covers.ts 的
验证钩子 window.__zgjCoverFault（同 api.ts __zgjMockEmbed 先例）：置 string 后每次
resolveCover 在 try 内抛错 → 与真实桥完全相同的 classify/report/banner 路径。

用法：python3 verification/layout_cover_verify.py
（自动复用已在跑的 5173 dev server；否则自行 `npm run dev:mock` 并在结束时关闭。）
"""
import asyncio
import os
import socket
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
FRONTEND = os.path.join(ROOT, "client-v2", "frontend")
BASE = "http://127.0.0.1:5173"
EXEC = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
)

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


async def card_metrics(page):
    """返回前 12 张真实卡（跳过 skeleton）的 {w,h,coverH,x,y} rect 列表。"""
    return await page.evaluate(
        """() => [...document.querySelectorAll('.pg-viewport .poster')].slice(0, 12)
           .map(p => {
             const r = p.getBoundingClientRect();
             const c = p.querySelector('.cover').getBoundingClientRect();
             return { w: r.width, h: r.height, coverH: c.height, coverW: c.width, x: r.x, y: r.y };
           })"""
    )


async def diff_le2(a, b, name):
    dw = abs(a["w"] - b["w"])
    dh = abs(a["h"] - b["h"])
    return check(name, dw <= 2 and dh <= 2, f"dW={dw:.2f}px dH={dh:.2f}px (card1 {a['w']:.2f}x{a['h']:.2f}, card2 {b['w']:.2f}x{b['h']:2f})")


async def aspect_ok(m, name):
    ratio = m["coverH"] / m["coverW"]
    return check(name, abs(ratio - 1.4) < 0.02, f"cover {m['coverW']:.2f}x{m['coverH']:.2f} ratio={ratio:.4f} (want 1:1.4)")


async def wait_posters(page):
    await page.wait_for_selector(".pg-viewport .poster .cover", timeout=15000)
    await page.wait_for_timeout(800)  # 等封面 data/占位落定


async def main():
    from playwright.async_api import async_playwright

    spawned = None
    if not port_open("127.0.0.1", 5173):
        spawned = subprocess.Popen(
            ["npm", "run", "dev:mock"],
            cwd=FRONTEND,
            stdout=open(os.path.join(HERE, ".dev_mock.log"), "w"),
            stderr=subprocess.STDOUT,
        )
        for _ in range(60):
            if port_open("127.0.0.1", 5173):
                break
            time.sleep(0.5)
        else:
            print("dev server 未能启动，见 verification/.dev_mock.log")
            sys.exit(2)

    async with async_playwright() as pw:
        browser = await pw.chromium.launch(executable_path=EXEC, args=["--no-sandbox"])

        # ---------- A. 搜索页布局 ----------
        page = await browser.new_page(viewport={"width": 1280, "height": 800})
        await page.goto(f"{BASE}/#/search")
        await page.fill("input[placeholder*='搜索剧名']", "世")
        await page.keyboard.press("Enter")
        await wait_posters(page)
        cards = await card_metrics(page)
        check("A0 搜索页海报墙渲染", len(cards) >= 2, f"{len(cards)} cards")
        ok1 = await diff_le2(cards[0], cards[1], "A1 搜索页第1/2卡宽高差≤2px")
        widths = [round(c["w"], 1) for c in cards]
        check("A1b 搜索页前12卡全一致", max(widths) - min(widths) <= 2, f"widths={widths}")
        await aspect_ok(cards[0], "A1c 搜索页封面区 aspect 1:1.4")
        # 长标题注入：复现真实数据「首条全名命中」形态，修复后不得拉大
        await page.evaluate(
            """() => {
              const ps = document.querySelectorAll('.pg-viewport .poster');
              ps[0].querySelector('.title').textContent = '重生之我在短剧当编剧之超长标题回归演示一二三四五';
              ps[0].querySelector('.sub').textContent = ' · 热度9999万·超长副标题回归演示文字';
            }"""
        )
        await page.wait_for_timeout(300)
        cards2 = await card_metrics(page)
        ok2 = await diff_le2(cards2[0], cards2[1], "A2 注入超长标题后第1/2卡宽高差≤2px")
        await page.screenshot(path=os.path.join(HERE, "layout-search.png"))
        okA = ok1 and ok2

        # ---------- B. 找剧页布局 ----------
        page2 = await browser.new_page(viewport={"width": 1280, "height": 800})
        await page2.goto(f"{BASE}/#/discover")
        await wait_posters(page2)
        dcards = await card_metrics(page2)
        check("B0 找剧页海报墙渲染", len(dcards) >= 2, f"{len(dcards)} cards")
        await diff_le2(dcards[0], dcards[1], "B1 找剧页第1/2卡宽高差≤2px")
        await aspect_ok(dcards[0], "B1b 找剧页封面区 aspect 1:1.4")
        await page2.screenshot(path=os.path.join(HERE, "layout-discover.png"))

        # ---------- C. missing-ffmpeg 警示条 ----------
        page3 = await browser.new_page(viewport={"width": 1280, "height": 800})
        await page3.add_init_script(
            "window.__zgjCoverFault = '未找到 ffmpeg：缺少 ffmpeg 组件，无法显示海报'"
        )
        await page3.goto(f"{BASE}/#/discover")
        try:
            await page3.wait_for_selector("[data-testid='cover-banner']", timeout=15000)
        except Exception:
            check("C1 ffmpeg 缺失警示条出现", False, "banner 未出现")
        else:
            banner_text = (await page3.inner_text("[data-testid='cover-banner']")).strip()
            check(
                "C1 ffmpeg 缺失警示条出现且文案含指引",
                "zhenGuoJian-v2-runtime.zip" in banner_text
                and "ffmpeg.exe" in banner_text
                and "ZHENGUO_FFMPEG" in banner_text,
                banner_text,
            )
            await page3.screenshot(path=os.path.join(HERE, "cover-banner.png"))
            await page3.click("[data-testid='cover-banner-close']")
            await page3.wait_for_selector("[data-testid='cover-banner']", state="detached", timeout=5000)
            check("C2 点击关闭后警示条消失", True)
            # 关闭后继续滚动制造新失败 → 不应重弹
            await page3.mouse.wheel(0, 1200)
            await page3.wait_for_timeout(1500)
            gone = await page3.evaluate(
                "!document.querySelector(\"[data-testid='cover-banner']\")"
            )
            check("C3 关闭后再触发失败不重弹（本页会话）", bool(gone))

        # ---------- D. generic error 警示条 ----------
        page4 = await browser.new_page(viewport={"width": 1280, "height": 800})
        await page4.add_init_script("window.__zgjCoverFault = '海报转码失败: exit status 1'")
        await page4.goto(f"{BASE}/#/discover")
        try:
            await page4.wait_for_selector("[data-testid='cover-banner']", timeout=15000)
            t4 = (await page4.inner_text("[data-testid='cover-banner']")).strip()
            check("D1 通用错误警示条文案", "部分封面加载失败" in t4 and "F12" in t4, t4)
        except Exception:
            check("D1 通用错误警示条文案", False, "banner 未出现")

        # console.warn 可见性断言（「看不到报错」的反面）：另开一路收集 console
        page5 = await browser.new_page(viewport={"width": 1280, "height": 800})
        warns = []
        page5.on("console", lambda m: warns.append(m.text) if m.type == "warning" else None)
        await page5.add_init_script(
            "window.__zgjCoverFault = '未找到 ffmpeg：缺少 ffmpeg 组件，无法显示海报'"
        )
        await page5.goto(f"{BASE}/#/discover")
        await page5.wait_for_timeout(4000)
        covers_warns = [w for w in warns if "[covers]" in w]
        check(
            "E1 console.warn '[covers]' missing-ffmpeg 分类可见",
            len(covers_warns) >= 5 and all("missing-ffmpeg" in w for w in covers_warns),
            f"{len(covers_warns)} warns, sample: {covers_warns[0][:96] if covers_warns else '无'}",
        )

        await browser.close()

    if spawned:
        spawned.terminate()
        try:
            spawned.wait(timeout=10)
        except subprocess.TimeoutExpired:
            spawned.kill()

    failed = [r for r in RESULTS if r[0] == "FAIL"]
    print(f"\n== {len(RESULTS) - len(failed)}/{len(RESULTS)} PASS ==")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    asyncio.run(main())
