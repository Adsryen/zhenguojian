"""Download queue panel: grouped by drama, with prominent batch merge."""
from __future__ import annotations

import os
import re
import shutil
import threading
import time

from PyQt5.QtCore import (QEvent, QPointF, QRect, QRectF, QSize, Qt, QTimer,
                          pyqtSignal)
from PyQt5.QtGui import (QColor, QFont, QFontMetrics, QPainter, QPainterPath,
                         QPen)
from PyQt5.QtWidgets import (QAbstractItemView, QCheckBox, QDialog, QFileDialog, QHBoxLayout,
                             QLabel, QLineEdit, QListWidget, QListWidgetItem,
                             QMessageBox, QProgressBar, QPushButton, QScrollArea, QSplitter,
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
ROW_HEIGHT = 48
CARD_RADIUS = 10
COL_STATE = 72          # 状态胶囊宽
META_WIDTH = 172        # 右侧两行元信息（速度 / 大小·剩余）
PERCENT_WIDTH = 46      # 进度条右端百分数
NAME_COLUMN = 210       # 名称区固定列宽（剧名+集数+站源），胶囊/进度条各行对齐
ICON_SIZE = 26          # 行尾图标按钮（正方形）
ICON_GAP = 6
ICON_EDGE = 12          # 图标区距卡片右缘

ROW_ACTIONS = {
    "downloading": ("pause", "remove"),
    "queued": ("pause", "remove"),
    "paused": ("resume", "remove"),
    "completed": ("play", "folder", "remove"),
    "failed": ("retry", "remove"),
}
ACTION_TEXT = {"pause": "暂停", "resume": "继续", "play": "播放",
               "retry": "重试", "folder": "打开目录", "remove": "删除"}
# 失败行的「重试」常驻提亮（D1d）；其余图标默认淡化、悬停行才提亮。
ALWAYS_ON_ICONS = {"failed": ("retry",)}

# 站源 slug → 展示名；查不到就原样显示 slug（只写观察到的，别杜撰）。
SOURCE_LABELS = {"hongguo": "红果"}


def _source_label(source):
    slug = str(source or "").strip()
    if not slug:
        return ""
    return SOURCE_LABELS.get(slug, slug)


def _episode_sort_key(name):
    """归档行按集数排：第N集 / E01 / 结尾数字；解不出的排最后。"""
    m = re.search(r"第\s*([0-9]{1,4})\s*集", name)
    if m:
        return int(m.group(1))
    m = re.search(r"[Ee]([0-9]{1,4})", name)
    if m:
        return int(m.group(1))
    m = re.search(r"([0-9]{1,4})\s*$", name)
    if m:
        return int(m.group(1))
    return 10 ** 9


def _archive_drama_key(filename):
    """合并成品文件名 → 剧名分组 key：剥掉集数范围/SxxExx/结尾数字，再去季号。"""
    stem = os.path.splitext(os.path.basename(filename))[0]
    stem = re.sub(r"\s*全\s*[0-9]+\s*-\s*[0-9]+\s*集.*$", "", stem)
    stem = re.sub(r"\s*[Ss][0-9]{1,2}[Ee][0-9]{1,4}.*$", "", stem)
    stem = re.sub(r"\s+[0-9]{1,4}$", "", stem)
    return strip_season(stem.strip()) or stem.strip() or "合集"

# 列表项数据（只在本模块用，和 widgets.py 的角色不冲突）
ROLE_JOB_STATE = Qt.UserRole + 1
ROLE_JOB_PROGRESS = Qt.UserRole + 2
ROLE_JOB_SPEED = Qt.UserRole + 3
ROLE_JOB_BYTES = Qt.UserRole + 4
ROLE_JOB_ID = Qt.UserRole + 5
ROLE_JOB_QUALITY = Qt.UserRole + 6
ROLE_JOB_ETA = Qt.UserRole + 7
ROLE_JOB_DRAMA = Qt.UserRole + 8   # (剧名, 站源展示名)，行首名称区用


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


def _icon_zone_width(state):
    """行尾图标区（含右边距）占掉的总宽；paint 与布局共用。"""
    keys = ROW_ACTIONS.get(state, ())
    if not keys:
        return 0
    return len(keys) * ICON_SIZE + (len(keys) - 1) * ICON_GAP + ICON_EDGE


def _row_buttons(card, state):
    """这一行的图标按钮 [(key, rect)]，从右往左排。

    画和点击判定共用同一份几何：写两遍迟早会错位。
    """
    keys = ROW_ACTIONS.get(state, ("remove",))
    items = []
    right = card.right() - ICON_EDGE
    for key in reversed(keys):
        items.append((key, QRect(right - ICON_SIZE, card.center().y() - ICON_SIZE // 2,
                                 ICON_SIZE, ICON_SIZE)))
        right -= ICON_SIZE + ICON_GAP
    return items


def _draw_action_icon(painter, rect, key, colour):
    """在 26×26 按钮中心画一个约 14px 的线性图标。

    全部自绘（不引图标库）：形状都是两三笔线条/实心块，缩放不糊，也不破坏
    「不新增依赖」的约束。
    """
    cx = rect.center().x()
    cy = rect.center().y()
    painter.save()
    painter.setRenderHint(QPainter.Antialiasing, True)
    if key in ("play", "resume"):
        path = QPainterPath()
        path.moveTo(cx - 4, cy - 6)
        path.lineTo(cx - 4, cy + 6)
        path.lineTo(cx + 6, cy)
        path.closeSubpath()
        painter.setPen(Qt.NoPen)
        painter.setBrush(colour)
        painter.drawPath(path)
    elif key == "pause":
        painter.setPen(Qt.NoPen)
        painter.setBrush(colour)
        painter.drawRoundedRect(QRectF(cx - 5, cy - 6, 3.2, 12), 1.6, 1.6)
        painter.drawRoundedRect(QRectF(cx + 1.8, cy - 6, 3.2, 12), 1.6, 1.6)
    elif key == "retry":
        pen = QPen(colour, 1.7)
        pen.setCapStyle(Qt.RoundCap)
        painter.setPen(pen)
        painter.setBrush(Qt.NoBrush)
        painter.drawArc(QRectF(cx - 5.5, cy - 5.5, 11, 11), 70 * 16, 260 * 16)
        head = QPainterPath()
        head.moveTo(cx + 0.5, cy - 7.5)
        head.lineTo(cx + 6.5, cy - 6.0)
        head.lineTo(cx + 2.5, cy - 1.5)
        head.closeSubpath()
        painter.setPen(Qt.NoPen)
        painter.setBrush(colour)
        painter.drawPath(head)
    elif key == "folder":
        pen = QPen(colour, 1.6)
        pen.setJoinStyle(Qt.RoundJoin)
        painter.setPen(pen)
        painter.setBrush(Qt.NoBrush)
        path = QPainterPath()
        path.moveTo(cx - 7, cy + 5)
        path.lineTo(cx - 7, cy - 5)
        path.lineTo(cx - 2.5, cy - 5)
        path.lineTo(cx - 1, cy - 3)
        path.lineTo(cx + 7, cy - 3)
        path.lineTo(cx + 7, cy + 5)
        path.closeSubpath()
        painter.drawPath(path)
    elif key == "remove":
        pen = QPen(colour, 1.6)
        pen.setCapStyle(Qt.RoundCap)
        pen.setJoinStyle(Qt.RoundJoin)
        painter.setPen(pen)
        painter.setBrush(Qt.NoBrush)
        painter.drawLine(QPointF(cx - 6, cy - 5), QPointF(cx + 6, cy - 5))
        painter.drawLine(QPointF(cx - 1.5, cy - 7), QPointF(cx + 1.5, cy - 7))
        painter.drawLine(QPointF(cx - 4.5, cy - 5), QPointF(cx - 3.6, cy + 6))
        painter.drawLine(QPointF(cx + 4.5, cy - 5), QPointF(cx + 3.6, cy + 6))
        painter.drawLine(QPointF(cx - 3.6, cy + 6), QPointF(cx + 3.6, cy + 6))
        painter.drawLine(QPointF(cx - 1.5, cy - 2), QPointF(cx - 1.4, cy + 3))
        painter.drawLine(QPointF(cx + 1.5, cy - 2), QPointF(cx + 1.4, cy + 3))
    painter.restore()

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
            fill, border = QColor(DESIGN["guo_accent_soft"]), QColor(DESIGN["guo_accent"])
        elif hovered:
            fill, border = QColor(DESIGN["guo_chip_hover"]), QColor(DESIGN["guo_border"])
        else:
            fill, border = QColor(DESIGN["guo_chip_bg"]), QColor(DESIGN["guo_border"])
        painter.setPen(QPen(border, 1))
        painter.setBrush(fill)
        painter.drawRoundedRect(card.adjusted(0, 0, -1, -1), CARD_RADIUS, CARD_RADIUS)

        colour = QColor(state_colour(state))
        buttons = _row_buttons(card, state)
        icons_w = _icon_zone_width(state)
        meta_right = card.right() - icons_w - 2
        meta_rect = QRect(meta_right - META_WIDTH, card.top(), META_WIDTH, card.height())

        # 名称区：剧名(粗) · 第N集(常规) · 站源(淡化)。固定列宽，状态胶囊对齐。
        left = card.left() + 14
        label = str(index.data(Qt.DisplayRole) or "")
        drama_title, source = index.data(ROLE_JOB_DRAMA) or ("", "")
        font_bold = QFont(option.font)
        font_bold.setPixelSize(13)
        font_bold.setBold(True)
        font_plain = QFont(option.font)
        font_plain.setPixelSize(12)
        font_small = QFont(option.font)
        font_small.setPixelSize(11)
        fm_bold = QFontMetrics(font_bold)
        fm_plain = QFontMetrics(font_plain)
        fm_small = QFontMetrics(font_small)
        suffix = " · " + label
        tail = (" · " + source) if source else ""
        suffix_w = fm_plain.width(suffix)
        tail_w = fm_small.width(tail) if tail else 0
        name_area = max(40, NAME_COLUMN - suffix_w - tail_w)
        shown_drama = fm_bold.elidedText(drama_title or label, Qt.ElideMiddle, name_area)
        painter.setFont(font_bold)
        painter.setPen(QColor(DESIGN["guo_text"]))
        painter.drawText(QRect(left, card.top(), name_area, card.height()),
                         Qt.AlignLeft | Qt.AlignVCenter, shown_drama)
        cursor = left + min(fm_bold.width(shown_drama), name_area)
        painter.setFont(font_plain)
        painter.setPen(QColor(DESIGN["muted"]))
        painter.drawText(QRect(cursor, card.top(), suffix_w + 4, card.height()),
                         Qt.AlignLeft | Qt.AlignVCenter, suffix)
        cursor += suffix_w
        if tail:
            painter.setFont(font_small)
            painter.setPen(QColor(DESIGN["muted2"]))
            painter.drawText(QRect(cursor, card.top(), tail_w + 4, card.height()),
                             Qt.AlignLeft | Qt.AlignVCenter, tail)
            cursor += tail_w

        # 状态胶囊：固定列，各行对齐；进度条从胶囊后开始。
        middle = card.center().y()
        pill = QRect(left + NAME_COLUMN + 10, middle - 11, COL_STATE, 22)
        if pill.right() + 60 > meta_rect.left():
            # 极窄窗口：胶囊退回文字后，保进度条可用宽度
            pill = QRect(cursor + 8, middle - 11, COL_STATE, 22)
        painter.setPen(Qt.NoPen)
        painter.setBrush(_tint(colour, 56))
        painter.drawRoundedRect(pill, 11, 11)
        painter.setPen(colour)
        font_pill = QFont(option.font)
        font_pill.setPixelSize(12)
        painter.setFont(font_pill)
        painter.drawText(pill, Qt.AlignCenter, STATE_TEXT.get(state, state))

        # 右侧两行元信息：线1 速度（下载中）或画质；线2 大小 · 剩余
        speed = float(index.data(ROLE_JOB_SPEED) or 0.0)
        eta = int(index.data(ROLE_JOB_ETA) or 0)
        done, total = index.data(ROLE_JOB_BYTES) or (0, 0)
        quality = index.data(ROLE_JOB_QUALITY) or ""
        line1 = line2 = ""
        if state == "downloading":
            line1 = _speed_text(speed) if speed > 0 else ""
        elif state in ("completed", "failed", "paused", "queued"):
            line1 = quality
        if total:
            line2 = "%s/%s" % (human_size(done).replace(" ", ""),
                               human_size(total).replace(" ", ""))
            if state == "downloading" and eta > 0:
                line2 += " · " + _eta_text(eta)
        painter.setPen(QColor(DESIGN["guo_text"]))
        font_meta1 = QFont(option.font)
        font_meta1.setPixelSize(12)
        painter.setFont(font_meta1)
        painter.drawText(QRect(meta_rect.left(), middle - 22, meta_rect.width(), 20),
                         Qt.AlignRight | Qt.AlignVCenter, line1)
        font_meta2 = QFont(option.font)
        font_meta2.setPixelSize(11)
        painter.setFont(font_meta2)
        painter.setPen(QColor(DESIGN["muted"]))
        painter.drawText(QRect(meta_rect.left(), middle + 1, meta_rect.width(), 19),
                         Qt.AlignRight | Qt.AlignVCenter, line2)

        # 中间剩下的都给细进度条 + 百分数
        bar_left = pill.right() + 14
        bar_area_right = meta_rect.left() - 10
        percent = index.data(ROLE_JOB_PROGRESS)
        percent = 0.0 if percent is None else max(0.0, min(1.0, float(percent)))
        bar_width = bar_area_right - bar_left - PERCENT_WIDTH
        if bar_width >= 40:
            track = QRect(bar_left, middle - 2, bar_width, 5)
            painter.setPen(Qt.NoPen)
            painter.setBrush(QColor(DESIGN["guo_border"]))
            painter.drawRoundedRect(track, 2.5, 2.5)
            filled = int(bar_width * percent)
            if filled > 0:
                painter.setBrush(colour)
                painter.drawRoundedRect(QRect(track.left(), track.top(), max(3, filled), 5),
                                        2.5, 2.5)
            font_pct = QFont(option.font)
            font_pct.setPixelSize(11)
            painter.setFont(font_pct)
            painter.setPen(QColor(DESIGN["muted"]))
            painter.drawText(QRect(track.right() + 8, card.top(), PERCENT_WIDTH - 6, card.height()),
                             Qt.AlignLeft | Qt.AlignVCenter, "%.0f%%" % (percent * 100))

        # 行尾图标按钮：默认淡化，悬停行提亮，悬停单按钮加圆底；失败行重试常驻。
        for key, rect in buttons:
            base = QColor(DESIGN["danger"] if key == "remove" else DESIGN["guo_accent"])
            force = key in ALWAYS_ON_ICONS.get(state, ())
            if hover_button == key:
                painter.setPen(Qt.NoPen)
                painter.setBrush(_tint(base, 60))
                painter.drawRoundedRect(rect.adjusted(0, 0, -1, -1), 8, 8)
                ink = base
            elif hovered or force:
                ink = QColor(DESIGN["danger"] if key == "remove" else DESIGN["guo_text"])
            else:
                ink = QColor(DESIGN["muted2"])
            _draw_action_icon(painter, rect, key, ink)

        painter.restore()

    def editorEvent(self, event, model, option, index):
        if event.type() != QEvent.MouseButtonRelease or event.button() != Qt.LeftButton:
            return False
        for key, rect in _row_buttons(option.rect, index.data(ROLE_JOB_STATE) or ""):
            if rect.contains(event.pos()):
                self._owner.job_action(index.row(), key)
                return True
        return False


def _make_chip(text, strong=False):
    """小胶囊标签（集数/占用/整集合并）。颜色只从 DESIGN 取。"""
    label = QLabel(text)
    label.setProperty("guoRole", "chip")
    label.setStyleSheet(
        "QLabel{background:%s;border:1px solid %s;border-radius:6px;"
        "padding:1px 7px;color:%s;font-size:11px;}"
        % (DESIGN["guo_chip_bg"], DESIGN["guo_border"],
           DESIGN["guo_accent"] if strong else DESIGN["guo_muted"]))
    return label


class _ArchiveFileRow(QWidget):
    """归档组内的一行成品：名称 + 徽标 + 大小 + 播放/删除。

    整行单击即播放（与旧「合并成品」行为一致，防误点靠面板级去抖），
    按钮吃掉自己的事件，不会连带触发行点击。
    """

    playRequested = pyqtSignal(str)
    deleteRequested = pyqtSignal(str)

    def __init__(self, path, name, size_text, badge="", mark="", parent=None):
        super().__init__(parent)
        self._path = path
        self._hover = False
        self.setFixedHeight(30)
        self.setCursor(Qt.PointingHandCursor)
        self.setToolTip(path)
        lay = QHBoxLayout(self)
        lay.setContentsMargins(30, 0, 8, 0)
        lay.setSpacing(8)
        title = ElidedLabel(name, self)
        font = QFont(title.font())
        font.setPixelSize(12)
        title.setFont(font)
        lay.addWidget(title, 1)
        if badge:
            lay.addWidget(_make_chip(badge, strong=True))
        if mark:
            mark_label = QLabel(mark)
            mark_label.setStyleSheet("color:%s;font-size:11px;" % DESIGN["muted2"])
            lay.addWidget(mark_label)
        size_label = QLabel(size_text)
        size_label.setFixedWidth(74)
        size_label.setAlignment(Qt.AlignRight | Qt.AlignVCenter)
        size_label.setStyleSheet("color:%s;font-size:11px;" % DESIGN["muted"])
        lay.addWidget(size_label)
        play_btn = QPushButton("播放")
        play_btn.setProperty("guoRole", "ctrlBtn")
        play_btn.setFixedSize(48, 22)
        play_btn.clicked.connect(
            lambda checked=False, target=path: self.playRequested.emit(target))
        lay.addWidget(play_btn)
        del_btn = QPushButton("删除")
        del_btn.setProperty("guoRole", "ctrlBtn")
        del_btn.setFixedSize(48, 22)
        del_btn.setToolTip("删除该文件（加密缓存保留，可重新解密）")
        del_btn.clicked.connect(
            lambda checked=False, target=path: self.deleteRequested.emit(target))
        lay.addWidget(del_btn)

    def enterEvent(self, event):
        if not self._hover:
            self._hover = True
            self.update()
        super().enterEvent(event)

    def leaveEvent(self, event):
        if self._hover:
            self._hover = False
            self.update()
        super().leaveEvent(event)

    def paintEvent(self, event):
        if self._hover:
            painter = QPainter(self)
            painter.setRenderHint(QPainter.Antialiasing, True)
            painter.setPen(Qt.NoPen)
            painter.setBrush(QColor(DESIGN["guo_chip_hover"]))
            painter.drawRoundedRect(self.rect().adjusted(2, 0, -2, 0), 6, 6)
            painter.end()
        super().paintEvent(event)

    def mousePressEvent(self, event):
        if event.button() == Qt.LeftButton:
            self.playRequested.emit(self._path)
        super().mousePressEvent(event)


class _ArchiveGroupHeader(QWidget):
    """组头：折叠箭头 + 剧名 + 集数/占用 chip + 打开目录。点任意空白处折叠。"""

    toggled = pyqtSignal(bool)         # 参数是新状态：是否折叠
    openFolder = pyqtSignal(str)

    def __init__(self, title, folder, chips, collapsed=False, parent=None):
        super().__init__(parent)
        self._collapsed = collapsed
        self.setFixedHeight(36)
        self.setCursor(Qt.PointingHandCursor)
        lay = QHBoxLayout(self)
        lay.setContentsMargins(10, 0, 8, 0)
        lay.setSpacing(8)
        self._chevron = QLabel("▸" if collapsed else "▾")
        self._chevron.setFixedWidth(16)
        self._chevron.setAlignment(Qt.AlignCenter)
        self._chevron.setStyleSheet("color:%s;" % DESIGN["guo_muted"])
        lay.addWidget(self._chevron)
        name = ElidedLabel(title, self)
        font = QFont(name.font())
        font.setPixelSize(13)
        font.setBold(True)
        name.setFont(font)
        name.setStyleSheet("color:%s;" % DESIGN["guo_text"])
        self._title = name
        lay.addWidget(name, 1)
        for text, strong in chips:
            lay.addWidget(_make_chip(text, strong=strong))
        btn = QPushButton("打开")
        btn.setProperty("guoRole", "ctrlBtn")
        btn.setFixedSize(52, 24)
        btn.clicked.connect(lambda checked=False: self.openFolder.emit(folder))
        lay.addWidget(btn)

    def mousePressEvent(self, event):
        if event.button() == Qt.LeftButton:
            self._collapsed = not self._collapsed
            self._chevron.setText("▸" if self._collapsed else "▾")
            self.toggled.emit(self._collapsed)
        super().mousePressEvent(event)


class _ArchiveGroup(QWidget):
    """一个剧的归档组：组头 + 可折叠的文件行列表。"""

    playRequested = pyqtSignal(str)
    deleteRequested = pyqtSignal(str)
    openFolderRequested = pyqtSignal(str)

    def __init__(self, title, folder, chips, entries, collapsed=False, parent=None):
        super().__init__(parent)
        lay = QVBoxLayout(self)
        lay.setContentsMargins(0, 0, 0, 0)
        lay.setSpacing(1)
        self.header = _ArchiveGroupHeader(title, folder, chips, collapsed, self)
        self.header.openFolder.connect(self.openFolderRequested.emit)
        lay.addWidget(self.header)
        self.body = QWidget(self)
        body_lay = QVBoxLayout(self.body)
        body_lay.setContentsMargins(0, 0, 0, 4)
        body_lay.setSpacing(0)
        for path, name, size_text, badge, mark in entries:
            row = _ArchiveFileRow(path, name, size_text, badge, mark, self.body)
            row.playRequested.connect(self.playRequested.emit)
            row.deleteRequested.connect(self.deleteRequested.emit)
            body_lay.addWidget(row)
        self.body.setVisible(not collapsed)
        self.header.toggled.connect(self.body.setVisible)
        lay.addWidget(self.body)


class _ArchiveView(QWidget):
    """已完成归档（D1a）：分集成品按剧名分组，组头带集数/占用，可折叠。

    取代旧的「合并成品」平铺列表：guoapp 的归档形态是「下载目录/剧名/第N集.mp4」，
    合并产物只是同剧名下的额外一条，所以按剧归组，而不是按目录。
    """

    playRequested = pyqtSignal(str)
    deleteRequested = pyqtSignal(str)
    openFolderRequested = pyqtSignal(str)

    def __init__(self, parent=None):
        super().__init__(parent)
        self.entry_count = 0
        self.collapsed_titles = set()
        self.scroll = QScrollArea(self)
        self.scroll.setProperty("guoRole", "bare")
        self.scroll.setWidgetResizable(True)
        self.scroll.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self._host = QWidget()
        # 归档页在 QTabWidget 的裸 QWidget 页里，全局 QSS 不铺背景；
        # 自带一层 surface 底色，组头/行文字才有对比度。
        self._host.setObjectName("archiveHost")
        self.scroll.viewport().setObjectName("archiveViewport")
        self.scroll.setStyleSheet(
            "QScrollArea{border:none;background:%s;}"
            "QWidget#archiveViewport,QWidget#archiveHost{background:%s;}"
            % (DESIGN["surface"], DESIGN["surface"]))
        self._lay = QVBoxLayout(self._host)
        self._lay.setContentsMargins(2, 4, 8, 6)
        self._lay.setSpacing(3)
        self._placeholder = QLabel("还没有成品：解密后的分集与合并成品会按剧名归档到这里。")
        self._placeholder.setStyleSheet("color:%s;padding:12px;" % DESIGN["muted"])
        self._lay.addWidget(self._placeholder)
        self._lay.addStretch(1)
        self.scroll.setWidget(self._host)
        outer = QVBoxLayout(self)
        outer.setContentsMargins(0, 0, 0, 0)
        outer.addWidget(self.scroll)

    def _clear(self):
        while self._lay.count():
            item = self._lay.takeAt(0)
            widget = item.widget()
            if widget is not None and widget is not self._placeholder:
                widget.deleteLater()
        if self._placeholder.parentWidget() is not None:
            self._placeholder.setParent(None)

    def set_groups(self, groups):
        """groups: [(title, folder, [(text, strong)], entries)]；
        entries: [(path, name, size_text, badge, mark)]，组内已排好序。"""
        self._clear()
        self.entry_count = sum(len(entries) for _t, _f, _c, entries in groups)
        if not groups:
            self._lay.addWidget(self._placeholder)
            self._placeholder.show()
        for title, folder, chips, entries in groups:
            collapsed = title in self.collapsed_titles
            group = _ArchiveGroup(title, folder, chips, entries, collapsed, self._host)
            group.playRequested.connect(self.playRequested.emit)
            group.deleteRequested.connect(self.deleteRequested.emit)
            group.openFolderRequested.connect(self.openFolderRequested.emit)
            # 折叠状态记在视图上：signature 变化重建后仍然保持。
            group.header.toggled.connect(
                lambda is_collapsed, key=title:
                self.collapsed_titles.add(key) if is_collapsed
                else self.collapsed_titles.discard(key))
            self._lay.addWidget(group)
        self._lay.addStretch(1)


class _StatBar(QWidget):
    """顶部统计条（D1 目标3）：状态计数 chip，点击即筛选下载队列；右侧站源筛选。"""

    filterChanged = pyqtSignal(str)     # "" / active / paused / completed / failed
    sourceChanged = pyqtSignal(str)     # "" / slug

    GROUPS = (
        ("", "全部", ("queued", "downloading", "paused", "completed", "failed")),
        ("active", "进行中", ("queued", "downloading")),
        ("paused", "已暂停", ("paused",)),
        ("completed", "已完成", ("completed",)),
        ("failed", "失败", ("failed",)),
    )

    def __init__(self, parent=None):
        super().__init__(parent)
        self._key = ""
        lay = QHBoxLayout(self)
        lay.setContentsMargins(2, 0, 2, 0)
        lay.setSpacing(6)
        self._buttons = {}
        for key, text, _states in self.GROUPS:
            btn = QPushButton(text)
            btn.setProperty("guoRole", "groupTab")
            btn.setCheckable(True)
            btn.setCursor(Qt.PointingHandCursor)
            btn.clicked.connect(lambda checked=False, k=key: self._choose(k))
            lay.addWidget(btn)
            self._buttons[key] = btn
        lay.addStretch(1)
        tip = QLabel("站源")
        tip.setStyleSheet("color:%s;" % DESIGN["guo_muted"])
        lay.addWidget(tip)
        self.source_combo = QComboBox(self)
        self.source_combo.setMinimumWidth(112)
        self.source_combo.currentIndexChanged.connect(self._source_changed)
        lay.addWidget(self.source_combo)
        self._sync_buttons()

    def _choose(self, key):
        # 再点一次已选中的筛选项 = 取消筛选（回到「全部」）。
        self._key = "" if key == "" or key == self._key else key
        self._sync_buttons()
        self.filterChanged.emit(self._key)

    def _sync_buttons(self):
        for k, btn in self._buttons.items():
            btn.setChecked(k == self._key)

    def update_counts(self, jobs):
        counts = {key: 0 for key, _text, _states in self.GROUPS}
        for job in jobs:
            state = job.get("state") or ""
            for key, _text, states in self.GROUPS:
                if state in states:
                    counts[key] += 1
        for key, text, _states in self.GROUPS:
            self._buttons[key].setText("%s %d" % (text, counts.get(key, 0)))

    def set_sources(self, slugs):
        """按任务里出现过的站源刷新下拉；保留当前选择（若仍存在）。"""
        kept = self.source_combo.currentData()
        self.source_combo.blockSignals(True)
        self.source_combo.clear()
        self.source_combo.addItem("全部站源", "")
        for slug in slugs:
            self.source_combo.addItem(_source_label(slug) or slug, slug)
        index = 0
        for i in range(self.source_combo.count()):
            if (self.source_combo.itemData(i) or "") == (kept or ""):
                index = i
                break
        self.source_combo.setCurrentIndex(index)
        self.source_combo.blockSignals(False)

    def current_source(self):
        return self.source_combo.currentData() or ""

    def _source_changed(self, _index):
        self.sourceChanged.emit(self.current_source())


# 状态筛选表：_StatBar chip key → 任务状态集合（"" 不筛选）。
_STATE_FILTER = {key: states for key, _text, states in _StatBar.GROUPS if key}


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
        # 统计条/站源筛选（D1）："" 表示不筛选。
        self._state_filter = ""
        self._source_filter = ""
        self._source_sig = None
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

        # 已完成归档（D1a）：分集成品按剧名分组，取代旧的「合并成品」平铺列表。
        self.archive = _ArchiveView()
        self.archive.playRequested.connect(self._play_merged_path)
        self.archive.deleteRequested.connect(self._delete_archive_file)
        self.archive.openFolderRequested.connect(self._open_path)
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
        # tab 页是裸 QWidget，全局 QSS 不铺背景；补一层 surface 免得露出浅色底。
        files_page.setObjectName("guoTabPage")
        files_page.setAttribute(Qt.WA_StyledBackground, True)
        files_page.setStyleSheet("QWidget#guoTabPage{background:%s;}" % DESIGN["surface"])
        files_layout = QVBoxLayout(files_page)
        files_layout.setContentsMargins(4, 6, 4, 4)
        files_layout.addWidget(self.archive)

        self.merge_tabs = QTabWidget()
        self.merge_tabs.setProperty("role", "compact")
        self.merge_tabs.addTab(queue_page, "合并队列")
        self.merge_tabs.addTab(files_page, "已完成归档")
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
        # 顶部统计条（D1 目标3）：状态计数 chip，点击即筛选；右侧站源筛选（D1c）。
        self.stat_bar = _StatBar()
        self.stat_bar.filterChanged.connect(self._on_stat_filter)
        self.stat_bar.sourceChanged.connect(self._on_source_filter)
        queue_tab = QWidget()
        queue_tab.setObjectName("guoTabPage")
        queue_tab.setAttribute(Qt.WA_StyledBackground, True)
        queue_tab.setStyleSheet("QWidget#guoTabPage{background:%s;}" % DESIGN["surface"])
        queue_tab_layout = QVBoxLayout(queue_tab)
        queue_tab_layout.setContentsMargins(4, 6, 4, 4)
        queue_tab_layout.setSpacing(6)
        queue_tab_layout.addWidget(self.stat_bar)
        queue_tab_layout.addWidget(self.queue, 1)
        self.merge_tabs.insertTab(0, queue_tab, "下载队列")
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
        total = getattr(self.archive, "entry_count", 0)
        tabs.setTabText(2, "已完成归档 (%d)" % total if total else "已完成归档")

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

    # ----------------------------------------------------- archived outputs
    def _refresh_merge_files(self):
        """扫描成品目录，按剧名分组刷「已完成归档」页（signature 守卫）。

        两类来源：
        1) 下载根目录下的剧名子目录里的分集 mp4 —— guoapp 归档形态
           「下载/剧名/第N集.mp4」；
        2) 「合并」目录里的整集成品 —— 按文件名解析剧名归回同一组。
        旧位置的文件同样列出（标盘符/旧位置），与旧「合并成品」列表语义一致。
        """
        current_root = tools.download_dir()
        current_merge = tools.merge_dir()
        try:
            roots = list(tools.download_dirs()) or [current_root]
        except Exception:
            roots = [current_root]
        try:
            merge_folders = list(tools.merge_dirs())
        except Exception:
            merge_folders = []
        # 当前根排最前：组文件夹优先指向还在用的位置，「打开目录」才不会跳旧盘。
        roots.sort(key=lambda r: os.path.normpath(r) != os.path.normpath(current_root))
        merge_folders.sort(key=lambda r: os.path.normpath(r) != os.path.normpath(current_merge))

        def elsewhere_mark(base_dir):
            norm = os.path.normpath(base_dir)
            if norm in (os.path.normpath(current_root), os.path.normpath(current_merge)):
                return ""
            drive = os.path.splitdrive(norm)[0]
            cur_drive = os.path.splitdrive(current_root)[0]
            return drive if drive and drive != cur_drive else "旧位置"

        groups = {}

        def bucket(key):
            return groups.setdefault(key, {"folder": "", "episodes": [], "merged": []})

        def folder_for(key, path, mark):
            entry = bucket(key)
            if not entry["folder"]:
                entry["folder"] = path
            return entry

        for root in roots:
            if not os.path.isdir(root):
                continue
            mark = elsewhere_mark(root)
            try:
                names = sorted(os.listdir(root))
            except OSError:
                continue
            for name in names:
                sub = os.path.join(root, name)
                if not os.path.isdir(sub) or name == "合并":
                    continue
                try:
                    files = sorted(os.listdir(sub))
                except OSError:
                    continue
                for fname in files:
                    fpath = os.path.join(sub, fname)
                    if not fname.lower().endswith(".mp4") or not os.path.isfile(fpath):
                        continue
                    try:
                        size = os.path.getsize(fpath)
                        mtime = os.path.getmtime(fpath)
                    except OSError:
                        continue
                    key = strip_season(name) or name
                    entry = folder_for(key, sub, mark)
                    entry["episodes"].append((fpath, fname, size, mtime, mark))

        for folder in merge_folders:
            if not os.path.isdir(folder):
                continue
            mark = elsewhere_mark(folder)
            try:
                names = sorted(os.listdir(folder))
            except OSError:
                continue
            for fname in names:
                fpath = os.path.join(folder, fname)
                if not fname.lower().endswith((".mkv", ".mp4")) or not os.path.isfile(fpath):
                    continue
                try:
                    size = os.path.getsize(fpath)
                    mtime = os.path.getmtime(fpath)
                except OSError:
                    continue
                key = _archive_drama_key(fname)
                entry = folder_for(key, folder, mark)
                entry["merged"].append((fpath, fname, size, mtime, mark))

        sig_rows = []
        for key, data in groups.items():
            for kind in ("episodes", "merged"):
                for _p, fname, size, _m, mark in data[kind]:
                    sig_rows.append((key, fname, size, mark))
        signature = tuple(sorted(sig_rows))
        if signature == self._merge_files_sig:
            return
        self._merge_files_sig = signature

        # clear 会把滚动条打回顶部：删最下面一条时视图会跳到第一组。
        # 记下位置，重建完再放回去（内容变短时 Qt 会自动夹到新的最大值）。
        scroll = self.archive.scroll.verticalScrollBar().value()
        built = []
        for key in sorted(groups):
            data = groups[key]
            episodes = sorted(data["episodes"],
                              key=lambda e: (_episode_sort_key(e[1]), -e[3], e[1]))
            merged = sorted(data["merged"], key=lambda e: e[1])
            entries = []
            for path, fname, size, _mtime, mark in merged:
                entries.append((path, fname, human_size(size), "整集合并", mark))
            for path, fname, size, _mtime, mark in episodes:
                entries.append((path, fname, human_size(size), "", mark))
            total = (sum(e[2] for e in data["episodes"])
                     + sum(e[2] for e in data["merged"]))
            chips = []
            if data["episodes"]:
                chips.append(("%d 集" % len(data["episodes"]), False))
            if total:
                chips.append((human_size(total), False))
            if data["merged"]:
                chips.append(("含整集合并", True))
            built.append((key, data["folder"], chips, entries))
        self.archive.set_groups(built)
        self.archive.scroll.verticalScrollBar().setValue(scroll)
        self._update_merge_tabs()

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
        jobs = list(self._jobs)
        if self._filter_drama is not None:
            jobs = [job for job in jobs
                    if (job.get("drama") or {}).get("id") == self._filter_drama]
        if self._source_filter:
            jobs = [job for job in jobs
                    if str((job.get("drama") or {}).get("source") or "") == self._source_filter]
        if self._state_filter:
            wanted = _STATE_FILTER.get(self._state_filter, ())
            jobs = [job for job in jobs if job.get("state") in wanted]
        return jobs

    def _on_stat_filter(self, key):
        if key == self._state_filter:
            return
        self._state_filter = key
        self._row_sig.clear()
        self.queue.clear()
        self._render()

    def _on_source_filter(self, slug):
        if slug == self._source_filter:
            return
        self._source_filter = slug
        self._row_sig.clear()
        self.queue.clear()
        self._render()

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
        self.stat_bar.update_counts(self._jobs)
        source_sig = tuple(sorted({str((job.get("drama") or {}).get("source") or "")
                                   for job in self._jobs if (job.get("drama") or {}).get("source")}))
        if source_sig != self._source_sig:
            self._source_sig = source_sig
            self.stat_bar.set_sources(source_sig)
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
            drama = job.get("drama") or {}
            drama_title = drama.get("title") or ""
            source_label = _source_label(drama.get("source"))
            signature = (job.get("id"), state, round(progress, 4), done, total,
                         round(speed, 1), label, quality, drama_title, source_label)
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
                item.setData(ROLE_JOB_DRAMA, (drama_title, source_label))
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
        source = _source_label((job.get("drama") or {}).get("source"))
        if source:
            lines.append("站源：%s" % source)
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
        """队列卡片行尾图标：暂停 / 继续 / 播放 / 重试 / 打开目录 / 删除。"""
        jobs = self._visible_jobs()
        if not 0 <= row < len(jobs):
            return
        job = jobs[row]
        if key == "play":
            self._play_job(job)
        elif key == "folder":
            self._open_job_folder(job)
        elif key == "retry":
            # 核心没有独立的 retry 命令，resume 就是「重新拉起这条任务」；
            # 真正的 retry 语义留给 V1 回归时确认核心是否支持。
            self._control(job, "resume")
        else:
            self._control(job, key)

    def _play_row(self, item):
        """双击一行：已完成的直接播放（别的状态双击不做事）。"""
        row = self.queue.row(item)
        jobs = self._visible_jobs()
        if 0 <= row < len(jobs) and jobs[row].get("state") == "completed":
            self._play_job(jobs[row])

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
        self._open_path(tools.download_dir())

    def _open_job_folder(self, job):
        """打开这一集成品所在目录（D1b）：优先 publisher 已发布路径，
        未发布时按「下载根目录/剧名」推导。"""
        try:
            published = self.publisher.path_for(job)
        except Exception:
            published = None
        if published:
            self._open_path(os.path.dirname(published))
            return
        title = (job.get("drama") or {}).get("title") or ""
        if title:
            self._open_path(os.path.join(tools.download_dir(), decrypt.series_folder(title)))

    def _open_path(self, path):
        if not path:
            return
        try:
            os.makedirs(path, exist_ok=True)
        except OSError as exc:
            self.statusMessage.emit("打开目录失败：%s" % exc)
            return
        os.startfile(path) if os.name == "nt" else None
        self.statusMessage.emit("已打开目录：%s" % path)

    def _delete_archive_file(self, path):
        """归档行删除：合并成品沿用旧确认文案；分集文件删掉后核心索引
        自愈（known() 判文件不存在，需要时自动重新解密落盘）。"""
        merge_folders = set()
        try:
            merge_folders.add(os.path.normpath(tools.merge_dir()))
            merge_folders.update(os.path.normpath(f) for f in tools.merge_dirs())
        except Exception:
            pass
        if os.path.normpath(os.path.dirname(path)) in merge_folders:
            self._delete_merged_file(path)
            return
        if not _confirm(self, "删除分集",
                        "确定删除「%s」吗？\n加密缓存保留，需要时可自动重新解密。"
                        % os.path.basename(path), yes="删除"):
            return
        try:
            os.remove(path)
        except OSError as exc:
            self.statusMessage.emit("删除失败：%s" % exc)
            return
        self._merge_files_sig = None
        self._refresh_merge_files()
        self.statusMessage.emit("已删除分集：%s" % os.path.basename(path))

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

