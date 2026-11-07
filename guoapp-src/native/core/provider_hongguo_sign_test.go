package core

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// 红果网关只接受参考客户端那套参数：query 里只有设备字段和 _rticket。
// 之前多带一个 ts=<秒> 时，详情/目录仍可用，但取流接口稳定回 200 + 空体，
// 下载因此退到网页兜底拿 360P。这个用例锁住参数集合与签名头。
func TestSignHongguoRequestKeepsReferenceQuery(t *testing.T) {
	query := url.Values{
		"aid": {"8662"}, "app_name": {"novelread"}, "device_platform": {"android"},
		"device_id": {"1079094239834907"}, "iid": {"1079094240101147"},
	}
	body := []byte(`{"biz_param":{"device_level":3}}`)
	request, err := http.NewRequest(http.MethodPost, "https://api5-normal-sinfonlineb.fqnovel.com/novel/player/multi_video_model/v1/?"+query.Encode(), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	now := time.Unix(1793280136, 0)
	signHongguoRequest(request, body, now)

	signed := request.URL.Query()
	if _, ok := signed["ts"]; ok {
		t.Fatalf("签名 query 不应包含 ts 参数: %s", request.URL.RawQuery)
	}
	ticket := signed.Get("_rticket")
	if ticket == "" {
		t.Fatalf("签名 query 缺少 _rticket: %s", request.URL.RawQuery)
	}
	if got := request.Header.Get("X-SS-Req-Ticket"); got != ticket {
		t.Fatalf("X-SS-Req-Ticket=%q 与 _rticket=%q 不一致", got, ticket)
	}
	if got := request.Header.Get("X-Khronos"); got != fmt.Sprint(now.Unix()) {
		t.Fatalf("X-Khronos=%q", got)
	}
	for _, key := range []string{"X-Gorgon", "X-Khronos", "X-Ladon", "X-Argus"} {
		if request.Header.Get(key) == "" {
			t.Fatalf("缺少签名头 %s", key)
		}
	}
	// 参考客户端的取流请求只有四个签名头；多带 Helios/Medusa 会被风控空包。
	for _, key := range []string{"X-Helios", "X-Medusa"} {
		if value := request.Header.Get(key); value != "" {
			t.Fatalf("取流请求不应带 %s（得到 %q）", key, value)
		}
	}
	if got, want := request.Header.Get("X-SS-STUB"), fmt.Sprintf("%X", md5.Sum(body)); got != want {
		t.Fatalf("X-SS-STUB=%q want %q", got, want)
	}
}

func TestSignHongguoRequestKeepsSixHeadersForDetail(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://api5-normal-sinfonlineb.fqnovel.com/novel/player/video_detail/v1/?aid=8662", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	signHongguoRequest(request, []byte(`{"series_id":"1"}`), time.Unix(1793280136, 0))
	for _, key := range []string{"X-Gorgon", "X-Khronos", "X-Ladon", "X-Argus", "X-Helios", "X-Medusa"} {
		if request.Header.Get(key) == "" {
			t.Fatalf("详情接口缺少签名头 %s", key)
		}
	}
}

func TestSignHongguoRequestWithoutBodyOmitsStub(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://api5-normal-sinfonlineb.fqnovel.com/novel/player/video_detail/v1/?aid=8662", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	signHongguoRequest(request, nil, time.Unix(1793280136, 0))
	if got := request.Header.Get("X-SS-STUB"); got != "" {
		t.Fatalf("无请求体时不应带 X-SS-STUB，得到 %q", got)
	}
}
