#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""v2-I 验证：详情页「下载当集/下载全部」双轨（headless chromium，自管理两台 dev server）。

轨道判定 = api.ts 模块加载时的 isMock/hasBridge：
  - dev:mock @5181（VITE_MOCK=1）→ mockApi/local.ts 暂存（旧行为不回归）
  - dev @5182（非 mock）+ add_init_script 注入 window.go.main.App stub
    → hasBridge=true → realApi → stub 的 App.DownloadStart（bindings_download.go:42
      DownloadStart(dramaJSON, entriesJSON「[{chapter,index}]」, quality) → {added}）

断言（任何 FAIL 退出码非 0）：
  ① mock 态点「下载全部」→ localStorage 'downloads.queue' 长度=按钮 N 集、旧 toast
     文案「本地队列/尚未开始下载」在；「下载当集」同理（去重文案「已在队列中」）
  ② stub 记录 payload：dramaJSON.id/source 正确、entries 数=chapters 数、index 全 ≥1
     连续、quality 参数=0；真实态 toast「已加入下载队列 8 集」；点 toast 按钮跳 /#/downloads
  ③ stub reject「磁盘剩余不足…[disk_full]」→ toast 含「磁盘剩余不足」且不含「已加入」、
     localStorage 'downloads.queue' 仍空 —— 失败可见、不假成功
