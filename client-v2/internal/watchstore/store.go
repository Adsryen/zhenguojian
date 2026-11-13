// Package watchstore is a pure-Go port of the v1 desktop client's local watch
// library (zhenguojian/app/zhenguojian/watchstore.py): the follow list (追剧)
// and viewing history (观看历史), persisted in a single JSON file
// watch-library.json under the app data directory.
//
// The on-disk format, mutation semantics (throttled writes, 300-entry history
// cap, sub-0.5s tick dedup, follow status machine, season/arrival update
// detection) and the corrupt-file tolerance of v1 are reproduced 1:1, so a
// watch-library.json written by v1 can be loaded and re-saved by v2 and vice
// versa with zero semantic drift.  See PORTING.md in this directory for the
// method-by-method mapping table and the list of intentional divergences.
//
// Rows are kept internally as decoded maps (json.Number preserved) so that
// fields v1 has but this package does not model (local_path, actors, legacy
// rows missing keys) round-trip unchanged.
package watchstore

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Mirror of v1 watchstore.py:23-24.
const (
	HistoryCap   = 300
	SaveInterval = 30.0 // seconds between progress-file writes while playing
	// FileName is the library file name inside the data dir (watchstore.py:67).
	FileName = "watch-library.json"
)

type obj = map[string]any

// ---------------------------------------------------------------------------
// Python-flavoured coercion helpers.  v1 relies on `float(x or 0)`,
// `int(x or 0)`, `str(x or "")` and truthiness everywhere; these reproduce it.
// Divergence (documented in PORTING.md): where Python would *crash* on a
// non-numeric string (float("abc") at watchstore.py:209), v2 coerces to 0.
// ---------------------------------------------------------------------------

// truthy mirrors Python bool(x): nil/false/""/0/empty containers are falsy.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, _ := x.Float64()
		return f != 0
	case float64:
		return x != 0
	case int:
		return x != 0
	case obj:
		return len(x) > 0
	case []any:
		return len(x) > 0
	}
	return true
}

// toFloat mirrors float(v or 0); numeric strings parse (Python float() also
// accepts surrounding whitespace); non-numeric strings yield 0 instead of the
// v1 ValueError crash.  NaN/Inf inputs (not reachable from JSON) → 0.
func toFloat(v any) float64 {
	if !truthy(v) {
		return 0
	}
	switch x := v.(type) {
	case bool:
		if x {
			return 1
		}
		return 0
	case json.Number:
		f, _ := x.Float64()
		return f
	case float64:
		if math.IsNaN(x) {
			return 0
		}
		return x
	case int:
		return float64(x)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil || math.IsNaN(f) {
			return 0
		}
		return f
	}
	return 0
}

// toInt mirrors int(v or 0): truncates toward zero, saturates far outside the
// int range instead of Python's bignums (call sites use episode/season counts).
func toInt(v any) int {
	f := toFloat(v)
	if f > math.MaxInt32 {
		return math.MaxInt32
	}
	if f < math.MinInt32 {
		return math.MinInt32
	}
	return int(f)
}

// intOr mirrors int(v or def).
func intOr(v any, def int) int {
	if !truthy(v) {
		return def
	}
	return toInt(v)
}

// int0 mirrors int(v or 0).
func int0(v any) int { return intOr(v, 0) }

// asStr mirrors str(v or "").
func asStr(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		if x == "" {
			return ""
		}
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "True"
		}
		return ""
	case float64:
		if x == 0 {
			return ""
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	}
	if !truthy(v) {
		return ""
	}
	return ""
}

// getBoolDefault mirrors bool(row.get(key, default)).
func getBoolDefault(o obj, key string, def bool) bool {
	v, ok := o[key]
	if !ok {
		return def
	}
	return truthy(v)
}

// jsonEqual is Python dict/list deep equality over decoded JSON, comparing
// numbers as floats (Python 5 == 5.0; json.Number("5") == json.Number("5.0")).
func jsonEqual(a, b any) bool {
	switch x := a.(type) {
	case obj:
		y, ok := b.(obj)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			yy, ok := y[k]
			if !ok || !jsonEqual(v, yy) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case json.Number:
		switch y := b.(type) {
		case json.Number:
			fx, ex := x.Float64()
			fy, ey := y.Float64()
			if ex == nil && ey == nil {
				return fx == fy
			}
			return x.String() == y.String()
		case float64, int:
			return toFloat(x) == toFloat(y)
		}
		return false
	case float64, int:
		switch b.(type) {
		case json.Number, float64, int:
			return toFloat(a) == toFloat(b)
		}
		return false
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	}
	return false
}

