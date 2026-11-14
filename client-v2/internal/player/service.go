// Package player 是 v2 客户端的播放器服务层：mpv 生命周期编排 + 弹幕管线，
// 进程内运行，供 app.go 接线、前端 PlayerView.vue 订阅。不含任何 UI。
//
// 前端契约（Emit 出口转发给 runtime.EventsEmit 的事件名与 payload）：
//
//	player:tick    每 TickInterval（默认 500ms）一次的轻量状态快照：
//	               {"playing":bool,"posMs":int64,"durationMs":int64,
//	                "speed":float64,"volume":int,
//	                "danmaku":{"enabled":bool,"count":int,"note":string},
//	                "episode":{"dramaID":string,"title":string,"index":int}}
//	               仅在已 Open（有播放器实例）时发送；前端零轮询。
//	player:ended   本集播完（end-file reason=eof 或 eof-reached 兜底，每集一次）：
//	               {"episode":{"dramaID":...,"title":...,"index":...}}
//	player:error   播放/重建失败等人话错误：{"message":string}
//	danmaku:stats  弹幕抓取进度或备注变化：{"count":int,"note":string}
//
// 会话语义：Service 单实例串行（一把锁），Open 换集复用同一 mpv 进程
// （loadfile replace），mpv 进程死亡/IPC 断开时自动重建并从上次位置续播，
// 连续失败超过 MaxRebuilds 次放弃并发 player:error。
package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"zhenguojian/client-v2/internal/danmaku"
	"zhenguojian/client-v2/internal/mpv"
)

// ErrNotOpen 表示还没有播放会话就调用了控制方法。
var ErrNotOpen = errors.New("player: 尚未打开播放会话")

// ErrClosed 表示服务已 Close。
var ErrClosed = errors.New("player: 服务已关闭")

// Plan 是 Resolver 解析一集给出的播放计划。JSON 名对齐核心 nativePlan
// （guoapp-src/native/core/app_runtime.go:145）：会话键是 `session`，
// `playbackSession` 只是核心 danmaku 动作的入参名。核心当前不产出
// mime/durationSeconds，字段留空即可（时长走 mpv duration 属性兜底）。
type Plan struct {
	URL           string            `json:"url"`
	Headers       map[string]string `json:"headers"`
	Mime          string            `json:"mime,omitempty"`
	DecryptionKey string            `json:"decryptionKey,omitempty"`
	DurationSec   float64           `json:"durationSeconds,omitempty"` // 0 = 未知，以 mpv 实测时长为准
	Session       string            `json:"session,omitempty"`         // 弹幕抓取会话；空 = 本集无弹幕会话
	Local         bool              `json:"local,omitempty"`
	Quality       int               `json:"quality,omitempty"`
}

// Request 是一次 Open 的请求：drama/chapter 为详情页透传的原样对象，
// 由注入的 Resolver 换回 Plan。Index 是 1 起始集号（ASS 文件名 key 用）。
type Request struct {
	DramaID string
	Title   string
	Index   int
	Drama   map[string]any
	Chapter map[string]any
	// Quality 是核心 resolve 档位：0=自动，其余为 540/720/1080。
	Quality int
}

// Resolver 把 (drama, chapter, index) 解析成播放计划。
// 生产实现由 app.go 适配 internal/native.Resolve；单测注入假实现。
type Resolver interface {
	Resolve(ctx context.Context, req Request) (Plan, error)
}

// MpvPlayer 是 Service 用到的 mpv.Player 方法子集（*mpv.Player 天然满足）。
type MpvPlayer interface {
	Start(ctx context.Context) error
	LoadWith(url string, headers map[string]string, title string, opts mpv.LoadOptions) error
	Play() error
	Pause() error
	AbsSeekSmooth(seconds float64) error
	SetSpeed(rate float64) error
	SetVolume(level int) error
	SubAdd(path, title string) error
	SubReload() error
	SubRemove() error
	State() mpv.State
	Alive() bool
	Events() <-chan mpv.Event
	Quit(ctx context.Context) error
}

// MpvLauncher 按配置造一个播放器实例（不 Start）。默认包装 mpv.New。
type MpvLauncher func(cfg mpv.Config) MpvPlayer

// Logger 最小日志接口（*log.Logger 满足）；nil = 静默。
type Logger interface {
	Printf(format string, v ...any)
}

// DanmakuWindowFunc 与 internal/native.DanmakuWindow 同形：
// endMs 是绝对窗尾毫秒（核心 danmaku 动作的 durationMs 语义，同 v1）。
type DanmakuWindowFunc func(session string, startMs, endMs int64) (map[string]any, error)

// NewDanmakuSource 把核心门面的窗口函数适配成 danmaku.Source，
// 供 Config.DanmakuSource 注入（app.go 传 native.DanmakuWindow 即可）。
func NewDanmakuSource(fn DanmakuWindowFunc) danmaku.Source { return coreSource{fn: fn} }

type coreSource struct{ fn DanmakuWindowFunc }

