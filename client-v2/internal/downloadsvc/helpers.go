// helpers.go —— 包内通用小函数。
package downloadsvc

import (
	"fmt"
	"io"
	"math"
	"runtime"
)

// humanSize v1 human_size：B/KB/MB/GB/TB，首档取整其余一位小数。
func humanSize(bytes int64) string {
	b := float64(bytes)
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f B", b)
	}
	return fmt.Sprintf("%.1f %s", b, units[i])
}

// copyAll 与调用点签名一致：copyAll(src, dst)。
func copyAll(src io.Reader, dst io.Writer) (int64, error) {
	return io.Copy(dst, src)
}

func parallelism() int {
	if n := runtime.GOMAXPROCS(0); n > 0 {
		return n
	}
	return 1
}

func roundTo(v, unit float64) float64 {
	return math.Round(v/unit) * unit
}
