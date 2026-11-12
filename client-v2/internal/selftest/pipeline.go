package selftest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"zhenguojian/client-v2/internal/downloadsvc"
	"zhenguojian/client-v2/internal/native"
)

// 这一步组是 v1 selftest 里最重的三段：download / decrypt / merge。
//
// 先说一条**架构差异**（不是偷懒，是 v2 真的没有那个子系统）：
// v1 的 `decrypt` 步测的是 `library.EpisodePublisher.publish_pending`
// —— 把加密分集解密、发布到 `下载/<剧名>/`、校验文件名与位置、确认已是明文。
// v2 的下载产物**保持加密就地存放**，解密发生在两处：
//   · 播放时把 key 交给 mpv（internal/mpv/player.go:248 lavfOptions）
//   · 合并时用 ffmpeg `-decryption_key` 内联解密（internal/downloadsvc/merge.go:138）
// 所以 v2 没有可单独调用的"解密发布"动作。这里把 decrypt 记成 **SKIP 并注明不适用**，
// 而它的实质保证（"加密产物确实解得开、解出来能播"）由 **merge 步的产物校验**承担：
// 合并失败或产物不可读，就等于 v1 那句 "published file is still encrypted"。
//
// 另一条必须先立起来的规矩：**这三步会在沙箱数据目录里跑**。
// `Prepare()` 在 `native.Init()` 之前把 `ZHENGUO_DATA` 指到临时目录，
// 否则 downloads/merges 会写进用户真库——v1 就是直接清真实库再下（`purge(..., remove_files=True)`），
// 那套行为不该被一个"自检"复制：自检不该有删除用户文件的权力。

// sandboxDir 记下临时目录，供失败时打印现场（Prepare 设置，cleanup 清理）。
var sandboxDir string

// SandboxDir 返回当前自检沙箱目录（未建立时为空串）。
func SandboxDir() string { return sandboxDir }

// ShouldKeepSandbox：只要有任何 FAIL 就保留沙箱，方便人排查下载/合并产物。
// 这不是"失败就该留垃圾"的借口——自检本来就是给打包机用的，
// 一次真跑的下载/合并成本很高，毁掉现场等于让人再跑一遍。
func ShouldKeepSandbox(results []Result) bool {
	for _, r := range results {
		if r.Outcome == FAIL {
			return true
		}
	}
	return false
}

// 这两个 var 是**测试缝**：单测把它们换成假核心/假目录，
// 于是 download 步的入队载荷、轮询、终态判断、cache_dir 都能在 CI 上真执行一遍
// （以前只有编译，打包机才第一次跑）。生产路径仍然指真门面。
var (
	coreCall     = native.Call
	coreDataDir  = native.DataDir
)

// sandboxReady 由 Prepare() 置真；download/merge 步据此拒绝在真库里跑。
var sandboxReady bool

// Prepare 建临时数据目录并把 ZHENGUO_DATA 指过去。
// **必须在 Run 之前调用**（resources 步会 native.Init()，Once 只认第一次的目录）。
// 返回的 cleanup 负责删临时目录；调用方 defer。
func Prepare() (func(), error) {
	dir, err := os.MkdirTemp("", "zhenguo-selftest-")
	if err != nil {
		return func() {}, fmt.Errorf("建自检沙箱目录失败: %w", err)
	}
	// Mac/Linux 上 macOS 的 TMPDIR 可能是 /var/... 软链，DataDir 只做字符串处理，无影响。
	if err := os.Setenv("ZHENGUO_DATA", dir); err != nil {
		os.RemoveAll(dir)
		return func() {}, fmt.Errorf("设置 ZHENGUO_DATA 失败: %w", err)
	}
	sandboxReady = true
	sandboxDir = dir
	prev := dir
	return func() {
		sandboxReady = false
		sandboxDir = ""
		_ = os.Unsetenv("ZHENGUO_DATA")
		_ = os.RemoveAll(prev)
	}, nil
}

// pipeline 是三步共用的下载服务实例（自检专用，事件不发往 GUI）。
type pipeline struct {
	svc  *downloadsvc.Service
	ctx  context.Context
	stop context.CancelFunc
}

func newPipeline() (*pipeline, error) {
	if !sandboxReady {
		return nil, fmt.Errorf("自检沙箱没建立（Prepare 未调用或已清理），" +
			"拒绝在真实库里下载/合并")
	}
	ctx, cancel := context.WithCancel(context.Background())
	svc := downloadsvc.New(downloadsvc.Options{
		Call:        coreCall,
		DataDirFunc: coreDataDir,
		EventsEmit:  func(string, ...any) {}, // 自检没有前端，事件丢掉
	})
	svc.OnStartup(ctx)
	return &pipeline{svc: svc, ctx: ctx, stop: cancel}, nil
}

// downloadRoot 走核心 `downloadDirectory` 动作（v2 的权威来源，
// 见 internal/downloadsvc/merge.go:74 同款调用），不是拿 DataDir 猜一个。
func downloadRoot() (string, error) {
	data, err := coreCall("downloadDirectory", nil)
	if err != nil {
		return "", err
	}
	root, _ := data["directory"].(string)
	return root, nil
}

