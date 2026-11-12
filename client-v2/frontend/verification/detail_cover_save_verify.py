#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""detail_cover_save_verify.py —— 详情页「↓ 高清海报」（v1 main.py:1485-1530 对齐）验收。

态：**非 mock dev @5184 + window.go.main.App stub**（沿用 detail_download_verify 的真实态套路），
因为高清取图走的是 `App.Cover`，mock 面根本没有这条链路。

stub 记录每次 `Cover(dramaJSON, force)`，用 `window.__coverMode` 切分支：
  ok            所有请求都回 1x1 PNG data URL
  hires_fail    带 `~tplv-shrink:1600:0.image` 的高清候选 reject，小图正常回
  all_reject    全部 reject（走 failed 文案）
  small_empty   高清候选 reject 且小图 resolve("")（走 no-source 文案）

断言（任何 FAIL 退出码非 0）：
  ① 首个高清候选逐字 = https://p6-novel.byteimg.com/novel-pic/<32hex>~tplv-shrink:1600:0.image，
     且 force=true（v1 media.py:136-150 的模板与主机顺序）
  ② 第一候选失败 → 自动换 p3 同资源号重试（顺序不能反）
  ③ 两候选都失败 → 回落小图（cover=原地址, force=false）+ toast 含「没取到高清版」+ 仍下载
  ④ 下载文件名 = `<safe_name(剧名)> 海报.jpg`；非法字符（/ : * ?）被替换成 _
     （v1 decrypt.safe_name 口径）
  ⑤ cover 里没有 /novel-pic/<32hex> → 不发任何高清请求，直接小图并说明
  ⑥ 小图也拿不到 → toast「没拿到这张海报的原图，请先在剧库里刷新一次」且不产生下载
  ⑦ showSaveFilePicker 抛 AbortError（用户在系统保存框里取消）→
     toast「已取消保存海报」，不算失败、不下载；picker 成功 → 走 picker 不触发 <a download>
  ⑧ en 态按钮文案 = ↓ Hi-res poster
截图：detail-cover.png（zh 真实态下载）、detail-cover-en.png。

