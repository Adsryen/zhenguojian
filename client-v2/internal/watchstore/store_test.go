package watchstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// clock is an injectable fake time source (s.now / s.today in tests).
type clock struct {
	t float64 // epoch seconds
}

func (c *clock) now() float64 { return c.t }
func (c *clock) today() time.Time {
	return time.Unix(int64(c.t), 0).In(time.Local)
}

func newStore(t *testing.T) (*Store, *clock, string) {
	t.Helper()
	dir := t.TempDir()
	c := &clock{t: 1_794_000_000.5} // 2026-11-06-ish, deterministic
	s := Open(dir)
	s.now = c.now
	s.today = c.today
	return s, c, dir
}

func decode(t *testing.T, b []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %q: %v", truncate(b), err)
	}
	if dec.More() {
		t.Fatalf("trailing garbage in %q", truncate(b))
	}
	return v
}

func loadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func truncate(b []byte) string {
	if len(b) > 400 {
		return string(b[:400]) + "…"
	}
	return string(b)
}

// v1SortedDesc emulates v1 watchstore.py:206-210 `rows.sort(key=lambda row:
// -float(row.get("updated") or 0))` over a decoded file, so tests can assert
// the exact read-order v1 would produce.
func v1SortedDesc(t *testing.T, data any) []string {
	t.Helper()
	root, ok := data.(obj)
	if !ok {
		t.Fatalf("root not object")
	}
	hist, ok := root["history"].(obj)
	if !ok {
		t.Fatalf("history not object")
	}
	type kv struct {
		id string
		u  float64
	}
	rows := make([]kv, 0, len(hist))
	for id, v := range hist {
		row, ok := v.(obj)
		if !ok {
			continue
		}
		u := toFloat(row["updated"]) // float(...) — Python crash only on non-numeric strings; fixture uses valid stamps
		rows = append(rows, kv{id, u})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].u != rows[j].u {
			return rows[i].u > rows[j].u
		}
		return rows[i].id < rows[j].id // deterministic tiebreak (v1: dict order)
	})
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.id)
	}
	return out
}

// ---------------------------------------------------------------------------
// v1-format fixture — assembled literally per watchstore.py field sets:
//   - follow rows: toggle_follow create (240-252) with observe/save mutations
//   - reserve-only row (reserve_drama 277-288 + 294-300)
//   - legacy row missing followed/reserved/newEpisodes/updateSeen keys
//   - history rows: save_watch (485-489), legacy row without drama.source,
//     numeric-STRING updated (v1 float() tolerates), local_path row (625-630)
//   - unknown extras (actors/score) that v2 must not drop
// Written in v1's json.dump(ensure_ascii=False, indent=1) style, no trailing
// newline.
// ---------------------------------------------------------------------------

const v1Fixture = `{
 "follows": {
  "hongguo:7600000000000000001": {
   "drama": {
    "id": "hongguo:7600000000000000001",
    "title": "大夏武主 第二季",
    "cover": "https://p3-sign.toutiaoimg.com/tos-cn-i-6w9my0vskp/aaa.jpeg~tplv-shrink-cover-jpeg-image.image",
    "source": "hongguo",
    "episodes": 24,
    "onlineDate": "2026-05-20",
    "releaseStatus": "ongoing"
   },
   "status": "在看",
   "known": 26,
   "read": 20,
   "marked": false,
   "added": 1762800000.5,
   "season": 2,
   "latestSeason": 2,
   "newSeason": false,
   "lastChecked": 1763000000.25,
   "followed": true,
   "reserved": false,
   "reserveSeason": 2,
   "reserveAtKnown": 24,
   "arrived": false,
   "arrivedSeen": true,
   "newEpisodes": 3,
   "updateAt": 1763000000.25,
   "updateSeen": false,
   "sourceTime": "2026-05-20",
   "nextDrama": null,
   "nextSeason": 0
  },
  "hongguo:7300000000000000002": {
   "drama": {
    "id": "hongguo:7300000000000000002",
    "title": "老剧无站源",
    "cover": "https://p9-sign.toutiaoimg.com/bbb.jpeg",
    "episodes": 12,
    "actors": [
     "张三",
     "李四"
    ],
    "score": 9.2
   },
   "status": "已看",
   "known": 12,
   "read": 12,
   "marked": true,
   "added": 1729000000.0,
   "season": 1,
   "latestSeason": 1,
   "newSeason": false,
   "lastChecked": 0,
   "arrived": false,
   "arrivedSeen": true,
   "updateAt": 0.0,
   "updateSeen": true,
   "sourceTime": "",
   "nextDrama": null,
   "nextSeason": 0
  },
  "duboku:6900000000000000003": {
   "drama": {
    "id": "duboku:6900000000000000003",
    "title": "未上线预约剧 第二季",
    "cover": "https://v2.duboku.com/ccc.jpg",
    "source": "duboku",
    "episodes": 0,
    "onlineDate": "2026-12-01",
    "releaseStatus": "premiere"
   },
   "status": "预约中",
   "known": 0,
   "read": 0,
   "marked": false,
   "added": 1761000000.0,
   "season": 2,
   "latestSeason": 2,
   "newSeason": false,
   "lastChecked": 0,
   "followed": false,
   "newEpisodes": 0,
   "updateAt": 0.0,
   "updateSeen": true,
   "sourceTime": "2026-12-01",
   "nextDrama": null,
   "nextSeason": 0,
   "reserved": true,
   "arrived": false,
   "arrivedSeen": true,
   "reserveSeason": 3,
   "reserveAtKnown": 0
  }
 },
 "history": {
  "hongguo:7600000000000000001": {
   "drama": {
    "id": "hongguo:7600000000000000001",
    "title": "大夏武主 第二季",
    "cover": "https://p3-sign.toutiaoimg.com/tos-cn-i-6w9my0vskp/aaa.jpeg~tplv-shrink-cover-jpeg-image.image",
    "source": "hongguo",
    "episodes": 26
   },
   "episode": 21,
   "position": 123.456,
   "duration": 150.5,
   "updated": 1763000000.125
  },
  "hongguo:7300000000000000002": {
   "drama": {
    "id": "hongguo:7300000000000000002",
    "title": "老剧无站源",
    "cover": "https://p9-sign.toutiaoimg.com/bbb.jpeg",
    "actors": ["张三", "李四"],
    "score": 9.2
   },
   "episode": 7,
   "position": 300.0,
   "duration": 301.0,
   "updated": "1762900000.5"
  },
  "local:老电影1080k": {
   "drama": {
    "id": "local:老电影1080k",
    "title": "老电影.1080K",
    "episodes": 0
   },
   "episode": 1,
   "position": 60.0,
   "duration": 0.0,
   "updated": 1762800000.75,
   "local_path": "/home/ryen/下载/合并/老电影.1080K.mp4"
  },
  "duboku:6900000000000000003": {
   "drama": {
    "id": "duboku:6900000000000000003",
    "title": "未上线预约剧 第二季",
    "cover": "https://v2.duboku.com/ccc.jpg",
    "source": "duboku"
   },
   "episode": 3,
   "position": 0.0,
   "duration": 240.0,
   "updated": 1762700000.0
  }
 }
}`

// normalizeV1 applies the two v1-writer deltas the real v1 _load+save cycle
// produces on this fixture: backfill drama.source from the id prefix for rows
// missing it (watchstore.py:94-97).  Expected = fixture + normalization,
// key order aside.
func normalizeV1(data obj) obj {
	for _, section := range []string{"follows", "history"} {
		rows, _ := data[section].(obj)
		for id, v := range rows {
			row, _ := v.(obj)
			_ = id
			normalizeRow(row)
		}
	}
	return data
}

