"""Trigger a callback when a scrolling view approaches its end."""
from __future__ import annotations

from PyQt5.QtCore import QEvent, QObject, QTimer


class ScrollLoader(QObject):
    def __init__(self, view, callback, visible_changed=None, margin=400):
        super().__init__(view)
        self._view = view
        self._viewport = view.viewport()
        self._callback = callback
        self._visible_changed = visible_changed
        self._margin = margin
        self._scheduled = False
        self._timer = QTimer(self)
        self._timer.setSingleShot(True)
        self._timer.timeout.connect(self._check)
        view.verticalScrollBar().valueChanged.connect(self._changed)
        view.verticalScrollBar().rangeChanged.connect(self._changed)
        self._viewport.installEventFilter(self)

    def _changed(self, *_args):
        self.schedule()

    def schedule(self):
        if self._scheduled:
            return
        self._scheduled = True
        self._timer.start(0)

    def _check(self):
        self._scheduled = False
        if not self._view.isVisible():
            return
        self._view.doItemsLayout()
        if self._visible_changed:
            self._visible_changed()
        bar = self._view.verticalScrollBar()
        if bar.maximum() - bar.value() <= self._margin:
            self._callback()

    def eventFilter(self, watched, event):
        if watched is self._viewport and event.type() in (QEvent.Resize, QEvent.Show):
            self.schedule()
        return super().eventFilter(watched, event)
