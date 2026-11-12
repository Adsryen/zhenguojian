package player_test

// local_test.go：OpenLocal（R22 本地成品播放，v1 main.py:2609-2640 open_local 的端口）。
//
// 盯四件事：
//   ① 本地播放**完全不走 Resolver**（不注入也不会报"未注入 Resolver"），
//      因为一个已经躺在磁盘上的文件没有"解析地址"这一步；
//   ② 路径原样交给 mpv（loadfile:<path>），标题回落到文件名；
//   ③ 本地文件不属于任何一集：Episode.DramaID/Index 必须空 ——
//      v1 是靠清空选集条 + set_neighbours(false,false) 表达同一件事，
//      v2 的选集/上下集都读会话里的 Episode，所以留空就是那条语义；
//   ④ Session 留空 → 弹幕抓取不启动（线上没有这一集的弹幕可抓）。
//      v1 的本地弹幕是挂下载时保存的同名 .ass（main.py:1880 _attach_sidecar_danmaku），
//      **这条还没做**，所以这里只断"不抓线上弹幕"，不去假装 sidecar 已接上。

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zhenguojian/client-v2/internal/player"
)

func TestOpenLocalSkipsResolver(t *testing.T) {
	// ① 故意把 Resolver 摘掉：Open 会报错的那条路径，OpenLocal 不该经过
	h := newHarness(t, func(cfg *player.Config) { cfg.Resolver = nil })
	file := filepath.Join(t.TempDir(), "某剧 S01 合并.mp4")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("写测试文件: %v", err)
	}

	if err := h.svc.OpenLocal(context.Background(), player.LocalRequest{Path: file}); err != nil {
		t.Fatalf("OpenLocal（无 Resolver）不该失败: %v", err)
	}
	ps := h.l.all()
	if len(ps) != 1 {
		t.Fatalf("应 spawn 1 个实例，得 %d", len(ps))
	}
	// ② 路径原样进 mpv
	if !ps[0].hasCall("loadfile:" + file) {
		t.Fatalf("OpenLocal 未装载本地路径: %v", ps[0].calls)
	}

	snap := h.svc.State()
	// ③ 不属于任何一集
	if snap.Episode.DramaID != "" || snap.Episode.Index != 0 {
		t.Fatalf("本地播放不该带剧集引用: %+v", snap.Episode)
	}
	// ②标题回落文件名
	if snap.Episode.Title != filepath.Base(file) {
		t.Fatalf("标题应回落到文件名，得 %q", snap.Episode.Title)
	}
	// ⑤会话必须**自己声明**这是本地会话：前端靠它判断"已打开"，
	// 只靠 episode.index 非空会把本地会话读成没打开（进度落盘就整条不触发）。
	if !snap.Local {
		t.Fatal("本地会话的快照没带 Local=true")
	}
	if snap.Episode.Index != 0 {
		t.Fatalf("本地文件不该有集号，得 %d", snap.Episode.Index)
	}
}

func TestOpenLocalKeepsExplicitTitle(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.svc.OpenLocal(context.Background(), player.LocalRequest{
		Path: filepath.Join(t.TempDir(), "media.mp4"), Title: "自定义名字"}); err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	if st := h.svc.State(); !st.Local {
		t.Fatal("显式标题那条也得是本地会话")
	}
	if got := h.svc.State().Episode.Title; got != "自定义名字" {
		t.Fatalf("显式 Title 不该被文件名覆盖，得 %q", got)
	}
}

func TestOpenLocalRejectsEmptyPath(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.svc.OpenLocal(context.Background(), player.LocalRequest{Path: "   "}); err == nil {
		t.Fatal("空路径必须报错")
	}
	if len(h.l.all()) != 0 {
		t.Fatalf("空路径不该 spawn 实例")
	}
}

func TestOpenLocalDoesNotFetchOnlineDanmaku(t *testing.T) {
	// ④ 本地播放不该去线上抓弹幕（Session 空 → 不给抓取器设剧集窗口）。
	// 但"断言恒为 0"什么都证明不了（第 27 轮那条教训在这里同样成立）——
// 而且它**果然在 CI 上兑现了**：第一版对照本身跑不起来（漏了 setLoaded），
// 是这条对照把假绿拦下来的。
	// 所以同一把尺子先跑一次**线上 Open 当阳性对照**：那必须抓到东西。
	h := newHarness(t, nil)

	online := openReq(1)
	if err := h.svc.Open(context.Background(), online); err != nil {
		t.Fatalf("Open: %v", err)
	}
	// 抓取窗口要**知道时长/位置**才会发起：第一版我漏了这一步，
	// CI 直接在 88 行判"等待超时: 线上 Open 应触发抓取"——
	// 也就是说对照跑不起来，后面的"0 次"根本不构成证据。
	// 补上假实例的 loaded（同包其它用例也是这个顺序：Open → setLoaded → 看 tick）。
	ps := h.l.all()
	if len(ps) != 1 {
		t.Fatalf("Open 后应有 1 个实例，得 %d", len(ps))
	}
	ps[0].setLoaded(5.0, 120.0)
	waitFor(t, 2*time.Second, func() bool { return srcReqs(h.src) > 0 }, "线上 Open 应触发抓取")

	file := filepath.Join(t.TempDir(), "media.mp4")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("写测试文件: %v", err)
	}
	before := srcReqs(h.src)
	if err := h.svc.OpenLocal(context.Background(), player.LocalRequest{Path: file}); err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	// 本地这侧同样给 loaded：不然"没有抓取"可能只是因为"还没时长"，
	// 那就不是在测 Session 为空这条规则了。
	ps = h.l.all()
	ps[len(ps)-1].setLoaded(3.0, 60.0)
	time.Sleep(300 * time.Millisecond) // 好几个 tick 的宽度
	if got := srcReqs(h.src); got != before {
		t.Fatalf("本地播放后不该再有线上抓取调用：前 %d 后 %d", before, got)
	}
	if h.svc.State().Episode.Index != 0 {
		t.Fatalf("本地播放后 Episode 应清空，得 %+v", h.svc.State().Episode)
	}
}