// ---------------------------------------------------------------------------
// TestV1FixtureRoundTrip — HARD acceptance: v1-format file → Go Load → Save →
// zero semantic drift.  Also asserts byte-identical re-save idempotency and
// that the v1 read-order (sorted by float(updated)) is preserved.
// ---------------------------------------------------------------------------

func TestV1FixtureRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(v1Fixture), 0o644); err != nil {
		t.Fatal(err)
	}

	s := Open(dir)
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	out := loadFile(t, path)
	got := decode(t, out).(obj)
	want := normalizeV1(decode(t, []byte(v1Fixture)).(obj))

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip drift:\ngot  = %s\nwant = %s", truncate(mustJSON(t, got)), truncate(mustJSON(t, want)))
	}

	// The ONLY expected change vs the raw fixture: drama.source backfilled on
	// the two legacy rows (follow "…002" and history "…002").  Assert that
	// explicitly so the test documents it.
	if got["follows"].(obj)["hongguo:7300000000000000002"].(obj)["drama"].(obj)["source"] != "hongguo" {
		t.Fatal("follow legacy row: expected drama.source backfilled")
	}
	rawFixture := decode(t, []byte(v1Fixture)).(obj)
	if _, ok := rawFixture["follows"].(obj)["hongguo:7300000000000000002"].(obj)["drama"].(obj)["source"]; ok {
		t.Fatal("fixture should NOT pre-set that source")
	}
	// "local:" pseudo-prefix must NOT become a source (watchstore.py:96).
	lh := got["history"].(obj)["local:老电影1080k"].(obj)["drama"].(obj)
	if _, ok := lh["source"]; ok {
		t.Fatalf("local: prefix must not become drama.source: %v", lh["source"])
	}
	// Number text must round-trip verbatim (json.Decoder.UseNumber), incl. the
	// numeric-STRING updated and the float "0.0"/int "0" typing distinction.
	h2 := got["history"].(obj)["hongguo:7300000000000000002"].(obj)
	if n, ok := h2["updated"].(string); !ok || n != "1762900000.5" {
		t.Fatalf("string updated must stay a string: %T %v", h2["updated"], h2["updated"])
	}
	if !bytes.Contains(out, []byte(`"added": 1729000000.0`)) {
		t.Fatalf("float text drifted: %s", truncate(out))
	}
	if !bytes.Contains(out, []byte(`"updated": "1762900000.5"`)) {
		t.Fatalf("string number drifted: %s", truncate(out))
	}
	// Ensure_ascii=False equivalent: CJK written raw, not \u-escaped.
	if !bytes.Contains(out, []byte("大夏武主")) || bytes.Contains(out, []byte(`\u5927`)) {
		t.Fatalf("ensure_ascii=False not reproduced")
	}
	// json.dump(indent=1) has NO trailing newline; Go TrimRight must match.
	if out[len(out)-1] == '\n' {
		t.Fatalf("trailing newline: v1 json.dump writes none")
	}

	// v1 would read this file and sort history identically to the fixture.
	wantOrder := []string{
		"hongguo:7600000000000000001", // 1763000000.125
		"hongguo:7300000000000000002", // "1762900000.5"
		"local:老电影1080k",              // 1762800000.75
		"duboku:6900000000000000003",  // 1762700000.0
	}
	if ord := v1SortedDesc(t, got); !reflect.DeepEqual(ord, wantOrder) {
		t.Fatalf("v1 read-order changed: %v", ord)
	}

	// Save → Load → Save again: byte-identical (idempotent persistence).
	first := append([]byte(nil), out...)
	s2 := Open(dir)
	if err := s2.Flush(); err != nil {
		t.Fatal(err)
	}
	if second := loadFile(t, path); !bytes.Equal(first, second) {
		t.Fatalf("double-save not byte-idempotent:\n%s\n%s", truncate(first), truncate(second))
	}

	// Typed view sanity over the loaded fixture.
	s3 := Open(dir)
	if !s3.IsFollowed("hongguo:7300000000000000002") {
		t.Fatal("legacy row missing 'followed' key must count as followed (v1 quirk)")
	}
	if s3.IsReserved("hongguo:7300000000000000002") {
		t.Fatal("missing 'reserved' key must count as not reserved")
	}
	if !s3.IsReserved("duboku:6900000000000000003") || s3.IsFollowed("duboku:6900000000000000003") {
		t.Fatal("reserve-only row flags wrong")
	}
	fl := s3.FollowList()
	if len(fl) != 3 {
		t.Fatalf("FollowList len %d", len(fl))
	}
	if fl[0].ID != "hongguo:7600000000000000001" || !fl[0].HasUpdate || fl[0].Badge != 26 {
		t.Fatalf("update置顶/标集数 wrong on row 1: %+v", fl[0])
	}
	if fl[1].ID != "duboku:6900000000000000003" || fl[1].HasUpdate || fl[1].Badge != 0 {
		t.Fatalf("reserved row must not badge: %+v", fl[1])
	}
	if fl[2].ID != "hongguo:7300000000000000002" || fl[2].HasUpdate || fl[2].Badge != 0 {
		t.Fatalf("已看 row (known==read) must not badge: %+v", fl[2])
	}
	if fl[0].SeenEpisode != 21 || !fl[0].HasWatched {
		t.Fatalf("SeenEpisode from history: %+v", fl[0])
	}
	if e := fl[2].Drama.Extra["actors"]; e == nil {
		t.Fatalf("unknown drama fields (actors) dropped from view")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---------------------------------------------------------------------------
// corruption tolerance (v1 _load branches, watchstore.py:100-120)
// ---------------------------------------------------------------------------

func TestCorruptFileTolerance(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		wantBackup bool
		wantRows   int
	}{
		{"badjson", `{"follows": {},`, true, 0},
		{"trailing-garbage", `{}` + "\n}garbage", true, 0},
		{"non-object-root", `[1, 2, 3]`, false, 0},
		{"scalar-root", `42`, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, FileName)
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			s := Open(dir)
			if len(s.history) != 0 || len(s.follows) != 0 {
				t.Fatal("corrupt store must start empty")
			}
			entries, _ := os.ReadDir(dir)
			backups := 0
			origStill := false
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), FileName+".corrupt-") {
					backups++
				}
				if e.Name() == FileName {
					origStill = true
				}
			}
			if tc.wantBackup && backups != 1 {
				t.Fatalf("expected 1 .corrupt-* backup, found %d", backups)
			}
			if tc.wantBackup && origStill {
				t.Fatal("v1 backup_broken renames the original away (os.replace) — it must be gone")
			}
			if !tc.wantBackup && (backups != 0 || !origStill) {
				t.Fatalf("non-dict root must be left in place untouched (v1: no backup)")
			}
			// Next save writes a clean file; backup keeps the old bytes.
			if err := s.Flush(); err != nil {
				t.Fatal(err)
			}
			got := decode(t, loadFile(t, path)).(obj)
			if len(got["follows"].(obj)) != 0 || len(got["history"].(obj)) != 0 {
				t.Fatal("fresh save not clean")
			}
			if tc.wantBackup {
				for _, e := range must(os.ReadDir(dir)) {
					if strings.HasPrefix(e.Name(), FileName+".corrupt-") {
						b := loadFile(t, filepath.Join(dir, e.Name()))
						if string(b) != tc.content {
							t.Fatalf("backup content lost: %q", b)
						}
					}
				}
			}
		})
	}
}

