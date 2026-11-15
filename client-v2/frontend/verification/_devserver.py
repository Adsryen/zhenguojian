#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""`_devserver.py` —— 验收脚本的 dev server 启停公共件（R8 根因的补丁）。

**为什么要有这个文件**（2026-11-12 实测）：`detail_download_verify.py` ② 真实态段
`text=/下载全部（8 集）/` 反复超时，看起来像详情页真桥链路坏了。真相是：
上一轮某个脚本退出时没把 vite 子进程收干净，一个 **`--mode mock` 的孤儿占了 5182**
（那是「非 mock」端口）。本脚本自己用 `--strictPort` 起不来，而就绪探测
`urlopen(5182)` 被那个孤儿答了 200 → 于是 ② 连上的是 **mock 页面 + 被忽略的 window.go stub**，
`testdl:1` 在 fixtures 里不存在 → chapters 为空 → 按钮文案等不到。
清掉孤儿后同一条命令 ①②③ 全绿。

两个缺口分别由这两个函数堵：
  - `start_dev`：进门前先断言端口是空的；不空就直接报错并给出排查命令，
    绝不把「别人应答的端口」当成自己的服务器。
  - `stop_dev`：SIGTERM → wait → SIGKILL → **回查端口确实关闭**，
    关不掉就报错，让下一轮不会莫名变红。
"""
import os
import atexit
import signal
import sys
import socket
import subprocess
import time
import urllib.request


def port_open(port, host="127.0.0.1", timeout=0.4):
    port = int(port)   # 有脚本把 PORT 写成字符串，socket.connect_ex 会炸
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.settimeout(timeout)
        return s.connect_ex((host, port)) == 0


def start_dev(front_dir, port, mock, out_dir, name="dev", ready_timeout=60):
    """起 vite（mock=True 走 dev:mock），返回 Popen。端口被占 = 硬失败，不复用别人的服务器。"""
    port = int(port)
    if port_open(port):
        raise RuntimeError(
            f"端口 {port} 已被占用（{name}）——多半是上一轮验收脚本泄漏的 vite。"
            f" 先清理：ps -eo pid,etimes,args | grep 'vite --mode' | grep -v grep 然后 kill；"
            f" 否则本脚本会连到别人的服务器，测出假红灯（见 _devserver.py 头部说明）。")
    args = ["npm", "run", "dev:mock" if mock else "dev", "--", "--port", str(port), "--strictPort"]
    log_path = os.path.join(out_dir, f"dev-{port}.log")
    log = open(log_path, "wb")
    proc = subprocess.Popen(args, cwd=front_dir, stdout=log, stderr=subprocess.STDOUT,
                            start_new_session=True)
    base = f"http://127.0.0.1:{port}"
    deadline = time.time() + ready_timeout
    while time.time() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(f"{name} dev server 提前退出 code={proc.returncode}，见 {log_path}")
        try:
            with urllib.request.urlopen(base + "/", timeout=2) as r:
                if r.status == 200:
                    # ⚠ 第五十六轮注册 atexit（只管正常退出），第六十一轮补 SIGTERM/SIGINT 收口。
                    # 原因不是"有人手滑"，而是**实测反复踩到的**：
                    # `sorani_verify.py` 与 `update_verify.py` 都没有 try/finally，
                    # 中途抛异常（本轮就是 Playwright 超时）就留下孤儿 vite 占住端口，
                    # 于是下一轮撞上「端口 5188 已被占用」，
                    # 而 run_all 的泄漏检测又把残留监听**误归属**给别的脚本。
                    # 更难看的是：sorani 自己的头部注释还写着「收口是 SIGTERM→wait→SIGKILL→回查端口」——
                    # 声明了一个代码里不存在的保证，这正是本任务一直在抓的那类错，这次在我自己的验收装置里。
                    #
                    # 为什么修在 _devserver 而不是逐个脚本补 finally：
                    # 靠"作者记得写"的规矩一定会再漏；注册在起服务器的那一刻才是结构性的。
                    # stop_dev 对已停的进程是幂等的（内部异常都吞），所以脚本自己
                    # 显式调 stop_dev 也不冲突。quiet_port=True：解释器退出阶段
                    # 不该因为"端口没关干净"再抛一次异常把真正的失败信息盖掉。
                    _MANAGED.append((proc, port, name))
                    _install_exit_hooks()
                    return proc
        except Exception:
            time.sleep(0.5)
    stop_dev(proc, port, name)
    raise RuntimeError(f"{name} dev server {ready_timeout}s 未就绪，见 {log_path}")


# ── 第六十一轮：被信号打断时的收口 ────────────────────────────────────────
# 第五十六轮我加的是 atexit。atexit 只在**正常退出**时跑：
# `timeout 120 python3 xxx_verify.py` 发的是 SIGTERM，Python 默认直接终止进程，
# atexit 一行都不执行 —— 于是 vite 留在端口上。这个洞本轮真的踩到了：
# 5206 被 59 分钟前那次被打断的 run 占着，detail_reserve_verify 开局就报
# 「端口 5206 已被占用」，而 run_all 已经白跑了近一小时。
# 所以光有 atexit 不够，必须把 SIGTERM/SIGINT 也接上。
_MANAGED = []          # [(proc, port, name)]
_HOOKED = [False]
_SIGNAL_HOOKS = True   # 关掉只留 atexit，用于反向验证（见 sigterm_leak_probe）


def _reap():
    """收掉本模块起过、还没收的所有 dev server。重复调用无害。"""
    while _MANAGED:
        proc, port, name = _MANAGED.pop(0)
        try:
            stop_dev(proc, port, name, True)
        except Exception:
            pass


def _install_exit_hooks():
    if _HOOKED[0]:
        return
    _HOOKED[0] = True
    atexit.register(_reap)
    if not _SIGNAL_HOOKS:
        return
    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        try:
            prev = signal.getsignal(sig)

            def _handler(signum, frame, _prev=prev):
                _reap()
                # 交还给原来的处理逻辑，别改变脚本自己的退出语义
                if callable(_prev):
                    _prev(signum, frame)
                elif _prev is signal.SIG_DFL:
                    signal.signal(signum, signal.SIG_DFL)
                    raise SystemExit(128 + signum)
                else:
                    raise SystemExit(128 + signum)

            signal.signal(sig, _handler)
        except (ValueError, OSError, RuntimeError):
            # 非主线程装不了信号处理器：atexit 仍在，退化为旧行为
            pass


def stop_dev(proc, port, name="dev", quiet_port=False):
    """收进程组：SIGTERM → wait(5) → SIGKILL → 回查端口。默认对「没关干净」报错。"""
    port = int(port)
    try:
        os.killpg(os.getpgid(proc.pid), signal.SIGTERM)
    except Exception:
        pass
    try:
        proc.wait(timeout=5)
    except Exception:
        try:
            os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
        except Exception:
            pass
        try:
            proc.wait(timeout=5)
        except Exception:
            pass
    deadline = time.time() + 5
    while port_open(port) and time.time() < deadline:
        time.sleep(0.2)
    if port_open(port) and not quiet_port:
        raise RuntimeError(
            f"{name} 退出后端口 {port} 仍在监听——泄漏的 vite 会让下一轮验收出假红灯，"
            f" 请手动 kill 掉 pid：ss -ltnp | grep {port}")


# ── 自带自检（run_all 预检会跑；纯 Python，不起 vite）────────────────────
# 测的是"收口机制"本身，不是 vite：把 stop_dev 换成写标记文件的桩，
# 用真信号（SIGTERM）打自己，看桩有没有被调用。
_SELFTEST_CHILD = """
import os, sys, signal
sys.path.insert(0, os.environ["ZGJ_VER_DIR"])
import _devserver as d
mark = os.environ["ZGJ_REAP_MARK"]
d.stop_dev = lambda *a, **k: open(mark, "a").write("reap\\n")
if os.environ.get("ZGJ_NO_SIGNAL") == "1":
    d._SIGNAL_HOOKS = False


