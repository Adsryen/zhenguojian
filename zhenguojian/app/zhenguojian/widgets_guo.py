# -*- coding: utf-8 -*-
"""F2 guoapp 风格公共控件库（多面板复用）。

约定：
- 颜色/圆角等视觉值全部来自 ``ui.DESIGN`` 的 ``guo_`` 派生 token，配合
  ``ui.build_stylesheet()`` 里的 ``[guoRole="..."]`` 样式段；本文件不出现
  主题色字面值（几何尺寸常量除外）。
- 可交互件的四态（normal/hover/pressed/disabled）由全局 QSS 处理；需要按
  动态属性切换的状态（hover/pressed/selected/current/watched）通过
  setProperty + unpolish/polish 触发。
- 使用前必须已 ``app.setStyleSheet(ui.build_stylesheet())``（主程序本来就会）。

控件一览（公开 API）：
- PosterCard:   set_data(title, cover, badge_rank=None, update_badge=None, vip=False)
                clicked 信号 / set_selected(bool) 已看置灰 / set_image_loader(callable)
- BlurCoverBackground: set_cover(pixmap)
- EpisodeGrid:  set_episodes(list[dict{index,title,watched,vip,current}])
                episode_clicked(int) 信号 / scroll_to_current() / select_group(i)
- SourceSwitchPopup: set_sources(list[dict{slug,name,enabled?}]) / set_current(slug)
                source_selected(slug) / source_toggled(slug, bool) / build_menu() / open_menu()
- SectionedListView: set_sections(list[(title, items_producer)]) / rebuild()
"""
from __future__ import annotations

from functools import partial

from PyQt5.QtCore import (QEvent, QPoint, QRect, QRectF, QSize, Qt,
                          QPropertyAnimation, pyqtSignal)
from PyQt5.QtGui import (QColor, QFont, QFontMetrics, QLinearGradient,
                         QPainter, QPainterPath, QPen, QPixmap)
from PyQt5.QtWidgets import (QCheckBox, QHBoxLayout, QLabel, QMenu,
                             QPushButton, QScrollArea, QSizePolicy,
                             QStyle, QStyleOptionButton, QVBoxLayout,
                             QWidget, QWidgetAction)

from . import ui
from .flow_layout import FlowLayout

DESIGN = ui.DESIGN  # 始终指向当前生效调色板（ui.apply_theme 就地改写它）

# --------------------------------------------------------------- 小工具
def _px(value, fallback):
    """'12px' -> 12；token 缺失或异常时回退几何值。"""
    try:
        return int(str(value).replace("px", "").strip())
    except (TypeError, ValueError):
        return fallback


def _repolish(widget):
    style = widget.style()
    style.unpolish(widget)
    style.polish(widget)


def _set_prop(widget, name, value):
    """动态属性驱动 QSS 选择器：value=None 表示移除该属性。"""
    if value is None:
        widget.setProperty(name, None)
    else:
        widget.setProperty(name, "true" if value is True else (
            "false" if value is False else str(value)))
    _repolish(widget)


def _wrap_two_lines(text, fm, width, max_lines=2):
    """中文友好按字符贪心换行，超过 max_lines 行时末行加省略号。"""
    text = (text or "").strip()
    if not text or width <= 0:
        return [text] if text else []
    lines, cur = [], ""
    for ch in text:
        if ch == "\n":
            lines.append(cur)
            cur = ""
            continue
        if not cur or fm.horizontalAdvance(cur + ch) <= width:
            cur += ch
        else:
            lines.append(cur)
            cur = ch
    if cur:
        lines.append(cur)
    if len(lines) > max_lines:
        lines = lines[:max_lines]
        last = lines[-1]
        while last and fm.horizontalAdvance(last + "…") > width:
            last = last[:-1]
        lines[-1] = last + "…"
    return lines


