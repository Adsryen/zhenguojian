package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// OpenDownloadPath 在资源管理器中打开下载目录；传入具体文件时定位并选中该文件。
// 目录不存在时自动创建（对齐 v1 _open_path 的 exist_ok 语义）。
func (a *App) OpenDownloadPath(path string) error {
	if path == "" {
		return fmt.Errorf("目录参数为空")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	st, serr := os.Stat(abs)
	if serr != nil {
		if !os.IsNotExist(serr) {
			return serr
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return err
		}
		st, _ = os.Stat(abs)
	}
	cmd := exec.Command("explorer.exe")
	if st != nil && !st.IsDir() {
		cmd.Args = append(cmd.Args, "/select,", abs)
	} else {
		cmd.Args = append(cmd.Args, abs)
	}
	cmd.SysProcAttr = hideWindowAttr()
	return cmd.Start()
}
