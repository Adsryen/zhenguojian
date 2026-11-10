package main

// 剪贴板 helper 单测：只测纯逻辑（UTF-16 编解码布局），Win32 调用不在
// Linux CI 覆盖（无剪贴板设备）；Windows 实现由交叉编译 + vet 把关。

import (
	"bytes"
	"testing"
)

func TestClipboardUTF16RoundTrip(t *testing.T) {
	cases := []string{
		"",
		"abc",
		"中文字幕「真果鉴」v2",
		"emoji 😀 mix",
		"line1\nline2\ttab",
	}
	for _, s := range cases {
		if got := utf16BytesToString(clipboardUTF16Bytes(s)); got != s {
			t.Errorf("round-trip %q: got %q", s, got)
		}
	}
}

func TestClipboardUTF16BytesLayout(t *testing.T) {
	// 'A' U+0041 → 41 00；'中' U+4E2D → 2D 4E；结尾 0000 码元。
	want := []byte{0x41, 0x00, 0x2D, 0x4E, 0x00, 0x00}
	if got := clipboardUTF16Bytes("A中"); !bytes.Equal(got, want) {
		t.Errorf("layout: got % X want % X", got, want)
	}
}

func TestUTF16BytesToStringStopsAtNul(t *testing.T) {
	// 缓冲区尾随多余垃圾（GlobalSize 大于实际字符串）：只读到首个 NUL。
	data := []byte{0x41, 0x00, 0x42, 0x00, 0x00, 0x00, 0x43, 0x00}
	if got := utf16BytesToString(data); got != "AB" {
		t.Errorf("nul stop: got %q want %q", got, "AB")
	}
}

func TestUTF16BytesToStringOddTail(t *testing.T) {
	// 奇数尾字节（理论不应出现）忽略，不得 panic。
	if got := utf16BytesToString([]byte{0x41, 0x00, 0x00}); got != "A" {
		t.Errorf("odd tail: got %q want %q", got, "A")
	}
	if got := utf16BytesToString([]byte{0x41}); got != "" {
		t.Errorf("1-byte buf: got %q want empty", got)
	}
}

func TestUTF16BytesToStringUnpairedSurrogate(t *testing.T) {
	// 孤立高代理项 → utf16.Decode 产出 U+FFFD，不应 panic。
	data := []byte{0x3D, 0xD8, 0x00, 0x00} // D83D 单独 + NUL
	if got := utf16BytesToString(data); got != "\uFFFD" {
		t.Errorf("lone surrogate: got %q", got)
	}
}
