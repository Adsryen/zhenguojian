package native

import (
	"os"
	"strings"
	"testing"
)

// ---------- 纯函数：input 组装（表驱动） ----------

func TestComposeInput(t *testing.T) {
	cases := []struct {
		name        string
		action      string
		payload     map[string]any
		wantAction  string
		wantKeys    []string
		denyKeys    []string
		wantSession string // 前缀匹配
	}{
		{
			name:       "catalog 带全部 v1 字段",
			action:     "catalog",
			payload:    map[string]any{"source": "hongguo", "query": "", "category": "", "page": 1, "force": false},
			wantAction: "catalog",
			wantKeys:   []string{"source", "query", "category", "page", "force", "session", "sequence"},
		},
		{
			name:       "initialize 豁免 session/sequence（v1 _call_once 名单）",
			action:     "initialize",
			payload:    map[string]any{"directory": "/tmp/x"},
			wantAction: "initialize",
			wantKeys:   []string{"directory", "action"},
			denyKeys:   []string{"session", "sequence"},
		},
		{
			name:       "release 豁免（v1 名单）",
			action:     "release",
			payload:    map[string]any{"session": "abc"},
			wantAction: "release",
			denyKeys:   []string{"sequence"},
		},
		{
			name:        "lane session 覆盖默认（同 lane 取消旧请求语义）",
			action:      "catalog",
			payload:     map[string]any{"session": "v2-1:catalog:search"},
			wantAction:  "catalog",
			wantSession: ":catalog:search",
		},
		{
			name:       "detail 传 drama 对象",
			action:     "detail",
			payload:    map[string]any{"drama": map[string]any{"id": "hongguo:123"}},
			wantAction: "detail",
			wantKeys:   []string{"drama", "session", "sequence"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := composeInput(c.action, c.payload)
			if got["action"] != c.wantAction {
				t.Fatalf("action=%v want %v", got["action"], c.wantAction)
			}
			for _, k := range c.wantKeys {
				if _, ok := got[k]; !ok {
					t.Errorf("缺少键 %q，got=%v", k, got)
				}
			}
			for _, k := range c.denyKeys {
				if _, ok := got[k]; ok {
					t.Errorf("不应有键 %q", k)
				}
			}
			if c.wantSession != "" {
				s, _ := got["session"].(string)
				if !strings.Contains(s, c.wantSession) {
					t.Errorf("session=%q 不含 %q", s, c.wantSession)
				}
			}
			if _, ok := c.payload["session"]; !ok {
				if s, has := got["session"].(string); has && !strings.HasPrefix(s, owner+":") {
					t.Errorf("默认 session %q 应以 owner %q 为前缀", s, owner)
				}
			}
		})
	}
}

func TestSequenceMonotonic(t *testing.T) {
	a, _ := composeInput("catalog", nil)["sequence"].(int64)
	b, _ := composeInput("catalog", nil)["sequence"].(int64)
	if b <= a {
		t.Fatalf("sequence 必须单调递增: %v -> %v", a, b)
	}
}

func TestLaneSession(t *testing.T) {
	if s := laneSession("catalog", ""); s != "" {
		t.Errorf("空 lane 不应生成 session: %q", s)
	}
	s := laneSession("catalog", "library")
	want := owner + ":catalog:library"
	if s != want {
		t.Errorf("lane session=%q want %q（v1: '%%s:catalog:%%s' %% (owner, lane)）", s, want)
	}
}

// ---------- 纯函数：信封解包（表驱动） ----------

