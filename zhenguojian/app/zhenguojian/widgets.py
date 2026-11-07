"""Reusable widgets: cover grids, episode lists and async cover loading."""
from __future__ import annotations

import os
import re
import subprocess
import threading
import time
from collections import OrderedDict

from PyQt5.QtCore import QEvent, QObject, QPointF, QRect, QRectF, QSize, Qt, QTimer, pyqtSignal
from PyQt5.QtGui import QColor, QFont, QFontMetrics, QIcon, QPainter, QPen, QPixmap
from PyQt5.QtWidgets import (QAbstractItemView, QApplication, QFrame, QHBoxLayout, QLabel,
                             QLineEdit, QListWidget, QListWidgetItem, QMenu,
                             QPushButton, QStyledItemDelegate, QTextBrowser,
                             QVBoxLayout, QWidget)

from . import tools
from .ui import DESIGN
from .poster_tooltip import poster_tooltip as _poster_tooltip

# Match the view's iconSize exactly: a larger pixmap gets rendered at its
# own height by the QSS-styled item, silently eating the text area below.
THUMB_SIZE = QSize(150, 210)
GRID_SIZE = QSize(190, 318)
NEWLINE = chr(10)     # 卡片文字里换行：拼字符串时比转义写法好读，也不怕被工具吞掉


class CoverDelegate(QStyledItemDelegate):
    """Reserve a full grid cell for icon + wrapped text.

    The default sizeHint tightens around the content, and the list paints
    each item clipped to it - whenever a title wrapped to two lines the
    meta line lost its bottom half.  Handing back the whole cell makes the
    clip boundary the grid line, where there is always room.
    """

    def sizeHint(self, option, index):
        return QSize(GRID_SIZE.width(), GRID_SIZE.height())


def _card_pixmap(caption="", fill=None):
    """Dark card standing in for a poster that is not there.

    A flat Qt.darkGray box - the old placeholder - read as a hole punched in
    the dark grid: every tile of a freshly opened page flashed light gray
    until its image arrived.  This keeps the app's own palette instead, and
    an optional caption says which kind of "no poster" this is.
    """
    pixmap = QPixmap(THUMB_SIZE)
    pixmap.fill(QColor(fill or DESIGN["surface3"]))
    painter = QPainter(pixmap)
    painter.setPen(QColor(DESIGN["border"]))
    painter.drawRect(0, 0, THUMB_SIZE.width() - 1, THUMB_SIZE.height() - 1)
    if caption:
        painter.setPen(QColor(DESIGN["muted"]))
        font = painter.font()
        font.setPointSize(10)
        painter.setFont(font)
        painter.drawText(QRect(12, 12, THUMB_SIZE.width() - 24, THUMB_SIZE.height() - 24),
                         Qt.AlignCenter | Qt.TextWordWrap, caption)
    painter.end()
    return pixmap


def _placeholder_pixmap():
    """Still loading: an empty slot (the title already sits under the tile)."""
    return _card_pixmap()


def missing_tile():
    """No poster, and none coming - say so instead of staying blank."""
    return _card_pixmap("暂无封面", DESIGN["surface2"])


