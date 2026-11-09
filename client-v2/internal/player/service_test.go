package player_test

// service_test.go：V2-3b 服务层编排测试（fake mpv + 可编程弹幕源 + 事件捕获）。
// 覆盖任务卡要求的六项：tick 形状 / version-bump→sub 序列 / 断线重建调用序 /
// SetDanmaku 透传 / Close 幂等 / 重复 Open 换源。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"zhenguojian/client-v2/internal/danmaku"
	"zhenguojian/client-v2/internal/player"
)

// ── 可编程弹幕源 ──

type dmCall struct {
	Session        string
	StartMs, EndMs int64
}

type dmSrc struct {
	mu    sync.Mutex
	calls []dmCall
	// windowItems 决定该窗返回的条目；nil = 空窗。
	windowItems func(startMs int64) []map[string]any
	err         error
}

func (d *dmSrc) fn(_ context.Context, session string, startMs, endMs int64) (map[string]any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, dmCall{Session: session, StartMs: startMs, EndMs: endMs})
	if d.err != nil {
		return nil, d.err
	}
	items := []map[string]any{}
	if d.windowItems != nil {
		if got := d.windowItems(startMs); got != nil {
			items = got
		}
	}
	return map[string]any{
		"items":   items,
		"startMs": startMs,
		"nextMs":  endMs,
		"total":   int64(len(items)),
	}, nil
}

func (d *dmSrc) snapshot() []dmCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]dmCall(nil), d.calls...)
}

func item(id string, timeMs int64, text string) map[string]any {
	return map[string]any{"id": id, "timeMs": timeMs, "text": text}
}

// ── 装配 ──

func newSVC(t *testing.T, src *dmSrc) (*player.Service, *fakeBackend, *emitter) {
	t.Helper()
	fb := &fakeBackend{}
	em := newEmitter()
	opts := player.Options{
		Backend:      fb,
		MpvPath:      "/fake/mpv",
		EventsEmit:   em.emit,
		TickInterval: 20 * time.Millisecond,
		AssDir:       t.TempDir(),
		FetcherOpt:   danmaku.FetcherOptions{WindowMS: 1000, MinGap: time.Millisecond, PrefetchWindows: 1},
	}
	if src != nil {
		opts.DanmakuSource = src.fn
	}
	svc := player.New(opts)
	svc.OnStartup(context.Background())
	t.Cleanup(func() { svc.OnShutdown(context.Background()) })
	return svc, fb, em
}

