package core

// Offline httptest suite for the 韩小圈 provider, including a faithful
// simulation of the search.php cookie gate measured on the live site
// 2026-11-07: 200 + `x-idc-search-verify: 1` + ~700-byte JS challenge for
// unverified clients, 401 for a malformed _idc_pass once the cookie is
// present, full results page for cookie + plausible base36 timestamp pass.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	hxqFixtureToken   = "e58db764d6c1adbec36733f7ba8f39db"
	hxqChallengeBytes = `<!DOCTYPE html><html><head><meta charset="utf-8"><title>请稍候 - 安全验证</title></head><body><div id="c">正在验证您的浏览器，请稍候…</div></body><script>document.cookie="idc_search_browser=` + hxqFixtureToken + `; Max-Age=1800; Path=/; SameSite=Lax; Secure";var u=new URL(location.href);u.searchParams.set("_idc_pass",Date.now().toString(36)+Math.random().toString(36).slice(2,8));setTimeout(function(){location.replace(u.href)},300);</script></html>`
)

func hanxiaoquanFixtureDownloader(t *testing.T, handler http.HandlerFunc) (*Downloader, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	prev := hanxiaoquanSiteBase
	hanxiaoquanSiteBase = srv.URL
	hanxiaoquanForgetToken()
	t.Cleanup(func() {
		hanxiaoquanSiteBase = prev
		hanxiaoquanForgetToken()
	})
	d := &Downloader{
		cfg:           Config{dataDir: t.TempDir(), Retries: 1, PageSize: 30, MaxPagesPerSort: 1},
		client:        srv.Client(),
		limiter:       newRequestLimiter(3, 0),
		providerHosts: map[string]string{},
	}
	return d, srv
}

type hxqGateServer struct {
	mu              sync.Mutex
	challenges      int
	results         int
	rejectedPasses  int
	requireGatePass bool
}

func (g *hxqGateServer) handler(resultsPage string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hxq/1.html", "/hxq/1-2.html":
			w.Write([]byte(hxqListFixture))
		case "/hanxiaoquan/4544.html":
			w.Write([]byte(hxqDetailFixture))
		case "/play/4544-2-0.html", "/play/4544-2-1.html", "/play/4544-2-2.html", "/play/4544-1-0.html", "/play/4544-1-1.html":
			w.Write([]byte(hxqPlayFixture(r.Host)))
		case "/media/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Write([]byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:9.009,\nseg0.ts\n#EXT-X-ENDLIST\n"))
		case "/search.php":
			g.serveSearch(w, r, resultsPage)
		default:
			http.NotFound(w, r)
		}
	}
}

func (g *hxqGateServer) serveSearch(w http.ResponseWriter, r *http.Request, resultsPage string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	cookie := r.Header.Get("Cookie")
	pass := r.URL.Query().Get("_idc_pass")
	if cookie != "idc_search_browser="+hxqFixtureToken {
		g.challenges++
		w.Header().Set("x-idc-search-verify", "1")
		w.Write([]byte(hxqChallengeBytes))
		return
	}
	// Emulate the live server's lax _idc_pass check: some prefix of the pass
	// must decode as a plausible epoch-ms. (Junk passes get HTTP 401 live.)
	plausible := false
	for n := 12; n >= 8 && !plausible; n-- {
		if len(pass) < n {
			continue
		}
		if v, err := strconv.ParseInt(pass[:n], 36, 64); err == nil && v >= 1_600_000_000_000 && v <= 3_600_000_000_000 {
			plausible = true
		}
	}
	if !plausible {
		g.rejectedPasses++
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("<html><head><title>401 Authorization Required</title></head><body>Authorization Required</body></html>"))
		return
	}
	g.results++
	w.Write([]byte(resultsPage))
}

func (g *hxqGateServer) counts() (int, int, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.challenges, g.results, g.rejectedPasses
}

const hxqListFixture = `<!DOCTYPE html><html><body>
<div class="module-page">
<div class="module"><div class="module-main">
<a href="/hanxiaoquan/4544.html" title="婚姻之后" class="module-item-pic"><img data-src="/uploads/cover/4544.jpg" alt=""></a>
<a href="/hanxiaoquan/4544.html" class="module-item-title">婚姻之后</a>
<span class="module-item-text">更新至06集</span>
</div></div>
<div class="module"><div class="module-main">
<a href="/hanxiaoquan/4500.html" title=" snowy 雪夜谭 " class="module-item-pic"><img data-src="https://pic3.yzzyimg.online/cover/4500.jpg" alt=""></a>
<a href="/hanxiaoquan/4500.html" class="module-item-title">雪夜谭</a>
<span class="module-item-text">HD</span>
</div></div>
</div>
<div class="page"><a href="/hxq/1-2.html">下一页</a></div>
</body></html>`

