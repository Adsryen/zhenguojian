package core

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// 网页版分类页（/category/<route>/<theme>）是红果目前唯一能直接拿到
// 题材筛选和分页的入口：App 的分类落地页只回内容，不回筛选项。
// 这里只解析服务端渲染的海报卡片，不执行页面脚本。

type hongguoWebTheme struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type hongguoWebGenre struct {
	Route  string            `json:"route"`
	Name   string            `json:"name"`
	Themes []hongguoWebTheme `json:"themes"`
}

var hongguoWebGenres = []hongguoWebGenre{
	{
		Route: "real-drama", Name: "真人剧",
		Themes: []hongguoWebTheme{
			{Slug: "romance", Name: "爱情"},
			{Slug: "period", Name: "年代"},
			{Slug: "comeback", Name: "逆袭"},
			{Slug: "legend", Name: "传奇"},
			{Slug: "growth", Name: "成长"},
			{Slug: "family", Name: "家庭"},
			{Slug: "clan", Name: "家族"},
			{Slug: "cute-kids", Name: "萌宝"},
			{Slug: "suspense", Name: "悬疑"},
			{Slug: "thriller", Name: "惊悚"},
			{Slug: "horror", Name: "恐怖"},
			{Slug: "supernatural", Name: "志怪"},
			{Slug: "costume", Name: "古装"},
			{Slug: "fantasy", Name: "玄幻"},
			{Slug: "wonder", Name: "奇幻"},
			{Slug: "urban", Name: "都市"},
			{Slug: "youth", Name: "青春"},
			{Slug: "comedy", Name: "喜剧"},
			{Slug: "sci-fi", Name: "科幻"},
			{Slug: "disaster", Name: "灾难"},
			{Slug: "action-adventure", Name: "动作冒险"},
			{Slug: "war", Name: "战争"},
			{Slug: "variety", Name: "综艺"},
			{Slug: "drama", Name: "剧情"},
		},
	},
	{
		Route: "comic-drama", Name: "漫剧",
		Themes: []hongguoWebTheme{
			{Slug: "creative", Name: "脑洞"},
			{Slug: "fantasy", Name: "玄幻"},
			{Slug: "drama", Name: "剧情"},
			{Slug: "apocalypse", Name: "末世"},
			{Slug: "wealthy-family", Name: "豪门"},
			{Slug: "wonder", Name: "奇幻"},
			{Slug: "sci-fi", Name: "科幻"},
			{Slug: "adventure", Name: "冒险"},
		},
	},
	{
		Route: "ai-drama", Name: "AI剧",
		Themes: []hongguoWebTheme{
			{Slug: "creative", Name: "脑洞"},
			{Slug: "fantasy", Name: "玄幻"},
			{Slug: "drama", Name: "剧情"},
			{Slug: "apocalypse", Name: "末世"},
			{Slug: "wealthy-family", Name: "豪门"},
			{Slug: "wonder", Name: "奇幻"},
			{Slug: "sci-fi", Name: "科幻"},
			{Slug: "adventure", Name: "冒险"},
		},
	},
}

type hongguoWebCategoryPage struct {
	Items      []Drama
	Page       int
	HasMore    bool
	TotalPages int
}

type hongguoWebCategoryEntry struct {
	page    hongguoWebCategoryPage
	expires time.Time
}

var hongguoWebCategoryCache = struct {
	sync.Mutex
	entries map[string]hongguoWebCategoryEntry
}{entries: map[string]hongguoWebCategoryEntry{}}

func hongguoWebGenreOf(route string) (hongguoWebGenre, bool) {
	for _, genre := range hongguoWebGenres {
		if genre.Route == route {
			return genre, true
		}
	}
	return hongguoWebGenre{}, false
}

func hongguoWebThemeOf(genre hongguoWebGenre, slug string) (hongguoWebTheme, bool) {
	for _, theme := range genre.Themes {
		if theme.Slug == slug {
			return theme, true
		}
	}
	return hongguoWebTheme{}, false
}

