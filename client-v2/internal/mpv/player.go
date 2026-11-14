// Package mpv 是 v2 播放器的 mpv 控制核心（毕业自 spike/mpvembed）：
// 负责 mpv 进程生命周期 + JSON IPC 命令面 + 属性事件泵，不含任何 GUI。
//
// 与 v1 zhenguojian/app/zhenguojian/player.py MpvSession 的行为基线对齐
// （启动参数、loadfile 前 set_property 传头、三种 seek、quit 宽限），
// 但通道形态按 V2-3 定案收敛为两条长连接：
//   - 命令连接：requestID 配对同步收发（复用 spike 读写器）；超时后
//     丢弃重拨——对应 v1「mpv.exe 管道 handler 会卡死」教训的另一半
//     （v1 靠每条命令一次性连接规避，v2 用超时重拨，真机若仍卡再降级）；
//   - 事件连接：observe_property 长连接。v1 observe() 的实测教训：
//     mpv 只把 property-change 发给注册观察的那条连接，且各属性必须用
//     不同 id，否则互相覆盖——所以事件连接独立、id 固定 1..6。
package mpv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNotRunning：mpv 没在跑/已断开时调用命令面。
var ErrNotRunning = errors.New("mpv: 播放器未运行")

// Config 播放器构造参数。零值字段走 v1 对齐的默认。
type Config struct {
	// MpvPath mpv 可执行文件；空则 DefaultMpvPath(AppRoot)。
	MpvPath string
	// AppRoot 应用根（打包后的 exe 目录语义），供 runtime/mpv/mpv.exe 兜底。
	AppRoot string
	// SocketPath IPC 地址；空则 DefaultSocketPath()。Windows 命名管道
	// "\\.\pipe\..."，Linux/测试 unix socket 或后端自定义方案。
	SocketPath string
	// Wid 宿主窗口句柄（HWND 数值）。0 = 不内嵌（v1 --wid 缺省语义）。
	Wid uintptr
	// HWDec true → --hwdec=auto-safe，false → no（v1 tools.hwdec_enabled）。
	HWDec bool
	// LogFile mpv --log-file；空则不传（v1 传 data 目录下日志尾部供诊断）。
	LogFile string
	// ExtraArgs 追加到启动参数尾部（联调用）。
	ExtraArgs []string
	// Backend 进程/连接后端；nil = 平台默认（Linux 单测注入 fake）。
	Backend Backend
	// CommandTimeout 单条命令超时（含 get_property 兜底）。默认 2s；
	// v1 对应值：get 0.5s、quit 1.0s（各自内部特判）。
	CommandTimeout time.Duration
	// PipeWait spawn 后等待 IPC 服务器就绪的最长时间。默认 20s
	// （spike TODO：Windows 真机首次握手耗时待测，先按 v1 量级放宽）。
	PipeWait time.Duration
}

// Process 是后端返回的 mpv 进程句柄（os/exec 包装，可 fake）。
type Process interface {
	Wait() error
	Terminate() error
	Kill() error
	PID() int
	// ErrTail stderr 尾部（诊断用，v1 _why_died 的素材）。
	ErrTail() string
}

// Backend 收拢平台差异：spawn 与拨号。win32 调用只在
// backend_windows.go 出现；Linux 用 backend_unix.go 或测试 fake。
type Backend interface {
	Launch(ctx context.Context, exePath string, args []string, wid uintptr) (Process, error)
	Dial(ctx context.Context, socketPath string) (Conn, error)
}

// EventKind 事件类别。
type EventKind string

const (
	EventProperty     EventKind = "property"     // 观察属性更新（Name 带属性名）
	EventFileLoaded   EventKind = "file-loaded"  // 新文件就绪（可挂 ASS）
	EventEndFile      EventKind = "end-file"     // 文件结束（State.EofReached 区分 eof/替换）
	EventDisconnected EventKind = "disconnected" // 事件连接断掉（止损点）
	EventExit         EventKind = "exit"         // mpv 进程退出（Err 带 why_died）
)

