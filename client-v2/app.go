// Package main —— 真果鉴 v2 桌面壳（Wails v2 + WebView2）。
// app.go 是绑定注册表：各域一行注册（V2-1 起 engine 门面），
// 后续任务按 design.md 约定在自己文件里定义 Bind<X>，由主代理统一在此追加。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"zhenguojian/client-v2/internal/downloadsvc"
	"zhenguojian/client-v2/internal/mpv"
	"zhenguojian/client-v2/internal/native"
	"zhenguojian/client-v2/internal/player"
	"zhenguojian/client-v2/internal/tray"
)

type App struct {
	ctx context.Context
	// V2-3b：播放服务（懒 spawn mpv；生命周期钩子 + 绑定方法均委托它）。
	playerSvc *player.Service
	// V2-3：下载与合并服务（轮询事件 + 合并队列；绑定见 bindings_download.go）。
	downloadSvc *downloadsvc.Service
	// V2-4：播放画面内嵌宿主（ZgjVideoHost 子窗）；embedMu 串行化 attach/bounds/detach。
	embedMu sync.Mutex
	embed   *mpv.EmbedHost
	// V-TRAY：系统托盘（默认开启零设置；Windows 生效，其余平台 unsupported 降级）。
	trayH *tray.Handler
	// quitting 托盘「退出程序」/更新重启旗标：OnBeforeClose 仅在置位时放行关闭，
	// 否则 X = 隐藏主窗后台运行（防误关断下载 + 防用户以为已退而再开第二实例）。
	quitting atomic.Bool
}

func NewApp() *App {
	appRoot := ""
	if exe, err := os.Executable(); err == nil {
		appRoot = filepath.Dir(exe)
	}
	a := &App{}
	dataDir, _ := native.DataDir()
	a.playerSvc = player.New(player.Config{
		AppRoot: appRoot,
		DataDir: dataDir,
		// 播放计划解析走核心 resolve 动作（同 v1 编排：drama/chapter 原样对象 + 1 起始集号）。
		Resolver: nativePlayerResolver{},
		// 弹幕抓取适配：本壳唯一允许 import native 的注入点（service 包不依赖 native）。
		DanmakuSource: player.NewDanmakuSource(native.DanmakuWindow),
		Emit: func(event string, payload any) {
			if a.ctx != nil {
				emitEventFunc(a.ctx, event, payload)
			}
		},
		// 窗口内嵌句柄：初值 0 = mpv 独立窗（回退态）；进入播放页时
		// PlayerAttach 惰性建 ZgjVideoHost 子窗并经 SetWid 翻转（V2-4）。
		Wid: 0,
	})
	a.downloadSvc = downloadsvc.New(downloadsvc.Options{
		Call: native.Call,
		EventsEmit: func(name string, data ...any) {
			if a.ctx != nil {
				emitEventFunc(a.ctx, name, data...)
			}
		},
		DataDirFunc: native.DataDir,
	})
	// V-TRAY：托盘句柄（Start 挂 startup，Register 失败仅记状态不致命）。
	// 回调在 tray 包的转发协程上执行——runtime API 走 PostMessage，线程安全。
	a.trayH = tray.New(tray.Actions{
		Restore: func() {
			if a.ctx != nil {
				windowShowFunc(a.ctx)
			}
		},
		CheckUpdate: func() {
			// 复用 update:available 横幅 + 原生结果框（见 updater.CheckManual）。
			a.updateService().CheckManual(context.Background())
		},
		Quit: func() {
			a.allowQuit()
			if a.ctx != nil {
				windowQuitFunc(a.ctx)
			}
		},
	})
	// 手动检查的原生提示出口：Windows=MessageBoxW；失败/非 Windows 回落事件
	// （前端未监听 update:notify，Windows 之外仅开发机，真机验证点见 tray_notes.md）。
	a.updateService().Notify = func(title, text string) {
		if err := tray.ShowInfoBox(title, text); err != nil {
			if a.ctx != nil {
				emitEventFunc(a.ctx, "update:notify", map[string]any{"title": title, "text": text})
			}
		}
	}
	return a
}

// allowQuit 放行一次真退出（置旗标）。runtime.Quit 在 Windows 走 WM_CLOSE →
// 会经过 beforeClose；更新重启（bindings_update.go）与托盘退出都必须先调它。
func (a *App) allowQuit() {
	a.quitting.Store(true)
	// 退出看门狗：OnShutdown 各步虽有界，但 wails 窗口销毁链路历史上出过卡死
	// （用户实锤：托盘退出后进程不没，只能任务管理器）。5 秒后仍在世就硬退出，
	// 正常路径早已结束，AfterFunc 随进程消失，不会误伤。
	time.AfterFunc(5*time.Second, func() { os.Exit(0) })
}