const hxqDetailFixture = `<!DOCTYPE html><html><head>
<meta property="og:image" content="/uploads/cover/4544.jpg">
</head><body>
<h1 class="page-title">婚姻之后</h1>
<div class="video-info"><a class="video-title" href="#">婚姻之后</a>
<div><span class="video-info-itemtitle">导演：</span><div class="video-info-item video-info-actor"><a>金某</a></div></div>
<div><span class="video-info-itemtitle">主演：</span><div class="video-info-item video-info-actor"><a>甲</a><a>乙</a></div></div>
<div><span class="video-info-itemtitle">上映：</span><div class="video-info-item">2024</div></div>
<div><span class="video-info-itemtitle">更新：</span><div class="video-info-item">2026-07-04</div></div>
<div><span class="video-info-itemtitle">剧情：</span><div class="video-info-item video-info-content">已婚女子的秘密生活@www.ad-site.example@全集无删减免费在线观看，口碑爆棚。</div></div>
</div>
<div class="nav-tabs"><ul>
<li><a href="#playlist2" data-toggle="tab"><i class="icon-play"></i> 云播资源</a></li>
<li><a href="#playlist1" data-toggle="tab"><i class="icon-play"></i> 快播资源</a></li>
</ul></div>
<div id="playlist2" class="tab-pane active"><ul class="play-list">
<li><a title="第05集" href="/play/4544-2-4.html">05</a></li>
<li><a title="第01集" href="/play/4544-2-0.html">01</a></li>
<li><a title="第02集" href="/play/4544-2-1.html">02</a></li>
<li><a title="第03集" href="/play/4544-2-2.html">03</a></li>
</ul></div>
<div id="playlist1" class="tab-pane"><ul class="play-list">
<li><a title="第01集" href="/play/4544-1-0.html">01</a></li>
<li><a title="第02集" href="/play/4544-1-1.html">02</a></li>
</ul></div>
</body></html>`

func hxqPlayFixture(host string) string {
	return `<html><body><script type="text/javascript">var now="http://` + host + `/media/index.m3u8";</script></body></html>`
}

const hxqSearchResults = `<!DOCTYPE html><html><body>
<div class="module"><a href="/hanxiaoquan/4544.html" title="婚姻之后" class="module-item-pic"><img data-src="/uploads/cover/4544.jpg"></a><a href="/hanxiaoquan/4544.html" class="module-item-title">婚姻之后</a></div>
<div class="module"><a href="/hanxiaoquan/4520.html" title="雪人" class="module-item-pic"><img data-src="/uploads/cover/4520.jpg"></a><a href="/hanxiaoquan/4520.html" class="module-item-title">雪人</a></div>
</body></html>`

func TestHanxiaoquanOfflineCatalog(t *testing.T) {
	gate := &hxqGateServer{}
	d, _ := hanxiaoquanFixtureDownloader(t, gate.handler(hxqSearchResults))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	items, hasMore, err := d.fetchHanxiaoquanCatalogPage(ctx, 1, "1", "")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if !hasMore {
		t.Error("expected hasMore from /hxq/1-2.html")
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(items))
	}
	if items[0].ID != "hanxiaoquan:4544" || items[0].Title != "婚姻之后" || items[0].Remark != "更新至06集" {
		t.Errorf("bad first card: %+v", items[0])
	}
	if items[1].Title != "snowy 雪夜谭" {
		t.Errorf("title attribute should win over inner text: %q", items[1].Title)
	}
	if _, _, err := d.fetchHanxiaoquanCatalogPage(ctx, 1, "9", ""); err == nil {
		t.Error("expected invalid category error")
	}
}

