#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""downloads_archive_scan_verify —— 归档目录扫描（交付 C：App.ArchiveScan 分组视图）mock 端到端验证。

覆盖断言（任务书交付 C 验证面）：
  ① 归档 tab 点开后渲染 3 个分组卡（一世之尊/闪婚老公是大佬/凡人修仙传，mtime 倒序）。
  ② 每组「N 集」计数（2 集/1 集/30 集）+ 大小（1.5 GB / 384.0 MB / 16.0 GB）
     + 一世之尊带「合并成品 3.0 GB」副量 + mtime 文案「更新于」。
  ③ 徽标：一世之尊=含合并+已归档；闪婚=已归档；凡人=未在索引（孤儿组），无含合并。
  ④ 点开调用：row-open → window.__zgjOpenPath 落
     `<directory>/合并/一世之尊 全001-002集.mkv`（合并组）与 `<directory>/fanren/mock0030`（分集组）。
  ⑤ 页签统计数 archive-count=扫描分组数 3。
  ⑥ dir-line 自证「归档扫描目录：D:\\ZhenguoJian\\downloads」+ 复制按钮点击出 toast。
  ⑦ 空态：localStorage 'zgj.mock.archiveMissing'='1' → 目录不存在态含真实路径 + 复制按钮；
     清旗标点「重新扫描」→ 分组恢复渲染。

