#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""release_notes.py —— latest.json 里 notes 那一行摘要的唯一实现。

为什么这个逻辑要在 Python 里而不是内联 PowerShell：

上一轮我在 CI 里内联改了一版"跳过 # 标题行取第一条非空行"，
CI 绿了、我也核了 latest.json，结果 notes 变成了 `| 文件 | 用途 |`
—— 跳过标题之后撞上的第一条是 Markdown 表格头。
**症状修掉了，但"notes 应该是什么"从来没定义过**，所以内联脚本里
没有任何东西能告诉我它取错了。挪到这里就能带 --selftest，
判据本身变成可测的东西。

规则（按优先级）：

1. **tag 注释正文**（annotated tag 才有）。我每次发版都写了给人看的注释，
   那才是 notes 的正主；RELEASE-NOTES.md 是 CI 生成的资产表，天生不是摘要。
2. 注释里没有可用正文时，退回 `--file` 指定的发布说明文件。
3. 两处都没有就返回空串——**空串比一行表格头诚实**：
   设置页走 `text || t('update.notesEmpty')`（SettingsView.vue:227），
   空串显示的是「（无说明）」。这句是查过实现的，不是推测。

⚠ 别把这里和 `frontend/src/services/updatenotes.ts` 的 `cleanReleaseBody` 当成同一件事：
   那个把**整篇 Release body 渲染成多行可读文本**（表格行转成「文件 — 用途」小圆点），
   这个只产出 **latest.json 里的一行摘要**。形状不同、判据也不同
   （那边表格是要保留并转写的，这边表格必须整行丢弃）。
   看着像同一个规则的两个副本，实际是两个规格，**不要合并**。

