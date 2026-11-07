# -*- coding: utf-8 -*-
"""T1 青空次元（sorani）账号登录 GUI：滑块验证码 + 账密提交。

对外契约（settings_panel / 渲染脚本 / 测试消费）
==============================================
    decode_data_url(data_url) -> QPixmap | None
    SliderCaptcha(core=None, parent=None)
        set_captcha(data: dict)     # captcha 行协议返回，解码并排版
        reset()                     # 清空当前轨迹，回到初始位
        solved(dict)                # 用户真实拖动完成后发出（track 入参 dict）
    SoraniAccountDialog(core, parent=None)
        refresh_status()            # 懒查登录态（异步，不阻塞 GUI）
        status_changed(dict)        # 登录态每次刷新后转发给宿主

track 形态 = provider_sorani_account.go 的 json tag（勿改字段名）：
    point  {"x":int,"y":int,"type":"down|move|up","t":int64}
    track  {"bgImageWidth","bgImageHeight","templateImageWidth",
            "templateImageHeight","startTime","stopTime","trackList":[point]}
    login  {"identifier","password","captchaId","track"} ——经 core_client.
           sorani_account 嵌套进 "account" 键（app_runtime.go 只读 input.Account）。

轨迹语义 1:1 对齐 reference/guoapp Flutter 版
(sorani_account_view.dart:300-460)：以按下点为原点，x 为相对位移、钳制到
[0, limit]，y 为原始纵向位移（不钳制）；坐标一律用「显示坐标」——上报的
bgImageWidth 等就是本控件实际渲染尺寸（≤300 宽等比缩放），与拖动像素同
一空间。轨迹点只来自真实鼠标按下/移动/抬起，绝不合成分布；位移 <2px 或
点数 <2 视为无效拖动（复位重来），上限 4000 点（核心 valid() 上限 5000）。

样式：本模块自绘部件在 paint 时读取 ui.DESIGN token（zhenguojian-ui 允许，
参照 widgets_guo.Posters），其余控件走全局 QSS 角色（role=primary/danger/
ghost、guoRole=chip/fieldLabel、role=hint）；无硬编码色值。后台任务全部经
tasks.TaskRunner，GUI 线程绝不执行 RPC。
"""

from __future__ import annotations

import base64
import time

from PyQt5.QtCore import QRect, Qt, pyqtSignal
from PyQt5.QtGui import QColor, QImage, QPainter, QPen, QPixmap, QFont
from PyQt5.QtWidgets import (QDialog, QFormLayout, QHBoxLayout, QLabel,
                             QLineEdit, QPushButton, QSizePolicy,
                             QStackedWidget, QVBoxLayout, QWidget)

from . import ui
from .tasks import TaskRunner

__all__ = ["decode_data_url", "SliderCaptcha", "SoraniAccountDialog",
           "TRACK_LIMIT_POINTS", "DISPLAY_MAX_WIDTH"]

# —— 与 Flutter 参考实现同值的几何常量（非颜色，允许字面量） ——
DISPLAY_MAX_WIDTH = 300    # 背景显示宽 = min(300, 原图宽)
TRACK_HEIGHT = 40          # 底部滑轨高
TRACK_GAP = 10             # 背景与滑轨的间距
HANDLE_MIN_WIDTH = 40      # 手柄最小可抓宽（模板显示宽过小时兜底）
TRACK_LIMIT_POINTS = 4000  # 与 Flutter 相同；Go valid() 上限 5000
REJECTED_MIN_OFFSET = 2    # 位移小于该值判定「没真的拖」，丢弃重来


def decode_data_url(data_url):
    """把 "data:image/jpeg;base64,..." 解回 QPixmap；任何异常返回 None。"""
    if not isinstance(data_url, str) or not data_url.startswith("data:"):
        return None
    _, _, payload = data_url.partition(",")
    if not payload:
        return None
    try:
        raw = base64.b64decode(payload)
    except Exception:
        return None
    image = QImage()
    if not image.loadFromData(raw):
        return None
    return QPixmap.fromImage(image)


def _now_epoch_ms():
    return int(time.time() * 1000)


