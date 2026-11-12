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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// localRows 读库里的 history 段；**文件不存在 = 什么都没写过**（v2 只在首次变更落盘），
// 那本身就是一种证据，不能当成"读库失败"。CI 第一版我就是没考虑这条，
// 于是 NeedsDramaID 用例报 no such file or directory。
func localRows(t *testing.T, s *Store) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(s.File())
	if os.IsNotExist(err) {
		return map[string]any{}
	}
	if err != nil {
		t.Fatalf("读库: %v", err)
	}
	v := decode(t, raw).(map[string]any)
	h, _ := v["history"].(map[string]any)
	if h == nil {
		return map[string]any{}
	}
	return h
}

func localRow(t *testing.T, s *Store, id string) map[string]any {
	t.Helper()
	row, _ := localRows(t, s)[id].(map[string]any)
	return row
}

// rowNum 取行里的数值字段。
// ⚠ CI 第一次跑就红在两条 `row["episode"].(float64)` 上——
// 同包的 `decode()` 开了 `json.Decoder.UseNumber()`，数字是 **json.Number**，
// 类型断言静默失败、零值冒充"字段是 0"，看起来像端口的 bug，其实是我自己写错了取数。
// 这正是本任务反复撞的那条：兼容只兼容想象中的形状。所以统一走这个 helper。
func rowNum(t *testing.T, row map[string]any, key string) float64 {
	t.Helper()
	v, ok := row[key]
	if !ok {
		t.Fatalf("行里缺字段 %q：%+v", key, row)
	}
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			t.Fatalf("%q 不是数：%v", key, err)
		}
		return f
	case float64:
		return n
	default:
		t.Fatalf("%q 类型意外 %T", key, v)
		return 0
	}
}

func TestSaveLocalWatchWritesAbsolutePath(t *testing.T) {
	s, _, dir := newStore(t)
	f := filepath.Join(dir, "downloads", "某剧 S01 合并.mp4")
	s.SaveLocalWatch("d1", "某剧", f, 30.0, 100.0, false)
	row := localRow(t, s, "d1")
	if got, _ := row["local_path"].(string); got != f {
		t.Fatalf("local_path 应是绝对路径 %q，得 %q", f, got)
	}
	if got := rowNum(t, row, "episode"); got != 1 {
		t.Fatalf("本地文件按 v1 记 episode=1，得 %v", got)
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
	if rowNum(t, second, "updated") != rowNum(t, first, "updated") {
		t.Fatalf("微差未看完不该重写：updated %v → %v", first["updated"], second["updated"])
	}

	// 差到 0.6s 就必须写（阈值两侧都测，否则这条断言等于没测阈值）
	c.t += 60
	s.SaveLocalWatch("d1", "A", f, 31.0, 100.0, false)
	if rowNum(t, localRow(t, s, "d1"), "updated") == rowNum(t, second, "updated") {
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
	if rowNum(t, after, "updated") == rowNum(t, before, "updated") {
		t.Fatal("finished=true 不该被去抖挡掉")
	}
}

func TestSaveLocalWatchZeroDurationReusesExisting(t *testing.T) {
	s, _, dir := newStore(t)
	f := filepath.Join(dir, "a.mp4")
	s.SaveLocalWatch("d1", "A", f, 10.0, 1234.0, false)
	s.SaveLocalWatch("d1", "A", f, 20.0, 0.0, false) // 加载完成前的一次回写
	row := localRow(t, s, "d1")
	if got := rowNum(t, row, "duration"); got != 1234 {
		t.Fatalf("duration=0 应回落已有值 1234，得 %v", got)
	}
}

func TestSaveLocalWatchNeedsDramaID(t *testing.T) {
	s, _, dir := newStore(t)
	s.SaveLocalWatch("", "A", filepath.Join(dir, "a.mp4"), 1, 2, false)
	if got := localRows(t, s); len(got) != 0 {
		t.Fatalf("空 dramaID 不该写出任何历史行，得 %d 条：%+v", len(got), got)
	}
}