// beforeClose 挂 wails OnBeforeClose：返回 true=允许关闭。
//
// 第五轮真机反馈改回 v1 语义。此前 v2 自创了"X = 隐藏主窗进托盘、下载不中断"，
// 用户实测结果是「我只能任务管理器才能关的掉」——窗藏了、托盘图标在
// Windows 的折叠区里找不到，进程就永久留在后台。
//
// v1 的做法核过代码：`main.py:2788-2838 closeEvent` 一路做
// playback.close → downloads.shutdown → `player.stop(blocking=True)`
// → media.kill_running() → runner.shutdown → watch.flush → 各面板 shutdown
// → tray.hide() → `QApplication.instance().quit()` + `event.accept()`。
// 也就是说 **v1 的 X 就是真退出**，并且明确接受"退出会停下载/会杀 ffmpeg"
// （注释原话：would otherwise outlive the app and keep writing to disk）。
// v1 的托盘（main.py:735-747）只有「显示主窗口」和「退出」两项，
// 没有任何"关窗转托盘"的分支，`_show_from_tray` 只由托盘双击触发。
//
// 所以这里放行关闭；真正的收尾在 OnShutdown（停托盘、停下载、Close 播放服务）。
// 看门狗仍然保留：wails 的窗口销毁链路历史上出过卡死，5 秒后仍在世就硬退出。
func (a *App) beforeClose(ctx context.Context) bool {
	a.quitting.Store(true)
	time.AfterFunc(quitWatchdog, func() { os.Exit(0) })
	return true
}

// quitWatchdog 是上面那个硬退出延时。
// ⚠ 做成包级变量而不是写死字面量，是为了**能在测试里改掉**：
// 直接调用 beforeClose 的单测若沿用 5s，测试跑到第五秒会被 os.Exit(0)
// 整截带走——表现是"套件绿了但少跑了一批"，本任务已经抓过多次
// "exit 0 但一条没测"这一族，不能自己再造一个。
var quitWatchdog = 5 * time.Second

// startup 在 wails 窗口就绪前调用；引擎 initialize 幂等且本地很快
// （spike 实测 0.0s），失败不致命——后续每次绑定调用都会重试 Init。
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// app.log 启动行：exe 身份自证（version=? 之谜从这一版起由日志回答）。
	rev := ""
	if bi := readBuildVCS(); bi != nil {
		rev, _ = bi["revision"].(string)
	}
	appLogStartup(buildVersion, rev)
	if err := native.Init(); err != nil {
		// sync.Once 语义：initialize 失败会保持到进程重启（数据目录不可写是硬故障）。
		println("[zhenguojian] engine initialize 失败: " + err.Error())
	}
	a.downloadSvc.OnStartup(ctx)
	if err := a.trayH.Start(); err != nil {
		// 非 Windows / shell 异常：Status() 已含降级原因，X 退回直接退出。
		println("[zhenguojian] tray: " + err.Error())
	}
}

