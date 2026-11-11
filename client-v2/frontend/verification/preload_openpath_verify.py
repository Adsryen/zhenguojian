#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
preload_openpath_verify.py — 预缓冲 + 打开下载目录 前端接线断言（dev:mock 5187）
①下载行「打开所在目录」按钮不再 disabled，点击落 __zgjOpenPath=dir/folder
②归档行同款按钮可点
③播放器真实会话（window.go stub + api 非 mock 太复杂）→ mock 态验证 preload 不发（demoMode 守卫）
   真桥 preload 走代码审查 + vue-tsc；此处断言 stub 桥下 PlayerOpen 成功后
   PreloadEpisode 被调用一次（用 window.go.main.App stub + __zgjPreload 钩子）。
"""
import asyncio, json, os, re, signal, socket, subprocess, sys, tempfile, time, urllib.parse

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = os.environ.get("VERIFY_PORT", "5187")
BASE = f"http://127.0.0.1:{PORT}"
EXEC = os.path.expanduser("~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell")
PIDF = os.path.join(tempfile.gettempdir(), f"zgj-po-{PORT}.pid")
RESULTS = []

def check(name, ok, detail=""):
    RESULTS.append((name, ok))
    print(f"[{'PASS' if ok else 'FAIL'}] {name}" + (f" | {detail}" if detail else ""))

def port_open():
    with socket.socket() as s:
        s.settimeout(0.3)
        return s.connect_ex(("127.0.0.1", int(PORT))) == 0

def start_server():
    if port_open():
        return None
    log = open(os.path.join(tempfile.gettempdir(), "zgj_po_vite.log"), "w")
    proc = subprocess.Popen(["npm", "run", "dev:mock", "--", "--port", PORT, "--strictPort"],
                            cwd=FRONTEND, stdout=log, stderr=log, preexec_fn=os.setsid)
    open(PIDF, "w").write(str(proc.pid))
    for _ in range(60):
        if port_open():
            return proc
        time.sleep(0.5)
    raise RuntimeError("vite 未启动")

def start_plain(port, pidf):
    log = open(os.path.join(tempfile.gettempdir(), f"zgj_po_vite_{port}.log"), "w")
    proc = subprocess.Popen(["npm", "run", "dev", "--", "--port", str(port), "--strictPort"],
                            cwd=FRONTEND, stdout=log, stderr=log, preexec_fn=os.setsid)
    open(pidf, "w").write(str(proc.pid))
    for _ in range(80):
        with socket.socket() as so:
            so.settimeout(0.3)
            if so.connect_ex(("127.0.0.1", port)) == 0:
                return proc
        time.sleep(0.5)
    raise RuntimeError("plain vite 未启动")

def stop_server(proc):
    if proc:
        try:
            os.killpg(os.getpgid(proc.pid), signal.SIGTERM)
        except Exception:
            pass
        if os.path.exists(PIDF):
            os.remove(PIDF)

STUB = """
window.__zgjPreload = []
window.__zgjOpenPath = ''
const snap = {playing:true,posMs:0,durationMs:0,speed:1,volume:100,danmaku:{enabled:true,count:0,note:''},episode:{dramaID:'x',title:'',index:1}}
window.go = { main: { App: {
  Ping: async () => 'pong',
  ListSources: async () => JSON.stringify({items:[]}),
  PlayerOpen: async () => JSON.stringify(snap),
  PlayerState: async () => JSON.stringify(snap),
  PlayerSetBounds: async () => '', PlayerAttach: async () => '', PlayerDetach: async () => '',
  PreloadEpisode: async (d, c, i, q) => { window.__zgjPreload.push([JSON.parse(d).title, i, q]); return '{}' },
  OpenDownloadPath: async (p) => { window.__zgjOpenPath = p; return '' },
  DownloadList: async () => JSON.stringify({jobs:[],merges:[]}),
  DownloadDirectory: async () => JSON.stringify({directory:'C:/dl',exists:true}),
  Detail: async () => JSON.stringify({drama:{id:'hongguo:1',title:'T',source:'hongguo',episodes:2},
      chapters:[{id:'hongguo:1:1',title:'1',currentEpisode:1},{id:'hongguo:1:2',title:'2',currentEpisode:2}]}),
} } }
window.runtime = { EventsOn: () => () => {}, EventsOff: () => {}, EventsEmit: () => {} }
"""

async def main():
    proc = start_server()
    try:
        import pathlib
        from playwright.async_api import async_playwright
        async with async_playwright() as pw:
            b = await pw.chromium.launch(executable_path=EXEC if os.path.exists(EXEC) else None, headless=True)
            # ---- A. mock 下载页：按钮可点 + 钩子记录 ----
            ctx = await b.new_context(viewport={"width": 1440, "height": 900})
            pg = await ctx.new_page()
            await pg.goto(f"{BASE}/#/downloads")
            await pg.wait_for_timeout(2500)
            btns = pg.locator("[data-testid=row-open]")
            n = await btns.count()
            check("A1 完成行存在打开按钮", n >= 1, f"n={n}")
            if n >= 1:
                dis = await btns.first.is_disabled()
                check("A2 按钮不再 disabled", not dis)
                await btns.first.click()
                await pg.wait_for_timeout(400)
                op = await pg.evaluate("window.__zgjOpenPath")
                check("A3 点击落 openDownloadPath(dir/folder)", isinstance(op, str) and re.search(r"ZhenguoJian.downloads", op) and "hongguo/mock" in op, f"path={op}")
            # ---- B. 非 mock dev server + window.go stub：PlayerOpen 成功后 PreloadEpisode 恰 1 次 ----
            proc2 = start_plain(int(PORT) + 1, PIDF + ".b")
            try:
                ctx2 = await b.new_context(viewport={"width": 1440, "height": 900})
                await ctx2.add_init_script(STUB)
                pg2 = await ctx2.new_page()
                await pg2.goto(f"http://127.0.0.1:{int(PORT)+1}/#/player?d=" + urllib.parse.quote(json.dumps({"id": "hongguo:1", "title": "T", "source": "hongguo", "episodes": 2}, ensure_ascii=False)) + "&ep=1")
                await pg2.wait_for_timeout(4000)
                pre = await pg2.evaluate("window.__zgjPreload")
                check("B1 桥态预缓冲下一集 1 次", isinstance(pre, list) and len(pre) == 1, f"preloads={pre}")
                if pre:
                    check("B2 预缓冲参数=第2集/quality auto→0", pre[0][1] == 2 and pre[0][2] == 0, str(pre[0]))
            finally:
                stop_server(proc2)
            await b.close()
    finally:
        stop_server(proc)
    failed = [r for r in RESULTS if not r[1]]
    print(f"\n===== preload_openpath: {len(RESULTS)-len(failed)}/{len(RESULTS)} PASS =====")
    return 1 if failed else 0

sys.exit(asyncio.run(main()))