未尽项（必须真机复验，本机造不出来）：WebView2 的 showSaveFilePicker 实际弹框、
用户选定目录后的落盘结果，以及 `<a download>` 回落时 webview 下载目录的位置。
"""
import json
import os
import signal
import sys
import urllib.parse

from playwright.sync_api import sync_playwright

import _devserver

OUT = os.path.dirname(os.path.abspath(__file__))
FRONT = os.path.abspath(os.path.join(OUT, ".."))
PORT = 5184
BASE = f"http://127.0.0.1:{PORT}"

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


# 1x1 合法 PNG（base64）——stub 用它当「拿到的海报」，解码后尺寸断言 1x1
# 真 1x1 RGBA PNG（zlib 生成，CRC 正确）。此前手写的 base64 串解不出图，
# coverSave.imageSize 的 createImageBitmap 直接 reject → toast 显示「海报已保存（?）」。
PNG_1X1 = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg=="

STUB_TMPL = """
(() => {
  const DRAMA = %s;
  const CH = [{ currentEpisode: 1, title: "第1集", duration: "01:00" },
              { currentEpisode: 2, title: "第2集", duration: "01:00" }];
  const PNG = %s;
  window.__coverCalls = [];
  window.__coverMode = "ok";
  window.go = { main: { App: {
    Detail: () => Promise.resolve(JSON.stringify({ drama: DRAMA, chapters: CH, warning: "" })),
    Cover: (dramaJSON, force) => {
      let cover = "";
      try { cover = (JSON.parse(dramaJSON) || {}).cover || ""; } catch (e) {}
      const rec = { cover, force: !!force };
      window.__coverCalls.push(rec);
      const hires = cover.indexOf("~tplv-shrink:1600:0.image") >= 0;
      if (window.__coverMode === "all_reject") return Promise.reject(new Error("核心封面失败 [cover]"));
      // small_empty = 高清候选全部拿不到 **且** 小图也返回空串（⑥ 的 no-source 路径）
      if (hires && (window.__coverMode === "hires_fail" || window.__coverMode === "small_empty"))
        return Promise.reject(new Error("403 signature"));
      if (!hires && window.__coverMode === "small_empty") return Promise.resolve("");
      return Promise.resolve(PNG);
    },
    DownloadStart: () => Promise.resolve(JSON.stringify({ added: 0 })),
  } } };
})();
"""


# headless chromium 里 window.showSaveFilePicker **存在但必定 reject AbortError**
# （实测：探针点一次就得到「已取消保存海报」）→ 不显式关掉它，`<a download>` 分支
# 永远不会被走到，①③④⑤ 全都会红。所以基础 stub 之后再叠一层「把 picker 置空」，
# 只有 ⑦ 自己覆盖它来测 picker 路径。
NO_PICKER_JS = """
(() => {
  Object.defineProperty(window, 'showSaveFilePicker', { value: undefined, configurable: true, writable: true });
})();
"""


def stub_js(drama, keep_picker=False):
    body = STUB_TMPL % (json.dumps(drama, ensure_ascii=False), json.dumps(PNG_1X1))
    return body if keep_picker else body + NO_PICKER_JS


def detail_url(drama):
    return (f"{BASE}/#/detail/" + urllib.parse.quote(drama["id"])
            + "?d=" + urllib.parse.quote(json.dumps(drama, ensure_ascii=False)))


def make_drama(title="测试海报剧", cover=""):
    asset = "0123456789abcdef0123456789abcdef"
    if cover == "hires":
        cover = f"https://p6-novel.byteimg.com/novel-pic/{asset}~tplv-shrink:400:572.image"
    elif cover == "plain":
        cover = "https://example.com/other/pic-400.jpg"
    elif cover == "weird":
        cover = f"https://p3-novel.byteimg.com/novel-pic/{asset}~tplv-shrink:400:572.image"
    return {"id": "testcover:1", "source": "testcover", "sourceId": "c1", "title": title,
            "description": "探针简介", "cover": cover, "episodes": 2, "category": "",
            "vip": None, "heat": "", "views": "", "onlineDate": "", "tags": [],
            "releaseStatus": ""}


HIRES_P6 = "https://p6-novel.byteimg.com/novel-pic/0123456789abcdef0123456789abcdef~tplv-shrink:1600:0.image"
HIRES_P3 = "https://p3-novel.byteimg.com/novel-pic/0123456789abcdef0123456789abcdef~tplv-shrink:1600:0.image"


def open_detail(page, drama):
    page.goto(detail_url(drama))
    page.wait_for_selector(".actions .btn", timeout=20000)
    page.wait_for_timeout(800)
    page.evaluate("() => { window.__coverCalls.length = 0; }")


def click_save(page, label="高清海报"):
    return page.locator(".actions .btn", has_text=label).first


def main():
    proc = _devserver.start_dev(FRONT, PORT, False, OUT, name="detail-cover")
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(
                executable_path="/home/ryen/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell")

            # ═══ ① + ④ 高清成功：候选模板逐字、force=true、下载文件名 ═══
            d = make_drama("测试海报剧", cover="hires")
            ctx = browser.new_context(viewport={"width": 1000, "height": 760}, accept_downloads=True)
            ctx.add_init_script(stub_js(d))
            page = ctx.new_page()
            open_detail(page, d)
            with page.expect_download(timeout=15000) as dl:
                click_save(page).click()
                toast = page.wait_for_selector(".toast:has-text('海报已保存')", timeout=15000)
            download = dl.value
            calls = page.evaluate("() => window.__coverCalls")
            first = calls[0] if calls else {}
            check("① 首个高清候选逐字 = p6 模板", first.get("cover") == HIRES_P6, f"cover={first.get('cover')!r}")
            check("① 高清请求 force=true", first.get("force") is True, f"force={first.get('force')}")
            check("① 一次成功就不再试 p3", len([c for c in calls if "1600" in c["cover"]]) == 1, f"calls={len(calls)}")
            check("④ 下载文件名 = 测试海报剧 海报.jpg",
                  download.suggested_filename == "测试海报剧 海报.jpg", f"name={download.suggested_filename!r}")
            text = (toast.inner_text() if toast else "")
            check("① toast 含解码尺寸 1x1 且不带降级说明",
                  "1x1" in text and "没取到高清版" not in text, f"toast={text[:80]}")
            page.screenshot(path=os.path.join(OUT, "detail-cover.png"))
            ctx.close()

            # ═══ ② + ③ 高清失败 → 换 p3 → 回落小图并说明 ═══
            d2 = make_drama("测试海报剧", cover="hires")
            ctx2 = browser.new_context(viewport={"width": 1000, "height": 760}, accept_downloads=True)
            ctx2.add_init_script(stub_js(d2))
            page2 = ctx2.new_page()
            open_detail(page2, d2)
            page2.evaluate("() => { window.__coverMode = 'hires_fail'; }")
            with page2.expect_download(timeout=15000) as dl2:
                click_save(page2).click()
                page2.wait_for_selector(".toast:has-text('没取到高清版')", timeout=15000)
            calls2 = page2.evaluate("() => window.__coverCalls")
            hi = [c["cover"] for c in calls2 if "1600" in c["cover"]]
            small = [c for c in calls2 if "1600" not in c["cover"]]
            check("② 候选顺序 p6 → p3（同资源号）", hi == [HIRES_P6, HIRES_P3], f"hi={hi}")
            check("③ 回落小图用原 cover 且 force=false",
                  len(small) == 1 and small[0]["cover"] == d2["cover"] and small[0]["force"] is False,
                  f"small={small}")
            check("③ 仍然把小图存下来了", dl2.value.suggested_filename == "测试海报剧 海报.jpg")
            ctx2.close()

            # ═══ ④b 非法字符清洗（v1 safe_name） ═══
            d3 = make_drama('A/B:C*?剧<>|  ', cover="hires")
            ctx3 = browser.new_context(viewport={"width": 1000, "height": 760}, accept_downloads=True)
            ctx3.add_init_script(stub_js(d3))
            page3 = ctx3.new_page()
            open_detail(page3, d3)
            with page3.expect_download(timeout=15000) as dl3:
                click_save(page3).click()
                page3.wait_for_selector(".toast:has-text('海报已保存')", timeout=15000)
            name3 = dl3.value.suggested_filename
            # v1 safe_name：/ : * ? < > | 逐个换成 _，再 strip(" .")（空格清掉、下划线留）
            check("④b 非法字符逐个换成 _ 且尾部空格清掉（v1 decrypt.py:33-34 口径）",
                  name3 == "A_B_C__剧___ 海报.jpg", f"name={name3!r}")
            ctx3.close()

            # ═══ ⑤ 认不出资源号 → 不试高清，直接小图并说明 ═══
            d4 = make_drama("无签名剧", cover="plain")
            ctx4 = browser.new_context(viewport={"width": 1000, "height": 760}, accept_downloads=True)
            ctx4.add_init_script(stub_js(d4))
            page4 = ctx4.new_page()
            open_detail(page4, d4)
            with page4.expect_download(timeout=15000) as dl4:
                click_save(page4).click()
                page4.wait_for_selector(".toast:has-text('没取到高清版')", timeout=15000)
            calls4 = page4.evaluate("() => window.__coverCalls")
            check("⑤ 未发出任何 1600 高清请求", all("1600" not in c["cover"] for c in calls4), f"calls={calls4}")
            ctx4.close()

            # ═══ ⑥ 连小图都空 → no-source 文案，且不下载 ═══
            d5 = make_drama("无原图剧", cover="hires")
            ctx5 = browser.new_context(viewport={"width": 1000, "height": 760}, accept_downloads=True)
            ctx5.add_init_script(stub_js(d5))
            page5 = ctx5.new_page()
            open_detail(page5, d5)
            page5.evaluate("() => { window.__coverMode = 'small_empty'; }")
            got_download = False
            try:
                with page5.expect_download(timeout=2500):
                    click_save(page5).click()
                got_download = True          # with 正常退出 = 确实等到了下载
            except Exception:
                pass                         # 超时 = 没有下载，正是这里要的结果
            page5.wait_for_selector(".toast:has-text('没拿到这张海报的原图')", timeout=8000)
            check("⑥ 无原图时报文案、不假装下载", not got_download, f"download={got_download}")
            ctx5.close()

            # ═══ ⑦ picker 取消 ≠ 失败；picker 成功则不走 <a download> ═══
            d6 = make_drama("取消剧", cover="hires")
            ctx6 = browser.new_context(viewport={"width": 1000, "height": 760}, accept_downloads=True)
            ctx6.add_init_script(stub_js(d6) + """
