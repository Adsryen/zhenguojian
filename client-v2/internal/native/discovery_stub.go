//go:build !v2overlay

package native

import (
	"encoding/json"
	"errors"
)

// discoveryOverlaid=false：默认二进制没有 core_overlay 入口。
const discoveryOverlaid = false

// discoveryRequest 在未启用 overlay 构建通道时返回明确错误。
// v1 的 browseFilters/browseFiltered 等动作实现在
// zhenguojian/sidecar/core_overlay（package core 的构建期 overlay，编译时合入
// core 包导出 NativeDiscovery/NativePosterMetadata），guoapp-src 本体不导出，
// 所以默认二进制拿不到。V2-2 若要真筛选面板，CI 加：
//
//	go build -tags v2overlay -overlay build/overlay.json
//
// （overlay.json 把 core_overlay/*.go 映射进 guoapp-src/native/core/ 包目录，
// 与 v1 编译脚本同一手法，见 zhenguojian/AI_BUILD.md:84。）
func discoveryRequest(raw string) string {
	var input struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal([]byte(raw), &input)
	body, _ := json.Marshal(map[string]any{
		"ok": false,
		"error": errors.New(input.Action +
			" 需要 v2overlay 构建通道（core_overlay 入口未编译进当前二进制）").Error(),
		"code": "discovery_unavailable",
	})
	return string(body)
}
