# -*- coding: utf-8 -*-
"""F2 公共控件离屏样例页（widgets_guo_gallery.py）

用 Mock 数据实例化 widgets_guo 的 5 个控件，QT_QPA_PLATFORM=offscreen 下
grab() 出 PNG 到 research/f2-gallery/，供人工/AI 视觉验收。

运行：
    QT_QPA_PLATFORM=offscreen python3 zhenguojian/app/zhenguojian/widgets_guo_gallery.py

页面全部为「示例数据」（每页右下角有灰色小字标注），不接任何真实数据。
"""

import os
import sys

try:  # 包内导入优先；直接脚本运行时回退到 sys.path 引导
    from . import ui, widgets_guo
except ImportError:  # pragma: no cover
    _APP = os.path.dirname(os.path.dirname(
        os.path.abspath(__file__)))  # <repo>/zhenguojian/app
    if _APP not in sys.path:
        sys.path.insert(0, _APP)
    from zhenguojian import ui, widgets_guo

from PyQt5.QtCore import QPoint, Qt
from PyQt5.QtGui import QColor, QFont, QLinearGradient, QPainter, QPixmap
from PyQt5.QtWidgets import (QApplication, QHBoxLayout, QLabel,
                             QPushButton, QVBoxLayout, QWidget)

_HERE = os.path.dirname(os.path.abspath(__file__))
# widgets_guo_gallery.py 位于 <repo>/zhenguojian/app/zhenguojian/，
# 上溯三级即仓库根；输出固定在 <repo>/research/f2-gallery/。
_REPO = os.path.abspath(os.path.join(_HERE, "..", "..", ".."))
OUT_DIR = os.path.join(_REPO, "research", "f2-gallery")

Titles = [
    "总裁的替嫁新娘", "穿越后我成了恶毒女配", "赘医", "暗夜追凶",
    "闪婚厚爱", "龙王殿", "她的小甜妻", "都市医仙", "重生嫡女",
]


def mock_cover(w, h, i, seed=0):
    """纯色+斜向渐变的假海报，带大号序号，方便肉眼区分。"""
    pm = QPixmap(w, h)
    hues = [210, 340, 25, 160, 280, 45, 190, 120, 0]
    hue = hues[(i + seed) % len(hues)]
    c1 = QColor.fromHsl(hue, 55, 62)
    c2 = QColor.fromHsl((hue + 40) % 360, 60, 40)
    pm.fill(c1)
    p = QPainter(pm)
    g = QLinearGradient(0, 0, w, h)
    g.setColorAt(0.0, c1)
    g.setColorAt(1.0, c2)
    p.fillRect(pm.rect(), g)
    p.setPen(QColor(255, 255, 255, 36))
    f = QFont("Microsoft YaHei", int(h / 3.2))
    f.setBold(True)
    p.setFont(f)
    p.drawText(pm.rect(), Qt.AlignCenter, str(i + 1))
    p.end()
    return pm


def page(title_text, size=(860, 560)):
    """带标题和水印的样例页容器（深色底：palette 走 DESIGN["bg"]，
    避免继承 QWidget 浅灰底让观感失真）。"""
    w = QWidget()
    w.setAutoFillBackground(True)
    pal = w.palette()
    pal.setColor(w.backgroundRole(), QColor(ui.DESIGN["bg"]))
    w.setPalette(pal)
    w.resize(*size)
    lay = QVBoxLayout(w)
    lay.setContentsMargins(24, 18, 24, 18)
    lay.setSpacing(10)
    head = QLabel(title_text)
    head.setStyleSheet("color:%s;font-size:15px;font-weight:bold;"
                       % ui.DESIGN["guo_accent"])
    lay.addWidget(head)
    body = QVBoxLayout()
    body.setSpacing(8)
    lay.addLayout(body, 1)
    # 右下角「示例数据」灰色小字水印
    mark = QLabel("示例数据", w)
    mark.setStyleSheet("color:%s;font-size:11px;background:transparent;"
                       % ui.DESIGN["guo_muted"])
    mark.adjustSize()
    w._body = body
    w._mark = mark
    w._size = size
    return w


def force_state(btn, hover=None, pressed=None):
    """离屏 grab 不触发 :hover/:pressed 伪类，用 dynamic property 强制态，
    与 ui.py 的 [hover=\"true\"]/… 属性选择器配套。"""
    if hover:
        btn.setProperty("hover", "true")
    if pressed:
        btn.setProperty("pressed", "true")
    btn.style().unpolish(btn)
    btn.style().polish(btn)


def finish(win, path):
    win._mark.move(win._size[0] - win._mark.width() - 10,
                   win._size[1] - win._mark.height() - 8)
    win._mark.raise_()
    QApplication.processEvents()
    win.grab().save(path)
    print("saved", path)