// FetchWindow 忽略 ctx（核心门面调用本身同步且自带超时）。
func (c coreSource) FetchWindow(_ context.Context, session string, startMs, endMs int64) (danmaku.Window, error) {
	raw, err := c.fn(session, startMs, endMs)
	if err != nil {
		return danmaku.Window{}, err
	}
	// 核心回复形状 {items[], startMs, nextMs, total, windows}；
	// 经 JSON 往返落到 danmaku.Window（Item 的 tag 与核心行字段一致）。
	buf, err := json.Marshal(raw)
	if err != nil {
		return danmaku.Window{}, fmt.Errorf("player: 弹幕窗口编码失败: %w", err)
	}
	var decoded struct {
		Items   []map[string]any `json:"items"`
		NextMs  int64            `json:"nextMs"`
		Total   int64            `json:"total"`
		Windows map[string]int64 `json:"windows"`
	}
	if err := json.Unmarshal(buf, &decoded); err != nil {
		return danmaku.Window{}, fmt.Errorf("player: 弹幕窗口解码失败: %w", err)
	}
	return danmaku.Window{
		Items:   danmaku.FromMaps(decoded.Items),
		NextMs:  decoded.NextMs,
		Total:   decoded.Total,
		Windows: decoded.Windows,
	}, nil
}

// Config 注入 Service 的全部外部依赖；零值走括号里的默认。
// openResolveTimeout 是 Open 阶段解析播放地址的硬上限（见 Open 内注释）。
const openResolveTimeout = 60 * time.Second

type Config struct {
	Resolver Resolver // Open 取播放计划；nil → Open 直接报错
	// MpvLauncher 造 mpv 实例；nil = 默认包装 mpv.New。
	// 单测注入假实例集合，可在断线事件后验证自动重建续播。
	MpvLauncher MpvLauncher
	// DanmakuSource 弹幕窗口源（NewDanmakuSource(native.DanmakuWindow) 适配）；
	// nil = 弹幕禁用（只发 player:tick 的 danmaku 快照，不抓取）。
	DanmakuSource danmaku.Source
	// Emit 事件出口；app.go 接线 runtime.EventsEmit。契约：非阻塞。
	Emit      func(event string, payload any)
	DataDir   string // 数据根目录（ASS 兜底路径用）
	AssDir    string // ASS 输出目录；"" → DataDir/ass；仍 "" → danmaku.WriteAss 临时目录默认
	MpvPath   string
	AppRoot   string
	Wid       uintptr
	HWDec     bool
	MpvSocket string      // 空 → mpv 默认（同 socket 覆盖式重启，v1 语义）
	Backend   mpv.Backend // 单测注入 net.Pipe 假后端；nil → 平台默认
	Logger    Logger

	TickInterval time.Duration          // 默认 500ms（对齐 v1 界面刷新节奏）
	MaxRebuilds  int                    // 断线自动重建上限，默认 3；超限发 player:error 放弃
	AssKeep      int                    // 每剧保留的 ASS 文件数；0 → WriteAss 默认
	FetcherOpts  danmaku.FetcherOptions // 弹幕抓取节奏覆盖；零值 = v1 默认
	// DanmakuOpacity 初始不透明度（0.05–1.0）；0 = 未设 → 按 1.0 不透明。
	// 对齐 v1 danmaku 面板的「透明 NN%」滑条初值来源（v1 读 app-settings.json）。
	DanmakuOpacity float64
}

// DanmakuState 快照里的弹幕部分。
type DanmakuState struct {
	Enabled bool    `json:"enabled"`
	Count   int     `json:"count"`
	Note    string  `json:"note"`
	// Opacity 当前不透明度（0.05–1.0）。v1 把它存在 app-settings.json，
	// v2 由前端存 zgj.player.danmakuOpacity 并在开弹幕时回灌；这里只报当前生效值，
	// 未设过（内部 0）一律报 1.0，避免前端拿到 0 又渲染成"全透明"。
	Opacity float64 `json:"opacity"`
}

// EpisodeRef 快照里的当前集标识。
type EpisodeRef struct {
	DramaID string `json:"dramaID"`
	Title   string `json:"title"`
	Index   int    `json:"index"`
}

// Snapshot 是 player:tick 的 payload，也是 State() 的返回值。
type Snapshot struct {
	Playing    bool         `json:"playing"`
	PosMs      int64        `json:"posMs"`
	DurationMs int64        `json:"durationMs"`
	Speed      float64      `json:"speed"`
	Volume     int          `json:"volume"`
	Danmaku    DanmakuState `json:"danmaku"`
	Episode    EpisodeRef   `json:"episode"`
	// Local 为真表示这是一次**本地成品文件**会话。
	// 为什么非要有这个字段：前端原先用 `!!snapshot.episode.index` 判断"会话是否已打开"
	// （stores/player.ts 的 applySnapshot），而本地文件**没有集号**（v1 同理：
	// open_local 会把选集条清空）——于是一加入本地播放，本地会话就被读成"没打开"：
	// running=false → 进度节流落盘整条不触发（`history_local_play_verify` ⑤ 实测到：
	// 等了 7 秒，localStorage 里那条历史记录一个字段都没变）。
	// 也就是说 `!!index` 只兼容了我想象里的形状（第 28 轮那条教训的又一副面孔），
	// 而"是不是本地会话"本来就该由契约自己说出来，不该由集号间接猜。
	Local bool `json:"local,omitempty"`
}

