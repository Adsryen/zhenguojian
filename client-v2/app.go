// Package main —— 真果鉴 v2 桌面壳（Wails v2 + WebView2）。
// app.go 是绑定注册表：各域一行注册（V2-1 起 engine 门面），
// 后续任务按 design.md 约定在自己文件里定义 Bind<X>，由主代理统一在此追加。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"zhenguojian/client-v2/internal/downloadsvc"
	"zhenguojian/client-v2/internal/native"
	"zhenguojian/client-v2/internal/player"
)

type App struct {
	ctx context.Context
	// V2-3b：播放服务（懒 spawn mpv；生命周期钩子 + 绑定方法均委托它）。
	playerSvc *player.Service
	// V2-3：下载与合并服务（轮询事件 + 合并队列；绑定见 bindings_download.go）。
	downloadSvc *downloadsvc.Service
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
		// 窗口内嵌句柄：现有壳无 HWND 管线，0 = mpv 独立窗口（真机遗留项）。
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
	return a
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
}

// shutdown 挂 wails OnShutdown：关停播放服务（Quit mpv、停 tick、停弹幕抓取）。
func (a *App) shutdown(ctx context.Context) {
	a.downloadSvc.OnShutdown(ctx)
	_ = a.playerSvc.Close()
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
func (a *App) DanmakuAll(session string) (string, error) {
	data, err := native.DanmakuAll(session)
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
	data, err := native.Resolve(req.Drama, req.Chapter, req.Index, 0)
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

// PlayerOpen dramaJSON/chapterJSON = 详情页透传的原样对象；index 为 1 起始集号。
// Go 内部走核心 resolve（同 v1 编排），返回 player.Snapshot 的 JSON。
func (a *App) PlayerOpen(dramaJSON, chapterJSON string, index int) (string, error) {
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
	title, _ := drama["title"].(string)
	dramaID, _ := drama["id"].(string)
	err = a.playerSvc.Open(a.ctx, player.Request{
		DramaID: dramaID,
		Title:   title,
		Index:   index,
		Drama:   drama,
		Chapter: chapter,
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
func (a *App) PlayerState() (string, error)          { return marshalJSON(a.playerSvc.State()) }
func (a *App) PlayerClosePlayer() (string, error)    { return "", a.playerSvc.Close() }

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
