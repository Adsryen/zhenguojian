package selftest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 这组单测**不碰网络也不碰核心**：Run 只吃 []Step，
// 所以能纯本地验的就是"自检这套机器本身"的契约——结局分类、退出码、
// PENDING 会不会被漏说、报告结构对不对。真正的管线对错由 --selftest 在打包机上跑。

func okStep(name string, detail map[string]any) Step {
	return Step{Name: name, Run: func() (map[string]any, error) { return detail, nil }}
}

func badStep(name string, msg string) Step {
	return Step{Name: name, Run: func() (map[string]any, error) { return map[string]any{"k": 1}, fmt.Errorf("%s", msg) }}
}

func withTempReport(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := reportDir
	reportDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { reportDir = orig })
	return dir
}

func TestRunAllOK(t *testing.T) {
	dir := withTempReport(t)
	var buf bytes.Buffer
	res, code := Run(&buf, []Step{
		okStep("resources", map[string]any{"mpv": "/x/mpv"}),
		okStep("library", map[string]any{"count": 10}),
	}, "v-test")
	if code != 0 {
		t.Fatalf("全 OK 应退出 0，得 %d", code)
	}
	if len(res) != 2 || res[0].Outcome != OK || res[1].Outcome != OK {
		t.Fatalf("结果不对：%+v", res)
	}
	for _, want := range []string{"[selftest] resources  OK", "[selftest] library    OK", "PENDING"} {
		if want == "PENDING" {
			if strings.Contains(buf.String(), "PENDING") {
				t.Fatalf("没有 SKIP 却打印了 PENDING：%s", buf.String())
			}
			continue
		}
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("输出缺 %q：\n%s", want, buf.String())
		}
	}
	// 报告落盘且结构可读
	b, err := os.ReadFile(filepath.Join(dir, "selftest-report.json"))
	if err != nil {
		t.Fatalf("报告没写出来: %v", err)
	}
	var rep Report
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("报告不是合法 JSON: %v", err)
	}
	if rep.Version != "v-test" || rep.ExitCode != 0 || len(rep.Steps) != 2 || len(rep.Failed) != 0 {
		t.Fatalf("报告字段不对：%+v", rep)
	}
}

func TestRunFailureSetsExitOneAndKeepsDetail(t *testing.T) {
	withTempReport(t)
	var buf bytes.Buffer
	res, code := Run(&buf, []Step{
		okStep("resources", nil),
		badStep("search", "搜索没有返回结果"),
	}, "v-test")
	if code != 1 {
		t.Fatalf("有 FAIL 必须退出 1，得 %d", code)
	}
	if res[1].Outcome != FAIL || res[1].Err != "搜索没有返回结果" {
		t.Fatalf("失败详情丢了：%+v", res[1])
	}
	if !strings.Contains(buf.String(), "FAILED 1 step(s): search") {
		t.Fatalf("失败汇总行没打出来：\n%s", buf.String())
	}
}

// SKIP 的意义全在这里：它**不能**被当成通过，也不能沉默。
func TestSkipIsNotPassAndIsAnnounced(t *testing.T) {
	var buf bytes.Buffer
	dir := withTempReport(t)
	steps := []Step{
		okStep("resources", nil),
		{Name: "download", Pending: "未接通：需要真实落盘"},
		{Name: "merge", Pending: "依赖 download 产物"},
	}
	res, code := Run(&buf, steps, "v-test")
	if code != 0 {
		t.Fatalf("SKIP 不该让退出码非 0（v1 语义：只有 FAIL 才红），得 %d", code)
	}
	if res[1].Outcome != SKIP || res[2].Outcome != SKIP {
		t.Fatalf("未实现步骤应记 SKIP：%+v", res)
	}
	out := buf.String()
	if !strings.Contains(out, "PENDING 2 step(s) not implemented yet: download, merge") {
		t.Fatalf("PENDING 行缺失或不完整：\n%s", out)
	}
	// 六步绿 ≠ 全对齐：pending 必须进报告，不然报告本身会撒谎
	b, err := os.ReadFile(filepath.Join(dir, "selftest-report.json"))
	if err != nil {
		t.Fatalf("读报告失败: %v", err)
	}
	var rep Report
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("报告 JSON 非法: %v", err)
	}
	if len(rep.Pending) != 2 || rep.Pending[0] != "download" {
		t.Fatalf("报告 pending 不对：%+v", rep.Pending)
	}
	if rep.Steps[1].Reason == "" {
		t.Fatalf("SKIP 步骤必须带原因：%+v", rep.Steps[1])
	}
}

func TestTruncRunesKeepsChineseIntact(t *testing.T) {
	// 直接字节切会让中文标题变成非法 UTF-8（报告里一片替换字符）
	s := "一世之尊的中文标题很长很长需要截断一下"
	got := truncRunes(s, 8)
	if !utf8Valid(got) {
		t.Fatalf("截断结果非法 UTF-8: %q", got)
	}
	if n := len([]rune(got)); n != 8 {
		t.Fatalf("应按字符截 8 个，得 %d（%q）", n, got)
	}
	if truncRunes("  abc  ", 10) != "abc" {
		t.Fatalf("应顺带去首尾空白")
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}
