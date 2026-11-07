"""Cast portraits and a creator's paged filmography."""
from __future__ import annotations

import os
import re

from PyQt5.QtCore import Qt, QRect, QRectF, QSize, pyqtSignal
from PyQt5.QtGui import QColor, QFont, QFontMetrics, QPainter, QPainterPath, QPixmap
from PyQt5.QtWidgets import (QAbstractButton, QApplication, QComboBox, QHBoxLayout, QLabel,
                             QPushButton, QScrollArea, QVBoxLayout, QWidget)
from .ui import DESIGN
from .widgets_guo import PosterCard
from .scroll_loader import ScrollLoader
from .tasks import TaskRunner
from .widgets import CoverLoader
from .poster_tooltip import poster_tooltip
from .flow_layout import FlowLayout


def avatar_drama(creator):
    return {"id": "creator:" + str(creator.get("userId") or creator.get("id") or ""),
            "source": "hongguo", "title": creator.get("name") or "", "cover": creator.get("avatar") or ""}


class AvatarLoader(CoverLoader):
    def _load(self, drama):
        info = self._core.cover(drama)
        path = info.get("path")
        if not path or not os.path.isfile(path): return None
        if info.get("heic"): path = self._convert(path)
        if not path: return None
        pixmap = QPixmap(path)
        return pixmap if not pixmap.isNull() else None


def _heat_value(drama):
    text = str(drama.get("heat") or "")
    found = re.search(r"[0-9]+(?:\.[0-9]+)?", text)
    return float(found.group()) * (100000000 if "亿" in text else 10000 if "万" in text else 1) if found else 0