// ErrGracefulShutdown 标记"mpv 自己发了 shutdown 事件"的退出：
// 用户点视频窗的 X、或我们下发 quit，都走这条路。
//
// 第五十九轮（真机反馈）加这个哨兵的原因：服务层原来只认
// "quitting/closed 才算用户退出"，于是**用户从 mpv 窗口自己点关闭**
// 被当成意外死亡 → 自动重建 → 窗刚关掉又弹出来
// （用户原话：「关闭又会被二次打开」）。
// v1 根本没有死亡重建（player.py grep rebuild/respawn 零命中），
// 这条是 v2 自加的行为，真机上直接成了 bug。
// 有类型化信号才能把"体面关窗"和"真崩了"分开，而不是靠猜。
var ErrGracefulShutdown = errors.New("mpv: 收到 shutdown 事件")

// Event 是发布给服务层的事件；State 是更新后的完整快照。
type Event struct {
	Kind  EventKind
	Name  string // property 事件的属性名
	State State
	Err   error
}

// State 播放状态快照。
type State struct {
	Position   float64 // 秒
	Duration   float64 // 秒
	Paused     bool
	EofReached bool
	Speed      float64
	Volume     int
	FileLoaded bool
	Title      string
}

// observe 表：id 必须互不相同（v1 observe() 注释里的 mpv 覆盖坑）。
var observedProperties = []string{"time-pos", "duration", "pause", "eof-reached", "speed", "volume"}

// Player mpv 播放器客户端。生命周期：New → Start(ctx) → 命令面 → Quit。
// 并发：命令可由多 goroutine 调用（IPC 内部序列化）；事件只读 Events()。
type Player struct {
	cfg Config
	be  Backend

	socket string

	mu      sync.Mutex
	ipc     *IPC
	proc    Process
	st      State
	stopped bool

	events   chan Event
	alive    atomic.Bool
	eventsIP *atomic.Pointer[IPC] // 事件连接（泵持有）
	seq      atomic.Uint64

	closeOnce sync.Once
}

// New 构造播放器（不 spawn）。
func New(cfg Config) *Player {
	if cfg.Backend == nil {
		cfg.Backend = DefaultBackend()
	}
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = 2 * time.Second
	}
	if cfg.PipeWait <= 0 {
		cfg.PipeWait = 20 * time.Second
	}
	p := &Player{
		cfg:      cfg,
		be:       cfg.Backend,
		events:   make(chan Event, 64),
		eventsIP: &atomic.Pointer[IPC]{},
	}
	p.st.Speed = 1.0
	return p
}

// SocketPath 实际使用的 IPC 地址（Start 后可读）。
func (p *Player) SocketPath() string { return p.socket }

// Start 拉起 mpv（--idle，无初始文件）并建立两条 IPC 连接。
func (p *Player) Start(ctx context.Context) error {
	if p.alive.Load() {
		return errors.New("mpv: 已启动")
	}
	exe := p.cfg.MpvPath
	if exe == "" {
		exe = DefaultMpvPath(p.cfg.AppRoot)
	}
	if exe == "" {
		return errors.New("mpv: 找不到 mpv 可执行文件（runtime/mpv/mpv.exe、ZHENGUO_MPV、PATH 均未命中）")
	}
	p.socket = p.cfg.SocketPath
	if p.socket == "" {
		p.socket = DefaultSocketPath(p.seq.Add(1))
	}

	launchCtx, cancelLaunch := context.WithTimeout(ctx, 30*time.Second)
	defer cancelLaunch()
	proc, err := p.be.Launch(launchCtx, exe, p.buildArgs(), p.cfg.Wid)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.proc = proc
	p.mu.Unlock()

	// 命令连接 + 事件连接（两拨；后端负责握手重试）
	dialCtx, cancelDial := context.WithTimeout(ctx, p.cfg.PipeWait)
	defer cancelDial()
	cmdConn, err := p.be.Dial(dialCtx, p.socket)
	if err != nil {
		_ = proc.Kill()
		_ = proc.Wait()
		return fmt.Errorf("mpv: IPC 连接失败: %w（stderr: %s）", err, proc.ErrTail())
	}
	evConn, err := p.be.Dial(dialCtx, p.socket)
	if err != nil {
		_ = cmdConn.Close()
		_ = proc.Kill()
		_ = proc.Wait()
		return fmt.Errorf("mpv: 事件连接失败: %w", err)
	}
	p.mu.Lock()
	p.ipc = NewIPC(cmdConn)
	p.mu.Unlock()
	p.eventsIP.Store(NewIPC(evConn))

	p.alive.Store(true)
	go p.processWatch(proc)

	// 事件泵先跑起来，再注册观察（observe ack 会经泵丢弃，不阻塞）
	go p.eventPump(p.eventsIP.Load())
	for idx, name := range observedProperties {
		// v1: {"command":["observe_property", index, name]}，长连接上无 requestID 等待
		if err := p.sendOnEvents(fmt.Sprintf(`{"command":["observe_property",%d,"%s"]}`, idx+1, name)); err != nil {
			p.markDisconnected(err)
			return fmt.Errorf("mpv: 注册观察 %s 失败: %w", name, err)
		}
	}
	return nil
}

