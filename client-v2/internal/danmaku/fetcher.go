package danmaku

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"
)

// WindowMS 抓取窗口长度：核心 danmaku 动作一次只给一个 30s 窗口
// （单窗上限 90 条，和官方 App 一致），v1 按 nextMs 逐窗走完。
const WindowMS = int64(30_000)

// MinGap 连续窗口请求的最小间隔。接口对背靠背请求会抽样（一个窗口只回
// 十几条，隔 2s 以上回满——v1 实测取 3.0s 留余量），逐窗必须按它限速。
const MinGap = 3 * time.Second

// Source 是取弹幕的数据缝。生产实现由服务层包一个 native 门面调用
// （core "danmaku" 动作：playbackSession/startMs/durationMs，endMs 语义
// 同 v1——传给核心的 durationMs 是窗口结束的绝对毫秒）。
// internal/native 门面目前**没有** danmaku 方法（依赖请求见交付报告），
// 所以这里只定义接口不改 native。
type Source interface {
	FetchWindow(ctx context.Context, session string, startMs, endMs int64) (Window, error)
}

// Window 一次窗口抓取结果（核心 hongguoDanmakuPage 的形状）。
type Window struct {
	Items []Item
	// NextMs 核心游标：下一窗建议起点。Fetcher 主游标按 30s 对齐走，
	// NextMs 仅用于空窗快速跳过（v1 windows 直方图同用途）。
	NextMs int64
	Total  int64
	// Windows 30s 窗口直方图（key=窗口起点毫秒，value=条数），可为 nil。
	Windows map[string]int64
}

// Episode 一集的目标：抓取会话 + 整集时长（span，v1 同名）。
type Episode struct {
	Session    string
	DurationMs int64
	// Key ASS 文件名 key（dramaID-集号，v1 write_ass key 语义），仅供展示。
	Key string
}

// FetcherOptions 抓取节奏参数，零值走 v1 默认。单测把 MinGap/RetryDelays
// 缩到毫秒级。
type FetcherOptions struct {
	WindowMS int64
	MinGap   time.Duration
	// PrefetchWindows 预取窗数：v1 永远保证「当前窗 + 下一窗」，=1。
	PrefetchWindows int
	// MaxWindowFails 同窗连续失败次数上限，达到即放弃并置 note
	// （v1：3 次，退避 10s/30s）。
	MaxWindowFails int
	RetryDelays    []time.Duration
	// MaxSampleRetries 抽样重试上限（v1 tries<2）。
	MaxSampleRetries int
	// Now 可注入假时钟；nil 用 time.Now。
	Now func() time.Time
}

func (o FetcherOptions) normalize() FetcherOptions {
	if o.WindowMS <= 0 {
		o.WindowMS = WindowMS
	}
	if o.MinGap <= 0 {
		o.MinGap = MinGap
	}
	if o.PrefetchWindows <= 0 {
		o.PrefetchWindows = 1
	}
	if o.MaxWindowFails <= 0 {
		o.MaxWindowFails = 3
	}
	if len(o.RetryDelays) == 0 {
		o.RetryDelays = []time.Duration{10 * time.Second, 30 * time.Second}
	}
	if o.MaxSampleRetries <= 0 {
		o.MaxSampleRetries = 2
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Fetcher 是 v1 main.py 在线懒抓取循环的 Go 形态：Tick(positionMs) 驱动
// （播放器 500ms tick 即可），内部串行发起窗口请求，不阻塞调用方。
//
// 行为逐条对齐 v1：
//   - start 按 30s 对齐，目标窗 = [当前窗, 当前窗+1]，已在 windows 集合则跳过；
//   - in-flight 只允许一个请求；两次请求间隔不小于 MinGap（抽样限速）；
//   - 返回按 id 去重合入 items（无 id 的按 timeMs+text 兜底 key）；
//   - 抽样启发式：expected=min(90, total*windowMS/span)，条数不足一半且
//     tries<2 → 该窗退回未完成，bust+1（重试换 duration 绕核心 5 分钟缓存），
//     冷却 = MinGap+RetryDelays[0]（v1 last=now+12 ≈ 3+9 的形态）；
//   - 失败：连续失败计数退避 RetryDelays，达 MaxWindowFails 放弃并
//     note(「本集弹幕加载失败」)，成功窗清零计数；
//   - 空窗且累计无 item → note(「本集没有弹幕」)。
type Fetcher struct {
	src Source
	opt FetcherOptions
	mu  sync.Mutex
	eps Episode

	items    map[string]Item // v1 state["items"]，按 id 去重
	windows  map[int64]bool  // 已完成窗口起点
	bust     map[int64]int   // 抽样重试的 duration 偏移
	tries    map[int64]int   // 每窗抽样重试次数
	failures map[int64]int   // 每窗连续失败次数
	note     string
	inflight bool
	lastReq  time.Time
	lastFail time.Time
	version  int64 // items/状态变更计数，服务层据此决定是否重建 ASS
	job      int64 // 换集/关闭时自增，丢弃在途回调（v1 _danmaku_job 语义）

	// 单测钩子：run 在后台 goroutine 执行 fetch；nil 用默认实现。
	run func(job int64, start int64)
}

func NewFetcher(src Source, opt FetcherOptions) *Fetcher {
	return &Fetcher{src: src, opt: opt.normalize(),
		items: map[string]Item{}, windows: map[int64]bool{},
		bust: map[int64]int{}, tries: map[int64]int{}, failures: map[int64]int{}}
}

// SetEpisode 换集：清空窗口/去重状态，重设会话与整集时长。
func (f *Fetcher) SetEpisode(eps Episode) {
	f.mu.Lock()
	f.job++
	f.eps = eps
	f.items = map[string]Item{}
	f.windows = map[int64]bool{}
	f.bust = map[int64]int{}
	f.tries = map[int64]int{}
	f.failures = map[int64]int{}
	f.note = ""
	f.inflight = false
	f.lastReq = time.Time{}
	f.lastFail = time.Time{}
	f.version++
	f.mu.Unlock()
}

// Episode 当前目标（服务层 ASS key 命名用）。
func (f *Fetcher) Episode() Episode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.eps
}

// Tick 由播放位置驱动（毫秒，可为负=0 处理）。尝试推进当前窗与预取窗。
// 非阻塞：实际请求在内部 goroutine 里跑。
func (f *Fetcher) Tick(positionMs int64) {
	if positionMs < 0 {
		positionMs = 0
	}
	f.mu.Lock()
	if f.eps.DurationMs <= 0 || f.eps.Session == "" || f.inflight {
		f.mu.Unlock()
		return
	}
	span := f.eps.DurationMs
	start := positionMs / f.opt.WindowMS * f.opt.WindowMS
	// v1：候选 (start, start+window)，取第一个未完成的
	var target, job int64
	var run func(int64, int64)
	found := false
	for cand := start; cand < start+f.opt.WindowMS*int64(f.opt.PrefetchWindows+1); cand += f.opt.WindowMS {
		if cand >= span {
			break
		}
		if f.windows[cand] {
			continue
		}
		if !f.lastReq.IsZero() && f.opt.Now().Sub(f.lastReq) < f.opt.MinGap {
			break // 限速：不满足 MinGap 本轮不发起
		}
		if !f.lastFail.IsZero() && f.opt.Now().Sub(f.lastFail) < f.retryDelayLocked(cand) {
			break // 失败退避窗口内
		}
		f.inflight = true
		f.lastReq = f.opt.Now()
		run = f.run
		if run == nil {
			run = f.runDefault
		}
		target, job, found = cand, f.job, true
		break
	}
	f.mu.Unlock()
	// 钩子在锁外执行：complete 需要重新拿锁（同步实现也不会自锁）
	if found {
		run(job, target)
	}
}

// retryDelayLocked 第 n 次失败的退避（v1：10s，再 30s）。
func (f *Fetcher) retryDelayLocked(start int64) time.Duration {
	n := f.failures[start]
	idx := n - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(f.opt.RetryDelays) {
		idx = len(f.opt.RetryDelays) - 1
	}
	return f.opt.RetryDelays[idx]
}

// runDefault 默认执行器：goroutine 调 Source，完成后回写状态。
func (f *Fetcher) runDefault(job int64, start int64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		f.mu.Lock()
		span := f.eps.DurationMs
		bust := f.bust[start]
		session := f.eps.Session
		f.mu.Unlock()
		win, err := f.src.FetchWindow(ctx, session, start, span+int64(bust))
		f.complete(job, start, win, err)
	}()
}

