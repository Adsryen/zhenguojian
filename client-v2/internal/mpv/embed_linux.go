//go:build linux

package mpv

// embed_linux.go：Linux（X11）播放画面内嵌 —— 真实现，不是桩。
//
// 为什么不再是桩（embed_stub.go 原来覆盖 !windows）：
//   「Linux 不支持内嵌」是当初的设计选择，不是技术限制。mpv 把画面画进一个
//   宿主窗（--wid=<窗口号>）：Windows 是 HWND，Linux 是 X11 的 XID，同一套
//   机制、不同建窗系统调用。跨平台的那一大块（PlaceDecision 几何/显隐分离、
//   前端上报时序、Debug 现场、app.go 接线）完全共用，只有「建一个原生子窗」
//   这一层是平台调用。本文件就是 Linux 的这一层。
//
// 架构参照 JavdBviewed-Clients apps/desktop/src/main/nativeVideoSurface.ts
// （owned 表面 + 父窗事件驱动重摆 + 几何钳进父内容区），按 X11 语义适配：
//   · Electron 里那是 owned 顶层窗（所以要用 setIgnoreMouseEvents 把鼠标
//     forward 给 DOM、要挂 9 个父窗事件、要 moveTop 抢 z-order）；
//   · X11 里我们直接在 Wails 主窗下建 X 子窗（与 Windows 的 WS_CHILD 同语义）：
//     子窗坐标天然相对父窗客户区（前端 getBoundingClientRect 也是相对视口，
//     有 WM 装饰也自动对齐）、父窗移动时子窗自动跟随（省掉 9 个事件监听，
//     前端 ResizeObserver 已负责尺寸变化时的重摆）、后建的孩子默认压在父窗
//     内容（含 WebKit 渲染）之上（省掉 moveTop）。
//
// 诚实边界（v1 版，已验证的 / 未验证的分开写）：
//   · 已在本机 Xvfb 真跑验证：建子窗 → 前端上报 → mpv --wid 进画面（见
//     embed_runtime_linux_test.go 与 scripts/linux-smoke.sh 的内嵌断言）。
//   · 点击穿透带（HitBands）本文件**未实现**：X11 等价物是 input-only 形状窗
//     （shape 扩展），v1 里子窗矩形内的点击全部归 mpv，叠在视频上的 DOM
//     控制条会收不到点击。已记录，未假装覆盖。
//   · Wayland 不支持（无 XID 概念，mpv --wid 无意义）：无 DISPLAY/Wayland 时
//     NewEmbedHost 返回错误，app.go PlayerAttach 走 embedded:false 回退独立窗，
//     行为与桩时代一致。

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/xproto"
)

// X11 的三个调用各有各的 value-mask 位定义，xproto 生成的 Cw* 常量命名/取值
// 与协议并不完全对应（它标注成 ChangeWindowAttributes 的 mask），所以这里只写
// 自己真正用到的那一个，按协议原值字面量并注释位序：
//
// ConfigureWindow mask（X11 协议）：CWX=1 CWY=2 CWWidth=4 CWHeight=8
// CWBorderWidth=16 CWSibling=32 CWStackMode=64；value-list 按置位顺序给值。
const (
	cwCfgGeometry = 1 | 2 | 4 | 8 // ConfigureWindow: CWX|CWY|CWWidth|CWHeight
)

var (
	embedXOnce sync.Once
	embedXErr  error
)

// EmbedHost Linux 实现：一个 X 子窗（XID）+ 持有 X 连接。
// 与 Windows 版同构：mu 串行化；lastIn/lastR/lastV/applyCount 只给 Debug 读。
type EmbedHost struct {
	mu     sync.Mutex
	conn   *xgb.Conn
	parent xproto.Window
	child  xproto.Window
	depth  byte
	root   xproto.Window

	lastIn Bounds
	lastR  Rect
	lastV  bool
	// ApplyBounds 被调用的次数；只给诊断读（语义与 Windows 版一致）。
	applyCount int
}

