"""guoapp 选集区 —— 渲染层换成 F2 EpisodeGrid，数据流与信号原样保留。

兼容面（main.py 的既有调用点，签名全部不变，见任务对照表）：
    signals: playRequested(int position) / downloadRequested(list positions)
    methods: set_chapters / set_downloaded / set_watched / set_scroll_area / chapters
    attrs:   .list（兼容垫片：main.py:287 仍在调 episodes.list.viewport().update()）

版式：头部行（「选集」+ 计数 + 批量开关 + 跳集 chip）+ EpisodeGrid（30 集一组、
当前集 accent 高亮、已看置浅、VIP 黄点）+ 批量模式底栏（全选/清空/选中前 N/
播放选中/下载选中，默认隐藏）。点击语义换到 guoapp 式：默认单击直接播；进入
「批量下载」模式后单击变勾选——勾选态复用 episode 按钮的 current 动态属性表达
accent 实底（QSS 驱动、主题安全），不碰 widgets_guo 一个字节。

已看判定（数据层假设，见任务 Notes）：观看记录只存「上次看到第几集 + 进度」，
这里把序号早于该集的集视为已看（置浅），该集本身作为「当前集」accent 高亮并
由 scroll_to_current 落位。

预缓冲（E1c）：set_chapters 换剧后把前 PRELOAD_FIRST_N 集经宿主（MainWindow）
现成的 preload 通道预热——core.preload 在后台线程解析并把首段拉进会话内存，
结果按 MainWindow._preload_key 的格式注入它的 _preloads 池，用户点播时
_take_preload 直接命中；旧剧的注入会话在切剧时经宿主的 _release_preload 归还。
找不到宿主（预览/测试环境）、接口失败、结果不可用：全部静默，绝不打扰页面。
"""
import threading
import time

from PyQt5.QtCore import QEvent, QTimer, pyqtSignal
from PyQt5.QtWidgets import (QHBoxLayout, QLabel, QPushButton, QScrollArea,
                             QSizePolicy, QVBoxLayout, QWidget)

from .widgets import _episode_number, _extra_title, _NumberChip
from .widgets_guo import EpisodeGrid

# 与 MainWindow.PRELOAD_LIMIT=2 对齐：进详情页只预热前 2 集，池语义不变
PRELOAD_FIRST_N = 2

# FlowLayout 估算参数（与 widgets_guo._rebuild_grid 保持一致：spacing 6、
# 左右 margin 各 6；chip 条高度/宽度是保守估值，宁可多出几像素也不要内滚动条）
_FLOW_SPACING = 6
_FLOW_MARGIN = 12
_CHIP_STRIDE = 64
_CHIP_ROW_H = 32


def _repolish(widget):
    try:
        style = widget.style()
        style.unpolish(widget)
        style.polish(widget)
    except RuntimeError:
        pass


class _CompatViewport(object):
    """垫片：main.py:287 只用到 viewport().update()，重定向到网格按钮重绘。"""

    __slots__ = ("_owner",)

    def __init__(self, owner):
        self._owner = owner

    def update(self, *args, **kwargs):
        self._owner._repaint_buttons()


class _CompatList(object):
    __slots__ = ("_viewport",)

    def __init__(self, owner):
        self._viewport = _CompatViewport(owner)

    def viewport(self):
        return self._viewport