// Service 播放器生命周期 + 弹幕管线编排。所有公开方法可并发调用，
// 内部一把锁保证单实例串行；事件在其间发出，Emit 必须非阻塞。
type Service struct {
	cfg Config

	mu       sync.Mutex
	ctx      context.Context // 服务生命周期（Close 取消）
	cancel   context.CancelFunc
	closed   bool
	quitting bool // 用户主动 Quit mpv 中：抑制断线自动重建

	// 当前会话
	req      *Request
	plan     *Plan
	p        MpvPlayer
	pumpStop chan struct{} // 当前实例事件泵的停止信号

	// 播放意图（重建后回放到新实例）
	wantPaused bool
	wantSpeed  float64
	wantVolume int

	// 窗口内嵌：mpv --wid 宿主句柄。初值取 Config.Wid（0=独立窗），
	// PlayerAttach 经 SetWid 惰性翻转为 ZgjVideoHost 子窗句柄。
	wid uintptr

	// 弹幕
	enabled     bool // Config.DanmakuSource 非 nil 时默认开
	opacity     float64 // 弹幕不透明度 0.05–1.0；0 = 未设（按 1.0，见 danmaku.AssOptions.normalize）
	fetcher     *danmaku.Fetcher
	subAttached bool
	lastVersion int64
	assPath     string
	// R22a③：本地播放时挂在同名 .ass 上的那条弹幕轨（v1 main.py:1880-1895）。
	// 线上会话恒空——两条路径的"弹幕从哪来"完全不同，共用一个字段会互相冒充。
	sidecarAss   string
	sidecarCount int64

	// 事件泵杂项
	lastPosMs       int64 // 上一次 tick 位置（跳变检测）
	pendingResumeMs int64 // 重建后等新 file-loaded 再续播（v1 resume 时机）
	rebuilds        int   // 本集已自动重建次数（Open/成功加载后清零）
	endedFired      bool  // 本集 player:ended 是否已发（end-file 与 eof-reached 双通道去重）
	durMs           int64 // 权威时长：plan 给出或 mpv duration 属性（取先到的有效值）

	// danmaku:stats 去重缓存
	lastNote  string
	lastCount int
}

// New 组装服务（不启动任何进程；Open 才 spawn）。
func New(cfg Config) *Service {
	if cfg.MpvLauncher == nil {
		cfg.MpvLauncher = func(mc mpv.Config) MpvPlayer { return mpv.New(mc) }
	}
	if cfg.Emit == nil {
		cfg.Emit = func(string, any) {}
	}
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = 500 * time.Millisecond
	}
	if cfg.MaxRebuilds <= 0 {
		cfg.MaxRebuilds = 3
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		cfg:        cfg,
		wid:        cfg.Wid,
		ctx:        ctx,
		cancel:     cancel,
		wantSpeed:  1.0,
		wantVolume: 100,
		enabled:    cfg.DanmakuSource != nil,
		// 初始不透明度来自 Config（v1 是 app-settings.json 里的 danmaku_opacity；
		// v2 前端存 zgj.player.danmakuOpacity 并在开弹幕时回灌）。
		// 零值 = 未设，由 opacityValueLocked / ass.go 统一按 1.0 处理。
		opacity: cfg.DanmakuOpacity,
	}
	if cfg.DanmakuSource != nil {
		s.fetcher = danmaku.NewFetcher(cfg.DanmakuSource, cfg.FetcherOpts)
	}
	go s.loop()
	return s
}

func (s *Service) log(format string, v ...any) {
	if s.cfg.Logger != nil {
		s.cfg.Logger.Printf(format, v...)
	}
}

func (s *Service) emit(event string, payload any) {
	if s.cfg.Emit != nil {
		s.cfg.Emit(event, payload)
	}
}

// mpvConfig 每次 spawn 用的内核配置（Launcher 为 nil 包装时才真正生效）。
// Wid 取 s.wid 而非 cfg.Wid：断线重建走同一条路径，内嵌句柄自动延续。
func (s *Service) mpvConfig() mpv.Config {
	return mpv.Config{
		MpvPath:    s.cfg.MpvPath,
		AppRoot:    s.cfg.AppRoot,
		SocketPath: s.cfg.MpvSocket,
		Wid:        s.wid,
		HWDec:      s.cfg.HWDec,
		Backend:    s.cfg.Backend,
	}
}

// SetWid 切换 mpv --wid 宿主句柄（内嵌接线 PlayerAttach 在进播放页、
// Open 之前调用）。只影响下一次 spawn：现存实例保持当前窗口语义；
// 0 = 独立顶层窗（ZHENGUO_NO_EMBED 回退路径）。
func (s *Service) SetWid(wid uintptr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wid = wid
}

// PlayerSetHitBands 更新视频 host 窗的点击穿透带（物理像素上/下高；
// 0=该侧不禁）。与 SetWid 同属 embed 层接线：不经 mpv 进程/IPC，直写
// mpv 包级状态，由 WM_NCHITTEST 即时消费；非 Windows 存而无消费（no-op）。
func (s *Service) PlayerSetHitBands(top, bottom int32) {
	mpv.SetHitBands(top, bottom)
}

// assDirPath ASS 输出目录：AssDir → DataDir/ass → ""（WriteAss 临时目录默认）。
func (s *Service) assDirPath() string {
	if s.cfg.AssDir != "" {
		return s.cfg.AssDir
	}
	if s.cfg.DataDir != "" {
		return filepath.Join(s.cfg.DataDir, "ass")
	}
	return ""
}

// ---------------------------------------------------------------- 公开 API

