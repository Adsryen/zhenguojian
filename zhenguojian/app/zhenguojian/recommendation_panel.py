# -*- coding: utf-8 -*-
"""剧库推荐：海报角标与新剧一致，滚动时连续追加推荐。"""
from __future__ import annotations

from PyQt5.QtCore import Qt, pyqtSignal
from PyQt5.QtWidgets import QHBoxLayout, QLabel, QPushButton, QVBoxLayout, QWidget

from .poster_metadata import PosterMetadataLoader
from .poster_wall import PosterWall
from .scroll_loader import ScrollLoader
from .tasks import TaskRunner

BATCH_SIZE = 50


class RecommendationPanel(QWidget):
    dramaActivated = pyqtSignal(object)

    def __init__(self, core, covers, request_covers, parent=None):
        super().__init__(parent)
        self._core = core
        self._runner = TaskRunner(self)
        self._metadata = PosterMetadataLoader(core, self)
        self._metadata.updated.connect(self._on_metadata)
        self._token = 0
        self._category = None
        self._loading = False
        self._server_more = False
        self._pending = []
        self._retry_request = None
        self._empty_pages = 0
        self._stopped = False

        self.list = PosterWall(covers=covers)
        self.list.setProperty("role", "newPosterWall")
        self.list.viewport().setObjectName("newPosterViewport")
        self.list.set_dual_badges(True)
        self.list.set_cover_loader(covers, request_covers)
        self.list.dramaActivated.connect(self.dramaActivated.emit)
        self.list.set_refresh_enabled(True)
        self.list.refreshRequested.connect(self._refresh_top)
        self._scroll_loader = ScrollLoader(self.list, self._load_more, self._request_metadata)
        self.status = QLabel("等待加载")
        self.status.setProperty("role", "hint")
        self.retry_button = QPushButton("重试加载")
        self.retry_button.setProperty("role", "ghost")
        self.retry_button.clicked.connect(self._retry)
        self.retry_button.hide()
        footer = QHBoxLayout()
        footer.setContentsMargins(4, 0, 4, 0)
        footer.setSpacing(8)
        footer.addWidget(self.status, 1)
        footer.addWidget(self.retry_button)
        layout = QVBoxLayout(self)
        layout.setContentsMargins(10, 6, 10, 6)
        layout.setSpacing(8)
        layout.addWidget(self.list, 1)
        layout.addLayout(footer)

        self.empty = QLabel(self.list)
        self.empty.setProperty("role", "hint")
        self.empty.setAlignment(Qt.AlignCenter)
        self.empty.setWordWrap(True)
        self.empty.setAttribute(Qt.WA_TransparentForMouseEvents)
        self.empty.hide()

    def resizeEvent(self, event):
        super().resizeEvent(event)
        self.empty.setGeometry(self.list.rect())

    def load(self, category="", force=False, more=False, replace=False):
        if self._stopped or (more and self._loading):
            return
        changed = category != self._category
        if not (changed or force or more or replace) and self.count():
            return
        self._token += 1
        token = self._token
        known = self.known_ids()
        self._category = category
        self._loading = True
        self._retry_request = None
        self.retry_button.hide()
        if not more:
            self._core.reset_recommendation_page()
        if changed:
            self._pending = []
            self._server_more = False
            self.list.set_items([])
            self._metadata.reset()
        if not more or replace:
            self._empty_pages = 0
        self.status.setText("正在加载更多推荐…" if more and not replace else "正在加载推荐…")
        self.empty.setVisible(not self.count())
        self.empty.setText("正在加载推荐…")
        self.empty.setGeometry(self.list.rect())

        def work():
            return self._core.recommendations(category=category, force=force,
                                              more=more, lane="library")

        def done(result):
            if token != self._token or self._stopped:
                return
            result = result or {}
            seen = set(known) if more else set()
            items = []
            for drama in result.get("items") or []:
                if isinstance(drama, dict) and drama.get("id") and drama["id"] not in seen:
                    items.append(drama)
                    seen.add(drama["id"])
            self._loading = False
            if replace and not items:
                self.load(category, force=True)
                return
            self._server_more = bool(result.get("hasMore"))
            if not more or replace:
                self._pending = items
                self.list.set_items([])
                self._metadata.reset()
            else:
                self._pending.extend(items)
            self._empty_pages = self._empty_pages + 1 if more and not items else 0
            if self._empty_pages >= 2:
                self._server_more = False
            self._append_batch()
            if not self.count():
                self.empty.setText(result.get("warning") or "暂时没有推荐内容")
                self.empty.show()
            self._sync_status()
            self._scroll_loader.schedule()

        def failed(message):
            if token != self._token or self._stopped:
                return
            self._loading = False
            self._retry_request = (category, force, more, replace)
            self.status.setText("加载失败：%s" % message)
            self.retry_button.show()
            if not self.count():
                self.empty.setText("加载失败，请重试")
                self.empty.show()

        self._runner.run(work, on_success=done, on_error=failed)

    def _append_batch(self):
        if not self._pending:
            return
        batch, self._pending = self._pending[:BATCH_SIZE], self._pending[BATCH_SIZE:]
        self.list.append_items(batch)
        self.empty.hide()

    def _load_more(self):
        if self._loading or self._retry_request or self._stopped:
            return
        if self._pending:
            self._append_batch()
            self._sync_status()
            self._scroll_loader.schedule()
        elif self._server_more:
            self.load(self._category or "", more=True)

    def _retry(self):
        if self._retry_request:
            category, force, more, replace = self._retry_request
            self.load(category, force=force, more=more, replace=replace)

    def _refresh_top(self):
        """墙顶钉住刷新按钮：强制重刷当前分类（不改变任何既有加载路径）。"""
        self.load(self._category or "", force=True)

    def _sync_status(self):
        tail = "下滑继续浏览" if self.has_more() else "已全部加载"
        self.status.setText("已加载 %d 部 · %s" % (self.count(), tail))

    def _request_metadata(self):
        self._metadata.request(self.list.visible_dramas())

    def _on_metadata(self, drama):
        if drama:
            self.list.update_drama(drama)

    def has_more(self):
        return bool(self._pending or self._server_more)

    def count(self):
        return self.list.count()

    def items(self):
        return self.list.dramas()

    def known_ids(self):
        return {drama["id"] for drama in self.items() + self._pending}

    def set_pixmap(self, drama_id, pixmap):
        self.list.set_pixmap(drama_id, pixmap)

    def set_failed(self, drama_id):
        self.list.set_failed(drama_id)

    def reload_theme(self):
        self.list.reload_theme()

    def shutdown(self):
        self._stopped = True
        self._token += 1
        self._metadata.stop()
        self._runner.shutdown(800)
