// Package downloadsvc —— V2-3「下载与合并」服务层。
//
// 职责对齐 v1 downloads.py（DownloadPanel 的 Go 化）：
//   - 入队：detail chapters 选集 → 核心 enqueueDownloads；
//   - 轮询：1500ms 定时器拉核心 downloads 快照，补算 speed/eta（v1 语义：
//     核心没有速度字段，客户端按 ~8s 滑动窗口用 bytes 增量算）；
//   - 事件：快照 JSON 有变化时 EventsEmit("downloads:update", payloadJSON)；
//   - 控制：pause/resume/remove + failed→resume 重试映射 + pauseAll/resumeAll；
//   - 设置：downloadConcurrency/downloadBySource 走核心 resourceSettings
//     读-改-写（v1 core_client.save_download_settings 语义），mergeVerify
//     属客户端偏好，落 DataDir/downloads.state.json；
//   - 合并：v1 _merge_queue FIFO —— 同剧已完成分集 ≥2 排队，单 worker 串行
//     执行 ffmpeg 管线（merge.go），期间持有核心 workLease("media") 防止
//     合并中删除分集/迁移目录（对 v1 的偏差改进）。
//
// 依赖注入：Call 为核心 action 函数值（bindings 传 native.Call），测试注假核心；
// 本包不 import wails —— 事件推送经 Options.EventsEmit 闭包。
package downloadsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// EventName 轮询事件名（前端 stores/downloads.ts 订阅）。
	EventName = "downloads:update"
	// DefaultPollInterval v1 downloads.py POLL_MS=1500。
	DefaultPollInterval = 1500 * time.Millisecond
	// speedWindow v1 速度滑动窗口 ≈8s。
	speedWindow = 8 * time.Second
	// interruptedMark v1 _auto_resume_interrupted 判据。
	interruptedMark = "上次下载已暂停"
	// maxMergeHistory 合并条目上限（done/failed/canceled 记录会修剪）。
	maxMergeHistory = 50
)

// CallFunc 核心门面函数值（native.Call 同签名：应答 data 已平铺）。
type CallFunc func(action string, payload map[string]any) (map[string]any, error)

// EpisodeEntry 入队单集：chapter=detail chapters[i] 原样，index=数组下标。
type EpisodeEntry struct {
	Chapter map[string]any `json:"chapter"`
	Index   int            `json:"index"`
}

// Options 服务装配项。
type Options struct {
	Call         CallFunc
	EventsEmit   func(name string, data ...any)
	DataDirFunc  func() (string, error) // mergeVerify 持久化位置；空则仅内存
	PollInterval time.Duration          // ≤0 用 DefaultPollInterval
	Executor     Executor               // nil 用默认 ffmpeg/ffprobe 子进程实现
	Now          func() time.Time       // nil 用 time.Now（测试可注时钟）
}

type speedSample struct {
	at    time.Time
	bytes int64
}

// Service 下载/合并服务（并发安全）。
type Service struct {
	call         CallFunc
	emit         func(name string, data ...any)
	dataDir      func() (string, error)
	pollInterval time.Duration
	exec         Executor
	now          func() time.Time

	mu        sync.Mutex
	started   bool
	cancel    context.CancelFunc
	// runCtx OnStartup 存下来的运行上下文（给弹幕保存这类后台任务用）。
	// 为什么不用 refresh 的参数传：那样要改五个调用点，而其中三个本来就没有 ctx。
	runCtx context.Context
	wg        sync.WaitGroup
	mergeWake chan struct{}

	jobs          []map[string]any
	merges        []*MergeEntry
	mergeSeq      int
	status        string
	samples       map[string][]speedSample
	autoResumeRun bool
	lastSig       string
	mergeVerify   bool
	// R22a③「下载时保存弹幕」客户端开关（默认关，v1 tools.danmaku_download_enabled）。
	// 与 mergeVerify 同一个文件，所以读取/写入都走 sidecar.go 的 loadClientFlags/saveClientFlags
	// ——各写各的键会互相抹掉。
	danmakuDownload bool
	flagsLoaded     bool
	sidecarBusy     map[string]bool
	sidecarTries    map[string]int
	// sidecarGate 容量 1 的串行闸（v1 library.py 的 _danmaku_gate）。
	// ⚠ 这个字段被我用 python 批量改结构体时**整行删掉过一次**，
	// 而引用它的三行在另一个文件里，本机没有 Go 工具链看不见——CI 两个 job 同时报
	// "s.sidecarGate undefined"才抓住。跨文件的字段/引用，改动要两边一起过一遍。
	sidecarGate     chan struct{}
	sidecarReports  []SidecarReport
	// sidecarRetryDelay 两次弹幕尝试之间的等待；默认 SidecarRetryDelay，
	// 测试压到毫秒级才有办法真跑完三轮（不压就得让 CI 白等 40 秒）。
	sidecarRetryDelay time.Duration
	prevStates      map[string]string
	// 交付 A：merges 表快照持久化（merge_state.go）
	mergesLoaded     bool
	mergesFlushTimer *time.Timer
}

