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

func (a *App) CancelReservation(dramaID string) (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"ok": s.CancelReservation(dramaID)})
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

// Calendar 红果上新日历：date=YYYYMMDD（空=今天），gender 2=全部 1=男频 0=女频。
// 透传核心 data（items 平铺）；错误中文原文走 JS reject。
func (a *App) Calendar(date string, offset float64, gender float64) (string, error) {
	payload := map[string]any{"date": date, "offset": int(offset)}
	if g := int(gender); g != 2 {
		payload["gender"] = g
	}
	data, err := native.Call("calendar", payload)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}