方法：VITE_MOCK dev server（5190，--strictPort，自管理：端口占用则复用，结束 kill 自启进程）。
截图：verification/downloads-archive-scan.png
用法：python3 verification/downloads_archive_scan_verify.py
"""
import asyncio
import os
import socket
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = 5190
BASE = f"http://127.0.0.1:{PORT}"
EXEC = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
)
MOCK_DIR = "D:\\ZhenguoJian\\downloads"

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


READ_JS = """
() => {
  const rows = [...document.querySelectorAll('[data-testid=archive-groups] .row')];
  return {
    dirLine: (document.querySelector('.dir-line') || {}).textContent || '',
    tabCount: (document.querySelector('[data-testid=archive-count]') || {}).textContent || '',
    rowCount: rows.length,
    rows: rows.map((r) => ({
      title: (r.querySelector('.r-title') || { textContent: '' }).textContent.trim(),
      cover: (r.querySelector('.g-avatar') || { textContent: '' }).textContent.trim(),
      eps: (r.querySelector('[data-testid=archive-eps]') || { textContent: '' }).textContent.trim(),
      meta: (r.querySelector('[data-testid=archive-meta]') || { textContent: '' }).textContent.trim(),
      merged: (r.querySelector('[data-testid=merged-badge]') || {}).textContent || '',
      archived: (r.querySelector('[data-testid=archived-badge]') || {}).textContent || '',
      orphan: (r.querySelector('[data-testid=orphan-badge]') || {}).textContent || '',
    })),
    missing: !!document.querySelector('[data-testid=archive-missing]'),
    missingText: (document.querySelector('[data-testid=archive-missing]') || {}).textContent || '',
    empty: !!document.querySelector('[data-testid=archive-empty]'),
    scanning: !!document.querySelector('[data-testid=archive-scanning]'),
    copyBtn: !!document.querySelector('[data-testid=copy-dir]'),
    rescanBtn: !!document.querySelector('[data-testid=rescan]'),
    toast: (document.querySelector('.toast') || {}).textContent || '',
  };
}
"""


async def click_archive_tab(page):
    await page.click("[data-testid=tab-archive]")


async def main():
    from playwright.async_api import async_playwright

    owned = None
    if not port_open("127.0.0.1", PORT):
        owned = subprocess.Popen(
            ["npm", "run", "dev:mock", "--", "--port", str(PORT), "--strictPort"],
            cwd=FRONTEND,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.STDOUT,
        )
        for _ in range(60):
            if port_open("127.0.0.1", PORT):
                break
            time.sleep(0.5)
    if not port_open("127.0.0.1", PORT):
        print("dev:mock 未能在 5190 启动")
        return 2

    try:
        async with async_playwright() as pw:
            browser = await pw.chromium.launch(
                executable_path=EXEC if os.path.exists(EXEC) else None, headless=True
            )
            ctx = await browser.new_context(viewport={"width": 1280, "height": 860})
            try:
                await ctx.grant_permissions(["clipboard-read", "clipboard-write"], origin=BASE)
            except Exception:
                pass  # 无剪贴板权限则 ⑥ 走 copyFail toast 分支
            page = await ctx.new_page()

            await page.goto(f"{BASE}/#/downloads", wait_until="load")
            await page.locator("[data-testid=tab-archive]").wait_for(state="visible", timeout=10000)

            # ---------- ① 分组渲染（3 组，mtime 倒序） ----------
            await click_archive_tab(page)
            await page.locator("[data-testid=archive-groups]").wait_for(state="visible", timeout=10000)
            d = await page.evaluate(READ_JS)
            titles = [r["title"] for r in d["rows"]]
            check(
                "① 归档 tab 渲染 3 个分组卡（一世之尊/闪婚老公是大佬/凡人修仙传，mtime 倒序）",
                titles == ["一世之尊", "闪婚老公是大佬", "凡人修仙传"],
                f"titles={titles}",
            )

            # ---------- ② N集 / 大小 / 合并副量 / mtime ----------
            eps = [r["eps"] for r in d["rows"]]
            metas = [r["meta"] for r in d["rows"]]
            check(
                "②a 每组「N 集」计数=扫描 episodes（2/1/30）",
                eps == ["2 集", "1 集", "30 集"],
                f"eps={eps}",
            )
            check(
                "②b 大小格式化（1.5 GB / 384.0 MB / 16.0 GB）+ 一世之尊「合并成品 3.0 GB」+ 更新于 mtime",
                "1.5 GB" in metas[0] and "合并成品 3.0 GB" in metas[0] and "更新于" in metas[0]
                and "384.0 MB" in metas[1] and "更新于" in metas[1]
                and "16.0 GB" in metas[2],
                f"metas={metas}",
            )
            covers = [r["cover"] for r in d["rows"]]
            check("②c 封面首字占位（名首字）", covers == ["一", "闪", "凡"], f"covers={covers}")

            # ---------- ③ 徽标 ----------
            r0, r1, r2 = d["rows"]
            check(
                "③ 徽标：一世之尊=含合并+已归档；闪婚=已归档无含合并；凡人=未在索引孤儿组",
                r0["merged"].strip() == "含合并" and "归档" in r0["archived"]
                and r1["merged"] == "" and "归档" in r1["archived"]
                and r2["merged"] == "" and r2["orphan"].strip() == "未在索引",
                f"badges={[(x['merged'], x['archived'], x['orphan']) for x in d['rows']]}",
            )

            # ---------- ④ 点开调用 openDownloadPath ----------
            await page.locator("[data-testid=archive-groups] .row").nth(0).locator("[data-testid=row-open]").click()
            await page.wait_for_function(
                f"() => window.__zgjOpenPath === {json_str(MOCK_DIR + '/合并/一世之尊 全001-002集.mkv')}",
                timeout=5000,
            )
            p0 = await page.evaluate("() => window.__zgjOpenPath")
            check("④a 合并组点开 → directory/合并/…全001-002集.mkv", True, f"path={p0}")
            await page.locator("[data-testid=archive-groups] .row").nth(2).locator("[data-testid=row-open]").click()
            await page.wait_for_function(
                f"() => window.__zgjOpenPath === {json_str(MOCK_DIR + '/fanren/mock0030')}",
                timeout=5000,
            )
            p2 = await page.evaluate("() => window.__zgjOpenPath")
            check("④b 分集组点开 → directory/fanren/mock0030", True, f"path={p2}")

            # ---------- ⑤ 页签统计数 = 分组数 ----------
            d = await page.evaluate(READ_JS)
            check(
                "⑤ 归档页签计数 archive-count = 扫描分组数 3",
                d["tabCount"].strip() == "3",
                f"tabCount={d['tabCount']}",
            )

            # ---------- ⑥ dir-line 自证 + 复制按钮 ----------
            check(
                "⑥a dir-line 显示「归档扫描目录：」+ mock 真实路径，含复制/重新扫描按钮",
                "归档扫描目录：" in d["dirLine"] and MOCK_DIR in d["dirLine"] and d["copyBtn"] and d["rescanBtn"],
                f"dirLine={d['dirLine'][:120]}",
            )
            await page.click("[data-testid=copy-dir]")
            await page.wait_for_timeout(400)
            d = await page.evaluate(READ_JS)
            toast_ok = d["toast"].strip() in ("已复制目录路径", "复制失败，请手动选择路径")
            clip = ""
            if d["toast"].strip() == "已复制目录路径":
                clip = await page.evaluate("async () => await navigator.clipboard.readText()")
            check(
                "⑥b 点复制 → toast（已复制/失败提示），成功时剪贴板=目录路径",
                toast_ok and (clip == "" or clip == MOCK_DIR),
                f"toast={d['toast']!r} clip={clip!r}",
            )

            await page.screenshot(path=os.path.join(HERE, "downloads-archive-scan.png"))

            # ---------- ⑦ 目录不存在空态 + 重新扫描恢复 ----------
            await page.evaluate("() => localStorage.setItem('zgj.mock.archiveMissing','1')")
            # 同 URL 的 goto 是 same-document 导航（SPA 状态不清零），必须真 reload
            await page.reload(wait_until="load")
            await page.locator("[data-testid=tab-archive]").wait_for(state="visible", timeout=10000)
            await click_archive_tab(page)
            await page.locator("[data-testid=archive-missing]").wait_for(state="visible", timeout=10000)
            d = await page.evaluate(READ_JS)
            check(
                "⑦a 目录不存在 → 空态含真实路径 + 「目录不存在」pill + 复制按钮仍在",
                d["missing"] and MOCK_DIR in d["missingText"] and "目录不存在" in d["dirLine"] and d["copyBtn"],
                f"missingText={d['missingText'][:110]!r}",
            )
            await page.evaluate("() => localStorage.removeItem('zgj.mock.archiveMissing')")
            await page.click("[data-testid=rescan]")
            await page.locator("[data-testid=archive-groups]").wait_for(state="visible", timeout=10000)
            d = await page.evaluate(READ_JS)
            check("⑦b 清旗标点「重新扫描」→ 分组恢复渲染（3 组）", d["rowCount"] == 3)

            await browser.close()
    finally:
        if owned is not None:
            owned.terminate()
            try:
                owned.wait(5)
            except Exception:
                owned.kill()

    fails = [r for r in RESULTS if r[0] == "FAIL"]
    print(f"\n== {len(RESULTS) - len(fails)}/{len(RESULTS)} PASS ==")
    return 1 if fails else 0


def json_str(s: str) -> str:
    import json

    return json.dumps(s, ensure_ascii=False)


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
