#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""palette_audit.py —— 设计 token 的对账（规格来自 v1 自己的测试）

为什么这个面值得做：本项目 v1 只有 3 个自带测试文件，
`test_palette.py` 是唯一还没当规格用过的一个 —— 而我上一轮定下的标准是
**"只有存在可交叉核对的规格来源才做这个面"**（否则期望值只有我归纳的一个来源，
正是这几轮反复踩的"我以为的形状"）。

v1 `test_palette.py` 的四条规矩（文件头还写了它为什么存在：
`accent_soft` 曾写成 `rgba(255, 59, 92, .14)`，QColor 不认 →
选中态底色变成纯黑块；QSS 却认 rgba()，所以同一个 token 一半正常一半发黑，
"很难查"）：

  1. 每个颜色 token 都必须是合法颜色；
  2. 深/浅两套的**键集合必须一致**；
  3. `*soft` 那族必须真半透明（0 < alpha < 255）；
  4. 品牌红：深色 `#ff3b5c` / 浅色 `#fe2c55`。

v2 的对应物是 `src/styles/tokens.css`（`:root/[data-theme=dark]` 与
`[data-theme=light]` 两块）。四条都能静态算，不需要起浏览器：

  A. 颜色格式合法（含 v1 那个坑的等价形态检查）；
  B. 两套键集合对称 —— 这条是最容易出真 bug 的：
     只在 dark 里定义的 token，切到 light 会**静默继承 dark 的值**
     （CSS 变量找不到就近的定义就用外层 :root 的），没有任何报错；
  C. soft 族半透明；
  D. 品牌红在 v2 改挂在 `--accent-v1`（v2 的 `--accent` 是 guoapp 蓝，
     这是有意的重新配色，文件头写明），所以 D 判的是
     "v1 的那两个值还在不在、在不在同一个 token 名下"，
     并**报告** accent 族的关系（见 F），不替人判断"该不该改成蓝"。

  F. 只报告不判红：`--selected-soft` 的色相与 `--accent` 是否同源。
     v1 的语义是"选中底色 = 品牌色 15% 透明"；v2 里
     `--accent` 已经换成蓝，而 `--selected-soft` 仍是红色系 ——
     这是**需要人来定的配色决定**（跟 v1 一致？还是跟 v2 的新主色一致？），
     所以我把它列出来而不是"顺手修对"。

用法：
    python3 verification/palette_audit.py            # 对账（A/B/C/D 不符 exit 1）
    python3 verification/palette_audit.py --json     # 机器可读
    python3 verification/palette_audit.py --selftest # 解析器形状自测