// Open 解析计划并起播：无实例则 spawn，已有则 loadfile replace 复用。
// 换集时先 SubRemove 防旧集弹幕串画（v1 同款），弹幕窗口状态清零重拉。
func (s *Service) Open(ctx context.Context, req Request) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.mu.Unlock()

	if s.cfg.Resolver == nil {
		return errors.New("player: 未注入 Resolver，无法解析播放地址")
	}
	// 解析必须有界：核心 resolve 内部对上游 403/限流会做多轮重试（单轮可达分钟级），
	// 无超时时前端会永远停在「正在打开播放会话…」（用户实锤且控制台无报错）。
	// 60s 上限后给出可操作的错误文案。
	rctx, rcancel := context.WithTimeout(ctx, openResolveTimeout)
	plan, err := s.cfg.Resolver.Resolve(rctx, req)
	rcancel()
	if err != nil {
		if rctx.Err() != nil && ctx.Err() == nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
			return fmt.Errorf("获取播放地址超时（%d 秒）：上游可能限流或网络不通，可在设置→网络与下载调整代理后重试", int(openResolveTimeout/time.Second))
		}
		return err
	}
	if plan.URL == "" {
		return errors.New("player: 解析结果为空地址")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed // resolve 期间可能被 Close
	}
	// 上一集可能是 Stop()/体面退出留下的 quitting，这次必须清掉，
	// 否则本集真崩了也不会重建（抑制标志的作用域只能是"这一次会话"）。
	s.quitting = false
	s.req = &Request{DramaID: req.DramaID, Title: req.Title, Index: req.Index,
		Drama: req.Drama, Chapter: req.Chapter, Quality: req.Quality}
	s.plan = &plan
	s.durMs = int64(plan.DurationSec * 1000)
	s.pendingResumeMs = 0
	s.rebuilds = 0
	s.endedFired = false
	s.lastPosMs = 0
	s.lastVersion = 0
	s.assPath = ""
	// 线上会话必须清掉上一条本地会话的 sidecar 状态：
	// 显示口径已经按 plan.Local 走，但 sidecarAss 留着会让"开弹幕"那条分支误判。
	s.sidecarAss, s.sidecarCount = "", 0
	s.wantPaused = false // 新集默认在播（v1 play_episode 语义）

	// 实例已死（断线后还没重建完就来 Open）：丢弃，下面 spawn 新的。
	if s.p != nil && !s.p.Alive() {
		if s.pumpStop != nil {
			close(s.pumpStop)
			s.pumpStop = nil
		}
		s.p = nil
		s.subAttached = false
	}

	// 换集：撤下上一集的弹幕轨（数据保留在 fetcher 之外，直接清窗重拉）
	if s.subAttached && s.p != nil {
		_ = s.p.SubRemove()
		s.subAttached = false
	}
	if s.fetcher != nil && plan.Session != "" {
		s.fetcher.SetEpisode(danmaku.Episode{
			Session:    plan.Session,
			DurationMs: s.durMs,
			Key:        s.assKeyLocked(),
		})
	}

	if err := s.applySourceLocked(); err != nil {
		s.emit("player:error", map[string]string{"message": "起播失败：" + err.Error()})
		return err
	}
	return nil
}

// LocalRequest 是一次本地文件播放请求（v1 main.py:2609 open_local(path, decryption_key, resume)）。
type LocalRequest struct {
	// Path 必须是**已校验过**的绝对路径（校验在壳层 App.PlayerOpenLocal 做：
	// 只允许下载根之下的普通文件——前端传什么都有可能，这里不能当它可信）。
	Path          string
	Title         string
	DecryptionKey string
	ResumeMs      int64
}

// OpenLocal 播一个本地成品文件（合并输出 / 已下载分集），**不走 Resolver**。
//
// 对端是 v1 `open_local`（main.py:2609-2640）：本地文件没有季/集概念，
// v1 于是把选集条清空、把上一集/下一集按钮关掉（`set_neighbours(False, False)`）。
// 这里对应地把 req 的 Index/DramaID/Chapter 留空、Session 留空——
// 弹幕抓取按 `plan.Session != ""` 才启动（见上面 Open 的同一处判断），
// 于是本地播放天然不会去线上抓一集不存在的弹幕；
// v1 的本地弹幕是挂**下载时保存的同名 .ass**（main.py:1880 _attach_sidecar_danmaku），
// 那是下一件事（需要下载侧的 .ass 命名约定），本函数不假装已经做了。
func (s *Service) OpenLocal(ctx context.Context, req LocalRequest) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.mu.Unlock()

	if strings.TrimSpace(req.Path) == "" {
		return errors.New("player: 本地路径为空")
	}
	plan := Plan{URL: req.Path, Local: true, DecryptionKey: req.DecryptionKey}
	title := req.Title
	if title == "" {
		title = filepath.Base(req.Path)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	// Index 0 / 无 drama：告诉所有读 req 的地方"这不是某一集"
	// （进度回写、下一季判断、ASS key 都靠它，见 v1 清空选集条的同款动机）。
	s.req = &Request{Title: title}
	s.plan = &plan
	s.durMs = 0
	s.pendingResumeMs = req.ResumeMs
	if s.pendingResumeMs < 0 {
		s.pendingResumeMs = 0
	}
	s.rebuilds = 0
	s.endedFired = false
	s.lastPosMs = 0
	s.lastVersion = 0
	s.assPath = ""
	s.wantPaused = false

	// 换集同款清理：撤下上一集的弹幕轨，且**不给本地文件设抓取窗口**。
	if s.subAttached && s.p != nil {
		_ = s.p.SubRemove()
		s.subAttached = false
	}
	if s.fetcher != nil {
		s.fetcher.SetEpisode(danmaku.Episode{})
	}
	// 上一条本地会话的 sidecar 状态必须清零，否则这一集会显示上一集的弹幕条数
	s.sidecarAss, s.sidecarCount = "", 0

	if err := s.applySourceLocked(); err != nil {
		return err
	}
	// v1 的顺序是"先起播，再挂同名 .ass"（open_local 末尾调 _attach_sidecar_danmaku）；
	// 没有 sidecar 就静默不挂，不报错也不影响播放。
	s.attachSidecarLocked(req.Path)
	s.emitStatsLocked()
	return nil
}

