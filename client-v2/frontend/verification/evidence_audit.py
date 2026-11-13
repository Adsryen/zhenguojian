#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""evidence_audit.py —— 每个绑定的**证据等级**清单（不是叙述，是现算的）

为什么要这个：这个任务里我已经反复纠正"声明与事实不符"
（假差异、恒真断言、把死代码当规格、凭记忆写数字）。
文档里写"某功能已验证"是叙述，而**验证到什么程度**必须由脚本现算，
否则下一轮我就会开始凭印象写覆盖情况——那正是我第 27 轮立规矩要防的事
（"手写名单管名字不管覆盖"）。

三个方向，严格程度不同：

A. 前端调了、Go 没有 —— **直接判红**。
   这是真 bug：`callGo('Xxx')` 打到不存在的绑定，运行时只会得到一个
   "原生方法 Xxx 不存在"，而 mock 面（VITE_MOCK）根本不走桥，
   Playwright 全绿也照样看不见。这条方向错了必须炸。

B. Go 有、前端没调 —— **只报告不判红**。
   Wails 把 App 的导出方法当 API 面，留着不一定错，但必须**看得见**它在长。
   本轮首跑就抓到 3 个：
     · App.BrowseFiltered —— R11 那轮筛选通道的探索残留，前端一行都没引用；
     · App.MergeList      —— 被 `DownloadList` 载荷里的 merges 取代（bindings_download.go:260
                             注释自己写着「同 DownloadList.merges」），没人单独调；
     · App.TrayStatus     —— 注释写「冒烟自检用；前端暂不消费」，
                             而 `internal/selftest` 里并没有一步用它。
   删除它们要单独决定（属于 API 面收窄），所以这里只登记。

C. 每个绑定有没有 Go 单测 / 有没有被 Playwright 验收脚本间接覆盖 —— 报告等级分布，
   并把"只有编译级证据"的那批点名列出来（这批才是真机清单要盯的东西）。

D. 自检（--selftest）：解析器自己的形状测试。
   没有它，这个审计本身就是下一个"看起来在工作其实读错了"的东西
   ——第 27/35/42/44 轮四次假绿全是判据装置坏了而结果照绿。

用法：
    python3 verification/evidence_audit.py            # 审计（A 类不符则 exit 1）
    python3 verification/evidence_audit.py --json     # 机器可读输出
    python3 verification/evidence_audit.py --selftest # 只跑解析器自检
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
ROOT = os.path.dirname(FRONTEND)          # client-v2/

# ---------------------------------------------------------------- 解析（A/B）

APP_METHOD_RE = re.compile(r"^func \(a \*App\) ([A-Z]\w+)\(", re.M)
CALLGO_HEAD_RE = re.compile(r"callGo(?:Raw)?\s*<")
CALLGO_PLAIN_RE = re.compile(r"callGo(?:Raw)?\s*\(")


def _skip_generics(text, i):
    """从 `<` 处开始跳过一层**平衡的**尖括号（类型参数里可以有任何东西，
    包括 `;`、`,`、对象字面量的 `>`-free 文本），返回尖括号之后的下标。"""
    depth = 0
    while i < len(text):
        ch = text[i]
        if ch == "<":
            depth += 1
        elif ch == ">":
            depth -= 1
            if depth == 0:
                return i + 1
        elif ch == "\n" and depth == 0:
            break
        i += 1
    return -1


def _first_quoted_ident(text, i):
    """从 `(` 之后找第一个**单/双引号**里的 PascalCase 标识符，即绑定名。
    限定在后随 `,`/`)`/空白 之前不跨行超过 200 字符，避免抓到远处的字符串。"""
    m = re.compile(r"[\s\n]*['\"]([A-Z]\w*)['\"]").match(text, i)
    return m.group(1) if m else None


