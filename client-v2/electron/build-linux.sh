#!/usr/bin/env bash
# client-v2/electron/build-linux.sh —— P5b：Linux 版 Electron 打包 + 打包态自检。
#
# 为什么先做 Linux：本机是 Linux，能**真实打包 + 真实运行**（Xvfb 下跑完整
# 播放链）。打包态 isPackaged=true 走的是 process.resourcesPath 路径解析
# （sidecar/frontend/runtime 布局），与 Windows 打包态**逻辑同形**（只差资源
# 根目录），所以 Linux 打包态绿 = 布局/路径/播放链在打包形态下成立；Windows
# 的「视频看得见」仍需 CI/实机像素确认（electron-builder 交叉打包的 Win 包
# 本机跑不了）。
#
# 步骤：
#   B1 sidecar-linux 新鲜（缺/旧则 go build）
#   B2 填 staging/runtime（mpv + ffmpeg + ffprobe，从系统拷贝——已验证可独立跑）
#   B3 electron-builder --dir 打包（unpacked，最快、可直接跑）
#   B4 打包态自检：跑 <out>/linux-unpacked/zgj-v2 --selftest（Xvfb），
#      主进程走 isPackaged=true 路径，解析 SELFTEST_JSON
# 退出 0=全过。
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
CLIENT="$(cd "$HERE/.." && pwd)"
REPO="$(cd "$CLIENT/.." && pwd)"
export PATH=/home/ryen/.local/go/bin:$PATH
export GOTOOLCHAIN=go1.25.3 GOPROXY=https://goproxy.cn,direct GOFLAGS=-mod=mod

step() { echo "[build-linux] $*"; }
fail() { echo "[build-linux] FAIL: $*"; exit 1; }

# ── B1 sidecar-linux ────────────────────────────────────────────────
export PATH=/home/ryen/.local/go/bin:$PATH
OV=/tmp/zhenguojian-overlay.json
python3 "$CLIENT/scripts/make-overlay.py" "$OV" || fail "make-overlay 失败"
mkdir -p "$CLIENT/build/bin"
if find "$CLIENT/app.go" "$CLIENT/embed_host.go" "$CLIENT/sidecar_main.go" "$CLIENT/internal" \
   -name '*.go' -newer "$CLIENT/build/bin/sidecar-linux" -print -quit 2>/dev/null | grep -q .; then
  step "B1 sidecar 源码较新，重建…"
  CGO_ENABLED=0 go build -C "$CLIENT" -tags "zgj_sidecar v2overlay" -overlay "$OV" -o build/bin/sidecar-linux . || fail "sidecar 构建失败"
else
  step "B1 sidecar 新鲜（$(stat -c%s "$CLIENT/build/bin/sidecar-linux") bytes）"
fi
[ -x "$CLIENT/build/bin/sidecar-linux" ] || fail "sidecar-linux 缺失"

# ── B2 staging/runtime（mpv + ffmpeg + ffprobe，系统拷贝）────────────
STAGE="$HERE/staging"
rm -rf "$STAGE"
mkdir -p "$STAGE/runtime/mpv" "$STAGE/runtime/ffmpeg"
for t in mpv ffmpeg ffprobe; do
  src=$(command -v "$t" || true)
  [ -n "$src" ] || fail "系统缺 $t（无法填 staging/runtime）"
  cp "$src" "$STAGE/runtime/$( [ "$t" = mpv ] && echo mpv || echo ffmpeg )/"
done
chmod +x "$STAGE/runtime/mpv/mpv" "$STAGE/runtime/ffmpeg/"*
cat > "$STAGE/README-RESOURCE" <<EOF
真果鉴 V2 Electron 包 runtime 资源
mpv + ffmpeg + ffprobe（$(date +%F) 本机 Linux 打包；Windows 包由 CI 下载官方构建填入）
EOF
step "B2 staging/runtime 填好：$(ls "$STAGE/runtime/mpv" "$STAGE/runtime/ffmpeg" | tr '\n' ' ')"

