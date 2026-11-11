package native

import (
	"strings"
	"testing"
)

// Call 层 mock：记录调用、返回预置结果，不经核心引擎（library_test.go 同款套路）。
type rankRecorder struct {
	calls   []libraryCallLog
	results []map[string]any
	errs    []error
	i       int
}

func (r *rankRecorder) action(action string, payload map[string]any) (map[string]any, error) {
	r.calls = append(r.calls, libraryCallLog{kind: "action", action: action, payload: payload})
	idx := r.i
	r.i++
	if r.errs != nil && idx < len(r.errs) && r.errs[idx] != nil {
		return nil, r.errs[idx]
	}
	if r.results != nil && idx < len(r.results) && r.results[idx] != nil {
		return r.results[idx], nil
	}
	return map[string]any{"items": []any{}}, nil
}

func withRankRecorder(t *testing.T, r *rankRecorder) {
	t.Helper()
	prev := rankCalls
	rankCalls = rankingsCalls{action: r.action}
	t.Cleanup(func() { rankCalls = prev })
}

func TestRankingsPayloadShape(t *testing.T) {
	r := &rankRecorder{results: []map[string]any{{
		"boardId": "hongguo-hot", "page": 2, "hasMore": true, "totalPages": 5,
		"updatedText": "11月10日已更新",
	}}}
	withRankRecorder(t, r)
	got, err := Rankings("hongguo-hot", 2, true)
	if err != nil {
		t.Fatalf("Rankings: %v", err)
	}
	if len(r.calls) != 1 || r.calls[0].action != "rankings" {
		t.Fatalf("calls = %+v", r.calls)
	}
	p := r.calls[0].payload
	if p["board"] != "hongguo-hot" || p["page"] != 2 || p["force"] != true {
		t.Fatalf("payload = %+v, want board/page/force = hongguo-hot/2/true", p)
	}
	// 应答平铺透传，不裁剪键。
	if got["updatedText"] != "11月10日已更新" || got["totalPages"] != 5 {
		t.Fatalf("got = %+v", got)
	}
}

func TestRankingsClampsPage(t *testing.T) {
	r := &rankRecorder{}
	withRankRecorder(t, r)
	for _, page := range []int{0, -3} {
		if _, err := Rankings("huangdou-all", page, false); err != nil {
			t.Fatalf("Rankings(page=%d): %v", page, err)
		}
	}
	for _, c := range r.calls {
		if c.payload["page"] != 1 {
			t.Fatalf("payload = %+v, want page clamped to 1 (v1 main.py max(1,page))", c.payload)
		}
		if c.payload["force"] != false {
			t.Fatalf("payload = %+v", c.payload)
		}
	}
}

func TestRankingsEmptyBoard(t *testing.T) {
	r := &rankRecorder{}
	withRankRecorder(t, r)
	_, err := Rankings("", 1, false)
	if err == nil || !strings.Contains(err.Error(), "榜单 id 不能为空") {
		t.Fatalf("err = %v, want 榜单 id 不能为空", err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("empty board must not reach core, calls=%+v", r.calls)
	}
}

func TestRankingBoardsAction(t *testing.T) {
	r := &rankRecorder{results: []map[string]any{{
		"items": []any{map[string]any{"id": "hongguo-hot", "source": "hongguo", "name": "总热播榜"}},
	}}}
	withRankRecorder(t, r)
	got, err := RankingBoards()
	if err != nil {
		t.Fatalf("RankingBoards: %v", err)
	}
	if len(r.calls) != 1 || r.calls[0].action != "rankingBoards" || r.calls[0].payload != nil {
		t.Fatalf("calls = %+v", r.calls)
	}
	items, ok := got["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("got = %+v", got)
	}
}
