package core

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestHongguoLandpageComicSeriesLive 守住上游 2026-11 的场景迁移：漫剧目录
// POST 被静默拒绝（HTTP 200+空体），必须靠 GET 回落出数。默认跳过，
// ZHENGUO_LIVE=1 时真打网关。
func TestHongguoLandpageComicSeriesLive(t *testing.T) {
	if os.Getenv("ZHENGUO_LIVE") == "" && os.Getenv("GUOPROXY_LIVE") == "" {
		t.Skip("set ZHENGUO_LIVE=1")
	}
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	d := engine.downloader
	if err := d.ensureHongguoIdentity(ctx); err != nil {
		t.Fatalf("identity: %v", err)
	}
	dramas, err := d.fetchHongguoAppCatalogCategory(ctx, "comic_series")
	if err != nil {
		t.Fatalf("comic_series: %v", err)
	}
	if len(dramas) == 0 {
		t.Fatal("comic_series empty")
	}
	t.Logf("comic_series items=%d first=%s", len(dramas), dramas[0].Title)
}
