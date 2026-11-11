package main

// 创作者页（creatorProfile / creatorWorks）壳层绑定 —— 详情页演员条点开的
// 「创作者 · 作品墙」页数据源（v1 creators.py CreatorPage 对应物）。
//
// 通道说明：与 bindings_cast.go 同路 —— 这两个 action 在 v1 sidecar 路由表
// 属 discovery 组（zhenguojian/sidecar/main.go:78），由 core_overlay 构建期
// overlay 合入 core 包（discovery.go NativeDiscovery）。internal/native 门面
// engine.go 的 discoveryActions 表已包含 creatorProfile/creatorWorks（无需
// 改门面），这里直接 native.Call 走 discoveryRequest 双态通道：
//   - `-tags v2overlay -overlay build/overlay.json` 构建 → 真核心数据；
//   - 默认构建 → code=discovery_unavailable 错误（绑定原样返回，前端 CreatorView
//     亮红条 + 重试按钮，v1 _retry_load 语义）。
//
// 入参 creatorJSON = 演员条 creators[] 单项原样对象（最小集 {userId,name,avatar}，
// 实际传 v1 CreatorCard 同款整对象含 role/description —— discoveryProfile 会把它
// 平铺进 profile 兜底，overlay discovery.go:245-248）。
//
// 返回契约（核心 data 平铺，字段逐键对齐 discovery.go）：
//
//	CreatorProfile → {"profile":{"userId","name","description","avatar",
//	                  "fans","following","likes",…原 creator 键},
//	                  "tabs":[{"id":int,"name":str,"total":int,
//	                           "subTabs":[{"id","name","total"}]}]}
//	  fans/following/likes 为 formatHeat 已格式化串（如「35.2万」）；
//	  profile 请求失败=整个 action 报错（tabs 拉不到时同样 reject，v1 done/failed 同语义）。
//
//	CreatorWorks   → {"items":[nativeDrama…（标准目录形状，cover 可直接走
//	                  services/covers.ts resolveCover）],"cursor":"…","hasMore":bool}
//	  sort ∈ "time_desc"|"hot_desc"（非法值核心回落 time_desc，discovery.go:283-286）；
//	  tab/subTab 原样透传核心：0 = 自动取首个作品 tab（v1 CreatorPage._tab 初值
//	  同款）；v1 core_client.creator_works 的参数默认值 8/100 在前端 api.ts
//	  兜底（Go 不做默认值注入，保持与 v1 显式传 0/0 的行为一致）。

import (
	"zhenguojian/client-v2/internal/native"
)

// CreatorProfile → JSON（见文件头契约）。creatorJSON 解析失败/核心错误走 JS reject。
func (a *App) CreatorProfile(creatorJSON string) (string, error) {
	creator, err := unmarshalObject(creatorJSON)
	if err != nil {
		return "", err
	}
	data, err := native.Call("creatorProfile", map[string]any{"creator": creator})
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

// CreatorWorks → JSON（见文件头契约）。tab/subTab 传 0 表示核心自动选首个
// 作品 tab；cursor 空串=首屏；sort 非法值由核心回落 time_desc。
func (a *App) CreatorWorks(creatorJSON string, tab int, subTab int, cursor string, sort string) (string, error) {
	creator, err := unmarshalObject(creatorJSON)
	if err != nil {
		return "", err
	}
	data, err := native.Call("creatorWorks", map[string]any{
		"creator": creator, "tab": tab, "subTab": subTab,
		"cursor": cursor, "sort": sort,
	})
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}