func cloneObj(v any) obj {
	out := obj{}
	if o, ok := v.(obj); ok {
		for k, val := range o {
			out[k] = val
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Typed views
// ---------------------------------------------------------------------------

// Drama is the flattened shape v1 keeps in each row's "drama" sub-dict.  Call
// sites that port v1 observe_drama/reserve_drama/toggle_follow should pass a
// *complete* drama (core fields + whatever the API returned in Extra), because
// v1 dict() / dict.update() copies the whole dict; see PORTING.md.
type Drama struct {
	ID            string
	Title         string
	Cover         string
	Source        string
	ReleaseStatus string
	OnlineDate    string
	Episodes      int
	// Extra carries every other key of the source drama dict (actors, score, …)
	// so merged rows stay faithful to v1.
	Extra obj
}

func dramaFromObj(o obj) Drama {
	d := Drama{
		ID:            asStr(o["id"]),
		Title:         asStr(o["title"]),
		Cover:         asStr(o["cover"]),
		Source:        asStr(o["source"]),
		ReleaseStatus: asStr(o["releaseStatus"]),
		OnlineDate:    asStr(o["onlineDate"]),
		Episodes:      toInt(o["episodes"]),
	}
	for k, v := range o {
		switch k {
		case "id", "title", "cover", "source", "releaseStatus", "onlineDate", "episodes":
			continue
		}
		if d.Extra == nil {
			d.Extra = obj{}
		}
		d.Extra[k] = v
	}
	return d
}

// toObj serialises the drama the way a Python caller's dict would look:
// id always present; title/cover/optional keys only when non-empty — v1
// callers always passed complete dicts, and skipping empty strings here
// prevents the merged!=stored equality check (observe_drama) from firing
// spuriously on sparse v2 calls.
func (d Drama) toObj() obj {
	o := obj{}
	for k, v := range d.Extra {
		o[k] = v
	}
	o["id"] = d.ID
	if d.Title != "" {
		o["title"] = d.Title
	}
	if d.Cover != "" {
		o["cover"] = d.Cover
	}
	if d.Source != "" {
		o["source"] = d.Source
	}
	if d.Episodes != 0 {
		o["episodes"] = d.Episodes
	}
	if d.ReleaseStatus != "" {
		o["releaseStatus"] = d.ReleaseStatus
	}
	if d.OnlineDate != "" {
		o["onlineDate"] = d.OnlineDate
	}
	return o
}

// HistoryEntry is the typed view of one history row (watchstore.py:70).
type HistoryEntry struct {
	ID           string
	Drama        Drama
	Episode      int
	Position     float64
	Duration     float64
	Updated      float64 // epoch seconds, v1 time.time()
	LocalPath    string
	HasLocalPath bool
}

// FollowEntry is the typed view of one follow row.  HasUpdate/Badge reproduce
// watch.py _update_state (watch.py:329-338); SeenEpisode reproduces the
// watched(id).episode lookup the follow panel does for 「看到」.
type FollowEntry struct {
	ID             string
	Drama          Drama
	Status         string // 想看 / 在看 / 已看 / 预约中
	Known          int
	Read           int
	Marked         bool
	Added          float64
	Season         int
	LatestSeason   int
	NewSeason      bool
	LastChecked    float64
	Followed       bool
	Reserved       bool
	ReserveSeason  int
	ReserveAtKnown int
	Arrived        bool
	ArrivedSeen    bool
	NewEpisodes    int
	UpdateAt       float64
	UpdateSeen     bool
	SourceTime     string
	NextDrama      *Drama
	NextSeason     int

	// Computed (v1 watch.py _update_state): 有更新置顶 + 「新 N 集」角标。
	HasUpdate bool
	Badge     int

	// Computed from the history table (v1 watch.py meta text).
	HasWatched  bool
	SeenEpisode int
}

// ToJSON / ToRowJSON export the v1 row key contract (camelCase) for shell
// bindings without touching the on-disk format.
func (d Drama) ToJSON() map[string]any {
	return map[string]any(d.toObj())
}

func (e FollowEntry) ToRowJSON() map[string]any {
	row := map[string]any{
		"id": e.ID, "status": e.Status, "known": e.Known, "read": e.Read,
		"marked": e.Marked, "added": e.Added, "season": e.Season,
		"latestSeason": e.LatestSeason, "newSeason": e.NewSeason,
		"lastChecked": e.LastChecked, "followed": e.Followed, "reserved": e.Reserved,
		"reserveSeason": e.ReserveSeason, "reserveAtKnown": e.ReserveAtKnown,
		"arrived": e.Arrived, "arrivedSeen": e.ArrivedSeen, "newEpisodes": e.NewEpisodes,
		"updateAt": e.UpdateAt, "updateSeen": e.UpdateSeen, "sourceTime": e.SourceTime,
		"hasUpdate": e.HasUpdate, "badge": e.Badge,
		"hasWatched": e.HasWatched, "seenEpisode": e.SeenEpisode,
		"drama": e.Drama.ToJSON(),
	}
	if e.NextDrama != nil {
		row["nextDrama"] = e.NextDrama.ToJSON()
		row["nextSeason"] = e.NextSeason
	} else {
		row["nextDrama"] = nil
	}
	return row
}

func (e HistoryEntry) ToRowJSON() map[string]any {
	row := map[string]any{
		"drama": e.Drama.ToJSON(), "episode": e.Episode,
		"position": e.Position, "duration": e.Duration, "updated": e.Updated,
	}
	if e.HasLocalPath {
		row["localPath"] = e.LocalPath
	}
	return row
}

// followUpdateState ports watch.py:329-338 _update_state exactly:
//
//	new  = int(row.get("newEpisodes") or max(0, known - read))
//	has  = bool(arrived) or new > 0
//	badge= known if known>0 else read+new   (only when has and not reserved)
func followUpdateState(o obj) (bool, int) {
	known := int0(o["known"])
	read := int0(o["read"])
	newEps := 0
	if truthy(o["newEpisodes"]) {
		newEps = toInt(o["newEpisodes"])
	} else if known-read > 0 {
		newEps = known - read
	}
	has := truthy(o["arrived"]) || newEps > 0
	badge := 0
	if has && !truthy(o["reserved"]) {
		if known > 0 {
			badge = known
		} else {
			badge = read + newEps
		}
	}
	return has, badge
}

func historyEntry(id string, o obj) HistoryEntry {
	e := HistoryEntry{
		ID:       id,
		Episode:  int0(o["episode"]),
		Position: toFloat(o["position"]),
		Duration: toFloat(o["duration"]),
		Updated:  toFloat(o["updated"]),
	}
	if d, ok := o["drama"].(obj); ok {
		e.Drama = dramaFromObj(d)
	}
	if v, ok := o["local_path"]; ok {
		e.HasLocalPath = truthy(v)
		e.LocalPath = asStr(v)
	}
	return e
}

func followEntry(id string, o obj, hist obj) FollowEntry {
	e := FollowEntry{
		ID:             id,
		Status:         asStr(o["status"]),
		Known:          int0(o["known"]),
		Read:           int0(o["read"]),
		Marked:         truthy(o["marked"]),
		Added:          toFloat(o["added"]),
		Season:         intOr(o["season"], 1),
		LatestSeason:   intOr(o["latestSeason"], 1),
		NewSeason:      truthy(o["newSeason"]),
		LastChecked:    toFloat(o["lastChecked"]),
		Followed:       getBoolDefault(o, "followed", true), // v1 default: missing ⇒ followed
		Reserved:       truthy(o["reserved"]),
		ReserveSeason:  int0(o["reserveSeason"]),
		ReserveAtKnown: int0(o["reserveAtKnown"]),
		Arrived:        truthy(o["arrived"]),
		ArrivedSeen:    truthy(o["arrivedSeen"]),
		NewEpisodes:    int0(o["newEpisodes"]),
		UpdateAt:       toFloat(o["updateAt"]),
		UpdateSeen:     truthy(o["updateSeen"]),
		SourceTime:     asStr(o["sourceTime"]),
		NextSeason:     int0(o["nextSeason"]),
	}
	if d, ok := o["drama"].(obj); ok {
		e.Drama = dramaFromObj(d)
	}
	if nd, ok := o["nextDrama"].(obj); ok {
		d := dramaFromObj(nd)
		e.NextDrama = &d
	}
	e.HasUpdate, e.Badge = followUpdateState(o)
	if hist != nil {
		e.HasWatched = true
		e.SeenEpisode = intOr(hist["episode"], 1) // watch.py: int(entry.get("episode") or 1)
	}
	return e
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

// Store is the Go port of v1 WatchStore.  All methods are safe for concurrent
// use (v1 used a single threading.Lock likewise; its unlocked reads are all
// taken under the mutex here).  The v1 `changed = pyqtSignal()` is replaced by
// OnChanged callbacks, fired outside the lock like Qt emitted on the GUI thread.
type Store struct {
	mu      sync.Mutex
	file    string
	follows obj
	history obj

	dirty     bool
	lastWrite float64
	lastErr   error

	// Injectable clocks for tests (defaults: time.Now).
	now   func() float64
	today func() time.Time

	onChange []func()
}

// DefaultDataDir ports tools.data_dir() (tools.py:122-129):
// ZHENGUO_DATA env wins; Windows → %LOCALAPPDATA%\ZhenguoJian (fallback ~);
// otherwise ~/.zhenguojian.
func DefaultDataDir() string {
	if base := os.Getenv("ZHENGUO_DATA"); base != "" {
		if abs, err := filepath.Abs(base); err == nil {
			return abs
		}
		return base
	}
	if runtime.GOOS == "windows" {
		root := os.Getenv("LOCALAPPDATA")
		if root == "" {
			root = homeDir()
		}
		return filepath.Join(root, "ZhenguoJian")
	}
	return filepath.Join(homeDir(), ".zhenguojian")
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// Open loads (or lazily creates) the watch-library.json inside dataDir.
// dataDir == "" selects DefaultDataDir().  Like v1 __init__ (watchstore.py:65-76)
// this never fails: a missing or corrupt file starts an empty library (a
// corrupt file is moved aside first, see load()).
func Open(dataDir string) *Store {
	if dataDir == "" {
		dataDir = DefaultDataDir()
	}
	s := &Store{
		file:    filepath.Join(dataDir, FileName),
		follows: obj{},
		history: obj{},
		now:     defaultNow,
		today:   time.Now,
	}
	s.load()
	return s
}

func defaultNow() float64 { return float64(time.Now().UnixNano()) / 1e9 }

// File returns the backing file path.
func (s *Store) File() string { return s.file }

// OnChanged registers a callback fired after every mutating operation,
// mirroring v1's `changed = pyqtSignal()` (emitted outside the lock).
func (s *Store) OnChanged(fn func()) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	s.onChange = append(s.onChange, fn)
	s.mu.Unlock()
}

func (s *Store) emitChanged() {
	s.mu.Lock()
	cbs := make([]func(), len(s.onChange))
	copy(cbs, s.onChange)
	s.mu.Unlock()
	for _, fn := range cbs {
		fn()
	}
}

// ---------------------------------------------------------------------------
// persistence
// ---------------------------------------------------------------------------

// normalizeRow ports WatchStore._normalize_row (watchstore.py:79-98): ensure
// drama is a dict, and backfill drama.source from the id prefix ("hongguo:xxx")
// for legacy rows — but never the "local:" pseudo-prefix.
func normalizeRow(row obj) {
	drama, ok := row["drama"].(obj)
	if !ok {
		row["drama"] = obj{}
		return
	}
	if !truthy(drama["source"]) {
		parts := strings.SplitN(asStr(drama["id"]), ":", 2)
		if len(parts) == 2 && parts[0] != "" && parts[0] != "local" {
			drama["source"] = parts[0]
		}
	}
}

// load ports WatchStore._load (watchstore.py:100-120) including tolerances:
//   - missing file            → empty, silent        (OSError branch)
//   - invalid JSON/UTF-8/trailing garbage → backupBroken + empty
//   - non-object root         → empty, NO backup
//   - non-object row values   → skipped
func (s *Store) load() {
	raw, err := os.ReadFile(s.file)
	if err != nil {
		return // v1: OSError → pass
	}
	if !utf8.Valid(raw) {
		backupBroken(s.file) // v1: UnicodeDecodeError → backup
		return
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // preserve number text verbatim for byte-faithful re-dump
	var data any
	if err := dec.Decode(&data); err != nil {
		backupBroken(s.file) // v1: JSONDecodeError → backup
		return
	}
	// Python json.load rejects trailing non-whitespace ("Extra data" → backup).
	// Decoder.More() misses `{} }garbage`, so compare the stream offset the way
	// CPython's scanner does (it skips only " \t\n\r").
	if rest := bytes.TrimLeft(raw[dec.InputOffset():], " \t\n\r"); len(rest) > 0 {
		backupBroken(s.file) // v1: "Extra data" JSONDecodeError → backup
		return
	}
	root, ok := data.(obj)
	if !ok {
		return // v1: not isinstance(data, dict) → pass (no backup)
	}
	if f, ok := root["follows"].(obj); ok {
		s.follows = normalizeRows(f)
	}
	if h, ok := root["history"].(obj); ok {
		s.history = normalizeRows(h)
	}
}

func normalizeRows(rows obj) obj {
	out := obj{}
	for k, v := range rows {
		row, ok := v.(obj)
		if !ok {
			continue // v1: isinstance(v, dict) filter
		}
		normalizeRow(row)
		out[k] = row
	}
	return out
}

// backupBroken ports tools.backup_broken (tools.py:55-65): rename the unparsable
// file aside as "<path>.corrupt-<YYYYmmdd-HHMMSS>"; returns the target path.
func backupBroken(path string) string {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	target := path + ".corrupt-" + time.Now().Format("20060102-150405")
	if os.Rename(path, target) != nil {
		return ""
	}
	return target
}

// commit ports WatchStore._commit (watchstore.py:122-152): atomic
// tmp+rename write in the same directory, json indent=1, ensure_ascii=False
// equivalent, no trailing newline.  Errors keep the store dirty and are
// surfaced via Flush(); v1 swallowed OSError silently.
func (s *Store) commit() error {
	if !s.dirty {
		return nil
	}
	root := obj{"follows": s.follows, "history": s.history}
	dir := filepath.Dir(s.file)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(root); err != nil {
		s.lastErr = err
		return err
	}
	payload := bytes.TrimRight(buf.Bytes(), "\n") // v1 json.dump: no trailing newline
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.lastErr = err
		return err
	}
	tmp, err := os.CreateTemp(dir, ".watch-*.tmp") // v1: prefix=".watch-" suffix=".tmp" dir=…
	if err != nil {
		s.lastErr = err
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		s.lastErr = err
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		s.lastErr = err
		return err
	}
	if err := os.Rename(tmpName, s.file); err != nil { // v1: os.replace
		os.Remove(tmpName)
		s.lastErr = err
		return err
	}
	s.dirty = false
	s.lastWrite = s.now()
	s.lastErr = nil
	return nil
}

// touch ports _touch: mark dirty and write now.
func (s *Store) touch() error {
	s.dirty = true
	return s.commit()
}

// Flush ports WatchStore.flush (watchstore.py:159-162): write pending state.
// Like v1 it writes unconditionally (it goes through _touch, which sets dirty
// first).  Returns the last write error, if any.
func (s *Store) Flush() error {
	s.mu.Lock()
	err := s.touch()
	s.mu.Unlock()
	return err
}

// ---------------------------------------------------------------------------
// queries (watchstore.py:165-210)
// ---------------------------------------------------------------------------

// IsFollowed ports is_followed (165-167).  Quirk preserved: a legacy row
// without the "followed" key counts as followed (row.get("followed", True)).
func (s *Store) IsFollowed(dramaID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.follows[dramaID].(obj)
	if !ok {
		return false
	}
	return getBoolDefault(row, "followed", true)
}

// IsReserved ports is_reserved (169-171).
func (s *Store) IsReserved(dramaID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.follows[dramaID].(obj)
	if !ok {
		return false
	}
	return truthy(row["reserved"])
}

// UnseenArrivals ports unseen_arrivals (173-179): count of unseen reservation
// arrivals + unseen episode updates (the follow-tab red dot).
func (s *Store) UnseenArrivals() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, row := range s.follows {
		o, ok := row.(obj)
		if !ok {
			continue
		}
		if (truthy(o["arrived"]) && !truthy(o["arrivedSeen"])) ||
			(int0(o["newEpisodes"]) > 0 && !truthy(o["updateSeen"])) {
			n++
		}
	}
	return n
}

// MarkArrivalsSeen ports mark_arrivals_seen (181-194): clear the red-dot
// flags (the arrived/update markers themselves stay).  Returns whether
// anything changed; like v1 it does NOT fire OnChanged.
func (s *Store) MarkArrivalsSeen() bool {
	changed := false
	s.mu.Lock()
	for _, row := range s.follows {
		o, ok := row.(obj)
		if !ok {
			continue
		}
		if truthy(o["arrived"]) && !truthy(o["arrivedSeen"]) {
			o["arrivedSeen"] = true
			changed = true
		}
		if int0(o["newEpisodes"]) > 0 && !truthy(o["updateSeen"]) {
			o["updateSeen"] = true
			changed = true
		}
	}
	if changed {
		s.touch()
	}
	s.mu.Unlock()
	return changed
}

// Watched ports watched (196-198): history row for dramaID or nil.
func (s *Store) Watched(dramaID string) *HistoryEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.history[dramaID].(obj)
	if !ok {
		return nil
	}
	e := historyEntry(dramaID, row)
	return &e
}

// History ports history (206-210): rows sorted by updated desc.
// Divergence: v1 crashes on a non-numeric string "updated" (float()); v2
// coerces to 0.  Equal timestamps: v1 tie order = dict insertion order
// (random across processes); v2 breaks ties by id for determinism.
func (s *Store) History() []HistoryEntry {
	s.mu.Lock()
	entries := make([]HistoryEntry, 0, len(s.history))
	for id, v := range s.history {
		if o, ok := v.(obj); ok {
			entries = append(entries, historyEntry(id, o))
		}
	}
	s.mu.Unlock()
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Updated != entries[j].Updated {
			return entries[i].Updated > entries[j].Updated
		}
		return entries[i].ID < entries[j].ID
	})
	return entries
}

