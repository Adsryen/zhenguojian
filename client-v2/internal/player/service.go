// Package player 是 wails 壳与 internal/mpv + internal/danmaku 内核之间的服务层（V2-3b）。
//
// 职责（对齐任务卡）：
//   - 懒 spawn：Open 才拉起 mpv（--idle + 双 IPC 连接），Close 回收；
//   - 方法面返回 JSON 字符串，错误沿用壳层「中文消息 [code]」约定；
//   - 事件只发两路：player:tick（500ms 快照）与 player:state（file-loaded/disconnected/exit，附 why_died）；
//   - 弹幕编排：Fetcher 由 tick 驱动，version 变化 → BuildAss → WriteAss → sub-add/sub-reload；
//     本包不 import internal/native——弹幕抓取经注入函数 DanmakuSource 由壳层适配。
//
// v1 语义参照：zhenguojian/app/zhenguojian/player.py（生命周期）、
// main.py 弹幕循环（Tick 驱动 + 同文件原地重写 + sub-reload）。
package player

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"zhenguojian/client-v2/internal/danmaku"
	"zhenguojian/client-v2/internal/mpv"
)

// DanmakuWindowFunc 弹幕窗口抓取注入点（壳层用 native.DanmakuWindow 适配）。
// endMs 为绝对窗尾毫秒，返回 map 即核心 danmaku 动作原始应答
// （{items[],startMs,nextMs,total,windows}）。nil = 无弹幕能力。
type DanmakuWindowFunc func(ctx context.Context, session string, startMs, endMs int64) (map[string]any, error)

// Options 服务装配参数（单测注入 fake 全部走这里）。
type Options struct {
	// Backend 覆盖 mpv 拉起后端（nil = mpv.DefaultBackend()）。
	Backend mpv.Backend
	// MpvPath/AppRoot 透传 mpv.Config；留空走内核兜底链。
	MpvPath    string
	AppRoot    string
	HWDec      bool
	EventsEmit func(name string, data ...any)
	// DanmakuSource nil = 弹幕链路关闭（SetDanmaku 仍透传 sub-visibility）。
	DanmakuSource DanmakuWindowFunc
	// WidProvider 每次 spawn 时取宿主窗口句柄（壳层传 hwnd 提供者）。
	WidProvider func() uintptr
	// TickInterval 事件 tick 周期，<=0 用 500ms（v1 _tick 同值）。
	TickInterval time.Duration
	// FetcherOpt 弹幕抓取节奏覆盖（单测缩窗用）。
	FetcherOpt danmaku.FetcherOptions
	// AssDir ASS 落盘目录；"" = WriteAss 默认 tempdir/zhenguojian-danmaku。
	AssDir string
	// AssKeep ASS 目录保留文件数（<=0 用 WriteAss 默认 6）。
	AssKeep int
}

// Video Open 的入参（前端组装自 Resolve 应答）。
type Video struct {
	URL         string            `json:"url"`
	Title       string            `json:"title"`
	Session     string            `json:"session"` // 弹幕 playbackSession，可空=无弹幕
	Local       bool              `json:"local"`
	DurationSec float64           `json:"durationSec"`
	Headers     map[string]string `json:"headers"`
	Ep          int               `json:"ep"`
}

// Service 播放服务。并发模型：单一 mu 串行化所有状态变更与 mpv 命令；
// tick/事件消费各自 goroutine，经 done 通道随 Close/替换 player 退出。
type Service struct {
	opts Options

	mu         sync.Mutex
	ctx        context.Context // OnStartup 注入；nil 前 Open 报错
	closing    bool
	player     *mpv.Player
	playerDone chan struct{}
	tickDone   chan struct{}
	video      Video
	opened     bool
	rebuilds   int

	// 弹幕编排状态
	fetcher      *danmaku.Fetcher
	dmOn         bool
	dmVersion    int64
	subAdded     bool
	assPath      string
	assDirty     bool
	viewW, viewH int
}

// New 构造服务（不拉起任何资源）。
func New(opts Options) *Service {
	if opts.TickInterval <= 0 {
		opts.TickInterval = 500 * time.Millisecond
	}
	s := &Service{opts: opts, dmOn: true}
	if opts.DanmakuSource != nil {
		s.fetcher = danmaku.NewFetcher(&sourceAdapter{fn: opts.DanmakuSource, svc: s}, opts.FetcherOpt)
	}
	return s
}

