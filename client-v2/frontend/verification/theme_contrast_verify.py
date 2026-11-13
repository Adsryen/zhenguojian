#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""theme_contrast_verify.py —— 浅色主题的对比度守卫（第五十九轮）

为什么单独一只脚本：这轮查出 SeasonMenu（详情页「全 N 季」浮层）引用的是
`var(--guo-surface3, #1c2130)` —— 名字**少了一个连字符**，所以永远取到回退值，
而回退值是**深色的固定值**。文字色 `--guo-text` 却是两套主题都定义的
（浅色 `#23283a`）。结果浅色主题下悬停底 = 近黑 + 深灰字：

    实测（浏览器解析出的真实变量值算 WCAG 相对亮度）
      修前：#23283a on #1c2130 → **1.10:1**（文字几乎不可见）
      修后：#23283a on #e9edf5 → **12.46:1**

同批漂移还有三处，都是"引用未定义的名字 + 写了深色回退值"：
  · App.vue `var(--line, #2b2b33)`（2 处，浅色下描边偏深）→ `--border`
  · LibraryView `var(--text-muted, #8a8f98)`（8 处，浅色对比 3.04:1）→ `--muted`（5.25:1）
  · CreatorView `var(--danger-soft, rgba(212,58,58,.08))` → `--danger-hover-bg`（两套都定义）

这类 bug **没有任何一道门能抓到**：CSS 静默取回退值，typecheck 过、构建过、
Playwright 全绿；只有"解析出计算值再算对比度"才能发现。所以这只脚本是必需的，
不是锦上添花。

判据（浅色主题，全部读 `getComputedStyle` 的实际值，不读源码字面量）：
  ① 浅色块里每个颜色 token 都解析出**浅色**值（亮于 #808080 的亮度）？
     ——不这么判（会误伤刻意深色 token），改为：
  ① 关键组合的对比度达标：正文/次要文字/悬停底/描边四组，阈值 4.5:1 与 3:1；
  ② 深色主题同样这几组不回退（防止我把浅色改对、把深色改坏）；
  ③ 源码里不再出现"引用未定义 token"的这四类名字（静态兜底，跑在同一脚本里）；
  ④ 全程无 JS 错误。

