package danmaku

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type fetchCall struct {
	Session  string
	StartMs  int64
	EndMsAbs int64 // 传给核心的 durationMs 参数 = 绝对窗尾（v1 语义）
}

type fakeSource struct {
	mu    sync.Mutex
	calls []fetchCall
	pages map[int64]Window // key=窗口起点
	errs  []error          // 队列头错误，逐次弹出；耗尽后无错
}

func (s *fakeSource) FetchWindow(_ context.Context, session string, startMs, endMs int64) (Window, error) {
	s.mu.Lock()
	s.calls = append(s.calls, fetchCall{Session: session, StartMs: startMs, EndMsAbs: endMs})
	var err error
	if len(s.errs) > 0 {
		err = s.errs[0]
		s.errs = s.errs[1:]
	}
	page := s.pages[startMs]
	s.mu.Unlock()
	if err != nil {
		return Window{}, err
	}
	return page, nil
}

func (s *fakeSource) callLog() []fetchCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]fetchCall, len(s.calls))
	copy(out, s.calls)
	return out
}

// syncFetcher 把 run 钩子换成同步执行，测试免 goroutine 竞态；
// endMs 计算复刻 runDefault（span + bust）。
func syncFetcher(t *testing.T, src *fakeSource, opt FetcherOptions) *Fetcher {
	t.Helper()
	opt.MinGap = time.Nanosecond
	opt.RetryDelays = []time.Duration{time.Nanosecond, time.Nanosecond}
	f := NewFetcher(src, opt)
	f.run = func(job, start int64) {
		f.mu.Lock()
		span := f.eps.DurationMs
		bust := f.bust[start]
		session := f.eps.Session
		f.mu.Unlock()
		win, err := src.FetchWindow(context.Background(), session, start, span+int64(bust))
		f.complete(job, start, win, err)
	}
	return f
}

func winItems(startMs int64, n int) Window {
	var items []Item
	for i := 0; i < n; i++ {
		items = append(items, Item{ID: fmt.Sprintf("%d-%d", startMs, i), TimeMs: startMs + int64(i*100)})
	}
	return Window{Items: items}
}

// 窗口推进 = v1：当前窗优先，预取下一窗；顺序严格按未完成的最近窗。
func TestFetcherWindowOrder(t *testing.T) {
	src := &fakeSource{pages: map[int64]Window{
		0: winItems(0, 1), 30_000: winItems(30_000, 1),
		60_000: winItems(60_000, 1), 90_000: winItems(90_000, 1),
	}}
	f := syncFetcher(t, src, FetcherOptions{})
	f.SetEpisode(Episode{Session: "s1", DurationMs: 120_000, Key: "1-1"})

	f.Tick(0)       // 窗 0
	f.Tick(0)       // 0 完成 → 预取窗 30000
	f.Tick(0)       // 都完成，无请求
	f.Tick(45_000)  // 当前窗 30000 完成 → 预取 60000
	f.Tick(95_000)  // → 90000
	f.Tick(119_000) // 全部完成（无窗可抓，120000>=span）

	var starts []int64
	for _, c := range src.callLog() {
		starts = append(starts, c.StartMs)
		if c.Session != "s1" {
			t.Errorf("session 未透传: %+v", c)
		}
	}
	want := []int64{0, 30_000, 60_000, 90_000}
	if len(starts) != len(want) {
		t.Fatalf("窗口序列 = %v, want %v", starts, want)
	}
	for i := range want {
		if starts[i] != want[i] {
			t.Fatalf("窗口序列 = %v, want %v", starts, want)
		}
	}
	done, itemsN, inflight, _ := f.Stats()
	if done != 4 || itemsN != 4 || inflight {
		t.Errorf("Stats = %d,%d,%v", done, itemsN, inflight)
	}
}

// 同窗重放（bust 重试）返回重复 id → 去重只算一条。
func TestFetcherDedupeById(t *testing.T) {
	dup := Window{Items: []Item{{ID: "b", TimeMs: 200, Text: "重复"}, {ID: "c", TimeMs: 31_000, Text: "新"}}}
	src := &fakeSource{pages: map[int64]Window{
		0:      {Items: []Item{{ID: "a", TimeMs: 100, Text: "旧"}}},
		30_000: dup,
		60_000: dup, // 重叠窗
	}}
	f := syncFetcher(t, src, FetcherOptions{})
	f.SetEpisode(Episode{Session: "s", DurationMs: 90_000})
	for _, pos := range []int64{0, 0, 30_000, 60_000, 60_000} {
		f.Tick(pos)
	}
	_, itemsN, _, _ := f.Stats()
	if itemsN != 3 {
		t.Fatalf("去重失败: %d", itemsN)
	}
	list := f.Items()
	if list[0].Text != "旧" || list[2].Text != "新" {
		t.Errorf("Items 未按时间序: %+v", list)
	}
}

