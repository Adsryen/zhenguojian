package main

// 演职员（dramaCreators）壳层绑定 —— 详情页「演职人员」条数据源。
//
// 通道说明：dramaCreators 属 v1 sidecar 路由表里的 discovery 组
// （zhenguojian/sidecar/main.go:78），由 core_overlay 构建期 overlay 合入
// core 包（zhenguojian/sidecar/core_overlay/discovery.go NativeDiscovery）。
// client-v2 门面 native.Call 已按 engine.go discoveryActions 表把它路由到
// discoveryRequest：
//   - `-tags v2overlay -overlay build/overlay.json` 构建 → 真核心数据；
//   - 默认构建 → code=discovery_unavailable 错误（绑定原样返回，
//     前端 catch 后静默隐藏演员条，见 DetailView.vue）。
//
// 入参 = catalog item 原样 JSON 字符串（整个 drama 对象，v1
// core_client.drama_creators(drama) 同形，overlay 内部只用 drama.id 拆
// hongguo seriesId）。返回核心 data 平铺 JSON：
//
//	{"creators":[{"id","userId","name","avatar","description","role"},…],
//	 "recordNumber":"…","heat":"…","followers":"…","plays":"…"}
//
// creators 按核心原序返回（overlay 已做「导演」稳定前置）。

import (
	"zhenguojian/client-v2/internal/native"
)

// DramaCreators → JSON（见文件头契约）。dramaJSON 解析失败/核心错误走 JS reject。
func (a *App) DramaCreators(dramaJSON string) (string, error) {
	drama, err := unmarshalObject(dramaJSON)
	if err != nil {
		return "", err
	}
	data, err := native.DramaCreators(drama)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}
