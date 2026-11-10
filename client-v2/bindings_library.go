package main

// 剧库推荐壳层绑定（V2-2 library）。
//
// 风格照 bindings_calendar.go：JSON 字符串返回，错误原样走 JS reject
// （native.Error 尾部自带 "[code]" 供前端 callGoRaw 提取）。
// Wails 通过 main.go `Bind: []interface{}{app}` 整结构体暴露本文件方法，
// 无需 app.go / main.go 注册 diff。
//
// 返回契约：与 v1 core_client.recommendations 的 data 一致（信封 data 平铺）：
//
//	{"items":[nativeDrama…],"hasMore":bool,"page":int,
//	 "warning"?:"…","localSearch":bool,"fresh":bool}
//
// 「全部」tab 前端也可直接用 App.Catalog(source, "", "", page, "library", force)
// 拿同样的形状（lane 会话隔离语义见 internal/native/library.go 头注释）。

import (
	"zhenguojian/client-v2/internal/native"
)

// Recommendations 剧库推荐一页：category 空=「全部」（catalog 分页，核心游标
// 在本壳层）；非空=题材个性化推荐（force 重新开局，more 走核心游标下一页）。
func (a *App) Recommendations(category string, force bool, more bool) (string, error) {
	data, err := native.Recommendations(category, force, more)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

// RecommendationPageReset 丢弃「全部」tab 的页码游标（刷新/切 tab 时前端调用；
// v1 reset_recommendation_page 同款，纯壳层状态）。
func (a *App) RecommendationPageReset() (string, error) {
	if err := native.ResetRecommendationPage(); err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"ok": true})
}
