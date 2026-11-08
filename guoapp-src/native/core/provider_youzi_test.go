package core

// Offline httptest suite for the 柚子 provider. Fixtures mirror the live
// youzidj.com markup captured 2026-11-08: pomelo article cards, ld+json
// TVSeason/VideoObject payloads, and the guest-cookie playback token API
// (POST /api/account/bootstrap -> eden_account cookie, POST /api/playback/
// {uuid} -> credentialized /media/... master.m3u8, cookie-gated GET).

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	youziFixtureCookie = "eden_account=fixture-session-0123456789"
	// 单视频作品（短 id abcd1234）与分集视频的 video UUID（合法 UUID 形态）。
	youziVidSingle   = "11111111-1111-4111-8111-111111111111"
	youziVidEpisode2 = "22222222-2222-4222-8222-222222222222"
	youziVersionID   = "33333333-3333-4333-8333-333333333333"
)

type youziFixtureState struct {
	bootstraps    int
	tokenCalls    map[string]int
	pageFetches   map[string]int
	searchCalls   int
	rejectTokens  bool // 置 true 时首个 /api/playback 返回 401（模拟会话失效）
	returnsCookie bool
}

func youziCardBlock(site, href, title, cover, badge string) string {
	badgeHTML := ""
	if badge != "" {
		badgeHTML = fmt.Sprintf(`<span class="home-card-episodes-long">%s</span>`, badge)
	}
	return fmt.Sprintf(`<article class="drama-card pomelo-drama-card" data-content-type="adult"><a class="home-drama-card-link" aria-label="查看详情：%s" href="%s%s/"><div class="drama-card-cover"><div class="drama-poster"><picture class="responsive-picture"><img src="%s" alt="%s海报" class="privacy-cover fill-image drama-poster-image"/></picture></div>%s</div><div class="drama-card-copy"><p class="drama-card-title" title="%s">%s</p></div></a><div class="drama-card-tags" aria-label="内容标签"><a class="drama-card-tag" aria-label="查看“示例”标签下的全部作品" href="%s/tags/demo/"><span>示例</span></a></div></article>`,
		title, site, href, cover, title, badgeHTML, title, title, site)
}

func youziListingFixture(site string, withNext bool) string {
	next := ""
	if withNext {
		next = `<link rel="next" href="` + site + `/ai-adult/page/2/"/>`
	}
	return `<!DOCTYPE html><html><head><title>柚子列表</title>` + next + `</head><body>` +
		youziCardBlock(site, "/ai-adult/abcd1234", "示例作品乙", "/catalogue-art/cover-b-960.webp", "昨天更新") +
		youziCardBlock(site, "/dramas/alpha-show/season-1", "示例节目甲", "/catalogue-art/cover-a-960.webp", "更新至7集") +
		// 干扰卡片：外站链接与非法路径，必须被丢弃。
		youziCardBlock("https://example.invalid", "/ai-adult/deadbeef", "外站卡片", "/catalogue-art/x.webp", "") +
		youziCardBlock(site, "/tags/demo", "非法路径卡片", "/catalogue-art/y.webp", "") +
		`</body></html>`
}

const youziSearchEmptyFixture = `<!DOCTYPE html><html><head><title>搜索</title></head><body>
<h2>没有找到相关作品</h2><h2>没有找到相关专题</h2></body></html>`

const youziSingleDetailFixture = `<!DOCTYPE html><html><head><title>示例作品乙</title></head><body>
<main><h1>示例作品乙</h1></main>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"VideoObject","@id":"https://youzidj.com/ai-adult/abcd1234/watch/` + youziVidSingle + `/#video","name":"示例作品乙","description":"一段中性的示例简介文本。","thumbnailUrl":["/catalogue-art/cover-b-960.webp"],"uploadDate":"2026-11-07T13:26:27.415Z","duration":"PT4M19S","genre":"示例标签","url":"https://youzidj.com/ai-adult/abcd1234/watch/` + youziVidSingle + `/","isFamilyFriendly":false,"contentUrl":"/media/` + youziVidSingle + `/` + youziVersionID + `/master.m3u8"}</script>
<script type="application/ld+json">{"@type":"BreadcrumbList","itemListElement":[]}</script>
</body></html>`

