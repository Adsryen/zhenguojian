package mpv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- fake mpv：net.Pipe 假应答器（Linux 单测全链路，无真进程） ----

type fakeProcess struct {
	dead  chan struct{}
	once  sync.Once
	tailS string
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{dead: make(chan struct{}), tailS: "mpv: fake stderr tail"}
}

func (f *fakeProcess) PID() int { return 4242 }
func (f *fakeProcess) Wait() error {
	<-f.dead
	return errors.New("exit status 1")
}
func (f *fakeProcess) Terminate() error { f.once.Do(func() { close(f.dead) }); return nil }
func (f *fakeProcess) Kill() error      { return f.Terminate() }
func (f *fakeProcess) ErrTail() string  { return f.tailS }

type observeReq struct {
	ID   int
	Name string
}

type fakeMPV struct {
	mu       sync.Mutex
	cmds     [][]any      // 命令连接收到的 command 数组，按序
	observes []observeReq // 事件连接收到的 observe_property
	propData map[string]any

	silentCmd bool // true：命令连接收而不回（模拟 wedged mpv）
	evConn    net.Conn
	dead      bool
}

func (f *fakeMPV) record(c []any) {
	f.mu.Lock()
	f.cmds = append(f.cmds, c)
	f.mu.Unlock()
}

func (f *fakeMPV) serveCmd(conn net.Conn, silent bool) {
	dec := json.NewDecoder(bufio.NewReader(conn))
	for {
		var req struct {
			Command   []any `json:"command"`
			RequestID *int  `json:"requestID"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		f.record(req.Command)
		if silent || req.RequestID == nil {
			continue
		}
		resp := map[string]any{"error": "success", "requestID": *req.RequestID}
		if len(req.Command) >= 2 && req.Command[0] == "get_property" {
			if name, ok := req.Command[1].(string); ok {
				f.mu.Lock()
				v, has := f.propData[name]
				f.mu.Unlock()
				if has {
					resp["data"] = v
				} else {
					resp["error"] = "property not found"
				}
			}
		}
		b, _ := json.Marshal(resp)
		if _, err := conn.Write(append(b, '\n')); err != nil {
			return
		}
	}
}

func (f *fakeMPV) serveEvents(conn net.Conn) {
	dec := json.NewDecoder(bufio.NewReader(conn))
	for {
		var req struct {
			Command []any `json:"command"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		if len(req.Command) == 3 && fmt.Sprint(req.Command[0]) == "observe_property" {
			id := int(req.Command[1].(float64))
			f.mu.Lock()
			f.observes = append(f.observes, observeReq{ID: id, Name: fmt.Sprint(req.Command[2])})
			f.mu.Unlock()
		}
	}
}

func (f *fakeMPV) pushEvent(line string) error {
	f.mu.Lock()
	conn := f.evConn
	dead := f.dead
	f.mu.Unlock()
	if conn == nil || dead {
		return errors.New("fake mpv: event conn closed")
	}
	_, err := conn.Write([]byte(line + "\n"))
	return err
}

func (f *fakeMPV) closeEvents() {
	f.mu.Lock()
	conn := f.evConn
	f.dead = true
	f.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (f *fakeMPV) waitObserves(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := len(f.observes)
		f.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("事件连接没收到 %d 条 observe_property 注册", n)
}

func (f *fakeMPV) waitCmds(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := len(f.cmds)
		f.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Fatalf("命令连接只收到 %d 条: %v", len(f.cmds), f.cmds)
}

type fakeBackend struct {
	srv       *fakeMPV
	proc      *fakeProcess
	dialCount int
	roles     []string // 每次 Dial 的角色（cmd/events），供断言
	silent1   bool     // 首条命令连接收而不回
}

func (b *fakeBackend) Launch(ctx context.Context, exe string, args []string, wid uintptr) (Process, error) {
	return b.proc, nil
}

func (b *fakeBackend) Dial(ctx context.Context, socket string) (Conn, error) {
	client, server := net.Pipe()
	b.dialCount++
	isEvents := b.dialCount == 2
	if isEvents {
		b.roles = append(b.roles, "events")
		b.srv.mu.Lock()
		b.srv.evConn = server
		b.srv.mu.Unlock()
		go b.srv.serveEvents(server)
		return client, nil
	}
	b.roles = append(b.roles, "cmd")
	silent := b.silent1 && b.dialCount == 1
	go b.srv.serveCmd(server, silent)
	return client, nil
}

func newTestPlayer(t *testing.T) (*Player, *fakeMPV, *fakeProcess) {
	t.Helper()
	srv := &fakeMPV{propData: map[string]any{"duration": float64(930), "time-pos": float64(3.5)}}
	proc := newFakeProcess()
	be := &fakeBackend{srv: srv, proc: proc}
	p := New(Config{
		MpvPath:    filepathJoinPlaceholder(),
		SocketPath: `\\.\pipe\zgj-mpv-test`,
		Backend:    be,
		PipeWait:   2 * time.Second,
		HWDec:      true,
		Wid:        12345,
	})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	srv.waitObserves(t, len(observedProperties))
	t.Cleanup(func() { _ = p.Close() })
	return p, srv, proc
}

func filepathJoinPlaceholder() string { return "/fake/mpv-mpv.exe" }

// ---- 测试 ----

// Start 必须在事件连接上按 distinct id 注册全部观察属性（v1 覆盖坑）。
func TestStartRegistersObservedProps(t *testing.T) {
	p, srv, _ := newTestPlayer(t)
	_ = p
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.observes) != len(observedProperties) {
		t.Fatalf("observes = %v", srv.observes)
	}
	seenID := map[int]bool{}
	for i, o := range srv.observes {
		if o.ID != i+1 || seenID[o.ID] {
			t.Fatalf("observe id 必须 1..N 且不重复: %v", srv.observes)
		}
		seenID[o.ID] = true
		if o.Name != observedProperties[i] {
			t.Fatalf("observe %d = %s, want %s", o.ID, o.Name, observedProperties[i])
		}
	}
}

// buildArgs 断言（Launch 参数即 v1 命令行对齐面）。
func TestLaunchArgsAligned(t *testing.T) {
	srv := &fakeMPV{propData: map[string]any{}}
	var gotArgs []string
	var gotWid uintptr
	be := &recordingBackend{fakeBackend: fakeBackend{srv: srv, proc: newFakeProcess()},
		onLaunch: func(exe string, args []string, wid uintptr) { gotArgs, gotWid = args, wid }}
	p := New(Config{MpvPath: "/fake/mpv", SocketPath: "test.sock", Backend: be, Wid: 0xdead, HWDec: true})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{
		"--no-config", "--idle=yes", "--no-osc", "--force-window=no", "--keep-open=yes",
		"--hwdec=auto-safe", "--video-sync=display-resample", "--network-timeout=30",
		"--cache=yes", "--cache-secs=60",
		"--demuxer-lavf-o=seg_max_retry=5,allowed_extensions=ALL,strict=experimental,protocol_whitelist=[http,https,tcp,tls,crypto,data,file]",
		"--input-ipc-server=test.sock", "--wid=57005",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("启动参数缺 %q；args=%v", want, gotArgs)
		}
	}
	if gotWid != 0xdead {
		t.Errorf("wid 透传 = %x", gotWid)
	}
}

