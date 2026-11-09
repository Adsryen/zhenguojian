//go:build !windows

package downloadsvc

import "syscall"

// diskFree 返回 path 所在卷的可用字节数；失败返回 0（调用方视为跳过检查）。
func diskFree(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
