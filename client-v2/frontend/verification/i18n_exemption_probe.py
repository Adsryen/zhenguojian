#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""i18n_exemption_probe.py —— 拿实测检验「豁免条款」到底是不是界面文案。

背景（诚实性问题）：`i18n_static_scan.py` 把 46 条中文标成「不是界面文案」就归零了欠账，
理由写在表里（品牌名 / 数据透传 / 开发者日志 / 核心契约）。
但**理由是我推定的**，没有任何东西验证过"这些串真的不会出现在页面上"。
其中至少一条我自己在登记时就写了含糊话
（api.ts 的「未检测到 Wails 原生桥」注作「进 console 与**降级提示**」——
降级提示就是给用户看的，那它凭什么算豁免？）。

本脚本把推定换成证据：把每条豁免 needle 拿到真实渲染的 DOM 里去找。

跑两种态（都用 `_devserver`，独立端口）：
  A. dev:mock + locale=en   → 正常产品态（英文界面）
  B. dev（非 mock，不注入任何 window.go stub）→ 浏览器直开的错配态，
     这一态专门用来抓「降级/诊断串会不会露到界面上」

判定：
  · 命中且属于「数据透传」白名单（fixtures 剧名/简介/标签、核心 displayName 等）→ 记 DATA
  · 命中且是界面文案 → 记 LEAK（退出码 1：豁免登记错了或该串本该进字典）
  · 没命中 →记 AWAY（豁免理由成立：这条确实不在界面上）

用法：
  python3 verification/i18n_exemption_probe.py            # 汇总
  python3 verification/i18n_exemption_probe.py --list     # 打印每条命中上下文
"""
import argparse
import json
import os
import sys

import _devserver
import i18n_static_scan as scan

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
MOCK_PORT = 5201
PLAIN_PORT = 5202
EXEC = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell")

ROUTES = ["/#/discover", "/#/library", "/#/search", "/#/calendar",
          "/#/reserve?tab=follow", "/#/history", "/#/downloads", "/#/settings"]

# 真正的数据透传（fixtures 剧名/简介/标签、核心 displayName、榜名等），不是界面文案。
# 这里只允许列「来自 services/fixtures.ts 或核心应答」的具体串，别把界面文案塞进来。
DATA_PREFIXES = [
    "一世之尊", "星河远征", "闪婚", "山河引", "午夜", "种子剧", "编剧",
    "红果短剧", "黄果AI剧", "成人测试源", "全部来源",
]


def needles():
    """从扫描器直接读豁免表，保证两边不会漂。"""
    out = []
    for table, kind in ((scan.EXEMPT_REASONS, "vue"), (scan.TS_EXEMPT, "ts")):
        for path, items in table.items():
            for needle, reason in items:
                out.append((kind, path, needle, reason))
    return out


def collect(page):
    """抓界面上所有可能露文字的通道：可见文本 + title/placeholder/aria-label/alt。"""
    return page.evaluate("""() => {
      const out = [];
      const w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
      let n;
      while ((n = w.nextNode())) {
        const el = n.parentElement;
        if (!el) continue;
        const cs = getComputedStyle(el);
        if (cs.display === 'none' || cs.visibility === 'hidden') continue;
        const t = (n.textContent || '').trim();
        if (t) out.push(t);
      }
      for (const el of document.querySelectorAll('[title],[placeholder],[aria-label],[alt]')) {
        for (const a of ['title', 'placeholder', 'aria-label', 'alt']) {
          const v = el.getAttribute(a);
          if (v && v.trim()) out.push(v.trim());
        }
      }
      return out;
    }""")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--list", action="store_true")
    args = ap.parse_args()

    items = needles()
    results = {}          # needle -> (verdict, evidence)
    seen_text = {}        # (mode, route) -> [texts]

    mock = _devserver.start_dev(FRONTEND, MOCK_PORT, True, HERE, name="exempt-mock")
    plain = _devserver.start_dev(FRONTEND, PLAIN_PORT, False, HERE, name="exempt-plain")
    try:
        from playwright.sync_api import sync_playwright
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXEC, headless=True)
            for mode, port in (("mock/en", MOCK_PORT), ("plain/zh", PLAIN_PORT), ("plain/en", PLAIN_PORT)):
                ctx = browser.new_context(viewport={"width": 1280, "height": 800})
                page = ctx.new_page()
                base = f"http://127.0.0.1:{port}"
                loc = "en" if mode.endswith("en") else "zh"
                texts = []
                for i, route in enumerate(ROUTES):
                    page.goto(base + route)
                    page.wait_for_timeout(700)
                    if i == 0:
                        page.evaluate("(l) => localStorage.setItem('zgj.locale', l)", loc)
                        page.reload()
                        page.wait_for_timeout(700)
                    texts.extend(collect(page))
                seen_text[(mode, port)] = texts
                ctx.close()
            browser.close()
    finally:
        _devserver.stop_dev(mock, MOCK_PORT, name="exempt-mock")
        _devserver.stop_dev(plain, PLAIN_PORT, name="exempt-plain")

    leaks, away, data = [], [], []

    # ── 自检：采样要是空的，"46 条都没命中"就毫无意义（绿灯 = 没测）。
    #    拿一批**必然在界面上**的文案当探针，采不到就判定采样失效、直接红。
    sample_probe = {
        ("mock/en", MOCK_PORT): ["Browse", "Library", "Settings", "History"],
        ("plain/zh", PLAIN_PORT): ["找剧", "设置", "历史"],
    }
    probe_fail = []
    for key, must in sample_probe.items():
        texts = seen_text.get(key) or []
        blob = "\n".join(texts)
        miss = [m for m in must if m not in blob]
        if miss or len(texts) < 50:
            probe_fail.append(f"{key[0]}: 缺 {miss} / 采样 {len(texts)} 条")
    if probe_fail:
        print("FAIL 采样失效，本次绿灯不可信（先修探针再谈豁免）：")
        for line in probe_fail:
            print("  ", line)
        return 2
    print(f"采样自检通过："
          + "，".join(f"{k[0]} {len(v)} 条文本" for k, v in seen_text.items()))

    for kind, path, needle, reason in items:
        hit = None
        for (mode, _port), texts in seen_text.items():
            for t in texts:
                if needle in t:
                    hit = (mode, t)
                    break
            if hit:
                break
        if not hit:
            away.append((kind, path, needle))
        elif any(d in hit[1] for d in DATA_PREFIXES):
            data.append((kind, path, needle, hit))
        else:
            leaks.append((kind, path, needle, reason, hit))

    print(f"检查 {len(items)} 条豁免（三态渲染：mock/en、plain/zh、plain/en）")
    print(f"  界面上确实不出现（豁免成立）：{len(away)}")
    print(f"  只随数据出现（剧名/源名等）  ：{len(data)}")
    print(f"  **露在界面上（豁免站不住）** ：{len(leaks)}")
    if args.list:
        for kind, path, needle in away[:60]:
            print(f"  AWAY  {kind} {path}  {needle!r}")
        for kind, path, needle, hit in data:
            print(f"  DATA  {kind} {path}  {needle!r}  ← {hit[0]}: {hit[1][:70]!r}")
    for kind, path, needle, reason, hit in leaks:
        print(f"LEAK  {kind} {path}  {needle!r}  登记理由「{reason[:40]}…」"
              f"  实际出现在 {hit[0]}：{hit[1][:90]!r}")
    return 1 if leaks else 0


if __name__ == "__main__":
    sys.exit(main())
