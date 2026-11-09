//go:build windows

package mpvembed

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// PipeName 是 mpv --input-ipc-server 用的命名管道（\\.\pipe\ 前缀）。
const PipeName = `\\.\pipe\zgj-spike`

// SpawnMPV 启动 mpv 并嵌入 hwnd 子窗口。mpvPath 指向随应用分发的 mpv.com
// （--wid 在 mpv 文档中要求窗口由调用进程拥有——mpv.com 是控制台版会闪窗，
// v2 实施期换 mpv.exe + CREATE_NO_WINDOW，这里骨架先记 TODO）。
func SpawnMPV(mpvPath string, hwnd windows.HWND, mediaURL string, extra ...string) (*exec.Cmd, error) {
	args := []string{
		fmt.Sprintf("--wid=%d", uintptr(hwnd)),
		"--input-ipc-server=" + PipeName,
		"--no-terminal",
		"--force-window=no",
		mediaURL,
	}
	args = append(args, extra...)
	cmd := exec.Command(mpvPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn mpv: %w", err)
	}
	return cmd, nil
}

// Connect 打开命名管道并包成 io 层，交给跨平台 IPC 读写器。
// 管道由 mpv 侧创建，需等它出现：v2 里做带退避的重试，spike 先一次调用。
func Connect() (Conn, error) {
	name, err := windows.UTF16PtrFromString(PipeName)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("open pipe %s: %w", PipeName, err)
	}
	return os.NewFile(uintptr(handle), PipeName), nil
}