func TestInvalidUTF8Backup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte{0xff, 0xfe, '{'}, 0o644); err != nil {
		t.Fatal(err)
	}
	s := Open(dir)
	if len(s.follows) != 0 {
		t.Fatal("expected empty")
	}
	found := false
	for _, e := range must(os.ReadDir(dir)) {
		if strings.HasPrefix(e.Name(), FileName+".corrupt-") {
			found = true
		}
	}
	if !found {
		t.Fatal("UnicodeDecodeError branch must back the file up too")
	}
}

func TestMissingFileNoBackup(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	if len(s.follows) != 0 || len(s.history) != 0 {
		t.Fatal("missing file must start empty")
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatalf("Flush must create dir+file: %v", err)
	}
}

func TestNonDictRowsSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	os.WriteFile(path, []byte(`{"follows": {"bad": 5, "good": {"drama": {"id": "hongguo:x"}}}, "history": {"nope": [], "ok": {"drama": "not-a-dict"}}}`), 0o644)
	s := Open(dir)
	if len(s.follows) != 1 || len(s.history) != 1 {
		t.Fatalf("non-dict rows must be skipped: %v %v", len(s.follows), len(s.history))
	}
	// drama not a dict → drama={} backfill (v1 _normalize_row), no crash.
	row := s.history["ok"].(obj)
	if d, ok := row["drama"].(obj); !ok || len(d) != 0 {
		t.Fatalf("drama=%v want empty obj", row["drama"])
	}
}

func must[T any](v []T, err error) []T {
	if err != nil {
		panic(err)
	}
	return v
}

// ---------------------------------------------------------------------------
// atomic write
// ---------------------------------------------------------------------------

