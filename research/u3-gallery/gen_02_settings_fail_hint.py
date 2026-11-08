#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""u3 冒烟 02：站源开关失败行内提示 + QSettings 暂存退役（settings_panel.py）。

跑法（必须 XDG 隔离，Linux 下 QSettings::setPath 无效，别污染真实 ~/.config）：
    rm -rf /tmp/u3-xdg && mkdir -p /tmp/u3-xdg
    XDG_CONFIG_HOME=/tmp/u3-xdg QT_QPA_PLATFORM=offscreen \
        python3 research/u3-gallery/gen_02_settings_fail_hint.py

验证点：
  1. FakeCore 成功态：翻转生效、发 sourceToggled、无红字；
  2. FakeCore 抛异常（模拟 CoreError）：不发信号、开关回滚、行内红字显示错误；
  3. FakeCore 返回 dict error（{"error": {...}}）：同上；
  4. 恢复成功：红字清除、信号恢复；
  5. core=None 兜底：FALLBACK_SOURCES 14 行仍展示，切换行内提示不保存；
  6. 全程 QSettings("zhenguojian","zhenguojian") 无 sources/enabled 键
     （allKeys + 原始 ini 字节双查）。
输出 02_settings_fail_hint.png（失败红字行）。
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
sys.path.insert(0, os.path.join(REPO, "zhenguojian", "app"))

from PyQt5.QtTest import QTest
from PyQt5.QtWidgets import QApplication

from zhenguojian import ui
from zhenguojian.settings_panel import SettingsPanel

RESULTS = []


def check(name, ok, detail=""):
    RESULTS.append((name, bool(ok), detail))
    print("%s %s%s" % ("PASS" if ok else "FAIL", name,
                       ("  [%s]" % detail) if detail else ""))
    return ok


ROWS_OK = [
    {"slug": "hongguo", "name": "红果短剧", "implemented": True, "enabled": True},
    {"slug": "huangdou", "name": "黄逗短剧", "implemented": True, "enabled": False},
    {"slug": "jinguo", "name": "金果短剧", "implemented": True, "enabled": True},
    {"slug": "hanxiaoquan", "name": "寒宵圈", "implemented": False, "enabled": False},
]


class FakeCore(object):
    """mode: ok | raise | dict_error"""

    def __init__(self, mode="ok"):
        self.mode = mode
        self.calls = []

    def list_sources(self):
        return [dict(row) for row in ROWS_OK]

    def set_source_enabled(self, slug, enabled):
        self.calls.append((slug, enabled))
        if self.mode == "raise":
            raise RuntimeError("核心拒绝：source 未注册")
        if self.mode == "dict_error":
            return {"error": {"message": "写入 source_prefs.json 失败"}}
        return {"ok": True}


