package core

// Offline httptest suite for the 鬼片 provider. Fixtures mirror the live
// guipianwu.com markup captured 2026-11-07 (see research notes): desktop
// macms list cards, ewave multi-lane detail with single-quoted episode
// anchors, and play pages carrying `var now="...m3u8"`.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func guipianFixtureDownloader(t *testing.T, handler http.HandlerFunc) (*Downloader, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	prev := guipianSiteBase
	guipianSiteBase = srv.URL
	t.Cleanup(func() { guipianSiteBase = prev })
	d := &Downloader{
		cfg:           Config{dataDir: t.TempDir(), Retries: 1, PageSize: 30, MaxPagesPerSort: 1},
		client:        srv.Client(),
		limiter:       newRequestLimiter(3, 0),
		providerHosts: map[string]string{},
	}
	return d, srv
}

const guipianListFixture = `<!DOCTYPE html><html><body>
<ul class="row video-list video-film-list">
<li><a class="video-link" href="/nv/1472.html" title="杀人鳄鱼潭2"><img data-original="/upload/vod/20230220/a.jpg"><div class="video-duration">HD</div></a></li>
<li><a class="video-link" href="/nv/76163.html" title="僵尸&#24072;&#29282;&#29602;"><img data-original="https://p.bfvp26.com/upload/vod/b.jpg"><div class="video-duration">更新至20集</div></a></li>
<li><a class="video-link" href="/nv/1472.html" title="重复卡片"><img data-original="/x.jpg"><div class="video-duration">HD</div></a></li>
</ul>
<div class="pagination"><a href="/list/3_2.html">下一页</a></div>
</body></html>`

const guipianDetailFixture = `<!DOCTYPE html><html><head><title>僵尸先生</title></head><body>
<h1 class="media-title">僵尸先生</h1>
<div class="detail-img"><img src="/upload/vod/poster.jpg"></div>
<li class="data">年份：<span>1985</span></li>
<li class="data">地区：<span><a href="/area/香港">香港</a></span></li>
<li class="data">状态：HD</li>
<li class="data">导演：<span>刘观伟</span></li>
<li class="data">主演：<span><a href="/role/a">林正英</a> <a href="/role/b">许冠文</a></span></li>
<li class="desc">简介：道士<em>张三</em>降妖除魔。</li>
<div class="swiper-wrapper">
<li class="swiper-slide ewave-tab active" data-target="#ewave-playlist-1">线路1</li>
<li class="swiper-slide ewave-tab" data-target="#ewave-playlist-2">高清线路</li>
</div>
<div class="playlist-slide ewave-tab-content" id="ewave-playlist-1">
<ul class="ewave-content">
<li><a title='HD中字' href='/play/76163-1-0.html'>HD</a></li>
</ul>
</div>
<div class="playlist-slide ewave-tab-content" id="ewave-playlist-2">
<ul class="ewave-content">
<li><a title='第03集' href='/play/76163-2-2.html'>3</a></li>
<li><a title='第01集' href='/play/76163-2-0.html'>1</a></li>
<li><a title='第02集' href='/play/76163-2-1.html'>2</a></li>
</ul>
</div>
</body></html>`

