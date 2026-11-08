package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// huangguoaiFixtureDownloader 参照 app_sources_test.go sourceFixtureDownloader 模式。
func huangguoaiFixtureDownloader(cfgURL string) *Downloader {
	return &Downloader{
		cfg:           Config{HuangguoAIURL: cfgURL, Retries: 1},
		client:        &http.Client{},
		limiter:       newRequestLimiter(3, 0),
		providerHosts: map[string]string{},
	}
}

// stubHuangguoAIPool 临时替换镜像池并清空漂移域，t.Cleanup 还原。
func stubHuangguoAIPool(t *testing.T, mirrors ...string) {
	t.Helper()
	previous := huangguoAIBaseMirrors
	huangguoAIBaseMirrors = mirrors
	huangguoAIDriftReset()
	huangguoAIEdgeClearCooldown()
	t.Cleanup(func() {
		huangguoAIBaseMirrors = previous
		huangguoAIDriftReset()
		huangguoAIEdgeClearCooldown()
	})
}

type edgeHit struct {
	method string
	path   string
	body   string
	header http.Header
}

// edgeEnvelopeHandler 断言 /api/edge/request 收到 POST 信封，并按 inner 映射回复 status/data。
func edgeEnvelopeHandler(t *testing.T, mu *sync.Mutex, hits *[]edgeHit, inner func(path string) (status int, msg string, data any)) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		rawBody, err := io.ReadAll(io.LimitReader(r.Body, providerMaxBodyBytes))
		if err != nil {
			t.Errorf("read envelope: %v", err)
			return
		}
		body := string(rawBody)
		var envelope struct {
			Method      string `json:"method"`
			Path        string `json:"path"`
			Body        string `json:"body"`
			ContentType string `json:"content_type"`
		}
		if err := json.Unmarshal([]byte(body), &envelope); err != nil {
			t.Errorf("edge handler got non-envelope body: %q", body)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if envelope.Method != http.MethodGet || envelope.Body != "" || envelope.ContentType != "" {
			t.Errorf("unexpected envelope fields: %+v", envelope)
		}
		if mu != nil {
			mu.Lock()
		}
		if hits != nil {
			*hits = append(*hits, edgeHit{method: r.Method, path: r.URL.Path, body: body, header: r.Header.Clone()})
		}
		if mu != nil {
			mu.Unlock()
		}
		status, msg, data := inner(envelope.Path)
		payload, err := json.Marshal(map[string]any{"status": status, "msg": msg, "data": data})
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}
}

func TestHuangguoaiEnvelopeRequestAndParse(t *testing.T) {
	var hits []edgeHit
	srv := httptest.NewServer(edgeEnvelopeHandler(t, nil, &hits, func(path string) (int, string, any) {
		if path != "/api/videos/abc123" {
			return 0, "路径不符", nil
		}
		return 1, "", map[string]any{"id": "abc123", "title": "测试剧集"}
	}))
	defer srv.Close()
	stubHuangguoAIPool(t, srv.URL)
	d := huangguoaiFixtureDownloader(srv.URL)

	var out map[string]any
	if err := d.huangguoAIAPI(context.Background(), "/api/videos/abc123", &out); err != nil {
		t.Fatalf("huangguoAIAPI: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	if hits[0].method != http.MethodPost || hits[0].path != huangguoAIEdgePath {
		t.Fatalf("got %s %s, want POST %s", hits[0].method, hits[0].path, huangguoAIEdgePath)
	}
	if got := hits[0].header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := hits[0].header.Get("User-Agent"); got != userAgent {
		t.Fatalf("User-Agent = %q, want %q", got, userAgent)
	}
	if !strings.HasPrefix(hits[0].header.Get("Referer"), srv.URL) {
		t.Fatalf("Referer = %q", hits[0].header.Get("Referer"))
	}
	if out["title"] != "测试剧集" {
		t.Fatalf("decoded data = %+v", out)
	}
	if host := d.providerHosts[sourceHuangguoAI]; host != srv.URL {
		t.Fatalf("providerHosts = %q, want %q", host, srv.URL)
	}
}

func TestHuangguoaiEnvelopeStatusFailurePassesMsg(t *testing.T) {
	srv := httptest.NewServer(edgeEnvelopeHandler(t, nil, nil, func(string) (int, string, any) {
		return 0, "权限不足", nil
	}))
	defer srv.Close()
	stubHuangguoAIPool(t, srv.URL)
	d := huangguoaiFixtureDownloader(srv.URL)
	err := d.huangguoAIAPI(context.Background(), "/api/videos/abc123", &map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "黄果接口返回失败：权限不足") {
		t.Fatalf("err = %v, want 黄果接口返回失败：权限不足", err)
	}
	if huangguoAIEdgeExhausted(err) {
		t.Fatalf("business refusal must not be marked exhausted")
	}
}

func TestHuangguoaiEdgeFailoverAcrossMirrors(t *testing.T) {
	var goodHits []edgeHit
	good := httptest.NewServer(edgeEnvelopeHandler(t, nil, &goodHits, func(string) (int, string, any) {
		return 1, "", map[string]any{"ok": true}
	}))
	defer good.Close()
	// 毒化域名：返回 HTML（非信封应答）→ 应换下一候选
	poison := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>旧版服务</html>"))
	}))
	defer poison.Close()
	stubHuangguoAIPool(t, poison.URL, good.URL)
	d := huangguoaiFixtureDownloader(good.URL)
	var out map[string]any
	if err := d.huangguoAIAPI(context.Background(), "/api/search?q=x", &out); err != nil {
		t.Fatalf("huangguoAIAPI: %v", err)
	}
	if out["ok"] != true || len(goodHits) != 1 {
		t.Fatalf("out = %+v hits = %d", out, len(goodHits))
	}
	if host := d.providerHosts[sourceHuangguoAI]; host != good.URL {
		t.Fatalf("providerHosts = %q, want %q", host, good.URL)
	}
}

