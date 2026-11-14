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

	"github.com/wailsapp/wails/v2/pkg/runtime"

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
	// trayHintShown 首次 X→托盘 提示只弹一次。
	trayHintShown atomic.Bool
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
				runtime.EventsEmit(a.ctx, event, payload)
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
				runtime.EventsEmit(a.ctx, name, data...)
			}
		},
		DataDirFunc: native.DataDir,
	})
	// V-TRAY：托盘句柄（Start 挂 startup，Register 失败仅记状态不致命）。
	// 回调在 tray 包的转发协程上执行——runtime API 走 PostMessage，线程安全。
	a.trayH = tray.New(tray.Actions{
		Restore: func() {
			if a.ctx != nil {
				runtime.WindowShow(a.ctx)
			}
		},
		CheckUpdate: func() {
			// 复用 update:available 横幅 + 原生结果框（见 updater.CheckManual）。
			a.updateService().CheckManual(context.Background())
		},
		Quit: func() {
			a.allowQuit()
			if a.ctx != nil {
				runtime.Quit(a.ctx)
			}
		},
	})
	// 手动检查的原生提示出口：Windows=MessageBoxW；失败/非 Windows 回落事件
	// （前端未监听 update:notify，Windows 之外仅开发机，真机验证点见 tray_notes.md）。
	a.updateService().Notify = func(title, text string) {
		if err := tray.ShowInfoBox(title, text); err != nil {
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "update:notify", map[string]any{"title": title, "text": text})
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

// beforeClose 挂 wails OnBeforeClose：返回 true=允许关闭。X 不再退出——隐藏主窗
// 进托盘（进程与下载/合并任务继续，双击托盘图标恢复）；托盘未运行（非 Windows
// 或注册失败）时保持旧行为直接退出，避免窗口无处可回。
func (a *App) beforeClose(ctx context.Context) bool {
	if a.quitting.Load() || a.trayH == nil || a.trayH.Status() != "running" {
		return true
	}
	runtime.WindowHide(ctx)
	if !a.trayHintShown.Swap(true) {
		go func() {
			_ = tray.ShowInfoBox("已最小化到托盘",
				"程序仍在后台运行（下载/合并不中断）。托盘图标可能被收进「显示隐藏的图标」箭头里；"+
					"右键图标选择「退出程序」才是真退出。")
		}()
	}
	return false
}

// startup 在 wails 窗口就绪前调用；引擎 initialize 幂等且本地很快
// （spike 实测 0.0s），失败不致命——后续每次绑定调用都会重试 Init。
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
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

// shutdown 挂 wails OnShutdown：停托盘（RemoveIcon + 消息线程收尾）、
// 关停播放服务（Quit mpv、停 tick、停弹幕抓取）。
func (a *App) shutdown(ctx context.Context) {
	a.trayH.Stop()
	a.downloadSvc.OnShutdown(ctx)
	_ = a.playerSvc.Close()
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
func (a *App) PlayerState() (string, error)          { return marshalJSON(a.playerSvc.State()) }
// PlayerClosePlayer 停"这一次播放"（Stop，**非终态**）。
// 第五十九轮真机反馈修正：这里原来调 `Close()`（终态，置 closed 后
// Open 永远返回 ErrClosed），而 playerSvc 是应用生命周期的（本文件只建一次），
// 于是用户点一次「关闭」，本进程内再也播不了 ——
// 截图里那句红字「播放失败：player: 服务已关闭」就是它。
// 应用退出的终态清理走 app.go:157 那一路，仍然调 Close()。
func (a *App) PlayerClosePlayer() (string, error)    { return "", a.playerSvc.Stop() }

// ---------- V2-4 播放画面内嵌（PlayerAttach/PlayerSetBounds/PlayerDetach） ----------
//
// 时序契约（PlayerView.vue 保证）：进入播放页 → PlayerAttach →（spawn 前
// SetWid 生效）→ PlayerOpen → 每次容器几何变化/状态切换 PlayerSetBounds →
// 离开页面 PlayerDetach（隐藏子窗，实例留复用）。ZHENGUO_NO_EMBED=1 或
// 非 Windows 平台 attach 返回 embedded:false，播放链路整体退化为现行独立窗。

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
	return marshalJSON(map[string]any{"embedded": true})
}

// PlayerSetBounds 按视频容器矩形移动/显隐子窗（物理像素，相对主窗客户区；
// W/H <1 表示隐藏画面，让位给 DOM 覆盖层）。
func (a *App) PlayerSetBounds(x, y, w, h int) (string, error) {
	a.embedMu.Lock()
	defer a.embedMu.Unlock()
	if a.embed == nil {
		return marshalJSON(map[string]any{"ok": false})
	}
	if err := a.embed.ApplyBounds(mpv.Bounds{X: x, Y: y, W: w, H: h}); err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"ok": true})
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
	}
	return marshalJSON(map[string]any{"ok": true})
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
