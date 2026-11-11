//go:build windows

package tray

// tray_windows.go：Win32 Shell_NotifyIconW 自建托盘（不经 wails 内部 API——
// v2.10.1 未导出 Options.Tray，pkg/internal/.../windows 全私有，唯一稳定路径
// 就是裸 Win32；LazyProc 模式照抄 internal/mpv/embed_windows.go，CGO_ENABLED=0 可编译）。
//
// 结构：
//   - 独立 OS 线程：注册窗口类 → 建 HWND_MESSAGE 消息窗（消息窗不出现在
//     EnumWindows，天然不污染 mpv 宿主窗打分逻辑，无需其复用/改动）→
//     Shell_NotifyIconW(NIM_ADD) → GetMessageW 泵 → 退出时清理。
//   - 回调 → 动作：WndProc 只做 h.dispatch(cmd)（非阻塞入队），动作在
//     tray.go 的分发 goroutine 里逐条新起协程执行，消息线程永不被网络检查卡住。
//   - 图标：LoadImageW 从本 exe 资源 RT_GROUP_ICON id=3（build/windows/icon.ico
//     经链接器 .syso 嵌入）按 SM_CXSMICON 小图标尺寸加载私有副本（退出 DestroyIcon），
//     失败回落 LoadIconW(id=3) → IDI_APPLICATION。
//   - Explorer 重启（TaskbarCreated 广播）自动重挂图标。

import (
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	trayWndClass = "zhenguojian-tray"
	// exeIconResID：exe 资源里 RT_GROUP_ICON 的 id（genres 工具链产出，实测 id=3）。
	// MAKEINTRESOURCE 语义：LPWSTR 参数直接传小整数，不经 unsafe 转换（vet unsafeptr 友好）。
	exeIconResID = uintptr(3)

	trayCallbackMsg = 0x0401 // WM_USER(0x0400)+1，托盘回调消息

	winDestroy     = 0x0002
	winClose       = 0x0010
	winNull        = 0x0000
	winCommand     = 0x0111
	winLButtonUp   = 0x0202
	winLButtonDbl  = 0x0203 // 左键双击 = 显示主窗（规格指定）
	winRButtonUp   = 0x0205 // 右键 = TrackPopupMenu
	winContextMenu = 0x007B // 菜单键/触摸长按，与右键同路径
	winNCCreate    = 0x0081

	nimAdd     = 0x0
	nimDelete  = 0x2
	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4

	imageIcon      = 1
	smCxSmIcon     = 76 // 小图标（托盘/标题栏）宽，DPI 自适应由系统度量给出
	smCySmIcon     = 77
	idiApplication = 32512

	mfString       = 0x0
	tpmLeftAlign   = 0x0
	tpmBottomALIGN = 0x20 // 贴屏幕底部时菜单向上弹

	mbOk              = 0x0
	mbIconInformation = 0x40
)

var (
	user32Tray = windows.NewLazySystemDLL("user32.dll")
	shell32Tr  = windows.NewLazySystemDLL("shell32.dll")

	trayProc = map[string]*windows.LazyProc{}
)

func init() {
	for _, n := range []string{
		"RegisterClassExW", "CreateWindowExW", "DefWindowProcW", "DestroyWindow",
		"PostQuitMessage", "PostMessageW", "GetMessageW", "TranslateMessage",
		"DispatchMessageW", "LoadIconW", "LoadImageW", "GetSystemMetrics",
		"DestroyIcon", "CreatePopupMenu", "AppendMenuW", "TrackPopupMenu",
		"DestroyMenu", "GetCursorPos", "SetForegroundWindow",
		"RegisterWindowMessageW", "MessageBoxW",
	} {
		trayProc[n] = user32Tray.NewProc(n)
	}
	trayProc["Shell_NotifyIconW"] = shell32Tr.NewProc("Shell_NotifyIconW")
}

