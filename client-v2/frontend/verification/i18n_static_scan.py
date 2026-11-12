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
    "components/NavRail.vue": [
        ('>真<', "品牌字标（logo 位），专有名不进字典"),
        ('>真果鉴<', "产品专有名，同 SettingsView 的关于页一行"),
    ],
    "components/PosterGrid.vue": [
        ("|| '剧'", "无标题时的首字占位，取的是数据串（v1 CoverLoader 同语义）"),
    ],
    "components/SearchGroups.vue": [
        ("|| '剧'", "同上：首字占位兜底，不是文案"),
    ],
    "views/SettingsView.vue": [
        ("label: '中文'", "语言选择项按惯例显示各自母语名（另一项是 English），切语言也不该翻"),
        ('真果鉴 {{ appVersionText }}', "产品专有名 + 版本号，不进字典"),
    ],
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
}


# ── 棘轾（ratchet）──
# 本工具诞生时（2026-11-12）全仓还有 47 处未走 i18n 的界面中文，分布在 10 个文件。
# 与其让门禁长期红（红的门禁等于没门禁），这里按文件钉住**当前欠账数**：
#   · 某文件违规数 > 基线 → 红（新代码不许再加硬编码中文）
#   · 某文件违规数 < 基线 → 红（还了债就必须回来把数字调低，防止基线悄悄失真）
# 还完一个文件就从表里删掉它。改动这张表的唯一合法时机是「顺手少了几处硬编码」。
# 2026-11-12 当晚：最后 7 个文件还清或归位完毕，基线整表清空。
# 此后是硬门禁——新的界面中文必须进 services/i18n.ts；
# 例外只能写进 EXEMPT_REASONS 并写明原因（品牌名、数据首字、语言母语名）。
BASELINE: dict = {}


# ───────────────────────── .ts 一侧（R9c）─────────────────────────
# .vue 之外，stores/services 也在造展示文案（toast、状态标签、诊断文本），
# 此前完全不在覆盖内 —— 本轮实测 88 行中文串字面量散在 11 个 .ts 里。
# 判据与 .vue 同源：块注释/行注释不算；console.* 归开发者日志；
# 真数据/算法用的中文（不是界面）进 TS_EXEMPT 并写明原因。

TS_SKIP_FILES = {
    "services/i18n.ts",      # 字典本体
    "services/fixtures.ts",  # VITE_MOCK 假数据（剧名/简介/标签是内容）
}

# 不是界面文案的 .ts 中文：(子串, 原因)
STATUS_REASON = ("watchstore 行状态枚举，逐字对齐 Go 核心（store.go:331/964）；"
                 "前端只做镜像且无视图渲染 row.status，改译属跨层契约变更")

DIAG_REASON = ("诊断/故障串：给开发者与控制台读的，不是用户界面文案"
               "（前缀 mock:/player:/[sim_fault] 是刻意的来源标记）")

