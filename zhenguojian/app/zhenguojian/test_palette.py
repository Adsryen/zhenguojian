# -*- coding: utf-8 -*-
"""调色板自检：每个颜色 token 都必须是 QColor 认得的格式。

踩过的坑：``accent_soft`` 写成 ``rgba(255, 59, 92, .14)``，PyQt5 的
``QColor`` 不认这种带空格和省略号的 rgba()，直接解析成纯黑 —— 点选卡片、
选中下载任务、正在播的那一集芯片，底色全变成黑块。QSS 能认 rgba()，
所以同一个 token 在 QSS 里正常、在自绘控件里发黑，很难查。

现在统一用 8 位十六进制 ``#AARRGGBB``，两种路径都对。
"""
from __future__ import annotations

import unittest

from PyQt5.QtGui import QColor

from . import ui


class PaletteTest(unittest.TestCase):
    def test_every_color_token_is_valid(self):
        for name, palette in (("dark", ui.DARK), ("light", ui.LIGHT)):
            for key, value in palette.items():
                if key in ("radius", "radius_small"):
                    continue
                color = QColor(value)
                self.assertTrue(color.isValid(),
                                "%s 调色板的 %s = %r 不是合法颜色" % (name, key, value))

    def test_both_palettes_share_keys(self):
        self.assertEqual(sorted(ui.DARK), sorted(ui.LIGHT))

    def test_soft_accents_are_translucent(self):
        """淡色底必须真的半透明，否则选中态会是一整块实色。"""
        for name, palette in (("dark", ui.DARK), ("light", ui.LIGHT)):
            for key in ("accent_soft", "selected_soft"):
                color = QColor(palette[key])
                self.assertLess(color.alpha(), 255,
                                "%s 的 %s 是全不透明（alpha=%d）" % (name, key, color.alpha()))
                self.assertGreater(color.alpha(), 0,
                                   "%s 的 %s 完全透明（alpha=%d）" % (name, key, color.alpha()))

    def test_brand_colour_matches_the_reference_client(self):
        """红果客户端的品牌色是 #ff3b5c（深色）/ #fe2c55（浅色）。"""
        self.assertEqual(ui.DARK["accent"], "#ff3b5c")
        self.assertEqual(ui.LIGHT["accent"], "#fe2c55")


if __name__ == "__main__":
    unittest.main()