class SliderCaptcha(QWidget):
    """背景图 + 拼图模板 + 底部滑轨；拖动手柄采集真实轨迹。

    完成一次有效拖动后发 ``solved(track_dict)``。widget 尺寸由
    ``set_captcha`` 按显示坐标定死（≤300 宽等比），拖动 x 与上报的
    bgImageWidth 同一空间（参考实现 Flutter 也按显示像素上报）。
    """

    solved = pyqtSignal(object)

    def __init__(self, parent=None):
        super().__init__(parent)
        self._bg = None            # 原尺寸背景 QPixmap
        self._tpl = None           # 原尺寸模板 QPixmap
        self._disp_w, self._disp_h = DISPLAY_MAX_WIDTH, 160
        self._tmpl_w, self._tmpl_h = 60, 160
        self._limit = 1
        self._offset = 0           # 当前拼图左缘（显示坐标 x）
        self._dragging = False
        self._hover_handle = False
        self._origin = None        # 按下点（widget 局部坐标）
        self._down_mono = 0.0
        self._start_ms = 0
        self._points = []
        self.setEnabled(True)
        self.setCursor(Qt.OpenHandCursor)
        self.setFixedSize(self._disp_w, self._disp_h + TRACK_GAP + TRACK_HEIGHT)

    # ------------------------------------------------------------ 数据装载
    def set_captcha(self, data):
        """data = soraniAccount captcha 返回；成功返回 True。"""
        data = data or {}
        bg = decode_data_url(data.get("backgroundImage") or "")
        tpl = decode_data_url(data.get("templateImage") or "")
        try:
            bg_w = int(data.get("backgroundWidth") or 0)
            bg_h = int(data.get("backgroundHeight") or 0)
            tpl_w = int(data.get("templateWidth") or 0)
            tpl_h = int(data.get("templateHeight") or 0)
        except (TypeError, ValueError):
            return False
        if bg is None or tpl is None or bg_w <= 0 or bg_h <= 0 or tpl_w <= 0 or tpl_h <= 0:
            return False
        self._bg, self._tpl = bg, tpl
        self._disp_w = min(DISPLAY_MAX_WIDTH, bg_w)
        scale = float(self._disp_w) / float(bg_w)
        self._disp_h = max(1, int(round(bg_h * scale)))
        self._tmpl_w = max(1, int(round(tpl_w * scale)))
        self._tmpl_h = max(1, int(round(tpl_h * scale)))
        self._limit = max(1, self._disp_w - self._tmpl_w)
        self.reset()
        self.setFixedSize(self._disp_w, self._disp_h + TRACK_GAP + TRACK_HEIGHT)
        return True

    def reset(self):
        """放弃当前轨迹回到起点（换题/无效拖动后调用）。"""
        self._dragging = False
        self._origin = None
        self._points = []
        self._offset = 0
        self.update()

    # ------------------------------------------------------------ 几何
    def handle_rect(self):
        """手柄在 widget 局部坐标的命中区（滑轨行）。"""
        top = self._disp_h + TRACK_GAP
        width = max(self._tmpl_w, HANDLE_MIN_WIDTH)
        return QRect(self._offset, top, width, TRACK_HEIGHT)

    # ------------------------------------------------------------ 事件
    def mousePressEvent(self, event):
        if (self._bg is not None and event.button() == Qt.LeftButton
                and self.handle_rect().adjusted(-6, -6, 6, 6).contains(event.pos())):
            self._dragging = True
            self._origin = event.pos()
            self._down_mono = time.monotonic()
            self._start_ms = _now_epoch_ms()
            self._points = [{"x": 0, "y": 0, "type": "down", "t": 0}]
            self.setCursor(Qt.ClosedHandCursor)
            self.update()
        super().mousePressEvent(event)

    def mouseMoveEvent(self, event):
        if self._dragging:
            point = self._collect_point(event)
            if len(self._points) < TRACK_LIMIT_POINTS:
                self._points.append(point)
            self._offset = point["x"]
            self.update()
        else:
            inside = self.handle_rect().contains(event.pos())
            if inside != self._hover_handle:
                self._hover_handle = inside
                self.update()
        super().mouseMoveEvent(event)

    def mouseReleaseEvent(self, event):
        if not self._dragging:
            return super().mouseReleaseEvent(event)
        self._dragging = False
        self.setCursor(Qt.OpenHandCursor)
        point = self._collect_point(event, kind="up")
        if len(self._points) < TRACK_LIMIT_POINTS:
            self._points.append(point)
        self._offset = point["x"]
        stop_ms = _now_epoch_ms()
        if self._offset < REJECTED_MIN_OFFSET or len(self._points) < 2:
            # 与参考实现一致：位移过短/点数过少判无效，直接复位重来。
            self.reset()
            return
        track = {
            "bgImageWidth": int(self._disp_w),
            "bgImageHeight": int(self._disp_h),
            "templateImageWidth": int(self._tmpl_w),
            "templateImageHeight": int(self._tmpl_h),
            "startTime": int(self._start_ms),
            "stopTime": int(stop_ms),
            "trackList": list(self._points),
        }
        self._dragging = False
        self.update()
        self.solved.emit(track)

    def _collect_point(self, event, kind="move"):
        dx = event.pos().x() - self._origin.x()
        dy = event.pos().y() - self._origin.y()
        x = max(0, min(self._limit, int(round(dx))))
        y = int(round(dy))   # 纵向不钳制（参考实现原样上报）
        t = max(0, int(round((time.monotonic() - self._down_mono) * 1000)))
        return {"x": x, "y": y, "type": kind, "t": t}

    # ------------------------------------------------------------ 绘制
    def paintEvent(self, _event):
        painter = QPainter(self)
        painter.setRenderHint(QPainter.SmoothPixmapTransform)
        token = ui.DESIGN
        border = QColor(token["guo_border"])
        if self._bg is None:
            painter.fillRect(self.rect(), QColor(token["surface"]))
            painter.setPen(QColor(token["guo_muted"]))
            painter.drawText(self.rect(), Qt.AlignCenter, "验证码加载中…")
            return
        painter.drawPixmap(QRect(0, 0, self._disp_w, self._disp_h), self._bg)
        if self._tpl is not None:
            painter.drawPixmap(
                QRect(int(round(self._offset)), 0, self._tmpl_w, self._tmpl_h),
                self._tpl)
        # 外框
        painter.setPen(QPen(border, 1))
        painter.setBrush(Qt.NoBrush)
        painter.drawRect(0, 0, self._disp_w - 1, self._disp_h - 1)
        # 滑轨
        top = self._disp_h + TRACK_GAP
        groove = QRect(0, top, self._disp_w, TRACK_HEIGHT)
        painter.setPen(Qt.NoPen)
        painter.setBrush(QColor(token["guo_chip_bg"]))
        painter.drawRoundedRect(groove, 6, 6)
        painter.setPen(QPen(border, 1))
        painter.setBrush(Qt.NoBrush)
        painter.drawRoundedRect(groove, 6, 6)
        # 已拖进度填充（手柄左侧）
        if self._offset > 0:
            fill = QRect(0, top, self._offset + groove.height() // 2, TRACK_HEIGHT)
            painter.setPen(Qt.NoPen)
            painter.setBrush(QColor(token["accent_soft"]))
            painter.drawRoundedRect(fill, 6, 6)
        # 手柄：状态色按 hover/pressed/disabled 三态取 DESIGN token
        handle = self.handle_rect().adjusted(2, 4, -2, -4)
        if not self.isEnabled():
            handle_bg, handle_border, arrow = (token["surface3"], border,
                                               token["disabled_text"])
        elif self._dragging:
            handle_bg, handle_border, arrow = (token["accent_pressed"],
                                               border, token["on_accent"])
        elif self._hover_handle:
            handle_bg, handle_border, arrow = (token["accent_hover"],
                                               border, token["on_accent"])
        else:
            handle_bg, handle_border, arrow = (token["accent"], border,
                                               token["on_accent"])
        painter.setPen(QPen(QColor(handle_border), 1))
        painter.setBrush(QColor(handle_bg))
        painter.drawRoundedRect(handle, 5, 5)
        font = QFont("Microsoft YaHei")
        font.setPixelSize(13)
        font.setBold(True)
        painter.setFont(font)
        painter.setPen(QColor(arrow))
        painter.drawText(handle, Qt.AlignCenter, "»")
        painter.end()


class SoraniAccountDialog(QDialog):
    """青空账号登录框：状态行 + 登录流程（滑块 → 账密 → 提交）+ 退出登录。"""

    status_changed = pyqtSignal(object)

    def __init__(self, core=None, parent=None):
        super().__init__(parent)
        self._core = core
        self.setObjectName("soraniLoginDialog")
        self.setWindowTitle("青空次元账号")
        self.setMinimumWidth(380)
        self._status = None          # {"loggedIn":bool,"name":str} | None=未知
        self._captcha = None         # 最近一次 captcha 返回
        self._track = None           # 最近一次有效轨迹
        self._busy = False
        self._runner = TaskRunner(self)
        self._build_ui()
        self._show_page(self.page_main)
        self._refresh_qss()

    # ------------------------------------------------------------ 构建
    def _build_ui(self):
        root = QVBoxLayout(self)
        root.setContentsMargins(18, 16, 18, 14)
        root.setSpacing(10)

        title = QLabel("青空次元账号")
        title.setProperty("guoRole", "panelTitle")
        root.addWidget(title)

        head = QHBoxLayout()
        head.setSpacing(8)
        self.status_chip = QLabel("未知")
        self.status_chip.setProperty("guoRole", "chip")
        self.status_chip.setProperty("guoStatus", "dev")
        head.addWidget(self.status_chip)
        self.status_hint = QLabel("登录状态未刷新")
        self.status_hint.setProperty("role", "hint")
        head.addStretch(1)
        root.addLayout(head)

        self.error_label = QLabel("")
        self.error_label.setProperty("guoRole", "fieldError")
        self.error_label.setWordWrap(True)
        self.error_label.hide()
        root.addWidget(self.error_label)

        self.stack = QStackedWidget()
        root.addWidget(self.stack, 1)

        # 页 0：主菜单（状态 + 登录/退出按钮）
        main = QWidget()
        main_lay = QVBoxLayout(main)
        main_lay.setContentsMargins(0, 4, 0, 0)
        main_lay.setSpacing(8)
        intro = QLabel("青空站源的剧集需要登录后才能播放。登录后可解除"
                       "「青空次元需要登录后才能播放」提示。")
        intro.setProperty("role", "hint")
        intro.setWordWrap(True)
        main_lay.addWidget(intro)
        main_lay.addStretch(1)
        buttons = QHBoxLayout()
        buttons.setSpacing(8)
        self.login_button = QPushButton("账号登录")
        self.login_button.setProperty("role", "primary")
        self.login_button.setCursor(Qt.PointingHandCursor)
        self.login_button.clicked.connect(self._begin_login)
        buttons.addWidget(self.login_button)
        self.logout_button = QPushButton("退出登录")
        self.logout_button.setProperty("role", "danger")
        self.logout_button.setCursor(Qt.PointingHandCursor)
        self.logout_button.clicked.connect(self._logout)
        self.logout_button.hide()
        buttons.addWidget(self.logout_button)
        buttons.addStretch(1)
        main_lay.addLayout(buttons)
        self.stack.addWidget(main)
        self.page_main = main

        # 页 1：滑块验证码
        captcha_page = QWidget()
        cap_lay = QVBoxLayout(captcha_page)
        cap_lay.setContentsMargins(0, 4, 0, 0)
        cap_lay.setSpacing(8)
        self.captcha_hint = QLabel("拖动右侧拼图，使其与背景缺口对齐")
        self.captcha_hint.setProperty("role", "hint")
        self.captcha_hint.setWordWrap(True)
        cap_lay.addWidget(self.captcha_hint)
        row = QHBoxLayout()
        row.addStretch(1)
        self.slider = SliderCaptcha()
        self.slider.solved.connect(self._on_solved)
        row.addWidget(self.slider)
        row.addStretch(1)
        cap_lay.addLayout(row)
        cap_btns = QHBoxLayout()
        self.captcha_back = QPushButton("返回")
        self.captcha_back.setProperty("role", "ghost")
        self.captcha_back.setCursor(Qt.PointingHandCursor)
        self.captcha_back.clicked.connect(lambda: self._show_page(self.page_main))
        cap_btns.addWidget(self.captcha_back)
        cap_btns.addStretch(1)
        cap_lay.addLayout(cap_btns)
        cap_lay.addStretch(1)
        self.stack.addWidget(captcha_page)
        self.page_captcha = captcha_page

        # 页 2：账号密码
        creds = QWidget()
        form = QFormLayout(creds)
        form.setContentsMargins(0, 4, 0, 0)
        form.setSpacing(8)
        form.setLabelAlignment(Qt.AlignRight | Qt.AlignVCenter)
        self.identifier_edit = QLineEdit()
        self.identifier_edit.setPlaceholderText("用户名或邮箱")
        self.identifier_edit.setClearButtonEnabled(True)
        form.addRow("账号", self.identifier_edit)
        self.password_edit = QLineEdit()
        self.password_edit.setPlaceholderText("密码")
        self.password_edit.setEchoMode(QLineEdit.Password)
        form.addRow("密码", self.password_edit)
        row2 = QHBoxLayout()
        row2.setSpacing(8)
        self.submit_button = QPushButton("登录")
        self.submit_button.setProperty("role", "primary")
        self.submit_button.setCursor(Qt.PointingHandCursor)
        self.submit_button.clicked.connect(self._submit_login)
        row2.addWidget(self.submit_button)
        self.creds_back = QPushButton("重新验证")
        self.creds_back.setProperty("role", "ghost")
        self.creds_back.setCursor(Qt.PointingHandCursor)
        self.creds_back.clicked.connect(self._begin_login)
        row2.addWidget(self.creds_back)
        row2.addStretch(1)
        form.addRow("", row2)
        self.stack.addWidget(creds)
        self.page_creds = creds

        root.addStretch(1)

    def _refresh_qss(self):
        qss = (
            "QLabel[guoRole=\"chip\"][guoStatus=\"ok\"] {"
            "  color: %(guo_success)s; background: %(guo_accent_soft)s; }"
            "QLabel[guoRole=\"chip\"][guoStatus=\"dev\"] { color: %(guo_muted)s; }"
            "QLabel[guoRole=\"fieldError\"] { color: %(guo_danger)s; }"
            "QLabel[guoRole=\"panelTitle\"] { font-size: 16px; font-weight: 600;"
            "  color: %(guo_text)s; }"
        ) % ui.DESIGN
        self.setStyleSheet(qss)

    def showEvent(self, event):
        super().showEvent(event)
        if self._status is None:
            self.refresh_status()

    # ------------------------------------------------------------ 状态
    def refresh_status(self):
        """异步查询登录态；失败保持「未知」，不崩。"""
        if self._core is None or not callable(getattr(self._core, "sorani_account", None)):
            self._apply_status(None, "本地核心未就绪，暂无法查询登录态")
            return
        self._busy_set(True)
        self.error_label.hide()
        core = self._core
        self._runner.run(lambda: core.sorani_account(""),
                         on_success=self._on_status, on_error=self._on_status_fail,
                         on_done=lambda: self._busy_set(False))

    def _on_status(self, data):
        self._apply_status(data if isinstance(data, dict) else None)

    def _on_status_fail(self, message):
        self._apply_status(None, "读取登录状态失败：%s" % message)

    def _apply_status(self, status, error=""):
        self._status = status if isinstance(status, dict) else None
        logged = bool(self._status and self._status.get("loggedIn"))
        name = str((self._status or {}).get("name") or "").strip()
        if self._status is None:
            self.status_chip.setText("未知")
            self.status_chip.setProperty("guoStatus", "dev")
            self.status_hint.setText(error or "登录状态未知")
        elif logged:
            self.status_chip.setText("已登录")
            self.status_chip.setProperty("guoStatus", "ok")
            self.status_hint.setText(name or "欢迎回来")
        else:
            self.status_chip.setText("未登录")
            self.status_chip.setProperty("guoStatus", "dev")
            self.status_hint.setText("登录后才能播放青空剧集")
        self._repolish(self.status_chip)
        self.logout_button.setVisible(logged)
        if error:
            self._show_error(error)
        self.status_changed.emit(self._status)

    def status(self):
        """当前缓存登录态（dict | None）。"""
        return self._status

    # ------------------------------------------------------------ 流程
    def _show_page(self, page):
        self._hide_error()
        self.stack.setCurrentWidget(page)
        # 按当前页收缩高度，避免 stack 被最高的滑块页撑出空白。
        # activate() 先同步重算（滑块 setFixedSize 后 sizeHint 才是新值）。
        if page.layout() is not None:
            page.layout().activate()
        self.stack.setFixedHeight(max(page.sizeHint().height(), 1))
        self.adjustSize()

    def _begin_login(self):
        if not self._has_core():
            self._show_error("本地核心未就绪，无法获取验证码")
            return
        self._track = None
        self._busy_set(True)
        self.slider.setEnabled(False)
        self._show_page(self.page_captcha)
        self.captcha_hint.setText("验证码获取中…")
        core = self._core
        self._runner.run(lambda: core.sorani_account("captcha"),
                         on_success=self._on_captcha,
                         on_error=lambda msg: self._fail_on_page(
                             msg, self.page_main, "验证码获取失败：%s"),
                         on_done=lambda: self._busy_set(False))

    def _on_captcha(self, data):
        if not isinstance(data, dict) or not self.slider.set_captcha(data):
            self._show_page(self.page_main)
            self._show_error("验证码数据无法解析，请稍后重试")
            return
        self._captcha = data
        self.slider.setEnabled(True)
        self.captcha_hint.setText("拖动右侧拼图，使其与背景缺口对齐（当前题目 id：%s）"
                                  % data.get("id", ""))
        self._hide_error()
        # 滑块尺寸刚变，重算页面高度（_begin_login 进页时还是空占位尺寸）。
        self._show_page(self.page_captcha)

    def _on_solved(self, track):
        """真实拖动完成：保存轨迹，进入账密页。"""
        self._track = track
        self._show_page(self.page_creds)
        self.identifier_edit.setFocus()

    def _submit_login(self):
        if not self._has_core():
            self._show_error("本地核心未就绪，无法提交登录")
            return
        identifier = self.identifier_edit.text().strip()
        password = self.password_edit.text()
        if not identifier or not password:
            self._show_error("请输入青空次元用户名或邮箱与密码")
            return
        if not self._captcha or not self._track:
            self._show_error("请先拖动滑块完成安全验证")
            self._begin_login()
            return
        self._busy_set(True)
        core = self._core
        captcha_id = self._captcha.get("id", "")
        track = self._track
        self._runner.run(
            lambda: core.sorani_account("login", identifier=identifier,
                                        password=password, captcha_id=captcha_id,
                                        track=track),
            on_success=self._on_login_ok,
            on_error=self._on_login_fail,
            on_done=lambda: self._busy_set(False))

    def _on_login_ok(self, data):
        self._apply_status(data if isinstance(data, dict) else {"loggedIn": True})
        self.password_edit.clear()
        self._track = None
        self._captcha = None
        self._show_page(self.page_main)

    def _on_login_fail(self, message):
        # 验证类失败按参考实现退回滑块重取一道；账密类失败留在当前页提示。
        if any(word in message for word in ("安全验证", "验证码")):
            self._begin_login()
            self._show_error("安全验证未通过，已换题：%s" % message)
        else:
            self._show_error(message or "登录失败，请重试")

    def _logout(self):
        if not self._has_core():
            self._show_error("本地核心未就绪，无法退出登录")
            return
        self._busy_set(True)
        core = self._core
        self._runner.run(lambda: core.sorani_account("logout"),
                         on_success=self._on_status,
                         on_error=lambda msg: self._show_error("退出登录失败：%s" % msg),
                         on_done=lambda: self._busy_set(False))

    # ------------------------------------------------------------ 小工具
    def _has_core(self):
        return self._core is not None and callable(getattr(self._core, "sorani_account", None))

    def _busy_set(self, busy):
        self._busy = bool(busy)
        for widget in (self.login_button, self.logout_button,
                       self.captcha_back, self.submit_button, self.creds_back):
            widget.setEnabled(not self._busy)
        if busy:
            self.login_button.setText("处理中…")
        else:
            self.login_button.setText("账号登录")

    def _fail_on_page(self, message, page, prefix):
        self._show_page(page)
        self._show_error("%s%s" % (prefix, message))

    def _show_error(self, text):
        self.error_label.setText(text)
        self.error_label.setVisible(True)

    def _hide_error(self):
        self.error_label.clear()
        self.error_label.hide()

    @staticmethod
    def _repolish(widget):
        widget.style().unpolish(widget)
        widget.style().polish(widget)

    def closeEvent(self, event):
        self._runner.shutdown(timeout=3000)
        super().closeEvent(event)