func TestHanxiaoquanOfflineDetailPicksBiggestLane(t *testing.T) {
	gate := &hxqGateServer{}
	d, _ := hanxiaoquanFixtureDownloader(t, gate.handler(hxqSearchResults))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	drama, chapters, err := d.fetchHanxiaoquanDetail(ctx, "4544")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if drama.Title != "婚姻之后" || drama.Source != "hanxiaoquan" {
		t.Errorf("bad drama: %+v", drama)
	}
	if strings.Contains(drama.Desc, "@") || drama.Desc == "" {
		t.Errorf("ad text not stripped: %q", drama.Desc)
	}
	if drama.OnlineDate != "2026-07-04" || drama.Remark != "2026-07-04" {
		t.Errorf("bad remark/onlineDate: %q %q", drama.Remark, drama.OnlineDate)
	}
	if len(chapters) != 4 {
		t.Fatalf("expected 4 chapters from 云播资源 lane, got %d", len(chapters))
	}
	for i, want := range []string{"第01集", "第02集", "第03集", "第05集"} {
		if chapters[i].Title != want {
			t.Errorf("chapter %d title %q want %q", i, chapters[i].Title, want)
		}
	}
	if chapters[3].EpisodeString(0) != "5" {
		t.Errorf("episode order from title: %q", chapters[3].EpisodeString(0))
	}
	if !strings.HasPrefix(chapters[0].VideoURL, "hanxiaoquan-play://") {
		t.Errorf("VideoURL %q", chapters[0].VideoURL)
	}
	if chapters[0].ID != providerChapterID("hanxiaoquan", "4544", chapters[0].PageURL) {
		t.Errorf("chapter ID %q", chapters[0].ID)
	}
}

func TestHanxiaoquanOfflineResolveStream(t *testing.T) {
	gate := &hxqGateServer{}
	d, srv := hanxiaoquanFixtureDownloader(t, gate.handler(hxqSearchResults))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, chapters, err := d.fetchHanxiaoquanDetail(ctx, "4544")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	media, err := d.resolveHanxiaoquanMedia(ctx, Task{DramaID: "hanxiaoquan:4544", Chapter: chapters[0]})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	wantURL := fmt.Sprintf("http://%s/media/index.m3u8", strings.TrimPrefix(srv.URL, "http://"))
	if media.URL != wantURL {
		t.Errorf("media URL %q want %q", media.URL, wantURL)
	}
	if !strings.HasPrefix(media.Playlist, "#EXTM3U") {
		t.Errorf("playlist not fetched: %q", media.Playlist)
	}
	bad := Task{DramaID: "hanxiaoquan:4544"}
	bad.Chapter.ID = providerChapterID("hanxiaoquan", "4544", "https://evil.example.com/play/4544-2-0.html")
	if _, err := d.resolveHanxiaoquanMedia(ctx, bad); err == nil {
		t.Error("expected foreign-host rejection")
	}
}

func TestHanxiaoquanOfflineSearchCookieGate(t *testing.T) {
	gate := &hxqGateServer{}
	d, _ := hanxiaoquanFixtureDownloader(t, gate.handler(hxqSearchResults))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	items, hasMore, err := d.searchHanxiaoquan(ctx, 1, "雪")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if hasMore {
		t.Error("hxq search is single-page")
	}
	if len(items) != 2 || items[0].ID != "hanxiaoquan:4544" {
		t.Fatalf("bad results: %+v", items)
	}
	if challenges, results, rejected := gate.counts(); challenges != 1 || results != 1 || rejected != 0 {
		t.Fatalf("gate flow wrong: challenges=%d results=%d rejected=%d", challenges, results, rejected)
	}
	if hanxiaoquanCachedToken() != hxqFixtureToken {
		t.Fatal("token not cached after solve")
	}

	// second search must reuse the cached token (no new challenge hop)
	if _, _, err := d.searchHanxiaoquan(ctx, 1, "婚姻"); err != nil {
		t.Fatalf("cached search: %v", err)
	}
	if challenges, results, rejected := gate.counts(); challenges != 1 || results != 2 || rejected != 0 {
		t.Fatalf("token cache not reused: challenges=%d results=%d", challenges, results)
	}

	// stale/wrong cached token -> must self-heal via a fresh solve round
	hanxiaoquanRememberToken("00000000000000000000000000000000")
	if _, _, err := d.searchHanxiaoquan(ctx, 1, "雪"); err != nil {
		t.Fatalf("recovered search: %v", err)
	}
	if challenges, results, _ := gate.counts(); challenges != 3 || results != 3 {
		t.Fatalf("recovery flow wrong: challenges=%d results=%d", challenges, results)
	}

	// page>1 short-circuits without any HTTP call
	if items, more, err := d.searchHanxiaoquan(ctx, 2, "雪"); err != nil || len(items) != 0 || more {
		t.Errorf("page 2 should be empty: %v %v", items, err)
	}
	if _, _, err := d.searchHanxiaoquan(ctx, 1, ""); err == nil {
		t.Error("empty query should error")
	}
}

