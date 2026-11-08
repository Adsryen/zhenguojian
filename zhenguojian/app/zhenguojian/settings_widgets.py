# -*- coding: utf-8 -*-
"""S2 设置页自绘小件：药丸开关 / 省略号标签 / 站源卡片 / 自适应栅格。

与 settings_panel 拆分说明
==========================
开关整体改为 paintEvent 自绘：Qt5 的 QSS ::indicator 在多条伪状态/动态属性
规则叠加时会互相劫持子控件匹配（历史坑，见 S1 实现注释），且 QSS 无法做滑块
位移动画。这里保留八态显式矩阵 off/on/offHover/onHover/offPressed/onPressed/
disabled/disabledOn（swState 动态属性同步更新，仅作为状态快照与兼容读点，
样式不再驱动绘制），paint 时按 ui.DESIGN token 取色——lighter/darker/alpha
派生属于 token 派生，文件内无任何字面色值。

- 滑动动画：QPropertyAnimation 驱动一个隐藏子件（1×1 位置代理）的 pos
  属性（QWidget 原生 Q_PROPERTY，PyQt5 无法自定义 Q_PROPERTY 的替代写法），
  140ms OutCubic；valueChanged → 父重绘，轨道/阴影/滑块逐帧同步。
- 层次：轨道 1px 描边（on 态描边取填充 darker），滑块下两层 guo_shadow
  派生软阴影，focus 时外圈 accent 描边。
- 键盘可达：QCheckBox 原生 Space 切换（StrongFocus），focus 环随事件重绘。

SourceGrid：面板宽 <640 每行 3 卡、≥640 每行 4 卡（resizeEvent 自适应），
单卡宽封顶后整体居中；SourceCard = 名称 + 18+ 徽章 + 状态 chip + 精简
note（省略号，全文进 tooltip）+ 开关。
"""

from PyQt5.QtCore import QEasingCurve, QEvent, QPoint, QPointF, QRectF, \
    QPropertyAnimation, QSize, Qt, pyqtSignal
from PyQt5.QtGui import QColor, QPainter, QPen
from PyQt5.QtWidgets import QCheckBox, QHBoxLayout, QLabel, QVBoxLayout, QWidget

from . import ui


def _c(name):
    """DESIGN token → QColor（paint 时读，主题切换后随 repaint_all 生效）。"""
    return QColor(ui.DESIGN[name])


def _palette(state):
    """八态 → (轨道填充, 轨道描边, 滑块色)，全部 token 或其派生。"""
    if state == "off":
        return _c("guo_chip_bg"), _c("guo_border"), _c("on_accent")
    if state == "offHover":
        return _c("guo_chip_hover"), _c("guo_accent"), _c("on_accent")
    if state == "offPressed":
        return _c("guo_chip_pressed"), _c("guo_accent"), _c("on_accent")
    if state == "on":
        fill = _c("guo_accent")
        return fill, fill.darker(118), _c("on_accent")
    if state == "onHover":
        fill = _c("guo_accent_hover")
        return fill, fill.darker(115), _c("on_accent")
    if state == "onPressed":
        fill = _c("guo_accent_pressed")
        return fill, fill.darker(112), _c("on_accent")
    if state == "disabledOn":
        return _c("surface3"), _c("disabled_border"), _c("disabled_text").lighter(190)
    # disabled（含「开发中」：禁用未勾选）
    return _c("surface2"), _c("disabled_border"), _c("disabled_text").lighter(170)


