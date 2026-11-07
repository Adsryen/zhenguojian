"""ffprobe / ffmpeg helpers: media probing plus episode merge."""
from __future__ import annotations

import hashlib
import json
import os
import re
import shutil
import subprocess
import threading
import time

from . import tools


class MediaError(RuntimeError):
    pass


# 合并成品的完整解码校验：片长越长上限越宽，并用“进度停滞”兜底。
# 2 小时只适用于短剧；1000 集（约 40 小时）在 4 核机器上要 3 小时上下。
VERIFY_MIN_DEADLINE = 7200.0       # 硬上限下限：2 小时
VERIFY_STALL_SECONDS = 600.0       # 连续 10 分钟没有推进 → 判定卡死
VERIFY_LOG_SECONDS = 30.0          # 进度日志间隔


def _verify_deadline(total):
    """校验硬上限：短剧 2 小时起，长剧按 6× 实时再加 30 分钟余量。"""
    return max(VERIFY_MIN_DEADLINE, float(total or 0) / 6.0 + 1800.0)


def _log_verify_tails(jobs, total, reason):
    """停滞/超时时把每个解码窗口的位置和最后几行输出写进日志。"""
    lines = ["合并校验%s：片长 %.2f 小时" % (reason, float(total or 0) / 3600.0)]
    for job in jobs:
        lines.append("窗口 %d 起点 %.1fs 最后 %.1fs" % (job["index"] + 1, job["start"], job["pos"]))
        tail = [row for row in job["tail"][-8:] if row]
        if tail:
            lines.extend(tail)
    tools.log_text("\n".join(lines))


# Every ffmpeg/ffprobe child is tracked so it can still be stopped when the
# window closes while a merge/decrypt pass is running.  Windows does not kill
# children with their parent, so without this an ffmpeg keeps writing into the
# merge folder long after the app has gone.
_running = set()
_running_lock = threading.Lock()


def _register(process):
    with _running_lock:
        _running.add(process)


def _unregister(process):
    with _running_lock:
        _running.discard(process)


def _kill_process(process, wait=5.0):
    """Kill one child and reap it; safe to call more than once."""
    try:
        process.kill()
    except Exception:
        pass
    try:
        process.wait(timeout=wait)
    except Exception:
        pass


def kill_running():
    """Stop every external tool started by this process (called on exit)."""
    with _running_lock:
        processes = list(_running)
    for process in processes:
        _kill_process(process, wait=1.0)


def _spawn_tracked(args, **kwargs):
    process = subprocess.Popen(args, **kwargs)
    _register(process)
    return process


def _run(args, timeout=None, progress=None, duration=None, cancel=None):
    """Run a tool, optionally reporting progress and honouring a cancel flag.

    The child is always reaped.  A timeout or cancellation kills it instead of
    leaving it running in the background, which used to leave multi-gigabyte
    ``part-*.ts`` files behind after the UI had already reported a failure.
    """
    process = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                               creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0)
                               if os.name == "nt" else 0)
    _register(process)
    tail = []
    started = time.time() if duration else None

    def pump():
        for raw in process.stdout:
            line = raw.decode("utf-8", "replace").strip()
            if line:
                tail.append(line)
                del tail[:-30]
                if progress and started and duration and "time=" in line:
                    stamp = re.search(r"time=(\d+):(\d+):(\d+)", line)
                    if stamp:
                        seconds = int(stamp.group(1)) * 3600 + int(stamp.group(2)) * 60 + int(stamp.group(3))
                        progress(min(1.0, seconds / float(duration)))

    thread = threading.Thread(target=pump, daemon=True)
    thread.start()
    deadline = time.time() + timeout if timeout else None
    try:
        while True:
            code = process.poll()
            if code is not None:
                break
            if cancel is not None and cancel.is_set():
                _kill_process(process)
                raise MediaError("已取消")
            if deadline is not None and time.time() > deadline:
                _kill_process(process)
                raise MediaError("外部命令执行超时（%s 秒）" % timeout)
            time.sleep(0.2)
    finally:
        _unregister(process)
    thread.join(timeout=2)
    if code != 0:
        raise MediaError("\n".join(tail[-6:]) or "外部命令执行失败（%s）" % code)
    return "\n".join(tail)


_COVER_ASSET = re.compile(r"/novel-pic/([0-9a-f]{32})", re.I)
# 同一张图的无签名 CDN：模板宽度要成 1600 时服务端直接回原图尺寸。
_COVER_HIRES_HOSTS = ("p6-novel.byteimg.com", "p3-novel.byteimg.com")


def hi_res_cover_urls(cover):
    """同一张海报的高清地址（按顺序试），拿不到就返回空列表。

    列表/详情用的是 400px 那版，而且 URL 里带 x-signature：改一个字符就 403。
    但同一张图的资源在无签名 CDN 上可以按模板取，实测能把 400x572 换成原图
    700x1000（像素 3 倍）。平时照旧用小图，只有"保存海报"才去要这一张。
    """
    match = _COVER_ASSET.search(cover or "")
    if not match:
        return []
    asset = match.group(1)
    return ["https://%s/novel-pic/%s~tplv-shrink:1600:0.image" % (host, asset)
            for host in _COVER_HIRES_HOSTS]


def image_kind(path):
    """看文件头判断图片格式：core 的封面缓存统一是 .img，不能信扩展名。"""
    if not path or not os.path.isfile(path):
        return ""
    with open(path, "rb") as handle:
        head = handle.read(16)
    if head[:3] == b"\xff\xd8\xff":
        return "jpg"
    if head[:8] == b"\x89PNG\r\n\x1a\n":
        return "png"
    if head[4:12] in (b"ftypheic", b"ftypheix", b"ftyphevc", b"ftypmif1"):
        return "heic"
    return ""