// ---------- 退出收尾（OnShutdown）：并发跑互不依赖的步骤 ----------
//
// 【耗时账：每步的最坏上界，逐条读代码算出来的（行号 = 改前现状）】
//
//	trayH.Stop()            internal/tray/tray.go:136-142
//	                        = winBackend.Unregister 等消息泵 time.After(2s)
//	                          （internal/tray/tray_windows.go:190）
//	                          ＋分发协程 time.After(3s)（tray.go:140）
//	                        → 最坏 5s；没注册成功（非 Windows / Start 失败）时 0s
//	downloadSvc.OnShutdown  internal/downloadsvc/service.go:182-206
//	                        = cancel()（ffmpeg 的 killTree 挂在 cmd.Cancel 上，
//	                          executor.go:103，由 os/exec 的 watchCtx goroutine
//	                          即刻触发，不等 worker 退出）
//	                          ＋等 pollLoop/workerLoop time.After(5s)（service.go:201）
//	                          ＋flushMergesNow 落盘（service.go:205）
//	                        → 最坏 5s＋写盘；空闲时几十 ms
//	playerSvc.Close()       internal/player/service.go:742-749
//	                        = mpv.Player.Quit（internal/mpv/player.go:482-513）：
//	                          ipc "quit" ≤1s → proc.Terminate() → 等进程
//	                          time.After(2s)（player.go:507，**不受 ctx 约束**，
//	                          所以 service 那句 2s ctx 管不到这一截）→ proc.Kill
//	                        → 最坏 ≈3s；mpv 正常应 quit 时几十~几百 ms
//
//	串行（改前）：最坏 5＋5＋3 ＝ 13s；典型 ＝ 三步相加 ＝ 用户实测的 2–3s。
//	并发（改后）：典型 ＝ 最慢的那一步；最坏 ＝ max(budget 2s, killBudget 4s) ＝ 4s。
//
// 【为什么这三步可以并发（不是"看起来独立"，是各自的等待对象不相交）】
//   - 托盘：LockOSThread 的 Win32 消息泵 ＋ 一个分发协程；
//   - 下载：它自己的 pollLoop/workerLoop 两个 goroutine ＋ 合并子进程句柄；
//   - 播放：mpv 的 IPC 连接与进程句柄。
//     三者不共享任何锁（tray.Handler.mu / downloadsvc.Service.mu /
//     player.Service.mu 是三把不同的锁），也不互相调用；唯一共享的外部依赖是
//     internal/native 引擎门面，而运行期下载轮询与弹幕抓取本来就在并发调它，
//     并发化不多一个调用方。
//
// 【顺带补上的真窟窿（比省时间更要紧）】
// beforeClose 的 quitWatchdog 第 5 秒 os.Exit(0)。串行时播放收尾排在最后，
// 前面压着 5s＋5s —— 托盘或下载一卡，看门狗就在 `playerSvc.Close()` 之前响，
// 进程一没，正在排队的 quit/Terminate 全没下发，**mpv 直接成孤儿**（ffmpeg 同理）。
// 并发后这一步在 t=0 起跑，Terminate 最迟 t≈1s 落地，离看门狗还有 4s 余量。
// 所以"并发化"没有削弱"不留孤儿"，它削弱的是"排到最后的才安全"这个假安全感。
//
// shutdownStep 是退出收尾里的一步。
type shutdownStep struct {
	name string
	// terminal=true：这一步"没跑完"的代价是外部进程还活着（孤儿 mpv），
	// 所以 budget 到点不撒手，一直等到 killBudget 为止。
	terminal bool
	// run 必须自己收敛（三步的现状上见上面的耗时账）；runner 只管
	// "同时起跑 / 等多久 / 失败与超时都记日志"。
	run func(context.Context) error
}

// shutdownOutcome 是一步的结果：日志和测试都读它。
type shutdownOutcome struct {
	name      string
	took      time.Duration
	err       error
	abandoned bool // 到点没等到结果（goroutine 可能还在后台跑）
}

func (o shutdownOutcome) String() string {
	switch {
	case o.abandoned:
		return fmt.Sprintf("%s=超时放弃(已等%s)", o.name, o.took)
	case o.err != nil:
		return fmt.Sprintf("%s=失败(%s): %v", o.name, o.took, o.err)
	default:
		return fmt.Sprintf("%s=ok(%s)", o.name, o.took)
	}
}

var (
	// shutdownBudget 是普通步骤（托盘、下载）的等待上限。到点放弃的是
	// **等待**，不是职责：goroutine 继续自己跑完，结果写进带缓冲的槽位。
	shutdownBudget = 2 * time.Second
	// shutdownKillBudget 是终态步骤（mpv）的硬上限。取 4s 的两个理由：
	// 它内部最坏 ≈3s（quit 1s ＋ 等进程 2s），留 1s 调度余量；
	// 且必须 < quitWatchdog(5s)——看门狗是 os.Exit，它一响连 Terminate 都没了。
	shutdownKillBudget = 4 * time.Second
)

// shutdownLogf 是收尾日志出口（打包成 windowsgui 后 stdout 不一定有人看，
// 但它是 devtools/控制台启动与真机复现时唯一能看到"关窗花了多久"的地方；
// 测试注入捕获函数，钉住"失败不静默"）。
var shutdownLogf = func(format string, v ...any) {
	println("[zhenguojian] shutdown: " + fmt.Sprintf(format, v...))
}

// stopTray 是托盘收尾的调用点。做成变量不是为好看：`_ = a.trayH.Stop()`
// 这种"把错误吞回去"的回归，从根包没有别的办法观察到——tray.Handler 的
// 后端在包外不可注入，非 Windows 上 Stop 又恒返回 nil。所以这里留一个缝，
// 由 TestShutdownTrayErrorIsNotSwallowed 钉住（改前它连一条日志都不会有）。
var stopTray = func(h *tray.Handler) error { return h.Stop() }