// notifyIconDataV1：NOTIFYICONDATAW 的 V1 前缀（到 szTip 为止，64 位下 168 字节）。
// cbSize 只声明到 V1：老 shell（含 Windows Server 2003 时代的 v0/v1 API）读到
// 合法尺寸即停，多余字段不写不读，最大化兼容面。
type notifyIconDataV1 struct {
	CbSize          uint32
	Hwnd            windows.HWND
	Uid             uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            windows.Handle
	Tip             [64]uint16
}

// wndclassexwTray：x/sys/windows 未导出 WNDCLASSEXW，同 embed_windows.go 手写布局。
type wndclassexwTray struct {
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
	HIconSm       windows.Handle
}

// winPoint：x/sys v0.39.0 未导出 POINT，用同布局（两个 int32）本地结构接
// GetCursorPos / MSG（沿用 internal/mpv/embed_windows.go hitBandsRequest 的先例）。
type winPoint struct {
	X, Y int32
}

type trayMsg struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Pt      winPoint
	Time    uint32
}

// winBackend：单实例后端；globals 供 NewCallback（禁闭包）回读。
type winBackend struct {
	h          *Handler
	hwnd       windows.HWND
	hIcon      windows.Handle
	iconPriv   bool // LoadImageW 私有副本才 DestroyIcon；IDI_APPLICATION 等共享柄禁毁
	msgTaskbar uint32
	ready      chan error
	done       chan struct{}
}

var (
	traySrvPtr  atomic.Pointer[winBackend] // WndProc 回读后端（Explorer 重启重挂图标用）
	trayHandPtr atomic.Pointer[Handler]
)

func newBackend() backend { return &winBackend{} }

// Register 启动消息线程并等待注册结果（成功=NIM_ADD 已生效）。
func (b *winBackend) Register(h *Handler) error {
	b.h = h
	b.ready = make(chan error, 1)
	b.done = make(chan struct{})
	traySrvPtr.Store(b)
	trayHandPtr.Store(h)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(b.done)
		defer traySrvPtr.Store(nil)
		b.threadMain()
	}()
	err := <-b.ready
	if err != nil {
		<-b.done // 线程已自行清理
		return err
	}
	return nil
}

// Unregister 删除图标并关消息窗，等泵线程收尾（最多 2s）。
func (b *winBackend) Unregister() error {
	b.deleteIcon()
	if b.hwnd != 0 {
		trayProc["PostMessageW"].Call(uintptr(b.hwnd), winClose, 0, 0)
	}
	select {
	case <-b.done:
	case <-time.After(2 * time.Second):
		return errors.New("tray: 消息线程 2s 未退出")
	}
	return nil
}

