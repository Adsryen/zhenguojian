package mpv

// embed_test.go：内嵌纯函数单测（Linux 交叉环境可跑，不触 Win32）。
// 覆盖：Bounds→几何换算钳位、宿主窗口候选筛选（多实例/不可见/降级打分）、
// ZHENGUO_NO_EMBED 回退开关解析。

import (
	"testing"
)

func TestVideoRect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      Bounds
		want    Rect
		visible bool
	}{
		{"原样透传", Bounds{X: 120, Y: 64, W: 960, H: 540}, Rect{120, 64, 960, 540}, true},
		{"负坐标钳0", Bounds{X: -5, Y: -9, W: 100, H: 50}, Rect{0, 0, 100, 50}, true},
		{"零宽隐藏", Bounds{X: 10, Y: 10, W: 0, H: 200}, Rect{}, false},
		{"零高隐藏", Bounds{X: 10, Y: 10, W: 200, H: 0}, Rect{}, false},
		{"负尺寸隐藏", Bounds{X: 1, Y: 1, W: -3, H: -4}, Rect{}, false},
		{"全零隐藏", Bounds{}, Rect{}, false},
		{"1px 边缘可见", Bounds{X: 0, Y: 0, W: 1, H: 1}, Rect{0, 0, 1, 1}, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got, visible := VideoRect(c.in)
			if visible != c.visible {
				t.Fatalf("visible=%v, want %v", visible, c.visible)
			}
			if got != c.want {
				t.Fatalf("rect=%+v, want %+v", got, c.want)
			}
		})
	}
}

func TestPickHostWindow(t *testing.T) {
	t.Parallel()
	const self, other uint32 = 4000, 9000
	cands := []WindowCandidate{
		{Hwnd: 0x111, PID: self, ClassName: "wailsWindow", Title: "真果鉴", Visible: true},  // 3 分正解
		{Hwnd: 0x222, PID: other, ClassName: "wailsWindow", Title: "真果鉴", Visible: true}, // 多实例他进程
		{Hwnd: 0x333, PID: self, ClassName: "wailsWindow", Title: "真果鉴", Visible: false}, // 不可见
	}
	if got := PickHostWindow(cands, self, WailsWindowClass, MainWindowTitle); got != 0x111 {
		t.Fatalf("正解筛选 got=%x want=111", got)
	}

	// 仅类名匹配优先于仅标题匹配。
	cands2 := []WindowCandidate{
		{Hwnd: 0x444, PID: self, ClassName: "wailsWindow", Title: "其他页面", Visible: true}, // 2 分
		{Hwnd: 0x555, PID: self, ClassName: "SomeDialog", Title: "真果鉴", Visible: true},   // 1 分
	}
	if got := PickHostWindow(cands2, self, WailsWindowClass, MainWindowTitle); got != 0x444 {
		t.Fatalf("类名优先 got=%x want=444", got)
	}

	// 全是无关窗（都不匹配）→ 0，拒绝乱挂。
	cands3 := []WindowCandidate{
		{Hwnd: 0x666, PID: self, ClassName: "SysDragImage", Title: "", Visible: true},
		{Hwnd: 0x777, PID: self, ClassName: "", Title: "无关标题", Visible: true},
	}
	if got := PickHostWindow(cands3, self, WailsWindowClass, MainWindowTitle); got != 0 {
		t.Fatalf("无匹配应返回 0, got=%x", got)
	}

	// 仅标题匹配也可命中（类名探测失败降级路径）。
	cands4 := []WindowCandidate{{Hwnd: 0x888, PID: self, ClassName: "wailsWindow2", Title: "真果鉴", Visible: true}}
	if got := PickHostWindow(cands4, self, WailsWindowClass, MainWindowTitle); got != 0x888 {
		t.Fatalf("标题降级 got=%x want=888", got)
	}

	if got := PickHostWindow(nil, self, WailsWindowClass, MainWindowTitle); got != 0 {
		t.Fatalf("空候选应返回 0, got=%x", got)
	}
	if got := PickHostWindow(cands, other, WailsWindowClass, MainWindowTitle); got != 0x222 {
		t.Fatalf("以 other 视角应命中其本进程窗 got=%x want=222", got)
	}
	// Hwnd=0 的脏候选被跳过。
	cands5 := []WindowCandidate{{Hwnd: 0, PID: self, ClassName: "wailsWindow", Title: "真果鉴", Visible: true}}
	if got := PickHostWindow(cands5, self, WailsWindowClass, MainWindowTitle); got != 0 {
		t.Fatalf("Hwnd=0 脏候选 got=%x want=0", got)
	}
}

func TestEmbedAllowed(t *testing.T) {
	// 不用 t.Parallel：os.Getenv 分支要 t.Setenv，两者互斥。
	fake := func(v string) func(string) string {
		return func(name string) string {
			if name != EnvNoEmbed {
				t.Fatalf("意外读取变量 %q", name)
			}
			return v
		}
	}
	for _, v := range []string{"1", "true", "TRUE", " yes ", "On"} {
		if EmbedAllowed(fake(v)) {
			t.Fatalf("ZHENGUO_NO_EMBED=%q 应禁用内嵌", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "off", "1 "} {
		// "1 " 经 TrimSpace 后是 "1" → 应禁用；其余允许。
		want := v != "1 "
		if got := EmbedAllowed(fake(v)); got != want {
			t.Fatalf("ZHENGUO_NO_EMBED=%q got=%v want=%v", v, got, want)
		}
	}

	// getenv=nil 走 os.Getenv 真值路径。
	t.Setenv(EnvNoEmbed, "1")
	if EmbedAllowed(nil) {
		t.Fatal("os.Getenv=1 应禁用内嵌")
	}
	t.Setenv(EnvNoEmbed, "")
	if !EmbedAllowed(nil) {
		t.Fatal("空值应允许内嵌")
	}
}
