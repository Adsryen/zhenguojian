package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 红果「上新日历」：GET /reading/user/subscribe/list/v1/
// 不登录也能拿到未来几天的新剧/待上线剧；登录后 subscribe_data 会带预约状态。
const hongguoCalendarPath = "/reading/user/subscribe/list/v1/"

var hongguoCalendarDate = regexp.MustCompile(`^\d{8}$`)

type hongguoCalendarItem struct {
	SeriesID     string   `json:"seriesId"`
	Title        string   `json:"title"`
	Cover        string   `json:"cover"`
	Description  string   `json:"description,omitempty"`
	Category     string   `json:"category,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	EpisodeCnt   int      `json:"episodeCnt,omitempty"`
	PublishAt    int64    `json:"publishAt,omitempty"`
	PublishText  string   `json:"publishText,omitempty"`
	Hint         string   `json:"hint,omitempty"`
	Note         string   `json:"note,omitempty"`
	Heat         string   `json:"heat,omitempty"`
	ExpectedText string   `json:"expectedText,omitempty"`
	Online       bool     `json:"isOnline"`
	Reserved     bool     `json:"subscribed"`
	Badge        string   `json:"badge,omitempty"`
}

type hongguoCalendarPage struct {
	Items       []hongguoCalendarItem `json:"items"`
	Dates       []string              `json:"dates"`
	DefaultDate string                `json:"defaultDate"`
	HasMore     bool                  `json:"hasMore"`
	NextOffset  int                   `json:"nextOffset"`
	SessionID   string                `json:"sessionId"`
	FetchedAt   time.Time             `json:"fetchedAt"`
}

type hongguoCalendarEntry struct {
	page    hongguoCalendarPage
	err     error
	expires time.Time
}

var hongguoCalendarCache = struct {
	mu      sync.Mutex
	entries map[string]hongguoCalendarEntry
}{entries: map[string]hongguoCalendarEntry{}}

func (d *Downloader) hongguoCalendar(ctx context.Context, date string, offset int, gender string) (hongguoCalendarPage, error) {
	date = strings.TrimSpace(date)
	if date != "" && !hongguoCalendarDate.MatchString(date) {
		return hongguoCalendarPage{}, errors.New("日历日期格式无效")
	}
	if offset < 0 || offset > 1000 {
		return hongguoCalendarPage{}, errors.New("日历分页无效")
	}
	gender = strings.TrimSpace(gender)
	if gender == "" {
		gender = "2"
	}
	if gender != "0" && gender != "1" && gender != "2" {
		return hongguoCalendarPage{}, errors.New("日历性别筛选无效")
	}
	key := date + ":" + strconv.Itoa(offset) + ":" + gender
	hongguoCalendarCache.mu.Lock()
	if entry, ok := hongguoCalendarCache.entries[key]; ok && time.Now().Before(entry.expires) {
		hongguoCalendarCache.mu.Unlock()
		return entry.page, entry.err
	}
	hongguoCalendarCache.mu.Unlock()

	if err := d.ensureHongguoIdentity(ctx); err != nil {
		return hongguoCalendarPage{}, err
	}
	query := url.Values{
		"active_panel":            {"6"},
		"gender_type":             {gender},
		"need_calendar_schema":    {"true"},
		"need_personal_recommend": {"1"},
		"tab_style":               {"2"},
		"tab_type":                {"5"},
	}
	if date != "" {
		query.Set("target_date", date)
	}
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	result, err := d.hongguoAppRequest(ctx, http.MethodGet, hongguoCalendarPath, query, nil)
	page := hongguoCalendarPage{Items: []hongguoCalendarItem{}, Dates: []string{}}
	if err == nil {
		page = parseHongguoCalendar(result)
	}
	entry := hongguoCalendarEntry{page: page, err: err, expires: time.Now().Add(5 * time.Minute)}
	if err != nil {
		entry.expires = time.Now().Add(30 * time.Second)
	}
	hongguoCalendarCache.mu.Lock()
	if len(hongguoCalendarCache.entries) > 32 {
		hongguoCalendarCache.entries = map[string]hongguoCalendarEntry{}
	}
	hongguoCalendarCache.entries[key] = entry
	hongguoCalendarCache.mu.Unlock()
	return page, err
}

func parseHongguoCalendar(result map[string]any) hongguoCalendarPage {
	page := hongguoCalendarPage{Items: []hongguoCalendarItem{}, Dates: []string{}}
	data := nestedMap(result, "data")
	if data == nil {
		return page
	}
	schema := nestedMap(data, "calendar_schema")
	for _, row := range anyList(schema["date_list"]) {
		if text, ok := row.(string); ok && hongguoCalendarDate.MatchString(text) {
			page.Dates = append(page.Dates, text)
		}
	}
	page.DefaultDate = mapString(schema, "default_date")
	if len(page.Dates) == 0 && hongguoCalendarDate.MatchString(page.DefaultDate) {
		page.Dates = []string{page.DefaultDate}
	}
	page.HasMore = mapBool(data, "has_more")
	page.NextOffset = int(mapInt64(data, "next_offset"))
	page.SessionID = mapString(data, "session_id")
	for _, row := range anyList(data["subscribe_items"]) {
		item, _ := row.(map[string]any)
		if item == nil {
			continue
		}
		entry := calendarItemFromMap(item)
		if entry.SeriesID != "" && entry.Title != "" {
			page.Items = append(page.Items, entry)
		}
	}
	return page
}

func calendarItemFromMap(item map[string]any) hongguoCalendarItem {
	detail := nestedMap(item, "subscribe_data")
	if detail == nil {
		detail = map[string]any{}
	}
	out := hongguoCalendarItem{
		SeriesID:    firstNonEmpty(mapString(item, "recommend_group_id"), mapString(detail, "series_id_str")),
		Title:       firstNonEmpty(mapString(item, "name"), mapString(detail, "title"), mapString(detail, "series_title")),
		Cover:       firstNonEmpty(mapString(item, "cover"), mapString(detail, "cover"), mapString(detail, "series_cover")),
		Description: firstNonEmpty(mapString(item, "item_desc"), mapString(detail, "video_desc"), mapString(detail, "series_intro")),
		Category:    firstNonEmpty(mapString(item, "category"), mapString(detail, "category")),
		EpisodeCnt:  int(firstNonZero(mapInt64(detail, "episode_cnt"), mapInt64(item, "episode_cnt"))),
		Online:      mapBool(item, "is_online"),
		PublishText: mapString(item, "sub_info"),
		Badge:       mapString(nestedMap(item, "title_left_icon"), "text"),
	}
	if out.SeriesID == "" {
		out.SeriesID = mapString(detail, "series_id_str")
	}
	if out.EpisodeCnt == 0 {
		out.EpisodeCnt = int(mapInt64(item, "episode_cnt"))
	}
	publish := mapInt64(item, "schedule_publish_time")
	if publish <= 0 {
		publish = mapInt64(item, "expected_publish_time")
	}
	if publish > 0 {
		out.PublishAt = publish * 1000
	}
	out.ExpectedText = mapString(item, "expected_rec_text")
	// tags：预约人数/分类等短标签
	seen := map[string]bool{}
	addTag := func(text string) {
		text = strings.TrimSpace(text)
		if text != "" && !seen[text] && len(out.Tags) < 6 {
			seen[text] = true
			out.Tags = append(out.Tags, text)
		}
	}
	for _, row := range anyList(item["sub_title_list"]) {
		if tag, ok := row.(map[string]any); ok {
			addTag(mapString(tag, "content"))
		}
	}
	for _, row := range anyList(item["rec_tags"]) {
		if tag, ok := row.(map[string]any); ok {
			content := mapString(tag, "content")
			addTag(content)
			if out.Heat == "" && strings.Contains(content, "热度") {
				out.Heat = content
			}
		}
	}
	addTag(mapString(item, "expected_rec_text"))
	for _, row := range anyList(item["categories"]) {
		if text, ok := row.(string); ok {
			addTag(text)
		}
	}
	// 日历行副信息：在线显示热度，待上线显示预约人数；hint 用接口给的
	// “预计 X 月 X 日上线”文案，接口没给就按时间戳补一条。
	if out.Online {
		out.Note = out.Heat
	} else {
		out.Note = firstNonEmpty(out.ExpectedText, out.Heat)
		out.Hint = out.PublishText
		if out.Hint == "" && out.PublishAt > 0 {
			stamp := time.Unix(out.PublishAt/1000, 0)
			out.Hint = fmt.Sprintf("预计%d月%d日上线", int(stamp.Month()), stamp.Day())
		}
		if out.Hint == "" {
			out.Hint = "即将上线"
		}
	}
	followed, _ := detail["followed"].(bool)
	exist, _ := detail["book_exist"].(bool)
	out.Reserved = followed || exist
	return out
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func mapBool(m map[string]any, keys ...string) bool {
	for _, key := range keys {
		switch value := m[key].(type) {
		case bool:
			return value
		case string:
			return value == "true" || value == "1"
		case float64:
			return value != 0
		case json.Number:
			number, _ := value.Int64()
			return number != 0
		}
	}
	return false
}

func mapInt64(m map[string]any, keys ...string) int64 {
	for _, key := range keys {
		switch value := m[key].(type) {
		case int64:
			return value
		case int:
			return int64(value)
		case float64:
			return int64(value)
		case json.Number:
			number, _ := value.Int64()
			return number
		case string:
			number, _ := strconv.ParseInt(value, 10, 64)
			return number
		}
	}
	return 0
}
