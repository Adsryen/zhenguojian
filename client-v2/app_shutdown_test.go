package main

// app_shutdown_test.go —— 退出收尾（OnShutdown）并发化的行为钉。
//
// 这个文件只钉六件具体的事（每条都对应一个"下次有人能改坏"的形状）：
//
//	① 三步**同时**起跑：barrier 只在三步都在里面时才放行。谁把它们改回串行，
//	   barrier 就永远凑不齐 → 全部被判定"未在预算内结束" → 红。
//	   这是结构证明，不靠掐秒表（掐秒表的门在 CI 上一定会抖）。
//	② 某一步卡死时：其余步骤照常完成，整体仍有界（不会变成无界等待）。
//	③ 终态步骤（mpv）不许被 budget 放弃——放弃了就是进程在 Terminate 之前退出，
//	   孤儿 mpv 回来。这条是本任务唯一不能松的保证，所以它单独一条测试。
//	④ killBudget 到点必须点名"可能留孤儿"：全套只剩这一条口子，不许静默。
//	⑤ 真 *mpv.Player*（只假造进程与管道）走退出路径时 ipc quit 仍排在
//	   Terminate 之前、Terminate 恰好一次、没有多余 Kill。
//	⑥ 退出路径用终态 Close，「关闭播放窗」用非终态 Stop（第五十九轮修的那条，
//	   不许再混回去）；失败不许被 `_ =` 吞掉。
//
// ⚠ quitWatchdog 依然是 os.Exit(0)：任何测试都不许把它调回 5 秒去"顺便等一等"，
// 否则测试进程会被第 5 秒整截带走（本任务已经抓过多次这一族，见 app_close_test.go）。
// 这里的测试也不碰 beforeClose——要碰的话照 app_close_test.go 先把 quitWatchdog 挪远。

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zhenguojian/client-v2/internal/downloadsvc"
	"zhenguojian/client-v2/internal/mpv"
	"zhenguojian/client-v2/internal/player"
	"zhenguojian/client-v2/internal/tray"
)

// ---------- 测试脚手架 ----------

// runShutdownStepsWatched 把 runner 放进 goroutine 再收：实现一旦被改回串行、
// 或谁把上限调成无界等待，测试要**快而可读地**红——只靠 `go test -timeout`
// 兜底的红只会说"卡住了"，不说是哪一步、为什么。
func runShutdownStepsWatched(t *testing.T, timeout time.Duration, parent context.Context,
	steps []shutdownStep, budget, killBudget time.Duration,
	logf func(format string, v ...any)) []shutdownOutcome {
	t.Helper()
	done := make(chan []shutdownOutcome, 1)
	go func() { done <- runShutdownSteps(parent, steps, budget, killBudget, logf) }()
	select {
	case outs := <-done:
		return outs
	case <-time.After(timeout):
		t.Fatalf("runShutdownSteps 在 %s 内没返回：步骤被串行化了，或谁把上限改成了无界等待", timeout)
	}
	return nil
}

func outcomeByName(outs []shutdownOutcome, name string) (shutdownOutcome, bool) {
	for _, o := range outs {
		if o.name == name {
			return o, true
		}
	}
	return shutdownOutcome{}, false
}

func mustOutcome(t *testing.T, outs []shutdownOutcome, name string) shutdownOutcome {
	t.Helper()
	o, ok := outcomeByName(outs, name)
	if !ok {
		t.Fatalf("结果里没有步骤 %q：%+v", name, outs)
	}
	return o
}

// logCapture 收 logf 的每一行（"失败不许静默"这条全靠它能被断言）。
type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (c *logCapture) printf(format string, v ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, fmt.Sprintf(format, v...))
}

func (c *logCapture) join() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

// waitForCond 轮询到条件成立；失败时报 why。
func waitForCond(t *testing.T, timeout time.Duration, why string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", why)
}

