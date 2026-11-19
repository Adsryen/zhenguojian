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
