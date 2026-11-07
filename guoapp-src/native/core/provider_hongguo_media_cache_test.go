package core

import "testing"

func TestPendingHongguoVideoIDsPrefersCurrent(t *testing.T) {
	manager := &nativeDownloads{jobs: map[string]*nativeDownloadRecord{}}
	add := func(id, drama string, index int, state, videoID string) *nativeDownloadRecord {
		record := &nativeDownloadRecord{nativeDownloadJob: nativeDownloadJob{
			ID: id, Index: index, State: state,
			Drama:   nativeDrama{ID: drama},
			Chapter: Chapter{VideoURL: "hongguo-cenc://" + videoID},
		}}
		manager.jobs[id] = record
		return record
	}
	add("a", "hongguo:1", 1, "queued", "1001")
	add("b", "hongguo:1", 2, "completed", "1002")
	add("c", "hongguo:1", 3, "downloading", "1003")
	add("d", "hongguo:2", 4, "queued", "2001")
	add("e", "hongguo:1", 5, "queued", "1005")
	manager.jobs["f"] = &nativeDownloadRecord{nativeDownloadJob: nativeDownloadJob{
		ID: "f", Archived: true, State: "queued", Drama: nativeDrama{ID: "hongguo:1"},
		Chapter: Chapter{VideoURL: "hongguo-cenc://1006"}}}

	job := nativeDownloadJob{ID: "c", Drama: nativeDrama{ID: "hongguo:1"}}
	ids := manager.pendingHongguoVideoIDs(job, 15)
	if len(ids) != 3 {
		t.Fatalf("ids=%v，期望只收集本剧排队/下载中的 3 集", ids)
	}
	if ids[0] != "1003" {
		t.Fatalf("当前任务应排第一，得到 %v", ids)
	}
	want := map[string]bool{"1001": true, "1003": true, "1005": true}
	for _, id := range ids {
		if !want[id] {
			t.Fatalf("意外的分集 ID %s（完整列表 %v）", id, ids)
		}
	}
}

func TestHongguoVideoIDOf(t *testing.T) {
	if id := hongguoVideoIDOf(Chapter{VideoURL: "hongguo-cenc://12345"}); id != "12345" {
		t.Fatalf("id=%q", id)
	}
	if id := hongguoVideoIDOf(Chapter{VideoURL: "https://example.com/x.mp4"}); id != "" {
		t.Fatalf("非红果章节不应返回 ID，得到 %q", id)
	}
	if id := hongguoVideoIDOf(Chapter{VideoURL: "hongguo-cenc://abc"}); id != "" {
		t.Fatalf("非数字 ID 不应返回，得到 %q", id)
	}
}