// recPlayer 是 player.MpvPlayer 的记录替身（⑦⑧ 用它盯 Close/Stop 的差别）。
// 它只回答"服务层调了我什么、调了几次"，mpv 的真实时序由 fakeMpvBackend 那条测。
type recPlayer struct {
	mu      sync.Mutex
	quits   int
	quitErr error
}

func (f *recPlayer) Start(context.Context) error { return nil }
func (f *recPlayer) LoadWith(string, map[string]string, string, mpv.LoadOptions) error {
	return nil
}
func (f *recPlayer) Play() error                 { return nil }
func (f *recPlayer) Pause() error                { return nil }
func (f *recPlayer) AbsSeekSmooth(float64) error { return nil }
func (f *recPlayer) SetSpeed(float64) error      { return nil }
func (f *recPlayer) SetVolume(int) error         { return nil }
func (f *recPlayer) SubAdd(string, string) error { return nil }
func (f *recPlayer) SubReload() error            { return nil }
func (f *recPlayer) SubRemove() error            { return nil }
func (f *recPlayer) State() mpv.State            { return mpv.State{FileLoaded: true} }
func (f *recPlayer) Alive() bool                 { return true }
func (f *recPlayer) Events() <-chan mpv.Event    { return make(chan mpv.Event) }

func (f *recPlayer) Quit(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.quits++
	return f.quitErr
}

func (f *recPlayer) quitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.quits
}

// fixedResolver 给一个能播的假地址（不发网络；URL 只是让 Open 过判空那关）。
type fixedResolver struct{}

func (fixedResolver) Resolve(context.Context, player.Request) (player.Plan, error) {
	return player.Plan{URL: "http://127.0.0.1:9/does-not-exist.mp4"}, nil
}

func openFakeSession(t *testing.T, svc *player.Service) {
	t.Helper()
	if err := svc.Open(context.Background(), player.Request{
		DramaID: "d1", Title: "假集", Index: 1,
		Drama:   map[string]any{"title": "假剧"},
		Chapter: map[string]any{"id": "c1"},
	}); err != nil {
		t.Fatalf("起假播放会话失败: %v", err)
	}
}

// ---------- ① 并发，不是串行 ----------

// TestShutdownStepsRunConcurrently 用 barrier 证明"三步同时在跑"。
//
// 判据为什么不用秒表：把墙钟差 100ms 当门，慢 CI 上就是随机红——
// 而这条 barrier 是结构的：串行实现永远凑不齐三步，必然被判成超时放弃。
// 顺带记并发峰值（=步数），它就是"墙钟 = 最慢一步、而不是求和"的机器可读版本。
func TestShutdownStepsRunConcurrently(t *testing.T) {
	names := []string{"tray", "downloads", "mpv"}
	in := make(chan string, len(names))
	release := make(chan struct{})

	var inside atomic.Int32
	var peak atomic.Int32
	go func() {
		for i := 0; i < len(names); i++ {
			select {
			case <-in:
			case <-time.After(5 * time.Second):
				return // 凑不齐：留给 budget 判它"未在预算内结束"
			}
		}
		close(release)
	}()

	steps := make([]shutdownStep, 0, len(names))
	for _, raw := range names {
		name := raw
		steps = append(steps, shutdownStep{name: name, run: func(context.Context) error {
			cur := inside.Add(1)
			for {
				old := peak.Load()
				if cur <= old || peak.CompareAndSwap(old, cur) {
					break
				}
			}
			in <- name
			<-release // barrier：所有兄弟步骤都在里面才放行
			inside.Add(-1)
			return nil
		}})
	}

	const budget = 400 * time.Millisecond
	start := time.Now()
	outs := runShutdownStepsWatched(t, 4*budget, context.Background(), steps, budget, 2*budget, nil)
	took := time.Since(start)

	if got := peak.Load(); int(got) != len(names) {
		t.Fatalf("并发峰值=%d, want %d（三步没能同时在跑 = 被串行化了）", got, len(names))
	}
	for _, o := range outs {
		if o.abandoned || o.err != nil {
			t.Fatalf("步骤 %s 没干净结束：%+v（barrier 没开 = 步骤没并发起跑）", o.name, o)
		}
	}
	// 墙钟 = 最慢一步而不是三步求和。3 倍余量是故意的：这条只是补刀，
	// 真正的并发证明是上面那个 barrier 与峰值（结构判据，不靠秒表）；
	// 拿"took < budget"当门，慢 CI 上迟早随机红，那比没门更坏。
	if took > 3*budget {
		t.Fatalf("并发收尾用了 %s，超过 %s（串行求和才会顶到这里）：%+v", took, 3*budget, outs)
	}
}

