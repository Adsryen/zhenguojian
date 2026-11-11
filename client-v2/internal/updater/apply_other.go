//go:build !windows

package updater

import "fmt"

// ApplySupported 非 Windows 不生成 bat，也不做进程自替换（决策约定）。
const ApplySupported = false

// LaunchDetached 仅 Windows 支持自动更新。
func LaunchDetached(batPath string) error {
	return fmt.Errorf("仅 Windows 支持自动更新")
}
