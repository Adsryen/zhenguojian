"""Main window: 剧库推荐 / 搜索 / 播放 / 下载 / 合并 in one desktop app."""
from __future__ import annotations

import json
import os
import sys
import threading
import time
import traceback

from PyQt5.QtCore import QEvent, QLockFile, QSettings, Qt, QTimer, QTranslator, QLibraryInfo, pyqtSignal
from PyQt5.QtGui import QBrush, QColor, QFont, QIcon, QKeySequence, QPainter, QPen, QPixmap
from PyQt5.QtWidgets import (QAbstractSpinBox, QAction, QApplication, QCheckBox, QComboBox, QFileDialog,
                             QFrame, QHBoxLayout, QLabel, QLineEdit, QListWidget, QListWidgetItem,
                             QMainWindow, QMenu, QMessageBox, QPushButton, QShortcut, QSplashScreen,
                             QSplitter, QStatusBar, QSystemTrayIcon, QTabWidget, QTextEdit,
                             QVBoxLayout, QWidget, QScrollArea, QStackedWidget, QSizePolicy)

from . import danmaku, decrypt, library, media, series, tools, ui
from .browse_panel import BrowsePanel
from .calendar_panel import CalendarPanel
from .core_client import CoreClient, CoreError
from .downloads import DownloadPanel
from .player import PlayerPane
from .poster_tooltip import poster_tooltip
from .flow_layout import FlowLayout
from .creators import CastStrip, CreatorPage, RoundedPoster, AvatarLoader
from .detail_episodes import DetailEpisodeList
from .detail_layout import DetailSummary
from .rank_list import RankList
from .recommendation_panel import RecommendationPanel
from .scroll_loader import ScrollLoader
from .tasks import TaskRunner
from .ui import build_stylesheet
from .watch import WatchPanel
from .watchstore import WatchStore, season_number
from .widgets import CoverGrid, CoverLoader, EpisodeList, SummaryView
from .widgets_guo import SourceSwitchPopup

APP_NAME = "真果鉴"
GENRES = [("", "全部"), ("short_play", "真人剧"), ("comic_series", "漫剧"), ("ai_series", "AI剧")]
# 下一集预热最多留两份（核心每集上限 4MB，纯内存，不写磁盘）；
# 停止播放、换剧、过期或退出应用时都会释放对应会话。
PRELOAD_LIMIT = 2

# ---------------------------------------------------------------- M1 站源上下文
# 站源清单优先走 core_client.list_sources()（F1 并行新增）；拿不到时回退下面这张
# 12 展示源（14 个内部 id，黄果三变体拆开列）硬编码表，implemented 标记见
# research/source-map.md ①：韩小圈/鬼片/禁果短剧/禁果/黑桃 本地无 provider。
DEFAULT_SOURCE = "hongguo"
FALLBACK_SOURCES = (
    ("hongguo", "红果", True),
    ("hanxiaoquan", "韩小圈", False),
    ("guipian", "鬼片", False),
    ("sorani", "青空", True),
    ("huangdou", "黄豆", True),
    ("huangju", "剧果", True),
    ("yeguo", "野果", True),
    ("dsd", "帝果", True),
    ("jinguoduanju", "禁果短剧", False),
    ("jinguo", "禁果", False),
    ("heitao", "黑桃", False),
    ("huangguo-video", "黄果·视频", True),
    ("huangguoai", "黄果·AI", True),
    ("cloudfront", "黄果·旧版", True),
)

# org/app 与 player.py 的 QSettings 约定保持一致，站源开关(S1)等键互通。
SETTINGS_ORG = "zhenguojian"
SETTINGS_APP = "zhenguojian"
RECENT_KEY = "search/recent"            # JSON: {slug: [关键词, ...]}，每源最多 RECENT_LIMIT 条
SOURCE_CURRENT_KEY = "source/current"   # 上次使用的站源，读不到时回 DEFAULT_SOURCE
RECENT_LIMIT = 10

# P1c：侧栏 quality_box 的四个位置 ↔ PlayerPane 的清晰度 slug（顺序与
# ["自动","1080P","720P","540P"] 一致，数值映射由 player.quality_level 提供）。
QUALITY_INDEX_SLUGS = ("auto", "1080p", "720p", "540p")


def _app_settings():
    return QSettings(SETTINGS_ORG, SETTINGS_APP)


def _recent_map():
    """M1c：读取 {slug: [关键词]}；QSettings 可能直接回 dict 或 JSON 串，坏数据当空。"""
    raw = _app_settings().value(RECENT_KEY, "") or ""
    if isinstance(raw, dict):
        data = raw
    else:
        try:
            data = json.loads(str(raw))
        except (ValueError, TypeError):
            return {}
    if not isinstance(data, dict):
        return {}
    out = {}
    for slug, words in data.items():
        if isinstance(words, list):
            cleaned = [str(w).strip() for w in words if str(w).strip()]
            if cleaned:
                out[str(slug)] = cleaned[:RECENT_LIMIT]
    return out


def _record_recent(slug, keyword):
    """M1c：关键词去重后置顶写入当前站源，只保留最近 RECENT_LIMIT 条。"""
    keyword = str(keyword or "").strip()
    if not keyword or not slug:
        return
    data = _recent_map()
    words = [w for w in data.get(slug, []) if w != keyword]
    words.insert(0, keyword)
    data[slug] = words[:RECENT_LIMIT]
    try:
        _app_settings().setValue(RECENT_KEY, json.dumps(data, ensure_ascii=False))
    except Exception:
        pass  # 最近搜索是锦上添花，写失败不能影响搜索本身


class Poster(QFrame):
    """Detail page header: cover, title, meta and action buttons."""

    seasonChosen = pyqtSignal(object)

    def __init__(self, parent=None):
        super().__init__(parent)
        self.drama = None
        self.cover = RoundedPoster()
        self.cover.setFixedSize(198, 264)
        self.cover.setScaledContents(True)
        self.cover.setProperty("role", "posterCover")
        self.title = QLabel("未选择剧集")
        self.title.setProperty("role", "detailTitle")
        self.title.setWordWrap(True)
        self.title.setTextFormat(Qt.PlainText)
        # 更新状态放在 meta 行开头：跟标题挤一行的话，剧名一长就会把它顶到
        # 最右边去，看着跟标题没关系了
        self.status_pill = QLabel("")
        self.status_pill.setProperty("role", "pill")
        self.status_pill.setVisible(False)
        self.meta = QLabel("")
        self.meta.setProperty("role", "detailMetrics")
        self.meta.setWordWrap(True)
        meta_row = QHBoxLayout()
        meta_row.setContentsMargins(0, 0, 0, 0)
        meta_row.setSpacing(8)
        meta_row.addWidget(self.status_pill, 0)
        meta_row.addWidget(self.meta, 1)
        # 采样出来的画质单独一行：和 meta 挤在一起会在长剧名 / 长分类下被截断
        self.quality = QLabel("")
        self.quality.setProperty("role", "quality")
        self.quality.setWordWrap(True)
        self.quality.setVisible(False)
        self.summary = DetailSummary()
        self.summary.setProperty("role", "detailSummary")
        self.summary_toggle = QPushButton("展开简介")
        self.summary_toggle.setProperty("role", "textLink")
        self.summary_toggle.setCheckable(True)
        self.summary_toggle.hide()
        self.summary.overflowChanged.connect(self.summary_toggle.setVisible)
        self.summary_toggle.toggled.connect(self._toggle_summary)
        self.tag_holder = QWidget()
        self.tags = FlowLayout(self.tag_holder, spacing=6)

        self.play_first = QPushButton("▶ 播放第一集")
        self.play_first.setProperty("role", "accent")
        self.follow_button = QPushButton("＋ 加入追剧")
        self.reserve_button = QPushButton("☆ 预约")
        self.reserve_button.setToolTip("预约更新/下一季，上线后在「追更/预约」页提醒")
        self.season_button = QPushButton("")
        self.season_button.setToolTip("同系列的其它季，点开直接切换")
        self.season_button.setVisible(False)
        self.download_all = QPushButton("下载全剧")
        self.download_all.setProperty("role", "success")
        self.save_cover = QPushButton("↓ 高清海报")
        self.save_cover.setToolTip("把这部剧的海报存成 JPG / PNG 图片")

        self.copy_summary = QPushButton("复制简介")
        self.copy_summary.clicked.connect(lambda: QApplication.clipboard().setText(self.summary.toPlainText()))
        buttons = FlowLayout(spacing=6)
        buttons.addWidget(self.season_button)
        buttons.addWidget(self.play_first)
        buttons.addWidget(self.follow_button)
        buttons.addWidget(self.reserve_button)
        buttons.addWidget(self.download_all)
        buttons.addWidget(self.save_cover)
        buttons.addWidget(self.copy_summary)

        text = QVBoxLayout()
        text.setContentsMargins(0, 0, 0, 0)
        text.setSpacing(12)
        text.addWidget(self.title)
        text.addLayout(meta_row)
        text.addWidget(self.tag_holder)
        text.addWidget(self.summary)
        text.addWidget(self.summary_toggle, 0, Qt.AlignLeft)
        text.addWidget(self.quality)
        text.addLayout(buttons)
        text.addStretch(1)

        layout = QHBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(24)
        layout.addWidget(self.cover, 0, Qt.AlignTop)
        layout.addLayout(text, 1)

    def _toggle_summary(self, expanded):
        self.summary.set_expanded(expanded)
        self.summary_toggle.setText("收起简介" if expanded else "展开简介")

    def set_tags(self, tags):
        while self.tags.count():
            item = self.tags.takeAt(0)
            if item.widget(): item.widget().deleteLater()
        for tag in list(dict.fromkeys(str(tag).strip() for tag in tags if str(tag).strip()))[:14]:
            label = QLabel(tag)
            label.setProperty("role", "detailTag")
            self.tags.addWidget(label)
        self.tag_holder.setVisible(bool(self.tags.count()))

    def set_release_status(self, status):
        """标题旁的更新状态。核心已经把 series_status 映射成 ongoing / finished。

        未知就不显示——写成"状态未知"只是噪音。改了动态属性要重新 polish，
        QSS 才会重新匹配到 tone。
        """
        text = {"ongoing": "更新中", "finished": "已完结"}.get((status or "").strip(), "")
        self.status_pill.setText(text)
        self.status_pill.setProperty("tone", "finished" if status == "finished" else "ongoing")
        self.status_pill.setVisible(bool(text))
        self.status_pill.style().unpolish(self.status_pill)
        self.status_pill.style().polish(self.status_pill)

    def set_seasons(self, rows, current_id=None):
        """显示「全 N 季」入口；只有一季或没查到时隐藏。"""
        rows = list(rows or [])
        previous = getattr(self, "_season_menu", None)
        if previous is not None:
            self.season_button.setMenu(None)
            previous.deleteLater()
            self._season_menu = None
        if len(rows) <= 1:
            self.season_button.setVisible(False)
            return
        menu = QMenu(self.season_button)
        for row in rows:
            drama = row.get("drama") or {}
            number = int(row.get("season") or 1)
            label = series.season_label(number, row.get("unit") or "季")
            episodes = int(row.get("episodes") or drama.get("episodes") or 0)
            text = label + (" · %d 集" % episodes if episodes else "")
            # 勾直接写进文字：QMenu 的可勾选动作会再画一个指示器，配文字勾会变成两个
            action = menu.addAction(
                ("✓ " if current_id and drama.get("id") == current_id else "") + text)
            action.triggered.connect(
                lambda _checked=False, item=drama: self.seasonChosen.emit(item))
        self._season_menu = menu
        self.season_button.setMenu(menu)
        self.season_button.setText("全 %d 季 ▾" % len(rows))
        self.season_button.setVisible(True)