TS_EXEMPT = {
    "services/covers.ts": [
        ("封面诊断", "「复制诊断」进剪贴板的报告正文，格式与 v1 一致，给开发者读；"
                     "用户可见的那句横幅文案走 i18n（covers.banner.partial/partialN）"),
        ("缺ffmpeg", "同上：诊断报告里的分类标签"),
        ("转码/网络", "同上：诊断报告里的分类标签"),
        ("失败 ${d.failCount}", "同上：诊断报告计数行"),
        ("最近：[", "同上：诊断报告里「最近一次失败」行，带 [kind] 机器标签"),
    ],
    "stores/searchAll.ts": [
        ("[sim_fault]", "验证用故障注入的标记串（配合 __zgjSearchFault），带 [sim_fault] "
                        "后缀正是给 cleanErrorMessage 剥掉用的，不给用户看"),
    ],
    # watchstore 的行状态是 **Go 核心契约**，不是界面文案：
    # client-v2/internal/watchstore/store.go:331 声明 想看/在看/已看/预约中，
    # :964 直接 `row["status"] = "预约中"`；v1 python watchstore.py:224 同形。
    # 前端（stores/watch.ts 的乐观更新镜像、api.ts mock 面）只是照着核心形状写，
    # 且**没有任何视图渲染 row.status**（ReserveView 显示的是加入时间与看到集数）。
    # 把它翻成英文属于跨层契约变更，本机没有 Go 工具链，不能改。
    "stores/watch.ts": [("'在看'", STATUS_REASON), ("'预约中'", STATUS_REASON)],

    # 同 watchstore：这里是**Go 核心契约的状态枚举**，不是界面文案
    # （v1 main.py:780-782 的 `status in ("想看","在看","预约中")` 逐字端口）。
    # 后台检查靠它挑行，翻成英文就等于改跨层契约。
    "services/followCheck.ts": [
        ("'想看'", STATUS_REASON), ("'在看'", STATUS_REASON), ("'预约中'", STATUS_REASON),
    ],

    "services/series.ts": [
        ("'季'", "多季标题解析/归并算法里的中文后缀常量（v1 series.py 1:1 端口），"
                "处理的是剧名本身，不是界面文案。条款收窄成带引号字面量，"
                "免得以后任何含「季」的新代码被这条静默豁免"),
        ("'十'", "同上：中文数字表项（同样收窄为带引号字面量）"),
        ("'百'", "同上"),
    ],
    "services/api.ts": [
        ("'在看'", STATUS_REASON),
        ("'预约中'", STATUS_REASON),
        ("滑块</text>", "mock 验证码 SVG 画布里的图形文字，VITE_MOCK 专用素材"),
        ("未检测到 Wails 原生桥", "启动诊断文案，进 console 与降级提示；本轮先登记，见 notes R9c"),
        ("尚未打开播放会话", DIAG_REASON),
        ("mock: 全部候选失败", DIAG_REASON),
        ("mock: 请先完成下载", DIAG_REASON),
        ("mock: zip 资产请手动解压", DIAG_REASON),
        ("原生方法 ${method} 不存在", "桥诊断（带方法名），开发者可见错误，不进正常 UI"),
        # mock 面照 Go 复刻的**日志串**：界面按 ApiError.code（local_missing/outside_root）
        # 从 i18n 取句子，这两句中文永远只进 console/日志，不进 DOM。
        ("本地文件已不存在", "mock 与 Go `PlayerOpenLocal` 同形的日志串；界面文案走 player.localMissing（按 code 取）"),
        ("只允许播放下载目录下的成品文件", "同上，对应 Go 的 [outside_root]；界面走 player.localOutside"),
        # 同一族：mock 的失败注入缝。句子只进 ApiError.message（日志/诊断），
        # 界面按 catch 分支显示 i18n 的 downloads.saveDanmakuFail。
        ("mock 保存弹幕开关失败", "VITE_MOCK 失败注入的日志串；用户看到的是 downloads.saveDanmakuFail 那句"),
        ("mock detail 故障注入", "VITE_MOCK 专用的故障注入标记：只作为 throw 的 message 存在，"
                               "而 followCheck 的失败分支只统计条数、不把异常文案显示给用户（v1 也是"
                               "「失败不打扰用户」）；界面能看到的只有 i18n 里的「追剧检查：N 部暂时取不到更新」。"),
    ],
    "services/coverSave.ts": [
        ("<safe_name(title)>", "v1 契约注释里的文件名模板（对齐 decrypt.safe_name），不是文案"),
        ('"短剧"', "v1 safe_name 的 fallback 参数逐字对齐（main.py:1495），改了会偏离 v1 落盘名"),
        ("海报图片", "File System Access 类型描述，随 locale 的意义弱；本轮先登记"),
        ("海报.jpg", "落盘文件名后缀，逐字对齐 v1 main.py:1496 `<safe_name(title)> 海报.jpg`，"
                     "翻成英文会偏离 v1/核心的落盘名约定"),
    ],
}

# .ts 一侧欠账表（R9c 建立）。规则同 .vue：多一处红、少一处也必须回来调低。
# 重新生成用 `--rebase`，它按当前实测打印可粘贴的字典。
TS_BASELINE: dict = {
    # 2026-11-12 R9c-2 收尾：api / calendar / covers / coverSave / searchAll 全部
    # 「键化」或「带理由豁免」，无未处置欠账。新的 .ts 界面中文会直接红。
}


def scan_ts_file(path):
    rows = []
    devlog = []
    text = open(path, encoding="utf-8").read()
    in_block = False
    for i, raw in enumerate(text.split("\n"), 1):
        line = raw
        if in_block:
            if "*/" in line:
                in_block = False
                line = line.split("*/", 1)[1]
            else:
                continue
        if "/*" in line and "*/" not in line:
            head = line.split("/*", 1)[0]
            in_block = True
            line = head
        line = re.sub(r"/\*.*?\*/", "", line)
        code = line.split("//")[0]
        st = code.strip()
        if st.startswith("*"):
            continue
        lits = [m.group(0) for m in STR_LITERAL.finditer(code) if CJK.search(m.group(0))]
        if not lits:
            continue
        r = rel(path)
        for needle, _ in TS_EXEMPT.get(r, []):
            if needle in code:
                break
        else:
            if re.search(r"\bconsole\.\w+\s*\(", code):
                devlog.append((r, i, lits[0][:60]))
            else:
                rows.append((r, i, "ts", code.strip()[:120]))
    return rows, devlog


