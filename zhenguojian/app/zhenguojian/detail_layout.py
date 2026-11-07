"""Natural-height synopsis and a single scroll surface for drama details.

E1a（详情页头部翻新，呈现层）：DetailSummary 在首次 showEvent 时找到宿主
Poster，把 BlurCoverBackground（F2 widgets_guo）垫到头部整幅底下，封面小图
+ 标题 + meta 行照常叠在上面；标签 chip、次要按钮统一换代到 guo- 样式。
全部通过 setProperty("guoRole", …) + repolish 走 ui.py 的 QSS（组件内不硬编码
颜色），主题切换由全局样式表重建自然生效；main.py 的装配、数据流与信号一律
不动，找不到宿主结构就安静退化成原样。
"""
from PyQt5.QtCore import QTimer, Qt, QEvent, QObject, pyqtSignal
from PyQt5.QtGui import QTextOption
from PyQt5.QtWidgets import QSizePolicy, QLabel, QPushButton
from .widgets import SummaryView
from .widgets_guo import BlurCoverBackground

# guoapp 头部封面小图：2:3 比例（source-map §④ 海报比例），比旧版 198×264 收敛一档
COVER_THUMB = (150, 225)
# 换代到 guo- 次级样式的按钮属性名（main.py Poster 上的成员名，逐个 getattr 防御）
SECONDARY_BUTTONS = ("season_button", "follow_button", "reserve_button",
                     "save_cover", "copy_summary")


def _repolish(widget):
    """动态属性变了要让 QSS 重新匹配，只能 unpolish/polish 一次。"""
    try:
        style = widget.style()
        style.unpolish(widget)
        style.polish(widget)
    except RuntimeError:
        pass  # C++ 对象已销毁（切剧重建期间完全可能）


