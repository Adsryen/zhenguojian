//go:build windows

package mpv

import (
	"testing"
)

// 诊断回执（HostDebug.ApplyCount / LastIn*）的自证。
//
// 为什么需要这条测试：真机连续两个 tag 的徽标都只显示「最近上报 0×0」，
// 而这个数字**无法区分两种完全相反的成因**——
//
//	① 前端根本没调用 setBounds（线断了 / 构建是旧的）；
//	② 调用了，但那一刻 stage 量出来就是 0（布局时机）。
//
// 我据此猜了一轮，所以把「上报到达次数」做进诊断，并在这里钉住它的语义：
// 计数与入参必须**如实记录每一次调用**，包括失败与零尺寸的那些次。
//
// 这里用假句柄：Win32 对无效 HWND 只会返回失败，不会崩；
// 而 ApplyCount/LastIn 走的是结构体字段，不依赖 Win32 的成功与否——
// 这正好也是我要验的那一半（摆位调用成没成功由真机徽标看，计数不能跟着一起丢）。
func TestApplyBoundsRecordsEveryReport(t *testing.T) {
	e := &EmbedHost{parent: 0, child: 0xBEEF}

	if got := e.Debug().ApplyCount; got != 0 {
		t.Fatalf("新建现场 ApplyCount 应为 0，得 %d", got)
	}

	// 1) 隐藏态也必须把入参原样记下来（第六十一轮修的就是「隐藏就不摆位」）
	if err := e.ApplyBounds(Bounds{X: 10, Y: 20, W: 800, H: 600, Show: false}); err != nil {
		t.Fatalf("ApplyBounds(隐藏态) 不该报错: %v", err)
	}
	d := e.Debug()
	if d.ApplyCount != 1 {
		t.Errorf("一次上报后 ApplyCount 应为 1，得 %d", d.ApplyCount)
	}
	if d.LastInW != 800 || d.LastInH != 600 {
		t.Errorf("LastIn 应原样记录入参，得 %dx%d", d.LastInW, d.LastInH)
	}
	// 换算后的矩形在隐藏态也保留真实尺寸：这是「几何与可见性分开」的结构不变式
	if d.LastOutW != 800 || d.LastOutH != 600 {
		t.Errorf("隐藏态换算矩形应仍是 800x600（旧行为会归零），得 %dx%d", d.LastOutW, d.LastOutH)
	}
	if d.LastOutVisible {
		t.Errorf("Show=false 时换算可见性应为 false")
	}

	// 2) 零尺寸同样计数——徽标要能区分「没调用」和「调用时量到 0」
	if err := e.ApplyBounds(Bounds{X: 0, Y: 0, W: 0, H: 0, Show: true}); err != nil {
		t.Fatalf("ApplyBounds(零尺寸) 不该报错: %v", err)
	}
	d = e.Debug()
	if d.ApplyCount != 2 {
		t.Errorf("零尺寸那次也必须计数，ApplyCount 应为 2，得 %d", d.ApplyCount)
	}
	if d.LastInW != 0 {
		t.Errorf("零尺寸应如实记录 0，得 %d", d.LastInW)
	}

	// 3) 子窗已销毁时**不计数**：否则「线断了」会被记成「送到了」，
	//    正是我要靠这个字段排除的那种误判。
	dead := &EmbedHost{child: 0}
	if err := dead.ApplyBounds(Bounds{W: 800, H: 600}); err == nil {
		t.Errorf("子窗销毁应报错")
	}
	if dead.Debug().ApplyCount != 0 {
		t.Errorf("销毁后的调用不得计数，得 %d", dead.Debug().ApplyCount)
	}
}
