package core

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestParseHongguoWebCategory(t *testing.T) {
	body := `<!doctype html><html><body>
<a class="pc-card" href="/detail?series_id=7699001781887437849">
  <div><picture><img src="https://img.example/a.webp" alt="妈咪受辱，大小姐回国掀翻全场"></picture></div>
  <div><p class="pc-episode-TGdnks">全57集</p></div>
  <p class="pc-title-l_s3n8">妈咪受辱，大小姐回国掀翻全场</p>
  <div><span class="pc-tag-text-vEpsxC">都市</span><span class="pc-tag-text-vEpsxC">揭露阴谋</span></div>
</a>
<a class="pc-card" href="/detail?series_id=7700000000000000001">
  <img class="pc-img-empty" src="https://cdn.example/empty_play_v3.png" alt="">
  <img src="https://img.example/b.webp" alt="更新中的剧">
  <p class="pc-episode-x">更新至12集</p>
</a>
<nav><a href="?page=2">2</a><a href="?page=34">34</a></nav>
</body></html>`
	genre, ok := hongguoWebGenreOf("real-drama")
	if !ok {
		t.Fatal("genre missing")
	}
	page, err := parseHongguoWebCategory(body, genre, "urban", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items=%d", len(page.Items))
	}
	first := page.Items[0]
	if first.Title != "妈咪受辱，大小姐回国掀翻全场" || first.SourceID != "7699001781887437849" {
		t.Fatalf("unexpected first item: %+v", first)
	}
	if first.EpisodeCount != "57" || first.ReleaseStatus != "finished" {
		t.Fatalf("episode fields: %+v", first)
	}
	if first.CategoryName != "都市" || len(first.Tags) != 2 {
		t.Fatalf("tags: %+v", first.Tags)
	}
	second := page.Items[1]
	if second.ReleaseStatus != "ongoing" || second.EpisodeCount != "12" {
		t.Fatalf("second item: %+v", second)
	}
	if second.Cover != "https://img.example/b.webp" {
		t.Fatalf("placeholder cover leaked: %+v", second.Cover)
	}
	if page.TotalPages != 34 || !page.HasMore {
		t.Fatalf("pagination: %+v", page)
	}
}

func TestHongguoWebCategoryLive(t *testing.T) {
	if os.Getenv("PROBE_BROWSE") == "" {
		t.Skip("PROBE_BROWSE not set")
	}
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	first, err := engine.downloader.fetchHongguoWebCategory(ctx, "real-drama", "urban", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("page1 items=%d totalPages=%d hasMore=%v", len(first.Items), first.TotalPages, first.HasMore)
	for index, drama := range first.Items {
		if index >= 5 {
			break
		}
		t.Logf("  %s | %s | %s | %v", drama.SourceID, drama.Title, drama.EpisodeCount, drama.Tags)
	}
	if len(first.Items) < 10 {
		t.Fatalf("too few items: %d", len(first.Items))
	}
	second, err := engine.downloader.fetchHongguoWebCategory(ctx, "real-drama", "urban", 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("page2 items=%d first=%s", len(second.Items), second.Items[0].Title)
	if len(second.Items) == 0 || second.Items[0].SourceID == first.Items[0].SourceID {
		t.Fatal("page 2 did not advance")
	}
}
