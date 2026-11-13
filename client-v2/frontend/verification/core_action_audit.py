#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""core_action_audit.py —— 核心动作（guoapp action）的**覆盖对账**

要回答的问题很具体：真机那一趟（`client-v2.exe --selftest` 与手点）
第一次会执行到哪些核心动作？
这件事以前只在我手写的 `v2-windows-followups.md` 里靠叙述，
而本任务已经反复证明：叙述会漂，能算的必须算。

三个方向：

A. 生产代码用到的核心动作（去重）→ 每个都带 `文件:行` 出处。
B. Go 测试的假核心处理过哪些动作（`handlers["xxx"] = …`）。
C. **两者相减 = 只有真实核心才会第一次执行的动作**，就是真机清单的机器生成版。

D. 反向检查：假核心里处理了、生产代码**根本不调用**的动作 → 判红。
   那意味着测试在桩一个不存在的需求（我写假核心时最容易写出这种），
   或者生产代码改过名而桩忘了跟着改 —— 后者会让"测试通过"完全失去意义。

判据形状（第 53 轮定，都是踩过之后写死的）：
  · 核心调用的真实写法只有两种：`s.call("action", payload)` 与
    `native.Call(ctx, "action", payload)`（另有 `callCore(` 的同族写法）。
  · ⚠ **不能用宽口子 grep 引号**：我第一版用 `call\(\s*"x"` 这种松匹配，
    把 mpv 的 IPC 命令 `p.Command("loadfile"/"seek"/"quit")` 也算成了核心动作，
    于是"18 个只有真机能验"里有 3 个是假的。
    现在按**具体门面函数名**匹配，并且额外回显
    "看起来像动作调用但没被匹配到"的位置（E 方向）——
    解析器的盲区必须自己冒出来，而不是等下一个人重新踩。
  · 假核心的桩名从 `handlers["…"]` 与 `acts["…"]` 两种写法里取。

用法：
    python3 verification/core_action_audit.py            # 对账报告（D 不符 exit 1）
    python3 verification/core_action_audit.py --json     # 机器可读
    python3 verification/core_action_audit.py --selftest # 解析器形状自测
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
CLIENT = os.path.dirname(FRONTEND)          # client-v2/

# 核心门面调用（两种真实写法 + 同族变体）
CORE_CALL_RES = [
    re.compile(r"\b[\w.]*[sS]vc?\.call\(\s*\"([a-zA-Z][\w]*)\""),
    re.compile(r"\bs\.call\(\s*\"([a-zA-Z][\w]*)\""),
    re.compile(r"\bc\.call\(\s*\"([a-zA-Z][\w]*)\""),
    re.compile(r"native\.Call\(\s*(?:ctx,\s*)?\"([a-zA-Z][\w]*)\""),
    re.compile(r"\bcallCore\(\s*(?:ctx,\s*)?\"([a-zA-Z][\w]*)\""),
    re.compile(r"\b(?:api|core)\.Call\(\s*(?:ctx,\s*)?\"([a-zA-Z][\w]*)\""),
    # 裸包函数写法（`return Call("dramaCreators", map…)`)。
    # `(?<![.\w])` 排除 `procGetDiskFreeSpace.Call(` 这类 Windows syscall。
    re.compile(r"(?<![.\w])Call\(\s*(?:ctx,\s*)?\"([a-zA-Z][\w]*)\""),
]
# 假核心的桩注册写法
FAKE_KEY_RES = [
    re.compile(r"handlers\[\"([a-zA-Z][\w]*)\"\]"),
    re.compile(r"\bacts\[\"([a-zA-Z][\w]*)\"\]"),
    re.compile(r"setups?\[\"([a-zA-Z][\w]*)\"\]"),
]
# E 方向：像动作调用但没被上面匹配到的位置（盲区回显）
SUSPECT_RE = re.compile(r"\b[\w.]*\.call\(\s*\"([a-zA-Z][\w]*)\"")
# 只认**播放器实例**上的 `.Command(`：`p.Command("loadfile", …)`。
# 第一版写成松口子 `\.Command\(`，把 `exec.Command("taskkill", …)` 也算成了 mpv 命令，
# 于是"参考名单"里出现了一个跟播放器无关的 Windows 进程命令 ——
# 同一族错（宽匹配）在两个方向上都犯过，所以这里连参考名单都要求精确。
MPV_COMMAND_RE = re.compile(r"\bp\.Command\(\s*\"([a-zA-Z][\w]*)\"")