func (b *winBackend) threadMain() {
	cls, err := windows.UTF16PtrFromString(trayWndClass)
	if err != nil {
		b.ready <- err
		return
	}
	var wc wndclassexwTray
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	wc.LpfnWndProc = windows.NewCallback(trayWndProc)
	wc.HInstance = windows.CurrentProcess()
	wc.LpszClassName = cls
	if ret, _, e := trayProc["RegisterClassExW"].Call(uintptr(unsafe.Pointer(&wc))); ret == 0 && e != windows.ERROR_CLASS_ALREADY_EXISTS {
		b.ready <- fmt.Errorf("tray: RegisterClassExW(%s) 失败: %w", trayWndClass, e)
		return
	}
	// 隐藏顶层窗（parent=NULL、无 WS_VISIBLE、0×0）：托盘消息宿主。
	// 不用 HWND_MESSAGE 消息窗——消息窗永远无法 SetForegroundWindow，右键菜单会
	// 复现「点外部不收起」经典缺陷；隐藏顶层窗是 MSDN shell-notify 示例与 systray
	// 系同款做法。不可见窗不进任务栏/Alt-Tab；EnumWindows 虽枚举到，但 mpv 宿主
	// 发现无害：embed.go:88 PickHostWindow 对候选硬性过滤 `!c.Visible`。
	b.hwnd = windows.HWND(0)
	ret, _, ce := trayProc["CreateWindowExW"].Call(
		0, uintptr(unsafe.Pointer(cls)), 0, 0,
		0, 0, 0, 0,
		0, // parent = NULL → 顶层窗
		0, uintptr(windows.CurrentProcess()), 0)
	if ret == 0 {
		b.ready <- fmt.Errorf("tray: CreateWindowExW(%s) 失败: %w", trayWndClass, ce)
		return
	}
	b.hwnd = windows.HWND(ret)
	regMsg, _, _ := trayProc["RegisterWindowMessageW"].Call(uintptr(unsafe.Pointer(mustUTF16("TaskbarCreated"))))
	b.msgTaskbar = uint32(regMsg)
	b.hIcon, b.iconPriv = loadTrayIcon()
	if err := b.addIcon(); err != nil {
		trayProc["DestroyWindow"].Call(uintptr(b.hwnd))
		b.hwnd = 0
		b.destroyIconIfPrivate()
		b.ready <- err
		return
	}
	b.ready <- nil

	var m trayMsg
	for {
		ret, _, _ := trayProc["GetMessageW"].Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int64(ret) <= 0 { // 0=WM_QUIT；-1=错误（hWnd 传 0 时仅参数非法才发生）
			break
		}
		trayProc["TranslateMessage"].Call(uintptr(unsafe.Pointer(&m)))
		trayProc["DispatchMessageW"].Call(uintptr(unsafe.Pointer(&m)))
	}
	// 收尾：正常路径 Unregister 已删图标；进程异常退出兜底再删一次并清私有柄。
	b.deleteIcon()
	b.destroyIconIfPrivate()
	b.hwnd = 0
}

// addIcon NIM_ADD（幂等：Explorer 重启后重挂）。
func (b *winBackend) addIcon() error {
	n := notifyIconDataV1{}
	n.CbSize = uint32(unsafe.Sizeof(n))
	n.Hwnd = b.hwnd
	n.Uid = 1
	n.Flags = nifMessage | nifIcon | nifTip
	n.CallbackMessage = trayCallbackMsg
	n.Icon = b.hIcon
	copyUTF16Into(n.Tip[:], AppName)
	ret, _, err := trayProc["Shell_NotifyIconW"].Call(nimAdd, uintptr(unsafe.Pointer(&n)))
	if ret == 0 {
		return fmt.Errorf("tray: Shell_NotifyIconW(NIM_ADD) 失败: %w", err)
	}
	return nil
}

func (b *winBackend) deleteIcon() {
	if b.hwnd == 0 {
		return
	}
	n := notifyIconDataV1{}
	n.CbSize = uint32(unsafe.Sizeof(n))
	n.Hwnd = b.hwnd
	n.Uid = 1
	trayProc["Shell_NotifyIconW"].Call(nimDelete, uintptr(unsafe.Pointer(&n)))
}

func (b *winBackend) destroyIconIfPrivate() {
	if b.hIcon != 0 && b.iconPriv {
		trayProc["DestroyIcon"].Call(uintptr(b.hIcon))
	}
	b.hIcon = 0
	b.iconPriv = false
}

// loadTrayIcon 三级回落：exe 资源 id=3 小尺寸私有副本 → exe 资源默认尺寸 → 系统 IDI_APPLICATION。
func loadTrayIcon() (windows.Handle, bool) {
	hInst := windows.CurrentProcess()
	cx, _, _ := trayProc["GetSystemMetrics"].Call(smCxSmIcon)
	cy, _, _ := trayProc["GetSystemMetrics"].Call(smCySmIcon)
	if cx == 0 {
		cx = 16
	}
	if cy == 0 {
		cy = 16
	}
	// flags=0 → 私有副本，退出时 DestroyIcon。
	ret, _, _ := trayProc["LoadImageW"].Call(uintptr(hInst), exeIconResID, imageIcon, cx, cy, 0)
	if ret != 0 {
		return windows.Handle(ret), true
	}
	ret, _, _ = trayProc["LoadIconW"].Call(uintptr(hInst), exeIconResID)
	if ret != 0 {
		return windows.Handle(ret), false // LoadIconW 返回共享柄，禁 DestroyIcon
	}
	ret, _, _ = trayProc["LoadIconW"].Call(0, idiApplication)
	return windows.Handle(ret), false
}

