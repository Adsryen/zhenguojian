package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	hongguoSearchTimeout      = 40 * time.Second
	hongguoSearchPageTimeout  = 12 * time.Second
	hongguoSearchPendingLimit = 32
	hongguoSearchNameCount    = 50
)

type hongguoSearchEntry struct {
	Dramas    []Drama
	Total     int
	Limited   bool
	Warning   string
	ExpiresAt time.Time
}

type hongguoSearchCall struct {
	done     chan struct{}
	changed  chan struct{}
	entry    hongguoSearchEntry
	snapshot hongguoSearchEntry
	revision uint64
	err      error
}

type hongguoSearchProgressKey struct{}

func reportHongguoSearchProgress(ctx context.Context, entry hongguoSearchEntry) {
	if progress, ok := ctx.Value(hongguoSearchProgressKey{}).(func(hongguoSearchEntry)); ok && len(entry.Dramas) > 0 {
		entry.Total = max(entry.Total, len(entry.Dramas))
		progress(entry)
	}
}

func hongguoSearchKeyword(keyword string) (string, error) {
	keyword = norm.NFKC.String(strings.TrimSpace(keyword))
	if keyword == "" || !utf8.ValidString(keyword) || utf8.RuneCountInString(keyword) > 80 || strings.IndexFunc(keyword, unicode.IsControl) >= 0 {
		return "", errors.New("请输入 1 至 80 个字符的搜索词")
	}
	return keyword, nil
}

func (downloader *Downloader) searchHongguoDramas(ctx context.Context, keyword string) (hongguoSearchEntry, error) {
	return downloader.searchHongguoDramasProgress(ctx, keyword, nil)
}