func TestHuangguoaiEdgeRedirectPreservesPostAndLearnsDrift(t *testing.T) {
	var srv2Hits []edgeHit
	var srv2 *httptest.Server
	srv2 = httptest.NewServer(edgeEnvelopeHandler(t, nil, &srv2Hits, func(string) (int, string, any) {
		return 1, "", map[string]any{"drift": srv2.URL}
	}))
	defer srv2.Close()
	var srv1Count int
	var mu sync.Mutex
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		srv1Count++
		mu.Unlock()
		if r.Method != http.MethodPost {
			t.Errorf("redirect-chain first hop should be POST, got %s", r.Method)
		}
		http.Redirect(w, r, srv2.URL+huangguoAIEdgePath, http.StatusMovedPermanently)
	}))
	defer srv1.Close()
	stubHuangguoAIPool(t, srv1.URL)
	d := huangguoaiFixtureDownloader(srv1.URL)

	var out map[string]any
	if err := d.huangguoAIAPI(context.Background(), "/api/videos/abc123", &out); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(srv2Hits) != 1 || srv2Hits[0].method != http.MethodPost {
		t.Fatalf("srv2 hits = %+v, want one POST", srv2Hits)
	}
	if !strings.Contains(srv2Hits[0].body, "\"method\":\"GET\"") || !strings.Contains(srv2Hits[0].body, "/api/videos/abc123") {
		t.Fatalf("envelope lost across redirect: %q", srv2Hits[0].body)
	}
	snapshot := huangguoAIDriftSnapshot()
	if len(snapshot) == 0 || snapshot[0] != srv2.URL {
		t.Fatalf("drift = %v, want %s first", snapshot, srv2.URL)
	}
	// 第二次调用漂移域优先，直达 srv2，不再绕 srv1
	if err := d.huangguoAIAPI(context.Background(), "/api/videos/abc123", &out); err != nil {
		t.Fatalf("second call: %v", err)
	}
	mu.Lock()
	count := srv1Count
	mu.Unlock()
	if count != 1 {
		t.Fatalf("srv1 hit %d times, want 1 (drift-first ordering failed)", count)
	}
	if len(srv2Hits) != 2 {
		t.Fatalf("srv2 hits = %d, want 2", len(srv2Hits))
	}
}

