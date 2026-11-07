"""A wrapping layout for tags and filter chips."""
from PyQt5.QtCore import QPoint, QRect, QSize, Qt
from PyQt5.QtWidgets import QLayout, QSizePolicy

class FlowLayout(QLayout):
    def __init__(self, parent=None, spacing=6):
        super().__init__(parent)
        self._items = []
        self.setContentsMargins(0, 0, 0, 0)
        self.setSpacing(spacing)

    def addItem(self, item): self._items.append(item)
    def count(self): return len(self._items)
    def itemAt(self, index): return self._items[index] if 0 <= index < len(self._items) else None
    def takeAt(self, index): return self._items.pop(index) if 0 <= index < len(self._items) else None
    def expandingDirections(self): return Qt.Orientations(Qt.Orientation(0))
    def hasHeightForWidth(self): return True
    def heightForWidth(self, width): return self._arrange(QRect(0, 0, width, 0), True)
    def setGeometry(self, rect):
        super().setGeometry(rect)
        self._arrange(rect, False)
    def sizeHint(self): return self.minimumSize()
    def minimumSize(self):
        result = QSize()
        for item in self._items: result = result.expandedTo(item.minimumSize())
        left, top, right, bottom = self.getContentsMargins()
        return result + QSize(left + right, top + bottom)
    def _arrange(self, rect, measure):
        left, top, right, bottom = self.getContentsMargins()
        area = rect.adjusted(left, top, -right, -bottom)
        x, y, line = area.x(), area.y(), 0
        for item in self._items:
            if item.widget() is not None and item.widget().isHidden(): continue
            size = item.sizeHint()
            if x > area.x() and x + size.width() > area.right() + 1:
                x, y, line = area.x(), y + line + self.spacing(), 0
            if not measure: item.setGeometry(QRect(QPoint(x, y), size))
            x += size.width() + self.spacing()
            line = max(line, size.height())
        return y + line - rect.y() + bottom