class DetailEpisodeList(QWidget):
    playRequested = pyqtSignal(int)
    downloadRequested = pyqtSignal(list)
    _preloadDone = pyqtSignal(object)      # 后台线程 → GUI 线程桥

    def __init__(self, parent=None):
        super().__init__(parent)
        self._chapters = []
        self._drama_vip = False            # 剧目级 VIP 标志（红果逐集不带 vip 字段）
        self._by_number = {}               # 集数 -> position(0 基)
        self._episodes = []                # EpisodeGrid payload
        self._downloaded = set()           # 集数集合
        self._watch_position = None        # 上次看到的 position
        self._watch_progress = 0.0
        self._marks = set()                # 批量模式勾选的集数
        self._batch = False
        self._scroll_area = None
        self._warm_token = 0
        self._warm_keys = set()
        self._injected = set()
        self._inner_viewports = set()

        outer = QVBoxLayout(self)
        outer.setContentsMargins(0, 0, 0, 0)
        outer.setSpacing(10)

        head = QHBoxLayout()
        head.setContentsMargins(0, 0, 0, 0)
        head.setSpacing(8)
        title = QLabel("选集")
        title.setProperty("guoRole", "sectionHeader")
        self.count_label = QLabel("")
        self.count_label.setProperty("role", "hint")
        self.count_label.setToolTip("这部剧一共多少集、本地已经有多少集")
        self.batch_toggle = QPushButton("批量下载")
        self.batch_toggle.setProperty("guoRole", "groupTab")
        self.batch_toggle.setCheckable(True)
        self.batch_toggle.setToolTip("进入后单击分集变成勾选，用底栏批量下载/播放选中")
        self.batch_toggle.toggled.connect(self._set_batch)
        self.jump_chip = _NumberChip("跳到第", "", "集")
        self.jump_chip.setToolTip("切到那一集所在分组并短暂高亮（长剧集比滚动快）")
        self.jump_chip.submitted.connect(self._jump_to)
        head.addWidget(title)
        head.addWidget(self.count_label)
        head.addStretch(1)
        head.addWidget(self.batch_toggle)
        head.addWidget(self.jump_chip)
        outer.addLayout(head)

        self.grid = EpisodeGrid(self)
        self.grid.episode_clicked.connect(self._on_episode_clicked)
        self.grid.installEventFilter(self)   # Resize/LayoutRequest → 高度拟合
        outer.addWidget(self.grid, 1)

        self._build_foot(outer)

        self.list = _CompatList(self)        # main.py:287 兼容垫片
        self.setSizePolicy(QSizePolicy.Expanding, QSizePolicy.Fixed)
        self.setMinimumWidth(0)
        self._fit_timer = QTimer(self)
        self._fit_timer.setSingleShot(True)
        self._fit_timer.timeout.connect(self._fit_grid_height)
        self._preloadDone.connect(self._on_preload_done)
        self._hook_wheel_forwarding()

    def _build_foot(self, outer):
        foot_host = QWidget(self)
        foot = QHBoxLayout(foot_host)
        foot.setContentsMargins(0, 0, 0, 0)
        foot.setSpacing(8)
        self.pill = QLabel("未选择分集")
        self.pill.setProperty("role", "pill")
        self.select_all = QPushButton("全选")
        self.select_all.setProperty("guoRole", "ctrlBtn")
        self.select_all.clicked.connect(lambda: self._set_all(True))
        self.select_none = QPushButton("清空")
        self.select_none.setProperty("guoRole", "ctrlBtn")
        self.select_none.clicked.connect(lambda: self._set_all(False))
        self.tail_chip = _NumberChip("选中前", 20, "集")
        self.tail_chip.setToolTip("勾选第 1 集到这一集（数字可以改，回车或点文字生效）")
        self.tail_chip.submitted.connect(self._select_tail)
        self.play_button = QPushButton("▶ 播放选中")
        self.play_button.setProperty("role", "accent")
        self.play_button.clicked.connect(lambda: self.playRequested.emit(self._first_marked_position()))
        self.download_button = QPushButton("↓ 下载选中")
        self.download_button.setProperty("role", "success")
        self.download_button.clicked.connect(self._emit_download)
        foot.addWidget(self.pill)
        foot.addStretch(1)
        foot.addWidget(self.select_all)
        foot.addWidget(self.select_none)
        foot.addWidget(self.tail_chip)
        foot.addWidget(self.play_button)
        foot.addWidget(self.download_button)
        foot_host.setVisible(False)
        self._foot = foot_host
        outer.addWidget(foot_host)
        self._sync_batch_state()

    # ---------------------------------------------------------------- 对外 API
    def chapters(self):
        return self._chapters

    def set_scroll_area(self, area):
        self._scroll_area = area

    def set_chapters(self, chapters, drama_vip=False):
        self._release_preloads()           # 旧剧预热会话先归还
        self._warm_token += 1
        self._chapters = list(chapters or [])
        self._drama_vip = bool(drama_vip)
        self._by_number = {}
        for position, chapter in enumerate(self._chapters):
            self._by_number.setdefault(_episode_number(chapter, position), position)
        self._watch_position = None
        self._watch_progress = 0.0
        self._marks.clear()
        if self._batch:
            self._set_batch(False)
        self._render()
        self._schedule_preload()

    def set_downloaded(self, numbers):
        """标记本地已有解密成品的分集（打开 / 切回详情页时对一次账）。"""
        numbers = set(numbers or ())
        if numbers == self._downloaded:
            return
        self._downloaded = numbers
        self._sync_tooltips()
        self._sync_count()

    def set_watched(self, position, progress=0.0):
        """上次看到哪一集：那一集作为当前高亮，之前的都置浅。"""
        if not (isinstance(position, int) and 0 <= position < len(self._chapters)):
            position = None
        state = (position, round(float(progress or 0.0), 4))
        if state == (self._watch_position, self._watch_progress):
            return
        self._watch_position, self._watch_progress = state
        self._render()

    # -------------------------------------------------------------------- 渲染
    def _render(self):
        anchor = self._watch_position
        payload = []
        for position, chapter in enumerate(self._chapters):
            payload.append({
                "index": _episode_number(chapter, position),
                "watched": anchor is not None and position < anchor,
                "vip": self._chapter_vip(chapter),
                "current": position == anchor,
            })
        self._episodes = payload
        self.grid.set_episodes(payload)
        self._hook_group_chips()
        self._sync_tooltips()
        self._sync_count()
        self._fit_timer.start(0)
        if anchor is not None:
            # 当前集落位：延一拍，等分组/尺寸就位（EpisodeGrid 会先切回所在组）
            QTimer.singleShot(0, self.grid.scroll_to_current)

    def _chapter_vip(self, chapter):
        # 实测红果 chapters 无逐集 VIP 字段，vip 挂在 drama 上：整剧标志优先，
        # 再兼容几种常见逐集写法，都没有就 False。
        if self._drama_vip:
            return True
        if chapter.get("vip"):
            return True
        if str(chapter.get("type") or "").lower() == "vip":
            return True
        return chapter.get("is_vip") in (1, "1", True, "true")

    def _hook_group_chips(self):
        """分组 chip 每次 rebuild 都是新对象；接一个后置钩子重新贴勾选样式。"""
        for chip in self.grid.findChildren(QPushButton):
            if chip.property("guoRole") != "groupTab" or getattr(chip, "_e1_hooked", False):
                continue
            chip._e1_hooked = True
            # F2 自己先接了 _on_chip_clicked；后接的槽在同一次 emit 里后执行，
            # 正好落在 _rebuild_grid 之后
            chip.clicked.connect(self._apply_marks)

    def _buttons_by_number(self):
        result = {}
        for btn in self.grid.findChildren(QPushButton):
            if btn.property("guoRole") != "episode" or btn.isHidden():
                continue
            text = str(btn.text())
            if not text.isdigit():
                continue
            result[int(text)] = btn
        return result

    def _repaint_buttons(self):
        for btn in self._buttons_by_number().values():
            btn.update()

    def _anchor_number(self):
        if self._watch_position is None:
            return None
        return _episode_number(self._chapters[self._watch_position], self._watch_position)

    def _sync_tooltips(self):
        for number, btn in self._buttons_by_number().items():
            position = self._by_number.get(number)
            if position is None:
                continue
            chapter = self._chapters[position]
            extra = _extra_title(chapter.get("title"), number)
            lines = ["第 %d 集%s" % (number, (" · %s" % extra) if extra else "")]
            try:
                seconds = max(0, int(float(chapter.get("duration") or 0)))
            except (TypeError, ValueError):
                seconds = 0
            if seconds:
                lines.append("时长 %02d:%02d" % divmod(seconds, 60))
            if number in self._downloaded:
                lines.append("已下载到本地")
            if position == self._watch_position:
                text = "上次看到这里"
                if self._watch_progress > 0:
                    text += "（%.0f%%）" % (self._watch_progress * 100.0)
                lines.append(text)
            lines.append("单击勾选" if self._batch else "单击播放")
            btn.setToolTip("\n".join(lines))

    def _sync_count(self):
        total = len(self._chapters)
        if not total:
            self.count_label.setText("")
            return
        text = "共 %d 集" % total
        if self._downloaded:
            text += " · 已下载 %d 集" % len(self._downloaded)
        self.count_label.setText(text)

    # ------------------------------------------------------------------ 点击
    def _on_episode_clicked(self, number):
        position = self._by_number.get(number)
        if position is None:
            return
        if self._batch:
            self._toggle_mark(number)
        else:
            self.playRequested.emit(position)

    def _toggle_mark(self, number):
        if number in self._marks:
            self._marks.discard(number)
        else:
            self._marks.add(number)
        self._apply_marks()
        self._sync_batch_state()

    def _apply_marks(self, *_ignored):
        """勾选态 = episode 按钮挂 current 属性（accent 实底，QSS 驱动）。

        真实当前集本来就带 current，不会被「取消勾选」误伤。
        """
        anchor_number = self._anchor_number()
        for number, btn in self._buttons_by_number().items():
            want = number in self._marks or number == anchor_number
            on = btn.property("current") == "true"
            if want == on:
                continue
            if want:
                btn.setProperty("current", "true")
            else:
                btn.setProperty("current", None)   # PyQt5 无 removeProperty；None=清除
            _repolish(btn)

    def _set_batch(self, on):
        on = bool(on)
        if on == self._batch:
            return
        self._batch = on
        self.batch_toggle.blockSignals(True)
        self.batch_toggle.setChecked(on)
        self.batch_toggle.setText("退出批量" if on else "批量下载")
        self.batch_toggle.blockSignals(False)
        self._foot.setVisible(on)
        if not on:
            self._marks.clear()
        self._apply_marks()
        self._sync_tooltips()
        self._sync_batch_state()

    def _set_all(self, checked):
        self._marks = set(self._by_number) if checked else set()
        self._apply_marks()
        self._sync_batch_state()

    def _select_tail(self, target):
        self._marks = {n for n in self._by_number if n <= target}
        self._apply_marks()
        self._sync_batch_state()

    def _first_marked_position(self):
        positions = sorted(self._by_number[n] for n in self._marks if n in self._by_number)
        return positions[0] if positions else 0

    def _emit_download(self):
        positions = sorted(self._by_number[n] for n in self._marks if n in self._by_number)
        if positions:
            self.downloadRequested.emit(positions)

    def _sync_batch_state(self):
        count = len(self._marks)
        self.pill.setText("未选择分集" if not count else "已选 %d 集" % count)
        self.play_button.setEnabled(count > 0)
        self.download_button.setEnabled(count > 0)

    # ------------------------------------------------------------------ 跳集
    def _jump_to(self, number):
        position = self._by_number.get(number)
        if position is None:
            return
        self.grid.select_group(position // EpisodeGrid.GROUP_SIZE)
        self._fit_timer.start(0)
        QTimer.singleShot(0, lambda n=number: self._reveal_jump(n))

    def _reveal_jump(self, number):
        self._apply_marks()          # select_group 重建后重新贴勾选/当前样式
        btn = self._buttons_by_number().get(number)
        if btn is None:
            return
        if self._scroll_area is not None:
            try:
                self._scroll_area.ensureWidgetVisible(btn, 60, 140)
            except Exception:
                pass
        if btn.property("current") == "true":
            return                   # 当前集/勾选集本来就有高亮，不再闪
        btn.setProperty("current", "true")
        _repolish(btn)

        def revert():
            try:
                if number not in self._marks and number != self._anchor_number():
                    btn.setProperty("current", None)
                    _repolish(btn)
            except RuntimeError:
                pass                 # 1.2s 内切了剧，按钮已被 deleteLater
        QTimer.singleShot(1200, revert)

    # -------------------------------------------------------------- 尺寸/滚轮
    def _hook_wheel_forwarding(self):
        # EpisodeGrid 内部竖向滚动区高度总是被我们拟合到不溢出；滚轮继续
        # 交给外层详情滚动条，保持详情页单滚动面（旧版语义）。
        for area in self.grid.findChildren(QScrollArea):
            viewport = area.viewport()
            viewport.installEventFilter(self)
            self._inner_viewports.add(viewport)

    def eventFilter(self, watched, event):
        etype = event.type()
        if watched is self.grid:
            if etype in (QEvent.LayoutRequest, QEvent.Resize):
                self._fit_timer.start(0)
        elif watched in self._inner_viewports:
            if etype == QEvent.Wheel and self._scroll_area is not None:
                area = watched.parent()
                bar = area.verticalScrollBar() if area is not None else None
                if bar is None or bar.maximum() == 0:
                    outer_bar = self._scroll_area.verticalScrollBar()
                    delta = event.pixelDelta().y() or event.angleDelta().y()
                    outer_bar.setValue(outer_bar.value() - delta)
                    return True
        return False

    def _fit_grid_height(self):
        total = len(self._episodes)
        if not total:
            self.grid.setVisible(False)
            return
        self.grid.setVisible(True)
        width = max(320, self.width())
        cols = max(1, (width - _FLOW_MARGIN + _FLOW_SPACING - 5) //
                   (EpisodeGrid.EPISODE_SIZE[0] + _FLOW_SPACING))
        shown = len(self._buttons_by_number()) or min(total, EpisodeGrid.GROUP_SIZE)
        rows = (shown + cols - 1) // cols
        height = rows * (EpisodeGrid.EPISODE_SIZE[1] + _FLOW_SPACING) - _FLOW_SPACING
        if total > EpisodeGrid.GROUP_SIZE:
            chip_cols = max(1, width // _CHIP_STRIDE)
            chip_rows = (self.grid.group_count() + chip_cols - 1) // chip_cols
            height += 8 + chip_rows * _CHIP_ROW_H + (chip_rows - 1) * _FLOW_SPACING
        self.grid.setFixedHeight(height)

    # -------------------------------------------------------------- 预缓冲 E1c
    def _find_pool(self):
        """沿 parent 链找 MainWindow 形状的预热池宿主（duck-typing，不动 main.py）。"""
        widget = self.parentWidget()
        while widget is not None:
            if (hasattr(widget, "_preloads")
                    and callable(getattr(widget, "_release_preload", None))
                    and getattr(widget, "_core", None) is not None
                    and callable(getattr(widget._core, "preload", None))):
                return widget
            widget = widget.parentWidget()
        return None

    def _find_drama(self):
        widget = self.parentWidget()
        while widget is not None:
            drama = getattr(getattr(widget, "poster", None), "drama", None)
            if isinstance(drama, dict) and drama.get("id"):
                return drama
            widget = widget.parentWidget()
        return None

    def _schedule_preload(self):
        if not self._chapters:
            return
        pool = self._find_pool()
        drama = self._find_drama()
        if pool is None or drama is None:
            return
        quality = int((getattr(pool, "_playback", None) or {}).get("quality") or 0)
        core = pool._core
        token = self._warm_token
        for position in range(min(PRELOAD_FIRST_N, len(self._chapters))):
            key = self._make_key(pool, drama, position, quality)
            if key is None or key in self._warm_keys or key in pool._preloads \
                    or key in getattr(pool, "_preloading", set()):
                continue
            self._warm_keys.add(key)
            chapter = dict(self._chapters[position])

            def work(core=core, drama=drama, chapter=chapter, position=position,
                     quality=quality, key=key, token=token):
                result = {"token": token, "key": key, "quality": quality,
                          "drama_id": str(drama.get("id") or ""), "plan": None}
                try:
                    result["plan"] = core.preload(drama, chapter, position + 1,
                                                  quality=quality)
                except Exception:
                    pass             # 失败静默：预缓冲只是锦上添花
                try:
                    self._preloadDone.emit(result)
                except RuntimeError:
                    pass             # 控件已销毁，会话由 closeEvent/_clear_preloads 兜底
            threading.Thread(target=work, daemon=True).start()

    def _make_key(self, pool, drama, position, quality):
        maker = getattr(pool, "_preload_key", None)
        if callable(maker):
            try:
                return tuple(maker(drama, position, quality))
            except Exception:
                pass
        drama_id = str(drama.get("id") or "")
        return (drama_id, int(position), int(quality or 0)) if drama_id else None

    def _on_preload_done(self, result):
        key = tuple(result.get("key") or ())
        self._warm_keys.discard(key)
        pool = self._find_pool()
        plan = result.get("plan") or {}
        session = plan.get("session")
        drama = self._find_drama()
        stale = (result.get("token") != self._warm_token
                 or pool is None
                 or not drama
                 or str(drama.get("id") or "") != result.get("drama_id"))
        usable = bool((plan.get("local") and plan.get("url")) or session)
        if stale or not usable:
            self._release_session(pool, session)   # 超时/换剧/不可用：归还会话防泄漏
            return
        try:
            pool._preloads[key] = {"plan": plan, "at": time.time()}
            self._injected.add(key)
        except Exception:
            self._release_session(pool, session)
            return
        prune = getattr(pool, "_prune_preloads", None)
        if callable(prune):
            try:
                prune(drama, int(result.get("quality") or 0))
            except Exception:
                pass

    def _release_preloads(self):
        pool = self._find_pool()
        for key in list(self._injected):
            self._injected.discard(key)
            if pool is None:
                continue
            entry = pool._preloads.pop(key, None)
            if entry is not None:
                releaser = getattr(pool, "_release_preload", None)
                if callable(releaser):
                    try:
                        releaser(entry)
                    except Exception:
                        pass
        self._warm_keys.clear()

    def _release_session(self, pool, session):
        if not session:
            return
        releaser = getattr(pool, "_release_session", None) if pool is not None else None
        if callable(releaser):
            try:
                releaser(session)
            except Exception:
                pass
            return
        if pool is not None:
            try:
                pool._core.release(session)
            except Exception:
                pass
