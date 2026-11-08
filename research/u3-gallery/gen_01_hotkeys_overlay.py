#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""u3 冒烟 01：播放器快捷键说明浮层（player.py P1e）。

跑法（XDG 隔离防污染真实 ~/.config/zhenguojian，player 构造期会读偏好键）：
    XDG_CONFIG_HOME=/tmp/u3-xdg QT_QPA_PLATFORM=offscreen \
        python3 research/u3-gallery/gen_01_hotkeys_overlay.py

注意两个 offscreen 测试语义（先例见 /tmp 探针）：
  * QShortcut(QKeySequence(Qt.Key_Question)) 匹配的是「过滤后」按键形态
    （key=Key_Question、modifiers=NoModifier）——真实 X11/Windows 键盘在
    Shift+/ 时会把隐式 Shift 折进键码，合成事件须按此形态发。
  * 焦点锚点用普通 StrongFocus QWidget：PlayerPane 默认焦点若落在
    QSlider（QAbstractSlider）上，_player_shortcuts_allowed 门会整体拦下，
    那是门本身的行为，不是浮层的 bug。

验证点：
  1. ?（Shift+/）WindowShortcut 打开浮层；浮层持焦时再按 ?/Esc 关闭；
  2. 浮层开着时 Esc 优先被浮层的 WidgetWithChildrenShortcut 吸收，
     不触发 main.py 的窗口级 Esc 退全屏（镜像绑定的计数器证明）；
  3. 空格/←→ 仍只有窗口级一个响应者（player 刻意不重复注册，防双触发）；
  4. QLineEdit 聚焦时 ? 被门拦住；
  5. ⌨ 按钮 click 打开/关闭；
  6. 自动收起：生产常量 8000ms；测试内临时 300ms 证明计时链路；
  7. ↑/F 原有快捷键不受影响。
输出 01_hotkeys_overlay.png（暗色主题，浮层+悬停控制条同框）。
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
sys.path.insert(0, os.path.join(REPO, "zhenguojian", "app"))

from PyQt5.QtCore import QEvent, Qt
from PyQt5.QtGui import QKeyEvent, QKeySequence
from PyQt5.QtTest import QTest
from PyQt5.QtWidgets import (QApplication, QLineEdit, QMainWindow, QWidget,
                             QShortcut)

from zhenguojian import player as player_mod
from zhenguojian import ui
from zhenguojian.player import PlayerPane

RESULTS = []


def check(name, ok, detail=""):
    RESULTS.append((name, bool(ok), detail))
    print("%s %s%s" % ("PASS" if ok else "FAIL", name,
                       ("  [%s]" % detail) if detail else ""))
    return ok


