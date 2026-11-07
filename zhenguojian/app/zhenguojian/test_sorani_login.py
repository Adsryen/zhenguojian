# -*- coding: utf-8 -*-
"""T1 青空登录 GUI 离线回归：滑块轨迹、对话框流程、设置行挂载。

跑法（包根为 zhenguojian/app）：
    cd zhenguojian/app && python3 -m unittest zhenguojian.test_sorani_login

全部走 FakeCore（duck-typed sorani_account，返回与 provider_sorani_account.go
行协议同形的数据：status/captcha/login/logout；图片为真实 JPEG/PNG 编码的
base64 data URL）。轨迹用合成 QMouseEvent 喂进真实事件链——测的是「事件→
trackList 序列化」形状，不是人机验证本身。
"""
from __future__ import annotations

import base64
import os
import unittest

os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")
os.environ.setdefault("XDG_CONFIG_HOME", "/tmp/t1-xdg-test")
os.environ.setdefault("HOME", "/tmp/t1-home-test")

from PyQt5.QtCore import QBuffer, QEvent, QIODevice, QPoint, QPointF, Qt
from PyQt5.QtGui import QColor, QImage, QMouseEvent, QPainter, QPixmap
from PyQt5.QtTest import QTest
from PyQt5.QtWidgets import QApplication

from . import ui
from .settings_panel import SettingsPanel, _SoraniAccountRow
from .sorani_login import (DISPLAY_MAX_WIDTH, SliderCaptcha,
                           SoraniAccountDialog, TRACK_LIMIT_POINTS,
                           decode_data_url)

BG_WIDTH, BG_HEIGHT = 600, 360
TPL_WIDTH, TPL_HEIGHT = 110, 360


def _fake_background():
    image = QImage(BG_WIDTH, BG_HEIGHT, QImage.Format_RGB32)
    painter = QPainter(image)
    painter.fillRect(image.rect(), QColor("#33415c"))
    painter.fillRect(QPixmap(1, 1).rect().adjusted(340, 90, 450, 200), QColor("#101826"))
    for x in range(0, BG_WIDTH, 40):
        painter.fillRect(x, 0, 20, BG_HEIGHT, QColor("#3d4c6b"))
    painter.end()
    return image


def _fake_template():
    image = QImage(TPL_WIDTH, TPL_HEIGHT, QImage.Format_ARGB32)
    image.fill(Qt.transparent)
    painter = QPainter(image)
    painter.fillRect(0, 90, TPL_WIDTH, 110, QColor(240, 200, 90, 255))
    painter.end()
    return image


def _data_url(image, fmt, mime):
    buffer_ = QBuffer()
    buffer_.open(QBuffer.WriteOnly)
    image.save(buffer_, fmt)
    payload = base64.b64encode(bytes(buffer_.data())).decode("ascii")
    buffer_.close()
    return "data:%s;base64,%s" % (mime, payload)


def captcha_sample():
    """与核心 captcha 返回同形（soraniCaptcha json tag）。"""
    return {
        "id": "SLIDER_test001",
        "backgroundImage": _data_url(_fake_background(), "JPEG", "image/jpeg"),
        "templateImage": _data_url(_fake_template(), "PNG", "image/png"),
        "backgroundWidth": BG_WIDTH,
        "backgroundHeight": BG_HEIGHT,
        "templateWidth": TPL_WIDTH,
        "templateHeight": TPL_HEIGHT,
    }


class ImmediateRunner:
    """替身 TaskRunner：同步回调（FakeCore 无真实网络，够测）。"""

    def run(self, function, on_success=None, on_error=None, on_done=None):
        try:
            value = function()
        except Exception as exc:  # noqa: BLE001 - 复刻 Worker 的错误面
            if on_error:
                on_error(str(exc) or exc.__class__.__name__)
        else:
            if on_success:
                on_success(value)
        if on_done:
            on_done()
        return None

    def shutdown(self, timeout=3000):
        pass


class FakeCore:
    """duck-typed sorani_account；形态 = provider_sorani_account.go 行协议。"""

    def __init__(self, fail=False):
        self.fail = fail
        self.calls = []
        self.login_kwargs = []
        self.status_state = {"loggedIn": False, "name": ""}
        self.captcha_count = 0

    def list_sources(self):
        return []

    def sorani_account(self, command="", identifier="", password="",
                       captcha_id="", track=None):
        self.calls.append(command or "")
        if self.fail:
            raise RuntimeError("本地核心通信失败（测试注入）")
        if command == "":
            return dict(self.status_state)
        if command == "captcha":
            self.captcha_count += 1
            sample = captcha_sample()
            sample["id"] = "SLIDER_test%03d" % self.captcha_count
            return sample
        if command == "login":
            self.login_kwargs.append({"identifier": identifier, "password": password,
                                      "captchaId": captcha_id, "track": track})
            if not (identifier and password):
                raise RuntimeError("请输入青空次元用户名或邮箱")
            if not track or not isinstance(track, dict) or "trackList" not in track:
                raise RuntimeError("安全验证数据无效，请重新拖动滑块")
            self.status_state = {"loggedIn": True, "name": "测试用户"}
            return dict(self.status_state)
        if command == "logout":
            self.status_state = {"loggedIn": False, "name": ""}
            return dict(self.status_state)
        raise RuntimeError("未知命令")


