//go:build !windows

package mpv

import "syscall"

// terminate SIGTERM（mpv 对 TERM 做优雅退出）。
func (p *osProcess) terminate() error {
	return p.cmd.Process.Signal(syscall.SIGTERM)
}
