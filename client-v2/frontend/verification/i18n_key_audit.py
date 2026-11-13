#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""i18n_key_audit.py —— i18n 键的**三向对账**（静态、机器可核）

为什么需要它：`t('某个没定义的键')` 不会报错，它**把键名原样显示到界面上**
（第四十四轮我自己就写出过一次 `downloads.mergeFail`，
靠 typecheck/build/静态扫描全绿混过去了 —— 那三道都不看这件事）。
同族问题第五十一轮已经证明过一次：不可见就会漂，所以把它做成门禁。

三个方向，处理方式不同：

A. **用了但没定义**（zh 或 en 任一缺）→ 判红。
   这是会直接显示成 `detail.followDone` 这种字符串的那一类。
B. **zh / en 键集不对称** → 判红。
   英文界面缺一个键 = 英文下露出键名（比露中文更难发现，因为看起来像代码）。
   这条规矩是第三十四轮立的，当时靠人核；现在靠脚本。
C. **定义了但字面量里没人用** → 只报告，不判红，
   因为其中一部分是**运行时拼键**（下面 D）在用，静态看不到；
   真死的键会一直出现在这份名单里，攒够了就该一次清掉。

D. **动态键清单**：所有 `t(...)`/`tf(...)` 里参数不是字符串字面量的位置
   （变量、三元、拼接、模板串），逐条列出 `文件:行 + 原文`。
   这个方向**不判红但必须可见**：动态键是 A/B 两道检查共同的盲区，
   而本仓确实有（`ctx.what.` 就是这么被扫出来的：界面上出现的是
   `t('ctx.what.' + suffix)` 这种拼接，字面量抽取只会得到一个残缺的
   `ctx.what.`，如果不把它归到"动态"里，我下一版就会把它当成
   "用了没定义的键"去修一个根本不存在的 bug）。

⚠ 边界（写死在这里，免得被当成更强的保证）：
  · 只解析 `zh`/`en` 两个字面量对象；键若由别处拼装或来自后端，看不见。
  · 抽取 `t('…')` 时剥掉行注释；块注释与字符串里的伪调用不管（够用，且自检盯着）。
  · 不做 TS 类型层校验（`Record<string,string>` 本来也不约束键名）。

用法：
    python3 verification/i18n_key_audit.py            # 对账（A/B 不符 exit 1）
    python3 verification/i18n_key_audit.py --json     # 机器可读
    python3 verification/i18n_key_audit.py --selftest # 解析器形状自测
"""
import argparse
import glob
import subprocess
import io
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
SRC = os.path.join(FRONTEND, "src")
I18N = os.path.join(SRC, "services", "i18n.ts")

DICT_RE = r"^const %s: Record<string, string> = \{\n(?P<body>.*?)\n\}$"
# ⚠ 第五十九轮修（子代理 D 报出、我复核确认）：字符类原来只有 `[\w.]`，**不含连字符**。
# 仓里就有活键 `source.note.huangguo-video`（i18n.ts:580 中文、:1259 英文，
# 经 components/settings/SourceCard.vue 动态取键），它既不进 zh_set/en_set、
# 也不算"用到/未用"。后果不是名单少一项，而是
# **把英文那条删掉，这道门照样全绿** —— 英文界面直接把键名显示给用户，
# 正是第 52 轮立这道门要拦的事故形，换个键名形状就绕过去了。
KEY_DEF_RE = re.compile(r"(?m)^\s{2}'([\w.\-]+)'\s*:")
LINE_COMMENT_RE = re.compile(r"(?m)^\s*//.*$")

# ⚠ 取键这件事**不能用一条正则搞定**，第一版就是栽在这里：
# 我用 `t\(\s*['"]([\w.]+)['"]` 抽字面量键，于是
#   t('ctx.what.' + suffix)
# 被抽成"用到一个叫 `ctx.what.` 的键"，直接进"用了没定义"名单 ——
# 一个假 bug（而且形状极其可信，因为那确实是个残缺的键前缀）。
# 而真正该登记为动态的那条反而没登记。
# 现在改成：先定位调用起点，再用小扫描器读出**第一个完整实参表达式**，
# 然后按形态分类：
#   纯字面量            → 字面量键（A/B 两个方向都适用）
#   字面量 + 拼接/三元等 → 动态键（能拿到前缀就记前缀）
#   变量/模板串/表达式   → 动态键
CALL_HEAD_RE = re.compile(r"(?<![\w$.])(?:tf|t)\(")
PURE_LITERAL_RE = re.compile(r"^['\"]([\w.\-]+)['\"]$")
CONCAT_PREFIX_RE = re.compile(r"^['\"]([\w.\-]+)['\"]\s*[+]")
QUOTED_ANY_RE = re.compile(r"^['\"]([\w.\-]*)['\"]")


def _first_arg(text, i):
    """从 `(` 之后读出第一个实参（到顶层 `,` 或 `)` 为止）。
    处理圆/方/花括号嵌套与单双引号内的括号；反引号（模板串）
    一律当动态键，遇到未转义的 ` 就收尾，不试图解析 ${…} 里的括号。"""
    depth = 1
    quote = ""
    out = []
    while i < len(text):
        ch = text[i]
        prev = text[i - 1] if i else ""
        if quote:
            out.append(ch)
            if ch == quote and prev != "\\":
                quote = ""
        elif ch in ("'", '"'):
            out.append(ch)
            quote = ch
        elif ch == "`":
            out.append(ch)
            # 模板串：整个表达式算动态键。这里不把内容读出来（`${}` 里可能有括号，
            # 硬解析就成了第二个真相），但**多带 40 个字符的上下文**进报告，
            # 否则人看到的只有 `t(\``，等于没告诉他在哪拼的键。
            out.append(text[i + 1:i + 41].replace("\n", "\\n"))
            break
        elif ch in "([{":
            depth += 1
            out.append(ch)
        elif ch in ")]}":
            depth -= 1
            if depth == 0:
                break
            out.append(ch)
        elif ch == "," and depth == 1:
            break
        else:
            out.append(ch)
        i += 1
    return "".join(out).strip()


