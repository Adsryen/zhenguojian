// 交付 B 单测：ScanArchiveRoot 归档目录扫描（t.TempDir 造树）。
// 覆盖：两层归组/计数/大小/archived 旗标、合并产物前缀回组与仅合并组、
// 隐藏/.cache/合并 排除、目录不存在与空目录态。

package downloadsvc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFileTimed(t *testing.T, path string, size int, mt time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mt, mt); err != nil {
		t.Fatal(err)
	}
}

func archJob(id, folder, dramaID, title string, ep int, archived bool) map[string]any {
	job := map[string]any{
		"id":      id,
		"drama":   map[string]any{"id": dramaID, "title": title, "source": "xh"},
		"index":   float64(ep),
		"state":   "completed",
		"bytes":   float64(1),
		"total":   float64(1),
		"folder":  folder,
		"chapter": map[string]any{"currentEpisode": float64(ep)},
	}
	if archived {
		job["archived"] = true
	}
	return job
}

func groupOf(t *testing.T, res map[string]any, name string) ArchiveGroup {
	t.Helper()
	groups, _ := res["groups"].([]ArchiveGroup)
	for _, g := range groups {
		if g.Name == name {
			return g
		}
	}
	t.Fatalf("分组「%s」缺失: %+v", name, groups)
	return ArchiveGroup{}
}

func TestArchiveScanGroupingCountsAndFlags(t *testing.T) {
	h := newHarness(t)
	root := filepath.Join(h.tmp, "dl")
	base := time.Unix(1730000000, 0)
	writeFileTimed(t, root+"/hongguo/j1/media.mp4", 300, base)
	writeFileTimed(t, root+"/hongguo/j2/media.mp4", 200, base.Add(time.Hour))
	writeFileTimed(t, root+"/hongguo/j3/media.mp4", 100, base.Add(2*time.Hour))
	writeFileTimed(t, root+"/xh/j5/media.mp4", 150, base.Add(3*time.Hour))
	writeFileTimed(t, root+"/hongguo/j9/media.mp4", 50, base) // 无索引记录 → 孤儿组
	h.core.handlers["downloadDirectory"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{"directory": root}, nil
	}
	h.core.handlers["downloads"] = func(map[string]any) (map[string]any, error) {
		return jobsResult(
			archJob("j1", "hongguo/j1", "d9", "一世之尊", 1, false),
			archJob("j2", "hongguo/j2", "d9", "一世之尊", 2, true),
			archJob("j3", "hongguo/j3", "d9", "一世之尊", 3, false),
			archJob("j5", "xh/j5", "d8", "闪婚老公是大佬", 5, false),
		), nil
	}

	res, err := h.svc.ScanArchiveRoot()
	if err != nil {
		t.Fatal(err)
	}
	if res["exists"] != true || res["directory"] != root {
		t.Fatalf("头部字段异常: %+v", res)
	}
	groups := res["groups"].([]ArchiveGroup)
	if len(groups) != 3 {
		t.Fatalf("应 3 组（2 剧 + 1 孤儿）: %+v", groups)
	}
	yi := groupOf(t, res, "一世之尊")
	if yi.Episodes != 3 || yi.Bytes != 600 || !yi.Indexed || !yi.Archived {
		t.Fatalf("一世之尊 归组异常: %+v", yi)
	}
	if len(yi.Folders) != 3 || yi.Folders[0] != "hongguo/j1" {
		t.Fatalf("Folders 相对路径异常: %+v", yi.Folders)
	}
	if yi.Mtime != base.Add(2*time.Hour).UnixMilli() {
		t.Fatalf("mtime 应取最新分集: %v", yi.Mtime)
	}
	if yi.OpenPath != "hongguo/j3" {
		t.Fatalf("无合并时 openPath=最新分集目录: %v", yi.OpenPath)
	}
	shan := groupOf(t, res, "闪婚老公是大佬")
	if shan.Episodes != 1 || shan.Archived {
		t.Fatalf("闪婚 归组异常: %+v", shan)
	}
	orphan := groupOf(t, res, "j9")
	if orphan.Indexed || orphan.Episodes != 1 {
		t.Fatalf("孤儿组异常: %+v", orphan)
	}
	// 排序：mtime 降序 → 闪婚(3h) 首位。
	if groups[0].Name != "闪婚老公是大佬" {
		t.Fatalf("排序异常: %+v", groups)
	}
}

