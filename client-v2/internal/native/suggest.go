package native

// Suggestions / Preload 门面 —— 搜索联想词与切集预缓冲。
//
// 协议对齐 v1 core_client.py：
//   - suggestions(query) → :341 `call("suggestions", query=query)` 取 items；
//   - preload(drama, chapter, index, quality) → :477 `call("preload", …)`，
//     核心 nativePreload 解析并把片头几 MB 拉进内存，应答同 resolve 的 plan
//     外加 prefetchedBytes / expiresAt（app_runtime.go:440）。

// Suggestions 返回联想词原始 data（含 items 数组）。
func Suggestions(query string) (map[string]any, error) {
	return Call("suggestions", map[string]any{"query": query})
}

// Preload 解析并预热指定集，返回 plan JSON（前端只用成功/失败）。
func Preload(drama, chapter map[string]any, index, quality int) (map[string]any, error) {
	// session/sequence 由 composeInput 统一补（v1 call() 同款默认
	// "owner:preload"），与 resolve 的隐式会话互不干扰。
	return Call("preload", map[string]any{
		"drama":   drama,
		"chapter": chapter,
		"index":   index,
		"quality": quality,
	})
}