class RoundedPoster(QLabel):
    def paintEvent(self,event):
        painter=QPainter(self);painter.setRenderHint(QPainter.Antialiasing);painter.setRenderHint(QPainter.SmoothPixmapTransform)
        path=QPainterPath();path.addRoundedRect(QRectF(self.rect()),9,9);painter.setClipPath(path)
        painter.fillRect(self.rect(),QColor(DESIGN["cover_bg"]))
        pixmap=self.pixmap()
        if pixmap is not None and not pixmap.isNull():
            scaled=pixmap.scaled(self.size(),Qt.KeepAspectRatioByExpanding,Qt.SmoothTransformation)
            painter.drawPixmap(self.rect(),scaled,QRect((scaled.width()-self.width())//2,(scaled.height()-self.height())//2,self.width(),self.height()))


class CreatorCard(QAbstractButton):
    def __init__(self, creator, parent=None, large=False):
        super().__init__(parent)
        self.creator = creator
        self._pixmap = None
        self._image_failed = False
        self._large = large
        self.setFixedSize(84, 106) if not large else self.setFixedSize(96, 96)
        self.setCursor(Qt.PointingHandCursor)
        self.setToolTip(poster_tooltip({"title": creator.get("name"), "description": creator.get("description")}))
        self.setFocusPolicy(Qt.StrongFocus)
        self.setAccessibleName("%s %s，查看作品" % (creator.get("name") or "演职员", creator.get("role") or ""))
    def set_pixmap(self, pixmap):
        self._pixmap = pixmap
        self._image_failed = False
        self.update()
    def image_failed(self):
        self._image_failed = True
        self.setToolTip(poster_tooltip({"title": self.creator.get("name"), "description": "头像暂未加载成功，仍可点击查看作品。"}))
        self.update()
    def paintEvent(self, event):
        painter = QPainter(self)
        painter.setRenderHint(QPainter.Antialiasing)
        diameter = 88 if self._large else 60
        circle = QRectF((self.width() - diameter) / 2, 2, diameter, diameter)
        path = QPainterPath(); path.addEllipse(circle)
        painter.save(); painter.setClipPath(path); painter.fillPath(path, QColor(DESIGN["surface3"]))
        if self._pixmap is not None and not self._pixmap.isNull():
            image = self._pixmap.scaled(QSize(diameter, diameter), Qt.KeepAspectRatioByExpanding, Qt.SmoothTransformation)
            painter.drawPixmap(circle.toRect(), image, QRect((image.width()-diameter)//2, (image.height()-diameter)//2, diameter, diameter))
        else:
            painter.setPen(QColor(DESIGN["muted"])); font = QFont(self.font()); font.setPixelSize(24); painter.setFont(font)
            # A neutral silhouette is used only while the actual avatar is loading.
            painter.setPen(Qt.NoPen)
            painter.setBrush(QColor(DESIGN["muted2"]))
            center = circle.center()
            painter.drawEllipse(QRectF(center.x()-diameter*.12, circle.y()+diameter*.22, diameter*.24, diameter*.24))
            painter.drawRoundedRect(QRectF(center.x()-diameter*.25, circle.y()+diameter*.52, diameter*.5, diameter*.35), diameter*.2, diameter*.2)
        painter.restore()
        if self.underMouse() or self.hasFocus():
            painter.setPen(QColor(DESIGN["accent"])); painter.setBrush(Qt.NoBrush); painter.drawEllipse(circle)
        if self._large: return
        font = QFont(self.font()); font.setPixelSize(12); painter.setFont(font)
        painter.setPen(QColor(DESIGN["muted"] if not self.isEnabled() else DESIGN["text"]))
        painter.drawText(QRect(0, 67, self.width(), 20), Qt.AlignCenter,
                         QFontMetrics(font).elidedText(self.creator.get("name") or "演职员", Qt.ElideRight, self.width()-4))
        font.setPixelSize(11); painter.setFont(font); painter.setPen(QColor(DESIGN["muted"]))
        painter.drawText(QRect(0, 88, self.width(), 18), Qt.AlignCenter,
                         QFontMetrics(font).elidedText(self.creator.get("role") or "", Qt.ElideRight, self.width()-4))


class CastStrip(QWidget):
    creatorActivated = pyqtSignal(object)

    def __init__(self, covers, parent=None):
        super().__init__(parent)
        self._covers = covers
        self._cards = {}
        self._expanded = False
        heading = QLabel("演职员")
        heading.setProperty("role", "detailSection")
        self.more = QPushButton("")
        self.more.setProperty("role", "textLink")
        self.more.clicked.connect(self._toggle)
        head = QHBoxLayout()
        head.setContentsMargins(0, 0, 0, 0)
        head.addWidget(heading)
        head.addStretch(1)
        head.addWidget(self.more)
        self._holder = QWidget()
        self._row = FlowLayout(self._holder, spacing=16)
        self.status = QLabel("")
        self.status.setProperty("role", "hint")
        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(12)
        layout.addLayout(head)
        layout.addWidget(self.status)
        layout.addWidget(self._holder)
        self._covers.loaded.connect(self._on_cover)
        if hasattr(self._covers, "failed"):
            self._covers.failed.connect(self._on_failed)
        self.clear()

    def clear(self, loading=False):
        self._cards.clear()
        self._expanded = False
        while self._row.count():
            item = self._row.takeAt(0)
            if item.widget():
                item.widget().hide()
                item.widget().deleteLater()
        self.status.setText("正在加载演职员…" if loading else "")
        self.status.setVisible(loading)
        self._holder.hide()
        self.more.hide()
        self.setVisible(loading)

    def set_creators(self, creators):
        self.clear()
        for creator in creators:
            avatar = avatar_drama(creator)
            if avatar["id"] in self._cards:
                continue
            card = CreatorCard(creator)
            card.clicked.connect(lambda checked=False, value=creator: self.creatorActivated.emit(value))
            card.setEnabled(bool(creator.get("userId")))
            self._row.addWidget(card)
            self._cards[avatar["id"]] = card
            cached = self._covers.cached(avatar["id"])
            if cached is not None:
                card.set_pixmap(cached)
            elif avatar.get("cover"):
                self._covers.request(avatar)
        self._holder.setVisible(bool(self._cards))
        self.setVisible(bool(self._cards))
        self._sync_visibility()

    def _toggle(self):
        self._expanded = not self._expanded
        self._sync_visibility()

    def _sync_visibility(self):
        for index, card in enumerate(self._cards.values()):
            card.setVisible(self._expanded or index < 8)
        self.more.setVisible(len(self._cards) > 8)
        self.more.setText("收起演职员" if self._expanded else "查看全部 %d 人" % len(self._cards))
        self._row.invalidate()
        self.updateGeometry()

    def _on_cover(self, identifier, pixmap):
        card = self._cards.get(identifier)
        if card is not None:
            card.set_pixmap(pixmap)

    def _on_failed(self, identifier):
        card = self._cards.get(identifier)
        if card is not None:
            card.image_failed()


class _WorksGrid(QScrollArea):
    """作者作品墙：guo- PosterCard 网格（W1d 换代，替代 poster_wall.PosterWall）。

    对 CreatorPage 保留原墙的最小接口：set_items / append_items / count /
    set_pixmap(id,pixmap) / doItemsLayout（ScrollLoader 需要）/
    verticalScrollBar+viewport（QScrollArea 自带）；ranked=True 时按序挂
    热度榜角标（旧双徽标里的热度位）。
    """

    dramaActivated = pyqtSignal(object)

    def __init__(self, covers=None, request_cb=None, parent=None):
        super().__init__(parent)
        self._covers = covers
        self._request_cb = request_cb
        self._cards = {}
        self._titles = {}
        self._ids = []
        self._ranked = False
        self.setProperty("guoRole", "bare")
        self.setWidgetResizable(True)
        self.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self._host = QWidget()
        self._flow = FlowLayout(self._host, spacing=18)
        self.setWidget(self._host)

    def set_ranked(self, ranked):
        ranked = bool(ranked)
        if ranked != self._ranked:
            self._ranked = ranked
            self._restripe()

    def count(self):
        return len(self._ids)

    def doItemsLayout(self):
        self._flow.invalidate()

    def set_items(self, items):
        while self._flow.count():
            item = self._flow.takeAt(0)
            widget = item.widget()
            if widget is not None:
                widget.setParent(None)
                widget.deleteLater()
        self._cards = {}
        self._ids = []
        for drama in items:
            self._add(drama)
        self._restripe()

    def append_items(self, items):
        for drama in items:
            self._add(drama)
        self._restripe()

    def set_pixmap(self, identifier, pixmap):
        card = self._cards.get(identifier)
        if card is None:
            return
        index = self._ids.index(identifier) + 1
        card.set_data(self._titles.get(identifier, "短剧"), cover=pixmap,
                      badge_rank=index if self._ranked else None)

    def _add(self, drama):
        identifier = drama.get("id")
        if not identifier or identifier in self._cards:
            return
        card = PosterCard(self._host, cover_size=(110, 164))
        card.clicked.connect(lambda checked=False, value=drama: self.dramaActivated.emit(value))
        cached = self._covers.cached(identifier) if self._covers is not None else None
        title = (drama.get("title") or "短剧").strip()
        card.set_data(title, cover=cached)
        card.setToolTip(poster_tooltip(drama))
        if cached is None and self._request_cb is not None \
                and not str(identifier).startswith("local:"):
            self._request_cb([drama])
        self._cards[identifier] = card
        self._titles[identifier] = title
        self._ids.append(identifier)
        self._flow.addWidget(card)

    def _restripe(self):
        for index, identifier in enumerate(self._ids):
            card = self._cards.get(identifier)
            if card is not None:
                card.set_data(self._titles.get(identifier, "短剧"),
                              cover=self._covers.cached(identifier) if self._covers is not None else None,
                              badge_rank=index + 1 if self._ranked else None)

    def reload_theme(self):
        for card in self._cards.values():
            card.update()


class CreatorPage(QWidget):
    backRequested = pyqtSignal()
    dramaActivated = pyqtSignal(object)
    def __init__(self, core, avatars, parent=None):
        super().__init__(parent)
        self.setProperty("role", "creatorPage")
        self._core = core; self._avatars = avatars; self._covers = CoverLoader(core, self); self._runner = TaskRunner(self)
        self._token = 0; self._creator = {}; self._cursor = ""; self._has_more = False; self._loading = False; self._retry = False
        self._tabs = []; self._tab = 0; self._sub_tab = 0; self._stopped = False; self._profile_loaded = False; self._items = []
        back = QPushButton("← 返回剧集详情"); back.setProperty("role", "ghost"); back.clicked.connect(self.backRequested.emit)
        top = QHBoxLayout(); top.addWidget(back); top.addStretch(1)
        self.avatar_host = QHBoxLayout(); self.avatar_host.setContentsMargins(0, 0, 0, 0)
        self.avatar = None
        self.name = QLabel(); self.name.setProperty("role", "detailTitle")
        self.meta = QLabel(); self.meta.setProperty("role", "hint")
        self.bio = QLabel(); self.bio.setWordWrap(True); self.bio.setTextFormat(Qt.PlainText); self.bio.setProperty("role", "hint")
        self.stats = QLabel(); self.stats.setProperty("role", "creatorStats")
        self.copy_id = QPushButton("复制红果号"); self.copy_id.setProperty("role", "ghost"); self.copy_id.clicked.connect(self._copy_id)
        identity = QVBoxLayout(); identity.addWidget(self.name); identity.addWidget(self.meta); identity.addWidget(self.copy_id, 0, Qt.AlignLeft)
        header = QHBoxLayout(); header.addLayout(self.avatar_host); header.addLayout(identity, 1)
        self.tab_box = QComboBox(); self.tab_box.currentIndexChanged.connect(self._select_tab)
        self.sort_box = QComboBox(); self.sort_box.addItem("最新", "time_desc"); self.sort_box.addItem("最热", "hot_desc")
        self.sort_box.currentIndexChanged.connect(self._sort_changed)
        self.count = QLabel("作品"); self.count.setProperty("role", "section")
        self.status = QLabel(); self.status.setProperty("role", "hint")
        self.retry = QPushButton("重试加载"); self.retry.setProperty("role", "ghost"); self.retry.clicked.connect(self._retry_load)
        controls = QHBoxLayout(); controls.addWidget(self.count); controls.addWidget(self.tab_box); controls.addStretch(1); controls.addWidget(self.sort_box)
        self.wall = _WorksGrid(covers=self._covers, request_cb=self._request_covers)
        self.wall.dramaActivated.connect(self.dramaActivated.emit)
        self._scroll = ScrollLoader(self.wall, self._load_more)
        footer = QHBoxLayout(); footer.addWidget(self.status, 1); footer.addWidget(self.retry)
        layout = QVBoxLayout(self); layout.setContentsMargins(10, 10, 10, 10); layout.setSpacing(10)
        layout.addLayout(top); layout.addLayout(header); layout.addWidget(self.bio); layout.addWidget(self.stats); layout.addLayout(controls); layout.addWidget(self.wall, 1); layout.addLayout(footer)
        self._covers.loaded.connect(self.wall.set_pixmap); self._avatars.loaded.connect(self._avatar_loaded)
    def show_creator(self, creator):
        self._token += 1; token = self._token; self._creator = dict(creator); self._loading = True; self._cursor = ""; self._has_more = False
        self._profile_loaded = False; self._items = []; self._tab = 0; self._sub_tab = 0
        self.wall.set_items([]); self.retry.hide(); self.status.setText("正在加载作品…"); self.stats.clear()
        self.name.setText(creator.get("name") or "演职员"); self.meta.setText("%s · 红果号 %s" % (creator.get("role") or "演职员", creator.get("userId") or ""))
        self.bio.setText(creator.get("description") or "")
        if self.avatar: self.avatar_host.removeWidget(self.avatar); self.avatar.deleteLater()
        self.avatar = CreatorCard(creator, large=True); self.avatar_host.addWidget(self.avatar)
        data = avatar_drama(creator); pixmap = self._avatars.cached(data["id"])
        if pixmap is not None: self.avatar.set_pixmap(pixmap)
        elif data.get("cover"): self._avatars.request(data)
        self.tab_box.blockSignals(True); self.tab_box.clear(); self.tab_box.addItem("全部作品", (0,0)); self.tab_box.blockSignals(False)
        def done(result):
            if token != self._token or self._stopped: return
            result = result or {}; profile = result.get("profile") or {}
            if profile.get("name"): self.name.setText(profile["name"])
            if profile.get("description"): self.bio.setText(profile["description"])
            parts = ["%s %s" % (profile[key], label) for key,label in (("fans","粉丝"),("following","关注"),("likes","获赞")) if profile.get(key) is not None]
            self.stats.setText("    ".join(parts)); self.stats.setVisible(bool(parts))
            self._tabs = result.get("tabs") or []
            self.tab_box.blockSignals(True); self.tab_box.clear()
            for tab in self._tabs:
                for sub in tab.get("subTabs") or [{"id":0,"name":"全部"}]:
                    self.tab_box.addItem("%s %s" % (tab.get("name") or "作品",sub.get("name") or "全部"),(tab["id"],sub["id"]))
            self.tab_box.blockSignals(False)
            self._loading = False; self._profile_loaded = True
            if self.tab_box.count():
                self._select_tab()
            else:
                self._has_more = False
                self.count.setText("作品 0")
                self.status.setText("该演职员暂无公开作品")
                self.retry.hide()
        def failed(message):
            if token != self._token: return
            self._loading = False; self.status.setText("作品加载失败：%s" % message); self.retry.show()
        self._runner.run(lambda:self._core.creator_profile(creator),on_success=done,on_error=failed)
    def _retry_load(self):
        if not self._profile_loaded: self.show_creator(self._creator)
        else: self._load(reset=not self.wall.count())
    def _sort_changed(self):
        bar = self.wall.verticalScrollBar()
        position = bar.value()
        self._render_items()
        bar.setValue(position)
    def _render_items(self):
        hot = self.sort_box.currentData() == "hot_desc"
        items = sorted(self._items, key=_heat_value, reverse=True) if hot else self._items
        self.wall.set_ranked(hot)
        self.wall.set_items(items)
    def _copy_id(self): QApplication.clipboard().setText(str(self._creator.get("userId") or ""))
    def _avatar_loaded(self, identifier, pixmap):
        if self.avatar and identifier == avatar_drama(self._creator)["id"]: self.avatar.set_pixmap(pixmap)
    def _select_tab(self,*_):
        if not self._profile_loaded:
            return
        selected = self.tab_box.currentData()
        if selected:
            self._tab, self._sub_tab = map(int, selected)
            self._load(reset=True)
    def _load(self,reset=False):
        if self._stopped or not self._creator.get("userId"): return
        if self._loading and not reset: return
        self._token += 1; token = self._token; self._loading = True; self._retry = False; self.retry.hide()
        if reset: self._cursor = ""; self._items = []; self.wall.set_items([]); self._has_more = False
        creator,cursor,tab,sub,order = dict(self._creator),self._cursor,self._tab,self._sub_tab,"time_desc"
        self.status.setText("正在加载作品…")
        def done(result):
            if token != self._token or self._stopped: return
            result = result or {}; previous = self.wall.count()
            known = {drama["id"] for drama in self._items}
            fresh = [drama for drama in result.get("items") or [] if drama.get("id") and drama["id"] not in known]
            self._items.extend(fresh)
            if self.sort_box.currentData() == "hot_desc":
                position = self.wall.verticalScrollBar().value(); self._render_items(); self.wall.verticalScrollBar().setValue(position)
            else: self.wall.append_items(fresh)
            self._has_more = bool(result.get("hasMore")); self._cursor = str(result.get("cursor") or "")
            if self._has_more and (not self._cursor or self._cursor == cursor or previous==self.wall.count()): self._has_more = False
            self._loading = False; self.count.setText("作品 %d" % self.wall.count())
            self.status.setText("已加载 %d 部 · %s" % (self.wall.count(),"下滑继续浏览" if self._has_more else "已全部加载"))
            self._scroll.schedule()
        def failed(message):
            if token != self._token: return
            self._loading = False; self._retry = True; self.status.setText("加载失败：%s" % message); self.retry.show()
        self._runner.run(lambda:self._core.creator_works(creator,tab=tab,sub_tab=sub,cursor=cursor,sort=order),on_success=done,on_error=failed)
    def _load_more(self):
        if self._has_more and not self._loading and not self._retry: self._load()
    def _request_covers(self, dramas):
        for drama in dramas: self._covers.request(drama)
    def reload_theme(self):
        self.wall.reload_theme()
        if self.avatar:self.avatar.update()
    def shutdown(self):
        self._stopped = True; self._token += 1; self._runner.shutdown(800); self._covers.stop()
