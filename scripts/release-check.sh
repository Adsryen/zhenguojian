#!/usr/bin/env bash
# release-check —— 说「发布了」之前必须过的机器判据
#
# 为什么要有它：62/63 轮我两次告诉用户「alpha.34 / alpha.35 已发布，请测这两个包」，
# 而它们的 CI **在 checkout 那一步就死了**（我给 fetch-tags 开了 true，tag 触发时
# git 报 "Cannot fetch both <sha> and refs/tags/<tag> to refs/tags/<tag>"，exit 128），
# 连 release 对象都没建出来。用户手上根本没有我说的那个包。
# 我起誓过的「诚实边界」在这件事上失效了：我报了「已发布」，却只验证了
# 「tag 推上去了」。**推上 tag ≠ 出包**，中间隔着整个 CI。
#
# 用法：
#   bash scripts/release-check.sh v2.0.0-alpha.36          # 已跑完，只核对
#   bash scripts/release-check.sh v2.0.0-alpha.36 --wait   # 等 CI 跑完再核对（最长 30 分钟）
set -uo pipefail

REPO_DIR=$(cd "$(dirname "$0")/.." && pwd)
OWNER=Adsryen/zhenguojian
WF=build-v2
PROXY=${PROXY:-http://192.168.0.36:17890}
TAG=${1:?用法: release-check.sh <tag> [--wait]}
WAIT=${2:-}
EXPECTED_ASSETS="zhenGuoJian-v2.exe zhenGuoJian-v2-app.zip zhenGuoJian-v2-runtime.zip build-info.json latest.json"

export HTTPS_PROXY=$PROXY
ghq() { gh api "$@" 2>/dev/null; }

fail() { echo "FAIL $1"; exit 1; }

SHA=$(git -C "$REPO_DIR" rev-parse "$TAG^{commit}") || fail "本地找不到 tag $TAG"
echo "[tag $TAG → sha ${SHA:0:12}]"

# ── 1) 找到这个 commit 上那条 build-v2 run ────────────────────────────────
# ⚠ 83 轮的教训：我在推完 tag 几秒后就跑 release-check --wait，
# 那时 GitHub 还没建出 run，脚本直接判「没有任何 build-v2 运行」退出。
# --wait 的语义必须包含**等 run 出现**，不然这道门在最快的用法下自己就是假失败。
find_run() {
  RUN_JSON=$(ghq "repos/$OWNER/actions/runs?head_sha=$SHA&per_page=100")
  RUN=$(python3 - "$RUN_JSON" "$WF" <<'PY'
import json,sys
d=json.loads(sys.argv[1]); want=sys.argv[2]
rs=[r for r in d.get('workflow_runs',[]) if r['name']==want]
if not rs: print(''); sys.exit()
r=max(rs, key=lambda x: x['id'])
print(f"{r['id']}|{r['status']}|{r['conclusion']}|{r['html_url']}")
PY
)
}
find_run
if [ "$WAIT" = "--wait" ] && [ -z "$RUN" ]; then
  echo "还没看到 $WF 的 run，等它出现（15s 一次，最长 5 分钟）…"
  for _ in $(seq 20); do
    sleep 15; find_run; [ -n "$RUN" ] && break; echo -n "."
  done
  echo
fi
[ -n "$RUN" ] || fail "这个 commit 上没有任何 $WF 运行——tag 可能没触发工作流（检查 on.push.tags）"
RUN_ID=${RUN%%|*}; REST=${RUN#*|}; RSTATUS=${REST%%|*}; RCONC=${REST#*|}; RURL=${RCONC#*|}; RCONC=${RCONC%%|*}
echo "[run $RUN_ID] status=$RSTATUS conclusion=${RCONC:-none}"

if [ "$WAIT" = "--wait" ] && [ "$RSTATUS" != completed ]; then
  echo "等待 CI 跑完（30s 一次，最长 30 分钟）…"
  for _ in $(seq 60); do
    sleep 30
    RSTATUS=$(ghq "repos/$OWNER/actions/runs/$RUN_ID" | python3 -c 'import json,sys;print(json.load(sys.stdin)["status"])')
    [ "$RSTATUS" = completed ] && break
    echo -n "."
  done
  echo
  RCONC=$(ghq "repos/$OWNER/actions/runs/$RUN_ID" | python3 -c 'import json,sys;print(json.load(sys.stdin)["conclusion"])')
  echo "[等待后] status=$RSTATUS conclusion=$RCONC"
fi
[ "$RSTATUS" = completed ] || fail "CI 还没跑完（status=$RSTATUS）。加 --wait 或直接看 $RURL"
[ "$RCONC" = success ] || {
  echo "CI 结论是 $RCONC，失败点："
  ghq "repos/$OWNER/actions/runs/$RUN_ID/jobs" | python3 -c '
import json,sys
for j in json.load(sys.stdin)["jobs"]:
    if j["conclusion"]!="success":
        print("  job:",j["name"],j["conclusion"])
        for st in j["steps"]:
            if st["conclusion"] not in ("success","skipped","neutral"):
                print("    step:",st["name"],"->",st["conclusion"])'
  fail "先看上面这一步，别急着重打 tag"
}

# ── 2) release 与资产必须真的存在 ─────────────────────────────────────────
REL=$(gh release view "$TAG" --json assets -q '[.assets[].name]|join(" ")' 2>/dev/null) \
  || fail "release $TAG 不存在——CI 没走到发布那步，用户拿不到任何包"
echo "[release 资产] $REL"
GOT_OK=1
for want in $EXPECTED_ASSETS; do
  case " $REL " in *" $want "*) : ;; *) echo "  缺资产：$want"; GOT_OK=0 ;; esac
