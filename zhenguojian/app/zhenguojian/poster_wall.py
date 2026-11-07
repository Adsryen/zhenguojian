# -*- coding: utf-8 -*-
"""海报墙：照搬原版红果客户端的卡片样式。

原版（Vue + WebView2）的卡片墙是这样画的：3:4 竖版封面、圆角 9px、
封面左下角压一个「全 102 集」的半透明徽标，封面下面是 600 字重的剧名，
再下面一行 12px 的弱化副信息（题材 / 热度）。卡片之间的间距 14px，
列宽按 `repeat(auto-fill, minmax(132px, 1fr))` 自适应。

Qt 里没有 CSS 网格，所以这里用 QListWidget 的 IconMode + 自绘 delegate：
列数按控件宽度算，单元格尺寸跟着变，效果和 auto-fill 一致。
"""
from __future__ import annotations

import time

from PyQt5.QtCore import QRect, QRectF, QSize, Qt, pyqtSignal
from PyQt5.QtGui import (QBrush, QColor, QFont, QFontMetrics, QPainter, QPainterPath,
                         QPen)
from PyQt5.QtWidgets import (QAbstractItemView, QListWidget, QListWidgetItem,
                             QStyle, QStyledItemDelegate)

from .ui import DESIGN
from .poster_tooltip import poster_tooltip

COVER_RADIUS = 9          # 原版 .rel-cover / 卡片封面圆角
CARD_GAP = 14             # 原版 grid gap（格子与格子之间）
MIN_TILE = 128            # 原版 minmax(128px, 1fr) 的下限（封面宽）
BADGE_H = 20


def _badge_text(drama):
    """封面左下角的徽标：集数优先，其次是更新状态。"""
    episodes = drama.get("episodes")
    status = (drama.get("releaseStatus") or "").strip()
    if drama.get("newSeries") and drama.get("episodeText"):
        return str(drama["episodeText"])
    if episodes:
        if status == "ongoing":
            return "更新至%s集" % episodes
        if drama.get("newSeries"):
            return "共%s集" % episodes
        return "全%s集" % episodes
    for key in ("episodeText", "remark", "publishText"):
        value = drama.get(key)
        if value:
            return str(value)
    return ""


def _sub_text(drama, hide_heat=False):
    """封面下面那行弱化副信息：题材 + 热度。"""
    parts = []
    category = (drama.get("category") or "").strip()
    if category:
        parts.append(category)
    heat = "" if hide_heat or drama.get("newSeries") else str(drama.get("heat") or "").strip()
    if heat:
        parts.append(heat)
    if not parts:
        tags = [str(tag) for tag in (drama.get("tags") or []) if str(tag).strip()]
        parts.extend(tags[:2])
    return " · ".join(parts[:2])


