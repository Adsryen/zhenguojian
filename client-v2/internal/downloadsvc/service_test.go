// service_test.go —— 假核心 + 假执行器覆盖：入队校验、设置读-改-写、
// 轮询速度/事件去重、自动恢复中断任务、控制命令映射、合并队列校验、
// 合并全链路（lease 获取释放/阶段/产物命名/未校验降级/取消）。
package downloadsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- 假核心 ----------

type coreCall struct {
	action  string
	payload map[string]any
}

type fakeCore struct {
	mu       sync.Mutex
	calls    []coreCall
	handlers map[string]func(payload map[string]any) (map[string]any, error)
}

func newFakeCore() *fakeCore {
	return &fakeCore{handlers: map[string]func(map[string]any) (map[string]any, error){}}
}

func (f *fakeCore) Call(action string, payload map[string]any) (map[string]any, error) {
	f.mu.Lock()
	f.calls = append(f.calls, coreCall{action: action, payload: payload})
	h := f.handlers[action]
	f.mu.Unlock()
	if h == nil {
		return map[string]any{}, nil
	}
	return h(payload)
}

func (f *fakeCore) count(action string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.action == action {
			n++
		}
	}
	return n
}

func (f *fakeCore) last(action string) (coreCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].action == action {
			return f.calls[i], true
		}
	}
	return coreCall{}, false
}

func jobsResult(jobs ...map[string]any) map[string]any {
	out := make([]any, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j)
	}
	return map[string]any{"jobs": out}
}

func testJob(id, state string, index int) map[string]any {
	return map[string]any{
		"id":     id,
		"drama":  map[string]any{"id": "d1", "title": "测试剧", "source": "xh"},
		"index":  float64(index),
		"state":  state,
		"bytes":  float64(0),
		"total":  float64(0),
		"folder": "d1/" + id,
		// 真实核心快照里 job 恒带 chapter（currentEpisode=集号，1-based），
		// 合并集号解析（episodeOfJob）以此为准。
		"chapter": map[string]any{
			"title":          fmt.Sprintf("第%d集", index),
			"currentEpisode": float64(index),
		},
	}
}

// ---------- 假执行器 ----------

type runCall struct {
	bin  string
	args []string
}

type fakeExecutor struct {
	mu       sync.Mutex
	runs     []runCall
	probes   map[string]string // 路径子串 → ffprobe JSON
	blockOn  string            // 命中子串的 ffmpeg 调用阻塞至 ctx 取消
	failOn   func(args []string) (error, bool)
	progress bool // ffmpeg -progress 调用即时推满进度行
}

func (f *fakeExecutor) Look(name string) (string, error) { return "/fake/bin/" + name, nil }

func (f *fakeExecutor) Run(ctx context.Context, bin string, args []string, onLine func(string)) error {
	f.mu.Lock()
	f.runs = append(f.runs, runCall{bin: bin, args: append([]string(nil), args...)})
	probes := f.probes
	blockOn := f.blockOn
	failOn := f.failOn
	progress := f.progress
	f.mu.Unlock()

	joined := strings.Join(args, " ")
	base := filepath.Base(bin)
	if base == "ffprobe" {
		path := args[len(args)-1]
		for key, body := range probes {
			if strings.Contains(path, key) {
				if onLine != nil {
					for _, line := range strings.Split(body, "\n") {
						onLine(line)
					}
				}
				return nil
			}
		}
		return &RunError{Bin: bin, ExitCode: 1, Stderr: "fake: 无该文件探针数据 " + path}
	}
	if failOn != nil {
		if err, bad := failOn(args); bad {
			return err
		}
	}
	if blockOn != "" && strings.Contains(joined, blockOn) {
		<-ctx.Done()
		return ctx.Err()
	}
	out := args[len(args)-1]
	if out == os.DevNull {
		return nil
	}
	if progress && onLine != nil && strings.Contains(joined, "-progress") {
		onLine("out_time_us=999000000")
	}
	if dir := filepath.Dir(out); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	if err := os.WriteFile(out, []byte("fake-media"), 0o644); err != nil {
		return err
	}
	return nil
}

func (f *fakeExecutor) countContaining(sub string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.runs {
		if strings.Contains(strings.Join(r.args, " "), sub) {
			n++
		}
	}
	return n
}

