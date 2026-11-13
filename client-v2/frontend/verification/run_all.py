#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""run_all.py —— 全量验收回归：一次跑完 `*_verify.py`，并把「谁泄漏了端口」归因出来。

为什么要有这个脚本（2026-11-12 的实测教训）：
- 单独跑绿 ≠ 全量跑绿。第十二轮就是因为 `downloads_statbar_verify.py` 的
  `FRONTEND` 路径拼错，**从没被真正执行过**，里面一条断言早就过期了也没人发现。
  只有全量跑才会暴露「脚本压根没跑」这种事。
- 泄漏要能**归因到人**：以前只能 `ss -ltn` 看到端口被占，不知道是谁留下的。
  这里在每个脚本跑完后立刻复查它自己声明过的端口，谁没关干净就点名。

用法：
  python3 verification/run_all.py                 # 全量（默认串行，避免端口抢占）
  python3 verification/run_all.py --only player   # 只跑文件名含 player 的
  python3 verification/run_all.py --audit         # i18n_sweep 追加整页 en 审计
  python3 verification/run_all.py --keep-shots    # 保留脚本重写的截图（默认还原）

退出码：0 = 全部 exit 0；否则 = 有脚本失败/超时，或发现端口泄漏。
"""
import argparse
import os
import re
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))  # git 的 cwd，porcelain 路径是相对它输出的
DEFAULT_TIMEOUT = 340


def declared_ports(script_path):
    """脚本里出现过的 51xx 端口（含注释里的历史端口，宁可多报不可漏报）。"""
    text = open(script_path, encoding="utf-8").read()
    return {p for p in re.findall(r"\b(51\d{2})\b", text)}


def listening_ports():
    out = subprocess.run(["ss", "-ltn"], capture_output=True, text=True).stdout
    return set(re.findall(r":(51\d{2})\b", out))


def split_porcelain(lines):
    """把 `git status --porcelain` 的行分成（该还原的截图, 必须保留的其他改动）。

    纯函数、可自测：第十七轮的事故就是把还原范围写成了整个目录，
    静默吞掉同目录里未提交的代码改动。这里把判据抽出来，配 `--selftest` 钉住：
      · 只有「` M`＝工作区已改、未 staged」且以 .png 结尾 才允许还原；
      · staged(`M `)、未跟踪(`??`)、删除(` D`/`D `)、改名(`R `) 一律归入"不许动"；
      · 路径带引号（含空格）要脱引号，但仍属"其他改动"时不还原。
    """
    shots, others = [], []
    for line in lines:
        if not line.strip():
            continue
        # porcelain 格式是 XY<空格>path：` M` 才是「工作区已改、未 staged」，
        # `M ` 是已 staged（用户明确要提交的东西，绝对不许被 checkout 覆盖）。
        x, y, path = line[0], line[1], line[3:].strip().strip('"')
        if x == " " and y == "M" and path.endswith(".png"):
            shots.append(path)
        else:
            others.append(line)
    return shots, others


def dirty_shots():
    r = subprocess.run(["git", "status", "--porcelain", "--", HERE],
                       capture_output=True, text=True, cwd=ROOT)
    return split_porcelain(r.stdout.splitlines())[0]


def git_revert_shots(files):
    """只还原**截图**：截图应当跟随改动它的那次代码提交，而不是被一次纯回归刷成
    20 个二进制噪声 diff。

    ⚠ 这里以前写的是 `git checkout -- <verification 目录>`——那会连同该目录里
    **未提交的代码改动一起丢掉**。第十六轮之后我第一次跑全量就把自己刚改好、
    还没提交的白名单收紧和计数修正整个吞掉，且退出码 0、一句提示都没有。
    工具默认行为绝不能吞未提交工作：现在只按 .png 精确还原，
    非截图的脏文件一律保留并显式报出来。"""
    if not files:
        return True
    r = subprocess.run(["git", "checkout", "--", *files], capture_output=True, text=True, cwd=ROOT)
    return r.returncode == 0


def declared_checks(script_path):
    """脚本里 `check(` 的调用点数（不含 def 那行）。

    为什么要在报表里带上它：第二十二轮我写 `contextmenu_verify` 时有条断言包在
    `if img.count() > 0:` 里，那一页恰好没图就**整条静默跳过**，
    而脚本照样打 `RESULT: ALL PASS`。声明数与打印数一比才看得见"少测了几条"——
    和第 27 轮"手写名单管名字不管覆盖"是同一个病根。
    """
    text = open(script_path, encoding="utf-8").read()
    return sum(1 for line in text.splitlines() if re.match(r"\s*check\(", line))


def parse_run(out, code, secs, name):
    """把一次脚本运行拆成可报表的字段。**纯函数**，`--selftest` 钉住它。

    为什么要单独抽出来：第十五轮之前 `run_all` 失败时只给 `exit/PASS/FAIL 计数`，
    我于是又得手动把那个脚本单独跑一遍才看得到红的是哪条断言——
    回归器最没用的形态就是"只告诉你红了"。
    """
    lines = (out or "").splitlines()
    npass = sum(1 for l in lines if l.startswith(("[PASS]", "PASS ")))
    fail_lines = [l.strip() for l in lines if l.startswith(("[FAIL]", "FAIL ", "FAILED"))]
    summary = next((l for l in reversed(lines)
                    if re.search(r"(ALL PASS|ALL PASSED|RESULT|=====|FAILURES)", l)),
                   (lines[-1].strip() if lines else "(无输出)"))
    return dict(name=name, code=code, passed=npass, failed=len(fail_lines),
                fail_lines=fail_lines, secs=secs, summary=summary[:70])


def run_script(name, timeout, no_audit):
    args = [sys.executable, "-u", name]
    if no_audit and name == "i18n_sweep_verify.py":
        args.append("--no-audit")
    t0 = time.time()
    try:
        r = subprocess.run(args, capture_output=True, text=True, timeout=timeout,
                           cwd=HERE, stdin=subprocess.DEVNULL)
        out, code, err = r.stdout, r.returncode, r.stderr
    except subprocess.TimeoutExpired as exc:
        raw = exc.stdout if isinstance(exc.stdout, str) else (exc.stdout or b"").decode(errors="replace")
        out, code, err = raw, 124, f"TIMEOUT >{timeout}s"
    res = parse_run(out, code, round(time.time() - t0), name)
    res["err"] = (err or "").strip()[-200:]
    return res


def selftest():  # 分母必须把两组用例都算进去——
    # 第一版加 parse_run 用例时忘了改汇总，7/7 实际跑了 12 条：
    # 这类"数字看着自洽其实没人核对"正是本任务一直在抓的东西。
    """表驱动钉住「哪些脏文件允许被还原」这条判据 —— 还原范围写宽过一次，
    把同目录未提交的代码改动整个吞掉（退出码 0、无提示），这就是那次的回归测试。"""
    cases = [
        ("tracked 改动的截图要还原",
         [" M client-v2/frontend/verification/a.png"], ["client-v2/frontend/verification/a.png"], 0),
        ("未提交的代码改动绝不还原",
         [" M client-v2/frontend/verification/i18n_sweep_verify.py"], [], 1),
        ("staged 的截图也不许被 checkout 覆盖",
         ["M  client-v2/frontend/verification/b.png"], [], 1),
        ("未跟踪新截图保留",
         ["?? client-v2/frontend/verification/new.png"], [], 1),
        ("删除的截图保留（那是有人在删文件）",
         [" D client-v2/frontend/verification/c.png"], [], 1),
        ("带引号路径的 .png 仍算截图",
         [' M "client-v2/frontend/verification/with space.png"'],
         ["client-v2/frontend/verification/with space.png"], 0),
        ("混合：只有 png 进还原列表",
         [" M client-v2/frontend/verification/d.png",
          " M client-v2/frontend/verification/run_all.py",
          "?? client-v2/frontend/verification/.dev_x.log"],
         ["client-v2/frontend/verification/d.png"], 2),
    ]
    bad = 0
    for name, lines, want_shots, want_others in cases:
        shots, others = split_porcelain(lines)
        ok = shots == want_shots and len(others) == want_others
        print(f"{'PASS' if ok else 'FAIL'} {name}")
        if not ok:
            print(f"      shots={shots} 期望={want_shots} others={len(others)} 期望={want_others}")
            bad += 1
    out_cases = [
        ("抓到红的那几条", "PASS A1 x\nFAIL A2 原因\n[FAIL] A3 又一条\nRESULT: 2 FAILED", 1, 1, 2),
        ("全绿没有 FAIL 行", "PASS A1\n[PASS] A2\n===== 2/2 PASS =====", 0, 2, 0),
        ("TIMEOUT 没有输出也不炸", "", 124, 0, 0),
        ("FAILED 汇总行也计入失败行", "ALL CHECKS PASSED", 0, 0, 0),
        ("FAILED: 3 check(s) 要被抓出来", "FAIL ⑦ x\nFAILED: 1 check(s)", 1, 0, 2),
    ]
    for name, out, code, want_pass, want_fail in out_cases:
        r = parse_run(out, code, 0, "x_verify.py")
        ok = r["passed"] == want_pass and r["failed"] == want_fail
        print(f"{'PASS' if ok else 'FAIL'} parse_run：{name}")
        if not ok:
            print(f"      got passed={r['passed']} failed={r['failed']} "
                  f"期望 {want_pass}/{want_fail} 行={out!r}")
            bad += 1

    total = len(cases) + len(out_cases)
    print(f"\n===== run_all --selftest: {total - bad}/{total} PASS =====")
    return 1 if bad else 0


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--only", default="", help="只跑名字含该子串的脚本")
    ap.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT)
    ap.add_argument("--no-audit", action="store_true",
                    help="让 i18n_sweep 跳过整页 en 审计（默认是跑的，别顺手关掉）")
    ap.add_argument("--audit-checks", action="store_true",
                    help="顺带对账每个脚本的声明 check 数与实际打印行数。"
                         "**只当人工判读的线索**：循环里的 check 会打印多次、"
                         "失败分支里的 check 平时不打印，都对不上（本轮实测 6 个脚本里"
                         "只有 1 个是真被条件跳过）。所以默认关掉，免得狼来了。")
    ap.add_argument("--selftest", action="store_true",
                    help="只跑内置表驱动自测（还原判据），不启动任何验收脚本")
    ap.add_argument("--keep-shots", action="store_true", help="保留脚本重写的截图")
    args = ap.parse_args()

    if args.selftest:
        return selftest()

    # 开工前预检：两个脚本默认抢同一个端口 → 直接不跑。
    # 为什么要在跑之前查而不是跑完看残留：撞号时前一个脚本的服务器会被后一个当成
    # "已在监听"复用（除非它自己做了预检），于是**绿是在别人的构建上测的**——
    # R8 那条假红灯就是这么来的，而当时我是靠事后 ss -ltn + 人工对表才找到的。
    # 现在把对表变成机器做的（审计逻辑与被测形状都由 ports_audit --selftest 钉着）。
    import ports_audit
    _per, _owner, conflicts = ports_audit.audit_dir(HERE)
    if conflicts:
        print("FAIL 端口冲突，先别跑（否则会测到别人的服务器）：")
        for p2, files in sorted(conflicts.items()):
            print(f"  {p2} ← {', '.join(files)}")
        print("处置：给后加入的脚本换空闲号（5200 段起步），并同步 spec 端口表；"
              "跑 `python3 ports_audit.py --selftest` 确认抽取判据没退化。")
        return 1
    print(f"端口预检 OK：{len(_owner)} 个号被 {len(_per)} 个脚本独占，无争用")
    # spec 表对账**只提示不拦路**：文档落后不是测试完整性问题，
    # 用它卡住全量回归会让人开始绕过门禁（比没门禁更糟）。
    # 要硬拦就单独跑 `ports_audit.py --check-spec`（我提交文档前自己跑）。
    _ = ports_audit.check_spec(HERE)

    # 绑定层证据审计：方向 A（前端调了 Go 没有）是真 bug，硬拦；
    # 等级分布与"前端零引用"名单只报告——它是给我看趋势用的，
    # 拿它拦路会让人开始绕过门禁（同 ports_audit 的 spec 对账只提示不拦路的理由）。
    import evidence_audit
    _ev = evidence_audit.audit()
    if _ev["missing_in_go"]:
        print("FAIL 前端调用了 Go 里不存在的绑定：%s" % ", ".join(_ev["missing_in_go"]))
        print("     （mock 面不走桥，Playwright 全绿也看不见这类错，所以在这里硬拦）")
        return 1
    _dist = " / ".join("%s %d" % (k.split("（")[0], v) for k, v in
                       sorted(_ev["distribution"].items(), key=lambda kv: -kv[1]))
    print("绑定证据审计：%d 个绑定，前端零引用 %d 个（%s）；等级：%s"
          % (_ev["bindings"], len(_ev["not_called"]),
             ", ".join(_ev["not_called"]) or "无", _dist))

    # i18n 键三向对账：A（用了没定义）与 B（zh/en 不对称）是真 bug，硬拦——
    # `t()` 缺键不会报错，它**把键名原样显示到界面上**（第四十四轮我写出过一次
    # `downloads.mergeFail`，typecheck/build/硬编码扫描三道全绿）。
    # C（未用名单）与 D（动态取键位置）只报数，因为"没用到"不是错误。
    try:
        import i18n_key_audit
        _k = i18n_key_audit.audit()
        print("i18n 键对账：zh %d / en %d，字面量用到 %d，动态取键 %d 处；"
              "未用 %d（数据在用 %d + 拼接可得 %d + 真死候选 %d）"
              % (_k["zh"], _k["en"], _k["used_literal"], _k["dynamic_sites"],
                 len(_k["not_by_literal"]), len(_k["explainable"]),
                 len(_k["by_prefix"]), len(_k["orphan"])))
        if _k["missing_zh"] or _k["missing_en"]:
            print("FAIL 用到但没定义的键（界面会直接显示键名）：%s"
                  % ", ".join(sorted(set(_k["missing_zh"]) | set(_k["missing_en"]))))
            return 1
        if _k["only_zh"] or _k["only_en"]:
            print("FAIL zh/en 键集不对称（英文界面会露键名）：只 zh %s / 只 en %s"
                  % (_k["only_zh"], _k["only_en"]))
            return 1
    except SystemExit as e:
        # 判据认不出 i18n.ts 结构时 audit() 主动炸 —— 不能降级成"没键可查所以通过"
        print("FAIL i18n 键对账判据失效：%s" % str(e)[:160])
        return 1

    # 核心动作覆盖对账：D（假核心桩了生产不调用的动作）与"判据被 mpv 命令污染"是真错，硬拦；
    # C（只有真实核心才会第一次执行的动作）只报数——那是真机清单的机器生成版，不是缺陷。
    try:
        import core_action_audit
        _ca = core_action_audit.audit()
        print("核心动作对账：生产 %d 个，假核心覆盖 %d，"
              "只有真机会第一次执行 %d；路由表内抓不到 %d（变量传参，不判死）"
              % (len(_ca["used"]), len(_ca["fake_covered"]), len(_ca["only_real_core"]),
                 len(_ca["table_only_unseen"])))
        if _ca["stale_fakes"]:
            print("FAIL 假核心桩了生产代码不调用的动作（测试在桩一个不存在的需求）：%s"
                  % ", ".join(_ca["stale_fakes"]))
            return 1
        if _ca["contaminated"]:
            print("FAIL 核心动作名单被 mpv IPC 命令污染：%s" % ", ".join(_ca["contaminated"]))
            return 1
    except Exception as exc:  # 判据失效必须响，不能静默通过
        print("FAIL 核心动作对账判据执行失败：%r" % (exc,))
        return 1

    scripts = sorted(f for f in os.listdir(HERE)
                     if f.endswith("_verify.py") and (not args.only or args.only in f))
    if not scripts:
        print("没有匹配的 *_verify.py")
        return 1

    print(f"全量回归 {len(scripts)} 个脚本（串行，避免端口抢占）\n")
    print(f"{'脚本':<36}{'exit':>5}{'PASS':>6}{'FAIL':>6}{'秒':>5}  残留端口  最后一行")
    print("-" * 118)

    leaks, bad, silent = [], [], []
    for f in scripts:
        mine = declared_ports(os.path.join(HERE, f))
        res = run_script(f, args.timeout, args.no_audit)
        res["declared"] = declared_checks(os.path.join(HERE, f))
        leftover = sorted(listening_ports() & mine)
        if leftover:
            leaks.append((f, leftover))
        if res["code"] != 0 or res["failed"]:
            bad.append(res)
        print(f"{res['name']:<36}{res['code']:>5}{res['passed']:>6}{res['failed']:>6}"
              f"{res['secs']:>5}  {(','.join(leftover) or '-'):<10} {res['summary']}")
        # 打印数 = PASS+FAIL 行；比声明少就是**有断言没跑到**（条件分支跳过）。
        printed = res["passed"] + res["failed"]
        if args.audit_checks and res["code"] == 0 and printed < res["declared"]:
            print(f"    ⚠ 静默跳过 {res['declared'] - printed} 条："
                  f"声明 {res['declared']} 打印 {printed}"
                  "（候选：可能被条件跳过，也可能是循环/失败分支里的 check 造成口径差）")
            silent.append((res["name"], res["declared"], printed))
        if res["fail_lines"]:
            for l in res["fail_lines"][:6]:
                print(f"    ↳ {l[:150]}")
            if len(res["fail_lines"]) > 6:
                print(f"    ↳ …另有 {len(res['fail_lines']) - 6} 条 FAIL 行")
        if res["code"] != 0 and res["err"]:
            print(f"    ⚠ stderr: {res['err'][:160]}")

    total = len(scripts)
    if silent:
        print(f"\静默跳过候选（{len(silent)} 个脚本声明数 > 打印数，需人工判读）：")
        for name, dec, pri in silent:
            print(f"  {name}: 声明 {dec} 打印 {pri}")
    print(f"\nexit0 {total - len(bad)}/{total}；失败 {len(bad)}；端口泄漏 {len(leaks)} 处"
          + (f"；静默跳过候选 {len(silent)} 个" if silent else ""))
    for f, ports in leaks:
        print(f"  泄漏：{f} 跑完仍占 {ports}")
    if not args.keep_shots:
        lines = subprocess.run(["git", "status", "--porcelain", "--", HERE],
                               capture_output=True, text=True, cwd=ROOT).stdout.splitlines()
        shots, others = split_porcelain(lines)
        others = [l for l in others if l.strip()]
        ok = git_revert_shots(shots)
        print(f"截图还原：{'OK' if ok else '失败'}（{len(shots)} 张）")
        if others:
            print(f"  ⚠ 该目录还有 {len(others)} 个非截图改动被保留（不会被还原）：{others[:5]}")
    return 1 if (bad or leaks) else 0


if __name__ == "__main__":
    sys.exit(main())
