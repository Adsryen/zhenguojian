"""Embedded mpv playback surface driven by JSON IPC over a Windows named pipe."""
from __future__ import annotations

import json
import os
import queue
import subprocess
import tempfile
import threading
import time
from collections import deque

from PyQt5.QtCore import QEvent, QPoint, QRectF, QSettings, Qt, QTimer, pyqtSignal
from PyQt5.QtGui import QColor, QCursor, QKeySequence, QPainter, QPalette, QPen, QPixmap
from PyQt5.QtWidgets import (QAbstractSlider, QAbstractSpinBox, QApplication, QComboBox,
                             QFrame, QGridLayout, QHBoxLayout, QLabel, QLineEdit,
                             QPlainTextEdit, QPushButton, QShortcut, QSizePolicy, QSlider,
                             QTextEdit, QVBoxLayout, QWidget)

from . import tools, ui
from .widgets_guo import EpisodeGrid

try:
    import pywintypes
    import win32file
    import win32pipe
    HAVE_WIN32 = True
except Exception:  # pragma: no cover - non-Windows fallback
    HAVE_WIN32 = False


# 全屏下鼠标不动这么久，就把控制条收起来让画面干净
CONTROLS_HIDE_DELAY = 5000

# P1b：鼠标离开画面、也不在悬停控制条上这么久，控制条自动收起
OVERLAY_HIDE_DELAY = 2000

# P1e：快捷键说明浮层无操作自动收起（毫秒）。刻意设短，避免遮罩长时间挡住
# 原生视频交互（Linux 下 mpv 是 X 子窗，鼠标未必经过 Qt 遮罩）。
HOTKEY_HELP_AUTO_HIDE_MS = 8000

# P1a：右侧选集停靠栏的展开宽度 / 折叠后细条宽度
DOCK_WIDTH = 300
DOCK_RAIL_WIDTH = 36

# P1d：QSettings 组织/应用名与键名（S1 设置面板消费同一批键，勿改名）
SETTINGS_ORG = "zhenguojian"
SETTINGS_APP = "zhenguojian"
SPEED_KEY = "player/speed"             # float
QUALITY_KEY = "player/quality"         # str slug
DANMAKU_KEY = "player/danmaku_enabled" # bool

# P1b/P1d：清晰度 slug → main 侧 resolve 用的 level（顺序与 quality_box 的
# 自动/1080P/720P/540P 一致），V1 接线时直接查这张表。
QUALITY_CHOICES = (("auto", "自动"), ("1080p", "1080P"),
                   ("720p", "720P"), ("540p", "540P"))
QUALITY_LABELS = dict(QUALITY_CHOICES)
QUALITY_LEVELS = {"auto": 0, "1080p": 1080, "720p": 720, "540p": 540}

# P1b：倍速常用档（参照 guoapp playback_preferences）
SPEED_CHOICES = (0.5, 0.75, 1.0, 1.25, 1.5, 2.0, 3.0)


def player_settings():
    """P1d 的持久层句柄；测试里可整体改到临时 IniFormat 路径，代码不变。"""
    return QSettings(SETTINGS_ORG, SETTINGS_APP)


def _as_bool(value, default=False):
    if isinstance(value, bool):
        return value
    if value is None:
        return default
    return str(value).strip().lower() in ("1", "true", "yes", "on")


def load_player_prefs():
    """读三枚播放偏好；键缺失时用现值播种（danmaku 取 tools 的配置）。"""
    settings = player_settings()
    try:
        speed = float(settings.value(SPEED_KEY, 1.0))
    except (TypeError, ValueError):
        speed = 1.0
    quality = str(settings.value(QUALITY_KEY, "auto") or "auto")
    if quality not in QUALITY_LEVELS:
        quality = "auto"
    raw = settings.value(DANMAKU_KEY, None)
    if raw is None:
        danmaku = bool(tools.danmaku_enabled())
        settings.setValue(DANMAKU_KEY, danmaku)
    else:
        danmaku = _as_bool(raw, True)
    return {"speed": speed, "quality": quality, "danmaku_enabled": danmaku}


# mpv.exe 静态链接了 Vulkan 加载器（libplacebo 的依赖），机器上没有这个 DLL 时
# Windows 会直接弹「丢失 vulkan-1.dll」并拒绝启动它，界面只能看到「无法连接播放器
# 控制通道」。runtime\mpv 里跟着一份就够了（mpv 实际用的是 D3D11，不依赖 Vulkan 驱动）。
PLAYER_REQUIRED_DLLS = ("vulkan-1.dll",)


def missing_player_dlls(binary):
    """播放器旁边还缺哪些运行库（系统目录里已有就不算缺）。"""
    folder = os.path.dirname(os.path.abspath(binary))
    system = os.path.join(os.environ.get("WINDIR") or r"C:\Windows", "System32")
    return [name for name in PLAYER_REQUIRED_DLLS
            if not os.path.isfile(os.path.join(folder, name))
            and not os.path.isfile(os.path.join(system, name))]