端口：自管理 mock vite @5212（`_devserver`）。
"""
import os
import re
import subprocess
import sys

import _devserver
from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = "5212"
BASE = f"http://127.0.0.1:{PORT}"
EXE = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell")
SRC = os.path.join(FRONTEND, "src")

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail[:190]}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


def hex_to_rgb(value):
    v = value.strip()
    if v.startswith('#'):
        v = v.lstrip('#')
        if len(v) == 3:
            v = ''.join(c * 2 for c in v)
        return tuple(int(v[i:i + 2], 16) for i in (0, 2, 4))
    m = re.match(r"rgba?\(([^)]+)\)", v)
    if m:
        parts = [p.strip() for p in m.group(1).split(',')]
        return tuple(int(float(p)) for p in parts[:3])
    return None


def lum(rgb):
    def f(c):
        c = c / 255.0
        return c / 12.92 if c <= 0.03928 else ((c + 0.055) / 1.055) ** 2.4
    r, g, b = (f(c) for c in rgb)
    return 0.2126 * r + 0.7152 * g + 0.0722 * b


def ratio(a, b):
    la, lb = lum(a), lum(b)
    hi, lo = max(la, lb), min(la, lb)
    return (hi + 0.05) / (lo + 0.05)


def main():
    proc = _devserver.start_dev(FRONTEND, PORT, True, HERE, name="theme-contrast")
    js_errors = []
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE, headless=True)
            page = browser.new_context(viewport={"width": 1280, "height": 900}).new_page()
            page.on("pageerror", lambda e: js_errors.append(str(e)[:150]))

            def resolve(theme):
                page.goto(BASE + "/#/")
                page.wait_for_timeout(1500)
                page.evaluate("(t) => localStorage.setItem('zgj.theme', t)", theme)
                page.reload()
                page.wait_for_selector(".page-title, .nav-rail", timeout=25000)
                page.wait_for_timeout(1200)
                got = page.evaluate("""() => {
                  const cs = getComputedStyle(document.documentElement);
                  const names = ['--surface','--surface-2','--guo-surface-3','--guo-text',
                                 '--muted','--text','--border','--danger-hover-bg','--bg'];
                  const out = {};
                  for (const n of names) out[n] = cs.getPropertyValue(n).trim();
                  out.__theme = document.documentElement.getAttribute('data-theme');
                  return out;
                }""")
                return got

            light = resolve("light")
            check("前提：主题确实切到 light", light.get("__theme") == "light", str(light.get("__theme")))

            # ① 浅色主题关键组合对比度
            # 只留语义清晰的三组（第四组我一开始写成 "danger-hover-bg on --surface"，
            # 那是把背景当文字比，凑数没有意义，删掉）
            pairs = [
                ("正文 on 卡片底", "--text", "--surface"),
                ("次要文字 on 面板底", "--muted", "--surface-2"),
                ("guo 文字 on 悬停层", "--guo-text", "--guo-surface-3"),
            ]
            vals = {}
            for label, fg, bg in pairs:
                a, b = hex_to_rgb(light.get(fg, '')), hex_to_rgb(light.get(bg, ''))
                if not a or not b:
                    check("① %s 对比度可计算" % label, False, "%s=%r %s=%r" % (fg, light.get(fg), bg, light.get(bg)))
                    continue
                r = ratio(a, b)
                vals[label] = r
            check("① 次要文字 on 面板底 ≥ 4.5:1（漂移前是 #8a8f98 → 3.04:1）",
                  vals.get("次要文字 on 面板底", 0) >= 4.5, str(vals))
            check("① guo 文字 on 悬停层 ≥ 4.5:1（漂移前 #23283a on #1c2130 = 1.10:1）",
                  vals.get("guo 文字 on 悬停层", 0) >= 4.5, str(vals))
            check("① 正文 on 卡片底 ≥ 4.5:1",
                  vals.get("正文 on 卡片底", 0) >= 4.5, str(vals))

            # ② 深色主题不能被改坏
            dark = resolve("dark")
            dv = {}
            for label, fg, bg in pairs[:3]:
                a, b = hex_to_rgb(dark.get(fg, '')), hex_to_rgb(dark.get(bg, ''))
                if a and b:
                    dv[label] = round(ratio(a, b), 2)
            check("② 深色主题同样三组 ≥ 4.5:1（我把浅色改对时不能把深色改坏）",
                  len(dv) == 3 and all(v >= 4.5 for v in dv.values()), str(dv))

            # ③ 静态兜底：这四个"少连字符/凭猜拼法"的名字不得再出现在引用里
            banned = ["--guo-surface2", "--guo-surface3", "--line", "--text-muted",
                      "--danger-soft", "--surface-1", "--guo-font-mono"]
            hits = []
            for root, _dirs, files in os.walk(SRC):
                for fn in files:
                    if not fn.endswith((".vue", ".css", ".ts")):
                        continue
                    path = os.path.join(root, fn)
                    if path.endswith(os.path.join("styles", "tokens.css")):
                        continue  # tokens.css 里是**定义**，不是引用
                    text = open(path, encoding="utf-8").read()
                    text = re.sub(r"(?m)^\s*//.*$", "", text)
                    text = re.sub(r"<!--.*?-->", "", text, flags=re.S)
                    for name in banned:
                        for m in re.finditer(r"var\(\s*%s\s*[,)]" % re.escape(name), text):
                            hits.append("%s:%s" % (os.path.relpath(path, SRC),
                                                   text[:m.start()].count("\n") + 1))
            check("③ 无引用未定义 token（上一批是 %s）" % ", ".join(banned[:4]),
                  not hits, str(hits[:5]))

            check("④ 全程无 JS 错误", not js_errors, "; ".join(js_errors[:2]))
            browser.close()
    finally:
        _devserver.stop_dev(proc, PORT, name="theme-contrast")

    print("\n===== theme_contrast_verify: %s =====" %
          ("ALL PASS" if not failures else "FAILURES: " + ", ".join(failures)))
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
