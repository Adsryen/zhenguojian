package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// ---------------------------------------------------------------- fixtures --
// 结构照抄 2026-11-07 实测页面（heitaodj.com，年龄墙 Cookie 生效）：
// 首页导航 /vod/type/id/N.html、卡片 <a class="thumb">+<img class="quic">+
// <div class="icon-br"><span class="label">N集</span>、详情页 ld+json
// [BreadcrumbList, TVSeries] + /vod/play/id/x/sid/1/nid/n.html 分集链接。

const heitaoHomeFixture = `<!DOCTYPE html><html><head><title>黑桃</title></head><body>
<div class="warp"><ul class="clearfix">
<li class="active"><a class="item" href="/">首页</a></li>
<li><a class="item" href="/index.php/vod/type/id/1.html">现代都市</a></li>
<li><a class="item" href="/index.php/vod/type/id/2.html">古装传奇</a></li>
<li><a class="item" href="/index.php/vod/type/id/3.html">仙侠玄幻</a></li>
<li><a class="item" href="/index.php/vod/type/id/13.html">家庭纠葛</a></li>
</ul></div>
<ul class="vod-list">
<li class="m-item"><a class="thumb" href="/index.php/vod/detail/id/1150.html" title="陆雪琪VS南宫仙儿">
<img class="quic" src="https://img.heitaodj.com/upload/vod/20261005-1/f3de49.jpg" width="200" height="300" alt="陆雪琪VS南宫仙儿">
<div class="icon-br"><span class="label">HD</span></div></a>
<div class="text"><h3 class="stitle"><a href="/index.php/vod/detail/id/1150.html" title="陆雪琪VS南宫仙儿">陆雪琪VS南宫仙儿</a></h3></div></li>
<li class="m-item"><a class="thumb" href="/index.php/vod/detail/id/1240.html" title="无能老公骚情妻">
<img class="quic" src="/upload/vod/20261104-1/1b663a.jpg" alt="无能老公骚情妻">
<div class="icon-br"><span class="label">12集</span></div></a>
<div class="text"><h3 class="stitle"><a href="/index.php/vod/detail/id/1240.html" title="无能老公骚情妻">无能老公骚情妻</a></h3></div></li>
</ul></body></html>`

const heitaoShowFixture = `<!DOCTYPE html><html><body>
<ul class="vod-list"><li class="m-item"><a class="thumb" href="/index.php/vod/detail/id/1468.html" title="新人女优">
<img class="quic" src="https://img.heitaodj.com/upload/vod/20261104-1/bd918e.jpg" alt="新人女优">
<div class="icon-br"><span class="label">2集</span></div></a></li>
<li class="m-item"><a class="thumb" href="/index.php/vod/detail/id/1240.html" title="无能老公骚情妻">
<img class="quic" data-src="https://img.heitaodj.com/upload/vod/20261104-1/1b663a.jpg" alt="无能老公骚情妻">
<div class="icon-br"><span class="label">12集</span></div></a></li>
<li class="m-item"><a class="thumb" href="/index.php/vod/detail/id/1240.html" title="重复卡片">
<img class="quic" src="https://img.heitaodj.com/x.jpg" alt="重复卡片"></a></li></ul>
<div class="pages">
<a href="/index.php/vod/show/id/1/page/2.html">2</a>
<a href="/index.php/vod/show/id/1/page/46.html">46</a>
<a href="/index.php/vod/show/id/11.html">子分类</a>
</div></body></html>`

const heitaoSearchFixture = `<!DOCTYPE html><html><body>
<ul class="vod-list"><li class="m-item"><a class="thumb" href="/index.php/vod/detail/id/1150.html" title="陆雪琪VS南宫仙儿">
<img class="quic" src="https://img.heitaodj.com/upload/1/f3de49.jpg" alt="陆雪琪VS南宫仙儿">
<div class="icon-br"><span class="label">HD</span></div></a></li></ul>
<div class="pages"><a href="/index.php/vod/search/page/2/wd/%E9%99%86%E9%9B%AA%E7%90%AA.html">下一页</a></div></body></html>`

