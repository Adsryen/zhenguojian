package main

import (
	"runtime"
	"strings"
	"testing"
)

// 第六十一轮：真机徽标给出「子窗 1×1 · 可见=0 · 最近上报 0×0」，
// 查出来是"几何"和"可见性"被合成了一件事（隐藏=上报 0×0，而隐藏分支又不摆位），
// 于是 mpv 从 spawn 起就在创建尺寸 1×1 的 drawable 上画。
// 修法把 Bounds.Show 拆出来，PlayerSetBounds 因此多了一个参数。
// 这个文件钉的是**绑定层那一侧**：多出来的参数不能只有编译级证据。

func TestPlayerSetBoundsWithoutEmbedIsNotAnError(t *testing.T) {
	a := &App{}
	// embed==nil（非 Windows、attach 失败、或已 detach）必须是"正常降级"：
	// 前端每次几何变化都会调这里，返回 error 会把控制台刷满、
	// 还会让 reportBounds 的 catch 吞掉后续 hitBands 上报。
	out, err := a.PlayerSetBounds(100, 50, 800, 600, true)
	if err != nil {
		t.Fatalf("embed 缺席时不该报错（降级是正常分支），实得 %v", err)
	}
	if !strings.Contains(out, `"ok":false`) {
		t.Fatalf("应回 {\"ok\":false} 让前端知道没生效，实得 %s", out)
	}
}

func TestPlayerSetBoundsDegenerateGeometryNeverPanics(t *testing.T) {
	a := &App{}
	// 旧约定里 0×0 被当作"隐藏"专用值；现在 show 与尺寸解耦后，
	// 任何尺寸/坐标组合（含负数、1px、全零）都必须活着返回 ——
	// 前端量到的矩形在布局抖动时什么都能出现。
	cases := [][5]any{
		{0, 0, 0, 0, true},
		{-5, -9, 10, 10, true},
		{1, 1, 1, 1, true},
		{100, 50, 800, 600, false}, // 真实几何 + 隐藏：这一轮新增的合法组合
	}
	for i, c := range cases {
		x, y, w, h := c[0].(int), c[1].(int), c[2].(int), c[3].(int)
		show := c[4].(bool)
		out, err := a.PlayerSetBounds(x, y, w, h, show)
		if err != nil {
			t.Errorf("用例 %d (%d,%d,%d,%d show=%v) 不该报错：%v", i, x, y, w, h, show, err)
		}
		if out == "" {
			t.Errorf("用例 %d 返回空串，前端 JSON.parse 会拿到 undefined", i)
		}
	}
}

func TestPlayerAttachReportsUnsupportedOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 真机可能真的内嵌成功，这条只钉非 Windows 的降级形状")
	}
	a := &App{}
	out, err := a.PlayerAttach()
	if err != nil {
		t.Fatalf("attach 失败不该是 error（回退是正常分支）：%v", err)
	}
	if !strings.Contains(out, `"embedded":false`) {
		t.Fatalf("非 Windows 必须回 embedded:false，实得 %s", out)
	}
	// attach 失败后紧跟的 setBounds 也必须无害 —— 真机上这条序列是
	// PlayerAttach → reportBounds → PlayerOpen，一步炸就整页黑。
	if _, err := a.PlayerSetBounds(0, 0, 640, 360, false); err != nil {
		t.Fatalf("attach 失败后 setBounds 不该报错：%v", err)
	}
}
