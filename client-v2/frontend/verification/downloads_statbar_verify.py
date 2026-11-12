#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""downloads_statbar_verify —— 下载统计条（H 轮：状态 chip + 站源下拉 + 打开所在目录）mock 端到端验证。

覆盖断言（任务书 ①–⑤ + 行操作自证）：
  ① 五 chip 渲染（全部/进行中/已暂停/已完成/失败），计数自洽：
     全部 = 四组之和 = 无筛选行数（removing 瞬时态 mock 面不出现，等式成立）。
  ② 点「失败」只剩失败行（全部行 pill-failed），再点取消恢复全量；chip active 类跟随。
  ③ 站源下拉生效：初始 options = 全部站源 + displayName（红果短剧，验证 app store 解析）；
     经「添加下载」入队一条 adult_lab 剧（午夜档案）→ options 追加「成人测试源」，
     选中后只剩该来源行（真实过滤，非仅切换值）。
  ④ 状态+站源叠加：成人测试源 × 进行中 → 仅午夜档案 1 行；chip 切「已暂停」→
     该组合无任务 → 过渡到空结果态。
  ⑤ 空结果态文案「没有符合筛选条件的任务」出现；重置 全部×全部站源 后行数恢复。
  ⑥ 「打开所在目录」：completed 行有该按钮且 disabled，title 含依赖请求说明
     （api.ts 无对应方法 → 待 Go 绑定 App.OpenDownloadPath，见返报）。

方法：VITE_MOCK dev server（5180，--strictPort，自管理：端口占用则复用）。
mock 队列推进不受筛选影响（fixtures 1s tick），断言全部用「同一 evaluate 内
chip 计数与 DOM 行数自洽」策略，规避跨采样竞态；失败/暂停集合与 adult_lab
新任务在测试窗口内状态稳定。

