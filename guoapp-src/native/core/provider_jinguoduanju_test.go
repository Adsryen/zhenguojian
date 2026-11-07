package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJinguoDuanjuRegistryAndAliases(t *testing.T) {
	def, found := providerSourceDef(sourceJinguoDuanju)
	if !found || !def.Implemented || def.DisplayName != "禁果短剧" || def.SearchKind != providerSearchOnline || !def.EnabledByDefault {
		t.Fatalf("registry entry not flipped to implemented: %+v found=%t", def, found)
	}
	for _, alias := range []string{"jinguoduanju", "jinguoduanju.app", "jgdj.app", "play.jinguoduanju.app"} {
		if got := canonicalProviderSource(alias); got != sourceJinguoDuanju {
			t.Fatalf("jinguoduanju alias was not canonicalized: %s -> %s", alias, got)
		}
	}
	if got := canonicalProviderSource("9fu8e.com"); got == sourceJinguoDuanju {
		t.Fatal("the unrelated landing host was mapped to 禁果短剧")
	}
	if err := nativeSourceGateError(sourceJinguoDuanju); err != nil {
		t.Fatalf("implemented source is still gated: %v", err)
	}
	if !validJinguoDuanjuCategory("home") || !validJinguoDuanjuCategory("heat") || !validJinguoDuanjuCategory("tag:22") || !validJinguoDuanjuCategory("") {
		t.Fatal("jinguoduanju category validation changed")
	}
	if validJinguoDuanjuCategory("tag:古装") || validJinguoDuanjuCategory("nav:") || validJinguoDuanjuCategory("bad:1") {
		t.Fatal("jinguoduanju accepted a malformed category")
	}
}

func TestJinguoDuanjuDramaParsing(t *testing.T) {
	// Wire payload: numbers arrive via json.Number, mirrors UseNumber decoding.
	decoder := json.NewDecoder(strings.NewReader(`{"id":2137,"title":"禁果短剧样本","episodeCount":12,"coverUrl":"/media/covers/sample.png","heat":8,"publishedAt":"2026-09-23T10:20:30+08:00","filmType":"FREE","tags":["都市","职场","都市","","超长标签超长标签超长标签超长标签超长标签超长标签超长标签超长标签超长标签超长标签超长标签超长标签超长标签超长标签超长标签"]}`))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("sample row did not decode: %v", err)
	}
	row, _ := value.(map[string]any)
	drama, err := jinguoDuanjuDramaFromMap(row, jinguoDuanjuBaseURL, "")
	if err != nil || drama.ID != "jinguoduanju:2137" || drama.SourceID != "2137" || drama.EpisodeCount != 12 ||
		drama.ChannelName != "禁果短剧" || drama.CategoryName != "都市" || drama.Heat != "8" || drama.OnlineDate != "2026-09-23" ||
		drama.VIP == nil || *drama.VIP || len(drama.Tags) != 2 || !strings.HasPrefix(nativeText(drama.Cover), jinguoDuanjuBaseURL+"/media/covers/") {
		t.Fatalf("jinguoduanju row parsed incorrectly: %+v err=%v", drama, err)
	}
	if _, err := jinguoDuanjuDramaFromMap(row, jinguoDuanjuBaseURL, "2138"); err == nil {
		t.Fatal("jinguoduanju accepted a mismatched source id")
	}
	if _, err := jinguoDuanjuDramaFromMap(map[string]any{"id": "2137", "title": "  "}, jinguoDuanjuBaseURL, ""); err == nil {
		t.Fatal("jinguoduanju accepted a titleless row")
	}
	vipRow := map[string]any{"id": "5", "title": "x", "filmType": "VIP"}
	parsed, err := jinguoDuanjuDramaFromMap(vipRow, jinguoDuanjuBaseURL, "")
	if err != nil || parsed.VIP == nil || !*parsed.VIP {
		t.Fatalf("VIP filmType was not flagged: %+v err=%v", parsed, err)
	}
	plain, err := jinguoDuanjuDramaFromMap(map[string]any{"id": "6", "title": "y"}, jinguoDuanjuBaseURL, "")
	if err != nil || plain.VIP != nil {
		t.Fatalf("missing filmType should stay unknown: %+v err=%v", plain, err)
	}
	if wrapped, err := jinguoDuanjuDramaRows(map[string]any{"list": []any{row}}); err != nil || len(wrapped) != 1 {
		t.Fatalf("object-wrapped listing rows not accepted: %v err=%v", wrapped, err)
	}
	if _, err := jinguoDuanjuDramaRows("nope"); err == nil {
		t.Fatal("garbage listing payload was accepted")
	}
	items := []Drama{{ID: "a"}, {ID: "b"}}
	if page, more, err := jinguoDuanjuPage(items, 1); err != nil || more || len(page) != 2 {
		t.Fatalf("short page was split incorrectly: %+v more=%t err=%v", page, more, err)
	}
	if page, more, err := jinguoDuanjuPage(items, 2); err != nil || more || len(page) != 0 {
		t.Fatalf("past-end page should be empty: %+v more=%t err=%v", page, more, err)
	}
	if _, _, err := jinguoDuanjuPage(items, 0); err == nil || strings.Contains(jinguoDuanjuPageErrorText(err), "panic") {
		t.Fatalf("invalid page number did not error: %v", err)
	}
	if _, err := jinguoDuanjuCatalogQuery("tag:古装", ""); err == nil {
		t.Fatal("invalid category slipped through the query builder")
	}
	values, err := jinguoDuanjuCatalogQuery("heat", "")
	if err != nil || values.Get("sort") != "heat" {
		t.Fatalf("heat category query changed: %v err=%v", values, err)
	}
	values, err = jinguoDuanjuCatalogQuery("home", "")
	if err != nil || values.Get("displayArea") != "HOME_FEED" {
		t.Fatalf("home feed area missing: %v err=%v", values, err)
	}
	values, err = jinguoDuanjuCatalogQuery("nav:3", "  样本  ")
	if err != nil || values.Get("q") != "样本" || values.Has("navigationId") {
		t.Fatalf("search should override the category: %v err=%v", values, err)
	}
	if _, err := jinguoDuanjuCatalogQuery("", strings.Repeat("字", 257)); err == nil {
		t.Fatal("oversized search query was accepted")
	}
}

func jinguoDuanjuPageErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestJinguoDuanjuCatalogDetailAndPlaybackDoNotUnlock(t *testing.T) {
	var sawUnlock bool
	var playAuthorization string
	var authRequests, catalogRequests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.Path, "/unlock") || strings.Contains(request.URL.Path, "/payments") {
			sawUnlock = true
			http.Error(response, "unlock should not be called", http.StatusForbidden)
			return
		}
		switch request.URL.Path {
		case "/api/v1/auth/device":
			authRequests++
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), `"deviceId":"h5:`) {
				t.Errorf("device auth body changed: %s", body)
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"token":"guest-token","user":{"id":1}}`)
		case "/api/v1/top-navigation":
			_, _ = io.WriteString(response, `[{"id":1,"frontendName":"标签系统","targetType":"SYSTEM","active":true,"valid":true},{"id":2,"frontendName":"都市","targetType":"CATEGORIES","active":true,"valid":true},{"id":2,"frontendName":"重复","targetType":"CATEGORIES","active":true,"valid":true},{"id":9,"frontendName":"停用","targetType":"CATEGORIES","active":false,"valid":true}]`)
		case "/api/v1/filter-config":
			_, _ = io.WriteString(response, `[{"id":9,"active":true,"frontendVisible":true,"tags":[{"id":22,"name":"古装","active":true,"frontendVisible":true}]},{"id":10,"active":false,"frontendVisible":true,"tags":[{"id":23,"name":"隐藏","active":true,"frontendVisible":true}]}]`)
		case "/api/v1/dramas":
			catalogRequests++
			if request.Header.Get("Authorization") != "Bearer guest-token" {
				t.Errorf("catalog missing authorization: %s", request.Header.Get("Authorization"))
			}
			if q := request.URL.Query().Get("q"); q != "" && q != "样本" {
				t.Errorf("unexpected search query: %s", request.URL.RawQuery)
			}
			response.Header().Set("Content-Type", "application/json")
			if request.URL.Query().Get("q") != "" {
				_, _ = io.WriteString(response, `[{"id":2137,"title":"禁果短剧样本","episodeCount":2,"coverUrl":"/media/covers/sample.png","filmType":"FREE","tags":["都市"]},{"id":2138,"title":"另一部","episodeCount":1,"filmType":"VIP","tags":["古装"]},{"id":2137,"title":"重复行"},{"id":2139,"title":""}]`)
				return
			}
			_, _ = io.WriteString(response, `[{"id":2137,"title":"禁果短剧样本","episodeCount":2,"coverUrl":"/media/covers/sample.png","filmType":"FREE","tags":["都市"]},{"id":2138,"title":"另一部","episodeCount":1,"filmType":"VIP","tags":["古装"]}]`)
		case "/api/v1/dramas/2137":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"drama":{"id":2137,"title":"禁果短剧样本","episodeCount":2,"filmType":"FREE","tags":["都市"]},"description":"简介","episodes":[{"id":5671,"number":1,"title":"第1集","locked":false},{"id":5672,"number":2,"title":"第2集","locked":true}]}`)
		case "/api/v1/dramas/2138":
			// detail asked for 2138 but the API answers with the 2137 row:
			// the sourceID guard must reject the mismatch.
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"drama":{"id":2137,"title":"错配行","episodeCount":1,"filmType":"FREE"},"episodes":[]}`)
		case "/api/v1/episodes/5671/play":
			if request.Header.Get("Authorization") != "Bearer guest-token" {
				t.Errorf("play request missing authorization: %s", request.Header.Get("Authorization"))
			}
			playAuthorization = request.Header.Get("Authorization")
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"episodeId":5671,"videoUrl":"`+jinguoDuanjuServerURL(request)+`/media/index.m3u8","allowance":{"allowed":true}}`)
		case "/api/v1/episodes/5672/play":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"episodeId":5672,"videoUrl":"","allowance":{"allowed":false}}`)
		case "/media/index.m3u8":
			if request.Header.Get("Referer") == "" {
				t.Error("playlist request missing referer")
			}
			_, _ = io.WriteString(response, "#EXTM3U\n#EXTINF:1.0,\nsegment.ts\n")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	d := &Downloader{cfg: Config{dataDir: t.TempDir(), Retries: 1, PageSize: 30, MaxPagesPerSort: 1}, client: server.Client(), limiter: newRequestLimiter(3, 0), providerHosts: map[string]string{}}
	client := d.jinguoDuanjuClient()
	if client != d.jinguoDuanjuClient() {
		t.Fatal("jinguoduanju client is not stable per downloader")
	}
	client.base = server.URL + "/api/v1"
	client.site = server.URL

	items, more, err := d.fetchJinguoDuanjuCatalogPage(context.Background(), 1, "", "")
	if err != nil || more || len(items) != 2 || items[0].ID != "jinguoduanju:2137" || items[1].VIP == nil || !*items[1].VIP {
		t.Fatalf("catalog page parsed incorrectly: %+v more=%t err=%v", items, more, err)
	}
	if authRequests != 1 {
		t.Fatalf("guest device auth was not fetched once: %d", authRequests)
	}
	if rest, more, err := d.fetchJinguoDuanjuCatalogPage(context.Background(), 2, "", ""); err != nil || more || len(rest) != 0 || catalogRequests != 1 {
		t.Fatalf("second page did not reuse the cached listing: %d more=%t err=%v requests=%d", len(rest), more, err, catalogRequests)
	}
	if page, more, err := d.fetchJinguoDuanjuCatalogPage(context.Background(), 1, "home", ""); err != nil || more || len(page) != 2 || catalogRequests != 2 {
		t.Fatalf("category listing should refetch: %d more=%t requests=%d err=%v", len(page), more, catalogRequests, err)
	}
	categories, err := d.fetchJinguoDuanjuCategories(context.Background())
	if err != nil || len(categories) != 4 || categories[2].ID != "nav:2" || categories[2].Name != "都市" || categories[3].ID != "tag:22" {
		t.Fatalf("categories parsed incorrectly: %+v err=%v", categories, err)
	}
	found, more, err := d.fetchJinguoDuanjuCatalogPage(context.Background(), 1, "", "样本")
	if err != nil || more || len(found) != 2 {
		t.Fatalf("search page parsed incorrectly: %d more=%t err=%v", len(found), more, err)
	}
	drama, chapters, err := d.fetchJinguoDuanjuDetail(context.Background(), "2137")
	if err != nil || drama.Desc != "简介" || len(chapters) != 2 || !chapters[1].VIP || chapters[0].VideoURL != "jinguoduanju-play://5671" {
		t.Fatalf("detail parsed incorrectly: %+v chapters=%+v err=%v", drama, chapters, err)
	}
	if _, _, err := d.fetchJinguoDuanjuDetail(context.Background(), "2138"); err == nil || !strings.Contains(err.Error(), "禁果短剧详情与请求剧集不符") {
		t.Fatalf("mismatched detail row was accepted: %v", err)
	}
	media, err := d.resolveJinguoDuanjuMedia(context.Background(), Task{DramaID: drama.ID, Chapter: chapters[0], Index: 1})
	if err != nil || !strings.Contains(media.Playlist, "#EXTM3U") || playAuthorization != "Bearer guest-token" {
		t.Fatalf("playback was not resolved: %+v auth=%s err=%v", media, playAuthorization, err)
	}
	if _, err := d.resolveJinguoDuanjuMedia(context.Background(), Task{DramaID: drama.ID, Chapter: chapters[1], Index: 2}); err == nil || !strings.Contains(err.Error(), "会员或解锁") || sawUnlock {
		t.Fatalf("locked episode was unlocked or accepted: unlock=%t err=%v", sawUnlock, err)
	}
	if _, err := d.resolveJinguoDuanjuMedia(context.Background(), Task{DramaID: "jinguoduanju:2137", Chapter: Chapter{ID: "huangju:2137:5671"}, Index: 1}); err == nil || !strings.Contains(err.Error(), "分集信息无效") {
		t.Fatalf("foreign chapter was accepted: %v", err)
	}
}

func TestJinguoDuanjuAuthRecoveryAndRotation(t *testing.T) {
	var staleTokens int
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/auth/device":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"token":"fresh-token"}`)
		case "/api/v1/dramas/2137":
			if request.Header.Get("Authorization") == "Bearer stale-token" {
				staleTokens++
				response.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(response, `{"status":401,"message":"需要登录"}`)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"drama":{"id":"2137","title":"样本"},"episodes":[]}`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	d := &Downloader{cfg: Config{dataDir: t.TempDir()}, client: server.Client(), limiter: newRequestLimiter(3, 0), providerHosts: map[string]string{}}
	client := d.jinguoDuanjuClient()
	client.base = server.URL + "/api/v1"
	client.site = server.URL
	client.token = "stale-token" // simulate an expired guest session
	drama, _, err := d.fetchJinguoDuanjuDetail(context.Background(), "2137")
	if err != nil || drama.ID != "jinguoduanju:2137" {
		t.Fatalf("stale token was not recovered: %+v err=%v", drama, err)
	}
	if staleTokens != 1 || client.token != "fresh-token" {
		t.Fatalf("auth refresh did not happen once: stale=%d token=%s", staleTokens, client.token)
	}

	// Status errors on the pinned base must stop candidate rotation, and
	// credential-bearing bases must be rejected before any dial: tests can
	// never fall through to the hardcoded production hosts.
	broken := &Downloader{cfg: Config{dataDir: t.TempDir()}, client: server.Client(), limiter: newRequestLimiter(3, 0), providerHosts: map[string]string{}}
	brokenClient := broken.jinguoDuanjuClient()
	brokenClient.base = server.URL + "/api/v1"
	brokenClient.token = "any"
	if _, err := brokenClient.get(context.Background(), "/dramas/4040", nil, 1<<20); err == nil || !jinguoDuanjuStatus(err) {
		t.Fatalf("status error was not surfaced as a stop-rotating failure: %v", err)
	}
	if _, err := brokenClient.requestBase(context.Background(), "https://user:pass@evil.test/api/v1", http.MethodGet, "/dramas/2137", nil, nil, "", false, 1<<20); err == nil || !strings.Contains(err.Error(), "接口地址无效") {
		t.Fatalf("credential-bearing base was accepted: %v", err)
	}
	if _, _, err := broken.fetchJinguoDuanjuDetail(context.Background(), "not-a-number"); err == nil {
		t.Fatal("invalid drama id reached the network")
	}
}

func jinguoDuanjuServerURL(request *http.Request) string {
	return "https://" + request.Host
}