// New 构造服务；Call 缺失时各方法返回错误（不 panic，便于壳降级）。
func New(opts Options) *Service {
	interval := opts.PollInterval
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	exec := opts.Executor
	if exec == nil {
		exec = newCommandExecutor()
	}
	return &Service{
		call:         opts.Call,
		emit:         opts.EventsEmit,
		dataDir:      opts.DataDirFunc,
		pollInterval: interval,
		exec:         exec,
		now:          now,
		mergeWake:    make(chan struct{}, 8),
		samples:      map[string][]speedSample{},
		mergeVerify:  true,
		sidecarRetryDelay: SidecarRetryDelay,
		sidecarGate:       make(chan struct{}, 1),
	}
}

// OnStartup 启动轮询与合并 worker（幂等；player.Service 同款生命周期钩子）。
func (s *Service) OnStartup(ctx context.Context) {
	if s.call == nil {
		return
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	// 交付 A：恢复 merges 快照；重启前排队中的条目直接唤醒 worker 续跑
	// （runMergeEntry 执行时重挂 jobs，见 merge_state.go）。
	s.loadMergesLocked()
	resumeQueued := s.hasQueuedMergesLocked()
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	// 弹幕保存要挂在运行上下文上（关窗就得收）。
	// 不用 refresh 的参数传：五个调用点里三个本来就没有 ctx（见 refresh 注释）。
	s.runCtx = runCtx
	s.wg.Add(2)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		s.pollLoop(runCtx)
	}()
	go func() {
		defer s.wg.Done()
		s.workerLoop(runCtx)
	}()
	if resumeQueued {
		s.wakeWorker()
	}
}

// OnShutdown 停轮询、取消进行中的合并并等 worker 退出。
func (s *Service) OnShutdown(_ context.Context) {
	s.mu.Lock()
	cancel := s.cancel
	for _, m := range s.merges {
		if m.State == MergeRunning && m.cancel != nil {
			m.cancel()
		}
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	// 交付 A：退出前立即落盘（worker 超时未退时快照仍含 running，
	// 下次启动按「重启中断」落定，语义自洽）。
	s.flushMergesNow()
}

// ---------- 轮询与事件 ----------

func (s *Service) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.refresh(true)
		}
	}
}

// refresh 拉核心 downloads 快照；emit=true 时按需推送事件。
func (s *Service) refresh(emit bool) ([]map[string]any, error) {
	if s.call == nil {
		return nil, errors.New("下载服务未接线（缺少核心门面）")
	}
	data, err := s.call("downloads", nil)
	if err != nil {
		return nil, err
	}
	jobs := extractJobs(data)
	s.mu.Lock()
	s.applyJobsLocked(jobs)
	first := !s.autoResumeRun
	s.autoResumeRun = true
	// R22a③：找出新变成 completed 的 job（弹幕只在"完成的那一刻"抓一次，
	// 与 v1 一致；第一遍刷新不算跃迁，否则每次启动都把历史重抓一遍）。
	fresh, next := noteCompletedTransition(s.prevStates, jobs)
	runCtx := s.runCtx
	s.prevStates = next
	s.mu.Unlock()
	if first {
		s.autoResumeInterrupted(jobs)
	}
	// 弹幕保存挂在"新完成"那一刻，且在锁外跑（v1 也是丢给后台线程）。
	// runCtx 来自 OnStartup：关窗时它会取消，正在翻弹幕的 goroutine 跟着收。
	s.queueSidecars(runCtx, fresh)
	if emit {
		s.notify()
	}
	return jobs, nil
}

