package main

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"zhenguojian/client-v2/internal/mpv"
	"zhenguojian/client-v2/internal/player"
)

// fakeEmbedHost：单测里的内嵌宿主替身。建造耗时由构造闭包控制。
// 注意：真实 *mpv.EmbedHost 的每个方法都先取 e.mu（见 embed_windows.go /
// embed_linux.go），所以壳层可以并发调它。这个 fake 必须**同样内部加锁**
// 才等价——否则并发 attach 各调一次 Debug() 就会在计数上撞车（-race 实锤过）。
type fakeEmbedHost struct {
	mu       sync.Mutex
	hwnd     uintptr
	bounds   []mpv.Bounds
	hidden   int
	debug    int
	buildSeq int
}

func (f *fakeEmbedHost) HWND() uintptr { return f.hwnd }
func (f *fakeEmbedHost) ApplyBounds(b mpv.Bounds) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bounds = append(f.bounds, b)
	return nil
}
func (f *fakeEmbedHost) Hide() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hidden++
}
func (f *fakeEmbedHost) Debug() mpv.HostDebug {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.debug++
	return mpv.HostDebug{Supported: true}
}

// TestSetBoundsDoesNotQueueBehindSlowConstruction 是第一百轮的**判据**：
// 建造（findHostWindow 轮询）慢 500ms 期间，PlayerSetBounds 必须立刻返回、
// 不因等建造而排队。alpha.42 真机 7/2P5（7 发 2 进、5 次等锁 >8s）的
// 根因就是建造压在 embedMu 里；这条把它钉死成可执行的回归。
//
// 反向自检：若有人把建造挪回锁内，本测试里 setBounds 会等 500ms 的建造锁，
// wall 必然 >300ms → 红。阈值留了 2 倍余量（CI 慢机不误报，本地真阻塞必超）。
func TestSetBoundsDoesNotQueueBehindSlowConstruction(t *testing.T) {
	t.Setenv("ZHENGUO_DATA", t.TempDir()) // app.log 落点隔离
	// 给真 playerSvc（attach 成功后会 SetWid）；New 无 cgo、只起 tick 协程，
	// 本测试不 Open，无副作用。
	a := &App{playerSvc: player.New(player.Config{})}

	// 建造缝：第一次调用睡 500ms（模拟 findHostWindow 轮询），只建一次。
	var constructOnce sync.Once
	oldNew := newEmbedHostFunc
	defer func() { newEmbedHostFunc = oldNew }()
	newEmbedHostFunc = func() (embedHost, error) {
		constructOnce.Do(func() { time.Sleep(500 * time.Millisecond) })
		return &fakeEmbedHost{hwnd: 0x1234}, nil
	}

	// attach 在后台跑（它要等 500ms 建造）。
	attachDone := make(chan struct{})
	go func() {
		if _, err := a.PlayerAttach(); err != nil {
			t.Errorf("PlayerAttach 报错（不该）：%v", err)
		}
		close(attachDone)
	}()

	// 等构造真正开始（attach 已进建造睡眠）：再等 80ms 保证它睡在 500ms 中段。
	time.Sleep(80 * time.Millisecond)

	// 此刻在旧实现下，这条 setBounds 必须排队等 ~420ms 的建造锁。
	start := time.Now()
	out, err := a.PlayerSetBounds(100, 50, 800, 600, true)
	wall := time.Since(start)
	if err != nil {
		t.Fatalf("建造进行中的 setBounds 不该报错：%v", err)
	}
	// 建造没完成前 embed==nil → 走 {"ok":false} 降级分支，这是**正确**行为
	// （前端下一帧会再报，那时 attach 已建成）。关键是它**立刻**返回。
	if !strings.Contains(out, `"ok":false`) {
		t.Fatalf("建造未完成时 setBounds 应回 {\"ok\":false}（降级），实得 %s", out)
	}
	if wall > 300*time.Millisecond {
		t.Fatalf("setBounds 排队等建造 %v（>300ms）——建造又压回锁里了（alpha.42 锁排队复现）", wall)
	}
	t.Logf("setBounds 在 500ms 建造进行 wall=%v（未排队）", wall)

	// 等 attach 走完，确认它最终 embedded:true（建造本身没被破坏）。
	<-attachDone
	out2, err := a.PlayerAttach() // 幂等：已建则直接复用
	if err != nil {
		t.Fatalf("二次 attach 报错：%v", err)
	}
	if !strings.Contains(out2, `"embedded":true`) {
		t.Fatalf("建造完成后 attach 应 embedded:true，实得 %s", out2)
	}
	// 建成后的 setBounds 必须真生效（ApplyBounds 被调用）。
	out3, err := a.PlayerSetBounds(100, 50, 800, 600, true)
	if err != nil {
		t.Fatalf("建成后的 setBounds 报错：%v", err)
	}
	if !strings.Contains(out3, `"ok":true`) {
		t.Fatalf("建成后的 setBounds 应 ok:true，实得 %s", out3)
	}
}

