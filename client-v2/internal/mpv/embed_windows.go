//go:build windows

package mpv

// embed_windows.go：宿主主窗口发现 + ZgjVideoHost WS_CHILD 子窗生命周期。
// 所有 Win32 调用集中在这里（CGO_ENABLED=0 下走 golang.org/x/sys/windows
// LazyProc，无 cgo）；纯逻辑（候选筛选、几何换算、开关）在 embed.go，
// 本文件只负责取数与执行，便于 Linux 侧 mock 测试。

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wsChildEmbed      = 0x40000000 // WS_CHILD
	swHide            = 0          // ShowWindow: SW_HIDE
	swShowNA          = 8          // SW_SHOWNA：显示但不激活（不抢 webview 焦点）
	swpNoSize         = 0x0001
	swpNoMove         = 0x0002
	swpNoActivate     = 0x0010
	hwndTop           = 0 // HWND_TOP：置顶到兄弟窗（WebView2 子窗）之上
	blackBrushStock   = 4 // GetStockObject(BLACK_BRUSH)：未覆盖区域刷黑，防白闪
	hostFindTimeout   = 2 * time.Second
	hostFindPollEvery = 50 * time.Millisecond
)

var (
	user32Embed = windows.NewLazySystemDLL("user32.dll")
	procEmbed   = map[string]*windows.LazyProc{}
	kernelEmbed = windows.NewLazySystemDLL("kernel32.dll")
	gdi32Embed  = windows.NewLazySystemDLL("gdi32.dll")
)

func init() {
	for _, n := range []string{
		"EnumWindows", "GetWindowThreadProcessId", "GetClassNameW", "GetWindowTextW",
		"IsWindowVisible", "RegisterClassExW", "CreateWindowExW", "DefWindowProcW",
		"MoveWindow", "ShowWindow", "SetWindowPos", "DestroyWindow",
	} {
		procEmbed[n] = user32Embed.NewProc(n)
	}
	procEmbed["GetCurrentProcessId"] = kernelEmbed.NewProc("GetCurrentProcessId")
	procEmbed["GetStockObject"] = gdi32Embed.NewProc("GetStockObject")
}

// wndclassexw：x/sys/windows 未导出 WNDCLASSEXW，按 64 位内存布局手写
// （沿用 spike/mpvembed/window_windows.go 验证过的结构）。
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

var (
	embedClassOnce sync.Once
	embedClassErr  error
)

func embedWndProc(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	ret, _, _ := procEmbed["DefWindowProcW"].Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

func registerEmbedClass() error {
	embedClassOnce.Do(func() {
		cls, err := windows.UTF16PtrFromString(EmbedWindowClass)
		if err != nil {
			embedClassErr = err
			return
		}
		bg, _, _ := procEmbed["GetStockObject"].Call(uintptr(blackBrushStock))
		var wc wndclassexw
		wc.CbSize = uint32(unsafe.Sizeof(wc))
		wc.LpfnWndProc = windows.NewCallback(embedWndProc)
		wc.HInstance = windows.CurrentProcess()
		wc.HBackground = windows.Handle(bg)
		wc.LpszClassName = cls
		// LazyProc.Call 第三返回值即 GetLastError。撞类容忍
		// ERROR_CLASS_ALREADY_EXISTS（spike 版误用第二返回值，此处修正）。
		ret, _, err := procEmbed["RegisterClassExW"].Call(uintptr(unsafe.Pointer(&wc)))
		if ret == 0 && err != windows.ERROR_CLASS_ALREADY_EXISTS {
			embedClassErr = fmt.Errorf("mpv: RegisterClassExW(%s) 失败: %w", EmbedWindowClass, err)
		}
	})
	return embedClassErr
}

// enumTopWindows 同步枚举本进程可见性未知的全部顶层窗（候选记录）。
func enumTopWindows() []WindowCandidate {
	selfPID, _, _ := procEmbed["GetCurrentProcessId"].Call()
	var cands []WindowCandidate
	cb := windows.NewCallback(func(hwnd, _ uintptr) uintptr {
		defer func() { _ = recover() }() // 回调内 panic 不能炸穿 EnumWindows
		var pid uint32
		procEmbed["GetWindowThreadProcessId"].Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid != uint32(selfPID) {
			return 1 // 非本进程直接跳过，省两次取串
		}
		visible, _, _ := procEmbed["IsWindowVisible"].Call(hwnd)
		cands = append(cands, WindowCandidate{
			Hwnd:      hwnd,
			PID:       pid,
			ClassName: winString(procEmbed["GetClassNameW"], hwnd),
			Title:     winString(procEmbed["GetWindowTextW"], hwnd),
			Visible:   visible != 0,
		})
		return 1
	})
	procEmbed["EnumWindows"].Call(cb, 0)
	return cands
}

// winString 调 GetClassNameW/GetWindowTextW 取 UTF16 串。
func winString(proc *windows.LazyProc, hwnd uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := proc.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)-1))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