class Fake:
    pid = os.getpid()


d._MANAGED.append((Fake(), 0, "selftest"))
d._install_exit_hooks()
mode = os.environ["ZGJ_MODE"]
if mode == "term":
    os.kill(os.getpid(), signal.SIGTERM)
elif mode == "exit":
    sys.exit(0)
print("SURVIVED")
"""


def _selftest():
    import subprocess
    import tempfile

    ver_dir = os.path.dirname(os.path.abspath(__file__))
    ok = True
    results = []

    def run(mode, no_signal=False):
        fd, mark = tempfile.mkstemp(prefix="zgj-reap-")
        os.close(fd)
        os.unlink(mark)
        env = dict(os.environ, ZGJ_VER_DIR=ver_dir, ZGJ_REAP_MARK=mark, ZGJ_MODE=mode)
        if no_signal:
            env["ZGJ_NO_SIGNAL"] = "1"
        r = subprocess.run([sys.executable, "-c", _SELFTEST_CHILD],
                           env=env, capture_output=True, text=True, timeout=30)
        reaped = os.path.exists(mark)
        n = open(mark).read().count("reap") if reaped else 0
        for f in (mark,):
            if os.path.exists(f):
                os.unlink(f)
        return r, reaped, n

    # A: SIGTERM 打断必须收口（第五十六轮的 atexit 单独做不到）
    r, reaped, n = run("term")
    a = reaped and n >= 1
    results.append(("A SIGTERM 打断后 stop_dev 被调用", a, "reap=%d rc=%d" % (n, r.returncode)))
    # B: 反向对照——只留 atexit 时确实不收口（证明 A 绿是新处理器带来的）
    r, reaped, n = run("term", no_signal=True)
    b = (not reaped)
    results.append(("B 关掉信号处理器后同一条不打（反向对照）", b, "reap=%d" % n))
    # C: 正常退出仍由 atexit 收口（别把老保证弄丢）
    r, reaped, n = run("exit")
    c = reaped and n >= 1
    results.append(("C 正常退出仍收口（atexit 未被拆掉）", c, "reap=%d" % n))
    # D: 信号路径下 stop_dev 只被调一次（重复收口会 killpg 已死进程）
    r, reaped, n = run("term")
    d2 = n == 1
    results.append(("D 一次打断只收口一次", d2, "reap=%d" % n))

    for name, passed, detail in results:
        print("%s %s (%s)" % ("PASS" if passed else "FAIL", name, detail))
        ok = ok and passed
    print("== _devserver selftest: %d/%d PASS ==" % (sum(1 for x in results if x[1]), len(results)))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(_selftest())

