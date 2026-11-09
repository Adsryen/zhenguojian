// 壳绑定冒烟（CI 步骤 go run ./bindings/smoke 执行）：验证进程内核心
// 与绑定门面在干净数据目录下可用——Ping/ListSources/ResourceSettings。
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"zhenguojian/client-v2/internal/native"
)

func main() {
	if err := native.Init(); err != nil {
		fmt.Println("SMOKE_FAIL initialize:", err)
		os.Exit(1)
	}
	items, err := native.ListSources()
	if err != nil || len(items) < 14 {
		fmt.Println("SMOKE_FAIL listSources:", err, len(items))
		os.Exit(1)
	}
	data, err := native.ResourceSettings()
	if err != nil {
		fmt.Println("SMOKE_FAIL resourceSettings:", err)
		os.Exit(1)
	}
	b, _ := json.Marshal(data)
	fmt.Println("SMOKE_OK sources:", len(items), "net:", string(b))
}
