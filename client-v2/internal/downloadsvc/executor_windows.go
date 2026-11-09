//go:build windows

package downloadsvc

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// CREATE_NO_WINDOW：合并子进程不弹窗（v1 行为）。
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

// killTree Windows：taskkill /T 连坐子进程树。
func killTree(p *os.Process) error {
	if p == nil {
		return nil
	}
	_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(p.Pid)).Run()
	return nil
}