func (p *Player) buildArgs() []string {
	hwdec := "no"
	if p.cfg.HWDec {
		hwdec = "auto-safe"
	}
	args := []string{
		// --no-osc：任务定案；其余逐项 = v1 player.py:376-389
		"--no-config", "--no-terminal", "--idle=yes", "--no-osc", "--force-window=no",
		"--keep-open=yes",
		"--hwdec=" + hwdec,
		"--video-sync=display-resample",
		"--network-timeout=30", "--cache=yes", "--cache-secs=60",
		"--demuxer-lavf-o=" + strings.Join(lavfOptions(false, ""), ","),
		"--input-ipc-server=" + p.socket,
	}
	if p.cfg.LogFile != "" {
		args = append(args, "--log-file="+p.cfg.LogFile)
	}
	if p.cfg.Wid != 0 {
		args = append(args, fmt.Sprintf("--wid=%d", p.cfg.Wid))
	}
	args = append(args, p.cfg.ExtraArgs...)
	return args
}

// lavfOptions v1 load/_launch 的 --demuxer-lavf-o 选项组逐字对齐。
func lavfOptions(local bool, decryptionKey string) []string {
	protocols := "protocol_whitelist=[http,https,tcp,tls,crypto,data,file]"
	if local {
		protocols = "protocol_whitelist=[file,crypto,data]"
	}
	options := []string{"seg_max_retry=5", "allowed_extensions=ALL", "strict=experimental", protocols}
	if decryptionKey != "" {
		options = append(options, "decryption_key="+decryptionKey)
	}
	return options
}

// LoadOptions Load 的可选参数（v1 load 的解密 key / 本地文件标志）。
type LoadOptions struct {
	DecryptionKey string
	// Local 本地成品文件：protocol_whitelist 收窄（v1 local 语义）。
	Local bool
}

// Load 切换播放源：v1 语义——loadfile 之前先 set_property
// demuxer-lavf-o 与 http-header-fields（逐文件选项只在 load 时刻生效），
// 然后 ["loadfile", url, "replace"]。headers 序列化 = v1 的
// "K: V" 逗号拼接。title 仅存状态供事件标注。
func (p *Player) Load(url string, headers map[string]string, title string) error {
	return p.LoadWith(url, headers, title, LoadOptions{})
}

func (p *Player) LoadWith(url string, headers map[string]string, title string, opts LoadOptions) error {
	p.mu.Lock()
	p.st.FileLoaded = false
	p.st.EofReached = false
	p.st.Position = 0
	p.st.Duration = 0
	p.st.Title = title
	p.mu.Unlock()

	if err := p.SetProperty("demuxer-lavf-o", strings.Join(lavfOptions(opts.Local, opts.DecryptionKey), ",")); err != nil {
		return err
	}
	hdr := ""
	if len(headers) > 0 {
		parts := make([]string, 0, len(headers))
		for k, v := range headers {
			parts = append(parts, fmt.Sprintf("%s: %s", k, v))
		}
		hdr = strings.Join(parts, ",")
	}
	if err := p.SetProperty("http-header-fields", hdr); err != nil {
		return err
	}
	return p.Command("loadfile", url, "replace")
}

// Play / Pause：v1 set_property pause（UI 侧意图即刻更新——mpv 的 pause
// 属性回落可滞后 80ms，v1 用意图值防止快速连点被吞）。
func (p *Player) Play() error  { return p.setPausedIntent(false) }
func (p *Player) Pause() error { return p.setPausedIntent(true) }

