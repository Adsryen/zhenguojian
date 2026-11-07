# -*- coding: utf-8 -*-
"""排行榜列表：guoapp 版式，名次徽标统一走 F2 PosterCard。

旧版是一行「名次圆徽 + 剧名 + 热度」的自绘 delegate；新版每行换成
「竖版小海报卡（badge_rank 名次角标，rank1/2/3 配色由 ui.py 的
guoRole=rankBadge QSS 统一处理）+ 右侧剧名 / 热度指标」。

对外 API 不变：无参构造、set_rows/append_rows/count、dramaActivated；
新增可选 `set_cover_loader(covers, request)` + `set_pixmap/set_failed`
接线封面（main.py 暂未接，行为与旧版一致，只是多了缩略图位）。
"""
from __future__ import annotations

from PyQt5.QtCore import QRect, QRectF, QSize, Qt, QTimer, pyqtSignal
from PyQt5.QtGui import QBrush, QColor, QFont, QFontMetrics, QPainter, QPainterPath
from PyQt5.QtWidgets import (QAbstractItemView, QHBoxLayout, QLabel, QListWidget,
                             QListWidgetItem, QStyle, QStyledItemDelegate, QVBoxLayout, QWidget)

from .ui import DESIGN
from .widgets_guo import PosterCard

ROW_HEIGHT = 92      # 新版行高（海报小卡 + 右侧信息列）
BADGE = 26
CARD_SIZE = (52, 78)  # 行内小卡封面尺寸（2:3，与主墙同比例）


class _RankDelegate(QStyledItemDelegate):
    """旧版自绘行：一行 = 名次徽标 + 剧名 + 热度指标。

    widget 行上线后不再挂到视图上，但保留类与配色逻辑，兼容外部引用。
    """

    def sizeHint(self, option, index):
        return QSize(300, 52)

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


class _RankRow(QWidget):
    """一行榜单：左边小海报卡（名次角标走 PosterCard badge_rank），
    右边剧名 + 热度指标；整行可点。"""

    def __init__(self, entry, activate, parent=None):
        super().__init__(parent)
        self._entry = entry
        self._activate = activate
        drama = entry.get("drama") or {}
        rank = entry.get("rank")
        try:
            rank = int(rank) if rank else None
        except (TypeError, ValueError):
            rank = None

        self._card = PosterCard(cover_size=CARD_SIZE)
        # 行内小卡只当缩略图 + 名次角标：标题走右侧 QLabel，卡内不叠字
        self._card.set_data("", cover=None,
                            badge_rank=rank, vip=bool(drama.get("vip")))
        self._card.clicked.connect(lambda _=False: self._fire())

        self._title = QLabel((drama.get("title") or "短剧").strip())
        self._title.setProperty("role", "emptyTitle")
        self._metric = QLabel(entry.get("metric") or "")
        self._metric.setProperty("role", "hint")
        self._texts = ((self._title, self._title.text()), (self._metric, self._metric.text()))

        info = QVBoxLayout()
        info.setContentsMargins(0, 0, 0, 0)
        info.setSpacing(3)
        info.addStretch(1)
        info.addWidget(self._title)
        info.addWidget(self._metric)
        info.addStretch(1)
        row = QHBoxLayout(self)
        row.setContentsMargins(6, 6, 12, 6)
        row.setSpacing(12)
        row.addWidget(self._card, 0, Qt.AlignVCenter)
        row.addLayout(info, 1)

    def apply_cover(self, pixmap):
        self._card.set_data("", cover=pixmap,
                            badge_rank=self._badge_rank(),
                            vip=bool((self._entry.get("drama") or {}).get("vip")))

    def _badge_rank(self):
        rank = self._entry.get("rank")
        try:
            return int(rank) if rank else None
        except (TypeError, ValueError):
            return None

    def set_metric(self, text):
        text = text or ""
        self._texts = ((self._title, self._texts[0][1]), (self._metric, text))
        self._metric.setText(text)

    def _fire(self):
        self._activate(self._entry)

    def mousePressEvent(self, event):
        if event.button() == Qt.LeftButton:
            self._fire()
            event.accept()
            return
        super().mousePressEvent(event)

    def resizeEvent(self, event):
        available = max(40, self.width() - self._card.width() - 46)
        for label, full in self._texts:
            metrics = QFontMetrics(label.font())
            label.setText(metrics.elidedText(full, Qt.ElideRight, available))
        super().resizeEvent(event)


