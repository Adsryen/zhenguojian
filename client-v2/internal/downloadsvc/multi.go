package downloadsvc

// multi.go —— 「按季合并选中」（v1 downloads.py:1940-1974 `_multi_targets` + `start_merge_multi`）。
//
// 第四十轮下载面对账时发现的三个"需要 Go"的缺口之一：
// v2 的 `MergeStart` 两种 mode 都把范围锁死在**一部剧**里
// （season 分支要求单个 dramaID；selected 分支遇到跨剧直接报「请选择同一部剧的分集合并」），
// 所以 v1 那个"勾选多部剧（同一部的多季），按季序逐季各出一个成品"的按钮
// 在 v2 界面上根本表达不出来。
//
// v1 的规则逐条搬过来（不做"我觉得更好"的改动）：
//  1. 每一部只取 `state == completed` 的分集，按 index 排序，**不足 2 集的直接丢掉**；
//  2. 剩下的目标**按季号升序**排（稳定排，季号相同保持传入顺序）；
//  3. 每季一个输出文件，名字是 `<去季号的安全名> S%02dE%02d.mkv`，
//     E 取这一季里**最小的集号**；同名冲突依次加 ` (2)`、` (3)`；
//  4. 目标不足两个就整批不发起（v1 原话改一个字：它说"左侧勾选"，v2 没有左侧列表）；
//  5. **正在合并中的目标跳过而不是报错**（v1 `_start_batch(skip_running=True)` 的语义：
//     一批里有一季在跑，不该让整批失败），已在排队但没开始的同目标则刷新分集
//     ——这与单剧 `MergeStart` 的行为不同（那边撞到运行中是直接报错），
//     是 v1 本来就有的差别，不是我给它分的特权。
//
// 与 v1 的一处**已知不同**：v1 的"多季批次完成后不自动播放"（docstring 原话
// "A multi-season batch never auto-plays when it finishes"）。
// v2 这边自动播放本来就是前端按"单条合并完成"决定的（前端只认最后一条），
// 所以这里不需要额外旗标；将来若前端改成"整批完成播第一个"，
// 这条差异就得回来补。写在这是为了别把它当成已经实现过的行为。

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// multiSeasonRe / multiSeasonTailRe 照 v1 downloads.py:250 与 283-288。
var (
	multiSeasonRe     = regexp.MustCompile(`第([0-9一二两三四五六七八九十]+)季`)
	multiSeasonTailRe = regexp.MustCompile(`[Ss]\d+$`)
	multiSMarkRe      = regexp.MustCompile(`[Ss](\d+)`)
	multiBadNameRe    = regexp.MustCompile(`[\\/:*?"<>|]`)
)

var multiCnDigits = map[rune]int{
	'零': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4,
	'五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
}

// multiCnToInt 是 v1 `_cn_to_int` 的窄版本：只支持本任务里真会出现的
// 「一…九十 / 数字」形态（v1 还处理"十二"这类，这里一并照搬，
// 因为季号写到第十二季是真实存在的形状）。
func multiCnToInt(text string) int {
	if text == "" {
		return 0
	}
	if n, err := strconv.Atoi(text); err == nil {
		return n
	}
	runes := []rune(text)
	if idx := strings.IndexRune(text, '十'); idx >= 0 {
		left := string(runes[:idx])
		right := string(runes[idx+1:])
		tens := 1
		if left != "" {
			tens = multiCnDigits[[]rune(left)[0]]
		}
		ones := 0
		if right != "" {
			ones = multiCnDigits[[]rune(right)[0]]
		}
		return tens*10 + ones
	}
	digits := make([]int, 0, len(runes))
	for _, r := range runes {
		d, ok := multiCnDigits[r]
		if !ok {
			return 0
		}
		digits = append(digits, d)
	}
	if len(digits) == 0 {
		return 0
	}
	n := 0
	for _, d := range digits {
		n = n*10 + d
	}
	return n
}

// multiSeasonOf = v1 `season_of`：第N季 → N；否则 Sxx → xx；都没有 → 1。
func multiSeasonOf(title string) int {
	if m := multiSeasonRe.FindStringSubmatch(title); m != nil {
		if n := multiCnToInt(m[1]); n > 0 {
			return n
		}
	}
	if m := multiSMarkRe.FindStringSubmatch(title); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	return 1
}

// multiStripSeason = v1 `strip_season`：去掉季标记，再清尾巴的 Sxx，空了给「合集」。
func multiStripSeason(title string) string {
	clean := multiSeasonRe.ReplaceAllString(title, "")
	clean = strings.Trim(clean, " 　-·~～")
	clean = multiSeasonTailRe.ReplaceAllString(clean, "")
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return "合集"
	}
	return clean
}

// multiSafeName = v1 `_safe_name`：非法文件名字符换 `_`，空给「合集」。
// ⚠ 这里**不复用** `safeMergeTitle`：那个是单剧输出的名字口径
// （截 60 字、空值给「合并成品」），语义不同，硬共用会让两个输出名互相影响。
func multiSafeName(text string) string {
	clean := multiBadNameRe.ReplaceAllString(strings.TrimSpace(text), "_")
	if clean == "" {
		return "合集"
	}
	return clean
}

// MultiTarget 一季的目标（分组信息 + 要合的分集 + 输出名）。
type MultiTarget struct {
	Drama      map[string]any   `json:"drama"`
	Season     int              `json:"season"`
	OutputName string           `json:"outputName"`
	Episodes   []int            `json:"episodes"`
}