type recordingBackend struct {
	fakeBackend
	onLaunch func(exe string, args []string, wid uintptr)
}

func (r *recordingBackend) Launch(ctx context.Context, exe string, args []string, wid uintptr) (Process, error) {
	if r.onLaunch != nil {
		r.onLaunch(exe, args, wid)
	}
	return r.fakeBackend.Launch(ctx, exe, args, wid)
}
func (r *recordingBackend) Dial(ctx context.Context, socket string) (Conn, error) {
	return r.fakeBackend.Dial(ctx, socket)
}

// Load 命令序列 = v1：set_property demuxer-lavf-o → set_property
// http-header-fields("K: V" 逗号拼) → loadfile url replace。
func TestLoadSequence(t *testing.T) {
	p, srv, _ := newTestPlayer(t)
	if err := p.Load("https://play.example/v.m3u8", map[string]string{"Referer": "https://www.mgtv.com/"}, "第1集"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	srv.waitCmds(t, 3)
	srv.mu.Lock()
	defer srv.mu.Unlock()
	want := []any{
		"set_property", "demuxer-lavf-o",
		"seg_max_retry=5,allowed_extensions=ALL,strict=experimental,protocol_whitelist=[http,https,tcp,tls,crypto,data,file]",
	}
	if fmt.Sprint(srv.cmds[0]) != fmt.Sprint(want) {
		t.Errorf("cmd0 = %v", srv.cmds[0])
	}
	if srv.cmds[1][0] != "set_property" || srv.cmds[1][1] != "http-header-fields" ||
		srv.cmds[1][2] != "Referer: https://www.mgtv.com/" {
		t.Errorf("headers cmd = %v", srv.cmds[1])
	}
	if srv.cmds[2][0] != "loadfile" || srv.cmds[2][1] != "https://play.example/v.m3u8" || srv.cmds[2][2] != "replace" {
		t.Errorf("loadfile cmd = %v", srv.cmds[2])
	}
	if p.State().Title != "第1集" {
		t.Errorf("title 未入状态")
	}
}

// 三种 seek 形态 = v1 的 relative / absolute+exact / absolute。
func TestSeekForms(t *testing.T) {
	p, srv, _ := newTestPlayer(t)
	if err := p.Seek(10); err != nil {
		t.Fatal(err)
	}
	if err := p.AbsSetPosition(20.5); err != nil {
		t.Fatal(err)
	}
	if err := p.AbsSeekSmooth(30); err != nil {
		t.Fatal(err)
	}
	srv.waitCmds(t, 3)
	srv.mu.Lock()
	defer srv.mu.Unlock()
	want := [][3]any{{"seek", 10.0, "relative"}, {"seek", 20.5, "absolute+exact"}, {"seek", 30.0, "absolute"}}
	for i, w := range want {
		if fmt.Sprint(srv.cmds[i]) != fmt.Sprint(w[:]) {
			t.Errorf("seek %d = %v, want %v", i, srv.cmds[i], w)
		}
	}
}

// 暂停走 set_property + 意图即刻生效 + property 事件发布。
func TestPauseIntentImmediate(t *testing.T) {
	p, srv, _ := newTestPlayer(t)
	if err := p.Pause(); err != nil {
		t.Fatal(err)
	}
	if !p.State().Paused {
		t.Error("Paused 意图未即刻更新")
	}
	ev := waitEvent(t, p, EventProperty)
	if ev.Name != "pause" || !ev.State.Paused {
		t.Errorf("事件 = %+v", ev)
	}
	if err := p.Play(); err != nil {
		t.Fatal(err)
	}
	if p.State().Paused {
		t.Error("Play 后仍 Paused")
	}
	srv.waitCmds(t, 2)
}

// 事件泵：property-change 更新状态、file-loaded/end-file(eof) 到位。
func TestEventPump(t *testing.T) {
	p, srv, _ := newTestPlayer(t)
	if err := srv.pushEvent(`{"event":"property-change","id":1,"name":"time-pos","data":12.5}`); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, p, EventProperty)
	if ev.Name != "time-pos" || ev.State.Position != 12.5 {
		t.Errorf("time-pos 事件 = %+v", ev)
	}
	// speed/volume 观察更新
	if err := srv.pushEvent(`{"event":"property-change","id":5,"name":"speed","data":1.25}`); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, p, EventProperty)
	if err := srv.pushEvent(`{"event":"property-change","id":6,"name":"volume","data":45}`); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, p, EventProperty)
	st := p.State()
	if st.Speed != 1.25 || st.Volume != 45 {
		t.Errorf("speed/volume = %+v", st)
	}

	if err := srv.pushEvent(`{"event":"file-loaded","requestID":0}`); err != nil {
		t.Fatal(err)
	}
	ev = waitEvent(t, p, EventFileLoaded)
	if !ev.State.FileLoaded {
		t.Errorf("file-loaded 未置位: %+v", ev.State)
	}
	if err := srv.pushEvent(`{"event":"end-file","reason":"eof"}`); err != nil {
		t.Fatal(err)
	}
	ev = waitEvent(t, p, EventEndFile)
	if ev.Name != "eof" || !ev.State.EofReached {
		t.Errorf("end-file = %+v", ev)
	}
}

