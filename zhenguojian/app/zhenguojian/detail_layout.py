"""Natural-height synopsis and a single scroll surface for drama details."""
from PyQt5.QtCore import QTimer, Qt, pyqtSignal
from PyQt5.QtGui import QTextOption
from PyQt5.QtWidgets import QSizePolicy
from .widgets import SummaryView


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