// sourceAdapter 把注入的 map 形窗口应答转成 danmaku.Window。
type sourceAdapter struct {
	fn  DanmakuWindowFunc
	svc *Service
}

func (a *sourceAdapter) FetchWindow(ctx context.Context, session string, startMs, endMs int64) (danmaku.Window, error) {
	m, err := a.fn(ctx, session, startMs, endMs)
	if err != nil {
		return danmaku.Window{}, err
	}
	var w danmaku.Window
	if len(m) > 0 {
		// 核心返回 {items[],startMs,nextMs,total,windows}，字段名大小写不敏感匹配。
		b, err := json.Marshal(m)
		if err != nil {
			return danmaku.Window{}, fmt.Errorf("弹幕窗口重编码失败: %w", err)
		}
		if err := json.Unmarshal(b, &w); err != nil {
			return danmaku.Window{}, fmt.Errorf("弹幕窗口结构不识别: %w", err)
		}
	}
	return w, nil
}

// OnStartup 由 wails OnStartup 调用（生命周期 ctx）。
func (s *Service) OnStartup(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
}

// OnShutdown 由 wails OnShutdown 调用：停循环、退进程。幂等。
func (s *Service) OnShutdown(ctx context.Context) {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	_, _ = s.Close()
}

// ── 方法面（全部返回 JSON 字符串；错误按「中文 [code]」） ──

// Open 拉起/复用 mpv 并换源。重复 Open = 换源（v1 load 语义）。
func (s *Service) Open(videoJSON string) (string, error) {
	var v Video
	if err := json.Unmarshal([]byte(videoJSON), &v); err != nil {
		return "", fail("bad_video", "播放参数解析失败: "+err.Error())
	}
	if v.URL == "" {
		return "", fail("bad_video", "播放参数缺少 url")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLive(); err != nil {
		return "", err
	}
	p, err := s.ensureStartedLocked()
	if err != nil {
		return "", err
	}
	if err := s.applySourceLocked(p, v); err != nil {
		return "", err
	}
	return `{"ok":true}`, nil
}

func (s *Service) Pause() (string, error) {
	return s.simple(func(p *mpv.Player) error { return p.Pause() })
}
func (s *Service) Resume() (string, error) {
	return s.simple(func(p *mpv.Player) error { return p.Play() })
}

// Seek 进度条跳转：positionMs 绝对毫秒 → absolute+exact（v1 进度条语义）。
func (s *Service) Seek(positionMs float64) (string, error) {
	return s.simple(func(p *mpv.Player) error { return p.AbsSetPosition(positionMs / 1000) })
}

// SetSpeed 倍速：限幅 0.5–3.0（v1 单点入口语义 player.py:1870 吸附+应用一体；
// v1 档位表 SPEED_CHOICES=0.5/0.75/1/1.25/1.5/2/3 player.py:61，前端只发档位值，
// 服务端限幅兜底越界输入）。
func (s *Service) SetSpeed(v float64) (string, error) {
	if v < 0.5 {
		v = 0.5
	}
	if v > 3.0 {
		v = 3.0
	}
	return s.simple(func(p *mpv.Player) error { return p.SetSpeed(v) })
}

// SetVolume 音量：钳位 0–100（v1 set_volume player.py:1905 max(0,min(100))；
// 前端持久化键 zgj.player.volume 由 stores/player.ts 负责）。
func (s *Service) SetVolume(v int) (string, error) {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return s.simple(func(p *mpv.Player) error { return p.SetVolume(v) })
}

// SetDanmaku 弹幕开关：只切 sub-visibility，不丢抓取进度；
// 关闭期间 tick 不再喂 Fetcher（不请求）。重新开启强制重建 ASS 补挂。
func (s *Service) SetDanmaku(on bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.livePlayerLocked()
	if p == nil {
		s.dmOn = on // 记录意图，下次 Open 生效
		return `{"ok":true}`, nil
	}
	s.dmOn = on
	if err := p.SetSubVisibility(on); err != nil {
		return "", failCmd(err)
	}
	if on {
		s.assDirty = true // 补挂/重读
		s.refreshAssLocked(p)
	}
	return `{"ok":true}`, nil
}

// ReportViewport 前端上报播放器区域像素尺寸（ASS 动态 PlayRes）。
func (s *Service) ReportViewport(w, h int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w <= 0 || h <= 0 {
		return "", fail("bad_video", "viewport 尺寸无效")
	}
	if s.viewW != w || s.viewH != h {
		s.viewW, s.viewH = w, h
		s.assDirty = true
		if p := s.livePlayerLocked(); p != nil {
			s.refreshAssLocked(p)
		}
	}
	return `{"ok":true}`, nil
}

// Close 退出 mpv 并复位。幂等；未打开时是 no-op。
func (s *Service) Close() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teardownLocked()
	return `{"ok":true}`, nil
}

