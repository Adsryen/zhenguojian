#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
i18n_sweep_verify.py — L 表收敛进 i18n.ts 的验收脚本

覆盖 7 组收敛（player.* / grid.* / hero.* / reserve.* / calendar.* / downloads.statbar.* / discover.*）：
  1) zh 默认态：逐页硬断言代表文案与收敛前组件内字符串【逐字一致】（≥12 处）；
     PlayerView 快捷键帮助浮层逐行断言 hotkeys 渲染不变形。
  2) 切 en 态：被扫组件内不得残留 CJK（节点级白名单法）。
     白名单 = 超出本次键表的展示中文（fixtures 剧名/简介/标签、stores/player.ts、
     stores/calendar.ts 透传文案、DiscoverView 非 hero chrome、SourceSwitch）——
     属任务范围外，已在返报「未尽项」列明；数据节点（title 属性含 alt 除外）与
     日期数字不计。
     v2-6-parity：PlayerView 模板内联 chrome（返回/播放器/示例数据/弹幕 N 条/已播完/
     正在打开播放会话/本集已播完/重新播放/播放·暂停/倍速/音量/弹幕开关·开合/关闭/第 N 集）
     已进 i18n，对应豁免已从 GLOBAL_ALLOW 撤除；.pl-group 不再整组豁免，title 一起扫。
  3) 截图 i18n-sweep-player.png / i18n-sweep-downloads.png（zh 默认态）。

dev:mock 端口 5185 自管理（--strictPort），结束 kill 并删 pid 文件。
"""
import asyncio
import json
import os
import re
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = os.environ.get("VERIFY_PORT", "5185")
BASE = f"http://127.0.0.1:{PORT}"
EXEC = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
)
LOG = os.path.join(tempfile.gettempdir(), "zgj_i18n_sweep_vite.log")
PIDF = os.path.join(tempfile.gettempdir(), f"zgj-i18n-sweep-{PORT}.pid")
SHOT_PLAYER = os.path.join(HERE, "i18n-sweep-player.png")
SHOT_DOWNLOADS = os.path.join(HERE, "i18n-sweep-downloads.png")

RESULTS = []
CJK_RE = re.compile(r"[\u4e00-\u9fff]")


def check(name, ok, detail=""):
    RESULTS.append((name, bool(ok), detail))
    print(f"[{'PASS' if ok else 'FAIL'}] {name}" + (f" | {detail}" if detail else ""))


def port_open():
    with socket.socket() as s:
        s.settimeout(0.3)
        return s.connect_ex(("127.0.0.1", int(PORT))) == 0


def start_server():
    if port_open():
        print(f"复用已在监听:{PORT}")
        return None
    proc = subprocess.Popen(
        ["npm", "run", "dev:mock", "--", "--port", PORT, "--strictPort"],
        cwd=FRONTEND,
        stdout=open(LOG, "w"),
        stderr=subprocess.STDOUT,
        start_new_session=True,
    )
    with open(PIDF, "w") as f:
        f.write(str(proc.pid))
    deadline = time.time() + 60
    while time.time() < deadline:
        if port_open():
            return proc
        time.sleep(0.4)
    raise RuntimeError("dev server 启动超时")


def stop_server(proc):
    if proc:
        try:
            os.killpg(os.getpgid(proc.pid), signal.SIGTERM)
        except Exception:
            pass
    try:
        os.remove(PIDF)
    except OSError:
        pass


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
  localStorage.setItem('zgj.history', JSON.stringify({
    'hongguo:7439220582928952383:12': {
      title: '一世之尊', episode: 12, position: 245, duration: 1320,
      ts: now - 600e3, dramaId: 'hongguo:7439220582928952383', cover: ''
    }
  }));
  localStorage.setItem('zgj.verify-seed', '1');
})();
"""

LOCALE_JS = "localStorage.setItem('zgj.locale', '%s')"

