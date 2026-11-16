//go:build !windows && !linux

package mpv

// embed_stub.go：非 Windows、非 Linux 平台的内嵌占位实现（即 darwin 等）。
// 播放内嵌：Windows 真机（embed_windows.go）与 Linux/X11（embed_linux.go）
// 各有真实现；其余平台 NewEmbedHost 恒报错，app.go 的 PlayerAttach 据此
// 返回 embedded:false，mpv 走独立窗路径，交叉编译保持通过。
//
// 点击穿透带不在此补桩：SetHitBands/VideoHitBands 状态在 embed.go 跨平台
// 存在（非 Windows 存而无消费方，行为等价 no-op），且 PlayerAttach 在
// 本平台必失败 → app.go PlayerHitBands 走 embed==nil 的 no-op 分支。

import "errors"

// EmbedHost 非 Windows 空壳。
type EmbedHost struct{}

// NewEmbedHost 在非 Windows 恒失败（调用方应把失败视为「不内嵌」而非致命错误）。
func NewEmbedHost() (*EmbedHost, error) {
	return nil, errors.New("mpv: 播放画面内嵌仅支持 Windows")
}

// HWND 恒 0。
func (e *EmbedHost) HWND() uintptr { return 0 }

// ApplyBounds 无操作。
func (e *EmbedHost) ApplyBounds(Bounds) error { return nil }

// Hide 无操作。
// Debug 非 Windows 没有现场可取，supported:false。
func (e *EmbedHost) Debug() HostDebug { return HostDebug{} }

func (e *EmbedHost) Hide() {}

// Close 无操作。
func (e *EmbedHost) Close() error { return nil }