// State 同步快照（JSON）。未打开返回 running=false 的空态。
func (s *Service) State() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]any{
		"running":   s.player != nil && s.player.Alive(),
		"opened":    s.opened,
		"danmakuOn": s.dmOn,
		"ep":        s.video.Ep,
		"title":     s.video.Title,
	}
	if p := s.player; p != nil && p.Alive() {
		st := p.State()
		out["position"] = st.Position
		out["duration"] = st.Duration
		out["paused"] = st.Paused
		out["speed"] = st.Speed
		out["volume"] = st.Volume
		out["eof"] = st.EofReached
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// ── 内部：生命周期 ──

func (s *Service) checkLive() error {
	if s.ctx == nil {
		return fail("not_ready", "播放服务尚未初始化")
	}
	if s.closing {
		return fail("closed", "播放服务已关闭")
	}
	return nil
}

// simple 在已打开且存活 player 上执行一条命令。
func (s *Service) simple(fn func(p *mpv.Player) error) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.livePlayerLocked()
	if p == nil {
		return "", fail("not_open", "当前没有播放中的视频")
	}
	if err := fn(p); err != nil {
		return "", failCmd(err)
	}
	return `{"ok":true}`, nil
}

func (s *Service) livePlayerLocked() *mpv.Player {
	if s.player != nil && s.player.Alive() {
		return s.player
	}
	return nil
}

// ensureStartedLocked 懒 spawn：Alive 则复用，坏死 player 先回收。
func (s *Service) ensureStartedLocked() (*mpv.Player, error) {
	if p := s.livePlayerLocked(); p != nil {
		return p, nil
	}
	if s.player != nil {
		s.retirePlayerLocked()
	}
	if err := s.checkLive(); err != nil {
		return nil, err
	}
	p, err := s.spawnLocked()
	if err != nil {
		return nil, err
	}
	s.startTickLocked()
	return p, nil
}

func (s *Service) spawnLocked() (*mpv.Player, error) {
	var wid uintptr
	if s.opts.WidProvider != nil {
		wid = s.opts.WidProvider()
	}
	p := mpv.New(mpv.Config{
		MpvPath: s.opts.MpvPath,
		AppRoot: s.opts.AppRoot,
		Wid:     wid,
		HWDec:   s.opts.HWDec,
		Backend: s.opts.Backend,
	})
	if err := p.Start(s.ctx); err != nil {
		return nil, fail("player_start_failed", err.Error())
	}
	s.attachPlayerLocked(p)
	return p, nil
}

// attachPlayerLocked 接管 player：注册事件消费（含 done 通道）。
func (s *Service) attachPlayerLocked(p *mpv.Player) {
	done := make(chan struct{})
	s.player = p
	s.playerDone = done
	go s.consumeEvents(p, done)
}

// retirePlayerLocked 摘除当前 player 并异步 Quit（不等进程回收，避免拖住 UI）。
func (s *Service) retirePlayerLocked() {
	p, done := s.player, s.playerDone
	s.player, s.playerDone = nil, nil
	if done != nil {
		close(done)
	}
	if p != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			_ = p.Quit(ctx)
		}()
	}
}

// teardownLocked Close/Shutdown 共用：停 tick、退 player、清弹幕编排态。
func (s *Service) teardownLocked() {
	if s.tickDone != nil {
		close(s.tickDone)
		s.tickDone = nil
	}
	p, done := s.player, s.playerDone
	s.player, s.playerDone = nil, nil
	if done != nil {
		close(done)
	}
	if p != nil {
		ctx := context.Background()
		if s.ctx != nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(s.ctx, 4*time.Second)
			defer cancel()
		}
		_ = p.Quit(ctx)
	}
	s.opened = false
	s.video = Video{}
	s.subAdded = false
	s.assPath = ""
	s.assDirty = false
	s.dmVersion = 0
	s.rebuilds = 0
}

