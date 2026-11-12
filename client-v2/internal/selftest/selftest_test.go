package selftest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"zhenguojian/client-v2/internal/downloadsvc"
	"zhenguojian/client-v2/internal/native"
)

// baseMpvName 与 mpvCandidates 内部用的是同一套名字规则，单独抽出来给断言用，
// 免得测试里再抄一遍规则（抄两遍就有一个会过期）。
func baseMpvName() string {
	if runtime.GOOS == "windows" {
		return "mpv.exe"
	}
	return "mpv"
}

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

// ── 第二十六轮：把"只是编译过"的沙箱/查路径逻辑变成 CI 真执行过的逻辑 ──
// 上一轮的诚实边界是「九步全部只拿到编译 + 纯逻辑单测」。
// 沙箱、查工具路径、报告写不干净这几段其实**不需要网络也不需要 ffmpeg**，
// 所以完全可以在 CI 上真跑——那就别把它们和 7/9 步一起留在"从未执行"里。

func TestPrepareSandboxLifecycle(t *testing.T) {
	before := os.Getenv("ZHENGUO_DATA")
	t.Cleanup(func() { _ = os.Setenv("ZHENGUO_DATA", before) })

	cleanup, err := Prepare()
	if err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}
	dir := SandboxDir()
	if dir == "" {
		t.Fatalf("SandboxDir 应记录临时目录")
	}
	if got := os.Getenv("ZHENGUO_DATA"); got != dir {
		t.Fatalf("ZHENGUO_DATA 应指向沙箱：env=%q dir=%q", got, dir)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("沙箱目录必须真的存在：%v", err)
	}
	if !sandboxReady {
		t.Fatalf("sandboxReady 未置真，pipeline 步会拒绝运行")
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("cleanup 后沙箱应被删除，仍在：%v", err)
	}
	if os.Getenv("ZHENGUO_DATA") != "" {
		t.Fatalf("cleanup 应清掉 ZHENGUO_DATA，得 %q", os.Getenv("ZHENGUO_DATA"))
	}
	if sandboxReady || SandboxDir() != "" {
		t.Fatalf("cleanup 后状态没复位：ready=%v dir=%q", sandboxReady, SandboxDir())
	}
}

// 这条钉的是"沙箱早于 Init"的实际效果：DataDir 读到的就是沙箱，
// 而不是用户真库（%LOCALAPPDATA%\ZhenguoJian）。上一轮这条只有注释，没有执行过。
func TestPrepareMakesDataDirPointAtSandbox(t *testing.T) {
	before := os.Getenv("ZHENGUO_DATA")
	t.Cleanup(func() { _ = os.Setenv("ZHENGUO_DATA", before) })

	cleanup, err := Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer cleanup()
	dir, err := native.DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if dir != SandboxDir() {
		t.Fatalf("DataDir 应等于沙箱目录，得 %q vs %q", dir, SandboxDir())
	}
	if real, err2 := os.UserConfigDir(); err2 == nil && strings.HasPrefix(dir, real) {
		t.Fatalf("沙箱不该落在用户配置目录下：%q（%q）", dir, real)
	}
}

func TestLookupToolEnvOverrideWins(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "fake-ffmpeg.bin")
	if err := os.WriteFile(stub, []byte("x"), 0o755); err != nil {
		t.Fatalf("写桩: %v", err)
	}
	t.Setenv("ZHENGUO_FFMPEG", stub)
	got, err := lookupTool("ffmpeg")
	if err != nil || got != stub {
		t.Fatalf("env 指定的路径必须优先，得 %q err=%v", got, err)
	}
}

// env 指到一个不存在的文件时必须**报错**而不是悄悄回落到 PATH：
// 否则"我明明指定了 ffmpeg 却用了一个别处的"这种问题永远查不出来。
func TestLookupToolRejectsBrokenEnvPath(t *testing.T) {
	t.Setenv("ZHENGUO_FFMPEG", filepath.Join(t.TempDir(), "nope-not-here"))
	_, err := lookupTool("ffmpeg")
	if err == nil || !strings.Contains(err.Error(), "ZHENGUO_FFMPEG") {
		t.Fatalf("应报出是哪个环境变量指错了，得 %v", err)
	}
}

func TestLookupToolFallsBackToPATH(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("写桩: %v", err)
	}
	t.Setenv("ZHENGUO_FFPROBE", "")          // 空串=未设（代码里走 TrimSpace 判空）
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := lookupTool("ffprobe")
	if err != nil {
		t.Fatalf("PATH 里的桩应该能找到: %v", err)
	}
	if filepath.Base(got) != "ffprobe" {
		t.Fatalf("找到了别的东西：%q", got)
	}
}