# ------------------------------------------------------------------ 药丸开关
class GuoSwitch(QCheckBox):
    """自绘药丸开关：八态矩阵 + 滑块滑动动画 + focus 环。

    几何：widget 44×26（外圈 2px 留给 focus 描边，不越界），轨道 40×22
    圆角 11，滑块直径 18，内缩 2，行程 x∈[4,22]。
    """

    _MARGIN = 2
    _TRACK_W, _TRACK_H = 40, 22
    _PAD = 2
    _THUMB = 18
    _KX_OFF = _MARGIN + _PAD                                  # 4
    _KX_ON = _MARGIN + _TRACK_W - _PAD - _THUMB               # 22
    _KY = _MARGIN + (_TRACK_H - _THUMB) // 2                  # 4
    ANIM_MS = 140

    def __init__(self, parent=None):
        super().__init__(parent)
        self.setProperty("guoRole", "switch")
        self.setFocusPolicy(Qt.StrongFocus)     # Space 切换 + 焦点可见
        self.setAttribute(Qt.WA_Hover, True)    # QWidget 用 WA_Hover，非 setAcceptHoverEvents
        self._down = False
        self._hover = False
        self._force_focus = False               # 出图/自检用：强制画 focus 环
        self._driver = QWidget(self)            # 1×1 位置代理，动画驱动它的 pos
        self._driver.setFixedSize(1, 1)
        self._driver.hide()
        self._driver.move(self._KX_OFF, self._KY)
        self._slide = QPropertyAnimation(self._driver, b"pos", self)
        self._slide.setDuration(self.ANIM_MS)
        self._slide.setEasingCurve(QEasingCurve.OutCubic)
        self._slide.valueChanged.connect(lambda _v: self.update())
        # Qt5 没有 QEvent.CheckStateChange（6.x 才补的枚举），勾选变化走
        # toggled 信号驱动动画与 swState；blockSignals 初始化路径由
        # refresh_state() 手动落位，语义与旧 _GuoSwitch 一致。
        self.toggled.connect(self._on_toggled)
        self._refresh()

    # ------------------------------------------------------------ 尺寸
    def sizeHint(self):
        return QSize(self._MARGIN * 2 + self._TRACK_W,
                     self._MARGIN * 2 + self._TRACK_H)

    def minimumSizeHint(self):
        return self.sizeHint()

    # ------------------------------------------------------------ 状态
    def changeEvent(self, event):
        t = event.type()
        if t == QEvent.EnabledChange:
            # 启用态切换会改变配色矩阵；勾选/取消勾选不产生 QEvent（Qt5 无
            # CheckStateChange 枚举），由 toggled 信号走 _on_toggled。
            self._refresh()
        elif t in (QEvent.FocusIn, QEvent.FocusOut):
            self.update()
        super().changeEvent(event)

    def _on_toggled(self, checked):
        # 用户点击、Space、程序 setChecked（未 blockSignals 时）都会到这里；
        # 未显示时直接落位，避免挂载动画闪现。
        self._refresh()
        self._slide_to(checked, instant=not self.isVisible())

    def showEvent(self, event):
        self._slide.stop()
        self._driver.move(self._KX_ON if self.isChecked() else self._KX_OFF,
                          self._KY)
        self._refresh()
        super().showEvent(event)

    def hoverEnterEvent(self, event):
        self._hover = True
        self._refresh()
        super().hoverEnterEvent(event)

    def hoverLeaveEvent(self, event):
        self._hover = False
        self._down = False
        self._refresh()
        super().hoverLeaveEvent(event)

    def mousePressEvent(self, event):
        self._down = True
        self._refresh()
        super().mousePressEvent(event)

    def mouseReleaseEvent(self, event):
        self._down = False
        self._refresh()
        super().mouseReleaseEvent(event)

    def _state_name(self):
        if not self.isEnabled():
            return "disabledOn" if self.isChecked() else "disabled"
        if self._down:
            return "onPressed" if self.isChecked() else "offPressed"
        if self._hover:
            return "onHover" if self.isChecked() else "offHover"
        return "on" if self.isChecked() else "off"

    def _refresh(self, *args):
        new = self._state_name()
        if self.property("swState") != new:
            self.setProperty("swState", new)   # 兼容读点：状态快照
        self.update()

    def refresh_state(self):
        """blockSignals 初始化路径：落位滑块 + 重算 swState（无动画）。"""
        self._slide.stop()
        self._driver.move(self._KX_ON if self.isChecked() else self._KX_OFF,
                          self._KY)
        self._refresh()

    # 出图/自检用：模拟指针态（不影响真实交互路径）
    def simulate_hover(self, on):
        self._hover = bool(on)
        self._refresh()

    def simulate_press(self, on):
        self._down = bool(on)
        self._refresh()

    def simulate_focus(self, on):
        self._force_focus = bool(on)
        self.update()

    # ------------------------------------------------------------ 动画
    def _slide_to(self, checked, instant=False):
        tx = self._KX_ON if checked else self._KX_OFF
        self._slide.stop()
        if instant or not self.isVisible():
            self._driver.move(tx, self._KY)
            self.update()
            return
        self._slide.setStartValue(self._driver.pos())
        self._slide.setEndValue(QPoint(tx, self._KY))
        self._slide.start()

    # ------------------------------------------------------------ 绘制
    def paintEvent(self, event):
        p = QPainter(self)
        p.setRenderHint(QPainter.Antialiasing, True)
        fill, stroke, knob = _palette(self._state_name())
        m = float(self._MARGIN)
        track = QRectF(m + 0.5, m + 0.5, self._TRACK_W - 1.0, self._TRACK_H - 1.0)
        radius = (self._TRACK_H - 1.0) / 2.0

        # 轨道：填充 + 1px 描边（on 态描边取填充 darker，形成边缘层次）
        p.setPen(QPen(stroke, 1.0))
        p.setBrush(fill)
        p.drawRoundedRect(track, radius, radius)

        # 滑块软阴影：两层 guo_shadow 派生（外层淡、内层近）
        dx = float(self._driver.x())
        dy = float(self._KY)
        base = _c("guo_shadow")
        p.setPen(Qt.NoPen)
        outer = QColor(base)
        outer.setAlpha(36)
        inner = QColor(base)
        inner.setAlpha(72)
        d = float(self._THUMB)
        p.setBrush(outer)
        p.drawEllipse(QPointF(dx + d / 2.0, dy + d / 2.0 + 1.4),
                      d / 2.0 + 2.2, d / 2.0 + 2.2)
        p.setBrush(inner)
        p.drawEllipse(QPointF(dx + d / 2.0, dy + d / 2.0 + 0.6),
                      d / 2.0 + 0.9, d / 2.0 + 0.9)

        # 滑块本体 + 细边（描边色派生自轨道描边，alpha 压低）
        hair = QColor(stroke)
        hair.setAlpha(140)
        p.setPen(QPen(hair, 0.8))
        p.setBrush(knob)
        p.drawEllipse(QRectF(dx, dy, d, d))

        # 键盘 focus 环：轨道外扩 2px accent 描边
        if self.hasFocus() or self._force_focus:
            ring = _c("guo_accent")
            ring.setAlpha(150)
            p.setPen(QPen(ring, 1.3))
            p.setBrush(Qt.NoBrush)
            p.drawRoundedRect(track.adjusted(-1.6, -1.6, 1.6, 1.6),
                              radius + 1.6, radius + 1.6)
        p.end()