// applySourceLocked 换源：LoadWith + Fetcher.SetEpisode + 编排态复位。
func (s *Service) applySourceLocked(p *mpv.Player, v Video) error {
	if err := p.LoadWith(v.URL, v.Headers, v.Title, mpv.LoadOptions{Local: v.Local}); err != nil {
		return failCmd(err)
	}
	s.video = v
	s.opened = true
	s.rebuilds = 0
	s.subAdded = false
	s.assPath = ""
	s.assDirty = false
	if s.fetcher != nil {
		eps := danmaku.Episode{}
		if v.Session != "" {
			eps = danmaku.Episode{
				Session:    v.Session,
				DurationMs: int64(v.DurationSec * 1000),
				Key:        s.assKey(),
			}
		}
		// 无会话 = 清窗，避免上一集 items 渲染进本集
		s.fetcher.SetEpisode(eps)
		_, _, _, ver := s.fetcher.Stats()
		s.dmVersion = ver
	}
	return nil
}

func (s *Service) assKey() string {
	name := s.video.Title
	if name == "" {
		name = "episode"
	}
	return name + "-ep" + strconv.Itoa(s.video.Ep)
}

// ── tick 循环（player:tick 500ms + Fetcher 驱动） ──

func (s *Service) startTickLocked() {
	if s.tickDone != nil {
		return
	}
	done := make(chan struct{})
	s.tickDone = done
	interval := s.opts.TickInterval
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				s.tickOnce()
			}
		}
	}()
}

func (s *Service) tickOnce() {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.livePlayerLocked()
	if p == nil || !s.opened {
		return
	}
	st := p.State()
	// Open 时时长未知（durationSec<=0）：拿到真实时长后补 SetEpisode，
	// 解锁 Fetcher（DurationMs<=0 时 Tick 被短路）。只补一次。
	if s.video.DurationSec <= 0 && st.Duration > 0 {
		s.video.DurationSec = st.Duration
		if s.fetcher != nil && s.video.Session != "" {
			s.fetcher.SetEpisode(danmaku.Episode{
				Session:    s.video.Session,
				DurationMs: int64(s.video.DurationSec * 1000),
				Key:        s.assKey(),
			})
			_, _, _, ver := s.fetcher.Stats()
			s.dmVersion = ver
		}
	}
	if s.dmOn && s.fetcher != nil {
		s.fetcher.Tick(int64(st.Position * 1000))
		s.refreshAssLocked(p)
	}
	note := ""
	count := 0
	if s.fetcher != nil && s.video.Session != "" {
		_, c, _, _ := s.fetcher.Stats()
		count = c
		note = s.fetcher.Note()
	}
	payload := map[string]any{
		"position":     st.Position,
		"duration":     st.Duration,
		"title":        s.video.Title,
		"paused":       st.Paused,
		"speed":        st.Speed,
		"volume":       st.Volume,
		"eof":          st.EofReached,
		"danmakuNote":  note,
		"danmakuCount": count,
	}
	s.emitLocked("player:tick", marshalString(payload))
}

// refreshAssLocked 版本变化（或视口/开关脏标记）→ 全量重建 ASS → 落盘 →
// 首挂 sub-add / 原地 sub-reload（v1 同文件名 + 循环尾部 sub-reload）。
func (s *Service) refreshAssLocked(p *mpv.Player) {
	if s.fetcher == nil || s.video.Session == "" {
		return
	}
	_, count, _, ver := s.fetcher.Stats()
	if ver == s.dmVersion && !s.assDirty {
		return
	}
	s.dmVersion = ver
	s.assDirty = false
	if count == 0 {
		return
	}
	st := p.State()
	durSec := s.video.DurationSec
	if st.Duration > durSec {
		durSec = st.Duration
	}
	durMs := int64(durSec * 1000)
	text := danmaku.BuildAss(s.fetcher.Items(), durMs, danmaku.AssOptions{Width: s.viewW, Height: s.viewH})
	if text == "" {
		return
	}
	path, err := danmaku.WriteAss(s.opts.AssDir, s.assKey(), text, s.opts.AssKeep)
	if err != nil {
		return
	}
	s.assPath = path
	if s.subAdded {
		_ = p.SubReload()
		return
	}
	if st.FileLoaded {
		if err := p.SubAdd(path, "弹幕"); err == nil {
			s.subAdded = true
		}
	}
}

