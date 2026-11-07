"""新剧页：新剧推荐 + 上新日历（对齐红果短剧 v1.2.21 的页面与接口）。

参考客户端（Wails/Vue 版）的两块数据：
- 新剧推荐：GET /reading/bookapi/bookmall/cell/change/v1/
  cell_gender=2/1/0，selected_items=firstonlinetime_new，18 条一页，
  第二页起带 session_id；返回 cell_view.video_data（title/cover/score/
  episode_cnt/tag_info/sub_title_list）。
- 上新日历：GET /reading/user/subscribe/list/v1/
  active_panel=6&tab_style=2&tab_type=5&need_calendar_schema=true，
  返回 subscribe_items + calendar_schema.date_list。

页面结构照原版：标题「新剧」+ 两个下划线页签；新剧推荐页右侧是
全部/男频/女频胶囊，卡片墙带评分；上新日历页是一条日期条 + 竖向列表，
已上线行给「▶ 播放」，待上线行给「🔔 预约 / ✓ 已预约」。
"""
from __future__ import annotations

import time

from PyQt5.QtCore import QEvent, QRectF, QTimer, Qt, pyqtSignal
from PyQt5.QtGui import QColor, QFont, QPainter, QPainterPath, QPen
from PyQt5.QtWidgets import (QButtonGroup, QFrame, QHBoxLayout, QLabel,
                             QPushButton, QScrollArea, QSizePolicy,
                             QStackedWidget, QVBoxLayout, QWidget)

from .tasks import TaskRunner
from .ui import DESIGN
from .widgets import CoverLoader
from .poster_tooltip import poster_tooltip

WEEK_NAMES = ("周日", "周一", "周二", "周三", "周四", "周五", "周六")
CHIP_GENDERS = (("2", "全部"), ("1", "男频"), ("0", "女频"))


def _today():
    return time.strftime("%Y%m%d")


def _date_parts(value):
    text = str(value or "")
    if len(text) != 8 or not text.isdigit():
        return str(value or ""), "", False
    month, day = int(text[4:6]), int(text[6:8])
    try:
        # strptime: 周一=0 … 周日=6；WEEK_NAMES 从「周日」起排。
        week = WEEK_NAMES[(int(time.strptime(text, "%Y%m%d").tm_wday) + 1) % 7]
    except ValueError:
        week = ""
    return "%d/%d" % (month, day), week, text == _today()


def _calendar_drama(item):
    series = str(item.get("seriesId") or "")
    if not series:
        return None
    return {
        "id": "hongguo:" + series,
        "source": "hongguo",
        "sourceId": series,
        "title": item.get("title") or "",
        "cover": item.get("cover") or "",
        "description": item.get("description") or "",
        "episodes": int(item.get("episodeCnt") or 0),
        "category": item.get("category") or "",
        "tags": [str(tag) for tag in (item.get("tags") or []) if str(tag).strip()],
        "publishAt": int(item.get("publishAt") or 0),
        "publishText": item.get("publishText") or "",
        "hint": item.get("hint") or "",
        "note": item.get("note") or "",
        "heat": item.get("heat") or "",
        "expectedText": item.get("expectedText") or "",
        "online": bool(item.get("isOnline")),
        "badge": item.get("badge") or "",
        "calendar": True,
    }


def _new_series_drama(item):
    series = str(item.get("seriesId") or "")
    if not series:
        return None
    tags = [str(tag) for tag in (item.get("tags") or []) if str(tag).strip()][:3]
    subtitle = str(item.get("subTitle") or "")
    episode_text = next((part.strip() for part in subtitle.split("·") if "集" in part), "")
    return {
        "id": "hongguo:" + series,
        "source": "hongguo",
        "sourceId": series,
        "title": item.get("title") or "",
        "cover": item.get("cover") or "",
        "description": item.get("description") or "",
        # 卡片副行就是三个题材，和原版 .card-sub 一致
        "category": " · ".join(tags),
        "tags": tags,
        "episodes": int(item.get("episodeCnt") or 0),
        "episodeText": episode_text,
        "releaseStatus": "ongoing" if "更新" in episode_text else "",
        "heat": str(item.get("heat") or "").strip(),
        "score": str(item.get("score") or ""),
        "badge": item.get("badge") or "",
        "newSeries": True,
    }