func (p *Player) setPausedIntent(paused bool) error {
	if err := p.SetProperty("pause", paused); err != nil {
		return err
	}
	p.mu.Lock()
	p.st.Paused = paused
	p.mu.Unlock()
	p.publish(Event{Kind: EventProperty, Name: "pause", State: p.State()})
	return nil
}

// Seek 相对跳转秒（v1 seek_relative：["seek", s, "relative"]）。
func (p *Player) Seek(seconds float64) error {
	return p.Command("seek", seconds, "relative")
}

// AbsSetPosition 绝对秒 + 精确帧（v1 进度条语义：["seek", s, "absolute+exact"]）。
func (p *Player) AbsSetPosition(seconds float64) error {
	return p.Command("seek", seconds, "absolute+exact")
}

// AbsSeekSmooth 绝对秒、允许取整关键帧（v1 续播 resume 语义）。
func (p *Player) AbsSeekSmooth(seconds float64) error {
	return p.Command("seek", seconds, "absolute")
}

// GetPosition/GetDuration：观察缓存优先，兜底 get_property（v1 get()）。
func (p *Player) GetPosition() (float64, error) {
	p.mu.Lock()
	pos, loaded := p.st.Position, p.st.FileLoaded
	p.mu.Unlock()
	if loaded {
		return pos, nil
	}
	return p.getPropertyFloat("time-pos")
}

func (p *Player) GetDuration() (float64, error) {
	p.mu.Lock()
	dur, loaded := p.st.Duration, p.st.FileLoaded
	p.mu.Unlock()
	if loaded {
		return dur, nil
	}
	return p.getPropertyFloat("duration")
}

func (p *Player) GetSpeed() (float64, error) {
	p.mu.Lock()
	v := p.st.Speed
	p.mu.Unlock()
	return v, nil
}

func (p *Player) SetSpeed(rate float64) error {
	return p.SetProperty("speed", rate)
}

func (p *Player) GetVolume() (int, error) {
	p.mu.Lock()
	v := p.st.Volume
	p.mu.Unlock()
	return v, nil
}

func (p *Player) SetVolume(level int) error {
	return p.SetProperty("volume", level)
}

// State 快照。
func (p *Player) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.st
}

// Alive 进程与事件连接都健在才算 true（命令面还会独立自愈重拨）。
func (p *Player) Alive() bool { return p.alive.Load() }

// Events 事件流（缓冲 64，满时丢弃最旧，永不阻塞泵）。
func (p *Player) Events() <-chan Event { return p.events }

func (p *Player) publish(ev Event) {
	ev.State = p.State()
	for {
		select {
		case p.events <- ev:
			return
		default:
			select {
			case <-p.events: // 丢最旧
			default:
			}
		}
	}
}

// SetProperty v1 set_property（异步入队 → 这里同步等 ack）。
func (p *Player) SetProperty(name string, value any) error {
	_, err := p.call("set_property", name, value)
	return err
}

// GetProperty 原始取值。
func (p *Player) GetProperty(name string) (json.RawMessage, error) {
	resp, err := p.call("get_property", name)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (p *Player) getPropertyFloat(name string) (float64, error) {
	data, err := p.GetProperty(name)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 || string(data) == "null" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(string(data), 64)
	if err != nil {
		return 0, fmt.Errorf("mpv: %s 非数值: %s", name, data)
	}
	return v, nil
}

// Command 通用命令透传（服务层用它做 screenshot-to-file 等）。
func (p *Player) Command(args ...any) error {
	_, err := p.call(args...)
	return err
}

// CommandResponse 透传并拿 data（如 get 非常规属性）。
func (p *Player) CommandResponse(args ...any) (Response, error) {
	return p.call(args...)
}

// ---- 字幕轨（弹幕 ASS 链路，v1 命令逐字对齐） ----

// SubAdd v1 ["sub-add", path, "select", "弹幕"]。
func (p *Player) SubAdd(path, title string) error {
	if title == "" {
		title = "弹幕"
	}
	return p.Command("sub-add", path, "select", title)
}

// SubReload v1 ["sub-reload"]——同文件原地重写后用。
func (p *Player) SubReload() error { return p.Command("sub-reload") }

// SubRemove v1 ["sub-remove"]。
func (p *Player) SubRemove() error { return p.Command("sub-remove") }

// SetSubVisibility v1 set_property sub-visibility "yes"/"no"。
func (p *Player) SetSubVisibility(on bool) error {
	v := "no"
	if on {
		v = "yes"
	}
	return p.SetProperty("sub-visibility", v)
}

// Quit v1：quit 命令（1s 宽限）→ 等进程 → 兜底 kill。幂等。
func (p *Player) Quit(ctx context.Context) error {
	p.mu.Lock()
	p.stopped = true
	proc := p.proc
	p.mu.Unlock()

	if ipc := p.ipcHandle(); ipc != nil {
		quitCtx, cancel := context.WithTimeout(ctx, time.Second)
		done := make(chan struct{})
		go func() {
			_, _ = ipc.SendCommand([]any{"quit"})
			close(done)
		}()
		select {
		case <-done:
		case <-quitCtx.Done():
		}
		cancel()
	}
	if proc != nil {
		_ = proc.Terminate()
		waited := make(chan error, 1)
		go func() { waited <- p.waitProc(proc) }()
		select {
		case <-waited:
		case <-time.After(2 * time.Second):
			_ = proc.Kill()
		}
	}
	p.teardown(nil)
	return nil
}

// Close 释放连接（进程若还活着先 Quit）。
func (p *Player) Close() error {
	if p.Alive() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return p.Quit(ctx)
	}
	p.teardown(nil)
	return nil
}

