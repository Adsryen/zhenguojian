package core

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestHongguoPlayerLive 端到端验证：走真实取流路径（含设备注册与新版
// 播放专用 Argus/Ladon），打印拿到的画质与 CENC key。
func TestHongguoPlayerLive(t *testing.T) {
	videoID := os.Getenv("PROBE_VIDEO_ID")
	if videoID == "" {
		videoID = "7648600565701692440"
	}
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	downloader := engine.downloader
	if err := downloader.ensureHongguoIdentity(ctx); err != nil {
		t.Fatalf("identity: %v", err)
	}
	media, err := downloader.resolveHongguoAppMedia(ctx, videoID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	fmt.Printf("RESOLVED video=%s quality=%d def=%s size=%dx%d bitrate=%d cenc=%d url=%s\n",
		videoID, media.Quality, media.Definition, media.Width, media.Height, media.Bitrate,
		len(media.CENCKey), truncate(media.URL, 80))
	if media.Quality >= 1080 {
		fmt.Println("*** 1080P RESTORED ***")
	}
	// 同账号再取一集，验证不是偶然命中。
	if second := os.Getenv("PROBE_VIDEO_ID2"); second != "" {
		media2, err2 := downloader.resolveHongguoAppMedia(ctx, second)
		if err2 != nil {
			t.Fatalf("resolve2: %v", err2)
		}
		fmt.Printf("RESOLVED2 video=%s quality=%d def=%s cenc=%d\n", second, media2.Quality, media2.Definition, len(media2.CENCKey))
	}
}
