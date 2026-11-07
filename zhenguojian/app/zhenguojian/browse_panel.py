# -*- coding: utf-8 -*-
"""Find dramas with server-provided multi-dimensional filters and continuous paging."""
from __future__ import annotations

from PyQt5.QtCore import Qt, QTimer, pyqtSignal
from PyQt5.QtWidgets import (QButtonGroup, QHBoxLayout, QLabel, QLineEdit, QPushButton,
                             QScrollArea, QSizePolicy, QVBoxLayout, QWidget)
from .flow_layout import FlowLayout
from .poster_wall import PosterWall
from .poster_metadata import PosterMetadataLoader
from .scroll_loader import ScrollLoader
from .tasks import TaskRunner
from .widgets import CoverLoader

FILTER_ORDER = ("genre", "category_dim_theme", "category_dim_role", "category_dim_epoch",
                "sort", "gender", "creation_status", "online_time")


class BrowsePanel(QWidget):
    openDrama = pyqtSignal(object)
    searchRequested = pyqtSignal(str)
    statusMessage = pyqtSignal(str)

    def __init__(self, core, parent=None):
        super().__init__(parent)
        self.setProperty("role", "browsePage")
        self._core = core
        self._runner = TaskRunner(self)
        self._covers = CoverLoader(core, self)
        self._metadata = PosterMetadataLoader(core, self)
        self._metadata.updated.connect(self._on_metadata)
        self._filters = {key: "" for key in FILTER_ORDER}
        self._groups = []
        self._button_groups = {}
        self._filter_cache = {}
        self._filter_token = 0
        self._token = 0
        self._loading = False
        self._filters_loading = False
        self._loaded = False
        self._offset = 0
        self._cursor = ""
        self._has_more = False
        self._retry_page = None
        self._filters_error = False
        self._empty_pages = 0
        self._collapsed = False
        self._build_ui()
        self._covers.loaded.connect(self.wall.set_pixmap)
        self._covers.failed.connect(self.wall.set_failed)
        QTimer.singleShot(0, self._first_load)

    def _build_ui(self):
        title = QLabel("找剧"); title.setProperty("role", "newPageTitle")
        self.query = QLineEdit(); self.query.setPlaceholderText("输入剧名 / 关键词"); self.query.setMaximumWidth(420)
        self.query.returnPressed.connect(self._search)
        search = QPushButton("搜索"); search.setProperty("role", "accent"); search.clicked.connect(self._search)
        top = QHBoxLayout(); top.addWidget(title); top.addStretch(1); top.addWidget(self.query, 1); top.addWidget(search)
        self.filter_summary = QLabel("全部剧集"); self.filter_summary.setProperty("role", "hint")
        self.filter_summary.setWordWrap(True)
        self.reset_button = QPushButton("重置"); self.reset_button.setProperty("role", "ghost"); self.reset_button.clicked.connect(self._reset_filters)
        self.toggle_button = QPushButton("收起筛选"); self.toggle_button.setProperty("role", "ghost"); self.toggle_button.clicked.connect(self._toggle_filters)
        filter_head = QHBoxLayout(); filter_head.addWidget(self.filter_summary, 1); filter_head.addWidget(self.reset_button); filter_head.addWidget(self.toggle_button)
        self.filter_holder = QWidget(); self.filter_holder.setProperty("role", "browseFilters")
        self.filter_layout = QVBoxLayout(self.filter_holder); self.filter_layout.setContentsMargins(0,0,0,0); self.filter_layout.setSpacing(4)
        self.filter_scroll = QScrollArea(); self.filter_scroll.setProperty("role", "filterScroll")
        self.filter_scroll.setWidgetResizable(True); self.filter_scroll.setFrameShape(QScrollArea.NoFrame)
        self.filter_scroll.setWidget(self.filter_holder); self.filter_scroll.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.filter_scroll.setMinimumHeight(80)
        self.wall = PosterWall(covers=self._covers); self.wall.setProperty("role", "newPosterWall")
        self.wall.viewport().setObjectName("newPosterViewport"); self.wall.set_dual_badges(True)
        self.wall.set_cover_loader(self._covers,self._request_covers); self.wall.dramaActivated.connect(self.openDrama.emit)
        self._scroll_loader = ScrollLoader(self.wall,self._load_more,self._request_metadata)
        self.status = QLabel("等待加载"); self.status.setProperty("role", "hint")
        self.retry_button = QPushButton("重试加载"); self.retry_button.setProperty("role","ghost"); self.retry_button.clicked.connect(self._retry); self.retry_button.hide()
        self.reload_button = QPushButton("刷新"); self.reload_button.setProperty("role","ghost"); self.reload_button.clicked.connect(self._refresh)
        foot = QHBoxLayout(); foot.addWidget(self.status,1); foot.addWidget(self.retry_button); foot.addWidget(self.reload_button)
        layout = QVBoxLayout(self); layout.setContentsMargins(14,12,14,12); layout.setSpacing(8)
        layout.addLayout(top); layout.addLayout(filter_head); layout.addWidget(self.filter_scroll); layout.addWidget(self.wall,1); layout.addLayout(foot)

    def _first_load(self):
        if self.isVisible() and not self._loaded and not self._filters_loading:
            self._load_filters()
    def showEvent(self,event):
        super().showEvent(event); QTimer.singleShot(0,self._first_load)
    def resizeEvent(self,event):
        super().resizeEvent(event); self._size_filters()
    def _size_filters(self):
        if self._collapsed: return
        width=max(300,self.filter_scroll.viewport().width())
        required=self.filter_layout.heightForWidth(width)
        if required<=0: required=self.filter_holder.sizeHint().height()
        self.filter_scroll.setFixedHeight(min(max(80,required+4),max(160,int(self.height()*0.46))))
    def _search(self):
        query=self.query.text().strip()
        if query:self.searchRequested.emit(query)
    def _toggle_filters(self):
        self._collapsed=not self._collapsed
        self.filter_scroll.setVisible(not self._collapsed)
        self.toggle_button.setText("展开筛选" if self._collapsed else "收起筛选")
        if not self._collapsed:self._size_filters()
    def _reset_filters(self):
        self._filters={key:"" for key in FILTER_ORDER};self._load_filters()
    def _load_filters(self):
        genre=self._filters["genre"]
        self._filter_token+=1;token=self._filter_token
        self._token+=1;self._loading=False;self._has_more=False;self._retry_page=None;self._filters_loading=True
        self.retry_button.hide();self.status.setText("正在加载筛选条件…")
        def done(result):
            if token!=self._filter_token:return
            self._filters_loading=False;self._filters_error=False;self._loaded=True
            self._groups=[g for g in (result or {}).get("groups",[]) if g.get("key") in FILTER_ORDER]
            self._filter_cache[genre]=self._groups
            for group in self._groups:
                key=group["key"]
                if self._filters.get(key) and not any(str(o.get("id"))==self._filters[key] for o in group.get("options") or []):self._filters[key]=""
            self._build_filters();self._load(reset=True)
        def failed(message):
            if token!=self._filter_token:return
            self._filters_loading=False;self._filters_error=True;self.status.setText("筛选条件加载失败：%s"%message);self.retry_button.show()
        if genre in self._filter_cache:done({"groups":self._filter_cache[genre]});return
        self._runner.run(lambda:self._core.browse_filters(genre),on_success=done,on_error=failed)
    def _build_filters(self):
        while self.filter_layout.count():
            item=self.filter_layout.takeAt(0)
            if item.widget():item.widget().hide();item.widget().deleteLater()
        for group in self._button_groups.values(): group.deleteLater()
        self._button_groups={}
        for group in sorted(self._groups,key=lambda row:FILTER_ORDER.index(row["key"])):
            key=group["key"];row=QWidget();row_layout=QHBoxLayout(row);row_layout.setContentsMargins(0,0,0,0);row_layout.setSpacing(10)
            label=QLabel(group.get("name") or key);label.setProperty("role","filterLabel");label.setFixedWidth(48);row_layout.addWidget(label,0,Qt.AlignTop)
            holder=QWidget();flow=FlowLayout(holder,spacing=4);holder.setSizePolicy(QSizePolicy.Expanding,QSizePolicy.Preferred)
            buttons=QButtonGroup(self);buttons.setExclusive(True);self._button_groups[key]=buttons
            for option in [{"id":"","name":"全部"}]+list(group.get("options") or []):
                value=str(option.get("id") or "");button=QPushButton(option.get("name") or value)
                button.setProperty("role","filterChip");button.setCheckable(True);button.setChecked(self._filters.get(key,"")==value)
                button.setCursor(Qt.PointingHandCursor);button.clicked.connect(lambda checked=False,k=key,v=value:self._select_filter(k,v))
                buttons.addButton(button);flow.addWidget(button)
            row_layout.addWidget(holder,1);self.filter_layout.addWidget(row)
        self._sync_summary();QTimer.singleShot(0,self._size_filters)
    def _select_filter(self,key,value):
        if self._filters.get(key)==value:return
        self._filters[key]=value
        if key=="genre":self._load_filters()
        else:self._sync_summary();self._load(reset=True)
    def _sync_summary(self):
        labels=[]
        for group in self._groups:
            value=self._filters.get(group["key"],"")
            if value:
                label=next((o.get("name") for o in group.get("options") or [] if str(o.get("id"))==value),value);labels.append(str(label))
        self.filter_summary.setText("已选："+" · ".join(labels) if labels else "全部剧集")
        self.reset_button.setEnabled(bool(labels))
    def _load(self,reset=False,force=False):
        if self._filters_loading or (self._loading and not reset):return
        self._token+=1;token=self._token;self._loading=True;self._retry_page=None;self.retry_button.hide()
        if reset:
            self._offset=0;self._cursor="";self._has_more=False;self._empty_pages=0;self.wall.set_items([]);self._metadata.reset()
        filters,offset,cursor=dict(self._filters),self._offset,self._cursor
        self.status.setText("正在加载更多…" if self.wall.count() else "正在加载剧集…")
        def done(result):
            if token!=self._token:return
            result=result or {};before=self.wall.count();self.wall.append_items(result.get("items") or []);added=self.wall.count()-before
            next_offset=int(result.get("nextOffset") or 0);self._has_more=bool(result.get("hasMore")) and next_offset>offset
            self._offset=next_offset;self._cursor=str(result.get("sessionId") or "");self._loading=False
            self._empty_pages=self._empty_pages+1 if not added else 0
            if self._empty_pages>=2:self._has_more=False
            self.status.setText("已加载 %d 部 · %s"%(self.wall.count(),"下滑继续浏览" if self._has_more else "已全部加载"))
            self._scroll_loader.schedule()
        def failed(message):
            if token!=self._token:return
            self._loading=False;self._retry_page=(filters,offset,cursor)
            self.status.setText("加载失败：%s"%message);self.retry_button.show();self.statusMessage.emit("找剧加载失败：%s"%message)
        self._runner.run(lambda:self._core.browse_filtered(filters,offset=offset,cursor=cursor),on_success=done,on_error=failed)
    def _load_more(self):
        if self._has_more and not self._loading and self._retry_page is None:self._load()
    def _retry(self):
        if self._filters_error:self._load_filters()
        elif self._retry_page is not None:self._load()
    def _refresh(self):
        if not self._loaded:self._load_filters()
        else:self._load(reset=True)
    def _request_covers(self,dramas):
        for drama in dramas:self._covers.request(drama)
    def _request_metadata(self):self._metadata.request(self.wall.visible_dramas())
    def _on_metadata(self,drama):
        if drama:self.wall.update_drama(drama)
    def shutdown(self):
        self._token+=1;self._filter_token+=1;self._has_more=False;self._metadata.stop();self._runner.shutdown(800);self._covers.stop()
    def reload_theme(self):self.wall.reload_theme()