// findHostWindow 轮询顶层窗候选直至拿到宿主主窗 HWND 或超时。
func findHostWindow(timeout time.Duration) (windows.HWND, error) {
	deadline := time.Now().Add(timeout)
	for {
		if hwnd := PickHostWindow(enumTopWindows(), uint32(windows.GetCurrentProcessId()), WailsWindowClass, MainWindowTitle); hwnd != 0 {
			return windows.HWND(hwnd), nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("mpv: 未找到宿主主窗口（class=%s title=%s）", WailsWindowClass, MainWindowTitle)
		}
		time.Sleep(hostFindPollEvery)
	}
}

// EmbedHost 持有宿主主窗与视频子窗。并发入口在 app.go embedMu 串行化，
// 这里再带一把锁兜底。
type EmbedHost struct {
	mu     sync.Mutex
	parent windows.HWND
	child  windows.HWND
}

// NewEmbedHost 发现 Wails 主窗口并在其下创建隐藏的视频承载子窗。
// 非 Windows 平台由 embed_stub.go 返回错误。
func NewEmbedHost() (*EmbedHost, error) {
	parent, err := findHostWindow(hostFindTimeout)
	if err != nil {
		return nil, err
	}
	if err := registerEmbedClass(); err != nil {
		return nil, err
	}
	cls, err := windows.UTF16PtrFromString(EmbedWindowClass)
	if err != nil {
		return nil, err
	}
	// 先建成隐藏态（不带 WS_VISIBLE）：首帧就绪前不放画面，避免黑块抢位。
	ret, callErr, _ := procEmbed["CreateWindowExW"].Call(
		0,
		uintptr(unsafe.Pointer(cls)),
		0,
		uintptr(wsChildEmbed),
		0, 0, 1, 1,
		uintptr(parent), 0, uintptr(windows.CurrentProcess()), 0,
	)
	if ret == 0 {
		return nil, fmt.Errorf("mpv: CreateWindowExW(%s) 失败 (lasterr=%d)", EmbedWindowClass, callErr)
	}
	return &EmbedHost{parent: parent, child: windows.HWND(ret)}, nil
}

// HWND 返回子窗句柄（供 --wid 使用）。未创建时返回 0。
func (e *EmbedHost) HWND() uintptr {
	e.mu.Lock()
	defer e.mu.Unlock()
	return uintptr(e.child)
}

// ApplyBounds 按前端上报几何移动/显示子窗；零尺寸隐藏整棵子树（含 mpv 窗）。
// 每次显示都 SetWindowPos(HWND_TOP) 把子窗提到 WebView2 兄弟窗之上
// （airspace：原生子窗绘制永远盖过 DOM，故视频矩形内的 DOM 不可见/不可点）。
func (e *EmbedHost) ApplyBounds(b Bounds) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.child == 0 {
		return errors.New("mpv: 子窗已销毁")
	}
	r, visible := VideoRect(b)
	if !visible {
		procEmbed["ShowWindow"].Call(uintptr(e.child), swHide)
		return nil
	}
	// MoveWindow(bMove=true) → SW_SHOWNA（显示不夺焦）→ 提 z-order。
	procEmbed["MoveWindow"].Call(uintptr(e.child), uintptr(r.X), uintptr(r.Y), uintptr(r.W), uintptr(r.H), 1)
	procEmbed["ShowWindow"].Call(uintptr(e.child), swShowNA)
	procEmbed["SetWindowPos"].Call(uintptr(e.child), uintptr(hwndTop), 0, 0, 0, 0,
		uintptr(swpNoSize|swpNoMove|swpNoActivate))
	return nil
}

// Hide 隐藏子窗（页面离开/暂停展示 DOM 覆盖层时用）。
func (e *EmbedHost) Hide() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.child != 0 {
		procEmbed["ShowWindow"].Call(uintptr(e.child), swHide)
	}
}

// Close 销毁子窗（当前调用方只有测试/未来扩展；正常退出随主窗析构）。
func (e *EmbedHost) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.child == 0 {
		return nil
	}
	if ret, _, err := procEmbed["DestroyWindow"].Call(uintptr(e.child)); ret == 0 {
		return fmt.Errorf("mpv: DestroyWindow 失败 (%w)", err)
	}
	e.child = 0
	return nil
}