# ---------------------------------------------------------------- 01 海报卡
def shot_poster(app):
    win = page("PosterCard — 榜单名次角标 / 更新角标 / VIP / 已看置灰",
               (860, 330))
    row = QHBoxLayout()
    row.setSpacing(14)
    win._body.addLayout(row)
    specs = [
        dict(badge_rank=1),
        dict(badge_rank=2, vip=True),
        dict(badge_rank=3, update_badge=18),
        dict(update_badge=32, vip=True),
        dict(),
        dict(badge_rank=7, update_badge=5),
        dict(update_badge=48),
        dict(badge_rank=10),
    ]
    for i, kw in enumerate(specs):
        card = widgets_guo.PosterCard()
        card.set_data(Titles[i % len(Titles)], mock_cover(100, 140, i), **kw)
        row.addWidget(card)
    # 已看态（置灰）
    seen = widgets_guo.PosterCard()
    seen.set_data("已看过的剧", mock_cover(100, 140, 8), update_badge=90)
    seen.set_selected(True)
    row.addWidget(seen)
    win.show()
    finish(win, os.path.join(OUT_DIR, "01_poster_card.png"))


# ---------------------------------------------------------- 02 模糊封面背景
def shot_blur(app):
    win = page("BlurCoverBackground — 封面 8x 降采样模糊铺底 + 60% 黑遮罩")
    bg = widgets_guo.BlurCoverBackground()
    bg.set_cover(mock_cover(320, 448, 1, seed=3))
    overlay = QVBoxLayout(bg)
    overlay.setContentsMargins(28, 0, 28, 24)
    overlay.addStretch(1)
    t = QLabel("暗夜追凶")
    t.setStyleSheet("color:%s;font-size:26px;font-weight:bold;"
                    % ui.DESIGN["guo_text"])
    s = QLabel("第 3 季 · 更新至 24 集 · 悬疑短剧 · 示例简介文字，说明遮罩下文字可读性。")
    s.setStyleSheet("color:%s;font-size:13px;" % ui.DESIGN["guo_muted"])
    overlay.addWidget(t)
    overlay.addWidget(s)
    win._body.addWidget(bg, 1)
    win.show()
    finish(win, os.path.join(OUT_DIR, "02_blur_cover.png"))


# ------------------------------------------------------------- 03 选集网格
def shot_episodes(app):
    win = page("EpisodeGrid — 30 集分组 chip / 当前集高亮 / 已看置浅 / VIP 黄点")
    grid = widgets_guo.EpisodeGrid()
    eps = []
    for i in range(1, 97):
        eps.append(dict(index=i, watched=i <= 42, vip=(61 <= i <= 70),
                        current=(i == 63)))
    grid.set_episodes(eps)
    win._body.addWidget(grid, 1)
    win.show()
    finish(win, os.path.join(OUT_DIR, "03_episode_grid.png"))


# ------------------------------------------------------------- 04 站源切换
def shot_source(app):
    win = page("SourceSwitchPopup — 按钮态 + 弹出菜单（当前高亮/可用性勾选）")
    bar = QHBoxLayout()
    win._body.addLayout(bar)
    sw = widgets_guo.SourceSwitchPopup()
    sw.set_sources([
        dict(slug="hl", name="红果短剧", enabled=True),
        dict(slug="tx", name="天狼短视频", enabled=True),
        dict(slug="qm", name="奇米免费短剧", enabled=False),
        dict(slug="xx", name="小星剧场（停用）", enabled=False),
    ])
    sw.set_current("tx")
    bar.addWidget(sw)
    sw2 = widgets_guo.SourceSwitchPopup()
    sw2.set_sources([dict(slug="only", name="唯一站源")])
    sw2.setEnabled(False)
    bar.addWidget(sw2)
    bar.addStretch(1)
    win._body.addStretch(1)
    win.show()
    menu = sw.build_menu()
    menu.adjustSize()
    pm = menu.grab()
    sw.adjustSize()
    bp = sw.button.grab()
    combo = QPixmap(max(pm.width(), bp.width()) + 32, pm.height() + bp.height() + 48)
    combo.fill(QColor(ui.DESIGN["bg"]))
    p = QPainter(combo)
    p.drawPixmap(16, 16, bp)
    p.drawPixmap(16, 16 + bp.height() + 16, pm)
    p.end()
    combo.save(os.path.join(OUT_DIR, "04_source_switch.png"))
    print("saved", os.path.join(OUT_DIR, "04_source_switch.png"),
          "(button+menu)")