(() => {
  window.__pickerCalls = 0;
  Object.defineProperty(window, 'showSaveFilePicker', { configurable: true, writable: true,
    value: (opts) => {
      window.__pickerCalls++;
      window.__pickerOpts = opts;
      return Promise.reject(Object.assign(new Error('cancel'), { name: 'AbortError' }));
    } });
})();
""")
            page6 = ctx6.new_page()
            open_detail(page6, d6)
            clicked = []
            page6.on("download", lambda dn: clicked.append(dn.suggested_filename))
            click_save(page6).click()
            page6.wait_for_selector(".toast:has-text('已取消保存海报')", timeout=8000)
            page6.wait_for_timeout(600)
            pk_calls = page6.evaluate("() => window.__pickerCalls")
            pk_opts = page6.evaluate("() => window.__pickerOpts || null")
            check("⑦ picker 被调用且带上建议文件名",
                  pk_calls == 1 and (pk_opts or {}).get("suggestedName") == "取消剧 海报.jpg",
                  f"calls={pk_calls} opts={pk_opts}")
            check("⑦ 取消不产生下载、不报失败", clicked == [], f"downloads={clicked}")
            ctx6.close()

            # ═══ ⑦b picker 成功：走 picker，不触发 <a download>，同样报「已保存」 ═══
            d7 = make_drama("写盘剧", cover="hires")
            ctx7b = browser.new_context(viewport={"width": 1000, "height": 760}, accept_downloads=True)
            ctx7b.add_init_script(stub_js(d7) + """
