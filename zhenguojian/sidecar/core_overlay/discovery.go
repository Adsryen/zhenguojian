package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type discoveryInput struct {
	Action   string            `json:"action"`
	Drama    nativeDrama       `json:"drama"`
	Creator  map[string]any    `json:"creator"`
	Tab      int               `json:"tab"`
	SubTab   int               `json:"subTab"`
	Cursor   string            `json:"cursor"`
	Sort     string            `json:"sort"`
	Category string            `json:"category"`
	Source   string            `json:"source"`
	Offset   int               `json:"offset"`
	Filters  map[string]string `json:"filters"`
}

var creatorDetailCache = struct {
	sync.Mutex
	values map[string]creatorDetailEntry
}{values: map[string]creatorDetailEntry{}}

type creatorDetailEntry struct {
	value   map[string]any
	expires time.Time
}

func rememberCreatorDetail(id string, detail map[string]any) {
	value := map[string]any{"creators": []any{}, "recordNumber": mapString(nestedMap(detail, "record_info"), "record_number"), "heat": posterHeat(detail), "followers": formatHeat(float64(mapInt64(detail, "followed_cnt"))), "plays": formatHeat(float64(mapInt64(detail, "series_play_cnt")))}
	creators := []any{}
	for _, raw := range anyList(detail["celebrities"]) {
		row := nestedMap(raw)
		schema, _ := url.Parse(mapString(row, "schema"))
		uid := ""
		if schema != nil {
			uid = schema.Query().Get("uid")
		}
		name := mapString(row, "nickname", "name")
		if name == "" {
			continue
		}
		creators = append(creators, map[string]any{"id": mapString(row, "celebrity_id"), "userId": uid, "name": name, "avatar": mapString(row, "avatar"), "description": mapString(row, "intro"), "role": mapString(row, "sub_title", "role_name")})
	}
	durations := map[string]int64{}
	for _, raw := range anyList(detail["video_list"]) {
		video := nestedMap(raw)
		durations[mapString(video, "vid_index")] = mapInt64(video, "duration")
	}
	value["durations"] = durations
	primary := []any{}
	for _, raw := range anyList(detail["main_creator_users"]) {
		row := nestedMap(raw)
		base := nestedMap(row, "base_info")
		tags := nestedMap(row, "user_tag")
		uid := firstNonEmpty(mapString(row, "user_id"), mapString(base, "user_id"))
		name := mapString(base, "user_name")
		if uid == "" || name == "" {
			continue
		}
		role := ""
		for _, tag := range anyList(tags["sub_title_list"]) {
			if text := mapString(nestedMap(tag), "text"); text != "" {
				role = text
				break
			}
		}
		primary = append(primary, map[string]any{"id": uid, "userId": uid, "name": name, "avatar": mapString(base, "user_avatar"), "description": mapString(base, "description"), "role": role})
	}
	if len(primary) > 0 {
		creators = primary
		sort.SliceStable(creators, func(i, j int) bool {
			a := mapString(nestedMap(creators[i]), "role")
			b := mapString(nestedMap(creators[j]), "role")
			return a == "导演" && b != "导演"
		})
	}
	value["creators"] = creators
	creatorDetailCache.Lock()
	defer creatorDetailCache.Unlock()
	if len(creatorDetailCache.values) > 128 {
		creatorDetailCache.values = map[string]creatorDetailEntry{}
	}
	creatorDetailCache.values[id] = creatorDetailEntry{value, time.Now().Add(5 * time.Minute)}
}

func NativeDiscovery(payload string) string {
	var input discoveryInput
	var result any
	err := json.Unmarshal([]byte(payload), &input)
	nativeState.Lock()
	engine := nativeState.engine
	nativeState.Unlock()
	if err == nil && engine == nil {
		err = errors.New("本地核心尚未启动")
	}
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		switch input.Action {
		case "dramaCreators":
			source, id, valid := splitProviderDramaID(input.Drama.ID)
			if !valid || source != sourceHongguo {
				err = errors.New("剧集 ID 无效")
				break
			}
			_, err = engine.downloader.hongguoAppDetail(ctx, id)
			if err == nil {
				creatorDetailCache.Lock()
				entry := creatorDetailCache.values[id]
				creatorDetailCache.Unlock()
				if entry.value != nil && time.Now().Before(entry.expires) {
					result = entry.value
				} else {
					result = map[string]any{"creators": []any{}}
				}
			}
		case "creatorProfile":
			result, err = engine.discoveryProfile(ctx, input)
		case "creatorWorks":
			result, err = engine.discoveryWorks(ctx, input)
		case "browseFilters":
			result, err = engine.discoveryBrowseFilters(ctx, input)
		case "browseFiltered":
			result, err = engine.discoveryBrowse(ctx, input)
		default:
			err = errors.New("未知查询")
		}
	}
	envelope := map[string]any{"ok": err == nil}
	if err != nil {
		envelope["error"] = publicError(err).Error()
	} else {
		envelope["data"] = result
	}
	encoded, _ := json.Marshal(envelope)
	return string(encoded)
}