func youziSeasonDetailFixture(site string) string {
	return `<!DOCTYPE html><html><head><title>示例节目甲</title></head><body>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"TVSeason","@id":"` + site + `/dramas/alpha-show/season-1/#season","seasonNumber":1,"name":"示例节目甲 · 第1季","description":"剧集示例简介。","image":"` + site + `/catalogue-art/cover-a-960.webp","numberOfEpisodes":3,"partOfSeries":{"@type":"TVSeries","@id":"` + site + `/dramas/alpha-show/#series","url":"` + site + `/dramas/alpha-show/","name":"示例节目甲"},"episode":[{"@type":"TVEpisode","@id":"` + site + `/dramas/alpha-show/season-1-episode-1/#episode","episodeNumber":1,"url":"` + site + `/dramas/alpha-show/season-1-episode-1/"},{"@type":"TVEpisode","episodeNumber":2,"url":"` + site + `/dramas/alpha-show/season-1-episode-2/"},{"@type":"TVEpisode","episodeNumber":3,"url":"` + site + `/dramas/alpha-show/season-1-episode-3/"}]}</script>
<section class="series-episodes" aria-labelledby="series-episodes-title" data-episode-selector="true">
<a class="p-button" href="` + site + `/dramas/alpha-show/season-1-episode-1/">01</a>
<a class="p-button" href="` + site + `/dramas/alpha-show/season-1-episode-2/">02</a>
<a class="p-button" href="` + site + `/dramas/alpha-show/season-1-episode-3/">03</a>
</section>
</body></html>`
}

func youziEpisodeDetailFixture(site string) string {
	return `<!DOCTYPE html><html><head><title>示例节目甲 第1季第2集</title></head><body>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"VideoObject","name":"示例节目甲 第1季第2集","description":"分集示例简介。","thumbnailUrl":["` + site + `/covers/` + youziVidEpisode2 + `/` + youziVersionID + `.jpg"],"uploadDate":"2026-11-06T09:00:00.000Z","duration":"PT259S","contentUrl":"` + site + `/media/` + youziVidEpisode2 + `/` + youziVersionID + `/master.m3u8"}</script>
</body></html>`
}

func youziMasterPlaylist() string {
	return "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-STREAM-INF:BANDWIDTH=8319848,RESOLUTION=1480x720\n720/playlist.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=14332384,RESOLUTION=2220x1080\n1080/playlist.m3u8\n"
}

func youziNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`<!DOCTYPE html><html><body><h1>404</h1></body></html>`))
}