def main():
    assert os.environ.get("XDG_CONFIG_HOME") == "/tmp/u3-xdg", \
        "必须 XDG 隔离运行（见 docstring）"
    ui.apply_theme("dark")
    app = QApplication(sys.argv)
    app.setStyleSheet(ui.build_stylesheet())

    # 镜像 main.py 的窗口级绑定（不动 main.py 本体）：Esc 退全屏 / 空格暂停 / ←→ 快进退
    esc_hits = []
    space_hits = []
    seek_hits = []

    class Window(QMainWindow):
        def __init__(self):
            super().__init__()
            self.pane = PlayerPane()
            self.setCentralWidget(self.pane)
            for seq, slot in ((Qt.Key_Escape, lambda: esc_hits.append(1)),
                              (Qt.Key_Space, lambda: space_hits.append(1)),
                              (Qt.Key_Left, lambda: seek_hits.append(-5)),
                              (Qt.Key_Right, lambda: seek_hits.append(5))):
                s = QShortcut(QKeySequence(seq), self)
                s.setContext(Qt.WindowShortcut)
                s.activated.connect(slot)

    window = Window()
    window.resize(1200, 680)
    window.show()
    QTest.qWaitForWindowExposed(window)
    QTest.qWait(150)

    pane = window.pane
    overlay = pane.hotkeys_overlay
    check("浮层初始隐藏", not overlay.isVisible())

    # 焦点锚点：普通 QWidget（不在输入控件黑名单），保证门放行
    anchor = QWidget(pane.stage)
    anchor.setFocusPolicy(Qt.StrongFocus)
    anchor.setGeometry(0, 0, 1, 1)
    anchor.show()

    def press(key, mod=Qt.NoModifier, text="", target=None):
        target = target or QApplication.focusWidget() or anchor
        ev = QKeyEvent(QEvent.KeyPress, key, mod, text)
        QApplication.sendEvent(target, ev)
        app.processEvents()
        return ev

    def press_question(target=None):
        # 真实 Shift+/ 经平台过滤后即 (Key_Question, NoModifier)
        return press(Qt.Key_Question, Qt.NoModifier, "?", target=target)

    def open_via_key():
        anchor.setFocus()
        app.processEvents()
        press_question(target=anchor)
        return overlay.isVisible()

    def force_open():
        # 开浮层并保证「关掉旧的再开」，不受 toggle 语义影响
        overlay.hide()
        app.processEvents()
        pane.show_hotkey_help()
        app.processEvents()
        return overlay.isVisible()

    # 1) ? 打开浮层；浮层持焦时再按 ? 由本浮层关闭（快捷键开合）
    check("? 打开浮层", open_via_key())
    check("浮层铺满画面区",
          overlay.geometry() == pane.stage.rect(),
          "%s vs %s" % (overlay.geometry().getRect(), pane.stage.rect().getRect()))
    check("浮层持有焦点（Esc 优先归它）", QApplication.focusWidget() is overlay)
    press_question()
    check("持焦再按 ? 关闭", not overlay.isVisible())

    # 2) Esc 冲突处理：浮层开着时 Esc 只关浮层，不触发窗口级退全屏
    check("? 再次打开", open_via_key() or force_open())
    esc_before = len(esc_hits)
    press(Qt.Key_Escape)                      # 焦点在浮层 → Esc 归本帮助
    check("Esc 关闭浮层", not overlay.isVisible())
    check("浮层期间窗口级 Esc 未触发", len(esc_hits) == esc_before,
          "esc_hits=%d" % len(esc_hits))
    press(Qt.Key_Escape, target=anchor)       # 浮层收起后 Esc 回到窗口级
    check("浮层收起后窗口级 Esc 恢复可达", len(esc_hits) == esc_before + 1,
          "esc_hits=%d" % len(esc_hits))

    # 3) 空格/←→ 仍只有窗口级响应（player 未注册，防双触发验证）
    press(Qt.Key_Space, target=anchor)
    check("空格单一响应者（1 次）", len(space_hits) == 1,
          "space_hits=%d" % len(space_hits))
    press(Qt.Key_Right, target=anchor)
    press(Qt.Key_Left, target=anchor)
    check("←/→ 单一响应者", seek_hits == [5, -5], str(seek_hits))

    # 4) 输入控件聚焦门：QLineEdit 聚焦时 ? 失效
    edit = QLineEdit(pane.stage)
    edit.setGeometry(4, 4, 180, 28)
    edit.show()
    edit.setFocus()
    app.processEvents()
    press_question(target=edit)
    check("QLineEdit 聚焦时 ? 被门拦住", not overlay.isVisible())
    edit.close()
    edit.deleteLater()

    # 5) ⌨ 按钮开合（悬停控制条上的入口）。先回锚点焦点：真实点击会先把
    #    焦点给按钮（门放行），程序化 click() 不改焦点，这里手动对齐语义。
    anchor.setFocus()
    app.processEvents()
    pane.hover_activity()
    app.processEvents()
    btn = pane.overlay.hotkey_button
    check("悬停控制条显示且含 ⌨ 按钮",
          pane.overlay.isVisible() and btn.isVisible() and btn.text() == "?")
    btn.click()
    check("⌨ 点击打开", overlay.isVisible())
    btn.click()
    check("⌨ 点击关闭（toggle）", not overlay.isVisible())

    # 6) 自动收起机制 + 生产常量
    check("自动收起常量 8000ms",
          player_mod.HOTKEY_HELP_AUTO_HIDE_MS == 8000,
          "实际 %d" % player_mod.HOTKEY_HELP_AUTO_HIDE_MS)
    pane.show_hotkey_help()
    overlay._auto_hide.setInterval(300)   # 仅测试内临时改，证明计时链路
    QTest.qWait(500)
    check("到时无操作自动收起", not overlay.isVisible())

    # 7) ↑/F 原快捷键仍工作（浮层改动没有吃掉绑定）。音量从 50 起测，
    #    避免默认 100 时 +5 被 clamp 掩盖。
    pane.set_volume(50)
    app.processEvents()
    vol0 = pane.volume()
    press(Qt.Key_Up, target=anchor)
    check("↑ 音量 +5 仍生效", pane.volume() == vol0 + 5,
          "%s → %s" % (vol0, pane.volume()))
    press(Qt.Key_Down, target=anchor)
    check("↓ 音量 -5 仍生效", pane.volume() == vol0,
          "回落后 %s，期望 %s" % (pane.volume(), vol0))
    fs0 = pane._fullscreen
    press(Qt.Key_F, target=anchor)
    check("F 全屏翻转仍生效", pane._fullscreen != fs0)
    pane.set_fullscreen(False)
    app.processEvents()

    # 截图：浮层 + 底部悬停控制条同框（恢复 8s 生产值再拍，杜绝竞态收起）
    overlay._auto_hide.setInterval(player_mod.HOTKEY_HELP_AUTO_HIDE_MS)
    pane.hover_activity()
    pane.show_hotkey_help()
    QTest.qWait(150)
    pix = window.grab()
    out = os.path.join(HERE, "01_hotkeys_overlay.png")
    pix.save(out)
    check("PNG 已保存", os.path.getsize(out) > 10000, out)

    failed = [n for n, ok, _ in RESULTS if not ok]
    print("\n%d/%d 通过" % (len(RESULTS) - len(failed), len(RESULTS)))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
