package core

// 柚子（youzidj.com）站源实现：服务端渲染 HTML 的 AI 成人短剧站。
// 目录 = article.drama-card 卡片（链接/标题/封面/角标/标签），分页 = /{栏目}/page/N/
// 并用 <link rel="next"> 判断续页；搜索 = /search/?q=（站方忽略 page 参数，单页）；
// 详情两种形态：
//   - 单视频页 /ai-adult/{id}/、/ai-face-swap/{id}/：ld+json VideoObject 内嵌
//     /media/{videoUUID}/{versionUUID}/master.m3u8，章节键即 videoUUID；
//   - 剧集季页 /dramas/{slug}/season-{N}/：ld+json TVSeason.episode[].url 列出分集页，
//     章节键为分集完整路径（…-episode-K），取流时再抓分集页提取 videoUUID。
// HTML 里的 media 地址只是占位，真正可播必须走 provider_youzi_client.go 的
// bootstrap→/api/playback 凭证流；凭证与 Cookie 由 providerMediaCredentials 下发。
// 参照 provider_heitao.go / provider_guipian.go / provider_jinguoduanju.go 的同族签名。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const (
	youziPlayScheme      = "youzi-play://"
	youziDefaultCategory = "dramas"
	youziPageSize        = 24
)

var youziCategories = []nativeCategory{
	{ID: "dramas", Name: "AI短剧"},
	{ID: "ai-face-swap", Name: "AI换脸"},
	{ID: "ai-adult", Name: "AI成人视频"},
	{ID: "ai-adult/real", Name: "AI真人"},
	{ID: "ai-adult/anime", Name: "AI动漫"},
	{ID: "ai-adult/remix", Name: "AI魔改"},
}

var (
	reYouziNextLink   = regexp.MustCompile(`(?i)<link[^>]+rel="next"`)
	reYouziEpisodes   = regexp.MustCompile(`更新至\s*(\d{1,6})\s*集`)
	reYouziEpisodeNum = regexp.MustCompile(`-episode-([1-9][0-9]{0,5})$`)
	reYouziDuration   = regexp.MustCompile(`^PT(?:(\d{1,3})H)?(?:(\d{1,3})M)?(?:([\d.]{1,9})S)?$`)
)

func youziCategoryIDs() []string {
	ids := make([]string, 0, len(youziCategories))
	for _, item := range youziCategories {
		ids = append(ids, item.ID)
	}
	return ids
}

func validYouziCategory(category string) bool {
	if category == "" {
		return true
	}
	for _, item := range youziCategories {
		if item.ID == category {
			return true
		}
	}
	return false
}

// youziCategoryName 由 sourceID 首段推导栏目名（供 CategoryName 展示）。
func youziCategoryName(sourceID string) string {
	head, _, _ := strings.Cut(sourceID, "/")
	switch head {
	case "dramas":
		return "AI短剧"
	case "ai-face-swap":
		return "AI换脸"
	case "ai-adult":
		// 子分类段（real/anime/remix…）优先命中注册栏目名；纯 id 段自然落空回默认。
		if rest := strings.TrimPrefix(sourceID, "ai-adult/"); rest != "" {
			sub, _, _ := strings.Cut(rest, "/")
			for _, item := range youziCategories {
				if item.ID == "ai-adult/"+sub {
					return item.Name
				}
			}
		}
		return "AI成人视频"
	}
	return "柚子"
}

func (d *Downloader) fetchYouziCategories(ctx context.Context) ([]nativeCategory, error) {
	return append([]nativeCategory{}, youziCategories...), nil
}