func waitEvent(t *testing.T, p *Player, kind EventKind) Event {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case ev := <-p.Events():
			if ev.Kind == kind {
				return ev
			}
			// 其它事件继续等
		case <-timer.C:
			t.Fatalf("等待 %s 事件超时", kind)
		}
	}
}

// GetPosition/GetDuration：FileLoaded 前走 get_property 兜底（v1 get()）。
func TestGetPropertyFallback(t *testing.T) {
	p, srv, _ := newTestPlayer(t)
	_ = srv
	dur, err := p.GetDuration()
	if err != nil || dur != 930 {
		t.Errorf("GetDuration fallback = %v, %v", dur, err)
	}
	pos, err := p.GetPosition()
	if err != nil || pos != 3.5 {
		t.Errorf("GetPosition fallback = %v, %v", pos, err)
	}
}

// 命令连接 wedged → 超时重拨（v1 一次性连接教训的 v2 等价防线）。
func TestCommandTimeoutRedials(t *testing.T) {
	srv := &fakeMPV{propData: map[string]any{}}
	proc := newFakeProcess()
	be := &fakeBackend{srv: srv, proc: proc, silent1: true}
	p := New(Config{MpvPath: "/fake/mpv", SocketPath: "test.sock", Backend: be,
		CommandTimeout: 100 * time.Millisecond, PipeWait: time.Second})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	srv.waitObserves(t, len(observedProperties))
	defer p.Close()

	if err := p.SetVolume(30); err == nil || !strings.Contains(err.Error(), "命令超时") {
		t.Fatalf("首条命令应超时: %v", err)
	}
	if err := p.SetVolume(40); err != nil {
		t.Fatalf("重拨后应恢复: %v", err)
	}
	if be.dialCount < 3 {
		t.Errorf("应发生重拨, dialCount=%d", be.dialCount)
	}
	srv.waitCmds(t, 2)
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if fmt.Sprint(srv.cmds[1]) != fmt.Sprint([]any{"set_property", "volume", 40.0}) {
		t.Errorf("重拨命令 = %v", srv.cmds[1])
	}
}