func (engine *nativeEngine) discoveryComment(ctx context.Context, uid string, payload any) (map[string]any, error) {
	if !hongguoNumericID.MatchString(uid) {
		return nil, errors.New("演职员 ID 无效")
	}
	if err := engine.downloader.ensureHongguoIdentity(ctx); err != nil {
		return nil, err
	}
	return engine.downloader.hongguoAppRequest(context.WithValue(ctx, hongguoCommentKey{}, true), http.MethodPost, "/novel/commentapi/user/profile/"+uid+"/v1", nil, payload)
}

type creatorTabsEntry struct {
	tabs    []any
	expires time.Time
}

var creatorTabsCache = struct {
	sync.Mutex
	values map[string]creatorTabsEntry
}{values: map[string]creatorTabsEntry{}}

func (engine *nativeEngine) discoveryCreatorTabs(ctx context.Context, uid string) ([]any, error) {
	if !hongguoNumericID.MatchString(uid) {
		return nil, errors.New("演职员 ID 无效")
	}
	creatorTabsCache.Lock()
	entry, found := creatorTabsCache.values[uid]
	creatorTabsCache.Unlock()
	if found && time.Now().Before(entry.expires) {
		return entry.tabs, nil
	}
	tabsResult, err := engine.discoveryComment(ctx, uid, map[string]any{"comment_source": 1506, "target_user_id": uid, "aid": 8662, "sort": "time_desc", "locate_album_id": "", "extra_param": map[string]any{"enter_source": "12"}})
	if err != nil {
		return nil, err
	}
	tabs := []any{}
	for _, raw := range anyList(nestedMap(tabsResult, "data")["tabs"]) {
		row := nestedMap(raw)
		name := mapString(row, "name")
		id := int(mapInt64(row, "tab_type"))
		if id <= 0 || (name != "作品" && name != "剧作") {
			continue
		}
		subs := []any{}
		for _, v := range anyList(row["sub_tabs"]) {
			sub := nestedMap(v)
			subs = append(subs, map[string]any{"id": mapInt64(sub, "tab_type"), "name": mapString(sub, "name"), "total": mapInt64(sub, "total")})
		}
		tabs = append(tabs, map[string]any{"id": id, "name": name, "total": mapInt64(row, "total"), "subTabs": subs})
	}

	creatorTabsCache.Lock()
	if len(creatorTabsCache.values) >= 256 {
		creatorTabsCache.values = map[string]creatorTabsEntry{}
	}
	creatorTabsCache.values[uid] = creatorTabsEntry{tabs: tabs, expires: time.Now().Add(10 * time.Minute)}
	creatorTabsCache.Unlock()
	return tabs, nil
}

func (engine *nativeEngine) discoveryWorkTab(ctx context.Context, uid string, tab, subTab int) (int, int, error) {
	tabs, err := engine.discoveryCreatorTabs(ctx, uid)
	if err != nil {
		return 0, 0, err
	}
	if len(tabs) == 0 {
		return 0, 0, errors.New("该演职员暂无公开作品")
	}
	if tab == 0 {
		first := nestedMap(tabs[0])
		tab = int(mapInt64(first, "id"))
		children := anyList(first["subTabs"])
		if len(children) > 0 {
			subTab = int(mapInt64(nestedMap(children[0]), "id"))
		}
	}
	for _, raw := range tabs {
		row := nestedMap(raw)
		if int(mapInt64(row, "id")) != tab {
			continue
		}
		children := anyList(row["subTabs"])
		if len(children) == 0 && subTab == 0 {
			return tab, subTab, nil
		}
		for _, child := range children {
			if int(mapInt64(nestedMap(child), "id")) == subTab {
				return tab, subTab, nil
			}
		}
		return 0, 0, errors.New("作品分类已更新，请重新打开个人作品页")
	}
	return 0, 0, errors.New("作品分类已更新，请重新打开个人作品页")
}

