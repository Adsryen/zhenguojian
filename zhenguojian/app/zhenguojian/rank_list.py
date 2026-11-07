# -*- coding: utf-8 -*-
"""排行榜列表：按原版红果客户端的样式画。

原版的榜单页左边是一列「名次 + 剧名 + 热度」的行，前三名用不同颜色标出，
行高固定、悬停有高亮。这里用自绘 delegate 画每一行，圆角的名次徽标按
橙/金/蓝三色区分前三名，其余是弱化的数字。
"""
from __future__ import annotations

from PyQt5.QtCore import QRect, QRectF, QSize, Qt, QTimer, pyqtSignal
from PyQt5.QtGui import QBrush, QColor, QFont, QFontMetrics, QPainter, QPainterPath
from PyQt5.QtWidgets import (QAbstractItemView, QListWidget, QListWidgetItem,
                             QStyle, QStyledItemDelegate, QWidget)

from .ui import DESIGN

ROW_HEIGHT = 52
BADGE = 26


class _RankDelegate(QStyledItemDelegate):
    """一行 = 名次徽标 + 剧名 + 热度指标。"""

    def sizeHint(self, option, index):
        return QSize(300, ROW_HEIGHT)

    def paint(self, painter, option, index):
        entry = index.data(Qt.UserRole)
        if not isinstance(entry, dict):
            return
        drama = entry.get("drama") or {}
        rank = int(entry.get("rank") or 0)
        metric = entry.get("metric") or ""
        title = (drama.get("title") or "短剧").strip()

        painter.save()
        painter.setRenderHint(QPainter.Antialiasing, True)
        rect = QRect(option.rect).adjusted(4, 2, -4, -2)
        if option.state & QStyle.State_Selected:
            painter.setBrush(QBrush(QColor(DESIGN["accent_soft"])))
            painter.setPen(Qt.NoPen)
            path = QPainterPath()
            path.addRoundedRect(QRectF(rect), 8, 8)
            painter.drawPath(path)
        elif option.state & QStyle.State_MouseOver:
            painter.setBrush(QBrush(QColor(DESIGN["chip_hover"])))
            painter.setPen(Qt.NoPen)
            path = QPainterPath()
            path.addRoundedRect(QRectF(rect), 8, 8)
            painter.drawPath(path)

        # 名次徽标
        badge = QRectF(rect.x() + 8, rect.y() + (rect.height() - BADGE) / 2, BADGE, BADGE)
        painter.setPen(Qt.NoPen)
        painter.setBrush(QBrush(QColor(self._rank_color(rank))))
        path = QPainterPath()
        path.addEllipse(badge)
        painter.drawPath(path)
        font = QFont(painter.font())
        font.setPointSize(9)
        font.setBold(True)
        painter.setFont(font)
        painter.setPen(QColor("#ffffff"))
        painter.drawText(badge, Qt.AlignCenter, str(rank))

        # 剧名 + 热度
        text_left = int(badge.right()) + 10
        text_width = max(40, rect.right() - text_left - 10)
        name_font = QFont(painter.font())
        name_font.setPointSize(10)
        name_font.setBold(True)
        painter.setFont(name_font)
        painter.setPen(QColor(DESIGN["text"]))
        metrics = QFontMetrics(name_font)
        title_rect = QRect(text_left, rect.y() + 6, text_width, metrics.height() + 2)
        painter.drawText(title_rect, Qt.AlignLeft | Qt.AlignVCenter,
                         metrics.elidedText(title, Qt.ElideRight, text_width))
        if metric:
            small = QFont(painter.font())
            small.setPointSize(9)
            small.setBold(False)
            painter.setFont(small)
            painter.setPen(QColor(DESIGN["muted2"]))
            sub_metrics = QFontMetrics(small)
            sub_rect = QRect(text_left, rect.y() + 6 + metrics.height() + 2,
                             text_width, sub_metrics.height() + 2)
            painter.drawText(sub_rect, Qt.AlignLeft | Qt.AlignVCenter,
                             sub_metrics.elidedText(metric, Qt.ElideRight, text_width))
        painter.restore()

    @staticmethod
    def _rank_color(rank):
        if rank == 1:
            return DESIGN.get("rank1", "#ff7a45")
        if rank == 2:
            return DESIGN.get("rank2", "#ffc04d")
        if rank == 3:
            return DESIGN.get("rank3", "#5ec2ff")
        return DESIGN.get("surface4", "#2e2e3a")


class RankList(QListWidget):
    """榜单行列表。"""

    dramaActivated = pyqtSignal(object)

    def __init__(self, parent=None):
        super().__init__(parent)
        self._rows = []
        self._hovered = False
        self.setProperty("role", "rankList")
        self.setItemDelegate(_RankDelegate(self))
        self.setSelectionMode(QAbstractItemView.SingleSelection)
        self.setUniformItemSizes(True)
        self.setMouseTracking(True)
        self.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.setVerticalScrollMode(QAbstractItemView.ScrollPerPixel)
        scrollbar = self.verticalScrollBar()
        scrollbar.setProperty("role", "rankScroll")
        scrollbar.setProperty("revealed", False)
        self._scroll_hide = QTimer(self)
        self._scroll_hide.setSingleShot(True)
        self._scroll_hide.timeout.connect(self._hide_scrollbar)
        scrollbar.valueChanged.connect(self._on_scroll)
        scrollbar.sliderPressed.connect(lambda: self._reveal_scrollbar(True))
        scrollbar.sliderReleased.connect(self._hide_scrollbar)
        self.itemClicked.connect(self._activate)

    def enterEvent(self, event):
        self._hovered = True
        self._scroll_hide.stop()
        self._reveal_scrollbar(True)
        super().enterEvent(event)

    def leaveEvent(self, event):
        self._hovered = False
        self._scroll_hide.start(450)
        super().leaveEvent(event)

    def _on_scroll(self, *_args):
        self._reveal_scrollbar(True)
        self._scroll_hide.start(800)

    def _hide_scrollbar(self):
        if not self._hovered and not self.verticalScrollBar().isSliderDown():
            self._reveal_scrollbar(False)

    def _reveal_scrollbar(self, revealed):
        scrollbar = self.verticalScrollBar()
        if bool(scrollbar.property("revealed")) == revealed:
            return
        scrollbar.setProperty("revealed", revealed)
        scrollbar.style().unpolish(scrollbar)
        scrollbar.style().polish(scrollbar)
        scrollbar.update()

    def set_rows(self, rows):
        self.clear()
        self._rows = []
        self.append_rows(rows)
        self.setMinimumHeight(120)

    def append_rows(self, rows):
        seen = {(entry.get("drama") or {}).get("id") for entry in self._rows}
        for entry in rows or []:
            if not isinstance(entry, dict):
                continue
            identifier = (entry.get("drama") or {}).get("id")
            if not identifier or identifier in seen:
                continue
            item = QListWidgetItem()
            item.setData(Qt.UserRole, entry)
            self.addItem(item)
            self._rows.append(entry)
            seen.add(identifier)

    def count(self):
        return len(self._rows)

    def _activate(self, item):
        index = self.row(item)
        if 0 <= index < len(self._rows):
            drama = self._rows[index].get("drama") or {}
            if drama.get("id"):
                self.dramaActivated.emit(drama)
