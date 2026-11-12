package selftest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"zhenguojian/client-v2/internal/native"
)

// 与 internal/downloadsvc/executor.go:58 的 toolEnvNames 同一套环境变量名。
// 这里复制而不 import：downloadsvc 的 Look 是 commandExecutor 的方法（未导出构造），
// 为一个查路径的动作去拖整个下载服务不值得——但**两处必须同步改**，
// 所以把来源行号写死在注释里，而不是写成"类似的规则"。
var toolEnv = map[string]string{"ffmpeg": "ZHENGUO_FFMPEG", "ffprobe": "ZHENGUO_FFPROBE"}

func nowISO() string { return time.Now().Format(time.RFC3339) }

// reportDir 是**可替换的落点**（单测把它指到临时目录，别把报告写进 CI 的构建目录）。
// 默认实现：exe 同级优先（v1 就是 selftest-report.json 放 app_root），
// 拿不到 exe 目录时退回数据目录。
var reportDir = defaultReportDir

func defaultReportDir() (string, error) {
	if exe, err := os.Executable(); err == nil && exe != "" {
		return filepath.Dir(exe), nil
	}
	if dir, err := native.DataDir(); err == nil {
		return dir, nil
	}
	return "", fmt.Errorf("既取不到 exe 目录也取不到数据目录")
}

// lookupTool 按「env → exe 同级 → PATH」找外部进程，顺序与 downloadsvc 一致。
func lookupTool(name string) (string, error) {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	if env := toolEnv[name]; env != "" {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			if _, err := os.Stat(v); err == nil {
				return v, nil
			}
			return "", fmt.Errorf("%s 指向的文件不存在: %s", env, v)
		}
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), name+ext)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("找不到 %s（放 exe 同级、PATH 或 %s 指定）", name+ext, toolEnv[name])
}