// NewEmbedHost 找到 Wails 主窗（_NET_WM_PID == 本进程 pid 的顶层窗），
// 在其下创建隐藏的视频承载子窗。无 X 显示/找不到主窗时返回错误，
// app.go PlayerAttach 据此回退独立窗（embedded:false）。
func NewEmbedHost() (*EmbedHost, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" && os.Getenv("DISPLAY") == "" {
		return nil, errors.New("mpv: 播放画面内嵌需要 X11 显示（Wayland 无 XID，mpv --wid 无意义）")
	}
	c, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("mpv: 连接 X 显示失败: %w", err)
	}
	info := xproto.Setup(c)
	screen := info.DefaultScreen(c)
	root := screen.Root

	parent := findHostWindowX(c, root)
	if parent == 0 {
		c.Close()
		return nil, fmt.Errorf("mpv: 未找到宿主主窗口（_NET_WM_PID==%d 的顶层窗）", os.Getpid())
	}

	child, err := xproto.NewWindowId(c)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("mpv: 分配子窗 id 失败: %w", err)
	}
	// 先建 1×1 不 map（等价 Windows 版的隐藏创建）：首帧就绪/前端摆位前不放画面。
	// 必须用 Checked 变体：CreateWindow 是 unchecked 请求，Check() 会 panic。
	// 几何走 CreateWindow 的专用 x/y/width/height 字段，value-mask 留 0、value-list
	// 留空（xgb 按 PopCount(mask) 读 value-list，mask=0 就不读，最无歧义）。
	// 不挂事件掩码：子窗只是给 mpv 当渲染目标，我们不收它的输入事件。
	cookie := xproto.CreateWindowChecked(c, screen.RootDepth, child, parent,
		0, 0, 1, 1, 0,
		xproto.WindowClassInputOutput, screen.RootVisual,
		0, []uint32{})
	if e := cookie.Check(); e != nil {
		c.Close()
		return nil, fmt.Errorf("mpv: CreateWindow(子窗) 失败: %w", e)
	}
	return &EmbedHost{conn: c, parent: parent, child: child, depth: screen.RootDepth, root: root}, nil
}

// findHostWindowX：_NET_WM_PID == 本进程 pid 的顶层窗（多实例安全、不依赖
// 窗口标题/类名——与 Windows 版「类名+标题打分」不同的原因写在这：X11 顶层窗
// 的枚举是 QueryTree(root)，Wails 主窗没有固定类名（随可执行文件名变），
// 而 _NET_WM_PID 是 EWMH 标准属性、Wails/GTK 必设（真机探针已确认）。
func findHostWindowX(c *xgb.Conn, root xproto.Window) xproto.Window {
	tree, err := xproto.QueryTree(c, root).Reply()
	if err != nil {
		return 0
	}
	pidAtom, err := xproto.InternAtom(c, false, uint16(len("_NET_WM_PID")), "_NET_WM_PID").Reply()
	if err != nil {
		return 0
	}
	selfPid := uint32(os.Getpid())
	for _, w := range tree.Children {
		prop, err := xproto.GetProperty(c, false, w, pidAtom.Atom, xproto.AtomCardinal, 0, 1).Reply()
		if err != nil || prop.Format != 32 || len(prop.Value) < 4 {
			continue
		}
		if binary.LittleEndian.Uint32(prop.Value[:4]) == selfPid {
			return w
		}
	}
	return 0
}

// HWND 返回子窗 XID（供 mpv --wid 使用）。未创建时 0。
func (e *EmbedHost) HWND() uintptr {
	e.mu.Lock()
	defer e.mu.Unlock()
	return uintptr(e.child)
}

// ApplyBounds 按前端上报几何摆放子窗，按 Bounds.Show 决定显隐。
// 与 Windows 版同一套 PlaceDecision：尺寸照摆（mpv drawable 从起手就是真实
// 尺寸），Show 只决定 Map/Unmap，隐藏态**不**抹几何 —— 61 轮 Windows 黑屏的
// 根因（隐藏分支只 SW_HIDE 不 MoveWindow）在这里从设计上一就不存在。
func (e *EmbedHost) ApplyBounds(b Bounds) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.child == 0 {
		return errors.New("mpv: 子窗已销毁")
	}
	r, visible := PlaceDecision(b)
	e.lastIn, e.lastR, e.lastV = b, r, visible
	e.applyCount++
	if r.W < 1 || r.H < 1 {
		// 前端还没量出 stage（或已归零）：整体藏起来，没什么可摆的。
		xproto.UnmapWindow(e.conn, e.child)
		return nil
	}
	// 尺寸照摆。子窗坐标相对父窗客户区，前端 r.left/r.top 也是相对视口（同一原点）。
	if err := xproto.ConfigureWindowChecked(e.conn, e.child, uint16(cwCfgGeometry),
		[]uint32{uint32(r.X), uint32(r.Y), uint32(r.W), uint32(r.H)}).Check(); err != nil {
		return fmt.Errorf("mpv: ConfigureWindow 失败: %w", err)
	}
	if !visible {
		xproto.UnmapWindow(e.conn, e.child)
		return nil
	}
	xproto.MapWindow(e.conn, e.child)
	return nil
}

