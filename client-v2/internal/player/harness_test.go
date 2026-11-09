package player_test

// harness_test.go：net.Pipe 假 mpv（对齐 internal/mpv/ipc.go JSON IPC 语义）。
// 每个 fakeServer = 一次 Launch：
//   - Dial 第 1 次 → 命令连接：收 {"command":[...],"requestID":N}，回
//     {"error":"success","requestID":N}；"quit" 额外让 fakeProc 退出；
//     "loadfile" 5ms 后经事件连接补推 file-loaded（真 mpv 行为）。
//   - Dial 第 2 次 → 事件连接：排空 Start 写入的 observe_property 行，
//     测试/服务器可从服务端往里 push {"event":...} 行；closeEvents 模拟断线
//     （eventPump 读到 EOF → markDisconnected → EventDisconnected）。

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"zhenguojian/client-v2/internal/danmaku"
	"zhenguojian/client-v2/internal/mpv"
	"zhenguojian/client-v2/internal/player"
)

type cmdRec struct {
	Server int // 所属 fakeServer 序号（spawn 顺序）
	Raw    string
	Name   string
	Args   []string
}

type fakeProc struct {
	exited chan struct{}
	once   sync.Once
	pid    int
}

func (p *fakeProc) Wait() error { <-p.exited; return nil }
func (p *fakeProc) Terminate() error {
	p.once.Do(func() { close(p.exited) })
	return nil
}
func (p *fakeProc) Kill() error     { return p.Terminate() }
func (p *fakeProc) PID() int        { return p.pid }
func (p *fakeProc) ErrTail() string { return "" }

type fakeServer struct {
	idx     int
	socket  string
	proc    *fakeProc
	cmdPipe net.Conn // 交给 Player 的一端
	evPipe  net.Conn // 交给 Player 的一端
	cmdConn net.Conn // 服务端
	evConn  net.Conn // 服务端

	cmdMu  sync.Mutex
	cmds   []cmdRec
	evWrMu sync.Mutex
	evCls  sync.Once
	dialMu sync.Mutex
	dials  int
}

func newFakeServer(idx int, socket string) *fakeServer {
	cmdSrv, cmdCli := net.Pipe()
	evSrv, evCli := net.Pipe()
	fs := &fakeServer{
		idx:     idx,
		socket:  socket,
		proc:    &fakeProc{exited: make(chan struct{}), pid: 9000 + idx},
		cmdPipe: cmdCli,
		evPipe:  evCli,
		cmdConn: cmdSrv,
		evConn:  evSrv,
	}
	go fs.serveCmd()
	go fs.drainEvents()
	return fs
}

func flatten(cmd []any) (name string, args []string) {
	if len(cmd) == 0 {
		return "", nil
	}
	name = fmt.Sprintf("%v", cmd[0])
	for _, a := range cmd[1:] {
		args = append(args, fmt.Sprintf("%v", a))
	}
	return
}

