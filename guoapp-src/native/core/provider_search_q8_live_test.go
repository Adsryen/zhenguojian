package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// 真网冒烟：默认 SKIP。跑法（需代理）：
//
//	HTTP_PROXY=http://192.168.0.36:17890 HTTPS_PROXY=http://192.168.0.36:17890 \
//	HUANGDOU_LIVE=1 ~/.local/go/bin/go test ./core/ -run 'LiveSearch' -count=1 -v

func q8LiveEnabled(t *testing.T, key string) {
	t.Helper()
	if os.Getenv(key) != "1" {
		t.Skipf("set %s=1 to run this live probe", key)
	}
}

// TestHuangdouLiveSearchDeepLocal 实网验证黄豆「深本地」路径：关键词参数被
// 上游忽略（2026-11-08 侦察结论），搜索必须走自动抓目录+本地匹配，并对
// 侦察时实测存在的剧名「我在女寝有张床」命中。
func TestHuangdouLiveSearchDeepLocal(t *testing.T) {
	q8LiveEnabled(t, "HUANGDOU_LIVE")
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine.downloader.limiter = newRequestLimiter(3, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := engine.nativeCatalog(ctx, nativeInput{Source: sourceHuangdou, Query: "女寝"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("huangdou deep-local live: items=%d local=%v warning=%q", len(result.Items), result.LocalSearch, result.Warning)
	if !result.LocalSearch || !strings.Contains(result.Warning, "暂无在线关键词搜索") {
		t.Fatalf("live result did not take the deep-local branch: %+v", result)
	}
	if len(result.Items) == 0 {
		t.Fatal("live warm search found nothing for a title present in the first page")
	}
}

// TestHuangdouLiveKeywordIgnored 复核「上游静默忽略 keyword 参数」的侦察结论，
// 若某天黄豆上线搜索接口，这个冒烟会先报警。
func TestHuangdouLiveKeywordIgnored(t *testing.T) {
	q8LiveEnabled(t, "HUANGDOU_LIVE")
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var baseline, keyworded any
	client := newHuangdouAPIClient(engine.downloader)
	if err := client.call(ctx, "/drama/list", map[string]any{"page": "1", "page_size": "30"}, &baseline); err != nil {
		t.Fatal(err)
	}
	if err := client.call(ctx, "/drama/list", map[string]any{"page": "1", "page_size": "30", "keyword": "寡妇"}, &keyworded); err != nil {
		t.Fatal(err)
	}
	baselineRows, keywordRows := huangdouList(baseline), huangdouList(keyworded)
	if len(baselineRows) != len(keywordRows) {
		t.Logf("huangdou keyword filter CHANGED row count: baseline=%d keyworded=%d — upstream may have added search", len(baselineRows), len(keywordRows))
		return
	}
	for i := range baselineRows {
		if baselineRows[i]["id"] != keywordRows[i]["id"] {
			t.Logf("huangdou keyword search appears LIVE (order differs at row %d)", i)
			return
		}
	}
	t.Log("confirmed: keyword param still ignored, deep-local remains correct")
}

// TestHuangguoVideoLiveSearchSmoke 实网验证黄果视频在线关键词搜索：
// 2026-11-08 侦察确认 /search?q= 服务端渲染且 fetchProviderText 的 iPhone UA
// 可通过 Cloudflare WAF（桌面 curl UA 拿到 403 拦截页，见 notes.md）。
func TestHuangguoVideoLiveSearchSmoke(t *testing.T) {
	q8LiveEnabled(t, "HUANGGUO_LIVE")
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	items, hasMore, err := engine.downloader.searchHuangguoVideo(ctx, 1, "女寝")
	if err != nil {
		t.Fatal(err)
	}
	if hasMore {
		t.Fatal("search endpoint must not advertise more pages (paging ignored upstream)")
	}
	if len(items) == 0 {
		t.Fatal("live search returned nothing for a title known to exist on site")
	}
	t.Logf("huangguo.video live search: items=%d first=%q id=%s", len(items), items[0].Title, items[0].ID)
	for _, drama := range items {
		if drama.Source != sourceHuangguoVideo || !strings.HasPrefix(drama.ID, sourceHuangguoVideo+":") {
			t.Fatalf("live row not normalized: %+v", drama)
		}
	}
	page2, more2, err := engine.downloader.searchHuangguoVideo(ctx, 2, "女寝")
	if err != nil || more2 || len(page2) != 0 {
		t.Fatalf("page 2 must terminate empty: items=%d more=%v err=%v", len(page2), more2, err)
	}
}

// TestLegacyLiveSearchSmoke 实网验证黄果旧版在线关键词搜索（侦察确认的
// /api/app/playlet/keyword/search + keyword）。
func TestLegacyLiveSearchSmoke(t *testing.T) {
	q8LiveEnabled(t, "HUANGGUO_LIVE")
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	items, hasMore, err := engine.downloader.searchLegacy(ctx, 1, "寡妇")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("live keyword search returned nothing")
	}
	t.Logf("legacy live search: items=%d hasMore=%v first=%q", len(items), hasMore, items[0].Title)
	for _, drama := range items {
		if drama.Source != sourceCloudFront || !strings.HasPrefix(drama.ID, sourceCloudFront+":") {
			t.Fatalf("live row not normalized: %+v", drama)
		}
	}
}
