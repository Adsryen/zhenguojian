# -*- coding: utf-8 -*-
"""追剧 / 观看历史 panels rendered as poster walls."""
from __future__ import annotations

import os
import time

from PyQt5.QtCore import Qt, pyqtSignal
from PyQt5.QtGui import QIcon
from PyQt5.QtWidgets import (QHBoxLayout, QLabel, QListWidget, QListWidgetItem,
                             QPushButton, QVBoxLayout, QWidget)

from . import watchstore
from .poster_tooltip import poster_tooltip
from .widgets import GRID_SIZE, THUMB_SIZE, CoverDelegate, missing_tile


def _short_time(stamp):
    try:
        return time.strftime("%m-%d %H:%M", time.localtime(float(stamp)))
    except (TypeError, ValueError):
        return ""


class WatchPanel(QWidget):
    """mode="follow" renders the follow list, "history" the watch history.

    Poster-wall layout: one tile per drama (cover, title, status line).
    A single click only selects the tile - the buttons below act on the
    selection - and a double click continues playback.  (Single click used to
    start playing, which made 「删除记录」 impossible: the tile you wanted to
    delete was already playing.)
    """

    playRequested = pyqtSignal(object, int, float)  # drama, episode index, resume seconds
    localPlayRequested = pyqtSignal(str, float)     # file path, resume seconds

    def __init__(self, store: watchstore.WatchStore, mode, covers, parent=None):
        super().__init__(parent)
        self._store = store
        self._mode = mode
        self._covers = covers
        self._sig = None
        self._rows = []
        self._rows_by_id = {}

        self.list = QListWidget()
        self.list.setProperty("role", "wall")
        self.list.setViewMode(QListWidget.IconMode)
        self.list.setResizeMode(QListWidget.Adjust)
        self.list.setMovement(QListWidget.Static)
        self.list.setWordWrap(True)
        self.list.setIconSize(THUMB_SIZE)
        self.list.setGridSize(GRID_SIZE)
        self.list.setItemDelegate(CoverDelegate(self.list))
        self.list.setUniformItemSizes(True)
        self.list.itemDoubleClicked.connect(self._open_current)
        self.list.currentItemChanged.connect(lambda *_: self._update_actions())

        self.status = QLabel("")
        self.status.setProperty("role", "hint")
        # 操作提示和计数合并到状态行里，底部只留一排控件：以前「清空历史」
        # 单独占一行，底边就是两排参差的按钮。
        self._hint = ("双击继续播放，单击选中后可删除这条记录" if mode == "history"
                      else "双击继续播放，单击选中后可标记已看 / 取消追剧或预约")

        self.clear_button = None
        if mode == "history":
            self.clear_button = QPushButton("清空历史")
            self.clear_button.clicked.connect(self._store.clear_history)

        actions = QHBoxLayout()
        self.play_button = QPushButton("▶ 继续播放")
        self.play_button.setProperty("role", "accent")
        self.play_button.clicked.connect(lambda: self._open_current(self.list.currentItem()))
        actions.addWidget(self.play_button)
        if mode == "follow":
            self.mark_button = QPushButton("标记已看")
            self.mark_button.clicked.connect(self._mark_watched)
            actions.addWidget(self.mark_button)
            self.remove_button = QPushButton("取消追剧")
            self.remove_button.clicked.connect(self._remove_selected)
            actions.addWidget(self.remove_button)
        else:
            self.remove_button = QPushButton("删除记录")
            self.remove_button.clicked.connect(self._remove_selected)
            actions.addWidget(self.remove_button)
        if self.clear_button is not None:
            actions.addWidget(self.clear_button)

        buttons = QHBoxLayout()
        buttons.addWidget(self.status, 1)
        buttons.addLayout(actions)

        layout = QVBoxLayout(self)
        layout.setContentsMargins(6, 6, 6, 6)
        layout.addWidget(self.list, 1)
        layout.addLayout(buttons)

        store.changed.connect(self.refresh)
        covers.loaded.connect(self._on_cover)
        self.refresh()

    # ----------------------------------------------------------------- rows
    def refresh(self):
        """按需重建列表，并在本页可见时才去拉封面。

        追剧/历史里的每一行都要一张海报；两个面板在窗口构造时就建好了，于是
        启动瞬间会替没人看的页签打几百次封面请求（历史里缺 source 的老记录还
        会全部失败重试）。所以拉封面这一步单独拆出来，只在页签真的显示时才做；
        切到本页时由主窗口再调一次 refresh()。
        """
        rows = self._store.follows() if self._mode == "follow" else self._store.history()
        # Only the row set/order decides a rebuild.  The five-second progress
        # save changes `updated`/`position`; rebuilding for that wiped the
        # user's selection and scrolled the list back to the top.
        structure = tuple(row["id"] for row in rows)
        self._rows = rows
        self._rows_by_id = {row["id"]: row for row in rows}
        if structure != self._sig:
            self._sig = structure
            current_id = None
            current = self.list.currentItem()
            if current is not None:
                current_id = current.data(Qt.UserRole)
            scroll = self.list.verticalScrollBar().value()
            self.list.blockSignals(True)
            self.list.clear()
            for row in rows:
                self.list.addItem(self._make_row(row))
            self.list.blockSignals(False)
            if current_id is not None:
                for index in range(self.list.count()):
                    if self.list.item(index).data(Qt.UserRole) == current_id:
                        self.list.setCurrentRow(index)
                        break
            self.list.verticalScrollBar().setValue(scroll)
        else:
            for index, row in enumerate(rows):
                item = self.list.item(index)
                if item is not None:
                    if item.text() != self._row_text(row):
                        item.setText(self._row_text(row))
                    item.setToolTip(poster_tooltip(row.get("drama") or {}))
        if not rows:
            self.status.setText("还没有%s记录 · %s"
                                % ("追更/预约" if self._mode == "follow" else "观看", self._hint))
        else:
            self.status.setText("共 %d 部 · %s" % (len(rows), self._hint))
        self._update_actions()
        self._request_covers()

    def _row_text(self, row):
        drama = row.get("drama") or {}
        title = (drama.get("title") or "短剧").strip()
        return title + "\n" + self._meta_text(row)

    def _make_row(self, row):
        item = QListWidgetItem(self._row_text(row))
        item.setData(Qt.UserRole, row["id"])
        item.setToolTip(poster_tooltip(row.get("drama") or {}))
        # A rebuilt wall must reuse already-loaded covers: the loader
        # only emits once per drama, so re-requesting a cached cover
        # would leave the tile on its placeholder forever.
        cached = self._covers.cached(row["id"])
        item.setIcon(QIcon(cached if cached is not None else missing_tile()))
        item.setTextAlignment(Qt.AlignHCenter | Qt.AlignTop)
        return item

    def reload_theme(self):
        """Repaint placeholder tiles after a dark/light switch."""
        for index in range(self.list.count()):
            item = self.list.item(index)
            row = self._rows_by_id.get(item.data(Qt.UserRole))
            if row is None:
                continue
            cached = self._covers.cached(row["id"])
            item.setIcon(QIcon(cached if cached is not None else missing_tile()))

    def _request_covers(self):
        """为本页还没有海报的行排队加载（只有页面显示时才真的发请求）。"""
        if not self.isVisible():
            return
        for row in self._rows:
            identifier = row["id"]
            if self._covers.cached(identifier) is not None or self._covers.known(identifier):
                continue
            if str(identifier).startswith("local:"):
                continue          # 本地文件没有海报可取，卡片已经写好剧名
            self._covers.request(row.get("drama") or {})

    def _meta_text(self, row):
        if self._mode == "follow":
            known = int(row.get("known") or 0)
            read = int(row.get("read") or 0)
            # 新记录直接存 newEpisodes；老记录没有这个字段时退回集数差。
            new = int(row.get("newEpisodes") or max(0, known - read))
            stamp = float(row.get("updateAt") or 0)
            if row.get("arrived"):
                meta = "🔔 已上线"
                if row.get("newSeason") and row.get("nextSeason"):
                    meta += " · 第 %d 季上线" % int(row["nextSeason"])
                elif new > 0:
                    meta += " · 新 %d 集" % new
            elif row.get("reserved"):
                meta = "预约中"
                target = int(row.get("reserveSeason") or 0)
                season = int(row.get("season") or 1)
                if target > season:
                    meta += " · 等第 %d 季" % target
                elif known > 0:
                    meta += " · 等更新"
                else:
                    meta += " · 等上线"
                if new > 0:
                    meta += " · 新 %d 集" % new
            else:
                meta = str(row.get("status") or "想看")
                if row.get("newSeason"):
                    meta += " · 新一季（第 %d 季）" % int(row.get("latestSeason") or 2)
                elif new > 0:
                    meta += " · 新 %d 集" % new
            if stamp and (new > 0 or row.get("arrived")):
                meta += " · %s 更新" % _short_time(stamp)
            if known > 0:
                meta += " · 共 %d 集" % known
            entry = self._store.watched(row["id"])
            if entry:
                meta += " · 看到第 %d 集" % int(entry.get("episode") or 1)
            return meta
        episode = int(row.get("episode") or 1)
        duration = float(row.get("duration") or 0)
        position = float(row.get("position") or 0)
        percent = " · 已看 %d%%" % int(position / duration * 100) if duration > 1 else ""
        local = "本地成品 · " if row.get("local_path") else ""
        return "%s上次看到 第 %d 集%s · %s" % (local, episode, percent, _short_time(row.get("updated")))

    def _on_cover(self, drama_id, pixmap):
        """Route freshly loaded covers into this wall's tiles."""
        for index in range(self.list.count()):
            item = self.list.item(index)
            if item.data(Qt.UserRole) == drama_id:
                item.setIcon(QIcon(pixmap))
                return

    # -------------------------------------------------------------- actions
    def _selected_row(self):
        item = self.list.currentItem()
        if item is None:
            return None
        return self._rows_by_id.get(item.data(Qt.UserRole))

    def _open_current(self, item):
        row = self._selected_row()
        if row is None and item is not None:
            row = self._rows_by_id.get(item.data(Qt.UserRole))
        if row is None:
            return
        entry = self._store.watched(row["id"])
        if entry and entry.get("local_path"):
            path = entry["local_path"]
            if os.path.isfile(path):
                self.localPlayRequested.emit(path, float(entry.get("position") or 0.0))
            else:
                self.status.setText("本地文件已不存在：%s" % os.path.basename(path))
            return
        drama = row.get("drama") or {}
        # 预约到的新一季是另一部剧：双击旧季时直接打开新季。
        if row.get("arrived") and isinstance(row.get("nextDrama"), dict):
            drama = row["nextDrama"]
        entry = self._store.watched(drama.get("id"))
        index, resume = 0, 0.0
        if entry and int(entry.get("episode") or 0) > 0:
            episode = int(entry["episode"])
            duration = float(entry.get("duration") or 0)
            position = float(entry.get("position") or 0)
            if duration > 1 and position >= duration - 1:
                # Finished this episode: continue with the next one.  For the
                # last episode clamp to itself so the click still plays
                # something instead of silently doing nothing.
                total = int(drama.get("episodes") or episode)
                index = min(episode, max(0, total - 1))
                resume = 0.0
            else:
                index, resume = episode - 1, position
        self.playRequested.emit(drama, index, resume)

    def _mark_watched(self):
        row = self._selected_row()
        if row:
            self._store.mark_watched(row["id"])

    def _remove_selected(self):
        row = self._selected_row()
        if row is None:
            return
        if self._mode == "follow":
            self._store.remove_follow(row["id"])
        else:
            self._store.remove_history(row["id"])

    def _update_actions(self):
        has = self._selected_row() is not None
        self.play_button.setEnabled(has)
        self.remove_button.setEnabled(has)
        if self._mode == "follow":
            self.mark_button.setEnabled(has)
            row = self._selected_row() or {}
            # 只预约（没追更）的记录，删掉它就是取消预约。
            self.remove_button.setText(
                "取消预约" if row.get("reserved") and not row.get("followed") else "取消追剧")