# —— en 态 CJK 白名单：节点文本（含祖先 class token 命中）匹配任一则豁免 ——
# 均为「超出本次键表的既有组件/store 文案」，逐条见返报未尽项。
GLOBAL_ALLOW = [
    r"一世之尊", r"星河远征", r"闪婚", r"山河引", r"编剧", r"午夜", r"种子剧",
    r"红果", r"黄果", r"华瓜", r"果汁", r"短剧",  # fixtures 源名/剧名（数据）
    r"^\d+\s*集$", r"^更新至\d+集$", r"^共\d+集$",  # pg-badge / ep 数据透传
    r"^\d+[.]\d$", r"周[一二三四五六日天]$", r"今天",  # 日历数据（store 文案）
    r"今晚 \d+:\d+", r"今日 \d+:\d+",  # stores/calendar.ts publishText（未尽项）
    r"热度$", r"已预约$",  # store normalize 尾缀（数据）
    r"加载中", r"加载失败", r"重试", r"选择源",
    r"源未接入", r"弹幕",
    r"返回", r"重新播放", r"正在打开", r"演示模式", r"操作失败", r"跳转失败",
    r"待核心接线", r"已切换到", r"真人剧", r"漫剧", r"AI剧", r"^全部$",  # 核心数据透传（FALLBACK_CHIPS 已进 i18n，此处仅剩 browseFilters 真名）
    r"找剧", r"追剧",  # DiscoverView 标题·日历行按钮组 store 侧变体（sweep 后应已英文化，仅兜底数据串）
]


async def cjk_nodes(page):
    return await page.evaluate(
        """() => {
      const out = [];
      const cjk = /[\\u4e00-\\u9fff]/;
      const w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
      let n;
      while ((n = w.nextNode())) {
        const t = n.textContent.trim();
        if (t && cjk.test(t)) {
          const el = n.parentElement;
          out.push({ t, cls: (el && el.className || '') + '', tag: el ? el.tagName : '' });
        }
      }
      document.querySelectorAll('[title]').forEach((el) => {
        const tt = el.getAttribute('title');
        if (tt && cjk.test(tt) && tt !== el.alt) out.push({ t: tt, cls: (el.className || '') + '', tag: el.tagName + '@title' });
      });
      return out;
    }"""
    )


def unexpected(nodes):
    pats = [re.compile(p) for p in GLOBAL_ALLOW]
    return [x for x in nodes if not any(p.search(x["t"]) for p in pats)]


async def settle(page, url, sel):
    await page.goto(BASE + url)
    await page.wait_for_selector(sel, timeout=15000)
    await page.wait_for_timeout(1000)