func (engine *nativeEngine) discoveryProfile(ctx context.Context, input discoveryInput) (any, error) {
	uid := mapString(input.Creator, "userId")
	profile := map[string]any{}
	for k, v := range input.Creator {
		profile[k] = v
	}
	tabs, err := engine.discoveryCreatorTabs(ctx, uid)
	if err != nil {
		return nil, err
	}
	details, profileErr := engine.downloader.hongguoAppRequest(ctx, http.MethodGet, "/reading/user/profile/get/v", url.Values{"user_id": {uid}}, nil)
	if profileErr == nil {
		user := nestedMap(details, "data", "ugc_user")
		base := nestedMap(user, "base_info")
		relation := nestedMap(user, "user_relation")
		if name := mapString(base, "user_name"); name != "" {
			profile["name"] = name
		}
		if intro := mapString(base, "description"); intro != "" {
			profile["description"] = intro
		}
		if avatar := mapString(base, "user_avatar"); avatar != "" {
			profile["avatar"] = avatar
		}
		for key, label := range map[string]string{"fans_num": "fans", "follow_user_num": "following", "recv_digged_count": "likes"} {
			if _, ok := relation[key]; ok {
				profile[label] = formatHeat(float64(mapInt64(relation, key)))
			}
		}
	}
	return map[string]any{"profile": profile, "tabs": tabs}, nil
}

func (engine *nativeEngine) discoveryWorks(ctx context.Context, input discoveryInput) (any, error) {
	uid := mapString(input.Creator, "userId")
	if len(input.Cursor) > 16384 {
		return nil, errors.New("作品分页标记无效")
	}
	sort := input.Sort
	if sort != "time_desc" && sort != "hot_desc" {
		sort = "time_desc"
	}
	tab, subTab, err := engine.discoveryWorkTab(ctx, uid, input.Tab, input.SubTab)
	if err != nil {
		return nil, err
	}
	body, err := engine.discoveryComment(ctx, uid, map[string]any{"comment_source": 1506, "target_user_id": uid, "sort": sort, "count": 18, "cursor": input.Cursor, "tab_type": tab, "sub_tab_type": subTab, "profile_user_type": 0, "locate_post_id": "", "extra_param": map[string]any{"is_video_tab_comment": "1"}})
	if err != nil {
		return nil, err
	}
	data := nestedMap(body, "data")
	items := []nativeDrama{}
	seen := map[string]bool{}
	for _, v := range anyList(data["data_list"]) {
		row := nestedMap(v)
		video := nestedMap(row, "video")
		common := nestedMap(video, "common")
		expand := nestedMap(video, "expand")
		stat := nestedMap(video, "stat")
		id := mapString(video, "video_id")
		if !hongguoNumericID.MatchString(id) {
			continue
		}
		raw := map[string]any{"series_id": id, "title": mapString(common, "title"), "series_cover": mapString(common, "cover"), "series_intro": mapString(common, "intro"), "episode_cnt": strconv.FormatInt(mapInt64(expand, "episode_cnt"), 10), "hot_score": strconv.FormatInt(mapInt64(stat, "hot"), 10), "series_play_cnt": strconv.FormatInt(mapInt64(stat, "play_cnt"), 10)}
		tags := []any{}
		for _, value := range anyList(expand["sub_title_list"]) {
			label := mapString(nestedMap(value), "text")
			if label != "" && !strings.Contains(label, "集") {
				tags = append(tags, label)
			}
		}
		raw["tags"] = tags
		drama := nativeNormalize(hongguoDramaFromAny(raw, ""))
		if drama.ID != "" && !seen[drama.ID] {
			items = append(items, drama)
			seen[drama.ID] = true
		}
	}
	page := nestedMap(data, "common_list_info")
	return map[string]any{"items": items, "cursor": mapString(page, "cursor"), "hasMore": page["has_more"] == true}, nil
}

func discoveryScene(genre string) (string, error) {
	switch genre {
	case "", "short_play":
		return "default", nil
	case "comic_series", "ai_series":
		return genre, nil
	}
	return "", errors.New("体裁无效")
}
// discoveryBrowseFilters 按站源分发筛选面板：红果沿用服务端 selector 面板，
// 其余站源用各自的目录分类（nativeCategories）给出一组「分类」芯片。
func (engine *nativeEngine) discoveryBrowseFilters(ctx context.Context, input discoveryInput) (any, error) {
	source := canonicalProviderSource(input.Source)
	if source != "" && source != sourceHongguo {
		categories, err := engine.nativeCategories(ctx, source, false)
		if err != nil {
			return nil, err
		}
		options := []any{}
		for _, item := range categories {
			options = append(options, map[string]any{"id": item.ID, "name": item.Name})
		}
		return map[string]any{"groups": []any{map[string]any{"key": "category", "name": "分类", "options": options}}}, nil
	}
	return engine.discoveryFilters(ctx, input.Category)
}

