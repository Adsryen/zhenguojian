package native

import (
	"strings"
	"testing"
)

// Call 层 mock：记录调用、返回预置结果，不经核心引擎。
type libraryRecorder struct {
	calls  []libraryCallLog
	errs   []error
	i      int
	onCall func() // 每次 catalog 前触发（模拟在途 Reset）
}

type libraryCallLog struct {
	kind    string // "catalog" | "action"
	action  string
	payload map[string]any
	source  string
	query   string
	page    int
	lane    string
	force   bool
}

func (r *libraryRecorder) catalog(source, query, category string, page int, lane string, force bool) (map[string]any, error) {
	r.calls = append(r.calls, libraryCallLog{kind: "catalog", action: "catalog",
		source: source, query: query, page: page, lane: lane, force: force})
	if r.onCall != nil {
		r.onCall()
	}
	idx := r.i
	r.i++
	if r.errs != nil && idx < len(r.errs) && r.errs[idx] != nil {
		return nil, r.errs[idx]
	}
	return map[string]any{"items": []any{}, "hasMore": true, "page": page}, nil
}

func (r *libraryRecorder) action(action string, payload map[string]any) (map[string]any, error) {
	r.calls = append(r.calls, libraryCallLog{kind: "action", action: action, payload: payload})
	idx := r.i
	r.i++
	if r.errs != nil && idx < len(r.errs) && r.errs[idx] != nil {
		return nil, r.errs[idx]
	}
	return map[string]any{"items": []any{}, "hasMore": false, "page": 0}, nil
}

func (r *libraryRecorder) seam() libraryCalls {
	return libraryCalls{catalog: r.catalog, action: r.action}
}

// 「全部」tab 的分页游标：more 严格递增、绝不重发当前页（v1「下一页坏掉」坑）；
// 非 more（首屏/刷新）恒回 page=1。
func TestLibraryRecommendationsAllTabPaging(t *testing.T) {
	if err := ResetRecommendationPage(); err != nil {
		t.Fatal(err)
	}
	rec := &libraryRecorder{}
	calls := rec.seam()

	for n := 0; n < 3; n++ {
		if _, err := recommendationsWith(calls, "", false, n > 0); err != nil {
			t.Fatal(err)
		}
	}
	if len(rec.calls) != 3 {
		t.Fatalf("calls=%d, want 3", len(rec.calls))
	}
	want := []int{1, 2, 3}
	for i, c := range rec.calls {
		if c.kind != "catalog" || c.action != "catalog" {
			t.Fatalf("call %d kind/action=%s/%s, want catalog/catalog", i, c.kind, c.action)
		}
		if c.page != want[i] {
			t.Errorf("call %d page=%d, want %d（重发当前页=v1「下一页看起来坏掉」坑）", i, c.page, want[i])
		}
		if c.lane != "library" {
			t.Errorf("call %d lane=%q, want library（剧库/搜索 lane 隔离）", i, c.lane)
		}
		if c.source != "" { // Catalog("") 内部缺省 hongguo，与 v1 一致
			t.Errorf("call %d source=%q, want 空串（走 Catalog 缺省）", i, c.source)
		}
	}
	if got := RecommendationPageForTest(); got != 3 {
		t.Errorf("游标=%d, want 3", got)
	}

	// 刷新（非 more）必须回到 page 1，游标重新计数。
	if _, err := recommendationsWith(calls, "", true, false); err != nil {
		t.Fatal(err)
	}
	last := rec.calls[3]
	if last.page != 1 || !last.force {
		t.Errorf("刷新调用 page=%d force=%v, want page=1 force=true", last.page, last.force)
	}
	if got := RecommendationPageForTest(); got != 1 {
		t.Errorf("刷新后游标=%d, want 1", got)
	}
}

