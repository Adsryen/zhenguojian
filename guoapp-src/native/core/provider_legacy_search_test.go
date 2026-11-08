package core

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// legacyFixtureDecryptParams 解开 fetchAPI GET 请求里的 ?data= AES 参数，
// 与 legacyFixtureLogin 的解密路径一致。
func legacyFixtureDecryptParams(t *testing.T, request *http.Request) map[string]string {
	t.Helper()
	encrypted, err := base64.StdEncoding.DecodeString(request.URL.Query().Get("data"))
	if err != nil || len(encrypted) == 0 || len(encrypted)%16 != 0 {
		t.Fatalf("invalid encrypted params: err=%v len=%d", err, len(encrypted))
	}
	block, err := aes.NewCipher([]byte(fixtureLegacyProtocol.ParamKey))
	if err != nil {
		t.Fatal(err)
	}
	cipher.NewCBCDecrypter(block, []byte(fixtureLegacyProtocol.ParamIV)).CryptBlocks(encrypted, encrypted)
	plain, err := pkcs7Unpad(encrypted, 16)
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]string
	if err := json.Unmarshal(plain, &params); err != nil {
		t.Fatalf("params payload: %v", err)
	}
	return params
}

func legacySearchDownloader(t *testing.T, transport sourceFixtureTransport) *Downloader {
	t.Helper()
	d := legacyFixtureDownloader(t, transport)
	d.cfg.Token = "configured-session"
	d.cfg.InterfaceKey, d.cfg.ParamKey, d.cfg.ParamIV = fixtureLegacyProtocol.InterfaceKey, fixtureLegacyProtocol.ParamKey, fixtureLegacyProtocol.ParamIV
	return d
}

func TestLegacySearchSendsKeywordAndNormalizesRows(t *testing.T) {
	var calls atomic.Int32
	d := legacySearchDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.URL.Path != "/api/app/playlet/keyword/search" {
			t.Errorf("unexpected endpoint %s", request.URL.Path)
		}
		params := legacyFixtureDecryptParams(t, request)
		if params["keyword"] != "寡妇" || params["pageNumber"] != "2" || params["pageSize"] != "3" {
			t.Errorf("keyword search params wrong: %v", params)
		}
		return legacyFixtureResponse(request, map[string]any{"list": []Drama{
			{ID: "kw-1", Title: "我在寡妇村当老师", Category: "都市", CoverURL: "https://cover.example/1.jpg"},
			{ID: "kw-1", Title: "重复行应被去重"},
			{ID: "", Title: "无 ID 行应被丢弃"},
			{ID: "kw-2", Title: "寡妇门前", Cover: "//cover.example/2.jpg"},
		}}), nil
	})
	items, hasMore, err := d.searchLegacy(context.Background(), 2, " 寡妇 ")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !hasMore {
		t.Fatalf("items=%d hasMore=%v", len(items), hasMore)
	}
	if items[0].Source != sourceCloudFront || items[0].SourceID != "kw-1" || items[0].ID != providerDramaID(sourceCloudFront, "kw-1") {
		t.Fatalf("identity normalization failed: %+v", items[0])
	}
	if items[0].ChannelName != "黄果旧版" || items[0].CategoryName != "都市" {
		t.Fatalf("channel/category: %+v", items[0])
	}
	if address, _ := items[0].Cover.(string); address != "https://cover.example/1.jpg" {
		t.Fatalf("cover: %v", items[0].Cover)
	}
	if address, _ := items[1].Cover.(string); address != "https://cover.example/2.jpg" {
		t.Fatalf("protocol-relative cover not resolved: %v", items[1].Cover)
	}
}

func TestLegacySearchRejectsInvalidQuery(t *testing.T) {
	for _, query := range []string{"", "   ", strings.Repeat("剧", 300)} {
		items, hasMore, err := searchLegacyWithNoCalls(t, query)
		if err == nil || items != nil || hasMore {
			t.Fatalf("query %q accepted: items=%v hasMore=%v err=%v", query[:min(len(query), 8)], items, hasMore, err)
		}
	}
}

func searchLegacyWithNoCalls(t *testing.T, query string) ([]Drama, bool, error) {
	t.Helper()
	var calls atomic.Int32
	d := legacySearchDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return legacyFixtureResponse(request, map[string]any{"list": []Drama{}}), nil
	})
	items, hasMore, err := d.searchLegacy(context.Background(), 1, query)
	if calls.Load() != 0 {
		t.Fatalf("invalid query still hit the network %d times", calls.Load())
	}
	return items, hasMore, err
}

func TestLegacySearchPropagatesAPIError(t *testing.T) {
	var calls atomic.Int32
	d := legacySearchDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return sourceFixtureResponse(request, 200, `{"code":4001,"hash":false,"msg":"parameters error"}`), nil
	})
	items, hasMore, err := d.searchLegacy(context.Background(), 1, "寡妇")
	if err == nil || !strings.Contains(err.Error(), "4001") {
		t.Fatalf("want 4001 api error, got items=%d hasMore=%v err=%v", len(items), hasMore, err)
	}
	if hasMore {
		t.Fatal("failed search must not claim more pages")
	}
}

func TestLegacySearchLastShortPageEndsPagination(t *testing.T) {
	var calls atomic.Int32
	d := legacySearchDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return legacyFixtureResponse(request, map[string]any{"list": []Drama{{ID: "kw-9", Title: "短页样本"}}}), nil
	})
	items, hasMore, err := d.searchLegacy(context.Background(), 1, "短页")
	if err != nil || len(items) != 1 || hasMore {
		t.Fatalf("items=%d hasMore=%v err=%v", len(items), hasMore, err)
	}
}