done
[ $GOT_OK -eq 1 ] || fail "资产不齐（期望 5 个）"

# ── 3) latest.json 内容契约 + notes 来源 ──────────────────────────────────
URL="https://github.com/$OWNER/releases/download/$TAG/latest.json"
timeout 120 curl -sSfL "$URL" -o /tmp/release-check-latest.json || fail "latest.json 取不到：$URL"
python3 - /tmp/release-check-latest.json "$TAG" <<'PY'
import io, json, re, subprocess, sys
path, tag = sys.argv[1], sys.argv[2]
d = json.load(io.open(path, encoding='utf-8'))
bad = []
if d.get('schema') != 1: bad.append(f"schema={d.get('schema')} 应为 1")
if d.get('tag') != tag: bad.append(f"tag={d.get('tag')!r} 与请求的 {tag!r} 不符")
if d.get('version') != tag.lstrip('v'): bad.append(f"version={d.get('version')!r} 应等于 tag 去掉 v")
exe = (d.get('assets') or {}).get('exe') or {}
if not re.fullmatch(r'[0-9a-f]{64}', str(exe.get('sha256') or '')): bad.append("exe.sha256 不是 64 位小写十六进制")
if not isinstance(exe.get('size'), int) or exe['size'] <= 0: bad.append("exe.size 无效")
if not str(exe.get('url') or '').startswith('https://'): bad.append("exe.url 不是 https")
notes = str(d.get('notes') or '')
if not notes.strip(): bad.append("notes 是空串（更新弹窗会显示「（无说明）」）")
print(f"  notes 首行={notes.splitlines()[0][:52] if notes else '(空)'}")
# 关键一条：annotated tag 有注释时，notes 必须就是注释首行。
# 62 轮查出来 CI 默认 checkout 不取 tag 对象，notes 会**静默**退回发布说明文件那一行，
# 从资产内容上看不出任何异常——这条判据把它变成可见失败。
try:
    ann = subprocess.run(['git', 'for-each-ref', '--format=%(contents)', f'refs/tags/{tag}'],
                         cwd='/home/ryen/Git/zhenguojian', capture_output=True, text=True,
                         encoding='utf-8', errors='replace').stdout
    typ = subprocess.run(['git', 'cat-file', '-t', f'refs/tags/{tag}'],
                         cwd='/home/ryen/Git/zhenguojian', capture_output=True, text=True).stdout.strip()
    if typ == 'tag':
        first = next((ln.strip() for ln in ann.splitlines() if ln.strip()), '')
        if first and first != notes.splitlines()[0].strip():
            bad.append(f"notes 没用 tag 注释：注释首行={first[:40]!r} 实际={notes.splitlines()[0][:40]!r}"
                       f"（多半是 CI 没取到 tag 对象，静默走了文件回退）")
        else:
            print("  notes 来源=annotated tag 注释 ✓")
    else:
        print(f"  本地 tag 类型={typ}（不是 annotated tag），跳过 notes 来源比对")
except Exception as e:
    print(f"  [warn] notes 来源比对没跑成：{e}")
if bad:
    for b in bad: print("  FAIL", b)
    sys.exit(1)
print("  latest.json 契约检查通过")
PY
[ $? -eq 0 ] || fail "latest.json 内容不合格"

echo "PASS $TAG 确实出包了：run success、5 个资产齐、latest.json 契约过"
echo "     下载页 https://github.com/$OWNER/releases/tag/$TAG"