# ------------------------------------------------------------------ 省略号标签
class ElidedLabel(QLabel):
    """单行标签：宽度不足时按省略号截断，全文始终留在 tooltip。"""

    def __init__(self, text="", parent=None):
        super().__init__(text, parent)
        self._full = str(text or "")

    def setText(self, text):                       # noqa: N802 (Qt 命名)
        self._full = str(text or "")
        self._reel()

    def setFullText(self, text):                   # noqa: N802
        self.setText(text)

    def fullText(self):
        return self._full

    def resizeEvent(self, event):
        self._reel()
        super().resizeEvent(event)

    def _reel(self):
        fm = self.fontMetrics()
        w = max(self.width() - 2, 10)
        if fm.horizontalAdvance(self._full) <= w:
            QLabel.setText(self, self._full)
        else:
            QLabel.setText(self, fm.elidedText(self._full, Qt.ElideRight, w))


# ------------------------------------------------------------------ 站源卡片
class SourceCard(QWidget):
    """栅格一卡：名称 + 18+ 徽章 / 状态 chip + 精简 note / 开关。

    对外与旧 _SourceRow 同构：slug、switch、chip、error、toggled(str,bool)、
    apply_state / reject_toggle / clear_error；hover 高亮走 [hover="true"]
    属性选择器（与 widgets_guo 同一路线，避开 :hover 伪状态叠加坑）。
    """

    toggled = pyqtSignal(str, bool)

    def __init__(self, info, parent=None):
        super().__init__(parent)
        self.slug = info["slug"]
        self.setProperty("guoRole", "sourceCard")
        self.setObjectName("s1src_" + self.slug.replace("-", "_"))

        lay = QHBoxLayout(self)
        lay.setContentsMargins(10, 7, 8, 7)
        lay.setSpacing(8)

        col = QVBoxLayout()
        col.setContentsMargins(0, 0, 0, 0)
        col.setSpacing(3)

        line1 = QHBoxLayout()
        line1.setContentsMargins(0, 0, 0, 0)
        line1.setSpacing(6)
        self.name = ElidedLabel(info["name"])
        self.name.setProperty("guoRole", "sourceCardName")
        line1.addWidget(self.name, 1)
        if info.get("adult"):
            badge = QLabel("18+")
            badge.setProperty("guoRole", "adultBadge")
            badge.setAlignment(Qt.AlignCenter)
            line1.addWidget(badge)
        col.addLayout(line1)

        line2 = QHBoxLayout()
        line2.setContentsMargins(0, 0, 0, 0)
        line2.setSpacing(6)
        self.chip = QLabel()
        self.chip.setProperty("guoRole", "chip")
        self.chip.setAlignment(Qt.AlignCenter)
        line2.addWidget(self.chip)
        self.error = ElidedLabel("")
        self.error.setProperty("guoRole", "rowError")
        self.error.setVisible(False)
        line2.addWidget(self.error, 1)
        self.note = ElidedLabel("")
        self.note.setProperty("guoRole", "sourceCardNote")
        line2.addWidget(self.note, 1)
        line2.addStretch(1)
        col.addLayout(line2)

        lay.addLayout(col, 1)
        self.switch = GuoSwitch()
        self.switch.setCursor(Qt.PointingHandCursor)
        lay.addWidget(self.switch, 0, Qt.AlignVCenter)

        self._note_text = info.get("note") or ""
        tip = "slug: %s" % self.slug
        if self._note_text:
            tip += "\n" + self._note_text
        self.setToolTip(tip)
        self.name.setToolTip(tip)
        self.note.setToolTip(self._note_text)

        self.apply_state(info)
        self.switch.toggled.connect(self._emit)

    # hover 属性选择器矩阵（同旧 _SourceRow）
    def enterEvent(self, event):
        self._set_hover(True)
        super().enterEvent(event)

    def leaveEvent(self, event):
        self._set_hover(False)
        super().leaveEvent(event)

    def _set_hover(self, on):
        self.setProperty("hover", "true" if on else None)
        style = self.style()
        style.unpolish(self)
        style.polish(self)

    def apply_state(self, info):
        """implemented/available/enabled → 控件状态（语义与旧行一致）。

        - 开发中（!implemented）：开关禁用且未勾选，chip「开发中」（警告色），
          名称/note 置灰——卡片底色走 cardState="dev"。
        - 暂不可用（available=False）：开关禁用（保留勾选态），chip「暂不可用」。
        - 正常：可交互，chip 收起（15 张卡都挂「已实现」反而是噪音）。
        """
        implemented = bool(info.get("implemented", True))
        available = bool(info.get("available", True))
        enabled = bool(info.get("enabled", implemented)) and implemented
        status = None
        if not implemented:
            self.chip.setText("开发中")
            self.chip.setProperty("guoStatus", "dev")
            status = "dev"
        elif not available:
            self.chip.setText("暂不可用")
            self.chip.setProperty("guoStatus", "unavailable")
            status = "unavailable"
        else:
            self.chip.setText("已实现")     # 文案保留给读点，控件隐藏
            self.chip.setProperty("guoStatus", "ok")
        self.chip.setVisible(status is not None)
        dim = "true" if status == "dev" else None
        for w in (self.name, self.note):
            if w.property("dim") != dim:
                w.setProperty("dim", dim)
                style = w.style()
                style.unpolish(w)
                style.polish(w)
        if self.property("cardState") != status:
            self.setProperty("cardState", status)
            style = self.style()
            style.unpolish(self)
            style.polish(self)
        self.note.setText(self._note_text)
        self.switch.blockSignals(True)
        self.switch.setChecked(enabled)
        self.switch.setEnabled(implemented and available)
        self.switch.blockSignals(False)
        self.switch.refresh_state()

    def _emit(self, checked):
        self.toggled.emit(self.slug, bool(checked))

    def reject_toggle(self, message):
        """core 同步失败：回滚开关（不发信号）并在行内显示红字。"""
        self.switch.blockSignals(True)
        self.switch.setChecked(not self.switch.isChecked())
        self.switch.blockSignals(False)
        self.switch.refresh_state()
        self._show_error(message)

    def clear_error(self):
        self._show_error("")

    def _show_error(self, message):
        text = str(message or "").strip()
        if text:
            short = text if len(text) <= 40 else text[:39] + "…"
            self.error.setText(short)
            self.error.setToolTip(text)
            self.error.setVisible(True)
            self.note.setVisible(False)
        else:
            self.error.setText("")
            self.error.setVisible(False)
            self.note.setVisible(True)