func (downloader *Downloader) searchHongguoDramasProgress(ctx context.Context, keyword string, progress func(hongguoSearchEntry)) (hongguoSearchEntry, error) {
	keyword, err := hongguoSearchKeyword(keyword)
	if err != nil {
		return hongguoSearchEntry{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, hongguoSearchTimeout)
	defer cancel()
	client := downloader.hongguoClient()
	for {
		if err := ctx.Err(); err != nil {
			return hongguoSearchEntry{}, err
		}
		client.mu.Lock()
		if cached, found := client.searches[keyword]; found && time.Now().Before(cached.ExpiresAt) {
			client.mu.Unlock()
			return cloneHongguoSearchEntry(cached), nil
		}
		if pending := client.searchPending[keyword]; pending != nil {
			client.mu.Unlock()
			entry, err := downloader.waitHongguoSearch(ctx, pending, progress)
			if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && ctx.Err() == nil {
				continue
			}
			return entry, err
		}
		if len(client.searchPending) >= hongguoSearchPendingLimit {
			client.mu.Unlock()
			return hongguoSearchEntry{}, errors.New("红果搜索请求较多，请稍后重试")
		}
		pending := &hongguoSearchCall{done: make(chan struct{}), changed: make(chan struct{})}
		client.searchPending[keyword] = pending
		client.mu.Unlock()
		fetchCtx := context.WithValue(ctx, hongguoSearchProgressKey{}, func(entry hongguoSearchEntry) {
			client.mu.Lock()
			pending.snapshot = cloneHongguoSearchEntry(entry)
			pending.revision++
			close(pending.changed)
			pending.changed = make(chan struct{})
			client.mu.Unlock()
			if progress != nil {
				progress(cloneHongguoSearchEntry(entry))
			}
		})
		entry, err := downloader.fetchHongguoSearch(fetchCtx, keyword)
		client.mu.Lock()

		if err == nil && entry.Warning == "" {
			if len(client.searches) >= 64 {
				client.searches = map[string]hongguoSearchEntry{}
			}
			entry.ExpiresAt = time.Now().Add(5 * time.Minute)
			client.searches[keyword] = entry
		}
		pending.entry, pending.err = entry, err
		delete(client.searchPending, keyword)
		close(pending.done)
		client.mu.Unlock()
		return cloneHongguoSearchEntry(entry), err
	}
}

func (downloader *Downloader) waitHongguoSearch(ctx context.Context, pending *hongguoSearchCall, progress func(hongguoSearchEntry)) (hongguoSearchEntry, error) {
	client := downloader.hongguoClient()
	var revision uint64
	for {
		client.mu.Lock()
		changed := pending.changed
		var update *hongguoSearchEntry
		if progress != nil && pending.revision > revision {
			entry := cloneHongguoSearchEntry(pending.snapshot)
			update = &entry
			revision = pending.revision
		}
		client.mu.Unlock()
		if update != nil {
			progress(*update)
		}
		select {
		case <-ctx.Done():
			return hongguoSearchEntry{}, ctx.Err()
		case <-pending.done:
			return cloneHongguoSearchEntry(pending.entry), pending.err
		case <-changed:
		}
	}
}

func (downloader *Downloader) fetchHongguoSearch(ctx context.Context, keyword string) (hongguoSearchEntry, error) {
	// 第一优先：H5 的 /api/search/result/。它和网页搜索返回同一份
	// searchList，但不经过 /search/<kw> 的 JS 风控；网页搜索在风控下
	// 会直接 500，以前就是这里把很多长剧名搜没了。
	api, apiErr := downloader.fetchHongguoSearchAPI(ctx, keyword)
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) && len(api.Dramas) > 0 {
			return hongguoSearchEntry{Dramas: api.Dramas, Total: api.Total, Limited: true, Warning: "搜索已到时限，已保留搜索接口结果，可重试"}, nil
		}
		return hongguoSearchEntry{}, err
	}
	apiDramas := api.Dramas
	if apiErr == nil {
		// 搜索接口在没有任何匹配时也会回一批“猜你喜欢”。按标题相关度
		// 过滤掉完全不沾边的条目，避免搜不存在的剧名却出来一堆别的剧。
		apiDramas = filterHongguoSearchDramas(apiDramas, keyword)
	}
	if len(apiDramas) > 0 {
		reportHongguoSearchProgress(ctx, hongguoSearchEntry{Dramas: apiDramas, Total: api.Total, Limited: api.Limited})
	}

	// 联想词仍保留：它能补一些搜索接口没给全的剧集/演员结果。
	names, namesErr := downloader.fetchHongguoSearchNames(ctx, keyword)
	if err := ctx.Err(); err != nil {
		partial := mergeHongguoSearchDramas(append([]Drama{}, apiDramas...), names)
		if errors.Is(err, context.DeadlineExceeded) && len(partial) > 0 {
			return hongguoSearchEntry{Dramas: partial, Total: max(api.Total, len(partial)), Limited: true, Warning: "搜索已到时限，已保留名称匹配结果，可重试"}, nil
		}
		return hongguoSearchEntry{}, err
	}
	if apiErr == nil {
		names = filterHongguoSearchDramas(names, keyword)
	}
	reportHongguoSearchProgress(ctx, hongguoSearchEntry{Dramas: mergeHongguoSearchDramas(append([]Drama{}, apiDramas...), names), Total: max(api.Total, len(apiDramas)+len(names)), Limited: true})

	// 搜索接口已经给结果时不再请求 HTML 搜索页：那条路 12 秒超时且
	// 经常被风控成 500，只会拖慢结果。接口失败才走网页兜底。
	var page hongguoSearchEntry
	var pageErr error
	if apiErr != nil {
		pageCtx, cancel := context.WithTimeout(ctx, hongguoSearchPageTimeout)
		page, pageErr = downloader.fetchHongguoSearchPage(pageCtx, keyword)
		cancel()
		if errors.Is(ctx.Err(), context.Canceled) {
			return hongguoSearchEntry{}, ctx.Err()
		}
	}
	if len(apiDramas)+len(page.Dramas)+len(names) == 0 && apiErr != nil && pageErr != nil && namesErr != nil {
		return hongguoSearchEntry{}, errors.Join(apiErr, pageErr, namesErr)
	}
	entry := hongguoSearchEntry{
		Dramas:  make([]Drama, 0, len(apiDramas)+len(page.Dramas)+len(names)),
		Total:   max(api.Total, page.Total),
		Limited: api.Limited || page.Limited,
	}
	for _, batch := range [][]Drama{apiDramas, page.Dramas, names} {
		entry.Dramas = mergeHongguoSearchDramas(entry.Dramas, batch)
	}
	reportHongguoSearchProgress(ctx, entry)
	seasonsLimited := downloader.completeHongguoSearchSeasons(ctx, keyword, &entry)
	if errors.Is(ctx.Err(), context.Canceled) {
		return hongguoSearchEntry{}, ctx.Err()
	}
	seasonsLimited = seasonsLimited || errors.Is(ctx.Err(), context.DeadlineExceeded)
	query := keyword
	sort.SliceStable(entry.Dramas, func(left, right int) bool {
		leftTitle, rightTitle := entry.Dramas[left].DisplayTitle(), entry.Dramas[right].DisplayTitle()
		leftRank, rightRank := hongguoTitleSearchRank(leftTitle, query), hongguoTitleSearchRank(rightTitle, query)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		leftBase, leftSeason, leftUnit, _ := hongguoSearchSeason(leftTitle)
		rightBase, rightSeason, rightUnit, _ := hongguoSearchSeason(rightTitle)
		if leftSeason > 0 && rightSeason > 0 && leftUnit == rightUnit && hongguoSearchText(leftBase) == hongguoSearchText(rightBase) {
			return leftSeason < rightSeason
		}
		return false
	})
	if len(apiDramas) == 0 {
		if pageErr != nil && len(names) > 0 {
			entry.Warning = "红果综合搜索暂不可用，已保留名称匹配结果，可重试"
		} else if namesErr != nil && len(page.Dramas) > 0 {
			entry.Warning = "红果名称检索暂不可用，结果可能缺少部分剧集，可重试"
		}
	}
	if seasonsLimited {
		if entry.Warning != "" {
			entry.Warning += "；"
		}
		entry.Warning += "部分季数暂未补齐，已保留当前结果，可重试"
	}
	entry.Limited = entry.Limited || (len(apiDramas) == 0 && (pageErr != nil || namesErr != nil)) || seasonsLimited
	if entry.Total < len(entry.Dramas) {
		entry.Total = len(entry.Dramas)
	}
	if len(entry.Dramas) == 0 {
		// 干净的“没有结果”，不要把 hasMore 提示当成失败原因。
		entry.Limited = false
		entry.Warning = ""
	}
	return entry, nil
}

