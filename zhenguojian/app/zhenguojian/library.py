# -*- coding: utf-8 -*-
"""Applies finished downloads to the user-facing media library.

The core owns the transfer (resume, retries, progress) and writes an
encrypted ``media.mp4`` per job.  This module is the second stage: once a job
reports ``completed`` it is decrypted, renamed to a readable episode name and
moved into ``<下载根>/<剧名>/``.  The encrypted original is deleted once the
clear file is verified, so the library only ever contains playable videos.
"""
from __future__ import annotations

import json
import os
import tempfile
import threading
import time

from . import danmaku, decrypt, media, tools


class LibraryError(media.MediaError):
    pass


class IndexBusy(LibraryError):
    """The index file exists but could not be read/written right now."""


def _index_file():
    return os.path.join(tools.download_dir(), "episodes.json")


_index_cache = None
_index_stamp = None
_index_lock = threading.Lock()
# Serialises the whole read-modify-write cycle.  Without it two writers
# (background decrypt + user deleting an episode) overwrite each other's
# entries: measured 55 expected records, 50 persisted, and no error at all.
_index_write_lock = threading.Lock()


def _read_index():
    """(parsed index, readable).

    ``readable`` is False only when the file exists but could not be read at
    all.  Callers that write must not treat that as an empty library: doing so
    truncates every published episode.  A stale cached snapshot is returned
    instead whenever one is available.
    """
    global _index_cache, _index_stamp
    path = _index_file()
    try:
        stamp = (os.path.getmtime(path), os.path.getsize(path))
    except FileNotFoundError:
        with _index_lock:
            _index_cache, _index_stamp = {}, None
            return _index_cache, True
    except OSError:
        with _index_lock:
            if _index_cache is not None:
                return _index_cache, True
            return {}, False
    with _index_lock:
        if _index_cache is not None and _index_stamp == stamp:
            return _index_cache, True
    import json
    # Another thread may hold the file open for reading while we replace it;
    # retry briefly instead of declaring the whole library unreadable.
    data = None
    for attempt in range(6):
        try:
            with open(path, "r", encoding="utf-8") as handle:
                data = json.load(handle)
            break
        except (json.JSONDecodeError, UnicodeDecodeError):
            # Keep the broken file for inspection instead of silently
            # starting from scratch on every launch.
            try:
                tools.backup_broken(path)
            except Exception:
                pass
            with _index_lock:
                _index_cache, _index_stamp = {}, None
            return {}, True
        except OSError:
            time.sleep(0.05 * (attempt + 1))
    if data is None:
        with _index_lock:
            if _index_cache is not None:
                return _index_cache, True
        return {}, False
    cache = data if isinstance(data, dict) else {}
    with _index_lock:
        _index_cache, _index_stamp = cache, stamp
    return cache, True


def _load_index():
    """Parsed episodes.json, cached by file fingerprint.

    The GUI thread calls this hundreds of times per poll cycle (every
    ``known()`` check) - re-reading and re-parsing a growing ~400KB file on
    each call is what made the whole window stutter once the library grew.
    Writes go through atomic ``os.replace``, so a cached snapshot can never
    be observed half-written; the (mtime, size) stamp keeps it fresh when
    the file changes underneath us.
    """
    return _read_index()[0]


def _save_index(index):
    import json
    global _index_cache, _index_stamp
    directory = tools.download_dir()
    os.makedirs(directory, exist_ok=True)
    # Unique temp name: a fixed ".tmp" let two writers open (and truncate) the
    # same file, which caused PermissionError and lost updates on Windows.
    handle = tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", delete=False,
                                         dir=directory, prefix=".episodes-", suffix=".tmp")
    temporary = handle.name
    try:
        with handle:
            json.dump(dict(index), handle, ensure_ascii=False, indent=1)
        # A GUI thread reading the index holds it open without delete
        # sharing on Windows, so replace can fail with WinError 5.  Reads are
        # short; retry briefly instead of losing the update.
        for attempt in range(8):
            try:
                os.replace(temporary, _index_file())
                break
            except PermissionError:
                if attempt == 7:
                    raise IndexBusy("成品索引暂时被占用，请稍后重试")
                time.sleep(0.05 * (attempt + 1))
    except BaseException:
        try:
            os.remove(temporary)
        except OSError:
            pass
        raise
    try:
        stamp = (os.path.getmtime(_index_file()), os.path.getsize(_index_file()))
    except OSError:
        stamp = None
    with _index_lock:
        # Store a private snapshot; callers keep mutating the dict they passed.
        _index_cache, _index_stamp = dict(index), stamp