func TestHuangguoaiAutoEnvelopeForCategoryGETs(t *testing.T) {
	var hits []edgeHit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == huangguoAIEdgePath && r.Method == http.MethodPost {
			rawBytes, err := io.ReadAll(io.LimitReader(r.Body, providerMaxBodyBytes))
			if err != nil {
				t.Errorf("read envelope: %v", err)
				return
			}
			raw := string(rawBytes)
			var envelope struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
				t.Errorf("bad envelope: %v", err)
			}
			hits = append(hits, edgeHit{method: r.Method, path: envelope.Path, body: raw})
			payload, _ := json.Marshal(map[string]any{"status": 1, "msg": "", "data": []map[string]any{{"id": "abc123", "title": "分类剧集"}}})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		// 非 edge 路径：HTML 页面，原样返回
		_, _ = w.Write([]byte(`<html><body><a href="/video/abc123/">页面</a></body></html>`))
	}))
	defer srv.Close()
	stubHuangguoAIPool(t, srv.URL)
	d := huangguoaiFixtureDownloader(srv.URL)
	ctx := context.Background()

	// /api/ 分类 GET → 信封（修复 app_categories.go 直连 GET 失效）
	body, err := d.fetchProviderText(ctx, srv.URL+"/api/videos/category/ai-duanju?sort=hot&page=1&size=24", srv.URL+"/")
	if err != nil {
		t.Fatalf("category fetch: %v", err)
	}
	if len(hits) != 1 || hits[0].path != "/api/videos/category/ai-duanju?sort=hot&page=1&size=24" {
		t.Fatalf("envelope not routed: %+v", hits)
	}
	if !strings.Contains(body, "分类剧集") {
		t.Fatalf("body should carry unwrapped data, got %q", body)
	}

	// HTML 页面路径保持纯 GET，不进信封
	if _, err := d.fetchProviderText(ctx, srv.URL+"/video/abc123/", srv.URL+"/"); err != nil {
		t.Fatalf("html fetch: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("html page must bypass envelope, hits = %+v", hits)
	}
}

func TestHuangguoaiDetailJSONChapters(t *testing.T) {
	srv := httptest.NewServer(edgeEnvelopeHandler(t, nil, nil, func(path string) (int, string, any) {
		if path != "/api/videos/abc123" {
			return 0, "路径不符", nil
		}
		return 1, "", map[string]any{
			"id":    "abc123",
			"title": "测试剧集",
			"desc":  "简介",
			"episodes": []any{
				map[string]any{"ep_num": 1, "title": "第1集·开局"},
				map[string]any{"ep_num": 2},
				map[string]any{"ep_num": 3, "title": "收尾"},
			},
		}
	}))
	defer srv.Close()
	stubHuangguoAIPool(t, srv.URL)
	d := huangguoaiFixtureDownloader(srv.URL)
	drama, chapters, err := d.fetchHuangguoAIDetailJSON(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("fetchHuangguoAIDetailJSON: %v", err)
	}
	if drama.SourceID != "abc123" || drama.DisplayTitle() != "测试剧集" {
		t.Fatalf("drama = %+v", drama)
	}
	if len(chapters) != 3 {
		t.Fatalf("chapters = %d, want 3", len(chapters))
	}
	wantKeys := []string{"auto-1", "ep-2", "ep-3"}
	wantTitles := []string{"第1集·开局", "第2集", "收尾"}
	for i, want := range wantKeys {
		if chapters[i].ID != providerChapterID(sourceHuangguoAI, "abc123", want) {
			t.Fatalf("chapter %d ID = %s, want ...:%s", i, chapters[i].ID, want)
		}
		if chapters[i].Title != wantTitles[i] {
			t.Fatalf("chapter %d title = %q, want %q", i, chapters[i].Title, wantTitles[i])
		}
		wantPage := srv.URL + "/video/abc123/"
		if i > 0 {
			wantPage += want + "/"
		}
		if chapters[i].PageURL != wantPage {
			t.Fatalf("chapter %d PageURL = %s, want %s", i, chapters[i].PageURL, wantPage)
		}
	}
}

