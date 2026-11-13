package downloadsvc

// seasons.go —— 「第N季」在本包里的唯一实现。
//
// 为什么这里需要一份、而 `internal/watchstore` 那份**不能共用**：
// v1 自己就有两种规格，不是同一件事写了两遍——
//   · `watchstore.py:31` 的 `_SEASON_RE = 第\s*([0-9０-９零〇一二两兩三四五六七八九十百]+)\s*[季部]`
//     宽：认「第2部」「第十二季」「全角数字」，服务于追剧/预约的季号推断；
//   · `downloads.py:250` 的 `_SEASON_RE = 第([0-9一二两三四五六七八九十]+)季`
//     窄：只认「第N季」，服务于合并产物命名与归档归组。
// 把两份并成一份，等于偷偷改变其中一个调用点的行为——
// 所以本包这份严格照 `downloads.py` 那两个函数写，watchstore 的那份不动。
//
// 第四十七轮我挂的债写成"第N季识别有两份实现，该收进独立 series 包"，
// 复核后更正：当时只看见"两份"，没核出它们是**两种规格**。
// 真需要收拢的是 `downloadsvc` 内部的两份拷贝
// （`multi.go` 与 `archive_scan.go` 各抄了一遍 downloads.py 的窄规格），
// 而且那种拷贝已经漂了：`archive_scan.go` 那份漏了 v1 `strip_season`
// 的第二步 `re.sub(r"[Ss]\d+$", "", …)`，于是 `测试剧 S02.mkv`
// 在 v1 归到「测试剧」组，在 v2 会自成一派。本文件把这条补齐。

import (
	"regexp"
	"strconv"
	"strings"
)

// downloads.py:248-250 的那张中文数字表（窄规格只有一…九 + 十）。
var seasonDigitsNarrow = map[rune]int{
	'零': 0, '一': 1, '二': 2, '两': 2, '三': 3,
	'四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
}

var (
	// narrowSeasonRe = downloads.py:250 `_SEASON_RE`。
	narrowSeasonRe = regexp.MustCompile(`第([0-9一二两三四五六七八九十]+)季`)
	// narrowSMarkRe = downloads.py:277 `re.search(r"[Ss](\d+)")`（season_of 的第二步）。
	narrowSMarkRe = regexp.MustCompile(`[Ss](\d+)`)
	// narrowSeasonTailRe = downloads.py:285 `re.sub(r"[Ss]\d+$", "", …)`（strip_season 的第二步）。
	narrowSeasonTailRe = regexp.MustCompile(`[Ss]\d+$`)
)

// cnNumberNarrow 是 downloads.py:253-266 `_cn_to_int` 的端口。
// 与 watchstore 那份的差别不是风格，是 v1 就写了两套：
// 这里不认「百」、不认全角、不认「〇/兩」。
func cnNumberNarrow(text string) int {
	if text == "" {
		return 0
	}
	if n, err := strconv.Atoi(text); err == nil {
		return n
	}
	if idx := strings.Index(text, "十"); idx >= 0 {
		left := []rune(text[:idx])
		right := []rune(text[idx+len("十"):])
		tens := 1
		if len(left) > 0 {
			tens = seasonDigitsNarrow[left[0]]
		}
		ones := 0
		if len(right) > 0 {
			ones = seasonDigitsNarrow[right[0]]
		}
		return tens*10 + ones
	}
	n := 0
	for _, r := range text {
		d, ok := seasonDigitsNarrow[r]
		if !ok {
			return 0
		}
		n = n*10 + d
	}
	return n
}

// seasonOfTitle = downloads.py:270-281 `season_of`：
// 「第N季」→ N；否则取 `Sxx` 里的数字；都没有 → 1。
func seasonOfTitle(title string) int {
	if m := narrowSeasonRe.FindStringSubmatch(title); m != nil {
		if n := cnNumberNarrow(m[1]); n > 0 {
			return n
		}
	}
	if m := narrowSMarkRe.FindStringSubmatch(title); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	return 1
}

// stripSeasonTitle = downloads.py:283-288 `strip_season`：
// 去季标记（**不锚位置**，v1 用的是 sub）→ 两端空白与 -·~～ →
// 再去结尾的 `Sxx`（这一步是 v1 的第二刀，archive_scan 之前漏的就是它）→
// 剥空给「合集」。
func stripSeasonTitle(title string) string {
	clean := strings.Trim(narrowSeasonRe.ReplaceAllString(title, ""), " 　-·~～")
	clean = strings.TrimSpace(narrowSeasonTailRe.ReplaceAllString(clean, ""))
	if clean == "" {
		return "合集"
	}
	return clean
}