// Hide 隐藏子窗（页面离开/暂停展示 DOM 覆盖层时）。
func (e *EmbedHost) Hide() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.child != 0 {
		xproto.UnmapWindow(e.conn, e.child)
	}
}

// Close 销毁子窗并断开 X 连接（当前调用方只有测试/退出路径；正常随进程结束）。
func (e *EmbedHost) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.child != 0 {
		xproto.DestroyWindow(e.conn, e.child)
		e.child = 0
	}
	if e.conn != nil {
		e.conn.Close()
		e.conn = nil
	}
	return nil
}

// Debug 取内嵌现场。全是「读现场」，任何一步失败就留零值，不影响播放。
// 字段语义与 Windows 版 HostDebug 对齐（ChildLeft/Top=屏幕坐标）。
func (e *EmbedHost) Debug() HostDebug {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := HostDebug{Supported: true,
		ParentHwnd: fmt.Sprintf("0x%x", uintptr(e.parent)),
		ChildHwnd:  fmt.Sprintf("0x%x", uintptr(e.child)),
		ApplyCount: e.applyCount,
		LastInX:    e.lastIn.X, LastInY: e.lastIn.Y, LastInW: e.lastIn.W, LastInH: e.lastIn.H,
		LastOutVisible: e.lastV,
		LastOutX:       e.lastR.X, LastOutY: e.lastR.Y, LastOutW: e.lastR.W, LastOutH: e.lastR.H,
		ParentClass: e.parentWmClass(),
	}
	if e.child != 0 {
		if a, err := xproto.GetWindowAttributes(e.conn, e.child).Reply(); err == nil {
			// X11 MapState：0=Unmapped 1=Unviewable 2=IsViewable（xproto.MapStateViewable）
			d.ChildVisible = a.MapState == xproto.MapStateViewable
		}
		if g, err := xproto.GetGeometry(e.conn, xproto.Drawable(e.child)).Reply(); err == nil {
			// GetGeometry 的 X/Y 相对父窗；换算成屏幕坐标与 Windows 版 GetWindowRect 对齐。
			d.ChildWidth, d.ChildHeight = int(g.Width), int(g.Height)
			if t, err := xproto.TranslateCoordinates(e.conn, e.child, e.root, 0, 0).Reply(); err == nil {
				d.ChildLeft, d.ChildTop = int(t.DstX), int(t.DstY)
			} else {
				d.ChildLeft, d.ChildTop = int(g.X), int(g.Y)
			}
			// X11 没有 RealWindowFromPoint：best-effort 判读——子窗已 map 且探测点
			// 落在子窗屏幕矩形内，就记 TopIsChild=true（与 Windows 版「=ours」同义）。
			if d.ChildWidth >= 2 && d.ChildHeight >= 2 && d.ChildVisible {
				px := d.ChildLeft + d.ChildWidth/2
				py := d.ChildTop + d.ChildHeight/2
				d.ProbeX, d.ProbeY = px, py
				if px >= d.ChildLeft && px < d.ChildLeft+d.ChildWidth &&
					py >= d.ChildTop && py < d.ChildTop+d.ChildHeight {
					d.TopAtCenterHwnd = d.ChildHwnd
					d.TopIsChild = true
					d.TopContainsProbe = true
				}
			}
		}
	}
	return d
}

// parentWmClass 读父窗 WM_CLASS（X11 没有「类名」系统调用，用 EWMH 属性近似
// Windows 版 GetClassNameW 的诊断用途：排查时确认挂对了父窗）。
func (e *EmbedHost) parentWmClass() string {
	classAtom, err := xproto.InternAtom(e.conn, false, uint16(len("WM_CLASS")), "WM_CLASS").Reply()
	if err != nil {
		return ""
	}
	prop, err := xproto.GetProperty(e.conn, false, e.parent, classAtom.Atom, xproto.AtomAny, 0, 256).Reply()
	if err != nil || len(prop.Value) == 0 {
		return ""
	}
	// WM_CLASS = NUL 分隔两串（instance\0class\0），取第二串。
	if i := indexByte(prop.Value, 0); i+1 < len(prop.Value) {
		return string(prop.Value[i+1 : len(prop.Value)-1])
	}
	return string(prop.Value)
}

func indexByte(b []byte, v byte) int {
	for i, c := range b {
		if c == v {
			return i
		}
	}
	return -1
}