"可用正文"的判据（first_prose）：跳过空行、Markdown 标题（#）、
表格行（|）、分隔线（---/***）、代码围栏（```）、HTML 注释（<!-- -->），
并把行首的列表符号（- * •、1.）去掉。最长 200 字符，超出截断加省略号。
"""

import argparse
import re
import subprocess
import sys

# ⚠ Windows runner 的默认 stdout 是 cp1252：本脚本的输出**必然是中文**
# （tag 注释、发布说明），上一版 CI 就是在 print("FAIL …中文…") 上
# UnicodeEncodeError 崩掉，而崩在打印 FAIL 的那一刻意味着
# "判据没过" 这条信息本身也没了。统一改 UTF-8，打不出就替换，绝不因编码崩。
try:
    for _f in (sys.stdout, sys.stderr):
        if hasattr(_f, "reconfigure"):
            _f.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

MAX_LEN = 200

_SKIP_RES = (
    re.compile(r"^#"),               # Markdown 标题
    re.compile(r"^\|"),              # 表格行（含分隔行 |:--|）
    re.compile(r"^-{3,}$"),          # 分隔线
    re.compile(r"^\*{3,}$"),
    re.compile(r"^_{3,}$"),
    re.compile(r"^```"),             # 代码围栏
    re.compile(r"^~~~"),
    re.compile(r"^<!--"),            # HTML 注释起始
    re.compile(r"^-->"),
)
_BULLET = re.compile(r"^\s*(?:[-*•+]|\d+[.)])\s+")


def first_prose(text):
    """返回 text 里第一条"给人看的正文"行，没有就返回空串。"""
    in_fence = False
    in_comment = False
    for raw in (text or "").splitlines():
        line = raw.strip()
        if not line:
            continue
        # 围栏**内部**的行一律不算正文，否则一段代码的第一行会被当成发布摘要
        if line.startswith("```") or line.startswith("~~~"):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        # HTML 注释同理：多行注释只跳首尾两行是不够的（selftest 抓到过），
        # 中间那行会被当成摘要。
        if in_comment:
            if line.endswith("-->"):
                in_comment = False
            continue
        if line.startswith("<!--"):
            if not line.endswith("-->"):
                in_comment = True
            continue
        if any(r.match(line) for r in _SKIP_RES):
            continue
        line = _BULLET.sub("", line).strip()
        if not line:
            continue
        if len(line) > MAX_LEN:
            line = line[:MAX_LEN].rstrip() + "…"
        return line
    return ""


def tag_annotation(tag, cwd=None):
    """取 annotated tag 的注释正文；lightweight tag / 取不到返回空串。

    用 for-each-ref 而不是 `git tag -n`：后者会把每行缩进进格式里，
    行首缩进会干扰"这是不是一行代码"的判断；for-each-ref 给原文。
    """
    if not tag:
        return ""
    try:
        # ⚠ 必须先判 objecttype：`%(contents)` 对 lightweight tag 返回的是
        #   **它所指向的 commit message**，不是空串（selftest 抓到这条）。
        #   于是"lightweight tag 退回发布说明文件"这个优先级会静默失效，
        #   notes 变成随机一条提交标题。
        t = subprocess.run(
            ["git", "for-each-ref", "--format=%(objecttype)", "refs/tags/" + tag],
            capture_output=True, text=True, cwd=cwd, timeout=30,
            encoding="utf-8", errors="replace")
        if t.returncode != 0 or t.stdout.strip() != "tag":
            return ""
        r = subprocess.run(
            ["git", "for-each-ref", "--format=%(contents)", "refs/tags/" + tag],
            capture_output=True, text=True, cwd=cwd, timeout=30,
            encoding="utf-8", errors="replace")
    except Exception:
        return ""
    if r.returncode != 0:
        return ""
    return r.stdout


def extract(tag, note_file=None, cwd=None):
    """按上面的优先级取 notes。永远返回字符串，不抛错（CI 里抛错只会让发布更难排）。"""
    got = first_prose(tag_annotation(tag, cwd=cwd))
    if got:
        return got
    if note_file:
        try:
            # utf-8-sig 不是多余：RELEASE-NOTES.md 由 CI 的 PowerShell 写，
            # pwsh 7 不带 BOM，但 Windows PowerShell 5.1 的 -Encoding utf8 带 ——
            # 带 BOM 时第一条非空行会是 "\ufeff## 标题"，判据会整条走偏。
            with open(note_file, "r", encoding="utf-8-sig", errors="replace") as f:
                return first_prose(f.read())
        except Exception:
            return ""
    return ""


def _selftest():
    ok = fail = 0

    def check(name, got, want):
        nonlocal ok, fail
        if got == want:
            ok += 1
        else:
            fail += 1
            print("FAIL %s\n  got  %r\n  want %r" % (name, got, want))

    # 1. 标题行 + 表格头：正是 alpha.31 踩中的形状，必须取正文而不是表格
    check("heading-then-table",
          first_prose("## 本 Release 资产说明\n\n| 文件 | 用途 |\n|---|---|\n真正的说明在这里\n"),
          "真正的说明在这里")
    # 2. 全是标题和表格 → 空串（不许拿表格头冒充摘要）
    check("all-structural",
          first_prose("# T\n## S\n| a | b |\n|---|---|\n"), "")
    # 3. 列表符号去掉，内容留下
    check("bullet", first_prose("- 修好了退出卡死\n"), "修好了退出卡死")
    check("numbered", first_prose("2. 第二条\n"), "第二条")
    # 4. CRLF（Windows 上生成的文件就是 CRLF，strip 后不能带 \r）
    check("crlf", first_prose("第一行\r\n第二行\r\n"), "第一行")
    # 5. 代码围栏与 HTML 注释跳过
    # 5b. 围栏里的代码行不得被当成摘要（加了围栏状态跟踪）
    check("fence-and-comment",
          first_prose("```\ncode\n```\n<!-- 别取我 -->\n正文\n"), "正文")
    check("comment-only-then-prose",
          first_prose("<!--\n多行注释\n-->\n正文\n"), "正文")
    # 6. 超长截断
    long = "字" * 250
    cut = first_prose(long)
    check("truncate-len", len(cut), MAX_LEN + 1)
    check("truncate-ellipsis", cut.endswith("…"), True)
    # 7. 空输入
    check("empty", first_prose(""), "")
    check("none", first_prose(None), "")
    # 8. 只有空白行
    check("whitespace-only", first_prose("   \n\t\n"), "")

    # 9. extract 的优先级：tag 注释优先于文件
    import tempfile, os
    d = tempfile.mkdtemp()
    subprocess.run(["git", "init", "-q", d], check=True,
                     encoding="utf-8", errors="replace")
    subprocess.run(["git", "-C", d, "commit", "-q", "--allow-empty", "-m", "base"],
                     encoding="utf-8", errors="replace",
                   check=True, env={"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t",
                                    "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t",
                                    "PATH": os.environ["PATH"], "HOME": os.environ.get("HOME", "/tmp")})
    with open(os.path.join(d, "n.md"), "w", encoding="utf-8") as f:
        f.write("## 资产说明\n| 文件 | 用途 |\n文件里的正文\n")
    # 9a. 没有 tag → 退回文件；文件里第一条可用正文是"文件里的正文"
    check("fallback-to-file", extract("no-such-tag-v0", os.path.join(d, "n.md"), cwd=d),
          "文件里的正文")
    # 9b. lightweight tag（无注释）→ 仍然退回文件
    subprocess.run(["git", "-C", d, "tag", "v-light"], check=True)
    check("lightweight-tag-falls-back", extract("v-light", os.path.join(d, "n.md"), cwd=d),
          "文件里的正文")
    # 9c. annotated tag → 注释优先，且取注释里的第一条可用正文
    subprocess.run(["git", "-C", d, "tag", "-a", "v-annot", "-m",
                    "v-annot —— 逃生门\n\n这一版让人能用起来。"], check=True,
                   env={"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t",
                        "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t",
                        "PATH": os.environ["PATH"], "HOME": os.environ.get("HOME", "/tmp"),
                        "GIT_COMMITTER_DATE": "2020-01-01T00:00:00+00:00"})
    check("annotation-wins", extract("v-annot", os.path.join(d, "n.md"), cwd=d),
          "v-annot —— 逃生门")
    # 9d. 注释里只有结构性内容 → 退回文件，不返回空
    subprocess.run(["git", "-C", d, "tag", "-a", "v-struct", "-m", "## 只有标题\n| a | b |"],
                   check=True,
                   env={"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t",
                        "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t",
                        "PATH": os.environ["PATH"], "HOME": os.environ.get("HOME", "/tmp"),
                        "GIT_COMMITTER_DATE": "2020-01-01T00:00:00+00:00"})
    check("struct-only-annotation-falls-back",
          extract("v-struct", os.path.join(d, "n.md"), cwd=d), "文件里的正文")
    # 10. 不存在的仓库/无文件 → 不抛错
    check("no-repo-no-file", extract("whatever", None, cwd="/tmp"), "")
    # 11. BOM：Windows PowerShell 5.1 的 -Encoding utf8 会写 BOM，
    #     读它必须用 utf-8-sig，否则第一条可用正文变成 "\ufeff## 标题"，
    #     判据会把标题当摘要发出去（静默错，不报错）。
    bom = os.path.join(d, "bom.md")
    with open(bom, "w", encoding="utf-8-sig") as f:
        f.write("## 资产说明\n\n带 BOM 的正文\n")
    check("bom-note-file", extract("no-such-tag-v0", bom, cwd=d), "带 BOM 的正文")
    # 12. stdout 必须是 UTF-8：alpha.32 的 Windows 构建就是栽在这里 ——
    #     print 中文 FAIL 行时 UnicodeEncodeError，**崩在报告失败的那一刻**，
    #     于是"判据没过"这条信息本身也丢了。这条断言让"去掉 reconfigure"
    #     在 Windows 上必红（Linux 默认 UTF-8，测不出差异，所以只能当真机门）。
    enc = (getattr(sys.stdout, "encoding", "") or "").lower().replace("-", "").replace("_", "")
    check("stdout-is-utf8", enc, "utf8")

    print("release_notes selftest: %d 通过 / %d 失败" % (ok, fail))
    return 0 if fail == 0 else 1


def main(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("--tag", default="")
    ap.add_argument("--file", default=None)
    ap.add_argument("--cwd", default=None)
    ap.add_argument("--selftest", action="store_true")
    args = ap.parse_args(argv)
    if args.selftest:
        return _selftest()
    print(extract(args.tag, args.file, cwd=args.cwd))
    return 0


if __name__ == "__main__":
    sys.exit(main())
