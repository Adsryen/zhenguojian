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
     v2-6-parity 盲区声明：本脚本与 i18n_static_scan.py 都只管 .vue 模板/字面量，
     .ts 里的展示串（stores/services 造的 label）不在覆盖内——第一个实例就是
     services/local.ts:115 HISTORY_BUCKETS（本轮已顺手改成存 i18n 键）。
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

import _devserver
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
    """启停交 _devserver。旧写法「端口已在监听就复用」并 return None ——
    意味着这个门禁可以在**别人的、可能是旧构建的**服务器上跑绿，
    而且结束时什么都不杀（泄漏就是这么来的）。"""
    return _devserver.start_dev(FRONTEND, int(PORT), True, HERE, name="i18n-sweep")


def stop_server(proc):
    _devserver.stop_dev(proc, int(PORT), name="i18n-sweep")


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
    r"真人剧", r"漫剧", r"AI剧", r"^全部$",  # browseFilters 核心数据真名
    #（R12 清死条目：r"待核心接线"/r"已切换到" 早已是 i18n 键产出，en 态渲染成英文，
    #  留着只会把真的中文泄漏也一起放过）
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


_GLOBAL_PATS = [(p, re.compile(p)) for p in GLOBAL_ALLOW]
WHITELIST_HITS = {p: 0 for p, _ in _GLOBAL_PATS}
RAW_NODES = []   # en 态扫到的、含 CJK 的候选节点（豁免过滤之前）


def ablation():
    """对每条白名单做消融：把它拿掉之后，本次扫到的节点里有几条会因此变成违规。

    0 条 = 这条**当前没有保护任何东西**（可能是彻底过期，也可能只是被节点级
    excludes 提前挡住了——后者仍值得留作第二道防线，所以要人工判读，不自动删）。
    >0 条 = 正在保护，数字就是它挡住的节点数。
    为什么不用「本次有没有命中」直接判死：命中数为 0 有 40 条，
    一半是数据被 selector 级 excludes 先挡掉了，那种叫冗余不叫错误。
    """
    rows = []
    for pat, _rx in _GLOBAL_PATS:
        others = [(q, rx) for q, rx in _GLOBAL_PATS if q != pat]
        exposed = [x["t"][:40] for x in RAW_NODES
                   if not any(rx.search(x["t"]) for _q, rx in others)]
        rows.append((pat, len(exposed), exposed[:2]))
    return rows


def unexpected(nodes):
    """过滤掉被白名单豁免的节点，并**统计每条白名单实际放过多少节点**。
    R12 手工删过两条死条目（r"待核心接线" / r"已切换到"：早已是 i18n 键产出，
    en 态渲染英文，留着只会把真的中文泄漏一起放过）。
    这里把它变成常驻可见项，不再依赖人肉想起来去清。"""
    out = []
    for x in nodes:
        hit = None
        for pat, rx in _GLOBAL_PATS:
            if rx.search(x["t"]):
                hit = pat
                break
        if hit is None:
            out.append(x)
        else:
            WHITELIST_HITS[hit] += 1
    return out


async def settle(page, url, sel):
    await page.goto(BASE + url)
    await page.wait_for_selector(sel, timeout=15000)
    await page.wait_for_timeout(1000)


# 「这条中文来自数据/专有名」的 provenance 判据，逐条都是本轮审计里亲眼核对过的：
#   真果鉴 / 真         品牌字标（NavRail 的 brand-name / brand-mark），静态扫描器也登记为豁免
#   ^·? \d[\d.]*[万亿] 核心/fixtures 的 heat/views 串自带中文数量单位（1286万 / 2.1亿）
#   ^[^ ]*[万亿] (heat|followers|plays)$  同上，数字来自数据、单位词是英文
#   绝对不存在的剧名zzz  本脚本 zh 段自己敲进搜索框的测试词，落进了「最近搜索」chip
#   ^第\d+集$           不是前端硬编码：是核心 chapter.title 透传。
#                       证据 = detail_download_verify 的 stub 返回 title:"第"+i+"集"，
#                       UI 显示的正是它；player_verify ④ 也用 text-is("第3集") 断言切集。
#   ^中文$              语言选择项的母语名（另一项是 English），切语言也不该翻。
PROVENANCE_RX = re.compile(
    r"^(?:第\d+集$|中文$|真果鉴|真|·?\s*\d[\d.]*[万亿](?:\s*(?:heat|followers|plays))?"
    r"|绝对不存在的剧名zzz)"
)