// fetchYouziCatalogPage 目录页/搜索页抓取。query 非空走搜索（单页，page>1 返回空）。
func (d *Downloader) fetchYouziCatalogPage(ctx context.Context, page int, category, query string) ([]Drama, bool, error) {
	if page < 1 || page > 100000 {
		return nil, false, errors.New("柚子目录页码无效")
	}
	var address string
	if strings.TrimSpace(query) != "" {
		if page > 1 {
			return []Drama{}, false, nil
		}
		address = youziSite() + "/search/?q=" + url.QueryEscape(strings.TrimSpace(query))
	} else {
		if !validYouziCategory(category) {
			return nil, false, errors.New("柚子内容分类无效")
		}
		if category == "" {
			category = youziDefaultCategory
		}
		if page > 1 {
			address = fmt.Sprintf("%s/%s/page/%d/", youziSite(), category, page)
		} else {
			address = youziSite() + "/" + category + "/"
		}
	}
	body, finalURL, err := d.youziFetchText(ctx, address, "")
	if err != nil {
		if page > 1 && errors.Is(err, errYouziNotFound) {
			return []Drama{}, false, nil
		}
		return nil, false, err
	}
	items := parseYouziCards(body, finalURL)
	if len(items) == 0 {
		return []Drama{}, false, nil
	}
	hasMore := query == "" && reYouziNextLink.MatchString(body)
	return items, hasMore, nil
}

// parseYouziCards 从目录/搜索页正文解析卡片。要求链接落在合法详情路径上。
func parseYouziCards(body, finalURL string) []Drama {
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}
	var items []Drama
	seen := map[string]bool{}
	for _, card := range providerHTMLNodes(document, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "article" &&
			providerHTMLClass(node, "drama-card") && providerHTMLClass(node, "pomelo-drama-card")
	}) {
		link := providerHTMLFirstClass(card, "home-drama-card-link")
		if link == nil {
			continue
		}
		href := providerHTMLAttr(link, "href")
		path := youziCardPath(href)
		if path == "" || !validYouziSourceID(path) || seen[path] {
			continue
		}
		seen[path] = true
		title := strings.TrimSpace(providerHTMLAttr(providerHTMLFirstClass(card, "drama-card-title"), "title"))
		if title == "" {
			title = strings.TrimSpace(providerHTMLText(providerHTMLFirstClass(card, "drama-card-title")))
		}
		if title == "" {
			label := strings.TrimSpace(providerHTMLAttr(link, "aria-label"))
			title = strings.TrimSpace(strings.TrimPrefix(label, "查看详情："))
		}
		if title == "" {
			continue
		}
		cover := youziCardCover(card, finalURL)
		drama := Drama{
			ID: providerDramaID(sourceYouzi, path), Source: sourceYouzi, SourceID: path,
			Title: truncate(title, 512), Name: truncate(title, 512),
			ChannelName: "柚子", CategoryName: youziCategoryName(path),
			Cover: cover, CoverURL: cover,
		}
		if badge := providerHTMLFirstClass(card, "home-card-episodes-long"); badge != nil {
			drama.Remark = truncate(strings.TrimSpace(providerHTMLText(badge)), 128)
			if match := reYouziEpisodes.FindStringSubmatch(drama.Remark); len(match) > 1 {
				if count, convErr := strconv.Atoi(match[1]); convErr == nil && count > 0 {
					drama.TotalEpisode = count
					drama.EpisodeCount = count
				}
			}
		} else if badge := providerHTMLFirstClass(card, "home-card-time-long"); badge != nil {
			drama.Remark = truncate(strings.TrimSpace(providerHTMLText(badge)), 128)
		}
		for _, tag := range providerHTMLNodes(card, func(node *html.Node) bool {
			return node.Type == html.ElementNode && node.Data == "a" && providerHTMLClass(node, "drama-card-tag")
		}) {
			name := strings.TrimSpace(providerHTMLText(tag))
			if name != "" {
				drama.Tags = append(drama.Tags, truncate(name, 64))
			}
		}
		items = append(items, drama)
	}
	return items
}

// youziCardPath 提取卡片 href 的详情路径（去首尾斜杠），仅接受站内绝对/相对地址。
func youziCardPath(href string) string {
	if href == "" {
		return ""
	}
	address, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return ""
	}
	path := strings.Trim(address.Path, "/")
	if address.IsAbs() && !youziHostAllowed(address.String()) {
		return ""
	}
	return path
}