# ------------------------------------------------------------------ 自适应栅格
class SourceGrid(QWidget):
    """站源卡片栅格：<640px 每行 3 卡，≥640px 每行 4 卡（resizeEvent 驱动）。

    手排 geometry 而非 QGridLayout：断点规则（3/4 列 + 单卡宽封顶 + 居中）
    在这里一目了然，也不和行拆分/增删的布局状态打架。卡片顺序 = 添加顺序，
    行优先填充。
    """

    GAP = 10
    CARD_H = 62
    COL_WIDE = 640        # 容器宽度断点：≥ 则 4 列，否则 3 列
    MAX_CARD_W = 300      # 宽屏时单卡封顶，多余空间均分居中

    def __init__(self, parent=None):
        super().__init__(parent)
        self.setObjectName("s1_source_grid")
        self._cards = []
        self._cols = 3

    def columns(self):
        return self._cols

    def card_count(self):
        return len(self._cards)

    def add_card(self, card):
        card.setParent(self)
        card.show()
        self._cards.append(card)
        self._relayout()

    def clear_cards(self):
        for w in self._cards:
            w.setParent(None)
            w.deleteLater()
        self._cards = []
        self._relayout()

    def resizeEvent(self, event):
        self._relayout()
        super().resizeEvent(event)

    def _relayout(self):
        n = len(self._cards)
        if n == 0:
            self.setMinimumHeight(0)
            self.updateGeometry()
            return
        cols = 4 if self.width() >= self.COL_WIDE else 3
        self._cols = cols
        rows = (n + cols - 1) // cols
        body = self.width() - self.GAP * (cols - 1)
        cw = int(body / cols)
        if cw > self.MAX_CARD_W:
            cw = self.MAX_CARD_W
            content_w = cw * cols + self.GAP * (cols - 1)
            x0 = max(0, int((self.width() - content_w) / 2))
        else:
            x0 = 0
        for i, w in enumerate(self._cards):
            r, c = divmod(i, cols)
            x = x0 + c * (cw + self.GAP)
            y = self.GAP + r * (self.CARD_H + self.GAP)
            w.setGeometry(x, y, cw, self.CARD_H)
        self.setMinimumHeight(self.GAP * 2 + rows * self.CARD_H
                              + (rows - 1) * self.GAP)
        self.updateGeometry()


__all__ = ["GuoSwitch", "ElidedLabel", "SourceCard", "SourceGrid"]
