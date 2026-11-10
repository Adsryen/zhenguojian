//go:build windows

package main

import "syscall"

// hideWindowAttr 壳层子进程统一窗口策略：CREATE_NO_WINDOW 不闪控制台。
// GUI 子系统主程序（-H=windowsgui）spawn 控制台子程序（ffmpeg 等）若不带该标志，
// 每次都会新开一个可见 cmd 窗口——封面转码并发时表现为疯狂弹窗（v1 PyQt 经
// STARTUPINFO 隐藏，v2 壳层必须自带）。与 internal/downloadsvc sysProcAttr 同值。
func hideWindowAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
