//go:build !windows

package tray

// tray_other.go：非 Windows 降级 —— Start 返回「当前平台不支持托盘」，
// ShowInfoBox 恒失败（绑定层据此回落 EventsEmit），行为与 Windows 侧同签名。

func newBackend() backend { return noopBackend{} }

type noopBackend struct{}

func (noopBackend) Register(*Handler) error { return ErrUnsupported }
func (noopBackend) Unregister() error       { return nil }

// ShowInfoBox 非 Windows 不支持原生弹窗，恒返回错误；调用方回落事件通道。
func ShowInfoBox(title, text string) error { return ErrUnsupported }
