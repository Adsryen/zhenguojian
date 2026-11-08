package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// huangdouDeepLocalServer 模拟真网确认过的黄豆目录接口：无关键词参数，
// /api/drama/list 按页返回 30/30/5 行（第三页不足页宽即 HasMore=false）。
func huangdouDeepLocalServer(calls *atomic.Int32, missing bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		if missing || r.URL.Path != "/api/drama/list" {
			http.NotFound(w, r)
			return
		}
		rowCount := 30
		if n >= 3 {
			rowCount = 5
		}
		rows := make([]map[string]any, 0, rowCount)
		for index := 0; index < rowCount; index++ {
			title := fmt.Sprintf("黄豆剧P%d-%02d", n, index)
			if n == 1 && index == 0 {
				title = "我在女寝有张床"
			}
			rows = append(rows, map[string]any{"id": fmt.Sprintf("hd-p%d-i%d", n, index), "name": title})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"list": rows}})
	}))
}

func newDeepLocalEngine(t *testing.T, directory string) *nativeEngine {
	t.Helper()
	engine, err := newNativeEngine(directory)
	if err != nil {
		t.Fatal(err)
	}
	engine.downloader.limiter = newRequestLimiter(3, 0)
	return engine
}

func TestHuangdouSearchDeepLocalWarmsCatalog(t *testing.T) {
	var calls atomic.Int32
	upstream := huangdouDeepLocalServer(&calls, false)
	defer upstream.Close()
	engine := newDeepLocalEngine(t, t.TempDir())
	engine.downloader.cfg.HuangdouURL = upstream.URL

	result, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangdou, Query: "女寝"})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("warm should fetch exactly 3 pages, got %d", calls.Load())
	}
	if !result.LocalSearch {
		t.Fatal("deep-local result must still be flagged LocalSearch")
	}
	if !strings.Contains(result.Warning, "前 3 页") || !strings.Contains(result.Warning, "暂无在线关键词搜索") {
		t.Fatalf("missing deep-local warning: %q", result.Warning)
	}
	if len(result.Items) != 1 || !strings.Contains(result.Items[0].Title, "女寝") {
		t.Fatalf("warm search misses: %+v", result.Items)
	}

	callsAfterWarm := calls.Load()
	second, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangdou, Query: "P3-04"})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != callsAfterWarm {
		t.Fatalf("warm cache bypassed, extra calls: %d", calls.Load()-callsAfterWarm)
	}
	if second.Warning != "" || len(second.Items) != 1 {
		t.Fatalf("second search: warning=%q items=%+v", second.Warning, second.Items)
	}
}

func TestHuangdouSearchDeepLocalReportsWarmFailure(t *testing.T) {
	var calls atomic.Int32
	upstream := huangdouDeepLocalServer(&calls, true)
	defer upstream.Close()
	engine := newDeepLocalEngine(t, t.TempDir())
	engine.downloader.cfg.HuangdouURL = upstream.URL
	engine.downloader.cfg.Retries = 1

	result, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangdou, Query: "女寝"})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("page-1 failure must stop the warm loop, calls=%d", calls.Load())
	}
	if !strings.Contains(result.Warning, "抓取失败") {
		t.Fatalf("want warm-failure warning, got %q", result.Warning)
	}
	if len(result.Items) != 0 || !result.LocalSearch {
		t.Fatalf("items=%+v local=%v", result.Items, result.LocalSearch)
	}
}

func TestHuangdouSearchDeepLocalStopsEarlyOnShortPage(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		rowCount := 30
		if n >= 2 {
			rowCount = 7 // 第二页不足页宽 → HasMore=false，warm 应止步于 2 页
		}
		rows := make([]map[string]any, 0, rowCount)
		for index := 0; index < rowCount; index++ {
			title := fmt.Sprintf("黄豆剧P%d-%02d", n, index)
			if n == 2 && index == 0 {
				title = "穿越地铁9号线"
			}
			rows = append(rows, map[string]any{"id": fmt.Sprintf("hd-p%d-i%d", n, index), "name": title})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"list": rows}})
	}))
	defer upstream.Close()
	engine := newDeepLocalEngine(t, t.TempDir())
	engine.downloader.cfg.HuangdouURL = upstream.URL

	result, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangdou, Query: "地铁"})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("short page must stop the warm loop at page 2, calls=%d", calls.Load())
	}
	if !strings.Contains(result.Warning, "前 2 页") {
		t.Fatalf("warning should report 2 fetched pages: %q", result.Warning)
	}
	if len(result.Items) != 1 || !strings.Contains(result.Items[0].Title, "地铁") {
		t.Fatalf("warm search misses: %+v", result.Items)
	}
}