// ---------- ② 有步骤卡住：其余仍完成、整体有界 ----------

func TestShutdownStuckStepDoesNotBlockOthers(t *testing.T) {
	const budget = 40 * time.Millisecond
	var ran atomic.Int32
	stuckCtx := make(chan error, 1)

	steps := []shutdownStep{
		{
			name: "tray", // 卡死的那一步（永不返回，只能靠 budget 放手）
			run: func(ctx context.Context) error {
				select {
				case <-ctx.Done():
					stuckCtx <- ctx.Err()
					return ctx.Err()
				case <-time.After(time.Minute):
					return nil
				}
			},
		},
		{name: "downloads", run: func(context.Context) error { ran.Add(1); return nil }},
		{name: "mpv", terminal: true, run: func(context.Context) error { ran.Add(1); return nil }},
	}

	start := time.Now()
	outs := runShutdownStepsWatched(t, 5*time.Second, context.Background(), steps, budget, 2*budget, nil)
	elapsed := time.Since(start)

	if got := ran.Load(); got != 2 {
		t.Fatalf("其余步骤完成数=%d, want 2（卡住的一步把它们带走了）：%+v", got, outs)
	}
	stuck := mustOutcome(t, outs, "tray")
	if !stuck.abandoned {
		t.Fatalf("卡住的步骤该被放弃，实得：%+v", stuck)
	}
	if stuck.err == nil {
		t.Fatal("放弃必须带错误，不能报成 ok")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("整体没界：等了 %s（budget 是 %s）", elapsed, budget)
	}
	// runner 返回时要 cancel 被放弃步骤的 ctx——那是它唯一的收工信号。
	select {
	case err := <-stuckCtx:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("被放弃步骤看到的 ctx 错误=%v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner 返回后没有取消被放弃步骤的 ctx")
	}
}

// TestShutdownLateResultDoesNotUndoAbandonment 钉住判定的单向性：
// 放弃之后迟到的"成功"不许把结果改写成 ok——否则日志和断言都跟着调度抖。
func TestShutdownLateResultDoesNotUndoAbandonment(t *testing.T) {
	release := make(chan struct{})
	steps := []shutdownStep{
		{name: "tray", run: func(ctx context.Context) error {
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return nil
			}
		}},
	}
	outs := runShutdownStepsWatched(t, 5*time.Second, context.Background(), steps, 20*time.Millisecond, 40*time.Millisecond, nil)
	close(release) // 迟到的完成

	o := mustOutcome(t, outs, "tray")
	if !o.abandoned {
		t.Fatalf("放弃的判定被迟到结果改了：%+v", o)
	}
}

// ---------- ③④ 终态步骤：budget 不撒手 / killBudget 到点点名孤儿 ----------