func videoJSON(url, session, title string, ep int, durSec float64) string {
	v := map[string]any{
		"url": url, "title": title, "session": session,
		"durationSec": durSec, "ep": ep,
		"headers": map[string]string{"Referer": "https://ex"},
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// findCmd 在（可选 server 过滤的）命令日志里找第一条 Name 匹配且各子串命中
// Args/Raw 的命令，返回下标；-1 未找到。
func findCmd(cmds []cmdRec, server int, name string, subs ...string) int {
	for i, c := range cmds {
		if c.Name != name {
			continue
		}
		if server >= 0 && c.Server != server {
			continue
		}
		hay := c.Raw
		ok := true
		for _, s := range subs {
			if !strings.Contains(hay, s) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// ── 1. Open → player:tick 形状 ──

func TestOpenTickSnapshot(t *testing.T) {
	svc, fb, em := newSVC(t, nil)
	if _, err := svc.Open(videoJSON("https://v/a.mp4", "", "剧A", 1, 60)); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return fb.lastServer().hasLoadfile() }, "loadfile")

	srv := fb.lastServer()
	srv.PushProperty("time-pos", 12.5)
	srv.PushProperty("duration", 60)

	ev := em.waitEvent(t, "player:tick", 2*time.Second)
	var m map[string]any
	if err := json.Unmarshal([]byte(ev.Data), &m); err != nil {
		t.Fatalf("tick payload 非 JSON: %v", err)
	}
	want := []string{"position", "duration", "title", "paused", "speed", "volume", "eof", "danmakuNote", "danmakuCount"}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("tick 缺字段 %s: %s", k, ev.Data)
		}
	}
	if m["title"] != "剧A" {
		t.Errorf("title=%v", m["title"])
	}
	if _, ok := m["danmakuNote"].(string); !ok {
		t.Errorf("danmakuNote 应为字符串: %v", m["danmakuNote"])
	}
	// position 经 property 通道更新后，某次 tick 应带上 12.5
	waitFor(t, 2*time.Second, func() bool {
		for {
			select {
			case e := <-em.ch:
				if e.Name != "player:tick" {
					continue
				}
				var mm map[string]any
				_ = json.Unmarshal([]byte(e.Data), &mm)
				if mm["position"] == 12.5 {
					return true
				}
			default:
				return false
			}
		}
	}, "position=12.5 的 tick")
}

// ── 2. version  bump → sub-add → sub-reload 序列 ──

func TestDanmakuVersionBumpSubSequence(t *testing.T) {
	src := &dmSrc{windowItems: func(startMs int64) []map[string]any {
		if startMs >= 2000 {
			return nil // 第三窗空
		}
		return []map[string]any{item(fmt.Sprintf("d%d", startMs), startMs+100, "冲")}
	}}
	svc, fb, _ := newSVC(t, src)
	if _, err := svc.Open(videoJSON("https://v/a.mp4", "sess-1", "剧B", 2, 3)); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool {
		cmds := fb.serverCmds()
		return findCmd(cmds, 0, "sub-add") >= 0 && findCmd(cmds, 0, "sub-reload") >= 0
	}, "sub-add 后出现 sub-reload")

	cmds := fb.serverCmds()
	addIdx := findCmd(cmds, 0, "sub-add")
	relIdx := findCmd(cmds, 0, "sub-reload")
	if !(addIdx >= 0 && relIdx > addIdx) {
		t.Fatalf("序列错误 add=%d reload=%d cmds=%+v", addIdx, relIdx, cmds)
	}
	// 抓取节奏：确实请求过窗口（session 透传）
	calls := src.snapshot()
	if len(calls) == 0 || calls[0].Session != "sess-1" {
		t.Fatalf("弹幕源调用异常: %+v", calls)
	}
	// ASS 文件已落盘（sub-add 参数指向 .ass）
	if i := findCmd(cmds, 0, "sub-add", ".ass"); i < 0 {
		t.Errorf("sub-add 应挂 .ass 文件: %+v", cmds[addIdx])
	}
}

// ── 3. 断线 → 重建调用序 ──

func TestDisconnectRebuildSequence(t *testing.T) {
	svc, fb, em := newSVC(t, nil)
	if _, err := svc.Open(videoJSON("https://v/a.mp4", "", "剧C", 1, 0)); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return fb.lastServer().hasLoadfile() }, "loadfile#1")
	srv0 := fb.lastServer()
	srv0.PushProperty("time-pos", 42)
	srv0.CloseEvents()

	st := em.waitEvent(t, "player:state", 3*time.Second)
	var m map[string]any
	if err := json.Unmarshal([]byte(st.Data), &m); err != nil {
		t.Fatalf("state payload: %v", err)
	}
	if m["state"] != "disconnected" {
		t.Fatalf("state=%v", m["state"])
	}
	if why, _ := m["why_died"].(string); !strings.Contains(why, "断开") {
		t.Errorf("why_died 缺断线原因: %q", why)
	}

	waitFor(t, 3*time.Second, func() bool {
		s1 := fb.server(1)
		if s1 == nil {
			return false
		}
		cmds := s1.Cmds()
		return findCmd(cmds, 1, "loadfile") >= 0 && findCmd(cmds, 1, "seek", "absolute", "42") >= 0
	}, "重建 player 续播 seek")

	cmds := fb.server(1).Cmds()
	loadIdx := findCmd(cmds, 1, "loadfile")
	seekIdx := findCmd(cmds, 1, "seek", "absolute", "42")
	if !(seekIdx > loadIdx) {
		t.Fatalf("重建后应先 loadfile 再 seek: %+v", cmds)
	}
}

// ── 4. SetDanmaku 透传 ──

