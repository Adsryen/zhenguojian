import io
import os

base = os.path.dirname(os.path.abspath(__file__))


def rw(rel, reps):
    p = os.path.join(base, rel)
    s = io.open(p, encoding='utf-8').read()
    for old, new in reps:
        c = s.count(old)
        assert c == 1, (rel, old[:60], c)
        s = s.replace(old, new)
    io.open(p, 'w', encoding='utf-8').write(s)
    print('OK', rel)


# ── 1) 共享结构：诊断字段（平台无关的形状，stub 也能填）──
rw('internal/mpv/embed.go', [
    ('''// Bounds 是前端上报的视频容器矩形（物理像素，相对主窗客户区）。''',
     '''// HostDebug 是内嵌链路的现场数据。
//
// 为什么要它：真机 alpha.28 报"音频有、画面全黑"，而我**没有任何手段
// 知道用户机器上的窗口层级长什么样** —— 类注册修好后这是第一次真跑，
// 剩下的可能原因（父窗选错、子窗尺寸、可见性、z-order、mpv 没画）
// 在本机一个都复现不了。继续猜就是在编故事，所以把现场取回来。
// 输出走 DevTools 控制台（Ctrl+Shift+F12），用户截一张图我就能定位。
type HostDebug struct {
	Supported    bool   `json:"supported"`
	ParentHwnd   string `json:"parentHwnd"`
	ParentClass  string `json:"parentClass"`
	ChildHwnd    string `json:"childHwnd"`
	ChildVisible bool   `json:"childVisible"`
	// 子窗在屏幕坐标下的矩形（物理像素）；宽或高为 1 就是"从没被真正摆过"
	ChildLeft   int `json:"childLeft"`
	ChildTop    int `json:"childTop"`
	ChildWidth  int `json:"childWidth"`
	ChildHeight int `json:"childHeight"`
	// 最近一次 ApplyBounds 收到的原始入参与换算结果
	LastInX, LastInY, LastInW, LastInH int  `json:"lastIn"`
	LastVisible                         bool `json:"lastOutVisible"`
	OutX, OutY, OutW, OutH              int  `json:"lastOut"`
}

// Bounds 是前端上报的视频容器矩形（物理像素，相对主窗客户区）。'''),
])

# ── 2) windows 侧：填现场 ──
rw('internal/mpv/embed_windows.go', [
    ('''		"ScreenToClient", "GetClientRect",''',
     '''		"ScreenToClient", "GetClientRect", "GetWindowRect",'''),
    ('''// EmbedHost 持有宿主主窗与视频子窗。并发入口在 app.go embedMu 串行化，
// 这里再带一把锁兜底。
type EmbedHost struct {
	mu     sync.Mutex
	parent windows.HWND
	child  windows.HWND
}''',
     '''// EmbedHost 持有宿主主窗与视频子窗。并发入口在 app.go embedMu 串行化，
// 这里再带一把锁兜底。
type EmbedHost struct {
	mu     sync.Mutex
	parent windows.HWND
	child  windows.HWND
	// 最近一次 ApplyBounds 的现场（诊断用）
	lastIn Bounds
	lastR  Rect
	lastV  bool
}'''),
    ('''// Hide 隐藏子窗（页面离开/暂停展示 DOM 覆盖层时用）。''',
     '''// Debug 取内嵌现场（句柄、类名、子窗矩形、可见性、最近一次换算）。
// 全部是"读现场"，失败就留零值，不影响播放。
func (e *EmbedHost) Debug() HostDebug {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := HostDebug{Supported: true,
		ParentHwnd: fmt.Sprintf("0x%x", uintptr(e.parent)),
		ChildHwnd:  fmt.Sprintf("0x%x", uintptr(e.child)),
		LastInX:    e.lastIn.X, LastInY: e.lastIn.Y, LastInW: e.lastIn.W, LastInH: e.lastIn.H,
		LastVisible: e.lastV,
		OutX:       e.lastR.X, OutY: e.lastR.Y, OutW: e.lastR.W, OutH: e.lastR.H}
	if e.parent != 0 {
		d.ParentClass = winString(procEmbed["GetClassNameW"], uintptr(e.parent))
	}
	if e.child != 0 {
		vis, _, _ := procEmbed["IsWindowVisible"].Call(uintptr(e.child))
		d.ChildVisible = vis != 0
		var rc rectWin
		if ret, _, _ := procEmbed["GetWindowRect"].Call(uintptr(e.child), uintptr(unsafe.Pointer(&rc))); ret != 0 {
			d.ChildLeft, d.ChildTop = int(rc.Left), int(rc.Top)
			d.ChildWidth, d.ChildHeight = int(rc.Right-rc.Left), int(rc.Bottom-rc.Top)
		}
	}
	return d
}

// Hide 隐藏子窗（页面离开/暂停展示 DOM 覆盖层时用）。'''),
    ('''	r, visible := VideoRect(b)
	if !visible {''',
     '''	r, visible := VideoRect(b)
	e.lastIn, e.lastR, e.lastV = b, r, visible // 诊断：记下这次进来什么、换算成什么
	if !visible {'''),
])

# ── 3) 非 windows 侧：同签名 ──
rw('internal/mpv/embed_stub.go', [
    ('''// Hide 非 Windows 无子窗可隐藏。''',
     '''// Debug 非 Windows 无现场可取，返回 supported:false。
func (e *EmbedHost) Debug() HostDebug { return HostDebug{} }

// Hide 非 Windows 无子窗可隐藏。'''),
])

# ── 4) 绑定层：把现场带出去 ──
rw('app.go', [
    ('''	a.playerSvc.SetWid(a.embed.HWND())
	return marshalJSON(map[string]any{"embedded": true})''',
     '''	a.playerSvc.SetWid(a.embed.HWND())
	// 现场数据一起返回：真机"音频有画面黑"这类问题只能靠它定位（见 HostDebug）。
	return marshalJSON(map[string]any{"embedded": true, "host": a.embed.Debug()})'''),
    ('''	if err := a.embed.ApplyBounds(mpv.Bounds{X: x, Y: y, W: w, H: h}); err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"ok": true})''',
     '''	if err := a.embed.ApplyBounds(mpv.Bounds{X: x, Y: y, W: w, H: h}); err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"ok": true, "host": a.embed.Debug()})'''),
])
