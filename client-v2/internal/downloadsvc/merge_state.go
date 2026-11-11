// merge 状态持久化（长尾补齐·交付 A）。
//
// 背景：merges 表此前仅内存（service.go maxMergeHistory 修剪），重启即空。
// 本文件把合并任务表整体快照到 <DataDir>/merges.state.json —— 文件名刻意
// 与 mergeVerify 校验缓存（downloads.state.json，verifyStatePath）分开，
// 互不挤占（校验缓存重启复用价值高）。
//
// 写盘时机：状态跃迁后 debounce ≤2s 合并写（markMergesDirty）+ OnShutdown
// 立即落盘；setStageQuiet 的高频进度不直接落盘，只由 done/failed/canceled
// 等终态与 queued→running 跃迁触发。0600 原子写（tmp+rename）；损坏回退空表。
//
// 启动 Load 语义（对齐 v1 downloads.py:2122-2130 重启批量中断）：
//   - running → failed「重启中断」；
//   - queued 原样保留：entry.jobs 不可导出、无法持久化，执行时由
//     runMergeEntry 按 DramaID+Episodes 从核心 completed 记录重挂
//     （reattachMergeJobs）；重挂不足 2 集 → failed
//     「重启后找不到已完成分集，请重新发起合并」（如实报错，不静默降级）。
//   - done/failed/canceled 原样恢复；createdAt 一并恢复（前端排序/展示）。
//
// DataDirFunc nil = 内存降级模式：path 为空，读写全部短路，行为同旧版。

package downloadsvc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// mergesStateVersion 快照格式版本（当前 1）。
	mergesStateVersion = 1
	// mergeSaveDebounce 状态跃迁后的合并写窗口（任务书要求 ≤2s）。
	mergeSaveDebounce = 2 * time.Second
	// restartBrokenMsg 重启时 running 条目的落定文案。
	restartBrokenMsg = "重启中断"
	// reattachFailMsg 重挂失败文案（父代理定稿：如实提示重新发起）。
	reattachFailMsg = "重启后找不到已完成分集，请重新发起合并"
)

// persistedMerge 快照行：字段与 MergeEntry 的 JSON tag 一一对应，另存
// createdAt（MergeEntry 该字段刻意不导出、不进事件载荷，这里补持久化维度）。
type persistedMerge struct {
	ID        string  `json:"id"`
	DramaID   string  `json:"dramaId"`
	Title     string  `json:"title"`
	Episodes  []int   `json:"episodes"`
	Count     int     `json:"count"`
	State     string  `json:"state"`
	Stage     string  `json:"stage,omitempty"`
	Progress  float64 `json:"progress,omitempty"`
	Detail    string  `json:"detail,omitempty"`
	Output    string  `json:"output,omitempty"`
	Bytes     int64   `json:"bytes,omitempty"`
	CreatedAt int64   `json:"createdAt,omitempty"` // unix ms
}

// mergesStateFile <dataDir>/merges.state.json 顶层结构。
type mergesStateFile struct {
	Version int              `json:"version"`
	SavedAt int64            `json:"savedAt"`
	Merges  []persistedMerge `json:"merges"`
}

func (s *Service) mergesStatePath() string {
	dir := s.stateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "merges.state.json")
}

// loadMergesLocked 首次访问时从快照恢复（幂等；调用方持 s.mu）。
func (s *Service) loadMergesLocked() {
	if s.mergesLoaded {
		return
	}
	s.mergesLoaded = true
	path := s.mergesStatePath()
	if path == "" {
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return // 无文件/不可读 → 空表（首次运行常态）
	}
	var st mergesStateFile
	if json.Unmarshal(body, &st) != nil || st.Version != mergesStateVersion {
		return // 损坏/版本不符 → 回退空表
	}
	loaded := make([]*MergeEntry, 0, len(st.Merges))
	for _, p := range st.Merges {
		if p.ID == "" {
			continue
		}
		switch p.State {
		case MergeQueued, MergeRunning, MergeDone, MergeFailed, MergeCanceled:
		default:
			continue
		}
		if p.DramaID == "" || len(p.Episodes) == 0 {
			continue
		}
		e := &MergeEntry{
			ID:       p.ID,
			DramaID:  p.DramaID,
			Title:    p.Title,
			Episodes: p.Episodes,
			Count:    p.Count,
			Stage:    p.Stage,
			Progress: p.Progress,
			Detail:   p.Detail,
			Output:   p.Output,
			Bytes:    p.Bytes,
			restored: p.State == MergeQueued, // 重挂标记：jobs 只能执行时重建
			createdAt: func() time.Time {
				if p.CreatedAt > 0 {
					return time.UnixMilli(p.CreatedAt)
				}
				return s.now()
			}(),
		}
		switch p.State {
		case MergeRunning:
			// v1 :2122 重启批量中断语义；用户可见文案「重启中断」。
			e.State = MergeFailed
			e.Stage = ""
			e.Detail = restartBrokenMsg
		case MergeQueued:
			e.State = MergeQueued
			e.Stage = "排队中"
		default:
			e.State = p.State
			e.Stage = ""
		}
		if e.Count == 0 {
			e.Count = len(e.Episodes)
		}
		loaded = append(loaded, e)
	}
	s.merges = append(s.merges, loaded...)
	for len(s.merges) > maxMergeHistory {
		s.merges = s.merges[1:]
	}
}

