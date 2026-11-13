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
                    # ⚠ 第五十六轮加的兜底：`start_dev` 成功返回后自己注册 atexit 收口。
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
                    atexit.register(stop_dev, proc, port, name, True)
                    return proc
        except Exception:
            time.sleep(0.5)
    stop_dev(proc, port, name)
    raise RuntimeError(f"{name} dev server {ready_timeout}s 未就绪，见 {log_path}")


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
