//go:build windows

package updater

import (
	"os/exec"
	"syscall"
)

// ApplySupported 标记当前平台是否支持「下载后自动替换并重启」。
const ApplySupported = true

// hideWindowAttr 与 client-v2/execattr_windows.go:11 的 main 包版本同值
// （那个在 package main 不可导入，此处为 updater 包内复刻，注意两处同步）。
func hideWindowAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NEW_CONSOLE
	}
}

// LaunchDetached 以最小化窗口分离启动更新脚本（cmd /C start "" /MIN bat），
// 不等子进程退出——脚本负责等本进程退出后完成替换与重启。
func LaunchDetached(batPath string) error {
	cmd := exec.Command("cmd.exe", "/C", "start", "", "/MIN", batPath)
	cmd.SysProcAttr = hideWindowAttr()
	return cmd.Start()
}
