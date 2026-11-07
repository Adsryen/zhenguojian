"""Download queue panel: grouped by drama, with prominent batch merge."""
from __future__ import annotations

import os
import re
import shutil
import threading
import time

from PyQt5.QtCore import QEvent, QRect, QSize, Qt, QTimer, pyqtSignal
from PyQt5.QtGui import (QColor, QFont, QFontMetrics, QPainter, QPen,
                         QTextLayout, QTextOption)
from PyQt5.QtWidgets import (QAbstractItemView, QCheckBox, QDialog, QFileDialog, QHBoxLayout,
                             QLabel, QLineEdit, QListWidget, QListWidgetItem,
                             QMessageBox, QProgressBar, QPushButton, QSplitter,
                             QStyle, QStyleOptionViewItem, QStyledItemDelegate, QTabWidget, QVBoxLayout, QWidget, QComboBox)

from . import decrypt, library, media, tools
from .tasks import TaskRunner
from .ui import DESIGN
from .widgets import ElidedLabel
from .flow_layout import FlowLayout

MERGE_STATE_TEXT = {"queued": "排队中", "merging": "合并中",
                    "done": "已完成", "failed": "失败"}
MERGE_STATE_COLOUR = {"queued": "muted", "merging": "accent",
                      "done": "success", "failed": "danger"}

STATE_TEXT = {
    "queued": "排队中", "downloading": "下载中", "paused": "已暂停",
    "completed": "已完成", "failed": "失败", "removing": "删除中",
}

def state_colour(state):
    """状态胶囊的颜色。写成函数而不是模块级字典：颜色得跟着当前主题走。"""
    return DESIGN.get("state_%s" % state) or DESIGN["muted"]

# ------------------------------------------------------------ 下载队列卡片
# 一集一张卡片，宽度按列固定、中间留给进度条；这些常量 paint 和点击命中
# (_row_buttons) 共用，改一处两边一起变。
ROW_HEIGHT = 46
CARD_RADIUS = 8
COL_EPISODE = 84
COL_STATE = 76
COL_QUALITY = 122
COL_SPEED = 86
COL_SIZE = 140
COL_ETA = 84
PERCENT_WIDTH = 48
BTN_WIDTH = 56
BTN_HEIGHT = 26
BTN_GAP = 6

ROW_ACTIONS = {
    "downloading": ("pause", "remove"),
    "queued": ("pause", "remove"),
    "paused": ("resume", "remove"),
    "completed": ("play", "remove"),
}
ACTION_TEXT = {"pause": "暂停", "resume": "继续", "play": "▶ 播放", "remove": "删除"}

# 列表项数据（只在本模块用，和 widgets.py 的角色不冲突）
ROLE_JOB_STATE = Qt.UserRole + 1
ROLE_JOB_PROGRESS = Qt.UserRole + 2
ROLE_JOB_SPEED = Qt.UserRole + 3
ROLE_JOB_BYTES = Qt.UserRole + 4
ROLE_JOB_ID = Qt.UserRole + 5
ROLE_JOB_QUALITY = Qt.UserRole + 6
ROLE_JOB_ETA = Qt.UserRole + 7


def _duration_text(seconds):
    """把秒数写成紧凑的中文时长；0/未知/超过 2 天返回空串。"""
    try:
        seconds = int(float(seconds) + 0.5)
    except (TypeError, ValueError):
        return ""
    if seconds <= 0 or seconds > 86400 * 2:
        return ""
    if seconds < 60:
        return "%d 秒" % seconds
    minutes = (seconds + 59) // 60
    if minutes < 60:
        return "%d 分" % minutes
    hours, minutes = divmod(minutes, 60)
    return "%d时%d分" % (hours, minutes)


def _eta_text(seconds):
    text = _duration_text(seconds)
    return ("剩 " + text) if text else ""


def _tint(colour, alpha=48):
    """同一个颜色加透明底：状态胶囊和按钮的悬停底色都用它。"""
    value = QColor(colour)
    value.setAlpha(alpha)
    return value


def _speed_text(speed):
    return "%.1f MB/s" % (float(speed or 0) / 1048576.0)


def job_quality_text(job):
    """下载卡片上的「1080P · 1.46 Mbps」。

    档位（actualQuality 是核心取到流之后回填的真实高度）一开始就有，码率要等下载
    结束才写进任务，所以没码率时只写档位。
    """
    quality = int(job.get("actualQuality") or job.get("quality") or 0)
    if quality <= 0:
        return ""
    rate = media.bitrate_text(job.get("bitrate"))
    return "%dP · %s" % (quality, rate) if rate else "%dP" % quality


def _row_buttons(card, state):
    """这一行的按钮 [(key, rect)]，从右往左排。

    画和点击判定共用同一份几何：写两遍迟早会错位。
    """
    keys = ROW_ACTIONS.get(state, ("remove",))
    items = []
    right = card.right() - 10
    for key in reversed(keys):
        items.append((key, QRect(right - BTN_WIDTH, card.center().y() - BTN_HEIGHT // 2,
                                 BTN_WIDTH, BTN_HEIGHT)))
        right -= BTN_WIDTH + BTN_GAP
    return items

# Merging is the whole point of this panel, so the season-merge button is the
# only loud, colour-filled control in the window and it lives above the table.


_CN_DIGITS = {"零": 0, "一": 1, "二": 2, "两": 2, "三": 3, "四": 4, "五": 5,
              "六": 6, "七": 7, "八": 8, "九": 9}
_SEASON_RE = re.compile(r"第([0-9一二两三四五六七八九十]+)季")


def _cn_to_int(text):
    text = text.strip()
    if text.isdigit():
        return int(text)
    if text == "十":
        return 10
    if "十" in text:
        left, _, right = text.partition("十")
        tens = _CN_DIGITS.get(left, 1) if left else 1
        ones = _CN_DIGITS.get(right, 0) if right else 0
        return tens * 10 + ones
    digits = [_CN_DIGITS.get(ch) for ch in text]
    if digits and all(d is not None for d in digits):
        return int("".join(str(d) for d in digits))
    return None


def season_of(title):
    """Season number of a drama title; 1 when it carries no marker."""
    match = _SEASON_RE.search(title or "")
    if match:
        number = _cn_to_int(match.group(1))
        if number:
            return number
    match = re.search(r"[Ss](\d+)", title or "")
    if match:
        return int(match.group(1))
    return 1


def strip_season(title):
    title = _SEASON_RE.sub("", title or "").strip(" 　-·~～")
    title = re.sub(r"[Ss]\d+$", "", title).strip()
    return title or "合集"


def _safe_name(text):
    return re.sub(r'[\\/:*?"<>|]', "_", (text or "").strip()) or "合集"


def _confirm(parent, title, text, yes="确定", no="取消"):
    """中文按钮的确认框。

    标准按钮（Yes/No）的文案在 Qt 的 qtbase 中文目录里，而 PyQt5 只带了
    qtbase_zh_TW，所以直接给按钮起中文名，别让「是/否」和英文混着出现。
    """
    box = QMessageBox(parent)
    box.setWindowTitle(title)
    box.setText(text)
    box.setIcon(QMessageBox.Question)
    yes_button = box.addButton(yes, QMessageBox.YesRole)
    box.addButton(no, QMessageBox.NoRole)
    box.exec_()
    return box.clickedButton() is yes_button


def human_size(value):
    value = float(value or 0)
    for unit in ("B", "KB", "MB", "GB"):
        if value < 1024 or unit == "GB":
            return "%.1f %s" % (value, unit)
        value /= 1024
    return "%.1f GB" % value


class _LocationDialog(QDialog):
    """Where the finished library goes: 成品库位置.

    Default is the app folder (``<exe>\\下载``); any other drive or folder can
    be picked.  The dialog spells out what does *not* move, because that is
    the part users get surprised by: the core's encrypted cache stays in the
    program folder, and files downloaded so far stay where they are.
    """

    def __init__(self, parent=None):
        super().__init__(parent)
        self.setWindowTitle("下载位置")
        self.setMinimumWidth(540)
        self._value = tools.library_root()

        self.path_edit = QLineEdit(self._value)
        self.path_edit.setReadOnly(True)
        browse = QPushButton("选择文件夹…")
        browse.clicked.connect(self._browse)

        row = QHBoxLayout()
        row.addWidget(QLabel("位置"))
        row.addWidget(self.path_edit, 1)
        row.addWidget(browse)

        self.preview = QLabel("")
        self.preview.setWordWrap(True)
        note = QLabel("解密后的分集与合并成品保存在该位置的「下载」子目录里。\n"
                      "内部加密缓存在程序目录，用于断点续传与合并，不随这里变动；\n"
                      "已下载的文件不会被搬走：旧位置的分集照旧可播，合并成品也会"
                      "继续列在「合并成品」里，并标出所在盘符。")
        note.setWordWrap(True)
        for label in (self.preview, note):
            label.setStyleSheet("color:%s;" % DESIGN["muted"])

        reset = QPushButton("恢复默认")
        reset.clicked.connect(self._reset)
        cancel = QPushButton("取消")
        cancel.clicked.connect(self.reject)
        confirm = QPushButton("确定")
        confirm.setProperty("role", "primary")
        confirm.setDefault(True)
        confirm.clicked.connect(self.accept)

        buttons = QHBoxLayout()
        buttons.addWidget(reset)
        buttons.addStretch(1)
        buttons.addWidget(cancel)
        buttons.addWidget(confirm)

        layout = QVBoxLayout(self)
        layout.addLayout(row)
        layout.addWidget(self.preview)
        layout.addWidget(note)
        layout.addLayout(buttons)
        self._refresh()

    def _refresh(self):
        self.path_edit.setText(self._value)
        self.preview.setText("成品库：%s" % os.path.join(self._value, "下载"))

    def _browse(self):
        chosen = QFileDialog.getExistingDirectory(self, "选择下载位置", self._value)
        if chosen:
            self._value = os.path.abspath(chosen)
            self._refresh()

    def _reset(self):
        self._value = tools.project_dir()
        self._refresh()

    def value(self):
        return self._value


class _JobQueue(QListWidget):
    """下载队列本体：悬停行/悬停按钮自己记着，QSS 的 ::item 悬停在这条绘制路径上用不上。"""

    def __init__(self, parent=None):
        super().__init__(parent)
        self.setProperty("role", "queue")
        self.setUniformItemSizes(True)
        self.setSelectionMode(QAbstractItemView.ExtendedSelection)
        self.setVerticalScrollMode(QAbstractItemView.ScrollPerPixel)
        self.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.setEditTriggers(QAbstractItemView.NoEditTriggers)
        self.setFocusPolicy(Qt.NoFocus)
        self.setMouseTracking(True)
        self.viewport().setObjectName("jobQueueViewport")
        self.viewport().setAutoFillBackground(False)
        self.setViewportMargins(2, 4, 2, 4)
        self._hover_row = -1
        self._hover_button = ""

    def _hit(self, position):
        index = self.indexAt(position)
        if not index.isValid():
            return -1, ""
        state = index.data(ROLE_JOB_STATE) or ""
        for key, rect in _row_buttons(self.visualRect(index), state):
            if rect.contains(position):
                return index.row(), key
        return index.row(), ""

    def mouseMoveEvent(self, event):
        row, button = self._hit(event.pos())
        if (row, button) != (self._hover_row, self._hover_button):
            self._hover_row, self._hover_button = row, button
            self.viewport().update()
        super().mouseMoveEvent(event)

    def leaveEvent(self, event):
        if self._hover_row != -1 or self._hover_button:
            self._hover_row, self._hover_button = -1, ""
            self.viewport().update()
        super().leaveEvent(event)