def main():
    assert os.environ.get("XDG_CONFIG_HOME") == "/tmp/u3-xdg", \
        "必须 XDG 隔离运行（见 docstring）"
    app = QApplication(sys.argv)
    ui.apply_theme("dark")
    app.setStyleSheet(ui.build_stylesheet())

    from PyQt5.QtCore import QSettings
    probe = QSettings("zhenguojian", "zhenguojian")
    check("QSettings 已隔离到 XDG 沙箱",
          probe.fileName().startswith("/tmp/u3-xdg"), probe.fileName())
    del probe

    events = []

    # --- 1) 成功态 ---
    from PyQt5.QtWidgets import QMainWindow
    core = FakeCore("ok")
    panel = SettingsPanel(core)
    panel.sourceToggled.connect(lambda slug, en: events.append((slug, en)))
    # 与 main.py 相同：面板挂 QMainWindow，全局 QSS 的窗口背景才生效
    win = QMainWindow()
    win.setCentralWidget(panel)
    win.resize(560, 760)
    win.show()
    QTest.qWait(150)
    row = panel._rows["hongguo"]
    check("列表来自 core（4 行）", len(panel._rows) == 4, str(sorted(panel._rows)))
    check("初始开关跟随 enabled", row.switch.isChecked())
    row.switch.click()                       # 关 → False
    QTest.qWait(50)
    check("成功：不发回滚，开关保持关", not row.switch.isChecked())
    check("成功：发 sourceToggled", events == [("hongguo", False)], str(events))
    check("成功：无行内红字", not row.error.isVisible(), row.error.text())
    check("成功：提示写明已同步", "已同步到本地核心" in panel._sources_hint.text(),
          panel._sources_hint.text())

    # --- 2) 异常失败态 ---
    events[:] = []
    core.mode = "raise"
    row.switch.click()                       # 尝试开 → True，核心抛错
    QTest.qWait(50)
    check("失败：不发信号", events == [], str(events))
    check("失败：开关回滚为原状态(关)", not row.switch.isChecked())
    check("失败：行内红字可见", row.error.isVisible(), row.error.text())
    check("失败：红字含核心消息", "source 未注册" in row.error.text(),
          row.error.text())

    # --- 3) dict error 失败态（另一形态） ---
    core.mode = "dict_error"
    row2 = panel._rows["jinguo"]
    row2.switch.click()                      # 关 → 尝试，核心返回 dict error
    QTest.qWait(50)
    check("dict error：不发信号", events == [], str(events))
    check("dict error：开关回滚(仍开)", row2.switch.isChecked())
    check("dict error：红字显示", row2.error.isVisible()
          and "source_prefs.json" in row2.error.text(), row2.error.text())

    # --- 4) 恢复成功清红字 ---
    core.mode = "ok"
    row.switch.click()                       # 再尝试开 → 成功
    QTest.qWait(50)
    check("恢复：信号发出", events == [("hongguo", True)], str(events))
    check("恢复：红字清除", not row.error.isVisible())
    check("恢复：开关保持开", row.switch.isChecked())

    # --- 5) core=None 展示兜底 ---
    panel2 = SettingsPanel(None)
    events2 = []
    panel2.sourceToggled.connect(lambda slug, en: events2.append((slug, en)))
    panel2.show()
    QTest.qWait(120)
    check("兜底：FALLBACK_SOURCES 14 行", len(panel2._rows) == 14,
          str(len(panel2._rows)))
    fb = panel2._rows.get("hongguo")
    fb.switch.click()
    QTest.qWait(50)
    check("兜底：切换不发信号", events2 == [], str(events2))
    check("兜底：行内提示不保存", fb.error.isVisible()
          and "未连接" in fb.error.text(), fb.error.text())

    # --- 6) QSettings 零写入核查 ---
    from PyQt5.QtCore import QSettings as QS
    keys = QS("zhenguojian", "zhenguojian").allKeys()
    check("QSettings 无 sources/enabled 键", "sources/enabled" not in keys,
          str(keys))
    ini = QS("zhenguojian", "zhenguojian").fileName()
    if os.path.exists(ini):
        with open(ini, "rb") as fh:
            blob = fh.read()
        check("原始 ini 无 sources/enabled 字节", b"sources/enabled" not in blob,
              ini)
    else:
        check("原始 ini 无 sources/enabled 字节", True, "ini 文件未生成（零写入）")

    # --- 截图：失败红字行 ---
    core.mode = "raise"
    panel._rows["huangdou"].switch.click()   # 黄逗开→失败，留一行红字
    QTest.qWait(50)
    panel._rows["hongguo"].switch.click()    # 红果再失败一次（两行红字）
    QTest.qWait(50)
    panel.raise_()
    pix = win.grab()
    out = os.path.join(HERE, "02_settings_fail_hint.png")
    pix.save(out)
    check("PNG 已保存", os.path.getsize(out) > 8000, out)

    failed = [n for n, ok, _ in RESULTS if not ok]
    print("\n%d/%d 通过" % (len(RESULTS) - len(failed), len(RESULTS)))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