// attachSidecarLocked 给本地成品挂同名 .ass（v1 `_attach_sidecar_danmaku` 的对应物）。
// 弹幕**条数**取同名 .danmaku.json 的 items 长度而不是核心 total：
// 用户看到的数一直是"本地已经存下多少条"。
func (s *Service) attachSidecarLocked(videoPath string) {
	ass, count, ok := danmaku.DetectSidecar(videoPath)
	if !ok {
		s.sidecarAss, s.sidecarCount = "", 0
		return
	}
	s.sidecarAss, s.sidecarCount = ass, count
	if !s.enabled || s.p == nil {
		return // 关着弹幕不挂载（与线上路径同一规则）
	}
	if err := s.p.SubAdd(ass, "弹幕"); err != nil {
		s.log("player: 挂 sidecar 失败: %v", err)
		return
	}
	s.subAttached = true
}

// Play 继续播放。
func (s *Service) Play() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p == nil {
		return ErrNotOpen
	}
	s.wantPaused = false
	return s.p.Play()
}

// Pause 暂停。
func (s *Service) Pause() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p == nil {
		return ErrNotOpen
	}
	s.wantPaused = true
	return s.p.Pause()
}

// SeekTo 绝对平滑跳转（posMs 毫秒）。命名避开 io.Seeker 惯例签名检查。
// 弹幕窗口下一 tick 按新位置自动重定位（已抓窗口不重拉，同 v1）。
func (s *Service) SeekTo(posMs int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p == nil {
		return ErrNotOpen
	}
	if posMs < 0 {
		posMs = 0
	}
	s.lastPosMs = posMs
	if s.pendingResumeMs > 0 {
		s.pendingResumeMs = posMs // 用户 seek 覆盖待续播点
	}
	return s.p.AbsSeekSmooth(float64(posMs) / 1000.0)
}

// SetSpeed 变速并记录意图（重建后恢复）。
func (s *Service) SetSpeed(rate float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p == nil {
		return ErrNotOpen
	}
	s.wantSpeed = rate
	return s.p.SetSpeed(rate)
}

// SetVolume 音量（0-130，mpv 语义）并记录意图（重建后恢复）。
func (s *Service) SetVolume(level int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p == nil {
		return ErrNotOpen
	}
	s.wantVolume = level
	return s.p.SetVolume(level)
}

// SetDanmakuEnabled 弹幕开关。
// 关：SubRemove 撤轨（已抓数据保留在 fetcher，不再发起抓取）；
// 开：立即用窗口内已拉数据重渲染挂载，后续抓取自然补当前集（v1「打开就补」）。
func (s *Service) SetDanmakuEnabled(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.DanmakuSource == nil {
		return errors.New("player: 未注入弹幕源")
	}
	if s.enabled == on {
		return nil
	}
	s.enabled = on
	if !on {
		if s.subAttached && s.p != nil {
			if err := s.p.SubRemove(); err != nil {
				s.log("player: 关弹幕撤轨失败: %v", err)
			}
			s.subAttached = false
		}
		s.emitStatsLocked()
		return nil
	}
	// 开：线上会话重渲染已保留数据（不足再由 ticker 补拉）；
	// 本地会话只有一件事可做——把同名 .ass 再挂回去（refreshTrackLocked 对它什么也不做，
	// 因为它整个建立在 fetcher 有窗口数据的前提上）。
	if localSession(s.plan) && s.sidecarAss != "" {
		if s.p != nil {
			if err := s.p.SubAdd(s.sidecarAss, "弹幕"); err != nil {
				s.log("player: 重挂 sidecar 失败: %v", err)
			} else {
				s.subAttached = true
			}
		}
	} else if s.p != nil {
		s.refreshTrackLocked()
	}
	s.emitStatsLocked()
	return nil
}

// opacityValueLocked 把内部零值翻成对外语义「未设 = 不透明 1.0」。调用方需持锁。
func (s *Service) opacityValueLocked() float64 {
	if s.opacity <= 0 {
		return 1.0
	}
	return s.opacity
}

// SetDanmakuOpacity 设弹幕不透明度（v1 danmaku 面板「透明 NN%」滑条的对端）。
// 钳位规则与 danmaku.AssOptions.normalize 一致：0/越界按 1.0，下限 0.05；
// 改完立即用已抓数据重渲染挂载（v1「改透明度立即重建字幕并 sub-reload」）。
// 弹幕关着时只存值，不触发渲染（与 SetDanmakuEnabled 的关档语义一致）。
func (s *Service) SetDanmakuOpacity(v float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v <= 0 || v > 1 {
		// 与 ass.go 同规则：0 与越界都视为「未设」→ 不透明
		v = 1.0
	}
	v = max(0.05, min(1.0, v)) // Go 1.21+ 内建 min/max（与 ass.go 同款写法，免引 math）
	if s.opacity == v {
		return nil
	}
	s.opacity = v
	if s.enabled && s.p != nil {
		s.refreshTrackLocked()
	}
	s.emitStatsLocked()
	return nil
}