func TestArchiveScanMergedGrouping(t *testing.T) {
	h := newHarness(t)
	root := filepath.Join(h.tmp, "dl")
	base := time.Unix(1730000000, 0)
	writeFileTimed(t, root+"/xh/j1/media.mp4", 300, base)
	writeFileTimed(t, root+"/xh/j2/media.mp4", 300, base)
	writeFileTimed(t, root+"/合并/测试剧 全001-002集.mkv", 1000, base.Add(time.Hour))
	writeFileTimed(t, root+"/合并/测试剧 全003-004集（未完整校验）.mkv", 500, base.Add(2*time.Hour))
	writeFileTimed(t, root+"/合并/孤儿剧 全001-003集.mkv", 700, base)
	writeFileTimed(t, root+"/合并/readme.txt", 10, base) // 非媒体，忽略
	h.core.handlers["downloadDirectory"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{"directory": root}, nil
	}
	h.core.handlers["downloads"] = func(map[string]any) (map[string]any, error) {
		return jobsResult(
			archJob("j1", "xh/j1", "d1", "测试剧", 1, false),
			archJob("j2", "xh/j2", "d1", "测试剧", 2, false),
		), nil
	}

	res, err := h.svc.ScanArchiveRoot()
	if err != nil {
		t.Fatal(err)
	}
	groups := res["groups"].([]ArchiveGroup)
	if len(groups) != 2 {
		t.Fatalf("应 2 组: %+v", groups)
	}
	ce := groupOf(t, res, "测试剧")
	if !ce.HasMerged || ce.MergedCount != 2 || ce.MergedBytes != 1500 {
		t.Fatalf("合并回组异常（含未完整校验名）: %+v", ce)
	}
	if ce.Episodes != 2 {
		t.Fatalf("合并/ 文件不得计入分集: %+v", ce)
	}
	if ce.OpenPath != "合并/测试剧 全003-004集（未完整校验）.mkv" {
		t.Fatalf("openPath 应取最新合并产物: %v", ce.OpenPath)
	}
	other := groupOf(t, res, "孤儿剧")
	if !other.HasMerged || other.Episodes != 0 || other.Indexed || other.OpenPath != "合并/孤儿剧 全001-003集.mkv" {
		t.Fatalf("仅合并组异常: %+v", other)
	}
}

func TestArchiveScanExcludesHiddenCacheAndJunk(t *testing.T) {
	h := newHarness(t)
	root := filepath.Join(h.tmp, "dl")
	base := time.Unix(1730000000, 0)
	writeFileTimed(t, root+"/xh/j1/media.mp4", 300, base)
	writeFileTimed(t, root+"/xh/j1/.hidden.mp4", 999, base) // 隐藏成品忽略
	writeFileTimed(t, root+"/xh/j1/poster.jpg", 77, base)   // 非媒体忽略
	writeFileTimed(t, root+"/.cache/merge/frag.mkv", 888, base)
	writeFileTimed(t, root+"/.hidden_dir/ep01.mp4", 999, base) // 隐藏容器不下降
	h.core.handlers["downloadDirectory"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{"directory": root}, nil
	}
	h.core.handlers["downloads"] = func(map[string]any) (map[string]any, error) {
		return jobsResult(archJob("j1", "xh/j1", "d1", "测试剧", 1, false)), nil
	}
	res, err := h.svc.ScanArchiveRoot()
	if err != nil {
		t.Fatal(err)
	}
	groups := res["groups"].([]ArchiveGroup)
	if len(groups) != 1 || groups[0].Episodes != 1 || groups[0].Bytes != 300 {
		t.Fatalf("排除规则失效: %+v", groups)
	}
}

func TestArchiveScanMissingAndEmpty(t *testing.T) {
	h := newHarness(t)
	root := filepath.Join(h.tmp, "dl")
	h.core.handlers["downloadDirectory"] = func(map[string]any) (map[string]any, error) {
		return map[string]any{"directory": root}, nil
	}

	// 目录不存在 → exists:false + 空组（非 error）。
	res, err := h.svc.ScanArchiveRoot()
	if err != nil {
		t.Fatalf("目录缺失不应报错: %v", err)
	}
	if res["exists"] != false || len(res["groups"].([]ArchiveGroup)) != 0 || res["directory"] != root {
		t.Fatalf("缺失态字段异常: %+v", res)
	}

	// 空目录 → exists:true + 空组。
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err = h.svc.ScanArchiveRoot()
	if err != nil {
		t.Fatal(err)
	}
	if res["exists"] != true || len(res["groups"].([]ArchiveGroup)) != 0 {
		t.Fatalf("空目录态异常: %+v", res)
	}

	// 核心未接线 → exists:false。
	svc := New(Options{})
	res, err = svc.ScanArchiveRoot()
	if err != nil || res["exists"] != false {
		t.Fatalf("未接线降级异常: %+v %v", res, err)
	}
}
