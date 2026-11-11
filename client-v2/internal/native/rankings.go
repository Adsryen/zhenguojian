package native

// 排行榜侧栏门面：逐字对齐 v1 core_client.boards / rankings
//（zhenguojian/app/zhenguojian/core_client.py:275-276、329-330）。
//
// 协议基线（以代码为准，非猜测）：
//   - action "rankings"，payload {board,page,force}
//   	（core_client.py:329-330；应答平铺形状 {boardId,page,items:[{rank,
//     drama,metric?}],hasMore,totalPages,updatedText,fetchedAt,stale,
//     warning}，见 guoapp-src/native/core/app_rankings.go:60-94）。
//     核心校验页码 1..500，越界报「榜单页码无效」；v1 壳层只做
//     page=max(1,page)（main.py _load_rankings），本文件同款。
//   - 榜单候选列表不在 rankings 应答里（无 boards 键），来自独立 action
//     "rankingBoards"（app_runtime.go:457-462 返回 {items:[{id,source,
//     name,description}]}，且已按 nativeSourceAvailable(board.Source) 过滤
//     下架站源）。v1 侧 board_box 下拉即以此为数据源。
//   - 无状态：rankings 是只读快照（核心侧 5min TTL 缓存
//     rankingCacheTTL，provider_rankings.go），force=true 跳缓存重拉，
//     壳层不需要页码游标（区别于「全部」tab 推荐）。

import (
	"fmt"
)

// rankingsCalls 是 Rankings/RankingBoards 的出站通道，单测替换用；生产即 native.Call。
type rankingsCalls struct {
	action func(action string, payload map[string]any) (map[string]any, error)
}

var rankCalls = rankingsCalls{action: Call}

// rankingsInput 校验并组装 rankings payload（v1 core_client.py:329-330 的形状）。
func rankingsInput(board string, page int, force bool) (map[string]any, error) {
	if board == "" {
		return nil, fmt.Errorf("榜单 id 不能为空")
	}
	if page < 1 {
		page = 1 // v1 main.py _load_rankings：page = max(1, int(page or 1))
	}
	// 上界 500 交给核心校验（app_rankings.go「榜单页码无效」），壳层不重复实现。
	return map[string]any{"board": board, "page": page, "force": force}, nil
}

// Rankings 拉一页榜单，返回 data 平铺对象（键形状见头注释）。
// 错误为 *Error 或参数错误（bindings 原样 return 给 JS）。
func Rankings(board string, page int, force bool) (map[string]any, error) {
	payload, err := rankingsInput(board, page, force)
	if err != nil {
		return nil, err
	}
	return rankCalls.action("rankings", payload)
}

// RankingBoards 榜单候选列表：{"items":[{"id","source","name","description"}…]}
// （核心已按站源可用性过滤；v1 boards() 取 items 后填 board_box）。
func RankingBoards() (map[string]any, error) {
	return rankCalls.action("rankingBoards", nil)
}
