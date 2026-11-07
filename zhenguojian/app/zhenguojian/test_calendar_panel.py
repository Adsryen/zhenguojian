# -*- coding: utf-8 -*-
"""Regression tests for the new-series wall and calendar theme."""
from __future__ import annotations

import os
import unittest
from unittest.mock import patch

os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")

from PyQt5.QtCore import QObject, QPoint, QPointF, QRect, Qt, pyqtSignal
from PyQt5.QtGui import QColor, QFont, QFontDatabase, QFontMetrics, QPainter, QPixmap, QWheelEvent
from PyQt5.QtTest import QTest
from PyQt5.QtWidgets import QApplication, QPushButton

from . import ui
from .calendar_panel import CalendarPanel, _new_series_drama
from .poster_wall import _PosterDelegate


class FakeCovers(QObject):
    loaded = pyqtSignal(str, object)

    def __init__(self, _core, parent=None):
        super().__init__(parent)
        self.pixmap = QPixmap(160, 220)
        self.pixmap.fill(QColor("#426e67"))

    def cached(self, _identifier):
        return self.pixmap

    def known(self, _identifier):
        return True

    def request(self, _drama, force=False):
        pass

    def stop(self):
        pass


class FakeWatch(QObject):
    changed = pyqtSignal()

    def __init__(self):
        super().__init__()
        self.reserved = set()

    def is_reserved(self, identifier):
        return identifier in self.reserved

    def reserve_drama(self, drama):
        self.reserved.add(drama["id"])
        self.changed.emit()

    def cancel_reservation(self, identifier):
        self.reserved.discard(identifier)
        self.changed.emit()


class FakeCore:
    def __init__(self, page_size=12, page_count=6):
        self.page_size = page_size
        self.page_count = page_count
        self.requests = []

    def new_series(self, gender="2", offset=0, cursor=""):
        self.requests.append((gender, offset, cursor))
        items = []
        for index in range(offset, offset + self.page_size):
            items.append({
                "seriesId": "%s-%d" % (gender, index),
                "title": "测试剧集 %d" % index,
                "episodeCnt": 105,
                "subTitle": "剧情·共105集",
                "heat": "8589万",
                "score": "8.6",
                "tags": ["剧情", "都市"],
            })
        next_offset = offset + self.page_size
        return {"items": items, "nextOffset": next_offset,
                "sessionId": "session-" + gender,
                "hasMore": next_offset < self.page_size * self.page_count}

    def calendar(self, date="", offset=0):
        return {
            "dates": ["20261105", "20261106"],
            "defaultDate": "20261105",
            "hasMore": False,
            "items": [{"seriesId": "future", "title": "测试预约",
                       "description": "测试简介", "category": "剧情",
                       "isOnline": False, "hint": "即将上线"}],
        }


class DeferredRunner:
    def __init__(self):
        self.pending = []

    def run(self, function, on_success=None, on_error=None, on_done=None):
        self.pending.append((function, on_success, on_error))

    def resolve(self, index=0):
        function, success, error = self.pending.pop(index)
        try:
            result = function()
        except Exception as exc:
            if error:
                error(str(exc))
        else:
            if success:
                success(result)

    def fail(self, message="测试失败"):
        _function, _success, error = self.pending.pop(0)
        error(message)

    def shutdown(self, timeout=3000):
        self.pending.clear()


class NewSeriesPanelTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = QApplication.instance() or QApplication([])
        QFontDatabase.addApplicationFont(r"C:\Windows\Fonts\msyh.ttc")
        cls.app.setFont(QFont("Microsoft YaHei", 9))

    def setUp(self):
        ui.apply_theme("dark")
        self.app.setStyleSheet(ui.build_stylesheet())
        self.cover_patch = patch("zhenguojian.calendar_panel.CoverLoader", FakeCovers)
        self.cover_patch.start()
        self.core = FakeCore()
        self.watch = FakeWatch()
        self.panel = CalendarPanel(self.core, self.watch)
        self.panel._runner.shutdown()
        self.runner = DeferredRunner()
        self.panel._runner = self.runner
        self.panel.resize(760, 560)
        self.panel.show()
        QTest.qWait(30)

    def tearDown(self):
        self.panel.hide()
        self.runner.shutdown()
        self.panel._covers.stop()
        self.panel.deleteLater()
        QTest.qWait(10)
        self.cover_patch.stop()
        ui.apply_theme("dark")
        self.app.setStyleSheet(ui.build_stylesheet())

    def fill_until_scrollable(self):
        for _ in range(6):
            QTest.qWait(20)
            if not self.runner.pending:
                break
            self.runner.resolve()
        QTest.qWait(50)
        self.assertGreater(self.panel.new_wall.verticalScrollBar().maximum(), 400)
        self.assertFalse(self.runner.pending)

    def test_heat_and_episode_overlays(self):
        drama = _new_series_drama({"seriesId": "1", "episodeCnt": 105,
                                   "heat": "8589万", "subTitle": "剧情·共105集"})
        self.assertEqual(drama["heat"], "8589万")
        delegate = _PosterDelegate(self.panel.new_wall)
        canvas = QPixmap(180, 240)
        canvas.fill(QColor("#426e67"))
        painter = QPainter(canvas)
        painted = []
        with patch.object(delegate, "_paint_overlay",
                          side_effect=lambda _p, rect, text, _font: painted.append((rect, text))):
            delegate._paint_new_series_badges(painter, QRect(0, 0, 180, 220), drama)
        painter.end()
        self.assertEqual([entry[1] for entry in painted], ["共105集", "8589万"])
        self.assertLess(painted[1][0].right(), painted[0][0].left())

        drama = _new_series_drama({"seriesId": "2", "episodeCnt": 20,
                                   "heat": "", "subTitle": "剧情·更新至20集"})
        self.assertEqual(drama["releaseStatus"], "ongoing")
        painter = QPainter(canvas)
        painted = []
        with patch.object(delegate, "_paint_overlay",
                          side_effect=lambda _p, rect, text, _font: painted.append((rect, text))):
            delegate._paint_new_series_badges(painter, QRect(0, 0, 180, 220), drama)
        painter.end()
        self.assertEqual([entry[1] for entry in painted], ["更新至20集"])

    def test_real_wheel_event_prefetches_without_loading_row(self):
        self.fill_until_scrollable()
        wall = self.panel.new_wall
        bar = wall.verticalScrollBar()
        bar.setValue(bar.maximum() - 430)
        QTest.qWait(30)
        self.assertFalse(self.runner.pending)
        before = wall.count()
        start = bar.value()
        position = QPointF(wall.viewport().rect().center())
        event = QWheelEvent(position, QPointF(wall.viewport().mapToGlobal(position.toPoint())),
                            QPoint(), QPoint(0, -120), Qt.NoButton, Qt.NoModifier,
                            Qt.NoScrollPhase, False)
        QApplication.sendEvent(wall.viewport(), event)
        QTest.qWait(30)
        self.assertGreater(bar.value(), start)
        self.assertEqual(len(self.runner.pending), 1)
        self.assertTrue(self.panel._new_loading)
        self.assertTrue(self.panel.new_status.isHidden())
        self.runner.resolve()
        QTest.qWait(50)
        self.assertGreater(wall.count(), before)
        self.assertEqual(self.core.requests[-1][2], "session-2")

    def test_existing_first_page_is_not_requested_twice(self):
        self.assertEqual(len(self.runner.pending), 1)
        self.panel.refresh()
        self.panel._switch_tab("new")
        QTest.qWait(20)
        self.assertEqual(len(self.runner.pending), 1)

    def test_short_first_pages_fill_viewport_automatically(self):
        self.core.page_size = 2
        self.core.page_count = 4
        for _ in range(6):
            QTest.qWait(30)
            if not self.runner.pending:
                break
            self.runner.resolve()
        QTest.qWait(50)
        self.assertEqual([request[1] for request in self.core.requests], [0, 2, 4, 6])
        self.assertEqual(self.panel.new_wall.count(), 8)
        self.assertFalse(self.panel._new_page["hasMore"])
        self.assertEqual(self.panel.new_status.text(), "没有更多了")

    def test_resize_refills_visible_space(self):
        self.fill_until_scrollable()
        before = len(self.core.requests)
        bar = self.panel.new_wall.verticalScrollBar()
        bar.setValue(0)
        self.panel.resize(1500, 1000)
        QTest.qWait(50)
        self.assertTrue(self.runner.pending)
        self.runner.resolve()
        QTest.qWait(50)
        self.assertGreater(len(self.core.requests), before)

    def test_duplicate_pages_stop_automatic_requests_and_can_retry(self):
        self.core.page_size = 1
        self.core.page_count = 10
        original = self.core.new_series

        def repeated(gender="2", offset=0, cursor=""):
            page = original(gender=gender, offset=offset, cursor=cursor)
            page["items"][0]["seriesId"] = "same"
            return page

        self.core.new_series = repeated
        for _ in range(6):
            QTest.qWait(30)
            if not self.runner.pending:
                break
            self.runner.resolve()
        QTest.qWait(50)
        self.assertEqual(len(self.core.requests), 4)
        self.assertEqual(self.panel.new_wall.count(), 1)
        self.assertIn("连续多页没有新剧", self.panel.new_status.text())
        self.assertFalse(self.runner.pending)
        self.panel._retry_new()
        self.assertEqual(len(self.runner.pending), 1)
        self.runner.resolve()
        self.assertEqual(self.core.requests[-1][1], 4)

    def test_rapid_gender_change_discards_stale_result(self):
        self.assertEqual(len(self.runner.pending), 1)
        self.panel._set_gender("0")
        self.assertEqual(len(self.runner.pending), 2)
        self.runner.resolve(0)
        self.assertTrue(self.panel._new_loading)
        self.assertEqual(self.panel.new_wall.count(), 0)
        self.runner.resolve(0)
        QTest.qWait(50)
        self.assertTrue(self.panel._new_items)
        self.assertTrue(all(key.startswith("hongguo:0-") for key in self.panel._new_items))

    def test_failed_page_retries_same_cursor_without_clearing_items(self):
        self.fill_until_scrollable()
        bar = self.panel.new_wall.verticalScrollBar()
        before = self.panel.new_wall.count()
        bar.setValue(bar.maximum())
        QTest.qWait(20)
        self.assertEqual(len(self.runner.pending), 1)
        expected_offset = self.panel._new_page["nextOffset"]
        self.runner.fail()
        self.assertEqual(self.panel.new_wall.count(), before)
        self.assertIn("重试", self.panel.new_status.text())
        self.panel._retry_new()
        self.assertEqual(len(self.runner.pending), 1)
        self.runner.resolve()
        QTest.qWait(50)
        self.assertEqual(self.core.requests[-1], ("2", expected_offset, "session-2"))
        self.assertGreater(self.panel.new_wall.count(), before)

    def test_theme_colors_and_controls_fit_at_narrow_width(self):
        self.runner.resolve()
        self.panel.resize(760, 560)
        QTest.qWait(50)
        buttons = [self.panel.new_tab, self.panel.calendar_tab] + self.panel._gender_group.buttons()
        for name in ("dark", "light"):
            ui.apply_theme(name)
            self.app.setStyleSheet(ui.build_stylesheet())
            self.panel.reload_theme()
            QTest.qWait(40)
            for button in buttons:
                self.assertGreaterEqual(button.width() - 12,
                                        QFontMetrics(button.font()).horizontalAdvance(button.text()),
                                        "%s button %s is clipped" % (name, button.text()))
            for left, right in zip(buttons, buttons[1:]):
                left_pos = left.mapTo(self.panel, QPoint(0, 0))
                right_pos = right.mapTo(self.panel, QPoint(0, 0))
                self.assertLessEqual(left_pos.x() + left.width(), right_pos.x(),
                                     "%s header controls overlap" % name)
            viewport = self.panel.new_wall.viewport().grab().toImage()
            actual = viewport.pixelColor(viewport.width() - 3, viewport.height() - 3)
            self.assertEqual(actual.name(), ui.DESIGN["bg"])
        self.panel._switch_tab("calendar")
        self.runner.resolve(-1)
        QTest.qWait(30)
        for button in self.panel.findChildren(QPushButton):
            if button.isVisible() and button.width() > 0:
                self.assertGreaterEqual(button.width() - 8,
                                        QFontMetrics(button.font()).horizontalAdvance(button.text()))

    def test_themes_update_existing_calendar_rows(self):
        self.runner.resolve()
        self.panel._switch_tab("calendar")
        self.assertTrue(self.runner.pending)
        self.runner.resolve(-1)
        QTest.qWait(50)
        row = self.panel._rows[0]
        position = self.panel.rows_area.verticalScrollBar().value()
        for name, expected in (("light", "#f5f5f8"), ("dark", "#0f1117")):
            ui.apply_theme(name)
            self.app.setStyleSheet(ui.build_stylesheet())
            self.panel.reload_theme()
            QTest.qWait(50)
            image = self.panel.date_area.viewport().grab().toImage()
            self.assertEqual(image.pixelColor(image.width() - 10, 10).name(), expected)
            self.assertEqual(row.property("role"), "calendarRow")
            self.assertEqual(row.reserve_button.property("reserved"), "false")
            self.assertEqual(self.panel.rows_area.verticalScrollBar().value(), position)
            self.assertEqual(self.panel.stack.currentIndex(), 1)
        row.reserve_button.click()
        self.assertTrue(self.watch.is_reserved("hongguo:future"))
        self.assertEqual(row.reserve_button.property("reserved"), "true")
        row.reserve_button.click()
        self.assertFalse(self.watch.is_reserved("hongguo:future"))


if __name__ == "__main__":
    unittest.main()
