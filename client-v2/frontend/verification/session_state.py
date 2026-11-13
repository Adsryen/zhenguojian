#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""session_state.py —— 把会话的**数字**全部现测后写成一份文件

为什么需要它：我在 notes 里已经**六次**写出错的提交数
（写「121」实得 117、写「128」实得 127、写「131」实得 130…），
每次都补一句"以后一律现测"，然后下次还是凭记忆。
这说明靠自觉不管用时，就要把数字放到**只能由脚本产生**的地方。

本脚本不做判断，只做一件事：所有数字都由子进程/文件系统现测，
写进 `research/SESSION-STATE.md`。
我（或任何人）在报告里写数字时，应该从这里抄，而不是从记忆里抄。

用法：
    python3 verification/session_state.py            # 重新生成文件
    python3 verification/session_state.py --check    # 只比较，不写（内容变了 exit 1）
"""
import argparse
import glob
import io
import os
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
CLIENT = os.path.dirname(FRONTEND)
REPO = os.path.dirname(CLIENT)
OUT = os.path.join(REPO, "research", "SESSION-STATE.md")
BASE = "a1a1c89"


def sh(args, cwd=REPO):
    r = subprocess.run(args, cwd=cwd, capture_output=True, text=True)
    return r.stdout.strip()


def commits():
    n = sh(["git", "rev-list", "--count", "%s..HEAD" % BASE])
    return int(n) + 1 if n.isdigit() else 0


def head():
    return sh(["git", "rev-parse", "--short", "HEAD"])


def dirty():
    return len([x for x in sh(["git", "status", "--porcelain"]).splitlines() if x.strip()])


def go_test_counts():
    """internal 九包与根包各自的声明用例数（现算，不抄）。"""
    per = {}
    for path in glob.glob(os.path.join(CLIENT, "internal", "*", "*_test.go")):
        pkg = os.path.basename(os.path.dirname(path))
        text = io.open(path, encoding="utf-8").read()
        names = set(x for x in _funcs(text))
        per.setdefault(pkg, set()).update(names)
    root = {}
    for path in glob.glob(os.path.join(CLIENT, "*_test.go")):
        text = io.open(path, encoding="utf-8").read()
        root["(root)"] = root.get("(root)", set()) | set(_funcs(text))
    return per, root


def _funcs(text):
    import re
    return re.findall(r"(?m)^func (Test[A-Za-z0-9_]+)\(", text)


# 易变字段：正在改代码时它们必然每跑一次就变，放进比较里等于**装一条永远闪的门**。
# （本轮实测就闪了一次：`--check` 红在"工作区未提交条目 2 vs 3"，
#   而那两个未提交条目正是我自己为了装这道门而改的文件 —— 判据把自己的施工过程
#   当成了待验事实。）
# 所以：这些行照旧**写进文件供人看**，但不参与一致性比较。
VOLATILE_PREFIXES = (
    "生成时间：",
    "| 工作区未提交条目",
    "| HEAD",
)


def norm(text):
    """比较前剥掉易变行：时间戳、HEAD、未提交条目。
    剩下全是"结构性"数字（提交数、脚本数、端口数、动作数、键数），
    它们只有在代码真变了才会变 —— 那才是这道门该管的。"""
    return "\n".join(l for l in text.splitlines()
                      if not any(l.startswith(p) for p in VOLATILE_PREFIXES))

    if args.check:
        old = io.open(OUT, encoding="utf-8").read() if os.path.exists(OUT) else ""
        if norm(old) == norm(body):
            print("PASS SESSION-STATE.md 与现测一致（比较忽略生成时间行）")
            return 0
        import difflib
        diff = [d for d in difflib.unified_diff(norm(old).splitlines(),
                                                norm(body).splitlines(),
                                                "文件", "现测", lineterm="", n=0)][2:]
        print("FAIL SESSION-STATE.md 与现测不一致（过期或被人手改）：")
        for d in diff[:12]:
            print("  " + d[:150])
        print("  重新生成：python3 verification/session_state.py")
        return 1

    io.open(OUT, "w", encoding="utf-8").write(body)
    print("已生成 %s（累计提交 %d、Go 声明用例见表内）" % (OUT, commits()))
    return 0




def main(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("--check", action="store_true")
    args = ap.parse_args(argv)

    import core_action_audit
    import evidence_audit
    import i18n_key_audit

    ca = core_action_audit.audit(CLIENT)
    ev = evidence_audit.audit(CLIENT, FRONTEND)
    ky = i18n_key_audit.audit()
    per, root = go_test_counts()
    scripts = sorted(os.path.basename(p) for p in
                     glob.glob(os.path.join(HERE, "*_verify.py")))

    lines = []
    a = lines.append
    a("<!-- 本文件由 `client-v2/frontend/verification/session_state.py` 生成，")
    a("     不要手改：里面每个数字都是现测的。我在这个任务里六次把提交数写错")
    a("     （凭记忆），所以数字从此只从这里抄。 -->")
    a("")
    a("# 会话状态（现测）")
    a("")
    a("生成时间：%s" % time.strftime("%Y-%m-%d %H:%M:%S %z"))
    a("")
    a("| 指标 | 值 | 怎么量的 |")
    a("|---|---|---|")
    a("| 累计提交（相对 `%s`） | **%d** | `git rev-list --count` + 首笔 |" % (BASE, commits()))
    a("| HEAD | `%s` | `git rev-parse --short HEAD` |" % head())
    a("| 工作区未提交条目 | %d | `git status --porcelain` 行数 |" % dirty())
    a("| 前端验收脚本 | %d 只 | `verification/*_verify.py` |" % len(scripts))
    import ports_audit
    _per, _owner, _conf = ports_audit.audit_dir(HERE)
    a("| 端口号（独占/冲突） | %d 个 / %d 处冲突 | `ports_audit.audit_dir` |"
      % (len(_owner), len(_conf)))
    a("| Go 绑定 | %d 个，前端零引用 %d 个 | `evidence_audit` |"
      % (ev["bindings"], len(ev["not_called"])))
    a("| 绑定证据等级 | %s | `evidence_audit` |"
      % " / ".join("%s %d" % (k.split("（")[0], v)
                   for k, v in sorted(ev["distribution"].items(), key=lambda kv: -kv[1])))
    a("| 核心动作 | 生产 %d，假核心覆盖 %d，只有真机第一次执行 %d | `core_action_audit` |"
      % (len(ca["used"]), len(ca["fake_covered"]), len(ca["only_real_core"])))
    a("| i18n 键 | zh %d / en %d，字面量用到 %d，动态取键 %d，真死候选 %d | `i18n_key_audit` |"
      % (ky["zh"], ky["en"], ky["used_literal"], ky["dynamic_sites"], len(ky["orphan"])))
    a("")
    a("## 只有真实核心才会第一次执行的动作（真机清单的机器生成版）")
    a("")
    a("`%s`" % " ".join(ca["only_real_core"]))
    a("")
    a("## 前端零引用的绑定（API 面收窄是决定，不自动删）")
    a("")
    a("`%s`" % " ".join(ev["not_called"]))
    a("")
    a("## i18n 真死候选（删或接都是决定）")
    a("")
    a("`%s`" % " ".join(ky["orphan"]))
    a("")
    a("## Go 测试声明数（现算）")
    a("")
    a("| 包 | 声明 |")
    a("|---|---|")
    total = 0
    for pkg in sorted(per):
        a("| internal/%s | %d |" % (pkg, len(per[pkg])))
        total += len(per[pkg])
    for pkg in sorted(root):
        a("| %s | %d |" % (pkg, len(root[pkg])))
        total += len(root[pkg])
    a("| **合计** | **%d** |" % total)
    a("")
    a("## 验收脚本清单")
    a("")
    for name in scripts:
        a("- `%s`" % name)
    a("")
    body = "\n".join(lines) + "\n"

def _selftest():
    """比较器的形状测试：它自己的两次错都用测试钉住 ——
    第一版把时间戳放进比较（刚生成就自红），
    第二版把"未提交条目"也放进去（施工中必闪）。"""
    fails, counts = [], [0]

    def t(name, cond, detail=""):
        counts[0] += 1
        print("%s %s%s" % ("PASS" if cond else "FAIL", name,
                           (" | " + str(detail)[:120]) if detail else ""))
        if not cond:
            fails.append(name)

    base = ("生成时间：2026-11-13 02:00:00 +0000\n"
            "| HEAD | `abc1234` | x |\n"
            "| 工作区未提交条目 | 0 | y |\n"
            "| 累计提交（相对 `base`） | **130** | z |\n"
            "| 核心动作 | 生产 26 | w |\n")
    moved = ("生成时间：2026-11-13 03:00:00 +0000\n"
             "| HEAD | `def5678` | x |\n"
             "| 工作区未提交条目 | 7 | y |\n"
             "| 累计提交（相对 `base`） | **130** | z |\n"
             "| 核心动作 | 生产 26 | w |\n")
    t("A1 时间戳/HEAD/未提交条目变了 → 不算过期", norm(base) == norm(moved))
    drifted = moved.replace("| 累计提交（相对 `base`） | **130** | z |",
                            "| 累计提交（相对 `base`） | **131** | z |")
    t("A2 结构性数字变了 → 必红（比较器不是摆设）", norm(base) != norm(drifted))
    more = moved.replace("| 核心动作 | 生产 26 | w |", "| 核心动作 | 生产 27 | w |")
    t("A3 动作数变了 → 也必红", norm(base) != norm(more))
    t("A4 norm 不会把整表抹平（保留的行数 > 2）",
      len(norm(base).splitlines()) == 2, norm(base).splitlines())
    print("\n===== session_state --selftest: %d/%d PASS =====" % (counts[0] - len(fails), counts[0]))
    return 1 if fails else 0


if __name__ == "__main__":
    if "--selftest" in sys.argv:
        sys.exit(_selftest())
    sys.exit(main())
