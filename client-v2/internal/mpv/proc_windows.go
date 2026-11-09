//go:build windows

package mpv

import "syscall"

// terminate Windows 无信号语义：OpenProcess(PROCESS_TERMINATE) +
// TerminateProcess（v1 quit 兜底同款路径）。失败让上层回落到 Kill。
func (p *osProcess) terminate() error {
	handle, err := syscall.OpenProcess(syscall.PROCESS_TERMINATE, false, uint32(p.cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)
	return syscall.TerminateProcess(handle, 1)
}
