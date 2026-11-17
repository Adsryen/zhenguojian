//go:build !zgj_sidecar

// shell_wails.go —— Wails 壳缝实现（!zgj_sidecar）。
//
// 把 shell_seams.go 声明的三个缝接到 Wails runtime（行为与第九十八轮改造前
// 逐位一致：runtime.EventsEmit / WindowShow / Quit）。sidecar 构建排除本文件
// （它 import wails runtime），由 shell_stdio.go 的 init 接管。
package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func init() {
	emitEventFunc = func(ctx context.Context, name string, data ...any) {
		runtime.EventsEmit(ctx, name, data...)
	}
	windowShowFunc = func(ctx context.Context) {
		runtime.WindowShow(ctx)
	}
	windowQuitFunc = func(ctx context.Context) {
		runtime.Quit(ctx)
	}
}
