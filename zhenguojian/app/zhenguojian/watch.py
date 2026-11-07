# -*- coding: utf-8 -*-
"""追剧 / 观看历史面板（guo- 呈现层）。

版式对齐 guoapp（research/source-map.md ④节）：
- 追剧（follow）：海报网格 PosterCard；「有更新」条目置顶并在封面右上挂
  「更新到N集」角标；顶部一排站源筛选 chips——筛选只是本地过滤已有数据，
  不发任何网络请求。站源展示名走 12 源硬编码回退表（①节）。
- 观看历史（history）：SectionedListView 按「今天 / 昨天 / 更早」分段，
  时间取记录自带的 updated 字段，按本地零点分桶（与 guoapp history_page 一致）。

数据流与旧版完全相同：读 WatchStore（follows()/history()/watched()），封面走
CoverLoader（只在本页可见时排队），playRequested / localPlayRequested 的发射
语义不变；仍是「单击选中、双击继续播放」，避免想删记录却先开始播放。
"""
from __future__ import annotations

import datetime
import os
import time

from PyQt5.QtCore import Qt, pyqtSignal
from PyQt5.QtGui import QColor, QFont, QPainter, QPainterPath
from PyQt5.QtWidgets import (QApplication, QButtonGroup, QFrame, QHBoxLayout,
                             QLabel, QPushButton, QScrollArea, QVBoxLayout,
                             QWidget)

from . import watchstore
from .flow_layout import FlowLayout
from .poster_tooltip import poster_tooltip
from .ui import DESIGN
from .widgets_guo import PosterCard, SectionedListView

# 展示名回退表（source-map.md ①节，guoapp 实际展示 12 源；「黄果」是分组名，
# 三个变体 huangguo-video / huangguoai / cloudfront 归一个 chip）。
# core 的站源注册表由 F1 在改，这里不 import，硬编码回退即可。
SOURCE_GROUP_NAMES = {
    "hongguo": "红果",
    "huangguo": "黄果",
    "huangdou": "黄豆",
    "yeguo": "野果",
    "huangju": "剧果",
    "dsd": "帝果",
    "jinguo": "禁果",
    "jinguoduanju": "禁果短剧",
    "heitao": "黑桃",
    "sorani": "青空",
    "hanxiaoquan": "韩小圈",
    "guipian": "鬼片",
}
SOURCE_ORDER = tuple(SOURCE_GROUP_NAMES)
SOURCE_ALIASES = {
    "huangguo-video": "huangguo",
    "huangguoai": "huangguo",
    "cloudfront": "huangguo",
    "tideember": "huangdou",
}

HISTORY_BUCKETS = ("今天", "昨天", "更早")
CARD_COVER = (110, 164)   # 2:3 海报，guoapp ProgressPoster 比例
CARD_SPACING = 18         # ④节：网格间距 18


def _short_time(stamp):
    try:
        return time.strftime("%m-%d %H:%M", time.localtime(float(stamp)))
    except (TypeError, ValueError):
        return ""


def _source_group(drama):
    """记录归属的筛选组 key：drama.source → 别名归组 → id 前缀兜底。"""
    drama = drama or {}
    source = str(drama.get("source") or "").strip().lower()
    if not source:
        prefix = str(drama.get("id") or "").split(":", 1)
        if len(prefix) == 2 and prefix[0] and prefix[0] != "local":
            source = prefix[0]
    if not source:
        return ""
    return SOURCE_ALIASES.get(source, source)


def _group_label(key):
    return SOURCE_GROUP_NAMES.get(key) or key


def _day_bucket(stamp):
    """0=今天 1=昨天 2=更早；无效时间戳归入更早。"""
    try:
        then = datetime.date.fromtimestamp(float(stamp or 0))
    except (TypeError, ValueError, OSError):
        return 2
    today = datetime.date.today()
    delta = (today - then).days
    if delta <= 0:
        return 0
    if delta == 1:
        return 1
    return 2