func probeJSON(duration float64, pixFmt string) string {
	streams := fmt.Sprintf(`[{"codec_type":"video","codec_name":"h264","width":1280,"height":720,"pix_fmt":"%s","bit_rate":"1000000","sample_aspect_ratio":"1:1","color_transfer":"bt709","color_primaries":"bt709"},{"codec_type":"audio","codec_name":"aac","profile":"LC","sample_rate":"44100","channels":2,"channel_layout":"stereo","extradata_hash":"abc"}]`, pixFmt)
	return fmt.Sprintf(`{"format":{"duration":"%.1f","size":"2000"},"streams":%s}`, duration, streams)
}

// ---------- 测试装配 ----------

type harness struct {
	core   *fakeCore
	exec   *fakeExecutor
	svc    *Service
	tmp    string
	mu     sync.Mutex
	events []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{core: newFakeCore(), exec: &fakeExecutor{}}
	h.tmp = t.TempDir()
	h.svc = New(Options{
		Call:         h.core.Call,
		EventsEmit:   func(name string, data ...any) { h.mu.Lock(); h.events = append(h.events, name); h.mu.Unlock() },
		DataDirFunc:  func() (string, error) { return h.tmp, nil },
		PollInterval: time.Hour, // 手动 refresh，避免定时器干扰
		Executor:     h.exec,
	})
	return h
}

func (h *harness) emitCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.events)
}

func waitFor(t *testing.T, cond func() bool, why string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", why)
}

func (h *harness) mergeState(id string) (map[string]any, bool) {
	list := h.svc.MergeList()
	for i := len(list) - 1; i >= 0; i-- {
		if list[i]["id"] == id {
			return list[i], true
		}
	}
	return nil, false
}

// ---------- 用例 ----------

func TestEnqueueValidation(t *testing.T) {
	h := newHarness(t)
	added := 0
	h.core.handlers["enqueueDownloads"] = func(p map[string]any) (map[string]any, error) {
		added++
		if got := len(p["entries"].([]EpisodeEntry)); got != 2 {
			t.Errorf("entries 数量 = %d, 期望 2", got)
		}
		return map[string]any{"added": 2}, nil
	}
	drama := map[string]any{"id": "d1", "source": "xh"}
	entries := []EpisodeEntry{{Chapter: map[string]any{"title": "第1集"}, Index: 0}, {Chapter: map[string]any{"title": "第2集"}, Index: 1}}
	out, err := h.svc.Enqueue(drama, entries, 0)
	if err != nil || out["added"] != 2 {
		t.Fatalf("Enqueue = %v, %v", out, err)
	}
	if added != 1 {
		t.Fatalf("核心入队调用 %d 次", added)
	}
	call, _ := h.core.last("enqueueDownloads")
	if call.payload["drama"] == nil || call.payload["quality"] == nil {
		t.Fatalf("入队 payload 缺字段: %v", call.payload)
	}
	if _, err := h.svc.Enqueue(map[string]any{"id": "d1"}, entries, 0); err == nil || !strings.Contains(err.Error(), "剧集信息不完整") {
		t.Errorf("缺 source 未报错: %v", err)
	}
	if _, err := h.svc.Enqueue(drama, nil, 0); err == nil || !strings.Contains(err.Error(), "请先选择") {
		t.Errorf("空选集未报错: %v", err)
	}
	big := make([]EpisodeEntry, 501)
	for i := range big {
		big[i] = EpisodeEntry{Index: i}
	}
	if _, err := h.svc.Enqueue(drama, big, 0); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("超 500 未报错: %v", err)
	}
}