用法：python3 verification/downloads_statbar_verify.py
产物：verification/downloads-statbar.png
"""
import asyncio
import os
import socket
import subprocess
import sys
import time

import _devserver

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
# verification/ 就在 client-v2/frontend 下面：ROOT 已经是 frontend。
# 旧值 os.path.join(ROOT, "client-v2", "frontend") 拼出 …/frontend/client-v2/frontend，
# 不存在的路径 → 脚本此前根本起不了服务器。
FRONTEND = ROOT
PORT = 5180
BASE = f"http://127.0.0.1:{PORT}"
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


# 单次 evaluate 同帧读取：chip（key/label/count/active）+ 队列行（标题/pill）+ 下拉（值/options/空态）
READ_JS = """
() => {
  const chips = [...document.querySelectorAll('[data-testid=stat-chip]')].map((el) => ({
    key: el.dataset.stat,
    label: el.textContent.replace(/\\d+\\s*$/, '').trim(),
    count: parseInt((el.querySelector('[data-testid=stat-count]') || {}).textContent || '-1', 10),
    active: el.classList.contains('active'),
  }));
  const rows = [...document.querySelectorAll('.card.list .row')].map((r) => ({
    title: (r.querySelector('.r-title') || {}).textContent || '',
    pills: [...r.querySelectorAll('.pill')].map((p) => p.className),
    opens: [...r.querySelectorAll('[data-testid=row-open]')].map((b) => ({
      disabled: b.disabled,
      title: b.title || '',
    })),
  }));
  const sel = document.querySelector('[data-testid=source-select]');
  return {
    chips,
    rowCount: rows.length,
    rows,
    selValue: sel ? sel.value : null,
    selText: sel ? (sel.options[sel.selectedIndex] || {}).textContent || '' : '',
    selOptions: sel ? [...sel.options].map((o) => o.textContent.trim()) : [],
    emptyFiltered: !!document.querySelector('[data-testid=filtered-empty]'),
    emptyText: (document.querySelector('[data-testid=filtered-empty] div') || {}).textContent || '',
    statBar: !!document.querySelector('[data-testid=stat-bar]'),
  };
}
"""


def chip(d, key):
    return next((c for c in d["chips"] if c["key"] == key), None)


async def main():
    from playwright.async_api import async_playwright

    # 端口预检 + 就绪等待 + 严格收口见 _devserver.py（旧写法「端口已开就复用」
    # 会把别的脚本的 vite 当自己的服务器，与 detail_download_verify ② 假红灯同类）
    owned = _devserver.start_dev(FRONTEND, PORT, True, HERE, name="downloads-statbar")

    try:
        async with async_playwright() as pw:
            browser = await pw.chromium.launch(
                executable_path=EXEC if os.path.exists(EXEC) else None, headless=True
            )
            ctx = await browser.new_context(viewport={"width": 1280, "height": 860})
            page = await ctx.new_page()

            await page.goto(f"{BASE}/#/downloads", wait_until="load")
            await page.locator("[data-testid=stat-bar]").wait_for(state="visible", timeout=10000)
            # 等首轮快照进 chip（全部>0），避免把加载前空态当数据
            await page.wait_for_function(
                "() => { const e=document.querySelector('[data-testid=stat-chip] [data-testid=stat-count]'); return e && parseInt(e.textContent,10)>0; }",
                timeout=10000,
            )

            # ---------- ① 五 chip 渲染 + 计数自洽 ----------
            d = await page.evaluate(READ_JS)
            labels = [c["label"] for c in d["chips"]]
            counts = {c["key"]: c["count"] for c in d["chips"]}
            group_sum = counts.get("active", -1) + counts.get("paused", -1) + counts.get("completed", -1) + counts.get("failed", -1)
            check(
                "①a 五 chip 渲染，标签=全部/进行中/已暂停/已完成/失败",
                labels == ["全部", "进行中", "已暂停", "已完成", "失败"],
                f"labels={labels}",
            )
            check(
                "①b 计数自洽：全部 = 四组之和 = 无筛选行数",
                counts.get("") == group_sum == d["rowCount"] and counts.get("", 0) > 0,
                f"全部={counts.get('')} 四组={group_sum} rows={d['rowCount']}",
            )
            check(
                "①c 默认态：全部 chip active，其余未选中",
                (chip(d, "") or {}).get("active") and not any(c["active"] for c in d["chips"] if c["key"]),
                f"chips={[(c['key'], c['active']) for c in d['chips']]}",
            )

            # ---------- ② 点「失败」只剩失败行，再点恢复 ----------
            await page.click("[data-testid=stat-chip][data-stat=failed]")
            await page.wait_for_timeout(200)
            d = await page.evaluate(READ_JS)
            failed_ok = (
                d["rowCount"] == chip(d, "failed")["count"]
                and d["rowCount"] > 0
                and all(any("pill-failed" in p for p in r["pills"]) for r in d["rows"])
                and chip(d, "failed")["active"]
                and not chip(d, "")["active"]
            )
            check(
                "②a 点「失败」→ 行数=失败计数 且全部行 pill-failed 且 chip active",
                failed_ok,
                f"rows={d['rowCount']} failed={chip(d, 'failed')['count']} titles={[r['title'].strip() for r in d['rows']]}",
            )
            await page.click("[data-testid=stat-chip][data-stat=failed]")
            await page.wait_for_timeout(200)
            d = await page.evaluate(READ_JS)
            check(
                "②b 再点「失败」→ 取消筛选恢复全量（行数=全部计数，chip 不选中）",
                d["rowCount"] == chip(d, "")["count"] and not chip(d, "failed")["active"] and chip(d, "")["active"],
                f"rows={d['rowCount']} all={chip(d, '')['count']}",
            )

            # ---------- ③ 站源下拉：初始 displayName → 入队 adult_lab → 真实过滤 ----------
            d = await page.evaluate(READ_JS)
            check(
                "③a 下拉初始 options=全部站源+红果短剧（displayName 从 app store 解析，非 slug）",
                d["selOptions"] == ["全部站源", "红果短剧"],
                f"options={d['selOptions']}",
            )
            await page.select_option("[data-testid=source-select]", "hongguo")
            await page.wait_for_timeout(200)
            d = await page.evaluate(READ_JS)
            check(
                "③b 选「红果短剧」（队列唯一源）→ 行数不变（等于全部计数）",
                d["selValue"] == "hongguo" and d["rowCount"] == chip(d, "")["count"],
                f"rows={d['rowCount']} all={chip(d, '')['count']}",
            )

            # 添加下载：搜索「午夜」→ 午夜档案（source=adult_lab）→ 选 1 集 → 入队
            await page.click("button:has-text('添加下载')")
            await page.locator(".panel .textin").wait_for(state="visible", timeout=6000)
            await page.fill(".panel .textin", "午夜")
            await page.click(".panel button:has-text('搜索')")
            item = page.locator(".result-item:has-text('午夜档案')").first
            await item.wait_for(state="visible", timeout=8000)
            await item.click()
            await page.locator(".chapter-grid .chapter").first.wait_for(state="visible", timeout=8000)
            await page.locator(".chapter-grid .chapter").first.click()
            await page.click("button:has-text('加入下载队列')")

            # 等快照回推：下拉出现「成人测试源」（≤8s）
            try:
                await page.wait_for_function(
                    "() => [...document.querySelectorAll('[data-testid=source-select] option')].some(o => o.textContent.trim() === '成人测试源')",
                    timeout=8000,
                )
                arrived = True
            except Exception:
                arrived = False
            check("③c 入队午夜档案（adult_lab）后下拉追加「成人测试源」（jobs 快照 source 去重）", arrived)

            await page.select_option("[data-testid=source-select]", "adult_lab")
            await page.wait_for_timeout(250)
            d = await page.evaluate(READ_JS)
            titles = [r["title"].strip() for r in d["rows"]]
            check(
                "③d 选「成人测试源」→ 只剩午夜档案行（站源过滤真实生效）",
                d["rowCount"] >= 1 and all("午夜档案" in t for t in titles) and not any("一世之尊" in t or "闪婚" in t for t in titles),
                f"titles={titles}",
            )

            # ---------- ④ 状态 + 站源叠加 ----------
            await page.click("[data-testid=stat-chip][data-stat=active]")
            await page.wait_for_timeout(200)
            d = await page.evaluate(READ_JS)
            check(
                "④a 成人测试源 × 进行中 → 仅午夜档案（新入队任务 queued/downloading ∈ 进行中）",
                d["selValue"] == "adult_lab"
                and chip(d, "active")["active"]
                and d["rowCount"] >= 1
                and all("午夜档案" in r["title"] for r in d["rows"])
                and all(any("pill-queued" in p or "pill-downloading" in p for p in r["pills"]) for r in d["rows"]),
                f"rows={d['rowCount']} titles={[r['title'].strip() for r in d['rows']]}",
            )
            await page.screenshot(path=os.path.join(HERE, "downloads-statbar.png"))

            # ---------- ⑤ chip 切「已暂停」× adult_lab → 空结果态；重置恢复 ----------
            await page.click("[data-testid=stat-chip][data-stat=paused]")
            await page.wait_for_timeout(200)
            d = await page.evaluate(READ_JS)
            check(
                "⑤a 成人测试源 × 已暂停 → 0 行 + 空结果态文案（队列非空才显示，区别于队列空）",
                d["emptyFiltered"] and d["rowCount"] == 0 and "没有符合筛选条件的任务" in d["emptyText"],
                f"empty={d['emptyFiltered']} text={d['emptyText']}",
            )
            await page.click("[data-testid=stat-chip][data-stat='']")  # 点「全部」chip：清除状态筛选
            await page.select_option("[data-testid=source-select]", "")
            await page.wait_for_timeout(250)
            d = await page.evaluate(READ_JS)
            check(
                "⑤b 重置 全部×全部站源 → 行数恢复=全部计数，空结果态消失",
                d["rowCount"] == chip(d, "")["count"] and not d["emptyFiltered"] and d["selValue"] == "",
                f"rows={d['rowCount']} all={chip(d, '')['count']}",
            )

            # ---------- ⑥ 打开所在目录（依赖请求占位） ----------
            d = await page.evaluate(READ_JS)
            completed_rows = [r for r in d["rows"] if any("pill-completed" in p for p in r["pills"])]
            other_rows = [r for r in d["rows"] if not any("pill-completed" in p for p in r["pills"])]
            opens_completed = [r["opens"] for r in completed_rows]
            opens_other = [r["opens"] for r in other_rows]
            # 契约更新（本轮实测）：App.OpenDownloadPath 已实现（api.ts:343，
            # DownloadsView.openPathTitle:274），按钮不再是 disabled + 「依赖未就绪」占位提示，
            # 而是 completed 行可用、title 带真实目录。本脚本此前因 FRONTEND 路径拼错
            # 从来没跑起来过，所以这条断言过期了很久没人发现。
            check(
                "⑥a 「打开所在目录」仅出现在 completed 行、可用且 title 带目录",
                len(completed_rows) > 0
                and all(len(o) == 1 and not o[0]["disabled"]
                        and o[0]["title"].startswith("打开所在目录：") for o in opens_completed)
                and all(len(o) == 0 for o in opens_other),
                f"completed行={len(completed_rows)} title={opens_completed[0][0]['title'] if opens_completed else 'N/A'}",
            )

            await browser.close()
    finally:
        # SIGTERM → wait → SIGKILL → 回查端口确实关闭（防泄漏，见 _devserver.py）
        _devserver.stop_dev(owned, PORT, name="downloads-statbar")

    fails = [r for r in RESULTS if r[0] == "FAIL"]
    print(f"\n== {len(RESULTS) - len(fails)}/{len(RESULTS)} PASS ==")
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