// FollowList ports follows (200-204) plus the watch.py follow-panel view
// computation: sorted by added desc; per entry HasUpdate/Badge from
// _update_state and SeenEpisode from the history table.
func (s *Store) FollowList() []FollowEntry {
	s.mu.Lock()
	entries := make([]FollowEntry, 0, len(s.follows))
	for id, v := range s.follows {
		o, ok := v.(obj)
		if !ok {
			continue
		}
		hist, _ := s.history[id].(obj)
		entries = append(entries, followEntry(id, o, hist))
	}
	s.mu.Unlock()
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Added != entries[j].Added {
			return entries[i].Added > entries[j].Added
		}
		return entries[i].ID < entries[j].ID
	})
	return entries
}

// ---------------------------------------------------------------------------
// segmentation (v1 watch.py _day_bucket, watch.py:87-99 — 0=今天 1=昨天 2=更早)
// ---------------------------------------------------------------------------

var segmentNames = [3]string{"今天", "昨天", "更早"}

// Segment is one 今天/昨天/更早 bucket of history, newest first.
type Segment struct {
	Name  string
	Items []HistoryEntry
}

// dayBucket ports _day_bucket: local calendar-day difference vs local today;
// future timestamps count as 今天 (delta <= 0); invalid stamps → 更早.
// Implemented with a noon anchor so it matches Python's date() truncation
// across DST (Python: date.fromtimestamp vs date.today).
func (s *Store) dayBucket(stamp float64) int {
	if math.IsNaN(stamp) || math.IsInf(stamp, 0) || stamp < -6.2e10 || stamp > 6.2e10 {
		return 2 // date.fromtimestamp would raise → v1 returns 2
	}
	then := time.Unix(int64(math.Floor(stamp)), 0).In(time.Local)
	today := s.today()
	delta := calendarDayDiff(today, then)
	if delta <= 0 {
		return 0
	}
	if delta == 1 {
		return 1
	}
	return 2
}