def _radius(token, fallback):
    try:
        return int(str(DESIGN.get(token, "")).replace("px", ""))
    except (TypeError, ValueError):
        return fallback


class _MiniCover(QWidget):
    """历史条目左侧的小圆角封面（6px，自绘，随主题即时生效）。"""

    def __init__(self, size=(58, 82), parent=None):
        super().__init__(parent)
        self._pixmap = None
        self.setFixedSize(*size)

    def set_pixmap(self, pixmap):
        self._pixmap = pixmap
        self.update()

    def paintEvent(self, _event):
        painter = QPainter(self)
        try:
            painter.setRenderHint(QPainter.Antialiasing, True)
            painter.setRenderHint(QPainter.SmoothPixmapTransform, True)
            radius = _radius("guo_radius", 6)
            path = QPainterPath()
            path.addRoundedRect(0, 0, self.width(), self.height(), radius, radius)
            painter.setClipPath(path)
            painter.fillRect(self.rect(), QColor(DESIGN["cover_bg"]))
            if self._pixmap is not None and not self._pixmap.isNull():
                scaled = self._pixmap.scaled(self.size(), Qt.KeepAspectRatioByExpanding,
                                             Qt.SmoothTransformation)
                left = max(0, (scaled.width() - self.width()) // 2)
                top = max(0, (scaled.height() - self.height()) // 2)
                painter.drawPixmap(-left, -top, scaled)
            painter.setClipping(False)
            painter.setPen(QColor(DESIGN["guo_border"]))
            painter.setBrush(Qt.NoBrush)
            painter.drawRoundedRect(0, 0, self.width() - 1, self.height() - 1,
                                    radius, radius)
        finally:
            painter.end()


class _HistoryRow(QFrame):
    """观看历史的一行：小封面 + 标题/进度，单击选中、双击续播。"""

    clicked = pyqtSignal(str)
    doubleClicked = pyqtSignal(str)

    def __init__(self, row, meta, parent=None):
        super().__init__(parent)
        self.row_id = row["id"]
        self.setProperty("guoRole", "posterCard")
        self.setProperty("selected", "false")
        self.setAttribute(Qt.WA_StyledBackground, True)
        self.setCursor(Qt.PointingHandCursor)
        self.setFixedHeight(94)

        layout = QHBoxLayout(self)
        layout.setContentsMargins(12, 8, 12, 8)
        layout.setSpacing(12)
        self.cover = _MiniCover()
        layout.addWidget(self.cover, 0, Qt.AlignVCenter)

        text = QVBoxLayout()
        text.setSpacing(4)
        drama = row.get("drama") or {}
        self.title = QLabel((drama.get("title") or "短剧").strip())
        font = QFont(self.title.font())
        font.setPixelSize(14)
        font.setBold(True)
        self.title.setFont(font)
        self.meta = QLabel(meta)
        self.meta.setProperty("role", "hint")
        text.addWidget(self.title)
        text.addWidget(self.meta)
        text.addStretch(1)
        layout.addLayout(text, 1)
        self.setToolTip(poster_tooltip(drama))

    def set_meta(self, meta):
        if self.meta.text() != meta:
            self.meta.setText(meta)

    def set_pixmap(self, pixmap):
        self.cover.set_pixmap(pixmap)

    def set_selected(self, selected):
        if self.property("selected") != ("true" if selected else "false"):
            self.setProperty("selected", "true" if selected else "false")
            self.style().unpolish(self)
            self.style().polish(self)

    def mouseReleaseEvent(self, event):
        if event.button() == Qt.LeftButton:
            self.clicked.emit(self.row_id)
        super().mouseReleaseEvent(event)

    def mouseDoubleClickEvent(self, event):
        if event.button() == Qt.LeftButton:
            self.doubleClicked.emit(self.row_id)
        super().mouseDoubleClickEvent(event)


class WatchPanel(QWidget):
    """mode="follow" 追剧网格，"history" 观看历史分段列表。

    单击只选中条目——底部按钮作用于选中项；双击继续播放。（以前单击即
    播放，想「删除记录」时先播起来了。）
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
        self._selected_id = None
        self._source_filter = None       # None = 全部
        self._chip_groups = None         # 上次渲染 chips 的组集合
        self._chips = {}                 # group key -> QPushButton（None 键 = 全部）
        self._cards = {}                 # follow: id -> PosterCard
        self._row_widgets = {}           # history: id -> _HistoryRow
        self._last_click = (None, 0.0)

        # ------------------------------------------------------------ 顶栏
        self.chip_bar = None
        if mode == "follow":
            host = QWidget()
            self._chip_flow = FlowLayout(host, spacing=8)
            self._chip_group = QButtonGroup(self)
            self._chip_group.setExclusive(True)
            self.chip_bar = QScrollArea()
            self.chip_bar.setProperty("guoRole", "bare")
            self.chip_bar.setWidgetResizable(True)
            self.chip_bar.setFixedHeight(38)
            self.chip_bar.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
            self.chip_bar.setWidget(host)
            self.chip_bar.setVisible(False)

        # ------------------------------------------------------------ 主体
        if mode == "history":
            self.sections = SectionedListView()
            body = self.sections
        else:
            self.grid_scroll = QScrollArea()
            self.grid_scroll.setProperty("guoRole", "bare")
            self.grid_scroll.setWidgetResizable(True)
            self.grid_scroll.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
            self.grid_host = QWidget()
            self.grid_flow = FlowLayout(self.grid_host, spacing=CARD_SPACING)
            self.grid_host.setContentsMargins(0, 0, 0, 0)
            self.grid_scroll.setWidget(self.grid_host)
            body = self.grid_scroll

        # ------------------------------------------------------------ 底栏
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
        self.play_button.clicked.connect(lambda: self._open_current(self._selected_id))
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
        layout.setSpacing(6)
        if self.chip_bar is not None:
            layout.addWidget(self.chip_bar)
        layout.addWidget(body, 1)
        layout.addLayout(buttons)

        store.changed.connect(self.refresh)
        covers.loaded.connect(self._on_cover)
        self.refresh()

    # ----------------------------------------------------------------- rows
    def _visible_rows(self):
        """追剧：先按「有更新」置顶（updateAt 新→旧），其余按加入时间；
        再按站源 chips 本地过滤（不发网络请求）。历史按 updated 新→旧。
        """
        if self._mode != "follow":
            return list(self._rows)
        def order(row):
            has_update, _ = self._update_state(row)
            stamp = float(row.get("updateAt") or row.get("added") or 0) \
                if has_update else float(row.get("added") or 0)
            return (0 if has_update else 1, -stamp)
        rows = sorted(self._rows, key=order)
        if self._source_filter:
            rows = [row for row in rows
                    if _source_group(row.get("drama")) == self._source_filter]
        return rows

    def _update_state(self, row):
        """(有更新?, 更新到第几集)。预约未上线的行不挂角标。"""
        known = int(row.get("known") or 0)
        read = int(row.get("read") or 0)
        new = int(row.get("newEpisodes") or max(0, known - read))
        has_update = bool(row.get("arrived")) or new > 0
        badge = 0
        if has_update and not row.get("reserved"):
            badge = known if known > 0 else (read + new)
        return has_update, badge

    def refresh(self):
        """按需重建网格/分段，并在本页可见时才去拉封面。

        追剧/历史里的每一行都要一张海报；两个面板在窗口构造时就建好了，于是
        启动瞬间会替没人看的页签打几百次封面请求（历史里缺 source 的老记录还
        会全部失败重试）。所以拉封面这一步单独拆出来，只在页签真的显示时才做；
        切到本页时由主窗口再调一次 refresh()。
        """
        rows = self._store.follows() if self._mode == "follow" else self._store.history()
        self._rows = rows
        self._rows_by_id = {row["id"]: row for row in rows}
        if self._mode == "follow":
            self._refresh_chips(rows)
        visible = self._visible_rows()
        # 结构（选中集/顺序）变化才重建；5 秒一次的进度保存只改 position，
        # 走就地更新，不清掉用户的选中项、不滚回顶部。
        if self._mode == "follow":
            structure = (self._source_filter,
                         tuple((row["id"], self._update_state(row)[1]) for row in visible))
        else:
            structure = tuple((row["id"], _day_bucket(row.get("updated")))
                              for row in visible)
        self._sig = structure
        if self._mode == "follow":
            if any(row["id"] not in self._cards for row in visible) \
                    or tuple(self._cards) != tuple(row["id"] for row in visible):
                self._rebuild_grid(visible)
            else:
                self._update_cards(visible)
        else:
            if tuple(self._row_widgets) != tuple(row["id"] for row in visible) \
                    or not self._row_widgets:
                self._rebuild_sections(visible)
            else:
                self._update_rows(visible)
        if not rows:
            self.status.setText("还没有%s记录 · %s"
                                % ("追更/预约" if self._mode == "follow" else "观看", self._hint))
        elif visible and not any(row["id"] == self._selected_id for row in visible):
            self.status.setText("共 %d 部 · 当前筛选 %d 部 · %s"
                                % (len(rows), len(visible), self._hint))
        else:
            self.status.setText("共 %d 部 · %s" % (len(rows), self._hint))
        self._update_actions()
        self._request_covers()

    # -------------------------------------------------------------- follow
    def _refresh_chips(self, rows):
        present = []
        for row in rows:
            key = _source_group(row.get("drama"))
            if key and key not in present:
                present.append(key)
        present.sort(key=lambda key: (SOURCE_ORDER.index(key) if key in SOURCE_ORDER
                                      else len(SOURCE_ORDER), key))
        if tuple(present) == self._chip_groups:
            return
        if self._source_filter and self._source_filter not in present:
            self._source_filter = None
        while self._chip_flow.count():
            item = self._chip_flow.takeAt(0)
            widget = item.widget()
            if widget is not None:
                self._chip_group.removeButton(widget) if isinstance(widget, QPushButton) else None
                widget.setParent(None)
                widget.deleteLater()
        self._chips = {}
        buttons = [(None, "全部")] + [(key, _group_label(key)) for key in present]
        for key, label in buttons:
            chip = QPushButton(label)
            chip.setProperty("guoRole", "groupTab")
            chip.setCheckable(True)
            chip.setCursor(Qt.PointingHandCursor)
            chip.setChecked(self._source_filter == key)
            chip.clicked.connect(lambda _checked=False, value=key: self._set_source_filter(value))
            self._chip_group.addButton(chip)
            self._chip_flow.addWidget(chip)
            self._chips[key] = chip
        self._chip_groups = tuple(present)
        if self.chip_bar is not None:
            self.chip_bar.setVisible(len(present) > 1)

    def _set_source_filter(self, key):
        if key == self._source_filter:
            return
        self._source_filter = key
        self.refresh()

    def _card_for(self, row):
        card = PosterCard(self.grid_host, cover_size=CARD_COVER)
        identifier = row["id"]
        card.clicked.connect(lambda checked=False, value=identifier: self._on_card_clicked(value))
        return card

    def _apply_card(self, card, row):
        drama = row.get("drama") or {}
        _, badge = self._update_state(row)
        cached = self._covers.cached(row["id"])
        card.set_data((drama.get("title") or "短剧").strip(),
                      cover=cached,
                      update_badge=badge if badge > 0 else None)
        card.setToolTip(poster_tooltip(drama))

    def _rebuild_grid(self, rows):
        while self.grid_flow.count():
            item = self.grid_flow.takeAt(0)
            widget = item.widget()
            if widget is not None:
                widget.setParent(None)
                widget.deleteLater()
        self._cards = {}
        for row in rows:
            card = self._card_for(row)
            self._apply_card(card, row)
            self.grid_flow.addWidget(card)
            self._cards[row["id"]] = card
        if self._selected_id in self._cards:
            self._apply_selection()
        else:
            self._selected_id = None

    def _update_cards(self, rows):
        for row in rows:
            card = self._cards.get(row["id"])
            if card is not None:
                self._apply_card(card, row)

    def _on_card_clicked(self, identifier):
        # 没有原生 item 单击/双击分离的网格，用时间窗自己区分：
        # 快速两下同卡 = 双击续播，否则单击选中。
        now = time.monotonic()
        last_id, last_at = self._last_click
        if last_id == identifier and (now - last_at) * 1000.0 <= QApplication.doubleClickInterval():
            self._last_click = (None, 0.0)
            self._open_current(identifier)
            return
        self._last_click = (identifier, now)
        self._select(identifier)

    # -------------------------------------------------------------- history
    def _make_history_row(self, row):
        widget = _HistoryRow(row, self._meta_text(row))
        widget.clicked.connect(lambda value: self._select(value))
        widget.doubleClicked.connect(lambda value: self._open_current(value))
        cached = self._covers.cached(row["id"])
        if cached is not None:
            widget.set_pixmap(cached)
        return widget

    def _rebuild_sections(self, rows):
        scroll = self.sections.findChild(QScrollArea)
        offset = scroll.verticalScrollBar().value() if scroll is not None else 0
        self._row_widgets = {}
        buckets = [[] for _ in HISTORY_BUCKETS]
        for row in rows:
            buckets[_day_bucket(row.get("updated"))].append(row)
        sections = []
        for title, bucket in zip(HISTORY_BUCKETS, buckets):
            if not bucket:
                continue
            factories = []
            for row in bucket:
                def factory(row=row):
                    widget = self._make_history_row(row)
                    self._row_widgets[row["id"]] = widget
                    return widget
                factories.append(factory)
            sections.append((title, factories))
        self.sections.set_sections(sections)
        if self._selected_id in self._row_widgets:
            self._apply_selection()
        else:
            self._selected_id = None
        if scroll is not None:
            scroll.verticalScrollBar().setValue(offset)

    def _update_rows(self, rows):
        for row in rows:
            widget = self._row_widgets.get(row["id"])
            if widget is not None:
                widget.set_meta(self._meta_text(row))

    # ---------------------------------------------------------------- shared
    def _select(self, identifier):
        if identifier == self._selected_id:
            return
        self._selected_id = identifier
        self._apply_selection()
        self._update_actions()

    def _apply_selection(self):
        if self._mode == "follow":
            for key, card in self._cards.items():
                selected = key == self._selected_id
                if card.property("selected") != ("true" if selected else "false"):
                    card.setProperty("selected", "true" if selected else "false")
                    card.style().unpolish(card)
                    card.style().polish(card)
        else:
            for key, widget in self._row_widgets.items():
                widget.set_selected(key == self._selected_id)

    def _row_text(self, row):
        drama = row.get("drama") or {}
        title = (drama.get("title") or "短剧").strip()
        return title + "\n" + self._meta_text(row)

    def reload_theme(self):
        """重绘自绘小封面（占位底色随主题）；QSS 部件由全局样式表即时生效。"""
        for widget in self._row_widgets.values():
            widget.cover.update()
        for card in self._cards.values():
            card.update()

    def _request_covers(self):
        """为本页还没有海报的行排队加载（只有页面显示时才真的发请求）。"""
        if not self.isVisible():
            return
        for row in self._visible_rows():
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
        """Route freshly loaded covers into this panel's widgets."""
        if self._mode == "follow":
            card = self._cards.get(drama_id)
            row = self._rows_by_id.get(drama_id)
            if card is not None and row is not None:
                self._apply_card(card, row)
            return
        widget = self._row_widgets.get(drama_id)
        if widget is not None:
            widget.set_pixmap(pixmap)

    # -------------------------------------------------------------- actions
    def _selected_row(self):
        if self._selected_id is None:
            return None
        return self._rows_by_id.get(self._selected_id)

    def _open_current(self, identifier):
        row = self._selected_row()
        if row is None and identifier is not None:
            row = self._rows_by_id.get(identifier)
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