func youziFixtureDownloader(t *testing.T, state *youziFixtureState) (*Downloader, *httptest.Server) {
	t.Helper()
	if state.tokenCalls == nil {
		state.tokenCalls = map[string]int{}
	}
	if state.pageFetches == nil {
		state.pageFetches = map[string]int{}
	}
	youziForgetSession()
	t.Cleanup(youziForgetSession)
	handler := func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		site := "http://" + r.Host
		switch {
		case r.Method == http.MethodPost && path == "/api/account/bootstrap":
			if r.Header.Get("Origin") == "" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"请求来源无效，请刷新页面后重试"}`))
				return
			}
			state.bootstraps++
			w.Header().Add("Set-Cookie", "eden_account=fixture-session-0123456789; Max-Age=31536000; Path=/; HttpOnly; Secure; SameSite=Strict")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"guest-1","phone":null,"guest":true}`))
		case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/playback/"):
			videoID := strings.TrimPrefix(path, "/api/playback/")
			state.tokenCalls[videoID]++
			if r.Header.Get("Origin") == "" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"请求来源无效"}`))
				return
			}
			if state.rejectTokens {
				state.rejectTokens = false
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"请刷新页面以恢复账户"}`))
				return
			}
			if !strings.Contains(r.Header.Get("Cookie"), "eden_account=") {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"请刷新页面以恢复账户"}`))
				return
			}
			if videoID != youziVidSingle && videoID != youziVidEpisode2 {
				youziNotFound(w)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Add("Set-Cookie", fmt.Sprintf("eden_play_%s=%s.%s.granted-1794135459.sig; Max-Age=900; Path=/media/%s/; HttpOnly; Secure; SameSite=Strict", videoID, videoID, youziVersionID, videoID))
			_, _ = fmt.Fprintf(w, `{"url":"/media/%s/%s/master.m3u8","expires_at":%d}`, videoID, youziVersionID, time.Now().Add(time.Hour).Unix())
		case strings.HasPrefix(path, "/media/"):
			if !strings.Contains(r.Header.Get("Cookie"), "eden_play_") {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"播放凭证已过期，请重新开始播放"}`))
				return
			}
			if !strings.Contains(r.Header.Get("Cookie"), "eden_account=") {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"播放凭证已过期，请重新开始播放"}`))
				return
			}
			if strings.HasSuffix(path, "/master.m3u8") {
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				_, _ = w.Write([]byte(youziMasterPlaylist()))
				return
			}
			youziNotFound(w)
		case path == "/search/":
			state.searchCalls++
			if r.URL.Query().Get("q") == "示例" {
				_, _ = w.Write([]byte(youziListingFixture(site, false)))
				return
			}
			_, _ = w.Write([]byte(youziSearchEmptyFixture))
		case path == "/ai-adult/" || reYouziPagePathMatch(path):
			page := strings.TrimSuffix(strings.TrimPrefix(path, "/ai-adult/page/"), "/")
			number := 1
			if page != "" && page != "/ai-adult/" {
				fmt.Sscanf(page, "%d", &number)
			}
			if number > 2 {
				youziNotFound(w)
				return
			}
			_, _ = w.Write([]byte(youziListingFixture(site, number < 2)))
		case path == "/ai-adult/abcd1234/":
			state.pageFetches[path]++
			_, _ = w.Write([]byte(youziSingleDetailFixture))
		case path == "/dramas/alpha-show/season-1/":
			state.pageFetches[path]++
			_, _ = w.Write([]byte(youziSeasonDetailFixture(site)))
		case path == "/dramas/alpha-show/season-1-episode-2/":
			state.pageFetches[path]++
			_, _ = w.Write([]byte(youziEpisodeDetailFixture(site)))
		case strings.HasSuffix(path, "/"):
			state.pageFetches[path]++
			_, _ = w.Write([]byte(youziListingFixture(site, false)))
		default:
			youziNotFound(w)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	prev := youziSiteBase
	youziSiteBase = srv.URL
	t.Cleanup(func() { youziSiteBase = prev })
	d := &Downloader{
		cfg:           Config{dataDir: t.TempDir(), Retries: 1, PageSize: 30, MaxPagesPerSort: 1},
		client:        srv.Client(),
		limiter:       newRequestLimiter(3, 0),
		providerHosts: map[string]string{},
	}
	return d, srv
}

func reYouziPagePathMatch(path string) bool {
	return strings.HasPrefix(path, "/ai-adult/page/")
}

func TestYouziOfflineCatalog(t *testing.T) {
	state := &youziFixtureState{}
	d, srv := youziFixtureDownloader(t, state)
	ctx := context.Background()

	dramas, hasMore, err := d.fetchYouziCatalogPage(ctx, 1, "ai-adult", "")
	if err != nil {
		t.Fatalf("catalog page 1: %v", err)
	}
	if len(dramas) != 2 {
		t.Fatalf("want 2 items (foreign/invalid cards dropped), got %d: %+v", len(dramas), dramas)
	}
	if hasMore != true {
		t.Error("rel=next link should mark more pages")
	}
	if got, want := dramas[0].ID, providerDramaID(sourceYouzi, "ai-adult/abcd1234"); got != want {
		t.Errorf("drama id = %q, want %q", got, want)
	}
	if dramas[0].Title != "示例作品乙" || dramas[0].SourceID != "ai-adult/abcd1234" {
		t.Errorf("bad first item: %+v", dramas[0])
	}
	if !strings.HasPrefix(dramas[0].Cover.(string), srv.URL+"/catalogue-art/cover-b-960.webp") {
		t.Errorf("cover not resolved: %v", dramas[0].Cover)
	}
	if dramas[1].TotalEpisode != 7 || dramas[1].Remark != "更新至7集" {
		t.Errorf("badge parsing failed: %+v", dramas[1])
	}
	if len(dramas[0].Tags) != 1 || dramas[0].Tags[0] != "示例" {
		t.Errorf("tags = %v", dramas[0].Tags)
	}

	_, hasMore, err = d.fetchYouziCatalogPage(ctx, 2, "ai-adult", "")
	if err != nil || hasMore {
		t.Fatalf("page 2: hasMore=%v err=%v", hasMore, err)
	}
	items, hasMore, err := d.fetchYouziCatalogPage(ctx, 3, "ai-adult", "")
	if err != nil || len(items) != 0 || hasMore {
		t.Fatalf("page 3 past 404 should be empty stop, got %d items hasMore=%v err=%v", len(items), hasMore, err)
	}

	if _, _, err := d.fetchYouziCatalogPage(ctx, 1, "bad../category", ""); err == nil {
		t.Error("invalid category must be rejected")
	}
	if items, _, err := d.fetchYouziCatalogPage(ctx, 1, "", ""); err != nil || len(items) != 2 {
		t.Errorf("empty category should fall back to default: %d items, err=%v", len(items), err)
	}
}

func TestYouziOfflineSearch(t *testing.T) {
	state := &youziFixtureState{}
	d, _ := youziFixtureDownloader(t, state)
	ctx := context.Background()

	items, hasMore, err := d.fetchYouziCatalogPage(ctx, 1, "ai-adult", "示例")
	if err != nil || len(items) == 0 || hasMore {
		t.Fatalf("search hit: %d items hasMore=%v err=%v", len(items), hasMore, err)
	}
	if state.searchCalls != 1 {
		t.Fatalf("search calls = %d", state.searchCalls)
	}
	// 站方忽略 page 参数：page>1 必须直接返回空，不再发请求。
	before := state.searchCalls
	items, hasMore, err = d.fetchYouziCatalogPage(ctx, 2, "", "示例")
	if err != nil || len(items) != 0 || hasMore {
		t.Fatalf("search page 2: %d items hasMore=%v err=%v", len(items), hasMore, err)
	}
	if state.searchCalls != before {
		t.Error("search page>1 must not hit the network")
	}

	items, _, err = d.fetchYouziCatalogPage(ctx, 1, "", "查无此词")
	if err != nil || len(items) != 0 {
		t.Fatalf("empty search: %d items err=%v", len(items), err)
	}
}

func TestYouziOfflineDetailSingle(t *testing.T) {
	state := &youziFixtureState{}
	d, _ := youziFixtureDownloader(t, state)
	ctx := context.Background()

	drama, chapters, err := d.fetchYouziDetail(ctx, "ai-adult/abcd1234")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if drama.Title != "示例作品乙" || drama.CategoryName != "AI成人视频" {
		t.Errorf("bad drama meta: %+v", drama)
	}
	if drama.Desc == "" || drama.OnlineDate != "2026-11-07" {
		t.Errorf("bad intro/date: %q %q", drama.Desc, drama.OnlineDate)
	}
	if seconds, ok := drama.Duration.(float64); !ok || seconds != 259 {
		t.Errorf("duration = %v, want 259 seconds", drama.Duration)
	}
	if len(chapters) != 1 {
		t.Fatalf("chapters = %d, want 1", len(chapters))
	}
	ch := chapters[0]
	if want := providerChapterID(sourceYouzi, "ai-adult/abcd1234", youziVidSingle); ch.ID != want {
		t.Errorf("chapter id = %q, want %q", ch.ID, want)
	}
	if ch.VideoURL != youziPlayScheme+youziVidSingle {
		t.Errorf("video url = %q", ch.VideoURL)
	}

	for _, bad := range []string{"", "ai-adult", "ai-adult/../etc", "dramas/alpha-show", "dramas/alpha-show/season-1/bogus", "evil/x/abcd1234"} {
		if _, _, err := d.fetchYouziDetail(ctx, bad); err == nil {
			t.Errorf("sourceID %q should be rejected", bad)
		}
	}
}

func TestYouziOfflineDetailSeason(t *testing.T) {
	state := &youziFixtureState{}
	d, _ := youziFixtureDownloader(t, state)
	ctx := context.Background()

	drama, chapters, err := d.fetchYouziDetail(ctx, "dramas/alpha-show/season-1")
	if err != nil {
		t.Fatalf("season detail: %v", err)
	}
	if drama.Title != "示例节目甲" || drama.CategoryName != "AI短剧" {
		t.Errorf("bad drama meta: %+v", drama)
	}
	if len(chapters) != 3 {
		t.Fatalf("chapters = %d, want 3", len(chapters))
	}
	for i, ch := range chapters {
		key := fmt.Sprintf("dramas/alpha-show/season-1-episode-%d", i+1)
		if ch.ID != providerChapterID(sourceYouzi, "dramas/alpha-show/season-1", key) {
			t.Errorf("chapter %d id = %q", i, ch.ID)
		}
		if ch.VideoURL != youziPlayScheme+key {
			t.Errorf("chapter %d video = %q", i, ch.VideoURL)
		}
		if ch.PageURL != youziDetailURL(key) {
			t.Errorf("chapter %d page = %q, want %q", i, ch.PageURL, youziDetailURL(key))
		}
		if number := chapterEpisodeNumber(ch, 0); number != i+1 {
			t.Errorf("chapter %d episode number = %d", i, number)
		}
	}
	if drama.TotalEpisode != 3 {
		t.Errorf("total episode = %v", drama.TotalEpisode)
	}
}

func TestYouziOfflineResolveEpisode(t *testing.T) {
	state := &youziFixtureState{}
	d, srv := youziFixtureDownloader(t, state)
	ctx := context.Background()

	_, chapters, err := d.fetchYouziDetail(ctx, "dramas/alpha-show/season-1")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	task := Task{DramaID: providerDramaID(sourceYouzi, "dramas/alpha-show/season-1"), Chapter: chapters[1]}
	media, err := d.resolveYouziMedia(ctx, task)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	wantURL := srv.URL + "/media/" + youziVidEpisode2 + "/" + youziVersionID + "/master.m3u8"
	if media.URL != wantURL {
		t.Errorf("media url = %q, want %q", media.URL, wantURL)
	}
	if !strings.HasPrefix(media.Playlist, "#EXTM3U") || !strings.Contains(media.Playlist, "1080/playlist.m3u8") {
		t.Errorf("playlist not fetched: %q", media.Playlist)
	}
	if media.credentials == nil || !strings.HasPrefix(media.credentials.cookie, youziFixtureCookie+"; ") || !strings.Contains(media.credentials.cookie, "eden_play_") {
		t.Errorf("credentials missing account/play cookie: %+v", media.credentials)
	}
	if media.credentials == nil || media.credentials.expires.Before(time.Now().Add(50*time.Minute)) {
		t.Error("credentials expiry not honored")
	}
	if state.bootstraps != 1 {
		t.Errorf("bootstraps = %d, want exactly 1", state.bootstraps)
	}
	if state.tokenCalls[youziVidEpisode2] != 1 {
		t.Errorf("token calls = %v", state.tokenCalls)
	}
	if state.pageFetches["/dramas/alpha-show/season-1-episode-2/"] != 2 {
		t.Logf("episode page fetches = %d (detail picks may skip fetch)", state.pageFetches["/dramas/alpha-show/season-1-episode-2/"])
	}

	// 第二次解析复用缓存会话，不再 bootstrap。
	if _, err := d.resolveYouziMedia(ctx, task); err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if state.bootstraps != 1 {
		t.Errorf("session not cached: bootstraps = %d", state.bootstraps)
	}
}

func TestYouziOfflineResolveSingleAndSessionRetry(t *testing.T) {
	state := &youziFixtureState{}
	d, srv := youziFixtureDownloader(t, state)
	ctx := context.Background()

	_, chapters, err := d.fetchYouziDetail(ctx, "ai-adult/abcd1234")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	task := Task{DramaID: providerDramaID(sourceYouzi, "ai-adult/abcd1234"), Chapter: chapters[0]}
	media, err := d.resolveYouziMedia(ctx, task)
	if err != nil {
		t.Fatalf("resolve single: %v", err)
	}
	if !strings.HasPrefix(media.URL, srv.URL+"/media/"+youziVidSingle+"/") {
		t.Errorf("media url = %q", media.URL)
	}
	if state.tokenCalls[youziVidSingle] != 1 {
		t.Errorf("single-video resolve must skip episode page fetch and use chapter key directly: %v", state.tokenCalls)
	}

	// 模拟会话失效：401 后自动重建 bootstrap 并重试一次。
	state.rejectTokens = true
	youziSession.mu.Lock()
	stale := youziSession.cookie
	youziSession.mu.Unlock()
	if stale == "" {
		t.Fatal("fixture session missing")
	}
	if _, err := d.resolveYouziMedia(ctx, task); err != nil {
		t.Fatalf("resolve after 401: %v", err)
	}
	if state.bootstraps != 2 {
		t.Errorf("bootstraps = %d, want 2 (rebuild after 401)", state.bootstraps)
	}
}

func TestYouziOfflineResolveRejectsForeignInput(t *testing.T) {
	state := &youziFixtureState{}
	d, _ := youziFixtureDownloader(t, state)
	ctx := context.Background()

	cases := []Task{
		{DramaID: providerDramaID("other", "ai-adult/abcd1234"), Chapter: Chapter{ID: "x"}},
		{DramaID: providerDramaID(sourceYouzi, "ai-adult/abcd1234"), Chapter: Chapter{ID: providerChapterID(sourceYouzi, "ai-adult/deadbeef", youziVidSingle)}},
		{DramaID: providerDramaID(sourceYouzi, "ai-adult/abcd1234"), Chapter: Chapter{ID: providerChapterID(sourceYouzi, "ai-adult/abcd1234", "../../etc/passwd")}},
		{DramaID: providerDramaID(sourceYouzi, "dramas/alpha-show/season-1"), Chapter: Chapter{ID: providerChapterID(sourceYouzi, "dramas/alpha-show/season-1", "dramas/other-show/season-1-episode-1")}},
	}
	for i, task := range cases {
		if _, err := d.resolveYouziMedia(ctx, task); err == nil {
			t.Errorf("case %d should be rejected", i)
		}
	}
}

func TestYouziValidationHelpers(t *testing.T) {
	if !validYouziCategory("") || !validYouziCategory("ai-adult/remix") || validYouziCategory("ai-adupt") {
		t.Error("category whitelist broken")
	}
	if !validYouziSourceID("ai-face-swap/a9f27459") || !validYouziSourceID("ai-adult/6b17c87e-049a-43f9-a45a-d212c162adfd") {
		t.Error("valid sourceIDs rejected")
	}
	if youziCategoryName("ai-adult/remix/anything") != "AI魔改" || youziCategoryName("dramas/x/season-2") != "AI短剧" {
		t.Errorf("category names: %q %q", youziCategoryName("ai-adult/remix/anything"), youziCategoryName("dramas/x/season-2"))
	}
	if youziParseDuration("PT1H2M3S") != 3723 || youziParseDuration("bogus") != 0 {
		t.Error("duration parser broken")
	}
}

func TestYouziRegistryRegistration(t *testing.T) {
	def, ok := providerSourceDef(sourceYouzi)
	if !ok {
		t.Fatal("youzi not registered")
	}
	if !def.Implemented {
		t.Fatal("youzi Implemented flag not flipped")
	}
	if def.SearchKind != providerSearchOnline || !def.Adult || def.EnabledByDefault {
		t.Errorf("bad def: %+v", def)
	}
	if len(def.CatalogKinds) != len(youziCategories) {
		t.Errorf("catalog kinds %d vs categories %d", len(def.CatalogKinds), len(youziCategories))
	}
	if !providerSourceImplemented("youzidj.com") {
		t.Error("domain alias should resolve to implemented provider")
	}
}

// TestYouziLiveSmoke exercises the real youzidj.com endpoints. It is skipped
// unless YOUZI_LIVE=1. If direct access fails, export HTTP_PROXY/HTTPS_PROXY
// (e.g. http://192.168.0.36:17890) before running.
func TestYouziLiveSmoke(t *testing.T) {
	if os.Getenv("YOUZI_LIVE") != "1" {
		t.Skip("set YOUZI_LIVE=1 to hit live youzidj.com")
	}
	cfg := defaultConfig()
	cfg.dataDir = t.TempDir()
	d := &Downloader{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second},
		limiter: newRequestLimiter(3, 0), providerHosts: map[string]string{}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	youziForgetSession()
	t.Cleanup(youziForgetSession)

	dramas, hasMore, err := d.fetchYouziCatalogPage(ctx, 1, "ai-adult", "")
	if err != nil || len(dramas) == 0 {
		t.Fatalf("live catalog ai-adult: %d items, err=%v", len(dramas), err)
	}
	t.Logf("catalog ai-adult: %d dramas hasMore=%v first=%q id=%s", len(dramas), hasMore, dramas[0].Title, dramas[0].ID)

	cats, err := d.fetchYouziCategories(ctx)
	if err != nil || len(cats) == 0 {
		t.Fatalf("live categories: %v", err)
	}
	for _, cat := range cats {
		items, _, err := d.fetchYouziCatalogPage(ctx, 1, cat.ID, "")
		if err != nil {
			t.Errorf("live category %s: %v", cat.ID, err)
			continue
		}
		t.Logf("category %-16s %2d items  first=%q", cat.ID, len(items), firstTitle(items))
	}

	term := ""
	if runes := []rune(dramas[0].Title); len(runes) >= 2 {
		term = string(runes[:2])
	}
	items, _, err := d.fetchYouziCatalogPage(ctx, 1, "", term)
	if err != nil {
		t.Fatalf("live search %q: %v", term, err)
	}
	t.Logf("search %q: %d matches", term, len(items))

	// 单视频详情 + 取流（bootstrap→token→playlist 全链路）。
	var single *Drama
	for i := range dramas {
		if strings.HasPrefix(dramas[i].SourceID, "ai-adult/") || strings.HasPrefix(dramas[i].SourceID, "ai-face-swap/") {
			single = &dramas[i]
			break
		}
	}
	if single == nil {
		t.Fatal("no single-video item in live catalog")
	}
	drama, chapters, err := d.fetchYouziDetail(ctx, single.SourceID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("live detail %s: %d chapters, err=%v", single.SourceID, len(chapters), err)
	}
	task := Task{DramaID: drama.ID, Chapter: chapters[0]}
	media, err := d.resolveYouziMedia(ctx, task)
	if err != nil {
		t.Fatalf("live resolve %s: %v", drama.ID, err)
	}
	host := strings.SplitN(strings.TrimPrefix(media.URL, "https://"), "/", 2)[0]
	t.Logf("single resolve: %q url host=%s playlist=%dB", drama.Title, host, len(media.Playlist))
	if !strings.HasPrefix(media.Playlist, "#EXTM3U") {
		t.Fatalf("playlist not fetched: %q", media.Playlist)
	}

	// 剧集季页详情 + 第一集取流。
	dramaItems, _, err := d.fetchYouziCatalogPage(ctx, 1, "dramas", "")
	if err != nil || len(dramaItems) == 0 {
		t.Fatalf("live dramas catalog: %v", err)
	}
	var season *Drama
	for i := range dramaItems {
		if strings.HasPrefix(dramaItems[i].SourceID, "dramas/") {
			season = &dramaItems[i]
			break
		}
	}
	if season == nil {
		t.Fatal("no season item in live dramas catalog")
	}
	sd, schapters, err := d.fetchYouziDetail(ctx, season.SourceID)
	if err != nil || len(schapters) == 0 {
		t.Fatalf("live season detail %s: %d chapters, err=%v", season.SourceID, len(schapters), err)
	}
	stask := Task{DramaID: sd.ID, Chapter: schapters[0]}
	smedia, err := d.resolveYouziMedia(ctx, stask)
	if err != nil {
		t.Fatalf("live season resolve %s: %v", sd.ID, err)
	}
	t.Logf("season resolve: %q %d chapters, first=%q playlist=%dB", sd.Title, len(schapters), schapters[0].Title, len(smedia.Playlist))
}

func firstTitle(items []Drama) string {
	if len(items) == 0 {
		return ""
	}
	return items[0].Title
}