def classify_usage(arg):
    """返回 ('literal', key) / ('dynamic', 前缀或'') 。"""
    m = PURE_LITERAL_RE.match(arg)
    if m:
        return "literal", m.group(1)
    m = CONCAT_PREFIX_RE.match(arg)
    if m:
        return "dynamic", m.group(1)
    m = QUOTED_ANY_RE.match(arg)
    if m and arg.strip() != m.group(0):
        # 例如 `'a.b' ? x : y`、`('a.b')`：引号开头但后面还有东西 → 仍是动态
        return "dynamic", m.group(1)
    return "dynamic", ""


def read_src_files():
    paths = glob.glob(os.path.join(SRC, "**", "*.vue"), recursive=True)
    paths += glob.glob(os.path.join(SRC, "**", "*.ts"), recursive=True)
    paths += glob.glob(os.path.join(SRC, "**", "*.tsx"), recursive=True)
    return sorted(set(paths))


def parse_dict_keys(text, name):
    # re.M 不能省：DICT_RE 里有 `^const …` 与 `^\}$`，
    # 只有 re.S 的话 `^` 就只锚**整个字符串的开头**，
    # 而真实 i18n.ts 的 `const zh:` 在第 33 行 —— 第一版就是这么匹配不到、
    # 自检一上来就炸在第一个 audit() 调用上（连一条 PASS 都没打出来）。
    m = re.search(DICT_RE % name, text, re.S | re.M)
    if not m:
        return None
    return set(KEY_DEF_RE.findall(m.group("body")))


def strip_line_comments(text):
    return LINE_COMMENT_RE.sub("", text)


KEY_SHAPE_RE = re.compile(r"^\w[\w-]*(?:\.\w[\w-]*)+$")


def parse_usages(paths, skip):
    """literal = t()/tf() 的字面量键；dynamic = 动态取键位置；
    as_data = 键形状但出现在别处的字符串字面量（把键当数据的写法）。"""
    literal, dynamic, as_data = set(), [], set()
    for path in paths:
        if os.path.abspath(path) == os.path.abspath(skip):
            continue
        raw = io.open(path, encoding="utf-8").read()
        text = strip_line_comments(raw)
        for m in CALL_HEAD_RE.finditer(text):
            arg = _first_arg(text, m.end())
            if not arg:
                continue
            kind, key = classify_usage(arg)
            if kind == "literal":
                literal.add(key)
                continue
            if re.match(r"^(?:this|window|console|Math|JSON)\.", arg):
                continue
            line = text[:m.start()].count("\n") + 1
            dynamic.append((("%s:%d: t(%s" % (os.path.relpath(path, SRC), line, arg[:70])), key))
        # 把键当数据的写法：WEEKDAYS = ['calendar.wkMon', …]、{ key: 'detail.followDone' }
        for frag in re.findall(r"['\"]([\w.-]+)['\"]", text):
            if KEY_SHAPE_RE.match(frag) and "." in frag:
                as_data.add(frag)
    return literal, dynamic, as_data