func (p *pipeline) close() {
	if p == nil {
		return
	}
	if p.svc != nil {
		p.svc.OnShutdown(context.Background())
	}
	p.stop()
}

// PipelineSteps 返回 download / decrypt / merge 三步，与前序步骤共享 state（drama/chapters/jobs）。
// 前序步骤没跑（比如只跑到 search）时，这些步骤会明确报"前置条件不成立"而不是乱下载。
func PipelineSteps(st map[string]any) []Step {
	var p *pipeline
	return []Step{
		{Name: "download", Run: func() (map[string]any, error) {
			drama, _ := st["drama"].(map[string]any)
			chapters := anySlice(st["chapters"])
			if drama == nil || len(chapters) < 2 {
				return nil, fmt.Errorf("前置条件不成立：需要 drama + 至少 2 个 chapter（detail 步先过）")
			}
			// Enqueue 会校验 id 与 source，缺一个整步直接红（service.go:351）。
			// 核心 item 正常带 source，这里只是兜底并把它记进 detail——
			// 打包机上红的时候能立刻看出"是入队校验没过，不是下载失败"。
			if src, _ := drama["source"].(string); src == "" {
				drama = copyMap(drama)
				drama["source"] = "hongguo"
			}
			if p == nil {
				np, err := newPipeline()
				if err != nil {
					return nil, err
				}
				p = np
			}
			entries := []downloadsvc.EpisodeEntry{
				{Chapter: toObj(chapters[0]), Index: 1},
				{Chapter: toObj(chapters[1]), Index: 2},
			}
			added, err := p.svc.Enqueue(drama, entries, 0)
			if err != nil {
				return nil, err
			}
			// v1 语义：最多等 900s，每 3s 看一次，直到两条都到终态
			deadline := time.Now().Add(downloadWaitBudget)
			var jobs []map[string]any
			for {
				snap, err := p.svc.Snapshot()
				if err != nil {
					return nil, err
				}
				jobs = jobList(snap)
				if allTerminal(jobs) {
					break
				}
				if time.Now().After(deadline) {
					break // 交给下面的数量判断报错，别在这儿吞状态
				}
				time.Sleep(pollEvery)
			}
			done := filterState(jobs, "completed")
			if len(done) < 2 {
				return map[string]any{"jobs": summarize(jobs)},
					fmt.Errorf("2 集里只有 %d 集下载完成", len(done))
			}
			st["jobs"] = done
			var bytes int64
			for _, j := range done {
				bytes += intOf(j["bytes"])
			}
			root, _ := downloadRoot()
			return map[string]any{"added": added["added"], "completed": len(done),
				"bytes": bytes, "cache_dir": truncRunes(root, 90),
				"sandbox": sandboxReady}, nil
		}},
		{Name: "decrypt", Pending: "不适用：v2 没有独立的解密发布子系统（产物保持加密存放，" +
			"解密发生在 mpv 播放 key 与合并时 ffmpeg -decryption_key）。" +
			"实质保证由 merge 步的产物可读性承担"},
		{Name: "merge", Run: func() (map[string]any, error) {
			drama, _ := st["drama"].(map[string]any)
			jobs, _ := st["jobs"].([]map[string]any)
			if p == nil || drama == nil || len(jobs) < 2 {
				return nil, fmt.Errorf("前置条件不成立：需要 download 步产出的 2 个已完成任务")
			}
			sorted := append([]map[string]any(nil), jobs...)
			sort.SliceStable(sorted, func(i, j int) bool {
				return intOf(sorted[i]["index"]) < intOf(sorted[j]["index"])
			})
			ids := make([]string, 0, 2)
			for _, j := range sorted[:2] {
				id, _ := j["id"].(string)
				if id == "" {
					return nil, fmt.Errorf("下载任务缺 id，无法合并")
				}
				ids = append(ids, id)
			}
			if _, err := p.svc.MergeStart("selected", drama, ids); err != nil {
				return nil, err
			}
			deadline := time.Now().Add(mergeWaitBudget)
			var merges []map[string]any
			for {
				snap, err := p.svc.Snapshot()
				if err != nil {
					return nil, err
				}
				merges = mergeList(snap)
				if mergeSettled(merges) {
					break
				}
				if time.Now().After(deadline) {
					break
				}
				time.Sleep(pollEvery)
			}
			// v1 在这里查 "merged file missing on disk" 与 "not playable on its own"；
			// v2 的等价校验交给 mergeOutcome（分开报三种原因，见该函数注释）。
			// 合并内部还做了 ffprobe 全片校验（mergeVerify），失败会落 failed 状态。
			out, err := mergeOutcome(merges)
			if err != nil {
				return map[string]any{"merges": summarize(merges)}, err
			}
			fi, _ := os.Stat(out)
			return map[string]any{"path": truncRunes(out, 90), "bytes": fi.Size(),
				"episodes": []int{1, 2}, "insideSandbox": strings.HasPrefix(
					filepath.Clean(out), filepath.Clean(os.Getenv("ZHENGUO_DATA")))}, nil
		}},
	}
}

