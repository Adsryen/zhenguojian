#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""V2-6 右键上下文菜单验证（headless chromium + VITE_MOCK dev server）。

自管理 dev:mock：独占端口 5196，启停走 `_devserver`（端口预检 + 严格收口）。
     旧写法要求人工「先起 5173」，且 5173 被多个脚本共用——没人起就报错、
     有人起就可能测到别人的服务器。
截图输出：本目录 contextmenu-copy.png / contextmenu-input.png。
断言以 PASS/FAIL 行打印，任何 FAIL 退出码非 0。

场景C 是第三十五轮补的：v1 简介控件右键菜单有「复制全部简介」，v2 之前只有详情页按钮、菜单里没有。
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)

from playwright.sync_api import sync_playwright

import _devserver

PORT = 5196
BASE = f"http://127.0.0.1:{PORT}"
EXE = "/home/ryen/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell"
OUT = os.path.dirname(os.path.abspath(__file__))

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line)
    if not cond:
        failures.append(name)


def input_hit(page, inp):
    """每次右键前**重算**输入框命中点，并确认这点真的落在输入框上。
    复用早先抓的坐标会飘：搜索页联想下拉出现/收起会让布局移动，
    实测有一次点到 `BUTTON.ss-btn`（站源开关）上，于是菜单里只有「全选」，
    粘贴断言等 30s 超时——看起来像产品坏了，其实是测试自己的坐标过期了。"""
    box = inp.bounding_box()
    x = box["x"] + min(36, box["width"] / 2)
    y = box["y"] + box["height"] / 2
    who = page.evaluate(
        "([x,y]) => { const el = document.elementFromPoint(x, y);"
        " return el ? el.tagName : null }", [x, y])
    return x, y, who

def item_labels(page):
    return page.eval_on_selector_all(
        ".zgj-ctx-item > span:first-child", "els=>els.map(e=>e.textContent)"
    )