// Stop 只停"这一次播放"：quit mpv、清会话状态，但**服务仍可再次 Open**。
//
// 第五十九轮（真机反馈）新增。此前 `PlayerClosePlayer` 直接调 `Close()`，
// 而 `Close()` 置 `closed=true` 且 `Open()` 见到它就返回 `ErrClosed`
// （service.go:355/382）—— 而这个 Service 是**应用生命周期**的
// （app.go 只建一次），于是用户点一次「关闭」之后，
// 本进程内所有后续播放永远失败在「player: 服务已关闭」。
// 用户截图里的红字正是这条 ErrClosed。
//
// v1 把"停止播放"和"退出程序"分成两件事：`player.stop()` 之后照样能再播，
// 只有 app 退出才做终态清理。这里补的就是这层区分。
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
		// v1 `stop(blocking=False)` 的同款理由（player.py:728-735：
		// "switching episodes never freezes the window"）：
		// quit 有超时上限，超时后 Terminate/Kill 兜底，全在 goroutine 里做，
		// 界面不能因为一个卡住的 IPC 而"关闭点了没反应"。
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := p.Quit(ctx); err != nil {
				s.log("player: stop 时 quit 失败（Quit 内部已尽力 terminate）: %v", err)
			}
		}()
	}
	return nil
}

// stopSessionLocked 清掉一次会话的运行时状态（调用方须持锁）。
// 与 Close 的唯一区别：不把服务判死。
func (s *Service) stopSessionLocked() {
	s.fetcher = nil
	s.req = nil
	s.plan = nil
	s.subAttached = false
	s.assPath = ""
}

// Close 停服务：quit mpv + 清弹幕状态。可重复调用（幂等）。
// ⚠ 终态：置 closed 后本服务不再接受 Open —— 只用于应用退出。
func (s *Service) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.quitting = true
	p := s.p
	s.p = nil
	if s.pumpStop != nil {
		close(s.pumpStop)
		s.pumpStop = nil
	}
	s.fetcher = nil
	s.req = nil
	s.plan = nil
	s.subAttached = false
	s.assPath = ""
	s.cancel()
	s.mu.Unlock()

	if p != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := p.Quit(ctx); err != nil {
			s.log("player: quit 失败（忽略）: %v", err)
			return err
		}
	}
	return nil
}

// State 当前状态快照（未 Open 时返回零值骨架）。
func (s *Service) State() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// ---------------------------------------------------------------- 内部实现

func (s *Service) snapshotLocked() Snapshot {
	snap := Snapshot{Speed: s.wantSpeed, Volume: s.wantVolume,
		Danmaku: DanmakuState{Enabled: s.enabled, Opacity: s.opacityValueLocked()}}
	if s.p != nil {
		st := s.p.State()
		snap.PosMs = msOf(st.Position)
		dur := st.Duration
		if durMs := msOf(dur); durMs > 0 {
			snap.DurationMs = durMs
		} else {
			snap.DurationMs = s.durMs
		}
		snap.Playing = st.FileLoaded && !st.Paused && !st.EofReached
		snap.Speed = st.Speed
		if st.Volume != 0 {
			snap.Volume = st.Volume
		}
	} else if s.durMs > 0 {
		snap.DurationMs = s.durMs
	}
	if localSession(s.plan) && s.sidecarAss != "" {
		// 本地会话的条数来自同名 .danmaku.json（v1 attach_danmaku(ass, count) 的第二个参数）。
		// ⚠ 不能用"s.fetcher == nil"当本地判据：OpenLocal 之后 fetcher 仍在，
		// 只是没有剧集窗口，Stats() 恒 0 —— 那样 sidecar 就永远显示不出来。
		snap.Danmaku.Count = int(s.sidecarCount)
	} else if s.fetcher != nil {
		_, count, _, _ := s.fetcher.Stats()
		snap.Danmaku.Count = count
		snap.Danmaku.Note = s.fetcher.Note()
	}
	if s.req != nil {
		snap.Episode = EpisodeRef{DramaID: s.req.DramaID, Title: s.req.Title, Index: s.req.Index}
	}
	if s.plan != nil {
		snap.Local = s.plan.Local
	}
	return snap
}

// localSession 判"这是一次本地成品会话"。
// 唯一真相是 plan.Local（OpenLocal 里落的），不去猜 plan.URL 是不是路径——
// 那会把 Windows 盘符、UNC 之类形状又变成一处隐式规则。
func localSession(plan *Plan) bool {
	return plan != nil && plan.Local
}

func msOf(sec float64) int64 {
	if sec <= 0 {
		return 0
	}
	return int64(sec*1000 + 0.5)
}

func (s *Service) assKeyLocked() string {
	// v1 write_ass key 语义：剧ID-集号（1 起始）
	return fmt.Sprintf("%s-%d", s.req.DramaID, s.req.Index)
}

// applySourceLocked spawn（若无实例）并把当前 plan 装载进 mpv。
func (s *Service) applySourceLocked() error {
	if s.p == nil {
		if err := s.spawnLocked(); err != nil {
			return err
		}
	}
	return s.p.LoadWith(s.plan.URL, s.plan.Headers, s.req.Title, mpv.LoadOptions{
		DecryptionKey: s.plan.DecryptionKey,
		Local:         s.plan.Local,
	})
}

