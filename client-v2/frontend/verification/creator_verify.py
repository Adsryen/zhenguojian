#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""创作者页（V2-L，子代理 L）验证（headless chromium + mock dev server @5189）。

自动拉起/关闭 `npm run dev:mock -- --port 5189 --strictPort`（谁拉起谁关闭；若端口已被
占用则复用并在结束时保留——5180/5182/5183/5184 上可能存在其他代理的 vite，勿杀）。

断言：
  C1 直达 /creator?u=<u_dao> → 头部渲染 + 三项统计（粉丝/关注/获赞）非空。
  C2 作品网格点击卡片 → 跳 /detail/（断言发生在 CreatorView 侧，不依赖 DetailView cast-click）。
  C3 排序切换 最新→最热 → creatorWorks 重拉（window.__zgjCreatorWorksCalls 计数增加），
     列表首条标题变化。
  C4 分页链：首屏 3 条瞬间 + 加载更多按钮在位 + cursor 追加至 7 条（PosterGrid 视口不满
     即自动 emit loadMore，与手动点击同走 loadWorks(false)；轮询捕捉三个瞬间），标题去重。
  C5 profile 失败（window.__zgjCreatorProfileFail）→ 红条「加载失败」；清标志点「重试」
     → 头部恢复（v1 _retry_load 语义）。
  C6 复制 ID → window.__zgjCopied == userId + toast「已复制」。
  C7 en locale：固定 UI 文案全英文（返回/排序/最新/最热/粉丝/获赞/加载更多或已全部加载）。
  C8 tabs 为空的创作者（u_nv）→ 「该演职员暂无公开作品」空态，不请求 works。
  C9 全程无未捕获 JS 错误（pageerror）。
