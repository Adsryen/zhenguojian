//go:build v2overlay

package native

import "duanjuapp/native/core"

// discoveryOverlaid=true：core.NativeDiscovery 已由 overlay 合入 core 包。
const discoveryOverlaid = true

// discoveryRequest 在 v2overlay 构建通道下转发到 core.NativeDiscovery——
// 该符号仅在 go build -overlay 把 zhenguojian/sidecar/core_overlay/*.go
// 合入 core 包时存在（v1 编译脚本同款手法）。
//
// 信封注意：NativeDiscovery 返回**完整信封** {"ok":bool,"data":…,"error":…}
// （v1 core_client.py browse_filters 解包口径），与 Call 通道的其余分支
// （NativeRequest/NativeSourcesCommand 平铺输出）不同。这里原样透传信封，
// decodeEnvelope 统一解包——stub 通道（discovery_stub.go）同形。
func discoveryRequest(raw string) string {
	return core.NativeDiscovery(raw)
}
