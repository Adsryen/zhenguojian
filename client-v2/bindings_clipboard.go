package main

// 系统剪贴板绑定（右键菜单「复制/剪切/粘贴」的 Go 回落通道）。
// WebView2 生产壳禁用了默认右键菜单，前端 navigator.clipboard 在部分
// 权限策略下也会被拒，此时经本绑定走 Win32 剪贴板直写。
// 平台实现分文件：bindings_clipboard_windows.go（Win32 LazyProc，零 cgo）/
// bindings_clipboard_other.go（非 Windows no-op）；本文件只放导出绑定
// （整结构体 Bind 自动注册，无需改 main.go）与可跨平台单测的纯逻辑 helper。

import "unicode/utf16"

// ClipboardWrite 把 text 以 CF_UNICODETEXT 写入系统剪贴板。
// 非 Windows 平台 no-op 返回 nil。
func (a *App) ClipboardWrite(text string) error {
	return clipboardWrite(text)
}

// ClipboardRead 读取系统剪贴板中的 CF_UNICODETEXT；格式不可用时返回空串。
// 非 Windows 平台 no-op 返回 ("", nil)。
func (a *App) ClipboardRead() (string, error) {
	return clipboardRead()
}

// clipboardUTF16Bytes 按 CF_UNICODETEXT 内存块布局编码字符串：
// UTF-16LE 码元 + 结尾双 NUL（0x0000 码元）。纯函数，Linux 侧可测。
func clipboardUTF16Bytes(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, (len(units)+1)*2)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return append(out, 0, 0)
}

// utf16BytesToString 解码 GlobalSize 长度的 UTF-16LE 内存块：读到首个
// 0x0000 码元为止（CF_UNICODETEXT 的空终止语义）；奇数尾字节忽略。
// 纯函数，Linux 侧可测。
func utf16BytesToString(data []byte) string {
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		u := uint16(data[i]) | uint16(data[i+1])<<8
		if u == 0 {
			break
		}
		units = append(units, u)
	}
	return string(utf16.Decode(units))
}