class RankList(QListWidget):
    """榜单行列表。"""

    dramaActivated = pyqtSignal(object)

    def __init__(self, parent=None):
        super().__init__(parent)
        self._rows = []
        self._widgets = {}      # drama id -> _RankRow
        self._pixmaps = {}      # drama id -> QPixmap（可选封面接线）
        self._covers = None
        self._cover_request = None
        self._hovered = False
        self.setProperty("role", "rankList")
        # widget 行自绘交互：禁用选中态，避免全局 item:selected 底色透出
        self.setSelectionMode(QAbstractItemView.NoSelection)
        self.setUniformItemSizes(True)
        self.setMouseTracking(True)
        self.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.setVerticalScrollMode(QAbstractItemView.ScrollPerPixel)
        self.setIconSize(QSize(0, 0))
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

    # ---------------------------------------------------------------- data
    def set_cover_loader(self, covers, request=None):
        """可选：接入 CoverLoader（同海报墙的路子）；不调用则维持无封面。"""
        self._covers = covers
        self._cover_request = request
        missing = []
        for entry in self._rows:
            drama = entry.get("drama") or {}
            identifier = drama.get("id")
            if not identifier or identifier in self._pixmaps:
                continue
            if covers is not None:
                cached = covers.cached(identifier)
                if cached is not None:
                    self._pixmaps[identifier] = cached
                    row = self._widgets.get(identifier)
                    if row is not None:
                        row.apply_cover(cached)
                    continue
                if not covers.known(identifier):
                    missing.append(drama)
        if missing and self._cover_request:
            self._cover_request(missing)

    def set_rows(self, rows):
        for row in self._widgets.values():
            row.hide()
            row.deleteLater()
        self._widgets = {}
        self._pixmaps = {}
        self.clear()
        self._rows = []
        self.append_rows(rows)
        self.setMinimumHeight(120)

    def append_rows(self, rows):
        seen = {(entry.get("drama") or {}).get("id") for entry in self._rows}
        missing = []
        for entry in rows or []:
            if not isinstance(entry, dict):
                continue
            drama = entry.get("drama") or {}
            identifier = drama.get("id")
            if not identifier or identifier in seen:
                continue
            item = QListWidgetItem()
            item.setData(Qt.UserRole, entry)
            item.setSizeHint(QSize(300, ROW_HEIGHT))
            row = _RankRow(entry, self._fire_entry, self)
            pixmap = self._pixmaps.get(identifier)
            if pixmap is None and self._covers is not None:
                pixmap = self._covers.cached(identifier)
                if pixmap is not None:
                    self._pixmaps[identifier] = pixmap
                elif not self._covers.known(identifier) and self._cover_request:
                    missing.append(drama)
            row.apply_cover(pixmap)
            self.addItem(item)
            self.setItemWidget(item, row)
            self._widgets[identifier] = row
            self._rows.append(entry)
            seen.add(identifier)
        if missing and self._cover_request:
            self._cover_request(missing)

    def set_pixmap(self, drama_id, pixmap):
        self._pixmaps[drama_id] = pixmap
        row = self._widgets.get(drama_id)
        if row is not None:
            row.apply_cover(pixmap)

    def set_failed(self, drama_id):
        self._pixmaps.pop(drama_id, None)
        row = self._widgets.get(drama_id)
        if row is not None:
            row.apply_cover(None)

    def count(self):
        return len(self._rows)

    # ------------------------------------------------------------ activate
    def _fire_entry(self, entry):
        drama = (entry or {}).get("drama") or {}
        if drama.get("id"):
            self.clearSelection()
            self.dramaActivated.emit(drama)

    def _activate(self, item):
        if item is None:
            return
        entry = item.data(Qt.UserRole)
        if isinstance(entry, dict):
            self._fire_entry(entry)