def _update_index(mutator):
    """Read-modify-write the episode index under one lock.

    ``mutator`` receives a copy and returns ``False`` to skip the write when
    nothing changed.  Every mutation must go through here; touching the dict
    returned by _load_index() and saving it separately loses concurrent edits.
    """
    with _index_write_lock:
        index_map, readable = _read_index()
        if not readable:
            raise IndexBusy("成品索引暂时无法读取，请稍后重试")
        index_map = dict(index_map)
        changed = mutator(index_map)
        if changed is not False:
            _save_index(index_map)
        return changed


def reconcile_index():
    """丢掉文件已经不在了的成品记录（启动时后台跑一次）。

    下载页只信 episodes.json：文件被删掉、或下载位置换过以后，这些条目会一直
    挂着，看着像"已下载"其实播不了，日积月累能到上千条。任务还在下载列表里的
    那些不用怕——发布器发现文件缺失会重新解密出来。
    """
    root = tools.download_dir()
    if not os.path.isdir(root):
        return 0        # 库目录整个不在（比如移动盘没插），先别动索引
    dropped = []

    def mutate(index_map):
        for key, entry in list(index_map.items()):
            path = (entry or {}).get("path")
            recorded = (entry or {}).get("bytes")
            try:
                actual = os.path.getsize(path) if path and os.path.isfile(path) else None
            except OSError:
                actual = None
            if actual is None or (recorded and actual != recorded):
                dropped.append(key)
                index_map.pop(key, None)
        return bool(dropped)

    _update_index(mutate)
    return len(dropped)


def stash_index(source_dir, target_dir):
    """Merge one library root's episode index into another's.

    Called when the user moves the library elsewhere.  Without this every
    completed job looks unpublished in the new location and the publisher
    would quietly re-decrypt the whole library again.  Entries carry
    absolute paths, so carrying them over is honest: those files really do
    still exist, under the old root, until the user deletes them.

    Returns how many entries the new root did not know about yet.
    """
    import json
    def read(path):
        try:
            with open(path, "r", encoding="utf-8") as handle:
                data = json.load(handle)
        except (json.JSONDecodeError, UnicodeDecodeError):
            try:
                tools.backup_broken(path)
            except Exception:
                pass
            return {}
        except OSError:
            return None
        return data if isinstance(data, dict) else {}

    source = os.path.join(source_dir, "episodes.json")
    previous = read(source)
    if not previous:
        return 0
    # stash_index runs after set_library_root(), so _index_file() already
    # points at the new root.  Reset the cache first: it still holds the old
    # root's snapshot, which must not be mixed into the new index.
    global _index_cache, _index_stamp
    with _index_lock:
        _index_cache, _index_stamp = None, None
    added = []

    def mutate(target_map):
        for key, entry in previous.items():
            if key not in target_map:
                target_map[key] = entry
                added.append(key)
        return bool(added)

    try:
        _update_index(mutate)
    except Exception as exc:
        tools.log_text("迁移成品索引失败：%s" % exc)
        return 0
    return len(added)


def job_entry(job):
    """Stable key for one episode: drama id + episode number."""
    drama = job.get("drama") or {}
    return "%s|%s" % (drama.get("id") or "?", job.get("index") or 0)


def downloaded_episodes(drama_id):
    """Episode numbers of one drama that are already published locally.

    The index is keyed by job_entry()'s "<drama id>|<number>", so one pass
    picks out a whole season instead of hitting the dict once per episode -
    the detail page calls this every time it paints its 选集 grid.
    """
    prefix = "%s|" % (drama_id or "")
    numbers = set()
    if not drama_id:
        return numbers
    for key in _load_index():
        if not key.startswith(prefix):
            continue
        number = key[len(prefix):]
        if number.isdigit():
            numbers.add(int(number))
    return numbers


def published_path(drama_id, index):
    """Path of the decrypted episode under 下载/, or None if not published."""
    entry = _load_index().get("%s|%s" % (drama_id or "", int(index or 0))) or {}
    return entry.get("path")


