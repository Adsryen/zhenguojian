#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""ports_audit.py —— 验收脚本的端口占位审计（把 R8 那类事故变成开工前的硬门禁）

为什么要有它（历史成本）：本任务在端口上栽过最多次——
  · 5182 被 `reserve_calendar`(mock) 与 `detail_download`(非 mock) 同时占用，
    泄漏的 mock vite 在 5182 上答 200，于是 detail_download 的 ② 断了一条假红灯（R8）；
  · 5187/5188、5189、5173 也各撞过一次，逐个手工排查（grep 数字还会被注释里的历史端口骗）。
`_devserver` 之后每个脚本**进门都会断言自己那个端口是空的**，
所以"同时跑的时候撞车"已经防住了；但**串行全量跑**时仍然防不住一种情况：
两个脚本默认用同一个端口，前一个泄漏 / 或后一个的预检被绕过——那时你看到的绿
是在别人的服务器上测的。

本审计只做一件静态的事：**把每个脚本声明的默认端口抽出来，看有没有两个脚本抢同一个号**。
`run_all.py` 在跑任何脚本之前先跑它，撞号就直接不跑（而不是跑到一半出现解释不了的绿）。

抽取必须覆盖仓里真实存在的四种写法（少一种就是假审计，见 `--selftest` 里逐条钉住的用例）：
  A `PORT = "5179"` / `PORT = 5196`            （模块级字面量）
  B `PORT = os.environ.get("VERIFY_PORT", "5185")`
     `PORT = int(os.environ.get("VERIFY_PORT", "5198"))`
                                              （env + 默认值）
  C `MOCK_BASE = "http://127.0.0.1:5181"`      （URL 常量里带端口）
  D `_devserver.start_dev(FRONT, 5177, ...)`   （直接写在调用里，没有常量）
