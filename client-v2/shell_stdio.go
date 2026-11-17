//go:build zgj_sidecar

// shell_stdio.go —— sidecar 壳的运行时缝实现（与 shell_wails.go 对偶）。
//
// 事件下行：emitEventFunc 把事件写进 stdio 协议通道（{"event":name,"data":…}
// 行），Electron 主进程（P2）读同一管道转发给渲染进程 window.runtime shim。
// 窗口操作：sidecar 没有窗——窗是 Electron 主进程自己的 BrowserWindow。
// windowShowFunc/windowQuitFunc 在这里是 no-op（语义转移，不是丢失）：
// 托盘「显示主窗口」由 Electron 主进程 win.show() 完成；「退出」由 Electron
// 主进程走协议的 __shutdown 动作 + app.quit() 完成。
package main

import (
	"context"
	"encoding/json"
)

// protoEvent 事件下行行（与 Wails 的 EventsEmit 对应，前端形状一致：
// 事件名 + 一个 payload 参数）。
type protoEvent struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

// sidecarEventSink 由 sidecar_main.go 在 serve 前接线（持有写通道互斥锁）。
// nil = 事件丢弃（测试未接线时不炸）。
var sidecarEventSink func(name string, data any)

// sidecarQuit 由 sidecar_main.go 接线：优雅收尾（stop 下载/mpv）后退出。
var sidecarQuit func()

func init() {
	emitEventFunc = func(ctx context.Context, name string, data ...any) {
		if sidecarEventSink == nil {
			return
		}
		var payload any
		if len(data) == 1 {
			payload = data[0]
		} else if len(data) > 1 {
			payload = data
		}
		// 序列化失败不能丢事件名——前端按名字路由，payload 空好过事件没到。
		if _, err := json.Marshal(payload); err != nil {
			payload = nil
		}
		sidecarEventSink(name, payload)
	}
	windowShowFunc = func(ctx context.Context) {
		// no-op：Electron 主进程自己 show()（见文件头）。
	}
	windowQuitFunc = func(ctx context.Context) {
		if sidecarQuit != nil {
			go sidecarQuit()
		}
	}
}
