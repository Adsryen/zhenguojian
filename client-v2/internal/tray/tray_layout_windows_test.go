//go:build windows

package tray

import (
	"testing"
	"unsafe"
)

// 托盘窗口类的布局自检：与 mpv 那次是同一对错误（字段顺序 + 伪句柄），
// 一起改就一起钉住。RegisterClassExW 失败会让托盘起不来，
// 用户侧的表现就是"找不到图标、关不掉程序"。
func TestTrayWndClassExLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("按 amd64 布局写死")
	}
	var wc wndclassexwTray
	if got := unsafe.Sizeof(wc); got != 80 {
		t.Fatalf("WNDCLASSEXW 尺寸应为 80，实得 %d", got)
	}
	if unsafe.Offsetof(wc.LpszMenuName) != 56 {
		t.Fatalf("lpszMenuName 应在偏移 56，实得 %d", unsafe.Offsetof(wc.LpszMenuName))
	}
	if unsafe.Offsetof(wc.LpszClassName) != 64 {
		t.Fatalf("lpszClassName 应在偏移 64，实得 %d", unsafe.Offsetof(wc.LpszClassName))
	}
	if last := unsafe.Offsetof(wc.HIconSm) + unsafe.Sizeof(wc.HIconSm); last != 80 {
		t.Fatalf("hIconSm 应填满结构，算得 %d", last)
	}
}