// applyJobsLocked 补算 speed/eta（v1 _job_speed 语义：8s 窗口 bytes 增量）。
func (s *Service) applyJobsLocked(jobs []map[string]any) {
	now := s.now()
	seen := make(map[string]bool, len(jobs))
	for _, job := range jobs {
		id, _ := job["id"].(string)
		if id == "" {
			continue
		}
		seen[id] = true
		state, _ := job["state"].(string)
		bytes := numField(job["bytes"])
		var speed float64
		if state == "downloading" {
			window := s.samples[id]
			if n := len(window); n > 0 && int64(bytes) < window[n-1].bytes {
				window = nil // bytes 回退=断点重开，窗口清零
			}
			window = append(window, speedSample{at: now, bytes: int64(bytes)})
			for len(window) > 1 && now.Sub(window[0].at) > speedWindow {
				window = window[1:]
			}
			if len(window) >= 2 {
				if dt := window[len(window)-1].at.Sub(window[0].at).Seconds(); dt >= 1 {
					speed = float64(window[len(window)-1].bytes-window[0].bytes) / dt
				}
			}
			s.samples[id] = window
		} else {
			delete(s.samples, id)
		}
		job["speed"] = round2(speed)
		eta := float64(0)
		total := numField(job["total"])
		if state == "downloading" && speed > 0 && total > bytes {
			eta = roundUp((total - bytes) / speed)
		}
		job["eta"] = eta
	}
	for id := range s.samples {
		if !seen[id] {
			delete(s.samples, id)
		}
	}
	s.jobs = jobs
}

// autoResumeInterrupted v1：上次会话被强杀留下的「上次下载已暂停」任务，
// 本次会话自动恢复一次。
func (s *Service) autoResumeInterrupted(jobs []map[string]any) {
	count := 0
	for _, job := range jobs {
		id, _ := job["id"].(string)
		state, _ := job["state"].(string)
		errMsg, _ := job["error"].(string)
		if id != "" && state == "paused" && strings.Contains(errMsg, interruptedMark) {
			if _, err := s.Control(id, "resume"); err == nil {
				count++
			}
		}
	}
	if count > 0 {
		s.mu.Lock()
		s.status = fmt.Sprintf("已自动恢复 %d 个中断的下载任务", count)
		s.mu.Unlock()
		s.notify()
	}
}

// notify 快照 JSON 变化才推事件。
func (s *Service) notify() {
	s.mu.Lock()
	body, err := json.Marshal(s.payloadLocked())
	sig := string(body)
	changed := err == nil && sig != s.lastSig
	if changed {
		s.lastSig = sig
	}
	emit := s.emit
	s.mu.Unlock()
	if !changed || emit == nil {
		return
	}
	emit(EventName, sig)
}

// Snapshot 绑定 DownloadList：强制刷新一次并回 {jobs,merges,status,mergeVerify}。
func (s *Service) Snapshot() (map[string]any, error) {
	_, err := s.refresh(false)
	s.mu.Lock()
	payload := s.payloadLocked()
	s.mu.Unlock()
	return payload, err
}

