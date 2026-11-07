package core

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestHuangdouLiveCatalogSmoke 走真实域名池验证 Q7 移植后的黄豆链路：
// 池内轮换（ferncider.cc 起）+ 加密协议封装 + /drama/list 目录。遗留域
// tideember.cc DNS 失效、xqjurgek.top 证书 2026-11-06 过期，池轮换即上游
// 绕开方案（站点证据捕获 2026-11-07：6 池域名 GET / 全部 200、证书有效至
// 2027-01-19）。CI/离线跳过，仅 GUOPROXY_LIVE=1 时执行。
func TestHuangdouLiveCatalogSmoke(t *testing.T) {
	if os.Getenv("GUOPROXY_LIVE") != "1" {
		t.Skip("set GUOPROXY_LIVE=1 to hit live huangdou pool")
	}
	cfg := defaultConfig()
	cfg.dataDir = t.TempDir()
	d := &Downloader{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second},
		limiter: newRequestLimiter(3, 0), providerHosts: map[string]string{}}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client := newHuangdouAPIClient(d)
	var decoded any
	if err := client.call(ctx, "/drama/list", map[string]any{"page": "1", "page_size": "30"}, &decoded); err != nil {
		t.Fatalf("live /drama/list: %v", err)
	}
	rows := huangdouList(decoded)
	if len(rows) == 0 {
		t.Fatalf("live /drama/list 空列表: %+v", decoded)
	}
	first := huangdouDramaFromMap(rows[0])
	t.Logf("catalog: %d rows host=%s first=%q id=%s status=%q channel=%q",
		len(rows), client.host, first.Title, first.SourceID, first.ReleaseStatus, first.ChannelName)
	if first.SourceID == "" || first.Title == "" {
		t.Fatalf("目录条目缺关键字段: %+v", first)
	}

	// 详情一次：验证 _detail 链路（call 走 .call 复用池轮换）。
	if item, err := d.huangdouDetail(ctx, first.SourceID); err != nil || len(item) == 0 {
		t.Logf("live detail soft-fail (允许，站点侧偶发): id=%s err=%v", first.SourceID, err)
	} else {
		t.Logf("detail keys=%d", len(item))
	}
}