截图：本目录 detail-download.png（②真实态成功 toast）、detail-download-mock.png（①）。
"""
import json
import os
import signal
import subprocess
import sys
import time
import urllib.parse
import urllib.request

from playwright.sync_api import sync_playwright

EXE = "/home/ryen/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
OUT = os.path.dirname(os.path.abspath(__file__))
FRONT = os.path.abspath(os.path.join(OUT, ".."))
MOCK_BASE = "http://127.0.0.1:5181"
REAL_BASE = "http://127.0.0.1:5182"

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


def start_server(name, port, mock):
    args = ["npm", "run", "dev:mock" if mock else "dev", "--", "--port", str(port), "--strictPort"]
    log = open(os.path.join(OUT, f"dev-{port}.log"), "wb")
    proc = subprocess.Popen(args, cwd=FRONT, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
    base = f"http://127.0.0.1:{port}"
    deadline = time.time() + 60
    while time.time() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(f"{name} dev server 提前退出 code={proc.returncode}，见 dev-{port}.log")
        try:
            with urllib.request.urlopen(base + "/", timeout=2) as r:
                if r.status == 200:
                    return proc
        except Exception:
            time.sleep(0.5)
    proc.kill()
    raise RuntimeError(f"{name} dev server 60s 未就绪，见 dev-{port}.log")


def stop_server(proc):
    try:
        os.killpg(os.getpgid(proc.pid), signal.SIGTERM)
        proc.wait(timeout=10)
    except Exception:
        try:
            os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
        except Exception:
            pass


# ---------- 真实态 stub（window.go.main.App，api.ts hasBridge 模块加载即读） ----------
# 最小面：Detail + DownloadStart。未 stub 方法 → ApiError bridge_missing →
# watch store 回落 local / loadCreators 静默 catch，页面不崩（见 api.ts:85 注释）。
STUB_JS = """
(() => {
  const DRAMA = { id: "testdl:1", source: "testdl", sourceId: "t1", title: "测试下载剧",
                  description: "", cover: "", episodes: 8, vip: null, tags: [] };
  const CH = [];
  for (let i = 1; i <= 8; i++) CH.push({ currentEpisode: i, title: "第" + i + "集", duration: "01:00" });
  window.__dlCalls = [];
  window.__dlMode = "ok";            // ok → resolve {added:n}；fail → reject 磁盘错误
  window.go = { main: { App: {
    Detail: () => Promise.resolve(JSON.stringify({ drama: DRAMA, chapters: CH, warning: "" })),
    DownloadStart: (dramaJSON, entriesJSON, quality) => {
      window.__dlCalls.push({ dramaJSON, entriesJSON, quality });
      if (window.__dlMode === "fail") {
        return Promise.reject(new Error("磁盘剩余不足，请先释放磁盘空间后再试 [disk_full]"));
      }
      const n = JSON.parse(entriesJSON).length;
      return Promise.resolve(JSON.stringify({ added: n }));
    }
  } } };
})();
"""

DRAMA_Q = {"id": "testdl:1", "source": "testdl", "sourceId": "t1", "title": "测试下载剧",
           "description": "", "cover": "", "episodes": 8, "category": "", "vip": None,
           "heat": "", "views": "", "onlineDate": "", "tags": [], "releaseStatus": ""}
DETAIL_URL = (REAL_BASE + "/#/detail/" + urllib.parse.quote("testdl:1")
              + "?d=" + urllib.parse.quote(json.dumps(DRAMA_Q, ensure_ascii=False)))


def queue_len(page):
    raw = page.evaluate("() => localStorage.getItem('downloads.queue')")
    if not raw:
        return 0
    try:
        return len(json.loads(raw))
    except Exception:
        return -1


def main():
    mock_proc = start_server("mock", 5181, True)
    real_proc = start_server("real", 5182, False)
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE)

            # ================= ① mock 态：旧本地暂存不回归 =================
            ctx = browser.new_context(viewport={"width": 900, "height": 700})
            page = ctx.new_page()
            page.goto(MOCK_BASE + "/#/discover")
            page.wait_for_selector(".page-title")
            page.get_by_text("闪婚老公是大佬", exact=True).first.click()
            page.wait_for_url(__import__("re").compile(r"/#/detail/"), timeout=15000)
            page.wait_for_selector("text=/下载全部（\\d+ 集）/", timeout=15000)
            all_btn = page.locator("button", has_text="下载全部").inner_text()
            n_mock = int(__import__("re").search(r"(\d+)", all_btn).group(1))
            page.evaluate("() => localStorage.removeItem('downloads.queue')")

            page.locator("button", has_text="下载全部").first.click()
            page.wait_for_selector(".toast", timeout=8000)
            toast = page.inner_text(".toast")
            qlen = queue_len(page)
            check("① mock 下载全部 → 本地队列条数=按钮集数", qlen == n_mock, f"queue={qlen} 按钮N={n_mock}")
            check("① mock toast 旧文案「本地队列」在", "本地队列" in toast, f"toast={toast}")
            check("① mock toast 声明尚未真实下载", "尚未开始" in toast, f"toast={toast}")

            page.locator("button", has_text="下载当集").first.click()
            page.wait_for_selector(".toast:has-text('已在队列中')", timeout=10000)
            toast2 = page.inner_text(".toast")
            check("① mock 下载当集：去重文案「已在队列中」", "已在队列中" in toast2, f"toast={toast2}")
            check("① mock 去重后队列条数不变", queue_len(page) == n_mock, f"queue={queue_len(page)}")
            page.screenshot(path=os.path.join(OUT, "detail-download-mock.png"))
            ctx.close()

            # ================= 真实桥 stub 态 =================
            ctx2 = browser.new_context(viewport={"width": 900, "height": 700})
            ctx2.add_init_script(STUB_JS)
            rp = ctx2.new_page()

            # ---------- ② 真实态：DownloadStart payload + toast + 跳转 ----------
            rp.goto(DETAIL_URL)
            rp.wait_for_selector("text=/下载全部（8 集）/", timeout=15000)
            rp.evaluate("() => { localStorage.removeItem('downloads.queue'); window.__dlCalls.length = 0; }")

            rp.locator("button", has_text="下载全部").first.click()
            rp.wait_for_selector(".toast:has-text('已加入下载队列')", timeout=8000)
            toast = rp.inner_text(".toast")
            check("② 真实态 toast「已加入下载队列 8 集」", "已加入下载队列 8 集" in toast, f"toast={toast}")

            calls = rp.evaluate("() => window.__dlCalls")
            check("② DownloadStart 被调用 1 次（8 集 < 500 分块阈值）", len(calls) == 1, f"calls={len(calls)}")
            if calls:
                c = calls[0]
                d = json.loads(c["dramaJSON"])
                es = json.loads(c["entriesJSON"])
                check("② payload.dramaJSON.id/source 正确", d.get("id") == "testdl:1" and d.get("source") == "testdl",
                      f"id={d.get('id')} source={d.get('source')}")
                check("② payload.entries 数=chapters 数(8)", len(es) == 8, f"entries={len(es)}")
                idxs = [e.get("index") for e in es]
                check("② entries.index 全为 1-based 真实集号", idxs == list(range(1, 9)), f"index={idxs}")
                check("② entries.chapter 携带分集对象", all(isinstance(e.get("chapter"), dict) and e["chapter"].get("title") for e in es))
                check("② quality 参数=0（自动最高档）", c["quality"] == 0, f"quality={c['quality']}")

            # toast 跳转按钮 → /#/downloads
            check("② toast 带「查看下载队列」按钮", rp.locator(".toast-action").count() == 1)
            rp.locator(".toast-action").click()
            rp.wait_for_url(__import__("re").compile(r"/#/downloads"), timeout=8000)
            check("② 点击 toast 按钮跳转 /#/downloads", "/#/downloads" in rp.url, f"url={rp.url}")
            rp.go_back()
            rp.wait_for_selector("text=/下载全部（8 集）/", timeout=15000)
            rp.evaluate("() => { window.__dlCalls.length = 0; }")
            rp.locator("button", has_text="下载当集").first.click()
            rp.wait_for_function("() => window.__dlCalls.length >= 1", timeout=8000)
            c1 = rp.evaluate("() => window.__dlCalls")[0]
            e1 = json.loads(c1["entriesJSON"])
            check("② 下载当集：entries 长度=1 且 index≥1", len(e1) == 1 and e1[0]["index"] >= 1,
                  f"entries={json.dumps(e1, ensure_ascii=False)[:120]}")
            rp.screenshot(path=os.path.join(OUT, "detail-download.png"))

            # ---------- ③ 失败透出：reject 磁盘错误 → 可见、不假成功 ----------
            rp.evaluate("() => { localStorage.removeItem('downloads.queue'); window.__dlCalls.length = 0; window.__dlMode = 'fail'; }")
            rp.locator("button", has_text="下载全部").first.click()
            rp.wait_for_selector(".toast:has-text('磁盘剩余不足')", timeout=8000)
            toast3 = rp.inner_text(".toast")
            check("③ 失败 toast 含核心中文错误「磁盘剩余不足」", "磁盘剩余不足" in toast3, f"toast={toast3}")
            check("③ 失败 toast 前缀「加入下载队列失败」", "加入下载队列失败" in toast3, f"toast={toast3}")
            check("③ 错误文案 [disk_full] 尾码被剥离", "[disk_full]" not in toast3, f"toast={toast3}")
            check("③ 不假成功：无「已加入」字样", "已加入" not in toast3, f"toast={toast3}")
            check("③ 失败时不落本地暂存队列（不双写假象）", queue_len(rp) == 0, f"queue={queue_len(rp)}")
            check("③ 失败仍调用了桥（1 次）", len(rp.evaluate("() => window.__dlCalls")) == 1)
            rp.screenshot(path=os.path.join(OUT, "detail-download-fail.png"))

            browser.close()
    finally:
        stop_server(mock_proc)
        stop_server(real_proc)

    print(f"\n{'ALL PASS' if not failures else 'FAILURES: ' + ', '.join(failures)}")
    sys.exit(0 if not failures else 1)


if __name__ == "__main__":
    main()