class _JobDelegate(QStyledItemDelegate):
    """一集一张卡片：集数 · 状态胶囊 · 进度条 · 速度 · 大小 · 两个按钮。

    以前是七列 QTableWidget：每一行都重复剧名、进度只有一个百分数、总大小被列宽
    截断，看着像数据库导出。队列动辄几百集又每 1.5 秒轮询一次，所以整行交给
    delegate 画（不挂控件），只有真正变了的那一行重画。
    """

    def __init__(self, owner, parent=None):
        super().__init__(parent)
        self._owner = owner

    def sizeHint(self, option, index):
        return QSize(360, ROW_HEIGHT)

    def paint(self, painter, option, index):
        card = QRect(option.rect.x() + 2, option.rect.y() + 2,
                     option.rect.width() - 6, option.rect.height() - 4)
        state = index.data(ROLE_JOB_STATE) or ""
        selected = bool(option.state & QStyle.State_Selected)
        hovered = getattr(option.widget, "_hover_row", -1) == index.row()
        hover_button = getattr(option.widget, "_hover_button", "") if hovered else ""

        painter.save()
        painter.setRenderHint(QPainter.Antialiasing, True)
        if selected:
            fill, border = QColor(DESIGN["accent_soft"]), QColor(DESIGN["accent"])
        elif hovered:
            fill, border = QColor(DESIGN["chip_hover"]), QColor(DESIGN["border"])
        else:
            fill, border = QColor(DESIGN["chip"]), QColor(DESIGN["border"])
        painter.setPen(QPen(border, 1))
        painter.setBrush(fill)
        painter.drawRoundedRect(card.adjusted(0, 0, -1, -1), CARD_RADIUS, CARD_RADIUS)

        colour = QColor(state_colour(state))
        middle = card.center().y()
        left = card.left() + 14

        # 集数
        font = QFont(option.font)
        font.setPixelSize(13)
        font.setBold(True)
        painter.setFont(font)
        painter.setPen(QColor(DESIGN["text"]))
        painter.drawText(QRect(left, card.top(), COL_EPISODE, card.height()),
                         Qt.AlignLeft | Qt.AlignVCenter, str(index.data(Qt.DisplayRole) or ""))
        left += COL_EPISODE

        # 状态胶囊
        pill = QRect(left, middle - 13, COL_STATE, 26)
        painter.setPen(Qt.NoPen)
        painter.setBrush(_tint(colour))
        painter.drawRoundedRect(pill, 13, 13)
        painter.setPen(colour)
        font.setBold(False)
        painter.setFont(font)
        painter.drawText(pill, Qt.AlignCenter, STATE_TEXT.get(state, state))
        left += COL_STATE + 14

        # 画质：档位一开始就有，码率下载完才有。列宽固定，没数据也占位，
        # 免得进度条在码率回填的瞬间左右跳一下。
        quality = index.data(ROLE_JOB_QUALITY) or ""
        if quality:
            painter.setPen(QColor(DESIGN["muted"]))
            painter.drawText(QRect(left, card.top(), COL_QUALITY, card.height()),
                             Qt.AlignLeft | Qt.AlignVCenter, quality)
        left += COL_QUALITY

        # 右边这几块宽度固定，从右往左排，中间剩下的都给进度条
        buttons = _row_buttons(card, state)
        right_edge = min((rect.left() for _key, rect in buttons), default=card.right()) - 12
        size_rect = QRect(right_edge - COL_SIZE, card.top(), COL_SIZE, card.height())
        eta_rect = QRect(size_rect.left() - COL_ETA, card.top(), COL_ETA, card.height())
        speed_rect = QRect(eta_rect.left() - COL_SPEED, card.top(), COL_SPEED, card.height())
        bar_right = speed_rect.left() - 14

        percent = index.data(ROLE_JOB_PROGRESS)
        percent = 0.0 if percent is None else max(0.0, min(1.0, float(percent)))
        bar_width = max(0, bar_right - left - PERCENT_WIDTH)
        if bar_width >= 40:
            track = QRect(left, middle - 4, bar_width, 8)
            painter.setPen(Qt.NoPen)
            painter.setBrush(QColor(DESIGN["border"]))
            painter.drawRoundedRect(track, 4, 4)
            filled = int(bar_width * percent)
            if filled > 0:
                painter.setBrush(colour)
                painter.drawRoundedRect(QRect(track.left(), track.top(), max(4, filled), 8), 4, 4)
            painter.setPen(QColor(DESIGN["muted"]))
            painter.drawText(QRect(track.right() + 10, card.top(), PERCENT_WIDTH - 10, card.height()),
                             Qt.AlignLeft | Qt.AlignVCenter, "%.0f%%" % (percent * 100))

        painter.setPen(QColor(DESIGN["muted"]))
        speed = float(index.data(ROLE_JOB_SPEED) or 0.0)
        painter.drawText(speed_rect, Qt.AlignRight | Qt.AlignVCenter,
                         _speed_text(speed) if state == "downloading" and speed > 0 else "-")
        eta = int(index.data(ROLE_JOB_ETA) or 0)
        if state == "downloading" and eta > 0:
            painter.drawText(eta_rect, Qt.AlignRight | Qt.AlignVCenter, _eta_text(eta))
        done, total = index.data(ROLE_JOB_BYTES) or (0, 0)
        painter.drawText(size_rect, Qt.AlignRight | Qt.AlignVCenter,
                         "%s / %s" % (human_size(done), human_size(total)))

        for key, rect in buttons:
            danger = key == "remove"
            base = QColor(DESIGN["danger"] if danger else DESIGN["accent"])
            if hover_button == key:
                painter.setPen(QPen(base, 1))
                painter.setBrush(_tint(base, 40))
            else:
                painter.setPen(QPen(QColor(DESIGN["border"]), 1))
                painter.setBrush(Qt.NoBrush)
            painter.drawRoundedRect(rect.adjusted(0, 0, -1, -1), 6, 6)
            painter.setPen(base if (danger or hover_button == key) else QColor(DESIGN["text"]))
            painter.drawText(rect, Qt.AlignCenter, ACTION_TEXT[key])

        painter.restore()

    def editorEvent(self, event, model, option, index):
        if event.type() != QEvent.MouseButtonRelease or event.button() != Qt.LeftButton:
            return False
        for key, rect in _row_buttons(option.rect, index.data(ROLE_JOB_STATE) or ""):
            if rect.contains(event.pos()):
                self._owner.job_action(index.row(), key)
                return True
        return False


class _MergedRow(QWidget):
    """One merged-output row: clicks play the file, the delete button is real.

    The row turns every left click (label included - it is mouse-transparent)
    into `clicked`; the delete button keeps its own events.
    """

    clicked = pyqtSignal()

    def mousePressEvent(self, event):
        if event.button() == Qt.LeftButton:
            self.clicked.emit()
        super().mousePressEvent(event)

    def mouseDoubleClickEvent(self, event):
        self.clicked.emit()
        super().mouseDoubleClickEvent(event)


class _DramaDelegate(QStyledItemDelegate):
    def sizeHint(self, option, index):
        return QSize(220, 62)
    def paint(self, painter, option, index):
        prepared = QStyleOptionViewItem(option)
        self.initStyleOption(prepared, index)
        title, _, meta = str(index.data(Qt.DisplayRole) or "").partition("\n")
        has_check = bool(prepared.features & QStyleOptionViewItem.HasCheckIndicator)
        prepared.text = ""
        prepared.widget.style().drawControl(QStyle.CE_ItemViewItem, prepared, painter, prepared.widget)
        painter.save()
        selected = bool(prepared.state & QStyle.State_Selected)
        font = QFont(prepared.font)
        font.setPixelSize(12)
        font.setBold(True)
        painter.setFont(font)
        painter.setPen(QColor(DESIGN["on_accent"] if selected else DESIGN["text"]))
        rect = prepared.rect.adjusted(30 if has_check else 10, 8, -10, -6)
        metrics = QFontMetrics(font)
        painter.drawText(QRect(rect.x(), rect.y(), rect.width(), 20), Qt.AlignVCenter,
                         metrics.elidedText(title, Qt.ElideRight, rect.width()))
        if meta:
            font.setBold(False); font.setPixelSize(11); painter.setFont(font)
            painter.setPen(QColor(DESIGN["on_accent"] if selected else DESIGN["muted"]))
            painter.drawText(QRect(rect.x(), rect.y()+23, rect.width(), 18), Qt.AlignVCenter,
                             QFontMetrics(font).elidedText(meta, Qt.ElideRight, rect.width()))
        painter.restore()


class _FitList(QListWidget):
    """QListWidget that reports viewport resizes so rows can re-wrap.

    Both lists sit in a splitter the user can drag wider; item size hints are
    width-dependent, so they have to be recomputed when the column changes.
    """

    resized = pyqtSignal()

    def resizeEvent(self, event):
        super().resizeEvent(event)
        self.resized.emit()


def _wrapped_height(text, width, font):
    """Pixel height for ``text`` wrapped at ``width`` (newlines respected)."""
    metrics = QFontMetrics(font)
    total = 0
    for part in (text or "").split("\n"):
        rect = metrics.boundingRect(QRect(0, 0, max(60, width), 10000),
                                    Qt.TextWordWrap, part)
        total += max(metrics.height(), rect.height())
    return total


