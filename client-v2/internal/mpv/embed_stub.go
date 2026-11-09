//go:build !windows

package mpv

// embed_stub.go：非 Windows 平台的内嵌占位实现。
// 播放内嵌只在 Windows 真机生效；其余平台 NewEmbedHost 恒报错，
// app.go 的 PlayerAttach 据此返回 embedded:false，mpv 走独立窗路径，
// 交叉编译（GOOS=linux/darwin）保持通过。

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
func (e *EmbedHost) Hide() {}

// Close 无操作。
func (e *EmbedHost) Close() error { return nil }
