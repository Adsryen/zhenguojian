//go:build linux

package mpv

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/xproto"
)

// 本文件的目的：让**真正的 Linux 内嵌代码**（embed_linux.go）在受控环境里被
// 真实执行一次（Xvfb 下真连 X server、真建窗、真摆位、真读回），而不是只被编译。
// 与 embed_runtime_windows_test.go 对偶：那边验 Win32 链，这边验 X11 链。
//
// 需要 DISPLAY 指向活着的 X server（本机 Xvfb :99）；没有就跳过，
// 不装成通过。

// createTestTopLevelX 建一个顶层测试窗并打上 _NET_WM_PID=本进程，
// 让 findHostWindowX 能把它认作宿主（模拟 Wails 主窗）。
func createTestTopLevelX(t *testing.T) (c *xgb.Conn, w xproto.Window) {
	t.Helper()
	c, err := xgb.NewConn()
	if err != nil {
		t.Skipf("无 X 显示可连（%v）：X11 运行时测试跳过", err)
	}
	info := xproto.Setup(c)
	screen := info.DefaultScreen(c)

	w, err = xproto.NewWindowId(c)
	if err != nil {
		c.Close()
		t.Fatalf("NewWindowId: %v", err)
	}
	cookie := xproto.CreateWindowChecked(c, screen.RootDepth, w, screen.Root,
		10, 10, 800, 600, 0,
		xproto.WindowClassInputOutput, screen.RootVisual,
		0, []uint32{})
	if e := cookie.Check(); e != nil {
		c.Close()
		t.Fatalf("CreateWindow(顶层测试窗): %v", e)
	}
	// _NET_WM_PID = 本进程 pid（CARDINAL，format 32）。
	atom, err := xproto.InternAtom(c, false, uint16(len("_NET_WM_PID")), "_NET_WM_PID").Reply()
	if err != nil {
		c.Close()
		t.Fatalf("InternAtom: %v", err)
	}
	pidBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(pidBuf, uint32(os.Getpid()))
	if e := xproto.ChangePropertyChecked(c, xproto.PropModeReplace, w, atom.Atom, xproto.AtomCardinal, 32, 1, pidBuf).Check(); e != nil {
		c.Close()
		t.Fatalf("ChangeProperty(_NET_WM_PID): %v", e)
	}
	xproto.MapWindow(c, w)
	_, err = xproto.GetInputFocus(c).Reply() // 冲刷：确保上面的请求都已落到 X server
	if err != nil {
		c.Close()
		t.Fatalf("flush: %v", err)
	}
	return c, w
}

// createDecoyTopLevelX 建一个 10×10 **未 map** 的同 pid 顶层窗——复刻 Wails 真机
// 上那个带本进程 pid 的隐形辅助窗（Xvfb 探针实锤 0x200001 10x10 IsUnMapped）。
// 用来钉死「findHostWindowX 只按 pid 取第一个 → 选中隐形窗」这个回归：
// 修好的发现链必须跳过它、选中已 map 的主窗。
func createDecoyTopLevelX(t *testing.T) *xgb.Conn {
	t.Helper()
	c, err := xgb.NewConn()
	if err != nil {
		t.Skipf("无 X 显示可连（%v）：X11 运行时测试跳过", err)
	}
	info := xproto.Setup(c)
	screen := info.DefaultScreen(c)
	w, err := xproto.NewWindowId(c)
	if err != nil {
		c.Close()
		t.Fatalf("NewWindowId(诱饵): %v", err)
	}
	// 10×10，建了但**不** MapWindow（保持 Unmapped，正是真机辅助窗的状态）。
	cookie := xproto.CreateWindowChecked(c, screen.RootDepth, w, screen.Root,
		5, 5, 10, 10, 0,
		xproto.WindowClassInputOutput, screen.RootVisual,
		0, []uint32{})
	if e := cookie.Check(); e != nil {
		c.Close()
		t.Fatalf("CreateWindow(诱饵): %v", e)
	}
	atom, err := xproto.InternAtom(c, false, uint16(len("_NET_WM_PID")), "_NET_WM_PID").Reply()
	if err != nil {
		c.Close()
		t.Fatalf("InternAtom(诱饵): %v", err)
	}
	pidBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(pidBuf, uint32(os.Getpid()))
	if e := xproto.ChangePropertyChecked(c, xproto.PropModeReplace, w, atom.Atom, xproto.AtomCardinal, 32, 1, pidBuf).Check(); e != nil {
		c.Close()
		t.Fatalf("ChangeProperty(诱饵 _NET_WM_PID): %v", e)
	}
	_, err = xproto.GetInputFocus(c).Reply() // 冲刷
	if err != nil {
		c.Close()
		t.Fatalf("flush(诱饵): %v", err)
	}
	return c
}