// buildMultiTargets 按 v1 `_multi_targets` 的规则算出目标清单。
// 抽成独立函数（而不是埋在 MergeStartMulti 里）是因为它**不需要锁、不需要网络**，
// 是唯一能在这台没有 Go 工具链的机器上被"整条规则"测到的部分——
// 靠 CI 跑它的表驱动用例。
func buildMultiTargets(dramas []map[string]any, jobs []map[string]any) []MultiTarget {
	type pair struct {
		drama  map[string]any
		merged []map[string]any
		season int
		order  int
	}
	pairs := make([]pair, 0, len(dramas))
	for i, drama := range dramas {
		id, _ := drama["id"].(string)
		if id == "" {
			continue
		}
		var done []map[string]any
		for _, job := range jobs {
			state, _ := job["state"].(string)
			if state != "completed" || jobDramaID(job) != id {
				continue
			}
			done = append(done, job)
		}
		if len(done) < 2 { // v1：每部至少完成 2 集，否则不进目标
			continue
		}
		sort.SliceStable(done, func(a, b int) bool {
			return jobIndex(done[a]) < jobIndex(done[b])
		})
		title, _ := drama["title"].(string)
		pairs = append(pairs, pair{drama: drama, merged: done, season: multiSeasonOf(title), order: i})
	}
	sort.SliceStable(pairs, func(a, b int) bool { return pairs[a].season < pairs[b].season })

	used := map[string]int{}
	out := make([]MultiTarget, 0, len(pairs))
	for _, p := range pairs {
		title, _ := p.drama["title"].(string)
		first := jobIndex(p.merged[0])
		base := fmt.Sprintf("%s S%02dE%02d", multiSafeName(multiStripSeason(title)), p.season, first)
		used[base]++
		name := base
		if used[base] > 1 { // v1 的冲突后缀：" (2)"、" (3)"…
			name = fmt.Sprintf("%s (%d)", base, used[base])
		}
		eps := make([]int, 0, len(p.merged))
		for _, job := range p.merged {
			eps = append(eps, episodeOfJob(job))
		}
		out = append(out, MultiTarget{Drama: p.drama, Season: p.season, OutputName: name + ".mkv", Episodes: eps})
	}
	return out
}

// MergeStartMulti 一次排入多季合并（v1 start_merge_multi）。
func (s *Service) MergeStartMulti(dramas []map[string]any) (map[string]any, error) {
	if len(dramas) == 0 {
		return nil, errors.New("请先勾选要合并的剧集")
	}
	if s.call == nil {
		return nil, errors.New("下载服务未接线（缺少核心门面）")
	}
	if _, err := s.refresh(false); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.loadMergesLocked()
	targets := buildMultiTargets(dramas, s.jobs)
	// v1 原话「请先在左侧勾选至少两部要合并的剧集（每部至少完成 2 集）」，
	// v2 没有左侧剧列表，去掉"左侧"两个字，其余照抄。
	if len(targets) < 2 {
		s.mu.Unlock()
		return nil, errors.New("请先勾选至少两部要合并的剧集（每部至少完成 2 集）")
	}

	queued := 0
	replaced := 0
	skipped := 0
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		id, _ := t.Drama["id"].(string)
		title, _ := t.Drama["title"].(string)
		entries := make([]map[string]any, 0, len(t.Episodes))
		for _, job := range s.jobs {
			state, _ := job["state"].(string)
			if state == "completed" && jobDramaID(job) == id {
				entries = append(entries, job)
			}
		}
		sort.SliceStable(entries, func(a, b int) bool {
			return jobIndex(entries[a]) < jobIndex(entries[b])
		})
		first, last := t.Episodes[0], t.Episodes[len(t.Episodes)-1]
		mergeID := fmt.Sprintf("%s#%03d-%03d", id, first, last)

		running := false
		slot := -1
		for i, m := range s.merges {
			if m.ID != mergeID {
				continue
			}
			if m.State == MergeRunning {
				running = true // v1 skip_running=True：这一季跳过，不判整批失败
				break
			}
			if m.State == MergeQueued {
				slot = i // 未开始的同目标：刷新分集清单（与单剧 MergeStart 同规则）
			}
		}
		if running {
			skipped++
			continue
		}
		s.mergeSeq++
		entry := &MergeEntry{
			ID:        mergeID,
			DramaID:   id,
			Title:     title,
			Episodes:  t.Episodes,
			Count:     len(entries),
			State:     MergeQueued,
			Stage:     "排队中",
			Output:    t.OutputName, // v1 的 SxxExx 命名在这里落地（单剧那批走 merge.go 的"全N集"口径）
			jobs:      entries,
			createdAt: s.now(),
		}
		if slot >= 0 {
			s.merges[slot] = entry
			replaced++
		} else {
			s.merges = append(s.merges, entry)
			for len(s.merges) > maxMergeHistory {
				s.merges = s.merges[1:]
			}
		}
		queued++
		names = append(names, t.OutputName)
	}
	s.mu.Unlock()

	if queued == 0 {
		return map[string]any{"queued": 0, "skipped": skipped, "outputs": []string{}},
			fmt.Errorf("选中的 %d 部都在合并中，没有可排的新任务", skipped)
	}
	s.wakeWorker()
	s.markMergesDirty()
	s.notify()
	return map[string]any{"queued": queued, "replaced": replaced, "skipped": skipped, "outputs": names}, nil
}
