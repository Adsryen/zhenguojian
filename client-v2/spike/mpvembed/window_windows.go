//go:build windows

package mpvembed

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 用户态最小子窗口：mpv --wid 需要一个 HWND；v2 里 parent 传 Wails 主窗口的
// 内容区宿主 div 对应的原生窗（或整窗覆盖后前端留出播放器矩形）。
// spike 只保证 API 形状 + 编译；真机嵌入效果留 v2 实施期在 Windows 上验。

const (
	wsChild   = 0x40000000 // WS_CHILD
	wsVisible = 0x10000000 // WS_VISIBLE
)

var (
	user32        = windows.NewLazySystemDLL("user32.dll")
	procRegClass  = user32.NewProc("RegisterClassExW")
	procCreateWnd = user32.NewProc("CreateWindowExW")
	procDefWndPr  = user32.NewProc("DefWindowProcW")
	classOnce     sync.Once
	classErr      error
)

func wndProc(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	ret, _, _ := procDefWndPr.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

// wndclassexw：x/sys/windows 未导出该类型，按 64 位 WNDCLASSEXW 内存布局手写
// （cbSize/style 各4字节，lpfnWndProc 8，cbClsExtra/cbWndExtra 各4，
// hInstance/hIcon/hCursor/hBackground 各8，两个类名/菜单名字符串指针）。
type wndclassexw struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HBackground   windows.Handle
	LpszClassName *uint16
	LpszMenuName  *uint16
}

func registerClass() error {
	classOnce.Do(func() {
		cls, err := windows.UTF16PtrFromString("zgjMPVEmbed")
		if err != nil {
			classErr = err
			return
		}
		var wc wndclassexw
		wc.CbSize = uint32(unsafe.Sizeof(wc))
		wc.LpfnWndProc = windows.NewCallback(wndProc)
		wc.HInstance = windows.CurrentProcess()
		wc.LpszClassName = cls
		ret, r1, _ := procRegClass.Call(uintptr(unsafe.Pointer(&wc)))
		if ret == 0 && syscall.Errno(r1) != windows.ERROR_CLASS_ALREADY_EXISTS {
			classErr = fmt.Errorf("RegisterClassExW failed (%d)", r1)
		}
	})
	return classErr
}

// NewEmbedWindow 在 parent 下创建一个 WS_CHILD|WS_VISIBLE 子窗口，返回 HWND。
func NewEmbedWindow(parent windows.HWND, x, y, w, h int) (windows.HWND, error) {
	if err := registerClass(); err != nil {
		return 0, err
	}
	cls, err := windows.UTF16PtrFromString("zgjMPVEmbed")
	if err != nil {
		return 0, err
	}
	ret, callErr, _ := procCreateWnd.Call(
		0,
		uintptr(unsafe.Pointer(cls)),
		0,
		uintptr(wsChild|wsVisible),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		uintptr(parent), 0, uintptr(windows.CurrentProcess()), 0,
	)
	if ret == 0 {
		return 0, fmt.Errorf("CreateWindowExW failed (lasterr=%d)", callErr)
	}
	return windows.HWND(ret), nil
}
