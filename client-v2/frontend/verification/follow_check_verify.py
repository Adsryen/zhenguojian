#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""follow_check_verify.py —— 后台追剧检查的接线（R23-2）

对端是 v1 `main.py:714-726`（节奏）+ `main.py:776-860`（每轮做什么）。
Go 侧只补了 `App.ObserveDrama` 绑定（`Store.ObserveDrama` 早有实现与单测，缺的是对外口子），
遍历/节奏/季号归并放在 TS —— 因为 `base_title/series_key/group/find_season/next_season`
的权威实现已经在 `services/series.ts`（v1 `series.py` 的端口，有断言钉着），
再往 Go 抄一份就是同一个规格两处实现。

**这条脚本能证明什么、不能证明什么，先说白：**
- ✅ 能证明：挑行规则、单轮 50 行上限、12 次系列搜索上限、节奏数字与 v1 一致、
  重入保护、失败计数与提示语、间隔的下限/默认/坏值兜底、启动钩子不叠定时器。
- ❌ **不能证明**：「N 部有更新」这条计数。因为 mock 面的 `observeDrama`
  故意**不模拟旗标推进**（那是 Go 的规格，抄过来必然漂，见 api.ts 注释）。
  所以这里断言的是"updated 恒为 0 且没有误报提示"，
  真实的更新检测只在 Go 单测 + 打包机真跑里成立。别把本脚本全绿读成"检测已验证"。

第 ⑩ 条是对 `App.vue` 那个启动钩子的直接验证（不是靠"看起来挂了"）；
脚本里顺手留了一句给后人：**单页会话超过 60s 的验收脚本会被这轮检查打断**，
现在最长一只 33s 所以没撞上，撞上时该拆会话或显式 stop，而不是去改小 v1 的 60s。