func heitaoDetailFixture(sourceID string, sids map[int]int) string {
	var builder strings.Builder
	builder.WriteString(`<!DOCTYPE html><html><head>
<script type="application/ld+json">[{"@context":"https://schema.org","@type":"BreadcrumbList","itemListElement":[
{"@type":"ListItem","position":2,"name":"家庭纠葛","item":"https://heitaodj.com/index.php/vod/type/id/13.html"}]},
{"@context":"https://schema.org","@type":"TVSeries","name":"无能老公骚情妻","url":"https://heitaodj.com/index.php/vod/detail/id/` + sourceID + `.html",
"description":"丈夫窝囊无能，妻子心生不满。","image":"https://img.heitaodj.com/upload/vod/20261104-1/1b663a.jpg","genre":["家庭纠葛"],"numberOfEpisodes":12}]</script>
</head><body>
<div class="title ellipsis-2">备用标题</div>
<ul class="detail-info"><li>分类：家庭纠葛</li></ul>
<div class="detail-tags"><a class="tag-chip" href="/index.php/vod/search.html?tag=%E4%BB%99%E4%BE%A0">仙侠</a>
<a class="tag-chip" href="/index.php/vod/search.html?tag=%E4%BB%99%E4%BE%A0">仙侠</a></div>
<div class="playlist">`)
	for sid, count := range sids {
		for nid := 1; nid <= count; nid++ {
			fmt.Fprintf(&builder, `<a href="/index.php/vod/play/id/%s/sid/%d/nid/%d.html">第%d集</a>`, sourceID, sid, nid, nid)
		}
	}
	builder.WriteString(`</div></body></html>`)
	return builder.String()
}

// heitaoPlainPlayerFixture 截断自 2026-11-07 实测 /index.php/vod/player/id/1240/nid/1/sid/1.html（encrypt=0 明文态）。
const heitaoPlainPlayerFixture = `<html><head><title>播放</title></head><body><div class="player">
<script type="text/javascript">
player_aaaa={"flag":"play","encrypt":0,"trysee":0,"points":0,"link":"\/index.php\/vod\/play\/id\/1240\/sid\/1\/nid\/1.html","link_next":"\/index.php\/vod\/play\/id\/1240\/sid\/1\/nid\/2.html","link_pre":"","vod_data":{"vod_name":"\u65e0\u80fd\u8001\u516c\u9a9a\u60c5\u59bb","vod_actor":"","vod_director":"","vod_class":""},"url":"https:\/\/play.djqiepian.app\/vod\/t-b439d805a8171ba15ac51dc5\/ep-000001-5fdf49418f23\/index.m3u8","url_next":"https:\/\/play.djqiepian.app\/vod\/t-b439d805a8171ba15ac51dc5\/ep-000002-5030858a1425\/index.m3u8","from":"zym3u8","server":"no","note":"","id":"1240","sid":1,"nid":1}</script>
<script src="/static/js/player.js"></script></div></body></html>`

// heitaoAgeGatePlayerFixture 无 Cookie 时服务端返回的年龄墙锁页（3039B 实测页缩样，
// 关键差异：不含 player_aaaa，只有 heitao-age-gate-lock 遮罩）。
const heitaoAgeGatePlayerFixture = `<html><head><link rel="stylesheet" href="/static/css/heitao-age-gate.css?v=20260921-1"></head>
<body><div class="heitao-age-gate"><div class="heitao-age-gate-lock">请确认年龄</div></div></body></html>`

const heitaoM3U8Fixture = "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:10\n#EXTINF:10.0,\nseg001.ts\n#EXT-X-ENDLIST\n"

// heitaoFixtureTransport 按路径路由黑桃 fixture；记录请求头供断言。
func heitaoFixtureTransport(t *testing.T, responses map[string]string, seen map[string]*http.Request) sourceFixtureTransport {
	t.Helper()
	return func(request *http.Request) (*http.Response, error) {
		if seen != nil {
			seen[request.URL.Path] = request.Clone(request.Context())
		}
		if request.URL.Host == "play.djqiepian.app" {
			return sourceFixtureResponse(request, http.StatusOK, heitaoM3U8Fixture), nil
		}
		if request.URL.Host != "heitaodj.com" {
			t.Fatalf("unexpected heitao host: %s", request.URL.Host)
		}
		body, found := responses[request.URL.Path]
		if !found {
			t.Fatalf("unexpected heitao path: %s", request.URL)
		}
		return sourceFixtureResponse(request, http.StatusOK, body), nil
	}
}

func heitaoResponses() map[string]string {
	return map[string]string{
		"/":                             heitaoHomeFixture,
		"/index.php/vod/show/id/1.html": heitaoShowFixture,
		"/index.php/vod/search/wd/陆雪琪.html": heitaoSearchFixture,
	}
}

// ------------------------------------------------------------------- tests --