def audit():
    text = io.open(I18N, encoding="utf-8").read()
    zh = parse_dict_keys(text, "zh")
    en = parse_dict_keys(text, "en")
    if zh is None or en is None:
        raise SystemExit("i18n.ts 结构变了（找不到 const zh/en: Record<string, string>），"
                         "判据要跟着改，不能装作没键")
    literal, dynamic, as_data = parse_usages(read_src_files(), I18N)
    missing_zh = sorted(k for k in literal if k not in zh)
    missing_en = sorted(k for k in literal if k not in en)
    only_zh = sorted(zh - en)
    only_en = sorted(en - zh)
    all_defined = zh | en
    not_by_literal = sorted(all_defined - literal)
    # 三档分开，别把"静态看不见"说成"没人用"：
    #   explainable —— 键名以数据形式出现在别处（数组、map、变量名传进 t()），
    #                 方向 D 的动态取键正是靠它们工作的，删了就是界面显示键名；
    #   by_prefix   —— 完整键名从没出现过，但有动态拼接的**前缀**能拼出它
    #                 （`t('ctx.what.' + kind)` 就是这样：全名不会以字面量形态存在）；
    #   orphan      —— 两者都排除掉，才是真死候选。
    dyn_prefixes = sorted({p for _, p in dynamic if p})
    explainable = sorted(k for k in not_by_literal if k in as_data)
    rest = [k for k in not_by_literal if k not in as_data]
    by_prefix = sorted(k for k in rest if any(k.startswith(p) for p in dyn_prefixes))
    orphan = sorted(k for k in rest if not any(k.startswith(p) for p in dyn_prefixes))
    # 动态键能不能被字面量前缀解释：拿每条动态串的**字面量前缀**去撞已定义键，
    # 撞不上的只是"看不清"，不判红（判红就该我去改成静态，那是设计决定）。
    return {
        "zh": len(zh), "en": len(en),
        "used_literal": len(literal),
        "dynamic_sites": len(dynamic),
        "missing_zh": missing_zh, "missing_en": missing_en,
        "only_zh": only_zh, "only_en": only_en,
        "not_by_literal": not_by_literal,
        "explainable": explainable,
        "by_prefix": by_prefix,
        "orphan": orphan,
        "dynamic_prefixes": dyn_prefixes,
        "dynamic": dynamic,
        "dynamic_sites_list": [[site, prefix] for site, prefix in dynamic],
        "zh_set": zh, "en_set": en,
    }


# ------------------------------------------------------------------ 自检