class _PosterDelegate(QStyledItemDelegate):
    """一格 = 封面 + 徽标 + 剧名 + 副行，全部自绘。"""

    def __init__(self, owner, parent=None):
        super().__init__(parent)
        self._owner = owner

    def sizeHint(self, option, index):
        return self._owner.tile_size()

    def paint(self, painter, option, index):
        drama = self._owner.drama_at(index)
        if drama is None:
            return
        painter.save()
        painter.setRenderHint(QPainter.Antialiasing, True)
        painter.setRenderHint(QPainter.SmoothPixmapTransform, True)
        tile = self._owner.tile_size()
        rect = QRect(option.rect)
        # 格子 = 封面 + 四周留白。Qt 的 IconMode 不会自己加列间距
        # （setSpacing() 在 IconMode 下无效），所以留白只能画在格子内部：
        # 封面比格子窄 CARD_GAP，两列之间就有了 2×CARD_GAP 的视觉间距。
        pad = CARD_GAP // 2
        cover = QRect(rect.x() + pad, rect.y() + pad,
                      tile.width() - 2 * pad, self._owner.cover_height())
        painter.setClipRect(rect)
        if option.state & QStyle.State_MouseOver:
            painter.setPen(Qt.NoPen)
            painter.setBrush(QColor(DESIGN["accent_soft"]))
            painter.drawRoundedRect(QRectF(rect.adjusted(2, 2, -2, -2)), 8, 8)
        self._paint_cover(painter, cover, self._owner.pixmap_for(drama))
        if self._owner.badge_enabled():
            if drama.get("newSeries") or self._owner.dual_badges_enabled():
                self._paint_new_series_badges(painter, cover, drama)
            else:
                badge = _badge_text(drama)
                if badge:
                    self._paint_badge(painter, cover, badge)
        painter.setClipRect(rect)
        self._paint_text(painter, cover, drama)
        painter.restore()

    # ------------------------------------------------------------- pieces
    def _paint_cover(self, painter, cover, pixmap):
        path = QPainterPath()
        path.addRoundedRect(QRectF(cover), COVER_RADIUS, COVER_RADIUS)
        painter.save()
        painter.setClipPath(path)
        painter.fillRect(cover, QColor(DESIGN["cover_bg"]))
        if pixmap is not None and not pixmap.isNull():
            scaled = pixmap.scaled(cover.size(), Qt.KeepAspectRatioByExpanding,
                                   Qt.SmoothTransformation)
            left = max(0, (scaled.width() - cover.width()) // 2)
            top = max(0, (scaled.height() - cover.height()) // 2)
            painter.drawPixmap(cover.x() - left, cover.y() - top, scaled)
        painter.restore()
        painter.setPen(QPen(QColor(DESIGN["border"]), 1))
        painter.setBrush(Qt.NoBrush)
        painter.drawPath(path)

    def _paint_badge(self, painter, cover, text):
        font = QFont(painter.font())
        font.setPointSize(8)
        font.setBold(True)
        metrics = QFontMetrics(font)
        width = metrics.horizontalAdvance(text) + 14
        height = BADGE_H
        rect = QRectF(cover.x() + 6, cover.bottom() - height - 6, width, height)
        painter.setPen(Qt.NoPen)
        painter.setBrush(QBrush(QColor(0, 0, 0, 158)))
        path = QPainterPath()
        path.addRoundedRect(rect, 6, 6)
        painter.drawPath(path)
        painter.setFont(font)
        painter.setPen(QColor("#ffffff"))
        painter.drawText(rect, Qt.AlignCenter, text)

    def _paint_new_series_badges(self, painter, cover, drama):
        font = QFont(painter.font())
        font.setPixelSize(11)
        font.setBold(True)
        metrics = QFontMetrics(font)
        heat = str(drama.get("heat") or "").strip()
        count = _badge_text(drama)
        left = cover.left() + 6
        right = cover.right() - 6
        height = max(18, metrics.height() + 4)
        top = cover.bottom() - height - 5
        if count:
            count_width = min(metrics.horizontalAdvance(count) + 12, max(0, cover.width() - 12))
            if heat:
                count_width = min(count_width, max(0, (right - left - 4) // 2))
            if count_width > 12:
                count_rect = QRectF(right - count_width, top, count_width, height)
                self._paint_overlay(painter, count_rect,
                                    metrics.elidedText(count, Qt.ElideRight, int(count_width) - 8), font)
                right = int(count_rect.left()) - 4
        if heat:
            heat_width = min(metrics.horizontalAdvance(heat) + 12, right - left)
            if heat_width > 12:
                heat_rect = QRectF(left, top, heat_width, height)
                self._paint_overlay(painter, heat_rect,
                                    metrics.elidedText(heat, Qt.ElideRight, heat_width - 8), font)

    @staticmethod
    def _paint_overlay(painter, rect, text, font):
        painter.save()
        painter.setPen(Qt.NoPen)
        painter.setBrush(QColor(0, 0, 0, 158))
        painter.drawRoundedRect(rect, 6, 6)
        painter.setFont(font)
        painter.setPen(QColor("#ffffff"))
        painter.drawText(rect, Qt.AlignCenter, text)
        painter.restore()

    def _paint_text(self, painter, cover, drama):
        title = (drama.get("title") or "短剧").strip()
        sub = _sub_text(drama, hide_heat=self._owner.dual_badges_enabled())
        font = QFont(painter.font())
        font.setPointSize(10)
        font.setBold(True)
        metrics = QFontMetrics(font)
        lines = self._wrap(metrics, title, cover.width())
        top = cover.bottom() + 6
        painter.setFont(font)
        painter.setPen(QColor(DESIGN["text"]))
        for line in lines[:2]:
            painter.drawText(QRect(cover.x(), top, cover.width(), metrics.height()),
                             Qt.AlignLeft | Qt.AlignVCenter, line)
            top += metrics.height() + 1
        if sub:
            small = QFont(painter.font())
            small.setPointSize(9)
            small.setBold(False)
            painter.setFont(small)
            painter.setPen(QColor(DESIGN["muted"] if drama.get("newSeries") else DESIGN["muted2"]))
            small_metrics = QFontMetrics(small)
            painter.drawText(QRect(cover.x(), top, cover.width(), small_metrics.height()),
                             Qt.AlignLeft | Qt.AlignVCenter,
                             small_metrics.elidedText(sub, Qt.ElideRight, cover.width()))
            top += small_metrics.height() + 2
        score = str(drama.get("score") or "").strip() if self._owner.score_enabled() else ""
        if score:
            hot = QFont(painter.font())
            hot.setPointSize(9)
            hot.setBold(True)
            painter.setFont(hot)
            painter.setPen(QColor(DESIGN["score"]))
            text = score if score.endswith("分") else score + "分"
            painter.drawText(QRect(cover.x(), top, cover.width(),
                                   QFontMetrics(hot).height()),
                             Qt.AlignLeft | Qt.AlignVCenter, text)

    @staticmethod
    def _wrap(metrics, text, width):
        lines, current = [], ""
        for char in text:
            if metrics.horizontalAdvance(current + char) > width and current:
                lines.append(current)
                current = char
                if len(lines) == 2:
                    return lines
            else:
                current += char
        if current:
            lines.append(current)
        return lines


class PosterWall(QListWidget):
    """封面卡片墙：可响应式换列，点一下出信号。"""

    dramaActivated = pyqtSignal(object)

    def __init__(self, parent=None, covers=None):
        super().__init__(parent)
        self._covers = covers
        self._cover_request = None
        self._items = {}          # id -> (item, drama)
        self._pixmaps = {}       # id -> QPixmap
        self._interactive = True
        self._last_activated = None
        self._last_activated_at = 0.0
        self._tile = QSize(150, 200)
        self._cover_height = 200
        self._score_mode = False
        self._badge_visible = True
        self._dual_badges = False

        self.setViewMode(QListWidget.IconMode)
        self.setResizeMode(QListWidget.Adjust)
        self.setMovement(QListWidget.Static)
        self.setWrapping(True)
        self.setWordWrap(False)
        self.setUniformItemSizes(True)
        self.setSelectionMode(QAbstractItemView.SingleSelection)
        self.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.setVerticalScrollMode(QAbstractItemView.ScrollPerPixel)
        self.setItemDelegate(_PosterDelegate(self, self))
        self.setMouseTracking(True)
        self.itemClicked.connect(self._activate)
        self.itemActivated.connect(self._activate)

    # ------------------------------------------------------------- sizing
    def tile_size(self):
        return self._tile

    def resizeEvent(self, event):
        super().resizeEvent(event)
        self._recompute_tile()

    def _recompute_tile(self):
        """按可用宽度算列数和格子尺寸（对应 auto-fill + minmax）。

        原版是 ``repeat(auto-fill, minmax(128px, 1fr))`` 加 ``gap: 14px``。

        Qt 的 IconMode 在每个格子左右各留 2px 内边距，所以它排的列数是
        ``floor(可用宽 / (格子宽 + 2))``。要让整行铺满，格子宽就得取
        ``可用宽 // 列数 - 2``。列数从大到小试，取第一个让封面不小于
        MIN_TILE 的解 —— 封面前宽一点好看，但也不能宽到一行放不下几张。
        """
        width = max(200, self.viewport().width())
        cell = MIN_TILE + CARD_GAP
        for columns in range(12, 1, -1):
            candidate = width // columns - 2
            if candidate < MIN_TILE + CARD_GAP:
                continue
            cell = candidate
            break
        self._apply_cell(cell)

    def _apply_cell(self, cell):
        cover_width = cell - CARD_GAP
        cover_height = int(cover_width * 4 / 3)
        # 格子高 = 封面 + 两侧留白 + 两行标题 + 一行副行（新剧卡片再加评分行）
        height = cover_height + CARD_GAP + 8 + 2 * 17 + 15 + 8
        if self._score_mode:
            height += 18
        size = QSize(cell, height)
        if size != self._tile:
            self._tile = size
            self._cover_height = cover_height
            self.setGridSize(size)
            self.setIconSize(QSize(0, 0))
            self.viewport().update()

    def cover_height(self):
        return self._cover_height

    def set_badge_mode(self, enabled):
        """新剧推荐的原版卡片不压「全 N 集」徽标，只有找剧/追剧卡片需要。"""
        enabled = bool(enabled)
        if enabled != self._badge_visible:
            self._badge_visible = enabled
            self.viewport().update()

    def set_dual_badges(self, enabled):
        self._dual_badges = bool(enabled)
        self.viewport().update()

    def dual_badges_enabled(self):
        return self._dual_badges

    def badge_enabled(self):
        return self._badge_visible

    def score_enabled(self):
        return self._score_mode

    def set_score_mode(self, enabled):
        """新剧推荐卡片在副行下面多一行金色评分。"""
        enabled = bool(enabled)
        if enabled != self._score_mode:
            self._score_mode = enabled
            self._apply_cell(self._tile.width())

    # -------------------------------------------------------------- data
    def set_cover_loader(self, covers, request=None):
        self._covers = covers
        self._cover_request = request

    def set_items(self, dramas):
        """整墙换一批内容。"""
        self.clear()
        self._items = {}
        self._pixmaps = {}
        batch = []
        seen = set()
        for drama in dramas:
            identifier = drama.get("id")
            if identifier and identifier not in seen:
                batch.append(drama)
                seen.add(identifier)
        self._add_batch(batch)
        self._recompute_tile()

    def append_items(self, dramas):
        """在已有卡片后面追加一页（新剧推荐的无限滚动）。"""
        batch = []
        seen = set(self._items)
        for drama in dramas:
            identifier = drama.get("id")
            if identifier and identifier not in seen:
                batch.append(drama)
                seen.add(identifier)
        if not batch:
            return
        self._add_batch(batch)
        self._recompute_tile()

    def _add_batch(self, batch):
        missing = []
        if self._covers is not None and self._cover_request:
            for drama in batch:
                identifier = drama.get("id")
                pixmap = self._covers.cached(identifier)
                if pixmap is not None:
                    self._pixmaps[identifier] = pixmap
                elif not self._covers.known(identifier):
                    missing.append(drama)
            if missing:
                self._cover_request(missing)
        for drama in batch:
            item = QListWidgetItem()
            item.setData(Qt.UserRole, drama.get("id"))
            item.setToolTip(poster_tooltip(drama))
            self.addItem(item)
            self._items[drama["id"]] = (item, drama)

    def count(self):
        return len(self._items)

    def dramas(self):
        return [drama for _, drama in self._items.values()]

    def update_drama(self, drama):
        identifier = drama.get("id")
        entry = self._items.get(identifier)
        if not entry:
            return False
        item, old = entry
        merged = dict(old)
        merged.update({key: value for key, value in drama.items() if value not in (None, "", [])})
        self._items[identifier] = (item, merged)
        item.setToolTip(poster_tooltip(merged))
        self.viewport().update(self.visualItemRect(item))
        return True

    def visible_dramas(self):
        bounds = self.viewport().rect()
        if not self.isVisible() or bounds.isEmpty():
            return []
        return [drama for item, drama in self._items.values()
                if self.visualItemRect(item).intersects(bounds)]

    def drama_at(self, index):
        row = index.row() if hasattr(index, "row") else int(index)
        item = self.item(row)
        if item is None:
            return None
        entry = self._items.get(item.data(Qt.UserRole))
        return entry[1] if entry else None

    def pixmap_for(self, drama):
        identifier = drama.get("id")
        if identifier in self._pixmaps:
            return self._pixmaps[identifier]
        if self._covers is not None:
            pixmap = self._covers.cached(identifier)
            if pixmap is not None:
                self._pixmaps[identifier] = pixmap
                return pixmap
        return None

    def set_pixmap(self, drama_id, pixmap):
        if drama_id not in self._items:
            return
        self._pixmaps[drama_id] = pixmap
        item = self._items[drama_id][0]
        row = self.row(item)
        if row >= 0:
            self.viewport().update()

    def set_failed(self, drama_id):
        self._pixmaps.pop(drama_id, None)
        item = self._items.get(drama_id)
        if item:
            row = self.row(item[0])
            if row >= 0:
                self.viewport().update()

    def set_interactive(self, enabled):
        self._interactive = bool(enabled)

    def _activate(self, item):
        if not self._interactive:
            return
        now = time.monotonic()
        if item is self._last_activated and now - self._last_activated_at < 0.4:
            return
        identifier = item.data(Qt.UserRole)
        entry = self._items.get(identifier)
        if entry is None:
            return
        self._last_activated = item
        self._last_activated_at = now
        self.dramaActivated.emit(entry[1])

    def reload_theme(self):
        self.viewport().update()
