package danmaku

import (
	"testing"
)

func schedItems() []Item {
	return []Item{
		{ID: "1", TimeMs: 1000, Text: "一条"},
		{ID: "2", TimeMs: 2000, Text: "两条"},
		{ID: "3", TimeMs: 5000, Text: "三条"},
		{ID: "4", TimeMs: 9000, Text: "四条"},
	}
}

// Window 是前瞻窗口 (cursor, cursor+ahead]，不含恰好落在 cursor 的条。
func TestSchedulerWindow(t *testing.T) {
	s := NewScheduler(schedItems(), 4000)
	s.SeekTo(0)
	if got := s.Window(); len(got) != 2 || got[0].ID != "1" || got[1].ID != "2" {
		t.Errorf("Window@0 = %+v, want 1000/2000", got)
	}
	s.SeekTo(1000) // 边界条目被排除
	if got := s.Window(); len(got) != 2 || got[0].ID != "2" {
		t.Errorf("Window@1000 = %+v", got)
	}
	s.SeekTo(9000)
	if got := s.Window(); len(got) != 0 {
		t.Errorf("Window@9000 = %+v, want 空", got)
	}
	// 未排序输入也要能定位（Replace 内排序）
	s2 := NewScheduler([]Item{{ID: "b", TimeMs: 5000}, {ID: "a", TimeMs: 1000}}, 8000)
	if got := s2.Window(); len(got) != 2 || got[0].ID != "a" {
		t.Errorf("乱序输入未排序: %+v", got)
	}
}

// Advance 吐新条目并推进水位；回 seek 后重新打开已消费区间。
func TestSchedulerAdvanceAndSeek(t *testing.T) {
	s := NewScheduler(schedItems(), 4000)
	s.SeekTo(0)
	if got := s.Advance(2500); len(got) != 2 {
		t.Fatalf("Advance(2500) = %+v", got)
	}
	if got := s.Advance(2500); got != nil {
		t.Errorf("重复推进不该重吐: %+v", got)
	}
	if got := s.Advance(9500); len(got) != 2 || got[0].ID != "3" || got[1].ID != "4" {
		t.Errorf("Advance(9500) = %+v", got)
	}
	if got := s.Advance(20_000); len(got) != 0 {
		t.Errorf("尽头推进 = %+v", got)
	}
	// v1 体感：拖回 3s 前 → 该区队列重新打开
	s.SeekTo(2000)
	if s.Cursor() != 2000 {
		t.Errorf("Cursor = %d", s.Cursor())
	}
	if got := s.Advance(6000); len(got) != 1 || got[0].ID != "3" {
		t.Errorf("回 seek 后 Advance = %+v, want 仅 5000 新条", got)
	}
	// 倒退 seek 到起点：队列重新打开
	s.SeekTo(0)
	if got := s.Advance(1500); len(got) != 1 || got[0].ID != "1" {
		t.Errorf("seek 回零后吐 = %+v", got)
	}
	// 往前 seek 不能倒流时钟
	s.SeekTo(500)
	if got := s.Advance(300); len(got) != 0 || s.Cursor() != 500 {
		t.Errorf("toMs<cursor 应忽略: cursor=%d got=%v", s.Cursor(), got)
	}
}

// Replace（增量刷新，Fetcher version 推动）保持时钟、换队列。
func TestSchedulerReplaceKeepsCursor(t *testing.T) {
	s := NewScheduler(schedItems(), 4000)
	s.SeekTo(3000)
	s.Replace([]Item{{ID: "x", TimeMs: 3500, Text: "增量"}})
	if s.Cursor() != 3000 {
		t.Errorf("Replace 不该动时钟: %d", s.Cursor())
	}
	if got := s.Advance(4000); len(got) != 1 || got[0].ID != "x" {
		t.Errorf("Replace 后窗口 = %+v", got)
	}
}
