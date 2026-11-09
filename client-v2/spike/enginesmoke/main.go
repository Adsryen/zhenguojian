// spike enginesmoke: 验证 v2 桌面壳能否直调 v1 native core 拿真实红果数据。
// 事实：core 包没有导出的 NewEngine——引擎构造 newNativeEngine(directory) 未导出，
// 唯一导出入口是 core.NativeRequest(rawJSON string) JSON 分发器（app_runtime.go:305）。
// 因此冒烟走 {"action":"initialize"} + {"action":"catalog"} 两步 JSON 调用。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"duanjuapp/native/core"
)

type catalogItem struct {
	Title string `json:"title"`
	ID    string `json:"id"`
}

func call(payload map[string]any) (map[string]any, error) {
	raw, _ := json.Marshal(payload)
	start := time.Now()
	out := core.NativeRequest(string(raw))
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		return nil, fmt.Errorf("非 JSON 应答(%d bytes, %v): %.200s", len(out), err, out)
	}
	fmt.Fprintf(os.Stderr, "[%.1fs] action=%s ok=%v\n", time.Since(start).Seconds(), payload["action"], env["ok"])
	if env["ok"] != true {
		return env, fmt.Errorf("core 错误: %v", env["error"])
	}
	return env, nil
}

func main() {
	dataDir := "/tmp/spike-data"
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fmt.Println("FATAL mkdir:", err)
		os.Exit(1)
	}

	env, err := call(map[string]any{"action": "initialize", "directory": dataDir})
	if err != nil {
		fmt.Println("INIT_FAIL:", err)
		os.Exit(2)
	}
	if data, ok := env["data"].(map[string]any); ok {
		fmt.Printf("INIT_OK version=%v revision=%v\n", data["version"], data["revision"])
	}

	env, err = call(map[string]any{"action": "catalog", "source": "hongguo", "page": 1})
	if err != nil {
		fmt.Println("CATALOG_FAIL:", err)
		os.Exit(3)
	}
	data, _ := env["data"].(map[string]any)
	items, _ := data["items"].([]any)
	fmt.Printf("CATALOG hongguo page1: %d items, hasMore=%v, fresh=%v\n", len(items), data["hasMore"], data["fresh"])
	for i, it := range items {
		if i >= 3 {
			break
		}
		if m, ok := it.(map[string]any); ok {
			fmt.Printf("  #%d %q id=%v\n", i+1, m["title"], m["id"])
		}
	}

	// 搜索链路（catalog 带 query 即搜索，nativeCatalog:557 起）
	if len(os.Args) > 1 {
		env, err = call(map[string]any{"action": "catalog", "source": "hongguo", "page": 1, "query": os.Args[1]})
		if err != nil {
			fmt.Println("SEARCH_FAIL:", err)
			os.Exit(4)
		}
		data, _ := env["data"].(map[string]any)
		items, _ := data["items"].([]any)
		fmt.Printf("SEARCH %q: %d items, localSearch=%v\n", os.Args[1], len(items), data["localSearch"])
		for i, it := range items {
			if i >= 3 {
				break
			}
			if m, ok := it.(map[string]any); ok {
				fmt.Printf("  #%d %q\n", i+1, m["title"])
			}
		}
	}
}
