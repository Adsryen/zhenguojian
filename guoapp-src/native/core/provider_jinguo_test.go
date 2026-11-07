package core

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func jinguoFixtureParams(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	encoded := request.URL.Query().Get("data")
	if request.Method == http.MethodPost {
		var body struct {
			Data string `json:"data"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil {
			t.Error("encrypted body is missing")
		}
		encoded = body.Data
	}
	if encoded == "" {
		return nil
	}
	encrypted, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(encrypted)%16 != 0 {
		t.Errorf("invalid encrypted params: %q", encoded)
		return nil
	}
	block, _ := aes.NewCipher([]byte(jinguoProtocol.ParamKey))
	cipher.NewCBCDecrypter(block, []byte(jinguoProtocol.ParamIV)).CryptBlocks(encrypted, encrypted)
	plain, err := pkcs7Unpad(encrypted, 16)
	var params map[string]any
	if err != nil || json.Unmarshal(plain, &params) != nil {
		t.Errorf("params were not encrypted with the jinguo key")
	}
	return params
}

func TestJinguoCatalogSearchDetailAndSessionReuse(t *testing.T) {
	var logins atomic.Int32
	var searched string
	transport := sourceFixtureTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "d1wyuxwdrlykj1.cloudfront.net" {
			t.Errorf("request left the jinguo deployment: %s", request.URL)
		}
		params := jinguoFixtureParams(t, request)
		if request.URL.Path != "/api/app/mine/login/h5" && request.Header.Get("Authorization") != "jinguo-session" {
			t.Errorf("%s lost its session", request.URL.Path)
		}
		if !strings.Contains(request.Header.Get("X-User-Agent"), "BuildID=com.hgaidj.ozThree;") {
			t.Error("jinguo requests must identify as the jinguo app")
		}
		switch request.URL.Path {
		case "/api/app/mine/login/h5":
			logins.Add(1)
			device, _ := params["devID"].(string)
			if !jinguoDeviceIDFormat.MatchString(device) || !strings.Contains(request.Header.Get("X-User-Agent"), "DevID="+device+";") {
				t.Errorf("login identity is inconsistent: %v", params)
			}
			return legacyFixtureResponse(request, map[string]string{"token": "jinguo-session"}), nil
		case "/api/app/playlet-tab/all":
			return legacyFixtureResponse(request, []map[string]any{{"id": "6a928dfedb2cc815f75d7213", "name": "热播", "moduleType": 1}, {"id": "bad id", "name": "坏分类"}}), nil
		case "/api/app/playlet/home/tab/6a928dfedb2cc815f75d7213":
			if params["tabSortType"] != "1" || params["pageNumber"] != "1" {
				t.Errorf("tab params changed: %v", params)
			}
			return legacyFixtureResponse(request, map[string]any{"list": []map[string]any{
				{"id": "6aa409d8d7dd5abdc481cbb1", "title": "禁果样本", "summary": "简介", "cover": "cf230705/image/a/b.gif", "totalEpisode": 3, "tags": []any{"6a92a1b2c3d4e5f60718293a"}, "tagNames": []any{"都市"}},
				{"id": "6aa409d8d7dd5abdc481cbb1", "title": "重复条目"},
				{"id": "", "title": "坏条目"},
			}}), nil
		case "/api/app/playlet/keyword/search":
			searched, _ = params["keyword"].(string)
			return legacyFixtureResponse(request, map[string]any{"list": nil}), nil
		case "/api/app/playlet/detail/6aa409d8d7dd5abdc481cbb1":
			return legacyFixtureResponse(request, map[string]any{"id": "6aa409d8d7dd5abdc481cbb1", "title": "禁果样本", "cover": "cf230705/image/a/b.gif", "totalEpisode": 3, "chapters": []map[string]any{
				{"id": "c2", "currentEpisode": 2, "videoUrl": "", "canPlay": false, "accessType": "coin"},
				{"id": "c1", "currentEpisode": 1, "videoUrl": "JHA-213/tk1/abc.m3u8", "canPlay": true, "accessType": "free"},
			}}), nil
		default:
			t.Errorf("unexpected request: %s", request.URL.Path)
			return nil, errors.New("unexpected endpoint")
		}
	})
	d := sourceFixtureDownloader(t, transport)
	categories, err := d.fetchJinguoCategories(context.Background())
	if err != nil || len(categories) != 3 || categories[2].ID != "tab:6a928dfedb2cc815f75d7213" || !validJinguoCategory(categories[2].ID) || validJinguoCategory("tab:bad id") {
		t.Fatalf("categories: %+v %v", categories, err)
	}
	items, _, err := d.fetchJinguoCatalogPage(context.Background(), 1, "", "")
	if err != nil || len(items) != 1 {
		t.Fatalf("catalog: %+v %v", items, err)
	}
	drama := items[0]
	if drama.ID != "jinguo:6aa409d8d7dd5abdc481cbb1" || drama.Source != jinguoSource || drama.CategoryName != "热播" || drama.Desc != "简介" ||
		drama.Cover != "https://zzzznnn.lkkwip.cn/cf230705/image/a/b.gif" || len(drama.Tags) != 1 || drama.Tags[0] != "都市" {
		t.Fatalf("drama parsed incorrectly: %+v", drama)
	}
	if items, more, err := d.fetchJinguoCatalogPage(context.Background(), 1, "", "  禁果 "); err != nil || len(items) != 0 || more || searched != "禁果" {
		t.Fatalf("search: %+v %t %v %q", items, more, err, searched)
	}
	_, chapters, err := d.fetchJinguoDetail(context.Background(), "6aa409d8d7dd5abdc481cbb1")
	if err != nil || len(chapters) != 2 || chapters[0].VIP || !chapters[1].VIP || chapters[0].Source != jinguoSource || chapters[0].VideoURL != "JHA-213/tk1/abc.m3u8" {
		t.Fatalf("chapters: %+v %v", chapters, err)
	}
	if _, err := d.resolveJinguoMedia(context.Background(), Task{Chapter: chapters[1]}); err == nil || !strings.Contains(err.Error(), "解锁") {
		t.Fatalf("paid episode was not refused: %v", err)
	}
	restarted := sourceFixtureDownloader(t, transport)
	restarted.cfg.dataDir = d.cfg.dataDir
	if _, _, err := restarted.jinguoClient().credentials(context.Background()); err != nil || logins.Load() != 1 {
		t.Fatalf("restart did not reuse the guest session: logins=%d %v", logins.Load(), err)
	}
}

func TestJinguoExpiredSessionRefreshesPlaylistOnce(t *testing.T) {
	var logins atomic.Int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/app/mine/login/h5":
			return legacyFixtureResponse(request, map[string]string{"token": "fresh-" + string(rune('0'+logins.Add(1)))}), nil
		case "/api/app/vid/h5/m3u8/JHA-213/tk1/a b.m3u8":
			if request.URL.EscapedPath() != "/api/app/vid/h5/m3u8/JHA-213/tk1/a%20b.m3u8" {
				t.Errorf("playlist path escaped incorrectly: %s", request.URL.EscapedPath())
			}
			if request.URL.Query().Get("token") == "fresh-1" {
				return sourceFixtureResponse(request, 200, `{"code":5005,"msg":"no token"}`), nil
			}
			return sourceFixtureResponse(request, 200, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"/api/app/vid/sec\"\n#EXTINF:5,\nhttps://cdn.example.test/0.ts\n#EXT-X-ENDLIST\n"), nil
		default:
			t.Errorf("unexpected request: %s", request.URL)
			return nil, errors.New("unexpected endpoint")
		}
	})
	media, err := d.resolveJinguoMedia(context.Background(), Task{Chapter: Chapter{Source: jinguoSource, VideoURL: "/JHA-213/tk1/a b.m3u8"}})
	if err != nil || logins.Load() != 2 || !strings.Contains(media.Playlist, "/api/app/vid/sec") || media.HLSKey != nil {
		t.Fatalf("playlist: %+v logins=%d %v", media, logins.Load(), err)
	}
	if media.Duration != 5*time.Second {
		t.Fatalf("duration: %v", media.Duration)
	}
	for _, bad := range []string{"../x.m3u8", "https://evil.test/x.m3u8", "a/./b.m3u8", "a.m3u8?x=1"} {
		if _, ok := jinguoPlaylistPath(bad); ok {
			t.Fatalf("unsafe video path accepted: %s", bad)
		}
	}
	def, registered := providerSourceDef(jinguoSource)
	if canonicalProviderSource("jinguo") != jinguoSource || !registered || !def.Implemented || !isHuangguoProviderSource(jinguoSource) {
		t.Fatalf("jinguo source registration: %+v %v", def, registered)
	}
	for _, domain := range []string{"d1wyuxwdrlykj1.cloudfront.net", "hghts.rlkfus.cn"} {
		if got := canonicalProviderSource(domain); got != jinguoSource {
			t.Fatalf("domain alias %s resolves to %q", domain, got)
		}
	}
}

func TestJinguoFailsOverToBackupLineAndKeepsIt(t *testing.T) {
	var primaryCalls, backupCalls atomic.Int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "d1wyuxwdrlykj1.cloudfront.net":
			primaryCalls.Add(1)
			return sourceFixtureResponse(request, http.StatusBadGateway, "bad gateway"), nil
		case "hghts.rlkfus.cn":
			backupCalls.Add(1)
		default:
			t.Errorf("request left the jinguo deployment: %s", request.URL)
			return nil, errors.New("unexpected host")
		}
		switch request.URL.Path {
		case "/api/app/mine/login/h5":
			return legacyFixtureResponse(request, map[string]string{"token": "shared-session"}), nil
		case "/api/app/playlet-tab/all":
			return legacyFixtureResponse(request, []Tab{{ID: "6a928dfedb2cc815f75d7213", Name: "热播"}}), nil
		case "/api/app/playlet/detail/missing":
			return sourceFixtureResponse(request, 200, `{"code":51021,"msg":"not found"}`), nil
		default:
			t.Errorf("unexpected request: %s", request.URL.Path)
			return nil, errors.New("unexpected endpoint")
		}
	})
	if _, err := d.fetchJinguoCategories(context.Background()); err != nil {
		t.Fatal(err)
	}
	if primaryCalls.Load() != 1 || backupCalls.Load() != 2 {
		t.Fatalf("failover did not pin the backup line: primary=%d backup=%d", primaryCalls.Load(), backupCalls.Load())
	}
	var detail json.RawMessage
	err := d.jinguoClient().get(context.Background(), "/api/app/playlet/detail/missing", nil, &detail)
	var apiErr *jinguoAPIError
	if !errors.As(err, &apiErr) || apiErr.code != "51021" || primaryCalls.Load() != 1 || backupCalls.Load() != 3 {
		t.Fatalf("business error switched lines: %v primary=%d backup=%d", err, primaryCalls.Load(), backupCalls.Load())
	}
}

// jinguoFixtureEnvelope encrypts a plaintext payload exactly the way the live
// jinguo server does (mirror of decryptResponse's key derivation, live
// verified 2026-11-07), so tests can exercise the hash=true path offline with
// the real protocol constants.
func jinguoFixtureEnvelope(t *testing.T, plain []byte) string {
	t.Helper()
	salt := bytes.Repeat([]byte{0xAB}, 12)
	r := append(append([]byte{}, jinguoProtocol.InterfaceKey...), salt...)
	l := len(r) >> 1
	s := sha256Bytes(r)[8:24]
	c := append(append([]byte{}, s...), r[:l]...)
	a := append(append([]byte{}, r[l:]...), s...)
	d := sha256Bytes(c)
	f := sha256Bytes(a)
	key := append(append(append([]byte{}, d[:8]...), f[8:24]...), d[24:]...)
	iv := append(append(append([]byte{}, f[:4]...), d[12:20]...), f[28:]...)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	padded := pkcs7Pad(plain, block.BlockSize())
	sealed := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(sealed, padded)
	return base64.StdEncoding.EncodeToString(append(salt, sealed...))
}

func TestJinguoEnvelopeRoundTrip(t *testing.T) {
	var logins atomic.Int32
	payload := map[string]any{"list": []map[string]any{{"id": "6aa409d8d7dd5abdc481cbb1", "title": "加密样本", "summary": "信封"}}}
	plain, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	transport := sourceFixtureTransport(func(request *http.Request) (*http.Response, error) {
		params := jinguoFixtureParams(t, request) // proves the encryptParam -> server-decrypt direction
		switch request.URL.Path {
		case "/api/app/mine/login/h5":
			if params["devID"] == nil {
				t.Error("login params missing")
			}
			logins.Add(1)
			body := `{"code":200,"hash":true,"data":"` + jinguoFixtureEnvelope(t, []byte(`{"token":"envelope-token"}`)) + `"}`
			return sourceFixtureResponse(request, 200, body), nil
		case "/api/app/playlet/keyword/search":
			if params["keyword"] != "禁果信封" {
				t.Errorf("search keyword lost: %v", params)
			}
			body := `{"code":200,"hash":true,"data":"` + jinguoFixtureEnvelope(t, plain) + `"}`
			return sourceFixtureResponse(request, 200, body), nil
		default:
			t.Errorf("unexpected request: %s", request.URL.Path)
			return nil, errors.New("unexpected endpoint")
		}
	})
	d := sourceFixtureDownloader(t, transport)
	items, _, err := d.fetchJinguoCatalogPage(context.Background(), 1, "", "禁果信封")
	if err != nil || len(items) != 1 || items[0].Title != "加密样本" || items[0].Desc != "信封" || items[0].Source != jinguoSource {
		t.Fatalf("envelope roundtrip: %+v %v", items, err)
	}
	if logins.Load() != 1 {
		t.Fatalf("logins=%d", logins.Load())
	}
}

// TestJinguoLiveSmoke hits the real jinguo deployment (both lines are probed
// implicitly through failover). Run manually with: JINGUO_LIVE=1 go test
// ./core/ -run TestJinguoLiveSmoke -count=1 -v
func TestJinguoLiveSmoke(t *testing.T) {
	if os.Getenv("JINGUO_LIVE") != "1" {
		t.Skip("set JINGUO_LIVE=1 to run the live jinguo smoke test")
	}
	d := &Downloader{
		cfg:           Config{dataDir: t.TempDir(), Retries: 1, PageSize: 5, MaxPagesPerSort: 1},
		client:        &http.Client{Timeout: 30 * time.Second},
		limiter:       newRequestLimiter(3, 0),
		providerHosts: map[string]string{},
	}
	ctx := context.Background()
	categories, err := d.fetchJinguoCategories(ctx)
	if err != nil || len(categories) < 3 {
		t.Fatalf("categories: %v %+v", err, categories)
	}
	fmt.Printf("[live] categories=%d first-tab=%s\n", len(categories), categories[2].ID)
	items, _, err := d.fetchJinguoCatalogPage(ctx, 1, jinguoChooseCategory, "")
	if err != nil || len(items) == 0 {
		t.Fatalf("choose catalog: %+v %v", items, err)
	}
	fmt.Printf("[live] choose n=%d first=%s/%s\n", len(items), items[0].ID, items[0].DisplayTitle())
	searched, _, err := d.fetchJinguoCatalogPage(ctx, 1, "", "总裁")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	fmt.Printf("[live] search n=%d\n", len(searched))
	detail, chapters, err := d.fetchJinguoDetail(ctx, items[0].SourceID)
	if err != nil || len(chapters) == 0 || detail.ID != items[0].ID {
		t.Fatalf("detail: %+v n=%d %v", detail, len(chapters), err)
	}
	fmt.Printf("[live] detail %q chapters=%d\n", detail.DisplayTitle(), len(chapters))
	for _, chapter := range chapters {
		if chapter.VIP {
			continue
		}
		media, err := d.resolveJinguoMedia(ctx, Task{Chapter: chapter})
		if err != nil {
			t.Fatalf("resolve %s: %v", chapter.ID, err)
		}
		if !strings.HasPrefix(media.Playlist, "#EXTM3U") {
			t.Fatalf("playlist %q", truncate(media.Playlist, 80))
		}
		fmt.Printf("[live] playlist ok url=%s... duration=%s\n", truncate(media.URL, 96), media.Duration)
		return
	}
	fmt.Println("[live] no free episode in sample; playback path untested live")
}