func (fs *fakeServer) serveCmd() {
	r := bufio.NewReader(fs.cmdConn)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var req struct {
			Command   []any `json:"command"`
			RequestID int   `json:"requestID"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		name, args := flatten(req.Command)
		fs.cmdMu.Lock()
		fs.cmds = append(fs.cmds, cmdRec{Server: fs.idx, Raw: strings.TrimSpace(string(line)), Name: name, Args: args})
		fs.cmdMu.Unlock()

		if req.RequestID != 0 {
			ack := fmt.Sprintf(`{"error":"success","requestID":%d}`, req.RequestID)
			fs.cmdMu.Lock()
			_, _ = fs.cmdConn.Write([]byte(ack + "\n"))
			fs.cmdMu.Unlock()
		}
		switch name {
		case "quit":
			_ = fs.proc.Terminate()
		case "loadfile":
			go func() {
				time.Sleep(5 * time.Millisecond)
				fs.PushEvent(`{"event":"file-loaded"}`)
			}()
		}
	}
}

// drainEvents 排空 Player 在事件连接上的 observe_property 写入。
func (fs *fakeServer) drainEvents() {
	r := bufio.NewReader(fs.evConn)
	_, _ = io.Copy(io.Discard, r)
}

// PushEvent 向 Player 的事件泵推一行 JSON 事件。
func (fs *fakeServer) PushEvent(line string) {
	fs.evWrMu.Lock()
	defer fs.evWrMu.Unlock()
	_, _ = fs.evConn.Write([]byte(line + "\n"))
}

func (fs *fakeServer) PushProperty(name string, data float64) {
	fs.PushEvent(fmt.Sprintf(`{"event":"property-change","name":%q,"data":%v}`, name, data))
}

// CloseEvents 模拟事件通道断开（mpv 进程/管道死亡）。
func (fs *fakeServer) CloseEvents() {
	fs.evCls.Do(func() { _ = fs.evConn.Close() })
}

func (fs *fakeServer) hasLoadfile() bool {
	for _, c := range fs.Cmds() {
		if c.Name == "loadfile" {
			return true
		}
	}
	return false
}

func (fs *fakeServer) Cmds() []cmdRec {
	fs.cmdMu.Lock()
	defer fs.cmdMu.Unlock()
	out := make([]cmdRec, len(fs.cmds))
	copy(out, fs.cmds)
	return out
}

type fakeBackend struct {
	mu      sync.Mutex
	servers []*fakeServer
}

func (fb *fakeBackend) Launch(_ context.Context, _ string, args []string, _ uintptr) (mpv.Process, error) {
	socket := ""
	for _, a := range args {
		if s, ok := strings.CutPrefix(a, "--input-ipc-server="); ok {
			socket = s
		}
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	fs := newFakeServer(len(fb.servers), socket)
	fb.servers = append(fb.servers, fs)
	return fs.proc, nil
}

func (fb *fakeBackend) Dial(_ context.Context, socketPath string) (mpv.Conn, error) {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	for i := len(fb.servers) - 1; i >= 0; i-- {
		fs := fb.servers[i]
		if fs.socket != socketPath {
			continue
		}
		fs.dialMu.Lock()
		defer fs.dialMu.Unlock()
		fs.dials++
		switch fs.dials {
		case 1:
			return fs.cmdPipe, nil
		case 2:
			return fs.evPipe, nil
		default:
			return nil, fmt.Errorf("fake mpv: %s 意外第 %d 次 Dial", socketPath, fs.dials)
		}
	}
	return nil, fmt.Errorf("fake mpv: 未知 socket %s", socketPath)
}

func (fb *fakeBackend) serverCount() int {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return len(fb.servers)
}

func (fb *fakeBackend) lastServer() *fakeServer {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if len(fb.servers) == 0 {
		return nil
	}
	return fb.servers[len(fb.servers)-1]
}

// serverCmds 汇总所有 fakeServer 的命令（spawn 序）。
func (fb *fakeBackend) server(i int) *fakeServer {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if i < 0 || i >= len(fb.servers) {
		return nil
	}
	return fb.servers[i]
}

func (fb *fakeBackend) serverCmds() []cmdRec {
	fb.mu.Lock()
	srvs := append([]*fakeServer(nil), fb.servers...)
	fb.mu.Unlock()
	var out []cmdRec
	for _, s := range srvs {
		out = append(out, s.Cmds()...)
	}
	return out
}

// waitFor 轮询直到 pred 为真或超时。
func waitFor(t *testing.T, timeout time.Duration, pred func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", desc)
}

// emitter 捕获 Emit(event, payload)（缓冲通道，满则丢——与服务契约「非阻塞」一致）。
type emitter struct {
	ch chan emEv
}

type emEv struct {
	Name    string
	Payload any
}

func newEmitter() *emitter { return &emitter{ch: make(chan emEv, 1024)} }

func (e *emitter) emit(name string, payload any) {
	select {
	case e.ch <- emEv{Name: name, Payload: payload}:
	default:
	}
}

// waitEvent  draining 等待名字匹配且 pred 为真的事件。
func (e *emitter) waitEvent(t *testing.T, name string, timeout time.Duration,
	pred func(any) bool) emEv {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-e.ch:
			if ev.Name == name && (pred == nil || pred(ev.Payload)) {
				return ev
			}
		case <-deadline:
			t.Fatalf("等待事件 %s 超时", name)
		}
	}
}

// collect 把当前已到达的事件全部取出（用于「之后不再有事件」断言）。
func (e *emitter) collect() []emEv {
	var out []emEv
	for {
		select {
		case ev := <-e.ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}

// ---------------------------------------------------------------- 集成用例
// 用真实 *mpv.Player（默认 Launcher）+ net.Pipe 假后端跑一遍完整链路：
// Open → 属性驱动 tick/弹幕 → 断开自动重建 → 新实例 file-loaded 后续播。

func TestIntegrationRealPlayerOverFakeIPC(t *testing.T) {
	fb := &fakeBackend{}
	src := &fakeSource{def: items(3, 5000)}
	em := newEmitter()
	svc := player.New(player.Config{
		Resolver: resolverFunc(func(_ context.Context, _ player.Request) (player.Plan, error) {
			return fixedPlan(), nil
		}),
		Backend:       fb,
		MpvPath:       "fake-mpv", // 真实内核 Start 只查非空，假后端忽略路径
		DanmakuSource: src,
		Emit:          em.emit,
		AssDir:        t.TempDir(),
		TickInterval:  10 * time.Millisecond,
		FetcherOpts: danmaku.FetcherOptions{MinGap: 2 * time.Millisecond,
			RetryDelays: []time.Duration{2 * time.Millisecond, 4 * time.Millisecond}},
	})
	defer func() { _ = svc.Close() }()

	if err := svc.Open(context.Background(), openReq(1)); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool {
		s := fb.lastServer()
		return s != nil && s.hasLoadfile()
	}, "真实内核经假 IPC 送达 loadfile")
	s0 := fb.lastServer()
	s0.PushProperty("duration", 120.0)
	s0.PushProperty("time-pos", 5.0)

	em.waitEvent(t, "danmaku:stats", 3*time.Second, func(p any) bool {
		m, _ := p.(map[string]any)
		return m["count"] == 3
	})
	waitFor(t, 3*time.Second, func() bool {
		for _, c := range s0.Cmds() {
			if c.Name == "sub-add" {
				return true
			}
		}
		return false
	}, "sub-add 送达 IPC")

	// 断开 → 自动重建：新 fakeServer、新 loadfile、旧实例收到 quit
	s0.PushProperty("time-pos", 60.0)
	s0.CloseEvents()
	waitFor(t, 5*time.Second, func() bool { return fb.serverCount() == 2 }, "断线自动重建")
	s1 := fb.lastServer()
	waitFor(t, 5*time.Second, func() bool { return s1.hasLoadfile() }, "新实例装载")
	waitFor(t, 5*time.Second, func() bool {
		for _, c := range s0.Cmds() {
			if c.Name == "quit" {
				return true
			}
		}
		return false
	}, "旧实例被 quit")
	// fakeServer 对 loadfile 自动补推 file-loaded → 服务应在新实例上发续播 seek
	waitFor(t, 5*time.Second, func() bool {
		for _, c := range s1.Cmds() {
			if c.Name == "seek" {
				return true
			}
		}
		return false
	}, "file-loaded 后续播 seek")
}