"""
import argparse
import glob
import io
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
TOKENS = os.path.join(FRONTEND, "src", "styles", "tokens.css")

# v1 test_palette.py 里的两个品牌红值
V1_ACCENT = {"dark": "#ff3b5c", "light": "#fe2c55"}
SOFT_INFIXES = ("soft",)

HEX6 = re.compile(r"^#[0-9a-fA-F]{6}$")
HEX8 = re.compile(r"^#[0-9a-fA-F]{8}$")
RGB_TRIPLE = re.compile(r"^rgb\(\s*(\d{1,3})\s+(\d{1,3})\s+(\d{1,3})\s*\)$")
RGBA_FN = re.compile(r"^rgba\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)\s*\)$")
NAMED = re.compile(r"^(transparent|currentColor|white|black|red|green|blue)$")


# 引用形态分两种（第五十九轮自纠）：
#   var(--x)         → 没定义时**整条声明失效**，元素拿继承值、不报错 → 真 bug
#   var(--x, #abc)   → 带回退值，是故意的降级，不该被说成"静默没样式"
# 第一版把两种混成一份名单，差点把 CreatorView 的真 bug 与
# LibraryView 的有意回退说成同一件事。
VAR_REF_RE = re.compile(r"var\(\s*(--[\w-]+)\s*(,)?")


def parse_usage(root):
    """扫组件与样式里**引用**了哪些 token（`var(--x)`）。

    ⚠ 为什么要加这一维（第五十九轮的真实教训）：
    我第一次跑这个审计时，据"色相不匹配"报了一条
    「v2 主强调色换成蓝、而选中底还是品牌红 → 视觉不一致」。
    一听就有道理，但**我没查这两个 token 到底有没有被用过**：
    实测 `--selected-soft` 与 `--accent-v1` 在整个 src 里**引用 0 次**
    （`--accent` 55 次、`--accent-soft` 11 次）——
    所以根本不存在"界面上一半蓝一半红"，那只是一对**没人用的死 token**。
    算出来"关系不对"就当成问题，是我这几轮反复犯的同一个错：
    **从计算信号直接跳到结论，中间缺一次消费侧核对。**
    """
    refs = {}   # token → [(出处, 有没有回退值), …]
    files = []
    for pat in ("**/*.vue", "**/*.css", "**/*.ts"):
        files += [f for f in glob.glob(os.path.join(root, pat), recursive=True)
                  if "styles/tokens.css" not in f]
    for path in files:
        text = io.open(path, encoding='utf-8').read()
        # 三种注释都要剥：CSS 块注释、HTML 注释、JS 行注释。
        # ⚠ 第一版只剥了 CSS 那种，于是 `<!-- var(--x) -->` 与 `// var(--y)`
        #   里的引用被当成真引用 —— 与第五十六轮端口提取器"把行尾注释当声明"
        #   是同一族错（那次是误收端口，这次会误收引用）。
        #   自检里那条 `--in-comment-should-still-count-or-not` 就是为这个准备的。
        text = re.sub(r"/\*.*?\*/", "", text, flags=re.S)
        text = re.sub(r"<!--.*?-->", "", text, flags=re.S)
        text = re.sub(r"(?m)^\s*//.*$", "", text)
        # 行尾 JS 注释也要剥：`const x = 1 // var(--in-js-comment)`
        # 上一版只剥了整行注释，H3 就是因此红的（我的自检测到了自己漏的形状）。
        # 判断"这个 // 是不是注释"用引号奇偶：行内引号为偶数时，其后的 // 不可能是 URL 的一部分。
        lines = []
        for ln in text.split("\n"):
            idx = ln.find("//")
            if idx >= 0 and ln[:idx].count('"') % 2 == 0 and ln[:idx].count("'") % 2 == 0:
                ln = ln[:idx]
            lines.append(ln)
        text = "\n".join(lines)
        for m in VAR_REF_RE.finditer(text):
            refs.setdefault(m.group(1), []).append(
                (os.path.relpath(path, root), bool(m.group(2))))
    return refs, len(files)


def split_blocks(text):
    """取出 `:root,[data-theme=dark] {…}` 与 `[data-theme=light] {…}` 两块。"""
    blocks = {}
    for m in re.finditer(r"(?P<sel>[^{}]*data-theme[^{}]*)\{(?P<body>[^{}]*)\}", text):
        sel = m.group("sel")
        name = "light" if "light" in sel else "dark"
        blocks[name] = m.group("body")
    if set(blocks) != {"dark", "light"}:
        raise SystemExit("tokens.css 结构变了（找不到 dark/light 两块），判据要跟着改")
    return blocks


def parse_tokens(body):
    out = {}
    for m in re.finditer(r"--([\w-]+)\s*:\s*([^;]+);", body):
        out["--" + m.group(1)] = m.group(2).strip()
    return out


def is_colorish(name, value):
    """尺寸/圆角/时长这类不算颜色（v1 也是这么排除 radius 的）。"""
    skip = ("radius", "z-index", "duration", "size", "width", "height", "border-width", "gap")
    if any(k in name for k in skip):
        return False
    if re.match(r"^-?[\d.]+(px|em|rem|ms|s|deg|fr)?$", value):
        return False
    return True


def alpha_of(name, value):
    """返回 0..1 的 alpha；非颜色返回 None。"""
    if HEX8.match(value):
        return int(value[7:9], 16) / 255.0
    if HEX6.match(value):
        return 1.0
    m = RGBA_FN.match(value)
    if m:
        return float(m.group(4))
    if RGB_TRIPLE.match(value) or NAMED.match(value):
        return 1.0
    return None


def valid_color(value):
    if HEX6.match(value) or HEX8.match(value):
        return True
    m = RGB_TRIPLE.match(value)
    if m:
        return all(0 <= int(g) <= 255 for g in m.groups())
    if RGBA_FN.match(value):
        return True
    if NAMED.match(value):
        return True
    if value.startswith("var(") or value.startswith("color-mix(") or value.startswith("hsl"):
        return True
    return False


def audit(path=TOKENS, src_root=None):
    text = io.open(path, encoding="utf-8").read()
    blocks = split_blocks(text)
    tok = {k: parse_tokens(v) for k, v in blocks.items()}
    res = {
        "dark_n": len(tok["dark"]),
        "light_n": len(tok["light"]),
        "only_dark": sorted(set(tok["dark"]) - set(tok["light"])),
        "only_light": sorted(set(tok["light"]) - set(tok["dark"])),
        "bad_format": [],
        "opaque_soft": [],
        "fully_clear_soft": [],
        "qt_hostile": [],
        "brand": {},
        "soft_hues": {},
    }
    for theme in ("dark", "light"):
        for name, value in tok[theme].items():
            if not is_colorish(name, value):
                continue
            if not valid_color(value):
                res["bad_format"].append("%s/%s=%r" % (theme, name, value))
            # v1 那个坑的等价形态：CSS 里 rgba() 合法，但 ".14" 这种省零写法
            # 在 CSS 也合法 —— 真正会踩的是 Qt 侧。这里只登记风格不一致，
            # 因为 v2 全仓约定用 #RRGGBBAA，混进 rgba() 会让"同一个 token 两种写法"。
            if RGBA_FN.match(value):
                res["qt_hostile"].append("%s/%s=%r（仓内约定是 #RRGGBBAA）" % (theme, name, value))
            if any(i in name for i in SOFT_INFIXES):
                a = alpha_of(name, value)
                if a is None:
                    res["bad_format"].append("%s/%s 解析不出 alpha" % (theme, name))
                elif a >= 1.0:
                    res["opaque_soft"].append("%s/%s=%r" % (theme, name, value))
                elif a <= 0.0:
                    res["fully_clear_soft"].append("%s/%s=%r" % (theme, name, value))
        # 品牌红是否还在（v2 挂在 --accent-v1）
        res["brand"][theme] = {
            "v1 期望(accent)": V1_ACCENT[theme],
            "v2 --accent": tok[theme].get("--accent"),
            "v2 --accent-v1": tok[theme].get("--accent-v1"),
        }
        # F：选中底的色相 vs 主强调色
        sel = tok[theme].get("--selected-soft", "")
        acc = tok[theme].get("--accent", "")
        accv1 = tok[theme].get("--accent-v1", "")
        hue = sel[:7].lower()
        res["soft_hues"][theme] = {
            "selected-soft": sel,
            "hue": hue,
            "等于 --accent": hue == acc.lower(),
            "等于 --accent-v1": hue == accv1.lower(),
        }
    # G. 引用面（死 token）／H. 引用了没定义的 token（更危险：静默没有样式）
    refs, n_files = parse_usage(src_root or os.path.join(FRONTEND, "src"))
    defined = set(tok["dark"]) | set(tok["light"])
    res["usage_files"] = n_files
    res["unused_tokens"] = sorted(d for d in defined if d not in refs
                                  and not any(k in d for k in ("radius", "z-index")))
    undef = sorted(r for r in refs if r not in defined)
    # 按**引用点**分档：只要有一个点没写回退值，那个点就是静默失效。
    # （上一版我按 token 求并集，于是 `--line` 因为有别处写了回退就被整体归成
    #   "故意的降级" —— 真 bug 被并集吞掉，这比漏报更隐蔽。）
    res["undefined_no_fallback"] = sorted(
        r for r in undef if any(not fb for _site, fb in refs[r]))
    res["undefined_with_fallback"] = sorted(
        r for r in undef if all(fb for _site, fb in refs[r]))
    res["undefined_refs"] = undef
    res["undefined_ref_sites"] = {
        r: ["%s%s" % (site, "（有回退）" if fb else "（无回退）")
            for site, fb in refs[r][:4]]
        for r in undef}

    brand_ok = all(
        (res["brand"][t]["v2 --accent-v1"] or "").lower() == V1_ACCENT[t]
        for t in ("dark", "light")
    )
    res["brand_red_preserved"] = brand_ok
    res["tokens"] = tok
    return res


# ------------------------------------------------------------------ 自检

def _selftest():
    import tempfile

    fails, counts = [], [0]

    def t(name, cond, detail=""):
        counts[0] += 1
        print("%s %s%s" % ("PASS" if cond else "FAIL", name, (" | " + str(detail)[:180]) if detail else ""))
        if not cond:
            fails.append(name)

    css_ok = (
        ':root,\n[data-theme="dark"] {\n'
        "  --radius: 8px;\n  --bg: #0f1117;\n  --accent: #4a83ff;\n"
        "  --accent-soft: #4a83ff26;\n  --accent-v1: #ff3b5c;\n"
        "  --selected-soft: #ff3b5c24;\n}\n"
        '[data-theme="light"] {\n'
        "  --radius: 8px;\n  --bg: #f5f5f8;\n  --accent: #4a83ff;\n"
        "  --accent-soft: #4a83ff26;\n  --accent-v1: #fe2c55;\n"
        "  --selected-soft: #ff3b5c24;\n}\n"
    )
    with tempfile.TemporaryDirectory() as d:
        good = os.path.join(d, "t.css")
        io.open(good, "w", encoding="utf-8").write(css_ok)
        r = audit(good)
        t("A1 全部合法颜色时不报任何格式问题", r["bad_format"] == [], r["bad_format"])
        t("B1 键集合一致时两侧差集为空", not r["only_dark"] and not r["only_light"],
          (r["only_dark"], r["only_light"]))
        t("C1 soft 族半透明 → 不报全实色", not r["opaque_soft"] and not r["fully_clear_soft"],
          (r["opaque_soft"], r["fully_clear_soft"]))
        t("D1 品牌红在 --accent-v1 上保留（v1 的 test_brand_colour_matches_the_reference_client）",
          r["brand_red_preserved"] is True, r["brand"])
        t("F1 选中底色相与两个 accent 的关系被算出来（不替人判断）",
          r["soft_hues"]["dark"]["等于 --accent"] is False
          and r["soft_hues"]["dark"]["等于 --accent-v1"] is True,
          r["soft_hues"]["dark"])

        # 四种坏形状，逐个必须被抓出来（v1 的四条规矩各对应一种）
        b1 = css_ok.replace("  --bg: #0f1117;", "  --bg: #0f117;")  # 5 位十六进制
        p = os.path.join(d, "b1.css")
        io.open(p, "w", encoding="utf-8").write(b1)
        t("A2 非法色值必须报（v1 规矩 1：QColor 认得的格式）",
          audit(p)["bad_format"] != [], audit(p)["bad_format"])

        b2 = css_ok.replace('  --accent-v1: #fe2c55;\n', '')  # light 少一个键
        p = os.path.join(d, "b2.css")
        io.open(p, "w", encoding="utf-8").write(b2)
        r2 = audit(p)
        t("B2 只在 dark 定义的键必须报（v1 规矩 2）",
          r2["only_dark"] == ["--accent-v1"], r2["only_dark"])

        b3 = css_ok.replace("--accent-soft: #4a83ff26", "--accent-soft: #4a83ff")  # alpha 满
        p = os.path.join(d, "b3.css")
        io.open(p, "w", encoding="utf-8").write(b3)
        t("C2 soft 写成全不透明必须报（v1 那次黑块的成因形状）",
          len(audit(p)["opaque_soft"]) == 2, audit(p)["opaque_soft"])

        b4 = css_ok.replace("--accent-v1: #ff3b5c", "--accent-v1: #ff3b5d")
        p = os.path.join(d, "b4.css")
        io.open(p, "w", encoding="utf-8").write(b4)
        t("D2 品牌红值被改动必须报（v1 规矩 4）",
          audit(p)["brand_red_preserved"] is False, audit(p)["brand"])

        b5 = css_ok.replace("--accent-soft: #4a83ff26", "--accent-soft: rgba(74, 131, 255, .14)")
        p = os.path.join(d, "b5.css")
        io.open(p, "w", encoding="utf-8").write(b5)
        t("E1 混进 rgba() 写法要登记（仓内约定 #RRGGBBAA，v1 的坑就在两种写法不一致）",
          len(audit(p)["qt_hostile"]) == 2, audit(p)["qt_hostile"])

        # radius 这类尺寸不能被判成颜色（v1 也是这么排除的）
        t("E2 radius 不当颜色检查（v1 test_every_color_token 里也 continue 掉 radius）",
          all("radius" not in x for x in audit(good)["bad_format"]), audit(good)["bad_format"])

        # 结构变了要主动炸，而不是"没键可查所以通过"
        p = os.path.join(d, "b6.css")
        io.open(p, "w", encoding="utf-8").write(".foo { --bg: #ffffff; }\n")
        raised = False
        try:
            audit(p)
        except SystemExit:
            raised = True
        t("E3 tokens.css 结构不认识时**直接炸**", raised)

        # G/H 两维各正反一条
        css_u = css_ok.replace("--selected-soft: #ff3b5c24;", "--selected-soft: #ff3b5c24;\n  --never-used: #123456;")
        p = os.path.join(d, "usage.css")
        # 同一个内容写两处：`p` 是给 audit(path=…) 读的 token 文件，
        # src/styles/tokens.css 是给真实仓库默认路径用的
        io.open(p, "w", encoding="utf-8").write(css_u)
        os.makedirs(os.path.join(d, "src", "styles"), exist_ok=True)
        io.open(os.path.join(d, "src", "styles", "tokens.css"), "w", encoding="utf-8").write(css_u)
        os.makedirs(os.path.join(d, "src", "components"), exist_ok=True)
        io.open(os.path.join(d, "src", "components", "X.vue"), "w", encoding="utf-8").write(
            "<style scoped>\n.a { color: var(--bg); }\n"
            ".b { color: var(--accent-soft); }\n"
            ".c { color: var(--ghost-token); }\n"
            ".d { color: var(--fallback-token, #777); }\n</style>\n"
            "<!-- var(--in-comment-should-still-count-or-not) -->\n"
            "<script>\nconst x = 1 // var(--in-js-comment)\n</script>\n")
        ru = audit(p, src_root=os.path.join(d, "src"))
        t("H1 引用了没定义的 token 必须报（静默没样式，比死 token 危险）",
          "--ghost-token" in ru["undefined_no_fallback"], ru["undefined_no_fallback"])
        # 有回退与没回退必须分档：混了就等于把真 bug 和有意降级说成同一件事
        t("H2b 带回退值的引用不算静默失效（分档判据）",
          "--fallback-token" not in ru["undefined_no_fallback"]
          and "--fallback-token" in ru["undefined_with_fallback"],
          (ru["undefined_no_fallback"], ru["undefined_with_fallback"]))
        # 出处现在带（无回退/有回退）标注：期望必须跟着我自己加的实现走。
        # 记一笔：上一版提交信息里我写"自检 21/21"，而这条当时还是旧期望、
        # 实际 20/21 —— 修工具的补丁没落地（assert 提前失败、文件没写回），
        # 我却照记忆写了结论。这就是本任务一直抓的"声明与事实不符"，发生在我自己身上。
        t("H2 报了要给出处（并标出那一点有没有回退值）",
          ru["undefined_ref_sites"].get("--ghost-token") == ["components/X.vue（无回退）"]
          and "（有回退）" in str(ru["undefined_ref_sites"].get("--fallback-token")),
          ru["undefined_ref_sites"])
        t("G1 定义了没人用的 token 进死名单",
          "--never-used" in ru["unused_tokens"], ru["unused_tokens"][:6])
        t("G2 被引用的不算死", "--bg" not in ru["unused_tokens"], ru["unused_tokens"][:6])
        t("G3 radius 一类不算死 token（v1 也排除它）",
          not any("radius" in x for x in ru["unused_tokens"]), ru["unused_tokens"][:6])
        t("H3 注释里的 var(--…) 不算引用（HTML 与 JS 行注释都要剥）",
          "--in-comment-should-still-count-or-not" not in ru["undefined_refs"]
          and "--in-js-comment" not in ru["undefined_refs"],
          ru["undefined_refs"])

    # 真实文件至少要有一百多个 token（防我写出恒空解析）
    r = audit()
    t("G1 真实 tokens.css 抓到 > 60 个 token", r["dark_n"] > 60, r["dark_n"])
    t("G2 真实文件两侧 token 数相等", r["dark_n"] == r["light_n"], (r["dark_n"], r["light_n"]))

    print("\n===== palette_audit --selftest: %d/%d PASS =====" % (counts[0] - len(fails), counts[0]))
    return 1 if fails else 0


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--selftest", action="store_true")
    args = ap.parse_args()
    if args.selftest:
        return _selftest()

    res = audit()
    if args.json:
        out = {k: v for k, v in res.items() if k != "tokens"}
        print(json.dumps(out, ensure_ascii=False, indent=1))
        bad = bool(res["bad_format"] or res["only_dark"] or res["only_light"]
                   or res["opaque_soft"] or res["fully_clear_soft"]
                   or res["undefined_no_fallback"]
                   or not res["brand_red_preserved"])
        return 1 if bad else 0

    print("token：dark %d 个 / light %d 个" % (res["dark_n"], res["light_n"]))
    bad = False

    if res["bad_format"]:
        bad = True
        print("FAIL 规矩 1：有 token 不是合法颜色：%s" % "; ".join(res["bad_format"][:6]))
    else:
        print("PASS 规矩 1：所有颜色 token 格式合法")

    if res["only_dark"] or res["only_light"]:
        bad = True
        print("FAIL 规矩 2：两套主题的键不对称 —— "
              "只在 dark 的 %s 切到浅色会**静默继承 dark 的值**（CSS 变量就近取值，不报错）"
              % (res["only_dark"] or "无"))
        print("     只在 light 的 %s" % (res["only_light"] or "无"))
    else:
        print("PASS 规矩 2：深/浅两套键集合完全一致")

    if res["opaque_soft"] or res["fully_clear_soft"]:
        bad = True
        print("FAIL 规矩 3：soft 族没有真半透明 —— 全实色 %s / 全透明 %s"
              "（v1 那次是反过来：rgba() 在 QColor 里解析成纯黑）"
              % (res["opaque_soft"], res["fully_clear_soft"]))
    else:
        print("PASS 规矩 3：soft 族都带 alpha 且在 (0,1) 内")

    if not res["brand_red_preserved"]:
        bad = True
        print("FAIL 规矩 4：v1 的品牌红不再对得上 —— %s"
              "（v2 把它挂在 --accent-v1，--accent 是 guoapp 蓝，这是有意的重新配色）"
              % json.dumps(res["brand"], ensure_ascii=False))
    else:
        print("PASS 规矩 4：v1 品牌红仍在，挂在 --accent-v1（dark #ff3b5c / light #fe2c55）；"
              "v2 的 --accent 是 guoapp 蓝，属有意的重新配色")

    if res["qt_hostile"]:
        print("\n登记：混用 rgba() 写法的 token（约定是 #RRGGBBAA）：")
        for x in res["qt_hostile"][:6]:
            print("  - %s" % x)

    if res["undefined_no_fallback"]:
        bad = True
        print("\nFAIL 引用了没定义、且没有回退值的 token —— "
              "CSS 变量找不到定义时整条声明失效（元素拿继承值，不报任何错）：")
        for r in res["undefined_no_fallback"]:
            print("  - %s ← %s" % (r, ", ".join(res["undefined_ref_sites"][r])))
    if res["undefined_with_fallback"]:
        print("\n只报告：引用了没定义但带回退值的 token（故意的降级，不是静默失效；"
              "仍该补进 tokens.css，否则退配色就散在组件里）：")
        for r in res["undefined_with_fallback"]:
            print("  - %s ← %s" % (r, ", ".join(res["undefined_ref_sites"][r][:3])))

    print("\n只报告：定义了却没被引用的 token（死 token；%d 个）"
          % len(res["unused_tokens"]))
    for x in res["unused_tokens"][:10]:
        print("  - %s" % x)
    if len(res["unused_tokens"]) > 10:
        print("  … 其余 %d 个见 --json" % (len(res["unused_tokens"]) - 10))

    print("\n只报告不判红（配色是决定，不是我顺手改）：")
    for theme, info in res["soft_hues"].items():
        print("  %s  --selected-soft=%s  色相=%s  与 --accent 同源=%s  与 --accent-v1 同源=%s"
              % (theme, info["selected-soft"], info["hue"],
                 info["等于 --accent"], info["等于 --accent-v1"]))
    print("  ⚠ v1 的语义是「选中底色 = 品牌色 15% 透明」；v2 主强调色已换成蓝，"
          "而选中底仍是红色系 —— 跟 v1 还是跟 v2 新主色，这是配色决定，登记为待决。")

    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