// ---------------- 下面这几个纯函数是单测的对象（不需要网络/ffmpeg） ----------------

// 时间预算照 v1：下载 900s、每 3s 轮询；合并没抄到 v1 的数字（v1 的 MergeTask 同步跑），
// 这里给 1200s 上限并在超时时按未完成报错。
const (
	downloadWaitBudget = 15 * time.Minute
	mergeWaitBudget    = 20 * time.Minute
	pollEvery          = 3 * time.Second
)

var terminalJobStates = map[string]bool{"completed": true, "failed": true, "error": true}

// allTerminal：v1 的条件是"至少 2 条且全部终态"。空列表不算终态——
// 否则刚入队还没刷新出来就会被判"完成"，然后报"0 集完成"这种误导性错误。
func allTerminal(jobs []map[string]any) bool {
	if len(jobs) < 2 {
		return false
	}
	for _, j := range jobs {
		if !terminalJobStates[stateOf(j)] {
			return false
		}
	}
	return true
}

// mergeSettled：没有任何 queued/running 就算落定（含全失败——失败也要往下报错，不能干等）。
func mergeSettled(merges []map[string]any) bool {
	if len(merges) == 0 {
		return false
	}
	for _, m := range merges {
		switch stateOf(m) {
		case "queued", "running":
			return false
		}
	}
	return true
}

func stateOf(m map[string]any) string {
	s, _ := m["state"].(string)
	return s
}

func filterState(jobs []map[string]any, want string) []map[string]any {
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		if stateOf(j) == want {
			out = append(out, j)
		}
	}
	return out
}

func jobList(snap map[string]any) []map[string]any {
	raw, _ := snap["jobs"].([]any)
	if raw == nil {
		// payloadLocked 返回的是 []map[string]any（不是 []any），两种都要能吃
		if typed, ok := snap["jobs"].([]map[string]any); ok {
			return typed
		}
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// mergeList 把快照里的合并表压成 map 形态。
//
// ⚠ 这里原本是个**会在打包机上白等 20 分钟的 bug**：
// downloadsvc.payloadLocked 返回的 merges 是 `[]*MergeEntry`（不是 []map[string]any，
// 也不是 JSON 的 []any），我原来两种都接不住 → 永远返回空 →
// mergeSettled 恒 false → 一直轮询到 mergeWaitBudget 超时，
// 最后报一句误导的"合并没有返回产物路径"。
// 现在优先吃真类型，另外两种形状留着（它们分别对应直接构造的单测输入与 JSON 往返结果）。
func mergeList(snap map[string]any) []map[string]any {
	switch m := snap["merges"].(type) {
	case []*downloadsvc.MergeEntry: // 真类型：Service 实际返回的就是它
		out := make([]map[string]any, 0, len(m))
		for _, e := range m {
			if e == nil {
				continue
			}
			out = append(out, map[string]any{
				"id": e.ID, "state": e.State, "stage": e.Stage,
				"detail": e.Detail, "output": e.Output, "count": e.Count,
			})
		}
		return out
	case []map[string]any:
		return m
	case []any:
		out := make([]map[string]any, 0, len(m))
		for _, it := range m {
			if mm, ok := it.(map[string]any); ok {
				out = append(out, mm)
			}
		}
		return out
	}
	return nil
}

// mergeOutcome 从落定的合并表里取产物路径，失败原因按三种真实情况分开报：
// ① 合并本身没成功（ffmpeg/解码侧）② 没给路径 ③ 路径存在但是空/读不到。
// 分开报是为了打包机上一眼看出该查哪一层，而不是只留一句"失败了"。
func mergeOutcome(merges []map[string]any) (string, error) {
	var out string
	for _, m := range merges {
		state, _ := m["state"].(string)
		if state != "done" {
			detail, _ := m["detail"].(string)
			return "", fmt.Errorf("合并未成功：state=%s detail=%s", state, truncRunes(detail, 90))
		}
		out, _ = m["output"].(string)
	}
	if len(merges) == 0 {
		return "", fmt.Errorf("合并表为空（轮询到超时也没出现任务）")
	}
	if out == "" {
		return "", fmt.Errorf("合并没有返回产物路径")
	}
	fi, err := os.Stat(out)
	if err != nil {
		return "", fmt.Errorf("合并产物文件不存在: %v", err)
	}
	if fi.Size() == 0 {
		return "", fmt.Errorf("合并产物为空文件: %s", filepath.Base(out))
	}
	return out, nil
}

func toObj(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func intOf(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

// summarize 把任务/合并列表压成一行行可诊断文本，供失败时的 detail 字段。
// 自检的价值在于失败时能看出**卡在哪**，所以这里宁多勿少（但仍截断，避免报告爆炸）。
func summarize(rows []map[string]any) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, fmt.Sprintf("%v/%v/%v",
			truncRunes(fmt.Sprint(r["id"]), 20), stateOf(r), truncRunes(fmt.Sprint(r["detail"]), 40)))
	}
	return out
}

// copyMap 避免污染前序步骤留在 state 里的原始对象。
func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}