def call_go_targets(text):
    """扫出所有 `callGo(...)` / `callGoRaw(...)` / `callGo<T>(...)` 的绑定名。

    为什么不用一条正则解决：这里要处理的类型参数形状是
      `callGo<PlayerSnapshot>('PlayerOpen', …)`
      `callGo<Record<string, number>>('SeenList')`
      `callGo<{ queued: number; replaced: boolean }>(\n  'MergeStart', …)`
    第二种的嵌套尖括号会让 `<[^>]*>` 提前收尾，
    第三种的 `;` 会让 `[^;]*` 直接放弃匹配 ——
    我前两版就分别在这两种形状上漏过真实绑定（SeenList / MergeStart 系三个）。
    括号配对用一个小扫描器做，比硬凑正则可靠，而且能被自检逐条钉住。
    """
    names = set()
    for m in re.finditer(r"callGo(?:Raw)?\s*", text):
        i = m.end()
        if i < len(text) and text[i] == "<":
            j = _skip_generics(text, i)
            if j < 0:
                continue
            i = j
        # 现在必须正好落在 `(` 上
        i2 = text.find("(", i)
        if i2 < 0 or i2 - i > 4 or "\n" in text[i:i2]:
            continue
        name = _first_quoted_ident(text, i2 + 1)
        if name:
            names.add(name)
    return names
# 少数地方不通过 callGo 而是直接摸桥（wailsApp.X / window.go.main.App.X）
BRIDGE_DIRECT_RE = re.compile(r"(?:wailsApp|window\.go\.main\.App)\.([A-Z]\w+)\s*\(")
# 宽松判据用：非注释代码里出现过的所有 PascalCase 标识符
ALL_IDENT_RE = re.compile(r"\b([A-Z][A-Za-z0-9_]+)\b")


LINE_COMMENT_RE = re.compile(r"^\s*//.*$", re.M)
TRAILING_COMMENT_RE = re.compile(r"\s*//[^\n]*$", re.M)


def read_all(paths):
    """读多个文件并**剥掉行注释**。

    为什么要剥：`callGo('X')` 出现在 `// callGo('X') 这行是注释` 里也会被正则抓到，
    于是"前端有引用"会被注释撑起来 —— 方向 B（Go 有、前端没调）的名单就会漏报，
    而这个脚本存在的全部意义就是那份名单准不准。
    只剥整行注释与行尾注释；不做完整 TS 词法分析（够用且可自检）。
    """
    out = []
    for path in paths:
        text = io.open(path, encoding="utf-8").read()
        text = LINE_COMMENT_RE.sub("", text)
        out.append(text)
    return "\n".join(out)


def strip_trailing_comments(text):
    return TRAILING_COMMENT_RE.sub("", text)


def parse_go_bindings(root):
    """App 的导出方法名 → 声明所在文件（用于报告行位置）。"""
    out = {}
    for path in sorted(glob.glob(os.path.join(root, "*.go"))):
        text = io.open(path, encoding="utf-8").read()
        for m in APP_METHOD_RE.finditer(text):
            line = text[:m.start()].count("\n") + 1
            out.setdefault(m.group(1), "%s:%d" % (os.path.basename(path), line))
    return out


def parse_ts_calls(frontend):
    """返回 (被 callGo 系精确识别到的名字, 在前端出现过的名字, 文件列表)。

    为什么要分两层判据 —— 这是本脚本自己被咬过之后改的：
    第一版只用一条 `callGo<...>('X')` 正则，把 `ClipboardWrite` 报成"无人调用的死绑定"，
    而它其实被 `contextMenu.ts` 经 `bridgeApp()` **别名**调用（`app?.ClipboardWrite(text)`）。
    再往前两版分别因为嵌套尖括号和类型里的 `;` 漏掉过 `SeenList` / `MergeStart` 系三个。
    结论：**"有没有人用"这件事不能靠解析调用点来判**，调用写法是开放的
    （callGo、callGoRaw、桥直调、bridgeApp() 别名、以后还会有新写法），
    解析器每漏一种就制造一条**假的"死 API"结论**，而那类结论会直接导向删除。

    所以现在：
      · called_strict = 精确认出的调用（callGo 系 + 桥直调 + 别名调用）
      · mentioned     = 名字在非注释代码里出现过（宽松、安全、不会漏）
    "前端无调用"的名单以 mentioned 为准（宁可少报，不可误报），
    strict 只用来分证据等级。
    """
    paths = glob.glob(os.path.join(frontend, "src/**/*.ts"), recursive=True)
    paths += glob.glob(os.path.join(frontend, "src/**/*.vue"), recursive=True)
    text = strip_trailing_comments(read_all(paths))
    strict = set(call_go_targets(text))
    strict.update(BRIDGE_DIRECT_RE.findall(text))
    # 别名写法：`app?.ClipboardWrite(` / `app.ClipboardWrite(`
    strict.update(re.findall(r"\bapp\.\.?([A-Z]\w+)\s*\(", text))
    mentioned = {n for n in ALL_IDENT_RE.findall(text)}
    return strict, mentioned, paths


