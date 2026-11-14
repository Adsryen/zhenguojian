//go:build windows

package mpv

import (
	"testing"
	"unsafe"
)

// WNDCLASSEXW 的布局自检。
//
// 为什么值得单独一条测试：真机 alpha.27 报的
// 「mpv: RegisterClassExW(ZgjVideoHost) 失败: The parameter is incorrect.」
// 有两个独立成因，其中一个就是**结构体字段顺序与 Win32 定义不一致**
// （LpszClassName 写在 LpszMenuName 前面），另一个是缺 hIconSm
// 导致 cbSize 比真实结构小 8 字节。
// 这类错误在本机（Linux）永远测不到，编译器也不管——
// 它只在运行时被 Windows 拒绝。所以把它钉成一条静态断言。
func TestWndClassExLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("本断言按 amd64 布局写死；386 指针宽度不同")
	}
	var wc wndclassexw
	if got := unsafe.Sizeof(wc); got != uintptr(wantWndClassExSize) {
		t.Fatalf("WNDCLASSEXW 尺寸应为 %d，实得 %d（cbSize 会照此上报，错了系统直接判 87）",
			wantWndClassExSize, got)
	}
	checks := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"HInstance", unsafe.Offsetof(wc.HInstance), wantInstanceOffset},
		{"LpszMenuName", unsafe.Offsetof(wc.LpszMenuName), wantMenuNameOffset},
		{"LpszClassName", unsafe.Offsetof(wc.LpszClassName), wantClassNameOffset},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Fatalf("%s 偏移应为 %d，实得 %d —— 字段顺序必须逐位对上 Win32 定义",
				c.name, c.want, c.got)
		}
	}
	// hIconSm 必须存在且在最后一位（缺字段就是这次 87 的成因之一）
	if last := unsafe.Offsetof(wc.HIconSm) + unsafe.Sizeof(wc.HIconSm); last != uintptr(wantWndClassExSize) {
		t.Fatalf("hIconSm 应是最后一个字段并填满结构，算得 %d", last)
	}
}
