package downloadsvc

// sidecar.go —— 「下载时保存弹幕」的客户端侧（v1 library.py:413-475）。
//
// v1 的形状：每集解密完成 → `_queue_danmaku` → 后台线程 `_collect_danmaku`：
// 拿会话 → 整集抓弹幕 → 把同名 `.ass` + `.danmaku.json` 写在**成品文件旁边**
// （写文件那半段在 `danmaku.CollectSidecar`，前一轮已落地并有单测）。
// "这一集的成品文件在哪"复用合并那条同一个函数
// `resolveEpisodeSource(job, root)`（核心 `localPlayback` → 本地文件，
// 兜底 `<root>/<folder||jobId>/` 里最大的媒体文件），避免出现第三套路径规则。
//
// ⚠ 三处与 v1 **不等**的地方，都来自 v2 的核心契约，不是我省略了：
//   1. v1 的 resolve 带 `force=True, session="danmaku-collect"`；
//      v2 核心 resolve 入参只有 `{drama, chapter, index, quality}`
//      （`internal/native/actions.go:34` 与 `guoapp-src/native/core/app_runtime.go:536`），
//      既没有 force 也没有调用方命名的会话 → 这里拿到的可能是核心缓存里的既有会话。
//      要补齐得先动核心契约，是单独一件事，不在这里假装等价。
//   2. v1 `fetch_all(core, session, duration_ms)` 靠传入时长翻窗口；
//      v2 的 `danmakuAll` 只收 `playbackSession`（`internal/native/actions.go:114`），
//      翻完由核心自己负责。于是 v2 **不需要**先 ffprobe 出时长来分页。
//   3. 但 `.ass` 的时间轴仍然需要一个总时长当事件上限（`BuildAss(items, durationMs)`），
//      而 v2 核心既不在 job 行里给 duration、resolve 也不回 durationSeconds。
//      这里取 `chapter.duration`（有就用），否则用**最后一条弹幕时间 + 60s** 近似，
//      并把它当近似写清楚：近似只会让最后 60s 内没有弹幕飞入的余量，
//      不会截断已有弹幕（事件时间都来自 items 本身）。
//
// 开关默认**关**（v1 `tools.danmaku_download_enabled` 默认 False；
// tooltip 原话「勾选后，每集解密完成会额外保存同名 .ass 字幕和 .danmaku.json；默认关闭」）。

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"zhenguojian/client-v2/internal/danmaku"
)

// SidecarAssTailSeconds 用"最后一条弹幕 + N 秒"近似总时长时加的余量（见文件头第 3 条）。
const SidecarAssTailSeconds = 60

// SidecarAttempts 一集最多试几次。
// 为什么需要重试（v1 不需要、v2 需要，这是两版结构差异逼出来的）：
// v1 是**自己**把分集解密落盘后才在 library 里写 entry，路径由它自己保证存在；
// v2 的 completed 状态来自核心轮询，"状态已是 completed"与"文件已经落地"之间
// 可以差一两个轮询周期。没有重试的话，一次撞空就永久丢掉这一集的弹幕
// ——因为状态跃迁只会发生一次，`noteCompletedTransition` 不会再来第二次。
const SidecarAttempts = 3

// SidecarRetryDelay 两次尝试之间的等待（字段可注入，测试里压到毫秒）。
const SidecarRetryDelay = 20 * time.Second

// SidecarOutcome 一条收集的结果分类，供测试与诊断读。
type SidecarOutcome string

const (
	SidecarSkippedDisabled SidecarOutcome = "disabled"
	SidecarSkippedReady    SidecarOutcome = "ready"
	SidecarRan             SidecarOutcome = "written"
	SidecarEmpty           SidecarOutcome = "empty"  // 本集没有弹幕：不写空文件
	SidecarFailed          SidecarOutcome = "failed" // 弹幕保存失败，与下载本身无关
)

