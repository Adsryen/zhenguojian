package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizePrefix(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"  ":                     "",
		"https://x.example/":     "https://x.example/",
		"https://x.example":      "https://x.example/",
		"x.example":              "https://x.example/",
		"http://y.example:8080/": "http://y.example:8080/",
	}
	for in, want := range cases {
		if got := NormalizePrefix(in); got != want {
			t.Errorf("NormalizePrefix(%q)=%q want %q", in, got, want)
		}
	}
}

func TestChainCandidatesOrder(t *testing.T) {
	raw := "https://api.example.com/r"
	got := ChainCandidates(raw, "https://user.mirror/")
	want := []string{
		"https://user.mirror/" + raw,
		raw,
		"https://gh-proxy.com/" + raw,
		"https://ghproxy.net/" + raw,
		"https://mirror.ghproxy.com/" + raw,
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("candidates=%v\nwant %v", got, want)
	}
	got = ChainCandidates(raw, "")
	if got[0] != raw || len(got) != 4 {
		t.Errorf("empty prefix must start with direct, len 4: %v", got)
	}
}

// 三失败一直连成：候选 1-3 分别 404 / 拒连 / 500，第 4 个成功。
func TestFetchJSONThreeFailThenSuccess(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("missing Accept header")
		}
		w.Write([]byte(`[{"tag_name":"v2.0.0"}]`))
	}))
	defer ok.Close()
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	refused := httptest.NewServer(nil)
	refusedURL := refused.URL
	refused.Close() // 制造拒连
	srv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv500.Close()

	urls := []string{notFound.URL + "/x", refusedURL, srv500.URL, ok.URL}
	body, err := FetchJSON(context.Background(), ok.Client(), urls, 0)
	if err != nil {
		t.Fatalf("FetchJSON should succeed on 4th candidate: %v", err)
	}
	if !strings.Contains(string(body), "v2.0.0") {
		t.Errorf("body=%s", body)
	}
}

func TestFetchJSONAllFailAggregates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := FetchJSON(context.Background(), srv.Client(), []string{srv.URL + "/1", srv.URL + "/2"}, 0)
	if err == nil || !strings.Contains(err.Error(), "全部候选失败") {
		t.Fatalf("want aggregated error, got %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("error should list per-candidate reason: %v", err)
	}
}

func TestFetchJSONEmptyChain(t *testing.T) {
	if _, err := FetchJSON(context.Background(), nil, nil, 0); err == nil {
		t.Error("empty chain should error")
	}
}