// 事件通道断开 = 止损点：EventDisconnected + Alive false + 命令面拒收。
func TestEventDisconnectStops(t *testing.T) {
	p, srv, _ := newTestPlayer(t)
	srv.closeEvents()
	ev := waitEvent(t, p, EventDisconnected)
	if ev.Err == nil {
		t.Error("断开事件应带错误")
	}
	if p.Alive() {
		t.Error("断开后 Alive 应为 false")
	}
	if err := p.Seek(1); !errors.Is(err, ErrNotRunning) {
		t.Errorf("断开后命令面应拒收, got %v", err)
	}
}

// Quit：v1 quit 命令 + 终止进程 + 幂等。
func TestQuit(t *testing.T) {
	p, srv, proc := newTestPlayer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := p.Quit(ctx); err != nil {
		t.Fatalf("Quit: %v", err)
	}
	select {
	case <-proc.dead:
	case <-time.After(time.Second):
		t.Fatal("进程未被终止")
	}
	if p.Alive() {
		t.Error("Quit 后仍 Alive")
	}
	srv.waitCmds(t, 1)
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if fmt.Sprint(srv.cmds[0]) != fmt.Sprint([]any{"quit"}) {
		t.Errorf("quit 命令 = %v", srv.cmds[0])
	}
	if err := p.Quit(ctx); err != nil { // 幂等
		t.Errorf("二次 Quit = %v", err)
	}
}

// 字幕轨命令 = v1 逐字（sub-add select 弹幕 / sub-reload / sub-visibility）。
func TestSubtitleCommands(t *testing.T) {
	p, srv, _ := newTestPlayer(t)
	if err := p.SubAdd(`C:\Windows\Temp\zhenguojian-danmaku\ep1.ass`, ""); err != nil {
		t.Fatal(err)
	}
	if err := p.SetSubVisibility(true); err != nil {
		t.Fatal(err)
	}
	if err := p.SubReload(); err != nil {
		t.Fatal(err)
	}
	srv.waitCmds(t, 3)
	srv.mu.Lock()
	defer srv.mu.Unlock()
	want := []string{
		fmt.Sprint([]any{"sub-add", `C:\Windows\Temp\zhenguojian-danmaku\ep1.ass`, "select", "弹幕"}),
		fmt.Sprint([]any{"set_property", "sub-visibility", "yes"}),
		fmt.Sprint([]any{"sub-reload"}),
	}
	for i, w := range want {
		if fmt.Sprint(srv.cmds[i]) != w {
			t.Errorf("cmd %d = %v, want %v", i, srv.cmds[i], w)
		}
	}
}

// 进程退出 → EventExit 带 stderr 尾部（v1 _why_died 素材）。
func TestProcessExitEvent(t *testing.T) {
	p, _, proc := newTestPlayer(t)
	proc.Terminate() // 模拟 mpv 自己没了（非 Quit 路径）
	ev := waitEvent(t, p, EventExit)
	if ev.Err == nil || !strings.Contains(ev.Err.Error(), "fake stderr tail") {
		t.Errorf("exit 事件应带 why_died 尾部: %v", ev.Err)
	}
}
