//go:build windows

package mpv

import (
	"fmt"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"zhenguojian/client-v2/internal/winapi"
)

// 本文件的目的：让**真正的 Windows 内嵌代码**（embed_windows.go）在受控环境里被
// 真实执行一次，而不是只被编译。真机 alpha.40 的黑屏（子窗 1×1、applyCount=0）
// 在这台 Linux 上从来测不到——因为内嵌只有 Win32 实现。用 Wine 跑这个测试，
// 就能在 CI/本机先把「建子窗 + 摆位 + 读矩形」这条链跑通，再谈发版。
//
// 为什么值得存在：本仓库此前的 Windows 侧测试只有**静态布局自检**（sizeof/偏移），
// 编译级验证；Win32 调用是否真被系统接受，只有运行时才知道。alpha.27 的
// RegisterClassExW 参数错、CurrentProcess 伪句柄，都是「编译过、从没跑过」的错。

// testParentProc：最小父窗 WndProc，一切消息交给 DefWindowProcW。
func testParentProc(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	ret, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("DefWindowProcW").Call(
		uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

// createTestWailsParent 在本进程建一个可见的顶层窗（class=wailsWindow、title=真果鉴），
// 让 findHostWindow 能把它当作宿主。返回父窗句柄与一个销毁函数。
func createTestWailsParent(t *testing.T) (windows.HWND, func()) {
	t.Helper()
	user32 := windows.NewLazySystemDLL("user32.dll")
	reg := user32.NewProc("RegisterClassExW")
	create := user32.NewProc("CreateWindowExW")
	show := user32.NewProc("ShowWindow")
	destroy := user32.NewProc("DestroyWindow")

	cls, err := windows.UTF16PtrFromString(WailsWindowClass)
	if err != nil {
		t.Fatalf("UTF16PtrFromString(%s): %v", WailsWindowClass, err)
	}
	title, err := windows.UTF16PtrFromString(MainWindowTitle)
	if err != nil {
		t.Fatalf("UTF16PtrFromString(%s): %v", MainWindowTitle, err)
	}
	whiteBrush := 5 // 0xFFFFFF / WHITE_BRUSH 常量；父窗背景无所谓
	// 第九十七轮：GetStockObject 在 gdi32.dll，不在 user32.dll。
	// 旧写法 `user32.NewProc("GetStockObject")` 在**真 Windows** 上
	// mustFind 直接 panic（user32 没有该导出）；Wine 6 把它当别名塞进了
	// user32 才一直没炸——又一个「只在 Wine 编译过、从没在真机跑过」的坑。
	// 生产代码 embed_windows.go:56 用的就是 gdi32 的 GetStockObject（procEmbed），
	// 这里照生产取，父窗与子窗同一来源，语义对齐。
	bg, _, _ := procEmbed["GetStockObject"].Call(uintptr(whiteBrush))

	hInst, errInst := winapi.SelfModule()
	if errInst != nil {
		t.Fatalf("SelfModule: %v", errInst)
	}

	var wc wndclassexw // 复用生产同一结构体，字段序错了这里也会一起红
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	wc.LpfnWndProc = windows.NewCallback(testParentProc)
	wc.HInstance = hInst
	wc.HBackground = windows.Handle(bg)
	wc.LpszClassName = cls

	ret, _, callErr := reg.Call(uintptr(unsafe.Pointer(&wc)))
	if ret == 0 && callErr != windows.ERROR_CLASS_ALREADY_EXISTS {
		t.Fatalf("RegisterClassExW(%s) 失败 lasterr=%d", WailsWindowClass, callErr)
	}

	const wsOverlapped = 0x00CF0000 // WS_OVERLAPPEDWINDOW
	const wsVisible = 0x10000000
	phwnd, _, cerr := create.Call(
		0,
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(title)),
		uintptr(wsOverlapped|wsVisible),
		50, 50, 900, 700,
		0, 0, uintptr(hInst), 0,
	)
	if phwnd == 0 {
		t.Fatalf("CreateWindowExW(父窗) 失败 lasterr=%d", cerr)
	}
	_, _, _ = show.Call(phwnd, 5) // SW_SHOW

	return windows.HWND(phwnd), func() {
		_, _, _ = destroy.Call(phwnd)
	}
}

// TestEmbedHostRuntimeUnderWine 跑真实的 NewEmbedHost → ApplyBounds → Debug 链。
//
// 判据逐条对应真机黑屏的读数：
//
//	E1 NewEmbedHost 成功且 HWND 非 0（子窗真的建出来了）
//	E2 ApplyBounds 真实尺寸后，GetWindowRect 读回的不是 1×1（1×1=卡死在创建尺寸）
//	E3 Debug().ApplyCount >= 1（Go 侧真的执行过一次摆位）
//	E4 Show=false 时子窗仍被摆到真实尺寸（几何/可见性分离，隐藏态不抹几何）
func TestEmbedHostRuntimeUnderWine(t *testing.T) {
	parent, cleanup := createTestWailsParent(t)
	defer cleanup()
	t.Logf("父窗 hwnd=0x%x（class=%s title=%s）", parent, WailsWindowClass, MainWindowTitle)

	host, err := NewEmbedHost()
	if err != nil {
		t.Fatalf("E1 NewEmbedHost 失败: %v", err)
	}
	if host.HWND() == 0 {
		t.Fatalf("E1 子窗句柄为 0，未真正创建")
	}
	t.Logf("E1 子窗已建 hwnd=0x%x", host.HWND())

	// E4：先按「隐藏 + 真实尺寸」摆一次（对应前端起手 show=false 的那次上报）
	if err := host.ApplyBounds(Bounds{X: 100, Y: 50, W: 780, H: 614, Show: false}); err != nil {
		t.Fatalf("E4 ApplyBounds(show=false) 失败: %v", err)
	}
	if d := host.Debug(); d.ChildWidth < 2 || d.ChildHeight < 2 {
		t.Fatalf("E4 隐藏态子窗仍是创建尺寸 1×1（w=%d h=%d），几何没摆进去", d.ChildWidth, d.ChildHeight)
	}

	// E2：显示 + 真实尺寸
	if err := host.ApplyBounds(Bounds{X: 100, Y: 50, W: 780, H: 614, Show: true}); err != nil {
		t.Fatalf("E2 ApplyBounds(show=true) 失败: %v", err)
	}
	var rc windows.Rect
	ret, _, callErr := procEmbed["GetWindowRect"].Call(host.HWND(), uintptr(unsafe.Pointer(&rc)))
	if ret == 0 {
		t.Fatalf("E2 GetWindowRect 失败 lasterr=%d", callErr)
	}
	w, h := int(rc.Right-rc.Left), int(rc.Bottom-rc.Top)
	if w < 700 || h < 500 {
		t.Fatalf("E2 摆位后 GetWindowRect=%dx%d（期望≈780x614），子窗没拿到真实尺寸", w, h)
	}
	t.Logf("E2 摆位后子窗实际矩形 %dx%d @ %d,%d", w, h, rc.Left, rc.Top)

	// E3：Debug 计数
	d := host.Debug()
	if d.ApplyCount < 2 {
		t.Fatalf("E3 ApplyCount=%d（期望>=2：隐藏一次+显示一次），摆位没真的执行", d.ApplyCount)
	}
	if d.ChildWidth < 700 || d.ChildHeight < 500 {
		t.Fatalf("E3 Debug 读回尺寸 %dx%d，与 GetWindowRect 不一致", d.ChildWidth, d.ChildHeight)
	}
	t.Logf("E3 ApplyCount=%d，Debug 读回 %dx%d 可见=%v", d.ApplyCount, d.ChildWidth, d.ChildHeight, d.ChildVisible)
	fmt.Println("E-ALL-PASS")
}
