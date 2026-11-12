package watchstore

// local_watch_test.go：Store.SaveLocalWatch 的语义（v1 watchstore.py:608-637）。
//
// 这个函数**早就移植好了，但一条测试都没有**，也没有绑定/调用方
// （第三十八轮我在对账里写"没人写 localPath"，其实写入函数是在的，
// 缺的还是"对外口子 + 调用点 + 测试"——R6 下一季、R23 观察器同一个形状）。
// 所以补绑定之前先把它的行为钉住，尤其是那三条容易改坏的细节：
//   · 去抖只在**没看完**时生效：finished=true 必须照样落盘（v1 同形），
//     否则"看完就抹掉进度"这条路会静默失灵；
//   · duration 传 0 时回落到已有值（v1 `if duration <= 0 and existing`），
//     本地播放器偶尔在加载完成前就回写一次，那一次不该把时长抹成 0；
//   · 记的是**绝对路径**（v1 os.path.abspath）——相对路径进库后换个 cwd 就找不到文件。

import (
	"os"
	"path/filepath"
	"testing"
)

func localRow(t *testing.T, s *Store, id string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(s.File())
	if err != nil {
		t.Fatalf("读库: %v", err)
	}
	v := decode(t, raw).(map[string]any)
	h, _ := v["history"].(map[string]any)
	row, _ := h[id].(map[string]any)
	return row
}

func TestSaveLocalWatchWritesAbsolutePath(t *testing.T) {
	s, _, dir := newStore(t)
	f := filepath.Join(dir, "downloads", "某剧 S01 合并.mp4")
	s.SaveLocalWatch("d1", "某剧", f, 30.0, 100.0, false)
	row := localRow(t, s, "d1")
	if got, _ := row["local_path"].(string); got != f {
		t.Fatalf("local_path 应是绝对路径 %q，得 %q", f, got)
	}
	if ep, _ := row["episode"].(float64); ep != 1 {
		t.Fatalf("本地文件按 v1 记 episode=1，得 %v", ep)
	}
	drama, _ := row["drama"].(map[string]any)
	if drama == nil || drama["title"] != "某剧" {
		t.Fatalf("drama 里该带上调用方传进来的标题，得 %v", drama)
	}
}

func TestSaveLocalWatchDedupesUnfinishedTinyDelta(t *testing.T) {
	s, c, dir := newStore(t)
	f := filepath.Join(dir, "a.mp4")
	s.SaveLocalWatch("d1", "A", f, 30.0, 100.0, false)
	first := localRow(t, s, "d1")

	// 同一路径、位置差 0.4s、时长差 0.4s、没看完 → v1 直接 return，不刷 updated
	c.t += 60
	s.SaveLocalWatch("d1", "A", f, 30.4, 100.4, false)
	second := localRow(t, s, "d1")
	if second["updated"] != first["updated"] {
		t.Fatalf("微差未看完不该重写：updated %v → %v", first["updated"], second["updated"])
	}

	// 差到 0.6s 就必须写（阈值两侧都测，否则这条断言等于没测阈值）
	c.t += 60
	s.SaveLocalWatch("d1", "A", f, 31.0, 100.0, false)
	if localRow(t, s, "d1")["updated"] == second["updated"] {
		t.Fatal("位置差 0.6s 应重写")
	}
}

func TestSaveLocalWatchFinishedAlwaysWrites(t *testing.T) {
	s, c, dir := newStore(t)
	f := filepath.Join(dir, "a.mp4")
	s.SaveLocalWatch("d1", "A", f, 30.0, 100.0, false)
	before := localRow(t, s, "d1")
	c.t += 10
	// 与上一次完全同值，但 finished=true：v1 的条件是 `not finished` 才去抖，
	// 看完这件事必须落盘，不然"看完抹进度"会静默失灵。
	s.SaveLocalWatch("d1", "A", f, 30.0, 100.0, true)
	after := localRow(t, s, "d1")
	if after["updated"] == before["updated"] {
		t.Fatal("finished=true 不该被去抖挡掉")
	}
}

func TestSaveLocalWatchZeroDurationReusesExisting(t *testing.T) {
	s, _, dir := newStore(t)
	f := filepath.Join(dir, "a.mp4")
	s.SaveLocalWatch("d1", "A", f, 10.0, 1234.0, false)
	s.SaveLocalWatch("d1", "A", f, 20.0, 0.0, false) // 加载完成前的一次回写
	row := localRow(t, s, "d1")
	if dur, _ := row["duration"].(float64); dur != 1234.0 {
		t.Fatalf("duration=0 应回落已有值 1234，得 %v", dur)
	}
}

func TestSaveLocalWatchNeedsDramaID(t *testing.T) {
	s, _, dir := newStore(t)
	s.SaveLocalWatch("", "A", filepath.Join(dir, "a.mp4"), 1, 2, false)
	if row := localRow(t, s, ""); row != nil {
		t.Fatal("空 dramaID 不该写任何行（v1 同形）")
	}
	v := decode(t, mustRead(t, s.File())).(map[string]any)
	h, _ := v["history"].(map[string]any)
	if len(h) != 0 {
		t.Fatalf("空 dramaID 却写出了 %d 条历史", len(h))
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