func (s *Service) payloadLocked() map[string]any {
	s.loadMergesLocked() // 交付 A：未跑 OnStartup 的调用方（绑定直连）也可见恢复表
	jobs := s.jobs
	if jobs == nil {
		jobs = []map[string]any{}
	}
	merges := s.merges
	if merges == nil {
		merges = []*MergeEntry{}
	}
	return map[string]any{
		"jobs":        jobs,
		"merges":      merges,
		"status":          s.status,
		"mergeVerify":     s.mergeVerifyLocked(),
		// R22a③：轮询面也带上，前端勾选状态才能在"另一处也改了开关"后自我纠正
		"danmakuDownload": s.danmakuDownloadEnabled(),
	}
}

// ---------- 队列控制（v1 ROW_ACTIONS / _control_all 语义） ----------

var controlCommands = map[string]bool{
	"pause": true, "resume": true, "remove": true, "archive": true, "restore": true,
}

// Enqueue 选集入队（核心侧做去重/上限/并发槽位）。
func (s *Service) Enqueue(drama map[string]any, entries []EpisodeEntry, quality int) (map[string]any, error) {
	if s.call == nil {
		return nil, errors.New("下载服务未接线（缺少核心门面）")
	}
	id, _ := drama["id"].(string)
	source, _ := drama["source"].(string)
	if id == "" || source == "" {
		return nil, errors.New("剧集信息不完整")
	}
	if len(entries) == 0 {
		return nil, errors.New("请先选择要下载的分集")
	}
	if len(entries) > 500 {
		return nil, errors.New("单次最多添加 500 集")
	}
	payload := map[string]any{
		"drama":   drama,
		"entries": entries,
		"quality": quality,
		"force":   false,
	}
	data, err := s.call("enqueueDownloads", payload)
	if err != nil {
		return nil, err
	}
	s.notify()
	out := map[string]any{"added": 0}
	if data != nil {
		if added, ok := data["added"]; ok {
			out["added"] = added
		}
	}
	return out, nil
}

// Control 单任务命令（pause/resume/remove/archive/restore）。
func (s *Service) Control(id, command string) (map[string]any, error) {
	if s.call == nil {
		return nil, errors.New("下载服务未接线（缺少核心门面）")
	}
	if id == "" || !controlCommands[command] {
		return nil, errors.New("不支持的下载任务操作")
	}
	if _, err := s.call("controlDownloads", map[string]any{"jobId": id, "command": command}); err != nil {
		return nil, err
	}
	s.notify()
	return map[string]any{"ok": true}, nil
}

// Retry v1：核心没有独立 retry 命令，失败任务「重试」=resume。
func (s *Service) Retry(id string) (map[string]any, error) { return s.Control(id, "resume") }

// Cancel 删除任务（v1 remove：核心侧删记录+删文件）。
func (s *Service) Cancel(id string) (map[string]any, error) { return s.Control(id, "remove") }

// ControlAll 全部暂停/继续（v1 _control_all）。
func (s *Service) ControlAll(command string) (map[string]any, error) {
	if s.call == nil {
		return nil, errors.New("下载服务未接线（缺少核心门面）")
	}
	if command != "pauseAll" && command != "resumeAll" {
		return nil, errors.New("不支持的批量操作")
	}
	if _, err := s.call("controlDownloads", map[string]any{"jobId": "", "command": command}); err != nil {
		return nil, err
	}
	s.notify()
	return map[string]any{"ok": true}, nil
}

// RemoveBatch 批量删除（核心 removeDownloads：{completed, failures}）。
func (s *Service) RemoveBatch(ids []string) (map[string]any, error) {
	if s.call == nil {
		return nil, errors.New("下载服务未接线（缺少核心门面）")
	}
	if len(ids) == 0 {
		return map[string]any{"completed": []string{}, "failures": map[string]string{}}, nil
	}
	data, err := s.call("removeDownloads", map[string]any{"jobIds": ids})
	if err != nil {
		return nil, err
	}
	s.notify()
	out := map[string]any{"completed": []string{}, "failures": map[string]string{}}
	if data != nil {
		for _, key := range []string{"completed", "failures"} {
			if v, ok := data[key]; ok {
				out[key] = v
			}
		}
	}
	return out, nil
}

// ---------- 设置（v1 core_client.save_download_settings 读-改-写语义） ----------