"""
import argparse
import collections
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

# 只认 51xx/52xx：验收脚本一律用这一段（真应用不会占这些号）
_PORT_RANGE = r"5[12]\d{2}"

_PATTERNS = [
    # A：PORT 家族常量（PORT / PORT2 / MOCK_PORT / PLAIN_PORT …），值为字面量
    (r'^\w*PORT\w*\s*=\s*"?(' + _PORT_RANGE + r')"?', "常量"),
    # B：env 带默认值（默认值才是"这台机器上不设 env 时真正会用的端口"）
    (r'^\w*PORT\w*\s*=\s*(?:int\(\s*)?os\.environ\.get\([^,]+,\s*"?(' + _PORT_RANGE + r')"?\)', "env默认"),
    # C：URL 常量里的端口
    (r'^(?:\w*BASE\w*|\w*URL\w*)\s*=\s*"[^"]*?127\.0\.0\.1:(' + _PORT_RANGE + r')"', "URL常量"),
    # D：调用现场的字面量（start_dev / stop_dev / port_open / urlopen 等）
    (r'(?:start_dev|stop_dev|port_open)\([^)]*?\b(' + _PORT_RANGE + r')\b', "调用现场"),
]


def strip_trailing_comments(text):
    """剥掉行尾的 `#` 注释（整行注释本来就不匹配四种模式，但**行尾**注释会）。

    ⚠ 第五十五轮的真实事故：`preload_openpath_verify.py:18` 写的是
        PORT = int(os.environ.get("VERIFY_PORT", "5198"))   # 原来默认 5187，第二个服务器取 PORT+1=5188
    注释里的 5187/5188 都是**历史号**，早就不归它。而 D 模式带 `re.S`，
    `[^)]*?` 会从前面某次 `port_open(` 一路吞进注释，把 5188 算成这个脚本的端口。
    后果不是报表难看，而是两件事同时错：
      · 全量回归把别人残留的监听**误判成 preload 泄漏**（"跑完仍占 5188"），
      · 真正被抢号的 sorani 则整只脚本跑不了。
    判据把注释当事实，就会同时产出假红与假归属。
    """
    out = []
    for line in text.split("\n"):
        i = line.find("#")
        if i >= 0 and line[:i].count('"') % 2 == 0 and line[:i].count("'") % 2 == 0:
            line = line[:i]
        out.append(line)
    return "\n".join(out)


def extract_ports(text):
    """返回 {端口: {命中方式, …}}。宁可多收（同一脚本多种写法都指向同端口无害），
    不能漏收——漏收才是假审计。**但注释不是写法**：先剥行尾注释。"""
    out = collections.defaultdict(set)
    body = strip_trailing_comments(text)
    for rx, how in _PATTERNS:
        # D（调用现场）必须逐行匹配：跨行匹配就是上一版误收注释的根因
        flags = re.M if how == "调用现场" else (re.M | re.S)
        for m in re.finditer(rx, body, flags):
            out[m.group(1)].add(how)
    return dict(out)


def _candidates(directory):
    """要审计的文件集合。

    不能只看 `*_verify.py`：`i18n_exemption_probe.py` 这类**探针脚本同样起 vite**
    （它默认占 5201/5202），却不会因为名字不带 _verify 就不撞车。
    判据改成"实际会不会起服务器"——出现 `start_dev` 或 `subprocess.Popen` 就算，
    并排除 `_devserver.py`/`run_all.py`/`ports_audit.py` 这三个自己不动端口的工具。
    """
    out = []
    for f in sorted(os.listdir(directory)):
        if not f.endswith(".py") or f in ("_devserver.py", "run_all.py", "ports_audit.py"):
            continue
        text = open(os.path.join(directory, f), encoding="utf-8").read()
        if f.endswith("_verify.py") or "start_dev" in text or "subprocess.Popen" in text:
            out.append((f, text))
    return out


def audit_dir(directory=HERE):
    """扫全部会起服务器的脚本，返回 (每个脚本的端口表, 归属表, 冲突表)。"""
    per = {}
    owner = collections.defaultdict(list)
    for f, text in _candidates(directory):
        ports = extract_ports(text)
        per[f] = ports
        for p in ports:
            owner[p].append(f)
    conflicts = {p: v for p, v in owner.items() if len(v) > 1}
    return per, owner, conflicts


# ───────────────────────── spec 端口表对账 ─────────────────────────
#
# 为什么也做成机器活：这张表是**唯一**能回答"这个号谁在用"的地方，却靠手工维护。
# 第二十九轮我补 5201/5202 那行之前，表里已经少了 `next_season_verify`
# （第二十二轮就加了的脚本）——漏加没有任何信号，除非恰好有人去数表行。
# 文档漂移不会自己报警，这条把它变成对账。

SPEC_RELPATH = os.path.join("..", "..", "..", ".trellis", "spec", "backend", "verification-harness.md")


def parse_spec_table(text):
    """从 markdown 端口表抽出 ({端口: [脚本,…]}, {脚本: [端口,…]})。

    只认 `| 5181 / 5182 | \`detail_download_verify\` | … |` 这种数据行；
    表头/分隔行/说明段都没有端口号，天然被过滤掉。
    """
    by_port = collections.defaultdict(list)
    by_script = collections.defaultdict(list)
    for line in text.splitlines():
        if not line.strip().startswith("|"):
            continue
        cols = [c.strip() for c in line.strip().strip("|").split("|")]
        if len(cols) < 2:
            continue
        ports = re.findall(_PORT_RANGE, cols[0])
        if not ports:
            continue
        m = re.search(r"`([A-Za-z0-9_]+)`", cols[1])
        if not m:
            continue
        script = m.group(1)
        for pt in ports:
            by_port[pt].append(script)
            by_script[script].append(pt)
    return dict(by_port), dict(by_script)


def spec_path(directory=HERE):
    return os.path.normpath(os.path.join(directory, SPEC_RELPATH))


def check_spec(directory=HERE):
    """表 vs 实际端口对账。

    spec 文件不在就 SKIP 而不是 FAIL：`.trellis/` 被 gitignore，
    CI 的干净 checkout 里根本没有它——那种情况下失败只会变成噪音。
    """
    path = spec_path(directory)
    if not os.path.isfile(path):
        print("SKIP spec 对账：找不到 %s（.trellis 被 gitignore，没有也正常）" % path)
        return 0
    per, owner, conflicts = audit_dir(directory)
    if conflicts:
        print("FAIL 先解决端口冲突再谈对账：%s" % conflicts)
        return 1
    table_ports, table_scripts = parse_spec_table(open(path, encoding="utf-8").read())

    missing = []   # 实际在用、表里没有 → 文档落后
    stale = []     # 表里写了、实际没有 → 文档超前或写错
    for pt, files in sorted(owner.items()):
        for f in files:
            name = f[:-3] if f.endswith(".py") else f
            if name not in table_scripts or pt not in table_scripts[name]:
                missing.append((pt, name))
    for pt, names in sorted(table_ports.items()):
        for name in names:
            fname = name + (".py" if name.endswith(("_verify", "_probe")) else "")
            ports = per.get(fname)
            if ports is None:
                stale.append((pt, name, "脚本不在了"))
            elif pt not in ports:
                stale.append((pt, name, "该脚本没再用这个号"))

    if not missing and not stale:
        print("PASS spec 对账：表 %d 个号与实际 %d 个号完全一致" % (len(table_ports), len(owner)))
        return 0
    for pt, name in missing:
        print("MISSING 表里缺行：%s @%s → 补进 %s" % (name, pt, path))
    for pt, name, why in stale:
        print("STALE   表里过期：%s @%s（%s）" % (name, pt, why))
    return 1


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--selftest", action="store_true")
    ap.add_argument("--quiet", action="store_true")
    ap.add_argument("--check-spec", action="store_true",
                    help="只做 spec 端口表对账（不一致就非零）")
    args = ap.parse_args()
    if args.selftest:
        return selftest()
    if args.check_spec:
        return check_spec()

    per, owner, conflicts = audit_dir()
    n_scripts = len(per)
    n_ports = len(owner)
    if not args.quiet:
        for p in sorted(owner):
            print(f"  {p}: {', '.join(owner[p])}")
        print(f"起服务器的脚本 {n_scripts} 个，端口 {n_ports} 个")
    if conflicts:
        print("FAIL 端口冲突（同一默认端口被多个脚本声明）：")
        for p, files in sorted(conflicts.items()):
            print(f"  {p} ← {', '.join(files)}")
        print("处置：给后加入的脚本换一个空闲号（5200 段起步），并同步 spec 的端口表。")
        return 1
    if not args.quiet:
        print("PASS 无端口冲突")
    # 冲突是硬伤，文档漂移是软伤：一次跑完一起决定退出码，
    # 免得修完冲突还得再跑一遍才发现表落后（第二十九轮我自己就这么来回过）。
    return check_spec()


def selftest():
    """表驱动钉住四种真实写法。
    第 28 轮的教训直接适用：**别测我想象中的形状，测仓里真实存在的形状**。
    这里的每个用例都对应一个真实脚本（注释里点名），改写法时必须同步改这里。
    """
    cases = [
        # A：player_verify.py:32 `PORT = "5179"`；contextmenu_verify.py `PORT = 5196`
        ("A 字面量常量（带引号）", 'PORT = "5179"\nBASE = f"http://127.0.0.1:{PORT}"', {"5179"}),
        ("A 字面量常量（不带引号）", "PORT = 5196\n", {"5196"}),
        # B：i18n_sweep_verify.py:46 / preload_openpath_verify.py:18,21
        ("B env 带默认值", 'PORT = os.environ.get("VERIFY_PORT", "5185")\n', {"5185"}),
        ("B env 带默认值且 int()", 'PORT = int(os.environ.get("VERIFY_PORT", "5198"))\n'
                                   'PLAIN_PORT = int(os.environ.get("VERIFY_PLAIN_PORT", "5199"))\n',
         {"5198", "5199"}),
        # C：detail_download_verify.py:34-35
        ("C URL 常量", 'MOCK_BASE = "http://127.0.0.1:5181"\n'
                       'REAL_BASE = "http://127.0.0.1:5182"\n', {"5181", "5182"}),
        # D：calendar_scroll_detail_verify.py（端口只写在 start_dev 调用里）
        ("D 调用现场字面量", 'proc = _devserver.start_dev(FRONT, 5177, True, OUT, name="x")\n'
                            '_devserver.stop_dev(proc, 5177, name="x")\n', {"5177"}),
        # 干扰项：注释/历史端口不该被算进来（本仓栽过：grep 数字误报）
        ("注释里的历史端口不算", '# 原来默认 5187，第二个服务器取 PORT+1=5188\n'
                              'PORT = 5193\n', {"5193"}),
        # 拼接出来的端口（PORT+1）静态算不出，但真脚本会显式写第二个常量；
        # 这里钉住"不瞎猜"：只抽显式出现的号
        ("隐式 PORT+1 不猜", "PORT = 5189\nSECOND = PORT + 1\n", {"5189"}),
        # ⚠ 本轮事故的真实形状：**代码行尾**的历史注释。
        # 整行注释的用例早就有，但这一种要等 D 模式跨行 + 行尾注释同时发生才会漏，
        # 所以我上一版加的那条测试**没能防住**这次 —— 记下来：
        # 测过一种形状不等于测过这一类形状。
        ("行尾历史注释不算（本轮事故）",
         'PORT = int(os.environ.get("VERIFY_PORT", "5198"))   # 原来默认 5187，第二个服务器取 PORT+1=5188\n'
         'PLAIN_PORT = int(os.environ.get("VERIFY_PLAIN_PORT", "5199"))\n'
         'def f():\n    return port_open("127.0.0.1", PORT)\n',
         {"5198", "5199"}),
        # D 模式不许跨行：跨行就会把后面注释里的号吸进来
        ("D 跨行不吸后面的号",
         'PORT = 5203\nif port_open("h", PORT):\n    # 以前这里用过 5190\n    pass\n',
         {"5203"}),
    ]
    bad = 0
    for name, text, want in cases:
        got = set(extract_ports(text))
        ok = got == want
        print(f"{'PASS' if ok else 'FAIL'} 抽取：{name}")
        if not ok:
            print(f"      got={sorted(got)} want={sorted(want)}  源文={text!r}")
            bad += 1

    # 冲突判定
    owner = {"5199": ["a_verify.py"], "5182": ["b_verify.py", "c_verify.py"]}
    conflicts = {p: v for p, v in owner.items() if len(v) > 1}
    if conflicts != {"5182": ["b_verify.py", "c_verify.py"]}:
        print("FAIL 冲突判定：应该只报 5182")
        bad += 1
    else:
        print("PASS 冲突判定：只报被两个脚本抢的号")

    # 真仓自证：所有脚本都要被抽到端口，且当前默认无冲突
    per, own, conf = audit_dir(HERE)
    n = len(per)
    missing = [f for f, ports in per.items() if not ports]
    print(f"实仓核对：起服务器的脚本 {n} 个 / 端口 {len(own)} 个 / 冲突 {len(conf)} 处")
    probes = [f for f in per if not f.endswith("_verify.py")]
    print("      其中非 _verify 却起服务器的：" + (str(probes) if probes else "无"))
    if not probes:
        print("FAIL 现在仓里应当至少有一个非 _verify 的探针在起服务器；"
              "它不见了说明判据退化了（审计范围被缩小）")
        bad += 1
    if missing:
        print(f"FAIL 有脚本一个端口都没抽到（说明它用了第五种写法，必须补模式）：{missing}")
        bad += 1
    else:
        print("PASS 每个脚本都抽到了端口（没被静默跳过）")
    if conf:
        print(f"FAIL 实仓存在端口冲突：{conf}")
        bad += 1
    else:
        print("PASS 实仓无端口冲突")

    # ── 表解析用例：形状一律取自真实 spec（第 28 轮的教训：别测想象中的形状）──
    tbl_cases = [
        ("普通行", "| 5179 | `player_verify` | mock | `_devserver` |", {"5179": ["player_verify"]}),
        ("一行两个号", "| 5181 / 5182 | `detail_download_verify` | mock / 非 mock | `_devserver` |",
         {"5181": ["detail_download_verify"], "5182": ["detail_download_verify"]}),
        ("名字后面还有说明文字", "| 5201 / 5202 | `i18n_exemption_probe`（非 _verify，但真起两台 vite） | mock | x |",
         {"5201": ["i18n_exemption_probe"], "5202": ["i18n_exemption_probe"]}),
        ("带轮次后缀的行", "| 5198 / 5199 | `preload_openpath_verify` | mock | `_devserver`（第十轮新） |",
         {"5198": ["preload_openpath_verify"], "5199": ["preload_openpath_verify"]}),
        ("表头与分隔行不算数据", "| 端口 | 脚本 | 态 | 启停 |\n|---|---|---|---|\n", {}),
        ("说明段里的端口号不算数据行", "把 5182 换到 5188 就好了\n", {}),
    ]
    for name, text, want_ports in tbl_cases:
        got_ports, _ = parse_spec_table(text)
        ok = got_ports == want_ports
        print(f"{'PASS' if ok else 'FAIL'} 表解析：{name}")
        if not ok:
            print(f"      got={got_ports} want={want_ports}")
            bad += 1

    # 漂移必须被抓出来（这条是这套对账存在的全部理由）
    import tempfile
    # 布局必须照真实仓的关系来：spec 在脚本目录的**上一级**再进 .trellis/spec/，
    # 所以这里建 root/{pkg, spec} 两个兄弟目录，directory 传 root/pkg。
    # 第一版我把 spec 建在 td 里面，normpath 直接跑到 /tmp/spec 去了，
    # 于是 check_spec 走的是"spec 不存在→SKIP 返回 0"分支，
    # 我差点把"漂移检测失效"当成"断言写错"来改——又是测量装置骗我（第三次）。
    root = tempfile.mkdtemp()
    pkg = os.path.join(root, "pkg")
    os.makedirs(os.path.join(root, "spec"))
    os.makedirs(pkg)
    open(os.path.join(pkg, "a_verify.py"), "w", encoding="utf-8").write("PORT = 5111\n")
    open(os.path.join(pkg, "b_verify.py"), "w", encoding="utf-8").write("PORT = 5112\n")
    spath = os.path.join(root, "spec", "verification-harness.md")
    open(spath, "w", encoding="utf-8").write(
        "| 端口 | 脚本 | 态 | 启停 |\n|---|---|---|---|\n"
        "| 5111 | `a_verify` | mock | x |\n"
        "| 5199 | `gone_verify` | mock | x |\n")   # 缺 b@5112，且多一行已不存在的脚本
    orig_rel = SPEC_RELPATH
    globals()["SPEC_RELPATH"] = os.path.join("..", "spec", "verification-harness.md")
    try:
        rc = check_spec(pkg)
    finally:
        globals()["SPEC_RELPATH"] = orig_rel
    # 这里不捕获 stdout，只看返回值：漂移必须非零
    print(f"{'PASS' if rc == 1 else 'FAIL'} 漂移检测：表缺行 + 表有过期行 → 必须非零（得 {rc}）")
    if rc != 1:
        bad += 1

    total = len(cases) + 3 + len(tbl_cases) + 1
    print(f"\n===== ports_audit --selftest: {total - bad}/{total} PASS =====")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
