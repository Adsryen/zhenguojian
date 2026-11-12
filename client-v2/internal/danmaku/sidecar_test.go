package danmaku

// sidecar_test.go：CollectSidecar 的形状。
// 覆盖的都是**没有网络也能测**的那半段（跳过已有 / 时长缺失 / 会话缺失 /
// 无弹幕不写空文件 / 排序 / 字节形状 / 半套残留），
// 真正的 resolve+danmakuAll 由接线层注入，CI 连不上核心，也不该假装连上过。

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFakeVideo(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("fake video bytes"), 0o644); err != nil {
		t.Fatalf("放成品: %v", err)
	}
	return p
}

func fixedDeps(items []Item, total int64, err error) SidecarDeps {
	return SidecarDeps{
		Resolve:  func() (string, string, error) { return "sess-1", "ep-9", nil },
		FetchAll: func(string) ([]Item, int64, error) { return items, total, err },
		Now:      func() int64 { return 1794000000123 },
	}
}

func TestCollectSidecarWritesBothFiles(t *testing.T) {
	dir := t.TempDir()
	video := writeFakeVideo(t, dir, "第一集.mp4")
	items := []Item{{ID: "b", TimeMs: 5000, Text: "后"}, {ID: "a", TimeMs: 1000, Text: "前"}}

	res, err := CollectSidecar(
		SidecarRequest{VideoPath: video, DurationMs: 60000, Width: 1280, Height: 720, SeriesID: "s-1"},
		fixedDeps(items, 0, nil), // total=0 → 用条数兜底（v1 `or len(items)`）
	)
	if err != nil {
		t.Fatalf("CollectSidecar: %v", err)
	}
	if !res.Written || res.Skipped || res.Empty || res.Items != 2 {
		t.Fatalf("结果不对：%+v", res)
	}

	ass, err := os.ReadFile(res.AssPath)
	if err != nil {
		t.Fatalf("读 .ass: %v", err)
	}
	// 画布参数必须真的传到 BuildAss（v1 size=(width,height)）
	if !strings.Contains(string(ass), "PlayResX: 1280") || !strings.Contains(string(ass), "PlayResY: 720") {
		t.Fatalf(".ass 画布不对：%s", firstLines(string(ass), 3))
	}
	// 事件顺序必须按时间轴（输入是乱序的）
	iA, iB := strings.Index(string(ass), "前"), strings.Index(string(ass), "后")
	if iA < 0 || iB < 0 || iA > iB {
		t.Fatalf(".ass 里弹幕没按 timeMs 排序：前@%d 后@%d", iA, iB)
	}

	raw, err := os.ReadFile(res.DocPath)
	if err != nil {
		t.Fatalf("读 .danmaku.json: %v", err)
	}
	var doc SidecarDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json 解析: %v / %s", err, raw)
	}
	if doc.EpisodeID != "ep-9" || doc.SeriesID != "s-1" || doc.Total != 2 || doc.FetchedAt != 1794000000123 {
		t.Fatalf("document 字段不对：%+v", doc)
	}
	// 缩进必须是 v1 的 indent=1：第二行**恰好一个空格**开头。
	// （上一版这里我写成反引号里的 `\n "total": 2,`——反引号串里 \n 是字面反斜杠，
	// 永远不可能命中，于是这条断言实际上只剩 HasPrefix 半边在跑。）
	lines := strings.Split(string(raw), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[1], " \"") || strings.HasPrefix(lines[1], "  \"") {
		t.Fatalf("缩进不是 v1 的 1 空格：%q", strings.Join(head(lines, 3), "|"))
	}
	if !strings.Contains(string(raw), "\"total\": 2") {
		t.Fatalf("total 没写进去：%s", string(raw)[:80])
	}
	if strings.Contains(string(raw), "\\u003c") {
		t.Fatal("HTML 被转义了，v1 不转义")
	}
}

func TestCollectSidecarSkipsWhenAlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	video := writeFakeVideo(t, dir, "a.mp4")
	ass, doc := SidecarPaths(video)
	if err := os.WriteFile(ass, []byte("[Script Info]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	deps := SidecarDeps{
		Resolve:  func() (string, string, error) { called = true; return "s", "e", nil },
		FetchAll: func(string) ([]Item, int64, error) { return nil, 0, nil },
	}
	res, err := CollectSidecar(SidecarRequest{VideoPath: video, DurationMs: 1000}, deps)
	if err != nil {
		t.Fatalf("跳过不该报错: %v", err)
	}
	if !res.Skipped || res.Written {
		t.Fatalf("该走跳过分支：%+v", res)
	}
	if called {
		t.Fatal("已经存过了还去 resolve，白跑一次网络（v1 在 _queue_danmaku 里就先判）")
	}
}

func TestCollectSidecarEmptyItemsWritesNothing(t *testing.T) {
	// v1：没有弹幕就 on_status 后 return，**不写空文件**。
	// 写了会让 SidecarReady 变真，以后永远补不上——这是最阴的一种坏状态。
	dir := t.TempDir()
	video := writeFakeVideo(t, dir, "a.mp4")
	res, err := CollectSidecar(SidecarRequest{VideoPath: video, DurationMs: 1000},
		fixedDeps(nil, 0, nil))
	if err != nil {
		t.Fatalf("无弹幕不该报错: %v", err)
	}
	if !res.Empty || res.Written {
		t.Fatalf("该走 Empty 分支：%+v", res)
	}
	ass, doc := SidecarPaths(video)
	for _, p := range []string{ass, doc} {
		if _, statErr := os.Stat(p); statErr == nil {
			t.Fatalf("无弹幕却写了 %s", filepath.Base(p))
		}
	}
	if SidecarReady(video) {
		t.Fatal("无弹幕后 SidecarReady 竟为真")
	}
}

func TestCollectSidecarGuards(t *testing.T) {
	dir := t.TempDir()
	video := writeFakeVideo(t, dir, "a.mp4")

	// 时长缺失（v1「缺少分集时长，无法抓弹幕」）
	_, err := CollectSidecar(SidecarRequest{VideoPath: video, DurationMs: 0}, fixedDeps([]Item{{ID: "x", TimeMs: 1, Text: "y"}}, 1, nil))
	if err == nil || !strings.Contains(err.Error(), "缺少分集时长") {
		t.Fatalf("时长守卫文案不对: %v", err)
	}

	// 会话缺失（v1「没有拿到播放会话」）
	noSession := SidecarDeps{
		Resolve:  func() (string, string, error) { return "", "", nil },
		FetchAll: func(string) ([]Item, int64, error) { return []Item{{ID: "x", TimeMs: 1, Text: "y"}}, 1, nil },
	}
	if _, err := CollectSidecar(SidecarRequest{VideoPath: video, DurationMs: 5000}, noSession); err == nil ||
		!strings.Contains(err.Error(), "没有拿到播放会话") {
		t.Fatalf("会话守卫文案不对: %v", err)
	}

	// 空路径 / 没注入 deps
	if _, err := CollectSidecar(SidecarRequest{VideoPath: "  ", DurationMs: 5000}, noSession); err == nil {
		t.Fatal("空路径该报错")
	}
	if _, err := CollectSidecar(SidecarRequest{VideoPath: video, DurationMs: 5000}, SidecarDeps{}); err == nil {
		t.Fatal("没注入 resolve/fetch 该报错，而不是 panic 或静默成功")
	}

	// fetch 失败原样上抛，且不留下半套
	boom := SidecarDeps{
		Resolve:  func() (string, string, error) { return "s", "e", nil },
		FetchAll: func(string) ([]Item, int64, error) { return nil, 0, errors.New("核心挂了") },
	}
	if _, err := CollectSidecar(SidecarRequest{VideoPath: video, DurationMs: 5000}, boom); err == nil ||
		!strings.Contains(err.Error(), "核心挂了") {
		t.Fatalf("fetch 失败该原样上抛: %v", err)
	}
	if assOnly, _ := SidecarPaths(video); fileExists(assOnly) {
		t.Fatal("fetch 失败却写了 .ass")
	}
}