var hongguoWebPageLink = regexp.MustCompile(`[?&]page=(\d+)`)
var hongguoWebEpisodeNumber = regexp.MustCompile(`\d+`)

func (downloader *Downloader) fetchHongguoWebCategory(ctx context.Context, route, theme string, page int) (hongguoWebCategoryPage, error) {
	genre, valid := hongguoWebGenreOf(strings.TrimSpace(route))
	if !valid {
		return hongguoWebCategoryPage{}, errors.New("分类无效")
	}
	theme = strings.TrimSpace(theme)
	if theme != "" {
		if _, valid := hongguoWebThemeOf(genre, theme); !valid {
			return hongguoWebCategoryPage{}, errors.New("题材筛选无效")
		}
	}
	if page < 1 || page > 200 {
		return hongguoWebCategoryPage{}, errors.New("分类页码无效")
	}
	key := genre.Route + "|" + theme + "|" + strconv.Itoa(page)
	hongguoWebCategoryCache.Lock()
	if entry, ok := hongguoWebCategoryCache.entries[key]; ok && time.Now().Before(entry.expires) {
		hongguoWebCategoryCache.Unlock()
		return entry.page, nil
	}
	hongguoWebCategoryCache.Unlock()

	address := hongguoBaseURL + "/category/" + genre.Route
	if theme != "" {
		address += "/" + theme
	}
	if page > 1 {
		address += "?page=" + strconv.Itoa(page)
	}
	body, err := downloader.fetchProviderText(ctx, address, hongguoBaseURL+"/category")
	if err != nil {
		return hongguoWebCategoryPage{}, err
	}
	result, err := parseHongguoWebCategory(body, genre, theme, page)
	if err != nil {
		return hongguoWebCategoryPage{}, err
	}
	result.Page = page
	if !result.HasMore && result.TotalPages < page {
		result.TotalPages = page
	}
	hongguoWebCategoryCache.Lock()
	if len(hongguoWebCategoryCache.entries) >= 96 {
		hongguoWebCategoryCache.entries = map[string]hongguoWebCategoryEntry{}
	}
	hongguoWebCategoryCache.entries[key] = hongguoWebCategoryEntry{page: result, expires: time.Now().Add(10 * time.Minute)}
	hongguoWebCategoryCache.Unlock()
	return result, nil
}