// shutdownSteps 把退出收尾的三个依赖映射成可并发的步骤。
// 顺序不构成语义（并发起跑），列在这里是为了让"谁被放弃、谁是终态"可读可测。
func (a *App) shutdownSteps() []shutdownStep {
	return []shutdownStep{
		{
			name: "tray",
			run: func(context.Context) error {
				// 改前这里是 Stop() 无返回值，Unregister 的"消息线程 2s 未退出"
				// 被 `_ =` 吞在托盘包内部；现在把错误交给调用方记日志。
				return stopTray(a.trayH)
			},
		},
		{
			name: "downloads",
			run: func(ctx context.Context) error {
				// 被 budget 放弃时各保住/丢掉什么（逐条对着 service.go:182-206 核过）：
				//   保住 ffmpeg：cancel() 一落地，os/exec 的 watchCtx goroutine 就调
				//     cmd.Cancel → killTree（executor.go:103）；它不等 worker 退出，
				//     但也不是 cancel() 返回那一瞬就完——被放弃时它已经有一整个
				//     budget 的时间去落地（cancel 在 t≈0，放弃在 t=budget）。
				//   保住轮询/worker 的取消：同一个 cancel；
				//   可能丢掉：末尾那次 merges.state.json 立即落盘（service.go:205）。
				//     但改前的串行路径在同一种"worker 卡死"场景下也丢它——
				//     那要等满 5s，而 quitWatchdog 第 5 秒就 os.Exit(0)，
				//     前面还压着托盘最多 5s。所以这不是新丢的东西，
				//     是把"本来就撞看门狗"的情形挪进了有界日志里。
				a.downloadSvc.OnShutdown(ctx)
				return nil
			},
		},
		{
			name:     "mpv",
			terminal: true,
			run: func(context.Context) error {
				// 终态 Close，**不是** PlayerClosePlayer 那条 Stop：
				// Stop 把 quit 丢进 goroutine 就返回（service.go:697-703，
				// 那是换集/关播放窗的语义，"never freezes the window"），
				// 退出时进程跟着没，那个 goroutine 也就没了 = 孤儿 mpv。
				return a.playerSvc.Close()
			},
		},
	}
}

// shutdown 挂 wails OnShutdown：并发跑 shutdownSteps。
func (a *App) shutdown(ctx context.Context) {
	runShutdownSteps(ctx, a.shutdownSteps(), shutdownBudget, shutdownKillBudget, shutdownLogf)
}

// runShutdownSteps 并发执行 steps，返回每步结果。语义（测试钉的就是这四条）：
//
//	① 所有步骤同时起跑 —— 这是"串行求和"变成"并发取最大"的唯一来源；
//	② 单步 panic 收成 error，不得带走其它步骤；
//	③ budget 到点只放弃**非终态**步骤；终态步骤继续等到 killBudget，
//	   再不到点才放弃，并且日志必须点名"可能留孤儿进程"——这是全套唯一
//	   还能漏 mpv 的口子，不能悄悄走过去；
//	④ 失败 / panic / 超时一律走 logf，不静默。
//
// 放弃的判定是**一次性**的（settled 标记）：迟到的结果不会把"已放弃"改写成 ok，
// 否则测试与日志都会跟着调度抖。
func runShutdownSteps(parent context.Context, steps []shutdownStep, budget, killBudget time.Duration,
	logf func(format string, v ...any)) []shutdownOutcome {

	if logf == nil {
		logf = func(string, ...any) {}
	}
	t0 := time.Now()
	// 一个共享的派生 ctx：runner 返回时 cancel，让**被放弃**的 goroutine
	// 有个"宿主已经不等了"的信号可看（现有三步都自带超时、不看它；
	// 但它保证了以后加进来的步骤不必再自己发明一套放弃协议）。
	// ⚠ 放弃靠的是"不再等"，不是取消：cancel 之后这一步仍在跑。
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	outcomes := make([]shutdownOutcome, len(steps))
	startedAt := make([]time.Time, len(steps))
	settled := make([]bool, len(steps))
	var mu sync.Mutex

	// settle 只在槽位空闲时写：先到的判定算数（结果或放弃），后来者被忽略。
	settle := func(i int, o shutdownOutcome) bool {
		mu.Lock()
		defer mu.Unlock()
		if settled[i] {
			return false
		}
		settled[i] = true
		outcomes[i] = o
		return true
	}
	giveUp := func(i int, why string) {
		settle(i, shutdownOutcome{
			name:      steps[i].name,
			took:      time.Since(startedAt[i]),
			err:       fmt.Errorf("%s（这一步自己没在预算内结束）", why),
			abandoned: true,
		})
	}

	var wg sync.WaitGroup
	for i, st := range steps {
		startedAt[i] = time.Now()
		wg.Add(1)
		go func(i int, st shutdownStep) {
			defer wg.Done()
			since := startedAt[i]
			err := callShutdownStep(ctx, st)
			settle(i, shutdownOutcome{name: st.name, took: time.Since(since), err: err})
		}(i, st)
	}
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()

	// 放弃所有"非终态且还没结束"的槽位。
	abandonOrdinary := func() {
		mu.Lock()
		idx := make([]int, 0, len(steps))
		for i := range steps {
			if !steps[i].terminal && !settled[i] {
				idx = append(idx, i)
			}
		}
		mu.Unlock()
		for _, i := range idx {
			giveUp(i, fmt.Sprintf("%s 未在 %s 内结束", steps[i].name, budget))
		}
	}
	terminalWaiting := func() bool {
		mu.Lock()
		defer mu.Unlock()
		for i := range steps {
			if steps[i].terminal && !settled[i] {
				return true
			}
		}
		return false
	}

	// 第一轮：所有步骤共用 budget。
	timer := time.NewTimer(budget)
	select {
	case <-finished:
		timer.Stop()
	case <-timer.C:
		abandonOrdinary()
	}
	// 第二轮：只剩终态步骤在等时，把上限放宽到 killBudget（从 t=0 起算）。
	if terminalWaiting() {
		left := killBudget - time.Since(t0)
		if left < 0 {
			left = 0
		}
		hard := time.NewTimer(left)
		select {
		case <-finished:
			hard.Stop()
		case <-hard.C:
			mu.Lock()
			idx := make([]int, 0, len(steps))
			for i := range steps {
				if steps[i].terminal && !settled[i] {
					idx = append(idx, i)
				}
			}
			mu.Unlock()
			for _, i := range idx {
				// 唯一还能留孤儿的路径：内部 Quit 的上界被人改大、或这一步
				// 根本卡死。点名喊出来，别让它躲在一条 ok 里。
				giveUp(i, fmt.Sprintf("%s 未在 %s 内结束——可能留下孤儿 mpv", steps[i].name, killBudget))
			}
		}
	}

	snapshot := make([]shutdownOutcome, len(outcomes))
	mu.Lock()
	copy(snapshot, outcomes)
	mu.Unlock()

	parts := make([]string, 0, len(snapshot))
	for _, o := range snapshot {
		parts = append(parts, o.String())
		if o.err != nil {
			logf("步骤 %s 未干净结束: %v", o.name, o.err)
		}
	}
	logf("并发收尾用时 %s（预算 %s / 终态上限 %s）：%s",
		time.Since(t0).Round(time.Millisecond), budget, killBudget, strings.Join(parts, " "))
	return snapshot
}

