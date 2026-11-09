// Package 内部共享件：进程包装 + stderr 尾部（v1 _drain_stderr/_why_died 素材）。
package mpv

import (
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

// sysProcAttr 平台后端的 exec 属性钩子（Linux 传 nil）。
type sysProcAttr struct {
	creates func() *syscall.SysProcAttr
}

// stderrTail 收集 mpv stderr 的尾部行。用作 cmd.Stderr 时由 exec 内部
// goroutine 串行写入；Wait 前拷贝已完成，因此不存在竞态外的乱序。
type stderrTail struct {
	mu    sync.Mutex
	lines []string
	pend  string
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pend += string(p)
	for {
		i := strings.IndexByte(t.pend, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(strings.TrimSuffix(t.pend[:i], "\r"))
		t.pend = t.pend[i+1:]
		if line != "" {
			t.lines = append(t.lines, line)
			if len(t.lines) > 40 {
				t.lines = t.lines[len(t.lines)-40:]
			}
		}
	}
	return len(p), nil
}

// String 尾部拼句（v1 _mpv_tail 的简化：错误行优先，没有就取末 2 行）。
func (t *stderrTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows := t.lines
	if t.pend != "" {
		rows = append(append([]string{}, rows...), strings.TrimSpace(t.pend))
	}
	if len(rows) == 0 {
		return ""
	}
	picked := make([]string, 0, 2)
	for _, row := range rows {
		if strings.Contains(row, "][e]") || strings.Contains(row, "][f]") {
			picked = append(picked, stripLogPrefix(row))
		}
	}
	if len(picked) == 0 {
		picked = rows[min(2, len(rows)):]
		if t.pend != "" {
			picked = rows[len(rows)-2:]
		}
	}
	return strings.Join(picked, "；")
}

func stripLogPrefix(row string) string {
	if i := strings.LastIndex(row, "] "); i >= 0 {
		return strings.TrimSpace(row[i+2:])
	}
	return strings.TrimSpace(row)
}

// osProcess 包装 os/exec 进程句柄（两个平台后端共用）。
type osProcess struct {
	cmd  *exec.Cmd
	tail *stderrTail
}

func (p *osProcess) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *osProcess) Wait() error { return p.cmd.Wait() }

func (p *osProcess) ErrTail() string { return p.tail.String() }

func (p *osProcess) Terminate() error {
	if p.cmd.Process == nil || p.cmd.ProcessState != nil {
		return nil
	}
	return p.terminate()
}

func (p *osProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// startProcess 公共启动路径：stdin/stdout 丢弃、stderr 收进尾部缓冲，
// sysAttr 由各平台后端传入（Windows 侧含 CREATE_NO_WINDOW）。
func startProcess(exePath string, args []string, sysAttr *sysProcAttr) (*osProcess, error) {
	cmd := exec.Command(exePath, args...)
	cmd.Stdin = nil // 关闭 stdin（v1 传 DEVNULL）
	cmd.Stdout = io.Discard
	tail := &stderrTail{}
	cmd.Stderr = tail
	if sysAttr != nil && sysAttr.creates != nil {
		cmd.SysProcAttr = sysAttr.creates()
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &osProcess{cmd: cmd, tail: tail}, nil
}
