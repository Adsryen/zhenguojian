package core

// 韩小圈 (www.jennyhow.com, macms "eWave" template) provider. Port of
// reference/guoapp provider_hanxiaoquan.go plus the empirically verified
// search-gate solution (see below).
//
// Channels:
//   - catalog:  HTML /hxq/{cat}.html, /hxq/{cat}-{page}.html   (plain HTTP, no gate)
//   - detail:   /hanxiaoquan/{id}.html                          (plain HTTP, no gate)
//   - stream:   /play/{id}-{lane}-{n}.html -> `var now="...m3u8"` (plain HTTP, no gate)
//   - search:   /search.php?searchword= behind a JS cookie gate:
//       first hop returns a 733-byte challenge page (header x-idc-search-verify: 1)
//       whose JS sets `document.cookie="idc_search_browser=<32 hex>"` and reloads
//       with `_idc_pass = Date.now().toString(36) + Math.random().toString(36).slice(2,8)`.
//       Verified 2026-11-07 by pure curl (no browser): sending
//       `Cookie: idc_search_browser=<token>` + `_idc_pass=<base36 ms><6 base36 chars>`
//       on the second hop yields the full results page. Rules measured:
//         * token alone without _idc_pass -> challenge again;
//         * junk _idc_pass ("hello" -> decodes to a 1970 ms date) -> HTTP 401;
//         * timestamp 24h stale -> still accepted (no freshness window check);
//         * wrong cookie value -> challenge again.
//       So: parse the token from the challenge body (never hardcode), mint the
//       pass exactly like the JS, cache the token for its Max-Age window.

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	providerSlugHanxiaoquan = "hanxiaoquan"
	hanxiaoquanUserAgent    = "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
	hanxiaoquanPageSize     = 24
	// The site issues the browser cookie with Max-Age=1800; keep it a bit shorter.
	hanxiaoquanGateTTL      = 20 * time.Minute
	hanxiaoquanGateTimeout  = 15 * time.Second
	hanxiaoquanGateMaxBytes = 4 * 1024
)

// hanxiaoquanSiteBase is a var so offline httptest suites can repoint it.
var hanxiaoquanSiteBase = "https://www.jennyhow.com"

var hanxiaoquanCategories = []nativeCategory{
	{ID: "1", Name: "最新韩剧"},
	{ID: "2", Name: "韩国电影"},
	{ID: "3", Name: "韩国综艺"},
	{ID: "4", Name: "韩国动漫"},
}

var (
	reHanxiaoquanCardPic  = regexp.MustCompile(`(?is)<a href="/hanxiaoquan/(\d+)\.html"[^>]*title="([^"]*)"[^>]*class="module-item-pic"`)
	reHanxiaoquanCardImg  = regexp.MustCompile(`(?is)<img[^>]*data-src="([^"]+)"`)
	reHanxiaoquanCardText = regexp.MustCompile(`(?is)<a href="/hanxiaoquan/\d+\.html"[^>]*class="module-item-title"[^>]*>([^<]*)</a>`)
	reHanxiaoquanCardNote = regexp.MustCompile(`(?is)class="module-item-text"[^>]*>([^<]*)<`)
	reHanxiaoquanAdText   = regexp.MustCompile(`@[^@]{1,60}@`)
	reHanxiaoquanTitle    = regexp.MustCompile(`(?is)<h1 class="page-title"[^>]*>(.*?)</h1>`)
	reHanxiaoquanOgImage  = regexp.MustCompile(`(?is)property="og:image"\s+content="([^"]+)"`)
	reHanxiaoquanCoverSrc = regexp.MustCompile(`(?is)<div class="video-cover">.*?<img[^>]*data-src="([^"]+)"`)
	reHanxiaoquanInfoItem = regexp.MustCompile(`(?is)<span class="video-info-itemtitle">([^<]*)</span>\s*<div class="video-info-item[^"]*">(.*?)</div>`)
	reHanxiaoquanAnchor   = regexp.MustCompile(`(?is)<a[^>]*>([^<]+)</a>`)
	reHanxiaoquanLineTab  = regexp.MustCompile(`(?is)href="#playlist(\d+)"[^>]*>\s*<i[^>]*></i>\s*([^<]+)`)
	reHanxiaoquanEpisodes = regexp.MustCompile(`(?is)<a title="([^"]*)" href="(/play/\d+-\d+-\d+\.html)"`)
	reHanxiaoquanNow      = regexp.MustCompile(`(?is)var\s+now\s*=\s*["']([^"']+)["']`)
	reHanxiaoquanMediaAny = regexp.MustCompile(`(?i)https?://[^"'\s<>\\]+\.(?:m3u8|mp4)(?:\?[^"'\s<>\\]*)?`)
	reHanxiaoquanTag      = regexp.MustCompile(`<[^>]+>`)
	reHanxiaoquanLeadNum  = regexp.MustCompile(`(\d+)`)
	// Gate tokens observed as 32 hex chars; keep the class generous.
	reHanxiaoquanGateToken = regexp.MustCompile(`idc_search_browser=([0-9a-zA-Z]{16,64})`)
)