// TestEmbedHostRuntimeUnderXvfb 跑真实的 NewEmbedHost → ApplyBounds → Debug 链。
// 判据与 Windows 版 E1-E4 对偶：
//
//	L1 NewEmbedHost 成功且 HWND 非 0（子窗真的建出来了）
//	L2 ApplyBounds(show=false, 780x614) 后 GetGeometry 读回 780x614（隐藏态不抹几何）
//	L3 ApplyBounds(show=true) 后 MapState=IsViewable（真的 map 上去了）
//	L4 Debug().ApplyCount >= 2、ChildWidth/Height 一致
//	L5 Close 后子窗真的没了（GetWindowAttributes 报 BadWindow）
func TestEmbedHostRuntimeUnderXvfb(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("无 DISPLAY：X11 运行时测试跳过（CI 里由 Xvfb 步骤提供）")
	}
	// 先建诱饵（未 map 的同 pid 10×10 窗），模拟 Wails 真机的隐形辅助窗；
	// 再建真正的主窗。发现链若只按 pid 取第一个，就会选中诱饵——
	// 下面的 L1b 断言专门钉这件事。
	decoyConn := createDecoyTopLevelX(t)
	defer decoyConn.Close()
	parentConn, parent := createTestTopLevelX(t)
	defer func() {
		xproto.DestroyWindow(parentConn, parent)
		parentConn.Close()
	}()

	host, err := NewEmbedHost()
	if err != nil {
		t.Fatalf("L1 NewEmbedHost 失败: %v", err)
	}
	if host.HWND() == 0 {
		t.Fatalf("L1 子窗 XID 为 0，未真正创建")
	}
	// L1b：父窗必须选已 map 的主窗，不能选未 map 的 10×10 诱饵。
	if host.parent != parent {
		t.Fatalf("L1b 宿主选错：选中 0x%x（期望主窗 0x%x）——只按 pid 取第一个会选中隐形辅助窗",
			host.parent, parent)
	}
	t.Logf("L1 子窗已建 XID=0x%x 父=0x%x（诱饵已排除）", host.HWND(), parent)

	// L2：隐藏 + 真实尺寸（对应前端起手 show=false 的那次上报）
	if err := host.ApplyBounds(Bounds{X: 100, Y: 50, W: 780, H: 614, Show: false}); err != nil {
		t.Fatalf("L2 ApplyBounds(show=false) 失败: %v", err)
	}
	g, err := xproto.GetGeometry(host.conn, xproto.Drawable(host.child)).Reply()
	if err != nil {
		t.Fatalf("L2 GetGeometry: %v", err)
	}
	if int(g.Width) != 780 || int(g.Height) != 614 {
		t.Fatalf("L2 隐藏态子窗几何 %dx%d（期望 780x614），隐藏把几何抹掉了", g.Width, g.Height)
	}
	attrs, err := xproto.GetWindowAttributes(host.conn, host.child).Reply()
	if err != nil {
		t.Fatalf("L2 GetWindowAttributes: %v", err)
	}
	if attrs.MapState == xproto.MapStateViewable {
		t.Fatalf("L2 show=false 时子窗却是 IsViewable，显隐没分开")
	}
	t.Logf("L2 隐藏态几何 %dx%d @ %d,%d（相对父窗）MapState=%d", g.Width, g.Height, g.X, g.Y, attrs.MapState)

	// L3：显示
	if err := host.ApplyBounds(Bounds{X: 100, Y: 50, W: 780, H: 614, Show: true}); err != nil {
		t.Fatalf("L3 ApplyBounds(show=true) 失败: %v", err)
	}
	attrs, err = xproto.GetWindowAttributes(host.conn, host.child).Reply()
	if err != nil {
		t.Fatalf("L3 GetWindowAttributes: %v", err)
	}
	if attrs.MapState != xproto.MapStateViewable {
		t.Fatalf("L3 show=true 后 MapState=%d（期望 IsViewable=2），子窗没 map 上去", attrs.MapState)
	}
	t.Logf("L3 子窗已 IsViewable")

	// L4：Debug 一致性
	d := host.Debug()
	if d.ApplyCount < 2 {
		t.Fatalf("L4 ApplyCount=%d（期望>=2），摆位没真的执行", d.ApplyCount)
	}
	if d.ChildWidth != 780 || d.ChildHeight != 614 {
		t.Fatalf("L4 Debug 读回 %dx%d（期望 780x614）", d.ChildWidth, d.ChildHeight)
	}
	if !d.ChildVisible {
		t.Fatalf("L4 Debug.ChildVisible=false，与 MapState 矛盾")
	}
	t.Logf("L4 ApplyCount=%d 屏幕坐标=%d,%d %dx%d 可见=%v 父窗类=%q",
		d.ApplyCount, d.ChildLeft, d.ChildTop, d.ChildWidth, d.ChildHeight, d.ChildVisible, d.ParentClass)

	// L5：Close 真销毁
	if err := host.Close(); err != nil {
		t.Fatalf("L5 Close: %v", err)
	}
	if _, err := xproto.GetWindowAttributes(parentConn, host.child).Reply(); err == nil {
		t.Fatalf("L5 Close 后子窗仍存在（GetWindowAttributes 没报错），没真销毁")
	}
	t.Logf("L5 子窗已销毁")
	t.Log("L-ALL-PASS")
}

