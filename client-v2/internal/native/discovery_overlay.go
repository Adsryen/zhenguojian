//go:build v2overlay

package native

import "duanjuapp/native/core"

// discoveryOverlaid=true：core.NativeDiscovery 已由 overlay 合入 core 包。
const discoveryOverlaid = true

// discoveryRequest 在 v2overlay 构建通道下转发到 core.NativeDiscovery——
// 该符号仅在 go build -overlay 把 zhenguojian/sidecar/core_overlay/*.go
// 合入 core 包时存在（v1 编译脚本同款手法）。
func discoveryRequest(raw string) string {
	return core.NativeDiscovery(raw)
}
