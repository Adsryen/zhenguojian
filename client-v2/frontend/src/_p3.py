import io
import os

base = os.path.dirname(os.path.abspath(__file__))
view = os.path.join(base, 'views', 'CalendarView.vue')
store = os.path.join(base, 'stores', 'calendar.ts')
i18n = os.path.join(base, 'services', 'i18n.ts')


def patch(path, reps):
    s = io.open(path, encoding='utf-8').read()
    for old, new in reps:
        assert s.count(old) == 1, (os.path.basename(path), old[:60], s.count(old))
        s = s.replace(old, new, 1)
    io.open(path, 'w', encoding='utf-8').write(s)
    print("OK", os.path.basename(path))


patch(view, [
    # scrollRef 我自己用了却没声明，模板也没绑上——typecheck 会直接红
    ("const store = useCalendarStore()",
     "const store = useCalendarStore()\n"
     "// 页内滚动容器（fillViewport 要知道内容有没有超过一屏）\n"
     "const scrollRef = ref<HTMLElement | null>(null)"),
    ('<div class="cal-scroll" @scroll.passive="onScroll">',
     '<div ref="scrollRef" class="cal-scroll" @scroll.passive="onScroll">'),
    # 可点的状态行需要一点视觉差别，否则看着还是像纯文本
    (".list-status {",
     ".list-status.clickable {\n"
     "  cursor: pointer;\n"
     "  text-decoration: underline dotted;\n"
     "}\n"
     ".list-status:disabled {\n"
     "  cursor: default;\n"
     "}\n"
     ".list-status {"),
])

patch(store, [
    # 视图靠它决定"这行能不能点"，所以判据放在 store 里（与文案同源）
    ("      if (s.stalled) return t('calendar.statusStalled')",
     "      if (s.stalled) return t('calendar.statusStalled')"),
    ("    statusText(s): (s: any) => string {",
     "    retryable(s): (s: any) => boolean {\n"
     "      // 只有两种停摆可以点重试（v1 也只在失败/无新内容时把状态行做成按钮）：\n"
     "      // ① 有 error；② 连续多页没有新内容（stalled）。\n"
     "      // 「没有更多了」「加载中…」点了没意义，所以 disabled。\n"
     "      return !!s.error || !!s.stalled\n"
     "    },\n"
     "    statusText(s): (s: any) => string {"),
])

patch(i18n, [
    ("  'calendar.statusNoMore': '没有更多了',",
     "  'calendar.statusNoMore': '没有更多了',\n"
     "  'calendar.statusStalled': '连续多页没有新剧（点此重试）',"),
    ("  'calendar.statusNoMore': 'No more items',",
     "  'calendar.statusNoMore': 'No more items',\n"
     "  'calendar.statusStalled': 'Several pages in a row had no new titles (click to retry)',"),
])