# ================================================================ 海报卡
class _CoverArea(QWidget):
    """PosterCard 内部：圆角封面 + 底部渐变遮罩 + 两行省略标题（全自绘）。

    对鼠标透明——点击/悬停事件由外层 PosterCard 统一处理。
    """

    def __init__(self, parent=None):
        super().__init__(parent)
        self.setAttribute(Qt.WA_TransparentForMouseEvents, True)
        self.setAttribute(Qt.WA_NoSystemBackground, True)
        self._pixmap = None
        self._title = ""

    def set_pixmap(self, pixmap):
        self._pixmap = pixmap
        self.update()

    def set_title(self, title):
        self._title = title or ""
        self.update()

    def paintEvent(self, event):
        painter = QPainter(self)
        try:
            painter.setRenderHint(QPainter.Antialiasing, True)
            painter.setRenderHint(QPainter.SmoothPixmapTransform, True)
            rect = QRect(QPoint(0, 0), self.size())
            radius = max(2, _px(DESIGN.get("guo_radius_lg"), 12) - 1)

            path = QPainterPath()
            path.addRoundedRect(QRectF(rect), radius, radius)
            painter.setClipPath(path)

            painter.fillRect(rect, QColor(DESIGN["cover_bg"]))
            pm = self._pixmap
            if pm is not None and not pm.isNull():
                scaled = pm.scaled(rect.size(), Qt.KeepAspectRatioByExpanding,
                                   Qt.SmoothTransformation)
                left = max(0, (scaled.width() - rect.width()) // 2)
                top = max(0, (scaled.height() - rect.height()) // 2)
                painter.drawPixmap(rect.x() - left, rect.y() - top, scaled)

            # 底部渐变遮罩 + 标题（两行省略）
            pad = 8
            start_y = int(rect.height() * 0.45)
            gradient = QLinearGradient(0, start_y, 0, rect.height())
            gradient.setColorAt(0.0, QColor(DESIGN["guo_scrim_top"]))
            gradient.setColorAt(1.0, QColor(DESIGN["guo_scrim_bottom"]))
            painter.fillRect(QRect(0, start_y, rect.width(),
                                   rect.height() - start_y), gradient)

            if self._title:
                font = QFont(painter.font())
                font.setPixelSize(12)
                font.setBold(True)
                painter.setFont(font)
                fm = QFontMetrics(font)
                line_h = fm.height()
                lines = _wrap_two_lines(self._title, fm, rect.width() - pad * 2)
                text_top = (rect.bottom() - pad - line_h * len(lines)
                            + fm.ascent())
                painter.setPen(QPen(QColor(DESIGN["guo_text"])))
                for i, line in enumerate(lines):
                    painter.drawText(pad, text_top + i * line_h, line)
        finally:
            # 异常也必关 painter，否则 QPaintDevice 泄漏直接 SIGSEGV
            painter.end()


class PosterCard(QWidget):
    """竖版海报卡：悬停上浮+边框高亮，右上名次/更新角标，左上 VIP chip。

    图片加载：``cover`` 传 QPixmap 直接显示；传 str(url) 时先显示占位色块，
    再调用注入的 ``image_loader(url, callback)``；callback(pixmap_or_None) 需
    在 GUI 线程调用。不传 loader 则保持占位。
    """

    clicked = pyqtSignal()

    def __init__(self, parent=None, cover_size=(100, 140), image_loader=None):
        super().__init__(parent)
        self._cover_size = tuple(cover_size)
        self._image_loader = image_loader
        self._hover = False
        self._pressed = False
        self._selected = False
        self._lift_base = None  # 悬停上浮时的还原锚点

        self.setProperty("guoRole", "posterCard")
        self.setAttribute(Qt.WA_StyledBackground, True)
        self.setCursor(Qt.PointingHandCursor)
        # 固定 = 封面 + 1px 边框：否则会被布局拉变形
        self.setFixedSize(self._cover_size[0] + 2, self._cover_size[1] + 2)

        self._cover_area = _CoverArea(self)

        self._rank = QLabel(self)
        self._rank.setProperty("guoRole", "rankBadge")
        self._rank.hide()
        self._update_badge = QLabel(self)
        self._update_badge.setProperty("guoRole", "updateBadge")
        self._update_badge.hide()
        self._vip = QLabel("VIP", self)
        self._vip.setProperty("guoRole", "vipBadge")
        self._vip.hide()

        self._shadow = None
        self._shadow_anim = None
        self._apply_effect()

    # ------------------------------------------------------------ 公开 API
    def set_data(self, title, cover=None, badge_rank=None, update_badge=None,
                 vip=False):
        """title: 标题；cover: QPixmap | url(str) | None(占位)。"""
        self._cover_area.set_title(title)
        if isinstance(cover, QPixmap):
            self._cover_area.set_pixmap(cover)
        elif isinstance(cover, str) and cover:
            self._cover_area.set_pixmap(None)
            loader = self._image_loader
            if callable(loader):
                loader(cover, self._on_loaded)
        else:
            self._cover_area.set_pixmap(None)

        if badge_rank is not None:
            self._rank.setText(str(badge_rank))
            _set_prop(self._rank, "rank", int(badge_rank)
                      if str(badge_rank).isdigit() else None)
            self._rank.adjustSize()
            self._rank.show()
        else:
            self._rank.hide()

        if update_badge is not None:
            if isinstance(update_badge, int):
                self._update_badge.setText("更新到%d集" % update_badge)
            else:
                self._update_badge.setText(str(update_badge))
            self._update_badge.adjustSize()
            self._update_badge.show()
        else:
            self._update_badge.hide()

        self._vip.setVisible(bool(vip))
        self._place_badges()
        self._cover_area.update()

    def set_image_loader(self, loader):
        """callable(url, callback)：网络取图由调用方注入。"""
        self._image_loader = loader

    def set_selected(self, selected):
        """已看态：整卡置灰至 45% 亮度。"""
        self._selected = bool(selected)
        _set_prop(self, "selected", self._selected)
        self._apply_effect()

    def is_selected(self):
        return self._selected

    def preview_state(self, hover=None, pressed=None):
        """测试/预览钩子：直接落到某态视觉（gallery 截图用），None 不改。"""
        if hover is not None:
            self._set_hover(bool(hover), animate=False)
        if pressed is not None:
            self._set_pressed(bool(pressed))

    # ------------------------------------------------------------ 内部
    def _on_loaded(self, pixmap):
        try:
            if pixmap is not None and not pixmap.isNull():
                self._cover_area.set_pixmap(pixmap)
        except RuntimeError:
            pass  # 控件已被销毁

    def _apply_effect(self):
        if self._shadow_anim is not None:
            self._shadow_anim.stop()
            self._shadow_anim = None
        if not self.isEnabled():
            from PyQt5.QtWidgets import QGraphicsOpacityEffect
            effect = QGraphicsOpacityEffect(self)
            effect.setOpacity(0.4)
            self._shadow = None
        elif self._selected:
            # 已看态：整体 45% 亮度。QGraphicsColorizeEffect 没有 setSaturation
            # （PyQt5 会 AttributeError），且一个 widget 只能挂一个 graphicsEffect，
            # 所以这里用透明度近似置灰（深底页面上效果等同压暗）。
            from PyQt5.QtWidgets import QGraphicsOpacityEffect
            effect = QGraphicsOpacityEffect(self)
            effect.setOpacity(0.45)
            self._shadow = None
        else:
            from PyQt5.QtWidgets import QGraphicsDropShadowEffect
            effect = QGraphicsDropShadowEffect(self)
            effect.setColor(QColor(DESIGN["guo_shadow"]))
            effect.setOffset(0, 6)
            effect.setBlurRadius(self._shadow_blur())
            self._shadow = effect
        self.setGraphicsEffect(effect)

    def _shadow_blur(self):
        return _px(DESIGN.get("guo_shadow_blur"), 24)

    def _set_hover(self, value, animate=True):
        if value == self._hover:
            return
        self._hover = value
        _set_prop(self, "hover", value)
        target = 0 if not value else self._shadow_blur()
        if self._shadow is not None:
            if animate:
                self._shadow_anim = QPropertyAnimation(self._shadow, b"blurRadius", self)
                self._shadow_anim.setDuration(140)
                self._shadow_anim.setStartValue(self._shadow.blurRadius())
                self._shadow_anim.setEndValue(target)
                self._shadow_anim.start()
            else:
                self._shadow.setBlurRadius(target)
        # 上浮 2px（leave 时还原），布局重排会自动归位
        if self._selected:
            return
        if value:
            if self._lift_base is None:
                self._lift_base = self.pos()
            self.move(self._lift_base.x(), self._lift_base.y() - 2)
        elif self._lift_base is not None:
            self.move(self._lift_base)
            self._lift_base = None

    def _set_pressed(self, value):
        if value == self._pressed:
            return
        self._pressed = value
        _set_prop(self, "pressed", value)

    # ------------------------------------------------------------ 事件
    def sizeHint(self):
        return QSize(*self._cover_size)

    def enterEvent(self, event):
        if self.isEnabled():
            self._set_hover(True)
        super().enterEvent(event)

    def leaveEvent(self, event):
        self._set_hover(False)
        self._set_pressed(False)
        super().leaveEvent(event)

    def mousePressEvent(self, event):
        if self.isEnabled() and event.button() == Qt.LeftButton:
            self._set_pressed(True)
        super().mousePressEvent(event)

    def mouseReleaseEvent(self, event):
        was = self._pressed
        self._set_pressed(False)
        if (was and self.isEnabled() and event.button() == Qt.LeftButton
                and self.rect().contains(event.pos())):
            self.clicked.emit()
        super().mouseReleaseEvent(event)

    def changeEvent(self, event):
        if event.type() == QEvent.EnabledChange:
            if not self.isEnabled():
                self._set_hover(False, animate=False)
                self._set_pressed(False)
            self._apply_effect()
        super().changeEvent(event)

    def resizeEvent(self, event):
        self._cover_area.setGeometry(self.rect().adjusted(1, 1, -1, -1))
        self._place_badges()
        super().resizeEvent(event)

    def showEvent(self, event):
        super().showEvent(event)
        self._place_badges()

    def _place_badges(self):
        # 注意用 isHidden()（自身标志）而非 isVisible()：父链未 show 时
        # isVisible() 恒为 False，会导致角标全部停在 (0,0)。
        m = 8
        w = self.width()
        y = m
        if not self._rank.isHidden():
            self._rank.adjustSize()
            self._rank.move(w - m - self._rank.width(), y)
            y += self._rank.height() + 4
        if not self._update_badge.isHidden():
            self._update_badge.adjustSize()
            self._update_badge.move(w - m - self._update_badge.width(), y)
            y += self._update_badge.height() + 4
        if not self._vip.isHidden():
            self._vip.adjustSize()
            self._vip.move(w - m - self._vip.width(), y)
        for badge in (self._rank, self._update_badge, self._vip):
            badge.raise_()


# ============================================================ 模糊封面背景
class BlurCoverBackground(QWidget):
    """把封面 8x 降采样再平滑放大做近似高斯模糊，铺满自身并叠 60% 黑遮罩。

    用法：置于兄弟控件之下（先加入布局或调用 lower()），几何由调用方负责
    （通常随父窗口 resizeEvent 同步）。
    """

    DOWNSCALE = 8

    def __init__(self, parent=None):
        super().__init__(parent)
        self.setAttribute(Qt.WA_NoSystemBackground, True)
        self._source = None
        self._cache = None       # (w, h) -> 模糊好的 QPixmap
        self._cache_key = None

    def set_cover(self, pixmap):
        self._source = pixmap if (pixmap is not None and not pixmap.isNull()) else None
        self._cache_key = None
        self.update()

    def _blurred(self, width, height):
        key = (width, height)
        if self._cache_key == key and self._cache is not None:
            return self._cache
        if self._source is None or width <= 0 or height <= 0:
            self._cache_key, self._cache = key, None
            return None
        small = self._source.scaled(
            max(2, width // self.DOWNSCALE), max(2, height // self.DOWNSCALE),
            Qt.IgnoreAspectRatio, Qt.SmoothTransformation)
        big = small.scaled(width, height, Qt.IgnoreAspectRatio,
                           Qt.SmoothTransformation)
        self._cache_key, self._cache = key, big
        return big

    def resizeEvent(self, event):
        self._cache_key = None  # 尺寸变了要重算
        super().resizeEvent(event)

    def paintEvent(self, event):
        painter = QPainter(self)
        try:
            pm = self._blurred(self.width(), self.height())
            if pm is not None:
                painter.drawPixmap(0, 0, pm)
            else:
                painter.fillRect(self.rect(), QColor(DESIGN["bg"]))
            painter.fillRect(self.rect(), QColor(DESIGN["guo_blur_scrim"]))
        finally:
            painter.end()


# ================================================================ 选集网格
class _FlowHost(QWidget):
    """Flow 网格宿主：minimumHeight 跟随 heightForWidth，让外层 QScrollArea 出竖向滚动条。"""

    def __init__(self, layout, parent=None):
        super().__init__(parent)
        self._flow = layout
        self.setSizePolicy(QSizePolicy.Preferred, QSizePolicy.Minimum)

    def _sync_height(self):
        h = self._flow.heightForWidth(self.width())
        self.setMinimumHeight(max(h, 0))

    def resizeEvent(self, event):
        self._sync_height()
        super().resizeEvent(event)


class EpisodeGrid(QWidget):
    """选集网格：每 30 集一组（顶部 chip 条切换），当前集 accent 高亮，已看置浅，VIP 小黄点。"""

    episode_clicked = pyqtSignal(int)

    GROUP_SIZE = 30
    EPISODE_SIZE = (44, 30)

    def __init__(self, parent=None):
        super().__init__(parent)
        self._episodes = []
        self._group = 0
        self._buttons = {}
        self._metrics_cache = None  # (font, style_loss) 惰性测量

        outer = QVBoxLayout(self)
        outer.setContentsMargins(0, 0, 0, 0)
        outer.setSpacing(8)

        self._chip_host = QWidget(self)
        self._chips = FlowLayout(self._chip_host, spacing=6)
        outer.addWidget(self._chip_host)

        self._scroll = QScrollArea(self)
        self._scroll.setProperty("guoRole", "bare")
        self._scroll.setWidgetResizable(True)
        self._scroll.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        grid_layout = FlowLayout(spacing=6)
        # 左右留 6px 边距：避免首个按钮贴 viewport 原点 (0,0) 时
        # offscreen grab 出现文字重影（swap 实验证实与贴边位置绑定）
        grid_layout.setContentsMargins(6, 0, 6, 0)
        self._grid_host = _FlowHost(grid_layout)
        self._grid_host.setLayout(grid_layout)
        self._flow = grid_layout
        self._scroll.setWidget(self._grid_host)
        outer.addWidget(self._scroll, 1)

    # ------------------------------------------------------------ 公开 API
    def set_episodes(self, episodes):
        """episodes: list[dict{index, title?, watched?, vip?, current?}]，按播放顺序。"""
        self._episodes = []
        for ep in episodes or []:
            if not isinstance(ep, dict):
                continue
            item = dict(ep)
            item.setdefault("index", len(self._episodes) + 1)
            item.setdefault("title", None)
            item.setdefault("watched", False)
            item.setdefault("vip", False)
            item.setdefault("current", False)
            self._episodes.append(item)
        self._group = self._group_of_current()
        self._rebuild_chips()
        self._rebuild_grid()

    def select_group(self, group_index):
        group_index = max(0, min(group_index, self.group_count() - 1))
        if group_index == self._group:
            return
        self._group = group_index
        for i, chip in enumerate(self._chips_list()):
            chip.setChecked(i == group_index)
        self._rebuild_grid()

    def group_count(self):
        return max(1, (len(self._episodes) + self.GROUP_SIZE - 1) // self.GROUP_SIZE)

    def scroll_to_current(self):
        """把当前集滚动到可见（若不在当前分组会先切组）。"""
        for i, ep in enumerate(self._episodes):
            if ep.get("current"):
                self.select_group(i // self.GROUP_SIZE)
                btn = self._buttons.get(ep["index"])
                if btn is not None:
                    self._grid_host._sync_height()
                    self._scroll.ensureWidgetVisible(btn, 40, 60)
                return

    # ------------------------------------------------------------ 内部
    def _group_of_current(self):
        for i, ep in enumerate(self._episodes):
            if ep.get("current"):
                return i // self.GROUP_SIZE
        return 0

    def _chips_list(self):
        return [self._chips.itemAt(i).widget()
                for i in range(self._chips.count())
                if self._chips.itemAt(i) is not None]

    def _clear_layout(self, layout):
        while layout.count():
            item = layout.takeAt(0)
            widget = item.widget()
            if widget is not None:
                widget.setParent(None)
                widget.deleteLater()

    def _group_label(self, gi):
        start = gi * self.GROUP_SIZE + 1
        end = min(len(self._episodes), start + self.GROUP_SIZE - 1)
        return "%d-%d" % (start, end)

    def _rebuild_chips(self):
        self._clear_layout(self._chips)
        for gi in range(self.group_count()):
            if len(self._episodes) == 0:
                break
            chip = QPushButton(self._group_label(gi), self._chip_host)
            chip.setProperty("guoRole", "groupTab")
            chip.setCheckable(True)
            chip.setAutoExclusive(True)
            chip.setCursor(Qt.PointingHandCursor)
            chip.setChecked(gi == self._group)
            chip.clicked.connect(partial(self._on_chip_clicked, gi))
            self._chips.addWidget(chip)
        self._chip_host.setVisible(len(self._episodes) > self.GROUP_SIZE)

    def _on_chip_clicked(self, gi):
        self._group = gi
        self._rebuild_grid()

    def _text_metrics(self):
        """返回 (QFontMetrics, 样式横向损耗)：损耗 = 按钮宽 - SE_PushButtonContents 宽。
        用一次性探针按钮在当前全局样式表下实测，避免各平台/主题默认 padding 不同。"""
        if self._metrics_cache is None:
            # 探针按钮必须无父级且立即隐藏：曾 parent=grid_host 导致 deleteLater
            # 生效前作为幽灵子控件渲染在 (0,0)，文字"000"被裁成乱影盖住第一格
            probe = QPushButton("000", None)
            probe.setProperty("guoRole", "episode")
            probe.hide()
            probe.ensurePolished()
            w, h = self.EPISODE_SIZE
            probe.setFixedSize(w, h)
            opt = QStyleOptionButton()
            opt.initFrom(probe)
            opt.rect = QRect(0, 0, w, h)
            r = probe.style().subElementRect(QStyle.SE_PushButtonContents, opt, probe)
            loss = max(0, w - max(1, r.width()))
            loss = min(loss, w - 12)  # 防御异常样式把文字区压没
            fm = QFontMetrics(probe.font())
            self._metrics_cache = (fm, loss)
            probe.deleteLater()
        return self._metrics_cache

    def _episode_width(self, text):
        fm, loss = self._text_metrics()
        need = fm.horizontalAdvance(text) + loss + 6
        return int(max(self.EPISODE_SIZE[0], min(need, 120)))

    def _rebuild_grid(self):
        self._clear_layout(self._flow)
        self._buttons = {}
        self._metrics_cache = None  # 样式可能变化，重建时重测
        lo = self._group * self.GROUP_SIZE
        hi = min(len(self._episodes), lo + self.GROUP_SIZE)
        for ep in self._episodes[lo:hi]:
            label = str(ep.get("title") or ep["index"])
            btn = QPushButton(label, self._grid_host)
            btn.setProperty("guoRole", "episode")
            btn.setFixedSize(QSize(self._episode_width(label), self.EPISODE_SIZE[1]))
            btn.setCursor(Qt.PointingHandCursor)
            if ep.get("watched"):
                btn.setProperty("watched", "true")
            if ep.get("current"):
                btn.setProperty("current", "true")
            btn.clicked.connect(partial(self._emit_clicked, ep["index"]))
            if ep.get("vip"):
                dot = QLabel(btn)
                dot.setProperty("guoRole", "vipDot")
                dot.setFixedSize(6, 6)
                dot.move(btn.width() - dot.width() - 3, 3)
                dot.raise_()
            self._buttons[ep["index"]] = btn
            self._flow.addWidget(btn)
        self._grid_host._sync_height()

    def _emit_clicked(self, index):
        self.episode_clicked.emit(index)


# ============================================================== 站源切换
class _SourceRow(QWidget):
    """下拉里的一行：名称 + 当前高亮 + 可选 enabled 复选；整行可点。"""

    clicked = pyqtSignal()
    toggled = pyqtSignal(bool)

    def __init__(self, name, is_current=False, enabled=None, parent=None):
        super().__init__(parent)
        self.setProperty("guoRole", "sourceRow")
        lay = QHBoxLayout(self)
        lay.setContentsMargins(0, 0, 0, 0)
        lay.setSpacing(8)
        self._label = QLabel(name, self)
        self._label.setProperty("guoRole", "sourceName")
        if is_current:
            self._label.setProperty("current", "true")
        lay.addWidget(self._label, 1)
        if enabled is not None:
            box = QCheckBox(self)
            box.setChecked(bool(enabled))
            # 只调几何，颜色交给原生/全局
            box.setStyleSheet("QCheckBox{spacing:0px;}"
                              "QCheckBox::indicator{width:14px;height:14px;}")
            box.toggled.connect(self.toggled.emit)
            lay.addWidget(box)
        self._hovered = False

    def mousePressEvent(self, event):
        if event.button() == Qt.LeftButton:
            self.clicked.emit()
        super().mousePressEvent(event)

    def enterEvent(self, event):
        if not self._hovered:
            self._hovered = True
            _set_prop(self, "hover", True)
        super().enterEvent(event)

    def leaveEvent(self, event):
        if self._hovered:
            self._hovered = False
            _set_prop(self, "hover", False)
        super().leaveEvent(event)


class SourceSwitchPopup(QWidget):
    """当前站源名 + ▾ 按钮；点击弹出深底列表，选中发 source_selected(slug)。

    sources: list[dict{slug, name, enabled?}]——带 enabled 键的条目在行尾画复选框，
    勾选变化发 source_toggled(slug, bool)（不影响选中）。
    """

    source_selected = pyqtSignal(str)
    source_toggled = pyqtSignal(str, bool)

    ARROW = "  ▾"

    def __init__(self, parent=None):
        super().__init__(parent)
        self._sources = []
        self._current = None
        lay = QHBoxLayout(self)
        lay.setContentsMargins(0, 0, 0, 0)
        self.button = QPushButton("", self)
        self.button.setProperty("guoRole", "sourceSwitch")
        self.button.setCursor(Qt.PointingHandCursor)
        self.button.setCheckable(True)
        self.button.clicked.connect(self.open_menu)
        lay.addWidget(self.button)
        self._sync_text()

    # ------------------------------------------------------------ 公开 API
    def set_sources(self, sources):
        self._sources = [dict(s) for s in (sources or []) if isinstance(s, dict)]
        if self._current is not None and not any(
                s.get("slug") == self._current for s in self._sources):
            self._current = None
        if self._current is None and self._sources:
            self._current = self._sources[0].get("slug")
        self._sync_text()

    def set_current(self, slug):
        self._current = slug
        self._sync_text()

    def current_slug(self):
        return self._current

    def name_of(self, slug):
        for s in self._sources:
            if s.get("slug") == slug:
                return s.get("name") or slug
        return slug

    def build_menu(self):
        """构造下拉菜单（不弹出）；open_menu 与 gallery 截图共用。"""
        menu = QMenu(self.button)
        menu.setProperty("guoRole", "sourceMenu")
        for s in self._sources:
            row = _SourceRow(s.get("name") or s.get("slug", ""),
                             is_current=(s.get("slug") == self._current),
                             enabled=s.get("enabled"))
            action = QWidgetAction(menu)
            action.setDefaultWidget(row)
            menu.addAction(action)
            row.clicked.connect(partial(self._select, s.get("slug"), menu))
            row.toggled.connect(partial(self.source_toggled.emit, s.get("slug")))
        menu.aboutToHide.connect(partial(self.button.setChecked, False))
        return menu

    def open_menu(self):
        menu = self.build_menu()
        menu.setAttribute(Qt.WA_DeleteOnClose)
        self.button.setChecked(True)
        menu.popup(self.button.mapToGlobal(QPoint(0, self.button.height() + 2)))
        return menu

    # ------------------------------------------------------------ 内部
    def _sync_text(self):
        text = self.name_of(self._current) if self._current else "选择站源"
        self.button.setText(text + self.ARROW)

    def _select(self, slug, menu):
        if slug is not None and slug != self._current:
            self._current = slug
            self._sync_text()
            self.source_selected.emit(slug)
        self.button.setChecked(False)
        menu.close()


# ============================================================== 分组列表
class SectionedListView(QWidget):
    """按 section 标题分组渲染调用方提供的 item widget（工厂回调或现成控件列表）。

    set_sections 的 producer 接受三种写法：
      callable() -> QWidget            callable() -> [QWidget|callable, ...]
      [QWidget|callable, ...]（直接给列表）
    """

    def __init__(self, parent=None):
        super().__init__(parent)
        self._sections = []
        lay = QVBoxLayout(self)
        lay.setContentsMargins(0, 0, 0, 0)
        self._scroll = QScrollArea(self)
        self._scroll.setProperty("guoRole", "bare")
        self._scroll.setWidgetResizable(True)
        self._scroll.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        # 固定复用同一个 host：widgetResizable 下 QScrollArea 会套一层
        # displayWidget 包装，再次 setWidget 时旧 host 会被连带销毁，
        # 届时 deleteLater 撞 RuntimeError，所以不换新 host，只清布局。
        self._host = QWidget(self)
        self._host_lay = QVBoxLayout(self._host)
        self._host_lay.setContentsMargins(0, 0, 8, 0)
        self._host_lay.setSpacing(4)
        self._scroll.setWidget(self._host)
        lay.addWidget(self._scroll)

    def set_sections(self, sections):
        """sections: list[(title, items_producer)]；title 可为空串（不出组头）。"""
        self._sections = list(sections or [])
        self.rebuild()

    def rebuild(self):
        while self._host_lay.count():
            item = self._host_lay.takeAt(0)
            widget = item.widget()
            if widget is not None:
                widget.setParent(None)
                widget.deleteLater()
        for title, producer in self._sections:
            if title:
                header = QLabel(str(title), self._host)
                header.setProperty("guoRole", "sectionHeader")
                self._host_lay.addWidget(header)
            for widget in self._produce(producer):
                self._host_lay.addWidget(widget)
        self._host_lay.addStretch(1)

    # ------------------------------------------------------------ 内部
    def _produce(self, producer):
        out = producer() if callable(producer) else producer
        if isinstance(out, QWidget):
            return [out]
        widgets = []
        for item in (out or []):
            widget = item() if callable(item) else item
            if isinstance(widget, QWidget):
                widgets.append(widget)
        return widgets


__all__ = ["PosterCard", "BlurCoverBackground", "EpisodeGrid",
           "SourceSwitchPopup", "SectionedListView"]