// TestShutdownWaitsTerminalStepPastBudget 是"不留孤儿 mpv"的结构保证：
// 普通步骤到点放弃，终态步骤必须继续等——放弃了就是 os.Exit 抢在
// mpv 的 Terminate 之前，孤儿回来。
func TestShutdownWaitsTerminalStepPastBudget(t *testing.T) {
	const budget = 30 * time.Millisecond
	entered := make(chan struct{})
	release := make(chan struct{})
	var ordinaryDone atomic.Bool

	steps := []shutdownStep{
		{name: "tray", run: func(context.Context) error { ordinaryDone.Store(true); return nil }},
		{name: "mpv", terminal: true, run: func(context.Context) error {
			close(entered)
			<-release
			return nil
		}},
	}

	done := make(chan []shutdownOutcome, 1)
	go func() { done <- runShutdownSteps(context.Background(), steps, budget, time.Minute, nil) }()

	<-entered
	// 终态步骤还卡着：runner 若在这里返回，就是把 mpv 的收尾放弃了。
	select {
	case outs := <-done:
		t.Fatalf("终态步骤被 budget 放弃了（会留孤儿 mpv）：%+v", outs)
	case <-time.After(10 * budget):
	}
	if !ordinaryDone.Load() {
		t.Fatal("普通步骤应当已经跑完（它不该被终态步骤拖住）")
	}

	close(release)
	var outs []shutdownOutcome
	select {
	case outs = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("释放终态步骤后 runner 没返回")
	}
	mpvOut := mustOutcome(t, outs, "mpv")
	if mpvOut.abandoned || mpvOut.err != nil {
		t.Fatalf("终态步骤正常结束后被判成失败/放弃：%+v", mpvOut)
	}
	// 普通步骤是真的完成（不是被 killBudget 顺手放弃的）
	if o := mustOutcome(t, outs, "tray"); o.abandoned || o.err != nil {
		t.Fatalf("普通步骤 %+v", o)
	}
}

// TestShutdownKillBudgetWarnsOrphan：唯一还能漏 mpv 的口子必须点名，不许静默。
func TestShutdownKillBudgetWarnsOrphan(t *testing.T) {
	logs := &logCapture{}
	steps := []shutdownStep{
		{name: "mpv", terminal: true, run: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}},
	}
	outs := runShutdownStepsWatched(t, 5*time.Second, context.Background(), steps, 10*time.Millisecond, 30*time.Millisecond, logs.printf)
	o := mustOutcome(t, outs, "mpv")
	if !o.abandoned {
		t.Fatalf("终态步骤超过 killBudget 后应放手，实得 %+v", o)
	}
	if joined := logs.join(); !strings.Contains(joined, "孤儿") {
		t.Fatalf("日志没点名孤儿风险，实得:\n%s", joined)
	}
}

// ---------- ⑤ panic 不带走其它步骤 ----------

func TestShutdownStepPanicBecomesError(t *testing.T) {
	logs := &logCapture{}
	var sibling atomic.Bool
	steps := []shutdownStep{
		{name: "boom", run: func(context.Context) error { panic("故意的步骤 panic") }},
		{name: "mpv", terminal: true, run: func(context.Context) error { sibling.Store(true); return nil }},
	}
	outs := runShutdownStepsWatched(t, 6*time.Second, context.Background(), steps, time.Second, time.Second, logs.printf)

	boom := mustOutcome(t, outs, "boom")
	if boom.err == nil || !strings.Contains(boom.err.Error(), "panic") {
		t.Fatalf("panic 没被收成 error：%+v", boom)
	}
	if boom.abandoned {
		t.Fatalf("panic 应算失败而不是超时：%+v", boom)
	}
	if !sibling.Load() {
		t.Fatal("一步 panic 带走了其它步骤")
	}
	if joined := logs.join(); !strings.Contains(joined, "boom") {
		t.Fatalf("panic 没进日志，实得:\n%s", joined)
	}
}

// ---------- ⑥ 真 mpv 栈：quit 仍排在 Terminate 之前 ----------

// fakeMpvBackend 只假造**进程与管道**；Quit 里的
// ipc quit → Terminate → 等进程 → Kill 全是 internal/mpv/player.go 的真代码。
// （fake 的 serve 每条命令都立刻回 ack，等价于一个肯配合的 mpv。）
type fakeMpvBackend struct {
	mu   sync.Mutex
	seq  []string
	proc *fakeMpvProc
	// silenceCmd：这条命令收下了但不回 ack —— 用来造"mpv 卡在 quit 上"，
	// 而 set_property/loadfile 照常应答（否则 Open 自己就先超时了）。
	silenceCmd string

	terminated chan struct{}
	termOnce   sync.Once
}