// spawnLocked 造新 mpv 实例、应用播放意图、启动事件泵。调用方持锁。
func (s *Service) spawnLocked() error {
	p := s.cfg.MpvLauncher(s.mpvConfig())
	if err := p.Start(s.ctx); err != nil {
		return err
	}
	// 播放意图先落到新进程（loadfile 不重置这些全局属性）
	if s.wantSpeed != 1.0 {
		if err := p.SetSpeed(s.wantSpeed); err != nil {
			s.log("player: 恢复倍速失败: %v", err)
		}
	}
	if s.wantVolume != 100 {
		if err := p.SetVolume(s.wantVolume); err != nil {
			s.log("player: 恢复音量失败: %v", err)
		}
	}
	s.p = p
	s.pumpStop = make(chan struct{})
	events := p.Events()
	stop := s.pumpStop
	go s.pump(p, events, stop)
	return nil
}

// pump 消费一个实例的事件流；实例被替换（重建）后自动退出。
// Events 通道永不关闭（mpv 内核保证），靠 stop 退出。
func (s *Service) pump(p MpvPlayer, events <-chan mpv.Event, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-s.ctx.Done():
			return
		case ev := <-events:
			s.mu.Lock()
			if s.p != p || s.closed {
				s.mu.Unlock()
				return
			}
			s.handleEventLocked(ev)
			s.mu.Unlock()
		}
	}
}

// handleEventLocked 事件语义（对齐 v1 player._on_event）：
//   - file-loaded：续播点恢复（v1 pending_resume 语义——loadfile 后不立刻 seek，
//     等新文件真正加载才跳）、清零重建计数、弹幕窗口初始化驱动交给 ticker；
//   - end-file reason=eof 或 eof-reached 属性：player:ended（每集去重一次，
//     keep-open 下 end-file 可能不发，属性是可靠兜底）；
//   - end-file reason=error：player:error；
//   - disconnected/exit：非用户退出时自动重建续播，最多 MaxRebuilds 次。
func (s *Service) handleEventLocked(ev mpv.Event) {
	switch ev.Kind {
	case mpv.EventFileLoaded:
		s.rebuilds = 0
		if s.pendingResumeMs > 1000 {
			pos := float64(s.pendingResumeMs) / 1000.0
			s.pendingResumeMs = 0
			s.lastPosMs = int64(pos * 1000)
			if err := s.p.AbsSeekSmooth(pos); err != nil {
				s.log("player: 续播 seek 失败: %v", err)
			}
		}
		if s.wantPaused {
			if err := s.p.Pause(); err != nil {
				s.log("player: 恢复暂停意图失败: %v", err)
			}
		}
	case mpv.EventEndFile:
		// 只对 reason=eof 报片尾（loadfile replace 换集时旧文件也会发
		// end-file reason=stop，不能当播完）；error 报失败；其余静默。
		switch ev.Name {
		case "eof":
			s.markEndedLocked()
		case "error":
			s.emit("player:error", map[string]string{"message": "播放失败"})
		}
	case mpv.EventProperty:
		if ev.Name == "eof-reached" && ev.State.EofReached {
			// keep-open 下 end-file 不一定发，属性兜底
			s.markEndedLocked()
		}
	case mpv.EventDisconnected, mpv.EventExit:
		if s.quitting || s.closed {
			return
		}
		// 用户从 mpv 窗口点关闭：mpv 先发 shutdown 事件再退出。这不是崩溃，
		// 不能重建——重建等于"把用户刚关掉的窗又开起来"（真机反馈的第一条）。
		// 报 player:stopped，让界面回到"可再播"而不是"播放失败"。
		if errors.Is(ev.Err, mpv.ErrGracefulShutdown) {
			s.log("player: mpv 体面退出（用户关窗或 quit），不重建")
			s.stopSessionLocked()
			s.emit("player:stopped", map[string]any{})
			return
		}
		why := "播放器与 mpv 断开"
		if ev.Err != nil {
			why = ev.Err.Error()
		}
		posMs := msOf(ev.State.Position)
		if posMs < s.lastPosMs {
			posMs = s.lastPosMs
		}
		s.log("player: 事件 %s（%s），尝试自动重建（第 %d/%d 次）",
			ev.Kind, why, s.rebuilds+1, s.cfg.MaxRebuilds)
		s.rebuildLocked(posMs, why)
	}
}

func (s *Service) markEndedLocked() {
	if s.endedFired {
		return
	}
	s.endedFired = true
	ref := EpisodeRef{}
	if s.req != nil {
		ref = EpisodeRef{DramaID: s.req.DramaID, Title: s.req.Title, Index: s.req.Index}
	}
	s.emit("player:ended", map[string]any{"episode": ref})
}