// huangguoVideoSearchHTML 结构裁剪自 2026-11-08 实网 GET /search?q=女寝 的响应
// （证据链见 .trellis/tasks/11-08-q8-search-all/notes.md）：服务端渲染的
// article.search-content-card，a→/series|/video 链接，img alt=标题，
// span.bg-black/55=更新进度；上游忽略分页参数，结果单页返回。
const huangguoVideoSearchHTML = `<html><body>
<article class="search-content-card group"><a href="/series/rxyp2x5y" class="focus-ring"><div class="relative"><img class="video-cover-img" alt="我在女寝有张床" src="/uploads/lib/series/1867/cover.webp?v=1"><span class="bg-black/55">更新至第3集</span></div></a><p class="font-card-title">我在女寝有张床</p></article>
<article class="search-content-card group"><a href="/video/snitk9x1"><img class="video-cover-img" alt="地铁逃生日" src="/uploads/lib/video/9/cover.webp"></a><p class="font-card-title">地铁逃生日</p></article>
</body></html>`

// huangguoVideoOnlineServer 模拟实网站点的两个面：/search?q=（在线搜索）与
// /videos?page=（目录）。若搜索路径被错误地重新引入深本地兜底，会打到
// /videos 上，被 videoCalls 计数抓住。
func huangguoVideoOnlineServer(searchCalls, videoCalls *atomic.Int32, lastQuery, lastUA *atomic.Value, searchBody string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search":
			searchCalls.Add(1)
			if lastQuery != nil {
				lastQuery.Store(r.URL.Query().Get("q"))
			}
			if lastUA != nil {
				lastUA.Store(r.Header.Get("User-Agent"))
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(searchBody))
		case strings.HasPrefix(r.URL.Path, "/videos"):
			videoCalls.Add(1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><body><article class="video-card"><a href="/series/base0001" title="目录剧"><img data-src="/c.jpg"></a></article></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestHuangguoVideoSearchParsesOnlineCards(t *testing.T) {
	var searchCalls, videoCalls atomic.Int32
	var lastQuery, lastUA atomic.Value
	upstream := huangguoVideoOnlineServer(&searchCalls, &videoCalls, &lastQuery, &lastUA, huangguoVideoSearchHTML)
	defer upstream.Close()
	engine := newDeepLocalEngine(t, t.TempDir())
	engine.downloader.cfg.HuangguoVideoURL = upstream.URL

	items, hasMore, err := engine.downloader.searchHuangguoVideo(context.Background(), 1, " 女寝 ")
	if err != nil {
		t.Fatal(err)
	}
	if hasMore {
		t.Fatal("site ignores paging, search must never advertise more")
	}
	if got := lastQuery.Load(); got != "女寝" {
		t.Fatalf("query not trimmed/escaped into q=: %v", got)
	}
	if ua, _ := lastUA.Load().(string); !strings.Contains(ua, "iPhone") {
		t.Fatalf("search must reuse the provider UA that passes the WAF, got %q", ua)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 cards, got %+v", items)
	}
	first := items[0]
	if first.Source != sourceHuangguoVideo || first.SourceID != "series/rxyp2x5y" || first.ID != providerDramaID(sourceHuangguoVideo, "series/rxyp2x5y") {
		t.Fatalf("row not normalized: %+v", first)
	}
	coverText := fmt.Sprintf("%v", first.Cover)
	if !strings.Contains(first.Title, "女寝") || !strings.Contains(coverText, "/uploads/lib/series/1867/cover.webp") || !strings.HasPrefix(coverText, "http") {
		t.Fatalf("title/cover wrong: %+v", first)
	}
	if first.CategoryName != "series" || items[1].CategoryName != "video" {
		t.Fatalf("category mapping wrong: %+v %+v", first, items[1])
	}
	if first.Remark != "更新至第3集" {
		t.Fatalf("remark from bg-black/55 lost: %+v", first)
	}
	if searchCalls.Load() != 1 || videoCalls.Load() != 0 {
		t.Fatalf("unexpected traffic: search=%d videos=%d", searchCalls.Load(), videoCalls.Load())
	}
}

func TestHuangguoVideoSearchSecondPageTerminatesWithoutCalls(t *testing.T) {
	var searchCalls, videoCalls atomic.Int32
	upstream := huangguoVideoOnlineServer(&searchCalls, &videoCalls, nil, nil, huangguoVideoSearchHTML)
	defer upstream.Close()
	engine := newDeepLocalEngine(t, t.TempDir())
	engine.downloader.cfg.HuangguoVideoURL = upstream.URL

	items, hasMore, err := engine.downloader.searchHuangguoVideo(context.Background(), 2, "女寝")
	if err != nil || hasMore || len(items) != 0 {
		t.Fatalf("page 2 must be terminal: items=%d more=%v err=%v", len(items), hasMore, err)
	}
	if searchCalls.Load() != 0 {
		t.Fatalf("page 2 hit the network %d times", searchCalls.Load())
	}
}

func TestHuangguoVideoSearchRejectsInvalidQuery(t *testing.T) {
	var searchCalls, videoCalls atomic.Int32
	upstream := huangguoVideoOnlineServer(&searchCalls, &videoCalls, nil, nil, huangguoVideoSearchHTML)
	defer upstream.Close()
	engine := newDeepLocalEngine(t, t.TempDir())
	engine.downloader.cfg.HuangguoVideoURL = upstream.URL

	for _, query := range []string{"", "   ", strings.Repeat("寝", 257)} {
		if _, _, err := engine.downloader.searchHuangguoVideo(context.Background(), 1, query); err == nil {
			t.Fatalf("query %q must be rejected", query)
		}
	}
	if searchCalls.Load() != 0 {
		t.Fatalf("invalid queries hit the network %d times", searchCalls.Load())
	}
}

func TestHuangguoVideoSearchParseFailureGuard(t *testing.T) {
	var searchCalls, videoCalls atomic.Int32
	upstream := huangguoVideoOnlineServer(&searchCalls, &videoCalls, nil, nil, "<html><body>something changed</body></html>")
	defer upstream.Close()
	engine := newDeepLocalEngine(t, t.TempDir())
	engine.downloader.cfg.HuangguoVideoURL = upstream.URL

	_, _, err := engine.downloader.searchHuangguoVideo(context.Background(), 1, "女寝")
	if err == nil || !strings.Contains(err.Error(), "解析失败") {
		t.Fatalf("DOM drift must surface as 解析失败, got %v", err)
	}

	// marker 存在但无卡片 → 真·零结果，不能报错
	upstream2 := huangguoVideoOnlineServer(&searchCalls, &videoCalls, nil, nil, `<html><body><div data-flag="search-content-card">无结果</div></body></html>`)
	defer upstream2.Close()
	engine.downloader.cfg.HuangguoVideoURL = upstream2.URL
	items, hasMore, err := engine.downloader.searchHuangguoVideo(context.Background(), 1, "zzz不存在")
	if err != nil || hasMore || len(items) != 0 {
		t.Fatalf("zero-result page must be a clean empty: items=%d more=%v err=%v", len(items), hasMore, err)
	}
}

// TestHuangguoVideoSearchOnlineBranchViaNativeCatalog 走 app_runtime.go :596
// 在线分支，确认黄果视频已脱离本地缓存过滤且不再触发深本地 warm。
func TestHuangguoVideoSearchOnlineBranchViaNativeCatalog(t *testing.T) {
	var searchCalls, videoCalls atomic.Int32
	upstream := huangguoVideoOnlineServer(&searchCalls, &videoCalls, nil, nil, huangguoVideoSearchHTML)
	defer upstream.Close()
	engine := newDeepLocalEngine(t, t.TempDir())
	engine.downloader.cfg.HuangguoVideoURL = upstream.URL

	result, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangguoVideo, Query: "地铁"})
	if err != nil {
		t.Fatal(err)
	}
	if result.LocalSearch || result.Warning != "" {
		t.Fatalf("online branch not used: local=%v warning=%q", result.LocalSearch, result.Warning)
	}
	if searchCalls.Load() != 1 || videoCalls.Load() != 0 {
		t.Fatalf("unexpected traffic: search=%d videos=%d", searchCalls.Load(), videoCalls.Load())
	}
	if len(result.Items) != 2 || result.HasMore {
		t.Fatalf("items=%+v more=%v", result.Items, result.HasMore)
	}
	if _, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangguoVideo, Page: 2, Query: "地铁"}); err != nil {
		t.Fatal(err)
	}
	if searchCalls.Load() != 1 || videoCalls.Load() != 0 {
		t.Fatalf("page 2 re-hit the network: search=%d videos=%d", searchCalls.Load(), videoCalls.Load())
	}
}