func newFakeMpvBackend(silenceQuit bool) *fakeMpvBackend {
	be := &fakeMpvBackend{terminated: make(chan struct{})}
	if silenceQuit {
		be.silenceCmd = "quit"
	}
	be.proc = &fakeMpvProc{be: be, dead: make(chan struct{})}
	return be
}

func (f *fakeMpvBackend) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq = append(f.seq, s)
}

func (f *fakeMpvBackend) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seq...)
}

func (f *fakeMpvBackend) indexOf(want string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, v := range f.seq {
		if v == want {
			return i
		}
	}
	return -1
}

func (f *fakeMpvBackend) countOf(want string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, v := range f.seq {
		if v == want {
			n++
		}
	}
	return n
}

func (f *fakeMpvBackend) markTerminated(kind string) {
	f.termOnce.Do(func() {
		f.record(kind)
		close(f.terminated)
		close(f.proc.dead)
	})
}

func (f *fakeMpvBackend) Launch(ctx context.Context, exePath string, args []string, wid uintptr) (mpv.Process, error) {
	f.record("launch")
	return f.proc, nil
}

func (f *fakeMpvBackend) Dial(ctx context.Context, socketPath string) (mpv.Conn, error) {
	f.record("dial")
	srv, cli := net.Pipe()
	go f.serve(srv)
	return cli, nil
}

func (f *fakeMpvBackend) serve(c net.Conn) {
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var req struct {
			Command   []any `json:"command"`
			RequestID int   `json:"requestID"`
		}
		if json.Unmarshal(line, &req) != nil || len(req.Command) == 0 {
			continue
		}
		name := fmt.Sprint(req.Command[0])
		if name == "observe_property" {
			continue // 事件连接上的注册行：无 requestID，真 mpv 也不回
		}
		f.record("cmd:" + name)
		if name == f.silenceCmd || req.RequestID == 0 {
			continue
		}
		// net.Pipe 的写要等对方读；对面（命令连接）已经不等了的时候必须放手，
		// 否则这个 serve goroutine 挂死在写上——那是测试自己的洞，不是被测对象。
		_ = c.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		_, _ = c.Write([]byte(fmt.Sprintf(`{"error":"success","requestID":%d}`+"\n", req.RequestID)))
		_ = c.SetWriteDeadline(time.Time{})
	}
}

type fakeMpvProc struct {
	be   *fakeMpvBackend
	dead chan struct{}
}

func (p *fakeMpvProc) PID() int        { return 4242 }
func (p *fakeMpvProc) ErrTail() string { return "" }

func (p *fakeMpvProc) Wait() error {
	<-p.dead
	return nil
}

func (p *fakeMpvProc) Terminate() error {
	p.be.markTerminated("terminate")
	return nil
}

func (p *fakeMpvProc) Kill() error {
	p.be.markTerminated("kill")
	return nil
}

func newShutdownApp(svc *player.Service) *App {
	return &App{
		playerSvc:   svc,
		trayH:       tray.New(tray.Actions{}),
		downloadSvc: downloadsvc.New(downloadsvc.Options{}),
	}
}

