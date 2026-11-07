package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestHongguoMultiEpisodeLive 取同一部剧的多集，确认新版播放签名稳定。
func TestHongguoMultiEpisodeLive(t *testing.T) {
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	d := engine.downloader
	if err := d.ensureHongguoIdentity(ctx); err != nil {
		t.Fatalf("identity: %v", err)
	}
	entry, err := d.hongguoAppDetail(ctx, "7648598645096320024")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	fmt.Printf("drama %s eps=%d\n", entry.Drama.Title, len(entry.Chapters))
	ok := 0
	for i, ch := range entry.Chapters {
		if i >= 3 {
			break
		}
		vid := strings.TrimPrefix(ch.VideoURL, "hongguo-cenc://")
		media, err := d.resolveHongguoAppMedia(ctx, vid)
		if err != nil {
			fmt.Printf("ep%d %s ERR %v\n", i+1, vid, err)
			continue
		}
		fmt.Printf("ep%d %s quality=%d def=%s size=%dx%d cenc=%d\n", i+1, vid, media.Quality, media.Definition, media.Width, media.Height, len(media.CENCKey))
		if media.Quality >= 1080 && len(media.CENCKey) == 16 {
			ok++
		}
	}
	if ok == 0 {
		t.Fatal("no 1080p episode")
	}
	fmt.Printf("*** 1080P OK on %d episodes ***\n", ok)
}