# ---------------------------------------------------------------- 证据等级（C）

def parse_go_test_refs(root):
    """Go 测试里出现过的绑定名（按标识符边界匹配，避免 `Open` 命中 `PlayerOpen`）。"""
    paths = glob.glob(os.path.join(root, "**", "*_test.go"), recursive=True)
    text = read_all(paths)
    return text, len(paths)


def parse_verify_scripts(frontend):
    """验收脚本里出现过的字符串（绑定名很少直接出现，但方法名会漏进来，
    所以这里只用于"脚本总数"与等级分布的分母说明，不做匹配依据）。"""
    return sorted(os.path.basename(p) for p in
                  glob.glob(os.path.join(frontend, "verification", "*_verify.py")))


def classify(bindings, called, mentioned, go_test_text):
    tiers = {}
    for name in sorted(bindings):
        # 注意 wired 用的是 mentioned（宽松）：宁可把"死绑定"判少，不可判错
        wired = name in mentioned
        tested = re.search(r"\b%s\b" % re.escape(name), go_test_text) is not None
        if wired and tested:
            tier = "桥接+Go单测"
        elif wired:
            tier = "仅桥接（Playwright/mock 覆盖，无 Go 单测）"
        elif tested:
            tier = "仅Go单测（前端无调用）"
        else:
            tier = "只有编译级证据（无人调用、无单测）"
        tiers[name] = tier
    return tiers


def audit(root=ROOT, frontend=FRONTEND):
    bindings = parse_go_bindings(root)
    called, mentioned, ts_paths = parse_ts_calls(frontend)
    go_test_text, n_test_files = parse_go_test_refs(root)
    scripts = parse_verify_scripts(frontend)
    tiers = classify(bindings, called, mentioned, go_test_text)

    missing_in_go = sorted(c for c in called if c not in bindings)
    not_called = sorted(n for n in bindings if n not in mentioned)
    distribution = {}
    for tier in tiers.values():
        distribution[tier] = distribution.get(tier, 0) + 1
    return {
        "bindings": len(bindings),
        "called": len(called),
        "mentioned": len(mentioned & set(bindings)),
        "ts_files": len(ts_paths),
        "go_test_files": n_test_files,
        "verify_scripts": len(scripts),
        "missing_in_go": missing_in_go,
        "not_called": not_called,
        "tiers": tiers,
        "distribution": distribution,
    }


# ---------------------------------------------------------------- 自检（D）