def export_cover(source, target, heic=False):
    """Write a fetched poster out as a normal image file.

    红果给的原图多是 HEIC（HEVC 编码），Windows 双击打不开，所以转码成 JPEG/PNG；
    源文件本来就是目标格式时直接复制，一张字节都不动。
    """
    extension = os.path.splitext(target)[1].lower()
    if extension == ".jpeg":
        extension = ".jpg"
    if extension not in (".jpg", ".png"):
        raise MediaError("海报只能存成 .jpg 或 .png")
    os.makedirs(os.path.dirname(os.path.abspath(target)), exist_ok=True)
    kind = image_kind(source) or ("heic" if heic else "")
    if kind == extension[1:]:
        shutil.copyfile(source, target)
    else:
        ffmpeg = tools.ffmpeg_path()
        if not ffmpeg:
            raise MediaError("缺少 ffmpeg 组件，无法转换海报格式")
        args = [ffmpeg, "-y", "-loglevel", "error", "-nostdin",
                "-i", source, "-frames:v", "1"]
        if extension == ".jpg":
            args += ["-q:v", "2"]         # 近无损，海报细节留得住
        args.append(target)
        _run(args, timeout=120)
    if not os.path.isfile(target) or os.path.getsize(target) == 0:
        raise MediaError("海报没有转换成功，请换个位置再试")
    return target


def _ffprobe_json(path):
    """跑一次 ffprobe，把它返回的 JSON 交出来（probe / media_signature 共用）。

    以前这里是两份一模一样的实现（probe_json 与 probe 各起一次子进程），
    合并成一处，行为不变。
    """
    binary = tools.ffprobe_path()
    if not binary:
        raise MediaError("缺少 ffprobe 组件")
    process = subprocess.Popen(
        [binary, "-v", "error", "-print_format", "json", "-show_format",
         "-show_streams", "-show_data_hash", "sha256", path],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0) if os.name == "nt" else 0)
    _register(process)
    try:
        try:
            out, err = process.communicate(timeout=120)
        except subprocess.TimeoutExpired:
            _kill_process(process)
            raise MediaError("解析媒体信息超时，文件可能已损坏")
    finally:
        _unregister(process)
    if process.returncode != 0:
        raise MediaError(err.decode("utf-8", "replace").strip() or "无法解析媒体信息")
    return json.loads(out.decode("utf-8", "replace") or "{}")


def _free_bytes(path):
    """Free space on the volume that will hold ``path`` (0 when unknown)."""
    probe = path
    while probe and not os.path.isdir(probe):
        parent = os.path.dirname(probe)
        if parent == probe:
            break
        probe = parent
    try:
        return shutil.disk_usage(probe).free
    except OSError:
        return 0


def ensure_free_space(path, needed, label="保存"):
    """Fail early with a readable message instead of a half-written file."""
    free = _free_bytes(path)
    if free and needed and free < needed:
        raise MediaError("%s需要约 %s，目标磁盘只剩 %s，请清理空间或换一个下载位置"
                         % (label, human_size(needed), human_size(free)))


def human_size(value):
    number = float(value or 0)
    for unit in ("B", "KB", "MB", "GB", "TB"):
        if number < 1024 or unit == "TB":
            return ("%.0f %s" % (number, unit)) if unit == "B" else ("%.1f %s" % (number, unit))
        number /= 1024.0
    return "%.1f TB" % number


def probe(path):
    return media_signature(path, _ffprobe_json(path))


def _video_signature(stream):
    """Decide whether two episodes share one video bitstream.

    Mirrors the reference pipeline's videoSignature: codec, frame size, pixel
    format, sample aspect ratio and colour description.  Any difference here
    means the track must be re-encoded before a stream-copy concat is safe.
    """
    if not stream:
        return ""
    return "|".join([
        str(stream.get("codec_name") or ""),
        str(stream.get("width") or 0),
        str(stream.get("height") or 0),
        str(stream.get("pix_fmt") or ""),
        str(stream.get("sample_aspect_ratio") or "1:1"),
        str(stream.get("color_transfer") or "unknown"),
        str(stream.get("color_primaries") or "unknown"),
    ])


def _audio_signature(stream):
    """Signature for audio tracks; ``none`` when the episode has no audio."""
    if not stream:
        return "none"
    return "|".join([
        str(stream.get("codec_name") or ""),
        str(stream.get("profile") or "unknown"),
        str(stream.get("sample_rate") or ""),
        str(stream.get("channels") or ""),
        str(stream.get("channel_layout") or ""),
        str(stream.get("extradata_hash") or "unknown"),
    ])


def media_signature(path, data=None):
    if data is None:
        data = _ffprobe_json(path)
    fmt = data.get("format", {})
    duration = 0.0
    try:
        duration = float(fmt.get("duration") or 0.0)
    except Exception:
        duration = 0.0
    if duration <= 0:
        for stream in data.get("streams", []):
            try:
                duration = max(duration, float(stream.get("duration") or 0.0))
            except Exception:
                pass
    video = next((s for s in data.get("streams", []) if s.get("codec_type") == "video"), None)
    audio = next((s for s in data.get("streams", []) if s.get("codec_type") == "audio"), None)
    return {
        "path": path,
        "duration": duration,
        "bytes": int(fmt.get("size") or os.path.getsize(path) if os.path.exists(path) else 0),
        "video": video,
        "audio": audio,
        "videoSignature": _video_signature(video),
        "audioSignature": _audio_signature(audio),
    }