// callShutdownStep 跑一步，panic 转成 error：一个步骤炸了不得带走其它收尾。
func callShutdownStep(ctx context.Context, st shutdownStep) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("shutdown: %s panic: %v", st.name, r)
		}
	}()
	if st.run == nil {
		return nil
	}
	return st.run(ctx)
}

// TrayStatus 托盘状态一行文本（running / unsupported: … / failed: … / stopped）。
// 冒烟自检用；前端暂不消费（api.ts/types.ts 不加映射）。
func (a *App) TrayStatus() string {
	if a.trayH == nil {
		return "uninitialized"
	}
	return a.trayH.Status()
}

// ---------- 绑定方法（全部返回 JSON 字符串；错误走 JS reject） ----------

// Ping 冒烟探针（CI 真机冒烟按钮用，继承 spike/hello 的验证点）。
func (a *App) Ping() string { return "pong" }

// DataPath 返回引擎数据目录，供设置页显示。
func (a *App) DataPath() (string, error) {
	return native.DataDir()
}

// ListSources → JSON: [{slug,name,displayName,adult,implemented,enabled,...}]
func (a *App) ListSources() (string, error) {
	items, err := native.ListSources()
	if err != nil {
		return "", err
	}
	return marshalJSON(items)
}

// SetSourceEnabled → JSON: {slug,enabled,persisted}
func (a *App) SetSourceEnabled(slug string, enabled bool) (string, error) {
	data, err := native.SetSourceEnabled(slug, enabled)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

// Catalog → JSON: {items,hasMore,page,warning?,localSearch,fresh}
// lane: "discover"/"search"/"library"…（同 lane 新请求取消旧请求，勿混用）。
func (a *App) Catalog(source, query, category string, page int, lane string, force bool) (string, error) {
	data, err := native.Catalog(source, query, category, page, lane, force)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

// Detail → JSON: {…v1 detail 形状（drama 字段并集 + chapters[]）}
// dramaJSON: catalog item 的原样 JSON 字符串。
func (a *App) Detail(dramaJSON string) (string, error) {
	drama, err := unmarshalObject(dramaJSON)
	if err != nil {
		return "", err
	}
	data, err := native.Detail(drama)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

// Resolve → JSON: {url,headers,quality,qualities,session,routeIndex,routeCount,...}
func (a *App) Resolve(dramaJSON, chapterJSON string, index, quality int) (string, error) {
	drama, err := unmarshalObject(dramaJSON)
	if err != nil {
		return "", err
	}
	chapter, err := unmarshalObject(chapterJSON)
	if err != nil {
		return "", err
	}
	data, err := native.Resolve(drama, chapter, index, quality)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

// BrowseFilters → JSON: 筛选分组；默认构建返回错误 discovery_unavailable。
func (a *App) BrowseFilters(source, category string) (string, error) {
	data, err := native.BrowseFilters(source, category)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

// BrowseFiltered → JSON: {items,...}；同 BrowseFilters 依赖 overlay 通道。
func (a *App) BrowseFiltered(source, filtersJSON string, offset int, cursor string) (string, error) {
	filters := map[string]string{}
	if filtersJSON != "" {
		if err := json.Unmarshal([]byte(filtersJSON), &filters); err != nil {
			return "", err
		}
	}
	data, err := native.BrowseFiltered(source, filters, offset, cursor)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

// ---------- helpers ----------

// DanmakuWindow → JSON: {items[],startMs,nextMs,total}；endMs 为绝对窗尾毫秒。
// session 来自 Resolve 应答的 playbackSession；同会话连续窗口走核心短缓存。
func (a *App) DanmakuWindow(session string, startMs, endMs float64) (string, error) {
	data, err := native.DanmakuWindow(session, int64(startMs), int64(endMs))
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

// DanmakuAll → JSON: 整集弹幕文档（受核心上限约束），供降级与调试。
// durationSec 是分集时长（秒）：核心要求 durationMs>0，缺它直接报错，
// 不再悄悄发一个 0 过去让真机拒绝。
func (a *App) DanmakuAll(session string, durationSec float64) (string, error) {
	data, err := native.DanmakuAll(session, int64(durationSec*1000))
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

// ---------- V2-3 播放绑定（委托 internal/player.Service，新 Config 契约） ----------

// nativePlayerResolver 把核心 resolve 应答映射为播放计划。
// Plan 的 JSON 名已对齐核心 nativePlan（app_runtime.go:145），一次编解码即可。
type nativePlayerResolver struct{}

func (nativePlayerResolver) Resolve(_ context.Context, req player.Request) (player.Plan, error) {
	data, err := native.Resolve(req.Drama, req.Chapter, req.Index, req.Quality)
	if err != nil {
		return player.Plan{}, err
	}
	body, err := json.Marshal(data)
	if err != nil {
		return player.Plan{}, err
	}
	var plan player.Plan
	if err := json.Unmarshal(body, &plan); err != nil {
		return player.Plan{}, err
	}
	if strings.TrimSpace(plan.URL) == "" {
		return player.Plan{}, errors.New("核心未返回可播放地址")
	}
	return plan, nil
}

// playerQualityCode 把前端画质档位映射为核心 resolve 的整数码。
// auto/空 → 0（自动），1080/720/540（可带 p 后缀）→ 对应档位。
func playerQualityCode(q string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(strings.TrimSuffix(q, "p"))) {
	case "", "auto":
		return 0, nil
	case "1080":
		return 1080, nil
	case "720":
		return 720, nil
	case "540":
		return 540, nil
	}
	return 0, fmt.Errorf("画质档位无效：%s", q)
}

// PlayerOpen dramaJSON/chapterJSON = 详情页透传的原样对象；index 为 1 起始集号。
// Go 内部走核心 resolve（同 v1 编排），返回 player.Snapshot 的 JSON。
func (a *App) PlayerOpen(dramaJSON, chapterJSON string, index int, quality string) (string, error) {
	drama, err := unmarshalObject(dramaJSON)
	if err != nil {
		return "", err
	}
	chapter, err := unmarshalObject(chapterJSON)
	if err != nil {
		return "", err
	}
	if index < 1 {
		return "", errors.New("集号无效")
	}
	qualityCode, err := playerQualityCode(quality)
	if err != nil {
		return "", err
	}
	title, _ := drama["title"].(string)
	dramaID, _ := drama["id"].(string)
	err = a.playerSvc.Open(a.ctx, player.Request{
		DramaID: dramaID,
		Title:   title,
		Index:   index,
		Drama:   drama,
		Chapter: chapter,
		Quality: qualityCode,
	})
	if err != nil {
		return "", err
	}
	return marshalJSON(a.playerSvc.State())
}

func (a *App) PlayerPlay() (string, error)           { return "", a.playerSvc.Play() }
func (a *App) PlayerPause() (string, error)          { return "", a.playerSvc.Pause() }
func (a *App) PlayerSeek(ms float64) (string, error) { return "", a.playerSvc.SeekTo(int64(ms)) }
func (a *App) PlayerSpeed(v float64) (string, error) { return "", a.playerSvc.SetSpeed(v) }
func (a *App) PlayerVolume(v int) (string, error)    { return "", a.playerSvc.SetVolume(v) }
func (a *App) PlayerDanmaku(on bool) (string, error) { return "", a.playerSvc.SetDanmakuEnabled(on) }

// PlayerDanmakuOpacity 弹幕不透明度（0.05–1.0；≤0 或 >1 一律按 1.0 不透明）。
// 对应 v1 播放控制条上的「透明 NN%」竖向滑条：改完 Go 侧立即用已抓数据重渲染 ASS
// 并重挂字幕轨，前端不需要自己拼 alpha。
func (a *App) PlayerDanmakuOpacity(v float64) (string, error) {
	return "", a.playerSvc.SetDanmakuOpacity(v)
}
func (a *App) PlayerState() (string, error) { return marshalJSON(a.playerSvc.State()) }

// PlayerClosePlayer 停"这一次播放"（Stop，**非终态**）。
// 第五十九轮真机反馈修正：这里原来调 `Close()`（终态，置 closed 后
// Open 永远返回 ErrClosed），而 playerSvc 是应用生命周期的（本文件只建一次），
// 于是用户点一次「关闭」，本进程内再也播不了 ——
// 截图里那句红字「播放失败：player: 服务已关闭」就是它。
// 应用退出的终态清理走 app.go:157 那一路，仍然调 Close()。
func (a *App) PlayerClosePlayer() (string, error) { return "", a.playerSvc.Stop() }

// ---------- V2-4 播放画面内嵌（PlayerAttach/PlayerSetBounds/PlayerDetach） ----------
//
// 时序契约（PlayerView.vue 保证）：进入播放页 → PlayerAttach →
// PlayerSetBounds(真实几何, show=false) 起手摆位 →（spawn 前 SetWid 生效）
// → PlayerOpen → 每次容器几何变化/状态切换重报 PlayerSetBounds →
// 离开页面 PlayerDetach（隐藏子窗，实例留复用）。ZHENGUO_NO_EMBED=1 或
// 非 Windows 平台 attach 返回 embedded:false，播放链路整体退化为现行独立窗。
// ⚠ 起手必须先摆位再 open：mpv 的 drawable 取自 spawn 那一刻的窗口尺寸，
// 先 1×1 后 resize 不保证重建（第六十一轮真机黑屏的成因）。

// PlayerAttach 建立/复用视频承载子窗并把其 HWND 交给播放服务。
// 返回 JSON {embedded bool, reason string}；失败不报 error——回退是正常分支。
func (a *App) PlayerAttach() (string, error) {
	if !mpv.EmbedAllowed(os.Getenv) {
		return marshalJSON(map[string]any{"embedded": false, "reason": mpv.EnvNoEmbed})
	}
	a.embedMu.Lock()
	defer a.embedMu.Unlock()
	if a.embed == nil {
		host, err := mpv.NewEmbedHost()
		if err != nil {
			return marshalJSON(map[string]any{"embedded": false, "reason": err.Error()})
		}
		a.embed = host
	}
	a.playerSvc.SetWid(a.embed.HWND())
	// 现场一起返回：真机「音频有画面黑」只能靠它定位（见 mpv.HostDebug）
	appLogEvent("attach hwnd=0x%x", uint64(a.embed.HWND()))
	return marshalJSON(map[string]any{"embedded": true, "host": a.embed.Debug()})
}

// PlayerSetBounds 摆放/显隐视频子窗（物理像素，相对主窗客户区）。
// show 与几何是两件事：量出尺寸就摆位，show 只决定看不看得见；
// W/H <1 才是「没有可摆的几何」，此时整窗 SW_HIDE。
// （旧签名把「隐藏」编码成 0×0，于是隐藏态永远不摆位 —— mpv 起进程时
// 拿到的是创建尺寸的 1×1 drawable，见 mpv.Bounds.Show 的注释。）
func (a *App) PlayerSetBounds(x, y, w, h int, show bool) (string, error) {
	// 第九十七轮 alpha.42：计数挪到锁**前**。alpha.41 真机 arrived=0 时，
	// 计数器还压在 embedMu.Lock() 之后——分不清「IPC 没到 Go」和「到了但
	// 卡锁」。现在：函数体一进入就 +1（arrived 从此=「到达过函数体」的
	// 真值），waitMs 单独记锁排队时长；两个数并排，三种故障形状互斥：
	//   前端#N>0 + arrived=0     → IPC 投递层丢/挂（看 wails.* 行定案）
	//   arrived=N + waitMs 大     → 到了 Go，锁被别的持锁者占
	//   arrived=N + waitMs 小     → 到了也执行了，问题在 ApplyBounds 之后
	waitStart := time.Now()
	appLogSetBoundsArrived()
	a.embedMu.Lock()
	defer a.embedMu.Unlock()
	appLogSetBounds(x, y, w, h, show, time.Since(waitStart).Milliseconds())
	if a.embed == nil {
		return marshalJSON(map[string]any{"ok": false})
	}
	if err := a.embed.ApplyBounds(mpv.Bounds{X: x, Y: y, W: w, H: h, Show: show}); err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"ok": true, "host": a.embed.Debug()})
}

// PlayerHitBands 上报视频矩形上/下「点击穿透带」高度（物理像素；前端按
// DOM 覆盖层与 stage 矩形的实际重叠 × devicePixelRatio 计算，0=该侧不禁）。
// 命中带内的鼠标经 WM_NCHITTEST 返回 HTTRANSPARENT 让给 WebView2（覆盖层
// 按钮可点），带外维持 mpv 原生手势。未内嵌（attach 未成功/回退开关）
// no-op 返回 nil；非法入参报错（前端换算应保证非负有限数）。
func (a *App) PlayerHitBands(top, bottom float64) error {
	if math.IsNaN(top) || math.IsNaN(bottom) || math.IsInf(top, 0) || math.IsInf(bottom, 0) {
		return fmt.Errorf("playerHitBands: 非法数值 top=%v bottom=%v", top, bottom)
	}
	if top < 0 || bottom < 0 {
		return fmt.Errorf("playerHitBands: 带宽不得为负 top=%v bottom=%v", top, bottom)
	}
	const maxBand = 1 << 30 // 钳到 int32 安全区；超限视同覆盖全窗（TransparentAt 退化分支）
	qt, qb := math.Round(top), math.Round(bottom)
	if qt > maxBand {
		qt = maxBand
	}
	if qb > maxBand {
		qb = maxBand
	}
	a.embedMu.Lock()
	defer a.embedMu.Unlock()
	if a.embed == nil {
		return nil // 未内嵌：独立窗/回退态没有 host 窗可判，静默忽略
	}
	a.playerSvc.PlayerSetHitBands(int32(qt), int32(qb))
	return nil
}

// PlayerDetach 离开播放页：隐藏子窗（含 mpv 子树）。子窗保留复用，
// 再进播放页 attach 幂等。
func (a *App) PlayerDetach() (string, error) {
	a.embedMu.Lock()
	defer a.embedMu.Unlock()
	if a.embed != nil {
		a.embed.Hide()
		appLogEvent("detach")
	}
	return marshalJSON(map[string]any{"ok": true})
}

// PlayerForceWindow 逃生门：把播放从"内嵌子窗"退回"mpv 独立窗口"。
//
// 为什么要它：真机连续两版反馈"有声音、画面全黑"，而 attach 已经不再报错
// （「画面未内嵌」徽标没出现），说明子窗建出来了、mpv 也拿到了 --wid，
// 问题只剩"画面没显示出来"这一层——可能是 z-order、可能是尺寸事件、
// 也可能是 mpv 的渲染上下文。这些我在本机一个都复现不了，
// 但用户不该在我查清之前一直看黑屏。
//
// 做法：藏掉子窗 + 把服务的 wid 归零（0 = 不内嵌，v1 `--wid` 缺省语义），
// 由前端重开当前集，mpv 就会自己开一个顶层窗。
// ⚠ 这只改"画面落在哪个窗"，不改播放/会话/弹幕任何语义。
func (a *App) PlayerForceWindow() (string, error) {
	a.embedMu.Lock()
	defer a.embedMu.Unlock()
	if a.embed != nil {
		a.embed.Hide()
	}
	a.playerSvc.SetWid(0)
	appLogEvent("force-window")
	return marshalJSON(map[string]any{"ok": true, "embedded": false})
}

// EmbedDiag 诊断绑定（第九十六轮）：app.log 的自读口 + exe 身份。
// 前端徽标/复制诊断带出 arrived=（Go 实际收到的 setBounds 次数）——
// 与前端自己量的「前端量得#N」并排，两侧读数对撞即可判「IPC 没到」还是
// 「到了没执行」。开发构建 version 为空=未注入 ldflags，照实回空串。
func (a *App) EmbedDiag() (string, error) {
	out := map[string]any{
		"version": buildVersion,
		"revision": func() string {
			if bi := readBuildVCS(); bi != nil {
				if r, ok := bi["revision"].(string); ok {
					return r
				}
			}
			return ""
		}(),
		"pid":     os.Getpid(),
		"arrived": appLogSetBoundsCount(),
		"logDir": func() string {
			dir, err := native.DataDir()
			if err != nil {
				return ""
			}
			return dir
		}(),
		"logFail": appLogger.fail(),
	}
	return marshalJSON(out)
}

func marshalJSON(v any) (string, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func unmarshalObject(s string) (map[string]any, error) {
	if s == "" {
		return nil, jsonUnmarshalError("drama/chapter 不能为空")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}

type jsonUnmarshalError string

func (e jsonUnmarshalError) Error() string { return string(e) }