// procWaitHook 单测注入点（export_test 引用）：包装 Wait 以便 fake 控制时序。
var procWaitHook func(Process) error

func (p *Player) waitProc(proc Process) error {
	if procWaitHook != nil {
		return procWaitHook(proc)
	}
	return proc.Wait()
}

// ---- 内部 ----

func (p *Player) ipcHandle() *IPC {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ipc
}

// call 带超时的同步命令；超时视为命令连接坏死：关闭并标记重拨
// （v1「wedged mpv 拖慢命令但不冻结界面」的等价防线）。
func (p *Player) call(args ...any) (Response, error) {
	if p.closed() {
		return Response{}, ErrNotRunning
	}
	// 事件通道断开 = 止损：状态新鲜度没了保障，拒绝继续操作，
	// 由服务层按 EventDisconnected 重建 Player（比半死不活更诚实）。
	if !p.alive.Load() {
		return Response{}, ErrNotRunning
	}
	ipc := p.ensureIPC()
	if ipc == nil {
		return Response{}, ErrNotRunning
	}
	type result struct {
		resp Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		r, e := ipc.SendCommand(args)
		ch <- result{r, e}
	}()
	timer := time.NewTimer(p.cfg.CommandTimeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.resp, r.err
	case <-timer.C:
		_ = ipc.Close()
		p.resetIPC(ipc)
		return Response{}, fmt.Errorf("mpv: 命令超时: %v", args[0])
	}
}

// ensureIPC 命令连接惰性重建（mpv 的管道服务器支持重复 accept；
// Windows 命名管道每次 CreateFileW 得到新实例，与 v1 一次性连接等价）。
func (p *Player) ensureIPC() *IPC {
	p.mu.Lock()
	if p.ipc != nil && !p.stopped {
		ipc := p.ipc
		p.mu.Unlock()
		return ipc
	}
	p.mu.Unlock()
	if p.stopped || !p.alive.Load() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.PipeWait)
	defer cancel()
	conn, err := p.be.Dial(ctx, p.socket)
	if err != nil {
		return nil
	}
	ipc := NewIPC(conn)
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		_ = conn.Close()
		return nil
	}
	p.ipc = ipc
	p.mu.Unlock()
	return ipc
}

func (p *Player) resetIPC(dead *IPC) {
	p.mu.Lock()
	if p.ipc == dead {
		_ = p.ipc.Close()
		p.ipc = nil
	}
	p.mu.Unlock()
}

// sendOnEvents 事件连接写原始行（observe 注册用），读侧由泵消化 ack。
func (p *Player) sendOnEvents(raw string) error {
	ipc := p.eventsIP.Load()
	if ipc == nil {
		return ErrNotRunning
	}
	if _, err := ipc.conn.Write([]byte(raw + "\n")); err != nil {
		return err
	}
	return nil
}