// youziCardCover 卡片封面：img 的 data-src/src（srcset 不参与），解析为绝对地址。
func youziCardCover(card *html.Node, finalURL string) string {
	var raw string
	for _, node := range providerHTMLNodes(card, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "img"
	}) {
		if value := strings.TrimSpace(providerHTMLAttr(node, "data-src")); value != "" {
			raw = value
			break
		}
		if value := strings.TrimSpace(providerHTMLAttr(node, "src")); value != "" && !strings.HasPrefix(value, "data:") {
			raw = value
			break
		}
	}
	if raw == "" {
		return ""
	}
	cover := youziResolveURL(finalURL, raw)
	if cover == "" || !strings.HasPrefix(cover, "http://") && !strings.HasPrefix(cover, "https://") {
		return ""
	}
	return cover
}

// youziResolveURL 站内相对地址解析：与 resolveProviderURL 的差别是不做
// http→https 强升级——站点自身是什么协议就跟什么协议，避免把凭证/封面
// 解析到与站点不同源的 URL 后过不了 youziHostAllowed 的同源校验。
func youziResolveURL(baseURL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "//") {
		if base, err := url.Parse(baseURL); err == nil && base.Scheme != "" {
			return base.Scheme + ":" + ref
		}
		return "https:" + ref
	}
	address, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	if address.IsAbs() {
		return address.String()
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return ref
	}
	return base.ResolveReference(address).String()
}

// fetchYouziDetail 详情页抓取：单视频（1 章节，键=videoUUID）或剧集季页（N 章节，键=分集路径）。
func (d *Downloader) fetchYouziDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	sourceID = strings.Trim(strings.TrimSpace(sourceID), "/")
	if !validYouziSourceID(sourceID) {
		return Drama{}, nil, errors.New("柚子作品标识无效")
	}
	pageURL := youziDetailURL(sourceID)
	body, finalURL, err := d.youziFetchText(ctx, pageURL, youziSite()+"/")
	if err != nil {
		if errors.Is(err, errYouziNotFound) {
			return Drama{}, nil, errors.New("柚子作品不存在或已下线")
		}
		return Drama{}, nil, err
	}
	_ = finalURL
	entities := youziLDEntities(body)
	if video := youziFindEntity(entities, "VideoObject"); video != nil {
		return d.youziSingleVideoDetail(sourceID, pageURL, video)
	}
	if season := youziFindEntity(entities, "TVSeason"); season != nil {
		return d.youziSeasonDetail(sourceID, pageURL, season, body)
	}
	return Drama{}, nil, errors.New("柚子详情页未返回作品信息，请刷新重试")
}

func (d *Downloader) youziSingleVideoDetail(sourceID, pageURL string, video map[string]any) (Drama, []Chapter, error) {
	title := truncate(youziEntityString(video, "name"), 512)
	if title == "" {
		return Drama{}, nil, errors.New("柚子详情页缺少作品标题")
	}
	contentURL := youziEntityFirstString(video, "contentUrl")
	videoID := ""
	if match := reYouziMediaVideo.FindStringSubmatch(contentURL); len(match) > 1 {
		videoID = match[1]
	}
	if videoID == "" {
		return Drama{}, nil, errors.New("柚子详情页未提供播放信息")
	}
	description := truncate(youziEntityString(video, "description"), 12000)
	cover := youziEntityFirstString(video, "thumbnailUrl")
	drama := Drama{
		ID: providerDramaID(sourceYouzi, sourceID), Source: sourceYouzi, SourceID: sourceID,
		Title: title, Name: title, Desc: description, Intro: description,
		Cover: cover, CoverURL: cover,
		ChannelName: "柚子", CategoryName: youziCategoryName(sourceID),
		Tags:         youziEntityStringList(video, "genre"),
		TotalEpisode: 1, EpisodeCount: 1,
	}
	if seconds := youziParseDuration(youziEntityString(video, "duration")); seconds > 0 {
		drama.Duration = seconds
	}
	if date := strings.TrimSpace(youziEntityString(video, "uploadDate")); date != "" {
		drama.OnlineDate = strings.SplitN(date, "T", 2)[0]
	}
	chapter := Chapter{
		ID: providerChapterID(sourceYouzi, sourceID, videoID), Source: sourceYouzi,
		Title:          title,
		VideoURL:       youziPlayScheme + videoID,
		CurrentEpisode: rawEpisode(1),
		PageURL:        pageURL,
		Referer:        youziSite() + "/",
	}
	return drama, []Chapter{chapter}, nil
}