func TestHuangguoaiDetailHTMLFallbackOnAPIDismiss(t *testing.T) {
	var wgBody = `<html><head><title>测试剧集</title></head><body><h1>测试剧集</h1>` +
		`<a href="/video/abc123/ep-1/">第1集</a><a href="/video/abc123/ep-2/">第2集</a></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == huangguoAIEdgePath {
			payload, _ := json.Marshal(map[string]any{"status": 0, "msg": "接口关闭", "data": nil})
			_, _ = w.Write(payload)
			return
		}
		_, _ = w.Write([]byte(wgBody))
	}))
	defer srv.Close()
	stubHuangguoAIPool(t, srv.URL)
	d := huangguoaiFixtureDownloader(srv.URL)
	drama, chapters, err := d.fetchHuangguoAIDetail(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("fallback detail: %v", err)
	}
	if drama.SourceID != "abc123" {
		t.Fatalf("drama = %+v", drama)
	}
	if len(chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(chapters))
	}
	if chapters[0].ID != providerChapterID(sourceHuangguoAI, "abc123", "ep-1") {
		t.Fatalf("chapter keys from HTML: %s / %s", chapters[0].ID, chapters[1].ID)
	}
}

func TestHuangguoaiSearch(t *testing.T) {
	var hits []edgeHit
	srv := httptest.NewServer(edgeEnvelopeHandler(t, nil, &hits, func(path string) (int, string, any) {
		if !strings.HasPrefix(path, "/api/search?") || !strings.Contains(path, "size=20") || !strings.Contains(path, "page=2") || !strings.Contains(path, "q=%E7%94%B5%E6%8A%80") {
			return 0, "搜索路径不符: " + path, nil
		}
		return 1, "", map[string]any{
			"items":      []any{map[string]any{"id": "abc123", "title": "搜索结果"}},
			"pagination": map[string]any{"page": 2, "pages": 5},
		}
	}))
	defer srv.Close()
	stubHuangguoAIPool(t, srv.URL)
	d := huangguoaiFixtureDownloader(srv.URL)
	items, hasMore, err := d.searchHuangguoAI(context.Background(), 2, " 电技 ")
	if err != nil {
		t.Fatalf("searchHuangguoAI: %v", err)
	}
	if len(items) != 1 || items[0].Title != "搜索结果" || items[0].SourceID != "abc123" {
		t.Fatalf("items = %+v", items)
	}
	if !hasMore {
		t.Fatalf("hasMore = false, want true (page 2 < pages 5)")
	}
	if _, _, err := d.searchHuangguoAI(context.Background(), 1, ""); err == nil || !strings.Contains(err.Error(), "黄果搜索关键词无效") {
		t.Fatalf("empty query err = %v", err)
	}
}

func TestHuangguoaiPlayURL(t *testing.T) {
	srv := httptest.NewServer(edgeEnvelopeHandler(t, nil, nil, func(path string) (int, string, any) {
		if path != "/api/videos/abc123/play?ep=3&page_texts=0" {
			return 0, "播放路径不符: " + path, nil
		}
		return 1, "", map[string]any{"video_url": "/media/ep3.m3u8"}
	}))
	defer srv.Close()
	stubHuangguoAIPool(t, srv.URL)
	d := huangguoaiFixtureDownloader(srv.URL)
	url, err := d.fetchHuangguoAIPlayURL(context.Background(), "abc123", 3)
	if err != nil {
		t.Fatalf("fetchHuangguoAIPlayURL: %v", err)
	}
	// normalizeProviderMediaURL 会把 http 基址升级为 https（参照行为），断言 host+path 而非全等
	wantPlay := strings.Replace(srv.URL, "http://", "https://", 1) + "/media/ep3.m3u8"
	if url != wantPlay {
		t.Fatalf("play url = %s, want %s", url, wantPlay)
	}
	if _, err := d.fetchHuangguoAIPlayURL(context.Background(), "abc123", 0); err == nil || !strings.Contains(err.Error(), "黄果集数无效") {
		t.Fatalf("invalid episode err = %v", err)
	}
}

func TestHuangguoaiEdgeCandidatesPool(t *testing.T) {
	previous := huangguoAIBaseMirrors
	t.Cleanup(func() { huangguoAIBaseMirrors = previous; huangguoAIDriftReset() })
	if len(huangguoAIBaseMirrors) < 9 {
		t.Fatalf("base pool = %d mirrors, want >= 9 (ref 8 + drift domain)", len(huangguoAIBaseMirrors))
	}
	huangguoAIDriftReset()
	huangguoAILearnDrift(mustParseURL(t, "https://26d.gitiecgad.cc/"))
	// 生产基址（huangguoai.com）→ 候选含生成域名收割；纯字符串构造，不触网
	d := huangguoaiFixtureDownloader("")
	candidates := d.huangguoAIEdgeCandidates()
	joined := strings.Join(candidates, " ")
	for _, want := range []string{"https://26d.gitiecgad.cc", "https://thu.lbvrhyir.cc", "https://pku.xxbgwypx.cc", "https://huangguoai.com", "https://thu.agdkczeyx.cc"} {
		if !strings.Contains(joined, want+huangguoAIEdgePath) {
			t.Fatalf("candidates missing %s: %v", want, candidates)
		}
	}
	if candidates[0] != "https://26d.gitiecgad.cc"+huangguoAIEdgePath {
		t.Fatalf("drift domain must come first, got %q", candidates[0])
	}
	seen := map[string]bool{}
	generated := 0
	for _, candidate := range candidates {
		if seen[candidate] {
			t.Fatalf("duplicate candidate %q", candidate)
		}
		seen[candidate] = true
		host := strings.TrimPrefix(strings.TrimPrefix(candidate, "https://"), "http://")
		domain := host
		if idx := strings.Index(domain, "/"); idx >= 0 {
			domain = domain[:idx]
		}
		parts := strings.SplitN(domain, ".", 2)
		if len(parts) == 2 && strings.HasPrefix(parts[1], "lbvrhyir.") == false {
			for _, suffix := range huangguoAIMirrorDomains {
				if parts[1] == suffix && !isPoolMirrorHost(t, parts[0]+"."+suffix) {
					generated++
				}
			}
		}
	}
	if generated < 4 {
		t.Fatalf("generated harvest candidates = %d across mirror domains, want >= 4 (pool: %d candidates)", generated, len(candidates))
	}
	if len(candidates) < len(previous)+8 {
		t.Fatalf("candidates = %d, want pool(%d)+generated(8)-dedupe", len(candidates), len(previous))
	}
}

// TestHuangguoAIForkFamilyPoolOrder 固化 2026-11-08 实测结论：同族 fork
// （kuoluoss/zhenguojian，guoapp3 系）的存活域名家族全部在硬镜像段，死域
// huangguoai.fit 不入池；fork 域作配置基址时不注入 sha256 生成域（仅
// huangguoai.com 基址收割生成域），保证池序 301 漂移池→硬镜像→生成域→cfg 不变。
func TestHuangguoAIForkFamilyPoolOrder(t *testing.T) {
	previous := huangguoAIBaseMirrors
	t.Cleanup(func() { huangguoAIBaseMirrors = previous; huangguoAIDriftReset() })
	wantMirrors := []string{
		"https://huangguoai.com",
		"https://thu.lbvrhyir.cc",
		"https://pku.xxbgwypx.cc",
		"https://ttvoij.ediayikma.cc",
		"https://thu.ediayikma.cc",
		"https://pku.ediayikma.cc",
		"https://fdu.ediayikma.cc",
		"https://thu.agdkczeyx.cc",
		"https://26d.gitiecgad.cc",
	}
	if strings.Join(huangguoAIBaseMirrors, " ") != strings.Join(wantMirrors, " ") {
		t.Fatalf("hard mirror pool = %v, want %v", huangguoAIBaseMirrors, wantMirrors)
	}
	if strings.Join(huangguoAIMirrorDomains, " ") != "lbvrhyir.cc xxbgwypx.cc ediayikma.cc agdkczeyx.cc" {
		t.Fatalf("generated-domain suffix pool = %v", huangguoAIMirrorDomains)
	}
	huangguoAIDriftReset()
	d := huangguoaiFixtureDownloader("https://thu.ediayikma.cc")
	candidates := d.huangguoAIEdgeCandidates()
	if len(candidates) != len(wantMirrors) {
		t.Fatalf("fork-base candidates = %v, want exactly the %d hard mirrors", candidates, len(wantMirrors))
	}
	for _, candidate := range candidates {
		root := strings.TrimSuffix(candidate, huangguoAIEdgePath)
		found := false
		for _, mirror := range wantMirrors {
			if root == mirror {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("candidate %q is not a pooled hard mirror (generation must stay off for fork bases)", candidate)
		}
	}
}

func isPoolMirrorHost(t *testing.T, host string) bool {
	t.Helper()
	for _, mirror := range huangguoAIBaseMirrors {
		if strings.Contains(mirror, "://"+host) {
			return true
		}
	}
	return false
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return parsed
}

func TestHuangguoaiExhaustedErrorMarksCooldown(t *testing.T) {
	// 全池网络死 → huangguoAIEdgeFailure（exhausted）并进入冷却（详情/分类接线据此快速回退旧语义）
	var dialed int
	var mu sync.Mutex
	poison := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		dialed++
		mu.Unlock()
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer poison.Close()
	stubHuangguoAIPool(t, poison.URL)
	d := huangguoaiFixtureDownloader(poison.URL)
	err := d.huangguoAIAPI(context.Background(), "/api/videos/abc123", &map[string]any{})
	if err == nil || !huangguoAIEdgeExhausted(err) {
		t.Fatalf("err = %v, want huangguoAIEdgeFailure", err)
	}
	if !strings.Contains(err.Error(), "黄果AI边缘接口请求失败：已尝试") {
		t.Fatalf("error text = %q", err.Error())
	}
	if dialed == 0 {
		t.Fatalf("candidate never dialed")
	}
	if !huangguoAIEdgeInCooldown() {
		t.Fatalf("exhaustion should mark cooldown")
	}
}

func TestHuangguoaiAutoEnvelopeFallsBackToLegacyGET(t *testing.T) {
	// 站点存活但 edge 拒载（实测 {"error":"请求无法处理"} 形态）→ 信封整池失败进冷却，
	// /api/* GET 回退旧直连语义；冷却期内后续调用不再重扫 edge。
	var edgeHits, getHits int
	htmlPage := `<html><head><title>测试剧集</title></head><body><h1>测试剧集</h1>` +
		`<a href="/video/abc123/ep-1/">第1集</a><a href="/video/abc123/ep-2/">第2集</a></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == huangguoAIEdgePath && r.Method == http.MethodPost:
			edgeHits++
			_, _ = w.Write([]byte(`{"error":"请求无法处理"}`))
		case strings.HasPrefix(r.URL.Path, "/api/videos/category/"):
			getHits++
			_, _ = w.Write([]byte(`{"data":[{"id":"abc123","title":"直连分类剧"}]}`))
		default:
			_, _ = w.Write([]byte(htmlPage))
		}
	}))
	defer srv.Close()
	stubHuangguoAIPool(t, srv.URL)
	d := huangguoaiFixtureDownloader(srv.URL)
	ctx := context.Background()
	categoryURL := srv.URL + "/api/videos/category/ai-duanju?sort=hot&page=1&size=24"

	body, err := d.fetchProviderText(ctx, categoryURL, srv.URL+"/")
	if err != nil {
		t.Fatalf("legacy fallback fetch: %v", err)
	}
	if edgeHits != 1 {
		t.Fatalf("edge hits = %d, want 1 (one pool scan)", edgeHits)
	}
	if !strings.Contains(body, "直连分类剧") {
		t.Fatalf("body should come from legacy GET, got %q", body)
	}
	if !huangguoAIEdgeInCooldown() {
		t.Fatalf("pool failure should engage cooldown")
	}

	// 第二次调用：冷却期直接旧语义，不再扫 edge
	if _, err := d.fetchProviderText(ctx, categoryURL, srv.URL+"/"); err != nil {
		t.Fatalf("second fetch during cooldown: %v", err)
	}
	if edgeHits != 1 {
		t.Fatalf("edge hits = %d after cooldown, want still 1", edgeHits)
	}
	if getHits != 2 {
		t.Fatalf("legacy GET hits = %d, want 2", getHits)
	}

	// 详情：冷却期跳过信封，直接 HTML 兜底（等同对齐前行为）
	drama, chapters, err := d.fetchHuangguoAIDetail(ctx, "abc123")
	if err != nil {
		t.Fatalf("detail during cooldown: %v", err)
	}
	if drama.SourceID != "abc123" || len(chapters) != 2 {
		t.Fatalf("detail from HTML = %+v chapters=%d", drama, len(chapters))
	}
	if edgeHits != 1 {
		t.Fatalf("detail should not re-scan edge during cooldown, hits=%d", edgeHits)
	}
}