func guipianRSSFixture() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<rss><channel>
<item><title><![CDATA[杀人鳄鱼潭2]]></title><link>https://www.guipianzj.org/nv/1472.html</link></item>
<item><title><![CDATA[僵尸先生]]></title><link>https://www.guipianzj.org/nv/76163.html</link></item>
<item><title><![CDATA[鳄鱼潭3]]></title><link>https://www.guipianzj.org/nv/90001.html</link></item>
<item><title><![CDATA[猛鬼宿舍]]></title><link>https://www.guipianzj.org/nv/90002.html</link></item>
</channel></rss>`
}

func TestGuipianOfflineCatalog(t *testing.T) {
	d, srv := guipianFixtureDownloader(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/list/3.html":
			if ua := r.Header.Get("User-Agent"); !strings.Contains(ua, "Android") {
				t.Errorf("catalog request lost mobile UA: %q", ua)
			}
			w.Write([]byte(guipianListFixture))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	items, hasMore, err := d.fetchGuipianCatalogPage(ctx, 1, "3", "")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if !hasMore {
		t.Error("expected hasMore from /list/3_2.html pagination link")
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 deduped cards, got %d", len(items))
	}
	if items[0].ID != providerDramaID("guipian", "1472") || items[0].Title != "杀人鳄鱼潭2" {
		t.Errorf("bad first card: %+v", items[0])
	}
	if items[0].CoverURL != srv.URL+"/upload/vod/20230220/a.jpg" {
		t.Errorf("relative cover not fixed up: %q", items[0].CoverURL)
	}
	if items[1].Remark != "更新至20集" {
		t.Errorf("remark: %q", items[1].Remark)
	}
	if strings.Contains(items[1].Title, "&#") {
		t.Errorf("numeric entities not decoded: %q", items[1].Title)
	}
	// invalid category rejected
	if _, _, err := d.fetchGuipianCatalogPage(ctx, 1, "999", ""); err == nil {
		t.Error("expected invalid category error")
	}
	// empty category defaults to 恐怖片 (list/3.html served above)
	if _, _, err := d.fetchGuipianCatalogPage(ctx, 1, "", ""); err != nil {
		t.Errorf("default category: %v", err)
	}
}

func TestGuipianOfflineDetailPicksBiggestLane(t *testing.T) {
	d, _ := guipianFixtureDownloader(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nv/76163.html" {
			w.Write([]byte(guipianDetailFixture))
			return
		}
		http.NotFound(w, r)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	drama, chapters, err := d.fetchGuipianDetail(ctx, "76163")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if drama.Title != "僵尸先生" || drama.Source != "guipian" || drama.ID != "guipian:76163" {
		t.Errorf("bad drama: %+v", drama)
	}
	if drama.Desc == "" || strings.Contains(drama.Desc, "<em>") {
		t.Errorf("desc tags not stripped: %q", drama.Desc)
	}
	if len(chapters) != 3 {
		t.Fatalf("expected 3 chapters from biggest lane, got %d", len(chapters))
	}
	for i, want := range []string{"第01集", "第02集", "第03集"} {
		if chapters[i].Title != want {
			t.Errorf("chapter %d title %q want %q", i, chapters[i].Title, want)
		}
		if ep := chapters[i].EpisodeString(0); ep != string('1'+rune(i)) {
			t.Errorf("chapter %d episode number %q", i, ep)
		}
	}
	first := chapters[0]
	if !strings.HasPrefix(first.VideoURL, "guipian-play://"+guipianSiteBase) || !strings.Contains(first.VideoURL, "/play/76163-2-0.html") {
		t.Errorf("bad VideoURL %q", first.VideoURL)
	}
	if first.ID != providerChapterID("guipian", "76163", first.PageURL) {
		t.Errorf("bad chapter ID %q", first.ID)
	}
	if first.Referer != guipianSiteBase+"/" {
		t.Errorf("bad referer %q", first.Referer)
	}
}

func TestGuipianOfflineResolveStream(t *testing.T) {
	var playBody string
	d, srv := guipianFixtureDownloader(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/nv/76163.html":
			w.Write([]byte(guipianDetailFixture))
		case r.URL.Path == "/play/76163-2-0.html":
			w.Write([]byte(playBody))
		case strings.HasSuffix(r.URL.Path, "/index.m3u8"):
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Write([]byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:9.009,\nseg0.ts\n#EXT-X-ENDLIST\n"))
		default:
			http.NotFound(w, r)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, chapters, err := d.fetchGuipianDetail(ctx, "76163")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	mediaURL := srv.URL + "/media/index.m3u8"
	playBody = `<html><body><script>var now="` + mediaURL + `";</script></body></html>`
	task := Task{DramaID: "guipian:76163", Chapter: chapters[0]}
	media, err := d.resolveGuipianMedia(ctx, task)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if media.URL != mediaURL {
		t.Errorf("media URL %q want %q", media.URL, mediaURL)
	}
	if !strings.HasPrefix(media.Playlist, "#EXTM3U") {
		t.Errorf("playlist not fetched: %q", media.Playlist)
	}
	if media.Referer != guipianSiteBase+"/" {
		t.Errorf("media referer %q", media.Referer)
	}
	// mp4 fallback (no var now, plain URL in page)
	playBody = `<html>url: https://cdn.example.test/v/clip.mp4?sign=1</html>`
	media, err = d.resolveGuipianMedia(ctx, task)
	if err != nil || !strings.HasSuffix(media.URL, "clip.mp4?sign=1") {
		t.Errorf("mp4 fallback: %+v err=%v", media, err)
	}
	// foreign play host rejected (production allowlist)
	bad := task
	bad.Chapter.ID = providerChapterID("guipian", "76163", "https://evil.example.com/play/76163-2-0.html")
	if _, err := d.resolveGuipianMedia(ctx, bad); err == nil {
		t.Error("expected foreign-host rejection")
	}
}

