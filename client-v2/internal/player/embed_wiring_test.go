package player_test

// embed_wiring_test.go：V2-4 窗口内嵌的 Wid 传递链单测（Linux 可跑，无 Win32）。
// 验证语义与 v1 Qt 线一致：
//   - Config.Wid 初值 0 = 独立窗（回退态）；
//   - SetWid（PlayerAttach 入口）只影响下一次 spawn——attach 先于 Open
//     即首帧内嵌；
//   - 断线自动重建复用 s.wid（内嵌态跨重连保持）；SetWid(0) 回退独立窗。

import (
	"context"
	"sync"
	"testing"
	"time"

	"zhenguojian/client-v2/internal/mpv"
	"zhenguojian/client-v2/internal/player"
)

type cfgRecorder struct {
	mu      sync.Mutex
	cfgs    []mpv.Config
	players []*fakeMpv
}

func (r *cfgRecorder) launch(cfg mpv.Config) player.MpvPlayer {
	p := newFakeMpv()
	r.mu.Lock()
	r.cfgs = append(r.cfgs, cfg)
	r.players = append(r.players, p)
	r.mu.Unlock()
	return p
}

func (r *cfgRecorder) wids() []uintptr {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]uintptr, len(r.cfgs))
	for i, c := range r.cfgs {
		out[i] = c.Wid
	}
	return out
}

func (r *cfgRecorder) nth(i int) *fakeMpv {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i >= len(r.players) {
		panic("cfgRecorder: 实例不存在")
	}
	return r.players[i]
}

func newRecorderSvc(t *testing.T, rec *cfgRecorder) *player.Service {
	t.Helper()
	svc := player.New(player.Config{
		Resolver: resolverFunc(func(_ context.Context, _ player.Request) (player.Plan, error) {
			return fixedPlan(), nil
		}),
		MpvLauncher:  rec.launch,
		Emit:         newEmitter().emit,
		TickInterval: 10 * time.Millisecond,
	})
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func TestSetWidFlowsIntoNextSpawnAndSurvivesRebuild(t *testing.T) {
	rec := &cfgRecorder{}
	svc := newRecorderSvc(t, rec)

	// 1) 未 attach：spawn 用 Config.Wid 零值 = 独立窗。
	if err := svc.Open(context.Background(), openReq(1)); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, time.Second, func() bool { return len(rec.wids()) == 1 }, "首次 spawn")
	if w := rec.wids()[0]; w != 0 {
		t.Fatalf("初 spawn Wid=%x，期望 0（独立窗回退态）", w)
	}

	// 2) 模拟 PlayerAttach 翻转句柄；断线重建的新 spawn 必须带上（重连保内嵌）。
	const embedWid = uintptr(0xdeadbeef)
	svc.SetWid(embedWid)
	rec.nth(0).push(mpv.Event{Kind: mpv.EventDisconnected})
	waitFor(t, time.Second, func() bool { return len(rec.wids()) == 2 }, "断线重建 spawn")
	if w := rec.wids()[1]; w != embedWid {
		t.Fatalf("重建 spawn Wid=%x，期望 %x（内嵌句柄延续）", w, embedWid)
	}

	// 3) 模拟 ZHENGUO_NO_EMBED 二次 attach 前退回：SetWid(0) 后重建回独立窗。
	svc.SetWid(0)
	rec.nth(1).push(mpv.Event{Kind: mpv.EventExit, Err: context.Canceled})
	waitFor(t, time.Second, func() bool { return len(rec.wids()) == 3 }, "二次重建 spawn")
	if w := rec.wids()[2]; w != 0 {
		t.Fatalf("回退后重建 Wid=%x，期望 0", w)
	}

	// 4) attach 先于首次 Open（前端时序契约）：新服务首 spawn 即内嵌。
	rec2 := &cfgRecorder{}
	svc2 := newRecorderSvc(t, rec2)
	svc2.SetWid(embedWid)
	if err := svc2.Open(context.Background(), openReq(1)); err != nil {
		t.Fatalf("Open(ep attach 态): %v", err)
	}
	waitFor(t, time.Second, func() bool { return len(rec2.wids()) == 1 }, "attach 后首 spawn")
	if w := rec2.wids()[0]; w != embedWid {
		t.Fatalf("attach 先 Open 时首 spawn Wid=%x，期望 %x", w, embedWid)
	}
}