func TestSaveSettingsReadModifyWrite(t *testing.T) {
	h := newHarness(t)
	h.core.handlers["resourceSettings"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{
			"proxyMode": "manual", "proxyUrl": "http://127.0.0.1:7890",
			"catalogConcurrency": float64(3), "catalogIntervalMs": float64(250),
			"downloadConcurrency": float64(2), "downloadBySource": true,
		}, nil
	}
	var saved map[string]any
	h.core.handlers["saveResourceSettings"] = func(p map[string]any) (map[string]any, error) {
		saved, _ = p["settings"].(map[string]any)
		return map[string]any{}, nil
	}
	// 越界钳位 + 保留 proxy/catalog 字段
	if _, err := h.svc.SaveSettings(0, false, false); err != nil {
		t.Fatal(err)
	}
	if saved["downloadConcurrency"] != 1 || saved["proxyMode"] != "manual" || saved["proxyUrl"] == "" ||
		saved["catalogConcurrency"] != float64(3) || saved["catalogIntervalMs"] != float64(250) || saved["downloadBySource"] != false {
		t.Fatalf("saveResourceSettings 补丁错误: %v", saved)
	}
	if _, err := h.svc.SaveSettings(99, true, true); err != nil {
		t.Fatal(err)
	}
	if saved["downloadConcurrency"] != 8 {
		t.Fatalf("并发未钳到 8: %v", saved["downloadConcurrency"])
	}
	st, err := h.svc.Settings()
	if err != nil || st["mergeVerify"] != true {
		t.Fatalf("Settings 合并视图错误: %v %v", st, err)
	}
	// mergeVerify 持久化：新服务从状态文件恢复 false
	if _, err := h.svc.SaveSettings(2, true, false); err != nil {
		t.Fatal(err)
	}
	h2 := New(Options{Call: h.core.Call, DataDirFunc: func() (string, error) { return h.tmp, nil }, Executor: h.exec})
	st2, err := h2.Settings()
	if err != nil || st2["mergeVerify"] != false {
		t.Fatalf("mergeVerify 未持久化: %v %v", st2, err)
	}
}