端口：自管理 mock vite @5207（`_devserver`）。
"""
import json
import os
import sys

import _devserver
from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FRONTEND = os.path.dirname(HERE)
PORT = "5207"
BASE = f"http://127.0.0.1:{PORT}"
EXE = os.path.expanduser(
    "~/.cache/ms-playwright/chromium_headless_shell-1254/chrome-headless-shell-linux64/chrome-headless-shell")
OUT = HERE

failures = []


def check(name, cond, detail=""):
    line = f"{'PASS' if cond else 'FAIL'} {name}"
    if detail:
        line += f" | {detail}"
    print(line, flush=True)
    if not cond:
        failures.append(name)


def seed_follows(page, n, prefix="fc"):
    """往 mock 的本地追剧表灌 n 行（v1 的 status 三态之一才会被挑中）"""
    page.evaluate(
        """([n, prefix]) => {
          const follows = {};
          for (let i = 0; i < n; i++) {
            const id = prefix + ':' + i;
            follows[id] = {
              drama: { id, source: 'hongguo', title: '检查剧' + i, episodes: 10, description: '' },
              ts: Date.now() - i * 1000
            };
          }
          localStorage.setItem('zgj.follows', JSON.stringify(follows));
          localStorage.removeItem('zgj.reservations');
          localStorage.removeItem('zgj.reservations.drama');
        }""",
        [n, prefix],
    )


def goto_reserve(page):
    page.goto(f"{BASE}/#/reserve?tab=follow")
    page.wait_for_timeout(2000)


def main():
    proc = _devserver.start_dev(FRONTEND, PORT, True, HERE, name="follow-check")
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=EXE, headless=True)
            page = browser.new_context(viewport={"width": 1280, "height": 800}).new_page()

            # ── ① 节奏常数与 v1 对齐 ──
            goto_reserve(page)
            caps = page.evaluate("() => window.__zgjFollowCheckCaps")
            check("① 节奏/上限常数与 v1 一致（1800s 默认、60s 首跑、50 行、12 次搜索、800/300ms）",
                  caps == {"ROW_CAP": 50, "SERIES_LOOKUP_CAP": 12,
                           "PER_ROW_DELAY": 800, "PER_LOOKUP_DELAY": 300, "FIRST_RUN_DELAY": 60000},
                  f"caps={caps}")

            # ── ② 间隔取值的三种情形 ──
            a = page.evaluate("""() => {
              localStorage.removeItem('zgj.follow.interval');
              const dflt = window.__zgjFollowCheckInterval();
              localStorage.setItem('zgj.follow.interval', '10');   // 低于下限
              const floored = window.__zgjFollowCheckInterval();
              localStorage.setItem('zgj.follow.interval', 'abc');  // 坏值
              const garbage = window.__zgjFollowCheckInterval();
              localStorage.setItem('zgj.follow.interval', '3600');
              const custom = window.__zgjFollowCheckInterval();
              localStorage.removeItem('zgj.follow.interval');
              return { dflt, floored, garbage, custom };
            }""")
            check("② 间隔：默认 1800 / 下限 60 / 坏值回落 1800 / 自定义 3600（v1 main.py:718-723）",
                  a == {"dflt": 1800, "floored": 60, "garbage": 1800, "custom": 3600}, f"{a}")

            # ── ③ 挑行规则：三种状态 + reserved 都进，别的状态不进 ──
            picked = page.evaluate("""() => {
              const mk = (id, status, reserved) => ({ id, status, reserved, known: 0 });
              const rows = [
                mk('a', '想看', false), mk('b', '在看', false), mk('c', '预约中', false),
                mk('d', '随便什么状态', true), mk('e', '已看', false), mk('f', '', false)
              ];
              return window.__zgjFollowCheckPick(rows).map(r => r.id);
            }""")
            check("③ 挑行 = status∈{想看,在看,预约中} 或 reserved（v1 main.py:780-782）",
                  picked == ["a", "b", "c", "d"], f"picked={picked}")

            # ── ④ 单轮上限 50 行 ──
            picked60 = page.evaluate("""() => {
              const rows = [];
              for (let i = 0; i < 60; i++) rows.push({ id: 'x' + i, status: '在看', reserved: false, known: 0 });
              return window.__zgjFollowCheckPick(rows).length;
            }""")
            check("④ 单轮最多 50 行（v1 的 [:50]）", picked60 == 50, f"len={picked60}")

            # ── ⑤ 真跑一轮：3 行全部检查、mock 不推进旗标所以不该有误报提示 ──
            seed_follows(page, 3, "fcok")
            page.reload()
            goto_reserve(page)
            res = page.evaluate("async () => await window.__zgjFollowCheck({ delayMs: 0 })")
            check("⑤a 一轮跑完 checked=3、updated=0（mock 不模拟旗标推进，见 docstring）",
                  res.get("checked") == 3 and res.get("updated") == 0 and not res.get("skipped"),
                  f"res={res}")
            check("⑤b 没有更新也没有失败时，不打提示（v1 只在 updated/failed 时才写状态栏）",
                  page.locator("[data-testid=follow-check-hint]").count() == 0,
                  f"hint 数={page.locator('[data-testid=follow-check-hint]').count()}")

            # ── ⑥ 一条坏剧不打断整轮，且提示语给"暂时取不到" ──
            seed_follows(page, 4, "fcbad")
            page.evaluate("() => { window.__zgjDetailFail = ['fcbad:1', 'fcbad:3']; }")
            page.reload()
            goto_reserve(page)
            page.evaluate("() => { window.__zgjDetailFail = ['fcbad:1', 'fcbad:3']; }")
            res2 = page.evaluate("async () => await window.__zgjFollowCheck({ delayMs: 0 })")
            check("⑥a 4 行里 2 行详情失败仍跑完整轮（checked=4，failed=2）",
                  res2.get("checked") == 4 and res2.get("failed") == 2,
                  f"res={json.dumps(res2, ensure_ascii=False)}")
            hint = ""
            if page.locator("[data-testid=follow-check-hint]").count():
                hint = page.locator("[data-testid=follow-check-hint]").first.inner_text().strip()
            check("⑥b 提示语是 v1 那句「追剧检查：%d 部暂时取不到更新」（main.py:856-857）",
                  hint == "追剧检查：2 部暂时取不到更新", f"hint={hint!r}")
            page.evaluate("() => { delete window.__zgjDetailFail; }")

            # ── ⑦ 重入保护（v1 _follow_busy）：上一轮没完就直接跳过 ──
            seed_follows(page, 2, "fcbusy")
            page.reload()
            goto_reserve(page)
            both = page.evaluate("""async () => {
              const a = window.__zgjFollowCheck({ delayMs: 600 });   // 占住一轮
              const b = await window.__zgjFollowCheck({ delayMs: 0 }); // 立刻再来一次
              await a;
              return b;
            }""")
            check("⑦ 第二轮被跳过（v1 的 _follow_busy 语义，不是排队跑两轮）",
                  both.get("skipped") is True and both.get("checked") == 0,
                  f"second={json.dumps(both, ensure_ascii=False)}")

            # ── ⑧ en 态提示语也是英文（新键必须成对） ──
            seed_follows(page, 3, "fcen")
            page.evaluate("() => { window.__zgjDetailFail = ['fcen:0']; }")
            page.evaluate("() => localStorage.setItem('zgj.locale', 'en')")
            page.reload()
            page.goto(f"{BASE}/#/reserve?tab=follow")
            page.wait_for_timeout(2200)
            page.evaluate("() => { window.__zgjDetailFail = ['fcen:0']; }")
            page.evaluate("async () => await window.__zgjFollowCheck({ delayMs: 0 })")
            page.wait_for_timeout(400)
            hint_en = ""
            if page.locator("[data-testid=follow-check-hint]").count():
                hint_en = page.locator("[data-testid=follow-check-hint]").first.inner_text().strip()
            check("⑧ en 态提示语是英文且没有中文残留",
                  hint_en == "Follow check: 1 title(s) unreachable for now", f"hint={hint_en!r}")
            page.evaluate("() => { delete window.__zgjDetailFail; localStorage.removeItem('zgj.locale'); }")

            # ── ⑨ 每轮开头/结尾都现读主表（v1 是现读 follows()，不是吃页面缓存） ──
            seed_follows(page, 2, "fcfresh")
            goto_reserve(page)
            r1 = page.evaluate("async () => await window.__zgjFollowCheck({ delayMs: 0 })")
            # 中途往本地主表塞第三条：如果下一轮还吃旧缓存，checked 会仍是 2
            seed_follows(page, 3, "fcfresh")
            r2 = page.evaluate("async () => await window.__zgjFollowCheck({ delayMs: 0 })")
            check("⑨ 第二轮现读到 3 行（第一轮 2 行）——不是吃上一轮的页面缓存",
                  r1.get("checked") == 2 and r2.get("checked") == 3,
                  f"r1={json.dumps(r1, ensure_ascii=False)} r2={json.dumps(r2, ensure_ascii=False)}")

            # ── ⑩ 启动钩子真的挂上了（App 级，不是某个页面里顺手调一下） ──
            sched = page.evaluate("() => window.__zgjFollowCheckScheduled() === true")
            check("⑩a 页面起来后后台检查已排期（App.vue onMounted 的接线在）", sched, f"scheduled={sched}")
            page.evaluate("() => window.__zgjFollowCheckStop()")
            check("⑩b 停止后排期清空（热重载/卸载不会叠第二个定时器）",
                  page.evaluate("() => window.__zgjFollowCheckScheduled() === false"),
                  "")

            page.screenshot(path=os.path.join(OUT, "follow-check-hint.png"))
            browser.close()
    finally:
        _devserver.stop_dev(proc, PORT, name="follow-check")

    print(f"\n===== follow_check_verify: {'ALL PASS' if not failures else 'FAILURES: ' + ', '.join(failures)} =====")
    print(f"screenshot -> {os.path.join(OUT, 'follow-check-hint.png')}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
