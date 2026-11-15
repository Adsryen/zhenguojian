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
# 83 轮实测：这一跑静默死在 `pkill -x mpv` 那一行 —— 日志里没有任何 FAIL/PASS，
# 进程也没了。没有退出陷阱就只能靠猜，所以先把「死在哪一行、什么退码、最后一条
# 命令是什么」打出来（被信号杀也一样跑 trap）。
# 收尾必须自己关掉 app 与 mpv：83 轮实测冒烟跑完不杀，client-v2-linux + mpv
# 继续拉流，紧接着的 run_all 里 discover_hero_verify 因负载被抢出现**假失败**
# （套件里 5/15 红、单独重跑 15/15 绿）。
# ⚠ bash 的 EXIT trap 是**后设的覆盖先设的**，不是叠加——所以诊断和收尾必须
# 写在同一个 trap 里，分两次 `trap ... EXIT` 会把前一条悄悄丢掉。
trap 'rc=$?; pkill -x client-v2-linux 2>/dev/null || true; pkill -x mpv 2>/dev/null || true; echo "EXIT: rc=$rc line=$LINENO last=[$BASH_COMMAND]" >&2' EXIT
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
pkill -x client-v2-linux 2>/dev/null || true; pkill -x mpv 2>/dev/null || true; sleep 2
setsid nohup timeout "${SMOKE_TTL:-600}" "$BIN" > "$OUT/app.log" 2>&1 < /dev/null &
sleep 22
W=$(xdotool search --onlyvisible --name "真果鉴" | head -1)
[ -n "$W" ] || { echo "FAIL 窗口没起来，见 $OUT/app.log"; tail -5 "$OUT/app.log"; exit 1; }
click(){ xdotool mousemove --window "$W" "$1" "$2" click 1; sleep "${3:-5}"; }
shot(){ import -window root "$OUT/$1.png"; echo "  $1 $(stat -c%s "$OUT/$1.png")B"; }

