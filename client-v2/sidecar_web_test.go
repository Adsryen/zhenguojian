//go:build zgj_sidecar

// sidecar_web_test.go —— web 模式端到端测试（httptest 注入，不起真端口）。
// 覆盖：鉴权缺失拒启、/health、/login 失败/成功、/rpc 无 cookie 401、
// WS 真往返（Ping/__methods）、静态 index.html。与 stdio e2e 同一脚手架
// （临时 ZHENGUO_DATA + native.ResetInitForTest）。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"zhenguojian/client-v2/internal/native"
)

func TestWebAuthEnvRequired(t *testing.T) {
	t.Setenv("ZGJ_WEB_USER", "")
	t.Setenv("ZGJ_WEB_PASS", "")
	if _, err := loadWebAuthEnv(); err == nil {
		t.Fatal("缺 env 必须拒绝启动")
	}
	t.Setenv("ZGJ_WEB_USER", "u")
	t.Setenv("ZGJ_WEB_PASS", "p")
	a, err := loadWebAuthEnv()
	if err != nil || a.User != "u" || a.Pass != "p" {
		t.Fatalf("合法 env 应通过: %v %v", a, err)
	}
}

// TestVideoSegProxy 专测 /video/seg 代理头回灌（P1 实网验收抓到的真 bug：
// 首版 WriteHeader 在 Set 头之前，Content-Range/Content-Length/Content-Type
// 全被静默丢弃——浏览器拿不到总长，时长/seek 全废，Content-Type 退化成 Go
// 按字节嗅探的纯巧合值）。上游用 httptest（默认 127.0.0.1，落 loopback 白名单）。
func TestVideoSegProxy(t *testing.T) {
	// 上游=模拟容器内 loopback 流服务器：支持 Range，媒体头齐全。
	payload := make([]byte, 1<<20)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	mediaCT := "video/mp4"
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/playlist.m3u8" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprintf(w, "#EXTM3U\n#EXTINF:10,\n%s/seg0.ts\n", upstream.URL)
			return
		}
		w.Header().Set("Content-Type", mediaCT)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"seg1"`)
		start, end := int64(0), int64(len(payload)-1)
		if rv := r.Header.Get("Range"); rv != "" {
			// 只支持 bytes=a-b
			spec := strings.TrimPrefix(rv, "bytes=")
			lo, hi, ok := strings.Cut(spec, "-")
			if ok {
				start = parseRangeNum(lo)
				if hi != "" {
					end = parseRangeNum(hi)
				}
			}
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", end-start+1))
		w.WriteHeader(206)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer upstream.Close()

	store := newWebAuthStore()
	tok, err := store.add()
	if err != nil {
		t.Fatal(err)
	}
	h := newVideoSegHandler(store)
	mux := http.NewServeMux()
	mux.Handle("/video/seg", h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	target := upstream.URL + "/v.mp4"
	segURL := srv.URL + "/video/seg?url=" + url.QueryEscape(target)

	// 无 cookie → 401
	resp, err := http.Get(segURL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("无 cookie 应 401，got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 非 loopback 目标 → 403（防 SSRF）
	c := &http.Client{}
	req, _ := http.NewRequest("GET", srv.URL+"/video/seg?url="+url.QueryEscape("http://example.com/x.mp4"), nil)
	req.Header.Set("Cookie", sessionCookie+"="+tok)
	r3, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if r3.StatusCode != 403 {
		t.Fatalf("非 loopback 目标应 403，got %d", r3.StatusCode)
	}
	r3.Body.Close()

	// Range 块：头必须逐项回灌 + 字节一致
	req2, _ := http.NewRequest("GET", segURL, nil)
	req2.Header.Set("Cookie", sessionCookie+"="+tok)
	req2.Header.Set("Range", "bytes=100-199")
	resp2, err := c.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 206 {
		t.Fatalf("应 206，got %d", resp2.StatusCode)
	}
	if got := resp2.Header.Get("Content-Type"); got != mediaCT {
		t.Errorf("Content-Type 回灌失败: %q（首版 bug=字节嗅探/丢失）", got)
	}
	if got := resp2.Header.Get("Content-Range"); got != "bytes 100-199/1048576" {
		t.Errorf("Content-Range 回灌失败: %q（首版 bug=静默丢失→浏览器时长/seek 全废）", got)
	}
	if got := resp2.Header.Get("Content-Length"); got != "100" {
		t.Errorf("Content-Length 回灌失败: %q", got)
	}
	body, _ := io.ReadAll(resp2.Body)
	if len(body) != 100 {
		t.Fatalf("字节数=%d，want 100", len(body))
	}
	for i := 0; i < 100; i++ {
		if body[i] != payload[100+i] {
			t.Fatalf("字节偏移 %d 不一致", i)
		}
	}

	// m3u8：分段地址必须改写成再经代理的相对 URL（hls.js 同源取分段）
	req3, _ := http.NewRequest("GET", srv.URL+"/video/seg?url="+url.QueryEscape(upstream.URL+"/playlist.m3u8"), nil)
	req3.Header.Set("Cookie", sessionCookie+"="+tok)
	resp3, err := c.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	pl, _ := io.ReadAll(resp3.Body)
	if !strings.Contains(string(pl), "/video/seg?url=") {
		t.Errorf("m3u8 分段未改写: %q", string(pl))
	}
	if strings.Contains(string(pl), upstream.URL+"/seg") {
		t.Errorf("m3u8 仍残留 loopback 绝对地址: %q", string(pl))
	}
}

func parseRangeNum(s string) int64 {
	var n int64
	if s == "" {
		return 0
	}
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil {
		return 0
	}
	return n
}

// newWebTestApp 起 buildWebMux + httptest（真 NewApp 全链，同 stdio e2e）。
func newWebTestApp(t *testing.T, user, pass, staticDir string) *httptest.Server {
	t.Setenv("ZHENGUO_DATA", t.TempDir())
	native.ResetInitForTest()
	t.Cleanup(native.ResetInitForTest)
	t.Setenv("ZGJ_WEB_USER", user)
	t.Setenv("ZGJ_WEB_PASS", pass)

	rt := newSidecarRuntime()
	t.Cleanup(func() { rt.finish() })
	hub := newWSHub()
	store := newWebAuthStore()
	sidecarEventSink = func(name string, data any) {} // 测试内事件不关心

	mux := buildWebMux(webAuthEnv{User: user, Pass: pass}, rt, hub, store, staticDir)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestWebEndpoints(t *testing.T) {
	static := t.TempDir()
	if err := os.WriteFile(static+"/index.html", []byte("<html>zgj-web</html>"), 0644); err != nil {
		t.Fatal(err)
	}
	srv := newWebTestApp(t, "ryen", "s3cret", static)

	// /health 无鉴权
	resp, err := http.Get(srv.URL + "/health")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("/health: %v %v", err, resp)
	}

	// 静态 index
	resp2, err := http.Get(srv.URL + "/")
	if err != nil || resp2.StatusCode != 200 {
		t.Fatalf("/: %v %v", err, resp2)
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp2.Body)
	if !strings.Contains(buf.String(), "zgj-web") {
		t.Fatalf("index.html 内容不对: %q", buf.String())
	}

	// /login 错口令
	resp3, err := http.Post(srv.URL+"/login", "application/json",
		strings.NewReader(`{"user":"ryen","pass":"wrong"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp3.StatusCode != 401 {
		t.Fatalf("错口令应 401，got %d", resp3.StatusCode)
	}

	// /rpc 无 cookie → 401
	wu, _ := url.Parse(srv.URL + "/rpc")
	wu.Scheme = "ws"
	_, _, err = websocket.DefaultDialer.Dial(wu.String(), nil)
	if err == nil {
		t.Fatal("无 cookie 的 WS 握手必须被拒")
	}

	// /login 对口令 → cookie；带 cookie 走 WS 真往返
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp4, err := c.Post(srv.URL+"/login", "application/json",
		strings.NewReader(`{"user":"ryen","pass":"s3cret"}`))
	if err != nil || resp4.StatusCode != 200 {
		t.Fatalf("/login 成功路径: %v %v", err, resp4)
	}
	baseURL, _ := url.Parse(srv.URL)
	cj := jar.Cookies(baseURL)
	if len(cj) == 0 {
		t.Fatal("登录应发 session cookie")
	}
	hdr := http.Header{"Cookie": []string{fmt.Sprintf("%s=%s", sessionCookie, cj[0].Value)}}
	conn, _, err := websocket.DefaultDialer.Dial(wu.String(), hdr)
	if err != nil {
		t.Fatalf("带 cookie 的 WS 握手: %v", err)
	}
	defer conn.Close()

	send := func(id, method string) {
		line, _ := json.Marshal(map[string]any{"id": id, "method": method, "args": []any{}})
		_ = conn.WriteMessage(websocket.TextMessage, line)
	}
	send("1", "Ping")
	send("2", "__methods")

	deadline := time.Now().Add(30 * time.Second)
	gotPing, gotMethods := false, 0
	for time.Now().Before(deadline) && !(gotPing && gotMethods > 0) {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("WS 读: %v", err)
		}
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		var id string
		_ = json.Unmarshal(m["id"], &id)
		switch id {
		case "1":
			var res string
			_ = json.Unmarshal(m["result"], &res)
			if res != "pong" {
				t.Fatalf("Ping 回执=%s", res)
			}
			gotPing = true
		case "2":
			var inner string
			_ = json.Unmarshal(m["result"], &inner)
			var names []string
			_ = json.Unmarshal([]byte(inner), &names)
			gotMethods = len(names)
			if len(names) < 80 {
				t.Fatalf("方法表异常小: %d", len(names))
			}
		}
	}
	if !gotPing || gotMethods == 0 {
		t.Fatal("WS 往返未收齐 Ping/__methods")
	}
}