func filterHongguoSearchDramas(dramas []Drama, query string) []Drama {
	if len(dramas) == 0 {
		return dramas
	}
	out := make([]Drama, 0, len(dramas))
	for _, drama := range dramas {
		if hongguoTitleSearchRank(drama.DisplayTitle(), query) <= 3 {
			out = append(out, drama)
		}
	}
	return out
}

func mergeHongguoSearchDramas(dramas, batch []Drama) []Drama {
	positions := make(map[string]int, len(dramas)+len(batch))
	for index, drama := range dramas {
		positions[drama.ID] = index
	}
	for _, drama := range batch {
		if drama.ID == "" {
			continue
		}
		if index, found := positions[drama.ID]; found {
			dramas[index] = mergeDramaMetadata(dramas[index], drama)
		} else {
			positions[drama.ID] = len(dramas)
			dramas = append(dramas, drama)
		}
	}
	return dramas
}

func (downloader *Downloader) fetchHongguoSearchNames(ctx context.Context, keyword string) ([]Drama, error) {
	records, err := downloader.fetchHongguoSuggestionRecords(ctx, keyword, hongguoSearchNameCount)
	if err != nil {
		return nil, err
	}
	dramas := make([]Drama, 0, len(records))
	for _, record := range records {

		if record.WordType != "short_play_name" || !hongguoNumericID.MatchString(mapString(record.VideoData, "series_id_str", "series_id")) {
			continue
		}
		drama := hongguoDramaFromAny(map[string]any{"video_data": record.VideoData, "name": record.Name, "keyword": record.Keyword}, "短剧")
		if drama.ID != "" && drama.DisplayTitle() != drama.SourceID {
			dramas = append(dramas, drama)
		}
	}
	return dramas, nil
}

func cloneHongguoSearchEntry(entry hongguoSearchEntry) hongguoSearchEntry {
	entry.Dramas = append([]Drama{}, entry.Dramas...)
	for index := range entry.Dramas {
		entry.Dramas[index].Tags = append([]string(nil), entry.Dramas[index].Tags...)
	}
	return entry
}

func hongguoSearchText(text string) string {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, norm.NFKC.String(text))
	if normalized == "" {
		return strings.ToLower(strings.TrimSpace(text))
	}
	return normalized
}

