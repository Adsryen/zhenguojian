package danmaku

import (
	"sort"
	"sync"
)

// Scheduler 把「视频时间轴当前位置」映射成「此刻应显示的弹幕队列」。
// ASS 字幕轨模式下 mpv 自带时间轴调度，Scheduler 不直接驱动渲染，
// 用途是：1) 前端非遮挡区（如弹幕输入联想、发送队列）取邻近弹幕；
// 2) 抓取进度推进判断；3) 单测/调试可观测。
//
// 输入是排序后的 []Item（Fetcher.Items()）；SeekTo/Advance 以毫秒推进内部
// 时钟；二分定位窗口边界，seek 后队列立即重定位（v1 Qt 覆盖层时代的
// 行为基线，见 main._pump 节奏的等价物）。
type Scheduler struct {
	mu     sync.Mutex
	items  []Item
	cursor int64 // 当前时间轴毫秒
	// aheadMs 窗口前瞻：默认 8000（一屏滚动时长，与 ScrollSeconds 对齐）。
	aheadMs int64
	// sent 已消费水位：Next 只吐出 cursor 之后、未吐过的条目。
	sent int64
}

func NewScheduler(items []Item, aheadMs int64) *Scheduler {
	s := &Scheduler{items: sortByTime(items), aheadMs: aheadMs}
	if s.aheadMs <= 0 {
		s.aheadMs = int64(ScrollSeconds * 1000)
	}
	return s
}

// Replace 换集/增量刷新时整体替换队列（ASS 全量重建同语义），
// 保持内部时钟不回退：seek 由调用方紧随其后显式发出。
func (s *Scheduler) Replace(items []Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = sortByTime(items)
}

// SeekTo 重定位时间轴（毫秒，绝对）。消费水位同步移动——seek 到过去会把
// 该区间的队列重新打开（对齐 v1「拖动进度条 libass 自己重画」的体感）。
func (s *Scheduler) SeekTo(positionMs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if positionMs < 0 {
		positionMs = 0
	}
	s.cursor = positionMs
	s.sent = positionMs
}

// Cursor 当前内部时钟。
func (s *Scheduler) Cursor() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursor
}

// Window 返回 (cursor, cursor+ahead] 秒级窗口内的条目（只读快照，不推进
// 水位）。二分定位左界后线性收集到右界。
func (s *Scheduler) Window() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.windowLocked(s.cursor, s.cursor+s.aheadMs)
}

// Advance 把时钟推到 max(cursor, toMs)，并吐出 (oldSent, newCursor] 区间
// 新出现的条目（服务层 tick 用：返回非空即可触发局部事件）。
func (s *Scheduler) Advance(toMs int64) []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	if toMs > s.cursor {
		s.cursor = toMs
	}
	out := s.windowLocked(s.sent, s.cursor)
	if len(out) > 0 {
		s.sent = s.cursor
	}
	return out
}

func (s *Scheduler) windowLocked(fromMs, toMs int64) []Item {
	if len(s.items) == 0 || toMs <= fromMs {
		return nil
	}
	// 二分：第一个 TimeMs > fromMs 的下标
	lo := sort.Search(len(s.items), func(i int) bool { return s.items[i].TimeMs > fromMs })
	var out []Item
	for i := lo; i < len(s.items) && s.items[i].TimeMs <= toMs; i++ {
		out = append(out, s.items[i])
	}
	return out
}