// calendarDayDiff returns (a - b) in local calendar days.
func calendarDayDiff(a, b time.Time) int {
	ya, ma, da := a.Date()
	yb, mb, db := b.Date()
	sa := time.Date(ya, ma, da, 12, 0, 0, 0, a.Location())
	sb := time.Date(yb, mb, db, 12, 0, 0, 0, b.Location())
	return int(math.Round(sa.Sub(sb).Hours() / 24))
}

// Segments buckets History() into 今天/昨天/更早 like watch.py does.
func (s *Store) Segments() []Segment {
	entries := s.History()
	var buckets [3][]HistoryEntry
	for _, e := range entries {
		b := s.dayBucket(e.Updated)
		buckets[b] = append(buckets[b], e)
	}
	out := make([]Segment, 0, 3)
	for i, name := range segmentNames {
		if len(buckets[i]) > 0 {
			out = append(out, Segment{Name: name, Items: buckets[i]})
		}
	}
	return out
}

// Today is the 今天 segment items (v1 watch.py header 「今天」).
func (s *Store) Today() []HistoryEntry { return s.segmentItems(0) }

// Yesterday is the 昨天 segment items.
func (s *Store) Yesterday() []HistoryEntry { return s.segmentItems(1) }

// Earlier is the 更早 segment items.
func (s *Store) Earlier() []HistoryEntry { return s.segmentItems(2) }

