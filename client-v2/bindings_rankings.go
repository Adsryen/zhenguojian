package main

// 排行榜壳层绑定（v1 侧栏功能补全，子代理 E）。
//
// 风格照 bindings_library.go：JSON 字符串原样透传核心应答，错误直接走 JS
// reject（native.Error 尾部自带 "[code]"，前端 callGoRaw 提取）。
// Wails 通过 main.go `Bind: []interface{}{app}` 整结构体暴露本文件方法，
// 无需 app.go / main.go 注册 diff。
//
// 返回契约（与核心平铺 data 一致，前端负责解析，见
// guoapp-src/native/core/app_rankings.go:60-94）：
//
//	Rankings      → {"boardId":"…","page":int,"items":[{"rank":int,
//	                  "drama":nativeDrama,"metric"?:"…"}…],"hasMore":bool,
//	                  "totalPages":int,"updatedText":"…","fetchedAt":"…",
//	                  "stale":bool,"warning"?:"…"}
//	RankingBoards → {"items":[{"id","source","name","description"}…]}
//	                  （核心已按站源可用性过滤，app_runtime.go:457-462）

import (
	"zhenguojian/client-v2/internal/native"
)

// Rankings 拉一页榜单：board 为 rankingBoards 里的 id（如 hongguo-hot）；
// page 从 1 起（壳层钳 <1→1，>500 由核心报错）；force 跳核心 5min TTL 缓存。
func (a *App) Rankings(board string, page int, force bool) (string, error) {
	data, err := native.Rankings(board, page, force)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

// RankingBoards 榜单候选列表（前端「切榜」数据源；v1 board_box QComboBox
// 同款语义）。前端在桥接不可用时回退常量表，见 LibraryView.vue RANK_BOARDS_FALLBACK。
func (a *App) RankingBoards() (string, error) {
	data, err := native.RankingBoards()
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}