// TestFindHostWindowXUnderWmReparent 钉死「有 WM 时宿主发现失效」这个回归。
// 真机断点（93 轮下半，xfwm4 + Xvfb 实测）：WM 把顶层窗 reparent 进自己的
// frame 后，QueryTree(root) 只能看到 frame，看不到我们的客户端窗 → 发现链
// 返回 0 → mpv 没拿到 --wid、退化成独立窗（root 下 640x360 mpv 窗）。
// 修法（findHostWindowX）：候选窗 = EWMH _NET_CLIENT_LIST ∪ QueryTree(root)，
// 再按 _NET_WM_PID==本进程 且 IsViewable 筛。本测试手工复刻 WM 的两个动作：
//
//	① 建一个「frame」顶层窗，把带 pid 的主窗 ReparentWindow 进去；
//	② 在 root 上写 _NET_CLIENT_LIST=[主窗]（WM 维护的客户端列表）。
//
// 断言：发现链仍能选中主窗；子窗建在主窗下（frame 下不行）。
func TestFindHostWindowXUnderWmReparent(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("无 DISPLAY：X11 运行时测试跳过（CI 里由 Xvfb 步骤提供）")
	}
	parentConn, client := createTestTopLevelX(t)
	defer func() {
		xproto.DestroyWindow(parentConn, client)
		parentConn.Close()
	}()
	// ① 建「WM frame」：一个不带本 pid 的普通顶层窗（模拟 xfwm4 的 frame）。
	frame, err := xproto.NewWindowId(parentConn)
	if err != nil {
		t.Fatalf("NewWindowId(frame): %v", err)
	}
	info := xproto.Setup(parentConn)
	screen := info.DefaultScreen(parentConn)
	cookie := xproto.CreateWindowChecked(parentConn, screen.RootDepth, frame, screen.Root,
		20, 20, 900, 700, 0,
		xproto.WindowClassInputOutput, screen.RootVisual, 0, []uint32{})
	if e := cookie.Check(); e != nil {
		t.Fatalf("CreateWindow(frame): %v", e)
	}
	xproto.MapWindow(parentConn, frame)
	// ② 把主窗 reparent 进 frame（xfwm4 的真实动作），坐标 0,0 无所谓。
	if e := xproto.ReparentWindowChecked(parentConn, client, frame, 0, 0).Check(); e != nil {
		t.Fatalf("ReparentWindow(主窗→frame): %v", e)
	}
	// ③ root 上写 _NET_CLIENT_LIST=[主窗]（WM 维护的客户端窗列表）。
	listAtom, err := xproto.InternAtom(parentConn, false, uint16(len("_NET_CLIENT_LIST")), "_NET_CLIENT_LIST").Reply()
	if err != nil {
		t.Fatalf("InternAtom(_NET_CLIENT_LIST): %v", err)
	}
	listBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(listBuf, uint32(client))
	if e := xproto.ChangePropertyChecked(parentConn, xproto.PropModeReplace, screen.Root,
		listAtom.Atom, xproto.AtomWindow, 32, 1, listBuf).Check(); e != nil {
		t.Fatalf("ChangeProperty(_NET_CLIENT_LIST): %v", e)
	}
	_, err = xproto.GetInputFocus(parentConn).Reply() // 冲刷
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	// 此刻 QueryTree(root) 里**没有** client（它挂在 frame 下）——
	// 先断言这个前提成立，否则测的不是我想测的形态。
	tree, err := xproto.QueryTree(parentConn, screen.Root).Reply()
	if err != nil {
		t.Fatalf("QueryTree: %v", err)
	}
	inRoot := false
	for _, w := range tree.Children {
		if w == client {
			inRoot = true
		}
	}
	if inRoot {
		t.Fatalf("测试前提不成立：reparent 后主窗仍在 root 直接子窗里")
	}
	// 断言：发现链仍选中主窗。
	if got := findHostWindowX(parentConn, screen.Root); got != client {
		t.Fatalf("WM reparent 后宿主发现失效：选中 0x%x（期望 0x%x）——"+
			"必须读 _NET_CLIENT_LIST 才能在 frame 下找到客户端窗", got, client)
	}
	t.Logf("WM reparent 后宿主发现正常：选中主窗 0x%x（frame=0x%x 下）", client, frame)
	// 再走一遍完整生产路径：子窗必须建在主窗下，不是 frame 下。
	host, err := NewEmbedHost()
	if err != nil {
		t.Fatalf("NewEmbedHost（WM 形态）失败: %v", err)
	}
	defer host.Close()
	if host.parent != client {
		t.Fatalf("WM 形态下子窗建错父：建在 0x%x 下（期望主窗 0x%x）", host.parent, client)
	}
	t.Logf("WM 形态下子窗已建在主窗下 XID=0x%x", host.HWND())
	// 收尾：还原 reparent（frame 销毁会带走 client，但顺序上不依赖它）。
	xproto.ReparentWindow(parentConn, client, screen.Root, 10, 10)
	xproto.DestroyWindow(parentConn, frame)
}