// ── player 事件 → player:state / 断线重建 ──

func (s *Service) consumeEvents(p *mpv.Player, done chan struct{}) {
	for {
		select {
		case <-done:
			return
		case ev := <-p.Events():
			s.handleEvent(p, ev)
		}
	}
}

func (s *Service) handleEvent(p *mpv.Player, ev mpv.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.player != p {
		return // 已 retire 的旧 player 噪声
	}
	switch ev.Kind {
	case mpv.EventFileLoaded:
		s.assDirty = true // 新文件 = 字幕轨全丢，重挂
		s.refreshAssLocked(p)
		s.emitLocked("player:state", stateJSON("file-loaded", "", p))
	case mpv.EventDisconnected:
		why := errText(ev.Err)
		s.emitLocked("player:state", stateJSON("disconnected", why, p))
		s.rebuildLocked(p, why)
	case mpv.EventExit:
		why := errText(ev.Err)
		s.emitLocked("player:state", stateJSON("exit", why, p))
		// 进程已亡，不做 Quit；摘除 player 并关闭消费通道，
		// 本 goroutine（consumeEvents）随 range 下一轮 select 退出。
		done := s.playerDone
		s.player, s.playerDone = nil, nil
		if done != nil {
			close(done)
		}
		s.opened = false
		s.subAdded = false
	case mpv.EventEndFile:
		// eof 经 tick 快照传达，不单独发事件（任务定案：state 仅三类）
	}
}

// rebuildLocked 断线自动重建：新 spawn → LoadWith 同源 → 恢复倍速/音量/暂停
// → AbsSeekSmooth 续播 → 弹幕经 assDirty 在 file-loaded 后重挂。
// 失败放弃（running=false），等用户重新 Open。
func (s *Service) rebuildLocked(old *mpv.Player, why string) {
	snap := old.State()
	if s.rebuilds >= 2 || s.video.URL == "" {
		return
	}
	s.rebuilds++
	_, done := s.player, s.playerDone
	s.player, s.playerDone = nil, nil
	if done != nil {
		close(done)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_ = old.Quit(ctx)
	}()
	p, err := s.spawnLocked()
	if err != nil {
		s.emitLocked("player:state", marshalString(map[string]any{
			"state": "disconnected", "why_died": why + "；重建失败: " + err.Error(),
		}))
		return
	}
	if err := p.LoadWith(s.video.URL, s.video.Headers, s.video.Title, mpv.LoadOptions{Local: s.video.Local}); err != nil {
		s.emitLocked("player:state", marshalString(map[string]any{
			"state": "disconnected", "why_died": why + "；重挂源失败: " + err.Error(),
		}))
		return
	}
	if snap.Speed > 0 && snap.Speed != 1 {
		_ = p.SetSpeed(snap.Speed)
	}
	if snap.Volume > 0 {
		_ = p.SetVolume(snap.Volume)
	}
	if snap.Paused {
		_ = p.Pause()
	}
	if snap.Position > 0 {
		_ = p.AbsSeekSmooth(snap.Position) // v1 续播语义：absolute 允许落关键帧
	}
	s.subAdded = false
	s.assDirty = true // items 还在 fetcher 里，version 未必变；脏标记保证重挂
}

// ── 小工具 ──

func stateJSON(state, why string, p *mpv.Player) string {
	out := map[string]any{"state": state}
	if why != "" {
		out["why_died"] = why
	}
	return marshalString(out)
}

func (s *Service) emitLocked(name string, data ...any) {
	if s.opts.EventsEmit == nil {
		return
	}
	s.opts.EventsEmit(name, data...)
}

func marshalString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func fail(code, msg string) error {
	return fmt.Errorf("%s [%s]", msg, code)
}

func failCmd(err error) error {
	return fail("command_failed", "播放器指令失败: "+err.Error())
}
