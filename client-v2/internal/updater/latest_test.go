package updater

// D1 主路径（latest.json + releases.atom）单测：atom 解析、契约校验、
// 通道选择、候选链，以及防回归钉子——检查流程在走主路径时不得有任何
// 请求打到 api.github.com（只有兜底分支才允许，见 discoverRelease）。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---------- fixtures ----------

// fakeAtomFeed 按 GitHub releases.atom 实测形态生成 feed：feed 级 id/link、
// entry 的 Repository/<数字>/<tag> id、tag 页 link、转义 HTML content、bot 作者。
func fakeAtomFeed(tags ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:media="http://search.yahoo.com/mrss/" xml:lang="en-US">
  <id>tag:github.com,2008:https://github.com/Adsryen/zhenguojian/releases</id>
  <link type="text/html" rel="alternate" href="https://github.com/Adsryen/zhenguojian/releases"/>
  <link type="application/atom+xml" rel="self" href="https://github.com/Adsryen/zhenguojian/releases.atom"/>
  <title>Release notes from zhenguojian</title>
  <updated>2026-11-15T04:14:13Z</updated>
`)
	for _, tag := range tags {
		fmt.Fprintf(&b, "  <entry>\n    <id>tag:github.com,2008:Repository/1450138827/%s</id>\n", tag)
		fmt.Fprintf(&b, "    <updated>2026-11-15T04:17:29Z</updated>\n")
		fmt.Fprintf(&b, `    <link rel="alternate" type="text/html" href="https://github.com/Adsryen/zhenguojian/releases/tag/%s"/>`+"\n", tag)
		fmt.Fprintf(&b, "    <title>%s</title>\n", tag)
		b.WriteString(`    <content type="html">&lt;h2&gt;本 Release 资产说明&lt;/h2&gt;&lt;p&gt;下载 &lt;code&gt;zhenGuoJian-v2.exe&lt;/code&gt; &#47; 或 app.zip &amp; runtime.zip；&lt;a href=&quot;https://github.com/Adsryen/zhenguojian/releases/tag/v9.9.99&quot;&gt;正文伪装链接&lt;/a&gt;&lt;/p&gt;</content>`)
		b.WriteString("\n    <author><name>github-actions\\[bot]</name></author>\n  </entry>\n")
	}
	b.WriteString("</feed>")
	return b.String()
}

// lmSHA 64 位十六进制样本（4×16）。
const lmSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// lmManifest 契约形态的 latest.json。
func lmManifest(tag, version string, prerelease bool, schema int) string {
	return fmt.Sprintf(`{"schema":%d,"generatedAt":"2026-11-15T12:00:00Z","tag":%q,"version":%q,"prerelease":%v,
"notes":"发布页正文摘要","assets":{
 "exe":{"name":"zhenGuoJian-v2.exe","size":21000000,"sha256":%q,"url":"https://github.com/Adsryen/zhenguojian/releases/download/%s/zhenGuoJian-v2.exe"},
 "appZip":{"name":"zhenGuoJian-v2-app.zip","size":8400000,"sha256":%q},
 "runtimeZip":{"name":"zhenGuoJian-v2-runtime.zip","size":145000000,"sha256":%q},
 "buildInfo":{"name":"build-info.json","size":120}}}`,
		schema, tag, version, prerelease, lmSHA, tag, lmSHA, lmSHA)
}

// ---------- atom 解析 ----------

func TestLatestTagFromAtomTenEntries(t *testing.T) {
	feed := fakeAtomFeed(
		"v2.0.0-alpha.29", "v2.0.0-alpha.28", "v2.0.0-alpha.27", "v2.0.0-alpha.26", "v2.0.0-alpha.25",
		"v2.0.0-alpha.24", "v2.0.0-alpha.23", "v2.0.0-alpha.22", "v2.0.0-alpha.21", "v2.0.0-alpha.20")
	if strings.Count(feed, "<entry>") != 10 {
		t.Fatalf("fixture 应有 10 条 entry")
	}
	tag, err := LatestTagFromAtom([]byte(feed))
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v2.0.0-alpha.29" {
		t.Errorf("应取文档序第一条合法 tag，got %q", tag)
	}
}

