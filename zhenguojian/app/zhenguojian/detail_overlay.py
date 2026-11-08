# -*- coding: utf-8 -*-
"""M2：剧集详情的内容区覆盖页（原「剧集详情」页签的形态改造）。

覆盖页挂在主窗口内容区的 QStackedWidget 顶层（page1），页签组整体退到
page0：打开详情不再切页签，返回时底下的页签与滚动位置原样还在。
详情本体（DetailPage，内部用 detail_layout/detail_episodes 拼装的只读引用）
由 main.py 创建后搬进这里托管——本文件不 import 那两个模块，不做任何
内部结构改动，只做 reparent。

样式钩子（QSS 在 ui.py 的 M2 段）：#detailOverlay / #detailOverlayBar /
#detailBackButton / QLabel[role="detailOverlayTitle"]。
"""
from __future__ import annotations

from PyQt5.QtCore import Qt, pyqtSignal
from PyQt5.QtWidgets import (QHBoxLayout, QLabel, QPushButton, QVBoxLayout,
                             QWidget)

BACK_TEXT = "← 返回"   # ← 是字形安全的箭头（U+2190），YaHei/DejaVu 都有


class DetailOverlay(QWidget):
    """返回栏 + 详情本体的容器；backRequested 由主窗口接线负责还原现场。"""

    backRequested = pyqtSignal()

    def __init__(self, detail_widget, parent=None):
        super().__init__(parent)
        self.setObjectName("detailOverlay")
        self.detail = detail_widget

        bar = QWidget(self)
        bar.setObjectName("detailOverlayBar")
        bar.setFixedHeight(44)
        row = QHBoxLayout(bar)
        row.setContentsMargins(8, 6, 16, 6)
        row.setSpacing(10)

        self.back_button = QPushButton(BACK_TEXT, bar)
        self.back_button.setObjectName("detailBackButton")
        self.back_button.setCursor(Qt.PointingHandCursor)
        self.back_button.setToolTip("返回上一页（Esc）")
        self.back_button.clicked.connect(self.backRequested.emit)

        self.bar_title = QLabel("", bar)
        self.bar_title.setProperty("role", "detailOverlayTitle")

        row.addWidget(self.back_button)
        row.addWidget(self.bar_title, 1)
        row.addStretch(1)

        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(0)
        layout.addWidget(bar)
        layout.addWidget(self.detail, 1)      # reparent：详情本体从这里起生效

    def set_title(self, text):
        self.bar_title.setText(str(text or ""))