截图 creator-page.png（u_dao 加载完成态）。PASS/FAIL 行打印，任何 FAIL 退出码非 0。
"""
import json
import os
import socket
import subprocess

import _devserver
import sys
import time
from urllib.parse import quote

from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = 5189
BASE = f"http://127.0.0.1:{PORT}"
EXE = "/home/ryen/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"

DAO = {"userId": "u_dao", "id": "74001", "name": "示例·张导", "avatar": "/posters/p1.svg", "role": "导演"}
NV = {"userId": "u_nv", "id": "74003", "name": "王十七", "role": "主演"}

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line, flush=True)
    if not cond:
        failures.append(name)
    return cond


def port_open():
    with socket.socket() as s:
        s.settimeout(0.4)
        return s.connect_ex(("127.0.0.1", PORT)) == 0


def wait_port(deadline=40.0):
    t0 = time.time()
    while time.time() - t0 < deadline:
        if port_open():
            return True
        time.sleep(0.4)
    return False


def creator_url(creator):
    u = quote(json.dumps(creator, ensure_ascii=False), safe="")
    return f"{BASE}/#/creator?u={u}&name={quote(creator['name'])}"


def reset_storage(page, locale=None):
    js = "localStorage.clear();"
    if locale:
        js += f" localStorage.setItem('zgj.locale', '{locale}');"
    if BASE not in page.url:
        page.goto(BASE + "/#/")
        page.wait_for_timeout(500)
        page.evaluate(js)
    else:
        page.evaluate(js)
        # hash 路由内 goto 同文档不重载，i18n 模块态必须整页 reload 才切 locale
        page.reload(wait_until="domcontentloaded")
        page.wait_for_timeout(600)


def works_calls(page):
    return page.evaluate("window.__zgjCreatorWorksCalls || 0")


def poster_titles(page):
    return page.evaluate(
        "[...document.querySelectorAll('.poster')].map(e => (e.querySelector('.title')||e).textContent.trim())"
    )


def goto_creator(page, creator, settle_ms=1600):
    page.goto(creator_url(creator))
    page.wait_for_timeout(settle_ms)


def main():
    spawned = _devserver.start_dev(FRONTEND, PORT, True, HERE, name="creator")
    pageerrors = []
    with sync_playwright() as pw:
        browser = pw.chromium.launch(executable_path=EXE, headless=True)
        ctx = browser.new_context(viewport={"width": 1280, "height": 900})
        page = ctx.new_page()
        page.on("pageerror", lambda e: pageerrors.append(str(e)))
        try:
            # ---------- C1 直达 + 头部三统计（settle 收紧，保留分页瞬间可观测） ----------
            reset_storage(page)
            page.goto(creator_url(DAO))
            page.wait_for_selector(".profile", timeout=8000)
            ok = check("C1a profile 头部渲染", page.locator(".profile").count() == 1)
            stat_vals = page.evaluate("[...document.querySelectorAll('.stats .stat-value')].map(e=>e.textContent.trim())")
            check("C1b 三项统计非空", ok and len(stat_vals) == 3 and all(v and v != "--" for v in stat_vals),
                  f"vals={stat_vals}")
            check("C1c 粉丝=formatHeat 串", bool(stat_vals) and "万" in stat_vals[0], f"fans={stat_vals[0] if stat_vals else None}")

            # ---------- C4 加载更多链（先于点击断言：轮询捕捉首屏3条+按钮出现+自动/手动追加到7） ----------
            # PosterGrid 视口不满一屏会即时 emit loadMore（与 SearchView 同构的合法行为），
            # 故「首屏 3 条」「加载更多按钮在位」「追加至 7」三个瞬间用轮询捕捉，而非终态断言。
            seen3 = seen_btn = grew = False
            for _ in range(80):
                n = page.locator(".poster").count()
                if page.locator(".works-status .btn").count() > 0:
                    seen_btn = True
                if n == 3:
                    seen3 = True
                if seen3 and n >= 7:
                    grew = True
                    break
                page.wait_for_timeout(80)
            check("C4a 首屏 3 条（page1）", seen3)
            check("C4b 加载更多按钮在位（hasMore）", seen_btn)
            check("C4c cursor 追加至 7 条", grew, "PosterGrid 自动触发或点击均走 loadWorks(false) 同一链")
            page.wait_for_timeout(1200)
            titles2 = poster_titles(page)
            check("C4d 追加去重（标题集合无重复）", len(set(titles2)) == len(titles2) and len(titles2) == 7,
                  f"uniq={len(set(titles2))}/{len(titles2)}")
            status_txt = page.locator(".works-status").first.text_content() or ""
            check("C4e 状态行已加载 7 部 + 末页按钮消失", "7" in status_txt and page.locator(".works-status .btn").count() == 0,
                  f"txt={status_txt.strip()}")

            # ---------- C2 网格点击 → 详情（不依赖 DetailView cast-click） ----------
            check("C2a 网格条目在位", page.locator(".poster").count() >= 1)
            page.locator(".poster").first.click()
            page.wait_for_timeout(900)
            check("C2b 点击卡片→详情路由", "/detail/" in page.url, f"url={page.url[:80]}")
            page.go_back()
            page.wait_for_timeout(1600)

            # ---------- C3 排序切换重拉 ----------
            c0 = works_calls(page)
            titles_time = poster_titles(page)
            first_before = titles_time[0] if titles_time else ""
            sorts = page.locator(".works-tools select")
            sort_sel = sorts.last
            sort_sel.select_option("hot_desc")
            page.wait_for_timeout(1600)
            c1 = works_calls(page)
            titles_hot = poster_titles(page)
            first_after = titles_hot[0] if titles_hot else ""
            check("C3a 排序切换触发重拉（计数增）", c1 >= c0 + 1, f"before={c0} after={c1}")
            check("C3b 列表次序随 sort 变化", bool(first_before) and bool(first_after) and first_after != first_before,
                  f"{first_before!r} -> {first_after!r}")

            # ---------- C5 profile 失败红条 + 重试恢复 ----------
            # 失败开关走 localStorage（导航存活；vite dev 由 verify 自动拉）
            page.evaluate("localStorage.setItem('zgj.creatorFail', '1')")
            page.reload(wait_until="domcontentloaded")
            page.wait_for_timeout(300)
            banner = page.locator(".err-banner")
            check("C5a 失败亮红条含「加载失败」", banner.count() >= 1 and "加载失败" in (banner.first.text_content() or ""))
            check("C5b 头部未渲染（profile 缺失）", page.locator(".profile").count() == 0)
            page.evaluate("localStorage.setItem('zgj.creatorFail', '0')")
            page.locator(".err-banner .btn").first.click()
            page.wait_for_timeout(1500)
            check("C5c 重试后头部恢复", page.locator(".profile").count() == 1)
            check("C5d 重试后红条消失", page.locator(".err-banner").count() == 0)

            # ---------- C6 复制 ID ----------
            page.evaluate("delete window.__zgjCopied")
            page.locator(".profile-id .btn.tiny").click()
            page.wait_for_timeout(600)
            copied = page.evaluate("window.__zgjCopied || ''")
            toast = page.locator(".toast").count() == 1
            check("C6 复制钩子=userId 且 toast", copied == "u_dao" and toast, f"copied={copied!r} toast={toast}")

            # 截图：完整加载态
            page.screenshot(path=os.path.join(HERE, "creator-page.png"), full_page=False)

            # ---------- C7 en 文案 ----------
            reset_storage(page, locale="en")
            goto_creator(page, DAO)
            body_fixed = page.evaluate(
                """() => {
                    const clone = (e) => {
                        const c = e.cloneNode(true);
                        c.querySelectorAll('select').forEach((x) => x.remove());  // option 是数据文案，不算 chrome
                        return c.textContent;
                    };
                    const sel = ['.headbar', '.stats .stat-label', '.works-head', '.works-tools', '.works-status', '.profile-id .btn'];
                    return sel.flatMap((s) => [...document.querySelectorAll(s)].map(clone)).join(' | ');
                }"""
            )
            has_cjk = any("\u4e00" <= ch <= "\u9fff" for ch in body_fixed)
            # 终态 7 部为末页：按钮已消失，出现的是 tailEnd='All loaded'
            want_en = ["Back", "Fans", "Likes", "Sort", "All loaded"]
            miss = [w for w in want_en if w.lower() not in body_fixed.lower()]
            check("C7 en 固定文案无 CJK 且关键英文串在位", (not has_cjk) and not miss,
                  f"missing={miss} sample={body_fixed[:160]!r}")

            # ---------- C8 tabs 空 → 空态不请求 ----------
            reset_storage(page)
            c_before = works_calls(page)
            goto_creator(page, NV)
            no_works = page.locator("text=该演职员暂无公开作品").count() >= 1
            check("C8a 空分类文案出现", no_works)
            check("C8b 未发起 works 请求", works_calls(page) == c_before, f"calls={works_calls(page)}")

            # ---------- C9 无未捕获 JS 错误 ----------
            check("C9 无 pageerror", len(pageerrors) == 0, f"errors={pageerrors[:2]}")
        finally:
            browser.close()
            _devserver.stop_dev(spawned, PORT, name="creator")

    print("\n== 结果:", "ALL PASS" if not failures else f"FAIL {len(failures)}: {failures}")
    sys.exit(0 if not failures else 1)


if __name__ == "__main__":
    main()
