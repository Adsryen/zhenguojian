package core

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHongguoSearchAPIUsesH5Endpoint(t *testing.T) {
	downloader := &Downloader{
		limiter:       newRequestLimiter(1, 0),
		providerHosts: map[string]string{},
		client: &http.Client{Timeout: time.Second, Transport: suggestionTransport(func(request *http.Request) (*http.Response, error) {
			if request.URL.Host != "hongguoduanju.com" || request.URL.Path != "/api/search/result/" {
				t.Fatalf("unexpected API request: %s", request.URL)
			}
			if request.URL.Query().Get("query") != "一年升一境凡人修仙之外门杂役" {
				t.Fatalf("query lost: %s", request.URL)
			}
			if request.Header.Get("Referer") == "" {
				t.Fatal("search API request has no referer")
			}
			body := `{"searchList":[{"name":"一年升一境凡人修仙之外门杂役第二季","keyword":"123","video_data":{"series_id":"123","series_title":"一年升一境凡人修仙之外门杂役第二季","episode_cnt":338,"series_status":1,"series_cover":"https://example.test/c.jpg"}}],"totalCount":1,"hasMore":false}`
			return &http.Response{StatusCode: 200, Header: http.Header{},
				Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
	}
	entry, err := downloader.fetchHongguoSearchAPI(context.Background(), "一年升一境凡人修仙之外门杂役")
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.Dramas) != 1 || entry.Dramas[0].Title != "一年升一境凡人修仙之外门杂役第二季" ||
		entry.Dramas[0].EpisodeCount != "338" || entry.Total != 1 || entry.Limited {
		t.Fatalf("unexpected parsed entry: %+v", entry)
	}
}

func TestHongguoSearchFiltersUnrelatedFallbackRows(t *testing.T) {
	exact := Drama{Title: "一年升一境凡人修仙之外门杂役"}
	season := Drama{Title: "一年升一境凡人修仙之外门杂役第二季"}
	unrelated := Drama{Title: "乡野女霸"}
	rows := filterHongguoSearchDramas([]Drama{exact, season, unrelated}, "一年升一境凡人修仙之外门杂役")
	if len(rows) != 2 || rows[0].Title != exact.Title || rows[1].Title != season.Title {
		t.Fatalf("unexpected filtered rows: %+v", rows)
	}
	if got := filterHongguoSearchDramas([]Drama{unrelated}, "不存在的剧名xyz"); len(got) != 0 {
		t.Fatalf("unrelated rows were kept: %+v", got)
	}
}