def scan_ts():
    rows, devlog = [], []
    for root, dirs, files in os.walk(SRC):
        dirs[:] = [d for d in dirs if d not in ("node_modules",)]
        for f in files:
            if not f.endswith(".ts") or f.endswith((".d.ts", ".test.ts")):
                continue
            p = os.path.join(root, f)
            if rel(p) in TS_SKIP_FILES:
                continue
            a, b = scan_ts_file(p)
            rows.extend(a)
            devlog.extend(b)
    return rows, devlog


def _assert_no_duplicate_keys():
    """字典字面量里的重复键会被 Python 静默「后写覆盖前写」——
    上一轮我就这么把两条已登记的豁免悄悄吞掉了。这里用 ast 自查，撞了就红。"""
    import ast as _ast
    tree = _ast.parse(open(__file__, encoding="utf-8").read())
    bad = []
    for node in tree.body:
        if isinstance(node, _ast.Assign) and isinstance(node.value, _ast.Dict):
            names = [k.value for k in node.value.keys if isinstance(k, _ast.Constant)]
            dup = sorted({n for n in names if names.count(n) > 1})
            if dup:
                bad.append(f"{node.targets[0].id}: {dup}")
    if bad:
        raise SystemExit("FAIL 扫描器自身字典有重复键（豁免会被静默丢弃）：\n  " + "\n  ".join(bad))


def check_dict_pairs():
    """i18n.ts 的 zh/en 必须成对，且任一侧不许有重复键。

    本轮就差点把 `calendar.today` 当成新键再加一遍（值相同，TS1117 才在 typecheck 里炸），
    而"只加了 zh 忘了 en"更安静：t() 缺键会逐字回退中文，en 态就漏出一条中文，
    静态棘轾和页面断言都不一定会立刻发现。所以这里补一道与 typecheck 无关的快速检查。
    """
    import collections
    path = os.path.join(SRC, "services", "i18n.ts")
    text = open(path, encoding="utf-8").read()
    try:
        zh_blk = text[text.index("const zh:") : text.index("const en:")]
        en_blk = text[text.index("const en:") : text.index("const dicts")]
    except ValueError:
        return ["i18n.ts 结构变了（找不到 const zh / en / dicts 分界），pairs 检查失效"]
    key_re = re.compile(r"^\s*'([^']+)':", re.M)
    zh_keys, en_keys = key_re.findall(zh_blk), key_re.findall(en_blk)
    problems = []
    for name, keys in (("zh", zh_keys), ("en", en_keys)):
        dup = sorted({k for k, c in collections.Counter(keys).items() if c > 1})
        if dup:
            problems.append(f"{name} 侧有重复键 {dup}（后写覆盖前写，改了没效果）")
    only_zh = sorted(set(zh_keys) - set(en_keys))
    only_en = sorted(set(en_keys) - set(zh_keys))
    if only_zh:
        problems.append(f"只有 zh 没有 en 的键 {len(only_zh)} 个：{only_zh[:12]}")
    if only_en:
        problems.append(f"只有 en 没有 zh 的键 {len(only_en)} 个：{only_en[:12]}")
    return problems


