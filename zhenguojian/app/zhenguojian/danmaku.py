# -*- coding: utf-8 -*-
"""弹幕：从核心里按时间窗口抓全一集，转成 ASS 字幕交给 mpv 渲染。

核心的 ``danmaku`` 动作一次只给一个 30 秒窗口（接口每窗口上限 90 条，和官方
App 一样），所以这里按 ``nextMs`` 逐窗走完。渲染不走 Qt 覆盖层：生成 _ASS_
字幕轨挂给 mpv，滚动用 ``\\move``，拖动进度条、全屏都由 libass 自己处理，
比在 mpv 的原生窗口上叠控件稳当得多。
"""
from __future__ import annotations

import os
import re
import tempfile
import time

WINDOW_MS = 30_000
# 弹幕接口对连续请求会抽样：背靠背请求一个窗口只回十几条，隔开两秒以上才回满
# （实测 2 秒稳定 90 条）。逐窗取的时候按这个间隔限速。
MIN_GAP_SECONDS = 3.0

# 字号/轨道按实际画面算：竖屏剧是 1080×1920，横屏是 1920×1080，
# 固定画布会让 libass 非等比缩放（字被拉高或压扁）。
DEFAULT_SIZE = (1920, 1080)
FONT_RATIO = 0.045          # 字号 ≈ 短边的 4.5%
AREA_RATIO = 0.5            # 弹幕只占画面上半部分
MAX_LANES = 12
SCROLL_SECONDS = 8.0
LANE_GAP_SECONDS = 0.25
MAX_RUNES = 60

ASS_HEADER = """[Script Info]
ScriptType: v4.00+
PlayResX: %d
PlayResY: %d
WrapStyle: 2
ScaledBorderAndShadow: yes

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Danmaku,Microsoft YaHei,%d,&H%sFFFFFF,&H%sFFFFFF,&H%s000000,&H%s000000,0,0,0,0,100,100,0,0,1,2,1,7,0,0,0,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
"""


def fetch_window(core, session, start_ms, duration_ms):
    """一个时间窗口的弹幕（接口单次上限 90 条，和官方 App 一样）。

    返回 ``(items, total)``。窗口从 ``start_ms`` 开始，长度由接口自己决定
    （通常 30 秒）。
    """
    if not session or duration_ms <= 0:
        return [], 0
    reply = core.call("danmaku", timeout=30, playbackSession=session,
                      startMs=int(start_ms), durationMs=int(duration_ms))
    items = list(reply.get("items") or [])
    items.sort(key=lambda row: int(row.get("timeMs") or 0))
    return items, int(reply.get("total") or 0)


def fetch_all(core, session, duration_ms, timeout=600):
    """整集弹幕：核心一次翻完所有 30 秒窗口并去重。

    只应该在用户勾选「下载时保存弹幕」时调用——它比在线播放的懒加载重，
    但换来的是离线可用的完整弹幕。返回 ``(items, total)``。
    """
    duration_ms = int(duration_ms or 0)
    if not session or duration_ms <= 0:
        return [], 0
    reply = core.call("danmakuAll", timeout=timeout, playbackSession=session,
                      startMs=0, durationMs=duration_ms)
    items = list(reply.get("items") or [])
    items.sort(key=lambda row: int(row.get("timeMs") or 0))
    return items, int(reply.get("total") or 0)


def document(items, total, episode_id="", series_id=""):
    """下载目录里 .danmaku.json 的内容；字段和核心的 nativeDanmakuDocument 一致。"""
    return {
        "episodeId": str(episode_id or ""),
        "seriesId": str(series_id or ""),
        "total": int(total or len(items or [])),
        "fetchedAt": int(time.time() * 1000),
        "items": list(items or []),
    }


def write_file(text, path):
    """原子写入任意文本文件（.ass / .danmaku.json）。"""
    path = str(path or "")
    if not path:
        raise ValueError("path is empty")
    folder = os.path.dirname(path)
    if folder:
        os.makedirs(folder, exist_ok=True)
    temporary = path + ".tmp"
    with open(temporary, "w", encoding="utf-8") as handle:
        handle.write(text)
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, path)
    return path