MPV_DIR = os.path.join("internal", "mpv") + os.sep


def go_files(root, exclude_mpv=False):
    out = []
    for path in glob.glob(os.path.join(root, "**", "*.go"), recursive=True):
        if path.endswith("_test.go"):
            continue
        rel = os.path.relpath(path, root)
        # 核心动作与 mpv IPC 只在**目录**层面分得开：两处都有 `.call("字符串")` 的形态，
        # 但 internal/mpv 那个是 mpv JSON IPC 的命令名（loadfile/seek/set_property/…），
        # 不是 guoapp 的 action。混进来就会把"只有真机才第一次执行"的名单撑胖
        # ——本轮第一版就撑胖过（详见方向 E 的回显）。
        if exclude_mpv and rel.startswith(MPV_DIR):
            continue
        out.append(path)
    return sorted(out)


def go_test_files(root):
    return sorted(glob.glob(os.path.join(root, "**", "*_test.go"), recursive=True))


ROUTE_TABLE_RES = [
    # v1 core_client 的豁免名单 / sidecar 路由表 —— 代码里的权威动作集合
    re.compile(r"noSessionActions\s*=\s*map\[string\]bool\{(?P<b>.*?)\n\t\}", re.S),
    re.compile(r"sourcesActions\s*=\s*map\[string\]bool\{(?P<b>.*?)\n\t\}", re.S),
    re.compile(r"discoveryActions\s*=\s*map\[string\]bool\{(?P<b>.*?)\n\t\}", re.S),
]
ENTRY_RE = re.compile(r"\"([a-zA-Z][\w]*)\"\s*:\s*true")
# ⚠ 这条判据改过两次，每次都栽在"以为覆盖了"：
#   第一版写死变量名 `(action|act|name|cmd)` —— 自检 G4 红，
#     因为我自己在假源码里把变量叫成 `a`，探测器当没看见。
#   第二版改成"任何小写标识符"，但前置条件写成 `(?<![.\w])Call|call`，
#     于是 `s.call(action, …)`（小写门面方法，本仓最常见的写法）也被排除 —— 自检 G6c 红。
# 现在显式列出三种接收者形态，并把 `ctx` 这类首参排除掉；
# 六种形状各有一条单测（G6a–G6d 等），**正反两个方向都要钉**：
# 只测"该被抓到"而不测"不该被抓到"，判据就会一路往过报的方向漂。
VAR_CALL_HEAD = r"(?:\b[\w.]*\.call\(|(?<![.\w])Call\(|\b(?:native|api|core)\.Call\()"
VAR_CALL_RE = re.compile(
    VAR_CALL_HEAD + r"""\s*(?:(?:ctx|t),\s*)?(?!["'`\s])(?!ctx\b)([a-z_]\w*)\s*[,)]"""
)


def parse_route_tables(root):
    """engine.go 里三张表的动作名集合（权威名单）+ 变量传参的调用点。

    为什么要它：我写的正则是**猜出来的判据**，而这三张表是代码自己声明的名单。
    两者对不上时，先怀疑判据（本轮就是这么发现裸 `Call("dramaCreators")` 漏抓）。
    但也不能反过来把"表里有、字面量没抓到"直接判成死代码：
    `internal/native/cover.go:33` 是 `Call(action, payload)` —— 动作名来自变量，
    字面量抽取天生看不见（与第 51 轮 `bridgeApp()` 别名调用同一个教训），
    所以这一档只报"看不清"，不判红。
    """
    tables, var_sites = set(), []
    for path in go_files(root):
        text = io.open(path, encoding='utf-8').read()
        for rx in ROUTE_TABLE_RES:
            for m in rx.finditer(text):
                tables.update(ENTRY_RE.findall(m.group('b')))
        rel = os.path.relpath(path, root)
        for ln, line in enumerate(text.split('\n'), 1):
            code = re.sub(r"//.*$", "", line)
            for m in VAR_CALL_RE.finditer(code):
                var_sites.append("%s:%d: %s" % (rel, ln, code.strip()[:90]))
    return tables, var_sites