// TestShutdownKeepsMpvQuitBeforeTerminate 是任务里"顺序没被并发打乱"那条。
//
// 两个压力叠在一起测：
//   - 托盘那一步被换成"只有 mpv 已经 Terminate 才放行"的哨兵——步骤一旦被
//     串行化，它永远等不到放行，于是这条必红（并发才是它的前提）；
//   - 断言真 Quit 的时序：cmd:quit 的位次在 terminate 之前、terminate 恰好一次、
//     没有多余的 kill。
func TestShutdownKeepsMpvQuitBeforeTerminate(t *testing.T) {
	for _, noAck := range []bool{false, true} {
		name := "mpv 肯应答"
		if noAck {
			name = "mpv wedged"
		}
		t.Run(name, func(t *testing.T) {
			be := newFakeMpvBackend(noAck)
			svc := player.New(player.Config{
				Resolver: fixedResolver{},
				Backend:  be,
				MpvPath:  "fake-mpv.exe",
			})
			a := newShutdownApp(svc)
			openFakeSession(t, svc)

			steps := a.shutdownSteps()
			// 只把 tray 步骤的**身体**换成哨兵；步骤清单本身
			// （谁 terminal、叫什么）仍然来自真实接线。
			replaced := false
			for i := range steps {
				if steps[i].name != "tray" {
					continue
				}
				steps[i].run = func(ctx context.Context) error {
					select {
					case <-be.terminated:
						return nil
					case <-ctx.Done():
						return errors.New("托盘步骤没等到 mpv 的 Terminate（步骤被串行化了？）")
					}
				}
				replaced = true
			}
			if !replaced {
				t.Fatal("shutdownSteps 里没有名为 tray 的步骤（接线漂了）")
			}

			// wedged 那条要真吃 mpv.Quit 内部的 1s quit 宽限，budget 得容得下。
			budget, kill := 2*time.Second, 4*time.Second
			outs := runShutdownStepsWatched(t, 12*time.Second, context.Background(), steps, budget, kill, nil)
			for _, o := range outs {
				if o.abandoned || o.err != nil {
					t.Fatalf("步骤 %s 没干净结束：%+v（seq=%v）", o.name, o, be.snapshot())
				}
			}
			seq := be.snapshot()
			q, tm := be.indexOf("cmd:quit"), be.indexOf("terminate")
			if q < 0 || tm < 0 {
				t.Fatalf("序列里没有 quit/terminate：%v", seq)
			}
			if q > tm {
				t.Fatalf("quit 排在了 Terminate 后面（位次 %d > %d）：%v", q, tm, seq)
			}
			if n := be.countOf("terminate"); n != 1 {
				t.Fatalf("terminate 次数=%d, want 1：%v", n, seq)
			}
			if n := be.countOf("kill"); n != 0 {
				t.Fatalf("不该有 Kill 兜底（真进程还活着才需要）：%v", seq)
			}
			if err := svc.Open(context.Background(), player.Request{Index: 2}); !errors.Is(err, player.ErrClosed) {
				t.Fatalf("退出路径必须是终态 Close，Open 实得 err=%v", err)
			}
		})
	}
}

// TestShutdownNoExtraQuitUnderConcurrency：并发收尾不得多发 quit
// （多发一次就意味着 Stop/Close 被混用过，或 quit 排在了 Terminate 之后）。
func TestShutdownNoExtraQuitUnderConcurrency(t *testing.T) {
	f := &recPlayer{}
	svc := player.New(player.Config{
		Resolver:    fixedResolver{},
		MpvLauncher: func(mpv.Config) player.MpvPlayer { return f },
	})
	a := newShutdownApp(svc)
	openFakeSession(t, svc)

	outs := runShutdownStepsWatched(t, 12*time.Second, context.Background(), a.shutdownSteps(), 2*time.Second, 4*time.Second, nil)
	for _, o := range outs {
		if o.abandoned || o.err != nil {
			t.Fatalf("步骤 %s：%+v", o.name, o)
		}
	}
	if got := f.quitCount(); got != 1 {
		t.Fatalf("退出路径 Quit 调用次数=%d, want 1", got)
	}
}

// ---------- ⑦ Close / Stop 不许混（第五十九轮那条红字） ----------

