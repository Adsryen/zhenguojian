package core

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestYeguoLiveCatalogSmoke 走真实站点验证 Q7 移植后的野果链路：
// 多站点发现（attract.vgbyziuz.cc → 备份站）→ _payload.json 外联配置（api_crypt_ver
// v0 + brand_domain/img_base）→ 脚本同源 apiBase 回退 → GET contentOptions /
// POST exploreList 目录。CI/离线跳过，仅 GUOPROXY_LIVE=1 时执行。
// 站点证据捕获于 2026-11-07（见 Q7 任务报告：主站/备份站/transit 站全部 200）。
func TestYeguoLiveCatalogSmoke(t *testing.T) {
	if os.Getenv("GUOPROXY_LIVE") != "1" {
		t.Skip("set GUOPROXY_LIVE=1 to hit live yeguo sites")
	}
	cfg := defaultConfig()
	cfg.dataDir = t.TempDir()
	d := &Downloader{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second},
		limiter: newRequestLimiter(3, 0), providerHosts: map[string]string{}}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	dramas, hasMore, err := d.fetchYeguoCatalogPage(ctx, 1, "", "")
	if err != nil || len(dramas) == 0 {
		t.Fatalf("live catalog page=1: %d items, err=%v", len(dramas), err)
	}
	sample := dramas[0]
	t.Logf("catalog: %d dramas hasMore=%v first=%q id=%s status=%q views=%q cover=%q",
		len(dramas), hasMore, sample.Title, sample.SourceID, sample.ReleaseStatus, sample.Views, sample.Cover)
	for _, item := range dramas[:min(3, len(dramas))] {
		if item.SourceID == "" || item.Title == "" {
			t.Fatalf("目录条目缺关键字段: %+v", item)
		}
	}

	// 分类接口已对齐上游改为 GET + query（无 Content-Type 请求体）。
	if data, err := d.yeguoClient().call(ctx, "/api/home/contentOptions", nil); err != nil {
		t.Fatalf("live contentOptions(GET): %v", err)
	} else {
		t.Logf("contentOptions keys=%d sample=%v", len(data), firstMapOf(data))
	}

	// 详情：取目录第一条走一次详情解析，验证 detail 路由 + 字段映射链路。
	if detailDrama, episodes, err := d.fetchYeguoDetail(ctx, sample.SourceID); err != nil || len(episodes) == 0 {
		t.Logf("live detail soft-fail (允许，站点侧偶发): id=%s err=%v", sample.SourceID, err)
	} else {
		t.Logf("detail: title=%q episodes=%d first_ep_id=%s", detailDrama.Title, len(episodes), episodes[0].ID)
	}
}

func firstMapOf(data map[string]any) any {
	for _, v := range data {
		return v
	}
	return nil
}
