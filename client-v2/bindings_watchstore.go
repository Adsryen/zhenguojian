package main

// watchstore / 日历壳层绑定（V2-2 依赖请求落地）。
// 行 JSON 契约 = v1 watch-library.json 行键（camelCase，watchstore 包
// FollowEntry/HistoryEntry.ToRowJSON 同源），前端 WatchRow/HistoryRow 逐键对齐。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"zhenguojian/client-v2/internal/native"
	"zhenguojian/client-v2/internal/player"
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

// PlayerOpenLocal 播一个本地成品文件（v1 main.py:2609 open_local 的壳层入口）。
//
// ⚠ 路径校验在这里做，**不下沉给播放器**：前端的字符串能是什么都有可能，
// 而 mpv 拿到什么路径就会读什么文件。所以只放两种：
//   1. 合并成品 <下载根>/合并/<名>.mp4
//   2. 分集成品 <下载根>/<jobId>/media.mp4
// —— 也就是 v1 那两处成品位置（bindings_download.go:238-240 的同一口径）。
// 判定用 filepath.Rel 而不是 strings.HasPrefix：前者能识破 `..` 上跳，
// 后者会被 `/downloads/../windows/win.ini` 这种串骗过去。
func (a *App) PlayerOpenLocal(path string, resumeSeconds float64) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("本地文件已不存在：")
	}
	root, err := a.downloadRootDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := checkLocalPlayable(root, abs); err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() {
		// 句子照 v1（watch.py:641「本地文件已不存在：<文件名>」），尾巴 `[local_missing]`
		// 是本仓既有的错误码约定（api.ts callGoRaw 用 `/\[([a-z_]+)\]\s*$/` 剥成
		// ApiError.code）。带码的原因很具体：**英文界面不能显示这句中文**——
		// 界面按 code 用 i18n 组句子，这句只进日志。
		return "", fmt.Errorf("本地文件已不存在：%s [local_missing]", filepath.Base(abs))
	}
	if err := a.playerSvc.OpenLocal(a.ctx, player.LocalRequest{
		Path:     abs,
		Title:    filepath.Base(abs),
		ResumeMs: int64(resumeSeconds * 1000),
	}); err != nil {
		return "", err
	}
	return marshalJSON(a.playerSvc.State())
}

// checkLocalPlayable 把"能不能播这个路径"抽成纯函数，好让它能被单测直接打。
// 安全规则写在 PlayerOpenLocal 的注释里，这里只落判定：必须是下载根**之下**的文件。
//
// 两点不是可选的细节：
//   · 用 filepath.Rel 而不是 strings.HasPrefix——HasPrefix 会被 `..` 上跳骗过去
//     （`/downloads/../windows/win.ini` 前缀照样是 /downloads）；
//     反过来 Rel 也会认兄弟目录同名前缀（`/downloads-evil` vs `/downloads`），
//     所以判定的是"相对路径不能以 .. 开头"，不是"字符串是不是前缀"。
//   · 大小写：Windows 文件系统不区分大小写，`C:\Downloads` 与 `c:\downloads` 是同一个目录，
//     照字面比较会把自家成品拒掉；Linux 区分大小写，折叠又会把根外路径放进来的
//     （`/data/Downloads/x` 折叠后像 `/data/downloads/x`）。
//     所以折叠只按当前平台语义开，测试用 fold 参数把两种平台行为都能在这台 Linux 上跑出来。
func checkLocalPlayable(root, abs string) error {
	r, a := normalizeForContainment(root, abs, isCaseInsensitiveOS())
	rel, err := filepath.Rel(r, a)
	if err != nil {
		return fmt.Errorf("只允许播放下载目录下的成品文件（无法归一化路径：%s） [outside_root]", filepath.Base(abs))
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("只允许播放下载目录下的成品文件（拒绝越界路径：%s） [outside_root]", filepath.Base(abs))
	}
	return nil
}

// normalizeForContainment 把两个路径整理成"可以做包含判断"的形态。
// foldCase=true（Windows）时统一小写，Linux 保持大小写敏感。
func normalizeForContainment(root, abs string, foldCase bool) (string, string) {
	r, a := filepath.Clean(root), filepath.Clean(abs)
	if foldCase {
		return strings.ToLower(r), strings.ToLower(a)
	}
	return r, a
}

// isCaseInsensitiveOS 只有 Windows 按不区分大小写处理。
// 本机跑不了 Windows 单测，所以判定与折叠都做成参数，让测试能同时覆盖两种平台语义。
func isCaseInsensitiveOS() bool { return runtime.GOOS == "windows" }

// downloadRootDir 取核心 `downloadDirectory` 的绝对路径（与归档扫描、设置页同源）。
func (a *App) downloadRootDir() (string, error) {
	data, err := native.Call("downloadDirectory", nil)
	if err != nil {
		return "", err
	}
	dir, _ := data["directory"].(string)
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("还没有可用的下载目录")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return abs, nil
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

// SaveLocalWatch 记一条**本地文件**的观看进度（R22a 的第①件：写入侧）。
//
// 这是本轮最容易写错的一处，先把事实钉住：
// `Store.SaveLocalWatch`（store.go:1445）**早就移植好了**，含 v1 的去抖语义
// （同一 local_path + 位置/时长差都 <0.5s + 未看完 → 直接不写）。
// 但它**没有绑定、没有调用方、也没有一条单测**——
// 所以第三十八轮我写对账时说"有字段、会序列化，但没有任何代码写它"其实还轻了：
// 写入函数都在，缺的同样是"对外口子 + 调用点 + 测试"（和 R6 下一季、R23 观察器同一个形状）。
//
// 与 v1 的一处**已知不同**（不是我偷懒，是 store.go:1434-1444 注释里写明的取舍）：
// v1 的 `save_local_watch(path, …)` 会用文件名 stem 去 `download_dir()/episodes.json`
// 反查是哪部剧（watchstore.py:529-581 `_resolve_local`，含 `local:<stem>` 兜底）；
// v2 的 Store 版本**故意不带那个解析器**（episodes.json 属于另一个子系统），
// 改成由调用方把 (dramaID, title) 传进来。所以这条绑定的入参多一个 dramaJSON。
// 反查这件事在 v2 里归前端（它本来就知道自己点的是哪一行），
// 归到前端也就等于**不做 episodes.json 那套字符串匹配**。
func (a *App) SaveLocalWatch(dramaJSON, path string, position, duration float64, finished bool) (string, error) {
	s, err := getWatchStore()
	if err != nil {
		return "", err
	}
	d, err := decodeWatchDrama(dramaJSON)
	if err != nil {
		return "", err
	}
	// 本地进度只在"确实知道是哪部剧"时才记：
	// dramaID 空时 Store 那边第一行就 return（v1 同形），这里不假装写成功。
	if d.ID == "" {
		return "", errors.New("本地观看记录需要剧集信息")
	}
	s.SaveLocalWatch(d.ID, d.Title, path, position, duration, finished)
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