func TestAtomicWriteNoLeftovers(t *testing.T) {
	s, _, dir := newStore(t)
	for i := 0; i < 50; i++ {
		s.SaveWatch(fmt.Sprintf("hongguo:%06d", i%17), "t", "c", 1, float64(i), 100, false)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	entries := must(os.ReadDir(dir))
	for _, e := range entries {
		if e.Name() != FileName {
			t.Fatalf("leftover temp/backup: %s", e.Name())
		}
	}
	decode(t, loadFile(t, filepath.Join(dir, FileName))) // parses
}

func TestWriteFailureKeepsOldFileAndDirty(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	s.SaveWatch("hongguo:a", "t", "c", 1, 10, 100, true) // creates + writes file
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(v1Fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	before := loadFile(t, filepath.Join(dir, FileName))

	// 要造的是"写不进去"这个**结果**，方法必须跨平台。
	// 原来用 `chmod(dir, 0500)`：Windows 上 chmod 基本是 no-op
	// （CI 实测写照样成功 → `expected write error surfaced`），
	// 而且 root 下也无效，所以还多了一条 Skip。
	// 换成把"目录"变成一个**同名普通文件**：在它下面建临时文件
	// 在 unix 与 Windows 上都必然失败（ENOTDIR），判据回到平台无关。
	if err := os.Remove(dir); err != nil {
		t.Fatalf("腾出路径: %v", err)
	}
	// ⚠ 用 cerr 而不是 err：下面紧跟着 `err := s.Flush()`，
	//   我第一版在这里写了 `f, err := ...` 把 err 先声明了，
	//   于是那行变成 "no new variables on left side of :=" ——
	//   一个纯编译错，linux 与 windows 一起红（本机没有可用 Go，
	//   这类错只能靠 CI 发现，所以更要写完就逐行看一遍而不是指望编译器兜底）。
	f, cerr := os.Create(dir)
	if cerr != nil {
		t.Fatal(cerr)
	}
	_ = f.Close()
	s.SaveWatch("hongguo:a", "t", "c", 2, 10, 100, true)
	err := s.Flush()
	if err == nil {
		t.Fatal("expected write error surfaced")
	}
	s.mu.Lock()
	dirty := s.dirty
	s.mu.Unlock()
	if !dirty {
		t.Fatal("v1 keeps _dirty on OSError so nothing is lost; v2 must too")
	}
	after := loadFile(t, filepath.Join(dir, FileName))
	if !bytes.Equal(before, after) {
		t.Fatal("failed write must not damage the existing file")
	}
	os.Chmod(dir, 0o755)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(loadFile(t, filepath.Join(dir, FileName)), before) {
		t.Fatal("retry after recovered permissions must land")
	}
}

// ---------------------------------------------------------------------------
// save_watch semantics
// ---------------------------------------------------------------------------

func TestSaveWatchSemantics(t *testing.T) {
	t.Run("dedup: identical tick no-op", func(t *testing.T) {
		s, c, _ := newStore(t)
		var fired int
		s.OnChanged(func() { fired++ })
		s.SaveWatch("hongguo:x", "T", "C", 3, 100, 200, false) // new → writes + emits
		fired = 0
		c.t += 1 // stay under SAVE_INTERVAL so the second call skips commit too
		s.SaveWatch("hongguo:x", "T", "C", 3, 100.3, 200.3, false)
		if fired != 0 {
			t.Fatal("dedup tick must not emit")
		}
		h := s.History()
		if len(h) != 1 || h[0].Position != 100 {
			t.Fatalf("dedup tick must not update row: %+v", h[0])
		}
		// same episode but ≥0.5 delta → update happens
		s.SaveWatch("hongguo:x", "T", "C", 3, 100.6, 200, false)
		if fired != 1 || s.History()[0].Position != 100.6 {
			t.Fatalf("distant tick must update+emit: fired=%d", fired)
		}
		// different episode → update even at same position
		s.SaveWatch("hongguo:x", "T", "C", 4, 100.6, 200, false)
		if s.History()[0].Episode != 4 {
			t.Fatal("episode change must update")
		}
		// finished bypasses dedup
		s.SaveWatch("hongguo:x", "T", "C", 4, 100.6, 200, true)
		if h := s.History(); h[0].Position != 100.6 || h[0].Duration != 200 {
			t.Fatal("finished must still write")
		}
	})
	t.Run("duration<=0 keeps existing", func(t *testing.T) {
		s, _, _ := newStore(t)
		s.SaveWatch("hongguo:x", "T", "C", 1, 0, 300, false)
		s.SaveWatch("hongguo:x", "T", "C", 1, 50, 0, false)
		if h := s.History(); h[0].Duration != 300 {
			t.Fatalf("duration fallback: %v", h[0])
		}
	})
	t.Run("no-id early return, no emit", func(t *testing.T) {
		s, _, _ := newStore(t)
		var fired int
		s.OnChanged(func() { fired++ })
		s.SaveWatch("", "T", "C", 1, 1, 1, false)
		if fired != 0 || len(s.History()) != 0 {
			t.Fatal("empty id must early-return like v1")
		}
	})
	t.Run("history cap 300 evicts oldest updated", func(t *testing.T) {
		s, c, _ := newStore(t)
		for i := 0; i < 305; i++ {
			s.SaveWatch(fmt.Sprintf("hongguo:%03d", i%400), "T", "C", 1, 0, 100, false)
			c.t += 1 // distinct updated stamps
		}
		h := s.History()
		if len(h) != HistoryCap {
			t.Fatalf("cap: %d", len(h))
		}
		// the 5 oldest (ids 000..004 first written… re-written after wrap at
		// i=400? 305<400 so no wrap) must be evicted
		for i := 0; i < 5; i++ {
			if s.Watched(fmt.Sprintf("hongguo:%03d", i)) != nil {
				t.Fatalf("oldest %d not evicted", i)
			}
		}
		if s.Watched("hongguo:005") == nil {
			t.Fatal("boundary entry evicted")
		}
	})
	t.Run("finished commits immediately; non-finished throttled 30s", func(t *testing.T) {
		s, c, dir := newStore(t)
		s.SaveWatch("hongguo:x", "T", "C", 1, 10, 100, false) // 10s? now-lastWrite… lastWrite=0, 1.794e9 → commit
		c.t += 5
		s.SaveWatch("hongguo:x", "T", "C", 1, 60, 100, false) // Δ<30 → dirty, NOT written
		mid := loadFile(t, filepath.Join(dir, FileName))
		if !bytes.Contains(mid, []byte(`"position": 10`)) {
			t.Fatalf("throttled write leaked to disk: %s", truncate(mid))
		}
		c.t += 30
		s.SaveWatch("hongguo:x", "T", "C", 1, 90, 100, false) // Δ≥30 → commit
		if b := loadFile(t, filepath.Join(dir, FileName)); !bytes.Contains(b, []byte(`"position": 90`)) {
			t.Fatal("interval elapsed must force commit")
		}
		c.t += 1
		s.SaveWatch("hongguo:x", "T", "C", 2, 15, 100, true) // finished → immediate
		if b := loadFile(t, filepath.Join(dir, FileName)); !bytes.Contains(b, []byte(`"episode": 2`)) {
			t.Fatal("finished milestone must commit now")
		}
	})
	t.Run("drama merge preserves unknown fields; empty title/cover keep old", func(t *testing.T) {
		s, _, dir := newStore(t)
		// seed via fixture so history row has actors
		os.WriteFile(filepath.Join(dir, FileName), []byte(v1Fixture), 0o644)
		s = Open(dir)
		s.now = func() float64 { return 1763000001 }
		s.SaveWatch("hongguo:7300000000000000002", "", "", 8, 10, 20, true)
		e := s.Watched("hongguo:7300000000000000002")
		if e.Drama.Extra["actors"] == nil || e.Drama.Title != "老剧无站源" || e.Drama.Cover != "https://p9-sign.toutiaoimg.com/bbb.jpeg" {
			t.Fatalf("merge dropped history drama fields: %+v", e.Drama)
		}
		// and score (unknown follow drama field) stays
		fl := s.FollowList()
		for _, f := range fl {
			if f.ID == "hongguo:7300000000000000002" {
				if f.Drama.Extra["score"] == nil || f.Drama.Source != "hongguo" {
					t.Fatalf("follow drama unknown fields dropped/source lost: %+v", f.Drama)
				}
			}
		}
	})
}

// ---------------------------------------------------------------------------
// follow side-effects of save_watch (v1 500-505)
// ---------------------------------------------------------------------------

func TestSaveWatchFollowSync(t *testing.T) {
	s, _, _ := newStore(t)
	d := Drama{ID: "hongguo:x", Title: "某剧 第二季", Cover: "c", Episodes: 12}
	s.ToggleFollow(d) // status 想看, known 12
	set := func(id string) obj {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.follows[id].(obj)
	}
	s.SaveWatch("hongguo:x", "某剧 第二季", "c", 5, 10, 100, false)
	if r := set("hongguo:x"); r["status"] != "在看" || int0(r["known"]) != 12 {
		t.Fatalf("non-finished tick: %+v", r)
	}
	s.SaveWatch("hongguo:x", "某剧 第二季", "c", 12, 100, 100, true)
	// v1 save_watch never sets `marked` — 已看 here is the auto-sync, so the
	// flag must still be falsy (only mark_watched marks).
	if r := set("hongguo:x"); r["status"] != "已看" || int0(r["read"]) != 12 || truthy(r["marked"]) {
		t.Fatalf("finished last episode → 已看: %+v", r)
	}
	// finished mid-season stays 在看, read=max
	s.MarkUnmarkedForTest("hongguo:x")
	s.SaveWatch("hongguo:x", "某剧 第二季", "c", 7, 0, 0, true)
	if r := set("hongguo:x"); r["status"] != "在看" || int0(r["read"]) != 12 {
		t.Fatalf("finished mid-season: %+v", r)
	}
	// marked row: tick must not change status
	s.MarkWatched("hongguo:x")
	s.SaveWatch("hongguo:x", "某剧 第二季", "c", 2, 0, 0, false)
	if r := set("hongguo:x"); r["status"] != "已看" {
		t.Fatalf("marked row status churned: %+v", r)
	}
	// known grows with drama episodes
	s2, _, _ := newStore(t)
	s2.ToggleFollow(Drama{ID: "hongguo:y", Title: "T", Episodes: 3})
	s2.SaveWatch("hongguo:y", "T", "c", 1, 0, 0, false) // no episodes param → merged drama has no episodes…
	s2.mu.Lock()
	known1 := int0(s2.follows["hongguo:y"].(obj)["known"])
	s2.mu.Unlock()
	if known1 != 3 {
		t.Fatalf("known churned from missing episodes: %d", known1)
	}
}

// ---------------------------------------------------------------------------
// follow state machine
// ---------------------------------------------------------------------------

func TestToggleFollowStates(t *testing.T) {
	s, c, _ := newStore(t)
	d := Drama{ID: "hongguo:x", Title: "T", Episodes: 4, OnlineDate: "2026-01-01"}
	if added := s.ToggleFollow(d); !added {
		t.Fatal("fresh toggle must add")
	}
	if !s.IsFollowed("hongguo:x") || s.IsReserved("hongguo:x") {
		t.Fatal("post-add flags")
	}
	// Follow() idempotent
	if s.Follow(d) {
		t.Fatal("Follow on followed must return false")
	}
	// Unfollow deletes row (not reserved)
	if !s.Unfollow("hongguo:x") {
		t.Fatal("unfollow must report change")
	}
	if s.IsFollowed("hongguo:x") {
		t.Fatal("row must be gone")
	}
	if s.Unfollow("hongguo:x") {
		t.Fatal("unfollow of missing row is a no-op")
	}
	// toggle on missing id → empty id guard
	if s.ToggleFollow(Drama{}) {
		t.Fatal("empty id must return false")
	}
	c.t += 1
	// reserved + toggle-off keeps reservation (v1 221-225)
	s.ToggleFollow(d)
	s.mu.Lock()
	s.follows["hongguo:x"].(obj)["reserved"] = true
	s.mu.Unlock()
	if added := s.ToggleFollow(d); added {
		t.Fatal("toggle off returns added=false")
	}
	row := s.rowForTest("hongguo:x")
	if row == nil || truthy(row["followed"]) || !truthy(row["reserved"]) {
		t.Fatalf("reserved off-branch: %v", row)
	}
	if row["status"] != "想看" { // known=4 > 0
		t.Fatalf("status after reserved-unfollow: %v", row["status"])
	}
	// toggle on → followed=true again, reserved untouched
	if added := s.ToggleFollow(d); !added {
		t.Fatal("reserved-only → follow")
	}
	row = s.rowForTest("hongguo:x")
	if !truthy(row["followed"]) || !truthy(row["reserved"]) {
		t.Fatalf("re-follow must keep reservation: %v", row)
	}
	// RemoveFollow deletes unconditionally and emits even when absent
	var fired int
	s.OnChanged(func() { fired++ })
	s.RemoveFollow("hongguo:x")
	if s.rowForTest("hongguo:x") != nil || fired != 1 {
		t.Fatal("RemoveFollow")
	}
	s.RemoveFollow("hongguo:x")
	if fired != 2 {
		t.Fatal("v1 remove_follow emits unconditionally")
	}
	// 预约中 status → 想看 when following an explicit non-followed row
	s2, _, _ := newStore(t)
	s2.mu.Lock()
	s2.follows["hongguo:z"] = obj{"drama": obj{"id": "hongguo:z"}, "status": "预约中", "followed": false, "known": 0}
	s2.mu.Unlock()
	if !s2.ToggleFollow(Drama{ID: "hongguo:z"}) {
		t.Fatal("toggle 预约中-only → followed")
	}
	if r := s2.rowForTest("hongguo:z"); r["status"] != "想看" {
		t.Fatalf("预约中→想看: %v", r["status"])
	}
	// known<=0 reserved unfollow → status 预约中
	s2.mu.Lock()
	s2.follows["hongguo:z"].(obj)["reserved"] = true
	s2.mu.Unlock()
	s2.ToggleFollow(Drama{ID: "hongguo:z"})
	if r := s2.rowForTest("hongguo:z"); r["status"] != "预约中" {
		t.Fatalf("known<=0 → 预约中: %v", r["status"])
	}
}

// ---------------------------------------------------------------------------
// reserve / cancel / mark_watched
// ---------------------------------------------------------------------------

func TestReserveCancelMarkWatched(t *testing.T) {
	s, _, _ := newStore(t)
	if s.Reserve(Drama{}) {
		t.Fatal("empty id")
	}
	ok := s.Reserve(Drama{ID: "hongguo:x", Title: "T", Episodes: 0, ReleaseStatus: "premiere", OnlineDate: "2026-12-01"})
	if !ok {
		t.Fatal("reserve")
	}
	r := s.rowForTest("hongguo:x")
	if r["status"] != "预约中" || getBoolDefault(r, "followed", true) {
		t.Fatalf("reserve-only new row must have followed=false: %+v", r)
	}
	if int0(r["reserveSeason"]) != 1 || int0(r["reserveAtKnown"]) != 0 || !truthy(r["reserved"]) {
		t.Fatalf("reserve targets: %v", r)
	}
	// finished drama → reserveSeason = season+1
	s.Reserve(Drama{ID: "hongguo:fin", Title: "T 第二季", ReleaseStatus: "finished", Episodes: 20})
	if r := s.rowForTest("hongguo:fin"); int0(r["reserveSeason"]) != 3 {
		t.Fatalf("finished reserveSeason: %v", r["reserveSeason"])
	}
	// existing row merge: stored drama ∪ drama
	s.mu.Lock()
	s.follows["hongguo:x"].(obj)["drama"] = obj{"id": "hongguo:x", "title": "T", "cover": "old", "extraField": "keep"}
	s.mu.Unlock()
	s.Reserve(Drama{ID: "hongguo:x", Title: "T"})
	r = s.rowForTest("hongguo:x")
	if d, _ := r["drama"].(obj); d["extraField"] != "keep" {
		t.Fatalf("reserve must merge drama: %v", d)
	}
	// cancel: not-followed+reserved → row deleted
	if !s.CancelReservation("hongguo:x") {
		t.Fatal("cancel")
	}
	if s.rowForTest("hongguo:x") != nil {
		t.Fatal("reserve-only row must be deleted on cancel (v1 315-316)")
	}
	if s.CancelReservation("hongguo:x") || s.CancelReservation("nothing:here") {
		t.Fatal("cancel is idempotent-false")
	}
	// cancel followed row: 预约中→想看, row kept
	s.mu.Lock()
	s.follows["hongguo:cf"] = obj{"drama": obj{"id": "hongguo:cf"}, "status": "预约中", "followed": true, "reserved": true}
	s.mu.Unlock()
	if !s.CancelReservation("hongguo:cf") {
		t.Fatal("cancel cf")
	}
	r = s.rowForTest("hongguo:cf")
	if r["status"] != "想看" || truthy(r["reserved"]) {
		t.Fatalf("cancel cf: %v", r)
	}
	// mark_watched
	s.mu.Lock()
	s.follows["hongguo:mk"] = obj{"drama": obj{"id": "hongguo:mk"}, "status": "在看", "known": 8, "read": 3, "followed": true}
	s.mu.Unlock()
	var fired int
	s.OnChanged(func() { fired++ })
	s.MarkWatched("hongguo:mk")
	r = s.rowForTest("hongguo:mk")
	if r["status"] != "已看" || int0(r["read"]) != 8 || !truthy(r["marked"]) || fired != 1 {
		t.Fatalf("mark_watched: %v fired=%d", r, fired)
	}
	before := fired
	s.MarkWatched("hongguo:absent")
	if fired != before {
		t.Fatal("mark_watched on absent id must not emit (v1 early return)")
	}
	// reserved+mark_watched re-arms reserveAtKnown
	s.mu.Lock()
	s.follows["hongguo:rm"] = obj{"drama": obj{"id": "hongguo:rm"}, "status": "在看", "known": 5, "read": 1, "reserved": true, "reserveAtKnown": 2}
	s.mu.Unlock()
	s.MarkWatched("hongguo:rm")
	if r := s.rowForTest("hongguo:rm"); int0(r["reserveAtKnown"]) != 5 {
		t.Fatalf("re-arm reserveAtKnown: %v", r["reserveAtKnown"])
	}
}

// ---------------------------------------------------------------------------
// observe_drama: the 「有更新置顶标集数」engine (watchstore.py:348-426) —
// table-driven.
// ---------------------------------------------------------------------------

func TestObserveDrama(t *testing.T) {
	base := func() obj {
		return obj{
			"drama":  obj{"id": "hongguo:x", "title": "T", "source": "hongguo", "episodes": 10},
			"status": "在看", "known": 10, "read": 5, "marked": false,
			"added": 100.0, "season": 1, "latestSeason": 1, "newSeason": false,
			"lastChecked": 0, "followed": true, "reserved": false,
			"reserveSeason": 1, "reserveAtKnown": 10, "arrived": false,
			"arrivedSeen": true, "newEpisodes": 0, "updateAt": 0.0,
			"updateSeen": true, "sourceTime": "2026-01-01",
			"nextDrama": nil, "nextSeason": 0,
		}
	}
	cases := []struct {
		name     string
		mutate   func(obj)       // initial-row tweaks
		drama    Drama           // observed drama
		want     func(obj) error // assertions on the row after
		wantEmit bool
	}{
		{
			name:  "no-op: same drama",
			drama: Drama{ID: "hongguo:x", Title: "T", Source: "hongguo", Episodes: 10},
			want: func(r obj) error {
				if int0(r["newEpisodes"]) != 0 || truthy(r["newSeason"]) {
					return fmt.Errorf("unexpected churn: %v", r)
				}
				return nil
			},
			wantEmit: false,
		},
		{
			name:  "episode bump → 新集数 + update置顶",
			drama: Drama{ID: "hongguo:x", Title: "T", Source: "hongguo", Episodes: 13, OnlineDate: "2026-11-02"},
			want: func(r obj) error {
				if int0(r["known"]) != 13 {
					return fmt.Errorf("known=%v", r["known"])
				}
				if int0(r["newEpisodes"]) != 8 { // max(0, 13-read5)=8
					return fmt.Errorf("newEpisodes=%v", r["newEpisodes"])
				}
				if !truthy(r["updateAt"]) || truthy(r["updateSeen"]) {
					return fmt.Errorf("update置顶 flags")
				}
				if r["sourceTime"] != "2026-11-02" {
					return fmt.Errorf("sourceTime=%v", r["sourceTime"])
				}
				return nil
			},
			wantEmit: true,
		},
		{
			name:   "season jump → newSeason, nextDrama, known=read reset",
			drama:  Drama{ID: "hongguo:x", Title: "T 第二季", Source: "hongguo", Episodes: 4},
			mutate: func(r obj) { r["season"] = 1 },
			want: func(r obj) error {
				if int0(r["season"]) != 2 || int0(r["known"]) != 4 || int0(r["read"]) != 0 {
					return fmt.Errorf("season fields: %v %v %v", r["season"], r["known"], r["read"])
				}
				if !truthy(r["newSeason"]) || int0(r["latestSeason"]) != 2 {
					return fmt.Errorf("newSeason flags")
				}
				if nd, _ := r["nextDrama"].(obj); nd == nil || nd["title"] != "T 第二季" {
					return fmt.Errorf("nextDrama=%v", r["nextDrama"])
				}
				if int0(r["nextSeason"]) != 2 || int0(r["newEpisodes"]) != 0 {
					return fmt.Errorf("nextSeason/newEpisodes")
				}
				return nil
			},
			wantEmit: true,
		},
		{
			name: "reserved arrives on next episode",
			mutate: func(r obj) {
				r["reserved"] = true
				r["reserveSeason"] = 1
				r["reserveAtKnown"] = 10
			},
			drama: Drama{ID: "hongguo:x", Title: "T", Source: "hongguo", Episodes: 11},
			want: func(r obj) error {
				if !truthy(r["arrived"]) || truthy(r["arrivedSeen"]) || truthy(r["reserved"]) {
					return fmt.Errorf("arrival flags: %v/%v/%v", r["arrived"], r["arrivedSeen"], r["reserved"])
				}
				return nil
			},
			wantEmit: true,
		},
		{
			name: "reserved not-yet: below target",
			mutate: func(r obj) {
				r["reserved"] = true
				r["reserveSeason"] = 1
				r["reserveAtKnown"] = 12
			},
			drama: Drama{ID: "hongguo:x", Title: "T", Source: "hongguo", Episodes: 11},
			want: func(r obj) error {
				if truthy(r["arrived"]) || !truthy(r["reserved"]) {
					return fmt.Errorf("should stay reserved/not arrived: %v", r)
				}
				// 11 > known 10 (but below the 12-episode reservation target):
				// v1 still records the bump → newEpisodes = max(0, 11-read 5) = 6
				if int0(r["known"]) != 11 || int0(r["newEpisodes"]) != 6 ||
					!truthy(r["updateAt"]) || truthy(r["updateSeen"]) {
					return fmt.Errorf("below-target bump: %v", r)
				}
				return nil
			},
			wantEmit: true, // episodes>known fires despite no arrival (v1:403-411)
		},
		{
			name: "reserved-only known<=0 arrival: first episode online",
			mutate: func(r obj) {
				r["reserved"] = true
				r["followed"] = false
				r["status"] = "预约中"
				r["known"] = 0
				r["read"] = 0
				r["reserveAtKnown"] = 0
				r["reserveSeason"] = 1
			},
			drama: Drama{ID: "hongguo:x", Title: "T", Source: "hongguo", Episodes: 1},
			want: func(r obj) error {
				if !truthy(r["arrived"]) || truthy(r["reserved"]) {
					return fmt.Errorf("arrival")
				}
				if r["status"] != "想看" { // !followed && 预约中 → 想看
					return fmt.Errorf("status=%v", r["status"])
				}
				if int0(r["newEpisodes"]) != 1 { // episodes>known(0): unread=1
					return fmt.Errorf("newEpisodes=%v", r["newEpisodes"])
				}
				return nil
			},
			wantEmit: true,
		},
		{
			name: "finished 已看 gets reopened by episode bump",
			mutate: func(r obj) {
				r["status"] = "已看"
				r["marked"] = false
			},
			drama: Drama{ID: "hongguo:x", Title: "T", Source: "hongguo", Episodes: 11},
			want: func(r obj) error {
				if r["status"] != "在看" {
					return fmt.Errorf("status=%v", r["status"])
				}
				return nil
			},
			wantEmit: true,
		},
		{
			name: "marked 已看 keeps status",
			mutate: func(r obj) {
				r["status"] = "已看"
				r["marked"] = true
			},
			drama: Drama{ID: "hongguo:x", Title: "T", Source: "hongguo", Episodes: 11},
			want: func(r obj) error {
				if r["status"] != "已看" {
					return fmt.Errorf("status churned: %v", r["status"])
				}
				return nil
			},
			wantEmit: true,
		},
		{
			name:  "drama merge: extra fields preserved, unknown field added",
			drama: Drama{ID: "hongguo:x", Title: "T", Source: "hongguo", Episodes: 10, Extra: obj{"newField": 5}},
			want: func(r obj) error {
				d, _ := r["drama"].(obj)
				if d["newField"] == nil || d["source"] != "hongguo" {
					return fmt.Errorf("merge: %v", d)
				}
				return nil
			},
			wantEmit: true, // merged != stored
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newStore(t)
			row := base()
			if tc.mutate != nil {
				tc.mutate(row)
			}
			s.mu.Lock()
			s.follows["hongguo:x"] = row
			s.mu.Unlock()
			var fired int
			s.OnChanged(func() { fired++ })
			gotChanged := s.ObserveDrama(tc.drama)
			// 返回值与 emit 必须是同一件事的两种说法（壳层绑定就靠它决定要不要提示用户）。
			// 上一版这里丢弃返回值，壳层只能在旁边自己比较整行快照 ——
			// 那比较里含每次都会刷新的 lastChecked，于是"changed"几乎恒真。
			if gotChanged != tc.wantEmit {
				t.Fatalf("changed=%v wantEmit=%v（返回值与 emit 必须同源）", gotChanged, tc.wantEmit)
			}
			if err := tc.want(row); err != nil {
				t.Fatalf("post-state: %v", err)
			}
			if (fired > 0) != tc.wantEmit {
				t.Fatalf("emit=%v want=%v (fired=%d)", fired > 0, tc.wantEmit, fired)
			}
		})
	}
	// absent follow → silent, no emit
	s, _, _ := newStore(t)
	var fired int
	s.OnChanged(func() { fired++ })
	if got := s.ObserveDrama(Drama{ID: "nobody:here", Title: "T", Episodes: 5}); got {
		t.Fatal("没追的剧不该报 changed")
	}
	if fired != 0 {
		t.Fatal("observe of unfollowed drama must not emit")
	}
}

// ---------------------------------------------------------------------------
// mark_next_season
// ---------------------------------------------------------------------------

func TestMarkNextSeason(t *testing.T) {
	s, _, _ := newStore(t)
	s.mu.Lock()
	s.follows["hongguo:x"] = obj{"drama": obj{"id": "hongguo:x"}, "status": "在看", "followed": true, "latestSeason": 1, "season": 1}
	s.mu.Unlock()
	if s.MarkNextSeason("", Drama{}, 2) {
		t.Fatal("empty id")
	}
	var fired int
	s.OnChanged(func() { fired++ })
	if s.MarkNextSeason("hongguo:absent", Drama{ID: "hongguo:y", Title: "T 第二季"}, 0) {
		t.Fatal("absent follow")
	}
	if fired != 0 {
		t.Fatal("absent follow must not emit")
	}
	// season=0 → parsed from nextDrama title
	if !s.MarkNextSeason("hongguo:x", Drama{ID: "hongguo:y", Title: "T 第二季"}, 0) {
		t.Fatal("mark")
	}
	r := s.rowForTest("hongguo:x")
	if !truthy(r["arrived"]) || truthy(r["arrivedSeen"]) || truthy(r["reserved"]) || !truthy(r["newSeason"]) {
		t.Fatalf("flags: %v", r)
	}
	if int0(r["latestSeason"]) != 2 || int0(r["nextSeason"]) != 2 {
		t.Fatalf("season numbers: %v %v", r["latestSeason"], r["nextSeason"])
	}
	nd := r["nextDrama"].(obj)
	if nd["id"] != "hongguo:y" {
		t.Fatalf("nextDrama: %v", nd)
	}
	if !truthy(r["updateAt"]) || truthy(r["updateSeen"]) {
		t.Fatal("置顶 update flags")
	}
	if fired != 1 {
		t.Fatalf("emit count %d", fired)
	}
	// reserve-only (followed=false, 预约中) → 想看
	s.mu.Lock()
	s.follows["hongguo:z"] = obj{"drama": obj{"id": "hongguo:z"}, "status": "预约中", "followed": false, "reserved": true, "latestSeason": 1}
	s.mu.Unlock()
	s.MarkNextSeason("hongguo:z", Drama{ID: "hongguo:w", Title: "W 第3季"}, 0)
	if r := s.rowForTest("hongguo:z"); r["status"] != "想看" || int0(r["latestSeason"]) != 3 {
		t.Fatalf("reserve-only season jump: %v", r)
	}
}

// ---------------------------------------------------------------------------
// unseen arrivals / mark_arrivals_seen
// ---------------------------------------------------------------------------

func TestUnseenArrivals(t *testing.T) {
	s, _, _ := newStore(t)
	s.mu.Lock()
	s.follows = obj{
		"a": obj{"arrived": true, "arrivedSeen": false},                   // unseen arrival
		"b": obj{"newEpisodes": 3, "updateSeen": false},                   // unseen update
		"c": obj{"arrived": true, "arrivedSeen": true, "newEpisodes": 2},  // seen update… updateSeen missing ⇒ falsy ⇒ unseen!
		"d": obj{"arrived": false, "newEpisodes": 0, "updateSeen": false}, // no
		"e": obj{"newEpisodes": "2"},                                      // numeric-string tolerated (v1 int())
	}
	s.mu.Unlock()
	if n := s.UnseenArrivals(); n != 4 {
		t.Fatalf("unseen=%d want 4", n)
	}
	var fired int
	s.OnChanged(func() { fired++ })
	if !s.MarkArrivalsSeen() {
		t.Fatal("expected changed")
	}
	if fired != 0 {
		t.Fatal("v1 mark_arrivals_seen does not emit")
	}
	if s.UnseenArrivals() != 0 {
		t.Fatal("flags not cleared")
	}
	if s.MarkArrivalsSeen() {
		t.Fatal("second call must report no change")
	}
	s.mu.Lock()
	c := s.follows["c"].(obj)
	s.mu.Unlock()
	if !truthy(c["arrived"]) || int0(c["newEpisodes"]) != 2 {
		t.Fatal("arrival/update markers themselves must be kept (v1 181)")
	}
}

// ---------------------------------------------------------------------------
// remove_history / clear_history emit quirks
// ---------------------------------------------------------------------------

func TestRemoveClearHistory(t *testing.T) {
	s, _, dir := newStore(t)
	var fired int
	s.OnChanged(func() { fired++ })
	// Seed one real row (finished → commits immediately).
	s.SaveWatch("hongguo:x", "T", "C", 1, 1, 100, true)
	fired = 0
	before := loadFile(t, filepath.Join(dir, FileName))
	s.RemoveHistory("never:there") // no row: no touch but STILL emits (v1 quirk)
	if fired != 1 {
		t.Fatalf("remove_history emits unconditionally: %d", fired)
	}
	if after := loadFile(t, filepath.Join(dir, FileName)); !bytes.Equal(before, after) {
		t.Fatal("remove_history of absent row must not rewrite the file (v1: _touch only when existed)")
	}
	s.RemoveHistory("hongguo:x")
	if len(s.History()) != 0 {
		t.Fatal("row must be gone")
	}
	s.SaveWatch("hongguo:x", "T", "C", 1, 1, 100, true)
	s.ClearHistory()
	if len(s.History()) != 0 {
		t.Fatal("clear")
	}
	if b := loadFile(t, filepath.Join(dir, FileName)); !bytes.Contains(b, []byte(`"follows"`)) {
		t.Fatalf("clear must persist: %s", truncate(b))
	}
}

// ---------------------------------------------------------------------------
// 今天/昨天/更早 segmentation boundaries (watch.py _day_bucket semantics)
// ---------------------------------------------------------------------------

func TestDayBucketBoundaries(t *testing.T) {
	// Anchor: a local date. Build stamps as exact local datetimes.
	now := time.Now()
	y, m, d := now.Date()
	noon := time.Date(y, m, d, 12, 0, 0, 0, time.Local)
	cases := []struct {
		name  string
		stamp float64
		want  int
	}{
		{"just-after-local-midnight-today", float64(dayStart(noon).Unix()), 0},
		{"one-second-before-midnight-yesterday", float64(dayStart(noon).Unix()) - 1, 1},
		{"yesterday-noon", float64(noon.Add(-24 * time.Hour).Unix()), 1},
		{"yesterday-first-second", float64(dayStart(noon).Add(-24 * time.Hour).Unix()), 1},
		{"day-before-yesterday-last-second", float64(dayStart(noon).Add(-24*time.Hour).Unix()) - 1, 2},
		{"future-one-second-before-tomorrow-noon", float64(noon.Add(24*time.Hour - time.Second).Unix()), 0},
		{"tomorrow", float64(noon.Add(48 * time.Hour).Unix()), 0}, // v1: delta<=0 ⇒ 今天
		{"epoch-zero", 0, 2},
		{"negative", -1000000, 2},
		{"far-future-valid", float64(noon.AddDate(1, 0, 0).Unix()), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, c, _ := newStore(t)
			c.t = float64(noon.Unix()) // s.today() = noon today
			s.mu.Lock()
			s.history["hongguo:x"] = obj{"updated": tc.stamp}
			s.mu.Unlock()
			got := s.dayBucket(toFloat(tc.stamp))
			if got != tc.want {
				t.Fatalf("bucket(%v)=%d want %d", tc.stamp, got, tc.want)
			}
		})
	}
}