func TestLatestTagFromAtomEmptyFeed(t *testing.T) {
	cases := map[string]string{
		"无 entry 的合法 feed": `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>tag:github.com,2008:https://github.com/Adsryen/zhenguojian/releases</id>
  <link type="text/html" rel="alternate" href="https://github.com/Adsryen/zhenguojian/releases"/>
</feed>`,
		"空应答":   "",
		"纯垃圾文本": "not xml at all",
	}
	for name, feed := range cases {
		if _, err := LatestTagFromAtom([]byte(feed)); err == nil {
			t.Errorf("%s 应报错，不得静默", name)
		} else if !strings.Contains(err.Error(), "未解析到") {
			t.Errorf("%s 错误文案不对：%v", name, err)
		}
	}
}

func TestLatestTagFromAtomPrereleaseForms(t *testing.T) {
	for _, tag := range []string{"v2.0.0-alpha.29", "v2.0.0-alpha.3", "v2.1.0-beta.2", "v3.0.0-rc.1", "v2.0.0-beta", "V2.0.0-Alpha.2"} {
		got, err := LatestTagFromAtom([]byte(fakeAtomFeed(tag)))
		if err != nil || got != tag {
			t.Errorf("tag %q: got %q err=%v", tag, got, err)
		}
	}
}

// 命名空间前缀、HTML/数字实体、转义正文里的伪装链接、非法 tag —— 全部不构成干扰。
func TestLatestTagFromAtomEntityNamespaceNoise(t *testing.T) {
	feed := `<?xml version="1.0" encoding="UTF-8"?>
<atom:feed xmlns:atom="http://www.w3.org/2005/Atom" xmlns:media="http://search.yahoo.com/mrss/">
  <atom:id>tag:github.com,2008:https://github.com/Adsryen/zhenguojian/releases</atom:id>
  <atom:link rel="alternate" href="https://github.com/Adsryen/zhenguojian/releases"/>
  <atom:entry>
    <atom:id>tag:github.com,2008:Repository/1450138827/not-a-release</atom:id>
    <atom:link type="text/html" rel="alternate" href="https://github.com/Adsryen/zhenguojian/releases/tag/not-a-release"/>
    <atom:title>WIP &nbsp;&#47;&amp; 乱码</atom:title>
    <atom:content type="html">&lt;a href=&quot;https://github.com/Adsryen/zhenguojian/releases/tag/v9.9.9&quot;&gt;伪装&lt;/a&gt;</atom:content>
  </atom:entry>
  <atom:entry>
    <atom:id>tag:github.com,2008:Repository/1450138827/v2.5.0-beta.2</atom:id>
    <atom:link href="https://github.com/Adsryen/zhenguojian/releases/tag/v2.5.0-beta.2"/>
    <media:title>v2.5.0-beta.2</media:title>
  </atom:entry>
</atom:feed>`
	got, err := LatestTagFromAtom([]byte(feed))
	if err != nil {
		t.Fatal(err)
	}
	// v9.9.9 只出现在转义正文（&quot; 非裸引号）里，非法 tag not-a-release 应被跳过。
	if got != "v2.5.0-beta.2" {
		t.Errorf("got %q want v2.5.0-beta.2（实体/命名空间/伪装链接不得干扰）", got)
	}
}