func init() {
	registerProviderSource(ProviderDef{
		Slug: providerSlugHanxiaoquan, DisplayName: "韩小圈", SearchKind: providerSearchOnline, Lanes: true,
		CatalogKinds: []string{"1", "2", "3", "4"}, EnabledByDefault: true,
		Domains: []string{"jennyhow.com", "www.jennyhow.com"}, Implemented: true,
	})
}

func validHanxiaoquanCategory(category string) bool {
	if category == "" {
		return true
	}
	if len(category) > 8 || strings.ContainsAny(category, "|/\\\x00\r\n") {
		return false
	}
	for _, entry := range hanxiaoquanCategories {
		if entry.ID == category {
			return true
		}
	}
	return false
}

func hanxiaoquanNumericID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 12 {
		return false
	}
	number, err := strconv.Atoi(value)
	return err == nil && number > 0
}

func hanxiaoquanClean(text string) string {
	if text == "" {
		return ""
	}
	text = reHanxiaoquanTag.ReplaceAllString(text, "")
	text = guipianUnescape(text)
	text = strings.ReplaceAll(text, "\u3000", " ")
	return strings.Join(strings.Fields(text), " ")
}

func hanxiaoquanFixURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	} else if strings.HasPrefix(raw, "/") {
		raw = hanxiaoquanSiteBase + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || !validNativeCoverURL(parsed) {
		return ""
	}
	return parsed.String()
}

// hanxiaoquanHostAllowed accepts the production hosts plus the host of the
// currently configured base (keeps httptest fixtures working).
func hanxiaoquanHostAllowed(host string) bool {
	host = strings.ToLower(host)
	if host == "jennyhow.com" || host == "www.jennyhow.com" {
		return true
	}
	if base, err := url.Parse(hanxiaoquanSiteBase); err == nil && base.Hostname() != "" && strings.EqualFold(base.Hostname(), host) {
		return true
	}
	return false
}

func (d *Downloader) fetchHanxiaoquanCategories() []nativeCategory {
	return append([]nativeCategory(nil), hanxiaoquanCategories...)
}

func (d *Downloader) hanxiaoquanGet(ctx context.Context, path string) (string, error) {
	site := strings.TrimRight(hanxiaoquanSiteBase, "/")
	address := path
	if !strings.HasPrefix(address, "http") {
		address = site + path
	}
	pageContext := context.WithValue(ctx, providerTextUserAgentKey{}, hanxiaoquanUserAgent)
	return d.fetchProviderText(pageContext, address, site+"/")
}