// Settings 资源设置 + 客户端两个旗标（mergeVerify、danmakuDownload）的合并视图。
func (s *Service) Settings() (map[string]any, error) {
	if s.call == nil {
		return nil, errors.New("下载服务未接线（缺少核心门面）")
	}
	data, err := s.call("resourceSettings", nil)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for k, v := range data {
		out[k] = v
	}
	s.mu.Lock()
	out["mergeVerify"] = s.mergeVerifyLocked()
	// R22a③：设置面也回显开关（前端勾选框的唯一真相，不在 localStorage 另存一份）
	out["danmakuDownload"] = s.danmakuDownloadEnabled()
	s.mu.Unlock()
	return out, nil
}

// SaveSettings 只改下载两项 + 校验开关，其余字段原样回写（v1 语义：
// 读全量 settings → 打补丁 → saveResourceSettings，避免误清 proxy 设置）。
// 并发槽位钳到 1..8（v1 下载设置面板 spin 范围）。
func (s *Service) SaveSettings(concurrency int, bySource bool, verify bool) (map[string]any, error) {
	if s.call == nil {
		return nil, errors.New("下载服务未接线（缺少核心门面）")
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 8 {
		concurrency = 8
	}
	current, err := s.call("resourceSettings", nil)
	if err != nil {
		return nil, err
	}
	patch := map[string]any{}
	for _, key := range []string{"proxyMode", "proxyUrl", "catalogConcurrency", "catalogIntervalMs"} {
		if v, ok := current[key]; ok && v != nil {
			patch[key] = v
		}
	}
	patch["downloadConcurrency"] = concurrency
	patch["downloadBySource"] = bySource
	if _, err := s.call("saveResourceSettings", map[string]any{"settings": patch}); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.mergeVerify = verify
	// 只留一把"读过盘"的闩（flagsLoaded）：两个旗标同文件，
	// 各留各的闩会一个读盘一个不读，行为随调用顺序变。
	s.flagsLoaded = true
	s.mu.Unlock()
	// 两个旗标一起写：只写 mergeVerify 会把 danmakuDownload 抹掉（同文件不同键）
	if err := s.saveClientFlags(); err != nil {
		return nil, err
	}
	s.notify()
	return s.Settings()
}

// mergeVerifyLocked 首次访问从 DataDir 状态文件恢复（默认开）。
func (s *Service) mergeVerifyLocked() bool {
	// 读盘收敛到 sidecar.go 的 loadClientFlagsLocked：那里一次读回**两个**旗标。
	// 原来这里自己读一遍只取 mergeVerify，是第二处解析同一文件的地方——
	// 加第二个键之后如果继续留着，读到的就是两份不同步的真相。
	s.loadClientFlagsLocked()
	return s.mergeVerify
}

// stateDir DataDir 解析（nil/失败 → 空串 = 内存降级模式）。
func (s *Service) stateDir() string {
	if s.dataDir == nil {
		return ""
	}
	dir, err := s.dataDir()
	if err != nil || dir == "" {
		return ""
	}
	return dir
}

func (s *Service) verifyStatePath() string {
	dir := s.stateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "downloads.state.json")
}

// saveVerifyState 保留给老调用点，但实现改为"两个旗标一起写"。
// 单独写 mergeVerify 会把 danmakuDownload 抹掉——同一文件里的键必须一起落盘。
func (s *Service) saveVerifyState(verify bool) error {
	path := s.verifyStatePath()
	if path == "" {
		return nil
	}
	body, err := json.Marshal(map[string]any{"mergeVerify": verify})
	if err != nil {
		return err
	}
	// 原 os.WriteFile 直写升级为 tmp+rename 原子写（模式保持 0644）。
	return writeFileAtomic(path, body, 0o644)
}

// ---------- 合并队列（v1 _merge_queue FIFO 语义） ----------

