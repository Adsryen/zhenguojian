# -*- coding: utf-8 -*-
"""海报墙：F2 PosterCard 换代版式（guoapp 质感）。

卡片本体（封面、圆角、名次/更新/VIP 角标、已看置灰、悬停上浮阴影）全部
由 `widgets_guo.PosterCard` 承担；本类只负责：

- QListWidget IconMode 响应式网格（对应原版 ``auto-fill + minmax``，
  列数按宽度算，见 `_recompute_tile`）；
- 顶部钉住的「大卡片位」`set_hero`：guoapp 式横条 banner——高度上限
  HERO_H(260px)、宽随内容区，封面小图(150×210)在右、标题+简介在左，
  简介最多两行省略；窗口矮到放不下 banner+一行卡片时 banner 收缩/隐藏，
  海报墙永远可见（按 research/source-map.md ④ 的版式：首 Tab 顶部大卡）。
- 顶部钉住的刷新按钮（`set_refresh_enabled` + `refreshRequested`，默认
  关闭，不影响日历/创作者页的既有用法）；
- 数据流原样保留：封面仍走 `set_cover_loader` + 外部 `set_pixmap/
  set_failed` 接线，点选防抖 0.4s。

旧 `_PosterDelegate`、`_badge_text`、`_sub_text` 原样保留：
`test_calendar_panel.py` 仍直接构造 delegate 并调用其方法；新 widget
卡片的角标文案也复用同一优先级（`_compose_badge`）。
"""
from __future__ import annotations

import time

from PyQt5.QtCore import QRect, QRectF, QSize, Qt, QTimer, pyqtSignal
from PyQt5.QtGui import (QBrush, QColor, QFont, QFontMetrics, QPainter,
                         QPainterPath, QPen, QPixmap)
from PyQt5.QtWidgets import (QAbstractItemView, QListWidget, QListWidgetItem, QPushButton,
                             QSizePolicy, QStyle, QStyledItemDelegate, QWidget)

from .ui import DESIGN
from .poster_tooltip import poster_tooltip
from .widgets_guo import PosterCard

COVER_RADIUS = 9          # 原版 .rel-cover / 卡片封面圆角
CARD_GAP = 14             # 原版 grid gap（格子与格子之间）
MIN_TILE = 128            # 原版 minmax(128px, 1fr) 的下限（封面宽）
BADGE_H = 20
# 顶部大卡片位（guoapp 式横条 banner）：高度上限 HERO_H，宽随内容区。
# 旧版按宽度 0.30 比例算高（上限 230），在个别分辨率/布局下 banner 会
# 挤压海报墙；改为固定上限 HERO_H + 纵向 Fixed sizePolicy 双保险，任何
# 布局都不可能把它撑高；窗口太矮放不下「banner+至少一行卡片」时 banner
# 先收缩到 HERO_MIN_H，再不够直接隐藏，保证海报墙永远可见可滚动。
HERO_H = 260              # banner 高度上限 px（设计意图 240-280 区间）
HERO_MIN_H = 140          # banner 收缩下限，低于此值隐藏 banner 让位给墙
HERO_PAD = 16             # banner 内边距
HERO_COVER = (150, 210)   # 右侧封面槽，与 widgets.THUMB_SIZE 同口径
REFRESH_H = 30            # 无 hero 时给顶部钉住刷新按钮预留的行高


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


def _wrap_lines(metrics, text, width, max_lines=2):
    """按像素宽折行，最多 max_lines 行；最后一行超宽就省略号。"""
    lines, current = [], ""
    for char in text:
        if current and metrics.horizontalAdvance(current + char) > width:
            lines.append(current)
            current = char
        else:
            current += char
    if current:
        lines.append(current)
    if not lines:
        return []
    if len(lines) > max_lines:
        lines = lines[:max_lines - 1] + ["".join(lines[max_lines - 1:])]
    lines[-1] = metrics.elidedText(lines[-1], Qt.ElideRight, width)
    return lines


class _PosterDelegate(QStyledItemDelegate):
    """一格 = 封面 + 徽标 + 剧名 + 副行，全部自绘。

    旧版渲染路径。widget 卡片上线后本 delegate 不再被装上视图，但
    `test_calendar_panel.py` 直接引用它，保留原样。
    """

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


