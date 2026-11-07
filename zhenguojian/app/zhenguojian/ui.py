# -*- coding: utf-8 -*-
"""Design tokens and the global stylesheet.

两套调色板（深色 / 浅色）共用同一组 token 名，``DESIGN`` 始终指向**当前生效**
的那一套，所以别处 ``DESIGN["accent"]`` 这类读取不用改。切换主题走
``apply_theme()``：它就地改写 DESIGN，再让调用方重建 stylesheet。

规则：颜色只能在这里出现字面值。自绘的控件（分集芯片、下载卡片、海报墙）在
paint 时读 DESIGN，所以换主题后调一次 ``repaint_all()`` 就够了。
"""
from __future__ import annotations

RADIUS = "8px"          # 中等：按钮 / 输入框 / 卡片 / 列表 / 菜单
RADIUS_SMALL = "4px"    # 小：菜单项 / 列表项 / 细条

# ---------------------------------------------------------------- 调色板
# 两套 token 必须完全同键，缺键会在切换时报 KeyError，而不是悄悄画错颜色。
DARK = {
    "radius": RADIUS,
    "radius_small": RADIUS_SMALL,
    "bg": "#0f1117",           # 窗口背景
    "surface": "#141821",      # 列表 / 表格 / 输入框
    "surface2": "#171b23",     # 菜单栏 / 状态栏 / 未选中标签页
    "surface3": "#24242e",     # 悬停底色
    "surface4": "#2e2e3a",     # 表头 / 分隔条 / 次要按钮禁用态
    "border": "#2b2b36",       # 细边框
    "accent": "#ff3b5c",       # 品牌红：选中 / 焦点 / 主按钮
    "accent_hover": "#e8264c",
    "accent_pressed": "#cf1f42",
    "accent_soft": "#24ff3b5c",   # 选中项的淡色底（#AARRGGBB；PyQt5 的 QColor 不认 rgba() 字符串）
    "on_accent": "#ffffff",    # 压在 accent/success 上的字
    "accent2": "#ff6f85",      # 品牌色浅一档：标题、徽标
    "success": "#2ea043",
    "success_hover": "#3fb950",
    "success_pressed": "#278839",
    "danger": "#ff5d5d",
    "warning": "#d29922",
    "score": "#ffb400",
    "text": "#e9e9f0",
    "muted": "#a2a2b2",        # --text-2
    "muted2": "#6d6d7c",       # --text-3，比 muted 再弱一档
    "chip": "transparent",     # 胶囊 chip 默认透明底
    "chip_hover": "#24242e",
    "cover_bg": "#24242e",     # 海报还没加载出来的占位底色
    "button_bg": "#24242e",
    "button_border": "#3a3a48",
    "button_hover": "#2e2e3a",
    "button_pressed": "#1c1c24",
    "button2_bg": "#1c1c24",   # 次要按钮（描边款）
    "button2_hover": "#24242e",
    "button2_border": "#3a3a48",
    "input_hover_border": "#4a4a5c",
    "disabled_bg": "#2e2e3a",  # 主按钮禁用态
    "disabled_text": "#6d6d7c",
    "disabled_border": "#3a3a48",
    "ghost_disabled_text": "#6d6d7c",
    "ghost_disabled_border": "#3a3a48",
    "success_disabled_bg": "#1f2a24",
    "success_disabled_text": "#6d6d7c",
    "success_disabled_border": "#2b3a31",
    "danger_border": "#5a3a41",
    "danger_hover_bg": "#2a1d21",
    "danger_pressed_bg": "#351f24",
    "grid": "#2b2b36",         # 表格网格线
    "alt_row": "#12121a",      # 表格隔行
    "selected_soft": "#24ff3b5c",
    "scroll": "#2b2b36",
    "scroll_hover": "#4a4a5c",
    "state_downloading": "#ff6f85",
    "state_completed": "#3fb950",
    "state_paused": "#d29922",
    "state_queued": "#a2a2b2",
    "state_failed": "#ff5d5d",
    "video_bg": "#000000",     # 播放器画面周围/底色（两套主题都保持深色）
    "video_text": "#a2a2b2",   # 压在 video_bg 上的字
    "video_danger": "#ff5d5d",  # 压在 video_bg 上的报错字
    "rank1": "#ff7a45",        # 排行榜前三的名次色
    "rank2": "#ffc04d",
    "rank3": "#5ec2ff",
    # ---- F2 guoapp 风格派生 token（guo_ 前缀，只增不改；两套主题必须同键）----
    "guo_radius": "6px",          # guoapp 基础圆角
    "guo_radius_lg": "12px",      # 海报卡 / 浮层大圆角
    "guo_surface3": "#1c2130",    # guoapp 深一档悬停层（不动现有 surface3）
    "guo_border": "#262c3a",      # guoapp 边框
    "guo_accent": "#4a83ff",      # guoapp 蓝：选中 / 焦点 / 高亮
    "guo_accent_hover": "#5f92ff",
    "guo_accent_pressed": "#3b6ecf",
    "guo_accent_soft": "#264a83ff",   # 15% accent（#AARRGGBB，QColor 不认 rgba()）
    "guo_success": "#2ea043",
    "guo_danger": "#e06c75",
    "guo_warning": "#d29922",
    "guo_text": "#d7dce5",
    "guo_muted": "#8d97a8",
    "guo_card_bg": "#141821",     # 海报卡本体底色
    "guo_badge_bg": "#b3000000",  # 角标小 chip 的黑底（70% 黑）
    "guo_scrim_top": "#00000000",    # 海报底部渐变：起（全透明）
    "guo_scrim_bottom": "#cc0f1117", # 海报底部渐变：止（80% 深底）
    "guo_blur_scrim": "#99000000",   # 模糊背景上的 60% 黑遮罩
    "guo_player_scrim": "#cc0f1117", # 控制条浮层底（80% 深色）
    "guo_vip_bg": "#3d3222",      # VIP chip 底
    "guo_vip_fg": "#e6c07b",      # VIP chip 字
    "guo_chip_bg": "#1c2130",     # 通用胶囊 chip（分组条 / 集按钮 / 站源按钮）
    "guo_chip_hover": "#232a3b",
    "guo_chip_pressed": "#1a2438",
    "guo_shadow": "#66000000",    # 悬停上浮阴影色（40% 黑）
    "guo_shadow_blur": 28,        # 悬停上浮阴影半径 px（几何值，非颜色）
}