// MergeEntry 合并任务行（事件载荷 merges[]，字段与前端 types.MergeEntry 对齐）。
type MergeEntry struct {
	ID       string  `json:"id"`
	DramaID  string  `json:"dramaId"`
	Title    string  `json:"title"`
	Episodes []int   `json:"episodes"`
	Count    int     `json:"count"`
	State    string  `json:"state"` // queued|running|done|failed|canceled
	Stage    string  `json:"stage,omitempty"`
	Progress float64 `json:"progress"`
	Detail   string  `json:"detail,omitempty"`
	Output   string  `json:"output,omitempty"`
	// OutputName 是排队时就定下的**目标文件名**（只有「按季合并选中」会设，
	// 单剧合并留空走默认 "剧名 全001-002集.mkv"）。
	// 为什么单开一个字段而不是直接写 Output：`Output` 在 v1/v2 里都表示
	// "成品已落盘的绝对路径"，合并没完成之前它是空的；把排队时的名字塞进 Output
	// 会让 UI 显示一个还不存在的路径（我上一轮就是这么写的，本轮复核才发现）。
	OutputName string  `json:"outputName,omitempty"`
	Bytes    int64   `json:"bytes,omitempty"`

	jobs      []map[string]any
	cancel    context.CancelFunc
	createdAt time.Time
	// restored 交付 A：本条目来自启动快照恢复，jobs 需执行时重挂。
	restored bool
}

// 合并状态常量。
const (
	MergeQueued   = "queued"
	MergeRunning  = "running"
	MergeDone     = "done"
	MergeFailed   = "failed"
	MergeCanceled = "canceled"
)

// MergeList 当前合并条目（绑定 MergeList）。
func (s *Service) MergeList() []map[string]any {
	s.mu.Lock()
	s.loadMergesLocked()
	defer s.mu.Unlock()
	body, err := json.Marshal(s.merges)
	if err != nil {
		return []map[string]any{}
	}
	var out []map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return []map[string]any{}
	}
	return out
}

// MergeStart 触发合并。mode=season：drama 全剧已完成分集；mode=selected：显式 jobIDs。
// 同剧已有排队条目则刷新其分集；正在合并则拒绝（v1 _start_batch 同语义）。
func (s *Service) MergeStart(mode string, drama map[string]any, jobIDs []string) (map[string]any, error) {
	if mode == "" {
		mode = "season"
	}
	if mode != "season" && mode != "selected" {
		return nil, errors.New("不支持的合并方式")
	}
	if _, err := s.refresh(false); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.loadMergesLocked()
	candidates := make([]map[string]any, 0, len(s.jobs))
	for _, job := range s.jobs {
		state, _ := job["state"].(string)
		if state == "completed" {
			candidates = append(candidates, job)
		}
	}
	var selected []map[string]any
	dramaID := ""
	title := ""
	switch mode {
	case "season":
		if drama != nil {
			dramaID, _ = drama["id"].(string)
		}
		if dramaID == "" {
			s.mu.Unlock()
			return nil, errors.New("缺少剧集信息")
		}
		for _, job := range candidates {
			if jobDramaID(job) == dramaID {
				selected = append(selected, job)
			}
		}
	case "selected":
		want := make(map[string]bool, len(jobIDs))
		for _, id := range jobIDs {
			want[id] = true
		}
		if len(want) == 0 {
			s.mu.Unlock()
			return nil, errors.New("请先勾选要合并的分集")
		}
		for _, job := range candidates {
			id, _ := job["id"].(string)
			if want[id] {
				selected = append(selected, job)
			}
		}
		if len(selected) != len(want) {
			s.mu.Unlock()
			return nil, errors.New("只有已完成的分集可以合并，请检查勾选")
		}
		for _, job := range selected {
			if jobDramaID(job) != "" && dramaID == "" {
				dramaID = jobDramaID(job)
				title = jobTitle(job)
			} else if jobDramaID(job) != dramaID {
				s.mu.Unlock()
				return nil, errors.New("请选择同一部剧的分集合并")
			}
		}
	}
	if len(selected) < 2 {
		s.mu.Unlock()
		return nil, fmt.Errorf("《%s》可合并的已完成分集不足 2 集", title)
	}
	sort.SliceStable(selected, func(i, j int) bool { return jobIndex(selected[i]) < jobIndex(selected[j]) })
	episodes := make([]int, 0, len(selected))
	for _, job := range selected {
		episodes = append(episodes, episodeOfJob(job))
	}
	if title == "" {
		title = jobTitle(selected[0])
	}
	if dramaID == "" {
		dramaID = jobDramaID(selected[0])
	}
	first, last := episodes[0], episodes[len(episodes)-1]
	id := fmt.Sprintf("%s#%03d-%03d", dramaID, first, last)
	for _, m := range s.merges {
		if m.ID == id && m.State == MergeRunning {
			s.mu.Unlock()
			return nil, errors.New("该剧这段分集正在合并中，请等待完成")
		}
	}
	s.mergeSeq++
	entry := &MergeEntry{
		ID:        id,
		DramaID:   dramaID,
		Title:     title,
		Episodes:  episodes,
		Count:     len(selected),
		State:     MergeQueued,
		Stage:     "排队中",
		jobs:      selected,
		createdAt: s.now(),
	}
	replaced := false
	for i, m := range s.merges {
		if m.ID == id && m.State == MergeQueued {
			s.merges[i] = entry // v1 _start_batch：未开始的同目标重排刷新分集
			replaced = true
			break
		}
	}
	if !replaced {
		s.merges = append(s.merges, entry)
		for len(s.merges) > maxMergeHistory {
			s.merges = s.merges[1:]
		}
	}
	s.mu.Unlock()
	s.wakeWorker()
	s.markMergesDirty() // 交付 A：入队/替换即变更表
	s.notify()
	return map[string]any{"queued": 1, "replaced": replaced, "episodes": episodes}, nil
}

