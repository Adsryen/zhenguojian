#!/usr/bin/env bash
# linux-smoke —— 发版前我自己跑的 Linux GUI 冒烟（不需要用户参与）
#
# 为什么要有它：前面几轮我把用户当测试机，反复要截图。
# 实测这台机器就能跑：Go 用 GOTOOLCHAIN 钉版本、Xvfb 出显示、
# xdotool 发点击、import 截图。Linux 版能真实拉流播放。
#
# ⚠ 一条必须写死的边界：**内嵌（--wid 子窗）只在 Windows 实现**
#   （internal/mpv/embed_stub.go 非 Windows 直接返回错误），
#   所以这套冒烟能验证"应用能起、页面能渲染、流能拉、mpv 能出画面"，
#   **验证不了 Windows 的黑屏/z-order 问题**。别用它冒充那件事的结论。
#
# 三个我自己踩过的坑，都写进实现里：
#   1. 点击必须用 `xdotool mousemove --window $W x y click 1`。
#      不带 --window 时（Xvfb 无窗口管理器）事件落不到应用，
#      我一度把它当成"侧栏点不动"的产品缺陷。
#   2. 坐标要按实际布局取（导航块只到 x≈185，我点 200 全部落空）。
#   3. 别用 `pkill -f <名字>` 收尾——它会匹配到自己的命令行并把自己杀掉；
#      用 `pkill -x` 精确进程名。
#   4. 后台跑要用 setsid + 足够长的 timeout；短 timeout 到期会伪装成"应用崩溃"
#      （日志里的 "Ctrl+C detected" 就是这么来的）。
set -uo pipefail
HERE="$(cd "$(dirname "$0")/.." && pwd)"
export PATH=/home/ryen/.local/go/bin:$PATH
export GOTOOLCHAIN=go1.25.3 GOPROXY=https://goproxy.cn,direct GOFLAGS=-mod=mod CGO_ENABLED=1
export DISPLAY=${DISPLAY:-:99}
export WEBKIT_DISABLE_COMPOSITING_MODE=1 LIBGL_ALWAYS_SOFTWARE=1
export ZHENGUO_DATA=${ZHENGUO_DATA:-/tmp/zgj-smoke-data}
BIN=${1:-$HERE/client-v2/build/bin/client-v2-linux}
OUT=${OUT:-/tmp/zgj-smoke}
mkdir -p "$OUT" "$ZHENGUO_DATA"

# 冒烟必须能**自己产出被测产物**。以前 BIN 靠我手敲一条 go build 记在聊天记录里，
# 两个后果：换机器/隔一周就复现不了；更糟的是「产物是旧的」会伪装成
# 「这轮改动没问题」——83 轮我第一次跑的就是 06:36 的旧产物，
# 而前端 09:46 才重新构建过。
needs_build() {
  [ -x "$BIN" ] || return 0
  local n
  n=$(find "$HERE/client-v2" -maxdepth 6 -type f \( -name '*.go' -o -name '*.vue' -o -name '*.ts' \) \
        -not -path '*/node_modules/*' -not -path '*/dist/*' -not -path '*/build/*' \
        -newer "$BIN" 2>/dev/null | head -1)
  [ -n "$n" ]
}
build_linux() {
  echo "== 构建 Linux 产物（前端 + overlay + go）=="
  ( cd "$HERE/client-v2/frontend" && npm run build ) > "$OUT/build.log" 2>&1 \
    || { echo "FAIL 前端构建失败，见 $OUT/build.log"; tail -15 "$OUT/build.log"; exit 1; }
  python3 "$HERE/client-v2/scripts/make-overlay.py" "$OUT/overlay.json" >> "$OUT/build.log" 2>&1 \
    || { echo "FAIL overlay 生成失败，见 $OUT/build.log"; tail -5 "$OUT/build.log"; exit 1; }
  local ver; ver=$(git -C "$HERE" describe --tags --always 2>/dev/null || echo dev)
  ( cd "$HERE/client-v2" && go build -tags "v2overlay production devtools" -overlay "$OUT/overlay.json" \
      -trimpath -ldflags "-s -w -X main.buildVersion=$ver" -o "$BIN" . ) >> "$OUT/build.log" 2>&1 \
    || { echo "FAIL Go 构建失败，见 $OUT/build.log"; tail -15 "$OUT/build.log"; exit 1; }
  echo "  产物 $(stat -c%s "$BIN")B  buildVersion=$ver"
}
if needs_build; then
  echo "[产物过期或缺失，需要重建]"
  build_linux
