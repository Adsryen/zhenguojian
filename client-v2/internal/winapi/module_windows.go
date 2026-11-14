//go:build windows

// Package winapi 收拢跨包共用的 Win32 小工具。
//
// 为什么单独有这一个包：真机反馈（alpha.27 截图那句
// 「mpv: RegisterClassExW(ZgjVideoHost) 失败: The parameter is incorrect.」）
// 追下来是**同一个错误在四个地方各犯了一遍** ——
// 把 `windows.CurrentProcess()` 当成 HINSTANCE 用。
// 查 x/sys 源码可见它返回的是进程伪句柄：
//
//	func CurrentProcess() Handle { return Handle(^uintptr(1 - 1)) }  // 0xFFFFFFFFFFFFFFFF
//
// 而 RegisterClassExW / CreateWindowExW / LoadImageW 要的是
// "装着这段代码/资源的那个模块"的句柄。传伪句柄的结果：
//
//	· RegisterClassExW → ERROR_INVALID_PARAMETER(87)（窗口类注册失败）；
//	· CreateWindowExW  → 同样拿不到合法模块；
//	· LoadImageW 从 exe 资源取图标 → 前两级回落必然失败。
//
// 一个错误四处重复，正是"各自抄一遍、没人核过"的形状，所以收成一个函数。
package winapi

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SelfModule 返回本 exe 的模块句柄，可直接当 WNDCLASSEXW.hInstance /
// CreateWindowExW 的 hInstance / LoadImageW 的 hinst 使用。
//
// 用 GetModuleHandleExW(0, NULL, &h)：flags=0 且名字为空时，
// MSDN 明确返回"调用方进程的主模块句柄"。
// 失败时返回 error（调用方不要把 0 句柄继续往下传——那正是这次的 bug 形状）。
func SelfModule() (windows.Handle, error) {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	getModuleHandleEx := kernel32.NewProc("GetModuleHandleExW")
	var h windows.Handle
	ret, _, err := getModuleHandleEx.Call(0, 0, uintptr(unsafe.Pointer(&h)))
	if ret == 0 {
		return 0, fmt.Errorf("winapi: GetModuleHandleExW 失败: %w", err)
	}
	if h == 0 {
		return 0, fmt.Errorf("winapi: GetModuleHandleExW 返回零句柄")
	}
	return h, nil
}