func parseHanxiaoquanCards(body string) []Drama {
	items := make([]Drama, 0, hanxiaoquanPageSize)
	seen := map[string]bool{}
	for _, match := range reHanxiaoquanCardPic.FindAllStringSubmatchIndex(body, -1) {
		if len(match) < 6 {
			continue
		}
		id := body[match[2]:match[3]]
		title := hanxiaoquanClean(body[match[4]:match[5]])
		if !hanxiaoquanNumericID(id) || seen[id] {
			continue
		}
		chunk := body[match[1]:]
		if end := len(chunk); end > 600 {
			chunk = chunk[:600]
		}
		cover := ""
		if coverMatch := reHanxiaoquanCardImg.FindStringSubmatch(chunk); len(coverMatch) > 1 {
			cover = hanxiaoquanFixURL(coverMatch[1])
		}
		note := ""
		if noteMatch := reHanxiaoquanCardNote.FindStringSubmatch(chunk); len(noteMatch) > 1 {
			note = hanxiaoquanClean(noteMatch[1])
		}
		if title == "" {
			if titleMatch := reHanxiaoquanCardText.FindStringSubmatch(chunk); len(titleMatch) > 1 {
				title = hanxiaoquanClean(titleMatch[1])
			}
		}
		if title == "" {
			continue
		}
		seen[id] = true
		items = append(items, Drama{
			ID: providerDramaID(providerSlugHanxiaoquan, id), Source: providerSlugHanxiaoquan, SourceID: id,
			Title: truncate(title, 512), Name: truncate(title, 512),
			Cover: cover, CoverURL: cover, ChannelName: "韩小圈",
			CategoryName: "韩剧", Remark: truncate(note, 64),
		})
		if len(items) > 500 {
			break
		}
	}
	return items
}

func hanxiaoquanPageCount(body, category string, page, count int) int {
	pattern := regexp.MustCompile(fmt.Sprintf(`(?is)/hxq/%s-(\d+)\.html`, regexp.QuoteMeta(category)))
	maxPage := page
	for _, match := range pattern.FindAllStringSubmatch(body, -1) {
		if value, err := strconv.Atoi(match[1]); err == nil && value > maxPage && value < 100000 {
			maxPage = value
		}
	}
	if maxPage > page {
		return maxPage
	}
	if count >= hanxiaoquanPageSize {
		return page + 1
	}
	return page
}

func (d *Downloader) fetchHanxiaoquanCatalogPage(ctx context.Context, page int, category, query string) ([]Drama, bool, error) {
	if page < 1 || page > 100000 {
		return nil, false, errors.New("韩小圈目录页码无效")
	}
	if query != "" {
		return d.searchHanxiaoquan(ctx, page, query)
	}
	if !validHanxiaoquanCategory(category) {
		return nil, false, errors.New("韩小圈内容分类无效")
	}
	if category == "" {
		category = hanxiaoquanCategories[0].ID
	}
	path := "/hxq/" + category + ".html"
	if page > 1 {
		path = fmt.Sprintf("/hxq/%s-%d.html", category, page)
	}
	body, err := d.hanxiaoquanGet(ctx, path)
	if err != nil {
		return nil, false, err
	}
	items := parseHanxiaoquanCards(body)
	if len(items) == 0 {
		return []Drama{}, false, nil
	}
	pageCount := hanxiaoquanPageCount(body, category, page, len(items))
	return items, page < pageCount, nil
}

func hanxiaoquanInfoFields(body string) map[string]string {
	fields := map[string]string{}
	for _, match := range reHanxiaoquanInfoItem.FindAllStringSubmatch(body, -1) {
		label := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(match[1]), "："))
		value := hanxiaoquanClean(match[2])
		if label != "" && value != "" && fields[label] == "" {
			fields[label] = value
		}
	}
	return fields
}