func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// TestSegmentsAgainstV1FileFormat: a store file → Segments() must bucket
// exactly as watch.py would for a known "today".
func TestSegments(t *testing.T) {
	s, c, _ := newStore(t)
	anchor := time.Now()
	y, m, d := anchor.Date()
	noon := time.Date(y, m, d, 12, 0, 0, 0, time.Local)
	c.t = float64(noon.Unix())
	mk := func(id string, off time.Duration) {
		s.mu.Lock()
		s.history[id] = obj{"drama": obj{"id": id, "title": id}, "episode": 1, "updated": float64(dayStart(noon).Add(off).Unix())}
		s.mu.Unlock()
	}
	mk("today-a", 8*time.Hour)
	mk("today-b", 23*time.Hour+59*time.Minute)
	mk("yday", -time.Hour) // yesterday 23:00
	mk("older", -49*time.Hour)
	segs := s.Segments()
	if len(segs) != 3 {
		t.Fatalf("segments: %+v", segs)
	}
	if segs[0].Name != "今天" || len(segs[0].Items) != 2 {
		t.Fatalf("today: %+v", segs[0])
	}
	if segs[0].Items[0].ID != "today-b" { // newer first
		t.Fatalf("order: %+v", segs[0].Items)
	}
	if segs[1].Name != "昨天" || segs[1].Items[0].ID != "yday" {
		t.Fatalf("yesterday: %+v", segs[1])
	}
	if segs[2].Name != "更早" || len(segs[2].Items) != 1 {
		t.Fatalf("earlier: %+v", segs[2])
	}
	if len(s.Today()) != 2 || len(s.Earlier()) != 1 {
		t.Fatal("helpers")
	}
	if len(s.Yesterday()) != 1 {
		t.Fatal("yesterday helper")
	}
	// empty bucket omitted like watch.py header loop over present rows
	s.ClearHistory()
	if len(s.Segments()) != 0 {
		t.Fatal("empty")
	}
}

