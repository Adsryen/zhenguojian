# -*- coding: utf-8 -*-
"""Background task plumbing: core calls and media work never block the GUI."""
from __future__ import annotations

import inspect
import time
import traceback

from PyQt5.QtCore import QObject, QThread, Qt, pyqtSignal

from . import tools
from .core_client import CoreError


class _Relay(QObject):
    """Marshals a worker-thread callback onto the GUI thread.

    A lambda connected to a signal runs wherever the signal is emitted, so
    callbacks connected that way would touch Qt from a worker thread.  This
    relay lives on the GUI thread and uses a queued connection to bounce the
    call back onto it.

    One relay per TaskRunner, and it is never deleted while the runner lives.
    A relay per worker, deleted from the worker thread as soon as the worker
    finished, also threw away the callback still sitting in the GUI event
    queue: 榜单下拉框、搜索结果、详情页会毫无规律地"没有回调"，两个并发请求
    的回调顺序也无从保证。
    """

    _invoked = pyqtSignal(object)

    def __init__(self, parent=None):
        super().__init__(parent)
        self._invoked.connect(self._call, Qt.QueuedConnection)

    def invoke(self, callback):
        self._invoked.emit(callback)

    def _call(self, callback=None):
        if callback is not None:
            try:
                callback()
            except Exception:
                traceback.print_exc()
                tools.log_exception("界面回调异常")


class Worker(QObject):
    finished = pyqtSignal()
    failed = pyqtSignal(str)
    succeeded = pyqtSignal(object)

    def __init__(self, function, parent=None):
        super().__init__(parent)
        self._function = function
        # Callers pass either ``fn()`` or ``fn(worker)``; support both.
        try:
            self._passes_worker = len(inspect.signature(function).parameters) > 0
        except (TypeError, ValueError):
            self._passes_worker = False
        self._thread = QThread()
        self.moveToThread(self._thread)
        self._thread.started.connect(self._run)
        self._thread.finished.connect(self._thread.deleteLater)

    def start(self):
        self._thread.start()
        return self

    def _run(self):
        try:
            result = self._function(self) if self._passes_worker else self._function()
        except Exception as exc:  # noqa: BLE001 - surfaced to the UI
            detail = str(exc) or exc.__class__.__name__
            # 用户切集/换页触发的取消是正常流程，不该写成失败日志。
            if not (isinstance(exc, CoreError) and exc.cancelled):
                traceback.print_exc()
                tools.log_exception("后台任务失败")
            self.failed.emit(detail)
        else:
            self.succeeded.emit(result)
        finally:
            self.finished.emit()
            self._thread.quit()

    def wait(self, timeout=None):
        return self._thread.wait(timeout)


class TaskRunner(QObject):
    """Fire-and-forget worker pool with completion signals.

    Every worker is kept in ``self._workers`` until its thread reports
    ``finished``.  Without that reference the QThread (and the C++ QObject
    that was moved onto it) is garbage collected while still running, which
    takes the whole process down with STATUS_STACK_BUFFER_OVERRUN.
    """

    def __init__(self, parent=None):
        super().__init__(parent)
        self._workers = set()
        # Shared by every worker of this runner; see _Relay for why it must
        # outlive the worker that queued the callback.
        self._relay = _Relay(self)

    def run(self, function, on_success=None, on_error=None, on_done=None):
        # No parent: a parented QObject cannot be moved to the worker thread,
        # which would run every task on the GUI thread instead.
        worker = Worker(function)
        self._workers.add(worker)
        relay = self._relay

        if on_success:
            worker.succeeded.connect(lambda value: relay.invoke(lambda: on_success(value)))
        if on_error:
            worker.failed.connect(lambda text: relay.invoke(lambda: on_error(text)))
        if on_done:
            worker.finished.connect(lambda: relay.invoke(on_done))

        def release():
            self._workers.discard(worker)
            worker.deleteLater()

        worker.finished.connect(release)
        worker.start()
        return worker

    def shutdown(self, timeout=3000):
        """Wait for workers, sharing one total budget instead of per worker."""
        deadline = time.time() + max(0, timeout) / 1000.0
        for worker in list(self._workers):
            remaining = int((deadline - time.time()) * 1000)
            if remaining <= 0:
                break
            try:
                worker.wait(remaining)
            except Exception:
                pass
        self._workers.clear()
