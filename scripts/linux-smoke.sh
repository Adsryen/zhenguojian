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
click 100 107 4; click 514 400 7; shot detail
click 283 370 14; shot playing
echo "  mpv 进程数=$(pgrep -x mpv | wc -l)  （0 表示没真播起来，别把截图当'能播'）"
click 100 427 7; shot after_leave
echo "  离开播放页后 mpv 进程数=$(pgrep -x mpv | wc -l)"
grep -aiE "panic|fatal|runtime error" "$OUT/app.log" | head -3
echo "截图目录：$OUT"
