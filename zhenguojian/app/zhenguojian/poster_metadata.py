"""Fetch heat and episode counts for visible posters in one upstream batch."""
from __future__ import annotations

import time
from collections import OrderedDict, deque

from PyQt5.QtCore import QObject, QTimer, pyqtSignal

from .tasks import TaskRunner

BATCH_SIZE = 18


class PosterMetadataLoader(QObject):
    updated = pyqtSignal(object)

    def __init__(self, core, parent=None):
        super().__init__(parent)
        self._core = core
        self._runner = TaskRunner(self)
        self._queue = deque()
        self._queued = set()
        self._active = False
        self._active_ids = set()
        self._stopped = False
        self._cache = OrderedDict()
        self._retry_at = {}
        self._timer = QTimer(self)
        self._timer.setSingleShot(True)
        self._timer.timeout.connect(self._start_next)

    def reset(self):
        self._queue.clear()
        self._queued = set(self._active_ids)
        self._timer.stop()

    def request(self, dramas):
        if self._stopped:
            return
        visible = {drama.get("id"): drama for drama in dramas}
        self._queue = deque(drama for drama in self._queue if drama["id"] in visible)
        self._queued = {drama["id"] for drama in self._queue} | self._active_ids
        now = time.monotonic()
        for identifier, drama in visible.items():
            if not identifier or not identifier.startswith("hongguo:"):
                continue
            if drama.get("heat") and drama.get("episodes"):
                continue
            cached = self._cache.get(identifier)
            if cached and now < cached[0]:
                self.updated.emit(dict(cached[1]))
                continue
            if identifier in self._queued or now < self._retry_at.get(identifier, 0):
                continue
            self._queued.add(identifier)
            self._queue.append(dict(drama))
        self._start_next()

    def _start_next(self):
        if self._stopped or self._active or not self._queue:
            return
        batch = [self._queue.popleft() for _ in range(min(BATCH_SIZE, len(self._queue)))]
        identifiers = {drama["id"] for drama in batch}
        self._active = True
        self._active_ids = identifiers

        def work():
            return self._core.poster_metadata(batch)

        def done(result):
            received = set()
            for fresh in (result or {}).get("items") or []:
                identifier = fresh.get("id")
                if identifier not in identifiers or not (fresh.get("heat") or fresh.get("episodes")):
                    continue
                received.add(identifier)
                self._cache[identifier] = (time.monotonic() + 600, dict(fresh))
                self._cache.move_to_end(identifier)
                if not self._stopped:
                    self.updated.emit(fresh)
            while len(self._cache) > 1000:
                self._cache.popitem(last=False)
            for identifier in identifiers - received:
                self._retry_at[identifier] = time.monotonic() + 300
            finish()

        def failed(_message):
            for identifier in identifiers:
                self._retry_at[identifier] = time.monotonic() + 300
            finish()

        def finish():
            self._active = False
            self._active_ids = set()
            self._queued.difference_update(identifiers)
            if not self._stopped:
                self._timer.start(0)

        self._runner.run(work, on_success=done, on_error=failed)

    def stop(self):
        self._stopped = True
        self.reset()
        self._runner.shutdown(800)