LIGHT = {
    "radius": RADIUS,
    "radius_small": RADIUS_SMALL,
    "bg": "#f5f5f8",
    "surface": "#ffffff",
    "surface2": "#f7f7fa",
    "surface3": "#f0f0f4",
    "surface4": "#e6e6ec",
    "border": "#e4e4ea",
    "accent": "#fe2c55",
    "accent_hover": "#e8264c",
    "accent_pressed": "#cf1f42",
    "accent_soft": "#1afe2c55",
    "on_accent": "#ffffff",
    "accent2": "#ff6f85",
    "success": "#1a7f37",
    "success_hover": "#1c8c3d",
    "success_pressed": "#146429",
    "danger": "#cf222e",
    "warning": "#bf8700",
    "score": "#976500",
    "text": "#1c1c24",
    "muted": "#55556a",
    "muted2": "#8a8a9a",
    "chip": "transparent",
    "chip_hover": "#f0f0f4",
    "cover_bg": "#f0f0f4",
    "button_bg": "#ffffff",
    "button_border": "#d4d4dc",
    "button_hover": "#f0f0f4",
    "button_pressed": "#e6e6ec",
    "button2_bg": "#f7f7fa",
    "button2_hover": "#f0f0f4",
    "button2_border": "#d4d4dc",
    "input_hover_border": "#b8b8c4",
    "disabled_bg": "#e6e6ec",
    "disabled_text": "#8a8a9a",
    "disabled_border": "#d4d4dc",
    "ghost_disabled_text": "#8a8a9a",
    "ghost_disabled_border": "#d4d4dc",
    "success_disabled_bg": "#e4efe6",
    "success_disabled_text": "#8a8a9a",
    "success_disabled_border": "#cfe0d3",
    "danger_border": "#e5b4b8",
    "danger_hover_bg": "#fdecee",
    "danger_pressed_bg": "#f9dade",
    "grid": "#e6e6ec",
    "alt_row": "#fafbfc",
    "selected_soft": "#1afe2c55",
    "scroll": "#c8cdd6",
    "scroll_hover": "#adb4bf",
    "state_downloading": "#fe2c55",
    "state_completed": "#1a7f37",
    "state_paused": "#bf8700",
    "state_queued": "#8a8a9a",
    "state_failed": "#cf222e",
    "video_bg": "#101114",
    "video_text": "#a2a2b2",
    "video_danger": "#ff5d5d",
    "rank1": "#ff7a45",
    "rank2": "#ffc04d",
    "rank3": "#5ec2ff",
    # ---- F2 guoapp 风格派生 token（浅色对应值，与 DARK 同键）----
    "guo_radius": "6px",
    "guo_radius_lg": "12px",
    "guo_surface3": "#e9edf5",
    "guo_border": "#d3dae6",
    "guo_accent": "#3a6fd8",
    "guo_accent_hover": "#2f5fc0",
    "guo_accent_pressed": "#274f9e",
    "guo_accent_soft": "#263a6fd8",
    "guo_success": "#1a7f37",
    "guo_danger": "#d0565f",
    "guo_warning": "#bf8700",
    "guo_text": "#23283a",
    "guo_muted": "#5f6878",
    "guo_card_bg": "#ffffff",
    "guo_badge_bg": "#b3ffffff",
    "guo_scrim_top": "#00000000",
    "guo_scrim_bottom": "#ccf5f5f8",
    "guo_blur_scrim": "#99ffffff",
    "guo_player_scrim": "#cc101114",  # 视频浮层两种主题都保持深色
    "guo_vip_bg": "#f3e3c0",
    "guo_vip_fg": "#8a6112",
    "guo_chip_bg": "#eef1f7",
    "guo_chip_hover": "#e4e8f2",
    "guo_chip_pressed": "#d8deeb",
    "guo_shadow": "#3320242e",
    "guo_shadow_blur": 24,
}

PALETTES = {"dark": DARK, "light": LIGHT}
DEFAULT_THEME = "dark"

# 当前生效的调色板；就地改这个 dict，别处 import 到的引用才不会失效。
DESIGN = dict(DARK)

_active = DEFAULT_THEME


def current_theme():
    return _active


def apply_theme(name):
    """切换到 ``name``，返回是否真的变了。调用方负责重建 stylesheet 并重绘。"""
    global _active
    palette = PALETTES.get(name)
    if palette is None or name == _active:
        return False
    DESIGN.clear()
    DESIGN.update(palette)
    _active = name
    return True


def repaint_all():
    """自绘控件（分集芯片、下载卡片、海报墙）在 paint 时读 DESIGN，换主题后要催一下。

    只遍历 QWidget：非控件的 QObject 上也可能挂着叫 update 的信号（信号不可调用），
    按类型过滤比 try/except 干净。
    """
    from PyQt5.QtWidgets import QApplication, QWidget
    for window in QApplication.topLevelWidgets():
        window.update()
        for widget in window.findChildren(QWidget):
            widget.update()


