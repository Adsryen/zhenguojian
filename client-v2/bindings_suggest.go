package main

// 搜索联想与切集预缓冲绑定 —— 对齐 v1 core_client.suggestions/preload。

import (
	"zhenguojian/client-v2/internal/native"
)

// Suggest 返回红果联想词 data JSON（含 items:[]string 形状以核心为准）。
func (a *App) Suggest(query string) (string, error) {
	data, err := native.Suggestions(query)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

// PreloadEpisode 预热下一集（片头拉进内存，切集秒开）。返回 plan JSON；
// 失败仅记日志级错误，前端不弹（v1 静默语义）。
func (a *App) PreloadEpisode(dramaJSON, chapterJSON string, index, quality int) (string, error) {
	drama, err := unmarshalObject(dramaJSON)
	if err != nil {
		return "", err
	}
	chapter, err := unmarshalObject(chapterJSON)
	if err != nil {
		return "", err
	}
	data, err := native.Preload(drama, chapter, index, quality)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}