echo "== 冒烟：逐页 =="
# 83 轮的实测缺陷：原来只 print 每张截图的字节数，**从不检查页面真的换了**，
# 而且我硬编码的 y 表整体偏了半行。逐张核对截图后真实条目中心是
#   找剧109 剧库推荐144 搜索178 新剧预约214 追更预约286 观看历史321 下载与合并355 设置428
# 原表 search:211 / reserve:283 / history:355 / downloads:427 / settings:499
# 五次点击**全部打错行**（page_downloads 与 page_settings 逐字节相同、高亮都在 427），
# 也就是说"7 个页面渲染正常"这句话里只有 3 页是真的。
# 判据换成「侧栏高亮项的中心 y 必须等于我点的那一行」：内容 md5 会被加载转圈、
# 封面数量、鼠标位置污染（既可能假变也可能假不变），高亮项不会说谎。
# 同一坐标重试三次：区分「点击时机撞上页面重渲染」与「导航真的坏了」——
# 重试能过就是时机问题，三次都不过才是缺陷。
NAV_FAIL=0
for spec in "discover:109" "library:144" "search:178" "reserve:214" "follow:286" "history:321" "downloads:355" "settings:428"; do
  name=${spec%%:*}; y=${spec#*:}
  OK=0; ACT_LAST=""
  for try in 1 2 3; do
    click 100 "$y" 5
    import -window root "$OUT/page_$name.png" 2>/dev/null || true
    ACT=$(python3 "$HERE/scripts/smoke_nav.py" "$OUT/page_$name.png")
    ACT_LAST="$ACT"
    case "$ACT" in
      # 空（读不到高亮）或"多个簇"（含空格，不是单个数字）都算没到达。
      # ⚠ 这里原来写的是 `set +e; DIFF=…; set -e`：本脚本第 22 行只有
      # `set -uo pipefail`，**根本没有 -e**，我那句"恢复"等于从这行起偷偷打开 -e，
      # 于是后面第一条合法返回 1 的命令（`pkill -x mpv` 没匹配到任何进程）
      # 就把整只脚本静默杀掉 —— 日志停在「进详情并播放」，一句 FAIL 都没有。
      # 83 轮实测 EXIT 陷阱打的就是这条：rc=1 line=1 last=[pkill -x mpv 2> /dev/null]
      ''|*[!0-9]*) DIFF=9999 ;;
      *) DIFF=$(( ACT - y )); DIFF=${DIFF#-} ;;
    esac
    if [ -n "$ACT" ] && [ "$DIFF" -le 12 ]; then
      OK=1; break
    fi
    [ "$try" = "1" ] && echo "  ⚠ $name：点了 y=$y，高亮却在「${ACT:-（读不到）}」，重试"
  done
  if [ "$OK" != "1" ]; then
    echo "  FAIL 页面 $name 没到达：点 y=$y 后高亮在「$ACT_LAST」（重试 3 次都一样）"
    NAV_FAIL=1
  else
    echo "  OK $name → 高亮 $ACT（点的是 $y）"
  fi
done
[ "$NAV_FAIL" = "0" ] || { echo "FAIL 有页面没到达，冒烟不算通过"; exit 1; }
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
# ⚠ 起播判据必须盯"**新**出现的 mpv"，不能只数 mpv 个数：
# 83 轮实测上一轮冒烟留下的 mpv 会活过 `pkill -x client-v2-linux`（它是 app 的子进程，
# 父进程死了它变孤儿继续拉流），于是这一跑明明**没进详情页**，
# 最后一行照样打印「起播成功，mpv 进程数=1」——假通过。
pkill -x mpv 2>/dev/null || true; sleep 2
if [ -n "$(pgrep -x mpv)" ]; then
  echo "FAIL 还有残留 mpv 清不掉（pid: $(pgrep -x mpv | tr '\n' ' ')），判据不可信"; exit 1
fi
MPV_BEFORE="none"
# 进详情也一样：网格会随顶部横幅（如「部分封面加载失败」）整体下移，
# 所以逐个候选点试，用「找得到主按钮的颜色」当作**已经在详情页**的判据。
PLAY_Y=""
PLAY_SRC="n/a"   # set -u 下必须先给值：没进详情页那条路也会走到最后的判断
FAILS=0
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
  FAILS=$((FAILS + 1))
else
  echo "  详情页已进，主按钮 y=$PLAY_Y"
  # 等 20s 不是 9s：冷启动要串「取会话 → 解析线路 → 起 mpv」，
  # 83 轮实测 9s 时这里报「按颜色点位没起播」并走退回扫描，
  # 而退回的第一个点恰好也起了播 —— 于是**分不清是颜色点位算错了，
  # 还是第一次点击本来就成功只是慢**。这种歧义留着，下次真回归就看不出来了。
  xdotool mousemove --window "$W" 283 "$PLAY_Y" click 1
  sleep 20
  PLAY_SRC="color"
  if [ -z "$(pgrep -x mpv)" ]; then
    echo "  按颜色点位（y=$PLAY_Y）等 20s 仍没起播，退回候选 y 扫描"
    PLAY_SRC="none"
    for y in 334 356 370 390 312; do
      xdotool mousemove --window "$W" 283 "$y" click 1; sleep 10
      [ -n "$(pgrep -x mpv)" ] && { PLAY_Y="scan$y"; PLAY_SRC="scan"; break; }
    done
  fi
fi
shot playing
MPV_NOW=$(pgrep -x mpv | tr '\n' ' ')
if [ -z "$PLAY_Y" ]; then
  # 没进详情页时**绝不能**报起播成功，哪怕机器上有个 mpv 在跑（那多半是别的来源）。
  echo "  FAIL 没走到播放这一步（详情页没进），不判起播"
  FAILS=$((FAILS + 1))
elif [ -n "$MPV_NOW" ]; then
  echo "  起播成功（按钮 y=$PLAY_Y，来源=$PLAY_SRC），新 mpv pid=$MPV_NOW"
  # 来源=scan 且 scan 的 y 与颜色点位相同 ⇒ 颜色算对了、只是原来的等待太短；
  # 来源=scan 且 y 不同 ⇒ 颜色判据本身要修。两种都要在日志里留下证据。
  [ "$PLAY_SRC" = "scan" ] && echo "  ⚠ 走了退回扫描：颜色点位=$PLAY_Y，需对照 detail.png 判断是等待问题还是算错"
else
  echo "  FAIL 没能起播（点完 y=$PLAY_Y 之后 mpv 进程数=0）——这才算产品侧失败"
  FAILS=$((FAILS + 1))
fi
grep -aiE "panic|fatal|runtime error" "$OUT/app.log" | head -3 || true
echo "截图目录：$OUT"
if [ "$FAILS" -gt 0 ]; then
  echo "FAIL 冒烟有 $FAILS 项没通过（见上面 FAIL 行）"
  exit 1
fi
echo "PASS 冒烟通过：逐页都真的换了页，且播放起了新 mpv"
