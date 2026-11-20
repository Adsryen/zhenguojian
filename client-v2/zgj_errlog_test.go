//go:build zgj_sidecar

// zgj_errlog_test.go —— 诊断日志出口的单测（v3 可观测性）。
// 钉死三条纪律：带 [zgj 时间戳] 前缀的单行格式、换行压平、800 字节截断。
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestZgjLogfFormat(t *testing.T) {
	var buf bytes.Buffer
	orig := zgjLogWriter
	zgjLogWriter = &lockedWriter{w: &buf}
	defer func() { zgjLogWriter = orig }()

	zgjLogf("http GET /video/seg 206 42ms")
	line := buf.String()
	if !strings.HasPrefix(line, "[zgj ") || !strings.HasSuffix(line, "\n") {
		t.Fatalf("必须以 [zgj 时间戳] 开头并换行收尾: %q", line)
	}
	if !strings.Contains(line, "http GET /video/seg 206 42ms") {
		t.Fatalf("正文缺失: %q", line)
	}

	// 换行压平（多行堆栈不许切断 docker logs 的行）
	zgjLogf("rpc 失败 X: 第一行\n第二行")
	if !strings.Contains(buf.String(), "第一行 第二行") {
		t.Fatalf("换行必须压成空格: %q", buf.String())
	}

	// 800 字节截断
	zgjLogf("%s", strings.Repeat("a", 2000))
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasSuffix(last, "…(truncated)") {
		t.Fatalf("超长行必须截断: len=%d", len(last))
	}
	if len(last) > len("[zgj ") + 32 + zgjLogLineCap + len("…(truncated)") {
		t.Fatalf("截断不生效: len=%d", len(last))
	}
}