func TestGuipianOfflineRSSSearch(t *testing.T) {
	d, _ := guipianFixtureDownloader(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/xml/rss.xml" {
			w.Write([]byte(guipianRSSFixture()))
			return
		}
		http.NotFound(w, r)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	items, hasMore, err := d.searchGuipian(ctx, 1, "鳄鱼")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if hasMore {
		t.Error("single-page feed should not report more")
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 鳄鱼 matches, got %d", len(items))
	}
	if items[0].ID != "guipian:1472" || items[1].ID != "guipian:90001" {
		t.Errorf("bad ids: %v %v", items[0].ID, items[1].ID)
	}
	if items, _, err := d.searchGuipian(ctx, 2, "鳄鱼"); err != nil || len(items) != 0 {
		t.Errorf("page 2 should be empty: %v %v", items, err)
	}
	if _, _, err := d.searchGuipian(ctx, 1, ""); err == nil {
		t.Error("empty query should error")
	}
}

func TestGuipianRegistryRegistration(t *testing.T) {
	def, ok := providerSourceDef(providerSlugGuipian)
	if !ok {
		t.Fatal("guipian not registered")
	}
	if !def.Implemented {
		t.Fatal("guipian Implemented flag not flipped")
	}
	if def.SearchKind != providerSearchRSS || !def.Lanes {
		t.Errorf("bad def: %+v", def)
	}
	if len(def.CatalogKinds) != len(guipianCategories) {
		t.Errorf("catalog kinds %d vs categories %d", len(def.CatalogKinds), len(guipianCategories))
	}
	if !providerSourceImplemented("guipianwu.com") {
		t.Error("domain alias should resolve to implemented provider")
	}
	if got := validGuipianCategory("3"); !got {
		t.Error("category 3 should be valid")
	}
}

// TestGuipianLiveSmoke exercises the real guipianwu.com endpoints. It is
// skipped unless GUOPROXY_LIVE=1 so CI/offline runs stay deterministic.
// Evidence captured 2026-11-07 (see task report): all four lanes live.
func TestGuipianLiveSmoke(t *testing.T) {
	if os.Getenv("GUOPROXY_LIVE") != "1" {
		t.Skip("set GUOPROXY_LIVE=1 to hit live guipianwu.com")
	}
	cfg := defaultConfig()
	cfg.dataDir = t.TempDir()
	d := &Downloader{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second},
		limiter: newRequestLimiter(3, 0), providerHosts: map[string]string{}}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	dramas, hasMore, err := d.fetchGuipianCatalogPage(ctx, 1, "3", "")
	if err != nil || len(dramas) == 0 {
		t.Fatalf("live catalog cat=3 page=1: %d items, err=%v", len(dramas), err)
	}
	t.Logf("catalog: %d dramas hasMore=%v first=%q id=%s", len(dramas), hasMore, dramas[0].Title, dramas[0].SourceID)

	// The RSS feed rotates, so derive the query term from the current
	// first item title instead of hardcoding one.
	feed, err := d.guipianGet(ctx, "/xml/rss.xml")
	if err != nil {
		t.Fatalf("live rss fetch: %v", err)
	}
	term := ""
	for _, item := range reGuipianRSSItem.FindAllStringSubmatch(feed, -1) {
		if m := reGuipianRSSTitle.FindStringSubmatch(item[1]); m != nil {
			if r := []rune(guipianClean(m[1])); len(r) >= 2 {
				term = string(r[:2])
				break
			}
		}
	}
	if term == "" {
		t.Fatal("rss feed has no parsable item title")
	}
	items, _, err := d.searchGuipian(ctx, 1, term)
	if err != nil || len(items) == 0 {
		t.Fatalf("live RSS search %q: %d items, err=%v", term, len(items), err)
	}
	t.Logf("rss search %q: %d matches, first=%q id=%s", term, len(items), items[0].Title, items[0].SourceID)

	drama, chapters, err := d.fetchGuipianDetail(ctx, items[0].SourceID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("live detail %s: %d chapters, err=%v", items[0].SourceID, len(chapters), err)
	}
	t.Logf("detail: %q lanes picked %d chapters first=%s", drama.Title, len(chapters), chapters[0].Title)

	task := Task{DramaID: providerDramaID(providerSlugGuipian, items[0].SourceID), Chapter: chapters[0]}
	media, err := d.resolveGuipianMedia(ctx, task)
	if err != nil {
		t.Fatalf("live resolve %s: %v", chapters[0].PageURL, err)
	}
	if !strings.Contains(media.URL, ".m3u8") && !strings.Contains(media.URL, ".mp4") {
		t.Errorf("unexpected media URL %q", media.URL)
	}
	t.Logf("resolve: %s", media.URL)
}