// link 缺失（镜像改写/裁剪）时从 <id> Repository/<数字>/<tag> 兜底。
func TestLatestTagFromAtomIDFallback(t *testing.T) {
	feed := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <id>tag:github.com,2008:Repository/1450138827/nightly-2026</id>
    <title>nightly-2026</title>
  </entry>
  <entry>
    <id>tag:github.com,2008:Repository/1450138827/v2.0.0-alpha.28</id>
    <title>v2.0.0-alpha.28</title>
  </entry>
</feed>`
	got, err := LatestTagFromAtom([]byte(feed))
	if err != nil {
		t.Fatal(err)
	}
	if got != "v2.0.0-alpha.28" {
		t.Errorf("id 兜链 got %q want v2.0.0-alpha.28", got)
	}
}

// ---------- latest.json 契约校验 ----------

func TestParseLatestJSONContractShape(t *testing.T) {
	rel, err := ParseLatestJSON([]byte(lmManifest("v2.0.0-alpha.29", "2.0.0-alpha.29", true, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v2.0.0-alpha.29" || rel.Version != "v2.0.0-alpha.29" || !rel.Prerelease {
		t.Errorf("tag/version/prerelease（tag 文本判定）wrong: %+v", rel)
	}
	if rel.PublishedAt != "2026-11-15T12:00:00Z" || rel.Body != "发布页正文摘要" {
		t.Errorf("generatedAt/notes 映射 wrong: %+v", rel)
	}
	if len(rel.Assets) != 4 {
		t.Fatalf("want 4 assets, got %+v", rel.Assets)
	}
	exe, err := rel.FindAsset("")
	if err != nil {
		t.Fatal(err)
	}
	if exe.Size != 21000000 || !isHTTPURL(exe.URL) {
		t.Errorf("exe asset wrong: %+v", exe)
	}
	// 契约示例里 appZip 无 url → 必须按 tag+name 合成 github.com 形状地址。
	app, err := rel.FindAsset(DefaultAppZipAsset)
	if err != nil || app.URL != ReleaseDownloadURL("v2.0.0-alpha.29", DefaultAppZipAsset) {
		t.Errorf("appZip url 合成 wrong: %+v err=%v", app, err)
	}
	if sel := rel.SelectableAssets(); len(sel) != 2 {
		t.Errorf("SelectableAssets want exe+appZip, got %+v", sel)
	}
}

func TestParseLatestJSONUnknownSchema(t *testing.T) {
	// schema=2（未来格式）与 schema 缺失(=0) 都必须明确报错，不得静默。
	for _, body := range []string{
		lmManifest("v2.0.0", "2.0.0", false, 2),
		`{}`,
	} {
		_, err := ParseLatestJSON([]byte(body))
		if err == nil || !strings.Contains(err.Error(), "schema") {
			t.Errorf("schema 不认必须报错含原因，got %v", err)
		}
	}
	if _, err := ParseLatestJSON([]byte("这不是 JSON")); err == nil || !strings.Contains(err.Error(), "解析失败") {
		t.Errorf("非法 JSON 应报错，got %v", err)
	}
}

func TestParseLatestJSONExeFieldGuard(t *testing.T) {
	cases := []struct{ name, body, wantMsg string }{
		{"缺 url", `{"schema":1,"tag":"v2.0.0","assets":{"exe":{"name":"zhenGuoJian-v2.exe","size":10,"sha256":"` + lmSHA + `"}}}`, "url"},
		{"url 非 http", `{"schema":1,"tag":"v2.0.0","assets":{"exe":{"name":"zhenGuoJian-v2.exe","url":"ftp://x/y","size":10,"sha256":"` + lmSHA + `"}}}`, "url"},
		{"缺 size", `{"schema":1,"tag":"v2.0.0","assets":{"exe":{"name":"zhenGuoJian-v2.exe","url":"https://github.com/o/r/releases/download/v2.0.0/zhenGuoJian-v2.exe","sha256":"` + lmSHA + `"}}}`, "size"},
		{"size=0", `{"schema":1,"tag":"v2.0.0","assets":{"exe":{"name":"zhenGuoJian-v2.exe","url":"https://github.com/o/r/releases/download/v2.0.0/zhenGuoJian-v2.exe","size":0,"sha256":"` + lmSHA + `"}}}`, "size"},
		{"缺 sha256", `{"schema":1,"tag":"v2.0.0","assets":{"exe":{"name":"zhenGuoJian-v2.exe","url":"https://github.com/o/r/releases/download/v2.0.0/zhenGuoJian-v2.exe","size":10}}}`, "sha256"},
		{"sha256 非 64hex", `{"schema":1,"tag":"v2.0.0","assets":{"exe":{"name":"zhenGuoJian-v2.exe","url":"https://github.com/o/r/releases/download/v2.0.0/zhenGuoJian-v2.exe","size":10,"sha256":"` + lmSHA[:63] + `"}}}`, "sha256"},
		{"tag 非法", `{"schema":1,"tag":"nightly","assets":{"exe":{"name":"zhenGuoJian-v2.exe","url":"https://github.com/o/r","size":10,"sha256":"` + lmSHA + `"}}}`, "tag"},
		{"version 与 tag 不一致", `{"schema":1,"tag":"v2.0.0","version":"2.0.1","assets":{"exe":{"name":"zhenGuoJian-v2.exe","url":"https://github.com/o/r","size":10,"sha256":"` + lmSHA + `"}}}`, "不一致"},
		{"prerelease 布尔与 tag 冲突", `{"schema":1,"tag":"v2.0.0-alpha.9","prerelease":false,"assets":{"exe":{"name":"zhenGuoJian-v2.exe","url":"https://github.com/o/r","size":10,"sha256":"` + lmSHA + `"}}}`, "冲突"},
		{"无 v2 约定资产", `{"schema":1,"tag":"v2.0.0","assets":{"exe":{"name":"other.exe","url":"https://github.com/o/r","size":10,"sha256":"` + lmSHA + `"}}}`, "v2 约定资产"},
	}
	for _, c := range cases {
		_, err := ParseLatestJSON([]byte(c.body))
		if err == nil || !strings.Contains(err.Error(), c.wantMsg) {
			t.Errorf("%s: want 报错含 %q, got %v", c.name, c.wantMsg, err)
		}
	}
}

func TestIsPrereleaseTag(t *testing.T) {
	cases := map[string]bool{
		"v2.0.0": false, "v1.5.0": false, "v2.0.0-alpha.29": true, "v2.0.0-alpha.3": true,
		"v2.1.0-beta.1": true, "v2.0.0-rc": true, "not-a-version": false, "": false,
	}
	for tag, want := range cases {
		if got := IsPrereleaseTag(tag); got != want {
			t.Errorf("IsPrereleaseTag(%q)=%v want %v", tag, got, want)
		}
	}
}

func TestReleaseURLShapes(t *testing.T) {
	if got := latestJSONURL("v2.0.0-alpha.29"); got != "https://github.com/Adsryen/zhenguojian/releases/download/v2.0.0-alpha.29/latest.json" {
		t.Errorf("latestJSONURL wrong: %s", got)
	}
	if got := ReleaseDownloadURL(" v2.0.0 ", " zhenGuoJian-v2-app.zip "); got != "https://github.com/Adsryen/zhenguojian/releases/download/v2.0.0/zhenGuoJian-v2-app.zip" {
		t.Errorf("ReleaseDownloadURL wrong: %s", got)
	}
}

// ---------- 候选链（新路径复用 ChainCandidates，不新写一套） ----------

func TestChainCandidatesForNewURLs(t *testing.T) {
	for _, raw := range []string{LatestJSONStableURL, ReleasesAtomURL, latestJSONURL("v2.0.0-alpha.29")} {
		if strings.Contains(raw, "api.github.com") {
			t.Fatalf("主路径 URL 本身含 api.github.com: %s", raw)
		}
		got := ChainCandidates(raw, "https://user.example/")
		if got[0] != "https://user.example/"+raw {
			t.Errorf("用户前缀必须排第一: %v", got)
		}
		if len(got) != 5 {
			t.Errorf("want 5 candidates, got %v", got)
		}
		for _, u := range got {
			if strings.Contains(u, "api.github.com") {
				t.Errorf("主路径候选混入 api.github.com: %s", u)
			}
		}
	}
}

// ---------- 通道选择（假 HTTP，mirrorPrefix 指向假服务器全程离线） ----------

func newChannelServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.Contains(p, "/releases.atom"):
			io.WriteString(w, fakeAtomFeed("v9.9.9-alpha.1", "v9.9.9"))
		case strings.Contains(p, "/releases/latest/download/latest.json"):
			io.WriteString(w, lmManifest("v9.9.9", "9.9.9", false, 1))
		case strings.Contains(p, "/releases/download/v9.9.9-alpha.1/latest.json"):
			io.WriteString(w, lmManifest("v9.9.9-alpha.1", "9.9.9-alpha.1", true, 1))
		case strings.Contains(p, "/releases/download/v9.9.9/latest.json"):
			io.WriteString(w, lmManifest("v9.9.9", "9.9.9", false, 1))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestCheckChannelSelection(t *testing.T) {
	base := newChannelServer(t)
	// 稳定通道：releases/latest/download/latest.json → v9.9.9，不得走 atom。
	svc := newTestService(t, base, LocalVersion{Version: "v2.0.0"})
	res, err := svc.Check(context.Background(), true)
	if err != nil || res.Release == nil || res.Release.Tag != "v9.9.9" {
		t.Fatalf("stable channel: %+v err=%v", res, err)
	}
	if res.Release.Prerelease || res.Channel != "stable" {
		t.Errorf("stable channel result wrong: %+v", res)
	}
	// 测试版通道：atom → 最新 tag v9.9.9-alpha.1 → per-tag latest.json。
	svc2 := newTestService(t, base, LocalVersion{Version: "v2.0.0"})
	if _, err := svc2.Settings.MergePatch(map[string]any{"prerelease": true}); err != nil {
		t.Fatal(err)
	}
	res2, err := svc2.Check(context.Background(), true)
	if err != nil || res2.Release == nil || res2.Release.Tag != "v9.9.9-alpha.1" {
		t.Fatalf("prerelease channel: %+v err=%v", res2, err)
	}
	if !res2.Release.Prerelease || res2.Channel != "prerelease" {
		t.Errorf("prerelease channel result wrong: %+v", res2)
	}
	// 下载入口按 tag 可查（主路径同样维护 s.releases）。
	if _, ok := svc2.ReleaseByTag("v9.9.9-alpha.1"); !ok {
		t.Error("主路径选中的 release 必须可从 ReleaseByTag 查到（下载依赖）")
	}
}

// ---------- urlRecordingTransport：钉死「主路径 0 次 api.github.com」 ----------

type urlRecordingTransport struct {
	mu   sync.Mutex
	urls []string
	rule func(u string) (int, string) // status, body；缺省 404 ""
}

func (rt *urlRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL.String()
	rt.mu.Lock()
	rt.urls = append(rt.urls, u)
	rt.mu.Unlock()
	status, body := http.StatusNotFound, ""
	if rt.rule != nil {
		status, body = rt.rule(u)
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func (rt *urlRecordingTransport) snapshot() []string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return append([]string(nil), rt.urls...)
}

func newTransportService(t *testing.T, rt *urlRecordingTransport, prefix string, prerelease bool) *Service {
	t.Helper()
	dir := t.TempDir()
	svc := NewService(dir, LocalVersion{Version: "v2.0.0-alpha.1"}, filepath.Join(dir, "update-settings.json"), nil)
	svc.checkClient = &http.Client{Transport: rt}
	if _, err := svc.Settings.MergePatch(map[string]any{"mirrorPrefix": prefix, "prerelease": prerelease}); err != nil {
		t.Fatal(err)
	}
	return svc
}

func primaryRule(atomTag string) func(string) (int, string) {
	return func(u string) (int, string) {
		switch {
		case strings.HasSuffix(u, ReleasesAtomURL):
			return http.StatusOK, fakeAtomFeed(atomTag, "v2.0.0")
		case strings.HasSuffix(u, LatestJSONStableURL):
			return http.StatusOK, lmManifest("v2.0.0", "2.0.0", false, 1)
		case strings.HasSuffix(u, latestJSONURL(atomTag)):
			v := strings.TrimPrefix(atomTag, "v")
			return http.StatusOK, lmManifest(atomTag, v, true, 1)
		}
		return http.StatusNotFound, ""
	}
}

// 防回归核心：主路径成功（稳定/预发布/带用户前缀三种形态）时，
// 整个检查流程请求 URL 里不得出现 api.github.com。
func TestCheckPrimaryPathNeverTouchesAPIGitHub(t *testing.T) {
	// 1) 稳定通道，无前缀（直连候选即中）
	rt := &urlRecordingTransport{rule: primaryRule("v2.0.0-alpha.30")}
	svc := newTransportService(t, rt, "", false)
	res, err := svc.Check(context.Background(), true)
	if err != nil || res.Release == nil || res.Release.Tag != "v2.0.0" {
		t.Fatalf("stable: %+v err=%v", res, err)
	}
	if got := rt.snapshot(); len(got) != 1 || got[0] != LatestJSONStableURL {
		t.Errorf("稳定通道应只取 releases/latest：\n%v", got)
	}

	// 2) 预发布通道 + 用户前缀：atom → download/<tag>/latest.json，顺序钉死。
	rt2 := &urlRecordingTransport{rule: primaryRule("v2.0.0-alpha.30")}
	svc2 := newTransportService(t, rt2, "https://gh-proxy.com/", true)
	res2, err := svc2.Check(context.Background(), true)
	if err != nil || res2.Release == nil || res2.Release.Tag != "v2.0.0-alpha.30" {
		t.Fatalf("prerelease: %+v err=%v", res2, err)
	}
	want := []string{
		"https://gh-proxy.com/" + ReleasesAtomURL,
		"https://gh-proxy.com/" + latestJSONURL("v2.0.0-alpha.30"),
	}
	got := rt2.snapshot()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("预发布通道 URL 序列 wrong:\n got %v\nwant %v", got, want)
	}
	for _, urls := range [][]string{rt.snapshot(), got} {
		for _, u := range urls {
			if strings.Contains(u, "api.github.com") {
				t.Errorf("主路径不得请求 api.github.com：%s", u)
			}
		}
	}
}

// 兜底分支定义上允许打 api.github.com：主路径 404 时必须回退到旧 API。
func TestCheckFallsBackToAPIOnlyAfterPrimaryFails(t *testing.T) {
	relJSON := `[{"tag_name":"v9.9.9","prerelease":false,"body":"old api",` +
		`"assets":[{"name":"zhenGuoJian-v2.exe","browser_download_url":"https://github.com/Adsryen/zhenguojian/releases/download/v9.9.9/zhenGuoJian-v2.exe","size":18}]}]`
	rt := &urlRecordingTransport{rule: func(u string) (int, string) {
		if strings.Contains(u, "api.github.com") {
			return http.StatusOK, relJSON
		}
		return http.StatusNotFound, ""
	}}
	svc := newTransportService(t, rt, "", false)
	res, err := svc.Check(context.Background(), true)
	if err != nil || res.Release == nil || res.Release.Tag != "v9.9.9" {
		t.Fatalf("兜底应接管: %+v err=%v", res, err)
	}
	got := rt.snapshot()
	// 主路径 4 候选（无前缀）失败后，兜底候选 1 = 直连 API。
	if len(got) != 5 {
		t.Fatalf("want 4 主 + 1 兜底 = 5 请求, got %v", got)
	}
	if got[0] != LatestJSONStableURL || got[4] != ReleasesAPIURL {
		t.Errorf("顺序 wrong: %v", got)
	}
}

// 双路径全失败：错误必须能还原每个候选的具体原因（沿用 FetchJSON 现有写法），
// 且 schema 校验失败不得被吞成「无更新」。
func TestCheckAllCandidatesFailureListsReasons(t *testing.T) {
	rt := &urlRecordingTransport{} // 全 404
	svc := newTransportService(t, rt, "", false)
	res, err := svc.Check(context.Background(), true)
	if err == nil || res.Error == "" {
		t.Fatalf("必须报错, res=%+v", res)
	}
	if !strings.Contains(res.Error, "latest.json 主路径失败") || !strings.Contains(res.Error, "旧 API 兜底失败") {
		t.Errorf("两段来源都要写明：%v", res.Error)
	}
	for _, want := range []string{"候选1", "候选4", "HTTP 404", "github.com", "gh-proxy.com", "ghproxy.net", "mirror.ghproxy.com", "api.github.com"} {
		if !strings.Contains(res.Error, want) {
			t.Errorf("错误里应含 %q，got：%s", want, res.Error)
		}
	}
	if n := len(rt.snapshot()); n != 8 { // 主 4 候选 + 兜底 4 候选（无前缀）
		t.Errorf("want 8 次请求, got %d", n)
	}
}

func TestCheckSchemaErrorNotSilent(t *testing.T) {
	rt := &urlRecordingTransport{rule: func(u string) (int, string) {
		if strings.HasSuffix(u, LatestJSONStableURL) {
			return http.StatusOK, lmManifest("v3.0.0", "3.0.0", false, 99)
		}
		return http.StatusNotFound, ""
	}}
	svc := newTransportService(t, rt, "", false)
	res, err := svc.Check(context.Background(), true)
	if err == nil || res.Available {
		t.Fatalf("schema 不认不得静默当无更新: %+v err=%v", res, err)
	}
	if !strings.Contains(res.Error, "schema=99") {
		t.Errorf("错误应写明 schema 不合: %s", res.Error)
	}
}

func TestCheckStableChannelRejectsPrereleaseManifest(t *testing.T) {
	rt := &urlRecordingTransport{rule: func(u string) (int, string) {
		if strings.HasSuffix(u, LatestJSONStableURL) {
			return http.StatusOK, lmManifest("v2.0.0-alpha.9", "2.0.0-alpha.9", true, 1)
		}
		return http.StatusNotFound, ""
	}}
	svc := newTransportService(t, rt, "", false)
	res, err := svc.Check(context.Background(), true)
	if err == nil || !strings.Contains(res.Error, "稳定通道") {
		t.Fatalf("稳定通道拿到预发布 manifest 必须报错: %+v err=%v", res, err)
	}
}

// 预发布通道：atom 里的最新 tag 与 manifest 自述 tag 不一致（CI 传错资产）必须报错。
func TestCheckPrereleaseTagCrossCheck(t *testing.T) {
	rt := &urlRecordingTransport{rule: func(u string) (int, string) {
		switch {
		case strings.HasSuffix(u, ReleasesAtomURL):
			return http.StatusOK, fakeAtomFeed("v2.0.0-alpha.31", "v2.0.0-alpha.30")
		case strings.HasSuffix(u, latestJSONURL("v2.0.0-alpha.31")):
			return http.StatusOK, lmManifest("v2.0.0-alpha.30", "2.0.0-alpha.30", true, 1)
		}
		return http.StatusNotFound, ""
	}}
	svc := newTransportService(t, rt, "", true)
	res, err := svc.Check(context.Background(), true)
	if err == nil || !strings.Contains(res.Error, "不一致") {
		t.Fatalf("tag 对账失败必须报错: %+v err=%v", res, err)
	}
}
