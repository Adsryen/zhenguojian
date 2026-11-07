package core

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestHongguoDanmakuAllSkipsEmptyWindows(t *testing.T) {
	const duration = int64(160000)
	counts := map[int]int{0: 2, 60000: 1, 120000: 1}
	histogram := map[string]int64{
		"0": 2, "30000": 0, "60000": 1, "90000": 0, "120000": 1, "150000": 0,
	}
	var calls atomic.Int32
	d := rankingTestDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		var body struct {
			Business struct {
				Start int64 `json:"start_offset_time"`
			} `json:"business_param"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		start := int(body.Business.Start)
		rows := []any{}
		for i := 0; i < counts[start]; i++ {
			rows = append(rows, danmakuTestRow(danmakuTestVideo,
				strconv.Itoa(start+i), "弹幕", start+i*1000))
		}
		cursor, _ := json.Marshal(map[string]any{
			"danmaku_count":        4,
			"start_offset_time":    start,
			"danmaku_window_count": histogram,
		})
		result := map[string]any{"code": 0, "data": map[string]any{
			"data_list": rows,
			"extra": map[string]any{
				"next_query_danmaku_list_time": strconv.Itoa(start + 30000),
			},
			"common_list_info": map[string]any{"cursor": string(cursor)},
		}}
		raw, _ := json.Marshal(result)
		return rankingHTTPResponse(request, 200, string(raw)), nil
	})

	document, err := d.hongguoDanmakuAll(context.Background(), danmakuTestSeries, danmakuTestVideo, duration)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 non-empty windows, got %d requests", calls.Load())
	}
	if len(document.Items) != 4 || document.Total != 4 {
		t.Fatalf("items/total lost: %+v", document)
	}
	if document.SeriesID != danmakuTestSeries || document.EpisodeID != danmakuTestVideo {
		t.Fatalf("identity lost: %+v", document)
	}
}