func TestHeitaoRegistryMarksImplemented(t *testing.T) {
	def, found := providerSourceDef(sourceHeitao)
	if !found {
		t.Fatal("heitao missing from provider registry")
	}
	if !def.Implemented {
		t.Fatal("heitao should be Implemented after init()")
	}
	if def.DisplayName != "黑桃" || def.SearchKind != providerSearchOnline || !def.Lanes || !def.EnabledByDefault {
		t.Fatalf("unexpected registry entry: %+v", def)
	}
	if !providerSourceImplemented(sourceHeitao) {
		t.Fatal("providerSourceImplemented should be true for heitao")
	}
}

func TestHeitaoCatalogAddressShapes(t *testing.T) {
	site := heitaoSite()
	cases := []struct {
		page                  int
		category, query, want string
	}{
		{1, "", "", site + "/index.php/vod/show.html"},
		{1, "3", "", site + "/index.php/vod/show/id/3.html"},
		{2, "3", "", site + "/index.php/vod/show/id/3/page/2.html"},
		{1, "", "陆雪琪", site + "/index.php/vod/search/wd/%E9%99%86%E9%9B%AA%E7%90%AA.html"},
		{3, "", "陆雪琪", site + "/index.php/vod/search/page/3/wd/%E9%99%86%E9%9B%AA%E7%90%AA.html"},
	}
	for _, item := range cases {
		if got := heitaoCatalogAddress(site, item.page, item.category, item.query); got != item.want {
			t.Fatalf("catalog address %d/%q/%q: got %s want %s", item.page, item.category, item.query, got, item.want)
		}
	}
}

func TestHeitaoFetchesInjectAgeCookieAndAgent(t *testing.T) {
	responses := heitaoResponses()
	var seen *http.Request
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/index.php/vod/show/id/1.html" {
			seen = request.Clone(request.Context())
		}
		return heitaoFixtureTransport(t, responses, nil)(request)
	})
	items, more, err := downloader.fetchHeitaoCatalogPage(context.Background(), 1, "1", "")
	if err != nil {
		t.Fatal(err)
	}
	if seen.Header.Get("Cookie") != heitaoAgeCookie {
		t.Fatalf("age cookie missing: %q", seen.Header.Get("Cookie"))
	}
	if !strings.Contains(seen.Header.Get("User-Agent"), "Pixel 7") {
		t.Fatalf("unexpected UA: %q", seen.Header.Get("User-Agent"))
	}
	if !more || len(items) != 2 {
		t.Fatalf("want 2 items more=true, got %d more=%v", len(items), more)
	}
	if items[1].ID != "heitao:1240" || items[1].SourceID != "1240" || items[1].Remark != "12集" || items[1].EpisodeCount != 12 {
		t.Fatalf("unexpected card: %+v", items[1])
	}
	if items[1].Cover != "https://img.heitaodj.com/upload/vod/20261104-1/1b663a.jpg" {
		t.Fatalf("lazy cover not used: %v", items[1].Cover)
	}
	if items[0].Cover != "https://img.heitaodj.com/upload/vod/20261104-1/bd918e.jpg" || items[0].EpisodeCount != 2 {
		t.Fatalf("unexpected first card: %+v", items[0])
	}
}

func TestHeitaoHomeCategoriesAndHomeCardCoverResolve(t *testing.T) {
	downloader := sourceFixtureDownloader(t, heitaoFixtureTransport(t, heitaoResponses(), nil))
	categories, err := downloader.fetchHeitaoCategories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(categories) != 4 || categories[3].ID != "13" || categories[3].Name != "家庭纠葛" {
		t.Fatalf("unexpected categories: %+v", categories)
	}
	items := parseHeitaoCards(mustHeitaoDocument(t, heitaoHomeFixture), heitaoSite()+"/")
	if len(items) != 2 || items[1].Cover != "https://heitaodj.com/upload/vod/20261104-1/1b663a.jpg" {
		t.Fatalf("relative cover not resolved: %+v", items)
	}
	if items[0].Remark != "HD" || items[0].EpisodeCount != nil {
		t.Fatalf("non-episode remark should leave count empty: %+v", items[0])
	}
}

func TestHeitaoSearchPageUsesSearchPagination(t *testing.T) {
	downloader := sourceFixtureDownloader(t, heitaoFixtureTransport(t, heitaoResponses(), nil))
	items, more, err := downloader.fetchHeitaoCatalogPage(context.Background(), 1, "", "陆雪琪")
	if err != nil {
		t.Fatal(err)
	}
	if !more || len(items) != 1 || items[0].SourceID != "1150" {
		t.Fatalf("unexpected search page: %d more=%v", len(items), more)
	}
}

