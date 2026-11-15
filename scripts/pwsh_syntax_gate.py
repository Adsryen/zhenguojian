#!/usr/bin/env python3
"""pwsh_syntax_gate —— workflow 里每段内联 PowerShell 都必须先过语法解析

为什么要有它（不是我推测的，是烧出来的）：
  alpha.36 的 windows job 死在这一步：
      ParserError: D:\\a\\_temp\\xxx.ps1:37
      (& python $rs --tag $tag --file client-v2/build/bin/RELEASE-NOTES.md
          --explain $srcFile | Select-Object -First 1)
      Missing closing ')' in expression.
  这段是上一轮（62）我为了"notes 来源可见"改的，改了之后**从没被执行过**——
  它排在 checkout 后面，而 checkout 先死了。等 checkout 修好，它才第一次跑，
  于是又白烧一个 tag。用户那边就是又一轮"你怎么还没修好"。

  根因不是 PowerShell 难写，是**内联在 workflow 里的代码，本地一行都跑不到**。
  修法分两层：
    1. 逻辑搬进 .ps1 文件（gen_latest_json.ps1），带 -SelfTest，能本地跑；
    2. 剩下必须留在 workflow 的胶水，用本脚本做**语法解析门**——
       不执行，只让 PowerShell 自己的 Parser 看一遍。有 pwsh 就能在本地做，
       我本机装了 pwsh 7.4.6（~/.local/pwsh），所以这道门是可跑的。

用法：
  python3 scripts/pwsh_syntax_gate.py                      # 检查默认 workflow
  python3 scripts/pwsh_syntax_gate.py --selftest           # 两个方向都验（坏块必须红）
  PWSH=/usr/bin/pwsh python3 scripts/pwsh_syntax_gate.py
"""
import io
import os
import subprocess
import sys
import tempfile

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_WORKFLOWS = [os.path.join(REPO, ".github", "workflows", "build-v2.yml")]

# 路径走环境变量传，不用 $args[0]：`pwsh -Command <脚本> <参数>` 的剩余参数
# 并不会可靠地进到 $args（实测报 "The file could not be read"），于是坏块和好块
# 都因为**同一个读文件错**而红——坏块看着像被抓到，其实一行都没解析。
PARSE_PS = r'''
$p = $env:ZGJ_PS_PARSE_TARGET
if (-not $p -or -not (Test-Path $p)) { Write-Output "TARGET-MISSING:$p"; exit 2 }
$tokens = $null; $errs = $null
[void][System.Management.Automation.Language.Parser]::ParseFile($p, [ref]$tokens, [ref]$errs)
foreach ($e in $errs) { Write-Output ("{0}:{1} {2}" -f $e.Extent.StartLineNumber, $e.Extent.StartColumnNumber, $e.Message) }
if ($errs.Count -gt 0) { exit 1 }
exit 0
'''


def find_pwsh():
    cand = [os.environ.get("PWSH"), os.path.expanduser("~/.local/pwsh/pwsh"), "pwsh", "powershell"]
    for c in cand:
        if not c:
            continue
        if c in ("pwsh", "powershell"):
            from shutil import which
            p = which(c)
            if p:
                return p
        elif os.path.exists(c):
            return c
    return None


def extract_blocks(path):
    """返回 [(标识, shell, run 文本)]。不依赖 yaml 库的键序，手工找 pwsh/powershell shell。"""
    try:
        import yaml
    except ImportError:
        return None
    with io.open(path, encoding="utf-8") as f:
        doc = yaml.safe_load(f)
    out = []
    jobs = (doc or {}).get("jobs") or {}
    for jname, job in jobs.items():
        for i, step in enumerate(job.get("steps") or []):
            shell = (step.get("shell") or "").lower()
            if "pwsh" not in shell and "powershell" not in shell:
                continue
            run = step.get("run") or ""
            name = step.get("name") or "(未命名步骤)"
            out.append((f"{os.path.basename(path)} › {jname} › [{i}] {name}", shell, run))
    return out


