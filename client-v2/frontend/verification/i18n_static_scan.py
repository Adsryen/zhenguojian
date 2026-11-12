#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""i18n_static_scan.py —— 前端展示文案的**静态**门禁（不启服务器、不起浏览器）。

为什么再要一个脚本：`i18n_sweep_verify.py` 是浏览器态验收，它靠**逐页枚举的白名单**
（GLOBAL_ALLOW + 每页 excludes）判「en 态有没有残留中文」。这在过去两轮里出过一次
自我高估：PlayerView 的 15 处内联中文全部躲在白名单里，绿灯照跑，
`GLOBAL_ALLOW` 注释自己写着「未尽项」（见 7fd6ed2/a2e7c36）。
本脚本不做浏览器断言，只做一件确定性的事：**把 .vue 里露在模板/字符串里的中文数出来**，
所以「还剩多少没收敛」第一次变成可数的数字，而不是某轮返报的印象。

判据
  1) `<template>` 区：含 CJK 的行即为违规，除非该行在 `<!-- -->` 注释内。
  2) `<script>` 区：只查字符串字面量（'…' "…" `…`）里的 CJK；行注释、块注释、
     以及整行只有注释的一律不算。
  3) `EXEMPT_FILES`：mock 数据与字典本身（fixtures/i18n 等），中文是它们的**内容**而非界面。
  4) `EXEMPT_REASONS`：确属核心数据透传/开发者可见日志的行（path → 关键词），
     必须写明原因，命中才豁免；不写原因的违规一律算红。

用法：
  python3 verification/i18n_static_scan.py            # 汇总；有违规则退出码 1
  python3 verification/i18n_static_scan.py --list     # 逐条列出（收敛时用）