// hasQueuedMergesLocked OnStartup 决定是否 wakeWorker。
func (s *Service) hasQueuedMergesLocked() bool {
	for _, m := range s.merges {
		if m.State == MergeQueued {
			return true
		}
	}
	return false
}

// buildMergesStateLocked 序列化当前表（调用方持锁；返回待写盘字节）。
func (s *Service) buildMergesStateLocked() ([]byte, error) {
	st := mergesStateFile{Version: mergesStateVersion, SavedAt: s.now().UnixMilli()}
	st.Merges = make([]persistedMerge, 0, len(s.merges))
	for _, m := range s.merges {
		st.Merges = append(st.Merges, persistedMerge{
			ID:        m.ID,
			DramaID:   m.DramaID,
			Title:     m.Title,
			Episodes:  m.Episodes,
			Count:     m.Count,
			State:     m.State,
			Stage:     m.Stage,
			Progress:  m.Progress,
			Detail:    m.Detail,
			Output:    m.Output,
			Bytes:     m.Bytes,
			CreatedAt: m.createdAt.UnixMilli(),
		})
	}
	return json.MarshalIndent(st, "", " ")
}

// markMergesDirty 状态跃迁后打标（持锁变体；debounce 合并写）。
func (s *Service) markMergesDirtyLocked() {
	if s.mergesStatePath() == "" {
		return // 内存降级：无盘可写
	}
	if s.mergesFlushTimer != nil {
		return // 已有待发窗口，合并写
	}
	s.mergesFlushTimer = time.AfterFunc(mergeSaveDebounce, s.flushMerges)
}

func (s *Service) markMergesDirty() {
	s.mu.Lock()
	s.markMergesDirtyLocked()
	s.mu.Unlock()
}

// flushMerges debounce 到期落盘。
func (s *Service) flushMerges() {
	s.mu.Lock()
	s.mergesFlushTimer = nil
	body, err := s.buildMergesStateLocked()
	s.mu.Unlock()
	if err != nil {
		return
	}
	if path := s.mergesStatePath(); path != "" {
		_ = writeFileAtomic(path, body, 0o600)
	}
}

// flushMergesNow OnShutdown 立即落盘（取消进行中的 debounce 窗口）。
// 先补一次 Load：从未启动/从未触表的 Service 直接 Shutdown 不得把历史
// 快照清成空表（load 幂等，已加载时零成本）。
func (s *Service) flushMergesNow() {
	s.mu.Lock()
	if s.mergesFlushTimer != nil {
		s.mergesFlushTimer.Stop()
		s.mergesFlushTimer = nil
	}
	s.loadMergesLocked()
	body, err := s.buildMergesStateLocked()
	s.mu.Unlock()
	if err != nil {
		return
	}
	if path := s.mergesStatePath(); path != "" {
		_ = writeFileAtomic(path, body, 0o600)
	}
}

// reattachMergeJobs 恢复条目执行时重挂 jobs（父修正第 3 条方案 a）：
// 按 DramaID+Episodes 从核心 completed 记录重建；同集多条记录后者胜
// （重新下载的新产物）。不足 2 集由调用方标 failed；部分命中时集号
// 以实际重建为准（runMerge 连续性检查会给出不连续的具体缺集提示）。
func reattachMergeJobs(jobs []map[string]any, entry *MergeEntry) []map[string]any {
	want := make(map[int]bool, len(entry.Episodes))
	for _, ep := range entry.Episodes {
		want[ep] = true
	}
	found := make(map[int]map[string]any, len(want))
	for _, job := range jobs {
		if jobDramaID(job) != entry.DramaID {
			continue
		}
		if state, _ := job["state"].(string); state != "completed" {
			continue
		}
		ep := episodeOfJob(job)
		if want[ep] {
			found[ep] = job
		}
	}
	if len(found) == 0 {
		return nil
	}
	nums := make([]int, 0, len(found))
	for ep := range found {
		nums = append(nums, ep)
	}
	sort.Ints(nums)
	out := make([]map[string]any, 0, len(nums))
	for _, ep := range nums {
		out = append(out, found[ep])
	}
	return out
}

// writeFileAtomic tmp+rename 原子写（0600 由调用方 perm 决定；权限刻意
// 收紧——快照含本地路径）。verify 缓存同用此函数（升级为原子写）。
func writeFileAtomic(path string, body []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建状态目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = "" // rename 成功，无需清理
	return nil
}