// SidecarReport 一集的结果（最近 N 条留在内存里，供设置页诊断与测试断言）。
type SidecarReport struct {
	JobID   string `json:"jobId"`
	Outcome string `json:"outcome"`
	Items   int    `json:"items"`
	Err     string `json:"err,omitempty"`
}

// ---------------------------------------------------------------- 开关（两个旗标同一个文件）

// danmakuDownloadEnabled 读客户端开关。默认关，且与 mergeVerify 共用 `downloads.state.json`。
func (s *Service) danmakuDownloadEnabled() bool {
	s.loadClientFlagsLocked()
	return s.danmakuDownload
}

// loadClientFlagsLocked 一次读盘把**两个**客户端旗标都读进来（只读一次）。
// 为什么必须一起读：这两个键写在同一个文件里，各读各的就会各写各的，
// 后写的把先写的抹掉（v1 没有这个问题，因为 v1 的两个键在不同文件）。
func (s *Service) loadClientFlagsLocked() {
	if s.flagsLoaded {
		return
	}
	s.flagsLoaded = true
	path := s.verifyStatePath()
	if path == "" {
		return // 内存降级：沿用结构体默认（mergeVerify=true、danmakuDownload=false）
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return // 文件不存在 = 没存过，正常
	}
	var st struct {
		MergeVerify     *bool `json:"mergeVerify"`
		DanmakuDownload *bool `json:"danmakuDownload"`
	}
	if json.Unmarshal(body, &st) != nil {
		return // 坏文件按默认值处理（与 merge_state 的坏表回退同一策略）
	}
	if st.MergeVerify != nil {
		s.mergeVerify = *st.MergeVerify
	}
	if st.DanmakuDownload != nil {
		s.danmakuDownload = *st.DanmakuDownload
	}
}

// saveClientFlags 两个旗标一起落盘（配合上面那句"不要各写各的键"）。
func (s *Service) saveClientFlags() error {
	path := s.verifyStatePath()
	if path == "" {
		return nil
	}
	body, err := json.Marshal(map[string]any{
		"mergeVerify":     s.mergeVerify,
		"danmakuDownload": s.danmakuDownload,
	})
	if err != nil {
		return err
	}
	return writeFileAtomic(path, body, 0o644)
}

// SetDanmakuDownload 客户端开关（v1 那个「下载时保存弹幕」勾选框的真值）。
// 返回落盘错误：开关是要留档的设置，静默失败就等于下次启动悄悄变回去。
func (s *Service) SetDanmakuDownload(on bool) error {
	s.mu.Lock()
	s.loadClientFlagsLocked()
	if s.danmakuDownload == on {
		s.mu.Unlock()
		return nil
	}
	s.danmakuDownload = on
	err := s.saveClientFlags()
	s.mu.Unlock()
	return err
}

// DanmakuDownload 当前开关（进页回显，照 mergeVerify 同一手法）。
func (s *Service) DanmakuDownload() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadClientFlagsLocked()
	return s.danmakuDownload
}

// SidecarReports 最近的结果（最多 20 条，新的在前）。
func (s *Service) SidecarReports() []SidecarReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SidecarReport, len(s.sidecarReports))
	copy(out, s.sidecarReports)
	return out
}

func (s *Service) recordSidecarReport(rep SidecarReport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sidecarReports = append([]SidecarReport{rep}, s.sidecarReports...)
	if len(s.sidecarReports) > 20 {
		s.sidecarReports = s.sidecarReports[:20]
	}
}

// ---------------------------------------------------------------- 触发