def _selftest():
    import tempfile

    fails, counts = [], [0]

    def t(name, cond, detail=""):
        counts[0] += 1
        mark = "PASS" if cond else "FAIL"
        print("%s %s%s" % (mark, name, (" | " + str(detail)[:150]) if detail else ""))
        if not cond:
            fails.append(name)

    with tempfile.TemporaryDirectory() as d:
        src = os.path.join(d, "src")
        svc = os.path.join(src, "services")
        os.makedirs(svc)
        io.open(os.path.join(svc, "i18n.ts"), "w", encoding="utf-8").write(
            "const zh: Record<string, string> = {\n"
            "  'a.one': '一',\n"
            "  'a.two': '二',\n"
            "  'a.three': '三',\n"
            "  'unused.key': '没人用',\n"
            "  'data.only.key': '只作为数据出现',\n"
            "  'ctx.what.other': '拼接出来的',\n"
            "}\n"
            "const en: Record<string, string> = {\n"
            "  'a.one': 'one',\n"
            "  'a.two': 'two',\n"
            "}\n"
            "const dicts: Record<Locale, Record<string, string>> = { zh, en }\n"
        )
        io.open(os.path.join(src, "View.vue"), "w", encoding="utf-8").write(
            "<script setup lang=\"ts\">\n"
            "const x = () => t('a.one')\n"
            "const y = () => tf(\"a.two\", { n: 1 })\n"
            "const z = () => t('a.missing')\n"
            "// t('a.inComment') 这行是注释，不算使用\n"
            "const d1 = () => t('ctx.what.' + suffix)\n"
            "const d2 = () => t(kind)\n"
            "const d3 = () => t(`tpl.${kind}`)\n"
            "const d4 = () => t(cond ? 'a.one' : 'a.two')\n"
            "const e1 = () => t('a.three', { x: 1 })\n"
            "const e2 = () => tf('a.one', { list: ['x', 'y'] })\n"
            "const alert2 = () => alert('a.one')\n"
            "const f = () => this.t('a.two')\n"
            "const WEEK = ['data.only.key', 'a.three']\n"
            "</script>\n"
        )
        global I18N, SRC
        old_i18n, old_src = I18N, SRC
        I18N = os.path.join(svc, "i18n.ts")
        SRC = src
        try:
            res = audit()
            t("A1 用了没定义的键被抓出来", res["missing_zh"] == ["a.missing"], res["missing_zh"])
            t("A2 注释里的使用不算", "a.inComment" not in res["missing_zh"], res["missing_zh"])
            t("A3 en 缺键单独报（同一键两侧都可能缺）",
              # a.three 只在 zh 定义、又被字面量用到 → 它**同时**是"en 缺键"和"zh/en 不对称"。
              # 我第一版把这条期望写成 ["a.missing"]，是我只想着"缺失"那一面、
              # 忘了 B 方向的不对称本来就会喂给 A 方向（两方向共用一份事实才对）。
              res["missing_en"] == ["a.missing", "a.three"], res["missing_en"])
            t("B1 zh/en 不对称被抓",
              # 名单里现在是三个：a.three / unused.key / data.only.key
              # （最后一个是我为 C 档新加的假键，期望要跟着事实走，
              #  而不是为了让它绿去删掉那个测试数据）
              res["only_zh"] == ["a.three", "ctx.what.other", "data.only.key", "unused.key"],
              res["only_zh"])
            t("B2 en 多出的键也要能看见（反向差集）", res["only_en"] == [], res["only_en"])
            t("C1 真死候选里只留从没出现过的键",
              "unused.key" in res["orphan"] and "data.only.key" not in res["orphan"],
              res["orphan"])
            t("C2 键名以数据形式出现的不算死键（WEEK = ['data.only.key', …]）",
              "data.only.key" in res["explainable"], res["explainable"])
            t("C3 三档求和对得上未用全集",
              len(res["explainable"]) + len(res["by_prefix"]) + len(res["orphan"])
              == len(res["not_by_literal"]),
              (len(res["explainable"]), len(res["by_prefix"]), len(res["orphan"]),
               len(res["not_by_literal"])))
            t("C4 只以拼接前缀可能出现的键不算真死（'ctx.what.' + kind 能拼出 ctx.what.other）",
              "ctx.what.other" in res["by_prefix"], res["by_prefix"])
            t("D1 拼接动态键被识别",
              any("ctx.what." in x for x, _ in res["dynamic_sites_list"]), res["dynamic"])
            # dynamic 现在是 (位置串, 前缀) 的元组列表；上一版我按字符串集合写断言，
            # `": t(kind" in 元组` 永远 False —— 判据装置自己过期，不是产品变了。
            t("D2 变量键被登记为动态",
              any(": t(kind" in x for x, _ in res["dynamic_sites_list"]), len(res["dynamic"]))
            t("D3 alert() 不误判成 t()", not any("alert" in x for x in res["dynamic"]),
              res["dynamic"])
            t("D4 this.t() 不误判", not any("this.t" in x for x in res["dynamic"]),
              res["dynamic"])
            t("E1 字面量键计数自洽（假源码里应有 4 个唯一键）",
              res["used_literal"] == 4, res["used_literal"])
            # 扫描器的几种形状，每一个都是我真会写错的地方
            t("E3 带第二实参的字面量键正常识别（t('a.three', { x: 1 })）",
              "a.three" not in res["missing_zh"], res["missing_zh"])
            t("E4 实参里的数组/对象括号不会截断读取（tf('a.one', { list: [...] })）",
              "a.one" not in res["missing_zh"] and res["dynamic_sites"] == 4,
              res["dynamic_sites"])
            t("E5 模板串算动态键",
              any("tpl." in x for x, _ in res["dynamic_sites_list"]),
              [x for x, _ in res["dynamic_sites_list"]])
            t("E6 三元表达式算动态键（分支里的键不进字面量名单，也不误报缺失）",
              any("cond ?" in x for x, _ in res["dynamic_sites_list"]),
              [x for x, _ in res["dynamic_sites_list"]])
            t("E7 拼接键绝不当作字面量键（就是那条 ctx.what. 假 bug）",
              "ctx.what." not in res["missing_zh"], res["missing_zh"])
            t("E8 拼接键的前缀被记下来（供人工核对动态命名）",
              any(p == "ctx.what." for _, p in res["dynamic_sites_list"]),
              [p for _, p in res["dynamic_sites_list"]])
            t("E9 this.t() 与 alert() 不算取键调用",
              not any("this.t" in x or "alert" in x for x, _ in res["dynamic_sites_list"]),
              [x for x, _ in res["dynamic_sites_list"]])
            raised = False
            try:
                io.open(I18N, "w", encoding="utf-8").write("export const weird = 1\n")
                audit()
            except SystemExit:
                raised = True
            t("E2 i18n.ts 结构不认识时**直接炸**，不装作没键", raised)
        finally:
            I18N, SRC = old_i18n, old_src

    # --json 必须真的可解析：上一版这里我写成 `" %s" % x`（对二元组做格式化），
    # 直接抛 TypeError，而自检里没人调用 --json，所以这个坏路径是靠手跑才发现的。
    try:
        proc = subprocess.run([sys.executable, os.path.abspath(__file__), "--json"],
                              capture_output=True, text=True, timeout=120)
        data = json.loads(proc.stdout)
        t("F1 --json 可解析且含三档计数",
          all(k in data for k in ("explainable", "by_prefix", "orphan")),
          sorted(data.keys())[:6])
    except Exception as exc:  # noqa: BLE001
        t("F1 --json 可解析且含三档计数", False, repr(exc)[:200])

    print("\n===== i18n_key_audit --selftest: %d/%d PASS =====" % (counts[0] - len(fails), counts[0]))
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
        out = {}
        for k, v in res.items():
            if k.endswith("_set"):
                continue
            out[k] = v
        print(json.dumps(out, ensure_ascii=False, indent=1))
        return 1 if (res["missing_zh"] or res["missing_en"] or res["only_zh"] or res["only_en"]) else 0

    print("i18n：zh %d 键 / en %d 键；字面量用到 %d 个；动态取键 %d 处"
          % (res["zh"], res["en"], res["used_literal"], res["dynamic_sites"]))
    bad = False
    if res["missing_zh"] or res["missing_en"]:
        bad = True
        print("FAIL 方向 A：用到但没定义（界面上会直接把键名显示出来）")
        for k in sorted(set(res["missing_zh"]) | set(res["missing_en"])):
            side = []
            if k in res["missing_zh"]:
                side.append("zh")
            if k in res["missing_en"]:
                side.append("en")
            print("  - %s（缺 %s）" % (k, "/".join(side)))
    else:
        print("PASS 方向 A：字面量用到的键两侧都有定义")

    if res["only_zh"] or res["only_en"]:
        bad = True
        print("FAIL 方向 B：zh/en 键集不对称（缺的那侧在英文界面会露键名）")
        for k in res["only_zh"]:
            print("  - 只在 zh: %s" % k)
        for k in res["only_en"]:
            print("  - 只在 en: %s" % k)
    else:
        print("PASS 方向 B：zh/en 键集完全对称（%d 对）" % res["zh"])

    print("\n方向 C1：字面量没用到，但键名以**数据**形式出现在别处（动态取键靠它们，不能算死）：%d 个"
          % len(res["explainable"]))
    for k in res["explainable"][:8]:
        print("  - %s" % k)
    if len(res["explainable"]) > 8:
        print("  … 其余 %d 个见 --json" % (len(res["explainable"]) - 8))
    print("\n方向 C2：完整键名没出现，但能被动态拼接前缀拼出来（也不能算死）：%d 个"
          % len(res["by_prefix"]))
    for k in res["by_prefix"][:8]:
        print("  - %s" % k)
    print("  动态前缀清单：%s" % (", ".join(res["dynamic_prefixes"]) or "无"))

    print("\n方向 C3：三档都排除后剩下的（真死候选，攒够再一次清）：%d 个"
          % len(res["orphan"]))
    for k in res["orphan"][:15]:
        print("  - %s" % k)
    if len(res["orphan"]) > 15:
        print("  … 其余 %d 个见 --json" % (len(res["orphan"]) - 15))
    # 求和自证：三档必须刚好覆盖"字面量没用到的"全集，
    # 否则就是我哪一档漏了（这条是本任务第 N 次"分组要能对上总数"的规矩）。
    assert (len(res["explainable"]) + len(res["by_prefix"]) + len(res["orphan"])
            == len(res["not_by_literal"])), "C1+C2+C3 ≠ 未用全集，分组漏了"

    print("\n方向 D：动态取键的位置（A/B 的共同盲区，必须可见）")
    for site, prefix in res["dynamic_sites_list"][:10]:
        print("  - %s%s" % (site, (" → 前缀 %r" % prefix) if prefix else " → 整体是变量/表达式"))
    if res["dynamic_sites"] > 10:
        print("  … 其余 %d 处见 --json" % (res["dynamic_sites"] - 10))
    if not res["dynamic_sites"]:
        print("  （无）")

    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
