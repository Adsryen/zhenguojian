package native

import "fmt"

// 领域方法：payload 字段照抄 v1 core_client.py 对应方法，不发明新 schema。
// 返回值为信封 data 平铺后的对象（字段名与 v1 一致：items/hasMore/page/
// warning/localSearch/fresh、chapters、url/headers/quality…）。

// Catalog 拉一页目录。query 非空即搜索（v1：catalog 带 query 就是搜索链路）。
// lane 对应 v1 的 read lane（如 "library"/"search"/"discover"）：同 lane 新请求
// 取消旧请求，不同 lane 互不干扰——剧库与搜索绝不能共用 lane。
// force=true 跳过 core 本地缓存强制回源。
func Catalog(source, query, category string, page int, lane string, force bool) (map[string]any, error) {
	if source == "" {
		source = "hongguo"
	}
	if page < 1 {
		page = 1
	}
	payload := map[string]any{"source": source, "query": query,
		"category": category, "page": page, "force": force}
	if s := laneSession("catalog", lane); s != "" {
		payload["session"] = s
	}
	return Call("catalog", payload)
}

// Detail 拉剧集详情（含分集 chapters）。drama 传 catalog item 原样对象。
func Detail(drama map[string]any) (map[string]any, error) {
	return Call("detail", map[string]any{"drama": drama})
}

// Resolve 解析一集播放地址，返回 plan（url/headers/quality/qualities/session…）。
func Resolve(drama, chapter map[string]any, index, quality int) (map[string]any, error) {
	return Call("resolve", map[string]any{"drama": drama, "chapter": chapter,
		"index": index, "quality": quality})
}

// ListSources 全部站源描述（core provider_registry 单一事实源）。
// 归一化逻辑照抄 v1 core_client.list_sources：enabled 缺省按 true；
// name 缺省回退 displayName/slug。
func ListSources() ([]map[string]any, error) {
	data, err := Call("listSources", nil)
	if err != nil {
		return nil, err
	}
	raw, _ := data["items"].([]any)
	view := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if _, has := row["enabled"]; !has {
			row["enabled"] = true
		}
		if name, _ := row["name"].(string); name == "" {
			display, _ := row["displayName"].(string)
			slug, _ := row["slug"].(string)
			if display != "" {
				row["name"] = display
			} else {
				row["name"] = slug
			}
		}
		view = append(view, row)
	}
	return view, nil
}

// SetSourceEnabled 开关站源；core 写 data 目录 source_prefs.json 后立即生效。
func SetSourceEnabled(slug string, enabled bool) (map[string]any, error) {
	if slug == "" {
		return nil, fmt.Errorf("站源 slug 不能为空")
	}
	return Call("setSourceEnabled", map[string]any{"slug": slug, "enabled": enabled})
}

// BrowseFilters 取找剧筛选面板的分组（发现页 chips 数据源）。
// category 为 v1 browse_filters 的 genre 入参，可空。
// 注意：该动作依赖 discovery overlay 构建通道（见 engine.go 头注释），
// 默认构建返回「未编译」错误，前端须走兜底 chips。
func BrowseFilters(source, category string) (map[string]any, error) {
	payload := map[string]any{"category": category}
	if source != "" {
		payload["source"] = source
	}
	return Call("browseFilters", payload)
}

// BrowseFiltered 按筛选组合拉一页（filters 为 v1 的 map[string]string）。
func BrowseFiltered(source string, filters map[string]string, offset int, cursor string) (map[string]any, error) {
	payload := map[string]any{"filters": filters, "offset": offset, "cursor": cursor}
	if source != "" {
		payload["source"] = source
	}
	return Call("browseFiltered", payload)
}

// DanmakuWindow 按播放会话拉一个时间窗的弹幕（绝对窗尾 endMs，单位毫秒）。
// 核心 payload 的 durationMs 即绝对窗尾（v1 main.py 弹幕循环同款用法）。
func DanmakuWindow(session string, startMs, endMs int64) (map[string]any, error) {
	if session == "" {
		return nil, fmt.Errorf("弹幕会话不能为空")
	}
	return Call("danmaku", map[string]any{
		"playbackSession": session,
		"startMs":         startMs,
		"durationMs":      endMs,
	})
}

// DanmakuAll 拉整集弹幕（受核心上限约束），供播放器降级或导出使用。
func DanmakuAll(session string) (map[string]any, error) {
	if session == "" {
		return nil, fmt.Errorf("弹幕会话不能为空")
	}
	return Call("danmakuAll", map[string]any{"playbackSession": session})
}

// DramaCreators 取剧集的演职员条（v1 core_client.drama_creators：
// payload {"drama": <完整 item 对象>}；overlay 侧只消费 drama.id，但保持
// v1 同款整对象入参）。data 形状（zhenguojian/sidecar/core_overlay/
// discovery.go creatorDetailCache 写入）：
//
//	{"creators":[{"id","userId","name","avatar","description","role"},…],
//	 "recordNumber","heat","followers","plays"}
//
// 注意：与 BrowseFilters 同走 discovery overlay 通道（engine.go 头注释），
// 默认构建返回 code=discovery_unavailable，前端据此静默隐藏演员条。
func DramaCreators(drama map[string]any) (map[string]any, error) {
	return Call("dramaCreators", map[string]any{"drama": drama})
}
