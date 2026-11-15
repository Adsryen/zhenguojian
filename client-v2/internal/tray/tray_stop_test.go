package tray

// tray_stop_test.go：Stop() 的**错误出口**（本次改动的核心承诺之一）。
//
// 改前 Stop() 无返回值：winBackend.Unregister 的"消息线程 2s 未退出"被
// `_ = h.b.Unregister()` 吞在包内，退出路径上没人知道托盘到底走没走干净。
// 现在它把错误交出来，由调用方（app.go 的并发收尾）记日志；这两条测试
// 钉的就是"交出来"这件事本身，包括两个上限都算失败。

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// errBackend：只关心 Unregister 的返回值。
type errBackend struct{ unregErr error }

func (b *errBackend) Register(*Handler) error { return nil }
func (b *errBackend) Unregister() error       { return b.unregErr }

func withDispatchWait(t *testing.T, d time.Duration) {
	t.Helper()
	old := trayDispatchWait
	trayDispatchWait = d
	t.Cleanup(func() { trayDispatchWait = old })
}

// TestStopPropagatesUnregisterError 后端报错时 Stop 必须把同一个错误交出来
// （而不是报成 ok），并且状态仍落到 stopped——超时不卡关机这条不变。
func TestStopPropagatesUnregisterError(t *testing.T) {
	withDispatchWait(t, 2*time.Second)
	want := errors.New("tray: 消息线程未退出")
	h := &Handler{acts: Actions{}, b: &errBackend{unregErr: want}, cmds: make(chan int, queueCapacity)}
	if err := h.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	err := h.Stop()
	if !errors.Is(err, want) {
		t.Fatalf("Stop 没把 Unregister 的错误交出来，实得 %v", err)
	}
	if s := h.Status(); s != "stopped" {
		t.Fatalf("Status=%q, want stopped", s)
	}
}

// TestStopReportsStuckDispatcher 分发协程不退出 = 第二个上限，也必须报。
// done 由测试自己拿着、永不关闭，走的正是 trayDispatchWait 那条分支。
func TestStopReportsStuckDispatcher(t *testing.T) {
	withDispatchWait(t, 20*time.Millisecond)
	unregErr := errors.New("tray: 消息线程未退出")
	h := &Handler{
		acts:    Actions{},
		b:       &errBackend{unregErr: unregErr},
		cmds:    make(chan int, queueCapacity),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		started: true,
	}
	err := h.Stop()
	if err == nil {
		t.Fatal("分发协程卡住时 Stop 必须报错")
	}
	if !strings.Contains(err.Error(), "分发协程") {
		t.Fatalf("错误里没提分发协程: %v", err)
	}
	// 两个上限同时命中时两条都要在文案里：少一条就少一个可归因的方向。
	if !errors.Is(err, unregErr) || !strings.Contains(err.Error(), "消息线程") {
		t.Fatalf("Unregister 的错误被分发协程那条盖掉了: %v", err)
	}
	if s := h.Status(); s != "stopped (unregister timeout)" {
		t.Fatalf("Status=%q, want stopped (unregister timeout)", s)
	}
}

// TestStopUnstartedIsSilentlyNil 没 Start 过（非 Windows 降级、Start 失败）
// 时 Stop 是 no-op：不得凭空造一条错误出来淹掉真故障。
func TestStopUnstartedIsSilentlyNil(t *testing.T) {
	h := New(Actions{})
	if err := h.Stop(); err != nil {
		t.Fatalf("未注册的 Handler Stop 应返回 nil，实得 %v", err)
	}
}
