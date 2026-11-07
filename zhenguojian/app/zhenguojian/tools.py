"""Locates the bundled mpv / ffmpeg binaries and the app data directory."""
from __future__ import annotations

import json
import os
import shutil
import sys
import tempfile
import threading
import time

_log_lock = threading.Lock()


def log_dir():
    """Folder for diagnostic logs; empty string when it cannot be created."""
    path = os.path.join(data_dir(), "logs")
    try:
        os.makedirs(path, exist_ok=True)
    except OSError:
        return ""
    return path


def log_text(text):
    """Append one timestamped line to app.log.

    The GUI build has no console, so tracebacks printed to stderr used to
    vanish.  Rotate at 2 MB to keep the file bounded.
    """
    directory = log_dir()
    if not directory:
        return
    path = os.path.join(directory, "app.log")
    try:
        with _log_lock:
            if os.path.isfile(path) and os.path.getsize(path) > 2 * 1024 * 1024:
                try:
                    os.replace(path, path + ".1")
                except OSError:
                    pass
            with open(path, "a", encoding="utf-8") as handle:
                handle.write("[%s] %s\n" % (time.strftime("%Y-%m-%d %H:%M:%S"), str(text).rstrip()))
    except OSError:
        pass


def log_exception(prefix=""):
    """Log the exception currently being handled, with its traceback."""
    import traceback
    body = traceback.format_exc()
    log_text(("%s\n%s" % (prefix, body)) if prefix else body)


def backup_broken(path):
    """Move an unparsable file aside so the next launch starts clean."""
    if not path or not os.path.isfile(path):
        return ""
    target = "%s.corrupt-%s" % (path, time.strftime("%Y%m%d-%H%M%S"))
    try:
        os.replace(path, target)
    except OSError:
        return ""
    log_text("已备份损坏的文件：%s" % target)
    return target


def app_root():
    """Directory that contains mpv/, ffmpeg/ and sidecar.exe at runtime."""
    if getattr(sys, "frozen", False):
        return os.path.dirname(sys.executable)
    return os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def _first_existing(candidates):
    for candidate in candidates:
        if candidate and os.path.isfile(candidate):
            return candidate
    return None


def _tool(subdir, name, extra=()):
    root = app_root()
    local = os.path.join(root, "runtime", subdir, name)
    if os.name == "nt" and not local.lower().endswith(".exe"):
        local += ".exe"
    found = _first_existing([local, os.path.join(root, subdir, name)] + list(extra))
    return found


def mpv_path():
    local = _tool("mpv", "mpv")
    if local:
        return local
    cached = os.path.join(app_root(), "runtime", "mpv", "mpv.exe")
    if os.path.isfile(cached):
        return cached
    env = os.environ.get("ZHENGUO_MPV")
    if env and os.path.isfile(env):
        return env
    return shutil.which("mpv")


def ffmpeg_path():
    # D:\bin 只是开发机兜底，打包后必须用 runtime/ffmpeg 里带的那份。
    extra = () if getattr(sys, "frozen", False) else ("D:\\bin\\ffmpeg.exe",)
    return _tool("ffmpeg", "ffmpeg", extra=extra) or shutil.which("ffmpeg")


def ffprobe_path():
    extra = () if getattr(sys, "frozen", False) else ("D:\\bin\\ffprobe.exe",)
    return _tool("ffmpeg", "ffprobe", extra=extra) or shutil.which("ffprobe")


def project_dir():
    """Root that ships with the app; used as the default download location."""
    if getattr(sys, "frozen", False):
        return os.path.dirname(sys.executable)
    return os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def data_dir():
    base = os.environ.get("ZHENGUO_DATA")
    if base:
        return os.path.abspath(base)
    if sys.platform == "win32":
        root = os.environ.get("LOCALAPPDATA") or os.path.expanduser("~")
        return os.path.join(root, "ZhenguoJian")
    return os.path.join(os.path.expanduser("~"), ".zhenguojian")


# ------------------------------------------------------------------ library
# The user-facing library (成品库) can be pointed at another disk.  Only this
# process writes the setting, so the resolved root is cached: download_dir()
# is called on every index lookup and must not stat a file each time.
_library_root = None


def _settings_file():
    return os.path.join(data_dir(), "app-settings.json")


def _read_settings():
    try:
        with open(_settings_file(), "r", encoding="utf-8") as handle:
            data = json.load(handle)
    except (json.JSONDecodeError, UnicodeDecodeError):
        backup_broken(_settings_file())
        return {}
    except OSError:
        return {}
    return data if isinstance(data, dict) else {}


def library_root():
    """Folder holding the 下载 library; the app folder until one is chosen.

    Everything the user sees lives one level below it, so the default stays
    exactly the layout the packaged app has always had: <exe>\\下载\\<剧名>\\.
    """
    global _library_root
    if _library_root is None:
        stored = _read_settings().get("libraryRoot")
        if isinstance(stored, str) and stored.strip():
            _library_root = os.path.abspath(stored)
        else:
            _library_root = project_dir()
    return _library_root


def library_roots():
    """当前成品库根，加上之前用过、还没被清掉的旧根（最近的在前面）。

    换下载位置时文件按设计不搬走，所以旧位置的成品必须继续列得出来：
    只扫当前根的话，用户在 D 盘合并好的剧一换到 C 盘就从界面上消失了。
    """
    roots = [library_root()]
    stored = _read_settings().get("libraryRoots")
    if isinstance(stored, list):
        for root in stored:
            if isinstance(root, str) and root.strip():
                candidate = os.path.abspath(root)
                if candidate not in roots:
                    roots.append(candidate)
    return roots