func (engine *nativeEngine) discoveryFilters(ctx context.Context, genre string) (any, error) {
	scene, err := discoveryScene(genre)
	if err != nil {
		return nil, err
	}
	body, err := engine.downloader.hongguoAppRequest(ctx, http.MethodPost, "/reading/distribution/category/landpage/v1/", nil, map[string]any{"req_scene": scene, "need_selector_panel": true, "req_type": "only_panel"})
	if err != nil {
		return nil, err
	}
	groups := []any{}
	for _, v := range anyList(nestedMap(body, "data")["selector_rows"]) {
		row := nestedMap(v)
		options := []any{}
		for _, value := range anyList(row["items"]) {
			option := nestedMap(value)
			if id := mapString(option, "selector_item_id"); id != "" {
				options = append(options, map[string]any{"id": id, "name": mapString(option, "show_name")})
			}
		}
		groups = append(groups, map[string]any{"key": mapString(row, "type"), "name": strings.TrimPrefix(mapString(row, "row_name"), "全部"), "options": options})
	}
	groups = append(groups, map[string]any{"key": "creation_status", "name": "状态", "options": []any{map[string]any{"id": "creation_status_0", "name": "已完结"}, map[string]any{"id": "creation_status_1", "name": "连载中"}}})
	return map[string]any{"groups": groups}, nil
}
// discoveryBrowseBySource 把筛选墙的分页语义映射到各站源目录：
// cursor 携带下一页页码（红果路径用 session_id，这里不复用）。
func (engine *nativeEngine) discoveryBrowseBySource(ctx context.Context, source string, input discoveryInput) (any, error) {
	category := strings.TrimSpace(input.Filters["category"])
	if !validNativeCategory(source, category) {
		return nil, errors.New("内容分类无效")
	}
	page := 1
	if cursor := strings.TrimSpace(input.Cursor); cursor != "" {
		if n, cerr := strconv.Atoi(cursor); cerr == nil && n > 0 {
			page = n
		}
	} else if input.Offset > 0 {
		page = 2
	}
	result, err := engine.nativeCatalog(ctx, nativeInput{Source: source, Category: category, Page: page})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"items":      result.Items,
		"hasMore":    result.HasMore,
		"nextOffset": input.Offset + len(result.Items),
		"sessionId":  strconv.Itoa(page + 1),
		"offset":     strconv.Itoa(input.Offset),
		"warning":    result.Warning,
	}, nil
}

func (engine *nativeEngine) discoveryBrowse(ctx context.Context, input discoveryInput) (any, error) {
	source := canonicalProviderSource(input.Source)
	if source != "" && source != sourceHongguo {
		return engine.discoveryBrowseBySource(ctx, source, input)
	}
	scene, err := discoveryScene(input.Filters["genre"])
	if err != nil {
		return nil, err
	}
	if input.Offset < 0 || input.Offset > 1000000 || len(input.Cursor) > 4096 {
		return nil, errors.New("筛选分页无效")
	}
	selected := map[string]any{}
	for _, key := range []string{"genre", "sort", "gender", "category_dim_theme", "category_dim_role", "category_dim_epoch", "online_time", "creation_status"} {
		values := []string{}
		if value := input.Filters[key]; value != "" {
			if len(value) > 128 {
				return nil, errors.New("筛选条件过长")
			}
			values = append(values, value)
		}
		selected[key] = values
	}
	reqType := 3
	if input.Offset > 0 {
		reqType = 2
	}
	body, err := engine.downloader.hongguoAppRequest(ctx, http.MethodPost, "/reading/distribution/category/landpage/v1/", nil, map[string]any{"req_scene": scene, "req_type": "only_content", "need_selector_panel": false, "limit": 18, "offset": input.Offset, "session_id": input.Cursor, "client_req_type": reqType, "select_items": selected, "filter_ids": ""})
	if err != nil {
		return nil, err
	}
	data := nestedMap(body, "data")
	items := []nativeDrama{}
	for _, v := range anyList(data["video_data"]) {
		drama := nativeNormalize(hongguoDramaFromAny(v, ""))
		if drama.ID != "" {
			items = append(items, drama)
		}
	}
	next := int(mapInt64(data, "next_offset"))
	hasMore := data["has_more"] == true
	if hasMore && next <= input.Offset {
		return nil, errors.New("筛选分页未前进")
	}
	return map[string]any{"items": items, "hasMore": hasMore, "nextOffset": next, "sessionId": mapString(data, "session_id"), "offset": strconv.Itoa(input.Offset)}, nil
}
