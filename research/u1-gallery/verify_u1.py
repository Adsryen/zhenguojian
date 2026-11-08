# -*- coding: utf-8 -*-
"""u1 验证：找剧页「站源筛选行」+「hero 固定高 banner」离屏渲染自测。

跑法：python3 research/u1-gallery/verify_u1.py
产物：本目录 01_filter_source_row.png / 02_source_chip_selected.png /
      03_hero_fixed.png / 04_hero_short_window.png（Mock 数据）
"""
import os
import sys
import time

os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")
os.environ.setdefault("XDG_CONFIG_HOME", "/tmp/u1-xdg")
APP = "/home/ryen/Git/zhenguojian/zhenguojian/app"
sys.path.insert(0, APP)

from PyQt5.QtCore import QSize, Qt
from PyQt5.QtGui import QColor, QFont, QPainter, QPixmap
from PyQt5.QtWidgets import QApplication

from zhenguojian.browse_panel import BrowsePanel, FALLBACK_SOURCES
from zhenguojian.ui import DESIGN, build_stylesheet

OUT = os.path.dirname(os.path.abspath(__file__))

HERO = {
    "id": "hero-001",
    "title": "出租车诡事",
    "description": ("深夜收班的出租车司机接连遇到搭车却无影的乘客，城市边缘的老巷口"
                    "总在零点浮现一列无人站台。他载过的人一个个从城市档案里消失，"
                    "而计价器上的数字，开始倒着走。"),
    "episodes": 24,
    "releaseStatus": "completed",
    "source": "hongguo",
}


def make_drama(i):
    return {
        "id": "drama-%03d" % i,
        "title": "剧集%02d · 深夜便利店" % i,
        "description": "Mock 简介若干。" * 4,
        "episodes": 8 + i % 20,
        "releaseStatus": "ongoing",
        "source": "hongguo",
    }


class FakeCore:
    def __init__(self):
        self.filter_calls = []   # (genre, source)
        self.browse_calls = []   # (filters_snapshot, source)

    def browse_filters(self, genre, source=None):
        self.filter_calls.append((genre, source))
        return {"groups": [
            {"key": "category_dim_theme", "name": "主题", "options": [
                {"id": "urban", "name": "都市"}, {"id": "costume", "name": "古装"},
                {"id": "suspense", "name": "悬疑"}, {"id": "sweet", "name": "甜宠"},
                {"id": "revenge", "name": "复仇"}, {"id": "family", "name": "家庭"}]},
            {"key": "category_dim_role", "name": "角色", "options": [
                {"id": "ceo", "name": "霸总"}, {"id": "heiress", "name": "千金"},
                {"id": "general", "name": "将军"}, {"id": "scholar", "name": "书生"}]},
            {"key": "category_dim_epoch", "name": "年代", "options": [
                {"id": "ancient", "name": "古代"}, {"id": "modern", "name": "现代"}]},
            {"key": "sort", "name": "排序", "options": [
                {"id": "hot", "name": "最热"}, {"id": "new", "name": "最新"}]},
            {"key": "gender", "name": "受众", "options": [
                {"id": "male", "name": "男频"}, {"id": "female", "name": "女频"}]},
            {"key": "creation_status", "name": "状态", "options": [
                {"id": "done", "name": "完结"}, {"id": "ongoing", "name": "连载"}]},
            {"key": "online_time", "name": "上线时间", "options": [
                {"id": "week", "name": "本周"}, {"id": "month", "name": "本月"}]},
        ]}

    def browse_filtered(self, filters, offset=0, cursor="", source=None):
        self.browse_calls.append((dict(filters or {}), source))
        items = [HERO] + [make_drama(i) for i in range(40)]
        return {"items": items, "hasMore": False, "nextOffset": 0}

    def cover(self, drama):
        return {"path": None}

    def poster_metadata(self, batch):
        return {}