def download_dirs():
    """所有要枚举的「下载」目录，当前根在前。

    只用于“列出来看看”；写入（解密落盘、合并输出）永远走 download_dir()，
    否则新下的东西会散到旧盘上。
    """
    return [os.path.join(root, "下载") for root in library_roots()]


def merge_dirs():
    """所有「下载/合并」目录，当前根在前。"""
    return [os.path.join(folder, "合并") for folder in download_dirs()]


def set_library_root(path):
    """Persist a new library location; empty/None restores the app folder."""
    global _library_root
    previous = library_root()
    stored = os.path.abspath(path) if path and str(path).strip() else ""
    settings = _read_settings()
    if stored:
        settings["libraryRoot"] = stored
    else:
        settings.pop("libraryRoot", None)
    # 旧根记一笔：里面的成品还在原地，界面上要能继续列出来
    keep = [stored or project_dir()]
    for root in [previous] + list(settings.get("libraryRoots") or []):
        if isinstance(root, str) and root.strip():
            candidate = os.path.abspath(root)
            if candidate not in keep:
                keep.append(candidate)
    settings["libraryRoots"] = keep[:8]
    _save_settings(settings)
    _library_root = stored or None
    return library_root()


def app_icon():
    """App icon (window/taskbar). Ships inside the package, so it works frozen."""
    return os.path.join(os.path.dirname(os.path.abspath(__file__)), "app.ico")


# 可选主题名必须和 ui.PALETTES 的键一致；这里不 import ui，免得把 PyQt5 拉进来。
THEMES = ("dark", "light")


def theme():
    """用户选的主题；没存过、存坏了都回落到深色。"""
    stored = _read_settings().get("theme")
    return stored if stored in THEMES else THEMES[0]


def set_theme(name):
    """把主题选择写进 app-settings.json。

    真正换配色的是 ui.apply_theme()——这里只负责记住选择，下次启动照它恢复。
    """
    settings = _read_settings()
    if name in THEMES:
        settings["theme"] = name
    else:
        settings.pop("theme", None)
    _save_settings(settings)
    return theme()


def _save_settings(settings):
    os.makedirs(data_dir(), exist_ok=True)
    handle = tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", delete=False,
                                         dir=data_dir(), prefix=".settings-", suffix=".tmp")
    temporary = handle.name
    try:
        with handle:
            json.dump(settings, handle, ensure_ascii=False, indent=1)
        os.replace(temporary, _settings_file())
    except BaseException:
        try:
            os.remove(temporary)
        except OSError:
            pass
        raise


def danmaku_enabled():
    """在线播放的弹幕开关；没设置过就默认开。"""
    return _read_settings().get("danmaku") is not False


def hwdec_enabled():
    """在线播放是否用显卡解码；默认关闭，个别显卡驱动会花屏。"""
    return _read_settings().get("hwdec") is True


def set_hwdec_enabled(on):
    settings = _read_settings()
    settings["hwdec"] = bool(on)
    _save_settings(settings)
    return hwdec_enabled()


def merge_verify_enabled():
    """合并后是否完整解码校验成品；默认开，长剧可在界面关掉。"""
    return _read_settings().get("mergeVerify") is not False


def set_merge_verify_enabled(on):
    settings = _read_settings()
    settings["mergeVerify"] = bool(on)
    _save_settings(settings)
    return merge_verify_enabled()


def set_danmaku_enabled(on):
    """记住弹幕开关，换集、重开程序都沿用。"""
    settings = _read_settings()
    settings["danmaku"] = bool(on)
    _save_settings(settings)
    return danmaku_enabled()


def danmaku_download_enabled():
    """下载完成时是否把整集弹幕存到成品旁边；默认关闭，用户勾选才下。"""
    return _read_settings().get("danmakuDownload") is True


def set_danmaku_download_enabled(on):
    settings = _read_settings()
    settings["danmakuDownload"] = bool(on)
    _save_settings(settings)
    return danmaku_download_enabled()


def danmaku_opacity():
    """弹幕不透明度百分比（20-100）；没设置过就 100。"""
    try:
        value = int(_read_settings().get("danmakuOpacity") or 100)
    except (TypeError, ValueError):
        value = 100
    return min(100, max(20, value))


def set_danmaku_opacity(value):
    """记住弹幕透明度；下次开播的弹幕直接按这个值生成。"""
    try:
        value = int(value)
    except (TypeError, ValueError):
        value = 100
    settings = _read_settings()
    settings["danmakuOpacity"] = min(100, max(20, value))
    _save_settings(settings)
    return danmaku_opacity()


def core_cache_dir():
    """Root the native core writes encrypted originals into.

    The core owns pause/resume and validates its own files, so the encrypted
    ``media.mp4`` must stay where it put it.  It lives inside the app folder
    (never on C:) and is hidden from the user-facing library below.
    """
    return os.path.join(project_dir(), ".cache", "downloads")


def download_dir():
    """User-facing library: one folder per drama with decrypted episodes."""
    return os.path.join(library_root(), "下载")


def merge_dir():
    """Merged full-length episodes, beside the per-drama folders."""
    return os.path.join(download_dir(), "合并")


def merge_work_dir():
    """Scratch space for the merge pipeline (intermediate TS/MKV parts).

    It lives under the library root instead of 下载/ so the user-facing
    library only ever shows finished episodes, and it is wiped after every
    merge.  Keeping it beside the output means a custom library location
    never shuffles merge parts across two disks.
    """
    return os.path.join(library_root(), ".cache", "merge")