# ── B3 electron-builder --dir ───────────────────────────────────────
step "B3 electron-builder --dir（unpacked）…"
( cd "$HERE" && ./node_modules/.bin/electron-builder --dir --publish never --linux dir ) || fail "electron-builder --dir 失败"
APPDIR="$HERE/out/linux-unpacked"
[ -x "$APPDIR/zgj-v2" ] || fail "打包产物缺失：$APPDIR/zgj-v2"
step "B3 打包完成：$APPDIR"
# 校验布局（打包态 main.js 依赖这些路径存在）
[ -f "$APPDIR/resources/sidecar-linux" ] || fail "resources/sidecar-linux 缺失"
[ -f "$APPDIR/resources/frontend/dist/index.html" ] || fail "resources/frontend/dist/index.html 缺失"
[ -f "$APPDIR/resources/runtime/mpv/mpv" ] || fail "resources/runtime/mpv/mpv 缺失"
step "B3 布局校验通过（sidecar/frontend/runtime 就位）"

# ── B4 打包态自检（isPackaged=true 路径）────────────────────────────
DISPLAY_NUM="${ZGJ_DISPLAY:-99}"
export "DISPLAY=:$DISPLAY_NUM"
[ -e "/tmp/.X11-unix/X$DISPLAY_NUM" ] || { step "B4 Xvfb :$DISPLAY_NUM 拉起…"; Xvfb ":$DISPLAY_NUM" -screen 0 1280x800x24 >/dev/null 2>&1 & sleep 2; }

PDATA=/tmp/zgj-electron-packaged-data
rm -rf "$PDATA"; mkdir -p "$PDATA/downloads"
# 测试视频（复用 selftest 素材；缺则 ffmpeg 现生成 30s）
if [ ! -f "$PDATA/downloads/p3-selftest.mp4" ]; then
  # -pix_fmt yuv420p：lavfi 源默认 444，统一成最保守介质（与 CI Windows 生成
  # 同参）。注意：Xvfb 打包态像素判据黑/绿交替系主窗叠加竞态，与此无关，
  # 见 main.js video_moving skip 分支注释。
  ffmpeg -y -hide_banner -loglevel error \
    -f lavfi -i testsrc=duration=30:size=1280x720:rate=25 \
    -f lavfi -i sine=frequency=440:duration=30 \
    -c:v libx264 -preset ultrafast -pix_fmt yuv420p -c:a aac "$PDATA/downloads/p3-selftest.mp4" || fail "测试视频生成失败"
fi

OUT=/tmp/zgj-electron-packaged-selftest
rm -rf "$OUT"; mkdir -p "$OUT"
step "B4 跑打包态 --selftest（isPackaged=true，$PDATA）…"
( cd "$HERE" && ZHENGUO_DATA="$PDATA" ZGJ_SELFTEST_OUT="$OUT" \
  timeout 300 "$APPDIR/zgj-v2" --selftest >/tmp/zgj-electron-pkg-stdout.log 2>>/tmp/zgj-electron-pkg-run.log )
ELECTRON_RC=$?
step "B4 packaged electron rc=$ELECTRON_RC"

JSON_LINE=$(grep '^SELFTEST_JSON ' /tmp/zgj-electron-pkg-stdout.log | tail -1)
[ -n "$JSON_LINE" ] || { step "FAIL：没抓到 SELFTEST_JSON（stdout: $(head -c 300 /tmp/zgj-electron-pkg-stdout.log)；run 尾: $(tail -5 /tmp/zgj-electron-pkg-run.log))"; exit 1; }
python3 -c "
import json,sys
d=json.loads(sys.argv[1])
print('verdict=' + ('ok' if d['ok'] else 'bad'))
for c in d['checks']:
    tag='SKIP' if c.get('skip') else ('PASS' if c['ok'] else 'FAIL')
    print('  ['+tag+'] '+c['name']+'  '+c['detail'][:140])
" "${JSON_LINE#SELFTEST_JSON }"
python3 -c "import json,sys; d=json.loads(sys.argv[1]); sys.exit(0 if d['ok'] else 1)" "${JSON_LINE#SELFTEST_JSON }" \
  || { step "FAIL：打包态自检有红项"; exit 1; }

step "PASS：Linux 打包态自检全过（isPackaged=true，resources 布局 + 播放链视频面）"
echo "[build-linux] 产物：$APPDIR  （AppImage 另跑 --linux AppImage）"
