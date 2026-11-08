# -*- coding: utf-8 -*-
"""S1 设置面板：站源开关 / 播放默认 / 存储路径 / 关于。

对外契约（M1 挂载用）
====================
    SettingsPanel(core=None, parent=None)      # 无必填参数，可直接 SettingsPanel()
    bind_core(client)                          # 可选：启动后显式注入 CoreClient

core 解析顺序：构造参数 core → 模块级 bind_core → 沿 parent 链探测
属性 ``_core`` / ``core``（需具备 list_sources / set_source_enabled /
sorani_account 任一；T1 站源账号行只依赖 sorani_account）。
全部环节 hasattr/try 保护；core 不可用时列表退化为内置 FALLBACK_SOURCES
（纯展示兜底，不是暂存——站源开关唯一落点是 core.set_source_enabled，
QSettings sources/enabled 暂存键已随 F1 就绪退役）。

信号
----
    sourceToggled(str, bool)   站源 slug、新状态（同步进本地核心成功后才发；
                               失败时只在行内红字提示，不发信号、不落任何暂存）
    speedChanged(float)        倍速档位变更
    qualityChanged(str)        清晰度 slug 变更
    danmakuChanged(bool)       弹幕开关变更
    libraryRootChanged(str)    成品库切换后的下载目录（绝对路径）

样式：全部颜色取 ui.DESIGN token（主题跟随，showEvent 时按签名重刷），
无任何硬编码色值。S2 起：开关/站源卡片/自适应栅格拆分到 settings_widgets
（开关 paintEvent 自绘 + 滑动动画 + 键盘可达；栅格窄 3 列 / 宽 4 列），
分区统一卡片化版式；行控件的 normal/hover/pressed/disabled 态齐备。
"""

import os

from PyQt5.QtCore import QTimer, Qt, pyqtSignal
from PyQt5.QtWidgets import (QComboBox, QFileDialog, QFrame,
                             QHBoxLayout, QLabel, QLineEdit, QPushButton,
                             QScrollArea, QSizePolicy, QVBoxLayout, QWidget)

from . import tools, ui
from .settings_widgets import GuoSwitch, SourceCard, SourceGrid
from .sorani_login import SoraniAccountDialog
from .tasks import TaskRunner

# S2 兼容别名：开关与站源行实现移到 settings_widgets（对外类名/读点不变，
# 探针与旧代码仍可按 _GuoSwitch/_SourceRow 引用）。
_GuoSwitch = GuoSwitch
_SourceRow = SourceCard

# ---------------------------------------------------------------- 播放器约定
# player.py 由 P1 并行开发：这里只消费常量与键名，绝不改动它。import 失败
# （或常量缺失）时退化为同值字面量，保证面板在任何并行状态下都能构造。
try:
    from . import player as _player
except Exception:  # pragma: no cover - 并行破坏时的保险
    _player = None

SETTINGS_ORG = getattr(_player, "SETTINGS_ORG", "zhenguojian")
SETTINGS_APP = getattr(_player, "SETTINGS_APP", "zhenguojian")
SPEED_KEY = getattr(_player, "SPEED_KEY", "player/speed")
QUALITY_KEY = getattr(_player, "QUALITY_KEY", "player/quality")
DANMAKU_KEY = getattr(_player, "DANMAKU_KEY", "player/danmaku_enabled")
SPEED_CHOICES = tuple(getattr(_player, "SPEED_CHOICES",
                              (0.5, 0.75, 1.0, 1.25, 1.5, 2.0, 3.0)))
QUALITY_CHOICES = tuple(getattr(_player, "QUALITY_CHOICES",
                                (("auto", "自动"), ("1080p", "1080P"),
                                 ("720p", "720P"), ("540p", "540P"))))

try:
    from . import __version__ as APP_VERSION
except Exception:  # pragma: no cover
    APP_VERSION = "dev"

# ------------------------------------------------------- 内置站源回退表（14 id）
# 与 research/source-map.md ①节一一对应：guoapp 展示 12 项，其中「黄果」是
# 分组名（groupId huangguo），本地按 3 个 provider 变体独立开关，故 14 行。
# implemented=False 的 5 项（韩小圈/鬼片/禁果短剧/禁果/黑桃）本地无 provider
# 实现，见 source-map ②移植清单；备注含连通性观察，展示为行 tooltip。
FALLBACK_SOURCES = [
    {"slug": "hongguo",        "name": "红果",      "implemented": True,
     "note": "主源，恒可用。"},
    {"slug": "hanxiaoquan",    "name": "韩小圈",    "implemented": True, "note": ""},
    {"slug": "guipian",        "name": "鬼片",      "implemented": True, "note": ""},
    {"slug": "sorani",         "name": "青空",      "implemented": True,
     "note": "播放需登录：设置 > 站源 > 站源账号。"},
    {"slug": "huangdou",       "name": "黄豆",      "implemented": True, "note": ""},
    {"slug": "huangju",        "name": "剧果",      "implemented": True, "note": ""},
    {"slug": "yeguo",          "name": "野果",      "implemented": True, "note": ""},
    {"slug": "dsd",            "name": "帝果",      "implemented": True,
     "note": "上游连通性不稳定，失败时稍后重试。"},
    {"slug": "jinguoduanju",   "name": "禁果短剧",  "implemented": True, "note": ""},
    {"slug": "jinguo",         "name": "禁果",      "implemented": True, "note": ""},
    {"slug": "heitao",         "name": "黑桃",      "implemented": True, "note": ""},
    {"slug": "huangguo-video", "name": "黄果·视频", "implemented": True,
     "note": "上游连通性不稳定，失败时稍后重试。"},
    {"slug": "huangguoai",     "name": "黄果·AI",   "implemented": True, "note": ""},
    {"slug": "cloudfront",     "name": "黄果·旧版", "implemented": True, "note": ""},
    {"slug": "youzi",          "name": "柚子",      "implemented": True,
     "enabled": False, "adult": True,
     "note": "18+ 内容，出厂默认停用，需手动打开。"},
]

