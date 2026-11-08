# -*- coding: utf-8 -*-
"""M2：独立非模态播放窗口（原「在线播放」页签的形态改造）。

形态约定（PRD 11-08-m2-dialog-nav）：
- 顶层窗口（Qt.Window），非模态，可缩放；主窗口被它盖住的页面照常可操作。
- PlayerPane 的生命周期**不在这里**：画面/控制/选集停靠栏等部件由 main.py
  创建并接线，这里只负责把它们装进窗口并管显示/关闭。
- 关窗 = 记几何 → 请 main 停止播放（stop_playback 走既有的停 mpv + release
  路径）→ hide。窗口与播放器对象都不销毁，下次播放直接复用。
- 几何记忆：QSettings("zhenguojian","zhenguojian") 的 "player/geometry"，
  saveGeometry/restoreGeometry 往返；全屏状态与最大化状态不会写坏记忆
  （全屏进行中不保存，退出全屏后的正常尺寸才入档）。
"""
from __future__ import annotations

from PyQt5.QtCore import QByteArray, Qt
from PyQt5.QtWidgets import QDialog, QVBoxLayout

SETTINGS_ORG = "zhenguojian"          # 与 main.py / player.py 的 QSettings 约定一致
SETTINGS_APP = "zhenguojian"
GEOMETRY_KEY = "player/geometry"
DEFAULT_TITLE = "在线播放"


class PlaybackDialog(QDialog):
    """播放窗宿主。main_window 需提供 on_playback_window_close()。"""

    def __init__(self, main_window, page_widget, parent=None):
        super().__init__(parent, Qt.Window)          # 独立顶层窗口，非模态
        self.setObjectName("playbackDialog")
        self.setModal(False)
        self.setAttribute(Qt.WA_DeleteOnClose, False)
        self._main = main_window
        self._page = page_widget
        self.setWindowTitle(DEFAULT_TITLE)
        self.resize(1080, 640)                        # 首次打开的兜底尺寸

        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.addWidget(page_widget)                 # 整块播放页（画面+侧栏）搬进窗口

    # ------------------------------------------------------------------ 显示
    def open_for(self, title_text=None):
        """播放入口统一调这里：设标题（剧名 - 第N集），必要时恢复上次几何。"""
        self.setWindowTitle(title_text or DEFAULT_TITLE)
        if not self.isVisible():
            self._restore_geometry()
            self.show()
        # 已在显示中就不动几何（可能正被用户拖着走）；只把它提到前面。
        self.raise_()
        self.activateWindow()

    def _settings(self):
        from PyQt5.QtCore import QSettings
        return QSettings(SETTINGS_ORG, SETTINGS_APP)

    def _restore_geometry(self):
        try:
            raw = self._settings().value(GEOMETRY_KEY)
        except Exception:
            raw = None
        if raw is None:
            return
        try:
            # QSettings 对 QByteArray 一般原样回读；个别平台会落成 base64 字符串，
            # 两种形态都喂给 restoreGeometry（它接受 QByteArray）。
            if isinstance(raw, str):
                raw = QByteArray.fromBase64(raw.encode("ascii"))
            self.restoreGeometry(raw)
        except Exception:
            pass

    def _save_geometry(self):
        # 没显示过的窗口存的是出厂默认矩形，会覆盖掉用户的真实记忆——跳过。
        if not self.isVisible():
            return
        # 全屏过程中 saveGeometry 存的是整屏矩形，会污染记忆；跳过，
        # 上一次正常窗口的几何仍然留在档里。
        if getattr(self._main, "_video_fullscreen", False):
            return
        try:
            self._settings().setValue(GEOMETRY_KEY, self.saveGeometry())
        except Exception:
            pass

    # ------------------------------------------------------------------ 关闭
    def closeEvent(self, event):
        # 全屏进行中关窗（Alt+F4 等旁路）：先退全屏，免得 main 里留下
        # 「全屏还在播」的脏标志，下次起播样式就错了。退完再存几何，
        # 存到的就是恢复后的正常窗口尺寸。
        if getattr(self._main, "_video_fullscreen", False):
            try:
                self._main._set_video_fullscreen(False)
            except Exception:
                pass
        self._save_geometry()
        # 关窗必须停会话：沿用 main 的 stop_playback（remember + player.stop + release）。
        try:
            self._main.on_playback_window_close()
        except Exception:
            pass
        event.accept()
        # QDialog.close() 默认就 hide，不销毁 —— PlayerPane 生命周期归 main。

    def reject(self):
        """Esc/窗口关闭按钮之外的 done() 路径也归一到 closeEvent，保证停播一致。"""
        self.close()

    def hideEvent(self, event):
        # 程序性 hide（例如主窗口退出前收起）不会走 closeEvent，这里补记几何。
        self._save_geometry()
        super().hideEvent(event)