def parse_block(pwsh, text):
    """让 PowerShell Parser 解析一段脚本文本，返回错误行列表（空=语法过）。

    ⚠ 先把 `${{ … }}` 换成占位符再解析。GitHub 是在调用 pwsh **之前**做表达式替换的，
    PowerShell 自己从不认识 `${{ }}`：原样喂给 Parser 会报
    `Use `{ instead of { in variable names.`（我第一版就被这条绊了一次，
    报的是好块，其实是插值语法）。
    占位符只能消除这一类假错；替换后**内容**是否破坏引号（比如表达式值里带空格）
    不在这道门的覆盖范围里，靠下面 S5 那种"值带空格仍安全"的写法约定来防。
    """
    import re
    text = re.sub(r"\$\{\{.*?\}\}", "GH_SUBSTITUTED", text, flags=re.S)
    with tempfile.NamedTemporaryFile("w", suffix=".ps1", delete=False, encoding="utf-8") as tf:
        tf.write(text)
        p = tf.name
    try:
        env = dict(os.environ, ZGJ_PS_PARSE_TARGET=p)
        r = subprocess.run([pwsh, "-NoProfile", "-Command", PARSE_PS],
                           capture_output=True, text=True, encoding="utf-8", errors="replace",
                           timeout=120, env=env)
        out = [ln.strip() for ln in (r.stdout or "").splitlines() if ln.strip()]
        if any(ln.startswith("TARGET-MISSING") for ln in out):
            return ["门本身坏了：pwsh 读不到待解析文件"]
        if r.returncode not in (0, 1):
            return out or [f"pwsh 退码 {r.returncode}: {(r.stderr or '').strip()[:200]}"]
        return out
    finally:
        os.unlink(p)


BROKEN = '''$rs = "x.py"
$tag = "v1"
$srcFile = "/tmp/s.txt"
$notes = (& python $rs --tag $tag --file a/RELEASE-NOTES.md
            --explain $srcFile | Select-Object -First 1)
'''

GOOD = '''$rs = "x.py"
$tag = "v1"
$srcFile = "/tmp/s.txt"
$lines = @(& python $rs --tag $tag --file a/RELEASE-NOTES.md --explain $srcFile)
$notes = $lines | Select-Object -First 1
'''


def continuation_lint(text):
    """PowerShell 的续行符是反引号 `，**不是** 反斜杠。

    83 轮我自己踩的：给 gen_latest_json.ps1 加 case 12 时按 shell 习惯写了
    `... -NotesFile $nf \\` + 换行。Parser 不报错（`\\` 被当成一个普通参数），
    于是它表现为运行期的 `Cannot bind argument to parameter 'Path' because it
    is an empty string.` —— 上面那道 Parser 门抓不到这类错，因为它语法合法。
    判据：代码部分（去掉行尾 # 注释、跳过 <# … #> 块）以 ` \\` 结尾。
    """
    hits = []
    in_block = False
    for i, line in enumerate(text.splitlines(), 1):
        stripped = line.strip()
        if not stripped:
            continue
        if in_block:
            if "#>" in stripped:
                in_block = False
            continue
        if stripped.startswith("<#"):
            if "#>" not in stripped:
                in_block = True
            continue
        code = stripped.split("#")[0].rstrip() if "#" in stripped else stripped.rstrip()
        if code.endswith("\\"):
            hits.append((i, stripped[:110]))
    return hits