func TestWriteTextAtomicLeavesNoTmp(t *testing.T) {
	dir := t.TempDir()
	// 目录不存在也要能写（v1 会 makedirs）
	path := filepath.Join(dir, "深", "一层", "x.ass")
	if err := WriteTextAtomic("hello", path); err != nil {
		t.Fatalf("写: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "hello" {
		t.Fatalf("内容不对: %q %v", b, err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("残留 .tmp，说明不是原子替换")
	}
	if err := WriteTextAtomic("x", ""); err == nil {
		t.Fatal("空路径该报错")
	}
}

func TestSidecarDocumentEmptyItemsIsArrayNotNull(t *testing.T) {
	// v1 `list(items or [])` → 空数组；Go 的 nil slice 会序列化成 null，
	// 那读取方（核心 nativeDanmakuDocument）拿到的形状就不一样了。
	text, err := SidecarDocument{EpisodeID: "e"}.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if !strings.Contains(text, `"items": []`) {
		t.Fatalf("items 该是 []：%s", text)
	}
	if strings.HasSuffix(text, "\n") {
		t.Fatal("尾部多了换行（v1 json.dumps 不补）")
	}
}

// head 而不是 min：Go 1.21 起 min/max 是内建函数，
// 在同一个包里再定义一个同名函数会**把内建遮蔽掉**——
// CI 实测后果是 ass.go:56 与 fetcher.go:271 的浮点 min() 全部编译失败
// （"我的测试文件让生产代码编译不过"，本机没有 Go 工具链，只有 CI 抓得到）。
func head(lines []string, n int) []string {
	if len(lines) < n {
		return lines
	}
	return lines[:n]
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestDetectSidecar(t *testing.T) {
	dir := t.TempDir()
	video := writeFakeVideo(t, dir, "ep1.mp4")
	ass, doc := SidecarPaths(video)

	// 一个都没有 → 不 ok（v1：没有就返回 False，静默不挂）
	if _, _, ok := DetectSidecar(video); ok {
		t.Fatal("还没有旁挂文件却报 ok")
	}

	// 空的 .ass → 不 ok（挂上去等于给一条空轨，还会谎报"这一集有弹幕"）
	if err := os.WriteFile(ass, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := DetectSidecar(video); ok {
		t.Fatal("空 .ass 不该算有弹幕")
	}
	if err := os.WriteFile(ass, []byte("[Script Info]"), 0o644); err != nil {
		t.Fatal(err)
	}

	// .danmaku.json 读不到 → 照样 ok、条数 0（v1 把 OSError/ValueError/TypeError 全吞掉）
	gotAss, count, ok := DetectSidecar(video)
	if !ok || gotAss != ass || count != 0 {
		t.Fatalf("缺 json 时该 ok 且条数 0，得 ok=%v ass=%q count=%d", ok, gotAss, count)
	}

	// items 优先于 total：条数是"本地已有多少条"，不是核心报的总量
	body, err := json.Marshal(SidecarDocument{
		EpisodeID: "e", SeriesID: "s", Total: 999, FetchedAt: 1,
		Items: []Item{{ID: "a", TimeMs: 1, Text: "一"}, {ID: "b", TimeMs: 2, Text: "二"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, count, _ = DetectSidecar(video); count != 2 {
		t.Fatalf("items 有 2 条却报 %d（total=999 不该优先）", count)
	}

	// items 空但 total 有 → 回落 total
	if err := os.WriteFile(doc, []byte(`{"episodeId":"e","seriesId":"s","total":7,"fetchedAt":1,"items":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, count, _ = DetectSidecar(video); count != 7 {
		t.Fatalf("items 空时应回落 total=7，得 %d", count)
	}

	// json 坏了 → 仍 ok、条数 0，不炸
	if err := os.WriteFile(doc, []byte("{坏掉的 json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, count, ok = DetectSidecar(video); !ok || count != 0 {
		t.Fatalf("坏 json 该 ok+0，得 ok=%v count=%d", ok, count)
	}
}

func firstLines(s string, n int) string {
	parts := strings.Split(s, "\n")
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "\n")
}