// complete 窗口回调收口（v1 done()/failed() 语义）。job 不匹配=已换集，丢弃。
func (f *Fetcher) complete(job int64, start int64, win Window, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if job != f.job {
		return
	}
	f.inflight = false
	if err != nil {
		f.failures[start]++
		n := f.failures[start]
		if n >= f.opt.MaxWindowFails {
			f.windows[start] = true // 放弃：标记完成，避免每 tick 重试刷屏
			f.note = "本集弹幕加载失败"
		} else {
			f.note = "弹幕加载失败，稍后重试：" + err.Error()
		}
		f.lastFail = f.opt.Now()
		f.version++
		return
	}
	f.failures[start] = 0
	added := 0
	for _, it := range win.Items {
		key := it.ID
		if key == "" {
			key = "n:" + strconv.FormatInt(it.TimeMs, 10) + ":" + it.Text
		}
		if _, dup := f.items[key]; dup {
			continue
		}
		f.items[key] = it
		added++
	}
	span := f.eps.DurationMs
	// 抽样启发式（v1 expected 公式）：明显低于整集密度估算的一半 → 重试。
	total := win.Total
	if total == 0 {
		total = int64(len(f.items))
	}
	expected := 0.0
	if total > 0 && span > 0 {
		expected = min(90.0, float64(total)*float64(f.opt.WindowMS)/float64(span))
	}
	tries := f.tries[start]
	if expected >= 20 && float64(len(win.Items)) < expected*0.5 && tries < f.opt.MaxSampleRetries {
		f.tries[start] = tries + 1
		f.bust[start]++
		// v1: last = now + 12（≈ MinGap 3s + 冷却 9s），走 lastFail 通道
		f.lastFail = f.opt.Now()
		f.note = ""
		f.version++
		return
	}
	f.windows[start] = true
	if added > 0 || win.Items != nil {
		f.note = ""
	}
	if len(f.items) == 0 && len(win.Items) == 0 {
		f.note = "本集没有弹幕"
	}
	f.version++
}

// Items 已抓取弹幕的时间序快照（ASS 重建输入，v1 _refresh_danmaku_track 同语义）。
func (f *Fetcher) Items() []Item {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Item, 0, len(f.items))
	for _, it := range f.items {
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TimeMs < out[j].TimeMs })
	return out
}

// Note 展示文案（空=正常）；服务层事件透传给前端。
func (f *Fetcher) Note() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.note
}

// Stats 供测试与诊断：已完成窗数、累计条数、是否有在途。
func (f *Fetcher) Stats() (doneWindows int, itemCount int, inflight bool, version int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.windows), len(f.items), f.inflight, f.version
}