def verify_duration(actual, expected, tolerance=1.5):
    if expected <= 0:
        return
    if abs(actual - expected) > max(tolerance, expected * 0.02):
        raise MediaError("合并后时长校验失败（期望 %.1fs，实际 %.1fs）" % (expected, actual))


def _clock(seconds):
    seconds = max(0, int(seconds))
    return "%02d:%02d:%02d" % (seconds // 3600, seconds % 3600 // 60, seconds % 60)


def _episode_at(episodes, position):
    """把成品里的绝对秒数映射回集数。episodes 是 [(集数, 时长), ...]."""
    total = 0.0
    for number, duration in episodes or ():
        total += duration
        if position <= total:
            return number
    return episodes[-1][0] if episodes else 0


def bitrate_text(bits_per_second):
    """码率写成人看的样子：1.46 Mbps / 720 kbps。"""
    value = float(bits_per_second or 0)
    if value <= 0:
        return ""
    if value >= 1000000:
        return "%.2f Mbps" % (value / 1000000.0)
    return "%.0f kbps" % (value / 1000.0)


def _definition_height(definition):
    digits = "".join(ch for ch in (definition or "") if ch.isdigit())
    return int(digits) if digits else 0


def quality_summary(items, chapters):
    """详情页头部那一行「这剧实际是什么画质」。

    ``items`` 是核心 sampleQuality 返回的 {视频ID: {...}}，``chapters`` 是送去采样的那几集。
    标签和真实分辨率不一致时（平台上有把 1280x720 标成 1080p 的片源）以真实值报，
    并把「标称」一并写出来，免得看着像 bug。
    """
    rows = []
    for chapter in chapters:
        video_id = (chapter.get("videoUrl") or "").replace("hongguo-cenc://", "")
        row = items.get(video_id)
        if row:
            rows.append(row)
    if not rows:
        return ""
    sampled = len(rows)
    playable = [row for row in rows if row.get("playable") and row.get("height")]
    if not playable:
        return "画质 ⚠ 抽样的 %d 集都没有播放器能解的档位" % sampled

    groups = {}
    for row in playable:
        key = (_definition_height(row.get("definition")), int(row.get("height") or 0),
               int(row.get("width") or 0))
        groups[key] = groups.get(key, 0) + 1
    (label, height, width), _count = max(groups.items(), key=lambda pair: pair[1])

    pieces = []
    if label and label > height:
        pieces.append("⚠ 标称 %dP、实际 %d×%d" % (label, width, height))
    else:
        pieces.append("%d×%d" % (width, height))
    rates = [int(row.get("bitrate") or 0) for row in playable if row.get("bitrate")]
    if rates:
        pieces.append("平均 %s" % bitrate_text(sum(rates) / len(rates)))
    if len(playable) < sampled:
        pieces.append("%d 集无可播档位" % (sampled - len(playable)))
    pieces.append("抽样 %d 集" % sampled)
    return "画质 " + " · ".join(pieces)


def _majority(items, key):
    """Most common signature; ties fall back to the longest total duration."""
    groups = {}
    order = []
    for item in items:
        signature = key(item)
        if signature not in groups:
            groups[signature] = []
            order.append(signature)
        groups[signature].append(item)

    def rank(signature):
        group = groups[signature]
        return (len(group), sum(float(item.get("duration") or 0.0) for item in group))

    best = order[0]
    for signature in order[1:]:
        if rank(signature) > rank(best):
            best = signature
    return groups[best][0]


def _video_bitrate(signature):
    """Average video bitrate of one probe, in bits per second.

    ffprobe reports a bit_rate for MP4 streams; when it is missing we fall
    back to the file size divided by its duration.
    """
    stream = signature.get("video") or {}
    try:
        rate = float(stream.get("bit_rate") or 0)
    except (TypeError, ValueError):
        rate = 0.0
    if rate > 0:
        return rate
    duration = float(signature.get("duration") or 0)
    if duration > 0:
        return float(signature.get("bytes") or 0) * 8.0 / duration
    return 0.0


def build_plan(signatures):
    """Pick the merge target the way the reference pipeline does.

    The most common signature across the season wins, so a couple of odd
    episodes are re-encoded to match the majority instead of dragging the whole
    season through the encoder.  Only episodes that actually differ are
    transcoded; the rest are stream-copied so they keep the source bitrate.
    """
    total = len(signatures)
    empty = {
        "video_change": [False] * total, "audio_change": [False] * total,
        "video_transcode": False, "audio_transcode": False,
        "canonical_aac": False, "transport": False, "ts": False,
        "video_codec": "", "width": 0, "height": 0, "pix_fmt": "yuv420p",
        "video_bitrate": 0,
        "aspect": "1:1", "has_audio": False, "audio_codec": "", "audio_profile": "",
        "sample_rate": "", "channels": 0, "channel_layout": "",
    }
    if not signatures:
        return empty

    video_base = _majority(signatures, lambda s: s["videoSignature"])
    video_stream = video_base.get("video") or {}
    codec = video_stream.get("codec_name") or ""

    with_audio = [s for s in signatures if s.get("audio")]
    audio_base = _majority(with_audio, lambda s: s["audioSignature"]) if with_audio else None
    audio_stream = (audio_base or {}).get("audio") or {}
    audio_codec = audio_stream.get("codec_name") or ""

    majority = [s for s in signatures
                if s["videoSignature"] == video_base["videoSignature"]]
    rates = [r for r in (_video_bitrate(s) for s in majority) if r > 0]
    video_bitrate = int(sum(rates) / len(rates)) if rates else 0

    video_change = [s["videoSignature"] != video_base["videoSignature"] for s in signatures]
    audio_change = [audio_base is not None and s["audioSignature"] != audio_base["audioSignature"]
                    for s in signatures]
    # 多数派的 extradata 已经一致，只把不同的那几集转成同一组 AAC 参数就够。
    # 以前只要有一集不同就把整季重编码，既慢，又让本来完全正常的集去踩
    # ffmpeg 原生 AAC 编码器的坏帧 bug。只有多数派本身不是 AAC-LC 时，才
    # 退回“全部统一成 LC”，因为别的 profile 转码后 extradata 不一定对得上。
    audio_profile = audio_stream.get("profile") or ""
    canonical_aac = audio_codec == "aac" and any(audio_change)
    if canonical_aac and audio_profile != "LC":
        audio_change = [True] * total

    transport = codec in ("h264", "hevc") and (
        audio_base is None or audio_codec in ("aac", "ac3", "eac3", "mp2"))

    try:
        channels = int(audio_stream.get("channels") or 0)
    except (TypeError, ValueError):
        channels = 0

    return {
        "video_change": video_change,
        "audio_change": audio_change,
        "video_transcode": any(video_change),
        "audio_transcode": any(audio_change),
        "canonical_aac": canonical_aac,
        "transport": transport,
        "ts": transport,
        "video_codec": codec,
        "width": int(video_stream.get("width") or 0),
        "height": int(video_stream.get("height") or 0),
        "pix_fmt": video_stream.get("pix_fmt") or "yuv420p",
        "video_bitrate": video_bitrate,
        "aspect": str(video_stream.get("sample_aspect_ratio") or "1:1"),
        "has_audio": audio_base is not None,
        "audio_codec": audio_codec,
        "audio_profile": audio_profile,
        "sample_rate": str(audio_stream.get("sample_rate") or ""),
        "channels": channels,
        "channel_layout": audio_stream.get("channel_layout") or "",
    }


def _part_audio_ok(ffmpeg, path):
    """Strictly decode one normalized part's audio track.

    ffmpeg's native AAC encoder occasionally emits a bad frame for a
    particular episode (decoder: "error in spectral data, ESC overflow" or
    "Number of bands exceeds limit").  The full-length check would then fail
    after the whole merge, so each part is checked right after it is produced.
    """
    creation = getattr(subprocess, "CREATE_NO_WINDOW", 0) if os.name == "nt" else 0
    try:
        result = subprocess.run(
            [ffmpeg, "-v", "error", "-xerror", "-err_detect", "explode", "-i", path,
             "-map", "0:a:0?", "-f", "null", "-"],
            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
            timeout=300, creationflags=creation)
    except (OSError, subprocess.TimeoutExpired):
        return False
    return result.returncode == 0


def _normalize_args(source, target, plan, index, has_source_audio, duration, audio_bitrate="192k"):
    """ffmpeg arguments that bring one episode in line with the merge target.

    Mirrors the reference pipeline's normalizeArguments: transcode only what
    differs (source codec preserved, bitrate matched to the majority group),
    copy the rest, and remux through MPEG-TS when the majority is H.264/H.265
    with a TS-friendly audio codec.
    """
    args = ["-i", source]
    add_silence = plan["has_audio"] and not has_source_audio
    if add_silence:
        rate = plan["sample_rate"] or "44100"
        channels = plan["channels"] or 2
        if channels == 1:
            layout = "mono"
        elif channels == 2:
            layout = "stereo"
        else:
            layout = plan["channel_layout"] or "stereo"
        args += ["-f", "lavfi", "-i", "anullsrc=r=%s:cl=%s" % (rate, layout)]
    args += ["-map", "0:v:0"]
    if plan["has_audio"]:
        args += ["-map", "1:a:0" if add_silence else "0:a:0"]
    if plan["video_change"][index]:
        width = plan["width"] or 1080
        height = plan["height"] or 1920
        pixel = plan["pix_fmt"] or "yuv420p"
        aspect = (plan["aspect"] or "1:1").replace(":", "/")
        encoder = "libx265" if plan["video_codec"] == "hevc" else "libx264"
        args += ["-c:v", encoder, "-preset", "veryfast", "-threads", "2"]
        target_rate = int(plan.get("video_bitrate") or 0)
        if target_rate > 0:
            # Follow the bitstream we are replacing instead of a fixed CRF.
            # These sources sit near 1 Mbit/s, so CRF 18 would balloon the
            # merged file to several times the size of the original episodes.
            args += ["-b:v", str(target_rate),
                     "-maxrate", str(int(target_rate * 1.4)),
                     "-bufsize", str(int(target_rate * 2.8))]
        else:
            args += ["-crf", "18"]
        args += [
            "-vf", ("scale=%d:%d:force_original_aspect_ratio=decrease,"
                    "pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=%s,format=%s"
                    % (width, height, width, height, aspect, pixel)),
        ]
        if plan["video_codec"] == "hevc":
            args += ["-x265-params", "pools=2:frame-threads=1"]
    else:
        args += ["-c:v", "copy"]
    if plan["has_audio"]:
        if plan["audio_change"][index]:
            args += ["-c:a", plan["audio_codec"] or "aac",
                     "-ar", plan["sample_rate"] or "44100",
                     "-ac", str(plan["channels"] or 2)]
            if plan["canonical_aac"]:
                args += ["-profile:a", "aac_low"]
            if plan["audio_codec"] in ("aac", "ac3", "eac3", "mp2"):
                args += ["-b:a", audio_bitrate]
            if plan["audio_codec"] == "aac":
                # 默认 twoloop 编码器在个别源上会编出坏帧，严格解码校验时报
                # “error in spectral data, ESC overflow”，整部剧因此合并失败。
                # fast 编码器在同样参数下输出稳定，声音仍是 192k 立体声。
                args += ["-aac_coder", "fast"]
        else:
            args += ["-c:a", "copy"]
    if add_silence:
        args += ["-t", "%.6f" % float(duration or 0.0)]
    args += ["-map_metadata", "-1", "-avoid_negative_ts", "make_zero"]
    if plan["transport"]:
        args += [
            "-bsf:v", "h264_mp4toannexb" if plan["video_codec"] == "h264" else "hevc_mp4toannexb",
            "-mpegts_flags", "+resend_headers",
            "-f", "mpegts",
        ]
    args += [target]
    return args


class MergeTask:
    """Concatenates a continuous episode range into one full-length file."""

    def __init__(self, core, jobs, on_progress=None, on_status=None, output_name=None):
        self.core = core
        self.jobs = sorted(jobs, key=lambda job: job.get("index", 0))
        self.on_progress = on_progress or (lambda ratio, text: None)
        self.on_status = on_status or (lambda text: None)
        self.output_name = output_name  # e.g. "一品布衣 S01E01.mkv"; None keeps default
        self.cancelled = threading.Event()
        # 校验失败但成品没能挪走时，保留 workdir 供用户手动取回。
        self._keep_workdir = False

    def cancel(self):
        self.cancelled.set()

    def _check(self):
        if self.cancelled.is_set():
            raise MediaError("合并已取消")

    def _local_file(self, job):
        """(path, decryptionKey) for a finished download, or (None, None).

        The decrypted library copy under 下载/<剧名>/ is preferred, because it
        needs no key and can be stream-copied straight into the merge.  The
        core's encrypted cache file is the fallback, paired with its key.
        """
        key = None
        plan = self.core.local_plan(job["drama"]["id"], job["index"])
        if plan and plan.get("local"):
            key = plan.get("decryptionKey") or None
        candidates = []
        clear = self._library_file(job)
        if clear:
            candidates.append(clear)
        if plan and plan.get("local") and plan.get("url"):
            candidates.append(plan["url"])
        folder = job.get("folder")
        if folder:
            candidates.append(os.path.join(folder, job.get("file") or "media.mp4"))
        job_id = job.get("id")
        if job_id:
            candidates.append(os.path.join(self.core.download_directory(), job_id,
                                           job.get("file") or "media.mp4"))
        if not key:
            key = job.get("key") or None
        for path in candidates:
            if path and os.path.isfile(path):
                # A clear library file never needs the key.
                return path, (None if path == clear else key)
        return None, key

    def _library_file(self, job):
        """Path of the decrypted, renamed episode published by library.py."""
        try:
            from . import library
        except Exception:
            return None
        try:
            path = library.EpisodePublisher(self.core).path_for(job)
        except Exception:
            return None
        return path if path and os.path.isfile(path) else None

    def _sources(self):
        sources = []
        for job in self.jobs:
            self._check()
            path, key = self._local_file(job)
            if not path:
                raise MediaError("第 %d 集没有完整的本地文件" % job["index"])
            sources.append((job, path, key))
        return sources

    def _normalize_all(self, ffmpeg, sources, signatures, plan, workdir):
        """Bring every episode to the merge target, several at a time.

        Episodes are independent remuxes, so they run on a small pool; the
        reported progress is episodes finished, which also drives the bar.
        """
        from concurrent.futures import ThreadPoolExecutor
        suffix = "ts" if plan["transport"] else "mkv"
        workers = max(2, min(6, (os.cpu_count() or 4) - 1))
        total = len(sources)
        finished = [0]
        lock = threading.Lock()

        def normalize(index):
            job, path, _key = sources[index]
            signature = signatures[index]
            self._check()
            number = job["index"]
            target = os.path.join(workdir, "part-%d.%s" % (index, suffix))
            exists = os.path.isfile(target) and os.path.getsize(target) > 0
            if exists and (not plan["has_audio"] or _part_audio_ok(ffmpeg, target)):
                pass
            else:
                if exists:
                    os.remove(target)
                for bitrate in ("192k", "128k"):
                    args = [ffmpeg, "-y", "-loglevel", "error"]
                    args += _normalize_args(path, target, plan, index,
                                            bool(signature.get("audio")),
                                            signature["duration"], audio_bitrate=bitrate)
                    _run(args, timeout=3600, cancel=self.cancelled)
                    if not plan["has_audio"] or _part_audio_ok(ffmpeg, target):
                        break
                    os.remove(target)
                else:
                    raise MediaError("第 %d 集音频转码校验失败（已尝试 192k/128k），请重试" % number)
            with lock:
                finished[0] += 1
                done = finished[0]
            self.on_progress(0.15 + 0.30 * done / total,
                             "统一分集格式 %d/%d（第 %d 集）" % (done, total, number))
            return target

        with ThreadPoolExecutor(max_workers=workers) as pool:
            futures = [pool.submit(normalize, index) for index in range(total)]
            try:
                return [future.result() for future in futures]
            except BaseException:
                for future in futures:
                    future.cancel()
                raise

    def _verify_merged(self, ffmpeg, staged, total, on_ratio, episodes=None):
        """Full-decode the merged file across several cores.

        The timeline is sliced into windows and every window is decoded by
        its own strict ffmpeg (-xerror -err_detect explode).  Accurate seek
        keeps each frame inside exactly one window, so the guarantee matches
        the old single-process pass; the last window has no end bound, so
        content running past the expected total is still fully decoded.

        ``episodes`` is [(集数, 时长), ...] in merge order and is only used to
        turn a failing decoder position into "about episode N" in the error.
        """
        cores = os.cpu_count() or 4
        workers = max(2, min(8, cores - 1))
        if total <= 0 or total / workers < 20.0:
            workers = 1
        span = total / float(workers)
        creation = getattr(subprocess, "CREATE_NO_WINDOW", 0) if os.name == "nt" else 0
        jobs = []
        for index in range(workers):
            start = index * span
            # -progress 输出机器可读的 out_time_us，避开日志行被缓冲导致的
            # “进度十分钟不动”误判（ffmpeg 的 time= 行走 stderr，重定向后会
            # 成块到达）。错误仍然走 stderr，和进度合并在同一个管道里。
            args = [ffmpeg, "-xerror", "-err_detect", "explode", "-nostdin",
                    "-loglevel", "error", "-stats_period", "1", "-progress", "pipe:1"]
            if index:
                args += ["-ss", "%.3f" % start]
            args += ["-threads", str(max(1, cores // workers)), "-i", staged]
            if index < workers - 1:
                args += ["-t", "%.6f" % span]
            args += ["-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn", "-f", "null", os.devnull]
            jobs.append({"args": args, "span": span, "pos": 0.0,
                         "index": index, "start": start,
                         "process": _spawn_tracked(args, stdout=subprocess.PIPE,
                                                   stderr=subprocess.STDOUT,
                                                   creationflags=creation),
                         "tail": []})

        lock = threading.Lock()
        # covered 用于进度（按窗口夹紧），raw 用于停滞判断（允许最后一段
        # 越过预期片长继续解码，不会因此被误判为卡死）。
        progress = {"covered": 0.0, "raw": 0.0}

        def pump(job):
            for raw in job["process"].stdout:
                line = raw.decode("utf-8", "replace").strip()
                if not line:
                    continue
                with lock:
                    job["tail"].append(line)
                    del job["tail"][:-12]
                stamp = re.search(r"out_time_us=(\d+)", line)
                seconds = None
                if stamp:
                    seconds = int(stamp.group(1)) / 1_000_000.0
                else:
                    stamp = re.search(r"time=(\d+):(\d+):(\d+)", line)
                    if stamp:
                        seconds = (int(stamp.group(1)) * 3600 + int(stamp.group(2)) * 60
                                   + int(stamp.group(3)))
                if seconds is not None:
                    with lock:
                        job["pos"] = seconds
                        covered = sum(min(j["pos"], j["span"]) for j in jobs)
                        progress["covered"] = covered
                        progress["raw"] = sum(j["pos"] for j in jobs)
                        on_ratio(min(0.99, covered / total))

        threads = [threading.Thread(target=pump, args=(job,), daemon=True) for job in jobs]
        for thread in threads:
            thread.start()
        started = time.time()
        limit = _verify_deadline(total)
        deadline = started + limit
        best = 0.0
        last_progress = started
        last_log = started
        tools.log_text("合并校验开始：片长 %.2f 小时，%d 个解码窗口，上限 %.1f 小时"
                       % (total / 3600.0, workers, limit / 3600.0))
        try:
            while any(job["process"].poll() is None for job in jobs):
                self._check()
                now = time.time()
                with lock:
                    covered = progress["covered"]
                    raw = progress["raw"]
                # 只要还有解码输出在增长就不算卡死；10 分钟完全不动才失败。
                if raw > best + max(0.5, total * 0.0005):
                    best = raw
                    last_progress = now
                if now - last_progress > VERIFY_STALL_SECONDS:
                    _log_verify_tails(jobs, total, "停滞")
                    raise MediaError("成品解码校验停滞（%.0f 秒没有进展）"
                                     % (now - last_progress))
                if now > deadline:
                    _log_verify_tails(jobs, total, "超时")
                    raise MediaError("成品解码校验超时（片长 %.1f 小时，上限 %.1f 小时）"
                                     % (total / 3600.0, limit / 3600.0))
                if now - last_log >= VERIFY_LOG_SECONDS:
                    elapsed = max(0.001, now - started)
                    tools.log_text("合并校验进度 %.1f%% · %.1f× 实时 · 已用 %.0f 分钟"
                                   % (min(99.0, covered / total * 100.0),
                                      best / elapsed, elapsed / 60.0))
                    last_log = now
                time.sleep(0.2)
        except BaseException:
            # Cancel/failure must also stop the decoders; otherwise they keep
            # reading the staged file while the cleanup below deletes it.
            for job in jobs:
                _kill_process(job["process"], wait=1.0)
            raise
        finally:
            for job in jobs:
                _unregister(job["process"])
        for thread in threads:
            thread.join(timeout=2)
        failed = [job for job in jobs if job["process"].returncode != 0]
        if failed:
            job = failed[0]
            position = job["start"] + job["pos"]
            where = "第 %d 个解码窗口" % (job["index"] + 1)
            if episodes:
                where += "，约第 %d 集（成品时间 %s）" % (_episode_at(episodes, position),
                                                          _clock(position))
            tail = "\n".join(job["tail"][-12:]) or "解码器返回错误"
            tools.log_text("合并解码校验失败：%s，窗口起点 %.3fs，最后时间 %.3fs\n%s"
                           % (where, job["start"], position,
                              "\n".join(job["tail"])))
            raise MediaError("成品解码校验失败（%s）：\n%s" % (where, tail))
        elapsed = max(0.001, time.time() - started)
        tools.log_text("合并校验完成：用时 %.1f 分钟，平均 %.1f× 实时"
                       % (elapsed / 60.0, total / elapsed))
        on_ratio(1.0)


    def _verify_streams(self, plan, checked):
        """Assert that the merged container still describes the planned streams.

        Full decode only proves the packets are decodable; it would happily
        accept a video-only file because ``-map 0:a:0?`` is optional.  These
        checks make a wrong stream mapping or a lost audio track fail loudly.
        """
        video = checked.get("video") or {}
        if not video:
            raise MediaError("成品缺少视频轨")
        if plan["video_codec"] and video.get("codec_name") != plan["video_codec"]:
            raise MediaError("成品视频编码异常：期望 %s，实际 %s"
                             % (plan["video_codec"], video.get("codec_name") or "未知"))
        expected_w, expected_h = int(plan["width"] or 0), int(plan["height"] or 0)
        actual_w, actual_h = int(video.get("width") or 0), int(video.get("height") or 0)
        if expected_w and expected_h and (actual_w, actual_h) != (expected_w, expected_h):
            raise MediaError("成品视频分辨率异常：期望 %dx%d，实际 %dx%d"
                             % (expected_w, expected_h, actual_w, actual_h))
        if plan["pix_fmt"] and video.get("pix_fmt") and video.get("pix_fmt") != plan["pix_fmt"]:
            raise MediaError("成品像素格式异常：期望 %s，实际 %s"
                             % (plan["pix_fmt"], video.get("pix_fmt")))
        if not plan["has_audio"]:
            return
        audio = checked.get("audio") or {}
        if not audio:
            raise MediaError("成品缺少音轨（源剧集有音频）")
        if plan["audio_codec"] and audio.get("codec_name") != plan["audio_codec"]:
            raise MediaError("成品音频编码异常：期望 %s，实际 %s"
                             % (plan["audio_codec"], audio.get("codec_name") or "未知"))
        if plan["sample_rate"] and str(audio.get("sample_rate") or "") != plan["sample_rate"]:
            raise MediaError("成品音频采样率异常：期望 %s，实际 %s"
                             % (plan["sample_rate"], audio.get("sample_rate") or "未知"))
        if plan["channels"] and int(audio.get("channels") or 0) != plan["channels"]:
            raise MediaError("成品音频声道数异常：期望 %d，实际 %d"
                             % (plan["channels"], int(audio.get("channels") or 0)))
        if plan["channel_layout"] and audio.get("channel_layout") and \
                audio.get("channel_layout") != plan["channel_layout"]:
            raise MediaError("成品音频声道布局异常：期望 %s，实际 %s"
                             % (plan["channel_layout"], audio.get("channel_layout")))

    def _preserve_unverified(self, staged, target):
        """校验失败时把成品挪到成品目录并标注，返回 (路径, 是否已挪走)。"""
        root, name = os.path.split(target)
        base, ext = os.path.splitext(name)
        candidate = os.path.join(root, base + "（未完整校验）" + ext)
        try:
            os.makedirs(root, exist_ok=True)
            if os.path.exists(candidate):
                os.remove(candidate)
            shutil.move(staged, candidate)
            return candidate, True
        except OSError:
            return staged, False

    def run(self):
        ffmpeg = tools.ffmpeg_path()
        if not ffmpeg:
            raise MediaError("缺少 ffmpeg 组件")
        drama = self.jobs[0]["drama"]
        title = (drama.get("title") or drama.get("id") or "短剧").strip()
        safe = re.sub(r'[\\/:*?"<>|]', "_", title)[:60]
        self.on_status("校验分集连续性")
        ordered = [job["index"] for job in self.jobs]
        if ordered != list(range(ordered[0], ordered[0] + len(ordered))):
            missing = [n for n in range(ordered[0], ordered[-1] + 1) if n not in set(ordered)]
            raise MediaError("分集不连续，缺少第 %s 集，请补齐后合并" % "、".join(str(n) for n in missing[:12]))

        sources = self._sources()
        # Per-merge subfolder keyed by drama + range: resuming this exact
        # merge reuses its parts, but a different drama/range never does
        # (parts are index-named and would silently carry wrong content).
        # Only folders left behind by an older run are cleared; a fresh one
        # may belong to a merge still running in another window.
        digest = hashlib.md5(("%s|%d-%d" % (drama.get("id") or title,
                                            ordered[0], ordered[-1])).encode("utf-8")).hexdigest()[:12]
        parent = tools.merge_work_dir()
        workdir = os.path.join(parent, digest)
        os.makedirs(parent, exist_ok=True)
        now = time.time()
        for entry in os.listdir(parent):
            if entry == digest:
                continue
            stale = os.path.join(parent, entry)
            try:
                if now - os.path.getmtime(stale) < 7200:
                    continue        # possibly an in-flight merge, leave it alone
                if os.path.isdir(stale):
                    shutil.rmtree(stale, ignore_errors=True)
                else:
                    os.remove(stale)
            except OSError:
                pass  # a locked leftover must not kill this merge
        os.makedirs(workdir, exist_ok=True)
        # Parts + staged output need roughly twice the source size; catch a
        # full disk before ffmpeg has written anything.
        source_bytes = sum(os.path.getsize(path) for _job, path, _key in sources)
        ensure_free_space(parent, source_bytes * 2 + (512 << 20), "合并")
        self.on_status("解密分集")
        decoded = []
        for offset, (job, path, key) in enumerate(sources):
            self._check()
            number = job["index"]
            if key:
                target = os.path.join(workdir, "dec-%d.mp4" % offset)
                if not os.path.isfile(target) or os.path.getsize(target) == 0:
                    _run([ffmpeg, "-y", "-loglevel", "error", "-decryption_key", key,
                          "-i", path, "-map", "0:v:0", "-map", "0:a:0?",
                          "-c", "copy", "-avoid_negative_ts", "make_zero", target],
                         timeout=3600, cancel=self.cancelled)
                decoded.append((job, target, key))
            else:
                decoded.append((job, path, None))
            self.on_progress(0.08 * (offset + 1) / len(sources),
                             "解密第 %d 集" % number)
        sources = decoded

        self.on_status("解析媒体信息")
        signatures = []
        for offset, (_, path, _) in enumerate(sources):
            self._check()
            signatures.append(probe(path))
            self.on_progress(0.08 + 0.07 * (offset + 1) / len(sources),
                             "解析媒体信息 %d/%d" % (offset + 1, len(sources)))
        plan = build_plan(signatures)

        parts = []
        try:
            # Every episode goes through the same normalise step: differing
            # episodes are transcoded to the majority format, the rest are
            # stream-copied and only remuxed into a uniform container.  This
            # is what keeps the merged file the same size as the sources.
            # Episodes are independent, so several run at once.
            parts = self._normalize_all(ffmpeg, sources, signatures, plan, workdir)

            self.on_status("合并视频")
            listing = os.path.join(workdir, "concat.txt")
            with open(listing, "w", encoding="utf-8") as handle:
                handle.write("ffconcat version 1.0\n")
                for part in parts:
                    handle.write("file '%s'\n" % part.replace("'", "'\\''"))
            total = sum(s["duration"] for s in signatures) or 60
            staged = os.path.join(workdir, "full.mkv")
            _run([ffmpeg, "-y", "-loglevel", "error", "-f", "concat", "-safe", "0", "-i", listing,
                  "-map", "0:v:0", "-map", "0:a:0?", "-c", "copy", "-avoid_negative_ts", "make_zero", staged],
                 progress=lambda ratio: self.on_progress(0.45 + 0.10 * ratio, "合并视频"),
                 duration=total, timeout=7200, cancel=self.cancelled)

            # 目标路径提前算好：校验失败时也要把成品挪到这里，而不是删掉。
            target_dir = tools.merge_dir()
            os.makedirs(target_dir, exist_ok=True)
            if self.output_name:
                # Only ever a plain file name: a title must not be able to
                # steer the output outside 下载/合并/.
                name = os.path.basename(str(self.output_name).replace("\\", "/")).strip(" .")
                if not name:
                    raise MediaError("成品文件名无效")
                target = os.path.join(target_dir, name)
            else:
                target = os.path.join(target_dir, "%s 全%03d-%03d集.mkv" % (safe, ordered[0], ordered[-1]))

            if tools.merge_verify_enabled():
                self.on_status("完整解码校验成品")
                self.on_progress(0.55, "完整解码校验成品")
                episodes = [(job["index"], signature["duration"])
                            for job, signature in zip(self.jobs, signatures)]
                try:
                    self._verify_merged(ffmpeg, staged, total,
                                        lambda ratio: self.on_progress(0.55 + 0.40 * ratio,
                                                                       "完整解码校验成品"),
                                        episodes)
                except MediaError as exc:
                    if self.cancelled.is_set():
                        raise
                    # 校验失败/超时不再丢弃几小时的合并成果：挪到成品目录并标注，
                    # 用户可以直接播放确认。
                    preserved, moved = self._preserve_unverified(staged, target)
                    if not moved:
                        self._keep_workdir = True
                    raise MediaError("成品已保留（未完整校验）：%s\n原因：%s" % (preserved, exc))
            else:
                self.on_status("已跳过完整解码校验")
                self.on_progress(0.95, "已跳过完整解码校验")
                tools.log_text("合并跳过完整解码校验（用户关闭了校验开关）")

            checked = probe(staged)
            self._verify_streams(plan, checked)
            # 流复制合并的时长漂移通常只有几秒；2% 对 3 小时的成品等于 228 秒，
            # 少掉一整集都可能通过，所以按 0.2% 收紧并封顶 30 秒。
            verify_duration(checked["duration"], total,
                            tolerance=max(5.0, min(30.0, total * 0.002)))

            self.on_status("保存成品")
            self.on_progress(0.97, "保存成品")
            # 先移动到目标目录的临时名，再原子替换。旧成品在新成品就位前
            # 不会被删掉；覆盖失败（例如播放器占用）时旧文件仍然可用。
            temporary = target + ".part"
            if os.path.exists(temporary):
                os.remove(temporary)
            shutil.move(staged, temporary)
            try:
                os.replace(temporary, target)
            except OSError as exc:
                raise MediaError("保存成品失败，旧成品已保留；新成品留在 %s（%s）"
                                 % (temporary, exc))
            self.on_progress(1.0, "合并完成")
            return {"path": target, "duration": checked["duration"],
                    "bytes": os.path.getsize(target), "title": title,
                    "episodes": ordered, "transcoded": bool(plan["video_transcode"] or plan["audio_transcode"])}
        finally:
            if not self._keep_workdir:
                shutil.rmtree(workdir, ignore_errors=True)
