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


def dirty_shots():
    """本轮被脚本重写的截图（只认 .png，别的文件一概不碰）。"""
    r = subprocess.run(["git", "status", "--porcelain", "--", HERE],
                       capture_output=True, text=True, cwd=ROOT)
    return [l[3:].strip().strip('"') for l in r.stdout.splitlines()
            if l[1:2] == "M" and l.rstrip().endswith(".png")]


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


def run_script(name, timeout, audit):
    args = [sys.executable, "-u", name]
    if audit and name == "i18n_sweep_verify.py":
        args.append("--audit")
    t0 = time.time()
    try:
        r = subprocess.run(args, capture_output=True, text=True, timeout=timeout,
                           cwd=HERE, stdin=subprocess.DEVNULL)
        out, code, err = r.stdout, r.returncode, r.stderr
    except subprocess.TimeoutExpired as exc:
        raw = exc.stdout if isinstance(exc.stdout, str) else (exc.stdout or b"").decode(errors="replace")
        out, code, err = raw, 124, f"TIMEOUT >{timeout}s"
    lines = out.splitlines()
    npass = sum(1 for l in lines if l.startswith(("[PASS]", "PASS ")))
    nfail = sum(1 for l in lines if l.startswith(("[FAIL]", "FAIL ", "FAILED")))
    summary = next((l for l in reversed(lines)
                    if re.search(r"(ALL PASS|ALL PASSED|RESULT|=====|FAILURES)", l)),
                   (lines[-1] if lines else "(无输出)"))
    return dict(name=name, code=code, passed=npass, failed=nfail,
                secs=round(time.time() - t0), summary=summary.strip()[:70],
                err=(err or "").strip()[-200:])


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--only", default="", help="只跑名字含该子串的脚本")
    ap.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT)
    ap.add_argument("--audit", action="store_true", help="i18n_sweep 追加整页 en 审计")
    ap.add_argument("--keep-shots", action="store_true", help="保留脚本重写的截图")
    args = ap.parse_args()

    scripts = sorted(f for f in os.listdir(HERE)
                     if f.endswith("_verify.py") and (not args.only or args.only in f))
    if not scripts:
        print("没有匹配的 *_verify.py")
        return 1

    print(f"全量回归 {len(scripts)} 个脚本（串行，避免端口抢占）\n")
    print(f"{'脚本':<36}{'exit':>5}{'PASS':>6}{'FAIL':>6}{'秒':>5}  残留端口  最后一行")
    print("-" * 118)

    leaks, bad = [], []
    for f in scripts:
        mine = declared_ports(os.path.join(HERE, f))
        res = run_script(f, args.timeout, args.audit)
        leftover = sorted(listening_ports() & mine)
        if leftover:
            leaks.append((f, leftover))
        if res["code"] != 0 or res["failed"]:
            bad.append(res)
        print(f"{res['name']:<36}{res['code']:>5}{res['passed']:>6}{res['failed']:>6}"
              f"{res['secs']:>5}  {(','.join(leftover) or '-'):<10} {res['summary']}")
        if res["code"] != 0 and res["err"]:
            print(f"    ⚠ stderr: {res['err'][:160]}")

    total = len(scripts)
    print(f"\nexit0 {total - len(bad)}/{total}；失败 {len(bad)}；端口泄漏 {len(leaks)} 处")
    for f, ports in leaks:
        print(f"  泄漏：{f} 跑完仍占 {ports}")
    if not args.keep_shots:
        shots = dirty_shots()
        others = [l for l in subprocess.run(["git", "status", "--porcelain", "--", HERE],
                                            capture_output=True, text=True, cwd=ROOT).stdout.splitlines()
                  if l[1:2] == "M" and not l.rstrip().endswith(".png")]
        ok = git_revert_shots(shots)
        print(f"截图还原：{'OK' if ok else '失败'}（{len(shots)} 张）")
        if others:
            print(f"  ⚠ 该目录还有 {len(others)} 个非截图改动被保留（不会被还原）：{others[:5]}")
    return 1 if (bad or leaks) else 0


if __name__ == "__main__":
    sys.exit(main())