// TestShutdownStepUsesTerminalClose：退出路径这一步走完，服务必须判死。
func TestShutdownStepUsesTerminalClose(t *testing.T) {
	f := &recPlayer{}
	svc := player.New(player.Config{
		Resolver:    fixedResolver{},
		MpvLauncher: func(mpv.Config) player.MpvPlayer { return f },
	})
	a := newShutdownApp(svc)
	openFakeSession(t, svc)

	{
		steps := a.shutdownSteps()
		if len(steps) != 3 {
			t.Fatalf("步骤清单漂了：%+v", steps)
		}
	}
	outs := runShutdownStepsWatched(t, 12*time.Second, context.Background(), a.shutdownSteps(), 2*time.Second, 4*time.Second, nil)
	if o := mustOutcome(t, outs, "mpv"); o.abandoned || o.err != nil {
		t.Fatalf("mpv 步骤 %+v", o)
	}
	if got := f.quitCount(); got != 1 {
		t.Fatalf("Quit 次数=%d, want 1（Close 与 Stop 混用会多发）", got)
	}
	if err := svc.Open(context.Background(), player.Request{Index: 2}); !errors.Is(err, player.ErrClosed) {
		t.Fatalf("退出后服务仍可用 = 走的是非终态 Stop，want ErrClosed，实得 %v", err)
	}
}

// TestPlayerClosePlayerStaysReopenable：「关闭播放窗」= 非终态 Stop。
// 它一旦改回 Close，本进程内再也播不了（用户截图那句
// 「播放失败：player: 服务已关闭」就是这么来的）。
func TestPlayerClosePlayerStaysReopenable(t *testing.T) {
	f := &recPlayer{}
	svc := player.New(player.Config{
		Resolver:    fixedResolver{},
		MpvLauncher: func(mpv.Config) player.MpvPlayer { return f },
	})
	a := newShutdownApp(svc)
	openFakeSession(t, svc)

	if _, err := a.PlayerClosePlayer(); err != nil {
		t.Fatalf("PlayerClosePlayer: %v", err)
	}
	// Stop 的 quit 在 goroutine 里（v1 stop(blocking=False)：换集不许冻界面）。
	waitForCond(t, 3*time.Second, "Stop 应把 quit 发出去", func() bool { return f.quitCount() >= 1 })

	if err := svc.Open(context.Background(), player.Request{Index: 2}); errors.Is(err, player.ErrClosed) {
		t.Fatal("PlayerClosePlayer 把服务判死了：这里必须是 Stop（非终态）")
	} else if err != nil {
		t.Fatalf("关播放窗后应能再开，Open 实得 %v", err)
	}
}

// ---------- ⑧ 生产接线：预算、日志、看门狗契约 ----------

// TestAppShutdownWiring 跑真正的 a.shutdown(ctx)：它读的是包级
// shutdownBudget / shutdownKillBudget / shutdownLogf，这条测的就是这层接线。
func TestAppShutdownWiring(t *testing.T) {
	oldBudget, oldKill, oldLog := shutdownBudget, shutdownKillBudget, shutdownLogf
	t.Cleanup(func() {
		shutdownBudget, shutdownKillBudget, shutdownLogf = oldBudget, oldKill, oldLog
	})
	shutdownBudget = 2 * time.Second
	shutdownKillBudget = 4 * time.Second

	logs := &logCapture{}
	shutdownLogf = logs.printf

	// 让 mpv 那一步返回错误：改前这里是 `_ = a.playerSvc.Close()`，
	// 错误既不上报也没日志（player.Config.Logger 是 nil，包内那句
	// s.log 也是静默的）。这条钉住"失败不许再被吞"。
	f := &recPlayer{quitErr: errors.New("假 quit 失败")}
	svc := player.New(player.Config{
		Resolver:    fixedResolver{},
		MpvLauncher: func(mpv.Config) player.MpvPlayer { return f },
	})
	a := newShutdownApp(svc)
	openFakeSession(t, svc)

	shutdownDone := make(chan struct{})
	go func() { a.shutdown(context.Background()); close(shutdownDone) }()
	select {
	case <-shutdownDone:
	case <-time.After(12 * time.Second):
		t.Fatal("a.shutdown 没在 12s 内返回（生产 budget 是 2s/4s）= 串行或无界等待回来了")
	}

	joined := logs.join()
	if !strings.Contains(joined, "假 quit 失败") {
		t.Fatalf("Close 的失败没进日志，实得:\n%s", joined)
	}
	if !strings.Contains(joined, "并发收尾用时") {
		t.Fatalf("日志没有整条收尾的用时（真机排查点关闭慢就靠这行），实得:\n%s", joined)
	}
	for _, want := range []string{"tray", "downloads", "mpv"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("日志里缺步骤 %q，实得:\n%s", want, joined)
		}
	}
}

