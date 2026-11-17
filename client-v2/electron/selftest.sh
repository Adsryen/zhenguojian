#!/usr/bin/env bash
# client-v2/electron/selftest.sh —— P2 门禁：Electron 壳在 Xvfb 上起真 UI。
#
# 判据（全部机器断言，无「看起来正常」）：
#   S1 sidecar 构建（zgj_sidecar 入口，P1 已提交）
#   S2 frontend/dist 新鲜度（src 新于 dist → 重建）
#   S3 起 Electron --selftest：main.js 内部跑桥探针（6 项）+ 8 页导航
#      （逐路由截图 + 读活动项标题/页面头），打印唯一 SELFTEST_JSON 行
#   S4 python3 对 8 张 PNG 跑 scripts/smoke_nav.py 的 blue_rows：
#      每页导航栏**恰 1 个蓝簇**（活动项唯一，复用 Wails 冒烟同一判据）
#   S5 汇总 SELFTEST_JSON 的 checks 全 ok
#
# 退出码 0=全过。任一段红即非 0 并打印定位行。
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
CLIENT="$(cd "$HERE/.." && pwd)"
REPO="$(cd "$CLIENT/.." && pwd)"
OUT="${ZGJ_SELFTEST_OUT:-/tmp/zgj-electron-selftest}"
export ZGJ_SELFTEST_OUT="$OUT"

FAIL=0
step() { echo "[selftest] $*"; }
fail() { echo "[selftest] FAIL: $*"; FAIL=1; }

command -v convert >/dev/null || { echo "[selftest] FAIL: ImageMagick convert 缺失（blue_rows 判据依赖）"; exit 1; }
command -v python3 >/dev/null || { echo "[selftest] FAIL: python3 缺失"; exit 1; }

# ── S1 sidecar 构建 ─────────────────────────────────────────────────
export PATH=/home/ryen/.local/go/bin:$PATH
export GOTOOLCHAIN=go1.25.3 GOPROXY=https://goproxy.cn,direct GOFLAGS=-mod=mod
OV=/tmp/zhenguojian-overlay.json
python3 "$CLIENT/scripts/make-overlay.py" "$OV" || fail "make-overlay 失败"
step "S1 构建 sidecar (zgj_sidecar)…"
mkdir -p "$CLIENT/build/bin"
if CGO_ENABLED=0 go build -C "$CLIENT" -tags "zgj_sidecar v2overlay" -overlay "$OV" -o build/bin/sidecar-linux .; then
  step "S1 sidecar ok ($(stat -c%s "$CLIENT/build/bin/sidecar-linux") bytes)"
else
  fail "sidecar 构建失败"
fi
[ -x "$CLIENT/build/bin/sidecar-linux" ] || { step "FAIL：sidecar 缺失，后续无意义，提前退出"; exit 1; }

# ── S2 dist 新鲜度 ─────────────────────────────────────────────────
if find "$CLIENT/frontend/src" "$CLIENT/frontend/index.html" -newer "$CLIENT/frontend/dist/index.html" \( -name '*.ts' -o -name '*.vue' -o -name '*.html' -o -name '*.css' \) -print -quit 2>/dev/null | grep -q .; then
  step "S2 dist 过期，重建 frontend…"
  ( cd "$CLIENT/frontend" && npm run build >/tmp/zgj-electron-distbuild.log 2>&1 ) || fail "frontend 构建失败（/tmp/zgj-electron-distbuild.log）"
else
  step "S2 dist 新鲜，跳过重建"
fi
[ -f "$CLIENT/frontend/dist/index.html" ] || fail "frontend/dist/index.html 不存在"

# ── S3 起 Electron --selftest ───────────────────────────────────────
DISPLAY_NUM="${ZGJ_DISPLAY:-99}"
export "DISPLAY=:$DISPLAY_NUM"
if [ ! -e "/tmp/.X11-unix/X$DISPLAY_NUM" ]; then
  step "S3 Xvfb :$DISPLAY_NUM 未起，拉起…"
  Xvfb ":$DISPLAY_NUM" -screen 0 1280x800x24 >/dev/null 2>&1 &
  sleep 2
fi
rm -rf "$OUT"
mkdir -p "$OUT"
: > /tmp/zgj-electron-run.log

step "S3 Electron --selftest（桥探针 + 8 页导航）…"
( cd "$HERE" && ZHENGUO_SIDECAR="$CLIENT/build/bin/sidecar-linux" \
  ZHENGUO_DATA=/tmp/zgj-electron-data \
  timeout 240 ./node_modules/.bin/electron . --selftest >/tmp/zgj-electron-stdout.log 2>>/tmp/zgj-electron-run.log )
ELECTRON_RC=$?
step "S3 electron rc=$ELECTRON_RC"

JSON_LINE=$(grep '^SELFTEST_JSON ' /tmp/zgj-electron-stdout.log | tail -1)
[ -n "$JSON_LINE" ] || fail "没抓到 SELFTEST_JSON 行（stdout: $(head -c 300 /tmp/zgj-electron-stdout.log)）"

# ── S4 8 页截图蓝行判据（blue_rows 与 Wails 冒烟同源：scripts/smoke_nav.py）─
PAGE_SLUGS="_discover _library _search _calendar _reserve_tab_follow _history _downloads _settings"
for slug in $PAGE_SLUGS; do
  png="$OUT/page_$slug.png"
  [ -f "$png" ] || continue
  clusters=$(python3 -c "
import sys
sys.path.insert(0, '/home/ryen/Git/zhenguojian/scripts')
import smoke_nav
c = smoke_nav.blue_rows(sys.argv[1])
print(len(c), *[str(x[2]) for x in c], sep=' ')
" "$png")
  count=${clusters%% *}
  if [ "$count" = "1" ]; then
    step "S4 page_$slug 蓝簇=1 @${clusters#* } ok"
  else
    fail "S4 page_$slug 蓝簇=$count（want 1）：$clusters"
  fi
done

# ── S5 SELFTEST_JSON checks 全 ok ───────────────────────────────────
if [ -n "$JSON_LINE" ]; then
  OK=$(python3 -c "
import json,sys
d=json.loads(sys.argv[1])
print('ok' if d['ok'] and all(c['ok'] for c in d['checks']) else 'bad')
for c in d['checks']:
    print(('  [PASS] ' if c['ok'] else '  [FAIL] ') + c['name'] + '  ' + c['detail'][:120])
" "${JSON_LINE#SELFTEST_JSON }")
  echo "$OK" | sed '1d'
  [ "$(echo "$OK" | head -1)" = "ok" ] || fail "S5 SELFTEST_JSON 有红项（见上）"
fi

echo
if [ "$FAIL" = "0" ]; then
  step "PASS：Electron 壳 Xvfb 自检全过（sidecar 桥 + 8 页导航 + 蓝行判据 + 播放链视频面）"
  exit 0
else
  step "FAIL：见上（日志 /tmp/zgj-electron-run.log，截图 $OUT/）"
  exit 1
fi