// v1/v2 打包布局是 exe 同级放 runtime\（app.zip + runtime.zip），
// 候选顺序必须"同级优先、runtime 次之"，否则装了 runtime 却报找不到。
func TestMpvCandidatesOrderAndLayout(t *testing.T) {
	got := mpvCandidates(`D:\app`)
	if len(got) != 3 {
		t.Fatalf("应有三个候选，得 %v", got)
	}
	if !strings.HasSuffix(got[0], filepath.Join("D:\\app", baseMpvName())) {
		t.Fatalf("第一候选应为 exe 同级：%v", got[0])
	}
	if !strings.Contains(got[1], "runtime") {
		t.Fatalf("第二候选应覆盖 runtime\\ 布局：%v", got[1])
	}
}

func TestRunSurvivesUnwritableReportDir(t *testing.T) {
	orig := reportDir
	reportDir = func() (string, error) { return "", fmt.Errorf("没地方写") }
	t.Cleanup(func() { reportDir = orig })
	var buf bytes.Buffer
	_, code := Run(&buf, []Step{okStep("resources", nil)}, "v-test")
	if code != 0 {
		t.Fatalf("报告写不出不该改变退出码，得 %d", code)
	}
	if !strings.Contains(buf.String(), "报告未落盘") {
		t.Fatalf("写不出报告必须说出来：\n%s", buf.String())
	}
}

// ── 第二十八轮：download / merge 两步的**逻辑**第一次在 CI 上真执行 ──
// 以前的说法是"代码完成但从未执行"。可这两步的入队载荷、轮询、终态判断、
// 产物校验并不依赖真网络与真 ffmpeg——只要把核心门面换成假的，它们就该被执行。
// 于是本轮顺手抓出一个**会在打包机上白等 20 分钟**的 bug（见 mergeList 注释）。

func useFakeCore(t *testing.T, handler func(action string, payload map[string]any) (map[string]any, error)) {
	t.Helper()
	origCall, origDir := coreCall, coreDataDir
	coreCall, coreDataDir = handler, func() (string, error) { return t.TempDir(), nil }
	t.Cleanup(func() { coreCall, coreDataDir = origCall, origDir })
}

func TestMergeListAcceptsRealServiceShape(t *testing.T) {
	// 回归钉：Service 的 payloadLocked 返回 merges 是 []*MergeEntry，
	// 以前 mergeList 只认 []map 与 []any → 永远空 → 合并步白等到超时还报错误原因。
	snap := map[string]any{"merges": []*downloadsvc.MergeEntry{
		{ID: "m1", State: "done", Output: "/x/y.mp4", Count: 2},
	}}
	got := mergeList(snap)
	if len(got) != 1 || got[0]["state"] != "done" || got[0]["output"] != "/x/y.mp4" {
		t.Fatalf("*MergeEntry 形状必须吃得下，得 %+v", got)
	}
	// 另两种形状也继续支持（单测构造 / JSON 往返）
	if len(mergeList(map[string]any{"merges": []map[string]any{{"state": "done"}}})) != 1 {
		t.Fatalf("[]map[string]any 形状丢了")
	}
	if len(mergeList(map[string]any{"merges": []any{map[string]any{"state": "done"}}})) != 1 {
		t.Fatalf("[]any 形状丢了")
	}
}

