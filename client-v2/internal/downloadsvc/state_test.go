// 交付 A 单测：merges 表快照持久化（merges.state.json）与恢复语义。
// 覆盖：Shutdown 落盘+文件模式+verify 缓存互不挤占、done 跨实例恢复、
// running→failed「重启中断」、queued 重挂成功/部分命中集号纠偏/重挂失败、
// 损坏回退空表。

package downloadsvc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeMergesFile(t *testing.T, dir string, st mergesStateFile) string {
	t.Helper()
	body, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "merges.state.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMergesStatePersistAndRecover(t *testing.T) {
	h := newHarness(t)
	mergeFixture(t, h)
	// 预置 verify 缓存：merges 落盘不得挤占 downloads.state.json。
	verifyPath := filepath.Join(h.tmp, "downloads.state.json")
	if err := os.WriteFile(verifyPath, []byte(`{"mergeVerify":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h.exec.progress = true
	ctx := context.Background()
	h.svc.OnStartup(ctx)

	if _, err := h.svc.MergeStart("season", map[string]any{"id": "d1"}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, ok := h.mergeState("d1#001-002")
		return ok && m["state"] == MergeDone
	}, "合并不达 done")
	h.svc.OnShutdown(ctx)

	body, err := os.ReadFile(filepath.Join(h.tmp, "merges.state.json"))
	if err != nil {
		t.Fatalf("Shutdown 后快照应存在: %v", err)
	}
	var st mergesStateFile
	if json.Unmarshal(body, &st) != nil {
		t.Fatalf("快照不可解析: %s", body)
	}
	if st.Version != mergesStateVersion || len(st.Merges) != 1 {
		t.Fatalf("快照结构异常: %+v", st)
	}
	p := st.Merges[0]
	if p.ID != "d1#001-002" || p.State != MergeDone || p.Output == "" || p.Bytes <= 0 || p.CreatedAt <= 0 {
		t.Fatalf("done 条目快照字段不全: %+v", p)
	}
	if fi, err := os.Stat(filepath.Join(h.tmp, "merges.state.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("快照模式应 0600: %v %v", fi, err)
	}
	vb, _ := os.ReadFile(verifyPath)
	if !strings.Contains(string(vb), `"mergeVerify":false`) {
		t.Fatalf("verify 缓存被挤占: %s", vb)
	}

	// 新实例（同 DataDir）懒加载恢复 done 行，字段一致。
	svc2 := New(Options{
		Call:         h.core.Call,
		DataDirFunc:  func() (string, error) { return h.tmp, nil },
		PollInterval: time.Hour,
		Executor:     h.exec,
	})
	list := svc2.MergeList()
	if len(list) != 1 {
		t.Fatalf("恢复应见 1 条: %+v", list)
	}
	got := list[0]
	if got["id"] != "d1#001-002" || got["state"] != MergeDone || got["output"] != p.Output {
		t.Fatalf("恢复字段不一致: %+v", got)
	}
	if n, _ := got["bytes"].(float64); int64(n) != p.Bytes {
		t.Fatalf("bytes 不一致: %v vs %v", got["bytes"], p.Bytes)
	}
}

func TestMergesRestoreRunningFailed(t *testing.T) {
	h := newHarness(t)
	writeMergesFile(t, h.tmp, mergesStateFile{Version: mergesStateVersion, Merges: []persistedMerge{{
		ID: "d1#001-002", DramaID: "d1", Title: "测试剧",
		Episodes: []int{1, 2}, Count: 2, State: MergeRunning, Stage: "合并中", Progress: 0.4,
		CreatedAt: time.Now().Add(-time.Hour).UnixMilli(),
	}}})
	list := h.svc.MergeList() // 懒加载
	if len(list) != 1 {
		t.Fatalf("应恢复 1 条: %+v", list)
	}
	m := list[0]
	if m["state"] != MergeFailed || m["detail"] != restartBrokenMsg || m["stage"] == "合并中" {
		t.Fatalf("running 应落定 failed/重启中断: %+v", m)
	}
	if m["progress"] != nil && m["progress"] != float64(0.4) {
		t.Fatalf("progress 应保留: %v", m["progress"])
	}
}

func TestMergesQueuedReattachSuccess(t *testing.T) {
	h := newHarness(t)
	mergeFixture(t, h) // j1/j2 completed + 产物文件 + probes 就绪
	writeMergesFile(t, h.tmp, mergesStateFile{Version: mergesStateVersion, Merges: []persistedMerge{{
		ID: "d1#001-002", DramaID: "d1", Title: "测试剧",
		Episodes: []int{1, 2}, Count: 2, State: MergeQueued, Stage: "排队中",
		CreatedAt: time.Now().Add(-time.Hour).UnixMilli(),
	}}})
	ctx := context.Background()
	h.svc.OnStartup(ctx)
	defer h.svc.OnShutdown(ctx)

	waitFor(t, func() bool {
		m, ok := h.mergeState("d1#001-002")
		return ok && m["state"] == MergeDone
	}, "queued 恢复后应重挂并跑完")
	m, _ := h.mergeState("d1#001-002")
	out, _ := m["output"].(string)
	if !strings.Contains(out, filepath.Join("合并", "测试剧 全001-002集.mkv")) {
		t.Fatalf("产物落点异常: %v", m)
	}
}

func TestMergesQueuedReattachPartialEpisodes(t *testing.T) {
	// 快照要 1-3 集，核心只有 1/2 completed：重挂部分命中 →
	// 集号以实际为准（连续性检查通过，done 文案 001-002 而非 001-003）。
	h := newHarness(t)
	mergeFixture(t, h)
	writeMergesFile(t, h.tmp, mergesStateFile{Version: mergesStateVersion, Merges: []persistedMerge{{
		ID: "d1#001-003", DramaID: "d1", Title: "测试剧",
		Episodes: []int{1, 2, 3}, Count: 3, State: MergeQueued,
		CreatedAt: time.Now().Add(-time.Hour).UnixMilli(),
	}}})
	ctx := context.Background()
	h.svc.OnStartup(ctx)
	defer h.svc.OnShutdown(ctx)

	waitFor(t, func() bool {
		m, ok := h.mergeState("d1#001-003")
		return ok && (m["state"] == MergeDone || m["state"] == MergeFailed)
	}, "部分命中应落终态")
	m, _ := h.mergeState("d1#001-003")
	if m["state"] != MergeDone {
		t.Fatalf("1-2 集连续应 done: %+v", m)
	}
	eps, _ := json.Marshal(m["episodes"])
	if string(eps) != "[1,2]" {
		t.Fatalf("集号应纠偏为实际重建 [1,2]: %v", m["episodes"])
	}
}

func TestMergesQueuedReattachFail(t *testing.T) {
	h := newHarness(t)
	h.core.handlers["downloads"] = func(map[string]any) (map[string]any, error) {
		return jobsResult(testJob("j1", "failed", 1)), nil // 无 completed 可挂
	}
	h.core.handlers["downloadDirectory"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{"directory": h.tmp + "/dl"}, nil
	}
	writeMergesFile(t, h.tmp, mergesStateFile{Version: mergesStateVersion, Merges: []persistedMerge{{
		ID: "d1#001-002", DramaID: "d1", Title: "测试剧",
		Episodes: []int{1, 2}, Count: 2, State: MergeQueued,
		CreatedAt: time.Now().Add(-time.Hour).UnixMilli(),
	}}})
	ctx := context.Background()
	h.svc.OnStartup(ctx)
	defer h.svc.OnShutdown(ctx)

	waitFor(t, func() bool {
		m, ok := h.mergeState("d1#001-002")
		return ok && m["state"] == MergeFailed
	}, "重挂失败应如实 failed")
	m, _ := h.mergeState("d1#001-002")
	if m["detail"] != reattachFailMsg {
		t.Fatalf("failed 文案应为重挂提示: %+v", m)
	}
	// 不得静默跑 runMerge（无产物）。
	if out, ok := m["output"].(string); ok && out != "" {
		t.Fatalf("failed 不应有 output: %v", m)
	}
}

func TestMergesStateCorruptFallback(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(h.tmp, "merges.state.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if list := h.svc.MergeList(); len(list) != 0 {
		t.Fatalf("损坏快照应回退空表: %+v", list)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("回退不应删除原文件: %v", err)
	}
}