def format_time(seconds):
    seconds = int(max(0.0, float(seconds or 0)))
    return "%02d:%02d" % (seconds // 60, seconds % 60) if seconds < 3600 else \
        "%d:%02d:%02d" % (seconds // 3600, (seconds % 3600) // 60, seconds % 60)


class VideoSurface(QWidget):
    """Native child window that hosts the mpv video output."""

    def __init__(self, parent=None):
        super().__init__(parent)
        self.setAttribute(Qt.WA_NativeWindow, True)
        self.setAttribute(Qt.WA_OpaquePaintEvent, True)
        self.setMinimumSize(320, 180)
        self.setSizePolicy(QSizePolicy.Expanding, QSizePolicy.Expanding)
        palette = self.palette()
        palette.setColor(QPalette.Window, QColor(0, 0, 0))
        self.setPalette(palette)
        self.setAutoFillBackground(True)
        # 画面铺满整块区域：鼠标动不动得靠这里报给控制条（全屏自动隐藏用）
        self.setMouseTracking(True)

    def _notify(self):
        # 画面被放进叠加容器后，直接父级不再是播放器；向上找到真正的宿主。
        # 宿主可以同时挂 activity（全屏控制条，旧行为）和 hover_activity
        # （P1b 悬停控制条），两个都喂一次。
        widget = self.parent()
        while widget is not None:
            activity = getattr(widget, "activity", None)
            hover = getattr(widget, "hover_activity", None)
            if callable(activity) or callable(hover):
                if callable(activity):
                    activity()
                if callable(hover):
                    hover()
                return
            widget = widget.parent()

    def mouseMoveEvent(self, event):
        self._notify()
        super().mouseMoveEvent(event)

    def mousePressEvent(self, event):
        self._notify()
        super().mousePressEvent(event)

    def mouseDoubleClickEvent(self, event):
        widget = self.parent()
        while widget is not None:
            if hasattr(widget, "toggle_fullscreen"):
                widget.toggle_fullscreen()
                break
            widget = widget.parent()
        super().mouseDoubleClickEvent(event)


class VideoPoster(QWidget):
    """切集空档的海报层：冻住上一帧，中央给一条转圈的加载提示。

    播放器画面是独立的原生窗口，会盖住一切 Qt 兄弟控件，也没法淡入淡出，
    所以切集时先把原生窗口收起来，用这张图占住位置；等新会话真的出画了
    再换回去。没有上一帧时（第一次播放、自动连播）就是一块深色底，比原来
    直接露出黑底加一行字更稳。
    """

    def __init__(self, parent=None):
        super().__init__(parent)
        self._frame = QPixmap()
        self._scaled = None
        self._scaled_for = None
        self._hint = ""
        self._angle = 0
        self.setAttribute(Qt.WA_OpaquePaintEvent, True)
        self.setMinimumSize(320, 180)
        self._spin = QTimer(self)
        self._spin.setInterval(40)
        self._spin.timeout.connect(self._advance)

    def set_frame(self, pixmap, hint=""):
        self._frame = pixmap if pixmap is not None else QPixmap()
        self._scaled = None
        self._scaled_for = None
        self._hint = hint or ""
        self.update()

    def showEvent(self, event):
        self._angle = 0
        self._spin.start()
        super().showEvent(event)

    def hideEvent(self, event):
        self._spin.stop()
        super().hideEvent(event)

    def _advance(self):
        self._angle = (self._angle + 14) % 360
        self.update()

    def _fit_frame(self):
        """按 contain 缩放：竖屏短剧和横屏都按原比例落在这块区域里，不变形。"""
        if self._frame.isNull():
            return None
        if self._scaled is None or self._scaled_for != self.size():
            self._scaled = self._frame.scaled(self.size(), Qt.KeepAspectRatio,
                                              Qt.SmoothTransformation)
            self._scaled_for = self.size()
        return self._scaled

    def paintEvent(self, event):
        colors = ui.DESIGN
        painter = QPainter(self)
        painter.fillRect(self.rect(), QColor(colors["video_bg"]))
        frame = self._fit_frame()
        if frame is not None:
            painter.drawPixmap((self.width() - frame.width()) // 2,
                               (self.height() - frame.height()) // 2, frame)
            # 压暗一档，白色提示压在亮画面上也看得清
            scrim = QColor(colors["video_bg"])
            scrim.setAlpha(115)
            painter.fillRect(self.rect(), scrim)
        if not self._hint:
            return
        painter.setRenderHint(QPainter.Antialiasing, True)
        metrics = painter.fontMetrics()
        text = metrics.elidedText(self._hint, Qt.ElideRight, max(120, self.width() - 140))
        width = metrics.width(text) + 58
        height = 42
        x = (self.width() - width) / 2.0
        y = (self.height() - height) / 2.0
        pill = QColor(colors["video_bg"])
        pill.setAlpha(215)
        edge = QColor(colors["on_accent"])
        edge.setAlpha(32)
        painter.setPen(QPen(edge, 1))
        painter.setBrush(pill)
        painter.drawRoundedRect(QRectF(x, y, width, height), height / 2.0, height / 2.0)
        # 左侧转圈的小圆弧：加载中才有动感
        arc = QRectF(x + 16, y + (height - 18) / 2.0, 18, 18)
        painter.setBrush(Qt.NoBrush)
        painter.setPen(QPen(QColor(colors["on_accent"]), 2.2, Qt.SolidLine, Qt.RoundCap))
        painter.drawArc(arc, int(-self._angle * 16), int(260 * 16))
        painter.setPen(QColor(colors["on_accent"]))
        painter.drawText(QRectF(x + 46, y, width - 58, height),
                         Qt.AlignVCenter | Qt.AlignLeft, text)


class MpvSession:
    """One mpv process plus its JSON IPC channel (Windows named pipe).

    mpv owns the named pipe server.  This build's per-client handler can
    wedge mid-session (observed right after pausing: the process stays
    alive and serves NEW connections instantly but never reads from the
    older one again, and sometimes stalls replies on fresh ones too), so:

    - the long-lived connection is used only to READ events;
    - every command is queued and sent by a dedicated command thread over
      a fresh one-shot connection - the GUI thread never performs IPC and
      can never be blocked by a wedged mpv;
    - reads inside ``_request`` are bounded via ``PeekNamedPipe``, so even
      a worker thread cannot hang past the timeout.

    Events reach the caller through a thread-safe queue drained on the GUI
    thread; no worker thread touches Qt.
    """

    def __init__(self, binary, url, headers=None, decryption_key="", local=False,
                 on_event=None, wid=None):
        self.binary = binary
        self.url = url
        self.on_event = on_event
        self._replies = {}
        self._observed = {}
        self._stderr_tail = deque(maxlen=20)
        self._lock = threading.Lock()
        self._write_lock = threading.Lock()
        self._counter = 0
        self._handle = None
        self._process = None
        self._reader = None
        self._events = queue.Queue()
        self._commands = queue.Queue()
        self._closed = threading.Event()
        self._stopping = False
        self.pipe_name = "\\\\.\\pipe\\zhenguojian-mpv-%d-%d" % (
            os.getpid(), int(time.time() * 1000) % 1000000)
        self._launch(wid, headers or {}, decryption_key, local)
        try:
            self._open_pipe()
        except BaseException:
            # The process is already running: without this it outlived the
            # failed session and kept decoding into a destroyed window.
            self._stopping = True
            try:
                self._commands.put(None)
            except Exception:
                pass
            if self._process is not None and self._process.poll() is None:
                try:
                    self._process.kill()
                    self._process.wait(timeout=2)
                except Exception:
                    pass
            if self._handle is not None:
                try:
                    win32file.CloseHandle(self._handle)
                except Exception:
                    pass
                self._handle = None
            raise
        self._command_thread = threading.Thread(target=self._command_loop, daemon=True)
        self._command_thread.start()
        self._reader = threading.Thread(target=self._read_events, daemon=True)
        self._reader.start()

    # ------------------------------------------------------------------ setup
    def _log_path(self):
        """mpv 日志：--no-terminal 让它不吐 stderr，出问题时原因只在这里。

        固定文件名，排查时直接看 <配置目录>\\logs\\mpv.log 就行。
        """
        folder = os.path.join(tools.data_dir(), "logs")
        try:
            os.makedirs(folder, exist_ok=True)
        except OSError:
            return ""
        return os.path.join(folder, "mpv.log")

    @staticmethod
    def _log_message(row):
        """去掉 mpv 日志行前面那串 [时间][级别][模块] 前缀。"""
        marker = row.rfind("] ")
        return row[marker + 2:].strip() if marker != -1 else row.strip()

    def _mpv_tail(self, lines=2):
        """日志尾部：先捞错误级别的行，没有才退回最后几行。"""
        if not getattr(self, "_log_file", ""):
            return ""
        try:
            with open(self._log_file, "r", encoding="utf-8", errors="replace") as handle:
                rows = [row.strip() for row in handle.readlines()[-40:]]
        except OSError:
            return ""
        rows = [row for row in rows if row]
        picked = [row for row in rows if "][e]" in row or "][f]" in row] or rows
        return "；".join(self._log_message(row) for row in picked[-lines:])

    def _launch(self, wid, headers, decryption_key, local):
        missing = missing_player_dlls(self.binary)
        if missing:
            raise RuntimeError(
                "播放器缺少组件 %s。它要和 mpv.exe 放在同一个目录（runtime\\mpv），"
                "可以从正常播放的那台电脑整个复制 runtime 目录过来"
                % "、".join(missing))
        protocols = "protocol_whitelist=[file,crypto,data]" if local else \
            "protocol_whitelist=[http,https,tcp,tls,crypto,data,file]"
        options = [
            "seg_max_retry=5",
            "allowed_extensions=ALL",
            "strict=experimental",
            protocols,
        ]
        if decryption_key:
            options.append("decryption_key=%s" % decryption_key)
        args = [
            self.binary,
            "--no-config", "--no-terminal", "--idle=yes", "--force-window=no",
            "--keep-open=yes",
            "--hwdec=%s" % ("auto-safe" if tools.hwdec_enabled() else "no"),
            "--video-sync=display-resample",
            "--network-timeout=30", "--cache=yes", "--cache-secs=60",
            "--demuxer-lavf-o=%s" % ",".join(options),
            "--input-ipc-server=%s" % self.pipe_name,
        ]
        self._log_file = self._log_path()
        if self._log_file:
            args.append("--log-file=%s" % self._log_file)
        if wid:
            args.append("--wid=%d" % int(wid))
        if headers:
            args.append("--http-header-fields=%s" %
                        ",".join("%s: %s" % (k, v) for k, v in headers.items()))
        args.append(self.url)
        creation = getattr(subprocess, "CREATE_NO_WINDOW", 0) if os.name == "nt" else 0
        # stderr 留一份尾部：mpv 起不来或中途退出时，它自己说的话比
        # 「无法连接控制通道」有用得多。必须有人读，否则管道写满会把 mpv 卡死。
        self._process = subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                         stderr=subprocess.PIPE, creationflags=creation)
        threading.Thread(target=self._drain_stderr, daemon=True).start()

    def _drain_stderr(self):
        stream = self._process.stderr if self._process else None
        if stream is None:
            return
        for raw in iter(stream.readline, b""):
            line = raw.decode("utf-8", "replace").strip()
            if line:
                self._stderr_tail.append(line)

    def _why_died(self):
        """把 mpv 的退出码 / 日志尾部拼成一句人话。"""
        code = self._process.returncode if self._process else None
        if code == 0xC0000135:                 # STATUS_DLL_NOT_FOUND
            return "播放器组件缺失（0xC0000135）：请检查 runtime\\mpv 目录是否完整"
        for _ in range(10):                    # 等读线程/日志刷完再取尾部
            if self._stderr_tail:
                break
            time.sleep(0.02)
        tail = self._mpv_tail(2) or "；".join(list(self._stderr_tail)[-2:])
        if tail:
            return "播放器已退出（代码 %s）：%s" % (code, tail)
        return "播放器启动失败（退出代码 %s）" % code

    def _connect(self):
        import pywintypes
        try:
            return win32file.CreateFile(
                self.pipe_name,
                win32file.GENERIC_READ | win32file.GENERIC_WRITE,
                0, None, win32file.OPEN_EXISTING, 0, None)
        except pywintypes.error:
            return None

    def _open_pipe(self):
        """mpv owns the pipe server; retry until it is listening.

        The handle is duplex; reads and writes run on separate threads.
        """
        if os.name != "nt":
            raise RuntimeError("播放器需要 Windows 命名管道支持")
        deadline = time.time() + 15.0
        while time.time() < deadline:
            if self._process.poll() is not None:
                raise RuntimeError(self._why_died())
            if self._handle is None:
                self._handle = self._connect()
            if self._handle is not None:
                return
            time.sleep(0.05)
        detail = self._mpv_tail(2)
        raise RuntimeError("无法连接播放器控制通道：mpv 没有就绪%s"
                           % ("（%s）" % detail if detail
                              else "，请检查 runtime\\mpv 是否完整"))

    # ------------------------------------------------------------------- ipc
    def _read_events(self):
        buffer = b""
        while not self._stopping:
            try:
                err, count = win32file.ReadFile(self._handle, 65536)
            except Exception:
                break
            if not count:
                break
            buffer += count
            while b"\n" in buffer:
                line, buffer = buffer.split(b"\n", 1)
                self._dispatch(line)
        self._closed.set()

    def _dispatch(self, line):
        line = line.strip()
        if not line:
            return
        try:
            message = json.loads(line.decode("utf-8", "replace"))
        except Exception:
            return
        if "request_id" in message:
            with self._lock:
                self._replies[message["request_id"]] = message
            return
        if message.get("event") == "property-change":
            with self._lock:
                self._observed[message.get("name")] = (message.get("data"), time.time())
            # Property changes normally stay in the cache; eof-reached must
            # reach the GUI thread because PlayerPane turns it into finished.
            if message.get("name") == "eof-reached":
                self._events.put(message)
            return
        # Queue rather than calling back directly: the handler runs Qt code and
        # has to live on the GUI thread.
        self._events.put(message)

    def drain_events(self, limit=64):
        """Hand queued mpv events to the GUI thread (called from a QTimer)."""
        if self.on_event is None:
            return
        for _ in range(limit):
            try:
                message = self._events.get_nowait()
            except queue.Empty:
                return
            if self._stopping:
                return
            try:
                self.on_event(message)
            except Exception:
                pass

    def _command_loop(self):
        """Serialise every mpv command on one worker thread.

        Commands arrive from the GUI thread; running them here means a
        wedged mpv can delay a command but never freeze the interface.
        """
        while True:
            try:
                payload = self._commands.get(timeout=0.2)
            except queue.Empty:
                if self._stopping:
                    return
                continue
            if payload is None or self._stopping:
                return
            try:
                self._request(payload, timeout=3.0)
            except Exception:
                pass

    def _enqueue(self, payload):
        if self._stopping or self._handle is None:
            return
        self._commands.put(payload)

    def _request(self, payload, timeout=3.0):
        """Send one command over a fresh one-shot pipe connection.

        The long-lived connection is read-only for us (events); its write
        side cannot be trusted once mpv's handler for it wedges, and a new
        connection usually gets a fresh, responsive handler.  Reads are
        bounded via ``PeekNamedPipe``: a wedged mpv that never replies can
        delay at most ``timeout``, never hang the caller.
        """
        if self._stopping or os.name != "nt":
            return None
        payload = dict(payload)
        deadline = time.time() + timeout
        with self._lock:
            self._counter += 1
            payload["request_id"] = self._counter
        request_id = payload["request_id"]
        body = (json.dumps(payload, ensure_ascii=False) + "\n").encode("utf-8")
        while time.time() < deadline and not self._stopping:
            handle = self._connect()
            if handle is None:
                time.sleep(0.05)
                continue
            try:
                win32file.WriteFile(handle, body)
                buffer = b""
                while time.time() < deadline and not self._stopping:
                    try:
                        _peeked, avail, _left = win32pipe.PeekNamedPipe(handle, 0)
                    except pywintypes.error:
                        return None  # connection died mid-command
                    if not avail:
                        time.sleep(0.02)
                        continue
                    err, data = win32file.ReadFile(handle, min(avail, 65536))
                    if not data:
                        return None
                    buffer += data
                    while b"\n" in buffer:
                        line, buffer = buffer.split(b"\n", 1)
                        try:
                            message = json.loads(line.decode("utf-8", "replace"))
                        except Exception:
                            continue
                        if message.get("request_id") == request_id:
                            return message
                return None
            except Exception:
                if self._stopping:
                    return None
                time.sleep(0.05)
            finally:
                try:
                    win32file.CloseHandle(handle)
                except Exception:
                    pass
        return None

    # -------------------------------------------------------------- commands
    def command(self, args):
        self._enqueue({"command": args})

    def load(self, url, headers=None, decryption_key="", local=False):
        """Switch this mpv process to another file without restarting it.

        mpv keeps the video window and decoder alive; ``loadfile replace`` is
        what removes the black frame / new-process delay on next-episode.
        Per-file options (decryption key, HTTP headers, protocol whitelist) are
        set just before the load command, so they apply to the new file only.
        """
        self.url = url
        protocols = "protocol_whitelist=[file,crypto,data]" if local else \
            "protocol_whitelist=[http,https,tcp,tls,crypto,data,file]"
        options = ["seg_max_retry=5", "allowed_extensions=ALL",
                   "strict=experimental", protocols]
        if decryption_key:
            options.append("decryption_key=%s" % decryption_key)
        with self._lock:
            self._observed.clear()
        self.set_property("demuxer-lavf-o", ",".join(options))
        if headers:
            self.set_property("http-header-fields",
                              ",".join("%s: %s" % (key, value) for key, value in headers.items()))
        else:
            self.set_property("http-header-fields", "")
        self.command(["loadfile", url, "replace"])

    def get(self, name, default=None):
        """Read a property without ever blocking the caller.

        Observed properties are cached by the reader thread, so the UI timer can
        poll a dict instead of paying a request/reply round-trip every tick.
        A wedged mpv must never freeze the window, so the request/reply fallback
        is served by a throw-away helper thread with a hard deadline.
        """
        # A session that is being torn down has no pipe left to talk to.
        if self._handle is None or self._stopping:
            return default
        with self._lock:
            cached = self._observed.get(name)
            if cached is not None and cached[0] is not None:
                return cached[0]
        box = {}

        def ask():
            try:
                reply = self._request({"command": ["get_property", name]}, timeout=0.5)
                if reply and reply.get("error") == "success" and "data" in reply:
                    box["value"] = reply["data"]
            except Exception:
                pass

        helper = threading.Thread(target=ask, daemon=True)
        helper.start()
        helper.join(0.35)
        if "value" in box:
            return box["value"]
        with self._lock:
            cached = self._observed.get(name)
        if cached is not None and cached[0] is not None:
            return cached[0]
        return default

    def cached(self, name, default=None):
        """只读观察缓存，绝不发起 IPC：海报层按 80ms 轮询进度用。"""
        with self._lock:
            entry = self._observed.get(name)
        return entry[0] if entry is not None and entry[0] is not None else default

    def reset_observations(self, *names):
        """Drop cached properties, or only the named ones.

        ``loadfile`` keeps the old file's last frame visible (keep-open), so
        its time-pos and eof-reached must be forgotten before the next file's
        events arrive, while duration/width/height can stay useful.
        """
        with self._lock:
            if names:
                for name in names:
                    self._observed.pop(name, None)
            else:
                self._observed.clear()

    def set_property(self, name, value):
        self._enqueue({"command": ["set_property", name, value]})

    def observe(self, *names):
        """Subscribe to property changes and seed the cache in the background.

        mpv 只把 property-change 事件发给**注册观察的那条连接**。观察如果像
        别的命令一样走一次性连接，命令回复后连接就关了，事件永远到不了读线程
        —— 缓存会一直停在种子值：time-pos 不走、暂停后 pause 永远读成 False，
        表现就是「暂停后点播放没反应」（它其实又发了一次暂停）。

        所以观察走长连接（只写不读）；每个属性用独立 id，避免 mpv 按 id 覆盖
        掉前面的观察。种子值仍由一次性连接去取，拿到的回复直接进缓存。
        """
        if self._handle is None or self._stopping:
            return
        for index, name in enumerate(names, 1):
            self._write_events_pipe({"command": ["observe_property", index, name]})

        def seed():
            for name in names:
                try:
                    reply = self._request({"command": ["get_property", name]}, timeout=1.0)
                    if reply and reply.get("error") == "success" and "data" in reply:
                        with self._lock:
                            self._observed[name] = (reply["data"], time.time())
                except Exception:
                    pass

        threading.Thread(target=seed, daemon=True).start()

    def _write_events_pipe(self, payload):
        """把一条命令写进长连接（事件也在这条连接上回来）。

        不需要回复，所以不带 request_id；mpv 回的那行没有 id，_dispatch 会
        当成未知事件丢掉。写失败只影响这一次注册，不影响播放。
        """
        if self._handle is None or self._stopping:
            return False
        body = (json.dumps(payload, ensure_ascii=False) + "\n").encode("utf-8")
        with self._write_lock:
            try:
                win32file.WriteFile(self._handle, body)
                return True
            except Exception:
                return False

    def alive(self):
        return self._process is not None and self._process.poll() is None and not self._closed.is_set()

    def stop(self, blocking=False):
        """Shut mpv down.

        ``blocking=False`` returns immediately: the quit/terminate/kill
        sequence runs on a helper thread, so switching episodes never freezes
        the window.  The window is blocked on exit (``blocking=True``) so no
        orphan mpv is left behind.
        """
        if self._stopping:
            return
        self._stopping = True
        process = self._process
        try:
            self._commands.put(None)  # wake the command loop so it exits
        except Exception:
            pass

        def shutdown():
            try:
                self._request({"command": ["quit"]}, timeout=1.0)
            except Exception:
                pass
            deadline = time.time() + 1.5
            while process is not None and process.poll() is None and time.time() < deadline:
                time.sleep(0.05)
            if process is not None and process.poll() is None:
                try:
                    process.terminate()
                    process.wait(timeout=2)
                except Exception:
                    try:
                        process.kill()
                    except Exception:
                        pass
            if self._handle is not None:
                try:
                    win32file.CloseHandle(self._handle)
                except Exception:
                    pass
                self._handle = None
            self._closed.set()

        if blocking:
            shutdown()
        else:
            threading.Thread(target=shutdown, daemon=True).start()
class ControlOverlay(QWidget):
    """P1b：浮在画面底部的悬停控制条（清晰度 / 倍速 / 静音 / 音量）。

    样式全部走 ui.py 已有的 guoRole 段（controlOverlay 半透明底、ctrlBtn
    四态、全局 QSlider），组件内不出现任何颜色。已知问题：Windows 原生 mpv
    子窗口会盖住 Qt 兄弟叠层（PRD Notes 预期，交 V1 复核）；选项弹层用
    Qt.Popup 顶层窗，可以正常浮在画面上。
    """

    def __init__(self, pane, parent=None):
        super().__init__(parent)
        self._pane = pane
        self.setObjectName("playerOverlay")
        self.setProperty("guoRole", "controlOverlay")
        self.setAttribute(Qt.WA_StyledBackground, True)

        row = QHBoxLayout(self)
        row.setContentsMargins(14, 7, 14, 7)
        row.setSpacing(8)

        self.quality_button = QPushButton("自动")
        self.quality_button.setProperty("guoRole", "ctrlBtn")
        self.quality_button.setToolTip("清晰度（当前档点开可选）")
        self.quality_button.setCursor(Qt.PointingHandCursor)
        self.quality_button.clicked.connect(
            lambda: pane.show_quality_popup(self.quality_button))

        self.speed_button = QPushButton("1.0x")
        self.speed_button.setProperty("guoRole", "ctrlBtn")
        self.speed_button.setToolTip("倍速 0.5x–3.0x")
        self.speed_button.setCursor(Qt.PointingHandCursor)
        self.speed_button.clicked.connect(
            lambda: pane.show_speed_popup(self.speed_button))

        self.mute_button = QPushButton("静音")
        self.mute_button.setProperty("guoRole", "ctrlBtn")
        self.mute_button.setToolTip("静音 / 取消静音（M）")
        self.mute_button.setCursor(Qt.PointingHandCursor)
        self.mute_button.clicked.connect(pane.toggle_mute)

        self.volume_slider = QSlider(Qt.Horizontal)
        self.volume_slider.setRange(0, 100)
        self.volume_slider.setValue(100)
        self.volume_slider.setSingleStep(5)
        self.volume_slider.setPageStep(10)
        self.volume_slider.setFixedWidth(140)
        self.volume_slider.setToolTip("音量（↑↓ 每档 5）")
        self.volume_slider.valueChanged.connect(pane.set_volume)

        # P1e：快捷键说明入口。先用 ⌨（U+2328），本机 fc-list charset=2328
        # 为空、截图渲染成豆腐块（YaHei/DejaVu 均缺字形），按 EpisodeDock
        # 折叠按钮的先例换 ASCII「?」——兼作快捷键提示。
        self.hotkey_button = QPushButton("?")
        self.hotkey_button.setProperty("guoRole", "ctrlBtn")
        self.hotkey_button.setFixedSize(28, 26)
        # ctrlBtn 全局 padding 会把 28px 宽里的单字符挤掉（同折叠按钮先例：
        # 只覆内边距，不碰颜色），字号随主题默认。
        self.hotkey_button.setStyleSheet("padding: 2px 4px;")
        self.hotkey_button.setToolTip("快捷键说明（? 或 Esc）")
        self.hotkey_button.setCursor(Qt.PointingHandCursor)
        self.hotkey_button.clicked.connect(pane.toggle_hotkey_help)

        row.addWidget(self.quality_button)
        row.addWidget(self.speed_button)
        row.addStretch(1)
        row.addWidget(self.mute_button)
        row.addWidget(self.volume_slider)
        row.addWidget(self.hotkey_button)
        self.hide()

    def mouseMoveEvent(self, event):
        # 覆盖条上的活动也算“鼠标还在”，别让收起计时把正在用的条子收掉
        self._pane.hover_activity()
        super().mouseMoveEvent(event)

    def sync_geometry(self):
        """贴画面区底部横一条；宽度随画面走。"""
        stage = self.parentWidget()
        if stage is None:
            return
        margin = 10
        height = self.sizeHint().height()
        width = max(10, stage.width() - 2 * margin)
        self.setGeometry(margin, max(0, stage.height() - height - margin),
                         width, height)


class HotkeyHelpOverlay(QWidget):
    """P1e：快捷键说明浮层（悬停控制条 ⌨ 按钮或 ? 键唤出）。

    整画面半透明遮罩（guo_player_scrim）+ 居中卡片（surface2 底、border
    描边、guo_radius 圆角，全部 ui.py DESIGN token，每次 show 重建局部 QSS
    跟随主题）。点击遮罩任意处关闭；?/Esc 再按一次关闭；8 秒无操作自动收起
    ——刻意不常驻，免得遮罩挡住原生视频交互。

    Esc/? 用 WidgetWithChildrenShortcut 注册在本浮层上：浮层持有焦点时它俩
    优先于 main.py 的窗口级 Esc（退出全屏）与 player 的 ↑↓F M，关帮助不会
    顺带退出全屏；浮层收起后焦点离开，快捷键随之失效。
    """

    ROWS = (
        ("空格", "播放 / 暂停"),
        ("← / →", "快退 / 快进 5 秒"),
        ("↑ / ↓", "音量 ±5"),
        ("F", "进入 / 退出全屏"),
        ("M", "静音 / 取消静音"),
        ("? / Esc", "打开 / 关闭本帮助"),
    )

    def __init__(self, pane, parent=None):
        super().__init__(parent)
        self._pane = pane
        self.setObjectName("hotkeyHelpOverlay")
        self.setAttribute(Qt.WA_StyledBackground, True)
        self.setFocusPolicy(Qt.StrongFocus)

        outer = QVBoxLayout(self)
        outer.setContentsMargins(0, 0, 0, 0)
        card = QFrame(self)
        card.setObjectName("hotkeyHelpCard")
        outer.addWidget(card, 0, Qt.AlignCenter)

        body = QVBoxLayout(card)
        body.setContentsMargins(24, 16, 24, 12)
        body.setSpacing(8)

        title = QLabel("快捷键")
        title.setObjectName("hotkeyHelpTitle")
        title.setAlignment(Qt.AlignCenter)
        body.addWidget(title)

        grid = QGridLayout()
        grid.setHorizontalSpacing(20)
        grid.setVerticalSpacing(6)
        body.addLayout(grid)
        for index, (keys, desc) in enumerate(self.ROWS):
            key_label = QLabel(keys)
            key_label.setObjectName("hotkeyHelpKey")
            key_label.setAlignment(Qt.AlignRight | Qt.AlignVCenter)
            desc_label = QLabel(desc)
            desc_label.setObjectName("hotkeyHelpDesc")
            desc_label.setAlignment(Qt.AlignLeft | Qt.AlignVCenter)
            grid.addWidget(key_label, index, 0)
            grid.addWidget(desc_label, index, 1)

        tip = QLabel("点击任意处关闭 · 8 秒后自动收起")
        tip.setObjectName("hotkeyHelpTip")
        tip.setAlignment(Qt.AlignCenter)
        body.addWidget(tip)

        self._auto_hide = QTimer(self)
        self._auto_hide.setInterval(HOTKEY_HELP_AUTO_HIDE_MS)
        self._auto_hide.setSingleShot(True)
        self._auto_hide.timeout.connect(self.hide)

        self._close_shortcuts = []
        for key in (Qt.Key_Escape, Qt.Key_Question):
            shortcut = QShortcut(QKeySequence(key), self)
            shortcut.setContext(Qt.WidgetWithChildrenShortcut)
            shortcut.activated.connect(self.hide)
            self._close_shortcuts.append(shortcut)
        self._silenced_shortcuts = []

    _CONFLICT_SEQUENCES = (QKeySequence(Qt.Key_Escape), QKeySequence(Qt.Key_Question))

    def _silence_conflicting_shortcuts(self):
        # Qt5 快捷键图里「同键序列 + 不同上下文」会判 ambiguous、两边都不触发
        # （offscreen 实测：窗口级 Esc/浮层 WWC Esc 同键时双双被吞）。浮层可见
        # 期间把同窗口其他 Esc/? 快捷键临时停用，让浮层成为唯一响应者；
        # 收起时在 _unsilence 原样恢复。
        mine = set(self._close_shortcuts)
        window = self.window()
        for shortcut in window.findChildren(QShortcut):
            if shortcut in mine or not shortcut.isEnabled():
                continue
            if shortcut.key() in self._CONFLICT_SEQUENCES:
                self._silenced_shortcuts.append(shortcut)
                shortcut.setEnabled(False)

    def _unsilence_conflicting_shortcuts(self):
        for shortcut in self._silenced_shortcuts:
            shortcut.setEnabled(True)
        self._silenced_shortcuts = []

    def sync_geometry(self):
        """铺满 stage（画面区）整块遮罩；stage 尺寸变化时重铺。"""
        stage = self.parentWidget()
        if stage is None:
            return
        self.setGeometry(0, 0, max(1, stage.width()), max(1, stage.height()))

    def _apply_qss(self):
        self.setStyleSheet((
            "QWidget#hotkeyHelpOverlay { background: %(guo_player_scrim)s; }"
            "QFrame#hotkeyHelpCard { background: %(surface2)s;"
            "  border: 1px solid %(border)s; border-radius: %(guo_radius)s; }"
            "QLabel#hotkeyHelpTitle { color: %(text)s; font-size: 15px;"
            "  font-weight: 600; }"
            "QLabel#hotkeyHelpKey { color: %(text)s; font-size: 13px;"
            "  font-weight: 600; }"
            "QLabel#hotkeyHelpDesc { color: %(muted)s; font-size: 13px; }"
            "QLabel#hotkeyHelpTip { color: %(muted2)s; font-size: 12px; }"
        ) % ui.DESIGN)

    def showEvent(self, event):
        super().showEvent(event)
        self._apply_qss()
        self._silence_conflicting_shortcuts()
        self._auto_hide.start()
        self.setFocus()

    def hideEvent(self, event):
        self._auto_hide.stop()
        self._unsilence_conflicting_shortcuts()
        super().hideEvent(event)

    def mousePressEvent(self, event):
        # 遮罩任意位置（含卡片空白）左键都收起：帮助卡内没有可交互控件
        if event.button() == Qt.LeftButton:
            self.hide()
            return
        super().mousePressEvent(event)

    def keyPressEvent(self, event):
        # 浮层持焦时 Esc/? 归本帮助。上面注册的 WidgetWithChildrenShortcut 在
        # 多数平台会先于窗口级快捷键命中；但 Qt5 的快捷键图在「同键序列 + 不同
        # 上下文」（此处与本窗口的 WindowShortcut ?/Esc 并存）时会判定 ambiguous
        # 把两边一起压掉（offscreen 实测：两个都不触发）。事件没被快捷键吃掉时
        # 会正常落到这里，故兜底在按键处理里收起，行为与快捷键一致。
        if event.key() in (Qt.Key_Escape, Qt.Key_Question):
            self.hide()
            event.accept()
            return
        super().keyPressEvent(event)

    def mouseMoveEvent(self, event):
        # 鼠标还在动就续期，别在用户阅读时突然收掉
        self._auto_hide.start()
        super().mouseMoveEvent(event)


class EpisodeDock(QWidget):
    """P1a：右侧选集停靠栏（标题行 + EpisodeGrid），可折成一条细边。

    分组页签 / 当前集高亮 / 已看淡化都由 EpisodeGrid 的 guoRole QSS 负责，
    这里只管数据、折叠和把点击原样转发出去（信号带 1-based index）。
    """

    episode_selected = pyqtSignal(int)

    def __init__(self, parent=None):
        super().__init__(parent)
        self.setObjectName("episodeDock")
        self._episodes = []
        self._current_index = None
        self._collapsed = True  # 没有数据前先收成细条，不挤占画面
        self._auto_expanded = False

        outer = QVBoxLayout(self)
        outer.setContentsMargins(10, 10, 10, 10)
        outer.setSpacing(8)

        header = QHBoxLayout()
        header.setContentsMargins(0, 0, 0, 0)
        header.setSpacing(6)
        self.title_label = QLabel("选集")
        self.title_label.setProperty("guoRole", "sectionHeader")
        header.addWidget(self.title_label)
        header.addStretch(1)
        self.collapse_button = QPushButton("→")  # U+2192: 折叠方向；⟩/⟨ 在 YaHei/DejaVu 均缺字形
        self.collapse_button.setProperty("guoRole", "ctrlBtn")
        self.collapse_button.setFixedSize(26, 26)
        # ctrlBtn 全局 padding(12px 横向)会吃光 26px 宽的文字区；仅覆盖内边距，颜色仍走全局四态
        self.collapse_button.setStyleSheet("padding: 2px 4px;")
        self.collapse_button.setToolTip("收起选集栏")
        self.collapse_button.setCursor(Qt.PointingHandCursor)
        self.collapse_button.clicked.connect(self.toggle_collapsed)
        header.addWidget(self.collapse_button)
        outer.addLayout(header)

        self.grid = EpisodeGrid(self)
        self.grid.episode_clicked.connect(self.episode_selected)
        outer.addWidget(self.grid, 1)

        self._apply_collapsed()

    # ------------------------------------------------------------------ 数据
    def set_episodes(self, episodes):
        self._episodes = [dict(ep) for ep in (episodes or []) if isinstance(ep, dict)]
        for position, ep in enumerate(self._episodes):
            ep.setdefault("index", position + 1)
        self._push()
        if self._episodes and not self._auto_expanded:
            self._auto_expanded = True
            self.set_collapsed(False)

    def set_current(self, index):
        """1-based 集号：改 current 标记、切组并滚到可见。"""
        if index == self._current_index:
            self.grid.scroll_to_current()
            return
        self._current_index = index
        self._push()
        self.grid.scroll_to_current()

    def count(self):
        return len(self._episodes)

    def _push(self):
        rows = []
        for ep in self._episodes:
            row = dict(ep)
            row["current"] = (row.get("index") == self._current_index)
            rows.append(row)
        self.grid.set_episodes(rows)

    # ---------------------------------------------------------------- 折叠
    def toggle_collapsed(self):
        self.set_collapsed(not self._collapsed)

    def set_collapsed(self, collapsed):
        self._collapsed = bool(collapsed)
        self._apply_collapsed()

    def is_collapsed(self):
        return self._collapsed

    def _apply_collapsed(self):
        if self._collapsed:
            self.title_label.hide()
            self.grid.hide()
            self.setFixedWidth(DOCK_RAIL_WIDTH)
            self.collapse_button.setText("←")
            self.collapse_button.setToolTip("展开选集栏")
        else:
            self.setFixedWidth(DOCK_WIDTH)
            self.collapse_button.setText("→")
            self.collapse_button.setToolTip("收起选集栏")
            self.title_label.show()
            self._push()
            self.grid.show()


class PlayerPane(QWidget):
    """Player surface plus transport controls for one episode."""

    positionChanged = pyqtSignal(float, float)
    stateChanged = pyqtSignal(str)
    finished = pyqtSignal()
    failed = pyqtSignal(str)
    fullscreenRequested = pyqtSignal(bool)
    pauseChanged = pyqtSignal(bool)
    previousRequested = pyqtSignal()
    nextRequested = pyqtSignal()
    danmakuToggled = pyqtSignal(bool)
    danmakuOpacityChanged = pyqtSignal(int)
    # P1 新增（旧信号一律不动）：
    episodeClicked = pyqtSignal(int)        # 停靠栏点击，0-based position
    qualityChanged = pyqtSignal(str)        # 清晰度 slug（auto/1080p/720p/540p）
    volumeChanged = pyqtSignal(int)         # 音量 0–100
    preloadNextRequested = pyqtSignal(int)  # 下一集 1-based index（P1e 接线口）

    def __init__(self, parent=None):
        super().__init__(parent)
        # 让沉浸式全屏的 QSS 背景真正画在这个自绘容器上（否则控制条区域
        # 会透出浅色主题的窗口底色，全屏底部就是一条白边）。
        self.setAttribute(Qt.WA_StyledBackground, True)
        self.setObjectName("playerPane")
        self._session = None
        self._duration = 0.0
        self._position = 0.0
        self._seeking = False
        self._eof = False
        self._stopping = False
        self._last_tick = 0.0
        # What we last asked for.  mpv's observed "pause" property lags the
        # command by up to 80ms; comparing against it swallowed rapid toggles.
        self._pause_intent = False
        # A pending mpv launch: play() builds the session off-thread and
        # stop() invalidates the token so a late result is thrown away.
        self._starting = False
        self._start_token = None
        # The launch thread cannot post back with QTimer.singleShot: that
        # queues onto the calling thread, which has no event loop, so the
        # session was never adopted and the pane stayed dead.  Results travel
        # through this queue and are adopted by the GUI-thread timer below.
        self._start_results = queue.Queue()
        self._pending_resume = 0.0

        self.surface = VideoSurface(self)
        self.surface.hide()
        self.placeholder = QLabel("选择分集后点击播放")
        self.placeholder.setAlignment(Qt.AlignCenter)
        self.placeholder.setProperty("role", "playerPlaceholder")
        # 切集空档铺在画面位置上的海报层（原生视频窗口藏起来时看得见）
        self.poster = VideoPoster(self)
        self.poster.hide()
        self._poster_active = False
        self._poster_image = None
        self._poster_hint = ""
        self._poster_path = ""
        self._poster_size = -1
        self._poster_deadline = 0.0
        self._poster_token = 0
        self._load_generation = 0
        self._awaiting_file_load = False
        self._switch_pending = False

        self.prev_button = QPushButton("上一集")
        self.prev_button.setToolTip("播放上一集")
        self.prev_button.clicked.connect(self.previousRequested.emit)
        self.next_button = QPushButton("下一集")
        self.next_button.setToolTip("播放下一集")
        self.next_button.clicked.connect(self.nextRequested.emit)

        self.play_button = QPushButton("▶")
        self.play_button.setFixedWidth(48)
        self.play_button.setToolTip("播放 / 暂停（空格）")
        self.play_button.clicked.connect(self.toggle_pause)

        self.position_slider = QSlider(Qt.Horizontal)
        self.position_slider.setRange(0, 0)
        self.position_slider.setToolTip("拖动跳到任意位置")
        self.position_slider.sliderPressed.connect(self._on_slider_press)
        self.position_slider.sliderMoved.connect(self._on_slider_move)
        self.position_slider.sliderReleased.connect(self._on_slider_release)

        self.time_label = QLabel("00:00 / 00:00")
        self.time_label.setMinimumWidth(120)
        self.time_label.setAlignment(Qt.AlignCenter)

        self.speed_box = QPushButton("倍速 1.0x")
        self.speed_box.clicked.connect(self.cycle_speed)

        # 弹幕开关：字幕轨由 attach_danmaku() 挂给 mpv，这里只切显隐。
        # P1d：优先恢复 QSettings 的键；键缺失时 load_player_prefs 会用
        # tools 现值播种，两边不一致时回写 tools（main 的抓取闸门读 tools）。
        self._prefs = load_player_prefs()
        self._danmaku_enabled = bool(self._prefs["danmaku_enabled"])
        if self._danmaku_enabled != bool(tools.danmaku_enabled()):
            try:
                tools.set_danmaku_enabled(self._danmaku_enabled)
            except Exception:
                pass
        self._danmaku_loaded = False
        self._danmaku_path = ""
        self._danmaku_count = 0
        self._danmaku_note = ""
        self.danmaku_button = QPushButton("弹幕")
        self.danmaku_button.setCheckable(True)
        self.danmaku_button.setChecked(self._danmaku_enabled)
        self.danmaku_button.setProperty("role", "toggle")
        self.danmaku_button.toggled.connect(self._on_danmaku_button)
        self._sync_danmaku_tip()

        # 透明度：像音量那样点开一个小竖条，上下拖动调节
        self._danmaku_opacity = tools.danmaku_opacity()
        self._opacity_popup = None
        self._opacity_slider = None
        self._opacity_value = None
        self.opacity_button = QPushButton()
        self.opacity_button.setToolTip("弹幕透明度：点开后上下拖动")
        self.opacity_button.clicked.connect(self._show_opacity_popup)
        self._sync_opacity_button()

        self.full_button = QPushButton("全屏")
        self.full_button.clicked.connect(self.toggle_fullscreen)

        # 控制条整块藏/显，所以需要一个容器（全屏 5 秒不动就收起来）
        self.controls = QWidget()
        self.controls.setObjectName("playerControls")
        self.controls.setAttribute(Qt.WA_StyledBackground, True)
        controls = QHBoxLayout(self.controls)
        controls.setContentsMargins(0, 0, 0, 0)
        controls.setSpacing(6)
        controls.addWidget(self.play_button)
        controls.addWidget(self.prev_button)
        controls.addWidget(self.next_button)
        controls.addWidget(self.position_slider, 1)
        controls.addWidget(self.time_label)
        controls.addWidget(self.speed_box)
        controls.addWidget(self.danmaku_button)
        controls.addWidget(self.opacity_button)
        controls.addWidget(self.full_button)

        # 画面、占位、海报三层叠在同一格里，靠显隐切换；原生视频窗口显示时
        # 永远盖在最上面，所以另外两层只会在它藏起来时露出来。
        self.stage = QWidget()
        stage = QGridLayout(self.stage)
        stage.setContentsMargins(0, 0, 0, 0)
        stage.setSpacing(0)
        stage.addWidget(self.surface, 0, 0)
        stage.addWidget(self.placeholder, 0, 0)
        stage.addWidget(self.poster, 0, 0)
        stage.setRowStretch(0, 1)
        stage.setColumnStretch(0, 1)

        # P1a：左侧画面+控制条，右侧选集停靠栏（可折叠）。stage / controls
        # 两个对象与 objectName 原样保留——main.py 的沉浸式全屏样式仍指着
        # player.controls，外层旧侧栏不受影响。
        column = QVBoxLayout()
        column.setContentsMargins(0, 0, 0, 0)
        column.setSpacing(6)
        column.addWidget(self.stage, 1)
        column.addWidget(self.controls)

        self.dock = EpisodeDock(self)
        self.dock.episode_selected.connect(self._on_episode_clicked)

        layout = QHBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(6)
        layout.addLayout(column, 1)
        layout.addWidget(self.dock)

        # P1b：悬停在画面上时浮出的半透明控制条（清晰度/倍速/静音/音量）。
        # stage 开了鼠标追踪：占位/海报上的移动会冒泡到 stage，由 eventFilter
        # 转成 hover_activity；覆盖条自身也在 mouseMoveEvent 里续期。
        self.overlay = ControlOverlay(self, self.stage)
        self.stage.setMouseTracking(True)
        self.stage.installEventFilter(self)
        self._overlay_timer = QTimer(self)
        self._overlay_timer.setInterval(OVERLAY_HIDE_DELAY)
        self._overlay_timer.setSingleShot(True)
        self._overlay_timer.timeout.connect(self._hide_overlay)

        # P1e：快捷键说明浮层（同样挂在 stage 上，晚于覆盖条创建→层级更高）
        self.hotkeys_overlay = HotkeyHelpOverlay(self, self.stage)
        self.hotkeys_overlay.hide()

        # P1c/P1d：倍速扩成常用档；三枚偏好键在构造期恢复（只读盘不写盘）。
        self.set_speeds = list(SPEED_CHOICES)
        self._speed_index = self._nearest_speed_index(self._prefs["speed"])
        self._quality_slug = str(self._prefs["quality"])
        self._volume = 100
        self._muted = False
        self._episode_position = None   # 0-based，与 main 的切集序号同语义
        self._preload_requested_for = None
        self._preload_hook = None
        self._sync_speed_ui()
        self._sync_quality_ui()
        self._sync_volume_ui()

        # P1c/P1d/P1e：只新增 main.py 未占用的按键——空格/←/→ 已由主窗口绑定
        # 走 toggle_pause/seek_relative，这里再绑会双重触发；↑↓/F/M/? 是空位
        # （? 即 Shift+/，main.py 全仓 grep 无绑定）。
        self._shortcuts = []
        for key, handler in ((Qt.Key_Up, lambda: self.change_volume(5)),
                             (Qt.Key_Down, lambda: self.change_volume(-5)),
                             (Qt.Key_F, self.toggle_fullscreen),
                             (Qt.Key_M, self.toggle_mute),
                             (Qt.Key_Question, self.toggle_hotkey_help)):
            shortcut = QShortcut(QKeySequence(key), self)
            shortcut.setContext(Qt.WindowShortcut)
            shortcut.activated.connect(lambda h=handler: self._run_player_shortcut(h))
            self._shortcuts.append(shortcut)
        self._fullscreen = False
        # 光标是否被我们藏起来了：全屏空闲时和控件一起收掉
        self._cursor_hidden = False
        self._neighbours = (False, False)
        self.setMouseTracking(True)

        self._timer = QTimer(self)
        self._timer.setInterval(500)
        self._timer.timeout.connect(self._tick)
        self._timer.start()

        self._controls_timer = QTimer(self)
        self._controls_timer.setInterval(CONTROLS_HIDE_DELAY)
        self._controls_timer.setSingleShot(True)
        self._controls_timer.timeout.connect(self._hide_controls)

        # 切集海报：轮询 mpv 存出来的那一帧，并在新画面出来后把海报撤掉
        self._poster_poll = QTimer(self)
        self._poster_poll.setInterval(50)
        self._poster_poll.timeout.connect(self._poll_poster)
        self._poster_swap = QTimer(self)
        self._poster_swap.setSingleShot(True)
        self._poster_swap.timeout.connect(self._swap_in_surface)
        # 海报盖着时盯播放进度：mpv 一开始出画就撤，不能等兜底计时
        self._poster_probe = QTimer(self)
        self._poster_probe.setInterval(50)
        self._poster_probe.timeout.connect(self._probe_first_frame)

        # mpv events are queued by the reader thread and drained here so
        # that Qt code always runs on the GUI thread.
        self._event_timer = QTimer(self)
        self._event_timer.setInterval(80)
        self._event_timer.timeout.connect(self._drain)
        self._event_timer.start()

    # ------------------------------------------------------------------ state
    def is_playing(self):
        # Only an adopted session counts: while mpv is still launching the pane
        # is "busy" but has nothing to poll, and touching the pipe too early
        # takes the window down.
        if self._session is not None:
            return self._session.alive()
        return False

    def _set_surface(self, visible):
        # 两个都留在布局里、只切显隐：replaceWidget 会把占位标签从布局里摘掉，
        # 之后再显示它就没人管它的位置了（控制条一收一放就会露出来）。
        self.surface.setVisible(visible)
        self.placeholder.setVisible(not visible)
        if visible:
            self._poster_active = False
            self._poster_probe.stop()
            self.poster.hide()
            self.surface.raise_()

    def _set_status(self, text, error=False):
        """写占位文字。error=True 走 QSS 的 state=error（暗底上的红字）。

        颜色交给样式表而不是内联，换主题时这一块才会跟着变。
        """
        self.placeholder.setText(text)
        self.placeholder.setProperty("state", "error" if error else "normal")
        # 改了动态属性要重新 polish，QSS 才会重新匹配到它
        self.placeholder.style().unpolish(self.placeholder)
        self.placeholder.style().polish(self.placeholder)

    # ----------------------------------------------------------------- poster
    def capture_poster(self):
        """切集前让 mpv 把当前画面存成临时图片，填住启动新会话的空档。

        解析下一集要花几百毫秒，这段时间旧画面还在正常播放，所以在这里
        就发截图命令；等真正 play() 时图片基本已经就绪。
        """
        session = self._session
        if session is None or not session.alive():
            return
        self._poster_token += 1
        self._poster_image = None
        self._discard_poster_file()
        path = os.path.join(
            tempfile.gettempdir(),
            "zhenguojian-poster-%d-%d.jpg" % (os.getpid(), self._poster_token))
        self._poster_path = path
        self._poster_size = -1
        self._poster_deadline = time.time() + 2.0
        session.command(["screenshot-to-file", path])
        self._poster_poll.start()

    def _discard_poster_file(self):
        path, self._poster_path = self._poster_path, ""
        if path:
            try:
                os.remove(path)
            except OSError:
                pass

    def _poll_poster(self):
        """等 jpg 写完（大小稳定且能解码）再读，别把半张图读进来。"""
        path = self._poster_path
        if not path or time.time() >= self._poster_deadline:
            self._poster_poll.stop()
            return
        try:
            size = os.path.getsize(path)
        except OSError:
            return
        if size <= 0 or size != self._poster_size:
            self._poster_size = size
            return
        pixmap = QPixmap(path)
        if pixmap.isNull():
            return
        self._poster_image = pixmap
        self._poster_poll.stop()
        self._discard_poster_file()
        if self._switch_pending:
            self._show_poster(self._poster_hint)
            self._poster_probe.start()
            self._poster_swap.start(3000)

    def _show_poster(self, hint=""):
        self._poster_active = True
        self.surface.hide()
        self.placeholder.hide()
        self.poster.set_frame(self._poster_image, hint or self._poster_hint)
        self.poster.show()
        self.poster.raise_()

    def _hide_poster(self):
        self._poster_active = False
        self._poster_probe.stop()
        self.poster.hide()
        if not self.surface.isVisible():
            self.placeholder.show()

    def _probe_first_frame(self):
        """海报还盖着时盯住缓存里的播放进度：一开始走就说明新画面在出了。

        mpv 的 property-change 事件只在 MpvSession 内部更新缓存、不往 GUI
        线程转发，所以这里读 cached()——没有 IPC 往返，也不会卡界面。
        """
        session = self._session
        if session is None or not self._poster_active:
            self._poster_probe.stop()
            return
        if self._awaiting_file_load:
            # The previous file's last frame is still on screen with
            # keep-open; its time-pos would otherwise look like the new
            # episode has started.
            return
        try:
            position = float(session.cached("time-pos", 0.0) or 0.0)
        except (TypeError, ValueError):
            position = 0.0
        if position <= 0.0:
            return
        self._poster_probe.stop()
        # 时间在走了；再留一帧的渲染时间，然后换回原生视频窗口
        self._poster_swap.stop()
        self._poster_swap.start(70)

    def _swap_in_surface(self):
        """新会话已经在出画：撤掉海报，换回原生视频窗口。"""
        self._poster_swap.stop()
        if not self._poster_active:
            return
        self._poster_image = None       # 这张海报只服务这一次切集
        self._set_surface(True)

    # ----------------------------------------------------------------- launch
    def play(self, url, headers=None, decryption_key="", local=False, title="", resume=0.0,
             hint="", reuse=False):
        """Start playback without ever blocking the GUI thread.

        Spawning mpv and connecting its IPC pipe takes seconds when the decoder
        stalls, so the session is built on a worker thread and adopted on the
        GUI thread once it is ready.  A newer request cancels the older one.

        ``reuse=True`` switches an already-running, healthy mpv to the new URL
        with ``loadfile replace``: no new process, no surface swap, no black
        frame when advancing to the next episode.
        """
        if reuse and self._session is not None and self._session.alive() \
                and not self._poster_active:
            if self._load_in_session(url, headers, decryption_key, local, resume, hint):
                return True
        self.stop(restore_window=False)
        # stop() shows the controls again; in fullscreen a channel switch
        # should start clean (hidden) instead of showing them for 5 seconds.
        # Moving the mouse brings them back through activity().
        if self._fullscreen:
            self._controls_timer.stop()
            self.controls.hide()
            self._set_cursor_hidden(True)
        binary = tools.mpv_path()
        if not binary or not os.path.isfile(binary):
            self.failed.emit("缺少 mpv 播放器组件")
            return False
        self._eof = False
        self._pause_intent = False
        self._starting = True
        self._duration = 0.0
        self._pending_resume = float(resume or 0.0)
        self.position_slider.setRange(0, 0)
        self.time_label.setText("00:00 / 00:00")
        # 先铺海报（有上一帧就冻住它，没有就是深色底），别让画面直接黑一下；
        # 新会话真的出画后 _swap_in_surface() 再换回视频窗口。
        self._switch_pending = True
        self._poster_hint = hint or title or "正在加载"
        self._show_poster(self._poster_hint)
        self._set_status("正在启动播放器…")
        self.stateChanged.emit("正在加载 %s" % title if title else "正在加载")
        try:
            wid = int(self.surface.winId())
        except Exception:
            wid = None
        request = object()

        def build():
            try:
                session = MpvSession(
                    binary, url, headers=headers, decryption_key=decryption_key,
                    local=local, on_event=self._on_event, wid=wid)
            except Exception as exc:  # noqa: BLE001 - reported to the UI
                self._start_results.put((request, None, title, exc))
                return
            self._start_results.put((request, session, title, None))

        self._start_token = request
        threading.Thread(target=build, daemon=True).start()
        return True

    def _load_in_session(self, url, headers, decryption_key, local, resume, hint):
        """Reuse the running mpv: keep the surface, swap only the file."""
        session = self._session
        if session is None or not session.alive():
            return False
        session.reset_observations()
        self._eof = False
        self._pause_intent = False
        self._duration = 0.0
        self._position = 0.0
        self._pending_resume = float(resume or 0.0)
        self._load_generation += 1
        generation = self._load_generation
        self._awaiting_file_load = True
        self._switch_pending = True
        self._poster_hint = hint or "正在切换下一集…"
        if self._poster_image is not None:
            # The screenshot taken before resolving the next plan is ready:
            # freeze it over the native surface while loadfile switches files.
            self._poster_poll.stop()
            self._show_poster(self._poster_hint)
            self._poster_probe.start()
            self._poster_swap.start(3000)
        elif self._poster_path:
            # Screenshot still being written; _poll_poster will show it.
            self._poster_poll.start()
        else:
            # No frame to freeze: keep the old surface rather than showing a
            # black poster.  The native window may flash, but we have no image.
            self._switch_pending = False

        self.position_slider.setRange(0, 0)
        self.time_label.setText("00:00 / 00:00")
        self.play_button.setText("❚❚")
        self._set_status(self._poster_hint)
        # The old ASS belongs to the previous episode; remove it before the
        # new danmaku track is attached, otherwise a few seconds of the old
        # danmaku can show over the new picture.
        if self._danmaku_loaded:
            session.command(["sub-remove"])
        self.reset_danmaku()
        # Do not unpause here: at EOF keep-open has already paused mpv, so
        # clearing pause now just lets the old file run to EOF and pause
        # again before loadfile arrives.  The new file is unpaused once it
        # reports file-loaded.

        def start_load():
            if generation != self._load_generation:
                return
            if session is not self._session or not session.alive():
                return
            if self._poster_image is not None:
                if not self._poster_active:
                    self._poster_poll.stop()
                    self._show_poster(self._poster_hint)
                    self._poster_probe.start()
                    self._poster_swap.start(3000)
            else:
                self._switch_pending = False
                self._poster_poll.stop()
            session.load(url, headers=headers, decryption_key=decryption_key, local=local)

        # Give the screenshot a short head start; until loadfile runs the old
        # surface still shows the last frame, so this is not a visible wait.
        if self._poster_path and self._poster_image is None:
            QTimer.singleShot(150, start_load)
        else:
            start_load()
        self.pauseChanged.emit(False)
        return True

    def _start_failed(self, request, exc):
        if request is not self._start_token:
            return
        self._starting = False
        self._switch_pending = False
        self._poster_swap.stop()
        self._poster_image = None
        self._hide_poster()
        self._set_status("启动播放器失败：%s" % exc, error=True)
        self.failed.emit("启动播放器失败：%s" % exc)

    def _start_ready(self, request, session, title):
        if request is not self._start_token:
            # A newer play()/stop() happened while mpv was starting.
            try:
                session.stop()
            except Exception:
                pass
            return
        self._start_token = None
        self._starting = False
        self._session = session
        self._awaiting_file_load = True
        self._switch_pending = False
        if self._poster_active:
            # 海报先留在上面：轮询播放进度，一出画就换回去（3 秒兜底防卡死），
            # 否则新会话还没渲染就撤海报会闪黑，也不能让声音先跑几秒。
            self._poster_probe.start()
            self._poster_swap.start(3000)
        else:
            self._set_surface(True)
        self.play_button.setText("❚❚")
        self._session.observe("time-pos", "duration", "pause", "eof-reached",
                              "width", "height")
        # Keep the speed chosen before this launch across episodes.
        self._session.set_property("speed", self.set_speeds[self._speed_index])
        # P1c：音量/静音跟着悬停控制条的状态延续到新会话。
        self._session.set_property("volume", int(self._volume))
        if self._muted:
            self._session.set_property("mute", True)
        # P1c：音量/静音跟着悬停控制条的当前状态延续到新会话。
        self._session.set_property("volume", int(self._volume))
        if self._muted:
            self._session.set_property("mute", True)
        if self._danmaku_loaded and self._danmaku_path:
            self._session.command(["sub-add", self._danmaku_path, "select", "弹幕"])
            self._session.set_property("sub-visibility",
                                       "yes" if self._danmaku_enabled else "no")
        self.pauseChanged.emit(False)
        # Controls deliberately stay as play() left them: hidden when
        # switching inside fullscreen, visible otherwise.

    def _on_event(self, message):
        event = message.get("event")
        if event == "file-loaded":
            self._awaiting_file_load = False
            # Drop stale time-pos/eof-reached from the previous file before
            # the poster probe decides the new picture is ready.
            if self._session is not None:
                self._session.reset_observations("time-pos", "eof-reached")
                # keep-open pauses mpv at EOF, and loadfile inherits that
                # pause.  Unpause only after the new file is actually loaded;
                # otherwise the first episode of the next load stays frozen.
                if not self._pause_intent:
                    self._session.set_property("pause", False)
            self._set_status("播放中")
            # P1e：本集真正加载出来了，才去预热下一集（内部按集去重）。
            self._request_preload_next()
            if self._pending_resume > 1.0:
                resume = self._pending_resume
                self._pending_resume = 0.0
                self._session.command(["seek", resume, "absolute"])
            else:
                self._pending_resume = 0.0
        elif event == "playback-restart":
            # Safety net for a missed file-loaded: mpv only restarts playback
            # after the new file is demuxed, so stale EOF events are behind us.
            self._awaiting_file_load = False
            if self._session is not None and not self._pause_intent:
                self._session.set_property("pause", False)
        elif event == "end-file":
            reason = message.get("reason", "eof")
            if reason == "eof" and not self._stopping and not self._eof and not self._awaiting_file_load:
                self._eof = True
                QTimer.singleShot(0, self.finished.emit)
            elif reason == "error" and not self._stopping:
                # 之前这种失败是完全静默的：画面停住、什么也不说。mpv 的
                # end-file 事件自带原因，没有就翻它的日志。
                self._switch_pending = False
                self._poster_swap.stop()
                self._poster_image = None
                self._hide_poster()
                detail = (message.get("error") or message.get("file_error") or "").strip()
                detail = detail or (self._session._mpv_tail(1) if self._session else "")
                text = "播放失败：%s" % (detail or "播放器没有给出原因，可查看 logs\\mpv.log")
                self._set_status(text, error=True)
                self.failed.emit(text)
        elif event == "property-change":
            name = message.get("name")
            if name == "eof-reached" and message.get("data") and not self._eof and not self._stopping and not self._awaiting_file_load:
                # keep-open=yes keeps the last frame visible; this property is
                # therefore the reliable end-of-episode signal (end-file may
                # not be sent when the file is kept open).
                self._eof = True
                QTimer.singleShot(0, self.finished.emit)

    # -------------------------------------------------------------- transport
    def toggle_pause(self):
        if not self.is_playing():
            return
        self.set_paused(not self.is_paused())

    def is_paused(self):
        if not self.is_playing():
            return False
        return bool(self._session.get("pause", False))

    def set_paused(self, paused):
        """明确地停/继续，而不是猜当前状态——切页签、空格键都用它。

        Returns True when the state actually changed.
        """
        if not self.is_playing():
            return False
        paused = bool(paused)
        if self._pause_intent == paused:
            return False
        self._pause_intent = paused
        self._session.set_property("pause", paused)
        self.play_button.setText("▶" if paused else "❚❚")
        self.pauseChanged.emit(paused)
        self.activity()          # 全屏下按空格：把控制条亮出来，看得见状态变了
        return True

    def set_neighbours(self, has_previous, has_next):
        """首集没有上一集、末集没有下一集，按钮跟着置灰。"""
        self._neighbours = (bool(has_previous), bool(has_next))
        self.prev_button.setEnabled(bool(has_previous))
        self.next_button.setEnabled(bool(has_next))

    def seek_relative(self, seconds):
        if self.is_playing():
            self._session.command(["seek", seconds, "relative"])

    def cycle_speed(self):
        # 底栏按钮的单点入口：全部落到 set_speed（吸附+应用+持久化同一处）。
        self.set_speed(self.set_speeds[(self._speed_index + 1) % len(self.set_speeds)])

    @staticmethod
    def _speed_text(speed):
        # 1.25 must never render as "1.2": keep two decimals only when needed.
        if speed == int(speed):
            return "倍速 %.1fx" % speed
        return "倍速 %sx" % ("%.2f" % speed).rstrip("0")

    # ------------------------------------------------------- P1b 悬停控制条
    def eventFilter(self, watched, event):
        # stage 上的鼠标移动（占位/海报/覆盖条空白区冒泡上来的）都算悬停
        # 活动；stage 尺寸变了（折叠停靠栏、窗口缩放）要把覆盖条重新贴底。
        if watched is self.stage:
            etype = event.type()
            if etype == QEvent.MouseMove:
                self.hover_activity()
            elif etype == QEvent.Resize:
                if self.overlay.isVisible():
                    self.overlay.sync_geometry()
                if self.hotkeys_overlay.isVisible():
                    self.hotkeys_overlay.sync_geometry()
        return super().eventFilter(watched, event)

    def hover_activity(self):
        """画面内有鼠标活动：亮出悬停控制条，并开始/续期 2 秒收起倒计时。"""
        self.overlay.sync_geometry()
        self.overlay.show()
        self.overlay.raise_()
        self._overlay_timer.start()

    def _hide_overlay(self):
        if not self.overlay.isVisible():
            return
        # 鼠标还停在覆盖条上（正在拖音量、点清晰度）就先别收，重新计时
        point = self.overlay.mapFromGlobal(QCursor.pos())
        if self.overlay.rect().contains(point):
            self._overlay_timer.start()
            return
        self.overlay.hide()

    def show_quality_popup(self, anchor):
        """清晰度浮层：现有 quality 通道（slug 存键 + 信号，V1 接 resolve）。"""
        self._show_choice_popup(anchor, list(QUALITY_CHOICES),
                                self._quality_slug, self.set_quality_slug)

    def show_speed_popup(self, anchor):
        self._show_choice_popup(anchor,
                                [(value, self._speed_chip(value)) for value in self.set_speeds],
                                self.speed(), self.set_speed)

    def _show_choice_popup(self, anchor, entries, current_value, on_pick):
        """竖排选项小浮层。和透明度条同路数：Qt.Popup 顶层窗，能浮在原生
        视频窗口之上（Windows 下覆盖条本体可能被盖，见类注释，交 V1 复核）。"""
        popup = QFrame(self, Qt.Popup)
        popup.setProperty("role", "playerPopup")
        popup.setAttribute(Qt.WA_StyledBackground, True)
        popup.setAttribute(Qt.WA_DeleteOnClose, True)
        body = QVBoxLayout(popup)
        body.setContentsMargins(6, 6, 6, 6)
        body.setSpacing(4)
        for value, label in entries:
            button = QPushButton(("✓ " if value == current_value else "") + str(label))
            button.setProperty("guoRole", "ctrlBtn")
            button.setCursor(Qt.PointingHandCursor)
            button.setMinimumWidth(88)

            def picked(event=None, value=value, popup=popup, on_pick=on_pick):
                popup.close()
                on_pick(value)

            button.clicked.connect(picked)
            body.addWidget(button)
        popup.adjustSize()
        top = anchor.mapToGlobal(QPoint(0, 0))
        popup.move(top.x(), max(0, top.y() - popup.height() - 6))
        popup.show()
        popup.raise_()

    # -------------------------------------------------------------- P1d 倍速
    def speed(self):
        return self.set_speeds[self._speed_index]

    def _nearest_speed_index(self, value):
        try:
            target = float(value)
        except (TypeError, ValueError):
            return self.set_speeds.index(1.0)
        return min(range(len(self.set_speeds)),
                   key=lambda i: abs(self.set_speeds[i] - target))

    def set_speed(self, value):
        """倍速单点入口：吸附常用档、应用会话、写 QSettings（变更即存）。"""
        index = self._nearest_speed_index(value)
        speed = self.set_speeds[index]
        self._speed_index = index
        self._sync_speed_ui()
        if self.is_playing():
            self._session.set_property("speed", speed)
        player_settings().setValue(SPEED_KEY, float(speed))
        self.activity()

    def _sync_speed_ui(self):
        speed = self.set_speeds[self._speed_index]
        self.speed_box.setText(self._speed_text(speed))
        self.overlay.speed_button.setText(self._speed_chip(speed))

    @staticmethod
    def _speed_chip(speed):
        if speed == int(speed):
            return "%.1fx" % speed
        return "%sx" % ("%.2f" % speed).rstrip("0")

    # ------------------------------------------------------------ P1c 音量
    def volume(self):
        return int(self._volume)

    def is_muted(self):
        return bool(self._muted)

    def set_volume(self, value):
        """0–100（与 mpv volume 同标度）。无会话时只记状态——Mock 验证走这条。"""
        try:
            level = int(round(float(value)))
        except (TypeError, ValueError):
            return
        level = max(0, min(100, level))
        self._volume = level
        if self.is_playing():
            self._session.set_property("volume", level)
            if level > 0 and self._muted:
                self._muted = False
                self._session.set_property("mute", False)
        self._sync_volume_ui()
        self.volumeChanged.emit(self._volume)

    def change_volume(self, delta):
        self.set_volume(self._volume + int(delta))

    def toggle_mute(self):
        self._muted = not self._muted
        if self.is_playing():
            self._session.set_property("mute", self._muted)
        self._sync_volume_ui()
        self.activity()

    def _sync_volume_ui(self):
        slider = self.overlay.volume_slider
        if slider.value() != self._volume and not slider.isSliderDown():
            slider.blockSignals(True)
            slider.setValue(self._volume)
            slider.blockSignals(False)
        self.overlay.mute_button.setText("已静音" if self._muted else "静音")

    # ---------------------------------------------------------- P1b 清晰度
    def quality_slug(self):
        return self._quality_slug

    @staticmethod
    def quality_level(slug):
        """slug → resolve/preload 用的 int（自动/1080/720/540，同 main 顺序）。"""
        return QUALITY_LEVELS.get(str(slug), 0)

    def set_quality_slug(self, slug, persist=True):
        slug = str(slug)
        if slug not in QUALITY_LEVELS:
            slug = "auto"
        changed = slug != self._quality_slug
        self._quality_slug = slug
        self._sync_quality_ui()
        if changed:
            # 变更即存；S1/旧配置读的都是这三枚键，只读启动恢复用 load_player_prefs。
            player_settings().setValue(QUALITY_KEY, slug)
            self.qualityChanged.emit(slug)

    def _sync_quality_ui(self):
        self.overlay.quality_button.setText(
            QUALITY_LABELS.get(self._quality_slug, "自动"))

    # -------------------------------------------------------------- P1a 选集
    def set_episodes(self, episodes):
        """喂选集列表 list[dict{index?,title?,watched?,vip?}]，按播放顺序。"""
        self.dock.set_episodes(list(episodes or []))

    def set_current_episode(self, position):
        """0-based 序号：高亮落位；正在播放时顺手预热下一集（P1e）。"""
        try:
            pos = int(position)
        except (TypeError, ValueError):
            return
        if pos < 0:
            return
        self._episode_position = pos
        self._preload_requested_for = None
        self.dock.set_current(pos + 1)
        if self.is_playing():
            self._request_preload_next()

    def current_episode(self):
        return self._episode_position

    def set_dock_collapsed(self, collapsed):
        self.dock.set_collapsed(bool(collapsed))

    def _on_episode_clicked(self, index):
        # EpisodeGrid 回的是 1-based index；对外统一换 0-based position，
        # V1 直接把它接到 play_episode(drama, position, ...) 上。
        position = int(index) - 1
        if position < 0:
            return
        self.set_current_episode(position)
        self.episodeClicked.emit(position)

    # ------------------------------------------------------------ P1e 预缓冲
    def set_preload_hook(self, hook):
        """V1 接线口：hook(next_index) 里调 core_client.preload（只调不改）。"""
        self._preload_hook = hook if callable(hook) else None

    def _request_preload_next(self):
        position = self._episode_position
        if position is None:
            return
        next_index = position + 2          # 1-based 的下一集 index
        if next_index > self.dock.count():
            return
        if self._preload_requested_for == position:
            return
        self._preload_requested_for = position
        hook = self._preload_hook
        if hook is not None:
            try:
                hook(next_index)
            except Exception:
                pass                       # 预热失败绝不能打断当前播放
        self.preloadNextRequested.emit(next_index)

    # ------------------------------------------------------------ P1c 快捷键
    def _run_player_shortcut(self, handler):
        if self._player_shortcuts_allowed():
            handler()

    def _player_shortcuts_allowed(self):
        """播放页可见、焦点不在文本/选项类控件才放行：弹幕输入框（ QLineEdit/
        QPlainTextEdit，若将来出现）聚焦时，这些快捷键必须整体失效。空格/←→
        由 main.py 窗口级绑定（自带同款守卫），这里不重复注册。"""
        if not self.isVisible():
            return False
        focus = QApplication.focusWidget()
        if isinstance(focus, (QLineEdit, QTextEdit, QPlainTextEdit, QComboBox,
                              QAbstractSpinBox, QAbstractSlider)):
            return False
        return True

    # ----------------------------------------------------------------- hotkey help
    def show_hotkey_help(self):
        """打开快捷键说明浮层：铺满画面区、置顶并夺焦（Esc/? 优先被浮层吸收，
        不会顺带触发 main.py 的窗口级 Esc 退全屏）。"""
        self.hotkeys_overlay.sync_geometry()
        self.hotkeys_overlay.show()
        self.hotkeys_overlay.raise_()

    def toggle_hotkey_help(self):
        """⌨ 按钮与 ? 快捷键共用入口；输入控件（弹幕框等）聚焦时不弹出。"""
        if self.hotkeys_overlay.isVisible():
            self.hotkeys_overlay.hide()
            return
        if not self._player_shortcuts_allowed():
            return
        self.show_hotkey_help()

    # ----------------------------------------------------------------- danmaku
    def danmaku_enabled(self):
        return self._danmaku_enabled

    def danmaku_loaded(self):
        return self._danmaku_loaded

    def duration(self):
        """当前这一集的时长（秒）；mpv 还没报出来时为 0。"""
        return float(self._duration or 0.0)

    def position(self):
        """当前播放位置（秒）；弹幕按这个位置取窗口。"""
        return float(self._position or 0.0)

    def video_size(self):
        """当前视频的画面尺寸；竖屏短剧是 1080×1920，读不到就给 16:9。"""
        width = height = 0
        if self._session is not None:
            try:
                width = int(self._session.get("width", 0) or 0)
                height = int(self._session.get("height", 0) or 0)
            except (TypeError, ValueError):
                width = height = 0
        return (width if width > 0 else 1920, height if height > 0 else 1080)

    def set_danmaku_enabled(self, on):
        """同步开关状态（不触发抓取）：换集、启动时由主窗口调用。"""
        self._danmaku_enabled = bool(on)
        self.danmaku_button.blockSignals(True)
        self.danmaku_button.setChecked(self._danmaku_enabled)
        self.danmaku_button.blockSignals(False)
        self._apply_danmaku_visibility()

    def toggle_danmaku(self):
        self.danmaku_button.toggle()

    def _on_danmaku_button(self, checked):
        self._danmaku_enabled = bool(checked)
        # P1d：用户拨开关=变更即存；tools 侧由 main 的 danmakuToggled 处理器
        # 同步（main.py:1403），S1 以后读的就是这三枚键里的这一枚。
        player_settings().setValue(DANMAKU_KEY, self._danmaku_enabled)
        self._apply_danmaku_visibility()
        self.danmakuToggled.emit(self._danmaku_enabled)
        self.activity()          # 全屏下点按钮，把控制条亮出来

    def _apply_danmaku_visibility(self):
        if self._session is not None and self._danmaku_loaded:
            self._session.set_property("sub-visibility", "yes" if self._danmaku_enabled else "no")

    def reset_danmaku(self, note=""):
        """换集时清掉上一集的状态（新一集会有新的 mpv 进程）。"""
        self._danmaku_loaded = False
        self._danmaku_path = ""
        self._danmaku_count = 0
        self._danmaku_note = note
        self._sync_danmaku_tip()

    def attach_danmaku(self, path, count=0):
        """把生成好的 ASS 字幕挂到当前 mpv 会话上。"""
        self._danmaku_path = path
        self._danmaku_count = int(count or 0)
        self._danmaku_loaded = bool(path)
        self._danmaku_note = ""
        session = self._session
        if session is not None and path:
            session.command(["sub-add", path, "select", "弹幕"])
        self._apply_danmaku_visibility()
        self._sync_danmaku_tip()

    def reload_danmaku(self, count=None):
        """增量刷新：同一路径的 ASS 已经重写，让 mpv 重新读一遍。"""
        if count is not None:
            self._danmaku_count = int(count)
        if self._session is not None and self._danmaku_loaded:
            self._session.command(["sub-reload"])
        self._apply_danmaku_visibility()
        self._sync_danmaku_tip()

    def set_danmaku_note(self, note):
        """把「本集没有弹幕 / 加载失败」这类话放进按钮提示里。"""
        self._danmaku_note = str(note or "")
        self._sync_danmaku_tip()

    # ------------------------------------------------------- 弹幕透明度
    def danmaku_opacity(self):
        return int(self._danmaku_opacity)

    def _sync_opacity_button(self):
        self.opacity_button.setText("透明 %d%%" % self._danmaku_opacity)
        if self._opacity_value is not None:
            self._opacity_value.setText("%d%%" % self._danmaku_opacity)
        if self._opacity_slider is not None:
            self._opacity_slider.blockSignals(True)
            self._opacity_slider.setValue(self._danmaku_opacity)
            self._opacity_slider.blockSignals(False)

    def _show_opacity_popup(self):
        """点按钮弹出一个竖向滑条：往上更实、往下更透明。"""
        if self._opacity_popup is None:
            popup = QFrame(self, Qt.Popup)
            popup.setProperty("role", "playerPopup")
            popup.setAttribute(Qt.WA_StyledBackground, True)
            layout = QVBoxLayout(popup)
            layout.setContentsMargins(10, 8, 10, 8)
            layout.setSpacing(4)
            self._opacity_value = QLabel()
            self._opacity_value.setAlignment(Qt.AlignCenter)
            self._opacity_slider = QSlider(Qt.Vertical)
            self._opacity_slider.setRange(20, 100)
            self._opacity_slider.setSingleStep(5)
            self._opacity_slider.setPageStep(10)
            self._opacity_slider.setFixedHeight(108)
            self._opacity_slider.valueChanged.connect(self._on_opacity_slider)
            layout.addWidget(self._opacity_value, 0, Qt.AlignHCenter)
            layout.addWidget(self._opacity_slider, 0, Qt.AlignHCenter)
            self._opacity_popup = popup
        self._sync_opacity_button()
        self._opacity_popup.adjustSize()
        anchor = self.opacity_button.mapToGlobal(QPoint(0, 0))
        self._opacity_popup.move(
            anchor.x() - (self._opacity_popup.width() - self.opacity_button.width()) // 2,
            anchor.y() - self._opacity_popup.height() - 6)
        self._opacity_popup.show()
        self._opacity_popup.raise_()

    def _on_opacity_slider(self, value):
        self._danmaku_opacity = int(value)
        self._sync_opacity_button()
        self.danmakuOpacityChanged.emit(self._danmaku_opacity)
        self.activity()

    def set_danmaku_opacity(self, percent):
        """外部（主窗口）同步透明度，不发信号。"""
        try:
            value = int(percent)
        except (TypeError, ValueError):
            value = 100
        self._danmaku_opacity = min(100, max(20, value))
        self._sync_opacity_button()

    def _sync_danmaku_tip(self):
        parts = ["弹幕开关（D）"]
        if self._danmaku_loaded:
            parts.append("本集 %d 条" % self._danmaku_count)
        elif self._danmaku_note:
            parts.append(self._danmaku_note)
        self.danmaku_button.setToolTip(" · ".join(parts))

    def toggle_fullscreen(self):
        self.set_fullscreen(not self._fullscreen)
        self.fullscreenRequested.emit(self._fullscreen)

    def set_fullscreen(self, on):
        self._fullscreen = on
        self.full_button.setText("退出全屏" if on else "全屏")
        # P1a：全屏只留画面，停靠栏跟着收掉（退出后恢复折叠前的展开状态）
        self.dock.setVisible(not on)
        if on:
            self.overlay.hide()
            self._overlay_timer.stop()
            self.activity()          # 进全屏先亮着，5 秒不动再收
        else:
            self._controls_timer.stop()
            self._set_cursor_hidden(False)
            self.controls.show()

    # ------------------------------------------------- 全屏下的鼠标活动
    def _set_cursor_hidden(self, hidden):
        """全屏空闲时和控件一起把光标收掉。

        用 override cursor 而不是 widget 的 cursor：它由 Qt 直接 SetCursor，
        鼠标停在原地不动也能立刻生效（等下一次鼠标移动才换形状就太晚了）。
        """
        if hidden == self._cursor_hidden:
            return
        self._cursor_hidden = hidden
        if hidden:
            QApplication.setOverrideCursor(Qt.BlankCursor)
        else:
            QApplication.restoreOverrideCursor()

    def activity(self):
        """鼠标动了：把控制条和光标叫回来，并重新开始 5 秒倒计时。"""
        if not self._fullscreen:
            return
        self._set_cursor_hidden(False)
        if not self.controls.isVisible():
            self.controls.show()
        self._controls_timer.start()

    def _hide_controls(self):
        if not self._fullscreen:
            return
        # 鼠标还停在控制条上（正在拖进度条、点倍速）就先别藏，重新计时
        if self.controls.rect().contains(self.controls.mapFromGlobal(QCursor.pos())):
            self._controls_timer.start()
            return
        self.controls.hide()
        self._set_cursor_hidden(True)

    def mouseMoveEvent(self, event):
        self.activity()
        super().mouseMoveEvent(event)

    def stop(self, restore_window=True, blocking=False):
        self._stopping = True
        # Abandon any launch that is still in flight.
        self._start_token = None
        self._starting = False
        session = self._session
        self._session = None
        if session is not None:
            session.stop(blocking=blocking)
        self._stopping = False
        self._pause_intent = False
        self._duration = 0.0
        self._position = 0.0
        self.position_slider.setRange(0, 0)
        self.time_label.setText("00:00 / 00:00")
        self.play_button.setText("▶")
        self._controls_timer.stop()
        self.controls.show()
        self._switch_pending = False
        self._poster_swap.stop()
        if restore_window:
            # 用户主动停止：别让下一部剧还铺着上一部的画面
            self._poster_poll.stop()
            self._discard_poster_file()
            self._poster_image = None
        self._hide_poster()
        self._set_surface(False)
        self._set_status("选择分集后点击播放")
        # P1b/P1e：停播时收掉悬停控制条，并允许下一集重新预热
        self.overlay.hide()
        self._overlay_timer.stop()
        self._preload_requested_for = None
        self.reset_danmaku()
        self.pauseChanged.emit(False)
        # A user-initiated stop while fullscreen must bring the window frame
        # back, or the title bar stays hidden and the window cannot minimize.
        if restore_window and self._fullscreen:
            self.set_fullscreen(False)
            self.fullscreenRequested.emit(False)

    def _adopt_start(self):
        """Adopt (or reject) whatever the launch thread produced."""
        while True:
            try:
                request, session, title, exc = self._start_results.get_nowait()
            except queue.Empty:
                return
            if exc is not None:
                self._start_failed(request, exc)
            else:
                self._start_ready(request, session, title)

    def _drain(self):
        """Pump queued mpv events on the GUI thread (never the reader thread)."""
        self._adopt_start()
        session = self._session
        if session is None:
            return
        session.drain_events()

    def _tick(self):
        if not self.is_playing():
            return
        now = time.time()
        if now - self._last_tick < 0.2:
            return
        self._last_tick = now
        duration = self._session.get("duration", 0.0) or 0.0
        try:
            duration = float(duration)
        except Exception:
            duration = 0.0
        if duration > 0 and abs(duration - self._duration) > 0.4:
            self._duration = duration
            self.position_slider.setRange(0, int(duration))
        position = self._session.get("time-pos", 0.0)
        try:
            position = float(position or 0.0)
        except Exception:
            position = 0.0
        self._position = position
        if not self._seeking:
            self.position_slider.setValue(int(min(position, self._duration or position)))
        self.time_label.setText("%s / %s" % (format_time(position), format_time(self._duration)))
        self.positionChanged.emit(position, self._duration)
        if not self._eof and not self._awaiting_file_load and self._session.cached("eof-reached", False):
            # keep-open=yes 下 mpv 不一定发 end-file；缓存兜底也发一次。
            self._eof = True
            QTimer.singleShot(0, self.finished.emit)

    def _on_slider_press(self):
        self._seeking = True

    def _on_slider_move(self, value):
        self.time_label.setText("%s / %s" % (format_time(value), format_time(self._duration)))

    def _on_slider_release(self):
        self._seeking = False
        if self.is_playing():
            self._session.command(["seek", float(self.position_slider.value()), "absolute+exact"])
        self._tick()