def parse_production(root):
    """action → [(文件:行, 原文)]。排除 internal/mpv（那是 mpv IPC，不是核心 action）。"""
    acts = {}
    for path in go_files(root, exclude_mpv=True):
        text = io.open(path, encoding="utf-8").read()
        for ln, line in enumerate(text.split("\n"), 1):
            code = re.sub(r"//.*$", "", line)
            for rx in CORE_CALL_RES:
                for m in rx.finditer(code):
                    acts.setdefault(m.group(1), []).append(
                        ("%s:%d" % (os.path.relpath(path, root), ln), code.strip()[:90]))
                    break
    return acts


def parse_fakes(root):
    keys = set()
    for path in go_test_files(root):
        text = io.open(path, encoding="utf-8").read()
        for rx in FAKE_KEY_RES:
            keys.update(rx.findall(text))
    return keys


def parse_suspects(root, matched):
    """被 .call("x") 形态调用、但没被任何判据收进名单的 → 盲区必须自己冒出来。"""
    hits = []
    for path in go_files(root):   # 盲区回显**不**排除 mpv，正是要看见这种同名不同物
        text = io.open(path, encoding="utf-8").read()
        for ln, line in enumerate(text.split("\n"), 1):
            code = re.sub(r"//.*$", "", line)
            for m in SUSPECT_RE.finditer(code):
                if m.group(1) not in matched:
                    hits.append("%s:%d: %s" % (os.path.relpath(path, root), ln, code.strip()[:90]))
    return hits


def parse_mpv(root):
    """mpv IPC 命令名单：用来反向自证它们**没有**被误算进核心动作。"""
    cmds = set()
    for path in go_files(root, exclude_mpv=False):
        text = io.open(path, encoding="utf-8").read()
        for line in text.split("\n"):
            for m in MPV_COMMAND_RE.finditer(re.sub(r"//.*$", "", line)):
                cmds.add(m.group(1))
    return cmds


def audit(root=CLIENT):
    acts = parse_production(root)
    fakes = parse_fakes(root)
    suspects = parse_suspects(root, set(acts))
    mpv = parse_mpv(root)
    used = set(acts)
    tables, var_sites = parse_route_tables(root)
    only_real = sorted(used - fakes)
    stale_fakes = sorted(fakes - used)
    # 判据必须自证不污染：mpv 命令出现在核心动作名单里就是错的
    contaminated = sorted(used & (mpv - {"resolve"}))
    return {
        "used": sorted(used),
        "route_table_actions": sorted(tables),
        "table_only_unseen": sorted(tables - used),
        "variable_call_sites": var_sites,
        "used_with_sites": {k: v for k, v in sorted(acts.items())},
        "fake_covered": sorted(fakes & used),
        "only_real_core": only_real,
        "stale_fakes": stale_fakes,
        "suspects": suspects,
        "mpv_commands": sorted(mpv),
        "contaminated": contaminated,
        "counts_selfcheck": len(used) == len(acts),
    }


# ------------------------------------------------------------------ 自检