class DetailPage(QWidget):
    dramaSelected = pyqtSignal(object)
    playEpisode = pyqtSignal(object, int)
    downloadEpisodes = pyqtSignal(object, list)
    statusMessage = pyqtSignal(str)
    followRequested = pyqtSignal(object)
    reserveRequested = pyqtSignal(object)
    saveCoverRequested = pyqtSignal(object)
    seasonSelected = pyqtSignal(object)
    # 画质采样在 worker 线程里做；这个信号负责把结果带回 GUI 线程
    qualityReady = pyqtSignal(str, str)

    def __init__(self, core, covers, watch, parent=None):
        super().__init__(parent)
        self._core = core
        self._covers = covers
        self._watch = watch
        self._chapters = []
        self._drama_id = None
        self._load_error = None
        self._quality_text = ""
        self._quality_serial = 0
        self.qualityReady.connect(self._apply_quality)

        self.poster = Poster()
        self.poster.play_first.clicked.connect(self._play_first)
        self.poster.download_all.clicked.connect(self._download_all)
        self.poster.follow_button.clicked.connect(self._toggle_follow)
        self.poster.reserve_button.clicked.connect(self._toggle_reserve)
        self.poster.save_cover.clicked.connect(self._request_cover_save)
        self.poster.seasonChosen.connect(self.seasonSelected)
        watch.changed.connect(self.refresh_follow_state)
        self.episodes = DetailEpisodeList()
        self.episodes.playRequested.connect(self._play_position)
        self.episodes.downloadRequested.connect(self._download_selected)

        self.loading = QLabel("正在加载分集…")
        self.loading.setAlignment(Qt.AlignCenter)
        self.loading.setProperty("role", "hint")

        self._cast_runner = TaskRunner(self)
        self._cast_token = 0
        self._episode_durations = {}
        self._creator_stats = {}
        self._avatars = AvatarLoader(core, self)
        self.cast = CastStrip(self._avatars)
        self.cast.creatorActivated.connect(self._show_creator)
        self.record_number = QLabel("")
        self.record_number.setProperty("role", "hint")
        self.record_number.setWordWrap(True)
        self.record_number.hide()
        self.detail_content = QWidget()
        self.detail_content.setProperty("role", "detailContent")
        content_layout = QVBoxLayout(self.detail_content)
        content_layout.setContentsMargins(24, 24, 24, 28)
        content_layout.setSpacing(22)
        content_layout.addWidget(self.poster)
        content_layout.addWidget(self.cast)
        content_layout.addWidget(self.record_number)
        self.episodes.setProperty("role", "detailEpisodeSection")
        content_layout.addWidget(self.loading)
        content_layout.addWidget(self.episodes)
        content_layout.addStretch(1)
        self.detail_scroll = QScrollArea()
        self.detail_scroll.setWidgetResizable(True)
        self.detail_scroll.setFrameShape(QScrollArea.NoFrame)
        self.detail_scroll.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.detail_scroll.setProperty("role", "detailScroll")
        self.detail_scroll.setWidget(self.detail_content)
        self.episodes.set_scroll_area(self.detail_scroll)
        self.creator_page = CreatorPage(core, self._avatars)
        self.creator_page.backRequested.connect(lambda: self.detail_stack.setCurrentIndex(0))
        self.creator_page.dramaActivated.connect(self.dramaSelected.emit)
        self.detail_stack = QStackedWidget()
        self.detail_stack.addWidget(self.detail_scroll)
        self.detail_stack.addWidget(self.creator_page)
        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.addWidget(self.detail_stack)

    def _show_creator(self, creator):
        self.detail_stack.setCurrentIndex(1)
        self.creator_page.show_creator(creator)

    def _load_creators(self, drama):
        self._cast_token += 1
        token = self._cast_token
        if not str(drama.get("id") or "").startswith("hongguo:") or not hasattr(self._core, "drama_creators"):
            self.cast.clear()
            return
        self.cast.clear(loading=True)
        def done(result):
            if token != self._cast_token: return
            result = result or {}
            self.cast.set_creators(result.get("creators") or [])
            self._episode_durations = result.get("durations") or {}
            for index, chapter in enumerate(self._chapters):
                chapter["duration"] = self._episode_durations.get(str(index+1), 0)
            self.episodes.list.viewport().update()
            self.record_number.setText(result.get("recordNumber") or "")
            self.record_number.setVisible(bool(result.get("recordNumber")))
            stats = []
            for key, label in (("heat", "热度"), ("followers", "人追剧"), ("plays", "次播放")):
                if result.get(key): stats.append("%s %s" % (result[key], label))
            self._creator_stats = result
            self._render_meta(self.poster.drama or drama)
        def failed(message):
            if token != self._cast_token: return
            self.cast.clear()
            self.statusMessage.emit("演职员信息暂不可用：%s" % message)
        self._cast_runner.run(lambda: self._core.drama_creators(drama), on_success=done, on_error=failed)

    def shutdown(self):
        self._cast_token += 1
        self.creator_page.shutdown()
        self._cast_runner.shutdown(800)
        self._avatars.stop()

    def reload_theme(self):
        self.creator_page.reload_theme()
        for card in self.cast._cards.values(): card.update()

    def show_drama(self, drama):
        self.detail_stack.setCurrentIndex(0)
        self.detail_scroll.verticalScrollBar().setValue(0)
        self.poster.summary_toggle.setChecked(False)
        self._episode_durations = {}
        self._creator_stats = {}
        self.record_number.hide()
        self._chapters = []
        self._load_error = None
        self._drama_id = drama.get("id")
        self._quality_serial += 1        # 上一部剧还在采样的话作废
        self._set_quality_text("")
        self.poster.drama = drama
        self.poster.set_seasons([], None)
        title = (drama.get("title") or "短剧").strip()
        self.poster.title.setText(title)
        self.refresh_follow_state()
        self._render_meta(drama)
        self.poster.summary.setPlainText(drama.get("description") or "暂无简介")
        self.poster.cover.setToolTip(poster_tooltip(drama))
        self.poster.cover.setPixmap(QPixmap())
        cached = self._covers.cached(self._drama_id)
        if cached is not None:
            self.poster.cover.setPixmap(cached)
        else:
            self._covers.request(drama)
        self.episodes.set_chapters([])
        self.loading.setText("正在加载分集…")
        self.loading.setVisible(True)
        self._load_creators(drama)

    def _toggle_follow(self):
        if self.poster.drama:
            self.followRequested.emit(self.poster.drama)

    def _toggle_reserve(self):
        if self.poster.drama:
            self.reserveRequested.emit(self.poster.drama)

    def _request_cover_save(self):
        if self.poster.drama:
            self.saveCoverRequested.emit(self.poster.drama)

    def set_series(self, rows):
        """把后台查到的同系列季列表挂到头部按钮上。"""
        current_id = (self.poster.drama or {}).get("id")
        self.poster.set_seasons(rows, current_id)

    def refresh_follow_state(self):
        followed = self._watch.is_followed(self._drama_id)
        self.poster.follow_button.setText("✓ 已在追剧" if followed else "＋ 加入追剧")
        reserved = self._watch.is_reserved(self._drama_id)
        if reserved:
            self.poster.reserve_button.setText("✓ 已预约")
        else:
            drama = self.poster.drama or {}
            known = int(drama.get("episodes") or 0)
            if known <= 0:
                self.poster.reserve_button.setText("☆ 预约上线")
            elif str(drama.get("releaseStatus") or "") == "finished":
                self.poster.reserve_button.setText("☆ 预约下一季")
            else:
                self.poster.reserve_button.setText("☆ 预约更新")
        self.refresh_marks()

    def refresh_marks(self):
        """已下载 / 上次看到哪一集 —— 打开详情页和切回这一页时都对一次账。"""
        drama = self.poster.drama or {}
        self.episodes.set_downloaded(library.downloaded_episodes(drama.get("id")))
        entry = self._watch.watched(drama.get("id")) or {}
        episode = entry.get("episode")
        position = None
        if not isinstance(episode, bool) and isinstance(episode, (int, float)) and episode > 0:
            index = int(episode) - 1
            if index < len(self._chapters):
                position = index
        duration = float(entry.get("duration") or 0.0)
        progress = float(entry.get("position") or 0.0) / duration if duration > 1 else 0.0
        self.episodes.set_watched(position, progress)

    def _render_meta(self, drama):
        meta = []
        if drama.get("category"):
            meta.append(drama["category"])
        status = (drama.get("releaseStatus") or "").strip()
        if drama.get("episodes"):
            # 更新中的剧写成「已更新 N 集」：写「共 N 集」会让人以为完结了，
            # 而它其实还会往上涨
            meta.append("已更新 %s 集" % drama["episodes"] if status == "ongoing"
                        else "共 %s 集" % drama["episodes"])
        if drama.get("heat"):
            meta.append("热度 %s" % drama["heat"])
        if drama.get("onlineDate"):
            meta.append("%s 上线" % str(drama["onlineDate"])[:10])
        stats = ["%s %s" % (self._creator_stats[key], label)
                 for key,label in (("heat", "热度"), ("followers", "人追剧"), ("plays", "次播放"))
                 if self._creator_stats.get(key)]
        self.poster.meta.setText("    ".join(stats) if stats else " · ".join(meta))
        tags = list(drama.get("tags") or [drama.get("category") or ""])
        if drama.get("episodes"): tags.insert(0, "共%s集" % drama["episodes"])
        self.poster.set_tags(tags)
        self.poster.set_release_status(status)

    def _set_quality_text(self, text):
        self._quality_text = text or ""
        self.poster.quality.setText(self._quality_text)
        self.poster.quality.setVisible(bool(self._quality_text))

    def _apply_quality(self, drama_id, text):
        # 采样期间用户可能已经翻到别的剧了
        if drama_id == self._drama_id:
            self._set_quality_text(text)

    def _probe_quality(self, drama, chapters):
        """后台抽几集看真实画质，结果补到头部一行。

        接口一次最多收 15 个视频 ID，所以取首/四分位/尾共 5 集、一次请求（约 250ms）
        就够；画质是装饰性信息，拿不到就不显示，也绝不让详情页等它。
        """
        self._quality_serial += 1
        serial = self._quality_serial
        total = len(chapters)
        if not total:
            return
        picks = sorted({0, total // 4, total // 2, total * 3 // 4, total - 1})
        sample = [chapters[index] for index in picks if 0 <= index < total]
        drama_id = drama.get("id")

        def work():
            text = ""
            try:
                items = self._core.chapter_quality(drama, sample)
                text = media.quality_summary(items, sample)
            except Exception:
                text = ""      # 画质标注失败不该弹错
            if serial == self._quality_serial:
                self.qualityReady.emit(drama_id, text)

        threading.Thread(target=work, daemon=True).start()

    def set_cover(self, drama_id, pixmap):
        """Cover finished loading; show it if this drama is still on display."""
        if pixmap is not None and drama_id == self._drama_id:
            self.poster.cover.setPixmap(pixmap)

    def set_chapters(self, drama, chapters):
        for index, chapter in enumerate(chapters):
            chapter["duration"] = self._episode_durations.get(str(index+1), 0)
        self._chapters = chapters
        self._load_error = None
        self.poster.drama = drama
        self.poster.title.setText(drama.get("title") or "短剧")
        if self.poster.summary.toPlainText() != (drama.get("description") or "暂无简介"):
            self.poster.summary_toggle.setChecked(False)
            self.poster.summary.setPlainText(drama.get("description") or "暂无简介")
        self.poster.cover.setToolTip(poster_tooltip(drama))
        self._render_meta(drama)
        self.loading.setVisible(False)
        self.episodes.set_chapters(chapters)
        self.refresh_marks()
        self._probe_quality(drama, chapters)

    def chapters(self):
        """Chapters of the drama currently shown (empty while loading)."""
        return list(self._chapters)

    def drama(self):
        return self.poster.drama

    def fail(self, message):
        self._load_error = message
        self.loading.setVisible(True)
        self.loading.setText(message)

    def _require_chapters(self):
        """Chapter list when loaded; otherwise tell the user why nothing runs."""
        if self._chapters:
            return self._chapters
        if self._load_error:
            self.statusMessage.emit("分集加载失败：%s" % self._load_error)
        else:
            self.statusMessage.emit("分集列表尚未加载完成，请等列表出现后再试")
        return None

    def _play_first(self):
        if self._require_chapters() is not None:
            self.playEpisode.emit(self.poster.drama, 0)

    def _play_position(self, position):
        if self._require_chapters() is not None:
            self.playEpisode.emit(self.poster.drama, position)

    def _download_all(self):
        if self._require_chapters() is not None:
            self.downloadEpisodes.emit(self.poster.drama, list(range(len(self._chapters))))

    def _download_selected(self, positions):
        if self._require_chapters() is not None:
            self.downloadEpisodes.emit(self.poster.drama, positions)


class MainWindow(QMainWindow):
    # 核心在工作线程里重启完成；用信号把提示切回 GUI 线程。
    restartNotice = pyqtSignal(str)

    def __init__(self, core):
        super().__init__()
        self._core = core
        self.restartNotice.connect(lambda text: self.statusBar().showMessage(text, 8000))
        self._runner = TaskRunner(self)
        self._covers = CoverLoader(core, self)
        self._covers.loaded.connect(self._on_cover)
        self._covers.failed.connect(self._on_cover_failed)
        self._play_session = None
        self._playback = None
        self._play_request = 0
        # 下一集预热：key -> {"plan": plan, "at": 时间}。只放内存，不落盘；
        # 停止播放、换剧、过期或退出时释放对应会话。
        self._preloads = {}
        self._preloading = set()
        self._local_path = None
        self._closed = False
        self._video_fullscreen = False
        self._fullscreen_was_maximized = False
        self._pending_play = None
        self._auto_paused = False     # 离开播放页时替用户按的那个暂停
        self._tab_index = 0
        self._watch = WatchStore(self)
        self._watch_last_save = 0.0
        # 弹幕：每换一集就发一个新编号，晚到的抓取结果按编号丢弃
        self._danmaku_job = 0
        self._danmaku_state = {}
        # Request guards: the core cancels an older read when a newer one
        # arrives on the same lane, so a superseded callback must never paint
        # its (canceled) outcome over the newer request's.
        self._search_token = 0
        self._detail_token = 0
        self._series_token = 0
        # series key -> (checksum/time, rows)；详情页来回切换时不用重复搜索
        self._series_cache = {}
        self._next_season_busy = set()
        self._next_season_prompted = set()
        self._search_pending = ""
        self._suggest_token = 0
        # M1：current_source 是搜索与目录请求的唯一站源。剧卡自带的 source
        # （打开详情、查季列表用）不受它影响，这里只管"主动检索"的范围。
        self.current_source = self._restore_current_source()
        self._sources = self._fallback_sources()
        # M1e：设置页懒加载状态（S1 的 settings_panel 并行开发中）
        self._settings_panel = None
        self._settings_tried = False

        self.setWindowTitle("%s · 红果短剧下载器" % APP_NAME)
        self.resize(1180, 780)

        self.tabs = QTabWidget()
        self.tabs.currentChanged.connect(self._on_tab)
        self.tabs.blockSignals(True)
        self.library_page = self._build_library()
        self.browse_page = self._build_browse()
        self.search_page = self._build_search()
        self.player_page = self._build_player()
        self.tabs.addTab(self.library_page, "剧库推荐")
        self.tabs.addTab(self.browse_page, "找剧")
        self.tabs.addTab(self.search_page, "搜索")
        self.tabs.addTab(self._build_detail(), "剧集详情")
        self.tabs.addTab(self.player_page, "在线播放")
        self.tabs.addTab(self._build_watch(), "追更/预约")
        self.tabs.addTab(self._build_history(), "观看历史")
        self.downloads_page = self._build_downloads()
        self.tabs.addTab(self.downloads_page, "下载与合并")
        self.tabs.addTab(self._build_calendar(), "新剧预约")
        self.settings_page = QWidget()  # M1e：首次进入才挂 SettingsPanel，失败留占位
        self.tabs.addTab(self.settings_page, "设置")
        self.tabs.blockSignals(False)
        # 视觉壳微调（只动 main.py 侧，不改 ui.py）：10 个页签不再平铺拉宽，
        # 放不下时出滚动箭头；guoRole 留给后续 ui.py 主题迭代做样式钩子。
        self.tabs.setProperty("guoRole", "mainTabs")
        self.tabs.tabBar().setExpanding(False)
        self.tabs.tabBar().setUsesScrollButtons(True)
        self.tabs.tabBar().setCursor(Qt.PointingHandCursor)
        self.setCentralWidget(self.tabs)
        # 建页签时信号是屏蔽的，currentChanged 一次都没发：剧库页在首屏，
        # 榜单得自己起一次加载，否则左边整列是空的。
        self._load_boards()

        self.setStatusBar(QStatusBar())
        self.statusBar().showMessage("核心就绪 v%s" % (core.version or "?"))
        self._danmaku_timer = QTimer(self)
        self._danmaku_timer.setInterval(1000)
        self._danmaku_timer.timeout.connect(self._pump_danmaku)
        self._danmaku_timer.start()
        # 拖透明度滑条时不要每一像素都重建一遍 ASS，停手后再刷
        self._danmaku_opacity_timer = QTimer(self)
        self._danmaku_opacity_timer.setSingleShot(True)
        self._danmaku_opacity_timer.setInterval(120)
        self._danmaku_opacity_timer.timeout.connect(self._refresh_danmaku_track)
        self._danmaku_opacity_value = tools.danmaku_opacity()
        self._danmaku_opacity_save = QTimer(self)
        self._danmaku_opacity_save.setSingleShot(True)
        self._danmaku_opacity_save.setInterval(500)
        self._danmaku_opacity_save.timeout.connect(
            lambda: tools.set_danmaku_opacity(self._danmaku_opacity_value))
        # 追剧后台检查：默认 30 分钟一轮，带 60 秒延迟避开启动高峰。
        # 环境变量只用于本地调试，不影响正常用户。
        self._follow_busy = False
        self._follow_timer = QTimer(self)
        try:
            interval = int(os.environ.get("ZHENGUO_FOLLOW_INTERVAL", "1800"))
        except ValueError:
            interval = 1800
        self._follow_timer.setInterval(max(60, interval) * 1000)
        self._follow_timer.timeout.connect(self._check_follows)
        self._follow_timer.start()
        QTimer.singleShot(60_000, self._check_follows)
        self.theme_button = QPushButton()
        self.theme_button.setProperty("role", "ghost")
        self.theme_button.setToolTip("在深色 / 浅色之间切换，选择会被记住")
        self.theme_button.clicked.connect(self._toggle_theme)
        self._sync_theme_button()
        self.statusBar().addPermanentWidget(self.theme_button)

        # 预约提醒：应用在后台/最小化时用托盘气泡 + 任务栏闪烁提示；
        # 应用完全退出后没有常驻进程，不会推送（本地预约的边界）。
        self._tray = None
        if QSystemTrayIcon.isSystemTrayAvailable():
            self._tray = QSystemTrayIcon(QIcon(tools.app_icon()), self)
            self._tray.setToolTip(APP_NAME)
            menu = QMenu(self)
            show_action = QAction("显示主窗口", menu)
            show_action.triggered.connect(self._show_from_tray)
            menu.addAction(show_action)
            quit_action = QAction("退出", menu)
            quit_action.triggered.connect(QApplication.quit)
            menu.addAction(quit_action)
            self._tray.setContextMenu(menu)
            self._tray.activated.connect(self._on_tray_activated)
            self._tray.show()

        self._watch_alert_count = 0
        self._watch.changed.connect(self._sync_watch_tab)
        self._sync_watch_tab()

        # No menu bar: 页签行就是窗口第一行 (打开下载目录 / 下载位置 live on the
        # 下载与合并 page, which is where they belong).
        QTimer.singleShot(200, self._load_recommendations)
        # 索引里可能有文件早就删掉的成品记录，后台对一次账（有变化才提示）。
        self._runner.run(library.reconcile_index, on_success=self._on_index_reconciled)

    def _sync_watch_tab(self):
        """追剧页签显示未看的上线/更新数；从 0 变多时闪一下任务栏。"""
        count = self._watch.unseen_arrivals()
        index = self.tabs.indexOf(self.follow_panel)
        if index >= 0:
            self.tabs.setTabText(index, "追更/预约" + (" (%d)" % count if count else ""))
        previous = int(getattr(self, "_watch_alert_count", 0))
        if count > previous:
            QApplication.alert(self, 0)
            self.statusBar().showMessage(
                "有 %d 部追剧有新内容，去「追更/预约」页看看" % count, 8000)
            if self._tray is not None:
                self._tray.showMessage("追剧更新",
                                       "有 %d 部追剧有新内容，可在「追更/预约」页查看" % count,
                                       QSystemTrayIcon.Information, 8000)
        self._watch_alert_count = count

    def _check_follows(self):
        """后台刷新追剧列表：有新集/新季才提示，失败不打扰用户。"""
        if self._follow_busy:
            return
        rows = [row for row in self._watch.follows()
                if row.get("status") in ("想看", "在看", "预约中")
                or row.get("reserved")][:50]
        if not rows:
            return
        self._follow_busy = True
        before = {row["id"]: (int(row.get("known") or 0), bool(row.get("newSeason")),
                              bool(row.get("arrived")), int(row.get("newEpisodes") or 0))
                  for row in rows}

        def work():
            updated = failed = 0
            cache = {}
            lookups = 0

            def season_rows(title, source):
                key = series.series_key(title)
                if key in cache:
                    return cache[key]
                result = self._core.catalog(query=series.base_title(title) or title,
                                            page=1, source=source or "hongguo",
                                            lane="series:auto")
                rows = series.group((result or {}).get("items") or [], title)
                cache[key] = rows
                return rows

            for row in rows:
                drama = row.get("drama") or {}
                try:
                    result = self._core.detail(drama) or {}
                    merged = dict(result.get("drama") or drama)
                    self._watch.observe_drama(merged)
                    # 预约的是“下一季”时，详情接口只能确认当前季；再按标题
                    # 搜一次同系列，找到目标季才把这条预约标成已上线。
                    fresh = next((item for item in self._watch.follows()
                                  if item["id"] == row["id"]), None)
                    target = int((fresh or row).get("reserveSeason") or 0)
                    current = int((fresh or row).get("season") or 1)
                    if (fresh and fresh.get("reserved") and not fresh.get("arrived")
                            and target > current and lookups < 12):
                        lookups += 1
                        found_rows = season_rows(merged.get("title") or drama.get("title"),
                                                 merged.get("source") or drama.get("source"))
                        found = (series.find_season(found_rows, target)
                                 or series.next_season(found_rows, current))
                        found_drama = (found or {}).get("drama") or {}
                        if found and found_drama.get("id") != merged.get("id"):
                            self._watch.mark_next_season(
                                row["id"], found_drama, found.get("season"))
                        time.sleep(0.3)
                except Exception:  # noqa: BLE001 - one bad drama must not stop the pass
                    failed += 1
                time.sleep(0.8)
            after = {item["id"]: (int(item.get("known") or 0), bool(item.get("newSeason")),
                                  bool(item.get("arrived")), int(item.get("newEpisodes") or 0))
                     for item in self._watch.follows()}
            for key, value in after.items():
                if key in before and value != before[key]:
                    updated += 1
            return updated, failed

        def done(result):
            self._follow_busy = False
            updated, failed = result or (0, 0)
            if updated:
                self.statusBar().showMessage("追剧检查：%d 部有更新" % updated, 8000)
            elif failed:
                self.statusBar().showMessage("追剧检查：%d 部暂时取不到更新" % failed, 6000)

        def error(message):
            self._follow_busy = False
            self.statusBar().showMessage("追剧检查失败：%s" % message, 6000)

        self._runner.run(work, on_success=done, on_error=error)

    def _show_from_tray(self):
        self.showNormal()
        self.raise_()
        self.activateWindow()

    def _on_tray_activated(self, reason):
        if reason == QSystemTrayIcon.DoubleClick:
            self._show_from_tray()

    def _sync_theme_button(self):
        """写当前主题：状态栏常驻的小按钮当指示器读最省事，点一下就是切到另一边。"""
        self.theme_button.setText("主题 深色" if ui.current_theme() == "dark" else "主题 浅色")

    def _toggle_theme(self):
        """换主题：改调色板 → 重建样式表 → 催自绘控件重画。

        自绘的芯片和下载卡片在 paint 时读 DESIGN，所以光换样式表不够，
        得让它们重画一遍——repaint_all() 负责这一下。
        """
        target = "light" if ui.current_theme() == "dark" else "dark"
        if not ui.apply_theme(target):
            return
        tools.set_theme(target)
        application = QApplication.instance()
        if application is not None:
            application.setStyleSheet(build_stylesheet())
        ui.repaint_all()
        if getattr(self, "_video_fullscreen", False):
            self._apply_fullscreen_player_style(True)
        # Placeholder posters are pre-painted pixmaps; update() cannot
        # recolour them, so ask the poster walls to rebuild their icons.
        for grid in (self.grid, self.search_grid, self.detail):
            try:
                grid.reload_theme()
            except Exception:
                pass
        try:
            self.browse_panel.reload_theme()
        except Exception:
            pass
        for panel in (self.follow_panel, self.history_panel, self.calendar_panel):
            try:
                panel.reload_theme()
            except Exception:
                pass
        self._sync_theme_button()

    def _on_index_reconciled(self, dropped):
        if dropped:
            self.statusBar().showMessage("清理了 %d 条文件已不存在的成品记录" % dropped, 8000)

    # ---------------------------------------------------------------- browse
    def _build_browse(self):
        self.browse_panel = BrowsePanel(self._core)
        self.browse_panel.openDrama.connect(self.open_detail)
        self.browse_panel.searchRequested.connect(self._search_from_browse)
        self.browse_panel.statusMessage.connect(
            lambda text: self.statusBar().showMessage(text, 6000))
        # M1：站源切换入口。browse_panel.py 归 C1 所有，这里只在外层加一条
        # 顶部薄条放 SourceSwitchPopup，不改动面板本身。
        page = QWidget()
        page.setProperty("guoRole", "browseShell")
        strip = QHBoxLayout()
        strip.setContentsMargins(14, 8, 14, 0)
        hint = QLabel("切换站源决定找剧与搜索的检索范围")
        hint.setProperty("role", "hint")
        self.source_switch = SourceSwitchPopup()
        self.source_switch.source_selected.connect(self._on_source_selected)
        self.source_switch.source_toggled.connect(self._on_source_toggled)
        strip.addWidget(hint)
        strip.addStretch(1)
        strip.addWidget(self.source_switch)
        layout = QVBoxLayout(page)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(0)
        layout.addLayout(strip)
        layout.addWidget(self.browse_panel, 1)
        self._apply_sources_to_switch()
        return page

    def _search_from_browse(self, query):
        self.tabs.setCurrentWidget(self.search_page)
        self.search_edit.setText(query)
        self._run_search()

    # ------------------------------------------------------------ M1 站源上下文
    def _fallback_sources(self):
        return [{"slug": slug, "name": name, "implemented": ok}
                for slug, name, ok in FALLBACK_SOURCES]

    def _restore_current_source(self):
        """M1：上次选择的站源要落得下来；只认已接入的源，坏值一律回默认。"""
        stored = str(_app_settings().value(SOURCE_CURRENT_KEY, "") or "")
        known = {slug for slug, _name, ok in FALLBACK_SOURCES if ok}
        return stored if stored in known else DEFAULT_SOURCE

    def _apply_sources_to_switch(self):
        """把当前清单灌进弹层；F1 的 list_sources 到位后异步替换硬编码表。"""
        self.source_switch.set_sources(self._sources)
        self.source_switch.set_current(self.current_source)
        if not hasattr(self._core, "list_sources"):
            return  # F1 尚未提供：回退 12 源展示表已经够用
        def work():
            return self._core.list_sources()

        def done(result):
            entries = self._normalise_source_list(result)
            if not entries:
                return
            self._sources = entries
            self.source_switch.set_sources(entries)
            self.source_switch.set_current(self.current_source)
        # 拉取失败保持静默：回退表可用，不该每次启动都打扰用户
        self._runner.run(work, on_success=done)

    @staticmethod
    def _normalise_source_list(payload):
        """兼容 F1 list_sources 可能的几种形状：list 或 {"sources"/"items":[...]}，
        字段名 slug/Slug/id、name/displayName/…；给了 enabled 才在弹层出复选框。"""
        if isinstance(payload, dict):
            payload = payload.get("sources") or payload.get("items") or []
        if not isinstance(payload, list):
            return []
        names = {slug: name for slug, name, _ok in FALLBACK_SOURCES}
        entries = []
        for item in payload:
            if not isinstance(item, dict):
                continue
            slug = str(item.get("slug") or item.get("Slug") or item.get("id") or "").strip()
            if not slug:
                continue
            name = str(item.get("name") or item.get("displayName") or item.get("DisplayName")
                       or item.get("label") or names.get(slug) or slug)
            entry = {"slug": slug, "name": name, "implemented": True}
            for key in ("enabled", "Enabled"):
                if isinstance(item.get(key), bool):
                    entry["enabled"] = item[key]
                    break
            entries.append(entry)
        return entries

    def _source_implemented(self, slug):
        for entry in self._sources:
            if entry.get("slug") == slug:
                return bool(entry.get("implemented", True))
        return False

    def _source_name(self, slug):
        if hasattr(self, "source_switch"):
            return self.source_switch.name_of(slug)
        return slug

    def _on_source_selected(self, slug):
        if not slug or slug == self.current_source:
            return
        if not self._source_implemented(slug):
            # 本地无 provider（Q1–Q4 移植进行中）：拒绝切换，把按钮拉回当前源。
            self.statusBar().showMessage(
                "「%s」尚未接入，暂不可切换" % self._source_name(slug), 5000)
            self.source_switch.set_current(self.current_source)
            return
        self.current_source = slug
        try:
            _app_settings().setValue(SOURCE_CURRENT_KEY, slug)
        except Exception:
            pass
        self.source_switch.set_current(slug)
        self._apply_source_to_search_bar()
        # C1 并行提供 browse_panel.set_source(slug)：有就让它自己刷新找剧页；
        # 还没有时只有搜索范围生效，不算失败。
        setter = getattr(self.browse_panel, "set_source", None)
        if callable(setter):
            try:
                setter(slug)
            except Exception as exc:
                self.statusBar().showMessage("找剧页刷新失败：%s" % exc, 6000)
        self.statusBar().showMessage(
            "当前站源：%s（搜索范围已同步）" % self._source_name(slug), 4000)

    def _on_source_toggled(self, slug, enabled):
        setter = getattr(self._core, "set_source_enabled", None)
        if not callable(setter):
            self.statusBar().showMessage(
                "本地核心尚未提供站源开关，待 F1 基座落地后可用", 6000)
            return
        def work():
            return setter(slug, bool(enabled))
        self._runner.run(work, on_error=lambda msg: self.statusBar().showMessage(
            "站源开关失败：%s" % msg, 6000))

    def _apply_source_to_search_bar(self):
        name = self._source_name(self.current_source)
        if hasattr(self, "search_edit"):
            self.search_edit.setPlaceholderText(
                "输入剧名、演员或题材（仅搜索 %s），回车开始搜索" % name)

    # ---------------------------------------------------------------- M1e 设置页
    def _ensure_settings_panel(self):
        """M1e：首次进入「设置」才 import S1 的 settings_panel，失败留占位不崩。"""
        if self._settings_panel is not None or self._settings_tried:
            return
        self._settings_tried = True
        widget = None
        try:
            from .settings_panel import SettingsPanel
            widget = SettingsPanel(self._core)  # 直连核心：站源开关落 source_prefs.json
        except Exception:
            widget = None
        if widget is None or not isinstance(widget, QWidget):
            widget = QLabel("设置面板开发中")
            widget.setAlignment(Qt.AlignCenter)
            widget.setProperty("role", "hint")
        self._settings_panel = widget
        layout = self.settings_page.layout()
        if layout is None:
            layout = QVBoxLayout(self.settings_page)
            layout.setContentsMargins(0, 0, 0, 0)
        layout.addWidget(widget)

    # ---------------------------------------------------------------- library
    def _build_library(self):
        page = QWidget()
        top = QHBoxLayout()
        top.addWidget(QLabel("分类"))
        self.genre_box = QComboBox()
        for key, name in GENRES:
            self.genre_box.addItem(name, key)
        self.genre_box.currentIndexChanged.connect(self._on_genre_changed)
        top.addWidget(self.genre_box)
        top.addWidget(QLabel("榜单"))
        self.board_box = QComboBox()
        # 改选榜单就直接加载。以前换了选项毫无反应，还得再点一次「查看榜单」，
        # 看着像坏了。
        self.board_box.currentIndexChanged.connect(lambda: self._load_rankings(1))
        top.addWidget(self.board_box)
        load_board = QPushButton("查看榜单")
        load_board.clicked.connect(lambda: self._load_rankings(1))
        top.addWidget(load_board)
        top.addStretch(1)
        refresh = QPushButton("刷新推荐")
        refresh.clicked.connect(self._refresh_recommendations)
        top.addWidget(refresh)

        self.rank_info = QLabel("")
        self.rank_info.setWordWrap(False)
        self.rank_info.setProperty("role", "rankUpdated")
        self.rank_info.setMaximumWidth(340)
        self.rank_info.setAlignment(Qt.AlignRight)
        self.rank_list = RankList()
        self.rank_list.setMaximumWidth(340)
        self.rank_list.setMinimumWidth(300)
        self.rank_list.dramaActivated.connect(self.open_detail)
        self.rank_list.set_cover_loader(self._covers, self._request_covers)  # C1 遗留：榜单行补真封面
        self.rank_retry = QPushButton("重试加载")
        self.rank_retry.setProperty("role", "ghost")
        self.rank_retry.clicked.connect(self._retry_rankings)
        self.rank_retry.hide()
        self.rank_page_label = QLabel("")
        self.rank_page_label.setProperty("role", "hint")
        rank_footer = QHBoxLayout()
        rank_footer.addWidget(self.rank_page_label, 1)
        rank_footer.addWidget(self.rank_retry)
        self._rank_page = 0
        self._rank_total = 0
        self._rank_boards = {}
        self._rank_updated = ""
        self._rank_token = 0
        self._rank_loading = False
        self._rank_has_more = False
        self._rank_retry_page = 0
        self._rank_empty_pages = 0
        self._rank_scroll_loader = ScrollLoader(self.rank_list, self._load_more_rankings,
                                                margin=200)

        self.grid = RecommendationPanel(self._core, self._covers, self._request_covers)
        self.grid.dramaActivated.connect(self.open_detail)

        rank_column = QVBoxLayout()
        rank_column.setContentsMargins(0, 0, 0, 0)
        rank_column.setSpacing(8)
        rank_column.addWidget(self.rank_list, 1)
        rank_column.addWidget(self.rank_info)
        rank_column.addLayout(rank_footer)
        right = QHBoxLayout()
        right.setSpacing(20)
        right.addLayout(rank_column, 0)
        right.addWidget(self.grid, 1)

        layout = QVBoxLayout(page)
        layout.setContentsMargins(10, 10, 10, 10)
        layout.addLayout(top)
        layout.addLayout(right, 1)
        return page

    def _load_boards(self):
        def work():
            return self._core.boards()

        def done(boards):
            self._rank_boards = {board.get("id"): board for board in boards}
            self.board_box.blockSignals(True)
            self.board_box.clear()
            for board in boards:
                self.board_box.addItem("%s（%s）" % (board.get("name", ""), board.get("id", "")), board.get("id"))
            self.board_box.blockSignals(False)
            self._sync_rank_info()
            # 榜单列表一拿到就把当前榜单填上：以前要等用户点「查看榜单」，
            # 在那之前首屏左边那一整列都是空白。
            if self.board_box.count() and not self.rank_list.count():
                self._load_rankings(1)

        self._runner.run(work, on_success=done)

    def _load_recommendations(self, force=False, more=False, replace=False):
        self.grid.load(self.genre_box.currentData() or "", force=force,
                       more=more, replace=replace)

    def _refresh_recommendations(self):
        """换一批: keep the feed moving but start the grid over."""
        if self.grid.count():
            self._load_recommendations(more=True, replace=True)
        else:
            self._load_recommendations(force=True)

    def _on_genre_changed(self, *_):
        """分类切换: 换一批 for that category, from its first page."""
        self._load_recommendations(force=True)

    def _sync_rank_info(self):
        """左侧榜单只留更新时间。

        榜单接口给的说明是「红果观看、互动等综合热度；每日更新。」，更新时间
        又跟着「· 基于红果内观看/互动等综合热度排序」，两句话在 340px 窄列里
        能占掉五六行，把名次列表压到下面去。只留「11月4日已更新」这一句。
        """
        text = str(self._rank_updated or "")
        if "·" in text:
            text = text.split("·")[0].strip()
        self.rank_info.setText("更新：%s" % text if text else "")

    def _load_rankings(self, page=1):
        board = self.board_box.currentData()
        if not board:
            return
        page = max(1, int(page or 1))
        if page > 1 and (self._rank_loading or
                         (self._rank_total and page > self._rank_total)):
            return
        self._rank_token += 1
        token = self._rank_token
        self._rank_loading = True
        self._rank_retry_page = 0
        self.rank_retry.hide()
        if page == 1:
            self.rank_list.set_rows([])
            self._rank_page = 0
            self._rank_total = 0
            self._rank_has_more = False
            self._rank_empty_pages = 0
            self._rank_updated = ""
            self.rank_info.clear()
        self.rank_page_label.setText("正在加载更多…" if page > 1 else "正在加载榜单…")

        def work():
            return self._core.rankings(board, page=page)

        def done(result):
            if token != self._rank_token:
                return
            result = result or {}
            before = self.rank_list.count()
            self.rank_list.append_rows(result.get("items") or [])
            added = self.rank_list.count() - before
            self._rank_page = page
            self._rank_total = int(result.get("totalPages") or 0)
            self._rank_updated = str(result.get("updatedText") or self._rank_updated)
            self._rank_has_more = bool(result.get("hasMore")) and (
                not self._rank_total or page < self._rank_total)
            self._rank_empty_pages = self._rank_empty_pages + 1 if not added else 0
            if self._rank_empty_pages >= 2:
                self._rank_has_more = False
            self._rank_loading = False
            self._sync_rank_info()
            if not self.rank_list.count():
                self.rank_info.setText(result.get("warning") or "榜单为空")
            tail = "下滑继续浏览" if self._rank_has_more else "已全部加载"
            self.rank_page_label.setText("已加载 %d 部 · %s" % (self.rank_list.count(), tail))
            self._rank_scroll_loader.schedule()

        def failed(message):
            if token != self._rank_token:
                return
            self._rank_loading = False
            self._rank_retry_page = page
            self.rank_page_label.setText("加载失败，点击重试")
            self.rank_page_label.setToolTip(message)
            self.rank_retry.show()

        self._runner.run(work, on_success=done, on_error=failed)

    def _load_more_rankings(self):
        if self._rank_has_more and not self._rank_loading and not self._rank_retry_page:
            self._load_rankings(self._rank_page + 1)

    def _retry_rankings(self):
        if self._rank_retry_page:
            self._load_rankings(self._rank_retry_page)

    # ----------------------------------------------------------------- search
    def _build_search(self):
        page = QWidget()
        bar = QHBoxLayout()
        self.search_edit = QLineEdit()
        self._apply_source_to_search_bar()
        self.search_edit.returnPressed.connect(lambda: self._run_search())
        self.search_button = QPushButton("搜索")
        self.search_button.setProperty("role", "accent")
        self.search_button.clicked.connect(self._run_search)
        bar.addWidget(self.search_edit, 1)
        bar.addWidget(self.search_button)

        self.suggest_box = QListWidget()
        self.suggest_box.setMaximumHeight(150)
        self.suggest_box.setVisible(False)
        self.suggest_box.itemClicked.connect(self._on_suggestion)
        self.suggest_box.itemActivated.connect(self._on_suggestion)

        self.search_grid = CoverGrid(columns=7, covers=self._covers)
        self.search_grid.set_cover_loader(self._covers, self._request_covers)
        # A search is one result set, not a feed: the paging bar would be
        # three dead buttons, so it is hidden.
        self.search_grid.set_pager_visible(False)
        self.search_grid.dramaActivated.connect(self.open_detail)

        layout = QVBoxLayout(page)
        layout.setContentsMargins(10, 10, 10, 10)
        layout.addLayout(bar)
        layout.addWidget(self.suggest_box)
        layout.addWidget(self.search_grid, 1)

        self.search_edit.textEdited.connect(self._on_search_typed)
        # M1c：聚焦空搜索框时展示当前站源的最近搜索
        self.search_edit.installEventFilter(self)
        return page

    def eventFilter(self, obj, event):
        if (event.type() == QEvent.FocusIn
                and obj is getattr(self, "search_edit", None)
                and not self.search_edit.text().strip()):
            self._show_recent_searches("")
        return super().eventFilter(obj, event)

    def _show_recent_searches(self, filter_text=""):
        """M1c：最近搜索下拉。空输入展示全量（带分组头），输入中只按前缀/子串过滤。"""
        words = self._recent_for_current()
        if filter_text:
            lowered = filter_text.lower()
            words = [w for w in words if lowered != w.lower() and lowered in w.lower()]
        self.suggest_box.clear()
        if not words:
            self.suggest_box.setVisible(False)
            return
        if not filter_text:
            header = QListWidgetItem("最近搜索")
            header.setFlags(Qt.NoItemFlags)
            self.suggest_box.addItem(header)
        for word in words:
            entry = QListWidgetItem(word)
            entry.setData(Qt.UserRole, word)
            self.suggest_box.addItem(entry)
        self.suggest_box.setVisible(True)

    def _recent_for_current(self):
        try:
            return list(_recent_map().get(self.current_source) or [])
        except Exception:
            return []

    def _on_search_typed(self, text):
        text = text.strip()
        if not text:
            # 清空输入：作废在途联想，回到最近搜索全量列表
            self._suggest_token += 1
            self._show_recent_searches("")
            return
        self._suggest_token += 1
        if self.current_source != "hongguo":
            # 联想词只有红果有（source-map ①）；其它站源输入时退化为过滤最近搜索。
            self._show_recent_searches(text)
            return
        token = self._suggest_token

        def work():
            return self._core.suggestions(text)

        def done(items):
            if token != self._suggest_token:
                return          # 这是上一次输入的联想词，丢掉
            self.suggest_box.clear()
            for item in items or []:
                if isinstance(item, dict):
                    label = item.get("name") or ""
                    kind = item.get("type") or ""
                else:
                    label, kind = str(item), ""
                if label:
                    entry = QListWidgetItem("%s    %s" % (label, kind) if kind else label)
                    entry.setData(Qt.UserRole, label)
                    self.suggest_box.addItem(entry)
            if items:
                self.suggest_box.setVisible(True)
            else:
                # 联想词空/核心没起来时，退化成最近搜索，下拉不至于死寂
                self._show_recent_searches(text)

        def error(_):
            if token == self._suggest_token:
                self._show_recent_searches(text)

        self._runner.run(work, on_success=done, on_error=error)

    def _on_suggestion(self, item):
        label = item.data(Qt.UserRole) or item.text()
        if not label or not (item.flags() & Qt.ItemIsEnabled):
            return  # 「最近搜索」分组头不可点，防御一层
        self.search_edit.setText(label)
        self.suggest_box.setVisible(False)
        self._run_search()

    def _run_search(self):
        """Search the typed keyword on its own lane.

        Pressing 搜索 (or Enter) again while the first request is still on the
        wire used to cancel it - the core cancels an older read on the same
        session - and the canceled one painted "搜索失败：context canceled"
        just before the newer one painted its results.  A repeated keyword is
        now simply ignored (the core caches searches anyway) and a superseded
        request never writes to the UI.
        """
        query = self.search_edit.text().strip()
        if not query:
            return
        # 点搜索时把联想框收掉，并让在途的联想回调失效；否则它晚一步返回
        # 又把列表弹出来，压在结果上面。
        self._suggest_token += 1
        self.suggest_box.setVisible(False)
        if query == self._search_pending:
            return
        self._search_token += 1
        token = self._search_token
        self._search_pending = query
        # M1b/M1c：搜索永远限定在当前站源；发起即记入该源的最近搜索。
        source = self.current_source
        _record_recent(source, query)
        self.suggest_box.setVisible(False)
        self.tabs.setCurrentWidget(self.search_page)
        self.search_grid.set_loading("正在搜索…")
        self.search_grid.set_interactive(False)

        def work():
            return self._core.catalog(query=query, page=1, source=source, lane="search")

        def done(result):
            if token != self._search_token:
                return
            self._search_pending = ""
            self.search_grid.set_interactive(True)
            result = result or {}
            items = result.get("items", [])
            self.search_grid.set_items(items, has_more=bool(result.get("hasMore")))
            if not items:
                hint = result.get("warning") or ""
                self.search_grid.set_empty(hint or "没有找到相关剧集",
                                           "" if hint else "换个关键词，或去「剧库推荐」按分类逛逛")
                self.search_grid.set_loading("共 0 部")

        def error(message):
            if token != self._search_token:
                return
            self._search_pending = ""
            self.search_grid.set_interactive(True)
            if not self.search_grid.count():
                self.search_grid.set_empty("搜索失败", message)
            self.search_grid.set_loading("搜索失败：%s" % message)

        self._runner.run(work, on_success=done, on_error=error)

    # ----------------------------------------------------------------- detail
    def _build_detail(self):
        self.detail = DetailPage(self._core, self._covers, self._watch)
        self.detail.dramaSelected.connect(self.open_detail)
        self.detail.playEpisode.connect(self.play_episode)
        self.detail.downloadEpisodes.connect(self.enqueue_downloads)
        self.detail.statusMessage.connect(lambda m: self.statusBar().showMessage(m, 6000))
        self.detail.followRequested.connect(self._toggle_follow)
        self.detail.reserveRequested.connect(self._toggle_reserve)
        self.detail.seasonSelected.connect(self.open_detail)
        self.detail.saveCoverRequested.connect(self._save_cover)
        return self.detail

    def _save_cover(self, drama):
        """把当前这部剧的海报另存到用户选的位置。

        平时列表/详情用的是 400px 那版小图，保存时单独去要一张高清的（同一张图的
        原图，实测 700x1000）；拿不到就退回小图，并在状态栏说明。HEIC 由 ffmpeg
        转成 JPG/PNG，Windows 上双击就能看。
        """
        title = (drama.get("title") or "短剧").strip()
        folder = os.path.join(tools.download_dir(), decrypt.series_folder(title))
        if not os.path.isdir(folder):
            folder = tools.download_dir()
        suggested = os.path.join(folder, "%s 海报.jpg" % decrypt.safe_name(title))
        target, _ = QFileDialog.getSaveFileName(self, "保存海报", suggested,
                                                "JPEG 图片 (*.jpg);;PNG 图片 (*.png)")
        if not target:
            return
        if os.path.splitext(target)[1].lower() not in (".jpg", ".jpeg", ".png"):
            target += ".jpg"                       # 没写后缀就按 JPEG 存
        self.statusBar().showMessage("正在获取高清海报…", 5000)

        def work():
            info = {}
            for candidate in media.hi_res_cover_urls(drama.get("cover")):
                try:
                    info = self._core.cover(dict(drama, cover=candidate)) or {}
                except CoreError:
                    info = {}
                if info.get("path") and os.path.isfile(info["path"]):
                    break
            hidpi = bool(info.get("path"))
            if not hidpi:
                info = self._core.cover(drama) or {}
            source = info.get("path")
            if not source or not os.path.isfile(source):
                raise media.MediaError("没拿到这张海报的原图，请先在剧库里刷新一次")
            path = media.export_cover(source, target, bool(info.get("heic")))
            video = (media.probe(path).get("video") or {})
            return {"path": path, "width": video.get("width"),
                    "height": video.get("height"), "hidpi": hidpi}

        def done(result):
            size = "%sx%s" % (result.get("width"), result.get("height"))
            note = "" if result.get("hidpi") else "（没取到高清版，用的小图）"
            self.statusBar().showMessage("海报已保存（%s）%s：%s"
                                         % (size, note, result["path"]), 8000)

        def error(message):
            QMessageBox.warning(self, "保存海报", "没能保存海报：%s" % message)

        self._runner.run(work, on_success=done, on_error=error)

    def _toggle_follow(self, drama):
        added = self._watch.toggle_follow(drama)
        self.statusBar().showMessage("已加入追剧，可在「追更/预约」页查看" if added
                                     else "已取消追剧", 5000)

    def _toggle_reserve(self, drama):
        if self._watch.is_reserved(drama.get("id")):
            self._watch.cancel_reservation(drama.get("id"))
            self.statusBar().showMessage("已取消预约", 5000)
            return
        self._watch.reserve_drama(drama)
        self.statusBar().showMessage(
            "已预约；应用运行期间每 30 分钟检查一次，上线/更新后会在「追更/预约」页提示", 7000)

    def _load_series(self, drama):
        """进详情页顺手查一次同系列；命中就显示「全 N 季」。

        红果没有 series_id，核心搜索会自动补齐同标题的其它季；这里只做
        标题归并和展示。失败/只有一季时按钮保持隐藏，不影响详情页。
        """
        title = str(drama.get("title") or "").strip()
        key = series.series_key(title)
        if len(key) < 2:
            return
        source = str(drama.get("source") or "")
        cache_key = source + ":" + key
        cached = self._series_cache.get(cache_key)
        if cached and time.time() - cached[0] < 900:
            self.detail.set_series(cached[1])
            return
        query = series.base_title(title) or title
        self._series_token += 1
        token = self._series_token

        def work():
            result = self._core.catalog(query=query, page=1, source=source or "hongguo",
                                        lane="series")
            return series.group((result or {}).get("items") or [], title)

        def done(rows):
            if token != self._series_token:
                return
            self._series_cache[cache_key] = (time.time(), rows)
            current = self.detail.drama() or {}
            if current.get("id") == drama.get("id"):
                self.detail.set_series(rows)

        def error(_message):
            # 同系列只是增强信息，搜不到就保持原样。
            if token == self._series_token:
                current = self.detail.drama() or {}
                if current.get("id") == drama.get("id"):
                    self.detail.set_series([])

        self._runner.run(work, on_success=done, on_error=error)

    def open_detail(self, drama):
        self._detail_token += 1
        token = self._detail_token
        self.tabs.setCurrentWidget(self.detail)
        self.detail.show_drama(drama)
        self._load_series(drama)

        def work():
            result = dict(self._core.detail(drama) or {})
            merged = dict(result.get("drama") or drama)
            try:
                # 红果的详情接口不带上架时间；原版一样会在缺日期时向
                # metadata 接口（网页版详情）补齐。
                if not merged.get("onlineDate"):
                    extra = (self._core.metadata(merged) or {}).get("drama") or {}
                    for key, value in extra.items():
                        if isinstance(value, str) and value and not merged.get(key):
                            merged[key] = value
            except CoreError:
                pass  # 日期是装饰性信息，拿不到也不能挡住详情页
            result["drama"] = merged
            return result

        def done(result):
            if token != self._detail_token:
                return      # 用户已经点了另一部剧，这个答案过期了
            result = result or {}
            loaded = result.get("drama", drama)
            self.detail.set_chapters(loaded, result.get("chapters", []))
            self._watch.observe_drama(loaded)
            if result.get("warning"):
                self.statusBar().showMessage(result["warning"], 6000)
            pending = getattr(self, "_pending_play", None)
            if pending and pending["drama"].get("id") == loaded.get("id"):
                self._pending_play = None
                if pending["position"] < len(self.detail.chapters()):
                    self.play_episode(loaded, pending["position"],
                                      resume=float(pending.get("resume") or 0.0))

        def error(message):
            if token != self._detail_token:
                return      # 过期请求的失败不该盖住新页面
            self._pending_play = None
            self.detail.fail("加载失败：%s" % message)

        self._runner.run(work, on_success=done, on_error=error)

    # ----------------------------------------------------------------- player
    def _build_player(self):
        page = QWidget()
        self.play_title = QLabel("未在播放")
        self.play_title.setFont(QFont("Microsoft YaHei", 12, QFont.Bold))
        self.player = PlayerPane()
        self.player.stateChanged.connect(self.statusBar().showMessage)
        self.player.failed.connect(lambda m: self.statusBar().showMessage(m, 8000))
        self.player.finished.connect(self._on_play_finished)
        self.player.fullscreenRequested.connect(self._set_video_fullscreen)
        self.player.positionChanged.connect(self._on_watch_progress)
        self.player.previousRequested.connect(lambda: self._play_neighbour(-1))
        self.player.nextRequested.connect(lambda: self._play_neighbour(1))
        self.player.danmakuToggled.connect(self._on_danmaku_toggled)
        self.player.danmakuOpacityChanged.connect(self._on_danmaku_opacity)
        # P1 接线：停靠栏选集/清晰度弹层/预热钩子（player.py 新 API，旧信号不动）。
        self.player.episodeClicked.connect(self._on_episode_row)
        self.player.qualityChanged.connect(self._on_player_quality)
        hook = getattr(self.player, "set_preload_hook", None)
        if callable(hook):
            hook(self._on_player_preload_next)

        side = QVBoxLayout()
        # 侧栏自己的边距归零：外层页面已经有 10px 了，再留一层会让选集网格
        # 少掉 18px、一行从四枚掉回三枚
        side.setContentsMargins(0, 0, 0, 0)
        # 和剧集详情同一套芯片网格：窄栏、点一下就换集，不再是一列长名单
        self.episode_list = EpisodeList(compact=True)
        self.episode_list.playRequested.connect(self._on_episode_row)
        self.route_box = QComboBox()
        self.route_box.setMaximumWidth(240)
        self.route_box.currentIndexChanged.connect(self._switch_route)
        self.quality_box = QComboBox()
        self.quality_box.addItems(["自动", "1080P", "720P", "540P"])
        self.quality_box.setMaximumWidth(120)
        # P1c：覆盖层弹层和这个下拉是同一状态。起屏先对齐 PlayerPane 恢复的偏好，
        # 对齐完了再接信号，免得建页时把默认值当成用户改动写回设置。
        self._sync_quality_box_from_player()
        self.quality_box.currentIndexChanged.connect(self._on_quality_box_changed)
        self.hwdec_box = QCheckBox("硬件解码")
        self.hwdec_box.setToolTip("用显卡解码，降低 1080P 的 CPU 占用；花屏或黑屏时请关闭（下一集生效）")
        self.hwdec_box.setChecked(tools.hwdec_enabled())
        self.hwdec_box.toggled.connect(self._toggle_hwdec)
        self.replay_button = QPushButton("下载本集")
        self.replay_button.clicked.connect(self._download_current)
        self.pause_button = QPushButton("暂停播放")
        self.pause_button.clicked.connect(self._toggle_playback_pause)
        self.player.pauseChanged.connect(
            lambda paused: self.pause_button.setText("继续播放" if paused else "暂停播放"))
        stop_button = QPushButton("停止播放")
        stop_button.clicked.connect(self.stop_playback)

        side.addWidget(self.episode_list, 1)
        side.addWidget(QLabel("线路"))
        side.addWidget(self.route_box)
        side.addWidget(QLabel("画质"))
        side.addWidget(self.quality_box)
        side.addWidget(self.hwdec_box)
        side.addWidget(self.replay_button)
        side.addWidget(self.pause_button)
        side.addWidget(stop_button)

        # One widget so video fullscreen can hide the whole column at once.
        # 定宽而不是最大宽度：选集网格一行正好放四枚，宽窄随窗口变会让它一会儿
        # 三枚一会儿四枚。
        self.player_side = QWidget()
        self.player_side.setLayout(side)
        self.player_side.setFixedWidth(240)

        layout = QHBoxLayout(page)
        layout.setContentsMargins(10, 10, 10, 10)
        layout.addWidget(self.player, 1)
        layout.addWidget(self.player_side)
        # 全屏时要把这圈边距收掉，否则画面四周留一圈底色
        self._player_layout = layout

        QShortcut(QKeySequence(Qt.Key_Escape), self, activated=self._exit_video_fullscreen)
        QShortcut(QKeySequence(Qt.Key_Left), self, activated=lambda: self._seek_by_keys(-5))
        QShortcut(QKeySequence(Qt.Key_Right), self, activated=lambda: self._seek_by_keys(5))
        QShortcut(QKeySequence(Qt.Key_Space), self, activated=self._toggle_pause_by_keys)
        QShortcut(QKeySequence(Qt.Key_D), self, activated=self._toggle_danmaku_by_keys)
        return page

    def _player_keys_allowed(self):
        """空格 / 方向键只在看视频时生效，而且不能抢输入框的按键。

        这里**不**排除按钮：快捷键会先吃掉这次按键（按钮本来也收不到空格），
        早先因为「焦点在按钮上就不响应」导致点过播放按钮之后空格就失灵了。
        """
        if self.tabs.currentWidget() is not self.player_page:
            return False
        focus = QApplication.focusWidget()
        if isinstance(focus, (QLineEdit, QTextEdit, QComboBox, QAbstractSpinBox)):
            return False
        return True

    def _seek_by_keys(self, seconds):
        if self._player_keys_allowed() and self.player.is_playing():
            self.player.seek_relative(seconds)
            self.statusBar().showMessage("快退 5 秒" if seconds < 0 else "快进 5 秒", 2000)

    def _toggle_pause_by_keys(self):
        if self._player_keys_allowed() and self.player.is_playing():
            self.player.toggle_pause()

    def _toggle_playback_pause(self):
        if not self.player.is_playing():
            self.statusBar().showMessage("当前没有正在播放的视频", 5000)
            return
        self.player.toggle_pause()

    def _toggle_hwdec(self, on):
        """记住硬件解码开关；mpv 参数在下一次起播/切集时生效。"""
        tools.set_hwdec_enabled(on)
        self.statusBar().showMessage(
            "硬件解码已%s，下一集生效" % ("开启" if on else "关闭"), 5000)

    # -------------------------------------------------------------- P1 播放器接线
    def _quality_index_for_slug(self, slug):
        return QUALITY_INDEX_SLUGS.index(slug) if slug in QUALITY_INDEX_SLUGS else 0

    def _sync_quality_box_from_player(self):
        slug = self.player.quality_slug() if hasattr(self.player, "quality_slug") else "auto"
        index = self._quality_index_for_slug(slug)
        if self.quality_box.currentIndex() != index:
            self.quality_box.blockSignals(True)
            self.quality_box.setCurrentIndex(index)
            self.quality_box.blockSignals(False)

    def _on_player_quality(self, slug):
        """P1c：覆盖层弹层改了清晰度 → 同步侧栏下拉，并把当前会话的记录画质改掉，
        这样随后的预热/下一集都按新清晰度走（实际换源在下一集生效，与旧下拉框一致）。"""
        level = int(PlayerPane.quality_level(slug))
        if getattr(self, "_playback", None):
            self._playback["quality"] = level
        index = self._quality_index_for_slug(slug)
        if self.quality_box.currentIndex() != index:
            self.quality_box.blockSignals(True)
            self.quality_box.setCurrentIndex(index)
            self.quality_box.blockSignals(False)

    def _on_quality_box_changed(self, index):
        """侧栏下拉 → PlayerPane 的同一状态；set_quality_slug 只在真变化时发信号。"""
        slug = QUALITY_INDEX_SLUGS[index] if 0 <= index < len(QUALITY_INDEX_SLUGS) else "auto"
        setter = getattr(self.player, "set_quality_slug", None)
        if callable(setter):
            setter(slug)

    def _on_player_preload_next(self, next_index):
        """P1e：停靠栏要求预热第 next_index（1-based）集。只接受「当前播放集的下一集」，
        其余情况交给 play_episode 自己的 _schedule_preload，避免切集途中预热错目标。"""
        playback = getattr(self, "_playback", None)
        if not playback or playback.get("local_path"):
            return
        try:
            position = int(playback.get("position", 0) or 0)
        except (TypeError, ValueError):
            return
        if int(next_index) != position + 2:
            return
        self._preload_next(self._play_request)

    # ----------------------------------------------------------------- danmaku
    def _toggle_danmaku_by_keys(self):
        if self._player_keys_allowed():
            self.player.toggle_danmaku()

    def _on_danmaku_toggled(self, on):
        tools.set_danmaku_enabled(on)
        self.statusBar().showMessage("弹幕已开启" if on else "弹幕已关闭", 2000)
        if on:
            # 关着的时候没抓；现在打开就补当前这一集
            self._ensure_danmaku()

    def _attach_sidecar_danmaku(self, path):
        """给一个具体本地文件挂同名 .ass；没有就返回 False。"""
        base = os.path.splitext(str(path or ""))[0]
        ass = base + ".ass"
        if not (os.path.isfile(ass) and os.path.getsize(ass) > 0):
            return False
        count = 0
        try:
            with open(base + ".danmaku.json", "r", encoding="utf-8") as handle:
                count = len(json.load(handle).get("items") or [])
        except (OSError, ValueError, TypeError):
            pass
        self._danmaku_state = None
        self.player.reset_danmaku()
        self.player.attach_danmaku(ass, count)
        return True

    def _attach_local_danmaku(self, plan, drama=None, position=None):
        """把下载时保存的同名 .ass 挂到本地播放；没有就返回 False。

        本地播放可能给的是核心缓存里的加密原件，而 .ass 写在「下载/」下解密后的
        成品旁边，所以两个路径都要试。
        """
        candidates = []
        path = str((plan or {}).get("url") or "")
        if path:
            candidates.append(path)
        if drama is not None and position is not None:
            published = library.published_path(drama.get("id"), int(position) + 1)
            if published:
                candidates.append(published)
        for candidate in candidates:
            base = os.path.splitext(candidate)[0]
            ass = base + ".ass"
            if not (os.path.isfile(ass) and os.path.getsize(ass) > 0):
                continue
            count = 0
            try:
                with open(base + ".danmaku.json", "r", encoding="utf-8") as handle:
                    count = len(json.load(handle).get("items") or [])
            except (OSError, ValueError, TypeError):
                pass
            self._danmaku_state = None
            self.player.reset_danmaku()
            self.player.attach_danmaku(ass, count)
            return True
        return False

    def _queue_danmaku(self, session, drama, position):
        """换集/换线路后重新准备弹幕（新一集是一个新的 mpv 进程）。

        按播放位置逐窗取，和官方客户端一致：一次只请求当前 30 秒那一窗，
        连着把整集要下来会触发接口抽样（每窗只回十几条）。
        """
        self._release_danmaku_session()
        self._danmaku_job += 1
        self._danmaku_state = {"session": str(session or ""), "owned": "",
                               "drama": drama, "position": int(position or 0),
                               "span": 0, "size": (1920, 1080),
                               "windows": set(), "items": {}, "bust": {}, "tries": {},
                               "inflight": False, "last": 0.0}
        self.player.reset_danmaku()
        if tools.danmaku_enabled():
            self._await_danmaku_duration(self._danmaku_job, 0)

    def _ensure_danmaku(self):
        state = self._danmaku_state
        if not state:
            return
        if not state.get("span"):
            self._await_danmaku_duration(self._danmaku_job, 0)
        else:
            self._pump_danmaku()

    def _on_danmaku_opacity(self, percent):
        """透明度只影响生成出来的 ASS，存下来后重建一次字幕轨。

        拖动时每个像素都会触发 valueChanged；重建有 120ms 防抖，落盘也要
        防抖，否则滑一下就往 app-settings.json 写几十次。
        """
        self._danmaku_opacity_value = int(percent)
        if (self._danmaku_state or {}).get("items"):
            self._danmaku_opacity_timer.start()
        self._danmaku_opacity_save.start()

    def _await_danmaku_duration(self, job, tries):
        """等 mpv 报出这一集的时长：弹幕按位置分窗取，要先知道整集多长。"""
        if job != self._danmaku_job or not tools.danmaku_enabled():
            return
        state = self._danmaku_state
        if not state or state.get("span"):
            return
        if not self.player.is_playing():
            if tries < 40:          # mpv 还在启动
                QTimer.singleShot(250, lambda: self._await_danmaku_duration(job, tries + 1))
            return
        duration = self.player.duration()
        if duration < 5.0:
            if tries < 40:
                QTimer.singleShot(250, lambda: self._await_danmaku_duration(job, tries + 1))
            else:
                self.player.set_danmaku_note("没拿到时长，弹幕没加载")
            return
        state["span"] = int(duration * 1000)
        state["size"] = self.player.video_size()
        self._pump_danmaku()

    def _pump_danmaku(self):
        """有需要就补一窗：先当前播放位置那一窗，再往前预取一窗。"""
        state = self._danmaku_state or {}
        if not state or not state.get("span") or state.get("inflight"):
            return
        if not tools.danmaku_enabled() or not self.player.is_playing():
            return
        if time.time() - float(state.get("last") or 0.0) < danmaku.MIN_GAP_SECONDS:
            return
        span = int(state["span"])
        position = int(self.player.position() * 1000)
        start = max(0, position // danmaku.WINDOW_MS * danmaku.WINDOW_MS)
        target = None
        for candidate in (start, start + danmaku.WINDOW_MS):
            if candidate < span and candidate not in state["windows"]:
                target = candidate
                break
        if target is not None:
            self._fetch_danmaku_window(target)

    def _fetch_danmaku_window(self, start):
        state = self._danmaku_state or {}
        job = self._danmaku_job
        span = int(state.get("span") or 0)
        drama = state.get("drama") or {}
        position = int(state.get("position") or 0)
        session = str(state.get("session") or "")
        chapters = self.detail.chapters()
        chapter = chapters[position] if 0 <= position < len(chapters) else None
        # 每次重试换一个 duration：核心按 (窗口起点, duration) 缓存 5 分钟，
        # 用同一个值重试只会拿回被打样的那一页。
        bust = int((state.get("bust") or {}).get(start) or 0)
        state["inflight"] = True
        state["last"] = time.time()

        def work():
            created = ""
            if not session:
                # 播放本地成品时核心里没有播放会话，补一次远端解析只为拿弹幕
                if chapter is None:
                    raise RuntimeError("没有播放会话")
                plan = self._core.call("resolve", drama=drama, chapter=chapter,
                                       index=position + 1, quality=0, force=True)
                created = str((plan or {}).get("session") or "")
                if not created:
                    raise RuntimeError("没有播放会话")
            items, total = danmaku.fetch_window(
                self._core, created or session, start, span + bust)
            return {"start": start, "items": items, "total": total, "created": created}

        def done(result):
            if job != self._danmaku_job or not result:
                return
            state["inflight"] = False
            created = str(result.get("created") or "")
            if created:
                state["owned"] = created
                state["session"] = created
            start = int(result.get("start") or 0)
            batch = result.get("items") or []
            total = int(result.get("total") or 0)
            for item in batch:
                ident = str(item.get("id") or "")
                if ident:
                    state["items"][ident] = item
            # 一窗的完整量上限是 90 条；明显低于按整集密度估的量，说明被抽样了。
            # 重试最多两次（设备被平台列入抽样时再多也没用），之后照收。
            span = int(state.get("span") or 0)
            expected = min(90.0, total * danmaku.WINDOW_MS / float(span)) if total and span else 0.0
            tries = int((state.get("tries") or {}).get(start) or 0)
            if expected >= 20 and len(batch) < expected * 0.5 and tries < 2:
                state.setdefault("tries", {})[start] = tries + 1
                state.setdefault("bust", {})[start] = bust + 1
                state["last"] = time.time() + 12.0
            else:
                state["windows"].add(start)
            self._refresh_danmaku_track()
            if not batch and not state["items"]:
                self.player.set_danmaku_note("本集没有弹幕")
            self._pump_danmaku()          # 顺手预取下一窗

        def failed(message):
            if job != self._danmaku_job:
                return
            state["inflight"] = False
            failure = int((state.get("failures") or {}).get(start) or 0) + 1
            state.setdefault("failures", {})[start] = failure
            if failure >= 3:
                # Give up on this window: retrying every 10s for a whole
                # episode just spammed the status bar.
                state["windows"].add(start)
                self.player.set_danmaku_note("本集弹幕加载失败")
            else:
                # Back off: 10s, then 30s.
                state["last"] = time.time() + (10.0 if failure == 1 else 30.0)
                self.player.set_danmaku_note("弹幕加载失败，稍后重试：%s" % message)
            self.statusBar().showMessage("弹幕加载失败：%s" % message, 6000)

        self._runner.run(work, on_success=done, on_error=failed)

    def _refresh_danmaku_track(self):
        """把已取到的弹幕重建成 ASS 并让 mpv 重读。"""
        state = self._danmaku_state or {}
        items = list((state.get("items") or {}).values())
        if not items:
            return
        items.sort(key=lambda row: int(row.get("timeMs") or 0))
        text = danmaku.build_ass(items, int(state.get("span") or 0),
                                 size=state.get("size") or (1920, 1080),
                                 opacity=self._danmaku_opacity_value / 100.0)
        if not text:
            return
        drama = state.get("drama") or {}
        key = "%s-%d" % (drama.get("id") or "drama", int(state.get("position") or 0) + 1)
        try:
            path = danmaku.write_ass(text, key)
        except OSError:
            return
        if self.player.danmaku_loaded():
            self.player.reload_danmaku(len(items))
        else:
            self.player.attach_danmaku(path, len(items))

    def _release_danmaku_session(self):
        """换集时释放只为弹幕而建的播放会话。"""
        state = self._danmaku_state or {}
        session = str(state.get("owned") or "")
        if not session:
            return
        state["owned"] = ""

        def work():
            try:
                self._core.release(session)
            except Exception:
                pass
        self._runner.run(work)

    def _apply_fullscreen_player_style(self, on):
        """全屏只给播放器本体黑底、控制条用主题色，子控件保持原样式。

        不带 selector 的 setStyleSheet("background:...") 会把背景传给所有子控件，
        按钮/进度条也跟着变黑；用 objectName 选择器只命中容器本身。
        """
        if not on:
            self.player.setStyleSheet("")
            self.player.controls.setStyleSheet("")
            return
        self.player.setStyleSheet(
            "QWidget#playerPane { background: %s; }" % ui.DESIGN["video_bg"])
        self.player.controls.setStyleSheet(
            "QWidget#playerControls { background: %s; border-top: 1px solid %s; }"
            % (ui.DESIGN["surface2"], ui.DESIGN["border"]))

    def _set_video_fullscreen(self, on):
        """Immersive fullscreen: only the video (and its controls) stays."""
        if on == self._video_fullscreen:
            return
        self._video_fullscreen = on
        self.player.set_fullscreen(on)
        # 全屏时去掉 QTabWidget 的页签容器边框；浅色主题下它就是那条白边。
        # QTabWidget 不是 QFrame，Qt5 没有 frameShape 可设；documentMode 会
        # 去掉 pane 的画框，再把内部 layout 的 frameWidth 边距清零，画面才能
        # 真正铺到屏幕四边。
        self.tabs.setProperty("immersive", "true" if on else "false")
        if on:
            if not hasattr(self, "_tabs_document_mode"):
                self._tabs_document_mode = self.tabs.documentMode()
            if not hasattr(self, "_tabs_layout_margins"):
                layout = self.tabs.layout()
                self._tabs_layout_margins = layout.contentsMargins() if layout is not None else None
            self.tabs.setDocumentMode(True)
            layout = self.tabs.layout()
            if layout is not None:
                layout.setContentsMargins(0, 0, 0, 0)
        else:
            self.tabs.setDocumentMode(getattr(self, "_tabs_document_mode", False))
            layout = self.tabs.layout()
            margins = getattr(self, "_tabs_layout_margins", None)
            if layout is not None and margins is not None:
                layout.setContentsMargins(margins)
        self.tabs.style().unpolish(self.tabs)
        self.tabs.style().polish(self.tabs)
        self.tabs.update()
        self.player.setProperty("immersive", "true" if on else "false")
        self._apply_fullscreen_player_style(on)
        self.tabs.setStyleSheet(
            "QTabWidget::pane { border: 0; border-radius: 0; }" if on else "")
        self.player.style().unpolish(self.player)
        self.player.style().polish(self.player)
        self.player.update()
        # 全屏就把页面边距收掉，画面直接顶到屏幕边
        self._player_layout.setContentsMargins(*((0, 0, 0, 0) if on else (10, 10, 10, 10)))
        if on:
            self._fullscreen_was_maximized = self.isMaximized()
            self.menuBar().hide()
            self.statusBar().hide()
            self.tabs.tabBar().hide()
            self.player_side.hide()
            self.showFullScreen()
        else:
            self.menuBar().show()
            self.statusBar().show()
            self.tabs.tabBar().show()
            self.player_side.show()
            if self._fullscreen_was_maximized:
                self.showMaximized()
            else:
                self.showNormal()

    def _exit_video_fullscreen(self):
        if self._video_fullscreen:
            self._set_video_fullscreen(False)

    def play_episode(self, drama, position, quality=None, resume=0.0, chapters=None):
        # Freeze the chapter table for THIS drama.  Reading detail.chapters()
        # meant that opening another drama in the detail tab while watching
        # made 下一集 resolve a chapter of the other show under this drama id.
        chapters = list(chapters) if chapters is not None else list(self.detail.chapters())
        if not chapters or position >= len(chapters):
            self.statusBar().showMessage(
                "没有可播放的分集" if not chapters else "已经是最后一集了", 4000)
            return
        # 换集之前先把上一集的进度存下来（手动点下一集也不丢进度）
        self._remember_progress()
        # 解析下一集要等接口，这段时间旧画面还在放：顺手抓一帧做切集海报，
        # 等真正换源时画面不会直接黑一下。
        self.player.capture_poster()
        previous = getattr(self, "_playback", None)
        previous_title = self.play_title.text()
        self._play_request += 1
        request = self._play_request
        self._playback = {"drama": drama, "position": position, "routes": 0, "session": None,
                          "chapters": chapters, "resume": float(resume or 0.0)}
        # 画质先定下来：P1 停靠栏 set_current_episode 会在播放中立刻钩预热，
        # 晚于这里就只能按「自动」算 key，和 _take_preload 对不上。
        if quality is None:
            index = self.quality_box.currentIndex()
            quality = {0: 0, 1: 1080, 2: 720, 3: 540}.get(index, 0)
        self._playback["quality"] = int(quality or 0)
        self._watch_last_save = time.time()
        # 同一部剧的选集只铺一次；换集只是挪一下「正在播放」的高亮
        fingerprint = (drama.get("id"), len(chapters))
        if getattr(self, "_player_episodes_for", None) != fingerprint:
            self._player_episodes_for = fingerprint
            self.episode_list.set_chapters(chapters)
            self.episode_list.set_downloaded(library.downloaded_episodes(drama.get("id")))
            self.episode_list.set_watched(position, 0.0)
            # P1a：同一份分集喂播放页停靠栏（chapters 是 dict，带 index，可直接吃）
            dock_setter = getattr(self.player, "set_episodes", None)
            if callable(dock_setter):
                dock_setter(list(chapters))
        self.episode_list.set_current(position)
        cur_setter = getattr(self.player, "set_current_episode", None)
        if callable(cur_setter):
            cur_setter(position)
        self.player.set_neighbours(position > 0, position + 1 < len(chapters))
        # 这里马上要开新的一集：清掉「切走时替用户按的暂停」，免得回到播放页时
        # 去继续那个正在被替换掉的旧会话
        self._auto_paused = False
        self.tabs.setCurrentWidget(self.player_page)
        self.play_title.setText("正在解析 %s 第 %d 集…" % (drama.get("title", ""), position + 1))

        # 这一集如果已经预热过，直接播它：省掉解析往返和首段下载等待。
        warmed = self._take_preload(drama, position, quality)
        if warmed is not None:
            self._start_plan(warmed, drama, position)
            return

        def work():
            return self._core.resolve(drama, chapters[position], position + 1, quality=quality)

        def done(plan):
            if request != self._play_request:
                return
            self._start_plan(plan, drama, position)

        def error(message):
            if request != self._play_request:
                return
            # The old episode is still playing; keep its history entry and
            # title instead of leaving the UI pointing at the failed one.
            if previous is not None:
                self._playback = previous
                self.play_title.setText(previous_title or "未在播放")
            else:
                self.play_title.setText("解析失败：%s" % message)
            self.statusBar().showMessage("解析失败：%s" % message, 8000)

        self._runner.run(work, on_success=done, on_error=error)

    def _start_plan(self, plan, drama, position, route_index=0):
        plan = plan or {}
        # 预热 key 用的是用户请求的画质（自动 = 0），不是解析出来的实际画质，
        # 否则「自动」选出的 1080P 计划和下一次请求的 key 对不上。
        requested_quality = int((self._playback or {}).get("quality", plan.get("quality") or 0) or 0)
        self._playback = dict(self._playback or {}, drama=drama, position=position,
                              session=plan.get("session"),
                              routes=int(plan.get("routeCount") or 1),
                              quality=requested_quality)
        # Record history only once the stream resolved, so a failed resolve
        # cannot move "看到第 N 集" onto an episode the user never watched.
        self._watch_last_save = time.time()
        self._watch.save_watch(drama, position + 1,
                               float(self._playback.get("resume") or 0.0), 0.0, False)
        self.route_box.blockSignals(True)
        self.route_box.clear()
        for index in range(self._playback["routes"]):
            self.route_box.addItem("线路 %d" % (index + 1))
        self.route_box.setCurrentIndex(min(route_index, max(0, self.route_box.count() - 1)))
        self.route_box.blockSignals(False)
        headers = plan.get("headers") or {}
        key = plan.get("decryptionKey") or ""
        local = bool(plan.get("local"))
        title = "%s · 第 %d 集" % (drama.get("title", "短剧"), position + 1)
        self.play_title.setText(title)
        resume = float((self._playback or {}).get("resume") or 0.0)
        self.player.play(plan.get("url"), headers=headers, decryption_key=key, local=local,
                         title=title, resume=resume, reuse=True,
                         hint="正在加载 第 %d 集" % (position + 1))
        # 下载时保存过弹幕就直接挂同名 .ass；没有才走在线逐窗抓取。
        if not (local and self._attach_local_danmaku(plan, drama, position)):
            self._queue_danmaku(plan.get("session"), drama, position)
        if not local:
            # 换剧/换画质后，旧预热就没用了：先释放，再排下一集的预热。
            self._prune_preloads(drama, requested_quality)
            self._schedule_preload()

    # ------------------------------------------------------------ 下一集预热
    def _schedule_preload(self):
        """等当前这一集的首屏缓冲稳定后再去预热下一集，避免抢带宽。"""
        request = self._play_request
        QTimer.singleShot(2500, lambda: self._preload_next(request))

    @staticmethod
    def _preload_key(drama, position, quality):
        return (str((drama or {}).get("id") or ""), int(position), int(quality or 0))

    def _preload_next(self, request):
        if self._closed or request != self._play_request:
            return
        playback = self._playback or {}
        drama = playback.get("drama") or {}
        chapters = playback.get("chapters") or []
        position = int(playback.get("position", 0) or 0)
        quality = int(playback.get("quality", 0) or 0)
        if not drama or playback.get("local_path") or position + 1 >= len(chapters):
            return
        target = position + 1
        key = self._preload_key(drama, target, quality)
        if key in self._preloads or key in self._preloading:
            return
        self._preloading.add(key)
        plan_arg = chapters[target]
        index_arg = target + 1

        def work():
            return self._core.preload(drama, plan_arg, index_arg, quality=quality)

        def done(plan):
            self._preloading.discard(key)
            plan = plan or {}
            session = plan.get("session")
            # 已下载的集会返回 local 计划（没有会话），同样可以直接用。
            usable = bool(plan.get("local") and plan.get("url")) or bool(session)
            if request != self._play_request or not usable:
                # 用户已经切走/停止：这份预热不再需要，立刻把会话还回去。
                if session:
                    self._release_session(session)
                return
            self._preloads[key] = {"plan": plan, "at": time.time()}
            self._prune_preloads(drama, quality)

        def error(message):
            self._preloading.discard(key)

        self._runner.run(work, on_success=done, on_error=error)

    def _take_preload(self, drama, position, quality):
        """取出并消费这一集的预热计划；过期或会话失效就退回现场解析。"""
        key = self._preload_key(drama, position, quality)
        entry = self._preloads.pop(key, None)
        if not entry:
            return None
        plan = entry.get("plan") or {}
        if plan.get("local"):
            # 本地缓存集：确认文件还在就能直接播，没有会话要释放。
            if not plan.get("url") or not os.path.isfile(plan.get("url")):
                return None
            return plan
        if not plan.get("session"):
            return None
        expires = plan.get("expiresAt")
        if expires:
            try:
                if time.time() * 1000.0 >= float(expires) - 2000:
                    self._release_preload(entry)
                    return None
            except (TypeError, ValueError):
                pass
        # 停在一集上看了很久，凭证可能已经过期；超过 20 分钟就重新解析。
        if time.time() - float(entry.get("at") or 0.0) > 20 * 60:
            self._release_preload(entry)
            return None
        return plan

    def _release_preload(self, entry):
        session = ((entry or {}).get("plan") or {}).get("session")
        if session:
            self._release_session(session)

    def _release_session(self, session):
        def work():
            self._core.release(session)
        self._runner.run(work, on_error=lambda _: None)

    def _prune_preloads(self, drama, quality):
        """只留当前这部剧、当前画质的预热，最多两份，其余释放掉。"""
        drama_id = str((drama or {}).get("id") or "")
        quality = int(quality or 0)
        for key in list(self._preloads):
            if key[0] != drama_id or key[2] != quality:
                self._release_preload(self._preloads.pop(key))
        while len(self._preloads) > PRELOAD_LIMIT:
            oldest = min(self._preloads, key=lambda k: self._preloads[k]["at"])
            self._release_preload(self._preloads.pop(oldest))

    def _clear_preloads(self):
        entries = list(self._preloads.values())
        self._preloads.clear()
        for entry in entries:
            self._release_preload(entry)

    def _switch_route(self, index):
        playback = getattr(self, "_playback", None)
        if not playback or index < 0 or not playback.get("session"):
            return
        session = playback["session"]
        self.player.capture_poster()

        def work():
            for _ in range(index + 1):
                plan = self._core.fallback(session)
            return plan

        def done(plan):
            self._start_plan(plan, playback["drama"], playback["position"], route_index=index)

        self._runner.run(work, on_success=done, on_error=lambda m: self.statusBar().showMessage(m, 6000))

    def _on_episode_row(self, row):
        playback = getattr(self, "_playback", None)
        if row < 0 or not playback or row == playback.get("position"):
            return
        if playback.get("local_path"):
            return      # a local file has no episode list to switch through
        self.play_episode(playback["drama"], row,
                          chapters=playback.get("chapters"))

    def _play_neighbour(self, step):
        """进度条旁边的「上一集 / 下一集」（本地文件没有集的概念，直接忽略）。"""
        playback = getattr(self, "_playback", None)
        if not playback or playback.get("local_path"):
            return
        chapters = playback.get("chapters") or []
        target = int(playback.get("position") or 0) + step
        if 0 <= target < len(chapters):
            self.play_episode(playback["drama"], target, chapters=chapters)
        else:
            self.statusBar().showMessage("已经是第一集了" if step < 0 else "已经是最后一集了", 3000)

    def _remember_progress(self):
        """离开当前这一集之前把进度落库：手动换集不该丢掉刚看的几分钟。"""
        playback = getattr(self, "_playback", None)
        if not playback or self.player._duration <= 0:
            return
        position = 0.0
        if self.player.is_playing():
            try:
                position = float(self.player._session.get("time-pos") or 0.0)
            except (TypeError, ValueError):
                position = 0.0
        if playback.get("local_path"):
            self._watch.save_local_watch(playback["local_path"], position,
                                         self.player._duration, False)
        else:
            self._watch.save_watch(playback.get("drama") or {},
                                   playback.get("position", 0) + 1,
                                   position, self.player._duration, False)

    def _download_current(self):
        playback = getattr(self, "_playback", None)
        if not playback:
            return
        if playback.get("local_path"):
            self.statusBar().showMessage("本地文件不需要下载", 4000)
            return
        self.enqueue_downloads(playback["drama"], [playback["position"]],
                               chapters=playback.get("chapters"))

    def stop_playback(self):
        playback = getattr(self, "_playback", None)
        self._remember_progress()
        self._play_request += 1
        self.player.stop()
        self.play_title.setText("未在播放")
        self._clear_preloads()
        try:
            self._watch.flush()   # 停止播放时立刻落盘，不等节流窗口
        except Exception:
            pass
        if playback and playback.get("session"):
            session = playback["session"]

            def work():
                self._core.release(session)

            self._runner.run(work, on_error=lambda _: None)
        self._playback = None

    def _on_watch_progress(self, position, duration):
        """Throttled viewing-history updates while playing."""
        playback = getattr(self, "_playback", None)
        if not playback or not duration or duration <= 0:
            return
        now = time.time()
        if now - self._watch_last_save < 5.0:
            return
        self._watch_last_save = now
        if playback.get("local_path"):
            self._watch.save_local_watch(playback["local_path"], float(position),
                                         float(duration), False)
        else:
            self._watch.save_watch(playback.get("drama") or {}, playback.get("position", 0) + 1,
                                   float(position), float(duration), False)

    def _on_play_finished(self):
        playback = getattr(self, "_playback", None)
        if not playback:
            return
        self._watch_last_save = 0.0
        if playback.get("local_path"):
            self._watch.save_local_watch(playback["local_path"], self.player._duration,
                                         self.player._duration, True)
            self.play_title.setText("播放完毕")
            return
        self._watch.save_watch(playback.get("drama") or {}, playback.get("position", 0) + 1,
                               self.player._duration, self.player._duration, True)
        chapters = playback.get("chapters") or []
        nxt = playback.get("position", 0) + 1
        if nxt < len(chapters):
            # Capture the request id: pressing 停止 or picking another episode
            # in the next tick must cancel this auto-advance.  No artificial
            # delay: the next plan is preloaded and mpv can loadfile straight
            # away, keeping the last frame until the new one renders.
            request = self._play_request
            QTimer.singleShot(0, lambda: self._auto_advance(request, playback, nxt))
        else:
            self.play_title.setText("本季播放完毕，正在查下一季…")
            self._check_next_season_after_end(playback.get("drama") or {})

    def _auto_advance(self, request, playback, position):
        if request != self._play_request:
            return
        self.play_episode(playback["drama"], position,
                          chapters=playback.get("chapters"))

    def _check_next_season_after_end(self, drama):
        """本季最后一集播完：按标题查一次下一季，找到就提示去观看。

        不跨季自动播放——下一季是另一部剧，先让用户确认；没找到也不弹窗，
        只在状态栏提示，避免每集结尾都打断。
        """
        title = str(drama.get("title") or "").strip()
        key = series.series_key(title)
        if not key:
            return
        current = season_number(title)
        prompt_key = (drama.get("id"), int(drama.get("episodes") or 0), current)
        if prompt_key in self._next_season_prompted:
            return
        source = str(drama.get("source") or "")
        cache_key = source + ":" + key
        cached = self._series_cache.get(cache_key)
        if cached and time.time() - cached[0] < 900:
            self._apply_next_season(drama, cached[1], current, prompt_key)
            return
        if key in self._next_season_busy:
            return
        self._next_season_busy.add(key)

        def work():
            result = self._core.catalog(query=series.base_title(title) or title,
                                        page=1, source=source or "hongguo",
                                        lane="series:next")
            return series.group((result or {}).get("items") or [], title)

        def done(rows):
            self._next_season_busy.discard(key)
            if self._closed:
                return
            self._series_cache[cache_key] = (time.time(), rows)
            self._apply_next_season(drama, rows, current, prompt_key)

        def error(_message):
            self._next_season_busy.discard(key)
            self.play_title.setText("本剧已播放完毕")
            self.statusBar().showMessage("没查到下一季，可在详情页预约", 6000)

        self._runner.run(work, on_success=done, on_error=error)

    def _apply_next_season(self, drama, rows, current, prompt_key):
        if self._closed:
            return
        found = series.next_season(rows or [], current)
        if not found:
            self.play_title.setText("本剧已播放完毕")
            self.statusBar().showMessage("没有找到下一季，可在详情页预约", 6000)
            return
        self._next_season_prompted.add(prompt_key)
        next_drama = found.get("drama") or {}
        if (self._watch.is_followed(drama.get("id"))
                or self._watch.is_reserved(drama.get("id"))):
            self._watch.mark_next_season(drama.get("id"), next_drama,
                                         found.get("season"))
        title = next_drama.get("title") or "下一季"
        self.play_title.setText("已找到下一季：%s" % title)
        answer = QMessageBox.question(
            self, "下一季",
            "已找到下一季：\n%s\n\n现在去观看吗？" % title,
            QMessageBox.Yes | QMessageBox.No, QMessageBox.Yes)
        if answer == QMessageBox.Yes:
            self.open_detail(next_drama)

    def open_local(self, path, decryption_key="", resume=0.0):
        """Play a local file, keeping the key when the file is still encrypted.

        Decrypted episodes under 下载/ need no key; the core cache copies are
        CENC-encrypted, and dropping the key there is what used to wedge mpv.
        Local playback is recorded in the viewing history with the file path,
        so merged outputs resume from the history too.
        """
        self._play_request += 1
        self._clear_preloads()
        self._playback = {"local_path": os.path.abspath(path), "position": 0,
                          "resume": float(resume or 0.0), "drama": None,
                          "chapters": []}
        self._watch_last_save = time.time()
        self._watch.save_local_watch(path, float(resume or 0.0), 0.0, False)
        self._auto_paused = False
        self.tabs.setCurrentWidget(self.player_page)
        # A local file has no season: clear the episode strip so it cannot
        # look like the previous drama's chapters are still selectable.
        self._player_episodes_for = None
        self.episode_list.set_chapters([])
        dock_setter = getattr(self.player, "set_episodes", None)
        if callable(dock_setter):
            dock_setter([])   # P1a：本地文件没有分集概念，停靠栏跟着清空
        self.player.set_neighbours(False, False)
        self.play_title.setText("本地播放 %s" % os.path.basename(path))
        self.player.play(path, headers={}, decryption_key=decryption_key or "",
                         local=True, title=os.path.basename(path), resume=float(resume or 0.0),
                         reuse=True)
        self._attach_sidecar_danmaku(path)

    # ---------------------------------------------------- follow & history
    def _build_watch(self):
        self.follow_panel = WatchPanel(self._watch, "follow", self._covers)
        self.follow_panel.playRequested.connect(self._play_from_watch)
        # localPlayRequested carries (path, resume-seconds); open_local's
        # second positional parameter is the decryption key.  Passing it
        # straight through fed the resume position to mpv as a key.
        self.follow_panel.localPlayRequested.connect(
            lambda path, resume: self.open_local(path, resume=resume))
        return self.follow_panel

    def _build_calendar(self):
        self.calendar_panel = CalendarPanel(self._core, self._watch)
        self.calendar_panel.openDrama.connect(self.open_detail)
        self.calendar_panel.playDrama.connect(self._play_from_new_series)
        self.calendar_panel.statusMessage.connect(
            lambda text: self.statusBar().showMessage(text, 6000))
        return self.calendar_panel

    def _play_from_new_series(self, drama):
        """新剧日历的「播放」：先拉详情，章节就绪后从第一集起播。"""
        self._pending_play = {"drama": drama, "position": 0, "resume": 0.0}
        self.open_detail(drama)

    def _build_history(self):
        self.history_panel = WatchPanel(self._watch, "history", self._covers)
        self.history_panel.playRequested.connect(self._play_from_watch)
        self.history_panel.localPlayRequested.connect(
            lambda path, resume: self.open_local(path, resume=resume))
        return self.history_panel

    def _play_from_watch(self, drama, episode_index, resume):
        """Continue from the follow/history lists, resuming the position."""
        current = self.detail.drama()
        if current and current.get("id") == drama.get("id") and self.detail.chapters():
            self.play_episode(current, episode_index, resume=resume)
            return
        self._pending_play = {"drama": drama, "position": episode_index, "resume": resume}
        self.open_detail(drama)

    # -------------------------------------------------------------- downloads
    def _build_downloads(self):
        self.downloads = DownloadPanel(self._core)
        self.downloads.statusMessage.connect(lambda m: self.statusBar().showMessage(m, 8000))
        self.downloads.openLocalRequested.connect(self.open_local)
        self.downloads.playEpisodeRequested.connect(self.play_from_downloads)
        self._download_view = self.downloads
        return self.downloads

    def play_from_downloads(self, drama, position, resume=0.0):
        """Play a finished episode with the full episode flow, so the season's
        chapter list, auto-next and episode picking all come alive."""
        current = self.detail.drama()
        if current and current.get("id") == drama.get("id") and self.detail.chapters():
            self.play_episode(current, position, resume=resume)
            return
        self._pending_play = {"drama": drama, "position": position, "resume": resume}
        self.open_detail(drama)

    def enqueue_downloads(self, drama, positions, chapters=None):
        chapters = list(chapters) if chapters is not None else list(self.detail.chapters())
        if not drama or not positions:
            return
        entries = [{"chapter": chapters[position], "index": position + 1}
                   for position in positions if position < len(chapters)]
        if not entries:
            self.statusBar().showMessage("没有可下载的分集：分集列表未加载完成", 6000)
            return
        # Downloads land in the core cache first, so a full disk is caught
        # here instead of after a few hundred MB of partial files.
        try:
            media.ensure_free_space(tools.core_cache_dir(), 1500 << 20, "下载")
        except media.MediaError as exc:
            self.statusBar().showMessage(str(exc), 12000)
            return
        self.tabs.setCurrentWidget(self.downloads_page)
        self.downloads.refresh()

        def work():
            # The core caps one enqueue at 500 episodes, so long seasons
            # (600+) are submitted in chunks - exactly like the reference
            # app does - and the totals are summed back up.
            added = 0
            for offset in range(0, len(entries), 500):
                chunk = entries[offset:offset + 500]
                added += (self._core.enqueue(drama, chunk) or {}).get("added", 0)
            return added

        def done(added):
            self.statusBar().showMessage("下载队列新增 %d 集" % (added or 0), 6000)
            self.downloads.refresh()

        self._runner.run(work, on_success=done,
                         on_error=lambda m: self.statusBar().showMessage("加入下载失败：%s" % m, 8000))

    def _on_tab(self, index):
        previous = getattr(self, "_tab_index", index)
        self._tab_index = index
        current = self.tabs.widget(index)
        before = self.tabs.widget(previous) if previous != index else current
        # 离开播放页临时暂停、回来接着放；不让视频在后台自己跑
        if before is self.player_page and current is not self.player_page:
            self._pause_in_background()
        elif current is self.player_page and before is not self.player_page:
            self._resume_in_background()
        if current is self.downloads_page:
            self.downloads.refresh()
        elif current is self.calendar_panel:
            self.calendar_panel.refresh()
        elif current is self.library_page and not self.board_box.count():
            self._load_boards()
        elif current is self.detail:
            self.detail.refresh_marks()      # 下载完了切回来，「已下载」角标要对得上
        elif current is self.follow_panel:
            self._watch.mark_arrivals_seen()
            self._sync_watch_tab()
            self.follow_panel.refresh()      # 页签可见了才去拉海报
        elif current is self.history_panel:
            self.history_panel.refresh()
        elif current is self.settings_page:
            self._ensure_settings_panel()

    def _pause_in_background(self):
        """切到别的页签就暂停，不用用户先手动暂停再走。"""
        if not self.player.is_playing() or self.player.is_paused():
            self._auto_paused = False
            return
        self._auto_paused = self.player.set_paused(True)
        if self._auto_paused:
            self.statusBar().showMessage("已自动暂停，回到「在线播放」页会自动继续", 6000)

    def _resume_in_background(self):
        """只有「因为切走才暂停的」才自动继续，用户自己按的暂停不动。"""
        if not self._auto_paused:
            return
        self._auto_paused = False
        if self.player.set_paused(False):
            self.statusBar().showMessage("继续播放", 3000)

    def _request_covers(self, dramas):
        """Queue poster loads for a list of dramas (skips known ids)."""
        for drama in dramas:
            self._covers.request(drama)

    def _on_cover(self, drama_id, pixmap):
        self.grid.set_pixmap(drama_id, pixmap)
        self.search_grid.set_pixmap(drama_id, pixmap)
        self.detail.set_cover(drama_id, pixmap)

    def _on_cover_failed(self, drama_id):
        """A poster that gave up shows the drama's title instead of a hole."""
        self.grid.set_failed(drama_id)
        self.search_grid.set_failed(drama_id)

    # ------------------------------------------------------------------ close
    def closeEvent(self, event):
        if self._closed:
            event.accept()
            return
        self._closed = True
        self._clear_preloads()
        try:
            self.downloads.shutdown()
        except Exception:
            pass
        # Blocking: on exit mpv must not survive the window as an orphan.
        self.player.stop(blocking=True)
        # Any ffmpeg still running (a merge/decrypt started from this window)
        # would otherwise outlive the app and keep writing to disk.
        try:
            media.kill_running()
        except Exception:
            pass
        try:
            self._runner.shutdown(800)
        except Exception:
            pass
        # Progress writes are throttled; make sure nothing stays pending.
        try:
            self._watch.flush()
        except Exception:
            pass
        self.detail.shutdown()
        self.grid.shutdown()
        self.browse_panel.shutdown()
        # S1 的面板可能自带线程/加载器；约定同名 shutdown，没有就算了。
        panel = getattr(self, "_settings_panel", None)
        if panel is not None and hasattr(panel, "shutdown"):
            try:
                panel.shutdown()
            except Exception:
                pass
        self._covers.stop()
        if self._tray is not None:
            self._tray.hide()
        QApplication.instance().quit()
        event.accept()


def _install_translations(app):
    """让 Qt 自带控件的文案说中文。

    PyQt5 只带了 qtbase_zh_TW，中文目录只有一份 Qt4 时代的 qt_zh_CN.qm：输入框的
    剪切/复制/粘贴、消息框的「确定」都在里面；QWidgetTextControl（简介那种文本
    视图）的右键菜单不在，那份由 widgets.SummaryView 自己提供。词典必须由 app
    持有，否则马上被回收。
    """
    for name in ("qtbase_zh_CN", "qt_zh_CN"):
        translator = QTranslator(app)
        if translator.load(name, QLibraryInfo.location(QLibraryInfo.TranslationsPath)):
            app.installTranslator(translator)
            return translator
    return None


def _install_crash_logging():
    """Write uncaught exceptions to app.log; the GUI build has no console.

    PyQt5 aborts the process on an exception raised inside a slot unless
    sys.excepthook has been replaced, so this also turns a hard crash into a
    logged error the next run can explain.
    """
    def hook(exc_type, exc, tb):
        text = "".join(traceback.format_exception(exc_type, exc, tb))
        tools.log_text("未捕获异常：\n" + text)
        try:
            sys.__excepthook__(exc_type, exc, tb)
        except Exception:
            pass

    sys.excepthook = hook
    if hasattr(threading, "excepthook"):
        def thread_hook(args):
            tools.log_text("后台线程异常（%s）：\n%s" % (
                getattr(args.thread, "name", "?"),
                "".join(traceback.format_exception(args.exc_type, args.exc_value,
                                                   args.exc_traceback))))
        threading.excepthook = thread_hook


def main():
    os.environ.setdefault("QT_ENABLE_HIGHDPI_SCALING", "1")
    app = QApplication(sys.argv)
    app.setApplicationName(APP_NAME)
    _install_crash_logging()
    icon = tools.app_icon()
    if os.path.isfile(icon):
        app.setWindowIcon(QIcon(icon))
    _install_translations(app)
    ui.apply_theme(tools.theme())
    app.setStyleSheet(build_stylesheet())
    # One window only: two instances share the cache/index and would fight
    # over the same files (and both spawn a sidecar).
    try:
        os.makedirs(tools.data_dir(), exist_ok=True)
    except OSError:
        pass
    instance_lock = QLockFile(os.path.join(tools.data_dir(), "app.lock"))
    instance_lock.setStaleLockTime(30000)
    if not instance_lock.tryLock(100):
        QMessageBox.information(None, APP_NAME,
                                "真果鉴已经在运行了。\n\n请在任务栏里找一下已有的窗口。")
        return 0
    core = CoreClient(tools.data_dir())
    # 核心初始化通常 1-2 秒，但冷启动/杀软扫描时可能更久；先给一个启动卡片，
    # 避免用户双击后长时间没有任何反馈。
    splash = None
    icon_pixmap = QIcon(icon).pixmap(256, 256) if os.path.isfile(icon) else QPixmap()
    if not icon_pixmap.isNull():
        card = QPixmap(460, 260)
        card.fill(Qt.transparent)
        painter = QPainter(card)
        painter.setRenderHint(QPainter.Antialiasing, True)
        painter.setBrush(QColor(27, 30, 36, 175))
        painter.setPen(QPen(QColor(255, 255, 255, 45), 1))
        painter.drawRoundedRect(1, 1, 458, 258, 26, 26)
        painter.drawPixmap(32, 66, 128, 128, icon_pixmap)
        title_font = QFont("Microsoft YaHei", 18)
        title_font.setBold(True)
        painter.setFont(title_font)
        painter.setPen(QColor("#ffffff"))
        painter.drawText(184, 108, "真果鉴")
        painter.setFont(QFont("Microsoft YaHei", 10))
        painter.setPen(QColor("#d5dbe5"))
        painter.drawText(184, 142, "正在初始化本地核心…")
        painter.end()
        splash = QSplashScreen(card)
        splash.setAttribute(Qt.WA_TranslucentBackground)
        splash.show()
        app.processEvents()
    try:
        core.start()
    except CoreError as exc:
        if splash is not None:
            splash.close()
        QMessageBox.critical(None, APP_NAME, "本地核心启动失败：%s" % exc)
        return 1
    if splash is not None:
        splash.close()
    window = MainWindow(core)
    core.on_restart = lambda: window.restartNotice.emit("本地核心已自动恢复")
    window.show()
    try:
        return app.exec_()
    finally:
        core.close()


if __name__ == "__main__":
    sys.exit(main())