// MergeCancel id="" 取消全部（运行中 + 队列）；否则单条。
func (s *Service) MergeCancel(id string) (map[string]any, error) {
	s.mu.Lock()
	s.loadMergesLocked()
	var cancels []context.CancelFunc
	for _, m := range s.merges {
		if id != "" && m.ID != id {
			continue
		}
		switch m.State {
		case MergeRunning:
			if m.cancel != nil {
				cancels = append(cancels, m.cancel)
			}
		case MergeQueued:
			m.State = MergeCanceled
			m.Stage = ""
			m.Detail = "已取消排队"
			cancels = append(cancels, nil)
		}
	}
	s.mu.Unlock()
	for _, c := range cancels {
		if c != nil {
			c()
		}
	}
	s.markMergesDirty() // 交付 A：取消即终态变更
	s.notify()
	return map[string]any{"ok": true}, nil
}

func (s *Service) wakeWorker() {
	select {
	case s.mergeWake <- struct{}{}:
	default:
	}
}

func (s *Service) workerLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.mergeWake:
		}
		for {
			entry := s.nextQueued()
			if entry == nil {
				break
			}
			s.runMergeEntry(ctx, entry)
			s.notify()
		}
	}
}

func (s *Service) nextQueued() *MergeEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadMergesLocked()
	for _, m := range s.merges {
		if m.State == MergeQueued {
			m.State = MergeRunning
			m.Stage = "准备中"
			m.Progress = 0
			m.Detail = ""
			s.markMergesDirtyLocked() // 交付 A：queued→running 跃迁落盘
			return m
		}
	}
	return nil
}