class _CardSlot(QWidget):
    """一格槽位：内部放一张 PosterCard，格子尺寸变化时重新居中。

    卡片靠自身 move() 做悬停上浮，所以槽位绝不能用布局摆放卡片
    （布局会在上浮后把它按回去）。
    """

    def __init__(self, card, parent=None):
        super().__init__(parent)
        self._card = card
        if card is not None:
            card.setParent(self)

    def place(self):
        card = self._card
        if card is None:
            return
        card.move(max(0, (self.width() - card.width()) // 2),
                  max(0, (self.height() - card.height()) // 2))

    def resizeEvent(self, event):
        self.place()
        super().resizeEvent(event)

    def showEvent(self, event):
        self.place()
        super().showEvent(event)


class _WallHero(QWidget):
    """顶部大卡片位：guoapp 式横条 banner（高度上限 HERO_H，矮窗可让位）。

    版式：卡片底 + 左侧标题/简介（简介最多两行省略）、右侧 150×210 封面
    小图；自绘，点击转成 dramaActivated。纵向 sizePolicy 钉成 Fixed，
    实际高度由 PosterWall._layout_overlays 设定——即使被误放进带 stretch
    的布局里也不会吃空间。
    """

    def __init__(self, owner, parent=None):
        super().__init__(parent)
        self._owner = owner
        self._drama = None
        self._pixmap = None
        self._hover = False
        self.setFixedHeight(HERO_H)
        self.setSizePolicy(QSizePolicy.Preferred, QSizePolicy.Fixed)
        self.setCursor(Qt.PointingHandCursor)
        self.setAttribute(Qt.WA_Hover, True)

    def set_data(self, drama, pixmap=None):
        self._drama = drama or {}
        if pixmap is not None:
            self._pixmap = pixmap
        self.update()

    def set_pixmap(self, pixmap):
        self._pixmap = pixmap
        self.update()

    def enterEvent(self, event):
        self._hover = True
        self.update()
        super().enterEvent(event)

    def leaveEvent(self, event):
        self._hover = False
        self.update()
        super().leaveEvent(event)

    def mouseReleaseEvent(self, event):
        if event.button() == Qt.LeftButton and self.rect().contains(event.pos()):
            self._owner.hero_clicked(self._drama)
        super().mouseReleaseEvent(event)

    def paintEvent(self, event):
        painter = QPainter(self)
        painter.setRenderHint(QPainter.Antialiasing, True)
        painter.setRenderHint(QPainter.SmoothPixmapTransform, True)
        rect = QRectF(self.rect()).adjusted(0.5, 0.5, -0.5, -0.5)
        radius = 12.0  # DESIGN["guo_radius_lg"] 对应 F2 大圆角
        path = QPainterPath()
        path.addRoundedRect(rect, radius, radius)
        painter.setClipPath(path)
        painter.fillRect(rect, QColor(DESIGN["guo_card_bg"]))
        drama = self._drama or {}
        # 右侧封面槽（标准 150×210，垂直居中，圆角裁切，居中缩放裁切）；
        # banner 矮于标准高度时封面等比缩小，保持 5:7 比例。
        cover_h = min(HERO_COVER[1], self.height() - 2 * HERO_PAD)
        cover_w = int(round(cover_h * HERO_COVER[0] / HERO_COVER[1]))
        cover = QRectF(self.width() - HERO_PAD - cover_w,
                       (self.height() - cover_h) / 2.0, cover_w, cover_h)
        cover_clip = QPainterPath()
        cover_clip.addRoundedRect(cover, COVER_RADIUS, COVER_RADIUS)
        pixmap = self._pixmap
        painter.save()
        painter.setClipPath(cover_clip)
        painter.fillRect(cover, QColor(DESIGN["cover_bg"]))
        if pixmap is not None and not pixmap.isNull():
            scaled = pixmap.scaled(cover.size().toSize(), Qt.KeepAspectRatioByExpanding,
                                   Qt.SmoothTransformation)
            left = max(0, (scaled.width() - int(cover.width())) // 2)
            top = max(0, (scaled.height() - int(cover.height())) // 2)
            painter.drawPixmap(int(cover.left()) - left, int(cover.top()) - top, scaled)
        painter.restore()
        # 左侧文字栏：标题 1 行省略 + 简介最多 2 行省略，整体垂直居中
        x = HERO_PAD + 2
        text_width = max(60, int(cover.left() - x - HERO_PAD))
        title = (drama.get("title") or "短剧").strip()
        desc = str(drama.get("description") or drama.get("intro") or "").strip().replace("\n", " ")
        title_font = QFont(self.font())
        title_font.setPixelSize(17)
        title_font.setBold(True)
        desc_font = QFont(self.font())
        desc_font.setPixelSize(12)
        title_metrics = QFontMetrics(title_font)
        desc_metrics = QFontMetrics(desc_font)
        lines = _wrap_lines(desc_metrics, desc, text_width, 2) if desc else []
        desc_h = desc_metrics.height() * len(lines)
        gap = 6 if lines else 0
        block_h = title_metrics.height() + gap + desc_h
        top = int((self.height() - block_h) / 2)
        painter.setFont(title_font)
        painter.setPen(QColor(DESIGN["text"]))
        painter.drawText(QRect(x, top, text_width, title_metrics.height()),
                         Qt.AlignLeft | Qt.AlignVCenter,
                         title_metrics.elidedText(title, Qt.ElideRight, text_width))
        if lines:
            painter.setFont(desc_font)
            painter.setPen(QColor(DESIGN["muted"]))
            y = top + title_metrics.height() + gap
            for line in lines:
                painter.drawText(QRect(x, y, text_width, desc_metrics.height()),
                                 Qt.AlignLeft | Qt.AlignVCenter, line)
                y += desc_metrics.height()
        # 常态不描边：真机反馈带边框的卡片像「中间突出的块」，
        # 与页面脱节；只在悬停时给一圈强调色提示可点击。
        painter.setClipping(False)
        if self._hover:
            painter.setPen(QPen(QColor(DESIGN["guo_accent"]), 1))
            painter.setBrush(Qt.NoBrush)
            painter.drawPath(path)


class PosterWall(QListWidget):
    """封面卡片墙：PosterCard 网格 + 顶部大卡片位 + 钉住刷新按钮。"""

    dramaActivated = pyqtSignal(object)
    refreshRequested = pyqtSignal()   # 顶部钉住刷新按钮被点（需先 set_refresh_enabled）

    def __init__(self, parent=None, covers=None):
        super().__init__(parent)
        self._covers = covers
        self._cover_request = None
        self._items = {}          # id -> (item, drama)
        self._cards = {}          # id -> (_CardSlot, PosterCard)
        self._pixmaps = {}       # id -> QPixmap
        self._interactive = True
        self._last_activated = None
        self._last_activated_at = 0.0
        self._tile = QSize(150, 200)
        self._cover_height = 200
        self._cover_size = (MIN_TILE, int(MIN_TILE * 3 / 2))
        self._score_mode = False
        self._badge_visible = True
        self._dual_badges = False
        self._hero = None
        self._hero_drama = None
        self._refresh_button = None
        self._refresh_enabled = False
        self._top_reserve = 0

        self.setViewMode(QListWidget.IconMode)
        self.setResizeMode(QListWidget.Adjust)
        self.setMovement(QListWidget.Static)
        self.setWrapping(True)
        self.setWordWrap(False)
        self.setUniformItemSizes(True)
        self.setSelectionMode(QAbstractItemView.SingleSelection)
        self.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.setVerticalScrollMode(QAbstractItemView.ScrollPerPixel)
        self.setIconSize(QSize(0, 0))
        self.setMouseTracking(True)
        self.itemClicked.connect(self._activate)
        self.itemActivated.connect(self._activate)
        self._tile_timer = QTimer(self)
        self._tile_timer.setSingleShot(True)
        self._tile_timer.setInterval(130)
        self._tile_timer.timeout.connect(self._recompute_tile)

    # ------------------------------------------------------------- sizing
    def tile_size(self):
        return self._tile

    def hero_active(self):
        return self._hero is not None and self._hero_drama is not None

    def hero_banner_min_viewport(self, rows=2):
        """「hero + rows 行卡片」同时可见所需的视口高度。

        宿主面板（browse_panel）用它决定筛选区让出多少高度；hero 未启用
        时返回纯网格高度，方便同一公式复用。
        """
        base = HERO_H + 8 if self.hero_active() else 0
        return base + rows * (self._tile.height() + CARD_GAP) + 6

    def resizeEvent(self, event):
        super().resizeEvent(event)
        self._layout_overlays()
        self._tile_timer.start()

    def showEvent(self, event):
        super().showEvent(event)
        self._layout_overlays()

    def _recompute_tile(self):
        """按可用宽度算列数和格子尺寸（对应 auto-fill + minmax）。

        原版是 ``repeat(auto-fill, minmax(128px, 1fr))`` 加 ``gap: 14px``。

        Qt 的 IconMode 在每个格子左右各留 2px 内边距，所以它排的列数是
        ``floor(可用宽 / (格子宽 + 2))``。要让整行铺满，格子宽就得取
        ``可用宽 // 列数 - 2``。列数从大到小试，取第一个让封面不小于
        MIN_TILE 的解 —— 封面前一点好看，但也不能宽到一行放不下几张。
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
        cover_width = max(96, cell - CARD_GAP)
        cover_height = int(cover_width * 3 / 2)   # F2 竖版海报 2:3
        size = QSize(cell, cover_height + CARD_GAP + 6)
        if size == self._tile and (cover_width, cover_height) == self._cover_size:
            return
        self._tile = size
        self._cover_height = cover_height
        self._cover_size = (cover_width, cover_height)
        self.setGridSize(size)
        self._rebuild_cards()

    def cover_height(self):
        return self._cover_height

    def set_badge_mode(self, enabled):
        """新剧推荐的原版卡片不压「全 N 集」徽标，只有找剧/追剧卡片需要。"""
        enabled = bool(enabled)
        if enabled != self._badge_visible:
            self._badge_visible = enabled
            self._refresh_all_cards()

    def set_dual_badges(self, enabled):
        enabled = bool(enabled)
        if enabled != self._dual_badges:
            self._dual_badges = enabled
            self._refresh_all_cards()

    def dual_badges_enabled(self):
        return self._dual_badges

    def badge_enabled(self):
        return self._badge_visible

    def score_enabled(self):
        return self._score_mode

    def set_score_mode(self, enabled):
        """新剧推荐卡片在副行下面多一行金色评分（widget 卡片合进角标文案）。"""
        enabled = bool(enabled)
        if enabled != self._score_mode:
            self._score_mode = enabled
            self._refresh_all_cards()

    # ------------------------------------------------------------- overlay
    def set_hero(self, drama):
        """钉在墙顶部的大卡片位；传 None 撤掉。"""
        if drama is None:
            self._hero_drama = None
            if self._hero is not None:
                self._hero.hide()
            self._layout_overlays()
            return
        if self._hero is None:
            self._hero = _WallHero(self, self)
        self._hero_drama = dict(drama)
        self._hero.set_data(self._hero_drama, self.pixmap_for(self._hero_drama))
        self._hero.show()
        self._layout_overlays()

    def hero_clicked(self, drama):
        identifier = (drama or {}).get("id")
        self._emit_activated(identifier)

    def set_refresh_enabled(self, enabled):
        """顶部钉住一个小刷新按钮（默认关，日历/创作者页不受影响）。"""
        enabled = bool(enabled)
        if enabled == self._refresh_enabled:
            return
        self._refresh_enabled = enabled
        if enabled:
            if self._refresh_button is None:
                button = QPushButton("↻", self)
                button.setProperty("guoRole", "groupTab")
                button.setCursor(Qt.PointingHandCursor)
                button.setToolTip("刷新")
                button.clicked.connect(self.refreshRequested.emit)
                self._refresh_button = button
            self._refresh_button.show()
        elif self._refresh_button is not None:
            self._refresh_button.hide()
        self._layout_overlays()

    def _hero_height(self):
        """banner 高度上限 HERO_H(260)，与宽度解耦；窗口太矮时按比例让位，
        保证 hero 下方至少还能看到一行卡片；连让位下限都不够就返回 0 隐藏
        hero——海报墙永远可见、可滚动，绝不允许 hero 吃掉整个内容区。"""
        total = self.height() - 2 * self.frameWidth()
        keep = max(self._tile.height(), MIN_TILE) + CARD_GAP
        h = min(HERO_H, total - 8 - keep)
        return h if h >= HERO_MIN_H else 0

    def _layout_overlays(self):
        frame = self.frameWidth()
        viewport = self.viewport()
        hero_active = self._hero is not None and self._hero_drama is not None
        hero_h = self._hero_height() if hero_active else 0
        if hero_h <= 0:
            hero_active = False
        reserve = hero_h + 8 if hero_h else (REFRESH_H + 6 if self._refresh_enabled else 0)
        if reserve != self._top_reserve:
            self._top_reserve = reserve
            self.setViewportMargins(0, reserve, 0, 0)
        if hero_active:
            self._hero.setFixedHeight(hero_h)
            self._hero.setGeometry(viewport.x(), frame + 3, viewport.width(), hero_h)
            self._hero.show()
            self._hero.raise_()
        elif self._hero is not None:
            self._hero.hide()
        if self._refresh_button is not None and self._refresh_enabled:
            button = self._refresh_button
            button.adjustSize()
            button.move(viewport.x() + viewport.width() - button.width() - 10,
                        frame + 7 if hero_active else viewport.y() + 6)
            button.raise_()

    # -------------------------------------------------------------- data
    def set_cover_loader(self, covers, request=None):
        self._covers = covers
        self._cover_request = request

    def set_items(self, dramas):
        """整墙换一批内容。"""
        self.clear()
        self._items = {}
        self._pixmaps = {}
        self._clear_cards()
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
            item.setSizeHint(self._tile)
            self.addItem(item)
            self._items[drama["id"]] = (item, drama)
            self._attach_card(drama["id"])

    def _attach_card(self, identifier):
        entry = self._items.get(identifier)
        if entry is None or identifier in self._cards:
            return
        item, _drama = entry
        card = PosterCard(cover_size=self._cover_size)
        slot = _CardSlot(card)
        slot.resize(self._tile.width(), self._tile.height())
        card.clicked.connect(lambda _=False, ident=identifier: self._emit_activated(ident))
        self.setItemWidget(item, slot)
        self._cards[identifier] = (slot, card)
        self._apply_card(identifier)
        slot.place()

    def _clear_cards(self):
        cards, self._cards = self._cards, {}
        for slot, _card in cards.values():
            slot.hide()
            slot.deleteLater()

    def _rebuild_cards(self):
        """格子尺寸变了：按新封面尺寸重建所有卡片（数据/封面回填）。"""
        self._clear_cards()
        for identifier in list(self._items):
            entry = self._items.get(identifier)
            if entry is not None:
                entry[0].setSizeHint(self._tile)
                self._attach_card(identifier)
        self.viewport().update()

    def _refresh_all_cards(self):
        for identifier in list(self._cards):
            self._apply_card(identifier)
        self.viewport().update()

    def _compose_badge(self, drama):
        """卡片右上角更新角标文案：评分（如开）+ 集数 + 新剧热度，同旧优先级。"""
        if not self._badge_visible and not self._score_mode:
            return None
        parts = []
        if self._score_mode:
            score = str(drama.get("score") or "").strip()
            if score:
                parts.append(score if score.endswith("分") else score + "分")
        if self._badge_visible:
            badge = _badge_text(drama)
            if badge:
                parts.append(badge)
            if self._dual_badges and drama.get("newSeries"):
                heat = str(drama.get("heat") or "").strip()
                if heat:
                    parts.append(heat)
        return " · ".join(parts) if parts else None

    def _apply_card(self, identifier):
        entry = self._items.get(identifier)
        pair = self._cards.get(identifier)
        if entry is None or pair is None:
            return
        drama = entry[1]
        _slot, card = pair
        rank = drama.get("rank")
        try:
            rank = int(rank) if rank else None
        except (TypeError, ValueError):
            rank = None
        card.set_data(drama.get("title") or "短剧",
                      cover=self._pixmaps.get(identifier),
                      badge_rank=rank,
                      update_badge=self._compose_badge(drama),
                      vip=bool(drama.get("vip")))
        card.set_selected(bool(drama.get("watched")))

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
        self._apply_card(identifier)
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
        if drama_id in self._items:
            self._pixmaps[drama_id] = pixmap
            self._apply_card(drama_id)
        if self._hero is not None and self._hero_drama is not None \
                and self._hero_drama.get("id") == drama_id:
            self._hero.set_pixmap(pixmap)

    def set_failed(self, drama_id):
        self._pixmaps.pop(drama_id, None)
        if drama_id in self._items:
            self._apply_card(drama_id)
        if self._hero is not None and self._hero_drama is not None \
                and self._hero_drama.get("id") == drama_id:
            self._hero.set_pixmap(None)

    def set_interactive(self, enabled):
        self._interactive = bool(enabled)

    def _activate(self, item):
        if not self._interactive or item is None:
            return
        self._emit_activated(item.data(Qt.UserRole))

    def _emit_activated(self, identifier):
        if not self._interactive or not identifier:
            return
        entry = self._items.get(identifier)
        if entry is None:
            return
        now = time.monotonic()
        if identifier == self._last_activated and now - self._last_activated_at < 0.4:
            return
        self._last_activated = identifier
        self._last_activated_at = now
        self.dramaActivated.emit(entry[1])

    def reload_theme(self):
        self.viewport().update()
        if self._hero is not None:
            self._hero.update()
        if self._refresh_button is not None:
            self._refresh_button.raise_()