// TestEnsureEmbedFailureIsFallbackNotPanic：建造失败（找不到宿主窗/X 不存在）
// 必须是 embedded:false 回退，不 panic、不把 embed 留在半状态。
func TestEnsureEmbedFailureIsFallbackNotPanic(t *testing.T) {
	t.Setenv("ZHENGUO_DATA", t.TempDir())
	a := &App{}
	oldNew := newEmbedHostFunc
	defer func() { newEmbedHostFunc = oldNew }()
	newEmbedHostFunc = func() (embedHost, error) {
		return nil, errors.New("mpv: 未找到宿主主窗口（class=wailsWindow title=真果鉴）")
	}
	out, err := a.PlayerAttach()
	if err != nil {
		t.Fatalf("建造失败不该是 error（回退是正常分支）：%v", err)
	}
	if !strings.Contains(out, `"embedded":false`) || !strings.Contains(out, "未找到宿主主窗口") {
		t.Fatalf("建造失败应回 embedded:false+reason，实得 %s", out)
	}
	// 失败后 embed 仍 nil：后续 setBounds 走 ok:false 降级，不 panic。
	if _, err := a.PlayerSetBounds(0, 0, 640, 360, false); err != nil {
		t.Fatalf("建造失败后 setBounds 不该报错：%v", err)
	}
}

// TestEnsureEmbedSingleFlight：N 个 attach 并发、建造慢——**只建一次**，
// 且都拿到同一宿主（single-flight 语义）。旧「建造在锁里」版本会让 N-1 个
// attach 排队等锁、逐个再建一次（N 次建造）；「先写者赢」版本竞态下会
// 双建并泄漏一个隐藏活窗。
func TestEnsureEmbedSingleFlight(t *testing.T) {
	t.Setenv("ZHENGUO_DATA", t.TempDir())
	a := &App{playerSvc: player.New(player.Config{})}

	var buildCalls int32
	var buildMu sync.Mutex
	oldNew := newEmbedHostFunc
	defer func() { newEmbedHostFunc = oldNew }()
	newEmbedHostFunc = func() (embedHost, error) {
		buildMu.Lock()
		buildCalls++
		buildMu.Unlock()
		time.Sleep(300 * time.Millisecond) // 拉宽竞态窗口
		return &fakeEmbedHost{hwnd: 0x99}, nil
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := a.PlayerAttach(); err != nil {
				errs[i] = err
			}
		}(i)
	}
	wg.Wait()

	buildMu.Lock()
	calls := buildCalls
	buildMu.Unlock()
	if calls != 1 {
		t.Fatalf("single-flight 失效：建造被调 %d 次（期望 1）——并发 attach 各建了一个宿主", calls)
	}
	for i, e := range errs {
		if e != nil {
			t.Fatalf("attach[%d] 报错（并发建造不该失败）：%v", i, e)
		}
	}
	if a.embed == nil || a.embed.HWND() != 0x99 {
		t.Fatalf("并发 attach 后 a.embed 应为唯一宿主 hwnd=0x99，实得 %v", a.embed)
	}
	// 在飞标记必须已清：再次 attach 幂等复用，不触发第二次建造。
	if _, err := a.PlayerAttach(); err != nil {
		t.Fatalf("收尾幂等 attach 报错：%v", err)
	}
	buildMu.Lock()
	defer buildMu.Unlock()
	if c2 := buildCalls; c2 != 1 {
		t.Fatalf("收尾 attach 又触发了建造（buildCalls=%d），在飞标记没清", c2)
	}
}
