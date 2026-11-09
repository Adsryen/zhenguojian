// executor.go —— 合并管线的子进程门面（可注入，测试注假执行器）。
// 查找顺序对齐 v1 tools._tool：环境变量 ZHENGUO_FFMPEG/ZHENGUO_FFPROBE →
// exe 目录 runtime/<name>[.exe] → exe 目录 <name>[.exe] → PATH。
package downloadsvc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Executor 抽象外部进程调用，runMerge 全部经它触达 ffmpeg/ffprobe。
type Executor interface {
	// Look 解析可执行文件绝对路径；找不到返回错误。
	Look(name string) (string, error)
	// Run 运行进程；onLine 非 nil 时逐行回调 stdout（用于 -progress 解析）。
	// ctx 取消时终止整个进程树并返回 ctx.Err()。
	Run(ctx context.Context, bin string, args []string, onLine func(string)) error
}

// RunError ffmpeg/ffprobe 非零退出（stderr 尾部已截断）。
type RunError struct {
	Bin      string
	ExitCode int
	Stderr   string
}

func (e *RunError) Error() string {
	tail := strings.TrimSpace(e.Stderr)
	if tail != "" {
		return fmt.Sprintf("%s 执行失败（退出码 %d）：%s", filepath.Base(e.Bin), e.ExitCode, tail)
	}
	return fmt.Sprintf("%s 执行失败（退出码 %d）", filepath.Base(e.Bin), e.ExitCode)
}

type commandExecutor struct {
	mu    sync.Mutex
	exes  map[string]string
	procs map[*os.Process]struct{}
}

func newCommandExecutor() *commandExecutor {
	return &commandExecutor{
		exes:  map[string]string{},
		procs: map[*os.Process]struct{}{},
	}
}

var toolEnvNames = map[string]string{"ffmpeg": "ZHENGUO_FFMPEG", "ffprobe": "ZHENGUO_FFPROBE"}

func (e *commandExecutor) Look(name string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p, ok := e.exes[name]; ok {
		return p, nil
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	var exeDir string
	if self, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(self)
	}
	candidates := []string{}
	if env := toolEnvNames[name]; env != "" {
		if v := os.Getenv(env); v != "" {
			candidates = append(candidates, v)
		}
	}
	if exeDir != "" {
		candidates = append(candidates,
			filepath.Join(exeDir, "runtime", name, name+suffix),
			filepath.Join(exeDir, name+suffix))
	}
	for _, cand := range candidates {
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			e.exes[name] = cand
			return cand, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		e.exes[name] = p
		return p, nil
	}
	return "", fmt.Errorf("缺少 %s 组件，请安装后重试", name)
}

const stderrTailBytes = 4000

func (e *commandExecutor) Run(ctx context.Context, bin string, args []string, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.SysProcAttr = sysProcAttr()
	cmd.Cancel = func() error { return killTree(cmd.Process) }
	cmd.WaitDelay = 3 * time.Second // 取消后强杀残留管道持有者
	stderr := &boundedTail{max: stderrTailBytes}
	var stderrW io.Writer = stderr
	if onLine != nil {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("启动 %s 失败：%w", filepath.Base(bin), err)
		}
		e.track(cmd)
		defer e.untrack(cmd)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			onLine(scanner.Text())
		}
		err = cmd.Wait()
		return runResult(ctx, bin, err, stderr.String())
	}
	cmd.Stderr = stderrW
	if err := cmd.Run(); err != nil {
		return runResult(ctx, bin, err, stderr.String())
	}
	return nil
}

func (e *commandExecutor) track(cmd *exec.Cmd) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cmd.Process != nil {
		e.procs[cmd.Process] = struct{}{}
	}
}

func (e *commandExecutor) untrack(cmd *exec.Cmd) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cmd.Process != nil {
		delete(e.procs, cmd.Process)
	}
}

func runResult(ctx context.Context, bin string, err error, stderrTail string) error {
	if err == nil {
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &RunError{Bin: bin, ExitCode: exitErr.ExitCode(), Stderr: stderrTail}
	}
	return fmt.Errorf("运行 %s 失败：%w", filepath.Base(bin), err)
}

// boundedTail 只保留最后 max 字节。
type boundedTail struct {
	max  int
	buf  []byte
	over bool
}

func (b *boundedTail) Write(p []byte) (int, error) {
	if len(p) >= b.max {
		b.buf = append(b.buf[:0], p[len(p)-b.max:]...)
		b.over = true
		return len(p), nil
	}
	if len(b.buf)+len(p) > b.max {
		trim := len(b.buf) + len(p) - b.max
		b.buf = append(b.buf[trim:], p...)
		b.over = true
	} else {
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

func (b *boundedTail) String() string { return string(b.buf) }