class GuoHeaderDresser(QObject):
    """把 main.py 的 Poster 头部换到 guoapp 版式：模糊封面底 + 小图 + chip 化。

    只读地借用 Poster 的既有成员（cover/title/meta/tag_holder/按钮），不改变
    任何信号接线和数据流；每一项 dressing 都是 setProperty/geometry 级别的操作，
    任何一步抛异常都静默跳过，详情页永远能用。
    必须是 QObject：installEventFilter 只接受 QObject 过滤器（PyQt5 强制）。
    """

    def __init__(self, summary):
        super().__init__(summary)   # 挂到 summary 名下随宿主一起销毁
        self._summary = summary
        self._poster = None
        self._cover = None
        self._tag_holder = None
        self._blur = None
        self._cover_key = None

    # ------------------------------------------------------------------ 装配
    def attach(self):
        try:
            self._attach()
        except Exception:
            # 呈现层永远不能因为 dressing 失败拖垮详情页
            pass

    def _attach(self):
        poster = self._find_poster()
        if poster is None:
            return
        self._poster = poster
        self._cover = getattr(poster, "cover", None)
        self._tag_holder = getattr(poster, "tag_holder", None)

        # 1) 模糊封面铺底：铺满 Poster、压到兄弟控件之下，几何由我们跟 Resize 同步
        self._blur = BlurCoverBackground(poster)
        self._blur.setGeometry(poster.rect())
        self._blur.lower()
        self._blur.show()   # attach 发生在 Poster 已显示之后：新建子控件默认隐藏
        poster.installEventFilter(self)

        # 2) 封面收成小图（2:3），RoundedPoster 自己按尺寸缩放绘制
        if self._cover is not None:
            self._cover.setFixedSize(*COVER_THUMB)
            self._cover.installEventFilter(self)
            self._sync_cover()

        # 3) 标签 chip 化；set_tags 重建时靠 ChildInserted 继续生效
        if self._tag_holder is not None:
            self._tag_holder.installEventFilter(self)
            self._retag_tags()

        # 4) 次要按钮换 guo- 样式；play_first（accent）与 download_all（success）
        #    本来就吃 DESIGN token，保留语义色（见任务 Notes 决策）。
        for name in SECONDARY_BUTTONS:
            button = getattr(poster, name, None)
            if isinstance(button, QPushButton) and button.property("guoRole") is None:
                button.setProperty("guoRole", "ctrlBtn")
                _repolish(button)

    def _find_poster(self):
        widget = self._summary.parentWidget()
        while widget is not None:
            if getattr(widget, "summary", None) is self._summary and \
               getattr(widget, "cover", None) is not None:
                return widget
            widget = widget.parentWidget()
        return None

    # ------------------------------------------------------------------ 刷新
    def _sync_cover(self):
        """从封面 QLabel 拿当前 pixmap 喂给模糊底；cacheKey 变了才更新。"""
        if self._blur is None or self._cover is None:
            return
        try:
            pixmap = self._cover.pixmap()
        except (AttributeError, RuntimeError):
            return
        if pixmap is None or pixmap.isNull():
            if self._cover_key is not None:
                self._cover_key = None
                self._blur.set_cover(None)
            return
        key = pixmap.cacheKey()
        if key == self._cover_key:
            return
        self._cover_key = key
        self._blur.set_cover(pixmap)

    def _retag_tags(self):
        if self._tag_holder is None:
            return
        try:
            labels = self._tag_holder.findChildren(QLabel)
        except RuntimeError:
            return
        for label in labels:
            if label.property("role") == "detailTag" and label.property("guoRole") is None:
                label.setProperty("guoRole", "chip")
                _repolish(label)

    # ------------------------------------------------------------------ 事件
    def eventFilter(self, watched, event):
        etype = event.type()
        if watched is self._poster:
            if etype == QEvent.Resize and self._blur is not None:
                self._blur.setGeometry(self._poster.rect())
            elif etype == QEvent.StyleChange and self._blur is not None:
                # 主题切换：BlurCoverBackground 的遮罩色读的是 DESIGN token，
                # 强制重绘一次让它拿到新调色板
                self._blur.update()
        elif watched is self._cover:
            if etype in (QEvent.Paint, QEvent.Show):
                self._sync_cover()
        elif watched is self._tag_holder:
            # set_tags 重建的标签是"先构造后 reparent 进 holder"，PyQt5 没有
            # ChildInserted，用 ChildAdded 接同样一次
            if etype in (QEvent.ChildAdded, QEvent.Show):
                self._retag_tags()
        return False


class DetailSummary(SummaryView):
    overflowChanged = pyqtSignal(bool)

    def __init__(self, parent=None):
        super().__init__(parent)
        self._expanded = False
        self._overflow = False
        self._measure_timer = QTimer(self)
        self._measure_timer.setSingleShot(True)
        self._measure_timer.timeout.connect(self._measure)
        self.setHorizontalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.setVerticalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.setSizePolicy(QSizePolicy.Expanding, QSizePolicy.Fixed)
        self.setWordWrapMode(QTextOption.WrapAtWordBoundaryOrAnywhere)
        self.document().setDocumentMargin(0)
        self._dresser = None      # 首次 show 时挂上的头部 dressing（保持引用防 GC）

    def setPlainText(self, text):
        self._expanded = False
        super().setPlainText(text)
        self._measure_timer.start(0)

    def set_expanded(self, expanded):
        self._expanded = bool(expanded)
        self._measure()

    def resizeEvent(self, event):
        super().resizeEvent(event)
        self._measure_timer.start(0)

    def showEvent(self, event):
        super().showEvent(event)
        if self._dresser is None:
            # 只做一次；此时 Poster 的兄弟控件都已就位
            self._dresser = GuoHeaderDresser(self)
            self._dresser.attach()

    def _measure(self):
        document = self.document()
        document.setTextWidth(max(80, self.viewport().width()))
        full = max(self.fontMetrics().height(), int(document.size().height()) + 4)
        preview = self.fontMetrics().lineSpacing() * 3 + 4
        overflow = full > preview
        if overflow != self._overflow:
            self._overflow = overflow
            self.overflowChanged.emit(overflow)
        height = full if self._expanded else min(full, preview)
        if self.height() != height:
            self.setFixedHeight(height)