func TestMergeOutcomeSeparatesThreeFailureModes(t *testing.T) {
	good := filepath.Join(t.TempDir(), "full.mp4")
	if err := os.WriteFile(good, []byte("data"), 0o644); err != nil {
		t.Fatalf("写桩: %v", err)
	}
	empty := filepath.Join(t.TempDir(), "empty.mp4")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatalf("写空桩: %v", err)
	}
	cases := []struct {
		name   string
		merges []map[string]any
		want   string
		wantOK bool
	}{
		{"成功", []map[string]any{{"state": "done", "output": good}}, good, true},
		{"合并失败要带 state", []map[string]any{{"state": "failed", "detail": "音频规范化失败"}}, "合并未成功：state=failed", false},
		{"没有表要说是空的", nil, "合并表为空", false},
		{"done 但没路径", []map[string]any{{"state": "done"}}, "没有返回产物路径", false},
		{"路径不存在", []map[string]any{{"state": "done", "output": filepath.Join(t.TempDir(), "nope.mp4")}}, "产物文件不存在", false},
		{"产物是空文件", []map[string]any{{"state": "done", "output": empty}}, "产物为空文件", false},
	}
	for _, c := range cases {
		got, err := mergeOutcome(c.merges)
		if c.wantOK {
			if err != nil || got != c.want {
				t.Fatalf("%s：应成功返回 %q，得 %q err=%v", c.name, c.want, got, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s：错误里要能看出『%s』，得 %v", c.name, c.want, err)
		}
	}
}

func TestDownloadStepRunsAgainstFakeCore(t *testing.T) {
	var mu sync.Mutex
	var enqueued []map[string]any
	fake := func(action string, payload map[string]any) (map[string]any, error) {
		mu.Lock()
		defer mu.Unlock()
		switch action {
		case "enqueueDownloads":
			enqueued = append(enqueued, payload)
			return map[string]any{"added": 2}, nil
		case "downloads":
			return map[string]any{"jobs": []any{
				map[string]any{"id": "j1", "state": "completed", "bytes": float64(1024), "index": float64(1)},
				map[string]any{"id": "j2", "state": "completed", "bytes": float64(2048), "index": float64(2)},
			}}, nil
		case "downloadDirectory":
			return map[string]any{"directory": `D:\沙箱\下载`}, nil
		}
		return map[string]any{}, nil
	}
	useFakeCore(t, fake)

	cleanup, err := Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer cleanup()

	st := map[string]any{
		"drama":    map[string]any{"id": "hongguo:1", "title": "自检剧", "episodes": float64(5)},
		"chapters": []any{map[string]any{"currentEpisode": float64(1)}, map[string]any{"currentEpisode": float64(2)}},
	}
	dl := PipelineSteps(st)[0]
	detail, err := dl.Run()
	if err != nil {
		t.Fatalf("download 步在假核心下应该走通，得 %v", err)
	}
	if detail["completed"] != 2 || detail["cache_dir"] != `D:\沙箱\下载` {
		t.Fatalf("结果不对：%+v", detail)
	}
	if detail["bytes"] != int64(3072) {
		t.Fatalf("bytes 应累加两条任务，得 %v", detail["bytes"])
	}
	mu.Lock()
	defer mu.Unlock()
	if len(enqueued) != 1 {
		t.Fatalf("应该只入队一次，得 %d 次", len(enqueued))
	}
	got, _ := enqueued[0]["entries"].([]downloadsvc.EpisodeEntry)
	if len(got) != 2 || got[0].Index != 1 || got[1].Index != 2 {
		t.Fatalf("必须只取前 2 集且 index 为 1/2，得 %+v", enqueued[0]["entries"])
	}
	d, _ := enqueued[0]["drama"].(map[string]any)
	if src, _ := d["source"].(string); src != "hongguo" {
		// Enqueue 会校验 source（service.go:351），缺了整步红——
		// 所以这里必须看到兜底生效后的值，而不是原始 item 的空值。
		t.Fatalf("drama.source 应被兜底成 hongguo，得 %q（drama=%+v）", src, d)
	}
}

func TestDownloadStepReportsPartialCompletion(t *testing.T) {
	fake := func(action string, payload map[string]any) (map[string]any, error) {
		switch action {
		case "enqueueDownloads":
			return map[string]any{"added": 2}, nil
		case "downloads":
			return map[string]any{"jobs": []any{
				map[string]any{"id": "j1", "state": "completed", "bytes": float64(10)},
				map[string]any{"id": "j2", "state": "failed", "detail": "网络中断"},
			}}, nil
		case "downloadDirectory":
			return map[string]any{"directory": "x"}, nil
		}
		return map[string]any{}, nil
	}
	useFakeCore(t, fake)
	cleanup, err := Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer cleanup()

	st := map[string]any{
		"drama":    map[string]any{"id": "hongguo:1", "source": "hongguo", "title": "自检剧"},
		"chapters": []any{map[string]any{"currentEpisode": float64(1)}, map[string]any{"currentEpisode": float64(2)}},
	}
	detail, err := PipelineSteps(st)[0].Run()
	if err == nil || !strings.Contains(err.Error(), "只有 1 集") {
		t.Fatalf("一条失败必须报『只有 1 集完成』，得 err=%v detail=%+v", err, detail)
	}
	// 失败时 detail 里要带 jobs 摘要（打包机上没人想看一句光秃秃的"失败了"）
	if got, _ := detail["jobs"].([]string); len(got) != 2 {
		t.Fatalf("detail.jobs 应带两条摘要，得 %+v", detail["jobs"])
	}
}