// TestLegacySearchOnlineBranchViaNativeCatalog 走 app_runtime.go :596 的在线
// 关键词分支，确认 cloudfront 已脱离本地缓存过滤。
func TestLegacySearchOnlineBranchViaNativeCatalog(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/app/playlet/keyword/search" {
			t.Errorf("online search hit unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		if r.Header.Get("Authorization") != "configured-session" {
			t.Error("online search lost the configured session")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"hash":false,"data":{"list":[{"id":"kw-live","title":"我在寡妇村当老师","cover":"https://cover.example/k.jpg"}]}}`))
	}))
	defer upstream.Close()
	engine := newDeepLocalEngine(t, t.TempDir())
	engine.downloader.cfg.APIBase = upstream.URL
	engine.downloader.cfg.Token = "configured-session"
	engine.downloader.cfg.InterfaceKey = fixtureLegacyProtocol.InterfaceKey
	engine.downloader.cfg.ParamKey = fixtureLegacyProtocol.ParamKey
	engine.downloader.cfg.ParamIV = fixtureLegacyProtocol.ParamIV

	result, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceCloudFront, Query: "寡妇"})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || result.LocalSearch || result.Warning != "" {
		t.Fatalf("online branch not used: calls=%d local=%v warning=%q", calls.Load(), result.LocalSearch, result.Warning)
	}
	if len(result.Items) != 1 || !strings.Contains(result.Items[0].Title, "寡妇") {
		t.Fatalf("online search misses: %+v", result.Items)
	}
	if result.Items[0].ID != providerDramaID(sourceCloudFront, "kw-live") {
		t.Fatalf("id not normalized: %+v", result.Items[0])
	}
}