func TestPollSpeedAndEventDedupe(t *testing.T) {
	h := newHarness(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := base
	h.svc.now = func() time.Time { return cur }
	state := "downloading"
	var bytes float64
	h.core.handlers["downloads"] = func(map[string]any) (map[string]any, error) {
		job := testJob("j1", state, 0)
		job["bytes"] = bytes
		job["total"] = float64(10000)
		return jobsResult(job), nil
	}

	jobs, err := h.svc.refresh(true) // 首采样，无速度
	if err != nil || jobs[0]["speed"] != float64(0) {
		t.Fatalf("首采样 speed=%v err=%v", jobs[0]["speed"], err)
	}
	if h.emitCount() != 1 {
		t.Fatalf("首次快照应推事件 1 次, got %d", h.emitCount())
	}
	cur = cur.Add(2 * time.Second)
	jobs, _ = h.svc.refresh(true)
	if speed := jobs[0]["speed"].(float64); speed != 0 { // bytes 仍 0/0
		t.Fatalf("bytes 未变时 speed=%v, 期望 0", speed)
	}
	cur = cur.Add(2 * time.Second)
	bytes = 2000
	jobs, _ = h.svc.refresh(true)
	// 窗口 [0s,2s,4s]，speed=(2000-0)/4=500
	if speed := jobs[0]["speed"].(float64); speed != 500 {
		t.Fatalf("speed = %v, 期望 500", speed)
	}
	if eta := jobs[0]["eta"].(float64); eta != 16 { // (10000-2000)/500
		t.Fatalf("eta = %v, 期望 16", eta)
	}
	before := h.emitCount()
	if _, err := h.svc.refresh(true); err != nil { // 完全相同快照
		t.Fatal(err)
	}
	if h.emitCount() != before {
		t.Fatalf("相同快照不应重复推事件")
	}
	// 断点重开：bytes 回退 → 窗口清零
	cur = cur.Add(1 * time.Second)
	bytes = 100
	jobs, _ = h.svc.refresh(true)
	if jobs[0]["speed"].(float64) != 0 {
		t.Fatalf("bytes 回退后 speed 应清零, got %v", jobs[0]["speed"])
	}
	// 非下载态清窗口
	cur = cur.Add(1 * time.Second)
	state = "completed"
	jobs, _ = h.svc.refresh(true)
	if jobs[0]["speed"].(float64) != 0 {
		t.Fatalf("completed 任务 speed=%v", jobs[0]["speed"])
	}
}

func TestAutoResumeInterruptedOnce(t *testing.T) {
	h := newHarness(t)
	job := testJob("j1", "paused", 0)
	job["error"] = "上次下载已暂停（程序异常退出）"
	h.core.handlers["downloads"] = func(map[string]any) (map[string]any, error) { return jobsResult(job), nil }
	var resumed []string
	h.core.handlers["controlDownloads"] = func(p map[string]any) (map[string]any, error) {
		resumed = append(resumed, fmt.Sprintf("%v/%v", p["jobId"], p["command"]))
		return map[string]any{}, nil
	}
	if _, err := h.svc.refresh(false); err != nil {
		t.Fatal(err)
	}
	if len(resumed) != 1 || resumed[0] != "j1/resume" {
		t.Fatalf("自动恢复调用 = %v", resumed)
	}
	if _, err := h.svc.refresh(false); err != nil {
		t.Fatal(err)
	}
	if len(resumed) != 1 {
		t.Fatalf("自动恢复应只跑一次, got %v", resumed)
	}
}

func TestControlMappingAndBatch(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Retry("j1"); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.core.last("controlDownloads"); c.payload["command"] != "resume" || c.payload["jobId"] != "j1" {
		t.Fatalf("Retry 未映射 resume: %v", c.payload)
	}
	if _, err := h.svc.Cancel("j2"); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.core.last("controlDownloads"); c.payload["command"] != "remove" {
		t.Fatalf("Cancel 未映射 remove: %v", c.payload)
	}
	if _, err := h.svc.Control("j1", "delete"); err == nil {
		t.Fatal("非法命令未拦截")
	}
	if _, err := h.svc.ControlAll("pauseAll"); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.core.last("controlDownloads"); c.payload["jobId"] != "" {
		t.Fatalf("ControlAll jobId 必须为空: %v", c.payload)
	}
	if _, err := h.svc.ControlAll("removeAll"); err == nil {
		t.Fatal("ControlAll 白名单未拦截")
	}
	h.core.handlers["removeDownloads"] = func(p map[string]any) (map[string]any, error) {
		ids := p["jobIds"].([]string)
		return map[string]any{"completed": ids[:1], "failures": map[string]string{"j3": "占用"}}, nil
	}
	out, err := h.svc.RemoveBatch([]string{"j2", "j3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out["completed"].([]string)) != 1 || out["failures"].(map[string]string)["j3"] != "占用" {
		t.Fatalf("RemoveBatch 结果透传错误: %v", out)
	}
	if out, err := h.svc.RemoveBatch(nil); err != nil || len(out["completed"].([]string)) != 0 {
		t.Fatalf("空批应直接返回: %v %v", out, err)
	}
}

// ---------- 合并 ----------

func mergeFixture(t *testing.T, h *harness) (ep1, ep2 string) {
	t.Helper()
	root := h.tmp + "/dl"
	ep1, ep2 = root+"/ep1.mp4", root+"/ep2.mp4"
	for _, p := range []string{ep1, ep2} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("source"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.core.handlers["downloads"] = func(map[string]any) (map[string]any, error) {
		return jobsResult(testJob("j1", "completed", 1), testJob("j2", "completed", 2)), nil
	}
	h.core.handlers["downloadDirectory"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{"directory": root}, nil
	}
	h.core.handlers["localPlayback"] = func(p map[string]any) (map[string]any, error) {
		idx := p["index"].(int)
		path := ep1
		if idx == 2 {
			path = ep2
		}
		return map[string]any{"local": true, "url": path}, nil
	}
	h.exec.probes = map[string]string{
		"ep1.mp4": probeJSON(60, "yuv420p"),
		"ep2.mp4": probeJSON(60, "yuv420p"),
		"full":    probeJSON(120, "yuv420p"),
	}
	return ep1, ep2
}

func TestMergeQueueValidation(t *testing.T) {
	h := newHarness(t)
	h.core.handlers["downloads"] = func(map[string]any) (map[string]any, error) {
		return jobsResult(testJob("j1", "completed", 0), testJob("j2", "downloading", 1),
			map[string]any{"id": "j9", "drama": map[string]any{"id": "d2", "title": "别剧"}, "index": float64(0), "state": "completed"}), nil
	}
	drama := map[string]any{"id": "d1"}
	if _, err := h.svc.MergeStart("season", drama, nil); err == nil || !strings.Contains(err.Error(), "不足 2 集") {
		t.Fatalf("d1 只有 1 集 completed 应拒绝: %v", err)
	}
	if _, err := h.svc.MergeStart("season", map[string]any{}, nil); err == nil {
		t.Fatal("缺 drama id 未拦截")
	}
	if _, err := h.svc.MergeStart("selected", nil, []string{"j1", "j2"}); err == nil || !strings.Contains(err.Error(), "只有已完成") {
		t.Fatalf("selected 含非完成集未拦截: %v", err)
	}
	if _, err := h.svc.MergeStart("selected", nil, []string{"j1", "j9"}); err == nil || !strings.Contains(err.Error(), "同一部剧") {
		t.Fatalf("跨剧合并未拦截: %v", err)
	}
	if _, err := h.svc.MergeStart("weird", drama, nil); err == nil {
		t.Fatal("非法 mode 未拦截")
	}
}

func TestMergeFullPipeline(t *testing.T) {
	h := newHarness(t)
	ep1, _ := mergeFixture(t, h)
	var leaseMu sync.Mutex
	var leases []string
	h.core.handlers["workLease"] = func(p map[string]any) (map[string]any, error) {
		leaseMu.Lock()
		leases = append(leases, fmt.Sprintf("%v/%v", p["jobId"], p["command"]))
		leaseMu.Unlock()
		return map[string]any{"count": 1}, nil
	}
	h.exec.progress = true
	ctx := context.Background()
	h.svc.OnStartup(ctx)
	defer h.svc.OnShutdown(ctx)

	out, err := h.svc.MergeStart("season", map[string]any{"id": "d1"}, nil)
	if err != nil {
		t.Fatalf("MergeStart: %v", err)
	}
	if out["queued"] != 1 {
		t.Fatalf("MergeStart 返回 %v", out)
	}
	waitFor(t, func() bool {
		m, ok := h.mergeState("d1#001-002")
		return ok && (m["state"] == MergeDone || m["state"] == MergeFailed)
	}, "合并应在假执行器下完成")
	m, _ := h.mergeState("d1#001-002")
	if m["state"] != MergeDone {
		t.Fatalf("合并状态 = %v, detail=%v", m["state"], m["detail"])
	}
	output, _ := m["output"].(string)
	wantName := "测试剧 全001-002集.mkv"
	if filepath.Base(output) != wantName {
		t.Fatalf("产物名 = %q, 期望 %q", output, wantName)
	}
	if st, err := os.Stat(output); err != nil || st.Size() == 0 {
		t.Fatalf("产物不存在: %v", err)
	}
	if m["progress"] != float64(1) {
		t.Fatalf("progress = %v", m["progress"])
	}
	// workLease 先 start 后 end
	leaseMu.Lock()
	leaseSeq := append([]string(nil), leases...)
	leaseMu.Unlock()
	if len(leaseSeq) != 2 || leaseSeq[0] != "media/start" || leaseSeq[1] != "media/end" {
		t.Fatalf("workLease 序列 = %v", leaseSeq)
	}
	// 合并后工作目录清理
	if entries, err := os.ReadDir(filepath.Join(filepath.Dir(ep1), ".cache", "merge")); err == nil && len(entries) > 0 {
		t.Fatalf("合并工作目录未清理: %v", entries)
	}
	// ffmpeg 调用序列：2×normalize(copy) + concat；ffprobe：3 源+1 成品复检
	if got := h.exec.countContaining("-f concat"); got != 1 {
		t.Fatalf("concat 调用次数 = %d", got)
	}
	if got := h.exec.countContaining("-decryption_key"); got != 0 {
		t.Fatalf("无密钥不应解密: %d", got)
	}
	// 完整解码校验默认开启：成品以 -xerror 解码
	if got := h.exec.countContaining("-xerror"); got < 1 {
		t.Fatalf("verify 应执行严格解码, got %d", got)
	}
	if detail, _ := m["detail"].(string); strings.Contains(detail, "未完整校验") {
		t.Fatalf("签名一致时不应标注未校验: %v", detail)
	}
	snap, _ := h.svc.Snapshot()
	jobsSnap := snap["jobs"].([]map[string]any)
	if len(jobsSnap) != 2 || jobsSnap[0]["speed"] == nil {
		t.Fatalf("Snapshot jobs 缺 speed 字段: %v", jobsSnap)
	}
	body, _ := json.Marshal(snap)
	if !strings.Contains(string(body), `"mergeVerify":true`) {
		t.Fatalf("Snapshot 应含 mergeVerify: %s", body)
	}
}

func TestMergeUnverifiedOnCheckMismatch(t *testing.T) {
	h := newHarness(t)
	mergeFixture(t, h)
	// 成品复检签名不同（pix_fmt 变化）→ 降级为「未完整校验」但产物保留
	h.exec.probes["full"] = probeJSON(120, "yuvj420p")
	ctx := context.Background()
	h.svc.OnStartup(ctx)
	defer h.svc.OnShutdown(ctx)
	if _, err := h.svc.MergeStart("season", map[string]any{"id": "d1"}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, ok := h.mergeState("d1#001-002")
		return ok && m["state"] != MergeQueued && m["state"] != MergeRunning
	}, "合并应结束")
	m, _ := h.mergeState("d1#001-002")
	if m["state"] != MergeDone {
		t.Fatalf("校验失败应降级完成: %v (%v)", m["state"], m["detail"])
	}
	output, _ := m["output"].(string)
	if !strings.Contains(filepath.Base(output), "（未完整校验）") {
		t.Fatalf("产物名应含未校验标注: %q", output)
	}
	if m["detail"] == "" || m["detail"] == nil {
		t.Fatal("detail 应记录校验失败原因")
	}
}

func TestMergeCancelAndSkipVerify(t *testing.T) {
	h := newHarness(t)
	mergeFixture(t, h)
	if _, err := h.svc.SaveSettings(2, true, false); err != nil {
		t.Fatal(err)
	}
	h.exec.blockOn = "-f concat" // 合并视频段挂起，制造运行中取消窗口
	ctx := context.Background()
	h.svc.OnStartup(ctx)
	defer h.svc.OnShutdown(ctx)
	if _, err := h.svc.MergeStart("season", map[string]any{"id": "d1"}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, ok := h.mergeState("d1#001-002")
		return ok && m["state"] == MergeRunning
	}, "应进入 running")
	if _, err := h.svc.MergeCancel("d1#001-002"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, ok := h.mergeState("d1#001-002")
		return ok && m["state"] == MergeCanceled
	}, "应转为 canceled")
	m, _ := h.mergeState("d1#001-002")
	if m["detail"] != "合并已取消" {
		t.Fatalf("取消 detail = %v", m["detail"])
	}
	// mergeVerify=false：不得出现 -xerror 严格解码调用
	if got := h.exec.countContaining("-xerror"); got != 0 {
		t.Fatalf("关闭校验后仍有 -xerror 调用: %d", got)
	}
	// 重排同目标 queued 条目
	h.exec.blockOn = ""
	out1, err := h.svc.MergeStart("season", map[string]any{"id": "d1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = out1
	waitFor(t, func() bool {
		m, ok := h.mergeState("d1#001-002")
		return ok && m["state"] == MergeDone
	}, "重提合并应完成")
}

func TestMergeFailsOnMissingSource(t *testing.T) {
	h := newHarness(t)
	mergeFixture(t, h)
	h.core.handlers["localPlayback"] = func(map[string]any) (map[string]any, error) {
		return nil, fmt.Errorf("核心不可用 [local_media]")
	}
	// folder 兜底路径不存在 → 直接失败
	ctx := context.Background()
	h.svc.OnStartup(ctx)
	defer h.svc.OnShutdown(ctx)
	if _, err := h.svc.MergeStart("season", map[string]any{"id": "d1"}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, ok := h.mergeState("d1#001-002")
		return ok && m["state"] == MergeFailed
	}, "缺源应失败")
	m, _ := h.mergeState("d1#001-002")
	if !strings.Contains(m["detail"].(string), "本地文件缺失") {
		t.Fatalf("失败原因 = %v", m["detail"])
	}
	if h.core.count("workLease") != 2 {
		t.Fatalf("失败也应释放 lease: %d", h.core.count("workLease"))
	}
}