func TestOpenLocalAttachesSidecarDanmaku(t *testing.T) {
	// v1 main.py:1880-1895 _attach_sidecar_danmaku：本地播放挂**同名 .ass**，
	// 条数取自同名 .danmaku.json；没有就静默不挂。
	// 这条测试的动机是"别只测有的一侧"：同一个用例里把有/无两种目录都跑一遍，
	// 否则很容易写成只在有 sidecar 时成立、无 sidecar 时靠 mpv 没调用蒙过去。
	dir := t.TempDir()
	video := filepath.Join(dir, "某剧 S01 合并.mp4")
	if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
		t.Fatalf("写成品: %v", err)
	}
	ass := filepath.Join(dir, "某剧 S01 合并.ass")
	if err := os.WriteFile(ass, []byte("[Script Info]\n[Events]\nDialogue: 0,0:00:01.00,Default,,0,0,0,,弹幕"), 0o644); err != nil {
		t.Fatalf("写 ass: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "某剧 S01 合并.danmaku.json"),
		[]byte(`{"episodeId":"e","seriesId":"s","total":3,"fetchedAt":1,"items":[{"id":"a","timeMs":1000,"text":"一"}]}`), 0o644); err != nil {
		t.Fatalf("写 json: %v", err)
	}

	h := newHarness(t, nil)
	if err := h.svc.OpenLocal(context.Background(), player.LocalRequest{Path: video}); err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	ps := h.l.all()
	last := ps[len(ps)-1]
	if !last.hasCall("sub-add:" + filepath.Base(ass)) {
		t.Fatalf("同名 .ass 没被挂上，calls=%v", last.calls)
	}
	// 条数取 items 的长度（1 条），不是 total=3 —— 用户看到的是"本地已有多少条"
	if got := h.svc.State().Danmaku.Count; got != 1 {
		t.Fatalf("弹幕条数该是 items 长度 1，得 %d", got)
	}

	// 关弹幕要撤轨，开回来要重挂同名 .ass（不是去线上重抓）
	if err := h.svc.SetDanmakuEnabled(false); err != nil {
		t.Fatalf("关弹幕: %v", err)
	}
	if !last.hasCall("sub-remove") {
		t.Fatalf("关弹幕没撤轨，calls=%v", last.calls)
	}
	before := subAddCount(last)
	if err := h.svc.SetDanmakuEnabled(true); err != nil {
		t.Fatalf("开弹幕: %v", err)
	}
	if subAddCount(last) <= before {
		t.Fatalf("开弹幕没重新挂 sidecar（before=%d after=%d）", before, subAddCount(last))
	}
	if !last.hasCall("sub-add:" + filepath.Base(ass)) {
		t.Fatalf("重挂的不是同名 .ass")
	}

	// 换一个**没有 sidecar** 的文件：不该挂任何字幕轨，也不报错
	video2 := filepath.Join(dir, "另一集.mp4")
	if err := os.WriteFile(video2, []byte("x"), 0o644); err != nil {
		t.Fatalf("写第二个成品: %v", err)
	}
	if err := h.svc.OpenLocal(context.Background(), player.LocalRequest{Path: video2}); err != nil {
		t.Fatalf("OpenLocal（无 sidecar）: %v", err)
	}
	if got := h.svc.State().Danmaku.Count; got != 0 {
		t.Fatalf("无 sidecar 却报出 %d 条", got)
	}
	if h.svc.State().Episode.Index != 0 || !h.svc.State().Local {
		t.Fatal("无 sidecar 那次也必须是本地会话")
	}
}

// srcReqs 读假源的调用次数（同包可直接拿字段，不额外给测试类型加公开方法）。
func srcReqs(src *fakeSource) int {
	src.mu.Lock()
	defer src.mu.Unlock()
	return len(src.reqs)
}

// waitFor 用 harness_test.go 里那一份（同包已存在），不再自己写一个：
// 第一版我在这儿重复定义了 waitFor，编译期就会撞"重复声明"——
// 本机没有 go，这类只能靠 CI 抓，所以写完先把同包的辅助函数名对过一遍。