func (s *Store) segmentItems(bucket int) []HistoryEntry {
	for _, seg := range s.Segments() {
		if seg.Name == segmentNames[bucket] {
			return seg.Items
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// follow ops
// ---------------------------------------------------------------------------

// ToggleFollow ports toggle_follow (watchstore.py:213-259).  Returns `added`
// (whether the drama ended up followed), same as the v1 bool return.
func (s *Store) ToggleFollow(drama Drama) bool {
	id := drama.ID
	if id == "" {
		return false
	}
	var added, changed bool
	s.mu.Lock()
	row, exists := s.follows[id].(obj)
	switch {
	case exists && getBoolDefault(row, "followed", true):
		if truthy(row["reserved"]) {
			// 还在预约中：取消追剧但保留预约，等上线仍然提醒。
			row["followed"] = false
			if int0(row["known"]) <= 0 {
				row["status"] = "预约中"
			} else {
				row["status"] = "想看"
			}
		} else {
			delete(s.follows, id)
		}
		added = false
		changed = true
	case exists:
		// 之前只预约、没追剧：现在补上追剧，预约保持不动。
		row["followed"] = true
		if asStr(row["status"]) == "预约中" {
			row["status"] = "想看"
		}
		added = true
		changed = true
	default:
		known := drama.Episodes // int(drama.get("episodes") or 0)
		season := SeasonNumber(drama.Title)
		read := 0
		if known > 0 {
			read = known
		}
		s.follows[id] = obj{
			"drama": drama.toObj(), "status": "想看",
			"known": known, "read": read,
			"marked": false, "added": s.now(),
			"season": season, "latestSeason": season,
			"newSeason": false, "lastChecked": 0,
			"followed": true, "reserved": false,
			"reserveSeason": season, "reserveAtKnown": known,
			"arrived": false, "arrivedSeen": true,
			"newEpisodes": 0, "updateAt": 0.0, "updateSeen": true,
			"sourceTime": drama.OnlineDate,
			"nextDrama":  nil, "nextSeason": 0,
		}
		added = true
		changed = true
	}
	if changed {
		s.touch()
	}
	s.mu.Unlock()
	if changed {
		s.emitChanged()
	}
	return added
}

// Follow is the idempotent wrapper the v1 UI used around toggle_follow
// (`if not is_followed(id): toggle_follow(drama)`).  Returns whether the call
// added the follow.
func (s *Store) Follow(drama Drama) bool {
	if drama.ID == "" || s.IsFollowed(drama.ID) {
		return false
	}
	return s.ToggleFollow(drama)
}

// Unfollow is the reverse idempotent wrapper: run only the followed→off branch
// of toggle_follow.  Returns whether anything changed.
func (s *Store) Unfollow(dramaID string) bool {
	s.mu.Lock()
	row, ok := s.follows[dramaID].(obj)
	if !ok || !getBoolDefault(row, "followed", true) {
		s.mu.Unlock()
		return false
	}
	if truthy(row["reserved"]) {
		row["followed"] = false
		if int0(row["known"]) <= 0 {
			row["status"] = "预约中"
		} else {
			row["status"] = "想看"
		}
	} else {
		delete(s.follows, dramaID)
	}
	s.touch()
	s.mu.Unlock()
	s.emitChanged()
	return true
}

// Reserve ports reserve_drama (watchstore.py:261-303): local reservation —
// target season/known recorded so observe_drama can flip arrived=True.
// Finished dramas aim at the *next* season; ongoing at the next episode;
// not-yet-online at the first episode.
func (s *Store) Reserve(drama Drama) bool {
	id := drama.ID
	if id == "" {
		return false
	}
	title := drama.Title
	season := SeasonNumber(title)
	known := drama.Episodes // int(drama.get("episodes") or 0)
	finished := drama.ReleaseStatus == "finished"
	s.mu.Lock()
	row, ok := s.follows[id].(obj)
	if !ok {
		status := "想看"
		if known <= 0 {
			status = "预约中"
		}
		read := 0
		if known > 0 {
			read = known
		}
		row = obj{
			"drama":  drama.toObj(),
			"status": status,
			"known":  known, "read": read,
			"marked": false, "added": s.now(),
			"season": season, "latestSeason": season,
			"newSeason": false, "lastChecked": 0,
			"followed":    false,
			"newEpisodes": 0, "updateAt": 0.0, "updateSeen": true,
			"sourceTime": drama.OnlineDate,
			"nextDrama":  nil, "nextSeason": 0,
		}
		s.follows[id] = row
	} else {
		stored := cloneObj(row["drama"])
		for k, v := range drama.toObj() { // v1: stored.update(drama)
			stored[k] = v
		}
		row["drama"] = stored
	}
	row["reserved"] = true
	row["arrived"] = false
	row["arrivedSeen"] = true
	row["nextDrama"] = nil
	row["nextSeason"] = 0
	if finished {
		row["reserveSeason"] = season + 1
	} else {
		row["reserveSeason"] = season
	}
	row["reserveAtKnown"] = known
	s.touch()
	s.mu.Unlock()
	s.emitChanged()
	return true
}

// CancelReservation ports cancel_reservation (watchstore.py:305-321).
func (s *Store) CancelReservation(dramaID string) bool {
	s.mu.Lock()
	row, ok := s.follows[dramaID].(obj)
	if !ok || !truthy(row["reserved"]) {
		s.mu.Unlock()
		return false
	}
	row["reserved"] = false
	row["arrived"] = false
	row["arrivedSeen"] = true
	row["nextDrama"] = nil
	row["nextSeason"] = 0
	if !getBoolDefault(row, "followed", true) {
		delete(s.follows, dramaID)
	} else if asStr(row["status"]) == "预约中" {
		row["status"] = "想看"
	}
	s.touch()
	s.mu.Unlock()
	s.emitChanged()
	return true
}

// RemoveFollow ports remove_follow (watchstore.py:323-328).  Quirk preserved:
// OnChanged fires even when the drama was not followed.
func (s *Store) RemoveFollow(dramaID string) {
	s.mu.Lock()
	if _, ok := s.follows[dramaID]; ok {
		delete(s.follows, dramaID)
		s.touch()
	}
	s.mu.Unlock()
	s.emitChanged()
}

// MarkWatched ports mark_watched (watchstore.py:330-346).  No-op (and no
// event) for an unknown id, same as v1.
func (s *Store) MarkWatched(dramaID string) {
	s.mu.Lock()
	follow, ok := s.follows[dramaID].(obj)
	if !ok {
		s.mu.Unlock()
		return
	}
	follow["status"] = "已看"
	follow["read"] = int0(follow["known"])
	follow["marked"] = true
	follow["arrived"] = false
	follow["arrivedSeen"] = true
	follow["newEpisodes"] = 0
	follow["updateSeen"] = true
	if truthy(follow["reserved"]) {
		// Re-arm for the next episode of the current season.
		follow["reserveAtKnown"] = int0(follow["known"])
	}
	s.touch()
	s.mu.Unlock()
	s.emitChanged()
}

// ObserveDrama ports observe_drama (watchstore.py:348-426) — the update
// detection engine behind 「有更新置顶标集数」: after a detail/API refresh,
// merges new drama data into the follow row, flips reservation arrivals, and
// bumps known/newEpisodes/newSeason counters.  Callers must pass a complete
// Drama (v1 passed the full API dict; see PORTING.md).
// ObserveDrama 返回"这次观察有没有真的改动数据"。
// ⚠ 这个返回值是第五十轮加上的：Store 内部本来就为了"要不要 touch/emit"
// 算好了 changed，但壳层绑定没用它，而是自己拿观察前后的**整行快照**做比较 ——
// 行里带着每次观察都会刷新的 lastChecked/updateAt，
// 于是那个"changed"几乎恒为真，只有在两次时钟恰好落在同一个浮点刻度上时才为假。
// （实测：go test -count=1 绿、-count=2 红，红在 idempotency 断言。）
// 现在只留一份真相：壳层用这个返回值，不再自己推断。
func (s *Store) ObserveDrama(drama Drama) bool {
	var changed bool
	s.mu.Lock()
	follow, ok := s.follows[drama.ID].(obj)
	if !ok {
		s.mu.Unlock()
		return false // v1: follow is None → return, no emit
	}
	stored := cloneObj(follow["drama"])
	seasonTitle := drama.Title // v1: drama.get("title") or stored.get("title")
	if seasonTitle == "" {
		seasonTitle = asStr(stored["title"])
	}
	season := SeasonNumber(seasonTitle)
	current := intOr(follow["season"], 1) // int(follow.get("season") or 1)
	episodes := drama.Episodes            // int(drama.get("episodes") or 0)
	now := s.now()
	merged := cloneObj(stored)
	for k, v := range drama.toObj() { // v1: merged.update(drama)
		merged[k] = v
	}
	if !jsonEqual(merged, stored) {
		follow["drama"] = merged
		changed = true
	}
	arrived := false
	if truthy(follow["reserved"]) {
		targetSeason := intOr(follow["reserveSeason"], current)
		targetKnown := intOr(follow["reserveAtKnown"], 0)
		if season > current && season >= targetSeason {
			arrived = true
		} else if season == current && episodes > targetKnown {
			arrived = true
		} else if episodes > 0 && intOr(follow["known"], 0) <= 0 {
			arrived = true
		}
		if arrived {
			follow["arrived"] = true
			follow["arrivedSeen"] = false
			follow["reserved"] = false
			if !getBoolDefault(follow, "followed", true) && asStr(follow["status"]) == "预约中" {
				follow["status"] = "想看"
			}
			changed = true
		}
	}
	if season > current {
		latest := max(intOr(follow["latestSeason"], current), season)
		follow["season"] = season
		follow["known"] = episodes
		follow["read"] = 0
		follow["latestSeason"] = latest
		follow["newSeason"] = true
		follow["nextDrama"] = drama.toObj() // v1: dict(drama)
		follow["nextSeason"] = season
		follow["updateAt"] = now
		follow["updateSeen"] = false
		follow["newEpisodes"] = 0
		follow["lastChecked"] = now
		changed = true
	} else {
		// Reserve-only rows start with known == read, so a new episode
		// still shows up as "新 N 集" after arrival.
		known := intOr(follow["known"], 0)
		follow["lastChecked"] = now
		if episodes > known {
			follow["known"] = episodes
			unread := max(0, episodes-intOr(follow["read"], 0))
			follow["newEpisodes"] = max(intOr(follow["newEpisodes"], 0), unread)
			follow["updateAt"] = now
			follow["updateSeen"] = false
			sourceTime := drama.OnlineDate // str(drama.onlineDate or follow.sourceTime or "")
			if sourceTime == "" {
				sourceTime = asStr(follow["sourceTime"])
			}
			follow["sourceTime"] = sourceTime
			if !truthy(follow["marked"]) && asStr(follow["status"]) == "已看" && unread > 0 {
				follow["status"] = "在看"
			}
			changed = true
		} else if arrived {
			changed = true
		}
	}
	if changed {
		s.touch()
	}
	s.mu.Unlock()
	if changed {
		s.emitChanged()
	}
	return changed
}

// MarkNextSeason ports mark_next_season (watchstore.py:428-454): when the
// backend finds a next season (separate drama id), flag the old season arrived
// and remember the entry (nextDrama/nextSeason).
func (s *Store) MarkNextSeason(dramaID string, nextDrama Drama, season int) bool {
	if dramaID == "" {
		return false
	}
	number := season
	if number == 0 { // v1: int(season or season_number(next_drama.get("title")))
		number = SeasonNumber(nextDrama.Title)
	}
	s.mu.Lock()
	follow, ok := s.follows[dramaID].(obj)
	if !ok {
		s.mu.Unlock()
		return false
	}
	follow["arrived"] = true
	follow["arrivedSeen"] = false
	follow["reserved"] = false
	follow["newSeason"] = true
	follow["latestSeason"] = max(intOr(follow["latestSeason"], 1), number)
	follow["nextDrama"] = nextDrama.toObj()
	follow["nextSeason"] = number
	follow["updateAt"] = s.now()
	follow["updateSeen"] = false
	if !getBoolDefault(follow, "followed", true) && asStr(follow["status"]) == "预约中" {
		follow["status"] = "想看"
	}
	s.touch()
	s.mu.Unlock()
	s.emitChanged()
	return true
}

// ---------------------------------------------------------------------------
// history ops
// ---------------------------------------------------------------------------

// SaveWatch ports save_watch (watchstore.py:457-512): record progress, called
// every few seconds while playing.  The in-memory update always happens; the
// file write is throttled (SaveInterval) so a long session doesn't serialise
// ~100KB every tick — a crash loses seconds of position, never more.  A
// finished episode is a milestone: it commits immediately.
//
// Divergence (PORTING.md): v1 stored dict(drama) wholesale; the flattened v2
// signature has no full drama, so the row's drama MERGES the new
// id/title/cover into the previous row's drama instead of replacing it,
// preserving fields v1 had (source, actors, episodes…).  The follow-side
// known bump uses that merged drama's "episodes" value.
func (s *Store) SaveWatch(dramaID, title, cover string, episode int, position, duration float64, finished bool) {
	if dramaID == "" {
		return
	}
	now := s.now()
	s.mu.Lock()
	existing, had := s.history[dramaID].(obj)
	if duration <= 0 && had {
		// Starting an episode records position 0 before the player reports
		// duration; keep the known duration instead of blanking the progress
		// percentage in the history list.
		duration = toFloat(existing["duration"])
	}
	// Paused playback keeps reporting the same time-pos.  Ignoring identical
	// ticks stops the history list from re-sorting every few seconds.
	if had && !finished &&
		intOr(existing["episode"], 0) == episode &&
		math.Abs(toFloat(existing["position"])-position) < 0.5 &&
		math.Abs(toFloat(existing["duration"])-duration) < 0.5 {
		s.mu.Unlock()
		return // no dirty, no emit (v1)
	}
	dramaObj := obj{}
	if had {
		if d, ok := existing["drama"].(obj); ok {
			for k, v := range d {
				dramaObj[k] = v
			}
		}
	}
	dramaObj["id"] = dramaID
	if title != "" || !truthy(dramaObj["title"]) {
		dramaObj["title"] = title
	}
	if cover != "" || !truthy(dramaObj["cover"]) {
		dramaObj["cover"] = cover
	}
	row := obj{
		"drama": dramaObj, "episode": episode,
		"position": position, "duration": duration,
		"updated": now,
	}
	s.history[dramaID] = row
	s.enforceHistoryCapLocked()
	follow, ok := s.follows[dramaID].(obj)
	if ok {
		known := max(intOr(follow["known"], 0), toInt(dramaObj["episodes"]))
		follow["known"] = known
		if !truthy(follow["marked"]) {
			if finished && (known <= 0 || episode >= known) {
				follow["status"] = "已看"
				follow["read"] = known
			} else {
				follow["status"] = "在看"
				if finished {
					follow["read"] = max(intOr(follow["read"], 0), episode)
				}
			}
		}
	}
	s.dirty = true
	if finished {
		s.commit()
	} else if now-s.lastWrite >= SaveInterval {
		s.commit()
	}
	s.mu.Unlock()
	s.emitChanged()
}

func (s *Store) enforceHistoryCapLocked() {
	n := len(s.history) - HistoryCap
	if n <= 0 {
		return
	}
	type kv struct {
		id string
		u  float64
	}
	all := make([]kv, 0, len(s.history))
	for id, v := range s.history {
		if o, ok := v.(obj); ok {
			all = append(all, kv{id, toFloat(o["updated"])})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].u != all[j].u {
			return all[i].u < all[j].u
		}
		return all[i].id < all[j].id // v1: stable-sort insertion-order ties
	})
	for _, e := range all[:n] {
		delete(s.history, e.id)
	}
}

// RemoveHistory ports remove_history (watchstore.py:514-519).  Quirk
// preserved: OnChanged fires unconditionally, touch only when the row existed.
func (s *Store) RemoveHistory(dramaID string) {
	s.mu.Lock()
	if _, ok := s.history[dramaID]; ok {
		delete(s.history, dramaID)
		s.touch()
	}
	s.mu.Unlock()
	s.emitChanged()
}

// ClearHistory ports clear_history (watchstore.py:639-643).
func (s *Store) ClearHistory() {
	s.mu.Lock()
	s.history = obj{}
	s.touch()
	s.mu.Unlock()
	s.emitChanged()
}

// SaveLocalWatch is the port of save_local_watch (watchstore.py:608-637)
// WITHOUT the filename→drama resolver: v1's _resolve_local (529-581) parses
// download_dir()/episodes.json (the download library index), which belongs to
// a different subsystem and is out of scope for this port.  v2 callers must
// resolve (dramaID, title) themselves — the v1 fallback for an unmatched
// file was id="local:"+normalised-stem, episode=1 — and the history row is
// written identically (local_path recorded, always committed via _touch,
// dedup keyed on the same local_path + sub-0.5s deltas).
func (s *Store) SaveLocalWatch(dramaID, title, path string, position, duration float64, finished bool) {
	if dramaID == "" {
		return
	}
	absPath := path
	if p, err := filepath.Abs(path); err == nil {
		absPath = p
	}
	s.mu.Lock()
	existing, had := s.history[dramaID].(obj)
	if duration <= 0 && had {
		duration = toFloat(existing["duration"])
	}
	if had && !finished &&
		asStr(existing["local_path"]) == absPath &&
		math.Abs(toFloat(existing["position"])-position) < 0.5 &&
		math.Abs(toFloat(existing["duration"])-duration) < 0.5 {
		s.mu.Unlock()
		return
	}
	dramaObj := obj{"id": dramaID, "title": title, "episodes": 0}
	row := obj{
		"drama": dramaObj, "episode": 1,
		"position": position, "duration": duration,
		"updated": s.now(), "local_path": absPath,
	}
	s.history[dramaID] = row
	s.enforceHistoryCapLocked()
	s.touch()
	s.mu.Unlock()
	s.emitChanged()
}

// ---------------------------------------------------------------------------
// 「看过」标记（v2-only 兄弟文件；绑定见 bindings_seen.go；范围 A：自
// localStorage `${NS}.watched` 迁移的真值源）。
//
// 为什么不复用 watch-library.json 既有字段：v1 对「看过」的最近锚点是 follow 行
// marked（watchstore.py:330-346 → 本文件 MarkWatched），但 marked 带进度同步副作用
// （read=known/newEpisodes=0/arrived=false）且只存在于有进度的追剧行；v2 详情页
// 「看过」按钮（local.ts 注释：不改变播放进度；不限已追剧）与其语义不等。
// 在 follow 行上加新字段同样不行：commit 顶层只写 {follows,history}（v1 save
// 同），v2 新顶层键会被 v1 回写丢弃；为未追剧的行创建 follow 行会混进 v1 追剧
// 网格（watch.py 全量渲染不过滤 followed）；poster_wall.py:756 的 drama["watched"]
// 在 v1 侧是无生产者死键。故真值 = 同目录独立文件 seen-library.json，v1 不读不写，
// watch-library.json 一字不动（v1 round-trip 兼容天然成立）。
//
// 形状 = { "<dramaID>": epoch 秒 }，落盘格式跟 commit 同款 house style
// （缩进 1 空格、不转义 HTML、无尾换行）。缺文件 = 空标记；文件损坏按空处理
// （非 v1 兼容资产，不做 .corrupt 备份）。持久化失败如实返回给调用方
// （前端需要该信号做乐观回滚），与既有 void 变更方法不同。
// ---------------------------------------------------------------------------

// SeenFileName is the seen-library file inside the data directory.
const SeenFileName = "seen-library.json"

// seenFile resolves the sibling path from the watch-library file.
func (s *Store) seenFile() string {
	return filepath.Join(filepath.Dir(s.file), SeenFileName)
}

// loadSeenLocked decodes seen-library.json into dramaID→epoch-seconds.
// Missing/corrupt file → empty map.  Callers must hold s.mu.
func (s *Store) loadSeenLocked() obj {
	out := obj{}
	raw, err := os.ReadFile(s.seenFile())
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return out
	}
	root := obj{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return out
	}
	for k, v := range root {
		out[k] = toFloat(v)
	}
	return out
}

// writeSeenLocked atomically replaces the seen file (tmp+rename in the same
// directory, mirroring commit; no dirty-retry — the binding layer opens the
// store fresh per call, disk is the only truth).  Callers must hold s.mu.
func (s *Store) writeSeenLocked(seen obj) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(seen); err != nil {
		return err
	}
	payload := bytes.TrimRight(buf.Bytes(), "\n")
	dir := filepath.Dir(s.file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".seen-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.seenFile()); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// setSeenLocked applies one mark transition and persists it.  Returns
// (resulting state, changed, err): no-op (no write) when the state already
// matches; on write failure the previous state stays on disk and is
// returned with the error.  Callers must hold s.mu and fire emitChanged
// themselves AFTER releasing it — emitChanged re-acquires the lock and
// sync.Mutex is not reentrant (v1 pattern: unlock-then-emit).
func (s *Store) setSeenLocked(dramaID string, on bool) (bool, bool, error) {
	seen := s.loadSeenLocked()
	_, had := seen[dramaID]
	if on == had {
		return had, false, nil
	}
	if on {
		seen[dramaID] = s.now()
	} else {
		delete(seen, dramaID)
	}
	if err := s.writeSeenLocked(seen); err != nil {
		return had, false, err
	}
	return on, true, nil
}

// SeenMarks returns every drama-level seen mark (dramaID → epoch seconds).
func (s *Store) SeenMarks() obj {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadSeenLocked()
}

// IsSeen reports whether dramaID carries a seen mark.
func (s *Store) IsSeen(dramaID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dramaID == "" {
		return false
	}
	_, ok := s.loadSeenLocked()[dramaID]
	return ok
}

// SetSeen writes (on=true) or clears (on=false) one mark; returns the
// resulting state, or the persist error with the previous state (the old
// file stays untouched — callers roll back their optimistic view).
func (s *Store) SetSeen(dramaID string, on bool) (bool, error) {
	if dramaID == "" {
		return false, os.ErrInvalid
	}
	s.mu.Lock()
	state, changed, err := s.setSeenLocked(dramaID, on)
	s.mu.Unlock()
	if changed {
		s.emitChanged()
	}
	return state, err
}

// ToggleSeen flips the mark for dramaID and persists.  Returns new state.
func (s *Store) ToggleSeen(dramaID string) (bool, error) {
	if dramaID == "" {
		return false, os.ErrInvalid
	}
	s.mu.Lock()
	_, had := s.loadSeenLocked()[dramaID]
	state, changed, err := s.setSeenLocked(dramaID, !had)
	s.mu.Unlock()
	if changed {
		s.emitChanged()
	}
	return state, err
}

// ImportSeenJSON merges a one-off migration payload — a JSON object of
// dramaID → timestamp (seconds; values > 1e11 are treated as milliseconds,
// the same tolerance as v1 normTs / services/local.ts).  Existing ids are
// kept untouched (Go truth wins); returns the number of ids added.
// Malformed JSON errors out; an empty payload is a no-op.
func (s *Store) ImportSeenJSON(entriesJSON string) (int, error) {
	trimmed := strings.TrimSpace(entriesJSON)
	if trimmed == "" {
		return 0, nil
	}
	root := obj{}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return 0, err
	}
	s.mu.Lock()
	seen := s.loadSeenLocked()
	added := 0
	for k, v := range root {
		if k == "" {
			continue
		}
		if _, had := seen[k]; had {
			continue
		}
		f := toFloat(v)
		if f > 1e11 { // 毫秒载荷 → 秒
			f /= 1000
		}
		seen[k] = f
		added++
	}
	if added == 0 {
		s.mu.Unlock()
		return 0, nil
	}
	err := s.writeSeenLocked(seen)
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	s.emitChanged()
	return added, nil
}
