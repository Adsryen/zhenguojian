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
	t.Logf("L1 子窗已建 XID=0x%x 父=0x%x", host.HWND(), parent)

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
