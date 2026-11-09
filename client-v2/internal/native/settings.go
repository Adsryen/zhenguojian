package native

// V2-4 设置绑定：网络与下载设置走进程内核心的 resourceSettings /
// saveResourceSettings 动作（payload 字段与 v1 core_client 完全一致，
// settings 嵌套对象由核心侧校验并即时生效）。

// ResourceSettings 返回当前资源/网络设置快照（proxyMode、proxyUrl、
// catalogConcurrency、catalogIntervalMs、downloadConcurrency、downloadBySource）。
func ResourceSettings() (map[string]any, error) {
	return Call("resourceSettings", nil)
}

// SaveResourceSettings 保存设置并即时生效；只发送显式给出的字段，
// 非法值由核心返回中文校验错误。
func SaveResourceSettings(settings map[string]any) (map[string]any, error) {
	return Call("saveResourceSettings", map[string]any{"settings": settings})
}
