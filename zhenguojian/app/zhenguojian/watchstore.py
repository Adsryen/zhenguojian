# -*- coding: utf-8 -*-
"""Local watch library: follow list (追剧) + viewing history (观看历史).

Mirrors the reference app's model (follow = favourite + status machine,
history = one WatchEntry per drama capped at 300).  Everything lives in one
JSON file under the app data directory; every mutation is atomic and emits
``changed`` so panels and the detail page can refresh.
"""
from __future__ import annotations

import json
import os
import re
import tempfile
import threading
import time
import unicodedata

from PyQt5.QtCore import QObject, pyqtSignal

from . import tools

HISTORY_CAP = 300
SAVE_INTERVAL = 30.0   # seconds between progress-file writes while playing


def _now():
    return time.time()


_SEASON_RE = re.compile(r"第\s*([0-9０-９零〇一二两兩三四五六七八九十百]+)\s*[季部]")
_CN_DIGITS = {"零": 0, "〇": 0, "一": 1, "二": 2, "两": 2, "兩": 2,
              "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9}


def _chinese_number(text):
    text = unicodedata.normalize("NFKC", str(text or "")).strip()
    if text.isdigit():
        return int(text)
    section = number = 0
    for char in text:
        if char in _CN_DIGITS:
            number = _CN_DIGITS[char]
        elif char == "十":
            section += (number or 1) * 10
            number = 0
        elif char == "百":
            section += (number or 1) * 100
            number = 0
    return section + number


def season_number(title):
    """从「第N季 / 第N部」标题里取季号；没有就按第 1 季处理。"""
    match = _SEASON_RE.search(str(title or ""))
    if not match:
        return 1
    value = _chinese_number(match.group(1))
    return value if 1 <= value <= 200 else 1


class WatchStore(QObject):
    changed = pyqtSignal()

    def __init__(self, parent=None):
        super().__init__(parent)
        self._file = os.path.join(tools.data_dir(), "watch-library.json")
        self._lock = threading.Lock()
        self._follows = {}   # drama id -> {drama, status, known, read, marked, added}
        self._history = {}   # drama id -> {drama, episode, position, duration, updated}
        self._local_cache = {}      # normalised stem -> (drama, episode)
        self._library_rows = []    # parsed episodes.json entries
        self._library_stamp = None  # (mtime, size) fingerprint of that file
        self._dirty = False
        self._last_write = 0.0
        self._load()

    # ------------------------------------------------------------ persistence
    @staticmethod
    def _normalize_row(row):
        """补齐老记录缺的 drama.source。

        早期版本存的 drama 没有 source 字段，core 取封面时会直接以「当前版本不
        包含此站源」失败——历史里曾有几百条这样的记录，启动时会白白打几百次请求。
        id 的前缀就是站源（hongguo:xxx），补上即可；`local:` 是本地文件的伪前缀，
        不能当成站源。
        """
        drama = row.get("drama")
        if not isinstance(drama, dict):
            # A hand-edited/corrupt record must not crash every panel that
            # calls .get("title") on it.
            row = dict(row, drama={})
            drama = row["drama"]
        if not drama.get("source"):
            prefix = str(drama.get("id") or "").split(":", 1)
            if len(prefix) == 2 and prefix[0] and prefix[0] != "local":
                row = dict(row, drama=dict(drama, source=prefix[0]))
        return row

    def _load(self):
        try:
            with open(self._file, "r", encoding="utf-8") as handle:
                data = json.load(handle)
        except (json.JSONDecodeError, UnicodeDecodeError):
            # Keep the broken file so it can be recovered by hand; the next
            # save writes a clean one instead of silently losing the list.
            tools.backup_broken(self._file)
            return
        except OSError:
            return
        if not isinstance(data, dict):
            return
        follows = data.get("follows")
        if isinstance(follows, dict):
            self._follows = {k: self._normalize_row(v) for k, v in follows.items()
                             if isinstance(v, dict)}
        history = data.get("history")
        if isinstance(history, dict):
            self._history = {k: self._normalize_row(v) for k, v in history.items()
                             if isinstance(v, dict)}

    def _commit(self):
        """Persist the library atomically.

        This runs on the GUI thread (progress is saved while a video
        plays), so a dirty flag lets the caller batch writes instead of
        rewriting the whole file on every tick - at 300 history entries
        the dump is ~100KB and costs several milliseconds.
        """
        if not getattr(self, "_dirty", False):
            return
        data = {"follows": self._follows, "history": self._history}
        try:
            os.makedirs(os.path.dirname(self._file), exist_ok=True)
            handle = tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", delete=False,
                                                 dir=os.path.dirname(self._file),
                                                 prefix=".watch-", suffix=".tmp")
            temporary = handle.name
            try:
                with handle:
                    json.dump(data, handle, ensure_ascii=False, indent=1)
                os.replace(temporary, self._file)
            except BaseException:
                try:
                    os.remove(temporary)
                except OSError:
                    pass
                raise
            self._dirty = False
            self._last_write = _now()
        except OSError:
            pass

    def _touch(self):
        """Mark the library dirty and write it out now."""
        self._dirty = True
        self._commit()

    def flush(self):
        """Write pending changes (called when the app goes idle/closes)."""
        with self._lock:
            self._touch()

    # ---------------------------------------------------------------- queries
    def is_followed(self, drama_id):
        row = self._follows.get(drama_id)
        return bool(row and row.get("followed", True))

    def is_reserved(self, drama_id):
        row = self._follows.get(drama_id)
        return bool(row and row.get("reserved"))

    def unseen_arrivals(self):
        """未看的预约上线 + 未看的集数更新，都算页签红点。"""
        with self._lock:
            return sum(1 for row in self._follows.values()
                       if (row.get("arrived") and not row.get("arrivedSeen"))
                       or (int(row.get("newEpisodes") or 0) > 0
                           and not row.get("updateSeen")))

    def mark_arrivals_seen(self):
        """用户打开追剧页后清掉页签红点；已上线/更新标记本身保留。"""
        changed = False
        with self._lock:
            for row in self._follows.values():
                if row.get("arrived") and not row.get("arrivedSeen"):
                    row["arrivedSeen"] = True
                    changed = True
                if int(row.get("newEpisodes") or 0) > 0 and not row.get("updateSeen"):
                    row["updateSeen"] = True
                    changed = True
            if changed:
                self._touch()
        return changed

    def watched(self, drama_id):
        entry = self._history.get(drama_id)
        return dict(entry) if entry else None

    def follows(self):
        with self._lock:
            rows = [dict(row, id=drama_id) for drama_id, row in self._follows.items()]
        rows.sort(key=lambda row: -float(row.get("added") or 0))
        return rows

    def history(self):
        with self._lock:
            rows = [dict(row, id=drama_id) for drama_id, row in self._history.items()]
        rows.sort(key=lambda row: -float(row.get("updated") or 0))
        return rows

    # ------------------------------------------------------------- follow ops
    def toggle_follow(self, drama):
        drama_id = drama.get("id")
        if not drama_id:
            return False
        changed = False
        with self._lock:
            row = self._follows.get(drama_id)
            if row is not None and row.get("followed", True):
                if row.get("reserved"):
                    # 还在预约中：取消追剧但保留预约，等上线仍然提醒。
                    row["followed"] = False
                    row["status"] = "预约中" if int(row.get("known") or 0) <= 0 else "想看"
                    added = False
                else:
                    del self._follows[drama_id]
                    added = False
                changed = True
            elif row is not None:
                # 之前只预约、没追剧：现在补上追剧，预约保持不动。
                row["followed"] = True
                if row.get("status") == "预约中":
                    row["status"] = "想看"
                added = True
                changed = True
            else:
                known = int(drama.get("episodes") or 0)
                season = season_number(drama.get("title"))
                self._follows[drama_id] = {
                    "drama": dict(drama), "status": "想看",
                    "known": known, "read": known if known > 0 else 0,
                    "marked": False, "added": _now(),
                    "season": season, "latestSeason": season,
                    "newSeason": False, "lastChecked": 0,
                    "followed": True, "reserved": False,
                    "reserveSeason": season, "reserveAtKnown": known,
                    "arrived": False, "arrivedSeen": True,
                    "newEpisodes": 0, "updateAt": 0.0, "updateSeen": True,
                    "sourceTime": str(drama.get("onlineDate") or ""),
                    "nextDrama": None, "nextSeason": 0,
                }
                added = True
                changed = True
            if changed:
                self._touch()
        if changed:
            self.changed.emit()
        return added

    def reserve_drama(self, drama):
        """本地预约：不依赖云端，后台检查到新集/新季时提醒。

        已完结的剧按“下一季”算目标；更新中的剧按“下一集”算；还没上线的
        剧按“第一集”算。目标只是本地记录，真正的到达判断在 observe_drama。
        """
        drama_id = drama.get("id")
        if not drama_id:
            return False
        title = drama.get("title") or ""
        season = season_number(title)
        known = int(drama.get("episodes") or 0)
        finished = str(drama.get("releaseStatus") or "") == "finished"
        with self._lock:
            row = self._follows.get(drama_id)
            if row is None:
                row = {
                    "drama": dict(drama),
                    "status": "预约中" if known <= 0 else "想看",
                    "known": known, "read": known if known > 0 else 0,
                    "marked": False, "added": _now(),
                    "season": season, "latestSeason": season,
                    "newSeason": False, "lastChecked": 0,
                    "followed": False,
                    "newEpisodes": 0, "updateAt": 0.0, "updateSeen": True,
                    "sourceTime": str(drama.get("onlineDate") or ""),
                    "nextDrama": None, "nextSeason": 0,
                }
                self._follows[drama_id] = row
            else:
                stored = dict(row.get("drama") or {})
                stored.update(drama)
                row["drama"] = stored
            row["reserved"] = True
            row["arrived"] = False
            row["arrivedSeen"] = True
            row["nextDrama"] = None
            row["nextSeason"] = 0
            row["reserveSeason"] = season + 1 if finished else season
            row["reserveAtKnown"] = known
            self._touch()
        self.changed.emit()
        return True

    def cancel_reservation(self, drama_id):
        with self._lock:
            row = self._follows.get(drama_id)
            if row is None or not row.get("reserved"):
                return False
            row["reserved"] = False
            row["arrived"] = False
            row["arrivedSeen"] = True
            row["nextDrama"] = None
            row["nextSeason"] = 0
            if not row.get("followed", True):
                del self._follows[drama_id]
            elif row.get("status") == "预约中":
                row["status"] = "想看"
            self._touch()
        self.changed.emit()
        return True

    def remove_follow(self, drama_id):
        with self._lock:
            if drama_id in self._follows:
                del self._follows[drama_id]
                self._touch()
        self.changed.emit()

    def mark_watched(self, drama_id):
        with self._lock:
            follow = self._follows.get(drama_id)
            if follow is None:
                return
            follow["status"] = "已看"
            follow["read"] = int(follow.get("known") or 0)
            follow["marked"] = True
            follow["arrived"] = False
            follow["arrivedSeen"] = True
            follow["newEpisodes"] = 0
            follow["updateSeen"] = True
            if follow.get("reserved"):
                # Re-arm for the next episode of the current season.
                follow["reserveAtKnown"] = int(follow.get("known") or 0)
            self._touch()
        self.changed.emit()

    def observe_drama(self, drama):
        """Refresh a followed/reserved drama after a detail load.

        A title that moves from 第1季 to 第2季 is a *new season*, not N new
        episodes of the old one: keep the old season's known/read progress and
        flag latestSeason so the follow wall can say 「新一季」.  For a local
        reservation, crossing reserveSeason/reserveAtKnown flips arrived=True;
        the UI turns that into the tab badge / notification.
        """
        drama_id = drama.get("id")
        changed = False
        with self._lock:
            follow = self._follows.get(drama_id)
            if follow is None:
                return
            stored = dict(follow.get("drama") or {})
            season = season_number(drama.get("title") or stored.get("title"))
            current = int(follow.get("season") or 1)
            episodes = int(drama.get("episodes") or 0)
            now = _now()
            merged = dict(stored)
            merged.update(drama)
            if merged != stored:
                follow["drama"] = merged
                changed = True
            arrived = False
            if follow.get("reserved"):
                target_season = int(follow.get("reserveSeason") or current)
                target_known = int(follow.get("reserveAtKnown") or 0)
                if season > current and season >= target_season:
                    arrived = True
                elif season == current and episodes > target_known:
                    arrived = True
                elif episodes > 0 and int(follow.get("known") or 0) <= 0:
                    arrived = True
                if arrived:
                    follow["arrived"] = True
                    follow["arrivedSeen"] = False
                    follow["reserved"] = False
                    if not follow.get("followed", True) and follow.get("status") == "预约中":
                        follow["status"] = "想看"
                    changed = True
            if season > current:
                latest = max(int(follow.get("latestSeason") or current), season)
                follow["season"] = season
                follow["known"] = episodes
                follow["read"] = 0
                follow["latestSeason"] = latest
                follow["newSeason"] = True
                follow["nextDrama"] = dict(drama)
                follow["nextSeason"] = season
                follow["updateAt"] = now
                follow["updateSeen"] = False
                follow["newEpisodes"] = 0
                follow["lastChecked"] = now
                changed = True
            else:
                # Reserve-only rows start with known == read, so a new episode
                # still shows up as "新 N 集" after arrival.
                known = int(follow.get("known") or 0)
                follow["lastChecked"] = now
                if episodes > known:
                    follow["known"] = episodes
                    unread = max(0, episodes - int(follow.get("read") or 0))
                    follow["newEpisodes"] = max(int(follow.get("newEpisodes") or 0), unread)
                    follow["updateAt"] = now
                    follow["updateSeen"] = False
                    follow["sourceTime"] = str(
                        drama.get("onlineDate") or follow.get("sourceTime") or "")
                    if (not follow.get("marked") and follow.get("status") == "已看"
                            and unread > 0):
                        follow["status"] = "在看"
                    changed = True
                elif arrived:
                    changed = True
            if changed:
                self._touch()
        if changed:
            self.changed.emit()

    def mark_next_season(self, drama_id, next_drama, season=None):
        """后台/季末查到下一季时调用：把旧季标成“已上线”，并记住入口。

        下一季本身是另一部剧（独立 id），这里只在旧季的记录上挂一个
        nextDrama，追剧列表双击/「去观看」时直接打开它。
        """
        if not drama_id or not isinstance(next_drama, dict):
            return False
        number = int(season or season_number(next_drama.get("title")))
        with self._lock:
            follow = self._follows.get(drama_id)
            if follow is None:
                return False
            follow["arrived"] = True
            follow["arrivedSeen"] = False
            follow["reserved"] = False
            follow["newSeason"] = True
            follow["latestSeason"] = max(int(follow.get("latestSeason") or 1), number)
            follow["nextDrama"] = dict(next_drama)
            follow["nextSeason"] = number
            follow["updateAt"] = _now()
            follow["updateSeen"] = False
            if not follow.get("followed", True) and follow.get("status") == "预约中":
                follow["status"] = "想看"
            self._touch()
        self.changed.emit()
        return True

    # ----------------------------------------------------------- history ops
    def save_watch(self, drama, episode, position, duration, finished):
        """Record progress. Called every few seconds while playing.

        The in-memory update always happens; the file write is throttled
        so a long session does not serialise ~100KB every single tick.
        A crash can therefore lose a few seconds of position, never more.
        """
        drama_id = drama.get("id")
        if not drama_id:
            return
        episode = int(episode or 0)
        position = float(position or 0.0)
        duration = float(duration or 0.0)
        with self._lock:
            existing = self._history.get(drama_id)
            if duration <= 0 and existing is not None:
                # Starting an episode records position 0 before the player
                # reports duration; keep the known duration instead of
                # blanking the progress percentage in the history list.
                duration = float(existing.get("duration") or 0.0)
            # Paused playback keeps reporting the same time-pos.  Ignoring
            # identical ticks stops the history list from re-sorting and
            # rebuilding every 5 seconds while the user is browsing it.
            if (existing is not None and not finished
                    and int(existing.get("episode") or 0) == episode
                    and abs(float(existing.get("position") or 0.0) - position) < 0.5
                    and abs(float(existing.get("duration") or 0.0) - duration) < 0.5):
                return
            self._history[drama_id] = {
                "drama": dict(drama), "episode": episode,
                "position": position, "duration": duration,
                "updated": _now(),
            }
            if len(self._history) > HISTORY_CAP:
                by_age = sorted(self._history.items(),
                                key=lambda kv: float(kv[1].get("updated") or 0))
                for key, _row in by_age[:len(self._history) - HISTORY_CAP]:
                    del self._history[key]
            follow = self._follows.get(drama_id)
            if follow is not None:
                known = max(int(follow.get("known") or 0), int(drama.get("episodes") or 0))
                follow["known"] = known
                if not follow.get("marked"):
                    if finished and (known <= 0 or episode >= known):
                        follow["status"], follow["read"] = "已看", known
                    else:
                        follow["status"] = "在看"
                        if finished:
                            follow["read"] = max(int(follow.get("read") or 0), episode)
            self._dirty = True
            # A finished episode is a milestone: never risk losing it.
            if finished:
                self._commit()
            elif _now() - self._last_write >= SAVE_INTERVAL:
                self._commit()
        self.changed.emit()

    def remove_history(self, drama_id):
        with self._lock:
            if drama_id in self._history:
                del self._history[drama_id]
                self._touch()
        self.changed.emit()

    # ------------------------------------------------------- local playback
    @staticmethod
    def _normalise(text):
        """Fold separators the filename sanitizer introduces, for matching."""
        for char in ' _*?:"<>|/\\，,、.-—～~':
            text = text.replace(char, "")
        return text.lower()

    def _resolve_local(self, stem):
        """Map a local file name back to a known drama via the library index.

        Users rename merged files freely ("... S34E01.mkv"), so match on the
        sanitised library title appearing inside the name - trying the full
        title first, then the title without its 第N季 suffix (paired with the
        SxxEyy / 第x季 marker in the file name so seasons stay distinct).

        Both the parsed index and the per-file answer are cached: this runs
        on the GUI thread every few seconds during local playback, and
        re-reading the whole library each time was the single biggest
        stall in that path.
        """
        import re
        key = self._normalise(stem)
        cached = self._local_cache.get(key)
        if cached is not None:
            return dict(cached[0]), cached[1]

        library = self._library_snapshot()
        stem_season = re.search(r"s(\d+)e\d+", key) or re.search(r"第(\d+)季", key)
        season = int(stem_season.group(1)) if stem_season else None
        best, best_len = None, 0
        for entry in library:
            title = (entry.get("title") or "").strip()
            if not title:
                continue
            norm = self._normalise(title)
            if len(norm) > best_len and norm in key:
                best, best_len = entry, len(norm)
                continue
            if season is not None:
                root = re.sub(r"第\d+季$", "", norm)
                title_season = re.search(r"第(\d+)季", norm)
                if (root and len(root) > best_len and root in key
                        and title_season and int(title_season.group(1)) == season):
                    best, best_len = entry, len(root)
        if best is None:
            result = ({"id": "local:" + key, "title": stem, "episodes": 0}, 1)
            self._local_cache[key] = result
            return dict(result[0]), result[1]
        episode = 1
        rng = re.search(r"全(\d+)-\d+集", stem)
        if rng:
            episode = int(rng.group(1))
        else:
            ep = re.search(r"s\d+e(\d+)", key) or re.search(r"第(\d+)集", key)
            if ep:
                episode = int(ep.group(1))
        drama = {"id": best.get("dramaId"), "title": best.get("title"),
                 "episodes": int(best.get("index") or episode), "source": "hongguo"}
        self._local_cache[key] = (dict(drama), episode)
        return drama, episode

    def _library_snapshot(self):
        """Parsed episodes.json, re-read only when the file changes.

        Keyed on (mtime, size) so a download finishing in the background
        is picked up, while an unchanged file costs one stat() call.
        """
        path = os.path.join(tools.download_dir(), "episodes.json")
        try:
            stamp = (os.path.getmtime(path), os.path.getsize(path))
        except OSError:
            return []
        if stamp == self._library_stamp:
            return self._library_rows
        try:
            with open(path, "r", encoding="utf-8") as handle:
                data = json.load(handle)
        except Exception:
            return []
        rows = [row for row in (data or {}).values() if isinstance(row, dict)]
        self._library_stamp = stamp
        self._library_rows = rows
        # The file changed, so earlier per-file matches may now be stale.
        self._local_cache = {}
        return rows

    def save_local_watch(self, path, position, duration, finished):
        """Record playback of a local file (merged output / downloaded file)."""
        path = os.path.abspath(path)
        stem = os.path.splitext(os.path.basename(path))[0]
        drama, _episode = self._resolve_local(stem)
        drama_id = drama.get("id")
        position = float(position or 0.0)
        duration = float(duration or 0.0)
        with self._lock:
            existing = self._history.get(drama_id)
            if duration <= 0 and existing is not None:
                duration = float(existing.get("duration") or 0.0)
            if (existing is not None and not finished
                    and existing.get("local_path") == path
                    and abs(float(existing.get("position") or 0.0) - position) < 0.5
                    and abs(float(existing.get("duration") or 0.0) - duration) < 0.5):
                return
            self._history[drama_id] = {
                "drama": drama, "episode": 1,
                "position": position,
                "duration": duration,
                "updated": _now(), "local_path": path,
            }
            if len(self._history) > HISTORY_CAP:
                by_age = sorted(self._history.items(),
                                key=lambda kv: float(kv[1].get("updated") or 0))
                for key, _row in by_age[:len(self._history) - HISTORY_CAP]:
                    del self._history[key]
            self._touch()
        self.changed.emit()

    def clear_history(self):
        with self._lock:
            self._history = {}
            self._touch()
        self.changed.emit()
