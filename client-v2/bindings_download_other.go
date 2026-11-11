//go:build !windows

package main

import "fmt"

// OpenDownloadPath 在非 Windows 平台仅打开目录（xdg-open），文件定位选中不支持。
func (a *App) OpenDownloadPath(path string) error {
	if path == "" {
		return fmt.Errorf("目录参数为空")
	}
	return fmt.Errorf("当前平台不支持打开下载目录")
}