def exemption_hits():
    """每条豁免实际覆盖多少行。0 命中说明条款过期或写错；命中数异常大要回去看是否过宽。"""
    hits = {}
    tables = (("EXEMPT_REASONS", EXEMPT_REASONS, ".vue"), ("TS_EXEMPT", TS_EXEMPT, ".ts"))
    for tname, table, suffix in tables:
        for relp, needles in table.items():
            path = os.path.join(SRC, relp)
            if not os.path.exists(path):
                for needle, _ in needles:
                    hits[(tname, relp, needle)] = -1      # 文件都不存在 = 条目失效
                continue
            text = open(path, encoding="utf-8").read()
            for needle, _ in needles:
                hits[(tname, relp, needle)] = text.count(needle)
    return hits


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
    in_js_block = False   # <script> 里的多行 /* … */（本轮修的漏判，见下）
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
        # 块注释状态机必须对**每一行**都跑，包括整行没有中文的那一行。
        # 之前它写在 `if not CJK.search(raw): continue` 后面，于是
        # 单独一行的 `/*`（没有中文）被短路跳过、in_js_block 永远开不起来，
        # 注释**里面**的代码行就被当成代码报上来 —— 我用注入负例实测到了（bad3 假阳性）。
        # 同理，`LINE_COMMENT` 会把跨行块注释的起始行整行放过，所以也必须排在它前面。
        # HTML 注释状态机：和 JS 块注释同一个毛病——起始行 `<!--` 常常没有中文，
        # 一旦排在 CJK 短路之后就永远开不起来，注释正文就被当成模板文案。
        # （`--selftest` 第一条 FAIL 就是它，见下方形状用例。）
        if in_template and not in_script:
            if in_html_comment:
                if "-->" in raw:
                    in_html_comment = False
                    raw = raw.split("-->", 1)[1]
                else:
                    continue
            elif "<!--" in raw and "-->" not in raw:
                in_html_comment = True
                raw = raw.split("<!--", 1)[0]
        if in_script:
            if in_js_block:
                if "*/" in raw:
                    in_js_block = False
                    raw = raw.split("*/", 1)[1]   # 注释结束后剩下的代码照常审
                else:
                    continue                       # 纯注释内容，不算界面文案
            elif "/*" in raw and "*/" not in raw:
                in_js_block = True
                raw = raw.split("/*", 1)[0]       # 注释之前的那截仍要审
        if not CJK.search(raw):
            continue
        if LINE_COMMENT.match(raw):
            continue
        if in_template:
            # 跨行注释的状态已在上面统一处理，这里只吃同行 <!--…-->
            stripped = re.sub(r"<!--.*?-->", "", raw)
            if not CJK.search(stripped):
                continue
            kind = "template"
        elif in_script:
            # 块注释状态已在上面处理完，这里只管单行：同行 /*…*/ 就地吃掉、去掉 // 之后
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


# ─────────────────────────── 扫描器自测 ───────────────────────────
#
# 为什么要有：本轮我把 <script> 的块注释处理**连错两次**——
# 第一次只改 elif 分支没动判定顺序（假阳性照旧），第二次把状态机放进
# `if not CJK.search(raw)` 之后（单行 `/*` 没有中文，被短路跳过，
# 于是注释**内部**的代码行又被当成代码）。两次都是"改了但没验证到形状"。
# 这类东西靠肉眼看 diff 是看不出来的，只能把形状做成用例。
VUE_SHAPES = [
    # (说明, 片段, 期望命中数)
    ("模板里的中文 = 界面文案", '<template>\n  <div>加载中</div>\n</template>\n', 1),
    ("跨行 HTML 注释里的中文不算", '<template>\n  <!--\n  说明：加载中\n  -->\n  <div>ok</div>\n</template>\n', 0),
    ("<script> 里的字符串字面量算", '<script setup>\nconst a = \'加载中\'\n</script>\n', 1),
    ("同行 /*…*/ 之后的字符串仍算", '<script setup>\nconst a = 0 /* 说明 */ \'加载中\'\n</script>\n', 1),
    ("跨行块注释**内部**不算", '<script setup>\n/*\nconst a = \'加载中\'\n*/\n</script>\n', 0),
    ("块注释结束后恢复审代码", '<script setup>\n/*\n说明\n*/\nconst a = \'加载中\'\n</script>\n', 1),
    ("起始行没有中文也要开状态", '<script setup>\n/*\nconst a = \'加载中\'\n*/\nconst b = \'真的\'\n</script>\n', 1),
    ("console 里的中文归开发者日志", '<script setup>\nconsole.warn(\'调试中文\')\n</script>\n', 0),
]
TS_SHAPES = [
    ("ts：字符串字面量算", 'export const x = \'加载中\'\n', 1),
    ("ts：跨行块注释内不算", '/*\nconst a = \'加载中\'\n*/\nexport const b = 1\n', 0),
    ("ts：起始行无中文也要开状态", '/*\nconst a = \'加载中\'\n*/\nexport const b = \'真的\'\n', 1),
]