# ----------------------------------------------------------- 05 分组列表
def shot_sections(app):
    win = page("SectionedListView — 今天/昨天/更早 分组头 + 调用方 item 工厂")
    view = widgets_guo.SectionedListView()

    def make_row(text, sub):
        row = QPushButton(text + "　·　" + sub)
        row.setCursor(Qt.PointingHandCursor)
        row.setProperty("guoRole", "groupTab")
        row.setLayoutDirection(Qt.LeftToRight)
        row.setStyleSheet("text-align:left;padding:8px 12px;")
        return row

    view.set_sections([
        ("今天", lambda: [make_row("总裁的替嫁新娘", "看到 12/80"),
                         make_row("赘医", "看到 3/24")]),
        ("昨天", lambda: [make_row("闪婚厚爱", "看到 45/60")]),
        ("更早", lambda: [make_row("都市医仙", "看到 7/90"),
                          make_row("龙王殿", "看到 88/90"),
                          make_row("她的小甜妻", "看到 1/30")]),
    ])
    win._body.addWidget(view, 1)
    win.show()
    finish(win, os.path.join(OUT_DIR, "05_sectioned_list.png"))


# --------------------------------------------------------------- 06 四态
def shot_states(app):
    win = page("四态对比 — normal / hover / pressed / disabled（自上而下）",
               (860, 480))
    grid = QHBoxLayout()
    grid.setSpacing(18)
    win._body.addLayout(grid)

    def column(caption, builder):
        col = QVBoxLayout()
        grid.addLayout(col)
        head = QLabel(caption)
        head.setStyleSheet("color:%s;font-size:12px;" % ui.DESIGN["guo_muted"])
        col.addWidget(head)
        builder(col)
        col.addStretch(1)

    def poster_col(col):
        for state in ("normal", "hover", "pressed", "disabled"):
            c = widgets_guo.PosterCard()
            c.set_data("状态演示标题", mock_cover(100, 140, 4),
                       update_badge=22)
            if state == "disabled":
                c.setEnabled(False)
            elif state == "normal":
                c.preview_state(hover=False, pressed=False)
            else:
                c.preview_state(**{state: True})
            col.addWidget(c)

    def buttons_col(col):
        ep = QPushButton("23")
        ep.setProperty("guoRole", "episode")
        ep.setFixedSize(64, 34)
        force_state(ep, hover=True)
        col.addWidget(ep)
        ep2 = QPushButton("24")
        ep2.setProperty("guoRole", "episode")
        ep2.setFixedSize(64, 34)
        force_state(ep2, pressed=True)
        col.addWidget(ep2)
        ep3 = QPushButton("25")
        ep3.setProperty("guoRole", "episode")
        ep3.setFixedSize(64, 34)
        ep3.setEnabled(False)
        col.addWidget(ep3)
        tab = QPushButton("31-60")
        tab.setProperty("guoRole", "groupTab")
        tab.setCheckable(True)
        tab.setChecked(True)
        tab.setMinimumWidth(90)
        col.addWidget(tab, 0, Qt.AlignLeft)
        tab2 = QPushButton("61-90")
        tab2.setProperty("guoRole", "groupTab")
        force_state(tab2, hover=True)
        tab2.setMinimumWidth(90)
        col.addWidget(tab2, 0, Qt.AlignLeft)
        src = QPushButton("天狼短视频  ▾")
        src.setProperty("guoRole", "sourceSwitch")
        force_state(src, hover=True)
        src.setMinimumWidth(150)
        col.addWidget(src, 0, Qt.AlignLeft)
        src2 = QPushButton("小星剧场  ▾")
        src2.setProperty("guoRole", "sourceSwitch")
        src2.setEnabled(False)
        src2.setMinimumWidth(150)
        col.addWidget(src2, 0, Qt.AlignLeft)

    def overlay_col(col):
        holder = QWidget()
        h = QHBoxLayout(holder)
        h.setContentsMargins(0, 0, 0, 0)
        for state in ("normal", "hover", "pressed", "disabled"):
            b = QPushButton(state)
            b.setProperty("guoRole", "ctrlBtn")
            if state == "disabled":
                b.setEnabled(False)
            elif state != "normal":
                force_state(b, **{state: True})
            h.addWidget(b)
        col.addWidget(holder)
        lab = QLabel("controlOverlay / ctrlBtn 浮层按钮")
        lab.setStyleSheet("color:%s;font-size:11px;" % ui.DESIGN["guo_muted"])
        col.addWidget(lab)

    column("PosterCard 海报卡", poster_col)
    column("episode / groupTab / sourceSwitch", buttons_col)
    column("ctrlBtn", overlay_col)
    win.show()
    finish(win, os.path.join(OUT_DIR, "06_states.png"))


def main():
    os.makedirs(OUT_DIR, exist_ok=True)
    app = QApplication.instance() or QApplication(sys.argv)
    app.setStyle("Fusion")
    ui.apply_theme("dark")
    app.setStyleSheet(ui.build_stylesheet())
    shot_poster(app)
    shot_blur(app)
    shot_episodes(app)
    shot_source(app)
    shot_sections(app)
    shot_states(app)
    print("gallery done ->", OUT_DIR)
    for name in sorted(os.listdir(OUT_DIR)):
        print(" ", name, os.path.getsize(os.path.join(OUT_DIR, name)))


if __name__ == "__main__":
    main()