def main():
    proc = _devserver.start_dev(FRONTEND, PORT, True, HERE, name='contextmenu')
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE)
            ctx = browser.new_context(viewport={"width": 1440, "height": 900})
            ctx.grant_permissions(["clipboard-read", "clipboard-write"], origin=BASE)
            page = ctx.new_page()

            # ---- 场景 A：选中文本 → 右键 → 复制 ----
            page.goto(BASE + "/#/discover")
            page.wait_for_selector(".page-title")
            selected = page.evaluate(
                """() => {
                  const el = document.querySelector('.page-title');
                  const r = document.createRange();
                  r.selectNodeContents(el);
                  const s = getSelection();
                  s.removeAllRanges();
                  s.addRange(r);
                  return s.toString();
                }"""
            )
            check("场景A 选区非空", len(selected.strip()) > 0, f"sel={selected!r}")
            box = page.locator(".page-title").bounding_box()
            page.mouse.click(box["x"] + box["width"] / 2, box["y"] + box["height"] / 2, button="right")
            page.wait_for_selector(".zgj-ctx-menu")
            labels = item_labels(page)
            check("场景A 菜单含「复制」", "复制" in labels, f"labels={labels}")
            check("场景A 菜单含「全选」", "全选" in labels)
            hints = page.eval_on_selector_all(".zgj-ctx-key", "els=>els.map(e=>e.textContent)")
            check("场景A 快捷键提示", hints[0] == "Ctrl+C", f"hints={hints}")
            page.screenshot(path=os.path.join(OUT, "contextmenu-copy.png"))
            page.locator(".zgj-ctx-item", has_text="复制").click()
            check("场景A 点击后菜单关闭", page.locator(".zgj-ctx-menu").count() == 0)
            clip = page.evaluate("navigator.clipboard.readText()")
            check("场景A 剪贴板==选区", clip == selected, f"clip={clip!r}")

            # Escape 关闭断言（菜单重开后按 Escape）
            page.mouse.click(box["x"] + box["width"] / 2, box["y"] + box["height"] / 2, button="right")
            page.wait_for_selector(".zgj-ctx-menu")
            page.keyboard.press("Escape")
            check("场景A Escape 关闭", page.locator(".zgj-ctx-menu").count() == 0)

            # 纯媒体区无选区不弹（任务书空菜单路径）
            page.evaluate(
                """() => {
                  const s = getSelection();
                  s.removeAllRanges();
                  const r = document.createRange();
                  const img = document.querySelector('img');
                  if (img) { r.setStart(img, 0); r.collapse(true); }
                }"""
            )
            # ⚠ 这条原本写成 `if img.count() > 0:`——**条件式断言就是静默漏测**。
            # 我是对账自己发现的：声明 17 条、实跑只打 16 条 PASS，差的正是这条
            # （那一页恰好没 img，于是"媒体区不弹菜单"整段被跳过，脚本照样 ALL PASS）。
            # 改成：拿一定会有封面的详情页；找不到 img 就直接判 FAIL 并说明原因，
            # 而不是把没测到的东西算成测过了。
            page.goto(BASE + "/#/detail/hongguo%3A7439220582928952383")
            page.wait_for_timeout(1800)
            # 命中点必须**真的落在 img 上**：详情头图是 overflow 放大的，
            # 光按 bounding_box 中心点去点会打到父容器（第一版就是这么红的），
            # 而"打到父容器弹出 全选"是 DOM 事实、不是产品缺陷。
            # 所以用 elementFromPoint 找一个确实可命中的 img，找不到就 FAIL 说明原因。
            hit = page.evaluate("""() => {
              for (const el of document.querySelectorAll('img')) {
                const r = el.getBoundingClientRect();
                if (r.width < 8 || r.height < 8) continue;
                const x = Math.min(Math.max(r.left + r.width / 2, 1), innerWidth - 1);
                const y = Math.min(Math.max(r.top + r.height / 2, 1), innerHeight - 1);
                if (document.elementFromPoint(x, y) === el) return {x, y, src: (el.src || '').slice(-20)};
              }
              return null;
            }""")
            if not hit:
                check("媒体区无选区不弹菜单", False, "没有可命中的 img，这条路径没被跑到（不许静默跳过）")
            else:
                ib = hit
                page.wait_for_timeout(200)
                check("媒体区无选区不弹菜单",
                      page.locator(".zgj-ctx-menu").count() == 0,
                      f"hit={hit}")

            # ---- 场景 B：输入框 → 全选 → 右键三项 → 剪切/粘贴 ----
            page.goto(BASE + "/#/search")
            page.wait_for_selector(".input-wrap input")
            inp = page.locator(".input-wrap input")
            inp.click()
            inp.type("测试剪切板")
            page.keyboard.press("Control+a")
            ib = inp.bounding_box()
            # 点击点落在选中文本内部（5 个 CJK ≈ 70px 宽），保选择不坍缩
            x0, y0, who0 = input_hit(page, inp)
            page.mouse.click(x0, y0, button="right")
            page.wait_for_selector(".zgj-ctx-menu")
            labels2 = item_labels(page)
            # v1 的 QLineEdit 右键菜单是 Qt 默认那套：撤销/剪切/复制/粘贴/删除/全选
            # （有选区才给剪切·复制）。本轮按 parity 补回**复制**这一项，顺序照 Qt；
            # **撤销不补**——execCommand('undo') 打在 Vue 受控输入上不会同步 model，
            # 看着能用其实把界面和数据劈开，宁缺勿假（理由写进对账表）。
            check(
                "场景B 输入框（有选区）四项=剪切/复制/粘贴/全选",
                labels2 == ["剪切", "复制", "粘贴", "全选"],
                f"labels={labels2}",
            )
            page.screenshot(path=os.path.join(OUT, "contextmenu-input.png"))
            page.keyboard.press("Escape")
            check("场景B Escape 关闭", page.locator(".zgj-ctx-menu").count() == 0)

            # 剪切：内容进剪贴板且输入框清空
            x1, y1, _ = input_hit(page, inp)
            page.mouse.click(x1, y1, button="right")
            page.wait_for_selector(".zgj-ctx-menu")
            page.locator(".zgj-ctx-item", has_text="剪切").click()
            page.wait_for_timeout(200)
            val = inp.input_value()
            check("场景B 剪切后输入框清空", val == "", f"val={val!r}")
            clip2 = page.evaluate("navigator.clipboard.readText()")
            check("场景B 剪贴板==剪切文本", clip2 == "测试剪切板", f"clip={clip2!r}")

            # 粘贴：剪贴板内容回写输入框
            x, y, who = input_hit(page, inp)
            check("场景B3 右键命中点确实在输入框上", who == "INPUT", f"命中={who}")
            page.mouse.click(x, y, button="right")
            page.wait_for_selector(".zgj-ctx-menu")
            page.locator(".zgj-ctx-item", has_text="粘贴").click()
            page.wait_for_timeout(200)
            val2 = inp.input_value()
            check("场景B 粘贴回填输入框", val2 == "测试剪切板", f"val={val2!r}")

            # 复制单独走一遍：放在剪切/粘贴之后，免得抢走它们依赖的选区状态
            # （第一版插在中间，剪切那步就等不到菜单了——测试自排的先后顺序也是被测的一部分）。
            inp.click()
            inp.type("复制测试")
            # 断言不写死内容：前面剪切/粘贴已经把输入框折腾成别的值了，
            # 写死 "复制测试" 第一次跑就红（clip='测试剪切板复制测试'）——
            # 以**当下实际值**为基准，测的是"复制不动内容"这条性质，不是猜字符串。
            want_c = inp.input_value()
            page.keyboard.press("Control+a")
            x2, y2, who2 = input_hit(page, inp)
            page.mouse.click(x2, y2, button="right")
            page.wait_for_selector(".zgj-ctx-menu")
            page.locator(".zgj-ctx-item", has_text="复制").click()
            page.wait_for_timeout(300)
            clip_c = page.evaluate("navigator.clipboard.readText()")
            check("场景B2 复制只进剪贴板，输入框内容不变",
                  clip_c == want_c and inp.input_value() == want_c,
                  f"clip={clip_c!r} want={want_c!r} val={inp.input_value()!r}")

            # ── 场景C：详情页简介的「复制全部简介」（v1 widgets.py:705-716 的第三项）──
            # 折叠态下 DOM 里仍是整段文本，所以复制到的必须是全文；
            # 而「展开」那个 label 属于控件，不该混进剪贴板（data-ctx-strip 负责剔掉）。
            page.goto(f"{BASE}/#/detail/hongguo%3A7439220582928952383")
            page.wait_for_selector(".desc", timeout=20000)
            page.wait_for_timeout(1200)
            want = page.evaluate("""() => {
              const el = document.querySelector('.desc');
              const c = el.cloneNode(true);
              c.querySelectorAll('[data-ctx-strip]').forEach(n => n.remove());
              return (c.textContent || '').trim();
            }""")
            # 明确不建选区：v1 无选区时「复制」是禁用项，v2 改成不出现这一行
            page.evaluate("() => { const s = getSelection(); if (s) s.removeAllRanges(); }")
            dbox = page.locator(".desc").bounding_box()
            page.mouse.click(dbox["x"] + 20, dbox["y"] + 8, button="right")
            page.wait_for_selector(".zgj-ctx-menu")
            labels3 = item_labels(page)
            check("场景C 简介上右键（无选区）给「复制全部简介」+「全选」，且没有点不动的「复制」",
                  "复制全部简介" in labels3 and "全选" in labels3 and "复制" not in labels3,
                  f"labels={labels3}")
            page.screenshot(path=os.path.join(OUT, "contextmenu-copyall.png"))
            page.locator(".zgj-ctx-item", has_text="复制全部简介").click()
            page.wait_for_timeout(400)
            clip3 = page.evaluate("navigator.clipboard.readText()")
            check("场景C 剪贴板==简介全文（含被折叠住的部分，且不掺「展开」label）",
                  clip3 == want and "展开" not in clip3,
                  f"len(clip)={len(clip3)} len(want)={len(want)} 尾部={clip3[-24:]!r}")
            # ⚠ 原本这里断「复制到的长度 > 60，证明含折叠部分」，实跑 len=22 直接红：
            # mock fixtures 的简介只有 22 字，压根进不了折叠阈值（120），
            # 也就是说**自然状态下这条性质在 mock 里测不到**——
            # 我不想留着一条靠猜的断言，也不想把阈值改成能过的样子。
            # 改成直接把 DOM 摆成折叠态（长文本 + .more label），
            # 走的是同一份代码路径（菜单在点击时才读块内文本），测的是真机制。
            long = "少年自微末崛起，" * 12 + "结尾标记。"
            page.evaluate(
                """([txt]) => {
                  const el = document.querySelector('.desc');
                  el.textContent = txt;
                  const more = document.createElement('span');
                  more.className = 'more';
                  more.setAttribute('data-ctx-strip', '');
                  more.textContent = '展开';
                  el.appendChild(more);
                }""", [long])
            dbox2 = page.locator(".desc").bounding_box()
            page.mouse.click(dbox2["x"] + 20, dbox2["y"] + 8, button="right")
            page.wait_for_selector(".zgj-ctx-menu")
            page.locator(".zgj-ctx-item", has_text="复制全部简介").click()
            page.wait_for_timeout(400)
            clip4 = page.evaluate("navigator.clipboard.readText()")
            check("场景C2 折叠态（长文本+展开label）复制到全文，且不掺「展开」",
                  clip4 == long and "展开" not in clip4 and len(clip4) > 100,
                  f"len={len(clip4)} 尾部={clip4[-16:]!r}")

            browser.close()

        if failures:
            print(f"\nRESULT: {len(failures)} FAIL -> {failures}")
            sys.exit(1)
        print("\nRESULT: ALL PASS")


    finally:
        _devserver.stop_dev(proc, PORT, name='contextmenu')
if __name__ == "__main__":
    main()