退出码：0 = 违规数 == 已登记豁免数；1 = 有未登记违规（新代码没走 i18n）。
"""
import argparse
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.abspath(os.path.join(HERE, "..", "src"))

CJK = re.compile(r"[㐀-䶿一-鿿]")
HTML_COMMENT = re.compile(r"<!--.*?-->", re.S)
STR_LITERAL = re.compile(r"'[^'\n]*'|\"[^\"\n]*\"|`[^`]*`")
LINE_COMMENT = re.compile(r"^\s*(//|/\*|\*)")
JS_BLOCK_COMMENT = re.compile(r"/\*.*?\*/", re.S)

# 内容本身就是中文的文件（假数据、字典、样式令牌）
EXEMPT_FILES = {
    "services/i18n.ts",       # 字典本体
    "services/fixtures.ts",   # VITE_MOCK 假数据：剧名/简介/标签是数据不是界面
    "services/local.ts",      # 本地暂存键与注释
}

# 逐条豁免：path → [(子串, 原因)]。命中子串的行不算违规。
# 只允许「核心数据透传」「开发者日志（不进 UI）」两类，新增必须写原因。
EXEMPT_REASONS = {
    "views/LibraryView.vue": [
        ("console.warn('rankingBoards 回退常量表", "开发者日志，不进 UI"),
        ("name: '总热播榜'", "桥接失败时的榜名回退表，逐字对齐 provider_rankings.go:23-37（核心数据）"),
        ("name: '魔改榜'", "同上"),
        ("name: '搜索榜'", "同上"),
        ("name: '收藏榜'", "同上"),
        ("name: '完结榜'", "同上"),
        ("name: '推荐榜'", "同上"),
        ("name: '潜力榜'", "同上"),
        ("name: '热播榜'", "同上"),
        ("name: '总榜'", "同上"),
        ("name: '真人剧榜'", "同上"),
        ("name: '漫剧榜'", "同上"),
        ("name: 'AI剧榜'", "同上"),
        ("|| '剧'", "无封面时按剧名首字占位，取的是数据字符串首字（PosterGrid 同款兜底）"),
    ],
    "views/DiscoverView.vue": [
        ("console.info('browseFilters 兜底", "开发者日志，不进 UI"),
        ("throw new ApiError('无 genre/category 组'", "内部错误诊断，被同函数 catch 后只进 console.info"),
    ],
    "views/CalendarView.vue": [
        ("console", "开发者日志"),
    ],
}


# ── 棘轾（ratchet）──
# 本工具诞生时（2026-11-12）全仓还有 47 处未走 i18n 的界面中文，分布在 10 个文件。
# 与其让门禁长期红（红的门禁等于没门禁），这里按文件钉住**当前欠账数**：
#   · 某文件违规数 > 基线 → 红（新代码不许再加硬编码中文）
#   · 某文件违规数 < 基线 → 红（还了债就必须回来把数字调低，防止基线悄悄失真）
# 还完一个文件就从表里删掉它。改动这张表的唯一合法时机是「顺手少了几处硬编码」。
BASELINE = {
    "views/SettingsView.vue": 5,
    "components/SourceSwitch.vue": 4,
    "components/SeasonMenu.vue": 3,
    "components/NavRail.vue": 2,
    "components/PosterGrid.vue": 2,
    "views/PlayerView.vue": 2,
    "components/SearchGroups.vue": 1,
}


def rel(path):
    return os.path.relpath(path, SRC).replace(os.sep, "/")


def scan_file(path):
    """返回 (违规行, 开发者日志行)。console.* 里的中文不进 UI，单独计数不判红。"""
    out = []
    devlog = []
    text = open(path, encoding="utf-8").read()
    lines = text.split("\n")
    in_template = False
    in_script = False
    in_html_comment = False
    tpl_depth = 0   # SFC 根 <template> 与组件内 <template v-if> 要区分：只有根区才算模板
    for i, raw in enumerate(lines, 1):
        st = raw.strip()
        if st.startswith("<template"):
            # 单行插槽 `<template v-if="…">中文</template>` 也是界面文案，不能整行跳过
            if "</template>" in st:
                pass
            else:
                tpl_depth += 1
                in_template = True
                continue
        if st == "</template>":
            tpl_depth = max(0, tpl_depth - 1)
            in_template = tpl_depth > 0
            continue
        if st.startswith("<script"):
            in_script = True
            continue
        if st.startswith("</script>"):
            in_script = False
            continue
        if not CJK.search(raw):
            continue
        if LINE_COMMENT.match(raw):
            continue
        if in_template:
            # 跨行 HTML 注释：进入 <!-- 后到 --> 之前的中文都是说明，不是界面
            if in_html_comment:
                if "-->" in raw:
                    in_html_comment = False
                continue
            stripped = re.sub(r"<!--.*?-->", "", raw)
            if "<!--" in raw and "-->" not in raw:
                in_html_comment = True
            if not CJK.search(stripped):
                continue
            kind = "template"
        elif in_script:
            body = JS_BLOCK_COMMENT.sub("", raw)
            if LINE_COMMENT.match(body.strip()):
                continue
            # 注释尾：把 // 之后切掉
            body = body.split("//")[0]
            lits = [m.group(0) for m in STR_LITERAL.finditer(body) if CJK.search(m.group(0))]
            if not lits:
                continue  # 只有注释里的中文
            if re.search(r"\bconsole\.(info|warn|log|error|debug)\s*\(", body):
                devlog.append((rel(path), i, lits[0][:60]))
                continue  # 开发者日志，不进 UI
            kind = "string"
        else:
            continue  # <style> 等
        r = rel(path)
        for needle, _ in EXEMPT_REASONS.get(r, []):
            if needle in raw:
                break
        else:
            out.append((r, i, kind, st[:120]))
    return out, devlog


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--list", action="store_true", help="逐条打印违规")
    args = ap.parse_args()

    all_rows = []
    dev_logs = []
    for root, dirs, files in os.walk(SRC):
        dirs[:] = [d for d in dirs if d not in ("node_modules",)]
        for f in files:
            if not f.endswith(".vue"):
                continue
            p = os.path.join(root, f)
            if rel(p) in EXEMPT_FILES:
                continue
            rows, logs = scan_file(p)
            all_rows.extend(rows)
            dev_logs.extend(logs)

    total_vue = sum(1 for _ in [1 for root, _, files in os.walk(SRC) for f in files if f.endswith(".vue")])
    byfile = {}
    for r, _, _, _ in all_rows:
        byfile[r] = byfile.get(r, 0) + 1
    print(f"扫描 {total_vue} 个 .vue（豁免文件 {len(EXEMPT_FILES)} 个、逐条豁免 "
          f"{sum(len(v) for v in EXEMPT_REASONS.values())} 条，基线文件 {len(BASELINE)} 个）："
          f"界面中文 {len(all_rows)} 处 / 基线 {sum(BASELINE.values())} 处；"
          f"开发者日志中文 {len(dev_logs)} 处（不判红）")
    bad = []
    for r in sorted(set(byfile) | set(BASELINE)):
        now, base = byfile.get(r, 0), BASELINE.get(r, 0)
        if now > base:
            bad.append(f"超出基线 {now - base} 处：{r}（{now} > {base}）—— 新写的中文必须走 i18n")
        elif now < base:
            bad.append(f"低于基线 {base - now} 处：{r}（{now} < {base}）—— 还了债，把 BASELINE 调低")
        elif now:
            print(f"  = {r} 持平 {now} 处")
    if args.list:
        for r, i, kind, text in all_rows:
            print(f"{r}:{i} [{kind}] {text}")
    for line in bad:
        print("FAIL", line)
    if bad:
        return 1
    if not all_rows:
        print("PASS 全仓界面中文已零硬编码，BASELINE 可以整表删除")
        return 0
    print(f"PASS 未新增硬编码（剩余欠账 {len(all_rows)} 处，见上表）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
