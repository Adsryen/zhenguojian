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

// ── 下面是 R7-2（download / decrypt / merge）新增的纯逻辑断言 ──
// 这三步真正跑起来需要打包机 + 网络 + ffmpeg，本机与 CI 都做不到；
// 所以能钉住的是它的**判断逻辑与安全前提**，而不是管线结果。

func TestAllTerminalNeedsTwoAndAllDone(t *testing.T) {
	if allTerminal(nil) || allTerminal([]map[string]any{{"state": "completed"}}) {
		t.Fatalf("少于 2 条不能算终态（否则入队瞬间就被判「完成」）")
	}
	if allTerminal([]map[string]any{{"state": "completed"}, {"state": "running"}}) {
		t.Fatalf("有 running 就不能算终态")
	}
	if !allTerminal([]map[string]any{{"state": "completed"}, {"state": "failed"}}) {
		t.Fatalf("completed/failed/error 都是终态")
	}
}

func TestMergeSettledCoversFailure(t *testing.T) {
	if mergeSettled(nil) {
		t.Fatalf("空列表不能算落定")
	}
	if mergeSettled([]map[string]any{{"state": "queued"}, {"state": "done"}}) {
		t.Fatalf("还有 queued 就不能算落定")
	}
	// 全失败也算落定：要往下报错，不能干等到超时
	if !mergeSettled([]map[string]any{{"state": "failed"}, {"state": "canceled"}}) {
		t.Fatalf("失败/取消必须让轮询退出并去报错")
	}
}

func TestJobListAcceptsBothShapes(t *testing.T) {
	typed := []map[string]any{{"state": "completed"}}
	fromAny := []any{map[string]any{"state": "completed"}}
	if len(jobList(map[string]any{"jobs": typed})) != 1 {
		t.Fatalf("[]map[string]any 形状要能吃下（payloadLocked 返回的就是它）")
	}
	if len(jobList(map[string]any{"jobs": fromAny})) != 1 {
		t.Fatalf("[]any 形状也要能吃下（JSON 往返后是它）")
	}
	if len(jobList(map[string]any{})) != 0 {
		t.Fatalf("缺字段不该 panic")
	}
}

func TestPipelineRefusesWithoutSandbox(t *testing.T) {
	// 安全前提：沙箱没建立就绝不入队——v1 是直接清真实库再下，v2 不给自检这个权力
	if sandboxReady {
		t.Skip("别的测试把沙箱打开了")
	}
	if _, err := newPipeline(); err == nil || !strings.Contains(err.Error(), "沙箱") {
		t.Fatalf("无沙箱必须拒绝，得 %v", err)
	}
	steps := PipelineSteps(map[string]any{})
	_, err := steps[0].Run()
	if err == nil || !strings.Contains(err.Error(), "前置条件") {
		t.Fatalf("缺前序状态应报前置条件不成立，得 %v", err)
	}
}

func TestShouldKeepSandboxOnlyOnFail(t *testing.T) {
	if ShouldKeepSandbox([]Result{{Name: "a", Outcome: OK}, {Name: "b", Outcome: SKIP}}) {
		t.Fatalf("SKIP 不该保留现场")
	}
	if !ShouldKeepSandbox([]Result{{Name: "merge", Outcome: FAIL}}) {
		t.Fatalf("FAIL 必须保留现场供排查")
	}
}

// 步骤清单的守护：v1 是九步，v2 对等成 discover + 显式 SKIP 的 decrypt。
// 有人增删步骤时必须改这里并说明理由，避免"报告悄悄少一步"。
func TestStepInventoryMatchesV1Parity(t *testing.T) {
	names := make([]string, 0)
	for _, st := range Steps() {
		names = append(names, st.Name)
	}
	want := []string{"resources", "library", "discover", "search", "detail",
		"preload", "download", "decrypt", "merge"}
	if len(names) != len(want) {
		t.Fatalf("步骤数变了：%v", names)
	}
	for i, n := range want {
		if names[i] != n {
			t.Fatalf("第 %d 步应为 %s，得 %s（全部：%v）", i+1, n, names[i], names)
		}
	}
	// decrypt 在 v2 没有独立子系统，必须是"不适用"型 SKIP，不能被当成欠账悄悄删掉
	all := Steps()
	var dec *Step
	for i := range all {
		if all[i].Name == "decrypt" {
			dec = &all[i]
		}
	}
	if dec == nil || dec.Run != nil || !strings.Contains(dec.Pending, "不适用") {
		t.Fatalf("decrypt 应为注明『不适用』的 SKIP 步：%+v", dec)
	}
}