func (d *Downloader) youziSeasonDetail(sourceID, pageURL string, season map[string]any, body string) (Drama, []Chapter, error) {
	title := ""
	if series, _ := youziEntityMap(season, "partOfSeries"); series != nil {
		title = youziEntityString(series, "name")
	}
	if title == "" {
		title = youziEntityString(season, "name")
	}
	title = truncate(title, 512)
	if title == "" {
		return Drama{}, nil, errors.New("柚子详情页缺少作品标题")
	}
	cover := youziEntityFirstString(season, "image")
	description := truncate(youziEntityString(season, "description"), 12000)

	seen := map[string]bool{}
	var chapters []Chapter
	for _, raw := range youziEntityList(season, "episode") {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		path := youziCardPath(youziEntityFirstString(item, "url"))
		number := youziEntityNumber(item, "episodeNumber")
		key, valid := youziEpisodeKey(sourceID, path)
		if !valid || seen[key] {
			continue
		}
		seen[key] = true
		if number <= 0 {
			number = len(chapters) + 1
		}
		episodeTitle := truncate(youziEntityString(item, "name"), 512)
		if episodeTitle == "" {
			episodeTitle = fmt.Sprintf("第 %d 集", number)
		}
		chapters = append(chapters, Chapter{
			ID: providerChapterID(sourceYouzi, sourceID, key), Source: sourceYouzi,
			Title: episodeTitle, VideoURL: youziPlayScheme + key, CurrentEpisode: rawEpisode(number),
			PageURL: youziDetailURL(key), Referer: youziSite() + "/",
		})
	}
	if len(chapters) == 0 {
		chapters = youziEpisodesFromHTML(sourceID, body, seen)
	}
	if len(chapters) == 0 {
		return Drama{}, nil, errors.New("柚子详情页未返回分集列表")
	}
	drama := Drama{
		ID: providerDramaID(sourceYouzi, sourceID), Source: sourceYouzi, SourceID: sourceID,
		Title: title, Name: title, Desc: description, Intro: description,
		Cover: cover, CoverURL: cover,
		ChannelName: "柚子", CategoryName: "AI短剧",
		TotalEpisode: len(chapters), EpisodeCount: len(chapters),
	}
	if count := youziEntityNumber(season, "numberOfEpisodes"); count > 0 && count != len(chapters) {
		drama.TotalEpisode = count
	}
	return drama, chapters, nil
}

// youziEpisodeKey 校验分集路径确属该季页：sourceID + "-episode-K"。
func youziEpisodeKey(sourceID, path string) (string, bool) {
	if path == "" || !strings.HasPrefix(path, sourceID+"-episode-") {
		return "", false
	}
	if !reYouziEpisodeNum.MatchString(path) {
		return "", false
	}
	return path, true
}

// youziEpisodesFromHTML ld+json 缺分集数组时的兜底：扫描页面分集链接。
func youziEpisodesFromHTML(sourceID, body string, seen map[string]bool) []Chapter {
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}
	var chapters []Chapter
	for _, anchor := range providerHTMLNodes(document, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "a"
	}) {
		path := youziCardPath(providerHTMLAttr(anchor, "href"))
		key, valid := youziEpisodeKey(sourceID, path)
		if !valid || seen[key] {
			continue
		}
		seen[key] = true
		number := 0
		if match := reYouziEpisodeNum.FindStringSubmatch(key); len(match) > 1 {
			number, _ = strconv.Atoi(match[1])
		}
		if number <= 0 {
			continue
		}
		title := strings.TrimSpace(providerHTMLText(anchor))
		if title == "" {
			title = fmt.Sprintf("第 %d 集", number)
		} else {
			title = truncate(title, 512)
		}
		chapters = append(chapters, Chapter{
			ID: providerChapterID(sourceYouzi, sourceID, key), Source: sourceYouzi,
			Title: title, VideoURL: youziPlayScheme + key, CurrentEpisode: rawEpisode(number),
			PageURL: youziDetailURL(key), Referer: youziSite() + "/",
		})
	}
	return chapters
}