func TestDecodeEnvelope(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantKeys []string
		wantErr  bool
		wantCode string
	}{
		{"ok+data 平铺", `{"ok":true,"data":{"items":[1],"hasMore":true}}`, []string{"items", "hasMore"}, false, ""},
		{"ok+null data → 空对象", `{"ok":true,"data":null}`, nil, false, ""},
		{"ok+bool data → value 包装", `{"ok":true,"data":true}`, []string{"value"}, false, ""},
		{"错误信封", `{"ok":false,"error":"请选择有效站源"}`, nil, true, ""},
		{"source_blocked+retryAt", `{"ok":false,"error":"x","code":"source_blocked","retryAt":"2026-11-09T10:00:00Z"}`, nil, true, "source_blocked"},
		{"非 JSON", `not json`, nil, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, err := decodeEnvelope(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("期望错误，got data=%v", data)
				}
				if c.wantCode != "" {
					var e *Error
					if !asError(err, &e) || e.Code != c.wantCode {
						t.Errorf("Code=%v want %v", e, c.wantCode)
					}
					if c.wantCode == "source_blocked" && !strings.Contains(err.Error(), "限流") {
						t.Errorf("source_blocked 错误文案应含限流提示: %v", err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			for _, k := range c.wantKeys {
				if _, ok := data[k]; !ok {
					t.Errorf("缺少键 %q got=%v", k, data)
				}
			}
		})
	}
}

func asError(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}

// ---------- 路由表与 v1 sidecar main.go 对齐（防漂移） ----------

func TestRoutingTablesMatchV1Sidecar(t *testing.T) {
	for _, a := range []string{"listSources", "setSourceEnabled"} {
		if !sourcesActions[a] {
			t.Errorf("%s 应路由到 NativeSourcesCommand", a)
		}
	}
	for _, a := range []string{"browseFilters", "browseFiltered", "dramaCreators", "creatorProfile", "creatorWorks"} {
		if !discoveryActions[a] {
			t.Errorf("%s 应路由到 NativeDiscovery 桥", a)
		}
	}
	// catalog/detail/resolve 必须留在 NativeRequest 主路由
	for _, a := range []string{"catalog", "detail", "resolve", "initialize"} {
		if sourcesActions[a] || discoveryActions[a] {
			t.Errorf("%s 不应被特殊路由", a)
		}
	}
}

// ---------- 集成：Linux/Windows 均可跑（无网络依赖） ----------

func TestEngineInitializeAndListSources(t *testing.T) {
	dir, err := os.MkdirTemp("", "zgj-v2-native")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	// 注意：core 的 nativeState 是进程级单例，先跑起来的测试会把引擎钉在它的
	// 目录上（core 对重复 initialize 是 no-op）；本测试负责证明 initialize
	// 与 listSources 在无网络依赖下可跑。
	t.Setenv("ZHENGUO_DATA", dir)

	if err := Init(); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	items, err := ListSources()
	if err != nil {
		t.Fatalf("listSources: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("listSources 空表——provider_registry 应有默认站源")
	}
	var found bool
	for _, it := range items {
		if it["slug"] == "hongguo" {
			found = true
			if it["implemented"] != true {
				t.Errorf("hongguo 应 implemented=true")
			}
			name, _ := it["name"].(string)
			if name == "" {
				t.Errorf("list_sources 归一化应补 name（displayName 回退）")
			}
		}
	}
	if !found {
		t.Errorf("站源表缺 hongguo: %v", items)
	}
}

func TestSetSourceEnabledRoundTrip(t *testing.T) {
	dir, err := os.MkdirTemp("", "zgj-v2-prefs")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("ZHENGUO_DATA", dir)
	ResetInitForTest()
	if err := Init(); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	defer ResetInitForTest()

	data, err := SetSourceEnabled("hongguo", false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if data["enabled"] != false {
		t.Fatalf("回包 enabled=%v", data["enabled"])
	}
	items, err := ListSources()
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it["slug"] == "hongguo" && it["enabled"] != false {
			t.Fatalf("关闭后 listSources 仍 enabled=%v", it["enabled"])
		}
	}
	if _, err := SetSourceEnabled("hongguo", true); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
}

// 未启用 overlay 时 discovery 必须返回可识别错误（而不是 panic/成功假象）
func TestDiscoveryStubError(t *testing.T) {
	_, err := BrowseFilters("hongguo", "")
	if discoveryOverlaid {
		if err == nil {
			return // overlay 通道真实现成功，合理
		}
		t.Skipf("v2overlay 通道下走真实现，运行期错误（可能无网络）: %v", err)
	}
	if err == nil {
		t.Fatal("默认构建下 BrowseFilters 应报错")
	}
	var e *Error
	if !asError(err, &e) {
		t.Fatalf("错误类型应为 *native.Error，实得 %T: %v", err, err)
	}
	if e.Code != "discovery_unavailable" || !strings.Contains(e.Message, "browseFilters") {
		t.Errorf("错误应标明动作与 overlay 缺失: %+v", e)
	}
}

func TestDanmakuWindowAcceptsAbsoluteWindowTail(t *testing.T) {
	dir, err := os.MkdirTemp("", "zgj-v2-danmaku")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("ZHENGUO_DATA", dir)
	if err := Init(); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	out, err := DanmakuWindow("bogus-session", 0, 60_000)
	if err != nil {
		t.Logf("danmaku(bogus) -> err=%v（上游会话不存在属预期）", err)
	} else {
		t.Logf("danmaku(bogus) -> %v", out)
	}
	// 带时长才走到核心（bogus 会话由上游报错，只记录不判失败）
	if _, err2 := DanmakuAll("bogus-session", 90); err2 != nil {
		t.Logf("danmakuAll(bogus) -> err=%v", err2)
	}
	// 新增：时长缺失必须**本地**拦下，不能发一个 0 让真核心去拒绝
	//（这正是第五十九轮修掉的 bug 的守卫：以前这里连 durationMs 都不发）
	if _, err2b := DanmakuAll("sess-x", 0); err2b == nil {
		t.Fatal("durationMs<=0 应本地拦截（核心 app_danmaku_all.go:181 也会拒）")
	}
	if _, err3 := DanmakuWindow("", 0, 1); err3 == nil {
		t.Fatal("空会话应本地拦截")
	}
}
