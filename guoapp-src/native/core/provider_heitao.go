package core

// 黑桃（heitaodj.com）：AppleCMS 模板站，页面全部走 DOM 抓取（col-span/vod 结构
// 与帝果同源），取流经 player_aaaa 三态解密，另有年龄墙需要 heitao_age_confirmed=1
// Cookie。移植自 reference/guoapp/native/core/provider_heitao.go，差异见该参照：
//   - 本地 fetchProviderText/fetchProviderPage 没有 Cookie 注入钩子（参照版通过
//     providerTextCookieKey），年龄墙 Cookie 改由本文件的 heitaoFetchText 直接
//     设置请求头，避免改动 provider_huangguo.go。
//   - 参照版取站点地址用 d.providerBaseURL(sourceHeitao)；本地 providerBaseURL
//     没有 heitao 分支（default 落到 CloudFront 兜底），因此这里用 heitaoSite()。
//   - Drama 本地无 Badge 字段，只保留 Remark。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const (
	sourceHeitao    = "heitao"
	heitaoBaseURL   = "https://heitaodj.com"
	heitaoUserAgent = "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
	heitaoAgeCookie = "heitao_age_confirmed=1"
)

var (
	reHeitaoEpisodeHref  = regexp.MustCompile(`(?i)/index\.php/vod/play/id/(\d+)/sid/(\d+)/nid/(\d+)\.html`)
	reHeitaoPlayerRoute  = regexp.MustCompile(`(?i)/index\.php/vod/player/id/\d+/nid/(\d+)/sid/(\d+)\.html`)
	reHeitaoEpisodeCount = regexp.MustCompile(`^(\d+)集$`)
	reHeitaoSearchPage   = regexp.MustCompile(`(?i)/vod/search/page/(\d+)/wd/`)
)

// heitaoSite 返回黑桃站点根地址。不能走 d.providerBaseURL：本地该函数的 switch
// 没有 heitao 分支，default 会返回 CloudFront 兜底地址。
func heitaoSite() string {
	return strings.TrimRight(heitaoBaseURL, "/")
}

