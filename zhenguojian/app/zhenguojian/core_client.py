"""IPC client for the bundled native core (duanjuapp/native)."""
from __future__ import annotations

import itertools
import json
import os
import re
import subprocess
import sys
import threading
import time
from datetime import datetime

from . import tools


def _retry_hint(value):
    """把核心返回的 retryAt（RFC3339）显示成 HH:MM；解析不了就退回“稍后”。"""
    text = str(value or "")
    if not text:
        return "稍后"
    try:
        return datetime.fromisoformat(text.replace("Z", "+00:00")).astimezone().strftime("%H:%M")
    except ValueError:
        match = re.search(r"T(\d{2}:\d{2})", text)
        return match.group(1) if match else "稍后"


class CoreError(RuntimeError):
    def __init__(self, message, code=""):
        super().__init__(message)
        self.message = message
        self.code = code

    @property
    def cancelled(self):
        return self.message == "请求已取消"


class _Result:
    __slots__ = ("event", "value", "error")

    def __init__(self):
        self.event = threading.Event()
        self.value = None
        self.error = None


class CoreClient:
    """Drives the native core over stdin/stdout JSON lines with id matching."""

    def __init__(self, data_dir):
        self.data_dir = data_dir
        self._process = None
        self._lock = threading.Lock()
        self._write_lock = threading.Lock()
        self._pending = {}
        self._sequence = itertools.count(1)
        self._owner = "hg%d" % (int(time.time() * 1000) % 100000000)
        self._alive = False
        self._reader = None
        self._stderr = []
        self._recommend_page = 0
        self._recommend_generation = 0
        self.version = ""
        self._closing = False
        self._restarting = False
        self._restart_lock = threading.Lock()
        self._last_restart = 0.0
        # 自动重启成功后通知界面（由 MainWindow 注入；可能来自工作线程）。
        self.on_restart = None

    # ------------------------------------------------------------------ setup
    def start(self, timeout=60.0):
        binary = os.path.join(tools.app_root(), "runtime", "sidecar.exe")
        if not os.path.isfile(binary):
            raise CoreError("缺少本地核心组件 sidecar.exe")
        os.makedirs(self.data_dir, exist_ok=True)
        self._point_downloads_at_app_folder()
        flags = getattr(subprocess, "CREATE_NO_WINDOW", 0) if sys.platform == "win32" else 0
        self._process = subprocess.Popen(
            [binary, self.data_dir],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            creationflags=flags,
        )
        self._alive = True
        self._reader = threading.Thread(target=self._read_loop, args=(self._process,), daemon=True)
        self._reader.start()
        threading.Thread(target=self._drain_stderr, args=(self._process,), daemon=True).start()
        info = self.call("initialize", directory=self.data_dir, timeout=timeout)
        self.version = (info or {}).get("version", "")
        return info

    def _point_downloads_at_app_folder(self):
        """Keep downloads on the app drive instead of %LOCALAPPDATA%.

        The core reads ``download-location.json`` from its data directory when
        it opens the download root, so writing it before the process launches
        is enough.  Episodes land in <app folder>/下载.
        """
        target = tools.core_cache_dir()
        marker = os.path.join(self.data_dir, "download-location.json")
        try:
            os.makedirs(target, exist_ok=True)
            with open(marker, "w", encoding="utf-8") as handle:
                json.dump({"directory": target}, handle)
        except OSError as exc:
            raise CoreError("无法创建下载目录（%s）：%s" % (target, exc))

    def _drain_stderr(self, process):
        stream = process.stderr
        while stream is not None:
            try:
                line = stream.readline()
            except Exception:
                break
            if not line:
                break
            text = line.decode("utf-8", "replace").strip()
            if text:
                self._stderr.append(text)
                del self._stderr[:-40]
                # 核心崩溃/被杀时 stderr 是唯一线索，不能只留在内存里。
                tools.log_text("[sidecar] " + text)

    # ------------------------------------------------------------- transports
    def _read_loop(self, process):
        stream = process.stdout
        while True:
            try:
                line = stream.readline()
            except Exception:
                break
            if not line:
                break
            try:
                payload = json.loads(line.decode("utf-8", "replace"))
            except Exception:
                continue
            identifier = payload.pop("id", None)
            with self._lock:
                waiter = self._pending.pop(identifier, None)
            if waiter is None:
                continue
            waiter.value = payload
            waiter.event.set()
        # Only the current process may declare the client dead; a reader from
        # a replaced process still draining must not clobber a fresh restart.
        if self._process is not process:
            return
        if not self._closing:
            tools.log_text("本地核心进程已退出（exit=%s）" % process.poll())
        self._alive = False
        with self._lock:
            waiters = list(self._pending.values())
            self._pending.clear()
        for waiter in waiters:
            waiter.error = CoreError("本地核心已退出，正在尝试恢复")
            waiter.event.set()

    def call(self, action, timeout=180.0, **payload):
        try:
            return self._call_once(action, timeout, **payload)
        except CoreError as exc:
            if self._closing or self._restarting or not self._dead_error(exc):
                raise
            self._restart_once()
            return self._call_once(action, timeout, **payload)

    @staticmethod
    def _dead_error(exc):
        return any(word in exc.message for word in ("尚未启动", "已退出", "通信失败"))

    def _restart_once(self):
        """Bring sidecar.exe back after it died, once, with a short cooldown."""
        with self._restart_lock:
            if self._closing:
                raise CoreError("本地核心已退出，请重启应用")
            if self._alive and self._process is not None and self._process.poll() is None:
                return
            if time.time() - self._last_restart < 3.0:
                raise CoreError("本地核心反复退出，请重启应用")
            self._last_restart = time.time()
            detail = "；".join(self._stderr[-5:])
            tools.log_text("本地核心已退出，正在自动重启%s"
                           % (("（%s）" % detail) if detail else ""))
            self._terminate_process()
            self._restarting = True
            try:
                self.start(timeout=60.0)
            except Exception as exc:
                self._terminate_process()
                raise CoreError("本地核心重启失败：%s" % exc)
            finally:
                self._restarting = False
            if self.on_restart is not None:
                try:
                    self.on_restart()
                except Exception:
                    pass

    def _terminate_process(self):
        """Stop the current child and fail every request still waiting on it."""
        process, self._process = self._process, None
        self._alive = False
        with self._lock:
            waiters = list(self._pending.values())
            self._pending.clear()
        for waiter in waiters:
            waiter.error = CoreError("本地核心正在重启")
            waiter.event.set()
        if process is not None:
            try:
                if process.stdin:
                    process.stdin.close()
            except Exception:
                pass
            if process.poll() is None:
                try:
                    process.terminate()
                    process.wait(timeout=3)
                except Exception:
                    try:
                        process.kill()
                    except Exception:
                        pass
        self._stderr = []

    def _call_once(self, action, timeout=180.0, **payload):
        if not self._alive or self._process is None or self._process.poll() is not None:
            raise CoreError("本地核心尚未启动")
        payload = dict(payload)
        payload["action"] = action
        if action not in ("initialize", "cancelRead", "release", "cancelPlayback", "controlDownloads",
                          "workLease", "controlDownloadBatch"):
            payload.setdefault("session", "%s:%s" % (self._owner, action))
            payload.setdefault("sequence", next(self._sequence))
        waiter = _Result()
        identifier = "%s:%d" % (self._owner, next(self._sequence))
        with self._lock:
            self._pending[identifier] = waiter
        body = json.dumps(dict(payload, id=identifier), ensure_ascii=False).encode("utf-8")
        try:
            # Several worker threads may talk to the core at once; keep whole
            # requests together so two JSON lines can never interleave.
            with self._write_lock:
                self._process.stdin.write(body + b"\n")
                self._process.stdin.flush()
        except Exception as exc:
            with self._lock:
                self._pending.pop(identifier, None)
            raise CoreError("本地核心通信失败：%s" % exc)
        if not waiter.event.wait(timeout):
            with self._lock:
                self._pending.pop(identifier, None)
            raise CoreError("本地核心响应超时（%s）" % action)
        if waiter.error:
            raise waiter.error
        envelope = waiter.value or {}
        if not envelope.get("ok"):
            message = envelope.get("error") or "本地核心处理失败"
            code = envelope.get("code", "")
            if code == "source_blocked":
                message = "%s（源被限流，预计 %s 后恢复）" % (message, _retry_hint(envelope.get("retryAt")))
            raise CoreError(message, code)
        return envelope.get("data")

    # ------------------------------------------------------------- operations
    def boards(self):
        return (self.call("rankingBoards") or {}).get("items", [])

    def recommendations(self, category="", force=False, more=False, lane="library"):
        """Personalised picks for one category.

        The native core only accepts a concrete category here; the "全部" tab
        is served by the paged catalog exactly like the reference Flutter app
        does (home_screen.dart maps `_recommendationCategory` to
        `recommendations()` and every other entry to `catalog()`).
        """
        if not category:
            # The catalog is a real page-number API, so "more" simply walks
            # the page counter forward.  Re-requesting the page we are
            # already on is what made 下一页 look broken: it re-sent page 1
            # every time.
            with self._lock:
                generation = self._recommend_generation
                page = max(1, self._recommend_page) + 1 if more else 1
            result = self.catalog(query="", category="", page=page,
                                  force=force, lane=lane)
            with self._lock:
                if generation == self._recommend_generation:
                    self._recommend_page = page
            return result
        return self.call("recommendations", category=category, force=force,
                         command="more" if more else "")

    def calendar(self, date="", offset=0, gender=""):
        """红果上新日历：date 为 YYYYMMDD，空字符串表示今天；offset 用于翻页。

        gender：2=全部、1=男频、0=女频（和红果客户端的 gender_type 一致）。
        """
        payload = {"date": date, "offset": int(offset or 0)}
        if gender:
            payload["gender"] = gender
        return self.call("calendar", **payload, timeout=30.0)

    def new_series(self, gender="2", offset=0, cursor=""):
        """红果「新剧推荐」：按上线时间，18 条一页。

        gender：2=全部、1=男频、0=女频。cursor 是上一页返回的 sessionId，
        首屏不传；分页时 offset 用 nextOffset。
        """
        return self.call("newSeries", gender=str(gender or "2"),
                         offset=int(offset or 0), cursor=cursor or "",
                         timeout=40.0)

    def reset_recommendation_page(self):
        """Forget the catalog page counter (换一批 / 分类切换后调用)."""
        with self._lock:
            self._recommend_generation += 1
            self._recommend_page = 0

    def rankings(self, board, page=1, force=False):
        return self.call("rankings", board=board, page=page, force=force)

    def browse_themes(self):
        """网页分类页的「类型 → 题材」树（真人剧 24 个、漫剧/AI剧各 8 个）。"""
        return (self.call("browseThemes", timeout=20.0) or {}).get("items", [])

    def browse(self, route="real-drama", theme="", page=1):
        """一页分类结果；route 是 real-drama/comic-drama/ai-drama。"""
        return self.call("browse", route=route, theme=theme, page=int(page or 1),
                         timeout=60.0)

    def suggestions(self, query):
        return (self.call("suggestions", query=query, timeout=20.0) or {}).get("items", [])

    def catalog(self, query="", category="", page=1, source="hongguo", force=False, lane=""):
        """One catalog page, on its own read lane when one is named.

        The core cancels an in-flight read as soon as a newer request of the
        same session arrives, so 剧库 and 搜索 must not share a session: a
        search used to kill the loading library feed (and the resulting
        "context canceled" surfaced as 加载失败).  Inside one lane the
        cancellation is still wanted - a newer search supersedes an older
        one - which is why the window also guards its callbacks by request.
        """
        payload = {"source": source, "query": query, "category": category,
                   "page": page, "force": force}
        if lane:
            payload["session"] = "%s:catalog:%s" % (self._owner, lane)
        return self.call("catalog", **payload)

    def drama_creators(self, drama):
        return self.call("dramaCreators", drama=drama, timeout=45.0)

    def list_sources(self):
        """全部站源描述（核心 provider_registry 单一事实源）。

        每项含 slug/name/enabled（M1/S1 契约）以及核心附带的
        displayName/searchKind/lanes/seasons/catalogKinds/enabledByDefault/
        implemented 字段；enabled 缺省时按 True 处理。"""
        items = (self.call("listSources", timeout=15.0) or {}).get("items", [])
        view = []
        for item in items:
            row = dict(item)
            row.setdefault("enabled", True)
            if not row.get("name"):
                row["name"] = row.get("displayName") or row.get("slug", "")
            view.append(row)
        return view

    def set_source_enabled(self, slug, enabled):
        """开关某个站源；核心写入 data 目录 source_prefs.json 后立即生效。"""
        return self.call("setSourceEnabled", slug=slug, enabled=bool(enabled), timeout=15.0)

    def sorani_account(self, command="", identifier="", password="", captcha_id="", track=None):
        """青空次元账号（T1，行协议 soraniAccount / provider_sorani_account.go）。

        command：""＝查询登录态 {"loggedIn","name"}；"captcha"＝取滑块验证
        码；"login"＝提交登录（须带 identifier/password/captcha_id/track）；
        "logout"＝清会话。

        注意：登录入参必须嵌套在 ``account`` 键下——app_runtime.go 的
        nativeInput 只读 ``input.Account``（tag ``json:"account"``），顶层
        平铺会被静默丢掉、永远报「请输入用户名或邮箱」。
        track 为 sorani_login.SliderCaptcha.solved 上报的 dict，字段名即
        provider_sorani_account.go 的 json tag（x/y/type/t + 尺寸 + 起止毫秒），
        原样走 json，不做任何加工。"""
        payload = {"command": command or ""}
        if (command or "") == "login":
            payload["account"] = {
                "identifier": identifier or "",
                "password": password or "",
                "captchaId": captcha_id or "",
                "track": track or {},
            }
        return self.call("soraniAccount", timeout=60.0, **payload)

    def creator_profile(self, creator):
        return self.call("creatorProfile", creator=creator, timeout=45.0)

    def creator_works(self, creator, tab=8, sub_tab=100, cursor="", sort="time_desc"):
        return self.call("creatorWorks", creator=creator, tab=tab, subTab=sub_tab,
                         cursor=cursor, sort=sort, timeout=45.0)

    def browse_filters(self, genre=""):
        return self.call("browseFilters", category=genre, timeout=45.0)

    def browse_filtered(self, filters, offset=0, cursor=""):
        return self.call("browseFiltered", filters=filters, offset=offset, cursor=cursor, timeout=45.0)

    def detail(self, drama):
        return self.call("detail", drama=drama)

    def poster_metadata(self, dramas):
        return self.call("posterMetadata", dramas=list(dramas), timeout=45.0)

    def metadata(self, drama, lane=""):
        payload = {"drama": drama}
        if lane:
            payload["session"] = "%s:metadata:%s" % (self._owner, lane)
        return self.call("metadata", **payload)

    def cover(self, drama, force=False):
        if str(drama.get("id") or "").startswith("creator:"):
            drama = dict(drama, id="hongguo:avatar:" + drama["id"].split(":", 1)[1], source="hongguo")
        return self.call("cover", drama=drama, force=force, timeout=60.0)

    def resolve(self, drama, chapter, index, quality=0):
        return self.call("resolve", drama=drama, chapter=chapter, index=index, quality=quality)

    def preload(self, drama, chapter, index, quality=0):
        """解析并预热下一集：核心会把片头几 MB 拉进内存，切集时直接复用。

        返回的 plan 和 resolve 的一样，另外带 prefetchedBytes / expiresAt。
        """
        return self.call("preload", drama=drama, chapter=chapter, index=index,
                         quality=quality, timeout=30.0)

    def chapter_quality(self, drama, chapters):
        """这几集下下来是什么画质。核心内部按接口上限切批并发，一次 15 集。

        返回 ``{videoUrl 里的视频 ID: {definition,width,height,bitrate,quality,playable}}``。
        只用红果源自带这个信息，其它源返回空字典。
        """
        result = self.call("chapterQuality", drama=drama, chapters=list(chapters), timeout=60.0)
        return (result or {}).get("items") or {}

    def fallback(self, session):
        return self.call("fallback", session=session, playbackSession=session)

    def release(self, session):
        try:
            self.call("release", session=session, playbackSession=session, timeout=10.0)
        except Exception:
            pass

    def enqueue(self, drama, entries, quality=0):
        return self.call("enqueueDownloads", drama=drama, entries=entries, quality=quality)

    def downloads(self):
        return (self.call("downloads", timeout=30.0) or {}).get("jobs", [])

    def control(self, job_id, command):
        return self.call("controlDownloads", jobId=job_id, command=command, timeout=30.0)

    def remove_downloads(self, job_ids):
        """批量删除任务：核心在锁内删完再统一落盘，避免逐集重写整份索引。"""
        return self.call("removeDownloads", jobIds=list(job_ids), timeout=600.0) or {}

    def local_plan(self, drama_id, index):
        try:
            return self.call("localPlayback", drama={"id": drama_id, "source": "hongguo"},
                             index=index, timeout=30.0)
        except CoreError as exc:
            if exc.code == "local_media":
                return None
            raise

    def download_directory(self):
        return (self.call("downloadDirectory") or {}).get("directory") or tools.download_dir()

    def cancel(self, scope, sequence):
        try:
            self.call("cancelRead", session="%s:%s" % (self._owner, scope), sequence=sequence, timeout=10.0)
        except Exception:
            pass

    def close(self):
        self._closing = True
        self._terminate_process()
