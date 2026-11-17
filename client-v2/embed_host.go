package main

import "zhenguojian/client-v2/internal/mpv"

// embedHost 是壳层用到的内嵌宿主接口。三个平台的 *mpv.EmbedHost
// （Windows/X11/占位）都实现它，且方法各自持内层锁（e.mu）——所以壳层
// 只需要**短临界区**读字段，不再跨整个宿主调用持 embedMu。
//
// 为什么从具体类型改成接口（第一百轮）：alpha.42 真机
// PlayerSetBounds:7/2P5=7 次上报只有 2 次进了临界区、5 次等锁 >8s，
// 长占点是 PlayerAttach 的建造路径 findHostWindow（至多 2s 轮询 sleep）
// 被压在 embedMu 里，把建造与全部上报串行化。修法是建造移出锁
// （App.ensureEmbed），而把建造缝成可注入的 newEmbedHostFunc 之后，
// 「建造不阻塞上报」第一次可以在单测里**执行级**验证（不真建窗）。
type embedHost interface {
	HWND() uintptr
	ApplyBounds(b mpv.Bounds) error
	Hide()
	Debug() mpv.HostDebug
}

// newEmbedHostFunc 是建造入口。做成包级变量（同 stopTray/emitEventFunc
// 的缝约定）只为测试注入：单测里让它慢 500ms 或直接失败，钉死
// 「建造期间 setBounds 不排队」「建造失败=embedded:false 回退」两条。
// 生产路径就是各平台的 mpv.NewEmbedHost，逐位等价。
var newEmbedHostFunc = func() (embedHost, error) { return mpv.NewEmbedHost() }