type heitaoSeriesMeta struct {
	Type        string `json:"@type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Image       any    `json:"image"`
}

// heitaoFetchText 抓取黑桃页面文本。与 fetchProviderText 不同点：始终携带年龄墙
// Cookie 与移动端 UA（年龄墙对播放器页是服务端拦截——不带 Cookie 时页面不含
// player_aaaa），并返回重定向后的最终地址供相对链接解析。
func (d *Downloader) heitaoFetchText(ctx context.Context, address, referer string) (string, string, error) {
	if referer == "" {
		referer = heitaoSite() + "/"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("User-Agent", heitaoUserAgent)
	request.Header.Set("Cookie", heitaoAgeCookie)
	request.Header.Set("Referer", referer)
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	response, err := d.doCatalogRequestWithTimeout(request, providerTimeout)
	if err != nil {
		return "", "", err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return "", "", readErr
	}
	if closeErr != nil {
		return "", "", closeErr
	}
	if len(body) > providerMaxBodyBytes {
		return "", "", fmt.Errorf("黑桃页面超过 %d 字节", providerMaxBodyBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", d.catalogResponseError(request, response, body)
	}
	if reason := catalogResponseBlockReason(response, body); reason != "" {
		return "", "", fmt.Errorf("黑桃请求被拦截：%s", reason)
	}
	rememberPlaybackResponseURL(ctx, address, response)
	finalURL := address
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	return string(body), finalURL, nil
}

func (d *Downloader) heitaoFetchPage(ctx context.Context, address, referer string) (*html.Node, string, error) {
	body, finalURL, err := d.heitaoFetchText(ctx, address, referer)
	if err != nil {
		return nil, "", err
	}
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	return document, finalURL, nil
}

func validHeitaoCategory(category string) bool {
	if category == "" {
		return true
	}
	return len(category) <= 6 && webProviderNumericID.MatchString(category)
}

func heitaoScriptText(node *html.Node) string {
	var builder strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			builder.WriteString(child.Data)
		}
	}
	return strings.TrimSpace(builder.String())
}

func heitaoSeriesMetadata(document *html.Node) heitaoSeriesMeta {
	for _, script := range providerHTMLNodes(document, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "script" &&
			strings.Contains(strings.ToLower(providerHTMLAttr(node, "type")), "ld+json")
	}) {
		text := heitaoScriptText(script)
		if text == "" {
			continue
		}
		var list []heitaoSeriesMeta
		if json.Unmarshal([]byte(text), &list) == nil {
			for _, item := range list {
				if strings.EqualFold(item.Type, "TVSeries") {
					return item
				}
			}
			continue
		}
		var single heitaoSeriesMeta
		if json.Unmarshal([]byte(text), &single) == nil && strings.EqualFold(single.Type, "TVSeries") {
			return single
		}
	}
	return heitaoSeriesMeta{}
}

func heitaoCategoryName(document *html.Node) string {
	for _, node := range providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "li" }) {
		text := strings.TrimSpace(providerHTMLText(node))
		if strings.HasPrefix(text, "分类：") {
			if name := strings.TrimSpace(strings.TrimPrefix(text, "分类：")); name != "" {
				return name
			}
		}
	}
	return ""
}

func heitaoTags(document *html.Node) []string {
	block := providerHTMLFirstClass(document, "detail-tags")
	if block == nil {
		return nil
	}
	var tags []string
	seen := map[string]bool{}
	for _, anchor := range providerHTMLNodes(block, func(node *html.Node) bool { return node.Data == "a" }) {
		value := strings.TrimSpace(providerHTMLText(anchor))
		if value == "" || len([]rune(value)) > 48 || seen[value] {
			continue
		}
		seen[value] = true
		tags = append(tags, value)
		if len(tags) >= 32 {
			break
		}
	}
	return tags
}

func heitaoEpisodeCount(remark string) int {
	match := reHeitaoEpisodeCount.FindStringSubmatch(strings.TrimSpace(remark))
	if len(match) < 2 {
		return 0
	}
	value, err := strconv.Atoi(match[1])
	if err != nil || value < 1 || value > 100000 {
		return 0
	}
	return value
}

// fetchHeitaoCategories 抓首页导航里的 /index.php/vod/type/id/X.html 分类。
func (d *Downloader) fetchHeitaoCategories(ctx context.Context) ([]nativeCategory, error) {
	site := heitaoSite()
	document, pageURL, err := d.heitaoFetchPage(ctx, site+"/", site+"/")
	if err != nil {
		return nil, err
	}
	var categories []nativeCategory
	seen := map[string]bool{}
	for _, anchor := range providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "a" }) {
		action, values, valid := dsdRouteParameters(pageURL, providerHTMLAttr(anchor, "href"))
		id, name := values["id"], strings.TrimSpace(providerHTMLText(anchor))
		if !valid || action != "type" || !validHeitaoCategory(id) || id == "" || seen[id] || name == "" || len([]rune(name)) > 48 {
			continue
		}
		seen[id] = true
		categories = append(categories, nativeCategory{ID: id, Name: name})
		if len(categories) >= 32 {
			break
		}
	}
	if len(categories) == 0 {
		return nil, errors.New("黑桃未返回有效的内容分类")
	}
	return categories, nil
}

func parseHeitaoCards(document *html.Node, pageURL string) []Drama {
	items := []Drama{}
	seen := map[string]bool{}
	for _, card := range providerHTMLNodes(document, func(node *html.Node) bool {
		return node.Data == "a" && providerHTMLClass(node, "thumb")
	}) {
		action, values, valid := dsdRouteParameters(pageURL, providerHTMLAttr(card, "href"))
		id := values["id"]
		if !valid || action != "detail" || !webProviderNumericID.MatchString(id) || seen[id] {
			continue
		}
		title := strings.TrimSpace(providerHTMLAttr(card, "title"))
		for _, image := range providerHTMLNodes(card, func(node *html.Node) bool { return node.Data == "img" }) {
			if title == "" {
				title = strings.TrimSpace(providerHTMLAttr(image, "alt"))
			}
			if title != "" {
				break
			}
		}
		if title == "" {
			title = strings.TrimSpace(providerHTMLText(card))
		}
		if title == "" {
			continue
		}
		seen[id] = true
		cover := ""
		for _, image := range providerHTMLNodes(card, func(node *html.Node) bool { return node.Data == "img" }) {
			for _, attribute := range []string{"src", "data-src", "data-original"} {
				if address := providerCoverAddress(providerHTMLAttr(image, attribute), pageURL); address != "" {
					cover = address
					break
				}
			}
			if cover != "" {
				break
			}
		}
		remark := ""
		if label := providerHTMLFirstClass(card, "label"); label != nil {
			remark = truncate(providerHTMLText(label), 64)
		}
		drama := Drama{
			ID: providerDramaID(sourceHeitao, id), Source: sourceHeitao, SourceID: id,
			Title: truncate(title, 512), Name: truncate(title, 512),
			Cover: cover, CoverURL: cover, ChannelName: "黑桃", CategoryName: "短剧", Remark: remark,
		}
		if count := heitaoEpisodeCount(remark); count > 0 {
			drama.EpisodeCount, drama.TotalEpisode = count, count
		}
		items = append(items, drama)
		if len(items) >= 500 {
			break
		}
	}
	return items
}

func heitaoPageCount(document *html.Node, pageURL, category, query string, page int) int {
	pages := page
	for _, anchor := range providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "a" }) {
		href, value := providerHTMLAttr(anchor, "href"), ""
		if query != "" {
			if match := reHeitaoSearchPage.FindStringSubmatch(href); len(match) == 2 {
				value = match[1]
			}
		} else if action, values, valid := dsdRouteParameters(pageURL, href); valid && (action == "show" || action == "type") && values["id"] == category {
			value = values["page"]
		}
		if number, err := strconv.Atoi(value); err == nil && number > pages && number < 100000 {
			pages = number
		}
	}
	return pages
}

func heitaoCatalogAddress(site string, page int, category, query string) string {
	if query != "" {
		escaped := url.PathEscape(query)
		if page <= 1 {
			return site + "/index.php/vod/search/wd/" + escaped + ".html"
		}
		return fmt.Sprintf("%s/index.php/vod/search/page/%d/wd/%s.html", site, page, escaped)
	}
	show := site + "/index.php/vod/show"
	if category != "" {
		show += "/id/" + category
	}
	if page <= 1 {
		return show + ".html"
	}
	return fmt.Sprintf("%s/page/%d.html", show, page)
}

// fetchHeitaoCatalogPage 目录页（category 走 show，query 走 search/wd）。
func (d *Downloader) fetchHeitaoCatalogPage(ctx context.Context, page int, category, query string) ([]Drama, bool, error) {
	query = strings.TrimSpace(query)
	if page < 1 || page > 100000 || len(query) > 256 || !validHeitaoCategory(category) {
		return nil, false, errors.New("黑桃目录查询参数无效")
	}
	site := heitaoSite()
	address := heitaoCatalogAddress(site, page, category, query)
	document, pageURL, err := d.heitaoFetchPage(ctx, address, site+"/")
	if err != nil {
		return nil, false, err
	}
	items := parseHeitaoCards(document, pageURL)
	pages := heitaoPageCount(document, pageURL, category, query, page)
	return items, pages > page, nil
}

// heitaoChapters 从详情页分集链接提取分集；sid=播放线路，取分集最多的线路，
// 并列时取 sid 小的。
func heitaoChapters(document *html.Node, site, sourceID string) []Chapter {
	type heitaoEpisode struct {
		sid   int
		nid   int
		title string
	}
	groups := map[int][]heitaoEpisode{}
	seen := map[string]bool{}
	for _, anchor := range providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "a" }) {
		match := reHeitaoEpisodeHref.FindStringSubmatch(providerHTMLAttr(anchor, "href"))
		if len(match) < 4 || match[1] != sourceID {
			continue
		}
		sid, sidErr := strconv.Atoi(match[2])
		nid, nidErr := strconv.Atoi(match[3])
		if sidErr != nil || nidErr != nil || sid < 1 || nid < 1 {
			continue
		}
		key := fmt.Sprintf("%d:%d", sid, nid)
		if seen[key] {
			continue
		}
		seen[key] = true
		title := strings.TrimSpace(providerHTMLText(anchor))
		if title == "" {
			title = fmt.Sprintf("第 %d 集", nid)
		}
		groups[sid] = append(groups[sid], heitaoEpisode{sid: sid, nid: nid, title: title})
	}
	var best []heitaoEpisode
	bestSid := 0
	for sid, items := range groups {
		if len(items) > len(best) || (len(items) == len(best) && sid < bestSid) {
			best, bestSid = items, sid
		}
	}
	chapters := make([]Chapter, 0, len(best))
	for _, item := range best {
		key := fmt.Sprintf("%d:%d", item.sid, item.nid)
		chapters = append(chapters, Chapter{
			ID:             providerChapterID(sourceHeitao, sourceID, key),
			Source:         sourceHeitao,
			Title:          truncate(item.title, 128),
			CurrentEpisode: rawEpisode(item.nid),
			VideoURL:       "heitao-player://" + key,
			PageURL:        fmt.Sprintf("%s/index.php/vod/player/id/%s/nid/%d/sid/%d.html", site, sourceID, item.nid, item.sid),
			Referer:        site + "/",
		})
	}
	sortProviderChapters(chapters)
	return chapters
}

// fetchHeitaoDetail 详情页：ld+json TVSeries 元数据 + DOM 分集。
func (d *Downloader) fetchHeitaoDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	if !webProviderNumericID.MatchString(sourceID) {
		return Drama{}, nil, errors.New("黑桃视频 ID 无效")
	}
	site := heitaoSite()
	address := site + "/index.php/vod/detail/id/" + sourceID + ".html"
	document, pageURL, err := d.heitaoFetchPage(ctx, address, site+"/")
	if err != nil {
		return Drama{}, nil, err
	}
	meta := heitaoSeriesMetadata(document)
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		if heading := providerHTMLFirstClass(document, "title"); heading != nil {
			name = strings.TrimSpace(providerHTMLText(heading))
		}
	}
	if name == "" {
		return Drama{}, nil, errors.New("黑桃详情缺少剧名")
	}
	chapters := heitaoChapters(document, site, sourceID)
	if len(chapters) == 0 {
		return Drama{}, nil, errors.New("黑桃该剧暂无可播放分集")
	}
	cover := providerCoverAddress(meta.Image, pageURL)
	if cover == "" {
		if block := providerHTMLFirstClass(document, "albumDetailImg"); block != nil {
			for _, image := range providerHTMLNodes(block, func(node *html.Node) bool { return node.Data == "img" }) {
				if address := providerCoverAddress(providerHTMLAttr(image, "src"), pageURL); address != "" {
					cover = address
					break
				}
			}
		}
	}
	description := truncate(meta.Description, 12000)
	drama := Drama{
		ID: providerDramaID(sourceHeitao, sourceID), Source: sourceHeitao, SourceID: sourceID,
		Title: truncate(name, 512), Name: truncate(name, 512),
		Desc: description, Intro: description,
		Cover: cover, CoverURL: cover, ChannelName: "黑桃",
		CategoryName: firstNonEmpty(heitaoCategoryName(document), "短剧"),
		Tags:         heitaoTags(document),
		TotalEpisode: len(chapters), EpisodeCount: len(chapters),
	}
	return drama, chapters, nil
}

func init() {
	registerProviderSource(ProviderDef{
		Slug:             sourceHeitao,
		DisplayName:      "黑桃",
		SearchKind:       providerSearchOnline,
		Lanes:            true,
		EnabledByDefault: true,
		Domains:          []string{"heitaodj.com"},
		Implemented:      true,
	})
}