// resolveYouziMedia 取流：解析章节键 → （剧集分集先抓页提取 videoUUID）→
// 换取播放凭证地址 → 带会话 Cookie 走 prepareWebProviderMedia。
func (d *Downloader) resolveYouziMedia(ctx context.Context, task Task) (providerMedia, error) {
	source, sourceID, ok := splitProviderDramaID(task.DramaID)
	if !ok || source != sourceYouzi || !validYouziSourceID(sourceID) {
		return providerMedia{}, errors.New("柚子作品信息无效，请刷新详情")
	}
	prefix := providerChapterID(sourceYouzi, sourceID, "")
	if !strings.HasPrefix(task.Chapter.ID, prefix) {
		return providerMedia{}, errors.New("柚子播放分集信息无效，请刷新详情")
	}
	key := strings.TrimPrefix(task.Chapter.ID, prefix)
	detailURL := youziDetailURL(sourceID)
	videoID := ""
	switch {
	case reYouziUUIDID.MatchString(key):
		videoID = key
	case strings.HasPrefix(key, sourceID+"-episode-"):
		if _, valid := youziEpisodeKey(sourceID, key); !valid {
			return providerMedia{}, errors.New("柚子播放分集信息无效，请刷新详情")
		}
		body, _, err := d.youziFetchText(ctx, youziDetailURL(key), detailURL)
		if err != nil {
			if errors.Is(err, errYouziNotFound) {
				return providerMedia{}, errors.New("柚子该分集不存在或已下线")
			}
			return providerMedia{}, err
		}
		match := reYouziMediaVideo.FindStringSubmatch(body)
		if len(match) > 1 {
			videoID = match[1]
		}
	}
	if videoID == "" {
		return providerMedia{}, errors.New("柚子该集暂无可用播放地址")
	}
	cookie, err := d.youziBootstrapSession(ctx)
	if err != nil {
		return providerMedia{}, err
	}
	mediaURL, expires, grantCookie, err := d.youziPlaybackToken(ctx, videoID, detailURL)
	if err != nil {
		return providerMedia{}, err
	}
	if grantCookie != "" {
		cookie = youziMergeCookie(cookie, grantCookie)
	}
	// Cookie 绑定到播放凭证实际下发的媒体源（通常即站点本身）；若上游把
	// m3u8 指到第三方主机，apply() 的 origin 相等判断会自然不发 Cookie。
	mediaOrigin := ""
	if address, parseErr := url.Parse(mediaURL); parseErr == nil {
		mediaOrigin = providerMediaOrigin(address)
	}
	return d.prepareWebProviderMedia(ctx, providerMedia{
		URL:     mediaURL,
		Referer: detailURL,
		credentials: &providerMediaCredentials{
			cookie:    cookie,
			origin:    mediaOrigin,
			referer:   detailURL,
			userAgent: youziUserAgent,
			expires:   expires,
		},
	}, "柚子")
}

// youziMergeCookie 并入播放凭证 Cookie：站点若已在 bootstrap 下发同名
// eden_play_<videoId>（Path 覆盖 /media/），追加会让首位旧值生效而被拒，
// 因此同名原位替换、异名追加。
func youziMergeCookie(cookie, grant string) string {
	key, _, _ := strings.Cut(grant, "=")
	var parts []string
	replaced := false
	for _, piece := range strings.Split(cookie, "; ") {
		name, _, _ := strings.Cut(piece, "=")
		if name == key {
			parts = append(parts, grant)
			replaced = true
			continue
		}
		parts = append(parts, piece)
	}
	if !replaced {
		parts = append(parts, grant)
	}
	return strings.Join(parts, "; ")
}