func TestHeitaoChaptersPickBusiestLane(t *testing.T) {
	document := mustHeitaoDocument(t, heitaoDetailFixture("1240", map[int]int{1: 3, 2: 5, 3: 5}))
	chapters := heitaoChapters(document, heitaoSite(), "1240")
	if len(chapters) != 5 {
		t.Fatalf("want 5 chapters from lane 2 (tie→lower sid), got %d", len(chapters))
	}
	for index, chapter := range chapters {
		wantID := fmt.Sprintf("heitao:1240:2:%d", index+1)
		wantPage := fmt.Sprintf("%s/index.php/vod/player/id/1240/nid/%d/sid/2.html", heitaoSite(), index+1)
		if chapter.ID != wantID || chapter.VideoURL != fmt.Sprintf("heitao-player://2:%d", index+1) ||
			chapter.PageURL != wantPage || chapter.Source != sourceHeitao || chapter.Referer != heitaoSite()+"/" {
			t.Fatalf("unexpected chapter %d: %+v", index, chapter)
		}
	}
	if other := heitaoChapters(document, heitaoSite(), "9999"); len(other) != 0 {
		t.Fatalf("wrong sourceID should yield no chapters: %d", len(other))
	}
}

func TestHeitaoDetailMetadataAndFallbacks(t *testing.T) {
	responses := map[string]string{
		"/index.php/vod/detail/id/1240.html": heitaoDetailFixture("1240", map[int]int{1: 12}),
	}
	downloader := sourceFixtureDownloader(t, heitaoFixtureTransport(t, responses, nil))
	drama, chapters, err := downloader.fetchHeitaoDetail(context.Background(), "1240")
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 12 || drama.TotalEpisode != 12 {
		t.Fatalf("want 12 episodes, got %d/%v", len(chapters), drama.TotalEpisode)
	}
	if drama.Title != "无能老公骚情妻" || drama.Cover != "https://img.heitaodj.com/upload/vod/20261104-1/1b663a.jpg" {
		t.Fatalf("unexpected ld+json metadata: %+v", drama)
	}
	if drama.Desc != "丈夫窝囊无能，妻子心生不满。" || drama.CategoryName != "家庭纠葛" {
		t.Fatalf("unexpected desc/category: %q %q", drama.Desc, drama.CategoryName)
	}
	if len(drama.Tags) != 1 || drama.Tags[0] != "仙侠" {
		t.Fatalf("unexpected tags: %+v", drama.Tags)
	}
	if _, _, err := downloader.fetchHeitaoDetail(context.Background(), "bad"); err == nil {
		t.Fatal("invalid id should fail")
	}
}

func TestHeitaoReadPlayerConfigStates(t *testing.T) {
	const plain = "https://play.djqiepian.app/vod/t-abc/ep-000001/index.m3u8"
	escaped := strings.NewReplacer("/", "%2F", ":", "%3A").Replace(plain)
	b64 := base64.StdEncoding.EncodeToString([]byte(escaped))
	samples := []struct {
		name, body string
		encrypt    int
		want       string
	}{
		{"live-encrypt0", heitaoPlainPlayerFixture, 0,
			"https://play.djqiepian.app/vod/t-b439d805a8171ba15ac51dc5/ep-000001-5fdf49418f23/index.m3u8"},
		{"fixture-encrypt1", `player_aaaa={"flag":"play","encrypt":1,"url":"` + escaped + `"};</script>`, 1, plain},
		{"fixture-encrypt2", `player_aaaa={"flag":"play","encrypt":"2","url":"` + b64 + `"};</script>`, 2, plain},
	}
	for _, sample := range samples {
		entry, err := heitaoReadPlayerConfig(sample.body)
		if err != nil {
			t.Fatalf("%s: %v", sample.name, err)
		}
		if got := heitaoPlayerEncrypt(entry.Encrypt); got != sample.encrypt {
			t.Fatalf("%s: encrypt got %d want %d", sample.name, got, sample.encrypt)
		}
		address, err := heitaoDecryptMediaURL(entry.URL, heitaoPlayerEncrypt(entry.Encrypt))
		if err != nil {
			t.Fatalf("%s decrypt: %v", sample.name, err)
		}
		if address != sample.want {
			t.Fatalf("%s: got %s want %s", sample.name, address, sample.want)
		}
	}
	if _, err := heitaoReadPlayerConfig(heitaoAgeGatePlayerFixture); err == nil || !strings.Contains(err.Error(), "未返回配置") {
		t.Fatalf("age-gate page should report missing config, got %v", err)
	}
	if _, err := heitaoReadPlayerConfig(`player_aaaa={"encrypt":0,"url":"https://a.test/x.m3u8"`); err == nil ||
		!strings.Contains(err.Error(), "不完整") {
		t.Fatalf("truncated json should fail, got %v", err)
	}
	if _, err := heitaoDecryptMediaURL(plain, 9); err == nil {
		t.Fatal("unknown encrypt mode should fail")
	}
}