def mock_cover_pixmap(label, color):
    pm = QPixmap(300, 420)
    pm.fill(QColor(color))
    painter = QPainter(pm)
    painter.setPen(QColor(DESIGN["text"]))
    font = QFont(); font.setPixelSize(46); font.setBold(True)
    painter.setFont(font)
    painter.drawText(pm.rect(), Qt.AlignCenter, label)
    painter.end()
    return pm


def pump(app, seconds=0.3):
    end = time.time() + seconds
    while time.time() < end:
        app.processEvents()
        time.sleep(0.01)


def wait_loaded(app, panel, timeout=10):
    end = time.time() + timeout
    while time.time() < end:
        app.processEvents()
        time.sleep(0.01)
        if panel.wall.count() > 0 and not panel._loading and not panel._filters_loading:
            pump(app, 0.4)   # 等 _recompute_tile(130ms) 收敛
            return True
    return False


def checked_chip(panel):
    if panel._source_group is None:
        return None
    for button in panel._source_group.buttons():
        if button.isChecked():
            return getattr(button, "_source_slug", None)
    return None


def chip_map(panel):
    return {getattr(b, "_source_slug", None): b for b in panel._source_group.buttons()}


def main():
    app = QApplication(sys.argv)
    app.setStyleSheet(build_stylesheet())
    core = FakeCore()
    panel = BrowsePanel(core)
    panel.resize(1200, 900)
    panel.show()
    assert wait_loaded(app, panel), "mock 数据未加载完成"
    results = []
    def check(name, ok, extra=""):
        results.append((name, ok, extra))
        print(("PASS" if ok else "FAIL"), name, extra)

    # ---- A. 站源芯片行 -------------------------------------------------
    first_row = panel.filter_layout.itemAt(0).widget()
    check("站源行是第一行", first_row is panel._source_row)
    chips = chip_map(panel)
    check("兜底 14 芯片(12 展示源)", len(chips) == len(FALLBACK_SOURCES),
          "got=%d" % len(chips))
    check("初始高亮=红果(默认源)", checked_chip(panel) == "hongguo",
          "got=%r" % checked_chip(panel))
    panel.grab().save(os.path.join(OUT, "01_filter_source_row.png"))

    # 点「黄豆」= 等价 popup 选源
    chips["huangdou"].click()
    pump(app, 0.6)
    wait_loaded(app, panel)
    check("chip 点击→_source=huangdou", panel._source == "huangdou")
    check("chip 高亮跟随", checked_chip(panel) == "huangdou")
    check("browse_filtered 带 source kwarg",
          core.browse_calls and core.browse_calls[-1][1] == "huangdou",
          "last=%r" % (core.browse_calls[-1][1],))
    dirty = [k for k, v in panel._filters.items() if "huangdou" in str(v)]
    check("slug 不进 _filters", not dirty, "dirty=%r" % dirty)
    check("摘要不含站源", panel.filter_summary.text() == "全部剧集",
          "got=%r" % panel.filter_summary.text())
    panel.grab().save(os.path.join(OUT, "02_source_chip_selected.png"))

    # 外部 set_source（SourceSwitchPopup 路径）→ chip 双向同步
    panel.set_source("heitao")
    pump(app, 0.6); wait_loaded(app, panel)
    check("外部 set_source→chip 同步", checked_chip(panel) == "heitao")
    panel._reset_filters()
    pump(app, 0.6); wait_loaded(app, panel)
    check("重置筛选不清站源", panel._source == "heitao" and checked_chip(panel) == "heitao")

    # set_sources 注入 + implemented=False
    injected = [{"slug": "aaa", "name": "源甲"}, {"slug": "bbb", "name": "源乙"},
                {"slug": "ccc", "name": "源丙", "implemented": False}]
    panel.set_sources(injected)
    pump(app, 0.2)
    chips2 = chip_map(panel)
    check("set_sources 注入生效", set(chips2) == {"aaa", "bbb", "ccc"}, "got=%r" % set(chips2))
    check("未接入源禁用", (not chips2["ccc"].isEnabled()))
    check("当前站源不在注入清单则不高亮(不骗人也不偷切源)", checked_chip(panel) is None)
    panel.set_source("bbb")
    pump(app, 0.6); wait_loaded(app, panel)
    check("外部 set_source 注入清单内→chip 同步", checked_chip(panel) == "bbb")
    panel.set_sources(FALLBACK_SOURCES)   # 还原标准 14 源清单
    panel.set_source("hongguo")           # 恢复默认态截图
    pump(app, 0.6); wait_loaded(app, panel)

    # ---- B. hero 固定高 banner ----------------------------------------
    wall = panel.wall
    hero = wall._hero
    pm = mock_cover_pixmap("出租车\n诡事", "#2c3f6b")
    wall.set_pixmap(HERO["id"], pm)
    for i in range(12):
        wall.set_pixmap("drama-%03d" % i, mock_cover_pixmap("%02d" % i, "#26313f"))
    pump(app, 0.5)
    check("hero 固定高 260", hero.height() == 260, "got=%d" % hero.height())
    check("hero sizePolicy 纵向 Fixed", hero.sizePolicy().verticalPolicy() == hero.sizePolicy().Fixed)
    wall_h = wall.viewport().height()
    tile_h = wall.tile_size().height()
    rows = visible_rows(wall)
    print("wall viewport=%d tile=%d rows=%d" % (wall_h, tile_h, rows))
    check("hero 下可见≥2行卡片", rows >= 2, "rows=%d" % rows)
    panel.grab().save(os.path.join(OUT, "03_hero_fixed.png"))
    hero.grab().save(os.path.join(OUT, "03b_hero_zoom.png"))

    # 矮窗口：hero 让位（收缩或隐藏），海报墙必须可见且可滚动
    panel.resize(900, 520)
    pump(app, 0.8)
    hero_h_short = hero.height() if hero.isVisible() else 0
    check("矮窗 hero 让位(≤260，可隐藏)", 0 <= hero_h_short <= 260,
          "got=%d visible=%s" % (hero_h_short, hero.isVisible()))
    rows_short = visible_rows(wall)
    check("矮窗海报墙可见≥1行", rows_short >= 1, "rows=%d" % rows_short)
    sb = wall.verticalScrollBar()
    check("矮窗海报墙可滚动", sb.maximum() > 0, "max=%d" % sb.maximum())
    panel.grab().save(os.path.join(OUT, "04_hero_short_window.png"))

    # 中等窗口：hero 按比例收缩但仍显示
    panel.resize(900, 800)
    pump(app, 0.8)
    hero_h_mid = hero.height() if hero.isVisible() else 0
    rows_mid = visible_rows(wall)
    check("中窗 hero 收缩(140..259)且墙≥1行", 140 <= hero_h_mid < 260 and rows_mid >= 1,
          "hero=%d rows=%d" % (hero_h_mid, rows_mid))
    panel.grab().save(os.path.join(OUT, "04b_hero_mid_window.png"))

    # 收起筛选后再测一次行数（用户常见态）
    panel.resize(1200, 900)
    pump(app, 0.6)
    if not panel._collapsed:
        panel._toggle_filters()
    pump(app, 0.6)
    rows2 = visible_rows(wall)
    check("收起筛选后≥2行", rows2 >= 2, "rows=%d" % rows2)
    panel.grab().save(os.path.join(OUT, "05_hero_collapsed.png"))

    bad = [n for n, ok, _ in results if not ok]
    print("=" * 40)
    print("TOTAL %d checks, FAILED: %s" % (len(results), bad or "none"))
    panel.shutdown()
    return 1 if bad else 0


def visible_rows(wall):
    """视口内（hero 预留带之下）出现的行数：按可见 item 的 top 去重计数。"""
    vp = wall.viewport().height()
    tops = set()
    for i in range(wall.count()):
        r = wall.visualItemRect(wall.item(i))
        if r.isNull() or r.height() <= 0:
            continue
        if r.bottom() <= 0 or r.top() >= vp:
            continue
        tops.add(r.top())
    return len(tops)


if __name__ == "__main__":
    sys.exit(main())