// trayWndProc 消息窗过程（NewCallback 顶层函数；经包级指针回读 Handler/后端）。
func trayWndProc(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	h := trayHandPtr.Load()
	switch msg {
	case trayCallbackMsg:
		if h == nil {
			return 0
		}
		switch uint32(lParam & 0xFFFF) {
		case winLButtonDbl:
			h.dispatch(CmdShow) // 左键双击 = 显示主窗
		case winRButtonUp, winContextMenu:
			showTrayMenu(hwnd)
		}
		return 0
	case winCommand:
		if h != nil {
			h.dispatch(int(wParam & 0xFFFF)) // 菜单 ID 即命令编号
		}
		return 0
	case winNull:
		return 0
	case winDestroy:
		trayProc["PostQuitMessage"].Call(0)
		return 0
	}
	if b := traySrvPtr.Load(); b != nil && b.msgTaskbar != 0 && msg == b.msgTaskbar {
		_ = b.addIcon() // Explorer 重启：重挂图标
		return 0
	}
	ret, _, _ := trayProc["DefWindowProcW"].Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

// showTrayMenu 右键/菜单键弹三键菜单。经典顺序：GetCursorPos →
// SetForegroundWindow(hwnd)（否则点菜单外区域不消失， Raymond Chen 补丁）→
// TrackPopupMenu（命令经 WM_COMMAND 分发）→ PostMessage(WM_NULL) 强制重绘 →
// DestroyMenu。owner 为隐藏顶层窗（见 threadMain 注释）：SetForegroundWindow
// 生效，点菜单外区域自动收起；若退回消息窗方案此补丁无效（复现不收起缺陷）。
func showTrayMenu(hwnd windows.HWND) {
	menu, _, _ := trayProc["CreatePopupMenu"].Call()
	if menu == 0 {
		return
	}
	add := func(id uintptr, label string) {
		trayProc["AppendMenuW"].Call(menu, mfString, id, uintptr(unsafe.Pointer(mustUTF16(label))))
	}
	add(CmdShow, MenuLabelShow)
	add(CmdCheck, MenuLabelCheck)
	add(CmdQuit, MenuLabelQuit)

	var pt winPoint
	trayProc["GetCursorPos"].Call(uintptr(unsafe.Pointer(&pt)))
	trayProc["SetForegroundWindow"].Call(uintptr(hwnd))
	trayProc["TrackPopupMenu"].Call(menu, tpmLeftAlign|tpmBottomALIGN, uintptr(pt.X), uintptr(pt.Y), 0, uintptr(hwnd), 0)
	trayProc["PostMessageW"].Call(uintptr(hwnd), winNull, 0, 0)
	trayProc["DestroyMenu"].Call(menu)
}

// ShowInfoBox 原生信息框（user32 MessageBoxW，MB_OK|MB_ICONINFORMATION）。
// 阻塞至用户点确定——只在托盘“检查更新”派发的独立 goroutine 里调用，
// 不占托盘消息线程，也不占主窗口 UI 线程。owner=NULL：框独立置顶于前台。
func ShowInfoBox(title, text string) error {
	if title == "" {
		title = AppName
	}
	ret, _, err := trayProc["MessageBoxW"].Call(
		0,
		uintptr(unsafe.Pointer(mustUTF16(text))),
		uintptr(unsafe.Pointer(mustUTF16(title))),
		mbOk|mbIconInformation,
	)
	if ret == 0 { // IDCANCEL=2 才是取消；0=MessageBoxW 调用失败
		return fmt.Errorf("tray: MessageBoxW 失败: %w", err)
	}
	return nil
}

func mustUTF16(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		p, _ = windows.UTF16PtrFromString(trayWndClass) // 含 NUL 等病态输入兜底，不该发生
	}
	return p
}

func copyUTF16Into(dst []uint16, s string) {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return
	}
	copy(dst, u)
}