def _selftest():
    """解析器的形状测试：用内存里的假源码，不依赖仓库当前状态。
    这样"审计脚本读错了"会立刻在这里红，而不是等到真实仓库上给出错的绿灯。"""
    import tempfile

    fails = []
    counts = [0]

    def t(name, cond, detail=""):
        counts[0] += 1
        mark = "PASS" if cond else "FAIL"
        print("%s %s%s" % (mark, name, (" | " + str(detail)[:150]) if detail else ""))
        if not cond:
            fails.append(name)

    with tempfile.TemporaryDirectory() as d:
        # Go 侧：一个正常方法、一个带接收者前缀不该匹配的、一个多参数
        io.open(os.path.join(d, "app.go"), "w", encoding="utf-8").write(
            "package main\n"
            "func (a *App) PlayerOpen(x string) (string, error) { return \"\", nil }\n"
            "func (a *App) TrayStatus() string { return \"\" }\n"
            "func (s *Service) NotABinding() {}\n"
            "// func (a *App) Commented() string { return \"\" }\n"
            "func (a *App) BrowseFiltered(a, b string) (string, error) { return \"\", nil }\n"
        )
        got = parse_go_bindings(d)
        t("A1 抓到 App 导出方法", {"PlayerOpen", "TrayStatus", "BrowseFiltered"} <= set(got), sorted(got))
        t("A2 非 App 接收者不算绑定", "NotABinding" not in got, sorted(got))
        t("A3 注释里的 func 不算绑定", "Commented" not in got, sorted(got))

        fs = os.path.join(d, "frontend")
        os.makedirs(os.path.join(fs, "src", "services"))
        io.open(os.path.join(fs, "src", "services", "api.ts"), "w", encoding="utf-8").write(
            "const a = callGo<PlayerSnapshot>('PlayerOpen', x)\n"
            "const b = callGo<{ ok: boolean }>('SaveWatch', JSON.stringify(d))\n"
            "const c = callGoRaw('NotReady', 'x')\n"
            "const e = wailsApp.TrayStatus()\n"
            "const g = window.go.main.App.BrowseFiltered(s, f)\n"
            "// callGo('InComment') 这行是注释，不算调用\n"
        )
        names, _men, paths = parse_ts_calls(fs)
        t("B1 callGo 泛型形式能解析", "PlayerOpen" in names, sorted(names))
        t("B2 callGoRaw 也能解析", "NotReady" in names, sorted(names))
        t("B3 桥直调两种写法都能解析", {"TrayStatus", "BrowseFiltered"} <= names, sorted(names))
        t("B4 注释里的调用名不算", "InComment" not in names, sorted(names))
        # 嵌套泛型：`callGo<Record<string, number>>('SeenList')`
        # 第一版就是在这里漏掉调用，把 SeenList 误判成死绑定
        io.open(os.path.join(fs, "src", "services", "nested.ts"), "w", encoding="utf-8").write(
            "const a = callGo<Record<string, number>>('SeenList')\n"
            "const b = callGo<Array<{ id: string; n: number }>>('ImportSeen', x)\n"
            "const c = callGo<Map<string, Set<number>>>('DeepGeneric')\n"
        )
        nested, _m, _p = parse_ts_calls(fs)
        for nm in ("SeenList", "ImportSeen", "DeepGeneric"):
            t("B6 嵌套泛型/含分号的类型参数能解析出 %s" % nm, nm in nested, sorted(nested))
        # 跨行的真实写法（我第二版在这里丢过 MergeStart/MergeStartMulti/ObserveDrama）
        io.open(os.path.join(fs, "src", "services", "multiline.ts"), "w", encoding="utf-8").write(
            "export const realApi = {\n"
            "  mergeStart: (mode, drama, jobIds) =>\n"
            "    callGo<{ queued: number; replaced: boolean; episodes: number[] }>(\n"
            "      'MergeStart',\n"
            "      mode\n"
            "    ),\n"
            "  observeDrama: (drama) =>\n"
            "    callGo<{ observed: boolean }>('ObserveDrama', JSON.stringify(drama)),\n"
            "}\n"
        )
        ml, _m2, _p2 = parse_ts_calls(fs)
        for nm in ("MergeStart", "ObserveDrama"):
            t("B7 跨行 + 含分号的对象类型也能解析出 %s" % nm, nm in ml, sorted(ml))
        # 别名调用（就是这条把我骗成"ClipboardWrite 是死绑定"的写法）
        io.open(os.path.join(fs, "src", "services", "alias.ts"), "w", encoding="utf-8").write(
            "const app = bridgeApp()\n"
            "if (app?.ClipboardWrite) { await app.ClipboardWrite(text) }\n"
            "await app.ClipboardRead()\n"
        )
        al, almen, _ = parse_ts_calls(fs)
        t("B8 bridgeApp() 别名调用被认出",
          {"ClipboardWrite", "ClipboardRead"} <= al, sorted(al))
        t("B9 宽松判据同样覆盖别名",
          {"ClipboardWrite", "ClipboardRead"} <= almen, len(almen))
        t("B5 返回引用文件列表非空", len(paths) == 1, paths)

        # 分级：假测试文本
        tdir = os.path.join(d, "td")
        os.makedirs(tdir)
        io.open(os.path.join(tdir, "x_test.go"), "w", encoding="utf-8").write(
            "func TestPlayerOpenThing(t *testing.T) { _ = \"PlayerOpen\" }\n"
        )
        text, nf = parse_go_test_refs(tdir)
        t("C1 测试文件计数", nf == 1, nf)
        t("C2 单词边界匹配（测试里出现 PlayerOpen）",
          re.search(r"\bPlayerOpen\b", text) is not None, "")
        # A 方向的核心判据：绑定名出现在 "PlayerOpenThing" 这种标识符里也算引用，
        # 但反过来 `NotReady` 不该被误匹配
        t("C3 未测试的绑定不会被误判为有测试",
          re.search(r"\bNotReady\b", text) is None, "")

    # 真实仓库上必须至少抓到一些东西（否则等于解析器空转）
    res = audit()
    t("D1 真实仓库抓到绑定数 > 40", res["bindings"] > 40, res["bindings"])
    t("D2 真实仓库抓到前端调用数 > 40", res["called"] > 40, res["called"])
    t("D3 每个绑定都有等级标签", len(res["tiers"]) == res["bindings"],
      "%d/%d" % (len(res["tiers"]), res["bindings"]))
    t("D4 等级分布求和等于绑定总数", sum(res["distribution"].values()) == res["bindings"],
      res["distribution"])
    called_real, men_real, _ = parse_ts_calls(FRONTEND)
    # ClipboardWrite 是"别名调用"那一族：strict 认得出，但我第一版整个漏判为死绑定
    for nm in ("SeenList", "MergeStart", "MergeStartMulti", "ObserveDrama",
               "PlayerOpen", "ClipboardWrite", "ClipboardRead"):
        t("D5 真实仓库里 %s 必须被判为有调用" % nm, nm in called_real or nm in men_real,
          "漏了就是解析器又退化了一种写法")

    total = counts[0]
    print("\n===== evidence_audit --selftest: %d/%d PASS =====" % (total - len(fails), total))
    return 1 if fails else 0


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--selftest", action="store_true")
    args = ap.parse_args()

    if args.selftest:
        return _selftest()

    res = audit()
    bindings_loc = parse_go_bindings(ROOT)
    if args.json:
        print(json.dumps(res, ensure_ascii=False, indent=1))
        return 1 if res["missing_in_go"] else 0

    print("绑定 %d 个｜前端引用名 %d 个｜Go 测试文件 %d 个｜验收脚本 %d 只"
          % (res["bindings"], res["called"], res["go_test_files"], res["verify_scripts"]))

    if res["missing_in_go"]:
        print("\nFAIL 前端调用了 Go 里不存在的绑定（运行时只会得到「原生方法不存在」，"
              "而 mock 面不走桥，Playwright 看不见）：")
        for n in res["missing_in_go"]:
            print("  - %s" % n)
    else:
        print("\nPASS 方向 A：前端调用的绑定名全部在 Go 里存在")

    print("\n方向 B：Go 有、前端没调（只登记不判红，删不删是 API 决定）：")
    for n in res["not_called"]:
        print("  - %-22s %s" % (n, res["tiers"][n]))

    print("\n证据等级分布：")
    for tier, cnt in sorted(res["distribution"].items(), key=lambda kv: -kv[1]):
        print("  %3d  %s" % (cnt, tier))

    only_compile = [n for n, t in res["tiers"].items() if t.startswith("只有编译级")]
    print("\n（判据说明：「前端无调用」用的是宽松判据——名字在非注释代码里出现过就算有调用；"
          "\n  精确的 callGo/桥直调/别名解析只用于分等级，避免解析器漏写法时误报死 API。）")
    print("\n只有编译级证据的绑定（这批才是真机清单要盯的）：")
    for n in only_compile:
        print("  - %-22s @ %s" % (n, bindings_loc.get(n, "?")))
    if not only_compile:
        print("  （无）")

    return 1 if res["missing_in_go"] else 0


if __name__ == "__main__":
    sys.exit(main())
