package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 红果「新剧推荐」：GET /reading/bookapi/bookmall/cell/change/v1/
//
// 客户端（红果短剧.exe v1.2.21）用的是同一接口：
//
//	cell_id=7431550523368554558
//	cell_gender=2/1/0（全部/男频/女频）
//	selected_items=firstonlinetime_new（按上线时间）
//	change_type=1&limit=18&need_personal_recommend=1
//
// 第 2 页起把上一页返回的 session_id 原样带回，offset 用 next_offset。
const (
	hongguoNewSeriesPath   = "/reading/bookapi/bookmall/cell/change/v1/"
	hongguoNewSeriesCellID = "7431550523368554558"
	hongguoNewSeriesLimit  = 18
)

type hongguoNewSeriesItem struct {
	SeriesID    string   `json:"seriesId"`
	Title       string   `json:"title"`
	Cover       string   `json:"cover"`
	Description string   `json:"description,omitempty"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	EpisodeCnt  int      `json:"episodeCnt,omitempty"`
	Score       string   `json:"score,omitempty"`
	Heat        string   `json:"heat,omitempty"`
	PlayCount   int64    `json:"playCount,omitempty"`
	Badge       string   `json:"badge,omitempty"`
	SubTitle    string   `json:"subTitle,omitempty"`
}

type hongguoNewSeriesPage struct {
	Items      []hongguoNewSeriesItem `json:"items"`
	HasMore    bool                   `json:"hasMore"`
	NextOffset int                    `json:"nextOffset"`
	SessionID  string                 `json:"sessionId"`
	FetchedAt  time.Time              `json:"fetchedAt"`
}

type hongguoNewSeriesEntry struct {
	page    hongguoNewSeriesPage
	err     error
	expires time.Time
}

var hongguoNewSeriesCache = struct {
	mu      sync.Mutex
	entries map[string]hongguoNewSeriesEntry
}{entries: map[string]hongguoNewSeriesEntry{}}

func (d *Downloader) hongguoNewSeries(ctx context.Context, gender string, offset int, cursor string) (hongguoNewSeriesPage, error) {
	gender = strings.TrimSpace(gender)
	if gender == "" {
		gender = "2"
	}
	if gender != "0" && gender != "1" && gender != "2" {
		return hongguoNewSeriesPage{}, errors.New("新剧性别筛选无效")
	}
	if offset < 0 || offset > 1000 {
		return hongguoNewSeriesPage{}, errors.New("新剧分页无效")
	}
	cursor = strings.TrimSpace(cursor)
	if len(cursor) > 4096 || strings.ContainsAny(cursor, "\r\n\x00") {
		return hongguoNewSeriesPage{}, errors.New("新剧分页标记无效")
	}
	first := offset == 0 && cursor == ""
	key := gender
	if first {
		hongguoNewSeriesCache.mu.Lock()
		if entry, ok := hongguoNewSeriesCache.entries[key]; ok && time.Now().Before(entry.expires) {
			hongguoNewSeriesCache.mu.Unlock()
			return entry.page, entry.err
		}
		hongguoNewSeriesCache.mu.Unlock()
	}

	if err := d.ensureHongguoIdentity(ctx); err != nil {
		return hongguoNewSeriesPage{}, err
	}
	query := url.Values{
		"cell_id":                 {hongguoNewSeriesCellID},
		"cell_gender":             {gender},
		"change_type":             {"1"},
		"limit":                   {strconv.Itoa(hongguoNewSeriesLimit)},
		"offset":                  {strconv.Itoa(offset)},
		"need_personal_recommend": {"1"},
		"selected_items":          {"firstonlinetime_new"},
	}
	if cursor != "" {
		query.Set("session_id", cursor)
	}
	result, err := d.hongguoAppRequest(ctx, http.MethodGet, hongguoNewSeriesPath, query, nil)
	page := hongguoNewSeriesPage{Items: []hongguoNewSeriesItem{}}
	if err == nil {
		page = parseHongguoNewSeries(result, offset)
	}
	if first {
		entry := hongguoNewSeriesEntry{page: page, err: err, expires: time.Now().Add(3 * time.Minute)}
		if err != nil {
			entry.expires = time.Now().Add(30 * time.Second)
		}
		hongguoNewSeriesCache.mu.Lock()
		if len(hongguoNewSeriesCache.entries) > 8 {
			hongguoNewSeriesCache.entries = map[string]hongguoNewSeriesEntry{}
		}
		hongguoNewSeriesCache.entries[key] = entry
		hongguoNewSeriesCache.mu.Unlock()
	}
	return page, err
}

func parseHongguoNewSeries(result map[string]any, offset int) hongguoNewSeriesPage {
	page := hongguoNewSeriesPage{Items: []hongguoNewSeriesItem{}, NextOffset: offset}
	data := nestedMap(result, "data")
	if data == nil {
		return page
	}
	view := nestedMap(data, "cell_view")
	rows := anyList(view["video_data"])
	if len(rows) == 0 {
		rows = anyList(data["video_data"])
	}
	page.HasMore = mapBool(data, "has_more")
	page.NextOffset = int(mapInt64(data, "next_offset"))
	page.SessionID = mapString(data, "session_id")
	if page.NextOffset == 0 && len(rows) > 0 {
		page.NextOffset = offset + len(rows)
	}
	if page.HasMore && page.NextOffset <= offset {
		// 游标没有前进就当作没有下一页，避免前端无限请求同一页。
		page.HasMore = false
	}
	seen := map[string]bool{}
	for _, row := range rows {
		item, _ := row.(map[string]any)
		if item == nil {
			continue
		}
		entry := newSeriesItemFromMap(item)
		if entry.SeriesID == "" || entry.Title == "" || seen[entry.SeriesID] {
			continue
		}
		seen[entry.SeriesID] = true
		page.Items = append(page.Items, entry)
	}
	return page
}

func newSeriesItemFromMap(item map[string]any) hongguoNewSeriesItem {
	out := hongguoNewSeriesItem{
		SeriesID:    firstNonEmpty(mapString(item, "series_id"), mapString(item, "recommend_group_id")),
		Title:       firstNonEmpty(mapString(item, "title"), mapString(item, "series_title")),
		Cover:       firstNonEmpty(mapString(item, "cover"), mapString(item, "horiz_cover")),
		Description: firstNonEmpty(mapString(item, "video_desc"), mapString(item, "description")),
		EpisodeCnt:  int(mapInt64(item, "episode_cnt")),
		Score:       mapString(item, "score"),
		PlayCount:   mapInt64(item, "play_cnt"),
		SubTitle:    mapString(item, "sub_title"),
	}
	if icon := nestedMap(item, "tag_info"); icon != nil {
		out.Badge = mapString(icon, "text")
	}
	// sub_title_list 里 data_type 27 是热度，3 是题材；其余（第N季/今日上新）只作展示。
	seenTag := map[string]bool{}
	addTag := func(text string) {
		text = strings.TrimSpace(text)
		if text != "" && !seenTag[text] && len(out.Tags) < 6 {
			seenTag[text] = true
			out.Tags = append(out.Tags, text)
		}
	}
	for _, row := range anyList(item["sub_title_list"]) {
		tag, _ := row.(map[string]any)
		if tag == nil {
			continue
		}
		content := strings.TrimSpace(mapString(tag, "content"))
		if content == "" {
			continue
		}
		switch int(mapInt64(tag, "data_type")) {
		case 27:
			if out.Heat == "" {
				out.Heat = content
			}
		case 3:
			if out.Category == "" {
				out.Category = content
			}
			addTag(content)
		default:
			if out.Category == "" && !strings.Contains(content, "季") && !strings.Contains(content, "集") && !strings.Contains(content, "上新") && !strings.Contains(content, "新剧") {
				out.Category = content
				addTag(content)
			}
		}
	}
	// 卡片副行要的是 3 个题材标签；category_schema 是完整题材列表，
	// 比 sub_title_list 更全（后者常只给一个）。
	if raw := mapString(item, "category_schema"); raw != "" && strings.HasPrefix(strings.TrimSpace(raw), "[") {
		var schema []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(raw), &schema) == nil {
			for _, entry := range schema {
				addTag(entry.Name)
			}
		}
	}
	if out.Category == "" && out.SubTitle != "" {
		if index := strings.Index(out.SubTitle, "·"); index > 0 {
			out.Category = strings.TrimSpace(out.SubTitle[:index])
			addTag(out.Category)
		}
	}
	return out
}
