"""Wide episode cards for the detail page, preserving the download picker."""
from PyQt5.QtCore import Qt,QRect,QRectF,QSize,QPointF,QEvent,QTimer
from PyQt5.QtGui import QColor,QFont,QPainter,QPen
from PyQt5.QtWidgets import QSizePolicy
from .ui import DESIGN
from .widgets import EpisodeList,_EpisodeDelegate,ROLE_CURRENT,ROLE_DOWNLOADED,ROLE_PROGRESS

class _DetailEpisodeDelegate(_EpisodeDelegate):
    def paint(self,painter,option,index):
        rect=QRect(option.rect.x()+self._gap//2,option.rect.y()+self._gap//2,self._chip.width(),self._chip.height())
        checked=index.data(Qt.CheckStateRole)==Qt.Checked
        hovered=getattr(option.widget,"_hover_row",-1)==index.row()
        current=bool(index.data(ROLE_CURRENT))
        background=DESIGN["accent"] if checked else DESIGN["accent_soft"] if current else DESIGN["surface3"] if hovered else DESIGN["surface"]
        ink=DESIGN["on_accent"] if checked else DESIGN["text"]
        painter.save();painter.setRenderHint(QPainter.Antialiasing)
        painter.setPen(QPen(QColor(DESIGN["accent"] if checked or hovered or current else DESIGN["border"]),1))
        painter.setBrush(QColor(background));painter.drawRoundedRect(rect.adjusted(0,0,-1,-1),6,6)
        font=QFont(option.font);font.setPixelSize(13);font.setBold(True);painter.setFont(font);painter.setPen(QColor(ink))
        number=str(index.data(Qt.DisplayRole) or "")
        painter.drawText(rect.adjusted(10,0,-rect.width()+38,0),Qt.AlignVCenter|Qt.AlignLeft,number)
        font.setBold(False);font.setPixelSize(12);painter.setFont(font)
        painter.setPen(QColor(ink if checked else DESIGN["muted"]))
        painter.drawText(rect.adjusted(38,0,-40,0),Qt.AlignVCenter|Qt.AlignLeft,"第%s集"%number)
        chapters=self._owner._chapters
        duration=(chapters[index.row()].get("duration") or 0) if index.row()<len(chapters) else 0
        try:seconds=max(0,int(float(duration)))
        except (TypeError,ValueError):seconds=0
        if seconds:painter.drawText(rect.adjusted(0,0,-9,0),Qt.AlignVCenter|Qt.AlignRight,"%02d:%02d"%divmod(seconds,60))
        progress=index.data(ROLE_PROGRESS)
        if isinstance(progress,(int,float)) and progress>=0:
            painter.setPen(Qt.NoPen);painter.setBrush(QColor(DESIGN["on_accent"] if checked else DESIGN["accent"]))
            painter.drawRoundedRect(QRectF(rect.x()+8,rect.bottom()-4,(rect.width()-16)*min(1,progress),2),1,1)
        if index.data(ROLE_DOWNLOADED):
            painter.setPen(QPen(QColor(DESIGN["on_accent"] if checked else DESIGN["success"]),2))
            x,y=rect.right()-10,rect.top()+7
            painter.drawLine(QPointF(x-4,y),QPointF(x-2,y+2));painter.drawLine(QPointF(x-2,y+2),QPointF(x+2,y-3))
        painter.restore()

class DetailEpisodeList(EpisodeList):
    def __init__(self, parent=None):
        super().__init__(parent)
        self._chip = QSize(166, 42)
        self._gap = 10
        self._scroll_area = None
        self._resizing = False
        self._viewport = self.list.viewport()
        self._resize_timer = QTimer(self)
        self._resize_timer.setSingleShot(True)
        self._resize_timer.timeout.connect(self._fit_grid)
        self.list.setVerticalScrollBarPolicy(Qt.ScrollBarAlwaysOff)
        self.list.setSizePolicy(QSizePolicy.Expanding, QSizePolicy.Fixed)
        self.list.setGridSize(QSize(176, 52))
        self._delegate = _DetailEpisodeDelegate(self, self.list, picker=True, chip=self._chip, gap=self._gap)
        self.list.setItemDelegate(self._delegate)
        self._viewport.installEventFilter(self)
        self.list.model().rowsInserted.connect(self._schedule_fit)
        self.list.model().modelReset.connect(self._schedule_fit)
        self.list.model().rowsRemoved.connect(self._schedule_fit)
        self.setSizePolicy(QSizePolicy.Expanding, QSizePolicy.Fixed)
        self.setMinimumWidth(0)
        self._schedule_fit()

    def set_scroll_area(self, area):
        self._scroll_area = area

    def _schedule_fit(self, *_args):
        self._resize_timer.start(0)

    def resizeEvent(self, event):
        super().resizeEvent(event)
        self._schedule_fit()

    def eventFilter(self, watched, event):
        if watched is self._viewport:
            if event.type() in (QEvent.Resize, QEvent.Show):
                self._schedule_fit()
            elif event.type() == QEvent.Wheel and self._scroll_area is not None:
                bar = self._scroll_area.verticalScrollBar()
                delta = event.pixelDelta().y() or event.angleDelta().y()
                bar.setValue(bar.value() - delta)
                return True
        return super().eventFilter(watched, event)

    def _fit_grid(self):
        if self._resizing:
            return
        self._resizing = True
        try:
            width = max(200, self._viewport.width() - 4)
            columns = max(1, width // 180)
            cell = max(100, width // columns - 2)
            size = QSize(cell - self._gap, 42)
            if size != self._chip:
                self._chip = size
                self._delegate._chip = size
                self.list.setGridSize(QSize(cell, 52))
            rows = (self.list.count() + columns - 1) // columns
            height = max(62, rows * 52 + 12)
            if self.list.height() != height:
                self.list.setFixedHeight(height)
            self.list.doItemsLayout()
            self.updateGeometry()
        finally:
            self._resizing = False

    def _jump_to(self, number):
        row = self._by_number.get(number)
        if row is None:
            return
        self._fit_grid()
        if self._scroll_area is not None:
            item = self.list.item(row)
            point = self._viewport.mapTo(self._scroll_area.widget(), self.list.visualItemRect(item).center())
            self._scroll_area.ensureVisible(point.x(), point.y(), 0, 80)
        self._flash(row)