def selftest(pwsh):
    n_ok = n_bad = 0

    def chk(name, cond, detail=""):
        nonlocal n_ok, n_bad
        if cond:
            n_ok += 1
            print(f"PASS {name}")
        else:
            n_bad += 1
            print(f"FAIL {name} | {detail}")

    if not pwsh:
        print("SKIP selftest：本机找不到 pwsh（装一个或设 PWSH=…），这道门在 CI 之外等于没跑")
        return 0
    e_bad = parse_block(pwsh, BROKEN)
    chk("S1 坏块（alpha.36 那条换行写法）被 Parser 拒绝", bool(e_bad), "居然没报错")
    # S2 要盯**报错内容**：第一版这里只判"有没有报错"，结果坏块和好块都因为
    # pwsh 读不到文件而报错，S1 看着过了，其实一次都没解析到源码。
    joined = " | ".join(e_bad)
    chk("S2 报的是真语法错（不是门自己坏了）",
        ("could not be read" not in joined) and ("TARGET-MISSING" not in joined)
        and ("门本身坏了" not in joined), joined)
    chk("S2b 错误定位到 alpha.36 那一类：括号没闭合",
        any("Missing closing" in x or "closing" in x for x in e_bad), joined)
    e_good = parse_block(pwsh, GOOD)
    chk("S3 好块通过", not e_good, str(e_good))
    # S4：GitHub 的 ${{ … }} 插值不该被误判成语法错（替换发生在 pwsh 之前）
    e_interp = parse_block(pwsh, '$tag = "${{ github.ref_name }}"\nWrite-Host $tag\n')
    chk("S4 工作流插值 ${{ … }} 不误报", not e_interp, str(e_interp))
    # S5：门自身坏掉时必须说"门坏了"，不能伪装成语法错、也不能装成通过。
    # （不写成 `... or True` 那种假判据——那正是我反复批评的东西。）
    r5 = subprocess.run([pwsh, "-NoProfile", "-Command", PARSE_PS],
                        capture_output=True, text=True, encoding="utf-8", errors="replace",
                        timeout=120, env=dict(os.environ, ZGJ_PS_PARSE_TARGET="/tmp/不存在.ps1"))
    chk("S5 目标文件缺失时门自己报错（退码 2 + TARGET-MISSING）",
        r5.returncode == 2 and "TARGET-MISSING" in (r5.stdout or ""),
        f"rc={r5.returncode} out={(r5.stdout or '').strip()[:120]}")
    # S6/S7：反斜杠续行门。Parser 抓不到（语法合法），只能靠文本判据。
    #   S6 坏写法必须点名；S7 反引号续行与注释里的反斜杠不得误报。
    bad_bs = continuation_lint('New-Manifest -Tag t -NotesFile $nf \\\n  -TempDir $tmp\n')
    chk("S6 反斜杠续行被点名", bool(bad_bs) and bad_bs[0][0] == 1, str(bad_bs))
    ok_bs = continuation_lint('New-Manifest -Tag t -NotesFile $nf `\n  -TempDir $tmp\n'
                              '# 用法：cmd a \\\n'
                              '<#\n  pwsh -File x.ps1 -Tag v1 \\\n#>\n')
    chk("S7 反引号续行/注释里的反斜杠不误报", not ok_bs, str(ok_bs))
    # S6b：门对真实脚本文件也要能跑（gen_latest_json.ps1 现在应该是干净的）
    real = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                        "client-v2", "scripts", "gen_latest_json.ps1")
    if os.path.exists(real):
        with open(real, encoding="utf-8") as f:
            hits = continuation_lint(f.read())
        chk("S6b 真脚本 gen_latest_json.ps1 无反斜杠续行", not hits, str(hits))
    print(f"\npwsh_syntax_gate selftest: {n_ok} 通过 / {n_bad} 失败")
    return 1 if n_bad else 0


def main():
    args = sys.argv[1:]
    pwsh = find_pwsh()
    if "--selftest" in args:
        return selftest(pwsh)
    files = [a for a in args if not a.startswith("-")] or DEFAULT_WORKFLOWS
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    # 反斜杠续行门：纯 Python，**不需要 pwsh 也能跑**，所以放在 pwsh 可用性判断之前。
    # 覆盖两处：仓库里的 *.ps1，以及 workflow 内联的 pwsh 段。
    lint_targets = []
    for dirpath in ("client-v2/scripts", "scripts"):
        d = os.path.join(root, dirpath)
        if os.path.isdir(d):
            for name in sorted(os.listdir(d)):
                if name.endswith(".ps1"):
                    lint_targets.append((os.path.join(dirpath, name), None))
    lint_bad = 0
    for rel, _ in lint_targets:
        try:
            with open(os.path.join(root, rel), encoding="utf-8") as fh:
                hits = continuation_lint(fh.read())
        except UnicodeDecodeError:
            hits = [("?", "文件不是 UTF-8")]
        if hits:
            lint_bad += 1
            print(f"FAIL 反斜杠续行 {rel}: {hits}")
    for f in files:
        blocks = extract_blocks(f)
        if blocks is None:
            continue
        for ident, shell, run in blocks:
            hits = continuation_lint(run)
            if hits:
                lint_bad += 1
                print(f"FAIL 反斜杠续行 {ident}: {hits}")
    if lint_bad:
        print(f"\n反斜杠续行：{lint_bad} 处 —— PowerShell 续行符是反引号 `，"
              "反斜杠会被当成一个普通参数，Parser 不报错但运行期必坏")
        return 1
    print(f"反斜杠续行：0 处（{len(lint_targets)} 个 .ps1 + workflow 内联段）")
    if not pwsh:
        print("SKIP pwsh 语法门：本机没有 pwsh（装 pwsh 7 或设 PWSH=/path/to/pwsh）。"
              "注意这是显式跳过，不代表 workflow 里的内联 PowerShell 没问题。")
        return 0
    total = bad = 0
    for f in files:
        blocks = extract_blocks(f)
        if blocks is None:
            print(f"SKIP {f}：没有 pyyaml，读不了 workflow")
            continue
        for ident, shell, run in blocks:
            total += 1
            errs = parse_block(pwsh, run)
            if errs:
                bad += 1
                print(f"FAIL {ident}  ({shell})")
                for e in errs[:5]:
                    print(f"     {e}")
            else:
                print(f"ok   {ident}")
    print(f"\n内联 PowerShell 块 {total} 段，语法错 {bad} 段")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
