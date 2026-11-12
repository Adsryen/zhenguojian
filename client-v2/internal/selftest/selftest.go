// Package selftest 是 v1 `真果鉴.exe --selftest`（zhenguojian/app/zhenguojian/selftest.py）
// 在 v2 的对等物：从**已打包的二进制内部**跑一遍真实管线，
// 证明「资源发现 → 核心 API → 流解析」在打包布局里真的可用，并输出机器可读的报告。
//
// 与 v1 的两点差异是**刻意的**，不是遗漏：
//
//  1. v1 的 `resources` 步骤查 sidecar/mpv/ffmpeg/ffprobe 四件套；
//     v2 的引擎是**进程内** `duanjuapp/native`，没有独立 sidecar 可查，
//     所以这一步查 mpv/ffmpeg/ffprobe 三件 + 报告里明确写 "sidecar: in-process"。
//     照抄 v1 的四件套会永远失败，假装通过更糟。
//  2. v1 只有 OK/FAIL 两种结局。v2 多一个 **SKIP**：本轮尚未接通的步骤
//    （download / decrypt / merge 需要真实下载与 ffmpeg 产物）显式标 SKIP 并带原因，
//     同时跑完打印 `PENDING` 行。**没有 SKIP，六步全绿会被读成"selftest 已等同 v1"**。
//
// 屏幕播放不进自检（v1 原话：它以前会在构建中途弹 mpv 窗口，已移除，人工验）。
package selftest

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Outcome 一步的结局。值与 v1 打印的单词对齐（OK/FAIL），SKIP 是 v2 新增。
type Outcome string

const (
	OK   Outcome = "OK"
	FAIL Outcome = "FAIL"
	SKIP Outcome = "SKIP"
)

// Result 一步的结果（报告里就是它 JSON 化）。
type Result struct {
	Name    string         `json:"name"`
	Outcome Outcome        `json:"outcome"`
	Detail  map[string]any `json:"detail,omitempty"`
	Err     string         `json:"error,omitempty"`
	Reason  string         `json:"reason,omitempty"` // SKIP 的原因
}

// Step 一个自检步骤。Run 为 nil 表示"该步还没接通"，此时以 SKIP + Pending 上报。
type Step struct {
	Name string
	Run  func() (map[string]any, error)
	// Pending 写清这一步缺什么、为什么缺（会进报告与终端输出，不给"静默少三步"留空间）。
	Pending string
}

// Report 是整个自检的输出契约（v1 落 selftest-report.json，v2 同名单同目录）。
type Report struct {
	Version  string    `json:"version"`
	Steps    []Result  `json:"steps"`
	Failed   []string  `json:"failed"`
	Pending  []string  `json:"pending"`
	ExitCode int       `json:"exitCode"`
	Started  string    `json:"started"`
	Results  []Result  `json:"-"` // 兼容旧字段名，见 Run 返回值
}

// Run 顺序执行 steps，逐行打印（与 v1 同格式，便于人眼比对），返回结果与退出码。
//
// 退出码语义与 v1 一致：**只有 FAIL 才非 0**；SKIP 不算失败，
// 但会单独打一行 PENDING，免得"绿色"被误读成完全对齐 v1。
func Run(w io.Writer, steps []Step, version string) ([]Result, int) {
	started := nowISO()
	results := make([]Result, 0, len(steps))
	var failed, pending []string
	for _, st := range steps {
		if st.Run == nil {
			res := Result{Name: st.Name, Outcome: SKIP, Reason: st.Pending}
			results = append(results, res)
			pending = append(pending, st.Name)
			fmt.Fprintf(w, "[selftest] %-10s SKIP %s\n", st.Name, st.Pending)
			continue
		}
		detail, err := st.Run()
		if err != nil {
			res := Result{Name: st.Name, Outcome: FAIL, Detail: detail, Err: err.Error()}
			results = append(results, res)
			failed = append(failed, st.Name)
			fmt.Fprintf(w, "[selftest] %-10s FAIL %s\n", st.Name, err)
			continue
		}
		results = append(results, Result{Name: st.Name, Outcome: OK, Detail: detail})
		fmt.Fprintf(w, "[selftest] %-10s OK   %s\n", st.Name, jsonCompact(detail))
	}
	code := 0
	if len(failed) > 0 {
		code = 1
	}
	rep := Report{Version: version, Steps: results, Failed: failed, Pending: pending,
		ExitCode: code, Started: started}
	if len(pending) > 0 {
		fmt.Fprintf(w, "[selftest] PENDING %d step(s) not implemented yet: %s\n",
			len(pending), strings.Join(pending, ", "))
	}
	if len(failed) > 0 {
		fmt.Fprintf(w, "[selftest] FAILED %d step(s): %s\n", len(failed), strings.Join(failed, ", "))
	}
	// 报告落盘：失败不影响退出码判定（自检结果已经打印过了）
	if dir, err := reportDir(); err == nil {
		if path, err := WriteReport(dir, rep); err == nil {
			fmt.Fprintf(w, "[selftest] report -> %s\n", path)
		} else {
			fmt.Fprintf(w, "[selftest] report 写入失败: %v\n", err)
		}
	} else {
		fmt.Fprintf(w, "[selftest] 数据目录不可用，报告未落盘: %v\n", err)
	}
	return results, code
}

// WriteReport 把报告写成 selftest-report.json（v1 同名同位置语义：exe 同级优先，
// 写不动就退回数据目录）。返回实际路径。
func WriteReport(dir string, rep Report) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "selftest-report.json")
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// names 把结果里的某类结局抽成有序名单（单测与报告都用它，避免两处漂移）。
func names(results []Result, outcome Outcome) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		if r.Outcome == outcome {
			out = append(out, r.Name)
		}
	}
	sort.Strings(out)
	return out
}

func jsonCompact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	s := string(b)
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}
