package main

// watchstore / 日历壳层绑定（V2-2 依赖请求落地）。
// 行 JSON 契约 = v1 watch-library.json 行键（camelCase，watchstore 包
// FollowEntry/HistoryEntry.ToRowJSON 同源），前端 WatchRow/HistoryRow 逐键对齐。

import (
	"errors"

	"zhenguojian/client-v2/internal/native"
	"zhenguojian/client-v2/internal/watchstore"
)

func getWatchStore() (*watchstore.Store, error) {
	dir, err := native.DataDir()
	if err != nil {
		return nil, err
	}
	return watchstore.Open(dir), nil
}

func decodeWatchDrama(s string) (watchstore.Drama, error) {
	m, err := unmarshalObject(s)
	if err != nil {
		return watchstore.Drama{}, err
	}
	d := watchstore.Drama{}
	d.ID, _ = m["id"].(string)
	d.Title, _ = m["title"].(string)
	d.Cover, _ = m["cover"].(string)
	d.Source, _ = m["source"].(string)
	d.ReleaseStatus, _ = m["releaseStatus"].(string)
	d.OnlineDate, _ = m["onlineDate"].(string)
	if f, ok := m["episodes"].(float64); ok {
		d.Episodes = int(f)
	}
	for k, v := range m {
		switch k {
		case "id", "title", "cover", "source", "releaseStatus", "onlineDate", "episodes":
			continue
		}
		if d.Extra == nil {
			d.Extra = map[string]any{}
		}
		d.Extra[k] = v
	}
	if d.ID == "" {
		return d, errors.New("剧集信息不完整")
	}
	return d, nil
}

// Watchlist → JSON 数组：v1 follow 行 + 计算位（hasUpdate/badge/seenEpisode）。
func (a *App) Watchlist() (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	rows := make([]map[string]any, 0, 64)
	for _, e := range s.FollowList() {
		rows = append(rows, e.ToRowJSON())
	}
	return marshalJSON(rows)
}

// HistoryList → JSON 数组：v1 历史行（updated=epoch 秒；localPath 仅本地已下载）。
func (a *App) HistoryList() (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	rows := make([]map[string]any, 0, 64)
	for _, e := range s.History() {
		rows = append(rows, e.ToRowJSON())
	}
	return marshalJSON(rows)
}

// ToggleFollow 追/取消追（v1 语义：已追则删行）。
func (a *App) ToggleFollow(dramaJSON string) (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	d, err := decodeWatchDrama(dramaJSON)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"followed": s.ToggleFollow(d)})
}

// Reserve 预约上新（v1 互斥语义：预约同时取消追剧）。
func (a *App) Reserve(dramaJSON string) (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	d, err := decodeWatchDrama(dramaJSON)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"reserved": s.Reserve(d)})
}

// ObserveDrama 是 R23 的 Go 半边：v1 的后台追剧检查
// （main.py:776-860 _check_follows）逐条拿 `core.detail(drama)` 的最新数据喂给
// watchstore.observe_drama（watchstore.py:348-426），由它决定
// known/newEpisodes/newSeason/arrived 这些旗标怎么变。
//
// v2 侧 `Store.ObserveDrama` 本身早就移植了（store.go:1176，带单测），
// 但**全仓零调用方**——没有定时器、没有绑定、前端也不触发，
// 于是"预约/追剧"只是打了个标记，没人去发现它上线了。
// 这里只补最小的一块：**观察一条剧并把变化后的行回吐**，
// 节奏与遍历逻辑放在前端（见 services/followCheck.ts），理由是——
// 季号解析/同系列归并的权威实现已在 TS 有一份（services/series.ts 的 v1 series.py 端口），
// 若在 Go 再抄一份 `base_title/series_key/group/find_season/next_season`，
// 就变成同一个规格两处实现；那类重复正是本任务一直在防的（第 27/35 轮同源问题）。
//
// 入参是**完整** Drama JSON（v1 传的就是完整 API dict；见 store.go 注释）。
// 返回观察后的行（不存在该追剧记录时返回 {observed:false}，照 v1「follow is None → 不 emit」）。
func (a *App) ObserveDrama(dramaJSON string) (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	d, err := decodeWatchDrama(dramaJSON)
	if err != nil {
		return "", err
	}
	// 不额外开 Store API：现有 FollowList()/ToRowJSON() 就是行快照的唯一口径，
	// 新加一个 SnapshotRow 只会多一处可能漂移的真相。
	rowOf := func(id string) map[string]any {
		for _, e := range s.FollowList() {
			if e.Drama.ID == id {
				return e.ToRowJSON()
			}
		}
		return nil
	}
	before := rowOf(d.ID)
	s.ObserveDrama(d)
	after := rowOf(d.ID)
	return marshalJSON(map[string]any{
		// v1：observe_drama 对没追的剧直接 return（连 emit 都不发）。
		// 这里把"有没有这条追剧记录"如实报出去，前端据此不再瞎猜有没有生效。
		"observed": before != nil && after != nil,
		"changed":  !jsonObjectsEqual(before, after),
		"row":      after,
	})
}

func (a *App) CancelReservation(dramaID string) (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"ok": s.CancelReservation(dramaID)})
}

// SetNextSeason 把「下一季是另一部剧」这个入口记在旧季记录上
// （v1 main.py:2598 在季末提示查到下一季后调 watchstore.mark_next_season）。
//
// 澄清一条旧记录：任务笔记写的「Go 侧 SetNextSeason 绑定待补」只对了一半 ——
// Store.MarkNextSeason 连同单测（store_test.go:1151 TestMarkNextSeason）早就齐了，
// 真正缺的是这条对外绑定和前端调用点，所以 v1 的「记住入口」行为一直没通到用户。
//
// season 传 0：与 v1 `int(season or season_number(next_drama.title))` 一致，
// 由 store 从下一季标题推导。dramaID 不在追剧表时照 v1 返回 false，不谎报成功。
func (a *App) SetNextSeason(dramaID, nextDramaJSON string) (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	next, err := decodeWatchDrama(nextDramaJSON)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"marked": s.MarkNextSeason(dramaID, next, 0)})
}

func (a *App) RemoveFollow(dramaID string) (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	s.RemoveFollow(dramaID)
	return `{"ok":true}`, nil
}

// SaveWatch 记录观看进度（episode 1-based，v1 save_watch 同参）。
func (a *App) SaveWatch(dramaJSON string, episode, position, duration float64, finished bool) (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	d, err := decodeWatchDrama(dramaJSON)
	if err != nil {
		return "", err
	}
	s.SaveWatch(d.ID, d.Title, d.Cover, int(episode), position, duration, finished)
	return `{"ok":true}`, nil
}

func (a *App) RemoveHistory(dramaID string) (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	s.RemoveHistory(dramaID)
	return `{"ok":true}`, nil
}

func (a *App) ClearHistory() (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	s.ClearHistory()
	return `{"ok":true}`, nil
}

// Calendar（追剧日历）已迁至 bindings_calendar.go：日历属独立功能域，
// 且原实现把 gender 以 number 传核心（string 字段）会整请求失败，见该文件头注释。

// jsonObjectsEqual 只用于"观察前后行有没有变"的判断：
// 行本身就是要序列化给前端的 map，直接比 JSON 形态最省事，
// 也避免为了比较去手写一串字段白名单（那种清单一旦加字段就漏）。
func jsonObjectsEqual(a, b map[string]any) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	ja, e1 := marshalJSON(a)
	jb, e2 := marshalJSON(b)
	if e1 != nil || e2 != nil {
		return false
	}
	return ja == jb
}
