//go:build !windows

package main

import "syscall"

// hideWindowAttr 非 Windows 平台无控制台窗口问题，返回 nil 保持默认。
func hideWindowAttr() *syscall.SysProcAttr { return nil }