// runMergeEntry 串行执行一条合并：workLease("media") 全程持有
// （核心侧 mediaBusy 会挡住「合并中删除分集/迁移下载目录」，对 v1 的改进）。
func (s *Service) runMergeEntry(ctx context.Context, entry *MergeEntry) {
	runCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	entry.cancel = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		entry.cancel = nil
		s.mu.Unlock()
	}()

	// 交付 A：快照恢复的 queued 条目没有 jobs（不可导出字段无法持久化），
	// 执行时按 DramaID+Episodes 从核心 completed 记录重挂；重挂失败如实
	// 标 failed，不静默降级（父代理修正第 3 条，方案 a）。
	s.mu.Lock()
	needReattach := entry.restored
	s.mu.Unlock()
	if needReattach {
		if _, err := s.refresh(false); err != nil {
			s.finishMerge(entry, MergeFailed, err.Error())
			return
		}
		s.mu.Lock()
		entry.jobs = reattachMergeJobs(s.jobs, entry)
		entry.restored = false
		n := len(entry.jobs)
		if n >= 2 {
			// 集号以实际重建为准（产物名/完成文案随之，防部分命中错标）。
			episodes := make([]int, 0, n)
			for _, job := range entry.jobs {
				episodes = append(episodes, episodeOfJob(job))
			}
			entry.Episodes = episodes
			entry.Count = n
		}
		s.mu.Unlock()
		if n < 2 {
			s.finishMerge(entry, MergeFailed, reattachFailMsg)
			return
		}
	}

	leased := false
	if s.call != nil {
		if _, err := s.call("workLease", map[string]any{"jobId": "media", "command": "start"}); err == nil {
			leased = true
		} else {
			s.finishMerge(entry, MergeFailed, err.Error())
			return
		}
		defer func() {
			if leased && s.call != nil {
				_, _ = s.call("workLease", map[string]any{"jobId": "media", "command": "end"})
			}
		}()
	}

	result, err := s.runMerge(runCtx, entry)
	switch {
	case err == nil:
		s.mu.Lock()
		entry.State = MergeDone
		entry.Stage = ""
		entry.Progress = 1
		if p, ok := result["path"].(string); ok {
			entry.Output = p
		}
		entry.Bytes, _ = result["bytes"].(int64)
		if unverified, _ := result["unverified"].(bool); unverified {
			entry.Detail = "产物未通过完整解码校验，文件名已标注"
		}
		s.status = fmt.Sprintf("《%s》合并完成：全%03d-%03d集", entry.Title, entry.Episodes[0], entry.Episodes[len(entry.Episodes)-1])
		s.mu.Unlock()
		s.markMergesDirty() // 交付 A：done 终态落盘
	case errors.Is(err, context.Canceled):
		s.finishMerge(entry, MergeCanceled, "合并已取消")
	default:
		s.finishMerge(entry, MergeFailed, err.Error())
	}
}

func (s *Service) finishMerge(entry *MergeEntry, state, detail string) {
	s.mu.Lock()
	entry.State = state
	entry.Stage = ""
	entry.Detail = detail
	if state == MergeFailed {
		s.status = fmt.Sprintf("《%s》合并失败：%s", entry.Title, detail)
	}
	s.mu.Unlock()
	s.markMergesDirty() // 交付 A：failed/canceled 终态落盘
}

// ---------- helpers ----------

func extractJobs(data map[string]any) []map[string]any {
	raw, _ := data["jobs"].([]any)
	jobs := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			jobs = append(jobs, m)
		}
	}
	return jobs
}

func numField(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func jobIndex(job map[string]any) int { return int(numField(job["index"])) }

// episodeOfJob 解析 job 的真实集号（1-based）。
// 核心协议里 index 即集号（enqueueDownloads 要求 ≥1，"下载集数无效"），
// chapter.currentEpisode 是最可靠锚点；历史 0-based 记录（旧前端按下标提交）
// 用 currentEpisode 纠正，两者皆缺时按旧规则 index+1 兜底。
func episodeOfJob(job map[string]any) int {
	ix := jobIndex(job)
	if ch, ok := job["chapter"].(map[string]any); ok {
		if cur := int(numField(ch["currentEpisode"])); cur > 0 {
			return cur
		}
	}
	if ix >= 1 {
		return ix
	}
	return ix + 1
}

func jobDramaID(job map[string]any) string {
	drama, _ := job["drama"].(map[string]any)
	id, _ := drama["id"].(string)
	return id
}

func jobTitle(job map[string]any) string {
	drama, _ := job["drama"].(map[string]any)
	title, _ := drama["title"].(string)
	return title
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

func roundUp(v float64) float64 { return float64(int64(v + 0.5)) }