else
  echo "[产物比所有源码新，直接用 $BIN —— 想强制重编就删掉它]"
fi

pgrep -x Xvfb >/dev/null || { Xvfb :99 -screen 0 1280x800x24 >/dev/null 2>&1 & sleep 3; }
pkill -x client-v2-linux 2>/dev/null; pkill -x mpv 2>/dev/null; sleep 2
setsid nohup timeout "${SMOKE_TTL:-600}" "$BIN" > "$OUT/app.log" 2>&1 < /dev/null &
sleep 22
W=$(xdotool search --onlyvisible --name "真果鉴" | head -1)
[ -n "$W" ] || { echo "FAIL 窗口没起来，见 $OUT/app.log"; tail -5 "$OUT/app.log"; exit 1; }
click(){ xdotool mousemove --window "$W" "$1" "$2" click 1; sleep "${3:-5}"; }
shot(){ import -window root "$OUT/$1.png"; echo "  $1 $(stat -c%s "$OUT/$1.png")B"; }

echo "== 冒烟：逐页 =="
for spec in "discover:100:107" "library:100:143" "search:100:211" "reserve:100:283" "history:100:355" "downloads:100:427" "settings:100:499"; do
  name=${spec%%:*}; xy=${spec#*:}; click "${xy%%:*}" "${xy#*:}" 4; shot "page_$name"
done
echo "== 冒烟：进详情并播放 =="
# 两条实测教训，都写进实现里：
#  1. 播放按钮的 y **不是常量**：它在简介/标签下方，标签换几行、
#     「看到第 N 集」这行在不在，都会把它推移几十像素。
#     83 轮先固定点 370（对某个剧恰好命中），换成别的剧就打在按钮下方——
#     mpv 进程数=0，看起来像产品回归，其实是我的点击没命中。
#  2. 所以改成**按颜色找按钮**：主按钮填充实测是 srgb(74,131,255)。
#     沿 x=283 这条竖线扫 y=295..445，取第一段连续 >=12 个蓝像素的中点。
#     找不到才退回候选 y 扫描。
find_play_y() {  # $1=png，输出按钮中心 y（找不到输出空）
  convert "$1" -crop 1x150+283+295 +repage txt:- 2>/dev/null |
    awk -F'[(,)]' -v top=295 '
      NR>1 { y = $2 + top; r = $3; g = $4; b = $5
             acc = (b > 120 && b - r > 60 && b - g > 40)
             if (acc) { if (run == 0) start = y; run++
                        if (run == 12) { printf "%d\n", int((start + y) / 2); exit } }
             else run = 0 }'
}
click 100 107 4
# 进详情也一样：网格会随顶部横幅（如「部分封面加载失败」）整体下移，
# 所以逐个候选点试，用「找得到主按钮的颜色」当作**已经在详情页**的判据。
PLAY_Y=""
for spot in "514 400" "300 400" "730 400" "514 660"; do
  set -- $spot
  click "$1" "$2" 6
  import -window root "$OUT/_probe_detail.png"
  PLAY_Y=$(find_play_y "$OUT/_probe_detail.png")
  [ -n "$PLAY_Y" ] && break
done
cp "$OUT/_probe_detail.png" "$OUT/detail.png"
if [ -z "$PLAY_Y" ]; then
  echo "  FAIL 四个候选点都没进详情页（找不到主按钮的颜色特征）——看 $OUT/detail.png"
else
  echo "  详情页已进，主按钮 y=$PLAY_Y"
  xdotool mousemove --window "$W" 283 "$PLAY_Y" click 1
  sleep 9
  if [ "$(pgrep -x mpv | wc -l)" -lt 1 ]; then
    echo "  按颜色点位没起播，退回候选 y 扫描"
    for y in 334 356 370 390 312; do
      xdotool mousemove --window "$W" 283 "$y" click 1; sleep 8
      [ "$(pgrep -x mpv | wc -l)" -ge 1 ] && { PLAY_Y="scan$y"; break; }
    done
  fi
fi
shot playing
if [ "$(pgrep -x mpv | wc -l)" -ge 1 ]; then
  echo "  起播成功（按钮 y=$PLAY_Y），mpv 进程数=$(pgrep -x mpv | wc -l)"
else
  echo "  FAIL 没能起播（mpv 进程数=0）——这才算产品侧失败"
fi
grep -aiE "panic|fatal|runtime error" "$OUT/app.log" | head -3
echo "截图目录：$OUT"
