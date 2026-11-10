//go:build !windows

package main

// 非 Windows 平台剪贴板 no-op（Linux CI 交叉编译/单测走这里；
// 产品目标是 Windows 壳，macOS 若支持需在真机验证剪贴板 API 后再补实现）。

func clipboardWrite(_ string) error { return nil }

func clipboardRead() (string, error) { return "", nil }