// 抽样启发式 = v1：expected=min(90,total*window/span)，不足一半且
// tries<2 → bust+1 重试（换 duration 绕核心缓存），两次后照单全收。
func TestFetcherSamplingBust(t *testing.T) {
	src := &fakeSource{pages: map[int64]Window{0: winItems(0, 5)}}
	for k := range src.pages { // Total=90 → expected=30；只回 5 条=抽样
		w := src.pages[k]
		w.Total = 90
		src.pages[k] = w
	}
	f := syncFetcher(t, src, FetcherOptions{})
	f.SetEpisode(Episode{Session: "s", DurationMs: 90_000})

	f.Tick(0) // 第 1 次：5<15，tries 0→1，bust=1，窗未完成
	if done, _, _, _ := f.Stats(); done != 0 {
		t.Fatalf("抽样窗不该标记完成")
	}
	f.Tick(0) // 第 2 次：tries 1→2，bust=2
	f.Tick(0) // 第 3 次：tries 用尽 → 收 5 条完成
	calls := src.callLog()
	if len(calls) != 3 {
		t.Fatalf("调用次数 = %d", len(calls))
	}
	for i, wantEnd := range []int64{90_000, 90_001, 90_002} {
		if calls[i].EndMsAbs != wantEnd {
			t.Errorf("call %d endMs = %d, want %d（bust 未生效）", i, calls[i].EndMsAbs, wantEnd)
		}
	}
	done, itemsN, _, _ := f.Stats()
	if done != 1 || itemsN != 5 {
		t.Errorf("bust 用尽后应收尾: %d 窗 %d 条", done, itemsN)
	}
}

// 失败退避：3 次放弃 + note（v1「本集弹幕加载失败」），之后推进到下一窗。
func TestFetcherFailureGiveUp(t *testing.T) {
	boom := errors.New("boom")
	src := &fakeSource{
		pages: map[int64]Window{0: {}, 30_000: winItems(30_000, 1)},
		errs:  []error{boom, boom, boom},
	}
	f := syncFetcher(t, src, FetcherOptions{})
	f.SetEpisode(Episode{Session: "s", DurationMs: 60_000})
	f.Tick(0)
	if n := f.Note(); n == "" || !contains(n, "稍后重试") {
		t.Errorf("首失败 note = %q", n)
	}
	f.Tick(0)
	f.Tick(0) // 第 3 次
	if n := f.Note(); !contains(n, "本集弹幕加载失败") {
		t.Errorf("放弃 note = %q", n)
	}
	done, _, _, _ := f.Stats()
	if done != 1 {
		t.Errorf("放弃窗应标记完成（防每 tick 刷屏重试）: %d", done)
	}
	f.Tick(0) // 推进到下一窗（成功清空 note，v1 done() 同语义）
	if n := f.Note(); n != "" {
		t.Errorf("成功窗后 note 应清空: %q", n)
	}
}

// 整集空 → note「本集没有弹幕」。
func TestFetcherEmptyEpisode(t *testing.T) {
	src := &fakeSource{pages: map[int64]Window{0: {Items: []Item{}}}}
	f := syncFetcher(t, src, FetcherOptions{})
	f.SetEpisode(Episode{Session: "s", DurationMs: 30_000})
	f.Tick(0)
	if n := f.Note(); !contains(n, "本集没有弹幕") {
		t.Errorf("note = %q", n)
	}
}

// 换集丢弃在途回调（v1 _danmaku_job 语义）。
func TestFetcherSetEpisodeDropsInflight(t *testing.T) {
	src := &fakeSource{pages: map[int64]Window{0: winItems(0, 10)}}
	f := syncFetcher(t, src, FetcherOptions{})
	f.SetEpisode(Episode{Session: "s1", DurationMs: 90_000})
	// 手工模拟在途：Tick 前记录 job
	f.mu.Lock()
	oldJob := f.job
	f.mu.Unlock()
	f.Tick(0) // 同步完成窗 0
	_, itemsN, _, _ := f.Stats()
	if itemsN != 10 {
		t.Fatalf("基线抓取失败: %d", itemsN)
	}
	f.SetEpisode(Episode{Session: "s2", DurationMs: 90_000})
	_, itemsN, _, _ = f.Stats()
	if itemsN != 0 {
		t.Errorf("换集未清空: %d", itemsN)
	}
	// 迟到的旧 job 回调必须被丢弃
	f.complete(oldJob, 30_000, winItems(30_000, 7), nil)
	_, itemsN, _, _ = f.Stats()
	if itemsN != 0 {
		t.Errorf("stale job 回调污染: %d", itemsN)
	}
}

// MinGap 限速：间隔不满足就不发起（v1 抽样防背靠背）。
func TestFetcherMinGap(t *testing.T) {
	src := &fakeSource{pages: map[int64]Window{0: winItems(0, 1), 30_000: winItems(30_000, 1)}}
	now := time.Unix(0, 0)
	f := NewFetcher(src, FetcherOptions{
		MinGap: 3 * time.Second,
		Now:    func() time.Time { return now },
	})
	f.run = func(job, start int64) {
		f.mu.Lock()
		span, session := f.eps.DurationMs, f.eps.Session
		bust := f.bust[start]
		f.mu.Unlock()
		win, err := src.FetchWindow(context.Background(), session, start, span+int64(bust))
		f.complete(job, start, win, err)
	}
	f.SetEpisode(Episode{Session: "s", DurationMs: 120_000})
	f.Tick(0) // 窗 0，lastReq=0
	f.Tick(0) // 窗 30000：MinGap 未到 → 不发起
	if len(src.callLog()) != 1 {
		t.Fatalf("MinGap 未限速: %v", src.callLog())
	}
	now = now.Add(3 * time.Second)
	f.Tick(0) // 到期发起
	calls := src.callLog()
	if len(calls) != 2 || calls[1].StartMs != 30_000 {
		t.Fatalf("到期应发起窗 30000: %v", calls)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