// 分页失败不得推进游标（v1 成功返回后才提交页码）。
func TestLibraryPageNotCommittedOnError(t *testing.T) {
	if err := ResetRecommendationPage(); err != nil {
		t.Fatal(err)
	}
	rec := &libraryRecorder{errs: []error{&Error{Message: "本地核心响应超时（catalog）", Code: "timeout"}}}
	if _, err := recommendationsWith(rec.seam(), "", false, true); err == nil {
		t.Fatal("错误必须透传")
	}
	if got := RecommendationPageForTest(); got != 0 {
		t.Errorf("失败后游标=%d, want 0（不提交）", got)
	}
}

// generation 守卫：「重置后才返回」的过期响应不得推进游标。
func TestLibraryGenerationGuard(t *testing.T) {
	if err := ResetRecommendationPage(); err != nil {
		t.Fatal(err)
	}
	rec := &libraryRecorder{}
	calls := rec.seam()
	if _, err := recommendationsWith(calls, "", false, false); err != nil { // page1，游标=1
		t.Fatal(err)
	}
	// 在途 more：catalog 返回前发生 Reset（模拟用户中途刷新）。
	inflight := &libraryRecorder{onCall: func() { _ = ResetRecommendationPage() }}
	if _, err := recommendationsWith(inflight.seam(), "", false, true); err != nil {
		t.Fatal(err)
	}
	if inflight.calls[0].page != 2 {
		t.Errorf("在途页码=%d, want 2", inflight.calls[0].page)
	}
	if got := RecommendationPageForTest(); got != 0 {
		t.Errorf("Reset 后过期提交应被丢弃，游标=%d, want 0", got)
	}
	// 重置后首屏回到 page1。
	if _, err := recommendationsWith(calls, "", false, false); err != nil {
		t.Fatal(err)
	}
	if rec.calls[len(rec.calls)-1].page != 1 {
		t.Errorf("Reset 后 more=false 应回 page1, got %d", rec.calls[len(rec.calls)-1].page)
	}
}

// 具体题材：payload 必须逐字对齐 v1 core_client.py:300-301——
// action "recommendations"，键仅 category/force/command，more→command="more"。
func TestLibraryRecommendationsActionPayload(t *testing.T) {
	rec := &libraryRecorder{}
	calls := rec.seam()
	if _, err := recommendationsWith(calls, "short_play", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := recommendationsWith(calls, "comic_series", false, true); err != nil {
		t.Fatal(err)
	}
	c0 := rec.calls[0]
	if c0.kind != "action" || c0.action != "recommendations" {
		t.Fatalf("call0 kind/action=%s/%s, want action/recommendations", c0.kind, c0.action)
	}
	if len(c0.payload) != 3 {
		t.Fatalf("payload=%v, want 仅 category/force/command 三键", c0.payload)
	}
	if c0.payload["category"] != "short_play" || c0.payload["force"] != true || c0.payload["command"] != "" {
		t.Errorf("payload=%v, want {short_play,true,\"\"}", c0.payload)
	}
	c1 := rec.calls[1]
	if c1.payload["command"] != "more" {
		t.Errorf("more 透传失败: payload=%v", c1.payload)
	}
	if _, ok := c1.payload["page"]; ok {
		t.Error("题材推荐不得带 page（核心自持游标，app_recommendations.go:114）")
	}
}

// 错误形状：门面原样透传 *Error；Error() 尾部 "[code]" 与 actions 域一致
// （bindings 直接 return，前端 callGoRaw 依此后缀提取 code）。
func TestLibraryErrorSuffixStyle(t *testing.T) {
	rec := &libraryRecorder{errs: []error{
		&Error{Message: "源被限流", Code: "source_blocked", RetryAt: "2026-11-10T11:00:00Z"},
	}}
	_, err := recommendationsWith(rec.seam(), "short_play", false, false)
	if err == nil {
		t.Fatal("want error")
	}
	if _, ok := err.(*Error); !ok {
		t.Fatalf("错误类型=%T, want *native.Error", err)
	}
	if !strings.HasSuffix(err.Error(), "[source_blocked]") {
		t.Errorf("Error()=%q, want 尾部 [source_blocked]", err.Error())
	}
}