func hanxiaoquanPickPlaylist(body string) (string, []Chapter, error) {
	lineNames := map[string]string{}
	for _, tab := range reHanxiaoquanLineTab.FindAllStringSubmatch(body, -1) {
		lineNames[tab[1]] = strings.TrimSpace(tab[2])
	}
	lineIDs := make([]string, 0, len(lineNames))
	for id := range lineNames {
		lineIDs = append(lineIDs, id)
	}
	sort.Strings(lineIDs)
	if len(lineIDs) == 0 {
		lineIDs = []string{""}
	}
	bestName := ""
	var best []Chapter
	for _, lineID := range lineIDs {
		segment := body
		if lineID != "" {
			marker := `id="playlist` + lineID + `"`
			start := strings.Index(body, marker)
			if start < 0 {
				continue
			}
			segment = body[start+len(marker):]
			if end := strings.Index(segment, `id="playlist`); end >= 0 {
				segment = segment[:end]
			} else if end := strings.Index(segment, `class="module-footer"`); end >= 0 {
				segment = segment[:end]
			}
		}
		var chapters []Chapter
		seenPath := map[string]bool{}
		for index, ep := range reHanxiaoquanEpisodes.FindAllStringSubmatch(segment, -1) {
			epTitle := hanxiaoquanClean(ep[1])
			playURL := hanxiaoquanPlayURL(ep[2])
			if playURL == "" || seenPath[playURL] {
				continue
			}
			seenPath[playURL] = true
			order := index + 1
			if num := reHanxiaoquanLeadNum.FindString(epTitle); num != "" {
				if parsed, err := strconv.Atoi(num); err == nil && parsed > 0 && parsed <= 100000 {
					order = parsed
				}
			}
			if epTitle == "" {
				epTitle = fmt.Sprintf("第 %d 集", order)
			}
			chapters = append(chapters, Chapter{
				ID:             providerChapterID(providerSlugHanxiaoquan, "", playURL),
				Source:         providerSlugHanxiaoquan,
				Title:          truncate(epTitle, 128),
				CurrentEpisode: rawEpisode(order),
				VideoURL:       "hanxiaoquan-play://" + playURL,
				PageURL:        playURL,
				Referer:        strings.TrimRight(hanxiaoquanSiteBase, "/") + "/",
			})
		}
		if len(chapters) > len(best) {
			best = chapters
			bestName = lineNames[lineID]
		}
	}
	if len(best) == 0 {
		return "", nil, errors.New("韩小圈暂无可播放分集")
	}
	sort.SliceStable(best, func(i, j int) bool {
		left, _ := strconv.Atoi(best[i].EpisodeString(i + 1))
		right, _ := strconv.Atoi(best[j].EpisodeString(j + 1))
		return left < right
	})
	return bestName, best, nil
}

func hanxiaoquanPlayURL(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if !strings.HasPrefix(href, "http") {
		href = strings.TrimRight(hanxiaoquanSiteBase, "/") + "/" + strings.TrimPrefix(href, "/")
	}
	parsed, err := url.Parse(href)
	if err != nil || parsed.User != nil {
		return ""
	}
	if !hanxiaoquanHostAllowed(parsed.Hostname()) {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(parsed.Path), ".html") || !strings.Contains(parsed.Path, "/play/") {
		return ""
	}
	return parsed.String()
}

func (d *Downloader) fetchHanxiaoquanDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	if !hanxiaoquanNumericID(sourceID) {
		return Drama{}, nil, errors.New("韩小圈视频 ID 无效")
	}
	body, err := d.hanxiaoquanGet(ctx, "/hanxiaoquan/"+sourceID+".html")
	if err != nil {
		return Drama{}, nil, err
	}
	name := ""
	if match := reHanxiaoquanTitle.FindStringSubmatch(body); len(match) > 1 {
		name = hanxiaoquanClean(match[1])
	}
	if name == "" {
		return Drama{}, nil, errors.New("韩小圈详情缺少视频名称")
	}
	cover := ""
	if match := reHanxiaoquanOgImage.FindStringSubmatch(body); len(match) > 1 {
		cover = hanxiaoquanFixURL(match[1])
	}
	if cover == "" {
		if match := reHanxiaoquanCoverSrc.FindStringSubmatch(body); len(match) > 1 {
			cover = hanxiaoquanFixURL(match[1])
		}
	}
	fields := hanxiaoquanInfoFields(body)
	description := fields["剧情"]
	if description == "" {
		description = fields["简介"]
	}
	description = reHanxiaoquanAdText.ReplaceAllString(description, "")

	drama := Drama{
		ID: providerDramaID(providerSlugHanxiaoquan, sourceID), Source: providerSlugHanxiaoquan, SourceID: sourceID,
		Title: truncate(name, 512), Name: truncate(name, 512),
		Desc: truncate(description, 12000), Intro: truncate(description, 12000),
		Cover: cover, CoverURL: cover, ChannelName: "韩小圈", CategoryName: "韩剧",
		Remark:     truncate(firstNonEmpty(fields["更新"], fields["状态"]), 64),
		OnlineDate: truncate(fields["更新"], 32),
	}
	tags := make([]string, 0, 8)
	seenTag := map[string]bool{}
	addTag := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && !seenTag[value] && len([]rune(value)) <= 48 && len(tags) < 16 {
			seenTag[value] = true
			tags = append(tags, value)
		}
	}
	if year := fields["上映"]; year != "" {
		addTag(year + "年")
	}
	if director := fields["导演"]; director != "" {
		addTag("导演 " + truncate(director, 32))
	}
	if actors := fields["主演"]; actors != "" {
		addTag("主演 " + truncate(actors, 48))
	}
	drama.Tags = tags

	_, chapters, err := hanxiaoquanPickPlaylist(body)
	if err != nil {
		return Drama{}, nil, err
	}
	for index := range chapters {
		chapters[index].ID = providerChapterID(providerSlugHanxiaoquan, sourceID, chapters[index].PageURL)
	}
	drama.TotalEpisode, drama.EpisodeCount = len(chapters), len(chapters)
	return drama, chapters, nil
}