# ------------------------------------------------------------ 按钮样式
# 这些按钮样式全在 build_stylesheet() 里按 role 属性定义（primary / secondary /
# success / accent）。做成函数一次性 setStyleSheet 的话，换主题时那些按钮会留着
# 旧颜色——属性选择器跟着全局样式表走，重新 setStyleSheet 就一起更新了。


def build_stylesheet():
    d = DESIGN
    return (
        # ---------------------------------------------------------- base
        "QWidget { color: %(text)s; font-size: 13px; font-family: 'Microsoft YaHei'; }"
        "QMainWindow, QDialog { background: %(bg)s; }"
        # ------------------------------------------- menus (右键菜单)
        "QMenu { background: %(surface3)s; border: 1px solid %(border)s; border-radius: %(radius)s; padding: 4px; }"
        "QMenu::item { padding: 6px 24px 6px 14px; border-radius: %(radius_small)s; }"
        "QMenu::item:selected { background: %(accent)s; color: %(on_accent)s; }"
        "QMenu::item:disabled { color: %(muted)s; }"
        "QMenu::separator { height: 1px; background: %(border)s; margin: 4px 8px; }"
        # ----------------------------------------------------------- tabs
        "QTabWidget::pane { border: 1px solid %(border)s; border-radius: 0 0 %(radius)s %(radius)s; top: -1px; }"
        # 全屏播放：把页签容器的 1px 边框收掉，浅色主题下也不会露白边
        "QTabWidget[immersive=\"true\"]::pane { border: 0; border-radius: 0; background: %(video_bg)s; }"
        "QTabWidget[immersive=\"true\"] { background: %(video_bg)s; }"
        "QWidget[immersive=\"true\"] { background: %(video_bg)s; }"
        "QTabBar::tab { background: %(surface2)s; color: %(muted)s; padding: 8px 22px;"
        "  border: 1px solid %(border)s; border-bottom: none; margin-right: 2px;"
        "  border-top-left-radius: %(radius)s; border-top-right-radius: %(radius)s; }"
        "QTabBar::tab:selected { background: %(accent)s; color: %(on_accent)s; border-color: %(accent)s; }"
        "QTabBar::tab:!selected:hover { background: %(surface3)s; color: %(text)s; }"
        # -------------------------------------------------- lists & tables
        "QListWidget, QTableWidget, QTextBrowser { background: %(surface)s;"
        "  border: 1px solid %(border)s; border-radius: %(radius)s; }"
        "QListWidget::item { padding: 6px 8px; border-radius: %(radius_small)s; margin: 1px 4px; color: %(text)s; }"
        "QListWidget::item:hover { background: %(surface3)s; }"
        "QListWidget::item:selected { background: %(accent)s; color: %(on_accent)s; }"
        "QTableWidget { gridline-color: %(grid)s; alternate-background-color: %(alt_row)s; }"
        "QTableWidget::item { padding: 4px; border-radius: %(radius_small)s; }"
        "QTableWidget::item:selected { background: %(selected_soft)s; color: %(text)s; }"
        "QHeaderView::section { background: %(surface4)s; color: %(muted)s; padding: 6px;"
        "  border: none; border-right: 1px solid %(border)s; border-bottom: 1px solid %(border)s; }"
        # --------------------------------------------------------- inputs
        "QLineEdit, QComboBox { background: %(surface)s; border: 1px solid %(border)s;"
        "  border-radius: %(radius)s; padding: 5px 8px; selection-background-color: %(accent)s; }"
        "QLineEdit:hover, QComboBox:hover { border-color: %(input_hover_border)s; }"
        "QLineEdit:focus, QComboBox:focus { border: 1px solid %(accent)s; }"
        "QComboBox::drop-down { border: none; width: 20px; }"
        "QComboBox QAbstractItemView { background: %(surface)s; border: 1px solid %(border)s;"
        "  border-radius: %(radius)s; selection-background-color: %(accent)s; selection-color: %(on_accent)s; }"
        # ------------------------------------------------------- controls
        "QPushButton { background: %(button_bg)s; border: 1px solid %(button_border)s;"
        "  border-radius: %(radius)s; padding: 6px 16px; color: %(text)s; }"
        "QPushButton:hover { background: %(button_hover)s; border-color: %(accent)s; }"
        "QPushButton:pressed { background: %(button_pressed)s; }"
        "QPushButton:focus { border-color: %(accent)s; }"
        "QPushButton:disabled { background: %(surface2)s; border-color: %(border)s; color: %(disabled_text)s; }"
        # 带菜单的按钮（详情页「全 N 季」）文字里已经有 ▾，再画原生指示器就成了双箭头
        "QPushButton::menu-indicator { image: none; width: 0px; height: 0px; }"
        # 可切换按钮（播放页的「弹幕」）：开着的状态用强调色，一眼能看出开关
        "QPushButton[role=\"toggle\"]:checked { background: %(accent)s; color: %(on_accent)s;"
        "  border-color: %(accent)s; font-weight: bold; }"
        "QPushButton[role=\"toggle\"]:checked:hover { background: %(accent_hover)s; }"
        # 具名按钮：role=primary(一键合并/搜索) / secondary(描边款) /
        # success(下载全剧) / accent(播放第一集)。做成 role 而不是一次性
        # setStyleSheet，换主题时才会跟着全局样式表一起变。
        "QPushButton[role=\"primary\"] { background: %(accent)s; color: %(on_accent)s;"
        "  font-weight: bold; border: 1px solid %(accent)s; border-radius: %(radius)s;"
        "  padding: 0 18px; }"
        "QPushButton[role=\"primary\"]:hover { background: %(accent_hover)s; }"
        "QPushButton[role=\"primary\"]:pressed { background: %(accent_pressed)s; }"
        "QPushButton[role=\"primary\"]:disabled { background: %(disabled_bg)s;"
        "  color: %(disabled_text)s; border-color: %(disabled_border)s; }"
        "QPushButton[role=\"accent\"] { background: %(accent)s; color: %(on_accent)s;"
        "  border: 1px solid %(accent)s; border-radius: %(radius)s; padding: 6px 18px; }"
        "QPushButton[role=\"accent\"]:hover { background: %(accent_hover)s; }"
        "QPushButton[role=\"accent\"]:pressed { background: %(accent_pressed)s; }"
        "QPushButton[role=\"accent\"]:disabled { background: %(disabled_bg)s;"
        "  color: %(disabled_text)s; border-color: %(disabled_border)s; }"
        "QPushButton[role=\"success\"] { background: %(success)s; color: %(on_accent)s;"
        "  border: 1px solid %(success)s; border-radius: %(radius)s; padding: 6px 16px; }"
        "QPushButton[role=\"success\"]:hover { background: %(success_hover)s; }"
        "QPushButton[role=\"success\"]:pressed { background: %(success_pressed)s; }"
        "QPushButton[role=\"success\"]:disabled { background: %(success_disabled_bg)s;"
        "  color: %(success_disabled_text)s; border-color: %(success_disabled_border)s; }"
        "QPushButton[role=\"secondary\"] { background: %(button2_bg)s; color: %(text)s;"
        "  border: 1px solid %(button2_border)s; border-radius: %(radius)s; padding: 0 12px; }"
        "QPushButton[role=\"secondary\"]:hover { background: %(button2_hover)s;"
        "  border-color: %(accent)s; }"
        "QPushButton[role=\"secondary\"]:disabled { color: %(ghost_disabled_text)s;"
        "  border-color: %(ghost_disabled_border)s; background: %(surface4)s; }"
        "QProgressBar { background: %(surface)s; border: 1px solid %(border)s;"
        "  border-radius: %(radius)s; height: 15px; text-align: center; color: %(text)s; font-size: 12px; }"
        "QProgressBar::chunk { background: %(accent)s; border-radius: %(radius)s; margin: 1px; }"
        "QSlider::groove:horizontal { height: 4px; background: %(border)s; border-radius: %(radius_small)s; }"
        "QSlider::sub-page:horizontal { background: %(accent)s; border-radius: %(radius_small)s; }"
        "QSlider::handle:horizontal { width: 14px; height: 14px; margin: -5px 0;"
        "  background: %(accent)s; border-radius: 7px; }"
        "QSlider::handle:horizontal:hover { background: %(accent_hover)s; }"
        # 竖条滑块：播放页的弹幕透明度
        "QSlider::groove:vertical { width: 4px; background: %(border)s; border-radius: %(radius_small)s; }"
        "QSlider::sub-page:vertical { background: %(border)s; border-radius: %(radius_small)s; }"
        "QSlider::add-page:vertical { background: %(accent)s; border-radius: %(radius_small)s; }"
        "QSlider::handle:vertical { width: 14px; height: 14px; margin: 0 -5px;"
        "  background: %(accent)s; border-radius: 7px; }"
        "QSlider::handle:vertical:hover { background: %(accent_hover)s; }"
        # 小弹层（透明度滑条等）：带边框的浮层，换主题跟着变
        "QFrame[role=\"playerPopup\"] { background: %(surface)s; border: 1px solid %(border)s;"
        "  border-radius: %(radius)s; }"
        # ------------------------------------------------- 选集 / 分集芯片网格
        # 芯片本身由 _EpisodeDelegate 画（QSS 的 ::item 只作用于默认绘制路径），
        # 这里管的是网格容器、工具栏按钮和计数胶囊。
        "QListWidget[role=\"chips\"] { background: transparent; border: none; }"
        "#episodeBoardViewport { background: transparent; }"
        "QPushButton[role=\"ghost\"] { background: transparent; border: 1px solid %(border)s;"
        "  color: %(muted)s; padding: 3px 10px; }"
        "QPushButton[role=\"ghost\"]:hover { background: %(surface3)s; border-color: %(accent)s;"
        "  color: %(text)s; }"
        "QPushButton[role=\"ghost\"]:pressed { background: %(surface2)s; }"
        "QPushButton[role=\"ghost\"]:disabled { background: transparent; border-color: %(border)s;"
        "  color: %(ghost_disabled_text)s; }"
        # 数字胶囊（选中前 [20] 集 / 跳到 __ 集）：整块可点，数字可就地改
        "QWidget[role=\"chipfield\"] { background: %(surface)s; border: 1px solid %(border)s;"
        "  border-radius: %(radius)s; }"
        "QWidget[role=\"chipfield\"]:hover { background: %(surface3)s; border-color: %(accent)s; }"
        "QLineEdit[role=\"tiny\"] { background: transparent; border: none; padding: 3px 2px; }"
        "QLineEdit[role=\"tiny\"]:hover { border: none; }"
        "QLineEdit[role=\"tiny\"]:focus { border: none; border-bottom: 1px solid %(accent)s;"
        "  border-radius: 0; }"
        "QLabel[role=\"section\"] { font-size: 14px; font-weight: bold; }"
        "QLabel[role=\"hint\"] { color: %(muted)s; }"
        "QLabel[role=\"quality\"] { color: %(muted2)s; }"
        # 内容区居中的空态：一行标题 + 浅一档的下一步提示
        "QLabel[role=\"emptyTitle\"] { color: %(text)s; font-size: 14px; }"
        "QLabel[role=\"posterCover\"] { background: %(cover_bg)s; }"
        # 播放器占位/报错：画面区两套主题都保持深色，所以用 video_* 那组 token
        "QLabel[role=\"playerPlaceholder\"] { background: %(video_bg)s; color: %(video_text)s;"
        "  font-size: 15px; }"
        "QLabel[role=\"playerPlaceholder\"][state=\"error\"] { color: %(video_danger)s; }"
        "QLabel[role=\"pill\"] { background: %(accent_soft)s; color: %(accent)s;"
        "  border-radius: 11px; padding: 3px 12px; font-weight: bold; }"
        # 更新状态：更新中走高亮底色，已完结轻一档——只有前者需要扎眼
        "QLabel[role=\"pill\"][tone=\"finished\"] { background: %(surface3)s;"
        "  color: %(muted)s; }"
        "QLabel[role=\"pill\"][empty=\"true\"] { background: transparent; color: %(muted)s;"
        "  font-weight: normal; padding-left: 2px; }"
        # ------------------------------------------- 下载与合并（队列卡片）
        # 队列行整行由 _JobDelegate 画，QSS 只管容器：item 的内外边距必须归零，
        # 否则自绘的卡片和 item 矩形对不上。
        "QListWidget[role=\"queue\"] { background: transparent; border: none; }"
        "#jobQueueViewport { background: transparent; }"
        "QListWidget[role=\"queue\"]::item { padding: 0px; margin: 0px; border: none;"
        "  background: transparent; border-radius: 0px; }"
        "QListWidget[role=\"queue\"]::item:hover,"
        "QListWidget[role=\"queue\"]::item:selected { background: transparent; }"
        "#currentDramaCard { background: %(surface)s; border: 1px solid %(border)s;"
        "  border-radius: %(radius)s; }"
        "QProgressBar[role=\"slim\"] { border: none; background: %(border)s;"
        "  border-radius: %(radius_small)s; height: 8px; }"
        "QProgressBar[role=\"slim\"]::chunk { background: %(accent)s; border-radius: %(radius_small)s;"
        "  margin: 0px; }"
        "QPushButton[role=\"danger\"] { background: transparent; border: 1px solid %(danger_border)s;"
        "  color: %(danger)s; }"
        "QPushButton[role=\"danger\"]:hover { background: %(danger_hover_bg)s;"
        "  border-color: %(danger)s; }"
        "QPushButton[role=\"danger\"]:pressed { background: %(danger_pressed_bg)s; }"
        "QPushButton[role=\"danger\"]:disabled { color: %(ghost_disabled_text)s;"
        "  border-color: %(border)s; background: transparent; }"
        "QListWidget[role=\"dramas\"]::item, QListWidget[role=\"merges\"]::item,"
        "QListWidget[role=\"mergequeue\"]::item { padding: 6px 8px; margin: 2px 4px; }"
        # 左栏的「合并队列 / 合并成品」页签：比主窗口页签矮一档，窄列才放得下
        "QTabWidget[role=\"compact\"]::pane { border: 1px solid %(border)s;"
        "  border-radius: %(radius)s; top: -1px; }"
        "QTabWidget[role=\"compact\"] QTabBar::tab { padding: 4px 10px;"
        "  border-top-left-radius: %(radius_small)s; border-top-right-radius: %(radius_small)s; }"
        # 合并成品是「整行一个控件」，item 不能再留内边距，否则控件被挤扁
        "QListWidget[role=\"merges\"]::item { padding: 0px; margin: 2px 4px; }"
        # --------------------------------------- 追剧 / 观看历史（海报墙）
        # 单击只选中：选中态要看得见，但别用整块实心蓝压住海报
        "QListWidget[role=\"wall\"]::item { padding: 6px 8px; margin: 3px;"
        "  border: 1px solid transparent; }"
        "QListWidget[role=\"wall\"]::item:selected { background: %(accent_soft)s;"
        "  border: 1px solid %(accent)s; color: %(text)s; }"
        # --------------------------------------------------- 新剧 / 日历
        "QWidget[role=\"newSeriesPage\"], QStackedWidget[role=\"newSeriesStack\"], QWidget#newDateContents, QWidget#newListContents,"
        " QWidget#newDateViewport, QWidget#newListViewport, QWidget#newPosterViewport { background: %(bg)s; }"
        "QScrollArea[role=\"newSeriesScroll\"] { border: 0; background: %(bg)s; }"
        "QListWidget[role=\"newPosterWall\"] { background: %(bg)s; border: 0; }"
        "QListWidget[role=\"newPosterWall\"]::item { padding: 0; margin: 0; border: 0; background: transparent; }"
        "QListWidget[role=\"newPosterWall\"]::item:hover, QListWidget[role=\"newPosterWall\"]::item:selected { background: transparent; }"
        "QLabel[role=\"newPageTitle\"] { color: %(text)s; font-size: 18px; font-weight: bold; }"
        "QPushButton[role=\"newTab\"] { background: transparent; color: %(muted)s;"
        " border: 0; border-bottom: 2px solid transparent; border-radius: 0;"
        " padding: 6px 12px; font-size: 15px; }"
        "QPushButton[role=\"newTab\"]:hover { color: %(text)s; background: %(surface3)s; }"
        "QPushButton[role=\"newTab\"]:checked { color: %(text)s; font-weight: bold;"
        " border-bottom: 2px solid %(accent)s; }"
        "QPushButton[role=\"newGender\"] { background: %(button_bg)s; color: %(muted)s;"
        " border: 1px solid %(button_border)s; border-radius: 14px; padding: 0 12px; }"
        "QPushButton[role=\"newGender\"]:hover { background: %(button_hover)s; color: %(text)s; }"
        "QPushButton[role=\"newGender\"]:checked { background: %(accent)s;"
        " border-color: %(accent)s; color: %(on_accent)s; font-weight: bold; }"
        "QFrame[role=\"calendarRow\"] { background: %(surface)s; border: 1px solid %(border)s;"
        " border-radius: %(radius)s; }"
        "QFrame[role=\"calendarRow\"]:hover { border-color: %(button_border)s; }"
        "QLabel[role=\"calendarTitle\"] { color: %(text)s; font-size: 15px; font-weight: bold;"
        " background: transparent; }"
        "QLabel[role=\"calendarMeta\"] { color: %(muted)s; font-size: 12px; background: transparent; }"
        "QLabel[role=\"calendarOnline\"] { color: %(success)s; font-size: 12px;"
        " font-weight: bold; background: transparent; }"
        "QLabel[role=\"calendarPending\"] { color: %(accent)s; font-size: 12px;"
        " font-weight: bold; background: transparent; }"
        "QLabel[role=\"calendarBadge\"] { background: %(accent)s; color: %(on_accent)s;"
        " font-size: 11px; padding: 0 5px; border-radius: 4px; }"
        "QLabel[role=\"newStatus\"] { color: %(muted)s; font-size: 12px; background: transparent; }"
        "QPushButton[role=\"calendarPlay\"], QPushButton[role=\"calendarReserve\"] {"
        " background: %(accent)s; border: 1px solid %(accent)s; color: %(on_accent)s;"
        " border-radius: 7px; padding: 0 9px; font-size: 12px; font-weight: bold; }"
        "QPushButton[role=\"calendarPlay\"]:hover, QPushButton[role=\"calendarReserve\"]:hover {"
        " background: %(accent_hover)s; border-color: %(accent_hover)s; }"
        "QPushButton[role=\"calendarReserve\"] { border-radius: 13px; }"
        "QPushButton[role=\"calendarReserve\"][reserved=\"true\"] { background: transparent;"
        " color: %(muted)s; border-color: %(button_border)s; }"
        "QPushButton[role=\"calendarReserve\"][reserved=\"true\"]:hover {"
        " color: %(danger)s; border-color: %(danger)s; background: %(danger_hover_bg)s; }"
        # ==================== F2 guo- 公共控件样式（全新 guoRole 选择器，不影响既有页面） ====================
        # 海报卡：normal / hover（边框高亮）/ pressed / selected（已看）/ disabled
        "QWidget[guoRole=\"posterCard\"] { background: %(guo_card_bg)s; border: 1px solid %(guo_border)s;"
        "  border-radius: %(guo_radius_lg)s; }"
        "QWidget[guoRole=\"posterCard\"][hover=\"true\"] { border-color: %(guo_accent)s; }"
        "QWidget[guoRole=\"posterCard\"][pressed=\"true\"] { border-color: %(guo_accent_pressed)s;"
        "  background: %(guo_surface3)s; }"
        "QWidget[guoRole=\"posterCard\"][selected=\"true\"] { border-color: %(guo_accent)s; }"
        "QWidget[guoRole=\"posterCard\"]:disabled { border-color: %(border)s; background: %(surface2)s; }"
        # 名次角标（榜单 1-10；前三走 rank1/2/3）/ 更新到 N 集 chip / VIP chip / 小黄点 / 通用 chip
        "QLabel[guoRole=\"rankBadge\"] { background: %(guo_accent)s; color: %(on_accent)s;"
        "  border-radius: %(guo_radius)s; font-size: 11px; font-weight: bold; padding: 1px 5px; }"
        "QLabel[guoRole=\"rankBadge\"][rank=\"1\"] { background: %(rank1)s; }"
        "QLabel[guoRole=\"rankBadge\"][rank=\"2\"] { background: %(rank2)s; }"
        "QLabel[guoRole=\"rankBadge\"][rank=\"3\"] { background: %(rank3)s; }"
        "QLabel[guoRole=\"updateBadge\"] { background: %(guo_badge_bg)s; color: %(guo_accent)s;"
        "  border-radius: 9px; padding: 1px 7px; font-size: 11px; }"
        "QLabel[guoRole=\"vipBadge\"] { background: %(guo_vip_bg)s; color: %(guo_vip_fg)s;"
        "  border-radius: %(guo_radius)s; padding: 1px 5px; font-size: 11px; font-weight: bold; }"
        "QLabel[guoRole=\"vipDot\"] { background: %(score)s; border: none; border-radius: 3px; }"
        "QLabel[guoRole=\"chip\"] { background: %(guo_chip_bg)s; border: 1px solid %(guo_border)s;"
        "  color: %(guo_muted)s; border-radius: 10px; padding: 2px 10px; font-size: 12px; }"
        # 30 集分组条
        "QPushButton[guoRole=\"groupTab\"] { background: %(guo_chip_bg)s; color: %(guo_muted)s;"
        "  border: 1px solid %(guo_border)s; border-radius: 13px; padding: 3px 14px; font-size: 12px; }"
        "QPushButton[guoRole=\"groupTab\"]:hover { background: %(guo_chip_hover)s; color: %(guo_text)s;"
        "  border-color: %(guo_accent)s; }"
        "QPushButton[guoRole=\"groupTab\"][hover=\"true\"] { background: %(guo_chip_hover)s; color: %(guo_text)s;"
        "  border-color: %(guo_accent)s; }"
        "QPushButton[guoRole=\"groupTab\"]:pressed { background: %(guo_chip_pressed)s; }"
        "QPushButton[guoRole=\"groupTab\"][pressed=\"true\"] { background: %(guo_chip_pressed)s; }"
        "QPushButton[guoRole=\"groupTab\"]:checked { background: %(guo_accent_soft)s; color: %(guo_accent)s;"
        "  border-color: %(guo_accent)s; font-weight: bold; }"
        "QPushButton[guoRole=\"groupTab\"]:disabled { background: %(surface2)s; color: %(disabled_text)s;"
        "  border-color: %(border)s; }"
        # 集按钮：current accent 高亮 / watched 置浅 / VIP 用上方小黄点；四态齐
        "QPushButton[guoRole=\"episode\"] { background: %(guo_chip_bg)s; color: %(guo_text)s;"
        "  border: 1px solid %(guo_border)s; border-radius: %(guo_radius)s; font-size: 12px; }"
        "QPushButton[guoRole=\"episode\"]:hover { background: %(guo_chip_hover)s; border-color: %(guo_accent)s; }"
        "QPushButton[guoRole=\"episode\"][hover=\"true\"] { background: %(guo_chip_hover)s; border-color: %(guo_accent)s; }"
        "QPushButton[guoRole=\"episode\"]:pressed { background: %(guo_chip_pressed)s;"
        "  border-color: %(guo_accent_pressed)s; }"
        "QPushButton[guoRole=\"episode\"][pressed=\"true\"] { background: %(guo_chip_pressed)s;"
        "  border-color: %(guo_accent_pressed)s; }"
        "QPushButton[guoRole=\"episode\"][watched=\"true\"] { background: %(surface)s; color: %(guo_muted)s; }"
        "QPushButton[guoRole=\"episode\"][current=\"true\"] { background: %(guo_accent)s; color: %(on_accent)s;"
        "  border-color: %(guo_accent)s; font-weight: bold; }"
        "QPushButton[guoRole=\"episode\"][current=\"true\"]:hover { background: %(guo_accent_hover)s; }"
        "QPushButton[guoRole=\"episode\"][current=\"true\"]:pressed { background: %(guo_accent_pressed)s; }"
        "QPushButton[guoRole=\"episode\"]:disabled { background: %(surface2)s; color: %(disabled_text)s;"
        "  border-color: %(border)s; }"
        # 分段记录头（分组列表小标题，muted）
        "QLabel[guoRole=\"sectionHeader\"] { background: transparent; color: %(guo_muted)s;"
        "  font-size: 12px; font-weight: bold; padding: 10px 2px 2px 2px; }"
        # 站源切换下拉：按钮 + 深底菜单 + 行（hover 高亮，当前项 accent）
        "QPushButton[guoRole=\"sourceSwitch\"] { background: %(guo_chip_bg)s; color: %(guo_text)s;"
        "  border: 1px solid %(guo_border)s; border-radius: %(guo_radius)s; padding: 4px 10px; text-align: left; }"
        "QPushButton[guoRole=\"sourceSwitch\"]:hover { border-color: %(guo_accent)s; background: %(guo_chip_hover)s; }"
        "QPushButton[guoRole=\"sourceSwitch\"][hover=\"true\"] { border-color: %(guo_accent)s; background: %(guo_chip_hover)s; }"
        "QPushButton[guoRole=\"sourceSwitch\"]:pressed { border-color: %(guo_accent)s; background: %(guo_chip_pressed)s; }"
        "QPushButton[guoRole=\"sourceSwitch\"][pressed=\"true\"] { border-color: %(guo_accent)s; background: %(guo_chip_pressed)s; }"
        "QPushButton[guoRole=\"sourceSwitch\"]:checked { border-color: %(guo_accent)s; background: %(guo_chip_pressed)s; }"
        "QPushButton[guoRole=\"sourceSwitch\"]:disabled { background: %(surface2)s; color: %(disabled_text)s;"
        "  border-color: %(border)s; }"
        "QMenu[guoRole=\"sourceMenu\"] { background: %(guo_surface3)s; border: 1px solid %(guo_border)s;"
        "  border-radius: %(guo_radius)s; padding: 4px; }"
        "QMenu[guoRole=\"sourceMenu\"]::item { padding: 0; margin: 0; border-radius: %(guo_radius)s;"
        "  background: transparent; }"
        "QMenu[guoRole=\"sourceMenu\"]::item:hover, QMenu[guoRole=\"sourceMenu\"]::item:selected {"
        "  background: transparent; }"
        "QWidget[guoRole=\"sourceRow\"] { background: transparent; border-radius: %(guo_radius)s; }"
        "QWidget[guoRole=\"sourceRow\"][hover=\"true\"] { background: %(guo_accent_soft)s; }"
        "QLabel[guoRole=\"sourceName\"] { background: transparent; color: %(guo_text)s; padding: 6px 8px; }"
        "QLabel[guoRole=\"sourceName\"][current=\"true\"] { color: %(guo_accent)s; font-weight: bold; }"
        # 控制条浮层（压在画面区上的半透明圆角条）
        "QWidget[guoRole=\"controlOverlay\"] { background: %(guo_player_scrim)s;"
        "  border: 1px solid %(guo_border)s; border-radius: %(guo_radius_lg)s; }"
        "QPushButton[guoRole=\"ctrlBtn\"] { background: transparent; border: 1px solid %(guo_border)s;"
        "  color: %(guo_text)s; border-radius: %(guo_radius)s; padding: 4px 12px; }"
        "QPushButton[guoRole=\"ctrlBtn\"]:hover { border-color: %(guo_accent)s; color: %(guo_accent)s; }"
        "QPushButton[guoRole=\"ctrlBtn\"][hover=\"true\"] { border-color: %(guo_accent)s; color: %(guo_accent)s; }"
        "QPushButton[guoRole=\"ctrlBtn\"]:pressed { background: %(guo_accent_soft)s; color: %(guo_accent)s; }"
        "QPushButton[guoRole=\"ctrlBtn\"][pressed=\"true\"] { background: %(guo_accent_soft)s; color: %(guo_accent)s; }"
        "QPushButton[guoRole=\"ctrlBtn\"]:disabled { color: %(disabled_text)s; border-color: %(border)s; }"
        # 控件自建滚动区：透明容器，滚动条沿用全局段
        "QScrollArea[guoRole=\"bare\"] { border: 0; background: transparent; }"
        "QScrollArea[guoRole=\"bare\"] > QWidget > QWidget { background: transparent; }"
        # -------------------------------------------------------- tooltip
        "QToolTip { background: %(surface3)s; color: %(text)s; border: 1px solid %(button_border)s;"
        "  border-radius: %(radius)s; padding: 6px 8px; font-size: 12px; }"
        # ----------------------------------------------------- scrollbars
        "QScrollBar:vertical { background: transparent; width: 10px; margin: 2px; }"
        "QScrollBar::handle:vertical { background: %(scroll)s; border-radius: 4px; min-height: 30px; }"
        "QScrollBar::handle:vertical:hover { background: %(scroll_hover)s; }"
        "QScrollBar:horizontal { background: transparent; height: 10px; margin: 2px; }"
        "QScrollBar::handle:horizontal { background: %(scroll)s; border-radius: 4px; min-width: 30px; }"
        "QScrollBar::handle:horizontal:hover { background: %(scroll_hover)s; }"
        "QScrollBar::add-line, QScrollBar::sub-line { width: 0; height: 0; }"
        "QScrollBar::add-page, QScrollBar::sub-page { background: transparent; }"
        # ---------------------------------------------------- collapsible browse filters
        "QWidget[role=\"browsePage\"], QWidget[role=\"browseFilters\"] { background: %(bg)s; }"
        "QScrollArea[role=\"filterScroll\"] { background: %(bg)s; border: none; }"
        "QLabel[role=\"newPageTitle\"] { font-size: 20px; font-weight: bold; color: %(text)s; }"
        "QLabel[role=\"filterLabel\"] { color: %(muted2)s; padding-top: 6px; }"
        "QPushButton[role=\"filterChip\"] { background: transparent; border: 1px solid transparent; color: %(muted)s; border-radius: %(radius)s; padding: 4px 11px; }"
        "QPushButton[role=\"filterChip\"]:hover { background: %(surface3)s; color: %(text)s; }"
        "QPushButton[role=\"filterChip\"]:pressed { background: %(surface4)s; color: %(text)s; }"
        "QPushButton[role=\"filterChip\"]:checked { border-color: %(button_border)s; background: %(surface)s; color: %(accent)s; }"
        "QPushButton[role=\"filterChip\"]:disabled { background: transparent; color: %(disabled_text)s; border-color: transparent; }"
        # ---------------------------------------------------- details and creators
        "QWidget[role=\"detailHeader\"], QWidget[role=\"detailContent\"], QWidget[role=\"creatorPage\"] { background: %(bg)s; }"
        "QLabel[role=\"detailTitle\"] { font-size: 22px; font-weight: bold; color: %(text)s; }"
        "QLabel[role=\"detailMetrics\"] { color: %(muted)s; font-size: 13px; }"
        "QLabel[role=\"detailSection\"] { color: %(text)s; font-size: 14px; font-weight: bold; }"
        "QLabel[role=\"detailTag\"] { background: %(surface3)s; color: %(muted)s; border-radius: %(radius)s; padding: 2px 8px; font-size: 12px; }"
        "QTextBrowser[role=\"detailSummary\"] { border: none; background: transparent; color: %(muted)s; padding: 0; font-size: 13px; }"
        "QScrollArea[role=\"detailScroll\"], QScrollArea[role=\"castScroll\"] { border: none; background: %(bg)s; }"
        "QPushButton[role=\"textLink\"] { color: %(muted)s; background: transparent; border: 0; padding: 2px 0; font-size: 12px; }"
        "QPushButton[role=\"textLink\"]:hover { color: %(accent)s; }"
        "QPushButton[role=\"textLink\"]:pressed { color: %(accent_pressed)s; }"
        "QPushButton[role=\"textLink\"]:disabled { color: %(disabled_text)s; }"
        "QLabel[role=\"creatorStats\"] { color: %(text)s; font-size: 16px; font-weight: bold; padding: 8px 0; }"
        # ------------------------------------------------------- home rank list
        "QListWidget[role=\"rankList\"] { background: transparent; border: 0; }"
        "QLabel[role=\"rankUpdated\"] { color: %(muted2)s; font-size: 11px; }"
        "QScrollBar[role=\"rankScroll\"]:vertical { background: transparent; width: 6px; margin: 2px 0; }"
        "QScrollBar[role=\"rankScroll\"]::handle:vertical { background: transparent;"
        " border-radius: %(radius)s; min-height: 36px; }"
        "QScrollBar[role=\"rankScroll\"][revealed=\"true\"]::handle:vertical { background: %(scroll)s; }"
        "QScrollBar[role=\"rankScroll\"][revealed=\"true\"]::handle:vertical:hover { background: %(scroll_hover)s; }"
        "QScrollBar[role=\"rankScroll\"][revealed=\"true\"]::handle:vertical:pressed { background: %(muted)s; }"
        "QScrollBar[role=\"rankScroll\"]::handle:vertical:disabled { background: transparent; }"
        # -------------------------------------------------------- chrome
        "QSplitter::handle { background: %(surface4)s; }"
        "QSplitter::handle:vertical { height: 4px; }"
        "QSplitter::handle:horizontal { width: 4px; }"
        "QStatusBar { background: %(surface2)s; color: %(muted)s; }"
        "QStatusBar::item { border: none; }"
        "QMessageBox { background: %(surface2)s; }"
        % d
    )
