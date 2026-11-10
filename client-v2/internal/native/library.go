package native

// 剧库推荐门面：逐字对齐 v1 core_client.recommendations /
// reset_recommendation_page（zhenguojian/app/zhenguojian/core_client.py:278-327）。
//
// 语义基线（recommendation_panel.py:71-144 的 load() 走查结论）：
//   - category 为空（「全部」tab）：核心 recommendations action 只接受具体题材
//     （guoapp-src/native/core/provider_hongguo_recommendations.go:25-31 用
//     hongguoAppGenres 校验 genre），所以「全部」走分页 catalog——页码是真实
//     游标，「下一页」= 页码 +1；重发当前页正是 v1 修过的「下一页看起来坏掉」
//     坑（core_client.py:287-290 注释）。
//   - category 非空：action "recommendations"，payload 只有
//     category/force/command（command="more" 或空串，core_client.py:300-301），
//     无页码；游标（offset/sessionId/seen）由核心自持（app_recommendations.go:114），
//     且每次返回的是「累计合并表」而非增量（app_recommendations.go:105-122），
//     去重责任在壳层/前端（known_ids 过滤）。
//   - reset_recommendation_page 不对应任何核心 action（core_client.py:323-327
//     只动 _recommend_generation/_recommend_page 两个壳层变量）——v2 的「壳层」
//     就是本文件，页码游标同样放在这里。
//   - catalog lane 语义沿用 v1：session="owner:catalog:library"（laneSession），
//     剧库与搜索绝不共用 lane；source 固定 hongguo——推荐链路核心只实现红果
//     （app_recommendations.go:15/37 sourceHongguo），与 v1 默认源一致。

import (
	"sync"
)

// 推荐「全部」tab 的页码游标（v1 core_client._recommend_page/_recommend_generation）。
// generation 用于丢弃「重置后才返回」的在途请求对游标的过期提交。
var (
	recoMu   sync.Mutex
	recoPage int
	recoGen  int
)

// libraryCalls 是 Recommendations 的两条出站通道，单测替换用；
// 生产即 native.Catalog / native.Call。
type libraryCalls struct {
	catalog func(source, query, category string, page int, lane string, force bool) (map[string]any, error)
	action  func(action string, payload map[string]any) (map[string]any, error)
}

var libraryRecoCalls = libraryCalls{catalog: Catalog, action: Call}

// Recommendations 拉一页剧库推荐。category 为空走 catalog(lane="library") 分页，
// 页码严格递增（more=true 时取 max(1,当前游标)+1，绝不重发当前页）；
// 具体 category 走核心 recommendations action（个性化、无页码、more 由核心游标）。
// 返回 data 平铺对象：{"items":[…],"hasMore":…,"page":…,"warning"?…}。
// 错误为 *Error（Error() 尾部已带 "[code]"，v2 bindings 直接 return 给 JS）。
func Recommendations(category string, force, more bool) (map[string]any, error) {
	return recommendationsWith(libraryRecoCalls, category, force, more)
}

func recommendationsWith(calls libraryCalls, category string, force, more bool) (map[string]any, error) {
	if category == "" {
		// v1 core_client.py:291-299：先取游标快照，成功返回后才提交页码；
		// 期间若发生 Reset（generation 变化）则不提交，防止过期响应推进游标。
		recoMu.Lock()
		generation := recoGen
		page := 1
		if more {
			page = recoPage
			if page < 1 {
				page = 1
			}
			page++
		}
		recoMu.Unlock()

		result, err := calls.catalog("", "", "", page, "library", force)
		if err != nil {
			return nil, err
		}
		recoMu.Lock()
		if generation == recoGen {
			recoPage = page
		}
		recoMu.Unlock()
		return result, nil
	}
	command := ""
	if more {
		command = "more"
	}
	return calls.action("recommendations", map[string]any{
		"category": category,
		"force":    force,
		"command":  command,
	})
}

// ResetRecommendationPage 丢弃「全部」tab 的页码游标（换一批/切分类后调用，
// v1 core_client.py:323-327）。纯壳层状态，不触核心，不返回错误。
func ResetRecommendationPage() error {
	recoMu.Lock()
	defer recoMu.Unlock()
	recoGen++
	recoPage = 0
	return nil
}

// RecommendationPageForTest 读取当前「全部」tab 页码游标（仅测试可见）。
func RecommendationPageForTest() int {
	recoMu.Lock()
	defer recoMu.Unlock()
	return recoPage
}
