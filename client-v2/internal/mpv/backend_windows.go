//go:build windows

package mpv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const createNoWindow = 0x08000000 // CREATE_NO_WINDOW（v1 spawn 同款）

type windowsBackend struct{}

// DefaultBackend Windows：命名管道拨号 + CREATE_NO_WINDOW spawn。
func DefaultBackend() Backend { return windowsBackend{} }

// missingPlayerDLLs v1 PLAYER_REQUIRED_DLLS 检查：mpv.exe 静态链了
// Vulkan loader，缺 vulkan-1.dll 时 Windows 直接拒启（0xC0000135），
// 提前报人话比「无法连接控制通道」有用。
func missingPlayerDLLs(binary string) []string {
	folder := filepath.Dir(binary)
	system := filepath.Join(os.Getenv("WINDIR"), "System32")
	if system == filepath.Join("", "System32") {
		system = `C:\Windows\System32`
	}
	var missing []string
	for _, name := range []string{"vulkan-1.dll"} {
		a := filepath.Join(folder, name)
		b := filepath.Join(system, name)
		if stat(a) || stat(b) {
			continue
		}
		missing = append(missing, name)
	}
	return missing
}

func stat(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (windowsBackend) Launch(ctx context.Context, exePath string, args []string, wid uintptr) (Process, error) {
	if missing := missingPlayerDLLs(exePath); len(missing) > 0 {
		return nil, fmt.Errorf("播放器缺少组件 %s。它要和 mpv.exe 放在同一个目录（runtime\\mpv）",
			joinNames(missing))
	}
	attr := &sysProcAttr{creates: func() *syscall.SysProcAttr {
		return &syscall.SysProcAttr{CreationFlags: createNoWindow}
	}}
	return startProcess(exePath, args, attr)
}

func joinNames(names []string) string { return strings.Join(names, "、") }

// Dial 命名管道拨号：mpv 拥有管道服务器，spawn 后要等它就绪
// （v1 _open_pipe 重试循环；间隔 50ms，ctx 超时由调用方给 PipeWait）。
func (windowsBackend) Dial(ctx context.Context, socketPath string) (Conn, error) {
	name, err := syscall.UTF16PtrFromString(socketPath)
	if err != nil {
		return nil, err
	}
	backoff := time.NewTicker(50 * time.Millisecond)
	defer backoff.Stop()
	for {
		// OPEN_EXISTING：不创建，只连已存在的服务器
		handle, err := syscall.CreateFile(name,
			syscall.GENERIC_READ|syscall.GENERIC_WRITE,
			0, nil, syscall.OPEN_EXISTING, 0, 0)
		if err == nil && handle != syscall.InvalidHandle {
			return os.NewFile(uintptr(handle), socketPath), nil
		}
		select {
		case <-ctx.Done():
			if err != nil {
				return nil, errors.New("mpv: 管道连接超时: " + err.Error())
			}
			return nil, errors.New("mpv: 管道连接超时")
		case <-backoff.C:
		}
	}
}