func parseHongguoWebCategory(body string, genre hongguoWebGenre, theme string, page int) (hongguoWebCategoryPage, error) {
	if len(body) > 6<<20 {
		return hongguoWebCategoryPage{}, errors.New("分类页返回内容过大")
	}
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return hongguoWebCategoryPage{}, errors.New("分类页格式异常")
	}
	anchors := providerHTMLNodes(document, func(node *html.Node) bool {
		if node.Data != "a" {
			return false
		}
		href := providerHTMLAttr(node, "href")
		return strings.HasPrefix(href, "/detail?series_id=") || strings.HasPrefix(href, "https://hongguoduanju.com/detail?series_id=")
	})
	themeName := genre.Name
	if theme != "" {
		if item, ok := hongguoWebThemeOf(genre, theme); ok {
			themeName = item.Name
		}
	}
	items := make([]Drama, 0, len(anchors))
	seen := map[string]bool{}
	for _, anchor := range anchors {
		href := providerHTMLAttr(anchor, "href")
		parsed, err := url.Parse(href)
		if err != nil {
			continue
		}
		sourceID := parsed.Query().Get("series_id")
		if !hongguoNumericID.MatchString(sourceID) || seen[sourceID] {
			continue
		}
		title, cover := "", ""
		images := providerHTMLNodes(anchor, func(node *html.Node) bool { return node.Data == "img" })
		// 懒加载卡片里第一张 <img> 是 empty_play 占位图，真正的封面在
		// 后面那张带 alt 的 <img>；不跳过占位图的话整面墙都是空白。
		for _, image := range images {
			alt := strings.TrimSpace(providerHTMLAttr(image, "alt"))
			src := strings.TrimSpace(providerHTMLAttr(image, "src"))
			if hongguoWebImagePlaceholder(image, src) {
				continue
			}
			if title == "" && alt != "" {
				title = alt
			}
			if cover == "" && alt != "" {
				cover = src
			}
		}
		if cover == "" {
			for _, image := range images {
				src := strings.TrimSpace(providerHTMLAttr(image, "src"))
				if src != "" && !hongguoWebImagePlaceholder(image, src) {
					cover = src
					break
				}
			}
		}
		if cover == "" {
			for _, source := range providerHTMLNodes(anchor, func(node *html.Node) bool { return node.Data == "source" }) {
				srcset := strings.TrimSpace(providerHTMLAttr(source, "srcset"))
				if srcset == "" {
					continue
				}
				cover = strings.TrimSpace(strings.Split(srcset, " ")[0])
				if cover != "" {
					break
				}
			}
		}
		episodeText := ""
		for _, node := range providerHTMLNodes(anchor, func(node *html.Node) bool {
			return node.Data == "p" && nodeClassContains(node, "pc-episode")
		}) {
			episodeText = strings.TrimSpace(providerHTMLText(node))
			if episodeText != "" {
				break
			}
		}
		if title == "" {
			for _, node := range providerHTMLNodes(anchor, func(node *html.Node) bool {
				return node.Data == "p" && nodeClassContains(node, "pc-title")
			}) {
				title = strings.TrimSpace(providerHTMLText(node))
				if title != "" {
					break
				}
			}
		}
		if title == "" || cover == "" {
			continue
		}
		tags := []string{}
		addTag := func(value string) {
			value = strings.TrimSpace(value)
			if value == "" {
				return
			}
			for _, existing := range tags {
				if existing == value {
					return
				}
			}
			tags = append(tags, value)
		}
		addTag(themeName)
		for _, node := range providerHTMLNodes(anchor, func(node *html.Node) bool {
			return node.Data == "span" && nodeClassContains(node, "pc-tag-text")
		}) {
			addTag(providerHTMLText(node))
		}
		count := ""
		if digits := hongguoWebEpisodeNumber.FindString(episodeText); digits != "" {
			count = digits
		}
		release := ""
		switch {
		case strings.Contains(episodeText, "更新"):
			release = "ongoing"
		case strings.HasPrefix(episodeText, "全"):
			release = "finished"
		}
		seen[sourceID] = true
		items = append(items, Drama{
			ID: providerDramaID(sourceHongguo, sourceID), Source: sourceHongguo, SourceID: sourceID,
			Title: title, Name: title, Cover: cover, CoverURL: cover,
			CategoryName: themeName, ChannelName: "红果", Remark: episodeText,
			TotalEpisode: count, EpisodeCount: count, Tags: tags,
			ReleaseStatus: release,
		})
	}
	if len(items) == 0 {
		if page > 1 {
			return hongguoWebCategoryPage{Page: page, HasMore: false, TotalPages: page}, nil
		}
		return hongguoWebCategoryPage{}, errors.New("分类页没有返回可识别的剧集")
	}
	totalPages := page
	for _, match := range hongguoWebPageLink.FindAllStringSubmatch(body, -1) {
		if value, err := strconv.Atoi(match[1]); err == nil && value > totalPages {
			totalPages = value
		}
	}
	hasMore := totalPages > page
	if totalPages == page && len(items) >= 20 {
		// 分页条没渲染出来时，满页就按还有下一页处理。
		hasMore = true
	}
	return hongguoWebCategoryPage{Items: items, Page: page, HasMore: hasMore, TotalPages: totalPages}, nil
}

func nodeClassContains(node *html.Node, fragment string) bool {
	for _, attr := range node.Attr {
		if attr.Key == "class" {
			for _, className := range strings.Fields(attr.Val) {
				if strings.Contains(className, fragment) {
					return true
				}
			}
		}
	}
	return false
}

func hongguoWebImagePlaceholder(node *html.Node, src string) bool {
	return nodeClassContains(node, "pc-img-empty") || strings.Contains(src, "empty_play")
}