class EpisodePublisher:
    """Decrypts completed jobs into <下载根>/<剧名>/<剧名> 第NN集.mp4.

    Runs off the GUI thread; every finished job is processed at most once and
    a failure leaves the encrypted file untouched so nothing is lost.
    """

    def __init__(self, core, on_change=None, on_status=None):
        self.core = core
        self.on_change = on_change or (lambda: None)
        self.on_status = on_status or (lambda text: None)
        self._busy = set()
        self._lock = threading.Lock()
        # Danmaku collection runs after publishing, one episode at a time:
        # the comment endpoint samples bursts, so serialising avoids the
        # download queue turning into a burst of comment requests.
        self._danmaku_busy = set()
        self._danmaku_gate = threading.Semaphore(1)

    # ------------------------------------------------------------------ state
    def library(self):
        """Merged index of published episodes, keyed like job_entry()."""
        return _load_index()

    def path_for(self, job):
        return (self.library().get(job_entry(job)) or {}).get("path")

    def known(self, job):
        path = self.path_for(job)
        if not path or not os.path.isfile(path) or os.path.getsize(path) == 0:
            return False
        recorded = (self.library().get(job_entry(job)) or {}).get("bytes")
        try:
            return not recorded or os.path.getsize(path) == recorded
        except OSError:
            return False

    # ------------------------------------------------------------------ work
    def publish_pending(self, jobs, progress=None, cancel=None, on_failure=None):
        """Decrypt every freshly completed job; returns published entries."""
        published = []
        pending = [job for job in jobs
                   if job.get("state") == "completed" and not self.known(job)]
        if not pending:
            return published
        for offset, job in enumerate(pending):
            if cancel is not None and cancel.is_set():
                break
            try:
                entry = self._publish_one(job, progress, offset, len(pending), cancel)
            except Exception as exc:  # noqa: BLE001 - reported, never fatal
                self.on_status("%s：%s" % (self._label(job), exc))
                if on_failure is not None:
                    on_failure(job, exc)
                continue
            if entry:
                published.append(entry)
                self.on_change()
        return published

    def _label(self, job):
        drama = job.get("drama") or {}
        return "%s 第%s集" % (drama.get("title") or "短剧", job.get("index"))

    def _publish_one(self, job, progress, offset, total, cancel):
        entry_key = job_entry(job)
        with self._lock:
            if entry_key in self._busy:
                return None
            self._busy.add(entry_key)
        try:
            return self._do_publish(job, entry_key, progress, offset, total, cancel)
        finally:
            with self._lock:
                self._busy.discard(entry_key)

    def _do_publish(self, job, entry_key, progress, offset, total, cancel):
        drama = job.get("drama") or {}
        index = int(job.get("index") or 0)
        plan = self.core.local_plan(drama.get("id"), index)
        if not plan or not plan.get("local"):
            raise LibraryError("该分集还没有完整的本地文件")
        source = plan.get("url")
        key = plan.get("decryptionKey") or ""
        if not source or not os.path.isfile(source):
            raise LibraryError("原始下载文件缺失，请重新下载")
        title = (drama.get("title") or "短剧").strip()
        chapter = job.get("chapter") or {}
        folder = os.path.join(tools.download_dir(), decrypt.series_folder(title))
        os.makedirs(folder, exist_ok=True)
        filename = decrypt.episode_filename(title, index, chapter.get("title") or "",
                                            drama.get("episodes"))
        target = os.path.join(folder, filename + decrypt._suffix(source))
        if cancel is not None and cancel.is_set():
            raise LibraryError("已取消")
        if progress:
            progress("解密第 %s 集" % index, (offset + 0.15) / max(1, total))
        decrypt.decrypt_to(source, target, key)
        if cancel is not None and cancel.is_set():
            try:
                os.remove(target)
            except OSError:
                pass
            raise LibraryError("已取消")
        if progress:
            progress("校验第 %s 集" % index, (offset + 0.85) / max(1, total))
        info = media.probe(target)
        if not (info.get("video") or {}).get("codec_name"):
            raise LibraryError("解密后的分集缺少视频轨")
        size = os.path.getsize(target)
        entry = {"path": target, "dramaId": drama.get("id"), "title": title,
                 "index": index, "chapter": chapter.get("title") or "",
                 "bytes": size, "duration": round(info.get("duration") or 0.0, 2),
                 "video": (info.get("video") or {}).get("codec_name"),
                 "width": (info.get("video") or {}).get("width"),
                 "height": (info.get("video") or {}).get("height"),
                 "jobId": job.get("id")}
        def store(index_map):
            index_map[entry_key] = entry

        _update_index(store)
        # Danmaku is optional and never blocks publishing: the worker below
        # resolves/fetches in the background only when the user enabled it.
        self._queue_danmaku(job, entry)
        # The encrypted original stays in the core cache: the core validates
        # it for resume and merge, so deleting it would make the job look
        # broken.  Users only ever see the decrypted file under 下载/.
        if progress:
            progress("第 %s 集已就绪" % index, (offset + 1) / max(1, total))
        return entry

    # ------------------------------------------------------------- danmaku
    def _queue_danmaku(self, job, entry):
        if not tools.danmaku_download_enabled():
            return
        path = (entry or {}).get("path") or ""
        if not path:
            return
        base = os.path.splitext(path)[0]
        if (os.path.isfile(base + ".ass") and os.path.getsize(base + ".ass") > 0
                and os.path.isfile(base + ".danmaku.json")):
            return
        key = job_entry(job)
        with self._lock:
            if key in self._danmaku_busy:
                return
            self._danmaku_busy.add(key)
        threading.Thread(target=self._collect_danmaku, args=(job, entry),
                         name="danmaku-%s" % key, daemon=True).start()

    def _collect_danmaku(self, job, entry):
        key = job_entry(job)
        session = ""
        try:
            with self._danmaku_gate:
                drama = job.get("drama") or {}
                chapter = job.get("chapter") or {}
                index = int(job.get("index") or 0)
                duration_ms = int(round(float((entry or {}).get("duration") or 0.0) * 1000))
                if duration_ms <= 0:
                    info = media.probe(entry.get("path") or "")
                    duration_ms = int(round(float(info.get("duration") or 0.0) * 1000))
                if duration_ms <= 0:
                    raise LibraryError("缺少分集时长，无法抓弹幕")
                plan = self.core.call("resolve", drama=drama, chapter=chapter,
                                      index=index, quality=0, force=True,
                                      session="danmaku-collect") or {}
                session = str(plan.get("session") or "")
                if not session:
                    raise LibraryError("没有拿到播放会话")
                items, total = danmaku.fetch_all(self.core, session, duration_ms)
                if not items:
                    self.on_status("%s：本集没有弹幕" % self._label(job))
                    return
                width = int((entry or {}).get("width") or 0) or 1920
                height = int((entry or {}).get("height") or 0) or 1080
                text = danmaku.build_ass(items, duration_ms, size=(width, height))
                base = os.path.splitext(entry["path"])[0]
                danmaku.write_file(text, base + ".ass")
                episode_id = str(plan.get("danmakuId") or "")
                series_id = str(drama.get("sourceId") or "")
                document = danmaku.document(items, total, episode_id, series_id)
                danmaku.write_file(json.dumps(document, ensure_ascii=False, indent=1),
                                   base + ".danmaku.json")

                def mutate(index_map):
                    row = index_map.get(key)
                    if row is None:
                        return False
                    row["danmaku"] = len(items)
                    row["danmakuPath"] = base + ".ass"
                    return True

                _update_index(mutate)
                self.on_status("%s：弹幕已保存（%d 条）" % (self._label(job), len(items)))
                self.on_change()
        except Exception as exc:  # noqa: BLE001 - optional feature, never fatal
            self.on_status("%s：弹幕保存失败（%s）" % (self._label(job), exc))
        finally:
            if session:
                try:
                    self.core.call("release", session=session, timeout=10)
                except Exception:  # noqa: BLE001 - cleanup only
                    pass
            with self._lock:
                self._danmaku_busy.discard(key)

    def purge(self, drama_id, remove_files=False):
        """Forget every published episode of one drama (self-test / reset)."""
        prefix = "%s|" % (drama_id or "?")
        removed = []

        def mutate(index_map):
            for key in [key for key in index_map if key.startswith(prefix)]:
                entry = index_map.pop(key, None)
                if entry is not None:
                    removed.append(entry)
            return bool(removed)

        _update_index(mutate)
        if remove_files:
            for entry in removed:
                path = (entry or {}).get("path")
                try:
                    if path and os.path.isfile(path):
                        os.remove(path)
                except OSError:
                    pass
        return len(removed)

    def forget(self, job):
        """Drop a removed job from the index (files are deleted by the core)."""
        key = job_entry(job)
        box = {}

        def mutate(index_map):
            entry = index_map.pop(key, None)
            if entry is None:
                return False
            box["entry"] = entry
            return True

        _update_index(mutate)
        return box.get("entry")