class _WrappedTitle(QLabel):
    """Title that wraps to however many lines the column actually needs.

    以前固定两行、超出补省略号；合并成品名字里带「全001-1000集」，集数一多
    名字必然超过两行，就被截成「…」。现在按实际宽度算行数，行高跟着长，
    名字完整显示（极端长名仍有 tooltip 兜底）。
    """

    REFERENCE_WIDTH = 170

    def __init__(self, text="", max_lines=None, wrap_width=202, parent=None):
        super().__init__(parent)
        self._full = text or ""
        self._max_lines = max_lines
        # 布局前 QLabel.width() 还是默认值，行数必须按列宽算，否则 sizeHint
        # 只报一行、后面的行会被裁掉。_fit_merge_rows 会跟着列宽同步。
        self._wrap_width = max(40, int(wrap_width))
        self.setToolTip(self._full)
        self.setWordWrap(False)
        self.setMinimumWidth(60)

    def set_wrap_width(self, width):
        width = max(40, int(width))
        if width == self._wrap_width:
            return
        self._wrap_width = width
        self.updateGeometry()
        self.update()

    def setText(self, text):
        self._full = text or ""
        self.setToolTip(self._full)
        self.update()

    def text(self):
        return self._full

    def _lines(self, width):
        lines = []
        for paragraph in self._full.split("\n"):
            layout = QTextLayout(paragraph, self.font())
            option = QTextOption()
            # 按词边界换行：中文照样能断，但 "全001-1000集.mkv" 不会只剩一个
            # "v" 掉到下一行。
            option.setWrapMode(QTextOption.WrapAtWordBoundaryOrAnywhere)
            layout.setTextOption(option)
            layout.beginLayout()
            while True:
                line = layout.createLine()
                if not line.isValid():
                    break
                line.setLineWidth(max(20, width))
                start = line.textStart()
                lines.append(paragraph[start:start + line.textLength()])
            layout.endLayout()
        return lines or [""]

    def _display_lines(self, width):
        lines = self._lines(width)
        if self._max_lines is None or len(lines) <= self._max_lines:
            return lines
        metrics = QFontMetrics(self.font())
        lines = lines[:self._max_lines]
        last = lines[-1]
        while last and metrics.horizontalAdvance(last + "…") > width:
            last = last[:-1]
        lines[-1] = last + "…"
        return lines

    def sizeHint(self):
        metrics = QFontMetrics(self.font())
        return QSize(140, metrics.height() * max(1, len(self._lines(self._wrap_width))))

    def paintEvent(self, event):
        painter = QPainter(self)
        metrics = QFontMetrics(self.font())
        width = max(40, self.width())
        # Never paint more lines than the allocated height can hold.
        line_height = max(1, metrics.height())
        max_lines = max(1, self.height() // line_height)
        if self._max_lines is not None:
            max_lines = min(self._max_lines, max_lines)
        painter.setPen(self.palette().color(self.foregroundRole()))
        y = 0
        for line in self._display_lines(width)[:max_lines]:
            painter.drawText(QRect(0, y, width, line_height),
                             Qt.AlignLeft | Qt.AlignVCenter, line)
            y += line_height


class DownloadPanel(QWidget):
    statusMessage = pyqtSignal(str)
    openLocalRequested = pyqtSignal(str, str)
    playEpisodeRequested = pyqtSignal(object, int)

    # Worker threads emit these; Qt queues them onto the GUI thread so no
    # callback ever touches a widget from a background thread.
    _jobsReady = pyqtSignal(object)
    _renderRequested = pyqtSignal()
    _statusText = pyqtSignal(str)
    _mergeBarUpdate = pyqtSignal(float, str)
    # 后台删除的进度从工作线程发回：走队列连接，不能直接碰状态栏。
    _deleteStatus = pyqtSignal(str)

    def __init__(self, core, parent=None):
        super().__init__(parent)
        self._core = core
        self._jobs = []
        # 正在后台删除的任务 id：点删除后先从界面移除，核心删完自然消失；
        # 删不掉（文件被占用）的会重新回到列表。
        self._hidden_ids = set()
        # 核心的下载任务只回传 bytes/total，没有 speed：按两次轮询的字节增量
        # 自己算，10 秒窗口平滑，行内速度和 ETA 都靠它。
        self._speed_samples = {}
        self._speeds = {}
        self._merge_task = None
        # 正在合并的剧 id：排队时用来跳过已经在合并的那一部。
        self._merge_current_id = None
        self._refresh_pending = False
        self._decrypt_task = None
        self._decrypt_cancel = None
        # Consecutive publish failures per job id.  Without a cap a single
        # undecryptable episode was retried every 1.5s forever, spawning a new
        # ffmpeg each time.
        self._decrypt_failures = {}
        self.decrypted = 0
        # Per-row caches so a refresh only rewrites what actually changed.
        self._row_sig = {}
        # Drama grouping: None means "show every drama" in the table.
        self._filter_drama = None
        self._drama_sig = None
        self._populating_dramas = False
        # Season/batch merge bookkeeping.
        self._merge_queue = []
        self._merge_total = 0
        self._merge_processed = 0
        # 合并 ETA：按“当前阶段”的完成量/阶段进度推算，而不是用分阶段加权的总进度。
        self._merge_stage = ""
        self._merge_stage_start = 0.0
        self._merge_stage_ratio = 0.0
        self._merge_last_progress = 0.0
        self._merge_count = 0
        # 核心任务没有 speed 字段，速度按字节增量自己算（8 秒滑动窗口）。
        self._speed_hist = {}
        self._job_speed = {}
        self._merge_outputs = []
        self._merge_failures = []
        # 合并队列页签的数据源：本批次每一部的状态/进度/成品。
        self._merge_entries = []
        self._fitting_merge_queue = False
        self._merge_files_sig = None
        self._last_merged_play = (None, 0.0)
        self._auto_resumed = False

        self._runner = TaskRunner(self)
        self._build_ui()

        self.publisher = library.EpisodePublisher(
            core,
            on_change=self._request_render,
            on_status=self._post_status)

        self._jobsReady.connect(self._jobs_ready, Qt.QueuedConnection)
        self._renderRequested.connect(self._render, Qt.QueuedConnection)
        self._statusText.connect(self.merge_status.setText, Qt.QueuedConnection)
        self._mergeBarUpdate.connect(self._apply_merge_progress, Qt.QueuedConnection)
        self._deleteStatus.connect(self.statusMessage.emit, Qt.QueuedConnection)

        self._timer = QTimer(self)
        self._timer.setInterval(1500)
        self._timer.timeout.connect(self._poll)
        self._timer.start()
        self._update_merge_actions()

    def _poll(self):
        """定时刷新：只在这个页签真的显示时才问 core。

        否则每分钟 40 次 IPC 全打在没人看的页面上（虽然单次很便宜，但没道理）。
        切到本页时由主窗口显式调 refresh()。
        """
        if self.isVisible():
            self.refresh()
            self._refresh_merge_eta()

    # ------------------------------------------------------------------- ui
    def _build_ui(self):
        # ---- 顶部：成品库位置 + 整条队列的开关
        self.open_folder = QPushButton("打开下载目录")
        self.open_folder.clicked.connect(self._open_folder)
        self.location_button = QPushButton("下载位置…")
        self.location_button.setToolTip("成品库保存到哪个目录（默认在程序目录）")
        self.location_button.clicked.connect(self._choose_location)

        self.pause_all = QPushButton("全部暂停")
        self.pause_all.clicked.connect(lambda: self._control_all("pauseAll"))
        self.resume_all = QPushButton("全部继续")
        self.resume_all.clicked.connect(lambda: self._control_all("resumeAll"))

        self.danmaku_download = QCheckBox("下载时保存弹幕")
        self.danmaku_download.setToolTip(
            "勾选后，每集解密完成会额外保存同名 .ass 字幕和 .danmaku.json；默认关闭")
        self.danmaku_download.setChecked(tools.danmaku_download_enabled())
        self.danmaku_download.toggled.connect(self._on_danmaku_download_toggled)

        toolbar = FlowLayout(spacing=8)
        toolbar.addWidget(self.open_folder)
        toolbar.addWidget(self.location_button)
        toolbar.addWidget(self.danmaku_download)
        toolbar.addWidget(self.resume_all)
        toolbar.addWidget(self.pause_all)

        # ---- 左栏：剧集（按剧聚合，可勾选多季）+ 合并成品
        # Both lists live in a draggable splitter: long drama names are common
        # and a fixed 236px column cut them off with no way to read the rest.
        self.drama_list = _FitList()
        self.drama_list.setProperty("role", "dramas")
        self.drama_list.setWordWrap(True)
        self.drama_list.setItemDelegate(_DramaDelegate(self.drama_list))
        self.drama_list.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.drama_list.setMinimumWidth(200)
        self.drama_list.currentRowChanged.connect(self._on_drama_changed)
        self.drama_list.itemChanged.connect(self._on_drama_item_changed)
        self.drama_list.resized.connect(self._fit_drama_rows)
        self._checked_ids = set()

        drama_title = QLabel("剧集")
        drama_title.setProperty("role", "section")
        drama_title.setToolTip("选择一部剧即可只看它的分集，并一键合并整季")

        self.merge_files = _FitList()
        self.merge_files.setProperty("role", "merges")
        self.merge_files.setMinimumWidth(200)
        self.merge_files.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.merge_files.itemClicked.connect(self._play_merged)
        self.merge_files.itemDoubleClicked.connect(self._play_merged)
        self.merge_files.resized.connect(self._fit_merge_rows)
        # 左栏下半：合并队列（本批次每部的状态/进度）和合并成品共用一块，
        # 排队和结果不用在两个地方找。
        self.merge_queue = _FitList()
        self.merge_queue.setProperty("role", "mergequeue")
        self.merge_queue.setWordWrap(True)
        self.merge_queue.setMinimumWidth(200)
        self.merge_queue.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.merge_queue.resized.connect(self._fit_merge_queue_rows)
        self.merge_queue.itemDoubleClicked.connect(self._open_merge_entry)

        queue_page = QWidget()
        queue_layout = QVBoxLayout(queue_page)
        queue_layout.setContentsMargins(4, 6, 4, 4)
        queue_layout.addWidget(self.merge_queue)

        files_page = QWidget()
        files_layout = QVBoxLayout(files_page)
        files_layout.setContentsMargins(4, 6, 4, 4)
        files_layout.addWidget(self.merge_files)

        self.merge_tabs = QTabWidget()
        self.merge_tabs.setProperty("role", "compact")
        self.merge_tabs.addTab(queue_page, "合并队列")
        self.merge_tabs.addTab(files_page, "合并成品")
        self.merge_tabs.setCurrentIndex(0)

        left_column = QVBoxLayout()
        left_column.setContentsMargins(0, 0, 0, 0)
        left_column.setSpacing(6)
        self.drama_search = QLineEdit()
        self.drama_search.setPlaceholderText("搜索已下载的剧名")
        self.drama_search.setClearButtonEnabled(True)
        self.drama_search.textChanged.connect(self._apply_drama_filter)
        self.drama_state = QComboBox()
        for label,value in (("全部状态", ""), ("下载未完成", "active"), ("已全部完成", "done"), ("有失败分集", "failed")):
            self.drama_state.addItem(label, value)
        self.drama_state.currentIndexChanged.connect(self._apply_drama_filter)
        self.drama_count = QLabel("")
        self.drama_count.setProperty("role", "hint")
        left_column.addWidget(drama_title)
        left_column.addWidget(self.drama_search)
        left_column.addWidget(self.drama_state)
        left_column.addWidget(self.drama_list, 1)
        left_column.addWidget(self.drama_count)
        left_panel = QWidget()
        left_panel.setLayout(left_column)
        left_panel.setMinimumWidth(200)

        # ---- 当前剧集卡片：标题、总进度、以及作用在这部剧上的合并动作
        self.current_title = ElidedLabel("全部剧集")
        self.current_title.setProperty("role", "section")
        self.current_title.setMinimumWidth(100)
        self.current_meta = ElidedLabel("")
        self.current_meta.setProperty("role", "hint")
        self.current_bar = QProgressBar()
        self.current_bar.setRange(0, 1000)
        self.current_bar.setTextVisible(False)
        self.current_bar.setFixedHeight(8)
        self.current_bar.setProperty("role", "slim")

        self.delete_drama_button = QPushButton("删除该剧")
        self.delete_drama_button.setProperty("role", "danger")
        self.delete_drama_button.setToolTip("移除左侧选中剧集的全部下载任务和已保存分集")
        self.delete_drama_button.clicked.connect(self._delete_selected_drama)

        head = QHBoxLayout()
        head.setSpacing(8)
        head.addWidget(self.current_title, 1)
        head.addWidget(self.delete_drama_button)

        # Primary merge action stays loud and lives where the drama it acts on is
        # decided — 合并是这个面板存在的理由。
        self.merge_season_button = QPushButton("一键合并整季")
        self.merge_season_button.setProperty("role", "primary")
        self.merge_season_button.setMinimumHeight(34)
        self.merge_season_button.setToolTip("把这个剧集列表里选中那部剧的全部已完成分集合成一个完整视频")
        self.merge_season_button.clicked.connect(self.start_merge_season)

        self.merge_button = QPushButton("合并选中分集")
        self.merge_button.setProperty("role", "secondary")
        self.merge_button.setMinimumHeight(34)
        self.merge_button.setToolTip("只合并下面列表里手动选中的那几集")
        self.merge_button.clicked.connect(self.start_merge)

        self.merge_multi_button = QPushButton("按季合并选中")
        self.merge_multi_button.setMinimumHeight(34)
        self.merge_multi_button.setToolTip(
            "勾选左侧多部剧（如同一部的多季），按季数顺序逐季合并，"
            "每季输出一个「剧名 S01E01」样式的成品")
        self.merge_multi_button.clicked.connect(self.start_merge_multi)

        actions = FlowLayout(spacing=8)
        actions.addWidget(self.merge_season_button)
        actions.addWidget(self.merge_button)
        actions.addWidget(self.merge_multi_button)
        self.merge_verify_box = QCheckBox("完整解码校验")
        self.merge_verify_box.setToolTip(
            "合并后把成品完整解码一遍，最稳但耗时；几百集的长剧可以关掉，"
            "仍会校验时长、编码和流参数")
        self.merge_verify_box.setChecked(tools.merge_verify_enabled())
        self.merge_verify_box.toggled.connect(self._toggle_merge_verify)
        actions.addWidget(self.merge_verify_box)

        card = QVBoxLayout()
        card.setContentsMargins(12, 10, 12, 12)
        card.setSpacing(8)
        card.addLayout(head)
        card.addWidget(self.current_meta)
        card.addWidget(self.current_bar)
        card.addLayout(actions)
        self.current_card = QWidget()
        self.current_card.setAttribute(Qt.WA_StyledBackground, True)
        self.current_card.setObjectName("currentDramaCard")
        self.current_card.setLayout(card)

        # ---- 队列：一集一张卡片
        self.queue = _JobQueue()
        self.queue.setItemDelegate(_JobDelegate(self, self.queue))
        self.queue.itemDoubleClicked.connect(self._play_row)

        self.merge_status = QLabel("")
        self.merge_status.setProperty("role", "hint")
        self.merge_status.setWordWrap(True)
        self.merge_bar = QProgressBar()
        self.merge_bar.setRange(0, 100)
        self.merge_bar.setMinimumWidth(100)
        self.merge_bar.setMaximumWidth(230)
        self.merge_bar.setVisible(False)
        self.merge_eta = QLabel("")
        self.merge_eta.setProperty("role", "hint")
        self.merge_eta.setMinimumWidth(130)

        status = QHBoxLayout()
        status.setSpacing(8)
        status.addWidget(self.merge_status, 1)
        status.addWidget(self.merge_eta)
        status.addWidget(self.merge_bar)

        right = QVBoxLayout()
        right.setContentsMargins(0, 0, 0, 0)
        right.setSpacing(8)
        right.addWidget(self.current_card)
        self.merge_tabs.insertTab(0, self.queue, "下载队列")
        self.merge_tabs.setCurrentIndex(0)
        right.addWidget(self.merge_tabs, 1)
        right.addLayout(status)
        right_panel = QWidget()
        right_panel.setLayout(right)

        main = QVBoxLayout(self)
        main.setContentsMargins(10, 10, 10, 10)
        main.setSpacing(8)
        main.addLayout(toolbar)
        splitter = QSplitter(Qt.Horizontal)
        splitter.setChildrenCollapsible(False)
        splitter.setHandleWidth(6)
        splitter.addWidget(left_panel)
        splitter.addWidget(right_panel)
        splitter.setStretchFactor(0, 0)
        splitter.setStretchFactor(1, 1)
        splitter.setSizes([300, 880])
        main.addWidget(splitter, 1)

    # --------------------------------------------------------------- helpers
    def _on_danmaku_download_toggled(self, checked):
        tools.set_danmaku_download_enabled(checked)
        self._post_status("下载弹幕：%s" % ("开" if checked else "关"))

    def _post_status(self, text):
        """Thread-safe status update; safe to call from any thread."""
        self._statusText.emit(str(text))

    def _request_render(self):
        """Thread-safe request to redraw the table."""
        self._renderRequested.emit()

    # ------------------------------------------------------------------ data
    def refresh(self):
        """Pull the queue off the GUI thread.

        ``CoreClient.downloads()`` blocks on IPC, so calling it straight from
        the timer used to freeze the window whenever the core was busy - most
        visibly during a merge.  One in-flight fetch at a time keeps the list
        fresh without piling up work.
        """
        if self._refresh_pending:
            return
        self._refresh_pending = True
        self._runner.run(self._core.downloads,
                         on_success=self._jobsReady.emit,
                         on_error=self._refresh_failed,
                         on_done=self._refresh_finished)

    def _refresh_finished(self):
        self._refresh_pending = False

    def _refresh_failed(self, message):
        self._post_status(message)

    def _jobs_ready(self, jobs):
        jobs = jobs or []
        present = {str(job.get("id") or "") for job in jobs}
        # 删除在后台跑：行先藏起来，核心删完自然不再出现；文件被占用、删不掉的
        # 会以 failed 回到列表，让用户看到到底哪几集没删掉。
        self._hidden_ids &= present
        self._hidden_ids -= {str(job.get("id") or "") for job in jobs
                             if job.get("state") == "failed"}
        self._jobs = [job for job in jobs
                      if str(job.get("id") or "") not in self._hidden_ids
                      and job.get("state") != "removing"]
        self._render()
        self._refresh_merge_files()
        self._auto_resume_interrupted()
        self._start_decrypt()

    def _hide_jobs(self, jobs):
        """点删除后立刻从列表移除；真正的文件删除留在后台完成。"""
        ids = {str(job.get("id") or "") for job in jobs}
        if not ids:
            return
        self._hidden_ids |= ids
        self._jobs = [job for job in self._jobs
                      if str(job.get("id") or "") not in ids]
        self._render()

    def _auto_resume_interrupted(self):
        """Jobs mid-download when the app closed come back paused by the core;
        resume them once per session so a restart simply continues the queue."""
        if self._auto_resumed:
            return
        self._auto_resumed = True
        targets = [job for job in self._jobs
                   if job.get("state") == "paused"
                   and "上次下载已暂停" in (job.get("error") or "")]
        if not targets:
            return

        def work():
            done = 0
            for job in targets:
                try:
                    self._core.control(job["id"], "resume")
                    done += 1
                except Exception:
                    pass
            return done

        def resumed(done):
            if done:
                self.statusMessage.emit("已自动恢复 %d 个中断的下载任务" % done)

        self._runner.run(work, on_success=resumed)
        self.refresh()

    # ------------------------------------------------------- merge queue view
    def _merge_entry(self, group, jobs):
        """合并队列里的一行：这一步要合什么、合到哪了。"""
        indexes = sorted(int(job.get("index") or 0) for job in jobs)
        if len(indexes) >= 2:
            span = "第 %03d–%03d 集" % (indexes[0], indexes[-1])
        elif indexes:
            span = "第 %03d 集" % indexes[0]
        else:
            span = ""
        return {"id": group.get("id") or "",
                "title": (group.get("title") or "").strip(),
                "span": span, "count": len(jobs), "state": "queued",
                "progress": 0.0, "detail": "", "output": ""}

    def _merge_entry_text(self, entry):
        label = MERGE_STATE_TEXT.get(entry["state"], entry["state"])
        meta = entry["span"]
        if entry["count"]:
            meta = (meta + " · ") if meta else ""
            meta = "%s%d 集" % (meta, entry["count"])
        if entry["state"] == "merging":
            meta += " · %d%%" % int(round(entry["progress"] * 100))
        elif entry["state"] == "done" and entry.get("output"):
            meta += " · " + os.path.basename(entry["output"])
        elif entry["state"] == "failed" and entry.get("detail"):
            meta += " · " + entry["detail"]
        return ("%s · %s\n%s" % (label, entry["title"] or "未知剧集", meta)).rstrip()

    def _render_merge_queue(self):
        if not hasattr(self, "merge_queue"):
            return
        texts = [self._merge_entry_text(entry) for entry in self._merge_entries]
        current = [self.merge_queue.item(i).text()
                   for i in range(self.merge_queue.count())]
        if current != texts:
            scroll = self.merge_queue.verticalScrollBar().value()
            self.merge_queue.blockSignals(True)
            self.merge_queue.clear()
            for text in texts:
                item = QListWidgetItem(text)
                item.setToolTip(text.replace("\n", " · "))
                self.merge_queue.addItem(item)
            self.merge_queue.blockSignals(False)
            self._fit_merge_queue_rows()
            self.merge_queue.doItemsLayout()
            self.merge_queue.verticalScrollBar().setValue(scroll)
        for index, entry in enumerate(self._merge_entries):
            item = self.merge_queue.item(index)
            if item is None:
                continue
            item.setData(Qt.UserRole, entry)
            item.setForeground(QColor(DESIGN.get(
                MERGE_STATE_COLOUR.get(entry["state"], ""), DESIGN["text"])))
        self._update_merge_tabs()

    def _fit_merge_queue_rows(self):
        if getattr(self, "_fitting_merge_queue", False) or not hasattr(self, "merge_queue"):
            return
        self._fitting_merge_queue = True
        try:
            width = max(120, self.merge_queue.viewport().width() - 42)
            for index in range(self.merge_queue.count()):
                item = self.merge_queue.item(index)
                height = _wrapped_height(item.text(), width,
                                         self.merge_queue.font()) + 18
                hint = item.sizeHint()
                if hint.width() != width or hint.height() != height:
                    item.setSizeHint(QSize(width, height))
        finally:
            self._fitting_merge_queue = False

    def _update_merge_tabs(self):
        tabs = getattr(self, "merge_tabs", None)
        if tabs is None:
            return
        pending = sum(1 for entry in self._merge_entries
                      if entry["state"] in ("queued", "merging"))
        tabs.setTabText(1, "合并队列 (%d)" % pending if pending else "合并队列")
        total = self.merge_files.count()
        if total == 1:
            only = self.merge_files.item(0)
            if only is not None and not only.data(Qt.UserRole):
                total = 0
        tabs.setTabText(2, "合并成品 (%d)" % total if total else "合并成品")

    def _finish_merge_entry(self, state, detail="", output=""):
        for entry in self._merge_entries:
            if entry["state"] == "merging":
                entry["state"] = state
                entry["detail"] = detail
                entry["output"] = output or ""
                if state == "done":
                    entry["progress"] = 1.0
                break
        self._render_merge_queue()

    def _open_merge_entry(self, item):
        entry = item.data(Qt.UserRole) if item is not None else None
        if isinstance(entry, dict) and entry.get("state") == "done" and entry.get("output"):
            self._play_merged_path(entry["output"])

    # ----------------------------------------------------- merged file list
    def _refresh_merge_files(self):
        """Mirror 下载/合并/ into the merged-outputs list (signature-guarded).

        Rows show a shortened name so the 删除 button always stays inside the
        visible 230px column; the full name travels in the item's tooltip and
        UserRole (the play path).
        """
        current = tools.merge_dir()
        entries = []
        for folder in tools.merge_dirs():
            if not os.path.isdir(folder):
                continue
            for name in os.listdir(folder):
                path = os.path.join(folder, name)
                if os.path.isfile(path) and name.lower().endswith((".mkv", ".mp4")):
                    try:
                        # 旧位置的成品也列出来——换下载位置时文件按设计没搬走，
                        # 只扫当前根的话它们在界面上就凭空消失了
                        entries.append((os.path.getmtime(path), name, os.path.getsize(path),
                                        path, folder != current))
                    except OSError:
                        continue
        entries.sort(reverse=True)
        signature = tuple((name, size, elsewhere)
                          for _mtime, name, size, _path, elsewhere in entries)
        if signature == self._merge_files_sig:
            return
        self._merge_files_sig = signature
        # clear() 会把滚动条打回顶部：删最下面一条时视图会直接跳到第一集。
        # 记下位置，重建完再放回去（内容变短时 Qt 会自动夹到新的最大值）。
        scroll = self.merge_files.verticalScrollBar().value()
        self.merge_files.clear()
        for _mtime, name, size, path, elsewhere in entries:
            # No item text: the row widget paints it.  Text on the item would
            # show through the transparent widget and double up.
            item = QListWidgetItem()
            item.setData(Qt.UserRole, path)
            # 旧位置的标一下：异盘就写盘符（D:），同盘写「旧位置」——同盘时
            # 盘符两边一样，标了等于没标
            marker = ""
            if elsewhere:
                drive = os.path.splitdrive(path)[0]
                marker = drive if drive and drive != os.path.splitdrive(current)[0] else "旧位置"
            item.setToolTip("%s\n%s%s · 点一下播放" % (
                name, (marker + " · ") if marker else "", human_size(size)))
            if elsewhere:
                item.setToolTip(item.toolTip() + "\n在旧位置：%s" % os.path.dirname(path))
            row = _MergedRow()
            row.clicked.connect(lambda checked=False, target=path: self._play_merged_path(target))
            row_layout = QVBoxLayout(row)
            row_layout.setContentsMargins(8, 4, 6, 4)
            row_layout.setSpacing(2)
            # 名字独占一行、最多两行（超出省略号）：尺寸和删除按钮放到第二行，
            # 否则 236px 的窄列里只够显示七八个字。
            label = _WrappedTitle(name)
            row.title = label
            label.setAttribute(Qt.WA_TransparentForMouseEvents, True)
            amount = QLabel("%s · %s" % (marker, human_size(size)) if marker else human_size(size))
            amount.setProperty("role", "hint")
            amount.setAttribute(Qt.WA_TransparentForMouseEvents, True)
            button = self._mini("删除", lambda checked=False, target=path:
                                self._delete_merged_file(target))
            bottom = QHBoxLayout()
            bottom.setContentsMargins(0, 0, 0, 0)
            bottom.setSpacing(6)
            bottom.addWidget(amount, 0)
            bottom.addStretch(1)
            bottom.addWidget(button)
            row_layout.addWidget(label)
            row_layout.addLayout(bottom)
            row.adjustSize()
            height = max(70, row.sizeHint().height())
            self.merge_files.addItem(item)
            self.merge_files.setItemWidget(item, row)
            item.setSizeHint(QSize(216, height))
        if not entries:
            self.merge_files.addItem(QListWidgetItem("还没有合并成品"))
        self._fit_merge_rows()
        self.merge_files.doItemsLayout()
        self.merge_files.verticalScrollBar().setValue(scroll)
        self._update_merge_tabs()

    def _play_merged(self, item):
        self._play_merged_path(item.data(Qt.UserRole))

    def _play_merged_path(self, path):
        now = time.time()
        if path and path == self._last_merged_play[0] and now - self._last_merged_play[1] < 0.8:
            return  # a double click reports itself twice; play once
        self._last_merged_play = (path, now)
        if path and os.path.isfile(path):
            self.openLocalRequested.emit(path, "")
        else:
            self.statusMessage.emit("成品文件不存在：%s" % os.path.basename(path or "?"))

    def _delete_merged_file(self, path):
        if not _confirm(
                self, "删除合并成品",
                "确定删除「%s」吗？\n删除后需要重新合并才能找回，此操作不可恢复。"
                % os.path.basename(path), yes="删除"):
            return
        try:
            os.remove(path)
        except OSError as exc:
            self.statusMessage.emit("删除失败：%s" % exc)
            return
        self._merge_files_sig = None
        self._refresh_merge_files()
        self.statusMessage.emit("已删除合并成品：%s" % os.path.basename(path))

    # ---------------------------------------------------------- delete drama
    def _delete_selected_drama(self):
        item = self.drama_list.currentItem()
        drama_id = item.data(Qt.UserRole) if item is not None else None
        if not drama_id:
            self.statusMessage.emit("请先在左侧选中一部要删除的剧集")
            return
        jobs = [job for job in self._jobs
                if (job.get("drama") or {}).get("id") == drama_id]
        if not jobs:
            self.statusMessage.emit("该剧没有可删除的下载任务")
            return
        title = ((jobs[0].get("drama") or {}).get("title") or drama_id).strip()
        folder = os.path.join(tools.download_dir(), decrypt.series_folder(title))
        folder_note = ""
        if os.path.isdir(folder):
            folder_note = "\n\n并将删除已保存的分集文件夹：\n%s" % folder
        if not _confirm(
                self, "删除剧集",
                "确定删除「%s」吗？\n\n将移除全部 %d 个下载任务（含加密缓存）%s。\n此操作不可恢复。"
                % (title, len(jobs), folder_note), yes="删除"):
            return
        # 立刻从列表移除：核心是一集一集删的，几千集时逐行变“删除中”既慢又
        # 占位置；后台删完自然消失，删不掉的会在 done() 里放回来。
        self._hide_jobs(jobs)
        self.statusMessage.emit("已从列表移除「%s」，正在后台删除 %d 集…"
                                % (title, len(jobs)))

        def work():
            errors = {}
            pending = list(jobs)
            total = len(pending)
            done_ids = set()
            # 每集单独删会让核心每删一集就重写整份索引（2 万条时 234 ms/集），
            # 所以分批交给 removeDownloads，一批只落盘两次。批次压到 200：
            # 上千集时核心每次只锁很短时间，界面轮询/合并不会被堵住。
            # 核心仍可能拒绝正在播放/被占用的分集，重试幸存者直到删完或错误
            # 明确持续；任何单集失败都不能中断整轮，否则又会留下一半。
            deadline = time.time() + max(90.0, 0.01 * len(jobs))
            idle_rounds = 0
            while pending and time.time() < deadline and idle_rounds < 3:
                still = []
                progressed = False
                for offset in range(0, len(pending), 200):
                    chunk = pending[offset:offset + 200]
                    try:
                        result = self._core.remove_downloads(
                            [job["id"] for job in chunk]) or {}
                    except Exception as exc:
                        for job in chunk:
                            errors[job.get("id")] = str(exc)
                        still.extend(chunk)
                        continue
                    failures = result.get("failures") or {}
                    for job in chunk:
                        job_id = job.get("id")
                        if job_id in failures:
                            errors[job_id] = str(failures[job_id])
                            still.append(job)
                            continue
                        if job_id not in done_ids:
                            done_ids.add(job_id)
                            progressed = True
                        try:
                            self.publisher.forget(job)
                        except Exception:
                            pass
                    self._deleteStatus.emit("正在后台删除：已处理 %d/%d 集…"
                                            % (len(done_ids), total))
                pending = still
                if pending:
                    idle_rounds = 0 if progressed else idle_rounds + 1
                    time.sleep(0.5)
            if not pending and os.path.isdir(folder):
                shutil.rmtree(folder, ignore_errors=True)
            return len(done_ids), pending, errors

        def done(result):
            removed, pending, errors = result
            # 删不掉的放回列表：文件被占用时核心会保留任务并写 error。
            self._hidden_ids -= {str(job.get("id") or "") for job in pending}
            self.refresh()
            if pending:
                stuck = "、".join(str(job.get("index")) for job in pending[:8])
                detail = (errors.get(pending[0].get("id")) or "").strip()
                self.statusMessage.emit(
                    "已删除 %d 集；第 %s 集暂时无法删除：%s"
                    % (removed, stuck, detail[:90]))
            else:
                self.statusMessage.emit("已删除「%s」：%d 个下载任务已彻底移除" % (title, removed))

        def failed(message):
            # 整批中断：不确定删到哪一步，把行都放回来，以下一次刷新为准。
            self._hidden_ids -= {str(job.get("id") or "") for job in jobs}
            self.refresh()
            self.statusMessage.emit("删除失败：%s" % message)

        self._runner.run(work, on_success=done, on_error=failed)

    # -------------------------------------------------------------- grouping
    def _drama_groups(self):
        """Aggregate the flat job list into one record per drama (season)."""
        groups = {}
        order = []
        for job in self._jobs:
            drama = job.get("drama") or {}
            key = drama.get("id") or ""
            if key not in groups:
                groups[key] = {"id": key,
                               "title": (drama.get("title") or key or "未知剧集").strip(),
                               "total": 0, "done": 0, "bytes": 0}
                order.append(key)
            group = groups[key]
            group["total"] += 1
            if job.get("state") == "completed":
                group["done"] += 1
                group["bytes"] += int(job.get("bytes") or job.get("total") or 0)
        return [groups[key] for key in order]

    def _visible_jobs(self):
        if self._filter_drama is None:
            return list(self._jobs)
        return [job for job in self._jobs
                if (job.get("drama") or {}).get("id") == self._filter_drama]

    def _render_dramas(self):
        groups = self._drama_groups()
        done = sum(g["done"] for g in groups)
        texts = ["全部剧集（%d 部 / 已完成 %d 集）" % (len(groups), done)]
        texts += ["%s\n已完成 %d/%d 集 · %s" % (
            group["title"], group["done"], group["total"],
            human_size(group["bytes"])) for group in groups]
        # Structure only: done/bytes change on every poll while downloading.
        # Rebuilding the list for those wiped the scroll position and then
        # snapped the view back to the selected (downloading) drama, so the
        # user could not scroll up to look at the first entries.
        structure = tuple((group["id"], group["title"], group["total"])
                          for group in groups)
        # Keep the delete/multi buttons in sync even on early returns.
        self.delete_drama_button.setEnabled(self._filter_drama is not None)
        self._update_multi_button()

        if structure == self._drama_sig and self.drama_list.count() == len(texts):
            changed = False
            for index, text in enumerate(texts):
                item = self.drama_list.item(index)
                if item.text() != text:
                    item.setText(text)
                    item.setToolTip(text)
                    changed = True
            if changed:
                self._fit_drama_rows()
            self._apply_drama_filter()
            return

        self._drama_sig = structure
        scroll = self.drama_list.verticalScrollBar().value()
        self._populating_dramas = True
        try:
            self.drama_list.blockSignals(True)
            self.drama_list.clear()
            all_row = QListWidgetItem(texts[0])
            all_row.setToolTip(texts[0])
            self.drama_list.addItem(all_row)
            for index, group in enumerate(groups):
                text = texts[index + 1]
                item = QListWidgetItem(text)
                item.setToolTip(text)
                item.setData(Qt.UserRole, group["id"])
                item.setFlags(item.flags() | Qt.ItemIsUserCheckable)
                item.setCheckState(Qt.Checked if group["id"] in self._checked_ids
                                   else Qt.Unchecked)
                self.drama_list.addItem(item)
            self._checked_ids &= {group["id"] for group in groups}
            known = {group["id"] for group in groups}
            if self._filter_drama is not None and self._filter_drama not in known:
                self._filter_drama = None
            self._select_drama_row()
            self._fit_drama_rows()
            self.delete_drama_button.setEnabled(self._filter_drama is not None)
        finally:
            self.drama_list.blockSignals(False)
            self._populating_dramas = False
            # setCurrentRow scrolls to the selected row; put the user's scroll
            # back where it was unless the selection genuinely has to be shown.
            self.drama_list.doItemsLayout()
            self.drama_list.verticalScrollBar().setValue(scroll)
        self._apply_drama_filter()

    def _apply_drama_filter(self, *_args):
        if not hasattr(self, "drama_search"): return
        query = self.drama_search.text().strip().casefold()
        state = self.drama_state.currentData() or ""
        groups = {group["id"]: group for group in self._drama_groups()}
        failed = {(job.get("drama") or {}).get("id") for job in self._jobs if job.get("state") == "failed"}
        count = 0
        for index in range(1, self.drama_list.count()):
            item = self.drama_list.item(index)
            group = groups.get(item.data(Qt.UserRole)) or {}
            matches = query in str(group.get("title") or "").casefold()
            if state == "active": matches = matches and group.get("done", 0) < group.get("total", 0)
            elif state == "done": matches = matches and group.get("total", 0) > 0 and group.get("done", 0) == group.get("total", 0)
            elif state == "failed": matches = matches and group.get("id") in failed
            item.setHidden(not matches)
            count += int(matches)
        self.drama_count.setText("显示 %d / %d 部" % (count, len(groups)))

    def _fit_drama_rows(self):
        """Give every drama row the height its wrapped text actually needs."""
        if getattr(self, "_fitting_dramas", False):
            return
        self._fitting_dramas = True
        try:
            width = max(120, self.drama_list.viewport().width() - 42)
            for index in range(self.drama_list.count()):
                item = self.drama_list.item(index)
                # 6px padding + 2px margin on both sides (see the QSS for
                # list items) plus 2px of slack: 16 is the real inset, and
                # being even 4px short makes Qt drop the second line.
                height = 62
                hint = item.sizeHint()
                if hint.width() != width or hint.height() != height:
                    item.setSizeHint(QSize(width, height))
        finally:
            self._fitting_dramas = False

    def _fit_merge_rows(self):
        """Keep merged rows exactly as wide as the (draggable) column."""
        if getattr(self, "_fitting_merges", False):
            return
        self._fitting_merges = True
        try:
            width = max(180, self.merge_files.viewport().width() - 8)
            for index in range(self.merge_files.count()):
                item = self.merge_files.item(index)
                if item.data(Qt.UserRole):      # real file, not the placeholder
                    widget = self.merge_files.itemWidget(item)
                    # Re-measure every time: the row must follow the font
                    # size, not just the column width.
                    title = getattr(widget, "title", None)
                    if title is not None:
                        title.set_wrap_width(width - 14)
                    height = max(70, widget.sizeHint().height()) if widget else 70
                    hint = item.sizeHint()
                    if hint.width() != width or hint.height() != height:
                        item.setSizeHint(QSize(width, height))
        finally:
            self._fitting_merges = False

    def _select_drama_row(self):
        row = 0
        if self._filter_drama is not None:
            for index in range(1, self.drama_list.count()):
                if self.drama_list.item(index).data(Qt.UserRole) == self._filter_drama:
                    row = index
                    break
        # setCurrentRow() scrolls the view; skip it when nothing changed so a
        # background refresh cannot yank the list back to the selected drama.
        if self.drama_list.currentRow() != row:
            self.drama_list.setCurrentRow(row)

    def _on_drama_item_changed(self, item):
        if self._populating_dramas:
            return
        drama_id = item.data(Qt.UserRole)
        if not drama_id:
            return
        if item.checkState() == Qt.Checked:
            self._checked_ids.add(drama_id)
        else:
            self._checked_ids.discard(drama_id)
        self._update_multi_button()

    def _update_multi_button(self):
        count = len(self._checked_ids)
        if count >= 2:
            self.merge_multi_button.setText("按季合并选中（%d 部）" % count)
            # 合并中也可点：按季合并会排到当前批次后面。
            self.merge_multi_button.setEnabled(True)
        else:
            self.merge_multi_button.setText("按季合并选中")
            self.merge_multi_button.setEnabled(False)

    def _multi_targets(self):
        """[(group, jobs)] for every checked drama, ordered by season."""
        pairs = []
        for group in self._drama_groups():
            if group["id"] not in self._checked_ids:
                continue
            jobs = sorted((job for job in self._jobs
                           if (job.get("drama") or {}).get("id") == group["id"]
                           and job.get("state") == "completed"),
                          key=lambda job: job.get("index", 0))
            if len(jobs) >= 2:
                pairs.append((group, jobs))
        pairs.sort(key=lambda pair: season_of(pair[0]["title"]))
        used = {}
        targets = []
        for group, jobs in pairs:
            season = season_of(group["title"])
            start = min(job.get("index", 0) for job in jobs)
            base = "%s S%02dE%02d" % (_safe_name(strip_season(group["title"])), season, start)
            used[base] = used.get(base, 0) + 1
            name = base + ("" if used[base] == 1 else " (%d)" % used[base]) + ".mkv"
            targets.append((dict(group, output_name=name), jobs))
        return targets

    def start_merge_multi(self):
        """Merge every checked season, in season order, one file each.

        A multi-season batch never auto-plays when it finishes (that is
        reserved for single-season merges).
        """
        targets = self._multi_targets()
        if len(targets) < 2:
            self.statusMessage.emit("请先在左侧勾选至少两部要合并的剧集（每部至少完成 2 集）")
            return
        self._start_batch(targets, skip_running=True)

    def _on_drama_changed(self, row):
        if self._populating_dramas:
            return
        item = self.drama_list.item(row)
        value = item.data(Qt.UserRole) if item is not None else None
        if value == self._filter_drama:
            return
        self._filter_drama = value
        # Refresh immediately on selection: the render below early-returns
        # when nothing else changed, so the button must not wait for it.
        self.delete_drama_button.setEnabled(value is not None)
        # The visible set changed, so every cached row must be rebuilt.
        self._row_sig.clear()
        self.queue.clear()
        self._render()

    # ---------------------------------------------------------------- render
    def _render(self):
        self._render_dramas()
        self.merge_tabs.setTabText(0, "下载队列 (%d)" % len(self._visible_jobs()))
        self._render_current()
        jobs = self._visible_jobs()
        queue = self.queue
        if queue.count() != len(jobs):
            # 行数变了才重建；重建会丢选中，所以按任务 id 记一下再恢复
            selected = {item.data(ROLE_JOB_ID) for item in queue.selectedItems()}
            scroll = queue.verticalScrollBar().value()
            queue.blockSignals(True)
            queue.clear()
            for _job in jobs:
                queue.addItem(QListWidgetItem())
            queue.blockSignals(False)
            self._row_sig.clear()
            if selected:
                for row in range(queue.count()):
                    if jobs[row].get("id") in selected:
                        queue.item(row).setSelected(True)
            # Adding/removing rows must not throw the user back to the top.
            queue.doItemsLayout()
            queue.verticalScrollBar().setValue(scroll)
        for row, job in enumerate(jobs):
            state = job.get("state", "")
            job_id = str(job.get("id") or "")
            chapter = job.get("chapter") or {}
            label = chapter.get("title") or "第 %d 集" % job.get("index", 0)
            progress = 1.0 if state == "completed" else float(job.get("progress") or 0.0)
            done = int(job.get("bytes") or 0)
            total = int(job.get("total") or 0)
            speed = float(job.get("speed") or job.get("bytesPerSecond") or 0.0)
            if speed <= 0 and state == "downloading":
                now = time.time()
                history = self._speed_hist.setdefault(job_id, [])
                history.append((now, done))
                while history and now - history[0][0] > 8.0:
                    history.pop(0)
                if len(history) >= 2:
                    span = now - history[0][0]
                    if span >= 1.0:
                        speed = max(0.0, (done - history[0][1]) / span)
            if state != "downloading":
                speed = 0.0
                self._speed_hist.pop(job_id, None)
            self._job_speed[job_id] = speed
            eta = 0
            if state == "downloading" and total > done and speed > 0:
                eta = int((total - done) / speed + 0.5)
            quality = job_quality_text(job)
            signature = (job.get("id"), state, round(progress, 4), done, total,
                         round(speed, 1), label, quality)
            if self._row_sig.get(row) != signature:
                self._row_sig[row] = signature
                item = queue.item(row)
                item.setText(label)
                item.setData(ROLE_JOB_STATE, state)
                item.setData(ROLE_JOB_PROGRESS, progress)
                item.setData(ROLE_JOB_SPEED, speed)
                item.setData(ROLE_JOB_BYTES, (done, total))
                item.setData(ROLE_JOB_ETA, eta)
                item.setData(ROLE_JOB_ID, job.get("id"))
                item.setData(ROLE_JOB_QUALITY, quality)
                item.setToolTip(self._job_tooltip(job, label, done, total, speed))
        current_ids = {str(job.get("id") or "") for job in jobs}
        for key in [k for k in self._speed_hist if k not in current_ids]:
            self._speed_hist.pop(key, None)
        for key in [k for k in self._job_speed if k not in current_ids]:
            self._job_speed.pop(key, None)
        for row in [r for r in self._row_sig if r >= len(jobs)]:
            self._row_sig.pop(row, None)
        self._update_merge_actions()
        # 顺手刷一遍合并队列：换主题后状态色要跟着 DESIGN 走，轮询就能带上。
        self._render_merge_queue()

    def _job_tooltip(self, job, label, done, total, speed=0.0):
        drama = (job.get("drama") or {}).get("title") or ""
        lines = ["%s · %s" % (drama, label) if drama else label,
                 "%s · %s / %s" % (STATE_TEXT.get(job.get("state", ""), job.get("state", "")),
                                   human_size(done), human_size(total))]
        quality = job_quality_text(job)
        if quality:
            lines.append("画质：%s" % quality.replace(" · ", "，码率 "))
        if job.get("state") == "downloading" and speed > 0 and total > done:
            lines.append("预计剩余：%s" % _duration_text((total - done) / speed))
        error = (job.get("error") or "").strip()
        if error:
            lines.append("失败原因：%s" % error)
        elif job.get("state") == "completed":
            lines.append("双击这一行可以直接播放")
        return "\n".join(lines)

    def _drama_eta(self, drama_id=None):
        """整部剧的预计剩余：活动任务剩余字节 + 排队集数 × 已完成的平均单集大小。"""
        jobs = [job for job in self._jobs
                if drama_id is None or (job.get("drama") or {}).get("id") == drama_id]
        speed = 0.0
        remaining = 0
        completed = 0
        completed_bytes = 0
        queued = 0
        for job in jobs:
            state = job.get("state")
            done = int(job.get("bytes") or 0)
            total = int(job.get("total") or 0)
            if state == "downloading":
                speed += self._job_speed.get(str(job.get("id") or ""), 0.0)
                if total > done:
                    remaining += total - done
            elif state == "completed":
                completed += 1
                completed_bytes += done or total
            elif state == "queued":
                queued += 1
        if speed <= 0:
            return 0
        if queued:
            average = completed_bytes / completed if completed else 0
            if average <= 0:
                # 还没有任何一集完成，排队集的大小无从估计。
                return 0
            remaining += average * queued
        if remaining <= 0:
            return 0
        return remaining / speed

    def _eta_suffix(self, drama_id=None):
        eta = _duration_text(self._drama_eta(drama_id))
        if eta:
            return " · 预计剩余 " + eta
        jobs = [job for job in self._jobs
                if drama_id is None or (job.get("drama") or {}).get("id") == drama_id]
        if any(job.get("state") == "downloading" for job in jobs):
            return " · 预计剩余计算中…"
        return ""

    def _render_current(self):
        """当前剧集卡片：标题、总进度，以及这部剧能不能合并。"""
        groups = self._drama_groups()
        if self._filter_drama is None:
            title = "全部剧集"
            done = sum(group["done"] for group in groups)
            total = sum(group["total"] for group in groups)
            size = sum(group["bytes"] for group in groups)
            self.current_meta.setText("%d 部 · 已完成 %d/%d 集 · %s%s"
                                      % (len(groups), done, total, human_size(size),
                                         self._eta_suffix()))
        else:
            group = next((g for g in groups if g["id"] == self._filter_drama), None)
            if group is None:
                self.current_title.setText("全部剧集")
                self.current_meta.setText("")
                self.current_bar.setValue(0)
                return
            title, done, total, size = (group["title"], group["done"],
                                        group["total"], group["bytes"])
            self.current_meta.setText("已完成 %d/%d 集 · %s%s"
                                      % (done, total, human_size(size),
                                         self._eta_suffix(group["id"])))
        self.current_title.setText(title)
        self.current_title.setToolTip(title)
        self.current_bar.setValue(int(1000 * done / total) if total else 0)

    # ------------------------------------------------------------ 行内操作
    def job_action(self, row, key):
        """队列卡片上的按钮：暂停 / 继续 / 播放 / 删除。"""
        jobs = self._visible_jobs()
        if not 0 <= row < len(jobs):
            return
        job = jobs[row]
        if key == "play":
            self._play_job(job)
        else:
            self._control(job, key)

    def _play_row(self, item):
        """双击一行：已完成的直接播放（别的状态双击不做事）。"""
        row = self.queue.row(item)
        jobs = self._visible_jobs()
        if 0 <= row < len(jobs) and jobs[row].get("state") == "completed":
            self._play_job(jobs[row])

    def _mini(self, label, handler):
        button = QPushButton(label)
        button.setProperty("role", "ghost")
        button.setFixedWidth(52)
        button.clicked.connect(lambda: handler())
        return button

    # -------------------------------------------------------------- controls
    def _control(self, job, command):
        if command == "remove":
            # 行先消失，删除在后台完成；失败时再放回来。
            self._hide_jobs([job])

        def work():
            self._core.control(job["id"], command)
            return command

        def done(applied):
            if applied == "remove":
                self.publisher.forget(job)
            self.refresh()

        def failed(message):
            if command == "remove":
                self._hidden_ids.discard(str(job.get("id") or ""))
                self.refresh()
            self._control_failed(message)

        self._runner.run(work, on_success=done, on_error=failed)

    def _control_failed(self, message):
        self.statusMessage.emit(message)

    def _control_all(self, command):
        def work():
            self._core.control("", command)

        self._runner.run(work, on_error=self._control_failed)
        self.refresh()

    def selected_jobs(self):
        rows = sorted(self.queue.row(item) for item in self.queue.selectedItems())
        visible = self._visible_jobs()
        return [visible[row] for row in rows if row < len(visible)]

    def _play_job(self, job):
        """Route a finished episode through the episode flow: that loads the
        season's chapter list, so auto-next and episode picking all work."""
        drama = job.get("drama") or {}
        position = max(0, int(job.get("index") or 1) - 1)
        self.playEpisodeRequested.emit(drama, position)

    def _open_folder(self):
        # Open the user-facing library, not the core's internal cache.
        path = tools.download_dir()
        os.makedirs(path, exist_ok=True)
        os.startfile(path) if os.name == "nt" else None

    def _choose_location(self):
        """Point the library somewhere else, keeping what is already there.

        The folder is created and checked before the setting is written, so a
        failed pick can never leave the app pointing at a path it cannot use.
        ``stash_index`` hands the new root the episode records of the old one
        - without it every finished job would look unpublished again and the
        publisher would re-decrypt the whole library in the background.
        """
        dialog = _LocationDialog(self)
        if dialog.exec_() != QDialog.Accepted:
            return
        default_root = tools.project_dir()
        chosen = dialog.value()
        if os.path.abspath(chosen) == os.path.abspath(default_root):
            chosen = ""                      # 等于默认位置：设置里不留多余项
        previous = tools.download_dir()
        target = os.path.join(chosen or default_root, "下载")
        if os.path.abspath(target) == os.path.abspath(previous):
            return
        try:
            os.makedirs(target, exist_ok=True)
        except OSError as exc:
            QMessageBox.warning(self, "下载位置", "这个目录不能用：%s\n%s" % (target, exc))
            return
        try:
            tools.set_library_root(chosen)
        except OSError as exc:
            QMessageBox.warning(self, "下载位置", "保存设置失败：%s" % exc)
            return
        carried = library.stash_index(previous, target)
        message = "成品库已切换到 %s" % target
        if carried:
            message += "（沿用 %d 条成品记录）" % carried
        self.statusMessage.emit(message)
        self.refresh()

    # ------------------------------------------------------------ decryption
    def _start_decrypt(self):
        """Decrypt newly finished downloads in the background.

        Downloads land encrypted in the core cache; this turns each one into a
        named, playable episode under 下载/<剧名>/ without touching the GUI
        thread.  One pass at a time keeps ffmpeg from thrashing the disk.
        """
        if self._decrypt_task is not None:
            return
        jobs = [job for job in self._jobs
                if job.get("state") == "completed" and not self.publisher.known(job)
                and self._decrypt_failures.get(job.get("id"), 0) < 3]
        if not jobs:
            return
        self._decrypt_cancel = threading.Event()
        cancel = self._decrypt_cancel

        def progress(text, ratio):
            self._post_status("%s（%d%%）" % (text, int(ratio * 100)))

        def failed_job(job, exc):
            # A busy index is a temporary I/O condition, not a bad episode:
            # retrying it later is right and must not use up the 3 attempts.
            if isinstance(exc, library.IndexBusy):
                return
            job_id = job.get("id")
            count = self._decrypt_failures.get(job_id, 0) + 1
            self._decrypt_failures[job_id] = count
            if count == 3:
                self.statusMessage.emit(
                    "%s 连续 3 次解密失败，已暂停自动重试：%s"
                    % (self.publisher._label(job), exc))

        def work():
            return self.publisher.publish_pending(jobs, progress=progress,
                                                  cancel=cancel, on_failure=failed_job)

        def done(entries):
            self._decrypt_task = None
            self.decrypted += len(entries)
            for entry in entries:
                self._decrypt_failures.pop(entry.get("jobId"), None)
            if entries:
                # statusMessage is str-only; the 8s timeout lives in the
                # receiver.  A second argument here would qFatal the app.
                self.statusMessage.emit("已解密 %d 集，保存到 %s" % (len(entries), tools.download_dir()))
            self._render()

        def failed(message):
            self._decrypt_task = None
            self.merge_status.setText("解密失败：%s" % message)

        runner = TaskRunner(self)
        self._decrypt_task = runner
        runner.run(work, on_success=done, on_error=failed)

    # ----------------------------------------------------------------- merge
    def _merge_targets(self):
        """[(drama_group, completed_jobs)] for everything the button would merge."""
        wanted = self._filter_drama
        targets = []
        for group in self._drama_groups():
            if wanted is not None and group["id"] != wanted:
                continue
            jobs = sorted((job for job in self._jobs
                           if (job.get("drama") or {}).get("id") == group["id"]
                           and job.get("state") == "completed"),
                          key=lambda job: job.get("index", 0))
            if len(jobs) >= 2:
                targets.append((group, jobs))
        return targets

    def _update_merge_actions(self):
        busy = self._merge_task is not None or bool(self._merge_queue)
        targets = self._merge_targets()
        episodes = sum(len(jobs) for _group, jobs in targets)
        # 合并进行中不再禁用：新点进来的任务排到当前批次后面，等上一轮结束
        # 自动继续，用户不用守着合并完再点一次。
        self.merge_season_button.setEnabled(bool(targets))
        self.merge_button.setEnabled(True)
        self.merge_multi_button.setEnabled(len(self._checked_ids) >= 2)
        suffix = "（加入队列）" if busy else ""
        # 按钮文字不再带剧名：剧名一长，右栏最小宽度就被撑大，QSplitter 会从
        # 左栏抢宽度，点不同剧时左栏忽宽忽窄、行高跟着跳。剧名放 tooltip，
        # 标题卡片上也一直显示着。
        if self._filter_drama is None:
            if targets:
                self.merge_season_button.setText("一键合并全部（%d 部 / %d 集）%s"
                                                % (len(targets), episodes, suffix))
            else:
                self.merge_season_button.setText("一键合并全部")
            tip = "把列表里所有剧集的已完成分集分别合并成完整视频"
        else:
            title = next((g["title"] for g in self._drama_groups()
                          if g["id"] == self._filter_drama), "")
            if targets:
                self.merge_season_button.setText("一键合并整季（%d 集）%s"
                                                % (episodes, suffix))
            else:
                self.merge_season_button.setText("一键合并整季")
            tip = "把「%s」的全部已完成分集合成一个完整视频" % (title or "当前剧集")
        if busy:
            tip += "；合并进行中，点击会排到当前合并之后"
        self.merge_season_button.setToolTip(tip)

    def start_merge_season(self):
        """Merge every completed, continuous season the current view covers."""
        targets = self._merge_targets()
        if not targets:
            self.statusMessage.emit("没有可合并的分集：请先在左侧选中一部剧，并至少完成 2 集")
            return
        # “全部”时跳过正在合并的那部；“整季”是用户点名要合，允许排队重合并。
        self._start_batch(targets, skip_running=self._filter_drama is None)

    def start_merge(self):
        """Merge only the rows the user highlighted in the table."""
        jobs = [job for job in self.selected_jobs() if job.get("state") == "completed"]
        if len(jobs) < 2:
            self.statusMessage.emit("请先在同一剧中选中至少 2 个已完成的分集")
            return
        ids = {(job.get("drama") or {}).get("id") for job in jobs}
        if len(ids) != 1:
            self.statusMessage.emit("只能合并同一部剧的分集")
            return
        group = next((g for g in self._drama_groups() if g["id"] in ids),
                     {"id": "", "title": "选中分集"})
        self._start_batch([(group, sorted(jobs, key=lambda job: job.get("index", 0)))],
                          skip_running=False)

    def _toggle_merge_verify(self, on):
        """记住完整校验开关；对下一次合并生效。"""
        tools.set_merge_verify_enabled(on)
        self.statusMessage.emit("完整解码校验已%s（下一次合并生效）" % ("开启" if on else "关闭"))

    def _start_batch(self, targets, skip_running=True):
        if not targets:
            self.statusMessage.emit("没有可合并的分集")
            return
        if self._merge_task is not None or self._merge_queue:
            self._enqueue_targets(targets, skip_running=skip_running)
            return
        self._merge_queue = list(targets)
        self._merge_total = len(self._merge_queue)
        self._merge_processed = 0
        self._merge_outputs = []
        self._merge_failures = []
        self._merge_entries = [self._merge_entry(group, jobs)
                               for group, jobs in targets]
        self.merge_tabs.setCurrentIndex(1)
        self._render_merge_queue()
        self._run_next_merge()

    def _enqueue_targets(self, targets, skip_running=True):
        """把新任务排到当前合并批次后面，等上一轮结束自动继续。"""
        ahead = (1 if self._merge_task is not None else 0) + len(self._merge_queue)
        current = self._merge_current_id if skip_running else None
        queued = {group.get("id"): index
                  for index, (group, _jobs) in enumerate(self._merge_queue)}
        appended = 0
        replaced = 0
        skipped = 0
        for group, jobs in targets:
            group_id = group.get("id")
            if current is not None and group_id == current:
                skipped += 1
                continue
            if group_id in queued:
                self._merge_queue[queued[group_id]] = (group, jobs)
                replaced += 1
                continue
            self._merge_queue.append((group, jobs))
            queued[group_id] = len(self._merge_queue) - 1
            appended += 1
        if appended:
            self._merge_total += appended
        known = {entry["id"]: entry for entry in self._merge_entries}
        for group, jobs in targets:
            group_id = group.get("id")
            if current is not None and group_id == current:
                continue
            entry = known.get(group_id)
            if entry is None or entry["state"] != "queued":
                # 合完再加新分集重合同一部：留一条历史，不覆盖上一轮的结果
                self._merge_entries.append(self._merge_entry(group, jobs))
            else:
                entry.update(self._merge_entry(group, jobs))
        self._render_merge_queue()
        self._update_merge_actions()
        names = "、".join((group.get("title") or group.get("id") or "").strip()
                          for group, _jobs in targets)
        if appended:
            self.statusMessage.emit("已加入合并队列：%s（前面还有 %d 部）" % (names, ahead))
        elif replaced:
            self.statusMessage.emit("已更新合并队列中的任务：%s" % names)
        elif skipped:
            self.statusMessage.emit("「%s」正在合并，完成后可再次合并新增分集" % names)

    def _run_next_merge(self):
        if not self._merge_queue:
            self._finish_merge_batch()
            return
        group, jobs = self._merge_queue.pop(0)
        self._merge_current_id = group.get("id")
        self._merge_processed += 1
        self._merge_stage = ""
        self._merge_stage_start = 0.0
        self._merge_last_progress = time.time()
        self._merge_count = len(jobs)
        for entry in self._merge_entries:
            if entry["id"] == (group.get("id") or "") and entry["state"] == "queued":
                entry["state"] = "merging"
                entry["progress"] = 0.0
                break
        self.merge_tabs.setCurrentIndex(1)
        self._render_merge_queue()
        self.merge_eta.setText("")
        self.merge_status.setText("合并 %s（第 %d/%d 部 · %d 集）"
                                  % (group["title"], self._merge_processed,
                                     self._merge_total, len(jobs)))
        self.merge_bar.setVisible(True)
        self.merge_bar.setValue(0)
        task = media.MergeTask(self._core, jobs,
                               on_progress=self._merge_progress,
                               on_status=self._post_status,
                               output_name=group.get("output_name"))
        self._merge_task = task
        self._update_merge_actions()

        def work():
            return task.run()

        self._runner.run(work, on_success=self._merge_one_done,
                         on_error=self._merge_one_failed)

    def _merge_one_done(self, result):
        self._merge_task = None
        self._merge_current_id = None
        path = result.get("path") if isinstance(result, dict) else None
        if path:
            self._merge_outputs.append(path)
            self.merge_status.setText("已生成：%s" % os.path.basename(path))
            self.statusMessage.emit("合并完成：%s" % path)
            self._merge_files_sig = None
            self._refresh_merge_files()
        self._finish_merge_entry("done", output=path or "")
        self._run_next_merge()

    def _merge_one_failed(self, message):
        self._merge_task = None
        self._merge_current_id = None
        self._merge_failures.append(message)
        self._finish_merge_entry("failed", detail=message)
        self.statusMessage.emit("合并失败：%s" % message)
        self._run_next_merge()

    def _finish_merge_batch(self):
        self._merge_task = None
        self._merge_current_id = None
        self._merge_queue = []
        self._merge_stage = ""
        self.merge_eta.setText("")
        self.merge_bar.setVisible(False)
        if len(self._merge_outputs) == 1 and not self._merge_failures:
            self.merge_status.setText("合并完成：%s" % os.path.basename(self._merge_outputs[0]))
        elif self._merge_outputs or self._merge_failures:
            self.merge_status.setText("合并结束：成功 %d 部 · 失败 %d 部"
                                      % (len(self._merge_outputs), len(self._merge_failures)))
        if self._merge_failures and not self._merge_outputs:
            self.statusMessage.emit("合并失败：%s" % self._merge_failures[0])
        elif self._merge_failures:
            self.statusMessage.emit("有 %d 部合并失败：%s"
                                    % (len(self._merge_failures), self._merge_failures[0]))
        self._update_merge_actions()

    def _merge_progress(self, ratio, text):
        """Runs on the merge worker thread - never touch widgets here."""
        ratio = max(0.0, min(1.0, float(ratio or 0.0)))
        self._mergeBarUpdate.emit(ratio, text or "")
        self._post_status(text)

    _MERGE_STAGES = (("解密", "decrypt"), ("解析媒体信息", "probe"),
                     ("统一分集格式", "normalize"), ("合并视频", "concat"),
                     ("完整解码校验", "verify"), ("保存成品", "save"))

    def _merge_stage_of(self, text):
        for marker, name in self._MERGE_STAGES:
            if marker in text:
                return name
        return ""

    def _update_merge_eta(self, ratio, text):
        """按当前阶段的完成量/阶段进度估算剩余，阶段切换就重新计时。

        合并的总进度是分阶段加权的（解密 8%、解析 7%、统一编码 30%、拼接 10%、
        校验 40%），用总进度线性外推会在阶段交界处给出荒唐的数字；这里只算
        当前阶段还剩多久，阶段内没有新进度时由 _poll 改显示“本阶段已用”。
        """
        now = time.time()
        stage = self._merge_stage_of(text)
        if stage and stage != self._merge_stage:
            self._merge_stage = stage
            self._merge_stage_start = now
            self._merge_stage_ratio = ratio
        if not stage:
            return
        self._merge_last_progress = now
        match = re.search(r"(\d+)/(\d+)", text)
        done = total = 0
        if match:
            done, total = int(match.group(1)), int(match.group(2))
        elif stage == "decrypt":
            match = re.search(r"解密第\s*(\d+)\s*集", text)
            if match:
                done = int(match.group(1))
                total = max(1, self._merge_count)
        elapsed = now - self._merge_stage_start
        eta = 0.0
        if stage in ("normalize", "probe") and done and total and done >= 2 and elapsed >= 2:
            eta = max(0.0, (total - done) * elapsed / done)
        elif stage == "decrypt" and done and total and elapsed >= 2:
            eta = max(0.0, (total - done) * elapsed / max(1, done))
        elif stage in ("concat", "verify"):
            base, span = (0.45, 0.10) if stage == "concat" else (0.55, 0.40)
            fraction = max(0.0, min(1.0, (ratio - base) / span))
            if fraction > 0.02 and elapsed > 2:
                eta = (1.0 - fraction) * elapsed / fraction
        elif stage == "save":
            self.merge_eta.setText("")
            return
        text_eta = _duration_text(eta)
        self.merge_eta.setText(("本阶段剩余 " + text_eta) if text_eta else "本阶段计算中…")

    def _refresh_merge_eta(self):
        """进度长时间没有更新时，把冻结的倒计时换成已用时间。"""
        if not self._merge_stage:
            return
        idle = time.time() - self._merge_last_progress
        if idle < 15:
            return
        elapsed = max(0.0, time.time() - self._merge_stage_start)
        self.merge_eta.setText("本阶段已用 %d:%02d" % (int(elapsed) // 60, int(elapsed) % 60))

    def _apply_merge_progress(self, ratio, text=""):
        ratio = max(0.0, min(1.0, float(ratio or 0.0)))
        self.merge_bar.setVisible(True)
        self.merge_bar.setValue(int(ratio * 100))
        changed = False
        for entry in self._merge_entries:
            if entry["state"] == "merging":
                if int(round(entry["progress"] * 100)) != int(ratio * 100):
                    entry["progress"] = ratio
                    changed = True
                break
        if changed:
            self._render_merge_queue()
        self._update_merge_eta(ratio, text or "")

    # -------------------------------------------------------------- teardown
    def shutdown(self):
        """Stop polling and wind down any running merge/decrypt pass."""
        try:
            self._timer.stop()
        except Exception:
            pass
        self._merge_queue = []
        if self._decrypt_cancel is not None:
            self._decrypt_cancel.set()
        if self._merge_task is not None:
            self._merge_task.cancel()
        # ffmpeg children are not killed by their parent on Windows; stop
        # them explicitly, then give the workers a moment to unwind.
        try:
            media.kill_running()
        except Exception:
            pass
        try:
            self._runner.shutdown(1500)
        except Exception:
            pass