def selftest():
    import tempfile

    bad = 0
    total = 0
    for kind, shapes in (("vue", VUE_SHAPES), ("ts", TS_SHAPES)):
        for desc, frag, want in shapes:
            total += 1
            with tempfile.TemporaryDirectory() as td:
                fn = "a." + ("vue" if kind == "vue" else "ts")
                path = os.path.join(td, fn)
                with open(path, "w", encoding="utf-8") as fh:
                    fh.write(frag)
                # .ts 走 scan_ts_file：scan_file 只认 SFC 的 template/script 区，
                # 直接喂它 .ts 会命中 0 —— 那是测试用错函数，不是扫描器漏判
                # （自测第一次跑就是这样报了 2 条假 FAIL）。
                hits, devlog = (scan_file(path) if kind == "vue" else scan_ts_file(path))
            if len(hits) != want:
                print(f"FAIL {kind}：{desc}（命中 {len(hits)}，期望 {want}）{hits}")
                bad += 1
            else:
                print(f"PASS {kind}：{desc}")
    # console 那条除了不判红，还必须落进 devlog 计数
    with tempfile.TemporaryDirectory() as td:
        path = os.path.join(td, "a.vue")
        open(path, "w", encoding="utf-8").write(
            "<script setup>\nconsole.warn('调试中文')\n</script>\n")
        hits, devlog = scan_file(path)
        total += 1
        if hits or len(devlog) != 1:
            print(f"FAIL console 归类：hits={hits} devlog={devlog}")
            bad += 1
        else:
            print("PASS console 归类：不判红，但计入开发者日志")
    print(f"\n===== i18n_static_scan --selftest: {total - bad}/{total} PASS =====")
    return 1 if bad else 0


def main():
    _assert_no_duplicate_keys()

    hits = exemption_hits()
    dead = [f"{t}:{p} 条款 {n!r} 命中 {c}" for (t, p, n), c in hits.items() if c <= 0]
    if dead:
        for d in dead:
            print("FAIL 豁免条款失效：", d)
        if not args.rebase:
            return 1

    pairs = check_dict_pairs()
    for line in pairs:
        print("FAIL i18n 字典：", line)
    if pairs and not args.rebase:
        return 1

    ap = argparse.ArgumentParser()
    ap.add_argument("--selftest", action="store_true",
                    help="跑内置形状用例（注释/模板/字符串各形状）")
    ap.add_argument("--list", action="store_true", help="逐条打印违规")
    ap.add_argument("--rebase", action="store_true",
                    help="按当前实测打印 BASELINE / TS_BASELINE 字典（还完债后粘贴回来）")
    args = ap.parse_args()
    if args.selftest:
        return selftest()

    all_rows = []
    dev_logs = []
    ts_rows, ts_logs = scan_ts()
    all_rows.extend(ts_rows)
    dev_logs.extend(ts_logs)
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
    if args.rebase:
        vue_part, ts_part = {}, {}
        for r, _, kind, _ in all_rows:
            (ts_part if kind == "ts" else vue_part)[r] = (ts_part if kind == "ts" else vue_part).get(r, 0) + 1
        for name, d in (("BASELINE", vue_part), ("TS_BASELINE", ts_part)):
            print(f"{name} = {{")
            for r in sorted(d, key=lambda x: (-d[x], x)):
                print(f'    "{r}": {d[r]},')
            print("}")
        print(f"# 合计 {len(all_rows)} 处（.vue {sum(vue_part.values())} / .ts {sum(ts_part.values())}）")
        return 0

    byfile = {}
    for r, _, _, _ in all_rows:
        byfile[r] = byfile.get(r, 0) + 1
    print(f"扫描 {total_vue} 个 .vue（豁免文件 {len(EXEMPT_FILES)} 个、逐条豁免 "
          f"{sum(len(v) for v in EXEMPT_REASONS.values())} + .ts 豁免 "
          f"{sum(len(v) for v in TS_EXEMPT.values())} 条，基线文件 {len(BASELINE) + len(TS_BASELINE)} 个）："
          f"界面中文 {len(all_rows)} 处 / 基线 {sum(BASELINE.values()) + sum(TS_BASELINE.values())} 处；"
          f"开发者日志中文 {len(dev_logs)} 处（不判红）")
    byfile_all = {}
    for r, _, _, _ in all_rows:
        byfile_all[r] = byfile_all.get(r, 0) + 1
    # 注意：不能把基线值回填进 byfile_all —— 那样一个「已经还清（0 处）」的文件
    # 会被自己填成 now==base 而报「持平」，棘轾就永远不催你下调。本轮实测踩过。
    base_map = {**BASELINE, **TS_BASELINE}
    bad = []
    for r in sorted(set(byfile_all) | set(BASELINE) | set(TS_BASELINE)):
        now, base = byfile_all.get(r, 0), base_map.get(r, 0)
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
        print("PASS 全仓界面中文零硬编码（BASELINE 为空表：此后新增任何一处都会直接红）")
        return 0
    print(f"PASS 未新增硬编码（剩余欠账 {len(all_rows)} 处，见上表）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