// eventPump 事件连接读循环：property-change / file-loaded / end-file。
// 读到错误 = 断开止损点：alive 置 false 并发布 EventDisconnected。
func (p *Player) eventPump(ipc *IPC) {
	reader := bufio.NewReader(ipc.conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if !p.closed() {
				p.markDisconnected(fmt.Errorf("mpv: 事件通道断开: %w", err))
			}
			return
		}
		ev, ok := parseEventLine(line)
		if !ok {
			continue
		}
		switch ev.Event {
		case "property-change":
			p.applyProperty(ev.Name, ev.Data)
			p.publish(Event{Kind: EventProperty, Name: ev.Name})
		case "file-loaded":
			p.mu.Lock()
			p.st.FileLoaded = true
			p.mu.Unlock()
			p.publish(Event{Kind: EventFileLoaded})
		case "end-file":
			p.mu.Lock()
			if ev.Reason == "eof" {
				p.st.EofReached = true
			}
			p.mu.Unlock()
			p.publish(Event{Kind: EventEndFile, Name: ev.Reason})
		case "shutdown":
			p.publish(Event{Kind: EventExit, Err: ErrGracefulShutdown})
		}
	}
}

func (p *Player) applyProperty(name string, data json.RawMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch name {
	case "time-pos":
		if f, ok := jsonFloat(data); ok {
			p.st.Position = f
		}
	case "duration":
		if f, ok := jsonFloat(data); ok {
			p.st.Duration = f
		}
	case "pause":
		var b bool
		if json.Unmarshal(data, &b) == nil {
			p.st.Paused = b
		}
	case "eof-reached":
		var b bool
		if json.Unmarshal(data, &b) == nil {
			p.st.EofReached = b
		}
	case "speed":
		if f, ok := jsonFloat(data); ok {
			p.st.Speed = f
		}
	case "volume":
		if f, ok := jsonFloat(data); ok {
			p.st.Volume = int(f)
		}
	}
}

func jsonFloat(data json.RawMessage) (float64, bool) {
	if len(data) == 0 || string(data) == "null" {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(data, &f); err != nil {
		return 0, false
	}
	return f, true
}

func (p *Player) markDisconnected(err error) {
	p.alive.Store(false)
	p.publish(Event{Kind: EventDisconnected, Err: err})
}

func (p *Player) processWatch(proc Process) {
	err := p.waitProc(proc)
	if p.closed() {
		return
	}
	why := err
	if tail := proc.ErrTail(); tail != "" {
		// 组装 v1 _why_died 语义的人话：stderr 尾部比退出码有用
		why = fmt.Errorf("%w：%s", err, tail)
	}
	p.alive.Store(false)
	p.publish(Event{Kind: EventExit, Err: why})
}

func (p *Player) closed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopped
}

func (p *Player) teardown(_ error) {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.stopped = true
		ipc := p.ipc
		p.ipc = nil
		p.mu.Unlock()
		p.alive.Store(false)
		if ipc != nil {
			_ = ipc.Close()
		}
		if ev := p.eventsIP.Swap(nil); ev != nil {
			_ = ev.Close()
		}
	})
}

// ---- 路径与套接字默认（v1 tools.mpv_path / pipe_name 语义） ----

// DefaultMpvPath 兜底链 = v1 tools.mpv_path：
// runtime/mpv/mpv.exe（AppRoot 下）→ ZHENGUO_MPV → PATH。空串=没找到。
func DefaultMpvPath(appRoot string) string {
	exe := "mpv"
	if runtime.GOOS == "windows" {
		exe = "mpv.exe"
	}
	if appRoot != "" {
		if cand := filepath.Join(appRoot, "runtime", "mpv", exe); fileExists(cand) {
			return cand
		}
	}
	if env := os.Getenv("ZHENGUO_MPV"); env != "" && fileExists(env) {
		return env
	}
	if found, err := exec.LookPath(exe); err == nil {
		return found
	}
	if found, err := exec.LookPath("mpv"); err == nil {
		return found
	}
	return ""
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// DefaultSocketPath v1 pipe 名生成语义（pid + 时间序号）：
// Windows \\.\pipe\zgj-mpv-<pid>-<seq>；其它系统用临时目录 unix socket。
func DefaultSocketPath(seq uint64) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`\\.\pipe\zgj-mpv-%d-%d`, os.Getpid(), seq%1_000_000)
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("zgj-mpv-%d-%d.sock", os.Getpid(), seq))
}
