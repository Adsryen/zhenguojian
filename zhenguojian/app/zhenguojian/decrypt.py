# -*- coding: utf-8 -*-
"""Turns raw CENC downloads into playable, named episode files.

The native core stores each episode as an encrypted ``media.mp4`` plus a
16-byte content key.  mpv and every external player need the clear stream,
so the finished download is decrypted once, on the spot, and written to a
human readable ``<剧名>/<剧名> 第NN集.mp4`` path.  The encrypted original is
removed afterwards so nothing unplayable is left behind.
"""
from __future__ import annotations

import os
import re
import shutil

from . import media, tools


class DecryptError(media.MediaError):
    pass


ILLEGAL = re.compile(r'[\\/:*?"<>|\x00-\x1f]')
EPISODE_PREFIX = re.compile(r"^第\s*0*(\d+)\s*[集话]")
# Windows device names; harmless on modern builds but they break older ones
# and any tool that resolves paths through the legacy DOS layer.
_RESERVED = {"CON", "PRN", "AUX", "NUL"} | {"COM%d" % i for i in range(1, 10)} \
            | {"LPT%d" % i for i in range(1, 10)}


def safe_name(value, fallback="短剧", limit=60):
    """Filesystem-safe fragment, trimmed but never empty."""
    text = ILLEGAL.sub("_", (value or "")).strip().strip(" .")
    text = text[:limit].rstrip(" .")
    if not text:
        return fallback
    if text.split(".")[0].upper() in _RESERVED:
        text = "_" + text
    return text[:limit]


def _suffix(path):
    extension = os.path.splitext(path)[1].lower()
    return extension if extension in (".mp4", ".mkv", ".mov", ".m4v") else ".mp4"


def episode_filename(drama_title, index, chapter_title="", total=None):
    """``穿越六零 第012集.mp4`` - stable, sortable, and unambiguous."""
    number = int(index or 0)
    if total and total > 1:
        width = max(3, len(str(int(total))))
    else:
        width = 3
    name = "%s 第%0*d集" % (safe_name(drama_title), width, number)
    if chapter_title:
        extra = safe_name(chapter_title, "", 24)
        match = EPISODE_PREFIX.match(extra or "")
        if match and int(match.group(1)) == number:
            # The number is already in the name; keep only what the title adds
            # ("大结局", "上", ...).  Compared numerically because the feed
            # writes 第9集 while the name carries 第009集.
            extra = extra[match.end():].strip(" ·-—、:：")
        if extra and extra not in name:
            joiner = "" if extra[0] in "（(【[" else " "
            name = "%s%s%s" % (name, joiner, extra)
    return name


def series_folder(drama_title, limit=60):
    """One folder per drama, so episodes group by season/series."""
    return safe_name(drama_title, limit=limit)


def is_decrypted(path):
    """True when the file plays on its own (no key needed)."""
    try:
        info = media.probe(path)
    except media.MediaError:
        return False
    video = info.get("video") or {}
    if not video:
        return False
    return bool(video.get("codec_name"))


def decrypt_to(source, target, key, timeout=3600):
    """Stream-copy the decrypted clear video into ``target``.

    Output goes to ``<target>.part`` first, so a killed/timed-out run can
    never leave a half-written file that looks like a playable episode.
    """
    ffmpeg = tools.ffmpeg_path()
    if not ffmpeg:
        raise DecryptError("缺少 ffmpeg 组件")
    os.makedirs(os.path.dirname(target), exist_ok=True)
    temporary = target + ".part"
    try:
        if not key:
            # Nothing to strip; copy the file so the layout stays uniform.
            shutil.copyfile(source, temporary)
        else:
            args = [ffmpeg, "-y", "-loglevel", "error", "-nostdin",
                    "-decryption_key", key, "-i", source,
                    "-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn",
                    "-c", "copy", "-movflags", "+faststart",
                    "-avoid_negative_ts", "make_zero", "-f", "mp4", temporary]
            try:
                media._run(args, timeout=timeout)
            except media.MediaError as exc:
                raise DecryptError("解密失败：%s" % exc)
        if not os.path.isfile(temporary) or os.path.getsize(temporary) == 0:
            raise DecryptError("解密后没有生成可播放文件")
        if not is_decrypted(temporary):
            raise DecryptError("解密后的文件仍无法解析，请重试该集")
        os.replace(temporary, target)
    except BaseException:
        try:
            os.remove(temporary)
        except OSError:
            pass
        raise
    return target