// ---------------------------------------------------------------------------
// SeasonNumber ports (v1 53-59 clamp + Chinese numerals)
// ---------------------------------------------------------------------------

func TestSeasonNumber(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 1}, {"平凡之路", 1}, {"大夏武主 第二季", 2}, {"大夏武主 第2季", 2},
		{"第 3 部", 3}, {"第２季", 2}, {"第一百季", 100}, {"第一百一十一季", 111},
		{"第十二季", 12}, {"第二十季", 20}, {"第三十季", 30},
		{"二十季", 1}, {"三十季", 1}, // v1 regex requires the 第 prefix — bare CN numeral does NOT match
		{"第零季", 1}, {"第二百零一季", 1}, // clamp 1..200
		{"第二百季", 200}, {"第250季", 1}, {"第十季", 10}, {"第十季", 10}, {"第两季", 2},
		{"第一季", 1}, {"第O季", 1}, // non-class char → no match → 1
		{"第999999999999999999999季", 1}, // overflow → clamp path
	}
	for _, tc := range cases {
		if got := SeasonNumber(tc.in); got != tc.want {
			t.Errorf("SeasonNumber(%q)=%d want %d", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// concurrency
// ---------------------------------------------------------------------------

func TestConcurrentSafety(t *testing.T) {
	s, _, dir := newStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				id := fmt.Sprintf("hongguo:%d-%d", i, j)
				s.SaveWatch(id, "T", "C", j, float64(j)*2, 100, j%5 == 0)
				s.ToggleFollow(Drama{ID: id, Title: "T", Episodes: j})
				s.History()
				s.FollowList()
				s.Segments()
			}
		}(i)
	}
	wg.Wait()
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush after concurrent writes: %v", err)
	}
	got := decode(t, loadFile(t, filepath.Join(dir, FileName))) // must parse
	root := got.(obj)
	if len(root["follows"].(obj)) == 0 || len(root["history"].(obj)) == 0 {
		t.Fatal("lost writes")
	}
}