def _selftest():
    import tempfile

    fails, counts = [], [0]

    def t(name, cond, detail=""):
        counts[0] += 1
        mark = "PASS" if cond else "FAIL"
        print("%s %s%s" % (mark, name, (" | " + str(detail)[:200]) if detail else ""))
        if not cond:
            fails.append(name)

    with tempfile.TemporaryDirectory() as d:
        svc = os.path.join(d, "internal", "x")
        os.makedirs(svc)
        io.open(os.path.join(svc, "svc.go"), "w", encoding="utf-8").write(
            "package x\n"
            'func (s *Service) Run() { _, _ = s.call("resolve", nil)\n'
            '\t_, _ = s.call("danmakuAll", map[string]any{})\n'
            '\t// s.call("inComment", nil) 注释里的不算\n'
            '}\n'
            "func init() { _, _ = native.Call(ctx, \"detail\", nil)\n"
            '\t_, _ = c.call("catalog", nil)\n'
            "  // ⚠ 下面这行**故意写成 s.call2**，用来验证判据不会什么都往名单里塞\n"
            '\t_, _ = s.call2("weirdOne", nil)\n'
            "}\n"
        )
        io.open(os.path.join(d, "main.go"), "w", encoding="utf-8").write(
            "package main\n"
            'var _ = callCore(ctx, "initialize", nil)\n'
            'func f() { _ = api.Call(ctx, "listSources", nil) }\n'
        )
        mpvdir = os.path.join(d, "internal", "mpv")
        os.makedirs(mpvdir, exist_ok=True)
        io.open(os.path.join(mpvdir, "mpv.go"), "w", encoding="utf-8").write(
            "package x\n"
            'func (p *P) Go() { _ = p.Command("loadfile", u, "replace"); _ = p.Command("seek", 1) }\n'
        )
        io.open(os.path.join(svc, "x_test.go"), "w", encoding="utf-8").write(
            "package x\n"
            'func TestX(t *testing.T) { h.core.handlers["resolve"] = f\n'
            '\th.acts["catalog"] = f\n'
            '\th.handlers["nonexistentAction"] = f }\n'
        )
        res = audit(d)
        t("A1 抓到 s.call 与 native.Call/c.call/callCore/api.Call 五种写法",
          {"resolve", "danmakuAll", "detail", "catalog", "initialize", "listSources"} <= set(res["used"]),
          res["used"])
        t("A2 注释里的调用不算", "inComment" not in res["used"], res["used"])
        t("A3 未登记的写法（s.call2）不进名单，但也不误报",
          "weirdOne" not in res["used"], res["used"])
        t("A4 每个动作带出处",
          res["used_with_sites"]["resolve"][0][0].endswith("svc.go:2"),
          res["used_with_sites"].get("resolve"))
        t("B1 假核心两种注册写法都认",
          "resolve" in res["fake_covered"] and "catalog" in res["fake_covered"],
          res["fake_covered"])
        t("C1 只有真机会第一次执行的差集正确",
          {"danmakuAll", "detail", "initialize", "listSources"} <= set(res["only_real_core"]),
          res["only_real_core"])
        t("D1 假核心桩了生产不调用的动作要被抓出来（判红项）",
          "nonexistentAction" in res["stale_fakes"], res["stale_fakes"])
        t("E1 mpv 命令不被算进核心动作",
          "loadfile" not in res["used"] and "seek" not in res["used"], res["used"])
        t("E2 mpv 命令单独成名单（供反向自证）",
          {"loadfile", "seek"} <= set(res["mpv_commands"]), res["mpv_commands"])
        t("E3 污染自证：真机名单里若混进 mpv 命令就红",
          res["contaminated"] == [], res["contaminated"])
        t("F1 suspects 是列表（真实仓库上的盲区回显通道没坏）",
          isinstance(res["suspects"], list), res["suspects"][:2])
        # 本轮判据自己回显出来的两个洞，用测试钉死不让它回来
        io.open(os.path.join(mpvdir, "mpv2.go"), "w", encoding="utf-8").write(
            "package x\n"
            'func (p *P) Set(n string, v any) error { return p.call("set_property", n, v) }\n'
            'func kill() { _ = exec.Command("taskkill", "/F", "/IM", "x.exe").Run() }\n'
        )
        res2 = audit(d)
        t("F2 internal/mpv 的 .call(\"set_property\") 不算核心动作",
          "set_property" not in res2["used"], res2["used"])
        t("F3 exec.Command 不进 mpv 参考名单（只有 p.Command 算）",
          "taskkill" not in res2["mpv_commands"], res2["mpv_commands"])
        t("F4 但盲区回显仍把它报出来（同名不同物必须可见）",
          any("set_property" in x for x in res2["suspects"]), res2["suspects"][:3])

        # 第六种写法 + 权威名单交叉核对
        io.open(os.path.join(svc, "actions.go"), "w", encoding="utf-8").write(
            "package x\n"
            'func Creators(d map[string]any) (any, error) {\n'
            '\treturn Call("dramaCreators", map[string]any{"drama": d})\n}\n'
            'func Var(a string, p map[string]any) (any, error) { return Call(a, p) }\n'
            "var noSessionActions = map[string]bool{\n"
            '\t\t"initialize": true, "cancelRead": true,\n'
            '\t}\n'
            "var sourcesActions = map[string]bool{\"listSources\": true}\n"
            "var discoveryActions = map[string]bool{\n"
            '\t\t"dramaCreators": true, "creatorWorks": true,\n'
            '\t}\n'
        )
        res3 = audit(d)
        t("G1 裸 Call(\"x\") 写法被抓到（第一版判据漏的就是它）",
          "dramaCreators" in res3["used"], res3["used"])
        t("G2 三张路由表都解析到（表名写死在代码里，不再靠我猜形状）",
          {"initialize", "cancelRead", "listSources", "creatorWorks"} <= set(res3["route_table_actions"]),
          res3["route_table_actions"])
        t("G3 表里有、字面量抓不到的进 table_only_unseen",
          "cancelRead" in res3["table_only_unseen"], res3["table_only_unseen"])
        t("G4 变量传参点被列出来，且**不靠变量叫什么名字**（第一版就是栽在这）",
          any("Call(a, p)" in x for x in res3["variable_call_sites"]),
          res3["variable_call_sites"][:2])
        t("G5 syscall 形态的 .Call( 不算核心动作",
          "taskkill" not in res3["used"], res3["used"])
        # 假源码里有 `native.Call(ctx, "detail", nil)`：
        # 首参是 ctx、次参才是字面量，**不该**被当成变量传参报出来。
        # 直接单测这个函数：`Call(ctx, "x")` 的 ctx 不该被当变量名登记，
        # 而 `Call(a, p)` 必须被登记 —— 两个方向都要钉，
        # 上一版只测了一个方向（还是靠变量名的那个方向），所以假阴性没被发现。
        def var_hits(line):
            return [m.group(1) for m in VAR_CALL_RE.finditer(line)]
        t("G6a Call(ctx, \"字面量\") 不登记成变量传参",
          var_hits('data, err := native.Call(ctx, "detail", nil)') == [],
          var_hits('data, err := native.Call(ctx, "detail", nil)'))
        t("G6b Call(a, p) 必须登记（不认变量叫什么）",
          var_hits('return Call(a, p)') == ["a"], var_hits('return Call(a, p)'))
        t("G6c s.call(action, payload) 也登记",
          var_hits('_ = s.call(action, payload)') == ["action"],
          var_hits('_ = s.call(action, payload)'))
        t("G6d syscall 形态 .Call( 不算",
          var_hits('r1, _, _ := procGetDiskFreeSpace.Call uintptr(a)') == [],
          var_hits('r1, _, _ := procGetDiskFreeSpace.Call uintptr(a)'))

    print("\n===== core_action_audit --selftest: %d/%d PASS =====" % (counts[0] - len(fails), counts[0]))
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
        print(json.dumps(res, ensure_ascii=False, indent=1))
        return 1 if res["stale_fakes"] or res["contaminated"] else 0

    print("核心动作：生产用到 %d 个；假核心覆盖 %d 个；"
          "只有真实核心才会第一次执行 %d 个"
          % (len(res["used"]), len(res["fake_covered"]), len(res["only_real_core"])))

    bad = False
    if res["stale_fakes"]:
        bad = True
        print("\nFAIL 方向 D：假核心桩了生产代码**不调用**的动作"
              "（测试在桩一个不存在的需求，或动作改名后桩没跟着改 —— 后者会让测试通过失去意义）：")
        for k in res["stale_fakes"]:
            print("  - %s" % k)
    else:
        print("\nPASS 方向 D：假核心的桩全部对应生产在用的动作")

    if res["contaminated"]:
        bad = True
        print("\nFAIL 判据被 mpv IPC 命令污染：%s（.Command 不该进核心动作名单）"
              % res["contaminated"])

    print("\n方向 C：只有真实核心才会第一次执行的动作（真机清单的机器生成版）：")
    for k in res["only_real_core"]:
        sites = res["used_with_sites"][k]
        print("  - %-22s @ %s" % (k, sites[0][0]))

    print("\n方向 E：疑似动作调用但没被判据收下（盲区回显，0 才说明判据覆盖完）：")
    for s in res["suspects"][:12]:
        print("  - %s" % s)
    if not res["suspects"]:
        print("  （无）")
    if res["suspects"]:
        print("  …共 %d 处，见 --json" % len(res["suspects"]))

    print("\n方向 F：与 engine.go 三张路由表交叉核对（表是代码自己声明的权威名单）：")
    print("  表内动作 %d 个；其中字面量调用抓不到的 %d 个"
          % (len(res["route_table_actions"]), len(res["table_only_unseen"])))
    if res["table_only_unseen"]:
        print("  ⚠ 这些**不判死**：动作名可能来自变量传参（如 cover.go 的 Call(action, payload)），"
              "字面量抽取天生看不见 —— 与第 51 轮 bridgeApp() 别名调用同一教训。")
        for k in res["table_only_unseen"]:
            print("    - %s" % k)
    if res["variable_call_sites"]:
        print("  变量传参点（判据的天花板就在这里）：")
        for v in res["variable_call_sites"][:4]:
            print("    - %s" % v)

    print("\n参考：mpv IPC 命令（不计入核心动作）：%s" % " ".join(res["mpv_commands"]))
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
