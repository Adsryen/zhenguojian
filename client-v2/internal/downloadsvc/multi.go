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
// ⚠ 更正（第四十五轮）：这里原本写"v1 的多季批次完成后不自动播放，这条差异没实现"，
// 那是**照抄 v1 docstring、没去核它的代码**。核完的事实是：
// **v1 里根本没有"合并完成后自动播放"的任何实现**——
// `grep -rnE "auto_play|autoplay|自动播放"` 在整个 v1 客户端只命中一句无关注释
// （main.py:2545「不跨季自动播放——下一季是另一部剧，先让用户确认」）；
// 单季完成 `_merge_one_done`（downloads.py:2565-2577）只写状态栏文本，
// 整批完成 `_finish_merge_batch`（2586-2605）也只写文本。
// v1 docstring 那句描述的是一个不存在的特性（大概是早期设计残留）。
//
// 所以这条差异**不存在**：不需要旗标，也没有"将来要回来补"的东西。
// v1 真正做而 v2 当时缺的是另一件事，已在本轮接上：
// **双击「合并」页签里已完成的条目 → 播放那个成品**
// （downloads.py:1139 itemDoubleClicked → _open_merge_entry 1495-1498 →
//  _play_merged_path 1631-1640，0.8s 去抖，文件不在提示「成品文件不存在：<文件名>」）。

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// 季号解析与"去季后缀"在 seasons.go（本包唯一实现，照 v1 downloads.py 的窄规格）。
// 这里只留文件名清洗的正则。
var (
	// multiBadNameRe = downloads.py:289 `_safe_name` 的那一组非法文件名字符。
	multiBadNameRe = regexp.MustCompile(`[\\/:*?"<>|]`)
)

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
		pairs = append(pairs, pair{drama: drama, merged: done, season: seasonOfTitle(title), order: i})
	}
	sort.SliceStable(pairs, func(a, b int) bool { return pairs[a].season < pairs[b].season })

	used := map[string]int{}
	out := make([]MultiTarget, 0, len(pairs))
	for _, p := range pairs {
		title, _ := p.drama["title"].(string)
		first := jobIndex(p.merged[0])
		base := fmt.Sprintf("%s S%02dE%02d", multiSafeName(stripSeasonTitle(title)), p.season, first)
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
		// 尾巴 `[multi_none]` 是本仓既有错误码约定（api.ts callGoRaw 剥成 ApiError.code）：
		// 界面按 code 取 i18n，这句中文只进日志——否则英文界面会吐中文。
		return nil, errors.New("请先勾选要合并的剧集 [multi_none]")
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
		return nil, errors.New("请先勾选至少两部要合并的剧集（每部至少完成 2 集） [multi_targets]")
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
			// 命名落到 **OutputName**（目标文件名），不是 Output（已落盘的绝对路径）。
			// ⚠ 我上一轮这里写的就是 `Output:`，那有两个错：
			//   ① 合并还没跑，路径并不存在，UI 的 done 分支会把它当真实路径显示；
			//   ② 更关键的是 `runMerge` 的保存那一步从来不看 Output，
			//      它自己按 "剧名 全001-002集.mkv" 拼名字——
			//      所以那个 SxxExx 命名**只在排队时算出来过，从没落到磁盘上**。
			//   本轮复核发现并改正：现在由 runMerge 读 OutputName（含 v1 的 basename 守卫）。
			OutputName: t.OutputName,
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
			fmt.Errorf("选中的 %d 部都在合并中，没有可排的新任务 [multi_running]", skipped)
	}
	s.wakeWorker()
	s.markMergesDirty()
	s.notify()
	return map[string]any{"queued": queued, "replaced": replaced, "skipped": skipped, "outputs": names}, nil
}
