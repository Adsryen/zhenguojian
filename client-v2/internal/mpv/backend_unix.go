//go:build !windows

package mpv

import (
	"context"
	"errors"
	"net"
	"time"
)

type unixBackend struct{}

// DefaultBackend 非 Windows：普通 spawn + unix socket 拨号。
// Linux 本机跑 v2 壳或真机验证时用；单测一般注入 fake Backend。
func DefaultBackend() Backend { return unixBackend{} }

func (unixBackend) Launch(ctx context.Context, exePath string, args []string, wid uintptr) (Process, error) {
	return startProcess(exePath, args, nil)
}

// Dial 等待 mpv 的 unix socket 就绪（50ms 重试）。
func (unixBackend) Dial(ctx context.Context, socketPath string) (Conn, error) {
	backoff := time.NewTicker(50 * time.Millisecond)
	defer backoff.Stop()
	var lastErr error
	for {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, errors.New("mpv: socket 连接超时: " + lastErr.Error())
		case <-backoff.C:
		}
	}
}
