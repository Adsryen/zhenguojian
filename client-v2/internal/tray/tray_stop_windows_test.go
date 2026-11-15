//go:build windows

package tray

// tray_stop_windows_test.go：winBackend.Unregister 的两个出口。
// hwnd 保持 0 → deleteIcon 直接 return、PostMessage 分支跳过，
// 所以这条只测"等多久 / 超时报不报"，不碰任何 Win32 调用。

import (
	"strings"
	"testing"
	"time"
)

func withUnregisterTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := unregisterTimeout
	unregisterTimeout = d
	t.Cleanup(func() { unregisterTimeout = old })
}

func TestWinBackendUnregisterTimesOut(t *testing.T) {
	withUnregisterTimeout(t, 20*time.Millisecond)
	b := &winBackend{done: make(chan struct{})} // 消息泵永不退出
	err := b.Unregister()
	if err == nil {
		t.Fatal("done 永不关闭时 Unregister 必须报错（app.go 的收尾日志靠它）")
	}
	if !strings.Contains(err.Error(), "消息线程") {
		t.Fatalf("错误文案漂了: %v", err)
	}
	if !strings.Contains(err.Error(), unregisterTimeout.String()) {
		t.Fatalf("错误里应当带上限本身，便于真机归因: %v", err)
	}
}

func TestWinBackendUnregisterHappyPath(t *testing.T) {
	withUnregisterTimeout(t, time.Second)
	b := &winBackend{done: make(chan struct{})}
	close(b.done)
	if err := b.Unregister(); err != nil {
		t.Fatalf("消息泵已退出时不该报错: %v", err)
	}
}
