// shell_seams.go —— 壳运行时缝的声明（无 build tag，两壳共享）。
//
// 第九十八轮（Electron 迁移）：App 结构体与绑定方法只调这三个函数变量，
// 不直接 import wails runtime。默认实现是 no-op（安全兜底：万一某壳忘了
// init 接线，事件/窗口操作被静默忽略而不是 panic）。
//
//   - shell_wails.go（!zgj_sidecar）的 init 把它们接到 Wails runtime.*；
//   - shell_stdio.go（zgj_sidecar）的 init 把它们接到 stdio 协议。
//
// 为什么用变量而不是接口/方法：App 是包级结构体、绑定方法签名被 Wails Bind
// 反射约束，var 是最小侵入的注入点。
package main

import "context"

// emitEventFunc 事件下行。默认 no-op。
var emitEventFunc = func(ctx context.Context, name string, data ...any) {}

// windowShowFunc 托盘「显示主窗口」。默认 no-op。
var windowShowFunc = func(ctx context.Context) {}

// windowQuitFunc 托盘「退出」/更新重启。默认 no-op。
var windowQuitFunc = func(ctx context.Context) {}
