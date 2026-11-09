package main

// 「看过」标记绑定（范围 A：详情页 watched localStorage 键 → Go 真值迁移）。
// 真值 = internal/watchstore 的兄弟文件 seen-library.json（数据目录，
// v2-only：v1 不读不写它，watch-library.json 一字不动，v1 round-trip
// 兼容天然成立；为何不复用 follow 行 marked/read/known——见 store.go
// seen 区块头注释）。
//
// 契约（JSON，均返回 string；错误走第二返回值给前端 isBridgeDown 判别）：
//   SeenList()                → { "<dramaID>": epoch 秒 }（无标记 → {}）
//   ToggleSeen(dramaID)       → { "on": bool }（翻转并持久化，返回新状态）
//   ImportSeen(entriesJSON)   → { "added": int }（一次性 localStorage 迁移；
//                               值 ms（>1e11）自动归一秒；既有 id 不覆盖）

import (
	"errors"
)

// SeenList 返回全部 drama 级看过标记。
func (a *App) SeenList() (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	marks := s.SeenMarks()
	if marks == nil {
		marks = map[string]any{}
	}
	return marshalJSON(marks)
}

// ToggleSeen 翻转某剧看过标记并落盘。
func (a *App) ToggleSeen(dramaID string) (string, error) {
	if dramaID == "" {
		return "", errors.New("缺少剧目 id")
	}
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	on, err := s.ToggleSeen(dramaID)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"on": on})
}

// ImportSeen 合并 { dramaID: ts } JSON（ts 秒/毫秒均可）；已有 id 保留
// Go 侧值不覆盖，返回实际新增条数。前端迁移成功后自行清 localStorage 键。
func (a *App) ImportSeen(entriesJSON string) (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	added, err := s.ImportSeenJSON(entriesJSON)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"added": added})
}