// noteCompletedTransition 返回**这一轮新变成 completed** 的 job，并给出新的状态表。
//
// 为什么第一遍刷新一个都不算：`prev` 为空时如果也当成跃迁，
// 每次启动都会把历史里所有已完成集重抓一遍弹幕（几十次 resolve + 几十次整集翻窗），
// v1 也只在"完成的那一刻"抓（`_queue_danmaku` 挂在 entry 完成路径上）。
func noteCompletedTransition(prev map[string]string, jobs []map[string]any) ([]map[string]any, map[string]string) {
	next := make(map[string]string, len(jobs))
	var fresh []map[string]any
	for _, job := range jobs {
		id, _ := job["id"].(string)
		if id == "" {
			continue
		}
		state, _ := job["state"].(string)
		next[id] = state
		if state != "completed" {
			continue
		}
		if was, seen := prev[id]; seen && was != "completed" {
			fresh = append(fresh, job)
		}
	}
	sort.SliceStable(fresh, func(i, j int) bool {
		a, _ := fresh[i]["id"].(string)
		b, _ := fresh[j]["id"].(string)
		return a < b
	})
	return fresh, next
}

// queueSidecars 给新完成的分集排一次弹幕保存。
// 并发规则照 v1：同一个 job 只跑一次（`_danmaku_busy`），并且整轮**串行**
// （v1 那把 `_danmaku_gate` 就是容量 1 的渠道）——这一步要打 resolve + 翻整集弹幕，
// 一起放出去是给上游上压力，也与下载并发槽位无关，不该抢。
func (s *Service) queueSidecars(ctx context.Context, jobs []map[string]any) {
	if s.call == nil || len(jobs) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if !s.danmakuDownloadEnabled() {
		s.mu.Unlock()
		return
	}
	if s.sidecarBusy == nil {
		s.sidecarBusy = map[string]bool{}
	}
	if s.sidecarGate == nil {
		s.sidecarGate = make(chan struct{}, 1)
	}
	pending := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		id, _ := job["id"].(string)
		if id == "" || s.sidecarBusy[id] {
			continue
		}
		s.sidecarBusy[id] = true
		pending = append(pending, job)
	}
	gate := s.sidecarGate
	s.mu.Unlock()

	for _, job := range pending {
		job := job
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			gate <- struct{}{}
			defer func() { <-gate }()

			id, _ := job["id"].(string)
			rep := s.collectWithRetry(ctx, job, id)
			s.recordSidecarReport(rep)

			s.mu.Lock()
			delete(s.sidecarBusy, id)
			delete(s.sidecarTries, id)
			s.mu.Unlock()
		}()
	}
}

// collectWithRetry 试到文件落地为止，最多 SidecarAttempts 次。
// 单独抽出来是因为循环里要同时管三件事：次数、关窗取消、以及"别再上第二次锁"。
func (s *Service) collectWithRetry(ctx context.Context, job map[string]any, id string) SidecarReport {
	var rep SidecarReport
	for attempt := 1; attempt <= SidecarAttempts; attempt++ {
		rep = s.collectSidecarForJob(ctx, job)
		if rep.Outcome != string(SidecarFailed) {
			return rep
		}
		if attempt == SidecarAttempts {
			return rep // 次数用尽：如实留最后一次错误，不粉饰成"跳过"
		}
		if ctx.Err() != nil {
			rep.Err += "（关窗中断，未再重试）"
			return rep
		}
		s.mu.Lock()
		delay := s.sidecarRetryDelay
		if s.sidecarTries == nil {
			s.sidecarTries = map[string]int{}
		}
		s.sidecarTries[id] = attempt // 让外部看得见"这一集在第几次尝试上"
		s.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			rep.Err += "（关窗中断，未再重试）"
			return rep
		case <-timer.C:
		}
	}
	return rep
}