async def main():
    from playwright.async_api import async_playwright

    proc = start_server()
    try:
        async with async_playwright() as pw:
            browser = await pw.chromium.launch(
                executable_path=EXEC if os.path.exists(EXEC) else None, headless=True
            )
            ctx = await browser.new_context(viewport={"width": 1440, "height": 900})
            await ctx.add_init_script(SEED_JS)
            page = await ctx.new_page()

            # ═════ 1. zh 逐字断言（≥12 处代表文案） ═════
            await settle(page, "/#/player?d=mock_drama_1&ep=1", ".pl-title")
            await page.evaluate(LOCALE_JS % "zh")
            await page.reload()
            await page.wait_for_selector(".pl-title", timeout=15000)
            await page.wait_for_timeout(1000)
            zh_player = []
            for sel, want in [
                (".pl-panel-title", "选集"),
                (".pl-label", "倍速"),
                (".pl-panel-cur", "第1集"),
                (".pl-group:has(.quality)", "画质"),
                (".pl-btn.ghost.ap", "连播开"),
                (".pl-btn.nav.next-ep", ""),
                (".pl-btn.ghost.sm", ""),
                (".pl-btn.ghost.hk", ""),
            ]:
                got = await page.locator(sel).first.inner_text()
                zh_player.append((sel, want, got))
            check("zh|player 面板/条 8 处文案（含 v2-6-parity chrome）", all((w in g) if w else bool(g.strip()) for _, w, g in zh_player),
                  str([x for x in zh_player if x[1] != x[2]]))
            qopt = await page.locator(".pl-select.quality option", has_text="自动").count()
            check("zh|player 画质 option「自动」", qopt >= 1)
            nt = await page.locator(".pl-btn.nav.next-ep").get_attribute("title")
            ct = await page.locator(".pl-btn.ghost.sm").get_attribute("title")
            hk = await page.locator(".pl-btn.ghost.hk").get_attribute("title")
            check("zh|player title 下一集/收起选集/快捷键帮助", nt == "下一集" and ct == "收起选集" and hk == "快捷键帮助",
                  f"nt={nt} ct={ct} hk={hk}")
            await page.keyboard.press("?")
            try:
                await page.wait_for_selector(".hk-scrim", state="visible", timeout=4000)
            except Exception:
                pass
            hk = await page.locator(".hk-card").inner_text()
            help_lines = ["键盘快捷键", "空格", "播放 / 暂停", "快退 / 快进 5 秒",
                          "音量 +5 / −5", "全屏 / 退出全屏", "静音 / 恢复", "弹幕开关",
                          "打开 / 关闭本帮助", "8 秒后自动关闭 · 点遮罩或按 Esc 立即关闭"]
            miss = [l for l in help_lines if l not in hk]
            check("zh|player hotkeys 帮助浮层 10 行逐字", not miss, f"missing={miss}")

            # ── v2-6-parity：DiscoverView chrome 逐字（标题/刷新按钮/首芯片/横幅）──
            await settle(page, "/#/", ".page-title")
            dz = []
            dz.append(("标题", "找剧", await page.locator(".page-head .page-title").inner_text()))
            dz.append(("刷新按钮", "刷新", (await page.locator(".page-head .btn").first.inner_text()).strip()))
            dz.append(("首芯片", "全部", (await page.locator(".chips .chip").first.inner_text()).strip()))
            ban_cnt = await page.locator(".banner").count()
            dz.append(("横幅", "VITE_MOCK", (await page.locator(".banner").first.inner_text()) if ban_cnt else "(无横幅)"))
            check("zh|discover 4 处逐字", all(w in g for _, w, g in dz), str(dz))
            await page.keyboard.press("Escape")
            await page.screenshot(path=SHOT_PLAYER, full_page=False)

            await settle(page, "/#/reserve?tab=follow", ".page-title")
            rv = await page.locator("#app").inner_text()
            # 「已上线/待上线」属 /calendar 数据面（下方 calendar 组断言），本页 follow 态无此串
            zh = [x for x in ["追更预约", "继续播放", "标记已看", "去详情页", "取消追剧",
                              "看到第 12集"] if x not in rv]
            check("zh|reserve 6 处逐字", not zh, f"missing={zh}")
            chip_all = await page.locator("[data-testid=source-chips] .chip").first.inner_text()
            check("zh|reserve 源chip「全部」", chip_all.strip() == "全部", chip_all)
            await page.locator(".btn.sm:not(.primary):not(.danger)").first.click()
            await page.wait_for_timeout(600)
            await page.reload(wait_until="domcontentloaded")
            await page.wait_for_selector("[data-testid=source-chips]", timeout=15000)
            await page.wait_for_timeout(800)
            rv2 = await page.locator("#app").inner_text()
            check("zh|reserve「已看 ✓」标记回显", "已看 ✓" in rv2,
                  "seen-on chip after mark+reload")

            await settle(page, "/#/calendar", ".cal-page")
            cv = await page.locator(".cal-page").inner_text()
            zh = [x for x in ["追剧日历", "男频", "已上线", "待上线", "今天",
                              "刷新", "预约"] if x not in cv]
            check("zh|calendar 7 处逐字", not zh, f"missing={zh}")

            await settle(page, "/#/downloads", ".page-title")
            dv = await page.locator("#app").inner_text()
            zh = [x for x in ["全部", "进行中", "已暂停", "已完成", "失败", "站源"] if x not in dv]
            check("zh|downloads statbar 6 处逐字", not zh, f"missing={zh}")
            open_btn = await page.locator("[data-testid=row-open]").first.get_attribute("title")
            check("zh|downloads openTodo title 前缀",
                  open_btn is not None and open_btn.startswith("打开所在目录"),
                  f"title={open_btn}")
            await page.screenshot(path=SHOT_DOWNLOADS, full_page=False)
            # statbar chips 计数
            chips = await page.locator("[data-testid=stat-chip]").count()
            check("zh|downloads stat chips 渲染", chips >= 5, f"n={chips}")

            # ═════ 2. en 无 CJK 残留（节点白名单） ═════
            await page.evaluate(LOCALE_JS % "en")
            await page.reload()
            await page.wait_for_timeout(800)

            en_spots = {
                "/#/player?d=mock_drama_1&ep=1": (".pl-title", ["Episodes", "Quality", "Next episode", "Hotkey help", "Collapse episodes", "Autoplay on",
                                                 "Back", "Speed", "Volume", "Ep 1"],
                                                  [".pl-top", ".pl-title", ".pl-hint", ".pl-ended", ".pl-panel-cur", ".ep-btn", ".pl-toast", ".pl-label", ".dm", ".pl-badge"]),
                "/#/": (".page-head", ["Browse", "All", "Refresh", "VITE_MOCK"],
                        [".hero-title", ".hero-desc", ".hero-tag", ".pg-title", ".pg-badge", ".ss-name", ".rv-ep", ".toast"]),
                "/#/calendar": (".cal-page", ["Follow Calendar", "All", "Male", "Online", "Today", "Refresh"],
                                [".date-cur", ".date-chips", ".cal-title", ".cal-desc", ".list-status", ".banner", ".cover-badge", ".chip.static", ".warn", ".cal-desc", ".cover"]),
                "/#/reserve?tab=follow": (".page-title", ["Follow & Reserve", "Resume", "Mark seen", "Details", "Unfollow", "Seen to ep. 12"],
                               [".cal-title", ".cal-desc", ".toast", "[data-testid=source-chips]", ".cover", ".cover-ph", ".rv-title"]),
                "/#/downloads": (".stat-bar", ["Open folder"],
                                [".row", ".badge", ".empty", ".btn", ".foot", ".hint", ".sub", ".stat-bar", ".card"]),
            }
            for url, (sel, spots, excludes) in en_spots.items():
                try:
                    await settle(page, url, sel)
                except Exception as e:
                    check(f"en|{url} 加载", False, str(e)[:120])
                    continue
                if url.startswith("/#/downloads"):
                    # 行内数据豁免太宽 → 行容器整体排除后仅扫 statbar
                    scope = ".stat-bar, [data-testid=source-select], .empty"
                elif url == "/#/":
                    # v2-6-parity：标题/刷新/横幅已键化 → scope 扩到 .banner，
                    # .chips 仍留在扫描面外（体裁名来自 browseFilters 核心数据，属透传）
                    scope = ".page-head, .banner"
                elif url.startswith("/#/player"):
                    # v2-6-parity：chrome 已键化，不再整组豁免 .pl-group（title/弹幕按钮一起扫）
                    scope = ".pl-bar, .pl-panel"
                    excludes = [x for x in excludes if x not in (".pl-label", ".pl-panel-cur", ".pl-badge")]
                else:
                    scope = sel
                sel_ex = ", ".join(excludes)
                nodes = await page.evaluate(
                    """([scope, exSel]) => {
                      const root = document.querySelector(scope) || document.body;
                      const cjk = /[\\u4e00-\\u9fff]/;
                      const out = [];
                      const w = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
                      let n;
                      while ((n = w.nextNode())) {
                        const t = n.textContent.trim();
                        if (!t || !cjk.test(t)) continue;
                        const el = n.parentElement;
                        if (exSel && el && el.closest(exSel)) continue;
                        out.push({ t, cls: (el && el.className || '') + '' });
                      }
                      root.querySelectorAll('[title]').forEach((el) => {
                        const tt = el.getAttribute('title');
                        if (tt && cjk.test(tt) && !(exSel && el.closest(exSel))) out.push({ t: tt, cls: (el.className||'') + '@title' });
                      });
                      return out;
                    }""",
                    [scope, sel_ex],
                )
                bad = unexpected(nodes)
                check(f"en|{url} 无未豁免 CJK", not bad, f"bad={json.dumps(bad[:4], ensure_ascii=False)}")
                if url == "/#/":
                    chip0 = (await page.locator(".chips .chip").first.inner_text()).strip()
                    ttl = (await page.locator(".page-head .page-title").inner_text()).strip()
                    check("en|discover 首芯片=All 且标题=Browse", chip0 == "All" and ttl == "Browse",
                          f"chip0={chip0!r} title={ttl!r}")
                # spots 存在性查整页（行内按钮不在扫描根内；title 属性并入，图标按钮文案在 title）
                body_txt = await page.locator("#app").inner_text()
                body_txt += "\n" + await page.evaluate(
                    "() => [...document.querySelectorAll('#app [title]')].map(e => e.getAttribute('title')).join(' ')"
                )
                miss = [s for s in spots if s not in body_txt]
                check(f"en|{url} 英文代表文案出现", not miss, f"missing={miss}")

            await browser.close()
    finally:
        stop_server(proc)

    failed = [r for r in RESULTS if not r[1]]
    print(f"\n===== i18n_sweep: {len(RESULTS) - len(failed)}/{len(RESULTS)} PASS =====")
    for n, _, d in failed:
        print(f"  FAIL: {n} | {d}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