func (d *Downloader) resolveHanxiaoquanMedia(ctx context.Context, task Task) (providerMedia, error) {
	source, sourceID, valid := splitProviderDramaID(task.DramaID)
	prefix := providerChapterID(providerSlugHanxiaoquan, sourceID, "")
	if !valid || source != providerSlugHanxiaoquan || !hanxiaoquanNumericID(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return providerMedia{}, errors.New("韩小圈播放分集信息无效，请刷新详情")
	}
	playURL := strings.TrimPrefix(task.Chapter.ID, prefix)
	if playURL == "" {
		playURL = task.Chapter.PageURL
	}
	if hanxiaoquanPlayURL(playURL) == "" {
		return providerMedia{}, errors.New("韩小圈播放页地址无效")
	}
	body, err := d.hanxiaoquanGet(ctx, playURL)
	if err != nil {
		return providerMedia{}, err
	}
	address := ""
	if match := reHanxiaoquanNow.FindStringSubmatch(body); len(match) > 1 {
		address = strings.TrimSpace(match[1])
	}
	if address == "" {
		address = reHanxiaoquanMediaAny.FindString(body)
	}
	if address == "" {
		return providerMedia{}, errors.New("韩小圈该集暂无可用播放地址")
	}
	if strings.HasPrefix(address, "//") {
		address = "https:" + address
	}
	if !isProviderHTTPMediaURL(address) {
		return providerMedia{}, errors.New("韩小圈播放地址无效")
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return providerMedia{}, errors.New("韩小圈播放地址无效")
	}
	media := providerMedia{URL: address, Referer: strings.TrimRight(hanxiaoquanSiteBase, "/") + "/"}
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8") {
		return d.prepareWebProviderMedia(ctx, media, "韩小圈")
	}
	return media, nil
}

// ---------------------------------------------------------------------------
// search.php cookie gate
// ---------------------------------------------------------------------------

var hanxiaoquanGate struct {
	mu      sync.Mutex
	token   string
	expires time.Time
}

func hanxiaoquanCachedToken() string {
	hanxiaoquanGate.mu.Lock()
	defer hanxiaoquanGate.mu.Unlock()
	if hanxiaoquanGate.token != "" && time.Now().Before(hanxiaoquanGate.expires) {
		return hanxiaoquanGate.token
	}
	return ""
}

func hanxiaoquanRememberToken(token string) {
	hanxiaoquanGate.mu.Lock()
	defer hanxiaoquanGate.mu.Unlock()
	hanxiaoquanGate.token = token
	hanxiaoquanGate.expires = time.Now().Add(hanxiaoquanGateTTL)
}

func hanxiaoquanForgetToken() {
	hanxiaoquanGate.mu.Lock()
	defer hanxiaoquanGate.mu.Unlock()
	hanxiaoquanGate.token = ""
	hanxiaoquanGate.expires = time.Time{}
}