class _CoverThumb(QWidget):
    """日历行左侧 78×104 的圆角封面。"""

    def __init__(self, parent=None):
        super().__init__(parent)
        self._pixmap = None
        self.setFixedSize(78, 104)

    def set_pixmap(self, pixmap):
        self._pixmap = pixmap
        self.update()

    def paintEvent(self, _event):
        painter = QPainter(self)
        painter.setRenderHint(QPainter.Antialiasing, True)
        painter.setRenderHint(QPainter.SmoothPixmapTransform, True)
        rect = QRectF(0, 0, self.width(), self.height())
        path = QPainterPath()
        path.addRoundedRect(rect, 9, 9)
        painter.save()
        painter.setClipPath(path)
        painter.fillRect(rect, QColor(DESIGN["cover_bg"]))
        if self._pixmap is not None and not self._pixmap.isNull():
            scaled = self._pixmap.scaled(self.size(), Qt.KeepAspectRatioByExpanding,
                                          Qt.SmoothTransformation)
            left = max(0, (scaled.width() - self.width()) // 2)
            top = max(0, (scaled.height() - self.height()) // 2)
            painter.drawPixmap(-left, -top, scaled)
        painter.restore()
        painter.setPen(QPen(QColor(DESIGN["border"]), 1))
        painter.setBrush(Qt.NoBrush)
        painter.drawPath(path)
        painter.end()


class _Badge(QLabel):
    """封面左上角的「新剧 / 红果首发」小标。"""

    def __init__(self, text, parent=None):
        super().__init__(text, parent)
        self.setProperty("role", "calendarBadge")
        self.setAttribute(Qt.WA_TransparentForMouseEvents, True)


class _StatusLabel(QLabel):
    """底部状态行；失败时点一下重试。"""

    clicked = pyqtSignal()

    def mouseReleaseEvent(self, event):
        if event.button() == Qt.LeftButton:
            self.clicked.emit()
        super().mouseReleaseEvent(event)


class _DateChip(QWidget):
    """日期条上的一格：上面星期，下面 M/D；今天选中时红底。"""

    clicked = pyqtSignal(str)

    def __init__(self, value, parent=None):
        super().__init__(parent)
        day, week, _ = _date_parts(value)
        self._value = value
        self._day = day
        self._week = "今天" if value == _today() else week
        self._past = value < _today()
        self._selected = False
        self._hover = False
        self.setFixedSize(64, 52)
        self.setCursor(Qt.PointingHandCursor)

    def value(self):
        return self._value

    def set_selected(self, selected):
        selected = bool(selected)
        if selected != self._selected:
            self._selected = selected
            self.update()

    def enterEvent(self, _event):
        self._hover = True
        self.update()

    def leaveEvent(self, _event):
        self._hover = False
        self.update()

    def mouseReleaseEvent(self, event):
        if event.button() == Qt.LeftButton:
            self.clicked.emit(self._value)
        super().mouseReleaseEvent(event)

    def paintEvent(self, _event):
        painter = QPainter(self)
        painter.setRenderHint(QPainter.Antialiasing, True)
        rect = QRectF(0.5, 0.5, self.width() - 1, self.height() - 1)
        if self._selected:
            background, border, color = DESIGN["accent"], DESIGN["accent"], DESIGN["on_accent"]
        else:
            background = DESIGN["button_hover"] if self._hover else DESIGN["button_bg"]
            border = DESIGN["button_border"]
            color = DESIGN["muted2"] if self._past else DESIGN["text"]
        painter.setPen(QPen(QColor(border), 1))
        painter.setBrush(QColor(background))
        painter.drawRoundedRect(rect, 10, 10)
        week_font = QFont(self.font())
        week_font.setPixelSize(12)
        day_font = QFont(self.font())
        day_font.setPixelSize(15)
        day_font.setBold(True)
        painter.setPen(QColor(color))
        painter.setFont(week_font)
        painter.drawText(QRectF(0, 8, self.width(), 16), Qt.AlignCenter, self._week)
        painter.setFont(day_font)
        painter.drawText(QRectF(0, 26, self.width(), 20), Qt.AlignCenter, self._day)
        painter.end()


class _ReserveButton(QPushButton):
    """原版 .reserve：默认品牌红「🔔 预约」，已预约是描边「✓ 已预约」，
    悬停换成红色「取消预约」。"""

    def __init__(self, reserved=False, parent=None):
        super().__init__(parent)
        self._reserved = bool(reserved)
        self._hover = False
        self.setFixedSize(88, 26)
        self.setCursor(Qt.PointingHandCursor)
        self.setProperty("role", "calendarReserve")
        self._paint_state()

    def set_reserved(self, reserved):
        reserved = bool(reserved)
        if reserved != self._reserved:
            self._reserved = reserved
            self._paint_state()

    def enterEvent(self, event):
        self._hover = True
        self._paint_state()
        super().enterEvent(event)

    def leaveEvent(self, event):
        self._hover = False
        self._paint_state()
        super().leaveEvent(event)

    def _paint_state(self):
        self.setToolTip("点击取消预约" if self._reserved else "上线提醒")
        if self._reserved:
            self.setText("取消预约" if self._hover else "✓ 已预约")
        else:
            self.setText("预约")
        value = "true" if self._reserved else "false"
        if self.property("reserved") != value:
            self.setProperty("reserved", value)
            self.style().unpolish(self)
            self.style().polish(self)
        self.update()


class _CalendarRow(QFrame):
    """上新日历的一行：封面 + 标题/状态/简介 + 播放或预约按钮。"""

    activated = pyqtSignal(object)
    playRequested = pyqtSignal(object)
    reserveToggled = pyqtSignal(object)

    def __init__(self, drama, reserved, parent=None):
        super().__init__(parent)
        self.drama = drama
        self.setProperty("role", "calendarRow")
        self.setCursor(Qt.PointingHandCursor)
        self.setMinimumHeight(124)

        layout = QHBoxLayout(self)
        layout.setContentsMargins(10, 10, 10, 10)
        layout.setSpacing(14)

        self.cover = _CoverThumb()
        self.cover.setToolTip(poster_tooltip(drama))
        layout.addWidget(self.cover)

        main = QVBoxLayout()
        main.setSpacing(6)
        title = QLabel(drama.get("title") or "未知剧集")
        title.setProperty("role", "calendarTitle")
        title.setSizePolicy(QSizePolicy.Expanding, QSizePolicy.Fixed)
        main.addWidget(title)

        meta = QHBoxLayout()
        meta.setSpacing(10)
        state = QLabel("已上线" if drama.get("online") else (drama.get("hint") or "即将上线"))
        state.setProperty("role", "calendarOnline" if drama.get("online") else "calendarPending")
        meta.addWidget(state)
        episode_text = ("共%d集" % drama["episodes"]) if drama.get("online") and drama.get("episodes") else ""
        for text in (drama.get("category"), drama.get("note"), episode_text):
            if not text:
                continue
            label = QLabel(str(text))
            label.setProperty("role", "calendarMeta")
            meta.addWidget(label)
        meta.addStretch(1)
        main.addLayout(meta)

        text = drama.get("description") or ""
        # 原版 .cal-intro 是 clamp-2；QLabel 没有行数限制，按两行宽度截断。
        if len(text) > 58:
            text = text[:58].rstrip() + "…"
        desc = QLabel(text)
        desc.setTextFormat(Qt.PlainText)
        desc.setToolTip(poster_tooltip(drama))
        desc.setWordWrap(True)
        desc.setMaximumHeight(34)
        desc.setProperty("role", "calendarMeta")
        main.addWidget(desc)
        main.addStretch(1)
        layout.addLayout(main, 1)

        self.play_button = None
        self.reserve_button = None
        if drama.get("online"):
            self.play_button = QPushButton("▶ 播放")
            self.play_button.setFixedSize(74, 26)
            self.play_button.setCursor(Qt.PointingHandCursor)
            self.play_button.setProperty("role", "calendarPlay")
            self.play_button.clicked.connect(lambda: self.playRequested.emit(self.drama))
            layout.addWidget(self.play_button, 0, Qt.AlignVCenter)
        else:
            self.reserve_button = _ReserveButton(reserved)
            self.reserve_button.clicked.connect(lambda: self.reserveToggled.emit(self.drama))
            layout.addWidget(self.reserve_button, 0, Qt.AlignVCenter)

        if drama.get("badge"):
            self._badge = _Badge(drama["badge"], self.cover)
            self._badge.move(4, 4)

    def set_pixmap(self, pixmap):
        self.cover.set_pixmap(pixmap)

    def set_reserved(self, reserved):
        if self.reserve_button is not None:
            self.reserve_button.set_reserved(reserved)

    def mouseReleaseEvent(self, event):
        if event.button() == Qt.LeftButton:
            self.activated.emit(self.drama)
        super().mouseReleaseEvent(event)


class CalendarPanel(QWidget):
    """新剧页：新剧推荐 + 上新日历。"""

    openDrama = pyqtSignal(object)
    playDrama = pyqtSignal(object)
    statusMessage = pyqtSignal(str)

    def __init__(self, core, watch, parent=None):
        super().__init__(parent)
        self.setProperty("role", "newSeriesPage")
        self._core = core
        self.watch = watch
        self._runner = TaskRunner(self)
        self._covers = CoverLoader(core, self)
        self._new_items = {}
        self._new_page = {"hasMore": False, "nextOffset": 0, "sessionId": ""}
        self._new_loading = False
        self._new_loaded = False
        self._new_error = ""
        self._new_gender = "2"
        self._new_generation = 0
        self._new_empty_pages = 0
        self._new_failed_request = None
        self._new_check_scheduled = False
        self._calendar_items = {}
        self._calendar_page = {"hasMore": False, "nextOffset": 0}
        self._calendar_loading = False
        self._calendar_loaded = False
        self._calendar_error = ""
        self._calendar_date = ""
        self._date_chips = []
        self._rows = []
        self._rows_by_id = {}
        self._build_ui()
        self._covers.loaded.connect(self._on_cover)
        try:
            watch.changed.connect(self._sync_reserved)
        except Exception:
            pass
        # 和原版一样，页面真正显示时才拉当前页签的数据；启动时不必为空页预取。
        QTimer.singleShot(0, self._first_load)

    # ------------------------------------------------------------------ ui
    def _build_ui(self):
        header = QHBoxLayout()
        header.setSpacing(0)
        title = QLabel("新剧")
        title.setProperty("role", "newPageTitle")
        header.addWidget(title)

        tabs = QHBoxLayout()
        tabs.setContentsMargins(16, 0, 0, 0)
        tabs.setSpacing(4)
        self.new_tab = self._tab_button("新剧推荐")
        self.calendar_tab = self._tab_button("上新日历")
        tabs.addWidget(self.new_tab)
        tabs.addWidget(self.calendar_tab)
        header.addLayout(tabs)
        header.addStretch(1)

        self.gender_box = QHBoxLayout()
        self.gender_box.setSpacing(6)
        self._gender_group = QButtonGroup(self)
        self._gender_group.setExclusive(True)
        for value, name in CHIP_GENDERS:
            chip = QPushButton(name)
            chip.setCheckable(True)
            chip.setCursor(Qt.PointingHandCursor)
            chip.setFixedHeight(28)
            chip.setProperty("role", "newGender")
            if value == self._new_gender:
                chip.setChecked(True)
            chip.clicked.connect(lambda _checked, v=value: self._set_gender(v))
            self._gender_group.addButton(chip)
            self.gender_box.addWidget(chip)
        header.addLayout(self.gender_box)

        self.new_tab.setChecked(True)
        self.new_tab.clicked.connect(lambda: self._switch_tab("new"))
        self.calendar_tab.clicked.connect(lambda: self._switch_tab("calendar"))

        self.stack = QStackedWidget()
        self.stack.setProperty("role", "newSeriesStack")
        self.stack.addWidget(self._build_new_page())
        self.stack.addWidget(self._build_calendar_page())

        layout = QVBoxLayout(self)
        layout.setContentsMargins(10, 10, 10, 10)
        layout.setSpacing(8)
        layout.addLayout(header)
        layout.addWidget(self.stack, 1)

    def _tab_button(self, text):
        button = QPushButton(text)
        button.setCheckable(True)
        button.setCursor(Qt.PointingHandCursor)
        button.setProperty("role", "newTab")
        return button

    def _build_new_page(self):
        from .poster_wall import PosterWall

        page = QWidget()
        page.setProperty("role", "newSeriesPage")
        self.new_wall = PosterWall(covers=self._covers)
        self.new_wall.setProperty("role", "newPosterWall")
        self.new_wall.viewport().setObjectName("newPosterViewport")
        self.new_wall.set_cover_loader(self._covers, self._request_covers)
        self.new_wall.set_score_mode(True)
        self.new_wall.dramaActivated.connect(self._open_drama)
        scrollbar = self.new_wall.verticalScrollBar()
        scrollbar.valueChanged.connect(self._new_scrolled)
        scrollbar.rangeChanged.connect(lambda _minimum, _maximum: self._schedule_new_check())
        self.new_wall.viewport().installEventFilter(self)

        self.new_status = _StatusLabel("")
        self.new_status.setAlignment(Qt.AlignCenter)
        self.new_status.setCursor(Qt.PointingHandCursor)
        self.new_status.setProperty("role", "newStatus")
        self.new_status.clicked.connect(self._retry_new)

        layout = QVBoxLayout(page)
        layout.setContentsMargins(0, 6, 0, 0)
        layout.setSpacing(6)
        layout.addWidget(self.new_wall, 1)
        layout.addWidget(self.new_status)
        return page

    def _build_calendar_page(self):
        page = QWidget()
        page.setProperty("role", "newSeriesPage")

        self.date_area = QScrollArea()
        self.date_area.setWidgetResizable(True)
        self.date_area.setFixedHeight(62)
        self.date_area.setVerticalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.date_area.setHorizontalScrollBarPolicy(Qt.ScrollBarAsNeeded)
        self.date_area.setProperty("role", "newSeriesScroll")
        self.date_area.viewport().setObjectName("newDateViewport")
        self.date_widget = QWidget()
        self.date_widget.setObjectName("newDateContents")
        self.date_layout = QHBoxLayout(self.date_widget)
        self.date_layout.setContentsMargins(0, 0, 0, 4)
        self.date_layout.setSpacing(8)
        self.date_layout.addStretch(1)
        self.date_area.setWidget(self.date_widget)

        self.rows_area = QScrollArea()
        self.rows_area.setWidgetResizable(True)
        self.rows_area.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.rows_area.setProperty("role", "newSeriesScroll")
        self.rows_area.viewport().setObjectName("newListViewport")
        self.rows_widget = QWidget()
        self.rows_widget.setObjectName("newListContents")
        self.rows_layout = QVBoxLayout(self.rows_widget)
        self.rows_layout.setContentsMargins(0, 4, 2, 0)
        self.rows_layout.setSpacing(12)
        self.rows_layout.addStretch(1)
        self.rows_area.setWidget(self.rows_widget)
        self.rows_area.verticalScrollBar().valueChanged.connect(self._calendar_scrolled)

        self.calendar_status = _StatusLabel("")
        self.calendar_status.setAlignment(Qt.AlignCenter)
        self.calendar_status.setCursor(Qt.PointingHandCursor)
        self.calendar_status.setProperty("role", "newStatus")
        self.calendar_status.clicked.connect(lambda: self.refresh_calendar(force=True))

        layout = QVBoxLayout(page)
        layout.setContentsMargins(0, 6, 0, 0)
        layout.setSpacing(8)
        layout.addWidget(self.date_area)
        layout.addWidget(self.rows_area, 1)
        layout.addWidget(self.calendar_status)
        return page

    # ------------------------------------------------------------- tabs
    def _switch_tab(self, which):
        if which == "new":
            self.new_tab.setChecked(True)
            self.calendar_tab.setChecked(False)
            self.stack.setCurrentIndex(0)
            for index in range(self.gender_box.count()):
                widget = self.gender_box.itemAt(index).widget()
                if widget is not None:
                    widget.show()
            if not self._new_loaded and not self._new_loading:
                self.refresh_new(force=True)
            elif self._new_loaded:
                self._schedule_new_check()
        else:
            self.calendar_tab.setChecked(True)
            self.new_tab.setChecked(False)
            self.stack.setCurrentIndex(1)
            for index in range(self.gender_box.count()):
                widget = self.gender_box.itemAt(index).widget()
                if widget is not None:
                    widget.hide()
            if not self._calendar_loaded:
                self.refresh_calendar(force=True)

    def _set_gender(self, value):
        if value == self._new_gender:
            return
        self._new_gender = value
        self._new_loaded = False
        if self.stack.currentIndex() == 0:
            self.refresh_new(force=True)

    # ---------------------------------------------------------- new series
    def refresh_new(self, force=False):
        if self._new_loaded and not force:
            return
        self._new_generation += 1
        generation = self._new_generation
        self._new_loading = True
        self._new_loaded = False
        self._new_error = ""
        self._new_empty_pages = 0
        self._new_failed_request = None
        self._new_page = {"hasMore": False, "nextOffset": 0, "sessionId": ""}
        self._new_items = {}
        self.new_wall.set_items([])
        self._update_new_status()
        self._fetch_new(0, "", replace=True, generation=generation)

    def _fetch_new(self, offset, cursor, replace, generation=None):
        gender = self._new_gender
        if generation is None:
            generation = self._new_generation

        def work():
            return self._core.new_series(gender=gender, offset=offset, cursor=cursor)

        def done(page):
            if generation != self._new_generation:
                return
            self._new_loaded = True
            self._new_error = ""
            self._new_failed_request = None
            page = page or {}
            items = [d for d in (_new_series_drama(row) for row in (page.get("items") or [])) if d]
            if replace:
                self._new_items = {d["id"]: d for d in items}
                self.new_wall.set_items(list(self._new_items.values()))
                added = len(self._new_items)
            else:
                fresh_by_id = {}
                for drama in items:
                    if drama["id"] not in self._new_items:
                        fresh_by_id[drama["id"]] = drama
                self._new_items.update(fresh_by_id)
                self.new_wall.append_items(list(fresh_by_id.values()))
                added = len(fresh_by_id)
            next_offset = int(page.get("nextOffset") or 0)
            has_more = bool(page.get("hasMore")) and next_offset > offset
            self._new_empty_pages = 0 if added else self._new_empty_pages + 1
            self._new_page = {
                "hasMore": has_more,
                "nextOffset": next_offset,
                "sessionId": page.get("sessionId") or cursor,
            }
            if has_more and self._new_empty_pages >= 3:
                self._new_error = "连续多页没有新剧"
                self._new_failed_request = (next_offset, self._new_page["sessionId"], False)
            self._new_loading = False
            self._update_new_status()
            self._schedule_new_check()

        def failed(message):
            if generation != self._new_generation:
                return
            self._new_loading = False
            self._new_error = str(message)
            self._new_failed_request = (offset, cursor, replace)
            self._update_new_status()

        self._runner.run(work, on_success=done, on_error=failed)

    def _retry_new(self):
        if self._new_loading or not self._new_error:
            return
        if not self._new_failed_request:
            self.refresh_new(force=True)
            return
        offset, cursor, replace = self._new_failed_request
        self._new_error = ""
        self._new_empty_pages = 0
        self._new_loading = True
        self._update_new_status()
        self._fetch_new(offset, cursor, replace)

    def _update_new_status(self):
        if self._new_error:
            self.new_status.setText("加载失败：%s（点此重试）" % self._new_error)
        elif self._new_loading and not self._new_items:
            self.new_status.setText("加载新剧…")
        elif not self._new_items:
            self.new_status.setText("暂无新剧")
        elif not self._new_loading and not self._new_page.get("hasMore"):
            self.new_status.setText("没有更多了")
        else:
            self.new_status.clear()
        self.new_status.setVisible(bool(self.new_status.text()))

    def _schedule_new_check(self):
        if self._new_check_scheduled:
            return
        self._new_check_scheduled = True
        QTimer.singleShot(0, self._check_new_scroll)

    def _check_new_scroll(self):
        self._new_check_scheduled = False
        if not self.isVisible() or self.stack.currentIndex() != 0:
            return
        self.new_wall.doItemsLayout()
        self._new_scrolled(self.new_wall.verticalScrollBar().value())

    def _new_scrolled(self, value):
        if (not self.isVisible() or self.stack.currentIndex() != 0 or self._new_loading
                or self._new_error or not self._new_page.get("hasMore")):
            return
        bar = self.new_wall.verticalScrollBar()
        if bar.maximum() - value > 400:
            return
        self._new_loading = True
        self._update_new_status()
        self._fetch_new(int(self._new_page.get("nextOffset") or 0),
                        self._new_page.get("sessionId") or "", replace=False)

    def eventFilter(self, watched, event):
        wall = getattr(self, "new_wall", None)
        if wall is not None and watched is wall.viewport() and event.type() in (QEvent.Resize, QEvent.Show):
            self._schedule_new_check()
        return super().eventFilter(watched, event)

    # ------------------------------------------------------------- calendar
    def refresh_calendar(self, force=False):
        if self._calendar_loading:
            return
        if self._calendar_loaded and not force:
            return
        self._calendar_loading = True
        self._calendar_error = ""
        self.calendar_status.setText("加载上新日历…")
        self._fetch_calendar(self._calendar_date, 0, replace=True)

    def _fetch_calendar(self, date, offset, replace):
        def work():
            return self._core.calendar(date=date, offset=offset)

        def done(page):
            self._calendar_loading = False
            self._calendar_loaded = True
            page = page or {}
            if replace:
                dates = [str(value) for value in (page.get("dates") or []) if value]
                if date == "":
                    self._calendar_date = page.get("defaultDate") or (dates[-1] if dates else _today())
                self._render_dates(dates, page.get("defaultDate") or "")
            items = [d for d in (_calendar_drama(row) for row in (page.get("items") or [])) if d]
            items.sort(key=lambda drama: 0 if drama.get("online") else 1)
            if replace:
                self._calendar_items = {d["id"]: d for d in items}
                self._render_rows()
            else:
                self._calendar_items.update({d["id"]: d for d in items})
                self._append_rows(items)
            self._calendar_page = {
                "hasMore": bool(page.get("hasMore")),
                "nextOffset": int(page.get("nextOffset") or 0),
            }
            self._update_calendar_status()

        def failed(message):
            self._calendar_loading = False
            self._calendar_error = str(message)
            self.calendar_status.setText("加载失败：%s（点此重试）" % message)

        self._runner.run(work, on_success=done, on_error=failed)

    def _render_dates(self, dates, default):
        values = list(dict.fromkeys([d for d in dates if d]))
        if not values and default:
            values = [default]
        for chip in self._date_chips:
            self.date_layout.removeWidget(chip)
            chip.hide()
            chip.setParent(None)
        self._date_chips = []
        for value in values:
            chip = _DateChip(value)
            chip.clicked.connect(self._select_date)
            chip.set_selected(value == self._calendar_date)
            self.date_layout.insertWidget(self.date_layout.count() - 1, chip)
            self._date_chips.append(chip)

    def _select_date(self, value):
        if self._calendar_loading or value == self._calendar_date:
            return
        self._calendar_date = value
        for chip in self._date_chips:
            chip.set_selected(chip.value() == value)
        self._calendar_loading = True
        self.calendar_status.setText("加载中…")
        self._fetch_calendar(value, 0, replace=True)

    def _render_rows(self):
        for row in self._rows:
            self.rows_layout.removeWidget(row)
            row.hide()
            row.setParent(None)
        self._rows = []
        self._rows_by_id = {}
        self._append_rows(list(self._calendar_items.values()))

    def _append_rows(self, items):
        index = max(0, self.rows_layout.count() - 1)
        for drama in items:
            if drama["id"] in self._rows_by_id:
                continue
            row = _CalendarRow(drama, self.watch.is_reserved(drama["id"]))
            row.activated.connect(self._on_row_activated)
            row.playRequested.connect(self._open_drama_play)
            row.reserveToggled.connect(self._toggle_reserved)
            self.rows_layout.insertWidget(index, row)
            index += 1
            self._rows.append(row)
            self._rows_by_id[drama["id"]] = row
            pixmap = self._covers.cached(drama["id"])
            if pixmap is not None:
                row.set_pixmap(pixmap)
            else:
                self._covers.request(drama)

    def _calendar_scrolled(self, value):
        if self._calendar_loading or not self._calendar_page.get("hasMore"):
            return
        bar = self.rows_area.verticalScrollBar()
        if bar.maximum() - value > 160:
            return
        self._calendar_loading = True
        self.calendar_status.setText("加载中…")
        self._fetch_calendar(self._calendar_date,
                             int(self._calendar_page.get("nextOffset") or 0), replace=False)

    def _update_calendar_status(self):
        if self._calendar_loading:
            self.calendar_status.setText("加载中…")
        elif self._calendar_error:
            self.calendar_status.setText("加载失败：%s（点此重试）" % self._calendar_error)
        elif not self._calendar_items:
            self.calendar_status.setText("这一天没有上新")
        elif self._calendar_page.get("hasMore"):
            self.calendar_status.setText("")
        else:
            self.calendar_status.setText("没有更多了")

    # ------------------------------------------------------------- actions
    def _open_drama(self, drama):
        if drama:
            self.openDrama.emit(drama)

    def _on_row_activated(self, drama):
        if drama.get("online"):
            self.playDrama.emit(drama)
        else:
            self.openDrama.emit(drama)

    def _open_drama_play(self, drama):
        if drama:
            self.playDrama.emit(drama)

    def _toggle_reserved(self, drama):
        drama_id = drama.get("id")
        if not drama_id:
            return
        if self.watch.is_reserved(drama_id):
            self.watch.cancel_reservation(drama_id)
            self.statusMessage.emit("已取消预约")
        else:
            self.watch.reserve_drama(drama)
            self.statusMessage.emit("预约成功，上线后会出现在「我的预约」里")
        self._sync_reserved()

    def _sync_reserved(self, *_args):
        for drama_id, row in self._rows_by_id.items():
            row.set_reserved(self.watch.is_reserved(drama_id))

    # ------------------------------------------------------------- covers
    def _request_covers(self, dramas):
        for drama in dramas:
            self._covers.request(drama)

    def _on_cover(self, drama_id, pixmap):
        self.new_wall.set_pixmap(drama_id, pixmap)
        row = self._rows_by_id.get(drama_id)
        if row is not None:
            row.set_pixmap(pixmap)

    def reload_theme(self):
        self.new_wall.reload_theme()
        for chip in self._date_chips:
            chip.update()
        for row in self._rows:
            row.cover.update()
            row.update()
        self.date_area.viewport().update()
        self.rows_area.viewport().update()
        self.update()

    # ------------------------------------------------------------- lifecycle
    def _first_load(self):
        if self.isVisible():
            self.refresh()

    def refresh(self):
        """页签可见时再决定要不要真的发请求（和原版按需加载一致）。"""
        if self.stack.currentIndex() == 0:
            if not self._new_loaded and not self._new_loading:
                self.refresh_new(force=True)
        elif not self._calendar_loaded:
            self.refresh_calendar(force=True)