def _send(widget, etype, pos, buttons):
    event = QMouseEvent(etype, QPointF(pos), QPointF(pos),
                        Qt.LeftButton, buttons, Qt.NoModifier)
    QApplication.sendEvent(widget, event)


def drag_slider(slider, deltas, release_extra=0):
    """按下手柄中心 → 依次按 (dx,dy) 移动 → 抬起；返回收到的 track。"""
    received = []
    slider.solved.connect(received.append)
    start = slider.handle_rect().center()
    _send(slider, QEvent.MouseButtonPress, start, Qt.LeftButton)
    last = start
    for dx, dy in deltas:
        last = start + QPoint(dx, dy)
        _send(slider, QEvent.MouseMove, last, Qt.LeftButton)
    if release_extra:
        last = last + QPoint(release_extra, 0)
        _send(slider, QEvent.MouseMove, last, Qt.LeftButton)
    _send(slider, QEvent.MouseButtonRelease, last, Qt.NoButton)
    slider.solved.disconnect(received.append)
    return received[0] if received else None


class DecodeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = QApplication.instance() or QApplication([])

    def test_roundtrip_and_garbage(self):
        url = _data_url(_fake_background(), "JPEG", "image/jpeg")
        pixmap = decode_data_url(url)
        self.assertIsInstance(pixmap, QPixmap)
        self.assertFalse(pixmap.isNull())
        self.assertEqual((pixmap.width(), pixmap.height()), (BG_WIDTH, BG_HEIGHT))
        for bad in (None, "", "not-a-data-url", "data:image/png;base64,@@@@",
                    "data:image/png;base64,aGVhZGVyLW5vLWltYWdl"):
            self.assertIsNone(decode_data_url(bad))


class SliderTrackTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = QApplication.instance() or QApplication([])

    def setUp(self):
        self.slider = SliderCaptcha()
        self.assertTrue(self.slider.set_captcha(captcha_sample()))

    def test_display_geometry(self):
        # 显示坐标空间：宽 300、高 180；模板 55×180；limit=245。
        self.assertEqual(self.slider._disp_w, DISPLAY_MAX_WIDTH)
        self.assertEqual(self.slider._disp_h, 180)
        self.assertEqual(self.slider._tmpl_w, 55)
        self.assertEqual(self.slider._tmpl_h, 180)
        self.assertEqual(self.slider._limit, 300 - 55)

    def test_drag_yields_go_shape_track(self):
        track = drag_slider(self.slider, [(40, 5), (90, -3), (160, 2)])
        self.assertIsNotNone(track)
        self.assertEqual(
            sorted(track),
            ["bgImageHeight", "bgImageWidth", "startTime", "stopTime",
             "templateImageHeight", "templateImageWidth", "trackList"])
        # Go valid()：尺寸 ∈(0,4000]、start>0、stop>=start、2<=点数<=5000
        self.assertEqual(track["bgImageWidth"], 300)
        self.assertEqual(track["bgImageHeight"], 180)
        self.assertEqual(track["templateImageWidth"], 55)
        self.assertEqual(track["templateImageHeight"], 180)
        self.assertGreater(track["startTime"], 0)
        self.assertGreaterEqual(track["stopTime"], track["startTime"])
        points = track["trackList"]
        self.assertGreaterEqual(len(points), 2)
        self.assertLessEqual(len(points), 5000)
        # 点位 = soraniCaptchaPoint：{x,y,type,t}，首 down、末 up、其余 move
        for point in points:
            self.assertEqual(sorted(point), ["t", "type", "x", "y"])
            self.assertIsInstance(point["x"], int)
            self.assertIsInstance(point["y"], int)
            self.assertIsInstance(point["t"], int)
            self.assertGreaterEqual(point["t"], 0)
            self.assertIn(point["type"], ("down", "move", "up"))
        self.assertEqual(points[0]["type"], "down")
        self.assertEqual(points[-1]["type"], "up")
        self.assertEqual(points[0]["x"], 0)
        for point in points[1:-1]:
            self.assertEqual(point["type"], "move")
        self.assertEqual(points[-1]["x"], 160)
        self.assertEqual(self.slider._offset, 160)

    def test_x_clamps_to_limit_y_does_not(self):
        track = drag_slider(self.slider, [(400, -80)], release_extra=0)
        last = track["trackList"][-1]
        self.assertEqual(last["x"], self.slider._limit)   # x 钳到 [0, limit]
        self.assertLess(last["y"], -80 + 10)               # y 原样（相对按下点）
        self.assertLess(last["y"], 0)

    def test_short_drag_discarded(self):
        received = []
        self.slider.solved.connect(received.append)
        start = self.slider.handle_rect().center()
        _send(self.slider, QEvent.MouseButtonPress, start, Qt.LeftButton)
        _send(self.slider, QEvent.MouseMove, start + QPoint(1, 0), Qt.LeftButton)
        _send(self.slider, QEvent.MouseButtonRelease, start + QPoint(1, 0), Qt.NoButton)
        self.assertEqual(received, [])
        self.assertEqual(self.slider._offset, 0)

    def test_press_outside_handle_ignored(self):
        _send(self.slider, QEvent.MouseButtonPress, QPoint(290, 10), Qt.LeftButton)
        _send(self.slider, QEvent.MouseMove, QPoint(10, 10), Qt.LeftButton)
        _send(self.slider, QEvent.MouseButtonRelease, QPoint(10, 10), Qt.NoButton)
        self.assertEqual(self.slider._offset, 0)
        self.assertEqual(self.slider._points, [])

    def test_point_cap_4000(self):
        deltas = [(i, 0) for i in range(1, 200)]
        track = drag_slider(self.slider, deltas * 25)   # 5000 个移动事件
        self.assertLessEqual(len(track["trackList"]), TRACK_LIMIT_POINTS)


class DialogFlowTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = QApplication.instance() or QApplication([])

    def setUp(self):
        ui.apply_theme("dark")
        self.app.setStyleSheet(ui.build_stylesheet())
        self.core = FakeCore()
        self.dialog = SoraniAccountDialog(self.core)
        self.dialog._runner = ImmediateRunner()

    def tearDown(self):
        self.dialog.close()

    def _goto_creds(self):
        self.dialog.login_button.click()
        self.assertIs(self.dialog.stack.currentWidget(), self.dialog.page_captcha)
        drag_slider(self.dialog.slider, [(30, 2), (80, -1), (150, 3)])
        self.assertIs(self.dialog.stack.currentWidget(), self.dialog.page_creds)

    def test_lazy_status_on_show(self):
        self.dialog.show()
        QTest.qWait(20)
        self.assertEqual(self.core.calls[0], "")          # 打开即懒查状态
        self.assertEqual(self.dialog.status_chip.text(), "未登录")
        self.assertFalse(self.dialog.logout_button.isVisibleTo(self.dialog))

    def test_full_login_flow_and_payload(self):
        self.dialog.show()
        QTest.qWait(20)
        self._goto_creds()
        self.dialog.identifier_edit.setText("tester@mail")
        self.dialog.password_edit.setText("s3cret")
        self.dialog.submit_button.click()
        entry = self.core.login_kwargs[-1]
        self.assertEqual(entry["identifier"], "tester@mail")
        self.assertEqual(entry["password"], "s3cret")
        self.assertEqual(entry["captchaId"], "SLIDER_test001")
        self.assertIn("trackList", entry["track"])        # 轨迹原样进 core
        self.assertEqual(self.dialog.status_chip.text(), "已登录")
        self.assertEqual(self.dialog.status_hint.text(), "测试用户")
        self.assertTrue(self.dialog.logout_button.isVisibleTo(self.dialog))

    def test_captcha_then_credentials_order(self):
        # 任务流程：先弹滑块，拖动完成后才出现账密页（顺序不可反）。
        self.dialog.login_button.click()
        self.assertIs(self.dialog.stack.currentWidget(), self.dialog.page_captcha)
        self.assertEqual(self.core.calls[-1], "captcha")
        drag_slider(self.dialog.slider, [(60, 0), (120, 4)])
        self.assertIs(self.dialog.stack.currentWidget(), self.dialog.page_creds)

    def test_submit_without_captcha_restarts_flow(self):
        # 直接跳到账密页但无轨迹：应退回验证码流程（重取滑块）。
        self.dialog.stack.setCurrentWidget(self.dialog.page_creds)
        self.dialog.identifier_edit.setText("a")
        self.dialog.password_edit.setText("b")
        before = self.core.captcha_count
        self.dialog.submit_button.click()
        self.assertEqual(self.core.captcha_count, before + 1)
        self.assertIs(self.dialog.stack.currentWidget(), self.dialog.page_captcha)

    def test_missing_credentials_blocked_locally(self):
        self._goto_creds()
        before = len(self.core.calls)
        self.dialog.submit_button.click()                # 空账密：本地拦下
        self.assertEqual(len(self.core.calls), before)
        self.assertTrue(self.dialog.error_label.isVisibleTo(self.dialog))

    def test_captcha_fail_falls_back_to_main_with_error(self):
        self.core.fail = True
        self.dialog.login_button.click()
        self.assertIs(self.dialog.stack.currentWidget(), self.dialog.page_main)
        self.assertIn("验证码获取失败", self.dialog.error_label.text())

    def test_login_rejected_reissues_captcha(self):
        self._goto_creds()
        self.dialog.identifier_edit.setText("tester")
        self.dialog.password_edit.setText("pw")

        def reject(**kwargs):
            self.core.login_kwargs.append(kwargs)
            raise RuntimeError("安全验证未通过，请重新拖动滑块")

        original = self.core.sorani_account

        def scripted(command="", **kwargs):
            if command == "login":
                return reject(**kwargs)
            return original(command=command, **kwargs)

        self.core.sorani_account = scripted
        self.dialog.submit_button.click()
        # 参考实现语义：安全验证失败 → 退回滑块页并换新题
        self.assertIs(self.dialog.stack.currentWidget(), self.dialog.page_captcha)
        self.assertEqual(self.core.captcha_count, 2)

    def test_logout(self):
        self._goto_creds()
        self.dialog.identifier_edit.setText("tester")
        self.dialog.password_edit.setText("pw")
        self.dialog.submit_button.click()
        self.assertTrue(self.dialog.logout_button.isVisibleTo(self.dialog))
        self.dialog.logout_button.click()
        self.assertEqual(self.core.calls[-1], "logout")
        self.assertEqual(self.dialog.status_chip.text(), "未登录")


class SettingsRowTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = QApplication.instance() or QApplication([])

    def setUp(self):
        ui.apply_theme("dark")
        self.app.setStyleSheet(ui.build_stylesheet())
        self.core = FakeCore()
        self.panel = SettingsPanel(core=self.core)
        self.panel._sorani_runner = ImmediateRunner()

    def tearDown(self):
        self.panel.deleteLater()
        QTest.qWait(20)

    def _row(self):
        row = self.panel.findChild(_SoraniAccountRow, "s1_sorani_account")
        self.assertIsNotNone(row, "站源区未挂载青空账号行")
        return row

    def test_row_mounted_lazy_status(self):
        row = self._row()
        self.panel.show()
        QTest.qWait(30)
        self.assertEqual(self.core.calls[0], "")
        self.assertEqual(row.chip.text(), "未登录")
        self.assertTrue(row.login_btn.isVisibleTo(row))
        self.assertFalse(row.logout_btn.isVisibleTo(row))

    def test_logged_in_chip_and_logout_button(self):
        row = self._row()
        row.set_status({"loggedIn": True, "name": "小明"})
        self.assertEqual(row.chip.text(), "已登录：小明")
        self.assertEqual(row.chip.property("guoStatus"), "ok")
        self.assertTrue(row.logout_btn.isVisibleTo(row))

    def test_status_failure_shows_unknown(self):
        self.core.fail = True
        row = self._row()
        self.panel.show()
        QTest.qWait(30)
        self.assertEqual(row.chip.text(), "未知")          # 失败不崩、显示未知

    def test_usable_accepts_account_only_core(self):
        class AccountOnlyCore:
            def sorani_account(self, command="", **kwargs):
                return {"loggedIn": True, "name": "solo"}

        panel = SettingsPanel(core=AccountOnlyCore())
        panel._sorani_runner = ImmediateRunner()
        row = panel.findChild(_SoraniAccountRow, "s1_sorani_account")
        panel.show()
        QTest.qWait(30)
        self.assertEqual(row.chip.text(), "已登录：solo")
        self.assertIs(panel._sorani_core().__class__, AccountOnlyCore)
        panel.deleteLater()

    def test_make_sorani_dialog_wired(self):
        self.panel.show()
        QTest.qWait(20)
        dialog = self.panel.make_sorani_dialog()
        self.assertIsInstance(dialog, SoraniAccountDialog)
        self.assertIs(dialog._core, self.core)
        # 对话框状态回传 → 行同步
        dialog._apply_status({"loggedIn": True, "name": "小明"})
        self.assertEqual(self._row().chip.text(), "已登录：小明")
        dialog.close()

    def test_populate_twice_keeps_single_account_row(self):
        self.panel._populate_sources()
        widgets = [self.panel._sources_body.itemAt(i).widget()
                   for i in range(self.panel._sources_body.count())]
        rows = [w for w in widgets if isinstance(w, _SoraniAccountRow)]
        self.assertEqual(len(rows), 1)
        self.assertIs(rows[0], self.panel._sorani_row)


if __name__ == "__main__":
    unittest.main(verbosity=2)