_BOUND_CORE = None


def bind_core(client):
    """M1 可选：显式注入 CoreClient（面板下次构造/显示时生效）。"""
    global _BOUND_CORE
    _BOUND_CORE = client


# ------------------------------------------------------------------ 小工具
def _qsettings():
    """与 player 同一枚 QSettings（org/app 'zhenguojian'）。"""
    if _player is not None and hasattr(_player, "player_settings"):
        try:
            return _player.player_settings()
        except Exception:  # pragma: no cover
            pass
    from PyQt5.QtCore import QSettings
    return QSettings(SETTINGS_ORG, SETTINGS_APP)


def _load_playback_prefs():
    """播放默认初值：优先 player.load_player_prefs()（含 danmaku 播种逻辑）。"""
    if _player is not None and hasattr(_player, "load_player_prefs"):
        try:
            return _player.load_player_prefs()
        except Exception:  # pragma: no cover
            pass
    s = _qsettings()
    return {"speed": float(s.value(SPEED_KEY, 1.0) or 1.0),
            "quality": str(s.value(QUALITY_KEY, "auto") or "auto"),
            "danmaku_enabled": bool(s.value(DANMAKU_KEY, True))}


def _speed_label(speed):
    """与 player._speed_chip 同格式（1.0x / 0.75x / 1.25x）；不依赖 P1 实例。"""
    s = float(speed)
    if s == int(s):
        return "%.1fx" % s
    return ("%.2f" % s).rstrip("0") + "x"


def _repolish(widget):
    widget.style().unpolish(widget)
    widget.style().polish(widget)


class _SoraniAccountRow(QWidget):
    """站源账号行（T1）：青空登录态 + 登录/退出入口。

    沿用 _SourceRow 的行结构（sourceRow hover + chip 状态色）；登录态由
    面板异步查询后喂入 set_status，读取失败显示「未知」而非空白。"""

    loginRequested = pyqtSignal()
    logoutRequested = pyqtSignal()

    def __init__(self, parent=None):
        super().__init__(parent)
        self.setProperty("guoRole", "sourceRow")
        self.setObjectName("s1_sorani_account")

        lay = QHBoxLayout(self)
        lay.setContentsMargins(8, 3, 8, 3)
        lay.setSpacing(8)

        self.name = QLabel("青空账号")
        self.name.setProperty("guoRole", "sourceName")
        lay.addWidget(self.name)
        lay.addStretch(1)

        self.chip = QLabel("未知")
        self.chip.setProperty("guoRole", "chip")
        self.chip.setProperty("guoStatus", "dev")
        self.chip.setAlignment(Qt.AlignCenter)
        lay.addWidget(self.chip)
        lay.addSpacing(6)

        self.login_btn = QPushButton("登录")
        self.login_btn.setProperty("role", "primary")
        self.login_btn.setCursor(Qt.PointingHandCursor)
        self.login_btn.clicked.connect(lambda checked=False: self.loginRequested.emit())
        lay.addWidget(self.login_btn)

        self.logout_btn = QPushButton("退出登录")
        self.logout_btn.setProperty("role", "danger")
        self.logout_btn.setCursor(Qt.PointingHandCursor)
        self.logout_btn.clicked.connect(lambda checked=False: self.logoutRequested.emit())
        self.logout_btn.hide()
        lay.addWidget(self.logout_btn)

        self.setToolTip("青空次元剧集需登录播放；此处即「站源账号」入口。")

    def enterEvent(self, event):
        self._set_hover(True)
        super().enterEvent(event)

    def leaveEvent(self, event):
        self._set_hover(False)
        super().leaveEvent(event)

    def _set_hover(self, on):
        self.setProperty("hover", "true" if on else None)
        _repolish(self)

    def set_status(self, status, error=""):
        """status = soraniAccount 返回 dict | None（None=未知）。"""
        logged_in = False
        if isinstance(status, dict) and status.get("loggedIn"):
            account = str(status.get("name") or "").strip()
            self.chip.setText("已登录：" + account if account else "已登录")
            self.chip.setProperty("guoStatus", "ok")
            logged_in = True
        elif isinstance(status, dict):
            self.chip.setText("未登录")
            self.chip.setProperty("guoStatus", "unavailable")
        else:
            self.chip.setText("未知")
            self.chip.setProperty("guoStatus", "dev")
        _repolish(self.chip)
        self.logout_btn.setVisible(logged_in)
        tip = "青空次元剧集需登录播放；此处即「站源账号」入口。"
        if error:
            tip = "%s（%s）" % (tip, error)
        self.setToolTip(tip)


