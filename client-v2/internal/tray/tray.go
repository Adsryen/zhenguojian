// Package tray 实现 Windows 系统托盘（通知区域）图标与菜单动作分发。
//
// 选型说明：Wails v2.10.1 的托盘能力在其 windows 内部包中未导出
// （pkg/internal/frontend/desktop/windows 不对外），Options 也无 Tray 字段，
// 所以这里直接走 Win32 Shell_NotifyIconW 自建（见 tray_windows.go）。
// 本文件只放平台无关的分发机制：菜单/鼠标命令 → channel → 独立 goroutine
// 调注入回调，保证 WndProc（消息循环线程）永不被动作阻塞。
package tray

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// 命令 ID：托盘菜单项与鼠标消息共用的动作编号。
// WM_COMMAND 分发时取 wParam 低 16 位；鼠标消息在 tray_windows.go 映射。
const (
	CmdShow  = 1001 // 显示主窗口
	CmdCheck = 1002 // 检查更新
	CmdQuit  = 1003 // 退出程序
)

// 原生菜单/提示文本 —— 与 frontend/src/services/i18n.ts 的 tray.* 键组保持一致。
// 原生托盘读不到前端翻译，当前固定中文（遗留风险见 verification/tray_notes.md）。
const (
	AppName        = "真果鉴"
	MenuLabelShow  = "显示真果鉴"
	MenuLabelCheck = "检查更新"
	MenuLabelQuit  = "退出程序"
)

// ErrUnsupported 非 Windows 平台注册失败的标准错误（Start 原样返回）。
var ErrUnsupported = errors.New("当前平台不支持托盘")

// ErrAlreadyRunning 重复 Start 的幂等哨兵（双实例互踩保护）。
var ErrAlreadyRunning = errors.New("tray: 已注册，忽略重复 Start")

// Actions 托盘 → 壳动作的注入回调。允许长阻塞（网络检查等）：
// dispatcher 每条命令都另起 goroutine 调用，回调实现无需再自行转线程。
type Actions struct {
	Restore     func()
	CheckUpdate func()
	Quit        func()
}

// backend 平台后端：Windows 真实现（tray_windows.go），其余 stub（tray_other.go）。
type backend interface {
	// Register 注册托盘并开始分发；h.dispatch 由此后端在消息线程回调。
	Register(h *Handler) error
	// Unregister 注销托盘并停止消息泵。
	Unregister() error
}

const queueCapacity = 16

// Handler 托盘句柄：全进程单实例（New 一次、Start 一次）。
type Handler struct {
	acts Actions
	b    backend
	cmds chan int
	stop chan struct{}
	done chan struct{}

	mu      sync.Mutex
	started bool
	stopped bool
	status  string
}

// New 构造托盘句柄（不注册；Start 才碰平台 API）。
func New(acts Actions) *Handler {
	return &Handler{acts: acts, b: newBackend(), cmds: make(chan int, queueCapacity)}
}

// Start 注册托盘图标，幂等：重复调用返回 ErrAlreadyRunning，不会再注册一份。
// Stop 之后不允许重启（消息线程/分发 goroutine 生命周期只做一轮，退出在即无需复用）。
func (h *Handler) Start() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.started {
		return ErrAlreadyRunning
	}
	if h.stopped {
		return errors.New("tray: 已停止，不再重复注册")
	}
	if err := h.b.Register(h); err != nil {
		if errors.Is(err, ErrUnsupported) {
			h.status = "unsupported: " + err.Error()
		} else {
			h.status = "failed: " + err.Error()
		}
		return err
	}
	h.stop = make(chan struct{})
	h.done = make(chan struct{})
	go h.run()
	h.started = true
	h.status = "running"
	return nil
}

// Stop 注销托盘并等消息泵收尾（最多 3s；超时只记状态不卡关机）。
func (h *Handler) Stop() {
	h.mu.Lock()
	if !h.started {
		h.mu.Unlock()
		return
	}
	h.started = false
	h.stopped = true
	close(h.stop)
	done := h.done
	h.mu.Unlock()

	_ = h.b.Unregister()
	select {
	case <-done:
		h.setStatus("stopped")
	case <-time.After(3 * time.Second):
		h.setStatus("stopped (unregister timeout)")
	}
}

// Status 当前状态一行文本（running / unsupported:… / failed:… / stopped）。
// 绑定层 TrayStatus() 直接透传，供冒烟自检；前端不消费。
func (h *Handler) Status() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

func (h *Handler) setStatus(s string) {
	h.mu.Lock()
	h.status = s
	h.mu.Unlock()
}

// dispatch 供平台后端（WndProc 所在消息线程）投递命令：非阻塞。
// 队列满时丢弃新命令（托盘连点不至于堆积；用户可再点一次）。
func (h *Handler) dispatch(cmd int) {
	select {
	case h.cmds <- cmd:
	default:
	}
}

// run 分发循环：stop 关闭或命令到达时唤醒；每条命令独立 goroutine 执行。
func (h *Handler) run() {
	defer close(h.done)
	for {
		select {
		case <-h.stop:
			return
		case cmd := <-h.cmds:
			go h.invoke(cmd)
		}
	}
}

// invoke 命令 → 回调映射；回调 panic 吞掉并打日志（不能炸穿宿主进程）。
func (h *Handler) invoke(cmd int) {
	defer func() {
		if r := recover(); r != nil {
			println("[tray] 回调 panic: " + fmt.Sprint(r))
		}
	}()
	var fn func()
	switch cmd {
	case CmdShow:
		fn = h.acts.Restore
	case CmdCheck:
		fn = h.acts.CheckUpdate
	case CmdQuit:
		fn = h.acts.Quit
	default:
		return // 未知命令 ID：忽略
	}
	if fn != nil {
		fn()
	}
}
