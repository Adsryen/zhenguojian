#!/usr/bin/env python3
"""smoke_nav.py —— 从截图里读「侧栏当前高亮的是哪一项」的中心 y。

为什么需要它（83 轮实测）：linux-smoke.sh 原来只 print 每页截图的字节数，
从不检查页面真的换了。实跑 md5 对比发现 page_downloads.png 与
page_settings.png **逐字节相同**，而且更糟：按我原先硬编码的 y 表
  discover:107 library:143 search:211 reserve:283 history:355 downloads:427 settings:499
逐条核对截图后，真实条目中心是
  找剧109 剧库推荐144 搜索178 新剧预约214 追更预约286 观看历史321 下载与合并355 设置428
也就是说 search/reserve/history/downloads/settings 五次点击**全部打错行**，
"7 个页面渲染正常"这句话里只有 3 页是真的。

判据用"高亮项"而不是"页面内容变了"：
  - 内容 md5 变不变会被加载转圈、封面数量、鼠标位置污染，既可能假变也可能假不变；
  - 活动项的文字是主题蓝（srgb(74,131,255) 一类），hover 只是灰底 —— 两者可分；
  - 顶部 logo 也是蓝的，所以只看 y>=60 的区域。

用法：python3 scripts/smoke_nav.py <png>   → 打印中心 y（找不到打印空）
退出码 0 恒成立（调用方按空/非空判断）。
"""
import re
import subprocess
import sys

BLUE_RE = re.compile(r"\s*(\d+),(\d+): \((\d+),(\d+),(\d+)")


def blue_rows(png, x0=0, x1=200, y0=60, y1=760, min_blue=5):
    """返回 (起点, 终点, 中心) 的蓝色文字行簇。"""
    out = subprocess.run(
        ["convert", png, "-crop", "%dx%d+%d+%d" % (x1 - x0, y1 - y0, x0, y0),
         "+repage", "txt:-"],
        capture_output=True, text=True, errors="replace").stdout
    if not out.strip():
        # convert 失败（策略/文件问题）时**必须**返回空，不能返回"有簇"的假象
        return []
    rows = {}
    for line in out.splitlines():
        m = BLUE_RE.match(line)
        if not m:
            continue
        _x, y, r, g, b = (int(v) for v in m.groups())
        if b > 120 and b - r > 60 and b - g > 40:
            rows[y] = rows.get(y, 0) + 1
    ys = sorted(y for y, n in rows.items() if n >= min_blue)
    groups, cur = [], []
    for y in ys:
        if cur and y - cur[-1] > 8:
            groups.append(cur)
            cur = []
        cur.append(y)
    if cur:
        groups.append(cur)
    return [(g[0] + y0, g[-1] + y0, (g[0] + g[-1]) // 2 + y0) for g in groups]


def selftest(tmpdir):
    """判据自己也要有门：用 ImageMagick 画已知布局的图，验证检测器说的数。

    没有这几条，"高亮项在 109"这句话完全可能只是我把正则写错后的噪声。
    """
    import os
    failures = []

    # N1 单行蓝条 → 检出中心 ≈ 条的中点
    p = os.path.join(tmpdir, "n1.png")
    ok = subprocess.run(["convert", "-size", "200x700", "xc:#131722",
                         "-fill", "rgb(74,131,255)",
                         "-draw", "rectangle 20,195 180,208", p],
                        capture_output=True, text=True, errors="replace").returncode == 0
    got = blue_rows(p) if ok else []
    c = got[0][2] if len(got) == 1 else -1
    failures.append(("N1 单行检出", abs(c - 201) <= 3, "got=%r" % (got,)))

    # N2 全灰 → 空（不能把底色或噪声当高亮）
    p2 = os.path.join(tmpdir, "n2.png")
    subprocess.run(["convert", "-size", "200x700", "xc:#131722", p2],
                   capture_output=True, text=True, errors="replace")
    failures.append(("N2 无蓝条→空", blue_rows(p2) == [], "got=%r" % (blue_rows(p2),)))

    # N3 文件不存在 → 空且不抛（调用方按空判失败）
    failures.append(("N3 坏文件→空", blue_rows(os.path.join(tmpdir, "nope.png")) == [],
                     "应返回空"))

    # N4 两行蓝条 → 两簇都报出来（调用方据此判失败，不静默取第一个）
    p4 = os.path.join(tmpdir, "n4.png")
    subprocess.run(["convert", "-size", "200x700", "xc:#131722",
                    "-fill", "rgb(74,131,255)",
                    "-draw", "rectangle 20,195 180,208",
                    "-draw", "rectangle 20,495 180,508", p4],
                   capture_output=True, text=True, errors="replace")
    g4 = blue_rows(p4)
    failures.append(("N4 两行→两簇", len(g4) == 2, "got=%r" % (g4,)))

    # N5 y0 之下的不算（logo 在顶部，必须被排除窗口挡掉）
    p5 = os.path.join(tmpdir, "n5.png")
    subprocess.run(["convert", "-size", "200x700", "xc:#131722",
                    "-fill", "rgb(74,131,255)",
                    "-draw", "rectangle 20,10 180,23", p5],
                   capture_output=True, text=True, errors="replace")
    failures.append(("N5 顶部 logo 排除", blue_rows(p5) == [], "got=%r" % (blue_rows(p5),)))

    passed = 0
    for name, cond, detail in failures:
        if cond:
            passed += 1
            print("PASS %s" % name)
        else:
            print("FAIL %s | %s" % (name, detail))
    print("smoke_nav selftest: %d 通过 / %d 失败" % (passed, len(failures) - passed))
    return 0 if passed == len(failures) else 1


def main():
    if len(sys.argv) >= 2 and sys.argv[1] == "--selftest":
        import tempfile
        return selftest(tempfile.mkdtemp(prefix="zgj-navtest-"))
    if len(sys.argv) != 2:
        sys.stderr.write("用法：smoke_nav.py <png> | --selftest\n")
        return 2
    clusters = blue_rows(sys.argv[1])
    if not clusters:
        return 0
    # 正常只有一个高亮项；多个就都打出来（调用方会因此判失败，不静默取第一个）
    print(" ".join(str(c[2]) for c in clusters))
    return 0


if __name__ == "__main__":
    sys.exit(main())
