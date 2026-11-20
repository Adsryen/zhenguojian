// Package main —— zgj 诊断日志行（v3 容器可观测性，2026-11-20）。
//
// 为什么单独一个出口：v3 容器形态下「排查」的主通道是 `docker logs zgj-v3`
// （sidecar 的 stderr 直通容器日志）。既有 `fmt.Fprintf(os.Stderr, "[sidecar-web] …")`
// 是散落的启动/监听行，缺一份**带时间戳、单行、可 grep** 的运行期诊断流：
// 访问日志（status+耗时）、RPC 错误/慢调用、封面失败、登录失败。
//
// 纪律（与 app_log.go 同源思想，出口不同）：
//   - 只写 stderr（容器=docker logs；桌面 GUI 无控制台，不可见但无害；
//     stdio sidecar 里 stdout 已被改道 stderr，协议独占原始 fd 不受影响）；
//   - 单行：入参里的换行压成空格（多行堆栈进 docker logs 会切断 grep 行）；
//   - 截断 800 字节：诊断行不允许把日志刷爆（封面 data URL 这类大载荷不进这里）；
//   - 写失败吞掉（诊断不是产品路径）。
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// zgjLogLineCap 单行上限：一行诊断最多 800 字节（超出截断加标记）。
const zgjLogLineCap = 800

// zgjLogWriter 诊断行的出口（默认 stderr；测试可换 buffer 断言内容）。
// 带锁：访问日志与 RPC 日志来自不同 goroutine，单行必须原子可见。
var zgjLogWriter = func() *lockedWriter {
	return &lockedWriter{w: os.Stderr}
}()

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) write(p []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(p)
}

// zgjLogf 输出一条带时间戳的单行诊断。格式：
//
//	[zgj 2026-11-20T15:50:00+08:00] <msg>
func zgjLogf(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	msg = strings.ReplaceAll(msg, "\n", " ")
	msg = strings.ReplaceAll(msg, "\r", " ")
	if len(msg) > zgjLogLineCap {
		msg = msg[:zgjLogLineCap] + "…(truncated)"
	}
	zgjLogWriter.write([]byte(fmt.Sprintf("[zgj %s] %s\n",
		time.Now().Format("2006-01-02T15:04:05-07:00"), msg)))
}