# ------------------------------------------------------------------ 设置面板
class SettingsPanel(QWidget):
    sourceToggled = pyqtSignal(str, bool)
    speedChanged = pyqtSignal(float)
    qualityChanged = pyqtSignal(str)
    danmakuChanged = pyqtSignal(bool)
    libraryRootChanged = pyqtSignal(str)

    _QSS_TOKENS = ("guo_accent", "guo_accent_hover", "guo_accent_pressed",
                   "guo_accent_soft", "guo_border", "guo_chip_bg",
                   "guo_chip_hover", "guo_chip_pressed", "guo_warning",
                   "guo_muted", "guo_text", "guo_danger", "guo_card_bg",
                   "guo_radius", "guo_radius_lg", "surface", "surface2",
                   "surface3", "border", "disabled_text", "disabled_border",
                   "on_accent")

    def __init__(self, core=None, parent=None):
        super().__init__(parent)
        self._explicit_core = core
        self._core = None
        self._sources_mode = "fallback"    # core | fallback
        self._rows = {}                    # slug -> SourceCard
        self._source_grid = None           # 站源卡片栅格（S2）
        self._qss_sig = None
        self._sorani_row = None            # 站源账号行（T1）
        self._sorani_status = None         # 缓存登录态 dict | None=未知
        self._sorani_queried = False       # 懒查只做一次
        self._sorani_runner = TaskRunner(self)
        self._build_ui()
        self._populate_sources()
        self._load_playback()
        self._load_paths()
        self._refresh_about()
        self.refresh_qss()

    # ------------------------------------------------------------ 主题跟随
    def _token_sig(self):
        return tuple(ui.DESIGN.get(k) for k in self._QSS_TOKENS)

    def refresh_qss(self):
        """面板局部 QSS：颜色/圆角全部 %(...)s 插值 ui.DESIGN token，禁硬编码。

        S2 起开关由 settings_widgets.GuoSwitch paintEvent 自绘——面板不再放
        任何 ::indicator 规则（Qt5 伪状态叠加劫持子控件匹配的历史坑就此绕开，
        swState 属性仅作状态快照读点保留）。
        """
        qss = (
            # 开关容器：禁 QSS 参与绘制，只保证背景透明、无焦点矩形之外残留
            "QCheckBox[guoRole=\"switch\"] {"
            "  spacing: 0px; background: transparent; border: none; }"
            # 分区卡片化：统一 底色+描边+大圆角+内边距（_section 挂属性）
            "QWidget[guoRole=\"settingsSection\"] {"
            "  background: %(guo_card_bg)s;"
            "  border: 1px solid %(guo_border)s;"
            "  border-radius: %(guo_radius_lg)s; }"
            # 页头
            "QLabel[guoRole=\"panelTitle\"] { font-size: 17px; font-weight: 600;"
            "  color: %(guo_text)s; background: transparent; }"
            "QLabel[guoRole=\"panelSub\"] { font-size: 12px; color: %(guo_muted)s;"
            "  background: transparent; }"
            # 分区头：覆盖全局 sectionHeader 的 muted 小字，提级为 13px 正文色
            "QLabel[guoRole=\"sectionHeader\"] { color: %(guo_text)s;"
            "  font-size: 13px; font-weight: 600; background: transparent;"
            "  padding: 0px 0px 0px 9px; border-left: 3px solid %(guo_accent)s; }"
            # 状态 chip：复用全局 guoRole="chip"，卡片底上加深一档避免糊底
            "QLabel[guoRole=\"chip\"] { background: %(surface2)s;"
            "  border: 1px solid %(border)s; padding: 1px 8px;"
            "  font-size: 11px; }"
            "QLabel[guoRole=\"chip\"][guoStatus=\"ok\"] {"
            "  color: %(guo_accent)s; background: %(guo_accent_soft)s;"
            "  border: 1px solid %(guo_accent_soft)s; }"
            "QLabel[guoRole=\"chip\"][guoStatus=\"dev\"] {"
            "  color: %(guo_warning)s; background: %(surface2)s; }"
            "QLabel[guoRole=\"chip\"][guoStatus=\"unavailable\"] {"
            "  color: %(disabled_text)s; background: %(surface2)s; }"
            # 站源卡：常态 / hover / 停用态（开发中·暂不可用降饱和）
            "QWidget[guoRole=\"sourceCard\"] {"
            "  background: %(guo_chip_bg)s;"
            "  border: 1px solid %(guo_border)s;"
            "  border-radius: %(guo_radius)s; }"
            "QWidget[guoRole=\"sourceCard\"][hover=\"true\"] {"
            "  background: %(guo_chip_hover)s;"
            "  border-color: %(guo_accent)s; }"
            "QWidget[guoRole=\"sourceCard\"][cardState=\"dev\"],"
            "QWidget[guoRole=\"sourceCard\"][cardState=\"unavailable\"] {"
            "  background: %(surface2)s; border-color: %(border)s; }"
            "QLabel[guoRole=\"sourceCardName\"] { color: %(guo_text)s;"
            "  font-weight: 600; background: transparent; }"
            "QLabel[guoRole=\"sourceCardName\"][dim=\"true\"] {"
            "  color: %(disabled_text)s; }"
            "QLabel[guoRole=\"sourceCardNote\"] { color: %(guo_muted)s;"
            "  font-size: 11px; background: transparent; }"
            "QLabel[guoRole=\"sourceCardNote\"][dim=\"true\"] {"
            "  color: %(disabled_text)s; }"
            # 表单行标签 + 下拉 + 只读路径框 + 按钮，统一一套描边/圆角口径
            "QLabel[guoRole=\"fieldLabel\"] { color: %(guo_text)s;"
            "  background: transparent; }"
            "QComboBox { background: %(guo_chip_bg)s;"
            "  border: 1px solid %(guo_border)s;"
            "  border-radius: %(guo_radius)s; padding: 4px 10px;"
            "  color: %(guo_text)s; }"
            "QComboBox:hover, QComboBox:focus, QComboBox:on {"
            "  border-color: %(guo_accent)s; }"
            "QComboBox::drop-down { border: none; width: 22px; }"
            "QComboBox QAbstractItemView { background: %(surface)s;"
            "  border: 1px solid %(guo_border)s;"
            "  border-radius: %(guo_radius)s;"
            "  selection-background-color: %(guo_accent)s;"
            "  selection-color: %(on_accent)s; }"
            "QLineEdit[guoRole=\"pathBox\"] { background: %(guo_chip_bg)s;"
            "  border: 1px solid %(guo_border)s;"
            "  border-radius: %(guo_radius)s; padding: 4px 10px;"
            "  color: %(guo_text)s; }"
            "QLineEdit[guoRole=\"pathBox\"]:read-only { color: %(guo_muted)s; }"
            "QPushButton[role=\"ghost\"] { background: transparent;"
            "  border: 1px solid %(guo_border)s; color: %(guo_muted)s;"
            "  border-radius: %(guo_radius)s; padding: 4px 12px; }"
            "QPushButton[role=\"ghost\"]:hover {"
            "  border-color: %(guo_accent)s; color: %(guo_text)s;"
            "  background: %(guo_chip_hover)s; }"
            "QPushButton[role=\"ghost\"]:pressed { background: %(guo_chip_pressed)s; }"
            "QPushButton[role=\"primary\"] { background: %(guo_accent)s;"
            "  color: %(on_accent)s; border: 1px solid %(guo_accent)s;"
            "  border-radius: %(guo_radius)s; padding: 4px 14px;"
            "  font-weight: 600; }"
            "QPushButton[role=\"primary\"]:hover {"
            "  background: %(guo_accent_hover)s;"
            "  border-color: %(guo_accent_hover)s; }"
            "QPushButton[role=\"primary\"]:pressed {"
            "  background: %(guo_accent_pressed)s;"
            "  border-color: %(guo_accent_pressed)s; }"
            "QPushButton[role=\"danger\"] { background: transparent;"
            "  border: 1px solid %(guo_danger)s; color: %(guo_danger)s;"
            "  border-radius: %(guo_radius)s; padding: 4px 14px; }"
            "QPushButton[role=\"danger\"]:hover { background: %(guo_chip_hover)s; }"
            "QPushButton[role=\"danger\"]:pressed { background: %(guo_chip_pressed)s; }"
            # 站源行同步失败红字提示（F1 起失败不再落 QSettings 暂存，只行内提示）
            "QLabel[guoRole=\"rowError\"] { color: %(guo_danger)s;"
            "  font-size: 11px; background: transparent; }"
        ) % ui.DESIGN
        self.setStyleSheet(qss)
        self._qss_sig = self._token_sig()

    def showEvent(self, event):
        if self._qss_sig != self._token_sig():
            self.refresh_qss()          # 主题在挂载后被切过 → 重拼局部 QSS
        super().showEvent(event)

    # ------------------------------------------------------------ 骨架
    def _build_ui(self):
        root = QVBoxLayout(self)
        root.setContentsMargins(18, 16, 18, 16)
        root.setSpacing(10)

        title = QLabel("设置")
        title.setProperty("guoRole", "panelTitle")
        root.addWidget(title)
        sub = QLabel("站源 · 播放默认 · 存储 · 关于")
        sub.setProperty("guoRole", "panelSub")
        root.addWidget(sub)

        scroll = QScrollArea()
        scroll.setProperty("guoRole", "bare")
        scroll.setWidgetResizable(True)
        scroll.setFrameShape(QFrame.NoFrame)
        scroll.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)

        content = QWidget()
        cl = QVBoxLayout(content)
        cl.setContentsMargins(2, 6, 2, 2)
        cl.setSpacing(14)
        cl.addWidget(self._build_sources_section())
        cl.addWidget(self._build_playback_section())
        cl.addWidget(self._build_paths_section())
        cl.addWidget(self._build_about_section())
        cl.addStretch(1)
        scroll.setWidget(content)
        root.addWidget(scroll, 1)

    def _section(self, object_name, header, hint_text):
        box = QWidget()
        box.setObjectName(object_name)
        box.setProperty("guoRole", "settingsSection")
        lay = QVBoxLayout(box)
        lay.setContentsMargins(16, 12, 16, 14)
        lay.setSpacing(6)
        head = QLabel(header)
        head.setProperty("guoRole", "sectionHeader")
        lay.addWidget(head)
        hint = QLabel(hint_text)
        hint.setProperty("role", "hint")
        hint.setWordWrap(True)
        lay.addWidget(hint)
        body = QVBoxLayout()
        body.setContentsMargins(0, 2, 0, 0)
        body.setSpacing(4)
        lay.addLayout(body)
        return box, body, hint

    # ------------------------------------------------------------ ① 站源开关
    def _build_sources_section(self):
        box, body, self._sources_hint = self._section(
            "s1_sources", "站源开关", "打开=允许该站参与找剧/搜索；变更即时生效。")
        self._sources_body = body
        return box

    def _resolve_core(self):
        def usable(c):
            return c is not None and (callable(getattr(c, "list_sources", None))
                                      or callable(getattr(c, "set_source_enabled", None))
                                      or callable(getattr(c, "sorani_account", None)))
        for c in (self._explicit_core, _BOUND_CORE):
            if usable(c):
                return c
        w = self.parent()
        while w is not None:
            for attr in ("_core", "core"):
                c = getattr(w, attr, None)
                if usable(c):
                    return c
            w = w.parent()
        return None

    def _normalise_sources(self, raw):
        """把 core.list_sources() 结果归一为行 dict；形态不符的条目跳过。

        期望条目：dict{slug,name,implemented,enabled,available,note}
        （容忍 tuple/list 前两位 = slug,name）。缺失字段按内置表/默认值补齐。
        """
        rows = []
        for item in raw or ():
            if isinstance(item, dict):
                data = item
            elif isinstance(item, (list, tuple)) and item:
                data = {"slug": item[0], "name": item[1] if len(item) > 1 else item[0]}
            else:
                continue
            slug = str(data.get("slug") or data.get("id") or "").strip()
            if not slug:
                continue
            known = next((f for f in FALLBACK_SOURCES if f["slug"] == slug), None)
            implemented = data.get("implemented")
            if implemented is None:
                implemented = known["implemented"] if known else True
            available = data.get("available")
            if available is None:
                available = True
            enabled = data.get("enabled")
            if enabled is None:
                enabled = bool(implemented)
            adult = data.get("adult")
            if adult is None:
                adult = (known or {}).get("adult", False)
            rows.append({
                "slug": slug,
                "name": str(data.get("name") or data.get("display_name")
                            or (known or {}).get("name") or slug),
                "implemented": bool(implemented),
                "enabled": bool(enabled),
                "available": bool(available),
                "adult": bool(adult),
                "note": str(data.get("note") or (known or {}).get("note") or ""),
            })
        return rows

    def _populate_sources(self):
        core = self._resolve_core()
        self._core = core
        rows = None
        failure = ""
        if core is not None and callable(getattr(core, "list_sources", None)):
            try:
                # 注意：F1 若以 core.call 同步 IPC 实现 list_sources，须保证
                # 毫秒级返回（GUI 线程调用），否则请改为缓存快照。见交还说明。
                rows = self._normalise_sources(core.list_sources())
            except Exception as exc:
                rows = None
                failure = "读取本地核心站源列表失败（%s），" % exc
            if rows:
                self._sources_mode = "core"
        if not rows:
            # 纯展示兜底：core 完全不可用（断链）时仍列出内置源表，开关默认态
            # 取行内显式 enabled（18+ 出厂停用源 enabled=False，与真实核心口径
            # 一致），缺省按 implemented；不做任何暂存。
            self._sources_mode = "fallback"
            rows = []
            for info in FALLBACK_SOURCES:
                row = dict(info)
                row["enabled"] = bool(row.get("enabled", row["implemented"]))
                rows.append(row)

        # 清旧行后重建：卡片放进 SourceGrid（自适应 3/4 列栅格），栅格本身
        # 是 _sources_body 里站源区唯一的直接子 widget——青空账号行仍直接挂
        # _sources_body（test_sorani_login 依赖该布局读点，勿改）。
        while self._sources_body.count():
            item = self._sources_body.takeAt(0)
            w = item.widget()
            if w is not None:
                w.deleteLater()
        self._rows = {}
        grid = SourceGrid()
        self._source_grid = grid
        self._sources_body.addWidget(grid)
        for row in rows:
            w = _SourceRow(row)
            w.toggled.connect(self._on_source_toggled)
            grid.add_card(w)
            self._rows[row["slug"]] = w

        if self._sources_mode == "core":
            self._sources_hint.setText("数据来源：本地核心 list_sources()；切换即时同步 set_source_enabled。")
        else:
            self._sources_hint.setText(
                failure +
                "数据来源：内置源表回退（本地核心未连接，仅供查看）；"
                "此状态下的开关不会保存，切换将在行内提示失败。")

        self._mount_sorani_row()

    # ------------------------------------------------------- 站源账号（T1）
    def _mount_sorani_row(self):
        """在站源区尾部追加「青空账号」行，并安排一次性懒查登录态。

        行清理由 _populate_sources 头部的统一循环负责（含上一轮的spacer与
        本行），这里只负责追加。"""
        self._sorani_row = _SoraniAccountRow()
        self._sorani_row.loginRequested.connect(self._open_sorani_dialog)
        self._sorani_row.logoutRequested.connect(self._sorani_logout)
        self._sources_body.addSpacing(8)
        self._sources_body.addWidget(self._sorani_row)
        self._sorani_row.set_status(self._sorani_status)
        self._schedule_sorani_status()

    def _sorani_core(self):
        core = self._core if callable(getattr(self._core, "sorani_account", None)) \
            else self._resolve_core()
        return core if callable(getattr(core, "sorani_account", None)) else None

    def _schedule_sorani_status(self):
        if self._sorani_queried:
            return
        self._sorani_queried = True
        QTimer.singleShot(0, self._refresh_sorani_status)

    def _refresh_sorani_status(self):
        core = self._sorani_core()
        if core is None:
            self._on_sorani_status(None, "本地核心未就绪")
            return
        self._sorani_runner.run(
            lambda: core.sorani_account(""),
            on_success=lambda data: self._on_sorani_status(
                data if isinstance(data, dict) else None),
            on_error=lambda msg: self._on_sorani_status(None, "读取登录状态失败：%s" % msg))

    def _on_sorani_status(self, status, error=""):
        self._sorani_status = status if isinstance(status, dict) else None
        if self._sorani_row is not None:
            self._sorani_row.set_status(self._sorani_status, error)

    def _sorani_logout(self):
        core = self._sorani_core()
        if core is None:
            if self._sorani_row is not None:
                self._sorani_row.set_status(self._sorani_status, "本地核心未就绪")
            return
        self._sorani_runner.run(
            lambda: core.sorani_account("logout"),
            on_success=lambda data: self._on_sorani_status(
                data if isinstance(data, dict) else None),
            on_error=lambda msg: self._on_sorani_status(
                self._sorani_status, "退出登录失败：%s" % msg))

    def make_sorani_dialog(self):
        """构造登录对话框（不 exec，供测试与复用）；状态回传本行。"""
        dlg = SoraniAccountDialog(self._sorani_core(), self)
        dlg.status_changed.connect(self._on_sorani_status)
        return dlg

    def _open_sorani_dialog(self):
        self.make_sorani_dialog().exec_()

    def _on_source_toggled(self, slug, checked):
        """开关唯一落点：core.set_source_enabled（F1 → Go 侧 source_prefs.json）。

        失败只回滚行内开关并红字提示——不写 QSettings 暂存、不发信号；
        成功才 clear_error + 更新提示 + 发 sourceToggled。"""
        row = self._rows.get(slug)
        core = self._core
        err = "本地核心未连接，开关不会保存"
        if core is not None and callable(getattr(core, "set_source_enabled", None)):
            try:
                result = core.set_source_enabled(slug, bool(checked))
            except Exception as exc:
                err = str(getattr(exc, "message", None) or exc)
            else:
                err = self._call_error(result)
        if err:
            if row is not None:
                row.reject_toggle(err)
            return
        if row is not None:
            row.clear_error()
        self._sources_hint.setText("已同步到本地核心：%s → %s"
                                   % (slug, "开" if checked else "关"))
        self.sourceToggled.emit(slug, bool(checked))

    @staticmethod
    def _call_error(result):
        """core 行协议的返回值失败形态：信封 error 字段或 ok=False
        （异常形态由调用方 except 处理）。返回错误文本，空串=成功。"""
        if isinstance(result, dict):
            error = result.get("error")
            if error:
                if isinstance(error, dict):
                    error = error.get("message") or str(error)
                return str(error)
            if result.get("ok") is False:
                return str(result.get("message") or "核心未确认本次修改")
        return ""

    # ------------------------------------------------------------ ② 播放默认
    def _build_playback_section(self):
        box, body, _ = self._section(
            "s1_playback", "播放默认",
            "变更即存（QSettings player/*，与 P1 播放页同键）；对新打开的剧集生效。")
        self.speed_combo = self._combo_row(body, "倍速", "s1_speed")
        self.quality_combo = self._combo_row(body, "清晰度", "s1_quality")
        drow = QWidget()
        dl = QHBoxLayout(drow)
        dl.setContentsMargins(0, 2, 0, 2)
        dl.setSpacing(10)
        dlab = QLabel("弹幕")
        dlab.setProperty("guoRole", "fieldLabel")
        dlab.setMinimumWidth(96)
        dl.addWidget(dlab)
        self.danmaku_switch = _GuoSwitch()
        self.danmaku_switch.setObjectName("s1_danmaku")
        self.danmaku_switch.setCursor(Qt.PointingHandCursor)
        dl.addWidget(self.danmaku_switch)
        dl.addStretch(1)
        body.addWidget(drow)

        for combo, handler in ((self.speed_combo, self._on_speed),
                               (self.quality_combo, self._on_quality)):
            combo.currentIndexChanged.connect(handler)
        self.danmaku_switch.toggled.connect(self._on_danmaku)
        return box

    def _combo_row(self, body, label_text, object_name):
        row = QWidget()
        lay = QHBoxLayout(row)
        lay.setContentsMargins(0, 2, 0, 2)
        lay.setSpacing(10)
        lab = QLabel(label_text)
        lab.setProperty("guoRole", "fieldLabel")
        lab.setMinimumWidth(96)
        lay.addWidget(lab)
        combo = QComboBox()
        combo.setObjectName(object_name)
        combo.setMinimumWidth(140)
        combo.setSizePolicy(QSizePolicy.Preferred, QSizePolicy.Fixed)
        lay.addWidget(combo)
        lay.addStretch(1)
        body.addWidget(row)
        return combo

    def _load_playback(self):
        prefs = _load_playback_prefs()
        self.speed_combo.blockSignals(True)
        self.speed_combo.clear()
        for s in SPEED_CHOICES:
            self.speed_combo.addItem(_speed_label(s), float(s))
        idx = self.speed_combo.findData(float(prefs.get("speed", 1.0)))
        self.speed_combo.setCurrentIndex(idx if idx >= 0 else max(
            0, self.speed_combo.findData(1.0)))
        self.speed_combo.blockSignals(False)

        self.quality_combo.blockSignals(True)
        self.quality_combo.clear()
        for slug, label in QUALITY_CHOICES:
            self.quality_combo.addItem(label, slug)
        qidx = self.quality_combo.findData(str(prefs.get("quality", "auto")))
        self.quality_combo.setCurrentIndex(qidx if qidx >= 0 else 0)
        self.quality_combo.blockSignals(False)

        self.danmaku_switch.blockSignals(True)
        self.danmaku_switch.setChecked(bool(prefs.get("danmaku_enabled", True)))
        self.danmaku_switch.blockSignals(False)
        self.danmaku_switch.refresh_state()

    def _set_pref(self, key, value):
        s = _qsettings()
        s.setValue(key, value)
        s.sync()   # 变更即存：立即落盘，满足「重启后 prefs 持久」

    def _on_speed(self, _index):
        value = float(self.speed_combo.currentData() or 1.0)
        self._set_pref(SPEED_KEY, value)
        self.speedChanged.emit(value)

    def _on_quality(self, _index):
        slug = str(self.quality_combo.currentData() or "auto")
        self._set_pref(QUALITY_KEY, slug)
        self.qualityChanged.emit(slug)

    def _on_danmaku(self, checked):
        self._set_pref(DANMAKU_KEY, bool(checked))
        # 镜像同步 tools 侧：与 main.py:1403 danmakuToggled 处理器同款语义，
        # 保证 tools.danmaku_enabled() 与 player/danmaku_enabled 键一致。
        try:
            tools.set_danmaku_enabled(bool(checked))
        except Exception:  # pragma: no cover
            pass
        self.danmakuChanged.emit(bool(checked))

    # ------------------------------------------------------------ ③ 存储路径
    def _build_paths_section(self):
        box, body, self._paths_hint = self._section(
            "s1_paths", "存储路径",
            "与下载页「修改位置」同一配置源（app-settings.json libraryRoot），互不冲突。")
        self.lib_edit = self._path_row(body, "成品库（下载）", "s1_libpath", True)
        self.cache_edit = self._path_row(body, "加密缓存", "s1_cachepath", False)
        return box

    def _path_row(self, body, label_text, object_name, editable):
        row = QWidget()
        lay = QHBoxLayout(row)
        lay.setContentsMargins(0, 2, 0, 2)
        lay.setSpacing(10)
        lab = QLabel(label_text)
        lab.setProperty("guoRole", "fieldLabel")
        lab.setMinimumWidth(96)
        lay.addWidget(lab)
        edit = QLineEdit()
        edit.setObjectName(object_name)
        edit.setProperty("guoRole", "pathBox")
        edit.setReadOnly(True)
        edit.setSizePolicy(QSizePolicy.Expanding, QSizePolicy.Fixed)
        lay.addWidget(edit, 1)
        if editable:
            btn = QPushButton("修改…")
            btn.setObjectName("s1_libedit")
            btn.setProperty("role", "ghost")
            btn.setCursor(Qt.PointingHandCursor)
            btn.clicked.connect(self._on_choose_location)
            lay.addWidget(btn)
        body.addWidget(row)
        return edit

    def _load_paths(self):
        try:
            self.lib_edit.setText(tools.download_dir())
        except Exception:  # pragma: no cover
            self.lib_edit.setText("")
        try:
            self.cache_edit.setText(tools.core_cache_dir())
            self.cache_edit.setToolTip("内部核心自管的解密缓存目录，位置固定，暂不可修改。")
        except Exception:  # pragma: no cover
            self.cache_edit.setText("")

    def _on_choose_location(self):
        """复刻 downloads.py _choose_location 流程（不改 downloads.py 本体）：
        先建目录再写设置，最后 stash_index 把旧库的成品记录搬给新根。"""
        default_root = tools.project_dir()
        previous = tools.download_dir()
        start = os.path.dirname(previous) if previous else default_root
        chosen = QFileDialog.getExistingDirectory(self, "选择成品库位置", start)
        if not chosen:
            return
        chosen = os.path.abspath(chosen)
        if chosen == os.path.abspath(default_root):
            chosen = ""                      # 等于默认位置：设置里不留多余项
        target = os.path.join(chosen or default_root, "下载")
        if os.path.abspath(target) == os.path.abspath(previous):
            self._paths_hint.setText("所选位置与当前相同，未做变更。")
            return
        try:
            os.makedirs(target, exist_ok=True)
        except OSError as exc:
            self._paths_hint.setText("这个目录不能用：%s（%s）" % (target, exc))
            return
        try:
            tools.set_library_root(chosen)
        except OSError as exc:
            self._paths_hint.setText("保存设置失败：%s" % exc)
            self._load_paths()
            return
        message = "成品库已切换到 %s" % target
        try:
            from . import library
            carried = library.stash_index(previous, target)
            if carried:
                message += "（沿用 %d 条成品记录）" % carried
        except Exception as exc:
            message += "（成品记录搬运失败：%s，重启后将按新目录重建索引）" % exc
        self._load_paths()
        self._paths_hint.setText(message)
        self.libraryRootChanged.emit(target)

    # ------------------------------------------------------------ ④ 关于
    def _build_about_section(self):
        box, body, _ = self._section(
            "s1_about", "关于", "真果鉴 · 红果短剧聚合客户端（站源适配参考 guoapp）。")
        self._about_version = self._kv_row(body, "应用版本")
        self._about_core = self._kv_row(body, "本地核心")
        self._about_commit = self._kv_row(body, "打包提交")
        self._about_data = self._kv_row(body, "数据目录")
        # 配置存储说明较长：用独立 wordWrap 提示段，避免把行最小宽度撑爆
        store = QLabel("")
        store.setProperty("role", "hint")
        store.setWordWrap(True)
        body.addWidget(store)
        self._about_store = store
        return box

    def _kv_row(self, body, label_text):
        row = QWidget()
        lay = QHBoxLayout(row)
        lay.setContentsMargins(0, 1, 0, 1)
        lay.setSpacing(10)
        lab = QLabel(label_text)
        lab.setProperty("guoRole", "fieldLabel")
        lab.setMinimumWidth(96)
        lay.addWidget(lab)
        val = QLabel("")
        val.setProperty("role", "hint")
        val.setTextInteractionFlags(Qt.TextSelectableByMouse)
        lay.addWidget(val, 1)
        body.addWidget(row)
        return val

    def _commit_info(self):
        """指纹来源两级：sidecar 的 go build VCS 戳记 → 程序目录 build-info.json。

        构建脚本若带 -buildvcs=false（或在 git 工作区外编译），戳记会缺失；
        此时允许打包时在 exe 同目录写 build-info.json：
        {"revision": "<git rev-parse HEAD>", "commitTime": "<%cI>", "modified": bool}
        """
        info = getattr(self._core, "build_info", None) if self._core else None
        if (info or {}).get("revision"):
            return info
        try:
            import json
            path = os.path.join(tools.app_root(), "build-info.json")
            if os.path.isfile(path):
                with open(path, "r", encoding="utf-8") as fh:
                    data = json.load(fh)
                if isinstance(data, dict) and data.get("revision"):
                    return data
        except Exception:  # pragma: no cover
            pass
        return info or {}

    def _format_commit(self):
        """把打包时的 git 指纹排成一行可读文本。"""
        info = self._commit_info()
        revision = (info or {}).get("revision") or ""
        if not revision:
            return "未知（本地核心未连接，或构建未注入版本信息）"
        stamp = revision[:12]
        when = (info or {}).get("commitTime") or ""
        if when:
            stamp += "  ·  " + when[:10]
        if (info or {}).get("modified"):
            stamp += "  ·  ⚠ 构建时含未提交改动"
        return stamp

    def _refresh_about(self):
        self._about_version.setText("真果鉴 v%s" % APP_VERSION)
        core_version = getattr(self._core, "version", "") if self._core else ""
        if core_version:
            self._about_core.setText(str(core_version))
        else:
            self._about_core.setText("本地核心未连接")
        self._about_commit.setText(self._format_commit())
        info = getattr(self._core, "build_info", None) if self._core else None
        full = (info or {}).get("revision") or ""
        self._about_commit.setToolTip(full or "无版本指纹")
        try:
            self._about_data.setText(tools.data_dir())
        except Exception:  # pragma: no cover
            self._about_data.setText("")
        self._about_store.setText(
            "播放默认 → QSettings player/*；站源开关 → 本地核心 source_prefs.json"
            "（面板不落 QSettings）；成品库 → app-settings.json libraryRoot。")


__all__ = ["SettingsPanel", "bind_core", "FALLBACK_SOURCES"]