// collectSidecarForJob 跑一集的采集。任何失败只记进 report：
// 弹幕是附属品，不该影响下载与合并那条路。
func (s *Service) collectSidecarForJob(ctx context.Context, job map[string]any) SidecarReport {
	id, _ := job["id"].(string)
	rep := SidecarReport{JobID: id, Outcome: string(SidecarFailed)}

	videoPath, err := s.sidecarVideoPath(job)
	if err != nil {
		rep.Err = err.Error()
		return rep
	}
	if danmaku.SidecarReady(videoPath) {
		rep.Outcome = string(SidecarSkippedReady)
		return rep
	}

	drama, _ := job["drama"].(map[string]any)
	chapter, _ := job["chapter"].(map[string]any)
	index := jobIndex(job)

	var lastMs int64
	deps := danmaku.SidecarDeps{
		// 见文件头第 1 条：v2 没有 force/session
		Resolve: func() (string, string, error) {
			data, err := s.call("resolve", map[string]any{
				"drama": drama, "chapter": chapter, "index": index, "quality": 0,
			})
			if err != nil {
				return "", "", err
			}
			session, _ := data["session"].(string)
			epID, _ := data["danmakuId"].(string)
			return session, epID, nil
		},
		// 见文件头第 2 条：v2 的 danmakuAll 只收 playbackSession
		FetchAll: func(session string) ([]danmaku.Item, int64, error) {
			data, err := s.call("danmakuAll", map[string]any{"playbackSession": session})
			if err != nil {
				return nil, 0, err
			}
			raw, _ := data["items"].([]any)
			rows := make([]map[string]any, 0, len(raw))
			for _, it := range raw {
				if m, ok := it.(map[string]any); ok {
					rows = append(rows, m)
				}
			}
			items := danmaku.FromMaps(rows)
			for _, it := range items {
				if it.TimeMs > lastMs {
					lastMs = it.TimeMs
				}
			}
			return items, int64(jsonFloat(data["total"])), nil
		},
	}

	req := danmaku.SidecarRequest{
		VideoPath:  videoPath,
		DurationMs: sidecarDurationMs(chapter, lastMs),
		Width:      danmaku.DefaultWidth,
		Height:     danmaku.DefaultHeight,
		SeriesID:   stringField(drama["sourceId"]),
	}
	out, err := danmaku.CollectSidecar(req, deps)
	if err != nil {
		rep.Err = err.Error()
		return rep
	}
	switch {
	case out.Skipped:
		rep.Outcome = string(SidecarSkippedReady)
	case out.Empty:
		rep.Outcome = string(SidecarEmpty)
	case out.Written:
		rep.Outcome = string(SidecarRan)
		rep.Items = out.Items
	default:
		rep.Err = "未知结果，既没写也没跳"
	}
	return rep
}

// sidecarVideoPath 这一集的成品文件在哪。
// 复用合并那条 `resolveEpisodeSource`（核心 localPlayback → 本地路径 → 目录兜底），
// 不再自己写第四套推导——那是最容易和别处漂移的地方。
func (s *Service) sidecarVideoPath(job map[string]any) (string, error) {
	root, err := s.downloadRootForSidecar()
	if err != nil {
		return "", err
	}
	src, err := s.resolveEpisodeSource(job, root)
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(src.path)
	if path == "" {
		return "", errors.New("找不到这一集的成品文件")
	}
	if st, serr := os.Stat(path); serr != nil || st.IsDir() {
		return "", errors.New("这一集的成品文件读不到：" + filepath.Base(path))
	}
	return filepath.Clean(path), nil
}

func (s *Service) downloadRootForSidecar() (string, error) {
	data, err := s.call("downloadDirectory", nil)
	if err != nil {
		return "", err
	}
	dir, _ := data["directory"].(string)
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("还没有可用的下载目录")
	}
	return filepath.Abs(dir)
}

// sidecarDurationMs ASS 的事件时间上限。
// 优先 chapter.duration（真实集长），拿不到就"最后一条弹幕 + 60s"近似（文件头第 3 条）。
func sidecarDurationMs(chapter map[string]any, lastMs int64) int64 {
	if d := jsonFloat(chapter["duration"]); d > 0 {
		return int64(d*1000 + 0.5)
	}
	if lastMs > 0 {
		return lastMs + SidecarAssTailSeconds*1000
	}
	return 0 // 交给 CollectSidecar 报"缺少分集时长"，不自己编一个数
}