// rebuildLocked 自动重建：杀旧实例 → 新实例 Start → 载同一 plan →
// 记住续播点等新 file-loaded 再 seek（v1 resume 时机）。
func (s *Service) rebuildLocked(posMs int64, why string) {
	s.rebuilds++
	old := s.p
	s.p = nil
	if s.pumpStop != nil {
		close(s.pumpStop)
		s.pumpStop = nil
	}
	s.subAttached = false // 新进程没有字幕轨了
	if old != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		_ = old.Quit(ctx)
		cancel()
	}
	if s.plan == nil || s.req == nil {
		return
	}
	if s.rebuilds > s.cfg.MaxRebuilds {
		s.emit("player:error", map[string]any{"message": fmt.Sprintf("播放器断开，自动重建失败（已重试 %d 次）：%s", s.cfg.MaxRebuilds, why)})
		return
	}
	s.pendingResumeMs = posMs
	if err := s.applySourceLocked(); err != nil {
		s.emit("player:error", map[string]any{"message": "播放器自动重建失败：" + err.Error()})
		return
	}
	// 重建成功不报错误：前端从 player:tick 的位置恢复即可感知。
}

// ---------------------------------------------------------------- ticker 循环

// loop 是 500ms 心跳：发 player:tick、驱动弹幕抓取与轨道刷新。
func (s *Service) loop() {
	t := time.NewTicker(s.cfg.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.tick()
		}
	}
}

func (s *Service) tick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.p == nil || s.req == nil {
		return
	}

	st := s.p.State()
	posMs := msOf(st.Position)

	// 时长权威值：plan 没给就用 mpv duration 属性；补写进 fetcher 的 Episode
	// （v1「没拿到时长，弹幕没加载」等待语义：fetcher 在 DurationMs<=0 时不抓）。
	if durMs := msOf(st.Duration); durMs > s.durMs {
		s.durMs = durMs
		s.syncEpisodeLocked()
	}

	// 位置大幅跳变（>一个弹幕窗）→ 记日志；重定位由 Fetcher 按当前
	// position 重推窗口起点自然完成（已抓窗口跳过，同 v1）。
	if s.fetcher != nil && posMs > 0 && absInt64(posMs-s.lastPosMs) > danmaku.WindowMS {
		s.log("player: 位置跳变 %d→%d，弹幕窗重定位", s.lastPosMs, posMs)
	}
	s.lastPosMs = posMs

	// 抓取驱动：开着弹幕、真正在播（非暂停非片尾）才 pump（v1 同款门控）。
	if s.fetcher != nil && s.enabled && s.plan != nil && s.plan.Session != "" &&
		st.FileLoaded && !st.Paused && !st.EofReached {
		s.fetcher.Tick(posMs)
		_, count, _, version := s.fetcher.Stats()
		if version != s.lastVersion {
			s.lastVersion = version
			s.refreshTrackLocked()
			s.emitStatsLocked()
		} else if s.needStatsNoteLocked(count) {
			s.emitStatsLocked()
		}
	}

	s.emit("player:tick", s.snapshotLocked())
}

// syncEpisodeLocked 把新拿到的时长补写给 fetcher；仅在「还没抓到东西」
// （version==0）时重写 Episode，避免打断已进行的抓取。
func (s *Service) syncEpisodeLocked() {
	if s.fetcher == nil || s.durMs <= 0 || s.plan == nil || s.plan.Session == "" {
		return
	}
	if s.fetcher.Episode().DurationMs > 0 {
		return
	}
	s.fetcher.SetEpisode(danmaku.Episode{
		Session:    s.plan.Session,
		DurationMs: s.durMs,
		Key:        s.assKeyLocked(),
	})
}

// needStatsNoteLocked 备注变化也要播报一次（失败退避等 count 不变的提示）。
func (s *Service) needStatsNoteLocked(count int) bool {
	note := ""
	if s.fetcher != nil {
		note = s.fetcher.Note()
	}
	return note != s.lastNote || count != s.lastCount
}

func (s *Service) emitStatsLocked() {
	count, note := 0, ""
	if localSession(s.plan) && s.sidecarAss != "" {
		count = int(s.sidecarCount) // 与 snapshotLocked 同一口径，两处别各写一份规则
	} else if s.fetcher != nil {
		_, count, _, _ = s.fetcher.Stats()
		note = s.fetcher.Note()
	}
	s.lastNote = note
	s.lastCount = count
	s.emit("danmaku:stats", map[string]any{"count": count, "note": note})
}

// refreshTrackLocked 版本变更后重渲染当前窗口数据：
// BuildAss(全量已抓 items, durationMs) → WriteAss(key=剧ID-集号，原地原子重写) →
// 已挂载则 SubReload（mpv 原地重读同一文件），否则 SubAdd。
// 关着弹幕时不挂载；无数据不挂载（v1 空窗不挂空轨）。
func (s *Service) refreshTrackLocked() {
	if !s.enabled || s.fetcher == nil || s.p == nil || s.req == nil || s.durMs <= 0 {
		return
	}
	items := s.fetcher.Items()
	if len(items) == 0 {
		return
	}
	text := danmaku.BuildAss(items, s.durMs, danmaku.AssOptions{Opacity: s.opacity})
	if text == "" {
		return
	}
	dir := s.assDirPath()
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			s.log("player: 创建 ASS 目录失败: %v", err)
			return
		}
	}
	path, err := danmaku.WriteAss(dir, s.assKeyLocked(), text, s.cfg.AssKeep)
	if err != nil {
		s.log("player: 写 ASS 失败: %v", err)
		return
	}
	s.assPath = path
	if s.subAttached {
		if err := s.p.SubReload(); err != nil {
			s.log("player: sub-reload 失败: %v", err)
		}
		return
	}
	if err := s.p.SubAdd(path, "弹幕"); err != nil {
		s.log("player: sub-add 失败: %v", err)
		return
	}
	s.subAttached = true
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