// Steps 组装 v1 的九步（顺序照 v1）。
//
// **命名差异先说清**：v1 的 `new_series` 在 v2 没有同名动作——核心把「上新」折叠进
// catalog（lane=discover）/ browse_filtered，所以这一步在 v2 叫 `discover`，
// 能力对等、名字不同。照抄 v1 的名字去调一个不存在的动作，才是真正的对齐。
//
// download / decrypt / merge 三步本轮 **Run=nil → SKIP**：
// 它们要真实落盘 + ffmpeg 产物 + 可清理的库目录，属于"打包机上才该跑"的部分，
// 需要单独一轮把 downloadsvc 的安全用法（临时库目录、只下 2 集、跑完清理）接起来。
// Run() 会打印 PENDING 行，六步绿不会被读成"已与 v1 完全等同"。
func Steps() []Step {
	st := map[string]any{} // 跨步骤传值（v1 也叫 state：drama/chapters/jobs…）
	steps := []Step{
		{Name: "resources", Run: func() (map[string]any, error) {
			// v2 引擎是进程内 duanjuapp/native，没有 sidecar 可查——
			// 照 v1 查四件套会永远 FAIL，所以这里查三件并把 sidecar 记成 in-process。
			out := map[string]any{"sidecar": "in-process"}
			mpv, err := lookupToolOrMpv()
			if err != nil {
				return out, err
			}
			out["mpv"] = mpv
			for _, name := range []string{"ffmpeg", "ffprobe"} {
				p, err := lookupTool(name)
				if err != nil {
					return out, err
				}
				out[name] = p
			}
			if err := native.Init(); err != nil {
				return out, fmt.Errorf("核心初始化失败: %w", err)
			}
			out["dataDir"], _ = native.DataDir()
			return out, nil
		}},
		{Name: "library", Run: func() (map[string]any, error) {
			recs, err := native.Recommendations("", false, false)
			if err != nil {
				return nil, err
			}
			items, _ := recs["items"].([]any)
			if len(items) == 0 {
				return recs, fmt.Errorf("推荐位没有返回条目")
			}
			d, _ := items[0].(map[string]any)
			st["drama"] = d
			out := map[string]any{"count": len(items), "title": d["title"]}
			if boards, err := native.RankingBoards(); err == nil {
				if l, ok := boards["boards"].([]any); ok {
					out["boards"] = len(l)
				}
			}
			return out, nil
		}},
		{Name: "discover", Run: func() (map[string]any, error) {
			// v1 new_series 的 v2 对等物（见上面 Steps 的命名说明）
			page, err := native.Catalog("hongguo", "", "", 1, "discover", true)
			if err != nil {
				return nil, err
			}
			items, _ := page["items"].([]any)
			if len(items) == 0 {
				return page, fmt.Errorf("发现页没有返回条目")
			}
			d, _ := items[0].(map[string]any)
			return map[string]any{"count": len(items), "first": d["title"],
				"score": d["score"], "hasMore": page["hasMore"]}, nil
		}},
		{Name: "search", Run: func() (map[string]any, error) {
			hints, err := native.Suggestions("穿越")
			if err != nil {
				return nil, err
			}
			page, err := native.Catalog("hongguo", "穿越", "", 1, "search", true)
			if err != nil {
				return hints, err
			}
			items, _ := page["items"].([]any)
			if len(items) == 0 {
				return page, fmt.Errorf("搜索没有返回结果")
			}
			d, _ := items[0].(map[string]any)
			st["drama"] = d
			return map[string]any{"hints": len(anySlice(hints["items"])),
				"hits": len(items), "title": d["title"]}, nil
		}},
		{Name: "detail", Run: func() (map[string]any, error) {
			d, _ := st["drama"].(map[string]any)
			if d == nil {
				return nil, fmt.Errorf("前序步骤没拿到剧目，detail 无从查起")
			}
			detail, err := native.Detail(d)
			if err != nil {
				return nil, err
			}
			chapters := anySlice(detail["chapters"])
			if len(chapters) < 2 {
				return detail, fmt.Errorf("至少要有 2 集，实际 %d", len(chapters))
			}
			st["detail"] = detail
			st["chapters"] = chapters
			first, _ := chapters[0].(map[string]any)
			st["chapter"] = first
			return map[string]any{"episodes": len(chapters),
				"title": truncRunes(fmt.Sprint(d["title"]), 24)}, nil
		}},
		{Name: "preload", Run: func() (map[string]any, error) {
			d, _ := st["drama"].(map[string]any)
			c, _ := st["chapter"].(map[string]any)
			if d == nil || c == nil {
				return nil, fmt.Errorf("缺 drama/chapter，preload 跳过前置条件不成立")
			}
			plan, err := native.Preload(d, c, 1, 0)
			if err != nil {
				return nil, err
			}
			url, _ := plan["url"].(string)
			local, _ := plan["local"].(bool)
			session, _ := plan["session"].(string)
			// v1 原条件：有 url，且（本地文件或走本地代理 127.0.0.1）
			if url == "" || (!local && !strings.Contains(url, "127.0.0.1")) {
				return plan, fmt.Errorf("预加载拿不到可用流地址: url=%q local=%v session=%q", url, local, session)
			}
			return map[string]any{"local": local, "session": session != ""}, nil
		}},
	}
	steps = append(steps, PipelineSteps(st)...)
	return steps
}

// truncRunes 按**字符**截断而不是按字节。
// v1 用 Python 的 s[:24]（本来就是字符语义）；Go 里直接 s[:24] 会把中文标题从中间劈开，
// JSON 报告落成替换字符——"看着有内容其实是乱码"，这种错误不该出现在自检输出里。
func truncRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if n <= 0 || len(r) <= n {
		return string(r)
	}
	return string(r[:n])
}

func anySlice(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

// lookupToolOrMpv：mpv 的路径规则在 internal/mpv.DefaultMpvPath（app 根下 runtime），
// 与上面查 ffmpeg 的三档顺序不同，所以单列——别把它硬塞进 lookupTool 造成两套规则。
func lookupToolOrMpv() (string, error) {
	if env := strings.TrimSpace(os.Getenv("ZHENGUO_MPV")); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("取不到 exe 路径: %w", err)
	}
	root := filepath.Dir(exe)
	for _, cand := range mpvCandidates(root) {
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	if p, err := exec.LookPath("mpv"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("找不到 mpv（放 exe 同级/runtime 或 ZHENGUO_MPV 指定）")
}

func mpvCandidates(root string) []string {
	name := "mpv"
	if runtime.GOOS == "windows" {
		name = "mpv.exe"
	}
	return []string{
		filepath.Join(root, name),
		filepath.Join(root, "runtime", name),
		filepath.Join(root, "mpv", name),
	}
}