// hanxiaoquanIDCPass mirrors the challenge JS:
// Date.now().toString(36) + Math.random().toString(36).slice(2, 8).
// The server only sanity-checks that the leading base36 segment decodes to a
// plausible epoch-ms (a junk 5-char pass gets 401); freshness is not enforced.
func hanxiaoquanIDCPass() string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	var suffix strings.Builder
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		seed := time.Now().UnixNano()
		for i := range buf {
			buf[i] = byte(seed >> (8 * i))
		}
	}
	for _, b := range buf {
		suffix.WriteByte(alphabet[int(b)%len(alphabet)])
	}
	return strconv.FormatInt(time.Now().UnixMilli(), 36) + suffix.String()
}

// hanxiaoquanGateFetch performs one search.php GET, optionally presenting the
// browser cookie + pass. Returns body, whether the JS challenge was served.
func (d *Downloader) hanxiaoquanGateFetch(ctx context.Context, address, token string) (string, bool, error) {
	requestURL := address
	if token != "" {
		separator := "?"
		if strings.Contains(requestURL, "?") {
			separator = "&"
		}
		requestURL = requestURL + separator + "_idc_pass=" + hanxiaoquanIDCPass()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", false, err
	}
	request.Header.Set("User-Agent", hanxiaoquanUserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	request.Header.Set("Referer", strings.TrimRight(hanxiaoquanSiteBase, "/")+"/")
	if token != "" {
		request.Header.Set("Cookie", "idc_search_browser="+token)
	}
	response, err := d.doCatalogRequestWithTimeout(request, hanxiaoquanGateTimeout)
	if err != nil {
		return "", false, err
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes))
	if readErr != nil {
		return "", false, fmt.Errorf("韩小圈搜索响应读取失败：%w", readErr)
	}
	body := string(raw)
	challenge := false
	switch {
	case response.StatusCode == http.StatusUnauthorized && token != "":
		hanxiaoquanForgetToken()
		challenge = true
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		challenge = true
	case strings.EqualFold(response.Header.Get("x-idc-search-verify"), "1"):
		challenge = true
	default:
		if len(body) < hanxiaoquanGateMaxBytes && strings.Contains(body, "idc_search_browser=") && strings.Contains(body, "document.cookie") {
			challenge = true
		}
	}
	if response.StatusCode >= 400 && !challenge && response.StatusCode != http.StatusUnauthorized {
		return body, false, fmt.Errorf("韩小圈搜索请求失败：HTTP %d", response.StatusCode)
	}
	return body, challenge, nil
}

// hanxiaoquanSearchBody resolves the cookie gate (two-hop challenge solve,
// cached token reuse) and returns the real results page.
func (d *Downloader) hanxiaoquanSearchBody(ctx context.Context, address string) (string, error) {
	if token := hanxiaoquanCachedToken(); token != "" {
		body, challenged, err := d.hanxiaoquanGateFetch(ctx, address, token)
		if err != nil {
			return "", err
		}
		if !challenged {
			return body, nil
		}
	}
	body, challenged, err := d.hanxiaoquanGateFetch(ctx, address, "")
	if err != nil {
		return "", err
	}
	if !challenged {
		return body, nil
	}
	match := reHanxiaoquanGateToken.FindStringSubmatch(body)
	if len(match) < 2 {
		return "", errors.New("韩小圈搜索验证失败：挑战页缺少浏览器令牌")
	}
	token := match[1]
	body, challenged, err = d.hanxiaoquanGateFetch(ctx, address, token)
	if err != nil {
		return "", err
	}
	if challenged {
		return "", errors.New("韩小圈搜索验证未通过，请稍后重试")
	}
	hanxiaoquanRememberToken(token)
	return body, nil
}

func (d *Downloader) searchHanxiaoquan(ctx context.Context, page int, query string) ([]Drama, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 256 {
		return nil, false, errors.New("韩小圈搜索关键词无效")
	}
	// macms search returns the full result set on page 1 only; deeper pages
	// come back empty even with page= parameters (verified live 2026-11-07).
	if page > 1 {
		return []Drama{}, false, nil
	}
	address := strings.TrimRight(hanxiaoquanSiteBase, "/") + "/search.php?" + url.Values{"searchword": {query}}.Encode()
	body, err := d.hanxiaoquanSearchBody(ctx, address)
	if err != nil {
		return nil, false, err
	}
	return parseHanxiaoquanCards(body), false, nil
}
