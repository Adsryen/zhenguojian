import io
import os

base = os.path.dirname(os.path.abspath(__file__))


def rw(rel, reps):
    p = os.path.join(base, rel)
    s = io.open(p, encoding='utf-8').read()
    for old, new in reps:
        c = s.count(old)
        assert c == 1, (rel, old[:64], c)
        s = s.replace(old, new)
    io.open(p, 'w', encoding='utf-8').write(s)
    print('OK', rel)


# ── 1) mpv：把"mpv 自己发了 shutdown 事件"变成可判定的类型化信号 ──
rw('internal/mpv/player.go', [
    ('''		EventExit         EventKind = "exit"         // mpv 进程退出（Err 带 why_died）''',
     '''		EventExit         EventKind = "exit"         // mpv 进程退出（Err 带 why_died）
	)

	// ErrGracefulShutdown 标记"mpv 自己发了 shutdown 事件"的退出：
	// 用户点视频窗的 X、或我们下发 quit，都走这条路。
	//
	// 第五十九轮（真机反馈）加这个哨兵的原因：服务层原来只分
	// "quitting/closed 才算用户退出"，于是**用户从 mpv 窗口自己点关闭**
	// 被当成意外死亡 → 自动重建 → 窗口刚关掉又弹出来（用户原话：
	// 「关闭又会被二次打开」）。v1 根本没有死亡重建
	// （player.py grep rebuild/respawn 零命中），所以这条差异是 v2 自加的，
	// 真机上直接成了 bug。有类型化信号就能把"体面关窗"和"真崩了"分开。
	var ErrGracefulShutdown = errors.New("mpv: 收到 shutdown 事件")'''),
    ('''			p.publish(Event{Kind: EventExit, Err: errors.New("mpv: 收到 shutdown 事件")})''',
     '''			p.publish(Event{Kind: EventExit, Err: ErrGracefulShutdown})'''),
])

# ── 2) 服务层：体面关窗不重建；新增非终态 Stop ──
rw('internal/player/service.go', [
    ('''	case mpv.EventDisconnected, mpv.EventExit:
		if s.quitting || s.closed {
			return
		}''',
     '''	case mpv.EventDisconnected, mpv.EventExit:
		if s.quitting || s.closed {
			return
		}
		// 用户从 mpv 窗口点关闭：mpv 会先发 shutdown 事件再退出。
		// 这不是崩溃，不能重建（重建就等于"把用户刚关掉的窗又开起来"）。
		// 报一个 stopped 让界面回到可再播的状态。
		if errors.Is(ev.Err, mpv.ErrGracefulShutdown) {
			s.log("player: mpv 体面退出（用户关窗或 quit），不重建")
			s.stopSessionLocked()
			s.emit("player:stopped", map[string]any{})
			return
		}'''),
    ('''// Close 停服务：quit mpv + 清弹幕状态。可重复调用（幂等）。''',
     '''// Stop 只停"这一次播放"：quit mpv、清会话状态，但**服务仍可再次 Open**。
//
// 第五十九轮（真机反馈）新增。此前 `PlayerClosePlayer` 直接调 `Close()`，
// 而 `Close()` 会置 `closed=true` 且 `Open()` 见到它就返回
// `ErrClosed`（service.go:355/382）—— 这个 service 是**应用生命周期**的
// （app.go 只建一次），于是用户点一次「关闭」之后，
// 本进程内所有后续播放永远失败在「player: 服务已关闭」。
// 真机实测正是这条：截图里的红字就是 ErrClosed。
//
// v1 的语义是"停止播放"与"退出程序"两件事：
// `player.stop()` 之后照样能再播；只有 app 退出才走终态清理。
func (s *Service) Stop() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.quitting = true // 抑制这次 quit 触发的重建
	p := s.p
	s.p = nil
	s.stopSessionLocked()
	if s.pumpStop != nil {
		close(s.pumpStop)
		s.pumpStop = nil
	}
	s.mu.Unlock()

	if p != nil {
		// v1 `stop(blocking=False)` 的同款理由：关窗/换集都不能冻界面
		// （player.py:728-735 明写 "switching episodes never freezes the window"）。
		// quit 有 1s 上限，超时后 Terminate/Kill 兜底，都在 goroutine 里。
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := p.Quit(ctx); err != nil {
				s.log("player: stop 时 quit 失败（已尽力 terminate）: %v", err)
			}
		}()
	}
	return nil
}

// stopSessionLocked 清掉一次会话的运行时状态（调用方须持锁）。
// 与 Close 的区别只在"是否把服务判死"。
func (s *Service) stopSessionLocked() {
	s.fetcher = nil
	s.req = nil
	s.plan = nil
	s.subAttached = false
	s.assPath = ""
}

// Close 停服务：quit mpv + 清弹幕状态。可重复调用（幂等）。
// ⚠ 终态：置 closed 后本服务不再接受 Open —— 只用于应用退出。'''),
])

# ── 3) Open 复位 quitting（否则第二次播放会被上一次的 quit 抑制）──
rw('internal/player/service.go', [
    ('''	s.req = &Request{DramaID: req.DramaID, Title: req.Title, Index: req.Index,
		Drama: req.Drama, Chapter: req.Chapter, Quality: req.Quality}''',
     '''	// 上一集可能是 Stop()/体面退出留下的 quitting，这次必须清掉，
	// 否则本集真崩了也不会重建（抑制标志的作用域只能是"这一次会话"）。
	s.quitting = false
	s.req = &Request{DramaID: req.DramaID, Title: req.Title, Index: req.Index,
		Drama: req.Drama, Chapter: req.Chapter, Quality: req.Quality}'''),
])

# ── 4) 绑定层：关闭=停这次播放，不是判死服务 ──
rw('app.go', [
    ('''func (a *App) PlayerClosePlayer() (string, error)    { return "", a.playerSvc.Close() }''',
     '''// PlayerClosePlayer 停"这一次播放"（Stop，非终态）：
// 第五十九轮真机反馈——这里原来调 Close()（终态），
// 用户点一次关闭后本进程内再也播不了（「player: 服务已关闭」）。
// 应用退出的终态清理在 app.go:157 那一路，仍然走 Close()。
func (a *App) PlayerClosePlayer() (string, error)    { return "", a.playerSvc.Stop() }'''),
])