// ---------------------------------------------------------------------------
// v1 quirks: sort-crash coercion + Open/DefaultDataDir
// ---------------------------------------------------------------------------

func TestFloatCrashQuirk(t *testing.T) {
	// v1 watchstore.py:209 `float(row.get("updated") or 0)` CRASHES on a
	// non-numeric string ("abc"→ValueError). v2 coerces to 0 and never
	// crashes — documented intentional divergence.
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	os.WriteFile(path, []byte(`{"follows": {}, "history": {"x": {"updated": "abc"}, "y": {"updated": 5}}}`), 0o644)
	s := Open(dir)
	h := s.History()
	if len(h) != 2 || h[0].ID != "y" || h[1].Updated != 0 {
		t.Fatalf("coerce-to-0 ordering: %+v", h)
	}
}

func TestDefaultDataDirEnv(t *testing.T) {
	t.Setenv("ZHENGUO_DATA", "rel/path")
	if got := DefaultDataDir(); !filepath.IsAbs(got) || !strings.HasSuffix(got, filepath.Join("rel", "path")) {
		t.Fatalf("ZHENGUO_DATA→abspath: %q", got)
	}
	t.Setenv("ZHENGUO_DATA", "")
	// ⚠ 第五轮真机反馈连带修正：这条用例此前只在 Linux 上跑过，
	//    windows job 加上 internal 测试后当场红 9 条。逐条核过都是
	//    **测试自己假设 POSIX**，不是产品在 Windows 上不对；
	//    但"测试假设错"与"产品真错"必须分开核过才能这么说，
	//    每条的判据写在各自注释里。
	// 默认目录本来就是分平台的：unix 走 ~/.zhenguojian，
	// Windows 走 %LOCALAPPDATA%\ZhenguoJian（CI 实测返回
	// `C:\Users\runneradmin\AppData\Local\ZhenguoJian`，那是**期望值**，
	// 原来这句断言把 unix 分支写死成唯一正确答案）。
	got := DefaultDataDir()
	if runtime.GOOS == "windows" {
		if !filepath.IsAbs(got) || filepath.Base(got) != "ZhenguoJian" {
			// ⚠ 文案里不要写 `%LOCALAPPDATA%`：vet 会把 `%L` 当格式动词
			//   （`unknown verb L`），而 `go test` 默认跑 vet ——
			//   所以这一处同时让 linux 与 windows 两个 job 都红。
			//   改成不带百分号的说法，语义一样清楚。
			t.Fatalf("windows default 应落在 LOCALAPPDATA 下的 ZhenguoJian，实得 %q", got)
		}
	} else if got != filepath.Join(homeDir(), ".zhenguojian") {
		t.Fatalf("unix default: %q", got)
	}
}

func TestOpenWritesOnDemand(t *testing.T) {
	// Open on a fresh dir does not create the file (v1 never touches disk
	// without a mutation) — first Flush does.
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested")
	s := Open(sub)
	if _, err := os.Stat(filepath.Join(sub, FileName)); err == nil {
		t.Fatal("Open must not write")
	}
	if err := s.Flush(); err != nil { // Flush()=touch even without dirty? v1 flush→touch→dirty=true→commit; even empty store writes.
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sub, FileName)); err != nil {
		t.Fatalf("Flush must create nested dir + file: %v", err)
	}
}

// ---------------------------------------------------------------------------
// white-box helpers for tests (same package, production API stays clean)
// ---------------------------------------------------------------------------

// rowForTest returns the LIVE follow-row map (not a copy) so assertions can
// read internal state exactly as the store sees it.
func (s *Store) rowForTest(dramaID string) obj {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, _ := s.follows[dramaID].(obj)
	return row
}

// MarkUnmarkedForTest clears the `marked` flag (user "在看" override) so
// follow-status sync can be re-triggered in tests.
func (s *Store) MarkUnmarkedForTest(dramaID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.follows[dramaID].(obj); ok {
		r["marked"] = false
	}
}