func hongguoTitleSearchRank(title, query string) int {
	words := strings.FieldsFunc(norm.NFKC.String(query), func(r rune) bool { return unicode.IsSpace(r) || unicode.IsPunct(r) })
	query = hongguoSearchText(query)
	title = hongguoSearchText(title)
	switch {
	case title == query:
		return 0
	case strings.HasPrefix(title, query):
		return 1
	case strings.Contains(title, query):
		return 2
	default:
		matches := 0
		for _, word := range words {
			if strings.Contains(title, hongguoSearchText(word)) {
				matches++
			}
		}
		if len(words) > 0 && matches == len(words) {
			return 3
		}
		return 4
	}
}

func (downloader *Downloader) fetchHongguoSearchAPI(ctx context.Context, keyword string) (hongguoSearchEntry, error) {
	// 网页 /search/<kw> 现在经常被字节的 JS 风控拦成 500；这个 H5 搜索
	// 接口返回同样的 searchList，而且不需要执行脚本。它是主路径，网页
	// 和联想词只作为兜底。
	params := url.Values{
		"query": {keyword},
		"page":  {"1"},
		"count": {"20"},
	}
	pageURL := hongguoBaseURL + "/api/search/result/?" + params.Encode()
	referer := hongguoBaseURL + "/search/" + url.PathEscape(keyword)
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(300 * time.Millisecond):
			case <-ctx.Done():
				return hongguoSearchEntry{}, ctx.Err()
			}
		}
		body, err := downloader.fetchProviderText(ctx, pageURL, referer)
		if err != nil {
			lastErr = err
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(body))
		decoder.UseNumber()
		var payload map[string]any
		if err := decoder.Decode(&payload); err != nil {
			lastErr = errors.New("红果搜索接口未返回有效数据")
			continue
		}
		rows := anyList(payload["searchList"])
		if message := mapString(payload, "message"); message != "" && len(rows) == 0 {
			lastErr = errors.New("红果搜索接口：" + message)
			continue
		}
		entry := hongguoSearchEntry{Dramas: make([]Drama, 0, len(rows))}
		seen := make(map[string]bool, len(rows))
		for _, row := range rows {
			drama := hongguoDramaFromAny(row, "短剧")
			if drama.ID == "" || seen[drama.ID] {
				continue
			}
			seen[drama.ID] = true
			entry.Dramas = append(entry.Dramas, drama)
		}
		entry.Total, _ = strconv.Atoi(mapString(payload, "totalCount"))
		entry.Limited, _ = payload["hasMore"].(bool)
		if entry.Total < len(entry.Dramas) {
			entry.Total = len(entry.Dramas)
		}
		return entry, nil
	}
	return hongguoSearchEntry{}, lastErr
}

func (downloader *Downloader) fetchHongguoSearchPage(ctx context.Context, keyword string) (hongguoSearchEntry, error) {
	pageURL := hongguoBaseURL + "/search/" + url.PathEscape(keyword)
	body, err := downloader.fetchProviderText(ctx, pageURL, hongguoBaseURL+"/")
	if err != nil {
		return hongguoSearchEntry{}, err
	}
	page := routerLoaderMap(parseRouterData(body), "search_(keyword)/page", "search_")
	rows, valid := page["searchList"].([]any)
	if page["isSuccess"] != true || !valid || mapString(page, "query") != keyword {
		return hongguoSearchEntry{}, errors.New("红果搜索未返回有效结果")
	}
	entry := hongguoSearchEntry{Dramas: make([]Drama, 0, len(rows))}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if len(nestedMap(row, "video_data")) == 0 {
			continue
		}
		drama := hongguoDramaFromAny(row, "短剧")
		if drama.ID != "" && !seen[drama.ID] {
			seen[drama.ID] = true
			entry.Dramas = append(entry.Dramas, drama)
		}
	}
	if len(rows) > 0 && len(entry.Dramas) == 0 {
		return hongguoSearchEntry{}, errors.New("红果搜索结果中没有可识别的剧集")
	}
	entry.Total, _ = strconv.Atoi(mapString(page, "totalCount"))

	entry.Limited = entry.Total > len(entry.Dramas)
	if entry.Total < len(entry.Dramas) {
		entry.Total = len(entry.Dramas)
	}
	return entry, nil
}