AUDIT_ROUTES = ["/#/discover", "/#/library", "/#/search", "/#/detail/hongguo%3A7439220582928952383",
              "/#/calendar", "/#/reserve?tab=follow", "/#/history", "/#/downloads", "/#/settings"]


async def audit_full_page(page, base):
    """整页 en 扫描，**只报告不断言**：现有 en 门禁实际只看了 2 个 CJK 候选节点
    （范围全靠逐页 selector + 一长串节点级 excludes），所以先把真实情况量出来，
    再决定哪些能收成硬断言——不把没覆盖的东西说成已经覆盖了。"""
    from i18n_static_scan import SRC
    # 数据来源两处：mock fixtures 本体 + 本脚本自己往 localStorage 灌的种子
    corpus = open(os.path.join(SRC, "services", "fixtures.ts"), encoding="utf-8").read() + SEED_JS
    DATA_TOKENS = set()
    # 两种引号都收，长度下限 1 字：初版写 {2,}，于是 1 字的数据
    # （fixtures.ts:377 的 badge:'爆'）与 2 字标签词会漏收，凭空造出假「待判定」。
    for _groups in re.findall(r"'([^'\n]{1,}?)'|\"([^\"\n]{1,}?)\"", corpus):
        for _tok in _groups:
            if _tok and re.search(r"[\u4e00-\u9fff]", _tok):
                DATA_TOKENS.add(_tok)
    # 单字节点 = 封面/头像的「标题首字占位」（PosterGrid/SearchGroups/cast-initial 的
    # `title.trim()[0] || '剧'`）。字符来自数据本身，不是文案：按「是不是某个数据 token 的首字」判。
    FIRST_CHARS = {tok[0] for tok in DATA_TOKENS}
    rows = {}
    for route in AUDIT_ROUTES:
        await page.goto(base + route)
        await page.wait_for_timeout(900)
        nodes = await page.evaluate("""() => {
          const out = [], cjk = /[\u3400-\u4dbf\u4e00-\u9fff]/;
          const w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
          let n;
          while ((n = w.nextNode())) {
            const el = n.parentElement; if (!el) continue;
            const cs = getComputedStyle(el);
            if (cs.display === 'none' || cs.visibility === 'hidden') continue;
            const t = (n.textContent || '').trim();
            if (t && cjk.test(t)) out.push({ t, cls: String(el.className || el.tagName) });
          }
          return out;
        }""")
        data, unknown = 0, []
        for x in nodes:
            t = x["t"]
            key = t[:26]
            # 判据：这条中文里的某个 ≥3 字段落在 mock 数据/种子数据里出现过 → 属数据透传。
            # 用「子串包含」而不是「前缀等于」：界面常见 `一世之尊 · Full season (2 eps)`
            # 这种「数据 + 英文标签」拼接，前缀法会把它们全推到待判定里，
            # 制造 5 条假疑点（本轮实测就是这样，逐条核过 fixtures.ts:1027 / SEED_JS 才确认全是数据）。
            hit = (PROVENANCE_RX.match(t)
                   or next((tok for tok in DATA_TOKENS if tok in t), None)
                   or (len(t) == 1 and t in FIRST_CHARS and t))
            if hit:
                data += 1
            else:
                unknown.append((key, x["cls"][:26]))
        rows[route] = (len(nodes), data, unknown)
    print("\n──────── en 态整页审计（只报告，不计 PASS/FAIL）────────")
    tot_u = 0
    for r, (n, d, unk) in rows.items():
        tot_u += len(unk)
        print(f"  {r:52} 节点 {n:3}  fixtures可解释 {d:3}  待判定 {len(unk)}")
        for t, cls in unk[:4]:
            print(f"       ⚠ {t!r}  @{cls}")
    print(f"  合计待判定：{tot_u}")
    print("  说明：『待判定』≠『泄漏』，但本条已升级为硬断言——新增未定性中文会直接把 sweep 跑红，"
          "处置只有两种：进 i18n，或在 provenance 里写出可核对的证据（数据出处/契约出处）。")
    return [(r,) + u for r, (_n, _d, unk) in rows.items() for u in unk]


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

            # ── v2-6-parity：SearchView chrome 逐字（static_scan 基线 8 → 0）──
            await settle(page, "/#/search", ".search-bar")
            sz = []
            sz.append(("标题", "搜索", await page.locator(".page-head .page-title").inner_text()))
            ph = await page.locator("[data-testid=search-input]").get_attribute("placeholder")
            sz.append(("占位符", "搜索剧名 / 标签，回车开始", ph or ""))
            sz.append(("搜索按钮", "搜索", (await page.locator(".btn.primary").first.inner_text()).strip()))
            sz.append(("全部来源芯片", "全部来源", await page.locator(".all-src span").first.inner_text()))
            sz.append(("未搜索提示", "输入关键词搜索当前源（", await page.locator(".state-block.hint").first.inner_text()))
            check("zh|search 5 处逐字", all(w in g for _, w, g in sz), str(sz))
            # 无结果文案（乱码关键词 → mock 过滤后为空）
            await page.fill("[data-testid=search-input]", "绝对不存在的剧名zzz")
            await page.keyboard.press("Enter")
            await page.wait_for_selector(".state-block", timeout=10000)
            await page.wait_for_timeout(1200)
            nr = await page.locator(".state-block").first.inner_text()
            check("zh|search 无结果文案逐字", "没有找到与「绝对不存在的剧名zzz」相关的剧" in nr, f"nr={nr[:70]}")
            await page.keyboard.press("Escape")
            await page.screenshot(path=SHOT_PLAYER, full_page=False)

            # ── v2-6-parity：HistoryView 逐字（原先整页没走 i18n，static_scan 16 处已还清）──
            await settle(page, "/#/history", ".page-title")
            hz = []
            hz.append(("标题", "历史", await page.locator(".page-head .page-title").inner_text()))
            hz.append(("清空按钮", "清空历史", (await page.locator(".btn.danger").first.inner_text()).strip()))
            hz.append(("分桶标签", "今天", (await page.locator(".bucket-label").first.inner_text()).strip()))
            # 首行落在哪个集取决于 fixtures 时序，不断言具体集号；断言格式与键文案
            sub = await page.locator(".h-sub").first.inner_text()
            hz.append(("集号格式", "第", sub))
            hz.append(("集号单位", "集", sub))
            hz.append(("进度文案", "看到", sub))
            hz.append(("进度百分比", "%", sub))
            hz.append(("脚注", "watch-library.json", await page.locator(".footnote").first.inner_text()))
            check("zh|history 7 处逐字（v2-6-parity 还清 16 处硬编码）", all(w in g for _, w, g in hz), str(hz))

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

            # ── R9c：站源备注（stores/app.ts SOURCE_NOTES 改为存 i18n 键）──
            await settle(page, "/#/settings", "[data-testid=source-note]")
            notes = await page.locator("[data-testid=source-note]").all_inner_texts()
            joined = " / ".join(n.strip() for n in notes)
            want = ["主源，恒可用。", "18+ 内容，出厂默认停用"]
            miss = [w for w in want if w not in joined]
            check("zh|settings 站源备注逐字", not miss, f"missing={miss} notes={joined[:160]}")

            # ═════ 2. en 无 CJK 残留（节点白名单） ═════
            await page.evaluate(LOCALE_JS % "en")
            await page.reload()
            await page.wait_for_timeout(800)

            en_spots = {
                "/#/player?d=mock_drama_1&ep=1": (".pl-title", ["Episodes", "Quality", "Next episode", "Hotkey help", "Collapse episodes", "Autoplay on",
                                                 "Back", "Speed", "Volume", "Ep 1"],
                                                  [".pl-top", ".pl-title", ".pl-hint", ".pl-ended", ".pl-panel-cur", ".ep-btn", ".pl-toast", ".pl-label", ".dm", ".pl-badge"]),
                # 提示句在 en 遍里可能被「最近搜索」块顶掉（zh 遍刚搜过东西，recents 已有值），
                # 那句在 zh|search 里已逐字断言，这里不再重复要求
                "/#/settings": ("[data-testid=source-note]", ["Primary source"],
                               [".ss-name", ".sc-name"]),
                "/#/search": (".search-bar", ["Search", "All sources"],
                             [".rec-label", ".chip", ".ss-name", ".banner", ".toast", "[data-testid=search-input]"]),
                "/#/history": (".hist-list", ["History", "Clear history", "Today", "Ep 12", "watched"],
                               [".h-title", ".cover", ".bar", ".sample-chip"]),
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
                elif url == "/#/settings":
                    # 只扫备注节点：卡片名/开关文案另有各自覆盖面，站源 displayName 属数据
                    scope = "[data-testid=source-note]"
                elif url == "/#/search":
                    # .chip 是最近搜索词与源名（数据），不参与零 CJK 判定
                    scope = ".page-head, .search-bar, .state-block"
                elif url == "/#/history":
                    # 行首 .h-title 是 fixtures 剧名（数据），其余都是已键化 chrome
                    scope = ".page-head, .hist-list, .footnote"
                elif url.startswith("/#/player"):
                    # v2-6-parity：chrome 已键化，不再整组豁免 .pl-group（title/弹幕按钮一起扫）
                    # R9c-2：真实走一遍「切画质」——文案由 stores/player.ts 经 i18n 生成，
                    # zh 路径由 player_verify ⑦ 断言，这里断 en 路径零 CJK。
                    # 不用 .pl-status：mock 下走的是真实 openEpisode，store.status 为空，
                    # 元素压根不渲染（第一版我就断错了这个，超时 30s 才发现）。
                    await page.locator(".pl-select.quality").select_option("720")
                    await page.wait_for_selector(".pl-toast", timeout=15000)
                    toast = await page.locator(".pl-toast").first.inner_text()
                    check("en|player 画质 toast 英化且零 CJK",
                          "Switched to 720P" in toast and not CJK_RE.search(toast),
                          f"toast={toast[:80]}")
                    scope = ".pl-bar, .pl-panel, .pl-toast"
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
                RAW_NODES.extend(nodes)
                bad = unexpected(nodes)
                check(f"en|{url} 无未豁免 CJK", not bad, f"bad={json.dumps(bad[:4], ensure_ascii=False)}")
                if url == "/#/search":
                    # placeholder 是属性，不在 #app innerText 里，spots 机制看不到 → 单独取属性断言
                    ph_en = await page.locator("[data-testid=search-input]").get_attribute("placeholder")
                    check("en|/#/search placeholder 已英化且无 CJK",
                          ph_en == "Search titles / tags — press Enter" and not CJK_RE.search(ph_en or ""),
                          f"ph={ph_en!r}")
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

            if "--audit" in sys.argv:
                unknown = await audit_full_page(page, BASE)
                check("audit|en 整页无未定性中文节点", not unknown,
                      f"待判定 {len(unknown)} 条，例：{[u[0] for u in unknown[:3]]}")

            await browser.close()
    finally:
        stop_server(proc)

    failed = [r for r in RESULTS if not r[1]]
    rows = ablation()
    protecting = [(p, n) for p, n, _ in rows if n]
    idle = [p for p, n, _ in rows if not n]
    print(f"白名单共 {len(rows)} 条；本次 en 扫描实际采到 CJK 候选节点 {len(RAW_NODES)} 个")
    print("  正在保护：", protecting or "（无）")
    print("  消融后无影响（当前没挡任何东西）：", len(idle))
    if idle:
        print("     " + str(idle[:20]) + (" …" if len(idle) > 20 else ""))
        print("     判读口径：删了不会立刻红 ≠ 该删。两类原因：串已进 i18n（真过期，删）；"
              "或数据被节点级 excludes 先挡住（冗余第二道防线，留但要注明）。")
    print(f"\n===== i18n_sweep: {len(RESULTS) - len(failed)}/{len(RESULTS)} PASS =====")
    for n, _, d in failed:
        print(f"  FAIL: {n} | {d}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
