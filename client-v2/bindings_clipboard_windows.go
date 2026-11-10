//go:build windows

package main

// Win32 剪贴板实现：CGO_ENABLED=0 下全部走 x/sys/windows LazyProc
// （DLL 句柄模式沿用 internal/mpv/embed_windows.go：NewLazySystemDLL + NewProc）。
// 指针策略：不把 Win32 返回的 uintptr 转回 unsafe.Pointer——go vet 的
// unsafeptr 检查不豁免 LazyProc.Call 返回值；内存拷贝统一经
// kernel32!MoveMemory（缺失时回落 ntdll!RtlMoveMemory），只在参数位置做
// unsafe.Pointer→uintptr 转换（syscall 调用表达式内联，符合安全规则）。
// 内存所有权规则：SetClipboardData 成功后句柄归系统，禁止再 GlobalFree；
// 失败路径必须自行 GlobalFree，否则泄漏。CloseClipboard 经 defer 释放占用。

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cfUnicodeText = 13     // CF_UNICODETEXT
	gmemMoveable  = 0x0002 // GMEM_MOVEABLE（SetClipboardData 要求可移动句柄）
	clipOpenTries = 5      // OpenClipboard 重试次数（剪贴板可能被别的进程短时占用）
	clipOpenWait  = 40 * time.Millisecond
)

var (
	user32Clip   = windows.NewLazySystemDLL("user32.dll")
	kernel32Clip = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard    = user32Clip.NewProc("OpenClipboard")
	procCloseClipboard   = user32Clip.NewProc("CloseClipboard")
	procEmptyClipboard   = user32Clip.NewProc("EmptyClipboard")
	procSetClipboardData = user32Clip.NewProc("SetClipboardData")
	procGetClipboardData = user32Clip.NewProc("GetClipboardData")
	procClipFormatAvail  = user32Clip.NewProc("IsClipboardFormatAvailable")
	procGlobalAllocClip  = kernel32Clip.NewProc("GlobalAlloc")
	procGlobalFreeClip   = kernel32Clip.NewProc("GlobalFree")
	procGlobalLockClip   = kernel32Clip.NewProc("GlobalLock")
	procGlobalUnlockClip = kernel32Clip.NewProc("GlobalUnlock")
	procGlobalSizeClip   = kernel32Clip.NewProc("GlobalSize")
)

// procMoveMemory：kernel32!MoveMemory 优先，取不到回落 ntdll!RtlMoveMemory。
var procMoveMemory = func() *windows.LazyProc {
	if p := kernel32Clip.NewProc("MoveMemory"); p.Find() == nil {
		return p
	}
	return windows.NewLazySystemDLL("ntdll.dll").NewProc("RtlMoveMemory")
}()

// moveMemory 在两个裸地址间拷贝 size 字节；调用点保证对应 Go 侧切片
// 在 Call 表达式期间存活（地址参数内联转换，不被 GC 移动）。
func moveMemory(dst, src, size uintptr) error {
	if r, _, errno := procMoveMemory.Call(dst, src, size); r == 0 && errno != nil {
		return errno
	}
	return nil
}

// clipOpen 带重试的 OpenClipboard(NULL)；成功返回 true。
func clipOpen() bool {
	for i := 0; i < clipOpenTries; i++ {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			return true
		}
		time.Sleep(clipOpenWait)
	}
	return false
}

func clipboardWrite(text string) error {
	data := clipboardUTF16Bytes(text)
	if !clipOpen() {
		return fmt.Errorf("无法打开系统剪贴板（可能被其他程序占用）")
	}
	defer procCloseClipboard.Call()
	if r, _, _ := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("清空系统剪贴板失败")
	}
	hMem, _, _ := procGlobalAllocClip.Call(gmemMoveable, uintptr(len(data)))
	if hMem == 0 {
		return fmt.Errorf("分配剪贴板内存失败（%d 字节）", len(data))
	}
	lockAddr, _, _ := procGlobalLockClip.Call(hMem)
	if lockAddr == 0 {
		procGlobalFreeClip.Call(hMem)
		return fmt.Errorf("锁定剪贴板内存失败")
	}
	if err := moveMemory(lockAddr, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data))); err != nil {
		procGlobalUnlockClip.Call(hMem)
		procGlobalFreeClip.Call(hMem)
		return fmt.Errorf("复制文本到剪贴板内存失败: %w", err)
	}
	procGlobalUnlockClip.Call(hMem)
	if r, _, _ := procSetClipboardData.Call(cfUnicodeText, hMem); r == 0 {
		// 失败时所有权未转移，需自行释放
		procGlobalFreeClip.Call(hMem)
		return fmt.Errorf("写入剪贴板数据失败")
	}
	// 成功：内存归系统所有，CloseClipboard（defer）提交内容。
	return nil
}

func clipboardRead() (string, error) {
	if r, _, _ := procClipFormatAvail.Call(cfUnicodeText); r == 0 {
		return "", nil // 无文本内容：空串，非错误
	}
	if !clipOpen() {
		return "", fmt.Errorf("无法打开系统剪贴板（可能被其他程序占用）")
	}
	defer procCloseClipboard.Call()
	hMem, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if hMem == 0 {
		return "", fmt.Errorf("获取剪贴板数据句柄失败")
	}
	size, _, _ := procGlobalSizeClip.Call(hMem)
	if size == 0 {
		return "", fmt.Errorf("剪贴板内存块大小为 0")
	}
	lockAddr, _, _ := procGlobalLockClip.Call(hMem)
	if lockAddr == 0 {
		return "", fmt.Errorf("锁定剪贴板内存失败")
	}
	defer procGlobalUnlockClip.Call(hMem)
	buf := make([]byte, size)
	if err := moveMemory(uintptr(unsafe.Pointer(&buf[0])), lockAddr, size); err != nil {
		return "", fmt.Errorf("读取剪贴板内存失败: %w", err)
	}
	return utf16BytesToString(buf), nil
}