func mustParseYouziSite() *url.URL {
	address, err := url.Parse(youziSite())
	if err != nil {
		return nil
	}
	return address
}

// youziParseDuration 解析 ISO-8601 时长（PT259S / PT4M30S / PT1H2M3S）为秒。
func youziParseDuration(raw string) float64 {
	match := reYouziDuration.FindStringSubmatch(strings.TrimSpace(raw))
	if len(match) < 4 || (match[1] == "" && match[2] == "" && match[3] == "") {
		return 0
	}
	hours, _ := strconv.Atoi(match[1])
	minutes, _ := strconv.Atoi(match[2])
	seconds, _ := strconv.ParseFloat(match[3], 64)
	return float64(hours)*3600 + float64(minutes)*60 + seconds
}

// ---- ld+json 解析辅助（该页 ld+json 为单对象或数组，字段形态有 string/数组/数字漂移）----

func youziLDEntities(body string) []map[string]any {
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, node := range providerHTMLNodes(document, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "script" &&
			strings.Contains(strings.ToLower(providerHTMLAttr(node, "type")), "application/ld+json")
	}) {
		var text strings.Builder
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			text.WriteString(child.Data)
		}
		raw := strings.TrimSpace(text.String())
		if raw == "" {
			continue
		}
		var value any
		if json.Unmarshal([]byte(raw), &value) != nil {
			continue
		}
		out = append(out, youziFlattenEntity(value)...)
	}
	return out
}

func youziFlattenEntity(value any) []map[string]any {
	var out []map[string]any
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			out = append(out, youziFlattenEntity(item)...)
		}
	case map[string]any:
		out = append(out, typed)
		if graph, ok := typed["@graph"].([]any); ok {
			out = append(out, youziFlattenEntity(graph)...)
		}
	}
	return out
}

func youziEntityType(entity map[string]any, want string) bool {
	switch typed := entity["@type"].(type) {
	case string:
		return strings.EqualFold(typed, want)
	case []any:
		for _, item := range typed {
			if name, ok := item.(string); ok && strings.EqualFold(name, want) {
				return true
			}
		}
	}
	return false
}

func youziFindEntity(entities []map[string]any, want string) map[string]any {
	for _, entity := range entities {
		if youziEntityType(entity, want) {
			return entity
		}
	}
	return nil
}

func youziEntityString(entity map[string]any, key string) string {
	value, _ := entity[key].(string)
	return strings.TrimSpace(value)
}

// youziEntityFirstString 取 string 或 []string 的首个非空值。
func youziEntityFirstString(entity map[string]any, key string) string {
	switch typed := entity[key].(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		for _, item := range typed {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func youziEntityStringList(entity map[string]any, key string) []string {
	var out []string
	switch typed := entity[key].(type) {
	case string:
		if value := strings.TrimSpace(typed); value != "" {
			out = append(out, truncate(value, 64))
		}
	case []any:
		for _, item := range typed {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				out = append(out, truncate(strings.TrimSpace(value), 64))
			}
		}
	}
	return out
}

func youziEntityMap(entity map[string]any, key string) (map[string]any, bool) {
	value, ok := entity[key].(map[string]any)
	return value, ok
}

func youziEntityList(entity map[string]any, key string) []any {
	value, _ := entity[key].([]any)
	return value
}

func youziEntityNumber(entity map[string]any, key string) int {
	switch typed := entity[key].(type) {
	case float64:
		return int(typed)
	case string:
		number, _ := strconv.Atoi(strings.TrimSpace(typed))
		return number
	}
	return 0
}

func init() {
	registerProviderSource(ProviderDef{
		Slug:             sourceYouzi,
		DisplayName:      "柚子",
		SearchKind:       providerSearchOnline,
		CatalogKinds:     youziCategoryIDs(),
		EnabledByDefault: false,
		Adult:            true,
		Domains:          []string{"youzidj.com"},
		Implemented:      true,
	})
}