func TestHeitaoResolveMediaEndToEnd(t *testing.T) {
	responses := map[string]string{
		"/index.php/vod/player/id/1240/nid/1/sid/1.html": heitaoPlainPlayerFixture,
	}
	var playerRequest, mediaRequest *http.Request
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "heitaodj.com":
			playerRequest = request.Clone(request.Context())
			return heitaoFixtureTransport(t, responses, nil)(request)
		case "play.djqiepian.app":
			mediaRequest = request.Clone(request.Context())
			return sourceFixtureResponse(request, http.StatusOK, heitaoM3U8Fixture), nil
		}
		t.Fatalf("unexpected host %s", request.URL.Host)
		return nil, nil
	})
	task := Task{DramaID: "heitao:1240", Chapter: Chapter{
		ID:       "heitao:1240:1:1",
		Source:   sourceHeitao,
		VideoURL: "heitao-player://1:1",
		PageURL:  heitaoSite() + "/index.php/vod/player/id/1240/nid/1/sid/1.html",
	}}
	media, err := downloader.resolveHeitaoMedia(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if playerRequest.Header.Get("Cookie") != heitaoAgeCookie || playerRequest.Header.Get("Referer") != heitaoSite()+"/index.php/vod/play/id/1240/sid/1/nid/1.html" {
		t.Fatalf("player request headers wrong: cookie=%q referer=%q", playerRequest.Header.Get("Cookie"), playerRequest.Header.Get("Referer"))
	}
	wantURL := "https://play.djqiepian.app/vod/t-b439d805a8171ba15ac51dc5/ep-000001-5fdf49418f23/index.m3u8"
	if media.URL != wantURL || media.Referer != heitaoSite()+"/" {
		t.Fatalf("unexpected media: %+v", media)
	}
	if !strings.HasPrefix(media.Playlist, "#EXTM3U") {
		t.Fatalf("playlist not fetched: %q", media.Playlist)
	}
	if mediaRequest == nil || mediaRequest.Header.Get("Referer") != heitaoSite()+"/" ||
		!strings.Contains(mediaRequest.Header.Get("User-Agent"), "Pixel 7") ||
		mediaRequest.Header.Get("Cookie") != "" {
		t.Fatalf("media request headers wrong: %+v", mediaRequest.Header)
	}
}

func TestHeitaoResolveMediaBlockedByAgeGate(t *testing.T) {
	responses := map[string]string{
		"/index.php/vod/player/id/1240/nid/1/sid/1.html": heitaoAgeGatePlayerFixture,
	}
	downloader := sourceFixtureDownloader(t, heitaoFixtureTransport(t, responses, nil))
	_, err := downloader.resolveHeitaoMedia(context.Background(), Task{
		DramaID: "heitao:1240",
		Chapter: Chapter{ID: "heitao:1240:1:1", Source: sourceHeitao, PageURL: heitaoSite() + "/index.php/vod/player/id/1240/nid/1/sid/1.html"},
	})
	if err == nil || !strings.Contains(err.Error(), "未返回配置") {
		t.Fatalf("age-gate should block resolution, got %v", err)
	}
	if _, err := downloader.resolveHeitaoMedia(context.Background(), Task{
		DramaID: "dsd:1240", Chapter: Chapter{ID: "heitao:1240:1:1"},
	}); err == nil || !strings.Contains(err.Error(), "分集信息无效") {
		t.Fatalf("foreign drama id should be rejected, got %v", err)
	}
}

func TestHeitaoChapterKeyParsing(t *testing.T) {
	if sid, nid, ok := heitaoSeparateEpisode("2:15"); !ok || sid != 2 || nid != 15 {
		t.Fatalf("separate failed: %d %d %v", sid, nid, ok)
	}
	if _, _, ok := heitaoSeparateEpisode("0:1"); ok {
		t.Fatal("sid 0 should be invalid")
	}
	if sid, nid, ok := heitaoPlayerID("https://heitaodj.com/index.php/vod/player/id/1240/nid/7/sid/3.html"); !ok || sid != 3 || nid != 7 {
		t.Fatalf("player route failed: %d %d %v", sid, nid, ok)
	}
	if _, _, ok := heitaoPlayerID("https://heitaodj.com/index.php/vod/play/id/1240/sid/1/nid/1.html"); ok {
		t.Fatal("play route must not match player route")
	}
}

func mustHeitaoDocument(t *testing.T, body string) *html.Node {
	t.Helper()
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return document
}