(() => {
  window.__written = 0;
  window.__closed = 0;
  Object.defineProperty(window, 'showSaveFilePicker', { configurable: true, writable: true,
    value: (opts) => {
      window.__lastOpts = opts;
      return Promise.resolve({ createWritable: () => Promise.resolve({
        write: () => { window.__written++; return Promise.resolve(); },
        close: () => { window.__closed++; return Promise.resolve(); } }) });
    } });
})();
""")
            page7b = ctx7b.new_page()
            open_detail(page7b, d7)
            dl_hits = []
            page7b.on("download", lambda dn: dl_hits.append(dn.suggested_filename))
            click_save(page7b).click()
            page7b.wait_for_selector(".toast:has-text('海报已保存')", timeout=10000)
            page7b.wait_for_timeout(500)
            check("⑦b picker 成功后 write+close 各 1 次",
                  page7b.evaluate("() => window.__written") == 1 and page7b.evaluate("() => window.__closed") == 1,
                  f"written={page7b.evaluate('() => window.__written')} closed={page7b.evaluate('() => window.__closed')}")
            check("⑦b picker 成功时不再触发 <a download>", dl_hits == [], f"downloads={dl_hits}")
            check("⑦b picker 带上 jpg 过滤器",
                  (page7b.evaluate("() => window.__lastOpts") or {}).get("types", [{}])[0]["accept"].get("image/jpeg") == [".jpg", ".jpeg"],
                  f"opts={page7b.evaluate('() => window.__lastOpts')}")
            ctx7b.close()

            # ═══ ⑧ en 态按钮文案 ═══
            ctx7 = browser.new_context(viewport={"width": 1000, "height": 760})
            ctx7.add_init_script(stub_js(d) + "\nlocalStorage.setItem('zgj.locale','en');")
            page7 = ctx7.new_page()
            page7.goto(BASE + "/#/discover")
            page7.wait_for_selector(".page-title")
            open_detail(page7, d)
            label = click_save(page7, "Hi-res poster").inner_text().strip()
            check("⑧ en 态按钮 = ↓ Hi-res poster", label == "↓ Hi-res poster", f"label={label!r}")
            page7.screenshot(path=os.path.join(OUT, "detail-cover-en.png"))
            ctx7.close()

            browser.close()
    finally:
        _devserver.stop_dev(proc, PORT, name="detail-cover")

    print(f"\n===== detail_cover_save_verify: {'ALL PASSED' if not failures else str(len(failures)) + ' FAIL'} =====")
    for f in failures:
        print("FAILED:", f)
    sys.exit(1 if failures else 0)


if __name__ == "__main__":
    main()