def cover_pixmap(pixmap):
    """Fit a cover into THUMB_SIZE exactly (object-fit: cover).

    KeepAspectRatioByExpanding can exceed the box in one dimension, and the
    list paints the icon at its pixmap size - a taller pixmap silently eats
    the text area under the poster.  Crop the overflow, centered.
    """
    if pixmap.isNull():
        return pixmap
    scaled = pixmap.scaled(THUMB_SIZE, Qt.KeepAspectRatioByExpanding, Qt.SmoothTransformation)
    width, height = scaled.width(), scaled.height()
    if width == THUMB_SIZE.width() and height == THUMB_SIZE.height():
        return scaled
    left = max(0, (width - THUMB_SIZE.width()) // 2)
    top = max(0, (height - THUMB_SIZE.height()) // 2)
    return scaled.copy(left, top, THUMB_SIZE.width(), THUMB_SIZE.height())


def _card_label(title, meta, metrics):
    """海报下面那两段文字，标题固定占两行。

    同一行里有的卡片标题折两行、有的一行，下面的「66 集 · 家庭」就会上下
    错开；给标题补足两行，元信息基线就齐了。宽度按网格宽度再减掉 item 的
    左右内边距估，宁可多算一行（那样只是整体低一行，不会参差）。
    """
    wrapped = metrics.boundingRect(0, 0, GRID_SIZE.width() - 20, 0, Qt.TextWordWrap, title)
    lines = max(1, min(2, int(round(wrapped.height() / max(1, metrics.height())))))
    label = title + NEWLINE * (2 - lines)
    if meta:
        label += NEWLINE + " · ".join(meta)
    return label


class CoverLoader(QObject):
    """Fetches covers through the core, decoding HEIC through ffmpeg.

    A failed load (busy core, network hiccup) is retried with backoff a few
    times before giving up - a one-shot failure used to leave tiles gray
    until the page happened to rebuild.

    Decoded posters are kept in an LRU: one is ~120KB in RAM, and a long
    browsing session can walk through thousands of them, so an unbounded
    dict would quietly grow into the hundreds of megabytes.
    """

    RETRY_DELAYS = (5.0, 15.0, 30.0)
    MAX_CACHED = 600
    THUMB_WIDTH = 320            # 2 倍于界面上的 150px 格子，HiDPI 也够
    THUMB_BYTES_LIMIT = 300 * 1024 * 1024
    THUMB_COUNT_LIMIT = 5000

    loaded = pyqtSignal(str, object)   # drama id, QPixmap
    failed = pyqtSignal(str)

    def __init__(self, core, parent=None):
        super().__init__(parent)
        self._core = core
        self._pending = set()          # queued or in flight right now
        self._pixmaps = OrderedDict()   # id -> pixmap, oldest first
        self._attempts = {}
        self._lock = threading.Lock()
        self._queue = []
        self._signal = threading.Condition(self._lock)
        self._stopped = False
        self._thumbs_purged = False
        self._thumb_writes = 0
        self._threads = [threading.Thread(target=self._worker, daemon=True) for _ in range(4)]
        for thread in self._threads:
            thread.start()

    def cached(self, drama_id):
        """Pixmap decoded earlier, so a page opened again shows it at once.

        A hit also marks it as recently used, so the posters still on
        screen are the last ones the cache would drop.
        """
        with self._lock:
            pixmap = self._pixmaps.get(drama_id)
            if pixmap is not None:
                self._pixmaps.move_to_end(drama_id)
            return pixmap

    def _store(self, drama_id, pixmap):
        """Cache a poster and evict the oldest ones past the cap."""
        with self._lock:
            self._pixmaps[drama_id] = pixmap
            self._pixmaps.move_to_end(drama_id)
            overflow = len(self._pixmaps) - self.MAX_CACHED
            if overflow > 0:
                for _ in range(overflow):
                    self._pixmaps.popitem(last=False)

    def known(self, drama_id):
        """True when there is nothing to fetch: decoded, or already loading.

        Deliberately false for an evicted id - the file is still on disk,
        so re-decoding it is cheap and beats leaving a gray tile.
        """
        with self._lock:
            return drama_id in self._pixmaps or drama_id in self._pending

    def request(self, drama, force=False):
        identifier = drama.get("id")
        if not identifier:
            return
        with self._lock:
            if not force and identifier in self._pending:
                return
            self._pending.add(identifier)
            self._queue.append(drama)
            self._signal.notify_all()

    def forget(self, drama_id):
        with self._lock:
            self._pending.discard(drama_id)
            self._pixmaps.pop(drama_id, None)

    def _requeue(self, drama):
        with self._lock:
            if self._stopped:
                return
            self._queue.append(drama)
            self._signal.notify_all()

    def _worker(self):
        while True:
            with self._lock:
                while not self._queue and not self._stopped:
                    self._signal.wait(1.0)
                if self._stopped:
                    return
                drama = self._queue.pop(0)
            try:
                pixmap = self._load(drama)
            except Exception:
                pixmap = None
            if pixmap is not None:
                with self._lock:
                    self._attempts.pop(drama.get("id"), None)
                    # Not just bookkeeping: known() treats "in _pending" as
                    # "already loading", so leaking it meant an evicted cover
                    # was never requested again for the rest of the session.
                    self._pending.discard(drama.get("id"))
                self._store(drama.get("id"), pixmap)
                self.loaded.emit(drama.get("id"), pixmap)
            else:
                attempt = self._attempts.get(drama.get("id"), 0)
                if attempt < len(self.RETRY_DELAYS):
                    with self._lock:
                        self._attempts[drama.get("id")] = attempt + 1
                    delay = self.RETRY_DELAYS[attempt]
                    threading.Timer(delay, self._requeue, args=(drama,)).start()
                else:
                    # Give up for now: a later page rebuild may try again.
                    with self._lock:
                        self._attempts.pop(drama.get("id"), None)
                    self.forget(drama.get("id"))
                    self.failed.emit(drama.get("id"))

    def _load(self, drama):
        info = self._core.cover(drama)
        path = info.get("path")
        if not path or not os.path.isfile(path):
            return None
        if info.get("heic"):
            path = self._convert(path)
            if not path:
                return None
        pixmap = QPixmap(path)
        if pixmap.isNull():
            return None
        return cover_pixmap(pixmap)

    def _convert(self, source):
        """HEIC 原图 → 显示用的小 JPEG。

        PNG 存照片是纯浪费：实测同一张海报 PNG 4.2MB、JPEG(q=3) 0.59MB，而界面
        只画到 150x210。所以缩到 2 倍显示宽度再存 JPEG，顺带把老版本留下的
        PNG 缓存（本机曾经 1.7GB）清掉，并把缓存总量封顶。
        """
        ffmpeg = tools.ffmpeg_path()
        if not ffmpeg:
            return None
        cache = os.path.join(tools.data_dir(), "thumbs")
        os.makedirs(cache, exist_ok=True)
        self._purge_legacy_thumbs(cache)
        target = os.path.join(cache, os.path.splitext(os.path.basename(source))[0] + ".jpg")
        if os.path.isfile(target) and os.path.getsize(target) > 0:
            return target
        try:
            subprocess.run([ffmpeg, "-y", "-loglevel", "error", "-i", source,
                            "-frames:v", "1", "-vf", "scale=%d:-2" % self.THUMB_WIDTH,
                            "-q:v", "3", target],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=90,
                           creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0) if os.name == "nt" else 0)
        except Exception:
            return None
        if not os.path.isfile(target):
            return None
        self._trim_thumbs(cache)
        return target

    def _purge_legacy_thumbs(self, cache):
        """老版本把 HEIC 转成 PNG 存在这里，一张能到 4MB；新代码只认 .jpg。"""
        if self._thumbs_purged:
            return
        self._thumbs_purged = True
        try:
            names = [name for name in os.listdir(cache) if name.lower().endswith(".png")]
        except OSError:
            return
        for name in names:
            try:
                os.remove(os.path.join(cache, name))
            except OSError:
                pass

    def _trim_thumbs(self, cache):
        """缓存总量封顶：超过就按最久没用的先删（每 200 张新图检查一次）。"""
        self._thumb_writes += 1
        if self._thumb_writes % 200:
            return
        try:
            entries = []
            total = 0
            for entry in os.scandir(cache):
                if not entry.is_file():
                    continue
                stat = entry.stat()
                total += stat.st_size
                entries.append((stat.st_mtime, entry.path, stat.st_size))
        except OSError:
            return
        if total <= self.THUMB_BYTES_LIMIT and len(entries) <= self.THUMB_COUNT_LIMIT:
            return
        entries.sort()
        for _, path, size in entries:
            try:
                os.remove(path)
            except OSError:
                continue
            total -= size
            if total <= self.THUMB_BYTES_LIMIT * 0.8:
                break

    def stop(self):
        with self._lock:
            self._stopped = True
            self._signal.notify_all()


class CoverGrid(QWidget):
    """Poster grid with a paging bar.

    Pages are kept as separate batches instead of one ever-growing list, so
    上一页/下一页 can walk back and forth over what was already fetched
    without hitting the network again.  Only the newest page can ask for
    more, and 换一批 throws the whole stack away for a fresh first page.

    One page is all the grid ever renders, which is what keeps the feed
    cheap: the core answers a catalog request with every drama it has
    accumulated (thousands of them), so the window slices that answer into
    batches and hands them over here.
    """

    dramaActivated = pyqtSignal(object)
    selectionChanged = pyqtSignal(list)
    moreRequested = pyqtSignal()
    refreshRequested = pyqtSignal()

    def __init__(self, parent=None, columns=6, covers=None):
        super().__init__(parent)
        self._items = {}
        self._columns = columns
        self._pages = []          # list of (list of drama dict, has_more)
        self._page = 0
        self._covers = covers     # CoverLoader, used to restore cached posters
        self._cover_request = None
        self._interactive = True
        self._last_activated = None
        self._last_activated_at = 0.0

        self.list = QListWidget()
        self.list.setViewMode(QListWidget.IconMode)
        self.list.setResizeMode(QListWidget.Adjust)
        self.list.setMovement(QListWidget.Static)
        self.list.setWordWrap(True)
        self.list.setIconSize(THUMB_SIZE)
        self.list.setGridSize(GRID_SIZE)
        self.list.setItemDelegate(CoverDelegate(self.list))
        self.list.setSpacing(6)
        self.list.setSelectionMode(QAbstractItemView.SingleSelection)
        self.list.setUniformItemSizes(True)
        self.list.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.list.itemActivated.connect(self._activate)
        self.list.itemClicked.connect(self._activate)

        self.status = QLabel("等待加载")
        self.status.setProperty("role", "hint")

        # 空态单独一块，居中盖在网格上。以前空文案借用下面那行分页状态，
        # 结果上面一大片空白、左下角孤零零一行小字。
        self.empty = QWidget(self.list)
        self.empty.setAttribute(Qt.WA_TransparentForMouseEvents, True)
        empty_layout = QVBoxLayout(self.empty)
        empty_layout.setAlignment(Qt.AlignCenter)
        empty_layout.setSpacing(8)
        self.empty_title = QLabel("")
        self.empty_title.setProperty("role", "emptyTitle")
        self.empty_title.setAlignment(Qt.AlignCenter)
        self.empty_hint = QLabel("")
        self.empty_hint.setProperty("role", "hint")
        self.empty_hint.setAlignment(Qt.AlignCenter)
        # 固定宽度：开了 wordWrap 又不给宽度的 QLabel 会缩到最小宽度，
        # 一句提示被折成两三行。给个最大阅读宽度，短句也就不折了。
        self.empty_hint.setWordWrap(True)
        self.empty_hint.setFixedWidth(360)
        empty_layout.addWidget(self.empty_title)
        empty_layout.addWidget(self.empty_hint)
        self.empty.hide()

        self.prev_button = QPushButton("上一页")
        self.prev_button.clicked.connect(self.prev_page)
        self.next_button = QPushButton("下一页")
        self.next_button.clicked.connect(self.next_page)
        self.refresh_button = QPushButton("换一批")
        self.refresh_button.clicked.connect(self.refreshRequested.emit)

        pager = QHBoxLayout()
        pager.addWidget(self.status, 1)
        pager.addWidget(self.prev_button)
        pager.addWidget(self.next_button)
        pager.addWidget(self.refresh_button)

        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.addWidget(self.list, 1)
        layout.addLayout(pager)

    def set_loading(self, text):
        """底部分页行左边那行字：进度 / 计数，不再兼任空态。"""
        self.status.setText(text)

    def set_empty(self, title, hint=""):
        """内容区居中的空态。set_items 拿到内容时会自动把它收起来。"""
        self.empty_title.setText(title)
        self.empty_hint.setText(hint)
        self.empty_hint.setVisible(bool(hint))
        self.empty.show()
        self._place_empty()

    def clear_empty(self):
        self.empty.hide()

    def reload_theme(self):
        """Regenerate placeholder tiles after the palette changed.

        The placeholder is a QPixmap painted with colours captured at
        creation time; repaint_all() only calls update() and cannot recolour
        an existing icon.
        """
        covers = self._covers.cached if self._covers is not None else None
        for identifier, (item, _drama) in self._items.items():
            pixmap = covers(identifier) if covers is not None else None
            item.setIcon(QIcon(pixmap if pixmap is not None else _placeholder_pixmap()))

    def _place_empty(self):
        self.empty.setGeometry(self.list.rect())
        self.empty.raise_()

    def resizeEvent(self, event):
        super().resizeEvent(event)
        self._place_empty()

    def set_interactive(self, enabled):
        """Block clicks while a request runs, without Qt's disabled look.

        A disabled QListWidget renders its icons in the disabled mode - every
        poster on screen drops to gray - so a running search turned the whole
        wall gray-white.  The wall now stays in full colour and only ignores
        activations until the answer arrives.
        """
        self._interactive = bool(enabled)

    def set_items(self, dramas, append=False, has_more=False):
        """Replace the stack with one page, or push another page on the end.

        ``append`` keeps the pages already loaded (that is 下一页); without
        it the stack restarts from a single page (首屏 / 换一批).
        """
        batch = [drama for drama in dramas if drama.get("id")]
        if not append:
            self._pages = []
            self._page = 0
        if batch:
            self.clear_empty()
        if not batch:
            if not append:
                # An empty answer must wipe the previous results; leaving
                # them on screen let the transparent empty-state layer pass
                # clicks through to dramas from the last search.
                self.list.clear()
                self._items.clear()
                self._pages = []
                self._page = 0
                self._sync_pager()
                self.set_loading("共 0 部")
                # 默认空态；调用方拿到结果后可以用自己的文案覆盖
                self.set_empty("暂无内容")
            return 0
        self._pages.append((batch, bool(has_more)))
        if append:
            self._page = len(self._pages) - 1
        added = self._show_page(self._page)
        return added

    def next_page(self):
        """Step forward: into an already fetched page, else ask for more."""
        if self._page + 1 < len(self._pages):
            self.goto_page(self._page + 1)
            return
        if self.has_more():
            self.moreRequested.emit()

    def prev_page(self):
        if self._page > 0:
            self.goto_page(self._page - 1)

    def goto_page(self, index):
        if 0 <= index < len(self._pages):
            self._show_page(index)

    def has_more(self):
        return bool(self._pages) and self._pages[self._page][1]

    def count(self):
        return len(self._items)

    def items(self):
        return [drama for _, drama in self._items.values()]

    def set_pages(self, pages):
        """Replace the whole page stack (used by 换一批)."""
        self._pages = list(pages or [])
        self._page = 0
        if self._pages:
            self._show_page(0)
        else:
            self.list.clear()
            self._items.clear()
            self._sync_pager()

    def append_pages(self, pages):
        """Add freshly fetched pages and land on the first of them.

        This is 下一页: the new batches go after everything already fetched,
        and the grid shows the first new one instead of jumping to the last
        (a fetch that returned more than one batch would otherwise skip
        straight past the page the user asked for).
        """
        pages = list(pages or [])
        if not pages:
            return
        first = len(self._pages)
        self._pages.extend(pages)
        self._show_page(first)

    def known_ids(self):
        """Ids of every drama on every loaded page.

        The recommendation endpoint answers with everything it has
        accumulated so far, so the window needs this to tell genuinely
        new dramas from ones already on screen.
        """
        return {drama.get("id")
                for batch, _ in self._pages
                for drama in batch
                if drama.get("id")}

    def set_has_more(self, has_more):
        """Update the newest page's flag (feed exhausted, load failed…)."""
        if not self._pages:
            return
        batch, _ = self._pages[self._page]
        self._pages[self._page] = (batch, bool(has_more))
        self._sync_pager()

    def set_pager_visible(self, visible):
        """Hide the paging bar for grids that only ever show one page."""
        for button in (self.prev_button, self.next_button, self.refresh_button):
            button.setVisible(bool(visible))

    def _show_page(self, index):
        """Render one page into the list, rebuilding it from scratch.

        Posters come from the loader's cache: this page was fetched once
        already, so going back must not blank every tile until the image
        is downloaded a second time.  Anything still missing is queued
        first and re-checked afterwards, so a load that finishes right
        away shows up immediately instead of staying gray.
        """
        batch = self._pages[index][0]
        if batch:
            # A page with content must remove any "暂无内容"/"加载失败"
            # overlay left from an earlier request; it used to cover the new
            # posters because only set_items() hid it.
            self.clear_empty()
        self._page = index
        self.list.clear()
        self._items.clear()
        covers = self._covers.cached if self._covers is not None else None

        # Pass 1: queue whatever is not cached yet.
        missing = []
        if covers is not None and self._cover_request:
            for drama in batch:
                identifier = drama.get("id")
                if identifier and not covers(identifier) and not self._covers.known(identifier):
                    missing.append(drama)
            if missing:
                self._cover_request(missing)

        # Pass 2: build the tiles, now reading whatever the cache holds.
        for drama in batch:
            identifier = drama.get("id")
            if not identifier or identifier in self._items:
                continue
            title = (drama.get("title") or "短剧").strip()
            meta = []
            if drama.get("episodes"):
                # 更新中的剧写「更新至」：和详情页一个口径，扫海报墙就能看出
                # 哪些还在更
                meta.append("更新至 %s 集" % drama["episodes"]
                            if (drama.get("releaseStatus") or "").strip() == "ongoing"
                            else "%s 集" % drama["episodes"])
            if drama.get("category"):
                meta.append(drama["category"])
            item = QListWidgetItem(_card_label(title, meta, self.list.fontMetrics()))
            pixmap = covers(identifier) if covers is not None else None
            item.setIcon(QIcon(pixmap if pixmap is not None else _placeholder_pixmap()))
            item.setToolTip(_poster_tooltip(drama))
            item.setTextAlignment(Qt.AlignHCenter | Qt.AlignTop)
            self.list.addItem(item)
            self._items[identifier] = (item, drama)
        self.list.scrollToTop()
        self._sync_pager()
        self._update_status()
        return len(self._items)

    def _update_status(self):
        """Describe what is on screen, and how much the feed holds.

        The counts must not be conflated: the grid shows one page at a
        time, so 本页 is the page and 共 is everything the feed has handed
        over so far - which is why the paged wording names each of them.
        """
        if not self._pages:
            self.set_loading("暂无内容")
            return
        if len(self._pages) == 1:
            self.set_loading("共 %d 部" % len(self._items))
            return
        self.set_loading("第 %d/%d 页 · 本页 %d 部 · 共 %d 部"
                        % (self._page + 1, len(self._pages), len(self._items),
                           len(self.known_ids())))

    def _sync_pager(self):
        self.prev_button.setEnabled(self._page > 0)
        self.next_button.setEnabled(self._page + 1 < len(self._pages) or self.has_more())

    def set_cover_loader(self, covers, request=None):
        """Attach the loader so a rebuilt page can reuse cached posters.

        ``request`` is called with the dramas a page still needs; it is
        where the window plugs in its own queueing.
        """
        self._covers = covers
        self._cover_request = request

    def drama_for_item(self, item):
        """The drama dict behind one list item (None when it is not ours)."""
        for entry in self._items.values():
            if entry[0] is item:
                return entry[1]
        return None

    def set_pixmap(self, drama_id, pixmap):
        entry = self._items.get(drama_id)
        if entry:
            entry[0].setIcon(QIcon(pixmap))

    def set_failed(self, drama_id):
        """Poster will not arrive: mark the tile instead of leaving it blank.

        Covers that give up (provider refuses, source unknown) would
        otherwise leave the loading card up forever.
        """
        entry = self._items.get(drama_id)
        if entry:
            entry[0].setIcon(QIcon(missing_tile()))

    def _activate(self, item):
        if not self._interactive:
            return              # 正在取新的一页/新的一次搜索，先别响应旧内容
        # itemClicked and itemActivated both fire; a double click used to
        # start two detail requests and the older answer could overwrite the
        # newer page.  Ignore the second activation of the same tile.
        now = time.monotonic()
        if item is self._last_activated and now - self._last_activated_at < 0.4:
            return
        for identifier, (candidate, drama) in self._items.items():
            if candidate is item:
                self._last_activated = item
                self._last_activated_at = now
                self.dramaActivated.emit(drama)
                return


class SummaryView(QTextBrowser):
    """详情页简介：右键菜单自己做一份中文的。

    QTextBrowser 的右键菜单文案来自 Qt 的 QWidgetTextControl 翻译，而 PyQt5
    自带的 qt_zh_CN.qm 里没有这一组（只有 Qt4 时代的老条目），所以右键一直是
    Copy / Select All。这里自己给中文菜单，顺便跟随应用主题。
    """

    def __init__(self, parent=None):
        super().__init__(parent)
        self.setOpenExternalLinks(True)

    def context_menu(self):
        """中文右键菜单（测试和 contextMenuEvent 共用一份）。"""
        menu = QMenu(self)
        copy = menu.addAction("复制")
        copy.setEnabled(self.textCursor().hasSelection())
        copy.triggered.connect(self.copy)
        copy_all = menu.addAction("复制全部简介")
        copy_all.triggered.connect(self._copy_all)
        select_all = menu.addAction("全选")
        select_all.triggered.connect(self.selectAll)
        return menu

    def _copy_all(self):
        QApplication.clipboard().setText(self.toPlainText())

    def contextMenuEvent(self, event):
        self.context_menu().exec_(event.globalPos())


# ---------------------------------------------------------- 选集：分集芯片
CHIP_SIZE = QSize(68, 46)            # 详情页（一屏铺开，勾选下载）
CHIP_COMPACT = QSize(48, 36)         # 播放页侧栏（窄，点一下就换集）
CHIP_GAP = 8                         # 芯片之间的呼吸
CHIP_GAP_COMPACT = 6                 # 侧栏窄，缝也收一点，正好一行四枚
CHIP_RADIUS = 8

ROLE_POSITION = Qt.UserRole          # 分集下标（沿用旧版的约定）
ROLE_DOWNLOADED = Qt.UserRole + 1    # 本地已有解密成品
ROLE_PROGRESS = Qt.UserRole + 2      # 上次看到的位置 0~1；-1 表示没看过
ROLE_FLASH = Qt.UserRole + 3         # 「跳到第 N 集」之后短暂高亮
ROLE_CURRENT = Qt.UserRole + 4       # 播放页：正在播的就是这一集

# 「第12集」「第 12 话」「EP12：」这类前缀，用于判断标题里有没有补充信息
_TITLE_PREFIX = re.compile(r"^(第\s*0*\d+\s*[集话]|ep?\s*0*\d+)\s*[：:·.\-、]?\s*", re.I)


class _EpisodeDelegate(QStyledItemDelegate):
    """把一集画成一枚芯片：数字居中，状态全在 paint 里。

    旧方案一集占一整行、一行只写一个三位数，1000 集的剧就是 1000 行滚动，
    还白白吃掉面板整条宽度。换成会换行的网格后一行十几集，像流媒体的选集条；
    绘制交给 delegate 而不是「一集一个控件」，所以再长的剧集也是瞬时布局。

    ``picker=False`` 是播放页用的模式：没有勾选这回事，点一下直接换到那一集。
    """

    def __init__(self, owner, parent=None, picker=True, chip=CHIP_SIZE, gap=CHIP_GAP):
        super().__init__(parent)
        self._owner = owner
        self._picker = picker
        self._chip = chip
        self._gap = gap
        self._anchor = None          # Shift 连选的起点
        self._press_state = None     # 这一次手势按下前的勾选状态
        self._skip_release = False

    def sizeHint(self, option, index):
        return QSize(self._chip.width() + self._gap, self._chip.height() + self._gap)

    def paint(self, painter, option, index):
        board = option.widget
        size = self._chip
        inset = self._gap // 2
        rect = QRect(option.rect.x() + inset, option.rect.y() + inset,
                     size.width(), size.height())
        checked = index.data(Qt.CheckStateRole) == Qt.Checked
        current = bool(index.data(ROLE_CURRENT))
        hovered = getattr(board, "_hover_row", -1) == index.row()
        progress = index.data(ROLE_PROGRESS)
        has_progress = isinstance(progress, (int, float)) and progress >= 0

        painter.save()
        painter.setRenderHint(QPainter.Antialiasing, True)

        if checked:
            fill = QColor(DESIGN["accent_hover"] if hovered else DESIGN["accent"])
            ink = QColor(DESIGN["on_accent"])
            border = fill
        elif current:
            # 正在播的这一集：淡蓝底 + 主色描边，和「勾选」区分开
            fill = QColor(DESIGN["accent_soft"])
            ink = QColor(DESIGN["accent"])
            border = QColor(DESIGN["accent"])
        else:
            fill = QColor(DESIGN["chip_hover"] if hovered else DESIGN["chip"])
            ink = QColor(DESIGN["text"])
            border = QColor(DESIGN["accent"] if hovered else DESIGN["border"])
        painter.setPen(QPen(border, 1))
        painter.setBrush(fill)
        painter.drawRoundedRect(rect.adjusted(0, 0, -1, -1), CHIP_RADIUS, CHIP_RADIUS)

        # 上次看到这一集：芯片底边一条进度条，一眼看出续播从哪开始
        if has_progress:
            ratio = min(1.0, max(0.0, float(progress)))
            painter.setPen(Qt.NoPen)
            painter.setBrush(QColor(DESIGN["on_accent"]) if checked else QColor(DESIGN["accent"]))
            painter.drawRoundedRect(
                QRectF(rect.left() + 8, rect.bottom() - 7, max(8.0, (rect.width() - 16) * ratio), 3),
                1.5, 1.5)

        font = QFont(option.font)
        font.setPixelSize(15 if size.width() >= 60 else 13)
        font.setBold(True)
        painter.setFont(font)
        painter.setPen(ink)
        painter.drawText(rect.adjusted(0, 0, 0, -3) if has_progress else rect,
                         Qt.AlignCenter, str(index.data(Qt.DisplayRole) or ""))

        if index.data(ROLE_DOWNLOADED):
            pen = QPen(QColor(DESIGN["on_accent"]) if checked else QColor(DESIGN["success"]), 2)
            pen.setCapStyle(Qt.RoundCap)
            pen.setJoinStyle(Qt.RoundJoin)
            painter.setPen(pen)
            x, y = rect.right() - 13.0, rect.top() + 10.0
            painter.drawLine(QPointF(x, y), QPointF(x + 2.5, y + 2.5))
            painter.drawLine(QPointF(x + 2.5, y + 2.5), QPointF(x + 7, y - 4))

        if index.data(ROLE_FLASH):
            painter.setPen(QPen(QColor(DESIGN["accent"]), 2))
            painter.setBrush(Qt.NoBrush)
            painter.drawRoundedRect(QRectF(rect.adjusted(1, 1, -2, -2)), CHIP_RADIUS, CHIP_RADIUS)

        painter.restore()

    def editorEvent(self, event, model, option, index):
        """单击＝勾选，Shift 单击＝连选，双击＝播放这一集。

        双击不能顺手改选集：把手势开始前的状态恢复回去就行。比起「再翻一次」，
        这样在两个平台差异下都对——有的平台会补发双击前那次 Release（已经翻过
        一次勾），有的只发 DblClick。
        """
        kind = event.type()
        if not self._picker:
            # 播放页：点一下就是「换到这一集播」，没有勾选这回事
            if kind == QEvent.MouseButtonRelease:
                self._owner.row_clicked(index.row())
                return True
            return False
        if kind == QEvent.MouseButtonPress:
            self._press_state = (index.row(), index.data(Qt.CheckStateRole))
            self._skip_release = False
            return False
        if kind == QEvent.MouseButtonRelease:
            if self._skip_release:
                self._skip_release = False
                return False
            if event.modifiers() & Qt.ShiftModifier and self._anchor is not None:
                self._select_range(model, self._anchor, index.row())
            else:
                self._flip(model, index)
                self._anchor = index.row()
            return True
        if kind == QEvent.MouseButtonDblClick:
            row, state = self._press_state or (None, None)
            if row == index.row() and state is not None:
                model.setData(index, state, Qt.CheckStateRole)
            self._skip_release = True
            return False        # 让 view 继续发 doubleClicked
        return False

    def _flip(self, model, index):
        state = Qt.Checked if index.data(Qt.CheckStateRole) != Qt.Checked else Qt.Unchecked
        model.setData(index, state, Qt.CheckStateRole)

    def _select_range(self, model, anchor, row):
        state = Qt.Checked if model.index(row, 0).data(Qt.CheckStateRole) != Qt.Checked else Qt.Unchecked
        low, high = sorted((anchor, row))
        self._owner.begin_bulk()
        try:
            for position in range(low, high + 1):
                model.setData(model.index(position, 0), state, Qt.CheckStateRole)
        finally:
            self._owner.end_bulk()


class EpisodeBoard(QListWidget):
    """芯片网格本体：悬停行自己记着，QSS 的 ::item 悬停在这个绘制路径上用不上。"""

    def __init__(self, chip=CHIP_SIZE, gap=CHIP_GAP, parent=None):
        super().__init__(parent)
        self.setProperty("role", "chips")       # 无边框透明底，见 build_stylesheet
        self.setViewMode(QListWidget.IconMode)
        self.setFlow(QListWidget.LeftToRight)
        self.setWrapping(True)
        self.setResizeMode(QListWidget.Adjust)
        self.setMovement(QListWidget.Static)
        self.setUniformItemSizes(True)
        self.setGridSize(QSize(chip.width() + gap, chip.height() + gap))
        self.setSpacing(0)
        self.setSelectionMode(QAbstractItemView.NoSelection)
        self.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.setVerticalScrollMode(QAbstractItemView.ScrollPerPixel)
        self.setEditTriggers(QAbstractItemView.NoEditTriggers)
        self.setFocusPolicy(Qt.NoFocus)
        self.setMouseTracking(True)
        self.viewport().setObjectName("episodeBoardViewport")
        self.viewport().setAutoFillBackground(False)
        self.setViewportMargins(2, 4, 2, 4)     # 别让首行贴着工具栏
        self._hover_row = -1

    def mouseMoveEvent(self, event):
        index = self.indexAt(event.pos())
        row = index.row() if index.isValid() else -1
        if row != self._hover_row:
            self._hover_row = row
            self.viewport().update()
        super().mouseMoveEvent(event)

    def leaveEvent(self, event):
        if self._hover_row != -1:
            self._hover_row = -1
            self.viewport().update()
        super().leaveEvent(event)


class ElidedLabel(QLabel):
    """窄下来就用省略号收尾，别把整条工具栏挤出面板。"""

    def __init__(self, text="", parent=None):
        super().__init__(text, parent)
        self.setMinimumWidth(48)

    def minimumSizeHint(self):
        hint = super().minimumSizeHint()
        return QSize(48, hint.height())

    def paintEvent(self, event):
        painter = QPainter(self)
        text = QFontMetrics(self.font()).elidedText(self.text(), Qt.ElideRight, self.width())
        painter.setPen(self.palette().color(self.foregroundRole()))
        painter.drawText(self.rect(), Qt.AlignLeft | Qt.AlignVCenter, text)


class _NumberChip(QWidget):
    """「选中前 [20] 集」这种小胶囊：点一下就生效，数字可以就地改。"""

    submitted = pyqtSignal(int)

    def __init__(self, prefix, value, suffix, parent=None):
        super().__init__(parent)
        self.setProperty("role", "chipfield")
        self.setAttribute(Qt.WA_StyledBackground, True)
        self.setCursor(Qt.PointingHandCursor)
        self.edit = QLineEdit(str(value))
        self.edit.setProperty("role", "tiny")
        self.edit.setFixedWidth(34)
        self.edit.setAlignment(Qt.AlignCenter)
        self.edit.returnPressed.connect(self._submit)
        row = QHBoxLayout(self)
        row.setContentsMargins(10, 1, 10, 1)
        row.setSpacing(2)
        row.addWidget(QLabel(prefix))
        row.addWidget(self.edit)
        row.addWidget(QLabel(suffix))

    def value(self, fallback=0):
        try:
            return int(self.edit.text().strip())
        except ValueError:
            return fallback

    def _submit(self):
        value = self.value()
        if value > 0:
            self.submitted.emit(value)

    def mousePressEvent(self, event):
        # 点在「选中前」/「集」这些空白处就当作确认；点数字则是改数字
        if event.button() == Qt.LeftButton:
            self._submit()
            return
        super().mousePressEvent(event)


class EpisodeList(QWidget):
    """选集面板：会换行的芯片网格 + 全选 / 区间快捷操作。

    两副面孔，同一套芯片：
    - 默认（详情页）：勾选多集来批量下载，单击勾选、Shift 连选、双击播放；
    - ``compact=True``（播放页侧栏）：窄一些、没有勾选，单击就直接换到那一集，
      另外把「正在播放」的那一格标出来。

    对外接口与旧的勾选列表一致（selectionChanged / playRequested /
    downloadRequested / selected_positions / chapters）。
    """

    selectionChanged = pyqtSignal(list)
    playRequested = pyqtSignal(int)
    downloadRequested = pyqtSignal(list)

    def __init__(self, parent=None, compact=False):
        super().__init__(parent)
        self._compact = compact
        self._chip = CHIP_COMPACT if compact else CHIP_SIZE
        self._gap = CHIP_GAP_COMPACT if compact else CHIP_GAP
        self._chapters = []
        self._by_number = {}
        self._downloaded = set()
        self._watched = (None, 0.0)
        self._current = None
        self._bulk = False
        self._flash_row = -1
        self._flash_timer = QTimer(self)
        self._flash_timer.setSingleShot(True)
        self._flash_timer.timeout.connect(self._clear_flash)

        self.list = EpisodeBoard(self._chip, self._gap, self)
        self._delegate = _EpisodeDelegate(self, self.list, picker=not compact,
                                          chip=self._chip, gap=self._gap)
        self.list.setItemDelegate(self._delegate)
        self.list.itemChanged.connect(self._changed)
        if not compact:
            self.list.itemDoubleClicked.connect(self._double_clicked)

        title = QLabel("选集")
        title.setProperty("role", "section")
        self.count_label = ElidedLabel("")
        self.count_label.setProperty("role", "hint")
        self.count_label.setToolTip("这部剧一共多少集、本地已经有多少集")

        self.jump_chip = _NumberChip("跳到第", "", "集")
        self.jump_chip.setToolTip("滚到指定的那一集（长剧集里比拖滚动条快）")
        self.jump_chip.submitted.connect(self._jump_to)

        head = QHBoxLayout()
        head.setSpacing(6)
        if compact:
            # 侧栏只有两百多像素：一行放「集数统计 + 跳转」就够
            head.addWidget(self.count_label)
            head.addStretch(1)
            head.addWidget(self.jump_chip)
        else:
            self.select_all = self._ghost("全选", lambda: self._set_all(True))
            self.select_none = self._ghost("清空", lambda: self._set_all(False))
            self.tail_chip = _NumberChip("选中前", 20, "集")
            self.tail_chip.setToolTip("选中第 1 集到这一集（数字可以改，回车或点文字生效）")
            self.tail_chip.submitted.connect(self._select_tail)
            head.addWidget(title)
            head.addWidget(self.count_label)
            head.addStretch(1)
            head.addWidget(self.select_all)
            head.addWidget(self.select_none)
            head.addWidget(self._divider())
            head.addWidget(self.tail_chip)
            head.addWidget(self.jump_chip)

        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(6)
        layout.addLayout(head)
        layout.addWidget(self.list, 1)
        if not compact:
            self.pill = QLabel("未选择分集")
            self.pill.setProperty("role", "pill")
            self.play_button = QPushButton("▶ 播放选中")
            self.play_button.setProperty("role", "accent")
            self.play_button.clicked.connect(lambda: self.playRequested.emit(self._first_selected()))
            self.download_button = QPushButton("↓ 下载选中")
            self.download_button.setProperty("role", "success")
            self.download_button.clicked.connect(self._emit_download)

            foot = QHBoxLayout()
            foot.setSpacing(8)
            foot.addWidget(self.pill)
            foot.addStretch(1)
            foot.addWidget(self.play_button)
            foot.addWidget(self.download_button)
            layout.addLayout(foot)
        self._sync_state()

    # -------------------------------------------------------------- 数据装配
    def set_chapters(self, chapters):
        self._chapters = list(chapters or [])
        self._by_number = {}
        self._current = None
        # Chunked population: a 1000-episode season used to freeze the GUI
        # for ~0.5s while 1000 items were created in one go.  A generation
        # token discards the remainder if the page changes mid-fill.
        self._populate_token = getattr(self, "_populate_token", 0) + 1
        token = self._populate_token
        board = self.list
        board.blockSignals(True)
        board.clear()
        board.blockSignals(False)
        self._delegate._anchor = None
        self._watched = (None, 0.0)
        self._refresh_marks()
        self._sync_state()
        self.selectionChanged.emit([])
        self._populate_chapters(0, token)

    def _populate_chapters(self, start, token):
        if token != getattr(self, "_populate_token", 0):
            return
        total = len(self._chapters)
        if start >= total:
            self._refresh_marks()
            return
        end = min(total, start + 200)
        board = self.list
        board.setUpdatesEnabled(False)
        board.blockSignals(True)
        try:
            for position in range(start, end):
                chapter = self._chapters[position]
                number = _episode_number(chapter, position)
                self._by_number.setdefault(number, position)
                item = QListWidgetItem(str(number))
                if self._compact:
                    # QListWidgetItem 默认就带 ItemIsUserCheckable，点一下会翻勾；
                    # 播放页靠单击换集，得显式摘掉
                    item.setFlags(item.flags() & ~Qt.ItemIsUserCheckable)
                else:
                    item.setFlags(item.flags() | Qt.ItemIsUserCheckable)
                    item.setCheckState(Qt.Unchecked)
                item.setData(ROLE_POSITION, position)
                item.setData(ROLE_DOWNLOADED, number in self._downloaded)
                item.setData(ROLE_PROGRESS, float(self._watched[1])
                             if position == self._watched[0] else -1.0)
                item.setData(ROLE_FLASH, False)
                item.setData(ROLE_CURRENT, position == self._current)
                item.setToolTip(self._tooltip(position))
                board.addItem(item)
        finally:
            board.blockSignals(False)
            board.setUpdatesEnabled(True)
        if end < total:
            QTimer.singleShot(0, lambda: self._populate_chapters(end, token))
        else:
            self._refresh_marks()

    def chapters(self):
        return self._chapters

    def set_current(self, position):
        """播放页：把「正在播放」的那一格标出来（-1/None 表示没有）。"""
        if position == self._current:
            return
        previous, self._current = self._current, position
        for row in (previous, position):
            if isinstance(row, int) and 0 <= row < self.list.count():
                self.list.item(row).setData(ROLE_CURRENT, row == position)
        if isinstance(position, int) and 0 <= position < self.list.count():
            # 自动续播一集一集往后走，正在播的那格得跟着滚进可视区。延到下一轮
            # 事件循环再滚：play_episode 里刚切页签，这会儿侧栏还没有真实尺寸，
            # 当场滚会被钳到底部去。
            QTimer.singleShot(0, lambda row=position: self._reveal(row))

    def _reveal(self, row):
        if not 0 <= row < self.list.count():
            return
        item = self.list.item(row)
        if not self.list.viewport().rect().contains(self.list.visualItemRect(item)):
            self.list.scrollToItem(item, QAbstractItemView.PositionAtCenter)

    def set_downloaded(self, numbers):
        """标记本地已有解密成品的分集（打开 / 切回详情页时对一次账）。"""
        numbers = set(numbers or ())
        if numbers == self._downloaded:
            return
        self._downloaded = numbers
        self._refresh_marks()

    def set_watched(self, position, progress=0.0):
        """记住看到哪一集：那一格底边画一条进度条。"""
        state = (position, round(float(progress or 0.0), 4))
        if state == self._watched:
            return
        self._watched = state
        self._refresh_marks()

    def _refresh_marks(self):
        position, progress = self._watched
        for row in range(self.list.count()):
            item = self.list.item(row)
            item.setData(ROLE_DOWNLOADED, self._number_at(row) in self._downloaded)
            item.setData(ROLE_PROGRESS, float(progress) if row == position else -1.0)
            item.setToolTip(self._tooltip(row))
        self._sync_state()
        self._sync_count()
        self.list.viewport().update()

    def _number_at(self, row):
        if 0 <= row < len(self._chapters):
            return _episode_number(self._chapters[row], row)
        return row + 1

    def _tooltip(self, row):
        number = self._number_at(row)
        extra = _extra_title((self._chapters[row] if row < len(self._chapters) else {}).get("title"),
                             number)
        lines = ["第 %d 集%s" % (number, " · %s" % extra if extra else "")]
        if number in self._downloaded:
            lines.append("已下载到本地")
        if row == self._watched[0]:
            lines.append("上次看到这里")
        lines.append("单击选择 · 双击播放 · Shift 单击连选")
        return "\n".join(lines)

    def _sync_count(self):
        total = self.list.count()
        if not total:
            self.count_label.setText("")
            return
        text = "共 %d 集" % total
        if self._downloaded and not self._compact:
            text += " · 已下载 %d 集" % len(self._downloaded)
        self.count_label.setText(text)

    def _sync_state(self, positions=None):
        if self._compact:
            return
        count = len(self.selected_positions() if positions is None else positions)
        empty = count == 0
        self.pill.setText("未选择分集" if empty else "已选 %d 集" % count)
        self._restyle(self.pill, "empty", empty)
        self.play_button.setEnabled(not empty)
        self.download_button.setEnabled(not empty)

    # ---------------------------------------------------------------- 勾选集
    def selected_positions(self):
        result = []
        for row in range(self.list.count()):
            item = self.list.item(row)
            if item.checkState() == Qt.Checked:
                result.append(item.data(ROLE_POSITION))
        return result

    def begin_bulk(self):
        """连选期间先别逐条重算（1000 行逐条 emit 就是 O(n²)）。"""
        self._bulk = True

    def end_bulk(self):
        self._bulk = False
        self._changed()

    def _changed(self):
        if self._bulk:
            return
        positions = self.selected_positions()
        self._sync_state(positions)
        self.selectionChanged.emit(positions)

    def _set_all(self, checked):
        state = Qt.Checked if checked else Qt.Unchecked
        board = self.list
        board.blockSignals(True)
        for row in range(board.count()):
            board.item(row).setCheckState(state)
        board.blockSignals(False)
        self._changed()

    def _select_tail(self, target):
        board = self.list
        board.blockSignals(True)
        for row in range(board.count()):
            item = board.item(row)
            item.setCheckState(Qt.Checked if self._number_at(row) <= target else Qt.Unchecked)
        board.blockSignals(False)
        self._changed()

    def _first_selected(self):
        selected = self.selected_positions()
        return selected[0] if selected else 0

    def _double_clicked(self, item):
        self.playRequested.emit(item.data(ROLE_POSITION))

    def row_clicked(self, row):
        """播放页单击一格：直接换到那一集播（芯片上只有数字，没有勾选框）。"""
        item = self.list.item(row)
        if item is not None:
            self.playRequested.emit(item.data(ROLE_POSITION))

    def _emit_download(self):
        selected = self.selected_positions()
        if selected:
            self.downloadRequested.emit(selected)

    # ------------------------------------------------------------ 跳到某一集
    def _jump_to(self, number):
        row = self._by_number.get(number)
        if row is None:
            return
        self.list.scrollToItem(self.list.item(row), QAbstractItemView.PositionAtCenter)
        self._flash(row)

    def _flash(self, row):
        if self._flash_row != row:
            self._set_flash(self._flash_row, False)
            self._flash_row = row
            self._set_flash(row, True)
        self._flash_timer.start(1200)

    def _clear_flash(self):
        self._set_flash(self._flash_row, False)
        self._flash_row = -1

    def _set_flash(self, row, value):
        item = self.list.item(row) if 0 <= row < self.list.count() else None
        if item is not None:
            item.setData(ROLE_FLASH, bool(value))

    # ------------------------------------------------------------------ 零件
    @staticmethod
    def _ghost(text, slot):
        button = QPushButton(text)
        button.setProperty("role", "ghost")
        button.setCursor(Qt.PointingHandCursor)
        button.clicked.connect(slot)
        return button

    @staticmethod
    def _divider():
        line = QFrame()
        line.setProperty("role", "divider")
        line.setFixedSize(1, 16)
        return line

    @staticmethod
    def _restyle(widget, prop, value):
        widget.setProperty(prop, value)
        widget.style().unpolish(widget)
        widget.style().polish(widget)


def _extra_title(title, number):
    """分集标题里除「第N集」之外的补充文字（没有就返回空串）。"""
    text = (title or "").strip()
    if not text:
        return ""
    stripped = _TITLE_PREFIX.sub("", text).strip()
    return "" if stripped.isdigit() else stripped


def _episode_number(chapter, fallback):
    value = chapter.get("currentEpisode")
    if isinstance(value, bool):
        value = None
    if isinstance(value, (int, float)):
        number = int(value)
    elif isinstance(value, str) and value.strip().isdigit():
        number = int(value.strip())
    else:
        number = 0
    return number if number > 0 else fallback + 1
