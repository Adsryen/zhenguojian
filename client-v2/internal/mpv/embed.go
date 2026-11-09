package mpv

// embed.go：播放画面窗口内嵌的纯逻辑（跨平台、可 mock、Linux 可跑单测）。
// Windows 实现在 embed_windows.go（win32 调用全部收在 _windows.go 里），
// 其他平台在 embed_stub.go 提供同签名 no-op，保证 app.go 交叉编译不炸。
//
// 设计：Go 在 Wails 主窗口下创建一个 WS_CHILD 子窗（类名 ZgjVideoHost），
// mpv 以 --wid=<子窗HWND> 挂进去（v1 Qt 线同语义：mpv 填满并跟随宿主窗尺寸）。
// 前端把播放容器矩形（CSS px × devicePixelRatio 的物理像素）经
// PlayerSetBounds 上报，Go 侧 MoveWindow 子窗；需要隐藏画面（加载/错误/
// 播完/离开页面）时上报零尺寸 → SW_HIDE。
//
// 回退开关：环境变量 ZHENGUO_NO_EMBED ∈ {1,true,yes,on}（大小写不敏感）时
// PlayerAttach 直接放弃内嵌，mpv 保持现行独立顶层窗行为。

import (
	"os"
	"strings"
)

// EnvNoEmbed 是内嵌回退开关的环境变量名。
const EnvNoEmbed = "ZHENGUO_NO_EMBED"

// EmbedWindowClass 是承载 mpv 的子窗类名（排查时可用 spy 工具按此名找窗）。
const EmbedWindowClass = "ZgjVideoHost"

// WailsWindowClass 是 Wails v2.10.1 windows 后端主窗口的固定类名
// （internal/frontend/desktop/windows/window.go 里 RegClassOnlyOnce 注册）。
const WailsWindowClass = "wailsWindow"

// MainWindowTitle 是本应用主窗口标题（main.go wails.Options.Title），
// 与类名联合校验以避开同进程中其它窗口与多实例串扰。
const MainWindowTitle = "真果鉴"

// Bounds 是视频容器相对宿主窗口客户区的矩形，单位物理像素
// （前端已按 devicePixelRatio 换算）。W/H 小于 1 视为隐藏请求。
type Bounds struct {
	X, Y, W, H int
}

// Rect 是换算后的目标窗体几何。
type Rect struct {
	X, Y, W, H int
}

// VideoRect 把上报的 Bounds 规范化为子窗几何：
//   - W/H 任一 < minEdge → 不可见（调用方应 SW_HIDE）；
//   - X/Y 负值钳到 0（客户区外坐标无意义，防越界绘制）。
func VideoRect(b Bounds) (Rect, bool) {
	const minEdge = 1
	if b.W < minEdge || b.H < minEdge {
		return Rect{}, false
	}
	r := Rect{X: b.X, Y: b.Y, W: b.W, H: b.H}
	if r.X < 0 {
		r.X = 0
	}
	if r.Y < 0 {
		r.Y = 0
	}
	return r, true
}

// WindowCandidate 是顶层窗口枚举的一条候选记录（embed_windows.go 填充，
// PickHostWindow 纯函数消费，方便 mock 测试）。
type WindowCandidate struct {
	Hwnd      uintptr
	PID       uint32
	ClassName string
	Title     string
	Visible   bool
}

// PickHostWindow 从候选里挑宿主主窗口：
//   - 必须同进程（PID==selfPID，多实例安全）、必须可见；
//   - 打分：类名+标题都对=3 > 仅类名=2 > 仅标题=1 > 其它=0（拒绝，
//     避免误挂到同进程的隐藏消息窗/对话框上）；
//   - 取最高分第一个；无得分者返回 0（调用方决定重试或降级）。
func PickHostWindow(cands []WindowCandidate, selfPID uint32, wantClass, wantTitle string) uintptr {
	best, bestScore := uintptr(0), 0
	for _, c := range cands {
		if c.Hwnd == 0 || c.PID != selfPID || !c.Visible {
			continue
		}
		score := 0
		classOK := wantClass != "" && c.ClassName == wantClass
		titleOK := wantTitle != "" && c.Title == wantTitle
		switch {
		case classOK && titleOK:
			score = 3
		case classOK:
			score = 2
		case titleOK:
			score = 1
		}
		if score > bestScore {
			best, bestScore = c.Hwnd, score
		}
	}
	return best
}

// EmbedAllowed 判断内嵌开关：getenv 传 nil 时用 os.Getenv（测试注入假函数）。
func EmbedAllowed(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	switch strings.ToLower(strings.TrimSpace(getenv(EnvNoEmbed))) {
	case "1", "true", "yes", "on":
		return false
	default:
		return true
	}
}