// TestShutdownTrayErrorIsNotSwallowed 钉住"任一步失败都要有日志"这一条。
//
// 反例就是改前的形状：`Stop()` 无返回值，"消息线程 2s 未退出"被 `_ =`
// 吞在托盘包内部，退出路径上没人知道托盘到底走没走干净。现在包内把错误
// 交出来（internal/tray/tray_stop_test.go 钉那半边），调用点若再吞一次
// （`_ = stopTray(...)` 然后 return nil），这条红。
func TestShutdownTrayErrorIsNotSwallowed(t *testing.T) {
	oldStop, oldLog := stopTray, shutdownLogf
	t.Cleanup(func() { stopTray, shutdownLogf = oldStop, oldLog })

	want := errors.New("假托盘：消息线程未退出")
	stopTray = func(*tray.Handler) error { return want }
	logs := &logCapture{}
	shutdownLogf = logs.printf

	svc := player.New(player.Config{})
	a := newShutdownApp(svc)
	defer func() { _ = svc.Close() }()

	a.shutdown(context.Background())

	joined := logs.join()
	if !strings.Contains(joined, "假托盘：消息线程未退出") {
		t.Fatalf("托盘的失败没进日志，实得:\n%s", joined)
	}
	if !strings.Contains(joined, "tray") {
		t.Fatalf("日志里认不出是哪一步失败，实得:\n%s", joined)
	}
}

// TestShutdownStepShape 钉住步骤清单：三步、只有 mpv 那一步是终态。
// 顺序本身不是语义（并发起跑），但"谁不能放弃"必须是可读的一行。
func TestShutdownStepShape(t *testing.T) {
	svc := player.New(player.Config{})
	defer func() { _ = svc.Close() }() // 只为收掉 New 起的 tick 协程
	a := newShutdownApp(svc)
	steps := a.shutdownSteps()
	if len(steps) != 3 {
		t.Fatalf("步骤数=%d, want 3：%+v", len(steps), steps)
	}
	wantTerminal := map[string]bool{"tray": false, "downloads": false, "mpv": true}
	for _, st := range steps {
		term, ok := wantTerminal[st.name]
		if !ok {
			t.Fatalf("出现未知步骤 %q", st.name)
		}
		if st.terminal != term {
			t.Fatalf("步骤 %q 的 terminal=%v, want %v", st.name, st.terminal, term)
		}
		delete(wantTerminal, st.name)
	}
	if len(wantTerminal) != 0 {
		t.Fatalf("步骤清单缺项：%+v", steps)
	}
}

// TestShutdownBudgetsUnderWatchdog 是耗时账的机器可读版本：
// budget < killBudget < quitWatchdog。
// 最外面那条是关键——看门狗是 os.Exit(0)，收尾一旦排到它后面，
// 连"放弃等待"的机会都没有，正在排队的 quit/Terminate 一起没。
func TestShutdownBudgetsUnderWatchdog(t *testing.T) {
	if !(shutdownBudget < shutdownKillBudget) {
		t.Fatalf("budget(%s) 必须小于 killBudget(%s)", shutdownBudget, shutdownKillBudget)
	}
	if !(shutdownKillBudget < quitWatchdog) {
		t.Fatalf("killBudget(%s) 必须小于 quitWatchdog(%s)：看门狗一响就是孤儿 mpv",
			shutdownKillBudget, quitWatchdog)
	}
}