def _stamp(seconds):
    seconds = max(0.0, float(seconds))
    centis = int(round(seconds * 100))
    hours, rest = divmod(centis, 360000)
    minutes, rest = divmod(rest, 6000)
    whole, frac = divmod(rest, 100)
    return "%d:%02d:%02d.%02d" % (hours, minutes, whole, frac)


def _clean(text):
    text = str(text or "").replace("\r", " ").replace("\n", " ").strip()
    # ASS 里花括号是覆盖指令，反斜杠是转义前缀，换成全角免得被当成控制符
    text = text.replace("{", "（").replace("}", "）").replace("\\", "＼")
    if len(text) > MAX_RUNES:
        text = text[:MAX_RUNES] + "…"
    return text


def _width(text, size):
    total = 0.0
    for char in text:
        total += size if ord(char) > 0x2E80 else size * 0.55
    return total


def build_ass(items, duration_ms, size=DEFAULT_SIZE, opacity=1.0):
    """把弹幕列表拼成一条 ASS 字幕。没有可用条目时返回空串。

    ``opacity`` 是 0-1 的不透明度：ASS 的 alpha 是反过来的（00 不透明、
    FF 全透明），所以主色、描边、阴影统一按它算。
    """
    width, height = (int(size[0]) or DEFAULT_SIZE[0], int(size[1]) or DEFAULT_SIZE[1])
    opacity = max(0.05, min(1.0, float(opacity or 1.0)))
    alpha = "%02X" % int(round((1.0 - opacity) * 255))
    font = max(24, int(min(width, height) * FONT_RATIO))
    line = int(font * 1.25)
    lanes = min(MAX_LANES, max(6, int(height * AREA_RATIO) // line))
    top_margin = max(12, int(font * 0.6))
    lane_free = [0.0] * lanes
    rows = []
    limit = max(0.0, float(duration_ms) / 1000.0)
    for item in items:
        text = _clean(item.get("text"))
        if not text:
            continue
        start = int(item.get("timeMs") or 0) / 1000.0
        if start < 0 or start > limit + 5.0:
            continue
        text_width = min(_width(text, font), width * 0.9)
        lane = next((index for index, free in enumerate(lane_free) if free <= start), -1)
        if lane < 0:
            continue            # 所有轨道都占着，这一条就不刷了（和官方客户端一样丢）
        lane_free[lane] = start + SCROLL_SECONDS * text_width / (width + text_width) + LANE_GAP_SECONDS
        top = top_margin + lane * line
        # 从右边界外走入、走到左边界外：起点 x=W，终点 x=-文本宽
        rows.append(
            "Dialogue: 0,%s,%s,Danmaku,,0,0,0,,{\\move(%d,%d,%d,%d,0,%d)}%s"
            % (_stamp(start), _stamp(start + SCROLL_SECONDS),
               width + 40, top, -int(text_width) - 40, top,
               int(SCROLL_SECONDS * 1000), text))
    if not rows:
        return ""
    return (ASS_HEADER % (width, height, font, alpha, alpha, alpha, alpha)) + "\n".join(rows) + "\n"


def write_ass(text, key, keep=6):
    """写到临时目录，返回路径。

    同一集固定用同一个文件名：抓取过程中每 30 秒补一批，播放器用 mpv 的
    ``sub-reload`` 重读这个文件就能增量刷新，不用反复挂轨。
    """
    folder = os.path.join(tempfile.gettempdir(), "zhenguojian-danmaku")
    os.makedirs(folder, exist_ok=True)
    safe = re.sub(r"[^0-9A-Za-z._-]+", "_", str(key or ""))[-60:].strip("_") or "episode"
    path = os.path.join(folder, safe + ".ass")
    temporary = path + ".tmp"
    with open(temporary, "w", encoding="utf-8") as handle:
        handle.write(text)
    os.replace(temporary, path)
    try:
        stale = sorted((os.path.join(folder, name) for name in os.listdir(folder)
                        if name.endswith(".ass")), key=os.path.getmtime, reverse=True)
        for old in stale[keep:]:
            os.remove(old)
    except OSError:
        pass
    return path