func TestHanxiaoquanOfflineIDCPassShape(t *testing.T) {
	pass := hanxiaoquanIDCPass()
	if len(pass) < 10 {
		t.Fatalf("pass too short: %q", pass)
	}
	// The pass is base36(ms) + a random [0-9a-z] tail. Parse the leading
	// timestamp by scanning prefix lengths 12..8 and taking the first
	// decode that lands in a plausible epoch-ms window.
	for _, c := range pass {
		if !strings.ContainsRune("0123456789abcdefghijklmnopqrstuvwxyz", c) {
			t.Fatalf("pass not base36: %q", pass)
		}
	}
	ms := int64(0)
	for n := 12; n >= 8; n-- {
		if len(pass) < n {
			continue
		}
		if v, err := strconv.ParseInt(pass[:n], 36, 64); err == nil && v >= 1_600_000_000_000 && v <= 3_600_000_000_000 {
			ms = v
			break
		}
	}
	if ms == 0 {
		t.Fatalf("pass has no plausible epoch-ms prefix: %q", pass)
	}
	nowMs := time.Now().UnixMilli()
	if ms > nowMs+60_000 || ms < nowMs-86_400_000 {
		t.Errorf("pass timestamp %d far from now %d", ms, nowMs)
	}
}

func TestHanxiaoquanRegistryRegistration(t *testing.T) {
	def, ok := providerSourceDef(providerSlugHanxiaoquan)
	if !ok {
		t.Fatal("hanxiaoquan not registered")
	}
	if !def.Implemented {
		t.Fatal("hanxiaoquan Implemented flag not flipped")
	}
	if def.SearchKind != providerSearchOnline || !def.Lanes {
		t.Errorf("bad def: %+v", def)
	}
	if !providerSourceImplemented("jennyhow.com") || !providerSourceImplemented("www.jennyhow.com") {
		t.Error("domain aliases should resolve to implemented provider")
	}
	if got := validHanxiaoquanCategory("2"); !got {
		t.Error("category 2 should be valid")
	}
	if _, ok := providerSourceDef(providerSlugGuipian); !ok {
		t.Error("guipian def missing (init ordering)")
	}
}

// TestHanxiaoquanLiveSmoke exercises the real www.jennyhow.com endpoints,
// including the search.php cookie gate. Skipped unless GUOPROXY_LIVE=1.
// Evidence captured 2026-11-07: catalog/detail/play live; search passes the
// idc_search_browser gate via Cookie header + _idc_pass query param.
func TestHanxiaoquanLiveSmoke(t *testing.T) {
	if os.Getenv("GUOPROXY_LIVE") != "1" {
		t.Skip("set GUOPROXY_LIVE=1 to hit live www.jennyhow.com")
	}
	hanxiaoquanForgetToken()
	cfg := defaultConfig()
	cfg.dataDir = t.TempDir()
	d := &Downloader{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second},
		limiter: newRequestLimiter(3, 0), providerHosts: map[string]string{}}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	dramas, hasMore, err := d.fetchHanxiaoquanCatalogPage(ctx, 1, "1", "")
	if err != nil || len(dramas) == 0 {
		t.Fatalf("live catalog cat=1 page=1: %d items, err=%v", len(dramas), err)
	}
	t.Logf("catalog: %d dramas hasMore=%v first=%q id=%s", len(dramas), hasMore, dramas[0].Title, dramas[0].SourceID)

	drama, chapters, err := d.fetchHanxiaoquanDetail(ctx, "4544")
	if err != nil || len(chapters) == 0 {
		t.Fatalf("live detail 4544: %d chapters, err=%v", len(chapters), err)
	}
	t.Logf("detail: %q -> %d chapters, first=%s page=%s", drama.Title, len(chapters), chapters[0].Title, chapters[0].PageURL)

	items, _, err := d.searchHanxiaoquan(ctx, 1, "雪")
	if err != nil {
		t.Fatalf("live gated search 雪: %v", err)
	}
	t.Logf("gated search: %d matches (token cached=%v)", len(items), hanxiaoquanCachedToken() != "")
	if len(items) == 0 {
		t.Error("live gated search returned nothing")
	}

	task := Task{DramaID: providerDramaID(providerSlugHanxiaoquan, "4544"), Chapter: chapters[0]}
	media, err := d.resolveHanxiaoquanMedia(ctx, task)
	if err != nil {
		t.Fatalf("live resolve %s: %v", chapters[0].PageURL, err)
	}
	if !strings.Contains(media.URL, ".m3u8") && !strings.Contains(media.URL, ".mp4") {
		t.Errorf("unexpected media URL %q", media.URL)
	}
	t.Logf("resolve: %s", media.URL)
}
