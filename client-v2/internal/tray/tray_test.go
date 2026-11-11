package tray

// tray_test.go：平台无关纯逻辑单测（回调分发 / 双实例幂等注册 / 非 Windows 降级）。
// Windows API 调用不进单测：用 stubBackend 注入，真机验证点见
// verification/tray_notes.md。

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubBackend 记录注册次数的假后端，替代 winBackend/stubBackend 的真实 API 行为。
type stubBackend struct {
	mu        sync.Mutex
	registers int
	unregs    int
	err       error
}

func (s *stubBackend) Register(*Handler) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.registers++
	return nil
}

func (s *stubBackend) Unregister() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unregs++
	return nil
}

func (s *stubBackend) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registers, s.unregs
}

// countingActions 三个回调各自计数，供分发断言。
type countingActions struct {
	mu                sync.Mutex
	show, check, quit int
}

func (c *countingActions) build() Actions {
	return Actions{
		Restore:     func() { c.mu.Lock(); c.show++; c.mu.Unlock() },
		CheckUpdate: func() { c.mu.Lock(); c.check++; c.mu.Unlock() },
		Quit:        func() { c.mu.Lock(); c.quit++; c.mu.Unlock() },
	}
}

func (c *countingActions) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.show + c.check + c.quit
}

func newWithStub(t *testing.T, acts Actions, b *stubBackend) *Handler {
	t.Helper()
	return &Handler{acts: acts, b: b, cmds: make(chan int, queueCapacity)}
}

func waitFor(t *testing.T, cond func() bool, why string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", why)
}

// TestDispatchRoutesCommandsToActions 命令 → 对应回调各归各位，未知命令被忽略。
func TestDispatchRoutesCommandsToActions(t *testing.T) {
	c := &countingActions{}
	b := &stubBackend{}
	h := newWithStub(t, c.build(), b)
	if err := h.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer h.Stop()

	h.dispatch(CmdShow)
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.show == 1
	}, "CmdShow → Restore")

	h.dispatch(CmdCheck)
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.check == 1
	}, "CmdCheck → CheckUpdate")

	h.dispatch(CmdQuit)
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.quit == 1
	}, "CmdQuit → Quit")

	h.dispatch(9999) // 未知命令：不得触发任何回调，也不得 panic
	time.Sleep(50 * time.Millisecond)
	if got := c.total(); got != 3 {
		t.Fatalf("未知命令后回调总数=%d, want 3", got)
	}
}

// TestNilCallbacksSafe 未注入回调时 dispatch 不 panic（stub 场景合法）。
func TestNilCallbacksSafe(t *testing.T) {
	h := newWithStub(t, Actions{}, &stubBackend{})
	if err := h.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.dispatch(CmdShow)
	h.dispatch(CmdCheck)
	h.dispatch(CmdQuit)
	time.Sleep(50 * time.Millisecond)
	h.Stop()
}

// TestIdempotentRegistration 双实例幂等：第二次 Start 返回 ErrAlreadyRunning
// 且底层 Register 只发生一次；Stop 后再 Start 被拒绝。
func TestIdempotentRegistration(t *testing.T) {
	c := &countingActions{}
	b := &stubBackend{}
	h := newWithStub(t, c.build(), b)

	if err := h.Start(); err != nil {
		t.Fatalf("首次 Start: %v", err)
	}
	if err := h.Start(); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("重复 Start err=%v, want ErrAlreadyRunning", err)
	}
	if regs, _ := b.counts(); regs != 1 {
		t.Fatalf("Register 次数=%d, want 1", regs)
	}
	if s := h.Status(); s != "running" {
		t.Fatalf("Status=%q, want running", s)
	}

	h.Stop()
	if _, unregs := b.counts(); unregs != 1 {
		t.Fatalf("Unregister 次数=%d, want 1", unregs)
	}
	if err := h.Start(); err == nil || !strings.Contains(err.Error(), "已停止") {
		t.Fatalf("Stop 后重启应拒绝, got %v", err)
	}
	if s := h.Status(); s != "stopped" {
		t.Fatalf("Status=%q, want stopped", s)
	}
}

// TestStartFailureStatus Register 失败（模拟）时状态带 failed 前缀且保持可查。
func TestStartFailureStatus(t *testing.T) {
	b := &stubBackend{err: errors.New("boom")}
	h := newWithStub(t, Actions{}, b)
	if err := h.Start(); err == nil {
		t.Fatal("want error")
	}
	if s := h.Status(); !strings.HasPrefix(s, "failed: ") {
		t.Fatalf("Status=%q, want failed: 前缀", s)
	}
}

// TestUnsupportedPlatformDegradation 非 Windows：New 走 stub 后端，
// Start 返回「当前平台不支持托盘」，Status 标记 unsupported，Stop 不炸。
// Windows 真机 CI 时跳过（真实 API 不进单测）。
func TestUnsupportedPlatformDegradation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 真机后端不在单测范围")
	}
	h := New(Actions{})
	err := h.Start()
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err=%v, want ErrUnsupported", err)
	}
	if err.Error() != "当前平台不支持托盘" {
		t.Fatalf("错误文案=%q", err.Error())
	}
	if s := h.Status(); !strings.HasPrefix(s, "unsupported: ") {
		t.Fatalf("Status=%q, want unsupported: 前缀", s)
	}
	h.Stop() // 未注册时 Stop 应为无操作
	if s := h.Status(); !strings.HasPrefix(s, "unsupported: ") {
		t.Fatalf("Stop 后 Status=%q，不应变化", s)
	}
}

// TestDispatchNonBlockingWhenQueueFull 队列满时 dispatch 丢弃而非阻塞
// （WndProc 线程绝对不能被卡）。
func TestDispatchNonBlockingWhenQueueFull(t *testing.T) {
	c := &countingActions{}
	b := &stubBackend{}
	h := newWithStub(t, c.build(), b)
	// 故意不 Start：无分发协程消费，队列必然堆满。
	done := make(chan struct{})
	go func() {
		for i := 0; i < queueCapacity*4; i++ {
			h.dispatch(CmdShow)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("dispatch 在队列满时阻塞")
	}
}

// TestCommandIDsStable 菜单 ID 契约：WM_COMMAND 按低 16 位分发，禁止漂移。
func TestCommandIDsStable(t *testing.T) {
	if CmdShow != 1001 || CmdCheck != 1002 || CmdQuit != 1003 {
		t.Fatalf("命令 ID 漂移: %d %d %d", CmdShow, CmdCheck, CmdQuit)
	}
}