func TestSetDanmakuPassthrough(t *testing.T) {
	// 未打开：只记意图，不发命令、不抓取
	src := &dmSrc{windowItems: func(int64) []map[string]any { return []map[string]any{item("x", 100, "hi")} }}
	svc, fb, _ := newSVC(t, src)
	if _, err := svc.SetDanmaku(false); err != nil {
		t.Fatalf("未开时 SetDanmaku: %v", err)
	}
	if st := svc.State(); !strings.Contains(st, `"danmakuOn":false`) {
		t.Fatalf("意图未记录: %s", st)
	}
	if n := fb.serverCount(); n != 0 {
		t.Fatalf("不应拉起 player: %d", n)
	}
	// 关闭态 Open：tick 不喂 Fetcher
	if _, err := svc.Open(videoJSON("https://v/a.mp4", "sess-1", "剧D", 1, 3)); err != nil {
		t.Fatalf("Open: %v", err)
	}
	time.Sleep(120 * time.Millisecond)
	if calls := src.snapshot(); len(calls) != 0 {
		t.Fatalf("弹幕关闭期间不应请求: %+v", calls)
	}
	// 打开：透传 sub-visibility=true 并开始抓取
	if _, err := svc.SetDanmaku(true); err != nil {
		t.Fatalf("SetDanmaku(true): %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		cmds := fb.serverCmds()
		return findCmd(cmds, 0, "set_property", "sub-visibility", "yes") >= 0
	}, "sub-visibility yes")
	waitFor(t, 2*time.Second, func() bool { return len(src.snapshot()) > 0 }, "弹幕请求启动")
	if _, err := svc.SetDanmaku(false); err != nil {
		t.Fatalf("SetDanmaku(false): %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return findCmd(fb.serverCmds(), 0, "set_property", "sub-visibility", "no") >= 0
	}, "sub-visibility no")
}

// ── 5. Close 幂等 + 重新 Open ──

func TestCloseIdempotentAndReopen(t *testing.T) {
	svc, fb, _ := newSVC(t, nil)
	if _, err := svc.Open(videoJSON("https://v/a.mp4", "", "剧E", 1, 60)); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return fb.lastServer().hasLoadfile() }, "loadfile")

	if _, err := svc.Close(); err != nil {
		t.Fatalf("Close#1: %v", err)
	}
	if _, err := svc.Close(); err != nil {
		t.Fatalf("Close#2 应幂等: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		n := 0
		for _, c := range fb.serverCmds() {
			if c.Name == "quit" {
				n++
			}
		}
		return n == 1
	}, "quit 恰好一次")
	if st := svc.State(); !strings.Contains(st, `"running":false`) || !strings.Contains(st, `"opened":false`) {
		t.Fatalf("Close 后状态异常: %s", st)
	}
	// 重新 Open = 新 spawn
	if _, err := svc.Open(videoJSON("https://v/b.mp4", "", "剧E", 1, 60)); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return fb.serverCount() == 2 }, "新 spawn")
}

// ── 6. 重复 Open = 换源（同实例） ──

func TestRepeatOpenReplacesSource(t *testing.T) {
	src := &dmSrc{windowItems: func(int64) []map[string]any { return []map[string]any{item("x", 100, "hi")} }}
	svc, fb, _ := newSVC(t, src)
	if _, err := svc.Open(videoJSON("https://v/a.mp4", "sess-1", "剧F", 1, 3)); err != nil {
		t.Fatalf("Open#1: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return len(src.snapshot()) > 0 }, "sess-1 首窗")
	if _, err := svc.Open(videoJSON("https://v/b.mp4", "sess-2", "剧F", 2, 3)); err != nil {
		t.Fatalf("Open#2: %v", err)
	}
	if n := fb.serverCount(); n != 1 {
		t.Fatalf("重复 Open 不应重复 spawn: %d", n)
	}
	cmds := fb.serverCmds()
	i2 := findCmd(cmds, 0, "loadfile", "b.mp4")
	if i2 < 0 {
		t.Fatalf("第二次 loadfile 缺新 url: %+v", cmds)
	}
	waitFor(t, 2*time.Second, func() bool {
		for _, c := range src.snapshot() {
			if c.Session == "sess-2" {
				return true
			}
		}
		return false
	}, "换集后按新 session 抓取")
}
